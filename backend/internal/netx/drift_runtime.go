package netx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// A successful command with malformed, null or empty output is not an empty
// inventory. Only a JSON array can establish that an owned object is missing.
func driftArray[T any](ctx context.Context, name string, args ...string) ([]T, error) {
	out, err := run(ctx, name, args...)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "[") {
		return nil, fmt.Errorf("%s returned no JSON array", name)
	}
	var entries []T
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

type driftNamespace struct {
	links               []ipLink
	addresses           []ipAddr
	linkErr, addressErr error
}

func (s *Service) driftRuntime(ctx context.Context, sp *Spec) []DriftObservation {
	out := []DriftObservation{}
	inventories := map[string]driftNamespace{}
	if len(sp.Links)+len(sp.Addresses)+len(sp.Shaping)+len(sp.Routes) > 0 {
		inventories[""] = readDriftNamespace(ctx, "")
	}
	if len(sp.Namespaces) > 0 {
		namespaces, err := driftArray[ipNetns](ctx, "ip", "-j", "netns", "list")
		if err == nil && slices.ContainsFunc(namespaces, func(n ipNetns) bool { return n.Name == "" }) {
			err = fmt.Errorf("namespace inventory contains an unnamed object")
		}
		for _, ns := range sp.Namespaces {
			o := observation("namespace", ns.Name)
			o.Coverage = "presence"
			if err != nil {
				o.Status, o.Reason = "unreadable", err.Error()
			} else if slices.ContainsFunc(namespaces, func(n ipNetns) bool { return n.Name == ns.Name }) {
				o.Status = "matching"
			} else {
				o.Status, o.Repairable, o.Reason = "missing", true, "The saved managed namespace is absent."
			}
			out = append(out, o)
		}
	}
	for _, l := range sp.Links {
		inv := inventories[""]
		out = append(out, driftLink(l, inv.links, inv.linkErr))
		for _, cidr := range l.Addresses {
			out = append(out, driftAddress(l.Name, cidr, "", inv))
		}
		if l.Kind != "veth" || l.Peer == "" {
			continue
		}
		if _, exists := inventories[l.PeerNamespace]; !exists {
			if err := ValidNamespace(l.PeerNamespace); err != nil {
				inventories[l.PeerNamespace] = driftNamespace{linkErr: err, addressErr: err}
			} else {
				inventories[l.PeerNamespace] = readDriftNamespace(ctx, l.PeerNamespace)
			}
		}
		peer := inventories[l.PeerNamespace]
		o := observation("link", l.PeerNamespace+"/"+l.Peer)
		o.Coverage = "presence-and-kind"
		if peer.linkErr != nil {
			o.Status, o.Reason = "unreadable", peer.linkErr.Error()
		} else {
			o.Status, o.Reason = "missing", "The managed veth peer is absent; review both ends before rebuilding the pair."
			for _, link := range peer.links {
				if link.IfName == l.Peer {
					o.Status = "matching"
					if link.LinkInfo.InfoKind != "veth" {
						o.Status, o.Owned, o.Reason = "conflict", false, "A device of another kind occupies the peer name."
					}
					break
				}
			}
		}
		out = append(out, o)
	}
	for _, a := range sp.Addresses {
		namespace := ""
		if peer, ok := peerOf(sp.Links, a.Link); ok {
			namespace = peer.PeerNamespace
		}
		out = append(out, driftAddress(a.Link, a.CIDR, namespace, inventories[namespace]))
	}
	for _, family := range []string{"inet", "inet6"} {
		flag := "-4"
		if family == "inet6" {
			flag = "-6"
		}
		if slices.ContainsFunc(sp.Routes, func(r RouteSpec) bool { return r.Family == family }) {
			routes, err := driftArray[json.RawMessage](ctx, "ip", "-j", flag, "route", "show", "table", "all")
			for _, route := range sp.Routes {
				if route.Family == family {
					out = append(out, driftRoute(route, routes, err))
				}
			}
		}
		if slices.ContainsFunc(sp.Rules, func(r RuleSpec) bool { return r.Family == family }) {
			rules, err := driftArray[json.RawMessage](ctx, "ip", "-j", flag, "rule", "show")
			for _, rule := range sp.Rules {
				if rule.Family == family {
					out = append(out, driftRule(rule, rules, err))
				}
			}
		}
	}
	keys := make([]string, 0, len(sp.Sysctls))
	for key := range sp.Sysctls {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		o := observation("sysctl", key)
		o.Expected = map[string]string{"value": sp.Sysctls[key]}
		actual, err := readSysctl(key)
		if err != nil {
			o.Status, o.Reason = "unreadable", err.Error()
		} else {
			o.Status, o.Observed = "matching", map[string]string{"value": actual}
			if actual != sp.Sysctls[key] {
				o.Status, o.Repairable, o.Reason = "drift", true, "The kernel value differs from the saved value."
			}
		}
		out = append(out, o)
	}
	for _, sh := range sp.Shaping {
		if sh.SQM != nil {
			out = append(out, driftSQM(ctx, sh))
			continue
		}
		o := observation("shaping", sh.Device)
		v := readShapeVerification(ctx, sh)
		o.Status, o.Reason = v.Status, v.Reason
		if o.Status == "verified" {
			o.Status = "matching"
		}
		if o.Status == "drift" {
			o.Repairable = true
		}
		out = append(out, o)
	}
	return driftRuntimeDependencies(out, sp, inventories[""])
}

func readDriftNamespace(ctx context.Context, namespace string) driftNamespace {
	args := []string{}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	links, linkErr := driftArray[ipLink](ctx, "ip", append(slices.Clone(args), "-j", "-d", "link", "show")...)
	addresses, addressErr := driftArray[ipAddr](ctx, "ip", append(slices.Clone(args), "-j", "addr", "show")...)
	if linkErr == nil && slices.ContainsFunc(links, func(l ipLink) bool { return l.IfName == "" || l.Flags == nil || l.MTU <= 0 }) {
		linkErr = fmt.Errorf("link inventory contains an incomplete object")
	}
	if addressErr == nil {
		for _, link := range addresses {
			if link.IfName == "" || link.AddrInfo == nil {
				addressErr = fmt.Errorf("address inventory contains an incomplete object")
				break
			}
			for _, a := range link.AddrInfo {
				local, err := netip.ParseAddr(a.Local)
				if err != nil || a.PrefixLen < 0 || a.PrefixLen > local.BitLen() {
					addressErr = fmt.Errorf("address inventory contains an invalid address")
					break
				}
			}
		}
	}
	return driftNamespace{links, addresses, linkErr, addressErr}
}

func driftLink(want LinkSpec, links []ipLink, err error) DriftObservation {
	o := observation("link", want.Name)
	o.Expected = map[string]string{"kind": want.Kind, "adminUp": strconv.FormatBool(want.Up)}
	if err != nil {
		o.Status, o.Reason = "unreadable", err.Error()
		return o
	}
	var actual *ipLink
	for i := range links {
		if links[i].IfName == want.Name {
			actual = &links[i]
			break
		}
	}
	if actual == nil {
		o.Status, o.Repairable, o.Reason = "missing", true, "The saved managed device is absent."
		return o
	}
	o.Observed = map[string]string{"kind": actual.LinkInfo.InfoKind, "adminUp": strconv.FormatBool(slices.Contains(actual.Flags, "UP")), "mtu": strconv.Itoa(actual.MTU), "master": actual.Master}
	if actual.LinkInfo.InfoKind != want.Kind {
		o.Status, o.Owned, o.Reason = "conflict", false, "A device of another kind occupies the saved name."
		return o
	}
	mismatch := []string{}
	if slices.Contains(actual.Flags, "UP") != want.Up {
		mismatch = append(mismatch, "administrative state")
	}
	if want.MTU > 0 && actual.MTU != want.MTU {
		mismatch = append(mismatch, "MTU")
	}
	if actual.Master != want.Master {
		mismatch = append(mismatch, "master")
	}
	unknown := actual.Flags == nil
	if want.Kind == "veth" {
		unknown = true
	}
	if want.Parent != "" {
		parent := actual.Link
		if parent == "" && actual.LinkIndex != 0 {
			for _, l := range links {
				if l.IfIndex == actual.LinkIndex {
					parent = l.IfName
				}
			}
		}
		if parent == "" {
			unknown = true
		} else if parent != want.Parent {
			mismatch = append(mismatch, "parent")
		}
	}
	var data map[string]json.RawMessage
	if len(actual.LinkInfo.InfoData) > 0 && json.Unmarshal(actual.LinkInfo.InfoData, &data) != nil {
		unknown = true
	}
	check := func(key string, expected any) {
		raw, exists := data[key]
		if !exists {
			unknown = true
			return
		}
		var value any
		if json.Unmarshal(raw, &value) != nil {
			unknown = true
			return
		}
		if fmt.Sprint(value) != fmt.Sprint(expected) {
			mismatch = append(mismatch, key)
		}
	}
	switch want.Kind {
	case "bridge":
		check("stp_state", map[bool]int{false: 0, true: 1}[want.STP])
	case "vlan":
		check("id", want.VLANID)
	case "macvlan":
		check("mode", want.Mode)
	case "vxlan":
		check("id", want.VNI)
		if want.Port > 0 {
			check("dstport", want.Port)
		}
		if want.Local != "" {
			check("local", want.Local)
		}
		if want.Remote != "" {
			check("group", want.Remote)
		}
		if want.Group != "" {
			check("group", want.Group)
		}
	case "gre", "gretap", "ip6gre", "ip6gretap":
		unknown = true
		if want.Local != "" {
			check("local", want.Local)
		}
		if want.Remote != "" {
			check("remote", want.Remote)
		}
	}
	if want.TTL != 0 {
		check("ttl", want.TTL)
	}
	if len(mismatch) > 0 {
		o.Status, o.Repairable, o.Reason = "drift", true, "Managed device differs in "+strings.Join(mismatch, ", ")+"."
	} else if unknown {
		o.Reason = "Some saved device attributes were not exposed by this kernel inventory."
	} else {
		o.Status = "matching"
	}
	return o
}

func driftAddress(device, cidr, namespace string, inv driftNamespace) DriftObservation {
	resource := device + "/" + cidr
	if namespace != "" {
		resource = namespace + "/" + resource
	}
	o := observation("address", resource)
	if inv.addressErr != nil {
		o.Status, o.Reason = "unreadable", inv.addressErr.Error()
		return o
	}
	want, err := netip.ParsePrefix(cidr)
	if err != nil {
		o.Status, o.Reason = "unreadable", "Saved address is invalid."
		return o
	}
	for _, link := range inv.addresses {
		if link.IfName == device {
			for _, a := range link.AddrInfo {
				if canonicalAddr(a.Local) == canonicalAddr(want.Addr().String()) && a.PrefixLen == want.Bits() {
					o.Status = "matching"
					return o
				}
			}
		}
	}
	// An address can be restored only after its target exists. Never create or
	// change a foreign parent as a side effect of this proposal.
	o.Status, o.Reason = "missing", "The saved managed address is absent."
	o.Repairable = slices.ContainsFunc(inv.links, func(l ipLink) bool { return l.IfName == device }) && inv.linkErr == nil
	if !o.Repairable {
		o.Reason += " Restore or inspect its target device first."
	}
	return o
}

func driftRoute(want RouteSpec, entries []json.RawMessage, err error) DriftObservation {
	o := observation("route", strconv.Itoa(want.ID))
	o.Expected = map[string]string{"family": want.Family, "destination": want.Destination, "table": strconv.Itoa(want.Table)}
	if err != nil {
		o.Status, o.Reason = "unreadable", err.Error()
		return o
	}
	byID, byName := rtTables()
	for _, raw := range entries {
		var r ipRoute
		if json.Unmarshal(raw, &r) != nil {
			o.Status, o.Reason = "unreadable", "Malformed route entry."
			return o
		}
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		if _, exists := fields["dst"]; !exists {
			o.Status, o.Reason = "unreadable", "Route entry does not expose its destination."
			return o
		}
		table, _ := tableOf(r.Table, byID, byName)
		e := routeEntry(r, want.Family, table, emptySpec())
		if canonicalDest(e.Destination) != canonicalDest(want.Destination) || table != want.Table || (e.Metric != want.Metric && !(want.Metric == 0 && want.Family == "inet6" && e.Metric == 1024)) {
			continue
		}
		o.Observed = map[string]string{"gateway": e.Gateway, "device": e.Device, "source": e.Source, "type": e.Type}
		// managedRoute compares a multipath route's legs; a single-path
		// route must not have gained any.
		if _, ok := managedRoute(&Spec{Routes: []RouteSpec{want}}, e, table); ok && canonicalAddr(e.Gateway) == canonicalAddr(want.Gateway) && canonicalAddr(e.Source) == canonicalAddr(want.Source) && (len(want.Nexthops) > 0 || len(e.Nexthops) == 0) {
			if unsupportedJSONFields(raw, []string{"type", "dst", "gateway", "dev", "table", "protocol", "scope", "metric", "prefsrc", "flags", "pref", "nexthops"}) {
				o.Reason = "The route has selectors or attributes this inspector cannot compare."
			} else {
				o.Status = "matching"
			}
		} else {
			o.Status, o.Owned, o.Reason = "conflict", false, "A different route occupies the saved table, destination and metric; no replacement is proposed."
		}
		return o
	}
	o.Status, o.Repairable, o.Reason = "missing", true, "The saved managed route is absent."
	return o
}

func driftRule(want RuleSpec, entries []json.RawMessage, err error) DriftObservation {
	o := observation("rule", strconv.Itoa(want.ID))
	o.Expected = map[string]string{"family": want.Family, "priority": strconv.Itoa(want.Priority)}
	if err != nil {
		o.Status, o.Reason = "unreadable", err.Error()
		return o
	}
	byID, byName := rtTables()
	count := 0
	for _, raw := range entries {
		var r ipRule
		if json.Unmarshal(raw, &r) != nil {
			o.Status, o.Reason = "unreadable", "Malformed policy rule entry."
			return o
		}
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		if _, exists := fields["priority"]; !exists {
			o.Status, o.Reason = "unreadable", "Policy rule entry does not expose its priority."
			return o
		}
		if r.Priority != want.Priority {
			continue
		}
		count++
		if count > 1 {
			o.Status, o.Repairable, o.Reason = "conflict", false, "Several policy rules occupy the saved priority."
			return o
		}
		e := ruleEntry(r, want.Family, byID, byName, &Spec{Rules: []RuleSpec{want}})
		if unsupportedJSONFields(raw, []string{"priority", "src", "srclen", "dst", "dstlen", "iif", "oif", "fwmark", "fwmask", "table", "action", "protocol", "uid_start", "uid_end", "tos", "goto", "l3mdev"}) {
			o.Status, o.Reason = "unknown", "The policy rule has selectors this inspector cannot compare."
		} else if e.Managed {
			o.Status = "matching"
		} else {
			o.Status, o.Owned, o.Reason = "conflict", false, "A different policy rule occupies the saved priority; no replacement is proposed."
		}
	}
	if count == 0 {
		o.Status, o.Repairable, o.Reason = "missing", true, "The saved managed policy rule is absent."
	}
	return o
}

func unsupportedJSONFields(raw json.RawMessage, allowed []string) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return true
	}
	for key := range fields {
		if !slices.Contains(allowed, key) {
			return true
		}
	}
	return false
}

func driftRuntimeDependencies(out []DriftObservation, sp *Spec, host driftNamespace) []DriftObservation {
	managed := map[string]bool{}
	for _, l := range sp.Links {
		managed[l.Name] = true
	}
	byID := map[string]DriftObservation{}
	for _, o := range out {
		byID[o.ID] = o
	}
	for i := range out {
		o := &out[i]
		if !o.Repairable {
			continue
		}
		dependencies := []string{}
		if o.Domain == "link" {
			for _, l := range sp.Links {
				if l.Name == o.Resource {
					if l.Parent != "" {
						dependencies = append(dependencies, l.Parent)
					}
					if l.Master != "" {
						dependencies = append(dependencies, l.Master)
					}
					if l.PeerNamespace != "" {
						o.Dependencies = append(o.Dependencies, "namespace:"+l.PeerNamespace)
					}
				}
			}
		}
		if o.Domain == "route" {
			for _, r := range sp.Routes {
				if strconv.Itoa(r.ID) == o.Resource && r.Device != "" {
					dependencies = append(dependencies, r.Device)
				}
			}
		}
		if o.Domain == "address" {
			for _, l := range sp.Links {
				if strings.HasPrefix(o.Resource, l.Name+"/") {
					dependencies = append(dependencies, l.Name)
				}
			}
			for _, a := range sp.Addresses {
				if strings.HasPrefix(o.Resource, a.Link+"/") {
					dependencies = append(dependencies, a.Link)
				}
			}
		}
		if o.Domain == "rule" {
			for _, r := range sp.Rules {
				if strconv.Itoa(r.ID) == o.Resource {
					if r.IIF != "" {
						dependencies = append(dependencies, r.IIF)
					}
					if r.OIF != "" {
						dependencies = append(dependencies, r.OIF)
					}
				}
			}
		}
		if o.Domain == "shaping" {
			dependencies = append(dependencies, o.Resource)
		}
		for _, device := range dependencies {
			if managed[device] {
				o.Dependencies = append(o.Dependencies, "link:"+device)
				dep := byID["link:"+device]
				if dep.Status != "matching" && !dep.Repairable {
					o.Repairable, o.Reason = false, o.Reason+" Managed dependency "+device+" is not safely repairable."
				}
			} else if host.linkErr != nil || !slices.ContainsFunc(host.links, func(l ipLink) bool { return l.IfName == device }) {
				o.Repairable, o.Reason = false, o.Reason+" Its native device dependency "+device+" is absent or unreadable; restore that through its owner."
			}
		}
		for _, dependency := range o.Dependencies {
			if strings.HasPrefix(dependency, "namespace:") {
				dep, known := byID[dependency]
				if !known || (dep.Status != "matching" && !dep.Repairable) {
					o.Repairable, o.Reason = false, o.Reason+" Namespace dependency is not safely repairable."
				}
			}
		}
	}
	return out
}
