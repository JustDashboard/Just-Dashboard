package netx

import (
	"context"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// IPAMNativeInventory keeps failures beside prefixes rather than suppressing
// failed family and namespace reads. No native resource is changed or adopted.
type IPAMNativeInventory struct {
	Prefixes []IPAMNativePrefix
	Sources  []IPAMNativeSource
}
type IPAMNativePrefix struct{ Prefix, Owner, Resource, Domain, Basis string }
type IPAMNativeSource struct {
	Source, State, Detail string
	CheckedAt             time.Time
}

func (s *Service) IPAMInventory(ctx context.Context, inv Inventory) IPAMNativeInventory {
	result := IPAMNativeInventory{Prefixes: []IPAMNativePrefix{}, Sources: []IPAMNativeSource{}}
	source := func(name string, e error, detail string) {
		if len(result.Sources) >= 128 {
			return
		}
		if len(result.Sources) == 127 {
			result.Sources = append(result.Sources, IPAMNativeSource{"bounded_native_sources", "unknown", "Native source evidence exceeded the 128-item inspection bound", time.Now().UTC()})
			return
		}
		state := "observed"
		if e != nil {
			state, detail = "unreadable", e.Error()
		}
		result.Sources = append(result.Sources, IPAMNativeSource{name, state, detail, time.Now().UTC()})
	}
	prefixLimitReported := false
	add := func(value, owner, resource, domain, basis string) {
		p, e := netip.ParsePrefix(value)
		if e != nil || p.Addr().Is4In6() {
			source(domain, fmt.Errorf("an owner returned an unreadable prefix"), "")
			return
		}
		if p.Bits() == 0 || p.Addr().IsLoopback() || p.Addr().IsLinkLocalUnicast() || p.Addr().IsMulticast() {
			return
		}
		if len(result.Prefixes) >= 4096 {
			if !prefixLimitReported {
				prefixLimitReported = true
				source("bounded_native", fmt.Errorf("native prefixes exceed the 4096-item inspection bound"), "")
			}
			return
		}
		result.Prefixes = append(result.Prefixes, IPAMNativePrefix{p.Masked().String(), owner, resource, domain, basis})
	}
	sp, e := s.loadSpec()
	if e != nil {
		sp = emptySpec()
		source("saved_ownership", e, "")
	} else {
		source("saved_ownership", nil, "Saved identities were read; foreign resources remain native owned")
	}
	linkRaw, linkErr := run(ctx, "ip", "-j", "-d", "link", "show")
	addrRaw, addrErr := run(ctx, "ip", "-j", "addr", "show")
	if linkErr != nil {
		source("host_links", linkErr, "")
	}
	if addrErr != nil {
		source("host_addresses", addrErr, "")
	}
	if linkErr == nil && addrErr == nil {
		links, err := parseLinks(linkRaw, addrRaw)
		source("host_addresses", err, "Current host addresses were read in both families")
		bridge := func(name string) bool {
			for _, n := range inv.Networks {
				if n.Bridge == name {
					return true
				}
			}
			return false
		}
		for _, l := range links {
			managed := false
			for _, owned := range sp.Links {
				managed = managed || owned.Name == l.Name
			}
			for _, a := range l.Addresses {
				add(a.CIDR, ownerOf(l, managed, bridge), l.Name, "host_addresses", "observed_native")
			}
		}
	}
	for _, family := range []string{"inet", "inet6"} {
		args := []string{"-j"}
		if family == "inet6" {
			args = append(args, "-6")
		}
		args = append(args, "route", "show", "table", "all")
		raw, err := run(ctx, "ip", args...)
		if err != nil {
			source("host_routes/"+family, err, "")
			continue
		}
		routes, err := parseIPRoutes(raw)
		source("host_routes/"+family, err, "Non-default all-table selectors were read; a route is not a provider allocation")
		for _, r := range routes {
			if r.Dst == "" || r.Dst == "default" {
				continue
			}
			owner := "route"
			if r.Protocol != "" {
				owner += ":" + r.Protocol
			}
			add(r.Dst, owner, r.Dev+"/table:"+string(r.Table), "host_routes/"+family, "observed_native")
		}
	}
	s.ipamWireGuard(ctx, source, add)
	ts, err := s.Tailscale(ctx, "")
	if err != nil {
		source("tailscale", err, "")
	} else if !ts.Installed {
		source("tailscale", fmt.Errorf("Tailscale tool unavailable; remote tailnet allocations remain unknown"), "")
	} else {
		source("tailscale_status", nil, "This node's native approved-route observations were read")
		if !ts.PrefsReadable {
			source("tailscale_preferences", fmt.Errorf("Tailscale offers could not be read"), "")
		} else {
			source("tailscale_preferences", nil, "Native advertised subnet routes were read")
		}
		for _, p := range ts.Prefs.AdvertiseRoutes {
			add(p, "tailscale_offer", "this host", "tailscale", "observed_native")
		}
		if ts.Self != nil {
			for _, p := range ts.Self.PrimaryRoutes {
				add(p, "tailscale_approved", ts.Self.HostName, "tailscale", "observed_native")
			}
			for _, value := range ts.Self.TailscaleIPs {
				a, err := netip.ParseAddr(value)
				if err == nil {
					add(netip.PrefixFrom(a, a.BitLen()).String(), "tailscale_address", ts.Self.HostName, "tailscale", "observed_native")
				}
			}
		}
		for _, peer := range ts.Peers {
			for _, p := range peer.PrimaryRoutes {
				add(p, "tailscale_approved", peer.HostName, "tailscale", "observed_native")
			}
		}
	}
	raw, err := run(ctx, "ip", "-j", "netns", "list")
	if err != nil {
		source("named_namespaces", err, "")
	} else {
		names, e := parseNamespaces(raw)
		source("named_namespaces", e, "Named namespace inventory was read")
		if len(names) > 64 {
			names = names[:64]
			source("named_namespaces", fmt.Errorf("named namespace inventory exceeds the 64-item inspection bound"), "")
		}
		for _, n := range names {
			if ctx.Err() != nil {
				source("named_namespaces", ctx.Err(), "")
				break
			}
			if e := ValidNamespace(n.Name); e != nil {
				source("namespace/"+n.Name, fmt.Errorf("native namespace name is outside the supported grammar"), "")
				continue
			}
			out, e := run(ctx, "ip", "-n", n.Name, "-j", "addr", "show")
			if e != nil {
				source("namespace/"+n.Name, e, "")
				continue
			}
			devices, e := parseNamespaceDevices(out)
			source("namespace/"+n.Name, e, "Namespace addresses were read; isolation does not imply connectivity")
			owner := "foreign_namespace"
			if hasNamespace(sp, n.Name) {
				owner = "just-dashboard_namespace"
			}
			for _, d := range devices {
				for _, p := range d.Addresses {
					add(p, owner, n.Name+"/"+d.Name, "namespace_addresses", "observed_native")
				}
			}
		}
	}
	result.Sources = append(result.Sources, IPAMNativeSource{"foreign_provider_scope", "unknown", "Provider allocations, remote/unnamed/container-only configuration and namespace routes are not authoritative from this host inventory", time.Now().UTC()})
	return result
}

func (s *Service) ipamWireGuard(ctx context.Context, source func(string, error, string), add func(string, string, string, string, string)) {
	entries, e := os.ReadDir(s.paths.WireGuard)
	if os.IsNotExist(e) {
		e, entries = nil, nil
	}
	source("wireguard_files", e, "Configured addresses and non-default peer selectors were read")
	if len(entries) > 256 {
		entries = entries[:256]
		source("wireguard_files", fmt.Errorf("WireGuard files exceed the 256-file inspection bound"), "")
	}
	remaining := 8 << 20
	for _, entry := range entries {
		if ctx.Err() != nil {
			source("wireguard_files", ctx.Err(), "")
			break
		}
		name, ok := strings.CutSuffix(entry.Name(), ".conf")
		if !ok || entry.IsDir() {
			continue
		}
		if e := validWGName(name); e != nil {
			source("wireguard_file", fmt.Errorf("unsupported native WireGuard file name"), "")
			continue
		}
		raw, e := readIPAMWireGuardFile(filepath.Join(s.paths.WireGuard, entry.Name()))
		if e != nil {
			source("wireguard_file/"+name, e, "")
			continue
		}
		remaining -= len(raw)
		if remaining < 0 {
			source("wireguard_files", fmt.Errorf("WireGuard configuration exceeds the 8 MiB inspection bound"), "")
			break
		}
		conf := parseWGConf(string(raw))
		if conf.iface() == nil {
			source("wireguard_file/"+name, fmt.Errorf("WireGuard file lacks a supported Interface section"), "")
			continue
		}
		owner := "foreign_wireguard"
		if conf.managed {
			owner = "just-dashboard_wireguard"
		}
		for _, p := range conf.iface().list("address") {
			add(p, owner, name, "wireguard_configuration", "configured_native")
		}
		for _, peer := range conf.peers() {
			for _, p := range peer.list("allowedips") {
				add(p, owner, name+"/peer", "wireguard_configuration", "configured_native")
			}
		}
	}
	if !has("wg") {
		source("wireguard_kernel", fmt.Errorf("WireGuard tool unavailable; kernel peer selectors remain unknown"), "")
		return
	}
	raw, e := run(ctx, "wg", "show", "all", "allowed-ips")
	source("wireguard_kernel", e, "Native non-default peer selectors were read; private keys were not queried")
	if e != nil {
		return
	}
	for _, line := range strings.Split(raw, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 3 {
			source("wireguard_kernel", fmt.Errorf("native selector output is unreadable"), "")
			continue
		}
		for _, p := range strings.Fields(strings.ReplaceAll(parts[2], ",", " ")) {
			if p != "(none)" {
				add(p, "wireguard_peer", parts[0]+"/peer", "wireguard_kernel", "observed_native")
			}
		}
	}
}

func readIPAMWireGuardFile(path string) ([]byte, error) {
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, fmt.Errorf("WireGuard file is not a bounded regular native configuration")
	}
	raw, e := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if e != nil {
		return nil, e
	}
	if len(raw) > 1<<20 {
		return nil, fmt.Errorf("WireGuard file exceeds the inventory bound")
	}
	return raw, nil
}
