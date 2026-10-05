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

// A labelled listing is one environment's containers, so the stopped ones are
// inspected too: their exit code and restart count are what the Runtime page
// shows for a service that is down. The unfiltered listing must not pay that.
func TestFilteredContainerInventoryReadsLastRunOfStoppedContainers(t *testing.T) {
	inspected := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1.47/containers/json":
			_, _ = w.Write([]byte(`[
				{"Id":"up","Names":["/up"],"State":"running"},
				{"Id":"oom","Names":["/oom"],"State":"exited"},
				{"Id":"fresh","Names":["/fresh"],"State":"created"}]`))
		case "/v1.47/containers/up/json":
			inspected["up"]++
			_, _ = w.Write([]byte(`{"Id":"up","RestartCount":3,"State":{"Running":true,"ExitCode":137,"StartedAt":"2026-09-01T00:00:00Z"}}`))
		case "/v1.47/containers/oom/json":
			inspected["oom"]++
			_, _ = w.Write([]byte(`{"Id":"oom","RestartCount":1,"State":{"ExitCode":137,"OOMKilled":true}}`))
		case "/v1.47/containers/fresh/json":
			inspected["fresh"]++
			_, _ = w.Write([]byte(`{"Id":"fresh","State":{"ExitCode":0}}`))
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

	items, err := owner.ListContainersWithLabels(t.Context(), map[string]string{"io.just-dashboard.managed": "true"})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Container{}
	for _, item := range items {
		byID[item.ID] = item
	}
	if up := byID["up"]; up.Restarts != 3 || up.Exited != nil || up.WasOOMKilled {
		t.Fatalf("a running container reported a previous run's exit: %+v", up)
	}
	if oom := byID["oom"]; oom.Restarts != 1 || oom.Exited == nil || *oom.Exited != 137 || !oom.WasOOMKilled || oom.Inspected {
		t.Fatalf("stopped container = %+v", oom)
	}
	if fresh := byID["fresh"]; fresh.Exited != nil {
		t.Fatalf("a created container has not exited: %+v", fresh)
	}

	inspected = map[string]int{}
	if _, err := owner.ListContainers(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	if inspected["oom"] != 0 || inspected["fresh"] != 0 {
		t.Fatalf("the unfiltered listing inspected stopped containers: %v", inspected)
	}
}
