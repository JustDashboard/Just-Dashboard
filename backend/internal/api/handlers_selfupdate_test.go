package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/selfupdate"
	"github.com/go-chi/chi/v5"
)

// Upgrading the dashboard replaces every container in the stack with one built
// from code that is not on the machine yet. Two things guard it and both are
// pinned here: only system.admin may ask, and the asking has to name the
// version. Neither is enforceable in the browser, so neither is tested there.

const publishedManifest = `{"releases":[
	{"version":"0.9","date":"2026-12-01","title":"Nine","summary":"s","changes":[{"kind":"added","text":"a thing"}]},
	{"version":"0.5","date":"2026-08-01","title":"Five","summary":"s","changes":[{"kind":"added","text":"another"}]}]}`

// selfUpdateRouter mounts the routes with a chosen role, against a service
// that believes 0.9 has been published and that this install is a checkout it
// could rebuild. Nothing here reaches Docker: every assertion is about a
// refusal that happens before an upgrade could start.
func selfUpdateRouter(t *testing.T, role auth.Role) http.Handler {
	t.Helper()
	s := testServer(t)

	repo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(publishedManifest))
	}))
	t.Cleanup(repo.Close)

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte("name: jd\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s.modules.selfUpdate = selfupdate.New(selfupdate.Options{
		Current: "0.5", DataDir: t.TempDir(), UpdateDir: dir,
		Repo: "owner/repo", Ref: "main", CheckOnline: true,
		BaseURL: repo.URL, Log: s.Log,
	})
	// Prime the cache, so the handler is answering about a real published
	// version rather than about an install that has never checked.
	if rep := s.modules.selfUpdate.Report(context.Background(), selfupdate.Forced); !rep.Available {
		t.Fatalf("the fixture install does not see an update: %+v", rep)
	}

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			p := &httpx.Principal{
				User: &auth.User{ID: 1, Username: "tester"},
				Role: role, Kind: "session", IP: "127.0.0.1",
			}
			next.ServeHTTP(w, req.WithContext(httpx.WithPrincipal(req.Context(), p)))
		})
	})
	s.mountSelfUpdateRoutes(r)
	return r
}

func drive(t *testing.T, router http.Handler, method, path, body, confirm string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if confirm != "" {
		req.Header.Set(httpx.ConfirmHeader, confirm)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// The phrase is the version. Short on purpose — what has to be read is *which*
// version, and naming the object is what every other typed route in this
// codebase does.
func TestInstallingAnUpdateDemandsTheVersionBeTyped(t *testing.T) {
	router := selfUpdateRouter(t, auth.RoleAdmin)

	rec := drive(t, router, http.MethodPost, "/dashboard/update/install", `{}`, "")
	if rec.Code != http.StatusPreconditionRequired {
		t.Fatalf("an unconfirmed upgrade answered %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"phrase":"0.9"`) {
		t.Fatalf("the server did not say which phrase it wants: %s", rec.Body.String())
	}

	// The phrase for a *different* version must not work, or the confirmation
	// stops being about the thing being installed.
	rec = drive(t, router, http.MethodPost, "/dashboard/update/install", `{}`, "0.5")
	if rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("a mismatched phrase answered %d: %s", rec.Code, rec.Body.String())
	}
}

// A tab left open across a release would otherwise confirm one version and
// install another.
func TestInstallRefusesAVersionThatHasMovedOn(t *testing.T) {
	router := selfUpdateRouter(t, auth.RoleAdmin)
	rec := drive(t, router, http.MethodPost, "/dashboard/update/install", `{"version":"0.7"}`, "0.7")
	if rec.Code != http.StatusConflict {
		t.Fatalf("installing a superseded version answered %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "0.9") {
		t.Errorf("the refusal does not name the version that is actually newest: %s", rec.Body.String())
	}
}

// Invariant 4: the capability check is on the route, and the UI hiding the
// button is not what stops a limited account from pressing it.
func TestOnlyAnAdminCanInstallAnUpdate(t *testing.T) {
	for _, role := range []auth.Role{auth.RoleReadOnly, auth.RoleLimited} {
		t.Run(string(role), func(t *testing.T) {
			router := selfUpdateRouter(t, role)
			rec := drive(t, router, http.MethodPost, "/dashboard/update/install", `{}`, "0.9")
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s could reach the installer (%d): %s", role, rec.Code, rec.Body.String())
			}
			// Reading the version and its release notes is not privileged —
			// it is on the sign-in page — and a read-only operator noticing
			// is often how the one who can install finds out.
			if rec := drive(t, router, http.MethodGet, "/dashboard/update", "", ""); rec.Code != http.StatusOK {
				t.Fatalf("%s cannot read the update status (%d)", role, rec.Code)
			}
		})
	}
}

// A transcript is readable by whoever can read the report that already
// carries its end: every role for an upgrade, only system.admin for a restart,
// whose output names the perimeter's ports and paths.
func TestTranscriptsFollowTheirReports(t *testing.T) {
	router := selfUpdateRouter(t, auth.RoleReadOnly)
	rec := drive(t, router, http.MethodGet, "/dashboard/update/log", "", "")
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("a read-only operator cannot read the upgrade transcript (%d %s)",
			rec.Code, rec.Header().Get("Content-Type"))
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("the transcript may be cached (%q); every run rewrites it", got)
	}
	if rec := drive(t, router, http.MethodGet, "/dashboard/config/log", "", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("a read-only operator reached the restart transcript (%d)", rec.Code)
	}
}

// An install this cannot upgrade in place is a perfectly good install — a
// binary on a systemd unit, say. It has to be told so, precisely, rather than
// shown a button that fails.
func TestAnInstallWithNothingToRebuildSaysSo(t *testing.T) {
	s := testServer(t)
	s.modules.selfUpdate = selfupdate.New(selfupdate.Options{
		Current: "0.5", DataDir: t.TempDir(),
		UpdateDir: filepath.Join(t.TempDir(), "not-here"),
		Log:       s.Log,
	})
	rep := s.modules.selfUpdate.Report(context.Background(), selfupdate.Cached)
	if rep.Install.Supported {
		t.Fatal("an install with no checkout reported that it can rebuild itself")
	}
	if rep.Install.Reason == "" {
		t.Error("no reason given, so the UI has nothing to show but a disabled button")
	}
	// The changelog is compiled in, so it is readable with no network, no
	// Docker and no checkout.
	if len(rep.History) == 0 {
		t.Error("an install that cannot update itself also lost its own release notes")
	}
	if rep.Version != "0.5" {
		t.Errorf("version reported as %q", rep.Version)
	}
}

// The updater is given no configuration of its own, so the address it should
// ask before declaring an upgrade finished travels with the job. Getting this
// wrong means either a probe that can never succeed — reported to the operator
// as a failed upgrade that in fact worked — or none at all.
func TestHealthURLIsSomethingTheUpdaterCanActuallyAsk(t *testing.T) {
	s := testServer(t)
	for _, tc := range []struct{ addr, agent, want string }{
		{"127.0.0.1:8080", "", "http://127.0.0.1:8080/healthz"},
		// A wildcard bind is not an address you can send a request to.
		{"0.0.0.0:8080", "", "http://127.0.0.1:8080/healthz"},
		{":8080", "", "http://127.0.0.1:8080/healthz"},
		// An agent serves TLS on the same address, with a certificate that is
		// self-signed by design — so the probe there is about liveness only.
		{"127.0.0.1:9443", "agent", "https://127.0.0.1:9443/healthz"},
	} {
		s.Cfg.Addr = tc.addr
		s.Cfg.AgentMode = tc.agent == "agent"
		if got := s.healthURL(); got != tc.want {
			t.Errorf("addr %q → %q, want %q", tc.addr, got, tc.want)
		}
	}
	s.Cfg.AgentMode = false
	// No port, nothing to probe — and an empty URL is what tells the updater
	// to skip the probe rather than to fail on a malformed one.
	s.Cfg.Addr = "not-an-address"
	if got := s.healthURL(); got != "" {
		t.Errorf("an unparseable bind address produced %q", got)
	}
}
