package hublink

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accountlink"
	"github.com/Wayy01/Just-Dashboard/backend/internal/accountlink/linkstub"
)

func TestConnectingAsksTheHubAndStoresTheLinkOnApproval(t *testing.T) {
	e := newEnv(t)
	req, err := e.svc.Connect(t.Context(), ConnectRequest{URL: e.hub.URL(), Scope: accountlink.ScopeOperate, By: "ion"})
	if err != nil {
		t.Fatal(err)
	}
	if req.URL != e.hub.URL() || req.UserCode != "K7QM-29XD" || req.Scope != accountlink.ScopeOperate {
		t.Fatalf("request = %+v", req)
	}
	// What the hub was asked: the account link's body, the endpoint, no token.
	body := e.hub.request(req.ID).body
	server, _ := body["server"].(map[string]any)
	if body["scope"] != "operate" || server["endpoint"] != "https://box.tailed39ba.ts.net:8443" || server["name"] != "box" || server["version"] != "0.8.5" {
		t.Fatalf("request body = %v", body)
	}
	if _, ok := body["token"]; ok {
		t.Fatal("a token was sent that nobody gave")
	}
	if got := e.hub.lastHeaders().Get("User-Agent"); got != "just-dashboard/0.8.5 hub-link" {
		t.Fatalf("User-Agent = %q", got)
	}
	if e.svc.Hello(t.Context()) != HelloPending {
		t.Fatal("hello does not say a request is waiting")
	}
	if res, err := e.svc.Poll(t.Context()); err != nil || res.State != accountlink.PollPending {
		t.Fatalf("poll before approval = %+v %v", res, err)
	}

	e.hub.approve(req.ID)
	res, err := e.svc.Poll(t.Context())
	if err != nil || res.State != accountlink.PollLinked {
		t.Fatalf("poll = %+v %v", res, err)
	}
	st := res.Link
	if st.URL != e.hub.URL() || st.ServerID != vectorServerID || st.Owner != "hub" || st.Scope != accountlink.ScopeOperate ||
		st.HeartbeatSeconds != 15 || st.LinkedBy != "ion" || st.SiteKeys[0].Kid != vectorKid {
		t.Fatalf("link = %+v", st)
	}
	if e.svc.Hello(t.Context()) != HelloLinked {
		t.Fatal("hello does not say linked")
	}
	rows := e.auditRows("hub.link")
	if len(rows) != 1 || rows[0].Action != "hub.link" || rows[0].Username != "ion" || !strings.Contains(rows[0].Detail, e.hub.URL()) {
		t.Fatalf("audit = %+v", rows)
	}
	// Its state is under hub.link.*, and nothing was written for the account
	// link.
	for _, key := range []string{"hub.link.identity", "hub.link.state", "hub.link.sequence"} {
		if _, ok, _ := e.st.Setting(t.Context(), key); !ok {
			t.Errorf("%s is missing", key)
		}
	}
	if _, ok, _ := e.st.Setting(t.Context(), "account.link.identity"); ok {
		t.Fatal("the hub link wrote the account link's identity")
	}
}

func TestATokenApprovesInAdvanceAndABadOneCreatesNothing(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.Connect(t.Context(), ConnectRequest{URL: e.hub.URL(), Scope: accountlink.ScopeMaintain, By: "cli", Token: "jht_short"}); !errors.Is(err, ErrBadToken) {
		t.Fatalf("malformed token = %v", err)
	}
	if e.hub.requestCount() != 0 {
		t.Fatal("a malformed token reached the hub")
	}
	unknown := "jht_" + strings.Repeat("B", 43)
	_, err := e.svc.Connect(t.Context(), ConnectRequest{URL: e.hub.URL(), Scope: accountlink.ScopeMaintain, By: "cli", Token: unknown})
	var un *UnreachableError
	if !errors.As(err, &un) || !strings.Contains(err.Error(), "bad_token") {
		t.Fatalf("unknown token = %v", err)
	}
	if _, pending, _ := e.svc.Pending(t.Context()); pending || e.hub.requestCount() != 0 {
		t.Fatal("a refused token left a request behind")
	}

	token := "jht_" + strings.Repeat("A", 43)
	e.hub.addToken(token)
	req, err := e.svc.Connect(t.Context(), ConnectRequest{URL: e.hub.URL(), Scope: accountlink.ScopeMaintain, By: "cli", Token: token})
	if err != nil {
		t.Fatal(err)
	}
	if e.hub.request(req.ID).body["token"] != token {
		t.Fatal("the token was not sent")
	}
	res, err := e.svc.Await(t.Context())
	if err != nil || res.State != accountlink.PollLinked || res.Link.LinkedBy != "cli" {
		t.Fatalf("await = %+v %v", res, err)
	}
	// Single use: the same token again is refused and nothing is made.
	if _, err := e.svc.Disconnect(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Connect(t.Context(), ConnectRequest{URL: e.hub.URL(), Scope: accountlink.ScopeMaintain, By: "cli", Token: token}); err == nil {
		t.Fatal("a used token was accepted")
	}
}

func TestOneHubPerServerAndOneRequestAtATime(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.Connect(t.Context(), ConnectRequest{URL: e.hub.URL(), Scope: accountlink.ScopeObserve, By: "ion"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Connect(t.Context(), ConnectRequest{URL: e.hub.URL(), Scope: accountlink.ScopeObserve, By: "ion"}); !errors.Is(err, ErrLinkPending) {
		t.Fatalf("second connect while pending = %v", err)
	}
	if err := e.svc.Cancel(t.Context()); err != nil {
		t.Fatal(err)
	}
	e.link(accountlink.ScopeObserve)
	before := e.tailnet.calls()
	if _, err := e.svc.Connect(t.Context(), ConnectRequest{URL: "hub-2.tailed39ba.ts.net", Scope: accountlink.ScopeObserve, By: "ion"}); !errors.Is(err, ErrAlreadyLinked) {
		t.Fatalf("connect while linked = %v", err)
	}
	if e.tailnet.calls() != before {
		t.Fatal("a refused connect still asked the tailnet")
	}
}

func TestADeclinedOrExpiredRequestIsRememberedForThePage(t *testing.T) {
	e := newEnv(t)
	req, err := e.svc.Connect(t.Context(), ConnectRequest{URL: e.hub.URL(), Scope: accountlink.ScopeObserve, By: "ion"})
	if err != nil {
		t.Fatal(err)
	}
	e.hub.setRequestStatus(req.ID, "denied")
	if res, _ := e.svc.Poll(t.Context()); res.State != accountlink.PollDenied || res.Message != "the request was declined in the hub" {
		t.Fatalf("poll = %+v", res)
	}
	s, _ := e.svc.Status(t.Context(), true)
	if s.State != StateUnlinked || s.Admin.LastOutcome == nil || s.Admin.LastOutcome.Result != "denied" {
		t.Fatalf("status = %+v", s)
	}
	if e.svc.Hello(t.Context()) != HelloAvailable {
		t.Fatal("hello after a declined request")
	}
}

func TestTheHeartbeatCarriesTheSnapshotAndCachesTheFleetAndNotices(t *testing.T) {
	e := newEnv(t)
	e.link(accountlink.ScopeObserve)
	e.hub.setReply("fleet", []map[string]any{
		{"serverId": vectorServerID, "name": "box", "endpoint": "https://box.tailed39ba.ts.net:8443", "state": "online", "version": "0.8.5", "self": true, "lastSeenAt": vectorNow},
		// An endpoint that breaks its rule is removed; the entry stays. (A hub
		// at the test origin may name any https origin, so this one is http.)
		{"serverId": "5b1f9a20-0000-4000-8000-000000000001", "name": "vmi3690758", "endpoint": "http://evil.example.com", "state": "late", "version": "0.8.5", "self": false},
		// Entries that break a rule are dropped.
		{"serverId": "bad", "name": "x", "state": "online", "version": "", "self": false},
		{"serverId": "5b1f9a20-0000-4000-8000-000000000009", "name": "x", "state": "sleeping", "version": "", "self": false},
		{"serverId": "5b1f9a20-0000-4000-8000-000000000008", "name": "second-self", "state": "online", "version": "", "self": true},
		{"serverId": "5b1f9a20-0000-4000-8000-000000000007", "name": "no version", "state": "online", "self": false},
	})
	e.hub.setReply("notices", []map[string]any{
		{"id": "update-0.8.6", "level": "info", "title": "Update ready on 1 server", "body": "0.8.6 is out.", "href": e.hub.URL() + "/updates", "at": vectorNow - 120},
		{"id": "elsewhere", "level": "warning", "title": "Off-site link", "body": "", "href": "https://example.com/x", "at": vectorNow},
		{"id": "bad level", "level": "info", "title": "x", "body": "", "at": vectorNow},
		{"id": "loud", "level": "panic", "title": "x", "body": "", "at": vectorNow},
	})
	e.hub.setReply("signIn", map[string]any{"users": []string{"owner", "Not Valid"}})
	if err := e.svc.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	bodies := e.hub.heartbeatBodies()
	if len(bodies) != 1 {
		t.Fatalf("heartbeats = %d", len(bodies))
	}
	var hb map[string]any
	if err := json.Unmarshal(bodies[0], &hb); err != nil {
		t.Fatal(err)
	}
	server, _ := hb["server"].(map[string]any)
	containers, _ := hb["containers"].(map[string]any)
	if hb["protocol"] != 1.0 || server["scope"] != "observe" || containers["list"] != nil {
		t.Fatalf("heartbeat at observe = %s", bodies[0])
	}
	if _, ok := hb["signIn"]; ok {
		t.Fatal("the heartbeat carries signIn, which this release never sends")
	}

	s, err := e.svc.Status(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}
	if s.State != StateLinked || s.HubName != "hub" || s.HubURL != e.hub.URL() || s.Reachable == nil || !*s.Reachable || s.LastHeartbeatAt == nil {
		t.Fatalf("status = %+v", s)
	}
	if len(s.Fleet) != 2 || !s.Fleet[0].Self || s.Fleet[0].Endpoint == "" || s.Fleet[0].LastSeenAt == nil || s.Fleet[1].Endpoint != "" || s.Fleet[1].State != "late" {
		t.Fatalf("fleet = %+v", s.Fleet)
	}
	if len(s.Notices) != 2 || s.Notices[0].Href != e.hub.URL()+"/updates" || s.Notices[1].Href != "" {
		t.Fatalf("notices = %+v", s.Notices)
	}
	if s.SignIn == nil || s.SignIn.Enabled || !s.SignIn.Offered || strings.Join(s.SignIn.Users, ",") != "owner" {
		t.Fatalf("signIn = %+v", s.SignIn)
	}
	if s.Admin == nil || s.Admin.Link == nil || string(s.Admin.Link.LastSnapshot) != string(bodies[0]) || len(s.Admin.Link.HubKeys) != 1 ||
		s.Admin.Link.HubKeys[0].Fingerprint != "39f7:13d0:a644:253f" || s.Admin.TestOrigin != e.hub.URL() {
		t.Fatalf("admin = %+v", s.Admin)
	}
	if sess := e.svc.Session(t.Context()); sess == nil || !sess.Linked || sess.Name != "hub" || sess.URL != e.hub.URL() || !sess.Reachable {
		t.Fatalf("session = %+v", sess)
	}

	// Three intervals without a heartbeat and the hub is no longer reachable.
	e.advance(46 * time.Second)
	if sess := e.svc.Session(t.Context()); sess.Reachable {
		t.Fatal("a heartbeat older than three intervals still reads as reachable")
	}
}

func TestAReplyThatIsNotProtocolOneFailsTheHeartbeatAndIsNotUsed(t *testing.T) {
	for name, set := range map[string]func(*fakeHub){
		"unknown top-level field": func(h *fakeHub) { h.setReply("plan", "pro") },
		"no fleet":                func(h *fakeHub) { h.setReply("fleet", nil) },
		"null notices":            func(h *fakeHub) { h.setReply("notices", json.RawMessage("null")) },
		"unknown fleet field": func(h *fakeHub) {
			h.setReply("fleet", []map[string]any{{"serverId": vectorServerID, "name": "box", "state": "online", "version": "", "self": true, "seats": 3}})
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.link(accountlink.ScopeObserve)
			set(e.hub)
			_ = e.svc.Sync(t.Context())
			s, _ := e.svc.Status(t.Context(), true)
			if *s.Reachable || s.Admin.Link.LastError == "" || len(s.Fleet) != 0 {
				t.Fatalf("status after a bad reply = %+v / %+v", s, s.Admin.Link)
			}
		})
	}
}

// Every scope, every kind: what each scope may do runs through the Host as
// hub:<name>, what it may not is refused, and all of it is audited under
// hub.command.*.
func TestCommandsRunAtTheirScopeAndAreAuditedAsTheHub(t *testing.T) {
	kinds := []struct{ kind, args, scope, method string }{
		{"snapshot.now", `{}`, "observe", ""},
		{"container.start", `{"container":"shop-web"}`, "operate", "Container"},
		{"container.stop", `{"container":"shop-web"}`, "operate", "Container"},
		{"container.restart", `{"container":"shop-web"}`, "operate", "Container"},
		{"stack.restart", `{"name":"shop"}`, "operate", "RestartStack"},
		{"backup.run", `{"jobId":3}`, "operate", "RunBackup"},
		{"dashboard.update.install", `{"version":"0.8.6"}`, "maintain", "InstallUpdate"},
		{"dashboard.update.policy", `{"auto":"patch","window":{"start":"03:00","end":"05:00"}}`, "maintain", ""},
		{"dashboard.restart", `{"rebuild":false}`, "maintain", "RestartDashboard"},
	}
	for _, held := range []accountlink.Scope{accountlink.ScopeObserve, accountlink.ScopeOperate, accountlink.ScopeMaintain} {
		t.Run(string(held), func(t *testing.T) {
			e := newEnv(t)
			e.link(held)
			e.svc.Start(t.Context())
			expires := e.clock().Add(10 * time.Minute)
			for i, k := range kinds {
				e.hub.command(ulid(i+1), k.kind, k.args, expires)
			}
			for i, k := range kinds {
				id := ulid(i + 1)
				waitFor(t, id, func() bool { _, ok := e.hub.result(id); return ok })
				res, _ := e.hub.result(id)
				allowed := held.Includes(accountlink.Scope(k.scope))
				if allowed && res.Status != "succeeded" {
					t.Errorf("%s at %s: %s %s", k.kind, held, res.Status, res.Error)
				}
				if !allowed && (res.Status != "refused" || !strings.Contains(res.Error, "needs scope "+k.scope)) {
					t.Errorf("%s at %s: %s %s, want refused", k.kind, held, res.Status, res.Error)
				}
			}
			ran := map[string]int{}
			for _, c := range e.host.called() {
				if c.Actor != "hub:hub" {
					t.Errorf("%s ran as %s", c.Method, c.Actor)
				}
				ran[c.Method]++
			}
			want := map[string]int{}
			for _, k := range kinds {
				if k.method != "" && held.Includes(accountlink.Scope(k.scope)) {
					want[k.method]++
				}
			}
			if len(ran) != len(want) {
				t.Errorf("host calls = %v, want %v", ran, want)
			}
			for m, n := range want {
				if ran[m] != n {
					t.Errorf("%s ran %d times, want %d", m, ran[m], n)
				}
			}
			rows := e.auditRows("hub.command.")
			if len(rows) != len(kinds) {
				t.Fatalf("audited %d commands, want %d", len(rows), len(kinds))
			}
			for _, r := range rows {
				if r.Actor != "hub:hub" || r.Username != "hub:hub" || r.Method != "LINK" || r.Path != "/api/hub/v1/commands" {
					t.Errorf("audit row = %+v", r)
				}
			}
			// The policy the hub set is its own, under hub.link.*.
			if held == accountlink.ScopeMaintain {
				if _, ok, _ := e.st.Setting(t.Context(), "hub.link.update-policy"); !ok {
					t.Error("the hub's update policy was not stored under hub.link.update-policy")
				}
				if _, ok, _ := e.st.Setting(t.Context(), "account.link.update-policy"); ok {
					t.Error("the hub's update policy was stored as the account link's")
				}
			}
		})
	}
}

func TestReplayedExpiredForgedAndUnknownCommandsAreRefusedAndAudited(t *testing.T) {
	e := newEnv(t)
	e.link(accountlink.ScopeMaintain)
	e.svc.Start(t.Context())
	now := e.clock()

	good := e.hub.command(ulid(1), "container.restart", `{"container":"shop-web"}`, now.Add(10*time.Minute))
	waitFor(t, "the first delivery", func() bool { _, ok := e.hub.result(good.ID); return ok })
	// The same signed command again: audited, not run, not reported over the
	// first result.
	e.hub.queueCommands(good)
	expired := e.hub.command(ulid(2), "container.restart", `{"container":"shop-web"}`, now.Add(-time.Second))
	forged := signCommand(t, seedKey(t, vectorOtherSeed), vectorKid, vectorServerID, ulid(3), "container.stop", `{"container":"shop-web"}`, now.Add(10*time.Minute).Unix())
	e.hub.queueCommands(forged)
	unknownKid := signCommand(t, e.hub.key, "hub-2099-01", vectorServerID, ulid(4), "container.stop", `{"container":"shop-web"}`, now.Add(10*time.Minute).Unix())
	e.hub.queueCommands(unknownKid)
	otherServer := signCommand(t, e.hub.key, vectorKid, "7a0d3c1e-0000-4000-8000-000000000003", ulid(5), "container.stop", `{"container":"shop-web"}`, now.Add(10*time.Minute).Unix())
	e.hub.queueCommands(otherServer)
	invalid := e.hub.command(ulid(6), "shell.run", `{"cmd":"id"}`, now.Add(10*time.Minute))

	for _, c := range []accountlink.Command{expired, forged, unknownKid, otherServer, invalid} {
		id := c.ID
		waitFor(t, id, func() bool { _, ok := e.hub.result(id); return ok })
		if res, _ := e.hub.result(id); res.Status != "refused" {
			t.Errorf("%s: %s %s", id, res.Status, res.Error)
		}
	}
	waitFor(t, "the replay's audit entry", func() bool {
		n := 0
		for _, r := range e.auditRows("hub.command.container.restart") {
			if strings.Contains(r.Detail, "already received") {
				n++
			}
		}
		return n == 1
	})
	if calls := e.host.called(); len(calls) != 1 {
		t.Fatalf("host calls = %+v, want only the first delivery", calls)
	}
	if rows := e.auditRows("hub.command.invalid"); len(rows) != 1 || rows[0].Success {
		t.Fatalf("invalid kind audit = %+v", rows)
	}
	var n int
	if err := e.st.DB.QueryRow(`SELECT count(*) FROM hub_link_commands`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("hub_link_commands rows = %d %v; only commands past the signature are recorded", n, err)
	}
	if err := e.st.DB.QueryRow(`SELECT count(*) FROM account_link_commands`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("account_link_commands rows = %d %v", n, err)
	}
}

func TestDisconnectTellsTheHubAndKeepsTheIdentity(t *testing.T) {
	e := newEnv(t)
	e.link(accountlink.ScopeObserve)
	_ = e.svc.Sync(t.Context())
	fp := e.svc.engine.Fingerprint(t.Context())
	seqBefore, _, _ := e.st.Setting(t.Context(), "hub.link.sequence")
	notified, err := e.svc.Disconnect(t.Context())
	if err != nil || !notified || !e.hub.removed() {
		t.Fatalf("disconnect = %v %v", notified, err)
	}
	for _, key := range []string{"hub.link.state", "hub.link.view", "hub.link.status"} {
		if _, ok, _ := e.st.Setting(t.Context(), key); ok {
			t.Errorf("%s survived the disconnect", key)
		}
	}
	if e.svc.engine.Fingerprint(t.Context()) != fp {
		t.Fatal("the identity changed")
	}
	if seq, _, _ := e.st.Setting(t.Context(), "hub.link.sequence"); seq < seqBefore {
		t.Fatal("the sequence went back")
	}
	if e.svc.Session(t.Context()) != nil || e.svc.Hello(t.Context()) != HelloAvailable {
		t.Fatal("still shown as linked")
	}
	if _, err := e.svc.Disconnect(t.Context()); !errors.Is(err, ErrNotLinked) {
		t.Fatalf("second disconnect = %v", err)
	}
}

func TestAServerRemovedInTheHubForgetsItsLink(t *testing.T) {
	e := newEnv(t)
	e.link(accountlink.ScopeObserve)
	_ = e.svc.Sync(t.Context())
	e.hub.remove()
	e.svc.Start(t.Context())
	// The audit entry is written once the link is cleared.
	waitFor(t, "the removal to be audited", func() bool { return len(e.auditRows("hub.unlink")) > 0 })
	if _, linked, _ := e.svc.Linked(t.Context()); linked {
		t.Fatal("the link outlived its removal")
	}
	rows := e.auditRows("hub.unlink")
	if len(rows) != 1 || rows[0].Actor != "system" || !strings.Contains(rows[0].Detail, "the server was removed in the hub") {
		t.Fatalf("audit = %+v", rows)
	}
	if _, ok, _ := e.st.Setting(t.Context(), "hub.link.view"); ok {
		t.Fatal("the cached view outlived the link")
	}
}

func TestSwitchedOffNothingIsSentAndConnectIsRefused(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Enabled = false })
	if _, err := e.svc.Connect(t.Context(), ConnectRequest{URL: e.hub.URL(), Scope: accountlink.ScopeObserve, By: "ion"}); !errors.Is(err, ErrDisabled) {
		t.Fatalf("connect = %v", err)
	}
	s, _ := e.svc.Status(t.Context(), true)
	raw, _ := json.Marshal(s)
	if string(raw) != `{"enabled":false,"state":"unlinked"}` {
		t.Fatalf("status = %s", raw)
	}
	e.svc.Start(t.Context())
	if e.svc.Session(t.Context()) != nil || e.hub.requestCount() != 0 {
		t.Fatal("something was sent or shown while switched off")
	}
}

// A link stored on one tailnet is not used once the server is on another: it
// stays stored, idles, and says why; a manual sync is refused.
func TestAStoredLinkThatNoLongerPassesTheRuleIdles(t *testing.T) {
	e := newEnv(t)
	st := e.link(accountlink.ScopeObserve)
	st.URL = "https://hub.tailed39ba.ts.net"
	if _, err := e.st.DB.Exec(`UPDATE settings SET value = ? WHERE key = 'hub.link.state'`, mustJSON(t, st)); err != nil {
		t.Fatal(err)
	}
	e.tailnet.set(func(s *TailnetStatus) { s.Suffix = "other.ts.net"; s.Self = "box.other.ts.net" })
	if err := e.svc.Sync(t.Context()); !errors.Is(err, ErrURLRefused) {
		t.Fatalf("sync = %v", err)
	}
	e.svc.Start(t.Context())
	waitFor(t, "the reason on the status", func() bool {
		s, _ := e.svc.Status(t.Context(), true)
		return s.Admin.Link != nil && strings.Contains(s.Admin.Link.LastError, "no longer accepts the hub's address")
	})
	if _, linked, _ := e.svc.Linked(t.Context()); !linked {
		t.Fatal("the link was cleared rather than idled")
	}
	if len(e.hub.heartbeatBodies()) != 0 {
		t.Fatal("a refused link sent a heartbeat")
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// The account link and the hub link run side by side on one store: separate
// identities, counters, state and command tables, and ending one leaves the
// other running.
func TestTheAccountLinkAndTheHubLinkRunSideBySide(t *testing.T) {
	e := newEnv(t)
	site := linkstub.New(t)
	account := accountlink.New(accountlink.Options{
		Store: e.st, Sealer: e.sealer, Audit: e.audit, SiteURL: site.URL, Enabled: true, Host: e.host, Version: "0.8.5",
	})
	account.SetTiming(accountlink.Timing{Idle: 10 * time.Millisecond, Watch: 10 * time.Millisecond, HeartbeatsPerMinute: 1 << 20})
	t.Cleanup(account.Stop)

	if _, err := account.BeginLink(t.Context(), accountlink.ScopeObserve, "ion"); err != nil {
		t.Fatal(err)
	}
	site.Approve("ion@example.com")
	if res, err := account.PollLink(t.Context()); err != nil || res.State != accountlink.PollLinked {
		t.Fatalf("account link = %+v %v", res, err)
	}
	e.link(accountlink.ScopeMaintain)

	if account.Fingerprint(t.Context()) == e.svc.engine.Fingerprint(t.Context()) {
		t.Fatal("the two links share an identity")
	}
	account.Start(t.Context())
	e.svc.Start(t.Context())
	waitFor(t, "a heartbeat on each link", func() bool {
		_, _, beats, _, _ := site.Count()
		return beats > 0 && len(e.hub.heartbeatBodies()) > 0
	})
	acctSeq, _, _ := e.st.Setting(t.Context(), "account.link.sequence")
	hubSeq, _, _ := e.st.Setting(t.Context(), "hub.link.sequence")
	if acctSeq == "" || hubSeq == "" {
		t.Fatal("a counter is missing")
	}

	// A command from the hub is the hub's alone.
	e.hub.command(ulid(1), "container.restart", `{"container":"shop-web"}`, e.clock().Add(10*time.Minute))
	waitFor(t, "the command", func() bool { _, ok := e.hub.result(ulid(1)); return ok })
	if rows := e.auditRows("account.command."); len(rows) != 0 {
		t.Fatalf("a hub command was audited as the account link's: %+v", rows)
	}

	// Ending the hub link leaves the account link running.
	if _, err := e.svc.Disconnect(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, linked, _ := account.Linked(t.Context()); !linked {
		t.Fatal("disconnecting the hub unlinked the account")
	}
	_, _, before, _, _ := site.Count()
	waitFor(t, "the account link to keep sending", func() bool {
		_ = account.SyncNow(t.Context())
		_, _, after, _, _ := site.Count()
		return after > before
	})
	// And the other way round.
	e.link(accountlink.ScopeObserve)
	if _, err := account.Unlink(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, linked, _ := e.svc.Linked(t.Context()); !linked {
		t.Fatal("unlinking the account disconnected the hub")
	}
}
