package netx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"golang.org/x/sys/unix"
)

// HostSupport describes prerequisites, not a promise that a virtual NIC or
// provider permits every operation. Missing kernel modules, restricted
// containers and upstream firewalls can still reject a valid command.
type HostSupport struct {
	Tools []ToolSupport `json:"tools"`
	// Probes test what a binary's presence cannot: whether the kernel and
	// this process's privileges allow the operation. Every probe is a read
	// or an open-and-close; none changes host state.
	Probes      []CapabilityProbe `json:"probes"`
	Persistence string            `json:"persistence"`
	IPv6State   string            `json:"ipv6State"`
	Notes       []string          `json:"notes"`
}

type ToolSupport struct {
	Tool      string `json:"tool"`
	Available bool   `json:"available"`
	Purpose   string `json:"purpose"`
	Package   string `json:"package,omitempty"`
}

// CapabilityProbe is one read-only test of a kernel or privilege capability.
type CapabilityProbe struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Status   string `json:"status"`
	Evidence string `json:"evidence"`
	Enables  string `json:"enables"`
}

// Capability probe statuses.
const (
	ProbeSupported   = "supported"
	ProbeUnsupported = "unsupported"
	ProbeRestricted  = "restricted"
	ProbeUnknown     = "unknown"
)

var (
	hostTool      = hostexec.AvailableOnHost
	sysModuleRoot = "/sys/module"
	// packetSocket opens and closes an AF_PACKET socket; nothing is sent.
	packetSocket = func() error {
		fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
		if err != nil {
			return err
		}
		return unix.Close(fd)
	}
)

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
		{Tool: "tcpdump", Package: "tcpdump", Purpose: "bounded packet summary snapshots"},
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
	view.IPv6State = "unavailable"
	if raw, err := readSysctl("net.ipv6.conf.all.disable_ipv6"); err == nil {
		view.IPv6State = "all.disable_ipv6=" + strings.TrimSpace(raw) + " (inspect individual interfaces for their IPv6 state)"
	}
	view.Notes = append(view.Notes, "Package names are common Debian/Ubuntu names; use the equivalent package for the host distribution.")
	view.Probes = capabilityProbes(ctx)
	return view
}

// capabilityProbes asks the kernel and this process what they allow. A
// missing binary makes its probe unknown rather than unsupported: the
// question was not asked.
func capabilityProbes(ctx context.Context) []CapabilityProbe {
	var out []CapabilityProbe
	add := func(id, label, enables, status, evidence string) {
		out = append(out, CapabilityProbe{ID: id, Label: label, Status: status, Evidence: evidence, Enables: enables})
	}

	switch err := packetSocket(); {
	case err == nil:
		add("packet_socket", "Raw packet sockets", "Wake-on-LAN frames from the dashboard process", ProbeSupported, "an AF_PACKET socket was opened and closed; nothing was sent")
	case errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES):
		add("packet_socket", "Raw packet sockets", "Wake-on-LAN frames from the dashboard process", ProbeRestricted, "opening an AF_PACKET socket was refused: the process lacks CAP_NET_RAW")
	default:
		add("packet_socket", "Raw packet sockets", "Wake-on-LAN frames from the dashboard process", ProbeUnsupported, "opening an AF_PACKET socket failed: "+err.Error())
	}

	if hostTool("nft") {
		tables, err := run(ctx, "nft", "list", "tables")
		lower := strings.ToLower(tables)
		switch {
		case err == nil:
			count := 0
			for _, line := range strings.Split(strings.TrimSpace(tables), "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "table ") {
					count++
				}
			}
			add("nftables", "nftables ruleset readable", "gateway, protection and firewall evidence", ProbeSupported, fmt.Sprintf("nft list tables succeeded (%d tables)", count))
		case strings.Contains(lower, "operation not permitted") || strings.Contains(lower, "permission denied"):
			add("nftables", "nftables ruleset readable", "gateway, protection and firewall evidence", ProbeRestricted, "nft list tables was refused: "+firstLine(tables))
		case strings.Contains(lower, "not supported") || strings.Contains(lower, "protocol not supported"):
			add("nftables", "nftables ruleset readable", "gateway, protection and firewall evidence", ProbeUnsupported, "the kernel has no nf_tables: "+firstLine(tables))
		default:
			add("nftables", "nftables ruleset readable", "gateway, protection and firewall evidence", ProbeUnknown, "nft list tables failed: "+firstLine(nonEmptyString(tables, err.Error())))
		}
	} else {
		add("nftables", "nftables ruleset readable", "gateway, protection and firewall evidence", ProbeUnknown, "nft is not installed, so the ruleset was not read")
	}

	if hostTool("iptables") {
		version, err := run(ctx, "iptables", "-V")
		switch {
		case err != nil:
			add("iptables_backend", "iptables backend", "Docker and ufw rule compatibility", ProbeUnknown, "iptables -V failed: "+firstLine(nonEmptyString(version, err.Error())))
		case strings.Contains(version, "nf_tables"):
			add("iptables_backend", "iptables backend", "Docker and ufw rule compatibility", ProbeSupported, "nf_tables backend: "+firstLine(version))
		case strings.Contains(version, "legacy"):
			add("iptables_backend", "iptables backend", "Docker and ufw rule compatibility", ProbeSupported, "legacy x_tables backend: "+firstLine(version)+"; its rules are not visible to nft")
		default:
			add("iptables_backend", "iptables backend", "Docker and ufw rule compatibility", ProbeSupported, firstLine(version))
		}
	}

	if entries, err := os.ReadDir(filepath.Join(procSysRoot, "net/ipv6/conf")); err != nil {
		add("ipv6", "IPv6 in the kernel", "IPv6 addresses, routes and diagnostics", ProbeUnsupported, "net.ipv6 sysctls are absent: the kernel has no IPv6")
	} else {
		var disabled []string
		for _, e := range entries {
			if e.Name() == "all" || e.Name() == "default" {
				continue
			}
			if value, err := readSysctl("net.ipv6.conf." + e.Name() + ".disable_ipv6"); err == nil && value == "1" {
				disabled = append(disabled, e.Name())
			}
		}
		if len(disabled) == 0 {
			add("ipv6", "IPv6 in the kernel", "IPv6 addresses, routes and diagnostics", ProbeSupported, "enabled on every interface")
		} else {
			sort.Strings(disabled)
			add("ipv6", "IPv6 in the kernel", "IPv6 addresses, routes and diagnostics", ProbeSupported, "available; disabled on "+strings.Join(disabled, ", "))
		}
	}

	v4, err4 := readSysctl(sysctlForwardV4)
	v6, err6 := readSysctl(sysctlForwardV6)
	if err4 != nil && err6 != nil {
		add("forwarding", "Packet forwarding", "gateway, NAT and VPN routing", ProbeUnknown, "the forwarding sysctls could not be read")
	} else {
		add("forwarding", "Packet forwarding", "gateway, NAT and VPN routing", ProbeSupported, fmt.Sprintf("IPv4 forwarding=%s, IPv6 forwarding=%s (read only)", nonEmptyString(v4, "unreadable"), nonEmptyString(v6, "unreadable")))
	}

	for _, module := range []struct{ id, name, label, enables string }{
		{"wireguard", "wireguard", "WireGuard kernel module", "WireGuard tunnels"},
		{"sch_cake", "sch_cake", "CAKE queue discipline", "CAKE shaping (HTB and fq_codel do not need it)"},
	} {
		if _, err := os.Stat(filepath.Join(sysModuleRoot, module.name)); err == nil {
			add(module.id, module.label, module.enables, ProbeSupported, "loaded")
			continue
		}
		if !hostTool("modinfo") {
			add(module.id, module.label, module.enables, ProbeUnknown, "not loaded; modinfo is not installed, so availability was not checked")
			continue
		}
		file, err := run(ctx, "modinfo", "-F", "filename", module.name)
		switch {
		case err == nil && strings.TrimSpace(file) != "":
			detail := "available, not loaded yet"
			if strings.TrimSpace(file) == "(builtin)" {
				detail = "built into the kernel"
			}
			add(module.id, module.label, module.enables, ProbeSupported, detail)
		default:
			add(module.id, module.label, module.enables, ProbeUnsupported, "not loaded and modinfo found no module")
		}
	}

	if count, err := readSysctl("net.netfilter.nf_conntrack_count"); err == nil {
		limit, _ := readSysctl("net.netfilter.nf_conntrack_max")
		add("conntrack", "Connection tracking", "NAT, stateful firewall rules and connection limits", ProbeSupported, fmt.Sprintf("%s tracked of %s", count, nonEmptyString(limit, "an unreadable maximum")))
	} else {
		add("conntrack", "Connection tracking", "NAT, stateful firewall rules and connection limits", ProbeUnknown, "nf_conntrack counters are not readable; the module may not be loaded yet")
	}

	if hostTool("findmnt") {
		if target, err := run(ctx, "findmnt", "-n", "-t", "bpf", "-o", "TARGET"); err == nil && strings.TrimSpace(target) != "" {
			add("bpffs", "BPF filesystem", "pinned eBPF programs and maps", ProbeSupported, "mounted at "+firstLine(target))
		} else {
			add("bpffs", "BPF filesystem", "pinned eBPF programs and maps", ProbeUnsupported, "no bpf filesystem is mounted on the host")
		}
	}

	if hostTool("systemctl") {
		state, _ := run(ctx, "systemctl", "is-active", "systemd-resolved")
		state = strings.TrimSpace(state)
		status := ProbeUnsupported
		if state == "active" {
			status = ProbeSupported
		}
		add("resolved", "systemd-resolved", "resolver settings, statistics and policy-aware DNS evidence", status, "systemctl is-active: "+nonEmptyString(firstLine(state), "no answer"))
	}
	return out
}

func firstLine(text string) string {
	return strings.TrimSpace(strings.SplitN(strings.TrimSpace(text), "\n", 2)[0])
}

func nonEmptyString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
