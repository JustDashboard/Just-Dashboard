package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
)

// The routes that move data, over HTTP, against SQL Server, Oracle and MySQL:
// every export format, an upload imported each way it can be, the dashboard's
// own dump taken and loaded back as jobs, and a database copied where the
// engine has a second one to copy into.
//
// One suite runs against all three. What differs between them is how a table
// is created and a name is quoted; what a route answers is the same.
//
// None of it runs in a database somebody else is in. SQL Server gets a
// database made for the test, Oracle a user made for it, and MySQL the one
// database its connection string names plus two made beside it.

// transferEngine is one server the suite runs against.
type transferEngine struct {
	driver dbx.Driver
	// dsn is the connection the dashboard is given: the test's own database.
	dsn string
	// schema is what the routes take as the table's schema.
	schema string
	// quote spells a table's or a column's name the engine's way.
	quote func(string) string
	seed  []string
	// teardown drops what seed and the suite make; failures are ignored.
	teardown []string
	// adminDSN, when it differs from dsn, is a login that may create a
	// database, pointed at the same one. The copy and the restore into a new
	// database run through it.
	adminDSN string
	// newDatabase is whether the engine has a second database to restore or
	// copy into on the same connection.
	newDatabase bool
	// database is the name the connection's own database goes by.
	database string
}

// rel is a table as the routes write it: with its schema.
func (e transferEngine) rel(table string) string {
	return e.quote(e.schema) + "." + e.quote(table)
}

// elsewhere is the same table as a session in another database names it. On
// MySQL the schema is the database, so there the name goes bare.
func (e transferEngine) elsewhere(table string) string {
	if e.driver == dbx.DriverMySQL {
		return e.quote(table)
	}
	return e.rel(table)
}

// openOwnMSSQL makes a database for the test on the server the environment
// names: that connection string lands in master, which is everybody's.
func openOwnMSSQL(t *testing.T, name string) string {
	t.Helper()
	base := os.Getenv("JD_TEST_MSSQL_DSN")
	if base == "" {
		t.Skip("set JD_TEST_MSSQL_DSN to a SQL Server this test may create a database on")
	}
	ctx := context.Background()
	_, _ = dbx.DropDatabase(ctx, dbx.DriverMSSQL, base, name)
	if err := dbx.CreateDatabase(ctx, dbx.DriverMSSQL, base, name); err != nil {
		t.Skipf("SQL Server is unreachable, or this login cannot create a database: %v", err)
	}
	t.Cleanup(func() { _, _ = dbx.DropDatabase(context.Background(), dbx.DriverMSSQL, base, name) })
	return dbx.DSNForDatabase(dbx.DriverMSSQL, base, name)
}

// openOwnOracle makes a user for the test, with what an application's account
// ordinarily may do, and returns its connection string and schema.
func openOwnOracle(t *testing.T, name string) (string, string) {
	t.Helper()
	adminDSN := os.Getenv("JD_TEST_ORACLE_ADMIN_DSN")
	if adminDSN == "" {
		t.Skip("set JD_TEST_ORACLE_ADMIN_DSN to an Oracle account that may create a user for this test")
	}
	ctx := context.Background()
	admin, err := dbx.OpenDatabase(ctx, dbx.DriverOracle, adminDSN, "")
	if err != nil {
		t.Skipf("Oracle unreachable: %v", err)
	}
	schema := strings.ToUpper(name)
	const password = "jdtest"
	drop := func() {
		// A session still open as the user keeps it from being dropped.
		if rows, err := admin.Query(`SELECT sid, serial# FROM v$session WHERE username = :1`, schema); err == nil {
			var kills []string
			for rows.Next() {
				var sid, serial string
				if rows.Scan(&sid, &serial) == nil {
					kills = append(kills, "ALTER SYSTEM KILL SESSION '"+sid+","+serial+"' IMMEDIATE")
				}
			}
			rows.Close()
			for _, kill := range kills {
				_, _ = admin.Exec(kill)
			}
		}
		_, _ = admin.Exec(`DROP USER ` + schema + ` CASCADE`)
	}
	drop()
	for _, statement := range []string{
		`CREATE USER ` + schema + ` IDENTIFIED BY ` + password + ` QUOTA UNLIMITED ON USERS`,
		`GRANT CREATE SESSION, CREATE TABLE, CREATE VIEW, CREATE SEQUENCE TO ` + schema,
	} {
		if _, err := admin.Exec(statement); err != nil {
			admin.Close()
			t.Skipf("the administrator's account cannot make a user for this test: %v", err)
		}
	}
	t.Cleanup(func() {
		drop()
		admin.Close()
	})
	u, err := url.Parse(adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(schema, password)
	return u.String(), schema
}

func bracketQuote(name string) string  { return "[" + name + "]" }
func doubleQuote(name string) string   { return `"` + name + `"` }
func backtickQuote(name string) string { return "`" + name + "`" }

func TestLiveAPISQLServerTransfer(t *testing.T) {
	dsn := openOwnMSSQL(t, "jd_transfer_api")
	runTransferSuite(t, transferEngine{
		driver: dbx.DriverMSSQL, dsn: dsn, schema: "dbo", quote: bracketQuote,
		newDatabase: true, database: "jd_transfer_api",
		seed: []string{
			`CREATE TABLE dbo.jd_api_orders (
				id int NOT NULL PRIMARY KEY,
				customer nvarchar(100) NOT NULL CONSTRAINT jd_api_orders_customer UNIQUE,
				total decimal(10,2) NULL,
				placed datetime2(3) NULL,
				note nvarchar(max) NULL,
				raw varbinary(max) NULL
			)`,
			`CREATE TABLE dbo.jd_api_lines (
				id int IDENTITY(1,1) NOT NULL PRIMARY KEY,
				order_id int NOT NULL CONSTRAINT jd_api_lines_order REFERENCES dbo.jd_api_orders(id),
				sku nvarchar(40) NULL
			)`,
			`INSERT INTO dbo.jd_api_orders VALUES
				(1, N'ann', 10.50, '2026-03-04T05:06:07.123', N'it''s; "quoted", Zoë', 0x00FF41),
				(2, N'bo', NULL, NULL, NULL, NULL)`,
			`INSERT INTO dbo.jd_api_lines (order_id, sku) VALUES (1, N'a'), (1, N'b'), (2, N'c')`,
		},
	})
}

func TestLiveAPIOracleTransfer(t *testing.T) {
	dsn, schema := openOwnOracle(t, "jd_transfer_api")
	runTransferSuite(t, transferEngine{
		driver: dbx.DriverOracle, dsn: dsn, schema: schema, quote: doubleQuote,
		seed: []string{
			// Quoted, in lower case: that is how the dashboard addresses a
			// name, and how a table made through it is spelled.
			`CREATE TABLE "jd_api_orders" (
				"id" NUMBER(10) PRIMARY KEY,
				"customer" VARCHAR2(100) NOT NULL CONSTRAINT "jd_api_orders_customer" UNIQUE,
				"total" NUMBER(10,2),
				"placed" TIMESTAMP(3),
				"note" VARCHAR2(4000),
				"raw" BLOB
			)`,
			`CREATE TABLE "jd_api_lines" (
				"id" NUMBER GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
				"order_id" NUMBER(10) NOT NULL CONSTRAINT "jd_api_lines_order" REFERENCES "jd_api_orders"("id"),
				"sku" VARCHAR2(40)
			)`,
			`INSERT INTO "jd_api_orders" VALUES (1, 'ann', 10.50,
				TO_TIMESTAMP('2026-03-04 05:06:07.123', 'YYYY-MM-DD HH24:MI:SS.FF'), 'it''s; "quoted", Zoë', HEXTORAW('00FF41'))`,
			`INSERT INTO "jd_api_orders" VALUES (2, 'bo', NULL, NULL, NULL, NULL)`,
			`INSERT INTO "jd_api_lines" ("order_id", "sku") VALUES (1, 'a')`,
			`INSERT INTO "jd_api_lines" ("order_id", "sku") VALUES (1, 'b')`,
			`INSERT INTO "jd_api_lines" ("order_id", "sku") VALUES (2, 'c')`,
		},
	})
}

func TestLiveAPIMySQLTransfer(t *testing.T) {
	// The MySQL 8 fixture where there is one, and whatever MySQL the common
	// variable names where there is not.
	dsn := envOr("JD_TEST_MYSQL8_DSN", os.Getenv("JD_TEST_MYSQL_DSN"))
	admin := envOr("JD_TEST_MYSQL8_ADMIN_DSN", "")
	if os.Getenv("JD_TEST_MYSQL8_DSN") == "" {
		admin = os.Getenv("JD_TEST_MYSQL_ADMIN_DSN")
	}
	info, err := dbx.ParseDSN(dbx.DriverMySQL, dsn)
	if dsn == "" || err != nil || info.Database == "" {
		t.Skip("set JD_TEST_MYSQL8_DSN (or JD_TEST_MYSQL_DSN) to a MySQL database this test may replace the tables of")
	}
	e := transferEngine{
		driver: dbx.DriverMySQL, dsn: dsn, schema: info.Database, quote: backtickQuote,
		newDatabase: admin != "", database: info.Database,
		teardown: []string{`DROP TABLE IF EXISTS jd_api_new`, `DROP TABLE IF EXISTS jd_api_lines`, `DROP TABLE IF EXISTS jd_api_orders`},
		seed: []string{
			`CREATE TABLE jd_api_orders (
				id INT PRIMARY KEY,
				customer VARCHAR(100) NOT NULL,
				total DECIMAL(10,2),
				placed DATETIME(3),
				note TEXT,
				raw BLOB,
				UNIQUE KEY jd_api_orders_customer (customer)
			)`,
			`CREATE TABLE jd_api_lines (
				id INT AUTO_INCREMENT PRIMARY KEY,
				order_id INT NOT NULL,
				sku VARCHAR(40),
				CONSTRAINT jd_api_lines_order FOREIGN KEY (order_id) REFERENCES jd_api_orders(id)
			)`,
			`INSERT INTO jd_api_orders VALUES
				(1, 'ann', 10.50, '2026-03-04 05:06:07.123', 'it''s; "quoted", Zoë', 0x00FF41),
				(2, 'bo', NULL, NULL, NULL, NULL)`,
			`INSERT INTO jd_api_lines (order_id, sku) VALUES (1, 'a'), (1, 'b'), (2, 'c')`,
		},
	}
	if admin != "" {
		e.adminDSN = dbx.DSNForDatabase(dbx.DriverMySQL, admin, info.Database)
	}
	runTransferSuite(t, e)
}

// addLiveConnection saves one more connection on a test server and returns
// its id.
func addLiveConnection(t *testing.T, s *Server, name string, driver dbx.Driver, dsn string) int64 {
	t.Helper()
	sealed, err := s.Sealer.Seal(dsn)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Store.DB.Exec(
		`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES(?,?,?,?)`,
		name, string(driver), sealed, 0)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func runTransferSuite(t *testing.T, e transferEngine) {
	s, r, id := liveAPIRouter(t, e.driver, e.dsn)
	s.Cfg.BackupLocalDir = t.TempDir()
	ctx := context.Background()
	direct, err := dbx.OpenDatabase(ctx, e.driver, e.dsn, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := func(statements ...string) {
		t.Helper()
		for _, statement := range statements {
			if _, err := direct.ExecContext(ctx, statement); err != nil {
				t.Fatalf("%v\n%s", err, statement)
			}
		}
	}
	teardown := func() {
		for _, statement := range e.teardown {
			_, _ = direct.ExecContext(context.Background(), statement)
		}
	}
	teardown()
	// Closed after the last thing that uses it, which is the teardown.
	t.Cleanup(func() {
		teardown()
		direct.Close()
	})
	exec(e.seed...)

	orders, lines := e.rel("jd_api_orders"), e.rel("jd_api_lines")
	col := e.quote
	count := func(rel string) int {
		t.Helper()
		var n int
		if err := direct.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+rel).Scan(&n); err != nil {
			t.Fatalf("counting %s: %v", rel, err)
		}
		return n
	}
	text := func(query string) string {
		t.Helper()
		var out sql.NullString
		if err := direct.QueryRowContext(ctx, query).Scan(&out); err != nil {
			t.Fatalf("%v\n%s", err, query)
		}
		return out.String
	}
	const note = `it's; "quoted", Zoë`
	path := func(suffix string) string { return pathf("/databases/%d"+suffix, id) }
	table := "?schema=" + url.QueryEscape(e.schema) + "&table=jd_api_orders"
	upload := func(options, filename, content string) (*httptest.ResponseRecorder, dbx.ImportReport) {
		t.Helper()
		rec := liveUpload(t, r, path("/import/upload"), options, filename, []byte(content))
		var report dbx.ImportReport
		_ = json.Unmarshal(rec.Body.Bytes(), &report)
		return rec, report
	}
	into := func(table, rest string) string {
		return `{"schema":` + jsonString(e.schema) + `,"table":"` + table + `"` + rest + `}`
	}

	// --- export ---------------------------------------------------------

	t.Run("export_in_every_format", func(t *testing.T) {
		get := func(format, extra string) (*http.Response, string) {
			t.Helper()
			rec := do(t, r, http.MethodGet, path("/export")+table+"&orderBy=id&format="+format+extra, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("%s export: %d %s", format, rec.Code, rec.Body.String())
			}
			return rec.Result(), rec.Body.String()
		}
		res, csv := get("csv", "")
		if res.Trailer.Get(exportStatusTrailer) != "complete" || res.Trailer.Get(exportRowsTrailer) != "2" {
			t.Errorf("csv trailers = %v", res.Trailer)
		}
		csvLines := strings.Split(strings.TrimSpace(csv), "\n")
		if len(csvLines) != 3 || csvLines[0] != "id,customer,total,placed,note,raw" {
			t.Fatalf("csv:\n%s", csv)
		}
		// The whole of a binary value, a timestamp as an instant, and a text
		// with every character CSV has to quote.
		for _, want := range []string{`1,ann,10.5`, `05:06:07.123`, `"it's; ""quoted"", Zoë"`, `\x00ff41`} {
			if !strings.Contains(csvLines[1], want) {
				t.Errorf("the first row has no %s: %s", want, csvLines[1])
			}
		}
		if csvLines[2] != "2,bo,,,," {
			t.Errorf("a row of NULLs is %q", csvLines[2])
		}

		_, tsv := get("tsv", "")
		if !strings.HasPrefix(tsv, "id\tcustomer\ttotal\tplaced\tnote\traw\n") || !strings.Contains(tsv, "2\tbo\t\t\t\t\n") {
			t.Errorf("tsv:\n%s", tsv)
		}

		_, body := get("json", "")
		var objects []map[string]any
		if err := json.Unmarshal([]byte(body), &objects); err != nil || len(objects) != 2 {
			t.Fatalf("json: %v\n%s", err, body)
		}
		if objects[0]["customer"] != "ann" || objects[0]["note"] != note || objects[0]["raw"] != `\x00ff41` ||
			objects[1]["total"] != nil || objects[1]["note"] != nil {
			t.Errorf("json rows = %v", objects)
		}
		// Keys in the table's column order, not the alphabet's.
		if at := strings.Index(body, `"id"`); at < 0 || at > strings.Index(body, `"customer"`) || strings.Index(body, `"customer"`) > strings.Index(body, `"raw"`) {
			t.Errorf("json keys are out of column order:\n%s", body)
		}

		res, ndjson := get("ndjson", "")
		rows := strings.Split(strings.TrimSpace(ndjson), "\n")
		if len(rows) != 2 || res.Header.Get("Content-Type") != "application/x-ndjson" {
			t.Fatalf("ndjson (%s):\n%s", res.Header.Get("Content-Type"), ndjson)
		}
		for _, row := range rows {
			var object map[string]any
			if err := json.Unmarshal([]byte(row), &object); err != nil || len(object) != 6 {
				t.Errorf("ndjson line %q: %v", row, err)
			}
		}

		// The SQL form is in the engine's own dialect: emptied and fed its own
		// export, the table holds what it held.
		_, script := get("sql", "")
		if !strings.Contains(script, "INSERT INTO "+orders+" (") || !strings.Contains(script, "-- export complete: 2 rows") {
			t.Fatalf("sql:\n%s", script)
		}
		exec("DELETE FROM "+lines, "DELETE FROM "+orders)
		for _, statement := range strings.Split(script, ";\n") {
			statement = strings.TrimSpace(stripSQLComments(statement))
			if statement != "" {
				exec(statement)
			}
		}
		if count(orders) != 2 || text("SELECT "+col("note")+" FROM "+orders+" WHERE "+col("id")+" = 1") != note {
			t.Errorf("replaying the SQL export gave %d orders, note %q", count(orders),
				text("SELECT "+col("note")+" FROM "+orders+" WHERE "+col("id")+" = 1"))
		}
		// The bytes and the instant came back as they went out.
		_, again := get("csv", "")
		if again != csv {
			t.Errorf("the table after replaying its SQL export differs:\n%s\nwas\n%s", again, csv)
		}
		exec(e.seed[len(e.seed)-countLineInserts(e):]...)

		// A projection, in the order asked for; a limit that says it was
		// reached; a filter the grid would send.
		_, projected := get("csv", "&columns=customer,id")
		if projected != "customer,id\nann,1\nbo,2\n" {
			t.Errorf("projected csv:\n%s", projected)
		}
		res, limited := get("ndjson", "&limit=1")
		if res.Trailer.Get(exportStatusTrailer) != "truncated" || !strings.Contains(limited, `"__export"`) ||
			!strings.Contains(limited, `"status":"truncated"`) || strings.Count(limited, "\n") != 2 {
			t.Errorf("a limited export (%v):\n%s", res.Trailer, limited)
		}
		_, filtered := get("csv", "&columns=customer&filters="+url.QueryEscape(`[{"column":"customer","op":"eq","value":"bo"}]`))
		if filtered != "customer\nbo\n" {
			t.Errorf("filtered csv:\n%s", filtered)
		}
		if rec := do(t, r, http.MethodGet, path("/export")+table+"&format=xlsx", ""); rec.Code != http.StatusBadRequest {
			t.Errorf("a format nobody writes: %d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("export_of_a_statement", func(t *testing.T) {
		statement := "SELECT o." + col("customer") + ", COUNT(*) AS " + col("n") + " FROM " + orders + " o JOIN " + lines +
			" l ON l." + col("order_id") + " = o." + col("id") + " GROUP BY o." + col("customer") + " ORDER BY 1"
		body, _ := json.Marshal(map[string]any{"sql": statement, "format": "csv"})
		rec := do(t, r, http.MethodPost, path("/export/query"), string(body))
		if rec.Code != http.StatusOK || rec.Body.String() != "customer,n\nann,2\nbo,1\n" {
			t.Errorf("query export: %d\n%s", rec.Code, rec.Body.String())
		}
		// The same result as INSERT statements for a table the caller names.
		body, _ = json.Marshal(map[string]any{"sql": statement, "format": "sql", "table": "tally"})
		rec = do(t, r, http.MethodPost, path("/export/query"), string(body))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "INSERT INTO "+e.quote("tally")+" (") {
			t.Errorf("query export as sql: %d\n%s", rec.Code, rec.Body.String())
		}
		// A statement that writes is not exported, and is not run.
		body, _ = json.Marshal(map[string]any{"sql": "DELETE FROM " + lines, "format": "csv"})
		rec = do(t, r, http.MethodPost, path("/export/query"), string(body))
		if rec.Code != http.StatusBadRequest || count(lines) != 3 {
			t.Errorf("a DELETE through the export: %d %s (%d lines left)", rec.Code, rec.Body.String(), count(lines))
		}
	})

	// --- import ---------------------------------------------------------

	t.Run("import_dry_run_writes_nothing", func(t *testing.T) {
		rec, report := upload(into("jd_api_orders", `,"dryRun":true`), "orders.csv",
			"id,customer,total,Placed\n3,cy,5,2026-01-02 03:04:05\n4,di,oops,\n")
		if rec.Code != http.StatusOK || !report.DryRun || report.RowsRead != 2 || count(orders) != 2 {
			t.Fatalf("dry run: %d %s (%d orders)", rec.Code, rec.Body.String(), count(orders))
		}
		if len(report.Columns) != 4 || report.Columns[2].Target != "total" || report.Columns[2].Warning == "" ||
			report.Columns[3].Target != "placed" || report.Columns[0].Inferred != "integer" {
			t.Errorf("columns = %+v", report.Columns)
		}
		if report.Preview == nil || len(report.Preview.Rows) != 2 || !strings.Contains(report.Statement, "INSERT INTO "+orders) {
			t.Errorf("preview = %+v, statement = %s", report.Preview, report.Statement)
		}
	})

	t.Run("import_csv_skips_bad_rows_or_imports_nothing", func(t *testing.T) {
		const file = "id,customer,total\n3,cy,5\n1,dupe,1\n4,di,oops\n5,ed,7.25\n"
		// Without leave to skip, the first row the table refuses ends it, and
		// the rows before that one are not left behind.
		rec, _ := upload(into("jd_api_orders", ``), "orders.csv", file)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "nothing was imported") || count(orders) != 2 {
			t.Fatalf("an import with a bad row: %d %s (%d orders)", rec.Code, rec.Body.String(), count(orders))
		}
		if !strings.Contains(rec.Body.String(), "row 2 (line 3)") {
			t.Errorf("the refused row is not named by its line: %s", rec.Body.String())
		}
		rec, report := upload(into("jd_api_orders", `,"skipBadRows":true`), "orders.csv", file)
		if rec.Code != http.StatusOK || report.Inserted != 2 || report.Skipped != 2 || len(report.Errors) != 2 || count(orders) != 4 {
			t.Fatalf("import: %d %s (%d orders)", rec.Code, rec.Body.String(), count(orders))
		}
		if report.Errors[0].Line != 3 || report.Errors[1].Line != 4 || !report.Atomic {
			t.Errorf("report = %+v", report)
		}
	})

	t.Run("import_ndjson_upserts", func(t *testing.T) {
		rec, report := upload(into("jd_api_orders", `,"mode":"upsert"`), "orders.ndjson",
			`{"id":1,"customer":"ann","total":99.5,"note":"changed"}`+"\n"+
				`{"id":6,"customer":"flo","total":null,"note":"new"}`+"\n"+
				`{"id":6,"customer":"flo","total":3,"note":"twice in one file"}`+"\n")
		if rec.Code != http.StatusOK || report.Inserted != 1 || report.Updated != 2 || count(orders) != 5 {
			t.Fatalf("upsert: %d %s (%d orders)", rec.Code, rec.Body.String(), count(orders))
		}
		if len(report.Key) != 1 || report.Key[0] != "id" {
			t.Errorf("matched on %v", report.Key)
		}
		if got := text("SELECT " + col("note") + " FROM " + orders + " WHERE " + col("id") + " = 1"); got != "changed" {
			t.Errorf("the row that was there holds %q", got)
		}
		if got := text("SELECT " + col("note") + " FROM " + orders + " WHERE " + col("id") + " = 6"); got != "twice in one file" {
			t.Errorf("the key the file names twice holds %q", got)
		}
		// A column the file does not carry is left as it was, not emptied.
		if got := text("SELECT " + col("customer") + " FROM " + orders + " WHERE " + col("raw") + " IS NOT NULL"); got != "ann" {
			t.Errorf("the upsert emptied a column it was not given: %q", got)
		}
		// Matched on the unique constraint instead, by its name. The file
		// carries the row's own id as well: the engines whose upsert is an
		// INSERT first check that the row could be one.
		rec, report = upload(into("jd_api_orders", `,"mode":"upsert","conflict":{"constraint":"jd_api_orders_customer"}`), "by-name.csv",
			"id,customer,note\n2,bo,found by name\n")
		if rec.Code != http.StatusOK || report.Updated != 1 || report.Inserted != 0 {
			t.Fatalf("upsert on a named constraint: %d %s", rec.Code, rec.Body.String())
		}
		if got := text("SELECT " + col("note") + " FROM " + orders + " WHERE " + col("id") + " = 2"); got != "found by name" {
			t.Errorf("the row matched by its customer holds %q", got)
		}
	})

	t.Run("import_into_a_new_table", func(t *testing.T) {
		created := e.rel("jd_api_new")
		e.teardown = append([]string{"DROP TABLE " + created}, e.teardown...)
		const file = "sku,qty,price,added,live\nbolt,10,1.5,2026-01-02,true\nnut,,2.25,2026-01-03,false\n"
		rec, report := upload(into("jd_api_new", `,"createTable":{"columns":[{"name":"sku","type":"","notNull":true,"primaryKey":true}]}`), "new.csv", file)
		if rec.Code != http.StatusOK || report.Inserted != 2 || report.Create == nil || !report.Create.Created || count(created) != 2 {
			t.Fatalf("create from a file: %d %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(report.Create.Statement, "CREATE TABLE "+created) {
			t.Errorf("create statement = %s", report.Create.Statement)
		}
		// What was inferred holds what the file held: a number is a number.
		if got := text("SELECT SUM(" + col("qty") + ") FROM " + created); got != "10" {
			t.Errorf("sum of qty = %q", got)
		}
		// The key it was told to make is enforced.
		if _, err := direct.ExecContext(ctx, "INSERT INTO "+created+" ("+col("sku")+") VALUES ('bolt')"); err == nil {
			t.Error("the new table took a second row with the same key")
		}
		// It is there now, so it cannot be created again.
		rec, _ = upload(into("jd_api_new", `,"createTable":{}`), "new.csv", file)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "already exists") {
			t.Errorf("creating a table that exists: %d %s", rec.Code, rec.Body.String())
		}
		// And one whose rows are then refused is not left behind.
		rec, _ = upload(into("jd_api_gone", `,"createTable":{"columns":[{"name":"n","type":"","notNull":true,"primaryKey":true}]}`), "gone.csv", "n\n1\n1\n")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("a new table whose rows break its key: %d %s", rec.Code, rec.Body.String())
		}
		if _, err := direct.ExecContext(ctx, "SELECT COUNT(*) FROM "+e.rel("jd_api_gone")); err == nil {
			t.Error("a table made for an import that failed was left behind")
			_, _ = direct.ExecContext(ctx, "DROP TABLE "+e.rel("jd_api_gone"))
		}
	})

	t.Run("import_replace_is_all_or_nothing", func(t *testing.T) {
		before := count(lines)
		rec, _ := upload(into("jd_api_lines", `,"mode":"replace"`), "lines.csv", "order_id,sku\n1,x\n999,orphan\n")
		if rec.Code != http.StatusBadRequest || count(lines) != before {
			t.Fatalf("a replace that fails: %d %s (%d lines, were %d)", rec.Code, rec.Body.String(), count(lines), before)
		}
		rec, report := upload(into("jd_api_lines", `,"mode":"replace"`), "lines.tsv", "order_id\tsku\n1\tx\n2\ty\n")
		if rec.Code != http.StatusOK || report.Inserted != 2 || report.Format != "tsv" || count(lines) != 2 {
			t.Fatalf("replace: %d %s (%d lines)", rec.Code, rec.Body.String(), count(lines))
		}
	})

	t.Run("an_export_imports_back_as_it_was", func(t *testing.T) {
		rec := do(t, r, http.MethodGet, path("/export")+table+"&orderBy=id&format=csv", "")
		exported := rec.Body.String()
		exec("DELETE FROM "+lines, "DELETE FROM "+orders)
		rec, report := upload(into("jd_api_orders", ``), "orders.csv", exported)
		if rec.Code != http.StatusOK || report.Inserted != 5 {
			t.Fatalf("importing the export: %d %s", rec.Code, rec.Body.String())
		}
		again := do(t, r, http.MethodGet, path("/export")+table+"&orderBy=id&format=csv", "").Body.String()
		if again != exported {
			t.Errorf("the table after importing its own export differs:\n%s\nwas\n%s", again, exported)
		}
		rec, _ = upload(into("jd_api_lines", ``), "lines.csv", "order_id,sku\n1,a\n1,b\n2,c\n")
		if rec.Code != http.StatusOK || count(lines) != 3 {
			t.Fatalf("refilling the lines: %d %s", rec.Code, rec.Body.String())
		}
	})

	// --- dump and restore -----------------------------------------------

	result := func(lines []jobs.Line) transferResult {
		var res transferResult
		for _, line := range lines {
			if line.Stream == "result" {
				_ = json.Unmarshal([]byte(line.Text), &res)
			}
		}
		return res
	}
	var dumped string
	t.Run("dump_is_a_job_and_restores", func(t *testing.T) {
		job, out := liveJob(t, s, do(t, r, http.MethodPost, path("/backup"), `{"note":"whole"}`))
		if job.Status != jobs.StatusSucceeded {
			t.Fatalf("job = %+v\n%+v", job, out)
		}
		res := result(out)
		dumped = res.File
		if dumped == "" || res.Size == 0 || res.Summary == "" {
			t.Fatalf("result = %+v", res)
		}
		for _, line := range out {
			if info, err := dbx.ParseDSN(e.driver, e.dsn); err == nil && info.Password != "" && strings.Contains(line.Text, info.Password) {
				t.Errorf("a job line carries the password: %s", line.Text)
			}
		}
		var listing struct {
			Files   []dbBackupFile     `json:"files"`
			Options dbx.DumpCapability `json:"options"`
		}
		rec := do(t, r, http.MethodGet, path("/backups"), "")
		if err := json.Unmarshal(rec.Body.Bytes(), &listing); err != nil || len(listing.Files) != 1 {
			t.Fatalf("listing: %v %s", err, rec.Body.String())
		}
		file := listing.Files[0]
		if file.File != dumped || file.Note != "whole" || file.Origin != "dump" || file.By != "tester" || file.Tool == "" {
			t.Errorf("listed as %+v", file)
		}
		if file.Tool == dbx.BuiltInDumpTool && file.Format != "SQL" {
			t.Errorf("the dashboard's own dump is listed as %q", file.Format)
		}
		if listing.Options.NewDatabase != e.newDatabase && e.adminDSN == "" {
			t.Errorf("options = %+v", listing.Options)
		}

		// Changed since, then put back as it was when the dump was taken.
		exec("DELETE FROM "+lines, "UPDATE "+orders+" SET "+col("note")+" = 'since the dump'")
		job, out = liveJob(t, s, do(t, r, http.MethodPost, path("/restore"), `{"file":"`+dumped+`","dumpFirst":true}`))
		if job.Status != jobs.StatusSucceeded {
			t.Fatalf("restore job = %+v\n%+v", job, out)
		}
		res = result(out)
		if res.SafetyDump == "" || res.SafetyDump == dumped {
			t.Errorf("no dump of the state before the restore: %+v", res)
		} else if _, err := os.Stat(s.dbDumpDir("live-"+string(e.driver)) + "/" + res.SafetyDump); err != nil {
			t.Errorf("the safety dump is not in the dump directory: %v", err)
		}
		if count(orders) != 5 || count(lines) != 3 ||
			text("SELECT "+col("note")+" FROM "+orders+" WHERE "+col("id")+" = 1") != "changed" {
			t.Errorf("after the restore: %d orders, %d lines, note %q", count(orders), count(lines),
				text("SELECT "+col("note")+" FROM "+orders+" WHERE "+col("id")+" = 1"))
		}
		// The key between the two tables came back with them.
		if _, err := direct.ExecContext(ctx, "INSERT INTO "+lines+" ("+col("order_id")+", "+col("sku")+") VALUES (999, 'orphan')"); err == nil {
			t.Error("the foreign key did not come back: an orphan was accepted")
		}
		// The table that numbers its own rows still does, past the ones it has.
		exec("INSERT INTO " + lines + " (" + col("order_id") + ", " + col("sku") + ") VALUES (1, 'after')")
		if got := text("SELECT COUNT(*) FROM " + lines + " WHERE " + col("sku") + " = 'after' AND " + col("id") + " > 3"); got != "1" {
			t.Errorf("the row added after the restore did not get an id past the restored ones")
		}
		// And the pages the dashboard serves read the restored tables.
		if rec := do(t, r, http.MethodGet, path("/export")+table+"&columns=customer&format=csv&orderBy=id", ""); rec.Code != http.StatusOK ||
			!strings.HasPrefix(rec.Body.String(), "customer\nann\nbo\n") {
			t.Errorf("after the restore the export answers %d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("dump_of_some_tables_compressed", func(t *testing.T) {
		job, out := liveJob(t, s, do(t, r, http.MethodPost, path("/backup"),
			`{"tables":["jd_api_orders"],"compression":"gzip","schemaOnly":true}`))
		if job.Status != jobs.StatusSucceeded {
			t.Fatalf("job = %+v\n%+v", job, out)
		}
		res := result(out)
		raw, err := os.ReadFile(s.dbDumpDir("live-"+string(e.driver)) + "/" + res.File)
		if err != nil || !bytes.HasPrefix(raw, []byte{0x1f, 0x8b}) {
			t.Errorf("a compressed dump is not compressed: %v", err)
		}
		rec := do(t, r, http.MethodGet, path("/backups"), "")
		if !strings.Contains(rec.Body.String(), `"schemaOnly":true`) || !strings.Contains(rec.Body.String(), `"tables":["jd_api_orders"]`) ||
			!strings.Contains(rec.Body.String(), `"format":"compressed SQL"`) {
			t.Errorf("listing: %s", rec.Body.String())
		}
		if rec := do(t, r, http.MethodPost, path("/backup"), `{"tables":["no_such_table"]}`); rec.Code != http.StatusAccepted {
			t.Errorf("a dump of a table that is not there: %d %s", rec.Code, rec.Body.String())
		} else if job, _ := liveJob(t, s, rec); job.Status != jobs.StatusFailed || !strings.Contains(job.Error, "no such table") {
			t.Errorf("a dump of a table that is not there: %+v", job)
		}
	})

	// --- a second database ------------------------------------------------

	if !e.newDatabase {
		t.Run("no_second_database_to_copy_into", func(t *testing.T) {
			if rec := do(t, r, http.MethodPost, path("/copy"), `{"name":"jd_api_copy"}`); rec.Code != http.StatusBadRequest {
				t.Errorf("copy: %d %s", rec.Code, rec.Body.String())
			}
			if rec := do(t, r, http.MethodPost, path("/restore"), `{"file":"`+dumped+`","target":{"newDatabase":"jd_api_new"}}`); rec.Code != http.StatusBadRequest {
				t.Errorf("restore into a new database: %d %s", rec.Code, rec.Body.String())
			}
		})
		return
	}

	// The login that may create a database, where that is not the one the
	// rest ran as.
	adminID, adminDSN := id, e.dsn
	if e.adminDSN != "" {
		adminDSN = e.adminDSN
		adminID = addLiveConnection(t, s, "live-admin", e.driver, e.adminDSN)
	}
	adminPath := func(suffix string) string { return pathf("/databases/%d"+suffix, adminID) }
	scratch := []string{e.database + "_api_new", e.database + "_api_copy"}
	dropScratch := func() {
		for _, name := range scratch {
			_, _ = dbx.DropDatabase(context.Background(), e.driver, adminDSN, name)
		}
	}
	dropScratch()
	t.Cleanup(dropScratch)
	countIn := func(database, rel string) int {
		t.Helper()
		other, err := dbx.OpenDatabase(ctx, e.driver, adminDSN, database)
		if err != nil {
			t.Fatal(err)
		}
		defer other.Close()
		var n int
		if err := other.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+rel).Scan(&n); err != nil {
			t.Fatalf("counting %s in %s: %v", rel, database, err)
		}
		return n
	}

	t.Run("restore_into_a_new_database_leaves_this_one_alone", func(t *testing.T) {
		job, out := liveJob(t, s, do(t, r, http.MethodPost, adminPath("/backup"), `{}`))
		if job.Status != jobs.StatusSucceeded {
			t.Fatalf("job = %+v\n%+v", job, out)
		}
		file := result(out).File
		exec("DELETE FROM " + lines)
		job, out = liveJob(t, s, do(t, r, http.MethodPost, adminPath("/restore"),
			`{"file":"`+file+`","target":{"newDatabase":"`+scratch[0]+`"}}`))
		if job.Status != jobs.StatusSucceeded {
			t.Fatalf("job = %+v\n%+v", job, out)
		}
		if got := countIn(scratch[0], e.elsewhere("jd_api_lines")); got != 4 {
			t.Errorf("the new database holds %d lines, want 4", got)
		}
		if count(lines) != 0 {
			t.Errorf("restoring into a new database changed this one: %d lines", count(lines))
		}
		// A second time the name is taken, and that is said rather than
		// restored over.
		job, _ = liveJob(t, s, do(t, r, http.MethodPost, adminPath("/restore"),
			`{"file":"`+file+`","target":{"newDatabase":"`+scratch[0]+`"}}`))
		if job.Status != jobs.StatusFailed || !strings.Contains(job.Error, "already exists") {
			t.Errorf("a restore into a database that exists: %+v", job)
		}
		if got := countIn(scratch[0], e.elsewhere("jd_api_lines")); got != 4 {
			t.Errorf("the database that was there now holds %d lines", got)
		}
		job, out = liveJob(t, s, do(t, r, http.MethodPost, adminPath("/restore"), `{"file":"`+file+`"}`))
		if job.Status != jobs.StatusSucceeded || count(lines) != 4 {
			t.Fatalf("restore into this database: %+v\n%+v (%d lines)", job, out, count(lines))
		}
	})

	t.Run("copy_makes_a_database_with_the_same_rows", func(t *testing.T) {
		job, out := liveJob(t, s, do(t, r, http.MethodPost, adminPath("/copy"), `{"name":"`+scratch[1]+`"}`))
		if job.Status != jobs.StatusSucceeded {
			t.Fatalf("job = %+v\n%+v", job, out)
		}
		if res := result(out); res.Database != scratch[1] || res.Summary == "" {
			t.Errorf("result = %+v", res)
		}
		if orders, lines := countIn(scratch[1], e.elsewhere("jd_api_orders")), countIn(scratch[1], e.elsewhere("jd_api_lines")); orders != 5 || lines != 4 {
			t.Errorf("the copy holds %d orders and %d lines, want 5 and 4", orders, lines)
		}
		// The copy is a database of its own: emptied, the original is as it was.
		other, err := dbx.OpenDatabase(ctx, e.driver, adminDSN, scratch[1])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := other.ExecContext(ctx, "DELETE FROM "+e.elsewhere("jd_api_lines")); err != nil {
			t.Error(err)
		}
		other.Close()
		if count(lines) != 4 {
			t.Errorf("emptying the copy emptied the original: %d lines", count(lines))
		}
		// The dump a copy takes is its own, and is gone when it ends.
		entries, _ := os.ReadDir(s.Cfg.BackupLocalDir + "/databases")
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".copy-") {
				t.Errorf("a copy left its working directory behind: %s", entry.Name())
			}
		}
		// A copy of the structure alone.
		_, _ = dbx.DropDatabase(ctx, e.driver, adminDSN, scratch[1])
		job, out = liveJob(t, s, do(t, r, http.MethodPost, adminPath("/copy"), `{"name":"`+scratch[1]+`","structureOnly":true}`))
		if job.Status != jobs.StatusSucceeded {
			t.Fatalf("job = %+v\n%+v", job, out)
		}
		if orders, lines := countIn(scratch[1], e.elsewhere("jd_api_orders")), countIn(scratch[1], e.elsewhere("jd_api_lines")); orders != 0 || lines != 0 {
			t.Errorf("a copy of the structure holds %d orders and %d lines", orders, lines)
		}
		if rec := do(t, r, http.MethodPost, adminPath("/copy"), `{"name":"`+e.database+`"}`); rec.Code != http.StatusBadRequest {
			t.Errorf("a copy onto the database itself: %d %s", rec.Code, rec.Body.String())
		}
		job, _ = liveJob(t, s, do(t, r, http.MethodPost, adminPath("/copy"), `{"name":"`+scratch[1]+`"}`))
		if job.Status != jobs.StatusFailed || !strings.Contains(job.Error, "already exists") {
			t.Errorf("a copy onto a database that exists: %+v", job)
		}
	})
}

// countLineInserts is how many of an engine's seed statements fill the lines
// table: the ones to run again once the orders they point at are back.
func countLineInserts(e transferEngine) int {
	n := 0
	for _, statement := range e.seed {
		if strings.Contains(statement, "INSERT INTO") && strings.Contains(statement, "jd_api_lines") {
			n++
		}
	}
	return n
}

// stripSQLComments drops the comment lines of an export so what is left of a
// piece is a statement or nothing.
func stripSQLComments(piece string) string {
	var kept []string
	for _, line := range strings.Split(piece, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "--") {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}
