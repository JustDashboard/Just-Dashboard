package dockerx

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/docker/docker/client"
)

func TestContainerListingNamesAnImageWhoseTagMovedOn(t *testing.T) {
	const oldPostgres = "sha256:3c5c8892d184f738f4fe282d14ddaa613a38f00f4189d2d94725ebe6f2909ddb"
	const release = "sha256:c9051a2ac152cb76dba839db5eea38f3b407bed22ebd38e77aa1160d60485286"
	inspected := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1.47/containers/json":
			_, _ = w.Write([]byte(`[
				{"Id":"db","Names":["/lampino-db"],"State":"exited","Image":"` + oldPostgres + `"},
				{"Id":"web","Names":["/web"],"State":"exited","Image":"nginx:1.27"},
				{"Id":"release","Names":["/jd-e175-r4"],"State":"exited","Image":"` + release + `"}
			]`))
		case "/v1.47/containers/db/json":
			inspected["db"]++
			_, _ = w.Write([]byte(`{"Id":"db","State":{},"Config":{"Image":"postgres:16-alpine"}}`))
		case "/v1.47/containers/release/json":
			inspected["release"]++
			// A deployment is created from its image id, so its config has no
			// name either and the id is all there is to show.
			_, _ = w.Write([]byte(`{"Id":"release","State":{},"Config":{"Image":"` + release + `"}}`))
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
	items, err := (&Client{cli: cli}).ListContainers(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}
	images := map[string]string{}
	for _, item := range items {
		images[item.Name] = item.Image
		if item.Inspected {
			t.Errorf("%s: a stopped container's limits were reported as read", item.Name)
		}
	}
	if images["lampino-db"] != "postgres:16-alpine" || images["web"] != "nginx:1.27" || images["jd-e175-r4"] != release {
		t.Fatalf("images=%v", images)
	}
	if inspected["db"] != 1 || inspected["release"] != 1 {
		t.Fatalf("inspections=%v", inspected)
	}
}
