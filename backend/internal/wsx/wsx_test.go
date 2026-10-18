package wsx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
