package netsec

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSummarisePeersFoldsByAddress(t *testing.T) {
	out := SummarisePeers([]Connection{
		{Protocol: "tcp", LocalPort: 443, RemoteAddr: "203.0.113.9", Status: "ESTABLISHED", Process: "nginx"},
		{Protocol: "tcp", LocalPort: 443, RemoteAddr: "203.0.113.9", Status: "TIME_WAIT", Process: "nginx"},
		{Protocol: "tcp", LocalPort: 22, RemoteAddr: "203.0.113.9", Status: "ESTABLISHED", Process: "sshd"},
		{Protocol: "tcp", LocalPort: 443, RemoteAddr: "198.51.100.7", Status: "ESTABLISHED", Process: "nginx"},
	})
	if len(out.Peers) != 2 {
		t.Fatalf("got %d peers, want 2", len(out.Peers))
	}
	// Busiest first: the address holding three sockets is the one worth
	// looking at, and sorting by address would bury it.
	top := out.Peers[0]
	if top.Address != "203.0.113.9" || top.Count != 3 || top.Established != 2 {
		t.Fatalf("top peer = %+v", top)
	}
	if len(top.Ports) != 2 || top.Ports[0] != 22 {
		t.Errorf("ports = %v, want them sorted", top.Ports)
	}
	if len(top.Processes) != 2 {
		t.Errorf("processes = %v", top.Processes)
	}
}

func TestSummarisePeersSeparatesLoopback(t *testing.T) {
	out := SummarisePeers([]Connection{
		{Protocol: "tcp", LocalPort: 5432, RemoteAddr: "127.0.0.1", Status: "ESTABLISHED"},
		{Protocol: "tcp", LocalPort: 443, RemoteAddr: "203.0.113.9", Status: "ESTABLISHED"},
	})
	if out.Loopback != 1 {
		t.Errorf("loopback = %d", out.Loopback)
	}
	if len(out.Peers) != 1 {
		t.Fatalf("loopback should not appear as a peer: %+v", out.Peers)
	}
	if out.Total != 2 {
		t.Errorf("total = %d; the folded ones still count", out.Total)
	}
}

// Most of a healthy host's connections are private, and being able to say so
// is what makes the public ones worth looking at.
func TestSummarisePeersMarksPrivateAddresses(t *testing.T) {
	out := SummarisePeers([]Connection{
		{Protocol: "tcp", LocalPort: 443, RemoteAddr: "10.1.2.3"},
		{Protocol: "tcp", LocalPort: 443, RemoteAddr: "100.101.102.103"},
		{Protocol: "tcp", LocalPort: 443, RemoteAddr: "203.0.113.9"},
	})
	byAddr := map[string]Peer{}
	for _, p := range out.Peers {
		byAddr[p.Address] = p
	}
	if !byAddr["10.1.2.3"].Private {
		t.Error("RFC1918 not marked private")
	}
	if !byAddr["100.101.102.103"].Private {
		t.Error("a tailnet address is not the internet")
	}
	if byAddr["203.0.113.9"].Private {
		t.Error("a public address marked private")
	}
}

func TestSummarisePeersNamesTheService(t *testing.T) {
	out := SummarisePeers([]Connection{{Protocol: "tcp", LocalPort: 5432, RemoteAddr: "203.0.113.9"}})
	if out.Peers[0].Service != "PostgreSQL" {
		t.Errorf("service = %q", out.Peers[0].Service)
	}
}

// The two figures under the table have to add up, and they did not while
// "listening" was computed by subtraction: an unconnected datagram socket has
// no peer and is not a listener, so it was counted as one.
func TestSummarisePeersTotalsAddUp(t *testing.T) {
	conns := []Connection{
		{Protocol: "tcp", LocalPort: 443, RemoteAddr: "203.0.113.9"},
		{Protocol: "tcp", LocalPort: 443, RemoteAddr: "127.0.0.1"},
	}
	out := SummarisePeers(conns)
	peered := 0
	for _, p := range out.Peers {
		peered += p.Count
	}
	if peered+out.Loopback != out.Total {
		t.Fatalf("%d in peers + %d loopback != %d total", peered, out.Loopback, out.Total)
	}
}

// Folding by address keeps the transports, the far-end ports (bounded) and
// how many sockets are in each state.
func TestSummarisePeersKeepsProtocolsRemotePortsAndStates(t *testing.T) {
	conns := []Connection{
		{Protocol: "tcp", LocalPort: 443, RemoteAddr: "203.0.113.9", RemotePort: 51000, Status: "ESTABLISHED"},
		{Protocol: "tcp", LocalPort: 443, RemoteAddr: "203.0.113.9", RemotePort: 51001, Status: "TIME_WAIT"},
		{Protocol: "udp", LocalPort: 443, RemoteAddr: "203.0.113.9", RemotePort: 51002, Status: "NONE"},
	}
	for i := 0; i < 10; i++ {
		conns = append(conns, Connection{Protocol: "tcp", LocalPort: 443, RemoteAddr: "203.0.113.9", RemotePort: uint32(52000 + i), Status: "ESTABLISHED"})
	}
	p := SummarisePeers(conns).Peers[0]
	if strings.Join(p.Protocols, ",") != "tcp,udp" || len(p.RemotePorts) != maxRemotePorts || p.MorePorts != 13-maxRemotePorts || p.RemotePorts[0] != 51000 {
		t.Fatalf("peer = %+v", p)
	}
	if p.States["ESTABLISHED"] != 11 || p.States["TIME_WAIT"] != 1 || p.States["CONNECTED"] != 1 {
		t.Fatalf("states = %v", p.States)
	}
}

// A tuple keeps the first read that held it, and one missing from the next
// read is a close between the two, remembered for fifteen minutes.
func TestConnectionTrackerAgesTuplesAndNoticesCloses(t *testing.T) {
	var tr connTracker
	t0 := time.Unix(1_000_000, 0)
	a := Connection{Protocol: "tcp", LocalAddr: "203.0.113.10", LocalPort: 443, RemoteAddr: "198.51.100.7", RemotePort: 51000, Process: "caddy"}
	b := Connection{Protocol: "tcp", LocalAddr: "203.0.113.10", LocalPort: 443, RemoteAddr: "198.51.100.7", RemotePort: 51001, Process: "caddy"}
	// The same socket read as IPv4-mapped is the same tuple.
	mapped := a
	mapped.LocalAddr, mapped.RemoteAddr = "::ffff:203.0.113.10", "::ffff:198.51.100.7"

	list := []Connection{a, b}
	q := tr.observe(t0, list)
	if q.IntervalSeconds != 0 || q.ClosedSinceLast != 0 || !list[0].FirstSeen.Equal(t0) || len(q.Limits) == 0 {
		t.Fatalf("first read = %+v %+v", q, list)
	}
	list = []Connection{mapped}
	q = tr.observe(t0.Add(10*time.Second), list)
	if q.IntervalSeconds != 10 || q.ClosedSinceLast != 1 || !list[0].FirstSeen.Equal(t0) {
		t.Fatalf("second read = %+v first seen %v", q, list[0].FirstSeen)
	}
	closed := tr.closedTo("198.51.100.7")
	if len(closed) != 1 || closed[0].RemotePort != 51001 || closed[0].ObservedSeconds != 0 || !closed[0].GoneBy.Equal(t0.Add(10*time.Second)) {
		t.Fatalf("closed = %+v", closed)
	}
	q = tr.observe(t0.Add(25*time.Second), nil)
	closed = tr.closedTo("198.51.100.7")
	if q.ClosedSinceLast != 1 || len(closed) != 2 || closed[0].RemotePort != 51000 || closed[0].ObservedSeconds != 10 {
		t.Fatalf("third read = %+v closed %+v", q, closed)
	}
	if other := tr.closedTo("192.0.2.1"); len(other) != 0 {
		t.Fatalf("another address's closes = %+v", other)
	}
	// Closes older than fifteen minutes are forgotten at the next read.
	tr.observe(t0.Add(25*time.Second+closedFor+time.Second), nil)
	if left := tr.closedTo("198.51.100.7"); len(left) != 0 {
		t.Fatalf("old closes kept: %+v", left)
	}
	// The ring is bounded however many close at once.
	many := make([]Connection, closedKept+50)
	for i := range many {
		many[i] = Connection{Protocol: "udp", LocalAddr: "203.0.113.10", LocalPort: uint32(1000 + i), RemoteAddr: "198.51.100.9", RemotePort: 53}
	}
	at := t0.Add(time.Hour)
	tr.observe(at, many)
	tr.observe(at.Add(time.Second), nil)
	if n := len(tr.closedTo("198.51.100.9")); n != closedKept {
		t.Fatalf("kept %d closes", n)
	}
}

func TestPeerDetailRefusesWhatIsNotAnAddress(t *testing.T) {
	if _, err := New().PeerDetail(context.Background(), "example.com"); !errors.Is(err, ErrInvalidAddress) {
		t.Fatalf("err = %v", err)
	}
}
