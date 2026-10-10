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

func TestMemoryUpdatePreservesSwapHeadroom(t *testing.T) {
	for _, tc := range []struct {
		name                             string
		oldMemory, oldSwap, expectedSwap int64
		explicit                         int64
	}{{"unset", 0, 0, 128 << 20, 0}, {"unlimited", 0, -1, -1, 0}, {"no swap", 128 << 20, 128 << 20, 64 << 20, 0}, {"headroom", 128 << 20, 384 << 20, 320 << 20, 0}, {"explicit", 0, 0, 192 << 20, 192}} {
		t.Run(tc.name, func(t *testing.T) {
			updated := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path := strings.TrimPrefix(r.URL.Path, "/v1.47")
				if path == "/containers/web/json" && r.Method == http.MethodGet {
					json.NewEncoder(w).Encode(map[string]any{"HostConfig": map[string]any{"Memory": tc.oldMemory, "MemorySwap": tc.oldSwap}})
					return
				}
				if path == "/containers/web/update" && r.Method == http.MethodPost {
					var update container.UpdateConfig
					if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
						t.Error(err)
					}
					if update.Memory != 64<<20 || update.MemorySwap != tc.expectedSwap {
						t.Errorf("limits=%d/%d expected 64MiB/%d", update.Memory, update.MemorySwap, tc.expectedSwap)
					}
					updated = true
					fmt.Fprint(w, `{"Warnings":[]}`)
					return
				}
				t.Errorf("unexpected %s %s", r.Method, path)
				w.WriteHeader(500)
			}))
			defer server.Close()
			api, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.47"))
			if err != nil {
				t.Fatal(err)
			}
			defer api.Close()
			c := &Client{cli: api}
			if _, err := c.UpdateResources(t.Context(), "web", ResourceLimits{MemoryMB: 64, MemorySwapMB: tc.explicit}); err != nil || !updated {
				t.Fatalf("update=%v %v", updated, err)
			}
		})
	}
}

func TestResourceUpdateRejectsOverflowAndEmptyRequests(t *testing.T) {
	c := &Client{}
	for _, limits := range []ResourceLimits{{}, {MemoryMB: 1}, {MemoryMB: -1}, {CPUs: -1}, {MemoryMB: 1 << 62}, {MemorySwapMB: 1 << 62}, {PidsLimit: -1}} {
		if _, err := c.UpdateResources(t.Context(), "web", limits); err == nil {
			t.Fatalf("invalid limits accepted: %+v", limits)
		}
	}
}
