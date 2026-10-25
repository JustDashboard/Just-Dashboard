package accountlink

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store/storetest"
)

// The keys and server id the protocol document's test vectors are made with.
const (
	vectorServerSeed = "9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60"
	vectorSiteSeed   = "4ccd089b28ff96da9db6c346ec114e0f5b8a319f35aba624da8cf6ed4fb8a6fb"
	vectorServerID   = "7a0d3c1e-0000-4000-8000-000000000001"
	vectorKid        = "2026-10"
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

// testEnv is a Service over a real store, talking to a fake site.
type testEnv struct {
	t     *testing.T
	st    *store.Store
	svc   *Service
	site  *fakeSite
	host  *fakeHost
	audit *audit.Logger
	now   time.Time
	mu    sync.Mutex
}

func (e *testEnv) clock() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.now
}

func (e *testEnv) advance(d time.Duration) {
	e.mu.Lock()
	e.now = e.now.Add(d)
	e.mu.Unlock()
	e.site.setNow(e.clock())
}

type envOption func(*Options)

func newEnv(t *testing.T, opts ...envOption) *testEnv {
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
	e := &testEnv{t: t, st: st, now: time.Unix(vectorNow, 0).UTC(), host: newFakeHost()}
	e.site = newFakeSite(t, e.now)
	e.audit = audit.New(st, slog.New(slog.DiscardHandler))
	o := Options{
		Store: st, Sealer: sealer, Audit: e.audit, SiteURL: e.site.srv.URL, Enabled: true, Host: e.host,
		Version: "0.8.2", Now: e.clock, Location: time.UTC,
		Describe: func(context.Context) ServerDescription {
			return ServerDescription{Name: "box", Hostname: "box", OS: "Ubuntu 24.04", Arch: "amd64"}
		},
	}
	for _, f := range opts {
		f(&o)
	}
	e.svc = New(o)
	// Quick loops: a test cannot wait fifteen seconds for anything.
	e.svc.idle, e.svc.watch = 10*time.Millisecond, 10*time.Millisecond
	e.svc.backoffMin, e.svc.backoffMax = 2*time.Millisecond, 10*time.Millisecond
	e.svc.policyFirst, e.svc.policyEvery = time.Hour, time.Hour
	// Pacing runs on the real clock, so a test that fails the site on purpose
	// would otherwise sit out a real minute.
	e.svc.hbPerMinute = 1 << 20
	t.Cleanup(e.svc.Stop)
	return e
}

// link performs a whole link through the service the way the dashboard does,
// leaving the install linked at scope.
func (e *testEnv) link(scope Scope) State {
	e.t.Helper()
	req, err := e.svc.BeginLink(e.t.Context(), scope, "admin")
	if err != nil {
		e.t.Fatal(err)
	}
	e.site.approve(req.ID, "ion@example.com")
	res, err := e.svc.PollLink(e.t.Context())
	if err != nil || res.State != PollLinked {
		e.t.Fatalf("link did not complete: %+v %v", res, err)
	}
	return res.Link
}

// auditRows returns the audit entries whose action starts with prefix.
func (e *testEnv) auditRows(prefix string) []audit.Entry {
	e.t.Helper()
	rows, _, err := e.audit.List(e.t.Context(), audit.Filter{ActionPrefixes: []string{prefix}, Limit: 100})
	if err != nil {
		e.t.Fatal(err)
	}
	return rows
}

// ---------------------------------------------------------------- fake host --

type hostCall struct {
	Method string
	Actor  string
	Args   []any
}

type fakeHost struct {
	mu       sync.Mutex
	calls    []hostCall
	snapshot Snapshot
	update   UpdateStatus
	fail     map[string]error
	output   string

	updateReadCount int
	// onCollect runs while the machine is being read, before Collect returns:
	// what happens during the seconds a real read takes.
	onCollect func()
}

func newFakeHost() *fakeHost {
	return &fakeHost{
		snapshot: Snapshot{Server: ServerInfo{Name: "box", Version: "0.8.2"}},
		update:   UpdateStatus{Version: "0.8.2", Latest: "0.8.2", Supported: true},
		fail:     map[string]error{},
	}
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

func (h *fakeHost) Collect(context.Context) (Snapshot, error) {
	h.mu.Lock()
	snap, hook := h.snapshot, h.onCollect
	h.mu.Unlock()
	if hook != nil {
		hook()
	}
	return snap, nil
}

func (h *fakeHost) UpdateStatus(context.Context) (UpdateStatus, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.updateReadCount++
	return h.update, nil
}

// updateReads is how often the machine was asked where its updates stand.
func (h *fakeHost) updateReads() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.updateReadCount
}

func (h *fakeHost) result(def string) string {
	if h.output != "" {
		return h.output
	}
	return def
}

func (h *fakeHost) Container(_ context.Context, actor, name, action string) (string, error) {
	if err := h.record("Container", actor, name, action); err != nil {
		return "", err
	}
	return h.result(name + " " + action), nil
}

func (h *fakeHost) RestartStack(_ context.Context, actor, name string) (string, error) {
	if err := h.record("RestartStack", actor, name); err != nil {
		return "", err
	}
	return h.result("restarted " + name), nil
}

func (h *fakeHost) RunBackup(_ context.Context, actor string, id int64) (string, error) {
	if err := h.record("RunBackup", actor, id); err != nil {
		return "", err
	}
	return h.result("backup started"), nil
}

func (h *fakeHost) InstallUpdate(_ context.Context, actor, version string) (string, error) {
	if err := h.record("InstallUpdate", actor, version); err != nil {
		return "", err
	}
	return h.result("installing " + version), nil
}

func (h *fakeHost) RestartDashboard(_ context.Context, actor string, rebuild bool) (string, error) {
	if err := h.record("RestartDashboard", actor, rebuild); err != nil {
		return "", err
	}
	return h.result("restarting"), nil
}

// ---------------------------------------------------------------- fake site --

type fakeRequest struct {
	id, code string
	pub      ed25519.PublicKey
	scope    string
	server   json.RawMessage
	status   string // pending, approved, denied, expired, consumed
	owner    string
	seq      int64
}

type fakeServer struct {
	id      string
	pub     ed25519.PublicKey
	seq     int64
	removed bool
}

// fakeSite behaves like the real site as far as the protocol goes: it checks the
// signature, the time window and the sequence of every signed request, hands
// out commands signed with its own key, and answers 410 once a server is
// removed.
type fakeSite struct {
	t   *testing.T
	srv *httptest.Server
	key ed25519.PrivateKey

	mu         sync.Mutex
	now        time.Time
	requests   map[string]*fakeRequest
	servers    map[string]*fakeServer
	heartbeats [][]byte
	queue      []Command
	results    map[string]CommandResult
	resultLog  []string
	posts      map[string]int
	hits       map[string]int
	nextID     int
	siteReply  func(w http.ResponseWriter) bool
	failNext   map[string]int // path prefix -> remaining 500s
	rotate     []wireSiteKey
	unlinked   bool
}

func newFakeSite(t *testing.T, now time.Time) *fakeSite {
	f := &fakeSite{
		t: t, key: seedKey(t, vectorSiteSeed), now: now,
		requests: map[string]*fakeRequest{}, servers: map[string]*fakeServer{},
		results: map[string]CommandResult{}, hits: map[string]int{}, failNext: map[string]int{}, posts: map[string]int{},
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeSite) setNow(t time.Time) {
	f.mu.Lock()
	f.now = t
	f.mu.Unlock()
}

func (f *fakeSite) hitCount(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for k, v := range f.hits {
		if strings.HasPrefix(k, prefix) {
			n += v
		}
	}
	return n
}

func (f *fakeSite) fail(prefix string, times int) {
	f.mu.Lock()
	f.failNext[prefix] = times
	f.mu.Unlock()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func apiError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": msg}})
}

func (f *fakeSite) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	defer f.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, basePath)
	f.hits[r.Method+" "+path]++
	for prefix, n := range f.failNext {
		if n > 0 && strings.HasPrefix(path, prefix) {
			f.failNext[prefix] = n - 1
			apiError(w, 500, "internal", "synthetic failure")
			return
		}
	}
	if r.Header.Get(headerProtocol) != "1" {
		apiError(w, 400, "unsupported_protocol", "need protocol 1")
		return
	}
	switch {
	case r.Method == http.MethodGet && path == "/site":
		if f.siteReply != nil && f.siteReply(w) {
			return
		}
		writeJSON(w, 200, map[string]any{
			"protocol":   []int{1},
			"siteKeys":   []wireSiteKey{{Kid: vectorKid, PublicKey: pubB64(f.key)}},
			"approveUrl": f.srv.URL + "/link",
		})
	case r.Method == http.MethodPost && path == "/requests":
		f.createRequest(w, body)
	case strings.HasPrefix(path, "/requests/"):
		f.pollRequest(w, r, body, strings.TrimPrefix(path, "/requests/"))
	default:
		f.signed(w, r, body, path)
	}
}

func (f *fakeSite) createRequest(w http.ResponseWriter, body []byte) {
	var in struct {
		PublicKey string          `json:"publicKey"`
		Scope     string          `json:"scope"`
		Server    json.RawMessage `json:"server"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		apiError(w, 400, "bad_request", err.Error())
		return
	}
	pub, err := base64.StdEncoding.DecodeString(in.PublicKey)
	if err != nil || len(pub) != 32 {
		apiError(w, 400, "bad_request", "publicKey")
		return
	}
	f.nextID++
	id := fmt.Sprintf("req-%08d-aaaa", f.nextID)
	code := fmt.Sprintf("ABCD-%04d", 2222+f.nextID)
	f.requests[id] = &fakeRequest{id: id, code: code, pub: pub, scope: in.Scope, server: in.Server, status: "pending"}
	writeJSON(w, 200, map[string]any{
		"requestId": id, "userCode": code, "approveUrl": f.srv.URL + "/link?code=" + code,
		"expiresAt": f.now.Add(15 * time.Minute).Unix(), "pollSeconds": 2,
	})
}

// check verifies the signature headers the way the site documents it. It
// returns the public key's owner id on success.
func (f *fakeSite) check(w http.ResponseWriter, r *http.Request, body []byte, lookup func(id string) (ed25519.PublicKey, *int64, bool, bool)) bool {
	id := r.Header.Get(headerServer)
	pub, seq, removed, ok := lookup(id)
	if !ok {
		apiError(w, 401, "unknown_server", "no such server")
		return false
	}
	if removed {
		apiError(w, 410, "server_removed", "removed")
		return false
	}
	ts, err1 := strconv.ParseInt(r.Header.Get(headerTime), 10, 64)
	n, err2 := strconv.ParseInt(r.Header.Get(headerSequence), 10, 64)
	sig, err3 := base64.StdEncoding.DecodeString(r.Header.Get(headerSignature))
	if err1 != nil || err2 != nil || err3 != nil {
		apiError(w, 401, "bad_signature", "malformed")
		return false
	}
	if d := f.now.Unix() - ts; d > 60 || d < -60 {
		apiError(w, 401, "stale_time", "clock")
		return false
	}
	msg := requestMessage(r.Method, r.URL.RequestURI(), body, ts, n)
	if !ed25519.Verify(pub, []byte(msg), sig) {
		apiError(w, 401, "bad_signature", "signature")
		return false
	}
	if n <= *seq {
		apiError(w, 401, "replayed_sequence", "sequence")
		return false
	}
	*seq = n
	return true
}

func (f *fakeSite) pollRequest(w http.ResponseWriter, r *http.Request, body []byte, id string) {
	req := f.requests[id]
	ok := f.check(w, r, body, func(string) (ed25519.PublicKey, *int64, bool, bool) {
		if req == nil || r.Header.Get(headerServer) != id {
			return nil, nil, false, false
		}
		return req.pub, &req.seq, false, true
	})
	if !ok {
		return
	}
	switch req.status {
	case "pending":
		writeJSON(w, 200, map[string]any{"status": "pending"})
	case "approved":
		req.status = "consumed"
		serverID := fmt.Sprintf("server-%04d-0000", f.nextID)
		f.servers[serverID] = &fakeServer{id: serverID, pub: req.pub, seq: req.seq}
		writeJSON(w, 200, map[string]any{
			"status": "approved", "serverId": serverID,
			"siteKey":          map[string]string{"kid": vectorKid, "publicKey": pubB64(f.key)},
			"heartbeatSeconds": 30, "pollWaitSeconds": 1, "owner": map[string]string{"label": req.owner},
		})
	case "denied":
		apiError(w, 410, "request_denied", "denied")
	case "expired":
		apiError(w, 410, "request_expired", "expired")
	default:
		apiError(w, 410, "request_consumed", "consumed")
	}
}

func (f *fakeSite) approve(id, owner string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests[id].status, f.requests[id].owner = "approved", owner
}

func (f *fakeSite) setRequestStatus(id, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests[id].status = status
}

func (f *fakeSite) signed(w http.ResponseWriter, r *http.Request, body []byte, path string) {
	var server *fakeServer
	ok := f.check(w, r, body, func(id string) (ed25519.PublicKey, *int64, bool, bool) {
		server = f.servers[id]
		if server == nil {
			return nil, nil, false, false
		}
		return server.pub, &server.seq, server.removed, true
	})
	if !ok {
		return
	}
	switch {
	case r.Method == http.MethodPost && path == "/heartbeat":
		f.heartbeats = append(f.heartbeats, append([]byte(nil), body...))
		reply := map[string]any{"ok": true, "heartbeatSeconds": 30, "pendingCommands": len(f.queue)}
		if f.rotate != nil {
			reply["siteKeys"] = f.rotate
		}
		writeJSON(w, 200, reply)
	case r.Method == http.MethodGet && path == "/commands":
		f.mu.Unlock()
		deadline := time.Now().Add(150 * time.Millisecond)
		for time.Now().Before(deadline) {
			f.mu.Lock()
			n := len(f.queue)
			f.mu.Unlock()
			if n > 0 {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		f.mu.Lock()
		out := f.queue
		f.queue = nil
		if out == nil {
			out = []Command{}
		}
		writeJSON(w, 200, map[string]any{"commands": out})
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/commands/") && strings.HasSuffix(path, "/result"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/commands/"), "/result")
		var res CommandResult
		if err := json.NewDecoder(bytes.NewReader(body)).Decode(&res); err != nil {
			apiError(w, 400, "bad_request", err.Error())
			return
		}
		f.results[id] = res
		f.posts[id]++
		f.resultLog = append(f.resultLog, id)
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost && path == "/unlink":
		server.removed = true
		f.unlinked = true
		w.WriteHeader(http.StatusNoContent)
	default:
		apiError(w, 404, "not_found", path)
	}
}

// remove is the owner removing the server on the site.
func (f *fakeSite) remove(serverID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.servers[serverID].removed = true
}

func (f *fakeSite) heartbeatBodies() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]byte(nil), f.heartbeats...)
}

// resultPosts is how many results the site has been sent under an id.
func (f *fakeSite) resultPosts(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.posts[id]
}

func (f *fakeSite) result(id string) (CommandResult, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.results[id]
	return r, ok
}

// command signs a command for serverID with the site key and queues it.
func (f *fakeSite) command(serverID, id, kind string, args map[string]any, expires time.Time) Command {
	c := f.signedCommand(f.key, vectorKid, serverID, id, kind, args, expires)
	f.mu.Lock()
	f.queue = append(f.queue, c)
	f.mu.Unlock()
	return c
}

func (f *fakeSite) signedCommand(key ed25519.PrivateKey, kid, serverID, id, kind string, args map[string]any, expires time.Time) Command {
	rawArgs, err := json.Marshal(args)
	if err != nil {
		f.t.Fatal(err)
	}
	return signCommandWith(f.t, key, kid, serverID, id, kind, rawArgs, expires.Unix())
}

func signCommandWith(t testing.TB, key ed25519.PrivateKey, kid, serverID, id, kind string, rawArgs []byte, expires int64) Command {
	t.Helper()
	decoded, err := decodeArgs(rawArgs)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicalJSON(decoded)
	if err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(key, []byte(commandMessage(serverID, id, kind, canonical, expires)))
	return Command{
		ID: id, Kind: kind, Args: rawArgs, IssuedBy: "ion@example.com", IssuedAt: expires - 600,
		ExpiresAt: expires, Kid: kid, Signature: base64.StdEncoding.EncodeToString(sig),
	}
}

// ulid builds a valid command id from a counter.
func ulid(n int) string { return fmt.Sprintf("01JB%022d", n) }

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
