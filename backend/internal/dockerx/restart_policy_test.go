package dockerx

import (
	"encoding/json"
	"fmt"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRestartPolicyUpdatesInPlaceAndRefusesOwnedConfiguration(t *testing.T) {
	for _, mode := range []string{"standalone", "compose", "auto-remove", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			mutations := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path := strings.TrimPrefix(r.URL.Path, "/v1.47")
				if path == "/containers/web/json" && r.Method == http.MethodGet {
					labels := map[string]string{}
					if mode == "compose" {
						labels["com.docker.compose.project"] = "site"
					}
					json.NewEncoder(w).Encode(map[string]any{"Id": "web", "Config": map[string]any{"Labels": labels}, "HostConfig": map[string]any{"AutoRemove": mode == "auto-remove"}})
					return
				}
				if path == "/containers/web/update" && r.Method == http.MethodPost {
					mutations++
					var update container.UpdateConfig
					if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
						t.Fatal(err)
					}
					if update.RestartPolicy.Name != container.RestartPolicyUnlessStopped || update.Memory != 0 {
						t.Errorf("unexpected update %+v", update)
					}
					fmt.Fprint(w, `{"Warnings":["fixture warning"]}`)
					return
				}
				t.Errorf("unexpected engine operation %s %s", r.Method, path)
				w.WriteHeader(500)
			}))
			defer server.Close()
			api, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.47"))
			if err != nil {
				t.Fatal(err)
			}
			defer api.Close()
			c := &Client{cli: api}
			policy := "unless-stopped"
			if mode == "invalid" {
				policy = "unrecognised"
			}
			warnings, err := c.UpdateRestartPolicy(t.Context(), "web", policy, 0)
			if mode == "standalone" {
				if err != nil || mutations != 1 || len(warnings) != 1 {
					t.Fatalf("update: %v %v %d", warnings, err, mutations)
				}
			} else if err == nil || mutations != 0 {
				t.Fatalf("unsafe update: %v %d", err, mutations)
			}
		})
	}
}
