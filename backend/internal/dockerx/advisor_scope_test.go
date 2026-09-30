package dockerx

import (
	"encoding/json"
	"github.com/docker/docker/client"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdvisorReclaimOnlyTouchesAdvertisedImagesAndCache(t *testing.T) {
	paths := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/v1.47")
		paths = append(paths, path)
		switch path {
		case "/images/prune", "/build/prune":
			json.NewEncoder(w).Encode(map[string]any{"SpaceReclaimed": 100})
		default:
			t.Errorf("advisor touched unselected objects: %s", path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	api, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.47"))
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	c := &Client{cli: api}
	reports, err := c.PruneAll(t.Context(), PruneOptions{ImagesAndCacheOnly: true, AllImages: true, BuildCache: true, AllBuildCache: true})
	if err != nil || len(reports) != 2 || len(paths) != 2 {
		t.Fatalf("scope: %+v %v %v", reports, paths, err)
	}
	for _, report := range reports {
		if report.Error != "" {
			t.Fatal(report.Error)
		}
	}
}

func TestRuntimeUnknownInspectionIsNotNoHealthCheck(t *testing.T) {
	summary := summarizeRuntime([]Container{{ID: "unread", State: "running"}}, nil)
	if summary.Unknown != 1 || summary.NoHealthchk != 0 || summary.Healthy != 0 || summary.Status != "notice" {
		t.Fatalf("unread check treated as answer: %+v", summary)
	}
}
