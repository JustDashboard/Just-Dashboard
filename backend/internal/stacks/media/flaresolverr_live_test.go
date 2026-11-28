package media

// The live test of the FlareSolverr step: the pinned Prowlarr and FlareSolverr
// on a network of the test's own, with a Torznab server of the test's own
// (liveTorznabServe) as the person's source. It runs only with
// JD_STACKS_LIVE=1, as root:
//
//	go test -c -o /tmp/media.test ./internal/stacks/media
//	sudo -n env JD_STACKS_LIVE=1 PATH=$PATH /tmp/media.test -test.run LiveFlareSolverr -test.v
//
// Every container and the network are the test's own, named jdfs-*, and
// removed after it. No real site is asked anything.

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks/stackstest"
)

const liveFlareSolverrNetwork = "jdfs-net"

func TestLiveFlareSolverrProxyAndTags(t *testing.T) {
	if os.Getenv("JD_STACKS_LIVE") != "1" {
		t.Skip("set JD_STACKS_LIVE=1 to run against real containers")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	containers := map[string]string{}
	for _, service := range []string{"prowlarr", "flaresolverr", "torznab"} {
		containers[service] = "jdfs-" + service
	}
	remove := func() {
		for _, name := range containers {
			_ = exec.Command("docker", "rm", "-f", "-v", name).Run()
		}
		_ = exec.Command("docker", "network", "rm", liveFlareSolverrNetwork).Run()
	}
	remove()
	t.Cleanup(remove)

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	torznabKey := liveArrKey(t)
	variables := map[string]string{VariableProwlarrAPIKey: liveArrKey(t)}
	liveArrDocker(t, "network", "create", liveFlareSolverrNetwork)
	start := func(service, image string, arguments ...string) {
		run := []string{"run", "-d", "--name", containers[service], "--network", liveFlareSolverrNetwork, "--network-alias", service}
		run = append(run, arguments...)
		liveArrDocker(t, append(run, image)...)
	}
	start("torznab", liveArrImages["prowlarr"], "--entrypoint", "/fake-torznab", "-v", self+":/fake-torznab:ro",
		"-e", fmt.Sprintf("JD_FAKE_TORZNAB_ADDR=:%d", liveTorznabPort), "-e", "JD_FAKE_TORZNAB_KEY="+torznabKey)
	start("prowlarr", liveArrImages["prowlarr"], "-e", "PUID=1000", "-e", "PGID=1000", "-e", "TZ=Etc/UTC",
		"-e", "PROWLARR__AUTH__APIKEY="+variables[VariableProwlarrAPIKey])
	start("flaresolverr", liveArrImages["flaresolverr"], "-e", "TZ=Etc/UTC", "-e", "LOG_LEVEL=info")

	env := stackstest.Live(t, Manifest(), liveFlareSolverrNetwork, containers, "complete", map[string]string{}, variables)
	liveArrWait(ctx, t, env, "torznab", liveTorznabPort, "/api?t=caps&apikey="+torznabKey)
	liveArrWait(ctx, t, env, "prowlarr", PortProwlarr, "/ping")
	liveArrWait(ctx, t, env, "flaresolverr", PortFlareSolver, "/health")
	run := func(id string) stacks.Result {
		t.Helper()
		for _, step := range Manifest().Steps {
			if step.ID == id {
				stepCtx, stop := context.WithTimeout(ctx, step.Timeout)
				defer stop()
				result, err := stackstest.Run(stepCtx, env, step)
				if err != nil {
					t.Fatalf("%s: %v", id, err)
				}
				t.Logf("%s: %s %v", id, result.Message, result.Outputs)
				return result
			}
		}
		t.Fatalf("the manifest has no step %s", id)
		return stacks.Result{}
	}
	catalogue, err := FetchSourceCatalogue(ctx, env)
	if err != nil {
		t.Fatal(err)
	}
	torznab, found := catalogue.Lookup("Torznab:Generic Torznab")
	if !found {
		t.Fatal("the catalogue has no generic Torznab")
	}
	address := fmt.Sprintf("http://torznab:%d", liveTorznabPort)
	prepared, err := catalogue.Prepare(torznab.Definition, map[string]any{"baseUrl": address, "apiKey": torznabKey})
	if err != nil {
		t.Fatal(err)
	}

	// A source the person had before FlareSolverr came: added without the tag.
	before, err := AddSource(ctx, env, prepared)
	if err != nil {
		t.Fatal(err)
	}
	client, err := newArrClient(ctx, env, arrProwlarr)
	if err != nil {
		t.Fatal(err)
	}
	indexer := func(id int) map[string]any {
		t.Helper()
		var entry map[string]any
		if err := client.call(ctx, http.MethodGet, "indexer/"+strconv.Itoa(id), nil, &entry); err != nil {
			t.Fatal(err)
		}
		return entry
	}
	if tags := sourceTags(indexer(before.Source.ID)["tags"]); len(tags) != 0 {
		t.Fatalf("a source added before the proxy has tags %v", tags)
	}

	first := run("prowlarr.flaresolverr")
	tag, _ := strconv.Atoi(first.Outputs["tagId"])
	if tag <= 0 || !strings.Contains(first.Message, "1 media source tagged for it") {
		t.Fatalf("first run: %+v", first)
	}
	stored := indexer(before.Source.ID)
	if !slices.Contains(sourceTags(stored["tags"]), tag) {
		t.Fatalf("the source was not tagged: %v", stored["tags"])
	}
	// Its key survived being sent back masked: Prowlarr's test of it as
	// stored reaches the Torznab server with the right one (a wrong one is
	// refused 401 there).
	if err := client.call(ctx, http.MethodPost, "indexer/test", stored, nil); err != nil {
		t.Fatalf("the tagged source no longer passes Prowlarr's test: %v", err)
	}
	var proxies []map[string]any
	if err := client.call(ctx, http.MethodGet, "indexerproxy", nil, &proxies); err != nil {
		t.Fatal(err)
	}
	if len(proxies) != 1 || proxies[0]["name"] != flareSolverrProxy || !slices.Contains(sourceTags(proxies[0]["tags"]), tag) {
		t.Fatalf("proxies = %v", proxies)
	}

	second := run("prowlarr.flaresolverr")
	if !strings.Contains(second.Message, "already") || second.Outputs["tagId"] != first.Outputs["tagId"] ||
		second.Outputs["proxyId"] != first.Outputs["proxyId"] {
		t.Fatalf("second run: %+v", second)
	}

	// A source added now carries the tag from its first save.
	if _, err := RemoveSource(ctx, env, before.Source.ID); err != nil {
		t.Fatal(err)
	}
	after, err := AddSource(ctx, env, prepared)
	if err != nil {
		t.Fatal(err)
	}
	if tags := sourceTags(indexer(after.Source.ID)["tags"]); !slices.Equal(tags, []int{tag}) {
		t.Fatalf("a source added with the proxy has tags %v", tags)
	}
}
