package hublink

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accountlink"
	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store/storetest"
	"github.com/Wayy01/Just-Dashboard/backend/internal/tailnetgate"
)

// The keys, ids and time the protocol's vectors are made with.
const (
	vectorServerSeed = "9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60"
	vectorHubSeed    = "4ccd089b28ff96da9db6c346ec114e0f5b8a319f35aba624da8cf6ed4fb8a6fb"
	vectorOtherSeed  = "c5aa8df43f9f837bedb7442f31dcb7b166d38535076f094b85ce3a2e0b4458f7"
	vectorServerID   = "7a0d3c1e-0000-4000-8000-000000000002"
	vectorKid        = "hub-2026-11"
	vectorNow        = int64(1761350400)
)

func seedKey(t testing.TB, seedHex string) ed25519.PrivateKey {
	t.Helper()
	seed, err := hex.DecodeString(seedHex)
	if err != nil {
		t.Fatal(err)
	}
	return ed25519.NewKeyFromSeed(seed)
}

func pubB64(k ed25519.PrivateKey) string {
	return base64.StdEncoding.EncodeToString(k.Public().(ed25519.PublicKey))
}

// signedString is the fake hub's own reading of protocol section 3.1, written
// apart from the dashboard's so the two can disagree; the vectors test checks
// it too.
func signedString(method, pathWithQuery string, body []byte, unixTime, sequence int64) string {
	sum := sha256.Sum256(body)
	return method + "\n" + pathWithQuery + "\n" + hex.EncodeToString(sum[:]) + "\n" +
		strconv.FormatInt(unixTime, 10) + "\n" + strconv.FormatInt(sequence, 10)
}

// ------------------------------------------------------------------ the hub --

type hubRequest struct {
	id, code string
	pub      ed25519.PublicKey
	scope    string
	body     map[string]any
	status   string // pending, approved, collected, denied, expired, consumed
	seq      int64
}

type hubServer struct {
	id      string
	pub     ed25519.PublicKey
	seq     int64
	removed bool
}

// fakeHub behaves like Just Hub as far as the protocol goes: it checks the
// protocol header, the signature, the time window and the sequence of every
// signed request in section 3.1's order, keeps request ids and server ids
// apart, hands out commands signed with its own key, and answers 410 once a
// server is removed.
type fakeHub struct {
	t   *testing.T
	srv *httptest.Server
	key ed25519.PrivateKey
	kid string

	mu         sync.Mutex
	now        time.Time
	serverID   string
	label      string
	requests   map[string]*hubRequest
	servers    map[string]*hubServer
	tokens     map[string]bool
	heartbeats [][]byte
	queue      []accountlink.Command
	results    map[string]accountlink.CommandResult
	nextID     int
	headers    []http.Header
	// reply adds to or replaces fields of the heartbeat reply.
	reply map[string]any
	// hold, when set, keeps the next heartbeat's answer until it is closed;
	// arrived is closed once that heartbeat has been accepted.
	hold, arrived chan struct{}
	// origin is where the dashboard thinks the hub is: its own URL, or a
	// tailnet name a test transport brings here.
	origin string
	// polls counts the request polls that reached the hub.
	polls int
	// dropApproved loses that many approved answers in transit: the hub
	// collected the approval, the dashboard never heard.
	dropApproved int
}

func newFakeHub(t *testing.T, now time.Time) *fakeHub {
	h := &fakeHub{
		t: t, key: seedKey(t, vectorHubSeed), kid: vectorKid, now: now, label: "hub",
		serverID: vectorServerID, requests: map[string]*hubRequest{}, servers: map[string]*hubServer{},
		tokens: map[string]bool{}, results: map[string]accountlink.CommandResult{},
	}
	h.srv = httptest.NewServer(http.HandlerFunc(h.serve))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *fakeHub) URL() string { return h.srv.URL }

// Origin is the hub URL the dashboard links to.
func (h *fakeHub) Origin() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.origin != "" {
		return h.origin
	}
	return h.srv.URL
}

func (h *fakeHub) pollCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.polls
}

func (h *fakeHub) setNow(t time.Time) {
	h.mu.Lock()
	h.now = t
	h.mu.Unlock()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func hubError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": code}})
}

func (h *fakeHub) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.headers = append(h.headers, r.Header.Clone())
	if r.Header.Get("X-JD-Link-Protocol") != "1" {
		hubError(w, 400, "unsupported_protocol")
		return
	}
	path, ok := strings.CutPrefix(r.URL.Path, "/api/hub/v1")
	if !ok {
		hubError(w, 404, "not_found")
		return
	}
	switch {
	case r.Method == http.MethodPost && path == "/requests":
		h.createRequest(w, body)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/requests/"):
		h.pollRequest(w, r, body, strings.TrimPrefix(path, "/requests/"))
	default:
		h.signed(w, r, body, path)
	}
}

func (h *fakeHub) createRequest(w http.ResponseWriter, body []byte) {
	var in map[string]any
	if err := json.Unmarshal(body, &in); err != nil {
		hubError(w, 400, "bad_request")
		return
	}
	pub, err := base64.StdEncoding.DecodeString(fmt.Sprint(in["publicKey"]))
	if err != nil || len(pub) != 32 {
		hubError(w, 400, "bad_request")
		return
	}
	status := "pending"
	if tok, ok := in["token"].(string); ok {
		// Single use: the first request that names it consumes it.
		valid := h.tokens[tok]
		delete(h.tokens, tok)
		if !valid {
			hubError(w, 403, "bad_token")
			return
		}
		status = "approved"
	}
	h.nextID++
	id := fmt.Sprintf("2c9e41d7-0000-4000-8000-%012d", h.nextID)
	code := "K7QM-29XD"
	h.requests[id] = &hubRequest{id: id, code: code, pub: pub, scope: fmt.Sprint(in["scope"]), body: in, status: status}
	writeJSON(w, 200, map[string]any{
		"requestId": id, "userCode": code, "approveUrl": h.originLocked() + "/servers/add/?request=" + id,
		"expiresAt": h.now.Add(15 * time.Minute).Unix(), "pollSeconds": 2,
	})
}

// check is section 3.1's order: the id names something, the time is within
// a minute, the signature verifies, the sequence is new.
func (h *fakeHub) check(w http.ResponseWriter, r *http.Request, body []byte, pub ed25519.PublicKey, seq *int64) bool {
	ts, err1 := strconv.ParseInt(r.Header.Get("X-JD-Time"), 10, 64)
	n, err2 := strconv.ParseInt(r.Header.Get("X-JD-Sequence"), 10, 64)
	sig, err3 := base64.StdEncoding.DecodeString(r.Header.Get("X-JD-Signature"))
	if err1 != nil || err2 != nil || err3 != nil {
		hubError(w, 401, "bad_signature")
		return false
	}
	if d := h.now.Unix() - ts; d > 60 || d < -60 {
		hubError(w, 401, "stale_time")
		return false
	}
	if !ed25519.Verify(pub, []byte(signedString(r.Method, r.URL.RequestURI(), body, ts, n)), sig) {
		hubError(w, 401, "bad_signature")
		return false
	}
	if n <= *seq {
		hubError(w, 401, "replayed_sequence")
		return false
	}
	*seq = n
	return true
}

func (h *fakeHub) originLocked() string {
	if h.origin != "" {
		return h.origin
	}
	return h.srv.URL
}

func (h *fakeHub) pollRequest(w http.ResponseWriter, r *http.Request, body []byte, id string) {
	h.polls++
	req := h.requests[id]
	if req == nil || r.Header.Get("X-JD-Server") != id {
		hubError(w, 401, "unknown_server")
		return
	}
	if !h.check(w, r, body, req.pub, &req.seq) {
		return
	}
	switch req.status {
	case "pending":
		writeJSON(w, 200, map[string]any{"status": "pending"})
	case "approved", "collected":
		// The first approved poll collects the approval (protocol 4.2):
		// the server row starts with the request's sequence. Later polls
		// get the same answer until the window ends, so one lost in transit
		// heals on the next.
		if req.status == "approved" {
			req.status = "collected"
			h.servers[h.serverID] = &hubServer{id: h.serverID, pub: req.pub, seq: req.seq}
		} else if s := h.servers[h.serverID]; s != nil && s.seq < req.seq {
			s.seq = req.seq
		}
		if h.dropApproved > 0 {
			h.dropApproved--
			hubError(w, 503, "unavailable")
			return
		}
		writeJSON(w, 200, map[string]any{
			"status": "approved", "serverId": h.serverID,
			"siteKey":          map[string]string{"kid": h.kid, "publicKey": pubB64(h.key)},
			"heartbeatSeconds": 15, "pollWaitSeconds": 1, "owner": map[string]string{"label": h.label},
		})
	case "denied":
		hubError(w, 410, "request_denied")
	case "expired":
		hubError(w, 410, "request_expired")
	default:
		hubError(w, 410, "request_consumed")
	}
}

func (h *fakeHub) signed(w http.ResponseWriter, r *http.Request, body []byte, path string) {
	server := h.servers[r.Header.Get("X-JD-Server")]
	if server == nil {
		// A request id is never accepted here.
		hubError(w, 401, "unknown_server")
		return
	}
	if server.removed {
		hubError(w, 410, "server_removed")
		return
	}
	if !h.check(w, r, body, server.pub, &server.seq) {
		return
	}
	switch {
	case r.Method == http.MethodPost && path == "/heartbeat":
		h.heartbeats = append(h.heartbeats, append([]byte(nil), body...))
		if hold, arrived := h.hold, h.arrived; hold != nil {
			h.hold, h.arrived = nil, nil
			close(arrived)
			h.mu.Unlock()
			<-hold
			h.mu.Lock()
		}
		reply := map[string]any{
			"ok": true, "heartbeatSeconds": 15, "pendingCommands": len(h.queue),
			"fleet": []map[string]any{{
				"serverId": h.serverID, "name": "box", "endpoint": "https://box.tailed39ba.ts.net:8443",
				"state": "online", "version": "0.8.5", "self": true, "lastSeenAt": h.now.Unix(),
			}},
			"notices": []map[string]any{},
		}
		for k, v := range h.reply {
			if v == nil {
				delete(reply, k)
			} else {
				reply[k] = v
			}
		}
		writeJSON(w, 200, reply)
	case r.Method == http.MethodGet && path == "/commands":
		if r.URL.Query().Get("wait") == "" {
			hubError(w, 400, "bad_request")
			return
		}
		h.mu.Unlock()
		deadline := time.Now().Add(150 * time.Millisecond)
		for time.Now().Before(deadline) {
			h.mu.Lock()
			n := len(h.queue)
			h.mu.Unlock()
			if n > 0 {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		h.mu.Lock()
		out := h.queue
		h.queue = nil
		if out == nil {
			out = []accountlink.Command{}
		}
		writeJSON(w, 200, map[string]any{"commands": out})
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/commands/") && strings.HasSuffix(path, "/result"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/commands/"), "/result")
		var res accountlink.CommandResult
		if err := json.NewDecoder(bytes.NewReader(body)).Decode(&res); err != nil {
			hubError(w, 400, "bad_request")
			return
		}
		if _, dup := h.results[id]; dup {
			hubError(w, 404, "unknown_command")
			return
		}
		h.results[id] = res
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost && path == "/unlink":
		server.removed = true
		w.WriteHeader(http.StatusNoContent)
	default:
		hubError(w, 404, "not_found")
	}
}

func (h *fakeHub) approve(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.requests[id].status = "approved"
}

func (h *fakeHub) setRequestStatus(id, status string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.requests[id].status = status
}

func (h *fakeHub) request(id string) *hubRequest {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.requests[id]
}

func (h *fakeHub) requestCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.requests)
}

func (h *fakeHub) remove() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.servers[h.serverID].removed = true
}

func (h *fakeHub) removed() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.servers[h.serverID]
	return s != nil && s.removed
}

func (h *fakeHub) setReply(k string, v any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.reply == nil {
		h.reply = map[string]any{}
	}
	h.reply[k] = v
}

// holdNextHeartbeat keeps the next heartbeat's answer until release is
// called; arrived is closed once the hub has accepted that heartbeat.
func (h *fakeHub) holdNextHeartbeat() (arrived <-chan struct{}, release func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	hold, got := make(chan struct{}), make(chan struct{})
	h.hold, h.arrived = hold, got
	return got, func() { close(hold) }
}

func (h *fakeHub) addToken(tok string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tokens[tok] = true
}

func (h *fakeHub) heartbeatBodies() [][]byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([][]byte(nil), h.heartbeats...)
}

func (h *fakeHub) result(id string) (accountlink.CommandResult, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ok := h.results[id]
	return r, ok
}

func (h *fakeHub) lastHeaders() http.Header {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.headers[len(h.headers)-1]
}

// signCommand signs a command for serverID with key, the way the hub does.
func signCommand(t testing.TB, key ed25519.PrivateKey, kid, serverID, id, kind string, rawArgs string, expires int64) accountlink.Command {
	t.Helper()
	canonical, err := accountlink.CanonicalArgs(json.RawMessage(rawArgs))
	if err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(key, []byte(accountlink.CommandMessage(serverID, id, kind, canonical, expires)))
	return accountlink.Command{
		ID: id, Kind: kind, Args: json.RawMessage(rawArgs), IssuedBy: "owner", IssuedAt: expires - 600,
		ExpiresAt: expires, Kid: kid, Signature: base64.StdEncoding.EncodeToString(sig),
	}
}

// queue puts commands on the long poll.
func (h *fakeHub) queueCommands(cmds ...accountlink.Command) {
	h.mu.Lock()
	h.queue = append(h.queue, cmds...)
	h.mu.Unlock()
}

// command signs and queues one command for the linked server.
func (h *fakeHub) command(id, kind, rawArgs string, expires time.Time) accountlink.Command {
	c := signCommand(h.t, h.key, h.kid, h.serverID, id, kind, rawArgs, expires.Unix())
	h.queueCommands(c)
	return c
}

func ulid(n int) string { return fmt.Sprintf("01JB%022d", n) }

// ------------------------------------------------------- tailnet and whois --

type fakeTailnet struct {
	mu  sync.Mutex
	st  TailnetStatus
	err error
	n   int
}

func newFakeTailnet() *fakeTailnet {
	return &fakeTailnet{st: TailnetStatus{
		Running: true, Suffix: "tailed39ba.ts.net", Self: "box.tailed39ba.ts.net",
		Peers: map[string]netip.Addr{"hub.tailed39ba.ts.net": netip.MustParseAddr("100.101.102.103")},
	}}
}

func (f *fakeTailnet) Status(context.Context) (TailnetStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	return f.st, f.err
}

func (f *fakeTailnet) set(fn func(*TailnetStatus)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(&f.st)
}

func (f *fakeTailnet) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.n
}

// fakeWhoIs is tailscaled's WhoIs: the hub's machine is node, StableID id,
// owned by login (or tagged with tags), unless err says otherwise. Its fields
// are set before the connector runs, or through set.
type fakeWhoIs struct {
	mu    sync.Mutex
	node  string
	id    string
	login string
	tags  []string
	err   error
	asks  []netip.Addr
}

func (f *fakeWhoIs) WhoIs(_ context.Context, a netip.Addr) (tailnetgate.Peer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asks = append(f.asks, a)
	if f.err != nil {
		return tailnetgate.Peer{}, f.err
	}
	id := f.id
	if id == "" {
		id = "nHub1CNTRL"
	}
	tags := f.tags
	if tags == nil && f.login == "" {
		tags = []string{"tag:hub"}
	}
	return tailnetgate.Peer{Address: a, Node: f.node, StableID: id, Login: f.login, Tags: tags}, nil
}

func (f *fakeWhoIs) set(fn func(*fakeWhoIs)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *fakeWhoIs) asked() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.asks)
}

// ---------------------------------------------------------------- the host --

type hostCall struct {
	Method, Actor string
	Args          []any
}

type fakeHost struct {
	mu    sync.Mutex
	calls []hostCall
	fail  map[string]error
}

func (h *fakeHost) record(method, actor string, args ...any) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, hostCall{method, actor, args})
	return h.fail[method]
}

func (h *fakeHost) called() []hostCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]hostCall(nil), h.calls...)
}

func (h *fakeHost) Collect(context.Context) (accountlink.Snapshot, error) {
	return accountlink.Snapshot{
		Server: accountlink.ServerInfo{Name: "box", Version: "0.8.5"},
		Containers: &accountlink.ContainersInfo{Running: 1, List: []accountlink.ContainerInfo{
			{ID: "abc", Name: "shop-web", Image: "shop:1", State: "running"},
		}},
		Access: &accountlink.AccessInfo{Kind: "tailscale", Endpoint: "https://box.tailed39ba.ts.net:8443"},
	}, nil
}

func (h *fakeHost) UpdateStatus(context.Context) (accountlink.UpdateStatus, error) {
	return accountlink.UpdateStatus{Version: "0.8.5", Latest: "0.8.6", Available: true, Supported: true}, nil
}

func (h *fakeHost) Container(_ context.Context, actor, name, action string) (string, error) {
	return name + " " + action, h.record("Container", actor, name, action)
}

func (h *fakeHost) RestartStack(_ context.Context, actor, name string) (string, error) {
	return "restarted " + name, h.record("RestartStack", actor, name)
}

func (h *fakeHost) RunBackup(_ context.Context, actor string, id int64) (string, error) {
	return "backup started", h.record("RunBackup", actor, id)
}

func (h *fakeHost) InstallUpdate(_ context.Context, actor, version string) (string, error) {
	return "installing " + version, h.record("InstallUpdate", actor, version)
}

func (h *fakeHost) RestartDashboard(_ context.Context, actor string, rebuild bool) (string, error) {
	return "restarting", h.record("RestartDashboard", actor, rebuild)
}

// ----------------------------------------------------------------- the env --

// env is a hub link over a real store, reaching a fake hub at the test origin.
type env struct {
	t       *testing.T
	st      *store.Store
	sealer  *auth.Sealer
	audit   *audit.Logger
	hub     *fakeHub
	host    *fakeHost
	tailnet *fakeTailnet
	whois   *fakeWhoIs
	svc     *Service
	opts    Options

	mu  sync.Mutex
	now time.Time
}

func (e *env) clock() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.now
}

func (e *env) advance(d time.Duration) {
	e.mu.Lock()
	e.now = e.now.Add(d)
	e.mu.Unlock()
	e.hub.setNow(e.clock())
}

type envOption func(*Options)

func newEnv(t *testing.T, opts ...envOption) *env {
	t.Helper()
	return newEnvAt(t, false, opts...)
}

func newEnvAt(t *testing.T, tailnet bool, opts ...envOption) *env {
	t.Helper()
	st, err := storetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	sealer, err := auth.NewSealer(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	e := &env{
		t: t, st: st, sealer: sealer, now: time.Unix(vectorNow, 0).UTC(),
		host: &fakeHost{fail: map[string]error{}}, tailnet: newFakeTailnet(), whois: &fakeWhoIs{node: "hub.tailed39ba.ts.net."},
	}
	e.hub = newFakeHub(t, e.now)
	e.audit = audit.New(st, slog.New(slog.DiscardHandler))
	origin, err := ParseTestOrigin(e.hub.URL())
	if err != nil {
		t.Fatal(err)
	}
	o := Options{
		Store: st, Sealer: sealer, Audit: e.audit, Enabled: true, TestOrigin: origin, Host: e.host,
		Version: "0.8.5", Now: e.clock, Location: time.UTC, Tailnet: e.tailnet, WhoIs: e.whois,
		Describe: func(context.Context) accountlink.ServerDescription {
			return accountlink.ServerDescription{Name: "box", Hostname: "box", OS: "Ubuntu 24.04", Arch: "amd64"}
		},
		Endpoint: func(context.Context) string { return "https://box.tailed39ba.ts.net:8443" },
	}
	if tailnet {
		o.TestOrigin = ""
		o.HTTP = &http.Client{Transport: toFakeHub{e.hub}}
		e.hub.origin = tailnetOrigin
	}
	for _, f := range opts {
		f(&o)
	}
	e.opts = o
	e.svc = e.service(o)
	return e
}

// service makes a Service from o with the test's short waits, as the
// dashboard would on its next start over the same store.
func (e *env) service(o Options) *Service {
	svc := New(o)
	svc.Engine().SetTiming(accountlink.Timing{
		Idle: 10 * time.Millisecond, Watch: 10 * time.Millisecond,
		BackoffMin: 2 * time.Millisecond, BackoffMax: 10 * time.Millisecond,
		PolicyFirst: time.Hour, PolicyEvery: time.Hour, HeartbeatsPerMinute: 1 << 20,
	})
	// The system resolver knows no tailnet name here: the address is the one
	// tailscale status lists.
	svc.dial.lookup = func(context.Context, string) ([]netip.Addr, error) { return nil, errors.New("no such host") }
	e.t.Cleanup(svc.Stop)
	return svc
}

// restart stops the running Service and makes a new one over the same store,
// with the env's options changed by opts.
func (e *env) restart(opts ...envOption) *Service {
	e.svc.Stop()
	o := e.opts
	for _, f := range opts {
		f(&o)
	}
	e.svc = e.service(o)
	return e.svc
}

// toFakeHub carries each request for the hub's tailnet name to the fake hub,
// path, headers and signature unchanged.
type toFakeHub struct{ hub *fakeHub }

func (t toFakeHub) RoundTrip(r *http.Request) (*http.Response, error) {
	to := r.Clone(r.Context())
	to.URL.Scheme, to.URL.Host = "http", strings.TrimPrefix(t.hub.URL(), "http://")
	to.Host = to.URL.Host
	return http.DefaultTransport.RoundTrip(to)
}

// tailnetOrigin is where newTailnetEnv's hub is.
const tailnetOrigin = "https://hub.tailed39ba.ts.net"

// newTailnetEnv is newEnv with the hub at its tailnet name rather than the
// test origin: the URL rule, WhoIs and the hub node check all run. The
// resolver knows nothing, so the address checked is the one tailscale status
// lists for the hub, 100.101.102.103.
func newTailnetEnv(t *testing.T, opts ...envOption) *env {
	t.Helper()
	return newEnvAt(t, true, opts...)
}

// link connects to the fake hub and has the owner approve it in the hub.
func (e *env) link(scope accountlink.Scope) accountlink.State {
	e.t.Helper()
	req, err := e.svc.Connect(e.t.Context(), ConnectRequest{URL: e.hub.Origin(), Scope: scope, By: "ion"})
	if err != nil {
		e.t.Fatal(err)
	}
	e.hub.approve(req.ID)
	res, err := e.svc.Poll(e.t.Context())
	if err != nil || res.State != accountlink.PollLinked {
		e.t.Fatalf("link did not complete: %+v %v", res, err)
	}
	return res.Link
}

func (e *env) auditRows(prefix string) []audit.Entry {
	e.t.Helper()
	rows, _, err := e.audit.List(e.t.Context(), audit.Filter{ActionPrefixes: []string{prefix}, Limit: 200})
	if err != nil {
		e.t.Fatal(err)
	}
	return rows
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
