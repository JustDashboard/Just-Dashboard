package dockerx

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/docker/docker/client"
)

func TestRecreateNeverRemovesParkingNameOccupantAndRestoresFailedCandidate(t *testing.T) {
	for _, mode := range []string{"success", "collision_exhausted", "candidate_failure", "candidate_missing"} {
		t.Run(mode, func(t *testing.T) {
			originalName := "web"
			originalRunning := true
			candidateExists := false
			renameAttempts := 0
			var removed []string
			var parkingNames []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				path := strings.TrimPrefix(r.URL.Path, "/v1.47")
				fail := func(status int, message string) {
					w.WriteHeader(status)
					_ = json.NewEncoder(w).Encode(map[string]string{"message": message})
				}
				switch {
				case r.Method == http.MethodGet && path == "/containers/web/json":
					if originalName == "web" {
						fmt.Fprint(w, `{"Id":"original-id","Name":"/web","Config":{"Image":"fixture:latest"},"HostConfig":{},"State":{"Running":true}}`)
					} else {
						fail(404, "name is free")
					}
				case r.Method == http.MethodGet && strings.HasPrefix(path, "/images/"):
					fmt.Fprint(w, `{"Id":"sha256:fixture"}`)
				case r.Method == http.MethodPost && path == "/containers/original-id/rename":
					name := r.URL.Query().Get("name")
					if name == "web" {
						if candidateExists {
							fail(409, "candidate still owns original name")
							return
						}
						originalName = name
						w.WriteHeader(204)
						return
					}
					parkingNames = append(parkingNames, name)
					renameAttempts++
					if mode == "collision_exhausted" || renameAttempts == 1 {
						fail(409, "unrelated container already owns parking name")
						return
					}
					originalName = name
					w.WriteHeader(204)
				case r.Method == http.MethodPost && path == "/containers/original-id/stop":
					originalRunning = false
					w.WriteHeader(204)
				case r.Method == http.MethodPost && path == "/containers/create":
					candidateExists = true
					fmt.Fprint(w, `{"Id":"candidate-id","Warnings":[]}`)
				case r.Method == http.MethodPost && path == "/containers/candidate-id/start":
					if mode == "candidate_failure" || mode == "candidate_missing" {
						fail(500, "candidate failed to start")
					} else {
						w.WriteHeader(204)
					}
				case r.Method == http.MethodPost && path == "/containers/original-id/start":
					originalRunning = true
					w.WriteHeader(204)
				case r.Method == http.MethodDelete:
					id := strings.TrimPrefix(path, "/containers/")
					removed = append(removed, id)
					if id != "original-id" && id != "candidate-id" {
						t.Errorf("deleted unrelated parking occupant %q", id)
					}
					if id == "candidate-id" {
						candidateExists = false
						if mode == "candidate_missing" {
							fail(404, "candidate already removed")
							return
						}
					}
					w.WriteHeader(204)
				default:
					t.Errorf("unexpected request %s %s", r.Method, path)
					fail(500, "unexpected request")
				}
			}))
			defer server.Close()
			api, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.47"))
			if err != nil {
				t.Fatal(err)
			}
			defer api.Close()
			c := &Client{cli: api}
			result, err := c.Recreate(t.Context(), "web", RecreateOptions{Spec: &ContainerSpec{Image: "fixture:latest"}}, nil)
			switch mode {
			case "success":
				if err != nil || result == nil || !result.Started || len(removed) != 1 || removed[0] != "original-id" {
					t.Fatalf("result %#v error %v removed %v", result, err, removed)
				}
			case "collision_exhausted":
				if err == nil || len(removed) != 0 || originalName != "web" || !originalRunning {
					t.Fatalf("collision changed original: %v %v %s %v", err, removed, originalName, originalRunning)
				}
			case "candidate_failure", "candidate_missing":
				if err == nil || len(removed) != 1 || removed[0] != "candidate-id" || originalName != "web" || !originalRunning {
					t.Fatalf("failed to restore: %v %v %s %v", err, removed, originalName, originalRunning)
				}
			}
			seen := map[string]bool{}
			for _, name := range parkingNames {
				if seen[name] || name == "web_jd_replaced" {
					t.Fatalf("parking name reused: %v", parkingNames)
				}
				seen[name] = true
			}
		})
	}
}
