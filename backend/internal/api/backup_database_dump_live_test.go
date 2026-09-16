package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// A real PostgreSQL server is provisioned, filled with a canary row, dumped by
// a backup job, dropped, and restored from the archive into a drill database
// through the Databases owner. This is the journey the deployment gate relies
// on when it accepts native dump coverage for a linked database.
func TestLiveBackupDumpsAndRestoresAPostgresDatabase(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 on a Docker host")
	}
	s, router := dbTestRouter(t)
	s.modules.docker = dockerx.New("unix:///var/run/docker.sock")
	name := fmt.Sprintf("jd-dump-postgres-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = s.modules.docker.RemoveContainer(ctx, name, true, false)
		if err := s.modules.docker.RemoveVolume(ctx, name+"-data", false); err != nil {
			t.Errorf("test volume cleanup: %v", err)
		}
	})
	body, _ := json.Marshal(map[string]string{"engine": "postgres", "name": name, "database": "app"})
	created := do(t, router, http.MethodPost, "/databases/provision", string(body))
	if created.Code != http.StatusAccepted {
		t.Fatalf("provision failed: HTTP %d: %s", created.Code, created.Body)
	}
	deadline := time.Now().Add(2 * time.Minute)
	var conn dbConnection
	for {
		adopted := do(t, router, http.MethodPost, "/databases/adopt", fmt.Sprintf(`{"container":%q}`, name))
		if adopted.Code >= 200 && adopted.Code < 300 {
			if err := json.Unmarshal(adopted.Body.Bytes(), &conn); err != nil {
				t.Fatal(err)
			}
			ping := do(t, router, http.MethodGet, pathf("/databases/%d/ping", conn.ID), "")
			var health struct {
				OK bool `json:"ok"`
			}
			_ = json.Unmarshal(ping.Body.Bytes(), &health)
			if health.OK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("database did not accept connections; last adopt status %d", adopted.Code)
		}
		time.Sleep(time.Second)
	}
	pool, _, err := s.dbPool(t.Context(), conn.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE orders (id SERIAL PRIMARY KEY, note TEXT NOT NULL)`,
		`INSERT INTO orders(note) VALUES ('canary-before-backup')`,
		`CREATE DATABASE drill`,
	} {
		if _, err := pool.ExecContext(t.Context(), statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}

	source := filepath.Join(s.Cfg.FileRoots[0], "app-files")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	job, err := s.modules.backupStore.Create(t.Context(), &backups.Job{
		Name: "postgres-dump", Sources: []string{source}, DatabaseDumps: []int64{conn.ID},
		TargetKind: backups.TargetLocal, Target: backups.TargetConfig{Path: filepath.Join(s.Cfg.FileRoots[0], "archives")},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.modules.backupRunner.Execute(t.Context(), job.ID, "test")
	if err != nil || run.Status != backups.StatusSuccess {
		t.Fatalf("backup run = %+v, %v\n%s", run, err, run.Log)
	}
	if run.Manifest == nil || len(run.Manifest.DatabaseDumps) != 1 || run.Manifest.DatabaseDumps[0].Driver != "postgres" ||
		run.Manifest.DatabaseDumps[0].Database != "app" || run.Manifest.DatabaseDumps[0].Bytes == 0 {
		t.Fatalf("dump evidence = %#v", run.Manifest)
	}
	t.Logf("dump captured with %s: %d bytes", run.Manifest.DatabaseDumps[0].Method, run.Manifest.DatabaseDumps[0].Bytes)
	if err := s.modules.backupRunner.VerifyDatabaseCoverage(t.Context(), run.ID, []int64{conn.ID}); err != nil {
		t.Fatal(err)
	}

	// Damage the live data, then restore into the drill database only.
	if _, err := pool.ExecContext(t.Context(), `DELETE FROM orders`); err != nil {
		t.Fatal(err)
	}
	// The database test router mounts only Databases routes; the restore
	// lives under Backups, behind the same admin principal.
	backupRouter := chi.NewRouter()
	backupRouter.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			p := &httpx.Principal{User: &auth.User{ID: 1, Username: "tester"}, Role: auth.RoleAdmin, Kind: "session", IP: "127.0.0.1"}
			next.ServeHTTP(w, req.WithContext(httpx.WithPrincipal(req.Context(), p)))
		})
	})
	s.mountBackupRoutes(backupRouter)
	restoreRequest := func(database, confirm string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, pathf("/backups/runs/%d/restore-database", run.ID),
			strings.NewReader(fmt.Sprintf(`{"connectionId":%d,"database":%q}`, conn.ID, database)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Confirm", confirm)
		rec := httptest.NewRecorder()
		backupRouter.ServeHTTP(rec, req)
		return rec
	}
	restored := restoreRequest("drill", "drill")
	if restored.Code != http.StatusOK {
		t.Fatalf("restore-database = %d %s", restored.Code, restored.Body.String())
	}
	drillDSN := strings.Replace(mustDSN(t, s, conn.ID), "/app", "/drill", 1)
	drill, err := s.modules.dbs.Pool(t.Context(), conn.ID+1000, conn.Driver, drillDSN)
	if err != nil {
		t.Fatal(err)
	}
	var note string
	if err := drill.QueryRowContext(t.Context(), `SELECT note FROM orders ORDER BY id LIMIT 1`).Scan(&note); err != nil || note != "canary-before-backup" {
		t.Fatalf("restored drill database = %q, %v", note, err)
	}
	var live int
	if err := pool.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM orders`).Scan(&live); err != nil || live != 0 {
		t.Fatalf("restore touched the live database: %d rows, %v", live, err)
	}
	// A wrong typed confirmation never reaches the engine.
	refused := restoreRequest("app", "drill")
	if refused.Code == http.StatusOK {
		t.Fatalf("restore accepted a mismatched confirmation: %s", refused.Body.String())
	}
}

func mustDSN(t *testing.T, s *Server, id int64) string {
	t.Helper()
	_, dsn, err := s.dbConnRow(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return dsn
}
