package netx

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
)

// procSysRoot is where the kernel's settings are read from; a variable so
// tests can stand a directory behind it.
var procSysRoot = "/proc/sys"

// The sysctl keys that switch a family's forwarding, and the files they are.
const (
	sysctlForwardV4 = "net.ipv4.ip_forward"
	sysctlForwardV6 = "net.ipv6.conf.all.forwarding"
)

// ForwardingNeeds is what the rest of the server knows depends on forwarding,
// supplied by the api layer because it is Docker's and Tailscale's to say. What
// the dashboard's own spec needs (port forwards, NAT, WireGuard exits) is read
// from the spec here.
type ForwardingNeeds struct {
	// DockerNetworks counts the bridge networks Docker runs; their containers
	// reach each other and the internet through this host's forwarding.
	DockerNetworks        int  `json:"dockerNetworks"`
	TailscaleExitNode     bool `json:"tailscaleExitNode"`
	TailscaleSubnetRoutes bool `json:"tailscaleSubnetRoutes"`
}

// ForwardingView is whether this host passes traffic between networks.
type ForwardingView struct {
	IPv4 ForwardingState `json:"ipv4"`
	IPv6 ForwardingState `json:"ipv6"`
}

// ForwardingState is one family's.
type ForwardingState struct {
	// Available is false where the kernel has no such setting, an IPv6-less
	// host.
	Available bool `json:"available"`
	Enabled   bool `json:"enabled"`
	// Persisted is whether the dashboard's boot file sets it, and so whether
	// it survives a restart whatever the distribution's own settings say.
	Persisted bool `json:"persisted"`
	// NeededBy names what stops working if it is turned off.
	NeededBy []string `json:"neededBy"`
	// Guard is why it cannot be turned off, when it cannot.
	Guard string `json:"guard,omitempty"`
}

// forwardingKey maps a family name to its sysctl key and canonical spelling.
func forwardingKey(family string) (key, canon string, err error) {
	switch strings.ToLower(strings.TrimSpace(family)) {
	case "ipv4", "inet", "4":
		return sysctlForwardV4, "ipv4", nil
	case "ipv6", "inet6", "6":
		return sysctlForwardV6, "ipv6", nil
	}
	return "", "", fmt.Errorf("the family is ipv4 or ipv6")
}

func procPath(key string) string {
	return filepath.Join(procSysRoot, strings.ReplaceAll(key, ".", "/"))
}

// readSysctl reads a kernel setting through /proc/sys, which needs no process.
func readSysctl(key string) (string, error) {
	b, err := os.ReadFile(procPath(key))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// forwardingNeeds lists what needs a family's forwarding.
func forwardingNeeds(sp *Spec, needs ForwardingNeeds, canon string) []string {
	out := []string{}
	if canon == "ipv4" && needs.DockerNetworks > 0 {
		out = append(out, fmt.Sprintf("%d Docker %s", needs.DockerNetworks, plural(needs.DockerNetworks, "network", "networks")))
	}
	if needs.TailscaleExitNode {
		out = append(out, "Tailscale's exit node")
	}
	if needs.TailscaleSubnetRoutes {
		out = append(out, "Tailscale's subnet routes")
	}
	wants := func(raw string) bool {
		a, err := netip.ParsePrefix(raw)
		if err != nil {
			addr, err := netip.ParseAddr(raw)
			if err != nil {
				return canon == "ipv4"
			}
			return addr.Is4() == (canon == "ipv4")
		}
		return a.Addr().Is4() == (canon == "ipv4")
	}
	for _, f := range sp.Forwards {
		if f.Enabled && wants(f.Target) {
			out = append(out, fmt.Sprintf("the port forward %q", f.Name))
		}
	}
	for _, n := range sp.NAT {
		if !n.Enabled || !wants(n.Source) {
			continue
		}
		if iface, ok := strings.CutPrefix(n.Owner, "wireguard:"); ok {
			out = append(out, fmt.Sprintf("the WireGuard exit through %s", iface))
			continue
		}
		out = append(out, fmt.Sprintf("the NAT entry %q", n.Name))
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// Forwarding reads both families' state and what depends on each. The
// dashboard runs in the host's network namespace, so /proc/sys/net is the
// host's own.
func (s *Service) Forwarding(ctx context.Context, needs ForwardingNeeds) ForwardingView {
	sp, err := s.loadSpec()
	if err != nil {
		sp = emptySpec()
	}
	return forwardingFrom(sp, needs)
}

// SetForwarding turns a family's forwarding on or off. Off is refused while
// anything needs it, naming what.
func (s *Service) SetForwarding(ctx context.Context, family string, on bool, needs ForwardingNeeds, actor string) (*ForwardingView, error) {
	key, canon, err := forwardingKey(family)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sp, err := s.loadSpec()
	if err != nil {
		return nil, err
	}
	next := sp.clone()
	prev, err := readSysctl(key)
	if err != nil {
		return nil, fmt.Errorf("%s forwarding is not available on this host", canon)
	}
	if !on {
		if by := forwardingNeeds(next, needs, canon); len(by) > 0 {
			return nil, guarded("%s forwarding cannot be turned off: %s would stop working.", canon, strings.Join(by, ", "))
		}
	}
	if on && canon == "ipv6" {
		if err := raForwardingGuard(ctx); err != nil {
			return nil, err
		}
	}
	val := "0"
	if on {
		val = "1"
	}
	next.Sysctls[key] = val
	keys := []string{key}
	want, previous := map[string]string{key: val}, map[string]string{key: prev}
	// Linux resets this protection when ip_forward changes. Preserve the
	// dashboard's managed choice immediately as well as in the boot drop-in.
	redirectKey := "net.ipv4.conf.all.accept_redirects"
	if canon == "ipv4" && prev != val {
		if protection, set := next.Sysctls[redirectKey]; set {
			cur, err := readSysctl(redirectKey)
			if err != nil {
				return nil, fmt.Errorf("reading the managed redirect policy before changing forwarding: %w", err)
			}
			keys = append(keys, redirectKey)
			want[redirectKey], previous[redirectKey] = protection, cur
		}
	}
	undo := func(ctx context.Context) { restoreSysctls(ctx, keys, previous) }
	err = s.commit(ctx, next, step{
		apply: applying(func(ctx context.Context) error {
			for _, k := range keys {
				if _, err := run(ctx, "sysctl", "-w", k+"="+want[k]); err != nil {
					return err
				}
			}
			return nil
		}, undo),
		undo: undo,
		verify: func(context.Context) error {
			for _, k := range keys {
				got, err := readSysctl(k)
				if err != nil {
					return err
				}
				if got != want[k] {
					return fmt.Errorf("the kernel still reports %s = %s", k, got)
				}
			}
			return nil
		},
	})
	if err != nil {
		return nil, err
	}
	view := forwardingFrom(next, needs)
	return &view, nil
}

// forwardingFrom is Forwarding over a spec already in hand, which SetForwarding
// holds the lock for.
func forwardingFrom(sp *Spec, needs ForwardingNeeds) ForwardingView {
	state := func(key, canon string) ForwardingState {
		st := ForwardingState{NeededBy: forwardingNeeds(sp, needs, canon)}
		if v, err := readSysctl(key); err == nil {
			st.Available, st.Enabled = true, v == "1"
		}
		_, st.Persisted = sp.Sysctls[key]
		if len(st.NeededBy) > 0 {
			st.Guard = "Turned off, " + strings.Join(st.NeededBy, ", ") + " would stop working."
		}
		return st
	}
	return ForwardingView{IPv4: state(sysctlForwardV4, "ipv4"), IPv6: state(sysctlForwardV6, "ipv6")}
}

// raForwardingGuard refuses turning IPv6 forwarding on for a host whose IPv6
// default route was learned from a router advertisement on a device that does
// not keep honouring them under forwarding. With forwarding on the kernel
// ignores advertisements on a device unless its accept_ra is 2, so the route
// expires at the end of its lifetime and IPv6 goes with it. A device whose
// accept_ra is already 2 keeps its route, and is not refused.
func raForwardingGuard(ctx context.Context) error {
	out, err := run(ctx, "ip", "-j", "-6", "route", "show", "default")
	if err != nil {
		return err
	}
	routes, err := parseIPRoutes(out)
	if err != nil {
		return err
	}
	for _, r := range routes {
		if r.Protocol != "ra" || ValidIfName(r.Dev) != nil {
			continue
		}
		// The device's name goes into the path as a path element, not through
		// sysctl's dot-for-slash key form, which a VLAN's "eth0.100" would
		// break.
		b, err := os.ReadFile(filepath.Join(procSysRoot, "net", "ipv6", "conf", r.Dev, "accept_ra"))
		if err == nil && strings.TrimSpace(string(b)) == "2" {
			continue
		}
		return guarded("This server's IPv6 default route on %s was learned from a router advertisement, and with IPv6 forwarding on the kernel ignores advertisements on a device unless its accept_ra is 2; the route would expire and IPv6 connectivity with it. Set net.ipv6.conf.%s.accept_ra to 2 first.", r.Dev, r.Dev)
	}
	return nil
}
