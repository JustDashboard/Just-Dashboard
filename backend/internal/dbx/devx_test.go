package dbx

import (
	"context"
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
