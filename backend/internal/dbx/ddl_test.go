package dbx

import (
	"context"
	"strings"
	"testing"
)

func TestCreateTableSQL(t *testing.T) {
	got, err := CreateTableSQL(DriverPostgres, "public", "widgets", []NewColumn{
		{Name: "id", Type: "serial", PrimaryKey: true, NotNull: true},
		{Name: "name", Type: "varchar(255)", NotNull: true},
		{Name: "price", Type: "numeric(10,2)", Default: "0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`CREATE TABLE "public"."widgets"`,
		`"id" serial NOT NULL PRIMARY KEY`,
		`"name" varchar(255) NOT NULL`,
		`"price" numeric(10,2) DEFAULT 0`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestCreateTableCompositeKey(t *testing.T) {
	got, err := CreateTableSQL(DriverMySQL, "app", "memberships", []NewColumn{
		{Name: "user_id", Type: "int", PrimaryKey: true, NotNull: true},
		{Name: "group_id", Type: "int", PrimaryKey: true, NotNull: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	// A composite key must become a table constraint, never two inline PRIMARY
	// KEY clauses — which is a syntax error on every engine.
	if strings.Count(got, "PRIMARY KEY") != 1 {
		t.Errorf("expected exactly one PRIMARY KEY clause:\n%s", got)
	}
	if !strings.Contains(got, "PRIMARY KEY (`user_id`, `group_id`)") {
		t.Errorf("composite key not rendered as a constraint:\n%s", got)
	}
}

func TestDDLRejectsInjectedTypesAndDefaults(t *testing.T) {
	bad := []string{
		"int); DROP TABLE users;--",
		"text/**/",
		"varchar(255); DELETE FROM users",
		"int -- comment",
	}
	for _, typ := range bad {
		if _, err := CreateTableSQL(DriverPostgres, "public", "t", []NewColumn{
			{Name: "c", Type: typ},
		}); err == nil {
			t.Errorf("accepted dangerous type %q", typ)
		}
	}
	badDefaults := []string{"(SELECT 1)", "'a'||'b'", "1); DROP TABLE x;--"}
	for _, def := range badDefaults {
		if _, err := CreateTableSQL(DriverPostgres, "public", "t", []NewColumn{
			{Name: "c", Type: "int", Default: def},
		}); err == nil {
			t.Errorf("accepted dangerous default %q", def)
		}
	}
}

func TestDDLAcceptsRealTypes(t *testing.T) {
	ok := map[Driver][]string{
		DriverPostgres:   {"varchar(255)", "numeric(10,2)", "timestamp with time zone", "TEXT"},
		DriverMySQL:      {"varchar(255)", "int unsigned", "enum('a','b')", "TEXT"},
		DriverClickHouse: {"DateTime64(3)"},
		DriverOracle:     {"NUMBER(18,2)"},
	}
	for driver, types := range ok {
		for _, typ := range types {
			if err := validateType(driver, typ); err != nil {
				t.Errorf("%s rejected legitimate type %q: %v", driver, typ, err)
			}
		}
	}
	for _, def := range []string{"0", "-1.5", "'draft'", "NULL", "CURRENT_TIMESTAMP", "true"} {
		if err := validateDefault(def); err != nil {
			t.Errorf("rejected legitimate default %q: %v", def, err)
		}
	}
}

func TestClickHouseDeclinesDDL(t *testing.T) {
	// ClickHouse needs an engine and sorting key the form cannot choose, so it
	// must refuse rather than emit SQL the server will reject.
	if _, err := CreateTableSQL(DriverClickHouse, "default", "t", []NewColumn{
		{Name: "c", Type: "String"},
	}); err == nil {
		t.Error("expected ClickHouse to decline the generic create-table form")
	}
}

func TestDDLRoundTripOnSQLite(t *testing.T) {
	db, _ := openTestDB(t)
	ctx := context.Background()

	if _, err := CreateTable(ctx, db, DriverSQLite, "", "widgets", []NewColumn{
		{Name: "id", Type: "INTEGER", PrimaryKey: true, NotNull: true},
		{Name: "label", Type: "TEXT", NotNull: true, Default: "'none'"},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := AddColumn(ctx, db, DriverSQLite, "", "widgets",
		NewColumn{Name: "qty", Type: "INTEGER", Default: "0"}); err != nil {
		t.Fatalf("add column: %v", err)
	}
	if _, err := CreateIndex(ctx, db, DriverSQLite, "", "widgets", "idx_widgets_label",
		[]string{"label"}, false); err != nil {
		t.Fatalf("create index: %v", err)
	}

	d, err := Detail(ctx, db, DriverSQLite, "main", "widgets")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Columns) != 3 {
		t.Errorf("expected 3 columns after add, got %d", len(d.Columns))
	}
	found := false
	for _, ix := range d.Indexes {
		if ix.Name == "idx_widgets_label" {
			found = true
		}
	}
	if !found {
		t.Errorf("created index not reported back: %+v", d.Indexes)
	}

	if _, err := DropIndex(ctx, db, DriverSQLite, "", "widgets", "idx_widgets_label"); err != nil {
		t.Fatalf("drop index: %v", err)
	}
	if _, err := RenameTable(ctx, db, DriverSQLite, "", "widgets", "gadgets"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if _, err := TruncateTable(ctx, db, DriverSQLite, "", "gadgets"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if _, err := DropTable(ctx, db, DriverSQLite, "", "gadgets"); err != nil {
		t.Fatalf("drop: %v", err)
	}
}

func TestAddColumnRefusesUnsatisfiableNotNull(t *testing.T) {
	db, _ := openTestDB(t)
	// NOT NULL with no default cannot hold for rows that already exist; saying
	// so plainly beats relaying the engine's phrasing of the same thing.
	if _, err := AddColumn(context.Background(), db, DriverSQLite, "", "users",
		NewColumn{Name: "nickname", Type: "TEXT", NotNull: true}); err == nil {
		t.Error("expected NOT NULL with no default to be refused")
	}
}
