package proxysvc

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"
)

type memoryWatches struct {
	endpoints []WatchedEndpoint
	saved     []WatchCheck
}

func (m *memoryWatches) WatchInterval(context.Context) (time.Duration, error) {
	return DefaultWatchInterval, nil
}
func (m *memoryWatches) WatchedEndpoints(context.Context) ([]WatchedEndpoint, error) {
	return m.endpoints, nil
}
func (m *memoryWatches) SaveWatchChecks(_ context.Context, checks []WatchCheck) error {
	m.saved = append(m.saved, checks...)
	return nil
}

// A network probe is checked on the TLS watch's schedule by connecting, and
// a TLS watch by its handshake, each kept as its own kind of check.
func TestTheWatchMonitorProbesNetworkEndpoints(t *testing.T) {
	store := &memoryWatches{endpoints: []WatchedEndpoint{
		{ID: 1, Domain: "app.example.test", Port: 443, Kind: WatchTLS},
		{ID: 2, Domain: "db.example.test", Port: 5432, Kind: WatchTCP},
	}}
	m := NewTLSMonitor(store)
	var handshakes, probes []string
	m.Check = func(_ context.Context, domain, ip string, port int) (*Certificate, error) {
		handshakes = append(handshakes, domain)
		return &Certificate{Name: domain}, nil
	}
	m.Probe = func(_ context.Context, domain, ip string, port int) (*ProbeCheck, error) {
		probes = append(probes, domain)
		return &ProbeCheck{OK: true, State: "connected", Ms: 3}, nil
	}
	if _, err := m.CheckDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(handshakes) != 1 || handshakes[0] != "app.example.test" || len(probes) != 1 || probes[0] != "db.example.test" {
		t.Fatalf("handshakes %v probes %v", handshakes, probes)
	}
	for _, c := range store.saved {
		if c.EndpointID == 2 && (c.Probe == nil || c.Cert != nil) || c.EndpointID == 1 && (c.Cert == nil || c.Probe != nil) {
			t.Fatalf("check = %+v", c)
		}
	}
}

// A probe connects and says how fast, or says it was refused, and never
// sends anything.
func TestCheckTCPConnectsOrSaysWhyNot(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	received := make(chan int, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		buf := make([]byte, 16)
		conn.SetReadDeadline(time.Now().Add(time.Second))
		n, _ := conn.Read(buf)
		received <- n
		conn.Close()
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	check, err := CheckTCP(context.Background(), "localhost", "127.0.0.1", port)
	if err != nil || !check.OK || check.State != "connected" || check.Address != "127.0.0.1:"+strconv.Itoa(port) {
		t.Fatalf("check = %+v %v", check, err)
	}
	if n := <-received; n != 0 {
		t.Fatalf("the probe sent %d bytes", n)
	}
	ln.Close()
	check, err = CheckTCP(context.Background(), "127.0.0.1", "", port)
	if err != nil || check.OK || check.State != "refused" {
		t.Fatalf("closed port = %+v %v", check, err)
	}
	if targets := FleetTargets(nil, []WatchedEndpoint{{Domain: "db.example.test", Port: 5432, Kind: WatchTCP}}); len(targets) != 0 {
		t.Fatalf("a probe was scanned for TLS: %+v", targets)
	}
}
