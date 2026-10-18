package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Statement history against real servers: two snapshots with known work
// between them, and the interval holds that work and nothing from before.
//
// Each test runs its statements in a database of its own, so the fixture's
// other users' statements are not among them, and never resets a server's
// statistics, which are every user's. What the fixture's other users do to
// the statistics meanwhile — a reset, a digest table emptied — is what an
// interval is built to survive, so a test reads its cycle again when the
// counters were plainly taken from under it.

const historyDB = "jdt_query_insights"

// historyRun is the statement whose text holds every fragment, in an interval.
func historyRun(t *testing.T, snap *StatementSnapshot, in *StatementsInterval, fragments ...string) (string, *StatementRun) {
	t.Helper()
	for id, text := range snap.Texts {
		found := true
		for _, fragment := range fragments {
			found = found && strings.Contains(text, fragment)
		}
		if !found {
			continue
		}
		for i := range in.Statements {
			if in.Statements[i].ID == id {
				return id, &in.Statements[i]
			}
		}
		return id, nil
	}
	return "", nil
}

// sameTextsAsTheList holds a snapshot's texts to the statement list's: the
// history keeps nothing the list does not show.
func sameTextsAsTheList(t *testing.T, ctx context.Context, db *sql.DB, driver Driver, snap *StatementSnapshot) {
	t.Helper()
	report, err := TopStatements(ctx, db, driver, StatementsOptions{Sort: StatementsByTotal, Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]string{}
	for _, s := range report.Statements {
		if listed[s.ID] == "" {
			listed[s.ID] = s.Query
		}
	}
	for id, text := range snap.Texts {
		if want, ok := listed[id]; ok && want != text {
			t.Errorf("the snapshot's text of %s is %q, the list's %q", id, text, want)
		}
	}
}

func TestLivePostgresStatementHistory(t *testing.T) {
	server, dsn := opsLive(t, DriverPostgres, "JD_TEST_POSTGRES_DSN")
	ctx := t.Context()
	var super bool
	var preload sql.NullString
	if err := server.QueryRowContext(ctx, `SELECT rolsuper, current_setting('shared_preload_libraries', true)
	  FROM pg_roles WHERE rolname = current_user`).Scan(&super, &preload); err != nil {
		t.Fatal(err)
	}
	if !super || !strings.Contains(preload.String, "pg_stat_statements") {
		t.Skipf("needs a superuser on a server that preloads pg_stat_statements (superuser %v, preload %q)", super, preload.String)
	}
	cleanup := func() { server.Exec(`DROP DATABASE IF EXISTS ` + historyDB + ` WITH (FORCE)`) }
	cleanup()
	t.Cleanup(cleanup)
	opsMustExec(t, server, `CREATE DATABASE `+historyDB)
	db := opsOpen(t, DriverPostgres, DSNForDatabase(DriverPostgres, dsn, historyDB), "the test's own database")
	opsMustExec(t, db, `CREATE EXTENSION pg_stat_statements`,
		`CREATE TABLE jdt_query_insights_t (id int PRIMARY KEY, v text)`,
		`INSERT INTO jdt_query_insights_t SELECT n, 'row ' || n FROM generate_series(1, 100) n`)
	lookup := func(times int) {
		for i := range times {
			var v string
			if err := db.QueryRowContext(ctx, `SELECT v FROM jdt_query_insights_t WHERE id = $1`, i+1).Scan(&v); err != nil {
				t.Fatal(err)
			}
		}
	}

	lookup(5)
	first, err := TakeStatementSnapshot(ctx, db, DriverPostgres, nil)
	if err != nil || !first.Supported || first.Window || first.RowsRead || first.Durations {
		t.Fatalf("first snapshot %+v: %v", first, err)
	}
	lookup(7)
	// New since the first snapshot: its totals are its first interval's,
	// which the history cannot tell from totals that ran up before it.
	if _, err := db.ExecContext(ctx, `SELECT count(*) FROM jdt_query_insights_t WHERE v LIKE 'row 9%'`); err != nil {
		t.Fatal(err)
	}
	second, err := TakeStatementSnapshot(ctx, db, DriverPostgres, first)
	if err != nil {
		t.Fatal(err)
	}
	in := StatementsBetween(first, second, false)
	id, run := historyRun(t, second, in, "FROM jdt_query_insights_t WHERE id = $1")
	if run == nil {
		t.Fatalf("the lookup is not in the interval (id %q): %+v", id, in.Statements)
	}
	if in.Reset {
		t.Skip("the fixture's statistics were reset between the two snapshots")
	}
	if run.Calls != 7 || run.Rows != 7 || run.TotalMs <= 0 || run.RowsRead != -1 || run.Durations != nil {
		t.Errorf("the lookup's interval: %+v", run.StatementFigures)
	}
	if _, scan := historyRun(t, second, in, "v LIKE"); scan != nil {
		t.Errorf("a statement new to the list was given its first totals: %+v", scan)
	}
	if in.TotalMs < run.TotalMs {
		t.Errorf("the interval's whole %v is less than one statement's %v", in.TotalMs, run.TotalMs)
	}
	sameTextsAsTheList(t, ctx, db, DriverPostgres, second)

	// This database's entries dropped and made again: counted from zero.
	opsMustExec(t, db, `SELECT pg_stat_statements_reset(0, (SELECT oid FROM pg_database WHERE datname = current_database()), 0)`)
	lookup(2)
	third, err := TakeStatementSnapshot(ctx, db, DriverPostgres, second)
	if err != nil {
		t.Fatal(err)
	}
	in = StatementsBetween(second, third, false)
	if _, run := historyRun(t, third, in, "FROM jdt_query_insights_t WHERE id = $1"); run == nil || run.Calls != 2 {
		t.Errorf("after its entry was made again the lookup ran %+v", run)
	}
}

// MySQL 8 keeps a histogram of each digest's run times, MariaDB does not;
// both count the rows a statement examined.
func TestLiveMySQLStatementHistory(t *testing.T) {
	for _, c := range []struct {
		env       string
		durations bool
	}{
		{"JD_TEST_MYSQL8_ADMIN_DSN", true},
		{"JD_TEST_MYSQL_ADMIN_DSN", false},
	} {
		t.Run(c.env, func(t *testing.T) {
			server, dsn := opsLive(t, DriverMySQL, c.env)
			ctx := t.Context()
			cleanup := func() { server.Exec(`DROP DATABASE IF EXISTS ` + historyDB) }
			cleanup()
			t.Cleanup(cleanup)
			opsMustExec(t, server, `CREATE DATABASE `+historyDB)
			db := opsOpen(t, DriverMySQL, DSNForDatabase(DriverMySQL, dsn, historyDB), "the test's own database")
			opsMustExec(t, db, `CREATE TABLE jdt_query_insights_t (id INT PRIMARY KEY, v VARCHAR(20))`)
			for n := 1; n <= 100; n++ {
				opsMustExec(t, db, fmt.Sprintf(`INSERT INTO jdt_query_insights_t VALUES (%d, 'row %d')`, n, n))
			}
			scan := func(times int) {
				for range times {
					// Sent as text, so performance_schema has the statement and not
					// a prepared one's execution.
					var n int
					if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jdt_query_insights_t WHERE v LIKE '%7%'`).Scan(&n); err != nil {
						t.Fatal(err)
					}
				}
			}
			scan(1)
			for attempt := 0; ; attempt++ {
				first, err := TakeStatementSnapshot(ctx, db, DriverMySQL, nil)
				if err != nil {
					t.Fatal(err)
				}
				if !first.Supported {
					t.Skipf("no statement statistics here: %s", first.Reason)
				}
				if !first.RowsRead || first.Durations != c.durations {
					t.Fatalf("rows read %v, durations %v", first.RowsRead, first.Durations)
				}
				scan(3)
				second, err := TakeStatementSnapshot(ctx, db, DriverMySQL, first)
				if err != nil {
					t.Fatal(err)
				}
				in := StatementsBetween(first, second, false)
				_, run := historyRun(t, second, in, "jdt_query_insights_t", "LIKE ?")
				if run == nil || run.Calls != 3 {
					if attempt < 2 {
						continue // the digest table was emptied under the test
					}
					t.Fatalf("the scan's interval: %+v of %+v", run, in.Statements)
				}
				if run.Rows != 3 || run.RowsRead < 300 || run.TotalMs <= 0 {
					t.Errorf("the scan's interval: %+v", run.StatementFigures)
				}
				var timed int64
				for _, n := range run.Durations {
					timed += n
				}
				if c.durations {
					if _, ok := run.Durations.Quantile(0.95); timed != 3 || !ok {
						t.Errorf("the histogram counts %d runs of 3: %v", timed, run.Durations)
					}
				} else if run.Durations != nil {
					t.Errorf("durations where the engine keeps none: %v", run.Durations)
				}
				sameTextsAsTheList(t, ctx, db, DriverMySQL, second)
				return
			}
		})
	}
}

// ClickHouse's snapshot is a window of the query log: the runs that finished
// between two snapshots, each counted once.
func TestLiveClickHouseStatementHistory(t *testing.T) {
	server, dsn := opsLive(t, DriverClickHouse, "JD_TEST_CLICKHOUSE_DSN")
	ctx := t.Context()
	cleanup := func() { server.Exec(`DROP DATABASE IF EXISTS ` + historyDB) }
	cleanup()
	t.Cleanup(cleanup)
	opsMustExec(t, server, `CREATE DATABASE `+historyDB)
	// The connection's own database is the scope of the query log's runs.
	db := opsOpen(t, DriverClickHouse, urlDSNWithPath(dsn, historyDB), "the test's own database")
	opsMustExec(t, db, `CREATE TABLE jdt_query_insights_t (id UInt32, v String) ENGINE = MergeTree ORDER BY id`,
		`INSERT INTO jdt_query_insights_t SELECT number, concat('row ', toString(number)) FROM numbers(1000)`)

	first, err := TakeStatementSnapshot(ctx, db, DriverClickHouse, nil)
	if err != nil || !first.Supported || !first.Window || first.Through.IsZero() || len(first.Figures) != 0 {
		t.Fatalf("first snapshot %+v: %v", first, err)
	}
	// Past the window the first snapshot ends at, by the server's clock.
	time.Sleep(clickhouseFlushLag + time.Second)
	for range 5 {
		var n uint64
		if err := db.QueryRowContext(ctx, `SELECT count() FROM jdt_query_insights_t WHERE v LIKE '%7%'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
	}
	opsMustExec(t, db, `SYSTEM FLUSH LOGS`)
	// And the second ends past the runs.
	time.Sleep(clickhouseFlushLag + time.Second)
	second, err := TakeStatementSnapshot(ctx, db, DriverClickHouse, first)
	if err != nil {
		t.Fatal(err)
	}
	in := StatementsBetween(first, second, false)
	id, run := historyRun(t, second, in, "FROM jdt_query_insights_t WHERE v LIKE ?")
	if run == nil {
		t.Fatalf("the scan is not in the window (id %q): %+v", id, in.Statements)
	}
	if run.Calls != 5 || run.Rows != 5 || run.RowsRead < 5000 || !second.RowsRead || !second.Durations {
		t.Errorf("the scan's window: %+v", run.StatementFigures)
	}
	var timed int64
	for edge, n := range run.Durations {
		if edge < 1 {
			t.Errorf("a bucket edge under a millisecond: %v", run.Durations)
		}
		timed += n
	}
	if p, ok := run.Durations.Quantile(0.99); timed != 5 || !ok || p < 1 {
		t.Errorf("the durations count %d runs of 5, p99 %v: %v", timed, p, run.Durations)
	}
	if in.TotalMs < run.TotalMs {
		t.Errorf("the window's whole %v is less than one statement's %v", in.TotalMs, run.TotalMs)
	}
	sameTextsAsTheList(t, ctx, db, DriverClickHouse, second)

	// The next window starts where this one ended: the runs are not counted twice.
	third, err := TakeStatementSnapshot(ctx, db, DriverClickHouse, second)
	if err != nil {
		t.Fatal(err)
	}
	if f, ok := third.Figures[id]; ok {
		t.Errorf("the next window counted the scan again: %+v", f)
	}
}

// SQL Server and Oracle keep running totals beside each cached plan or
// cursor, read through the statement list alone.
func TestLiveMSSQLStatementHistory(t *testing.T) {
	server, dsn := opsLive(t, DriverMSSQL, "JD_TEST_MSSQL_DSN")
	ctx := t.Context()
	cleanup := func() {
		server.Exec(`IF DB_ID('` + historyDB + `') IS NOT NULL BEGIN ALTER DATABASE ` + historyDB +
			` SET SINGLE_USER WITH ROLLBACK IMMEDIATE; DROP DATABASE ` + historyDB + ` END`)
	}
	cleanup()
	t.Cleanup(cleanup)
	opsMustExec(t, server, `CREATE DATABASE `+historyDB)
	db := opsOpen(t, DriverMSSQL, DSNForDatabase(DriverMSSQL, dsn, historyDB), "the test's own database")
	opsMustExec(t, db, `CREATE TABLE jdt_query_insights_t (id INT PRIMARY KEY, v NVARCHAR(20))`,
		`INSERT INTO jdt_query_insights_t SELECT TOP 100 ROW_NUMBER() OVER (ORDER BY (SELECT 1)), N'row' FROM sys.objects`)
	scan := func(times int) {
		for range times {
			var n int
			if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jdt_query_insights_t WHERE v LIKE N'%ow%'`).Scan(&n); err != nil {
				t.Fatal(err)
			}
		}
	}
	scan(2)
	first, err := TakeStatementSnapshot(ctx, db, DriverMSSQL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Supported {
		t.Skipf("no statement statistics here: %s", first.Reason)
	}
	scan(4)
	second, err := TakeStatementSnapshot(ctx, db, DriverMSSQL, first)
	if err != nil {
		t.Fatal(err)
	}
	in := StatementsBetween(first, second, false)
	_, run := historyRun(t, second, in, "jdt_query_insights_t WHERE v LIKE")
	if run == nil || run.Calls != 4 || run.Rows != 4 {
		t.Fatalf("the scan's interval: %+v of %+v", run, in.Statements)
	}
	if text := second.Texts[run.ID]; strings.Contains(text, "%ow%") {
		t.Errorf("a literal reached the history: %q", text)
	}
	sameTextsAsTheList(t, ctx, db, DriverMSSQL, second)
}

func TestLiveOracleStatementHistory(t *testing.T) {
	app, _ := opsLive(t, DriverOracle, "JD_TEST_ORACLE_DSN")
	admin, _ := opsLive(t, DriverOracle, "JD_TEST_ORACLE_ADMIN_DSN")
	ctx := t.Context()
	app.Exec(`DROP TABLE jdt_query_insights_t PURGE`)
	t.Cleanup(func() { app.Exec(`DROP TABLE jdt_query_insights_t PURGE`) })
	opsMustExec(t, app, `CREATE TABLE jdt_query_insights_t (id NUMBER PRIMARY KEY, v VARCHAR2(20))`,
		`INSERT INTO jdt_query_insights_t SELECT LEVEL, 'row ' || LEVEL FROM dual CONNECT BY LEVEL <= 2000`)
	scan := func(times int) {
		for range times {
			var n int
			if err := app.QueryRowContext(ctx, `SELECT COUNT(*) FROM jdt_query_insights_t WHERE v LIKE '%77%'`).Scan(&n); err != nil {
				t.Fatal(err)
			}
		}
	}
	scan(2)
	first, err := TakeStatementSnapshot(ctx, admin, DriverOracle, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Supported {
		t.Skipf("no statement statistics here: %s", first.Reason)
	}
	scan(3)
	second, err := TakeStatementSnapshot(ctx, admin, DriverOracle, first)
	if err != nil {
		t.Fatal(err)
	}
	in := StatementsBetween(first, second, false)
	id, run := historyRun(t, second, in, "jdt_query_insights_t WHERE v LIKE ?")
	if id == "" {
		t.Skip("the scan is not among the shared pool's two hundred costliest statements")
	}
	if run == nil || run.Calls != 3 {
		t.Fatalf("the scan's interval: %+v", run)
	}
	sameTextsAsTheList(t, ctx, admin, DriverOracle, second)
}
