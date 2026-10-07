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

// ensureForwarding records in a spec that a family's forwarding is on. A change
// that creates something forwarding carries — a NAT entry, a port forward, a
// tunnel's exit — calls it on the copy of the spec it is about to commit, so
// the boot file turns forwarding on before the gateway table is loaded.
// Turning it on in the running kernel is enableForwarding's job, run in the
// change's apply; this only writes the spec.
func ensureForwarding(next *Spec, family string) {
	key, _, err := forwardingKey(family)
	if err != nil {
		return
	}
	if next.Sysctls == nil {
		next.Sysctls = map[string]string{}
	}
	next.Sysctls[key] = "1"
}

// enableForwarding turns a family's forwarding on in the running kernel. It is
// not guarded the way SetForwarding is, because it only ever adds: whoever
// calls it is creating something that has to work.
func enableForwarding(ctx context.Context, family string) error {
	key, _, err := forwardingKey(family)
	if err != nil {
		return err
	}
	if v, err := readSysctl(key); err == nil && v == "1" {
		return nil
	}
	_, err = run(ctx, "sysctl", "-w", key+"=1")
	return err
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
		// With forwarding on the kernel stops acting on router advertisements
		// unless accept_ra is 2, and a host whose IPv6 default route came from
		// one loses it when its lifetime runs out.
		out, err := run(ctx, "ip", "-j", "-6", "route", "show", "default")
		if err != nil {
			return nil, err
		}
		routes, err := parseIPRoutes(out)
		if err != nil {
			return nil, err
		}
		for _, r := range routes {
			if r.Protocol == "ra" {
				return nil, guarded("This server's IPv6 default route was learned from a router advertisement, which the kernel stops honouring once IPv6 forwarding is on; the route would expire and IPv6 connectivity with it. Set accept_ra to 2 on the uplink first.")
			}
		}
	}
	val := "0"
	if on {
		val = "1"
	}
	next.Sysctls[key] = val
	err = s.commit(ctx, next, step{
		apply: func(ctx context.Context) error {
			_, err := run(ctx, "sysctl", "-w", key+"="+val)
			return err
		},
		undo: func(ctx context.Context) { s.best(ctx, "sysctl", "-w", key+"="+prev) },
		verify: func(context.Context) error {
			got, err := readSysctl(key)
			if err != nil {
				return err
			}
			if got != val {
				return fmt.Errorf("the kernel still reports %s = %s", key, got)
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
