package netx

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
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
	DockerNetworks int `json:"dockerNetworks"`
	// DockerIPv6Networks counts bridges with IPv6 enabled or an IPv6 subnet.
	DockerIPv6Networks int `json:"dockerIPv6Networks"`
	// Unknown dependencies guard both families until Docker can be read again.
	DockerNetworksUnknown bool `json:"dockerNetworksUnknown,omitempty"`
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
	// DockerBasis says how the Docker dependency was counted for this
	// family, where Docker runs bridge networks.
	DockerBasis string `json:"dockerBasis,omitempty"`
	// Health is forwarding as measured rather than switched.
	Health ForwardingHealth `json:"health"`
}

// ForwardingHealth is what the kernel's counters and per-device switches say.
// Status is off, measuring (the first reading, with no rate yet),
// forwarding, idle, partial (some devices do not forward) or unknown.
// Datagrams forwarded show the switch is doing something; they cannot say
// whether a particular flow was meant to pass or reached its destination.
type ForwardingHealth struct {
	Status        string    `json:"status"`
	Forwarded     *uint64   `json:"forwarded,omitempty"`
	RatePerSecond *float64  `json:"ratePerSecond,omitempty"`
	WindowSeconds float64   `json:"windowSeconds,omitempty"`
	Disabled      []string  `json:"disabled"`
	CheckedAt     time.Time `json:"checkedAt"`
	Reason        string    `json:"reason,omitempty"`
}

// procNetRoot is where the kernel's protocol counters are read from; a
// variable so tests can stand a directory behind it.
var procNetRoot = "/proc/net"

// forwardingSamples keeps the previous counter reading per family, so the
// next read can turn the difference into a rate.
type forwardingSamples struct {
	mu   sync.Mutex
	last map[string]forwardingSample
}

type forwardingSample struct {
	at    time.Time
	value uint64
	rate  *float64
	span  float64
}

// forwardedDatagrams reads the kernel's count of datagrams it forwarded:
// ForwDatagrams in the Ip block of snmp, Ip6OutForwDatagrams in snmp6.
func forwardedDatagrams(canon string) (uint64, error) {
	if canon == "ipv6" {
		b, err := os.ReadFile(filepath.Join(procNetRoot, "snmp6"))
		if err != nil {
			return 0, err
		}
		for _, line := range strings.Split(string(b), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[0] == "Ip6OutForwDatagrams" {
				return strconv.ParseUint(fields[1], 10, 64)
			}
		}
		return 0, fmt.Errorf("snmp6 has no Ip6OutForwDatagrams")
	}
	b, err := os.ReadFile(filepath.Join(procNetRoot, "snmp"))
	if err != nil {
		return 0, err
	}
	var header []string
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "Ip:" {
			continue
		}
		if header == nil {
			header = fields
			continue
		}
		for i, name := range header {
			if name == "ForwDatagrams" && i < len(fields) {
				return strconv.ParseUint(fields[i], 10, 64)
			}
		}
	}
	return 0, fmt.Errorf("snmp has no Ip ForwDatagrams")
}

// devicesNotForwarding lists the IPv4 devices whose own forwarding switch
// is off while the family's is on; traffic arriving on them is not
// forwarded. IPv6 has no such switch: a device's forwarding setting chooses
// host or router behaviour (router advertisements), and only all/forwarding
// decides whether the kernel forwards.
func devicesNotForwarding() ([]string, error) {
	dir := filepath.Join(procSysRoot, "net", "ipv4", "conf")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, e := range entries {
		name := e.Name()
		if name == "all" || name == "default" || name == "lo" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, name, "forwarding"))
		if err == nil && strings.TrimSpace(string(b)) == "0" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}

// measure reads one family's health and remembers the counter for the next
// read. A window shorter than a second keeps the previous rate.
func (f *forwardingSamples) measure(canon string, enabled bool, now time.Time) ForwardingHealth {
	h := ForwardingHealth{Status: "unknown", Disabled: []string{}, CheckedAt: now}
	value, err := forwardedDatagrams(canon)
	if err != nil {
		h.Reason = "The kernel's forwarding counter could not be read: " + err.Error()
		return h
	}
	h.Forwarded = &value
	f.mu.Lock()
	if f.last == nil {
		f.last = map[string]forwardingSample{}
	}
	prev, had := f.last[canon]
	next := forwardingSample{at: now, value: value, rate: prev.rate, span: prev.span}
	if had && value >= prev.value {
		if span := now.Sub(prev.at).Seconds(); span >= 1 {
			rate := float64(value-prev.value) / span
			next.rate, next.span = &rate, span
		} else {
			next.at, next.value = prev.at, prev.value
		}
	} else if had {
		// A counter that went backwards was reset; start measuring again.
		next.rate, next.span = nil, 0
	}
	f.last[canon] = next
	f.mu.Unlock()
	h.RatePerSecond, h.WindowSeconds = next.rate, next.span
	if !enabled {
		h.Status, h.Reason = "off", "The family's forwarding switch is off; the counter shows what was forwarded before."
		return h
	}
	if canon == "ipv4" {
		disabled, err := devicesNotForwarding()
		if err != nil {
			h.Reason = "Per-device forwarding could not be read: " + err.Error()
			return h
		}
		h.Disabled = disabled
	}
	disabled := h.Disabled
	switch {
	case len(disabled) > 0:
		h.Status = "partial"
		h.Reason = "Traffic arriving on " + strings.Join(disabled, ", ") + " is not forwarded: " + plural(len(disabled), "its", "their") + " own forwarding switch is off."
	case next.rate == nil:
		h.Status, h.Reason = "measuring", "The first reading has no rate yet; the next one will."
	case *next.rate > 0:
		h.Status = "forwarding"
	default:
		h.Status, h.Reason = "idle", "No datagram was forwarded between the last two readings."
	}
	return h
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
	dockerNetworks := needs.DockerNetworks
	if canon == "ipv6" {
		dockerNetworks = needs.DockerIPv6Networks
	}
	if dockerNetworks > 0 {
		out = append(out, fmt.Sprintf("%d Docker %s", dockerNetworks, plural(dockerNetworks, "network", "networks")))
	}
	if needs.DockerNetworksUnknown {
		out = append(out, "Docker's network dependencies are unknown because its networks could not be read")
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
	return s.forwardingFrom(sp, needs)
}

// dockerBasis explains the Docker count beside a family's dependencies.
func dockerBasis(needs ForwardingNeeds, canon string) string {
	switch {
	case needs.DockerNetworksUnknown:
		return "Docker's networks could not be read, so both families are treated as needed until they can be."
	case needs.DockerNetworks == 0:
		return ""
	case canon == "ipv4":
		return fmt.Sprintf("Counts all %d Docker bridge %s: Docker's inventory does not say whether a network disabled IPv4 or relies on custom IPAM without a listed subnet, so each is assumed to forward IPv4.", needs.DockerNetworks, plural(needs.DockerNetworks, "network", "networks"))
	}
	return fmt.Sprintf("Counts the %d of %d Docker bridge networks with IPv6 enabled or an IPv6 subnet.", needs.DockerIPv6Networks, needs.DockerNetworks)
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
			if needs.DockerNetworksUnknown {
				return nil, guarded("%s forwarding cannot be turned off: Docker's networks could not be read, so forwarding dependencies are unknown.", canon)
			}
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
	view := s.forwardingFrom(next, needs)
	return &view, nil
}

// forwardingFrom is Forwarding over a spec already in hand, which SetForwarding
// holds the lock for.
func (s *Service) forwardingFrom(sp *Spec, needs ForwardingNeeds) ForwardingView {
	now := time.Now().UTC()
	state := func(key, canon string) ForwardingState {
		st := ForwardingState{NeededBy: forwardingNeeds(sp, needs, canon), DockerBasis: dockerBasis(needs, canon)}
		if v, err := readSysctl(key); err == nil {
			st.Available, st.Enabled = true, v == "1"
			st.Health = s.forwardingSample.measure(canon, st.Enabled, now)
		} else {
			st.Health = ForwardingHealth{Status: "unknown", Disabled: []string{}, CheckedAt: now, Reason: "This host has no " + canon + " forwarding setting."}
		}
		_, st.Persisted = sp.Sysctls[key]
		if len(st.NeededBy) > 0 {
			st.Guard = "Turned off, " + strings.Join(st.NeededBy, ", ") + " would stop working."
			if needs.DockerNetworksUnknown {
				st.Guard = "Docker's networks could not be read, so forwarding dependencies are unknown."
			}
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
