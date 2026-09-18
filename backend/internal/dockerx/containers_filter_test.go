package dockerx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/docker/docker/client"
)

func TestFilteredContainerInventoryInspectsOnlySelectedRuntime(t *testing.T) {
	listCalls, inspectCalls := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1.47/containers/json":
			listCalls++
			var filter map[string]map[string]bool
			if err := json.Unmarshal([]byte(r.URL.Query().Get("filters")), &filter); err != nil ||
				!filter["label"]["io.just-dashboard.environment-id=7"] ||
				!filter["label"]["io.just-dashboard.managed=true"] || r.URL.Query().Get("all") != "1" {
				t.Errorf("missing daemon filters: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`[{"Id":"selected","Names":["/web"],"State":"running"}]`))
		case "/v1.47/containers/selected/json":
			inspectCalls++
			_, _ = w.Write([]byte(`{"Id":"selected","State":{"StartedAt":"2026-09-01T00:00:00Z","Health":{"Status":"healthy"}}}`))
		default:
			t.Errorf("unexpected Docker request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	cli, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.47"))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	owner := &Client{cli: cli}
	items, err := owner.ListContainersWithLabels(t.Context(), map[string]string{
		"io.just-dashboard.environment-id": "7", "io.just-dashboard.managed": "true",
	})
	if err != nil || len(items) != 1 || items[0].Health != "healthy" || listCalls != 1 || inspectCalls != 1 {
		t.Fatalf("inventory=%+v err=%v lists=%d inspections=%d", items, err, listCalls, inspectCalls)
	}
}
