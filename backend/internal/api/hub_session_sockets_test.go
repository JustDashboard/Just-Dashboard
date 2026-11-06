package api

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/gorilla/websocket"
)

// A socket is authenticated once, at the upgrade. One a hub session opened
// must still end when the session does (hub-link-protocol.md section 9.9:
// "ends at once when its grant ends ... when the link ends or goes idle",
// two hours at most), or a terminal opened from a taken-over hub would keep
// its shell after every kill switch.

// openStream opens the metrics socket as the client c, from a real server,
// and reads its first frame.
func openStream(t *testing.T, srv *httptest.Server, c *client) *websocket.Conn {
	t.Helper()
	header := http.Header{}
	header.Set("Cookie", c.cookie)
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/system/stream?interval=1000"
	conn, res, err := websocket.DefaultDialer.Dial(url, header)
	if err != nil {
		status := 0
		if res != nil {
			status = res.StatusCode
		}
		t.Fatalf("dial = %v (%d)", err, status)
	}
	t.Cleanup(func() { conn.Close() })
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatalf("first frame: %v", err)
	}
	return conn
}

// closedWithin reads until the server ends the socket, and says how long that
// took; a socket still open after limit fails the test.
func closedWithin(t *testing.T, conn *websocket.Conn, limit time.Duration) time.Duration {
	t.Helper()
	start := time.Now()
	conn.SetReadDeadline(start.Add(limit))
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				t.Fatalf("the socket is still open %s later", limit)
			}
			return time.Since(start)
		}
	}
}

// stillOpen reads frames for d and fails if the socket ends meanwhile.
func stillOpen(t *testing.T, conn *websocket.Conn, d time.Duration) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(d))
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				return
			}
			t.Fatalf("the socket ended: %v", err)
		}
	}
}

// Every event that ends a grant or the link closes the sockets of the hub
// sessions it ended. One this process revoked closes at once, with the
// periodic check an hour away; one nothing announced closes at the next
// check.
func TestAHubSessionsSocketsEndWithIt(t *testing.T) {
	for _, ev := range grantEnds() {
		t.Run(ev.name, func(t *testing.T) {
			e := newSignInEnv(t)
			e.s.hubSockets.every = time.Hour
			if ev.unannounced {
				e.s.hubSockets.every = 200 * time.Millisecond
			}
			srv := httptest.NewServer(e.h)
			t.Cleanup(srv.Close)
			conn := openStream(t, srv, e.enter())
			if n := e.s.hubSockets.open(); n != 1 {
				t.Fatalf("watched sockets = %d, want 1", n)
			}
			ev.fire(e)
			closedWithin(t, conn, 5*time.Second)
			waitFor(t, "the watch to end", func() bool { return e.s.hubSockets.open() == 0 })
		})
	}
}

// A password session's socket is not a hub session's: it is not watched, and
// an event that ends hub sessions leaves it open.
func TestASignedInSessionsSocketIsLeftAlone(t *testing.T) {
	e := newSignInEnv(t)
	e.s.hubSockets.every = 100 * time.Millisecond
	srv := httptest.NewServer(e.h)
	t.Cleanup(srv.Close)
	hubConn := openStream(t, srv, e.enter())
	conn := openStream(t, srv, e.admin)
	if n := e.s.hubSockets.open(); n != 1 {
		t.Fatalf("watched sockets = %d, want only the hub session's", n)
	}
	if w := e.admin.do(http.MethodDelete, "/api/v1/account/hub-signin", "", nil); w.Code != 204 {
		t.Fatalf("revoke = %d %s", w.Code, w.Body)
	}
	closedWithin(t, hubConn, 5*time.Second)
	stillOpen(t, conn, 1500*time.Millisecond)
}

// A hub session's socket closes when the session expires, whatever happens
// to be checked by then.
func TestAHubSessionsSocketEndsAtItsExpiry(t *testing.T) {
	e := newSignInEnv(t)
	e.s.hubSockets.every = time.Hour
	srv := httptest.NewServer(e.h)
	t.Cleanup(srv.Close)
	hub := e.enter()
	if _, err := e.s.Store.DB.Exec(`UPDATE sessions SET expires_at = ? WHERE auth_method = 'hub'`,
		time.Now().Add(2*time.Second).Unix()); err != nil {
		t.Fatal(err)
	}
	conn := openStream(t, srv, hub)
	if took := closedWithin(t, conn, 6*time.Second); took < 500*time.Millisecond {
		t.Fatalf("closed after %s, before the session expired", took)
	}
}

// A socket the dashboard proxies (the Remote Desktop stream bridge) is
// carried on a request whose context ends with the hub session behind it.
func TestAProxiedStreamEndsWithTheHubSession(t *testing.T) {
	e := newSignInEnv(t)
	e.s.hubSockets.every = time.Hour
	hub := e.enter()
	var id string
	if err := e.s.Store.DB.QueryRow(`SELECT id FROM sessions WHERE auth_method = 'hub'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/remote-desktop/bridge/api/host/stream", nil)
	r.Header.Set("Cookie", hub.cookie)
	r = r.WithContext(httpx.WithPrincipal(r.Context(), &httpx.Principal{
		Kind: "session", SessionID: id, HubSession: true,
	}))
	guarded, done := e.s.guardHubStream(r)
	defer done()
	select {
	case <-guarded.Context().Done():
		t.Fatal("the stream ended while its session stood")
	case <-time.After(300 * time.Millisecond):
	}
	if _, err := e.s.modules.hubLink.Disconnect(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-guarded.Context().Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the proxied stream outlived its hub session")
	}
}
