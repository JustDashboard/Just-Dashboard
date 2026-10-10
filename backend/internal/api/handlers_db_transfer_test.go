package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
	"github.com/go-chi/chi/v5"

	_ "modernc.org/sqlite"
)

// The routes that move data, driven over HTTP against a SQLite file: the one
// engine that is always there, and enough of one to prove what a route does
// with its request before any engine is asked.

// transferFixture is a server with one SQLite connection whose file exists,
// a dump directory of its own, and a caller whose role the test chooses.
type transferFixture struct {
	t      *testing.T
	s      *Server
	router http.Handler
	role   auth.Role
	user   string
	dbPath string
	id     int64
}

func newTransferFixture(t *testing.T) *transferFixture {
	t.Helper()
	s := testServer(t)
	// Deliberately outside the file roots: the dump directory is the
	// dashboard's own, and none of what follows may depend on an operator
	// being allowed to browse it.
	s.Cfg.BackupLocalDir = t.TempDir()
	f := &transferFixture{t: t, s: s, role: auth.RoleAdmin, user: "tester"}

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			p := &httpx.Principal{
				User: &auth.User{ID: 1, Username: f.user},
				Role: f.role, Kind: "session", IP: "127.0.0.1",
			}
			next.ServeHTTP(w, req.WithContext(httpx.WithPrincipal(req.Context(), p)))
		})
	})
	r.Use(httpx.AuditMutations(s.Audit))
	s.mountDatabaseRoutes(r)
	f.router = r

	f.dbPath = filepath.Join(s.Cfg.FileRoots[0], "shop.db")
	db, err := sql.Open("sqlite", f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, qty INTEGER, blob BLOB)`,
		`INSERT INTO items (id, name, qty, blob) VALUES (1, 'bolt', 10, x'00ff41'), (2, 'nut; "quoted"', NULL, NULL), (3, 'washer', 7, NULL)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	sealed, err := s.Sealer.Seal(f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Store.DB.Exec(
		`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES(?,?,?,?)`,
		"shop", string(dbx.DriverSQLite), sealed, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.id, _ = res.LastInsertId()
	return f
}

func (f *transferFixture) path(suffix string) string {
	return "/databases/" + strconv.FormatInt(f.id, 10) + suffix
}

func (f *transferFixture) do(method, path, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

// upload posts a multipart body: the fields in order, then the file.
func (f *transferFixture) upload(path string, fields [][2]string, filename string, content []byte) *httptest.ResponseRecorder {
	f.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, field := range fields {
		if err := mw.WriteField(field[0], field[1]); err != nil {
			f.t.Fatal(err)
		}
	}
	if filename != "" {
		part, err := mw.CreateFormFile("file", filename)
		if err != nil {
			f.t.Fatal(err)
		}
		part.Write(content)
	}
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

// waitJob follows a job a route started to its end.
func (f *transferFixture) waitJob(rec *httptest.ResponseRecorder) (jobs.Job, []jobs.Line) {
	f.t.Helper()
	if rec.Code != http.StatusAccepted {
		f.t.Fatalf("the route did not start a job: %d %s", rec.Code, rec.Body.String())
	}
	var started jobs.Job
	if err := json.Unmarshal(rec.Body.Bytes(), &started); err != nil || started.ID == "" {
		f.t.Fatalf("no job in the answer: %s", rec.Body.String())
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		job, lines, ok := f.s.modules.jobs.Get(started.ID)
		if !ok {
			f.t.Fatalf("job %s is gone", started.ID)
		}
		if job.Status != jobs.StatusRunning {
			return job, lines
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("job %s is still running", started.ID)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// result reads the line a transfer job ends on.
func jobResult(t *testing.T, lines []jobs.Line) transferResult {
	t.Helper()
	var res transferResult
	found := false
	for _, line := range lines {
		if line.Stream == "result" {
			if err := json.Unmarshal([]byte(line.Text), &res); err != nil {
				t.Fatalf("result line is not JSON: %s", line.Text)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("the job wrote no result line: %+v", lines)
	}
	return res
}

func (f *transferFixture) audited(action string) []string {
	f.t.Helper()
	rows, err := f.s.Store.DB.Query(`SELECT detail FROM audit_log WHERE action = ? ORDER BY id`, action)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var detail string
		if err := rows.Scan(&detail); err != nil {
			f.t.Fatal(err)
		}
		out = append(out, detail)
	}
	return out
}

func (f *transferFixture) count(query string) int {
	f.t.Helper()
	db, err := sql.Open("sqlite", f.dbPath)
	if err != nil {
		f.t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(query).Scan(&n); err != nil {
		f.t.Fatalf("%v\n%s", err, query)
	}
	return n
}

// --- export ------------------------------------------------------------------

func TestExportFormatsSayHowTheyEnded(t *testing.T) {
	f := newTransferFixture(t)
	for format, check := range map[string]func(body string){
		"csv": func(body string) {
			if !strings.HasPrefix(body, "id,name,qty,blob\n") || !strings.Contains(body, `"nut; ""quoted"""`) {
				t.Errorf("csv:\n%s", body)
			}
			// The whole value, not the preview a grid cell shows.
			if !strings.Contains(body, `\x00ff41`) {
				t.Errorf("csv lost the binary value:\n%s", body)
			}
		},
		"tsv": func(body string) {
			if !strings.HasPrefix(body, "id\tname\tqty\tblob\n") {
				t.Errorf("tsv:\n%s", body)
			}
		},
		"json": func(body string) {
			var rows []map[string]any
			if err := json.Unmarshal([]byte(body), &rows); err != nil || len(rows) != 3 {
				t.Fatalf("json: %v\n%s", err, body)
			}
			if rows[1]["qty"] != nil || rows[0]["name"] != "bolt" {
				t.Errorf("json rows = %v", rows)
			}
			// Keys in the table's order, not the alphabet's.
			if strings.Index(body, `"id"`) > strings.Index(body, `"blob"`) {
				t.Errorf("json keys are not in column order:\n%s", body)
			}
		},
		"ndjson": func(body string) {
			lines := strings.Split(strings.TrimSpace(body), "\n")
			if len(lines) != 3 || !strings.HasPrefix(lines[0], `{"id":"1"`) {
				t.Errorf("ndjson:\n%s", body)
			}
		},
		"sql": func(body string) {
			if !strings.Contains(body, `INSERT INTO "items" ("id", "name", "qty", "blob") VALUES`) ||
				!strings.Contains(body, `X'00FF41'`) || !strings.Contains(body, "'nut; \"quoted\"'") {
				t.Errorf("sql:\n%s", body)
			}
			if !strings.Contains(body, "-- export complete: 3 rows") {
				t.Errorf("the SQL export does not say how it ended:\n%s", body)
			}
		},
	} {
		rec := f.do(http.MethodGet, f.path("/export?table=items&format="+format), "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", format, rec.Code, rec.Body.String())
		}
		res := rec.Result()
		if got := res.Trailer.Get(exportStatusTrailer); got != "complete" {
			t.Errorf("%s: status trailer = %q, want complete", format, got)
		}
		if got := res.Trailer.Get(exportRowsTrailer); got != "3" {
			t.Errorf("%s: rows trailer = %q, want 3", format, got)
		}
		if !strings.Contains(res.Header.Get("Content-Disposition"), "items."+format) {
			t.Errorf("%s: disposition = %q", format, res.Header.Get("Content-Disposition"))
		}
		check(rec.Body.String())
	}
	// Each is on the trail twice: when it began, and with how it ended.
	if began, ended := f.audited("database.export"), f.audited("database.export.finished"); len(began) != 5 || len(ended) != 5 ||
		!strings.Contains(began[0], `"table":"items"`) || !strings.Contains(ended[0], `"rows":3`) {
		t.Errorf("exports recorded: %v\n%v", began, ended)
	}
}

func TestExportThatHitsItsLimitSaysSoInTheFileAndBesideIt(t *testing.T) {
	f := newTransferFixture(t)
	const id = "export-0001"
	for format, marker := range map[string]string{
		"json":   `{"__export":{"rows":2,"status":"truncated"}}`,
		"ndjson": `{"__export":{"rows":2,"status":"truncated"}}`,
		"sql":    "-- export truncated at 2 rows",
		"csv":    "",
	} {
		rec := f.do(http.MethodGet, f.path("/export?table=items&limit=2&exportId="+id+"&format="+format), "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", format, rec.Code, rec.Body.String())
		}
		if got := rec.Result().Trailer.Get(exportStatusTrailer); got != "truncated" {
			t.Errorf("%s: status trailer = %q, want truncated", format, got)
		}
		body := rec.Body.String()
		if marker != "" && !strings.Contains(body, marker) {
			t.Errorf("%s does not carry its marker:\n%s", format, body)
		}
		if format == "json" {
			// Still a document a parser accepts.
			var rows []map[string]any
			if err := json.Unmarshal([]byte(body), &rows); err != nil || len(rows) != 3 {
				t.Errorf("a truncated JSON export is not valid JSON of two rows and a marker: %v\n%s", err, body)
			}
		}
		// And the page that named the export can ask, which is the only way
		// a CSV has of saying it.
		status := f.do(http.MethodGet, f.path("/export/status?exportId="+id), "")
		var state exportState
		if err := json.Unmarshal(status.Body.Bytes(), &state); err != nil || state.Status != "truncated" || state.Rows != 2 {
			t.Errorf("%s: status = %d %s", format, status.Code, status.Body.String())
		}
	}

	// Somebody else asking after the same name is told there is no such thing.
	f.user = "somebody-else"
	if rec := f.do(http.MethodGet, f.path("/export/status?exportId="+id), ""); rec.Code != http.StatusNotFound {
		t.Errorf("another account read this one's export status: %d %s", rec.Code, rec.Body.String())
	}
}

func TestExportProjectionAndRefusals(t *testing.T) {
	f := newTransferFixture(t)
	rec := f.do(http.MethodGet, f.path("/export?table=items&columns=name,id"), "")
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Body.String(), "name,id\nbolt,1\n") {
		t.Fatalf("projection: %d\n%s", rec.Code, rec.Body.String())
	}
	rec = f.do(http.MethodGet, f.path(`/export?table=items&columns=%5B%22qty%22%5D&filters=%5B%7B%22column%22%3A%22name%22%2C%22op%22%3A%22eq%22%2C%22value%22%3A%22washer%22%7D%5D`), "")
	if rec.Code != http.StatusOK || rec.Body.String() != "qty\n7\n" {
		t.Fatalf("projection with a filter: %d\n%s", rec.Code, rec.Body.String())
	}

	// Each of these is wrong with the request, and is answered as an error —
	// never as a file with the complaint inside it.
	for name, query := range map[string]string{
		"a format this cannot write": "table=items&format=xlsx",
		"a column the table lacks":   "table=items&columns=nope",
		"a table that is not there":  "table=missing",
		"a filter that is not JSON":  "table=items&filters=%7B",
		"no table":                   "format=csv",
		"an export name that is odd": "table=items&exportId=a%20b",
		"a column named twice":       "table=items&columns=id,id",
	} {
		rec := f.do(http.MethodGet, f.path("/export?"+query), "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Content-Disposition") != "" || !strings.Contains(rec.Body.String(), `"error"`) {
			t.Errorf("%s was answered as a download: %v %s", name, rec.Header(), rec.Body.String())
		}
	}
}

// A failure after the file has begun cannot become an error response, and a
// response that simply ends looks complete. So it does not end: the
// connection is aborted, and the client sees a transfer that failed.
func TestExportThatFailsPartwayAbortsTheResponse(t *testing.T) {
	f := newTransferFixture(t)
	db, err := sql.Open("sqlite", f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	// The last row cannot be computed: the absolute value of the smallest
	// integer overflows, and SQLite says so when it reaches that row — by
	// which time the rows before it have left.
	const rows = 4000
	for _, stmt := range []string{
		`CREATE VIEW breaks AS WITH RECURSIVE c(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM c WHERE i < ` + strconv.Itoa(rows) + `)
			SELECT i AS id, CASE WHEN i = ` + strconv.Itoa(rows) + ` THEN abs(-9223372036854775807 - 1) ELSE i END AS n,
			       printf('%080d', i) AS padding FROM c`,
		// And one that fails on its third row, while everything is still held.
		`CREATE VIEW breaks_early AS SELECT id, CASE WHEN id = 3 THEN abs(-9223372036854775807 - 1) ELSE id END AS n FROM items`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	srv := httptest.NewServer(f.router)
	defer srv.Close()
	res, err := http.Get(srv.URL + f.path("/export?table=breaks&format=ndjson&exportId=export-fail-1"))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer res.Body.Close()
	body, readErr := io.ReadAll(res.Body)
	if readErr == nil {
		t.Fatalf("a failed export ended as if it were complete (status %d):\n%s", res.StatusCode, body)
	}
	if !strings.Contains(string(body), `"__export":{"error":`) || !strings.Contains(string(body), `"status":"failed"`) {
		t.Errorf("the part that arrived does not say it is a failed export:\n%s", body)
	}
	if got := res.Trailer.Get(exportStatusTrailer); got != "" {
		t.Errorf("a failed export sent a status trailer: %q", got)
	}

	state, ok := f.s.dbExports.get(exportKey("tester", f.id, "export-fail-1"))
	if !ok || state.Status != "failed" || state.Rows != rows-1 || state.Error == "" {
		t.Errorf("status = %+v", state)
	}
	details := f.audited("database.export.finished")
	if len(details) != 1 || !strings.Contains(details[0], "error") || len(f.audited("database.export")) != 1 {
		t.Errorf("the failure was not recorded: %v", details)
	}

	// A failure while nothing has yet been sent is an ordinary error response,
	// in every format.
	for _, format := range []string{"csv", "json", "ndjson", "sql"} {
		res, err := http.Get(srv.URL + f.path("/export?table=breaks_early&format="+format))
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "integer overflow") ||
			res.Header.Get("Content-Disposition") != "" {
			t.Errorf("%s: a failure before anything was sent answered %d: %s", format, res.StatusCode, body)
		}
	}
}

func TestExportQueryTakesOnlyAStatementThatReads(t *testing.T) {
	f := newTransferFixture(t)
	rec := f.do(http.MethodPost, f.path("/export/query"),
		`{"sql":"SELECT name, qty * 2 AS doubled FROM items WHERE qty IS NOT NULL ORDER BY id","format":"sql","table":"totals"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("query export: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `INSERT INTO "totals" ("name", "doubled") VALUES ('bolt', 20), ('washer', 14);`) {
		t.Errorf("sql:\n%s", body)
	}
	if got := rec.Result().Trailer.Get(exportRowsTrailer); got != "2" {
		t.Errorf("rows trailer = %q", got)
	}
	if details := f.audited("database.export.query"); len(details) != 1 || !strings.Contains(details[0], "SELECT name") {
		t.Errorf("the statement was not recorded: %v", details)
	}

	for name, body := range map[string]string{
		"a delete":         `{"sql":"DELETE FROM items","format":"csv"}`,
		"an update":        `{"sql":"UPDATE items SET qty = 0 WHERE id = 1","format":"csv"}`,
		"a pragma":         `{"sql":"PRAGMA journal_mode = WAL","format":"csv"}`,
		"two statements":   `{"sql":"SELECT 1; SELECT 2","format":"csv"}`,
		"nothing":          `{"sql":"  ","format":"csv"}`,
		"an unknown field": `{"sql":"SELECT 1","format":"csv","run":true}`,
	} {
		rec := f.do(http.MethodPost, f.path("/export/query"), body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	if f.count(`SELECT COUNT(*) FROM items`) != 3 {
		t.Fatal("an export changed the table")
	}

	// It runs what the caller wrote, so it takes what the query runner takes.
	f.role = auth.RoleReadOnly
	if rec := f.do(http.MethodPost, f.path("/export/query"), `{"sql":"SELECT 1","format":"csv"}`); rec.Code != http.StatusForbidden {
		t.Errorf("a read-only role exported a query: %d", rec.Code)
	}
	// The table export stays on the read surface.
	if rec := f.do(http.MethodGet, f.path("/export?table=items"), ""); rec.Code != http.StatusOK {
		t.Errorf("a read-only role could not export a table: %d %s", rec.Code, rec.Body.String())
	}
}

// --- import ------------------------------------------------------------------

func TestImportUploadStreamsAFileWithItsOptions(t *testing.T) {
	f := newTransferFixture(t)
	csv := []byte("Name;Quantity;junk\nscrew;40;x\nbolt;11;y\nrivet;;z\n")

	// A dry run first: what would happen, and nothing happening.
	rec := f.upload(f.path("/import/upload"), [][2]string{{"options",
		`{"table":"items","delimiter":";","dryRun":true,"mapping":{"Name":"name","Quantity":"qty"}}`}}, "stock.csv", csv)
	if rec.Code != http.StatusOK {
		t.Fatalf("dry run: %d %s", rec.Code, rec.Body.String())
	}
	var report dbx.ImportReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if !report.DryRun || report.Preview == nil || len(report.Preview.Rows) != 3 || report.RowsRead != 3 {
		t.Fatalf("dry run report = %+v", report)
	}
	if f.count(`SELECT COUNT(*) FROM items`) != 3 {
		t.Fatal("a dry run changed the table")
	}
	if len(f.audited("database.import")) != 0 {
		t.Error("a dry run was recorded as an import")
	}

	// Upsert on the unique name: one new row, one overwritten, one refused
	// for want of nothing — rivet has no quantity and that is allowed.
	rec = f.upload(f.path("/import/upload"), [][2]string{{"options",
		`{"table":"items","delimiter":";","mode":"upsert","conflict":{"columns":["name"]},"mapping":{"Name":"name","Quantity":"qty"}}`}}, "stock.csv", csv)
	if rec.Code != http.StatusOK {
		t.Fatalf("upsert: %d %s", rec.Code, rec.Body.String())
	}
	json.Unmarshal(rec.Body.Bytes(), &report)
	if report.Inserted != 2 || report.Updated != 1 || report.Skipped != 0 {
		t.Fatalf("upsert report = %+v", report)
	}
	if f.count(`SELECT qty FROM items WHERE name = 'bolt'`) != 11 || f.count(`SELECT COUNT(*) FROM items`) != 5 {
		t.Error("the upsert did not land as reported")
	}
	if details := f.audited("database.import"); len(details) != 1 || !strings.Contains(details[0], `"mode":"upsert"`) {
		t.Errorf("import recorded as %v", details)
	}

	// The format follows the file's name when nobody says.
	rec = f.upload(f.path("/import/upload"), [][2]string{{"options", `{"table":"items"}`}}, "more.ndjson",
		[]byte(`{"name":"anchor","qty":3}`+"\n"+`{"name":"hinge","qty":null}`+"\n"))
	json.Unmarshal(rec.Body.Bytes(), &report)
	if rec.Code != http.StatusOK || report.Inserted != 2 || report.Format != dbx.ImportFormatNDJSON {
		t.Fatalf("ndjson by extension: %d %s", rec.Code, rec.Body.String())
	}

	// A new table from the file.
	rec = f.upload(f.path("/import/upload"), [][2]string{{"options", `{"table":"prices","createTable":{}}`}}, "prices.csv",
		[]byte("sku,price,since\nA,1.5,2026-01-02\nB,2,2026-02-03\n"))
	json.Unmarshal(rec.Body.Bytes(), &report)
	if rec.Code != http.StatusOK || report.Create == nil || !report.Create.Created || report.Inserted != 2 {
		t.Fatalf("create table: %d %s", rec.Code, rec.Body.String())
	}
	if f.count(`SELECT COUNT(*) FROM prices`) != 2 {
		t.Error("the new table does not hold the file's rows")
	}
}

func TestImportUploadRefusals(t *testing.T) {
	f := newTransferFixture(t)
	csv := []byte("name,qty\nscrew,40\n")

	// The options decide what the bytes after them are, and whether this
	// caller may do what they ask, so they have to be there first.
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, _ := mw.CreateFormFile("file", "x.csv")
	part.Write(csv)
	mw.WriteField("options", `{"table":"items"}`)
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, f.path("/import/upload"), &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "before the file") {
		t.Errorf("a file before its options: %d %s", rec.Code, rec.Body.String())
	}

	for name, options := range map[string]string{
		"an unknown option": `{"table":"items","truncate":true}`,
		"no table":          `{}`,
		"an unknown mode":   `{"table":"items","mode":"merge"}`,
		"an unknown format": `{"table":"items","format":"xlsx"}`,
		"a missing table":   `{"table":"nowhere"}`,
		"options not JSON":  `table=items`,
	} {
		rec := f.upload(f.path("/import/upload"), [][2]string{{"options", options}}, "x.csv", csv)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	if rec := f.upload(f.path("/import/upload"), [][2]string{{"options", `{"table":"items"}`}}, "", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("no file part: %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.do(http.MethodPost, f.path("/import/upload"), `{"table":"items"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("a JSON body on the upload route: %d %s", rec.Code, rec.Body.String())
	}
	if f.count(`SELECT COUNT(*) FROM items`) != 3 {
		t.Fatal("a refused import changed the table")
	}

	// The limit is the install's, and an upload past it is told the limit.
	f.s.Cfg.DBUploadMaxMB = 1
	var big bytes.Buffer
	for i := 0; big.Len() < 3<<20; i++ {
		big.WriteString("a-name-that-is-long-enough-to-add-up-" + strconv.Itoa(i) + ",1\n")
	}
	rec = f.upload(f.path("/import/upload"), [][2]string{{"options", `{"table":"items","header":false,"columns":["name","qty"]}`}}, "big.csv", big.Bytes())
	if rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), "JD_DB_UPLOAD_MAX_MB") {
		t.Errorf("an upload past the limit: %d %s", rec.Code, rec.Body.String())
	}
	if f.count(`SELECT COUNT(*) FROM items`) != 3 {
		t.Error("an upload cut off by the limit left rows behind")
	}
}

// Replacing a table's contents is destructive, and only the options say that
// a request does it. The route cannot know; the handler has to.
func TestImportUploadChecksReplaceByContent(t *testing.T) {
	f := newTransferFixture(t)
	csv := []byte("name,qty\nscrew,40\n")
	replace := [][2]string{{"options", `{"table":"items","mode":"replace"}`}}

	f.role = auth.RoleLimited
	rec := f.upload(f.path("/import/upload"), replace, "x.csv", csv)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a role without the destructive capability replaced a table: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "confirmation") {
		t.Errorf("a replacing import asked for a typed phrase: %s", rec.Body.String())
	}
	if f.count(`SELECT COUNT(*) FROM items`) != 3 {
		t.Fatal("a refused replace emptied the table")
	}
	// Seeing what a replace would do writes nothing, and needs nothing more.
	if rec := f.upload(f.path("/import/upload"), [][2]string{{"options", `{"table":"items","mode":"replace","dryRun":true}`}}, "x.csv", csv); rec.Code != http.StatusOK {
		t.Errorf("a dry run of a replace: %d %s", rec.Code, rec.Body.String())
	}
	// Adding rows is the ordinary import.
	if rec := f.upload(f.path("/import/upload"), [][2]string{{"options", `{"table":"items"}`}}, "x.csv", csv); rec.Code != http.StatusOK {
		t.Errorf("an appending import: %d %s", rec.Code, rec.Body.String())
	}

	f.role = auth.RoleReadOnly
	if rec := f.upload(f.path("/import/upload"), [][2]string{{"options", `{"table":"items"}`}}, "x.csv", csv); rec.Code != http.StatusForbidden {
		t.Errorf("a read-only role imported: %d", rec.Code)
	}

	f.role = auth.RoleAdmin
	rec = f.upload(f.path("/import/upload"), replace, "x.csv", csv)
	if rec.Code != http.StatusOK || f.count(`SELECT COUNT(*) FROM items`) != 1 {
		t.Fatalf("replace as admin: %d %s", rec.Code, rec.Body.String())
	}
}

// --- dumps -------------------------------------------------------------------

func TestBackupRunsAsAJobAndIsListedWithWhatItHolds(t *testing.T) {
	f := newTransferFixture(t)
	job, lines := f.waitJob(f.do(http.MethodPost, f.path("/backup"), `{"note":"before the migration"}`))
	if job.Status != jobs.StatusSucceeded {
		t.Fatalf("job = %+v\n%+v", job, lines)
	}
	if job.Kind != transferPrefix(f.id)+"backup" || job.Target != "shop" || job.StartedBy != "tester" {
		t.Errorf("job = %+v", job)
	}
	res := jobResult(t, lines)
	if res.File == "" || res.Size == 0 {
		t.Fatalf("result = %+v", res)
	}

	rec := f.do(http.MethodGet, f.path("/backups"), "")
	var listing struct {
		Dir     string             `json:"dir"`
		Files   []dbBackupFile     `json:"files"`
		Options dbx.DumpCapability `json:"options"`
		Job     *jobs.Job          `json:"job"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	if len(listing.Files) != 1 || listing.Job != nil {
		t.Fatalf("listing = %s", rec.Body.String())
	}
	got := listing.Files[0]
	if got.File != res.File || got.Note != "before the migration" || got.Origin != dbx.DumpOriginDump ||
		got.By != "tester" || got.Tool != dbx.BuiltInDumpTool || got.ToolVersion == "" ||
		got.DurationMs == nil || got.Format != "SQLite file" || got.Contents == nil {
		t.Errorf("listed dump = %+v", got)
	}
	// The description is beside the dump and is not itself listed as one.
	if _, err := os.Stat(dbx.DumpMetaPath(filepath.Join(listing.Dir, got.File))); err != nil {
		t.Errorf("no description beside the dump: %v", err)
	}

	// A narrower dump is the SQL one, and says what it holds.
	job, lines = f.waitJob(f.do(http.MethodPost, f.path("/backup"), `{"schemaOnly":true,"tables":["items"],"compression":"gzip"}`))
	if job.Status != jobs.StatusSucceeded {
		t.Fatalf("job = %+v\n%+v", job, lines)
	}
	rec = f.do(http.MethodGet, f.path("/backups"), "")
	json.Unmarshal(rec.Body.Bytes(), &listing)
	var narrow *dbBackupFile
	for i := range listing.Files {
		if listing.Files[i].Contents != nil && listing.Files[i].Contents.SchemaOnly {
			narrow = &listing.Files[i]
		}
	}
	if narrow == nil || narrow.Format != "compressed SQL" || narrow.Contents.Compression != "gzip" ||
		len(narrow.Contents.Tables) != 1 || !strings.Contains(narrow.Summary, "structure only") {
		t.Errorf("the narrowed dump is listed as %+v", narrow)
	}

	// Both entries of the trail: the request, and how the job ended.
	if len(f.audited("database.backup")) != 2 || len(f.audited("database.backup.finish")) != 2 {
		t.Errorf("audit: %v / %v", f.audited("database.backup"), f.audited("database.backup.finish"))
	}

	// What the engine cannot honour is refused before a job exists.
	for name, body := range map[string]string{
		"both halves left out":   `{"schemaOnly":true,"dataOnly":true}`,
		"an unknown option":      `{"encrypt":true}`,
		"an unknown compression": `{"compression":"zstd"}`,
		"redis databases":        `{"databases":[1]}`,
		"a note too long":        `{"note":"` + strings.Repeat("x", 600) + `"}`,
	} {
		if rec := f.do(http.MethodPost, f.path("/backup"), body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}

	// The summary is every connection's newest dump in one read.
	rec = f.do(http.MethodGet, "/databases/backups/summary", "")
	var summary struct {
		Connections []dbBackupSummaryRow `json:"connections"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &summary); err != nil || len(summary.Connections) != 1 {
		t.Fatalf("summary: %d %s", rec.Code, rec.Body.String())
	}
	row := summary.Connections[0]
	if row.ID != f.id || row.Count != 2 || row.Newest == nil || row.TotalSize == 0 || row.Job != nil {
		t.Errorf("summary row = %+v", row)
	}
}

func TestASecondTransferOfTheSameConnectionIsRefused(t *testing.T) {
	f := newTransferFixture(t)
	release := make(chan struct{})
	running, started := f.s.modules.jobs.StartExclusive(transferPrefix(f.id),
		jobs.Spec{Kind: transferPrefix(f.id) + "backup", Title: "Dump shop", Target: "shop", StartedBy: "someone"},
		func(ctx context.Context, out jobs.Emitter) error { <-release; return nil })
	if !started {
		t.Fatal("could not start the job that holds the connection")
	}
	defer close(release)

	for _, c := range []struct{ method, path, body string }{
		{http.MethodPost, f.path("/backup"), `{}`},
		{http.MethodPost, f.path("/restore"), `{"dumpPath":"` + f.dbPath + `"}`},
	} {
		rec := f.do(c.method, c.path, c.body)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "transfer_running") {
			t.Fatalf("%s: %d %s", c.path, rec.Code, rec.Body.String())
		}
		// The answer names the job in the way, so the page can show it.
		if !strings.Contains(rec.Body.String(), running.ID) {
			t.Errorf("the refusal does not name the running job: %s", rec.Body.String())
		}
	}
	// And the listing shows it, so a reopened page finds it again.
	rec := f.do(http.MethodGet, f.path("/backups"), "")
	if !strings.Contains(rec.Body.String(), running.ID) {
		t.Errorf("the listing does not carry the running job: %s", rec.Body.String())
	}

	// Another connection is not held by this one. Its id starts with the
	// same digits on purpose.
	if transferPrefix(1) == transferPrefix(12)[:len(transferPrefix(1))] {
		t.Error("connection 1's jobs would be taken for connection 12's")
	}
}

func TestUploadedDumpIsContainedListedDownloadedAndDeleted(t *testing.T) {
	f := newTransferFixture(t)
	dump, err := os.ReadFile(f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	rec := f.upload(f.path("/backups/upload?note=from+the+old+server"), nil, "old-server.sqlite", dump)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	var uploaded dbBackupFile
	json.Unmarshal(rec.Body.Bytes(), &uploaded)
	if uploaded.File != "old-server.sqlite" || uploaded.Origin != dbx.DumpOriginUpload || uploaded.Note != "from the old server" ||
		uploaded.Format != "SQLite file" || uploaded.Size != int64(len(dump)) || uploaded.DurationMs != nil {
		t.Errorf("uploaded = %+v", uploaded)
	}
	dir := f.s.dbDumpDir("shop")
	if st, err := os.Stat(filepath.Join(dir, "old-server.sqlite")); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("the uploaded dump is not a private file in the dump directory: %v %v", st, err)
	}

	// A name is a name. None of these lands anywhere.
	outside := filepath.Join(filepath.Dir(dir), "escaped.sqlite")
	for _, name := range []string{"../escaped.sqlite", "sub/x.sqlite", ".hidden", "x.sqlite.meta.json", "white space.sql", ""} {
		rec := f.upload(f.path("/backups/upload?name="+strings.ReplaceAll(name, " ", "%20")), nil, "ok.sqlite", dump)
		if name == "" {
			// No name given: the file's own is used, and that one is fine.
			if rec.Code != http.StatusCreated {
				t.Errorf("an upload under its own name: %d %s", rec.Code, rec.Body.String())
			}
			continue
		}
		if rec.Code != http.StatusBadRequest {
			t.Errorf("the name %q was accepted: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	if _, err := os.Stat(outside); err == nil {
		t.Fatal("an upload escaped the dump directory")
	}
	// A path in the filename field is cut down to its last element.
	if rec := f.upload(f.path("/backups/upload"), nil, "../../etc/passwd.sql", []byte("-- x\n")); rec.Code != http.StatusCreated ||
		!strings.Contains(rec.Body.String(), `"file":"passwd.sql"`) {
		t.Errorf("a path in the filename: %d %s", rec.Code, rec.Body.String())
	}
	// A name that is taken is not overwritten.
	if rec := f.upload(f.path("/backups/upload"), nil, "old-server.sqlite", dump); rec.Code != http.StatusConflict ||
		strings.Contains(rec.Body.String(), dir) {
		t.Errorf("a second upload under the same name: %d %s", rec.Code, rec.Body.String())
	}
	// Nor when the uploads arrive together: one of them has the name and the
	// rest are told it is taken, and the file is the one that was accepted.
	const together = 8
	var wg sync.WaitGroup
	codes := make([]int, together)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = f.upload(f.path("/backups/upload"), nil, "raced.sql", []byte(fmt.Sprintf("-- upload %d\n", i))).Code
		}()
	}
	wg.Wait()
	accepted := -1
	for i, code := range codes {
		switch {
		case code == http.StatusCreated && accepted >= 0:
			t.Fatalf("uploads %d and %d were both accepted under one name", accepted, i)
		case code == http.StatusCreated:
			accepted = i
		case code != http.StatusConflict:
			t.Errorf("upload %d: %d", i, code)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "raced.sql")); accepted < 0 || string(got) != fmt.Sprintf("-- upload %d\n", accepted) {
		t.Errorf("accepted upload %d, and the file holds %q", accepted, got)
	}
	// Nothing an upload was staged in is left, and none of it was ever listed.
	if entries, _ := os.ReadDir(dir); len(entries) != 8 {
		t.Errorf("the dump directory holds %d entries, want four dumps and their descriptions", len(entries))
	}

	// Download by name; the description is not a dump and is not handed out.
	if rec := f.do(http.MethodGet, f.path("/backup/download?file=old-server.sqlite"), ""); rec.Code != http.StatusOK || rec.Body.Len() != len(dump) {
		t.Errorf("download: %d, %d bytes", rec.Code, rec.Body.Len())
	}
	for _, name := range []string{"old-server.sqlite.meta.json", "../shop.db", ""} {
		if rec := f.do(http.MethodGet, f.path("/backup/download?file="+name), ""); rec.Code != http.StatusBadRequest {
			t.Errorf("download of %q: %d %s", name, rec.Code, rec.Body.String())
		}
	}

	// Deleting a dump takes its description with it.
	if rec := f.do(http.MethodDelete, f.path("/backups"), `{"file":"old-server.sqlite"}`); rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	for _, gone := range []string{"old-server.sqlite", "old-server.sqlite.meta.json"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); err == nil {
			t.Errorf("%s survived the delete", gone)
		}
	}
	if rec := f.do(http.MethodDelete, f.path("/backups"), `{"file":"ok.sqlite.meta.json"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("a description was deleted on its own: %d %s", rec.Code, rec.Body.String())
	}

	f.role = auth.RoleReadOnly
	if rec := f.upload(f.path("/backups/upload"), nil, "another.sqlite", dump); rec.Code != http.StatusForbidden {
		t.Errorf("a read-only role uploaded a dump: %d", rec.Code)
	}
}

func TestRestoreReadsTheDashboardsOwnDumpsWhateverTheRootsAre(t *testing.T) {
	f := newTransferFixture(t)
	job, lines := f.waitJob(f.do(http.MethodPost, f.path("/backup"), `{}`))
	if job.Status != jobs.StatusSucceeded {
		t.Fatalf("backup: %+v", job)
	}
	dumped := jobResult(t, lines).File

	// Open the pool, as a page that has been looking at the table has.
	if rec := f.do(http.MethodGet, f.path("/browse?table=items&schema=main"), ""); rec.Code != http.StatusOK {
		t.Fatalf("browse: %d %s", rec.Code, rec.Body.String())
	}
	db, err := sql.Open("sqlite", f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM items WHERE id > 1`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	// By name, with the current state kept first.
	job, lines = f.waitJob(f.do(http.MethodPost, f.path("/restore"), `{"file":"`+dumped+`","dumpFirst":true}`))
	if job.Status != jobs.StatusSucceeded {
		t.Fatalf("restore: %+v\n%+v", job, lines)
	}
	res := jobResult(t, lines)
	if res.File != dumped || res.SafetyDump == "" || res.SafetyDump == dumped {
		t.Fatalf("result = %+v", res)
	}
	if f.count(`SELECT COUNT(*) FROM items`) != 3 {
		t.Error("the restore did not bring the rows back")
	}
	// The page that was open reads the restored database, not the one before.
	rec := f.do(http.MethodGet, f.path("/browse?table=items&schema=main"), "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "washer") {
		t.Errorf("browse after the restore: %d %s", rec.Code, rec.Body.String())
	}
	// The safety dump is a dump like any other, marked as what it is.
	listing := f.do(http.MethodGet, f.path("/backups"), "").Body.String()
	if !strings.Contains(listing, res.SafetyDump) || !strings.Contains(listing, `"origin":"safety"`) ||
		!strings.Contains(listing, "Taken before restoring "+dumped) {
		t.Errorf("the safety dump is not listed as one: %s", listing)
	}

	// By the path the listing reports — which is outside the file roots here,
	// and must work all the same: it is the dashboard's own file.
	full := filepath.Join(f.s.dbDumpDir("shop"), dumped)
	if _, err := f.s.modules.files.Resolve(full); err == nil {
		t.Fatal("this test needs the dump directory to be outside the file roots")
	}
	if job, _ := f.waitJob(f.do(http.MethodPost, f.path("/restore"), `{"dumpPath":"`+full+`"}`)); job.Status != jobs.StatusSucceeded {
		t.Errorf("restore by the listing's path: %+v", job)
	}

	// A file elsewhere is a client-supplied path, and is held to the roots.
	inRoots := filepath.Join(f.s.Cfg.FileRoots[0], "copy.sqlite")
	raw, _ := os.ReadFile(full)
	os.WriteFile(inRoots, raw, 0o600)
	if job, _ := f.waitJob(f.do(http.MethodPost, f.path("/restore"), `{"dumpPath":"`+inRoots+`"}`)); job.Status != jobs.StatusSucceeded {
		t.Errorf("restore from a path under the roots: %+v", job)
	}
	// A file that is not a database is refused by the job, and the database
	// is as it was.
	os.WriteFile(filepath.Join(f.s.dbDumpDir("shop"), "junk.sqlite"), []byte("not a database"), 0o600)
	job, _ = f.waitJob(f.do(http.MethodPost, f.path("/restore"), `{"file":"junk.sqlite"}`))
	if job.Status != jobs.StatusFailed || !strings.Contains(job.Error, "not a SQLite database") {
		t.Errorf("restoring junk: %+v", job)
	}
	if f.count(`SELECT COUNT(*) FROM items`) != 3 {
		t.Error("a refused restore changed the database")
	}
	if got := f.audited("database.restore.finish"); len(got) != 4 || !strings.Contains(got[3], "not a SQLite database") {
		t.Errorf("restore outcomes recorded: %v", got)
	}

	f.role = auth.RoleLimited
	if rec := f.do(http.MethodPost, f.path("/restore"), `{"file":"`+dumped+`"}`); rec.Code != http.StatusForbidden {
		t.Errorf("a role without the destructive capability restored: %d", rec.Code)
	}
	if rec := f.do(http.MethodPost, f.path("/backup"), `{}`); rec.Code != http.StatusAccepted {
		t.Errorf("a role with service.control could not start a dump: %d %s", rec.Code, rec.Body.String())
	} else {
		f.waitJob(rec)
	}
}

func TestRestoreRefusesWhatItMayNotRead(t *testing.T) {
	f := newTransferFixture(t)
	job, lines := f.waitJob(f.do(http.MethodPost, f.path("/backup"), `{}`))
	if job.Status != jobs.StatusSucceeded {
		t.Fatalf("backup: %+v", job)
	}
	dumped := jobResult(t, lines).File
	raw, err := os.ReadFile(filepath.Join(f.s.dbDumpDir("shop"), dumped))
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.sqlite")
	os.WriteFile(outside, raw, 0o600)
	for name, body := range map[string]string{
		"a path outside the roots":       `{"dumpPath":"` + outside + `"}`,
		"a name that walks out":          `{"file":"../../shop.db"}`,
		"a dump that is not there":       `{"file":"missing.sqlite"}`,
		"a description":                  `{"file":"` + dumped + `.meta.json"}`,
		"no file at all":                 `{}`,
		"a new database, on SQLite":      `{"file":"` + dumped + `","target":{"newDatabase":"other"}}`,
		"a target that is neither":       `{"file":"` + dumped + `","target":"elsewhere"}`,
		"a target with an unknown field": `{"file":"` + dumped + `","target":{"newDatabase":"x","drop":true}}`,
	} {
		rec := f.do(http.MethodPost, f.path("/restore"), body)
		if rec.Code == http.StatusAccepted {
			t.Errorf("%s started a restore: %s", name, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "confirmation") {
			t.Errorf("%s asked for a typed phrase: %s", name, rec.Body.String())
		}
	}

	if got := f.audited("database.restore.finish"); len(got) != 0 {
		t.Errorf("a refused restore was recorded as having run: %v", got)
	}
}

// None of these is the rare, unrecoverable kind a typed phrase is for, and
// the set of routes that take one does not grow.
func TestTransferRoutesDoNotAskForAPhrase(t *testing.T) {
	f := newTransferFixture(t)
	for _, c := range []confirmCase{
		{http.MethodPost, f.path("/restore"), `{"file":"x.sqlite"}`, "a restore takes an ordinary confirmation"},
		{http.MethodPost, f.path("/backup"), `{}`, "a dump changes nothing"},
		{http.MethodPost, f.path("/copy"), `{"name":"other"}`, "a copy makes a database and removes none"},
		{http.MethodPost, f.path("/export/query"), `{"sql":"SELECT 1"}`, "an export reads"},
		{http.MethodDelete, f.path("/backups"), `{"file":"x.sqlite"}`, "a dump can be taken again"},
	} {
		rec := f.do(c.method, c.path, c.body)
		if strings.Contains(rec.Body.String(), "confirmation") || rec.Code == http.StatusPreconditionRequired {
			t.Errorf("%s %s asked for a phrase, but %s: %d %s", c.method, c.path, c.why, rec.Code, rec.Body.String())
		}
	}
	for _, path := range []string{"/import/upload", "/backups/upload"} {
		rec := f.upload(f.path(path), [][2]string{{"options", `{"table":"items","mode":"replace"}`}}, "x.csv", []byte("name\nx\n"))
		if strings.Contains(rec.Body.String(), "confirmation") || rec.Code == http.StatusPreconditionRequired {
			t.Errorf("POST %s asked for a phrase: %d %s", path, rec.Code, rec.Body.String())
		}
	}
}

func TestCopyNeedsWhatCreatingADatabaseNeeds(t *testing.T) {
	f := newTransferFixture(t)
	// SQLite has no second database beside the file, and says so.
	if rec := f.do(http.MethodPost, f.path("/copy"), `{"name":"other"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("a copy on SQLite: %d %s", rec.Code, rec.Body.String())
	}
	f.role = auth.RoleLimited
	if rec := f.do(http.MethodPost, f.path("/copy"), `{"name":"other"}`); rec.Code != http.StatusForbidden {
		t.Errorf("a role that cannot create a database copied one: %d", rec.Code)
	}
}
