package api

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

func TestReservationChecksNameFuturePolicyConflicts(t *testing.T) {
	ufw := &netsec.FirewallStatus{Backend: netsec.BackendUFW, Available: true, Enabled: true, Rules: []netsec.Rule{
		{Number: 1, Action: "ALLOW", Direction: "IN", From: "Anywhere", To: "8080/tcp", Port: "8080", Protocol: "tcp", Raw: "8080/tcp ALLOW IN Anywhere"},
		{Number: 2, Action: "DENY", Direction: "IN", From: "203.0.113.0/24", To: "7000:7010", Port: "7000:7010", Raw: "7000:7010 DENY IN 203.0.113.0/24"},
		{Number: 3, Action: "ALLOW", Direction: "OUT", From: "Anywhere", To: "9000/tcp", Port: "9000", Protocol: "tcp", Raw: "9000/tcp ALLOW OUT Anywhere"},
	}}
	firewall := firewallCheck(ufw, "tcp")
	if r, ok := firewall(8080); !ok || !strings.Contains(r.Detail, "admits it from anywhere") {
		t.Fatalf("an allow rule already decides the port: %+v %v", r, ok)
	}
	if r, ok := firewall(7005); !ok || !strings.Contains(r.Detail, "refuses it from 203.0.113.0/24") {
		t.Fatalf("a deny range already decides the port: %+v %v", r, ok)
	}
	if _, ok := firewall(9000); ok {
		t.Fatal("an outbound rule does not decide who reaches a listener")
	}
	if _, ok := firewallCheck(ufw, "udp")(8080); ok {
		t.Fatal("a TCP rule does not decide a UDP port")
	}

	gateway := gatewayCheck([]netx.ForwardView{{Name: "game", Protocol: "both", Interface: "eth0", Ports: "25565-25575", Target: "10.0.0.5", TargetPort: "25565", Enabled: true},
		{Name: "off", Protocol: "tcp", Ports: "8443", Enabled: false}}, "udp")
	if r, ok := gateway(25570); !ok || r.Source != "gateway" {
		t.Fatalf("a gateway forward translates the port away: %+v %v", r, ok)
	}
	if _, ok := gateway(8443); ok {
		t.Fatal("a disabled forward translates nothing")
	}

	if _, ok := previewCheck("tcp", "0.0.0.0")(21004); !ok {
		t.Fatal("a wildcard bind meets the preview range on the tailnet address")
	}
	if _, ok := previewCheck("tcp", "100.101.102.103")(21999); !ok {
		t.Fatal("a tailnet bind meets the preview range")
	}
	if _, ok := previewCheck("tcp", "127.0.0.1")(21004); ok {
		t.Fatal("a loopback bind does not meet tailscaled's tailnet listener")
	}
	if _, ok := ephemeralCheck(proxysvc.PortRange{Low: 32768, High: 60999})(40000); !ok {
		t.Fatal("the ephemeral range is passed over")
	}

	leases := leaseCheck([]portLease{{Address: "127.0.0.1", Port: 15000, Protocol: "tcp", Environment: "shop · production", ExpiresAt: time.Unix(0, 0)}}, "tcp", "0.0.0.0")
	if r, ok := leases(15000); !ok || !strings.Contains(r.Detail, "shop · production") {
		t.Fatalf("a wildcard bind meets a loopback lease: %+v %v", r, ok)
	}
	if _, ok := leaseCheck([]portLease{{Address: "127.0.0.1", Port: 15000, Protocol: "tcp"}}, "tcp", "10.0.0.1")(15000); ok {
		t.Fatal("a bind on another address does not meet a loopback lease")
	}
}

func TestFreePortsPassOverLeasesAndSayWhatWasNotSupplied(t *testing.T) {
	s, router := gatewayRouter(t, auth.RoleAdmin, false)
	router.Route("/ports", s.mountPortRoutes)
	port := 0
	for candidate := 15000; candidate < 19999; candidate++ {
		if l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", candidate)); err == nil {
			l.Close()
			port = candidate
			break
		}
	}
	if port == 0 {
		t.Skip("no free loopback port below the ephemeral range")
	}
	project, err := s.Store.DB.Exec(`INSERT INTO deploy_projects(name, repo_path, hook_secret, hook_id, created_at) VALUES('shop', '/srv/shop', 'sealed', 'shop-hook', 1)`)
	if err != nil {
		t.Fatal(err)
	}
	projectID, _ := project.LastInsertId()
	environment, err := s.Store.DB.Exec(`INSERT INTO deploy_environments(project_id,name,slug,kind,created_at,updated_at) VALUES(?,'production','production','production',1,1)`, projectID)
	if err != nil {
		t.Fatal(err)
	}
	environmentID, _ := environment.LastInsertId()
	if _, err := s.Store.DB.Exec(`INSERT INTO deploy_port_leases(environment_id, address, port, protocol, purpose, token, expires_at, created_at) VALUES(?, '127.0.0.1', ?, 'tcp', 'candidate', 'token', ?, 1)`,
		environmentID, port, time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	rec := gwDo(router, http.MethodGet, fmt.Sprintf("/ports/free?address=127.0.0.1&from=%d&count=1", port), "")
	var out portsFree
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		t.Fatalf("free: %d %s", rec.Code, rec.Body.String())
	}
	if len(out.Ports) != 1 || out.Ports[0] == port {
		t.Fatalf("a leased port must be passed over: %+v", out)
	}
	if len(out.Reservations) == 0 || out.Reservations[0].Port != port || out.Reservations[0].Source != "deployments" || !strings.Contains(out.Reservations[0].Detail, "shop · production") {
		t.Fatalf("the lease is named: %+v", out.Reservations)
	}
	states := map[string]string{}
	for _, source := range out.Sources {
		states[source.Key] = source.State
	}
	if states["provider"] != portSourceNotSupplied || states["firewall"] != portSourceChecked || states["deployments"] != portSourceChecked || states["sockets"] != portSourceChecked {
		t.Fatalf("every source says whether it was read: %+v", out.Sources)
	}
	for _, source := range out.Sources {
		if source.Key == "firewall" && !strings.Contains(source.Detail, "loopback") {
			t.Fatalf("a loopback search says why inbound policy does not apply: %+v", source)
		}
	}
	// The policy alone, without a bind on a routable address: a wildcard
	// search needs the firewall and gateway owners, which are unread here.
	states = map[string]string{}
	for _, source := range s.freePortPolicy(t.Context(), "tcp", "0.0.0.0").sources {
		states[source.Key] = source.State
	}
	if states["firewall"] != portSourceUnavailable || states["gateway"] != portSourceUnavailable {
		t.Fatalf("a wildcard search needs the firewall and gateway owners: %+v", states)
	}
}
