package api

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// A hub session (hub-link-protocol.md section 9.9) ends at once when its grant
// ends, when the link ends or goes idle, and after two hours. Every HTTP
// request resolves the session again, but a socket is authenticated once, at
// the upgrade: a terminal opened from a hub session would otherwise keep its
// shell after every one of those ends. So every socket a hub session opens,
// and every stream the dashboard proxies for one, is watched: it is closed at
// the session's expiry, when a check finds the session gone, and when a
// revocation that ended hub sessions has committed (kick).

// hubSocketRecheck is how often a hub session's socket checks its session
// again. A revocation made by this process closes it at once (kick); this
// catches one nothing announced, such as a session revoked from the sessions
// list or a grant row deleted outside the dashboard.
const hubSocketRecheck = 15 * time.Second

// hubSockets is every hub session socket that is open, so a revocation can
// make each check again now.
type hubSockets struct {
	mu    sync.Mutex
	kicks map[chan struct{}]struct{}
	// every replaces hubSocketRecheck, for tests.
	every time.Duration
}

func (h *hubSockets) interval() time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.every > 0 {
		return h.every
	}
	return hubSocketRecheck
}

func (h *hubSockets) add() (chan struct{}, func()) {
	kick := make(chan struct{}, 1)
	h.mu.Lock()
	if h.kicks == nil {
		h.kicks = map[chan struct{}]struct{}{}
	}
	h.kicks[kick] = struct{}{}
	h.mu.Unlock()
	return kick, func() {
		h.mu.Lock()
		delete(h.kicks, kick)
		h.mu.Unlock()
	}
}

// kick makes every open hub session socket check its session now. It is
// called once a transaction that revoked hub sessions has committed.
func (h *hubSockets) kick() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for k := range h.kicks {
		select {
		case k <- struct{}{}:
		default:
		}
	}
}

// open is how many hub session sockets are being watched, for tests.
func (h *hubSockets) open() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.kicks)
}

// guardHubSocket is the wsx.Guard of every socket the API opens. A socket
// opened by anything but a hub session is left as it was.
func (s *Server) guardHubSocket(r *http.Request, end func(), ended <-chan struct{}) {
	p, ok := httpx.PrincipalFrom(r.Context())
	if !ok || !p.HubSession {
		return
	}
	kick, drop := s.hubSockets.add()
	go func() {
		defer drop()
		s.watchHubSocket(r, p, end, ended, kick)
	}()
}

// guardHubStream is guardHubSocket for a socket the dashboard proxies rather
// than opens (the Remote Desktop stream bridge): the request it returns is
// cancelled when the hub session behind it ends, which closes the proxied
// connection. done must be called when the proxy returns.
func (s *Server) guardHubStream(r *http.Request) (*http.Request, func()) {
	ctx, cancel := context.WithCancel(r.Context())
	r = r.WithContext(ctx)
	s.guardHubSocket(r, cancel, ctx.Done())
	return r, cancel
}

func (s *Server) watchHubSocket(r *http.Request, p *httpx.Principal, end func(), ended <-chan struct{}, kick <-chan struct{}) {
	// The request's context ends when its handler returns, which for a
	// socket is when the socket is done.
	ctx := r.Context()
	expires, err := s.Authn.SessionStands(r, p)
	if err != nil {
		end()
		return
	}
	expiry := time.NewTimer(time.Until(expires))
	defer expiry.Stop()
	tick := time.NewTicker(s.hubSockets.interval())
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ended:
			return
		case <-expiry.C:
			end()
			return
		case <-tick.C:
		case <-kick:
		}
		if _, err := s.Authn.SessionStands(r, p); err != nil {
			end()
			return
		}
	}
}
