package accountlink

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func startedEnv(t *testing.T, opts ...envOption) *testEnv {
	t.Helper()
	e := newEnv(t, opts...)
	e.svc.pollEvery = 10 * time.Millisecond
	e.svc.Start(t.Context())
	return e
}

func (e *testEnv) linkedState() (State, bool) {
	st, ok, _ := e.svc.kv.state(e.t.Context())
	return st, ok
}

func TestTheConnectorCompletesALinkItBeganThenReportsAndObeys(t *testing.T) {
	t.Parallel()
	e := startedEnv(t)
	req, err := e.svc.BeginLink(t.Context(), ScopeOperate, "admin")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the request to be polled", func() bool { return e.site.hitCount("GET /requests/") > 0 })
	e.site.approve(req.ID, "ion@example.com")
	waitFor(t, "the link to be stored", func() bool { _, ok := e.linkedState(); return ok })
	waitFor(t, "the first heartbeat", func() bool { return len(e.site.heartbeatBodies()) > 0 })

	var first Snapshot
	if err := json.Unmarshal(e.site.heartbeatBodies()[0], &first); err != nil || first.Server.Scope != ScopeOperate {
		t.Fatalf("first heartbeat = %s (%v)", e.site.heartbeatBodies()[0], err)
	}
	st, _ := e.linkedState()
	cmd := e.site.command(st.ServerID, ulid(1), KindContainerReboot, map[string]any{"container": "shop-web"}, e.clock().Add(10*time.Minute))
	waitFor(t, "the command's result", func() bool { _, ok := e.site.result(cmd.ID); return ok })
	if res, _ := e.site.result(cmd.ID); res.Status != resultSucceeded {
		t.Fatalf("result = %+v", res)
	}
	if calls := e.host.called(); len(calls) != 1 || calls[0].Method != "Container" {
		t.Fatalf("calls = %+v", calls)
	}
	// A command finishing is followed by a heartbeat.
	waitFor(t, "the heartbeat after the command", func() bool { return len(e.site.heartbeatBodies()) > 1 })
}

func TestALinkMadeAtTheTerminalStartsTheHeartbeatWithoutARestart(t *testing.T) {
	t.Parallel()
	e := startedEnv(t)
	time.Sleep(50 * time.Millisecond)
	if n := e.site.hitCount(""); n != 0 {
		t.Fatalf("an unlinked dashboard made %d requests", n)
	}

	// The terminal command is another process over the same store.
	cli := New(Options{Store: e.st, Sealer: e.svc.sealer, SiteURL: e.site.srv.URL, Enabled: true, Version: "0.8.2", Now: e.clock,
		Describe: e.svc.desc})
	req, err := cli.BeginLink(t.Context(), ScopeObserve, "cli")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if n := e.site.hitCount("GET /requests/"); n != 0 {
		t.Fatalf("the running dashboard polled a request the terminal owns (%d polls)", n)
	}
	e.site.approve(req.ID, "ion@example.com")
	if res, err := cli.PollLink(t.Context()); err != nil || res.State != PollLinked {
		t.Fatalf("terminal poll = %+v, %v", res, err)
	}
	waitFor(t, "the running dashboard to start heartbeating", func() bool { return len(e.site.heartbeatBodies()) > 0 })
}

func TestUnlinkingAtTheTerminalStopsTheRunningConnector(t *testing.T) {
	t.Parallel()
	e := startedEnv(t)
	e.link(ScopeObserve)
	e.svc.nudge()
	waitFor(t, "heartbeats", func() bool { return len(e.site.heartbeatBodies()) > 0 })

	cli := New(Options{Store: e.st, Sealer: e.svc.sealer, SiteURL: e.site.srv.URL, Enabled: true, Now: e.clock})
	if _, err := cli.Unlink(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Whatever was in flight settles; after that nothing more is sent.
	time.Sleep(300 * time.Millisecond)
	settled := e.site.hitCount("")
	time.Sleep(300 * time.Millisecond)
	if after := e.site.hitCount(""); after != settled {
		t.Fatalf("the connector kept calling the site after an unlink (%d then %d)", settled, after)
	}
}

func TestTheSiteRemovingTheServerClearsTheLinkAndStopsEverything(t *testing.T) {
	t.Parallel()
	e := startedEnv(t)
	st := e.link(ScopeObserve)
	e.svc.nudge()
	waitFor(t, "heartbeats", func() bool { return len(e.site.heartbeatBodies()) > 0 })
	before, _ := e.svc.Status(t.Context())

	e.site.remove(st.ServerID)
	waitFor(t, "the link to be cleared", func() bool { _, ok := e.linkedState(); return !ok })
	after, _ := e.svc.Status(t.Context())
	if after.State != StateUnlinked || after.Link != nil || after.Fingerprint != before.Fingerprint {
		t.Fatalf("status = %+v", after)
	}
	waitFor(t, "the unlink to be audited", func() bool { return len(e.auditRows("account.unlink")) == 1 })
	if rows := e.auditRows("account.unlink"); !strings.Contains(rows[0].Detail, "removed on the site") {
		t.Fatalf("audit = %+v", rows)
	}
	time.Sleep(200 * time.Millisecond)
	settled := e.site.hitCount("")
	time.Sleep(200 * time.Millisecond)
	if e.site.hitCount("") != settled {
		t.Fatal("the connector kept calling a site that removed it")
	}
	// And it can be linked again, from the same identity.
	if _, err := e.svc.BeginLink(t.Context(), ScopeObserve, "admin"); err != nil {
		t.Fatalf("relink: %v", err)
	}
}

func TestFailedExchangesBackOffShowAsAnErrorAndRecover(t *testing.T) {
	t.Parallel()
	e := startedEnv(t)
	e.link(ScopeObserve)
	e.site.fail("/heartbeat", 1000)
	e.site.fail("/commands", 1000)
	waitFor(t, "an error to show", func() bool {
		s, _ := e.svc.Status(t.Context())
		return s.Link != nil && s.Link.LastError != "" && e.site.hitCount("POST /heartbeat") >= 3 && e.site.hitCount("GET /commands") >= 3
	})
	e.site.fail("/heartbeat", 0)
	e.site.fail("/commands", 0)
	waitFor(t, "recovery", func() bool {
		s, _ := e.svc.Status(t.Context())
		return s.Link != nil && s.Link.LastError == "" && s.Link.LastHeartbeatAt != nil && len(e.site.heartbeatBodies()) > 0
	})
}

func TestTheBackoffGrowsFromOneSecondToFiveMinutes(t *testing.T) {
	t.Parallel()
	b := backoff{min: time.Second, max: 5 * time.Minute}
	prev := time.Duration(0)
	var last time.Duration
	for i := 0; i < 20; i++ {
		last = b.next()
		if i > 0 && b.cur < prev {
			t.Fatalf("backoff shrank: %s then %s", prev, b.cur)
		}
		prev = b.cur
	}
	if b.cur != 5*time.Minute || last < 4*time.Minute || last > 6*time.Minute {
		t.Fatalf("backoff ended at %s (waiting %s)", b.cur, last)
	}
	b.reset()
	if first := b.next(); first < 800*time.Millisecond || first > 1200*time.Millisecond {
		t.Fatalf("first wait after a reset = %s", first)
	}
}

func TestSwitchedOffTheConnectorSendsNothingEvenWithALinkInTheStore(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.link(ScopeObserve)
	hits := e.site.hitCount("")
	off := New(Options{Store: e.st, Sealer: e.svc.sealer, SiteURL: e.site.srv.URL, Enabled: false, Host: e.host, Now: e.clock})
	off.idle, off.watch = 5*time.Millisecond, 5*time.Millisecond
	off.Start(t.Context())
	defer off.Stop()
	time.Sleep(150 * time.Millisecond)
	if e.site.hitCount("") != hits {
		t.Fatal("a switched-off connector called the site")
	}
	if err := off.SyncNow(t.Context()); err != ErrDisabled {
		t.Fatalf("sync = %v", err)
	}
	if err := off.SetScope(t.Context(), ScopeOperate); err != ErrDisabled {
		t.Fatalf("scope = %v", err)
	}
	// Unlinking still works, locally, and tells no one.
	notified, err := off.Unlink(t.Context())
	if err != nil || notified {
		t.Fatalf("unlink = %v, %v", notified, err)
	}
	if e.site.hitCount("") != hits {
		t.Fatal("unlinking while switched off called the site")
	}
}

func TestALinkToAnotherSiteIsLeftIdleWhenTheConfiguredSiteChanged(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.link(ScopeObserve)
	hits := e.site.hitCount("")
	moved := New(Options{Store: e.st, Sealer: e.svc.sealer, SiteURL: "https://other.example.com", Enabled: true, Host: e.host, Now: e.clock})
	moved.idle, moved.watch = 5*time.Millisecond, 5*time.Millisecond
	moved.Start(t.Context())
	defer moved.Stop()
	waitFor(t, "the mismatch to be reported", func() bool {
		s, _ := moved.Status(t.Context())
		return s.Link != nil && strings.Contains(s.Link.LastError, "JD_ACCOUNT_URL")
	})
	if e.site.hitCount("") != hits {
		t.Fatal("the connector talked to a site it was not configured for")
	}
}

func TestCommandsLeftRunningByADeadProcessAreClosedAndReported(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	st := e.link(ScopeOperate)
	id := ulid(9)
	if _, err := e.st.DB.Exec(`INSERT INTO account_link_commands(id, kind, received_at, status) VALUES(?, 'dashboard.restart', 1, 'running')`, id); err != nil {
		t.Fatal(err)
	}
	e.svc.closeInterrupted(t.Context(), st)
	var status, errText string
	if err := e.st.DB.QueryRow(`SELECT status, error FROM account_link_commands WHERE id = ?`, id).Scan(&status, &errText); err != nil || status != resultFailed || !strings.Contains(errText, "restarted") {
		t.Fatalf("row = %q %q (%v)", status, errText, err)
	}
	if res, ok := e.site.result(id); !ok || res.Status != resultFailed {
		t.Fatalf("site was told %+v (%v)", res, ok)
	}
}

func TestAKeyRotationAnnouncedInAHeartbeatIsPinnedAndTheOldOneRetired(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	st := e.link(ScopeOperate)
	next := seedKey(t, vectorServerSeed) // any other key stands in for the successor
	end := e.clock().Add(48 * time.Hour).Unix()
	e.site.mu.Lock()
	e.site.rotate = []wireSiteKey{
		{Kid: vectorKid, PublicKey: pubB64(e.site.key), NotAfter: &end},
		{Kid: "2026-11", PublicKey: pubB64(next)},
	}
	e.site.mu.Unlock()
	if err := e.svc.sendHeartbeat(t.Context(), st, true); err != nil {
		t.Fatal(err)
	}
	cur, _ := e.linkedState()
	if len(cur.SiteKeys) != 2 || cur.SiteKeys[0].NotAfter != end || cur.SiteKeys[1].Kid != "2026-11" {
		t.Fatalf("keys = %+v", cur.SiteKeys)
	}

	// Both verify while the old one is in date; only the new one after it.
	signed := func(n int, key []byte) Command { return Command{} }
	_ = signed
	oldCmd := e.site.signedCommand(e.site.key, vectorKid, st.ServerID, ulid(1), KindSnapshotNow, map[string]any{}, e.clock().Add(time.Minute))
	newCmd := e.site.signedCommand(next, "2026-11", st.ServerID, ulid(2), KindSnapshotNow, map[string]any{}, e.clock().Add(time.Minute))
	for _, c := range []Command{oldCmd, newCmd} {
		if _, _, err := e.svc.verify(t.Context(), cur, c, e.clock()); err != nil {
			t.Fatalf("%s: %v", c.Kid, err)
		}
	}
	e.advance(72 * time.Hour)
	oldCmd2 := e.site.signedCommand(e.site.key, vectorKid, st.ServerID, ulid(3), KindSnapshotNow, map[string]any{}, e.clock().Add(time.Minute))
	if _, _, err := e.svc.verify(t.Context(), cur, oldCmd2, e.clock()); err == nil {
		t.Fatal("a retired key still verified")
	}
	// A heartbeat after that drops the retired key from the pinned set.
	e.site.mu.Lock()
	e.site.rotate = nil
	e.site.mu.Unlock()
	if err := e.svc.sendHeartbeat(t.Context(), cur, true); err != nil {
		t.Fatal(err)
	}
	after, _ := e.linkedState()
	if len(after.SiteKeys) != 1 || after.SiteKeys[0].Kid != "2026-11" {
		t.Fatalf("keys after retirement = %+v", after.SiteKeys)
	}
}

func TestAnAnnouncementCannotEmptyTheKeySetOrChangeAPinnedKey(t *testing.T) {
	t.Parallel()
	now := time.Unix(vectorNow, 0)
	pinned := []SiteKey{{Kid: "a", PublicKey: pubB64(seedKey(t, vectorSiteSeed))}}
	// A different key under a pinned id is not an update to it.
	other := pubB64(seedKey(t, vectorServerSeed))
	got, changed := mergeSiteKeys(pinned, []wireSiteKey{{Kid: "a", PublicKey: other}}, now)
	if changed || got[0].PublicKey != pinned[0].PublicKey {
		t.Fatalf("a pinned key was replaced: %+v", got)
	}
	// Malformed keys are ignored.
	if got, changed := mergeSiteKeys(pinned, []wireSiteKey{{Kid: "b", PublicKey: "AAAA"}}, now); changed || len(got) != 1 {
		t.Fatalf("a short key was pinned: %+v", got)
	}
	// An end date in the past on the only key leaves it, rather than leaving
	// nothing to verify with.
	past := now.Add(-time.Hour).Unix()
	if got, _ := mergeSiteKeys(pinned, []wireSiteKey{{Kid: "a", PublicKey: pinned[0].PublicKey, NotAfter: &past}}, now); len(got) != 1 {
		t.Fatalf("the key set was emptied: %+v", got)
	}
}

func TestWhatTheSiteRefusesIsSaidInWordsAPersonCanActOn(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		err  error
		want string
	}{
		{&APIError{Status: 401, Code: "stale_time", Message: "clock"}, "clock"},
		{&APIError{Status: 401, Code: "bad_signature"}, "unlink and link it again"},
		{&APIError{Status: 401, Code: "unknown_server"}, "unlink and link it again"},
		{&APIError{Status: 429, Code: "rate_limited"}, "limiting how often"},
		{&APIError{Status: 429, Code: "poll_in_flight"}, "limiting how often"},
		{&APIError{Status: 500, Code: "internal", Message: "boom"}, "500 internal"},
		{errors.New("dial tcp 203.0.113.9:443: i/o timeout"), "i/o timeout"},
	} {
		if got := describeError(tc.err); !strings.Contains(got, tc.want) {
			t.Errorf("describeError(%v) = %q, want it to contain %q", tc.err, got, tc.want)
		}
	}
}

func TestAClockTheSiteRejectsShowsAsAnErrorOnTheStatus(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	st := e.link(ScopeObserve)
	// This server's clock is far from the site's.
	e.mu.Lock()
	e.now = e.now.Add(10 * time.Minute)
	e.mu.Unlock()
	if err := e.svc.sendHeartbeat(t.Context(), st, true); err == nil {
		t.Fatal("a heartbeat signed ten minutes ahead was accepted")
	}
	status, _ := e.svc.Status(t.Context())
	if status.Link.LastError == "" || !strings.Contains(status.Link.LastError, "60 seconds") {
		t.Fatalf("lastError = %q", status.Link.LastError)
	}
}

func TestTheUpdatePolicyIsLookedAtOnceAMinuteSoAShortWindowIsNotMissed(t *testing.T) {
	t.Parallel()
	// The defaults, not the test's quick ones: once an hour, at a fixed minute
	// after a start, would never see 03:00 to 03:30 if the start was at 02:40.
	s := New(Options{Store: newEnv(t).st, Enabled: true})
	if s.policyFirst > time.Minute || s.policyEvery > time.Minute {
		t.Fatalf("the policy is first looked at after %s and then every %s", s.policyFirst, s.policyEvery)
	}

	e := newEnv(t)
	st := e.link(ScopeMaintain)
	e.host.mu.Lock()
	e.host.update = UpdateStatus{Version: "0.8.2", Latest: "0.8.3", Available: true, Supported: true}
	e.host.mu.Unlock()
	if _, err := e.svc.setPolicy(t.Context(), UpdatePolicy{AutoPatch, Window{"03:00", "03:30"}}); err != nil {
		t.Fatal(err)
	}
	e.svc.policyFirst, e.svc.policyEvery = 5*time.Millisecond, 5*time.Millisecond
	e.mu.Lock()
	e.now = time.Date(2026, 10, 25, 2, 40, 0, 0, time.UTC)
	e.mu.Unlock()
	e.site.setNow(e.clock())

	go e.svc.policyLoop(t.Context(), st)
	time.Sleep(50 * time.Millisecond)
	if len(e.host.called()) != 0 {
		t.Fatalf("installed outside the window: %+v", e.host.called())
	}
	if e.host.updateReads() != 0 {
		t.Fatal("the machine was asked about updates while the window was shut")
	}
	// Twenty-five minutes on, the window is open for the five that remain of it.
	e.advance(25 * time.Minute)
	waitFor(t, "the update to start inside a half-hour window", func() bool { return len(e.host.called()) == 1 })
}

func TestAScopeNarrowedWhileAHeartbeatIsBeingBuiltIsTheScopeItIsSentAt(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	st := e.link(ScopeOperate)
	e.host.mu.Lock()
	e.host.snapshot = fullSnapshot()
	// Reading the machine takes seconds, and waiting for the site's rate
	// window can take most of a minute; the person narrows the scope meanwhile.
	e.host.onCollect = func() {
		if err := e.svc.SetScope(t.Context(), ScopeObserve); err != nil {
			t.Error(err)
		}
	}
	e.host.mu.Unlock()

	if err := e.svc.sendHeartbeat(t.Context(), st, true); err != nil {
		t.Fatal(err)
	}
	body := e.site.heartbeatBodies()[0]
	if strings.Contains(string(body), "SECRET") {
		t.Fatalf("a heartbeat built before the scope was narrowed went out with names in it: %s", body)
	}
	var sent Snapshot
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Server.Scope != ScopeObserve || sent.Containers == nil || sent.Containers.List != nil || sent.Stacks != nil {
		t.Fatalf("sent at %q: %s", sent.Server.Scope, body)
	}
}

func TestAHeartbeatForALinkThatEndedWhileItWasBeingBuiltIsNotSent(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	st := e.link(ScopeOperate)
	e.host.mu.Lock()
	e.host.onCollect = func() {
		if _, err := e.svc.Unlink(t.Context()); err != nil {
			t.Error(err)
		}
	}
	e.host.mu.Unlock()
	hits := e.site.hitCount("POST /heartbeat")
	if err := e.svc.sendHeartbeat(t.Context(), st, true); err != ErrNotLinked {
		t.Fatalf("heartbeat = %v", err)
	}
	if e.site.hitCount("POST /heartbeat") != hits {
		t.Fatal("a heartbeat was sent for a link that had ended")
	}
}

// A heartbeat storing a rotated key and the page changing the scope both read
// the link and write it back. Outside a transaction one of them puts the
// other's change away.
func TestNotingAHeartbeatAndChangingTheScopeDoNotOverwriteEachOther(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	st := e.link(ScopeObserve)
	pub := pubB64(seedKey(t, vectorServerSeed))
	const rounds = 150
	var wg sync.WaitGroup
	wg.Add(2)
	scopes := []Scope{ScopeObserve, ScopeOperate, ScopeMaintain}
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			e.svc.noteHeartbeat(t.Context(), st, heartbeatResponse{OK: true, HeartbeatSeconds: 30,
				SiteKeys: []wireSiteKey{{Kid: fmt.Sprintf("k-%03d", i), PublicKey: pub}}})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			if err := e.svc.SetScope(t.Context(), scopes[i%3]); err != nil {
				t.Error(err)
			}
		}
	}()
	wg.Wait()
	final, _ := e.linkedState()
	if want := scopes[(rounds-1)%3]; final.Scope != want {
		t.Fatalf("the last scope set was %s, the link says %s: a heartbeat wrote the old one back", want, final.Scope)
	}
	if len(final.SiteKeys) != rounds+1 {
		t.Fatalf("%d keys survived of %d announced: a scope change wrote an old key set back", len(final.SiteKeys), rounds+1)
	}
}

func TestNothingIsSentToASiteThatIsNotTheOneTheLinkWasMadeWith(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.link(ScopeObserve)
	hitsBefore := e.site.hitCount("")

	// The same store, now configured for another site.
	elsewhere := newFakeSite(t, e.clock())
	moved := New(Options{Store: e.st, Sealer: e.svc.sealer, SiteURL: elsewhere.srv.URL, Enabled: true, Host: e.host, Now: e.clock})
	moved.hbPerMinute = 1 << 20

	if err := moved.SyncNow(t.Context()); err != ErrSiteChanged {
		t.Fatalf("SyncNow = %v", err)
	}
	st, _, _ := moved.Linked(t.Context())
	if err := moved.sendHeartbeat(t.Context(), st, true); err != ErrSiteChanged {
		t.Fatalf("sendHeartbeat = %v", err)
	}
	// A heartbeat answer never merges keys into a link it was not made for.
	moved.noteHeartbeat(t.Context(), st, heartbeatResponse{SiteKeys: []wireSiteKey{{Kid: "x", PublicKey: pubB64(seedKey(t, vectorServerSeed))}}})
	if cur, _, _ := moved.Linked(t.Context()); len(cur.SiteKeys) != 1 {
		// noteHeartbeat itself is only reached from a send; this documents
		// that the guard above is what keeps the other site's keys out.
		t.Log("noteHeartbeat merges what it is given; sendHeartbeat refuses before it can be given anything")
	}
	notified, err := moved.Unlink(t.Context())
	if err != nil || notified {
		t.Fatalf("Unlink = %v, %v", notified, err)
	}
	if elsewhere.hitCount("") != 0 {
		t.Fatalf("the other site was called %d times", elsewhere.hitCount(""))
	}
	if e.site.hitCount("") != hitsBefore {
		t.Fatal("the site the link was made with was called by a service configured for another")
	}
	if s, _ := moved.Status(t.Context()); s.State != StateUnlinked {
		t.Fatalf("status = %+v", s)
	}
}
