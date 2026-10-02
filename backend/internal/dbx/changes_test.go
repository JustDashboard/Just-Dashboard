package dbx

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// changeDB is a SQLite file with one table of each kind a change set meets: a
// keyed one, one with a two-column key, and one with no key at all.
func changeDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", sqliteDialect{}.NormaliseDSN(filepath.Join(t.TempDir(), "changes.db")))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	for _, s := range []string{
		`CREATE TABLE people (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			age INTEGER DEFAULT 18,
			big INTEGER,
			data BLOB,
			doc TEXT
		)`,
		`INSERT INTO people(id, name, age) VALUES (1, 'Ann', 31), (2, 'Bo', 24), (3, 'Cy', 40)`,
		`CREATE TABLE seats (room TEXT NOT NULL, seat INTEGER NOT NULL, holder TEXT, PRIMARY KEY (room, seat))`,
		`INSERT INTO seats VALUES ('a', 1, 'Ann'), ('a', 2, NULL), ('b', 1, 'Bo')`,
		`CREATE TABLE notes (msg TEXT, n INTEGER)`,
		`INSERT INTO notes VALUES ('one', 1), ('two', NULL), ('dup', 7), ('dup', 7)`,
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("seed: %v\n%s", err, s)
		}
	}
	return db
}

func rowCount(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func TestApplyChangesRunsTheSetInOrder(t *testing.T) {
	db := changeDB(t)
	res, err := ApplyChanges(context.Background(), db, DriverSQLite, ChangeSet{
		Table: "people",
		Changes: []Change{
			{Op: ChangeInsert, Values: map[string]any{"name": "Di"}},
			{Op: ChangeUpdate, Key: map[string]any{"id": json.Number("1")}, Values: map[string]any{"age": json.Number("32")}},
			{Op: ChangeDelete, Key: map[string]any{"id": json.Number("2")}},
			// A later change may depend on an earlier one in the same set.
			{Op: ChangeInsert, Values: map[string]any{"id": json.Number("2"), "name": "Bo again"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Applied || res.Attempts != 1 || len(res.Results) != 4 {
		t.Fatalf("result = %+v", res)
	}
	if len(res.KeyColumns) != 1 || res.KeyColumns[0] != "id" {
		t.Errorf("keyColumns = %v, want [id]", res.KeyColumns)
	}
	// The inserted row comes back with what the table filled in.
	if row := res.Results[0].Row; row == nil || row["age"] != "18" || row["name"] != "Di" || row["id"] != "4" {
		t.Errorf("inserted row = %v", res.Results[0].Row)
	}
	if row := res.Results[1].Row; row == nil || row["age"] != "32" || row["name"] != "Ann" {
		t.Errorf("updated row = %v", res.Results[1].Row)
	}
	if res.Results[2].Row != nil || res.Results[2].Affected != 1 {
		t.Errorf("delete outcome = %+v", res.Results[2])
	}
	// The statements are rendered for reading, values written in.
	want := []string{
		`INSERT INTO "people" ("name") VALUES ('Di');`,
		`UPDATE "people" SET "age" = 32 WHERE "id" = 1;`,
		`DELETE FROM "people" WHERE "id" = 2;`,
		`INSERT INTO "people" ("id", "name") VALUES (2, 'Bo again');`,
	}
	if !sameStrings(res.Statements, want) {
		t.Errorf("statements =\n%s\nwant\n%s", strings.Join(res.Statements, "\n"), strings.Join(want, "\n"))
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM people`); n != 4 {
		t.Errorf("rows = %d, want 4", n)
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM people WHERE id = 2 AND name = 'Bo again'`); n != 1 {
		t.Error("the delete and the insert that followed it did not both apply")
	}
}

// An update or a delete that does not touch exactly one row stops the set, and
// everything the set had already done is undone.
func TestApplyChangesRollsBackUnlessEachChangeTouchesOneRow(t *testing.T) {
	db := changeDB(t)
	ctx := context.Background()

	for _, c := range []struct {
		name    string
		change  Change
		matched int64
	}{
		{"an update of a row that is gone", Change{Op: ChangeUpdate, Key: map[string]any{"id": 99}, Values: map[string]any{"name": "x"}}, 0},
		{"a delete of a row that is gone", Change{Op: ChangeDelete, Key: map[string]any{"id": 99}}, 0},
		// The extra key column is the optimistic check: the row is there, but
		// it is no longer the row that was read.
		{"an update of a row that changed", Change{Op: ChangeUpdate, Key: map[string]any{"id": 1, "name": "Stale"}, Values: map[string]any{"age": 1}}, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := ApplyChanges(ctx, db, DriverSQLite, ChangeSet{Table: "people", Changes: []Change{
				{Op: ChangeInsert, Values: map[string]any{"name": "should not stay"}},
				{Op: ChangeDelete, Key: map[string]any{"id": 3}},
				c.change,
				{Op: ChangeDelete, Key: map[string]any{"id": 1}},
			}})
			var change *ChangeError
			if !errors.As(err, &change) {
				t.Fatalf("err = %v, want a ChangeError", err)
			}
			if !change.Conflict || change.Index != 2 || change.Matched != c.matched || change.Op != c.change.Op {
				t.Errorf("conflict = %+v", change)
			}
			if !strings.Contains(change.Error(), "change 3") || !strings.Contains(change.Error(), "nothing was applied") {
				t.Errorf("message = %q", change.Error())
			}
			if n := rowCount(t, db, `SELECT COUNT(*) FROM people`); n != 3 {
				t.Errorf("rows = %d: the earlier changes were not rolled back", n)
			}
			if n := rowCount(t, db, `SELECT COUNT(*) FROM people WHERE name = 'should not stay'`); n != 0 {
				t.Error("an insert from a failed set stayed")
			}
		})
	}
}

// A table with no primary key is edited by the row as it was read: every
// column, a NULL compared as a NULL, and still exactly one row or nothing.
func TestApplyChangesOnATableWithNoKey(t *testing.T) {
	db := changeDB(t)
	ctx := context.Background()

	res, err := ApplyChanges(ctx, db, DriverSQLite, ChangeSet{Table: "notes", Changes: []Change{
		{Op: ChangeUpdate, Key: map[string]any{"msg": "two", "n": nil}, Values: map[string]any{"n": 2}},
		{Op: ChangeDelete, Key: map[string]any{"msg": "one", "n": 1}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.KeyColumns) != 0 {
		t.Errorf("keyColumns = %v, want none", res.KeyColumns)
	}
	if res.Statements[0] != `UPDATE "notes" SET "n" = 2 WHERE "msg" = 'two' AND "n" IS NULL;` {
		t.Errorf("statement = %s", res.Statements[0])
	}
	if row := res.Results[0].Row; row == nil || row["n"] != "2" {
		t.Errorf("updated row = %v", res.Results[0].Row)
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM notes WHERE msg = 'two' AND n = 2`); n != 1 {
		t.Error("the row matched by a NULL was not updated")
	}

	// Two identical rows cannot be told apart, so neither is touched.
	_, err = ApplyChanges(ctx, db, DriverSQLite, ChangeSet{Table: "notes", Changes: []Change{
		{Op: ChangeDelete, Key: map[string]any{"msg": "dup", "n": 7}},
	}})
	var change *ChangeError
	if !errors.As(err, &change) || !change.Conflict || change.Matched != 2 {
		t.Fatalf("err = %v, want a conflict on 2 rows", err)
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM notes WHERE msg = 'dup'`); n != 2 {
		t.Errorf("duplicate rows left = %d, want both", n)
	}
	_, err = ApplyChanges(ctx, db, DriverSQLite, ChangeSet{Table: "notes", Changes: []Change{
		{Op: ChangeUpdate, Key: map[string]any{"msg": "dup"}, Values: map[string]any{"n": 8}},
	}})
	if !errors.As(err, &change) || !change.Conflict || change.Matched != 2 {
		t.Fatalf("err = %v, want a conflict on 2 rows", err)
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM notes WHERE n = 8`); n != 0 {
		t.Error("an update that matched two rows was kept")
	}
}

// The key is the table's, read from the catalogue. A request cannot choose a
// looser one.
func TestApplyChangesHoldsTheRequestToThePrimaryKey(t *testing.T) {
	db := changeDB(t)
	ctx := context.Background()
	for _, c := range []struct {
		name    string
		table   string
		change  Change
		message string
	}{
		{"a key that is not the primary key", "people",
			Change{Op: ChangeDelete, Key: map[string]any{"name": "Ann"}}, "primary key column"},
		{"half of a composite key", "seats",
			Change{Op: ChangeUpdate, Key: map[string]any{"room": "a"}, Values: map[string]any{"holder": "x"}}, "(room, seat)"},
		{"a null in the key", "people",
			Change{Op: ChangeDelete, Key: map[string]any{"id": nil}}, "cannot be null"},
		{"no key at all", "people",
			Change{Op: ChangeUpdate, Values: map[string]any{"name": "everyone"}}, "primary key"},
		{"a column the table does not have", "people",
			Change{Op: ChangeInsert, Values: map[string]any{`name") VALUES ('x'); DROP TABLE people; --`: "x"}}, "is not in this table"},
		{"a key column the table does not have", "people",
			Change{Op: ChangeDelete, Key: map[string]any{"id": 1, "1=1 OR id": 1}}, "is not in this table"},
		{"an unknown operation", "people", Change{Op: "upsert"}, "op must be"},
		{"an insert with a key", "people",
			Change{Op: ChangeInsert, Key: map[string]any{"id": 1}, Values: map[string]any{"name": "x"}}, "not a key"},
		{"a delete with values", "people",
			Change{Op: ChangeDelete, Key: map[string]any{"id": 1}, Values: map[string]any{"name": "x"}}, "not values"},
		{"an update with nothing to set", "people",
			Change{Op: ChangeUpdate, Key: map[string]any{"id": 1}}, "no column values"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := ApplyChanges(ctx, db, DriverSQLite, ChangeSet{Table: c.table, Changes: []Change{c.change}})
			var change *ChangeError
			if !errors.As(err, &change) || change.Conflict || change.Index != 0 {
				t.Fatalf("err = %v, want a refused change", err)
			}
			if !strings.Contains(err.Error(), c.message) {
				t.Errorf("message %q does not mention %q", err.Error(), c.message)
			}
		})
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM people`); n != 3 {
		t.Errorf("rows = %d: a refused change ran", n)
	}

	// The whole composite key works.
	if _, err := ApplyChanges(ctx, db, DriverSQLite, ChangeSet{Table: "seats", Changes: []Change{
		{Op: ChangeUpdate, Key: map[string]any{"room": "a", "seat": 2}, Values: map[string]any{"holder": "Cy"}},
	}}); err != nil {
		t.Fatalf("composite key: %v", err)
	}
}

func TestApplyChangesDryRunRendersAndTouchesNothing(t *testing.T) {
	db := changeDB(t)
	res, err := ApplyChanges(context.Background(), db, DriverSQLite, ChangeSet{
		Table: "people", DryRun: true,
		Changes: []Change{
			{Op: ChangeDelete, Key: map[string]any{"id": 1}},
			{Op: ChangeInsert, Values: map[string]any{"name": "O'Brien"}},
			{Op: ChangeInsert, Values: map[string]any{}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied || !res.DryRun || res.Attempts != 0 {
		t.Errorf("result = %+v", res)
	}
	want := []string{
		`DELETE FROM "people" WHERE "id" = 1;`,
		`INSERT INTO "people" ("name") VALUES ('O''Brien');`,
		`INSERT INTO "people" DEFAULT VALUES;`,
	}
	if !sameStrings(res.Statements, want) {
		t.Errorf("statements = %q", res.Statements)
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM people`); n != 3 {
		t.Errorf("rows = %d: a dry run changed the table", n)
	}
	// A dry run refuses what the real run would refuse.
	if _, err := ApplyChanges(context.Background(), db, DriverSQLite, ChangeSet{
		Table: "people", DryRun: true, Changes: []Change{{Op: ChangeDelete, Key: map[string]any{"name": "Ann"}}},
	}); err == nil {
		t.Error("a dry run accepted a key the real run refuses")
	}
}

// Values that JSON cannot carry as themselves arrive whole: a 64-bit integer,
// bytes, a document.
func TestApplyChangesKeepsExactAndBinaryValues(t *testing.T) {
	db := changeDB(t)
	ctx := context.Background()
	blob := []byte{0x00, 0xff, 0x10, 'a', 0x80}

	res, err := ApplyChanges(ctx, db, DriverSQLite, ChangeSet{Table: "people", Changes: []Change{
		{Op: ChangeInsert, Values: map[string]any{
			"id": json.Number("10"), "name": "exact",
			"big":  json.Number("9223372036854775807"),
			"data": map[string]any{"$base64": "AP8QYYA="},
			"doc":  map[string]any{"n": json.Number("9007199254740993"), "tags": []any{"a", "b"}},
		}},
		// And the form the grid shows a binary cell in goes straight back.
		{Op: ChangeUpdate, Key: map[string]any{"id": json.Number("1")}, Values: map[string]any{"data": `\x00ff106180`}},
		{Op: ChangeUpdate, Key: map[string]any{"id": json.Number("2")}, Values: map[string]any{"age": map[string]any{"$default": true}}},
	}})
	if err == nil {
		t.Fatal("SQLite cannot SET a column to DEFAULT; the change should have been refused")
	}
	res, err = ApplyChanges(ctx, db, DriverSQLite, ChangeSet{Table: "people", Changes: []Change{
		{Op: ChangeInsert, Values: map[string]any{
			"id": json.Number("10"), "name": "exact",
			"big":  json.Number("9223372036854775807"),
			"data": map[string]any{"$base64": "AP8QYYA="},
			"doc":  map[string]any{"n": json.Number("9007199254740993"), "tags": []any{"a", "b"}},
			// Left out of the statement, so the column's own default applies.
			"age": map[string]any{"$default": true},
		}},
		{Op: ChangeUpdate, Key: map[string]any{"id": json.Number("1")}, Values: map[string]any{"data": `\x00ff106180`}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	row := res.Results[0].Row
	if row["big"] != "9223372036854775807" || row["age"] != "18" || row["data"] != `\x00ff106180` {
		t.Errorf("returned row = %v", row)
	}
	var big int64
	var data, data1 []byte
	var doc string
	if err := db.QueryRow(`SELECT big, data, doc FROM people WHERE id = 10`).Scan(&big, &data, &doc); err != nil {
		t.Fatal(err)
	}
	if big != 9223372036854775807 {
		t.Errorf("big = %d", big)
	}
	if !bytes.Equal(data, blob) {
		t.Errorf("data = %x, want %x", data, blob)
	}
	if doc != `{"n":9007199254740993,"tags":["a","b"]}` {
		t.Errorf("doc = %s", doc)
	}
	if err := db.QueryRow(`SELECT data FROM people WHERE id = 1`).Scan(&data1); err != nil || !bytes.Equal(data1, blob) {
		t.Errorf("the grid's hex form stored %x (%v), want %x", data1, err, blob)
	}
	if !strings.Contains(res.Statements[0], `X'00FF106180'`) {
		t.Errorf("the rendered statement does not show the bytes: %s", res.Statements[0])
	}
}

func TestChangesAreRefusedWhereARowCannotBeIdentified(t *testing.T) {
	// No server is contacted: the refusal comes before anything is read.
	_, err := ApplyChanges(context.Background(), nil, DriverClickHouse, ChangeSet{
		Table: "events", Changes: []Change{{Op: ChangeDelete, Key: map[string]any{"id": 1}}},
	})
	if !errors.Is(err, ErrChangesUnsupported) {
		t.Fatalf("err = %v, want ErrChangesUnsupported", err)
	}
	if !strings.Contains(err.Error(), "sorting key") {
		t.Errorf("the refusal does not say why: %v", err)
	}
	if ChangesSupported(DriverClickHouse) || ChangesSupported(DriverMongo) || !ChangesSupported(DriverPostgres) {
		t.Error("ChangesSupported disagrees with ApplyChanges")
	}
	// The single-row routes go through the same door.
	if _, err := DeleteRow(context.Background(), nil, DriverClickHouse, "", "events", map[string]any{"id": 1}); !errors.Is(err, ErrChangesUnsupported) {
		t.Errorf("DeleteRow on ClickHouse = %v", err)
	}
}

func TestChangeSetsAreBounded(t *testing.T) {
	db := changeDB(t)
	changes := make([]Change, MaxChanges+1)
	for i := range changes {
		changes[i] = Change{Op: ChangeInsert, Values: map[string]any{"name": "x"}}
	}
	if _, err := ApplyChanges(context.Background(), db, DriverSQLite, ChangeSet{Table: "people", Changes: changes}); err == nil {
		t.Error("a set past the bound was accepted")
	}
	if _, err := ApplyChanges(context.Background(), db, DriverSQLite, ChangeSet{Table: "people"}); err == nil {
		t.Error("an empty set was accepted")
	}
	if _, err := ApplyChanges(context.Background(), db, DriverSQLite, ChangeSet{
		Table: "nowhere", Changes: changes[:1],
	}); err == nil || !strings.Contains(err.Error(), "was not found") {
		t.Errorf("a missing table = %v", err)
	}
}

// Statements are rendered in each engine's own spelling: its quotes, its
// placeholders, its literals, and a cast where a type has no equality.
func TestChangesRenderForEachEngine(t *testing.T) {
	plan := func(driver Driver, key []string, cols ...Column) *changePlan {
		d := mustDialect(t, driver)
		rel, err := qualify(d, "app", "t")
		if err != nil {
			t.Fatal(err)
		}
		p := &changePlan{dialect: d, rel: rel, columns: map[string]Column{}, key: key}
		for _, c := range cols {
			p.columns[c.Name] = c
		}
		return p
	}
	id, name, flag := Column{Name: "id", Type: "bigint"}, Column{Name: "name", Type: "text"}, Column{Name: "ok", Type: "boolean"}

	c, err := plan(DriverPostgres, []string{"id"}, id, name, flag).render(Change{
		Op: ChangeUpdate, Key: map[string]any{"id": json.Number("7")},
		Values: map[string]any{"name": `it's`, "ok": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.sql != `UPDATE "app"."t" SET "name" = $1, "ok" = $2 WHERE "id" = $3` {
		t.Errorf("postgres sql = %s", c.sql)
	}
	if c.rendered != `UPDATE "app"."t" SET "name" = 'it''s', "ok" = TRUE WHERE "id" = 7;` {
		t.Errorf("postgres rendered = %s", c.rendered)
	}
	if len(c.args) != 3 {
		t.Errorf("args = %v", c.args)
	}

	// MySQL: a backslash in a value is doubled in the rendering, or the text
	// shown for review would not be the statement it describes.
	c, err = plan(DriverMySQL, []string{"id"}, id, name).render(Change{
		Op: ChangeUpdate, Key: map[string]any{"id": json.Number("7")}, Values: map[string]any{"name": `a\`},
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.sql != "UPDATE `app`.`t` SET `name` = ? WHERE `id` = ?" || c.rendered != "UPDATE `app`.`t` SET `name` = 'a\\\\' WHERE `id` = 7;" {
		t.Errorf("mysql = %s | %s", c.sql, c.rendered)
	}
	if empty, err := plan(DriverMySQL, []string{"id"}, id).render(Change{Op: ChangeInsert}); err != nil || empty.sql != "INSERT INTO `app`.`t` () VALUES ()" {
		t.Errorf("mysql empty insert = %v %v", empty, err)
	}
	if _, err := plan(DriverOracle, []string{"id"}, id).render(Change{Op: ChangeInsert}); err == nil {
		t.Error("Oracle accepted an insert with no values")
	}

	// PostgreSQL has no = for json, so a keyless table's json column is
	// compared as text.
	c, err = plan(DriverPostgres, nil, name, Column{Name: "doc", Type: "json"}).render(Change{
		Op: ChangeDelete, Key: map[string]any{"name": "a", "doc": map[string]any{"k": json.Number("1")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.sql != `DELETE FROM "app"."t" WHERE CAST("doc" AS TEXT) = $1 AND "name" = $2` {
		t.Errorf("json key = %s", c.sql)
	}

	c, err = plan(DriverMSSQL, []string{"id"}, id, name).render(Change{
		Op: ChangeUpdate, Key: map[string]any{"id": 1}, Values: map[string]any{"name": map[string]any{"$default": true}},
	})
	if err != nil || c.sql != "UPDATE [app].[t] SET [name] = DEFAULT WHERE [id] = @p1" {
		t.Errorf("mssql default = %v %v", c, err)
	}

	// No value is the keyword, in the statement that runs as in the one shown,
	// and takes no bind marker: the markers that follow are numbered as if it
	// were not there. SQL Server's driver sends a bound NULL as an nvarchar,
	// which a varbinary column will not be converted from.
	payload := Column{Name: "payload", Type: "varbinary(max)"}
	c, err = plan(DriverMSSQL, []string{"id"}, id, name, payload).render(Change{
		Op: ChangeUpdate, Key: map[string]any{"id": 1}, Values: map[string]any{"name": "a", "payload": nil},
	})
	if err != nil || c.sql != "UPDATE [app].[t] SET [name] = @p1, [payload] = NULL WHERE [id] = @p2" ||
		c.rendered != "UPDATE [app].[t] SET [name] = N'a', [payload] = NULL WHERE [id] = 1;" || len(c.args) != 2 {
		t.Errorf("mssql null = %+v %v", c, err)
	}
	c, err = plan(DriverMSSQL, []string{"id"}, id, name, payload).render(Change{
		Op: ChangeInsert, Values: map[string]any{"id": 2, "name": nil, "payload": nil},
	})
	if err != nil || c.sql != "INSERT INTO [app].[t] ([id], [name], [payload]) VALUES (@p1, NULL, NULL)" ||
		c.rendered != "INSERT INTO [app].[t] ([id], [name], [payload]) VALUES (2, NULL, NULL);" || len(c.args) != 1 {
		t.Errorf("mssql null insert = %+v %v", c, err)
	}
}

// Oracle reads text into a date by the session's NLS format, and has no = for
// a LOB. Both are written into the statement around the bind marker — and
// around the literal in the statement shown for review — while the value
// itself is still bound.
func TestChangesRenderOraclesDatesAndLobs(t *testing.T) {
	d := oracleDialect{}
	p := &changePlan{dialect: d, rel: `"APP"."T"`, columns: map[string]Column{}, key: []string{"ID"}}
	for _, c := range []Column{
		{Name: "ID", Type: "NUMBER(10,0)"}, {Name: "D", Type: "DATE"}, {Name: "TS", Type: "TIMESTAMP(6)"},
		{Name: "TZ", Type: "TIMESTAMP(6) WITH TIME ZONE"}, {Name: "LTZ", Type: "TIMESTAMP(3) WITH LOCAL TIME ZONE"},
		{Name: "NAME", Type: "VARCHAR2(50)"}, {Name: "BODY", Type: "CLOB"}, {Name: "NBODY", Type: "NCLOB"},
		{Name: "RAWS", Type: "BLOB"}, {Name: "DOC", Type: "JSON"}, {Name: "X", Type: "XMLTYPE"},
	} {
		p.columns[c.Name] = c
	}

	c, err := p.render(Change{Op: ChangeUpdate, Key: map[string]any{"ID": json.Number("7")}, Values: map[string]any{
		"D": "2024-01-02T03:04:05Z", "TS": "2024-01-02T03:04:05.123456Z", "TZ": "2024-01-02T03:04:05.5+02:00",
		"LTZ": "2024-01-02 03:04:05", "NAME": "2024-01-02T03:04:05Z",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if want := `UPDATE "APP"."T" SET "D" = TO_DATE(:1, 'YYYY-MM-DD"T"HH24:MI:SS'), ` +
		`"LTZ" = TO_TIMESTAMP_TZ(:2, 'YYYY-MM-DD"T"HH24:MI:SS.FF9TZH:TZM'), "NAME" = :3, ` +
		`"TS" = TO_TIMESTAMP(:4, 'YYYY-MM-DD"T"HH24:MI:SS.FF9'), ` +
		`"TZ" = TO_TIMESTAMP_TZ(:5, 'YYYY-MM-DD"T"HH24:MI:SS.FF9TZH:TZM') WHERE "ID" = :6`; c.sql != want {
		t.Errorf("sql  = %s\nwant = %s", c.sql, want)
	}
	// A zoneless type takes the clock the grid showed; a zoned one keeps the
	// offset it was written with; text that is not a date column is untouched.
	wantArgs := []any{
		"2024-01-02T03:04:05", "2024-01-02T03:04:05.000000000+00:00", "2024-01-02T03:04:05Z",
		"2024-01-02T03:04:05.123456000", "2024-01-02T03:04:05.500000000+02:00", json.Number("7"),
	}
	if fmt.Sprint(c.args) != fmt.Sprint(wantArgs) {
		t.Errorf("args = %v\nwant   %v", c.args, wantArgs)
	}
	if !strings.Contains(c.rendered, `"D" = TO_DATE('2024-01-02T03:04:05', 'YYYY-MM-DD"T"HH24:MI:SS')`) ||
		!strings.Contains(c.rendered, `"NAME" = '2024-01-02T03:04:05Z'`) {
		t.Errorf("rendered = %s", c.rendered)
	}
	// What is not a date in any form this knows is left for Oracle to read,
	// and to refuse in its own words. No value at all is the keyword, with no
	// mask around it and nothing bound for it.
	c, err = p.render(Change{Op: ChangeInsert, Values: map[string]any{"ID": json.Number("1"), "D": "next tuesday", "TS": nil}})
	if err != nil || c.sql != `INSERT INTO "APP"."T" ("D", "ID", "TS") VALUES (:1, :2, NULL)` || len(c.args) != 2 {
		t.Errorf("an unreadable date = %v %v", c, err)
	}

	// No primary key: the row is the key, LOBs and all.
	p.key = []string{}
	c, err = p.render(Change{Op: ChangeDelete, Key: map[string]any{
		"BODY": "text", "NBODY": "ntext", "RAWS": `\x00ff`, "DOC": map[string]any{"a": json.Number("1")}, "X": "<a/>",
		"D": "2024-01-02T00:00:00Z", "NAME": nil,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if want := `DELETE FROM "APP"."T" WHERE DBMS_LOB.COMPARE("BODY", TO_CLOB(:1)) = 0 AND ` +
		`"D" = TO_DATE(:2, 'YYYY-MM-DD"T"HH24:MI:SS') AND JSON_EQUAL("DOC", :3) AND "NAME" IS NULL AND ` +
		`DBMS_LOB.COMPARE("NBODY", TO_NCLOB(:4)) = 0 AND DBMS_LOB.COMPARE("RAWS", TO_BLOB(:5)) = 0 AND ` +
		`DBMS_LOB.COMPARE(XMLSERIALIZE(CONTENT "X" AS CLOB NO INDENT), TO_CLOB(:6)) = 0`; c.sql != want {
		t.Errorf("sql  = %s\nwant = %s", c.sql, want)
	}
	if len(c.args) != 6 || fmt.Sprint(c.args[4]) != fmt.Sprint([]byte{0x00, 0xff}) || c.args[2] != `{"a":1}` {
		t.Errorf("args = %#v", c.args)
	}
	if !strings.Contains(c.rendered, `DBMS_LOB.COMPARE("BODY", TO_CLOB('text')) = 0`) || !strings.HasSuffix(c.rendered, ";") {
		t.Errorf("rendered = %s", c.rendered)
	}
}

// SQL Server is asked for the statement's own count, in the batch it runs in.
func TestSQLServerChangesAskForTheirOwnCount(t *testing.T) {
	counter, ok := mustDialect(t, DriverMSSQL).(ownRowCounter)
	if !ok {
		t.Fatal("SQL Server does not count its own rows")
	}
	if got := counter.countedSQL("DELETE FROM [t] WHERE [id] = @p1"); got != "DELETE FROM [t] WHERE [id] = @p1; SELECT @@ROWCOUNT AS jd_rows_touched" {
		t.Errorf("counted statement = %s", got)
	}
	for _, driver := range []Driver{DriverPostgres, DriverMySQL, DriverSQLite, DriverOracle} {
		if _, ok := mustDialect(t, driver).(ownRowCounter); ok {
			t.Errorf("%s is asked for a count its driver already reports", driver)
		}
	}
}

// The single-row functions the older routes call are a set of one, so they are
// held to the same key and the same one-row rule.
func TestSingleRowEditsAreHeldToOneRow(t *testing.T) {
	db := changeDB(t)
	ctx := context.Background()
	if _, err := UpdateRow(ctx, db, DriverSQLite, "", "people", map[string]any{"age": 1}, map[string]any{"name": "Ann"}); err == nil {
		t.Error("UpdateRow accepted a key that is not the primary key")
	}
	var change *ChangeError
	if _, err := DeleteRow(ctx, db, DriverSQLite, "", "people", map[string]any{"id": 99}); !errors.As(err, &change) || !change.Conflict {
		t.Errorf("DeleteRow of a missing row = %v, want a conflict", err)
	}
	res, err := UpdateRow(ctx, db, DriverSQLite, "", "people", map[string]any{"age": 50}, map[string]any{"id": 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Affected != 1 || res.RowCount != 1 || indexOf(res.Columns, "age") < 0 {
		t.Errorf("UpdateRow result = %+v", res)
	}
}
