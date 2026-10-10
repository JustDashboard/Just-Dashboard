package netx

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Egress groups: a policy-selected set of candidate next hops — gateways or
// tunnels this host owns — of which the kernel routes through the members the
// last decision chose.
//
// Every object a group needs is owned and reserved so a decision can be
// written, undone and restored without touching anything else:
//
//   - a member table per member (egressMemberTable) holding one default route
//     through that member, and a rule sending packets carrying the member's
//     mark there. Probes set that mark on their own sockets, so every member
//     is measured through its own path whichever one carries traffic, and a
//     sticky group's pinned connections return to their member by the same
//     rule;
//   - a group table (egressGroupTable) whose default route is the decided
//     member set — a single next hop, or several weighted next hops of one
//     tier;
//   - while the group is enabled, rules that keep the operator addresses it
//     protects on the main table, the selector rule sending the policy's
//     traffic to the group table, and for a whole-host policy the
//     `suppress_prefixlength 0` rule in front of it, so connected networks,
//     Docker, the tailnet and every other specific route keep answering
//     before the group's default does.
//
// A switch is one `ip route replace` of the group table's default route,
// made through Service.commit like every other managed change: journaled,
// independently recoverable, path-guarded, written into the boot files with
// the decided member, and restored at boot.

const (
	// egressSlots bounds groups: each slot owns ten tables, sixteen rule
	// priorities and eight packet marks.
	egressSlots      = 6
	egressMembersMax = 8
	egressProbesMax  = 4
	// egressProtectedMax bounds the operator addresses a group keeps on the
	// main table.
	egressProtectedMax = 4

	egressTableBase    = 7700
	egressTableSpan    = 10
	egressPriorityBase = 19000
	egressPrioritySpan = 16
	// egressMarkMask is the packet-mark space members are told apart in. It
	// stays clear of Tailscale's 0xff0000, the gateway's 0xff000000, and the
	// values wg-quick (0xca6c → 0x4a00) and kube-proxy (0x4000) leave in it:
	// member values run from 0x100 to 0x3000.
	egressMarkMask = 0x7f00

	// Offsets inside a slot's priority block. Member rules come first so a
	// probe or a pinned connection is never caught by the selector.
	egressExclusionOffset = egressMembersMax
	egressSuppressOffset  = egressExclusionOffset + egressProtectedMax
	egressSelectorOffset  = egressSuppressOffset + 1

	egressNFTable = "jd_egress"
	egressFile    = "egress.nft"
)

func egressGroupTable(slot int) int { return egressTableBase + slot*egressTableSpan }

func egressMemberTable(slot, member int) int { return egressGroupTable(slot) + member }

func egressPriority(slot, offset int) int {
	return egressPriorityBase + slot*egressPrioritySpan + offset
}

func egressMark(slot, member int) uint32 {
	return uint32(slot*egressMembersMax+member) << 8
}

// egressReservedTable reports whether a table belongs to the egress slots, so
// a route added by hand cannot land in one.
func egressReservedTable(id int) bool {
	return id >= egressTableBase && id < egressTableBase+egressSlots*egressTableSpan
}

func egressReservedPriority(p int) bool {
	return p >= egressPriorityBase && p < egressPriorityBase+egressSlots*egressPrioritySpan
}

// EgressGroupSpec is one monitored egress group.
type EgressGroupSpec struct {
	ID int `json:"id"`
	// Slot fixes the tables, rule priorities and marks the group owns.
	Slot   int          `json:"slot"`
	Name   string       `json:"name"`
	Family string       `json:"family"`
	Policy EgressPolicy `json:"policy"`
	// Members are the candidate next hops; each member's ID (1–8) is its
	// table, rule and mark within the slot, and survives edits.
	Members    []EgressMember   `json:"members"`
	Probes     []EgressProbe    `json:"probes"`
	Thresholds EgressThresholds `json:"thresholds"`
	// Sticky keeps a connection on the member it started on for as long as
	// that member stays up, so a failback moves new connections only.
	Sticky bool `json:"sticky"`
	// Connections is what happens to the tracked connections of a member the
	// monitor declares down: flush deletes them so clients reconnect at once
	// over the new path; keep leaves them to recover or time out.
	Connections string `json:"connections"`
	// Failback is automatic (after a stable recovery and the hold time) or
	// manual (the operator fails back).
	Failback string `json:"failback"`
	// Protected are operator addresses or networks kept on the main table
	// while the group carries traffic.
	Protected []string `json:"protected,omitempty"`
	// Enabled installs the selector: the group carries its policy's traffic.
	Enabled bool `json:"enabled"`
	// Automation lets the monitor switch. It requires a passed simulation of
	// exactly this configuration (Simulation.Fingerprint).
	Automation bool `json:"automation"`
	// Active are the member IDs the group table routes through: the last
	// decision, restored at boot.
	Active     []int                `json:"active"`
	DecidedAt  time.Time            `json:"decidedAt,omitzero"`
	DecidedBy  string               `json:"decidedBy,omitempty"`
	Simulation *EgressSimulationRef `json:"simulation,omitempty"`
	Made
}

// EgressSimulationRef is the passed simulation that authorised automation.
type EgressSimulationRef struct {
	ID          string    `json:"id"`
	Fingerprint string    `json:"fingerprint"`
	PassedAt    time.Time `json:"passedAt"`
}

// EgressPolicy selects the traffic a group carries.
type EgressPolicy struct {
	// Kind is all (every packet the main table would send to its default
	// route), selector (source and/or destination networks) or rule (a
	// packet mark and/or the interface it arrived on).
	Kind   string `json:"kind"`
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
	IIF    string `json:"iif,omitempty"`
	FWMark string `json:"fwmark,omitempty"`
}

// EgressMember is one candidate next hop.
type EgressMember struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	// Kind is gateway (an address on a device) or tunnel (an owned tunnel
	// device routed without a gateway).
	Kind    string `json:"kind"`
	Gateway string `json:"gateway,omitempty"`
	Device  string `json:"device"`
	// Source is the address probes are sent from; empty takes the device's
	// first global address of the group's family.
	Source string `json:"source,omitempty"`
	// Priority is the member's tier: the lowest tier with a proven member
	// carries traffic. Weight shares a tier between its members.
	Priority int `json:"priority"`
	Weight   int `json:"weight"`
}

// EgressProbe is one literal target each member is measured against.
type EgressProbe struct {
	// Kind is icmp, tcp (a completed handshake) or dns (an answer to Name).
	Kind   string `json:"kind"`
	Target string `json:"target"`
	Port   int    `json:"port,omitempty"`
	Name   string `json:"name,omitempty"`
}

// EgressThresholds are the measurement and hysteresis rules.
type EgressThresholds struct {
	IntervalSeconds int `json:"intervalSeconds"`
	TimeoutMillis   int `json:"timeoutMillis"`
	// LossPercent and LatencyMillis judge a sample: loss over the last
	// Window samples above LossPercent, or a median round trip above
	// LatencyMillis, is a bad sample.
	LossPercent   int `json:"lossPercent"`
	LatencyMillis int `json:"latencyMillis"`
	Window        int `json:"window"`
	// FailAfter consecutive bad samples declare a member down;
	// RecoverAfter consecutive good samples declare it up.
	FailAfter    int `json:"failAfter"`
	RecoverAfter int `json:"recoverAfter"`
	// HoldSeconds is the least time between two switches unless the
	// carrying member is down; StableSeconds is how long a better member
	// must stay up before an automatic failback.
	HoldSeconds   int `json:"holdSeconds"`
	StableSeconds int `json:"stableSeconds"`
}

var defaultEgressThresholds = EgressThresholds{
	IntervalSeconds: 10, TimeoutMillis: 1000, LossPercent: 20, LatencyMillis: 300, Window: 5,
	FailAfter: 3, RecoverAfter: 5, HoldSeconds: 60, StableSeconds: 120,
}

// EgressGroupRequest creates or edits a group.
type EgressGroupRequest struct {
	Name        string           `json:"name"`
	Family      string           `json:"family,omitempty"`
	Policy      EgressPolicy     `json:"policy"`
	Members     []EgressMember   `json:"members"`
	Probes      []EgressProbe    `json:"probes"`
	Thresholds  EgressThresholds `json:"thresholds"`
	Sticky      bool             `json:"sticky"`
	Connections string           `json:"connections,omitempty"`
	Failback    string           `json:"failback,omitempty"`
	Protected   []string         `json:"protected,omitempty"`
}

// config validates a request into a group's configuration, keeping the IDs
// of members the previous configuration already had.
func (req EgressGroupRequest) config(previous *EgressGroupSpec) (EgressGroupSpec, error) {
	g := EgressGroupSpec{}
	var err error
	if g.Name, err = CleanLabel(req.Name, 48); err != nil || g.Name == "" {
		return g, fmt.Errorf("an egress group needs a name of up to 48 characters")
	}
	switch strings.ToLower(strings.TrimSpace(req.Family)) {
	case "", "inet", "ipv4", "4":
		g.Family = "inet"
	case "inet6", "ipv6", "6":
		g.Family = "inet6"
	default:
		return g, fmt.Errorf("an egress group's family is inet or inet6")
	}
	if g.Policy, err = req.Policy.normalize(g.Family); err != nil {
		return g, err
	}
	if len(req.Members) < 2 || len(req.Members) > egressMembersMax {
		return g, fmt.Errorf("an egress group has 2 to %d members: failover needs somewhere to go", egressMembersMax)
	}
	taken := map[int]bool{}
	for _, m := range req.Members {
		if m.ID != 0 {
			if m.ID < 1 || m.ID > egressMembersMax || taken[m.ID] {
				return g, fmt.Errorf("member id %d is not valid", m.ID)
			}
			if previous == nil || previous.member(m.ID) == nil {
				return g, fmt.Errorf("member %d does not exist in this group", m.ID)
			}
			taken[m.ID] = true
		}
	}
	paths := map[string]string{}
	names := map[string]bool{}
	for _, raw := range req.Members {
		m, err := raw.normalize(g.Family)
		if err != nil {
			return g, err
		}
		if m.ID == 0 {
			for id := 1; id <= egressMembersMax; id++ {
				if !taken[id] {
					m.ID, taken[id] = id, true
					break
				}
			}
		}
		if names[m.Name] {
			return g, fmt.Errorf("two members are named %s", m.Name)
		}
		names[m.Name] = true
		key := m.Device + " " + m.Gateway
		if other, dup := paths[key]; dup {
			return g, fmt.Errorf("%s and %s are the same next hop; members must be different paths", other, m.Name)
		}
		paths[key] = m.Name
		g.Members = append(g.Members, m)
	}
	sort.Slice(g.Members, func(i, j int) bool { return g.Members[i].ID < g.Members[j].ID })
	if len(req.Probes) < 1 || len(req.Probes) > egressProbesMax {
		return g, fmt.Errorf("an egress group measures 1 to %d probe targets", egressProbesMax)
	}
	for _, raw := range req.Probes {
		p, err := raw.normalize(g.Family)
		if err != nil {
			return g, err
		}
		g.Probes = append(g.Probes, p)
	}
	if g.Thresholds, err = req.Thresholds.normalize(); err != nil {
		return g, err
	}
	switch c := strings.ToLower(strings.TrimSpace(req.Connections)); c {
	case "", "flush":
		g.Connections = "flush"
	case "keep":
		g.Connections = "keep"
	default:
		return g, fmt.Errorf("connections is flush or keep")
	}
	switch f := strings.ToLower(strings.TrimSpace(req.Failback)); f {
	case "", "automatic":
		g.Failback = "automatic"
	case "manual":
		g.Failback = "manual"
	default:
		return g, fmt.Errorf("failback is automatic or manual")
	}
	g.Sticky = req.Sticky
	if g.Sticky {
		if g.Connections != "flush" {
			return g, fmt.Errorf("a sticky group flushes the connections of a member that goes down; kept, they would stay pinned to a dead path")
		}
		if g.Policy.FWMark != "" {
			return g, fmt.Errorf("a sticky group cannot select by packet mark: the mark that selects the traffic would hide the member's own")
		}
		if g.Policy.IIF == "lo" {
			return g, fmt.Errorf("a sticky group selects forwarded traffic by interface; local traffic is selected by the whole-host or address policies")
		}
	}
	seen := map[string]bool{}
	for _, raw := range req.Protected {
		p, err := ParsePrefix(raw)
		if err != nil {
			return g, err
		}
		p = p.Masked()
		if familyOf(p.Addr()) != g.Family {
			return g, fmt.Errorf("protected %s is not an %s network", p, g.Family)
		}
		if p.Bits() == 0 {
			return g, fmt.Errorf("protecting every address leaves the group nothing to carry")
		}
		if !seen[p.String()] {
			seen[p.String()] = true
			g.Protected = append(g.Protected, p.String())
		}
	}
	if len(g.Protected) > egressProtectedMax {
		return g, fmt.Errorf("a group keeps at most %d operator addresses on the main table", egressProtectedMax)
	}
	return g, nil
}

func (p EgressPolicy) normalize(family string) (EgressPolicy, error) {
	out := EgressPolicy{Kind: strings.ToLower(strings.TrimSpace(p.Kind))}
	prefix := func(raw string) (string, error) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return "", nil
		}
		pr, err := ParsePrefix(raw)
		if err != nil {
			return "", err
		}
		pr = pr.Masked()
		if familyOf(pr.Addr()) != family {
			return "", fmt.Errorf("%s is not an %s network", pr, family)
		}
		if pr.Bits() == 0 {
			return "", fmt.Errorf("a selector of every address is the whole-host policy")
		}
		return pr.String(), nil
	}
	var err error
	switch out.Kind {
	case "all":
		if p.From != "" || p.To != "" || p.IIF != "" || p.FWMark != "" {
			return out, fmt.Errorf("the whole-host policy takes no selector")
		}
	case "selector":
		if out.From, err = prefix(p.From); err != nil {
			return out, err
		}
		if out.To, err = prefix(p.To); err != nil {
			return out, err
		}
		if out.From == "" && out.To == "" {
			return out, fmt.Errorf("an address policy needs a source or a destination network")
		}
	case "rule":
		if iif := strings.TrimSpace(p.IIF); iif != "" {
			if err := ValidIfName(iif); err != nil {
				return out, err
			}
			out.IIF = iif
		}
		if out.FWMark, err = canonicalFWMark(p.FWMark); err != nil {
			return out, err
		}
		if out.FWMark != "" && egressMarkOverlaps(out.FWMark) {
			return out, guarded("Packet marks within 0x7f00 tell egress members apart; select the group's traffic with a mark outside those bits.")
		}
		if out.IIF == "" && out.FWMark == "" {
			return out, fmt.Errorf("a rule policy needs a packet mark or an incoming interface")
		}
	default:
		return out, fmt.Errorf("an egress policy is all, selector or rule")
	}
	return out, nil
}

// egressMarkOverlaps reports whether a policy mark requires bits of the
// member space: such packets would meet a member rule before the selector.
func egressMarkOverlaps(fwmark string) bool {
	val, rawMask, masked := strings.Cut(fwmark, "/")
	v, err := strconv.ParseUint(val, 0, 32)
	if err != nil {
		return true
	}
	mask := uint64(0xffffffff)
	if masked {
		if mask, err = strconv.ParseUint(rawMask, 0, 32); err != nil {
			return true
		}
	}
	return v&mask&egressMarkMask != 0
}

func (m EgressMember) normalize(family string) (EgressMember, error) {
	out := EgressMember{ID: m.ID, Kind: strings.ToLower(strings.TrimSpace(m.Kind))}
	var err error
	if err := ValidIfName(strings.TrimSpace(m.Device)); err != nil {
		return out, fmt.Errorf("member device: %w", err)
	}
	out.Device = strings.TrimSpace(m.Device)
	if out.Name, err = CleanLabel(m.Name, 32); err != nil {
		return out, err
	}
	if out.Name == "" {
		out.Name = out.Device
	}
	switch out.Kind {
	case "gateway":
		gw, err := ParseAddr(m.Gateway)
		if err != nil {
			return out, fmt.Errorf("member %s needs a gateway address: %w", out.Name, err)
		}
		if familyOf(gw) != family {
			return out, fmt.Errorf("the gateway %s is not an %s address", gw, family)
		}
		if gw.IsUnspecified() || gw.IsMulticast() || gw.IsLoopback() {
			return out, fmt.Errorf("%s cannot be a gateway", gw)
		}
		out.Gateway = gw.String()
	case "tunnel":
		if strings.TrimSpace(m.Gateway) != "" {
			return out, fmt.Errorf("a tunnel member is routed through its device without a gateway")
		}
	default:
		return out, fmt.Errorf("a member is a gateway or a tunnel")
	}
	if raw := strings.TrimSpace(m.Source); raw != "" {
		src, err := ParseAddr(raw)
		if err != nil {
			return out, err
		}
		if familyOf(src) != family {
			return out, fmt.Errorf("the source %s is not an %s address", src, family)
		}
		out.Source = src.String()
	}
	out.Priority, out.Weight = m.Priority, m.Weight
	if out.Priority == 0 {
		out.Priority = 1
	}
	if out.Weight == 0 {
		out.Weight = 1
	}
	if out.Priority < 1 || out.Priority > 8 {
		return out, fmt.Errorf("a member's tier is 1 to 8")
	}
	if out.Weight < 1 || out.Weight > 16 {
		return out, fmt.Errorf("a member's weight is 1 to 16")
	}
	return out, nil
}

func (p EgressProbe) normalize(family string) (EgressProbe, error) {
	out := EgressProbe{Kind: strings.ToLower(strings.TrimSpace(p.Kind))}
	target, err := ParseAddr(p.Target)
	if err != nil {
		return out, fmt.Errorf("a probe target is a literal address: %w", err)
	}
	if familyOf(target) != family {
		return out, fmt.Errorf("the probe target %s is not an %s address", target, family)
	}
	if target.IsUnspecified() || target.IsMulticast() || target.IsLoopback() || target.IsLinkLocalUnicast() {
		return out, fmt.Errorf("%s cannot be measured through a gateway", target)
	}
	out.Target = target.String()
	switch out.Kind {
	case "icmp":
		if p.Port != 0 || p.Name != "" {
			return out, fmt.Errorf("an ICMP probe has no port or name")
		}
	case "tcp":
		out.Port = p.Port
		if out.Port == 0 {
			out.Port = 443
		}
		if out.Port < 1 || out.Port > 65535 {
			return out, fmt.Errorf("a TCP probe port is 1 to 65535")
		}
		if p.Name != "" {
			return out, fmt.Errorf("a TCP probe has no name")
		}
	case "dns":
		out.Port = p.Port
		if out.Port == 0 {
			out.Port = 53
		}
		if out.Port < 1 || out.Port > 65535 {
			return out, fmt.Errorf("a DNS probe port is 1 to 65535")
		}
		name := strings.TrimSuffix(strings.TrimSpace(p.Name), ".")
		if name == "" {
			name = "."
		}
		if name != "." && !validProbeName(name) {
			return out, fmt.Errorf("%q is not a DNS name", p.Name)
		}
		out.Name = name
	default:
		return out, fmt.Errorf("a probe is icmp, tcp or dns")
	}
	return out, nil
}

func validProbeName(name string) bool {
	if len(name) > 253 {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
				return false
			}
		}
	}
	return true
}

func (t EgressThresholds) normalize() (EgressThresholds, error) {
	// Omitted thresholds take the defaults; given ones are taken as they
	// are, since zero is a meaningful hold, stability or loss threshold.
	out := t
	if t == (EgressThresholds{}) {
		out = defaultEgressThresholds
	}
	for _, c := range []struct {
		v, min, max int
		what        string
	}{
		{out.IntervalSeconds, 2, 300, "the probe interval is 2 to 300 seconds"},
		{out.TimeoutMillis, 100, 5000, "a probe timeout is 100 to 5000 milliseconds"},
		{out.LossPercent, 0, 99, "the loss threshold is 0 to 99 percent"},
		{out.LatencyMillis, 1, 5000, "the latency threshold is 1 to 5000 milliseconds"},
		{out.Window, 1, 60, "the loss window is 1 to 60 samples"},
		{out.FailAfter, 1, 30, "failing takes 1 to 30 consecutive bad samples"},
		{out.RecoverAfter, 1, 60, "recovering takes 1 to 60 consecutive good samples"},
		{out.HoldSeconds, 0, 3600, "the hold time is 0 to 3600 seconds"},
		{out.StableSeconds, 0, 3600, "the stable recovery time is 0 to 3600 seconds"},
	} {
		if c.v < c.min || c.v > c.max {
			return out, fmt.Errorf("%s", c.what)
		}
	}
	if out.TimeoutMillis >= out.IntervalSeconds*1000 {
		return out, fmt.Errorf("a probe timeout must be shorter than the probe interval")
	}
	if out.LatencyMillis >= out.TimeoutMillis {
		return out, fmt.Errorf("the latency threshold must be below the probe timeout, or a slow answer could never be told from a lost one")
	}
	return out, nil
}

func (g *EgressGroupSpec) member(id int) *EgressMember {
	for i := range g.Members {
		if g.Members[i].ID == id {
			return &g.Members[i]
		}
	}
	return nil
}

// Fingerprint identifies a configuration a simulation describes. Name,
// enablement, automation and decisions are not part of it.
func (g *EgressGroupSpec) Fingerprint() string {
	b, _ := json.Marshal(struct {
		Family      string           `json:"family"`
		Policy      EgressPolicy     `json:"policy"`
		Members     []EgressMember   `json:"members"`
		Probes      []EgressProbe    `json:"probes"`
		Thresholds  EgressThresholds `json:"thresholds"`
		Sticky      bool             `json:"sticky"`
		Connections string           `json:"connections"`
		Failback    string           `json:"failback"`
	}{g.Family, g.Policy, g.Members, g.Probes, g.Thresholds, g.Sticky, g.Connections, g.Failback})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// firstTier is the members of the best tier, the initial decision before
// any evidence: the configuration's own order.
func (g *EgressGroupSpec) firstTier() []int {
	best := 0
	for _, m := range g.Members {
		if best == 0 || m.Priority < best {
			best = m.Priority
		}
	}
	var out []int
	for _, m := range g.Members {
		if m.Priority == best {
			out = append(out, m.ID)
		}
	}
	return out
}

func (g *EgressGroupSpec) tierOf(ids []int) int {
	best := 0
	for _, id := range ids {
		if m := g.member(id); m != nil && (best == 0 || m.Priority < best) {
			best = m.Priority
		}
	}
	return best
}

// egressObject is one kernel object a group owns: its identity, how it is
// added and how it is removed.
type egressObject struct {
	key    string
	family string
	add    []string
	del    []string
	// route objects are replaced in place when they change; a changed rule
	// is removed and added again.
	route bool
	// stage orders application: member tables and their rules first, the
	// group table, then the rules that move traffic.
	stage int
}

func (o egressObject) argv(verb []string) []string {
	return append(append([]string{}, familyArgs(o.family)...), verb...)
}

// egressObjects is every object a group owns in its current state, in
// dependency order. Values come from a validated spec and are parsed again
// here; an entry that does not validate renders nothing.
func egressObjects(g EgressGroupSpec) []egressObject {
	var out []egressObject
	if g.Slot < 0 || g.Slot >= egressSlots {
		return nil
	}
	defaultDest := "default"
	if g.Family == "inet6" {
		defaultDest = "::/0"
	}
	for _, m := range g.Members {
		hop, ok := egressHop(g.Family, m)
		if !ok || m.ID < 1 || m.ID > egressMembersMax {
			continue
		}
		table := strconv.Itoa(egressMemberTable(g.Slot, m.ID))
		out = append(out, egressObject{
			key: "route:" + table, family: g.Family, route: true, stage: 0,
			add: append(append([]string{"route", "replace", defaultDest}, hop...), "table", table),
			del: []string{"route", "del", defaultDest, "table", table},
		})
		rule := []string{"priority", strconv.Itoa(egressPriority(g.Slot, m.ID-1)), "fwmark", fmt.Sprintf("0x%x/0x%x", egressMark(g.Slot, m.ID), egressMarkMask), "lookup", table}
		out = append(out, egressObject{
			key: "rule:" + rule[1], family: g.Family, stage: 1,
			add: append([]string{"rule", "add"}, rule...), del: append([]string{"rule", "del"}, rule...),
		})
	}
	groupTable := strconv.Itoa(egressGroupTable(g.Slot))
	if route, ok := egressGroupRoute(g); ok {
		out = append(out, egressObject{
			key: "route:" + groupTable, family: g.Family, route: true, stage: 2,
			add: append(append([]string{"route", "replace", defaultDest}, route...), "table", groupTable),
			del: []string{"route", "del", defaultDest, "table", groupTable},
		})
	}
	if !g.Enabled {
		return out
	}
	for i, raw := range g.Protected {
		p, err := ParsePrefix(raw)
		if err != nil || familyOf(p.Addr()) != g.Family || i >= egressProtectedMax {
			continue
		}
		rule := []string{"priority", strconv.Itoa(egressPriority(g.Slot, egressExclusionOffset+i)), "to", p.Masked().String(), "lookup", "main"}
		out = append(out, egressObject{
			key: "rule:" + rule[1], family: g.Family, stage: 3,
			add: append([]string{"rule", "add"}, rule...), del: append([]string{"rule", "del"}, rule...),
		})
	}
	if g.Policy.Kind == "all" {
		rule := []string{"priority", strconv.Itoa(egressPriority(g.Slot, egressSuppressOffset)), "lookup", "main", "suppress_prefixlength", "0"}
		out = append(out, egressObject{
			key: "rule:" + rule[1], family: g.Family, stage: 3,
			add: append([]string{"rule", "add"}, rule...), del: append([]string{"rule", "del"}, rule...),
		})
	}
	selector, ok := egressSelector(g)
	if ok {
		rule := append([]string{"priority", strconv.Itoa(egressPriority(g.Slot, egressSelectorOffset))}, selector...)
		rule = append(rule, "lookup", groupTable)
		out = append(out, egressObject{
			key: "rule:" + rule[1], family: g.Family, stage: 4,
			add: append([]string{"rule", "add"}, rule...), del: append([]string{"rule", "del"}, rule...),
		})
	}
	return out
}

// egressHop is the route arguments through one member.
func egressHop(family string, m EgressMember) ([]string, bool) {
	if ValidIfName(m.Device) != nil {
		return nil, false
	}
	switch m.Kind {
	case "gateway":
		gw, err := ParseAddr(m.Gateway)
		if err != nil || familyOf(gw) != family {
			return nil, false
		}
		return []string{"via", gw.String(), "dev", m.Device}, true
	case "tunnel":
		return []string{"dev", m.Device}, true
	}
	return nil, false
}

// egressGroupRoute is the group table's default route through the decided
// members: one next hop, or a weighted multipath route over several.
func egressGroupRoute(g EgressGroupSpec) ([]string, bool) {
	var hops []EgressMember
	for _, id := range g.Active {
		if m := g.member(id); m != nil {
			if _, ok := egressHop(g.Family, *m); ok {
				hops = append(hops, *m)
			}
		}
	}
	switch len(hops) {
	case 0:
		return nil, false
	case 1:
		hop, _ := egressHop(g.Family, hops[0])
		return hop, true
	}
	var out []string
	for _, m := range hops {
		hop, _ := egressHop(g.Family, m)
		out = append(out, "nexthop")
		out = append(out, hop...)
		out = append(out, "weight", strconv.Itoa(m.Weight))
	}
	return out, true
}

// egressSelector is the selector rule's match; the whole-host policy has
// none (its suppress rule is in front of it).
func egressSelector(g EgressGroupSpec) ([]string, bool) {
	var out []string
	switch g.Policy.Kind {
	case "all":
		return out, true
	case "selector":
		for _, f := range []struct{ key, val string }{{"from", g.Policy.From}, {"to", g.Policy.To}} {
			if f.val == "" {
				continue
			}
			p, err := ParsePrefix(f.val)
			if err != nil || familyOf(p.Addr()) != g.Family {
				return nil, false
			}
			out = append(out, f.key, p.Masked().String())
		}
	case "rule":
		if g.Policy.IIF != "" {
			if ValidIfName(g.Policy.IIF) != nil {
				return nil, false
			}
			out = append(out, "iif", g.Policy.IIF)
		}
		if g.Policy.FWMark != "" {
			mark, err := canonicalFWMark(g.Policy.FWMark)
			if err != nil {
				return nil, false
			}
			out = append(out, "fwmark", mark)
		}
	default:
		return nil, false
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// egressBatchLines are a family's lines for its boot batch file.
func egressBatchLines(sp *Spec, family string) []string {
	var out []string
	for _, g := range sp.EgressGroups {
		if g.Family != family {
			continue
		}
		for _, o := range egressObjects(g) {
			// The batch file is started with -6 for IPv6, so the lines carry
			// no family argument of their own.
			out = append(out, strings.Join(o.add, " "))
		}
	}
	return out
}

// egressCommands is what takes the kernel from one group state to another:
// removals of what the next state no longer has (the traffic-moving rules
// first), then additions and in-place route replacements in dependency
// order. A nil state is a group that does not exist.
func egressCommands(from, to *EgressGroupSpec) []recoveryCommand {
	var before, after []egressObject
	if from != nil {
		before = egressObjects(*from)
	}
	if to != nil {
		after = egressObjects(*to)
	}
	next := map[string]egressObject{}
	for _, o := range after {
		next[o.key] = o
	}
	prior := map[string]egressObject{}
	for _, o := range before {
		prior[o.key] = o
	}
	var out []recoveryCommand
	removals := append([]egressObject{}, before...)
	sort.SliceStable(removals, func(i, j int) bool { return removals[i].stage > removals[j].stage })
	for _, o := range removals {
		n, kept := next[o.key]
		if kept && (o.route || strings.Join(n.add, " ") == strings.Join(o.add, " ")) {
			continue
		}
		out = append(out, recoveryCommand{Tool: "ip", Args: o.argv(o.del), AllowGone: true})
	}
	for _, o := range after {
		p, had := prior[o.key]
		if had && strings.Join(p.add, " ") == strings.Join(o.add, " ") {
			continue
		}
		out = append(out, recoveryCommand{Tool: "ip", Args: o.argv(o.add), AllowExists: !o.route})
	}
	return out
}

// egressStickyGroups are the groups whose connections are pinned now.
func egressStickyGroups(sp *Spec) []EgressGroupSpec {
	var out []EgressGroupSpec
	for _, g := range sp.EgressGroups {
		if g.Sticky && g.Enabled {
			out = append(out, g)
		}
	}
	return out
}

// renderEgressNFT is the sticky groups' connection pinning. A new connection
// a sticky group selects is pinned, as it leaves, to the member it leaves
// through (its device, and for a gateway member the next hop); later packets
// of that connection in its original direction carry the member's mark, so
// the member rule keeps them on that member when the group table changes.
// Packets that already carry any mark are never touched: they belong to
// Tailscale, wg-quick or whatever else set it. Declare–delete–define makes
// loading an atomic replace, and with no sticky group the file only removes
// the table.
func renderEgressNFT(sp *Spec) string {
	var b strings.Builder
	b.WriteString(generatedHeader)
	b.WriteString("table inet " + egressNFTable + "\ndelete table inet " + egressNFTable + "\n")
	groups := egressStickyGroups(sp)
	if len(groups) == 0 {
		return b.String()
	}
	mask := fmt.Sprintf("0x%x", egressMarkMask)
	keep := fmt.Sprintf("0x%08x", ^uint32(egressMarkMask))
	fmt.Fprintf(&b, "table inet %s {\n", egressNFTable)
	for _, chain := range []struct{ name, hook string }{
		{"restore_out", "type route hook output priority mangle; policy accept;"},
		{"restore_pre", "type filter hook prerouting priority mangle; policy accept;"},
	} {
		fmt.Fprintf(&b, "\tchain %s {\n\t\t%s\n", chain.name, chain.hook)
		fmt.Fprintf(&b, "\t\tmeta mark 0x0 ct direction original ct mark and %s != 0x0 meta mark set ct mark and %s comment \"egress-restore\"\n\t}\n", mask, mask)
	}
	b.WriteString("\tchain pin {\n\t\ttype filter hook postrouting priority mangle; policy accept;\n")
	// A packet that already carries a mark (a probe bound to its member, a
	// pinned connection, another owner's traffic) is never pinned.
	fmt.Fprintf(&b, "\t\tmeta mark != 0x0 return\n\t\tct mark and %s != 0x0 return\n\t\tct state != new return\n", mask)
	for _, g := range groups {
		proto, addr := "ipv4", "ip"
		if g.Family == "inet6" {
			proto, addr = "ipv6", "ip6"
		}
		var match []string
		match = append(match, "meta nfproto "+proto)
		if g.Policy.Kind == "selector" {
			if g.Policy.From != "" {
				if p, err := ParsePrefix(g.Policy.From); err == nil {
					match = append(match, addr+" saddr "+p.Masked().String())
				}
			}
			if g.Policy.To != "" {
				if p, err := ParsePrefix(g.Policy.To); err == nil {
					match = append(match, addr+" daddr "+p.Masked().String())
				}
			}
		}
		if g.Policy.Kind == "rule" && g.Policy.IIF != "" && ValidIfName(g.Policy.IIF) == nil {
			match = append(match, "iifname \""+g.Policy.IIF+"\"")
		}
		for _, m := range g.Members {
			if _, ok := egressHop(g.Family, m); !ok {
				continue
			}
			line := append(append([]string{}, match...), "oifname \""+m.Device+"\"")
			if m.Kind == "gateway" {
				line = append(line, "rt "+addr+" nexthop "+m.Gateway)
			}
			fmt.Fprintf(&b, "\t\t%s ct mark set ct mark and %s or 0x%x comment \"egress-pin:%d:%d\"\n",
				strings.Join(line, " "), keep, egressMark(g.Slot, m.ID), g.ID, m.ID)
		}
	}
	b.WriteString("\t}\n}\n")
	return b.String()
}

// egressFreeSlot is the first slot no group holds.
func egressFreeSlot(sp *Spec) (int, bool) {
	used := map[int]bool{}
	for _, g := range sp.EgressGroups {
		used[g.Slot] = true
	}
	for slot := 0; slot < egressSlots; slot++ {
		if !used[slot] {
			return slot, true
		}
	}
	return 0, false
}

func (sp *Spec) egressGroup(id int) (*EgressGroupSpec, int) {
	for i := range sp.EgressGroups {
		if sp.EgressGroups[i].ID == id {
			return &sp.EgressGroups[i], i
		}
	}
	return nil, -1
}

// selectsReply reports whether an enabled group's rules could carry the
// replies to an operator connection: locally generated, unmarked, from the
// path's source to the client. A protected network keeps them on main.
func egressSelectsReply(g EgressGroupSpec, path Path) bool {
	if !g.Enabled || path.Address == "" || path.Local {
		return false
	}
	client, err := netip.ParseAddr(path.Address)
	if err != nil || familyOf(client) != g.Family {
		return false
	}
	for _, raw := range g.Protected {
		if p, err := netip.ParsePrefix(raw); err == nil && p.Contains(client) {
			return false
		}
	}
	switch g.Policy.Kind {
	case "all":
		return true
	case "selector":
		if g.Policy.To != "" {
			if p, err := netip.ParsePrefix(g.Policy.To); err != nil || !p.Contains(client) {
				return false
			}
		}
		if g.Policy.From != "" {
			src, err := netip.ParseAddr(path.Source)
			if err != nil {
				return true
			}
			if p, err := netip.ParsePrefix(g.Policy.From); err != nil || !p.Contains(src) {
				return false
			}
		}
		return true
	case "rule":
		// Replies carry no mark; only a rule on the loopback interface
		// (locally generated traffic) selects them.
		return g.Policy.FWMark == "" && g.Policy.IIF == "lo"
	}
	return false
}
