package dbx

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The what-if needs the hypopg extension, which the shared PostgreSQL fixture
// does not carry, so the part that plans with a hypothetical index runs against
// JD_TEST_POSTGRES_HYPOPG_DSN (postgres:17 plus postgresql-17-hypopg, with
// `CREATE EXTENSION hypopg` run in the database). The candidates and the
// statement that is said not to be possible run on either.

const adviceOrders = `SELECT o.id FROM jdt_index_advice_orders o
	JOIN jdt_index_advice_customers c ON c.id = o.customer_id
	WHERE o.status = 'open' AND o.total > 100 AND c.region = 'eu'
	ORDER BY o.created_at DESC LIMIT 20`

func adviceFixture(t *testing.T, env string) (context.Context, func(string) int) {
	t.Helper()
	db := catalogLive(t, DriverPostgres, env)
	ctx := context.Background()
	cleanup := []string{`DROP TABLE IF EXISTS jdt_index_advice_orders`, `DROP TABLE IF EXISTS jdt_index_advice_customers`}
	catalogExec(t, db, cleanup...)
	t.Cleanup(func() {
		for _, s := range cleanup {
			_, _ = db.ExecContext(ctx, s)
		}
	})
	catalogExec(t, db,
		`CREATE TABLE jdt_index_advice_customers (id serial PRIMARY KEY, region text NOT NULL, name text NOT NULL)`,
		`CREATE TABLE jdt_index_advice_orders (id serial PRIMARY KEY, customer_id int NOT NULL, status text NOT NULL,
			total numeric NOT NULL, created_at timestamptz NOT NULL)`,
		`INSERT INTO jdt_index_advice_customers SELECT g, (ARRAY['eu','us','apac'])[1 + g % 3], 'c' || g FROM generate_series(1, 2000) g`,
		`INSERT INTO jdt_index_advice_orders SELECT g, 1 + g % 2000, (ARRAY['open','paid','shipped','void'])[1 + g % 4], g % 500,
			now() - (g || ' minutes')::interval FROM generate_series(1, 60000) g`,
		`ANALYZE jdt_index_advice_customers`, `ANALYZE jdt_index_advice_orders`,
	)
	indexes := func(table string) int {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_indexes WHERE tablename = $1`, table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	t.Cleanup(func() { db.Close() })
	return ctx, indexes
}

func TestLiveIndexCandidatesNameTheColumnsAPlanFiltersAndJoinsOn(t *testing.T) {
	const env = "JD_TEST_POSTGRES_DSN"
	ctx, _ := adviceFixture(t, env)
	db := liveSQL(t, DriverPostgres, env, "")
	st, err := ExplainTarget(DriverPostgres, adviceOrders)
	if err != nil {
		t.Fatal(err)
	}
	advice, err := IndexCandidates(ctx, db, DriverPostgres, st)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]IndexCandidate{}
	for _, c := range advice.Candidates {
		found[c.Table+"("+strings.Join(c.Columns, ",")+")"] = c
	}
	for _, want := range []string{
		"jdt_index_advice_orders(status,total)",
		"jdt_index_advice_orders(customer_id)",
		"jdt_index_advice_customers(region)",
	} {
		if _, ok := found[want]; !ok {
			t.Errorf("no candidate %s in %v", want, found)
		}
	}
	// Both tables' primary keys already lead with id.
	if _, ok := found["jdt_index_advice_customers(id)"]; ok {
		t.Error("an index was proposed on a primary key")
	}
	if advice.Covered == 0 {
		t.Error("the primary keys that already lead with id were not counted as covered")
	}
	if advice.HypoPG.State != HypoPGNotInstalled && advice.HypoPG.State != HypoPGReady {
		t.Errorf("hypopg = %+v", advice.HypoPG)
	}

	// A range column has to sit where the candidate puts it: an index on the
	// same columns the other way round does not serve it, and is not mistaken for one.
	proposed := func() bool {
		advice, err := IndexCandidates(ctx, db, DriverPostgres, st)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range advice.Candidates {
			if c.Table == "jdt_index_advice_orders" && strings.Join(c.Columns, ",") == "status,total" {
				return true
			}
		}
		return false
	}
	if _, err := db.ExecContext(ctx, `CREATE INDEX ON jdt_index_advice_orders (total, status)`); err != nil {
		t.Fatal(err)
	}
	if !proposed() {
		t.Error("an index on the columns in the other order made the candidate disappear")
	}
	// Once an index leads with the columns, it is not proposed again.
	if _, err := db.ExecContext(ctx, `CREATE INDEX ON jdt_index_advice_orders (status, total, id)`); err != nil {
		t.Fatal(err)
	}
	if proposed() {
		t.Error("a candidate was proposed although an index already leads with its columns")
	}

	// A partition is not proposed for, and the answer says it left one out.
	catalogExec(t, db,
		`CREATE TABLE jdt_index_advice_events (id int, kind text) PARTITION BY LIST (kind)`,
		`CREATE TABLE jdt_index_advice_events_a PARTITION OF jdt_index_advice_events FOR VALUES IN ('a')`,
		`INSERT INTO jdt_index_advice_events SELECT g, 'a' FROM generate_series(1, 20000) g`,
		`ANALYZE jdt_index_advice_events`,
	)
	t.Cleanup(func() { _, _ = db.ExecContext(ctx, `DROP TABLE IF EXISTS jdt_index_advice_events`) })
	events, err := ExplainTarget(DriverPostgres, `SELECT id FROM jdt_index_advice_events WHERE kind = 'a' AND id = 5`)
	if err != nil {
		t.Fatal(err)
	}
	partitioned, err := IndexCandidates(ctx, db, DriverPostgres, events)
	if err != nil {
		t.Fatal(err)
	}
	if len(partitioned.Candidates) != 0 || partitioned.Skipped == 0 {
		t.Errorf("a partitioned table: %+v", partitioned)
	}

	if _, err := IndexCandidates(ctx, db, DriverMySQL, st); !errors.Is(err, ErrNoWhatIf) {
		t.Errorf("another engine: %v", err)
	}
}

func TestLiveHypotheticalIndexChangesThePlanAndNothingElse(t *testing.T) {
	const env = "JD_TEST_POSTGRES_HYPOPG_DSN"
	ctx, indexes := adviceFixture(t, env)
	db := liveSQL(t, DriverPostgres, env, "")
	st, err := ExplainTarget(DriverPostgres, adviceOrders)
	if err != nil {
		t.Fatal(err)
	}
	advice, err := IndexCandidates(ctx, db, DriverPostgres, st)
	if err != nil {
		t.Fatal(err)
	}
	if advice.HypoPG.State != HypoPGReady || advice.HypoPG.Version == "" {
		t.Fatalf("hypopg = %+v; run CREATE EXTENSION hypopg in the %s database", advice.HypoPG, env)
	}
	// One connection, so a session that was handed back to the pool with its
	// hypothetical indexes would be the one the check below reads.
	db.SetMaxOpenConns(1)
	held := indexes("jdt_index_advice_orders")

	got, err := TryIndex(ctx, db, DriverPostgres, st, WhatIfIndex{
		Schema: "public", Table: "jdt_index_advice_orders", Columns: []string{"status", "total"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Used || got.AfterCost >= got.BeforeCost || got.BeforeCost <= 0 {
		t.Errorf("the hypothetical index did not help: used=%v before=%v after=%v", got.Used, got.BeforeCost, got.AfterCost)
	}
	if !strings.HasPrefix(got.Statement, "CREATE INDEX ") || !strings.Contains(got.IndexName, "jdt_index_advice_orders") {
		t.Errorf("statement %q, index %q", got.Statement, got.IndexName)
	}
	if after := indexes("jdt_index_advice_orders"); after != held {
		t.Errorf("the table has %d indexes, had %d", after, held)
	}
	// pg_indexes never lists a hypothetical index; HypoPG does.
	var hypothetical int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM hypopg_list_indexes`).Scan(&hypothetical); err != nil || hypothetical != 0 {
		t.Errorf("hypothetical indexes left on the connection: %d, %v", hypothetical, err)
	}

	// An index nothing in the statement uses is reported as not used, with
	// the plan it left unchanged.
	idle, err := TryIndex(ctx, db, DriverPostgres, st, WhatIfIndex{
		Schema: "public", Table: "jdt_index_advice_customers", Columns: []string{"name"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if idle.Used || idle.AfterCost != idle.BeforeCost {
		t.Errorf("an unrelated index changed the plan: %+v", idle)
	}

	// A statement that writes is planned, not run.
	del, err := ExplainTarget(DriverPostgres, `DELETE FROM jdt_index_advice_orders WHERE status = 'open'`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TryIndex(ctx, db, DriverPostgres, del, WhatIfIndex{
		Schema: "public", Table: "jdt_index_advice_orders", Columns: []string{"status"},
	}); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM jdt_index_advice_orders`).Scan(&rows); err != nil || rows != 60000 {
		t.Errorf("rows after a what-if of a DELETE = %d, %v", rows, err)
	}

	// The engine's refusals reach the caller in its words.
	if _, err := TryIndex(ctx, db, DriverPostgres, st, WhatIfIndex{
		Schema: "public", Table: "jdt_index_advice_orders", Columns: []string{"nope"},
	}); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("a column that is not there: %v", err)
	}
	if _, err := TryIndex(ctx, db, DriverPostgres, st, WhatIfIndex{Table: "jdt_index_advice_orders"}); err == nil {
		t.Error("an index of no columns was tried")
	}
	if _, err := TryIndex(ctx, db, DriverMySQL, st, WhatIfIndex{Table: "t", Columns: []string{"a"}}); !errors.Is(err, ErrNoWhatIf) {
		t.Errorf("another engine: %v", err)
	}
}

func TestLiveWhatIfSaysWhenHypoPGIsNotThere(t *testing.T) {
	const env = "JD_TEST_POSTGRES_DSN"
	ctx, _ := adviceFixture(t, env)
	db := liveSQL(t, DriverPostgres, env, "")
	state, err := hypopgState(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if state.State == HypoPGReady {
		t.Skip("this server has hypopg")
	}
	st, err := ExplainTarget(DriverPostgres, adviceOrders)
	if err != nil {
		t.Fatal(err)
	}
	_, err = TryIndex(ctx, db, DriverPostgres, st, WhatIfIndex{
		Schema: "public", Table: "jdt_index_advice_orders", Columns: []string{"status"},
	})
	var missing ErrHypoPGUnavailable
	if !errors.As(err, &missing) || !strings.Contains(err.Error(), "hypopg") {
		t.Errorf("TryIndex without the extension: %v", err)
	}
}

func TestLiveSQLServerListsTheIndexesItsPlansWantedAndTheStatementsWork(t *testing.T) {
	db := sqlServerLive(t)
	ctx := context.Background()
	drop := `DROP TABLE IF EXISTS dbo.jdt_index_advice_orders`
	catalogExec(t, db, drop)
	t.Cleanup(func() { _, _ = db.ExecContext(ctx, drop) })
	catalogExec(t, db,
		`CREATE TABLE dbo.jdt_index_advice_orders (id int IDENTITY PRIMARY KEY, customer_id int NOT NULL,
			status varchar(16) NOT NULL, total decimal(10,2) NOT NULL, note varchar(40) NULL)`,
		`INSERT INTO dbo.jdt_index_advice_orders (customer_id, status, total)
			SELECT TOP (30000) ABS(CHECKSUM(NEWID())) % 2000, 'open', ABS(CHECKSUM(NEWID())) % 500
			FROM sys.all_objects a CROSS JOIN sys.all_objects b`,
	)
	// The optimizer notes a wish when it compiles a statement it cannot serve.
	// A join keeps the plan from being a trivial one, for which it notes none.
	for i := 0; i < 3; i++ {
		rows, err := db.QueryContext(ctx, `SELECT a.id, b.total FROM dbo.jdt_index_advice_orders a
			JOIN dbo.jdt_index_advice_orders b ON b.customer_id = a.customer_id
			WHERE a.status = 'open' AND a.customer_id = 7 AND b.total > 100`)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
		}
		rows.Close()
	}

	report, err := ListMissingIndexes(ctx, db, DriverMSSQL)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Supported {
		t.Fatalf("report = %+v", report)
	}
	var ours *MissingIndex
	for i := range report.Indexes {
		entry := &report.Indexes[i]
		if entry.Table == "jdt_index_advice_orders" && len(entry.Equality) > 0 && entry.Equality[0] == "customer_id" {
			ours = entry
		}
	}
	if ours == nil {
		t.Fatalf("no entry for the table's customer_id in %+v", report.Indexes)
	}
	if ours.Schema != "dbo" || ours.Seeks+ours.Scans == 0 || ours.AvgImpact <= 0 ||
		!strings.HasPrefix(ours.Statement, "CREATE NONCLUSTERED INDEX ") {
		t.Errorf("entry = %+v", ours)
	}
	if report.Since == nil {
		t.Error("the report does not say since when the server has been counting")
	}
	// The statement is one the server accepts.
	if _, err := db.ExecContext(ctx, ours.Statement); err != nil {
		t.Errorf("%v\n%s", err, ours.Statement)
	}

	other, err := ListMissingIndexes(ctx, db, DriverPostgres)
	if err != nil || other.Supported || other.Reason == "" || other.Indexes == nil {
		t.Errorf("an engine with no such views: %+v, %v", other, err)
	}
}
