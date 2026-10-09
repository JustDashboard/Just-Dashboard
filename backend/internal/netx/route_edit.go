package netx

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ImpactCandidate is an address something on this host depends on, supplied
// by the api layer for owners netx does not read (Docker's networks).
type ImpactCandidate struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Address string `json:"address"`
}

// RouteImpact is what a route change would do to the traffic this host is
// known to send, modeled before it is applied. Affected addresses would take
// a different route; Unknown ones could not be decided because a rule
// selects on something the model does not know for them.
type RouteImpact struct {
	Family         string       `json:"family"`
	Table          int          `json:"table"`
	CheckedAt      time.Time    `json:"checkedAt"`
	Affected       []ImpactItem `json:"affected"`
	Unknown        []ImpactItem `json:"unknown"`
	Unaffected     int          `json:"unaffected"`
	ClientAffected bool         `json:"clientAffected"`
	Limits         []string     `json:"limits"`
}

// ImpactItem is one address and how its modeled route would change.
type ImpactItem struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Address string `json:"address"`
	Before  string `json:"before"`
	After   string `json:"after"`
	Reason  string `json:"reason,omitempty"`
}

var impactLimits = []string{
	"Modeled as packets this host sends, unmarked unless named, with the source the first lookup would choose; forwarded traffic and other sources can differ.",
	"Candidates are the addresses this host is known to depend on: your connection, its anchors and tunnel endpoints, port-forward and NAT entries, allowlisted networks, Docker networks and current connections. Traffic to anything else is not listed.",
}

// PreviewRoute models a route before it is added. Nothing is applied.
func (s *Service) PreviewRoute(ctx context.Context, req RouteRequest, client string, extra []ImpactCandidate) (*RouteImpact, error) {
	r, err := req.spec()
	if err != nil {
		return nil, err
	}
	view, err := s.readRouting(ctx)
	if err != nil {
		return nil, err
	}
	path, _ := clientPath(ctx, client)
	vrfs, read := readVRFs(ctx)
	impact := s.routeImpact(ctx, view, !read || len(vrfs) > 0, path, &r, nil, extra)
	return &impact, nil
}

// routeEntryFor is the inventory entry a managed route reads back as.
func routeEntryFor(r RouteSpec) RouteEntry {
	e := RouteEntry{
		ID: r.ID, Family: r.Family, Destination: canonicalDest(r.Destination), Type: r.Type,
		Gateway: r.Gateway, Device: r.Device, Protocol: "static", Metric: r.Metric, Source: r.Source,
		Flags: []string{}, Nexthops: []RouteNexthop{}, Owner: "just-dashboard", Managed: true,
	}
	if e.Metric == 0 && r.Family == "inet6" {
		e.Metric = 1024
	}
	for _, n := range r.Nexthops {
		e.Nexthops = append(e.Nexthops, RouteNexthop{Gateway: n.Gateway, Device: n.Device, Weight: max(n.Weight, 1)})
	}
	return e
}

// send models a packet this host sends as ip_route_connect does: the first
// lookup has no source, so source-selecting rules cannot match it; the
// route's preferred source then repeats the lookup.
func (m modelRouting) send(dst netip.Addr, mark uint32) RouteDecision {
	unspecified := netip.IPv4Unspecified()
	if dst.Is6() {
		unspecified = netip.IPv6Unspecified()
	}
	t := routeTuple{family: familyOf(dst), dst: dst, src: unspecified, iif: "lo", mark: mark}
	first := m.decide(t)
	if first.Status != "route" || first.Route == nil || first.Route.Source == "" {
		return first
	}
	src, err := netip.ParseAddr(first.Route.Source)
	if err != nil || familyOf(src) != t.family {
		return first
	}
	t.src = src
	return m.decide(t)
}

type impactProbe struct {
	ImpactCandidate
	addr netip.Addr
	mark uint32
}

// impactCandidates gathers the addresses the host depends on, one family.
func (s *Service) impactCandidates(ctx context.Context, family string, path Path, extra []ImpactCandidate) []impactProbe {
	var out []impactProbe
	seen := map[string]bool{}
	add := func(kind, name, raw string, mark uint32) {
		addr, err := ParseAddr(strings.Trim(raw, "[]"))
		if err != nil || familyOf(addr) != family || addr.IsLoopback() || addr.IsUnspecified() {
			return
		}
		key := fmt.Sprintf("%s|%s|%d", kind, addr, mark)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, impactProbe{ImpactCandidate: ImpactCandidate{Kind: kind, Name: name, Address: addr.String()}, addr: addr, mark: mark})
	}
	addPrefix := func(kind, name, raw string) {
		p, err := ParsePrefix(raw)
		if err != nil {
			add(kind, name, raw, 0)
			return
		}
		p = p.Masked()
		first := p.Addr()
		if p.Bits() < p.Addr().BitLen() {
			first = first.Next()
		}
		add(kind, name, first.String(), 0)
	}
	if path.Address != "" && !path.Local {
		add("client", "your connection to the dashboard", path.Address, 0)
	}
	for _, a := range anchorTargets {
		target := a.args[slices.Index(a.args, "get")+1]
		var mark uint32
		if i := slices.Index(a.args, "mark"); i > 0 {
			if v, err := strconv.ParseUint(a.args[i+1], 0, 32); err == nil {
				mark = uint32(v)
			}
		}
		add("anchor", a.label, target, mark)
	}
	for _, a := range tunnelAnchorTargets(ctx) {
		target := a.args[slices.Index(a.args, "get")+1]
		var mark uint32
		if i := slices.Index(a.args, "mark"); i > 0 {
			if v, err := strconv.ParseUint(a.args[i+1], 0, 32); err == nil {
				mark = uint32(v)
			}
		}
		add("tunnel", a.label, target, mark)
	}
	if sp, err := s.loadSpec(); err == nil {
		for _, f := range sp.Forwards {
			if f.Enabled {
				add("forward", fmt.Sprintf("the port forward %q", f.Name), f.Target, 0)
			}
		}
		for _, n := range sp.NAT {
			if n.Enabled {
				addPrefix("nat", fmt.Sprintf("the NAT entry %q", n.Name), n.Source)
			}
		}
	}
	for _, p := range s.trustedRanges {
		addPrefix("allowlist", "the allowlisted network "+p.String(), p.String())
	}
	for _, c := range extra {
		addPrefix(c.Kind, c.Name, c.Address)
	}
	for _, addr := range establishedPeers(ctx, 32) {
		add("connection", "an established connection", addr, 0)
	}
	return out
}

// establishedPeers are the distinct remote addresses of this host's
// established TCP connections, at most limit of them, in a stable order.
func establishedPeers(ctx context.Context, limit int) []string {
	out, err := run(ctx, "ss", "-Htn", "state", "established")
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var peers []string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		host := fields[3]
		if i := strings.LastIndexByte(host, ':'); i > 0 {
			host = host[:i]
		}
		addr, err := netip.ParseAddr(strings.Trim(host, "[]"))
		if err != nil {
			continue
		}
		addr = addr.Unmap().WithZone("")
		if addr.IsLoopback() || addr.IsLinkLocalUnicast() || seen[addr.String()] {
			continue
		}
		seen[addr.String()] = true
		peers = append(peers, addr.String())
	}
	sort.Strings(peers)
	if len(peers) > limit {
		peers = peers[:limit]
	}
	return peers
}

// routeImpact compares every candidate's modeled route before and after
// removing one route and adding another.
func (s *Service) routeImpact(ctx context.Context, view *RoutingView, vrf bool, path Path, add, remove *RouteSpec, extra []ImpactCandidate) RouteImpact {
	ref := add
	if ref == nil {
		ref = remove
	}
	impact := RouteImpact{
		Family: ref.Family, Table: ref.Table, CheckedAt: time.Now().UTC(),
		Affected: []ImpactItem{}, Unknown: []ImpactItem{}, Limits: impactLimits,
	}
	before := newModel(view, ref.Family, vrf)
	after := before
	if remove != nil {
		gone := *remove
		after = after.withoutRoute(gone.Table, func(e RouteEntry) bool {
			_, ok := managedRoute(&Spec{Routes: []RouteSpec{gone}}, e, gone.Table)
			return ok
		})
	}
	if add != nil {
		after = after.withRoute(add.Table, tableName(view, add.Table), routeEntryFor(*add))
	}
	for _, c := range s.impactCandidates(ctx, ref.Family, path, extra) {
		b, a := before.send(c.addr, c.mark), after.send(c.addr, c.mark)
		item := ImpactItem{Kind: c.Kind, Name: c.Name, Address: c.Address, Before: describeDecision(b), After: describeDecision(a)}
		switch {
		case b.Status == "unknown" || a.Status == "unknown":
			item.Reason = firstNonEmpty(a.Reason, b.Reason)
			impact.Unknown = append(impact.Unknown, item)
			if c.Kind == "client" {
				impact.ClientAffected = true
			}
		case !sameDecision(b, a):
			impact.Affected = append(impact.Affected, item)
			if c.Kind == "client" {
				impact.ClientAffected = true
			}
		default:
			impact.Unaffected++
		}
	}
	return impact
}

func sameDecision(a, b RouteDecision) bool {
	if a.Status != b.Status || a.Table != b.Table {
		return false
	}
	if a.Route == nil || b.Route == nil {
		return a.Route == nil && b.Route == nil
	}
	return a.Route.Destination == b.Route.Destination && a.Route.Device == b.Route.Device &&
		canonicalAddr(a.Route.Gateway) == canonicalAddr(b.Route.Gateway) && a.Route.Type == b.Route.Type &&
		sameNexthopEntries(a.Route.Nexthops, b.Route.Nexthops)
}

func sameNexthopEntries(a, b []RouteNexthop) bool {
	want := make([]NexthopSpec, len(a))
	for i, n := range a {
		want[i] = NexthopSpec{Gateway: n.Gateway, Device: n.Device, Weight: n.Weight}
	}
	return sameNexthops(want, b)
}

// RoutePlan is a reviewed edit of a managed route: what it is, what it would
// become, how the change is made and what it would do to known traffic.
// Replace is an in-place `ip route replace`, possible while the family,
// table, destination and metric — the kernel's identity of a route — stay
// the same; otherwise the new route is added before the old one is
// removed, so the traffic is never left without either.
type RoutePlan struct {
	Before   RouteSpec   `json:"before"`
	After    RouteSpec   `json:"after"`
	Replace  bool        `json:"replace"`
	Commands []string    `json:"commands"`
	Changes  []string    `json:"changes"`
	Impact   RouteImpact `json:"impact"`
}

// routeIdentity is what the kernel keys a route by.
func routeIdentity(r RouteSpec) string {
	metric := r.Metric
	if metric == 0 && r.Family == "inet6" {
		metric = 1024
	}
	return fmt.Sprintf("%s|%d|%s|%d", r.Family, r.Table, canonicalDest(r.Destination), metric)
}

// routeChanges lists the fields an edit changes, in the form's words.
func routeChanges(before, after RouteSpec) []string {
	var out []string
	field := func(name, a, b string) {
		if a != b {
			out = append(out, fmt.Sprintf("%s: %s → %s", name, firstNonEmpty(a, "none"), firstNonEmpty(b, "none")))
		}
	}
	field("destination", before.Destination, after.Destination)
	field("kind", before.Type, after.Type)
	field("via", before.Gateway, after.Gateway)
	field("device", before.Device, after.Device)
	field("table", strconv.Itoa(before.Table), strconv.Itoa(after.Table))
	field("metric", strconv.Itoa(before.Metric), strconv.Itoa(after.Metric))
	field("source", before.Source, after.Source)
	field("nexthops", describeNexthops(before.Nexthops), describeNexthops(after.Nexthops))
	field("note", before.Comment, after.Comment)
	return out
}

func describeNexthops(legs []NexthopSpec) string {
	parts := make([]string, 0, len(legs))
	for _, n := range legs {
		part := strings.TrimSpace(n.Gateway + " " + n.Device)
		if n.Weight > 1 {
			part += fmt.Sprintf(" ×%d", n.Weight)
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, ", ")
}

// editPlan validates an edit against the spec and the kernel. It is shared
// by the read-only plan and the apply, which recomputes it under the lock.
func (s *Service) editPlan(ctx context.Context, sp *Spec, id int, req RouteRequest) (RoutePlan, error) {
	var old RouteSpec
	found := false
	for _, r := range sp.Routes {
		if r.ID == id {
			old, found = r, true
		}
	}
	if !found {
		return RoutePlan{}, fmt.Errorf("route %d: %w", id, ErrNotFound)
	}
	next, err := req.spec()
	if err != nil {
		return RoutePlan{}, err
	}
	next.ID, next.Made = old.ID, old.Made
	plan := RoutePlan{Before: old, After: next, Replace: routeIdentity(old) == routeIdentity(next), Changes: routeChanges(old, next)}
	if len(plan.Changes) == 0 {
		return RoutePlan{}, fmt.Errorf("the edit changes nothing in route %d", id)
	}
	add, err := routeCommand("add", next)
	if err != nil {
		return RoutePlan{}, err
	}
	for _, have := range sp.Routes {
		if have.ID == id {
			continue
		}
		if routeIdentity(have) == routeIdentity(next) {
			return RoutePlan{}, fmt.Errorf("route %d already occupies that destination, table and metric: %w", have.ID, ErrExists)
		}
	}
	if next.Table == tableMain && next.Destination == "default" && next.Type == "unicast" && !plan.Replace {
		if err := onlyThisDefault(ctx, next.Family, old); err != nil {
			return RoutePlan{}, err
		}
	}
	if plan.Replace {
		replace, _ := routeCommand("replace", next)
		plan.Commands = []string{"ip " + strings.Join(replace, " ")}
	} else {
		del, _ := routeCommand("del", old)
		plan.Commands = []string{"ip " + strings.Join(add, " "), "ip " + strings.Join(del, " ")}
	}
	return plan, nil
}

// onlyThisDefault refuses a second default route in main unless the only one
// there now is the managed route being edited.
func onlyThisDefault(ctx context.Context, family string, old RouteSpec) error {
	args := append([]string{"-j"}, familyArgs(family)...)
	out, err := run(ctx, "ip", append(args, "route", "show", "default")...)
	if err != nil {
		return err
	}
	routes, err := parseIPRoutes(out)
	if err != nil {
		return err
	}
	for _, r := range routes {
		e := routeEntry(r, family, tableMain, emptySpec())
		if _, mine := managedRoute(&Spec{Routes: []RouteSpec{old}}, e, tableMain); mine {
			continue
		}
		desc := "a default route"
		if r.Gateway != "" {
			desc += " via " + r.Gateway
		}
		return guarded("This server already has %s. A second default route in the main table would replace or compete with its way to the internet; put it in a table of its own and select the traffic with a policy rule.", desc)
	}
	return nil
}

// PlanRouteEdit reviews an edit without applying it.
func (s *Service) PlanRouteEdit(ctx context.Context, id int, req RouteRequest, client string, extra []ImpactCandidate) (*RoutePlan, error) {
	sp, err := s.loadSpec()
	if err != nil {
		return nil, err
	}
	plan, err := s.editPlan(ctx, sp, id, req)
	if err != nil {
		return nil, err
	}
	view, err := s.readRouting(ctx)
	if err != nil {
		return nil, err
	}
	path, _ := clientPath(ctx, client)
	vrfs, read := readVRFs(ctx)
	plan.Impact = s.routeImpact(ctx, view, !read || len(vrfs) > 0, path, &plan.After, &plan.Before, extra)
	return &plan, nil
}

// EditRoute applies a reviewed edit. The plan is recomputed under the lock,
// never taken from the client, and the change is guarded, journaled and
// verified like an add.
func (s *Service) EditRoute(ctx context.Context, id int, req RouteRequest, client, actor string) (*RouteSpec, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sp, err := s.loadSpec()
	if err != nil {
		return nil, err
	}
	plan, err := s.editPlan(ctx, sp, id, req)
	if err != nil {
		return nil, err
	}
	next := sp.clone()
	st, err := s.readLinkState(ctx, next, client)
	if err != nil {
		return nil, err
	}
	for _, dev := range routeDevices(plan.After) {
		if _, err := st.need(dev); err != nil {
			return nil, err
		}
	}
	for i := range next.Routes {
		if next.Routes[i].ID == id {
			next.Routes[i] = plan.After
		}
	}
	oldAdd, err := routeCommand("add", plan.Before)
	if err != nil {
		return nil, err
	}
	oldDel, _ := routeCommand("del", plan.Before)
	newAdd, err := routeCommand("add", plan.After)
	if err != nil {
		return nil, err
	}
	newDel, _ := routeCommand("del", plan.After)
	flows := flowAnchors(ctx)
	var apply func(context.Context) error
	var undo func(context.Context)
	if plan.Replace {
		replace, _ := routeCommand("replace", plan.After)
		back, _ := routeCommand("replace", plan.Before)
		apply = func(ctx context.Context) error { _, err := run(ctx, "ip", replace...); return err }
		undo = func(ctx context.Context) { s.best(ctx, "ip", back...) }
	} else {
		apply = func(ctx context.Context) error {
			if _, err := run(ctx, "ip", newAdd...); err != nil {
				return err
			}
			if _, err := run(ctx, "ip", oldDel...); err != nil && !isGone(err) {
				s.best(ctx, "ip", newDel...)
				return err
			}
			return nil
		}
		undo = func(ctx context.Context) {
			s.best(ctx, "ip", newDel...)
			s.best(ctx, "ip", oldAdd...)
		}
	}
	if err := s.commit(ctx, next, step{
		apply:    apply,
		undo:     undo,
		verify:   verifyRouting(st.path),
		evidence: routingEvidence(st.path, flows),
	}); err != nil {
		return nil, err
	}
	return &plan.After, nil
}

// ChangeValidation records what a routing change was checked against after
// it applied. The client, its source-selected reply and every anchor are
// guards: a move is rolled back. Established connections are evidence: a
// route is often added precisely to move some of them, so the ones that now
// leave differently are listed for the operator to judge before confirming.
type ChangeValidation struct {
	CheckedAt      time.Time   `json:"checkedAt"`
	Client         bool        `json:"client"`
	SourceSelected bool        `json:"sourceSelected"`
	Anchors        []string    `json:"anchors"`
	Flows          int         `json:"flows"`
	Moved          []MovedFlow `json:"moved"`
}

// MovedFlow is an established connection's peer whose kernel path changed.
type MovedFlow struct {
	Address string `json:"address"`
	Before  string `json:"before"`
	After   string `json:"after"`
}

// maxFlowAnchors bounds the established peers re-asked after a change.
const maxFlowAnchors = 32

// flowAnchors asks the kernel how each established connection's peer is
// reached now, before a change. A variable for the reason anchorPaths is.
var flowAnchors = readFlowAnchors

func readFlowAnchors(ctx context.Context) []anchorPath {
	var out []anchorPath
	for _, peer := range establishedPeers(ctx, maxFlowAnchors) {
		args := []string{"-j"}
		if strings.Contains(peer, ":") {
			args = append(args, "-6")
		}
		a := anchorTarget{label: peer, args: append(args, "route", "get", peer)}
		if p, ok := readAnchor(ctx, a); ok {
			out = append(out, anchorPath{label: a.label, args: a.args, path: p})
		}
	}
	return out
}

// routingEvidence is the record verifyRouting's success leaves in the
// journal, with the flows re-asked after the change.
func routingEvidence(path Path, flows []anchorPath) func(context.Context) *ChangeValidation {
	return func(ctx context.Context) *ChangeValidation {
		v := &ChangeValidation{
			CheckedAt: time.Now().UTC(), Anchors: []string{}, Moved: []MovedFlow{}, Flows: len(flows),
			Client:         path.Address != "" && !path.Local,
			SourceSelected: path.Address != "" && !path.Local && path.Source != "",
		}
		for _, a := range path.anchors {
			v.Anchors = append(v.Anchors, a.label)
		}
		for _, f := range flows {
			after, ok := readAnchor(ctx, anchorTarget{label: f.label, args: f.args})
			if !ok {
				v.Moved = append(v.Moved, MovedFlow{Address: f.label, Before: describePath(f.path), After: "no route"})
				continue
			}
			if !samePath(f.path, after) {
				v.Moved = append(v.Moved, MovedFlow{Address: f.label, Before: describePath(f.path), After: describePath(after)})
			}
		}
		return v
	}
}
