package netx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// The kernel's reserved routing table ids.
const (
	tableLocal   = 255
	tableMain    = 254
	tableDefault = 253
	// tableTailscale is where tailscaled puts the tailnet's routes.
	tableTailscale = 52
)

// RoutingView is every routing table in both families, the policy rules that
// choose between them, and the path the browser's replies take.
type RoutingView struct {
	Tables []RoutingTable `json:"tables"`
	Rules  []RuleEntry    `json:"rules"`
	// ClientPath is how the kernel answers the address the request came from.
	ClientPath Path `json:"clientPath"`
	// HiddenLocal counts the entries of the local table (the host's own
	// addresses, broadcasts and multicast), which the kernel keeps and which
	// nobody edits.
	HiddenLocal int `json:"hiddenLocal"`
	// RulePriorities is the range rules made here are numbered in.
	RulePriorities PriorityRange `json:"rulePriorities"`
	// ClientDecision names the table and rule behind ClientPath: the table
	// as the kernel reports it, the rule as the model evaluates it.
	ClientDecision *ClientDecision `json:"clientDecision,omitempty"`
	// VRFs are the VRF devices and the tables they look up.
	VRFs []VRFDevice `json:"vrfs"`
	// local is the local table, which decisions consult and the page does
	// not draw.
	local []RouteEntry
}

// VRFDevice is an l3mdev master and its table.
type VRFDevice struct {
	Name  string `json:"name"`
	Table int    `json:"table"`
}

// PriorityRange is a closed range of rule priorities.
type PriorityRange struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

// RoutingTable is one table's routes.
type RoutingTable struct {
	ID     int          `json:"id"`
	Name   string       `json:"name"`
	Routes []RouteEntry `json:"routes"`
}

// RouteEntry is one route.
type RouteEntry struct {
	// ID is the dashboard's id for a route it made, and zero for any other.
	ID     int    `json:"id"`
	Family string `json:"family"`
	// Destination is "default" for a default route of either family.
	Destination string         `json:"destination"`
	Type        string         `json:"type"`
	Gateway     string         `json:"gateway,omitempty"`
	Device      string         `json:"device,omitempty"`
	Protocol    string         `json:"protocol"`
	Scope       string         `json:"scope,omitempty"`
	Metric      int            `json:"metric"`
	Source      string         `json:"source,omitempty"`
	Flags       []string       `json:"flags"`
	Nexthops    []RouteNexthop `json:"nexthops"`
	// Owner is kernel, dhcp, tailscale, docker, wireguard, just-dashboard or
	// system.
	Owner   string `json:"owner"`
	Managed bool   `json:"managed"`
	// Guard is why the route cannot be removed here, when it cannot.
	Guard string `json:"guard,omitempty"`
	// Comment is the note a managed route was saved with.
	Comment string `json:"comment,omitempty"`
}

// RouteNexthop is one leg of a multipath route.
type RouteNexthop struct {
	Gateway string `json:"gateway,omitempty"`
	Device  string `json:"device,omitempty"`
	Weight  int    `json:"weight,omitempty"`
}

// RuleEntry is one policy rule: which packets it selects, and what it does
// with them. Rules are read in priority order and the first that selects a
// packet and has an answer decides it.
type RuleEntry struct {
	ID       int    `json:"id"`
	Family   string `json:"family"`
	Priority int    `json:"priority"`
	From     string `json:"from,omitempty"`
	To       string `json:"to,omitempty"`
	IIF      string `json:"iif,omitempty"`
	OIF      string `json:"oif,omitempty"`
	FWMark   string `json:"fwmark,omitempty"`
	UIDRange string `json:"uidRange,omitempty"`
	TOS      string `json:"tos,omitempty"`
	IPProto  string `json:"ipProto,omitempty"`
	SPort    string `json:"sport,omitempty"`
	DPort    string `json:"dport,omitempty"`
	// Not inverts every selector of the rule.
	Not bool `json:"not,omitempty"`
	// L3MDev looks a packet up in the table of the VRF device it uses.
	L3MDev bool `json:"l3mdev,omitempty"`
	// SuppressPrefixLength discards a lookup's answer whose prefix is this
	// long or shorter, so the next rule decides instead.
	SuppressPrefixLength *int `json:"suppressPrefixLength,omitempty"`
	// Action is lookup, blackhole, unreachable, prohibit, goto or nop.
	Action    string `json:"action"`
	Table     int    `json:"table,omitempty"`
	TableName string `json:"tableName,omitempty"`
	Goto      int    `json:"goto,omitempty"`
	// Unresolved is a goto whose target priority holds no rule; the kernel
	// then passes over it.
	Unresolved bool `json:"unresolved,omitempty"`
	// Owner is system, tailscale or just-dashboard.
	Owner   string `json:"owner"`
	Managed bool   `json:"managed"`
	Guard   string `json:"guard,omitempty"`
}

// ipRoute is one entry of `ip -j route show`.
type ipRoute struct {
	Type     string   `json:"type"`
	Dst      string   `json:"dst"`
	Gateway  string   `json:"gateway"`
	Dev      string   `json:"dev"`
	Table    ipTable  `json:"table"`
	Protocol string   `json:"protocol"`
	Scope    string   `json:"scope"`
	Metric   int      `json:"metric"`
	PrefSrc  string   `json:"prefsrc"`
	Flags    []string `json:"flags"`
	Nexthops []struct {
		Gateway string `json:"gateway"`
		Dev     string `json:"dev"`
		Weight  int    `json:"weight"`
	} `json:"nexthops"`
}

// ipRule is one entry of `ip -j rule show`.
type ipRule struct {
	Priority          int     `json:"priority"`
	Src               string  `json:"src"`
	SrcLen            *int    `json:"srclen"`
	Dst               string  `json:"dst"`
	DstLen            *int    `json:"dstlen"`
	IIF               string  `json:"iif"`
	OIF               string  `json:"oif"`
	FWMark            string  `json:"fwmark"`
	FWMask            string  `json:"fwmask"`
	Table             ipTable `json:"table"`
	Action            string  `json:"action"`
	UIDStart          *uint32 `json:"uid_start"`
	UIDEnd            *uint32 `json:"uid_end"`
	TOS               string  `json:"tos"`
	IPProto           string  `json:"ipproto"`
	SPort             *int    `json:"sport"`
	SPortStart        *int    `json:"sport_start"`
	SPortEnd          *int    `json:"sport_end"`
	DPort             *int    `json:"dport"`
	DPortStart        *int    `json:"dport_start"`
	DPortEnd          *int    `json:"dport_end"`
	Goto              *int    `json:"goto"`
	SuppressPrefixLen *int    `json:"suppress_prefixlen"`
	Not               present `json:"not"`
	Unresolved        present `json:"unresolved"`
	L3MDev            present `json:"l3mdev"`
}

// present is a flag ip prints as a key with a null value ("not": null).
type present bool

func (p *present) UnmarshalJSON([]byte) error {
	*p = true
	return nil
}

// iproute2 versions may encode a table's number as a string or a JSON number,
// while named tables remain strings. Both identify the same kernel table.
type ipTable string

func (t *ipTable) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err == nil {
		*t = ipTable(name)
		return nil
	}
	var id uint32
	if err := json.Unmarshal(data, &id); err != nil {
		return fmt.Errorf("a routing table is a name or a 32-bit number: %w", err)
	}
	*t = ipTable(strconv.FormatUint(uint64(id), 10))
	return nil
}

// rtTableDirs are the directories iproute2 reads table names from, the
// administrator's first. A variable so tests can point them at fixtures.
var rtTableDirs = []string{"/etc/iproute2", "/usr/share/iproute2"}

// rtTables are the table names on this host. iproute2 reads rt_tables and the
// *.conf files in rt_tables.d, from /etc before /usr/share, and the first name
// given to an id is the one `ip` prints, so that is the one taken here.
func rtTables() (byID map[int]string, byName map[string]int) {
	byID, byName = map[int]string{}, map[string]int{}
	for _, dir := range rtTableDirs {
		files := []string{filepath.Join(dir, "rt_tables")}
		if conf, err := filepath.Glob(filepath.Join(dir, "rt_tables.d", "*.conf")); err == nil {
			sort.Strings(conf)
			files = append(files, conf...)
		}
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			for _, line := range strings.Split(string(b), "\n") {
				if i := strings.IndexByte(line, '#'); i >= 0 {
					line = line[:i]
				}
				fields := strings.Fields(line)
				if len(fields) < 2 {
					continue
				}
				id, err := strconv.ParseInt(fields[0], 0, 64)
				if err != nil || id < 0 || id > 4294967295 {
					continue
				}
				if _, taken := byID[int(id)]; !taken {
					byID[int(id)] = fields[1]
				}
				if _, taken := byName[fields[1]]; !taken {
					byName[fields[1]] = int(id)
				}
			}
		}
	}
	for id, name := range map[int]string{tableLocal: "local", tableMain: "main", tableDefault: "default"} {
		if _, ok := byID[id]; !ok {
			byID[id] = name
		}
		if _, ok := byName[name]; !ok {
			byName[name] = id
		}
	}
	return byID, byName
}

// tableOf turns the table field of `ip -j` — a number, or the name ip found in
// rt_tables, or nothing for main — into an id and a name.
func tableOf(field ipTable, byID map[int]string, byName map[string]int) (int, string) {
	if field == "" {
		return tableMain, byID[tableMain]
	}
	if n, err := strconv.Atoi(string(field)); err == nil {
		return n, byID[n]
	}
	if id, ok := byName[string(field)]; ok {
		return id, string(field)
	}
	return 0, string(field)
}

func parseIPRoutes(out string) ([]ipRoute, error) {
	out = strings.TrimSpace(out)
	if out == "" {
		return nil, nil
	}
	var routes []ipRoute
	if err := json.Unmarshal([]byte(out), &routes); err != nil {
		return nil, fmt.Errorf("ip route printed something unreadable: %w", err)
	}
	return routes, nil
}

func parseIPRules(out string) ([]ipRule, error) {
	out = strings.TrimSpace(out)
	if out == "" {
		return nil, nil
	}
	var rules []ipRule
	if err := json.Unmarshal([]byte(out), &rules); err != nil {
		return nil, fmt.Errorf("ip rule printed something unreadable: %w", err)
	}
	return rules, nil
}

// Routing reads every table and rule. client is the address the request came
// from, whose path the page names.
func (s *Service) Routing(ctx context.Context, client string) (*RoutingView, error) {
	view, err := s.readRouting(ctx)
	if err != nil {
		return nil, err
	}
	path, _ := clientPath(ctx, client)
	view.ClientPath = path
	vrfs, read := readVRFs(ctx)
	view.VRFs = vrfs
	view.ClientDecision = clientDecision(ctx, view, path, !read || len(vrfs) > 0)
	return view, nil
}

// readRouting is the inventory alone, which previews model changes against.
func (s *Service) readRouting(ctx context.Context) (*RoutingView, error) {
	v4, err := run(ctx, "ip", "-j", "route", "show", "table", "all")
	if err != nil {
		return nil, err
	}
	// A kernel without IPv6 has nothing to say here; the page is the IPv4
	// one, not an error.
	v6, err := run(ctx, "ip", "-j", "-6", "route", "show", "table", "all")
	if err != nil {
		v6 = ""
	}
	r4, err := run(ctx, "ip", "-j", "rule", "show")
	if err != nil {
		return nil, err
	}
	r6, err := run(ctx, "ip", "-j", "-6", "rule", "show")
	if err != nil {
		r6 = ""
	}
	sp, err := s.loadSpec()
	if err != nil {
		sp = emptySpec()
	}
	byID, byName := rtTables()
	routes4, err := parseIPRoutes(v4)
	if err != nil {
		return nil, err
	}
	routes6, err := parseIPRoutes(v6)
	if err != nil {
		return nil, err
	}
	rules4, err := parseIPRules(r4)
	if err != nil {
		return nil, err
	}
	rules6, err := parseIPRules(r6)
	if err != nil {
		return nil, err
	}
	return buildRouting(routing{
		routes: map[string][]ipRoute{"inet": routes4, "inet6": routes6},
		rules:  map[string][]ipRule{"inet": rules4, "inet6": rules6},
		spec:   sp, byID: byID, byName: byName,
	}), nil
}

// routing is everything buildRouting joins, gathered so it stays pure.
type routing struct {
	routes map[string][]ipRoute
	rules  map[string][]ipRule
	spec   *Spec
	byID   map[int]string
	byName map[string]int
}

func buildRouting(in routing) *RoutingView {
	view := &RoutingView{
		Tables: []RoutingTable{}, Rules: []RuleEntry{}, VRFs: []VRFDevice{},
		RulePriorities: PriorityRange{Min: rulePriorityMin, Max: rulePriorityMax},
	}
	byTable := map[int]*RoutingTable{}
	order := []int{}
	table := func(id int, name string) *RoutingTable {
		t, ok := byTable[id]
		if !ok {
			t = &RoutingTable{ID: id, Name: name, Routes: []RouteEntry{}}
			byTable[id] = t
			order = append(order, id)
		}
		return t
	}
	table(tableMain, in.byID[tableMain])

	for _, family := range []string{"inet", "inet6"} {
		for _, r := range in.routes[family] {
			id, name := tableOf(r.Table, in.byID, in.byName)
			e := routeEntry(r, family, id, in.spec)
			if id == tableLocal {
				view.HiddenLocal++
				view.local = append(view.local, e)
				continue
			}
			t := table(id, name)
			t.Routes = append(t.Routes, e)
		}
	}
	for _, family := range []string{"inet", "inet6"} {
		for _, r := range in.rules[family] {
			e := ruleEntry(r, family, in.byID, in.byName, in.spec)
			view.Rules = append(view.Rules, e)
			// A table a rule sends traffic to is shown even while empty, so
			// the rule does not point at nothing.
			if e.Action == "lookup" && e.Table != tableLocal && e.Table != tableDefault && e.Table != 0 {
				table(e.Table, e.TableName)
			}
		}
	}
	sort.SliceStable(view.Rules, func(i, j int) bool {
		if view.Rules[i].Priority != view.Rules[j].Priority {
			return view.Rules[i].Priority < view.Rules[j].Priority
		}
		return view.Rules[i].Family < view.Rules[j].Family
	})
	sort.SliceStable(order, func(i, j int) bool {
		a, b := order[i], order[j]
		if (a == tableMain) != (b == tableMain) {
			return a == tableMain
		}
		return a < b
	})
	for _, id := range order {
		t := byTable[id]
		sortRoutes(t.Routes)
		view.Tables = append(view.Tables, *t)
	}
	return view
}

func sortRoutes(routes []RouteEntry) {
	key := func(r RouteEntry) (fam int, def int, addr netip.Addr, bits int) {
		if r.Family == "inet6" {
			fam = 1
		}
		if r.Destination != "default" {
			def = 1
			if p, err := netip.ParsePrefix(r.Destination); err == nil {
				addr, bits = p.Addr(), p.Bits()
			}
		}
		return
	}
	sort.SliceStable(routes, func(i, j int) bool {
		fi, di, ai, bi := key(routes[i])
		fj, dj, aj, bj := key(routes[j])
		switch {
		case fi != fj:
			return fi < fj
		case di != dj:
			return di < dj
		case ai != aj:
			return ai.Compare(aj) < 0
		case bi != bj:
			return bi < bj
		}
		return routes[i].Metric < routes[j].Metric
	})
}

// routeEntry joins one kernel route with who made it.
func routeEntry(r ipRoute, family string, table int, sp *Spec) RouteEntry {
	typ := r.Type
	if typ == "" {
		typ = "unicast"
	}
	e := RouteEntry{
		Family: family, Destination: r.Dst, Type: typ, Gateway: r.Gateway, Device: r.Dev,
		Protocol: r.Protocol, Scope: r.Scope, Metric: r.Metric, Source: r.PrefSrc,
		Flags: r.Flags, Nexthops: []RouteNexthop{},
	}
	if e.Flags == nil {
		e.Flags = []string{}
	}
	if e.Destination == "" {
		e.Destination = "default"
	}
	for _, n := range r.Nexthops {
		e.Nexthops = append(e.Nexthops, RouteNexthop{Gateway: n.Gateway, Device: n.Dev, Weight: n.Weight})
	}
	if m, ok := managedRoute(sp, e, table); ok {
		e.ID, e.Managed, e.Comment = m.ID, true, m.Comment
	}
	e.Owner, e.Guard = routeOwner(e, table)
	return e
}

// managedRoute finds the spec entry a kernel route is, comparing what the
// kernel would have been told. A route made without a device or a metric
// reads back with the ones the kernel chose, so those fields only count where
// the spec gave them (and the IPv6 metric default, 1024, stands for none).
func managedRoute(sp *Spec, e RouteEntry, table int) (RouteSpec, bool) {
	dest := canonicalDest(e.Destination)
	for _, m := range sp.Routes {
		if m.Family != e.Family || canonicalDest(m.Destination) != dest || m.Type != e.Type || m.Table != table {
			continue
		}
		if m.Gateway != "" && canonicalAddr(m.Gateway) != canonicalAddr(e.Gateway) {
			continue
		}
		if m.Device != "" && m.Device != e.Device && e.Type == "unicast" {
			continue
		}
		if m.Metric != 0 && m.Metric != e.Metric {
			continue
		}
		if m.Metric == 0 && e.Metric != 0 && !(m.Family == "inet6" && e.Metric == 1024) {
			continue
		}
		if !sameNexthops(m.Nexthops, e.Nexthops) {
			continue
		}
		return m, true
	}
	return RouteSpec{}, false
}

// sameNexthops compares a managed route's legs with the kernel's, in any
// order: a leg without a device matches the device the kernel resolved, and
// an unwritten weight is the kernel's 1.
func sameNexthops(want []NexthopSpec, got []RouteNexthop) bool {
	if len(want) != len(got) {
		return false
	}
	used := make([]bool, len(got))
	for _, w := range want {
		weight := w.Weight
		if weight == 0 {
			weight = 1
		}
		found := false
		for i, g := range got {
			gw := g.Weight
			if gw == 0 {
				gw = 1
			}
			if used[i] || canonicalAddr(w.Gateway) != canonicalAddr(g.Gateway) || weight != gw || (w.Device != "" && w.Device != g.Device) {
				continue
			}
			used[i], found = true, true
			break
		}
		if !found {
			return false
		}
	}
	return true
}

func canonicalDest(s string) string {
	if s == "default" || s == "0.0.0.0/0" || s == "::/0" {
		return "default"
	}
	p, err := ParsePrefix(s)
	if err != nil {
		return s
	}
	return p.Masked().String()
}

func canonicalAddr(s string) string {
	if a, err := netip.ParseAddr(s); err == nil {
		return a.Unmap().String()
	}
	return s
}

// routeOwner says who a route belongs to and, when it is not the dashboard's,
// why it cannot be removed here.
func routeOwner(e RouteEntry, table int) (owner, guard string) {
	switch {
	case e.Managed:
		return "just-dashboard", ""
	case table == tableTailscale || strings.HasPrefix(e.Device, "tailscale"):
		return "tailscale", "Tailscale keeps its own table and the routes in it, one for each peer; they follow the tailnet."
	case e.Device == "docker0" || isDockerBridgeName(e.Device):
		return "docker", "Docker made this route with its network and takes it away with it."
	case strings.HasPrefix(e.Device, "wg"):
		return "wireguard", "wg-quick made this route with the tunnel; change it on the VPN page."
	case e.Protocol == "dhcp":
		return "dhcp", "Learned from DHCP: the provider hands this route out again at every renewal."
	case e.Protocol == "kernel":
		return "kernel", "The kernel made this route when an address was configured on the device."
	case e.Destination == "default":
		return "system", "The default route is how this server reaches the internet; it is changed in the distribution's network settings."
	}
	return "system", "Configured by this host's network settings (netplan, systemd-networkd or NetworkManager); change it there."
}

// ruleEntry joins one kernel rule with who made it.
func ruleEntry(r ipRule, family string, byID map[int]string, byName map[string]int, sp *Spec) RuleEntry {
	e := RuleEntry{
		Family: family, Priority: r.Priority, IIF: r.IIF, OIF: r.OIF,
		Action: r.Action, From: ruleSelector(r.Src, r.SrcLen), To: ruleSelector(r.Dst, r.DstLen),
	}
	if r.FWMark != "" {
		e.FWMark = r.FWMark
		if m, err := strconv.ParseUint(r.FWMask, 0, 32); err == nil && m != 0xffffffff {
			e.FWMark += "/" + r.FWMask
		}
	}
	if r.UIDStart != nil && r.UIDEnd != nil {
		e.UIDRange = fmt.Sprintf("%d-%d", *r.UIDStart, *r.UIDEnd)
	}
	e.TOS, e.IPProto, e.Not, e.L3MDev = r.TOS, r.IPProto, bool(r.Not), bool(r.L3MDev)
	e.SPort, e.DPort = portSelector(r.SPort, r.SPortStart, r.SPortEnd), portSelector(r.DPort, r.DPortStart, r.DPortEnd)
	e.SuppressPrefixLength = r.SuppressPrefixLen
	if r.Goto != nil {
		e.Action, e.Goto, e.Unresolved = "goto", *r.Goto, bool(r.Unresolved)
	}
	if e.Action == "" {
		e.Action = "lookup"
	}
	if r.Table != "" {
		e.Table, e.TableName = tableOf(r.Table, byID, byName)
	}
	if m, ok := managedRule(sp, e); ok {
		e.ID, e.Managed = m.ID, true
	}
	switch {
	case e.Managed:
		e.Owner = "just-dashboard"
	case e.Priority >= 5200 && e.Priority < 5300, strings.HasPrefix(e.FWMark, "0x80000"):
		e.Owner = "tailscale"
		e.Guard = "Tailscale's rules send its own traffic and the tailnet's to table 52; they come and go with tailscaled."
	case e.Priority == 0 || e.Priority == 32766 || e.Priority == 32767:
		e.Owner = "system"
		e.Guard = "The kernel's own rules: local addresses first, then the main table, then the default."
	default:
		e.Owner = "system"
		e.Guard = "Not made by Just Dashboard; whatever set it up owns it."
	}
	return e
}

// portSelector reads ip's single port or its start/end pair.
func portSelector(single, start, end *int) string {
	switch {
	case single != nil:
		return strconv.Itoa(*single)
	case start != nil && end != nil && *start == *end:
		return strconv.Itoa(*start)
	case start != nil && end != nil:
		return fmt.Sprintf("%d-%d", *start, *end)
	}
	return ""
}

// ruleSelector reads ip's split of a rule address and its prefix length. "all"
// is no selector.
func ruleSelector(addr string, bits *int) string {
	if addr == "" || addr == "all" {
		return ""
	}
	if bits == nil {
		return addr
	}
	return addr + "/" + strconv.Itoa(*bits)
}

func managedRule(sp *Spec, e RuleEntry) (RuleSpec, bool) {
	// A selector the dashboard never writes makes the kernel's rule something
	// else, however much of the rest agrees.
	if e.Not || e.IPProto != "" || e.SPort != "" || e.DPort != "" || e.SuppressPrefixLength != nil {
		return RuleSpec{}, false
	}
	for _, m := range sp.Rules {
		if m.Family != e.Family || m.Priority != e.Priority || m.Action != e.Action || m.Table != e.Table ||
			m.Goto != e.Goto || m.L3MDev != e.L3MDev {
			continue
		}
		if uid, err := canonicalUIDRange(m.UIDRange); err != nil || uid != e.UIDRange {
			continue
		}
		if !sameDSField(m.TOS, e.TOS) {
			continue
		}
		if canonicalSelector(m.From) != canonicalSelector(e.From) || canonicalSelector(m.To) != canonicalSelector(e.To) ||
			m.IIF != e.IIF || m.OIF != e.OIF {
			continue
		}
		if mark, err := canonicalFWMark(m.FWMark); err != nil || mark != canonicalFWMarkLoose(e.FWMark) {
			continue
		}
		return m, true
	}
	return RuleSpec{}, false
}

func canonicalSelector(s string) string {
	if s == "" {
		return ""
	}
	p, err := ParsePrefix(s)
	if err != nil {
		return s
	}
	return p.Masked().String()
}

func canonicalFWMarkLoose(s string) string {
	c, err := canonicalFWMark(s)
	if err != nil {
		return s
	}
	return c
}

// canonicalFWMark reads a firewall mark with an optional mask and writes it in
// the one form ip prints: hex, and no mask when it covers every bit.
func canonicalFWMark(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	val, mask, hasMask := strings.Cut(s, "/")
	v, err := strconv.ParseUint(val, 0, 32)
	if err != nil {
		return "", fmt.Errorf("%q is not a firewall mark", s)
	}
	out := fmt.Sprintf("0x%x", v)
	if hasMask {
		m, err := strconv.ParseUint(mask, 0, 32)
		if err != nil {
			return "", fmt.Errorf("%q is not a firewall mark mask", mask)
		}
		if m == 0 {
			return "", guarded("A firewall mark mask of zero matches every packet; select at least one mark bit.")
		}
		if m != 0xffffffff {
			out += fmt.Sprintf("/0x%x", m)
		}
	}
	return out, nil
}

// routeArgs is the arguments after `ip route add` for a managed route.
func routeArgs(r RouteSpec) ([]string, error) {
	var args []string
	switch r.Type {
	case "unicast":
	case "blackhole", "unreachable", "prohibit":
		args = append(args, r.Type)
	default:
		return nil, fmt.Errorf("%q is not a route type", r.Type)
	}
	dest := r.Destination
	switch {
	case dest == "default" && r.Family == "inet6":
		dest = "::/0"
	case dest == "default" && r.Family == "inet":
	default:
		p, err := ParsePrefix(dest)
		if err != nil {
			return nil, err
		}
		if (r.Family == "inet") != p.Addr().Is4() {
			return nil, fmt.Errorf("%s is not an %s network", dest, r.Family)
		}
		dest = p.Masked().String()
	}
	args = append(args, dest)
	if len(r.Nexthops) > 0 {
		return multipathArgs(r, args)
	}
	if r.Gateway != "" {
		gw, err := ParseAddr(r.Gateway)
		if err != nil {
			return nil, err
		}
		args = append(args, "via", gw.String())
	}
	if r.Device != "" {
		if err := ValidIfName(r.Device); err != nil {
			return nil, err
		}
		args = append(args, "dev", r.Device)
	}
	if r.Table != 0 && r.Table != tableMain {
		if r.Table < 1 || r.Table > 4294967294 {
			return nil, fmt.Errorf("a table is 1 to 4294967294")
		}
		args = append(args, "table", strconv.Itoa(r.Table))
	}
	if r.Metric != 0 {
		if r.Metric < 0 || r.Metric > 4294967295 {
			return nil, fmt.Errorf("a metric is 0 to 4294967295")
		}
		args = append(args, "metric", strconv.Itoa(r.Metric))
	}
	if r.Source != "" {
		src, err := ParseAddr(r.Source)
		if err != nil {
			return nil, err
		}
		args = append(args, "src", src.String())
	}
	return args, nil
}

// multipathArgs finishes a multipath route. ip reads every attribute of the
// route before its first nexthop, so table, metric and source come first.
func multipathArgs(r RouteSpec, args []string) ([]string, error) {
	if r.Type != "unicast" || r.Gateway != "" || r.Device != "" {
		return nil, fmt.Errorf("only a unicast route without a single gateway or device has nexthops")
	}
	if len(r.Nexthops) < 2 || len(r.Nexthops) > maxNexthops {
		return nil, fmt.Errorf("a multipath route has 2 to %d nexthops", maxNexthops)
	}
	if r.Table != 0 && r.Table != tableMain {
		if r.Table < 1 || r.Table > 4294967294 {
			return nil, fmt.Errorf("a table is 1 to 4294967294")
		}
		args = append(args, "table", strconv.Itoa(r.Table))
	}
	if r.Metric != 0 {
		if r.Metric < 0 || r.Metric > 4294967295 {
			return nil, fmt.Errorf("a metric is 0 to 4294967295")
		}
		args = append(args, "metric", strconv.Itoa(r.Metric))
	}
	if r.Source != "" {
		src, err := ParseAddr(r.Source)
		if err != nil {
			return nil, err
		}
		args = append(args, "src", src.String())
	}
	for _, n := range r.Nexthops {
		args = append(args, "nexthop")
		if n.Gateway != "" {
			gw, err := ParseAddr(n.Gateway)
			if err != nil {
				return nil, err
			}
			if (r.Family == "inet") != gw.Is4() {
				return nil, fmt.Errorf("the nexthop %s is not an %s address", gw, r.Family)
			}
			args = append(args, "via", gw.String())
		}
		if n.Device != "" {
			if err := ValidIfName(n.Device); err != nil {
				return nil, err
			}
			args = append(args, "dev", n.Device)
		}
		if n.Gateway == "" && n.Device == "" {
			return nil, fmt.Errorf("every nexthop needs a gateway, a device, or both")
		}
		if n.Weight != 0 {
			if n.Weight < 1 || n.Weight > 256 {
				return nil, fmt.Errorf("a nexthop weight is 1 to 256")
			}
			args = append(args, "weight", strconv.Itoa(n.Weight))
		}
	}
	return args, nil
}

// maxNexthops bounds a managed multipath route; the kernel takes more, but
// a form and a boot line stay readable.
const maxNexthops = 16

// familyArgs is the `-6` an ip route command needs where the batch line
// could infer the family from the address and a command line cannot.
func familyArgs(family string) []string {
	if family == "inet6" {
		return []string{"-6"}
	}
	return nil
}

// routeCommand is an `ip` invocation on a managed route.
func routeCommand(verb string, r RouteSpec) ([]string, error) {
	args, err := routeArgs(r)
	if err != nil {
		return nil, err
	}
	if verb == "del" {
		args = withoutSource(args)
	}
	out := append([]string{}, familyArgs(r.Family)...)
	out = append(out, "route", verb)
	return append(out, args...), nil
}

// withoutSource drops `src X`: it is a preference of an added route, not part
// of what identifies it, and a delete that names it only has one more way to
// miss.
func withoutSource(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == "src" && i+1 < len(args) {
			i++
			continue
		}
		out = append(out, args[i])
	}
	return out
}

// RouteRequest is a route to add.
type RouteRequest struct {
	// Destination is a network, an address, or "default".
	Destination string `json:"destination"`
	// Type is unicast (the default), blackhole, unreachable or prohibit.
	Type    string `json:"type,omitempty"`
	Gateway string `json:"gateway,omitempty"`
	Device  string `json:"device,omitempty"`
	// Table is main (254, and the default) or a number the dashboard may use.
	Table  int    `json:"table,omitempty"`
	Metric int    `json:"metric,omitempty"`
	Source string `json:"source,omitempty"`
	// Nexthops make an equal-cost multipath route; Gateway and Device are
	// then left empty.
	Nexthops []NexthopRequest `json:"nexthops,omitempty"`
	Comment  string           `json:"comment,omitempty"`
}

// NexthopRequest is one leg of a multipath route.
type NexthopRequest struct {
	Gateway string `json:"gateway,omitempty"`
	Device  string `json:"device,omitempty"`
	Weight  int    `json:"weight,omitempty"`
}

// spec validates a request and returns the entry it would record.
func (req RouteRequest) spec() (RouteSpec, error) {
	r := RouteSpec{Type: "unicast", Table: tableMain}
	switch t := strings.ToLower(strings.TrimSpace(req.Type)); t {
	case "", "unicast":
	case "blackhole", "unreachable", "prohibit":
		r.Type = t
	default:
		return RouteSpec{}, fmt.Errorf("a route type is unicast, blackhole, unreachable or prohibit")
	}
	var gw, src netip.Addr
	var err error
	if req.Gateway = strings.TrimSpace(req.Gateway); req.Gateway != "" {
		if gw, err = ParseAddr(req.Gateway); err != nil {
			return RouteSpec{}, err
		}
		if gw.IsUnspecified() || gw.IsMulticast() || gw.IsLoopback() {
			return RouteSpec{}, fmt.Errorf("%s cannot be a gateway", gw)
		}
		r.Gateway = gw.String()
	}
	if req.Source = strings.TrimSpace(req.Source); req.Source != "" {
		if src, err = ParseAddr(req.Source); err != nil {
			return RouteSpec{}, err
		}
		r.Source = src.String()
	}
	dest := strings.TrimSpace(req.Destination)
	switch strings.ToLower(dest) {
	case "":
		return RouteSpec{}, fmt.Errorf("a route needs a destination")
	case "default":
		r.Destination = "default"
		r.Family = "inet"
		if gw.IsValid() && gw.Is6() || src.IsValid() && src.Is6() {
			r.Family = "inet6"
		}
	default:
		p, err := ParsePrefix(dest)
		if err != nil {
			return RouteSpec{}, err
		}
		p = p.Masked()
		r.Family = familyOf(p.Addr())
		r.Destination = p.String()
		if p.Bits() == 0 {
			r.Destination = "default"
		}
	}
	if gw.IsValid() && (r.Family == "inet") != gw.Is4() {
		return RouteSpec{}, fmt.Errorf("the gateway %s is not an %s address", gw, r.Family)
	}
	if src.IsValid() && (r.Family == "inet") != src.Is4() {
		return RouteSpec{}, fmt.Errorf("the source %s is not an %s address", src, r.Family)
	}
	if dev := strings.TrimSpace(req.Device); dev != "" {
		if err := ValidIfName(dev); err != nil {
			return RouteSpec{}, err
		}
		r.Device = dev
	}
	if len(req.Nexthops) > 0 {
		if err := r.setNexthops(req.Nexthops, dest); err != nil {
			return RouteSpec{}, err
		}
	} else if r.Type == "unicast" {
		if r.Gateway == "" && r.Device == "" {
			return RouteSpec{}, fmt.Errorf("a route needs a gateway, a device, or both")
		}
	} else if r.Gateway != "" || r.Device != "" || r.Source != "" {
		return RouteSpec{}, fmt.Errorf("a %s route has no gateway, device or source", r.Type)
	}
	if req.Table != 0 {
		r.Table = req.Table
	}
	if err := checkTable(r.Table); err != nil {
		return RouteSpec{}, err
	}
	if req.Metric < 0 || req.Metric > 4294967295 {
		return RouteSpec{}, fmt.Errorf("a metric is 0 to 4294967295")
	}
	r.Metric = req.Metric
	if c := strings.TrimSpace(req.Comment); c != "" {
		if r.Comment, err = CleanLabel(c, 80); err != nil {
			return RouteSpec{}, err
		}
	}
	return r, nil
}

// setNexthops validates the legs of a multipath route. A default
// destination takes its family from the first leg's gateway, like a single
// route's gateway decides it.
func (r *RouteSpec) setNexthops(legs []NexthopRequest, dest string) error {
	if r.Type != "unicast" {
		return fmt.Errorf("a %s route has no nexthops", r.Type)
	}
	if r.Gateway != "" || r.Device != "" {
		return fmt.Errorf("a multipath route names its gateways and devices in its nexthops")
	}
	if len(legs) < 2 || len(legs) > maxNexthops {
		return fmt.Errorf("a multipath route has 2 to %d nexthops", maxNexthops)
	}
	seen := map[string]bool{}
	for i, leg := range legs {
		n := NexthopSpec{Weight: leg.Weight}
		if gw := strings.TrimSpace(leg.Gateway); gw != "" {
			addr, err := ParseAddr(gw)
			if err != nil {
				return err
			}
			if addr.IsUnspecified() || addr.IsMulticast() || addr.IsLoopback() {
				return fmt.Errorf("%s cannot be a gateway", addr)
			}
			if i == 0 && strings.EqualFold(strings.TrimSpace(dest), "default") && r.Source == "" {
				r.Family = familyOf(addr)
			}
			if familyOf(addr) != r.Family {
				return fmt.Errorf("the nexthop %s is not an %s address", addr, r.Family)
			}
			n.Gateway = addr.String()
		}
		if dev := strings.TrimSpace(leg.Device); dev != "" {
			if err := ValidIfName(dev); err != nil {
				return err
			}
			n.Device = dev
		}
		if n.Gateway == "" && n.Device == "" {
			return fmt.Errorf("every nexthop needs a gateway, a device, or both")
		}
		if n.Weight < 0 || n.Weight > 256 {
			return fmt.Errorf("a nexthop weight is 1 to 256")
		}
		if n.Weight == 1 {
			n.Weight = 0
		}
		key := n.Gateway + "|" + n.Device
		if seen[key] {
			return fmt.Errorf("the nexthop %s appears twice", strings.Trim(n.Gateway+" "+n.Device, " "))
		}
		seen[key] = true
		r.Nexthops = append(r.Nexthops, n)
	}
	return nil
}

// checkTable refuses the tables that are not the dashboard's to write in.
func checkTable(id int) error {
	switch {
	case id == tableLocal:
		return guarded("Table 255 is the kernel's local table, which holds this server's own addresses; it is not edited.")
	case id == tableTailscale:
		return guarded("Table 52 is Tailscale's; routes put there are removed the next time tailscaled changes its own.")
	case id == tableDefault:
		return guarded("Table 253 is the kernel's default table, consulted last; use main or a table of your own.")
	case egressReservedTable(id):
		return guarded("Tables %d–%d belong to egress groups, whose decisions rewrite them; use a table of your own.", egressTableBase, egressTableBase+egressSlots*egressTableSpan-1)
	case id < 1 || id > 4294967294:
		return fmt.Errorf("a table is main or a number from 1 to 4294967294")
	}
	return nil
}

// hasDefaultRoute reports whether the main table already has a default route
// of a family, and returns its description.
func hasDefaultRoute(ctx context.Context, family string) (string, bool, error) {
	args := []string{"-j"}
	args = append(args, familyArgs(family)...)
	out, err := run(ctx, "ip", append(args, "route", "show", "default")...)
	if err != nil {
		return "", false, err
	}
	routes, err := parseIPRoutes(out)
	if err != nil || len(routes) == 0 {
		return "", false, err
	}
	r := routes[0]
	desc := "a default route"
	if r.Gateway != "" {
		desc = "a default route via " + r.Gateway
	}
	if r.Dev != "" {
		desc += " on " + r.Dev
	}
	return desc, true, nil
}

// verifyRouting is verifyPath plus the question verifyPath cannot ask: where
// do the browser's replies go from the address they are sent from? A policy
// rule selecting on the server's own address changes nothing for `ip route
// get <client>`, which names no source, and everything for the packets that
// carry one.
func verifyRouting(before Path) func(ctx context.Context) error {
	plain := verifyPath(before)
	return func(ctx context.Context) error {
		if err := plain(ctx); err != nil {
			return err
		}
		if before.Address == "" || before.Local || before.Source == "" {
			return nil
		}
		args := []string{"-j"}
		if a, err := netip.ParseAddr(before.Address); err == nil && a.Is6() {
			args = append(args, "-6")
		}
		out, err := run(ctx, "ip", append(args, "route", "get", before.Address, "from", before.Source)...)
		if err != nil {
			return guarded("with this change the kernel has no route for the replies to your connection to the dashboard (%s) from %s, so it was put back",
				before.Address, before.Source)
		}
		after, err := parseRouteGet(out, Path{Address: before.Address})
		if err != nil {
			return err
		}
		if !samePath(before, after) {
			return guarded("this would send the replies to your connection to the dashboard (%s) %s instead of %s when they come from %s, so it was put back",
				before.Address, describePath(after), describePath(before), before.Source)
		}
		return nil
	}
}

// routeDevices are the devices a route names, its own or its legs'.
func routeDevices(r RouteSpec) []string {
	var out []string
	if r.Device != "" {
		out = append(out, r.Device)
	}
	for _, n := range r.Nexthops {
		if n.Device != "" && !slices.Contains(out, n.Device) {
			out = append(out, n.Device)
		}
	}
	return out
}

// AddRoute adds a route, checks the browser's path survived it, and records it.
func (s *Service) AddRoute(ctx context.Context, req RouteRequest, client, actor string) (*RouteSpec, error) {
	r, err := req.spec()
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
	st, err := s.readLinkState(ctx, next, client)
	if err != nil {
		return nil, err
	}
	for _, dev := range routeDevices(r) {
		if _, err := st.need(dev); err != nil {
			return nil, err
		}
	}
	if r.Table == tableMain && r.Destination == "default" && r.Type == "unicast" {
		desc, exists, err := hasDefaultRoute(ctx, r.Family)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, guarded("This server already has %s. A second default route in the main table would replace or compete with its way to the internet; put it in a table of its own and select the traffic with a policy rule.", desc)
		}
	}
	add, err := routeCommand("add", r)
	if err != nil {
		return nil, err
	}
	del, err := routeCommand("del", r)
	if err != nil {
		return nil, err
	}
	for _, have := range next.Routes {
		if h, err := routeCommand("add", have); err == nil && strings.Join(h, " ") == strings.Join(add, " ") {
			return nil, fmt.Errorf("this route: %w", ErrExists)
		}
	}
	r.ID = next.takeID()
	r.Made = stamp(actor)
	next.Routes = append(next.Routes, r)
	flows := flowAnchors(ctx)
	err = s.commit(ctx, next, step{
		apply:    func(ctx context.Context) error { _, err := run(ctx, "ip", add...); return err },
		undo:     func(ctx context.Context) { s.best(ctx, "ip", del...) },
		verify:   verifyRouting(st.path),
		evidence: routingEvidence(st.path, flows),
	})
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// DeleteRoute removes a route the dashboard added.
func (s *Service) DeleteRoute(ctx context.Context, id int, client, actor string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sp, err := s.loadSpec()
	if err != nil {
		return err
	}
	next := sp.clone()
	var gone RouteSpec
	var kept []RouteSpec
	found := false
	for _, r := range next.Routes {
		if r.ID == id {
			gone, found = r, true
			continue
		}
		kept = append(kept, r)
	}
	if !found {
		return fmt.Errorf("route %d: %w", id, ErrNotFound)
	}
	next.Routes = kept
	path, err := clientPath(ctx, client)
	if err != nil {
		return err
	}
	del, err := routeCommand("del", gone)
	if err != nil {
		return err
	}
	add, err := routeCommand("add", gone)
	if err != nil {
		return err
	}
	flows := flowAnchors(ctx)
	return s.commit(ctx, next, step{
		apply: func(ctx context.Context) error {
			if _, err := run(ctx, "ip", del...); err != nil && !isGone(err) {
				return err
			}
			return nil
		},
		undo:     func(ctx context.Context) { s.best(ctx, "ip", add...) },
		verify:   verifyRouting(path),
		evidence: routingEvidence(path, flows),
	})
}

// RuleRequest is a policy rule to add.
type RuleRequest struct {
	// Family is inet or inet6; omitted, addresses select it and interface or
	// mark-only rules default to IPv4.
	Family string `json:"family,omitempty"`
	// Priority is chosen where zero; given, it is within the dashboard's
	// range.
	Priority int    `json:"priority,omitempty"`
	From     string `json:"from,omitempty"`
	To       string `json:"to,omitempty"`
	IIF      string `json:"iif,omitempty"`
	OIF      string `json:"oif,omitempty"`
	FWMark   string `json:"fwmark,omitempty"`
	// UIDRange selects locally generated traffic by its socket's owner: a
	// UID or a range such as 1000-1999.
	UIDRange string `json:"uidRange,omitempty"`
	// TOS selects a DS field value, such as 0x10 or EF.
	TOS string `json:"tos,omitempty"`
	// L3MDev looks the traffic up in its VRF device's table; it names no
	// table of its own.
	L3MDev bool `json:"l3mdev,omitempty"`
	// Action is lookup (the default), goto, blackhole, unreachable or
	// prohibit.
	Action string `json:"action,omitempty"`
	Table  int    `json:"table,omitempty"`
	// Goto is the priority a goto rule continues at: a later rule made here.
	Goto    int    `json:"goto,omitempty"`
	Comment string `json:"comment,omitempty"`
}

// spec validates a request and returns the entry it would record, without a
// priority.
func (req RuleRequest) spec() (RuleSpec, error) {
	r := RuleSpec{Family: "inet", Action: "lookup"}
	explicitFamily := strings.ToLower(strings.TrimSpace(req.Family))
	switch explicitFamily {
	case "":
	case "inet", "ipv4", "4":
		r.Family = "inet"
	case "inet6", "ipv6", "6":
		r.Family = "inet6"
	default:
		return RuleSpec{}, fmt.Errorf("a rule's family is inet or inet6")
	}
	switch a := strings.ToLower(strings.TrimSpace(req.Action)); a {
	case "", "lookup":
	case "goto", "blackhole", "unreachable", "prohibit":
		r.Action = a
	default:
		return RuleSpec{}, fmt.Errorf("a rule's action is lookup, goto, blackhole, unreachable or prohibit")
	}
	var families []netip.Addr
	selector := func(raw string) (string, error) {
		raw = strings.TrimSpace(raw)
		if raw == "" || strings.EqualFold(raw, "all") {
			return "", nil
		}
		p, err := ParsePrefix(raw)
		if err != nil {
			return "", err
		}
		p = p.Masked()
		families = append(families, p.Addr())
		if p.Bits() == 0 {
			return "", nil
		}
		return p.String(), nil
	}
	var err error
	if r.From, err = selector(req.From); err != nil {
		return RuleSpec{}, err
	}
	if r.To, err = selector(req.To); err != nil {
		return RuleSpec{}, err
	}
	for i, a := range families {
		family := familyOf(a)
		if i == 0 && explicitFamily == "" {
			r.Family = family
		}
		if family != r.Family {
			return RuleSpec{}, fmt.Errorf("a rule's source and destination must match its %s family", r.Family)
		}
	}
	for _, f := range []struct {
		raw string
		dst *string
	}{{req.IIF, &r.IIF}, {req.OIF, &r.OIF}} {
		if name := strings.TrimSpace(f.raw); name != "" {
			if err := ValidIfName(name); err != nil {
				return RuleSpec{}, err
			}
			*f.dst = name
		}
	}
	if r.FWMark, err = canonicalFWMark(req.FWMark); err != nil {
		return RuleSpec{}, err
	}
	if r.UIDRange, err = canonicalUIDRange(req.UIDRange); err != nil {
		return RuleSpec{}, err
	}
	if tos := strings.TrimSpace(req.TOS); tos != "" {
		if r.TOS, err = canonicalTOS(tos, r.Family); err != nil {
			return RuleSpec{}, err
		}
	}
	r.L3MDev = req.L3MDev
	// A VRF lookup selects only traffic using a VRF device, which is a
	// selection of its own; the other additions narrow like any selector.
	if r.From == "" && r.To == "" && r.IIF == "" && r.OIF == "" && r.FWMark == "" && r.UIDRange == "" && r.TOS == "" && !r.L3MDev {
		return RuleSpec{}, guarded("A rule with no selector matches every packet, so sending it to another table reroutes the whole server, this connection included. Select by source, destination, interface, mark, UID, TOS or VRF.")
	}
	switch {
	case r.L3MDev:
		if r.Action != "lookup" {
			return RuleSpec{}, fmt.Errorf("a VRF rule looks traffic up in its device's table; it has no other action")
		}
		if req.Table != 0 {
			return RuleSpec{}, fmt.Errorf("a VRF rule takes its table from the VRF device and names none")
		}
	case r.Action == "lookup":
		if req.Table == 0 {
			return RuleSpec{}, fmt.Errorf("a lookup rule needs a table")
		}
		if err := checkRuleTable(req.Table); err != nil {
			return RuleSpec{}, err
		}
		r.Table = req.Table
	case req.Table != 0:
		return RuleSpec{}, fmt.Errorf("a %s rule names no table", r.Action)
	}
	if r.Action == "goto" {
		if req.Goto < rulePriorityMin || req.Goto > rulePriorityMax {
			return RuleSpec{}, guarded("A goto made here continues at a priority from %d to %d, so it can never jump past the kernel's, Tailscale's or the distribution's rules.", rulePriorityMin, rulePriorityMax)
		}
		r.Goto = req.Goto
	} else if req.Goto != 0 {
		return RuleSpec{}, fmt.Errorf("only a goto rule names a priority to continue at")
	}
	if req.Priority != 0 && (req.Priority < rulePriorityMin || req.Priority > rulePriorityMax) {
		return RuleSpec{}, fmt.Errorf("a rule made here has a priority from %d to %d; the kernel's, Tailscale's and the distribution's rules are outside it", rulePriorityMin, rulePriorityMax)
	}
	if egressReservedPriority(req.Priority) {
		return RuleSpec{}, guarded("Priorities %d–%d belong to egress groups; choose another.", egressPriorityBase, egressPriorityBase+egressSlots*egressPrioritySpan-1)
	}
	r.Priority = req.Priority
	if c := strings.TrimSpace(req.Comment); c != "" {
		if r.Comment, err = CleanLabel(c, 80); err != nil {
			return RuleSpec{}, err
		}
	}
	return r, nil
}

// checkRuleTable refuses the tables a rule may not send traffic to. A rule
// may look in main or default, which a route may not be added to by id.
func checkRuleTable(id int) error {
	switch {
	case id == tableLocal:
		return guarded("Table 255 is the kernel's local table; the first rule already consults it.")
	case id == tableTailscale:
		return guarded("Table 52 is Tailscale's; a rule sending traffic there competes with the ones tailscaled keeps.")
	case egressReservedTable(id):
		return guarded("Tables %d–%d belong to egress groups; select a group's traffic with its own policy.", egressTableBase, egressTableBase+egressSlots*egressTableSpan-1)
	case id < 1 || id > 4294967294:
		return fmt.Errorf("a table is 1 to 4294967294")
	}
	return nil
}

// ruleArgs is the arguments after `ip rule add` for a managed rule.
func ruleArgs(r RuleSpec) ([]string, error) {
	if r.Family != "" && r.Family != "inet" && r.Family != "inet6" {
		return nil, fmt.Errorf("a rule's family is inet or inet6")
	}
	if r.Priority < rulePriorityMin || r.Priority > rulePriorityMax {
		return nil, fmt.Errorf("priority %d is outside the dashboard's range", r.Priority)
	}
	args := []string{"priority", strconv.Itoa(r.Priority)}
	for _, f := range []struct{ key, val string }{{"from", r.From}, {"to", r.To}} {
		if f.val == "" {
			continue
		}
		p, err := ParsePrefix(f.val)
		if err != nil {
			return nil, err
		}
		if p.Addr().Is6() != (r.Family == "inet6") {
			return nil, fmt.Errorf("a rule's selector must match its family")
		}
		args = append(args, f.key, p.Masked().String())
	}
	for _, f := range []struct{ key, val string }{{"iif", r.IIF}, {"oif", r.OIF}} {
		if f.val == "" {
			continue
		}
		if err := ValidIfName(f.val); err != nil {
			return nil, err
		}
		args = append(args, f.key, f.val)
	}
	if r.FWMark != "" {
		mark, err := canonicalFWMark(r.FWMark)
		if err != nil {
			return nil, err
		}
		args = append(args, "fwmark", mark)
	}
	if r.UIDRange != "" {
		uid, err := canonicalUIDRange(r.UIDRange)
		if err != nil {
			return nil, err
		}
		args = append(args, "uidrange", uid)
	}
	if r.TOS != "" {
		tos, err := canonicalTOS(r.TOS, r.Family)
		if err != nil {
			return nil, err
		}
		args = append(args, "tos", tos)
	}
	if r.L3MDev {
		if r.Action != "lookup" || r.Table != 0 {
			return nil, fmt.Errorf("a VRF rule looks up its device's table and names none")
		}
		return append(args, "l3mdev"), nil
	}
	switch r.Action {
	case "lookup":
		if r.Table < 1 || r.Table > 4294967294 {
			return nil, fmt.Errorf("a table is 1 to 4294967294")
		}
		args = append(args, "lookup", strconv.Itoa(r.Table))
	case "goto":
		// The kernel refuses a backward goto; the dashboard's range keeps
		// every jump among its own rules.
		if r.Goto <= r.Priority || r.Goto > rulePriorityMax {
			return nil, fmt.Errorf("a goto continues at a later priority within the dashboard's range")
		}
		args = append(args, "goto", strconv.Itoa(r.Goto))
	case "blackhole", "unreachable", "prohibit":
		args = append(args, r.Action)
	default:
		return nil, fmt.Errorf("%q is not a rule action", r.Action)
	}
	return args, nil
}

// canonicalUIDRange reads a UID or a range and writes ip's start-end form.
func canonicalUIDRange(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	lo, hi, ranged := strings.Cut(s, "-")
	start, err := strconv.ParseUint(strings.TrimSpace(lo), 10, 32)
	end := start
	if err == nil && ranged {
		end, err = strconv.ParseUint(strings.TrimSpace(hi), 10, 32)
	}
	if err != nil {
		return "", fmt.Errorf("%q is not a UID or a UID range such as 1000-1999", s)
	}
	// 4294967295 is the kernel's invalid UID and cannot own a socket.
	if start > end || end > 4294967294 {
		return "", fmt.Errorf("a UID range runs upward from 0 to at most 4294967294")
	}
	if start == 0 && end == 4294967294 {
		return "", fmt.Errorf("a UID range of every UID selects nothing in particular; leave it empty")
	}
	return fmt.Sprintf("%d-%d", start, end), nil
}

// canonicalTOS reads a DS field value, by number or by its rt_dsfield name,
// and writes it in hex. The kernel refuses either ECN bit, and an IPv4
// rule compares only the TOS bits 0x1c.
func canonicalTOS(s, family string) (string, error) {
	v, ok := dsfieldValue(s)
	if !ok {
		return "", fmt.Errorf("%q is not a TOS value such as 0x10 or EF", s)
	}
	if v&0x03 != 0 {
		return "", fmt.Errorf("a TOS selector's two ECN bits must be 0")
	}
	if v == 0 {
		return "", fmt.Errorf("TOS 0 is the kernel's \"any TOS\" and selects nothing in particular; leave it empty")
	}
	if family != "inet6" && v&^0x1c != 0 {
		return "", fmt.Errorf("an IPv4 rule selects TOS 0x04 to 0x1c in steps of 4; a DSCP class such as EF is an IPv6 selector")
	}
	return fmt.Sprintf("0x%02x", v), nil
}

// standardDSField is iproute2's shipped rt_dsfield: the RFC 2474/2597/2598
// names ip prints. The backend's own image may lack the file, and the
// host's /usr/share is not mounted, so the standard names are known here.
var standardDSField = map[string]uint8{
	"default": 0x00, "af11": 0x28, "af12": 0x30, "af13": 0x38, "af21": 0x48, "af22": 0x50, "af23": 0x58,
	"af31": 0x68, "af32": 0x70, "af33": 0x78, "af41": 0x88, "af42": 0x90, "af43": 0x98,
	"cs1": 0x20, "cs2": 0x40, "cs3": 0x60, "cs4": 0x80, "cs5": 0xa0, "cs6": 0xc0, "cs7": 0xe0, "ef": 0xb8,
}

// dsfieldValue reads a number or a name from the rt_dsfield files ip prints
// names from, so a rule read back as "EF" compares with one written 0xb8.
func dsfieldValue(s string) (uint8, bool) {
	s = strings.TrimSpace(s)
	if v, err := strconv.ParseUint(s, 0, 8); err == nil {
		return uint8(v), true
	}
	for _, dir := range rtTableDirs {
		b, err := os.ReadFile(filepath.Join(dir, "rt_dsfield"))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			if i := strings.IndexByte(line, '#'); i >= 0 {
				line = line[:i]
			}
			fields := strings.Fields(line)
			if len(fields) >= 2 && strings.EqualFold(fields[1], s) {
				if v, err := strconv.ParseUint(fields[0], 0, 8); err == nil {
					return uint8(v), true
				}
			}
		}
	}
	v, ok := standardDSField[strings.ToLower(s)]
	return v, ok
}

func sameDSField(a, b string) bool {
	if a == "" || b == "" {
		return a == b
	}
	x, okA := dsfieldValue(a)
	y, okB := dsfieldValue(b)
	return okA && okB && x == y
}

// shadowsReplies reports whether a rule that discards what it selects would
// select the replies to the browser: locally generated, to the client's
// address, from the address they are sent from, out the device they leave by.
// It cannot be left to verifyRouting, because a `prohibit` rule makes `ip
// route get` fail and the rollback would arrive only after the connection had
// already dropped.
func shadowsReplies(r RuleSpec, path Path) bool {
	// A goto discards nothing: it continues at a later rule made here, and
	// what that path does to the replies is verified after it applies.
	if r.Action == "lookup" || r.Action == "goto" || path.Address == "" || path.Local {
		return false
	}
	client, err := netip.ParseAddr(path.Address)
	if err != nil {
		return false
	}
	if client.Is6() != (r.Family == "inet6") {
		return false
	}
	if r.FWMark != "" {
		val, rawMask, masked := strings.Cut(r.FWMark, "/")
		mark, err := strconv.ParseUint(val, 0, 32)
		if err != nil {
			return true
		}
		mask := uint64(0xffffffff)
		if masked {
			mask, err = strconv.ParseUint(rawMask, 0, 32)
			if err != nil {
				return true
			}
		}
		if mark&mask != 0 {
			return false
		}
	}
	if r.IIF != "" && r.IIF != "lo" {
		return false
	}
	if r.OIF != "" && path.Device != "" && r.OIF != path.Device {
		return false
	}
	if r.To != "" {
		if p, err := netip.ParsePrefix(r.To); err != nil || !p.Contains(client) {
			return false
		}
	}
	if r.From != "" && path.Source != "" {
		if p, err := netip.ParsePrefix(r.From); err != nil || !p.Contains(netip.MustParseAddr(path.Source)) {
			return false
		}
	}
	// Only sockets actually answering the client prove a UID range misses
	// them; without that evidence the rule may select the replies. A TOS
	// selector is never such evidence, since an application chooses its
	// own DS field (OpenSSH marks interactive sessions).
	if r.UIDRange != "" && len(path.replyUIDs) > 0 {
		lo, hi, _ := strings.Cut(r.UIDRange, "-")
		start, err1 := strconv.ParseUint(lo, 10, 32)
		end, err2 := strconv.ParseUint(hi, 10, 32)
		if err1 == nil && err2 == nil && !slices.ContainsFunc(path.replyUIDs, func(uid uint32) bool {
			return uint64(uid) >= start && uint64(uid) <= end
		}) {
			return false
		}
	}
	return true
}

// replySocketUIDs reads the owners of the established sockets talking to the
// client, which are the sockets its replies leave from. ss prints a uid only
// when it is not root's.
func replySocketUIDs(ctx context.Context, client string) []uint32 {
	addr, err := netip.ParseAddr(client)
	if err != nil {
		return nil
	}
	out, err := run(ctx, "ss", "-Htne", "state", "established", "dst", addr.String())
	if err != nil {
		return nil
	}
	return parseSocketUIDs(out)
}

func parseSocketUIDs(out string) []uint32 {
	var uids []uint32
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		uid := uint32(0)
		for _, field := range strings.Fields(line) {
			if v, ok := strings.CutPrefix(field, "uid:"); ok {
				if n, err := strconv.ParseUint(v, 10, 32); err == nil {
					uid = uint32(n)
				}
			}
		}
		if !slices.Contains(uids, uid) {
			uids = append(uids, uid)
		}
	}
	return uids
}

// checkGoto keeps a jump among the dashboard's own rules: the kernel refuses a
// backward goto and passes over an unresolved one, and a jump over a rule
// someone else installed would silently disable it for the traffic selected.
func checkGoto(r RuleSpec, managed []RuleSpec, live []ipRule) error {
	if r.Goto <= r.Priority {
		return fmt.Errorf("a goto continues at a later priority than its own (%d); choose a priority before %d", r.Priority, r.Goto)
	}
	target := slices.ContainsFunc(managed, func(m RuleSpec) bool { return m.Family == r.Family && m.Priority == r.Goto })
	if !target {
		return fmt.Errorf("a goto made here continues at a rule made here; no %s rule has priority %d", r.Family, r.Goto)
	}
	for _, l := range live {
		if l.Priority <= r.Priority || l.Priority >= r.Goto {
			continue
		}
		if !slices.ContainsFunc(managed, func(m RuleSpec) bool { return m.Family == r.Family && m.Priority == l.Priority }) {
			return guarded("A rule this dashboard did not make sits at priority %d, between %d and %d; a jump over it would disable it for the traffic selected.", l.Priority, r.Priority, r.Goto)
		}
	}
	return nil
}

// placeRule gives a rule its priority and runs every guard an add runs
// before it is applied: a free priority in the dashboard's range checked
// against live foreign rules too, a goto kept among the dashboard's own
// rules, and no discard of the dashboard's replies. The preview runs it so a
// refusal reads the same before and at apply.
func placeRule(ctx context.Context, managed []RuleSpec, r RuleSpec, client string) (RuleSpec, Path, error) {
	used := map[int]bool{}
	for _, have := range managed {
		if have.Family == r.Family {
			used[have.Priority] = true
		}
	}
	// Explicit priorities must avoid foreign rules too. Two rules at one
	// priority otherwise leave their order dependent on who installed first.
	query := append([]string{"-j"}, familyArgs(r.Family)...)
	out, err := run(ctx, "ip", append(query, "rule", "show")...)
	if err != nil {
		return r, Path{}, err
	}
	live, err := parseIPRules(out)
	if err != nil {
		return r, Path{}, err
	}
	for _, l := range live {
		used[l.Priority] = true
	}
	if r.Priority != 0 {
		if used[r.Priority] {
			return r, Path{}, fmt.Errorf("priority %d: %w", r.Priority, ErrExists)
		}
	} else {
		for p := rulePriorityMin; p <= rulePriorityMax; p++ {
			if !used[p] && !egressReservedPriority(p) {
				r.Priority = p
				break
			}
		}
		if r.Priority == 0 {
			return r, Path{}, fmt.Errorf("all %d priorities set aside for rules made here are in use", rulePriorityMax-rulePriorityMin+1)
		}
	}
	if r.Action == "goto" {
		if err := checkGoto(r, managed, live); err != nil {
			return r, Path{}, err
		}
	}
	path, err := clientPath(ctx, client)
	if err != nil {
		return r, Path{}, err
	}
	if r.UIDRange != "" && r.Action != "lookup" && r.Action != "goto" {
		path.replyUIDs = replySocketUIDs(ctx, path.Address)
	}
	if shadowsReplies(r, path) {
		return r, path, guarded("This rule would discard the replies to your connection to the dashboard (%s), so it was not applied.", path.Address)
	}
	return r, path, nil
}

// AddRule adds a policy rule and records it.
func (s *Service) AddRule(ctx context.Context, req RuleRequest, client, actor string) (*RuleSpec, error) {
	r, err := req.spec()
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
	r, path, err := placeRule(ctx, next.Rules, r, client)
	if err != nil {
		return nil, err
	}
	args, err := ruleArgs(r)
	if err != nil {
		return nil, err
	}
	r.ID = next.takeID()
	r.Made = stamp(actor)
	next.Rules = append(next.Rules, r)
	flows := flowAnchors(ctx)
	err = s.commit(ctx, next, step{
		apply: func(ctx context.Context) error {
			_, err := run(ctx, "ip", append(append(familyArgs(r.Family), "rule", "add"), args...)...)
			return err
		},
		undo: func(ctx context.Context) {
			s.best(ctx, "ip", append(append(familyArgs(r.Family), "rule", "del"), args...)...)
		},
		verify:   verifyRouting(path),
		evidence: routingEvidence(path, flows),
	})
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// DeleteRule removes a rule the dashboard added.
func (s *Service) DeleteRule(ctx context.Context, id int, client, actor string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sp, err := s.loadSpec()
	if err != nil {
		return err
	}
	next := sp.clone()
	var gone RuleSpec
	var kept []RuleSpec
	found := false
	for _, r := range next.Rules {
		if r.ID == id {
			gone, found = r, true
			continue
		}
		kept = append(kept, r)
	}
	if !found {
		return fmt.Errorf("rule %d: %w", id, ErrNotFound)
	}
	for _, r := range kept {
		if r.Action == "goto" && r.Family == gone.Family && r.Goto == gone.Priority {
			return guarded("Rule %d jumps to this rule's priority %d; without it the jump would silently pass over every rule between them. Remove rule %d first.", r.Priority, gone.Priority, r.Priority)
		}
	}
	next.Rules = kept
	path, err := clientPath(ctx, client)
	if err != nil {
		return err
	}
	args, err := ruleArgs(gone)
	if err != nil {
		return err
	}
	flows := flowAnchors(ctx)
	return s.commit(ctx, next, step{
		apply: func(ctx context.Context) error {
			if _, err := run(ctx, "ip", append(append(familyArgs(gone.Family), "rule", "del"), args...)...); err != nil && !isGone(err) {
				return err
			}
			return nil
		},
		undo: func(ctx context.Context) {
			s.best(ctx, "ip", append(append(familyArgs(gone.Family), "rule", "add"), args...)...)
		},
		verify:   verifyRouting(path),
		evidence: routingEvidence(path, flows),
	})
}
