package netx

import (
	"context"
	"fmt"
	"io"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// udpSample is `ss -uanpH` as a host running a resolver, an HTTP/3 server and
// a QUIC client prints it: unconnected listeners and one connected socket.
const udpSample = `UNCONN 0      0          127.0.0.53%lo:53         0.0.0.0:*    users:(("systemd-resolve",pid=612,fd=13))
UNCONN 0      0                0.0.0.0:443        0.0.0.0:*    users:(("caddy",pid=4410,fd=9))
UNCONN 0      0                   [::]:443           [::]:*    users:(("caddy",pid=4410,fd=10))
ESTAB  0      0           203.0.113.10:51544 198.51.100.20:443 users:(("agent-cli",pid=50969,fd=31))
ESTAB  0      0           203.0.113.10:40012  192.0.2.53:53
`

// snmpSample is /proc/net/snmp's Tcp lines with the given open counts.
func snmpSample(active, passive uint64) string {
	return "Ip: Forwarding DefaultTTL\nIp: 1 64\n" +
		"Tcp: RtoAlgorithm RtoMin RtoMax MaxConn ActiveOpens PassiveOpens AttemptFails EstabResets CurrEstab InSegs OutSegs RetransSegs InErrs OutRsts InCsumErrors\n" +
		fmt.Sprintf("Tcp: 1 200 120000 -1 %d %d 9 4 82 1000 2000 30 0 7 0\n", active, passive)
}

func withSNMP(t *testing.T, content string) *string {
	t.Helper()
	current := content
	prev := procNetSNMP
	procNetSNMP = func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(current)), nil }
	t.Cleanup(func() { procNetSNMP = prev })
	return &current
}

func TestParseTCPCountersReadsTheHeaderedLine(t *testing.T) {
	c, err := parseTCPCounters(strings.NewReader(snmpSample(23930214, 4414215)))
	if err != nil {
		t.Fatal(err)
	}
	if c.activeOpens != 23930214 || c.passiveOpens != 4414215 || c.attemptFails != 9 || c.estabResets != 4 ||
		c.currEstab != 82 || c.outSegs != 2000 || c.retransSegs != 30 || c.outRsts != 7 {
		t.Fatalf("counters = %+v", c)
	}
	if _, err := parseTCPCounters(strings.NewReader("Ip: a b\nIp: 1 2\n")); err == nil {
		t.Fatal("a file without Tcp lines parsed")
	}
	if _, err := parseTCPCounters(strings.NewReader("Tcp: A B C\nTcp: 1 2\n")); err == nil {
		t.Fatal("a short value line parsed")
	}
}

// Every step carries packets a second and the faults it added, and the TCP
// ring beside it says how much of what left had to be sent again.
func TestLiveStepsCarryPacketsFaultsAndTCPContext(t *testing.T) {
	s := newSampler(nil, nil, 0, 0)
	t0 := time.Unix(1_000_000, 0)
	s.observe(t0, map[string]devCounters{"eth0": {rxBytes: 1000, rxPackets: 10, rxErrs: 1, txDrop: 2}})
	s.observeTCP(t0, &tcpCounters{activeOpens: 10, passiveOpens: 5, outSegs: 1000, retransSegs: 10, currEstab: 40}, nil)
	t1 := t0.Add(2 * time.Second)
	s.observe(t1, map[string]devCounters{"eth0": {rxBytes: 5000, rxPackets: 410, txPackets: 200, rxErrs: 3, txDrop: 5}})
	s.observeTCP(t1, &tcpCounters{activeOpens: 14, passiveOpens: 7, outSegs: 3000, retransSegs: 110, estabResets: 2, attemptFails: 4, currEstab: 44}, nil)

	view := s.LiveView(0, t1.Add(500*time.Millisecond))
	p := view.Series["eth0"]
	if len(p) != 1 || p[0].RxPackets != 200 || p[0].TxPackets != 100 || p[0].Errors != 2 || p[0].Drops != 3 {
		t.Fatalf("live point = %+v", p)
	}
	if len(view.TCP) != 1 {
		t.Fatalf("tcp ring = %+v", view.TCP)
	}
	tcp := view.TCP[0]
	if tcp.OutSegs != 1000 || tcp.Retrans != 50 || tcp.Opens != 3 || tcp.Resets != 1 || tcp.Failed != 2 || tcp.Established != 44 {
		t.Fatalf("tcp step = %+v", tcp)
	}
	if view.SampledAt != t1.Unix() || view.StepSeconds != 2 || view.Now != t1.Unix() || view.TCPError != "" {
		t.Fatalf("view = %+v", view)
	}
	// since filters the TCP ring as it does the devices'.
	if v := s.LiveView(t1.Unix(), t1); len(v.TCP) != 0 || len(v.Series["eth0"]) != 0 {
		t.Fatalf("since left %+v", v)
	}

	// A failed read is reported and does not become a quiet step.
	s.observeTCP(t1.Add(2*time.Second), nil, fmt.Errorf("open /proc/net/snmp: permission denied"))
	if v := s.LiveView(0, t1); v.TCPError == "" || len(v.TCP) != 1 {
		t.Fatalf("failed read = %+v", v)
	}
	// The ring keeps fifteen minutes.
	for i := 0; i < liveKeep+20; i++ {
		at := t1.Add(time.Duration(i+2) * liveStep)
		s.observeTCP(at, &tcpCounters{outSegs: uint64(i * 10)}, nil)
	}
	if v := s.LiveView(0, t1); len(v.TCP) != liveKeep {
		t.Fatalf("ring holds %d", len(v.TCP))
	}
}

func TestParseSSReadsLatencyRetransmissionsAndCongestion(t *testing.T) {
	out := "0 0 203.0.113.10:44134 198.51.100.20:443\n" +
		"\t ts sack bbr wscale:7,7 rto:204 rtt:12.5/3.2 ato:40 mss:1448 cwnd:10 bytes_sent:2000 bytes_retrans:144 bytes_acked:1990 bytes_received:900 segs_out:40 segs_in:30 send 844112150bps pacing_rate 1688020296bps delivery_rate 14551555bps retrans:1/4 minrtt:9.8\n" +
		"0 0 127.0.0.1:5432 127.0.0.1:41000\n" +
		"\t cubic wscale:7,7 rtt:0.04/0.01 bytes_sent:10 bytes_received:20 segs_out:3\n" +
		"0 0 [2001:db8::1]:22 [2001:db8::9]:51000\n" +
		"\t bytes_sent:10 bytes_received:20\n"
	socks, _ := parseSS(out)
	if len(socks) != 3 {
		t.Fatalf("%d sockets", len(socks))
	}
	a := socks[0]
	if a.congestion != "bbr" || !a.hasRTT || a.rttMs != 12.5 || a.minRTTMs != 9.8 || a.retransOut != 1 || a.retransTotal != 4 ||
		a.segsOut != 40 || a.bytesRetrans != 144 || !a.hasDelivery || a.deliveryBps != 14551555 || a.tx != 2000 {
		t.Fatalf("first = %+v", a)
	}
	if socks[1].congestion != "cubic" || socks[1].rttMs != 0.04 {
		t.Fatalf("second = %+v", socks[1])
	}
	// Fields the kernel did not print stay absent, not zero measurements.
	if c := socks[2]; c.hasRTT || c.hasDelivery || c.congestion != "" {
		t.Fatalf("third = %+v", c)
	}
	if v, ok := parseBps("12.5Mbps"); !ok || v != 12.5e6 {
		t.Fatalf("parseBps = %v %v", v, ok)
	}
}

// Latency is TCP's estimate over the sockets that left the machine.
func TestLatencySummaryLeavesLoopbackOut(t *testing.T) {
	socks := []flowSocket{
		{peerAddr: "127.0.0.1", rttMs: 0.02, hasRTT: true},
		{peerAddr: "::1", rttMs: 0.01, hasRTT: true},
		{peerAddr: "::ffff:127.0.0.1", rttMs: 0.01, hasRTT: true},
		{peerAddr: "198.51.100.1", rttMs: 10, hasRTT: true},
		{peerAddr: "198.51.100.2", rttMs: 20, hasRTT: true, retransOut: 2},
		{peerAddr: "198.51.100.3", rttMs: 30, hasRTT: true},
		{peerAddr: "2001:db8::4", rttMs: 200, hasRTT: true},
		{peerAddr: "198.51.100.5"},
	}
	l := summariseLatency(socks, true, time.Unix(100, 0))
	if l.Loopback != 3 || l.Sockets != 4 || l.MedianMs != 20 || l.P90Ms != 200 || l.MaxMs != 200 || l.Retransmitting != 1 || !l.Truncated {
		t.Fatalf("latency = %+v", l)
	}
	if empty := summariseLatency(nil, false, time.Unix(100, 0)); empty.Sockets != 0 || empty.MedianMs != 0 {
		t.Fatalf("no sockets = %+v", empty)
	}
}

// The latency read is shared: a page polling every two seconds runs ss at
// most once in ten.
func TestTCPLatencyIsReadAtMostEveryTenSeconds(t *testing.T) {
	rec := record(t)
	rec.on("ss -tinH state established", fixture(t, "traffic-ss.txt"))
	s := testService(t)
	a := s.TCPLatency(context.Background())
	b := s.TCPLatency(context.Background())
	if a.Error != "" || a.Sockets == 0 || !a.At.Equal(b.At) || len(rec.commands()) != 1 {
		t.Fatalf("a = %+v b = %+v ran %v", a, b, rec.commands())
	}
	record(t, "ss")
	s = testService(t)
	if l := s.TCPLatency(context.Background()); !strings.Contains(l.Error, "not installed") {
		t.Fatalf("without ss = %+v", l)
	}
}

func TestParseUDPReadsStatesOwnersAndPeers(t *testing.T) {
	socks, truncated := parseUDP(udpSample)
	if truncated || len(socks) != 5 {
		t.Fatalf("%d sockets, truncated %v", len(socks), truncated)
	}
	if s := socks[1]; s.state != "UNCONN" || s.name != "caddy" || s.pids[0] != 4410 || s.peerAddr != "" {
		t.Fatalf("listener = %+v", s)
	}
	if s := socks[3]; s.state != "ESTAB" || s.peerAddr != "198.51.100.20" || s.peerPort != 443 || s.name != "agent-cli" {
		t.Fatalf("connected = %+v", s)
	}
	if s := socks[4]; s.name != "" || s.peerAddr != "192.0.2.53" {
		t.Fatalf("ownerless = %+v", s)
	}
}

// UDP sockets are counted per program and their peers listed without bytes;
// connections the kernel opened that no read saw are counted, not guessed.
func TestProgramsCountUDPAndConnectionsNoReadSaw(t *testing.T) {
	f := newFlowSampler()
	t0 := time.Unix(1_000_000, 0)
	udp, _ := parseUDP(udpSample)
	opens := func(n uint64) *uint64 { return &n }
	first := f.observeAll(t0, []flowSocket{
		sock("10.0.0.1:1", "198.51.100.20:443", "agent-cli", 50969, 100, 100),
		sock("10.0.0.1:2", "198.51.100.21:443", "agent-cli", 50969, 100, 100),
	}, udp, opens(100), false)
	if !first.Warming || first.MissedOpens != nil {
		t.Fatalf("first read = %+v", first)
	}
	agent := program(t, first, "agent-cli")
	if agent.UDPConnected != 1 || agent.UDPUnconnected != 0 {
		t.Fatalf("agent udp = %+v", agent)
	}
	caddy := program(t, first, "caddy")
	if caddy.UDPUnconnected != 2 || caddy.Connections != 0 || len(caddy.Peers) != 0 {
		t.Fatalf("caddy = %+v", caddy)
	}
	var udpPeer *PeerTraffic
	for i, p := range agent.Peers {
		if p.Protocol == "udp" {
			udpPeer = &agent.Peers[i]
		}
		if p.Protocol == "tcp" && !p.BytesKnown {
			t.Fatalf("a TCP peer without counters: %+v", p)
		}
	}
	if udpPeer == nil || udpPeer.BytesKnown || udpPeer.Address != "198.51.100.20" || udpPeer.Connections != 1 {
		t.Fatalf("udp peer = %+v in %+v", udpPeer, agent.Peers)
	}

	// Seven opens since; one new socket seen, one of the old gone: six
	// connections were born and gone between the two reads.
	second := f.observeAll(t0.Add(3*time.Second), []flowSocket{
		sock("10.0.0.1:1", "198.51.100.20:443", "agent-cli", 50969, 200, 150),
		sock("10.0.0.1:3", "198.51.100.22:443", "agent-cli", 50969, 50, 50),
	}, udp, opens(107), false)
	if second.MissedOpens == nil || *second.MissedOpens != 6 || second.Closed != 1 {
		t.Fatalf("second read missed %v, closed %d", second.MissedOpens, second.Closed)
	}
	// A truncated read cannot say how many it missed.
	third := f.observeAll(t0.Add(6*time.Second), nil, nil, opens(110), true)
	if third.MissedOpens != nil || !third.Truncated {
		t.Fatalf("truncated read = %+v", third)
	}
	// Nor can one after it, whose baseline was truncated, nor one whose
	// kernel counters were unreadable.
	if fourth := f.observeAll(t0.Add(9*time.Second), nil, nil, opens(112), false); fourth.MissedOpens != nil {
		t.Fatalf("read after a truncated one = %+v", fourth.MissedOpens)
	}
	if fifth := f.observeAll(t0.Add(12*time.Second), nil, nil, nil, false); fifth.MissedOpens != nil {
		t.Fatalf("read without counters = %+v", fifth.MissedOpens)
	}
}

func TestProgramsCarryTheirMedianRTTAndRetransmissions(t *testing.T) {
	f := newFlowSampler()
	a := sock("10.0.0.1:1", "198.51.100.20:443", "curl", 1, 0, 0)
	a.rttMs, a.hasRTT, a.retransTotal, a.segsOut = 10, true, 2, 100
	b := sock("10.0.0.1:2", "198.51.100.21:443", "curl", 1, 0, 0)
	b.rttMs, b.hasRTT, b.retransTotal, b.segsOut = 30, true, 1, 50
	c := sock("10.0.0.1:3", "198.51.100.22:443", "curl", 1, 0, 0)
	res := f.observe(time.Unix(1, 0), []flowSocket{a, b, c})
	p := program(t, res, "curl")
	if p.MedianRTTMs == nil || *p.MedianRTTMs != 10 || p.Retransmitted != 3 || p.SegmentsOut != 150 {
		t.Fatalf("program = %+v", p)
	}
}

// The per-address read filters in ss and returns the counters by tuple,
// IPv4-mapped addresses unmapped so they join the connection table's.
func TestTCPSocketsToFiltersByAddress(t *testing.T) {
	rec := record(t)
	rec.on("ss -tinH state established dst 198.51.100.20", "0 0 [::ffff:203.0.113.10]:44134 [::ffff:198.51.100.20]:443\n\t cubic rtt:12.5/3 bytes_sent:2000 bytes_received:900 retrans:0/2\n")
	rec.on("ss -tinH state established dst [2001:db8::9]", "")
	s := testService(t)
	list, truncated, err := s.TCPSocketsTo(context.Background(), netip.MustParseAddr("198.51.100.20"))
	if err != nil || truncated || len(list) != 1 {
		t.Fatalf("list = %+v %v %v", list, truncated, err)
	}
	c := list[0]
	if c.LocalAddr != "203.0.113.10" || c.LocalPort != 44134 || c.PeerAddr != "198.51.100.20" || c.PeerPort != 443 ||
		c.TxBytes != 2000 || c.RxBytes != 900 || c.RTTMs == nil || *c.RTTMs != 12.5 || c.Retransmitted != 2 || c.Congestion != "cubic" {
		t.Fatalf("counters = %+v", c)
	}
	if list, _, err := s.TCPSocketsTo(context.Background(), netip.MustParseAddr("2001:db8::9")); err != nil || len(list) != 0 {
		t.Fatalf("v6 = %+v %v", list, err)
	}
}
