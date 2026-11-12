package dockerx

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// The proxy's ingress lookup reads `docker inspect` of every running
// container. InspectRunning is that output from the API, and a container that
// went away between the listing and its inspect is left out rather than
// failing the whole reading, as it failed the CLI's.
func TestInspectRunningIsDockerInspectOfWhatRuns(t *testing.T) {
	var asked []string
	c := cacheTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path+"?"+r.URL.RawQuery)
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			fmt.Fprint(w, `[{"Id":"aaa"},{"Id":"gone"},{"Id":"bbb"}]`)
		case strings.HasSuffix(r.URL.Path, "/containers/gone/json"):
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"No such container: gone"}`)
		case strings.HasSuffix(r.URL.Path, "/containers/aaa/json"):
			fmt.Fprint(w, `{"Id":"aaa","Name":"/edge","State":{"Running":true},"Config":{"Cmd":["caddy","run"]},"Unknown":{"kept":true}}`)
		case strings.HasSuffix(r.URL.Path, "/containers/bbb/json"):
			fmt.Fprint(w, `{"Id":"bbb","Name":"/app","State":{"Running":true}}`)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	})
	raw, err := c.InspectRunning(cacheTestContext(t))
	if err != nil {
		t.Fatal(err)
	}
	var containers []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &containers); err != nil {
		t.Fatalf("%v in %s", err, raw)
	}
	if len(containers) != 2 || string(containers[0]["Id"]) != `"aaa"` || string(containers[1]["Id"]) != `"bbb"` {
		t.Fatalf("got %s", raw)
	}
	// The daemon's own bytes, not a re-encoding of the fields this SDK knows.
	if string(containers[0]["Unknown"]) != `{"kept":true}` {
		t.Fatalf("the daemon's answer was not passed through: %s", raw)
	}
	// Running containers only, as `docker ps -q` lists.
	if len(asked) == 0 || strings.Contains(asked[0], "all=1") {
		t.Fatalf("the listing asked %q", asked)
	}

	raw, err = c.InspectOne(cacheTestContext(t), "aaa")
	if err != nil || json.Unmarshal(raw, &containers) != nil || len(containers) != 1 || string(containers[0]["Name"]) != `"/edge"` {
		t.Fatalf("InspectOne: %s, %v", raw, err)
	}
	if _, err := c.InspectOne(cacheTestContext(t), "gone"); err == nil {
		t.Fatal("a container that is not there was inspected")
	}
}

func TestInspectRunningWithoutContainersIsAnEmptyList(t *testing.T) {
	c := cacheTestClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `[]`) })
	raw, err := c.InspectRunning(cacheTestContext(t))
	if err != nil || string(raw) != "[]" {
		t.Fatalf("got %q, %v", raw, err)
	}
	if _, err := New("unix:///nonexistent/docker.sock").InspectRunning(cacheTestContext(t)); !Unreachable(err) {
		t.Fatalf("a daemon that is not there: %v", err)
	}
}
