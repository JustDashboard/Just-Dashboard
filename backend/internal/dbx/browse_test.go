package dbx

import (
	"context"
	"strings"
	"testing"
)

func TestBuildWhereOperators(t *testing.T) {
	d := mustDialect(t, DriverPostgres)
	clause, args, err := buildWhere(d, []Filter{
		{Column: "age", Op: "gte", Value: "18"},
		{Column: "name", Op: "contains", Value: "ann"},
		{Column: "deleted_at", Op: "is_null"},
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := ` WHERE "age" >= $1 AND CAST("name" AS TEXT) LIKE $2 AND "deleted_at" IS NULL`
	if clause != want {
		t.Errorf("clause = %q, want %q", clause, want)
	}
	// The null test binds nothing, so the numbering must not skip.
	if len(args) != 2 || args[0] != "18" || args[1] != "%ann%" {
		t.Errorf("args = %v, want [18 %%ann%%]", args)
	}
}

func TestBuildWhereRejectsUnknownOperator(t *testing.T) {
	// The operator set is closed. A filter is the only place a caller shapes the
	// WHERE clause rather than just its values, so an unlisted operator must be
	// refused rather than concatenated.
	if _, _, err := buildWhere(mustDialect(t, DriverPostgres),
		[]Filter{{Column: "id", Op: "; DROP TABLE users --", Value: "1"}}, 1); err == nil {
		t.Error("accepted an unknown filter operator")
	}
	if _, _, err := buildWhere(mustDialect(t, DriverPostgres),
		[]Filter{{Column: "id\x00", Op: "eq", Value: "1"}}, 1); err == nil {
		t.Error("accepted a filter column with a NUL byte")
	}
}

func TestPaginateShapes(t *testing.T) {
	cases := map[Driver]string{
		DriverPostgres: "LIMIT $1 OFFSET $2",
		DriverMySQL:    "LIMIT ? OFFSET ?",
		DriverSQLite:   "LIMIT ? OFFSET ?",
		// SQL Server and Oracle take offset before limit, and SQL Server needs
		// an ORDER BY in front of OFFSET at all.
		DriverMSSQL:  "ORDER BY (SELECT NULL) OFFSET @p1 ROWS FETCH NEXT @p2 ROWS ONLY",
		DriverOracle: "OFFSET :1 ROWS FETCH NEXT :2 ROWS ONLY",
	}
	for driver, want := range cases {
		d := mustDialect(t, driver)
		got, args := d.Paginate(50, 100, 1)
		if got != want {
			t.Errorf("Paginate(%s) = %q, want %q", driver, got, want)
		}
		// Whichever order the clause names them in, the args must match it.
		if driver == DriverMSSQL || driver == DriverOracle {
			if args[0] != 100 || args[1] != 50 {
				t.Errorf("Paginate(%s) args = %v, want [100 50]", driver, args)
			}
		} else if args[0] != 50 || args[1] != 100 {
			t.Errorf("Paginate(%s) args = %v, want [50 100]", driver, args)
		}
	}
}

func TestBrowseSortAndFilterOnSQLite(t *testing.T) {
	db, _ := openTestDB(t)
	ctx := context.Background()

	desc, err := Browse(ctx, db, DriverSQLite, BrowseOptions{
		Table: "users", Limit: 10, OrderBy: "id", Desc: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if desc.RowCount != 2 {
		t.Fatalf("rows = %d", desc.RowCount)
	}
	idIdx := indexOf(desc.Columns, "id")
	if got := desc.Rows[0][idIdx]; got != "2" {
		t.Errorf("descending sort put %v first, want 2", got)
	}

	filtered, err := Browse(ctx, db, DriverSQLite, BrowseOptions{
		Table: "users", Limit: 10,
		Filters: []Filter{{Column: "email", Op: "prefix", Value: "a@"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if filtered.RowCount != 1 {
		t.Errorf("prefix filter matched %d rows, want 1", filtered.RowCount)
	}

	// A "contains" against a numeric column must work, which is the whole
	// reason the column is cast to text first.
	numeric, err := Browse(ctx, db, DriverSQLite, BrowseOptions{
		Table: "users", Limit: 10,
		Filters: []Filter{{Column: "id", Op: "contains", Value: "2"}},
	})
	if err != nil {
		t.Fatalf("contains on a numeric column: %v", err)
	}
	if numeric.RowCount != 1 {
		t.Errorf("numeric contains matched %d rows, want 1", numeric.RowCount)
	}

	// A null test binds nothing and must still filter.
	nulls, err := Browse(ctx, db, DriverSQLite, BrowseOptions{
		Table: "users", Limit: 10,
		Filters: []Filter{{Column: "name", Op: "is_null"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if nulls.RowCount != 1 {
		t.Errorf("is_null matched %d rows, want 1", nulls.RowCount)
	}
}

func TestCountRespectsFilters(t *testing.T) {
	db, _ := openTestDB(t)
	ctx := context.Background()

	all, err := Count(ctx, db, DriverSQLite, BrowseOptions{Table: "users"})
	if err != nil {
		t.Fatal(err)
	}
	if all != 2 {
		t.Errorf("count = %d, want 2", all)
	}
	some, err := Count(ctx, db, DriverSQLite, BrowseOptions{
		Table: "users", Filters: []Filter{{Column: "name", Op: "not_null"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if some != 1 {
		t.Errorf("filtered count = %d, want 1", some)
	}
}

func TestBrowseRejectsBadSortColumn(t *testing.T) {
	db, _ := openTestDB(t)
	if _, err := Browse(context.Background(), db, DriverSQLite, BrowseOptions{
		Table: "users", Limit: 10, OrderBy: "id\x00; DROP TABLE users",
	}); err == nil {
		t.Error("accepted a sort column containing a NUL byte")
	}
}

func TestOutlineListsTablesAndColumns(t *testing.T) {
	db, _ := openTestDB(t)
	outline, err := Outline(context.Background(), db, DriverSQLite, "")
	if err != nil {
		t.Fatal(err)
	}
	cols, ok := outline.Tables["users"]
	if !ok {
		t.Fatalf("outline missing users: %v", outline.Tables)
	}
	if !contains(cols, "email") {
		t.Errorf("users columns = %v, want email among them", cols)
	}
}

func TestRelationsBuildsTheGraph(t *testing.T) {
	db, _ := openTestDB(t)
	rels, err := Relations(context.Background(), db, DriverSQLite, "")
	if err != nil {
		t.Fatal(err)
	}
	fks, ok := rels["posts"]
	if !ok || len(fks) == 0 {
		t.Fatalf("expected posts to have a foreign key, got %v", rels)
	}
	if fks[0].RefTable != "users" {
		t.Errorf("posts references %q, want users", fks[0].RefTable)
	}
}

func TestEveryDialectAgreesOnQualifiedNames(t *testing.T) {
	// Each engine quotes differently; what must hold everywhere is that the
	// schema and table both end up quoted and separated by a dot.
	for _, driver := range []Driver{DriverPostgres, DriverMySQL, DriverMSSQL, DriverOracle, DriverClickHouse} {
		d := mustDialect(t, driver)
		got, err := qualify(d, "app", "users")
		if err != nil {
			t.Fatalf("qualify(%s): %v", driver, err)
		}
		if !strings.Contains(got, ".") || strings.Contains(got, "app.users") {
			t.Errorf("qualify(%s) = %q, expected both parts quoted", driver, got)
		}
	}
	// SQLite has no schema worth qualifying, so it yields a bare table name.
	got, err := qualify(mustDialect(t, DriverSQLite), "main", "users")
	if err != nil {
		t.Fatal(err)
	}
	if got != `"users"` {
		t.Errorf("qualify(sqlite) = %q, want \"users\"", got)
	}
}

func indexOf(ss []string, s string) int {
	for i, v := range ss {
		if v == s {
			return i
		}
	}
	return -1
}

func contains(ss []string, s string) bool { return indexOf(ss, s) >= 0 }
