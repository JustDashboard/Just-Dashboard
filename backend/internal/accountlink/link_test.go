package accountlink

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestALinkRequestIsApprovedAndStoredWithItsKeys(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := t.Context()

	status, err := e.svc.Status(ctx)
	if err != nil || status.State != StateUnlinked || status.Fingerprint != "" || !status.Enabled || status.SiteURL != e.site.srv.URL {
		t.Fatalf("fresh status = %+v, %v", status, err)
	}

	req, err := e.svc.BeginLink(ctx, ScopeOperate, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if req.UserCode != "ABCD-2223" || req.Scope != ScopeOperate || req.By != "admin" || req.Driver != driverDashboard {
		t.Fatalf("request = %+v", req)
	}
	// The body the site was sent describes the machine and carries the public key.
	e.site.mu.Lock()
	sent := e.site.requests[req.ID]
	e.site.mu.Unlock()
	var described map[string]string
	if err := json.Unmarshal(sent.server, &described); err != nil {
		t.Fatal(err)
	}
	if described["name"] != "box" || described["version"] != "0.8.2" || described["os"] != "Ubuntu 24.04" || described["arch"] != "amd64" || sent.scope != "operate" {
		t.Fatalf("site was told %v scope %q", described, sent.scope)
	}

	status, _ = e.svc.Status(ctx)
	if status.State != StatePending || status.Request == nil || status.Request.UserCode != req.UserCode ||
		status.Request.Scope != ScopeOperate || status.Fingerprint == "" || status.Link != nil {
		t.Fatalf("pending status = %+v", status)
	}
	if res, err := e.svc.PollLink(ctx); err != nil || res.State != PollPending {
		t.Fatalf("poll before approval = %+v, %v", res, err)
	}

	e.site.approve(req.ID, "ion@example.com")
	res, err := e.svc.PollLink(ctx)
	if err != nil || res.State != PollLinked {
		t.Fatalf("poll after approval = %+v, %v", res, err)
	}
	st, linked, err := e.svc.kv.state(ctx)
	if err != nil || !linked {
		t.Fatalf("state not stored: %v", err)
	}
	if st.Owner != "ion@example.com" || st.Scope != ScopeOperate || st.LinkedBy != "admin" || st.URL != e.site.srv.URL ||
		st.HeartbeatSeconds != 30 || st.PollWaitSeconds != 1 || len(st.SiteKeys) != 1 || st.SiteKeys[0].Kid != vectorKid ||
		st.SiteKeys[0].PublicKey != pubB64(e.site.key) {
		t.Fatalf("state = %+v", st)
	}
	if _, pending, _ := e.svc.kv.request(ctx); pending {
		t.Fatal("the request outlived its approval")
	}

	status, _ = e.svc.Status(ctx)
	if status.State != StateLinked || status.Link == nil || status.Request != nil ||
		status.Link.Owner != "ion@example.com" || len(status.Link.SiteKeys) != 1 ||
		status.Link.SiteKeys[0].Fingerprint != Fingerprint(e.site.key.Public().(ed25519.PublicKey)) {
		t.Fatalf("linked status = %+v", status)
	}

	// The approval is delivered once; a second poll finds it gone.
	if _, err := e.svc.BeginLink(ctx, ScopeObserve, "admin"); err != ErrAlreadyLinked {
		t.Fatalf("linking twice = %v", err)
	}
	if rows := e.auditRows("account.link"); len(rows) != 1 || rows[0].Target != "ion@example.com" || !rows[0].Success {
		t.Fatalf("audit = %+v", rows)
	}
}

func TestADeclinedRequestEndsWithItsReason(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	req, _ := e.svc.BeginLink(t.Context(), ScopeObserve, "admin")
	e.site.setRequestStatus(req.ID, "denied")
	res, err := e.svc.PollLink(t.Context())
	if err != nil || res.State != PollDenied {
		t.Fatalf("poll = %+v, %v", res, err)
	}
	status, _ := e.svc.Status(t.Context())
	if status.State != StateUnlinked || status.Request != nil || status.LastOutcome == nil || status.LastOutcome.Result != "denied" {
		t.Fatalf("status = %+v", status)
	}
	// And a new request can be started, which clears the old outcome.
	if _, err := e.svc.BeginLink(t.Context(), ScopeObserve, "admin"); err != nil {
		t.Fatal(err)
	}
	if status, _ := e.svc.Status(t.Context()); status.LastOutcome != nil {
		t.Fatalf("outcome survived a new request: %+v", status.LastOutcome)
	}
}

func TestAnExpiredRequestEndsWhetherTheSiteOrTheClockSaysSo(t *testing.T) {
	t.Parallel()
	t.Run("the site says so", func(t *testing.T) {
		e := newEnv(t)
		req, _ := e.svc.BeginLink(t.Context(), ScopeObserve, "admin")
		e.site.setRequestStatus(req.ID, "expired")
		if res, err := e.svc.PollLink(t.Context()); err != nil || res.State != PollExpired {
			t.Fatalf("poll = %+v, %v", res, err)
		}
		if status, _ := e.svc.Status(t.Context()); status.LastOutcome == nil || status.LastOutcome.Result != "expired" {
			t.Fatalf("status = %+v", status)
		}
	})
	t.Run("the clock says so", func(t *testing.T) {
		e := newEnv(t)
		e.svc.BeginLink(t.Context(), ScopeObserve, "admin")
		hits := e.site.hitCount("GET /requests/")
		e.advance(16 * time.Minute)
		if res, err := e.svc.PollLink(t.Context()); err != nil || res.State != PollExpired {
			t.Fatalf("poll = %+v, %v", res, err)
		}
		if e.site.hitCount("GET /requests/") != hits {
			t.Fatal("an expired request was still polled")
		}
		// Reading the status closes a request nobody polled, too.
		e.svc.BeginLink(t.Context(), ScopeObserve, "admin")
		e.advance(16 * time.Minute)
		if status, _ := e.svc.Status(t.Context()); status.State != StateUnlinked || status.LastOutcome == nil {
			t.Fatalf("status = %+v", status)
		}
	})
}

func TestOnlyOneRequestIsPendingAndItCanBeCancelled(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	if _, err := e.svc.BeginLink(t.Context(), ScopeObserve, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.BeginLink(t.Context(), ScopeObserve, "admin"); err != ErrLinkPending {
		t.Fatalf("second request = %v", err)
	}
	if err := e.svc.CancelLink(t.Context()); err != nil {
		t.Fatal(err)
	}
	if status, _ := e.svc.Status(t.Context()); status.State != StateUnlinked {
		t.Fatalf("status after cancel = %+v", status)
	}
	if err := e.svc.CancelLink(t.Context()); err != nil {
		t.Fatalf("cancelling nothing is not an error: %v", err)
	}
}

func TestATerminalCommandReplacesTheRequestAKilledOneLeftButNotOneFromSettings(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	if _, err := e.svc.BeginLink(t.Context(), ScopeObserve, "cli"); err != nil {
		t.Fatal(err)
	}
	// The command was killed; another is run.
	second, err := e.svc.BeginLink(t.Context(), ScopeOperate, "cli")
	if err != nil {
		t.Fatalf("a killed command's request blocked the next one: %v", err)
	}
	if st, _ := e.svc.Status(t.Context()); st.Request == nil || st.Request.UserCode != second.UserCode || st.Request.Scope != ScopeOperate {
		t.Fatalf("status = %+v", st)
	}
	// A request begun in Settings belongs to a person, and the terminal does not take it over.
	e2 := newEnv(t)
	if _, err := e2.svc.BeginLink(t.Context(), ScopeObserve, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := e2.svc.BeginLink(t.Context(), ScopeObserve, "cli"); err != ErrLinkPending {
		t.Fatalf("terminal over Settings = %v", err)
	}
}

func TestLinkingIsRefusedWhenSwitchedOffOrAskedForABadScope(t *testing.T) {
	t.Parallel()
	off := newEnv(t, func(o *Options) { o.Enabled = false })
	if _, err := off.svc.BeginLink(t.Context(), ScopeObserve, "admin"); err != ErrDisabled {
		t.Fatalf("disabled = %v", err)
	}
	if off.site.hitCount("") != 0 {
		t.Fatal("a disabled dashboard contacted the site")
	}
	if status, _ := off.svc.Status(t.Context()); status.Enabled {
		t.Fatal("status says enabled")
	}
	e := newEnv(t)
	for _, scope := range []Scope{"", "root", "OBSERVE"} {
		if _, err := e.svc.BeginLink(t.Context(), scope, "admin"); err != ErrBadScope {
			t.Fatalf("scope %q = %v", scope, err)
		}
	}
}

func TestAnUnreachableOrIncompatibleSiteIsASiteError(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(o *Options) { o.SiteURL = "http://127.0.0.1:1" })
	_, err := e.svc.BeginLink(t.Context(), ScopeObserve, "admin")
	var site *SiteError
	if !asSiteError(err, &site) || !strings.Contains(err.Error(), "http://127.0.0.1:1") {
		t.Fatalf("unreachable = %v", err)
	}
	if status, _ := e.svc.Status(t.Context()); status.State != StateUnlinked {
		t.Fatalf("a failed begin left %+v", status)
	}

	e = newEnv(t)
	e.site.siteReply = func(w http.ResponseWriter) bool {
		writeJSON(w, 200, map[string]any{"protocol": []int{2, 3}, "siteKeys": []any{}, "approveUrl": "x"})
		return true
	}
	if _, err := e.svc.BeginLink(t.Context(), ScopeObserve, "admin"); !asSiteError(err, &site) || !strings.Contains(err.Error(), "protocol 1") {
		t.Fatalf("incompatible = %v", err)
	}

	e = newEnv(t)
	e.site.fail("/requests", 1)
	if _, err := e.svc.BeginLink(t.Context(), ScopeObserve, "admin"); !asSiteError(err, &site) {
		t.Fatalf("site failure = %v", err)
	}
}

func asSiteError(err error, target **SiteError) bool {
	for err != nil {
		if s, ok := err.(*SiteError); ok {
			*target = s
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func TestAnApprovalAddressOnAnotherSiteIsNotAccepted(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	good := e.site.srv.Config.Handler
	e.site.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/requests") {
			writeJSON(w, 200, createRequestResponse{
				RequestID: "req-00000001-aaaa", UserCode: "ABCD-2222", ApproveURL: "https://evil.example/link?code=ABCD-2222",
				ExpiresAt: e.clock().Add(15 * time.Minute).Unix(), PollSeconds: 5,
			})
			return
		}
		good.ServeHTTP(w, r)
	})
	if _, err := e.svc.BeginLink(t.Context(), ScopeObserve, "admin"); err == nil || !strings.Contains(err.Error(), "another site") {
		t.Fatalf("foreign approval address accepted: %v", err)
	}
}

func TestAMalformedApprovalIsNotStored(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*pollResponse){
		"no server id":  func(r *pollResponse) { r.ServerID = "" },
		"no site key":   func(r *pollResponse) { r.SiteKey = nil },
		"short key":     func(r *pollResponse) { r.SiteKey.PublicKey = "AAAA" },
		"no owner":      func(r *pollResponse) { r.Owner = nil },
		"bad key id":    func(r *pollResponse) { r.SiteKey.Kid = "../x" },
		"blank account": func(r *pollResponse) { r.Owner.Label = "  " },
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			req, _ := e.svc.BeginLink(t.Context(), ScopeObserve, "admin")
			resp := pollResponse{Status: "approved", ServerID: "server-0001-0000", HeartbeatSeconds: 30, PollWaitSeconds: 25}
			resp.SiteKey = &struct {
				Kid       string `json:"kid"`
				PublicKey string `json:"publicKey"`
			}{vectorKid, pubB64(e.site.key)}
			resp.Owner = &struct {
				Label string `json:"label"`
			}{"ion@example.com"}
			mutate(&resp)
			if _, err := e.svc.completeLink(t.Context(), req, resp); err == nil {
				t.Fatal("accepted")
			}
			if _, linked, _ := e.svc.kv.state(t.Context()); linked {
				t.Fatal("a half link was stored")
			}
		})
	}
}

func TestConsumedApprovalIsLinkedIfAnotherProcessCollectedIt(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	req, _ := e.svc.BeginLink(t.Context(), ScopeObserve, "admin")
	// The other process took the one-time approval and stored the link.
	e.site.setRequestStatus(req.ID, "consumed")
	other := State{URL: e.site.srv.URL, ServerID: "server-0001-0000", Scope: ScopeObserve, Owner: "ion@example.com",
		SiteKeys: []SiteKey{{Kid: vectorKid, PublicKey: pubB64(e.site.key)}}}
	if err := e.svc.kv.put(t.Context(), keyState, other); err != nil {
		t.Fatal(err)
	}
	if res, err := e.svc.PollLink(t.Context()); err != nil || res.State != PollLinked {
		t.Fatalf("poll = %+v, %v", res, err)
	}

	// Without that stored link, a consumed request is a failure the page can name.
	e2 := newEnv(t)
	req2, _ := e2.svc.BeginLink(t.Context(), ScopeObserve, "admin")
	e2.site.setRequestStatus(req2.ID, "consumed")
	res, err := e2.svc.PollLink(t.Context())
	if err != nil || res.State != PollFailed {
		t.Fatalf("poll = %+v, %v", res, err)
	}
	// On the dashboard-driven path there is no other process to blame; what
	// happened is that the approval was handed out and not kept.
	if strings.Contains(res.Message, "another process") || !strings.Contains(res.Message, "no link was stored") || !strings.Contains(res.Message, "request a new code") {
		t.Fatalf("message = %q", res.Message)
	}
	if status, _ := e2.svc.Status(t.Context()); status.LastOutcome == nil || status.LastOutcome.Message != res.Message {
		t.Fatalf("status = %+v", status)
	}
}

func TestPollsRacingForOneApprovalAllSeeTheLink(t *testing.T) {
	t.Parallel()
	// The site hands an approval over once. Polls from one process that race
	// for it must all come back linked: the losers are told it was consumed,
	// and that is only true once the winner has stored the link.
	for round := 0; round < 20; round++ {
		e := newEnv(t)
		req, err := e.svc.BeginLink(t.Context(), ScopeObserve, "admin")
		if err != nil {
			t.Fatal(err)
		}
		e.site.approve(req.ID, "ion@example.com")
		results := make(chan PollResult, 4)
		for range 4 {
			go func() {
				res, err := e.svc.PollLink(t.Context())
				if err != nil {
					res = PollResult{State: "error", Message: err.Error()}
				}
				results <- res
			}()
		}
		for range 4 {
			if res := <-results; res.State != PollLinked {
				t.Fatalf("round %d: a racing poll = %+v", round, res)
			}
		}
		if status, _ := e.svc.Status(t.Context()); status.LastOutcome != nil {
			t.Fatalf("round %d: a linked install shows the outcome %+v", round, status.LastOutcome)
		}
	}
}

func TestATransientPollFailureLeavesTheRequestStanding(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.svc.BeginLink(t.Context(), ScopeObserve, "admin")
	e.site.fail("/requests/", 1)
	if res, err := e.svc.PollLink(t.Context()); err == nil || res.State != PollPending {
		t.Fatalf("poll = %+v, %v", res, err)
	}
	if status, _ := e.svc.Status(t.Context()); status.State != StatePending {
		t.Fatalf("status = %+v", status)
	}
}

func TestAwaitLinkReturnsOnApprovalAndCancelsOnTimeout(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	req, _ := e.svc.BeginLink(t.Context(), ScopeObserve, "cli")
	if req.Driver != driverCLI {
		t.Fatalf("driver = %s", req.Driver)
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		e.site.approve(req.ID, "ion@example.com")
	}()
	// The request asks to be polled every two seconds; the poll that sees the
	// approval is the first one after the site flips.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	res, err := e.svc.AwaitLink(ctx)
	if err != nil || res.State != PollLinked || res.Link.LinkedBy != "cli" {
		t.Fatalf("await = %+v, %v", res, err)
	}

	e2 := newEnv(t)
	e2.svc.BeginLink(t.Context(), ScopeObserve, "cli")
	short, cancel2 := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel2()
	res, err = e2.svc.AwaitLink(short)
	if err != nil || res.State != PollExpired {
		t.Fatalf("timeout = %+v, %v", res, err)
	}
	if status, _ := e2.svc.Status(t.Context()); status.State != StateUnlinked {
		t.Fatalf("a timed-out request was left pending: %+v", status)
	}
}

func TestScopeCanBeChangedOnlyWhileLinked(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	if err := e.svc.SetScope(t.Context(), ScopeOperate); err != ErrNotLinked {
		t.Fatalf("unlinked = %v", err)
	}
	e.link(ScopeObserve)
	if err := e.svc.SetScope(t.Context(), "root"); err != ErrBadScope {
		t.Fatalf("bad scope = %v", err)
	}
	if err := e.svc.SetScope(t.Context(), ScopeMaintain); err != nil {
		t.Fatal(err)
	}
	if status, _ := e.svc.Status(t.Context()); status.Link.Scope != ScopeMaintain {
		t.Fatalf("scope = %s", status.Link.Scope)
	}
}

func TestUnlinkTellsTheSiteForgetsTheLinkAndKeepsTheIdentity(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.link(ScopeObserve)
	before, _ := e.svc.Status(t.Context())
	e.svc.setPolicy(t.Context(), UpdatePolicy{Auto: AutoAll, Window: Window{"01:00", "02:00"}})

	notified, err := e.svc.Unlink(t.Context())
	if err != nil || !notified {
		t.Fatalf("unlink = %v, %v", notified, err)
	}
	if !e.site.unlinked {
		t.Fatal("the site was not told")
	}
	after, _ := e.svc.Status(t.Context())
	if after.State != StateUnlinked || after.Link != nil || after.Fingerprint != before.Fingerprint || after.Fingerprint == "" {
		t.Fatalf("after unlink = %+v (before %+v)", after, before)
	}
	if _, ok := e.svc.policy(t.Context()); ok {
		t.Fatal("the policy the site set outlived the link")
	}
	if _, err := e.svc.Unlink(t.Context()); err != ErrNotLinked {
		t.Fatalf("second unlink = %v", err)
	}
	// The identity is kept, so linking again shows the same fingerprint.
	req, err := e.svc.BeginLink(t.Context(), ScopeObserve, "admin")
	if err != nil {
		t.Fatal(err)
	}
	e.site.mu.Lock()
	pub := e.site.requests[req.ID].pub
	e.site.mu.Unlock()
	if Fingerprint(pub) != before.Fingerprint {
		t.Fatalf("fingerprint changed across an unlink")
	}
}

func TestUnlinkStillClearsWhenTheSiteCannotBeReached(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.link(ScopeObserve)
	e.site.srv.Close()
	notified, err := e.svc.Unlink(t.Context())
	if err != nil || notified {
		t.Fatalf("unlink = %v, %v", notified, err)
	}
	if status, _ := e.svc.Status(t.Context()); status.State != StateUnlinked {
		t.Fatalf("status = %+v", status)
	}
}

func TestSyncNowSendsAHeartbeatAndRecordsIt(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	if err := e.svc.SyncNow(t.Context()); err != ErrNotLinked {
		t.Fatalf("unlinked = %v", err)
	}
	e.link(ScopeObserve)
	if err := e.svc.SyncNow(t.Context()); err != nil {
		t.Fatal(err)
	}
	status, _ := e.svc.Status(t.Context())
	if status.Link.LastHeartbeatAt == nil || len(status.Link.LastSnapshot) == 0 || status.Link.LastError != "" {
		t.Fatalf("status = %+v", status.Link)
	}
	var sent Snapshot
	if err := json.Unmarshal(status.Link.LastSnapshot, &sent); err != nil || sent.Protocol != 1 || sent.Server.Scope != ScopeObserve || sent.Server.Name != "box" {
		t.Fatalf("lastSnapshot = %s (%v)", status.Link.LastSnapshot, err)
	}
	bodies := e.site.heartbeatBodies()
	if len(bodies) != 1 || string(bodies[0]) != string(status.Link.LastSnapshot) {
		t.Fatalf("lastSnapshot is not the exact body the site received:\n%s\n%s", status.Link.LastSnapshot, bodies)
	}

	// A failing site shows as lastError, cleared by the next success.
	e.site.fail("/heartbeat", 1)
	if err := e.svc.SyncNow(t.Context()); err != nil {
		t.Fatal(err)
	}
	if status, _ := e.svc.Status(t.Context()); status.Link.LastError == "" {
		t.Fatal("a failed heartbeat left no lastError")
	}
	if err := e.svc.SyncNow(t.Context()); err != nil {
		t.Fatal(err)
	}
	if status, _ := e.svc.Status(t.Context()); status.Link.LastError != "" {
		t.Fatalf("lastError not cleared: %q", status.Link.LastError)
	}
}

func TestTheFifthHeartbeatInAMinuteWaitsForTheSitesWindow(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.svc.hbPerMinute = maxHeartbeatsPerMinute
	now := time.Now()
	for i := 0; i < 4; i++ {
		e.svc.hbTimes = append(e.svc.hbTimes, now.Add(-time.Duration(40-i*5)*time.Second))
	}
	if wait := e.svc.heartbeatWait(); wait <= 0 || wait > 61*time.Second {
		t.Fatalf("wait = %s", wait)
	}
	e.svc.hbTimes = e.svc.hbTimes[:0]
	for i := 0; i < 4; i++ {
		e.svc.hbTimes = append(e.svc.hbTimes, now.Add(-time.Duration(90-i*5)*time.Second))
	}
	if wait := e.svc.heartbeatWait(); wait != 0 {
		t.Fatalf("wait after the window = %s", wait)
	}
}

func TestTheIdentityIsSealedAndAnUnreadableOneIsReplacedOnlyWhileUnlinked(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	req, err := e.svc.BeginLink(t.Context(), ScopeObserve, "admin")
	if err != nil {
		t.Fatal(err)
	}
	raw, _, _ := e.st.Setting(t.Context(), keyIdentity)
	id, _, _ := loadIdentity(t.Context(), e.st, e.svc.sealer)
	if strings.Contains(raw, id.PublicKey()) || raw == "" {
		t.Fatalf("identity stored in the clear: %q", raw)
	}
	e.svc.CancelLink(t.Context())
	_ = req

	// A master key that cannot open the stored identity.
	if err := e.st.SetSetting(t.Context(), keyIdentity, "not-sealed-by-this-key"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadIdentity(t.Context(), e.st, e.svc.sealer); err != ErrIdentityUnreadable {
		t.Fatalf("load = %v", err)
	}
	if status, _ := e.svc.Status(t.Context()); status.Fingerprint != "" {
		t.Fatalf("fingerprint of an unreadable identity: %q", status.Fingerprint)
	}
	if _, err := e.svc.BeginLink(t.Context(), ScopeObserve, "admin"); err != nil {
		t.Fatalf("an unlinked install could not recover from an unreadable identity: %v", err)
	}
}

func TestTextSentToTheSiteFitsItsLimitsInCharactersNotBytes(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"":                         "unknown",
		"   ":                      "unknown",
		"box":                      "box",
		"  padded  ":               "padded",
		"ünïcödé-host":             "ünïcödé-host",
		"a\x07b\nc":                "a b c",
		strings.Repeat("é", 100):   strings.Repeat("é", 64),
		strings.Repeat("host", 50): strings.Repeat("host", 16),
	} {
		if got := fitText(in, 64); got != want {
			t.Errorf("fitText(%q) = %q, want %q", in, got, want)
		}
	}
	if got := fitText("Ubuntu 24.04 LTS (Noble Numbat) on a very long virtualised platform string", 64); len([]rune(got)) != 64 {
		t.Errorf("long OS string = %d characters", len([]rune(got)))
	}
}
