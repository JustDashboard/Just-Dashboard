package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
)

// Bringing a database over, against real servers: the routes driving the
// whole sequence, with nothing but the HTTP surface between the test and the
// engines. Everything made is called jdt_online_import_*.

// waitImportJob follows the job a start or a cutover answered with to its end.
func waitImportJob(t *testing.T, s *Server, rec *httptest.ResponseRecorder) (jobs.Job, []jobs.Line) {
	t.Helper()
	if rec.Code != http.StatusAccepted {
		t.Fatalf("the route did not start a job: %d %s", rec.Code, rec.Body.String())
	}
	// A start answers with the import and its job; a cutover and a copy with the job.
	var answer struct {
		Job jobs.Job `json:"job"`
		ID  string   `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatal(err)
	}
	id := answer.ID
	if answer.Job.ID != "" {
		id = answer.Job.ID
	}
	deadline := time.Now().Add(2 * time.Minute)
	for {
		job, lines, ok := s.modules.jobs.Get(id)
		if !ok {
			t.Fatalf("job %q is gone: %s", id, rec.Body.String())
		}
		if job.Status != jobs.StatusRunning {
			return job, lines
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s is still running", id)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func importLiveRead(t *testing.T, rec *httptest.ResponseRecorder, into any) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), into); err != nil {
		t.Fatalf("%v: %s", err, rec.Body.String())
	}
}

func importLiveCount(t *testing.T, dsn, query string, args ...any) int64 {
	t.Helper()
	db, err := dbx.OpenDatabase(context.Background(), dbx.DriverPostgres, dsn, "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int64
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%v\n%s", err, query)
	}
	return n
}

func TestLiveAPIOnlineImportReplicatesAndCutsOver(t *testing.T) {
	publisher := os.Getenv("JD_TEST_POSTGRES_DSN")
	subscriber := os.Getenv("JD_TEST_POSTGRES_SUBSCRIBER_DSN")
	if publisher == "" || subscriber == "" {
		t.Skip("JD_TEST_POSTGRES_DSN and JD_TEST_POSTGRES_SUBSCRIBER_DSN name the two servers")
	}
	s, r, id := liveAPIRouter(t, dbx.DriverPostgres, subscriber)
	s.Cfg.BackupLocalDir = t.TempDir()
	ctx := context.Background()
	const (
		sourceName = "jdt_online_import_api_src"
		first      = "jdt_online_import_api_a"
		second     = "jdt_online_import_api_b"
	)
	source := dbx.DSNForDatabase(dbx.DriverPostgres, publisher, sourceName)
	removeAll := func() {
		for _, name := range []string{first, second} {
			_ = dbx.DropSubscription(ctx, dbx.DSNForDatabase(dbx.DriverPostgres, subscriber, name), name)
			_ = dbx.DropSlot(ctx, source, name)
			_ = dbx.DropPublication(ctx, source, name)
			_, _ = dbx.DropDatabase(ctx, dbx.DriverPostgres, subscriber, name)
		}
	}
	removeAll()
	_, _ = dbx.DropDatabase(ctx, dbx.DriverPostgres, publisher, sourceName)
	if err := dbx.CreateDatabase(ctx, dbx.DriverPostgres, publisher, sourceName); err != nil {
		t.Skipf("this login cannot create a database on the publisher: %v", err)
	}
	t.Cleanup(func() {
		removeAll()
		_, _ = dbx.DropDatabase(ctx, dbx.DriverPostgres, publisher, sourceName)
	})
	src, err := dbx.OpenDatabase(ctx, dbx.DriverPostgres, source, "")
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	for _, stmt := range []string{
		`CREATE TABLE orders (id bigserial PRIMARY KEY, customer text NOT NULL, total numeric(10,2))`,
		`CREATE TABLE tags (name text)`,
		`INSERT INTO orders (customer, total) SELECT 'customer ' || g, g FROM generate_series(1, 500) g`,
		`INSERT INTO tags VALUES ('a'), ('b')`,
	} {
		if _, err := src.Exec(stmt); err != nil {
			t.Fatalf("%v\n%s", err, stmt)
		}
	}
	password := func() string {
		info, _ := dbx.ParseDSN(dbx.DriverPostgres, publisher)
		return info.Password
	}()
	startBody := func(database, name string) string {
		b, _ := json.Marshal(map[string]any{"source": source, "mode": "replication", "database": database, "name": name})
		return string(b)
	}
	inspectBody, _ := json.Marshal(map[string]any{"source": source, "name": first})

	// A table with no key is said before anything is made.
	rec := do(t, r, http.MethodPost, pathf("/databases/%d/imports/inspect", id), string(inspectBody))
	var inspected importInspection
	importLiveRead(t, rec, &inspected)
	if !inspected.Reachable || inspected.ReplicationReady || inspected.Replication == nil ||
		!strings.Contains(strings.Join(inspected.Replication.Blockers, "\n"), `ALTER TABLE "public"."tags" REPLICA IDENTITY FULL`) {
		t.Fatalf("inspection = %+v", inspected)
	}
	if password != "" && strings.Contains(rec.Body.String(), password) {
		t.Error("the inspection carries the source's password")
	}
	if rec := do(t, r, http.MethodPost, pathf("/databases/%d/imports", id), startBody(first, first)); rec.Code == http.StatusAccepted {
		job, _ := waitImportJob(t, s, rec)
		if job.Status != jobs.StatusFailed {
			t.Fatalf("an import of a source with a keyless table %s", job.Status)
		}
		var rows importList
		importLiveRead(t, do(t, r, http.MethodGet, pathf("/databases/%d/imports", id), ""), &rows)
		if len(rows.Imports) != 1 || rows.Imports[0].Status != importFailed {
			t.Fatalf("imports = %+v", rows.Imports)
		}
		if exists, _ := dbx.DatabaseExists(ctx, dbx.DriverPostgres, subscriber, first); exists {
			t.Error("a failed import left its database behind")
		}
		if n := importLiveCount(t, publisher, `SELECT count(*) FROM pg_replication_slots WHERE slot_name = $1`, first); n != 0 {
			t.Error("a failed import left a slot on the source")
		}
		if rec := do(t, r, http.MethodDelete, pathf("/databases/%d/imports/", id)+jsonID(t, rows.Imports[0].ID), ""); rec.Code != http.StatusOK {
			t.Fatalf("forget: %d %s", rec.Code, rec.Body.String())
		}
	} else {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}

	if _, err := src.Exec(`ALTER TABLE tags REPLICA IDENTITY FULL`); err != nil {
		t.Fatal(err)
	}
	rec = do(t, r, http.MethodPost, pathf("/databases/%d/imports", id), startBody(first, first))
	var started importStarted
	importLiveRead2(t, rec, http.StatusAccepted, &started)
	if strings.Contains(rec.Body.String(), "source_enc") || (password != "" && strings.Contains(rec.Body.String(), password)) {
		t.Errorf("the answer carries the source's string: %s", rec.Body.String())
	}
	job, lines := waitImportJob(t, s, rec)
	if job.Status != jobs.StatusSucceeded {
		t.Fatalf("job = %s %q\n%v", job.Status, job.Error, lines)
	}
	for _, line := range lines {
		if password != "" && strings.Contains(line.Text, password) {
			t.Errorf("a job line carries the password: %s", line.Text)
		}
	}
	one := pathf("/databases/%d/imports/", id) + jsonID(t, started.Import.ID)

	// The target's account of the copy, and the source's of the lag.
	var live importLive
	deadline := time.Now().Add(60 * time.Second)
	for {
		importLiveRead(t, do(t, r, http.MethodGet, one, ""), &live)
		if live.Subscription != nil && live.Subscription.Ready == 2 && live.Subscription.Applying {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the copy did not finish: %+v", live)
		}
		time.Sleep(300 * time.Millisecond)
	}
	if live.Import.Status != importReplicating || live.Lag == nil || !live.Lag.Exists {
		t.Fatalf("live = %+v", live)
	}
	target := dbx.DSNForDatabase(dbx.DriverPostgres, subscriber, first)
	if n := importLiveCount(t, target, `SELECT count(*) FROM orders`); n != 500 {
		t.Errorf("orders = %d", n)
	}

	// The cutover will not take the operator's word for nothing.
	if rec := do(t, r, http.MethodPost, one+"/cutover", `{}`); rec.Code != http.StatusBadRequest {
		t.Errorf("a cutover without writesStopped: %d", rec.Code)
	}
	// The same name cannot be used twice while it is held.
	if rec := do(t, r, http.MethodPost, pathf("/databases/%d/imports", id), startBody(second, first)); rec.Code != http.StatusConflict {
		t.Errorf("a second import under the same name: %d %s", rec.Code, rec.Body.String())
	}
	// The connection cannot be forgotten while it holds a subscription.
	if rec := do(t, r, http.MethodDelete, pathf("/databases/%d", id), ""); rec.Code != http.StatusConflict {
		t.Errorf("forgetting a connection with an import under way: %d", rec.Code)
	}

	if _, err := src.Exec(`INSERT INTO orders (customer, total) VALUES ('late', 1); UPDATE orders SET customer = 'renamed' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	job, lines = waitImportJob(t, s, do(t, r, http.MethodPost, one+"/cutover", `{"writesStopped":true}`))
	if job.Status != jobs.StatusSucceeded {
		t.Fatalf("cutover = %s %q\n%v", job.Status, job.Error, lines)
	}
	var after importLive
	importLiveRead(t, do(t, r, http.MethodGet, one, ""), &after)
	if after.Import.Status != importDone || after.Subscription != nil {
		t.Errorf("after the cutover: %+v", after)
	}
	var sealed string
	if err := s.Store.DB.QueryRow(`SELECT source_enc FROM db_online_imports WHERE id = ?`, started.Import.ID).Scan(&sealed); err != nil || sealed != "" {
		t.Errorf("the source's string outlived the import: %q %v", sealed, err)
	}
	if n := importLiveCount(t, target, `SELECT count(*) FROM orders`); n != 501 {
		t.Errorf("orders after the cutover = %d, want 501", n)
	}
	if n := importLiveCount(t, target, `SELECT last_value FROM orders_id_seq`); n != 501 {
		t.Errorf("the sequence = %d, want 501", n)
	}
	if n := importLiveCount(t, publisher, `SELECT count(*) FROM pg_replication_slots WHERE slot_name = $1`, first); n != 0 {
		t.Error("the slot is still on the source")
	}
	if n := importLiveCount(t, source, `SELECT count(*) FROM pg_publication WHERE pubname = $1`, first); n != 0 {
		t.Error("the publication is still on the source")
	}
	// Done, it is forgotten like any ended import, and the connection is free.
	if rec := do(t, r, http.MethodDelete, one, ""); rec.Code != http.StatusOK {
		t.Errorf("forget: %d %s", rec.Code, rec.Body.String())
	}

	// And one that is stopped instead leaves nothing on either server.
	rec = do(t, r, http.MethodPost, pathf("/databases/%d/imports", id), startBody(second, second))
	importLiveRead2(t, rec, http.StatusAccepted, &started)
	if job, lines := waitImportJob(t, s, rec); job.Status != jobs.StatusSucceeded {
		t.Fatalf("second import = %s %q\n%v", job.Status, job.Error, lines)
	}
	two := pathf("/databases/%d/imports/", id) + jsonID(t, started.Import.ID)
	var removed importRemoved
	importLiveRead(t, do(t, r, http.MethodDelete, two, ""), &removed)
	if len(removed.Warnings) != 0 {
		t.Errorf("warnings = %v", removed.Warnings)
	}
	var stopped importLive
	importLiveRead(t, do(t, r, http.MethodGet, two, ""), &stopped)
	if stopped.Import.Status != importCancelled {
		t.Errorf("status = %s", stopped.Import.Status)
	}
	if n := importLiveCount(t, publisher, `SELECT count(*) FROM pg_replication_slots WHERE slot_name = $1`, second); n != 0 {
		t.Error("a stopped import left its slot on the source")
	}
	if n := importLiveCount(t, source, `SELECT count(*) FROM pg_publication WHERE pubname = $1`, second); n != 0 {
		t.Error("a stopped import left its publication on the source")
	}
	if state, err := dbx.ReadSubscription(ctx, dbx.DSNForDatabase(dbx.DriverPostgres, subscriber, second), second); err != nil || state.Exists {
		t.Errorf("a stopped import left its subscription: %+v %v", state, err)
	}
}

func importLiveRead2(t *testing.T, rec *httptest.ResponseRecorder, code int, into any) {
	t.Helper()
	if rec.Code != code {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), into); err != nil {
		t.Fatalf("%v: %s", err, rec.Body.String())
	}
}

func jsonID(t *testing.T, id int64) string {
	t.Helper()
	b, _ := json.Marshal(id)
	return string(b)
}

// Two servers of the one wire protocol: a database goes from MariaDB to MySQL
// as the source is read, by the import and by a copy between two saved
// connections.
func TestLiveAPICopiesAndImportsFromMariaDBToMySQL(t *testing.T) {
	from := os.Getenv("JD_TEST_MYSQL_ADMIN_DSN")
	to := os.Getenv("JD_TEST_MYSQL8_ADMIN_DSN")
	if from == "" || to == "" {
		t.Skip("JD_TEST_MYSQL_ADMIN_DSN and JD_TEST_MYSQL8_ADMIN_DSN name the MariaDB and the MySQL server")
	}
	ctx := context.Background()
	const (
		sourceName = "jdt_online_import_api_src"
		imported   = "jdt_online_import_api_in"
		copied     = "jdt_online_import_api_cp"
	)
	for _, name := range []string{imported, copied} {
		_, _ = dbx.DropDatabase(ctx, dbx.DriverMySQL, to, name)
	}
	_, _ = dbx.DropDatabase(ctx, dbx.DriverMySQL, from, sourceName)
	if err := dbx.CreateDatabase(ctx, dbx.DriverMySQL, from, sourceName); err != nil {
		t.Skipf("this login cannot create a database: %v", err)
	}
	t.Cleanup(func() {
		_, _ = dbx.DropDatabase(ctx, dbx.DriverMySQL, from, sourceName)
		for _, name := range []string{imported, copied} {
			_, _ = dbx.DropDatabase(ctx, dbx.DriverMySQL, to, name)
		}
	})
	src, err := dbx.OpenDatabase(ctx, dbx.DriverMySQL, from, sourceName)
	if err != nil {
		t.Skipf("the MariaDB server does not answer: %v", err)
	}
	defer src.Close()
	for _, stmt := range []string{
		// A collation both servers know: MariaDB 11.4's default is one MySQL does not.
		`CREATE TABLE t (id INT AUTO_INCREMENT PRIMARY KEY, label VARCHAR(40) NOT NULL) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci`,
		`INSERT INTO t (label) VALUES ('one'), ('it''s two'), ('three')`,
	} {
		if _, err := src.Exec(stmt); err != nil {
			t.Fatalf("%v\n%s", err, stmt)
		}
	}
	sourceDSN := dbx.DSNForDatabase(dbx.DriverMySQL, from, sourceName)

	// The target, a MySQL server, is the connection the page is on.
	s, r, target := liveAPIRouter(t, dbx.DriverMySQL, to)
	s.Cfg.BackupLocalDir = t.TempDir()
	count := func(database string) int64 {
		db, err := dbx.OpenDatabase(ctx, dbx.DriverMySQL, to, database)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var n int64
		if err := db.QueryRow(`SELECT COUNT(*) FROM t`).Scan(&n); err != nil {
			t.Fatalf("%s: %v", database, err)
		}
		return n
	}

	// The import: one form on MySQL, a dump read as it loads.
	body, _ := json.Marshal(map[string]any{"source": sourceDSN, "database": imported})
	rec := do(t, r, http.MethodPost, pathf("/databases/%d/imports", target), string(body))
	var started importStarted
	importLiveRead2(t, rec, http.StatusAccepted, &started)
	if started.Import.Mode != importStream {
		t.Fatalf("mode = %s", started.Import.Mode)
	}
	job, lines := waitImportJob(t, s, rec)
	if job.Status != jobs.StatusSucceeded {
		t.Fatalf("job = %s %q\n%v", job.Status, job.Error, lines)
	}
	if n := count(imported); n != 3 {
		t.Errorf("imported rows = %d", n)
	}
	var live importLive
	importLiveRead(t, do(t, r, http.MethodGet, pathf("/databases/%d/imports/", target)+jsonID(t, started.Import.ID), ""), &live)
	if live.Import.Status != importDone || !strings.Contains(live.Import.Detail, "1 tables, 3 rows") {
		t.Errorf("import = %+v", live.Import)
	}
	// The same database again is refused rather than loaded over itself.
	rec = do(t, r, http.MethodPost, pathf("/databases/%d/imports", target), string(body))
	if job, _ := waitImportJob(t, s, rec); job.Status != jobs.StatusFailed || !strings.Contains(job.Error, "already exists") {
		t.Errorf("a second import into %s: %s %q", imported, job.Status, job.Error)
	}

	// The copy: from the MariaDB connection to the MySQL one.
	sealed, err := s.Sealer.Seal(dbx.DSNForDatabase(dbx.DriverMySQL, from, sourceName))
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Store.DB.Exec(`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES(?,?,?,?)`,
		"live-mariadb", string(dbx.DriverMySQL), sealed, 0)
	if err != nil {
		t.Fatal(err)
	}
	maria, _ := res.LastInsertId()
	copyBody, _ := json.Marshal(map[string]any{"name": copied, "to": target})
	job, lines = waitImportJob(t, s, do(t, r, http.MethodPost, pathf("/databases/%d/copy", maria), string(copyBody)))
	if job.Status != jobs.StatusSucceeded {
		t.Fatalf("copy = %s %q\n%v", job.Status, job.Error, lines)
	}
	if n := count(copied); n != 3 {
		t.Errorf("copied rows = %d", n)
	}
	var result transferResult
	for _, line := range lines {
		if line.Stream == "result" {
			_ = json.Unmarshal([]byte(line.Text), &result)
		}
	}
	if result.Database != copied || result.Connection != target || result.ConnectionName != "live-mysql" {
		t.Errorf("result = %+v", result)
	}
	// And the source is as it was.
	var kept int
	if err := src.QueryRow(`SELECT COUNT(*) FROM t`).Scan(&kept); err != nil || kept != 3 {
		t.Errorf("the source holds %d rows: %v", kept, err)
	}
}
