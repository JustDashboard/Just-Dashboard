// Package linkstub is a minimal stand-in for the site, for the tests of the
// packages that drive a link: the terminal command and the HTTP handlers. It
// speaks just enough of protocol 1 to be linked to, heartbeated to and
// unlinked from. It does not verify signatures; the accountlink package's own
// tests do that against a site that does.
package linkstub

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type Site struct {
	*httptest.Server
	t testing.TB

	mu       sync.Mutex
	status   string
	owner    string
	requests int
	polls    int
	beats    int
	unlinks  int
	hits     int
	key      ed25519.PublicKey
}

// New starts a site that has one request to give out at a time.
func New(t testing.TB) *Site {
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	s := &Site{t: t, status: "pending", key: pub}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

func (s *Site) set(status, owner string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status, s.owner = status, owner
}

// Approve, Deny and Expire decide the request the next poll will see.
func (s *Site) Approve(owner string) { s.set("approved", owner) }
func (s *Site) Deny()                { s.set("denied", "") }
func (s *Site) Expire()              { s.set("expired", "") }

// Count is how many of each thing the site has been asked for.
func (s *Site) Count() (requests, polls, heartbeats, unlinks, all int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests, s.polls, s.beats, s.unlinks, s.hits
}

// WaitForRequest blocks until a link request has been created.
func (s *Site) WaitForRequest() {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if r, _, _, _, _ := s.Count(); r > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	s.t.Fatal("no link request arrived")
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func fail(w http.ResponseWriter, status int, code string) {
	reply(w, status, map[string]any{"error": map[string]string{"code": code, "message": code}})
}

func (s *Site) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hits++
	path := strings.TrimPrefix(r.URL.Path, "/api/link/v1")
	switch {
	case path == "/site":
		reply(w, 200, map[string]any{
			"protocol": []int{1}, "approveUrl": s.URL + "/link",
			"siteKeys": []map[string]any{{"kid": "stub", "publicKey": base64.StdEncoding.EncodeToString(s.key), "notAfter": nil}},
		})
	case r.Method == http.MethodPost && path == "/requests":
		s.requests++
		s.status = "pending"
		reply(w, 200, map[string]any{
			"requestId": fmt.Sprintf("request-%04d-stub", s.requests), "userCode": "ABCD-2345",
			"approveUrl": s.URL + "/link?code=ABCD-2345", "expiresAt": time.Now().Add(15 * time.Minute).Unix(), "pollSeconds": 2,
		})
	case strings.HasPrefix(path, "/requests/"):
		s.polls++
		switch s.status {
		case "approved":
			s.status = "consumed"
			reply(w, 200, map[string]any{
				"status": "approved", "serverId": "server-0001-stub",
				"siteKey":          map[string]string{"kid": "stub", "publicKey": base64.StdEncoding.EncodeToString(s.key)},
				"heartbeatSeconds": 30, "pollWaitSeconds": 1, "owner": map[string]string{"label": s.owner},
			})
		case "denied":
			fail(w, 410, "request_denied")
		case "expired":
			fail(w, 410, "request_expired")
		case "consumed":
			fail(w, 410, "request_consumed")
		default:
			reply(w, 200, map[string]string{"status": "pending"})
		}
	case path == "/heartbeat":
		s.beats++
		reply(w, 200, map[string]any{"ok": true, "heartbeatSeconds": 30, "pendingCommands": 0})
	case path == "/commands":
		reply(w, 200, map[string]any{"commands": []any{}})
	case path == "/unlink":
		s.unlinks++
		w.WriteHeader(http.StatusNoContent)
	default:
		fail(w, 404, "not_found")
	}
}
