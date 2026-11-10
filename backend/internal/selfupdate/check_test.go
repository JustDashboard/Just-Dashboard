package selfupdate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const twoReleases = `{"releases":[
	{"version":"0.6","date":"2026-09-01","title":"Six","summary":"s","changes":[{"kind":"added","text":"a thing"}]},
	{"version":"0.5","date":"2026-08-01","title":"Five","summary":"s","changes":[{"kind":"added","text":"another"}]}]}`

func serving(t *testing.T, status int, body string) (*httptest.Server, *[]string) {
	t.Helper()
	agents := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agents = append(agents, r.Header.Get("User-Agent"))
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &agents
}

func checkerAgainst(t *testing.T, url, current string) *Checker {
	t.Helper()
	c := NewChecker(current, "owner/repo", "main", true, quiet())
	c.baseURL = url
	return c
}

func TestCheckFindsANewerRelease(t *testing.T) {
	srv, agents := serving(t, http.StatusOK, twoReleases)
	c := checkerAgainst(t, srv.URL, "0.5")

	res, err := c.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available || res.Latest != "0.6" {
		t.Fatalf("check reported %+v", res)
	}
	if len(res.Releases) != 1 || res.Releases[0].Version != "0.6" {
		t.Fatalf("releases %+v — only the ones newer than the install belong here", res.Releases)
	}
	// The request identifies the product and its version, and nothing else.
	// There is no telemetry in this dashboard and this is the line that has to
	// keep being true.
	if len(*agents) != 1 || (*agents)[0] != "just-dashboard/0.5" {
		t.Fatalf("user agent was %v", *agents)
	}
	if !strings.HasSuffix(res.Source, ManifestPath) {
		t.Errorf("source %q does not name the file that was read", res.Source)
	}
}

func TestCheckOnTheNewestVersionOffersNothing(t *testing.T) {
	srv, _ := serving(t, http.StatusOK, twoReleases)
	res, err := checkerAgainst(t, srv.URL, "0.6").Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Available || len(res.Releases) != 0 {
		t.Fatalf("an up-to-date install was offered %+v", res.Releases)
	}
}

// A server behind a firewall is a normal state for this product. The failure
// has to be reported as information — and the answer it already had has to
// survive, or a dropped tunnel makes the update banner blink out of existence.
func TestCheckKeepsTheLastGoodAnswer(t *testing.T) {
	body := twoReleases
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	defer srv.Close()
	c := checkerAgainst(t, srv.URL, "0.5")
	if _, err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	status, body = http.StatusInternalServerError, "boom"
	// The floor on forced re-checks would otherwise serve the cache and never
	// exercise the failure path.
	c.mu.Lock()
	c.result.CheckedAt = c.result.CheckedAt.Add(-time.Hour)
	c.mu.Unlock()

	res, err := c.Refresh(context.Background())
	if err == nil {
		t.Fatal("a 500 was reported as a successful check")
	}
	if res.Error == "" {
		t.Error("the failure is not reported to the operator")
	}
	if res.Latest != "0.6" || !res.Available {
		t.Fatalf("the previous answer was thrown away on one failed check: %+v", res)
	}
}

func TestCheckRejectsAManifestItCannotRead(t *testing.T) {
	srv, _ := serving(t, http.StatusOK, `{"releases":[{"version":"tomorrow"}]}`)
	res, err := checkerAgainst(t, srv.URL, "0.5").Refresh(context.Background())
	if err == nil {
		t.Fatal("a malformed changelog was accepted")
	}
	if res.Available {
		t.Fatal("an update was offered from a changelog that could not be parsed")
	}
	if !strings.Contains(res.Error, "changelog") {
		t.Errorf("error %q does not say what could not be read", res.Error)
	}
}

// Turning the check off has to mean no request at all, not a request whose
// answer is ignored.
func TestCheckDisabledMakesNoRequest(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Write([]byte(twoReleases))
	}))
	defer srv.Close()
	c := NewChecker("0.5", "owner/repo", "main", false, quiet())
	c.baseURL = srv.URL
	c.Start(context.Background())
	if _, err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.Nudge(checkInterval)
	if calls != 0 {
		t.Fatalf("JD_UPDATE_CHECK=false still made %d request(s)", calls)
	}
	if c.Result().Enabled {
		t.Error("a disabled check reports as enabled, so the UI would show it as up to date")
	}
}

func TestCheckerURL(t *testing.T) {
	c := NewChecker("0.5", " Wayy01/Just-Dashboard/ ", " main ", true, quiet())
	want := "https://raw.githubusercontent.com/Wayy01/Just-Dashboard/main/" + ManifestPath
	if got := c.URL(); got != want {
		t.Fatalf("URL is %q, want %q", got, want)
	}
	// A fork with nothing configured still reads the project it came from.
	if got := NewChecker("0.5", "", "", true, quiet()).URL(); !strings.Contains(got, DefaultRepo) {
		t.Fatalf("default URL is %q", got)
	}
}

// One field this version has never heard of, in a release it is not on yet.
// The check used to answer "the published changelog could not be read" on every
// install, and the release that taught them to read it was in that file.
func TestCheckReadsAChangelogWrittenByANewerRelease(t *testing.T) {
	srv, _ := serving(t, http.StatusOK, `{"schema":2,"releases":[
		{"version":"0.6","date":"2026-09-01","title":"Six","summary":"s","signedBy":"release key",
			"changes":[{"kind":"performance","text":"a thing"}]},
		{"version":"0.5","date":"2026-08-01","title":"Five","summary":"s","changes":[{"kind":"added","text":"another"}]}]}`)
	res, err := checkerAgainst(t, srv.URL, "0.5").Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available || res.Latest != "0.6" || res.Error != "" {
		t.Fatalf("check reported %+v", res)
	}
}

// With nothing readable to offer and something unreadable published, "up to
// date" is the one wrong answer.
func TestCheckSaysWhenTheOnlyNewerReleaseCannotBeRead(t *testing.T) {
	srv, _ := serving(t, http.StatusOK, `{"latest":"0.6","releases":[
		{"version":"0.6","date":"2026-09-01","title":"Six","changes":{"added":["a thing"]}},
		{"version":"0.5","date":"2026-08-01","title":"Five","summary":"s","changes":[{"kind":"added","text":"another"}]}]}`)
	res, err := checkerAgainst(t, srv.URL, "0.5").Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Available {
		t.Fatalf("offered %+v from an entry that could not be read", res.Releases)
	}
	if !strings.Contains(res.Error, "0.6") || !strings.Contains(res.Error, "shell") {
		t.Fatalf("error %q does not say that 0.6 is out or where to install it from", res.Error)
	}
}

// A file longer than this version reads is said to be too long. Cut off at the
// ceiling it was JSON that stopped mid-word, reported as a file that "could not
// be read".
func TestCheckSaysWhenTheChangelogIsLongerThanItReads(t *testing.T) {
	padding := strings.Repeat(" ", maxManifest)
	srv, _ := serving(t, http.StatusOK, twoReleases+padding)
	res, err := checkerAgainst(t, srv.URL, "0.5").Refresh(context.Background())
	if err == nil || !strings.Contains(res.Error, "larger than") {
		t.Fatalf("err %v, reported %q", err, res.Error)
	}
	if res.Available {
		t.Fatal("offered an update from a file it did not finish reading")
	}
}

func TestCheckReadsAChangelogPastTheOldCeiling(t *testing.T) {
	if maxManifest <= FieldManifestCap {
		t.Fatalf("this version reads %d bytes, no more than the %d the field already reads", maxManifest, FieldManifestCap)
	}
	srv, _ := serving(t, http.StatusOK, twoReleases+strings.Repeat(" ", FieldManifestCap))
	res, err := checkerAgainst(t, srv.URL, "0.5").Refresh(context.Background())
	if err != nil || !res.Available {
		t.Fatalf("err %v, reported %+v", err, res)
	}
}
