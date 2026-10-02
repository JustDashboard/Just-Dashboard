package dbx

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

// MySQL's and MariaDB's answers to the operations questions.
//
// One dialect serves both, and they have drifted: MariaDB kept the InnoDB
// lock tables MySQL 8 moved into performance_schema, MySQL renamed half its
// replication statements and MariaDB kept the old names, and MariaDB ships
// with performance_schema off. Wherever they differ the newer spelling is
// tried first and the older one second, by asking rather than by parsing the
// version string — a fork that reports itself as 8.0 answers for itself.

// mysqlFirstRows runs the first of several statements the server accepts and
// returns its rows by column name. It is how one question is put to two
// engines that spell it differently.
//
// When none is accepted, the error worth reporting is the one that says the
// account may not ask, if any did: on MySQL 8.4 the older spelling is a syntax
// error or a table that is not there, and reporting whichever came last told
// an operator whose account lacks REPLICATION CLIENT that the server does not
// know SLAVE STATUS.
func mysqlFirstRows(ctx context.Context, db *sql.DB, statements ...string) ([]map[string]string, error) {
	var refused mysqlRefusals
	for _, stmt := range statements {
		rows, err := db.QueryContext(ctx, stmt)
		if err != nil {
			refused.add(err)
			continue
		}
		return rowMaps(rows)
	}
	return nil, refused.err()
}

// mysqlRefusals collects what a server answered to several spellings of one
// question, and picks the answer to report: the first that denied access,
// which names the privilege that is missing, and otherwise the last.
type mysqlRefusals struct{ denied, last error }

func (r *mysqlRefusals) add(err error) {
	if r.denied == nil && mysqlAccessDenied(err) {
		r.denied = err
	}
	r.last = err
}

func (r *mysqlRefusals) err() error {
	if r.denied != nil {
		return r.denied
	}
	return r.last
}

// mysqlAccessDenied reports whether the server refused a statement for want
// of a privilege rather than for what the statement was: a database, a table
// or a column the account may not read, or a privilege such as PROCESS the
// statement needs.
func mysqlAccessDenied(err error) bool {
	var refused *mysql.MySQLError
	if !errors.As(err, &refused) {
		return false
	}
	switch refused.Number {
	case 1044, 1045, 1142, 1143, 1227, 1370:
		return true
	}
	return false
}

// mysqlPerformanceSchemaGap says why performance_schema gave no counts, given
// the error its read ended in, or nil when it answered with no rows: the
// account may not read it, the server runs with it off, or neither.
func mysqlPerformanceSchemaGap(ctx context.Context, db *sql.DB, err error) string {
	if mysqlAccessDenied(err) {
		return "this account may not read performance_schema (" + err.Error() + ")"
	}
	var on sql.NullBool
	if db.QueryRowContext(ctx, `SELECT @@performance_schema`).Scan(&on) == nil && on.Valid && !on.Bool {
		return "performance_schema is off on this server"
	}
	if err != nil {
		return "performance_schema could not be read (" + err.Error() + ")"
	}
	return "performance_schema holds no counts for them; its table instruments may be switched off"
}

// mysqlIsMariaDB asks the server which of the two it is. Only the handful of
// places where no statement works on both need to know.
func mysqlIsMariaDB(ctx context.Context, db *sql.DB) bool {
	var version string
	if err := db.QueryRowContext(ctx, `SELECT VERSION()`).Scan(&version); err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(version), "mariadb")
}

// --- snapshot ----------------------------------------------------------------

// mysqlStatusNames is what the snapshot reads out of SHOW GLOBAL STATUS: a
// few dozen of its five hundred rows. The page charts these and no others, and
// asking for the rest every few seconds would be most of the cost of asking.
var mysqlStatusNames = []string{
	"Uptime", "Questions", "Queries", "Connections", "Threads_connected", "Threads_running",
	"Max_used_connections", "Aborted_connects", "Aborted_clients",
	"Com_commit", "Com_rollback", "Com_select", "Com_insert", "Com_update", "Com_delete",
	"Handler_commit", "Handler_rollback",
	"Handler_read_first", "Handler_read_key", "Handler_read_next", "Handler_read_prev",
	"Handler_read_rnd", "Handler_read_rnd_next", "Handler_write", "Handler_update", "Handler_delete",
	"Innodb_buffer_pool_read_requests", "Innodb_buffer_pool_reads",
	"Innodb_buffer_pool_pages_total", "Innodb_buffer_pool_pages_free", "Innodb_buffer_pool_pages_dirty",
	"Innodb_buffer_pool_bytes_data", "Innodb_row_lock_waits", "Innodb_row_lock_time", "Innodb_deadlocks",
	"Innodb_data_reads", "Innodb_data_writes", "Innodb_data_fsyncs",
	"Created_tmp_tables", "Created_tmp_disk_tables", "Created_tmp_files",
	"Slow_queries", "Select_full_join", "Select_scan", "Table_locks_waited",
	"Bytes_received", "Bytes_sent", "Open_tables", "Opened_tables",
}

func (mysqlDialect) ServerStats(ctx context.Context, db *sql.DB) (*ServerStats, error) {
	out := &ServerStats{Counters: map[string]float64{}, Gauges: map[string]float64{}, Facts: map[string]string{}}
	var comment sql.NullString
	var database sql.NullString
	var limit int
	var readOnly sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT VERSION(), @@version_comment, DATABASE(), @@max_connections, @@read_only`).
		Scan(&out.Version, &comment, &database, &limit, &readOnly); err != nil {
		return nil, err
	}
	out.Database = database.String
	if comment.Valid {
		out.Facts["versionComment"] = comment.String
	}
	out.Facts["readOnly"] = readOnly.String

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(mysqlStatusNames)), ",")
	args := make([]any, len(mysqlStatusNames))
	for i, n := range mysqlStatusNames {
		args[i] = n
	}
	rows, err := db.QueryContext(ctx, `SHOW GLOBAL STATUS WHERE Variable_name IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, err
	}
	status := map[string]float64{}
	for rows.Next() {
		var name string
		var value sql.NullString
		if err := rows.Scan(&name, &value); err != nil {
			rows.Close()
			return nil, err
		}
		if n, err := strconv.ParseFloat(value.String, 64); err == nil {
			status[name] = n
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	out.At = time.Now().UTC()
	if up, ok := status["Uptime"]; ok {
		out.UptimeSeconds = up
		started := out.At.Add(-time.Duration(up) * time.Second)
		out.StartedAt = &started
	}

	counter := func(key string, names ...string) {
		total, found := 0.0, false
		for _, n := range names {
			if v, ok := status[n]; ok {
				total, found = total+v, true
			}
		}
		if found {
			out.Counters[key] = total
		}
	}
	gauge := func(key, name string) {
		if v, ok := status[name]; ok {
			out.Gauges[key] = v
		}
	}
	// The handler counters, not Com_commit: a statement run with autocommit
	// on commits without a COMMIT statement and Com_commit never sees it.
	counter(StatTransactionsCommitted, "Handler_commit")
	counter(StatTransactionsRolledBack, "Handler_rollback")
	counter(StatRowsRead, "Handler_read_first", "Handler_read_key", "Handler_read_next",
		"Handler_read_prev", "Handler_read_rnd", "Handler_read_rnd_next")
	counter(StatRowsWritten, "Handler_write", "Handler_update", "Handler_delete")
	counter(StatQueries, "Questions")
	counter(StatTempFiles, "Created_tmp_files")
	counter(StatBlocksRead, "Innodb_buffer_pool_reads")
	if requests, ok := status["Innodb_buffer_pool_read_requests"]; ok {
		// Read requests count every logical read; reads counts the ones that
		// missed the buffer pool. The difference is the hits.
		out.Counters[StatBlocksHit] = math.Max(requests-status["Innodb_buffer_pool_reads"], 0)
	}
	counter("statementsCommitted", "Com_commit")
	counter("statementsRolledBack", "Com_rollback")
	counter("selects", "Com_select")
	counter("inserts", "Com_insert")
	counter("updates", "Com_update")
	counter("deletes", "Com_delete")
	counter("connectionsAccepted", "Connections")
	counter("abortedConnects", "Aborted_connects")
	counter("abortedClients", "Aborted_clients")
	counter("rowLockWaits", "Innodb_row_lock_waits")
	counter("rowLockMs", "Innodb_row_lock_time")
	counter("tempTables", "Created_tmp_tables")
	counter("tempDiskTables", "Created_tmp_disk_tables")
	counter("slowQueries", "Slow_queries")
	counter("fullJoins", "Select_full_join")
	counter("fullScans", "Select_scan")
	counter("tableLockWaits", "Table_locks_waited")
	counter("bytesReceived", "Bytes_received")
	counter("bytesSent", "Bytes_sent")
	counter("dataReads", "Innodb_data_reads")
	counter("dataWrites", "Innodb_data_writes")
	counter("dataFsyncs", "Innodb_data_fsyncs")
	counter("tablesOpened", "Opened_tables")
	gauge("threadsRunning", "Threads_running")
	gauge("maxUsedConnections", "Max_used_connections")
	gauge("bufferPoolPages", "Innodb_buffer_pool_pages_total")
	gauge("bufferPoolPagesFree", "Innodb_buffer_pool_pages_free")
	gauge("bufferPoolPagesDirty", "Innodb_buffer_pool_pages_dirty")
	gauge("bufferPoolBytes", "Innodb_buffer_pool_bytes_data")
	gauge("openTables", "Open_tables")

	// MariaDB keeps a deadlock counter in the status list; MySQL keeps it in
	// the InnoDB metrics table.
	if v, ok := status["Innodb_deadlocks"]; ok {
		out.Counters[StatDeadlocks] = v
	} else {
		var deadlocks float64
		if err := db.QueryRowContext(ctx, `SELECT COUNT FROM information_schema.INNODB_METRICS WHERE NAME = 'lock_deadlocks'`).Scan(&deadlocks); err == nil {
			out.Counters[StatDeadlocks] = deadlocks
		}
	}

	var size sql.NullFloat64
	sizeQuery := `SELECT SUM(DATA_LENGTH + INDEX_LENGTH) FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE()`
	if out.Database == "" {
		// No default database: the connection sees the whole server, so that
		// is what is measured.
		sizeQuery = `SELECT SUM(DATA_LENGTH + INDEX_LENGTH) FROM information_schema.TABLES
		             WHERE TABLE_SCHEMA NOT IN ('mysql', 'information_schema', 'performance_schema', 'sys')`
	}
	if err := db.QueryRowContext(ctx, sizeQuery).Scan(&size); err != nil {
		out.Notes = append(out.Notes, "The database size could not be read: "+err.Error())
	}
	out.DatabaseBytes = int64(size.Float64)

	conns := &ConnectionCounts{Max: limit}
	ownOnly, err := mysqlProcessList(ctx, db, `
	  SELECT COALESCE(p.COMMAND, ''), COALESCE(p.USER, ''), COALESCE(p.STATE, ''), t.trx_id IS NOT NULL
	  FROM information_schema.PROCESSLIST p
	  LEFT JOIN information_schema.INNODB_TRX t ON t.trx_mysql_thread_id = p.ID`, `
	  SELECT COALESCE(COMMAND, ''), COALESCE(USER, ''), COALESCE(STATE, ''), 0 FROM information_schema.PROCESSLIST`,
		func() { *conns = ConnectionCounts{Max: limit} },
		func(rows *sql.Rows) error {
			var command, user, state string
			var inTx bool
			if err := rows.Scan(&command, &user, &state, &inTx); err != nil {
				return err
			}
			switch mysqlSessionStatus(command, user, inTx) {
			case SessionBackground:
				return nil
			case SessionIdle:
				conns.Idle++
			case SessionIdleInTransaction:
				conns.IdleInTransaction++
			default:
				conns.Active++
				if strings.Contains(strings.ToLower(state), "lock") {
					conns.Waiting++
				}
			}
			conns.Total++
			return nil
		})
	switch connected, counted := status["Threads_connected"]; {
	case err != nil:
		out.Notes = append(out.Notes, "Sessions could not be counted: "+err.Error())
	case ownOnly && counted:
		// The list held this account's own sessions and nobody else's, and a
		// count of those is not the server's. The server's own two figures
		// need no privilege: how many clients are connected, and how many of
		// them are not asleep.
		running := math.Min(status["Threads_running"], connected)
		out.Connections = &ConnectionCounts{Max: limit, Total: int(connected), Active: int(running), Idle: int(connected - running)}
		out.Notes = append(out.Notes, mysqlOwnSessionsNote+
			" The sessions here are the server's own totals: which of them are inside a transaction or waiting on a lock is not known.")
	case ownOnly:
		out.Notes = append(out.Notes, mysqlOwnSessionsNote+" The sessions are not counted.")
	default:
		out.Connections = conns
	}

	// Which side of replication this is. Both statements need a replication
	// privilege; without it the role is left unsaid rather than guessed.
	if sources, err := mysqlFirstRows(ctx, db, "SHOW REPLICA STATUS", "SHOW SLAVE STATUS"); err == nil {
		switch {
		case len(sources) > 0:
			out.Role = "replica"
			if lag, err := strconv.ParseFloat(firstOf(sources[0], "Seconds_Behind_Source", "Seconds_Behind_Master"), 64); err == nil {
				out.Gauges["replicationLagSeconds"] = lag
			}
		default:
			out.Role = "standalone"
			if replicas, err := mysqlFirstRows(ctx, db, "SHOW REPLICAS", "SHOW SLAVE HOSTS"); err == nil && len(replicas) > 0 {
				out.Role = "primary"
				out.Gauges["replicas"] = float64(len(replicas))
			}
		}
	}
	return out, nil
}

// mysqlOwnSessionsNote says why a reading of the sessions is partial.
const mysqlOwnSessionsNote = "This account lacks the PROCESS privilege, so the process list shows only its own sessions."

// mysqlProcessList reads the process list joined to the InnoDB transactions,
// and without them where that join is refused: each row of whichever answered
// is handed to row. ownOnly reports that it was refused for want of a
// privilege, which is the PROCESS privilege — and without it the list that
// did answer holds this account's sessions and nobody else's.
//
// The refusal does not always arrive where a statement's errors do. MariaDB
// refuses the statement; MySQL 8 accepts it, sends the columns, and refuses
// on the first row, which reaches database/sql as a result that simply ends.
// Read with Close alone that was an empty list and no error: a server with
// forty clients reported none connected, and a restricted account was shown
// no sessions where it has its own. So the result is read to its end and
// asked how it ended. reset undoes what row made of a result that then
// failed.
func mysqlProcessList(ctx context.Context, db *sql.DB, joined, plain string, reset func(), row func(*sql.Rows) error) (ownOnly bool, err error) {
	read := func(query string) error {
		rows, err := db.QueryContext(ctx, query)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			if err := row(rows); err != nil {
				return err
			}
		}
		return rows.Err()
	}
	refused := read(joined)
	if refused == nil {
		return false, nil
	}
	reset()
	return mysqlAccessDenied(refused), read(plain)
}

// mysqlSessionStatus puts a process-list row into the shared vocabulary. The
// threads the server runs for itself are named: the event scheduler has been
// "running" since the server started and is not a slow query.
func mysqlSessionStatus(command, user string, inTransaction bool) string {
	switch strings.ToLower(command) {
	case "sleep":
		if inTransaction {
			return SessionIdleInTransaction
		}
		return SessionIdle
	case "daemon", "binlog dump", "binlog dump gtid", "slave_io", "slave_sql", "slave_worker", "connect":
		return SessionBackground
	}
	switch strings.ToLower(user) {
	case "event_scheduler", "system user":
		return SessionBackground
	}
	return SessionActive
}

// --- sessions ----------------------------------------------------------------

// mysqlBlockers maps a waiting connection id to the connection ids holding
// the row locks it wants. MySQL 8 keeps the waits in performance_schema;
// MariaDB and MySQL 5.7 keep them in information_schema. A server that
// refuses both — an account without PROCESS — has no blocking graph to show.
func mysqlBlockers(ctx context.Context, db *sql.DB) map[string][]string {
	out := map[string][]string{}
	for _, q := range []string{
		`SELECT CAST(r.trx_mysql_thread_id AS CHAR), CAST(b.trx_mysql_thread_id AS CHAR)
		 FROM performance_schema.data_lock_waits w
		 JOIN information_schema.INNODB_TRX r ON r.trx_id = w.REQUESTING_ENGINE_TRANSACTION_ID
		 JOIN information_schema.INNODB_TRX b ON b.trx_id = w.BLOCKING_ENGINE_TRANSACTION_ID`,
		`SELECT CAST(r.trx_mysql_thread_id AS CHAR), CAST(b.trx_mysql_thread_id AS CHAR)
		 FROM information_schema.INNODB_LOCK_WAITS w
		 JOIN information_schema.INNODB_TRX r ON r.trx_id = w.requesting_trx_id
		 JOIN information_schema.INNODB_TRX b ON b.trx_id = w.blocking_trx_id`,
	} {
		rows, err := db.QueryContext(ctx, q)
		if err != nil {
			continue
		}
		for rows.Next() {
			var waiting, blocking string
			if rows.Scan(&waiting, &blocking) == nil && !containsString(out[waiting], blocking) {
				out[waiting] = append(out[waiting], blocking)
			}
		}
		rows.Close()
		return out
	}
	return out
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Sessions reads the process list beside the InnoDB transactions, which is
// what tells a sleeping connection from one sleeping inside a transaction —
// the difference between a pooled connection and a held lock.
func (mysqlDialect) Sessions(ctx context.Context, db *sql.DB) ([]Activity, error) {
	now := time.Now().UTC()
	out := []Activity{}
	_, err := mysqlProcessList(ctx, db, `
	  SELECT CAST(p.ID AS CHAR), COALESCE(p.USER, ''), COALESCE(p.DB, ''), COALESCE(p.COMMAND, ''),
	         COALESCE(p.TIME, 0), COALESCE(p.INFO, ''), COALESCE(p.HOST, ''), COALESCE(p.STATE, ''),
	         CASE WHEN t.trx_id IS NULL THEN -1 ELSE COALESCE(TIMESTAMPDIFF(SECOND, t.trx_started, NOW()), 0) END,
	         CASE WHEN p.ID = CONNECTION_ID() THEN 1 ELSE 0 END
	  FROM information_schema.PROCESSLIST p
	  LEFT JOIN information_schema.INNODB_TRX t ON t.trx_mysql_thread_id = p.ID
	  ORDER BY p.TIME DESC`, `
	  SELECT CAST(ID AS CHAR), COALESCE(USER, ''), COALESCE(DB, ''), COALESCE(COMMAND, ''),
	         COALESCE(TIME, 0), COALESCE(INFO, ''), COALESCE(HOST, ''), COALESCE(STATE, ''),
	         -1, CASE WHEN ID = CONNECTION_ID() THEN 1 ELSE 0 END
	  FROM information_schema.PROCESSLIST
	  ORDER BY TIME DESC`, func() { out = out[:0] }, func(rows *sql.Rows) error {
		var a Activity
		var elapsed, txSeconds float64
		var self int
		if err := rows.Scan(&a.PID, &a.User, &a.Database, &a.State, &elapsed, &a.Query, &a.Client, &a.Wait, &txSeconds, &self); err != nil {
			return err
		}
		a.Self = self != 0
		a.Status = mysqlSessionStatus(a.State, a.User, txSeconds >= 0)
		// TIME is how long the thread has been in its current state, and the
		// server keeps no timestamp for it; the start is this clock minus it.
		since := now.Add(-time.Duration(elapsed * float64(time.Second)))
		a.StateSince = &since
		switch a.Status {
		case SessionActive:
			a.Seconds = elapsed
			a.QueryStart = &since
		case SessionIdle, SessionIdleInTransaction:
			a.IdleSeconds = elapsed
		}
		if txSeconds >= 0 {
			a.TransactionSeconds = txSeconds
			start := now.Add(-time.Duration(txSeconds * float64(time.Second)))
			a.TransactionStart = &start
		}
		out = append(out, a)
		return nil
	})
	if err != nil {
		return nil, err
	}

	blockers := mysqlBlockers(ctx, db)
	// The program name a client declared, where the server kept it.
	programs := map[string]string{}
	if prows, err := db.QueryContext(ctx, `
	  SELECT CAST(PROCESSLIST_ID AS CHAR), COALESCE(ATTR_VALUE, '')
	  FROM performance_schema.session_connect_attrs WHERE ATTR_NAME = 'program_name'`); err == nil {
		for prows.Next() {
			var id, name string
			if prows.Scan(&id, &name) == nil {
				programs[id] = name
			}
		}
		prows.Close()
	}
	for i := range out {
		out[i].Application = programs[out[i].PID]
		if b := blockers[out[i].PID]; len(b) > 0 {
			out[i].BlockedByPIDs = b
			out[i].BlockedBy = strings.Join(b, ",")
		}
	}
	return out, nil
}

// Cancel is KILL QUERY: the statement stops and the connection stays, which
// is the half of KILL the plain form does not offer.
func (mysqlDialect) Cancel(ctx context.Context, db *sql.DB, pid string) error {
	if err := validatePID(pid); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, "KILL QUERY "+pid)
	return err
}

// --- locks -------------------------------------------------------------------

func (mysqlDialect) Locks(ctx context.Context, db *sql.DB) (*LocksReport, error) {
	out := &LocksReport{Supported: true, Waits: []LockWait{}, Locks: []HeldLock{}}
	type waitQuery struct {
		sql   string
		split bool // the lock names its table as one quoted `schema`.`table`
	}
	queries := []waitQuery{
		{sql: `
		  SELECT CAST(r.trx_mysql_thread_id AS CHAR), CAST(b.trx_mysql_thread_id AS CHAR),
		         CONCAT_WS('.', rl.OBJECT_SCHEMA, rl.OBJECT_NAME), COALESCE(rl.INDEX_NAME, ''),
		         COALESCE(rl.LOCK_TYPE, ''), COALESCE(rl.LOCK_MODE, ''),
		         COALESCE(TIMESTAMPDIFF(SECOND, r.trx_wait_started, NOW()), 0),
		         COALESCE(LEFT(r.trx_query, 2000), ''), COALESCE(LEFT(b.trx_query, 2000), ''),
		         COALESCE(b.trx_state, ''), COALESCE(TIMESTAMPDIFF(SECOND, b.trx_started, NOW()), 0)
		  FROM performance_schema.data_lock_waits w
		  JOIN information_schema.INNODB_TRX r ON r.trx_id = w.REQUESTING_ENGINE_TRANSACTION_ID
		  JOIN information_schema.INNODB_TRX b ON b.trx_id = w.BLOCKING_ENGINE_TRANSACTION_ID
		  LEFT JOIN performance_schema.data_locks rl ON rl.ENGINE_LOCK_ID = w.REQUESTING_ENGINE_LOCK_ID
		  ORDER BY 7 DESC LIMIT ` + itoa(maxHeldLocks)},
		{split: true, sql: `
		  SELECT CAST(r.trx_mysql_thread_id AS CHAR), CAST(b.trx_mysql_thread_id AS CHAR),
		         COALESCE(rl.lock_table, ''), COALESCE(rl.lock_index, ''),
		         COALESCE(rl.lock_type, ''), COALESCE(rl.lock_mode, ''),
		         COALESCE(TIMESTAMPDIFF(SECOND, r.trx_wait_started, NOW()), 0),
		         COALESCE(LEFT(r.trx_query, 2000), ''), COALESCE(LEFT(b.trx_query, 2000), ''),
		         COALESCE(b.trx_state, ''), COALESCE(TIMESTAMPDIFF(SECOND, b.trx_started, NOW()), 0)
		  FROM information_schema.INNODB_LOCK_WAITS w
		  JOIN information_schema.INNODB_TRX r ON r.trx_id = w.requesting_trx_id
		  JOIN information_schema.INNODB_TRX b ON b.trx_id = w.blocking_trx_id
		  LEFT JOIN information_schema.INNODB_LOCKS rl ON rl.lock_id = w.requested_lock_id
		  ORDER BY 7 DESC LIMIT ` + itoa(maxHeldLocks)},
	}
	var refused mysqlRefusals
	read := false
	for _, q := range queries {
		rows, err := db.QueryContext(ctx, q.sql)
		if err != nil {
			refused.add(err)
			continue
		}
		for rows.Next() {
			var w LockWait
			var index string
			if err := rows.Scan(&w.WaitingPID, &w.BlockingPID, &w.Object, &index, &w.LockType, &w.Mode,
				&w.WaitSeconds, &w.WaitingQuery, &w.BlockingQuery, &w.BlockingState, &w.BlockingSeconds); err != nil {
				rows.Close()
				return nil, err
			}
			if q.split {
				w.Object = strings.ReplaceAll(w.Object, "`", "")
			}
			if index != "" {
				w.Object += " (index " + index + ")"
			}
			out.Waits = append(out.Waits, w)
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		read = true
		break
	}
	if !read {
		// Neither table answered: an account without PROCESS, or an engine
		// on this wire that has no InnoDB.
		return &LocksReport{Waits: []LockWait{}, Locks: []HeldLock{},
			Reason: "The InnoDB lock tables could not be read: " + refused.err().Error()}, nil
	}
	users := map[string]string{}
	if rows, err := db.QueryContext(ctx, `SELECT CAST(ID AS CHAR), COALESCE(USER, '') FROM information_schema.PROCESSLIST`); err == nil {
		for rows.Next() {
			var id, user string
			if rows.Scan(&id, &user) == nil {
				users[id] = user
			}
		}
		rows.Close()
	}
	for i := range out.Waits {
		out.Waits[i].WaitingUser = users[out.Waits[i].WaitingPID]
		out.Waits[i].BlockingUser = users[out.Waits[i].BlockingPID]
	}

	// The lock table itself, where the engine keeps one. MariaDB lists only
	// the locks involved in a wait, which is the same rows as above and is
	// not repeated.
	rows, err := db.QueryContext(ctx, `
	  SELECT CAST(COALESCE(t.trx_mysql_thread_id, 0) AS CHAR),
	         CONCAT_WS('.', l.OBJECT_SCHEMA, l.OBJECT_NAME), COALESCE(l.INDEX_NAME, ''),
	         COALESCE(l.LOCK_TYPE, ''), COALESCE(l.LOCK_MODE, ''), l.LOCK_STATUS = 'GRANTED',
	         COALESCE(t.trx_state, ''), COALESCE(LEFT(t.trx_query, 500), '')
	  FROM performance_schema.data_locks l
	  LEFT JOIN information_schema.INNODB_TRX t ON t.trx_id = l.ENGINE_TRANSACTION_ID
	  ORDER BY l.LOCK_STATUS = 'GRANTED', t.trx_mysql_thread_id
	  LIMIT `+itoa(maxHeldLocks+1))
	if err != nil {
		return out, nil
	}
	defer rows.Close()
	for rows.Next() {
		var h HeldLock
		var index string
		if err := rows.Scan(&h.PID, &h.Object, &index, &h.LockType, &h.Mode, &h.Granted, &h.State, &h.Query); err != nil {
			return nil, err
		}
		if index != "" {
			h.Object += " (index " + index + ")"
		}
		h.User = users[h.PID]
		if len(out.Locks) == maxHeldLocks {
			out.Truncated = true
			break
		}
		out.Locks = append(out.Locks, h)
	}
	return out, rows.Err()
}

// --- replication -------------------------------------------------------------

// Replication reads both ends through the SHOW statements, by column name:
// their column lists differ between MySQL and MariaDB and between releases of
// each, and the names are the stable part.
func (mysqlDialect) Replication(ctx context.Context, db *sql.DB) (*ReplicationReport, error) {
	out := &ReplicationReport{Facts: map[string]string{}}
	for _, name := range []string{"server_id", "log_bin", "read_only", "binlog_format", "gtid_mode"} {
		var v sql.NullString
		// A variable one of the two engines does not have is an error here,
		// and is simply not a fact about this server.
		if err := db.QueryRowContext(ctx, "SELECT @@GLOBAL."+name).Scan(&v); err == nil && v.Valid {
			out.Facts[name] = v.String
		}
	}

	sources, err := mysqlFirstRows(ctx, db, "SHOW ALL REPLICAS STATUS", "SHOW ALL SLAVES STATUS", "SHOW REPLICA STATUS", "SHOW SLAVE STATUS")
	if err != nil {
		out.Notes = append(out.Notes, "Replica status could not be read: "+err.Error())
	}
	for _, row := range sources {
		src := ReplicaSource{
			Channel:    firstOf(row, "Channel_Name", "Connection_name"),
			Host:       firstOf(row, "Source_Host", "Master_Host"),
			Port:       firstOf(row, "Source_Port", "Master_Port"),
			User:       firstOf(row, "Source_User", "Master_User"),
			State:      firstOf(row, "Replica_IO_State", "Slave_IO_State"),
			IORunning:  strings.EqualFold(firstOf(row, "Replica_IO_Running", "Slave_IO_Running"), "Yes"),
			SQLRunning: strings.EqualFold(firstOf(row, "Replica_SQL_Running", "Slave_SQL_Running"), "Yes"),
			LagSeconds: -1,
			LastError:  firstOf(row, "Last_Error", "Last_SQL_Error", "Last_IO_Error"),
			GTID:       firstOf(row, "Executed_Gtid_Set", "Gtid_IO_Pos"),
		}
		if lag, err := strconv.ParseFloat(firstOf(row, "Seconds_Behind_Source", "Seconds_Behind_Master"), 64); err == nil {
			src.LagSeconds = lag
		}
		if file := firstOf(row, "Relay_Source_Log_File", "Relay_Master_Log_File"); file != "" {
			src.Position = file + ":" + firstOf(row, "Exec_Source_Log_Pos", "Exec_Master_Log_Pos")
		}
		out.Sources = append(out.Sources, src)
	}

	replicas, err := mysqlFirstRows(ctx, db, "SHOW REPLICAS", "SHOW REPLICA HOSTS", "SHOW SLAVE HOSTS")
	if err != nil {
		out.Notes = append(out.Notes, "Connected replicas could not be listed: "+err.Error())
	}
	for _, row := range replicas {
		r := Replica{
			Name:   firstOf(row, "Server_Id", "Server_id"),
			Client: firstOf(row, "Host"),
			// The primary does not know how far behind a replica is; only the
			// replica does, and says so in its own status.
			LagBytes: -1, LagSeconds: -1,
		}
		if port := firstOf(row, "Port"); port != "" && r.Client != "" {
			r.Client += ":" + port
		}
		out.Replicas = append(out.Replicas, r)
	}

	if binlog, err := mysqlFirstRows(ctx, db, "SHOW BINARY LOG STATUS", "SHOW MASTER STATUS", "SHOW BINLOG STATUS"); err == nil && len(binlog) > 0 {
		if file := firstOf(binlog[0], "File"); file != "" {
			out.Facts["binaryLog"] = file + ":" + firstOf(binlog[0], "Position")
		}
		if gtid := firstOf(binlog[0], "Executed_Gtid_Set"); gtid != "" {
			out.Facts["executedGtidSet"] = gtid
		}
	}

	switch {
	case len(out.Sources) > 0:
		out.Role = "replica"
	case len(out.Replicas) > 0:
		out.Role = "primary"
	default:
		out.Role = "standalone"
	}
	return out, nil
}

// --- table and index statistics ------------------------------------------------

// opsMySQLSchemaFilter matches one database, or the connection's own when none
// is named, or every database that is not the server's when the connection
// has none either.
const opsMySQLSchemaFilter = `((? <> '' AND %[1]s = ?)
	   OR (? = '' AND %[1]s = DATABASE())
	   OR (? = '' AND DATABASE() IS NULL AND %[1]s NOT IN ('mysql', 'information_schema', 'performance_schema', 'sys')))`

func mysqlSchemaArgs(schema string) []any { return []any{schema, schema, schema, schema} }

// TableStats reads information_schema.TABLES. DATA_FREE is the space the
// table's file holds and is not using, which is the one bloat figure InnoDB
// states outright. Scan counts come from performance_schema where it is on.
func (mysqlDialect) TableStats(ctx context.Context, db *sql.DB, opts StatsOptions) (*TableStatsReport, error) {
	args := append(mysqlSchemaArgs(opts.Schema), opts.limit()+1)
	rows, err := db.QueryContext(ctx, `
	  SELECT TABLE_SCHEMA, TABLE_NAME, COALESCE(ENGINE, ''), COALESCE(TABLE_ROWS, 0),
	         COALESCE(DATA_LENGTH, 0) + COALESCE(INDEX_LENGTH, 0),
	         COALESCE(DATA_LENGTH, 0), COALESCE(INDEX_LENGTH, 0), COALESCE(DATA_FREE, 0)
	  FROM information_schema.TABLES
	  WHERE TABLE_TYPE = 'BASE TABLE' AND `+schemaFilterOn(opsMySQLSchemaFilter, "TABLE_SCHEMA")+`
	  ORDER BY 5 DESC, 1, 2
	  LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	out := &TableStatsReport{Schema: opts.Schema, Tables: []TableStat{}}
	for rows.Next() {
		t := TableStat{Kind: "table", DeadRows: -1, SeqScans: -1, SeqRowsRead: -1, IndexScans: -1,
			IndexRowsRead: -1, Inserts: -1, Updates: -1, Deletes: -1, ModsSinceStats: -1}
		if err := rows.Scan(&t.Schema, &t.Table, &t.Engine, &t.Rows, &t.TotalBytes, &t.TableBytes, &t.IndexBytes, &t.BloatBytes); err != nil {
			rows.Close()
			return nil, err
		}
		out.Tables = append(out.Tables, t)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	// Rows read with and without an index, and rows changed, per table. The
	// summary has a row per index and one with a NULL index name for reads
	// that used none.
	urows, err := db.QueryContext(ctx, `
	  SELECT OBJECT_SCHEMA, OBJECT_NAME,
	         SUM(CASE WHEN INDEX_NAME IS NULL THEN COUNT_READ ELSE 0 END),
	         SUM(CASE WHEN INDEX_NAME IS NOT NULL THEN COUNT_READ ELSE 0 END),
	         SUM(COUNT_INSERT), SUM(COUNT_UPDATE), SUM(COUNT_DELETE)
	  FROM performance_schema.table_io_waits_summary_by_index_usage
	  WHERE `+schemaFilterOn(opsMySQLSchemaFilter, "OBJECT_SCHEMA")+`
	  GROUP BY OBJECT_SCHEMA, OBJECT_NAME`, mysqlSchemaArgs(opts.Schema)...)
	if err != nil {
		out.Notes = append(out.Notes, "Read and write counts are unavailable: "+mysqlPerformanceSchemaGap(ctx, db, err)+".")
		return out, nil
	}
	defer urows.Close()
	type usage struct{ seqRows, idxRows, ins, upd, del int64 }
	byTable := map[string]usage{}
	for urows.Next() {
		var schema, table string
		var u usage
		if err := urows.Scan(&schema, &table, &u.seqRows, &u.idxRows, &u.ins, &u.upd, &u.del); err != nil {
			return nil, err
		}
		byTable[schema+"\x00"+table] = u
	}
	if err := urows.Err(); err != nil {
		return nil, err
	}
	if len(byTable) == 0 && len(out.Tables) > 0 {
		out.Notes = append(out.Notes, "Read and write counts are empty: "+mysqlPerformanceSchemaGap(ctx, db, nil)+".")
	}
	for i := range out.Tables {
		t := &out.Tables[i]
		if u, ok := byTable[t.Schema+"\x00"+t.Table]; ok {
			t.SeqRowsRead, t.IndexRowsRead = u.seqRows, u.idxRows
			t.Inserts, t.Updates, t.Deletes = u.ins, u.upd, u.del
		}
	}
	return out, nil
}

// schemaFilterOn names the column a schema filter applies to.
func schemaFilterOn(filter, column string) string {
	return strings.ReplaceAll(filter, "%[1]s", column)
}

// IndexStats reads the index list from information_schema, their sizes from
// InnoDB's own statistics table and their use from performance_schema. The
// last two are each optional: an account that cannot read the mysql schema
// gets no sizes, and a server with performance_schema off gets no scan counts.
//
// The sizes arrive after the list, so the list is read whole and ranked here;
// ReadIndexStats cuts it to the limit once it is in order.
func (mysqlDialect) IndexStats(ctx context.Context, db *sql.DB, opts StatsOptions) (*IndexStatsReport, error) {
	args := append(mysqlSchemaArgs(opts.Schema), opts.Table, opts.Table, maxIndexCatalogue+1)
	rows, err := db.QueryContext(ctx, `
	  SELECT s.TABLE_SCHEMA, s.TABLE_NAME, s.INDEX_NAME, MAX(s.INDEX_TYPE), MAX(s.NON_UNIQUE),
	         GROUP_CONCAT(COALESCE(s.COLUMN_NAME, '(expression)') ORDER BY s.SEQ_IN_INDEX SEPARATOR 0x1f),
	         MAX(CASE WHEN s.SUB_PART IS NOT NULL OR s.COLUMN_NAME IS NULL THEN 1 ELSE 0 END)
	  FROM information_schema.STATISTICS s
	  WHERE `+schemaFilterOn(opsMySQLSchemaFilter, "s.TABLE_SCHEMA")+` AND (? = '' OR s.TABLE_NAME = ?)
	  GROUP BY s.TABLE_SCHEMA, s.TABLE_NAME, s.INDEX_NAME
	  ORDER BY s.TABLE_SCHEMA, s.TABLE_NAME, s.INDEX_NAME
	  LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	out := &IndexStatsReport{Schema: opts.Schema, Indexes: []IndexStat{}}
	for rows.Next() {
		ix := IndexStat{Valid: true, Scans: -1, RowsRead: -1}
		var nonUnique, partial int
		var cols string
		if err := rows.Scan(&ix.Schema, &ix.Table, &ix.Name, nullText{&ix.Method}, &nonUnique, nullText{&cols}, &partial); err != nil {
			rows.Close()
			return nil, err
		}
		ix.Method = strings.ToLower(ix.Method)
		ix.Unique = nonUnique == 0
		ix.Primary = ix.Name == "PRIMARY"
		// A unique index is the constraint: MySQL has no other way to state one.
		ix.Constraint = ix.Unique
		ix.Columns = splitUnit(cols)
		// A prefix index or one on an expression is not the same index as a
		// plain one on the same column, and can neither cover nor be covered.
		ix.plain = partial == 0
		if !ix.plain {
			ix.signature = "partial:" + ix.Name
		}
		if len(out.Indexes) == maxIndexCatalogue {
			out.Notes = append(out.Notes, indexCatalogueNote())
			break
		}
		out.Indexes = append(out.Indexes, ix)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	sizes := map[string]int64{}
	if srows, err := db.QueryContext(ctx, `
	  SELECT database_name, table_name, index_name, stat_value * @@innodb_page_size
	  FROM mysql.innodb_index_stats
	  WHERE stat_name = 'size' AND `+schemaFilterOn(opsMySQLSchemaFilter, "database_name"), mysqlSchemaArgs(opts.Schema)...); err == nil {
		for srows.Next() {
			var schema, table, index string
			var size int64
			if srows.Scan(&schema, &table, &index, &size) == nil {
				sizes[schema+"\x00"+table+"\x00"+index] = size
			}
		}
		srows.Close()
	} else {
		out.Notes = append(out.Notes, "Index sizes need read access to mysql.innodb_index_stats.")
	}

	type usage struct{ scans, rows int64 }
	used := map[string]usage{}
	urows, usageErr := db.QueryContext(ctx, `
	  SELECT OBJECT_SCHEMA, OBJECT_NAME, INDEX_NAME, COUNT_READ, COUNT_FETCH
	  FROM performance_schema.table_io_waits_summary_by_index_usage
	  WHERE INDEX_NAME IS NOT NULL AND `+schemaFilterOn(opsMySQLSchemaFilter, "OBJECT_SCHEMA"), mysqlSchemaArgs(opts.Schema)...)
	if usageErr == nil {
		for urows.Next() {
			var schema, table, index string
			var u usage
			if urows.Scan(&schema, &table, &index, &u.scans, &u.rows) == nil {
				used[schema+"\x00"+table+"\x00"+index] = u
			}
		}
		urows.Close()
	}
	if len(used) == 0 && len(out.Indexes) > 0 {
		out.Notes = append(out.Notes, "Use counts are unavailable: "+mysqlPerformanceSchemaGap(ctx, db, usageErr)+", so no index can be called unused.")
	}
	for i := range out.Indexes {
		ix := &out.Indexes[i]
		key := ix.Schema + "\x00" + ix.Table + "\x00" + ix.Name
		ix.Bytes = sizes[key]
		if u, ok := used[key]; ok {
			// performance_schema counts rows read through the index rather
			// than scans started; zero of either means the same thing.
			ix.Scans, ix.RowsRead = u.scans, u.rows
		}
	}
	// Largest first, as every other engine's list is; the catalogue query
	// could not sort by a size it does not have.
	sortIndexStatsBySize(out.Indexes)
	return out, nil
}

// --- maintenance -------------------------------------------------------------

func (mysqlDialect) MaintenanceActions() []MaintenanceAction {
	return []MaintenanceAction{
		{ID: "analyze", Label: "Analyze", Scope: "either",
			Description: "Refreshes the key distribution statistics the optimiser chooses plans from."},
		{ID: "check", Label: "Check", Scope: "either", ReadOnly: true,
			Description: "Reads the table and its indexes looking for corruption. Changes nothing."},
		{ID: "optimize", Label: "Optimize", Scope: "either", Blocking: true,
			Description: "Rebuilds the table to reclaim free space and defragment it. On InnoDB this is a full copy of the table and needs the disk for it; on MyISAM the table is locked throughout."},
		{ID: "repair", Label: "Repair", Scope: "either", Blocking: true, Destructive: true,
			Description: "Rebuilds a damaged MyISAM, ARCHIVE or CSV table from what can still be read. Rows that cannot be read are dropped, so take a dump first. InnoDB does not support it."},
	}
}

// maxMaintenanceTables bounds a whole-database run. Past this the operator is
// better served by the server's own scheduler than by one HTTP request.
const maxMaintenanceTables = 500

// mysqlMaintenanceSQL renders the statement for a batch of tables.
func mysqlMaintenanceSQL(action string, tables []string) (string, error) {
	verb := map[string]string{
		"analyze": "ANALYZE TABLE ", "check": "CHECK TABLE ",
		"optimize": "OPTIMIZE TABLE ", "repair": "REPAIR TABLE ",
	}[action]
	if verb == "" {
		return "", maintenanceRefused("unknown action %q", action)
	}
	return verb + strings.Join(tables, ", "), nil
}

func (d mysqlDialect) Maintain(ctx context.Context, db *sql.DB, _ string, req MaintenanceRequest) (*MaintenanceResult, error) {
	if req.Index != "" {
		return nil, maintenanceRefused("MySQL rebuilds a table's indexes with the table; name the table")
	}
	names := []string{}
	if req.Table != "" {
		names = append(names, req.Table)
	} else {
		q := `SELECT TABLE_NAME FROM information_schema.TABLES
		      WHERE TABLE_TYPE = 'BASE TABLE' AND TABLE_SCHEMA = DATABASE() ORDER BY TABLE_NAME LIMIT ?`
		args := []any{maxMaintenanceTables + 1}
		if req.Schema != "" {
			q = `SELECT TABLE_NAME FROM information_schema.TABLES
			     WHERE TABLE_TYPE = 'BASE TABLE' AND TABLE_SCHEMA = ? ORDER BY TABLE_NAME LIMIT ?`
			args = []any{req.Schema, maxMaintenanceTables + 1}
		}
		rows, err := db.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				return nil, err
			}
			names = append(names, name)
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		if len(names) == 0 {
			return nil, maintenanceRefused("there are no tables to %s", req.Action)
		}
		if len(names) > maxMaintenanceTables {
			return nil, maintenanceRefused("this database has more than %d tables; run %s on the tables that need it", maxMaintenanceTables, req.Action)
		}
	}
	quoted := make([]string, len(names))
	for i, n := range names {
		rel, err := qualify(d, req.Schema, n)
		if err != nil {
			return nil, err
		}
		quoted[i] = rel
	}
	out := &MaintenanceResult{Statements: []string{}, Output: []string{}, OK: true}
	conn, release, err := mysqlStoppableSession(ctx, db)
	if err != nil {
		return nil, err
	}
	defer release()
	// In batches: the statement takes a list, and a list of five hundred
	// names is one result the operator waits minutes for with nothing to read.
	const batch = 25
	for start := 0; start < len(quoted); start += batch {
		end := min(start+batch, len(quoted))
		stmt, err := mysqlMaintenanceSQL(req.Action, quoted[start:end])
		if err != nil {
			return nil, err
		}
		rows, err := conn.QueryContext(ctx, stmt)
		if err != nil {
			return nil, err
		}
		out.Statements = append(out.Statements, stmt)
		// Table, Op, Msg_type, Msg_text — one or more rows per table, the
		// last of which is its verdict. Every row is read for the verdict even
		// once no more lines are kept.
		for rows.Next() {
			var table, op, kind, text sql.NullString
			if err := rows.Scan(&table, &op, &kind, &text); err != nil {
				rows.Close()
				return nil, err
			}
			out.keep(table.String + ": " + kind.String + ": " + text.String)
			if strings.EqualFold(kind.String, "error") {
				out.OK = false
			}
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// mysqlStoppableSession takes one connection out of the pool and arranges for
// whatever it is running to be stopped on the server if the context ends. The
// caller runs its statements on the connection and calls release when done.
//
// The driver's own answer to a cancelled context is to close the socket. The
// server does not notice a closed socket while it is busy: an OPTIMIZE TABLE
// whose request was abandoned carries on rebuilding the table to the end. So
// the session's id is read first, and on cancellation a second connection
// sends KILL QUERY for it — the statement stops and its work is rolled back.
//
// A connection the kill may have been aimed at is not handed back to the
// pool. The kill is sent a moment after the context ends, and by then a
// returned connection could be running somebody else's query.
func mysqlStoppableSession(ctx context.Context, db *sql.DB) (conn *sql.Conn, release func(), err error) {
	conn, err = db.Conn(ctx)
	if err != nil {
		return nil, nil, err
	}
	var id int64
	if err := conn.QueryRowContext(ctx, `SELECT CONNECTION_ID()`).Scan(&id); err != nil {
		conn.Close()
		return nil, nil, err
	}
	unwatch := context.AfterFunc(ctx, func() {
		// Its own context: the one that asked for the work is the one that
		// just ended.
		kill, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_, _ = db.ExecContext(kill, "KILL QUERY "+strconv.FormatInt(id, 10))
	})
	return conn, func() {
		if !unwatch() || ctx.Err() != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		conn.Close()
	}, nil
}
