// Package hubstub is a minimal stand-in for Just Hub, for the tests of the
// packages that drive a hub link: the terminal command and the HTTP handlers.
// It speaks just enough of hub link protocol 1 to be connected to, heartbeated
// to and disconnected from, at a loopback address the tests name as
// JD_HUB_LINK_TEST_ORIGIN. It does not verify signatures; the hublink
// package's own tests do that against a hub that does.
package hubstub

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

// ServerID is the id the stub gives the server it approves.
const ServerID = "7a0d3c1e-0000-4000-8000-000000000002"

type Hub struct {
	*httptest.Server
	t testing.TB

	mu       sync.Mutex
	status   string
	label    string
	tokens   map[string]bool
	requests int
	polls    int
	beats    int
	unlinks  int
	key      ed25519.PublicKey
	lastBody map[string]any
}

// New starts a hub that has one request to give out at a time.
func New(t testing.TB) *Hub {
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	h := &Hub{t: t, status: "pending", label: "hub", key: pub, tokens: map[string]bool{}}
	h.Server = httptest.NewServer(http.HandlerFunc(h.serve))
	t.Cleanup(h.Close)
	return h
}

func (h *Hub) set(status string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.status = status
}

// Approve, Deny and Expire decide the request the next poll will see.
func (h *Hub) Approve() { h.set("approved") }
func (h *Hub) Deny()    { h.set("denied") }
func (h *Hub) Expire()  { h.set("expired") }

// Token makes a one-time token that approves the request that names it.
func (h *Hub) Token() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	tok := "jht_" + strings.Repeat(fmt.Sprintf("%d", len(h.tokens)%10), 43)
	h.tokens[tok] = true
	return tok
}

// Count is how many of each thing the hub has been asked for.
func (h *Hub) Count() (requests, polls, heartbeats, unlinks int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.requests, h.polls, h.beats, h.unlinks
}

// LastRequest is the body of the last link request.
func (h *Hub) LastRequest() map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastBody
}

// WaitForRequest blocks until a link request has been created.
func (h *Hub) WaitForRequest() {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if r, _, _, _ := h.Count(); r > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.t.Fatal("no link request arrived")
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

func (h *Hub) serve(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/api/hub/v1")
	switch {
	case r.Method == http.MethodPost && path == "/requests":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		h.lastBody = body
		if tok, ok := body["token"].(string); ok {
			valid := h.tokens[tok]
			delete(h.tokens, tok)
			if !valid {
				fail(w, 403, "bad_token")
				return
			}
			h.status = "approved"
		} else {
			h.status = "pending"
		}
		h.requests++
		reply(w, 200, map[string]any{
			"requestId": fmt.Sprintf("2c9e41d7-0000-4000-8000-%012d", h.requests), "userCode": "K7QM-29XD",
			"approveUrl": h.URL + "/servers/pair?code=K7QM-29XD", "expiresAt": time.Now().Add(15 * time.Minute).Unix(), "pollSeconds": 2,
		})
	case strings.HasPrefix(path, "/requests/"):
		h.polls++
		switch h.status {
		case "approved":
			h.status = "consumed"
			reply(w, 200, map[string]any{
				"status": "approved", "serverId": ServerID,
				"siteKey":          map[string]string{"kid": "hub-2026-11", "publicKey": base64.StdEncoding.EncodeToString(h.key)},
				"heartbeatSeconds": 15, "pollWaitSeconds": 1, "owner": map[string]string{"label": h.label},
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
		h.beats++
		reply(w, 200, map[string]any{
			"ok": true, "heartbeatSeconds": 15, "pendingCommands": 0,
			"fleet": []map[string]any{{
				"serverId": ServerID, "name": "box", "endpoint": "https://box.tailed39ba.ts.net:8443",
				"state": "online", "version": "0.8.5", "self": true, "lastSeenAt": time.Now().Unix(),
			}},
			"notices": []map[string]any{{
				"id": "update-0.8.6", "level": "info", "title": "Update ready on 1 server", "body": "0.8.6 is out.",
				"href": h.URL + "/updates", "at": time.Now().Unix(),
			}},
		})
	case path == "/commands":
		reply(w, 200, map[string]any{"commands": []any{}})
	case path == "/unlink":
		h.unlinks++
		w.WriteHeader(http.StatusNoContent)
	default:
		fail(w, 404, "not_found")
	}
}
