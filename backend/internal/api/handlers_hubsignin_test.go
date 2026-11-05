package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/hublink"
	"github.com/Wayy01/Just-Dashboard/backend/internal/hublink/hubstub"
	"github.com/Wayy01/Just-Dashboard/backend/internal/tailnetgate"
	"github.com/pquerna/otp/totp"
)

// Hub sign-in through the real router (hub-link-protocol.md section 9): a
// ticket signed with the hub key the stub pins, redeemed at
// POST /api/v1/auth/hub/enter, and every event that must end the session it
// made.

const hubPassword = "Correct-Horse-Battery-9"

// signInWhoIs answers for the browser addresses the tests use: one device of
// one tailnet login.
type signInWhoIs struct{}

func (signInWhoIs) WhoIs(_ context.Context, a netip.Addr) (tailnetgate.Peer, error) {
	switch a.String() {
	case "100.101.102.103":
		return tailnetgate.Peer{Address: a, Login: "Wayy01@GitHub", StableID: "nLaptop", Node: "laptop.tailed39ba.ts.net"}, nil
	case "100.101.102.104":
		return tailnetgate.Peer{Address: a, Login: "someone@github", StableID: "nOther", Node: "other.tailed39ba.ts.net"}, nil
	}
	return tailnetgate.Peer{}, tailnetgate.ErrNotAPeer
}

type signInEnv struct {
	t     *testing.T
	s     *Server
	h     http.Handler
	hub   *hubstub.Hub
	admin *client
	// adminID is the administrator's account; it opted in for "owner".
	adminID int64
	calls   int
}

// hubAccount makes an account and a password session for it, with an
// authenticator when secret is wanted.
func hubAccount(t *testing.T, s *Server, name string, role auth.Role, withTOTP bool) (*client, int64, string) {
	t.Helper()
	ctx := t.Context()
	u, err := s.Auth.CreateUser(ctx, name, hubPassword, role, false)
	if err != nil {
		t.Fatal(err)
	}
	login, err := s.Auth.Login(ctx, name, hubPassword, "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	secret := ""
	if withTOTP {
		enroll, err := s.Auth.BeginTOTPEnrollment(ctx, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		secret = enroll.Secret
		code, _ := totp.GenerateCode(secret, time.Now())
		if _, err := s.Auth.ConfirmTOTPEnrollmentForSession(ctx, u.ID, login.SessionID, code); err != nil {
			t.Fatal(err)
		}
	}
	return &client{t: t, h: s.Routes(), cookie: httpx.SessionCookie + "=" + login.Token}, u.ID, secret
}

// nextCode is a code for the next step: the current one was used up when the
// authenticator was enrolled.
func nextCode(t *testing.T, secret string, steps int) string {
	t.Helper()
	code, err := totp.GenerateCode(secret, time.Now().Add(time.Duration(steps)*30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return code
}

// newSignInEnv is a server linked to the stub hub, with hub sign-in on, the hub
// offering "owner", and an administrator who opted in for "owner".
func newSignInEnv(t *testing.T) *signInEnv {
	t.Helper()
	s := testServer(t)
	s.Cfg.HubSignIn = true
	// Browsers on the tailnet may reach it, as on a real install.
	_, tailnet, _ := net.ParseCIDR("100.64.0.0/10")
	_, loopback, _ := net.ParseCIDR("127.0.0.0/8")
	s.Cfg.AllowedCIDRs = append(s.Cfg.AllowedCIDRs, tailnet, loopback)
	hub := hubstub.New(t)
	hub.OfferSignIn("owner")
	origin, err := hublink.ParseTestOrigin(hub.URL)
	if err != nil {
		t.Fatal(err)
	}
	s.modules.hubLink = hublink.New(hublink.Options{
		Store: s.Store, Sealer: s.Sealer, Audit: s.Audit, Enabled: true, TestOrigin: origin, SignIn: true,
		Auth: s.Auth, Host: newAccountHost(s), Version: "test", Tailnet: stubTailnet{}, WhoIs: signInWhoIs{},
		SessionsEnded: s.hubSockets.kick,
	})
	admin, adminID, secret := hubAccount(t, s, "ion", auth.RoleAdmin, true)
	e := &signInEnv{t: t, s: s, h: s.Routes(), hub: hub, admin: admin, adminID: adminID}
	if w := admin.do(http.MethodPost, "/api/v1/dashboard/hub", `{"url":"`+hub.URL+`","scope":"maintain"}`, nil); w.Code != 200 {
		t.Fatalf("connect = %d %s", w.Code, w.Body)
	}
	hub.Approve()
	if _, err := s.modules.hubLink.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	e.sync()
	e.allow(admin, "owner", hubPassword, nextCode(t, secret, 1), http.StatusNoContent)
	return e
}

// sync sends a heartbeat now, so the reply's sign-in offer is what the server
// holds.
func (e *signInEnv) sync() {
	e.t.Helper()
	if err := e.s.modules.hubLink.Sync(e.t.Context()); err != nil {
		e.t.Fatal(err)
	}
}

func (e *signInEnv) allow(c *client, hubUser, password, code string, want int) *httptest.ResponseRecorder {
	e.t.Helper()
	body, _ := json.Marshal(map[string]string{"hubUser": hubUser, "password": password, "code": code})
	w := c.do(http.MethodPut, "/api/v1/account/hub-signin", string(body), nil)
	if w.Code != want {
		e.t.Fatalf("allow %s = %d %s, want %d", hubUser, w.Code, w.Body, want)
	}
	return w
}

type ticketFields map[string]any

// ticket signs a payload as the hub does: v1.<kid>.<payload>.<signature>.
func (e *signInEnv) ticket(key ed25519.PrivateKey, change func(ticketFields)) string {
	e.t.Helper()
	now := time.Now().Unix()
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)
	p := ticketFields{
		"v": 1, "typ": "hub.signin", "hub": e.hub.URL, "serverId": hubstub.ServerID, "userId": e.adminID,
		"hubUser": "owner", "tsLogin": "wayy01@github", "tsNode": "nLaptop",
		"nonce": base64.RawURLEncoding.EncodeToString(nonce), "iat": now, "exp": now + 60, "mfaAt": now - 60,
		"next": "/dashboard",
	}
	if change != nil {
		change(p)
	}
	raw, _ := json.Marshal(p)
	seg := base64.RawURLEncoding.EncodeToString(raw)
	sig := ed25519.Sign(key, []byte("jd hub sign-in ticket v1\n"+hubstub.Kid+"\n"+seg))
	return "v1." + hubstub.Kid + "." + seg + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// redeem posts a ticket from a browser at addr, with cookie if it holds one.
func (e *signInEnv) redeem(ticket, addr, cookie string) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.redeemBody(map[string]any{"ticket": ticket}, addr, cookie)
}

// redeemBody posts body as the redeem's, from a browser at addr.
func (e *signInEnv) redeemBody(fields map[string]any, addr, cookie string) *httptest.ResponseRecorder {
	e.t.Helper()
	body, _ := json.Marshal(fields)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/hub/enter", strings.NewReader(string(body)))
	// Each redeem comes from its own address, so the sign-in budget per
	// address, which the redeem shares with the password form, is not what a
	// test meets.
	e.calls++
	req.RemoteAddr = fmt.Sprintf("127.0.%d.%d:5555", e.calls/250, e.calls%250+1)
	if addr != "" {
		req.RemoteAddr = addr + ":5555"
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(httpx.CSRFHeader, "1")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	return w
}

// enter redeems a fresh ticket from the loopback address and returns the hub
// session's client.
func (e *signInEnv) enter() *client {
	e.t.Helper()
	w := e.redeem(e.ticket(e.hub.PrivateKey(), nil), "", "")
	if w.Code != 200 {
		e.t.Fatalf("redeem = %d %s", w.Code, w.Body)
	}
	return &client{t: e.t, h: e.h, cookie: sessionCookieOf(e.t, w)}
}

func sessionCookieOf(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == httpx.SessionCookie && c.Value != "" {
			return c.Name + "=" + c.Value
		}
	}
	t.Fatalf("no session cookie: %v", w.Header())
	return ""
}

func codeOf(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	return linkErrorCode(t, w.Body.String())
}

// alive says whether a client's session still works on an ordinary read.
func alive(c *client) bool {
	return c.do(http.MethodGet, "/api/v1/dashboard/hub", "", nil).Code == 200
}

func (e *signInEnv) audits(action string) []audit.Entry {
	e.t.Helper()
	rows, _, err := e.s.Audit.List(e.t.Context(), audit.Filter{ActionPrefixes: []string{action}, Limit: 200})
	if err != nil {
		e.t.Fatal(err)
	}
	var out []audit.Entry
	for _, r := range rows {
		if r.Action == action {
			out = append(out, r)
		}
	}
	return out
}

func TestAValidTicketOpensACompleteSessionForTheAccountThatOptedIn(t *testing.T) {
	e := newSignInEnv(t)
	w := e.redeem(e.ticket(e.hub.PrivateKey(), nil), "", "")
	if w.Code != 200 {
		t.Fatalf("redeem = %d %s", w.Code, w.Body)
	}
	body := decodeMap(t, w.Body.String())
	if body["authenticated"] != true || body["needsTotp"] != false || body["next"] != "/dashboard" {
		t.Fatalf("answer = %s", w.Body)
	}
	c := &client{t: t, h: e.h, cookie: sessionCookieOf(t, w)}
	// The account has an authenticator, and the session is complete anyway:
	// the hub's own code, under its pinned key, stood in for it.
	session := decodeMap(t, c.do(http.MethodGet, "/api/v1/auth/session", "", nil).Body.String())
	if session["authenticated"] != true || session["needsTotp"] != false {
		t.Fatalf("session = %v", session)
	}
	sessions, err := e.s.Auth.ListSessions(t.Context(), e.adminID, "")
	if err != nil {
		t.Fatal(err)
	}
	var hubSessions []*auth.Session
	for _, sess := range sessions {
		if sess.AuthMethod == auth.MethodHub {
			hubSessions = append(hubSessions, sess)
		}
	}
	if len(hubSessions) != 1 || hubSessions[0].HubUser != "owner" || !hubSessions[0].TwoFAPassed ||
		hubSessions[0].ExpiresAt.Sub(hubSessions[0].CreatedAt) > auth.HubSessionTTL {
		t.Fatalf("hub sessions = %+v", hubSessions)
	}
	rows := e.audits("hub.signin")
	if len(rows) != 1 || !rows[0].Success || rows[0].Username != "ion" || rows[0].Actor != "hub:hub" ||
		!strings.Contains(rows[0].Detail, `"deviceCheck":"skipped"`) || !strings.Contains(rows[0].Detail, `"sessionId"`) {
		t.Fatalf("audit = %+v", rows)
	}
}

func TestATicketIsRefusedAtTheStepThatFailsAndAudited(t *testing.T) {
	e := newSignInEnv(t)
	_, other, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	_, readerID, _ := hubAccount(t, e.s, "reader", auth.RoleReadOnly, false)
	cases := []struct {
		name, ticket, addr string
		status             int
		code               string
	}{
		{"expired", e.ticket(e.hub.PrivateKey(), func(p ticketFields) {
			p["iat"], p["exp"], p["mfaAt"] = time.Now().Unix()-200, time.Now().Unix()-150, time.Now().Unix()-300
		}), "", 401, "ticket_expired"},
		{"wrong key", e.ticket(other, nil), "", 401, "bad_ticket"},
		{"wrong server", e.ticket(e.hub.PrivateKey(), func(p ticketFields) { p["serverId"] = "5b1f9a20-0000-4000-8000-000000000001" }), "", 401, "wrong_server"},
		{"another hub", e.ticket(e.hub.PrivateKey(), func(p ticketFields) { p["hub"] = "https://hub.tailed39ba.ts.net" }), "", 401, "wrong_server"},
		{"second factor too old", e.ticket(e.hub.PrivateKey(), func(p ticketFields) { p["mfaAt"] = time.Now().Unix() - 601 }), "", 401, "mfa_stale"},
		{"another device", e.ticket(e.hub.PrivateKey(), nil), "100.101.102.104", 401, "wrong_device"},
		{"not on the tailnet", e.ticket(e.hub.PrivateKey(), nil), "100.64.9.9", 401, "wrong_device"},
		// Another account, which never opted in.
		{"wrong user", e.ticket(e.hub.PrivateKey(), func(p ticketFields) { p["userId"] = readerID }), "", 403, "not_permitted"},
		// The right account, but a hub user it did not choose.
		{"wrong hub user", e.ticket(e.hub.PrivateKey(), func(p ticketFields) { p["hubUser"] = "mallory" }), "", 403, "not_permitted"},
		{"garbage", "v1.hub-2026-11.not-a-ticket", "", 401, "bad_ticket"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := e.redeem(c.ticket, c.addr, "")
			if w.Code != c.status || codeOf(t, w) != c.code {
				t.Fatalf("redeem = %d %s, want %d %s", w.Code, w.Body, c.status, c.code)
			}
			if len(w.Result().Cookies()) != 0 {
				t.Fatalf("a refusal set a cookie: %v", w.Result().Cookies())
			}
		})
	}
	failures := e.audits("hub.signin")
	if len(failures) != len(cases) {
		t.Fatalf("%d hub.signin entries for %d refusals", len(failures), len(cases))
	}
	// Each entry records the status the redeem answered, as every audited
	// request does: 403 for a refusal at the grant, 401 for one at the ticket.
	answered := map[string]int{}
	for _, c := range cases {
		answered[c.code] = c.status
	}
	for _, f := range failures {
		code, _ := decodeMap(t, f.Detail)["code"].(string)
		if f.Success || code == "" || f.Status != answered[code] {
			t.Fatalf("refusal audit = %+v", f)
		}
	}
}

func TestATicketOpensOneSessionOnlyAndTheRightDeviceIsChecked(t *testing.T) {
	e := newSignInEnv(t)
	ticket := e.ticket(e.hub.PrivateKey(), nil)
	// From another tailnet login it is refused and not spent.
	if w := e.redeem(ticket, "100.101.102.104", ""); codeOf(t, w) != "wrong_device" {
		t.Fatalf("wrong device = %d %s", w.Code, w.Body)
	}
	// From the device that asked the hub, its login read case-blind.
	w := e.redeem(ticket, "100.101.102.103", "")
	if w.Code != 200 {
		t.Fatalf("right device = %d %s", w.Code, w.Body)
	}
	rows := e.audits("hub.signin")
	if !strings.Contains(rows[0].Detail, `"deviceCheck":"checked"`) {
		t.Fatalf("audit = %s", rows[0].Detail)
	}
	// Replayed: refused, from anywhere.
	if w := e.redeem(ticket, "100.101.102.103", ""); w.Code != 401 || codeOf(t, w) != "ticket_used" {
		t.Fatalf("replay = %d %s", w.Code, w.Body)
	}
}

func TestSignInNeedsTheSwitchTheLinkAndTheGrant(t *testing.T) {
	t.Run("switched off", func(t *testing.T) {
		e := newSignInEnv(t)
		ticket := e.ticket(e.hub.PrivateKey(), nil)
		e.s.modules.hubLink = hublink.New(hublink.Options{
			Store: e.s.Store, Sealer: e.s.Sealer, Audit: e.s.Audit, Enabled: true, SignIn: false, Auth: e.s.Auth,
			Version: "test", Tailnet: stubTailnet{}, WhoIs: signInWhoIs{},
		})
		h := e.s.Routes()
		e.h = h
		if w := e.redeem(ticket, "", ""); w.Code != 403 || codeOf(t, w) != "hub_signin_disabled" {
			t.Fatalf("switched off = %d %s", w.Code, w.Body)
		}
		rows := e.audits("hub.signin")
		if len(rows) != 1 || rows[0].Success || rows[0].Status != http.StatusForbidden || rows[0].Actor != "anonymous" ||
			rows[0].Detail != `{"code":"hub_signin_disabled"}` {
			t.Fatalf("a probe of a server with sign-in off is audited with only its code and its 403: %+v", rows)
		}
	})
	t.Run("no link", func(t *testing.T) {
		e := newSignInEnv(t)
		ticket := e.ticket(e.hub.PrivateKey(), nil)
		if w := e.admin.do(http.MethodDelete, "/api/v1/dashboard/hub", "", nil); w.Code != 200 {
			t.Fatalf("disconnect = %d %s", w.Code, w.Body)
		}
		if w := e.redeem(ticket, "", ""); w.Code != 403 || codeOf(t, w) != "hub_not_linked" {
			t.Fatalf("no link = %d %s", w.Code, w.Body)
		}
	})
	t.Run("idle link", func(t *testing.T) {
		e := newSignInEnv(t)
		ticket := e.ticket(e.hub.PrivateKey(), nil)
		if _, err := e.s.Store.DB.Exec(`UPDATE settings SET value = json_set(value, '$.idle',
			json_object('code', 'hub_url_refused', 'reason', 'the hub''s machine changed', 'at', '2026-11-05T00:00:00Z'))
			WHERE key = 'hub.link.state'`); err != nil {
			t.Fatal(err)
		}
		if w := e.redeem(ticket, "", ""); w.Code != 403 || codeOf(t, w) != "hub_not_linked" {
			t.Fatalf("idle link = %d %s", w.Code, w.Body)
		}
		// Nor can an account opt in while it is idle.
		if w := e.admin.do(http.MethodPut, "/api/v1/account/hub-signin",
			`{"hubUser":"owner","password":"`+hubPassword+`","code":"000000"}`, nil); w.Code != http.StatusConflict {
			t.Fatalf("opting in on an idle link = %d %s", w.Code, w.Body)
		}
	})
	t.Run("not opted in", func(t *testing.T) {
		e := newSignInEnv(t)
		if w := e.admin.do(http.MethodDelete, "/api/v1/account/hub-signin", "", nil); w.Code != 204 {
			t.Fatalf("opt out = %d %s", w.Code, w.Body)
		}
		if w := e.redeem(e.ticket(e.hub.PrivateKey(), nil), "", ""); w.Code != 403 || codeOf(t, w) != "not_permitted" {
			t.Fatalf("no grant = %d %s", w.Code, w.Body)
		}
	})
	t.Run("account unavailable", func(t *testing.T) {
		e := newSignInEnv(t)
		if _, err := e.s.Store.DB.Exec(`UPDATE users SET locked_until = ? WHERE id = ?`, time.Now().Add(time.Hour).Unix(), e.adminID); err != nil {
			t.Fatal(err)
		}
		if w := e.redeem(e.ticket(e.hub.PrivateKey(), nil), "", ""); w.Code != 403 || codeOf(t, w) != "account_unavailable" {
			t.Fatalf("locked = %d %s", w.Code, w.Body)
		}
	})
}

// A disconnect that lands between the nonce and the session leaves no session
// (section 9.7, Transactions).
func TestADisconnectBetweenTheNonceAndTheSessionLeavesNoSession(t *testing.T) {
	e := newSignInEnv(t)
	hublink.SetAfterNonce(e.s.modules.hubLink, func() {
		if _, err := e.s.modules.hubLink.Disconnect(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if w := e.redeem(e.ticket(e.hub.PrivateKey(), nil), "", ""); w.Code != 403 || codeOf(t, w) != "hub_not_linked" {
		t.Fatalf("redeem = %d %s", w.Code, w.Body)
	}
	var n int
	if err := e.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM sessions WHERE auth_method = 'hub'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("hub sessions = %d, %v", n, err)
	}
}

// A disconnect that starts after the session row is written, while the
// transaction of steps 12 to 14 is still open, waits for it to commit and
// then revokes what it made: no live hub session is left either way (section
// 9.7, "Transactions").
func TestADisconnectBetweenTheSessionAndTheCommitLeavesNoSession(t *testing.T) {
	e := newSignInEnv(t)
	done := make(chan error, 1)
	hublink.SetBeforeSessionCommit(e.s.modules.hubLink, func() {
		go func() {
			_, err := e.s.modules.hubLink.Disconnect(context.Background())
			done <- err
		}()
		// Long enough for the disconnect to reach its own write transaction
		// and wait there; it cannot commit before this one does.
		time.Sleep(200 * time.Millisecond)
	})
	w := e.redeem(e.ticket(e.hub.PrivateKey(), nil), "", "")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var n int
	if err := e.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM sessions WHERE auth_method = 'hub'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("hub sessions = %d, %v", n, err)
	}
	// The redeem committed first, so it answered with a session; that
	// session is the one the disconnect revoked.
	if w.Code != 200 {
		t.Fatalf("redeem = %d %s", w.Code, w.Body)
	}
	if hub := (&client{t: t, h: e.h, cookie: sessionCookieOf(t, w)}); alive(hub) {
		t.Fatal("the session the redeem made still works after the disconnect")
	}
}

// The link going idle between the nonce and the session leaves no session
// either: the session's transaction reads the idle flag (sections 2.4, 9.7).
func TestTheLinkGoingIdleBetweenTheNonceAndTheSessionLeavesNoSession(t *testing.T) {
	e := newSignInEnv(t)
	hublink.SetAfterNonce(e.s.modules.hubLink, func() {
		if _, err := e.s.Store.DB.Exec(`UPDATE settings SET value = json_set(value, '$.idle',
			json_object('code', 'hub_url_refused', 'reason', 'the hub''s machine changed', 'at', '2026-11-05T00:00:00Z'))
			WHERE key = 'hub.link.state'`); err != nil {
			t.Error(err)
		}
	})
	if w := e.redeem(e.ticket(e.hub.PrivateKey(), nil), "", ""); w.Code != 403 || codeOf(t, w) != "hub_not_linked" {
		t.Fatalf("redeem = %d %s", w.Code, w.Body)
	}
	var n int
	if err := e.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM sessions WHERE auth_method = 'hub'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("hub sessions = %d, %v", n, err)
	}
}

// grantEnd is one event of section 9.3 that ends a grant or the link.
// unannounced is an event no revocation of this process follows, which a hub
// session's open socket learns of at its next check rather than at once.
type grantEnd struct {
	name        string
	fire        func(e *signInEnv)
	unannounced bool
}

// grantEnds is every event of section 9.3 that ends a grant, or the link.
func grantEnds() []grantEnd {
	return []grantEnd{
		{name: "the account removes its grant", fire: func(e *signInEnv) {
			e.admin.do(http.MethodDelete, "/api/v1/account/hub-signin", "", nil)
		}},
		{name: "an administrator removes it", fire: func(e *signInEnv) {
			other, _, _ := hubAccount(e.t, e.s, "second-admin", auth.RoleAdmin, false)
			if w := other.do(http.MethodDelete, fmt.Sprintf("/api/v1/dashboard/hub/signin/%d", e.adminID), "", nil); w.Code != 204 {
				e.t.Fatalf("remove = %d %s", w.Code, w.Body)
			}
		}},
		{name: "the account replaces it with the same hub user", fire: func(e *signInEnv) {
			var secret string
			e.s.Store.DB.QueryRow(`SELECT totp_secret FROM users WHERE id = ?`, e.adminID).Scan(&secret)
			plain, err := e.s.Sealer.Open(secret)
			if err != nil {
				e.t.Fatal(err)
			}
			// The next step's code was spent opting in; a test cannot wait
			// for the one after, so the record of it is put back.
			e.s.Store.DB.Exec(`UPDATE users SET totp_last_step = 0 WHERE id = ?`, e.adminID)
			e.allow(e.admin, "owner", hubPassword, nextCode(e.t, plain, 1), http.StatusNoContent)
		}},
		{name: "the link is disconnected", fire: func(e *signInEnv) {
			e.admin.do(http.MethodDelete, "/api/v1/dashboard/hub", "", nil)
		}},
		{name: "the account is disabled", fire: func(e *signInEnv) {
			other, _, _ := hubAccount(e.t, e.s, "second-admin", auth.RoleAdmin, false)
			other.do(http.MethodPatch, fmt.Sprintf("/api/v1/dashboard-users/%d", e.adminID), `{"disabled":true}`, nil)
		}},
		{name: "the account is deleted", fire: func(e *signInEnv) {
			hubAccount(e.t, e.s, "second-admin", auth.RoleAdmin, false)
			if err := e.s.Auth.DeleteUser(e.t.Context(), e.adminID); err != nil {
				e.t.Fatal(err)
			}
		}},
		{name: "its password changes", fire: func(e *signInEnv) {
			e.admin.do(http.MethodPost, "/api/v1/account/password",
				`{"currentPassword":"`+hubPassword+`","newPassword":"Another-Horse-Battery-10"}`, nil)
		}},
		{name: "its password is reset", fire: func(e *signInEnv) {
			if err := e.s.Auth.ResetPassword(e.t.Context(), e.adminID, "Another-Horse-Battery-10"); err != nil {
				e.t.Fatal(err)
			}
		}},
		{name: "its authenticator is reset by an administrator", fire: func(e *signInEnv) {
			other, _, _ := hubAccount(e.t, e.s, "second-admin", auth.RoleAdmin, false)
			other.do(http.MethodPost, fmt.Sprintf("/api/v1/dashboard-users/%d/reset-totp", e.adminID), "", nil)
		}},
		{name: "its authenticator is turned off", fire: func(e *signInEnv) {
			e.admin.do(http.MethodPost, "/api/v1/account/2fa/disable", `{"password":"`+hubPassword+`"}`, nil)
		}},
		{name: "its sessions are all revoked", fire: func(e *signInEnv) {
			if err := e.s.Auth.RevokeAllSessions(e.t.Context(), e.adminID); err != nil {
				e.t.Fatal(err)
			}
		}},
		{name: "the hub stops offering its hub user", fire: func(e *signInEnv) {
			e.hub.OfferSignIn("someone-else")
			e.sync()
		}},
		{name: "the hub stops offering sign-in", fire: func(e *signInEnv) {
			e.hub.OfferSignIn()
			e.sync()
		}},
		{name: "hub sign-in is found off at start", unannounced: true, fire: func(e *signInEnv) {
			off := hublink.New(hublink.Options{
				Store: e.s.Store, Sealer: e.s.Sealer, Audit: e.s.Audit, Enabled: true, SignIn: false, Auth: e.s.Auth,
				Version: "test", Tailnet: stubTailnet{}, WhoIs: signInWhoIs{},
			})
			if err := off.PrepareSignIn(e.t.Context()); err != nil {
				e.t.Fatal(err)
			}
			var grants, sessions int
			e.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM hub_signin_grants`).Scan(&grants)
			e.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM sessions WHERE auth_method = 'hub'`).Scan(&sessions)
			if grants != 0 || sessions != 0 {
				e.t.Fatalf("after the start clean-up: %d grants, %d hub sessions", grants, sessions)
			}
		}},
		// No revocation at all: the row is simply gone. The session is still
		// refused on its next use (section 9.3).
		{name: "the grant row disappears without a revocation", unannounced: true, fire: func(e *signInEnv) {
			if _, err := e.s.Store.DB.Exec(`DELETE FROM hub_signin_grants`); err != nil {
				e.t.Fatal(err)
			}
		}},
		// Nor here: the idle flag is written into the stored link with no
		// session revoked beside it, as a restart finds a link that went idle
		// before (sections 2.4 and 9.3).
		{name: "the link is idle in the store without a revocation", unannounced: true, fire: func(e *signInEnv) {
			if _, err := e.s.Store.DB.Exec(`UPDATE settings SET value = json_set(value, '$.idle',
				json_object('code', 'hub_url_refused', 'reason', 'the hub''s machine changed', 'at', '2026-11-05T00:00:00Z'))
				WHERE key = 'hub.link.state'`); err != nil {
				e.t.Fatal(err)
			}
		}},
	}
}

// Every event of section 9.3 that ends a grant, or the link, ends the hub
// sessions it allowed: a session made before it is refused after it.
func TestEveryEndOfAGrantEndsItsHubSessions(t *testing.T) {
	for _, ev := range grantEnds() {
		t.Run(ev.name, func(t *testing.T) {
			e := newSignInEnv(t)
			hubSession := e.enter()
			if !alive(hubSession) {
				t.Fatal("the hub session does not work before the event")
			}
			ev.fire(e)
			if alive(hubSession) {
				t.Fatalf("the hub session still works after %s", ev.name)
			}
			w := hubSession.do(http.MethodGet, "/api/v1/dashboard/hub", "", nil)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("after %s: %d %s, want 401", ev.name, w.Code, w.Body)
			}
		})
	}
}

// A grant made without an authenticator ends when the account enrols one: it
// must not stand in for the code the account now requires.
func TestEnrollingAnAuthenticatorEndsAGrantMadeWithoutOne(t *testing.T) {
	e := newSignInEnv(t)
	plain, plainID, _ := hubAccount(t, e.s, "plain", auth.RoleAdmin, false)
	e.allow(plain, "owner2", hubPassword, "", http.StatusBadRequest)
	e.hub.OfferSignIn("owner", "owner2")
	e.sync()
	e.allow(plain, "owner2", hubPassword, "", http.StatusNoContent)
	w := e.redeem(e.ticket(e.hub.PrivateKey(), func(p ticketFields) { p["userId"], p["hubUser"] = plainID, "owner2" }), "", "")
	if w.Code != 200 {
		t.Fatalf("redeem = %d %s", w.Code, w.Body)
	}
	hubSession := &client{t: t, h: e.h, cookie: sessionCookieOf(t, w)}
	setup := decodeMap(t, plain.do(http.MethodPost, "/api/v1/auth/2fa/setup", "", nil).Body.String())
	secret, _ := setup["secret"].(string)
	code, _ := totp.GenerateCode(secret, time.Now())
	if w := plain.do(http.MethodPost, "/api/v1/auth/2fa/enable", `{"code":"`+code+`"}`, nil); w.Code != 200 {
		t.Fatalf("enable = %d %s", w.Code, w.Body)
	}
	if alive(hubSession) {
		t.Fatal("a hub session made without a code survived enrolling an authenticator")
	}
	var n int
	e.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM hub_signin_grants WHERE user_id = ?`, plainID).Scan(&n)
	if n != 0 {
		t.Fatal("the grant survived enrolment")
	}
	ends := e.audits("hub.signin.revoke")
	if len(ends) == 0 || !strings.Contains(ends[0].Detail, `"reason":"authenticator_changed"`) {
		t.Fatalf("revoke audit = %+v", ends)
	}
}

func TestOptingInChecksThePasswordAndCodeAndOneHubUserPerAccount(t *testing.T) {
	e := newSignInEnv(t)
	var sealed string
	e.s.Store.DB.QueryRow(`SELECT totp_secret FROM users WHERE id = ?`, e.adminID).Scan(&sealed)
	secret, _ := e.s.Sealer.Open(sealed)
	if w := e.allow(e.admin, "owner", "wrong", nextCode(t, secret, 2), 401); codeOf(t, w) != "invalid_credentials" {
		t.Fatalf("wrong password = %s", w.Body)
	}
	if w := e.allow(e.admin, "owner", hubPassword, "000000", 401); codeOf(t, w) != "invalid_code" {
		t.Fatalf("wrong code = %s", w.Body)
	}
	// The code that made the grant in newSignInEnv is used up.
	if w := e.allow(e.admin, "owner", hubPassword, nextCode(t, secret, 1), 401); codeOf(t, w) != "invalid_code" {
		t.Fatalf("reused code = %s", w.Body)
	}
	if w := e.allow(e.admin, "nobody", hubPassword, nextCode(t, secret, 2), 400); codeOf(t, w) != "bad_request" {
		t.Fatalf("hub user not offered = %s", w.Body)
	}
	other, _, otherSecret := hubAccount(t, e.s, "other", auth.RoleAdmin, true)
	if w := e.allow(other, "owner", hubPassword, nextCode(t, otherSecret, 1), 409); codeOf(t, w) != "hub_user_taken" {
		t.Fatalf("taken = %s", w.Body)
	}
	// A hub session may remove its own grant but never make one.
	hubSession := e.enter()
	if w := e.allow(hubSession, "owner", hubPassword, nextCode(t, secret, 2), 403); codeOf(t, w) != "hub_session_not_allowed" {
		t.Fatalf("hub session opting in = %s", w.Body)
	}
	// The heartbeat tells the hub who opted in, at once.
	e.sync()
	beat := e.hub.LastHeartbeat()
	signIn, _ := beat["signIn"].(map[string]any)
	grants, _ := signIn["grants"].([]any)
	if len(grants) != 1 || grants[0].(map[string]any)["hubUser"] != "owner" || grants[0].(map[string]any)["username"] != "ion" {
		t.Fatalf("heartbeat signIn = %v", beat["signIn"])
	}
	status := decodeMap(t, e.admin.do(http.MethodGet, "/api/v1/dashboard/hub", "", nil).Body.String())
	si, _ := status["signIn"].(map[string]any)
	mine, _ := si["mine"].(map[string]any)
	admin, _ := status["admin"].(map[string]any)
	if si["enabled"] != true || si["offered"] != true || mine["hubUser"] != "owner" || len(admin["grants"].([]any)) != 1 {
		t.Fatalf("status = %v", status)
	}
}

// A session a ticket made is refused on every route that would leave a
// credential or a link behind it, and nowhere else it is not refused anyway.
func TestAHubSessionCannotMakeCredentialsOrLinks(t *testing.T) {
	e := newSignInEnv(t)
	hubSession := e.enter()
	for _, rt := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/tokens/", `{"name":"k","role":"admin"}`},
		{http.MethodPost, "/api/v1/dashboard-users/", `{"username":"x","password":"Correct-Horse-Battery-9","role":"admin"}`},
		{http.MethodPost, "/api/v1/account/password", `{"currentPassword":"` + hubPassword + `","newPassword":"Another-Horse-Battery-10"}`},
		{http.MethodPost, "/api/v1/auth/2fa/setup", ``},
		{http.MethodPut, "/api/v1/dashboard/hub/scope", `{"scope":"observe"}`},
		{http.MethodPost, "/api/v1/dashboard/link/", `{"scope":"observe"}`},
	} {
		w := hubSession.do(rt.method, rt.path, rt.body, nil)
		if w.Code != 403 || codeOf(t, w) != "hub_session_not_allowed" {
			t.Errorf("%s %s with a hub session = %d %s", rt.method, rt.path, w.Code, w.Body)
		}
	}
	// The same account's password session is not refused.
	for _, rt := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/tokens/", `{"name":"k","role":"admin"}`},
		{http.MethodPost, "/api/v1/dashboard-users/", `{"username":"x","password":"Correct-Horse-Battery-9","role":"admin"}`},
	} {
		if w := e.admin.do(rt.method, rt.path, rt.body, nil); w.Code >= 300 {
			t.Errorf("%s %s with a password session = %d %s", rt.method, rt.path, w.Code, w.Body)
		}
	}
	// What only takes power away stays open.
	if w := hubSession.do(http.MethodPost, "/api/v1/dashboard/hub/sync", "", nil); w.Code != 200 {
		t.Errorf("sync with a hub session = %d %s", w.Code, w.Body)
	}
	// A code given from a hub session does not turn it into an ordinary one.
	var sealed string
	e.s.Store.DB.QueryRow(`SELECT totp_secret FROM users WHERE id = ?`, e.adminID).Scan(&sealed)
	secret, _ := e.s.Sealer.Open(sealed)
	e.s.Store.DB.Exec(`UPDATE users SET totp_last_step = 0 WHERE id = ?`, e.adminID)
	hubSession.do(http.MethodPost, "/api/v1/auth/2fa/verify", `{"code":"`+nextCode(t, secret, 0)+`"}`, nil)
	var method string
	e.s.Store.DB.QueryRow(`SELECT auth_method FROM sessions WHERE user_id = ? AND hub_user = 'owner'`, e.adminID).Scan(&method)
	if method != auth.MethodHub {
		t.Fatalf("a code turned the hub session into %q", method)
	}
	if w := hubSession.do(http.MethodPost, "/api/v1/tokens/", `{"name":"k","role":"admin"}`, nil); codeOf(t, w) != "hub_session_not_allowed" {
		t.Fatalf("after a code the hub session made a key: %d %s", w.Code, w.Body)
	}
}

// Every route of the refused list answers a hub session with 403
// hub_session_not_allowed, whatever it would have done with the body: the
// walk proves the middleware is there, this that nothing before it answers
// first (section 9.9).
func TestEveryRefusedRouteAnswersAHubSessionWith403(t *testing.T) {
	e := newSignInEnv(t)
	hubSession := e.enter()
	params := regexp.MustCompile(`\{[^}]+\}`)
	for _, rt := range hubSessionRefused {
		method, pattern, _ := strings.Cut(rt, " ")
		w := hubSession.do(method, params.ReplaceAllString(pattern, "x1"), `{}`, nil)
		if w.Code != http.StatusForbidden || codeOf(t, w) != "hub_session_not_allowed" {
			t.Errorf("%s with a hub session = %d %s", rt, w.Code, w.Body)
		}
	}
	if !alive(hubSession) {
		t.Fatal("being refused ended the hub session")
	}
}

func TestAHubSessionEndsAfterTwoHours(t *testing.T) {
	e := newSignInEnv(t)
	hubSession := e.enter()
	var created, expires int64
	e.s.Store.DB.QueryRow(`SELECT created_at, expires_at FROM sessions WHERE auth_method = 'hub'`).Scan(&created, &expires)
	if expires-created > int64(auth.HubSessionTTL/time.Second) {
		t.Fatalf("a hub session lasts %ds", expires-created)
	}
	// Two hours and a second later.
	e.s.Store.DB.Exec(`UPDATE sessions SET expires_at = ?, last_seen_at = ? WHERE auth_method = 'hub'`,
		time.Now().Unix()-1, time.Now().Unix())
	if alive(hubSession) {
		t.Fatal("a hub session outlived its two hours")
	}
}

// Redeeming in a browser that holds another session replaces it: one browser,
// one session. Another account's complete session is ended only when the page
// asked and the person chose to continue ("replace", section 9.5 step 2);
// without that the redeem is refused and both stay as they were.
func TestRedeemingReplacesTheBrowsersSession(t *testing.T) {
	e := newSignInEnv(t)
	other, _, _ := hubAccount(t, e.s, "someone", auth.RoleReadOnly, false)
	w := e.redeem(e.ticket(e.hub.PrivateKey(), nil), "", other.cookie)
	if w.Code != http.StatusConflict || codeOf(t, w) != "other_account_signed_in" {
		t.Fatalf("redeem over another account's session, not asked = %d %s", w.Code, w.Body)
	}
	if !alive(other) {
		t.Fatal("a refused redeem ended the browser's session")
	}
	if rows := e.audits("hub.signin"); len(rows) != 1 || rows[0].Success || rows[0].Status != http.StatusConflict {
		t.Fatalf("the refusal's audit entry = %+v", rows)
	}
	var n int
	e.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM sessions WHERE auth_method = 'hub'`).Scan(&n)
	if n != 0 {
		t.Fatalf("a refused redeem left %d hub sessions", n)
	}

	w = e.redeemBody(map[string]any{"ticket": e.ticket(e.hub.PrivateKey(), nil), "replace": true}, "", other.cookie)
	if w.Code != 200 {
		t.Fatalf("redeem after Continue = %d %s", w.Code, w.Body)
	}
	if alive(other) {
		t.Fatal("the browser's earlier session survived")
	}

	// The same account's own session, and a session still owing its second
	// factor, are replaced without a question.
	same := &client{t: t, h: e.h, cookie: sessionCookieOf(t, w)}
	w = e.redeem(e.ticket(e.hub.PrivateKey(), nil), "", same.cookie)
	if w.Code != 200 || alive(same) {
		t.Fatalf("redeem over the same account's session = %d %s", w.Code, w.Body)
	}
	partial, _, _ := hubAccount(t, e.s, "partial", auth.RoleReadOnly, false)
	if _, err := e.s.Store.DB.Exec(`UPDATE sessions SET twofa_passed = 0 WHERE token_hash = ?`,
		auth.HashToken(strings.TrimPrefix(partial.cookie, httpx.SessionCookie+"="))); err != nil {
		t.Fatal(err)
	}
	if w := e.redeem(e.ticket(e.hub.PrivateKey(), nil), "", partial.cookie); w.Code != 200 {
		t.Fatalf("redeem over a half-signed-in session = %d %s", w.Code, w.Body)
	}
}
