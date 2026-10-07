package netx

import (
	"context"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
)

// OperatorAddress is the address the guards protect for a request: the
// request's own, or — when it arrived on loopback through an SSH tunnel —
// the address that SSH session came from.
//
// A browser that reaches the dashboard through `ssh -L` arrives as
// 127.0.0.1, a path no network change can take away, so every guard would
// pass anything. But the tunnel rides on an SSH connection from somewhere,
// and a route, a blocklist or a forward that cuts that connection cuts the
// operator off as surely as one that cut a browser on the internet. The
// session is found by what a local forward looks like from the outside: an
// sshd process holding a connection from loopback to loopback (the forwarded
// one) and a connection on a listening port from somewhere else (the
// session). Where none is found the request stays local.
func (s *Service) OperatorAddress(ctx context.Context, client string) string {
	addr, err := ParseAddr(client)
	if err != nil || !addr.IsLoopback() {
		return client
	}
	if peers := tunnelPeers(ctx); len(peers) > 0 {
		return peers[0]
	}
	return client
}

// tunnelPeers is a variable so tests stand a transcript behind it.
var tunnelPeers = readTunnelPeers

func readTunnelPeers(ctx context.Context) []string {
	established, err := run(ctx, "ss", "-Htnp", "state", "established")
	if err != nil {
		return nil
	}
	listening, err := run(ctx, "ss", "-Htln")
	if err != nil {
		return nil
	}
	return sshTunnelPeers(established, listening)
}

var ssUser = regexp.MustCompile(`\("([^"]+)",pid=(\d+),fd=\d+\)`)

// sshTunnelPeers reads the two ss listings: which sshd processes hold a
// loopback-to-loopback connection, and the remote end of each one's
// connection on a listening port.
func sshTunnelPeers(established, listening string) []string {
	ports := map[uint16]bool{}
	for _, line := range strings.Split(listening, "\n") {
		f := strings.Fields(line)
		// LISTEN Recv-Q Send-Q Local Peer
		if len(f) < 4 {
			continue
		}
		if ap, ok := parseSSEndpoint(f[3]); ok {
			ports[ap.Port()] = true
		}
	}
	forwards := map[string]bool{}
	sessions := map[string][]string{}
	var order []string
	for _, line := range strings.Split(established, "\n") {
		f := strings.Fields(line)
		// Recv-Q Send-Q Local Peer users:(...)
		if len(f) < 5 {
			continue
		}
		local, ok1 := parseSSEndpoint(f[2])
		peer, ok2 := parseSSEndpoint(f[3])
		if !ok1 || !ok2 {
			continue
		}
		for _, m := range ssUser.FindAllStringSubmatch(strings.Join(f[4:], " "), -1) {
			if !strings.HasPrefix(m[1], "sshd") {
				continue
			}
			pid := m[2]
			switch {
			case local.Addr().IsLoopback() && peer.Addr().IsLoopback():
				forwards[pid] = true
			case !peer.Addr().IsLoopback() && ports[local.Port()]:
				if _, seen := sessions[pid]; !seen {
					order = append(order, pid)
				}
				sessions[pid] = append(sessions[pid], peer.Addr().String())
			}
		}
	}
	var out []string
	for _, pid := range order {
		if forwards[pid] {
			out = append(out, sessions[pid]...)
		}
	}
	return out
}

// parseSSEndpoint reads ss's address:port, IPv6 bracketed, a zone dropped.
func parseSSEndpoint(s string) (netip.AddrPort, bool) {
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return netip.AddrPort{}, false
	}
	host, port := strings.Trim(s[:i], "[]"), s[i+1:]
	if z := strings.Index(host, "%"); z >= 0 {
		host = host[:z]
	}
	if host == "*" {
		host = "0.0.0.0"
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return netip.AddrPort{}, false
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return netip.AddrPort{}, false
	}
	return netip.AddrPortFrom(a.Unmap(), uint16(n)), true
}
