package api

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
	"github.com/go-chi/chi/v5"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// The transfer routes against real servers: the parts the SQLite tests cannot
// reach — an engine's own dump tool running as a job, a database made to
// restore into, a statement refused by the server rather than by the
// classifier. Each skips when its engine is not reachable.

func liveUpload(t *testing.T, r http.Handler, path, options, filename string, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if options != "" {
		mw.WriteField("options", options)
	}
	part, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	part.Write(content)
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func liveJob(t *testing.T, s *Server, rec *httptest.ResponseRecorder) (jobs.Job, []jobs.Line) {
	t.Helper()
	if rec.Code != http.StatusAccepted {
		t.Fatalf("the route did not start a job: %d %s", rec.Code, rec.Body.String())
	}
	var started jobs.Job
	if err := json.Unmarshal(rec.Body.Bytes(), &started); err != nil || started.ID == "" {
		t.Fatalf("no job in the answer: %s", rec.Body.String())
	}
	deadline := time.Now().Add(2 * time.Minute)
	for {
		job, lines, ok := s.modules.jobs.Get(started.ID)
		if !ok {
			t.Fatalf("job %s is gone", started.ID)
		}
		if job.Status != jobs.StatusRunning {
			return job, lines
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s is still running", started.ID)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestLiveAPIPostgresTransfer(t *testing.T) {
	dsn := envOr("JD_TEST_POSTGRES_DSN", "postgres://jdtest:jdtest@127.0.0.1:5432/jdtest?sslmode=disable")
	s, r, id := liveAPIRouter(t, dbx.DriverPostgres, dsn)
	s.Cfg.BackupLocalDir = t.TempDir()
	ctx := context.Background()
	info, err := dbx.ParseDSN(dbx.DriverPostgres, dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := func(sql string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"query": sql})
		return do(t, r, http.MethodPost, pathf("/databases/%d/query", id), string(body))
	}
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS jd_api_lines`, `DROP TABLE IF EXISTS jd_api_orders`,
		`CREATE TABLE jd_api_orders (id integer PRIMARY KEY, customer text NOT NULL UNIQUE, total numeric(10,2))`,
		`CREATE TABLE jd_api_lines (id serial PRIMARY KEY, order_id integer NOT NULL REFERENCES jd_api_orders(id), sku text)`,
		`INSERT INTO jd_api_orders VALUES (1, 'ann', 10.50), (2, 'bo', 20)`,
		`INSERT INTO jd_api_lines (order_id, sku) VALUES (1, 'a'), (1, 'b'), (2, 'c')`,
	} {
		if rec := query(stmt); rec.Code != http.StatusOK {
			t.Fatalf("seed: %d %s\n%s", rec.Code, rec.Body.String(), stmt)
		}
	}
	scratch := []string{info.Database + "_api_new", info.Database + "_api_copy"}
	dropScratch := func() {
		for _, name := range scratch {
			_, _ = dbx.DropDatabase(context.Background(), dbx.DriverPostgres, dsn, name)
		}
	}
	dropScratch()
	t.Cleanup(func() {
		query(`DROP TABLE IF EXISTS jd_api_lines`)
		query(`DROP TABLE IF EXISTS jd_api_orders`)
		dropScratch()
	})

	t.Run("export_query_is_read_only_on_the_server", func(t *testing.T) {
		rec := do(t, r, http.MethodPost, pathf("/databases/%d/export/query", id),
			`{"sql":"SELECT o.customer, count(*) AS lines FROM jd_api_orders o JOIN jd_api_lines l ON l.order_id = o.id GROUP BY o.customer ORDER BY 1","format":"ndjson"}`)
		if rec.Code != http.StatusOK || rec.Body.String() != "{\"customer\":\"ann\",\"lines\":\"2\"}\n{\"customer\":\"bo\",\"lines\":\"1\"}\n" {
			t.Fatalf("query export: %d %s", rec.Code, rec.Body.String())
		}
		// A statement that reads as a SELECT and writes: the classifier lets
		// it by, and the read-only transaction does not.
		rec = do(t, r, http.MethodPost, pathf("/databases/%d/export/query", id),
			`{"sql":"SELECT nextval('jd_api_lines_id_seq')","format":"csv"}`)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "read-only") {
			t.Errorf("a writing function ran inside an export: %d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("export_sql_is_in_the_source_dialect", func(t *testing.T) {
		rec := do(t, r, http.MethodGet, pathf("/databases/%d/export", id)+"?schema=public&table=jd_api_orders&format=sql&orderBy=id", "")
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(),
			`INSERT INTO "public"."jd_api_orders" ("id", "customer", "total") VALUES (1, 'ann', '10.50'), (2, 'bo', '20.00');`) {
			t.Errorf("sql export: %d\n%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("import_upload_skips_a_bad_row_and_keeps_the_rest", func(t *testing.T) {
		rec := liveUpload(t, r, pathf("/databases/%d/import/upload", id),
			`{"schema":"public","table":"jd_api_orders","skipBadRows":true}`, "orders.csv",
			[]byte("id,customer,total\n3,cy,5\n1,dupe,1\n4,di,oops\n5,ed,7.25\n"))
		var report dbx.ImportReport
		json.Unmarshal(rec.Body.Bytes(), &report)
		if rec.Code != http.StatusOK || report.Inserted != 2 || report.Skipped != 2 || len(report.Errors) != 2 {
			t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
		}
		if report.Errors[0].Line != 3 || report.Errors[1].Line != 4 {
			t.Errorf("error lines = %+v", report.Errors)
		}
	})

	var dumped string
	t.Run("backup_is_a_job_with_the_tools_own_lines", func(t *testing.T) {
		job, lines := liveJob(t, s, do(t, r, http.MethodPost, pathf("/databases/%d/backup", id),
			`{"tables":["public.jd_api_orders","public.jd_api_lines"],"note":"two tables"}`))
		if job.Status != jobs.StatusSucceeded {
			t.Fatalf("job = %+v\n%+v", job, lines)
		}
		for _, line := range lines {
			if line.Stream == "result" {
				var res transferResult
				json.Unmarshal([]byte(line.Text), &res)
				dumped = res.File
			}
			if strings.Contains(line.Text, "jdtest:") || strings.Contains(line.Text, "PGPASSWORD") {
				t.Errorf("a job line carries a credential: %s", line.Text)
			}
		}
		if dumped == "" {
			t.Fatalf("no result line: %+v", lines)
		}
		rec := do(t, r, http.MethodGet, pathf("/databases/%d/backups", id), "")
		if !strings.Contains(rec.Body.String(), `"note":"two tables"`) || !strings.Contains(rec.Body.String(), dumped) {
			t.Errorf("listing: %s", rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), `"tool":"pg_dump"`) && !strings.Contains(rec.Body.String(), `"format":"pg_dump archive"`) {
			t.Errorf("a pg_dump archive is not listed as one: %s", rec.Body.String())
		}
	})

	t.Run("restore_into_a_new_database_leaves_this_one_alone", func(t *testing.T) {
		if dumped == "" {
			t.Skip("no dump to restore")
		}
		query(`DELETE FROM jd_api_lines`)
		job, lines := liveJob(t, s, do(t, r, http.MethodPost, pathf("/databases/%d/restore", id),
			`{"file":"`+dumped+`","target":{"newDatabase":"`+scratch[0]+`"}}`))
		if job.Status != jobs.StatusSucceeded {
			t.Fatalf("job = %+v\n%+v", job, lines)
		}
		restored, err := dbx.OpenDatabase(ctx, dbx.DriverPostgres, dsn, scratch[0])
		if err != nil {
			t.Fatal(err)
		}
		defer restored.Close()
		var n int
		if err := restored.QueryRow(`SELECT count(*) FROM jd_api_lines`).Scan(&n); err != nil || n != 3 {
			t.Errorf("the new database holds %d lines (%v), want 3", n, err)
		}
		// The database the dump came from was not what was restored into.
		rec := query(`SELECT count(*) FROM jd_api_lines`)
		if !strings.Contains(rec.Body.String(), `[["0"]]`) {
			t.Errorf("restoring into a new database changed this one: %s", rec.Body.String())
		}
		// A second time, the name is taken, and that is said rather than
		// restored over.
		job, _ = liveJob(t, s, do(t, r, http.MethodPost, pathf("/databases/%d/restore", id),
			`{"file":"`+dumped+`","target":{"newDatabase":"`+scratch[0]+`"}}`))
		if job.Status != jobs.StatusFailed || !strings.Contains(job.Error, "already exists") {
			t.Errorf("a restore into a database that exists: %+v", job)
		}
	})

	t.Run("restore_into_this_database_keeps_the_current_state_first", func(t *testing.T) {
		if dumped == "" {
			t.Skip("no dump to restore")
		}
		job, lines := liveJob(t, s, do(t, r, http.MethodPost, pathf("/databases/%d/restore", id),
			`{"file":"`+dumped+`","dumpFirst":true}`))
		if job.Status != jobs.StatusSucceeded {
			t.Fatalf("job = %+v\n%+v", job, lines)
		}
		var res transferResult
		for _, line := range lines {
			if line.Stream == "result" {
				json.Unmarshal([]byte(line.Text), &res)
			}
		}
		if res.SafetyDump == "" {
			t.Errorf("no safety dump named: %+v", res)
		}
		if _, err := os.Stat(s.dbDumpDir("live-postgres") + "/" + res.SafetyDump); err != nil {
			t.Errorf("the safety dump is not in the dump directory: %v", err)
		}
		// The pool the pages use was closed for the restore and works after it.
		rec := query(`SELECT count(*) FROM jd_api_lines`)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `[["3"]]`) {
			t.Errorf("after the restore: %d %s", rec.Code, rec.Body.String())
		}
	})

	// A dump that is stopped stops, and leaves nothing that looks like one.
	// The table is locked so the dump has something to wait on.
	t.Run("a_running_dump_can_be_stopped", func(t *testing.T) {
		holder, err := dbx.OpenDatabase(ctx, dbx.DriverPostgres, dsn, info.Database)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Close()
		tx, err := holder.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx, `LOCK TABLE jd_api_orders IN ACCESS EXCLUSIVE MODE`); err != nil {
			t.Fatal(err)
		}
		before, _ := os.ReadDir(s.dbDumpDir("live-postgres"))

		rec := do(t, r, http.MethodPost, pathf("/databases/%d/backup", id), `{"tables":["public.jd_api_orders"]}`)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("backup: %d %s", rec.Code, rec.Body.String())
		}
		var started jobs.Job
		json.Unmarshal(rec.Body.Bytes(), &started)
		// While it runs, a second is refused and the listing shows the first.
		if again := do(t, r, http.MethodPost, pathf("/databases/%d/backup", id), `{}`); again.Code != http.StatusConflict {
			t.Errorf("a second dump while one runs: %d %s", again.Code, again.Body.String())
		}
		time.Sleep(500 * time.Millisecond)
		if job, _, _ := s.modules.jobs.Get(started.ID); job.Status != jobs.StatusRunning {
			t.Fatalf("the dump did not wait on the lock: %+v", job)
		}
		if !s.modules.jobs.Cancel(started.ID) {
			t.Fatal("the job could not be cancelled")
		}
		job, lines := liveJob(t, s, rec)
		if job.Status != jobs.StatusCancelled {
			t.Fatalf("job = %+v\n%+v", job, lines)
		}
		after, _ := os.ReadDir(s.dbDumpDir("live-postgres"))
		if len(after) != len(before) {
			t.Errorf("a stopped dump left a file: %d entries before, %d after", len(before), len(after))
		}
		// And the connection is free for the next one.
		tx.Rollback()
		if job, _ := liveJob(t, s, do(t, r, http.MethodPost, pathf("/databases/%d/backup", id), `{"tables":["public.jd_api_orders"]}`)); job.Status != jobs.StatusSucceeded {
			t.Errorf("a dump after a stopped one: %+v", job)
		}
	})

	t.Run("copy_makes_a_database_with_the_same_rows", func(t *testing.T) {
		job, lines := liveJob(t, s, do(t, r, http.MethodPost, pathf("/databases/%d/copy", id), `{"name":"`+scratch[1]+`"}`))
		if job.Status != jobs.StatusSucceeded {
			t.Fatalf("job = %+v\n%+v", job, lines)
		}
		copied, err := dbx.OpenDatabase(ctx, dbx.DriverPostgres, dsn, scratch[1])
		if err != nil {
			t.Fatal(err)
		}
		defer copied.Close()
		var n int
		if err := copied.QueryRow(`SELECT count(*) FROM jd_api_orders`).Scan(&n); err != nil || n < 2 {
			t.Errorf("the copy holds %d orders (%v)", n, err)
		}
		// The dump a copy takes is its own, and is gone when it ends.
		entries, _ := os.ReadDir(s.Cfg.BackupLocalDir + "/databases")
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".copy-") {
				t.Errorf("a copy left its working directory behind: %s", e.Name())
			}
		}
		if rec := do(t, r, http.MethodPost, pathf("/databases/%d/copy", id), `{"name":"`+info.Database+`"}`); rec.Code != http.StatusBadRequest {
			t.Errorf("a copy onto the database itself: %d %s", rec.Code, rec.Body.String())
		}
	})
}

func TestLiveAPIMongoTransfer(t *testing.T) {
	dsn := envOr("JD_TEST_MONGO_DSN", "mongodb://127.0.0.1:27017/jdtest")
	s, r, id := liveAPIRouter(t, dbx.DriverMongo, dsn)
	s.Cfg.BackupLocalDir = t.TempDir()
	const coll = "jd_api_upload"
	drop := func() {
		do(t, r, http.MethodDelete, pathf("/databases/%d/collections", id), `{"collection":"`+coll+`"}`)
	}
	drop()
	t.Cleanup(drop)

	ctx := context.Background()
	client, err := dbx.MongoClient(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect(ctx)
	info, _ := dbx.ParseDSN(dbx.DriverMongo, dsn)
	target := client.Database(info.Database).Collection(coll)
	count := func() int64 {
		n, err := target.CountDocuments(ctx, map[string]any{})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}

	// One document per line, uploaded and inserted.
	rec := liveUpload(t, r, pathf("/databases/%d/import/upload", id), `{"table":"`+coll+`"}`, "docs.ndjson",
		[]byte(`{"_id":1,"name":"Ann","tags":["a"]}`+"\n"+`{"_id":2,"name":"Bo"}`+"\n"))
	var report dbx.ImportReport
	json.Unmarshal(rec.Body.Bytes(), &report)
	if rec.Code != http.StatusOK || report.Inserted != 2 || count() != 2 {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := target.Indexes().CreateOne(ctx, uniqueNameIndex()); err != nil {
		t.Fatal(err)
	}

	// A replace whose file breaks the collection's own rule leaves the
	// collection as it was: the swap is the last step, and it is refused.
	rec = liveUpload(t, r, pathf("/databases/%d/import/upload", id), `{"table":"`+coll+`","mode":"replace"}`, "dupes.json",
		[]byte(`[{"_id":10,"name":"Same"},{"_id":11,"name":"Same"}]`))
	if rec.Code != http.StatusBadRequest || count() != 2 {
		t.Fatalf("a replace that breaks a unique index: %d %s (%d documents)", rec.Code, rec.Body.String(), count())
	}
	// One that does not, replaces — and the index is still there after.
	rec = liveUpload(t, r, pathf("/databases/%d/import/upload", id), `{"table":"`+coll+`","mode":"replace"}`, "fresh.json",
		[]byte(`[{"_id":20,"name":"Cy"},{"_id":21,"name":"Di"},{"_id":22,"name":"Ed"}]`))
	json.Unmarshal(rec.Body.Bytes(), &report)
	if rec.Code != http.StatusOK || report.Inserted != 3 || !report.Atomic || count() != 3 {
		t.Fatalf("replace: %d %s (%d documents)", rec.Code, rec.Body.String(), count())
	}
	if _, err := target.InsertOne(ctx, map[string]any{"name": "Cy"}); err == nil {
		t.Error("the unique index did not survive the replace")
	}
	// Nothing is left of the collection the documents were staged in.
	names, _ := client.Database(info.Database).ListCollectionNames(ctx, map[string]any{})
	for _, name := range names {
		if strings.HasPrefix(name, "jd_import_") {
			t.Errorf("a staging collection was left behind: %s", name)
		}
	}

	// The inline route's replace reads the data through before it removes
	// anything: a file with a mistake in it used to cost the collection.
	rec = do(t, r, http.MethodPost, pathf("/databases/%d/import", id),
		`{"table":"`+coll+`","format":"json","truncate":true,"data":"[{\"name\":\"ok\"},{broken"}`)
	if rec.Code != http.StatusBadRequest || count() != 3 {
		t.Fatalf("an inline replace with broken data: %d %s (%d documents)", rec.Code, rec.Body.String(), count())
	}
	rec = do(t, r, http.MethodPost, pathf("/databases/%d/import", id),
		`{"table":"`+coll+`","format":"json","truncate":true,"data":"[{\"name\":\"only\"}]"}`)
	if rec.Code != http.StatusOK || count() != 1 {
		t.Fatalf("an inline replace: %d %s (%d documents)", rec.Code, rec.Body.String(), count())
	}
	if _, err := target.InsertOne(ctx, map[string]any{"name": "only"}); err == nil {
		t.Error("the unique index did not survive the inline replace")
	}

	// A server that cannot be reached is an error, not an empty file.
	unreachable, router, bad := liveAPIRouterUnchecked(t, dbx.DriverMongo, "mongodb://127.0.0.1:1/x?serverSelectionTimeoutMS=300&connectTimeoutMS=300")
	_ = unreachable
	rec = do(t, router, http.MethodGet, pathf("/databases/%d/export", bad)+"?table=things&format=csv", "")
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "connect_failed") || rec.Header().Get("Content-Disposition") != "" {
		t.Errorf("export from an unreachable server: %d %s", rec.Code, rec.Body.String())
	}
}

func TestLiveAPIRedisBackupChoosesDatabases(t *testing.T) {
	dsn := os.Getenv("JD_TEST_REDIS_OWN_DSN")
	if dsn == "" {
		t.Skip("set JD_TEST_REDIS_OWN_DSN to a Redis server this test may flush")
	}
	s, r, id := liveAPIRouter(t, dbx.DriverRedis, dsn)
	s.Cfg.BackupLocalDir = t.TempDir()
	ctx := context.Background()
	client, err := dbx.RedisClient(ctx, dsn, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	conn := client.Conn()
	defer conn.Close()
	conn.FlushAll(ctx)
	t.Cleanup(func() { conn.FlushAll(context.Background()) })
	for db, key := range map[int]string{0: "zero", 3: "three"} {
		conn.Select(ctx, db)
		conn.Set(ctx, key, "v", 0)
	}

	job, lines := liveJob(t, s, do(t, r, http.MethodPost, pathf("/databases/%d/backup", id), `{}`))
	if job.Status != jobs.StatusSucceeded {
		t.Fatalf("job = %+v\n%+v", job, lines)
	}
	rec := do(t, r, http.MethodGet, pathf("/databases/%d/backups", id), "")
	if !strings.Contains(rec.Body.String(), `"database":"0,3"`) || !strings.Contains(rec.Body.String(), "2 keys in 2 databases") {
		t.Errorf("a dump of every database is listed as %s", rec.Body.String())
	}
	job, _ = liveJob(t, s, do(t, r, http.MethodPost, pathf("/databases/%d/backup", id), `{"databases":[3]}`))
	if job.Status != jobs.StatusSucceeded {
		t.Fatalf("job = %+v", job)
	}
	rec = do(t, r, http.MethodGet, pathf("/databases/%d/backups", id), "")
	if !strings.Contains(rec.Body.String(), `"databases":[3]`) {
		t.Errorf("the chosen databases are not recorded: %s", rec.Body.String())
	}
	for name, body := range map[string]string{
		"tables on redis":     `{"tables":["k"]}`,
		"a negative database": `{"databases":[-1]}`,
		"structure on redis":  `{"schemaOnly":true}`,
	} {
		if rec := do(t, r, http.MethodPost, pathf("/databases/%d/backup", id), body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
}

// uniqueNameIndex is a unique index on a collection's name field.
func uniqueNameIndex() mongo.IndexModel {
	return mongo.IndexModel{Keys: bson.D{{Key: "name", Value: 1}}, Options: options.Index().SetUnique(true)}
}

// liveAPIRouterUnchecked is liveAPIRouter for a connection that is expected
// not to answer: the row is saved and nothing is pinged.
func liveAPIRouterUnchecked(t *testing.T, driver dbx.Driver, dsn string) (*Server, http.Handler, int64) {
	t.Helper()
	s := testServer(t)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			p := &httpx.Principal{
				User: &auth.User{ID: 1, Username: "tester"},
				Role: auth.RoleAdmin, Kind: "session", IP: "127.0.0.1",
			}
			next.ServeHTTP(w, req.WithContext(httpx.WithPrincipal(req.Context(), p)))
		})
	})
	s.mountDatabaseRoutes(r)
	sealed, err := s.Sealer.Seal(dsn)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Store.DB.Exec(
		`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES(?,?,?,?)`,
		"unreachable-"+string(driver), string(driver), sealed, 0)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return s, r, id
}
