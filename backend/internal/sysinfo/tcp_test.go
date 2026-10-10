package sysinfo

import (
	"net"
	"net/netip"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// The fixtures are this kernel's own /proc/net/snmp and /proc/net/netstat,
// copied as they were.
func TestTCPCountersAreReadByColumnName(t *testing.T) {
	prevSNMP, prevNetstat := snmpPath, netstatPath
	snmpPath, netstatPath = filepath.Join("testdata", "proc-net-snmp.txt"), filepath.Join("testdata", "proc-net-netstat.txt")
	t.Cleanup(func() { snmpPath, netstatPath = prevSNMP, prevNetstat })
	c, ok := ReadTCPCounters()
	if !ok {
		t.Fatal("fixtures not read")
	}
	want := TCPCounters{ActiveOpens: 24030263, PassiveOpens: 4482575, AttemptFails: 1311457, EstabResets: 748750, OutSegs: 1566434322,
		RetransSegs: 9694688, InErrs: 390600, ListenOverflows: 0, ListenDrops: 210, Timeouts: 249990}
	if c != want {
		t.Fatalf("counters=%+v", c)
	}
	snmpPath = filepath.Join(t.TempDir(), "missing")
	if _, ok := ReadTCPCounters(); ok {
		t.Fatal("a missing MIB read as zeroes")
	}
}

func TestTCPRatesAreDeltasAndRefuseCountersThatWentBack(t *testing.T) {
	prev := TCPCounters{OutSegs: 1000, RetransSegs: 10, AttemptFails: 5, EstabResets: 2, ListenDrops: 0}
	cur := TCPCounters{OutSegs: 3000, RetransSegs: 50, AttemptFails: 15, EstabResets: 2, ListenDrops: 30}
	s := tcpRates(prev, cur, 10)
	if s.OutSegsRate != 200 || s.RetransRate != 4 || s.AttemptFailsRate != 1 || s.ListenDropsRate != 3 || s.RetransPercent != 2 {
		t.Fatalf("rates=%+v", s)
	}
	reset := tcpRates(cur, prev, 10)
	if reset.OutSegsRate != 0 || reset.RetransRate != 0 || reset.RetransPercent != 0 {
		t.Fatalf("a counter that went back made a rate: %+v", reset)
	}
	if first := tcpRates(prev, cur, 0); first.RetransRate != 0 || !first.Supported {
		t.Fatalf("no interval made a rate: %+v", first)
	}
}

func TestLatencySummaryIsNearestRankAndSaysWhenNothingWasMeasured(t *testing.T) {
	l := summariseRTTs([]float64{40, 10, 30, 20, 100, 25, 35, 15, 45, 50}, true)
	if l.Sockets != 10 || l.MedianMs != 30 || l.P90Ms != 50 {
		t.Fatalf("summary=%+v", l)
	}
	if empty := summariseRTTs(nil, true); empty.Sockets != 0 || empty.MedianMs != 0 || !empty.Supported {
		t.Fatalf("empty=%+v", empty)
	}
	for addr, external := range map[string]bool{
		"127.0.0.1": false, "10.0.0.5": false, "172.17.0.2": false, "192.168.1.9": false, "fe80::1": false,
		"fd7a:115c:a1e0::5": false, "100.110.34.9": true, "203.0.113.9": true, "2001:db8::1": true,
	} {
		if got := externalPeer(netip.MustParseAddr(addr)); got != external {
			t.Errorf("externalPeer(%s)=%v", addr, got)
		}
	}
}

// The netlink request and the tcp_info offset, against this kernel: a
// connection the test opens on loopback is found in the dump with an RTT.
// Nothing leaves the host and nothing else is read but this namespace's own
// established sockets.
func TestSockDiagFindsAnEstablishedConnectionsRTT(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			accepted <- c
		}
	}()
	client, err := net.Dial("tcp4", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server := <-accepted
	defer server.Close()
	if _, err := client.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := server.Read(buf); err != nil {
		t.Fatal(err)
	}
	port := uint16(client.LocalAddr().(*net.TCPAddr).Port)
	rtts, ok := tcpInfoRTTs(unix.AF_INET, maxLatencySockets, func(a netip.Addr) bool { return a.IsLoopback() })
	if !ok {
		t.Skipf("sock_diag is not available here (port %d)", port)
	}
	if len(rtts) == 0 {
		t.Fatal("the dump did not include the connection this test holds")
	}
	for _, rtt := range rtts {
		if rtt < 0 || rtt > 1000 {
			t.Fatalf("an implausible loopback RTT %v ms: the tcp_info offset is wrong", rtt)
		}
	}
}
