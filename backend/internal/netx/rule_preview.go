package netx

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// RulePreview is a policy rule reviewed before it is added: the priority it
// would take and its neighbours, the rules that already decide everything it
// selects or that it would decide for, the known traffic it would move, and
// an optional packet the operator names, answered by the kernel for now and
// by the model with the rule in place.
type RulePreview struct {
	Rule       RuleSpec       `json:"rule"`
	CheckedAt  time.Time      `json:"checkedAt"`
	After      *RuleEntry     `json:"after,omitempty"`
	Before     *RuleEntry     `json:"before,omitempty"`
	ShadowedBy []RuleRelation `json:"shadowedBy"`
	Shadows    []RuleRelation `json:"shadows"`
	Impact     RouteImpact    `json:"impact"`
	Probe      *RuleProbe     `json:"probe,omitempty"`
	// Refusal is the guard an add would answer with; the preview still
	// shows the rest so the reason can be read beside its evidence.
	Refusal string `json:"refusal,omitempty"`
}

// RuleRelation names another rule and why it relates.
type RuleRelation struct {
	Priority int    `json:"priority"`
	Owner    string `json:"owner"`
	Reason   string `json:"reason"`
}

// RuleProbe is one named packet: what the kernel answers now, what the model
// answers now and with the rule. Agreement is whether the model's present
// answer is the kernel's, which is what entitles its prediction to trust.
type RuleProbe struct {
	Tuple       RouteTuple    `json:"tuple"`
	Kernel      *RouteLookup  `json:"kernel,omitempty"`
	KernelError string        `json:"kernelError,omitempty"`
	Now         RouteDecision `json:"now"`
	With        RouteDecision `json:"with"`
	Agreement   string        `json:"agreement"`
	Changes     bool          `json:"changes"`
}

// PreviewRule reviews a rule without adding it.
func (s *Service) PreviewRule(ctx context.Context, req RuleRequest, probe *RouteTuple, client string, extra []ImpactCandidate) (*RulePreview, error) {
	r, err := req.spec()
	if err != nil {
		return nil, err
	}
	sp, err := s.loadSpec()
	if err != nil {
		return nil, err
	}
	preview := &RulePreview{CheckedAt: time.Now().UTC(), ShadowedBy: []RuleRelation{}, Shadows: []RuleRelation{}}
	placed, path, err := placeRule(ctx, sp.Rules, r, client)
	var guard *GuardError
	switch {
	case errors.As(err, &guard):
		preview.Refusal = guard.Reason
	case err != nil && placed.Priority == 0:
		return nil, err
	case err != nil:
		preview.Refusal = err.Error()
	}
	preview.Rule = placed
	view, err := s.readRouting(ctx)
	if err != nil {
		return nil, err
	}
	vrfs, read := readVRFs(ctx)
	vrf := !read || len(vrfs) > 0
	candidate := ruleEntryFor(placed)
	for i := range view.Rules {
		e := view.Rules[i]
		if e.Family != placed.Family {
			continue
		}
		if e.Priority <= placed.Priority {
			preview.After = &e
			if coverage, why := covers(e, candidate, view); coverage {
				preview.ShadowedBy = append(preview.ShadowedBy, RuleRelation{Priority: e.Priority, Owner: e.Owner, Reason: why})
			}
		} else {
			if preview.Before == nil {
				preview.Before = &e
			}
			if coverage, why := covers(candidate, e, view); coverage {
				preview.Shadows = append(preview.Shadows, RuleRelation{Priority: e.Priority, Owner: e.Owner, Reason: why})
			}
		}
	}
	now := newModel(view, placed.Family, vrf)
	with := now.withRule(candidate)
	preview.Impact = RouteImpact{
		Family: placed.Family, CheckedAt: preview.CheckedAt, Affected: []ImpactItem{}, Unknown: []ImpactItem{}, Limits: impactLimits,
	}
	for _, c := range s.impactCandidates(ctx, placed.Family, path, extra) {
		b, a := now.send(c.addr, c.mark), with.send(c.addr, c.mark)
		item := ImpactItem{Kind: c.Kind, Name: c.Name, Address: c.Address, Before: describeDecision(b), After: describeDecision(a)}
		switch {
		case b.Status == "unknown" || a.Status == "unknown":
			item.Reason = firstNonEmpty(a.Reason, b.Reason)
			preview.Impact.Unknown = append(preview.Impact.Unknown, item)
			preview.Impact.ClientAffected = preview.Impact.ClientAffected || c.Kind == "client"
		case !sameDecision(b, a):
			preview.Impact.Affected = append(preview.Impact.Affected, item)
			preview.Impact.ClientAffected = preview.Impact.ClientAffected || c.Kind == "client"
		default:
			preview.Impact.Unaffected++
		}
	}
	if probe != nil {
		p, err := s.probeRule(ctx, *probe, placed.Family, now, with)
		if err != nil {
			return nil, err
		}
		preview.Probe = p
	}
	return preview, nil
}

// ruleEntryFor is the inventory entry a managed rule reads back as.
func ruleEntryFor(r RuleSpec) RuleEntry {
	return RuleEntry{
		ID: r.ID, Family: r.Family, Priority: r.Priority, From: r.From, To: r.To, IIF: r.IIF, OIF: r.OIF,
		FWMark: r.FWMark, UIDRange: r.UIDRange, TOS: r.TOS, L3MDev: r.L3MDev, Action: r.Action,
		Table: r.Table, Goto: r.Goto, Owner: "just-dashboard", Managed: true,
	}
}

// probeRule answers the named packet. The kernel is asked only about the
// present, through the same literal-only lookup the route lookup tool uses.
func (s *Service) probeRule(ctx context.Context, probe RouteTuple, family string, now, with modelRouting) (*RuleProbe, error) {
	t, err := probe.parse()
	if err != nil {
		return nil, err
	}
	if t.family != family {
		return nil, fmt.Errorf("the packet's address family must be the rule's")
	}
	out := &RuleProbe{Tuple: probe, Now: now.decide(t), With: with.decide(t)}
	out.Changes = !sameDecision(out.Now, out.With)
	lookup, err := s.lookupTuple(ctx, probe)
	if err != nil {
		out.KernelError = err.Error()
		out.Agreement = "kernel_unavailable"
		return out, nil
	}
	out.Kernel = &lookup
	switch {
	case out.Now.Status == "unknown":
		out.Agreement = "model_unknown"
	case out.Now.Status == "route" && out.Now.Table == lookup.Table && routeUses(out.Now.Route, lookup.Device):
		out.Agreement = "agrees"
	case out.Now.Status == "local" && lookup.Local:
		out.Agreement = "agrees"
	default:
		out.Agreement = "disagrees"
	}
	return out, nil
}

// lookupTuple is LookupRoute with the input device, UID and TOS a rule can
// select on. Every value is parsed before it becomes an argument.
func (s *Service) lookupTuple(ctx context.Context, probe RouteTuple) (RouteLookup, error) {
	t, err := probe.parse()
	if err != nil {
		return RouteLookup{}, err
	}
	args := []string{"-j"}
	if t.family == "inet6" {
		args = append(args, "-6")
	}
	args = append(args, "route", "get", t.dst.String())
	if t.src.IsValid() {
		args = append(args, "from", t.src.String())
	}
	if t.iif != "lo" {
		// A packet arriving on a device needs a source the kernel can
		// validate it against.
		if !t.src.IsValid() {
			return RouteLookup{}, fmt.Errorf("a packet arriving on %s needs a source address", t.iif)
		}
		args = append(args, "iif", t.iif)
	}
	if t.oif != "" {
		args = append(args, "oif", t.oif)
	}
	if t.mark != 0 {
		args = append(args, "mark", fmt.Sprintf("0x%x", t.mark))
	}
	if t.uid != nil {
		args = append(args, "uid", strconv.FormatUint(uint64(*t.uid), 10))
	}
	if t.tosKnown {
		args = append(args, "tos", fmt.Sprintf("0x%02x", t.tos))
	}
	out, err := run(ctx, "ip", args...)
	if err != nil {
		return RouteLookup{}, fmt.Errorf("the kernel could not select a route: %w", err)
	}
	p, err := parseRouteGet(out, Path{Address: t.dst.String()})
	if err != nil {
		return RouteLookup{}, err
	}
	byID, byName := rtTables()
	table, _ := tableOf(ipTable(p.Table), byID, byName)
	if p.Local {
		table = tableLocal
	}
	return RouteLookup{Path: p, Family: t.family, Table: table}, nil
}

// covers reports whether rule a, asked first, decides every packet rule b
// selects, so b never sees them. It answers only from selectors and actions
// it can compare; anything else is not claimed.
func covers(a, b RuleEntry, view *RoutingView) (bool, string) {
	if a.Family != b.Family || a.Not || b.Not || a.L3MDev || a.IPProto != "" || a.SPort != "" || a.DPort != "" || a.SuppressPrefixLength != nil {
		return false, ""
	}
	if !prefixCovers(a.From, b.From) || !prefixCovers(a.To, b.To) ||
		(a.IIF != "" && a.IIF != b.IIF) || (a.OIF != "" && a.OIF != b.OIF) ||
		!markCovers(a.FWMark, b.FWMark) || !uidCovers(a.UIDRange, b.UIDRange) || (a.TOS != "" && !sameDSField(a.TOS, b.TOS)) {
		return false, ""
	}
	switch a.Action {
	case "blackhole", "unreachable", "prohibit":
		return true, fmt.Sprintf("rule %d selects everything rule %d does and discards it (%s)", a.Priority, b.Priority, a.Action)
	case "lookup":
		for _, t := range view.Tables {
			if t.ID != a.Table {
				continue
			}
			for _, r := range t.Routes {
				if r.Family != a.Family || r.Type == "throw" {
					continue
				}
				p, ok := routePrefix(r)
				if ok && prefixCovers(p.String(), firstNonEmpty(b.To, zeroPrefix(a.Family))) {
					return true, fmt.Sprintf("rule %d selects everything rule %d does and table %s holds a route for all of it (%s)", a.Priority, b.Priority, tableLabel(a.Table, a.TableName), r.Destination)
				}
			}
		}
	}
	return false, ""
}

func zeroPrefix(family string) string {
	if family == "inet6" {
		return "::/0"
	}
	return "0.0.0.0/0"
}

// prefixCovers reports whether selector a holds everything selector b does;
// an empty selector is everything.
func prefixCovers(a, b string) bool {
	if a == "" {
		return true
	}
	if b == "" {
		return false
	}
	pa, err1 := ParsePrefix(a)
	pb, err2 := ParsePrefix(b)
	if err1 != nil || err2 != nil {
		return false
	}
	pa, pb = pa.Masked(), pb.Masked()
	return pa.Bits() <= pb.Bits() && pa.Contains(pb.Addr())
}

// markCovers reports whether every mark b selects also passes a: a's mask
// must be within b's and the two agree on a's bits.
func markCovers(a, b string) bool {
	if a == "" {
		return true
	}
	if b == "" {
		return false
	}
	av, am, ok1 := parseMark(a)
	bv, bm, ok2 := parseMark(b)
	return ok1 && ok2 && am&^bm == 0 && (av^bv)&am == 0
}

func parseMark(s string) (value, mask uint64, ok bool) {
	val, rawMask, masked := strings.Cut(s, "/")
	v, err := strconv.ParseUint(val, 0, 32)
	if err != nil {
		return 0, 0, false
	}
	m := uint64(0xffffffff)
	if masked {
		if m, err = strconv.ParseUint(rawMask, 0, 32); err != nil {
			return 0, 0, false
		}
	}
	return v & m, m, true
}

func uidCovers(a, b string) bool {
	if a == "" {
		return true
	}
	if b == "" {
		return false
	}
	alo, ahi, ok1 := parseUIDRange(a)
	blo, bhi, ok2 := parseUIDRange(b)
	return ok1 && ok2 && alo <= blo && ahi >= bhi
}

func parseUIDRange(s string) (uint64, uint64, bool) {
	lo, hi, _ := strings.Cut(s, "-")
	a, err1 := strconv.ParseUint(lo, 10, 32)
	b, err2 := strconv.ParseUint(hi, 10, 32)
	return a, b, err1 == nil && err2 == nil
}
