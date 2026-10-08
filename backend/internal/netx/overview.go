package netx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

// DefaultRoute is one way off this machine: the device a default route leaves
// through and the gateway it hands traffic to.
type DefaultRoute struct {
	Family  string `json:"family"`
	Device  string `json:"device"`
	Gateway string `json:"gateway,omitempty"`
	Metric  int    `json:"metric,omitempty"`
}

// DefaultRoutes reads the main table's default routes in both families, lowest
// metric first, which is the order the kernel prefers them in.
func DefaultRoutes(ctx context.Context) []DefaultRoute {
	var out []DefaultRoute
	for _, family := range []string{"inet", "inet6"} {
		args := []string{"-j", "route", "show", "default"}
		if family == "inet6" {
			args = []string{"-j", "-6", "route", "show", "default"}
		}
		raw, err := run(ctx, "ip", args...)
		if err != nil {
			continue
		}
		var routes []struct {
			Dev     string `json:"dev"`
			Gateway string `json:"gateway"`
			Metric  int    `json:"metric"`
		}
		if json.Unmarshal([]byte(raw), &routes) != nil {
			continue
		}
		for _, r := range routes {
			if r.Dev != "" {
				out = append(out, DefaultRoute{Family: family, Device: r.Dev, Gateway: r.Gateway, Metric: r.Metric})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Family != out[j].Family {
			return out[i].Family == "inet"
		}
		return out[i].Metric < out[j].Metric
	})
	return out
}

// ForwardingSwitches is whether this machine routes packets that are not its
// own, as two plain switches for the Overview; the Routing page reads the
// fuller ForwardingView with what needs each.
type ForwardingSwitches struct {
	IPv4 bool `json:"ipv4"`
	IPv6 bool `json:"ipv6"`
}

// CurrentForwarding reads the two forwarding switches.
func CurrentForwarding() ForwardingSwitches {
	v4, _ := readSysctl(sysctlForwardV4)
	v6, _ := readSysctl(sysctlForwardV6)
	return ForwardingSwitches{IPv4: v4 == "1", IPv6: v6 == "1"}
}

// Finding is one thing on the Overview's attention list: what was measured,
// how bad it is, and the page that fixes it.
type Finding struct {
	ID     string `json:"id"`
	Level  string `json:"level"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Href   string `json:"href"`
}

// OverviewInput is everything the attention list is judged from, gathered by
// the api layer so the judging stays a pure function and testable without a
// network.
type OverviewInput struct {
	Links       []Link
	Spec        *Spec
	Persistence Persistence
	Forwarding  ForwardingSwitches
	// LinkHistoryErrors is each device's errors and drops over the last hour,
	// from the recorded samples; a counter since boot cannot tell an old
	// incident from a current one.
	LinkHistoryErrors map[string]uint64
	FirewallAvailable bool
	FirewallEnabled   bool
	// ConntrackPercent is the connection-tracking table's fullness, negative
	// when it could not be read.
	ConntrackPercent float64
	// EncryptedDNS is false where every upstream is plain DNS; nil when the
	// resolver could not be read.
	EncryptedDNS *bool
}

// OverviewFindings judges the network, worst first.
func OverviewFindings(in OverviewInput) []Finding {
	var out []Finding
	add := func(f Finding) { out = append(out, f) }

	if in.Spec != nil && in.Persistence.Made > 0 && in.Persistence.Unit == "disabled" {
		add(Finding{
			ID: "persistence.unit", Level: "warning", Href: "/network",
			Title:  "What the dashboard made will not come back after a reboot",
			Detail: fmt.Sprintf("%s holds %d entries, but %s is disabled, so nothing restores them at boot.", in.Persistence.Dir, in.Persistence.Made, UnitName),
		})
	}
	if in.Persistence.Made > 0 && in.Persistence.Unit == "unsupported" {
		add(Finding{
			ID: "persistence.systemd", Level: "notice", Href: "/network",
			Title:  "Nothing restores the dashboard's network changes at boot",
			Detail: "This host has no systemd, so the boot files are written but no unit reads them.",
		})
	}
	if in.Spec != nil {
		needs4, needs6 := false, false
		for _, n := range in.Spec.NAT {
			p, err := netip.ParsePrefix(n.Source)
			if n.Enabled && err == nil {
				if p.Addr().Unmap().Is4() {
					needs4 = true
				} else {
					needs6 = true
				}
			}
		}
		for _, f := range in.Spec.Forwards {
			a, err := netip.ParseAddr(f.Target)
			if f.Enabled && err == nil {
				if a.Unmap().Is4() {
					needs4 = true
				} else {
					needs6 = true
				}
			}
		}
		for _, family := range []struct {
			name            string
			needed, enabled bool
		}{
			{"IPv4", needs4, in.Forwarding.IPv4}, {"IPv6", needs6, in.Forwarding.IPv6},
		} {
			if family.needed && !family.enabled {
				add(Finding{
					ID: "forwarding.off." + strings.ToLower(family.name), Level: "critical", Href: "/network/routing",
					Title:  family.name + " forwarding is off, so its port forwards and NAT carry nothing",
					Detail: "The gateway translates traffic for other machines, but the kernel is not routing this family's packets that are not its own.",
				})
			}
		}
	}
	if !in.FirewallAvailable {
		add(Finding{
			ID: "firewall.none", Level: "warning", Href: "/network/firewall",
			Title:  "No supported host firewall manager was detected",
			Detail: "Neither ufw nor firewalld is available. Custom kernel rules and provider firewalls can still filter traffic; their absence has not been established by this reading.",
		})
	} else if !in.FirewallEnabled {
		add(Finding{
			ID: "firewall.off", Level: "warning", Href: "/network/firewall",
			Title:  "The firewall is installed but not enforcing",
			Detail: "Its rules exist and are not applied.",
		})
	}
	switch {
	case in.ConntrackPercent >= 95:
		add(Finding{
			ID: "conntrack.full", Level: "critical", Href: "/network/protection",
			Title:  "The connection-tracking table is nearly full",
			Detail: fmt.Sprintf("%.0f%% of its entries are in use; once it is full the kernel drops new connections, the dashboard's included.", in.ConntrackPercent),
		})
	case in.ConntrackPercent >= 80:
		add(Finding{
			ID: "conntrack.high", Level: "warning", Href: "/network/protection",
			Title:  "The connection-tracking table is filling up",
			Detail: fmt.Sprintf("%.0f%% of its entries are in use. A flood of connections, or a maximum set too low for this machine.", in.ConntrackPercent),
		})
	}
	for _, l := range in.Links {
		if !l.Uplink {
			continue
		}
		if n := in.LinkHistoryErrors[l.Name]; n > 0 {
			add(Finding{
				ID: "link.errors." + l.Name, Level: "warning", Href: "/network/interfaces",
				Title:  fmt.Sprintf("%s dropped or failed %d packets in the last hour", l.Name, n),
				Detail: "Errors on the uplink are a cable, a driver or the provider; drops are a queue too short for the traffic.",
			})
		}
		if !l.Carrier && l.AdminUp {
			add(Finding{
				ID: "link.carrier." + l.Name, Level: "critical", Href: "/network/interfaces",
				Title:  fmt.Sprintf("%s has no carrier", l.Name),
				Detail: "The device is up but nothing is connected to it.",
			})
		}
	}
	if in.EncryptedDNS != nil && !*in.EncryptedDNS {
		add(Finding{
			ID: "dns.plain", Level: "notice", Href: "/network/dns",
			Title:  "Name lookups leave this server unencrypted",
			Detail: "Every upstream resolver is asked in plain DNS, which anyone on the path can read and rewrite. DNS over TLS is a setting away.",
		})
	}

	rank := map[string]int{"critical": 0, "warning": 1, "notice": 2}
	sort.SliceStable(out, func(i, j int) bool { return rank[out[i].Level] < rank[out[j].Level] })
	if out == nil {
		out = []Finding{}
	}
	return out
}

// CurrentConntrack is the connection-tracking table's fullness, for the
// Overview's attention list and the gateway's reading.
func CurrentConntrack() Conntrack { return readConntrack() }
