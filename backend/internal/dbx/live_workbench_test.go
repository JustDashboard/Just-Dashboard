package dbx

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// Live tests for the table editor and the query runner.
//
// The unit tests beside this file prove the statements are the ones intended
// and that SQLite accepts them. These prove the things only a server can: that
// PostgreSQL's lexer and this one agree about where a dollar-quoted body ends,
// that a cancelled statement really stops on the server, that an UPDATE which
// changes nothing is still counted as matching one row on MySQL.
//
// They never fall back to an engine's standard port. A fixture is named by its
// environment variable or the test skips.

type workbenchEngine struct {
	name   string
	driver Driver
	env    string
	// binary and json are the column types this engine spells those two as.
	binary, json string
}

func workbenchEngines() []workbenchEngine {
	return []workbenchEngine{
		{"postgres", DriverPostgres, "JD_TEST_POSTGRES_DSN", "BYTEA", "JSONB"},
		{"mariadb", DriverMySQL, "JD_TEST_MYSQL_DSN", "VARBINARY(64)", "JSON"},
		{"mysql8", DriverMySQL, "JD_TEST_MYSQL8_DSN", "VARBINARY(64)", "JSON"},
	}
}

// openWorkbench connects to an engine's fixture or skips. schema is what the
// catalogue calls the place the fixture's tables are in.
func openWorkbench(t *testing.T, e workbenchEngine) (db *sql.DB, schema string) {
	t.Helper()
	dsn := os.Getenv(e.env)
	if dsn == "" {
		t.Skipf("%s: set %s to run this", e.name, e.env)
	}
	db = liveSQL(t, e.driver, e.env, dsn)
	// Like the pool the dashboard opens, so a session thrown away by a write
	// is replaced rather than exhausting it.
	db.SetMaxOpenConns(5)
	switch e.driver {
	case DriverPostgres:
		return db, "public"
	default:
		info, err := ParseDSN(e.driver, dsn)
		if err != nil || info.Database == "" {
			t.Fatalf("%s names no database: %v", e.env, err)
		}
		return db, info.Database
	}
}

func mustExec(t *testing.T, db *sql.DB, statements ...string) {
	t.Helper()
	for _, s := range statements {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%v\n%s", err, s)
		}
	}
}

func dropAfter(t *testing.T, db *sql.DB, tables ...string) {
	t.Helper()
	drop := func() {
		for _, table := range tables {
			_, _ = db.Exec("DROP TABLE IF EXISTS " + table)
		}
	}
	drop()
	t.Cleanup(drop)
}

func TestLiveChangeSets(t *testing.T) {
	for _, e := range workbenchEngines() {
		t.Run(e.name, func(t *testing.T) {
			db, schema := openWorkbench(t, e)
			ctx := context.Background()
			dropAfter(t, db, "jdwb_people", "jdwb_notes", "jdwb_blobkey")
			auto := "id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY"
			if e.driver == DriverMySQL {
				auto = "id BIGINT AUTO_INCREMENT PRIMARY KEY"
			}
			mustExec(t, db,
				`CREATE TABLE jdwb_people (`+auto+`,
					name VARCHAR(100) NOT NULL,
					age INT DEFAULT 18,
					big BIGINT,
					price DECIMAL(30,10),
					data `+e.binary+`,
					doc `+e.json+`
				)`,
				`INSERT INTO jdwb_people(name, age) VALUES ('Ann', 31), ('Bo', 24), ('Cy', 40)`,
				`CREATE TABLE jdwb_notes (msg VARCHAR(100), n INT)`,
				`INSERT INTO jdwb_notes VALUES ('one', 1), ('two', NULL), ('dup', 7), ('dup', 7)`,
				`CREATE TABLE jdwb_blobkey (k `+e.binary+` PRIMARY KEY, v VARCHAR(20))`,
			)
			if e.driver == DriverPostgres {
				mustExec(t, db, `INSERT INTO jdwb_blobkey VALUES ('\x68656c6c6f'::bytea, 'text-like'), ('\x00ff'::bytea, 'binary')`)
			} else {
				mustExec(t, db, `INSERT INTO jdwb_blobkey VALUES (0x68656c6c6f, 'text-like'), (0x00ff, 'binary')`)
			}
			blob := []byte{0x00, 0xff, 0x10, 'a', 0x80}

			res, err := ApplyChanges(ctx, db, e.driver, ChangeSet{Schema: schema, Table: "jdwb_people", Changes: []Change{
				{Op: ChangeInsert, Values: map[string]any{
					"name":  "exact",
					"big":   json.Number("9223372036854775807"),
					"price": json.Number("12345678901234567890.1234567890"),
					"data":  map[string]any{"$base64": "AP8QYYA="},
					"doc":   map[string]any{"n": json.Number("9007199254740993"), "tags": []any{"a", "b"}},
				}},
				{Op: ChangeUpdate, Key: map[string]any{"id": json.Number("1")}, Values: map[string]any{"age": json.Number("32")}},
				// Writing back the value that is already there. MySQL reports
				// zero rows changed for this; it still matched exactly one.
				{Op: ChangeUpdate, Key: map[string]any{"id": json.Number("2")}, Values: map[string]any{"name": "Bo"}},
				{Op: ChangeDelete, Key: map[string]any{"id": json.Number("3")}},
			}})
			if err != nil {
				t.Fatalf("ApplyChanges: %v", err)
			}
			if !res.Applied || !sameStrings(res.KeyColumns, []string{"id"}) {
				t.Fatalf("result = %+v", res)
			}
			inserted := res.Results[0].Row
			if inserted == nil {
				t.Fatal("the inserted row did not come back")
			}
			// The generated key, the default and the exact values, as stored.
			if inserted["id"] != "4" || inserted["age"] != "18" || inserted["big"] != "9223372036854775807" {
				t.Errorf("inserted row = %v", inserted)
			}
			if got := fmt.Sprint(inserted["price"]); got != "12345678901234567890.1234567890" {
				t.Errorf("price came back as %s", got)
			}
			if inserted["data"] != `\x00ff106180` {
				t.Errorf("binary cell = %v, want it as hex", inserted["data"])
			}
			if row := res.Results[1].Row; row == nil || row["age"] != "32" {
				t.Errorf("updated row = %v", res.Results[1].Row)
			}
			if res.Results[2].Affected != 1 || res.Results[2].Row == nil {
				t.Errorf("an update that changed nothing = %+v", res.Results[2])
			}
			var big int64
			var price, doc string
			var data []byte
			if err := db.QueryRow(`SELECT big, CAST(price AS CHAR(60)), data, CAST(doc AS CHAR(200)) FROM jdwb_people WHERE id = 4`).
				Scan(&big, &price, &data, &doc); err != nil {
				t.Fatal(err)
			}
			// CHAR(n) is blank-padded on PostgreSQL.
			if big != 9223372036854775807 || strings.TrimSpace(price) != "12345678901234567890.1234567890" || !bytes.Equal(data, blob) {
				t.Errorf("stored big = %d price = %s data = %x", big, price, data)
			}
			if !strings.Contains(doc, "9007199254740993") {
				t.Errorf("stored doc = %s", doc)
			}

			// A conflict undoes the set, earlier changes included.
			_, err = ApplyChanges(ctx, db, e.driver, ChangeSet{Schema: schema, Table: "jdwb_people", Changes: []Change{
				{Op: ChangeInsert, Values: map[string]any{"name": "should not stay"}},
				{Op: ChangeUpdate, Key: map[string]any{"id": json.Number("999")}, Values: map[string]any{"age": json.Number("1")}},
			}})
			var change *ChangeError
			if !errors.As(err, &change) || !change.Conflict || change.Index != 1 || change.Matched != 0 {
				t.Fatalf("err = %v, want a conflict on the second change", err)
			}
			var n int
			if err := db.QueryRow(`SELECT COUNT(*) FROM jdwb_people WHERE name = 'should not stay'`).Scan(&n); err != nil || n != 0 {
				t.Errorf("an insert from a failed set stayed (%d, %v)", n, err)
			}
			// An engine's own refusal names the change too.
			_, err = ApplyChanges(ctx, db, e.driver, ChangeSet{Schema: schema, Table: "jdwb_people", Changes: []Change{
				{Op: ChangeDelete, Key: map[string]any{"id": json.Number("1")}},
				{Op: ChangeInsert, Values: map[string]any{"age": json.Number("5")}}, // name is NOT NULL
			}})
			if !errors.As(err, &change) || change.Conflict || change.Index != 1 {
				t.Fatalf("err = %v, want the engine's refusal of the second change", err)
			}
			if err := db.QueryRow(`SELECT COUNT(*) FROM jdwb_people WHERE id = 1`).Scan(&n); err != nil || n != 1 {
				t.Errorf("a delete from a failed set stayed (%d, %v)", n, err)
			}

			// No primary key: matched on the row, NULL as NULL, one row or none.
			res, err = ApplyChanges(ctx, db, e.driver, ChangeSet{Schema: schema, Table: "jdwb_notes", Changes: []Change{
				{Op: ChangeUpdate, Key: map[string]any{"msg": "two", "n": nil}, Values: map[string]any{"n": json.Number("2")}},
			}})
			if err != nil || len(res.KeyColumns) != 0 {
				t.Fatalf("keyless update: %v %+v", err, res)
			}
			if row := res.Results[0].Row; row == nil || row["n"] != "2" {
				t.Errorf("keyless updated row = %v", res.Results[0].Row)
			}
			_, err = ApplyChanges(ctx, db, e.driver, ChangeSet{Schema: schema, Table: "jdwb_notes", Changes: []Change{
				{Op: ChangeDelete, Key: map[string]any{"msg": "dup", "n": json.Number("7")}},
			}})
			if !errors.As(err, &change) || !change.Conflict || change.Matched != 2 {
				t.Fatalf("two identical rows: err = %v", err)
			}
			if err := db.QueryRow(`SELECT COUNT(*) FROM jdwb_notes WHERE msg = 'dup'`).Scan(&n); err != nil || n != 2 {
				t.Errorf("duplicate rows left = %d (%v), want both", n, err)
			}

			// A binary key, sent back in the form the grid shows it — including
			// one whose bytes happen to spell a word.
			page, err := Browse(ctx, db, e.driver, BrowseOptions{Schema: schema, Table: "jdwb_blobkey", OrderBy: "v"})
			if err != nil {
				t.Fatal(err)
			}
			at := indexOf(page.Columns, "k")
			if page.Rows[0][at] != `\x00ff` || page.Rows[1][at] != `\x68656c6c6f` || page.Kinds[at] != KindBinary {
				t.Fatalf("binary keys browse as %v (%v)", page.Rows, page.Kinds)
			}
			for _, row := range page.Rows {
				if _, err := ApplyChanges(ctx, db, e.driver, ChangeSet{Schema: schema, Table: "jdwb_blobkey", Changes: []Change{
					{Op: ChangeUpdate, Key: map[string]any{"k": row[at]}, Values: map[string]any{"v": "edited"}},
				}}); err != nil {
					t.Errorf("update by binary key %v: %v", row[at], err)
				}
			}
			if err := db.QueryRow(`SELECT COUNT(*) FROM jdwb_blobkey WHERE v = 'edited'`).Scan(&n); err != nil || n != 2 {
				t.Errorf("rows edited by a binary key = %d (%v), want 2", n, err)
			}
		})
	}
}

// PostgreSQL defines no equality for json, so a keyless table with a json
// column is matched through its text form.
func TestLivePostgresKeylessJSON(t *testing.T) {
	db, _ := openWorkbench(t, workbenchEngines()[0])
	dropAfter(t, db, "jdwb_events")
	mustExec(t, db,
		`CREATE TABLE jdwb_events (kind TEXT, payload JSON)`,
		`INSERT INTO jdwb_events VALUES ('a', '{"k": 1}'), ('a', '{"k": 2}')`)
	page, err := Browse(context.Background(), db, DriverPostgres, BrowseOptions{Schema: "public", Table: "jdwb_events"})
	if err != nil {
		t.Fatal(err)
	}
	key := map[string]any{}
	for i, c := range page.Columns {
		key[c] = page.Rows[0][i]
	}
	if _, err := ApplyChanges(context.Background(), db, DriverPostgres, ChangeSet{
		Schema: "public", Table: "jdwb_events", Changes: []Change{{Op: ChangeDelete, Key: key}},
	}); err != nil {
		t.Fatalf("delete by a row holding json: %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM jdwb_events`).Scan(&n); err != nil || n != 1 {
		t.Errorf("rows left = %d (%v), want 1", n, err)
	}
}

// MySQL compares a JSON value with a string as different types. A keyless row
// is matched through the document's text instead.
func TestLiveMySQLKeylessJSON(t *testing.T) {
	for _, e := range workbenchEngines()[1:] {
		t.Run(e.name, func(t *testing.T) {
			db, schema := openWorkbench(t, e)
			dropAfter(t, db, "jdwb_events")
			mustExec(t, db,
				`CREATE TABLE jdwb_events (kind VARCHAR(10), payload JSON)`,
				`INSERT INTO jdwb_events VALUES ('a', '{"k": 1}'), ('a', '{"k": 2}')`)
			page, err := Browse(context.Background(), db, DriverMySQL, BrowseOptions{Schema: schema, Table: "jdwb_events"})
			if err != nil {
				t.Fatal(err)
			}
			key := map[string]any{}
			for i, c := range page.Columns {
				key[c] = page.Rows[0][i]
			}
			if _, err := ApplyChanges(context.Background(), db, DriverMySQL, ChangeSet{
				Schema: schema, Table: "jdwb_events", Changes: []Change{{Op: ChangeDelete, Key: key}},
			}); err != nil {
				t.Fatalf("delete by a row holding json (%v): %v", key, err)
			}
			var n int
			if err := db.QueryRow(`SELECT COUNT(*) FROM jdwb_events`).Scan(&n); err != nil || n != 1 {
				t.Errorf("rows left = %d (%v), want 1", n, err)
			}
		})
	}
}

// A serialization failure is the engine asking for the transaction to be run
// again. It is, a bounded number of times.
func TestLiveChangeSetRetriesSerializationFailures(t *testing.T) {
	db, _ := openWorkbench(t, workbenchEngines()[0])
	ctx := context.Background()
	dropAfter(t, db, "jdwb_retry")
	mustExec(t, db,
		`DROP SEQUENCE IF EXISTS jdwb_retry_attempts`,
		`CREATE SEQUENCE jdwb_retry_attempts`,
		`CREATE TABLE jdwb_retry (id INT PRIMARY KEY, fail_until INT NOT NULL)`,
		// A sequence is not rolled back, so it counts attempts across retries.
		`CREATE OR REPLACE FUNCTION jdwb_retry_fail() RETURNS trigger AS $f$
		 BEGIN
		   IF nextval('jdwb_retry_attempts') <= NEW.fail_until THEN
		     RAISE EXCEPTION 'could not serialize access' USING ERRCODE = '40001';
		   END IF;
		   RETURN NEW;
		 END $f$ LANGUAGE plpgsql`,
		`CREATE TRIGGER jdwb_retry_before BEFORE INSERT ON jdwb_retry
		 FOR EACH ROW EXECUTE FUNCTION jdwb_retry_fail()`)
	t.Cleanup(func() {
		_, _ = db.Exec(`DROP FUNCTION IF EXISTS jdwb_retry_fail() CASCADE`)
		_, _ = db.Exec(`DROP SEQUENCE IF EXISTS jdwb_retry_attempts`)
	})

	res, err := ApplyChanges(ctx, db, DriverPostgres, ChangeSet{Schema: "public", Table: "jdwb_retry", Changes: []Change{
		{Op: ChangeInsert, Values: map[string]any{"id": json.Number("1"), "fail_until": json.Number("2")}},
	}})
	if err != nil {
		t.Fatalf("two serialization failures were not retried through: %v", err)
	}
	if res.Attempts != 3 || !res.Applied {
		t.Errorf("attempts = %d applied = %v, want 3 and true", res.Attempts, res.Applied)
	}
	// Past the bound it is the caller's to retry, and it is told so.
	_, err = ApplyChanges(ctx, db, DriverPostgres, ChangeSet{Schema: "public", Table: "jdwb_retry", Changes: []Change{
		{Op: ChangeInsert, Values: map[string]any{"id": json.Number("2"), "fail_until": json.Number("1000")}},
	}})
	if !IsSerializationFailure(err) {
		t.Fatalf("err = %v, want the serialization failure after the last attempt", err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM jdwb_retry`).Scan(&n); err != nil || n != 1 {
		t.Errorf("rows = %d (%v), want only the one that was retried through", n, err)
	}
}

func TestLiveChangesAreRefusedOnClickHouse(t *testing.T) {
	dsn := os.Getenv("JD_TEST_CLICKHOUSE_DSN")
	if dsn == "" {
		t.Skip("set JD_TEST_CLICKHOUSE_DSN to run this")
	}
	db := liveSQL(t, DriverClickHouse, "JD_TEST_CLICKHOUSE_DSN", dsn)
	info, _ := ParseDSN(DriverClickHouse, dsn)
	dropAfter(t, db, "jdwb_events")
	mustExec(t, db,
		`CREATE TABLE jdwb_events (id Int32, label String) ENGINE = MergeTree ORDER BY id`,
		`INSERT INTO jdwb_events VALUES (1, 'a'), (1, 'b'), (2, 'c')`)
	_, err := ApplyChanges(context.Background(), db, DriverClickHouse, ChangeSet{
		Schema: info.Database, Table: "jdwb_events",
		Changes: []Change{{Op: ChangeDelete, Key: map[string]any{"id": json.Number("1")}}},
	})
	if !errors.Is(err, ErrChangesUnsupported) {
		t.Fatalf("err = %v, want the refusal", err)
	}
	var n uint64
	if err := db.QueryRow(`SELECT count() FROM jdwb_events`).Scan(&n); err != nil || n != 3 {
		t.Errorf("rows = %d (%v): a delete by the sorting key ran", n, err)
	}
	// A cell can still be read, and a key that names two rows is said to.
	if _, err := ReadCell(context.Background(), db, DriverClickHouse, info.Database, "jdwb_events", "label",
		map[string]any{"id": json.Number("1")}); !errors.Is(err, ErrRowAmbiguous) {
		t.Errorf("a sorting key shared by two rows = %v", err)
	}
	cell, err := ReadCell(context.Background(), db, DriverClickHouse, info.Database, "jdwb_events", "label",
		map[string]any{"id": json.Number("2")})
	if err != nil || cell.Value != "c" {
		t.Errorf("cell = %+v %v", cell, err)
	}
}

func liveBrowseEngines() []workbenchEngine {
	return append(workbenchEngines(), workbenchEngine{name: "clickhouse", driver: DriverClickHouse, env: "JD_TEST_CLICKHOUSE_DSN"})
}

func TestLiveBrowseOperators(t *testing.T) {
	for _, e := range liveBrowseEngines() {
		t.Run(e.name, func(t *testing.T) {
			db, schema := openWorkbench(t, e)
			ctx := context.Background()
			dropAfter(t, db, "jdwb_items")
			if e.driver == DriverClickHouse {
				mustExec(t, db, `CREATE TABLE jdwb_items (id Int32, label String, price Float64, grp Nullable(String))
					ENGINE = MergeTree ORDER BY id`)
			} else {
				mustExec(t, db, `CREATE TABLE jdwb_items (id INT PRIMARY KEY, label VARCHAR(100), price DOUBLE PRECISION, grp VARCHAR(10))`)
			}
			mustExec(t, db, `INSERT INTO jdwb_items VALUES
				(1, '50% off', 5.0, 'a'), (2, '50 percent', 7.5, 'a'), (3, 'under_score', 1.0, 'b'),
				(4, 'underXscore', 2.0, 'b'), (5, 'MiXeD Case', 9.0, NULL), (6, 'tail!', 3.0, 'c')`)

			ids := func(opts BrowseOptions) string {
				t.Helper()
				opts.Schema, opts.Table = schema, "jdwb_items"
				res, err := Browse(ctx, db, e.driver, opts)
				if err != nil {
					t.Fatalf("Browse(%+v): %v", opts, err)
				}
				at := indexOf(res.Columns, "id")
				var out []string
				for _, row := range res.Rows {
					out = append(out, fmt.Sprint(row[at]))
				}
				return strings.Join(out, ",")
			}
			byID := []SortKey{{Column: "id"}}
			for _, c := range []struct {
				name   string
				filter Filter
				want   string
			}{
				{"contains a percent sign", Filter{Column: "label", Op: "contains", Value: "50%"}, "1"},
				{"contains an underscore", Filter{Column: "label", Op: "contains", Value: "r_s"}, "3"},
				{"suffix with the escape character", Filter{Column: "label", Op: "suffix", Value: "l!"}, "6"},
				{"prefix", Filter{Column: "label", Op: "prefix", Value: "50"}, "1,2"},
				{"icontains", Filter{Column: "label", Op: "icontains", Value: "mixed CASE"}, "5"},
				{"not_contains", Filter{Column: "label", Op: "not_contains", Value: "score"}, "1,2,5,6"},
				{"in", Filter{Column: "id", Op: "in", Values: []string{"2", "4", "9"}}, "2,4"},
				{"not_in", Filter{Column: "id", Op: "not_in", Values: []string{"1", "2", "3", "4"}}, "5,6"},
				{"between", Filter{Column: "price", Op: "between", Values: []string{"2", "5"}}, "1,4,6"},
				{"regex", Filter{Column: "label", Op: "regex", Value: "^under.score$"}, "3,4"},
				{"contains on a number", Filter{Column: "id", Op: "contains", Value: "5"}, "5"},
				{"is_null", Filter{Column: "grp", Op: "is_null"}, "5"},
			} {
				if got := ids(BrowseOptions{Filters: []Filter{c.filter}, Sort: byID}); got != c.want {
					t.Errorf("%s: ids = %q, want %q", c.name, got, c.want)
				}
			}
			if got := ids(BrowseOptions{Sort: []SortKey{{Column: "grp", Desc: true}, {Column: "price", Desc: true}}, Filters: []Filter{
				{Column: "grp", Op: "not_null"},
			}}); got != "6,4,3,2,1" {
				t.Errorf("multi-column sort = %q", got)
			}
			if got := ids(BrowseOptions{MatchAny: true, Sort: byID, Filters: []Filter{
				{Column: "grp", Op: "eq", Value: "c"}, {Column: "price", Op: "between", Values: []string{"7", "8"}},
			}}); got != "2,6" {
				t.Errorf("match any = %q", got)
			}
			n, err := Count(ctx, db, e.driver, BrowseOptions{Schema: schema, Table: "jdwb_items", Filters: []Filter{
				{Column: "label", Op: "icontains", Value: "UNDER"},
			}})
			if err != nil || n != 2 {
				t.Errorf("count = %d %v", n, err)
			}

			// The page: key, order, whether more follow, and the engine's own
			// idea of how many rows the table has.
			switch e.driver {
			case DriverPostgres:
				mustExec(t, db, `ANALYZE jdwb_items`)
			case DriverMySQL:
				mustExec(t, db, `ANALYZE TABLE jdwb_items`)
			}
			page, err := BrowseTablePage(ctx, db, e.driver, BrowseOptions{
				Schema: schema, Table: "jdwb_items", Limit: 4, Columns: []string{"label", "id"},
			})
			if err != nil {
				t.Fatal(err)
			}
			if page.RowCount != 4 || !page.Truncated || !sameStrings(page.Columns, []string{"label", "id"}) {
				t.Errorf("page = %d rows, truncated %v, columns %v", page.RowCount, page.Truncated, page.Columns)
			}
			if !sameStrings(page.PrimaryKey, []string{"id"}) || len(page.Sort) != 1 || fmt.Sprint(page.Rows[0][1]) != "1" {
				t.Errorf("key = %v sort = %v first = %v", page.PrimaryKey, page.Sort, page.Rows[0])
			}
			if page.EstimatedRows == nil || *page.EstimatedRows != 6 {
				t.Errorf("estimated rows = %v, want 6", page.EstimatedRows)
			}
			// With no schema named, the table is found where the connection is —
			// which for these fixtures is not the engine's default schema.
			bare, err := BrowseTablePage(ctx, db, e.driver, BrowseOptions{Table: "jdwb_items", Limit: 2})
			if err != nil {
				t.Fatal(err)
			}
			if !sameStrings(bare.PrimaryKey, []string{"id"}) || bare.EstimatedRows == nil {
				t.Errorf("with no schema: key = %v estimate = %v", bare.PrimaryKey, bare.EstimatedRows)
			}
			if _, err := ReadCell(ctx, db, e.driver, "", "jdwb_items", "label", map[string]any{"id": json.Number("1")}); err != nil {
				t.Errorf("a cell read with no schema: %v", err)
			}
		})
	}
}

// The property the whole read path rests on, against real servers: a statement
// the classifier called a read cannot write.
func TestLiveReadOnlyScope(t *testing.T) {
	for _, e := range liveBrowseEngines() {
		t.Run(e.name, func(t *testing.T) {
			db, _ := openWorkbench(t, e)
			ctx := context.Background()
			dropAfter(t, db, "jdwb_guard", "jdwb_smuggled")
			if e.driver == DriverClickHouse {
				mustExec(t, db, `CREATE TABLE jdwb_guard (id Int32) ENGINE = MergeTree ORDER BY id`)
			} else {
				mustExec(t, db, `CREATE TABLE jdwb_guard (id INT PRIMARY KEY)`)
			}
			mustExec(t, db, `INSERT INTO jdwb_guard VALUES (1), (2)`)

			writes := []string{
				`INSERT INTO jdwb_guard VALUES (3)`,
				`CREATE TABLE jdwb_smuggled (id INT)`,
				`DROP TABLE jdwb_guard`,
			}
			if e.driver == DriverClickHouse {
				writes[1] = `CREATE TABLE jdwb_smuggled (id Int32) ENGINE = Memory`
				writes = append(writes, `ALTER TABLE jdwb_guard DELETE WHERE 1 = 1`, `TRUNCATE TABLE jdwb_guard`)
			} else {
				writes = append(writes, `UPDATE jdwb_guard SET id = id + 10`, `DELETE FROM jdwb_guard`)
			}
			for _, query := range writes {
				if _, err := RunStatement(ctx, db, e.driver, misread(query), 10); err == nil {
					t.Errorf("%q ran inside the read-only scope", query)
				}
			}
			var n int
			if err := db.QueryRow(`SELECT COUNT(*) FROM jdwb_guard WHERE id IN (1, 2)`).Scan(&n); err != nil || n != 2 {
				t.Errorf("rows untouched = %d (%v), want 2", n, err)
			}
			if _, err := db.Exec(`SELECT 1 FROM jdwb_smuggled`); err == nil {
				t.Error("a CREATE TABLE got through the read-only scope")
			}

			// A statement that answers in rows on this engine has them returned.
			probe := map[string]string{"mariadb": `CHECK TABLE jdwb_guard`, "mysql8": `CHECK TABLE jdwb_guard`, "clickhouse": `EXISTS TABLE jdwb_guard`}[e.name]
			if probe != "" {
				res, err := RunStatement(ctx, db, e.driver, mustStatement(t, e.driver, probe), 10)
				if err != nil || res.RowCount == 0 {
					t.Errorf("%s returned %+v %v, want its rows", probe, res, err)
				}
			}

			// A read still reads, and the connection it used writes afterwards.
			res, err := RunStatement(ctx, db, e.driver, mustStatement(t, e.driver, `SELECT id FROM jdwb_guard ORDER BY id`), 10)
			if err != nil || res.RowCount != 2 {
				t.Fatalf("a read inside the scope: %+v %v", res, err)
			}
			if _, err := RunStatement(ctx, db, e.driver, mustStatement(t, e.driver, `INSERT INTO jdwb_guard VALUES (9)`), 10); err != nil {
				t.Fatalf("a write after a read was refused: %v", err)
			}
		})
	}
}

// An account the server already holds to reads may not change the setting the
// scope uses. Its reads must still work, under the server's own limit.
func TestLiveClickHouseReadOnlyAccountStillReads(t *testing.T) {
	dsn := os.Getenv("JD_TEST_CLICKHOUSE_DSN")
	if dsn == "" {
		t.Skip("set JD_TEST_CLICKHOUSE_DSN to run this")
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	db, err := sql.Open("clickhouse", dsn+sep+"readonly=1")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Skipf("clickhouse unreachable: %v", err)
	}
	ctx := context.Background()
	res, err := RunStatement(ctx, db, DriverClickHouse, mustStatement(t, DriverClickHouse, `SELECT 1 AS one`), 10)
	if err != nil {
		t.Fatalf("a read on a read-only session failed: %v", err)
	}
	if res.RowCount != 1 {
		t.Errorf("rows = %d", res.RowCount)
	}
	if _, err := RunStatement(ctx, db, DriverClickHouse, misread(`CREATE TABLE jdwb_never (id Int32) ENGINE = Memory`), 10); err == nil {
		_, _ = db.Exec(`DROP TABLE IF EXISTS jdwb_never`)
		t.Error("a write ran on a read-only session")
	}
}

func TestLiveRunScript(t *testing.T) {
	for _, e := range workbenchEngines() {
		t.Run(e.name, func(t *testing.T) {
			db, _ := openWorkbench(t, e)
			ctx := context.Background()
			dropAfter(t, db, "jdwb_script")
			mustExec(t, db, `CREATE TABLE jdwb_script (id INT PRIMARY KEY, label VARCHAR(20))`)

			// Session state set on one line is there on the next, and gone for
			// the next request.
			set, show := `SET @jdwb = 41`, `SELECT @jdwb + 1`
			if e.driver == DriverPostgres {
				set, show = `SET application_name = 'jdwb-script'`, `SHOW application_name`
			}
			res, err := RunScript(ctx, db, e.driver, parseScript(t, e.driver, set+";\n"+show), ScriptOptions{})
			if err != nil || res.Failed != -1 {
				t.Fatalf("script: %+v %v", res, err)
			}
			want := "42"
			if e.driver == DriverPostgres {
				want = "jdwb-script"
			}
			if got := fmt.Sprint(res.Steps[1].Result.Rows[0][0]); got != want {
				t.Errorf("the second statement saw %q, want %q", got, want)
			}
			after, err := RunStatement(ctx, db, e.driver, mustStatement(t, e.driver, show), 10)
			if err != nil {
				t.Fatal(err)
			}
			if got := fmt.Sprint(after.Rows[0][0]); got == want {
				t.Errorf("the script's session state reached the next request: %q", got)
			}

			// One transaction: the first failure undoes what came before it.
			res, err = RunScript(ctx, db, e.driver, parseScript(t, e.driver, `
				INSERT INTO jdwb_script VALUES (1, 'a');
				INSERT INTO jdwb_script VALUES (1, 'duplicate key');
				INSERT INTO jdwb_script VALUES (2, 'b')`), ScriptOptions{Transaction: true})
			if err != nil {
				t.Fatal(err)
			}
			if res.Failed != 1 || res.Transaction != ScriptRolledBack || res.Steps[2].Status != StepSkipped {
				t.Errorf("result = %+v", res)
			}
			var n int
			if err := db.QueryRow(`SELECT COUNT(*) FROM jdwb_script`).Scan(&n); err != nil || n != 0 {
				t.Errorf("rows after a rolled-back script = %d (%v)", n, err)
			}
			// Without one, each statement stands and the script still stops.
			res, err = RunScript(ctx, db, e.driver, parseScript(t, e.driver, `
				INSERT INTO jdwb_script VALUES (1, 'a');
				INSERT INTO jdwb_script VALUES (1, 'duplicate key');
				INSERT INTO jdwb_script VALUES (2, 'b')`), ScriptOptions{})
			if err != nil || res.Failed != 1 || res.Transaction != ScriptNone {
				t.Fatalf("result = %+v %v", res, err)
			}
			if err := db.QueryRow(`SELECT COUNT(*) FROM jdwb_script`).Scan(&n); err != nil || n != 1 {
				t.Errorf("rows after a stopped script = %d (%v), want the first insert", n, err)
			}
		})
	}
}

// Cancelling stops the statement on the server, not just the wait for it.
func TestLiveCancelStopsTheStatementOnTheServer(t *testing.T) {
	for _, e := range workbenchEngines() {
		t.Run(e.name, func(t *testing.T) {
			db, _ := openWorkbench(t, e)
			marker := fmt.Sprintf("jdwb_cancel_%d", time.Now().UnixNano())
			sleep := `SELECT /* ` + marker + ` */ SLEEP(60)`
			running := `SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE INFO LIKE ? AND ID <> CONNECTION_ID()`
			if e.driver == DriverPostgres {
				sleep = `SELECT /* ` + marker + ` */ pg_sleep(60)`
				running = `SELECT COUNT(*) FROM pg_stat_activity WHERE query LIKE $1 AND state = 'active' AND pid <> pg_backend_pid()`
			}
			count := func() int {
				var n int
				if err := db.QueryRow(running, "%"+marker+"%").Scan(&n); err != nil {
					t.Fatal(err)
				}
				return n
			}

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() {
				_, err := RunStatement(ctx, db, e.driver, mustStatement(t, e.driver, sleep), 10)
				done <- err
			}()
			deadline := time.Now().Add(10 * time.Second)
			for count() == 0 {
				if time.Now().After(deadline) {
					t.Fatal("the statement never showed up as running")
				}
				time.Sleep(50 * time.Millisecond)
			}
			cancel()
			select {
			case err := <-done:
				if err == nil {
					t.Error("a cancelled statement reported success")
				}
			case <-time.After(10 * time.Second):
				t.Fatal("the request did not return after it was cancelled")
			}
			deadline = time.Now().Add(10 * time.Second)
			for count() != 0 {
				if time.Now().After(deadline) {
					t.Fatal("the server is still running the statement ten seconds after it was cancelled")
				}
				time.Sleep(100 * time.Millisecond)
			}
		})
	}
}

func TestLiveExplain(t *testing.T) {
	for _, e := range workbenchEngines() {
		t.Run(e.name, func(t *testing.T) {
			db, _ := openWorkbench(t, e)
			ctx := context.Background()
			dropAfter(t, db, "jdwb_plan")
			mustExec(t, db,
				`CREATE TABLE jdwb_plan (id INT PRIMARY KEY, label VARCHAR(20))`,
				`INSERT INTO jdwb_plan VALUES (1, 'a'), (2, 'b'), (3, 'c')`)
			rows := func() int {
				var n int
				if err := db.QueryRow(`SELECT COUNT(*) FROM jdwb_plan`).Scan(&n); err != nil {
					t.Fatal(err)
				}
				return n
			}

			sel, err := ExplainTarget(e.driver, `SELECT * FROM jdwb_plan WHERE id > 1`)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := Explain(ctx, db, e.driver, sel, ExplainOptions{Format: ExplainJSON})
			if err != nil {
				t.Fatalf("json plan: %v", err)
			}
			if plan.Plan == nil || plan.Analyzed || plan.Format != ExplainJSON {
				t.Fatalf("json plan = %+v", plan)
			}
			// A tree, not a string: an object, or PostgreSQL's array of one.
			switch plan.Plan.(type) {
			case map[string]any, []any:
			default:
				t.Errorf("the plan was not parsed: %T", plan.Plan)
			}

			del, err := ExplainTarget(e.driver, `DELETE FROM jdwb_plan WHERE id > 1`)
			if err != nil {
				t.Fatal(err)
			}
			// Without analyze nothing runs.
			if _, err := Explain(ctx, db, e.driver, del, ExplainOptions{}); err != nil {
				t.Fatalf("plain plan of a DELETE: %v", err)
			}
			if rows() != 3 {
				t.Fatal("a plain plan deleted rows")
			}

			// MySQL only measures what it can run as a query; the others run
			// the DELETE for real and it is rolled back.
			analysed, err := Explain(ctx, db, e.driver, sel, ExplainOptions{Analyze: true})
			if err != nil {
				t.Fatalf("analysed plan of a SELECT: %v", err)
			}
			if !analysed.Analyzed || analysed.RolledBack || analysed.Result.RowCount == 0 {
				t.Errorf("analysed select = %+v", analysed)
			}
			if e.name == "mysql8" {
				return
			}
			measured, err := Explain(ctx, db, e.driver, del, ExplainOptions{Analyze: true, Format: ExplainJSON})
			if err != nil {
				t.Fatalf("analysed plan of a DELETE: %v", err)
			}
			if !measured.Analyzed || !measured.RolledBack || measured.Plan == nil {
				t.Errorf("analysed delete = %+v", measured)
			}
			if rows() != 3 {
				t.Errorf("rows after an analysed DELETE = %d: it was not rolled back", rows())
			}
		})
	}
}

// The body this lexer reads out of a dollar-quoted string is the body
// PostgreSQL reads out of it, and what follows the body is not part of the
// statement PostgreSQL ran.
func TestLiveDollarQuotesAgreeWithPostgres(t *testing.T) {
	db, _ := openWorkbench(t, workbenchEngines()[0])
	dropAfter(t, db, "jdwb_canary")
	mustExec(t, db, `CREATE TABLE jdwb_canary (id INT)`)
	for _, literal := range []string{
		"$$plain$$",
		"$$$$",
		"$a$ ; DROP TABLE jdwb_canary; $a$",
		"$a$ $b$ $$ ' \" -- /* $a$",
		"$tag$ $ta$ $tagg$ $tag $tag$",
		"$$a$b$$",
		"$tagé$ ; DROP TABLE jdwb_canary; $tagé$",
		"$_1$ $1$ $_1$",
		"$x$$x$",
		"$A$ $a$ $A$",
	} {
		query := "SELECT " + literal + "; DROP TABLE jdwb_canary"
		statements, err := ParseScript(DriverPostgres, query)
		if err != nil || len(statements) != 2 {
			t.Errorf("%q: split into %d (%v), want 2", query, len(statements), err)
			continue
		}
		tokens, err := lexSQL(lexRulesFor(DriverPostgres), statements[0].SQL)
		if err != nil || len(tokens) != 2 || tokens[1].kind != tokDollar {
			t.Errorf("%q: tokens = %+v (%v)", statements[0].SQL, tokens, err)
			continue
		}
		text := statements[0].SQL[tokens[1].start:tokens[1].end]
		tag := strings.Index(text[1:], "$") + 2
		body := text[tag : len(text)-tag]

		var got string
		if err := db.QueryRow(statements[0].SQL).Scan(&got); err != nil {
			t.Errorf("PostgreSQL refused %q: %v", statements[0].SQL, err)
			continue
		}
		if got != body {
			t.Errorf("%q: PostgreSQL read the body as %q, this lexer as %q", literal, got, body)
		}
	}
	// An identifier with dollars in it is a name to both.
	if _, err := db.Exec(`CREATE TABLE jdwb_canary2 (a$$ INT, b$c$ INT)`); err != nil {
		t.Fatalf("PostgreSQL refused dollar signs in names: %v", err)
	}
	defer db.Exec(`DROP TABLE IF EXISTS jdwb_canary2`)
	statements, err := ParseScript(DriverPostgres, "SELECT a$$ FROM jdwb_canary2; SELECT b$c$ FROM jdwb_canary2")
	if err != nil || len(statements) != 2 {
		t.Fatalf("split into %d (%v), want 2", len(statements), err)
	}
	for _, st := range statements {
		if _, err := db.Exec(st.SQL); err != nil {
			t.Errorf("PostgreSQL refused %q: %v", st.SQL, err)
		}
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM jdwb_canary`).Scan(&n); err != nil {
		t.Errorf("the canary table is gone: %v", err)
	}
}

// The comment and quote rules the lexer applies to MySQL are MySQL's.
func TestLiveLexerAgreesWithMySQL(t *testing.T) {
	for _, e := range workbenchEngines()[1:] {
		t.Run(e.name, func(t *testing.T) {
			db, _ := openWorkbench(t, e)
			for _, c := range []struct{ query, want string }{
				{"SELECT 'a;b' # ; SELECT 2\n", "a;b"},
				{"SELECT 'it''s' -- ; SELECT 2\n", "it's"},
				{"SELECT `c` FROM (SELECT 'x' AS `c`) AS `t;u`", "x"},
				{`SELECT 'C:\\dir\\'`, `C:\dir\`},
				{"SELECT /* ; */ 'kept'", "kept"},
				{"SELECT \"double ; quoted\"", "double ; quoted"},
			} {
				statements, err := ParseScript(DriverMySQL, c.query)
				if err != nil || len(statements) != 1 {
					t.Errorf("%q: split into %d (%v), want 1", c.query, len(statements), err)
					continue
				}
				var got string
				if err := db.QueryRow(statements[0].SQL).Scan(&got); err != nil {
					t.Errorf("the server refused %q: %v", statements[0].SQL, err)
					continue
				}
				if got != c.want {
					t.Errorf("%q returned %q, want %q", statements[0].SQL, got, c.want)
				}
			}
		})
	}
}

func TestLiveReadCell(t *testing.T) {
	for _, e := range workbenchEngines() {
		t.Run(e.name, func(t *testing.T) {
			db, schema := openWorkbench(t, e)
			ctx := context.Background()
			dropAfter(t, db, "jdwb_cells")
			blobType := "BYTEA"
			if e.driver == DriverMySQL {
				blobType = "LONGBLOB"
			}
			mustExec(t, db, `CREATE TABLE jdwb_cells (id INT PRIMARY KEY, body TEXT, raw `+blobType+`)`)
			long := strings.Repeat("long text é ", 2000)
			blob := bytes.Repeat([]byte{0x00, 0xff, 'h', 'i'}, 300)
			if _, err := db.Exec(`INSERT INTO jdwb_cells VALUES (1, `+placeholder(e, 1)+`, `+placeholder(e, 2)+`)`, long, blob); err != nil {
				t.Fatal(err)
			}

			// The grid shows a preview of both and says so.
			page, err := Browse(ctx, db, e.driver, BrowseOptions{Schema: schema, Table: "jdwb_cells", ClipText: 1024})
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Clipped) != 2 {
				t.Fatalf("clipped = %+v, want both cells", page.Clipped)
			}
			for _, c := range page.Clipped {
				want := int64(len(long))
				if page.Columns[c.Column] == "raw" {
					want = int64(len(blob))
				}
				if c.Size != want {
					t.Errorf("clipped %s size = %d, want %d", page.Columns[c.Column], c.Size, want)
				}
			}

			// And the cell route returns each whole.
			key := map[string]any{"id": json.Number("1")}
			cell, err := ReadCell(ctx, db, e.driver, schema, "jdwb_cells", "body", key)
			if err != nil || cell.Encoding != "text" || cell.Value != long || cell.Size != int64(len(long)) {
				t.Errorf("text cell: %v encoding %v size %v", err, cell.Encoding, cell.Size)
			}
			cell, err = ReadCell(ctx, db, e.driver, schema, "jdwb_cells", "raw", key)
			if err != nil || cell.Encoding != "base64" || cell.Size != int64(len(blob)) || cell.Kind != KindBinary {
				t.Fatalf("binary cell: %v %+v", err, cell)
			}
			if _, err := ReadCell(ctx, db, e.driver, schema, "jdwb_cells", "raw", map[string]any{"id": json.Number("2")}); !errors.Is(err, ErrRowNotFound) {
				t.Errorf("a missing row = %v", err)
			}
		})
	}
}

func placeholder(e workbenchEngine, n int) string {
	return mustDialectFor(e.driver).Placeholder(n)
}

func mustDialectFor(driver Driver) Dialect {
	d, err := DialectFor(driver)
	if err != nil {
		panic(err)
	}
	return d
}

// What the catalogue says about a PostgreSQL table, now that it is read from
// pg_attribute: real type names, a materialized view's columns, an index on an
// expression.
func TestLivePostgresColumnsAndIndexes(t *testing.T) {
	db, _ := openWorkbench(t, workbenchEngines()[0])
	ctx := context.Background()
	_, _ = db.Exec(`DROP MATERIALIZED VIEW IF EXISTS jdwb_typed_mv`)
	dropAfter(t, db, "jdwb_typed")
	_, _ = db.Exec(`DROP TYPE IF EXISTS jdwb_mood`)
	mustExec(t, db,
		`CREATE TYPE jdwb_mood AS ENUM ('sad', 'ok', 'happy')`,
		`CREATE TABLE jdwb_typed (
			id BIGINT PRIMARY KEY,
			tags TEXT[],
			mood jdwb_mood DEFAULT 'ok',
			email VARCHAR(255) NOT NULL,
			amount NUMERIC(10,2),
			seen TIMESTAMP(3) WITH TIME ZONE,
			extra TEXT
		)`,
		`CREATE INDEX jdwb_typed_lower ON jdwb_typed (lower(email), id) INCLUDE (extra)`,
		`CREATE MATERIALIZED VIEW jdwb_typed_mv AS SELECT id, email FROM jdwb_typed`)
	t.Cleanup(func() {
		_, _ = db.Exec(`DROP MATERIALIZED VIEW IF EXISTS jdwb_typed_mv`)
		_, _ = db.Exec(`DROP TABLE IF EXISTS jdwb_typed`)
		_, _ = db.Exec(`DROP TYPE IF EXISTS jdwb_mood`)
	})

	cols, err := ListColumns(ctx, db, DriverPostgres, "public", "jdwb_typed")
	if err != nil {
		t.Fatal(err)
	}
	types := map[string]Column{}
	for _, c := range cols {
		types[c.Name] = c
	}
	for name, want := range map[string]string{
		"id": "bigint", "tags": "text[]", "mood": "jdwb_mood", "email": "character varying(255)",
		"amount": "numeric(10,2)", "seen": "timestamp(3) with time zone",
	} {
		if got := types[name].Type; got != want {
			t.Errorf("%s is reported as %q, want %q", name, got, want)
		}
	}
	if types["email"].Nullable || !types["tags"].Nullable || !strings.Contains(types["mood"].Default, "ok") || types["id"].Position != 1 {
		t.Errorf("columns = %+v", cols)
	}
	if mv, err := ListColumns(ctx, db, DriverPostgres, "public", "jdwb_typed_mv"); err != nil || len(mv) != 2 {
		t.Errorf("a materialized view's columns = %v (%v), want 2", mv, err)
	}

	detail, err := Detail(ctx, db, DriverPostgres, "public", "jdwb_typed")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ix := range detail.Indexes {
		if ix.Name != "jdwb_typed_lower" {
			continue
		}
		found = true
		// The expression is kept, the key column follows, and the INCLUDE
		// column is not passed off as part of the key.
		if len(ix.Columns) != 2 || !strings.Contains(ix.Columns[0], "lower") || ix.Columns[1] != "id" {
			t.Errorf("expression index columns = %q", ix.Columns)
		}
	}
	if !found {
		t.Errorf("the expression index is missing: %+v", detail.Indexes)
	}
}

// SHOW CREATE TABLE answers in four columns for a view. Scanning two failed,
// and the view had no definition at all.
func TestLiveMySQLViewDefinition(t *testing.T) {
	for _, e := range workbenchEngines()[1:] {
		t.Run(e.name, func(t *testing.T) {
			db, schema := openWorkbench(t, e)
			_, _ = db.Exec(`DROP VIEW IF EXISTS jdwb_view`)
			dropAfter(t, db, "jdwb_viewed")
			mustExec(t, db,
				`CREATE TABLE jdwb_viewed (id INT PRIMARY KEY)`,
				`CREATE VIEW jdwb_view AS SELECT id FROM jdwb_viewed`)
			t.Cleanup(func() { _, _ = db.Exec(`DROP VIEW IF EXISTS jdwb_view`) })
			d := mustDialectFor(DriverMySQL)
			ddl, err := d.CreateSQL(context.Background(), db, schema, "jdwb_view", nil)
			if err != nil || !strings.Contains(strings.ToUpper(ddl), "VIEW") {
				t.Errorf("view definition = %q (%v)", ddl, err)
			}
			ddl, err = d.CreateSQL(context.Background(), db, schema, "jdwb_viewed", nil)
			if err != nil || !strings.Contains(ddl, "CREATE TABLE") {
				t.Errorf("table definition = %q (%v)", ddl, err)
			}
		})
	}
}
