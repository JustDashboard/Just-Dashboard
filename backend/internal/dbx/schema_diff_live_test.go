package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The comparison and the migration it writes are proved the way a person would
// use them: two schemas are built on a real server, the first is compared with
// the second, every statement of the migration is run on the first, and the
// two are compared again. A migration is right when nothing is left, apart
// from what it said it could not do.

const (
	diffSchemaA = "jdt_schema_diff_a"
	diffSchemaB = "jdt_schema_diff_b"
)

type diffFixture struct {
	name   string
	driver Driver
	// open returns the connection each side is read from, and a function that
	// makes its schema empty. Both are one connection on the engines whose
	// schemas share a server.
	open func(t *testing.T) (a, b *sql.DB)
	// schemaA and schemaB name the schemas compared.
	schemaA, schemaB string
	// build makes the "from" and the "to" schema; the statements are run in
	// order and {s} is the schema's name.
	from, to []string
	// teardown empties the schema {s} is the name of, ignoring what is not there.
	teardown []string
	// leftover is what the migration cannot do and says so, as "kind name":
	// the objects still different afterwards. reverse is the same for the
	// migration back from the "to" schema to the "from" one.
	leftover, reverse []string
	// manual and reverseManual count the steps left to the operator.
	manual, reverseManual int
}

func (f diffFixture) run(t *testing.T) {
	for _, leg := range []struct {
		name     string
		reversed bool
		leftover []string
		manual   int
	}{{"from the first to the second", false, f.leftover, f.manual}, {"and back", true, f.reverse, f.reverseManual}} {
		t.Run(leg.name, func(t *testing.T) {
			a, b := f.open(t)
			ctx := context.Background()
			f.reset(t, a, b)
			t.Cleanup(func() { f.clean(a, b) })

			fromDB, toDB, fromSchema, toSchema := a, b, f.schemaA, f.schemaB
			if leg.reversed {
				fromDB, toDB, fromSchema, toSchema = b, a, f.schemaB, f.schemaA
			}
			flavor := IdentifySQL(ctx, fromDB, f.driver).Flavor
			read := func(db *sql.DB, schema string) *SchemaSnapshot {
				t.Helper()
				snap, err := ReadSchemaSnapshot(ctx, db, f.driver, flavor, schema)
				if err != nil {
					t.Fatalf("reading %s: %v", schema, err)
				}
				return snap
			}
			from, to := read(fromDB, fromSchema), read(toDB, toSchema)
			diff := DiffSchemas(from, to)
			if diff.Empty() {
				t.Fatal("the two schemas were built to differ and the comparison finds nothing")
			}
			migration := PlanMigration(ctx, fromDB, from, to, diff)
			t.Logf("differences: %s\nmigration:\n%s", describeObjects(diff.Objects), migration.Script())

			manual := 0
			for _, step := range migration.Steps {
				if step.Manual {
					manual++
					continue
				}
				for _, stmt := range step.Statements {
					if _, err := fromDB.ExecContext(ctx, stmt); err != nil {
						t.Fatalf("step %q failed: %v\n%s", step.Title, err, stmt)
					}
				}
			}
			if manual != leg.manual {
				t.Errorf("%d steps were left to the operator, want %d", manual, leg.manual)
			}

			after := DiffSchemas(read(fromDB, fromSchema), to)
			var left []string
			for _, o := range after.Objects {
				left = append(left, o.Kind+" "+o.Name)
			}
			sort.Strings(left)
			want := append([]string(nil), leg.leftover...)
			sort.Strings(want)
			if strings.Join(left, ",") != strings.Join(want, ",") {
				t.Errorf("still different after the migration: %v, want %v\n%s", left, want, describeObjects(after.Objects))
			}
		})
	}
}

func (f diffFixture) reset(t *testing.T, a, b *sql.DB) {
	t.Helper()
	f.clean(a, b)
	ctx := context.Background()
	for _, side := range []struct {
		db     *sql.DB
		schema string
		stmts  []string
	}{{a, f.schemaA, f.from}, {b, f.schemaB, f.to}} {
		for _, stmt := range side.stmts {
			stmt = strings.ReplaceAll(stmt, "{s}", side.schema)
			if _, err := side.db.ExecContext(ctx, stmt); err != nil {
				t.Fatalf("%v\n%s", err, stmt)
			}
		}
	}
}

func (f diffFixture) clean(a, b *sql.DB) {
	ctx := context.Background()
	for _, side := range []struct {
		db     *sql.DB
		schema string
	}{{a, f.schemaA}, {b, f.schemaB}} {
		for _, stmt := range f.teardown {
			_, _ = side.db.ExecContext(ctx, strings.ReplaceAll(stmt, "{s}", side.schema))
		}
	}
}

func describeObjects(objects []ObjectDiff) string {
	var b strings.Builder
	for _, o := range objects {
		fmt.Fprintf(&b, "  %s %s %s\n", o.Change, o.Kind, o.Name)
		for _, p := range o.Parts {
			fmt.Fprintf(&b, "      %s %s %s: %q -> %q\n", p.Change, p.Part, p.Name, p.From, p.To)
		}
	}
	return b.String()
}

func TestLivePostgresSchemaDiff(t *testing.T) {
	f := diffFixture{
		name: "postgres", driver: DriverPostgres, schemaA: diffSchemaA, schemaB: diffSchemaB,
		open: func(t *testing.T) (*sql.DB, *sql.DB) {
			db := catalogLive(t, DriverPostgres, "JD_TEST_POSTGRES_DSN")
			return db, db
		},
		teardown: []string{`DROP SCHEMA IF EXISTS {s} CASCADE`},
		from: []string{
			`CREATE SCHEMA {s}`,
			`CREATE TYPE {s}.mood AS ENUM ('sad', 'ok')`,
			`CREATE SEQUENCE {s}.ticket_seq START 10`,
			`CREATE TABLE {s}.users (id serial PRIMARY KEY, email varchar(120) NOT NULL, nickname text, mood {s}.mood DEFAULT 'ok',
			   age integer, CONSTRAINT users_email_key UNIQUE (email))`,
			`CREATE TABLE {s}.orders (id bigint PRIMARY KEY, user_id integer REFERENCES {s}.users(id), total numeric(10,2) NOT NULL DEFAULT 0, legacy text)`,
			`CREATE INDEX orders_user_idx ON {s}.orders (user_id)`,
			`CREATE TABLE {s}.audit_old (id integer PRIMARY KEY, note text)`,
			`CREATE VIEW {s}.v_users AS SELECT id, email FROM {s}.users`,
			`CREATE VIEW {s}.v_old AS SELECT id FROM {s}.audit_old`,
			`CREATE FUNCTION {s}.add_two(x integer) RETURNS integer LANGUAGE sql AS 'select x + 2'`,
			`CREATE FUNCTION {s}.old_fn() RETURNS integer LANGUAGE sql AS 'select 1'`,
		},
		to: []string{
			`CREATE SCHEMA {s}`,
			`CREATE TYPE {s}.mood AS ENUM ('sad', 'meh', 'ok')`,
			`CREATE SEQUENCE {s}.ticket_seq START 10`,
			`CREATE SEQUENCE {s}.invoice_seq`,
			`CREATE TABLE {s}.users (id serial PRIMARY KEY, email varchar(120) NOT NULL, nickname varchar(40) NOT NULL DEFAULT 'anon',
			   mood {s}.mood DEFAULT 'ok', created_at timestamptz DEFAULT now(), CONSTRAINT users_email_len CHECK (length(email) > 3))`,
			`CREATE TABLE {s}.orders (id bigint PRIMARY KEY, user_id integer REFERENCES {s}.users(id) ON DELETE CASCADE, total numeric(12,2) NOT NULL DEFAULT 0)`,
			`CREATE INDEX orders_user_idx ON {s}.orders (user_id, total) WHERE total > 0`,
			`CREATE TABLE {s}.payments (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, order_id bigint NOT NULL REFERENCES {s}.orders(id),
			   amount numeric(12,2) NOT NULL, note text GENERATED ALWAYS AS ('p' || id::text) STORED)`,
			`CREATE INDEX payments_order_idx ON {s}.payments (order_id)`,
			`CREATE VIEW {s}.v_users AS SELECT id, email, nickname FROM {s}.users`,
			`CREATE VIEW {s}.v_new AS SELECT id, total FROM {s}.orders`,
			`CREATE FUNCTION {s}.add_two(x integer) RETURNS integer LANGUAGE sql AS 'select x + 3'`,
			`CREATE FUNCTION {s}.new_fn(a text) RETURNS text LANGUAGE sql AS 'select upper(a)'`,
		},
		// A label cannot be taken out of an enum type.
		reverse: []string{"enum mood"}, reverseManual: 1,
	}
	f.run(t)
}

func TestLiveMariaDBAndMySQLSchemaDiff(t *testing.T) {
	for _, c := range []struct{ name, env string }{
		{"mariadb", "JD_TEST_MYSQL_ADMIN_DSN"}, {"mysql", "JD_TEST_MYSQL8_ADMIN_DSN"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := diffFixture{
				name: c.name, driver: DriverMySQL, schemaA: diffSchemaA, schemaB: diffSchemaB,
				open: func(t *testing.T) (*sql.DB, *sql.DB) {
					db := catalogLive(t, DriverMySQL, c.env)
					return db, db
				},
				teardown: []string{`DROP DATABASE IF EXISTS {s}`},
				from: []string{
					`CREATE DATABASE {s}`,
					`CREATE TABLE {s}.users (id int NOT NULL AUTO_INCREMENT PRIMARY KEY, email varchar(120) NOT NULL, nickname varchar(80),
					   mood enum('sad','ok') DEFAULT 'ok', age int, UNIQUE KEY users_email_key (email))`,
					`CREATE TABLE {s}.orders (id bigint NOT NULL PRIMARY KEY, user_id int, total decimal(10,2) NOT NULL DEFAULT 0, legacy text,
					   KEY orders_user_idx (user_id), CONSTRAINT orders_user_fk FOREIGN KEY (user_id) REFERENCES {s}.users(id))`,
					`CREATE TABLE {s}.audit_old (id int NOT NULL PRIMARY KEY, note text)`,
					`CREATE VIEW {s}.v_users AS SELECT id, email FROM {s}.users`,
					`CREATE VIEW {s}.v_old AS SELECT id FROM {s}.audit_old`,
					`CREATE FUNCTION {s}.add_two(x int) RETURNS int DETERMINISTIC RETURN x + 2`,
					`CREATE FUNCTION {s}.old_fn() RETURNS int DETERMINISTIC RETURN 1`,
				},
				to: []string{
					`CREATE DATABASE {s}`,
					`CREATE TABLE {s}.users (id int NOT NULL AUTO_INCREMENT PRIMARY KEY, email varchar(120) NOT NULL, nickname varchar(40) NOT NULL DEFAULT 'anon',
					   mood enum('sad','meh','ok') DEFAULT 'ok', created_at timestamp NULL DEFAULT CURRENT_TIMESTAMP,
					   CONSTRAINT users_email_len CHECK (char_length(email) > 3))`,
					`CREATE TABLE {s}.orders (id bigint NOT NULL PRIMARY KEY, user_id int, total decimal(12,2) NOT NULL DEFAULT 0,
					   KEY orders_user_idx (user_id, total), CONSTRAINT orders_user_fk FOREIGN KEY (user_id) REFERENCES {s}.users(id) ON DELETE CASCADE)`,
					`CREATE TABLE {s}.payments (id bigint NOT NULL AUTO_INCREMENT PRIMARY KEY, order_id bigint NOT NULL, amount decimal(12,2) NOT NULL,
					   note varchar(40) AS (concat('p', order_id)) STORED, KEY payments_order_idx (order_id),
					   CONSTRAINT payments_order_fk FOREIGN KEY (order_id) REFERENCES {s}.orders(id))`,
					`CREATE VIEW {s}.v_users AS SELECT id, email, nickname FROM {s}.users`,
					`CREATE VIEW {s}.v_new AS SELECT id, total FROM {s}.orders`,
					`CREATE FUNCTION {s}.add_two(x int) RETURNS int DETERMINISTIC RETURN x + 3`,
					`CREATE FUNCTION {s}.new_fn(a varchar(20)) RETURNS varchar(20) DETERMINISTIC RETURN upper(a)`,
				},
			}
			f.run(t)
		})
	}
}

func TestLiveSQLServerSchemaDiff(t *testing.T) {
	f := diffFixture{
		name: "sqlserver", driver: DriverMSSQL, schemaA: diffSchemaA, schemaB: diffSchemaB,
		open: func(t *testing.T) (*sql.DB, *sql.DB) {
			db := sqlServerLive(t)
			return db, db
		},
		// In the order the objects can go: what refers to a table before the table.
		teardown: []string{
			`DROP VIEW IF EXISTS {s}.v_users`, `DROP VIEW IF EXISTS {s}.v_old`, `DROP VIEW IF EXISTS {s}.v_new`,
			`DROP FUNCTION IF EXISTS {s}.add_two`, `DROP FUNCTION IF EXISTS {s}.new_fn`,
			`DROP PROCEDURE IF EXISTS {s}.old_proc`, `DROP PROCEDURE IF EXISTS {s}.new_proc`,
			`DROP TABLE IF EXISTS {s}.payments`, `DROP TABLE IF EXISTS {s}.orders`, `DROP TABLE IF EXISTS {s}.users`,
			`DROP TABLE IF EXISTS {s}.audit_old`, `DROP SEQUENCE IF EXISTS {s}.ticket_seq`, `DROP SEQUENCE IF EXISTS {s}.invoice_seq`,
			`DROP SCHEMA IF EXISTS {s}`,
		},
		from: []string{
			`CREATE SCHEMA {s}`,
			`CREATE SEQUENCE {s}.ticket_seq START WITH 10`,
			`CREATE TABLE {s}.users (id int IDENTITY(1,1) NOT NULL PRIMARY KEY, email nvarchar(120) NOT NULL, nickname nvarchar(80) NULL,
			   age int NULL, CONSTRAINT users_email_key UNIQUE (email))`,
			`CREATE TABLE {s}.orders (id bigint NOT NULL PRIMARY KEY, user_id int NULL, total decimal(10,2) NOT NULL DEFAULT 0, legacy nvarchar(max) NULL,
			   CONSTRAINT orders_user_fk FOREIGN KEY (user_id) REFERENCES {s}.users(id))`,
			`CREATE INDEX orders_user_idx ON {s}.orders (user_id)`,
			`CREATE TABLE {s}.audit_old (id int NOT NULL PRIMARY KEY, note nvarchar(max) NULL)`,
			`CREATE VIEW {s}.v_users AS SELECT id, email FROM {s}.users`,
			`CREATE VIEW {s}.v_old AS SELECT id FROM {s}.audit_old`,
			`CREATE FUNCTION {s}.add_two(@x int) RETURNS int AS BEGIN RETURN @x + 2 END`,
			`CREATE PROCEDURE {s}.old_proc AS SELECT 1 AS one`,
		},
		to: []string{
			`CREATE SCHEMA {s}`,
			`CREATE SEQUENCE {s}.ticket_seq START WITH 10`,
			`CREATE SEQUENCE {s}.invoice_seq`,
			`CREATE TABLE {s}.users (id int IDENTITY(1,1) NOT NULL PRIMARY KEY, email nvarchar(120) NOT NULL, nickname nvarchar(40) NOT NULL DEFAULT 'anon',
			   created_at datetime2 NULL DEFAULT SYSUTCDATETIME(), CONSTRAINT users_email_len CHECK (LEN(email) > 3))`,
			`CREATE TABLE {s}.orders (id bigint NOT NULL PRIMARY KEY, user_id int NULL, total decimal(12,2) NOT NULL DEFAULT 0,
			   CONSTRAINT orders_user_fk FOREIGN KEY (user_id) REFERENCES {s}.users(id) ON DELETE CASCADE)`,
			`CREATE INDEX orders_user_idx ON {s}.orders (user_id, total)`,
			`CREATE TABLE {s}.payments (id bigint IDENTITY(1,1) NOT NULL PRIMARY KEY, order_id bigint NOT NULL, amount decimal(12,2) NOT NULL,
			   note AS ('p' + CONVERT(varchar(20), id)) PERSISTED, CONSTRAINT payments_order_fk FOREIGN KEY (order_id) REFERENCES {s}.orders(id))`,
			`CREATE INDEX payments_order_idx ON {s}.payments (order_id)`,
			`CREATE VIEW {s}.v_users AS SELECT id, email, nickname FROM {s}.users`,
			`CREATE VIEW {s}.v_new AS SELECT id, total FROM {s}.orders`,
			`CREATE FUNCTION {s}.add_two(@x int) RETURNS int AS BEGIN RETURN @x + 3 END`,
			`CREATE FUNCTION {s}.new_fn(@a nvarchar(20)) RETURNS nvarchar(20) AS BEGIN RETURN UPPER(@a) END`,
			`CREATE PROCEDURE {s}.new_proc AS SELECT 2 AS two`,
		},
	}
	f.run(t)
}

func TestLiveSQLiteSchemaDiff(t *testing.T) {
	// Two files stand for two connections. SQLite cannot change a column or a
	// foreign key in place, so the migration says so for those and does the
	// rest.
	f := diffFixture{
		name: "sqlite", driver: DriverSQLite, schemaA: "main", schemaB: "main",
		teardown: nil,
		from: []string{
			`CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, email TEXT NOT NULL, nickname TEXT, age INTEGER, CONSTRAINT users_email_key UNIQUE (email))`,
			`CREATE TABLE orders (id INTEGER PRIMARY KEY, user_id INTEGER REFERENCES users(id), total REAL NOT NULL DEFAULT 0, legacy TEXT)`,
			`CREATE INDEX orders_user_idx ON orders (user_id)`,
			`CREATE TABLE audit_old (id INTEGER PRIMARY KEY, note TEXT)`,
			`CREATE VIEW v_users AS SELECT id, email FROM users`,
			`CREATE VIEW v_old AS SELECT id FROM audit_old`,
		},
		to: []string{
			`CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, email TEXT NOT NULL, nickname TEXT NOT NULL DEFAULT 'anon', created_at TEXT, CONSTRAINT users_email_key UNIQUE (email))`,
			`CREATE TABLE orders (id INTEGER PRIMARY KEY, user_id INTEGER REFERENCES users(id) ON DELETE CASCADE, total REAL NOT NULL DEFAULT 0)`,
			`CREATE INDEX orders_user_idx ON orders (user_id, total) WHERE total > 0`,
			`CREATE TABLE payments (id INTEGER PRIMARY KEY, order_id INTEGER NOT NULL REFERENCES orders(id), amount REAL NOT NULL)`,
			`CREATE INDEX payments_order_idx ON payments (order_id)`,
			`CREATE VIEW v_users AS SELECT id, email, nickname FROM users`,
			`CREATE VIEW v_new AS SELECT id, total FROM orders`,
		},
		// The default and nullability of a column, and a foreign key's action.
		leftover: []string{"table orders", "table users"}, manual: 2,
		reverse: []string{"table orders", "table users"}, reverseManual: 2,
	}
	f.open = func(t *testing.T) (*sql.DB, *sql.DB) {
		dir := t.TempDir()
		open := func(name string) *sql.DB {
			db, err := sql.Open("sqlite", filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			db.SetMaxOpenConns(1)
			t.Cleanup(func() { db.Close() })
			return db
		}
		return open("a.db"), open("b.db")
	}
	f.run(t)
}
