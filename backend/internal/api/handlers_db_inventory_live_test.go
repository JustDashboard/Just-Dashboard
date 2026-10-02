package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// The inventory against the machine the test runs on: its real Docker daemon,
// socket tables, units and files.
//
// None of these runs unless asked, because none is hermetic. The first only
// reads. The second signs in to one container, named by the caller, and to
// nothing else — it never runs the reconcile, which would sign in to every
// database the machine has. The third signs in to the two MongoDB servers the
// caller names: one open, one with access control on.
//
//	JD_TEST_INVENTORY_LIVE=1 go test ./internal/api -run TestLiveInventory -count=1 -v
//	JD_TEST_INVENTORY_CONTAINER=my-throwaway-redis go test ./internal/api -run TestLiveInventoryConnects -count=1 -v
//	JD_TEST_MONGO_AUTH_DSN=mongodb://root:pw@127.0.0.1:57117/admin go test ./internal/api -run TestLiveMongoSignIn -count=1 -v

func liveInventoryRouter(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	s := testServer(t)
	t.Cleanup(s.Shutdown)
	host := os.Getenv("JD_TEST_DOCKER_HOST")
	if host == "" {
		host = "unix:///var/run/docker.sock"
	}
	s.modules.docker = dockerx.New(host)
	t.Cleanup(func() { _ = s.modules.docker.Close() })
	s.modules.files = files.New([]string{"/"})
	s.Cfg.ComposeRoots = []string{"/opt", "/srv", "/home"}

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

// TestLiveInventoryNeverCarriesAContainerSecret lists the machine and checks
// the one property that must hold whatever is on it: no value of a
// credential-shaped variable of any container appears anywhere in the answer.
func TestLiveInventoryNeverCarriesAContainerSecret(t *testing.T) {
	if os.Getenv("JD_TEST_INVENTORY_LIVE") == "" {
		t.Skip("JD_TEST_INVENTORY_LIVE not set")
	}
	s, r := liveInventoryRouter(t)
	rec := do(t, r, http.MethodPost, "/databases/inventory/scan", "{}")
	if rec.Code != http.StatusOK {
		t.Fatalf("inventory answered %d: %s", rec.Code, rec.Body.String())
	}
	var inv inventoryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &inv); err != nil {
		t.Fatal(err)
	}
	for _, scan := range inv.Scans {
		t.Logf("scan %-9s ok=%v count=%d %dms truncated=%v %s", scan.Source, scan.OK, scan.Count, scan.DurationMs, scan.Truncated, scan.Reason)
	}
	for _, inst := range inv.Instances {
		t.Logf("%-8s %-10s %-9s %-11s %-11s %s", inst.Kind, inst.Engine, inst.State, inst.Credentials, inst.Confidence, inst.Key)
		if inst.Key == "" || inst.Engine == "" || inst.Label == "" || len(inst.Evidence) == 0 {
			t.Errorf("%+v is missing what every instance states", inst)
		}
		if !inst.Connectable && inst.Reason == "" {
			t.Errorf("%s cannot be connected and does not say why", inst.Key)
		}
	}

	containers, err := s.modules.docker.ListContainers(t.Context(), true)
	if err != nil {
		t.Skipf("Docker did not answer: %v", err)
	}
	body := rec.Body.String()
	checked := 0
	for _, c := range containers {
		detail, err := s.modules.docker.Inspect(t.Context(), c.ID)
		if err != nil {
			continue
		}
		for name, value := range envMap(detail.Env) {
			// Short values are words, not secrets: "postgres", "true".
			if !dockerx.IsSecretEnvKey(name) || len(value) < 8 {
				continue
			}
			checked++
			if strings.Contains(body, value) {
				t.Errorf("the value of %s on %s is in the inventory", name, c.Name)
			}
		}
	}
	t.Logf("%d instances; %d credential-shaped values checked against the answer", len(inv.Instances), checked)
}

// TestLiveInventoryConnectsAContainerByKey connects one container the caller
// names, end to end: the key is looked up, the credentials are read from the
// container, the engine is signed in to, and the connection answers a ping.
func TestLiveInventoryConnectsAContainerByKey(t *testing.T) {
	name := os.Getenv("JD_TEST_INVENTORY_CONTAINER")
	if name == "" {
		t.Skip("JD_TEST_INVENTORY_CONTAINER not set")
	}
	s, r := liveInventoryRouter(t)
	var inv inventoryResponse
	rec := do(t, r, http.MethodGet, "/databases/inventory", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &inv); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("inventory answered %d: %s", rec.Code, rec.Body.String())
	}
	var found *dbx.Instance
	for i := range inv.Instances {
		if c := inv.Instances[i].Container; c != nil && c.Name == name && inv.Instances[i].Kind == dbx.KindServer {
			found = &inv.Instances[i]
		}
	}
	if found == nil {
		t.Skipf("no container named %s was recognised as a database", name)
	}
	t.Logf("%s is %s (%s), recognised by %s: %v", found.Key, found.Label, found.Credentials, found.Confidence, found.Evidence)

	body, _ := json.Marshal(map[string]string{"key": found.Key})
	rec = do(t, r, http.MethodPost, "/databases/inventory/connect", string(body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("connect answered %d: %s", rec.Code, rec.Body.String())
	}
	var conn dbConnection
	if err := json.Unmarshal(rec.Body.Bytes(), &conn); err != nil {
		t.Fatal(err)
	}
	if origin := s.originOfConnection(t.Context(), conn.ID); origin != found.Key {
		t.Errorf("origin = %q, want %q", origin, found.Key)
	}
	ping := do(t, r, http.MethodGet, pathf("/databases/%d/ping", conn.ID), "")
	if !strings.Contains(ping.Body.String(), `"ok":true`) {
		t.Errorf("the saved connection does not answer: %s", ping.Body.String())
	}
	again := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/databases/inventory/connect", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(again, req)
	if again.Code != http.StatusOK {
		t.Errorf("connecting it again answered %d: %s", again.Code, again.Body.String())
	}
}

// TestLiveMongoSignInIsNotAPing checks the sign-in against real servers. A
// MongoDB answers a ping from anybody, so a connection that names no account
// used to pass against a server that then refused everything asked of it.
func TestLiveMongoSignInIsNotAPing(t *testing.T) {
	locked := os.Getenv("JD_TEST_MONGO_AUTH_DSN")
	if locked == "" {
		t.Skip("JD_TEST_MONGO_AUTH_DSN not set")
	}
	s := testServer(t)
	t.Cleanup(s.Shutdown)
	parsed, err := url.Parse(locked)
	if err != nil || parsed.User == nil {
		t.Fatalf("JD_TEST_MONGO_AUTH_DSN must name an account: %v", err)
	}

	if err := s.probeConnection(t.Context(), dbx.DriverMongo, locked); err != nil {
		t.Fatalf("the server refused its own account: %v", err)
	}
	nobody := *parsed
	nobody.User = nil
	err = s.probeConnection(t.Context(), dbx.DriverMongo, nobody.String())
	if err == nil {
		t.Fatal("a connection naming no account passed against a server with access control on")
	}
	if !credentialRefusal(err) {
		t.Errorf("%v was not read as the server refusing who asked", err)
	}
	t.Logf("no account: %v", err)
	wrong := *parsed
	wrong.User = url.UserPassword(parsed.User.Username(), "not-the-password")
	if err := s.probeConnection(t.Context(), dbx.DriverMongo, wrong.String()); err == nil || !credentialRefusal(err) {
		t.Errorf("a wrong password answered %v", err)
	} else {
		t.Logf("wrong password: %v", err)
	}

	// And an open server still signs in with nothing.
	if open := os.Getenv("JD_TEST_MONGO_DSN"); open != "" {
		if err := s.probeConnection(t.Context(), dbx.DriverMongo, open); err != nil {
			t.Errorf("the open server refused a connection with no account: %v", err)
		}
	}
}
