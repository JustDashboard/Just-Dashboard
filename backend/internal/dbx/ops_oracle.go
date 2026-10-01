package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Oracle's answers to the operations questions, from the V$ views.
//
// An application schema is very often not granted any of them, and one that
// is granted v$session is not thereby granted v$sysstat. So nothing here is
// one query: each view is asked on its own and a refusal becomes a note the
// page prints, which tells an operator exactly which grant is missing.
// The licensed diagnostic views (AWR, ASH) are never read.

// --- snapshot ----------------------------------------------------------------

func (oracleDialect) ServerStats(ctx context.Context, db *sql.DB) (*ServerStats, error) {
	out := &ServerStats{Counters: map[string]float64{}, Gauges: map[string]float64{}, Facts: map[string]string{}}
	var schema sql.NullString
	if err := db.QueryRowContext(ctx, `
	  SELECT (SELECT banner FROM v$version WHERE ROWNUM = 1),
	         SYS_CONTEXT('USERENV', 'CURRENT_SCHEMA')
	  FROM dual`).Scan(nullText{&out.Version}, &schema); err != nil {
		return nil, err
	}
	out.At = time.Now().UTC()
	out.Database = schema.String
	note := func(what, grant string, err error) {
		out.Notes = append(out.Notes, what+" could not be read ("+grant+"): "+err.Error())
	}

	var uptime sql.NullFloat64
	var status sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT (SYSDATE - startup_time) * 86400, status FROM v$instance`).Scan(&uptime, &status); err != nil {
		note("The instance", "v$instance", err)
	} else {
		out.UptimeSeconds = uptime.Float64
		started := out.At.Add(-time.Duration(uptime.Float64 * float64(time.Second)))
		out.StartedAt = &started
		out.Facts["instanceStatus"] = status.String
	}

	out.Role = "standalone"
	var role, openMode, logMode sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT database_role, open_mode, log_mode FROM v$database`).Scan(&role, &openMode, &logMode); err != nil {
		note("The database role", "v$database", err)
	} else {
		out.Facts["databaseRole"], out.Facts["openMode"], out.Facts["logMode"] = role.String, openMode.String, logMode.String
		if strings.Contains(strings.ToUpper(role.String), "STANDBY") {
			out.Role = "replica"
		}
	}

	// The schema's own segments: the one size an ordinary account may read.
	var size sql.NullFloat64
	if err := db.QueryRowContext(ctx, `SELECT NVL(SUM(bytes), 0) FROM user_segments`).Scan(&size); err == nil {
		out.DatabaseBytes = int64(size.Float64)
	}

	rows, err := db.QueryContext(ctx, `
	  SELECT name, value FROM v$sysstat
	  WHERE name IN ('user commits', 'user rollbacks', 'execute count', 'user calls', 'physical reads',
	                 'session logical reads', 'table scan rows gotten', 'table fetch by rowid',
	                 'sorts (disk)', 'enqueue deadlocks', 'redo size', 'parse count (hard)',
	                 'logons cumulative', 'bytes sent via SQL*Net to client',
	                 'bytes received via SQL*Net from client')`)
	if err != nil {
		note("The system statistics", "v$sysstat", err)
	} else {
		stat := map[string]float64{}
		for rows.Next() {
			var name string
			var value float64
			if err := rows.Scan(&name, &value); err != nil {
				rows.Close()
				return nil, err
			}
			stat[name] = value
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		for key, name := range map[string]string{
			StatTransactionsCommitted: "user commits", StatTransactionsRolledBack: "user rollbacks",
			StatQueries: "execute count", "userCalls": "user calls", StatBlocksRead: "physical reads",
			StatDeadlocks: "enqueue deadlocks", "redoBytes": "redo size", "diskSorts": "sorts (disk)",
			"hardParses": "parse count (hard)", "logons": "logons cumulative",
			"bytesSent": "bytes sent via SQL*Net to client", "bytesReceived": "bytes received via SQL*Net from client",
		} {
			if v, ok := stat[name]; ok {
				out.Counters[key] = v
			}
		}
		if logical, ok := stat["session logical reads"]; ok {
			out.Counters[StatBlocksHit] = max0(logical - stat["physical reads"])
		}
		if _, ok := stat["table scan rows gotten"]; ok {
			out.Counters[StatRowsRead] = stat["table scan rows gotten"] + stat["table fetch by rowid"]
		}
	}

	conns := &ConnectionCounts{}
	var limit sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT value FROM v$parameter WHERE name = 'sessions'`).Scan(&limit); err == nil {
		conns.Max = atoiOr(limit.String, 0)
	}
	rows, err = db.QueryContext(ctx, `
	  SELECT status, COUNT(*),
	         SUM(CASE WHEN blocking_session IS NOT NULL THEN 1 ELSE 0 END),
	         SUM(CASE WHEN status = 'INACTIVE' AND taddr IS NOT NULL THEN 1 ELSE 0 END)
	  FROM v$session WHERE type = 'USER' GROUP BY status`)
	if err != nil {
		note("Sessions", "v$session", err)
		return out, nil
	}
	defer rows.Close()
	for rows.Next() {
		var state sql.NullString
		var n, blocked, idleInTx int
		if err := rows.Scan(&state, &n, &blocked, &idleInTx); err != nil {
			return nil, err
		}
		conns.Total += n
		conns.Waiting += blocked
		if strings.EqualFold(state.String, "ACTIVE") {
			conns.Active += n
		} else {
			conns.IdleInTransaction += idleInTx
			conns.Idle += n - idleInTx
		}
	}
	out.Connections = conns
	return out, rows.Err()
}

// --- sessions ----------------------------------------------------------------

// oracleSessionsSQL lists user sessions. The blocker is rendered as the same
// "sid,serial#" pair a session's own handle is: Activity reported the bare
// sid, which matched no row's handle and could not be passed to a kill.
// The cursor is joined on its child number as well as its id — one sql_id has
// a child per plan, and joining on the id alone listed the session once per
// child. withTx adds how long the session's transaction has been open, which
// needs a grant on v$transaction of its own.
func oracleSessionsSQL(withTx bool) string {
	tx := "CASE WHEN s.taddr IS NOT NULL THEN 0 ELSE -1 END"
	if withTx {
		tx = "NVL((SELECT (SYSDATE - t.start_date) * 86400 FROM v$transaction t WHERE t.addr = s.taddr), -1)"
	}
	return `
	  SELECT TO_CHAR(s.sid) || ',' || TO_CHAR(s.serial#),
	         s.username,
	         SYS_CONTEXT('USERENV', 'DB_NAME'),
	         s.status,
	         NVL(s.last_call_et, 0),
	         SUBSTR(q.sql_text, 1, 4000),
	         s.machine,
	         s.program,
	         CASE WHEN s.state = 'WAITING' AND s.wait_class <> 'Idle' THEN s.wait_class END,
	         CASE WHEN s.state = 'WAITING' AND s.wait_class <> 'Idle' THEN s.event END,
	         CASE WHEN s.blocking_session IS NOT NULL THEN
	           (SELECT TO_CHAR(b.sid) || ',' || TO_CHAR(b.serial#) FROM v$session b
	            WHERE b.sid = s.blocking_session AND ROWNUM = 1) END,
	         CASE WHEN s.taddr IS NOT NULL THEN 1 ELSE 0 END,
	         ` + tx + `,
	         CASE WHEN s.sid = SYS_CONTEXT('USERENV', 'SID') THEN 1 ELSE 0 END
	  FROM v$session s
	  LEFT JOIN v$sql q ON q.sql_id = s.sql_id AND q.child_number = s.sql_child_number
	  WHERE s.type = 'USER'
	  ORDER BY s.last_call_et DESC`
}

func (oracleDialect) Sessions(ctx context.Context, db *sql.DB) ([]Activity, error) {
	rows, err := db.QueryContext(ctx, oracleSessionsSQL(true))
	if err != nil {
		rows, err = db.QueryContext(ctx, oracleSessionsSQL(false))
		if err != nil {
			return nil, err
		}
	}
	defer rows.Close()
	now := time.Now().UTC()
	out := []Activity{}
	for rows.Next() {
		var a Activity
		var elapsed, txSeconds float64
		var blocker string
		var inTx, self int
		if err := rows.Scan(&a.PID, nullText{&a.User}, nullText{&a.Database}, nullText{&a.State}, &elapsed,
			nullText{&a.Query}, nullText{&a.Client}, nullText{&a.Application}, nullText{&a.WaitType},
			nullText{&a.WaitEvent}, nullText{&blocker}, &inTx, &txSeconds, &self); err != nil {
			return nil, err
		}
		a.Self = self != 0
		a.Wait = a.WaitEvent
		if blocker != "" {
			// One handle, with a comma inside it: the list form is the only
			// one that can carry it.
			a.BlockedBy = blocker
			a.BlockedByPIDs = []string{blocker}
		}
		// last_call_et is how long the session has been in its current
		// status, whichever that is.
		since := now.Add(-time.Duration(elapsed * float64(time.Second)))
		a.StateSince = &since
		switch {
		case strings.EqualFold(a.State, "ACTIVE"):
			a.Status = SessionActive
			a.Seconds = elapsed
			a.QueryStart = &since
		case inTx == 1:
			a.Status = SessionIdleInTransaction
			a.IdleSeconds = elapsed
		default:
			a.Status = SessionIdle
			a.IdleSeconds = elapsed
		}
		if txSeconds > 0 {
			a.TransactionSeconds = txSeconds
			start := now.Add(-time.Duration(txSeconds * float64(time.Second)))
			a.TransactionStart = &start
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Cancel stops the session's current statement and leaves it connected,
// which Oracle has offered since 18c. The handle is the "sid,serial#" pair
// the session list reports, checked half by half as Kill checks it.
func (oracleDialect) Cancel(ctx context.Context, db *sql.DB, pid string) error {
	parts := strings.Split(pid, ",")
	if len(parts) != 2 {
		return fmt.Errorf("an Oracle session id is \"sid,serial#\"")
	}
	for _, part := range parts {
		if err := validatePID(part); err != nil {
			return err
		}
	}
	_, err := db.ExecContext(ctx, "ALTER SYSTEM CANCEL SQL '"+parts[0]+", "+parts[1]+"'")
	return err
}
