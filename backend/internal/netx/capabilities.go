package netx

import (
	"context"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// HostSupport describes prerequisites, not a promise that a virtual NIC or
// provider permits every operation. Missing kernel modules, restricted
// containers and upstream firewalls can still reject a valid command.
type HostSupport struct {
	Tools       []ToolSupport `json:"tools"`
	Persistence string        `json:"persistence"`
	IPv6        bool          `json:"ipv6"`
	Notes       []string      `json:"notes"`
}

type ToolSupport struct {
	Tool      string `json:"tool"`
	Available bool   `json:"available"`
	Purpose   string `json:"purpose"`
	Package   string `json:"package,omitempty"`
}

var hostTool = hostexec.AvailableOnHost

func (s *Service) HostSupport(ctx context.Context) HostSupport {
	view := HostSupport{Tools: []ToolSupport{}, Notes: []string{
		"Tool availability does not prove kernel, NIC or provider support. Individual operations still validate and guard the live network.",
		"Cloud security groups, provider NAT, hypervisor filtering and home-router port mappings are configured outside this host.",
		"Wake-on-LAN reaches the selected local Ethernet segment; the target firmware and NIC must support and enable it.",
		"Upstream network managers own DHCP, Wi-Fi and provider addresses; this dashboard preserves their configuration.",
	}}
	for _, tool := range []ToolSupport{
		{Tool: "ip", Package: "iproute2", Purpose: "devices, addresses, routes, IPv4/IPv6 policy rules and namespaces"},
		{Tool: "tc", Package: "iproute2", Purpose: "queue disciplines and bandwidth limits"},
		{Tool: "nft", Package: "nftables", Purpose: "gateway, NAT, blocklists and connection limits"},
		{Tool: "iptables", Package: "iptables", Purpose: "IPv4 gateway admission through ufw and Docker"},
		{Tool: "ip6tables", Package: "iptables", Purpose: "IPv6 gateway admission through ufw and Docker"},
		{Tool: "systemctl", Package: "systemd", Purpose: "restore network changes at boot and manage host services"},
		{Tool: "wg", Package: "wireguard-tools", Purpose: "WireGuard tunnel and peer inventory"},
		{Tool: "wg-quick", Package: "wireguard-tools", Purpose: "managed WireGuard server configuration"},
		{Tool: "resolvectl", Package: "systemd-resolved", Purpose: "resolver settings and statistics"},
		{Tool: "tailscale", Package: "tailscale", Purpose: "tailnet inventory, exit node and subnet advertisements"},
		{Tool: "headscale", Package: "headscale", Purpose: "native Headscale inventory (container installations are also detected on the VPN page)"},
		{Tool: "vtysh", Package: "frr", Purpose: "BGP peer inventory"},
		{Tool: "bpftool", Package: "bpftool", Purpose: "loaded eBPF program and attachment inventory"},
		{Tool: "ss", Package: "iproute2", Purpose: "sockets, TCP process traffic and listeners"},
		{Tool: "ping", Package: "iputils-ping", Purpose: "ICMP reachability"},
		{Tool: "traceroute", Package: "traceroute", Purpose: "hop-by-hop reachability (tracepath is a fallback)"},
		{Tool: "tracepath", Package: "iputils-tracepath", Purpose: "path MTU and traceroute fallback"},
		{Tool: "tcpdump", Package: "tcpdump", Purpose: "bounded packet metadata snapshots"},
		{Tool: "whois", Package: "whois", Purpose: "registration and autonomous system ownership"},
		{Tool: "ssh-keyscan", Package: "openssh-client", Purpose: "SSH host key fingerprints"},
	} {
		tool.Available = hostTool(tool.Tool)
		view.Tools = append(view.Tools, tool)
	}
	view.Persistence = "unavailable"
	if hostTool("systemctl") {
		// A systemctl binary can exist in a non-systemd container. Probe the
		// manager instead of claiming the network will survive reboot.
		state, _ := run(ctx, "systemctl", "is-system-running")
		switch strings.TrimSpace(state) {
		case "running", "degraded", "starting", "initializing":
			view.Persistence = "systemd"
		default:
			view.Persistence = "systemd manager unreachable"
		}
	}
	if raw, err := readSysctl("net.ipv6.conf.all.disable_ipv6"); err == nil {
		view.IPv6 = strings.TrimSpace(raw) == "0"
	}
	view.Notes = append(view.Notes, "Package names are common Debian/Ubuntu names; use the equivalent package for the host distribution.")
	return view
}
