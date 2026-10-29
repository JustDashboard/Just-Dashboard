package tailnetgate

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The route the proxy asks: GET /api/v1/tailnet/whois?environment=<id>, with
// the tailnet address the request came from in X-Tailnet-Peer. The answer is
// 204 with X-Tailnet-Login and X-Tailnet-User when the environment's list
// admits whoever holds the address, 403 when it does not or nobody on the
// tailnet does, and 503 when tailscaled cannot be asked — never 403 for
// that, so a daemon restarting is an outage and not a refusal.
//
// Only the proxy may ask. The proxy is on this host and calls from loopback,
// with no X-Forwarded-For of its own; a call from anywhere else, or one that
// another proxy forwarded — the dashboard's own Caddy forwards browsers to
// the API from loopback too, and always says so in X-Forwarded-For — is
// refused before anything is looked up, so nobody who can open the dashboard
// can ask it who holds an address.

const (
	// PeerHeader carries the tailnet address the request came from, as
	// tailscaled reported it to the proxy. It is a header of its own rather
	// than X-Forwarded-For so that the API's network allowlist judges the
	// proxy's own loopback address, as it does for every other caller.
	PeerHeader = "X-Tailnet-Peer"
	// LoginHeader and UserHeader are what the application is told: the
	// login, or a tagged machine's tags, and the display name, or the
	// machine's name.
	LoginHeader = "X-Tailnet-Login"
	UserHeader  = "X-Tailnet-User"
	// Path is where the API mounts the route; the proxies' renderers name it.
	Path = "/api/v1/tailnet/whois"
	// EnvironmentParam names the environment whose list decides.
	EnvironmentParam = "environment"
)

// ErrNotGated is an environment that names nobody: not tailnet-only, or
// tailnet-only with no list, which the tailnet alone fences. The proxy only
// asks for an environment whose live release has a list, so this is a race
// with a release that dropped it, and it is answered as a refusal.
var ErrNotGated = errors.New("this environment does not decide its tailnet visitors by identity")

// Access answers an environment's allow-list as its live release has it.
type Access func(ctx context.Context, environmentID int64) ([]string, error)

// Refusal is one request the gate turned away, for the audit trail.
type Refusal struct {
	EnvironmentID int64
	// Address is the tailnet address asked about, or for a refused caller
	// the caller's own address.
	Address string
	Login   string
	Name    string
	Node    string
	Tags    []string
	// Reason is caller, no_peer, not_gated, not_on_tailnet, not_listed or
	// unavailable.
	Reason string
	Status int
	// Repeated is how many refusals of the same kind for the same address
	// were folded into this one since the last was recorded.
	Repeated int
}

// Handler is the route. Every field but Refused is required.
type Handler struct {
	WhoIs  WhoIs
	Access Access
	// Refused records a refusal. It is called at most once a minute for
	// one environment, address and reason; the rest are counted into the
	// next one, so a script hammering the address cannot flood the trail.
	Refused func(context.Context, Refusal)
	// TTL is how long an answer from tailscaled and an environment's list
	// are kept. A few seconds: a page and its assets are one question
	// rather than forty, and a login removed from the list stops working
	// within the same few seconds of its release going live.
	TTL time.Duration
	Now func() time.Time

	mu      sync.Mutex
	peers   map[netip.Addr]peerAnswer
	lists   map[int64]listAnswer
	audited map[string]auditMark
}

type peerAnswer struct {
	peer  Peer
	err   error
	until time.Time
}

type listAnswer struct {
	allow []string
	until time.Time
}

type auditMark struct {
	at     time.Time
	folded int
}

// auditEvery is how often one kind of refusal of one address is recorded.
const auditEvery = time.Minute

// cacheLimit bounds the two caches; a full one is emptied of what expired,
// and of everything when nothing has.
const cacheLimit = 4096

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h *Handler) ttl() time.Duration {
	if h.TTL > 0 {
		return h.TTL
	}
	return 5 * time.Second
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "the tailnet check is asked with GET", http.StatusMethodNotAllowed)
		return
	}
	caller := remoteAddr(r)
	if !caller.IsValid() || !caller.IsLoopback() || forwarded(r) {
		h.refuse(r.Context(), w, Refusal{Address: caller.String(), Reason: "caller", Status: http.StatusForbidden},
			"only the proxy in front of a tailnet-only deployment asks this")
		return
	}
	environmentID, err := strconv.ParseInt(r.URL.Query().Get(EnvironmentParam), 10, 64)
	if err != nil || environmentID <= 0 {
		http.Error(w, "environment is the id of a deployment environment", http.StatusBadRequest)
		return
	}
	address, ok := peerAddress(r.Header.Get(PeerHeader))
	if !ok {
		h.refuse(r.Context(), w, Refusal{EnvironmentID: environmentID, Address: strings.TrimSpace(r.Header.Get(PeerHeader)),
			Reason: "no_peer", Status: http.StatusForbidden}, "the request did not come over the tailnet")
		return
	}
	allow, err := h.list(r.Context(), environmentID)
	switch {
	case errors.Is(err, ErrNotGated):
		h.refuse(r.Context(), w, Refusal{EnvironmentID: environmentID, Address: address.String(),
			Reason: "not_gated", Status: http.StatusForbidden}, "this deployment does not admit anyone by their tailnet login")
		return
	case err != nil:
		http.Error(w, "the deployment's tailnet access could not be read", http.StatusServiceUnavailable)
		return
	}
	peer, err := h.peer(r.Context(), address)
	switch {
	case errors.Is(err, ErrNotAPeer):
		h.refuse(r.Context(), w, Refusal{EnvironmentID: environmentID, Address: address.String(),
			Reason: "not_on_tailnet", Status: http.StatusForbidden}, "no device on this tailnet holds that address")
		return
	case err != nil:
		h.refuse(r.Context(), w, Refusal{EnvironmentID: environmentID, Address: address.String(),
			Reason: "unavailable", Status: http.StatusServiceUnavailable}, "tailscaled could not be asked who this is")
		return
	}
	if _, ok := Admits(allow, peer); !ok {
		h.refuse(r.Context(), w, Refusal{EnvironmentID: environmentID, Address: address.String(), Login: peer.Login,
			Name: peer.Name, Node: peer.Node, Tags: peer.Tags, Reason: "not_listed", Status: http.StatusForbidden},
			"this tailnet login is not one this deployment admits")
		return
	}
	login := peer.Login
	if peer.Tagged() {
		login = strings.Join(peer.Tags, ",")
	}
	w.Header().Set(LoginHeader, headerValue(login))
	w.Header().Set(UserHeader, headerValue(peer.Name))
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) refuse(ctx context.Context, w http.ResponseWriter, refusal Refusal, message string) {
	if h.Refused != nil {
		if repeated, record := h.mark(refusal); record {
			refusal.Repeated = repeated
			h.Refused(context.WithoutCancel(ctx), refusal)
		}
	}
	if refusal.Reason == "caller" {
		// Answered the way the API refuses an address its allowlist does
		// not admit: whoever asked is not the proxy, and is told nothing
		// else.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(refusal.Status)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "network_denied", "message": message}})
		return
	}
	http.Error(w, message, refusal.Status)
}

// mark says whether this refusal is recorded now, and how many like it were
// held back since the last one that was.
func (h *Handler) mark(refusal Refusal) (int, bool) {
	key := strconv.FormatInt(refusal.EnvironmentID, 10) + "\x00" + refusal.Address + "\x00" + refusal.Reason
	now := h.now()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.audited == nil {
		h.audited = map[string]auditMark{}
	}
	mark, seen := h.audited[key]
	if seen && now.Sub(mark.at) < auditEvery {
		mark.folded++
		h.audited[key] = mark
		return 0, false
	}
	if len(h.audited) >= cacheLimit {
		for other, old := range h.audited {
			if now.Sub(old.at) >= auditEvery {
				delete(h.audited, other)
			}
		}
		if len(h.audited) >= cacheLimit {
			clear(h.audited)
		}
	}
	h.audited[key] = auditMark{at: now}
	return mark.folded, true
}

func (h *Handler) list(ctx context.Context, environmentID int64) ([]string, error) {
	now := h.now()
	h.mu.Lock()
	if answer, ok := h.lists[environmentID]; ok && now.Before(answer.until) {
		h.mu.Unlock()
		return answer.allow, nil
	}
	h.mu.Unlock()
	allow, err := h.Access(ctx, environmentID)
	if err != nil {
		return nil, err
	}
	if len(allow) == 0 {
		// Not kept: an environment's first release moves its address to
		// the gate a moment before it goes live, and a refusal remembered
		// from that moment would turn its first visitors away for seconds.
		return nil, ErrNotGated
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.lists == nil || len(h.lists) >= cacheLimit {
		h.lists = map[int64]listAnswer{}
	}
	h.lists[environmentID] = listAnswer{allow: allow, until: now.Add(h.ttl())}
	return allow, nil
}

func (h *Handler) peer(ctx context.Context, address netip.Addr) (Peer, error) {
	now := h.now()
	h.mu.Lock()
	if answer, ok := h.peers[address]; ok && now.Before(answer.until) {
		h.mu.Unlock()
		return answer.peer, answer.err
	}
	h.mu.Unlock()
	peer, err := h.WhoIs.WhoIs(ctx, address)
	if err != nil && !errors.Is(err, ErrNotAPeer) {
		// An outage is asked about again on the next request, so the gate
		// opens the moment tailscaled is back.
		return Peer{}, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.peers == nil {
		h.peers = map[netip.Addr]peerAnswer{}
	}
	if len(h.peers) >= cacheLimit {
		for other, old := range h.peers {
			if !now.Before(old.until) {
				delete(h.peers, other)
			}
		}
		if len(h.peers) >= cacheLimit {
			clear(h.peers)
		}
	}
	h.peers[address] = peerAnswer{peer: peer, err: err, until: now.Add(h.ttl())}
	return peer, err
}

// remoteAddr is the connection's own peer, never a header.
func remoteAddr(r *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	address, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	return address.Unmap()
}

// forwarded says another proxy stands between the caller and this route.
// The gate's proxies remove all three before they ask.
func forwarded(r *http.Request) bool {
	for _, name := range []string{"X-Forwarded-For", "X-Real-IP", "Forwarded"} {
		if strings.TrimSpace(r.Header.Get(name)) != "" {
			return true
		}
	}
	return false
}

// peerAddress reads the address the proxy says the request came from. Only
// the right-most entry counts if a list arrives, as with X-Forwarded-For:
// that is the one the proxy itself saw.
func peerAddress(raw string) (netip.Addr, bool) {
	parts := strings.Split(raw, ",")
	value := strings.TrimSpace(parts[len(parts)-1])
	value = strings.TrimSuffix(strings.TrimPrefix(value, "["), "]")
	address, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Addr{}, false
	}
	address = address.Unmap()
	if address.Zone() != "" || address.IsUnspecified() {
		return netip.Addr{}, false
	}
	return address, true
}

// headerValue keeps a header to printable ASCII, the way tailscaled writes
// its own identity headers: a display name in another script is sent as an
// RFC 2047 encoded word rather than as bytes a proxy may reject.
func headerValue(value string) string {
	value = strings.TrimSpace(value)
	for _, r := range value {
		if r < 0x20 || r > 0x7e {
			return mime.QEncoding.Encode("utf-8", strings.Map(func(r rune) rune {
				if r < 0x20 || r == 0x7f {
					return -1
				}
				return r
			}, value))
		}
	}
	return value
}
