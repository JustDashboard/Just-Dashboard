package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gnet "github.com/shirou/gopsutil/v4/net"
)

// Lines copied from this host's /proc/net tables (Ubuntu 25.04, kernel 6.14),
// with `ss` as the reading each was checked against: sshd on every interface
// in both families, caddy on the tailnet address, tailscaled's IPv6 tailnet
// port, systemd-networkd's DHCP client on the public address, and
// systemd-resolved's stub on 127.0.0.53. The last TCP line is a TIME_WAIT
// socket, which has no inode.
const (
	hostTCP = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
  20: 1F226E64:20FB 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 408579124 2 0000000000000000 100 0 0 10 0
  28: 3500007F:0035 00000000:0000 0A 00000000:00000000 00:00000000 00000000   990        0 4308 1 0000000000000000 100 0 0 10 5
  45: 00000000:0016 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 127916755 1 0000000000000000 100 0 0 10 0
 167: 0100007F:861E 3500007F:0035 06 00000000:00000000 03:00000742 00000000     0        0 0 3 0000000000000000
`
	hostTCP6 = `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   1: 5C117AFD0000E0A1000000002022379E:CCEF 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 255986565 2 0000000000000000 100 0 0 10 0
   3: 00000000000000000000000000000000:0016 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 127915939 1 0000000000000000 100 0 0 10 0
`
	hostUDP = `   sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode ref pointer drops
 2492: 1F226E64:20FB 00000000:0000 07 00000000:00000000 00:00000000 00000000     0        0 408579129 2 0000000000000000 0
10486: 3500007F:0035 00000000:0000 07 00000000:00000000 00:00000000 00000000   990        0 4307 2 0000000000000000 0
10501: 57158339:0044 00000000:0000 07 00000000:00000000 00:00000000 00000000   998        0 411881343 2 0000000000000000 0
`
)

func TestParseSocketTableReadsThisHostsAddresses(t *testing.T) {
	got := map[string]socketRow{}
	for _, table := range []struct{ content, proto string }{
		{hostTCP, "tcp"}, {hostTCP6, "tcp"}, {hostUDP, "udp"},
	} {
		for _, row := range parseSocketTable([]byte(table.content), table.proto) {
			got[fmt.Sprintf("%s %s", row.proto, net.JoinHostPort(row.address, fmt.Sprint(row.port)))] = row
		}
	}
	for key, inode := range map[string]uint64{
		"tcp 0.0.0.0:22":                        127916755,
		"tcp [::]:22":                           127915939,
		"tcp 100.110.34.31:8443":                408579124,
		"tcp [fd7a:115c:a1e0::9e37:2220]:52463": 255986565,
		"tcp 127.0.0.53:53":                     4308,
		"udp 100.110.34.31:8443":                408579129,
		"udp 57.131.21.87:68":                   411881343,
		"udp 127.0.0.53:53":                     4307,
		"tcp 127.0.0.1:34334":                   0,
	} {
		row, ok := got[key]
		if !ok {
			t.Errorf("%s was not read; got %v", key, got)
			continue
		}
		if row.inode != inode {
			t.Errorf("%s inode = %d, want %d", key, row.inode, inode)
		}
	}
	if timeWait := got["tcp 127.0.0.1:34334"]; timeWait.listening() {
		t.Error("a TIME_WAIT socket is not listening")
	}
}

// A socket on one specific address is reachable by whoever can route to
// that address. It used to be called loopback because it was not a wildcard.
func TestBindScopeTellsLoopbackFromOneAddressFromEveryInterface(t *testing.T) {
	for address, want := range map[string]BindScope{
		"0.0.0.0":                   ScopeAll,
		"::":                        ScopeAll,
		"*":                         ScopeAll,
		"":                          ScopeAll,
		"127.0.0.1":                 ScopeLoopback,
		"127.0.0.53":                ScopeLoopback,
		"::1":                       ScopeLoopback,
		"100.110.34.31":             ScopeInterface,
		"fd7a:115c:a1e0::9e37:2220": ScopeInterface,
		"57.131.21.87":              ScopeInterface,
		"203.0.113.5":               ScopeInterface,
		"10.0.0.1":                  ScopeInterface,
		"fe80::1":                   ScopeInterface,
	} {
		if got := bindScope(address); got != want {
			t.Errorf("bindScope(%q) = %q, want %q", address, got, want)
		}
	}
}

func TestListenersFromKeepsWhatAcceptsAndSaysHowFarItReaches(t *testing.T) {
	sockets := []socketRow{
		{proto: "tcp", family: "ipv4", address: "0.0.0.0", port: 22, state: "0A", inode: 10},
		// sshd's other family: the same service, a socket of its own.
		{proto: "tcp", family: "ipv6", address: "::", port: 22, state: "0A", inode: 19},
		{proto: "tcp", family: "ipv4", address: "100.110.34.31", port: 8443, state: "0A", inode: 11},
		{proto: "tcp", family: "ipv4", address: "127.0.0.1", port: 5432, state: "0A", inode: 12},
		// Two workers with SO_REUSEPORT: one endpoint, one row.
		{proto: "tcp", family: "ipv4", address: "0.0.0.0", port: 80, state: "0A", inode: 13},
		{proto: "tcp", family: "ipv4", address: "0.0.0.0", port: 80, state: "0A", inode: 14},
		// An established connection is not a listener.
		{proto: "tcp", family: "ipv4", address: "10.0.0.1", port: 22, remotePort: 51000, state: "01", inode: 15},
		{proto: "udp", family: "ipv4", address: "57.131.21.87", port: 68, inode: 16},
		// A connected UDP socket is a DNS lookup, not a service.
		{proto: "udp", family: "ipv4", address: "10.0.0.1", port: 40000, remotePort: 53, state: "01", inode: 17},
		// Created and never bound: it listens on nothing.
		{proto: "udp", family: "ipv4", address: "0.0.0.0", port: 0, inode: 18},
	}
	holders := map[uint64][]int32{
		10: {1, 2450808},
		19: {1, 2450808},
		11: {2066},
		13: {900},
		14: {901},
		16: {998},
	}
	// The two port-80 workers are siblings forked by 900.
	got := listenersFrom(sockets, holders, map[int32]int32{2450808: 1, 900: 1, 901: 900})
	want := []Listener{
		{Protocol: "tcp", Family: "ipv4", Address: "0.0.0.0", Port: 22, PID: 2450808, PPID: 1, Scope: ScopeAll, Exposed: true},
		{Protocol: "tcp", Family: "ipv6", Address: "::", Port: 22, PID: 2450808, PPID: 1, Scope: ScopeAll, Exposed: true},
		// A parent that could not be read is none.
		{Protocol: "udp", Family: "ipv4", Address: "57.131.21.87", Port: 68, PID: 998, Scope: ScopeInterface, Exposed: true},
		{Protocol: "tcp", Family: "ipv4", Address: "0.0.0.0", Port: 80, PID: 900, PPID: 1, Scope: ScopeAll, Exposed: true},
		{Protocol: "tcp", Family: "ipv4", Address: "127.0.0.1", Port: 5432, PID: 0, Scope: ScopeLoopback, Exposed: false},
		{Protocol: "tcp", Family: "ipv4", Address: "100.110.34.31", Port: 8443, PID: 2066, Scope: ScopeInterface, Exposed: true},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d listeners, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("listener %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// Each socket's family is the kernel table it is in: tcp and udp are IPv4,
// tcp6 and udp6 IPv6. The page folds a service's two into one row by it.
func TestListListenersReadsEachSocketsFamilyFromItsTable(t *testing.T) {
	root := fakeProc(t, map[string]string{"tcp": hostTCP, "tcp6": hostTCP6, "udp": hostUDP}, nil)
	t.Setenv("HOST_PROC", root)
	listeners, err := ListListeners(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, l := range listeners {
		got[endpointKey(l.Protocol, l.Address, l.Port)] = l.Family
	}
	for key, family := range map[string]string{
		"tcp 0.0.0.0:22":                        "ipv4",
		"tcp [::]:22":                           "ipv6",
		"tcp 100.110.34.31:8443":                "ipv4",
		"tcp [fd7a:115c:a1e0::9e37:2220]:52463": "ipv6",
		"udp 57.131.21.87:68":                   "ipv4",
	} {
		if got[key] != family {
			t.Errorf("%s family = %q, want %q", key, got[key], family)
		}
	}
}

// ssh.socket: systemd holds port 22 and so does the sshd it started. The
// listing used to name PID 1, /sbin/init, because the /proc walk met it first.
// A prefork server's owner is its master, found by parentage rather than by
// the lowest PID: after the PID counter wraps, the workers a reload respawns
// are numbered below the master that forked them.
func TestOwnerOfNamesTheDaemonRatherThanInit(t *testing.T) {
	for _, c := range []struct {
		name    string
		holders []int32
		parents map[int32]int32
		want    int32
	}{
		{"socket-activated sshd", []int32{1, 2450808}, map[int32]int32{2450808: 1}, 2450808},
		{"init met last", []int32{2450808, 1}, map[int32]int32{2450808: 1}, 2450808},
		{"master below its workers", []int32{1, 900, 901, 902}, map[int32]int32{900: 1, 901: 900, 902: 900}, 900},
		{"master above its workers after a wrap", []int32{1, 3000000, 2000000, 2000001},
			map[int32]int32{3000000: 1, 2000000: 3000000, 2000001: 3000000}, 3000000},
		{"master whose parent could not be read", []int32{3000000, 2000000}, map[int32]int32{2000000: 3000000}, 3000000},
		{"unrelated holders: the lowest", []int32{800, 700}, map[int32]int32{700: 1, 800: 1}, 700},
		{"no parent known: the lowest", []int32{1, 901, 900}, nil, 900},
		{"init alone", []int32{1}, nil, 1},
		{"nobody", nil, nil, 0},
	} {
		if got := ownerOf(c.holders, c.parents); got != c.want {
			t.Errorf("%s: ownerOf(%v, %v) = %d, want %d", c.name, c.holders, c.parents, got, c.want)
		}
	}
}

func TestParentFromStatCountsPastTheCommandName(t *testing.T) {
	for stat, want := range map[string]int32{
		"812 (nginx) S 1 812 812 0 -1 4194624 2 0 0 0":                   1,
		"4242 (a) b (c)) S 812 4242 4242 0 -1 4194560 118 0 0 0":         812,
		"2000000 (nginx: worker process) S 3000000 3000000 3000000 0 -1": 3000000,
	} {
		if got, ok := parentFromStat(stat); !ok || got != want {
			t.Errorf("parentFromStat(%q) = %d, %v; want %d", stat, got, ok, want)
		}
	}
	if _, ok := parentFromStat("812 (nginx"); ok {
		t.Error("a truncated stat line gave a parent")
	}
}

// fakeProc lays out the parts of /proc the listing reads: the socket tables,
// and for each PID its descriptors (as the kernel's "socket:[inode]" links)
// and the files gopsutil names a process from.
func fakeProc(t *testing.T, tables map[string]string, procs map[int]fakeProcess) string {
	t.Helper()
	root := t.TempDir()
	write := func(path, content string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range tables {
		write(filepath.Join(root, "net", name), content)
	}
	for pid, p := range procs {
		dir := filepath.Join(root, fmt.Sprint(pid))
		write(filepath.Join(dir, "comm"), p.name+"\n")
		write(filepath.Join(dir, "cmdline"), strings.Join(p.cmdline, "\x00")+"\x00")
		write(filepath.Join(dir, "status"), "Name:\t"+p.name+"\nUid:\t0\t0\t0\t0\nGid:\t0\t0\t0\t0\n")
		write(filepath.Join(dir, "stat"), fmt.Sprintf("%d (%s) S %d %d %d 0 -1 4194560 0 0 0 0\n", pid, p.name, p.parent, pid, pid))
		if p.cgroup != "" {
			write(filepath.Join(dir, "cgroup"), p.cgroup)
		}
		if err := os.MkdirAll(filepath.Join(dir, "fd"), 0o755); err != nil {
			t.Fatal(err)
		}
		for fd, inode := range p.sockets {
			if err := os.Symlink(fmt.Sprintf("socket:[%d]", inode), filepath.Join(dir, "fd", fmt.Sprint(fd))); err != nil {
				t.Fatal(err)
			}
		}
		// A descriptor that is not a socket is passed over.
		if err := os.Symlink("/dev/null", filepath.Join(dir, "fd", "0")); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

type fakeProcess struct {
	name    string
	cmdline []string
	parent  int
	// sockets maps a descriptor number to the socket inode it holds.
	sockets map[int]uint64
	// cgroup is /proc/<pid>/cgroup, left out when empty.
	cgroup string
}

// The whole listing over a /proc laid out as this host's is: port 22 held by
// init and by sshd. The owner is the test process standing in for sshd,
// because gopsutil confirms a PID exists by signalling it.
func TestListListenersNamesTheSocketActivatedDaemon(t *testing.T) {
	sshd := os.Getpid()
	root := fakeProc(t,
		map[string]string{"tcp": hostTCP, "tcp6": hostTCP6, "udp": hostUDP},
		map[int]fakeProcess{
			1:    {name: "systemd", cmdline: []string{"/sbin/init"}, sockets: map[int]uint64{367: 127916755, 368: 127915939}},
			sshd: {name: "sshd", cmdline: []string{"sshd: /usr/sbin/sshd -D [listener] 0 of 10-100 startups"}, parent: 1, sockets: map[int]uint64{3: 127916755, 4: 127915939}},
		})
	t.Setenv("HOST_PROC", root)

	listeners, err := ListListeners(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, l := range listeners {
		if l.Port == 0 {
			t.Errorf("a socket on port 0 was listed: %+v", l)
		}
		if l.Port != 22 {
			continue
		}
		found++
		if l.PID != int32(sshd) || l.Process != "sshd" || l.User != "root" || !strings.HasPrefix(l.Cmdline, "sshd: /usr/sbin/sshd") {
			t.Errorf("port 22 on %s is owned by %+v, want sshd (PID %d)", l.Address, l, sshd)
		}
		if l.Scope != ScopeAll || !l.Exposed {
			t.Errorf("port 22 on %s: scope %q exposed %v", l.Address, l.Scope, l.Exposed)
		}
	}
	if found != 2 {
		t.Errorf("port 22 listed %d times, want once per family: %+v", found, listeners)
	}
}

// nginx after the PID counter wrapped: its master kept the PID it started
// with, and the worker a reload respawned was numbered below it. The row is
// the master's, which the old lowest-PID rule gave to the worker. The test
// process stands in for the master, as gopsutil names only a PID that exists.
func TestListListenersNamesTheMasterAboveItsWorkers(t *testing.T) {
	master := os.Getpid()
	worker := 2
	root := fakeProc(t,
		map[string]string{"tcp": hostTCP, "udp": hostUDP},
		map[int]fakeProcess{
			master: {name: "nginx", cmdline: []string{"nginx: master process /usr/sbin/nginx"}, parent: 1, sockets: map[int]uint64{6: 127916755}},
			worker: {name: "nginx", cmdline: []string{"nginx: worker process"}, parent: master, sockets: map[int]uint64{6: 127916755}},
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
		if l.PID != int32(master) || !strings.HasPrefix(l.Cmdline, "nginx: master process") {
			t.Errorf("port 22 is owned by PID %d %q, want the master (PID %d)", l.PID, l.Cmdline, master)
		}
		return
	}
	t.Fatalf("port 22 was not listed: %+v", listeners)
}

// Docker publishes a port with one docker-proxy per family: here port 22's
// IPv4 socket is one proxy's and its IPv6 socket another's, both dockerd's
// children. Each is listed with its own PID and the parent they share, which
// is what the page folds them into one service by. The test process and its
// parent stand in for the proxies, as gopsutil names only a PID that exists.
func TestListListenersNamesEachDockerProxyAndItsParent(t *testing.T) {
	proxy4, proxy6, dockerd := os.Getpid(), os.Getppid(), 1755428
	cmdline := func(hostIP string) []string {
		return []string{"/usr/bin/docker-proxy", "-proto", "tcp", "-host-ip", hostIP, "-host-port", "22",
			"-container-ip", "10.0.0.3", "-container-port", "22", "-use-listen-fd"}
	}
	root := fakeProc(t,
		map[string]string{"tcp": hostTCP, "tcp6": hostTCP6, "udp": hostUDP},
		map[int]fakeProcess{
			proxy4: {name: "docker-proxy", cmdline: cmdline("0.0.0.0"), parent: dockerd, sockets: map[int]uint64{3: 127916755}},
			proxy6: {name: "docker-proxy", cmdline: cmdline("::"), parent: dockerd, sockets: map[int]uint64{3: 127915939}},
		})
	t.Setenv("HOST_PROC", root)

	listeners, err := ListListeners(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	owners := map[string][2]int32{}
	for _, l := range listeners {
		if l.Port == 22 {
			if l.Process != "docker-proxy" {
				t.Errorf("port 22 on %s is %q, want docker-proxy", l.Address, l.Process)
			}
			owners[l.Family] = [2]int32{l.PID, l.PPID}
		}
	}
	want := map[string][2]int32{"ipv4": {int32(proxy4), int32(dockerd)}, "ipv6": {int32(proxy6), int32(dockerd)}}
	if fmt.Sprint(owners) != fmt.Sprint(want) {
		t.Errorf("port 22's owners (PID, parent) = %v, want %v", owners, want)
	}
}

// A kernel booted without IPv6 has no tcp6 or udp6; that is four sockets
// fewer, not a failed listing.
func TestListListenersReadsAHostWithoutIPv6(t *testing.T) {
	root := fakeProc(t, map[string]string{"tcp": hostTCP, "udp": hostUDP}, nil)
	t.Setenv("HOST_PROC", root)
	listeners, err := ListListeners(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(listeners) != 6 {
		t.Errorf("got %d listeners, want the six IPv4 ones: %+v", len(listeners), listeners)
	}
}

// The walk over every process's descriptors is the slow part on a busy host,
// and the handler's deadline only means something if the walk honours it.
func TestListListenersStopsWhenItsContextDoes(t *testing.T) {
	root := fakeProc(t, map[string]string{"tcp": hostTCP, "udp": hostUDP}, map[int]fakeProcess{
		os.Getpid(): {name: "sshd", sockets: map[int]uint64{3: 127916755}},
	})
	t.Setenv("HOST_PROC", root)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ListListeners(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

// Read against the real kernel, the listing agrees with gopsutil — which it
// replaced — on which endpoints are accepting, and finds this process's own
// sockets as this process's. Sockets that open or close between the reads are
// not held against it: only endpoints gopsutil saw both before and after.
func TestListListenersAgreesWithTheKernelOnThisHost(t *testing.T) {
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot listen here: %v", err)
	}
	defer tcp.Close()
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot bind here: %v", err)
	}
	defer udp.Close()

	ctx := context.Background()
	before := gopsutilEndpoints(t, ctx)
	listeners, err := ListListeners(ctx)
	if err != nil {
		t.Fatal(err)
	}
	after := gopsutilEndpoints(t, ctx)

	ours := map[string]Listener{}
	for _, l := range listeners {
		ours[endpointKey(l.Protocol, l.Address, l.Port)] = l
	}
	for key := range before {
		if after[key] {
			if _, ok := ours[key]; !ok {
				t.Errorf("gopsutil saw %s before and after, the listing did not", key)
			}
		}
	}
	for _, own := range []struct {
		proto string
		addr  net.Addr
	}{{"tcp", tcp.Addr()}, {"udp", udp.LocalAddr()}} {
		host, port, _ := net.SplitHostPort(own.addr.String())
		key := own.proto + " " + net.JoinHostPort(host, port)
		l, ok := ours[key]
		if !ok {
			t.Errorf("this process's %s was not listed", key)
			continue
		}
		if l.PID != int32(os.Getpid()) || l.Scope != ScopeLoopback || l.Exposed {
			t.Errorf("%s = %+v, want this process on loopback", key, l)
		}
	}
}

func endpointKey(proto, address string, port uint32) string {
	return proto + " " + net.JoinHostPort(address, fmt.Sprint(port))
}

func gopsutilEndpoints(t *testing.T, ctx context.Context) map[string]bool {
	t.Helper()
	conns, err := gnet.ConnectionsWithContext(ctx, "inet")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, c := range conns {
		if c.Laddr.Port == 0 {
			continue
		}
		switch {
		case c.Type == 1 && c.Status == "LISTEN":
			out[endpointKey("tcp", c.Laddr.IP, c.Laddr.Port)] = true
		case c.Type == 2 && c.Raddr.Port == 0:
			out[endpointKey("udp", c.Laddr.IP, c.Laddr.Port)] = true
		}
	}
	return out
}

// On a host where systemd holds a socket for a daemon it started — Ubuntu's
// ssh.socket — the listing names the daemon. Reading init's descriptors
// needs root, so this runs only as root; it reads /proc and changes nothing.
func TestListListenersNamesTheDaemonNotInitOnThisHost(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("reading init's descriptors needs root")
	}
	ctx := context.Background()
	sockets, err := readSockets("/proc")
	if err != nil {
		t.Fatal(err)
	}
	wanted := map[uint64]bool{}
	for _, s := range sockets {
		if s.listening() {
			wanted[s.inode] = true
		}
	}
	holders, err := socketHolders(ctx, "/proc", wanted)
	if err != nil {
		t.Fatal(err)
	}
	shared := map[string][]int32{}
	for _, s := range sockets {
		pids := holders[s.inode]
		if !s.listening() || len(pids) < 2 {
			continue
		}
		for _, pid := range pids {
			if pid == 1 {
				shared[endpointKey(s.proto, s.address, s.port)] = pids
			}
		}
	}
	if len(shared) == 0 {
		t.Skip("no listener on this host is held by init and a daemon")
	}
	listeners, err := ListListeners(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range listeners {
		key := endpointKey(l.Protocol, l.Address, l.Port)
		pids, ok := shared[key]
		if !ok {
			continue
		}
		t.Logf("%s held by %v, listed as %s (PID %d)", key, pids, l.Process, l.PID)
		if l.PID == 1 {
			t.Errorf("%s is listed as init though %v hold it", key, pids)
		}
	}
}

// The sysctl as the kernel writes it — two numbers split by a tab — and the
// shapes that would leave the page hiding the wrong ports if read loosely.
func TestParsePortRangeReadsTheSysctl(t *testing.T) {
	for _, tc := range []struct {
		content string
		want    PortRange
		err     bool
	}{
		{"32768\t60999\n", PortRange{32768, 60999}, false},
		{"1024 65535", PortRange{1024, 65535}, false},
		{"40000\t40000\n", PortRange{40000, 40000}, false},
		{"", PortRange{}, true},
		{"32768\n", PortRange{}, true},
		{"60999\t32768\n", PortRange{}, true},
		{"0\t60999\n", PortRange{}, true},
		{"32768\t70000\n", PortRange{}, true},
		{"low\thigh\n", PortRange{}, true},
		{"32768 40000 60999", PortRange{}, true},
	} {
		got, err := parsePortRange(tc.content)
		if (err != nil) != tc.err || got != tc.want {
			t.Errorf("parsePortRange(%q) = %+v, %v; want %+v, error %v", tc.content, got, err, tc.want, tc.err)
		}
	}
}

// The range comes from the same process table the sockets do, HOST_PROC
// included, and a table without it is an error rather than a guessed range.
func TestEphemeralPortsReadsTheProcessTableTheSocketsComeFrom(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOST_PROC", root)
	if _, err := EphemeralPorts(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("EphemeralPorts() with no sysctl = %v, want a missing file", err)
	}
	dir := filepath.Join(root, "sys", "net", "ipv4")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ip_local_port_range"), []byte("40000\t50000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := EphemeralPorts()
	if err != nil || got != (PortRange{40000, 50000}) {
		t.Fatalf("EphemeralPorts() = %+v, %v; want 40000-50000", got, err)
	}
}

// On this host the range read is the kernel's own.
func TestEphemeralPortsIsTheKernelsOnThisHost(t *testing.T) {
	content, err := os.ReadFile("/proc/sys/net/ipv4/ip_local_port_range")
	if err != nil {
		t.Skipf("no sysctl here: %v", err)
	}
	fields := strings.Fields(string(content))
	got, err := EphemeralPorts()
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("%d %d", got.Low, got.High); want != strings.Join(fields, " ") {
		t.Errorf("EphemeralPorts() = %s, the kernel says %q", want, content)
	}
}
