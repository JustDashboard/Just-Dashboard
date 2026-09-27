package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// The ports page polls this every fifteen seconds. A listing that outlives
// its deadline answers 504 and says it may be retried, rather than a page of
// requests queueing behind a stuck walk over /proc.
func TestPortListAnswersWithinItsDeadline(t *testing.T) {
	s := testServer(t)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/ports", nil).WithContext(ctx)
	w := httptest.NewRecorder()

	err := s.handlePortList(w, r)
	var apiErr *httpx.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (status %d), want an API error", err, w.Code)
	}
	if apiErr.Status != http.StatusGatewayTimeout || apiErr.Code != "timeout" || !apiErr.Retryable {
		t.Errorf("err = %+v, want a retryable 504 timeout", apiErr)
	}
}

// Every socket the host lists says how far it reaches, and exposed means
// anything but loopback — a socket on one tailnet or public address is not
// "loopback" because it is not 0.0.0.0.
func TestPortListSaysHowFarEachSocketReaches(t *testing.T) {
	own, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot listen here: %v", err)
	}
	defer own.Close()
	c, _ := newClient(t)

	w := c.do(http.MethodGet, "/api/v1/ports", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /ports = %d: %s", w.Code, w.Body.String())
	}
	var listeners []proxysvc.Listener
	if err := json.Unmarshal(w.Body.Bytes(), &listeners); err != nil {
		t.Fatal(err)
	}
	ownPort := uint32(own.Addr().(*net.TCPAddr).Port)
	found := false
	for _, l := range listeners {
		switch l.Scope {
		case proxysvc.ScopeLoopback, proxysvc.ScopeInterface, proxysvc.ScopeAll:
		default:
			t.Errorf("%s %s:%d has scope %q", l.Protocol, l.Address, l.Port, l.Scope)
		}
		if l.Exposed != (l.Scope != proxysvc.ScopeLoopback) {
			t.Errorf("%s %s:%d: exposed %v with scope %q", l.Protocol, l.Address, l.Port, l.Exposed, l.Scope)
		}
		if l.Port == 0 {
			t.Errorf("a socket on port 0 was listed: %+v", l)
		}
		if l.Protocol == "tcp" && l.Address == "127.0.0.1" && l.Port == ownPort {
			found = true
			if l.PID != int32(os.Getpid()) || l.Scope != proxysvc.ScopeLoopback {
				t.Errorf("this test's own listener = %+v", l)
			}
		}
	}
	if !found {
		t.Errorf("this test's own listener on port %d was not listed", ownPort)
	}
}
