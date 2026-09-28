package proxysvc

import (
	"context"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

// node names its main thread "MainThread", and the kernel names the process
// after it: every node server on this host is listed as MainThread. The
// executable says what it is; where another account's executable cannot be
// read, the command line's first word does.
func TestDisplayNamePrefersTheExecutableForAThreadName(t *testing.T) {
	for _, c := range []struct {
		comm, exe, cmdline, want string
	}{
		{"MainThread", "/home/ubuntu/.nvm/versions/node/v24.12.0/bin/node", "node server.js", "node"},
		{"MainThread", "", "/usr/bin/node /srv/app/server.js", "node"},
		{"MainThread", "/usr/bin/node (deleted)", "", "node"},
		{".NET ThreadPool", "/usr/share/dotnet/dotnet", "dotnet api.dll", "dotnet"},
		// A program named for itself keeps its name, a truncated one too.
		{"nginx", "/usr/sbin/nginx", "nginx: master process /usr/sbin/nginx", ""},
		{"threadweaver", "/usr/local/bin/threadweaver", "threadweaver --serve", ""},
		{"threadweaver-se", "/usr/local/bin/threadweaver-server", "", ""},
		// A title the program chose is its own business.
		{"PM2 v7.0.4: God", "/usr/bin/node", "PM2 v7.0.4: God Daemon (/root/.pm2)", ""},
		{"MainThread", "", "", ""},
	} {
		if got := displayName(c.comm, c.exe, c.cmdline); got != c.want {
			t.Errorf("displayName(%q, %q, %q) = %q, want %q", c.comm, c.exe, c.cmdline, got, c.want)
		}
	}
}

// The listing names a node server by its program, keeps the kernel's name
// for the code that matches on it, and reads who supervises it from its
// cgroup under the same process table. The test process stands in for the
// server, as gopsutil names only a PID that exists.
func TestListListenersNamesAThreadNamedServerByItsProgram(t *testing.T) {
	pid := os.Getpid()
	root := fakeProc(t,
		map[string]string{"tcp": hostTCP, "udp": hostUDP},
		map[int]fakeProcess{
			pid: {
				name:    "MainThread",
				cmdline: []string{"/home/ubuntu/.nvm/versions/node/v24.12.0/bin/node", "/srv/app/server.js"},
				parent:  1,
				sockets: map[int]uint64{9: 127916755},
				cgroup:  "0::/system.slice/app.service\n",
			},
		})
	t.Setenv("HOST_PROC", root)

	listeners, err := ListListeners(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range listeners {
		if l.Port != 22 {
			continue
		}
		if l.Process != "MainThread" || l.DisplayName != "node" {
			t.Errorf("process %q shown as %q, want MainThread shown as node", l.Process, l.DisplayName)
		}
		if l.Manager != "systemd" || l.ManagerName != "app.service" {
			t.Errorf("manager = %s %q, want systemd app.service", l.Manager, l.ManagerName)
		}
		return
	}
	t.Fatalf("port 22 was not listed: %+v", listeners)
}

// startedAt is the processes page's own reading of the owner's start, which
// POST /processes/{pid}/signal compares to refuse a PID reused since.
func TestListListenersSaysWhenTheOwnerStarted(t *testing.T) {
	own, err := listenLoopback(t)
	if err != nil {
		t.Skipf("cannot listen here: %v", err)
	}
	listeners, err := ListListeners(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	p, err := process.NewProcess(int32(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	created, err := p.CreateTime()
	if err != nil {
		t.Fatal(err)
	}
	want := time.UnixMilli(created).UTC()
	for _, l := range listeners {
		if l.Address != "127.0.0.1" || l.Port != own {
			continue
		}
		if l.PID == 0 {
			t.Skip("this account cannot see its own descriptors here")
		}
		if l.StartedAt == nil || !l.StartedAt.Equal(want) {
			t.Errorf("startedAt = %v, want %v", l.StartedAt, want)
		}
		return
	}
	t.Fatalf("own socket on port %d was not listed", own)
}

// systemd prints a socket unit's address as the kernel's table spells it,
// but for brackets and a link-local address's interface. Anything that is
// not an inet stream or datagram socket is not a port.
func TestSocketUnitAtReadsSystemdsAddresses(t *testing.T) {
	for _, c := range []struct {
		listen, kind string
		want         *SocketUnit
	}{
		{"0.0.0.0:22", "Stream", &SocketUnit{Protocol: "tcp", Address: "0.0.0.0", Port: 22}},
		{"[::]:22", "Stream", &SocketUnit{Protocol: "tcp", Address: "::", Port: 22}},
		{"[::1]:53", "Datagram", &SocketUnit{Protocol: "udp", Address: "::1", Port: 53}},
		{"[fe80::1%eth0]:22", "Stream", &SocketUnit{Protocol: "tcp", Address: "fe80::1", Port: 22}},
		{"vsock::22", "Stream", nil},
		{"/run/docker.sock", "Stream", nil},
		{"@ISCSIADM_ABSTRACT_NAMESPACE", "Stream", nil},
		{"route 1361", "Netlink", nil},
		{"0.0.0.0:22", "FIFO", nil},
		{"127.0.0.1:0", "Stream", nil},
	} {
		got, ok := SocketUnitAt(c.listen, c.kind, "x.socket", "x.service")
		if c.want == nil {
			if ok {
				t.Errorf("%q %s read as %+v", c.listen, c.kind, got)
			}
			continue
		}
		c.want.Unit, c.want.Activates = "x.socket", "x.service"
		if !ok || got != *c.want {
			t.Errorf("%q %s = %+v %v, want %+v", c.listen, c.kind, got, ok, *c.want)
		}
	}
}

// The containers this host runs: Docker's Caddy ingress on 80 and 443 in both
// families, a Postgres published on loopback, and the dashboard's own compose
// project — its backend mounting the data directory, its frontend published
// on loopback, its proxy on the host's network.
func hostContainers() []RunningContainer {
	return []RunningContainer{
		{
			ID: "5e3ac6b0d3f1c07a9a0f5e4a3c2b1d0e9f8a7b6c5d4e3f2a1b0c9d8e7f6a5b4c", Name: "just-dashboard-ingress", Image: "caddy:2-alpine",
			Ports: []PublishedPort{
				{"0.0.0.0", 80, "tcp"}, {"::", 80, "tcp"}, {"0.0.0.0", 443, "tcp"}, {"::", 443, "tcp"},
				{"", 0, "udp"}, {"", 0, "tcp"},
			},
		},
		{
			ID: "9a8b7c6d5e4f3a2b1c0d9e8f7a6b5c4d3e2f1a0b9c8d7e6f5a4b3c2d1e0f9a8b", Name: "Qhahdhhas", Image: "postgres:16-alpine",
			Ports: []PublishedPort{{"127.0.0.1", 5432, "tcp"}},
		},
		{
			ID: "0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0", Name: "just-dashboard-backend-1", Image: "just-dashboard-backend:latest",
			Project: "just-dashboard", Service: "backend",
			Mounts: []string{"/var/run/docker.sock", "/var/lib/just-dashboard", "/etc"},
		},
		{
			ID: "7c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d", Name: "just-dashboard-frontend-1", Image: "just-dashboard-frontend:latest",
			Project: "just-dashboard", Service: "frontend",
			Ports: []PublishedPort{{"127.0.0.1", 3000, "tcp"}},
		},
		{
			ID: "c13e58c8f7b4822ff505f3e86405a1cb386156d3cc6a3ca4068fb715fb98b2db", Name: "just-dashboard-proxy-1", Image: "caddy:2-alpine",
			Project: "just-dashboard", Service: "proxy",
		},
		{
			ID: "e175e175e175e175e175e175e175e175e175e175e175e175e175e175e175e175", Name: "jd-e175-r4", Image: "c9051a2ac152",
			EnvironmentID: 175,
			Ports:         []PublishedPort{{"127.0.0.1", 39069, "tcp"}},
		},
	}
}

func dockerProxy(port uint32, family, address string, pid int32) Listener {
	return Listener{
		Protocol: "tcp", Family: family, Address: address, Port: port, PID: pid, PPID: 1755428,
		Process: "docker-proxy", User: "root", Scope: bindScope(address), Exposed: bindScope(address) != ScopeLoopback,
		Manager: "systemd", ManagerName: "docker.service",
	}
}

func byEndpoint(listeners []Listener) map[string]Listener {
	out := map[string]Listener{}
	for _, l := range listeners {
		out[endpointKey(l.Protocol, l.Address, l.Port)] = l
	}
	return out
}

// docker-proxy holds a published port and names no container: the binding
// Docker reports for the same protocol, host address and port does.
func TestAttributeOwnersJoinsDockerProxyToItsContainer(t *testing.T) {
	listeners := AttributeOwners([]Listener{
		dockerProxy(80, "ipv4", "0.0.0.0", 1883643),
		dockerProxy(80, "ipv6", "::", 1883650),
		dockerProxy(443, "ipv4", "0.0.0.0", 1883666),
		dockerProxy(443, "ipv6", "::", 1883672),
		dockerProxy(5432, "ipv4", "127.0.0.1", 1750348),
		dockerProxy(3000, "ipv4", "127.0.0.1", 3300),
		// Root-owned descriptors are unreadable to a dashboard that is not
		// root: the socket is listed with no holder, and still joins.
		{Protocol: "tcp", Family: "ipv4", Address: "127.0.0.1", Port: 39069, Scope: ScopeLoopback},
	}, OwnerInput{Containers: hostContainers()})

	if len(listeners) != 7 {
		t.Fatalf("%d rows, want the 7 sockets and no port Docker publishes without one: %+v", len(listeners), listeners)
	}
	for _, c := range []struct {
		key, name, image string
		pid              int32
	}{
		{"tcp 0.0.0.0:80", "just-dashboard-ingress", "caddy:2-alpine", 1883643},
		{"tcp [::]:80", "just-dashboard-ingress", "caddy:2-alpine", 1883650},
		{"tcp 0.0.0.0:443", "just-dashboard-ingress", "caddy:2-alpine", 1883666},
		{"tcp [::]:443", "just-dashboard-ingress", "caddy:2-alpine", 1883672},
		{"tcp 127.0.0.1:5432", "Qhahdhhas", "postgres:16-alpine", 1750348},
		{"tcp 127.0.0.1:3000", "just-dashboard-frontend-1", "just-dashboard-frontend:latest", 3300},
		{"tcp 127.0.0.1:39069", "jd-e175-r4", "c9051a2ac152", 0},
	} {
		l, ok := byEndpoint(listeners)[c.key]
		if !ok {
			t.Errorf("%s is missing", c.key)
			continue
		}
		if l.Container == nil || l.Container.Name != c.name || l.Container.Image != c.image || !l.Container.Published {
			t.Errorf("%s: container %+v, want %s (%s), published", c.key, l.Container, c.name, c.image)
		}
		if l.PID != c.pid || l.Source != "" || l.Self {
			t.Errorf("%s: pid %d source %q self %v, want pid %d, a socket, not the dashboard's", c.key, l.PID, l.Source, l.Self, c.pid)
		}
	}
	deployed := byEndpoint(listeners)["tcp 127.0.0.1:39069"].Container
	if deployed.ID != "e175e175e175" || deployed.EnvironmentID != 175 {
		t.Errorf("deployment container = %+v, want the short ID and environment 175", deployed)
	}
}

// With Docker's userland proxy off, a published port is NAT rules and no
// socket: the listing had no row for it at all. Each binding is its own row,
// named for its container; a binding with a socket is not listed twice, and
// a port the container exposes without publishing is not listed.
func TestAttributeOwnersAddsPortsDockerPublishesWithoutASocket(t *testing.T) {
	listeners := AttributeOwners([]Listener{
		dockerProxy(80, "ipv4", "0.0.0.0", 1883643),
		dockerProxy(80, "ipv6", "::", 1883650),
		{Protocol: "udp", Family: "ipv4", Address: "127.0.0.53", Port: 53, PID: 790, Process: "systemd-resolve", Scope: ScopeLoopback},
	}, OwnerInput{Containers: hostContainers()[:2]})

	var got []string
	for _, l := range listeners {
		got = append(got, fmt.Sprintf("%s %s %s:%d %s", l.Protocol, l.Family, l.Address, l.Port, l.Source))
		if l.Source != SourceDockerNAT {
			continue
		}
		if l.PID != 0 || l.Process != "" || l.Container == nil || !l.Container.Published {
			t.Errorf("NAT row %s:%d = %+v, want no process and its container, published", l.Address, l.Port, l)
		}
		if l.Scope != bindScope(l.Address) || l.Exposed != (l.Scope != ScopeLoopback) {
			t.Errorf("NAT row %s:%d: scope %s exposed %v", l.Address, l.Port, l.Scope, l.Exposed)
		}
	}
	want := []string{
		"udp ipv4 127.0.0.53:53 ",
		"tcp ipv4 0.0.0.0:80 ",
		"tcp ipv6 :::80 ",
		"tcp ipv4 0.0.0.0:443 docker-nat",
		"tcp ipv6 :::443 docker-nat",
		"tcp ipv4 127.0.0.1:5432 docker-nat",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("rows =\n%q\nwant\n%q", got, want)
	}
	if name := byEndpoint(listeners)["tcp 127.0.0.1:5432"].Container.Name; name != "Qhahdhhas" {
		t.Errorf("5432 is named %q, want Qhahdhhas", name)
	}
}

// A Docker older than 20.10 published every interface as one docker-proxy
// on the IPv6 wildcard, while the binding says 0.0.0.0. sshd on :: beside a
// container publishing IPv4's 22 is not the container's.
func TestAttributeOwnersJoinsAnOldDockersDualStackProxyOnly(t *testing.T) {
	listeners := AttributeOwners([]Listener{
		dockerProxy(8080, "ipv6", "::", 3100),
		{Protocol: "tcp", Family: "ipv6", Address: "::", Port: 22, PID: 2450808, Process: "sshd", Scope: ScopeAll, Exposed: true},
		dockerProxy(22, "ipv4", "0.0.0.0", 3200),
	}, OwnerInput{Containers: []RunningContainer{
		{ID: "aaaaaaaaaaaaaaaa", Name: "web", Image: "nginx:1.27", Ports: []PublishedPort{{"0.0.0.0", 8080, "tcp"}}},
		{ID: "bbbbbbbbbbbbbbbb", Name: "gitea", Image: "gitea/gitea", Ports: []PublishedPort{{"0.0.0.0", 22, "tcp"}}},
	}})
	rows := byEndpoint(listeners)
	if len(listeners) != 3 {
		t.Fatalf("%d rows, want 3 and no NAT row for a binding the old proxy holds: %+v", len(listeners), listeners)
	}
	if c := rows["tcp [::]:8080"].Container; c == nil || c.Name != "web" {
		t.Errorf(":::8080 container = %+v, want web", c)
	}
	if c := rows["tcp [::]:22"].Container; c != nil {
		t.Errorf("sshd on :: was given container %+v", c)
	}
	if c := rows["tcp 0.0.0.0:22"].Container; c == nil || c.Name != "gitea" {
		t.Errorf("0.0.0.0:22 container = %+v, want gitea", c)
	}
}

// A container on the host's network holds its socket itself; its cgroup
// names it, and the listing names the container.
func TestAttributeOwnersNamesAContainerOnTheHostNetwork(t *testing.T) {
	listeners := AttributeOwners([]Listener{{
		Protocol: "tcp", Family: "ipv4", Address: "100.110.34.31", Port: 8443, PID: 1802112, Process: "caddy",
		Scope: ScopeInterface, Exposed: true, Manager: "container", ManagerName: "c13e58c8f7b4",
	}}, OwnerInput{Containers: hostContainers()})
	c := byEndpoint(listeners)["tcp 100.110.34.31:8443"].Container
	if c == nil || c.Name != "just-dashboard-proxy-1" || c.Project != "just-dashboard" || c.Service != "proxy" || c.Published {
		t.Errorf("container = %+v, want just-dashboard-proxy-1 of just-dashboard/proxy, not published", c)
	}
}

// ssh.socket holds port 22 for ssh.service: the socket unit is named whether
// sshd holds the socket too or systemd holds it alone.
func TestAttributeOwnersNamesTheSocketUnit(t *testing.T) {
	var units []SocketUnit
	for _, listen := range []string{"0.0.0.0:22", "[::]:22"} {
		u, ok := SocketUnitAt(listen, "Stream", "ssh.socket", "ssh.service")
		if !ok {
			t.Fatalf("%s did not read", listen)
		}
		units = append(units, u)
	}
	listeners := AttributeOwners([]Listener{
		{Protocol: "tcp", Family: "ipv4", Address: "0.0.0.0", Port: 22, PID: 2450808, Process: "sshd", Manager: "systemd", ManagerName: "ssh.service", Scope: ScopeAll},
		{Protocol: "tcp", Family: "ipv6", Address: "::", Port: 22, PID: 1, Process: "systemd", Scope: ScopeAll},
		{Protocol: "udp", Family: "ipv4", Address: "0.0.0.0", Port: 22, PID: 999, Process: "other", Scope: ScopeAll},
	}, OwnerInput{SocketUnits: units})
	for _, l := range listeners {
		want := l.Protocol == "tcp"
		if (l.SocketUnit == "ssh.socket" && l.Activates == "ssh.service") != want {
			t.Errorf("%s %s:%d socket unit %q → %q", l.Protocol, l.Address, l.Port, l.SocketUnit, l.Activates)
		}
	}
}

// PM2 runs its apps in the session that started its daemon; its own list is
// what names an app.
func TestAttributeOwnersNamesThePM2App(t *testing.T) {
	listeners := AttributeOwners([]Listener{
		{Protocol: "tcp", Address: "127.0.0.1", Port: 3773, PID: 4242, Process: "MainThread", DisplayName: "node", Manager: "session", ManagerName: "session-c307.scope"},
		{Protocol: "tcp", Address: "127.0.0.1", Port: 3774, PID: 4243, Process: "node", Manager: "session", ManagerName: "session-c307.scope"},
	}, OwnerInput{PM2: map[int32]string{4242: "api", 0: "stopped"}})
	if l := listeners[0]; l.Manager != "pm2" || l.ManagerName != "api" {
		t.Errorf("PM2's app is %s %q, want pm2 api", l.Manager, l.ManagerName)
	}
	if l := listeners[1]; l.Manager != "session" {
		t.Errorf("a node started by hand became %s %q", l.Manager, l.ManagerName)
	}
}

// The dashboard's own sockets: its process, and every container of the
// compose project whose backend mounts its data directory — the frontend
// published on loopback and the proxy on the host's network. Docker's
// ingress, which the dashboard created, serves deployments, not the
// dashboard, and is not.
func TestAttributeOwnersMarksTheDashboardsOwnSockets(t *testing.T) {
	self := int32(os.Getpid())
	listeners := AttributeOwners([]Listener{
		{Protocol: "tcp", Family: "ipv4", Address: "127.0.0.1", Port: 8080, PID: self, Process: "jd-server", Scope: ScopeLoopback},
		dockerProxy(3000, "ipv4", "127.0.0.1", 3300),
		{Protocol: "tcp", Family: "ipv4", Address: "100.110.34.31", Port: 8443, PID: 1802112, Process: "caddy", Scope: ScopeInterface, Manager: "container", ManagerName: "c13e58c8f7b4"},
		dockerProxy(80, "ipv4", "0.0.0.0", 1883643),
		{Protocol: "tcp", Family: "ipv4", Address: "127.0.0.1", Port: 9000, PID: 5000, Process: "jd-server", Scope: ScopeLoopback},
	}, OwnerInput{Containers: hostContainers(), SelfPID: self, DataDir: "/var/lib/just-dashboard/"})

	mine := map[uint32]bool{}
	for _, l := range listeners {
		if l.Self {
			mine[l.Port] = true
		}
	}
	// 443, 5432 and 39069 are published without a socket here; none is ours.
	want := map[uint32]bool{8080: true, 3000: true, 8443: true}
	if fmt.Sprint(mine) != fmt.Sprint(want) {
		t.Errorf("the dashboard's own ports = %v, want %v", mine, want)
	}

	// Without the data directory mounted — the dashboard is not a container
	// — only its process is its own, however its neighbours are named.
	bare := AttributeOwners([]Listener{
		{Protocol: "tcp", Address: "127.0.0.1", Port: 8080, PID: self, Scope: ScopeLoopback},
		dockerProxy(3000, "ipv4", "127.0.0.1", 3300),
	}, OwnerInput{Containers: hostContainers(), SelfPID: self, DataDir: "/srv/other"})
	for _, l := range bare {
		if l.Self != (l.Port == 8080) {
			t.Errorf("with the dashboard not in a container, %s:%d self %v, want its process alone", l.Address, l.Port, l.Self)
		}
	}
}

// Two containers mounting the data directory — a backup job beside the
// backend — are settled by the compose service called backend.
func TestSelfProjectPrefersTheBackendWhereSeveralMountTheData(t *testing.T) {
	containers := []RunningContainer{
		{Name: "nightly-backup", Project: "backups", Service: "job", Mounts: []string{"/var/lib/just-dashboard"}},
		{Name: "just-dashboard-backend-1", Project: "just-dashboard", Service: "backend", Mounts: []string{"/var/lib/just-dashboard"}},
	}
	if got := selfProject(containers, "/var/lib/just-dashboard"); got != "just-dashboard" {
		t.Errorf("selfProject = %q, want just-dashboard", got)
	}
	if got := selfProject(containers[:1], ""); got != "" {
		t.Errorf("with no data directory, selfProject = %q", got)
	}
}

func listenLoopback(t *testing.T) (uint32, error) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	t.Cleanup(func() { l.Close() })
	return uint32(l.Addr().(*net.TCPAddr).Port), nil
}

// PM2 is asked for its apps only when a socket's owner is a PM2 daemon's
// child, as asking starts a daemon where none runs.
func TestUnderPM2ReadsTheOwnersParent(t *testing.T) {
	root := fakeProc(t, nil, map[int]fakeProcess{
		4181319: {name: "PM2 v7.0.4: God"},
		2000:    {name: "bash"},
	})
	t.Setenv("HOST_PROC", root)
	if !UnderPM2([]Listener{{PID: 4242, PPID: 2000}, {PID: 4243, PPID: 4181319}}) {
		t.Error("an app the PM2 daemon started was not seen")
	}
	if UnderPM2([]Listener{{PID: 4242, PPID: 2000}, {PID: 900, PPID: 1}, {PID: 0}}) {
		t.Error("PM2 asked about sockets no PM2 daemon started")
	}
}
