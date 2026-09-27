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
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
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
		if want := reachForScope[l.Scope]; !want[l.Reach] {
			t.Errorf("%s %s:%d: reach %q with scope %q", l.Protocol, l.Address, l.Port, l.Reach, l.Scope)
		}
		if l.Port == 0 {
			t.Errorf("a socket on port 0 was listed: %+v", l)
		}
		if l.Protocol == "tcp" && l.Address == "127.0.0.1" && l.Port == ownPort {
			found = true
			if l.PID != int32(os.Getpid()) || l.Scope != proxysvc.ScopeLoopback || l.Reach != "loopback" {
				t.Errorf("this test's own listener = %+v", l)
			}
		}
	}
	if !found {
		t.Errorf("this test's own listener on port %d was not listed", ownPort)
	}
}

var reachForScope = map[proxysvc.BindScope]map[string]bool{
	proxysvc.ScopeLoopback:  {"loopback": true},
	proxysvc.ScopeInterface: {"host": true, "network": true, "public": true},
	proxysvc.ScopeAll:       {"all": true},
}

// A socket on a Docker or libvirt bridge address is graded as this host's
// alone, from the interface the address is on — the grade the posture levels
// its finding by. Runs where the host has such a bridge.
func TestPortListGradesABridgeAddressAsThisHosts(t *testing.T) {
	var bridge net.IP
	for _, a := range netsec.ReadHostNetwork(context.Background()).Addresses {
		if a.Kind == "bridge" && !a.DefaultRoute && a.IP.To4() != nil {
			bridge = a.IP
			break
		}
	}
	if bridge == nil {
		t.Skip("no bridge address on this host")
	}
	own, err := net.Listen("tcp", net.JoinHostPort(bridge.String(), "0"))
	if err != nil {
		t.Skipf("cannot listen on %s: %v", bridge, err)
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
	port := uint32(own.Addr().(*net.TCPAddr).Port)
	for _, l := range listeners {
		if l.Address == bridge.String() && l.Port == port {
			if l.Scope != proxysvc.ScopeInterface || !l.Exposed || l.Reach != "host" {
				t.Errorf("the socket on %s = %+v, want an exposed interface bind graded host", bridge, l)
			}
			return
		}
	}
	t.Errorf("the socket on %s:%d was not listed", bridge, port)
}
