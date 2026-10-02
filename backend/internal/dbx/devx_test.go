package dbx

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// SQLite's half of the developer-experience surface, plus the pure renderers.
// The live tests cover the four server engines; these cover the one engine that
// needs no server, and the cases that are about the rendering rather than the
// server accepting it.

func TestSQLiteActivityIsUnsupported(t *testing.T) {
	db, _ := openTestDB(t)
	_, err := ListActivity(context.Background(), db, DriverSQLite)
	if !errors.Is(err, ErrNoActivityView) {
		t.Fatalf("ListActivity = %v, want ErrNoActivityView", err)
	}
	if err := KillQuery(context.Background(), db, DriverSQLite, "1"); !errors.Is(err, ErrNoActivityView) {
		t.Fatalf("KillQuery = %v, want ErrNoActivityView", err)
	}
}

func TestSQLiteStorageOverview(t *testing.T) {
	db, _ := openTestDB(t)
	ov, err := StorageOverview(context.Background(), db, DriverSQLite, "")
	if err != nil {
		t.Fatalf("StorageOverview: %v", err)
	}
	byName := map[string]TableSize{}
	for _, tb := range ov.Tables {
		byName[tb.Table] = tb
	}
	u, ok := byName["users"]
	if !ok {
		t.Fatalf("users missing from overview: %+v", ov.Tables)
	}
	// SQLite counts exactly rather than estimating, so this is an equality and
	// not a "roughly".
	if u.Rows != 2 {
		t.Errorf("users rows = %d, want 2", u.Rows)
	}
	if ov.TotalRows != 4 {
		t.Errorf("total rows = %d, want 4", ov.TotalRows)
	}
}

func TestSQLiteSearch(t *testing.T) {
	db, _ := openTestDB(t)
	ctx := context.Background()

	res, err := Search(ctx, db, DriverSQLite, "", "b@x.io")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Matches) != 1 {
		t.Fatalf("matches = %d, want 1: %+v", len(res.Matches), res.Matches)
	}
	m := res.Matches[0]
	if m.Table != "users" || m.Column != "email" {
		t.Errorf("match = %s.%s, want users.email", m.Table, m.Column)
	}
	if m.Row["id"] == nil {
		t.Errorf("match carries no row: %+v", m.Row)
	}

	// Matching on a numeric column is the case the text cast exists for.
	num, err := Search(ctx, db, DriverSQLite, "", "1")
	if err != nil {
		t.Fatalf("Search(numeric): %v", err)
	}
	if len(num.Matches) == 0 {
		t.Error("numeric search found nothing")
	}

	// An empty needle is refused rather than matching every row in the schema.
	if _, err := Search(ctx, db, DriverSQLite, "", "   "); err == nil {
		t.Error("empty search was accepted")
	}
}

func TestRowInsertSQLLiterals(t *testing.T) {
	cases := []struct {
		name string
		row  map[string]any
		want string
	}{
		{"null", map[string]any{"a": nil}, "NULL"},
		{"quote", map[string]any{"a": "it's"}, "'it''s'"},
		{"injection", map[string]any{"a": "'); DROP TABLE t; --"}, "'''); DROP TABLE t; --'"},
		{"int", map[string]any{"a": int64(7)}, "7"},
		{"float", map[string]any{"a": 1.5}, "1.5"},
		// PostgreSQL has a boolean type and will not take an integer for it.
		{"bool", map[string]any{"a": true}, "TRUE"},
		{"binary as the grid shows it", map[string]any{"a": `\x00ff`}, `'\x00ff'::bytea`},
		{"json", map[string]any{"a": map[string]any{"k": "v"}}, `'{"k":"v"}'`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := RowInsertSQL(DriverPostgres, "public", "t", c.row)
			if err != nil {
				t.Fatalf("RowInsertSQL: %v", err)
			}
			if !strings.Contains(got, "VALUES ("+c.want+")") {
				t.Errorf("got %q, want a VALUES of %s", got, c.want)
			}
		})
	}
}

// A column name is an identifier and goes through the same quoting every other
// generated statement uses, so a name carrying the quote character cannot
// close it early.
func TestRowInsertSQLQuotesIdentifiers(t *testing.T) {
	got, err := RowInsertSQL(DriverPostgres, "", `t"x`, map[string]any{`c"1`: "v"})
	if err != nil {
		t.Fatalf("RowInsertSQL: %v", err)
	}
	if !strings.Contains(got, `"t""x"`) || !strings.Contains(got, `"c""1"`) {
		t.Errorf("identifiers not doubled: %s", got)
	}
	if _, err := RowInsertSQL(DriverPostgres, "", "t", map[string]any{"a\x00b": "v"}); err == nil {
		t.Error("a NUL in a column name was accepted")
	}
}

// Column order is sorted rather than map order, so copying the same row twice
// produces the same text — otherwise the feature is useless in a diff.
func TestRowInsertSQLIsStable(t *testing.T) {
	row := map[string]any{"z": 1, "a": 2, "m": 3}
	first, err := RowInsertSQL(DriverSQLite, "", "t", row)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		again, err := RowInsertSQL(DriverSQLite, "", "t", row)
		if err != nil {
			t.Fatal(err)
		}
		if again != first {
			t.Fatalf("unstable output:\n%s\n%s", first, again)
		}
	}
}

func TestRowsInsertSQLRendersEachRow(t *testing.T) {
	out, err := RowsInsertSQL(DriverSQLite, "", "t", []map[string]any{
		{"a": 1}, {"a": 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out, "INSERT INTO"); n != 2 {
		t.Errorf("statements = %d, want 2:\n%s", n, out)
	}
}

// With the table's columns in hand the statement is the one a person would
// have written: the columns in the table's order, and a number as a number.
// The grid shows a 64-bit integer and a decimal as text, because a browser's
// number holds neither, and copied out as they arrived they were quoted.
func TestRowInsertSQLFollowsTheTable(t *testing.T) {
	columns := []Column{
		{Name: "id", Type: "bigint"}, {Name: "name", Type: "text"}, {Name: "price", Type: "numeric(12,2)"},
		{Name: "ratio", Type: "double precision"}, {Name: "ok", Type: "boolean"}, {Name: "payload", Type: "bytea"},
	}
	got, err := rowInsertSQL(DriverPostgres, "public", "customers", columns, map[string]any{
		"payload": `\xdeadbeef`, "ok": true, "ratio": "1.5e-3", "price": "12.50", "name": "42",
		"id": "9007199254740993", "extra": "kept",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `INSERT INTO "public"."customers" ("id", "name", "price", "ratio", "ok", "payload", "extra")` + "\n" +
		`VALUES (9007199254740993, '42', 12.50, 1.5e-3, TRUE, '\xdeadbeef'::bytea, 'kept');`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}

	// What is in a numeric column and is not a number an engine reads bare
	// keeps its quotes, and nothing a value holds can leave them.
	for _, c := range []struct {
		column Column
		value  any
		want   string
	}{
		{Column{Name: "a", Type: "numeric"}, "NaN", "'NaN'"},
		{Column{Name: "a", Type: "integer"}, "007; DROP TABLE t", "'007; DROP TABLE t'"},
		{Column{Name: "a", Type: "integer"}, "1 OR 1=1", "'1 OR 1=1'"},
		{Column{Name: "a", Type: "integer"}, "1.5", "'1.5'"},
		{Column{Name: "a", Type: "numeric(10,2)"}, "1e5", "'1e5'"},
		{Column{Name: "a", Type: "money"}, "$1,234.00", "'$1,234.00'"},
		{Column{Name: "a", Type: "integer"}, "", "''"},
		{Column{Name: "a", Type: "integer"}, "-12", "-12"},
		{Column{Name: "a", Type: "integer"}, nil, "NULL"},
		{Column{Name: "a", Type: "integer"}, json.Number("7"), "7"},
		{Column{Name: "a", Type: "text"}, "12", "'12'"},
	} {
		got, err := rowInsertSQL(DriverPostgres, "", "t", []Column{c.column}, map[string]any{"a": c.value})
		if err != nil || !strings.HasSuffix(got, "VALUES ("+c.want+");") {
			t.Errorf("%s %v = %q (%v), want a VALUES of %s", c.column.Type, c.value, got, err, c.want)
		}
	}

	// Each engine's own spelling of a number's type is read.
	for _, c := range []struct {
		driver Driver
		typ    string
	}{
		{DriverMySQL, "bigint unsigned"}, {DriverMySQL, "decimal(20,6)"}, {DriverMSSQL, "money"},
		{DriverOracle, "NUMBER(19,0)"}, {DriverOracle, "NUMBER"}, {DriverClickHouse, "Nullable(UInt64)"},
		{DriverClickHouse, "Decimal(18, 4)"}, {DriverSQLite, "INTEGER"},
	} {
		got, err := rowInsertSQL(c.driver, "", "t", []Column{{Name: "a", Type: c.typ}}, map[string]any{"a": "18446744073709551615"})
		if err != nil || !strings.HasSuffix(got, "VALUES (18446744073709551615);") {
			t.Errorf("%s %s = %q (%v), want the number bare", c.driver, c.typ, got, err)
		}
	}
}

// The catalogue is read from the server the row came from, and a server that
// cannot be asked leaves the statement as it has always been written.
func TestTableRowsInsertSQLReadsTheCatalogue(t *testing.T) {
	db := changeDB(t)
	ctx := context.Background()
	rows := []map[string]any{{"name": "Ann", "id": "1", "age": "31", "big": "9223372036854775807", "doc": nil}}

	got, err := TableRowsInsertSQL(ctx, db, DriverSQLite, "", "people", rows)
	if err != nil {
		t.Fatal(err)
	}
	if want := `INSERT INTO "people" ("id", "name", "age", "big", "doc")` + "\n" +
		`VALUES (1, 'Ann', 31, 9223372036854775807, NULL);`; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if _, err := db.Exec(`DELETE FROM people WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(got); err != nil {
		t.Errorf("the statement does not run on the engine it was written for: %v", err)
	}

	// Each case has its own expectation: a map is walked in no fixed order,
	// and one case's statement must not be what the other is held to.
	for name, db := range map[string]*sql.DB{"no pool": nil, "a table that is not there": db} {
		table := "people"
		if db != nil {
			table = "nonesuch"
		}
		plain, err := RowsInsertSQL(DriverSQLite, "", table, rows)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := TableRowsInsertSQL(ctx, db, DriverSQLite, "", table, rows); err != nil || got != plain {
			t.Errorf("%s: %q (%v), want %q", name, got, err, plain)
		}
	}
}

func TestORMTargetsAreAllValid(t *testing.T) {
	targets := ORMTargets()
	if len(targets) < 4 {
		t.Fatalf("targets = %v, want at least prisma, drizzle, typescript, zod", targets)
	}
	for _, tg := range targets {
		if !tg.Valid() {
			t.Errorf("ORMTargets() lists %q, which Valid() rejects", tg)
		}
	}
	if ORMTarget("nonsense").Valid() {
		t.Error("an unknown target was accepted")
	}
}

func TestGenerateTypeScriptAndZodFromSQLite(t *testing.T) {
	db, _ := openTestDB(t)
	ctx := context.Background()
	tables, err := ListTables(ctx, db, DriverSQLite, "")
	if err != nil {
		t.Fatal(err)
	}
	details := map[string]*TableDetail{}
	for _, tb := range tables {
		d, err := Detail(ctx, db, DriverSQLite, tb.Schema, tb.Name)
		if err != nil {
			t.Fatal(err)
		}
		details[tb.Name] = d
	}

	ts, err := GenerateORM(ORMTypeScript, DriverSQLite, tables, details)
	if err != nil {
		t.Fatalf("typescript: %v", err)
	}
	// A nullable column has to be optional in the type, or every read site
	// gets a false guarantee from the compiler.
	if !strings.Contains(ts, "name") || !strings.Contains(ts, "null") {
		t.Errorf("nullable column not marked nullable:\n%s", ts)
	}
	if !strings.Contains(ts, "export interface") {
		t.Errorf("no interface emitted:\n%s", ts)
	}

	zod, err := GenerateORM(ORMZod, DriverSQLite, tables, details)
	if err != nil {
		t.Fatalf("zod: %v", err)
	}
	if !strings.Contains(zod, "z.object({") {
		t.Errorf("no zod object emitted:\n%s", zod)
	}
	if !strings.Contains(zod, "nullable()") {
		t.Errorf("nullable column not marked nullable in zod:\n%s", zod)
	}
	// The insert variant is the point of generating zod at all: a row you are
	// about to create does not have the columns the database fills in.
	if !strings.Contains(zod, "InsertSchema") {
		t.Errorf("no insert variant emitted:\n%s", zod)
	}
}
