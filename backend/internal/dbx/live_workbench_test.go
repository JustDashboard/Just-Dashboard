package dbx

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/url"
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
		// SQL Server has no JSON type before 2025; a document is text there.
		{"sqlserver", DriverMSSQL, "JD_TEST_MSSQL_DSN", "VARBINARY(64)", "NVARCHAR(MAX)"},
		{"oracle", DriverOracle, "JD_TEST_ORACLE_DSN", "RAW(64)", "JSON"},
	}
}

// mysqlEngines are the two servers behind the MySQL driver.
func mysqlEngines() []workbenchEngine { return workbenchEngines()[1:3] }

// ident is a name as the engine's catalogue holds it when the fixture wrote it
// without quotes. Oracle folds one to upper case, and the dashboard — which
// quotes every identifier it sends — has to be given it the way it is stored.
func (e workbenchEngine) ident(name string) string {
	if e.driver == DriverOracle {
		return strings.ToUpper(name)
	}
	return name
}

// row is ident for the column names of a key or a set of values.
func (e workbenchEngine) row(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for name, v := range m {
		out[e.ident(name)] = v
	}
	return out
}

// identity is a 64-bit primary key column the engine fills in itself.
func (e workbenchEngine) identity() string {
	switch e.driver {
	case DriverMySQL:
		return "id BIGINT AUTO_INCREMENT PRIMARY KEY"
	case DriverMSSQL:
		return "id BIGINT IDENTITY(1,1) PRIMARY KEY"
	case DriverOracle:
		return "id NUMBER(19) GENERATED ALWAYS AS IDENTITY PRIMARY KEY"
	}
	return "id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY"
}

func (e workbenchEngine) bigint() string {
	if e.driver == DriverOracle {
		return "NUMBER(19)"
	}
	return "BIGINT"
}

// bytes is a binary literal.
func (e workbenchEngine) bytes(hexDigits string) string {
	switch e.driver {
	case DriverPostgres:
		return `'\x` + hexDigits + `'::bytea`
	case DriverOracle:
		return "HEXTORAW('" + hexDigits + "')"
	}
	return "0x" + hexDigits
}

// longText and longBytes are the types with no length to run out of.
func (e workbenchEngine) longText() string {
	switch e.driver {
	case DriverMySQL:
		return "LONGTEXT"
	case DriverMSSQL:
		return "NVARCHAR(MAX)"
	case DriverOracle:
		return "CLOB"
	}
	return "TEXT"
}

func (e workbenchEngine) longBytes() string {
	switch e.driver {
	case DriverPostgres:
		return "BYTEA"
	case DriverMSSQL:
		return "VARBINARY(MAX)"
	case DriverOracle:
		return "BLOB"
	}
	return "LONGBLOB"
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
	case DriverMSSQL:
		return db, "dbo"
	}
	info, err := ParseDSN(e.driver, dsn)
	if e.driver == DriverOracle {
		// An Oracle schema is the account that owns it.
		if err != nil || info.User == "" {
			t.Fatalf("%s names no account: %v", e.env, err)
		}
		return db, strings.ToUpper(info.User)
	}
	if err != nil || info.Database == "" {
		t.Fatalf("%s names no database: %v", e.env, err)
	}
	return db, info.Database
}

func mustExec(t *testing.T, db *sql.DB, statements ...string) {
	t.Helper()
	for _, s := range statements {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%v\n%s", err, s)
		}
	}
}

// insertRows inserts each tuple with a statement of its own: Oracle before 23
// has no multi-row VALUES.
func insertRows(t *testing.T, db *sql.DB, into string, tuples ...string) {
	t.Helper()
	for _, tuple := range tuples {
		mustExec(t, db, into+" VALUES "+tuple)
	}
}

func dropAfter(t *testing.T, db *sql.DB, tables ...string) {
	t.Helper()
	drop := func() {
		for _, table := range tables {
			if _, err := db.Exec("DROP TABLE IF EXISTS " + table); err != nil {
				// Oracle before 23 has no IF EXISTS either.
				_, _ = db.Exec("DROP TABLE " + table)
			}
		}
	}
	drop()
	t.Cleanup(drop)
}

// sameNumber compares two decimal renderings by value: Oracle prints a NUMBER
// without the trailing zeros its declared scale would allow.
func sameNumber(a, b string) bool {
	x, okA := new(big.Rat).SetString(a)
	y, okB := new(big.Rat).SetString(b)
	return okA && okB && x.Cmp(y) == 0
}

func TestLiveChangeSets(t *testing.T) {
	for _, e := range workbenchEngines() {
		t.Run(e.name, func(t *testing.T) {
			db, schema := openWorkbench(t, e)
			ctx := context.Background()
			dropAfter(t, db, "jdwb_people", "jdwb_notes", "jdwb_blobkey", "jdwb_plain")
			mustExec(t, db,
				`CREATE TABLE jdwb_people (`+e.identity()+`,
					name VARCHAR(100) NOT NULL,
					age INT DEFAULT 18,
					big `+e.bigint()+`,
					price DECIMAL(30,10),
					data `+e.binary+`,
					doc `+e.json+`
				)`,
				`CREATE TABLE jdwb_notes (msg VARCHAR(100), n INT)`,
				`CREATE TABLE jdwb_blobkey (k `+e.binary+` PRIMARY KEY, v VARCHAR(20))`,
				`CREATE TABLE jdwb_plain (id INT PRIMARY KEY, label VARCHAR(20) DEFAULT 'none')`,
			)
			insertRows(t, db, `INSERT INTO jdwb_people(name, age)`, `('Ann', 31)`, `('Bo', 24)`, `('Cy', 40)`)
			insertRows(t, db, `INSERT INTO jdwb_notes`, `('one', 1)`, `('two', NULL)`, `('dup', 7)`, `('dup', 7)`)
			insertRows(t, db, `INSERT INTO jdwb_blobkey`,
				`(`+e.bytes("68656c6c6f")+`, 'text-like')`, `(`+e.bytes("00ff")+`, 'binary')`)
			blob := []byte{0x00, 0xff, 0x10, 'a', 0x80}
			people := e.ident("jdwb_people")
			id := func(n string) map[string]any { return e.row(map[string]any{"id": json.Number(n)}) }

			res, err := ApplyChanges(ctx, db, e.driver, ChangeSet{Schema: schema, Table: people, Changes: []Change{
				{Op: ChangeInsert, Values: e.row(map[string]any{
					"name":  "exact",
					"big":   json.Number("9223372036854775807"),
					"price": json.Number("12345678901234567890.1234567890"),
					"data":  map[string]any{"$base64": "AP8QYYA="},
					"doc":   map[string]any{"n": json.Number("9007199254740993"), "tags": []any{"a", "b"}},
				})},
				{Op: ChangeUpdate, Key: id("1"), Values: e.row(map[string]any{"age": json.Number("32")})},
				// Writing back the value that is already there. MySQL reports
				// zero rows changed for this; it still matched exactly one.
				{Op: ChangeUpdate, Key: id("2"), Values: e.row(map[string]any{"name": "Bo"})},
				{Op: ChangeDelete, Key: id("3")},
			}})
			if err != nil {
				t.Fatalf("ApplyChanges: %v", err)
			}
			if !res.Applied || !sameStrings(res.KeyColumns, []string{e.ident("id")}) {
				t.Fatalf("result = %+v", res)
			}
			inserted := res.Results[0].Row
			_, readsKey := mustDialectFor(e.driver).(generatedKeyReader)
			if inserted == nil {
				// SQL Server and Oracle hand back neither the row nor the key
				// they generated, so the grid's next page is where it shows up.
				if mustDialectFor(e.driver).SupportsReturning() || readsKey {
					t.Fatal("the inserted row did not come back")
				}
				page, err := Browse(ctx, db, e.driver, BrowseOptions{Schema: schema, Table: people, Filters: []Filter{
					{Column: e.ident("name"), Op: "eq", Value: "exact"},
				}})
				if err != nil || page.RowCount != 1 {
					t.Fatalf("the inserted row is not in the table: %+v %v", page, err)
				}
				inserted, _ = rowObject(page, 0)
			}
			// The generated key, the default and the exact values, as stored.
			if inserted[e.ident("id")] != "4" || inserted[e.ident("age")] != "18" || inserted[e.ident("big")] != "9223372036854775807" {
				t.Errorf("inserted row = %v", inserted)
			}
			if got := fmt.Sprint(inserted[e.ident("price")]); !sameNumber(got, "12345678901234567890.1234567890") {
				t.Errorf("price came back as %s", got)
			}
			if inserted[e.ident("data")] != `\x00ff106180` {
				t.Errorf("binary cell = %v, want it as hex", inserted[e.ident("data")])
			}
			if row := res.Results[1].Row; row == nil || row[e.ident("age")] != "32" {
				t.Errorf("updated row = %v", res.Results[1].Row)
			}
			if res.Results[2].Affected != 1 || res.Results[2].Row == nil {
				t.Errorf("an update that changed nothing = %+v", res.Results[2])
			}
			var big, price, doc string
			var data []byte
			if err := db.QueryRow(`SELECT big, price, data, doc FROM jdwb_people WHERE id = 4`).
				Scan(&big, &price, &data, &doc); err != nil {
				t.Fatal(err)
			}
			if big != "9223372036854775807" || !sameNumber(price, "12345678901234567890.1234567890") || !bytes.Equal(data, blob) {
				t.Errorf("stored big = %s price = %s data = %x", big, price, data)
			}
			if !strings.Contains(doc, "9007199254740993") {
				t.Errorf("stored doc = %s", doc)
			}

			// A conflict undoes the set, earlier changes included.
			_, err = ApplyChanges(ctx, db, e.driver, ChangeSet{Schema: schema, Table: people, Changes: []Change{
				{Op: ChangeInsert, Values: e.row(map[string]any{"name": "should not stay"})},
				{Op: ChangeUpdate, Key: id("999"), Values: e.row(map[string]any{"age": json.Number("1")})},
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
			_, err = ApplyChanges(ctx, db, e.driver, ChangeSet{Schema: schema, Table: people, Changes: []Change{
				{Op: ChangeDelete, Key: id("1")},
				{Op: ChangeInsert, Values: e.row(map[string]any{"age": json.Number("5")})}, // name is NOT NULL
			}})
			if !errors.As(err, &change) || change.Conflict || change.Index != 1 {
				t.Fatalf("err = %v, want the engine's refusal of the second change", err)
			}
			if err := db.QueryRow(`SELECT COUNT(*) FROM jdwb_people WHERE id = 1`).Scan(&n); err != nil || n != 1 {
				t.Errorf("a delete from a failed set stayed (%d, %v)", n, err)
			}

			// A key the request supplies is one the row can be found by again,
			// on every engine: the stored row comes back, defaults filled in,
			// and again after the key itself is edited.
			res, err = ApplyChanges(ctx, db, e.driver, ChangeSet{Schema: schema, Table: e.ident("jdwb_plain"), Changes: []Change{
				{Op: ChangeInsert, Values: id("7")},
				{Op: ChangeUpdate, Key: id("7"), Values: e.row(map[string]any{"id": json.Number("8"), "label": map[string]any{"$default": true}})},
			}})
			if err != nil {
				t.Fatalf("a row with its key supplied: %v", err)
			}
			if row := res.Results[0].Row; row == nil || row[e.ident("id")] != "7" || row[e.ident("label")] != "none" {
				t.Errorf("inserted row = %v, want it with its default", res.Results[0].Row)
			}
			if row := res.Results[1].Row; row == nil || row[e.ident("id")] != "8" || row[e.ident("label")] != "none" {
				t.Errorf("row after its key was edited = %v", res.Results[1].Row)
			}

			// No primary key: matched on the row, NULL as NULL, one row or none.
			notes := e.ident("jdwb_notes")
			res, err = ApplyChanges(ctx, db, e.driver, ChangeSet{Schema: schema, Table: notes, Changes: []Change{
				{Op: ChangeUpdate, Key: e.row(map[string]any{"msg": "two", "n": nil}), Values: e.row(map[string]any{"n": json.Number("2")})},
			}})
			if err != nil || len(res.KeyColumns) != 0 {
				t.Fatalf("keyless update: %v %+v", err, res)
			}
			if row := res.Results[0].Row; row == nil || row[e.ident("n")] != "2" {
				t.Errorf("keyless updated row = %v", res.Results[0].Row)
			}
			_, err = ApplyChanges(ctx, db, e.driver, ChangeSet{Schema: schema, Table: notes, Changes: []Change{
				{Op: ChangeDelete, Key: e.row(map[string]any{"msg": "dup", "n": json.Number("7")})},
			}})
			if !errors.As(err, &change) || !change.Conflict || change.Matched != 2 {
				t.Fatalf("two identical rows: err = %v", err)
			}
			if err := db.QueryRow(`SELECT COUNT(*) FROM jdwb_notes WHERE msg = 'dup'`).Scan(&n); err != nil || n != 2 {
				t.Errorf("duplicate rows left = %d (%v), want both", n, err)
			}
			// The same guard on an update, which would otherwise edit both.
			_, err = ApplyChanges(ctx, db, e.driver, ChangeSet{Schema: schema, Table: notes, Changes: []Change{
				{Op: ChangeUpdate, Key: e.row(map[string]any{"msg": "dup", "n": json.Number("7")}), Values: e.row(map[string]any{"n": json.Number("8")})},
			}})
			if !errors.As(err, &change) || !change.Conflict || change.Matched != 2 {
				t.Fatalf("an update of two identical rows: err = %v", err)
			}
			if err := db.QueryRow(`SELECT COUNT(*) FROM jdwb_notes WHERE n = 7`).Scan(&n); err != nil || n != 2 {
				t.Errorf("duplicate rows unedited = %d (%v), want both", n, err)
			}

			// A binary key, sent back in the form the grid shows it — including
			// one whose bytes happen to spell a word.
			page, err := Browse(ctx, db, e.driver, BrowseOptions{Schema: schema, Table: e.ident("jdwb_blobkey"), OrderBy: e.ident("v")})
			if err != nil {
				t.Fatal(err)
			}
			at := indexOf(page.Columns, e.ident("k"))
			if page.Rows[0][at] != `\x00ff` || page.Rows[1][at] != `\x68656c6c6f` || page.Kinds[at] != KindBinary {
				t.Fatalf("binary keys browse as %v (%v)", page.Rows, page.Kinds)
			}
			for _, row := range page.Rows {
				if _, err := ApplyChanges(ctx, db, e.driver, ChangeSet{Schema: schema, Table: e.ident("jdwb_blobkey"), Changes: []Change{
					{Op: ChangeUpdate, Key: e.row(map[string]any{"k": row[at]}), Values: e.row(map[string]any{"v": "edited"})},
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
	for _, e := range mysqlEngines() {
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
			dropAfter(t, db, "jdwb_items", "jdwb_loose")
			if e.driver == DriverClickHouse {
				mustExec(t, db, `CREATE TABLE jdwb_items (id Int32, label String, price Float64, grp Nullable(String))
					ENGINE = MergeTree ORDER BY id`)
			} else {
				mustExec(t, db, `CREATE TABLE jdwb_items (id INT PRIMARY KEY, label VARCHAR(100), price DOUBLE PRECISION, grp VARCHAR(10))`)
			}
			insertRows(t, db, `INSERT INTO jdwb_items`,
				`(1, '50% off', 5.0, 'a')`, `(2, '50 percent', 7.5, 'a')`, `(3, 'under_score', 1.0, 'b')`,
				`(4, 'underXscore', 2.0, 'b')`, `(5, 'MiXeD Case', 9.0, NULL)`, `(6, 'tail!', 3.0, 'c')`)
			items := e.ident("jdwb_items")
			col := e.ident

			ids := func(opts BrowseOptions) string {
				t.Helper()
				opts.Schema, opts.Table = schema, items
				res, err := Browse(ctx, db, e.driver, opts)
				if err != nil {
					t.Fatalf("Browse(%+v): %v", opts, err)
				}
				at := indexOf(res.Columns, col("id"))
				var out []string
				for _, row := range res.Rows {
					out = append(out, fmt.Sprint(row[at]))
				}
				return strings.Join(out, ",")
			}
			byID := []SortKey{{Column: col("id")}}
			for _, c := range []struct {
				name   string
				filter Filter
				want   string
			}{
				{"contains a percent sign", Filter{Column: col("label"), Op: "contains", Value: "50%"}, "1"},
				{"contains an underscore", Filter{Column: col("label"), Op: "contains", Value: "r_s"}, "3"},
				// A character class to SQL Server's LIKE, and text to everyone.
				{"contains a bracket", Filter{Column: col("label"), Op: "contains", Value: "[a-z]"}, ""},
				{"suffix with the escape character", Filter{Column: col("label"), Op: "suffix", Value: "l!"}, "6"},
				{"prefix", Filter{Column: col("label"), Op: "prefix", Value: "50"}, "1,2"},
				{"icontains", Filter{Column: col("label"), Op: "icontains", Value: "mixed CASE"}, "5"},
				{"not_contains", Filter{Column: col("label"), Op: "not_contains", Value: "score"}, "1,2,5,6"},
				{"eq", Filter{Column: col("label"), Op: "eq", Value: "tail!"}, "6"},
				{"ne", Filter{Column: col("id"), Op: "ne", Value: "1"}, "2,3,4,5,6"},
				{"lt", Filter{Column: col("price"), Op: "lt", Value: "2"}, "3"},
				{"gte", Filter{Column: col("price"), Op: "gte", Value: "7.5"}, "2,5"},
				{"in", Filter{Column: col("id"), Op: "in", Values: []string{"2", "4", "9"}}, "2,4"},
				{"not_in", Filter{Column: col("id"), Op: "not_in", Values: []string{"1", "2", "3", "4"}}, "5,6"},
				{"between", Filter{Column: col("price"), Op: "between", Values: []string{"2", "5"}}, "1,4,6"},
				{"regex", Filter{Column: col("label"), Op: "regex", Value: "^under.score$"}, "3,4"},
				{"contains on a number", Filter{Column: col("id"), Op: "contains", Value: "5"}, "5"},
				{"is_null", Filter{Column: col("grp"), Op: "is_null"}, "5"},
				{"not_null", Filter{Column: col("grp"), Op: "not_null"}, "1,2,3,4,6"},
			} {
				if !contains(FilterOpsFor(e.driver), c.filter.Op) {
					// An operator the engine is not said to have is refused,
					// not approximated.
					if _, err := Browse(ctx, db, e.driver, BrowseOptions{Schema: schema, Table: items, Filters: []Filter{c.filter}}); err == nil {
						t.Errorf("%s ran on an engine that does not list it", c.name)
					}
					continue
				}
				if got := ids(BrowseOptions{Filters: []Filter{c.filter}, Sort: byID}); got != c.want {
					t.Errorf("%s: ids = %q, want %q", c.name, got, c.want)
				}
			}
			if got := ids(BrowseOptions{Sort: []SortKey{{Column: col("grp"), Desc: true}, {Column: col("price"), Desc: true}}, Filters: []Filter{
				{Column: col("grp"), Op: "not_null"},
			}}); got != "6,4,3,2,1" {
				t.Errorf("multi-column sort = %q", got)
			}
			if got := ids(BrowseOptions{MatchAny: true, Sort: byID, Filters: []Filter{
				{Column: col("grp"), Op: "eq", Value: "c"}, {Column: col("price"), Op: "between", Values: []string{"7", "8"}},
			}}); got != "2,6" {
				t.Errorf("match any = %q", got)
			}
			n, err := Count(ctx, db, e.driver, BrowseOptions{Schema: schema, Table: items, Filters: []Filter{
				{Column: col("label"), Op: "icontains", Value: "UNDER"},
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
			case DriverOracle:
				mustExec(t, db, `ANALYZE TABLE jdwb_items COMPUTE STATISTICS`)
			}
			page, err := BrowseTablePage(ctx, db, e.driver, BrowseOptions{
				Schema: schema, Table: items, Limit: 4, Columns: []string{col("label"), col("id")},
			})
			if err != nil {
				t.Fatal(err)
			}
			if page.RowCount != 4 || !page.Truncated || !sameStrings(page.Columns, []string{col("label"), col("id")}) {
				t.Errorf("page = %d rows, truncated %v, columns %v", page.RowCount, page.Truncated, page.Columns)
			}
			if !sameStrings(page.PrimaryKey, []string{col("id")}) || len(page.Sort) != 1 || fmt.Sprint(page.Rows[0][1]) != "1" {
				t.Errorf("key = %v sort = %v first = %v", page.PrimaryKey, page.Sort, page.Rows[0])
			}
			if page.EstimatedRows == nil || *page.EstimatedRows != 6 {
				t.Errorf("estimated rows = %v, want 6", page.EstimatedRows)
			}
			// The page after it starts where this one stopped and is the last.
			next, err := BrowseTablePage(ctx, db, e.driver, BrowseOptions{
				Schema: schema, Table: items, Limit: 4, Offset: 4, Columns: []string{col("id")},
			})
			if err != nil {
				t.Fatal(err)
			}
			if next.RowCount != 2 || next.Truncated || next.Offset != 4 || fmt.Sprint(next.Rows[0][0]) != "5" || fmt.Sprint(next.Rows[1][0]) != "6" {
				t.Errorf("second page = %v, truncated %v", next.Rows, next.Truncated)
			}
			// Sorted the other way, the key still breaks the ties between pages.
			sorted, err := BrowseTablePage(ctx, db, e.driver, BrowseOptions{
				Schema: schema, Table: items, Limit: 2, Offset: 2, Columns: []string{col("id")},
				Sort: []SortKey{{Column: col("grp"), Desc: true}}, Filters: []Filter{{Column: col("grp"), Op: "not_null"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(sorted.Sort) != 2 || sorted.Sort[1].Column != col("id") || sorted.RowCount != 2 ||
				fmt.Sprint(sorted.Rows[0][0]) != "4" || fmt.Sprint(sorted.Rows[1][0]) != "1" {
				t.Errorf("sorted second page = %v by %v", sorted.Rows, sorted.Sort)
			}
			// With no schema named, the table is found where the connection is —
			// which for these fixtures is not the engine's default schema.
			bare, err := BrowseTablePage(ctx, db, e.driver, BrowseOptions{Table: items, Limit: 2})
			if err != nil {
				t.Fatal(err)
			}
			if !sameStrings(bare.PrimaryKey, []string{col("id")}) || bare.EstimatedRows == nil {
				t.Errorf("with no schema: key = %v estimate = %v", bare.PrimaryKey, bare.EstimatedRows)
			}
			if _, err := ReadCell(ctx, db, e.driver, "", items, col("label"), e.row(map[string]any{"id": json.Number("1")})); err != nil {
				t.Errorf("a cell read with no schema: %v", err)
			}

			// A table with no key has no order to page by, and still pages:
			// SQL Server will not take OFFSET without an ORDER BY of some kind.
			if e.driver == DriverClickHouse {
				return
			}
			mustExec(t, db, `CREATE TABLE jdwb_loose (n INT)`)
			insertRows(t, db, `INSERT INTO jdwb_loose`, `(1)`, `(2)`, `(3)`, `(4)`, `(5)`)
			loose, err := BrowseTablePage(ctx, db, e.driver, BrowseOptions{Schema: schema, Table: e.ident("jdwb_loose"), Limit: 2, Offset: 4})
			if err != nil {
				t.Fatal(err)
			}
			if len(loose.PrimaryKey) != 0 || len(loose.Sort) != 0 || loose.RowCount != 1 || loose.Truncated {
				t.Errorf("last page of a keyless table = %d rows, key %v, sort %v, truncated %v",
					loose.RowCount, loose.PrimaryKey, loose.Sort, loose.Truncated)
			}
			loose, err = BrowseTablePage(ctx, db, e.driver, BrowseOptions{Schema: schema, Table: e.ident("jdwb_loose"), Limit: 2})
			if err != nil || loose.RowCount != 2 || !loose.Truncated {
				t.Errorf("first page of a keyless table = %+v %v", loose, err)
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
			insertRows(t, db, `INSERT INTO jdwb_guard`, `(1)`, `(2)`)

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
			// SQL Server has no read-only transaction: there the write is made
			// and then undone, so it is the table that says what happened and
			// not the statement.
			undone := mustDialectFor(e.driver).readScope() == readScopeRollback
			for _, query := range writes {
				if _, err := RunStatement(ctx, db, e.driver, misread(query), 10); err == nil && !undone {
					t.Errorf("%q ran inside the read-only scope", query)
				}
			}
			var n int
			if err := db.QueryRow(`SELECT COUNT(*) FROM jdwb_guard WHERE id IN (1, 2)`).Scan(&n); err != nil || n != 2 {
				t.Errorf("rows untouched = %d (%v), want 2", n, err)
			}
			if err := db.QueryRow(`SELECT COUNT(*) FROM jdwb_guard`).Scan(&n); err != nil || n != 2 {
				t.Errorf("rows in the table = %d (%v), want 2", n, err)
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
			if err := db.QueryRow(`SELECT COUNT(*) FROM jdwb_guard WHERE id = 9`).Scan(&n); err != nil || n != 1 {
				t.Errorf("a write after a read left %d rows (%v), want it kept", n, err)
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

// The two accounts that may not send the setting the scope uses, as accounts
// rather than as a session: one the server holds to reads, which reads under
// the server's own limit, and one whose profile pins readonly at writable,
// which nothing holds to reads — so a statement called a read is not run on it.
func TestLiveClickHouseAccountsThatMayNotSetReadonly(t *testing.T) {
	admin, schema := openWorkbench(t, workbenchEngine{name: "clickhouse", driver: DriverClickHouse, env: "JD_TEST_CLICKHOUSE_DSN"})
	base, err := url.Parse(os.Getenv("JD_TEST_CLICKHOUSE_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	const reader, pinned, password = "jdwb_b2a_reader", "jdwb_b2a_pinned", "jdwb-b2a-Pass1"
	drop := func() {
		_, _ = admin.Exec(`DROP USER IF EXISTS ` + reader + `, ` + pinned)
		_, _ = admin.Exec(`DROP TABLE IF EXISTS jdwb_pinned_proof`)
		_, _ = admin.Exec(`DROP TABLE IF EXISTS jdwb_pinned_never`)
	}
	drop()
	t.Cleanup(drop)
	for _, statement := range []string{
		`CREATE USER ` + reader + ` IDENTIFIED WITH sha256_password BY '` + password + `' SETTINGS readonly = 1`,
		`CREATE USER ` + pinned + ` IDENTIFIED WITH sha256_password BY '` + password + `' SETTINGS readonly = 0 CONST`,
		`GRANT SELECT ON ` + schema + `.* TO ` + reader,
		`GRANT SELECT, INSERT, CREATE TABLE, DROP TABLE ON ` + schema + `.* TO ` + pinned,
	} {
		if _, err := admin.Exec(statement); err != nil {
			t.Skipf("the fixture account may not manage users: %v", err)
		}
	}
	open := func(user string) *sql.DB {
		dsn := *base
		dsn.User = url.UserPassword(user, password)
		db, err := sql.Open("clickhouse", dsn.String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		if err := db.Ping(); err != nil {
			t.Fatalf("%s cannot connect: %v", user, err)
		}
		return db
	}
	ctx := context.Background()
	read := mustStatement(t, DriverClickHouse, `SELECT 1 AS one`)
	smuggled := misread(`CREATE TABLE jdwb_pinned_never (id Int32) ENGINE = Memory`)

	held := open(reader)
	res, err := RunStatement(ctx, held, DriverClickHouse, read, 10)
	if err != nil || res.RowCount != 1 {
		t.Errorf("a read on an account held to reads = %+v %v", res, err)
	}
	if _, err := RunStatement(ctx, held, DriverClickHouse, smuggled, 10); err == nil {
		t.Error("a write ran on an account held to reads")
	}

	// The pinned account can write, which is what makes the missing scope matter.
	loose := open(pinned)
	mustExec(t, loose, `CREATE TABLE jdwb_pinned_proof (id Int32) ENGINE = Memory`)
	if _, err := RunStatement(ctx, loose, DriverClickHouse, smuggled, 10); err == nil {
		t.Error("a write called a read ran on an account nothing holds to reads")
	}
	if _, err := admin.Exec(`SELECT 1 FROM jdwb_pinned_never`); err == nil {
		t.Error("the table a statement called a read created is there")
	}
	_, err = RunStatement(ctx, loose, DriverClickHouse, read, 10)
	if err == nil || !strings.Contains(err.Error(), "would not hold this statement to reading") {
		t.Errorf("a read with no scope to run in = %v, want it refused and told why", err)
	}
	// A script of reads is one scope, and is refused as one.
	if res, err := RunScript(ctx, loose, DriverClickHouse, []SQLStatement{*read, *read}, ScriptOptions{}); err == nil {
		t.Errorf("a script of reads with no scope to run in = %+v", res)
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
			set, show, want := `SET @jdwb = 41`, `SELECT @jdwb + 1`, "42"
			switch e.driver {
			case DriverPostgres:
				set, show, want = `SET application_name = 'jdwb-script'`, `SHOW application_name`, "jdwb-script"
			case DriverMSSQL:
				set, show, want = `SET LANGUAGE Deutsch`, `SELECT @@LANGUAGE`, "Deutsch"
			case DriverOracle:
				set, show, want = `ALTER SESSION SET NLS_DATE_FORMAT = 'YYYY"jdwb"'`, `SELECT TO_CHAR(DATE '2024-05-06') FROM dual`, "2024jdwb"
			}
			res, err := RunScript(ctx, db, e.driver, parseScript(t, e.driver, set+";\n"+show), ScriptOptions{})
			if err != nil || res.Failed != -1 {
				t.Fatalf("script: %+v %v", res, err)
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
			// And one that succeeds is kept whole, with a result per statement.
			res, err = RunScript(ctx, db, e.driver, parseScript(t, e.driver, `
				INSERT INTO jdwb_script VALUES (2, 'b');
				UPDATE jdwb_script SET label = 'c' WHERE id = 2;
				SELECT label FROM jdwb_script ORDER BY id`), ScriptOptions{Transaction: true})
			if err != nil || res.Failed != -1 || res.Transaction != ScriptCommitted {
				t.Fatalf("result = %+v %v", res, err)
			}
			if last := res.Steps[2].Result; res.Steps[1].Result.Affected != 1 || last.RowCount != 2 || last.Rows[1][0] != "c" {
				t.Errorf("steps = %+v", res.Steps)
			}
			// A table the script made a moment ago is read by its next line,
			// read-only scope and all: Oracle will not show a table that new to
			// a read-only transaction, which is one reason its scope is not one.
			_, _ = db.Exec(`DROP TABLE jdwb_made`)
			t.Cleanup(func() { _, _ = db.Exec(`DROP TABLE jdwb_made`) })
			res, err = RunScript(ctx, db, e.driver, parseScript(t, e.driver, `
				CREATE TABLE jdwb_made (n INT);
				INSERT INTO jdwb_made VALUES (5);
				SELECT n FROM jdwb_made`), ScriptOptions{})
			if err != nil || res.Failed != -1 || res.Steps[2].Result.Rows[0][0] != "5" {
				t.Errorf("a script that makes a table and reads it = %+v %v", res, err)
			}
			if e.driver == DriverOracle {
				// EXPLAIN PLAN writes the plan to the session's plan table, and
				// the same session reads it on the next line.
				res, err = RunScript(ctx, db, e.driver, parseScript(t, e.driver, `
					EXPLAIN PLAN FOR SELECT * FROM jdwb_script WHERE id = 1;
					SELECT plan_table_output FROM TABLE(DBMS_XPLAN.DISPLAY())`), ScriptOptions{})
				if err != nil || res.Failed != -1 || res.Steps[1].Result.RowCount == 0 {
					t.Errorf("a plan stored and read in one script = %+v %v", res, err)
				}
			}
			// A script of nothing but reads is one read-only scope.
			res, err = RunScript(ctx, db, e.driver, parseScript(t, e.driver, `
				SELECT COUNT(*) FROM jdwb_script;
				SELECT label FROM jdwb_script WHERE id = 1`), ScriptOptions{})
			if err != nil || res.Failed != -1 || res.Transaction != ScriptReadOnly || res.Steps[1].Result.Rows[0][0] != "a" {
				t.Errorf("a script of reads = %+v %v", res, err)
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
			// What is asked of the server is asked on a connection of its own:
			// Oracle's application account may not see other sessions.
			watch := db
			sleep := `SELECT /* ` + marker + ` */ SLEEP(60)`
			running := `SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE INFO LIKE ? AND ID <> CONNECTION_ID()`
			switch e.driver {
			case DriverPostgres:
				sleep = `SELECT /* ` + marker + ` */ pg_sleep(60)`
				running = `SELECT COUNT(*) FROM pg_stat_activity WHERE query LIKE $1 AND state = 'active' AND pid <> pg_backend_pid()`
			case DriverMSSQL:
				// A read that takes minutes: three system views multiplied.
				sleep = `SELECT /* ` + marker + ` */ COUNT_BIG(*) FROM sys.all_columns a CROSS JOIN sys.all_columns b CROSS JOIN sys.all_columns c`
				running = `SELECT COUNT(*) FROM sys.dm_exec_requests r CROSS APPLY sys.dm_exec_sql_text(r.sql_handle) t
				           WHERE t.text LIKE @p1 AND r.session_id <> @@SPID`
			case DriverOracle:
				admin := os.Getenv("JD_TEST_ORACLE_ADMIN_DSN")
				if admin == "" {
					t.Skip("set JD_TEST_ORACLE_ADMIN_DSN to run this: only an account that may read v$session can see the statement stop")
				}
				watch = liveSQL(t, DriverOracle, "JD_TEST_ORACLE_ADMIN_DSN", admin)
				sleep = `SELECT /* ` + marker + ` */ COUNT(*) FROM all_objects a, all_objects b, all_objects c`
				running = `SELECT COUNT(*) FROM v$session s JOIN v$sql q ON q.sql_id = s.sql_id AND q.child_number = s.sql_child_number
				           WHERE s.status = 'ACTIVE' AND q.sql_text LIKE :1 AND s.sid <> SYS_CONTEXT('USERENV', 'SID')`
			}
			count := func() int {
				var n int
				if err := watch.QueryRow(running, "%"+marker+"%").Scan(&n); err != nil {
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
			// The pool the cancelled statement came from still works.
			res, err := RunStatement(context.Background(), db, e.driver, mustStatement(t, e.driver, map[bool]string{
				true: `SELECT 1 FROM dual`, false: `SELECT 1`}[e.driver == DriverOracle]), 10)
			if err != nil || res.RowCount != 1 {
				t.Errorf("a read after a cancelled one = %+v %v", res, err)
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
			mustExec(t, db, `CREATE TABLE jdwb_plan (id INT PRIMARY KEY, label VARCHAR(20))`)
			insertRows(t, db, `INSERT INTO jdwb_plan`, `(1, 'a')`, `(2, 'b')`, `(3, 'c')`)
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
			del, err := ExplainTarget(e.driver, `DELETE FROM jdwb_plan WHERE id > 1`)
			if err != nil {
				t.Fatal(err)
			}
			// The plain plan, on every engine: text, and nothing runs.
			plain, err := Explain(ctx, db, e.driver, sel, ExplainOptions{})
			if err != nil {
				t.Fatalf("plain plan of a SELECT: %v", err)
			}
			if plain.Format != ExplainText || plain.Analyzed || plain.RolledBack || plain.Plan != nil || plain.Result.RowCount == 0 {
				t.Errorf("plain plan = %+v", plain)
			}
			if _, err := Explain(ctx, db, e.driver, del, ExplainOptions{}); err != nil {
				t.Fatalf("plain plan of a DELETE: %v", err)
			}
			if rows() != 3 {
				t.Fatal("a plain plan deleted rows")
			}
			// The connection a plan was asked on still runs statements: SQL
			// Server's plan is a session mode, and one left on plans for ever.
			if res, err := RunStatement(ctx, db, e.driver, mustStatement(t, e.driver, `SELECT id FROM jdwb_plan ORDER BY id`), 10); err != nil || res.RowCount != 3 {
				t.Errorf("a statement after a plan = %+v %v", res, err)
			}

			jsonPlan, analyze := ExplainForms(e.driver)
			if !jsonPlan && !analyze {
				// SQL Server and Oracle have the text plan and no other; the
				// rest is refused before anything is sent.
				for _, opts := range []ExplainOptions{{Format: ExplainJSON}, {Analyze: true}, {Analyze: true, Format: ExplainJSON}} {
					if _, err := Explain(ctx, db, e.driver, del, opts); !errors.Is(err, ErrExplainUnsupported) {
						t.Errorf("Explain(%+v) = %v, want it refused as unsupported", opts, err)
					}
				}
				if rows() != 3 {
					t.Error("a refused plan deleted rows")
				}
				return
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
				// A measured plan as a document. From 8.3 the server gives one
				// only in the second version of its JSON plan, which the
				// session has to ask for; left on the first it is refused as
				// "not yet supported" by a server that supports it.
				measured, err := Explain(ctx, db, e.driver, sel, ExplainOptions{Analyze: true, Format: ExplainJSON})
				if err != nil {
					t.Fatalf("analysed json plan of a SELECT: %v", err)
				}
				if _, tree := measured.Plan.(map[string]any); !measured.Analyzed || !tree {
					t.Errorf("analysed json plan = %+v", measured)
				}
				// The session that asked for it is not the next request's: a
				// plain JSON plan afterwards is still the first version's.
				for range 6 {
					next, err := Explain(ctx, db, e.driver, sel, ExplainOptions{Format: ExplainJSON})
					if err != nil {
						t.Fatal(err)
					}
					if doc, _ := next.Plan.(map[string]any); doc["query_block"] == nil {
						t.Fatalf("a plan asked for after a measured one came back in its shape: %v", next.Plan)
					}
				}
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
	for _, e := range mysqlEngines() {
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
			mustExec(t, db, `CREATE TABLE jdwb_cells (id INT PRIMARY KEY, body `+e.longText()+`, bin `+e.longBytes()+`)`)
			long := strings.Repeat("long text é ", 2000)
			blob := bytes.Repeat([]byte{0x00, 0xff, 'h', 'i'}, 300)
			if _, err := db.Exec(`INSERT INTO jdwb_cells VALUES (1, `+placeholder(e, 1)+`, `+placeholder(e, 2)+`)`, long, blob); err != nil {
				t.Fatal(err)
			}
			// Not "raw": that is a reserved word on Oracle.
			cells, body, raw := e.ident("jdwb_cells"), e.ident("body"), e.ident("bin")

			// The grid shows a preview of both and says so.
			page, err := Browse(ctx, db, e.driver, BrowseOptions{Schema: schema, Table: cells, ClipText: 1024})
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Clipped) != 2 {
				t.Fatalf("clipped = %+v, want both cells", page.Clipped)
			}
			for _, c := range page.Clipped {
				want := int64(len(long))
				if page.Columns[c.Column] == raw {
					want = int64(len(blob))
					if page.Kinds[c.Column] != KindBinary {
						t.Errorf("the binary column is called %q on the page", page.Kinds[c.Column])
					}
				}
				if c.Size != want {
					t.Errorf("clipped %s size = %d, want %d", page.Columns[c.Column], c.Size, want)
				}
			}

			// And the cell route returns each whole.
			key := e.row(map[string]any{"id": json.Number("1")})
			cell, err := ReadCell(ctx, db, e.driver, schema, cells, body, key)
			if err != nil || cell.Encoding != "text" || cell.Value != long || cell.Size != int64(len(long)) {
				t.Errorf("text cell: %v encoding %v size %v", err, cell.Encoding, cell.Size)
			}
			cell, err = ReadCell(ctx, db, e.driver, schema, cells, raw, key)
			if err != nil || cell.Encoding != "base64" || cell.Size != int64(len(blob)) || cell.Kind != KindBinary {
				t.Fatalf("binary cell: %v %+v", err, cell)
			}
			if got, _ := base64.StdEncoding.DecodeString(fmt.Sprint(cell.Value)); !bytes.Equal(got, blob) {
				t.Errorf("binary cell holds %d bytes that are not the ones stored", len(got))
			}
			if _, err := ReadCell(ctx, db, e.driver, schema, cells, raw, e.row(map[string]any{"id": json.Number("2")})); !errors.Is(err, ErrRowNotFound) {
				t.Errorf("a missing row = %v", err)
			}
			// A number is not text and not bytes, and still has a size.
			cell, err = ReadCell(ctx, db, e.driver, schema, cells, e.ident("id"), key)
			if err != nil || cell.Size != 1 || fmt.Sprint(cell.Value) != "1" {
				t.Errorf("a number cell: %v %+v, want the value 1 and a size of 1", err, cell)
			}
			// A NULL is said to be one, not passed off as an empty value.
			mustExec(t, db, `INSERT INTO jdwb_cells (id) VALUES (3)`)
			cell, err = ReadCell(ctx, db, e.driver, schema, cells, body, e.row(map[string]any{"id": json.Number("3")}))
			if err != nil || cell.Encoding != "null" || cell.Value != nil {
				t.Errorf("a null cell: %v %+v", err, cell)
			}
		})
	}
}

// A value past the bound is measured where it is stored and refused there. It
// is the point of the bound: finding out a value was a gigabyte by fetching it
// is the cost the bound exists to avoid.
func TestLiveReadCellRefusesAValuePastTheBound(t *testing.T) {
	const size = 9_000_000 // past MaxCellBytes
	for _, e := range workbenchEngines() {
		t.Run(e.name, func(t *testing.T) {
			db, schema := openWorkbench(t, e)
			ctx := context.Background()
			dropAfter(t, db, "jdwb_bigcells")
			mustExec(t, db, `CREATE TABLE jdwb_bigcells (id INT PRIMARY KEY, body `+e.longText()+`, bin `+e.longBytes()+`)`)
			// Built on the server, so nothing this large crosses the wire.
			switch e.driver {
			case DriverPostgres:
				mustExec(t, db, fmt.Sprintf(`INSERT INTO jdwb_bigcells VALUES (1, repeat('x', %d), convert_to(repeat('x', %d), 'UTF8'))`, size, size))
			case DriverMySQL:
				mustExec(t, db, fmt.Sprintf(`INSERT INTO jdwb_bigcells VALUES (1, REPEAT('x', %d), REPEAT('x', %d))`, size, size))
			case DriverMSSQL:
				// NVARCHAR is two bytes a character as stored.
				mustExec(t, db, fmt.Sprintf(`INSERT INTO jdwb_bigcells VALUES (1,
					REPLICATE(CAST('x' AS NVARCHAR(MAX)), %d),
					CAST(REPLICATE(CAST('x' AS VARCHAR(MAX)), %d) AS VARBINARY(MAX)))`, size/2, size))
			case DriverOracle:
				mustExec(t, db, fmt.Sprintf(`DECLARE c CLOB; b BLOB;
					BEGIN
					  DBMS_LOB.CREATETEMPORARY(c, TRUE); DBMS_LOB.CREATETEMPORARY(b, TRUE);
					  FOR i IN 1..%d LOOP
					    DBMS_LOB.WRITEAPPEND(c, 30000, RPAD('x', 30000, 'x'));
					    DBMS_LOB.WRITEAPPEND(b, 30000, UTL_RAW.CAST_TO_RAW(RPAD('x', 30000, 'x')));
					  END LOOP;
					  INSERT INTO jdwb_bigcells VALUES (1, c, b);
					  COMMIT;
					END;`, size/30000))
			}
			key := e.row(map[string]any{"id": json.Number("1")})
			for _, column := range []string{"body", "bin"} {
				_, err := ReadCell(ctx, db, e.driver, schema, e.ident("jdwb_bigcells"), e.ident(column), key)
				var large *CellTooLargeError
				if !errors.As(err, &large) {
					t.Fatalf("%s: err = %v, want the value refused for its size", column, err)
				}
				if large.Size != size {
					t.Errorf("%s: measured as %d, want %d", column, large.Size, size)
				}
				// Oracle counts a CLOB in characters, which is the least its
				// bytes can be; every other figure here is exact.
				if floor := e.driver == DriverOracle && column == "body"; large.AtLeast != floor {
					t.Errorf("%s: at least = %v, want %v", column, large.AtLeast, floor)
				}
			}
			// The preview of the same row is still served.
			page, err := Browse(ctx, db, e.driver, BrowseOptions{Schema: schema, Table: e.ident("jdwb_bigcells"), ClipText: 256})
			if err != nil || page.RowCount != 1 || len(page.Clipped) != 2 {
				t.Errorf("the page with the large row = %v, clipped %+v", err, page)
			}
			if e.driver != DriverOracle {
				return
			}
			// The one value the measure lets through: a CLOB of two-byte
			// characters, fewer of them than the bound and more bytes than it.
			// It is fetched, and the bound in bytes still refuses it — with
			// the exact size this time.
			const chars = 4_500_000
			mustExec(t, db, fmt.Sprintf(`DECLARE c CLOB;
				BEGIN
				  DBMS_LOB.CREATETEMPORARY(c, TRUE);
				  FOR i IN 1..%d LOOP
				    DBMS_LOB.WRITEAPPEND(c, 10000, RPAD(UNISTR('\00E9'), 10000, UNISTR('\00E9')));
				  END LOOP;
				  INSERT INTO jdwb_bigcells (id, body) VALUES (2, c);
				  COMMIT;
				END;`, chars/10000))
			_, err = ReadCell(ctx, db, e.driver, schema, e.ident("jdwb_bigcells"), e.ident("body"), e.row(map[string]any{"id": json.Number("2")}))
			var large *CellTooLargeError
			if !errors.As(err, &large) || large.AtLeast || large.Size != 2*chars {
				t.Errorf("a CLOB of %d two-byte characters = %v, want it refused at %d bytes exactly", chars, err, 2*chars)
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
	for _, e := range mysqlEngines() {
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

// wire sends a row through JSON the way a request carries it, so the values a
// test hands back are the ones a browser would: a number as its digits, not as
// the float64 or bool the driver produced.
func wire(t *testing.T, row map[string]any) map[string]any {
	t.Helper()
	b, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	out := map[string]any{}
	if err := dec.Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// A table with no key is edited by the whole row as the grid read it, and a
// keyed one takes back the cells the grid showed. So every type a cell can
// hold has to return to its engine in the form it was displayed in and still
// be the same value: a date as RFC 3339, a decimal as its digits, bytes as
// hex. Oracle read none of its date types that way, and has no = for a LOB.
//
// The Oracle columns include JSON and BOOLEAN, which need 21c and 23ai.
func TestLiveARowGoesBackAsTheGridShowedIt(t *testing.T) {
	tables := map[Driver]struct{ columns, values string }{
		DriverPostgres: {
			`tag VARCHAR(10), s TEXT, c CHAR(5), i INT, b BIGINT, d NUMERIC(20,6), f DOUBLE PRECISION, r REAL,
			 flag BOOLEAN, dt DATE, tm TIME, ts TIMESTAMP, tstz TIMESTAMPTZ, u UUID, bin BYTEA, js JSONB, jt JSON,
			 arr INT[], iv INTERVAL`,
			`('full', 'héllo', 'ab', 1, 9223372036854775807, 1234.5, 1.5, 0.1,
			  TRUE, DATE '2024-01-02', TIME '03:04:05.123456', TIMESTAMP '2024-01-02 03:04:05.123456',
			  TIMESTAMPTZ '2024-01-02 03:04:05.123456+02', '00112233-4455-6677-8899-aabbccddeeff', '\x00ff',
			  '{"a": 1}', '{"a": 1}', '{1,2}', INTERVAL '1 day 02:03:04')`,
		},
		DriverMySQL: {
			`tag VARCHAR(10), s VARCHAR(50), c CHAR(5), i INT, b BIGINT, d DECIMAL(20,6), f DOUBLE,
			 flag TINYINT(1), dt DATE, tm TIME(6), ts DATETIME(6), tstz TIMESTAMP(6) NULL, bin VARBINARY(16), js JSON,
			 en ENUM('x','y'), yr YEAR`,
			`('full', 'héllo', 'ab', 1, 9223372036854775807, 1234.5, 1.5,
			  1, '2024-01-02', '03:04:05.123456', '2024-01-02 03:04:05.123456', '2024-01-02 03:04:05.123456', 0x00ff,
			  '{"a": 1}', 'y', 2024)`,
		},
		DriverMSSQL: {
			`tag VARCHAR(10), s NVARCHAR(50), c CHAR(5), i INT, b BIGINT, d DECIMAL(20,6), f FLOAT, r REAL, m MONEY,
			 flag BIT, dt DATE, tm TIME, dt2 DATETIME2, dto DATETIMEOFFSET, odt DATETIME, sdt SMALLDATETIME,
			 u UNIQUEIDENTIFIER, bin VARBINARY(16), x XML, tx TEXT, ntx NTEXT, big NVARCHAR(MAX)`,
			`('full', N'héllo', 'ab', 1, 9223372036854775807, 1234.5, 1.5, 0.1, 12.34,
			  1, '2024-01-02', '03:04:05.1234567', '2024-01-02T03:04:05.1234567', '2024-01-02T03:04:05.1234567+02:00',
			  '2024-01-02T03:04:05.123', '2024-01-02T03:04:00', '00112233-4455-6677-8899-AABBCCDDEEFF', 0x00ff,
			  '<a>1</a>', 'legacy', N'legacy n', N'long')`,
		},
		DriverOracle: {
			`tag VARCHAR2(10), s VARCHAR2(50), ns NVARCHAR2(50), c CHAR(5), i NUMBER(10), b NUMBER(19), d NUMBER(20,6),
			 n NUMBER, f BINARY_DOUBLE, r BINARY_FLOAT, dt DATE, ts TIMESTAMP, ts3 TIMESTAMP(3),
			 tstz TIMESTAMP WITH TIME ZONE, tsltz TIMESTAMP WITH LOCAL TIME ZONE,
			 iym INTERVAL YEAR TO MONTH, ids INTERVAL DAY TO SECOND,
			 bin RAW(16), cl CLOB, ncl NCLOB, bl BLOB, js JSON, x XMLTYPE, flag BOOLEAN`,
			`('full', 'héllo', N'héllo', 'ab', 1, 9223372036854775807, 1234.5,
			  12345678901234567890.123456789, 1.5, 0.1, TO_DATE('2024-01-02 03:04:05', 'YYYY-MM-DD HH24:MI:SS'),
			  TIMESTAMP '2024-01-02 03:04:05.123456', TIMESTAMP '2024-01-02 03:04:05.123',
			  TIMESTAMP '2024-01-02 03:04:05.123456 +02:00', TIMESTAMP '2024-01-02 03:04:05.123456',
			  INTERVAL '1-2' YEAR TO MONTH, INTERVAL '1 02:03:04' DAY TO SECOND,
			  HEXTORAW('00FF'), 'clob text é', N'nclob text é', HEXTORAW('00FF10'), JSON('{"a":1}'),
			  XMLTYPE('<a><b>1</b></a>'), TRUE)`,
		},
	}
	for _, e := range workbenchEngines() {
		t.Run(e.name, func(t *testing.T) {
			db, schema := openWorkbench(t, e)
			ctx := context.Background()
			dropAfter(t, db, "jdwb_typed_rows")
			spec := tables[e.driver]
			mustExec(t, db,
				`CREATE TABLE jdwb_typed_rows (`+spec.columns+`)`,
				`INSERT INTO jdwb_typed_rows VALUES `+spec.values,
				`INSERT INTO jdwb_typed_rows (tag) VALUES ('empty')`)
			table, tag := e.ident("jdwb_typed_rows"), e.ident("tag")
			read := func() []map[string]any {
				t.Helper()
				page, err := BrowseTablePage(ctx, db, e.driver, BrowseOptions{
					Schema: schema, Table: table, Sort: []SortKey{{Column: tag, Desc: true}},
				})
				if err != nil {
					t.Fatalf("browse: %v", err)
				}
				if len(page.PrimaryKey) != 0 || page.RowCount != 2 || len(page.Clipped) != 0 {
					t.Fatalf("page = %d rows, key %v, clipped %v", page.RowCount, page.PrimaryKey, page.Clipped)
				}
				rows := make([]map[string]any, page.RowCount)
				for i := range rows {
					obj, _ := rowObject(page.QueryResult, i)
					rows[i] = wire(t, obj)
				}
				return rows
			}
			apply := func(change Change) error {
				_, err := ApplyChanges(ctx, db, e.driver, ChangeSet{Schema: schema, Table: table, Changes: []Change{change}})
				return err
			}
			before := read()
			for _, row := range before {
				// Each cell written back as itself, found by the whole row.
				values := map[string]any{}
				for name, v := range row {
					if v != nil {
						values[name] = v
					}
				}
				if err := apply(Change{Op: ChangeUpdate, Key: row, Values: values}); err != nil {
					t.Errorf("row %v written back as shown: %v", row[tag], err)
					// Which column it was is the useful half of that.
					for name, v := range row {
						if err := apply(Change{Op: ChangeUpdate, Key: map[string]any{tag: row[tag], name: v}, Values: map[string]any{tag: row[tag]}}); err != nil {
							t.Errorf("  as a key, %s = %v: %v", name, v, err)
						}
						if v == nil {
							continue
						}
						if err := apply(Change{Op: ChangeUpdate, Key: map[string]any{tag: row[tag]}, Values: map[string]any{name: v}}); err != nil {
							t.Errorf("  as a value, %s = %v: %v", name, v, err)
						}
					}
				}
			}
			after := read()
			for i := range before {
				for name, was := range before[i] {
					if now := after[i][name]; fmt.Sprint(now) != fmt.Sprint(was) {
						t.Errorf("row %v: %s was %v and is %v after being written back as shown", before[i][tag], name, was, now)
					}
				}
			}
			// And removed by the same key: the table ends empty.
			for _, row := range after {
				if err := apply(Change{Op: ChangeDelete, Key: row}); err != nil {
					t.Errorf("row %v deleted by its whole row: %v", row[tag], err)
				}
			}
			var n int
			if err := db.QueryRow(`SELECT COUNT(*) FROM jdwb_typed_rows`).Scan(&n); err != nil || n != 0 {
				t.Errorf("rows left = %d (%v), want none", n, err)
			}
		})
	}
}

// The one-row rule counts the rows the statement touched, not the ones a
// trigger did on its behalf. SQL Server's driver adds the two together unless
// the trigger says SET NOCOUNT ON, so a table with an audit trigger could not
// be edited at all: every change "matched 3 rows".
func TestLiveSQLServerCountsTheStatementNotItsTriggers(t *testing.T) {
	e := workbenchEngines()[3]
	db, schema := openWorkbench(t, e)
	ctx := context.Background()
	dropAfter(t, db, "jdwb_audited", "jdwb_audit_log")
	mustExec(t, db,
		`CREATE TABLE jdwb_audited (id INT, v VARCHAR(20))`,
		`CREATE TABLE jdwb_audit_log (what VARCHAR(20))`,
		// Two rows written per statement, and no SET NOCOUNT ON.
		`CREATE TRIGGER jdwb_audited_log ON jdwb_audited AFTER INSERT, UPDATE, DELETE AS
		 BEGIN INSERT INTO jdwb_audit_log(what) SELECT 'seen' FROM (VALUES (1), (2)) AS twice(n) END`,
		`INSERT INTO jdwb_audited VALUES (1, 'a'), (2, 'b'), (3, 'dup'), (3, 'dup')`)
	apply := func(change Change) (*ChangeSetResult, error) {
		return ApplyChanges(ctx, db, e.driver, ChangeSet{Schema: schema, Table: "jdwb_audited", Changes: []Change{change}})
	}
	id := func(n string) map[string]any { return map[string]any{"id": json.Number(n)} }

	res, err := apply(Change{Op: ChangeUpdate, Key: id("1"), Values: map[string]any{"v": "edited"}})
	if err != nil || res.Results[0].Affected != 1 || res.Results[0].Row["v"] != "edited" {
		t.Fatalf("an update of one row on a table with a trigger: %+v %v", res, err)
	}
	if res, err = apply(Change{Op: ChangeDelete, Key: id("2")}); err != nil || res.Results[0].Affected != 1 {
		t.Fatalf("a delete of one row on a table with a trigger: %+v %v", res, err)
	}
	// The rule itself still holds, with the statement's own count in it.
	var change *ChangeError
	if _, err = apply(Change{Op: ChangeUpdate, Key: id("99"), Values: map[string]any{"v": "x"}}); !errors.As(err, &change) || !change.Conflict || change.Matched != 0 {
		t.Errorf("an update of no row = %v, want a conflict that matched none", err)
	}
	if _, err = apply(Change{Op: ChangeDelete, Key: id("3")}); !errors.As(err, &change) || !change.Conflict || change.Matched != 2 {
		t.Errorf("a delete of two rows = %v, want a conflict that matched two", err)
	}
	var rows, logged int
	if err := db.QueryRow(`SELECT COUNT(*) FROM jdwb_audited`).Scan(&rows); err != nil || rows != 3 {
		t.Errorf("rows = %d (%v), want the edited one and the two alike", rows, err)
	}
	// What the trigger wrote for the refused changes went with them.
	if err := db.QueryRow(`SELECT COUNT(*) FROM jdwb_audit_log`).Scan(&logged); err != nil || logged != 6 {
		t.Errorf("audit rows = %d (%v), want two each for the insert, the update and the delete that stood", logged, err)
	}
}

// An export of a table is the page of it, as a file: the same rows, read the
// same way. On Oracle the page asks the catalogue what each column is — a
// date compared with a filter has to be told how to read it, and an XMLTYPE
// cannot be read as SELECT * returns it — and an export that did not ask the
// same was refused for the filter (ORA-01861) and never came back from a
// table with an empty XML cell.
func TestLiveOracleExportReadsATableAsThePageDoes(t *testing.T) {
	e := workbenchEngines()[4]
	db, schema := openWorkbench(t, e)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	dropAfter(t, db, "jdwb_export")
	mustExec(t, db,
		`CREATE TABLE jdwb_export (id NUMBER(10) PRIMARY KEY, d DATE, x XMLTYPE)`,
		`INSERT INTO jdwb_export VALUES (1, DATE '2024-05-01', XMLTYPE('<a><b>1</b></a>'))`,
		`INSERT INTO jdwb_export VALUES (2, DATE '2024-06-01', NULL)`)

	page, err := BrowseTablePage(ctx, db, e.driver, BrowseOptions{Schema: schema, Table: "JDWB_EXPORT"})
	if err != nil {
		t.Fatalf("the page: %v", err)
	}
	// The date as the grid shows it, which is what a filter sends back.
	shown := fmt.Sprint(page.Rows[0][indexOf(page.Columns, "D")])

	var whole strings.Builder
	n, _, err := ExportSelection(ctx, db, e.driver, BrowseOptions{Schema: schema, Table: "JDWB_EXPORT"},
		ExportOptions{Format: ExportCSV}, &whole)
	if err != nil || n != 2 {
		t.Fatalf("the export of a table with a NULL in an XMLTYPE column: %d rows, %v", n, err)
	}
	if !strings.Contains(whole.String(), "<a><b>1</b></a>") {
		t.Errorf("the XML is not in the file as it was written:\n%s", whole.String())
	}

	var filtered strings.Builder
	n, _, err = ExportSelection(ctx, db, e.driver, BrowseOptions{
		Schema: schema, Table: "JDWB_EXPORT",
		Filters: []Filter{{Column: "D", Op: "eq", Value: shown}},
	}, ExportOptions{Format: ExportCSV}, &filtered)
	if err != nil || n != 1 {
		t.Fatalf("the export of the rows a date filter keeps (%q): %d rows, %v\n%s", shown, n, err, filtered.String())
	}
}

// go-ora names a result column by its wire type, which for several of Oracle's
// types says the wrong thing or nothing: a JSON column is a BLOB locator, a
// BOOLEAN a NUMBER, a BLOB a long raw. An XMLTYPE it reads laid out afresh,
// and a NULL one not at all — the statement fails or, with other columns in the
// row, waits — so one empty XML cell cost the page of the whole table. A
// table's cells are selected and typed from the catalogue instead. Needs
// Oracle 23ai (BOOLEAN).
func TestLiveOracleCellsAreTypedFromTheCatalogue(t *testing.T) {
	e := workbenchEngines()[4]
	db, schema := openWorkbench(t, e)
	ctx := context.Background()
	dropAfter(t, db, "jdwb_odd")
	mustExec(t, db,
		`CREATE TABLE jdwb_odd (id NUMBER(10) PRIMARY KEY, js JSON, x XMLTYPE, flag BOOLEAN, bl BLOB, f BINARY_DOUBLE, l LONG)`,
		// The BLOB's bytes spell a word, which is no reason to show it as one.
		`INSERT INTO jdwb_odd VALUES (1, JSON('{"a":1}'), XMLTYPE('<a><b>1</b></a>'), TRUE, UTL_RAW.CAST_TO_RAW('cafe'), 1.5, 'long text')`,
		`INSERT INTO jdwb_odd VALUES (2, NULL, NULL, FALSE, NULL, NULL, NULL)`)

	page, err := BrowseTablePage(ctx, db, e.driver, BrowseOptions{Schema: schema, Table: "JDWB_ODD"})
	if err != nil {
		t.Fatalf("the page of a table with a NULL in an XMLTYPE column: %v", err)
	}
	cell := func(row int, column string) any { return page.Rows[row][indexOf(page.Columns, column)] }
	kind := func(column string) string { return page.Kinds[indexOf(page.Columns, column)] }
	for column, want := range map[string]string{
		"ID": KindInteger, "JS": KindJSON, "X": KindText, "FLAG": KindBoolean, "BL": KindBinary, "F": KindFloat,
	} {
		if got := kind(column); got != want {
			t.Errorf("%s is called %q on the page, want %q", column, got, want)
		}
	}
	if cell(0, "JS") != `{"a":1}` || cell(0, "X") != `<a><b>1</b></a>` || cell(0, "FLAG") != true || cell(1, "FLAG") != false ||
		cell(0, "BL") != `\x63616665` || cell(0, "F") != 1.5 || cell(0, "L") != "long text" || cell(1, "JS") != nil || cell(1, "X") != nil {
		t.Errorf("rows = %#v", page.Rows)
	}

	// Written in the forms the grid sends, and handed back in the forms it shows.
	res, err := ApplyChanges(ctx, db, e.driver, ChangeSet{Schema: schema, Table: "JDWB_ODD", Changes: []Change{
		{Op: ChangeUpdate, Key: map[string]any{"ID": json.Number("2")}, Values: map[string]any{
			"JS": map[string]any{"b": []any{json.Number("1"), "two"}}, "X": "<n>new</n>", "FLAG": true,
			"BL": map[string]any{"$hex": "00ff"}, "F": json.Number("2.25"),
		}},
		{Op: ChangeUpdate, Key: map[string]any{"ID": json.Number("1")}, Values: map[string]any{"FLAG": false}},
	}})
	if err != nil {
		t.Fatalf("writing them: %v", err)
	}
	row := res.Results[0].Row
	if row["JS"] != `{"b":[1,"two"]}` || row["X"] != "<n>new</n>" || row["FLAG"] != true || row["BL"] != `\x00ff` || row["F"] != 2.25 {
		t.Errorf("the row that came back = %#v", row)
	}
	if res.Results[1].Row["FLAG"] != false {
		t.Errorf("a boolean set to false came back as %#v", res.Results[1].Row["FLAG"])
	}

	// One cell, whole: measured by the expression its type needs, then read.
	key := map[string]any{"ID": json.Number("1")}
	for column, want := range map[string]CellValue{
		"JS": {Kind: KindJSON, Encoding: "text", Value: `{"a":1}`, Size: 7},
		"X":  {Kind: KindText, Encoding: "text", Value: `<a><b>1</b></a>`, Size: 15},
		// A value that is not text has the size of its written form.
		"FLAG": {Kind: KindBoolean, Encoding: "json", Value: false, Size: 5},
		"BL":   {Kind: KindBinary, Encoding: "base64", Value: "Y2FmZQ==", Size: 4},
		"F":    {Kind: KindFloat, Encoding: "json", Value: 1.5, Size: 3},
	} {
		got, err := ReadCell(ctx, db, e.driver, schema, "JDWB_ODD", column, key)
		if err != nil {
			t.Errorf("cell %s: %v", column, err)
			continue
		}
		if got.Kind != want.Kind || got.Encoding != want.Encoding || got.Value != want.Value || got.Size != want.Size {
			t.Errorf("cell %s = %+v, want %+v", column, *got, want)
		}
	}
	// A LONG has no length to ask for, so it is not read whole.
	if _, err := ReadCell(ctx, db, e.driver, schema, "JDWB_ODD", "L", key); err == nil || !strings.Contains(err.Error(), "LONG") {
		t.Errorf("a LONG cell = %v, want it refused", err)
	}

	// Looking for a value reads the table's rows the same way. It used to ask
	// for * and wait on the row whose XML is NULL; the deadline is what a
	// relapse would run into.
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cols, err := ListColumns(ctx, db, e.driver, schema, "JDWB_ODD")
	if err != nil {
		t.Fatal(err)
	}
	found, err := searchColumns(bounded, db, mustDialectFor(e.driver), Table{Schema: schema, Name: "JDWB_ODD"}, cols, "NEW</n")
	if err != nil || len(found) != 1 || found[0].Column != "X" || found[0].Row["X"] != "<n>new</n>" || found[0].Row["FLAG"] != true {
		t.Errorf("a search of the table = %+v %v", found, err)
	}

	// The query editor has no catalogue to ask, only what the driver says. A
	// JSON document is still shown as its text and a BLOB still as hex.
	typed, err := RunStatement(ctx, db, e.driver, mustStatement(t, e.driver, `SELECT js, bl, f FROM jdwb_odd WHERE id = 1`), 10)
	if err != nil {
		t.Fatal(err)
	}
	if typed.Rows[0][0] != `{"a":1}` || typed.Rows[0][1] != `\x63616665` || typed.Kinds[1] != KindBinary || typed.Kinds[2] != KindFloat {
		t.Errorf("typed result = %#v kinds %v", typed.Rows, typed.Kinds)
	}
}

// Every Oracle type has its own way of being measured, and each expression is
// one Oracle has to accept: a wrong one fails the read rather than letting a
// large value through, but it fails it.
func TestLiveOracleMeasuresEveryType(t *testing.T) {
	e := workbenchEngines()[4]
	db, schema := openWorkbench(t, e)
	ctx := context.Background()
	dropAfter(t, db, "jdwb_sizes")
	mustExec(t, db,
		`CREATE TABLE jdwb_sizes (id NUMBER(10) PRIMARY KEY,
			s VARCHAR2(50), ns NVARCHAR2(50), c CHAR(5), n NUMBER, f BINARY_DOUBLE, r BINARY_FLOAT,
			dt DATE, ts TIMESTAMP, tstz TIMESTAMP WITH TIME ZONE, tsltz TIMESTAMP WITH LOCAL TIME ZONE,
			iym INTERVAL YEAR TO MONTH, ids INTERVAL DAY TO SECOND, bin RAW(16),
			cl CLOB, ncl NCLOB, bl BLOB, js JSON, x XMLTYPE)`,
		`INSERT INTO jdwb_sizes VALUES (1, 'héllo', N'héllo', 'ab', 12345.678, 1.5, 0.5,
			DATE '2024-01-02', TIMESTAMP '2024-01-02 03:04:05.123456', TIMESTAMP '2024-01-02 03:04:05 +02:00',
			TIMESTAMP '2024-01-02 03:04:05', INTERVAL '1-2' YEAR TO MONTH, INTERVAL '1 02:03:04' DAY TO SECOND,
			HEXTORAW('00FF'), 'clob é', N'nclob é', HEXTORAW('00FF10'), JSON('{"a":1}'), XMLTYPE('<a/>'))`)
	cols, err := ListColumns(ctx, db, e.driver, schema, "JDWB_SIZES")
	if err != nil || len(cols) != 19 {
		t.Fatalf("columns = %d %v", len(cols), err)
	}
	key := map[string]any{"ID": json.Number("1")}
	for _, col := range cols {
		cell, err := ReadCell(ctx, db, e.driver, schema, "JDWB_SIZES", col.Name, key)
		if err != nil {
			t.Errorf("%s (%s): %v", col.Name, col.Type, err)
			continue
		}
		if cell.Encoding == "null" || cell.Value == nil {
			t.Errorf("%s (%s) read as nothing: %+v", col.Name, col.Type, cell)
		}
	}
	for column, want := range map[string]string{"CL": "clob é", "NCL": "nclob é", "X": "<a/>", "S": "héllo"} {
		if cell, err := ReadCell(ctx, db, e.driver, schema, "JDWB_SIZES", column, key); err != nil || cell.Value != want {
			t.Errorf("%s = %+v %v, want %q", column, cell, err, want)
		}
	}
}

// The quote and comment rules the lexer applies to SQL Server and to Oracle
// are theirs: one statement here is one statement there, a backslash is an
// ordinary character in a string, and # and $ belong to names.
func TestLiveLexerAgreesWithSQLServerAndOracle(t *testing.T) {
	cases := map[Driver][]struct{ query, want string }{
		DriverMSSQL: {
			{"SELECT 'a;b' -- ; SELECT 2\n", "a;b"},
			{"SELECT 'it''s' --; SELECT 2\n", "it's"},
			{"SELECT [c;d] FROM (SELECT 'x' AS [c;d]) AS [t;u]", "x"},
			{"SELECT [a]]b] FROM (SELECT 'z' AS [a]]b]) AS t", "z"},
			{`SELECT "q;r" FROM (SELECT 'y' AS "q;r") AS t`, "y"},
			{`SELECT 'C:\dir\'`, `C:\dir\`},
			{"SELECT /* ; */ 'kept'", "kept"},
			{"SELECT N'ünï;code'", "ünï;code"},
			{"SELECT [serial#] FROM (SELECT 'h' AS [serial#]) AS t", "h"},
		},
		DriverOracle: {
			{"SELECT 'a;b' FROM dual -- ; SELECT 2\n", "a;b"},
			{"SELECT 'it''s' FROM dual --; SELECT 2\n", "it's"},
			{`SELECT "q;r" FROM (SELECT 'y' AS "q;r" FROM dual)`, "y"},
			{`SELECT 'C:\dir\' FROM dual`, `C:\dir\`},
			{"SELECT /* ; */ 'kept' FROM dual", "kept"},
			{"SELECT N'ünï;code' FROM dual", "ünï;code"},
			{`SELECT serial# FROM (SELECT 'h' AS serial# FROM dual)`, "h"},
			{`SELECT a$b FROM (SELECT 'd' AS a$b FROM dual)`, "d"},
			{"WITH q AS (SELECT 'w' AS v FROM dual) SELECT v FROM q", "w"},
			{"(SELECT 'p' FROM dual)", "p"},
		},
	}
	for _, e := range workbenchEngines()[3:] {
		t.Run(e.name, func(t *testing.T) {
			db, _ := openWorkbench(t, e)
			for _, c := range cases[e.driver] {
				statements, err := ParseScript(e.driver, c.query)
				if err != nil || len(statements) != 1 {
					t.Errorf("%q: split into %d (%v), want 1", c.query, len(statements), err)
					continue
				}
				if statements[0].Risk.Level != "read" {
					t.Errorf("%q is classified %s", c.query, statements[0].Risk.Level)
				}
				// Through the runner, so the read-only scope sees them too.
				res, err := RunStatement(context.Background(), db, e.driver, &statements[0], 10)
				if err != nil {
					t.Errorf("the server refused %q: %v", statements[0].SQL, err)
					continue
				}
				if res.RowCount != 1 || res.Rows[0][0] != c.want {
					t.Errorf("%q returned %v, want %q", statements[0].SQL, res.Rows, c.want)
				}
			}
		})
	}
}

// A date column is filtered by the text the grid shows it as, and by what a
// person types. Every engine but Oracle reads both into a date by itself;
// Oracle is told how, or the filter is ORA-01861 and the page is an error.
func TestLiveDateFilters(t *testing.T) {
	for _, e := range workbenchEngines() {
		t.Run(e.name, func(t *testing.T) {
			db, schema := openWorkbench(t, e)
			ctx := context.Background()
			dropAfter(t, db, "jdwb_dated")
			stamp := map[Driver]string{DriverMySQL: "DATETIME(6)", DriverMSSQL: "DATETIME2"}[e.driver]
			if stamp == "" {
				stamp = "TIMESTAMP"
			}
			mustExec(t, db, `CREATE TABLE jdwb_dated (id INT PRIMARY KEY, d DATE, ts `+stamp+`)`)
			at := func(text string) string {
				if e.driver == DriverOracle {
					return "TIMESTAMP '" + text + "'"
				}
				return "'" + text + "'"
			}
			insertRows(t, db, `INSERT INTO jdwb_dated`,
				`(1, `+at("2024-01-01 00:00:00")+`, `+at("2024-01-01 10:00:00.5")+`)`,
				`(2, `+at("2024-02-01 00:00:00")+`, `+at("2024-02-01 10:00:00.5")+`)`,
				`(3, `+at("2024-03-01 00:00:00")+`, `+at("2024-03-01 10:00:00.5")+`)`)
			table, id, d, ts := e.ident("jdwb_dated"), e.ident("id"), e.ident("d"), e.ident("ts")
			page, err := BrowseTablePage(ctx, db, e.driver, BrowseOptions{Schema: schema, Table: table})
			if err != nil || page.RowCount != 3 {
				t.Fatalf("page = %+v %v", page, err)
			}
			shown := func(row int, column string) string {
				return fmt.Sprint(page.Rows[row][indexOf(page.Columns, column)])
			}
			for _, c := range []struct {
				name   string
				filter Filter
				want   string
			}{
				{"a typed day", Filter{Column: d, Op: "gte", Value: "2024-02-01"}, "2,3"},
				{"a date as shown", Filter{Column: d, Op: "eq", Value: shown(1, d)}, "2"},
				{"a timestamp as shown", Filter{Column: ts, Op: "eq", Value: shown(1, ts)}, "2"},
				{"not that timestamp", Filter{Column: ts, Op: "ne", Value: shown(1, ts)}, "1,3"},
				{"before an RFC 3339 instant", Filter{Column: ts, Op: "lt", Value: "2024-02-01T10:00:00.5Z"}, "1"},
				{"between two forms", Filter{Column: ts, Op: "between", Values: []string{"2024-01-15T00:00:00Z", "2024-02-15 00:00:00"}}, "2"},
				{"in the dates shown", Filter{Column: d, Op: "in", Values: []string{shown(0, d), shown(2, d)}}, "1,3"},
				{"not in them", Filter{Column: d, Op: "not_in", Values: []string{shown(0, d), shown(2, d)}}, "2"},
				{"text inside a timestamp", Filter{Column: ts, Op: "contains", Value: "2024-02"}, "2"},
				{"text a date starts with", Filter{Column: d, Op: "prefix", Value: "2024-03"}, "3"},
			} {
				opts := BrowseOptions{Schema: schema, Table: table, Filters: []Filter{c.filter}, Columns: []string{id}, Sort: []SortKey{{Column: id}}}
				res, err := Browse(ctx, db, e.driver, opts)
				if err != nil {
					t.Errorf("%s: %v", c.name, err)
					continue
				}
				var ids []string
				for _, row := range res.Rows {
					ids = append(ids, fmt.Sprint(row[0]))
				}
				if got := strings.Join(ids, ","); got != c.want {
					t.Errorf("%s: ids = %q, want %q", c.name, got, c.want)
				}
				// The count beside the page is asked the same question.
				n, err := Count(ctx, db, e.driver, opts)
				if err != nil || int(n) != len(ids) {
					t.Errorf("%s: count = %d (%v) for a page of %d", c.name, n, err, len(ids))
				}
			}
		})
	}
}

// The statements a change set shows for review are the engine's own SQL: run
// as written, they do what applying the set does. A review that showed a
// statement the engine would refuse, or one that meant something else, would
// be a review of nothing.
func TestLiveReviewedStatementsAreTheEnginesOwnSQL(t *testing.T) {
	for _, e := range workbenchEngines() {
		t.Run(e.name, func(t *testing.T) {
			db, schema := openWorkbench(t, e)
			ctx := context.Background()
			dropAfter(t, db, "jdwb_review")
			stamp := map[Driver]string{DriverMySQL: "DATETIME(6)", DriverMSSQL: "DATETIME2"}[e.driver]
			if stamp == "" {
				stamp = "TIMESTAMP"
			}
			mustExec(t, db, `CREATE TABLE jdwb_review (id INT PRIMARY KEY, name VARCHAR(100), amount DECIMAL(20,4), data `+e.binary+`, seen `+stamp+`)`)
			// MySQL shows a DATETIME in its own form and takes that form back.
			first, second := "2024-01-02T03:04:05Z", "2024-06-07T08:09:10.5Z"
			if e.driver == DriverMySQL {
				first, second = "2024-01-02 03:04:05", "2024-06-07 08:09:10.5"
			}
			set := ChangeSet{Schema: schema, Table: e.ident("jdwb_review"), Changes: []Change{
				{Op: ChangeInsert, Values: e.row(map[string]any{
					"id": json.Number("1"), "name": `it's a "test" \ ünï`, "amount": json.Number("1234.5678"),
					"data": map[string]any{"$hex": "00ff10"}, "seen": first,
				})},
				{Op: ChangeInsert, Values: e.row(map[string]any{"id": json.Number("2"), "name": "gone", "seen": nil})},
				{Op: ChangeUpdate, Key: e.row(map[string]any{"id": json.Number("1"), "seen": first}),
					Values: e.row(map[string]any{"amount": json.Number("-0.5"), "seen": second})},
				{Op: ChangeDelete, Key: e.row(map[string]any{"id": json.Number("2"), "name": "gone", "seen": nil})},
			}}
			read := func() map[string]any {
				t.Helper()
				page, err := Browse(ctx, db, e.driver, BrowseOptions{Schema: schema, Table: e.ident("jdwb_review")})
				if err != nil || page.RowCount != 1 {
					t.Fatalf("the table holds %+v (%v), want the one row", page, err)
				}
				row, _ := rowObject(page, 0)
				return row
			}

			set.DryRun = true
			review, err := ApplyChanges(ctx, db, e.driver, set)
			if err != nil || review.Applied || len(review.Statements) != 4 {
				t.Fatalf("dry run = %+v %v", review, err)
			}
			for _, statement := range review.Statements {
				// The semicolon ends a statement in a script; a driver takes one
				// statement and Oracle's refuses the terminator.
				if _, err := db.Exec(strings.TrimSuffix(statement, ";")); err != nil {
					t.Fatalf("the engine refused a statement shown for review: %v\n%s", err, statement)
				}
			}
			written := read()
			if written[e.ident("name")] != `it's a "test" \ ünï` || written[e.ident("data")] != `\x00ff10` ||
				!sameNumber(fmt.Sprint(written[e.ident("amount")]), "-0.5") {
				t.Errorf("the row the reviewed statements wrote = %v", written)
			}

			mustExec(t, db, `DELETE FROM jdwb_review`)
			set.DryRun = false
			if _, err := ApplyChanges(ctx, db, e.driver, set); err != nil {
				t.Fatalf("applying the same set: %v", err)
			}
			if applied := read(); fmt.Sprint(applied) != fmt.Sprint(written) {
				t.Errorf("applied  %v\nreviewed %v", applied, written)
			}
		})
	}
}

// SQL Server names the transaction it sacrificed to a deadlock by number, not
// by SQLSTATE. It is the same request as a serialization failure — run it
// again — and a change set that lost one is run again.
func TestLiveSQLServerDeadlockVictimIsRetried(t *testing.T) {
	e := workbenchEngines()[3]
	db, schema := openWorkbench(t, e)
	ctx := context.Background()
	dropAfter(t, db, "jdwb_deadlock")
	mustExec(t, db,
		`CREATE TABLE jdwb_deadlock (id INT PRIMARY KEY, v VARCHAR(20))`,
		`INSERT INTO jdwb_deadlock VALUES (1, 'a'), (2, 'b')`)

	// The other transaction: it holds row 1, and is the one SQL Server keeps.
	rival, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rival.Close()
	for _, statement := range []string{
		`SET DEADLOCK_PRIORITY HIGH`, `BEGIN TRANSACTION`, `UPDATE jdwb_deadlock SET v = 'rival' WHERE id = 1`,
	} {
		if _, err := rival.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	defer rival.ExecContext(context.Background(), `IF @@TRANCOUNT > 0 ROLLBACK TRANSACTION`)

	type outcome struct {
		res *ChangeSetResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		// Row 2 first, then row 1: it takes one lock and waits for the other.
		res, err := ApplyChanges(ctx, db, e.driver, ChangeSet{Schema: schema, Table: "jdwb_deadlock", Changes: []Change{
			{Op: ChangeUpdate, Key: map[string]any{"id": json.Number("2")}, Values: map[string]any{"v": "set"}},
			{Op: ChangeUpdate, Key: map[string]any{"id": json.Number("1")}, Values: map[string]any{"v": "set"}},
		}})
		done <- outcome{res, err}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for blocked := 0; blocked == 0; {
		if err := db.QueryRow(`SELECT COUNT(*) FROM sys.dm_exec_requests WHERE blocking_session_id <> 0 AND database_id = DB_ID()`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("the change set never waited on the other transaction")
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Now the rival wants row 2: a cycle. The change set is the victim, the
	// rival's update goes through, and it commits.
	if _, err := rival.ExecContext(ctx, `UPDATE jdwb_deadlock SET v = 'rival' WHERE id = 2`); err != nil {
		t.Fatalf("the rival was the deadlock victim: %v", err)
	}
	if _, err := rival.ExecContext(ctx, `COMMIT TRANSACTION`); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("the change set was not run again after losing a deadlock: %v", got.err)
		}
		if got.res.Attempts < 2 || !got.res.Applied {
			t.Errorf("attempts = %d applied = %v, want a second attempt that applied", got.res.Attempts, got.res.Applied)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the change set did not return")
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM jdwb_deadlock WHERE v = 'set'`).Scan(&n); err != nil || n != 2 {
		t.Errorf("rows the change set wrote = %d (%v), want both", n, err)
	}
}

// What the catalogue says about an Oracle table, as the table editor reads it:
// a schema's own tables when none is named, and a column's length in the
// characters it was declared in.
func TestLiveOracleCatalogue(t *testing.T) {
	e := workbenchEngines()[4]
	db, schema := openWorkbench(t, e)
	ctx := context.Background()
	dropAfter(t, db, "jdwb_chars")
	mustExec(t, db, `CREATE TABLE jdwb_chars (id NUMBER(10) PRIMARY KEY,
		a VARCHAR2(20 CHAR), b VARCHAR2(20 BYTE), c NVARCHAR2(10), d CHAR(3 CHAR), e RAW(8), f NUMBER(12,3), g NUMBER)`,
		`INSERT INTO jdwb_chars (id, a) VALUES (1, 'x')`)

	cols, err := ListColumns(ctx, db, e.driver, "", "JDWB_CHARS")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range cols {
		got[c.Name] = c.Type
	}
	for name, want := range map[string]string{
		"ID": "NUMBER(10,0)", "A": "VARCHAR2(20)", "B": "VARCHAR2(20)", "C": "NVARCHAR2(10)", "D": "CHAR(3)",
		"E": "RAW(8)", "F": "NUMBER(12,3)", "G": "NUMBER",
	} {
		if got[name] != want {
			t.Errorf("%s is reported as %q, want %q", name, got[name], want)
		}
	}

	// With no schema named it is this account's tables and nobody else's.
	tables, err := ListTables(ctx, db, e.driver, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, table := range tables {
		found = found || table.Name == "JDWB_CHARS"
		if table.Schema != schema {
			t.Errorf("a listing with no schema named holds %s.%s", table.Schema, table.Name)
			break
		}
	}
	if !found {
		t.Error("the table just made is not in the listing")
	}

	d := mustDialectFor(e.driver)
	sizes, err := d.TableSizes(ctx, db, "")
	if err != nil {
		t.Fatalf("table sizes: %v", err)
	}
	found = false
	for _, size := range sizes {
		found = found || size.Table == "JDWB_CHARS"
	}
	if !found {
		t.Errorf("the table just made has no size row: %d rows", len(sizes))
	}

	// Sessions, each once: a statement has a row in v$sql per child cursor,
	// and the join used to list its session once for each. v$session needs an
	// account that may read it.
	admin := os.Getenv("JD_TEST_ORACLE_ADMIN_DSN")
	if admin == "" {
		t.Skip("set JD_TEST_ORACLE_ADMIN_DSN to check the session listing")
	}
	sessions, err := d.Activity(ctx, liveSQL(t, DriverOracle, "JD_TEST_ORACLE_ADMIN_DSN", admin))
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	seen := map[string]bool{}
	for _, session := range sessions {
		if seen[session.PID] {
			t.Errorf("session %s is listed twice", session.PID)
		}
		seen[session.PID] = true
	}
	if len(sessions) == 0 {
		t.Error("no session is listed, not even this one")
	}
}
