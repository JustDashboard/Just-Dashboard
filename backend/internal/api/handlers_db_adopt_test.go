package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// adoptEngine is a Docker daemon that runs exactly one Postgres container,
// whose password the test can change between requests — which is what a
// container removed and created again under the same name looks like from the
// dashboard's side: the same name, the same loopback port, a new secret.
func adoptEngine(t *testing.T, s *Server, password *atomic.Value) {
	t.Helper()
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/_ping":
			w.Header().Set("API-Version", "1.47")
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"Id": "c0ffee", "Names": []string{"/lampino"}, "Image": "postgres:16-alpine", "State": "running",
				"Ports": []map[string]any{{"IP": "127.0.0.1", "PrivatePort": 5432, "PublicPort": 5432, "Type": "tcp"}},
			}})
		case strings.HasSuffix(r.URL.Path, "/containers/c0ffee/json"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Id": "c0ffee", "Name": "/lampino",
				"Config": map[string]any{"Image": "postgres:16-alpine", "Env": []string{
					"POSTGRES_USER=jd", "POSTGRES_PASSWORD=" + password.Load().(string), "POSTGRES_DB=lampino",
				}},
				"HostConfig":      map[string]any{"NetworkMode": "bridge"},
				"NetworkSettings": map[string]any{"Networks": map[string]any{}},
			})
		default:
			t.Errorf("unexpected Docker request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(engine.Close)
	s.modules.docker = dockerx.New(engine.URL)
	t.Cleanup(func() { _ = s.modules.docker.Close() })
}

func adoptRouter(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	s := testServer(t)
	t.Cleanup(s.Shutdown)
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
	return s, r
}

func adopt(t *testing.T, r http.Handler) (int, dbConnection) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/databases/adopt", strings.NewReader(`{"container":"lampino"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	var conn dbConnection
	_ = json.Unmarshal(rec.Body.Bytes(), &conn)
	return rec.Code, conn
}

func storedPassword(t *testing.T, s *Server, id int64) string {
	t.Helper()
	conn, dsn, err := s.dbConnRow(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if conn.User != "jd" {
		t.Fatalf("stored user %q", conn.User)
	}
	// postgres://jd:<password>@127.0.0.1:5432/lampino?sslmode=disable
	rest := strings.TrimPrefix(dsn, "postgres://jd:")
	return rest[:strings.IndexByte(rest, '@')]
}

// A container removed and created again under the same name takes the same
// loopback port back with a freshly generated password. Adopting it must not
// hand back the old row untouched: the caller then polls a server that refuses
// the stored password until it gives up, and nothing says why. The row keeps
// its identity — a deployment references it by id — and gets the credentials
// the container actually has.
func TestAdoptRefreshesAStaleConnectionAtTheSameAddress(t *testing.T) {
	s, r := adoptRouter(t)
	var password atomic.Value
	password.Store("first-secret")
	adoptEngine(t, s, &password)

	code, first := adopt(t, r)
	if code != http.StatusCreated {
		t.Fatalf("first adopt: %d", code)
	}
	if got := storedPassword(t, s, first.ID); got != "first-secret" {
		t.Fatalf("stored %q after the first adopt", got)
	}

	// The same server again is the same row, untouched.
	code, again := adopt(t, r)
	if code != http.StatusOK || again.ID != first.ID {
		t.Fatalf("repeat adopt: %d id %d, want 200 id %d", code, again.ID, first.ID)
	}

	// A new server at the old address.
	password.Store("second-secret")
	code, refreshed := adopt(t, r)
	if code != http.StatusOK || refreshed.ID != first.ID {
		t.Fatalf("adopt of the replacement: %d id %d, want 200 id %d", code, refreshed.ID, first.ID)
	}
	if refreshed.Name != "lampino" {
		t.Errorf("the row was renamed to %q", refreshed.Name)
	}
	if got := storedPassword(t, s, first.ID); got != "second-secret" {
		t.Errorf("stored %q after adopting the replacement, want the container's current password", got)
	}
	var rows int
	_ = s.Store.DB.QueryRow(`SELECT COUNT(*) FROM db_connections`).Scan(&rows)
	if rows != 1 {
		t.Errorf("%d connection rows, want the one row refreshed in place", rows)
	}
}
