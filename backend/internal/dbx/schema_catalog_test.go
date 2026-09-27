package dbx

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"reflect"
	"slices"
	"sync/atomic"
	"testing"
)

type catalogConnector struct {
	source driver.Driver
	calls  *atomic.Int64
}

func (c catalogConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := c.source.Open(":memory:")
	if err != nil {
		return nil, err
	}
	return catalogConn{Conn: conn, calls: c.calls}, nil
}
func (c catalogConnector) Driver() driver.Driver { return c.source }

type catalogConn struct {
	driver.Conn
	calls *atomic.Int64
}

func (c catalogConn) Prepare(query string) (driver.Stmt, error) {
	c.calls.Add(1)
	return c.Conn.Prepare(query)
}
func (c catalogConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	c.calls.Add(1)
	if prepare, ok := c.Conn.(driver.ConnPrepareContext); ok {
		return prepare.PrepareContext(ctx, query)
	}
	return c.Conn.Prepare(query)
}

func TestSchemaCatalogQueryCountAndGraphCap(t *testing.T) {
	base, _ := sql.Open("sqlite", ":memory:")
	defer base.Close()
	for _, count := range []int{2, 500, 501} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			var calls atomic.Int64
			db := sql.OpenDB(catalogConnector{source: base.Driver(), calls: &calls})
			db.SetMaxOpenConns(1)
			defer db.Close()
			for i := range count {
				if _, err := db.Exec(fmt.Sprintf(`CREATE TABLE t%04d (id INTEGER PRIMARY KEY, value TEXT)`, i)); err != nil {
					t.Fatal(err)
				}
			}
			calls.Store(0)
			outline, err := Outline(t.Context(), db, DriverSQLite, "main")
			if err != nil || len(outline.Tables) != count || calls.Load() != int64(1+(count+499)/500) {
				t.Fatalf("outline tables=%v queries=%d err=%v", outline, calls.Load(), err)
			}
			calls.Store(0)
			graph, err := BuildSchemaGraph(t.Context(), db, DriverSQLite, "main")
			if err != nil || len(graph.Tables) != min(count, 120) || graph.Truncated != (count > 120) || calls.Load() != 5 {
				t.Fatalf("graph tables=%v queries=%d err=%v", graph, calls.Load(), err)
			}
		})
	}
}

func checkSchemaCatalog(t *testing.T, db *sql.DB, d Dialect, schema string) {
	t.Helper()
	tables, err := d.Tables(t.Context(), db, schema)
	if err != nil {
		t.Fatal(err)
	}
	got := withSchemaCatalog(t.Context(), db, d, tables, true)
	for _, table := range tables {
		key := catalogTable{table.Schema, table.Name}
		columns, ok := got.columns[key]
		if !ok {
			t.Fatalf("bulk columns fell back for %s", table.Name)
		}
		want, err := d.Columns(t.Context(), db, table.Schema, table.Name)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.EqualFunc(columns, want, func(a, b Column) bool { return a.Name == b.Name && a.Type == b.Type && a.Nullable == b.Nullable }) {
			t.Errorf("%s columns = %+v; want %+v", table.Name, columns, want)
		}
		pk, ok := got.primary[key]
		if !ok {
			t.Fatalf("bulk primary keys fell back for %s", table.Name)
		}
		wantPK, err := d.PrimaryKey(t.Context(), db, table.Schema, table.Name)
		if err != nil || !slices.Equal(pk, wantPK) {
			t.Errorf("%s PK = %v; want %v (%v)", table.Name, pk, wantPK, err)
		}
		indexes, ok := got.indexes[key]
		if !ok {
			t.Fatalf("bulk indexes fell back for %s", table.Name)
		}
		wantIX, err := d.Indexes(t.Context(), db, table.Schema, table.Name)
		unique := func(list []Index) map[string]bool {
			out := map[string]bool{}
			for _, ix := range list {
				if ix.Unique && len(ix.Columns) == 1 {
					out[ix.Columns[0]] = true
				}
			}
			return out
		}
		if err != nil || !reflect.DeepEqual(unique(indexes), unique(wantIX)) {
			t.Errorf("%s uniqueness differs: %+v / %+v (%v)", table.Name, indexes, wantIX, err)
		}
		foreign, ok := got.foreign[key]
		if !ok {
			t.Fatalf("bulk foreign keys fell back for %s", table.Name)
		}
		wantFK, err := d.ForeignKeys(t.Context(), db, table.Schema, table.Name)
		if err != nil || !slices.EqualFunc(foreign, wantFK, func(a, b ForeignKey) bool { return reflect.DeepEqual(a, b) }) {
			t.Errorf("%s foreign keys differ: %+v / %+v (%v)", table.Name, foreign, wantFK, err)
		}
	}
}

func TestSQLiteSchemaCatalogPreservesKeysViewsAndQuotedNames(t *testing.T) {
	db, _ := openTestDB(t)
	for _, statement := range []string{
		`CREATE TABLE "odd' name" (a INTEGER, b TEXT, PRIMARY KEY (b,a), UNIQUE(a,b))`,
		`CREATE TABLE child (a INTEGER, b TEXT, user_id INTEGER UNIQUE REFERENCES users ON DELETE CASCADE, FOREIGN KEY(b,a) REFERENCES "odd' name"(b,a))`,
		`CREATE UNIQUE INDEX expression_idx ON child(lower(b), a)`,
		`CREATE VIEW user_view AS SELECT id, email FROM users`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	checkSchemaCatalog(t, db, sqliteDialect{}, "main")
}

func TestLiveSchemaCatalogMatchesTableReads(t *testing.T) {
	for _, fixture := range sqlFixtures() {
		t.Run(string(fixture.driver), func(t *testing.T) {
			db := liveSQL(t, fixture.driver, fixture.env, fixture.dsn)
			setupFixture(t, db, fixture)
			d, _ := DialectFor(fixture.driver)
			checkSchemaCatalog(t, db, d, fixture.schema)
		})
	}
}

func TestSchemaCatalogRetainsUnreadableTables(t *testing.T) {
	db, _ := openTestDB(t)
	if _, err := db.Exec(`CREATE VIEW broken AS SELECT * FROM missing_table`); err != nil {
		t.Fatal(err)
	}
	outline, err := Outline(t.Context(), db, DriverSQLite, "main")
	if err != nil {
		t.Fatal(err)
	}
	if names, ok := outline.Tables["broken"]; !ok || len(names) != 0 {
		t.Fatalf("unreadable view lost: %v", outline.Tables)
	}
	if len(outline.Tables["users"]) != 3 {
		t.Fatal("failed bulk read hid readable columns")
	}
	graph, err := BuildSchemaGraph(t.Context(), db, DriverSQLite, "main")
	if err != nil || len(graph.Tables) != 3 || len(graph.Edges) != 1 {
		t.Fatalf("partial graph: %+v, %v", graph, err)
	}
}
