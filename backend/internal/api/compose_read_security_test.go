package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/go-chi/chi/v5"
)

func TestStackDetailOnlyEvaluatesComposeForAdmin(t *testing.T) {
	s := testServer(t)
	root := t.TempDir()
	stackDir := filepath.Join(root, "fixture")
	if err := os.Mkdir(stackDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stackDir, "compose.yml"), []byte("services:\n  web:\n    image: ${JD_COMPOSE_TEST_SECRET}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.Cfg.ComposeRoots = []string{root}
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/_ping" {
			w.Header().Set("API-Version", "1.47")
			return
		}
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/containers/json") {
			_, _ = w.Write([]byte("[]"))
			return
		}
		t.Errorf("unexpected Docker request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(engine.Close)
	s.modules.docker = dockerx.New(engine.URL)
	t.Cleanup(func() { _ = s.modules.docker.Close() })

	// An evaluated Compose error can contain inherited environment values.
	// A fake CLI makes that boundary observable without using the host daemon.
	bin := t.TempDir()
	marker := filepath.Join(bin, "invoked")
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\nprintf invoked > \"$JD_COMPOSE_TEST_MARKER\"\nprintf '%s' \"$JD_COMPOSE_TEST_SECRET\" >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("JD_COMPOSE_TEST_MARKER", marker)
	t.Setenv("JD_COMPOSE_TEST_SECRET", "synthetic-compose-environment-secret")
	for _, role := range []auth.Role{auth.RoleReadOnly, auth.RoleLimited, auth.RoleAdmin} {
		t.Run(string(role), func(t *testing.T) {
			request := specRequest(t, role)
			request.Method = http.MethodGet
			route := chi.NewRouteContext()
			route.URLParams.Add("name", "fixture")
			request = request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, route))
			response := httptest.NewRecorder()
			if err := s.handleStackDetail(response, request); err != nil {
				t.Fatal(err)
			}
			var detail StackDetail
			if err := json.Unmarshal(response.Body.Bytes(), &detail); err != nil {
				t.Fatal(err)
			}
			if len(detail.Declared) != 1 || detail.Declared[0] != "web" || detail.DeclaredSource != "file" {
				t.Fatalf("static service list lost: %+v", detail.ComposeStack)
			}
			_, err := os.Stat(marker)
			if role == auth.RoleAdmin {
				if err != nil || !strings.Contains(detail.DeclaredError, "synthetic-compose-environment-secret") {
					t.Fatalf("administrator fixture did not evaluate Compose: marker %v, error %q", err, detail.DeclaredError)
				}
			} else if !os.IsNotExist(err) || detail.DeclaredError != "" || strings.Contains(response.Body.String(), "synthetic-compose-environment-secret") {
				t.Fatalf("non-admin evaluated Compose or received inherited values: marker %v, response %s", err, response.Body.String())
			}
		})
	}
}
