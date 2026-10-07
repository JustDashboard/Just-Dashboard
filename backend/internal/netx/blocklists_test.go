package netx

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// feedServer stands behind the feed and country-zone addresses. Each path
// answers from its entry; a path with none is a 404.
type feedServer struct {
	*httptest.Server
	mu    sync.Mutex
	pages map[string]feedPage
	hits  map[string]int
}

type feedPage struct {
	status int
	body   string
}

func newFeedServer(t *testing.T) *feedServer {
	t.Helper()
	f := &feedServer{pages: map[string]feedPage{}, hits: map[string]int{}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		p, ok := f.pages[r.URL.Path]
		f.hits[r.URL.Path]++
		f.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		if p.status != 0 && p.status != http.StatusOK {
			w.Header().Set("Location", r.URL.Path)
			w.WriteHeader(p.status)
			return
		}
		fmt.Fprint(w, p.body)
	}))
	t.Cleanup(f.Close)
	// The real client is used as it is — its timeout and its redirect limit
	// are part of what is tested — against a plain-http server.
	prev4, prev6 := ipdenyV4, ipdenyV6
	ipdenyV4, ipdenyV6 = f.URL+"/v4/%s.zone", f.URL+"/v6/%s.zone"
	t.Cleanup(func() { ipdenyV4, ipdenyV6 = prev4, prev6 })
	return f
}

func (f *feedServer) set(path, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pages[path] = feedPage{body: body}
}

func (f *feedServer) hitCount(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[path]
}

const spamhausStyle = `; Spamhaus DROP List 2026/10/07 - (c) 2026 The Spamhaus Project
; https://www.spamhaus.org/drop/drop.txt
; Last-Modified: Wed, 07 Oct 2026 07:30:09 GMT
; Expires: Thu, 08 Oct 2026 07:36:04 GMT
203.0.113.0/24 ; SBL100001
198.51.100.0/25 ; SBL100002
2001:db8:bad::/48 ; SBL100003
10.0.0.0/8 ; a private network the feed has no business listing
192.168.7.0/24 # nor this one
0.0.0.0/0 ; everything
not an address
{"cidr":"192.0.2.0/24","sblid":"SBL100004","rir":"ripencc"}
`

func TestParseFeedReadsSpamhausStyleAndSkipsWhatItMust(t *testing.T) {
	nets, skipped := parseFeed([]byte(spamhausStyle))
	var got []string
	for _, n := range nets {
		got = append(got, n.String())
	}
	want := "203.0.113.0/24,198.51.100.0/25,2001:db8:bad::/48,192.0.2.0/24"
	if strings.Join(got, ",") != want {
		t.Fatalf("networks = %v, want %s", got, want)
	}
	// The two private ranges, the default route and the line that is no address.
	if skipped != 4 {
		t.Fatalf("skipped = %d", skipped)
	}
}

func TestParseFeedFireholNetsetKeepsTheBogonsOut(t *testing.T) {
	nets, _ := parseFeed([]byte("#\n# firehol_level1\n#\n0.0.0.0/8\n10.0.0.0/8\n100.64.0.0/10\n127.0.0.0/8\n172.16.0.0/12\n192.168.0.0/16\n169.254.0.0/16\n198.51.100.7\n203.0.113.0/24\n"))
	var got []string
	for _, n := range nets {
		got = append(got, n.String())
	}
	if strings.Join(got, ",") != "0.0.0.0/8,198.51.100.7/32,203.0.113.0/24" {
		t.Fatalf("networks = %v: a drop in raw cannot be allowed to take the machines behind this host", got)
	}
}

func TestParseFeedSkipsANetworkContainingAPrivateOne(t *testing.T) {
	nets, skipped := parseFeed([]byte("8.0.0.0/5\n10.1.0.0/16\n11.0.0.0/8\n"))
	if len(nets) != 1 || nets[0].String() != "11.0.0.0/8" || skipped != 2 {
		t.Fatalf("nets %v skipped %d", nets, skipped)
	}
}

func TestBuildBlocklistRules(t *testing.T) {
	cases := []struct {
		name string
		req  BlocklistRequest
		kind string
		want string
	}{
		{"manual", BlocklistRequest{Name: "x", Entries: []string{"203.0.113.0/24"}}, "manual", ""},
		{"manual with nothing", BlocklistRequest{Name: "x"}, "manual", "at least one"},
		{"manual with everything", BlocklistRequest{Name: "x", Entries: []string{"0.0.0.0/0"}}, "manual", "every address"},
		{"manual with a word", BlocklistRequest{Name: "x", Entries: []string{"evil"}}, "manual", "IP address"},
		{"manual holding the reader", BlocklistRequest{Name: "x", Entries: []string{"198.51.100.0/24"}}, "manual", "contains your own address"},
		{"country", BlocklistRequest{Name: "x", Countries: []string{"CN", "ru", "cn"}}, "country", ""},
		{"country with a word", BlocklistRequest{Name: "x", Countries: []string{"china"}}, "country", "two-letter"},
		{"country with none", BlocklistRequest{Name: "x"}, "country", "at least one country"},
		{"feed", BlocklistRequest{Name: "x", URL: "https://feed.example/l.txt"}, "feed", ""},
		{"feed with credentials", BlocklistRequest{Name: "x", URL: "https://u:p@feed.example/l.txt"}, "feed", "user name"},
		{"feed over ftp", BlocklistRequest{Name: "x", URL: "ftp://feed.example/l.txt"}, "feed", "http"},
		{"feed preset", BlocklistRequest{Preset: "spamhaus-drop"}, "feed", ""},
		{"unknown preset", BlocklistRequest{Name: "x", Preset: "evil-list"}, "feed", "not a feed"},
		{"a name with a quote", BlocklistRequest{Name: `x" accept`, Entries: []string{"203.0.113.0/24"}}, "manual", "quotes"},
		{"a kind that is none", BlocklistRequest{Name: "x"}, "bogus", "manual, a country or a feed"},
		{"changing kind", BlocklistRequest{Name: "x", Kind: "country", Entries: []string{"203.0.113.0/24"}}, "manual", "cannot become"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := buildBlocklist(c.req, c.kind, "198.51.100.44")
			switch {
			case c.want == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
				t.Fatalf("err = %v, want one containing %q", err, c.want)
			}
		})
	}
	bl, _ := buildBlocklist(BlocklistRequest{Name: "x", Countries: []string{"CN", "ru", "cn"}}, "country", "")
	if strings.Join(bl.Countries, ",") != "cn,ru" {
		t.Errorf("countries = %v", bl.Countries)
	}
	bl, _ = buildBlocklist(BlocklistRequest{Preset: "firehol-level1"}, "feed", "")
	if bl.Name != "FireHOL level 1" || bl.URL != "https://iplists.firehol.org/files/firehol_level1.netset" {
		t.Errorf("preset = %+v", bl)
	}
	var guard *GuardError
	if _, err := buildBlocklist(BlocklistRequest{Name: "x", Entries: []string{"198.51.100.0/24"}}, "manual", "198.51.100.44"); !errors.As(err, &guard) {
		t.Errorf("a manual list holding the reader must be a guard, got %T", err)
	}
}

func TestFeedPresetsAreTheTwoTheBriefNames(t *testing.T) {
	got := map[string]string{}
	for _, p := range feedPresets {
		got[p.ID] = p.URL
		if p.Name == "" || p.Description == "" {
			t.Errorf("%s has no words", p.ID)
		}
	}
	if got["spamhaus-drop"] != "https://www.spamhaus.org/drop/drop.txt" || got["firehol-level1"] != "https://iplists.firehol.org/files/firehol_level1.netset" {
		t.Fatalf("presets = %v", got)
	}
}

func TestAddManualBlocklistMergesAndLoadsIt(t *testing.T) {
	h := newGwHost(t)
	v, err := h.AddBlocklist(context.Background(), BlocklistRequest{
		Name: "scanners", Kind: "manual", Entries: []string{"203.0.113.0/25", "203.0.113.0/24", "203.0.113.77", "2001:db8:bad::/48"},
	}, gwClient, "ops")
	if err != nil {
		t.Fatal(err)
	}
	if v.Count != 2 || strings.Join(v.Entries, ",") != "203.0.113.0/24,2001:db8:bad::/48" || !v.Enabled || v.ContainsYou {
		t.Fatalf("view = %+v", v)
	}
	h.order(t, "nft -c -f", "nft -f", "nft list set", "systemctl")
	for _, want := range []string{"elements = { 203.0.113.0/24 }", "elements = { 2001:db8:bad::/48 }", `ip saddr @bl_1_4 counter drop comment "blocklist:1"`} {
		if !strings.Contains(h.loaded[0], want) {
			t.Errorf("ruleset lacks %q:\n%s", want, h.loaded[0])
		}
	}
	// The reader was added to the trusted set before the list was.
	if got := h.spec(t).Trusted; len(got) != 1 || got[0] != gwClient+"/32" {
		t.Fatalf("trusted = %v", got)
	}
	if h.rec.ran("iptables") {
		t.Error("a list that only drops needs no admission")
	}
}

func TestAddManualBlocklistHoldingTheReaderIsRefusedOutright(t *testing.T) {
	h := newGwHost(t)
	_, err := h.AddBlocklist(context.Background(), BlocklistRequest{Name: "x", Kind: "manual", Entries: []string{"100.64.0.0/10"}}, gwClient, "ops")
	var guard *GuardError
	if !errors.As(err, &guard) || !strings.Contains(err.Error(), gwClient) {
		t.Fatalf("err = %v", err)
	}
	if h.saved() || h.rec.ran("nft -f") {
		t.Error("applied")
	}
}

func TestAddCountryBlocklistFetchesBothFamiliesAndTolerates404ForV6(t *testing.T) {
	srv := newFeedServer(t)
	srv.set("/v4/xx.zone", "192.0.2.0/24\n198.51.100.0/24\n")
	srv.set("/v6/xx.zone", "2001:db8:1::/48\n")
	srv.set("/v4/yy.zone", "203.0.113.0/24\n") // no IPv6 zone: a 404 there
	h := newGwHost(t)
	v, err := h.AddBlocklist(context.Background(), BlocklistRequest{Name: "two countries", Kind: "country", Countries: []string{"XX", "yy"}}, gwClient, "ops")
	if err != nil {
		t.Fatal(err)
	}
	if v.Count != 4 || v.Refreshed == nil || v.Error != "" || v.Kind != "country" || strings.Join(v.Countries, ",") != "xx,yy" {
		t.Fatalf("view = %+v", v)
	}
	b, err := os.ReadFile(filepath.Join(h.paths.Dir, "lists", "1.txt"))
	if err != nil || string(b) != "192.0.2.0/24\n198.51.100.0/24\n203.0.113.0/24\n2001:db8:1::/48\n" {
		t.Fatalf("cache = %q, %v", b, err)
	}
	for _, want := range []string{"elements = { 192.0.2.0/24, 198.51.100.0/24, 203.0.113.0/24 }", "elements = { 2001:db8:1::/48 }"} {
		if !strings.Contains(h.loaded[0], want) {
			t.Errorf("ruleset lacks %q", want)
		}
	}
	// What the spec keeps of it is small; the networks are in the cache.
	if got := h.spec(t).Blocklists[0]; len(got.Entries) != 0 || got.Count != 4 {
		t.Fatalf("spec entry = %+v", got)
	}
}

func TestAddCountryBlocklistWithNoV4ZoneIsRefused(t *testing.T) {
	newFeedServer(t) // every path a 404
	h := newGwHost(t)
	_, err := h.AddBlocklist(context.Background(), BlocklistRequest{Name: "x", Kind: "country", Countries: []string{"zz"}}, gwClient, "ops")
	if err == nil || !strings.Contains(err.Error(), `no address list for the country "ZZ"`) {
		t.Fatalf("err = %v", err)
	}
	if h.saved() {
		t.Error("an empty list was saved as if it protected anything")
	}
}

func TestFeedFollowsRedirectsUpToThree(t *testing.T) {
	// /chain/K redirects to /chain/K-1 until K is zero, then serves the list.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		k := mustAtoi(strings.TrimPrefix(r.URL.Path, "/chain/"))
		if k > 0 {
			http.Redirect(w, r, fmt.Sprintf("/chain/%d", k-1), http.StatusFound)
			return
		}
		fmt.Fprint(w, "203.0.113.0/24\n")
	}))
	defer srv.Close()

	if _, err := fetchList(context.Background(), "feed", nil, srv.URL+"/chain/3"); err != nil {
		t.Fatalf("three redirects were refused: %v", err)
	}
	_, err := fetchList(context.Background(), "feed", nil, srv.URL+"/chain/5")
	if err == nil || !strings.Contains(err.Error(), "more than three times") {
		t.Fatalf("err = %v", err)
	}
}

func mustAtoi(s string) int {
	var n int
	fmt.Sscanf(s, "%d", &n)
	return n
}

func TestFeedBodyOverTheCapIsRefusedNotCutOff(t *testing.T) {
	srv := newFeedServer(t)
	srv.set("/big", strings.Repeat("203.0.113.0/24\n", (maxFeedBytes/15)+10))
	srv.set("/exact", "203.0.113.0/24\n")
	h := newGwHost(t)
	_, err := h.AddBlocklist(context.Background(), BlocklistRequest{Name: "big", Kind: "feed", URL: srv.URL + "/big"}, gwClient, "ops")
	if err == nil || !strings.Contains(err.Error(), "larger than 16 MB") {
		t.Fatalf("err = %v", err)
	}
	if h.saved() {
		t.Error("half a feed was saved as the whole")
	}
	if _, err := h.AddBlocklist(context.Background(), BlocklistRequest{Name: "ok", Kind: "feed", URL: srv.URL + "/exact"}, gwClient, "ops"); err != nil {
		t.Fatal(err)
	}
}

func TestFeedErrorsAreSaidNotSwallowed(t *testing.T) {
	srv := newFeedServer(t)
	srv.set("/empty", "; nothing but a header\n")
	srv.set("/private", "10.0.0.0/8\n192.168.0.0/16\n")
	for path, want := range map[string]string{"/missing": "404", "/empty": "no networks", "/private": "no networks"} {
		_, err := fetchList(context.Background(), "feed", nil, srv.URL+path)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want one containing %q", path, err, want)
		}
	}
}

func TestRefreshReloadsTheTableWithTheNewNetworks(t *testing.T) {
	srv := newFeedServer(t)
	srv.set("/feed", "203.0.113.0/24\n")
	h := newGwHost(t)
	ctx := context.Background()
	if _, err := h.AddBlocklist(ctx, BlocklistRequest{Name: "feed", Kind: "feed", URL: srv.URL + "/feed"}, gwClient, "ops"); err != nil {
		t.Fatal(err)
	}
	before := len(h.loaded)
	srv.set("/feed", "203.0.113.0/24\n198.51.100.0/24\n198.51.100.128/25\n")
	if err := h.RefreshBlocklist(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if len(h.loaded) != before+1 || !strings.Contains(h.loaded[before], "elements = { 198.51.100.0/24, 203.0.113.0/24 }") {
		t.Fatalf("the table was not reloaded with the new set; loaded %d, want %d", len(h.loaded), before+1)
	}
	if bl := h.spec(t).Blocklists[0]; bl.Count != 2 || bl.Error != "" || bl.Refreshed.IsZero() {
		t.Fatalf("spec = %+v", bl)
	}
}

func TestRefreshFailureKeepsTheListAndSaysWhy(t *testing.T) {
	srv := newFeedServer(t)
	srv.set("/feed", "203.0.113.0/24\n")
	h := newGwHost(t)
	ctx := context.Background()
	if _, err := h.AddBlocklist(ctx, BlocklistRequest{Name: "feed", Kind: "feed", URL: srv.URL + "/feed"}, gwClient, "ops"); err != nil {
		t.Fatal(err)
	}
	refreshed := h.spec(t).Blocklists[0].Refreshed
	srv.mu.Lock()
	delete(srv.pages, "/feed")
	srv.mu.Unlock()
	loaded := len(h.loaded)
	if err := h.RefreshBlocklist(ctx, 1); err == nil {
		t.Fatal("a 404 was a refresh")
	}
	bl := h.spec(t).Blocklists[0]
	if !strings.Contains(bl.Error, "404") || bl.Count != 1 || !bl.Refreshed.Equal(refreshed) {
		t.Fatalf("spec = %+v", bl)
	}
	if b, _ := os.ReadFile(filepath.Join(h.paths.Dir, "lists", "1.txt")); string(b) != "203.0.113.0/24\n" {
		t.Fatalf("the cache was changed: %q", b)
	}
	if len(h.loaded) != loaded {
		t.Error("a failed refresh reloaded the table")
	}
	// The next good fetch clears the message.
	srv.set("/feed", "203.0.113.0/24\n")
	if err := h.RefreshBlocklist(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if bl := h.spec(t).Blocklists[0]; bl.Error != "" {
		t.Fatalf("Error = %q", bl.Error)
	}
}

func TestRefreshPutsTheCacheBackWhenTheTableCannotBeLoaded(t *testing.T) {
	srv := newFeedServer(t)
	srv.set("/feed", "203.0.113.0/24\n")
	h := newGwHost(t)
	ctx := context.Background()
	if _, err := h.AddBlocklist(ctx, BlocklistRequest{Name: "feed", Kind: "feed", URL: srv.URL + "/feed"}, gwClient, "ops"); err != nil {
		t.Fatal(err)
	}
	srv.set("/feed", "198.51.100.0/24\n")
	h.first("nft -f", "Error: boom", errors.New("Error: boom"))
	if err := h.RefreshBlocklist(ctx, 1); err == nil {
		t.Fatal("expected the load failure")
	}
	if b, _ := os.ReadFile(filepath.Join(h.paths.Dir, "lists", "1.txt")); string(b) != "203.0.113.0/24\n" {
		t.Fatalf("the cache describes a list that never loaded: %q", b)
	}
}

func TestRefreshOfAManualListAndOfAMissingOneIsRefused(t *testing.T) {
	h := newGwHost(t)
	ctx := context.Background()
	if _, err := h.AddBlocklist(ctx, BlocklistRequest{Name: "m", Kind: "manual", Entries: []string{"203.0.113.0/24"}}, gwClient, "ops"); err != nil {
		t.Fatal(err)
	}
	if err := h.RefreshBlocklist(ctx, 1); err == nil || !strings.Contains(err.Error(), "nothing to fetch") {
		t.Errorf("err = %v", err)
	}
	if err := h.RefreshBlocklist(ctx, 77); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}

func TestRefresherSkipsFreshListsAndRefreshesTheStaleOnesOneAtATime(t *testing.T) {
	srv := newFeedServer(t)
	for _, p := range []string{"/fresh", "/stale", "/never", "/off", "/failing-ok"} {
		srv.set(p, "203.0.113.0/24\n")
	}
	h := newGwHost(t)
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	sp := emptySpec()
	for i, e := range []struct {
		path    string
		age     time.Duration
		enabled bool
		kind    string
	}{
		{"/fresh", 3 * time.Hour, true, "feed"},
		{"/stale", 25 * time.Hour, true, "feed"},
		{"/never", 0, true, "feed"},
		{"/off", 72 * time.Hour, false, "feed"},
		{"", 72 * time.Hour, true, "manual"},
	} {
		bl := BlocklistSpec{ID: i + 1, Name: fmt.Sprintf("list %d", i+1), Kind: e.kind, Enabled: e.enabled, Count: 1}
		if e.kind == "feed" {
			bl.URL = srv.URL + e.path
		} else {
			bl.Entries = []string{"203.0.113.0/24"}
		}
		if e.age > 0 {
			bl.Refreshed = now.Add(-e.age)
		}
		sp.Blocklists = append(sp.Blocklists, bl)
	}
	sp.NextID = 10
	h.seed(t, sp)

	h.refreshStale(context.Background(), now)
	for path, want := range map[string]int{"/fresh": 0, "/stale": 1, "/never": 1, "/off": 0} {
		if got := srv.hitCount(path); got != want {
			t.Errorf("%s fetched %d times, want %d", path, got, want)
		}
	}
	got := h.spec(t)
	// Refreshing stamps the real clock, not the one the pass was asked about.
	recent := time.Now().Add(-time.Minute)
	if !got.Blocklists[1].Refreshed.After(recent) || !got.Blocklists[2].Refreshed.After(recent) {
		t.Errorf("refreshed stamps: %v %v", got.Blocklists[1].Refreshed, got.Blocklists[2].Refreshed)
	}
	if !got.Blocklists[0].Refreshed.Equal(now.Add(-3 * time.Hour)) {
		t.Error("a fresh list was touched")
	}
}

func TestRefresherKeepsTryingAListThatFailsAndSaysWhyOnIt(t *testing.T) {
	srv := newFeedServer(t) // /gone is a 404
	h := newGwHost(t)
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	sp := emptySpec()
	sp.NextID = 5
	sp.Blocklists = []BlocklistSpec{{ID: 1, Name: "gone", Kind: "feed", URL: srv.URL + "/gone", Enabled: true, Count: 3, Refreshed: now.Add(-48 * time.Hour)}}
	h.seed(t, sp)
	h.refreshStale(context.Background(), now)
	h.refreshStale(context.Background(), now.Add(time.Hour))
	if got := srv.hitCount("/gone"); got != 2 {
		t.Errorf("a failing list was fetched %d times in two passes, want 2", got)
	}
	bl := h.spec(t).Blocklists[0]
	if !strings.Contains(bl.Error, "404") || bl.Count != 3 || !bl.Refreshed.Equal(now.Add(-48*time.Hour)) {
		t.Fatalf("spec = %+v", bl)
	}
}

func TestRefreshLoopRunsAPassAndStopsWithItsContext(t *testing.T) {
	srv := newFeedServer(t)
	srv.set("/stale", "203.0.113.0/24\n")
	h := newGwHost(t)
	sp := emptySpec()
	sp.NextID = 5
	sp.Blocklists = []BlocklistSpec{{ID: 1, Name: "s", Kind: "feed", URL: srv.URL + "/stale", Enabled: true, Count: 1, Refreshed: time.Now().Add(-48 * time.Hour)}}
	h.seed(t, sp)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		h.refreshLoop(ctx, 10*time.Millisecond, time.Hour)
		close(done)
	}()
	deadline := time.After(5 * time.Second)
	for srv.hitCount("/stale") == 0 {
		select {
		case <-deadline:
			t.Fatal("the refresher never ran")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the refresher outlived its context")
	}
}

func TestUpdateBlocklist(t *testing.T) {
	srv := newFeedServer(t)
	srv.set("/v4/xx.zone", "192.0.2.0/24\n")
	srv.set("/v4/yy.zone", "198.51.100.0/24\n")
	h := newGwHost(t)
	ctx := context.Background()
	v, err := h.AddBlocklist(ctx, BlocklistRequest{Name: "c", Kind: "country", Countries: []string{"xx"}}, gwClient, "ops")
	if err != nil {
		t.Fatal(err)
	}
	fetches := srv.hitCount("/v4/xx.zone")

	// A rename and a switch do not fetch again.
	off := false
	if _, err := h.UpdateBlocklist(ctx, v.ID, BlocklistRequest{Name: "renamed", Countries: []string{"xx"}, Enabled: &off}, gwClient, "ops"); err != nil {
		t.Fatal(err)
	}
	if srv.hitCount("/v4/xx.zone") != fetches {
		t.Error("a rename fetched the list again")
	}
	if bl := h.spec(t).Blocklists[0]; bl.Name != "renamed" || bl.Enabled || bl.CreatedBy != "ops" {
		t.Fatalf("spec = %+v", bl)
	}
	if strings.Contains(h.loaded[len(h.loaded)-1], "bl_1_4") {
		t.Error("a disabled list is still loaded")
	}

	// Other countries do.
	on := true
	if v, err = h.UpdateBlocklist(ctx, v.ID, BlocklistRequest{Name: "renamed", Countries: []string{"yy", "xx"}, Enabled: &on}, gwClient, "ops"); err != nil {
		t.Fatal(err)
	}
	if srv.hitCount("/v4/yy.zone") != 1 || v.Count != 2 || strings.Join(v.Countries, ",") != "xx,yy" {
		t.Fatalf("view = %+v", v)
	}

	// A failed fetch leaves the list as it was.
	srv.mu.Lock()
	delete(srv.pages, "/v4/yy.zone")
	srv.mu.Unlock()
	if _, err := h.UpdateBlocklist(ctx, v.ID, BlocklistRequest{Name: "renamed", Countries: []string{"yy"}}, gwClient, "ops"); err == nil {
		t.Fatal("expected the fetch failure")
	}
	if bl := h.spec(t).Blocklists[0]; strings.Join(bl.Countries, ",") != "xx,yy" {
		t.Fatalf("countries = %v", bl.Countries)
	}

	if _, err := h.UpdateBlocklist(ctx, 9, BlocklistRequest{Name: "x", Countries: []string{"xx"}}, gwClient, "ops"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v", err)
	}
	if _, err := h.UpdateBlocklist(ctx, v.ID, BlocklistRequest{Name: "x", Kind: "manual", Entries: []string{"203.0.113.0/24"}}, gwClient, "ops"); err == nil {
		t.Error("a list changed its kind")
	}
}

func TestUpdateManualBlocklistReplacesItsEntries(t *testing.T) {
	h := newGwHost(t)
	ctx := context.Background()
	v, err := h.AddBlocklist(ctx, BlocklistRequest{Name: "m", Kind: "manual", Entries: []string{"203.0.113.0/24"}}, gwClient, "ops")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.UpdateBlocklist(ctx, v.ID, BlocklistRequest{Name: "m", Entries: []string{"198.51.100.7"}}, gwClient, "ops"); err != nil {
		t.Fatal(err)
	}
	if bl := h.spec(t).Blocklists[0]; strings.Join(bl.Entries, ",") != "198.51.100.7/32" || bl.Count != 1 {
		t.Fatalf("spec = %+v", bl)
	}
	if _, err := h.UpdateBlocklist(ctx, v.ID, BlocklistRequest{Name: "m", Entries: []string{"100.64.0.0/10"}}, gwClient, "ops"); err == nil {
		t.Fatal("an edit that would list the reader was accepted")
	}
}

func TestDeleteBlocklistRemovesItsCacheAfterTheChange(t *testing.T) {
	srv := newFeedServer(t)
	srv.set("/feed", "203.0.113.0/24\n")
	h := newGwHost(t)
	ctx := context.Background()
	if _, err := h.AddBlocklist(ctx, BlocklistRequest{Name: "f", Kind: "feed", URL: srv.URL + "/feed"}, gwClient, "ops"); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(h.paths.Dir, "lists", "1.txt")
	// A change that fails keeps the cache.
	h.first("nft -f", "boom", errors.New("boom"))
	if err := h.DeleteBlocklist(ctx, 1, gwClient); err == nil {
		t.Fatal("expected failure")
	}
	if _, err := os.Stat(cache); err != nil {
		t.Fatal("the cache of a list that still exists was removed")
	}
	h.rec.mu.Lock()
	h.rec.replies = h.rec.replies[1:]
	h.rec.mu.Unlock()
	if err := h.DeleteBlocklist(ctx, 1, gwClient); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cache); err == nil {
		t.Fatal("the cache outlived its list")
	}
	if len(h.spec(t).Blocklists) != 0 || strings.Contains(h.loaded[len(h.loaded)-1], "bl_1") {
		t.Fatal("still loaded")
	}
}

func TestAFailedAddLeavesNoCacheBehind(t *testing.T) {
	srv := newFeedServer(t)
	srv.set("/feed", "203.0.113.0/24\n")
	h := newGwHost(t)
	h.first("nft -f", "boom", errors.New("boom"))
	if _, err := h.AddBlocklist(context.Background(), BlocklistRequest{Name: "f", Kind: "feed", URL: srv.URL + "/feed"}, gwClient, "ops"); err == nil {
		t.Fatal("expected failure")
	}
	if _, err := os.Stat(filepath.Join(h.paths.Dir, "lists", "1.txt")); err == nil {
		t.Fatal("a cache for a list that was never saved")
	}
}

func TestReadBlocklistSkipsGarbageAndAMissingFile(t *testing.T) {
	dir := t.TempDir()
	if got := readBlocklist(dir, 1); got != nil {
		t.Fatalf("a missing cache read as %v", got)
	}
	if err := os.WriteFile(blocklistFile(dir, 1), []byte("203.0.113.0/24\ngarbage; accept\n\n198.51.100.7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := readBlocklist(dir, 1)
	if len(got) != 2 || got[0] != netip.MustParsePrefix("203.0.113.0/24") {
		t.Fatalf("read %v", got)
	}
	if readBlocklist("", 1) != nil {
		t.Fatal("no directory should read as no list")
	}
}
