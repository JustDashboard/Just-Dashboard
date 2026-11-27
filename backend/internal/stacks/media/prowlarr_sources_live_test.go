package media

// The live test of the source picker: the pinned Prowlarr, Radarr and Sonarr
// on a network of the test's own, connected the way the stack connects them
// (the prowlarr.apps step), and a Torznab server of the test's own as the only
// source anywhere. The network is internal: nothing on it reaches beyond it,
// so no real site is ever asked anything. It runs only with JD_STACKS_LIVE=1,
// as root:
//
//	go test -c -o /tmp/media.test ./internal/stacks/media
//	sudo -n env JD_STACKS_LIVE=1 PATH=$PATH /tmp/media.test -test.run LiveSourcePicker -test.v
//
// The Torznab server is this test binary: started in a container of the
// pinned Prowlarr image with JD_FAKE_TORZNAB_ADDR set, it serves instead of
// testing (liveTorznabServe). Every container and the network are the test's
// own, named jdv2-p-*, and removed after it.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks/stackstest"
)

const (
	liveSourcesNetwork = "jdv2-p-net"
	liveTorznabPort    = 8080
)

func init() {
	if address := os.Getenv("JD_FAKE_TORZNAB_ADDR"); address != "" {
		os.Exit(liveTorznabServe(address, os.Getenv("JD_FAKE_TORZNAB_KEY")))
	}
}

// liveTorznabServe is a Torznab indexer with one film and one episode, which
// knows one API key. A wrong key is answered 401 with the key echoed back, as
// a site's error page might, so the test sees whether it reaches the person.
func liveTorznabServe(address, key string) int {
	caps := `<?xml version="1.0" encoding="UTF-8"?>
<caps>
  <server version="1.0" title="Just Dashboard test Torznab"/>
  <limits max="100" default="50"/>
  <searching>
    <search available="yes" supportedParams="q"/>
    <tv-search available="yes" supportedParams="q,season,ep"/>
    <movie-search available="yes" supportedParams="q"/>
  </searching>
  <categories>
    <category id="2000" name="Movies"/>
    <category id="5000" name="TV"/>
  </categories>
</caps>`
	item := func(title, category string, id int) string {
		return fmt.Sprintf(`<item>
  <title>%[1]s</title>
  <guid>http://torznab:8080/details/%[3]d</guid>
  <link>http://torznab:8080/download/%[3]d.torrent</link>
  <comments>http://torznab:8080/details/%[3]d</comments>
  <pubDate>%[4]s</pubDate>
  <size>1073741824</size>
  <category>%[2]s</category>
  <enclosure url="http://torznab:8080/download/%[3]d.torrent" length="1073741824" type="application/x-bittorrent"/>
  <torznab:attr name="category" value="%[2]s"/>
  <torznab:attr name="size" value="1073741824"/>
  <torznab:attr name="seeders" value="10"/>
  <torznab:attr name="peers" value="12"/>
  <torznab:attr name="infohash" value="%040d"/>
</item>`, title, category, id, time.Now().UTC().Add(-time.Hour).Format(time.RFC1123Z), id)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		given := query.Get("apikey")
		fmt.Printf("torznab: %s %s t=%s cat=%s key-ok=%v\n", r.Method, r.URL.Path, query.Get("t"), query.Get("cat"), given == key)
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		if given != key {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><error code="100" description="Incorrect user credentials: the key %s is not known"/>`, given)
			return
		}
		if query.Get("t") == "caps" {
			fmt.Fprint(w, caps)
			return
		}
		items := item("Just.Dashboard.Test.Film.2020.1080p.WEB-DL.x264-JDTEST", "2000", 1)
		if strings.HasPrefix(query.Get("cat"), "5") || query.Get("t") == "tvsearch" {
			items = item("Just.Dashboard.Test.Show.S01E01.1080p.WEB-DL.x264-JDTEST", "5000", 2)
		}
		fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom" xmlns:torznab="http://torznab.com/schemas/2015/feed">
<channel>
  <title>Just Dashboard test Torznab</title>
  %s
</channel>
</rss>`, items)
	})
	if err := http.ListenAndServe(address, handler); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func TestLiveSourcePickerAddsTestsSyncsAndRemoves(t *testing.T) {
	if os.Getenv("JD_STACKS_LIVE") != "1" {
		t.Skip("set JD_STACKS_LIVE=1 to run against real containers")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	containers := map[string]string{}
	for _, service := range []string{"prowlarr", "radarr", "sonarr", "torznab"} {
		containers[service] = "jdv2-p-" + service
	}
	remove := func() {
		for _, name := range containers {
			_ = exec.Command("docker", "rm", "-f", "-v", name).Run()
		}
		_ = exec.Command("docker", "network", "rm", liveSourcesNetwork).Run()
	}
	remove()
	t.Cleanup(remove)

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	torznabKey := liveArrKey(t)
	variables := map[string]string{
		VariableRadarrAPIKey:   liveArrKey(t),
		VariableSonarrAPIKey:   liveArrKey(t),
		VariableProwlarrAPIKey: liveArrKey(t),
	}
	liveArrDocker(t, "network", "create", "--internal", liveSourcesNetwork)
	linuxserver := []string{"-e", "PUID=1000", "-e", "PGID=1000", "-e", "TZ=Etc/UTC"}
	start := func(service, image string, arguments ...string) {
		run := []string{"run", "-d", "--name", containers[service], "--network", liveSourcesNetwork, "--network-alias", service}
		run = append(run, arguments...)
		liveArrDocker(t, append(run, image)...)
	}
	start("torznab", liveArrImages["prowlarr"], "--entrypoint", "/fake-torznab", "-v", self+":/fake-torznab:ro",
		"-e", fmt.Sprintf("JD_FAKE_TORZNAB_ADDR=:%d", liveTorznabPort), "-e", "JD_FAKE_TORZNAB_KEY="+torznabKey)
	start("prowlarr", liveArrImages["prowlarr"], append(slices.Clone(linuxserver), "-e", "PROWLARR__AUTH__APIKEY="+variables[VariableProwlarrAPIKey])...)
	start("radarr", liveArrImages["radarr"], append(slices.Clone(linuxserver), "-e", "RADARR__AUTH__APIKEY="+variables[VariableRadarrAPIKey])...)
	start("sonarr", liveArrImages["sonarr"], append(slices.Clone(linuxserver), "-e", "SONARR__AUTH__APIKEY="+variables[VariableSonarrAPIKey])...)

	env := stackstest.Live(t, Manifest(), liveSourcesNetwork, containers, "complete", map[string]string{}, variables)
	liveArrWait(ctx, t, env, "torznab", liveTorznabPort, "/api?t=caps&apikey="+torznabKey)
	for service, port := range map[string]int{"prowlarr": PortProwlarr, "radarr": PortRadarr, "sonarr": PortSonarr} {
		liveArrWait(ctx, t, env, service, port, "/ping")
	}
	run := func(id string) (stacks.Result, error) {
		t.Helper()
		for _, step := range Manifest().Steps {
			if step.ID == id {
				stepCtx, stop := context.WithTimeout(ctx, step.Timeout)
				defer stop()
				return stackstest.Run(stepCtx, env, step)
			}
		}
		t.Fatalf("the manifest has no step %s", id)
		return stacks.Result{}, nil
	}
	wantAction := func(when string) {
		t.Helper()
		_, err := run("prowlarr.sources")
		var action *stacks.ActionError
		if !errors.As(err, &action) || action.Action.ID != actionMediaSource {
			t.Fatalf("%s, prowlarr.sources: want the media-source action, got %v", when, err)
		}
	}
	if result, err := run("prowlarr.apps"); err != nil {
		t.Fatalf("prowlarr.apps: %v", err)
	} else {
		t.Logf("prowlarr.apps: %s", result.Message)
	}
	wantAction("before a source")

	// The catalogue: Prowlarr's own schema, read whole and bounded.
	started := time.Now()
	catalogue, err := FetchSourceCatalogue(ctx, env)
	if err != nil {
		t.Fatal(err)
	}
	public, needs := 0, 0
	for _, definition := range catalogue.Definitions {
		if definition.Privacy == SourcePublic {
			public++
		}
		if definition.NeedsInput {
			needs++
		}
	}
	t.Logf("catalogue: %d torrent definitions (%d public, %d need input) in %s", len(catalogue.Definitions), public, needs, time.Since(started).Round(time.Millisecond))
	// On a network with no way out, Prowlarr has only the definitions built
	// into it (57 torrent ones in 3.1.0): the site definitions it downloads
	// from its own server at start never arrive.
	if len(catalogue.Definitions) < 20 || public == 0 || needs == 0 || needs == len(catalogue.Definitions) {
		t.Fatalf("the catalogue is not Prowlarr's: %d definitions, %d public, %d needing input", len(catalogue.Definitions), public, needs)
	}
	for _, definition := range catalogue.Definitions {
		if detail, found := catalogue.Lookup(definition.Definition); !found || detail.Name != definition.Name {
			t.Fatalf("%s is listed and cannot be read", definition.Definition)
		}
	}
	torznab, found := catalogue.Lookup("Torznab:Generic Torznab")
	if !found {
		t.Fatal("the catalogue has no generic Torznab")
	}
	var fieldNames []string
	for _, field := range torznab.Fields {
		fieldNames = append(fieldNames, field.Name+":"+string(field.Type))
	}
	t.Logf("Generic Torznab's form: %s", strings.Join(fieldNames, ", "))
	if !slices.Contains(fieldNames, "baseUrl:text") || !slices.Contains(fieldNames, "apiKey:password") {
		t.Fatalf("Generic Torznab's form: %v", fieldNames)
	}

	// A source whose test fails: Prowlarr's reason, without what was typed.
	address := fmt.Sprintf("http://torznab:%d", liveTorznabPort)
	wrong := "wrong-" + liveArrKey(t)
	prepared, err := catalogue.Prepare(torznab.Definition, map[string]any{"baseUrl": address, "apiKey": wrong})
	if err != nil {
		t.Fatal(err)
	}
	_, err = AddSource(ctx, env, prepared)
	var failed *SourceTestFailedError
	if !errors.As(err, &failed) {
		t.Fatalf("a wrong key: want source_test_failed, got %v", err)
	}
	t.Logf("a wrong key: %s", failed.Message)
	if strings.Contains(failed.Message, wrong) || strings.Contains(failed.Message, strings.TrimPrefix(wrong, "wrong-")) {
		t.Fatal("the failure carries the key that was typed")
	}
	if sources, err := ListSources(ctx, env); err != nil || len(sources) != 0 {
		t.Fatalf("a source whose test failed was saved: %v %v", sources, err)
	}

	// The right key: tested, saved, synced, and in Radarr and Sonarr.
	if prepared, err = catalogue.Prepare(torznab.Definition, map[string]any{"baseUrl": address + " ", "apiKey": torznabKey}); err != nil {
		t.Fatal(err)
	}
	started = time.Now()
	outcome, err := AddSource(ctx, env, prepared)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("added %+v, synced %v, in %s", outcome.Source, outcome.Synced, time.Since(started).Round(time.Millisecond))
	if outcome.Source.ID <= 0 || outcome.Source.Name != "Generic Torznab" || !outcome.Source.Enabled || !outcome.Synced {
		t.Fatalf("outcome = %+v", outcome)
	}
	sources, err := ListSources(ctx, env)
	if err != nil || len(sources) != 1 || sources[0] != outcome.Source {
		t.Fatalf("sources = %+v %v", sources, err)
	}
	for _, app := range []arrApp{arrRadarr, arrSonarr} {
		indexer := liveSourcesIndexer(ctx, t, env, app, true)
		t.Logf("%s has %q (%s), enabled for RSS %v", app.title, indexer["name"], indexer["implementation"], indexer["enableRss"])
		if indexer["implementation"] != "Torznab" || indexer["protocol"] != "torrent" {
			t.Errorf("%s's indexer = %v", app.title, indexer)
		}
	}
	if result, err := run("prowlarr.sources"); err != nil || !strings.Contains(result.Message, "1 media source") {
		t.Fatalf("prowlarr.sources after the add: %v %+v", err, result)
	}
	if _, err := AddSource(ctx, env, prepared); !errors.As(err, new(*SourceExistsError)) {
		t.Fatalf("the same source twice: %v", err)
	}

	// Removing it takes it out of Radarr and Sonarr too.
	if removed, err := RemoveSource(ctx, env, outcome.Source.ID); err != nil || removed.Name != outcome.Source.Name {
		t.Fatalf("remove: %+v %v", removed, err)
	}
	if _, err := RemoveSource(ctx, env, outcome.Source.ID); !errors.Is(err, ErrSourceNotFound) {
		t.Fatalf("a second removal: %v", err)
	}
	for _, app := range []arrApp{arrRadarr, arrSonarr} {
		liveSourcesIndexer(ctx, t, env, app, false)
	}
	wantAction("after the removal")

	logs, err := env.Logs(ctx, "torznab", 500)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("the Torznab server was asked:\n%s", logs)
	if !strings.Contains(logs, "t=caps") || !strings.Contains(logs, "key-ok=false") || !strings.Contains(logs, "key-ok=true") {
		t.Error("the Torznab server was not asked with both keys")
	}
}

// liveSourcesIndexer waits until the application has, or no longer has, the
// indexer Prowlarr synced to it, and returns it.
func liveSourcesIndexer(ctx context.Context, t *testing.T, env *stacks.DockerEnv, app arrApp, present bool) map[string]any {
	t.Helper()
	client, err := newArrClient(ctx, env, app)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Minute)
	for {
		var indexers []map[string]any
		if err := client.call(ctx, http.MethodGet, "indexer", nil, &indexers); err != nil {
			t.Fatal(err)
		}
		var found map[string]any
		for _, indexer := range indexers {
			if name, _ := indexer["name"].(string); strings.Contains(name, "Generic Torznab") {
				found = indexer
			}
		}
		if (found != nil) == present {
			return found
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: the synced indexer present = %v after two minutes; it has %d indexers", app.title, !present, len(indexers))
		}
		time.Sleep(2 * time.Second)
	}
}
