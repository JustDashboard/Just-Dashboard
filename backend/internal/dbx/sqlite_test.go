package dbx

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests run the real SQL paths against a real database. SQLite is the one
// engine available without a server or CGO (modernc.org/sqlite is pure Go), so
// it is what proves the generated statements actually execute — the introspec-
// tion queries, the form-driven mutations and the export path all run here for
// real rather than only being string-matched.

func openTestDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	db, err := sql.Open("sqlite", sqliteDialect{}.NormaliseDSN(path))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	stmts := []string{
		`CREATE TABLE users (
			id INTEGER PRIMARY KEY,
			email TEXT NOT NULL UNIQUE,
			name TEXT
		)`,
		`CREATE TABLE posts (
			id INTEGER PRIMARY KEY,
			title TEXT NOT NULL,
			author_id INTEGER NOT NULL REFERENCES users(id)
		)`,
		`CREATE INDEX idx_posts_author ON posts(author_id)`,
		`INSERT INTO users(id, email, name) VALUES (1, 'a@x.io', 'Ann'), (2, 'b@x.io', NULL)`,
		`INSERT INTO posts(id, title, author_id) VALUES (1, 'Hello', 1), (2, 'World', 2)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("seed %q: %v", s, err)
		}
	}
	return db, path
}

func TestSQLiteListTablesColumns(t *testing.T) {
	db, _ := openTestDB(t)
	ctx := context.Background()

	tables, err := ListTables(ctx, db, DriverSQLite, "")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tbl := range tables {
		names[tbl.Name] = true
		if tbl.Schema != "main" {
			t.Errorf("table %s schema = %q, want main", tbl.Name, tbl.Schema)
		}
	}
	if !names["users"] || !names["posts"] {
		t.Fatalf("missing expected tables: %v", names)
	}

	cols, err := ListColumns(ctx, db, DriverSQLite, "main", "users")
	if err != nil {
		t.Fatal(err)
	}
	if len(cols) != 3 {
		t.Fatalf("users columns = %d, want 3", len(cols))
	}
	if cols[0].Name != "id" || cols[0].Key != "PRI" {
		t.Errorf("first column = %+v, want id/PRI", cols[0])
	}
	// name is nullable, email is not.
	byName := map[string]Column{}
	for _, c := range cols {
		byName[c.Name] = c
	}
	if byName["email"].Nullable {
		t.Error("email should be NOT NULL")
	}
	if !byName["name"].Nullable {
		t.Error("name should be nullable")
	}
}

func TestSQLiteDetail(t *testing.T) {
	db, _ := openTestDB(t)
	ctx := context.Background()

	d, err := Detail(ctx, db, DriverSQLite, "main", "posts")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.PrimaryKey) != 1 || d.PrimaryKey[0] != "id" {
		t.Errorf("posts primary key = %v, want [id]", d.PrimaryKey)
	}
	if len(d.ForeignKeys) != 1 {
		t.Fatalf("posts foreign keys = %d, want 1", len(d.ForeignKeys))
	}
	fk := d.ForeignKeys[0]
	if fk.RefTable != "users" || len(fk.Columns) != 1 || fk.Columns[0] != "author_id" || fk.RefColumns[0] != "id" {
		t.Errorf("unexpected foreign key: %+v", fk)
	}
	if !strings.Contains(d.CreateSQL, "CREATE TABLE") {
		t.Errorf("createSql missing DDL: %q", d.CreateSQL)
	}

	du, err := Detail(ctx, db, DriverSQLite, "main", "users")
	if err != nil {
		t.Fatal(err)
	}
	// The unique index on email must surface (SQLite names an implicit one).
	foundUnique := false
	for _, ix := range du.Indexes {
		if ix.Unique && len(ix.Columns) == 1 && ix.Columns[0] == "email" {
			foundUnique = true
		}
	}
	if !foundUnique {
		t.Errorf("expected a unique index on email, got %+v", du.Indexes)
	}
}

func TestSQLiteBrowseAndMutate(t *testing.T) {
	db, _ := openTestDB(t)
	ctx := context.Background()

	res, err := BrowseTable(ctx, db, DriverSQLite, "main", "users", 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.RowCount != 2 {
		t.Fatalf("browse users rows = %d, want 2", res.RowCount)
	}

	// Insert through the form path.
	if _, err := InsertRow(ctx, db, DriverSQLite, "main", "users",
		map[string]any{"id": 3, "email": "c@x.io", "name": "Cy"}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	// Update it, scoped by primary key.
	if _, err := UpdateRow(ctx, db, DriverSQLite, "main", "users",
		map[string]any{"name": "Cyrus"}, map[string]any{"id": 3}); err != nil {
		t.Fatalf("update: %v", err)
	}
	var name string
	if err := db.QueryRow(`SELECT name FROM users WHERE id = 3`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "Cyrus" {
		t.Errorf("after update name = %q, want Cyrus", name)
	}
	// Delete it.
	if _, err := DeleteRow(ctx, db, DriverSQLite, "main", "users", map[string]any{"id": 3}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("after delete count = %d, want 2", n)
	}
}

func TestSQLiteExport(t *testing.T) {
	db, _ := openTestDB(t)
	ctx := context.Background()

	var csvBuf strings.Builder
	count, truncated, err := ExportTable(ctx, db, DriverSQLite, "main", "users", ExportCSV, &csvBuf, 0)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 || truncated {
		t.Errorf("csv export count=%d truncated=%v, want 2/false", count, truncated)
	}
	csv := csvBuf.String()
	if !strings.HasPrefix(csv, "id,email,name") {
		t.Errorf("csv header wrong: %q", csv)
	}
	if !strings.Contains(csv, "a@x.io") {
		t.Errorf("csv missing data: %q", csv)
	}

	var jsonBuf strings.Builder
	if _, _, err := ExportTable(ctx, db, DriverSQLite, "main", "posts", ExportJSON, &jsonBuf, 0); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(jsonBuf.String(), `"title"`) {
		t.Errorf("json export missing keys: %q", jsonBuf.String())
	}
}

// The export takes the view the grid is showing. Before it did, narrowing a
// table to one row and pressing Export as CSV downloaded every row in it —
// which looks exactly like a correct export until somebody opens the file.
func TestSQLiteExportTakesTheFiltersAndTheOrder(t *testing.T) {
	db, _ := openTestDB(t)
	ctx := context.Background()

	var filtered strings.Builder
	count, _, err := ExportBrowse(ctx, db, DriverSQLite, BrowseOptions{
		Schema: "main", Table: "users",
		Filters: []Filter{{Column: "email", Op: "eq", Value: "a@x.io"}},
	}, ExportCSV, &filtered, 0)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("filtered export wrote %d rows, want 1", count)
	}
	if body := filtered.String(); strings.Contains(body, "b@x.io") {
		t.Errorf("filtered export carried a row the filter excluded: %q", body)
	}

	var ordered strings.Builder
	if _, _, err := ExportBrowse(ctx, db, DriverSQLite, BrowseOptions{
		Schema: "main", Table: "users", OrderBy: "email", Desc: true,
	}, ExportCSV, &ordered, 0); err != nil {
		t.Fatal(err)
	}
	if first, second := strings.Index(ordered.String(), "b@x.io"), strings.Index(ordered.String(), "a@x.io"); first > second {
		t.Errorf("descending export is in ascending order: %q", ordered.String())
	}

	// A filter the operator could not have written is refused rather than
	// silently dropped, which would hand back the whole table again.
	if _, _, err := ExportBrowse(ctx, db, DriverSQLite, BrowseOptions{
		Schema: "main", Table: "users",
		Filters: []Filter{{Column: "email", Op: "; DROP TABLE users --", Value: "x"}},
	}, ExportCSV, &strings.Builder{}, 0); err == nil {
		t.Error("export accepted an unknown filter operator")
	}
}

// A catalogue that cannot say how many rows a table holds says so, rather than
// reporting zero and being read as "this table is empty".
func TestSQLiteTablesReportAnUnknownRowCount(t *testing.T) {
	db, _ := openTestDB(t)
	tables, err := ListTables(context.Background(), db, DriverSQLite, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) == 0 {
		t.Fatal("no tables listed")
	}
	for _, table := range tables {
		if table.Rows != -1 {
			t.Errorf("%s reports %d rows; SQLite keeps no estimate, so it must report -1",
				table.Name, table.Rows)
		}
	}
}

func TestSQLiteDumpRestore(t *testing.T) {
	db, path := openTestDB(t)
	db.Close() // release the handle so the dump reads a settled file
	ctx := context.Background()

	outDir := filepath.Join(filepath.Dir(path), "dumps")
	res, err := Dump(ctx, DriverSQLite, path, "", outDir)
	if err != nil {
		t.Fatalf("dump: %v", err)
	}
	if res.Size == 0 {
		t.Error("dump produced an empty file")
	}
	if _, err := os.Stat(res.Path); err != nil {
		t.Fatalf("dump file missing: %v", err)
	}

	// Restoring the dump over a fresh path should yield a queryable database.
	target := filepath.Join(filepath.Dir(path), "restored.db")
	if _, err := Restore(ctx, DriverSQLite, target, "", res.Path); err != nil {
		t.Fatalf("restore: %v", err)
	}
	rdb, err := sql.Open("sqlite", sqliteDialect{}.NormaliseDSN(target))
	if err != nil {
		t.Fatal(err)
	}
	defer rdb.Close()
	var n int
	if err := rdb.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		t.Fatalf("query restored db: %v", err)
	}
	if n != 2 {
		t.Errorf("restored user count = %d, want 2", n)
	}
}

func TestProbeSQLite(t *testing.T) {
	_, path := openTestDB(t)
	version, err := Probe(context.Background(), DriverSQLite, path)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if !strings.HasPrefix(version, "SQLite ") {
		t.Errorf("probe version = %q, want SQLite prefix", version)
	}
}
