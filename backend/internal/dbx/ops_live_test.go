package dbx

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Live tests for the operations surface: sessions, locks, statistics,
// maintenance, settings, roles and grants, against real servers.
//
// Unlike the older live tests these never fall back to an engine's standard
// port. Everything here changes server-wide state — a role, a parameter, the
// statement statistics — and a test that did that to whatever happened to be
// listening on 5432 would be doing it to somebody's database. No variable,
// no test.

// opsLive opens the engine named by an environment variable or skips.
func opsLive(t *testing.T, driver Driver, env string) (*sql.DB, string) {
	t.Helper()
	dsn := os.Getenv(env)
	if dsn == "" {
		t.Skipf("%s is not set", env)
	}
	return opsOpen(t, driver, dsn, env), dsn
}

func opsOpen(t *testing.T, driver Driver, dsn, env string) *sql.DB {
	t.Helper()
	d, err := DialectFor(driver)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open(d.SQLDriverName(), d.NormaliseDSN(dsn))
	if err != nil {
		t.Skipf("%s unavailable (%s): %v", driver, env, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		t.Skipf("%s unreachable (%s): %v", driver, env, err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// opsLiveAny is opsLive for an engine the environment may name under either of
// two variables: this file's own, which wins, or the shared fixture's.
func opsLiveAny(t *testing.T, driver Driver, envs ...string) (*sql.DB, string) {
	t.Helper()
	for _, env := range envs {
		if dsn := os.Getenv(env); dsn != "" {
			return opsOpen(t, driver, dsn, env), dsn
		}
	}
	t.Skipf("none of %s is set", strings.Join(envs, ", "))
	return nil, ""
}

// opsSQLServer opens a database of this file's own on the SQL Server the
// environment names, and returns the connection string that opened it. The
// fixture is shared and its connection string opens master; nothing here is
// made there.
func opsSQLServer(t *testing.T) (*sql.DB, string) {
	t.Helper()
	server, dsn := opsLiveAny(t, DriverMSSQL, "JD_TEST_B3_MSSQL_DSN", "JD_TEST_MSSQL_DSN")
	opsMustExec(t, server, `IF DB_ID('jd_b3') IS NULL CREATE DATABASE jd_b3`)
	own := DSNForDatabase(DriverMSSQL, dsn, "jd_b3")
	return opsOpen(t, DriverMSSQL, own, "the jd_b3 database"), own
}

// mysqlOpsServers is every MySQL-family server the environment names: the
// MariaDB the shared variables point at, and a MySQL 8 behind a variable of
// this file's own. Each needs an administrative account, because the tests
// make users and read the InnoDB tables.
func mysqlOpsServers(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	if admin := os.Getenv("JD_TEST_MYSQL_ADMIN_DSN"); admin != "" {
		database := "jdtest"
		if user := os.Getenv("JD_TEST_MYSQL_DSN"); strings.Contains(user, "/") {
			database = user[strings.LastIndex(user, "/")+1:]
		}
		out["mariadb"] = strings.TrimSuffix(admin, "/") + "/" + database
	}
	if admin := os.Getenv("JD_TEST_B3_MYSQL8_ADMIN_DSN"); admin != "" {
		out["mysql8"] = admin
	}
	if len(out) == 0 {
		t.Skip("neither JD_TEST_MYSQL_ADMIN_DSN nor JD_TEST_B3_MYSQL8_ADMIN_DSN is set")
	}
	return out
}

func opsMustExec(t *testing.T, db *sql.DB, stmts ...string) {
	t.Helper()
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

func findSession(list []Activity, pid string) *Activity {
	for i := range list {
		if list[i].PID == pid {
			return &list[i]
		}
	}
	return nil
}

// waitFor polls until the condition holds: a blocked statement takes a moment
// to show up as waiting, and a fixed sleep is either too short or wasted.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// --- PostgreSQL --------------------------------------------------------------

func TestLiveOpsPostgresStats(t *testing.T) {
	db, _ := opsLive(t, DriverPostgres, "JD_TEST_POSTGRES_DSN")
	ctx := t.Context()
	first, err := ReadServerStats(ctx, db, DriverPostgres)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Supported || first.Version == "" || first.StartedAt == nil || first.DatabaseBytes <= 0 {
		t.Fatalf("snapshot is missing its basics: %+v", first)
	}
	if first.Connections == nil || first.Connections.Max <= 0 || first.Connections.Total < 1 {
		t.Fatalf("connections: %+v", first.Connections)
	}
	if first.Role != "standalone" && first.Role != "primary" && first.Role != "replica" {
		t.Errorf("role = %q", first.Role)
	}
	for _, key := range []string{StatTransactionsCommitted, StatTransactionsRolledBack, StatBlocksHit, StatBlocksRead,
		StatRowsRead, StatRowsWritten, StatDeadlocks, StatTempFiles, StatTempBytes, "walBytes"} {
		if _, ok := first.Counters[key]; !ok {
			t.Errorf("counter %q is missing", key)
		}
	}
	// A counter is a running total: a second reading is never behind the first.
	opsMustExec(t, db, `SELECT count(*) FROM pg_class`)
	second, err := ReadServerStats(ctx, db, DriverPostgres)
	if err != nil {
		t.Fatal(err)
	}
	if second.Counters[StatTransactionsCommitted] < first.Counters[StatTransactionsCommitted] {
		t.Errorf("commits went backwards: %v then %v", first.Counters[StatTransactionsCommitted], second.Counters[StatTransactionsCommitted])
	}
	if !second.At.After(first.At) {
		t.Errorf("the second snapshot is not later than the first")
	}
}

// An idle session used to be the longest-running query on the server, because
// "seconds" was the time since its last statement began. A session idle in a
// transaction must say so, with no running time, and a session waiting on its
// lock must name it — in the list and in the lock report.
func TestLiveOpsPostgresSessionsAndLocks(t *testing.T) {
	db, _ := opsLive(t, DriverPostgres, "JD_TEST_POSTGRES_DSN")
	ctx := t.Context()
	opsMustExec(t, db, `DROP TABLE IF EXISTS jd_b3_ops_lock`, `CREATE TABLE jd_b3_ops_lock (id int primary key, v int)`,
		`INSERT INTO jd_b3_ops_lock VALUES (1, 0)`)
	t.Cleanup(func() { db.Exec(`DROP TABLE IF EXISTS jd_b3_ops_lock`) })

	holder, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	var holderPID string
	if err := holder.QueryRowContext(ctx, `SELECT pg_backend_pid()::text`).Scan(&holderPID); err != nil {
		t.Fatal(err)
	}
	if _, err := holder.ExecContext(ctx, `SET application_name = 'jd-b3-holder'`); err != nil {
		t.Fatal(err)
	}
	if _, err := holder.ExecContext(ctx, `BEGIN`); err != nil {
		t.Fatal(err)
	}
	if _, err := holder.ExecContext(ctx, `UPDATE jd_b3_ops_lock SET v = 1 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	defer holder.ExecContext(context.Background(), `ROLLBACK`)

	waiter, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer waiter.Close()
	var waiterPID string
	if err := waiter.QueryRowContext(ctx, `SELECT pg_backend_pid()::text`).Scan(&waiterPID); err != nil {
		t.Fatal(err)
	}
	blocked := make(chan error, 1)
	go func() {
		_, err := waiter.ExecContext(context.Background(), `UPDATE jd_b3_ops_lock SET v = 2 WHERE id = 1`)
		blocked <- err
	}()

	var sessions []Activity
	waitFor(t, "the waiter to be blocked", func() bool {
		sessions, err = ListActivity(ctx, db, DriverPostgres)
		if err != nil {
			t.Fatal(err)
		}
		w := findSession(sessions, waiterPID)
		return w != nil && w.Status == SessionBlocked
	})
	h, w := findSession(sessions, holderPID), findSession(sessions, waiterPID)
	if h == nil {
		t.Fatal("the lock holder is not in the session list")
	}
	if h.Status != SessionIdleInTransaction || h.Seconds != 0 {
		t.Errorf("holder: status %q seconds %v, want idle_in_transaction and 0", h.Status, h.Seconds)
	}
	if h.TransactionStart == nil || h.Application != "jd-b3-holder" || h.StateSince == nil {
		t.Errorf("holder is missing its detail: %+v", h)
	}
	if len(w.BlockedByPIDs) != 1 || w.BlockedByPIDs[0] != holderPID || w.BlockedBy != holderPID {
		t.Errorf("waiter blocked by %v (%q), want %s", w.BlockedByPIDs, w.BlockedBy, holderPID)
	}
	if w.WaitType != "Lock" || w.QueryStart == nil {
		t.Errorf("waiter wait = %q/%q", w.WaitType, w.WaitEvent)
	}

	locks, err := ListLocks(ctx, db, DriverPostgres)
	if err != nil {
		t.Fatal(err)
	}
	var wait *LockWait
	for i := range locks.Waits {
		if locks.Waits[i].WaitingPID == waiterPID {
			wait = &locks.Waits[i]
		}
	}
	if wait == nil {
		t.Fatalf("no wait reported for %s: %+v", waiterPID, locks.Waits)
	}
	if wait.BlockingPID != holderPID || wait.BlockingState != "idle in transaction" || wait.Mode == "" {
		t.Errorf("wait: %+v", wait)
	}
	if !strings.Contains(wait.WaitingQuery, "jd_b3_ops_lock") {
		t.Errorf("the waiting statement is %q", wait.WaitingQuery)
	}
	held := false
	for _, l := range locks.Locks {
		if l.PID == holderPID && strings.Contains(l.Object, "jd_b3_ops_lock") && l.Granted {
			held = true
		}
	}
	if !held {
		t.Errorf("the holder's lock on the table is not in the lock table")
	}

	// Cancelling the waiter ends its statement and leaves its session.
	if err := CancelQuery(ctx, db, DriverPostgres, waiterPID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-blocked:
		if err == nil || !strings.Contains(err.Error(), "cancel") {
			t.Errorf("the cancelled statement ended with %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the cancelled statement never returned")
	}
	var alive int
	if err := waiter.QueryRowContext(ctx, `SELECT 1`).Scan(&alive); err != nil {
		t.Errorf("the session did not survive a cancel: %v", err)
	}
	if err := CancelQuery(ctx, db, DriverPostgres, "2147483000"); err == nil {
		t.Error("cancelling a session that does not exist reported success")
	}
}

func TestLiveOpsPostgresReplication(t *testing.T) {
	db, _ := opsLive(t, DriverPostgres, "JD_TEST_POSTGRES_DSN")
	rep, err := ReadReplication(t.Context(), db, DriverPostgres)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Supported || rep.Role == "" || rep.Facts["wal_level"] == "" {
		t.Fatalf("replication: %+v", rep)
	}
	if len(rep.Notes) != 0 {
		t.Errorf("a superuser was refused part of the report: %v", rep.Notes)
	}
	if rep.Replicas == nil || rep.Slots == nil || rep.Publications == nil || rep.Subscriptions == nil || rep.Sources == nil {
		t.Errorf("a list is null rather than empty: %+v", rep)
	}

	// A publication and a slot made here must be read back as made.
	db.Exec(`DROP PUBLICATION IF EXISTS jd_b3_ops_pub`)
	opsMustExec(t, db, `DROP TABLE IF EXISTS jd_b3_ops_pubt`, `CREATE TABLE jd_b3_ops_pubt (id int primary key)`,
		`CREATE PUBLICATION jd_b3_ops_pub FOR TABLE jd_b3_ops_pubt WITH (publish = 'insert, update')`)
	t.Cleanup(func() {
		db.Exec(`DROP PUBLICATION IF EXISTS jd_b3_ops_pub`)
		db.Exec(`DROP TABLE IF EXISTS jd_b3_ops_pubt`)
		db.Exec(`SELECT pg_drop_replication_slot('jd_b3_ops_slot')`)
	})
	slotMade := true
	if _, err := db.Exec(`SELECT pg_create_physical_replication_slot('jd_b3_ops_slot')`); err != nil {
		slotMade = false
		t.Logf("no slot could be made here: %v", err)
	}
	rep, err = ReadReplication(t.Context(), db, DriverPostgres)
	if err != nil {
		t.Fatal(err)
	}
	var pub *Publication
	for i := range rep.Publications {
		if rep.Publications[i].Name == "jd_b3_ops_pub" {
			pub = &rep.Publications[i]
		}
	}
	if pub == nil || !pub.Insert || !pub.Update || pub.Delete || pub.Tables != 1 || pub.AllTables {
		t.Errorf("publication: %+v", pub)
	}
	if slotMade {
		var slot *ReplicationSlot
		for i := range rep.Slots {
			if rep.Slots[i].Name == "jd_b3_ops_slot" {
				slot = &rep.Slots[i]
			}
		}
		if slot == nil || slot.Active || slot.Type != "physical" {
			t.Errorf("slot: %+v", slot)
		}
	}
}

func TestLiveOpsPostgresTableAndIndexStats(t *testing.T) {
	db, _ := opsLive(t, DriverPostgres, "JD_TEST_POSTGRES_DSN")
	ctx := t.Context()
	opsMustExec(t, db, `DROP TABLE IF EXISTS jd_b3_ops_stats`, `DROP TABLE IF EXISTS jd_b3_ops_small`,
		`CREATE TABLE jd_b3_ops_small (id int primary key)`,
		`CREATE TABLE jd_b3_ops_stats (id int primary key, a text, b int, CONSTRAINT jd_b3_ops_stats_a_key UNIQUE (a))`,
		`CREATE INDEX jd_b3_ops_stats_b ON jd_b3_ops_stats (b)`,
		`CREATE INDEX jd_b3_ops_stats_b_copy ON jd_b3_ops_stats (b)`,
		`CREATE INDEX jd_b3_ops_stats_ba ON jd_b3_ops_stats (b, a)`,
		`CREATE INDEX jd_b3_ops_stats_a_plain ON jd_b3_ops_stats (a)`,
		`CREATE INDEX jd_b3_ops_stats_lower ON jd_b3_ops_stats (lower(a))`,
		`CREATE INDEX jd_b3_ops_stats_upper ON jd_b3_ops_stats (upper(a))`,
		`INSERT INTO jd_b3_ops_stats SELECT g, md5(g::text), g % 10 FROM generate_series(1, 3000) g`,
		`DELETE FROM jd_b3_ops_stats WHERE id > 1000`,
		`ANALYZE jd_b3_ops_stats`)
	t.Cleanup(func() {
		db.Exec(`DROP TABLE IF EXISTS jd_b3_ops_stats`)
		db.Exec(`DROP TABLE IF EXISTS jd_b3_ops_small`)
	})

	tables, err := ReadTableStats(ctx, db, DriverPostgres, StatsOptions{Schema: "public"})
	if err != nil {
		t.Fatal(err)
	}
	var stat *TableStat
	for i := range tables.Tables {
		if tables.Tables[i].Table == "jd_b3_ops_stats" {
			stat = &tables.Tables[i]
		}
	}
	if stat == nil {
		t.Fatalf("the table is not in the report (%d tables)", len(tables.Tables))
	}
	if stat.Rows != 1000 || stat.TotalBytes <= stat.TableBytes || stat.IndexBytes <= 0 || stat.Kind != "table" {
		t.Errorf("sizes: %+v", stat)
	}
	if stat.LastAnalyze == nil {
		t.Errorf("last analyze is missing after an ANALYZE")
	}
	// Two thirds of the rows were deleted and never vacuumed: the heap holds
	// three times what its rows need, and the estimate has to see it.
	if stat.BloatBytes <= 0 {
		t.Errorf("bloat estimate = %d for a table that is two thirds dead", stat.BloatBytes)
	}

	// The limit keeps the largest and says there were more.
	bounded, err := ReadTableStats(ctx, db, DriverPostgres, StatsOptions{Schema: "", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(bounded.Tables) != 1 || !bounded.Truncated || bounded.Tables[0].Table != "jd_b3_ops_stats" {
		t.Errorf("limit 1 returned %+v, truncated %v", bounded.Tables, bounded.Truncated)
	}

	indexes, err := ReadIndexStats(ctx, db, DriverPostgres, StatsOptions{Schema: "public", Table: "jd_b3_ops_stats"})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]IndexStat{}
	for _, ix := range indexes.Indexes {
		by[ix.Name] = ix
	}
	if len(by) != 8 {
		t.Fatalf("got %d indexes, want 8: %+v", len(by), indexes.Indexes)
	}
	// Exactly one of the two identical indexes is the copy.
	b, copyOf := by["jd_b3_ops_stats_b"], by["jd_b3_ops_stats_b_copy"]
	if (b.DuplicateOf == "") == (copyOf.DuplicateOf == "") {
		t.Errorf("identical indexes: %q and %q", b.DuplicateOf, copyOf.DuplicateOf)
	}
	// The plain index on a is a duplicate of the constraint's, never the
	// other way round: the constraint's is the one that cannot be dropped.
	if got := by["jd_b3_ops_stats_a_plain"].DuplicateOf; got != "jd_b3_ops_stats_a_key" {
		t.Errorf("the plain index duplicates %q, want the constraint's index", got)
	}
	if key := by["jd_b3_ops_stats_a_key"]; key.DuplicateOf != "" || !key.Constraint || !key.Unique {
		t.Errorf("the constraint's index: %+v", key)
	}
	// Two expression indexes have the same (empty) column list and are not
	// the same index.
	if by["jd_b3_ops_stats_lower"].DuplicateOf != "" || by["jd_b3_ops_stats_upper"].DuplicateOf != "" {
		t.Errorf("two different expression indexes were called duplicates")
	}
	kept := b
	if b.DuplicateOf != "" {
		kept = copyOf
	}
	if kept.CoveredBy != "jd_b3_ops_stats_ba" {
		t.Errorf("(b) is covered by %q, want the (b, a) index", kept.CoveredBy)
	}
	if !by["jd_b3_ops_stats_ba"].Unused || by["jd_b3_ops_stats_pkey"].Unused {
		t.Errorf("unused: ba %v, pkey %v", by["jd_b3_ops_stats_ba"].Unused, by["jd_b3_ops_stats_pkey"].Unused)
	}
	if cols := by["jd_b3_ops_stats_ba"].Columns; len(cols) != 2 || cols[0] != "b" || cols[1] != "a" {
		t.Errorf("columns: %v", cols)
	}
}

func TestLiveOpsPostgresMaintenance(t *testing.T) {
	db, dsn := opsLive(t, DriverPostgres, "JD_TEST_POSTGRES_DSN")
	ctx := t.Context()
	opsMustExec(t, db, `DROP TABLE IF EXISTS jd_b3_ops_maint`,
		`CREATE TABLE jd_b3_ops_maint (id int primary key, v text)`,
		`INSERT INTO jd_b3_ops_maint SELECT g, 'x' FROM generate_series(1, 500) g`)
	t.Cleanup(func() { db.Exec(`DROP TABLE IF EXISTS jd_b3_ops_maint`) })

	for _, c := range []struct {
		req   MaintenanceRequest
		stmt  string
		print string
	}{
		{MaintenanceRequest{Action: "vacuum", Schema: "public", Table: "jd_b3_ops_maint"}, `VACUUM (VERBOSE) "public"."jd_b3_ops_maint"`, "jd_b3_ops_maint"},
		{MaintenanceRequest{Action: "vacuum_analyze", Table: "jd_b3_ops_maint"}, `VACUUM (VERBOSE, ANALYZE) "public"."jd_b3_ops_maint"`, "analyzing"},
		{MaintenanceRequest{Action: "analyze", Table: "jd_b3_ops_maint"}, `ANALYZE VERBOSE "public"."jd_b3_ops_maint"`, "analyzing"},
		{MaintenanceRequest{Action: "vacuum_full", Table: "jd_b3_ops_maint"}, `VACUUM (FULL, VERBOSE) "public"."jd_b3_ops_maint"`, "jd_b3_ops_maint"},
		{MaintenanceRequest{Action: "reindex", Table: "jd_b3_ops_maint"}, `REINDEX (VERBOSE) TABLE "public"."jd_b3_ops_maint"`, "jd_b3_ops_maint_pkey"},
		{MaintenanceRequest{Action: "reindex", Index: "jd_b3_ops_maint_pkey", Options: MaintenanceOptions{Concurrently: true}},
			`REINDEX (VERBOSE) INDEX CONCURRENTLY "public"."jd_b3_ops_maint_pkey"`, "jd_b3_ops_maint_pkey"},
	} {
		res, err := RunMaintenance(ctx, db, DriverPostgres, dsn, c.req)
		if err != nil {
			t.Fatalf("%s: %v", c.req.Action, err)
		}
		if len(res.Statements) != 1 || res.Statements[0] != c.stmt {
			t.Errorf("%s ran %v, want %s", c.req.Action, res.Statements, c.stmt)
		}
		if !res.OK || !strings.Contains(strings.Join(res.Output, "\n"), c.print) {
			t.Errorf("%s printed %q, want a line about %q", c.req.Action, res.Output, c.print)
		}
	}

	for _, bad := range []MaintenanceRequest{
		{Action: "drop"},
		{Action: "vacuum", Table: `x"; DROP TABLE jd_b3_ops_maint; --`},
		{Action: "analyze", Index: "jd_b3_ops_maint_pkey"},
	} {
		if res, err := RunMaintenance(ctx, db, DriverPostgres, dsn, bad); err == nil {
			t.Errorf("%+v was accepted: %+v", bad, res)
		}
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM jd_b3_ops_maint`).Scan(&n); err != nil || n != 500 {
		t.Fatalf("the table did not survive the refused requests: %d rows, %v", n, err)
	}
}

func TestLiveOpsPostgresSettings(t *testing.T) {
	db, _ := opsLive(t, DriverPostgres, "JD_TEST_POSTGRES_DSN")
	ctx := t.Context()
	all, err := ListSettings(ctx, db, DriverPostgres, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < 200 {
		t.Fatalf("the full list has %d parameters", len(all))
	}
	by := map[string]Setting{}
	for _, s := range all {
		by[s.Name] = s
	}
	if s := by["max_connections"]; s.Type != "integer" || !s.RestartRequired || s.Context != "postmaster" || s.Min == "" || s.Category == "" || s.Description == "" {
		t.Errorf("max_connections: %+v", s)
	}
	if s := by["wal_level"]; s.Type != "enum" || len(s.Enum) < 3 {
		t.Errorf("wal_level: %+v", s)
	}
	if s := by["server_version"]; s.Editable {
		t.Errorf("a compiled-in parameter is editable: %+v", s)
	}
	short, err := ListSettings(ctx, db, DriverPostgres, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(short) == 0 || len(short) >= len(all) || short[0].Name != "server_version" {
		t.Errorf("the short list has %d parameters, starting %q", len(short), short[0].Name)
	}

	// deadlock_timeout: reloadable, in milliseconds, and nothing on a test
	// server depends on its value.
	t.Cleanup(func() {
		db.Exec(`ALTER SYSTEM RESET deadlock_timeout`)
		db.Exec(`SELECT pg_reload_conf()`)
	})
	change, err := ChangeSetting(ctx, db, DriverPostgres, "deadlock_timeout", "1500ms", false)
	if err != nil {
		t.Fatal(err)
	}
	if !change.Persisted || change.RestartRequired || len(change.Statements) != 2 ||
		change.Statements[0] != `ALTER SYSTEM SET "deadlock_timeout" = '1500ms'` {
		t.Errorf("change: %+v", change)
	}
	waitFor(t, "the reload to apply", func() bool {
		var v string
		return db.QueryRow(`SHOW deadlock_timeout`).Scan(&v) == nil && v == "1500ms"
	})
	// A unit the setting can take is converted for the range check.
	if _, err := ChangeSetting(ctx, db, DriverPostgres, "deadlock_timeout", "2s", false); err != nil {
		t.Errorf("2s was refused: %v", err)
	}
	reset, err := ChangeSetting(ctx, db, DriverPostgres, "deadlock_timeout", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if reset.Statements[0] != `ALTER SYSTEM RESET "deadlock_timeout"` {
		t.Errorf("reset ran %v", reset.Statements)
	}
	waitFor(t, "the reset to apply", func() bool {
		var v string
		return db.QueryRow(`SHOW deadlock_timeout`).Scan(&v) == nil && v == "1s"
	})

	for name, value := range map[string]string{
		"deadlock_timeout":   "0",        // below the minimum of 1 ms
		"wal_level":          "sideways", // not one of the enum's values
		"autovacuum":         "maybe",    // not a boolean
		"max_connections":    "12.5",     // not a whole number
		"work_mem":           "64 parsecs",
		"server_version":     "99",   // compiled in
		"no_such_parameter":  "1",    // not in the engine's list
		"work_mem; DROP":     "1",    // not a name
		"deadlock_timeout\n": "1500", // trailing junk is trimmed to a real name, and that is fine
	} {
		_, err := ChangeSetting(ctx, db, DriverPostgres, name, value, false)
		if strings.HasSuffix(name, "\n") {
			if err != nil {
				t.Errorf("a padded name was refused: %v", err)
			}
			continue
		}
		if _, ok := err.(ErrSettingRequest); !ok {
			t.Errorf("%s = %q: %v, want a refusal", name, value, err)
		}
	}
}

// Changing a password must change the password. It used to rewrite every
// attribute to its default as well, so a superuser came out of a password
// change an ordinary login and a group role came out of one able to sign in.
func TestLiveOpsPostgresAlterRoleChangesOnlyWhatWasSent(t *testing.T) {
	db, _ := opsLive(t, DriverPostgres, "JD_TEST_POSTGRES_DSN")
	ctx := t.Context()
	admin, err := AdminFor(DriverPostgres)
	if err != nil {
		t.Fatal(err)
	}
	db.Exec(`DROP ROLE IF EXISTS jd_b3_ops_super`)
	db.Exec(`DROP ROLE IF EXISTS jd_b3_ops_group`)
	opsMustExec(t, db, `CREATE ROLE jd_b3_ops_super WITH LOGIN SUPERUSER CREATEDB CREATEROLE CONNECTION LIMIT 7 PASSWORD 'first'`,
		`CREATE ROLE jd_b3_ops_group WITH NOLOGIN`)
	t.Cleanup(func() {
		db.Exec(`DROP ROLE IF EXISTS jd_b3_ops_super`)
		db.Exec(`DROP ROLE IF EXISTS jd_b3_ops_group`)
	})
	read := func(name string) (login, super, createdb, createrole bool, limit int) {
		t.Helper()
		if err := db.QueryRow(`SELECT rolcanlogin, rolsuper, rolcreatedb, rolcreaterole, rolconnlimit FROM pg_roles WHERE rolname = $1`, name).
			Scan(&login, &super, &createdb, &createrole, &limit); err != nil {
			t.Fatal(err)
		}
		return
	}

	// What the password dialog sends: a name and a password, nothing else.
	for _, name := range []string{"jd_b3_ops_super", "jd_b3_ops_group"} {
		if err := admin.AlterRole(ctx, db, RoleSpec{Name: name, Password: "second-pw", SetPassword: true}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if login, super, createdb, createrole, limit := read("jd_b3_ops_super"); !login || !super || !createdb || !createrole || limit != 7 {
		t.Errorf("a password change altered the superuser: login %v super %v createdb %v createrole %v limit %d",
			login, super, createdb, createrole, limit)
	}
	if login, _, _, _, _ := read("jd_b3_ops_group"); login {
		t.Errorf("a password change gave a group role a login")
	}

	// One attribute at a time changes that attribute.
	off := false
	until := "2031-05-06"
	if err := admin.AlterRole(ctx, db, RoleSpec{Name: "jd_b3_ops_super", SetSuperuser: true, Superuser: false,
		Inherit: &off, ValidUntil: &until}); err != nil {
		t.Fatal(err)
	}
	if login, super, createdb, createrole, _ := read("jd_b3_ops_super"); !login || super || !createdb || !createrole {
		t.Errorf("clearing superuser: login %v super %v createdb %v createrole %v", login, super, createdb, createrole)
	}
	detail, err := ReadRoleDetail(ctx, db, DriverPostgres, "jd_b3_ops_super", "")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Attributes["inherit"] || !strings.HasPrefix(detail.ValidUntil, "2031-05-06") || detail.ConnLimit != 7 {
		t.Errorf("detail: attributes %v validUntil %q limit %d", detail.Attributes, detail.ValidUntil, detail.ConnLimit)
	}
	if err := admin.AlterRole(ctx, db, RoleSpec{Name: "jd_b3_ops_super"}); err == nil {
		t.Error("an alter that changes nothing was accepted")
	}
	if _, err := ReadRoleDetail(ctx, db, DriverPostgres, "jd_b3_no_such_role", ""); err != ErrNoSuchRole {
		t.Errorf("a missing role: %v", err)
	}
}

func TestLiveOpsPostgresPrivileges(t *testing.T) {
	db, _ := opsLive(t, DriverPostgres, "JD_TEST_POSTGRES_DSN")
	ctx := t.Context()
	var database string
	if err := db.QueryRow(`SELECT current_database()`).Scan(&database); err != nil {
		t.Fatal(err)
	}
	cleanup := func() {
		db.Exec(`DROP SCHEMA IF EXISTS jd_b3_ops_sch CASCADE`)
		db.Exec(`DROP OWNED BY jd_b3_ops_app`)
		db.Exec(`DROP ROLE IF EXISTS jd_b3_ops_app`)
		db.Exec(`DROP ROLE IF EXISTS jd_b3_ops_team`)
	}
	cleanup()
	t.Cleanup(cleanup)
	opsMustExec(t, db, `CREATE ROLE jd_b3_ops_app WITH LOGIN PASSWORD 'pw'`, `CREATE ROLE jd_b3_ops_team`,
		`CREATE SCHEMA jd_b3_ops_sch`, `CREATE TABLE jd_b3_ops_sch.orders (id serial primary key, v text)`,
		`CREATE TABLE jd_b3_ops_sch.items (id int primary key)`)

	change := func(c PrivilegeChange) []string {
		t.Helper()
		c.Role = "jd_b3_ops_app"
		out, err := ChangePrivileges(ctx, db, DriverPostgres, c, false)
		if err != nil {
			t.Fatalf("%+v: %v", c, err)
		}
		return out.Statements
	}
	can := func(privilege, table string) bool {
		t.Helper()
		var ok bool
		if err := db.QueryRow(`SELECT has_table_privilege('jd_b3_ops_app', $1, $2)`, table, privilege).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}

	change(PrivilegeChange{Level: GrantOnSchema, Schema: "jd_b3_ops_sch", Privileges: []string{"usage"}})
	stmts := change(PrivilegeChange{Level: GrantOnTable, Schema: "jd_b3_ops_sch", Table: "orders", Privileges: []string{"select", "INSERT"}})
	if len(stmts) != 1 || stmts[0] != `GRANT SELECT, INSERT ON TABLE "jd_b3_ops_sch"."orders" TO "jd_b3_ops_app"` {
		t.Errorf("table grant ran %v", stmts)
	}
	if !can("SELECT", "jd_b3_ops_sch.orders") || !can("INSERT", "jd_b3_ops_sch.orders") || can("DELETE", "jd_b3_ops_sch.orders") || can("SELECT", "jd_b3_ops_sch.items") {
		t.Error("the grant did not give exactly SELECT and INSERT on the one table")
	}

	grants, _, err := ListGrants(ctx, db, DriverPostgres, GrantFilter{Role: "jd_b3_ops_app"})
	if err != nil {
		t.Fatal(err)
	}
	var onTable, onSchema *Grant
	for i := range grants {
		switch {
		case grants[i].Level == GrantOnTable && grants[i].Table == "orders":
			onTable = &grants[i]
		case grants[i].Level == GrantOnSchema && grants[i].Schema == "jd_b3_ops_sch":
			onSchema = &grants[i]
		}
	}
	if onTable == nil || strings.Join(onTable.Privileges, ",") != "INSERT,SELECT" || onTable.Owner || onTable.Database != database {
		t.Errorf("table grant listed as %+v", onTable)
	}
	if onSchema == nil || strings.Join(onSchema.Privileges, ",") != "USAGE" {
		t.Errorf("schema grant listed as %+v", onSchema)
	}

	change(PrivilegeChange{Level: GrantOnTable, Schema: "jd_b3_ops_sch", Table: "orders", Privileges: []string{"INSERT"}, Revoke: true})
	if !can("SELECT", "jd_b3_ops_sch.orders") || can("INSERT", "jd_b3_ops_sch.orders") {
		t.Error("the revoke did not take exactly INSERT")
	}

	// Every table in the schema, and the ones made later.
	stmts = change(PrivilegeChange{Level: GrantOnTable, Schema: "jd_b3_ops_sch", Privileges: []string{"SELECT"}, Future: true})
	if len(stmts) != 2 || !strings.HasPrefix(stmts[1], `ALTER DEFAULT PRIVILEGES IN SCHEMA "jd_b3_ops_sch" GRANT SELECT ON TABLES`) {
		t.Errorf("schema-wide grant ran %v", stmts)
	}
	opsMustExec(t, db, `CREATE TABLE jd_b3_ops_sch.later (id int)`)
	if !can("SELECT", "jd_b3_ops_sch.items") || !can("SELECT", "jd_b3_ops_sch.later") {
		t.Error("the schema-wide grant missed an existing or a later table")
	}

	change(PrivilegeChange{Level: GrantOnDatabase, Database: database, Privileges: []string{"TEMPORARY"}})
	change(PrivilegeChange{Level: GrantOnRole, MemberOf: "jd_b3_ops_team"})
	detail, err := ReadRoleDetail(ctx, db, DriverPostgres, "jd_b3_ops_app", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.MemberOf) != 1 || detail.MemberOf[0] != "jd_b3_ops_team" {
		t.Errorf("memberOf = %v", detail.MemberOf)
	}
	future := false
	for _, g := range detail.Grants {
		if g.Future && g.Schema == "jd_b3_ops_sch" && g.Level == GrantOnTable {
			future = true
		}
	}
	if !future {
		t.Errorf("the default privilege is not in the role's grants: %+v", detail.Grants)
	}
	team, err := ReadRoleDetail(ctx, db, DriverPostgres, "jd_b3_ops_team", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(team.Members) != 1 || team.Members[0] != "jd_b3_ops_app" {
		t.Errorf("members = %v", team.Members)
	}
	change(PrivilegeChange{Level: GrantOnRole, MemberOf: "jd_b3_ops_team", Revoke: true})

	// Who can do what on one object.
	onOrders, _, err := ListGrants(ctx, db, DriverPostgres, GrantFilter{Schema: "jd_b3_ops_sch", Table: "orders"})
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, g := range onOrders {
		if g.Table != "orders" {
			t.Errorf("a grant on %q came back for a filter on orders", g.Table)
		}
		seen = seen || g.Grantee == "jd_b3_ops_app"
	}
	if !seen {
		t.Errorf("the grantee is missing from the object's grants: %+v", onOrders)
	}

	for _, bad := range []PrivilegeChange{
		{Level: GrantOnTable, Schema: "jd_b3_ops_sch", Table: "orders", Privileges: []string{"SELECT; DROP TABLE x"}},
		{Level: GrantOnTable, Schema: "jd_b3_ops_sch", Table: "orders", Privileges: []string{"ALL", "SELECT"}},
		{Level: GrantOnTable, Table: "orders", Privileges: []string{"SELECT"}},
		{Level: "galaxy", Privileges: []string{"SELECT"}},
		{Level: GrantOnSchema, Schema: "jd_b3_ops_sch", Privileges: []string{"SELECT"}},
		{Level: GrantOnTable, Schema: "jd_b3_ops_sch", Table: "orders", Privileges: []string{}},
	} {
		bad.Role = "jd_b3_ops_app"
		if _, err := ChangePrivileges(ctx, db, DriverPostgres, bad, false); err == nil {
			t.Errorf("%+v was accepted", bad)
		} else if _, ok := err.(ErrPrivilegeRequest); !ok {
			t.Errorf("%+v reached the server: %v", bad, err)
		}
	}
}

// The one-step grant used to cover schema public and nothing else, so an
// application whose tables lived in a schema of its own was granted a
// database it could not read.
func TestLiveOpsPostgresDatabaseGrantCoversEverySchema(t *testing.T) {
	db, _ := opsLive(t, DriverPostgres, "JD_TEST_POSTGRES_DSN")
	ctx := t.Context()
	var database string
	if err := db.QueryRow(`SELECT current_database()`).Scan(&database); err != nil {
		t.Fatal(err)
	}
	cleanup := func() {
		db.Exec(`DROP SCHEMA IF EXISTS jd_b3_ops_app_data CASCADE`)
		db.Exec(`DROP OWNED BY jd_b3_ops_reader`)
		db.Exec(`DROP ROLE IF EXISTS jd_b3_ops_reader`)
	}
	cleanup()
	t.Cleanup(cleanup)
	opsMustExec(t, db, `CREATE ROLE jd_b3_ops_reader WITH LOGIN PASSWORD 'pw'`, `CREATE SCHEMA jd_b3_ops_app_data`,
		`CREATE TABLE jd_b3_ops_app_data.events (id int primary key)`)
	admin, _ := AdminFor(DriverPostgres)
	granted, err := admin.Grant(ctx, db, DatabaseGrant{Role: "jd_b3_ops_reader", Database: database, Level: GrantRead})
	if err != nil {
		t.Fatal(err)
	}
	if len(granted.Statements) < 5 || !containsString(granted.Schemas, "jd_b3_ops_app_data") || !containsString(granted.Schemas, "public") {
		t.Errorf("the grant ran %v over %v", granted.Statements, granted.Schemas)
	}
	var read, write bool
	if err := db.QueryRow(`SELECT has_table_privilege('jd_b3_ops_reader', 'jd_b3_ops_app_data.events', 'SELECT'),
	  has_table_privilege('jd_b3_ops_reader', 'jd_b3_ops_app_data.events', 'INSERT')`).Scan(&read, &write); err != nil {
		t.Fatal(err)
	}
	if !read || write {
		t.Errorf("read level outside public: select %v insert %v", read, write)
	}
	// A failure part way through leaves nothing behind.
	if _, err := admin.Grant(ctx, db, DatabaseGrant{Role: "jd_b3_ops_reader", Database: database, Schema: "jd_b3_no_such_schema", Level: GrantWrite}); err == nil {
		t.Fatal("a grant on a schema that does not exist succeeded")
	}
	if err := db.QueryRow(`SELECT has_table_privilege('jd_b3_ops_reader', 'jd_b3_ops_app_data.events', 'INSERT')`).Scan(&write); err != nil || write {
		t.Errorf("a failed grant left a privilege behind: %v %v", write, err)
	}
}

// "This database" stops at a schema an extension owns, says which it left
// out, and reaches it when it is named. A preview answers the same without
// granting anything.
func TestLiveOpsPostgresDatabaseGrantLeavesExtensionSchemasAlone(t *testing.T) {
	db, _ := opsLive(t, DriverPostgres, "JD_TEST_POSTGRES_DSN")
	ctx := t.Context()
	var database string
	var had bool
	if err := db.QueryRow(`SELECT current_database(), EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'fuzzystrmatch')`).Scan(&database, &had); err != nil {
		t.Fatal(err)
	}
	cleanup := func() {
		db.Exec(`ALTER EXTENSION fuzzystrmatch DROP SCHEMA jd_b3_ops_ext`)
		db.Exec(`DROP SCHEMA IF EXISTS jd_b3_ops_ext CASCADE`)
		db.Exec(`DROP SCHEMA IF EXISTS jd_b3_ops_own CASCADE`)
		db.Exec(`DROP OWNED BY jd_b3_ops_scoped`)
		db.Exec(`DROP ROLE IF EXISTS jd_b3_ops_scoped`)
		if !had {
			db.Exec(`DROP EXTENSION IF EXISTS fuzzystrmatch`)
		}
	}
	cleanup()
	t.Cleanup(cleanup)
	if _, err := db.Exec(`CREATE EXTENSION IF NOT EXISTS fuzzystrmatch`); err != nil {
		t.Skipf("no contrib extension to own a schema: %v", err)
	}
	// A schema is the extension's when the catalogue says it is a member,
	// which is how pg_cron's and TimescaleDB's come to be theirs.
	opsMustExec(t, db, `CREATE ROLE jd_b3_ops_scoped WITH LOGIN PASSWORD 'pw'`,
		`CREATE SCHEMA jd_b3_ops_ext`, `CREATE TABLE jd_b3_ops_ext.job (id int primary key, command text)`,
		`ALTER EXTENSION fuzzystrmatch ADD SCHEMA jd_b3_ops_ext`,
		`CREATE SCHEMA jd_b3_ops_own`, `CREATE TABLE jd_b3_ops_own.orders (id int primary key)`)
	admin, _ := AdminFor(DriverPostgres)
	can := func(table string) bool {
		t.Helper()
		var ok bool
		if err := db.QueryRow(`SELECT has_table_privilege('jd_b3_ops_scoped', $1, 'SELECT')`, table).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}

	preview, err := admin.Grant(ctx, db, DatabaseGrant{Role: "jd_b3_ops_scoped", Database: database, Level: GrantRead, Preview: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Statements) == 0 || can("jd_b3_ops_own.orders") {
		t.Fatalf("a preview granted, or rendered nothing: %v", preview.Statements)
	}
	granted, err := admin.Grant(ctx, db, DatabaseGrant{Role: "jd_b3_ops_scoped", Database: database, Level: GrantRead})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(granted.Statements, "\n") != strings.Join(preview.Statements, "\n") {
		t.Errorf("the grant ran something other than its preview:\n%v\n%v", granted.Statements, preview.Statements)
	}
	if !containsString(granted.Schemas, "jd_b3_ops_own") || containsString(granted.Schemas, "jd_b3_ops_ext") {
		t.Errorf("schemas covered: %v", granted.Schemas)
	}
	var skipped *SkippedSchema
	for i := range granted.SkippedSchemas {
		if granted.SkippedSchemas[i].Name == "jd_b3_ops_ext" {
			skipped = &granted.SkippedSchemas[i]
		}
	}
	if skipped == nil || !strings.Contains(skipped.Reason, "fuzzystrmatch") {
		t.Errorf("the extension's schema is not reported as left out: %+v", granted.SkippedSchemas)
	}
	if !can("jd_b3_ops_own.orders") || can("jd_b3_ops_ext.job") {
		t.Errorf("after the grant: own schema %v, extension schema %v", can("jd_b3_ops_own.orders"), can("jd_b3_ops_ext.job"))
	}
	if strings.Contains(strings.Join(granted.Statements, "\n"), "jd_b3_ops_ext") {
		t.Errorf("a statement names the extension's schema: %v", granted.Statements)
	}
	// Named, it is granted: the operator said which schema they meant.
	named, err := admin.Grant(ctx, db, DatabaseGrant{Role: "jd_b3_ops_scoped", Database: database, Schema: "jd_b3_ops_ext", Level: GrantRead})
	if err != nil {
		t.Fatal(err)
	}
	if len(named.Schemas) != 1 || named.Schemas[0] != "jd_b3_ops_ext" || len(named.SkippedSchemas) != 0 || !can("jd_b3_ops_ext.job") {
		t.Errorf("a named schema: %+v, readable %v", named, can("jd_b3_ops_ext.job"))
	}
}

// A grant on a schema "and what is created in it later" has to reach the
// tables somebody else creates there — which is who creates them. It used to
// cover only what the dashboard's own account made.
func TestLiveOpsPostgresFutureGrantCoversOtherOwners(t *testing.T) {
	db, _ := opsLive(t, DriverPostgres, "JD_TEST_POSTGRES_DSN")
	ctx := t.Context()
	cleanup := func() {
		db.Exec(`DROP SCHEMA IF EXISTS jd_b3_ops_fut CASCADE`)
		for _, role := range []string{"jd_b3_ops_fut_reader", "jd_b3_ops_fut_owner", "jd_b3_ops_fut_migrator"} {
			db.Exec(`DROP OWNED BY ` + role)
			db.Exec(`DROP ROLE IF EXISTS ` + role)
		}
	}
	cleanup()
	t.Cleanup(cleanup)
	// One role owns the schema and has made nothing in it yet; another has a
	// table there. Neither is the account the dashboard signs in with.
	opsMustExec(t, db, `CREATE ROLE jd_b3_ops_fut_reader WITH LOGIN PASSWORD 'pw'`, `CREATE ROLE jd_b3_ops_fut_owner`,
		`CREATE ROLE jd_b3_ops_fut_migrator`, `CREATE SCHEMA jd_b3_ops_fut AUTHORIZATION jd_b3_ops_fut_owner`,
		`GRANT ALL ON SCHEMA jd_b3_ops_fut TO jd_b3_ops_fut_migrator`,
		`CREATE TABLE jd_b3_ops_fut.existing (id int primary key)`,
		`ALTER TABLE jd_b3_ops_fut.existing OWNER TO jd_b3_ops_fut_migrator`)
	var me string
	if err := db.QueryRow(`SELECT current_user`).Scan(&me); err != nil {
		t.Fatal(err)
	}
	change := PrivilegeChange{Role: "jd_b3_ops_fut_reader", Level: GrantOnTable, Schema: "jd_b3_ops_fut",
		Privileges: []string{"SELECT"}, Future: true}

	preview, err := ChangePrivileges(ctx, db, DriverPostgres, change, true)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{me, "jd_b3_ops_fut_migrator", "jd_b3_ops_fut_owner"}
	if strings.Join(preview.FutureOwners, ",") != strings.Join(want, ",") || len(preview.Statements) != 4 {
		t.Fatalf("preview: owners %v, statements %v", preview.FutureOwners, preview.Statements)
	}
	var defaults int
	if err := db.QueryRow(`SELECT count(*) FROM pg_default_acl a JOIN pg_namespace n ON n.oid = a.defaclnamespace WHERE n.nspname = 'jd_b3_ops_fut'`).Scan(&defaults); err != nil || defaults != 0 {
		t.Fatalf("a preview wrote %d default privileges (%v)", defaults, err)
	}

	out, err := ChangePrivileges(ctx, db, DriverPostgres, change, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(out.Statements, "\n") != strings.Join(preview.Statements, "\n") ||
		!containsString(out.Statements, `ALTER DEFAULT PRIVILEGES FOR ROLE "jd_b3_ops_fut_owner" IN SCHEMA "jd_b3_ops_fut" GRANT SELECT ON TABLES TO "jd_b3_ops_fut_reader"`) {
		t.Errorf("the change ran %v", out.Statements)
	}
	// The next migration, as each of the roles that run one.
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for role, table := range map[string]string{"jd_b3_ops_fut_owner": "by_owner", "jd_b3_ops_fut_migrator": "by_migrator"} {
		for _, stmt := range []string{`SET ROLE ` + role, `CREATE TABLE jd_b3_ops_fut.` + table + ` (id int)`, `RESET ROLE`} {
			if _, err := conn.ExecContext(ctx, stmt); err != nil {
				t.Fatalf("%s: %v", stmt, err)
			}
		}
		var ok bool
		if err := db.QueryRow(`SELECT has_table_privilege('jd_b3_ops_fut_reader', $1, 'SELECT')`, "jd_b3_ops_fut."+table).Scan(&ok); err != nil || !ok {
			t.Errorf("a table %s created later is not readable (%v)", role, err)
		}
	}

	// The revoke takes the same defaults away again.
	change.Revoke = true
	if _, err := ChangePrivileges(ctx, db, DriverPostgres, change, false); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM pg_default_acl a JOIN pg_namespace n ON n.oid = a.defaclnamespace WHERE n.nspname = 'jd_b3_ops_fut'`).Scan(&defaults); err != nil || defaults != 0 {
		t.Errorf("%d default privileges survived the revoke (%v)", defaults, err)
	}
}

// A poll of the snapshot must not make the server log an error. On every
// version before 17 it used to: the newer checkpoint view was tried first and
// its absence caught, and PostgreSQL writes a failed statement to its log —
// thousands of lines a day into the log this dashboard's own Logs page reads.
// A failed statement is also a rolled-back transaction, which is counted, and
// in a database nobody else is connected to the count is exact.
func TestLiveOpsPostgresStatsRaisesNoErrorOnTheServer(t *testing.T) {
	db, dsn := opsLive(t, DriverPostgres, "JD_TEST_POSTGRES_DSN")
	ctx := t.Context()
	var canFlush bool
	if err := db.QueryRow(`SELECT to_regproc('pg_stat_force_next_flush') IS NOT NULL`).Scan(&canFlush); err != nil || !canFlush {
		t.Skipf("pg_stat_force_next_flush is not available here (PostgreSQL 15+): %v", err)
	}
	db.Exec(`DROP DATABASE IF EXISTS jd_b3_ops_quiet`)
	opsMustExec(t, db, `CREATE DATABASE jd_b3_ops_quiet`)
	t.Cleanup(func() { db.Exec(`DROP DATABASE IF EXISTS jd_b3_ops_quiet`) })
	quiet, err := OpenDatabase(ctx, DriverPostgres, dsn, "jd_b3_ops_quiet")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { quiet.Close() })
	// One backend, so that what it counted is flushed when it is told to.
	quiet.SetMaxOpenConns(1)
	rollbacks := func() int64 {
		t.Helper()
		opsMustExec(t, quiet, `SELECT pg_stat_force_next_flush()`)
		var n int64
		if err := quiet.QueryRow(`SELECT xact_rollback FROM pg_stat_database WHERE datname = current_database()`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := rollbacks()
	for i := 0; i < 3; i++ {
		stats, err := ReadServerStats(ctx, quiet, DriverPostgres)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := stats.Counters["checkpointsTimed"]; !ok {
			t.Errorf("the checkpoint counters are missing: %v", stats.Counters)
		}
	}
	if _, err := TopStatements(ctx, quiet, DriverPostgres, StatementsOptions{}); err != nil {
		t.Fatal(err)
	}
	if after := rollbacks(); after != before {
		t.Errorf("three polls and a statements read made %d statements fail on the server", after-before)
	}
}

// Closing the request stops the command on the server, not only the wait for
// it: a statement left running would hold its lock to the end with nobody
// watching. The driver sends the cancel request; this is what says it does.
func TestLiveOpsPostgresMaintenanceStopsWithTheRequest(t *testing.T) {
	db, dsn := opsLive(t, DriverPostgres, "JD_TEST_POSTGRES_DSN")
	d := postgresDialect{}
	// Stands in for a VACUUM FULL of a large table: a statement that would
	// run for a minute, recognisable in the session list.
	const long = `SELECT pg_sleep(60) /* jd_b3_ops_stop */`
	running := func() int {
		t.Helper()
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE state = 'active' AND query = $1`, long).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- pgRunWithNotices(ctx, d.NormaliseDSN(dsn), long, &MaintenanceResult{})
	}()
	waitFor(t, "the statement to start", func() bool { return running() == 1 })
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Error("the cancelled statement reported success")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the cancelled statement never returned")
	}
	// Well inside the minute it had left.
	deadline := time.Now().Add(3 * time.Second)
	for running() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the statement is still running on the server after its request ended")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestLiveOpsPostgresStatements(t *testing.T) {
	db, _ := opsLive(t, DriverPostgres, "JD_TEST_POSTGRES_DSN")
	ctx := t.Context()
	report, err := TopStatements(ctx, db, DriverPostgres, StatementsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Supported {
		if report.Enable == nil || report.Enable.Extension != "pg_stat_statements" {
			t.Fatalf("no statistics and no way to enable them: %+v", report)
		}
		t.Skipf("pg_stat_statements is not enabled here: %s", report.Reason)
	}
	if _, err := ResetStatements(ctx, db, DriverPostgres); err != nil {
		t.Fatal(err)
	}
	// The extension stores a statement with its constants replaced, so the
	// probe is recognised by the table and column it reads.
	for i := 0; i < 5; i++ {
		opsMustExec(t, db, `SELECT count(*) FROM pg_class WHERE relname = 'jd_b3_ops_statement_probe'`)
	}
	opsMustExec(t, db, `SELECT pg_sleep(0.05)`)
	byCalls, err := TopStatements(ctx, db, DriverPostgres, StatementsOptions{Sort: StatementsByCalls, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if byCalls.Sort != StatementsByCalls || !byCalls.Resettable || len(byCalls.Statements) == 0 || len(byCalls.Statements) > 5 {
		t.Fatalf("by calls: %+v", byCalls)
	}
	if !strings.Contains(byCalls.Statements[0].Query, "FROM pg_class WHERE relname = $1") || byCalls.Statements[0].Calls < 5 {
		t.Errorf("the most-called statement is %+v", byCalls.Statements[0])
	}
	var share float64
	for i, s := range byCalls.Statements {
		share += s.Share
		if s.Share < 0 || s.Share > 1 {
			t.Errorf("share out of range: %+v", s)
		}
		if i > 0 && s.Calls > byCalls.Statements[i-1].Calls {
			t.Errorf("not ordered by calls at %d", i)
		}
	}
	if share <= 0 || share > 1.0001 {
		t.Errorf("shares sum to %v", share)
	}
	byMean, err := TopStatements(ctx, db, DriverPostgres, StatementsOptions{Sort: StatementsByMean, Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(byMean.Statements) == 0 || !strings.Contains(byMean.Statements[0].Query, "pg_sleep") {
		t.Errorf("the slowest statement on average is %+v", byMean.Statements)
	}
	if _, err := TopStatements(ctx, db, DriverPostgres, StatementsOptions{Sort: "calls desc; drop"}); err == nil {
		t.Error("an unknown sort was accepted")
	}
}

func TestLiveOpsPostgresAdvisor(t *testing.T) {
	db, _ := opsLive(t, DriverPostgres, "JD_TEST_POSTGRES_DSN")
	opsMustExec(t, db, `DROP TABLE IF EXISTS jd_b3_ops_adv_child`, `DROP TABLE IF EXISTS jd_b3_ops_adv`,
		`CREATE TABLE jd_b3_ops_adv (id int primary key, a int)`,
		`CREATE INDEX jd_b3_ops_adv_a ON jd_b3_ops_adv (a)`, `CREATE INDEX jd_b3_ops_adv_a2 ON jd_b3_ops_adv (a)`,
		`CREATE INDEX jd_b3_ops_adv_e1 ON jd_b3_ops_adv ((a + 1))`, `CREATE INDEX jd_b3_ops_adv_e2 ON jd_b3_ops_adv ((a + 2))`,
		`CREATE TABLE jd_b3_ops_adv_child (v int, parent int REFERENCES jd_b3_ops_adv(id))`)
	// A slot nothing reads from is the quiet way a disk fills.
	db.Exec(`SELECT pg_drop_replication_slot('jd_b3_ops_idle_slot')`)
	slotMade := true
	if _, err := db.Exec(`SELECT pg_create_physical_replication_slot('jd_b3_ops_idle_slot', true)`); err != nil {
		slotMade = false
		t.Logf("no slot could be made here: %v", err)
	}
	t.Cleanup(func() {
		db.Exec(`DROP TABLE IF EXISTS jd_b3_ops_adv_child`)
		db.Exec(`DROP TABLE IF EXISTS jd_b3_ops_adv`)
		db.Exec(`SELECT pg_drop_replication_slot('jd_b3_ops_idle_slot')`)
	})
	report, err := Advise(t.Context(), db, DriverPostgres, "public")
	if err != nil {
		t.Fatal(err)
	}
	if !report.EngineChecks || report.Version == "" || report.Truncated {
		t.Fatalf("report: %+v", report)
	}
	by := map[string]Advice{}
	for _, f := range report.Findings {
		by[f.ID] = f
		switch f.Category {
		case AdviceSecurity, AdvicePerformance, AdviceReliability, AdviceMaintenance:
		default:
			t.Errorf("%s has category %q", f.ID, f.Category)
		}
		if len(f.Targets) == 0 {
			t.Errorf("%s names no object", f.ID)
		}
	}
	dup, ok := by["duplicate-index"]
	if !ok {
		t.Fatalf("the duplicate index was not found: %v", report.Findings)
	}
	// One set: the two indexes on (a). The two expression indexes differ and
	// used to be reported as a second set.
	mine := 0
	for _, o := range dup.Objects {
		if strings.HasPrefix(o, "jd_b3_ops_adv:") {
			mine++
			if !strings.Contains(o, "jd_b3_ops_adv_a, jd_b3_ops_adv_a2") {
				t.Errorf("the set is %q", o)
			}
		}
	}
	if mine != 1 {
		t.Errorf("%d duplicate sets on the table, want 1: %v", mine, dup.Objects)
	}
	if !strings.Contains(dup.SQL, `DROP INDEX "public"."jd_b3_ops_adv_a2";`) {
		t.Errorf("the fix is %q", dup.SQL)
	}
	if fk, ok := by["unindexed-foreign-key"]; !ok || !strings.Contains(fk.SQL, `ON "public"."jd_b3_ops_adv_child" ("parent")`) {
		t.Errorf("unindexed foreign key: %+v", fk)
	}
	if pk, ok := by["no-primary-key"]; !ok || pk.Category != AdviceReliability {
		t.Errorf("no primary key: %+v", pk)
	}
	if slotMade {
		slot, ok := by["inactive-replication-slot"]
		if !ok || !strings.Contains(slot.SQL, `SELECT pg_drop_replication_slot('jd_b3_ops_idle_slot');`) {
			t.Errorf("inactive slot: %+v", slot)
		}
	}
	// The account asking is never told to demote itself.
	var me string
	if err := db.QueryRow(`SELECT current_user`).Scan(&me); err != nil {
		t.Fatal(err)
	}
	if supers, ok := by["extra-superusers"]; ok {
		for _, name := range supers.Objects {
			if name == me {
				t.Errorf("the advisor suggests demoting the connection's own role: %+v", supers)
			}
		}
	}
}

// A fix the advisor hands over has to be one the server accepts and that makes
// the finding go away: each of these is taken from the report, run as it
// stands, and the report read again.
func TestLiveOpsPostgresAdvisorFixesRun(t *testing.T) {
	db, _ := opsLive(t, DriverPostgres, "JD_TEST_POSTGRES_DSN")
	ctx := t.Context()
	drop := func() {
		for _, table := range []string{"jd_b3_fix_keyed", "jd_b3_fix_bare", "jd_b3_fix_serial", "jd_b3_fix_identity"} {
			db.Exec(`DROP TABLE IF EXISTS ` + table)
		}
		db.Exec(`DROP SEQUENCE IF EXISTS jd_b3_fix_capped`)
	}
	drop()
	t.Cleanup(drop)
	opsMustExec(t, db,
		// A key in everything but name, and a table with nothing to be one.
		`CREATE TABLE jd_b3_fix_keyed (code text NOT NULL, note text)`,
		`CREATE UNIQUE INDEX jd_b3_fix_keyed_code ON jd_b3_fix_keyed (code)`,
		`CREATE TABLE jd_b3_fix_bare (v int, note text)`,
		`INSERT INTO jd_b3_fix_bare VALUES (1, 'a'), (2, 'b')`,
		// Three sequences near their ceilings: a serial column's, an identity
		// column's, and a bigint one somebody capped by hand.
		`CREATE TABLE jd_b3_fix_serial (id serial PRIMARY KEY, v int)`,
		`SELECT setval('jd_b3_fix_serial_id_seq', 2000000000)`,
		`CREATE TABLE jd_b3_fix_identity (id int GENERATED ALWAYS AS IDENTITY PRIMARY KEY, v int)`,
		`SELECT setval('jd_b3_fix_identity_id_seq', 2100000000)`,
		`CREATE SEQUENCE jd_b3_fix_capped MAXVALUE 1000`,
		`SELECT setval('jd_b3_fix_capped', 900)`)

	find := func(id string) (Advice, map[string]AdviceTarget) {
		t.Helper()
		report, err := Advise(ctx, db, DriverPostgres, "public")
		if err != nil {
			t.Fatal(err)
		}
		targets := map[string]AdviceTarget{}
		for _, f := range report.Findings {
			if f.ID != id {
				continue
			}
			for _, target := range f.Targets {
				if strings.HasPrefix(target.Name, "jd_b3_fix_") {
					targets[target.Name] = target
				}
			}
			return f, targets
		}
		return Advice{}, targets
	}
	runFix := func(target AdviceTarget) {
		t.Helper()
		if target.SQL == "" {
			t.Fatalf("%s carries no fix", target.Name)
		}
		for _, stmt := range strings.Split(target.SQL, "\n") {
			opsMustExec(t, db, stmt)
		}
	}

	finding, keyless := find("no-primary-key")
	if keyed := keyless["jd_b3_fix_keyed"]; keyed.SQL != `ALTER TABLE "public"."jd_b3_fix_keyed" ADD CONSTRAINT "jd_b3_fix_keyed_pkey" PRIMARY KEY USING INDEX "jd_b3_fix_keyed_code";` {
		t.Errorf("a table with a unique index: %+v", keyed)
	}
	if bare := keyless["jd_b3_fix_bare"]; bare.SQL != `ALTER TABLE "public"."jd_b3_fix_bare" ADD COLUMN id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY;` {
		t.Errorf("a table with nothing to be a key: %+v", bare)
	}
	if !strings.Contains(finding.SQL, "jd_b3_fix_keyed_pkey") || !strings.Contains(finding.SQL, "jd_b3_fix_bare") {
		t.Errorf("the finding's own fix is %q", finding.SQL)
	}
	runFix(keyless["jd_b3_fix_keyed"])
	runFix(keyless["jd_b3_fix_bare"])
	// The insert the application already makes, naming no columns, still works.
	opsMustExec(t, db, `INSERT INTO jd_b3_fix_bare VALUES (3, 'c')`)
	if _, keyless = find("no-primary-key"); len(keyless) != 0 {
		t.Errorf("still without a key after the fix: %v", keyless)
	}

	_, sequences := find("sequence-exhaustion")
	if len(sequences) != 3 {
		t.Fatalf("sequences near their ceiling: %v", sequences)
	}
	if got := sequences["jd_b3_fix_serial_id_seq"]; got.Table != "jd_b3_fix_serial" ||
		got.SQL != "ALTER TABLE \"public\".\"jd_b3_fix_serial\" ALTER COLUMN \"id\" TYPE bigint;\nALTER SEQUENCE \"public\".\"jd_b3_fix_serial_id_seq\" AS bigint;" {
		t.Errorf("serial: %+v", got)
	}
	if got := sequences["jd_b3_fix_capped"]; got.SQL != `ALTER SEQUENCE "public"."jd_b3_fix_capped" NO MAXVALUE;` {
		t.Errorf("capped: %+v", got)
	}
	for _, target := range sequences {
		runFix(target)
	}
	if _, sequences = find("sequence-exhaustion"); len(sequences) != 0 {
		t.Errorf("still near their ceiling after the fix: %v", sequences)
	}
	for _, table := range []string{"jd_b3_fix_serial", "jd_b3_fix_identity"} {
		var column, sequence string
		if err := db.QueryRow(`
		  SELECT format_type(a.atttypid, NULL), s.data_type::text
		  FROM pg_attribute a, pg_sequences s
		  WHERE a.attrelid = $1::regclass AND a.attname = 'id' AND s.sequencename = $1 || '_id_seq'`, table).Scan(&column, &sequence); err != nil {
			t.Fatal(err)
		}
		if column != "bigint" || sequence != "bigint" {
			t.Errorf("%s: column %s, sequence %s", table, column, sequence)
		}
	}
}

// --- MySQL / MariaDB ---------------------------------------------------------

func TestLiveOpsMySQL(t *testing.T) {
	for flavour, dsn := range mysqlOpsServers(t) {
		t.Run(flavour, func(t *testing.T) {
			db := opsOpen(t, DriverMySQL, dsn, flavour)
			ctx := t.Context()
			maria := mysqlIsMariaDB(ctx, db)
			if maria != (flavour == "mariadb") {
				t.Fatalf("%s reports itself as MariaDB: %v", flavour, maria)
			}
			opsMustExec(t, db, `DROP TABLE IF EXISTS jd_b3_ops_t`,
				`CREATE TABLE jd_b3_ops_t (id INT PRIMARY KEY, a VARCHAR(64), b INT,
				   KEY jd_b3_ops_t_b (b), KEY jd_b3_ops_t_b_copy (b), KEY jd_b3_ops_t_ba (b, a)) ENGINE=InnoDB`,
				`INSERT INTO jd_b3_ops_t VALUES (1, 'x', 1), (2, 'y', 2), (3, 'z', 3)`)
			t.Cleanup(func() { db.Exec(`DROP TABLE IF EXISTS jd_b3_ops_t`) })

			t.Run("stats", func(t *testing.T) {
				stats, err := ReadServerStats(ctx, db, DriverMySQL)
				if err != nil {
					t.Fatal(err)
				}
				if stats.Version == "" || stats.StartedAt == nil || stats.Connections == nil || stats.Connections.Max <= 0 || stats.DatabaseBytes <= 0 {
					t.Fatalf("snapshot: %+v", stats)
				}
				for _, key := range []string{StatTransactionsCommitted, StatRowsRead, StatRowsWritten, StatQueries, StatBlocksHit, StatBlocksRead, StatDeadlocks} {
					if _, ok := stats.Counters[key]; !ok {
						t.Errorf("counter %q is missing", key)
					}
				}
				if stats.Role != "standalone" && stats.Role != "primary" && stats.Role != "replica" {
					t.Errorf("role = %q", stats.Role)
				}
			})

			t.Run("sessions_and_locks", func(t *testing.T) {
				holder, err := db.Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Close()
				waiter, err := db.Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer waiter.Close()
				var holderPID, waiterPID string
				if err := holder.QueryRowContext(ctx, `SELECT CAST(CONNECTION_ID() AS CHAR)`).Scan(&holderPID); err != nil {
					t.Fatal(err)
				}
				if err := waiter.QueryRowContext(ctx, `SELECT CAST(CONNECTION_ID() AS CHAR)`).Scan(&waiterPID); err != nil {
					t.Fatal(err)
				}
				if _, err := holder.ExecContext(ctx, `BEGIN`); err != nil {
					t.Fatal(err)
				}
				if _, err := holder.ExecContext(ctx, `UPDATE jd_b3_ops_t SET b = 10 WHERE id = 1`); err != nil {
					t.Fatal(err)
				}
				defer holder.ExecContext(context.Background(), `ROLLBACK`)
				blocked := make(chan error, 1)
				go func() {
					_, err := waiter.ExecContext(context.Background(), `UPDATE jd_b3_ops_t SET b = 20 WHERE id = 1`)
					blocked <- err
				}()
				var sessions []Activity
				waitFor(t, "the waiter to be blocked", func() bool {
					sessions, err = ListActivity(ctx, db, DriverMySQL)
					if err != nil {
						t.Fatal(err)
					}
					w := findSession(sessions, waiterPID)
					return w != nil && w.Status == SessionBlocked
				})
				h, w := findSession(sessions, holderPID), findSession(sessions, waiterPID)
				if h == nil || h.Status != SessionIdleInTransaction || h.Seconds != 0 || h.TransactionStart == nil {
					t.Errorf("holder: %+v", h)
				}
				if len(w.BlockedByPIDs) != 1 || w.BlockedByPIDs[0] != holderPID {
					t.Errorf("waiter blocked by %v, want %s", w.BlockedByPIDs, holderPID)
				}
				for _, s := range sessions {
					if s.User == "event_scheduler" && (s.Status != SessionBackground || s.Seconds != 0) {
						t.Errorf("the event scheduler reads as a long-running query: %+v", s)
					}
				}

				locks, err := ListLocks(ctx, db, DriverMySQL)
				if err != nil {
					t.Fatal(err)
				}
				if !locks.Supported {
					t.Fatalf("locks unsupported: %s", locks.Reason)
				}
				var wait *LockWait
				for i := range locks.Waits {
					if locks.Waits[i].WaitingPID == waiterPID {
						wait = &locks.Waits[i]
					}
				}
				if wait == nil || wait.BlockingPID != holderPID || !strings.Contains(wait.Object, "jd_b3_ops_t") {
					t.Fatalf("wait: %+v in %+v", wait, locks.Waits)
				}
				if wait.WaitingUser == "" || wait.Mode == "" {
					t.Errorf("wait is missing its detail: %+v", wait)
				}

				// KILL QUERY ends the statement and keeps the connection.
				if err := CancelQuery(ctx, db, DriverMySQL, waiterPID); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-blocked:
					if err == nil {
						t.Error("the cancelled statement succeeded")
					}
				case <-time.After(10 * time.Second):
					t.Fatal("the cancelled statement never returned")
				}
				var alive int
				if err := waiter.QueryRowContext(ctx, `SELECT 1`).Scan(&alive); err != nil {
					t.Errorf("the session did not survive a cancel: %v", err)
				}
			})

			t.Run("replication", func(t *testing.T) {
				rep, err := ReadReplication(ctx, db, DriverMySQL)
				if err != nil {
					t.Fatal(err)
				}
				if !rep.Supported || rep.Role != "standalone" || rep.Facts["server_id"] == "" {
					t.Errorf("replication: %+v", rep)
				}
				if len(rep.Notes) != 0 {
					t.Errorf("root was refused part of the report: %v", rep.Notes)
				}
			})

			t.Run("table_and_index_stats", func(t *testing.T) {
				opsMustExec(t, db, `ANALYZE TABLE jd_b3_ops_t`)
				tables, err := ReadTableStats(ctx, db, DriverMySQL, StatsOptions{})
				if err != nil {
					t.Fatal(err)
				}
				var stat *TableStat
				for i := range tables.Tables {
					if tables.Tables[i].Table == "jd_b3_ops_t" {
						stat = &tables.Tables[i]
					}
				}
				if stat == nil || stat.Engine != "InnoDB" || stat.TotalBytes <= 0 || stat.IndexBytes <= 0 || stat.SeqScans != -1 {
					t.Fatalf("table stat: %+v", stat)
				}
				indexes, err := ReadIndexStats(ctx, db, DriverMySQL, StatsOptions{Table: "jd_b3_ops_t"})
				if err != nil {
					t.Fatal(err)
				}
				by := map[string]IndexStat{}
				for _, ix := range indexes.Indexes {
					by[ix.Name] = ix
				}
				if len(by) != 4 {
					t.Fatalf("indexes: %+v", indexes.Indexes)
				}
				if !by["PRIMARY"].Primary || !by["PRIMARY"].Unique || by["PRIMARY"].Bytes <= 0 {
					t.Errorf("primary: %+v", by["PRIMARY"])
				}
				if (by["jd_b3_ops_t_b"].DuplicateOf == "") == (by["jd_b3_ops_t_b_copy"].DuplicateOf == "") {
					t.Errorf("identical indexes: %+v %+v", by["jd_b3_ops_t_b"], by["jd_b3_ops_t_b_copy"])
				}
				if cols := by["jd_b3_ops_t_ba"].Columns; len(cols) != 2 || cols[0] != "b" || cols[1] != "a" {
					t.Errorf("columns: %v", cols)
				}
				// Without performance_schema nothing is counted, and nothing
				// may then be called unused.
				if maria && (by["jd_b3_ops_t_ba"].Scans != -1 || by["jd_b3_ops_t_ba"].Unused) {
					t.Errorf("an uncounted index reads as unused: %+v", by["jd_b3_ops_t_ba"])
				}
				if !maria && by["jd_b3_ops_t_ba"].Scans != 0 {
					t.Errorf("scan count: %+v", by["jd_b3_ops_t_ba"])
				}
			})

			t.Run("maintenance", func(t *testing.T) {
				for _, action := range []string{"analyze", "check", "optimize"} {
					res, err := RunMaintenance(ctx, db, DriverMySQL, dsn, MaintenanceRequest{Action: action, Table: "jd_b3_ops_t"})
					if err != nil {
						t.Fatalf("%s: %v", action, err)
					}
					if !res.OK || len(res.Output) == 0 || !strings.Contains(res.Output[len(res.Output)-1], "OK") {
						t.Errorf("%s: %+v", action, res)
					}
					if !strings.HasSuffix(res.Statements[0], "TABLE `jd_b3_ops_t`") {
						t.Errorf("%s ran %v", action, res.Statements)
					}
				}
				whole, err := RunMaintenance(ctx, db, DriverMySQL, dsn, MaintenanceRequest{Action: "analyze"})
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(strings.Join(whole.Statements, " "), "`jd_b3_ops_t`") {
					t.Errorf("a whole-database analyze skipped the table: %v", whole.Statements)
				}
				if action, ok := MaintenanceActionFor(DriverMySQL, "repair"); !ok || !action.Destructive {
					t.Errorf("repair is not marked destructive: %+v", action)
				}
			})

			t.Run("settings", func(t *testing.T) {
				all, err := ListSettings(ctx, db, DriverMySQL, true)
				if err != nil {
					t.Fatal(err)
				}
				if len(all) < 200 {
					t.Fatalf("the full list has %d variables", len(all))
				}
				var current *Setting
				for i := range all {
					if all[i].Name == "max_connect_errors" {
						current = &all[i]
					}
				}
				if current == nil || current.Type != "integer" || !current.Editable {
					t.Fatalf("max_connect_errors: %+v", current)
				}
				t.Cleanup(func() {
					db.Exec(`RESET PERSIST IF EXISTS max_connect_errors`)
					db.Exec(`SET GLOBAL max_connect_errors = DEFAULT`)
				})
				change, err := ChangeSetting(ctx, db, DriverMySQL, "max_connect_errors", "4321", false)
				if err != nil {
					t.Fatal(err)
				}
				if change.Value != "4321" || change.Persisted == maria {
					t.Errorf("change: %+v", change)
				}
				if maria && change.Note == "" {
					t.Errorf("MariaDB does not persist and the answer does not say so")
				}
				reset, err := ChangeSetting(ctx, db, DriverMySQL, "max_connect_errors", "", true)
				if err != nil {
					t.Fatal(err)
				}
				if reset.Value == "4321" {
					t.Errorf("reset left the value: %+v", reset)
				}
				for name, value := range map[string]string{"max_connect_errors": "lots", "no_such_variable": "1", "max_connect_errors = 1, x": "1"} {
					if _, err := ChangeSetting(ctx, db, DriverMySQL, name, value, false); err == nil {
						t.Errorf("%s = %q was accepted", name, value)
					} else if _, ok := err.(ErrSettingRequest); !ok {
						t.Errorf("%s = %q reached the server: %v", name, value, err)
					}
				}
				short, err := ListSettings(ctx, db, DriverMySQL, false)
				if err != nil || len(short) == 0 || len(short) > len(mysqlSettingNames) {
					t.Errorf("short list: %d, %v", len(short), err)
				}
			})

			t.Run("roles_and_grants", func(t *testing.T) {
				var database string
				if err := db.QueryRow(`SELECT DATABASE()`).Scan(&database); err != nil {
					t.Fatal(err)
				}
				db.Exec(`DROP USER IF EXISTS 'jd_b3_ops_u'@'10.%'`)
				t.Cleanup(func() { db.Exec(`DROP USER IF EXISTS 'jd_b3_ops_u'@'10.%'`) })
				admin, _ := AdminFor(DriverMySQL)
				if err := admin.CreateRole(ctx, db, RoleSpec{Name: "jd_b3_ops_u", Host: "10.%", Password: "first-pw", SetPassword: true, Login: true}); err != nil {
					t.Fatal(err)
				}
				granted, err := admin.Grant(ctx, db, DatabaseGrant{Role: "jd_b3_ops_u", Host: "10.%", Database: database, Level: GrantRead})
				if err != nil {
					t.Fatal(err)
				}
				stmts := granted.Statements
				// The underscore in the name is escaped, or the grant would
				// also cover every database one character different.
				if want := strings.ReplaceAll(database, "_", `\_`); !strings.Contains(stmts[0], "`"+want+"`.*") {
					t.Errorf("the database is not escaped in %q", stmts[0])
				}
				if _, err := ChangePrivileges(ctx, db, DriverMySQL, PrivilegeChange{Role: "jd_b3_ops_u", Host: "10.%",
					Level: GrantOnTable, Database: database, Table: "jd_b3_ops_t", Privileges: []string{"update", "DELETE"}}, false); err != nil {
					t.Fatal(err)
				}
				detail, err := ReadRoleDetail(ctx, db, DriverMySQL, "jd_b3_ops_u", "10.%")
				if err != nil {
					t.Fatal(err)
				}
				var onDatabase, onTable *Grant
				for i := range detail.Grants {
					g := &detail.Grants[i]
					for _, p := range g.Privileges {
						if strings.Contains(strings.ToUpper(p), "IDENTIFIED") || strings.Contains(p, "*") {
							t.Errorf("a password hash reached the grant list: %q", p)
						}
					}
					switch {
					case g.Level == GrantOnDatabase && g.Database == database:
						onDatabase = g
					case g.Level == GrantOnTable && g.Table == "jd_b3_ops_t":
						onTable = g
					}
				}
				if onDatabase == nil || strings.Join(onDatabase.Privileges, ",") != "SELECT,SHOW VIEW" {
					t.Errorf("database grant: %+v in %+v", onDatabase, detail.Grants)
				}
				if onTable == nil || strings.Join(onTable.Privileges, ",") != "UPDATE,DELETE" {
					t.Errorf("table grant: %+v", onTable)
				}
				if detail.AuthPlugin == "" || detail.Host != "10.%" {
					t.Errorf("detail: %+v", detail)
				}

				// Lock it, then change only its password: it stays locked.
				locked := true
				if err := admin.AlterRole(ctx, db, RoleSpec{Name: "jd_b3_ops_u", Host: "10.%", Locked: &locked}); err != nil {
					t.Fatal(err)
				}
				if err := admin.AlterRole(ctx, db, RoleSpec{Name: "jd_b3_ops_u", Host: "10.%", Password: "second-pw", SetPassword: true}); err != nil {
					t.Fatal(err)
				}
				detail, err = ReadRoleDetail(ctx, db, DriverMySQL, "jd_b3_ops_u", "10.%")
				if err != nil {
					t.Fatal(err)
				}
				if !detail.Locked || detail.Login {
					t.Errorf("the account did not stay locked through a password change: %+v", detail.Role)
				}

				if _, err := ChangePrivileges(ctx, db, DriverMySQL, PrivilegeChange{Role: "jd_b3_ops_u", Host: "10.%",
					Level: GrantOnDatabase, Database: database, Privileges: []string{"ALL"}, Revoke: true}, false); err != nil {
					t.Fatalf("revoking the database grant: %v", err)
				}
				// A grant made without the escaping, as one made by hand is.
				opsMustExec(t, db, "GRANT SELECT ON `"+database+"`.* TO 'jd_b3_ops_u'@'10.%'")
				if _, err := ChangePrivileges(ctx, db, DriverMySQL, PrivilegeChange{Role: "jd_b3_ops_u", Host: "10.%",
					Level: GrantOnDatabase, Database: database, Privileges: []string{"SELECT"}, Revoke: true}, false); err != nil {
					t.Fatalf("revoking a grant stored without escaping: %v", err)
				}
				grants, _, err := ListGrants(ctx, db, DriverMySQL, GrantFilter{Role: "jd_b3_ops_u", Host: "10.%"})
				if err != nil {
					t.Fatal(err)
				}
				for _, g := range grants {
					if g.Level == GrantOnDatabase {
						t.Errorf("a database grant survived the revokes: %+v", g)
					}
				}
				onObject, _, err := ListGrants(ctx, db, DriverMySQL, GrantFilter{Schema: database, Table: "jd_b3_ops_t"})
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, g := range onObject {
					found = found || (g.Grantee == "jd_b3_ops_u" && g.Host == "10.%" && g.Table == "jd_b3_ops_t")
				}
				if !found {
					t.Errorf("the table's grants do not name the account: %+v", onObject)
				}
			})

			// An account is a name and a host. Asked about a name alone, the
			// list is for every account that name has — not for 'name'@'%',
			// which need not exist and is an error to ask about.
			t.Run("grants_for_every_host_of_a_name", func(t *testing.T) {
				var database string
				if err := db.QueryRow(`SELECT DATABASE()`).Scan(&database); err != nil {
					t.Fatal(err)
				}
				drop := func() {
					db.Exec(`DROP USER IF EXISTS 'jd_b3_ops_h'@'localhost'`)
					db.Exec(`DROP USER IF EXISTS 'jd_b3_ops_h'@'10.1.%'`)
				}
				drop()
				t.Cleanup(drop)
				opsMustExec(t, db, `CREATE USER 'jd_b3_ops_h'@'localhost' IDENTIFIED BY 'pw-one-1A'`,
					`CREATE USER 'jd_b3_ops_h'@'10.1.%' IDENTIFIED BY 'pw-two-2B'`,
					"GRANT SELECT ON `"+database+"`.`jd_b3_ops_t` TO 'jd_b3_ops_h'@'localhost'",
					"GRANT INSERT ON `"+database+"`.`jd_b3_ops_t` TO 'jd_b3_ops_h'@'10.1.%'")
				grants, _, err := ListGrants(ctx, db, DriverMySQL, GrantFilter{Role: "jd_b3_ops_h"})
				if err != nil {
					t.Fatalf("a name with no account at %%: %v", err)
				}
				held := map[string]string{}
				for _, g := range grants {
					if g.Level == GrantOnTable && g.Table == "jd_b3_ops_t" {
						held[g.Host] = strings.Join(g.Privileges, ",")
					}
				}
				if held["localhost"] != "SELECT" || held["10.1.%"] != "INSERT" || len(held) != 2 {
					t.Errorf("table grants by host: %v", held)
				}
				one, _, err := ListGrants(ctx, db, DriverMySQL, GrantFilter{Role: "jd_b3_ops_h", Host: "localhost"})
				if err != nil {
					t.Fatal(err)
				}
				for _, g := range one {
					if g.Host != "localhost" {
						t.Errorf("a grant of another host came back for localhost: %+v", g)
					}
				}
				none, _, err := ListGrants(ctx, db, DriverMySQL, GrantFilter{Role: "jd_b3_ops_nobody"})
				if err != nil || len(none) != 0 {
					t.Errorf("a name with no account: %v, %v", none, err)
				}
			})

			// Largest first means largest of all of them, not largest of the
			// first few by name.
			t.Run("largest_indexes_first", func(t *testing.T) {
				opsMustExec(t, db, `DROP TABLE IF EXISTS jd_b3_ops_zz_big`,
					`CREATE TABLE jd_b3_ops_zz_big (id INT PRIMARY KEY, pad VARCHAR(200), KEY jd_b3_ops_zz_pad (pad)) ENGINE=InnoDB`)
				t.Cleanup(func() { db.Exec(`DROP TABLE IF EXISTS jd_b3_ops_zz_big`) })
				// Five thousand rows of 160 characters: an index of about a megabyte,
				// far past anything else these tests make.
				const digits = `(SELECT 0 n UNION ALL SELECT 1 UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL SELECT 4
				  UNION ALL SELECT 5 UNION ALL SELECT 6 UNION ALL SELECT 7 UNION ALL SELECT 8 UNION ALL SELECT 9)`
				opsMustExec(t, db, `INSERT INTO jd_b3_ops_zz_big
				  SELECT a.n + b.n * 10 + c.n * 100 + d.n * 1000, REPEAT(MD5(a.n + b.n * 10 + c.n * 100 + d.n * 1000), 5)
				  FROM `+digits+` a CROSS JOIN `+digits+` b CROSS JOIN `+digits+` c CROSS JOIN `+digits+` d
				  WHERE d.n < 5`, `ANALYZE TABLE jd_b3_ops_zz_big`, `ANALYZE TABLE jd_b3_ops_t`)
				first, err := ReadIndexStats(ctx, db, DriverMySQL, StatsOptions{Limit: 1})
				if err != nil {
					t.Fatal(err)
				}
				if len(first.Indexes) != 1 || !first.Truncated {
					t.Fatalf("limit 1: %+v", first)
				}
				var largest int64
				if err := db.QueryRow(`SELECT MAX(stat_value * @@innodb_page_size) FROM mysql.innodb_index_stats
				  WHERE stat_name = 'size' AND database_name = DATABASE()`).Scan(&largest); err != nil {
					t.Fatal(err)
				}
				if first.Indexes[0].Bytes != largest || first.Indexes[0].Table != "jd_b3_ops_zz_big" {
					t.Errorf("the first index is %s.%s at %d bytes; the largest in the database is %d",
						first.Indexes[0].Table, first.Indexes[0].Name, first.Indexes[0].Bytes, largest)
				}
			})

			// An account is made with what was asked, or not made.
			t.Run("create_carries_every_attribute", func(t *testing.T) {
				db.Exec(`DROP USER IF EXISTS 'jd_b3_ops_c'@'%'`)
				t.Cleanup(func() { db.Exec(`DROP USER IF EXISTS 'jd_b3_ops_c'@'%'`) })
				admin, _ := AdminFor(DriverMySQL)
				locked, until := true, "2031-01-01"
				err := admin.CreateRole(ctx, db, RoleSpec{Name: "jd_b3_ops_c", Password: "first-pw-1A", SetPassword: true, Login: true, ValidUntil: &until})
				if _, refused := err.(ErrRoleAttribute); !refused {
					t.Fatalf("an expiry MySQL cannot set: %v", err)
				}
				if _, err := ReadRoleDetail(ctx, db, DriverMySQL, "jd_b3_ops_c", "%"); err != ErrNoSuchRole {
					t.Fatalf("a refused create left an account behind: %v", err)
				}
				if err := admin.CreateRole(ctx, db, RoleSpec{Name: "jd_b3_ops_c", Password: "first-pw-1A", SetPassword: true, Login: true,
					Locked: &locked, CreateRole: true, CreateDB: true, ConnLimit: 3}); err != nil {
					t.Fatal(err)
				}
				detail, err := ReadRoleDetail(ctx, db, DriverMySQL, "jd_b3_ops_c", "%")
				if err != nil {
					t.Fatal(err)
				}
				if !detail.Locked || detail.Login || !detail.CreateRole || !detail.CreateDB || detail.Superuser || detail.ConnLimit != 3 {
					t.Errorf("created as %+v", detail.Role)
				}
			})

			// Closing the request stops the statement on the server; closing
			// the socket, which is all the driver does, does not.
			t.Run("maintenance_stops_with_the_request", func(t *testing.T) {
				const long = `SELECT SLEEP(60) /* jd_b3_ops_stop */`
				running := func() int {
					t.Helper()
					var n int
					if err := db.QueryRow(`SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE INFO = ?`, long).Scan(&n); err != nil {
						t.Fatal(err)
					}
					return n
				}
				stopCtx, cancel := context.WithCancel(ctx)
				conn, release, err := mysqlStoppableSession(stopCtx, db)
				if err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() {
					_, err := conn.ExecContext(stopCtx, long)
					done <- err
				}()
				waitFor(t, "the statement to start", func() bool { return running() == 1 })
				cancel()
				select {
				case err := <-done:
					if err == nil {
						t.Error("the cancelled statement reported success")
					}
				case <-time.After(10 * time.Second):
					t.Fatal("the cancelled statement never returned")
				}
				release()
				// The server notices a vanished client by itself only every few
				// seconds, and not at all inside a table rebuild.
				deadline := time.Now().Add(2 * time.Second)
				for running() != 0 {
					if time.Now().After(deadline) {
						t.Fatal("the statement is still running on the server after its request ended")
					}
					time.Sleep(50 * time.Millisecond)
				}
				// The pool is whole: the connection that was killed is not in it.
				var one int
				for i := 0; i < 5; i++ {
					if err := db.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil {
						t.Fatalf("the pool after a stopped session: %v", err)
					}
				}
				// A session nobody cancelled goes back to the pool untouched.
				conn, release, err = mysqlStoppableSession(ctx, db)
				if err != nil {
					t.Fatal(err)
				}
				if err := conn.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil {
					t.Fatal(err)
				}
				release()
			})

			t.Run("statements", func(t *testing.T) {
				report, err := TopStatements(ctx, db, DriverMySQL, StatementsOptions{Sort: StatementsByCalls, Limit: 5})
				if err != nil {
					t.Fatal(err)
				}
				if maria {
					if report.Supported || report.Enable == nil {
						t.Errorf("performance_schema is off and the report says %+v", report)
					}
					return
				}
				if !report.Supported || !report.Resettable || len(report.Statements) == 0 {
					t.Fatalf("statements: %+v", report)
				}
				if _, err := ResetStatements(ctx, db, DriverMySQL); err != nil {
					t.Fatal(err)
				}
			})

			t.Run("advisor", func(t *testing.T) {
				report, err := Advise(ctx, db, DriverMySQL, "")
				if err != nil {
					t.Fatal(err)
				}
				// With no schema named the checks run on the connection's own
				// database, not on every database of the server.
				if !report.EngineChecks || report.Version == "" || report.TablesChecked == 0 {
					t.Fatalf("report: %+v", report)
				}
				for _, f := range report.Findings {
					if f.Category == "schema" || f.Category == "" {
						t.Errorf("%s has category %q", f.ID, f.Category)
					}
				}
			})

			// A table that is keyed in everything but name is offered the
			// statement that names the key; one that is not is offered none,
			// because a new column would break every INSERT that names no
			// columns.
			t.Run("advisor_fix_runs", func(t *testing.T) {
				opsMustExec(t, db, `DROP TABLE IF EXISTS jd_b3_fix_keyed`, `DROP TABLE IF EXISTS jd_b3_fix_bare`,
					`CREATE TABLE jd_b3_fix_keyed (tenant INT NOT NULL, code VARCHAR(20) NOT NULL, note VARCHAR(20),
					   UNIQUE KEY jd_b3_fix_keyed_code (tenant, code)) ENGINE=InnoDB`,
					`CREATE TABLE jd_b3_fix_bare (v INT, note VARCHAR(20), UNIQUE KEY jd_b3_fix_bare_note (note)) ENGINE=InnoDB`)
				t.Cleanup(func() {
					db.Exec(`DROP TABLE IF EXISTS jd_b3_fix_keyed`)
					db.Exec(`DROP TABLE IF EXISTS jd_b3_fix_bare`)
				})
				keyless := func() map[string]AdviceTarget {
					t.Helper()
					report, err := Advise(ctx, db, DriverMySQL, "")
					if err != nil {
						t.Fatal(err)
					}
					out := map[string]AdviceTarget{}
					for _, f := range report.Findings {
						if f.ID == "no-primary-key" {
							for _, target := range f.Targets {
								out[target.Name] = target
							}
						}
					}
					return out
				}
				found := keyless()
				keyed, isKeyless := found["jd_b3_fix_keyed"]
				// InnoDB already treats a unique index over NOT NULL columns as
				// the clustered key, and information_schema may report it as one.
				if isKeyless {
					if !strings.HasSuffix(keyed.SQL, "ADD PRIMARY KEY (`tenant`, `code`);") || !strings.Contains(keyed.SQL, "`jd_b3_fix_keyed`") {
						t.Fatalf("a table with a unique index: %+v", keyed)
					}
					opsMustExec(t, db, strings.TrimSuffix(keyed.SQL, ";"))
				}
				if bare, ok := found["jd_b3_fix_bare"]; !ok || bare.SQL != "" {
					t.Errorf("a table whose only unique column may be NULL: %+v (found %v)", bare, ok)
				}
				if _, still := keyless()["jd_b3_fix_keyed"]; still {
					t.Error("still without a key after the fix")
				}
			})
		})
	}
}

// --- SQLite --------------------------------------------------------------------

func TestOpsSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ops.db")
	d, _ := DialectFor(DriverSQLite)
	db, err := sql.Open(d.SQLDriverName(), d.NormaliseDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	d.TunePool(db)
	t.Cleanup(func() { db.Close() })
	ctx := t.Context()
	opsMustExec(t, db, `CREATE TABLE parent (id INTEGER PRIMARY KEY, name TEXT UNIQUE)`,
		`CREATE TABLE child (id INTEGER PRIMARY KEY, parent_id INTEGER REFERENCES parent(id), v TEXT)`,
		`CREATE INDEX child_parent ON child (parent_id)`, `CREATE INDEX child_parent_copy ON child (parent_id)`,
		`CREATE INDEX child_parent_v ON child (parent_id, v)`, `CREATE INDEX child_lower ON child (lower(v))`,
		`INSERT INTO parent VALUES (1, 'a'), (2, 'b')`,
		`INSERT INTO child VALUES (1, 1, 'x'), (2, 2, 'y'), (3, 1, 'z')`)

	stats, err := ReadServerStats(ctx, db, DriverSQLite)
	if err != nil {
		t.Fatal(err)
	}
	if !stats.Supported || stats.Gauges["fileBytes"] <= 0 || stats.Gauges["pageCount"] <= 0 || stats.Facts["journalMode"] == "" || stats.Database != path {
		t.Fatalf("stats: %+v", stats)
	}
	file, err := ReadSQLiteFile(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if file.Path != path || file.Objects["table"] != 2 || file.Objects["index"] < 4 || file.Version == "" || !file.ForeignKeys || len(file.CompileOptions) == 0 {
		t.Errorf("file: %+v", file)
	}

	tables, err := ReadTableStats(ctx, db, DriverSQLite, StatsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tables.Tables) != 2 {
		t.Fatalf("tables: %+v", tables.Tables)
	}
	for _, tb := range tables.Tables {
		want := map[string]int64{"parent": 2, "child": 3}[tb.Table]
		if tb.Rows != want || tb.SeqScans != -1 {
			t.Errorf("%s: %+v", tb.Table, tb)
		}
		if file.SizesKnown && tb.TotalBytes <= 0 {
			t.Errorf("%s has no size though dbstat is built in", tb.Table)
		}
	}
	indexes, err := ReadIndexStats(ctx, db, DriverSQLite, StatsOptions{Table: "child"})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]IndexStat{}
	for _, ix := range indexes.Indexes {
		by[ix.Name] = ix
	}
	if (by["child_parent"].DuplicateOf == "") == (by["child_parent_copy"].DuplicateOf == "") {
		t.Errorf("identical indexes: %+v", indexes.Indexes)
	}
	if by["child_lower"].DuplicateOf != "" || by["child_lower"].CoveredBy != "" || by["child_parent_v"].Unused {
		t.Errorf("expression index or unused flag: %+v", by)
	}

	for _, c := range []struct {
		req  MaintenanceRequest
		stmt string
		ok   bool
	}{
		{MaintenanceRequest{Action: "analyze"}, "ANALYZE", true},
		{MaintenanceRequest{Action: "analyze", Table: "child"}, `ANALYZE "child"`, true},
		{MaintenanceRequest{Action: "optimize"}, "PRAGMA optimize", true},
		{MaintenanceRequest{Action: "integrity_check"}, "PRAGMA integrity_check", true},
		{MaintenanceRequest{Action: "quick_check"}, "PRAGMA quick_check", true},
		{MaintenanceRequest{Action: "foreign_key_check"}, "PRAGMA foreign_key_check", true},
		{MaintenanceRequest{Action: "reindex", Table: "child"}, `REINDEX "child"`, true},
		{MaintenanceRequest{Action: "reindex", Index: "child_parent"}, `REINDEX "child_parent"`, true},
		{MaintenanceRequest{Action: "vacuum"}, "VACUUM", true},
		// Not in WAL mode yet: the checkpoint runs and says there is no log.
		{MaintenanceRequest{Action: "wal_checkpoint", Options: MaintenanceOptions{Mode: "passive"}}, "PRAGMA wal_checkpoint(PASSIVE)", true},
	} {
		res, err := RunMaintenance(ctx, db, DriverSQLite, path, c.req)
		if err != nil {
			t.Fatalf("%+v: %v", c.req, err)
		}
		if res.Statements[0] != c.stmt || res.OK != c.ok {
			t.Errorf("%+v ran %v ok=%v output %v", c.req, res.Statements, res.OK, res.Output)
		}
	}
	if res, err := RunMaintenance(ctx, db, DriverSQLite, path, MaintenanceRequest{Action: "integrity_check"}); err != nil || len(res.Output) != 1 || res.Output[0] != "ok" {
		t.Errorf("integrity check: %+v %v", res, err)
	}
	// A row whose parent is gone is what the foreign key check is for.
	opsMustExec(t, db, `PRAGMA foreign_keys = OFF`, `INSERT INTO child VALUES (9, 99, 'orphan')`, `PRAGMA foreign_keys = ON`)
	orphan, err := RunMaintenance(ctx, db, DriverSQLite, path, MaintenanceRequest{Action: "foreign_key_check"})
	if err != nil {
		t.Fatal(err)
	}
	if orphan.OK || len(orphan.Output) != 1 || !strings.Contains(orphan.Output[0], "child") {
		t.Errorf("foreign key check: %+v", orphan)
	}
	for _, bad := range []MaintenanceRequest{
		{Action: "vacuum", Table: "child"},
		{Action: "wal_checkpoint", Options: MaintenanceOptions{Mode: "sideways"}},
		{Action: "pragma_writable_schema"},
	} {
		if _, err := RunMaintenance(ctx, db, DriverSQLite, path, bad); err == nil {
			t.Errorf("%+v was accepted", bad)
		} else if _, ok := err.(ErrMaintenanceRequest); !ok {
			t.Errorf("%+v reached the engine: %v", bad, err)
		}
	}

	settings, err := ListSettings(ctx, db, DriverSQLite, true)
	if err != nil {
		t.Fatal(err)
	}
	editable := []string{}
	for _, s := range settings {
		if s.Editable {
			editable = append(editable, s.Name)
			if s.Context != "file" {
				t.Errorf("%s is editable and not stored in the file", s.Name)
			}
		}
	}
	if strings.Join(editable, ",") != "journal_mode,auto_vacuum,user_version,application_id" {
		t.Errorf("editable pragmas: %v", editable)
	}
	change, err := ChangeSetting(ctx, db, DriverSQLite, "journal_mode", "WAL", false)
	if err != nil {
		t.Fatal(err)
	}
	if change.Value != "wal" || change.Statements[0] != "PRAGMA journal_mode = wal" {
		t.Errorf("journal mode: %+v", change)
	}
	if change, err = ChangeSetting(ctx, db, DriverSQLite, "user_version", "42", false); err != nil || change.Value != "42" {
		t.Errorf("user_version: %+v %v", change, err)
	}
	if change, err = ChangeSetting(ctx, db, DriverSQLite, "user_version", "", true); err != nil || change.Value != "0" {
		t.Errorf("reset user_version: %+v %v", change, err)
	}
	for name, value := range map[string]string{
		"journal_mode": "off", "user_version": "1; DROP TABLE child", "foreign_keys": "off", "writable_schema": "1", "user_version ": "99999999999",
	} {
		if _, err := ChangeSetting(ctx, db, DriverSQLite, name, value, false); err == nil {
			t.Errorf("%s = %q was accepted", name, value)
		} else if _, ok := err.(ErrSettingRequest); !ok {
			t.Errorf("%s = %q reached the engine: %v", name, value, err)
		}
	}
	// In WAL mode now: a checkpoint has a log to fold in.
	opsMustExec(t, db, `INSERT INTO parent VALUES (3, 'c')`)
	if res, err := RunMaintenance(ctx, db, DriverSQLite, path, MaintenanceRequest{Action: "wal_checkpoint"}); err != nil || !res.OK || res.Statements[0] != "PRAGMA wal_checkpoint(TRUNCATE)" {
		t.Errorf("checkpoint: %+v %v", res, err)
	}

	if err := CancelQuery(ctx, db, DriverSQLite, "1"); err != ErrNoActivityView {
		t.Errorf("cancel on SQLite: %v", err)
	}
	if locks, err := ListLocks(ctx, db, DriverSQLite); err != nil || locks.Supported || locks.Reason == "" {
		t.Errorf("locks on SQLite: %+v %v", locks, err)
	}
	if rep, err := ReadReplication(ctx, db, DriverSQLite); err != nil || rep.Supported || rep.Replicas == nil {
		t.Errorf("replication on SQLite: %+v %v", rep, err)
	}

	report, err := Advise(ctx, db, DriverSQLite, "")
	if err != nil {
		t.Fatal(err)
	}
	if !report.EngineChecks {
		t.Errorf("the SQLite engine checks did not run: %+v", report)
	}
	for _, f := range report.Findings {
		if f.ID == "journal-mode-not-wal" {
			t.Errorf("a WAL database was advised to switch to WAL")
		}
	}
}

// A foreign key check answers a row per orphan. What is kept of it is bounded
// while it is read, and the result says there was more.
func TestSQLiteMaintenanceOutputIsBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orphans.db")
	d, _ := DialectFor(DriverSQLite)
	db, err := sql.Open(d.SQLDriverName(), d.NormaliseDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	d.TunePool(db)
	t.Cleanup(func() { db.Close() })
	opsMustExec(t, db, `PRAGMA foreign_keys = OFF`,
		`CREATE TABLE parent (id INTEGER PRIMARY KEY)`,
		`CREATE TABLE child (id INTEGER PRIMARY KEY, parent_id INTEGER REFERENCES parent(id))`,
		`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 2000)
		 INSERT INTO child SELECT i, i + 100000 FROM n`)
	res, err := RunMaintenance(t.Context(), db, DriverSQLite, path, MaintenanceRequest{Action: "foreign_key_check"})
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || !res.OutputTruncated || len(res.Output) != maxMaintenanceLines+1 {
		t.Fatalf("ok %v, truncated %v, %d lines", res.OK, res.OutputTruncated, len(res.Output))
	}
	if !strings.Contains(res.Output[0], "child row 1 ") || !strings.HasPrefix(res.Output[maxMaintenanceLines], "…") {
		t.Errorf("first line %q, last line %q", res.Output[0], res.Output[maxMaintenanceLines])
	}
}

// The largest index is first whatever its name, and a duplicate is found
// whichever side of the limit it falls.
func TestSQLiteIndexStatsRankBeforeTheyCut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "indexes.db")
	d, _ := DialectFor(DriverSQLite)
	db, err := sql.Open(d.SQLDriverName(), d.NormaliseDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	d.TunePool(db)
	t.Cleanup(func() { db.Close() })
	opsMustExec(t, db, `CREATE TABLE aaa (id INTEGER PRIMARY KEY, v TEXT)`, `CREATE INDEX aaa_v ON aaa (v)`,
		`CREATE TABLE zzz (id INTEGER PRIMARY KEY, v TEXT)`, `CREATE INDEX zzz_v ON zzz (v)`, `CREATE INDEX zzz_v_again ON zzz (v)`,
		`INSERT INTO aaa VALUES (1, 'x')`,
		`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 5000)
		 INSERT INTO zzz SELECT i, hex(randomblob(40)) FROM n`)
	report, err := ReadIndexStats(t.Context(), db, DriverSQLite, StatsOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Indexes) != 1 || !report.Truncated {
		t.Fatalf("limit 1: %+v", report)
	}
	if size, _ := sqliteObjectSizes(t.Context(), db); size == nil {
		t.Skip("this SQLite build has no dbstat table, so there are no sizes to rank by")
	}
	first := report.Indexes[0]
	if first.Table != "zzz" || first.Bytes <= 0 {
		t.Errorf("the first index is %s.%s at %d bytes", first.Table, first.Name, first.Bytes)
	}
	two, err := ReadIndexStats(t.Context(), db, DriverSQLite, StatsOptions{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(two.Indexes) != 2 || two.Indexes[0].Table != "zzz" || two.Indexes[1].Table != "zzz" ||
		(two.Indexes[0].DuplicateOf == "") == (two.Indexes[1].DuplicateOf == "") {
		t.Errorf("limit 2: %+v", two.Indexes)
	}
}

func TestSQLiteAdvisorSaysWhenTheFileIsNotInWALMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plain.db")
	d, _ := DialectFor(DriverSQLite)
	db, err := sql.Open(d.SQLDriverName(), d.NormaliseDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	d.TunePool(db)
	t.Cleanup(func() { db.Close() })
	opsMustExec(t, db, `CREATE TABLE t (id INTEGER PRIMARY KEY)`)
	report, err := Advise(t.Context(), db, DriverSQLite, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range report.Findings {
		if f.ID == "journal-mode-not-wal" {
			found = true
			if f.SQL != "PRAGMA journal_mode = WAL;" || f.Category != AdvicePerformance || len(f.Targets) != 1 {
				t.Errorf("finding: %+v", f)
			}
		}
	}
	if !found {
		t.Errorf("no journal mode finding: %+v", report.Findings)
	}
}

// --- ClickHouse --------------------------------------------------------------

func TestLiveOpsClickHouse(t *testing.T) {
	db, dsn := opsLive(t, DriverClickHouse, "JD_TEST_CLICKHOUSE_DSN")
	ctx := t.Context()
	var database string
	if err := db.QueryRow(`SELECT currentDatabase()`).Scan(&database); err != nil {
		t.Fatal(err)
	}
	opsMustExec(t, db, `DROP TABLE IF EXISTS jd_b3_ops_events`,
		`CREATE TABLE jd_b3_ops_events (id UInt64, d Date, v String, INDEX v_idx v TYPE bloom_filter GRANULARITY 4)
		 ENGINE = MergeTree PARTITION BY toYYYYMM(d) ORDER BY id`,
		`INSERT INTO jd_b3_ops_events SELECT number, toDate('2026-01-01') + number % 60, toString(number) FROM numbers(2000)`,
		`INSERT INTO jd_b3_ops_events SELECT number, toDate('2026-01-01') + number % 60, toString(number) FROM numbers(2000, 2000)`)
	t.Cleanup(func() { db.Exec(`DROP TABLE IF EXISTS jd_b3_ops_events`) })

	stats, err := ReadServerStats(ctx, db, DriverClickHouse)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stats.Version, "ClickHouse ") || stats.UptimeSeconds <= 0 || stats.DatabaseBytes <= 0 || stats.Connections == nil || stats.Connections.Max <= 0 {
		t.Fatalf("stats: %+v", stats)
	}
	for _, key := range []string{StatQueries, StatRowsRead, StatRowsWritten, "insertedRows", "selectQueries", "merges"} {
		if _, ok := stats.Counters[key]; !ok {
			t.Errorf("counter %q is missing", key)
		}
	}
	for _, key := range []string{"memoryTracking", "runningQueries", "runningMerges", "activeParts", "totalParts", "diskFreeBytes"} {
		if _, ok := stats.Gauges[key]; !ok {
			t.Errorf("gauge %q is missing", key)
		}
	}
	if len(stats.Notes) != 0 {
		t.Errorf("notes: %v", stats.Notes)
	}

	tables, err := ReadTableStats(ctx, db, DriverClickHouse, StatsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var stat *TableStat
	for i := range tables.Tables {
		if tables.Tables[i].Table == "jd_b3_ops_events" {
			stat = &tables.Tables[i]
		}
	}
	if stat == nil || stat.Rows != 4000 || stat.Parts < 2 || stat.Engine != "MergeTree" || stat.UncompressedBytes <= 0 || stat.TotalBytes <= 0 {
		t.Fatalf("table stat: %+v", stat)
	}
	indexes, err := ReadIndexStats(ctx, db, DriverClickHouse, StatsOptions{Table: "jd_b3_ops_events"})
	if err != nil {
		t.Fatal(err)
	}
	if len(indexes.Indexes) != 1 || indexes.Indexes[0].Name != "v_idx" || indexes.Indexes[0].Unused {
		t.Errorf("indexes: %+v", indexes.Indexes)
	}

	parts, err := ClickHouseTableParts(ctx, db, "", "jd_b3_ops_events", false)
	if err != nil {
		t.Fatal(err)
	}
	if parts.Database != database || len(parts.Partitions) != 3 || len(parts.Parts) < 3 {
		t.Fatalf("parts: %+v", parts)
	}
	var rows int64
	for _, p := range parts.Partitions {
		rows += p.Rows
		if p.Parts < 1 || p.Bytes <= 0 || p.Modified == nil {
			t.Errorf("partition: %+v", p)
		}
	}
	if rows != 4000 {
		t.Errorf("partitions hold %d rows", rows)
	}

	before := stat.Parts
	res, err := RunMaintenance(ctx, db, DriverClickHouse, dsn, MaintenanceRequest{Action: "optimize", Table: "jd_b3_ops_events", Options: MaintenanceOptions{Final: true}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Statements[0] != "OPTIMIZE TABLE `jd_b3_ops_events` FINAL" || !res.OK {
		t.Errorf("optimize: %+v", res)
	}
	after, err := ClickHouseTableParts(ctx, db, database, "jd_b3_ops_events", false)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(after.Parts)) >= before {
		t.Errorf("optimize final left %d parts of %d", len(after.Parts), before)
	}
	withOld, err := ClickHouseTableParts(ctx, db, database, "jd_b3_ops_events", true)
	if err != nil || len(withOld.Parts) < len(after.Parts) {
		t.Errorf("with inactive parts: %d, %v", len(withOld.Parts), err)
	}
	if _, err := RunMaintenance(ctx, db, DriverClickHouse, dsn, MaintenanceRequest{Action: "optimize"}); err == nil {
		t.Error("optimize without a table was accepted")
	}

	opsMustExec(t, db, `ALTER TABLE jd_b3_ops_events DELETE WHERE id = 1`)
	mutations, err := ClickHouseMutations(ctx, db, "")
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, m := range mutations {
		if m.Table == "jd_b3_ops_events" && strings.Contains(m.Command, "DELETE") && m.Created != nil {
			seen = true
		}
	}
	if !seen {
		t.Errorf("the mutation is not listed: %+v", mutations)
	}
	if _, err := ClickHouseMerges(ctx, db); err != nil {
		t.Errorf("merges: %v", err)
	}
	queries, err := ClickHouseQueries(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	self := false
	for _, q := range queries {
		self = self || (q.Self && strings.Contains(q.Query, "system.processes"))
	}
	if !self {
		t.Errorf("the asking query is not among the running ones: %+v", queries)
	}

	sessions, err := ListActivity(ctx, db, DriverClickHouse)
	if err != nil || len(sessions) == 0 || sessions[0].Status != SessionActive {
		t.Errorf("sessions: %+v %v", sessions, err)
	}
	if err := CancelQuery(ctx, db, DriverClickHouse, "x"); err != ErrNoCancel {
		t.Errorf("cancel on ClickHouse: %v", err)
	}

	settings, err := ListSettings(ctx, db, DriverClickHouse, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(settings) < 50 {
		t.Fatalf("%d server settings", len(settings))
	}
	for _, s := range settings {
		if s.Editable {
			t.Fatalf("a ClickHouse server setting is editable: %+v", s)
		}
	}
	if _, err := ChangeSetting(ctx, db, DriverClickHouse, "max_connections", "10", false); err == nil {
		t.Error("a ClickHouse server setting was changed")
	}
	short, err := ListSettings(ctx, db, DriverClickHouse, false)
	if err != nil || len(short) == 0 || len(short) > len(clickhouseSettingNames) {
		t.Errorf("short list: %d %v", len(short), err)
	}

	// The query log keeps each execution as it was sent; the list shows the
	// shape, with a ? where each literal was.
	opsMustExec(t, db, `SELECT count() FROM jd_b3_ops_events WHERE v = 'jd-b3-literal-17' AND id > 424242`, `SYSTEM FLUSH LOGS`)
	statements, err := TopStatements(ctx, db, DriverClickHouse, StatementsOptions{Sort: StatementsByCalls, Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	if !statements.Supported || statements.Resettable || len(statements.Statements) == 0 || statements.Since == nil {
		t.Errorf("statements: %+v", statements)
	}
	probe := false
	for _, st := range statements.Statements {
		if strings.Contains(st.Query, "jd-b3-literal-17") || strings.Contains(st.Query, "424242") {
			t.Errorf("a literal reached the list: %q", st.Query)
		}
		probe = probe || (strings.Contains(st.Query, "jd_b3_ops_events") && strings.Contains(st.Query, "?"))
	}
	if !probe {
		t.Errorf("the probe is not in the list by its shape: %+v", statements.Statements)
	}

	var user string
	if err := db.QueryRow(`SELECT currentUser()`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	grants, _, err := ListGrants(ctx, db, DriverClickHouse, GrantFilter{Role: user})
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) == 0 || grants[0].Grantee != user {
		t.Errorf("grants: %+v", grants)
	}
	detail, err := ReadRoleDetail(ctx, db, DriverClickHouse, user, "")
	if err != nil {
		t.Fatal(err)
	}
	if detail.AuthPlugin == "" || len(detail.Grants) == 0 {
		t.Errorf("role detail: %+v", detail)
	}

	report, err := Advise(ctx, db, DriverClickHouse, "")
	if err != nil {
		t.Fatal(err)
	}
	if !report.EngineChecks || report.Version == "" || report.EndOfLife == nil {
		t.Fatalf("report: %+v", report)
	}
	if len(report.Silences) != 0 {
		t.Errorf("silences: %v", report.Silences)
	}
}

// --- SQL Server --------------------------------------------------------------

// The SQL Server fixture is shared by every stream, so this works in a
// database of its own and puts back the one server option it changes.
func TestLiveOpsSQLServer(t *testing.T) {
	db, dsn := opsSQLServer(t)
	ctx := t.Context()
	opsMustExec(t, db, `IF OBJECT_ID('dbo.jd_b3_ops_child') IS NOT NULL DROP TABLE dbo.jd_b3_ops_child`,
		`IF OBJECT_ID('dbo.jd_b3_ops_t') IS NOT NULL DROP TABLE dbo.jd_b3_ops_t`,
		`CREATE TABLE dbo.jd_b3_ops_t (id INT PRIMARY KEY, a NVARCHAR(64), b INT)`,
		`CREATE INDEX jd_b3_ops_t_b ON dbo.jd_b3_ops_t (b)`,
		`CREATE INDEX jd_b3_ops_t_b_copy ON dbo.jd_b3_ops_t (b)`,
		`CREATE INDEX jd_b3_ops_t_ba ON dbo.jd_b3_ops_t (b, a)`,
		`CREATE INDEX jd_b3_ops_t_filtered ON dbo.jd_b3_ops_t (b) WHERE b > 1`,
		`INSERT INTO dbo.jd_b3_ops_t VALUES (1, 'x', 1), (2, 'y', 2), (3, 'z', 3)`)
	t.Cleanup(func() {
		db.Exec(`IF OBJECT_ID('dbo.jd_b3_ops_child') IS NOT NULL DROP TABLE dbo.jd_b3_ops_child`)
		db.Exec(`IF OBJECT_ID('dbo.jd_b3_ops_t') IS NOT NULL DROP TABLE dbo.jd_b3_ops_t`)
	})

	t.Run("stats", func(t *testing.T) {
		stats, err := ReadServerStats(ctx, db, DriverMSSQL)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(stats.Version, "Microsoft SQL Server") || stats.StartedAt == nil || stats.DatabaseBytes <= 0 ||
			stats.Connections == nil || stats.Connections.Max <= 0 || stats.Connections.Total < 1 {
			t.Fatalf("stats: %+v", stats)
		}
		if len(stats.Notes) != 0 {
			t.Errorf("sa was refused part of the snapshot: %v", stats.Notes)
		}
		for _, key := range []string{StatQueries, StatBlocksRead, StatBlocksHit, StatDeadlocks, StatTransactionsCommitted, "transactions"} {
			if _, ok := stats.Counters[key]; !ok {
				t.Errorf("counter %q is missing: %v", key, stats.Counters)
			}
		}
		if _, ok := stats.Gauges["userConnections"]; !ok {
			t.Errorf("gauges: %v", stats.Gauges)
		}
	})

	t.Run("sessions_and_locks", func(t *testing.T) {
		holder, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Close()
		waiter, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer waiter.Close()
		var holderPID, waiterPID string
		if err := holder.QueryRowContext(ctx, `SELECT CAST(@@SPID AS NVARCHAR(16))`).Scan(&holderPID); err != nil {
			t.Fatal(err)
		}
		if err := waiter.QueryRowContext(ctx, `SELECT CAST(@@SPID AS NVARCHAR(16))`).Scan(&waiterPID); err != nil {
			t.Fatal(err)
		}
		if _, err := holder.ExecContext(ctx, `BEGIN TRANSACTION`); err != nil {
			t.Fatal(err)
		}
		if _, err := holder.ExecContext(ctx, `UPDATE dbo.jd_b3_ops_t SET b = 10 WHERE id = 1`); err != nil {
			t.Fatal(err)
		}
		defer holder.ExecContext(context.Background(), `IF @@TRANCOUNT > 0 ROLLBACK TRANSACTION`)
		blocked := make(chan error, 1)
		go func() {
			_, err := waiter.ExecContext(context.Background(), `UPDATE dbo.jd_b3_ops_t SET b = 20 WHERE id = 1`)
			blocked <- err
		}()
		var sessions []Activity
		waitFor(t, "the waiter to be blocked", func() bool {
			sessions, err = ListActivity(ctx, db, DriverMSSQL)
			if err != nil {
				t.Fatal(err)
			}
			w := findSession(sessions, waiterPID)
			return w != nil && w.Status == SessionBlocked
		})
		// The holder has no request in flight. The old list read requests
		// alone, so the session everything waited on was not in it.
		h, w := findSession(sessions, holderPID), findSession(sessions, waiterPID)
		if h == nil || h.Status != SessionIdleInTransaction || h.Seconds != 0 || h.TransactionStart == nil {
			t.Errorf("holder: %+v", h)
		}
		if len(w.BlockedByPIDs) != 1 || w.BlockedByPIDs[0] != holderPID || !strings.Contains(w.Query, "jd_b3_ops_t") {
			t.Errorf("waiter: %+v", w)
		}
		locks, err := ListLocks(ctx, db, DriverMSSQL)
		if err != nil {
			t.Fatal(err)
		}
		var wait *LockWait
		for i := range locks.Waits {
			if locks.Waits[i].WaitingPID == waiterPID {
				wait = &locks.Waits[i]
			}
		}
		if wait == nil || wait.BlockingPID != holderPID || !strings.Contains(wait.Object, "jd_b3_ops_t") || wait.Mode == "" {
			t.Fatalf("wait: %+v in %+v", wait, locks.Waits)
		}
		// SQL Server stops a statement only by ending its session.
		if err := CancelQuery(ctx, db, DriverMSSQL, waiterPID); err != ErrNoCancel {
			t.Errorf("cancel: %v", err)
		}
		if _, err := holder.ExecContext(ctx, `ROLLBACK TRANSACTION`); err != nil {
			t.Fatal(err)
		}
		select {
		case <-blocked:
		case <-time.After(10 * time.Second):
			t.Fatal("the waiter never finished")
		}
	})

	t.Run("table_and_index_stats", func(t *testing.T) {
		opsMustExec(t, db, `SELECT COUNT(*) FROM dbo.jd_b3_ops_t WHERE b = 2`)
		tables, err := ReadTableStats(ctx, db, DriverMSSQL, StatsOptions{Schema: "dbo"})
		if err != nil {
			t.Fatal(err)
		}
		var stat *TableStat
		for i := range tables.Tables {
			if tables.Tables[i].Table == "jd_b3_ops_t" {
				stat = &tables.Tables[i]
			}
		}
		if stat == nil || stat.Rows != 3 || stat.TotalBytes <= 0 || stat.IndexBytes <= 0 {
			t.Fatalf("table stat: %+v", stat)
		}
		indexes, err := ReadIndexStats(ctx, db, DriverMSSQL, StatsOptions{Table: "jd_b3_ops_t"})
		if err != nil {
			t.Fatal(err)
		}
		by := map[string]IndexStat{}
		primary := ""
		for _, ix := range indexes.Indexes {
			by[ix.Name] = ix
			if ix.Primary {
				primary = ix.Name
			}
		}
		if len(by) != 5 || primary == "" {
			t.Fatalf("indexes: %+v", indexes.Indexes)
		}
		if (by["jd_b3_ops_t_b"].DuplicateOf == "") == (by["jd_b3_ops_t_b_copy"].DuplicateOf == "") {
			t.Errorf("identical indexes: %+v %+v", by["jd_b3_ops_t_b"], by["jd_b3_ops_t_b_copy"])
		}
		if by["jd_b3_ops_t_filtered"].DuplicateOf != "" || by["jd_b3_ops_t_filtered"].CoveredBy != "" {
			t.Errorf("a filtered index was called redundant: %+v", by["jd_b3_ops_t_filtered"])
		}
		if cols := by["jd_b3_ops_t_ba"].Columns; len(cols) != 2 || cols[0] != "b" || cols[1] != "a" {
			t.Errorf("columns: %v", cols)
		}
		if !by[primary].Constraint || !by[primary].Unique || by[primary].Unused {
			t.Errorf("primary key index: %+v", by[primary])
		}
	})

	t.Run("settings", func(t *testing.T) {
		all, err := ListSettings(ctx, db, DriverMSSQL, true)
		if err != nil {
			t.Fatal(err)
		}
		if len(all) < 30 {
			t.Fatalf("%d configuration options", len(all))
		}
		by := map[string]Setting{}
		for _, s := range all {
			by[s.Name] = s
			// sa holds ALTER SETTINGS, so every option can be set from here.
			if !s.Editable || s.Type != "integer" || s.Min == "" || s.Max == "" {
				t.Fatalf("option: %+v", s)
			}
		}
		short, err := ListSettings(ctx, db, DriverMSSQL, false)
		if err != nil || len(short) == 0 || len(short) > len(mssqlSettingNames) {
			t.Errorf("short list: %d %v", len(short), err)
		}
		if !SettingsWritable(DriverMSSQL) {
			t.Error("SQL Server options are reported as not writable")
		}

		// An advanced option, on a server that hides them: it is revealed for
		// the change and hidden again after it.
		const option = "cost threshold for parallelism"
		inUse := func(name string) string {
			t.Helper()
			var v string
			if err := db.QueryRow(`SELECT CAST(value_in_use AS NVARCHAR(64)) FROM sys.configurations WHERE name = @p1`, name).Scan(&v); err != nil {
				t.Fatal(err)
			}
			return v
		}
		before, shown := by[option].Value, inUse("show advanced options")
		t.Cleanup(func() {
			db.Exec(`EXEC sys.sp_configure N'show advanced options', 1`)
			db.Exec(`RECONFIGURE`)
			db.Exec(`EXEC sys.sp_configure N'` + option + `', ` + before)
			db.Exec(`EXEC sys.sp_configure N'show advanced options', ` + shown)
			db.Exec(`RECONFIGURE`)
		})
		for value, want := range map[string]string{"99999": "at most 32767", "-1": "at least 0", "seven": "takes a number", "7; SHUTDOWN": "takes a number"} {
			_, err := ChangeSetting(ctx, db, DriverMSSQL, option, value, false)
			if _, refused := err.(ErrSettingRequest); !refused || !strings.Contains(err.Error(), want) {
				t.Errorf("%s = %q: %v, want a refusal with %q", option, value, err, want)
			}
		}
		changed, err := ChangeSetting(ctx, db, DriverMSSQL, option, "7", false)
		if err != nil {
			t.Fatal(err)
		}
		wantStatements := []string{"EXEC sys.sp_configure N'cost threshold for parallelism', 7", "RECONFIGURE"}
		if shown == "0" {
			wantStatements = append(append([]string{"EXEC sys.sp_configure N'show advanced options', 1", "RECONFIGURE"}, wantStatements...),
				"EXEC sys.sp_configure N'show advanced options', 0", "RECONFIGURE")
		}
		if changed.Name != option || changed.Value != "7" || !changed.Persisted || changed.RestartRequired ||
			strings.Join(changed.Statements, " | ") != strings.Join(wantStatements, " | ") {
			t.Errorf("change: %+v", changed)
		}
		if got := inUse(option); got != "7" {
			t.Errorf("the server runs with %s", got)
		}
		if got := inUse("show advanced options"); got != shown {
			t.Errorf("show advanced options was %s and is now %s", shown, got)
		}
		// SQL Server publishes no default to go back to.
		if _, err := ChangeSetting(ctx, db, DriverMSSQL, option, "", true); err == nil {
			t.Error("an option was reset to a default the server does not record")
		} else if _, refused := err.(ErrSettingRequest); !refused {
			t.Errorf("reset: %v", err)
		}
		if back, err := ChangeSetting(ctx, db, DriverMSSQL, option, before, false); err != nil || back.Value != before {
			t.Errorf("putting it back: %+v %v", back, err)
		}
		// An option that is not advanced takes the two statements and no more.
		plain, err := ChangeSetting(ctx, db, DriverMSSQL, "backup compression default", by["backup compression default"].Value, false)
		if err != nil || len(plain.Statements) != 2 {
			t.Errorf("a plain option: %+v %v", plain, err)
		}
		if _, err := ChangeSetting(ctx, db, DriverMSSQL, "no such option", "1", false); err == nil {
			t.Error("an option the server does not have was set")
		}
	})

	t.Run("maintenance", func(t *testing.T) {
		run := func(req MaintenanceRequest) *MaintenanceResult {
			t.Helper()
			out, err := RunMaintenance(ctx, db, DriverMSSQL, "", req)
			if err != nil {
				t.Fatalf("%+v: %v", req, err)
			}
			if !out.OK || out.Duration == "" || len(out.Statements) != 1 {
				t.Fatalf("%+v: %+v", req, out)
			}
			return out
		}
		lines := func(out *MaintenanceResult) string { return strings.Join(out.Output, "\n") }

		stats := run(MaintenanceRequest{Action: "update_statistics", Table: "jd_b3_ops_t"})
		if stats.Statements[0] != "UPDATE STATISTICS [dbo].[jd_b3_ops_t]" || !strings.Contains(lines(stats), "jd_b3_ops_t_b: updated ") ||
			!strings.Contains(lines(stats), ", 3 rows, 3 sampled") {
			t.Errorf("update statistics: %+v", stats)
		}
		whole := run(MaintenanceRequest{Action: "update_statistics"})
		if whole.Statements[0] != "EXEC sys.sp_updatestats" || len(whole.Output) != 1 || !strings.Contains(whole.Output[0], "updated") {
			t.Errorf("sp_updatestats: %+v", whole)
		}
		reorganized := run(MaintenanceRequest{Action: "reorganize", Table: "jd_b3_ops_t"})
		if reorganized.Statements[0] != "ALTER INDEX ALL ON [dbo].[jd_b3_ops_t] REORGANIZE" || !strings.Contains(lines(reorganized), "% fragmented over ") ||
			!strings.Contains(lines(reorganized), "jd_b3_ops_t_ba: ") {
			t.Errorf("reorganize: %+v", reorganized)
		}
		one := run(MaintenanceRequest{Action: "rebuild", Schema: "dbo", Table: "jd_b3_ops_t", Index: "jd_b3_ops_t_b"})
		if one.Statements[0] != "ALTER INDEX [jd_b3_ops_t_b] ON [dbo].[jd_b3_ops_t] REBUILD" {
			t.Errorf("rebuild of one index: %+v", one)
		}
		// Online is an edition's feature; the fixture is a Developer edition.
		online := run(MaintenanceRequest{Action: "rebuild", Table: "jd_b3_ops_t", Options: MaintenanceOptions{Online: true}})
		if online.Statements[0] != "ALTER INDEX ALL ON [dbo].[jd_b3_ops_t] REBUILD WITH (ONLINE = ON)" {
			t.Errorf("online rebuild: %+v", online)
		}
		table := run(MaintenanceRequest{Action: "check", Table: "jd_b3_ops_t"})
		if table.Statements[0] != "DBCC CHECKTABLE (N'[dbo].[jd_b3_ops_t]') WITH TABLERESULTS" || !strings.Contains(lines(table), "jd_b3_ops_t") ||
			!strings.Contains(lines(table), "3 rows") {
			t.Errorf("checktable: %+v", table)
		}
		database := run(MaintenanceRequest{Action: "check"})
		if database.Statements[0] != "DBCC CHECKDB WITH TABLERESULTS" ||
			!strings.Contains(lines(database), "CHECKDB found 0 allocation errors and 0 consistency errors in database 'jd_b3'") {
			t.Errorf("checkdb: statements %v, %d lines, last %q", database.Statements, len(database.Output), database.Output[len(database.Output)-1])
		}

		// The request's own mistakes are refusals; the engine's are its words.
		for _, bad := range []MaintenanceRequest{
			{Action: "rebuild"}, {Action: "reorganize"}, {Action: "vacuum", Table: "jd_b3_ops_t"},
			{Action: "update_statistics", Schema: "dbo"}, {Action: "check", Table: "jd_b3_ops_t", Index: "jd_b3_ops_t_b"},
		} {
			if _, err := RunMaintenance(ctx, db, DriverMSSQL, "", bad); err == nil {
				t.Errorf("%+v ran", bad)
			} else if _, refused := err.(ErrMaintenanceRequest); !refused {
				t.Errorf("%+v: %v is not a request refusal", bad, err)
			}
		}
		if _, err := RunMaintenance(ctx, db, DriverMSSQL, "", MaintenanceRequest{Action: "rebuild", Table: "jd_b3_no_such_table"}); err == nil {
			t.Error("the indexes of a table that is not there were rebuilt")
		} else if _, refused := err.(ErrMaintenanceRequest); refused {
			t.Errorf("the engine's refusal was reported as the request's: %v", err)
		}
	})

	// An offline rebuild waits for the table. Closing the request has to end
	// the wait on the server, not only in this process.
	t.Run("maintenance_stops_with_the_request", func(t *testing.T) {
		holder, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Close()
		if _, err := holder.ExecContext(ctx, `BEGIN TRANSACTION`); err != nil {
			t.Fatal(err)
		}
		defer holder.ExecContext(context.Background(), `IF @@TRANCOUNT > 0 ROLLBACK TRANSACTION`)
		if _, err := holder.ExecContext(ctx, `UPDATE dbo.jd_b3_ops_t SET b = 30 WHERE id = 3`); err != nil {
			t.Fatal(err)
		}
		rebuilding := func() int {
			t.Helper()
			var n int
			if err := db.QueryRow(`
			  SELECT COUNT(*) FROM sys.dm_exec_requests r CROSS APPLY sys.dm_exec_sql_text(r.sql_handle) q
			  WHERE q.text LIKE 'ALTER INDEX ALL ON %jd_b3_ops_t% REBUILD' AND r.session_id <> @@SPID`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n
		}
		request, abandon := context.WithCancel(ctx)
		defer abandon()
		done := make(chan error, 1)
		go func() {
			_, err := RunMaintenance(request, db, DriverMSSQL, "", MaintenanceRequest{Action: "rebuild", Table: "jd_b3_ops_t"})
			done <- err
		}()
		waitFor(t, "the rebuild to be waiting on the table", func() bool { return rebuilding() == 1 })
		abandon()
		select {
		case err := <-done:
			if err == nil {
				t.Error("an abandoned rebuild reported success")
			}
		case <-time.After(15 * time.Second):
			t.Fatal("the abandoned rebuild never returned")
		}
		waitFor(t, "the rebuild to stop on the server", func() bool { return rebuilding() == 0 })
	})

	// The plan cache keeps a statement as it was sent; what is listed is its
	// shape.
	t.Run("statements", func(t *testing.T) {
		const probe = `SELECT COUNT(*) FROM dbo.jd_b3_ops_t t JOIN dbo.jd_b3_ops_t u ON u.id = t.id WHERE t.a = 'jd-b3-literal' AND u.b = 424242`
		for i := 0; i < 3; i++ {
			opsMustExec(t, db, probe)
		}
		var cached int
		if err := db.QueryRow(`
		  SELECT COUNT(*) FROM sys.dm_exec_query_stats s CROSS APPLY sys.dm_exec_sql_text(s.sql_handle) q
		  WHERE q.text LIKE '%jd-b3-literal%' AND q.text NOT LIKE '%dm_exec_query_stats%'`).Scan(&cached); err != nil || cached == 0 {
			t.Fatalf("the probe is not in the plan cache with its literal (%d, %v)", cached, err)
		}
		report, err := TopStatements(ctx, db, DriverMSSQL, StatementsOptions{Sort: StatementsByCalls, Limit: 200})
		if err != nil {
			t.Fatal(err)
		}
		if !report.Supported || report.Resettable || report.Sort != StatementsByCalls || report.TotalMs <= 0 {
			t.Fatalf("report: supported %v resettable %v sort %s total %v reason %q", report.Supported, report.Resettable, report.Sort, report.TotalMs, report.Reason)
		}
		var mine *Statement
		for i := range report.Statements {
			st := &report.Statements[i]
			if strings.Contains(st.Query, "jd-b3-literal") || strings.Contains(st.Query, "424242") {
				t.Errorf("a literal reached the list: %q", st.Query)
			}
			if st.Share < 0 || st.Share > 1 || st.ID == "" {
				t.Errorf("statement: %+v", st)
			}
			if strings.Contains(st.Query, "WHERE t.a = ? AND u.b = ?") {
				mine = st
			}
		}
		if mine == nil || mine.Calls < 3 || mine.TotalMs < 0 || mine.MeanMs < 0 || mine.Rows < 3 {
			t.Errorf("the probe: %+v", mine)
		}
		for _, sort := range StatementSorts() {
			if sorted, err := TopStatements(ctx, db, DriverMSSQL, StatementsOptions{Sort: sort, Limit: 3}); err != nil || !sorted.Supported || len(sorted.Statements) > 3 {
				t.Errorf("sort %s: %+v %v", sort, sorted, err)
			}
		}
		if _, err := ResetStatements(ctx, db, DriverMSSQL); err != ErrUnsupported {
			t.Errorf("reset: %v", err)
		}
	})

	t.Run("roles_and_grants", func(t *testing.T) {
		admin, _ := AdminFor(DriverMSSQL)
		drop := func() {
			db.Exec(`IF USER_ID('jd_b3_ops_login') IS NOT NULL DROP USER [jd_b3_ops_login]`)
			db.Exec(`IF SUSER_ID('jd_b3_ops_login') IS NOT NULL DROP LOGIN [jd_b3_ops_login]`)
		}
		drop()
		t.Cleanup(drop)
		// A login is made with everything it was asked to have, or not made:
		// disabled, and a member of both server roles.
		db.Exec(`IF SUSER_ID('jd_b3_ops_made') IS NOT NULL DROP LOGIN [jd_b3_ops_made]`)
		t.Cleanup(func() { db.Exec(`IF SUSER_ID('jd_b3_ops_made') IS NOT NULL DROP LOGIN [jd_b3_ops_made]`) })
		made := RoleSpec{Name: "jd_b3_ops_made", Password: "Jd-b3-made#2026", SetPassword: true, SetLogin: true, CreateDB: true, CreateRole: true}
		limited := made
		limited.ConnLimit = 3
		if _, refused := admin.CreateRole(ctx, db, limited).(ErrRoleAttribute); !refused {
			t.Fatal("a connection limit SQL Server cannot set was not refused")
		}
		if _, err := ReadRoleDetail(ctx, db, DriverMSSQL, "jd_b3_ops_made", ""); err != ErrNoSuchRole {
			t.Fatalf("a refused create left a login behind: %v", err)
		}
		if err := admin.CreateRole(ctx, db, made); err != nil {
			t.Fatal(err)
		}
		if created, err := ReadRoleDetail(ctx, db, DriverMSSQL, "jd_b3_ops_made", ""); err != nil || !created.Locked || created.Login ||
			!created.CreateDB || !created.CreateRole || created.Superuser {
			t.Errorf("created as %+v (%v)", created, err)
		}

		if err := admin.CreateRole(ctx, db, RoleSpec{Name: "jd_b3_ops_login", Password: "Jd-b3-first#2026", SetPassword: true, Login: true, CreateDB: true}); err != nil {
			t.Fatal(err)
		}
		// A password change leaves the server role alone, and the login enabled.
		if err := admin.AlterRole(ctx, db, RoleSpec{Name: "jd_b3_ops_login", Password: "Jd-b3-second#2026", SetPassword: true}); err != nil {
			t.Fatal(err)
		}
		detail, err := ReadRoleDetail(ctx, db, DriverMSSQL, "jd_b3_ops_login", "")
		if err != nil {
			t.Fatal(err)
		}
		if !detail.CreateDB || detail.Superuser || detail.Locked {
			t.Errorf("after a password change: %+v", detail.Role)
		}
		locked := true
		if err := admin.AlterRole(ctx, db, RoleSpec{Name: "jd_b3_ops_login", Locked: &locked, SetCreateDB: true}); err != nil {
			t.Fatal(err)
		}
		if detail, err = ReadRoleDetail(ctx, db, DriverMSSQL, "jd_b3_ops_login", ""); err != nil || !detail.Locked || detail.CreateDB {
			t.Errorf("after disabling and dropping dbcreator: %+v %v", detail, err)
		}

		changed, err := ChangePrivileges(ctx, db, DriverMSSQL, PrivilegeChange{Role: "jd_b3_ops_login", Level: GrantOnTable,
			Schema: "dbo", Table: "jd_b3_ops_t", Privileges: []string{"select", "UPDATE"}}, false)
		if err != nil {
			t.Fatal(err)
		}
		stmts := changed.Statements
		if len(stmts) != 2 || stmts[1] != "GRANT SELECT, UPDATE ON OBJECT::[dbo].[jd_b3_ops_t] TO [jd_b3_ops_login]" {
			t.Errorf("grant ran %v", stmts)
		}
		if _, err := ChangePrivileges(ctx, db, DriverMSSQL, PrivilegeChange{Role: "jd_b3_ops_login", Level: GrantOnSchema,
			Schema: "dbo", Privileges: []string{"EXECUTE"}}, false); err != nil {
			t.Fatal(err)
		}
		grants, _, err := ListGrants(ctx, db, DriverMSSQL, GrantFilter{Role: "jd_b3_ops_login"})
		if err != nil {
			t.Fatal(err)
		}
		var onTable, onSchema *Grant
		for i := range grants {
			switch {
			case grants[i].Level == GrantOnTable && grants[i].Table == "jd_b3_ops_t":
				onTable = &grants[i]
			case grants[i].Level == GrantOnSchema && grants[i].Schema == "dbo":
				onSchema = &grants[i]
			}
		}
		if onTable == nil || strings.Join(onTable.Privileges, ",") != "SELECT,UPDATE" || onTable.Schema != "dbo" {
			t.Errorf("table grant: %+v in %+v", onTable, grants)
		}
		if onSchema == nil || strings.Join(onSchema.Privileges, ",") != "EXECUTE" {
			t.Errorf("schema grant: %+v", onSchema)
		}
		if _, err := ChangePrivileges(ctx, db, DriverMSSQL, PrivilegeChange{Role: "jd_b3_ops_login", Level: GrantOnTable,
			Schema: "dbo", Table: "jd_b3_ops_t", Privileges: []string{"UPDATE"}, Revoke: true}, false); err != nil {
			t.Fatal(err)
		}
		grants, _, err = ListGrants(ctx, db, DriverMSSQL, GrantFilter{Schema: "dbo", Table: "jd_b3_ops_t"})
		if err != nil {
			t.Fatal(err)
		}
		if len(grants) != 1 || strings.Join(grants[0].Privileges, ",") != "SELECT" {
			t.Errorf("after the revoke: %+v", grants)
		}
		var database string
		if err := db.QueryRow(`SELECT DB_NAME()`).Scan(&database); err != nil {
			t.Fatal(err)
		}
		if _, err := admin.Grant(ctx, db, DatabaseGrant{Role: "jd_b3_ops_login", Database: database, Level: GrantWrite}); err != nil {
			t.Fatal(err)
		}
		if detail, err = ReadRoleDetail(ctx, db, DriverMSSQL, "jd_b3_ops_login", ""); err != nil ||
			strings.Join(detail.MemberOf, ",") != "db_datareader,db_datawriter" {
			t.Errorf("database roles: %v %v", detail.MemberOf, err)
		}
	})

	t.Run("advisor", func(t *testing.T) {
		opsMustExec(t, db, `CREATE TABLE dbo.jd_b3_ops_child (v INT, parent INT REFERENCES dbo.jd_b3_ops_t(id))`)
		report, err := Advise(ctx, db, DriverMSSQL, "dbo")
		if err != nil {
			t.Fatal(err)
		}
		if !report.EngineChecks || report.EndOfLife == nil || report.EndOfLife.Release != "2022" {
			t.Fatalf("report: %+v", report)
		}
		if len(report.Silences) != 0 {
			t.Errorf("silences: %v", report.Silences)
		}
		by := map[string]Advice{}
		for _, f := range report.Findings {
			by[f.ID] = f
		}
		if sa, ok := by["sa-enabled"]; !ok || sa.SQL != "ALTER LOGIN [sa] DISABLE;" {
			t.Errorf("sa finding: %+v", sa)
		}
		// The keyless table has nothing that could be its key, so the fix is a
		// new column — and it is one the server accepts as written.
		keyless, ok := by["no-primary-key"]
		if !ok || !strings.Contains(keyless.SQL, "ALTER TABLE [dbo].[jd_b3_ops_child] ADD id BIGINT IDENTITY(1,1) NOT NULL PRIMARY KEY;") {
			t.Fatalf("the keyless table: %+v in %+v", keyless, report.Findings)
		}
		opsMustExec(t, db, `INSERT INTO dbo.jd_b3_ops_child VALUES (1, 1)`,
			"ALTER TABLE [dbo].[jd_b3_ops_child] ADD id BIGINT IDENTITY(1,1) NOT NULL PRIMARY KEY",
			// The insert an application already makes, naming no columns.
			`INSERT INTO dbo.jd_b3_ops_child VALUES (2, 2)`)
		if fk, ok := by["unindexed-foreign-key"]; !ok || !strings.Contains(fk.SQL, "ON [dbo].[jd_b3_ops_child] ([parent])") {
			t.Errorf("unindexed foreign key: %+v", fk)
		}
		again, err := Advise(ctx, db, DriverMSSQL, "dbo")
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range again.Findings {
			if f.ID == "no-primary-key" && strings.Contains(strings.Join(f.Objects, ","), "jd_b3_ops_child") {
				t.Errorf("still without a key after the fix: %+v", f)
			}
		}
	})

	// An application's login: a user in its own database and nothing on the
	// server. What it may not read is a sentence naming the permission, not an
	// error and not an empty list that reads as "nothing is waiting".
	t.Run("ordinary_login", func(t *testing.T) {
		drop := func() {
			db.Exec(`IF USER_ID('jd_b3_ops_plain') IS NOT NULL DROP USER [jd_b3_ops_plain]`)
			db.Exec(`IF SUSER_ID('jd_b3_ops_plain') IS NOT NULL DROP LOGIN [jd_b3_ops_plain]`)
		}
		drop()
		t.Cleanup(drop)
		opsMustExec(t, db, `CREATE LOGIN [jd_b3_ops_plain] WITH PASSWORD = N'Jd-b3-plain#2026', CHECK_POLICY = OFF`,
			`CREATE USER [jd_b3_ops_plain] FOR LOGIN [jd_b3_ops_plain]`,
			`ALTER ROLE db_datareader ADD MEMBER [jd_b3_ops_plain]`)
		parsed, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		parsed.User = url.UserPassword("jd_b3_ops_plain", "Jd-b3-plain#2026")
		plain := opsOpen(t, DriverMSSQL, parsed.String(), "the ordinary login")

		if _, err := ListActivity(ctx, plain, DriverMSSQL); !errors.Is(err, ErrNoActivityView) || !strings.Contains(err.Error(), "VIEW SERVER STATE") {
			t.Errorf("sessions: %v", err)
		}
		locks, err := ListLocks(ctx, plain, DriverMSSQL)
		if err != nil || locks.Supported || !strings.Contains(locks.Reason, "VIEW SERVER STATE") || locks.Waits == nil {
			t.Errorf("locks: %+v %v", locks, err)
		}
		statements, err := TopStatements(ctx, plain, DriverMSSQL, StatementsOptions{})
		if err != nil || statements.Supported || !strings.Contains(statements.Reason, "VIEW SERVER STATE") || statements.Statements == nil {
			t.Errorf("statements: %+v %v", statements, err)
		}
		tables, err := ReadTableStats(ctx, plain, DriverMSSQL, StatsOptions{})
		if err != nil || tables.Supported || !strings.Contains(tables.Reason, "VIEW DATABASE STATE") || tables.Tables == nil {
			t.Errorf("tablestats: %+v %v", tables, err)
		}
		indexes, err := ReadIndexStats(ctx, plain, DriverMSSQL, StatsOptions{})
		if err != nil || indexes.Supported || !strings.Contains(indexes.Reason, "VIEW DATABASE STATE") || indexes.Indexes == nil {
			t.Errorf("indexstats: %+v %v", indexes, err)
		}
		// The snapshot keeps the part it may read and names the rest.
		stats, err := ReadServerStats(ctx, plain, DriverMSSQL)
		if err != nil || stats.Version == "" || len(stats.Notes) == 0 {
			t.Errorf("stats: %+v %v", stats, err)
		}
		// The options it may read; none is offered for editing, and a change
		// is refused before the server is asked.
		settings, err := ListSettings(ctx, plain, DriverMSSQL, true)
		if err != nil || len(settings) < 30 {
			t.Fatalf("settings: %d %v", len(settings), err)
		}
		for _, s := range settings {
			if s.Editable {
				t.Fatalf("a login without ALTER SETTINGS is offered %+v", s)
			}
		}
		if _, err := ChangeSetting(ctx, plain, DriverMSSQL, "cost threshold for parallelism", "9", false); err == nil {
			t.Error("a login without ALTER SETTINGS changed an option")
		} else if _, refused := err.(ErrSettingRequest); !refused {
			t.Errorf("the refusal reached the server: %v", err)
		}
		// Maintenance it has no right to is the engine's refusal, in its words.
		if _, err := RunMaintenance(ctx, plain, DriverMSSQL, "", MaintenanceRequest{Action: "rebuild", Table: "jd_b3_ops_t"}); err == nil {
			t.Error("a reader rebuilt an index")
		}
	})

	if locks, err := ListLocks(ctx, db, DriverMSSQL); err != nil || !locks.Supported {
		t.Errorf("locks with nothing waiting: %+v %v", locks, err)
	}
	if rep, err := ReadReplication(ctx, db, DriverMSSQL); err != nil || rep.Supported {
		t.Errorf("replication: %+v %v", rep, err)
	}
}

// --- Oracle ------------------------------------------------------------------

// The Oracle fixture is shared by every stream, so everything made here
// carries this stream's prefix and every parameter changed is put back. The
// administrative DSN is an account that may read the V$ views; the plain one
// is an ordinary application schema, which may not, and has to be told so
// rather than fail.
func TestLiveOpsOracle(t *testing.T) {
	db, dsn := opsLiveAny(t, DriverOracle, "JD_TEST_B3_ORACLE_ADMIN_DSN", "JD_TEST_ORACLE_ADMIN_DSN")
	ctx := t.Context()
	db.Exec(`DROP TABLE jd_b3_ops_t PURGE`)
	opsMustExec(t, db, `CREATE TABLE jd_b3_ops_t (id NUMBER PRIMARY KEY, v NUMBER, w VARCHAR2(20))`,
		`CREATE INDEX jd_b3_ops_t_v ON jd_b3_ops_t (v)`, `CREATE INDEX jd_b3_ops_t_vw ON jd_b3_ops_t (v, w)`,
		`CREATE INDEX jd_b3_ops_t_f ON jd_b3_ops_t (UPPER(w))`,
		`INSERT INTO jd_b3_ops_t VALUES (1, 0, 'a')`)
	t.Cleanup(func() { db.Exec(`DROP TABLE jd_b3_ops_t PURGE`) })

	t.Run("stats", func(t *testing.T) {
		stats, err := ReadServerStats(ctx, db, DriverOracle)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stats.Version, "Oracle") || stats.StartedAt == nil || stats.Connections == nil ||
			stats.Connections.Max <= 0 || stats.Connections.Total < 1 || stats.Role != "standalone" {
			t.Fatalf("stats: %+v", stats)
		}
		if len(stats.Notes) != 0 {
			t.Errorf("an administrator was refused part of the snapshot: %v", stats.Notes)
		}
		for _, key := range []string{StatTransactionsCommitted, StatTransactionsRolledBack, StatQueries, StatBlocksRead, StatBlocksHit, StatRowsRead, StatDeadlocks} {
			if _, ok := stats.Counters[key]; !ok {
				t.Errorf("counter %q is missing: %v", key, stats.Counters)
			}
		}
	})

	// The blocker used to be reported as a bare sid, which matched no row's
	// handle and could not be handed to a kill.
	t.Run("sessions", func(t *testing.T) {
		holder, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Close()
		waiter, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer waiter.Close()
		const handle = `SELECT TO_CHAR(sid) || ',' || TO_CHAR(serial#) FROM v$session WHERE sid = SYS_CONTEXT('USERENV', 'SID')`
		var holderPID, waiterPID string
		if err := holder.QueryRowContext(ctx, handle).Scan(&holderPID); err != nil {
			t.Fatal(err)
		}
		if err := waiter.QueryRowContext(ctx, handle).Scan(&waiterPID); err != nil {
			t.Fatal(err)
		}
		tx, err := holder.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx, `UPDATE jd_b3_ops_t SET v = 1 WHERE id = 1`); err != nil {
			t.Fatal(err)
		}
		blocked := make(chan error, 1)
		go func() {
			_, err := waiter.ExecContext(context.Background(), `UPDATE jd_b3_ops_t SET v = 2 WHERE id = 1`)
			blocked <- err
		}()
		var sessions []Activity
		waitFor(t, "the waiter to be blocked", func() bool {
			sessions, err = ListActivity(ctx, db, DriverOracle)
			if err != nil {
				t.Fatal(err)
			}
			w := findSession(sessions, waiterPID)
			return w != nil && w.Status == SessionBlocked
		})
		h, w := findSession(sessions, holderPID), findSession(sessions, waiterPID)
		if h == nil || h.Status != SessionIdleInTransaction || h.Seconds != 0 {
			t.Errorf("holder: %+v", h)
		}
		if len(w.BlockedByPIDs) != 1 || w.BlockedByPIDs[0] != holderPID || w.BlockedBy != holderPID {
			t.Errorf("waiter blocked by %v (%q), want %s", w.BlockedByPIDs, w.BlockedBy, holderPID)
		}
		if !strings.Contains(w.Query, "jd_b3_ops_t") || w.WaitEvent == "" {
			t.Errorf("waiter: %+v", w)
		}
		seen := map[string]bool{}
		for _, s := range sessions {
			if seen[s.PID] {
				t.Errorf("session %s is listed twice", s.PID)
			}
			seen[s.PID] = true
		}
		// The same wait, from the lock views: who, behind whom, on what.
		locks, err := ListLocks(ctx, db, DriverOracle)
		if err != nil {
			t.Fatal(err)
		}
		if !locks.Supported || locks.Reason != "" {
			t.Fatalf("locks: %+v", locks)
		}
		var wait *LockWait
		for i := range locks.Waits {
			if locks.Waits[i].WaitingPID == waiterPID {
				wait = &locks.Waits[i]
			}
		}
		if wait == nil || wait.BlockingPID != holderPID || !strings.HasSuffix(wait.Object, ".JD_B3_OPS_T") ||
			wait.LockType != "row (transaction)" || wait.Mode != "exclusive" || wait.BlockingState != "idle in transaction" ||
			!strings.Contains(wait.WaitingQuery, "jd_b3_ops_t") || !strings.Contains(wait.BlockingQuery, "jd_b3_ops_t") ||
			wait.WaitingUser == "" || wait.BlockingUser == "" {
			t.Fatalf("wait: %+v in %+v", wait, locks.Waits)
		}
		var held, asked bool
		for _, l := range locks.Locks {
			if l.PID == holderPID && l.Granted && l.LockType == "table" && strings.HasSuffix(l.Object, ".JD_B3_OPS_T") && l.Mode == "row exclusive" {
				held = true
			}
			if l.PID == waiterPID && !l.Granted && l.LockType == "row (transaction)" && l.Mode == "exclusive" {
				asked = true
			}
		}
		if !held || !asked {
			t.Errorf("the lock table lacks the holder's table lock (%v) or the waiter's request (%v): %+v", held, asked, locks.Locks)
		}
		if err := CancelQuery(ctx, db, DriverOracle, waiterPID); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-blocked:
			if err == nil {
				t.Error("the cancelled statement succeeded")
			}
		case <-time.After(15 * time.Second):
			t.Fatal("the cancelled statement never returned")
		}
		var alive int
		if err := waiter.QueryRowContext(ctx, `SELECT 1 FROM dual`).Scan(&alive); err != nil {
			t.Errorf("the session did not survive a cancel: %v", err)
		}
	})

	t.Run("settings", func(t *testing.T) {
		all, err := ListSettings(ctx, db, DriverOracle, true)
		if err != nil {
			t.Fatal(err)
		}
		if len(all) < 100 {
			t.Fatalf("%d parameters", len(all))
		}
		by := map[string]Setting{}
		for _, s := range all {
			by[s.Name] = s
			// Only what the running instance can change is offered.
			if s.Editable && s.Context != "immediate" && s.Context != "deferred" {
				t.Fatalf("a parameter read only at start is editable: %+v", s)
			}
		}
		if s := by["processes"]; s.Type != "integer" || s.Value == "" || s.Description == "" {
			t.Errorf("processes: %+v", s)
		}
		// The block size is fixed when the database is created.
		if s := by["db_block_size"]; !s.RestartRequired || s.Context != "false" || s.Editable {
			t.Errorf("db_block_size: %+v", s)
		}
		if s := by["undo_retention"]; !s.Editable || s.Context != "immediate" || s.Type != "integer" {
			t.Fatalf("undo_retention: %+v", s)
		}
		if s := by["sort_area_size"]; !s.Editable || s.Context != "deferred" {
			t.Errorf("sort_area_size: %+v", s)
		}
		// Inside a pluggable database a parameter the container may not set for
		// itself is listed and not offered.
		var container int
		if err := db.QueryRow(`SELECT TO_NUMBER(SYS_CONTEXT('USERENV', 'CON_ID')) FROM dual`).Scan(&container); err == nil && container > 1 {
			if s := by["processes"]; s.Editable {
				t.Errorf("processes is offered inside a pluggable database: %+v", s)
			}
			if _, err := ChangeSetting(ctx, db, DriverOracle, "processes", "300", false); err == nil {
				t.Error("processes was changed from inside a pluggable database")
			}
		}
		short, err := ListSettings(ctx, db, DriverOracle, false)
		if err != nil || len(short) == 0 || len(short) > len(oracleSettingNames) {
			t.Errorf("short list: %d %v", len(short), err)
		}
		if !SettingsWritable(DriverOracle) {
			t.Error("Oracle parameters are reported as not writable")
		}

		system := func(name string) string {
			t.Helper()
			var v sql.NullString
			if err := db.QueryRow(`SELECT display_value FROM v$system_parameter WHERE name = :1`, name).Scan(&v); err != nil {
				t.Fatal(err)
			}
			return v.String
		}
		t.Cleanup(func() {
			db.Exec(`ALTER SYSTEM RESET undo_retention SCOPE=BOTH`)
			db.Exec(`ALTER SYSTEM RESET sort_area_size DEFERRED SCOPE=BOTH`)
			db.Exec(`ALTER SYSTEM RESET cursor_sharing SCOPE=BOTH`)
			db.Exec(`ALTER SYSTEM RESET resource_limit SCOPE=BOTH`)
		})
		before := system("undo_retention")
		changed, err := ChangeSetting(ctx, db, DriverOracle, "UNDO_RETENTION", "901", false)
		if err != nil {
			t.Fatal(err)
		}
		if changed.Name != "undo_retention" || changed.Value != "901" || !changed.Persisted || changed.RestartRequired ||
			len(changed.Statements) != 1 || changed.Statements[0] != "ALTER SYSTEM SET undo_retention = 901 SCOPE=BOTH" {
			t.Errorf("change: %+v", changed)
		}
		if got := system("undo_retention"); got != "901" {
			t.Errorf("the instance runs with undo_retention %s", got)
		}
		// Written to the parameter file as well, which is what outlasts a restart.
		var stored sql.NullString
		if err := db.QueryRow(`SELECT MAX(value) FROM v$spparameter WHERE name = 'undo_retention' AND isspecified = 'TRUE'`).Scan(&stored); err != nil || stored.String != "901" {
			t.Errorf("the parameter file holds %q (%v)", stored.String, err)
		}
		reset, err := ChangeSetting(ctx, db, DriverOracle, "undo_retention", "", true)
		if err != nil || reset.Statements[0] != "ALTER SYSTEM RESET undo_retention SCOPE=BOTH" || reset.Value != before {
			t.Errorf("reset: %+v %v (was %s)", reset, err, before)
		}
		// A deferred parameter takes the keyword and applies to new sessions.
		deferred, err := ChangeSetting(ctx, db, DriverOracle, "sort_area_size", "65537", false)
		if err != nil || deferred.Statements[0] != "ALTER SYSTEM SET sort_area_size = 65537 DEFERRED SCOPE=BOTH" ||
			deferred.Value != "65537" || !strings.Contains(deferred.Note, "connect from now on") {
			t.Errorf("deferred: %+v %v", deferred, err)
		}
		if back, err := ChangeSetting(ctx, db, DriverOracle, "sort_area_size", "", true); err != nil ||
			back.Statements[0] != "ALTER SYSTEM RESET sort_area_size DEFERRED SCOPE=BOTH" {
			t.Errorf("deferred reset: %+v %v", back, err)
		}
		// A word goes in quoted, a boolean as its keyword.
		word, err := ChangeSetting(ctx, db, DriverOracle, "cursor_sharing", by["cursor_sharing"].Value, false)
		if err != nil || word.Statements[0] != "ALTER SYSTEM SET cursor_sharing = '"+by["cursor_sharing"].Value+"' SCOPE=BOTH" {
			t.Errorf("a string parameter: %+v %v", word, err)
		}
		flag, err := ChangeSetting(ctx, db, DriverOracle, "resource_limit", by["resource_limit"].Value, false)
		if err != nil || flag.Statements[0] != "ALTER SYSTEM SET resource_limit = "+strings.ToUpper(by["resource_limit"].Value)+" SCOPE=BOTH" {
			t.Errorf("a boolean parameter: %+v %v", flag, err)
		}
		for name, value := range map[string]string{
			"db_block_size": "16384", "undo_retention": "nine hundred", "no_such_parameter": "1", "resource_limit": "perhaps",
		} {
			_, err := ChangeSetting(ctx, db, DriverOracle, name, value, false)
			if _, refused := err.(ErrSettingRequest); !refused {
				t.Errorf("%s = %q: %v, want a refusal", name, value, err)
			}
		}
		// A value the engine itself refuses comes back in the engine's words.
		if _, err := ChangeSetting(ctx, db, DriverOracle, "cursor_sharing", "SIDEWAYS", false); err == nil || !strings.Contains(err.Error(), "ORA-") {
			t.Errorf("a value outside the parameter's own set: %v", err)
		}
	})

	t.Run("table_and_index_stats", func(t *testing.T) {
		// Statistics another schema's tables are read through DBA_SEGMENTS,
		// which this account may read.
		if _, err := RunMaintenance(ctx, db, DriverOracle, "", MaintenanceRequest{Action: "gather_stats", Table: "JD_B3_OPS_T"}); err != nil {
			t.Fatal(err)
		}
		var owner string
		if err := db.QueryRow(`SELECT SYS_CONTEXT('USERENV', 'CURRENT_SCHEMA') FROM dual`).Scan(&owner); err != nil {
			t.Fatal(err)
		}
		tables, err := ReadTableStats(ctx, db, DriverOracle, StatsOptions{Limit: 1000})
		if err != nil {
			t.Fatal(err)
		}
		if !tables.Supported || tables.Reason != "" {
			t.Fatalf("tablestats: supported %v reason %q", tables.Supported, tables.Reason)
		}
		var stat *TableStat
		for i := range tables.Tables {
			if tables.Tables[i].Table == "JD_B3_OPS_T" {
				stat = &tables.Tables[i]
			}
		}
		if stat == nil || stat.Schema != owner || stat.Rows != 1 || stat.TableBytes <= 0 || stat.IndexBytes <= 0 ||
			stat.TotalBytes != stat.TableBytes+stat.IndexBytes+stat.ToastBytes || stat.LastAnalyze == nil ||
			stat.SeqScans != -1 || stat.DeadRows != -1 || stat.Kind != "table" {
			t.Fatalf("table stat: %+v", stat)
		}
		for i := 1; i < len(tables.Tables); i++ {
			if tables.Tables[i].TotalBytes > tables.Tables[i-1].TotalBytes {
				t.Fatalf("tables are not largest first: %s after %s", tables.Tables[i].Table, tables.Tables[i-1].Table)
			}
		}
		if cut, err := ReadTableStats(ctx, db, DriverOracle, StatsOptions{Limit: 1}); err != nil || len(cut.Tables) != 1 || !cut.Truncated {
			t.Errorf("limit 1: %+v %v", cut, err)
		}

		indexes, err := ReadIndexStats(ctx, db, DriverOracle, StatsOptions{Table: "JD_B3_OPS_T"})
		if err != nil {
			t.Fatal(err)
		}
		if !indexes.Supported || len(indexes.Notes) == 0 {
			t.Fatalf("indexstats: %+v", indexes)
		}
		by := map[string]IndexStat{}
		primary := ""
		for _, ix := range indexes.Indexes {
			by[ix.Name] = ix
			if ix.Primary {
				primary = ix.Name
			}
			if ix.Bytes <= 0 || !ix.Valid || ix.Unused || ix.Table != "JD_B3_OPS_T" {
				t.Errorf("index: %+v", ix)
			}
		}
		if len(by) != 4 || primary == "" {
			t.Fatalf("indexes: %+v", indexes.Indexes)
		}
		if pk := by[primary]; !pk.Unique || !pk.Constraint || len(pk.Columns) != 1 || pk.Columns[0] != "ID" {
			t.Errorf("primary key index: %+v", pk)
		}
		if narrow, wide := by["JD_B3_OPS_T_V"], by["JD_B3_OPS_T_VW"]; narrow.CoveredBy != "JD_B3_OPS_T_VW" || wide.CoveredBy != "" ||
			strings.Join(wide.Columns, ",") != "V,W" || narrow.Method != "normal" {
			t.Errorf("covered index: %+v / %+v", narrow, wide)
		}
		// An index on an expression is neither a duplicate of anything nor
		// covered by anything, whatever its hidden column is called.
		if f := by["JD_B3_OPS_T_F"]; !strings.Contains(f.Method, "function-based") || f.CoveredBy != "" || f.DuplicateOf != "" {
			t.Errorf("function-based index: %+v", f)
		}
	})

	t.Run("maintenance", func(t *testing.T) {
		out, err := RunMaintenance(ctx, db, DriverOracle, "", MaintenanceRequest{Action: "gather_stats", Table: "JD_B3_OPS_T"})
		if err != nil {
			t.Fatal(err)
		}
		var owner string
		if err := db.QueryRow(`SELECT SYS_CONTEXT('USERENV', 'CURRENT_SCHEMA') FROM dual`).Scan(&owner); err != nil {
			t.Fatal(err)
		}
		want := `BEGIN DBMS_STATS.GATHER_TABLE_STATS(ownname => '"` + owner + `"', tabname => '"JD_B3_OPS_T"', cascade => TRUE); END;`
		if !out.OK || len(out.Statements) != 1 || out.Statements[0] != want || len(out.Output) != 1 ||
			!strings.HasPrefix(out.Output[0], "JD_B3_OPS_T: 1 row, ") || !strings.Contains(out.Output[0], "analysed 20") {
			t.Errorf("gather for a table: %+v", out)
		}

		// A whole schema, in one of this file's own: two tables, one of them
		// created with a quoted lower-case name, which is found only because
		// the name goes to DBMS_STATS quoted.
		db.Exec(`DROP USER jd_b3_schema CASCADE`)
		opsMustExec(t, db, `CREATE USER jd_b3_schema IDENTIFIED BY "Jd_b3_pw_2026" QUOTA UNLIMITED ON users`,
			`CREATE TABLE jd_b3_schema.first_t (id NUMBER PRIMARY KEY)`, `INSERT INTO jd_b3_schema.first_t VALUES (1)`,
			`INSERT INTO jd_b3_schema.first_t VALUES (2)`,
			`CREATE TABLE jd_b3_schema."lower_t" (id NUMBER)`, `INSERT INTO jd_b3_schema."lower_t" VALUES (1)`)
		t.Cleanup(func() { db.Exec(`DROP USER jd_b3_schema CASCADE`) })
		schema, err := RunMaintenance(ctx, db, DriverOracle, "", MaintenanceRequest{Action: "gather_stats", Schema: "JD_B3_SCHEMA"})
		if err != nil {
			t.Fatal(err)
		}
		if schema.Statements[0] != `BEGIN DBMS_STATS.GATHER_SCHEMA_STATS(ownname => '"JD_B3_SCHEMA"', cascade => TRUE); END;` ||
			len(schema.Output) != 2 || !strings.HasPrefix(schema.Output[0], "FIRST_T: 2 rows, ") || !strings.HasPrefix(schema.Output[1], "lower_t: 1 row, ") {
			t.Errorf("gather for a schema: %+v", schema)
		}
		lower, err := RunMaintenance(ctx, db, DriverOracle, "", MaintenanceRequest{Action: "gather_stats", Schema: "JD_B3_SCHEMA", Table: "lower_t"})
		if err != nil || len(lower.Output) != 1 || !strings.HasPrefix(lower.Output[0], "lower_t: 1 row, ") {
			t.Errorf("a table with a lower-case name: %+v %v", lower, err)
		}
		// Another schema's tables are sized from DBA_SEGMENTS.
		tables, err := ReadTableStats(ctx, db, DriverOracle, StatsOptions{Schema: "JD_B3_SCHEMA"})
		if err != nil || !tables.Supported || len(tables.Tables) != 2 {
			t.Fatalf("another schema's tables: %+v %v", tables, err)
		}
		for _, table := range tables.Tables {
			if table.Schema != "JD_B3_SCHEMA" || table.TableBytes <= 0 || table.LastAnalyze == nil || table.Rows < 1 {
				t.Errorf("table: %+v", table)
			}
		}

		for _, bad := range []MaintenanceRequest{{Action: "vacuum"}, {Action: "gather_stats", Table: "JD_B3_OPS_T", Index: "X"}} {
			if _, err := RunMaintenance(ctx, db, DriverOracle, "", bad); err == nil {
				t.Errorf("%+v ran", bad)
			} else if _, refused := err.(ErrMaintenanceRequest); !refused {
				t.Errorf("%+v: %v is not a request refusal", bad, err)
			}
		}
		if _, err := RunMaintenance(ctx, db, DriverOracle, "", MaintenanceRequest{Action: "gather_stats", Table: "JD_B3_NO_SUCH_TABLE"}); err == nil || !strings.Contains(err.Error(), "ORA-") {
			t.Errorf("a table that is not there: %v", err)
		}
	})

	// What a closed request does to a block that is still running: the driver
	// sends a break, and the server acts on it when it next looks — some
	// seconds into a block that is working, and not at all while one sleeps.
	// The block here only counts for a minute; gathering statistics on these
	// tables is over before a request could be closed, and it runs through the
	// same call.
	t.Run("a_closed_request_stops_the_block", func(t *testing.T) {
		const busy = `DECLARE n NUMBER := 0; t TIMESTAMP := SYSTIMESTAMP + INTERVAL '60' SECOND; BEGIN WHILE SYSTIMESTAMP < t LOOP n := n + 1; END LOOP; END; -- jd_b3 busy`
		running := func() bool {
			t.Helper()
			var n int
			if err := db.QueryRow(`
			  SELECT COUNT(*) FROM v$session s JOIN v$sql q ON q.sql_id = s.sql_id AND q.child_number = s.sql_child_number
			  WHERE s.status = 'ACTIVE' AND q.sql_text LIKE '%jd_b3 busy%' AND q.sql_text NOT LIKE '%v$session%'`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n > 0
		}
		request, abandon := context.WithCancel(ctx)
		defer abandon()
		done := make(chan error, 1)
		go func() {
			_, err := db.ExecContext(request, busy)
			done <- err
		}()
		waitFor(t, "the block to be running", running)
		abandoned := time.Now()
		abandon()
		select {
		case err := <-done:
			if err == nil {
				t.Error("an abandoned block reported success")
			}
			t.Logf("the block stopped %s after the request was closed: %v", time.Since(abandoned).Round(100*time.Millisecond), err)
		case <-time.After(40 * time.Second):
			t.Fatal("the abandoned block ran on")
		}
		waitFor(t, "the block to stop on the server", func() bool { return !running() })
	})

	// The shared pool keeps a statement as it was sent; what is listed is its
	// shape.
	t.Run("statements", func(t *testing.T) {
		const probe = `SELECT COUNT(*) FROM jd_b3_ops_t WHERE v = 424242 AND 'jd-b3-literal' = w`
		for i := 0; i < 3; i++ {
			opsMustExec(t, db, probe)
		}
		var mine *Statement
		var report *StatementsReport
		waitFor(t, "the probe to reach v$sqlstats", func() bool {
			var err error
			report, err = TopStatements(ctx, db, DriverOracle, StatementsOptions{Sort: StatementsByCalls, Limit: 200})
			if err != nil {
				t.Fatal(err)
			}
			for i := range report.Statements {
				if strings.Contains(report.Statements[i].Query, "FROM jd_b3_ops_t WHERE v = ? AND ? = w") {
					mine = &report.Statements[i]
				}
			}
			return mine != nil
		})
		if !report.Supported || report.Resettable || report.TotalMs <= 0 {
			t.Fatalf("report: supported %v resettable %v total %v reason %q", report.Supported, report.Resettable, report.TotalMs, report.Reason)
		}
		if mine.Calls < 3 || mine.ID == "" || mine.MaxMs != 0 || mine.Share < 0 || mine.Share > 1 {
			t.Errorf("the probe: %+v", mine)
		}
		for _, st := range report.Statements {
			if strings.Contains(st.Query, "jd-b3-literal") || strings.Contains(st.Query, "424242") {
				t.Errorf("a literal reached the list: %q", st.Query)
			}
		}
		for _, sort := range []string{StatementsByTotal, StatementsByMean, StatementsByRows} {
			if sorted, err := TopStatements(ctx, db, DriverOracle, StatementsOptions{Sort: sort, Limit: 3}); err != nil || !sorted.Supported || len(sorted.Statements) > 3 {
				t.Errorf("sort %s: %+v %v", sort, sorted, err)
			}
		}
		if _, err := TopStatements(ctx, db, DriverOracle, StatementsOptions{Sort: StatementsByMax}); err == nil {
			t.Error("sorted by a maximum Oracle does not keep")
		} else if _, bad := err.(ErrBadStatementsOption); !bad {
			t.Errorf("sort max: %v", err)
		}
		if _, err := ResetStatements(ctx, db, DriverOracle); err != ErrUnsupported {
			t.Errorf("reset: %v", err)
		}
	})

	t.Run("advisor", func(t *testing.T) {
		db.Exec(`DROP VIEW jd_b3_ops_broken`)
		db.Exec(`DROP TABLE jd_b3_ops_gone PURGE`)
		// A view over a table that is then dropped is the classic invalid object.
		opsMustExec(t, db, `CREATE TABLE jd_b3_ops_gone (id NUMBER)`, `CREATE VIEW jd_b3_ops_broken AS SELECT id FROM jd_b3_ops_gone`,
			`DROP TABLE jd_b3_ops_gone PURGE`)
		t.Cleanup(func() { db.Exec(`DROP VIEW jd_b3_ops_broken`) })
		report, err := Advise(ctx, db, DriverOracle, "")
		if err != nil {
			t.Fatal(err)
		}
		if !report.EngineChecks || report.Version == "" {
			t.Fatalf("report: %+v", report)
		}
		var invalid *Advice
		for i := range report.Findings {
			if report.Findings[i].ID == "invalid-objects" {
				invalid = &report.Findings[i]
			}
		}
		if invalid == nil || !strings.Contains(invalid.SQL, `"JD_B3_OPS_BROKEN" COMPILE;`) {
			t.Errorf("invalid objects: %+v in %+v", invalid, report.Findings)
		}
		// This account may read the DBA views, so the two checks that need
		// them were made rather than passed over.
		for _, silence := range report.Silences {
			if strings.Contains(silence, "Default passwords") || strings.Contains(silence, "Tablespace usage") {
				t.Errorf("a check an administrator can make was not made: %s", silence)
			}
		}
	})

	// A table that is keyed in everything but name is offered the statement
	// that names the key, and the statement is one the server accepts.
	t.Run("advisor_fix_runs", func(t *testing.T) {
		db.Exec(`DROP USER jd_b3_fix CASCADE`)
		opsMustExec(t, db, `CREATE USER jd_b3_fix IDENTIFIED BY "Jd_b3_pw_2026" QUOTA UNLIMITED ON users`,
			`CREATE TABLE jd_b3_fix.keyed (tenant NUMBER NOT NULL, code VARCHAR2(20) NOT NULL, note VARCHAR2(20))`,
			`CREATE UNIQUE INDEX jd_b3_fix.keyed_code ON jd_b3_fix.keyed (tenant, code)`,
			`CREATE TABLE jd_b3_fix.bare (v NUMBER, note VARCHAR2(20))`)
		t.Cleanup(func() { db.Exec(`DROP USER jd_b3_fix CASCADE`) })
		// Each finding's targets by name, with the fix each carries.
		targets := func(id string) map[string]AdviceTarget {
			t.Helper()
			report, err := Advise(ctx, db, DriverOracle, "JD_B3_FIX")
			if err != nil {
				t.Fatal(err)
			}
			out := map[string]AdviceTarget{}
			for _, f := range report.Findings {
				if f.ID == id {
					for _, target := range f.Targets {
						out[target.Name] = target
					}
				}
			}
			return out
		}
		keyless := targets("no-primary-key")
		if got := keyless["KEYED"].SQL; got != `ALTER TABLE "JD_B3_FIX"."KEYED" ADD PRIMARY KEY ("TENANT", "CODE");` {
			t.Fatalf("a table with a unique index: %+v", keyless["KEYED"])
		}
		// A new column would break every INSERT that names no columns.
		if bare, ok := keyless["BARE"]; !ok || bare.SQL != "" {
			t.Errorf("a table with nothing to be a key: %+v (found %v)", bare, ok)
		}
		// The statement offered for a table with no statistics is the block the
		// maintenance action runs.
		unanalysed := targets("never-analysed")
		if got := unanalysed["BARE"].SQL; got != `BEGIN DBMS_STATS.GATHER_TABLE_STATS(ownname => '"JD_B3_FIX"', tabname => '"BARE"', cascade => TRUE); END;` {
			t.Fatalf("a table with no statistics: %+v", unanalysed["BARE"])
		}
		opsMustExec(t, db, strings.TrimSuffix(keyless["KEYED"].SQL, ";"), unanalysed["BARE"].SQL)
		if _, still := targets("no-primary-key")["KEYED"]; still {
			t.Error("still without a key after the fix")
		}
		if _, still := targets("never-analysed")["BARE"]; still {
			t.Error("still without statistics after the fix")
		}
	})

	t.Run("unsupported_surfaces_say_so", func(t *testing.T) {
		if rep, err := ReadReplication(ctx, db, DriverOracle); err != nil || rep.Supported || rep.Reason == "" {
			t.Errorf("replication: %+v %v", rep, err)
		}
		if _, err := AdminFor(DriverOracle); err != ErrUnsupported {
			t.Errorf("admin: %v", err)
		}
		if levels := PrivilegeLevelsFor(DriverOracle); len(levels) != 0 {
			t.Errorf("privileges: %+v", levels)
		}
	})

	// An account that can sign in and nothing more — what an application
	// schema is on every release before the developer role of 23. Every read
	// answers it: with what it may see, or with the grant it lacks.
	t.Run("bare_account", func(t *testing.T) {
		db.Exec(`DROP USER jd_b3_bare CASCADE`)
		opsMustExec(t, db, `CREATE USER jd_b3_bare IDENTIFIED BY "Jd_b3_pw_2026"`, `GRANT CREATE SESSION TO jd_b3_bare`)
		t.Cleanup(func() { db.Exec(`DROP USER jd_b3_bare CASCADE`) })
		parsed, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		parsed.User = url.UserPassword("jd_b3_bare", "Jd_b3_pw_2026")
		bare := opsOpen(t, DriverOracle, parsed.String(), "the bare account")

		_, err = ListSettings(ctx, bare, DriverOracle, true)
		if _, refused := err.(ErrSettingsRefused); !refused || !strings.Contains(err.Error(), "v$parameter") {
			t.Errorf("settings: %v", err)
		}
		if _, err := ChangeSetting(ctx, bare, DriverOracle, "undo_retention", "901", false); err == nil {
			t.Error("an account that may not read the parameters changed one")
		}
		if _, err := ListActivity(ctx, bare, DriverOracle); !errors.Is(err, ErrNoActivityView) || !strings.Contains(err.Error(), "v$session") {
			t.Errorf("sessions: %v", err)
		}
		if locks, err := ListLocks(ctx, bare, DriverOracle); err != nil || locks.Supported || locks.Reason == "" {
			t.Errorf("locks: %+v %v", locks, err)
		}
		if statements, err := TopStatements(ctx, bare, DriverOracle, StatementsOptions{}); err != nil || statements.Supported || statements.Reason == "" {
			t.Errorf("statements: %+v %v", statements, err)
		}
		stats, err := ReadServerStats(ctx, bare, DriverOracle)
		if err != nil || stats.Version == "" || len(stats.Notes) < 3 {
			t.Errorf("stats: %+v %v", stats, err)
		}
		// The catalogue's ALL_ views answer anyone, with what they may see:
		// an account that owns nothing has nothing to list, and is not refused.
		if tables, err := ReadTableStats(ctx, bare, DriverOracle, StatsOptions{}); err != nil || !tables.Supported || len(tables.Tables) != 0 {
			t.Errorf("tablestats: %+v %v", tables, err)
		}
		if indexes, err := ReadIndexStats(ctx, bare, DriverOracle, StatsOptions{}); err != nil || !indexes.Supported || len(indexes.Indexes) != 0 {
			t.Errorf("indexstats: %+v %v", indexes, err)
		}
		if report, err := Advise(ctx, bare, DriverOracle, ""); err != nil || len(report.Silences) < 2 {
			t.Errorf("advisor: %+v %v", report, err)
		}
	})

	// An application schema with no grant on the V$ views gets a snapshot
	// with the parts it may read and a note for each it may not.
	t.Run("ordinary_account", func(t *testing.T) {
		plain, _ := opsLiveAny(t, DriverOracle, "JD_TEST_B3_ORACLE_DSN", "JD_TEST_ORACLE_DSN")
		stats, err := ReadServerStats(ctx, plain, DriverOracle)
		if err != nil {
			t.Fatalf("an ordinary account got no snapshot at all: %v", err)
		}
		if stats.Version == "" || len(stats.Notes) == 0 {
			t.Errorf("stats: %+v", stats)
		}
		for _, note := range stats.Notes {
			if !strings.Contains(note, "v$") {
				t.Errorf("a note does not name the view: %q", note)
			}
		}
		report, err := Advise(ctx, plain, DriverOracle, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(report.Silences) == 0 {
			t.Errorf("the checks that need DBA views read as passed: %+v", report)
		}

		// What it may not read is a sentence naming the grant, not an error
		// and not an empty list that reads as "nothing is waiting".
		if _, err := ListActivity(ctx, plain, DriverOracle); !errors.Is(err, ErrNoActivityView) || !strings.Contains(err.Error(), "v$session") {
			t.Errorf("sessions: %v", err)
		}
		locks, err := ListLocks(ctx, plain, DriverOracle)
		if err != nil || locks.Supported || !strings.Contains(locks.Reason, "v$session") || locks.Waits == nil {
			t.Errorf("locks: %+v %v", locks, err)
		}
		statements, err := TopStatements(ctx, plain, DriverOracle, StatementsOptions{})
		if err != nil || statements.Supported || !strings.Contains(statements.Reason, "v$sqlstats") || statements.Statements == nil {
			t.Errorf("statements: %+v %v", statements, err)
		}
		// Its parameters it may read, where it holds that grant, and none of
		// them is offered for editing: it has no ALTER SYSTEM.
		if settings, err := ListSettings(ctx, plain, DriverOracle, true); err == nil {
			for _, s := range settings {
				if s.Editable {
					t.Fatalf("an account without ALTER SYSTEM is offered %+v", s)
				}
			}
			if _, err := ChangeSetting(ctx, plain, DriverOracle, "undo_retention", "901", false); err == nil {
				t.Error("an account without ALTER SYSTEM changed a parameter")
			} else if _, refused := err.(ErrSettingRequest); !refused {
				t.Errorf("the refusal reached the server: %v", err)
			}
		}

		// Its own schema it can maintain and measure with no grant at all:
		// DBMS_STATS on its own tables, sizes from USER_SEGMENTS.
		plain.Exec(`DROP TABLE jd_b3_plain_t PURGE`)
		opsMustExec(t, plain, `CREATE TABLE jd_b3_plain_t (id NUMBER PRIMARY KEY, v VARCHAR2(40))`,
			`INSERT INTO jd_b3_plain_t SELECT LEVEL, 'row ' || LEVEL FROM dual CONNECT BY LEVEL <= 50`)
		t.Cleanup(func() { plain.Exec(`DROP TABLE jd_b3_plain_t PURGE`) })
		gathered, err := RunMaintenance(ctx, plain, DriverOracle, "", MaintenanceRequest{Action: "gather_stats", Table: "JD_B3_PLAIN_T"})
		if err != nil || len(gathered.Output) != 1 || !strings.HasPrefix(gathered.Output[0], "JD_B3_PLAIN_T: 50 rows, ") {
			t.Fatalf("gather on its own table: %+v %v", gathered, err)
		}
		tables, err := ReadTableStats(ctx, plain, DriverOracle, StatsOptions{})
		if err != nil || !tables.Supported {
			t.Fatalf("tablestats: %+v %v", tables, err)
		}
		var stat *TableStat
		for i := range tables.Tables {
			if tables.Tables[i].Table == "JD_B3_PLAIN_T" {
				stat = &tables.Tables[i]
			}
		}
		if stat == nil || stat.Rows != 50 || stat.TableBytes <= 0 || stat.IndexBytes <= 0 || stat.LastAnalyze == nil {
			t.Errorf("its own table: %+v", stat)
		}
		indexes, err := ReadIndexStats(ctx, plain, DriverOracle, StatsOptions{Table: "JD_B3_PLAIN_T"})
		if err != nil || !indexes.Supported || len(indexes.Indexes) != 1 || !indexes.Indexes[0].Primary || indexes.Indexes[0].Bytes <= 0 ||
			indexes.Indexes[0].Scans != -1 || !strings.Contains(strings.Join(indexes.Notes, " "), "DBA_INDEX_USAGE") {
			t.Errorf("its own index: %+v %v", indexes, err)
		}
		// Another schema's tables it may not size; that is a note, with the
		// size the statistics recorded in its place.
		if other, err := ReadTableStats(ctx, plain, DriverOracle, StatsOptions{Schema: "SYSTEM"}); err != nil || !other.Supported {
			t.Errorf("another schema: %+v %v", other, err)
		}
	})
}
