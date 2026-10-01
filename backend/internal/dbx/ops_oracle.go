package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
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
		if oracleRefused(err) {
			// An application schema: the list exists and is not this account's
			// to read. That is said, not failed.
			return nil, errSessionsRefused{reason: "This account may not read the session list (it needs SELECT on v$session and v$sql): " + err.Error()}
		}
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

// --- what an account may read --------------------------------------------------

// oracleRefused reports whether an error is Oracle refusing a view rather than
// a query going wrong. An account with no grant on a V$ or DBA view is told the
// view does not exist (ORA-00942) — the engine will not confirm that something
// it may not read is there — and one with a partial grant is told ORA-01031.
// Either is "this account may not", which a page says in a sentence.
func oracleRefused(err error) bool {
	if err == nil {
		return false
	}
	text := err.Error()
	return strings.Contains(text, "ORA-00942") || strings.Contains(text, "ORA-01031")
}

// --- locks -------------------------------------------------------------------

// oracleLockModes are the six modes a lock is held or asked for in, by the
// number v$lock reports.
var oracleLockModes = map[int]string{
	1: "null", 2: "row share", 3: "row exclusive", 4: "share", 5: "share row exclusive", 6: "exclusive",
}

// oracleLockTypes names the lock types an application takes. TX is a row lock:
// Oracle keeps no list of locked rows, a session waits on the transaction that
// holds the row.
var oracleLockTypes = map[string]string{"TX": "row (transaction)", "TM": "table", "UL": "user-defined"}

// Locks reads who waits on whom from v$session, which names each waiter's
// blocker directly, and what for from the lock the waiter has requested in
// v$lock. The object is the table the waiter's row is in, where the session
// records it. Both views need a grant an application schema does not have;
// without one the report says so and is not an error.
func (oracleDialect) Locks(ctx context.Context, db *sql.DB) (*LocksReport, error) {
	refused := func(err error) (*LocksReport, error) {
		if oracleRefused(err) {
			return &LocksReport{Waits: []LockWait{}, Locks: []HeldLock{},
				Reason: "This account may not read the lock views (it needs SELECT on v$session, v$lock and v$sql): " + err.Error()}, nil
		}
		return nil, err
	}
	// How long the blocker's transaction has been open is in v$transaction,
	// which takes a grant of its own; without it the wait is still listed.
	waits := func(blockerAge string) string {
		return `
		  SELECT TO_CHAR(w.sid) || ',' || TO_CHAR(w.serial#),
		         TO_CHAR(b.sid) || ',' || TO_CHAR(b.serial#),
		         w.username, b.username,
		         (SELECT SUBSTR(q.sql_text, 1, 2000) FROM v$sql q
		          WHERE q.sql_id = w.sql_id AND q.child_number = w.sql_child_number AND ROWNUM = 1),
		         (SELECT SUBSTR(q.sql_text, 1, 2000) FROM v$sql q
		          WHERE q.sql_id = NVL(b.sql_id, b.prev_sql_id)
		            AND q.child_number = NVL(b.sql_child_number, b.prev_child_number) AND ROWNUM = 1),
		         CASE WHEN b.status = 'ACTIVE' THEN 'active'
		              WHEN b.taddr IS NOT NULL THEN 'idle in transaction' ELSE 'idle' END,
		         (SELECT o.owner || '.' || o.object_name FROM all_objects o
		          WHERE o.object_id = w.row_wait_obj# AND ROWNUM = 1),
		         (SELECT MAX(l.type) KEEP (DENSE_RANK FIRST ORDER BY l.ctime DESC) FROM v$lock l
		          WHERE l.sid = w.sid AND l.request > 0),
		         (SELECT MAX(l.request) KEEP (DENSE_RANK FIRST ORDER BY l.ctime DESC) FROM v$lock l
		          WHERE l.sid = w.sid AND l.request > 0),
		         NVL(w.seconds_in_wait, 0),
		         ` + blockerAge + `
		  FROM v$session w
		  JOIN v$session b ON b.sid = w.blocking_session
		  WHERE w.blocking_session IS NOT NULL AND w.type = 'USER'
		  ORDER BY w.seconds_in_wait DESC
		  FETCH FIRST ` + itoa(maxHeldLocks) + ` ROWS ONLY`
	}
	rows, err := db.QueryContext(ctx, waits(`NVL((SELECT (SYSDATE - t.start_date) * 86400 FROM v$transaction t WHERE t.addr = b.taddr), 0)`))
	if oracleRefused(err) {
		rows, err = db.QueryContext(ctx, waits(`0`))
	}
	if err != nil {
		return refused(err)
	}
	out := &LocksReport{Supported: true, Waits: []LockWait{}, Locks: []HeldLock{}}
	for rows.Next() {
		var w LockWait
		var kind string
		var mode sql.NullInt64
		if err := rows.Scan(&w.WaitingPID, &w.BlockingPID, nullText{&w.WaitingUser}, nullText{&w.BlockingUser},
			nullText{&w.WaitingQuery}, nullText{&w.BlockingQuery}, nullText{&w.BlockingState}, nullText{&w.Object},
			nullText{&kind}, &mode, &w.WaitSeconds, &w.BlockingSeconds); err != nil {
			rows.Close()
			return nil, err
		}
		w.LockType = orText(oracleLockTypes[kind], kind)
		w.Mode = oracleLockModes[int(mode.Int64)]
		out.Waits = append(out.Waits, w)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	// The lock table: what user sessions hold and ask for, on tables and on
	// transactions. The system's own enqueues are left out; there are dozens
	// per session and none is what an application is stuck behind.
	rows, err = db.QueryContext(ctx, `
	  SELECT TO_CHAR(s.sid) || ',' || TO_CHAR(s.serial#), s.username, l.type,
	         CASE WHEN l.request > 0 THEN l.request ELSE l.lmode END,
	         CASE WHEN l.request > 0 THEN 0 ELSE 1 END,
	         CASE WHEN l.type = 'TM'
	              THEN (SELECT o.owner || '.' || o.object_name FROM all_objects o WHERE o.object_id = l.id1 AND ROWNUM = 1)
	              ELSE 'transaction ' || TO_CHAR(TRUNC(l.id1 / 65536)) || '.' || TO_CHAR(MOD(l.id1, 65536)) || '.' || TO_CHAR(l.id2) END,
	         LOWER(s.status),
	         (SELECT SUBSTR(q.sql_text, 1, 2000) FROM v$sql q
	          WHERE q.sql_id = NVL(s.sql_id, s.prev_sql_id)
	            AND q.child_number = NVL(s.sql_child_number, s.prev_child_number) AND ROWNUM = 1)
	  FROM v$lock l
	  JOIN v$session s ON s.sid = l.sid
	  WHERE s.type = 'USER' AND l.type IN ('TM', 'TX', 'UL')
	  ORDER BY CASE WHEN l.request > 0 THEN 0 ELSE 1 END, l.ctime DESC
	  FETCH FIRST `+itoa(maxHeldLocks+1)+` ROWS ONLY`)
	if err != nil {
		if oracleRefused(err) {
			out.Notes = append(out.Notes, "The lock table could not be read (it needs SELECT on v$lock): "+err.Error())
			return out, nil
		}
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var l HeldLock
		var kind string
		var mode sql.NullInt64
		var granted int
		if err := rows.Scan(&l.PID, nullText{&l.User}, &kind, &mode, &granted, nullText{&l.Object},
			nullText{&l.State}, nullText{&l.Query}); err != nil {
			return nil, err
		}
		if len(out.Locks) == maxHeldLocks {
			out.Truncated = true
			break
		}
		l.LockType = orText(oracleLockTypes[kind], kind)
		l.Mode = oracleLockModes[int(mode.Int64)]
		l.Granted = granted == 1
		out.Locks = append(out.Locks, l)
	}
	return out, rows.Err()
}

// --- table and index statistics ------------------------------------------------

// oracleOwner is the schema a statistics read is about: the one named, or the
// one the session is in.
func oracleOwner(ctx context.Context, db *sql.DB, schema string) (string, error) {
	if schema = strings.TrimSpace(schema); schema != "" {
		return schema, nil
	}
	err := db.QueryRowContext(ctx, `SELECT SYS_CONTEXT('USERENV', 'CURRENT_SCHEMA') FROM dual`).Scan(&schema)
	return schema, err
}

// oracleSegmentSizes reads what a schema's segments occupy, by segment name:
// tables and their partitions under "table", indexes under "index", large
// objects and their indexes under "lob".
//
// DBA_SEGMENTS covers every schema and needs a grant; USER_SEGMENTS needs none
// and covers only the account's own, which is the schema an application
// account is looking at anyway. known is false when neither could say
// anything about this schema — another account's, read without the grant.
func oracleSegmentSizes(ctx context.Context, db *sql.DB, owner string) (sizes map[string]map[string]int64, known bool, err error) {
	sizes = map[string]map[string]int64{"table": {}, "index": {}, "lob": {}}
	rows, err := db.QueryContext(ctx, `
	  SELECT segment_name, segment_type, SUM(bytes) FROM dba_segments
	  WHERE owner = :1 GROUP BY segment_name, segment_type`, owner)
	if oracleRefused(err) {
		rows, err = db.QueryContext(ctx, `
		  SELECT segment_name, segment_type, SUM(bytes) FROM user_segments
		  WHERE USER = :1 GROUP BY segment_name, segment_type`, owner)
		var self string
		if db.QueryRowContext(ctx, `SELECT USER FROM dual`).Scan(&self) == nil {
			known = self == owner
		}
	} else {
		known = err == nil
	}
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var name, kind string
		var bytes int64
		if err := rows.Scan(&name, &kind, &bytes); err != nil {
			return nil, false, err
		}
		switch {
		case strings.HasPrefix(kind, "TABLE") || kind == "NESTED TABLE":
			sizes["table"][name] += bytes
		case strings.HasPrefix(kind, "INDEX"):
			sizes["index"][name] += bytes
		case strings.HasPrefix(kind, "LOB"):
			sizes["lob"][name] += bytes
		}
	}
	return sizes, known, rows.Err()
}

// oracleBlockSizes reads the block size of each tablespace the account can
// use, for the sizes that have to be worked out from a block count.
func oracleBlockSizes(ctx context.Context, db *sql.DB) map[string]int64 {
	out := map[string]int64{}
	rows, err := db.QueryContext(ctx, `SELECT tablespace_name, block_size FROM user_tablespaces`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var size int64
		if rows.Scan(&name, &size) == nil {
			out[name] = size
		}
	}
	return out
}

// oracleDefaultBlock is the block size assumed for a tablespace the account
// cannot see: the one almost every database is created with.
const oracleDefaultBlock = 8192

// oraclePairs runs a query of two text columns for one owner and hands each
// row to fn. It is how the catalogue's side tables — which index belongs to
// which table, which large object to which — are read whole and joined here.
func oraclePairs(ctx context.Context, db *sql.DB, query, owner string, fn func(a, b string)) error {
	rows, err := db.QueryContext(ctx, query, owner)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var a, b string
		if err := rows.Scan(nullText{&a}, nullText{&b}); err != nil {
			return err
		}
		fn(a, b)
	}
	return rows.Err()
}

// TableStats reads the optimiser's own figures — rows and blocks as of the
// last time statistics were gathered — beside what each table's segments
// occupy on disk now.
//
// Oracle counts no scans per table, so those are -1. What it does count is
// the changes made to a table since its statistics were gathered, which is the
// figure that says they are stale. A table's large objects are segments of
// their own and are counted with it. A schema other than the account's own
// needs DBA_SEGMENTS for its sizes; without it the sizes are the blocks the
// statistics recorded, and a note says so.
//
// The catalogue views are each read once for the schema and joined here. In
// SQL the same join is one the optimiser plans badly — DBA_SEGMENTS against
// ALL_INDEXES took seconds on a schema of thirty tables — and the list has to
// be ordered by a size that only exists once they are joined.
func (oracleDialect) TableStats(ctx context.Context, db *sql.DB, opts StatsOptions) (*TableStatsReport, error) {
	out := &TableStatsReport{Schema: opts.Schema, Tables: []TableStat{}}
	owner, err := oracleOwner(ctx, db, opts.Schema)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `
	  SELECT owner, table_name, partitioned, NVL(num_rows, -1), last_analyzed, NVL(blocks, 0), tablespace_name
	  FROM all_tables
	  WHERE owner = :1 AND temporary = 'N' AND nested = 'NO' AND secondary = 'N' AND iot_name IS NULL
	  ORDER BY table_name
	  FETCH FIRST `+itoa(maxIndexCatalogue+1)+` ROWS ONLY`, owner)
	if err != nil {
		if oracleRefused(err) {
			out.Reason = "This account may not read the table statistics: " + err.Error()
			return out, nil
		}
		return nil, err
	}
	blocks, spaces := map[string]int64{}, map[string]string{}
	for rows.Next() {
		t := TableStat{Kind: "table", DeadRows: -1, BloatBytes: -1, SeqScans: -1, SeqRowsRead: -1, IndexScans: -1,
			IndexRowsRead: -1, Inserts: -1, Updates: -1, Deletes: -1, ModsSinceStats: -1}
		var partitioned, space string
		var analysed sql.NullTime
		var recorded int64
		if err := rows.Scan(&t.Schema, &t.Table, nullText{&partitioned}, &t.Rows, &analysed, &recorded, nullText{&space}); err != nil {
			rows.Close()
			return nil, err
		}
		if len(out.Tables) == maxIndexCatalogue {
			out.Notes = append(out.Notes, fmt.Sprintf("This schema has more than %d tables; only the first %d by name were ranked by size.", maxIndexCatalogue, maxIndexCatalogue))
			break
		}
		if strings.EqualFold(partitioned, "YES") {
			t.Kind = "partitioned table"
		}
		t.LastAnalyze = timePtr(analysed)
		blocks[t.Table], spaces[t.Table] = recorded, space
		out.Tables = append(out.Tables, t)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	sizes, known, err := oracleSegmentSizes(ctx, db, owner)
	if err != nil {
		return nil, err
	}
	// Which segment belongs to which table. Either read failing costs that
	// part of the size and nothing else.
	indexBytes, lobBytes := map[string]int64{}, map[string]int64{}
	_ = oraclePairs(ctx, db, `SELECT table_name, index_name FROM all_indexes WHERE table_owner = :1 AND index_type <> 'LOB'`, owner,
		func(table, index string) { indexBytes[table] += sizes["index"][index] })
	_ = oraclePairs(ctx, db, `SELECT table_name, segment_name FROM all_lobs WHERE owner = :1
	                          UNION ALL SELECT table_name, index_name FROM all_lobs WHERE owner = :1`, owner,
		func(table, segment string) { lobBytes[table] += sizes["lob"][segment] })
	changes := map[string]int64{}
	if rows, err := db.QueryContext(ctx, `
	  SELECT table_name, SUM(inserts + updates + deletes) FROM all_tab_modifications
	  WHERE table_owner = :1 AND partition_name IS NULL GROUP BY table_name`, owner); err == nil {
		for rows.Next() {
			var table string
			var n int64
			if rows.Scan(&table, &n) == nil {
				changes[table] = n
			}
		}
		rows.Close()
	}

	blockSize := oracleBlockSizes(ctx, db)
	estimated := false
	for i := range out.Tables {
		t := &out.Tables[i]
		if segment, ok := sizes["table"][t.Table]; ok {
			t.TableBytes = segment
		} else {
			// No segment this account can see — or none yet: Oracle makes a
			// table's segment when its first row arrives. The size the
			// statistics recorded stands in.
			size := blockSize[spaces[t.Table]]
			if size == 0 {
				size = oracleDefaultBlock
			}
			t.TableBytes = blocks[t.Table] * size
			estimated = estimated || (!known && t.TableBytes > 0)
		}
		t.IndexBytes, t.ToastBytes = indexBytes[t.Table], lobBytes[t.Table]
		t.TotalBytes = t.TableBytes + t.IndexBytes + t.ToastBytes
		if n, ok := changes[t.Table]; ok {
			t.ModsSinceStats = n
		}
	}
	sort.SliceStable(out.Tables, func(i, j int) bool { return out.Tables[i].TotalBytes > out.Tables[j].TotalBytes })
	if limit := opts.limit(); len(out.Tables) > limit+1 {
		out.Tables = out.Tables[:limit+1]
	}
	if estimated {
		out.Notes = append(out.Notes, "The segments of another schema could not be read (that needs SELECT on DBA_SEGMENTS), so each table's size is the blocks recorded when its statistics were last gathered, without its indexes or large objects.")
	}
	out.Notes = append(out.Notes, "Oracle counts no scans per table. Changes since statistics were last gathered are counted, and say when they are stale.")
	return out, nil
}

// IndexStats lists every index of the schema with its size, largest first.
//
// How often an index is used is recorded in DBA_INDEX_USAGE from 12.2 on, by
// sampling: an index with a row there has been used, and one without may only
// not have been sampled yet. So a use count is reported where there is one
// and is -1 — unknown, never zero — where there is not, and nothing here is
// called unused on the strength of a missing row.
func (oracleDialect) IndexStats(ctx context.Context, db *sql.DB, opts StatsOptions) (*IndexStatsReport, error) {
	out := &IndexStatsReport{Schema: opts.Schema, Indexes: []IndexStat{}}
	owner, err := oracleOwner(ctx, db, opts.Schema)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `
	  SELECT table_owner, table_name, index_name, LOWER(index_type), uniqueness, status, NVL(leaf_blocks, 0), tablespace_name
	  FROM all_indexes
	  WHERE table_owner = :1 AND table_name = NVL(:2, table_name) AND index_type NOT IN ('LOB', 'IOT - TOP')
	  ORDER BY table_name, index_name
	  FETCH FIRST `+itoa(maxIndexCatalogue+1)+` ROWS ONLY`, owner, oracleSchemaArg(opts.Table))
	if err != nil {
		if oracleRefused(err) {
			out.Reason = "This account may not read the index statistics: " + err.Error()
			return out, nil
		}
		return nil, err
	}
	leaves, spaces := map[string]int64{}, map[string]string{}
	for rows.Next() {
		ix := IndexStat{RowsRead: -1, Scans: -1, Columns: []string{}}
		var uniqueness, status, space string
		var leafBlocks int64
		if err := rows.Scan(&ix.Schema, &ix.Table, &ix.Name, nullText{&ix.Method}, nullText{&uniqueness}, nullText{&status},
			&leafBlocks, nullText{&space}); err != nil {
			rows.Close()
			return nil, err
		}
		if len(out.Indexes) == maxIndexCatalogue {
			out.Notes = append(out.Notes, indexCatalogueNote())
			break
		}
		ix.Unique = strings.EqualFold(uniqueness, "UNIQUE")
		// A partitioned index reports N/A here and its state per partition.
		ix.Valid = !strings.EqualFold(status, "UNUSABLE")
		// A function-based index's columns are the hidden ones Oracle made for
		// its expressions; two such indexes are not the same for having none in
		// common, and neither is covered by a wider one.
		ix.plain = !strings.Contains(ix.Method, "function-based") && !strings.Contains(ix.Method, "domain")
		if !ix.plain {
			ix.signature = "expression:" + ix.Name
		}
		leaves[ix.Name], spaces[ix.Name] = leafBlocks, space
		out.Indexes = append(out.Indexes, ix)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	sizes, _, err := oracleSegmentSizes(ctx, db, owner)
	if err != nil {
		return nil, err
	}
	columns := map[string][]string{}
	if err := oraclePairs(ctx, db, `SELECT index_name, column_name FROM all_ind_columns WHERE table_owner = :1 ORDER BY index_name, column_position`, owner,
		func(index, column string) { columns[index] = append(columns[index], column) }); err != nil {
		return nil, err
	}
	// The constraint an index enforces. A primary key wins over a unique
	// constraint on the same index, which is rare and legal.
	constraints := map[string]string{}
	_ = oraclePairs(ctx, db, `SELECT index_name, constraint_type FROM all_constraints
	                          WHERE owner = :1 AND constraint_type IN ('P', 'U') AND index_name IS NOT NULL`, owner,
		func(index, kind string) {
			if constraints[index] != "P" {
				constraints[index] = kind
			}
		})
	uses := map[string]int64{}
	if rows, err := db.QueryContext(ctx, `SELECT name, total_access_count FROM dba_index_usage WHERE owner = :1`, owner); err != nil {
		out.Notes = append(out.Notes, "How often each index is used is not shown: that needs SELECT on DBA_INDEX_USAGE.")
	} else {
		for rows.Next() {
			var name string
			var n int64
			if rows.Scan(&name, &n) == nil {
				uses[name] = n
			}
		}
		rows.Close()
		out.Notes = append(out.Notes, "Oracle samples index use, so an index with no recorded use is reported as unknown rather than unused.")
	}

	blockSize := oracleBlockSizes(ctx, db)
	for i := range out.Indexes {
		ix := &out.Indexes[i]
		if segment, ok := sizes["index"][ix.Name]; ok {
			ix.Bytes = segment
		} else {
			size := blockSize[spaces[ix.Name]]
			if size == 0 {
				size = oracleDefaultBlock
			}
			ix.Bytes = leaves[ix.Name] * size
		}
		if cols := columns[ix.Name]; cols != nil {
			ix.Columns = cols
		}
		ix.Primary = constraints[ix.Name] == "P"
		ix.Constraint = constraints[ix.Name] != ""
		if n, ok := uses[ix.Name]; ok {
			ix.Scans = n
		}
	}
	sortIndexStatsBySize(out.Indexes)
	return out, nil
}

// --- maintenance -------------------------------------------------------------

func (oracleDialect) MaintenanceActions() []MaintenanceAction {
	return []MaintenanceAction{
		{ID: "gather_stats", Label: "Gather statistics", Scope: "either",
			Description: "Refreshes the statistics the optimiser chooses plans from, for the table and its indexes or — with no table — for every table of the schema. Reads a sample and locks nothing; plans that used the old statistics are compiled again on their next use."},
	}
}

// oracleStatsName renders a name as DBMS_STATS takes one: a string, holding
// the identifier quoted so that it is matched exactly as the catalogue spells
// it. Bare, the package folds it to upper case and misses a table that was
// created with a quoted lower-case name.
func oracleStatsName(d oracleDialect, name string) (string, error) {
	quoted, err := d.QuoteIdent(name)
	if err != nil {
		return "", err
	}
	return dumpString(DriverOracle, quoted), nil
}

// oracleMaintenanceSQL renders the one block an action is.
func oracleMaintenanceSQL(d oracleDialect, req MaintenanceRequest, owner string) (string, error) {
	if req.Action != "gather_stats" {
		return "", maintenanceRefused("unknown action %q", req.Action)
	}
	if req.Index != "" {
		return "", maintenanceRefused("gather_stats takes a table; its indexes are gathered with it")
	}
	schema, err := oracleStatsName(d, owner)
	if err != nil {
		return "", err
	}
	if req.Table == "" {
		return "BEGIN DBMS_STATS.GATHER_SCHEMA_STATS(ownname => " + schema + ", cascade => TRUE); END;", nil
	}
	table, err := oracleStatsName(d, req.Table)
	if err != nil {
		return "", err
	}
	return "BEGIN DBMS_STATS.GATHER_TABLE_STATS(ownname => " + schema + ", tabname => " + table + ", cascade => TRUE); END;", nil
}

// Maintain gathers statistics and reports what the optimiser now has.
// DBMS_STATS prints nothing, so the lines returned are read from the catalogue
// afterwards: each table's rows and blocks and when it was analysed.
func (d oracleDialect) Maintain(ctx context.Context, db *sql.DB, _ string, req MaintenanceRequest) (*MaintenanceResult, error) {
	owner := req.Schema
	if owner == "" {
		if err := db.QueryRowContext(ctx, `SELECT SYS_CONTEXT('USERENV', 'CURRENT_SCHEMA') FROM dual`).Scan(&owner); err != nil {
			return nil, err
		}
	}
	stmt, err := oracleMaintenanceSQL(d, req, owner)
	if err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(ctx, stmt); err != nil {
		return nil, err
	}
	out := &MaintenanceResult{Statements: []string{stmt}, Output: []string{}, OK: true}
	rows, err := db.QueryContext(ctx, `
	  SELECT table_name, NVL(num_rows, -1), NVL(blocks, 0), TO_CHAR(last_analyzed, 'YYYY-MM-DD HH24:MI:SS')
	  FROM all_tables
	  WHERE owner = :1 AND table_name = NVL(:2, table_name) AND temporary = 'N'
	  ORDER BY table_name
	  FETCH FIRST `+itoa(maxMaintenanceLines+1)+` ROWS ONLY`, owner, oracleSchemaArg(req.Table))
	if err != nil {
		// The statistics were gathered; only the read-back was refused.
		return out, nil
	}
	defer rows.Close()
	for rows.Next() {
		var table string
		var count, blocks int64
		var analysed sql.NullString
		if rows.Scan(&table, &count, &blocks, &analysed) != nil {
			break
		}
		if !analysed.Valid {
			out.keep(table + ": no statistics")
			continue
		}
		out.keep(table + ": " + strconv.FormatInt(count, 10) + " row" + pluralS(int(count)) + ", " +
			strconv.FormatInt(blocks, 10) + " block" + pluralS(int(blocks)) + ", analysed " + analysed.String)
	}
	return out, nil
}
