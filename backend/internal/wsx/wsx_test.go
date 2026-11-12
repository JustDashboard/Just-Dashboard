package wsx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestOriginMustMatchSchemeHostAndEffectivePort(t *testing.T) {
	tests := []struct {
		name   string
		secure bool
		host   string
		origin string
		want   bool
	}{
		{name: "production origin", secure: true, host: "dashboard.example", origin: "https://dashboard.example", want: true},
		{name: "default TLS port", secure: true, host: "dashboard.example:443", origin: "https://dashboard.example", want: true},
		{name: "scheme downgrade", secure: true, host: "dashboard.example", origin: "http://dashboard.example", want: false},
		{name: "different port", secure: true, host: "dashboard.example", origin: "https://dashboard.example:8443", want: false},
		{name: "different host", secure: true, host: "dashboard.example", origin: "https://other.example", want: false},
		{name: "origin path", secure: true, host: "dashboard.example", origin: "https://dashboard.example/path", want: false},
		{name: "opaque origin", secure: true, host: "dashboard.example", origin: "null", want: false},
		{name: "development origin", secure: false, host: "localhost:8080", origin: "http://localhost:8080", want: true},
		{name: "development scheme upgrade", secure: false, host: "localhost:8080", origin: "https://localhost:8080", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := NewUpgrader(nil, tt.secure)
			r := httptest.NewRequest("GET", "http://backend.invalid/ws", nil)
			r.Host = tt.host
			r.Header.Set("Origin", tt.origin)
			if got := u.checkOrigin(r); got != tt.want {
				t.Fatalf("checkOrigin(%q, host %q) = %v, want %v", tt.origin, tt.host, got, tt.want)
			}
		})
	}
}

func TestOriginAllowsOnlyAnExplicitCrossOrigin(t *testing.T) {
	u := NewUpgrader([]string{"http://localhost:3000", "not an origin"}, true)
	for _, tt := range []struct {
		origin string
		want   bool
	}{
		{origin: "http://localhost:3000", want: true},
		{origin: "http://LOCALHOST:3000", want: true},
		{origin: "https://localhost:3000", want: false},
		{origin: "http://localhost:3001", want: false},
	} {
		r := httptest.NewRequest("GET", "http://backend.invalid/ws", nil)
		r.Host = "dashboard.example"
		r.Header.Set("Origin", tt.origin)
		if got := u.checkOrigin(r); got != tt.want {
			t.Errorf("checkOrigin(%q) = %v, want %v", tt.origin, got, tt.want)
		}
	}
}

func TestOriginlessNonBrowserClientIsAllowed(t *testing.T) {
	u := NewUpgrader(nil, true)
	r := httptest.NewRequest("GET", "http://dashboard.example/ws", nil)
	if !u.checkOrigin(r) {
		t.Fatal("originless API client was rejected")
	}
}

func TestUpgradeProtocolNamesTheProtocolOnlyWhenOffered(t *testing.T) {
	u := NewUpgrader(nil, false)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if conn, err := u.UpgradeProtocol(w, r, "guacamole"); err == nil {
			conn.Close()
		}
	}))
	defer server.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http")
	for _, offered := range [][]string{{"guacamole"}, {"chat", "guacamole"}, nil, {"chat"}} {
		dialer := websocket.Dialer{Subprotocols: offered}
		conn, res, err := dialer.Dial(url, nil)
		if err != nil {
			t.Fatalf("offered %v: %v", offered, err)
		}
		conn.Close()
		want := ""
		for _, p := range offered {
			if p == "guacamole" {
				want = "guacamole"
			}
		}
		if got := res.Header.Get("Sec-Websocket-Protocol"); got != want {
			t.Fatalf("offered %v, answered %q, want %q", offered, got, want)
		}
	}
}

// Every socket is handed to the Guard with the request that opened it; ending
// it from there closes the socket, and the channel the Guard watches closes
// whichever side ended it.
func TestTheGuardSeesEverySocketAndCanEndIt(t *testing.T) {
	u := NewUpgrader(nil, false)
	type opened struct {
		path  string
		end   func()
		ended <-chan struct{}
	}
	got := make(chan opened, 1)
	u.SetGuard(func(r *http.Request, end func(), ended <-chan struct{}) {
		got <- opened{r.URL.Path, end, ended}
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := u.Upgrade(w, r)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer srv.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	g := <-got
	if g.path != "/stream" {
		t.Fatalf("guarded %q", g.path)
	}
	select {
	case <-g.ended:
		t.Fatal("ended before anything ended it")
	default:
	}
	g.end()
	<-g.ended
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("the socket is still open after the guard ended it")
	}
}

// A handshake that fails is answered in the API's error envelope. gorilla's
// own answer is a line of text ("Bad Request"), the one refusal of a socket
// route a client could not read a code from.
func TestAFailedHandshakeIsAnsweredInTheErrorEnvelope(t *testing.T) {
	u := NewUpgrader(nil, true)
	handshake := func(edit func(*http.Request)) (*httptest.ResponseRecorder, string) {
		r := httptest.NewRequest(http.MethodGet, "http://backend.invalid/ws", nil)
		r.Host = "dashboard.example"
		r.Header.Set("Connection", "Upgrade")
		r.Header.Set("Upgrade", "websocket")
		r.Header.Set("Sec-Websocket-Version", "13")
		r.Header.Set("Sec-Websocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
		edit(r)
		rec := httptest.NewRecorder()
		if _, err := u.Upgrade(rec, r); err == nil {
			t.Fatal("the handshake was accepted")
		}
		var body struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error.Message == "" {
			t.Fatalf("the refusal is not the envelope: %q (%v)", rec.Body.String(), err)
		}
		if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
			t.Errorf("Content-Type = %q", got)
		}
		return rec, body.Error.Code
	}

	// A plain GET: curl, or a script that took the route for a read.
	rec, code := handshake(func(r *http.Request) {
		r.Header.Del("Connection")
		r.Header.Del("Upgrade")
	})
	if rec.Code != http.StatusBadRequest || code != "websocket_upgrade_required" {
		t.Errorf("a plain GET = %d %s", rec.Code, code)
	}
	if rec, code = handshake(func(r *http.Request) { r.Header.Set("Origin", "https://other.example") }); rec.Code != http.StatusForbidden || code != "origin_not_allowed" {
		t.Errorf("a foreign origin = %d %s", rec.Code, code)
	}
	if rec, code = handshake(func(r *http.Request) { r.Method = http.MethodPost }); rec.Code != http.StatusMethodNotAllowed || code != "method_not_allowed" {
		t.Errorf("a POST = %d %s", rec.Code, code)
	}
}
