package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

func TestReadOnlyExplainCannotMutateSQLite(t *testing.T) {
	s := testServer(t)
	t.Cleanup(s.Shutdown)
	sealed, err := s.Sealer.Seal(filepath.Join(s.Cfg.FileRoots[0], "plan-test.db"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Store.DB.Exec(`INSERT INTO db_connections(name,driver,dsn_enc,created_at) VALUES(?,?,?,?)`, "plan-test", string(dbx.DriverSQLite), sealed, 0)
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	pool, _, err := s.dbPool(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec("CREATE TABLE t(id INTEGER); INSERT INTO t VALUES(1)"); err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := &httpx.Principal{Role: auth.RoleReadOnly, Kind: "session", User: &auth.User{ID: 1, Username: "reader"}}
			next.ServeHTTP(w, r.WithContext(httpx.WithPrincipal(r.Context(), p)))
		})
	})
	s.mountDatabaseRoutes(router)
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"query":"DELETE FROM t"}`, http.StatusOK},
		{`{"query":"SELECT * FROM t; DELETE FROM t"}`, http.StatusBadRequest},
		{`{"query":"ANALYZE DELETE FROM t"}`, http.StatusBadRequest},
	} {
		req := httptest.NewRequest(http.MethodPost, "/databases/"+itoaLocal(int(id))+"/explain", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != tc.status {
			t.Fatalf("explain status %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
		}
		var count int
		if err := pool.QueryRow("SELECT count(*) FROM t").Scan(&count); err != nil || count != 1 {
			t.Fatalf("explain changed data: %d, %v", count, err)
		}
	}
}
