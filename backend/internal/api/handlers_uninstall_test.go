package api

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/selfupdate"
	"github.com/Wayy01/Just-Dashboard/backend/internal/uninstall"
	"github.com/go-chi/chi/v5"
)

// uninstallRouter mounts the dashboard routes with a chosen role, against an
// uninstall that locates a checkout and launches nothing real: `docker` on
// PATH is a script that succeeds.
func uninstallRouter(t *testing.T, role auth.Role) http.Handler {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	s := testServer(t)
	dataDir := t.TempDir()
	s.modules.uninstall = uninstall.New(uninstall.Options{
		DataDir:    dataDir,
		DurableLog: filepath.Join(dataDir, "durable.log"),
		Locate: func(context.Context) (*selfupdate.Location, error) {
			return &selfupdate.Location{Dir: t.TempDir(), Compose: "docker-compose.yml", Image: "just-dashboard-backend:latest"}, nil
		},
		Log: s.Log,
	})

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

// Invariant 3: a wipe deletes what nothing brings back, so the server asks
// for the phrase itself; keeping is undone by running the installer again
// and asks only the ordinary question.
func TestWipeTakesThePhraseAndKeepDoesNot(t *testing.T) {
	router := uninstallRouter(t, auth.RoleAdmin)
	if rec := drive(t, router, http.MethodPost, "/dashboard/uninstall", `{"mode":"wipe"}`, ""); rec.Code != http.StatusPreconditionRequired {
		t.Fatalf("a wipe with no phrase answered %d: %s", rec.Code, rec.Body.String())
	}
	if rec := drive(t, router, http.MethodPost, "/dashboard/uninstall", `{"mode":"wipe"}`, "some-other-server"); rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("a wipe with the wrong phrase answered %d: %s", rec.Code, rec.Body.String())
	}
	if rec := drive(t, router, http.MethodPost, "/dashboard/uninstall", `{"mode":"nuke"}`, ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("an unknown mode answered %d: %s", rec.Code, rec.Body.String())
	}
	rec := drive(t, router, http.MethodPost, "/dashboard/uninstall", `{"mode":"wipe","deleteBackups":true}`,
		url.PathEscape(uninstall.Phrase()))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("a confirmed wipe answered %d: %s", rec.Code, rec.Body.String())
	}
	// One at a time, whatever the second one asks for.
	if rec := drive(t, router, http.MethodPost, "/dashboard/uninstall", `{"mode":"keep"}`, ""); rec.Code != http.StatusConflict {
		t.Fatalf("a second uninstall answered %d: %s", rec.Code, rec.Body.String())
	}
	waitForHandOver(t, router)

	keep := uninstallRouter(t, auth.RoleAdmin)
	if rec := drive(t, keep, http.MethodPost, "/dashboard/uninstall", `{"mode":"keep"}`, ""); rec.Code != http.StatusAccepted {
		t.Fatalf("keeping without a phrase answered %d: %s", rec.Code, rec.Body.String())
	}
	waitForHandOver(t, keep)
}

// waitForHandOver lets the backend's half finish before the test's temporary
// directories are removed under it.
func waitForHandOver(t *testing.T, router http.Handler) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if rec := drive(t, router, http.MethodGet, "/dashboard/uninstall", "", ""); rec.Code == http.StatusOK &&
			strings.Contains(rec.Body.String(), `"phase":"stack"`) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the uninstall never handed over to its sibling")
}

// Invariant 4: only an administrator reaches any of it, reading included —
// the plan lists every path and volume on the server.
func TestOnlyAnAdminCanUninstall(t *testing.T) {
	for _, role := range []auth.Role{auth.RoleReadOnly, auth.RoleLimited} {
		t.Run(string(role), func(t *testing.T) {
			router := uninstallRouter(t, role)
			for _, req := range []struct{ method, path, body string }{
				{http.MethodGet, "/dashboard/uninstall", ""},
				{http.MethodGet, "/dashboard/uninstall/plan", ""},
				{http.MethodPost, "/dashboard/uninstall", `{"mode":"keep"}`},
			} {
				if rec := drive(t, router, req.method, req.path, req.body, ""); rec.Code != http.StatusForbidden {
					t.Fatalf("%s %s answered %s with %d", req.method, req.path, role, rec.Code)
				}
			}
		})
	}
}
