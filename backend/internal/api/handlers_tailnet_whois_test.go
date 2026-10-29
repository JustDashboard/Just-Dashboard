package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/config"
	"github.com/Wayy01/Just-Dashboard/backend/internal/tailnetgate"
)

type fakeWhoIs map[netip.Addr]tailnetgate.Peer

func (f fakeWhoIs) WhoIs(_ context.Context, address netip.Addr) (tailnetgate.Peer, error) {
	if peer, ok := f[address]; ok {
		return peer, nil
	}
	return tailnetgate.Peer{}, tailnetgate.ErrNotAPeer
}

// The route is mounted behind the network allowlist with no session to ask
// for, answers the proxy on loopback, refuses a call another proxy forwarded
// in the allowlist's own words, and writes each refusal to the audit trail.
func TestTailnetWhoisRouteAnswersOnlyTheProxyAndAuditsRefusals(t *testing.T) {
	s := testServer(t)
	h := s.Routes()
	gate := s.modules.tailnetGate
	if gate == nil {
		t.Fatal("the tailnet gate's route is not wired")
	}
	alice := netip.MustParseAddr("100.101.102.103")
	gate.WhoIs = fakeWhoIs{alice: {Address: alice, Login: "alice@example.com", Name: "Alice"}}
	gate.Access = func(_ context.Context, environmentID int64) ([]string, error) {
		if environmentID == 7 {
			return []string{"alice@example.com"}, nil
		}
		return nil, nil
	}
	ask := func(peer string, header ...string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tailnet/whois?environment=7", nil)
		req.RemoteAddr = "127.0.0.1:41000"
		req.Header.Set(tailnetgate.PeerHeader, peer)
		for i := 0; i+1 < len(header); i += 2 {
			req.Header.Set(header[i], header[i+1])
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	if w := ask("100.101.102.103"); w.Code != http.StatusNoContent || w.Header().Get(tailnetgate.LoginHeader) != "alice@example.com" {
		t.Fatalf("alice = %d %v %s", w.Code, w.Header(), w.Body.String())
	}
	// The dashboard's own Caddy forwards a browser to the API from loopback
	// too, and says so: a signed-in operator cannot ask who holds an address.
	if w := ask("100.101.102.103", "X-Forwarded-For", "100.101.102.103"); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "network_denied") {
		t.Fatalf("a forwarded caller = %d %s", w.Code, w.Body.String())
	}
	if w := ask("100.70.0.1"); w.Code != http.StatusForbidden {
		t.Fatalf("an address nobody holds = %d", w.Code)
	}
	var target, ip, detail string
	var success bool
	if err := s.Store.DB.QueryRow(`SELECT target, ip, detail, success FROM audit_log WHERE action = 'deploy.tailnet.refused' AND target = '7' ORDER BY id DESC LIMIT 1`).
		Scan(&target, &ip, &detail, &success); err != nil || ip != "100.70.0.1" || success || !strings.Contains(detail, `"reason":"not_on_tailnet"`) {
		t.Fatalf("audited refusal = %q %q %q %v, %v", target, ip, detail, success, err)
	}
	var caller string
	if err := s.Store.DB.QueryRow(`SELECT detail FROM audit_log WHERE action = 'deploy.tailnet.refused' AND target = '' ORDER BY id DESC LIMIT 1`).
		Scan(&caller); err != nil || !strings.Contains(caller, `"reason":"caller"`) {
		t.Fatalf("audited caller refusal = %q, %v", caller, err)
	}
	// Outside the network allowlist it is refused before the handler runs,
	// like every API route.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tailnet/whois?environment=7", nil)
	req.RemoteAddr = "203.0.113.5:9999"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "network_denied") {
		t.Fatalf("off the allowlist = %d %s", w.Code, w.Body.String())
	}
}

func TestTailnetGateAPIIsTheBackendOnLoopback(t *testing.T) {
	s := &Server{Cfg: &config.Config{}}
	for addr, want := range map[string]string{
		"127.0.0.1:8080":  "http://127.0.0.1:8080",
		"0.0.0.0:9090":    "http://127.0.0.1:9090",
		":7070":           "http://127.0.0.1:7070",
		"100.64.0.1:8080": "",
		"bad":             "",
	} {
		s.Cfg.Addr = addr
		if got := s.tailnetGateAPI(); got != want {
			t.Errorf("%s = %q, want %q", addr, got, want)
		}
	}
}
