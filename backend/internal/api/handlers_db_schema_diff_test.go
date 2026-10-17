package api

import (
	"database/sql"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
)

// schemaDiffSecond adds a second SQLite connection to the router's server: the
// shop as it will be, with an orders table that has grown a column, a customers
// table that lost one, and a table of its own.
func schemaDiffSecond(t *testing.T, s *Server, name string) (id int64, db *sql.DB) {
	t.Helper()
	path := filepath.Join(s.Cfg.FileRoots[0], name+".db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, stmt := range []string{
		`CREATE TABLE customers (id INTEGER PRIMARY KEY AUTOINCREMENT, email TEXT NOT NULL UNIQUE)`,
		`CREATE TABLE orders (id INTEGER PRIMARY KEY, customer_id INTEGER NOT NULL REFERENCES customers(id) ON DELETE CASCADE, total REAL, note TEXT)`,
		`CREATE INDEX orders_customer ON orders(customer_id)`,
		`CREATE VIEW big_orders AS SELECT id, total FROM orders WHERE total > 100`,
		`CREATE TABLE refunds (id INTEGER PRIMARY KEY, order_id INTEGER REFERENCES orders(id), amount REAL NOT NULL)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%v\n%s", err, stmt)
		}
	}
	sealed, err := s.Sealer.Seal(path)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Store.DB.Exec(`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES(?,?,?,0)`,
		name, string(dbx.DriverSQLite), sealed)
	if err != nil {
		t.Fatal(err)
	}
	id, _ = res.LastInsertId()
	return id, db
}

func schemaDiffGet(t *testing.T, router http.Handler, id, to int64, extra string) (*http.Response, map[string]any) {
	t.Helper()
	rec, body := schemaSend(t, router, http.MethodGet, pathf("/databases/%d/schema-diff", id)+"?to="+strconv.FormatInt(to, 10)+extra, "")
	return rec.Result(), body
}

// Any role that may read a schema may compare two, a protected connection is
// compared like any other, and the comparison writes to neither database and
// leaves nothing on the audit trail: the statements it returns are text.
func TestSchemaDiffComparesTwoConnectionsAndChangesNeither(t *testing.T) {
	s, router, shop := schemaRouter(t, auth.RoleReadOnly)
	next, nextDB := schemaDiffSecond(t, s, "shop-next")
	if _, err := s.Store.DB.Exec(`UPDATE db_connections SET read_only = 1 WHERE name = 'shop'`); err != nil {
		t.Fatal(err)
	}

	res, body := schemaDiffGet(t, router, 1, next, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %v", res.StatusCode, body)
	}
	from, _ := body["from"].(map[string]any)
	to, _ := body["to"].(map[string]any)
	if from["name"] != "shop" || to["name"] != "shop-next" || from["driver"] != "sqlite" || from["schema"] != "main" {
		t.Errorf("sides = %v / %v", from, to)
	}
	objects, _ := body["objects"].([]any)
	seen := map[string]string{}
	for _, o := range objects {
		o := o.(map[string]any)
		seen[o["kind"].(string)+" "+o["name"].(string)] = o["change"].(string)
	}
	for key, change := range map[string]string{
		"table customers": "changed", "table orders": "changed", "table refunds": "added",
	} {
		if seen[key] != change {
			t.Errorf("%s = %q among %v", key, seen[key], seen)
		}
	}
	script, _ := body["script"].(string)
	for _, want := range []string{
		"-- Create table refunds\nCREATE TABLE refunds",
		"-- Add column note to orders\nALTER TABLE \"orders\" ADD COLUMN \"note\" TEXT;",
		"-- Drop column name from customers\n-- DESTRUCTIVE:",
		"DROP COLUMN \"name\";",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("the script lacks %q:\n%s", want, script)
		}
	}
	steps, _ := body["steps"].([]any)
	destructive := 0
	for _, st := range steps {
		if st.(map[string]any)["destructive"] == true {
			destructive++
		}
	}
	if destructive == 0 {
		t.Errorf("a dropped column is not marked destructive: %v", steps)
	}

	// Nothing was run, here or there, and nothing was recorded.
	for _, c := range []struct {
		db         *sql.DB
		table, col string
		want       int
	}{{shop, "customers", "name", 1}, {shop, "orders", "note", 0}, {nextDB, "customers", "name", 0}, {nextDB, "orders", "note", 1}} {
		var n int
		if err := c.db.QueryRow(`SELECT count(*) FROM pragma_table_info(?) WHERE name = ?`, c.table, c.col).Scan(&n); err != nil || n != c.want {
			t.Errorf("%s.%s present = %d, want %d (%v)", c.table, c.col, n, c.want, err)
		}
	}
	if schemaHas(t, shop, "table", "refunds") {
		t.Error("the comparison created the table it reports as missing")
	}
	var audited int
	if err := s.Store.DB.QueryRow(`SELECT count(*) FROM audit_log`).Scan(&audited); err != nil || audited != 0 {
		t.Errorf("a comparison left %d audit entries, %v", audited, err)
	}
}

func TestSchemaDiffOfTwoLikeSchemasIsEmpty(t *testing.T) {
	s, router, _ := schemaRouter(t, auth.RoleReadOnly)
	// A second connection to the same file is the same schema twice over.
	var dsn string
	if err := s.Store.DB.QueryRow(`SELECT dsn_enc FROM db_connections WHERE id = 1`).Scan(&dsn); err != nil {
		t.Fatal(err)
	}
	res, err := s.Store.DB.Exec(`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES('shop-again','sqlite',?,0)`, dsn)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := res.LastInsertId()
	response, body := schemaDiffGet(t, router, 1, again, "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %v", response.StatusCode, body)
	}
	if objects, _ := body["objects"].([]any); len(objects) != 0 {
		t.Errorf("one schema differs from itself: %v", objects)
	}
	if steps, _ := body["steps"].([]any); len(steps) != 0 || body["script"] != "" {
		t.Errorf("a migration for nothing: %v %q", steps, body["script"])
	}
}

func TestSchemaDiffRefusesWhatItCannotCompare(t *testing.T) {
	s, router, _ := schemaRouter(t, auth.RoleAdmin)
	addConnection := func(name, driver, address string) int64 {
		sealed, err := s.Sealer.Seal(address)
		if err != nil {
			t.Fatal(err)
		}
		res, err := s.Store.DB.Exec(`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES(?,?,?,0)`, name, driver, sealed)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	pg := addConnection("pg", "postgres", "postgres://u:p@127.0.0.1:1/db?sslmode=disable")
	redis := addConnection("cache", "redis", "redis://127.0.0.1:1/0")
	redisToo := addConnection("cache-too", "redis", "redis://127.0.0.1:1/1")

	for _, c := range []struct {
		name   string
		path   string
		status int
		code   string
		says   string
	}{
		{"no other side", pathf("/databases/%d/schema-diff", 1), http.StatusBadRequest, "bad_request", "name the connection"},
		{"a garbled other side", pathf("/databases/%d/schema-diff", 1) + "?to=two", http.StatusBadRequest, "bad_request", "name the connection"},
		{"a connection that is not there", pathf("/databases/%d/schema-diff", 1) + "?to=999", http.StatusNotFound, "not_found", ""},
		{"the same schema twice", pathf("/databases/%d/schema-diff", 1) + "?to=1", http.StatusBadRequest, "bad_request", "nothing to compare"},
		{"two engines", pathf("/databases/%d/schema-diff", 1) + "?to=" + strconv.FormatInt(pg, 10), http.StatusBadRequest, "bad_request", "the same kind of engine"},
		{"no schema", pathf("/databases/%d/schema-diff", redis) + "?to=" + strconv.FormatInt(redisToo, 10), http.StatusBadRequest, "bad_request", "no schema to compare"},
	} {
		rec, body := schemaSend(t, router, http.MethodGet, c.path, "")
		if rec.Code != c.status || schemaErrCode(body) != c.code || !strings.Contains(schemaErrMessage(body), c.says) {
			t.Errorf("%s: %d %s %q", c.name, rec.Code, schemaErrCode(body), schemaErrMessage(body))
		}
	}
}

// The route asks for no capability beyond reading, so a role that may only
// read gets the same answer as the operator.
func TestSchemaDiffIsOnTheReadSurface(t *testing.T) {
	for _, role := range []auth.Role{auth.RoleReadOnly, auth.RoleAdmin} {
		s, router, _ := schemaRouter(t, role)
		next, _ := schemaDiffSecond(t, s, "shop-next")
		if res, body := schemaDiffGet(t, router, 1, next, ""); res.StatusCode != http.StatusOK {
			t.Errorf("%v: status %d: %v", role, res.StatusCode, body)
		}
	}
}
