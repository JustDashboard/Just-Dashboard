package netx

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
	"golang.org/x/sys/unix"
)

// Exceptions, trusted notes, refresh schedules, feed integrity and diffs,
// previews, session revocation, pressure and per-interface kernel values.

func gwClock(t *testing.T, at time.Time) *time.Time {
	t.Helper()
	now := at
	prev := gatewayNow
	gatewayNow = func() time.Time { return now }
	t.Cleanup(func() { gatewayNow = prev })
	return &now
}

func gwLastLoaded(t *testing.T, h *gwHost) string {
	t.Helper()
	if len(h.loaded) == 0 {
		t.Fatal("nothing was loaded")
	}
	return h.loaded[len(h.loaded)-1]
}

func TestExceptionsAreRenderedScopedAndExpireOutOfTheSpec(t *testing.T) {
	h := newGwHost(t)
	now := gwClock(t, time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC))
	sp := emptySpec()
	sp.NextID = 5
	sp.Blocklists = []BlocklistSpec{{ID: 1, Name: "scanners", Kind: "manual", Entries: []string{"198.51.100.0/24"}, Enabled: true, Count: 1}}
	h.seed(t, sp)

	v, err := h.AddException(context.Background(), ExceptionRequest{Address: "198.51.100.7", Scope: "blocklist:1", Reason: "partner monitor", ExpiresAt: "2026-10-09T18:00:00Z"}, "ops")
	if err != nil {
		t.Fatal(err)
	}
	if v.ScopeName != "scanners" || v.ExpiresAt == nil || v.Expired || v.Address != "198.51.100.7/32" {
		t.Fatalf("view = %+v", v)
	}
	loaded := gwLastLoaded(t, h)
	if !strings.Contains(loaded, "jump bx_1") || !strings.Contains(loaded, fmt.Sprintf("meta time < %d counter return comment \"exception:%d\"", time.Date(2026, 10, 9, 18, 0, 0, 0, time.UTC).Unix(), v.ID)) {
		t.Fatalf("loaded:\n%s", loaded)
	}
	if _, err := h.AddException(context.Background(), ExceptionRequest{Address: "198.51.100.7/32", Scope: "blocklist:1", Reason: "again"}, "ops"); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate: %v", err)
	}
	all, err := h.AddException(context.Background(), ExceptionRequest{Address: "2001:db8:77::/48", Reason: "vendor"}, "ops")
	if err != nil || all.Scope != "all" {
		t.Fatalf("all scope: %+v %v", all, err)
	}

	// Past its expiry the kernel already ignores the rule; the sweeper
	// takes it out of the spec on the next pass.
	*now = time.Date(2026, 10, 9, 18, 0, 1, 0, time.UTC)
	views := exceptionViews(h.spec(t), nil)
	if !views[0].Expired || views[1].Expired {
		t.Fatalf("views = %+v", views)
	}
	removed, err := h.sweepExpired(context.Background())
	if err != nil || !removed {
		t.Fatalf("sweep: %v %v", removed, err)
	}
	got := h.spec(t).Exceptions
	if len(got) != 1 || got[0].ID != all.ID {
		t.Fatalf("after sweep: %+v", got)
	}
	if removed, _ := h.sweepExpired(context.Background()); removed {
		t.Fatal("a second sweep found something")
	}
}

func TestRemovingTheExceptionThatLetsTheOperatorInIsRefused(t *testing.T) {
	h := newGwHost(t)
	gwClock(t, time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC))
	sp := emptySpec()
	sp.NextID = 5
	sp.Blocklists = []BlocklistSpec{{ID: 1, Name: "cgnat", Kind: "manual", Entries: []string{"100.110.0.0/16"}, Enabled: true, Count: 1}}
	sp.Exceptions = []ExceptionSpec{{ID: 2, Address: "100.110.34.0/24", Scope: "blocklist:1", Reason: "office"}}
	h.seed(t, sp)
	err := h.DeleteException(context.Background(), 2, gwClient)
	var guard *GuardError
	if !errors.As(err, &guard) {
		t.Fatalf("err = %v", err)
	}
	// Trusted permanently, the exception is no longer the only way in.
	sp.Trusted = []string{gwClient + "/32"}
	h.seed(t, sp)
	if err := h.DeleteException(context.Background(), 2, gwClient); err != nil {
		t.Fatal(err)
	}
}

func TestTrustedNotesRecordWhoWhyUntilWhenAndStaleness(t *testing.T) {
	h := newGwHost(t)
	now := gwClock(t, time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC))
	sp := emptySpec()
	sp.NextID = 5
	h.seed(t, sp)
	if _, err := h.AddLimit(context.Background(), LimitRequest{Name: "ssh", Protocol: "tcp", Ports: "22", Rate: 6, Per: "minute"}, "198.51.100.40", "alice"); err != nil {
		t.Fatal(err)
	}
	got := h.spec(t)
	if len(got.Trusted) != 1 || len(got.TrustedNotes) != 1 || got.TrustedNotes[0].CreatedBy != "alice" || got.TrustedNotes[0].Reason == "" {
		t.Fatalf("note = %+v / %+v", got.Trusted, got.TrustedNotes)
	}
	// A second address kept, reviewed long ago, never seen since.
	sp = got
	sp.Trusted = append(sp.Trusted, "203.0.113.77/32")
	sp.TrustedNotes = append(sp.TrustedNotes, TrustedNote{Address: "203.0.113.77/32", Made: Made{CreatedAt: now.Add(-90 * 24 * time.Hour)}})
	h.seed(t, sp)
	view := h.trustedView(h.spec(t), netip.MustParseAddr("198.51.100.40"), map[netip.Addr]time.Time{netip.MustParseAddr("198.51.100.40"): now.Add(-time.Hour)})
	var you, old TrustedEntry
	for _, e := range view {
		switch e.Address {
		case "198.51.100.40/32":
			you = e
		case "203.0.113.77/32":
			old = e
		}
	}
	if you.Origin != "you" || you.Stale || you.LastSeen == nil || you.AddedBy != "alice" {
		t.Fatalf("you = %+v", you)
	}
	if !old.Stale || old.LastSeen != nil {
		t.Fatalf("an unreviewed, unseen address is stale: %+v", old)
	}
	if err := h.UpdateTrusted(context.Background(), TrustedRequest{Address: "203.0.113.77", Reason: "build server", Confirm: true}, "198.51.100.40", "bob"); err != nil {
		t.Fatal(err)
	}
	view = h.trustedView(h.spec(t), netip.Addr{}, nil)
	for _, e := range view {
		if e.Address == "203.0.113.77/32" && (e.Stale || e.ConfirmedBy != "bob" || e.Reason != "build server") {
			t.Fatalf("confirmed = %+v", e)
		}
	}
	// The operator's only coverage cannot be set to expire.
	err := h.UpdateTrusted(context.Background(), TrustedRequest{Address: "198.51.100.40", ExpiresAt: "2026-10-10T00:00:00Z"}, "198.51.100.40", "alice")
	var guard *GuardError
	if !errors.As(err, &guard) {
		t.Fatalf("own expiry: %v", err)
	}
	// Another address may expire: it becomes a rule with its end in it,
	// leaving the permanent set.
	if err := h.UpdateTrusted(context.Background(), TrustedRequest{Address: "203.0.113.77", ExpiresAt: "2026-10-10T00:00:00Z"}, "198.51.100.40", "bob"); err != nil {
		t.Fatal(err)
	}
	if trusted := h.trustedFor(h.spec(t)); containsPrefix(trusted, "203.0.113.77/32") {
		t.Fatal("an expiring address stayed in the permanent set")
	}
	if !strings.Contains(gwLastLoaded(t, h), `comment "trusted:203.0.113.77/32"`) {
		t.Fatal("the expiring address has no rule")
	}
	*now = time.Date(2026, 10, 10, 0, 0, 1, 0, time.UTC)
	if removed, err := h.sweepExpired(context.Background()); err != nil || !removed {
		t.Fatalf("sweep: %v %v", removed, err)
	}
	if sp := h.spec(t); len(sp.Trusted) != 1 || len(sp.TrustedNotes) != 1 {
		t.Fatalf("after sweep: %+v %+v", sp.Trusted, sp.TrustedNotes)
	}
}

func containsPrefix(ps []netip.Prefix, want string) bool {
	for _, p := range ps {
		if p.String() == want {
			return true
		}
	}
	return false
}

func TestOperatorActivityReadsSessionsAndSignIns(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := gwClock(t, time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC))
	if _, err := st.DB.Exec(`INSERT INTO users(id, username, password_hash, role, created_at) VALUES (1, 'alice', 'x', 'admin', 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO sessions(id, user_id, token_hash, ip, created_at, last_seen_at, expires_at) VALUES ('s', 1, 'h', '198.51.100.40', 0, ?, ?)`, now.Add(-2*time.Hour).Unix(), now.Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		ip      string
		at      time.Time
		success int
	}{{"203.0.113.77", now.Add(-24 * time.Hour), 1}, {"203.0.113.78", now.Add(-time.Hour), 0}, {"203.0.113.79", now.Add(-200 * 24 * time.Hour), 1}} {
		if _, err := st.DB.Exec(`INSERT INTO audit_log(ts, ip, action, success) VALUES (?, ?, 'auth.login', ?)`, row.at.Unix(), row.ip, row.success); err != nil {
			t.Fatal(err)
		}
	}
	s := New(Options{Paths: Paths{Dir: t.TempDir()}, DB: st.DB})
	got := s.operatorActivity(context.Background())
	if len(got) != 2 || !got[netip.MustParseAddr("198.51.100.40")].Equal(now.Add(-2*time.Hour)) || got[netip.MustParseAddr("203.0.113.77")].IsZero() {
		t.Fatalf("activity = %+v", got)
	}
}

func TestRefreshSchedulesBackOffAndNeverFetchManualOnes(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		bl   BlocklistSpec
		due  bool
		next time.Duration // from now; negative is none
	}{
		{"daily default, fresh", BlocklistSpec{Kind: "feed", Enabled: true, Refreshed: now.Add(-time.Hour)}, false, 23 * time.Hour},
		{"daily default, old", BlocklistSpec{Kind: "feed", Enabled: true, Refreshed: now.Add(-25 * time.Hour)}, true, -time.Hour},
		{"six hours", BlocklistSpec{Kind: "feed", Enabled: true, Refresh: "6h", Refreshed: now.Add(-7 * time.Hour)}, true, -time.Hour},
		{"manual", BlocklistSpec{Kind: "feed", Enabled: true, Refresh: "manual", Refreshed: now.Add(-700 * time.Hour)}, false, -1},
		{"switched off", BlocklistSpec{Kind: "country", Enabled: false, Refreshed: now.Add(-700 * time.Hour)}, false, -1},
		{"failing, backing off", BlocklistSpec{Kind: "feed", Enabled: true, Refreshed: now.Add(-48 * time.Hour), Failures: 3, LastAttempt: now.Add(-30 * time.Minute)}, false, 30 * time.Minute},
		{"failing, retry due", BlocklistSpec{Kind: "feed", Enabled: true, Refreshed: now.Add(-48 * time.Hour), Failures: 1, LastAttempt: now.Add(-20 * time.Minute)}, true, -5 * time.Minute},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := refreshDue(c.bl, now); got != c.due {
				t.Fatalf("due = %v", got)
			}
			next := nextRefresh(c.bl)
			if c.next == -1 {
				if next != nil {
					t.Fatalf("next = %v, want none", next)
				}
				return
			}
			if next == nil || !next.Equal(now.Add(c.next)) {
				t.Fatalf("next = %v, want %v", next, now.Add(c.next))
			}
		})
	}
	if refreshBackoff(BlocklistSpec{Refresh: "6h", Failures: 20}) != 6*time.Hour {
		t.Fatal("the backoff exceeds the schedule")
	}
}

// signedFeed serves a feed, its validators and a detached signature.
type signedFeed struct {
	*httptest.Server
	mu   sync.Mutex
	body string
	etag string
	sig  string
	hits map[string]int
}

func newSignedFeed(t *testing.T) *signedFeed {
	t.Helper()
	f := &signedFeed{hits: map[string]int{}}
	f.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.hits[r.URL.Path]++
		switch r.URL.Path {
		case "/list":
			if f.etag != "" && r.Header.Get("If-None-Match") == f.etag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", f.etag)
			fmt.Fprint(w, f.body)
		case "/list.sig":
			fmt.Fprint(w, f.sig)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.Close)
	trustServer(t, f.Server)
	return f
}

func TestFeedRefreshRecordsProvenanceDiffsAndHonoursValidators(t *testing.T) {
	h := newGwHost(t)
	feed := newSignedFeed(t)
	feed.body, feed.etag = "203.0.113.0/24\n198.51.100.0/25\n# note\nnot an address\n", `"v1"`
	v, err := h.AddBlocklist(context.Background(), BlocklistRequest{Name: "feed", Kind: "feed", URL: feed.URL + "/list", Refresh: "6h"}, gwClient, "ops")
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Sources) != 1 || v.Sources[0].SHA256 == "" || v.Sources[0].Networks != 2 || v.Sources[0].Skipped != 1 || v.Sources[0].ETag != `"v1"` {
		t.Fatalf("provenance = %+v", v.Sources)
	}
	if v.Refresh != "6h" || v.Integrity != "https" || v.LastDiff == nil || v.LastDiff.Added != 2 || v.LastDiff.Baseline {
		t.Fatalf("view = %+v", v)
	}
	loads := len(h.loaded)
	// Unchanged upstream: a 304 records the refresh and touches nothing else.
	if err := h.RefreshBlocklist(context.Background(), v.ID); err != nil {
		t.Fatal(err)
	}
	bl := h.spec(t).Blocklists[0]
	if len(h.loaded) != loads || bl.Sources[0].Status != "unchanged" || bl.LastDiff == nil || bl.LastDiff.Added != 0 || !bl.LastDiff.Baseline {
		t.Fatalf("after 304: loads %d→%d, %+v", loads, len(h.loaded), bl)
	}
	// A real change: the diff names what came and went.
	feed.mu.Lock()
	feed.body, feed.etag = "203.0.113.0/24\n192.0.2.0/24\n", `"v2"`
	feed.mu.Unlock()
	if err := h.RefreshBlocklist(context.Background(), v.ID); err != nil {
		t.Fatal(err)
	}
	bl = h.spec(t).Blocklists[0]
	if d := bl.LastDiff; d == nil || !d.Baseline || d.Added != 1 || d.Removed != 1 || d.AddedSample[0] != "192.0.2.0/24" || d.RemovedSample[0] != "198.51.100.0/25" {
		t.Fatalf("diff = %+v", bl.LastDiff)
	}
	if bl.Failures != 0 || bl.Sources[0].ETag != `"v2"` {
		t.Fatalf("after refresh = %+v", bl)
	}
	// A failure is counted for the backoff and leaves the data alone.
	feed.mu.Lock()
	feed.body, feed.etag = "", ""
	feed.mu.Unlock()
	if err := h.RefreshBlocklist(context.Background(), v.ID); err == nil {
		t.Fatal("an empty feed was accepted")
	}
	bl = h.spec(t).Blocklists[0]
	if bl.Failures != 1 || bl.Error == "" || bl.Count != 2 {
		t.Fatalf("after failure = %+v", bl)
	}
}

func TestASignedFeedIsUsedOnlyWhenItsSignatureVerifies(t *testing.T) {
	h := newGwHost(t)
	feed := newSignedFeed(t)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	feed.body = "203.0.113.0/24\n"
	feed.sig = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(feed.body)))
	key := base64.StdEncoding.EncodeToString(pub)
	v, err := h.AddBlocklist(context.Background(), BlocklistRequest{Name: "signed", Kind: "feed", URL: feed.URL + "/list", SignatureURL: feed.URL + "/list.sig", PublicKey: key}, gwClient, "ops")
	if err != nil {
		t.Fatal(err)
	}
	if v.Integrity != "signed" || !v.Sources[0].Signed {
		t.Fatalf("view = %+v", v)
	}
	// Tampered in transit or at the source: the signature no longer covers it.
	feed.mu.Lock()
	feed.body = "203.0.113.0/24\n198.51.100.0/24\n"
	feed.mu.Unlock()
	err = h.RefreshBlocklist(context.Background(), v.ID)
	if err == nil || !strings.Contains(err.Error(), "does not verify") {
		t.Fatalf("err = %v", err)
	}
	if bl := h.spec(t).Blocklists[0]; bl.Count != 1 {
		t.Fatalf("a tampered list was loaded: %+v", bl)
	}
	if _, err := h.AddBlocklist(context.Background(), BlocklistRequest{Name: "badkey", Kind: "feed", URL: feed.URL + "/list", SignatureURL: feed.URL + "/list.sig", PublicKey: "short"}, gwClient, "ops"); err == nil {
		t.Fatal("a key that is not Ed25519 was accepted")
	}
}

func TestCountryProvenanceRecordsAMissingIPv6Zone(t *testing.T) {
	srv := newFeedServer(t)
	srv.set("/v4/xx.zone", "203.0.113.0/24\n")
	r, err := fetchListWith(context.Background(), "country", []string{"xx"}, "", fetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.sources) != 2 || r.sources[0].Family != "ipv4" || r.sources[0].Status != "ok" || r.sources[1].Status != "absent" || r.sources[1].Family != "ipv6" {
		t.Fatalf("sources = %+v", r.sources)
	}
}

func TestBlocklistPreviewNamesLocalNetworksTrustedOverlapAndOpenConnections(t *testing.T) {
	h := newGwHost(t, "100.64.0.0/10")
	g := &gwConntrack{entries: []gwCT{
		{src: "198.51.100.7", dst: "203.0.113.20", sport: 40000, dport: 443, proto: unix.IPPROTO_TCP, tcpState: 3},
		{src: "198.51.100.8", dst: "203.0.113.20", sport: 40001, dport: 443, proto: unix.IPPROTO_TCP, tcpState: 3},
		{src: "192.0.2.1", dst: "203.0.113.20", sport: 40002, dport: 443, proto: unix.IPPROTO_TCP, tcpState: 3},
	}}
	g.install(t)
	sp := emptySpec()
	sp.NextID = 2
	sp.NAT = []NATSpec{{ID: 1, Name: "lab", Source: "10.9.0.0/24", Interface: "eth0", Enabled: true}}
	h.seed(t, sp)
	p, err := h.PreviewBlocklist(context.Background(), BlocklistPreviewRequest{List: BlocklistRequest{Kind: "manual", Entries: []string{"198.51.100.0/24", "172.17.0.0/16", "10.9.0.0/16", "100.64.0.0/12"}}}, "203.0.113.200")
	if err != nil {
		t.Fatal(err)
	}
	if !p.Valid || p.Networks != 4 || p.Connections == nil || p.Connections.Tracked != 2 {
		t.Fatalf("preview = %+v %+v", p, p.Connections)
	}
	whats := map[string]bool{}
	for _, l := range p.LocalOverlap {
		whats[l.Network] = true
	}
	if !whats["172.17.0.0/16"] || !whats["10.9.0.0/24"] {
		t.Fatalf("local overlap = %+v", p.LocalOverlap)
	}
	if len(p.TrustedOverlap) == 0 || !strings.Contains(p.TrustedOverlap[0], "100.64.0.0/10") {
		t.Fatalf("trusted overlap = %+v", p.TrustedOverlap)
	}
	p, _ = h.PreviewBlocklist(context.Background(), BlocklistPreviewRequest{List: BlocklistRequest{Kind: "manual", Entries: []string{"203.0.113.0/24"}}}, "203.0.113.200")
	if p.Refused == "" || p.Valid {
		t.Fatalf("a list holding the reader is refused: %+v", p)
	}
	if h.spec(t).Blocklists != nil {
		t.Fatal("a preview saved a list")
	}
}

func TestSessionRevocationEndsOnlyEntriesFromABlockedNetwork(t *testing.T) {
	h := newGwHost(t)
	g := &gwConntrack{entries: []gwCT{
		{src: "198.51.100.7", dst: "203.0.113.20", sport: 40000, dport: 443, proto: unix.IPPROTO_TCP, tcpState: 3, id: 1},
		{src: "198.51.100.9", dst: "203.0.113.20", sport: 40001, dport: 22, proto: unix.IPPROTO_TCP, tcpState: 3, id: 2},
		{src: "192.0.2.1", dst: "203.0.113.20", sport: 40002, dport: 443, proto: unix.IPPROTO_TCP, tcpState: 3, id: 3},
	}}
	g.install(t)
	sp := emptySpec()
	sp.NextID = 3
	sp.Blocklists = []BlocklistSpec{{ID: 1, Name: "scanners", Kind: "manual", Entries: []string{"198.51.100.0/24"}, Enabled: true, Count: 1},
		{ID: 2, Name: "off", Kind: "manual", Entries: []string{"192.0.2.0/24"}, Count: 1}}
	h.seed(t, sp)
	h.first("nft -j list set inet jd_gateway bl_1_4", `{"nftables":[{"set":{"name":"bl_1_4","elem":[{"prefix":{"addr":"198.51.100.0","len":24}}]}}]}`, nil)
	h.first("nft -j list set inet jd_gateway bl_1_6", `{"nftables":[{"set":{"name":"bl_1_6","elem":[]}}]}`, nil)

	p, err := h.PreviewSessions(context.Background(), SessionRequest{Network: "198.51.100.0/24", BlocklistID: 1}, gwClient)
	if err != nil || p.Connections.Tracked != 2 || p.Refused != "" {
		t.Fatalf("preview = %+v %v", p, err)
	}
	before := len(g.requests)
	res, err := h.RevokeSessions(context.Background(), SessionRequest{Network: "198.51.100.0/24", BlocklistID: 1}, gwClient)
	if err != nil || res.Matched != 2 || res.Ended != 2 || res.Failed != 0 {
		t.Fatalf("result = %+v %v", res, err)
	}
	deletes := 0
	for _, r := range g.requests[before:] {
		if r[4]&0xff == ipctnlMsgCTDelete {
			deletes++
		}
	}
	if deletes != 2 {
		t.Fatalf("deletes = %d", deletes)
	}
	for _, c := range []struct {
		name string
		req  SessionRequest
		want string
	}{
		{"not inside the list", SessionRequest{Network: "203.0.113.0/24", BlocklistID: 1}, "not inside"},
		{"a list that is off", SessionRequest{Network: "192.0.2.0/24", BlocklistID: 2}, "switched off"},
	} {
		if _, err := h.RevokeSessions(context.Background(), c.req, gwClient); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	_, err = h.RevokeSessions(context.Background(), SessionRequest{Network: "198.51.100.0/24", BlocklistID: 1}, "198.51.100.5")
	var guard *GuardError
	if !errors.As(err, &guard) {
		t.Fatalf("the reader's own network: %v", err)
	}
	h.first("nft -j list set inet jd_gateway bl_1_4", "", errors.New("Error: No such file or directory"))
	if _, err := h.RevokeSessions(context.Background(), SessionRequest{Network: "198.51.100.0/24", BlocklistID: 1}, gwClient); err == nil || !strings.Contains(err.Error(), "not loaded") {
		t.Fatalf("an unloaded list: %v", err)
	}
}

func TestPressureBreaksTheTableDownAndNamesIndications(t *testing.T) {
	h := newGwHost(t)
	var entries []gwCT
	for i := 0; i < 120; i++ {
		entries = append(entries, gwCT{src: "198.51.100.7", dst: "203.0.113.20", sport: uint16(30000 + i), dport: 443, proto: unix.IPPROTO_TCP, tcpState: 2})
	}
	for i := 0; i < 40; i++ {
		entries = append(entries, gwCT{src: fmt.Sprintf("192.0.2.%d", i+1), dst: "203.0.113.20", sport: uint16(40000 + i), dport: 22, proto: unix.IPPROTO_TCP, tcpState: 3, status: ctStatusSeenReply | ctStatusAssured})
	}
	g := &gwConntrack{entries: entries}
	g.install(t)
	sp := emptySpec()
	sp.NextID = 2
	sp.Limits = []LimitSpec{{ID: 1, Name: "web", Protocol: "tcp", Ports: "443", MaxConnections: 50, GlobalConnections: 500, PerSource: true, Action: "drop", Enabled: true}}
	h.seed(t, sp)
	v, err := h.Pressure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v.Breakdown.Read != 160 || v.Breakdown.Unreplied != 120 || v.Breakdown.TopSources[0].Key != "198.51.100.7" || v.Breakdown.TopSources[0].Count != 120 {
		t.Fatalf("breakdown = %+v", v.Breakdown)
	}
	kinds := map[string]bool{}
	for _, c := range v.Causes {
		kinds[c.Kind] = true
	}
	for _, want := range []string{"syn", "unreplied", "source", "port"} {
		if !kinds[want] {
			t.Errorf("missing cause %s in %+v", want, v.Causes)
		}
	}
	if len(v.Limits) != 1 || v.Limits[0].Open != 120 || v.Limits[0].TopSources[0].Count != 120 {
		t.Fatalf("capacity = %+v", v.Limits)
	}
	if v.Stats == nil || v.Stats.Drop != 2 {
		t.Fatalf("stats = %+v", v.Stats)
	}
}

func TestPerInterfaceEffectiveValuesFollowTheKernelsCombination(t *testing.T) {
	h := newGwHost(t)
	set := func(family, dev, leaf, v string) { h.writeSys(filepath.Join("net", family, "conf", dev, leaf), v) }
	for _, dev := range []string{"all", "eth0", "wg0", "br-lab"} {
		for _, leaf := range []string{"rp_filter", "accept_redirects", "send_redirects", "accept_source_route", "log_martians", "forwarding"} {
			set("ipv4", dev, leaf, "0")
		}
		set("ipv6", dev, "accept_redirects", "0")
		set("ipv6", dev, "accept_source_route", "0")
	}
	set("ipv4", "all", "rp_filter", "2")
	set("ipv4", "wg0", "rp_filter", "1")
	set("ipv4", "all", "accept_redirects", "1")
	set("ipv4", "br-lab", "accept_redirects", "1")
	set("ipv4", "br-lab", "forwarding", "1")
	set("ipv4", "eth0", "send_redirects", "1")
	set("ipv6", "eth0", "accept_redirects", "1")
	r := readInterfaceSettings()
	values := map[string]map[string]InterfaceValue{}
	for _, dev := range r.Interfaces {
		values[dev.Name] = map[string]InterfaceValue{}
		for _, v := range dev.Values {
			values[dev.Name][v.Key] = v
		}
	}
	if v := values["wg0"]["net.ipv4.conf.all.rp_filter"]; v.Effective != "2" || v.Differs {
		t.Fatalf("max(2,1) = %+v", v)
	}
	set("ipv4", "all", "rp_filter", "0")
	r = readInterfaceSettings()
	for _, dev := range r.Interfaces {
		for _, v := range dev.Values {
			if dev.Name == "wg0" && v.Key == "net.ipv4.conf.all.rp_filter" && (v.Effective != "1" || !v.Differs) {
				t.Fatalf("a strict tunnel stays strict under loose all: %+v", v)
			}
		}
	}
	if v := values["eth0"]["net.ipv4.conf.all.accept_redirects"]; v.Effective != "1" {
		t.Fatalf("not forwarding: either accepts = %+v", v)
	}
	if v := values["br-lab"]["net.ipv4.conf.all.accept_redirects"]; v.Effective != "1" {
		t.Fatalf("forwarding: both accept = %+v", v)
	}
	if v := values["eth0"]["net.ipv4.conf.all.send_redirects"]; v.Effective != "1" || !v.Differs {
		t.Fatalf("either sends = %+v", v)
	}
	if v := values["eth0"]["net.ipv6.conf.all.accept_redirects"]; v.Effective != "1" || !v.Differs {
		t.Fatalf("IPv6 reads the device's own value = %+v", v)
	}
	for _, p := range kernelProfiles() {
		for key, value := range p.Values {
			d, ok := protectionDefFor(key)
			if !ok {
				t.Fatalf("profile %s names %s", p.ID, key)
			}
			if _, err := d.check(value); err != nil {
				t.Errorf("profile %s: %v", p.ID, err)
			}
		}
	}
}

func TestEditingASignedFeedKeepsItsPinnedKeyAndToggleKeepsTheSignature(t *testing.T) {
	h := newGwHost(t)
	feed := newSignedFeed(t)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	feed.body = "203.0.113.0/24\n"
	feed.sig = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(feed.body)))
	v, err := h.AddBlocklist(context.Background(), BlocklistRequest{Name: "signed", Kind: "feed", URL: feed.URL + "/list", SignatureURL: feed.URL + "/list.sig", PublicKey: base64.StdEncoding.EncodeToString(pub)}, gwClient, "ops")
	if err != nil {
		t.Fatal(err)
	}
	off := false
	if _, err := h.UpdateBlocklist(context.Background(), v.ID, BlocklistRequest{Name: "renamed", URL: feed.URL + "/list", SignatureURL: feed.URL + "/list.sig", Enabled: &off}, gwClient, "ops"); err != nil {
		t.Fatal(err)
	}
	bl := h.spec(t).Blocklists[0]
	if bl.Name != "renamed" || bl.SignatureURL == "" || bl.PublicKey != base64.StdEncoding.EncodeToString(pub) || bl.Enabled {
		t.Fatalf("after edit = %+v", bl)
	}
	if hits := feed.hits["/list"]; hits != 1 {
		t.Fatalf("an edit that changes neither the address nor the signature refetched: %d", hits)
	}
}

func TestDeletingAListTakesItsOwnExceptionsWithIt(t *testing.T) {
	h := newGwHost(t)
	gwClock(t, time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC))
	sp := emptySpec()
	sp.NextID = 9
	sp.Blocklists = []BlocklistSpec{
		{ID: 1, Name: "a", Kind: "manual", Entries: []string{"198.51.100.0/24"}, Enabled: true, Count: 1},
		{ID: 2, Name: "b", Kind: "manual", Entries: []string{"192.0.2.0/24"}, Enabled: true, Count: 1},
	}
	sp.Exceptions = []ExceptionSpec{
		{ID: 3, Address: "198.51.100.7/32", Scope: "blocklist:1", Reason: "x"},
		{ID: 4, Address: "192.0.2.7/32", Scope: "blocklist:2", Reason: "y"},
		{ID: 5, Address: "203.0.113.7/32", Scope: "all", Reason: "z"},
	}
	h.seed(t, sp)
	if err := h.DeleteBlocklist(context.Background(), 1, gwClient); err != nil {
		t.Fatal(err)
	}
	got := h.spec(t).Exceptions
	if len(got) != 2 || got[0].ID != 4 || got[1].ID != 5 {
		t.Fatalf("exceptions = %+v", got)
	}
}
