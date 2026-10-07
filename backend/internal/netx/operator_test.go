package netx

import (
	"context"
	"testing"
)

const ssListening = `LISTEN 0      4096         0.0.0.0:22         0.0.0.0:*
LISTEN 0      4096            [::]:22            [::]:*
LISTEN 0      4096       127.0.0.1:443        0.0.0.0:*
LISTEN 0      4096               *:8080             *:*
`

const ssEstablished = `0      0      203.0.113.20:22    198.51.100.23:51122 users:(("sshd-session",pid=4120,fd=4),("sshd-session",pid=4098,fd=4))
0      0         127.0.0.1:44312     127.0.0.1:443   users:(("sshd-session",pid=4120,fd=9))
0      0      203.0.113.20:22     192.0.2.77:40000 users:(("sshd-session",pid=5001,fd=4))
0      0      203.0.113.20:51000   10.0.4.5:80     users:(("sshd-session",pid=5001,fd=9))
0      0         127.0.0.1:55555     127.0.0.1:443   users:(("caddy",pid=900,fd=12))
`

func TestSSHTunnelPeersFindTheSessionBehindALocalForward(t *testing.T) {
	got := sshTunnelPeers(ssEstablished, ssListening)
	// 4120 forwards to loopback and came from 198.51.100.23; 5001 forwards
	// to another host, which is no tunnel to the dashboard.
	if len(got) != 1 || got[0] != "198.51.100.23" {
		t.Fatalf("peers = %v", got)
	}
}

func TestOperatorAddressResolvesOnlyLoopback(t *testing.T) {
	s := testService(t)
	prev := tunnelPeers
	t.Cleanup(func() { tunnelPeers = prev })
	tunnelPeers = func(context.Context) []string { return []string{"198.51.100.23"} }
	if got := s.OperatorAddress(context.Background(), "127.0.0.1"); got != "198.51.100.23" {
		t.Fatalf("tunnel client = %s", got)
	}
	if got := s.OperatorAddress(context.Background(), "100.110.34.9"); got != "100.110.34.9" {
		t.Fatalf("a tailnet client is its own address, got %s", got)
	}
	tunnelPeers = func(context.Context) []string { return nil }
	if got := s.OperatorAddress(context.Background(), "::1"); got != "::1" {
		t.Fatalf("no session found stays local, got %s", got)
	}
}
