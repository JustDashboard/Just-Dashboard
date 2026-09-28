package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
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
		if want := networksForReach[l.Reach]; !want[l.Network] {
			t.Errorf("%s %s:%d: network %q with reach %q", l.Protocol, l.Address, l.Port, l.Network, l.Reach)
		}
		if l.Family != "ipv4" && l.Family != "ipv6" {
			t.Errorf("%s %s:%d: family %q", l.Protocol, l.Address, l.Port, l.Family)
		}
		if l.Interface != "" && l.Scope != proxysvc.ScopeInterface {
			t.Errorf("%s %s:%d: interface %q on a %s bind", l.Protocol, l.Address, l.Port, l.Interface, l.Scope)
		}
		if l.Port == 0 {
			t.Errorf("a socket on port 0 was listed: %+v", l)
		}
		if l.Protocol == "tcp" && l.Address == "127.0.0.1" && l.Port == ownPort {
			found = true
			if l.PID != int32(os.Getpid()) || l.Scope != proxysvc.ScopeLoopback || l.Reach != "loopback" ||
				l.Network != "loopback" || l.Family != "ipv4" {
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

var networksForReach = map[string]map[string]bool{
	"loopback": {"loopback": true},
	"host":     {"docker": true, "bridge": true},
	"network":  {"tailnet": true, "vpn": true, "uplink": true, "private": true, "link-local": true},
	"public":   {"public": true},
	"all":      {"all": true},
}

// listedPorts is GET /ports as the page reads it.
func listedPorts(t *testing.T) []proxysvc.Listener {
	t.Helper()
	c, _ := newClient(t)
	w := c.do(http.MethodGet, "/api/v1/ports", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /ports = %d: %s", w.Code, w.Body.String())
	}
	var listeners []proxysvc.Listener
	if err := json.Unmarshal(w.Body.Bytes(), &listeners); err != nil {
		t.Fatal(err)
	}
	return listeners
}

// A socket on one address of the host's own is named by the network and the
// interface that address is on: the tailnet on tailscale0, Docker's bridge on
// docker0, loopback in either family. Each case runs where the host has such
// an address; the sockets are this test's own, on ports the kernel picks.
func TestPortListNamesTheNetworkEachSocketIsOn(t *testing.T) {
	host := netsec.ReadHostNetwork(context.Background())
	type bind struct {
		ip                  net.IP
		network, iface, fam string
	}
	binds := []bind{
		{net.ParseIP("127.0.0.1"), "loopback", "", "ipv4"},
		{net.ParseIP("::1"), "loopback", "", "ipv6"},
	}
	for _, a := range host.Addresses {
		place := host.Place(a.IP.String())
		if a.IP.IsLinkLocalUnicast() || (place.Network != netsec.NetworkTailnet && place.Network != netsec.NetworkDocker) {
			continue
		}
		fam := "ipv4"
		if a.IP.To4() == nil {
			fam = "ipv6"
		}
		binds = append(binds, bind{a.IP, string(place.Network), a.Interface, fam})
	}
	var open []net.Listener
	defer func() {
		for _, l := range open {
			l.Close()
		}
	}()
	ports := map[string]bind{}
	for _, b := range binds {
		l, err := net.Listen("tcp", net.JoinHostPort(b.ip.String(), "0"))
		if err != nil {
			t.Logf("cannot listen on %s: %v", b.ip, err)
			continue
		}
		open = append(open, l)
		ports[fmt.Sprintf("%s:%d", b.ip, l.Addr().(*net.TCPAddr).Port)] = b
	}
	if len(ports) == 0 {
		t.Skip("cannot listen here")
	}
	seen := 0
	for _, l := range listedPorts(t) {
		b, ok := ports[fmt.Sprintf("%s:%d", l.Address, l.Port)]
		if !ok || l.Protocol != "tcp" {
			continue
		}
		seen++
		t.Logf("%s:%d %s on %q, %s", l.Address, l.Port, l.Network, l.Interface, l.Family)
		if l.Network != b.network || l.Interface != b.iface || l.Family != b.fam || l.PID != int32(os.Getpid()) {
			t.Errorf("the socket on %s:%d = %+v, want network %q on %q, %s", l.Address, l.Port, l, b.network, b.iface, b.fam)
		}
	}
	if seen != len(ports) {
		t.Errorf("listed %d of this test's %d sockets", seen, len(ports))
	}
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
			if l.Scope != proxysvc.ScopeInterface || !l.Exposed || l.Reach != "host" ||
				!networksForReach["host"][l.Network] || l.Interface == "" {
				t.Errorf("the socket on %s = %+v, want an exposed interface bind graded host, on a named bridge", bridge, l)
			}
			return
		}
	}
	t.Errorf("the socket on %s:%d was not listed", bridge, port)
}

// A database's level on the ports page is the posture's, graded by the same
// rules with the same firewall: on a host whose firewall denies inbound by
// default the page used to draw red what the posture drew amber. The socket
// is a database port on a bridge address, which the posture levels a warning
// whatever the firewall says; beside it, a socket on no catalogued port has
// no level at all.
func TestPortListLevelsADatabaseAsThePostureDoes(t *testing.T) {
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
	var database net.Listener
	for _, port := range []string{"6379", "27017", "11211", "9200", "5432"} {
		if l, err := net.Listen("tcp", net.JoinHostPort(bridge.String(), port)); err == nil {
			database = l
			break
		}
	}
	if database == nil {
		t.Skipf("no database port is free on %s", bridge)
	}
	defer database.Close()
	plain, err := net.Listen("tcp", net.JoinHostPort(bridge.String(), "0"))
	if err != nil {
		t.Skipf("cannot listen on %s: %v", bridge, err)
	}
	defer plain.Close()

	dbPort := uint32(database.Addr().(*net.TCPAddr).Port)
	plainPort := uint32(plain.Addr().(*net.TCPAddr).Port)
	seen := 0
	// The port's level on the page is its highest socket's, as the posture's
	// one finding for it is at its widest bind.
	portLevel := ""
	for _, l := range listedPorts(t) {
		if l.Protocol == "tcp" && l.Port == dbPort && l.Level != "" && portLevel != "critical" {
			portLevel = l.Level
		}
		if l.Protocol != "tcp" || l.Address != bridge.String() {
			continue
		}
		switch l.Port {
		case dbPort:
			seen++
			if l.Level != "warning" {
				t.Errorf("the database on %s:%d = %+v, want the posture's warning", bridge, dbPort, l)
			}
		case plainPort:
			seen++
			if l.Level != "" || l.InboundDefault != "" {
				t.Errorf("the socket on %s:%d = %+v, want no level", bridge, plainPort, l)
			}
		}
	}
	if seen != 2 {
		t.Fatalf("listed %d of this test's 2 sockets on %s", seen, bridge)
	}

	c, _ := newClient(t)
	w := c.do(http.MethodGet, "/api/v1/security/posture", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /security/posture = %d: %s", w.Code, w.Body.String())
	}
	var posture netsec.Posture
	if err := json.Unmarshal(w.Body.Bytes(), &posture); err != nil {
		t.Fatal(err)
	}
	id := fmt.Sprintf("ports.exposed.tcp.%d", dbPort)
	for _, f := range posture.Findings {
		if f.ID == id {
			if f.Level != portLevel {
				t.Errorf("the posture levels %s %q, the ports page %q", id, f.Level, portLevel)
			}
			return
		}
	}
	t.Errorf("the posture raised no %s", id)
}

// GET /ports carries the posture's grade whole, why the firewall does not
// hold a socket included. On a host whose ufw denies inbound by default, the
// page drew amber and credited the firewall for a Postgres Docker publishes
// on every interface — which Docker forwards before ufw's default is met —
// and for a Redis a rule opens to anywhere.
func TestPortListSaysWhatGetsPastTheFirewall(t *testing.T) {
	ufw := &netsec.FirewallStatus{
		Backend: netsec.BackendUFW, Available: true, Enabled: true,
		Policy: netsec.DefaultPolicy{Incoming: "deny"},
		Rules: []netsec.Rule{{Number: 10, Action: "ALLOW", Direction: "IN", From: "Anywhere",
			To: "6379/tcp", Port: "6379", Protocol: "tcp"}},
	}
	listeners := []proxysvc.Listener{
		{Protocol: "tcp", Family: "ipv4", Address: "0.0.0.0", Port: 5432, Process: "docker-proxy", Exposed: true},
		{Protocol: "tcp", Family: "ipv4", Address: "0.0.0.0", Port: 6379, Process: "redis-server", Exposed: true},
		{Protocol: "tcp", Family: "ipv4", Address: "0.0.0.0", Port: 27017, Process: "mongod", Exposed: true},
	}
	placeListeners(listeners, netsec.HostNetwork{}, ufw)
	body, err := json.Marshal(listeners)
	if err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	for i, want := range []map[string]any{
		{"level": "critical", "pastFirewall": "docker"},
		{"level": "critical", "pastFirewall": "rule", "firewallRule": float64(10)},
		{"level": "warning", "inboundDefault": "deny"},
	} {
		for _, key := range []string{"level", "inboundDefault", "pastFirewall", "firewallRule"} {
			if got[i][key] != want[key] {
				t.Errorf("port %v: %s = %v, want %v (%s)", got[i]["port"], key, got[i][key], want[key], body)
			}
		}
	}
}

// GET /ports/meta says which ports the kernel hands out on its own, read from
// the same process table as the sockets, to any account that may read the
// list; where the range cannot be read it says so with null rather than a
// guessed default the page would hide ports by.
func TestPortsMetaSaysWhichPortsTheKernelHandsOut(t *testing.T) {
	s := testServer(t)
	h := s.Routes()
	reader := &client{t: t, h: h, cookie: signInAs(t, s, "reader", auth.RoleReadOnly)}
	root := t.TempDir()
	t.Setenv("HOST_PROC", root)

	w := reader.do(http.MethodGet, "/api/v1/ports/meta", "", nil)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"ephemeralRange":null}` {
		t.Fatalf("GET /ports/meta with no sysctl = %d %s, want a null range", w.Code, w.Body.String())
	}

	dir := filepath.Join(root, "sys", "net", "ipv4")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ip_local_port_range"), []byte("40000\t50000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w = reader.do(http.MethodGet, "/api/v1/ports/meta", "", nil)
	if w.Code != http.StatusOK ||
		strings.TrimSpace(w.Body.String()) != `{"ephemeralRange":{"low":40000,"high":50000}}` {
		t.Fatalf("GET /ports/meta = %d %s, want 40000-50000", w.Code, w.Body.String())
	}
}

// A socket the dashboard's own process holds is marked as the dashboard's,
// and says when its process started, as the processes page reads it.
func TestPortListMarksTheDashboardsOwnSockets(t *testing.T) {
	own, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot listen here: %v", err)
	}
	defer own.Close()
	port := uint32(own.Addr().(*net.TCPAddr).Port)
	s := testServer(t)
	detail, err := s.modules.table.Detail(context.Background(), int32(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range listedPorts(t) {
		if l.Address != "127.0.0.1" || l.Port != port {
			continue
		}
		if !l.Self {
			t.Errorf("the dashboard's own socket is not marked as its own: %+v", l)
		}
		if l.StartedAt == nil || !l.StartedAt.Equal(detail.CreateTime) {
			t.Errorf("startedAt = %v, want the processes page's %v", l.StartedAt, detail.CreateTime)
		}
		return
	}
	t.Fatalf("own socket on port %d was not listed", port)
}

// A deployment's containers carry its environment's ID; the listing names the
// project and environment, and a container whose environment is gone keeps
// its container name alone. A container the dashboard did not start is not
// taken for a deployment's by a label someone copied.
func TestPortListNamesTheDeploymentAContainerRunsFor(t *testing.T) {
	s := testServer(t)
	res, err := s.Store.DB.Exec(`INSERT INTO deploy_projects(name, repo_path, hook_secret, hook_id, created_at) VALUES('shop', '/srv/shop', 'sealed', 'shop-hook', 1)`)
	if err != nil {
		t.Fatal(err)
	}
	projectID, _ := res.LastInsertId()
	res, err = s.Store.DB.Exec(`INSERT INTO deploy_environments(project_id, name, slug, kind, created_at, updated_at) VALUES(?, 'Production', 'production-ports', 'production', 1, 1)`, projectID)
	if err != nil {
		t.Fatal(err)
	}
	environmentID, _ := res.LastInsertId()

	containers := runningContainers([]dockerx.Container{
		{ID: "e175e175e175e175", Name: "jd-e175-r4", Image: "c9051a2ac152", Labels: map[string]string{
			"io.just-dashboard.managed": "true", "io.just-dashboard.environment-id": fmt.Sprint(environmentID),
		}, Ports: []dockerx.Port{{IP: "127.0.0.1", PrivatePort: 3000, PublicPort: 39069, Type: "tcp"}}},
		{ID: "e999e999e999e999", Name: "jd-e999-r1", Image: "nginx", Labels: map[string]string{
			"io.just-dashboard.managed": "true", "io.just-dashboard.environment-id": "999999",
		}, Ports: []dockerx.Port{{IP: "127.0.0.1", PrivatePort: 80, PublicPort: 39070, Type: "tcp"}}},
		{ID: "c0ffeec0ffeec0ff", Name: "copycat", Image: "nginx", Labels: map[string]string{
			"io.just-dashboard.environment-id": fmt.Sprint(environmentID),
		}, Ports: []dockerx.Port{{IP: "127.0.0.1", PrivatePort: 80, PublicPort: 39071, Type: "tcp"}}},
	})
	listeners := proxysvc.AttributeOwners(nil, proxysvc.OwnerInput{Containers: containers})
	s.nameDeployments(context.Background(), listeners)

	want := map[uint32]string{39069: fmt.Sprintf("shop/Production/%d", projectID), 39070: "", 39071: ""}
	for _, l := range listeners {
		got := ""
		if d := l.Container.Deployment; d != nil {
			got = fmt.Sprintf("%s/%s/%d", d.Project, d.Environment, d.ProjectID)
		}
		if got != want[l.Port] {
			t.Errorf("port %d (%s) deployment = %q, want %q", l.Port, l.Container.Name, got, want[l.Port])
		}
	}
	if len(listeners) != 3 {
		t.Errorf("%d rows, want the three published ports", len(listeners))
	}
}

// A port Docker publishes is graded as Docker's whatever holds it — a holder
// this account cannot see, or none at all — as the posture grades it.
func TestPortListGradesAPortDockerPublishesAsDockers(t *testing.T) {
	deny := &netsec.FirewallStatus{Available: true, Enabled: true, Policy: netsec.DefaultPolicy{Incoming: "deny"}}
	listeners := proxysvc.AttributeOwners([]proxysvc.Listener{
		{Protocol: "tcp", Family: "ipv4", Address: "0.0.0.0", Port: 5432, Scope: proxysvc.ScopeAll, Exposed: true},
	}, proxysvc.OwnerInput{Containers: []proxysvc.RunningContainer{{
		ID: "9a8b7c6d5e4f3a2b", Name: "db", Image: "postgres:16-alpine",
		Ports: []proxysvc.PublishedPort{{HostIP: "0.0.0.0", HostPort: 5432, Protocol: "tcp"}, {HostIP: "::", HostPort: 5432, Protocol: "tcp"}},
	}}})
	placeListeners(listeners, netsec.HostNetwork{}, deny)
	if len(listeners) != 2 {
		t.Fatalf("%d rows, want the socket and the NAT-only IPv6 binding: %+v", len(listeners), listeners)
	}
	for _, l := range listeners {
		if l.Level != "critical" || l.PastFirewall != "docker" || l.InboundDefault != "" {
			t.Errorf("%s:%d (%q) = level %q past %q default %q, want critical past the firewall", l.Address, l.Port, l.Source, l.Level, l.PastFirewall, l.InboundDefault)
		}
	}
}
