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
		{proto: "tcp", address: "0.0.0.0", port: 22, state: "0A", inode: 10},
		{proto: "tcp", address: "100.110.34.31", port: 8443, state: "0A", inode: 11},
		{proto: "tcp", address: "127.0.0.1", port: 5432, state: "0A", inode: 12},
		// Two workers with SO_REUSEPORT: one endpoint, one row.
		{proto: "tcp", address: "0.0.0.0", port: 80, state: "0A", inode: 13},
		{proto: "tcp", address: "0.0.0.0", port: 80, state: "0A", inode: 14},
		// An established connection is not a listener.
		{proto: "tcp", address: "10.0.0.1", port: 22, remotePort: 51000, state: "01", inode: 15},
		{proto: "udp", address: "57.131.21.87", port: 68, inode: 16},
		// A connected UDP socket is a DNS lookup, not a service.
		{proto: "udp", address: "10.0.0.1", port: 40000, remotePort: 53, state: "01", inode: 17},
		// Created and never bound: it listens on nothing.
		{proto: "udp", address: "0.0.0.0", port: 0, inode: 18},
	}
	holders := map[uint64][]int32{
		10: {1, 2450808},
		11: {2066},
		13: {900},
		14: {901},
		16: {998},
	}
	got := listenersFrom(sockets, holders)
	want := []Listener{
		{Protocol: "tcp", Address: "0.0.0.0", Port: 22, PID: 2450808, Scope: ScopeAll, Exposed: true},
		{Protocol: "udp", Address: "57.131.21.87", Port: 68, PID: 998, Scope: ScopeInterface, Exposed: true},
		{Protocol: "tcp", Address: "0.0.0.0", Port: 80, PID: 900, Scope: ScopeAll, Exposed: true},
		{Protocol: "tcp", Address: "127.0.0.1", Port: 5432, PID: 0, Scope: ScopeLoopback, Exposed: false},
		{Protocol: "tcp", Address: "100.110.34.31", Port: 8443, PID: 2066, Scope: ScopeInterface, Exposed: true},
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

// ssh.socket: systemd holds port 22 and so does the sshd it started. The
// listing used to name PID 1, /sbin/init, because the /proc walk met it first.
func TestOwnerOfNamesTheDaemonRatherThanInit(t *testing.T) {
	for _, c := range []struct {
		holders []int32
		want    int32
	}{
		{[]int32{1, 2450808}, 2450808},
		{[]int32{2450808, 1}, 2450808},
		{[]int32{1, 901, 900}, 900},
		{[]int32{1}, 1},
		{nil, 0},
	} {
		if got := ownerOf(c.holders); got != c.want {
			t.Errorf("ownerOf(%v) = %d, want %d", c.holders, got, c.want)
		}
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
	// sockets maps a descriptor number to the socket inode it holds.
	sockets map[int]uint64
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
			sshd: {name: "sshd", cmdline: []string{"sshd: /usr/sbin/sshd -D [listener] 0 of 10-100 startups"}, sockets: map[int]uint64{3: 127916755, 4: 127915939}},
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
