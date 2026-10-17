package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
)

// Bringing a database over from another server, over HTTP against a store and
// a SQLite connection: what the routes refuse, who may ask, what is kept and
// what is never given back. The engines themselves are in the live tests.

const importSecret = "hunter2-source-password"

// addConnection saves a connection of any engine without dialling it.
func (f *transferFixture) addConnection(name string, driver dbx.Driver, dsn string, readOnly bool) int64 {
	f.t.Helper()
	sealed, err := f.s.Sealer.Seal(dsn)
	if err != nil {
		f.t.Fatal(err)
	}
	res, err := f.s.Store.DB.Exec(
		`INSERT INTO db_connections(name, driver, dsn_enc, created_at, read_only) VALUES(?,?,?,?,?)`,
		name, string(driver), sealed, 0, boolInt(readOnly))
	if err != nil {
		f.t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

// addImport writes a row as the import would have.
func (f *transferFixture) addImport(conn int64, name, mode, status string, aged time.Duration) int64 {
	f.t.Helper()
	sealed := ""
	if mode == importReplication && (status == importReplicating || status == importRunning || status == importCutting) {
		var err error
		sealed, err = f.s.Sealer.Seal("postgres://reader:" + importSecret + "@source.example.com:5432/shop")
		if err != nil {
			f.t.Fatal(err)
		}
	}
	at := time.Now().Add(-aged).Unix()
	res, err := f.s.Store.DB.Exec(`
		INSERT INTO db_online_imports(connection_id, name, mode, status, target_database, source_label, source_user,
			source_enc, started_by, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		conn, name, mode, status, "shop_copy", "source.example.com:5432/shop", "reader", sealed, "tester", at, at)
	if err != nil {
		f.t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func (f *transferFixture) importStatus(id int64) (status, sealed, detail string) {
	f.t.Helper()
	if err := f.s.Store.DB.QueryRow(`SELECT status, source_enc, detail FROM db_online_imports WHERE id = ?`, id).
		Scan(&status, &sealed, &detail); err != nil {
		f.t.Fatal(err)
	}
	return
}

func importPath(f *transferFixture, suffix string) string { return f.path("/imports" + suffix) }

func (f *transferFixture) postTo(conn int64, path, body string) *httptest.ResponseRecorder {
	return f.do(http.MethodPost, "/databases/"+strconv.FormatInt(conn, 10)+path, body)
}

// storeCount runs a count against the dashboard's own store.
func (f *transferFixture) storeCount(query string) int {
	f.t.Helper()
	var n int
	if err := f.s.Store.DB.QueryRow(query).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func TestImportRoutesAskForTheirCapability(t *testing.T) {
	f := newTransferFixture(t)
	number := f.addImport(f.id, "jd_import_shop", importReplication, importReplicating, time.Hour)
	one := "/" + strconv.FormatInt(number, 10)

	// Every role reads what is under way.
	for _, role := range []auth.Role{auth.RoleReadOnly, auth.RoleLimited, auth.RoleAdmin} {
		f.role = role
		for _, path := range []string{importPath(f, ""), importPath(f, one)} {
			if rec := f.do(http.MethodGet, path, ""); rec.Code != http.StatusOK {
				t.Errorf("%s read %s: %d %s", role, path, rec.Code, rec.Body.String())
			}
		}
	}
	// Making a database on a server and taking a replication away from it are
	// an administrator's.
	for _, role := range []auth.Role{auth.RoleReadOnly, auth.RoleLimited} {
		f.role = role
		for _, c := range []struct{ method, path, body string }{
			{http.MethodPost, importPath(f, "/inspect"), `{"source":"x"}`},
			{http.MethodPost, importPath(f, ""), `{"source":"x","database":"y"}`},
			{http.MethodPost, importPath(f, one+"/cutover"), `{"writesStopped":true}`},
			{http.MethodDelete, importPath(f, one), ``},
		} {
			if rec := f.do(c.method, c.path, c.body); rec.Code != http.StatusForbidden {
				t.Errorf("%s %s %s: %d %s", role, c.method, c.path, rec.Code, rec.Body.String())
			}
		}
	}
	if status, _, _ := f.importStatus(number); status != importReplicating {
		t.Errorf("a refused request changed the import to %s", status)
	}
}

func TestImportRoutesDoNotAskForAPhrase(t *testing.T) {
	f := newTransferFixture(t)
	number := f.addImport(f.id, "done_one", importStream, importDone, time.Hour)
	for _, c := range []confirmCase{
		{http.MethodPost, importPath(f, "/inspect"), `{"source":"x"}`, "an inspection reads"},
		{http.MethodPost, importPath(f, ""), `{"source":"x","database":"y"}`, "an import makes a database and removes none"},
		{http.MethodPost, importPath(f, "/"+strconv.FormatInt(number, 10)+"/cutover"), `{"writesStopped":true}`, "a cutover is a confirmation of its own"},
		{http.MethodDelete, importPath(f, "/"+strconv.FormatInt(number, 10)), ``, "forgetting an import removes a row"},
	} {
		rec := f.do(c.method, c.path, c.body)
		if strings.Contains(rec.Body.String(), "confirmation") || rec.Code == http.StatusPreconditionRequired {
			t.Errorf("%s %s asked for a phrase, but %s: %d %s", c.method, c.path, c.why, rec.Code, rec.Body.String())
		}
	}
}

func TestImportIsRefusedForAnEngineThatCannotBringOneOver(t *testing.T) {
	f := newTransferFixture(t)
	for _, c := range []struct{ path, body string }{
		{importPath(f, "/inspect"), `{"source":"somewhere"}`},
		{importPath(f, ""), `{"source":"somewhere","database":"other"}`},
	} {
		rec := f.do(http.MethodPost, c.path, c.body)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "cannot be brought over") {
			t.Errorf("POST %s on SQLite: %d %s", c.path, rec.Code, rec.Body.String())
		}
	}
	if n := f.storeCount(`SELECT COUNT(*) FROM db_online_imports`); n != 0 {
		t.Errorf("a refused import left %d rows", n)
	}
}

// The target is a PostgreSQL that nothing answers on: every check that runs
// before the target is dialled is exercised, and none of what comes back may
// carry a password.
func TestImportChecksItsRequestBeforeDialling(t *testing.T) {
	f := newTransferFixture(t)
	target := f.addConnection("pg", dbx.DriverPostgres, "postgres://app:"+importSecret+"@127.0.0.1:1/shop?sslmode=disable", false)
	post := func(suffix, body string) *httptest.ResponseRecorder {
		return f.postTo(target, "/imports"+suffix, body)
	}

	for name, c := range map[string]struct {
		suffix, body, want string
		code               int
	}{
		"no database":       {"", `{"source":"postgres://r:p@h/d"}`, "database", http.StatusBadRequest},
		"a bad database":    {"", `{"source":"postgres://r:p@h/d","database":"bad name!"}`, "letters, digits and underscores", http.StatusBadRequest},
		"no source":         {"", `{"database":"copy"}`, "source is required", http.StatusBadRequest},
		"unknown field":     {"", `{"source":"x","database":"copy","password":"x"}`, "", http.StatusBadRequest},
		"inspect no source": {"/inspect", `{}`, "source is required", http.StatusBadRequest},
	} {
		rec := post(c.suffix, c.body)
		if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	// The target does not answer: that is said without its password.
	rec := post("/inspect", `{"source":"postgres://reader:`+importSecret+`@source.example.com/shop"}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("an unreachable target: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), importSecret) {
		t.Errorf("the answer carries a password: %s", rec.Body.String())
	}
	if n := f.storeCount(`SELECT COUNT(*) FROM db_online_imports`); n != 0 {
		t.Errorf("a refused import left %d rows", n)
	}
}

func TestAnImportThatCannotBeParsedDoesNotQuoteTheString(t *testing.T) {
	f := newTransferFixture(t)
	target := f.addConnection("pg", dbx.DriverPostgres, "postgres://app:x@127.0.0.1:1/shop?sslmode=disable", false)
	rec := f.postTo(target, "/imports", `{"database":"copy","source":"postgres://reader:`+importSecret+`@bad host/shop"}`)
	if rec.Code == http.StatusAccepted || strings.Contains(rec.Body.String(), importSecret) {
		t.Errorf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestImportsAreListedWithoutTheirSecrets(t *testing.T) {
	f := newTransferFixture(t)
	stream := f.addImport(f.id, "done_one", importStream, importDone, time.Hour)
	replicating := f.addImport(f.id, "jd_import_shop", importReplication, importReplicating, time.Hour)

	rec := f.do(http.MethodGet, importPath(f, ""), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, importSecret) || strings.Contains(body, "sourceEnc") || strings.Contains(body, "source_enc") {
		t.Errorf("the list carries the connection string: %s", body)
	}
	var list importList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Imports) != 2 || list.Imports[0].ID != replicating || list.Imports[1].ID != stream {
		t.Fatalf("imports = %+v", list.Imports)
	}
	if got := list.Imports[0].Source; got.Address != "source.example.com:5432" || got.Database != "shop" || got.User != "reader" {
		t.Errorf("source = %+v", got)
	}

	one := f.do(http.MethodGet, importPath(f, "/"+strconv.FormatInt(replicating, 10)), "")
	if one.Code != http.StatusOK || strings.Contains(one.Body.String(), importSecret) {
		t.Errorf("detail: %d %s", one.Code, one.Body.String())
	}
	if rec := f.do(http.MethodGet, importPath(f, "/9999"), ""); rec.Code != http.StatusNotFound {
		t.Errorf("an import that is not there: %d", rec.Code)
	}
	if rec := f.do(http.MethodGet, importPath(f, "/x"), ""); rec.Code != http.StatusBadRequest {
		t.Errorf("an id that is not a number: %d", rec.Code)
	}
}

func TestAnImportBelongsToItsConnection(t *testing.T) {
	f := newTransferFixture(t)
	other := f.addConnection("other", dbx.DriverSQLite, f.dbPath, false)
	number := f.addImport(other, "elsewhere", importStream, importDone, time.Hour)
	if rec := f.do(http.MethodGet, importPath(f, "/"+strconv.FormatInt(number, 10)), ""); rec.Code != http.StatusNotFound {
		t.Errorf("another connection's import was read through this one: %d", rec.Code)
	}
	if rec := f.do(http.MethodDelete, importPath(f, "/"+strconv.FormatInt(number, 10)), ""); rec.Code != http.StatusNotFound {
		t.Errorf("another connection's import was removed through this one: %d", rec.Code)
	}
	if list := f.do(http.MethodGet, importPath(f, ""), ""); strings.Contains(list.Body.String(), "elsewhere") {
		t.Errorf("another connection's import is listed: %s", list.Body.String())
	}
}

func TestAnImportThatWasRunningWhenTheDashboardStoppedIsFailed(t *testing.T) {
	f := newTransferFixture(t)
	stale := f.addImport(f.id, "stale", importStream, importRunning, time.Hour)
	fresh := f.addImport(f.id, "fresh", importStream, importRunning, 0)

	if rec := f.do(http.MethodGet, importPath(f, ""), ""); rec.Code != http.StatusOK {
		t.Fatalf("%d", rec.Code)
	}
	if status, _, detail := f.importStatus(stale); status != importFailed || !strings.Contains(detail, "stopped") {
		t.Errorf("a stale run is %s (%q)", status, detail)
	}
	// A row written a moment ago has a job that has not started yet.
	if status, _, _ := f.importStatus(fresh); status != importRunning {
		t.Errorf("a run begun just now is %s", status)
	}
}

func TestEndingAnImportEmptiesItsSealedString(t *testing.T) {
	f := newTransferFixture(t)
	number := f.addImport(f.id, "jd_import_shop", importReplication, importRunning, time.Hour)
	if _, sealed, _ := f.importStatus(number); sealed == "" {
		t.Fatal("the fixture kept nothing sealed")
	}
	f.s.setImport(number, importFailed, "the source went away")
	if status, sealed, detail := f.importStatus(number); status != importFailed || sealed != "" || detail != "the source went away" {
		t.Errorf("status = %s, sealed = %q, detail = %q", status, sealed, detail)
	}
	// Still under way, it keeps what the cutover will need.
	again := f.addImport(f.id, "jd_import_again", importReplication, importRunning, time.Hour)
	f.s.setImport(again, importReplicating, "")
	if _, sealed, _ := f.importStatus(again); sealed == "" {
		t.Error("a replicating import lost the string its cutover needs")
	}
}

func TestACutoverNeedsTheWritesToHaveStopped(t *testing.T) {
	f := newTransferFixture(t)
	number := f.addImport(f.id, "jd_import_shop", importReplication, importReplicating, time.Hour)
	path := importPath(f, "/"+strconv.FormatInt(number, 10)+"/cutover")
	for _, body := range []string{`{}`, `{"writesStopped":false}`} {
		if rec := f.do(http.MethodPost, path, body); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "writesStopped") {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body.String())
		}
	}
	done := f.addImport(f.id, "finished", importReplication, importDone, time.Hour)
	if rec := f.do(http.MethodPost, importPath(f, "/"+strconv.FormatInt(done, 10)+"/cutover"), `{"writesStopped":true}`); rec.Code != http.StatusConflict {
		t.Errorf("a cutover of an import that is over: %d %s", rec.Code, rec.Body.String())
	}
	stream := f.addImport(f.id, "streamed", importStream, importDone, time.Hour)
	if rec := f.do(http.MethodPost, importPath(f, "/"+strconv.FormatInt(stream, 10)+"/cutover"), `{"writesStopped":true}`); rec.Code != http.StatusConflict {
		t.Errorf("a cutover of a stream: %d %s", rec.Code, rec.Body.String())
	}
}

func TestAnEndedImportIsForgottenAndAuditedAndAReplicatingOneIsNot(t *testing.T) {
	f := newTransferFixture(t)
	ended := f.addImport(f.id, "done_one", importStream, importDone, time.Hour)
	if rec := f.do(http.MethodDelete, importPath(f, "/"+strconv.FormatInt(ended, 10)), ""); rec.Code != http.StatusOK {
		t.Fatalf("forgetting an ended import: %d %s", rec.Code, rec.Body.String())
	}
	if n := f.storeCount(`SELECT COUNT(*) FROM db_online_imports WHERE id = ` + strconv.FormatInt(ended, 10)); n != 0 {
		t.Error("the row is still there")
	}
	if got := f.audited("database.import.forget"); len(got) != 1 || !strings.Contains(got[0], "done_one") || strings.Contains(got[0], importSecret) {
		t.Errorf("audit = %v", got)
	}
}

func TestAnImportThatIsRunningCannotBeRemovedUnderItsJob(t *testing.T) {
	f := newTransferFixture(t)
	number := f.addImport(f.id, "jd_import_shop", importStream, importRunning, time.Hour)
	release := make(chan struct{})
	job := f.s.modules.jobs.Start(jobs.Spec{Kind: transferPrefix(f.id) + "import", Title: "Import shop_copy", Target: "shop"},
		func(ctx context.Context, out jobs.Emitter) error { <-release; return nil })
	defer close(release)
	rec := f.do(http.MethodDelete, importPath(f, "/"+strconv.FormatInt(number, 10)), "")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), job.ID) {
		t.Errorf("%d %s", rec.Code, rec.Body.String())
	}
	if status, _, _ := f.importStatus(number); status != importRunning {
		t.Errorf("the import is %s", status)
	}
	// The row is shown with the job doing it.
	var list importList
	if err := json.Unmarshal(f.do(http.MethodGet, importPath(f, ""), "").Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Imports) != 1 || list.Imports[0].Job == nil || list.Imports[0].Job.ID != job.ID {
		t.Errorf("imports = %+v", list.Imports)
	}
}

func TestAConnectionWithAnImportUnderWayIsNotForgotten(t *testing.T) {
	f := newTransferFixture(t)
	target := f.addConnection("pg", dbx.DriverSQLite, f.dbPath, false)
	number := f.addImport(target, "jd_import_shop", importReplication, importReplicating, time.Hour)
	rec := f.do(http.MethodDelete, "/databases/"+strconv.FormatInt(target, 10), "")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "import_active") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	f.s.setImport(number, importCancelled, "")
	if rec := f.do(http.MethodDelete, "/databases/"+strconv.FormatInt(target, 10), ""); rec.Code != http.StatusNoContent {
		t.Errorf("once it was over: %d %s", rec.Code, rec.Body.String())
	}
	if n := f.storeCount(`SELECT COUNT(*) FROM db_online_imports`); n != 0 {
		t.Errorf("the history of a forgotten connection stayed behind: %d rows", n)
	}
}

func TestOnlyOneImportOfAnameIsUnderWayAtATime(t *testing.T) {
	f := newTransferFixture(t)
	first := f.addImport(f.id, "jd_import_shop", importReplication, importReplicating, time.Hour)
	_, err := f.s.Store.DB.Exec(`
		INSERT INTO db_online_imports(connection_id, name, mode, status, target_database, source_label, created_at, updated_at)
		VALUES(?, 'jd_import_shop', 'replication', 'running', 'x', 'h/d', 0, 0)`, f.id)
	if err == nil {
		t.Fatal("two imports under way took the same name")
	}
	f.s.setImport(first, importDone, "")
	if _, err := f.s.Store.DB.Exec(`
		INSERT INTO db_online_imports(connection_id, name, mode, status, target_database, source_label, created_at, updated_at)
		VALUES(?, 'jd_import_shop', 'replication', 'running', 'x', 'h/d', 0, 0)`, f.id); err != nil {
		t.Errorf("the name was not free once the first import ended: %v", err)
	}
}

func TestSuggestedImportNamesAreNamesThePublicationCanHave(t *testing.T) {
	for database, want := range map[string]string{
		"shop": "jd_import_shop", "My-Shop 2": "jd_import_my_shop_2", "": "jd_import_database", "ÿ": "jd_import_database",
		strings.Repeat("a", 80): "jd_import_" + strings.Repeat("a", 40),
	} {
		got := suggestedImportName(database)
		if got != want || !dbx.ImportNameRe.MatchString(got) {
			t.Errorf("%q: %q, want %q", database, got, want)
		}
	}
}

// --- a copy to another server -------------------------------------------------

func TestCopyToAnotherServerRefusesWhatItCannotDo(t *testing.T) {
	f := newTransferFixture(t)
	from := f.addConnection("from", dbx.DriverPostgres, "postgres://app:"+importSecret+"@127.0.0.1:1/shop?sslmode=disable", false)
	toPostgres := f.addConnection("to", dbx.DriverPostgres, "postgres://app:"+importSecret+"@127.0.0.1:1/other?sslmode=disable", false)
	protected := f.addConnection("guarded", dbx.DriverPostgres, "postgres://app:"+importSecret+"@127.0.0.1:1/live?sslmode=disable", true)
	mysql := f.addConnection("mysql", dbx.DriverMySQL, "app:"+importSecret+"@tcp(127.0.0.1:1)/shop", false)

	copyTo := func(to int64, name string) *httptest.ResponseRecorder {
		return f.postTo(from, "/copy", `{"name":"`+name+`","to":`+strconv.FormatInt(to, 10)+`}`)
	}
	for name, c := range map[string]struct {
		rec  *httptest.ResponseRecorder
		code int
		want string
	}{
		"another engine":                 {copyTo(mysql, "copy"), http.StatusBadRequest, "same engine"},
		"a connection that is not there": {copyTo(99999, "copy"), http.StatusNotFound, ""},
		"a protected target":             {copyTo(protected, "copy"), http.StatusConflict, "protected"},
		"a bad name":                     {copyTo(toPostgres, "bad name!"), http.StatusBadRequest, "letters, digits and underscores"},
		"the target's own database":      {copyTo(toPostgres, "other"), http.StatusBadRequest, "already on"},
	} {
		if c.rec.Code != c.code || !strings.Contains(c.rec.Body.String(), c.want) {
			t.Errorf("%s: %d %s", name, c.rec.Code, c.rec.Body.String())
		}
		if strings.Contains(c.rec.Body.String(), importSecret) {
			t.Errorf("%s: the answer carries a password", name)
		}
	}
}

// Past the checks it is a job, and when the servers do not answer the job fails
// with a message that carries neither password.
func TestCopyToAnotherServerIsAJobThatFailsWithoutLeakingEitherPassword(t *testing.T) {
	f := newTransferFixture(t)
	from := f.addConnection("from", dbx.DriverPostgres, "postgres://app:"+importSecret+"@127.0.0.1:1/shop?sslmode=disable", false)
	to := f.addConnection("to", dbx.DriverPostgres, "postgres://app:other-"+importSecret+"@127.0.0.1:1/other?sslmode=disable", false)

	rec := f.postTo(from, "/copy", `{"name":"shop_copy","to":`+strconv.FormatInt(to, 10)+`}`)
	job, lines := f.waitJob(rec)
	if job.Status != jobs.StatusFailed {
		t.Fatalf("job = %s", job.Status)
	}
	text := job.Error
	for _, line := range lines {
		text += "\n" + line.Text
	}
	if strings.Contains(text, importSecret) {
		t.Errorf("the job carries a password: %s", text)
	}
	if got := f.audited("database.copy"); len(got) != 1 || !strings.Contains(got[0], `"toConnection":"to"`) {
		t.Errorf("audit of the request = %v", got)
	}
	if got := f.audited("database.copy.finish"); len(got) != 1 || strings.Contains(got[0], importSecret) {
		t.Errorf("audit of the end = %v", got)
	}
}

func TestCopyToAnotherServerNeedsTheTargetToBeFree(t *testing.T) {
	f := newTransferFixture(t)
	from := f.addConnection("from", dbx.DriverPostgres, "postgres://app:x@127.0.0.1:1/shop?sslmode=disable", false)
	to := f.addConnection("to", dbx.DriverPostgres, "postgres://app:x@127.0.0.1:1/other?sslmode=disable", false)
	release := make(chan struct{})
	f.s.modules.jobs.Start(jobs.Spec{Kind: transferPrefix(to) + "backup", Title: "Dump to", Target: "to"},
		func(ctx context.Context, out jobs.Emitter) error { <-release; return nil })
	defer close(release)
	rec := f.postTo(from, "/copy", `{"name":"shop_copy","to":`+strconv.FormatInt(to, 10)+`}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "transfer_running") {
		t.Errorf("%d %s", rec.Code, rec.Body.String())
	}
}
