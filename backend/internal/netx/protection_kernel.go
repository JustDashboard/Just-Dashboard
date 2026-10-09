package netx

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Per-interface effective kernel values, and workload profiles.
//
// Five of the fifteen protections are per-interface in the kernel: the "all"
// value the page sets is combined with each device's own value, and how they
// combine differs per setting (include/linux/inetdevice.h). The page shows the
// value each device actually runs with, so a tunnel left at strict
// reverse-path filtering, or a bridge still accepting redirects, is visible
// rather than hidden behind "all".

// interfaceRule says how the kernel combines conf/all with conf/<device>.
type interfaceRule struct {
	Key string // the protection key, conf.all form
	// Leaf is the file under conf/<device>.
	Family, Leaf string
	// Combine is max, or, and, redirects (and when forwarding, or when
	// not), or min (IPv6 source routing).
	Combine string
	Explain string
}

var interfaceRules = []interfaceRule{
	{Key: "net.ipv4.conf.all.rp_filter", Family: "ipv4", Leaf: "rp_filter", Combine: "max",
		Explain: "The kernel uses the higher of all and the device's own value, so a device at strict (1) stays strict even when all is loose."},
	{Key: "net.ipv4.conf.all.accept_redirects", Family: "ipv4", Leaf: "accept_redirects", Combine: "redirects",
		Explain: "With forwarding on, a device accepts redirects only when both all and the device allow it; with forwarding off, when either does."},
	{Key: "net.ipv4.conf.all.send_redirects", Family: "ipv4", Leaf: "send_redirects", Combine: "or",
		Explain: "A device sends redirects when either all or the device allows it."},
	{Key: "net.ipv4.conf.all.accept_source_route", Family: "ipv4", Leaf: "accept_source_route", Combine: "and",
		Explain: "Source-routed packets are accepted only when both all and the device allow them."},
	{Key: "net.ipv4.conf.all.log_martians", Family: "ipv4", Leaf: "log_martians", Combine: "or",
		Explain: "Martians are logged when either all or the device asks for it."},
	{Key: "net.ipv6.conf.all.accept_redirects", Family: "ipv6", Leaf: "accept_redirects", Combine: "own",
		Explain: "IPv6 reads each device's own value; writing all copies it to every existing device, but a device can be changed back afterwards."},
	{Key: "net.ipv6.conf.all.accept_source_route", Family: "ipv6", Leaf: "accept_source_route", Combine: "min",
		Explain: "IPv6 accepts a routing header only up to the lower of all and the device's own value."},
}

// maxInterfaceRows bounds the per-interface reading on a host with hundreds
// of container veths.
const maxInterfaceRows = 96

// InterfaceValue is one setting on one device.
type InterfaceValue struct {
	Key       string `json:"key"`
	Own       string `json:"own"`
	All       string `json:"all"`
	Effective string `json:"effective"`
	// Differs is an effective value other than what all alone would give.
	Differs bool `json:"differs"`
}

// InterfaceSettings are one device's effective values.
type InterfaceSettings struct {
	Name       string           `json:"name"`
	Forwarding bool             `json:"forwarding"`
	Values     []InterfaceValue `json:"values"`
}

// InterfaceReading is the per-interface view and how each value combines.
type InterfaceReading struct {
	Interfaces []InterfaceSettings `json:"interfaces"`
	Rules      []InterfaceRuleView `json:"rules"`
	// Omitted counts devices left out past the bound (container veths last).
	Omitted int `json:"omitted"`
}

type InterfaceRuleView struct {
	Key     string `json:"key"`
	Combine string `json:"combine"`
	Explain string `json:"explain"`
}

func readConf(family, device, leaf string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(gatewaySysRoot, "net", family, "conf", device, leaf))
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(b)), true
}

func combineConf(rule interfaceRule, all, own string, forwarding bool) string {
	a, _ := strconv.Atoi(all)
	o, _ := strconv.Atoi(own)
	b := func(v bool) string {
		if v {
			return "1"
		}
		return "0"
	}
	switch rule.Combine {
	case "max":
		return strconv.Itoa(max(a, o))
	case "or":
		return b(a != 0 || o != 0)
	case "and":
		return b(a != 0 && o != 0)
	case "redirects":
		if forwarding {
			return b(a != 0 && o != 0)
		}
		return b(a != 0 || o != 0)
	case "min":
		return strconv.Itoa(min(a, o))
	}
	return own
}

// readInterfaceSettings reads every device's effective values for the
// per-interface protections.
func readInterfaceSettings() InterfaceReading {
	r := InterfaceReading{Interfaces: []InterfaceSettings{}}
	for _, rule := range interfaceRules {
		r.Rules = append(r.Rules, InterfaceRuleView{Key: rule.Key, Combine: rule.Combine, Explain: rule.Explain})
	}
	entries, err := os.ReadDir(filepath.Join(gatewaySysRoot, "net", "ipv4", "conf"))
	if err != nil {
		return r
	}
	var names []string
	for _, e := range entries {
		if n := e.Name(); n != "all" && n != "default" && ValidIfName(n) == nil {
			names = append(names, n)
		}
	}
	// Real devices first, container veths last: the bound drops the
	// least interesting.
	sort.Slice(names, func(i, j int) bool {
		vi, vj := strings.HasPrefix(names[i], "veth"), strings.HasPrefix(names[j], "veth")
		if vi != vj {
			return !vi
		}
		return names[i] < names[j]
	})
	if len(names) > maxInterfaceRows {
		r.Omitted = len(names) - maxInterfaceRows
		names = names[:maxInterfaceRows]
	}
	for _, name := range names {
		fwd, _ := readConf("ipv4", name, "forwarding")
		dev := InterfaceSettings{Name: name, Forwarding: fwd == "1", Values: []InterfaceValue{}}
		for _, rule := range interfaceRules {
			own, ok := readConf(rule.Family, name, rule.Leaf)
			if !ok {
				continue
			}
			all, _ := readConf(rule.Family, "all", rule.Leaf)
			v := InterfaceValue{Key: rule.Key, Own: own, All: all, Effective: combineConf(rule, all, own, dev.Forwarding)}
			v.Differs = rule.Combine != "own" && v.Effective != combineConf(rule, all, all, dev.Forwarding)
			if rule.Combine == "own" {
				v.Differs = own != all
			}
			dev.Values = append(dev.Values, v)
		}
		r.Interfaces = append(r.Interfaces, dev)
	}
	return r
}

// KernelProfile stages a set of values for a kind of host. Every value is
// one the closed list allows; the page applies them through the same
// confirmation and weakening gate as a hand-edited value.
type KernelProfile struct {
	ID     string            `json:"id"`
	Name   string            `json:"name"`
	Why    string            `json:"why"`
	Values map[string]string `json:"values"`
}

// kernelBaseline is what every profile shares: the recommendations that are
// right for any server on the internet.
func kernelBaseline() map[string]string {
	out := map[string]string{}
	for _, d := range protectionDefs {
		if d.Recommended != "" {
			out[d.Key] = d.Recommended
		}
	}
	return out
}

func kernelProfiles() []KernelProfile {
	with := func(extra map[string]string) map[string]string {
		v := kernelBaseline()
		for k, x := range extra {
			v[k] = x
		}
		return v
	}
	return []KernelProfile{
		{ID: "server", Name: "Internet-facing server",
			Why:    "Every recommendation as listed: SYN cookies on, loose reverse-path filtering, no redirects or source routing, quiet martian logging.",
			Values: with(nil)},
		{ID: "gateway", Name: "VPN or container gateway",
			Why:    "The recommendations, with a larger connection table and SYN backlog for the flows a gateway carries for others. Reverse-path filtering stays loose so tunnels and policy routing keep working.",
			Values: with(map[string]string{"net.netfilter.nf_conntrack_max": "524288", "net.ipv4.tcp_max_syn_backlog": "8192"})},
		{ID: "busy-web", Name: "Busy web server",
			Why:    "Room for bursts of new connections: a deep SYN backlog, a table sized for many short connections, and SYN-ACK retries kept low so half-open entries clear quickly.",
			Values: with(map[string]string{"net.netfilter.nf_conntrack_max": "1048576", "net.ipv4.tcp_max_syn_backlog": "16384", "net.ipv4.tcp_synack_retries": "2"})},
		{ID: "debug", Name: "Routing investigation",
			Why:    "The recommendations, with impossible source addresses logged. Use it while debugging a routing problem and return to the server profile afterwards: the log fills on a busy host.",
			Values: with(map[string]string{"net.ipv4.conf.all.log_martians": "1"})},
	}
}
