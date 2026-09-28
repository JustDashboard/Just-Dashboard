package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

func provisionNameEngine(t *testing.T, s *Server, containers, volumes map[string]bool) {
	t.Helper()
	var mu sync.Mutex
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		path := strings.TrimPrefix(r.URL.Path, "/v1.47")
		switch {
		case path == "/_ping":
			w.Header().Set("API-Version", "1.47")
		case strings.HasPrefix(path, "/containers/") && strings.HasSuffix(path, "/json"):
			name := strings.TrimSuffix(strings.TrimPrefix(path, "/containers/"), "/json")
			if !containers[name] {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"No such container"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Id": name, "Name": "/" + name,
				"Config": map[string]any{"Image": "postgres:16-alpine"},
				"State":  map[string]any{"Status": "running", "Running": true},
				"HostConfig": map[string]any{
					"NetworkMode": "bridge",
				},
				"NetworkSettings": map[string]any{"Ports": map[string]any{
					"5432/tcp": []map[string]string{{"HostIp": "127.0.0.1", "HostPort": "5432"}},
				}},
			})
		case strings.HasPrefix(path, "/volumes/"):
			name := strings.TrimPrefix(path, "/volumes/")
			if !volumes[name] {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"No such volume"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"Name": name})
		case strings.HasPrefix(path, "/images/") && strings.HasSuffix(path, "/json"):
			_, _ = w.Write([]byte(`{"Id":"postgres-image"}`))
		case path == "/containers/create":
			name := r.URL.Query().Get("name")
			if containers[name] || volumes[name+"-data"] {
				t.Errorf("provision tried to reuse %s or its persistent data", name)
				w.WriteHeader(http.StatusConflict)
				return
			}
			containers[name], volumes[name+"-data"] = true, true
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"Id": name})
		case strings.HasPrefix(path, "/containers/") && strings.HasSuffix(path, "/start"):
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected Docker request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(engine.Close)
	s.modules.docker = dockerx.New(engine.URL)
	t.Cleanup(func() { _ = s.modules.docker.Close() })
}

func TestDatabaseProvisionDefaultsSkipContainersAndRetainedVolumes(t *testing.T) {
	s, router := dbTestRouter(t)
	provisionNameEngine(t, s, map[string]bool{"jd-postgres": true}, map[string]bool{"jd-postgres-2-data": true})
	for _, want := range []string{"jd-postgres-3", "jd-postgres-4"} {
		response := do(t, router, http.MethodPost, "/databases/provision", `{"engine":"postgres","exposure":"local"}`)
		if response.Code != http.StatusAccepted {
			t.Fatalf("provision = %d %s", response.Code, response.Body.String())
		}
		var result struct {
			Container string `json:"container"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Container != want {
			t.Fatalf("provision = %#v, %v; want %s", result, err, want)
		}
	}
	for _, tc := range []struct{ name, code string }{
		{"jd-postgres", "container_exists"},
		{"jd-postgres-2", "volume_exists"},
	} {
		response := do(t, router, http.MethodPost, "/databases/provision",
			`{"engine":"postgres","exposure":"local","name":"`+tc.name+`"}`)
		if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), tc.code) {
			t.Fatalf("explicit name %s = %d %s", tc.name, response.Code, response.Body.String())
		}
	}
}

func TestDatabaseProvisionReservesNamesBeforeImagePullAndReleasesFailures(t *testing.T) {
	s := testServer(t)
	provisionNameEngine(t, s, map[string]bool{}, map[string]bool{})
	first, releaseFirst, err := s.reserveDatabaseName(t.Context(), "", "postgres")
	if err != nil {
		t.Fatal(err)
	}
	second, releaseSecond, err := s.reserveDatabaseName(t.Context(), "", "postgres")
	if err != nil {
		t.Fatal(err)
	}
	defer releaseSecond()
	if first != "jd-postgres" || second != "jd-postgres-2" {
		t.Fatalf("in-flight names = %q, %q", first, second)
	}
	if _, _, err := s.reserveDatabaseName(t.Context(), first, "postgres"); err == nil {
		t.Fatal("an explicit name collided with an in-flight provision")
	}
	releaseFirst()
	retry, releaseRetry, err := s.reserveDatabaseName(t.Context(), "", "postgres")
	if err != nil {
		t.Fatal(err)
	}
	defer releaseRetry()
	if retry != first {
		t.Fatalf("failed operation kept its name: got %s, want %s", retry, first)
	}
}
