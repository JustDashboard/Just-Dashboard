package netx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Every egress mutation goes through Service.commit: journaled before any
// effect, recoverable by the independent host process, written to the boot
// files only after its verification, and refused or put back when the
// operator's path or this server's own way out would move somewhere the
// change does not prove.

// egressRunCommands executes a group transition at runtime, accepting the
// absences and duplicates the independent recovery would accept.
func (s *Service) egressRunCommands(ctx context.Context, cmds []recoveryCommand) error {
	for _, c := range cmds {
		out, err := run(ctx, c.Tool, c.Args...)
		if err != nil && !(c.AllowGone && recoveryExpectedAbsence(c.Tool, out, err)) && !(c.AllowExists && recoveryExpectedExistence(c.Tool, c.Args, out, err)) {
			return fmt.Errorf("%s %s: %w", c.Tool, strings.Join(c.Args, " "), err)
		}
	}
	return nil
}

func (s *Service) egressUndo(cmds []recoveryCommand) func(context.Context) {
	return func(ctx context.Context) {
		if err := s.egressRunCommands(ctx, cmds); err != nil {
			recordRecoveryError(ctx, err)
			s.log.Warn("network: egress rollback failed", "err", err)
		}
	}
}

// egressStep is the runtime half of a group transition, with the pinning
// table reloaded when its rendering changes.
func (s *Service) egressStep(old, next *Spec, from, to *EgressGroupSpec, verify func(context.Context) error) step {
	forward := egressCommands(from, to)
	back := egressCommands(to, from)
	oldNFT, nextNFT := renderEgressNFT(old), renderEgressNFT(next)
	return step{
		apply: func(ctx context.Context) error {
			if err := s.egressRunCommands(ctx, forward); err != nil {
				return err
			}
			if oldNFT != nextNFT {
				if err := loadEgressNFT(ctx, next, nextNFT); err != nil {
					return err
				}
			}
			return nil
		},
		undo: func(ctx context.Context) {
			s.egressUndo(back)(ctx)
			if oldNFT != nextNFT {
				if err := loadEgressNFT(ctx, old, oldNFT); err != nil {
					recordRecoveryError(ctx, err)
				}
			}
		},
		verify: verify,
	}
}

// loadEgressNFT applies a pinning ruleset from standard input; a ruleset
// with no sticky group removes the table.
func loadEgressNFT(ctx context.Context, sp *Spec, ruleset string) error {
	if len(egressStickyGroups(sp)) == 0 {
		if _, err := run(ctx, "nft", "delete", "table", "inet", egressNFTable); err != nil && !isGone(err) && !strings.Contains(err.Error(), "No such file") {
			return err
		}
		return nil
	}
	if _, err := runStdin(ctx, []byte(ruleset), "nft", "-f", "-"); err != nil {
		var missing *UnavailableError
		if errors.As(err, &missing) {
			return &UnavailableError{Tool: "nft", Package: "nftables"}
		}
		return fmt.Errorf("loading the egress connection pinning: %w", err)
	}
	return nil
}

// egressNameTaken refuses two groups of one name.
func egressNameTaken(sp *Spec, name string, except int) bool {
	for _, g := range sp.EgressGroups {
		if g.ID != except && strings.EqualFold(g.Name, name) {
			return true
		}
	}
	return false
}

// checkEgressMembers refuses members whose devices are absent, a tunnel
// member that is not a tunnel this host owns, and a gateway member on a
// device the dashboard may not route through.
func (s *Service) checkEgressMembers(ctx context.Context, sp *Spec, g EgressGroupSpec, st *linkState) error {
	confs, _ := s.listWGConfs()
	for _, m := range g.Members {
		l, err := st.need(m.Device)
		if err != nil {
			return fmt.Errorf("member %s: device %s does not exist", m.Name, m.Device)
		}
		if l.Name == "lo" {
			return guarded("member %s cannot route through the loopback device", m.Name)
		}
		if m.Kind == "tunnel" {
			owned := false
			if c, ok := confs[m.Device]; ok && c.managed {
				owned = true
			}
			if link, ok := sp.link(m.Device); ok && slices.Contains([]string{"gre", "ip6gre"}, link.Kind) {
				owned = true
			}
			if !owned {
				return guarded("member %s: %s is not a tunnel this dashboard owns (a managed WireGuard tunnel or a managed GRE device); route a gateway member through it instead", m.Name, m.Device)
			}
		}
	}
	return nil
}

// checkEgressSlotFree refuses a slot whose tables or rule priorities hold
// something the dashboard did not put there, and foreign rules that select
// by marks inside the member mark space.
func (s *Service) checkEgressSlotFree(ctx context.Context, g EgressGroupSpec) error {
	for _, family := range []string{"inet", "inet6"} {
		args := append(append([]string{"-j"}, familyArgs(family)...), "rule", "show")
		out, err := run(ctx, "ip", args...)
		if err != nil {
			if family == "inet6" {
				continue
			}
			return err
		}
		rules, err := parseIPRules(out)
		if err != nil {
			return err
		}
		for _, r := range rules {
			if r.Priority >= egressPriority(g.Slot, 0) && r.Priority < egressPriority(g.Slot+1, 0) {
				return guarded("Rule priority %d, reserved for egress groups, is already used by something else on this host; nothing was changed.", r.Priority)
			}
			if r.FWMark != "" && !egressReservedPriority(r.Priority) {
				mask := uint64(0xffffffff)
				if r.FWMask != "" {
					if m, err := strconv.ParseUint(r.FWMask, 0, 32); err == nil {
						mask = m
					}
				}
				v, _ := strconv.ParseUint(r.FWMark, 0, 32)
				if v&mask&egressMarkMask != 0 {
					return guarded("A policy rule at priority %d selects packet marks inside 0x7f00, which egress groups use to tell their members apart; nothing was changed.", r.Priority)
				}
			}
		}
		for table := egressGroupTable(g.Slot); table <= egressMemberTable(g.Slot, egressMembersMax); table++ {
			args := append(append([]string{"-j"}, familyArgs(family)...), "route", "show", "table", strconv.Itoa(table))
			out, err := run(ctx, "ip", args...)
			if err != nil {
				continue
			}
			routes, err := parseIPRoutes(out)
			if err == nil && len(routes) > 0 {
				return guarded("Table %d, reserved for egress groups, already holds routes this dashboard did not add; nothing was changed.", table)
			}
		}
	}
	return nil
}

// CreateEgressGroup records a group and installs its member tables and
// rules. It carries no traffic until it is enabled.
func (s *Service) CreateEgressGroup(ctx context.Context, req EgressGroupRequest, client, actor string) (*EgressGroupSpec, error) {
	g, err := req.config(nil)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sp, err := s.loadSpec()
	if err != nil {
		return nil, err
	}
	if egressNameTaken(sp, g.Name, 0) {
		return nil, fmt.Errorf("an egress group named %s: %w", g.Name, ErrExists)
	}
	slot, ok := egressFreeSlot(sp)
	if !ok {
		return nil, fmt.Errorf("this host already has %d egress groups, the most the reserved tables allow", egressSlots)
	}
	next := sp.clone()
	st, err := s.readLinkState(ctx, next, client)
	if err != nil {
		return nil, err
	}
	g.Slot = slot
	if err := s.checkEgressMembers(ctx, next, g, st); err != nil {
		return nil, err
	}
	if err := s.checkEgressSlotFree(ctx, g); err != nil {
		return nil, err
	}
	g.ID = next.takeID()
	g.Active = g.firstTier()
	g.Made = stamp(actor)
	next.EgressGroups = append(next.EgressGroups, g)
	if err := s.commit(ctx, next, s.egressStep(sp, next, nil, &g, verifyRouting(st.path))); err != nil {
		return nil, err
	}
	s.egress.store.record(ctx, EgressEvent{GroupID: g.ID, Kind: "config", Outcome: s.egressOutcome(), Actor: actor, After: g.Active,
		Reason:   fmt.Sprintf("Created with %d members; member tables %d–%d and their rules installed. It carries no traffic until it is enabled.", len(g.Members), egressMemberTable(g.Slot, 1), egressMemberTable(g.Slot, egressMembersMax)),
		Evidence: &EgressEvidence{Fingerprint: g.Fingerprint(), Change: s.journalID()}})
	return &g, nil
}

// UpdateEgressGroup edits a group that is not carrying traffic. Automation
// is turned off; a changed configuration needs a new simulation.
func (s *Service) UpdateEgressGroup(ctx context.Context, id int, req EgressGroupRequest, client, actor string) (*EgressGroupSpec, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sp, err := s.loadSpec()
	if err != nil {
		return nil, err
	}
	prev, _ := sp.egressGroup(id)
	if prev == nil {
		return nil, fmt.Errorf("egress group %d: %w", id, ErrNotFound)
	}
	if prev.Enabled {
		return nil, guarded("Disable the group before editing it: an edit of a group carrying traffic could withdraw the path it is using.")
	}
	g, err := req.config(prev)
	if err != nil {
		return nil, err
	}
	if egressNameTaken(sp, g.Name, id) {
		return nil, fmt.Errorf("an egress group named %s: %w", g.Name, ErrExists)
	}
	next := sp.clone()
	st, err := s.readLinkState(ctx, next, client)
	if err != nil {
		return nil, err
	}
	g.ID, g.Slot, g.Made = prev.ID, prev.Slot, prev.Made
	if err := s.checkEgressMembers(ctx, next, g, st); err != nil {
		return nil, err
	}
	var active []int
	for _, a := range prev.Active {
		if g.member(a) != nil {
			active = append(active, a)
		}
	}
	if len(active) == 0 || g.tierOf(active) != g.tierOf(g.firstTier()) && len(active) != len(prev.Active) {
		active = g.firstTier()
	}
	g.Active, g.DecidedAt, g.DecidedBy = active, prev.DecidedAt, prev.DecidedBy
	if prev.Simulation != nil && prev.Simulation.Fingerprint == g.Fingerprint() {
		g.Simulation = prev.Simulation
	}
	ng, _ := next.egressGroup(id)
	*ng = g
	if err := s.commit(ctx, next, s.egressStep(sp, next, prev, &g, verifyRouting(st.path))); err != nil {
		return nil, err
	}
	reason := "Edited."
	if g.Simulation == nil && prev.Simulation != nil {
		reason = "Edited; the configuration changed, so automation needs a new simulation."
	}
	s.egress.store.record(ctx, EgressEvent{GroupID: id, Kind: "config", Outcome: s.egressOutcome(), Actor: actor, Before: prev.Active, After: g.Active, Reason: reason, Evidence: &EgressEvidence{Fingerprint: g.Fingerprint(), Change: s.journalID()}})
	return &g, nil
}

// DeleteEgressGroup removes a group and everything it installed. A group
// carrying traffic hands it back to the main table.
func (s *Service) DeleteEgressGroup(ctx context.Context, id int, client, actor string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sp, err := s.loadSpec()
	if err != nil {
		return err
	}
	prev, index := sp.egressGroup(id)
	if prev == nil {
		return fmt.Errorf("egress group %d: %w", id, ErrNotFound)
	}
	path, err := clientPath(ctx, client)
	if err != nil {
		return err
	}
	next := sp.clone()
	next.EgressGroups = append(next.EgressGroups[:index], next.EgressGroups[index+1:]...)
	verify := verifyRouting(path)
	if prev.Enabled {
		verify = verifyEgressWithdraw(*prev, path)
	}
	if err := s.commit(ctx, next, s.egressStep(sp, next, prev, nil, verify)); err != nil {
		return err
	}
	s.egress.forget(id)
	s.egress.store.record(ctx, EgressEvent{GroupID: id, Kind: "config", Outcome: s.egressOutcome(), Actor: actor, Before: prev.Active, Reason: "Removed with its tables, rules and pinning.", Evidence: &EgressEvidence{Change: s.journalID()}})
	return nil
}

// verifyEgressWithdraw is the check for taking a group's selector away: its
// traffic returns to whatever the main table says, so paths may move, but
// none may be left with no route and no WireGuard transport may end up in a
// tunnel. The operator's path may move only if the group was carrying it.
func verifyEgressWithdraw(g EgressGroupSpec, before Path) func(context.Context) error {
	carried := egressSelectsReply(g, before)
	return func(ctx context.Context) error {
		for _, a := range before.anchors {
			after, err := reaskAnchor(ctx, a)
			if err != nil || after.Device == "" {
				return guarded("this would leave this server with no route to %s, so it was put back", a.label)
			}
			if a.tunnels != nil && a.tunnels[after.Device] {
				return guarded("this would route %s into %s, so the tunnel would carry its own transport; it was put back", a.label, after.Device)
			}
		}
		if before.Address == "" || before.Local {
			return nil
		}
		after, err := clientPath(ctx, before.Address)
		if err != nil || after.Device == "" {
			return guarded("with this change the kernel has no route back to your connection (%s), so it was put back", before.Address)
		}
		if !carried && !samePath(before, after) {
			return guarded("this would send the reply to your connection (%s) %s instead of %s, so it was put back", before.Address, describePath(after), describePath(before))
		}
		return nil
	}
}

func reaskAnchor(ctx context.Context, a anchorPath) (Path, error) {
	raw, err := run(ctx, "ip", a.args...)
	if err != nil {
		return Path{}, err
	}
	return parseRouteGet(raw, Path{Address: a.path.Address})
}

// egressOperator is one operator connection a switch must not strand.
type egressOperator struct {
	before Path
	// carried is whether the group's next state selects its replies, and
	// proven whether a probe from its reply source passed through every
	// member it would then take.
	carried bool
	proven  bool
}

// hopMatches reports whether a kernel answer is a route through a member.
func hopMatches(m EgressMember, p Path) bool {
	if p.Device != m.Device {
		return false
	}
	return m.Kind == "tunnel" || canonicalAddr(p.Gateway) == canonicalAddr(m.Gateway)
}

// verifyEgressDecision checks a group's new decided state: the group table
// now holds exactly the decided route; every anchor either stayed or moved
// onto a decided member, and no WireGuard transport moved into a tunnel; and
// every operator connection stayed, or moved onto a decided member through
// which a probe from its own reply source was proven.
func (s *Service) verifyEgressDecision(next EgressGroupSpec, operators []egressOperator, anchors []anchorPath) func(context.Context) error {
	var hops []EgressMember
	tunnels := map[string]bool{}
	for _, id := range next.Active {
		if m := next.member(id); m != nil {
			hops = append(hops, *m)
			if m.Kind == "tunnel" {
				tunnels[m.Device] = true
			}
		}
	}
	onDecided := func(p Path) bool {
		for _, m := range hops {
			if hopMatches(m, p) {
				return true
			}
		}
		return false
	}
	return func(ctx context.Context) error {
		if err := checkEgressGroupRoute(ctx, next); err != nil {
			return err
		}
		for _, a := range anchors {
			after, err := reaskAnchor(ctx, a)
			if err != nil || after.Device == "" {
				return guarded("this would leave this server with no route to %s, which your connection and every outbound one ride on, so it was put back", a.label)
			}
			if a.tunnels != nil {
				if a.tunnels[after.Device] || tunnels[after.Device] {
					return guarded("this would route %s into %s, so the tunnel would carry its own transport and its peer would be cut off; it was put back. Keep the endpoint on the main table with a host route, or select narrower traffic", a.label, after.Device)
				}
				continue
			}
			if !samePath(a.path, after) && !onDecided(after) {
				return guarded("this would move how this server reaches %s (%s instead of %s), which is not the member decided; it was put back", a.label, describePath(after), describePath(a.path))
			}
		}
		for _, op := range operators {
			before := op.before
			if before.Address == "" || before.Local {
				continue
			}
			after, err := clientPath(ctx, before.Address)
			if err != nil || after.Device == "" {
				return guarded("with this change the kernel has no route back to your connection (%s), so it was put back", before.Address)
			}
			moved := !samePath(before, after)
			if !moved && before.Source != "" {
				args := []string{"-j"}
				if a, err := netip.ParseAddr(before.Address); err == nil && a.Is6() {
					args = append(args, "-6")
				}
				out, err := run(ctx, "ip", append(args, "route", "get", before.Address, "from", before.Source)...)
				if err != nil {
					return guarded("with this change the kernel has no route for the replies to your connection (%s) from %s, so it was put back", before.Address, before.Source)
				}
				if sourced, err := parseRouteGet(out, Path{Address: before.Address}); err == nil && !samePath(before, sourced) {
					after, moved = sourced, true
				}
			}
			if !moved {
				continue
			}
			if op.carried && op.proven && onDecided(after) {
				continue
			}
			return guarded("this would send the replies to your connection (%s) %s instead of %s, through a path not proven for them, so it was put back", before.Address, describePath(after), describePath(before))
		}
		return nil
	}
}

// checkEgressGroupRoute reads the group table back and compares it with the
// decided route.
func checkEgressGroupRoute(ctx context.Context, g EgressGroupSpec) error {
	want, ok := egressGroupRoute(g)
	if !ok {
		return nil
	}
	got, err := readEgressDefault(ctx, g.Family, egressGroupTable(g.Slot))
	if err != nil {
		return err
	}
	if !sameEgressRoute(got, want) {
		return fmt.Errorf("table %d does not hold the decided route after the change (it holds %s)", egressGroupTable(g.Slot), describeEgressRoute(got))
	}
	return nil
}

// egressRoute is a default route as read back: its next hops.
type egressRoute []RouteNexthop

func readEgressDefault(ctx context.Context, family string, table int) (egressRoute, error) {
	args := append(append([]string{"-j"}, familyArgs(family)...), "route", "show", "table", strconv.Itoa(table), "default")
	out, err := run(ctx, "ip", args...)
	if err != nil {
		return nil, fmt.Errorf("reading table %d: %w", table, err)
	}
	routes, err := parseIPRoutes(out)
	if err != nil {
		return nil, err
	}
	for _, r := range routes {
		if r.Dst != "default" && r.Dst != "" && r.Dst != "::/0" && r.Dst != "0.0.0.0/0" {
			continue
		}
		if len(r.Nexthops) > 0 {
			var hops egressRoute
			for _, n := range r.Nexthops {
				w := n.Weight
				if w == 0 {
					w = 1
				}
				hops = append(hops, RouteNexthop{Gateway: canonicalAddr(n.Gateway), Device: n.Dev, Weight: w})
			}
			return hops, nil
		}
		return egressRoute{{Gateway: canonicalAddr(r.Gateway), Device: r.Dev, Weight: 1}}, nil
	}
	return nil, nil
}

// sameEgressRoute compares a read-back route with the arguments it was
// added with.
func sameEgressRoute(got egressRoute, want []string) bool {
	var hops egressRoute
	current := -1
	for i := 0; i < len(want); i++ {
		switch want[i] {
		case "nexthop":
			hops = append(hops, RouteNexthop{Weight: 1})
			current = len(hops) - 1
		case "via", "dev", "weight":
			if current < 0 {
				hops = append(hops, RouteNexthop{Weight: 1})
				current = 0
			}
			if i+1 >= len(want) {
				return false
			}
			switch want[i] {
			case "via":
				hops[current].Gateway = canonicalAddr(want[i+1])
			case "dev":
				hops[current].Device = want[i+1]
			case "weight":
				hops[current].Weight, _ = strconv.Atoi(want[i+1])
			}
			i++
		}
	}
	if len(hops) != len(got) {
		return false
	}
	key := func(h RouteNexthop) string {
		w := h.Weight
		if len(hops) == 1 {
			w = 1
		}
		return h.Gateway + "|" + h.Device + "|" + strconv.Itoa(w)
	}
	a, b := make([]string, len(hops)), make([]string, len(got))
	for i := range hops {
		a[i], b[i] = key(hops[i]), key(got[i])
	}
	sort.Strings(a)
	sort.Strings(b)
	return slices.Equal(a, b)
}

func describeEgressRoute(r egressRoute) string {
	if len(r) == 0 {
		return "no default route"
	}
	parts := make([]string, len(r))
	for i, h := range r {
		parts[i] = describePath(Path{Device: h.Device, Gateway: h.Gateway})
		if len(r) > 1 {
			parts[i] += " weight " + strconv.Itoa(h.Weight)
		}
	}
	return strings.Join(parts, ", ")
}

// describeDecided is the route a decided member set means, for the record.
func describeDecided(g EgressGroupSpec, ids []int) string {
	var r egressRoute
	for _, id := range ids {
		if m := g.member(id); m != nil {
			r = append(r, RouteNexthop{Gateway: m.Gateway, Device: m.Device, Weight: m.Weight})
		}
	}
	return describeEgressRoute(r) + " (table " + strconv.Itoa(egressGroupTable(g.Slot)) + ")"
}

// egressSources resolves each member's probe source: its configured address,
// else the device's first global address of the group's family.
func (s *Service) egressSources(ctx context.Context, g EgressGroupSpec) map[int]netip.Addr {
	out := map[int]netip.Addr{}
	devices := map[string][]ipAddrInfo{}
	for _, m := range g.Members {
		if m.Source != "" {
			if a, err := netip.ParseAddr(m.Source); err == nil {
				out[m.ID] = a
			}
			continue
		}
		addrs, ok := devices[m.Device]
		if !ok {
			addrs = readDeviceAddrs(ctx, m.Device)
			devices[m.Device] = addrs
		}
		for _, a := range addrs {
			if a.family == g.Family && a.scope == "global" {
				out[m.ID] = a.addr
				break
			}
		}
	}
	return out
}

type ipAddrInfo struct {
	addr   netip.Addr
	family string
	scope  string
}

func readDeviceAddrs(ctx context.Context, device string) []ipAddrInfo {
	if ValidIfName(device) != nil {
		return nil
	}
	out, err := run(ctx, "ip", "-j", "addr", "show", "dev", device)
	if err != nil {
		return nil
	}
	var rows []struct {
		AddrInfo []struct {
			Family string `json:"family"`
			Local  string `json:"local"`
			Scope  string `json:"scope"`
		} `json:"addr_info"`
	}
	if json.Unmarshal([]byte(out), &rows) != nil {
		return nil
	}
	var addrs []ipAddrInfo
	for _, r := range rows {
		for _, a := range r.AddrInfo {
			if addr, err := netip.ParseAddr(a.Local); err == nil {
				addrs = append(addrs, ipAddrInfo{addr: addr, family: a.Family, scope: a.Scope})
			}
		}
	}
	return addrs
}

// egressMemberPath is where a member's probes are bound.
func (s *Service) egressMemberPath(g EgressGroupSpec, m EgressMember, sources map[int]netip.Addr) egressPath {
	return egressPath{Mark: egressMark(g.Slot, m.ID), Device: m.Device, Source: sources[m.ID], Netns: s.egressNetns}
}

// proveEgressMembers runs one probe round through each member now. A member
// is proven only if every probe answered within the latency threshold.
func (s *Service) proveEgressMembers(ctx context.Context, g EgressGroupSpec, ids []int, sources map[int]netip.Addr) (map[int]EgressSample, error) {
	timeout := time.Duration(g.Thresholds.TimeoutMillis) * time.Millisecond
	samples := map[int]EgressSample{}
	type result struct {
		id int
		s  EgressSample
	}
	ch := make(chan result, len(ids))
	for _, id := range ids {
		m := g.member(id)
		if m == nil {
			return nil, fmt.Errorf("member %d does not exist", id)
		}
		go func(m EgressMember) {
			ch <- result{m.ID, sampleMember(ctx, s.egressMemberPath(g, m, sources), g.Probes, timeout, time.Now().UTC())}
		}(*m)
	}
	var failed []string
	for range ids {
		r := <-ch
		judgeSample(&r.s, nil, g.Thresholds)
		samples[r.id] = r.s
		if r.s.OK != r.s.Total || !r.s.Good {
			why := r.s.Why
			if why == "" {
				why = fmt.Sprintf("%d of %d probes answered", r.s.OK, r.s.Total)
			}
			failed = append(failed, egressMemberName(g, r.id)+": "+why+egressProbeErrors(r.s))
		}
	}
	if len(failed) > 0 {
		sort.Strings(failed)
		return samples, guarded("%d %s did not prove %s path just now (%s); nothing was changed.", len(failed), plural(len(failed), "member", "members"), plural(len(failed), "its", "their"), strings.Join(failed, "; "))
	}
	return samples, nil
}

func egressProbeErrors(s EgressSample) string {
	var parts []string
	for _, p := range s.Probes {
		if !p.OK && p.Error != "" {
			parts = append(parts, p.Kind+" "+p.Target+": "+p.Error)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " — " + strings.Join(parts, ", ")
}

// proveOperatorSource probes through each member from an operator reply's
// own source address: the one thing that shows the member would carry those
// replies, since an upstream that filters foreign sources drops them.
func (s *Service) proveOperatorSource(ctx context.Context, g EgressGroupSpec, ids []int, path Path) error {
	src, err := netip.ParseAddr(path.Source)
	if err != nil {
		return guarded("The group would carry the replies to your connection (%s), and their source address could not be read to prove a member for them; nothing was changed.", path.Address)
	}
	timeout := time.Duration(g.Thresholds.TimeoutMillis) * time.Millisecond
	for _, id := range ids {
		m := g.member(id)
		if m == nil {
			continue
		}
		p := egressPath{Mark: egressMark(g.Slot, m.ID), Device: m.Device, Source: src, Netns: s.egressNetns}
		sample := sampleMember(ctx, p, g.Probes, timeout, time.Now().UTC())
		if sample.OK != sample.Total {
			return guarded("This would carry the replies to your connection (%s, sent from %s) through %s, and a probe from %s through %s failed%s; nothing was changed. Keep your address on the main table (the group's protected addresses) or connect through a path the group does not carry.",
				path.Address, path.Source, m.Name, path.Source, m.Name, egressProbeErrors(sample))
		}
	}
	return nil
}

// egressOperators reads every operator path a change must keep: the
// requester's, and the protected addresses' (the only operators an
// automated switch knows).
func (s *Service) egressOperators(ctx context.Context, next EgressGroupSpec, client string) ([]egressOperator, []anchorPath, error) {
	var ops []egressOperator
	var anchors []anchorPath
	if client != "" {
		path, err := clientPath(ctx, client)
		if err != nil {
			return nil, nil, err
		}
		anchors = path.anchors
		ops = append(ops, egressOperator{before: path})
	} else {
		anchors = anchorPaths(ctx)
	}
	for _, raw := range next.Protected {
		p, err := netip.ParsePrefix(raw)
		if err != nil || p.Bits() != p.Addr().BitLen() {
			continue
		}
		path, err := clientPath(ctx, p.Addr().String())
		if err != nil {
			continue
		}
		path.anchors = nil
		ops = append(ops, egressOperator{before: path})
	}
	for i := range ops {
		ops[i].carried = egressSelectsReply(next, ops[i].before)
	}
	return ops, anchors, nil
}

// EnableEgressGroup installs the selector: the group carries its policy's
// traffic through the decided members. The decided members must prove their
// paths now, and an operator connection the group would carry is kept on
// the main table as a protected address.
func (s *Service) EnableEgressGroup(ctx context.Context, id int, client, actor string) (*EgressGroupSpec, error) {
	sp, err := s.loadSpec()
	if err != nil {
		return nil, err
	}
	g, _ := sp.egressGroup(id)
	if g == nil {
		return nil, fmt.Errorf("egress group %d: %w", id, ErrNotFound)
	}
	if g.Enabled {
		return g, nil
	}
	sources := s.egressSources(ctx, *g)
	samples, err := s.proveEgressMembers(ctx, *g, g.Active, sources)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sp, err = s.loadSpec()
	if err != nil {
		return nil, err
	}
	prev, _ := sp.egressGroup(id)
	if prev == nil || prev.Enabled || prev.Fingerprint() != g.Fingerprint() || !equalIDs(prev.Active, g.Active) {
		return nil, &ConfirmationError{"The group changed while its members were being proven; review it and enable it again."}
	}
	next := sp.clone()
	ng, _ := next.egressGroup(id)
	ng.Enabled = true
	path, err := clientPath(ctx, client)
	if err != nil {
		return nil, err
	}
	protectedNow := ""
	if egressSelectsReply(*ng, path) {
		addr, err := netip.ParseAddr(path.Address)
		if err == nil && familyOf(addr) == ng.Family {
			if len(ng.Protected) >= egressProtectedMax {
				return nil, guarded("This group would carry the replies to your connection (%s) and already keeps %d protected addresses on the main table; remove one before enabling it from here.", path.Address, egressProtectedMax)
			}
			protectedNow = netip.PrefixFrom(addr, addr.BitLen()).String()
			ng.Protected = append(ng.Protected, protectedNow)
		}
	}
	ops, anchors, err := s.egressOperators(ctx, *ng, client)
	if err != nil {
		return nil, err
	}
	for i := range ops {
		if ops[i].carried {
			if err := s.proveOperatorSource(ctx, *ng, ng.Active, ops[i].before); err != nil {
				return nil, err
			}
			ops[i].proven = true
		}
	}
	routeBefore := ""
	if len(anchors) > 0 {
		routeBefore = describePath(anchors[0].path)
	}
	if err := s.commit(ctx, next, s.egressStep(sp, next, prev, ng, s.verifyEgressDecision(*ng, ops, anchors))); err != nil {
		s.egress.store.record(ctx, EgressEvent{GroupID: id, Kind: egressFailureKind(err), Outcome: egressFailureKind(err), Actor: actor, After: ng.Active, Reason: "Enabling was declined: " + err.Error()})
		return nil, err
	}
	reason := "Enabled: its traffic now takes " + describeDecided(*ng, ng.Active) + "."
	if protectedNow != "" {
		reason += " Your address " + protectedNow + " is kept on the main table."
	}
	s.egress.store.record(ctx, EgressEvent{GroupID: id, Kind: "config", Action: "enable", Outcome: s.egressOutcome(), Actor: actor, After: ng.Active, Reason: reason,
		Evidence: &EgressEvidence{Members: egressSampleEvidence(samples), RouteBefore: routeBefore, RouteAfter: describeDecided(*ng, ng.Active), Change: s.journalID()}})
	return ng, nil
}

// DisableEgressGroup withdraws the selector: the group's traffic returns to
// the main table. Automation is turned off with it.
func (s *Service) DisableEgressGroup(ctx context.Context, id int, client, actor string) (*EgressGroupSpec, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sp, err := s.loadSpec()
	if err != nil {
		return nil, err
	}
	prev, _ := sp.egressGroup(id)
	if prev == nil {
		return nil, fmt.Errorf("egress group %d: %w", id, ErrNotFound)
	}
	if !prev.Enabled {
		return prev, nil
	}
	path, err := clientPath(ctx, client)
	if err != nil {
		return nil, err
	}
	next := sp.clone()
	ng, _ := next.egressGroup(id)
	ng.Enabled, ng.Automation = false, false
	if err := s.commit(ctx, next, s.egressStep(sp, next, prev, ng, verifyEgressWithdraw(*prev, path))); err != nil {
		return nil, err
	}
	reason := "Disabled: its traffic returns to the main table."
	if prev.Automation {
		reason += " Automation is off."
	}
	s.egress.store.record(ctx, EgressEvent{GroupID: id, Kind: "config", Action: "disable", Outcome: s.egressOutcome(), Actor: actor, Before: prev.Active, Reason: reason, Evidence: &EgressEvidence{RouteBefore: describeDecided(*prev, prev.Active), Change: s.journalID()}})
	return ng, nil
}

// SetEgressAutomation lets the monitor switch, or stops it. Turning it on
// needs a carrying group whose current configuration passed a simulation.
func (s *Service) SetEgressAutomation(ctx context.Context, id int, on bool, actor string) (*EgressGroupSpec, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sp, err := s.loadSpec()
	if err != nil {
		return nil, err
	}
	prev, _ := sp.egressGroup(id)
	if prev == nil {
		return nil, fmt.Errorf("egress group %d: %w", id, ErrNotFound)
	}
	if prev.Automation == on {
		return prev, nil
	}
	passed := s.passedSimulation(ctx, *prev)
	if on {
		if block := egressAutomationBlock(*prev, passed); block != "" {
			return nil, &ConfirmationError{block}
		}
	}
	next := sp.clone()
	ng, _ := next.egressGroup(id)
	ng.Automation = on
	if on {
		ng.Simulation = &EgressSimulationRef{ID: passed.ID, Fingerprint: passed.Fingerprint, PassedAt: passed.FinishedAt}
	}
	if err := s.commit(ctx, next, step{}); err != nil {
		return nil, err
	}
	reason := "Automation off: the monitor measures and advises, and switches only when an operator does."
	if on {
		reason = fmt.Sprintf("Automation on, authorised by simulation %s of this configuration.", passed.ID)
	}
	s.egress.store.record(ctx, EgressEvent{GroupID: id, Kind: "automation", Outcome: "applied", Actor: actor, Reason: reason, Evidence: &EgressEvidence{Fingerprint: prev.Fingerprint()}})
	return ng, nil
}

// egressAutomationBlock says why automation cannot be turned on, or "";
// passed is the newest passed simulation of the current configuration.
func egressAutomationBlock(g EgressGroupSpec, passed *EgressSimulation) string {
	switch {
	case !g.Enabled:
		return "Enable the group before automating it: automation switches a group that carries traffic."
	case passed == nil:
		return "Run a simulation of this configuration first: automation stays off until the monitor has been shown flapping, loss and failure in disposable namespaces and every expectation held."
	}
	return ""
}

// egressRecorded is a switch failure already written to the record.
type egressRecorded struct{ error }

func (e egressRecorded) Unwrap() error { return e.error }

// EgressSwitchRequest is a manual switch.
type EgressSwitchRequest struct {
	// Action is failover (to the best proven tier other than the current
	// one), failback (to the best proven tier) or members (exactly Members).
	Action  string `json:"action"`
	Members []int  `json:"members,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// EgressSwitchResult is what a switch did.
type EgressSwitchResult struct {
	Group       *EgressGroupSpec   `json:"group"`
	Event       int64              `json:"event"`
	Before      []int              `json:"before"`
	After       []int              `json:"after"`
	Connections *EgressConnections `json:"connections,omitempty"`
}

// SwitchEgress is an operator's "fail over now" or "fail back now": the same
// proof and guards as an automated switch, without waiting for the rules.
func (s *Service) SwitchEgress(ctx context.Context, id int, req EgressSwitchRequest, client, actor string) (*EgressSwitchResult, error) {
	sp, err := s.loadSpec()
	if err != nil {
		return nil, err
	}
	g, _ := sp.egressGroup(id)
	if g == nil {
		return nil, fmt.Errorf("egress group %d: %w", id, ErrNotFound)
	}
	reason := strings.TrimSpace(req.Reason)
	if reason != "" {
		if reason, err = CleanLabel(reason, 160); err != nil {
			return nil, err
		}
	}
	views := s.egress.views(*g, time.Now())
	var target []int
	switch req.Action {
	case "failover", "failback":
		target = egressManualTarget(*g, views, req.Action)
		if len(target) == 0 {
			if req.Action == "failover" {
				return nil, guarded("No member outside the decided set is proven healthy, so there is nowhere to fail over to.")
			}
			return nil, guarded("The group is already on its best proven members, or no better member is proven healthy.")
		}
	case "members":
		target = append([]int{}, req.Members...)
		sort.Ints(target)
		if len(target) == 0 {
			return nil, fmt.Errorf("name the members to switch to")
		}
		tier := 0
		for _, mid := range target {
			m := g.member(mid)
			if m == nil {
				return nil, fmt.Errorf("member %d: %w", mid, ErrNotFound)
			}
			if tier != 0 && m.Priority != tier {
				return nil, fmt.Errorf("the members of one decision share a tier")
			}
			tier = m.Priority
		}
	default:
		return nil, fmt.Errorf("a switch is failover, failback or members")
	}
	if reason == "" {
		reason = "An operator chose " + req.Action + "."
	}
	return s.switchEgress(ctx, id, target, egressSwitchOptions{Action: "manual", Reason: reason, Actor: actor, Client: client})
}

// egressManualTarget picks the members an operator's failover or failback
// means.
func egressManualTarget(g EgressGroupSpec, views map[int]egressMemberView, action string) []int {
	byTier := map[int][]int{}
	var tiers []int
	for _, m := range g.Members {
		if action == "failover" && slices.Contains(g.Active, m.ID) {
			continue
		}
		if views[m.ID].State == "down" {
			continue
		}
		if _, ok := byTier[m.Priority]; !ok {
			tiers = append(tiers, m.Priority)
		}
		byTier[m.Priority] = append(byTier[m.Priority], m.ID)
	}
	sort.Ints(tiers)
	if action == "failover" {
		active := g.tierOf(g.Active)
		for _, t := range tiers {
			if t != active || len(g.Active) == 0 {
				return byTier[t]
			}
		}
		return nil
	}
	if len(tiers) == 0 || equalIDs(byTier[tiers[0]], g.Active) {
		return nil
	}
	return byTier[tiers[0]]
}

type egressSwitchOptions struct {
	Action string
	Reason string
	Actor  string
	// Client is the interactive operator; empty for the monitor, which knows
	// only the protected addresses.
	Client      string
	Automated   bool
	Fingerprint string
	Evidence    []EgressMemberEvidence
}

// switchEgress makes one decision real.
func (s *Service) switchEgress(ctx context.Context, id int, target []int, opts egressSwitchOptions) (*EgressSwitchResult, error) {
	target = append([]int{}, target...)
	sort.Ints(target)
	sp, err := s.loadSpec()
	if err != nil {
		return nil, err
	}
	g, _ := sp.egressGroup(id)
	if g == nil {
		return nil, fmt.Errorf("egress group %d: %w", id, ErrNotFound)
	}
	if opts.Automated && (!g.Automation || !g.Enabled || g.Fingerprint() != opts.Fingerprint) {
		return nil, &ConfirmationError{"Automation was turned off or the group changed before the switch."}
	}
	if equalIDs(g.Active, target) {
		return nil, fmt.Errorf("the group already routes through %s", egressNames(*g, target))
	}
	record := EgressEvent{GroupID: id, Kind: "switch", Action: opts.Action, Outcome: "applying", Reason: opts.Reason, Actor: opts.Actor, Before: g.Active, After: target,
		Evidence: &EgressEvidence{Members: opts.Evidence, RouteBefore: describeDecided(*g, g.Active), RouteAfter: describeDecided(*g, target), Fingerprint: g.Fingerprint()}}
	fail := func(err error) (*EgressSwitchResult, error) {
		record.Outcome = egressFailureKind(err)
		record.Reason = opts.Reason + " Declined: " + err.Error()
		if record.ID != 0 {
			s.egress.store.finish(ctx, record.ID, record.Outcome, record.Reason, record.Evidence)
			return nil, egressRecorded{err}
		}
		if opts.Automated {
			// The monitor records a repeated refusal once.
			return nil, err
		}
		record.Kind = record.Outcome
		s.egress.store.record(ctx, record)
		return nil, egressRecorded{err}
	}
	sources := s.egressSources(ctx, *g)
	samples, err := s.proveEgressMembers(ctx, *g, target, sources)
	if err != nil {
		return fail(err)
	}
	if len(record.Evidence.Members) == 0 {
		record.Evidence.Members = egressSampleEvidence(samples)
	}
	next := sp.clone()
	ng, _ := next.egressGroup(id)
	ng.Active, ng.DecidedAt, ng.DecidedBy = target, s.egress.clock(), opts.Actor
	ops, anchors, err := s.egressOperators(ctx, *ng, opts.Client)
	if err != nil {
		return fail(err)
	}
	for i := range ops {
		if ops[i].carried {
			if err := s.proveOperatorSource(ctx, *ng, target, ops[i].before); err != nil {
				return fail(err)
			}
			ops[i].proven = true
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.loadSpec()
	if err != nil {
		return fail(err)
	}
	now, _ := current.egressGroup(id)
	if now == nil || now.Fingerprint() != g.Fingerprint() || !equalIDs(now.Active, g.Active) || now.Enabled != g.Enabled || opts.Automated && !now.Automation {
		return fail(&ConfirmationError{"The group changed while the switch was being proven; nothing was switched."})
	}
	// Anything else in the spec may have changed while the members were
	// proven; the decision is made on the spec as it is now.
	decided := *ng
	next = current.clone()
	ng, _ = next.egressGroup(id)
	ng.Active, ng.DecidedAt, ng.DecidedBy = decided.Active, decided.DecidedAt, decided.DecidedBy
	record.ID = s.egress.store.record(ctx, record)
	leaving, staying := map[int]bool{}, map[int]bool{}
	views := s.egress.views(*g, time.Now())
	for _, mid := range g.Active {
		if !slices.Contains(target, mid) {
			leaving[mid] = true
		}
	}
	// A sticky group's pinned connections stay on any member still up,
	// including one the decision leaves: that is what stickiness is for.
	if g.Sticky {
		for _, m := range g.Members {
			if views[m.ID].State != "down" {
				staying[m.ID] = true
			}
		}
	}
	connections := s.countEgressConnections(ctx, *g, sources, leaving, staying)
	if err := s.commit(ctx, next, s.egressStep(current, next, now, ng, s.verifyEgressDecision(*ng, ops, anchors))); err != nil {
		record.Evidence.Connections = connections
		return fail(err)
	}
	record.Evidence.Change = s.journalID()
	down := map[int]bool{}
	for mid := range leaving {
		if views[mid].State == "down" {
			down[mid] = true
		}
	}
	if ng.Connections == "flush" && len(down) > 0 {
		s.flushEgressConnections(ctx, *ng, sources, down, egressSpared(*ng, ops), connections)
	}
	record.Evidence.Connections = connections
	record.Outcome = s.egressOutcome()
	s.egress.store.finish(ctx, record.ID, record.Outcome, "", record.Evidence)
	s.egress.noteSwitch(id)
	return &EgressSwitchResult{Group: ng, Event: record.ID, Before: g.Active, After: target, Connections: connections}, nil
}

// egressSpared are the addresses a flush must never cut: the protected
// networks and every operator connection read for the switch.
func egressSpared(g EgressGroupSpec, ops []egressOperator) []netip.Prefix {
	var out []netip.Prefix
	for _, raw := range g.Protected {
		if p, err := netip.ParsePrefix(raw); err == nil {
			out = append(out, p)
		}
	}
	for _, op := range ops {
		if a, err := netip.ParseAddr(op.before.Address); err == nil {
			out = append(out, netip.PrefixFrom(a, a.BitLen()))
		}
	}
	return out
}

func egressFailureKind(err error) string {
	var guard *GuardError
	var confirm *ConfirmationError
	var readOnly *ReadOnlyError
	if errors.As(err, &guard) || errors.As(err, &confirm) || errors.As(err, &readOnly) {
		return "refused"
	}
	return "failed"
}

func egressSampleEvidence(samples map[int]EgressSample) []EgressMemberEvidence {
	var out []EgressMemberEvidence
	for id, s := range samples {
		state := "proven"
		if !s.Good || s.OK != s.Total {
			state = "unproven"
		}
		out = append(out, EgressMemberEvidence{ID: id, State: state, LatencyMillis: s.LatencyMillis, Loss: s.Loss, OK: s.OK, Total: s.Total, Why: s.Why, At: s.At})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// journalID is the change just committed, for the record.
func (s *Service) journalID() string {
	if j, err := readChange(s.paths.Dir); err == nil {
		return j.ID
	}
	return ""
}

// egressOutcome is a committed change's outcome: applied, or pending while
// an interactive apply waits for reconnection confirmation (the monitor
// settles it once the journal is confirmed or recovered).
func (s *Service) egressOutcome() string {
	if j, err := readChange(s.paths.Dir); err == nil && j.Phase == "awaiting_confirmation" {
		return "pending"
	}
	return "applied"
}
