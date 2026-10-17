package assistant

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
)

// What a conversation is told about a real server: the structure its own
// catalogue reports, and not one of the values in it, the defaults and
// comments included. Each engine's variable names a server the test may make
// a schema or a database in, called jdt_ai_assistant_shop and dropped
// afterwards; without the variable the engine is skipped.

const liveSentinel = "jdt-ai-assistant-row-value"

type liveShop struct {
	name, env string
	driver    dbx.Driver
	// open makes the shop, takes it away again when the test ends, and hands
	// back a pool on it and the schema it is in.
	open func(ctx context.Context, t *testing.T, dsn string) (*sql.DB, string)
	// references is false where the engine has no foreign keys.
	references bool
}

func exec(ctx context.Context, t *testing.T, db *sql.DB, statements ...string) {
	t.Helper()
	for _, stmt := range statements {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%v\n%s", err, stmt)
		}
	}
}

// inDatabase makes the shop as a database of its own, for the engines whose
// schema is a database.
func inDatabase(driver dbx.Driver, tables ...string) func(ctx context.Context, t *testing.T, dsn string) (*sql.DB, string) {
	return func(ctx context.Context, t *testing.T, dsn string) (*sql.DB, string) {
		admin, err := dbx.OpenDatabase(ctx, driver, dsn, "")
		if err != nil {
			t.Skipf("not answering: %v", err)
		}
		t.Cleanup(func() { admin.Close() })
		if err := admin.PingContext(ctx); err != nil {
			t.Skipf("not answering: %v", err)
		}
		exec(ctx, t, admin, "DROP DATABASE IF EXISTS jdt_ai_assistant_shop", "CREATE DATABASE jdt_ai_assistant_shop")
		db, err := dbx.OpenDatabase(ctx, driver, dsn, "jdt_ai_assistant_shop")
		if err != nil {
			t.Fatal(err)
		}
		// Registered after the admin pool's own, so it runs first: the
		// database is dropped while that pool is still open, and after the
		// shop's pool has let its sessions go.
		t.Cleanup(func() {
			db.Close()
			_, _ = admin.ExecContext(context.Background(), "DROP DATABASE IF EXISTS jdt_ai_assistant_shop")
		})
		exec(ctx, t, db, tables...)
		return db, "jdt_ai_assistant_shop"
	}
}

var mysqlShop = []string{
	"CREATE TABLE jdt_ai_assistant_shop.customers (id BIGINT PRIMARY KEY, email VARCHAR(200) NOT NULL UNIQUE, note VARCHAR(100) DEFAULT '" + liveSentinel + "-default') COMMENT='" + liveSentinel + "-comment'",
	"CREATE TABLE jdt_ai_assistant_shop.orders (id BIGINT PRIMARY KEY, customer_id BIGINT NOT NULL, total DECIMAL(12,2), status VARCHAR(60) NOT NULL, FOREIGN KEY (customer_id) REFERENCES jdt_ai_assistant_shop.customers(id))",
	"INSERT INTO jdt_ai_assistant_shop.customers(id, email) VALUES (1, '" + liveSentinel + "@example.com')",
	"INSERT INTO jdt_ai_assistant_shop.orders VALUES (1, 1, 42.50, '" + liveSentinel + "')",
}

var liveShops = []liveShop{
	{
		name: "postgres", env: "JD_TEST_POSTGRES_DSN", driver: dbx.DriverPostgres, references: true,
		open: func(ctx context.Context, t *testing.T, dsn string) (*sql.DB, string) {
			db, err := dbx.OpenDatabase(ctx, dbx.DriverPostgres, dsn, "")
			if err != nil {
				t.Skipf("not answering: %v", err)
			}
			t.Cleanup(func() {
				_, _ = db.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS jdt_ai_assistant_shop CASCADE")
				db.Close()
			})
			if err := db.PingContext(ctx); err != nil {
				t.Skipf("not answering: %v", err)
			}
			exec(ctx, t, db,
				"DROP SCHEMA IF EXISTS jdt_ai_assistant_shop CASCADE",
				"CREATE SCHEMA jdt_ai_assistant_shop",
				"CREATE TABLE jdt_ai_assistant_shop.customers (id bigint PRIMARY KEY, email text NOT NULL UNIQUE, note text DEFAULT '"+liveSentinel+"-default')",
				"COMMENT ON TABLE jdt_ai_assistant_shop.customers IS '"+liveSentinel+"-comment'",
				"CREATE TABLE jdt_ai_assistant_shop.orders (id bigint PRIMARY KEY, customer_id bigint NOT NULL REFERENCES jdt_ai_assistant_shop.customers(id), total numeric(12,2), status text NOT NULL)",
				"CREATE VIEW jdt_ai_assistant_shop.shipped AS SELECT id, total FROM jdt_ai_assistant_shop.orders WHERE status = 'shipped'",
				"INSERT INTO jdt_ai_assistant_shop.customers(id, email) VALUES (1, '"+liveSentinel+"@example.com')",
				"INSERT INTO jdt_ai_assistant_shop.orders VALUES (1, 1, 42.50, '"+liveSentinel+"')",
			)
			return db, "jdt_ai_assistant_shop"
		},
	},
	{name: "mysql", env: "JD_TEST_MYSQL8_ADMIN_DSN", driver: dbx.DriverMySQL, references: true, open: inDatabase(dbx.DriverMySQL, mysqlShop...)},
	{name: "mariadb", env: "JD_TEST_MYSQL_ADMIN_DSN", driver: dbx.DriverMySQL, references: true, open: inDatabase(dbx.DriverMySQL, mysqlShop...)},
	{
		name: "clickhouse", env: "JD_TEST_CLICKHOUSE_DSN", driver: dbx.DriverClickHouse,
		open: inDatabase(dbx.DriverClickHouse,
			"CREATE TABLE jdt_ai_assistant_shop.customers (id UInt64, email String, note String DEFAULT '"+liveSentinel+"-default') ENGINE = MergeTree ORDER BY id COMMENT '"+liveSentinel+"-comment'",
			"CREATE TABLE jdt_ai_assistant_shop.orders (id UInt64, customer_id UInt64, total Decimal(12,2), status String) ENGINE = MergeTree ORDER BY id",
			"INSERT INTO jdt_ai_assistant_shop.customers (id, email) VALUES (1, '"+liveSentinel+"@example.com')",
			"INSERT INTO jdt_ai_assistant_shop.orders VALUES (1, 1, 42.50, '"+liveSentinel+"')",
		),
	},
	{
		name: "sqlserver", env: "JD_TEST_MSSQL_DSN", driver: dbx.DriverMSSQL, references: true,
		open: inDatabase(dbx.DriverMSSQL,
			"CREATE TABLE customers (id BIGINT PRIMARY KEY, email NVARCHAR(200) NOT NULL UNIQUE, note NVARCHAR(100) DEFAULT '"+liveSentinel+"-default')",
			"CREATE TABLE orders (id BIGINT PRIMARY KEY, customer_id BIGINT NOT NULL REFERENCES customers(id), total DECIMAL(12,2), status NVARCHAR(60) NOT NULL)",
			"INSERT INTO customers(id, email) VALUES (1, '"+liveSentinel+"@example.com')",
			"INSERT INTO orders VALUES (1, 1, 42.50, '"+liveSentinel+"')",
		),
	},
}

func TestLiveSchemaContextIsTheStructureAndNoValues(t *testing.T) {
	for _, shop := range liveShops {
		t.Run(shop.name, func(t *testing.T) {
			dsn := os.Getenv(shop.env)
			if dsn == "" {
				t.Skipf("%s unset", shop.env)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			db, schema := shop.open(ctx, t, dsn)
			if shop.driver == dbx.DriverMSSQL {
				// A SQL Server database's tables are in its dbo schema.
				schema = "dbo"
			}

			snapshot, err := ReadSchema(ctx, db, shop.driver, schema, "count the orders by customer")
			if err != nil {
				t.Fatal(err)
			}
			text, stats := SchemaText(snapshot, "count the orders by customer")
			prompt := SystemPrompt(shop.name, text, true)
			if strings.Contains(prompt, liveSentinel) {
				t.Fatalf("a value from inside the database reached the prompt:\n%s", prompt)
			}
			if stats.Described < 2 {
				t.Errorf("stats %+v\n%s", stats, text)
			}
			for _, want := range []string{"orders\n", "customers\n", "  customer_id ", "  email ", "  total "} {
				if !strings.Contains(text, want) {
					t.Errorf("the schema text lacks %q:\n%s", want, text)
				}
			}
			if shop.references && !strings.Contains(strings.ToLower(text), "references customers(id)") {
				t.Errorf("the reference from orders to customers is not said:\n%s", text)
			}
			if strings.Index(text, "orders\n") > strings.Index(text, "customers\n") {
				t.Errorf("the table the question named is not first:\n%s", text)
			}

			tables, missing := Describe(ctx, db, shop.driver, snapshot.Schema, []string{"ORDERS", "no_such_table"}, snapshot.Names)
			if len(tables) != 1 || len(tables[0].Columns) != 4 || len(missing) != 1 {
				t.Fatalf("described %+v, missing %v", tables, missing)
			}
			if described := DescribedText(tables, missing, snapshot.Schema); strings.Contains(described, liveSentinel) {
				t.Errorf("describing a table sent a value:\n%s", described)
			}
		})
	}
}
