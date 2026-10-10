package netx

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Every link, route, sysctl, firewall and WireGuard command in this fixture is
// confined to one of its three disposable namespaces. systemd is represented
// only by wg-quick invocations there; no real host unit is enabled or reloaded.
func wgDualLiveRun(ns string, s *Service) func(context.Context, string, ...string) (string, error) {
	return func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "systemctl" {
			joined := strings.Join(args, " ")
			if joined == "daemon-reload" || joined == "is-enabled "+UnitName {
				return "enabled\n", nil
			}
			if len(args) < 2 || !strings.HasPrefix(args[len(args)-1], "wg-quick@") {
				return "", fmt.Errorf("fixture refuses a host service")
			}
			iface := strings.TrimPrefix(args[len(args)-1], "wg-quick@")
			path, err := s.wgConfPath(iface)
			if err != nil {
				return "", err
			}
			switch args[0] {
			case "is-enabled", "is-active":
				return "active\n", nil
			case "enable", "start":
				name, args = "wg-quick", []string{"up", path}
			case "disable", "stop":
				name, args = "wg-quick", []string{"down", path}
			case "reload":
				strip := gwLiveCmd(ctx, "ip", "netns", "exec", ns, "env", "PATH="+os.Getenv("PATH"), "wg-quick", "strip", path)
				input, err := strip.Output()
				if err != nil {
					return "", err
				}
				cmd := gwLiveCmd(ctx, "ip", "netns", "exec", ns, "env", "PATH="+os.Getenv("PATH"), "wg", "syncconf", iface, "/dev/stdin")
				cmd.Stdin = bytes.NewReader(input)
				out, err := cmd.CombinedOutput()
				return string(out), err
			default:
				return "", fmt.Errorf("fixture refuses unknown systemd operation")
			}
		}
		switch name {
		case "ip", "nft", "iptables", "ip6tables", "wg", "wg-quick":
		default:
			return "", &UnavailableError{Tool: name}
		}
		cmd := gwLiveCmd(ctx, append([]string{"ip", "netns", "exec", ns, "env", "PATH=" + os.Getenv("PATH"), name}, args...)...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
}

func wgDualLiveLink(t *testing.T, a, aname, b, bname, v4a, v4b, v6a, v6b string) {
	t.Helper()
	gwMustInNS(t, a, "ip", "link", "add", aname, "type", "veth", "peer", "name", bname, "netns", b)
	for _, end := range []struct{ ns, dev, v4, v6 string }{{a, aname, v4a, v6a}, {b, bname, v4b, v6b}} {
		gwMustInNS(t, end.ns, "ip", "link", "set", end.dev, "up")
		if end.v4 != "" {
			gwMustInNS(t, end.ns, "ip", "addr", "add", end.v4, "dev", end.dev)
		}
		if end.v6 != "" {
			gwMustInNS(t, end.ns, "ip", "-6", "addr", "add", end.v6, "dev", end.dev, "nodad")
		}
	}
}

const wgDualEcho = `
import http.server, socket, socketserver, sys
class S(http.server.ThreadingHTTPServer):
    address_family = socket.AF_INET6 if ":" in sys.argv[1] else socket.AF_INET
    def server_bind(self):
        socketserver.TCPServer.server_bind(self)
        self.server_name, self.server_port = sys.argv[1], self.server_address[1]
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        body = self.client_address[0].encode()
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def log_message(self, *args): pass
S((sys.argv[1], 8080), H).serve_forever()
`

const wgDualDNSEcho = `
import socket, sys
s = socket.socket(socket.AF_INET6 if ":" in sys.argv[1] else socket.AF_INET, socket.SOCK_DGRAM)
s.bind((sys.argv[1], 53))
while True:
    data, peer = s.recvfrom(512)
    s.sendto(peer[0].encode(), peer)
`

const wgDualDNSQuery = `
import socket, sys
s = socket.socket(socket.AF_INET6 if ":" in sys.argv[1] else socket.AF_INET, socket.SOCK_DGRAM)
s.settimeout(2)
s.sendto(b"fixture DNS-shaped UDP flow", (sys.argv[1], 53))
print(s.recvfrom(512)[0].decode())
`

func wgDualLiveServe(t *testing.T, ns, addr string) {
	t.Helper()
	cmd := gwLiveCmd(context.Background(), "ip", "netns", "exec", ns, "timeout", "300", "python3", "-u", "-c", wgDualEcho, addr)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	if addr == "1.1.1.1" || addr == "2606:4700:4700::1111" {
		udp := gwLiveCmd(context.Background(), "ip", "netns", "exec", ns, "timeout", "300", "python3", "-u", "-c", wgDualDNSEcho, addr)
		if err := udp.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = udp.Process.Kill(); _ = udp.Wait() })
	}
}

func wgDualLiveFetch(t *testing.T, ns, addr string, want string) {
	t.Helper()
	host := addr
	if strings.Contains(addr, ":") {
		host = "[" + addr + "]"
	}
	out, err := gwInNS(t, ns, "curl", "--noproxy", "*", "-fsS", "--retry", "3", "--retry-connrefused", "--retry-delay", "1", "--max-time", "2", "http://"+host+":8080")
	if err != nil || strings.TrimSpace(out) != want {
		t.Fatalf("%s observation got %q (%v), want source %s", addr, out, err, want)
	}
}

// Resolver-manager integration is outside this routing fixture. Keep every
// generated address and AllowedIPs, and test UDP to its declared resolvers.
func wgDualClientText(text string) string {
	conf := parseWGConf(text)
	var body []wgLine
	for _, line := range conf.iface().body {
		if line.key != "dns" {
			body = append(body, line)
		}
	}
	conf.iface().body = body
	return conf.render()
}

func TestLiveWireGuardDualStackSplitFullAndContainment(t *testing.T) {
	gwLiveRequired(t)
	for _, tool := range []string{"wg", "wg-quick", "ip6tables"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	for _, transport6 := range []bool{false, true} {
		t.Run(fmt.Sprintf("IPv6_transport_%t", transport6), func(t *testing.T) {
			server := gwLiveNS(t, fmt.Sprintf("wg-s%t", transport6))
			client := gwLiveNS(t, fmt.Sprintf("wg-c%t", transport6))
			wan := gwLiveNS(t, fmt.Sprintf("wg-w%t", transport6))
			for _, ns := range []string{server, client, wan} {
				gwMustInNS(t, ns, "ip", "link", "set", "lo", "up")
				gwMustInNS(t, ns, "sysctl", "-w", "net.ipv6.conf.all.disable_ipv6=0")
			}
			wgDualLiveLink(t, server, "transport", client, "transport", "172.30.0.1/24", "172.30.0.2/24", "fd99:1::1/64", "fd99:1::2/64")
			wgDualLiveLink(t, server, "wan4", wan, "server4", "192.0.2.2/24", "192.0.2.1/24", "", "")
			wgDualLiveLink(t, server, "wan6", wan, "server6", "", "", "2001:db8:2::2/64", "2001:db8:2::1/64")
			wgDualLiveLink(t, client, "native", wan, "native-peer", "198.51.100.2/24", "198.51.100.1/24", "2001:db8:3::2/64", "2001:db8:3::1/64")
			gwMustInNS(t, server, "ip", "route", "add", "default", "via", "192.0.2.1", "dev", "wan4")
			gwMustInNS(t, server, "ip", "-6", "route", "add", "default", "via", "2001:db8:2::1", "dev", "wan6")
			gwMustInNS(t, client, "ip", "route", "add", "default", "via", "198.51.100.1", "dev", "native")
			gwMustInNS(t, client, "ip", "-6", "route", "add", "default", "via", "2001:db8:3::1", "dev", "native")
			gwMustInNS(t, server, "sysctl", "-w", "net.ipv4.ip_forward=1", "net.ipv6.conf.all.forwarding=1")
			gwMustInNS(t, server, "iptables", "-P", "FORWARD", "DROP")
			gwMustInNS(t, server, "ip6tables", "-P", "FORWARD", "DROP")
			for _, addr := range []string{"1.1.1.1/32", "9.9.9.9/32", "2606:4700:4700::1111/128", "2620:fe::fe/128"} {
				gwMustInNS(t, wan, "ip", "addr", "add", addr, "dev", "lo", "nodad")
				wgDualLiveServe(t, wan, strings.Split(addr, "/")[0])
			}
			s := vpnService(t)
			oldRun, oldHas, oldAnchors, oldClass := run, has, anchorPaths, gatewayClassNet
			t.Cleanup(func() { run, has, anchorPaths, gatewayClassNet = oldRun, oldHas, oldAnchors, oldClass })
			run = wgDualLiveRun(server, s)
			has = func(name string) bool {
				if name == "systemctl" {
					return true
				}
				_, err := exec.LookPath(name)
				return err == nil && name != "firewall-cmd" && name != "ufw"
			}
			anchorPaths = func(context.Context) []anchorPath { return nil }
			gatewayClassNet = t.TempDir()
			endpoint := "172.30.0.1:51880"
			if transport6 {
				endpoint = "[fd99:1::1]:51880"
			}
			made, err := s.CreateWireGuard(context.Background(), WGServerRequest{Name: "jdwgs", Port: 51880, Subnet: "10.77.0.0/24", Endpoint: endpoint, DNS: []string{"1.1.1.1", "2606:4700:4700::1111"}, ExitNode: true, IPv6: &WGIPv6Request{Subnet: "fd42:77::/64", ExitNode: true}}, "fixture")
			if err != nil {
				t.Fatal(err)
			}
			if made.Interface.Families.IPv4.Exit.Runtime != "verified" || made.Interface.Families.IPv6.Exit.Runtime != "verified" {
				t.Fatalf("family evidence: %+v", made.Interface.Families)
			}
			serverPath, _ := s.wgConfPath("jdwgs")
			beforeFile, _ := os.ReadFile(serverPath)
			beforeSpec := wgMustString(t, wgMustSpec(t, s))
			gwMustInNS(t, server, "env", "PATH="+os.Getenv("PATH"), "wg", "set", "jdwgs", "peer", fixPeerC, "allowed-ips", "fd42:77::99/128")
			beforePeers := gwMustInNS(t, server, "env", "PATH="+os.Getenv("PATH"), "wg", "show", "jdwgs", "allowed-ips")
			if _, err := s.AddWireGuardPeer(context.Background(), "jdwgs", WGPeerRequest{Name: "refused", Kind: wgKindDevice}, "", "fixture"); err == nil || !strings.Contains(err.Error(), "native owner") {
				t.Fatalf("foreign native peer provisioning not refused: %v", err)
			}
			both := true
			if _, err := s.SetWireGuardExitFamilies(context.Background(), "jdwgs", true, &both, "fixture"); err == nil || !strings.Contains(err.Error(), "native owner") {
				t.Fatalf("foreign native peer family edit not refused: %v", err)
			}
			afterFile, _ := os.ReadFile(serverPath)
			afterPeers := gwMustInNS(t, server, "env", "PATH="+os.Getenv("PATH"), "wg", "show", "jdwgs", "allowed-ips")
			if !bytes.Equal(beforeFile, afterFile) || beforePeers != afterPeers || beforeSpec != wgMustString(t, wgMustSpec(t, s)) {
				t.Fatal("refusal changed saved bytes, native peer routes or gateway intent")
			}
			gwMustInNS(t, server, "env", "PATH="+os.Getenv("PATH"), "wg", "set", "jdwgs", "peer", fixPeerC, "remove")
			clientPath := filepath.Join(t.TempDir(), "jdwgc.conf")
			for _, full := range []bool{false, true} {
				peer, err := s.AddWireGuardPeer(context.Background(), "jdwgs", WGPeerRequest{Name: fmt.Sprintf("client-%t", full), Kind: wgKindDevice, FullTunnel: full, ShareNetworks: []string{"1.1.1.1/32", "2606:4700:4700::1111/128"}}, "", "fixture")
				if err != nil {
					t.Fatal(err)
				}
				wgWriteFile(t, clientPath, wgDualClientText(peer.Config))
				out, err := gwInNS(t, client, "env", "PATH="+os.Getenv("PATH"), "wg-quick", "up", clientPath)
				if err != nil {
					t.Fatalf("bringing up generated peer: %v\n%s", err, out)
				}
				wgDualLiveFetch(t, client, "1.1.1.1", "192.0.2.2")
				wgDualLiveFetch(t, client, "2606:4700:4700::1111", "2001:db8:2::2")
				for _, dns := range []struct{ addr, want string }{{"1.1.1.1", "192.0.2.2"}, {"2606:4700:4700::1111", "2001:db8:2::2"}} {
					out, err := gwInNS(t, client, "python3", "-c", wgDualDNSQuery, dns.addr)
					if err != nil || strings.TrimSpace(out) != dns.want {
						t.Fatalf("configured resolver UDP routing: %q %v", out, err)
					}
				}
				v4, v6 := "198.51.100.2", "2001:db8:3::2"
				if full {
					v4, v6 = "192.0.2.2", "2001:db8:2::2"
				}
				wgDualLiveFetch(t, client, "9.9.9.9", v4)
				wgDualLiveFetch(t, client, "2620:fe::fe", v6)
				handshake := gwMustInNS(t, server, "env", "PATH="+os.Getenv("PATH"), "wg", "show", "jdwgs", "latest-handshakes")
				if strings.Contains(handshake, peer.Peer.PublicKey+"\t0\n") {
					t.Fatal("no measured handshake")
				}
				if full {
					off := false
					if _, err := s.SetWireGuardExitFamilies(context.Background(), "jdwgs", true, &off, "fixture"); err != nil {
						t.Fatal(err)
					}
					prior := wgMustString(t, wgMustSpec(t, s))
					baseRun := run
					run = func(ctx context.Context, name string, args ...string) (string, error) {
						if name == "ip6tables" && len(args) > 1 && args[0] == "-I" && args[1] == "FORWARD" {
							return "", fmt.Errorf("fixture permission refusal")
						}
						return baseRun(ctx, name, args...)
					}
					on := true
					_, enableErr := s.SetWireGuardExitFamilies(context.Background(), "jdwgs", true, &on, "fixture")
					run = baseRun
					if enableErr == nil || wgMustString(t, wgMustSpec(t, s)) != prior {
						t.Fatal("failed IPv6 admission did not retain prior IPv4 exit intent")
					}
					wgDualLiveFetch(t, client, "9.9.9.9", "192.0.2.2")
					if _, err := gwInNS(t, client, "curl", "--noproxy", "*", "-fsS", "--max-time", "2", "http://[2620:fe::fe]:8080"); err == nil {
						t.Fatal("IPv6 escaped its full tunnel after IPv6 exit refusal")
					}
					if _, err := s.SetWireGuardExitFamilies(context.Background(), "jdwgs", true, &on, "fixture"); err != nil {
						t.Fatal(err)
					}
					wgDualLiveFetch(t, client, "2620:fe::fe", "2001:db8:2::2")
					gwMustInNS(t, client, "env", "PATH="+os.Getenv("PATH"), "wg", "set", "jdwgc", "peer", made.Interface.PublicKey, "endpoint", "127.0.0.1:9")
					for _, host := range []string{"9.9.9.9", "[2620:fe::fe]"} {
						if _, err := gwInNS(t, client, "curl", "--noproxy", "*", "-fsS", "--max-time", "2", "http://"+host+":8080"); err == nil {
							t.Fatal("full tunnel fell back to native egress after transport failure")
						}
					}
					for _, dns := range []string{"1.1.1.1", "2606:4700:4700::1111"} {
						if _, err := gwInNS(t, client, "python3", "-c", wgDualDNSQuery, dns); err == nil {
							t.Fatal("full tunnel resolver UDP fell back to native egress")
						}
					}
				}
				gwMustInNS(t, client, "env", "PATH="+os.Getenv("PATH"), "wg-quick", "down", clientPath)
			}
			if !transport6 {
				legacy, err := s.CreateWireGuard(context.Background(), WGServerRequest{Name: "jdwglegacy", Port: 51881, Subnet: "10.78.0.0/24", Endpoint: "172.30.0.1:51881", ExitNode: true}, "fixture")
				if err != nil {
					t.Fatal(err)
				}
				if legacy.Interface.IPv6Enabled {
					t.Fatal("legacy server silently gained IPv6 allocation")
				}
				peer, err := s.AddWireGuardPeer(context.Background(), "jdwglegacy", WGPeerRequest{Name: "legacy-full", Kind: wgKindDevice, FullTunnel: true}, "", "fixture")
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(peer.Config, "IPv6 leak") {
					t.Fatal("legacy containment explanation lost")
				}
				wgWriteFile(t, clientPath, wgDualClientText(peer.Config))
				gwMustInNS(t, client, "env", "PATH="+os.Getenv("PATH"), "wg-quick", "up", clientPath)
				wgDualLiveFetch(t, client, "9.9.9.9", "192.0.2.2")
				if _, err := gwInNS(t, client, "curl", "--noproxy", "*", "-fsS", "--max-time", "2", "http://[2620:fe::fe]:8080"); err == nil {
					t.Fatal("legacy full client leaked IPv6 onto its native path")
				}
				gwMustInNS(t, client, "env", "PATH="+os.Getenv("PATH"), "wg-quick", "down", clientPath)
				if _, err := s.SetWireGuardExit(context.Background(), "jdwglegacy", false, "fixture"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.SetWireGuardExit(context.Background(), "jdwgs", false, "fixture"); err != nil {
				t.Fatal(err)
			}
			if len(wgMustSpec(t, s).NAT) != 0 {
				t.Fatal("one family remained configured after exit removal")
			}
		})
	}
}
