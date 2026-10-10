package netx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The monitor measures every member of every group on the group's own
// interval, through each member's own path, whether or not the member is
// carrying traffic. It keeps a bounded history, applies the hysteresis rules
// and, for a group whose automation is on, makes the switch the rules call
// for through the same guarded path an operator's switch takes. Every state
// change, switch, hold and refusal is recorded with the evidence it was
// decided on.

type egressMonitor struct {
	s     *Service
	store *egressStore
	clock func() time.Time

	mu     sync.Mutex
	groups map[int]*egressGroupState
	// sim is the simulation running now; one at a time.
	sim *egressSimRun

	startOnce sync.Once
	stopOnce  sync.Once
	stop      chan struct{}
	done      chan struct{}
	started   time.Time
}

type egressGroupState struct {
	members map[int]*egressMemberState
	lastRun time.Time
	running bool
	// holdKey and failKey keep a held or failing situation to one record.
	holdKey  string
	failKey  string
	advice   EgressDecision
	sources  map[int]netip.Addr
	verified bool
	runtime  *EgressRuntime
}

func newEgressMonitor(s *Service, store *egressStore) *egressMonitor {
	return &egressMonitor{s: s, store: store, clock: func() time.Time { return time.Now().UTC() }, groups: map[int]*egressGroupState{}, stop: make(chan struct{}), done: make(chan struct{})}
}

func egressMemberKey(g EgressGroupSpec, m EgressMember) string {
	b, _ := json.Marshal(struct {
		M EgressMember
		P []EgressProbe
		T EgressThresholds
		S int
	}{m, g.Probes, g.Thresholds, g.Slot})
	return string(b)
}

// state returns a group's state, starting members whose configuration
// changed afresh and loading their persisted history.
func (m *egressMonitor) state(ctx context.Context, g EgressGroupSpec) *egressGroupState {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.groups[g.ID]
	if !ok {
		st = &egressGroupState{members: map[int]*egressMemberState{}}
		m.groups[g.ID] = st
	}
	keep := map[int]bool{}
	for _, mem := range g.Members {
		keep[mem.ID] = true
		key := egressMemberKey(g, mem)
		if cur, ok := st.members[mem.ID]; ok && cur.key == key {
			continue
		}
		ms := newEgressMemberState(key)
		// A restarted monitor resumes the loss window from the record, but
		// never a member's verdict: state is earned again from fresh samples.
		ms.History = m.store.recentSamples(ctx, g.ID, mem.ID, g.Thresholds.Window)
		st.members[mem.ID] = ms
	}
	for id := range st.members {
		if !keep[id] {
			delete(st.members, id)
		}
	}
	return st
}

func (m *egressMonitor) forget(id int) {
	m.mu.Lock()
	delete(m.groups, id)
	m.mu.Unlock()
}

// noteSwitch clears the recorded hold so the next situation is recorded.
func (m *egressMonitor) noteSwitch(id int) {
	m.mu.Lock()
	if st, ok := m.groups[id]; ok {
		st.holdKey, st.failKey = "", ""
	}
	m.mu.Unlock()
}

// views is what the decision reads of each member now.
func (m *egressMonitor) views(g EgressGroupSpec, now time.Time) map[int]egressMemberView {
	m.mu.Lock()
	defer m.mu.Unlock()
	return egressViews(g, m.groups[g.ID], now)
}

func egressViews(g EgressGroupSpec, st *egressGroupState, now time.Time) map[int]egressMemberView {
	out := map[int]egressMemberView{}
	fresh := 3 * time.Duration(g.Thresholds.IntervalSeconds) * time.Second
	for _, mem := range g.Members {
		v := egressMemberView{State: "unknown"}
		if st != nil {
			if ms, ok := st.members[mem.ID]; ok {
				v.State, v.Since = ms.State, ms.Since
				if last := ms.last(); last != nil {
					v.Fresh = now.Sub(last.At) <= fresh
					v.Why = last.Why
				}
			}
		}
		out[mem.ID] = v
	}
	return out
}

func (m *egressMonitor) start(ctx context.Context) {
	m.startOnce.Do(func() {
		m.mu.Lock()
		m.started = m.clock()
		m.mu.Unlock()
		go m.loop(ctx)
	})
}

// halt stops the loop and waits for its round to end; a monitor that never
// started has nothing to wait for.
func (m *egressMonitor) halt() {
	m.stopOnce.Do(func() { close(m.stop) })
	m.mu.Lock()
	started := !m.started.IsZero()
	m.mu.Unlock()
	if !started {
		return
	}
	select {
	case <-m.done:
	case <-time.After(5 * time.Second):
	}
}

func (m *egressMonitor) loop(ctx context.Context) {
	defer close(m.done)
	m.reconcile(ctx)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	lastPrune := time.Time{}
	// The spec is read again only when the file changes: most hosts have no
	// group, and a tick should cost them a stat.
	var sp *Spec
	var stamp string
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stop:
			return
		case <-ticker.C:
		}
		now := m.clock()
		if now.Sub(lastPrune) > time.Hour {
			m.store.prune(ctx, now)
			lastPrune = now
		}
		info, err := os.Stat(m.s.specPath())
		if err != nil {
			sp, stamp = nil, ""
			continue
		}
		if key := fmt.Sprint(info.ModTime().UnixNano(), info.Size()); key != stamp || sp == nil {
			loaded, err := m.s.loadSpec()
			if err != nil {
				continue
			}
			sp, stamp = loaded, key
		}
		for _, g := range sp.EgressGroups {
			st := m.state(ctx, g)
			m.mu.Lock()
			due := !st.running && now.Sub(st.lastRun) >= time.Duration(g.Thresholds.IntervalSeconds)*time.Second
			if due {
				st.running, st.lastRun = true, now
			}
			m.mu.Unlock()
			if due {
				go func(g EgressGroupSpec) {
					defer func() {
						m.mu.Lock()
						if st, ok := m.groups[g.ID]; ok {
							st.running = false
						}
						m.mu.Unlock()
					}()
					m.round(ctx, g)
				}(g)
			}
		}
	}
}

// reconcile settles what a stopped process left: switches recorded as
// applying are compared with the decided members the spec holds (which the
// recovery journal restored if the change was interrupted), and running
// simulations are marked interrupted after their namespaces are removed.
func (m *egressMonitor) reconcile(ctx context.Context) {
	sp, _ := m.s.loadSpec()
	for _, e := range m.store.applying(ctx) {
		reason := "The backend stopped during this switch; "
		outcome := "interrupted"
		if sp != nil {
			if g, _ := sp.egressGroup(e.GroupID); g != nil {
				switch {
				case equalIDs(g.Active, e.After):
					outcome = "applied"
					reason += "the switch had been saved and is in effect."
				case equalIDs(g.Active, e.Before):
					reason += "the network recovery journal restored the members decided before it."
				default:
					reason += "the group now routes through other members."
				}
			}
		}
		m.store.finish(ctx, e.ID, outcome, reason, nil)
	}
	m.s.sweepEgressSimulations(ctx)
	m.store.interruptRunning(ctx)
	m.settlePending(ctx)
}

// settlePending resolves the changes an operator applied pending
// reconnection confirmation, once their journal says how they ended.
func (m *egressMonitor) settlePending(ctx context.Context) {
	pending := m.store.withOutcome(ctx, "pending")
	if len(pending) == 0 {
		return
	}
	j, _ := readChange(m.s.paths.Dir)
	sp, _ := m.s.loadSpec()
	for _, e := range pending {
		change := ""
		if e.Evidence != nil {
			change = e.Evidence.Change
		}
		if j != nil && j.ID == change {
			switch j.Phase {
			case "confirmed":
				m.store.finish(ctx, e.ID, "applied", "", nil)
			case "recovered":
				m.store.finish(ctx, e.ID, "recovered", "Not confirmed by a reconnection in time; the host restored the previous network.", nil)
			case "degraded":
				m.store.finish(ctx, e.ID, "failed", "Not confirmed in time, and restoring the previous network failed: "+strings.Join(j.RecoveryErrors, "; "), nil)
			}
			continue
		}
		// A later change replaced this one's journal, so it was confirmed or
		// recovered first; a switch's decision tells which.
		outcome, reason := "unknown", "A later change replaced this change's journal before its ending was observed."
		if g, _ := sp.egressGroup(e.GroupID); e.Kind == "switch" && g != nil {
			switch {
			case equalIDs(g.Active, e.After):
				outcome, reason = "applied", ""
			case equalIDs(g.Active, e.Before):
				outcome, reason = "recovered", "Not confirmed in time; the host restored the members decided before it."
			}
		}
		m.store.finish(ctx, e.ID, outcome, reason, nil)
	}
}

// round measures every member of a group once and acts on the result.
func (m *egressMonitor) round(ctx context.Context, g EgressGroupSpec) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(g.Thresholds.IntervalSeconds)*time.Second+30*time.Second)
	defer cancel()
	m.settlePending(ctx)
	now := m.clock()
	sources := m.s.egressSources(ctx, g)
	timeout := time.Duration(g.Thresholds.TimeoutMillis) * time.Millisecond
	samples := map[int]EgressSample{}
	var wg sync.WaitGroup
	var lock sync.Mutex
	for _, mem := range g.Members {
		wg.Add(1)
		go func(mem EgressMember) {
			defer wg.Done()
			s := sampleMember(ctx, m.s.egressMemberPath(g, mem, sources), g.Probes, timeout, now)
			lock.Lock()
			samples[mem.ID] = s
			lock.Unlock()
		}(mem)
	}
	wg.Wait()
	type transition struct {
		id     int
		state  string
		sample EgressSample
	}
	var transitions []transition
	states := map[int]string{}
	m.mu.Lock()
	st := m.groups[g.ID]
	if st == nil {
		m.mu.Unlock()
		return
	}
	st.sources = sources
	for _, mem := range g.Members {
		ms := st.members[mem.ID]
		if ms == nil {
			continue
		}
		changed, judged := ms.observe(samples[mem.ID], g.Thresholds)
		samples[mem.ID] = judged
		states[mem.ID] = ms.State
		if changed != "" {
			transitions = append(transitions, transition{mem.ID, changed, judged})
		}
	}
	verified := st.verified
	st.verified = true
	m.mu.Unlock()
	m.store.saveSamples(ctx, g.ID, samples, states)
	for _, t := range transitions {
		mem := g.member(t.id)
		reason := fmt.Sprintf("%s is up after %d consecutive good samples (median %.0f ms, loss %.0f%%).", mem.Name, g.Thresholds.RecoverAfter, t.sample.LatencyMillis, t.sample.Loss)
		if t.state == "down" {
			reason = fmt.Sprintf("%s is down after %d consecutive bad samples: %s.", mem.Name, g.Thresholds.FailAfter, t.sample.Why)
		}
		m.store.record(ctx, EgressEvent{GroupID: g.ID, At: t.sample.At, Kind: "state", MemberID: t.id, Outcome: "observed", Reason: reason, Actor: "egress monitor",
			Evidence: &EgressEvidence{Members: []EgressMemberEvidence{memberEvidence(t.id, t.state, t.sample.At, t.sample)}}})
		if t.state == "down" && g.Enabled && g.Sticky && g.Connections == "flush" && !slices.Contains(g.Active, t.id) {
			// Its pinned connections would keep routing into a dead path.
			out := &EgressConnections{ByMember: map[string]int{}, Basis: egressBasis(g)}
			m.s.flushEgressConnections(ctx, g, sources, map[int]bool{t.id: true}, egressSpared(g, nil), out)
			if out.Flushed+out.FlushFailed > 0 || out.Error != "" {
				m.store.record(ctx, EgressEvent{GroupID: g.ID, Kind: "connections", MemberID: t.id, Outcome: "applied", Actor: "egress monitor",
					Reason:   fmt.Sprintf("Flushed %d tracked connections of %s, which is down, so none stays routed into a dead path.", out.Flushed, mem.Name),
					Evidence: &EgressEvidence{Connections: out}})
			}
		}
	}
	if !verified {
		m.s.verifyEgressAtStart(ctx, g)
	}
	m.decide(ctx, g)
}

func memberEvidence(id int, state string, since time.Time, s EgressSample) EgressMemberEvidence {
	return EgressMemberEvidence{ID: id, State: state, Since: since, LatencyMillis: s.LatencyMillis, Loss: s.Loss, OK: s.OK, Total: s.Total, Why: s.Why, At: s.At}
}

// decide applies the rules and, when automation allows, acts.
func (m *egressMonitor) decide(ctx context.Context, g EgressGroupSpec) {
	now := m.clock()
	sp, err := m.s.loadSpec()
	if err != nil {
		return
	}
	current, _ := sp.egressGroup(g.ID)
	if current == nil || current.Fingerprint() != g.Fingerprint() {
		return
	}
	g = *current
	m.mu.Lock()
	st := m.groups[g.ID]
	views := egressViews(g, st, now)
	evidence := m.evidenceLocked(g, st)
	d := decideEgress(g, views, now)
	if st != nil {
		st.advice = d
	}
	m.mu.Unlock()
	if !g.Enabled {
		return
	}
	if d.switches() && g.Automation {
		_, err := m.s.switchEgress(ctx, g.ID, d.Target, egressSwitchOptions{Action: d.Action, Reason: d.Reason, Actor: "egress monitor", Automated: true, Fingerprint: g.Fingerprint(), Evidence: evidence})
		if err == nil {
			return
		}
		m.mu.Lock()
		key := d.Action + ":" + idKey(d.Target) + ":" + err.Error()
		repeat := st != nil && st.failKey == key
		if st != nil {
			st.failKey = key
		}
		m.mu.Unlock()
		var recorded egressRecorded
		if repeat || errors.As(err, &recorded) {
			return
		}
		kind := egressFailureKind(err)
		m.store.record(ctx, EgressEvent{GroupID: g.ID, Kind: kind, Action: d.Action, Outcome: kind, Actor: "egress monitor", Before: g.Active, After: d.Target,
			Reason:   d.Reason + " Declined: " + err.Error(),
			Evidence: &EgressEvidence{Members: evidence, RouteBefore: describeDecided(g, g.Active), RouteAfter: describeDecided(g, d.Target)}})
		return
	}
	key := d.key
	reason := d.Reason
	if d.switches() {
		key = "advice:" + d.Action + ":" + idKey(d.Target)
		reason = "Automation is off: the rules call for " + d.Action + " to " + egressNames(g, d.Target) + ". " + d.Reason
	}
	if key == "" {
		m.mu.Lock()
		if st != nil {
			st.holdKey = ""
		}
		m.mu.Unlock()
		return
	}
	m.mu.Lock()
	repeat := st != nil && st.holdKey == key
	if st != nil {
		st.holdKey = key
	}
	m.mu.Unlock()
	if repeat {
		return
	}
	m.store.record(ctx, EgressEvent{GroupID: g.ID, Kind: "hold", Action: d.Action, Outcome: "observed", Reason: reason, Actor: "egress monitor", Before: g.Active, After: d.Target,
		Evidence: &EgressEvidence{Members: evidence, RouteBefore: describeDecided(g, g.Active)}})
}

func (m *egressMonitor) evidenceLocked(g EgressGroupSpec, st *egressGroupState) []EgressMemberEvidence {
	var out []EgressMemberEvidence
	if st == nil {
		return out
	}
	for _, mem := range g.Members {
		ms := st.members[mem.ID]
		if ms == nil {
			continue
		}
		e := EgressMemberEvidence{ID: mem.ID, State: ms.State, Since: ms.Since}
		if last := ms.last(); last != nil {
			e.LatencyMillis, e.Loss, e.OK, e.Total, e.Why, e.At = last.LatencyMillis, last.Loss, last.OK, last.Total, last.Why, last.At
		}
		out = append(out, e)
	}
	return out
}

// EgressRuntime compares the kernel with a group's decided state.
type EgressRuntime struct {
	// Status is verified, drift or unreadable.
	Status    string    `json:"status"`
	Missing   []string  `json:"missing,omitempty"`
	Route     string    `json:"route"`
	CheckedAt time.Time `json:"checkedAt"`
	Error     string    `json:"error,omitempty"`
}

// egressReadback reads a group's objects back from the kernel.
func (s *Service) egressReadback(ctx context.Context, g EgressGroupSpec) EgressRuntime {
	rt := EgressRuntime{Status: "verified", CheckedAt: time.Now().UTC()}
	args := append(append([]string{"-j"}, familyArgs(g.Family)...), "rule", "show")
	out, err := run(ctx, "ip", args...)
	if err != nil {
		rt.Status, rt.Error = "unreadable", err.Error()
		return rt
	}
	rules, err := parseIPRules(out)
	if err != nil {
		rt.Status, rt.Error = "unreadable", err.Error()
		return rt
	}
	have := map[int]ipRule{}
	for _, r := range rules {
		have[r.Priority] = r
	}
	byID, byName := rtTables()
	for _, o := range egressObjects(g) {
		if o.route {
			table, _ := strconv.Atoi(o.key[len("route:"):])
			got, err := readEgressDefault(ctx, g.Family, table)
			if err != nil {
				rt.Status, rt.Error = "unreadable", err.Error()
				return rt
			}
			want := o.add[3 : len(o.add)-2]
			if !sameEgressRoute(got, want) {
				rt.Missing = append(rt.Missing, fmt.Sprintf("table %d default route (holds %s)", table, describeEgressRoute(got)))
			}
			if table == egressGroupTable(g.Slot) {
				rt.Route = describeEgressRoute(got)
			}
			continue
		}
		priority, _ := strconv.Atoi(o.key[len("rule:"):])
		r, ok := have[priority]
		wantTable := o.add[len(o.add)-1]
		if i := slices.Index(o.add, "lookup"); i >= 0 && i+1 < len(o.add) {
			wantTable = o.add[i+1]
		}
		id, _ := tableOf(r.Table, byID, byName)
		wantID, err := strconv.Atoi(wantTable)
		if err != nil {
			wantID = byName[wantTable]
		}
		if !ok || id != wantID {
			rt.Missing = append(rt.Missing, fmt.Sprintf("rule %d", priority))
		}
	}
	if len(rt.Missing) > 0 {
		rt.Status = "drift"
	}
	return rt
}

// verifyEgressAtStart checks the decided state the boot unit restored (or a
// previous process left) and puts back what is missing, through a journaled
// change, recording what it found either way.
func (s *Service) verifyEgressAtStart(ctx context.Context, g EgressGroupSpec) {
	rt := s.egressReadback(ctx, g)
	s.egress.mu.Lock()
	if st := s.egress.groups[g.ID]; st != nil {
		st.runtime = &rt
	}
	s.egress.mu.Unlock()
	views := s.egress.views(g, time.Now())
	var proven, unproven []string
	for _, id := range g.Active {
		if views[id].proven() {
			proven = append(proven, egressMemberName(g, id))
		} else {
			unproven = append(unproven, egressMemberName(g, id)+" ("+views[id].State+")")
		}
	}
	evidence := &EgressEvidence{RouteAfter: rt.Route}
	switch rt.Status {
	case "verified":
		reason := "Checked at start: table " + strconv.Itoa(egressGroupTable(g.Slot)) + " sends its traffic " + rt.Route + ", as decided, with every member table and rule in place."
		if len(unproven) > 0 {
			reason += " Not yet proven by this process's probes: " + strings.Join(unproven, ", ") + "."
		} else if len(proven) > 0 {
			reason += " Probes through " + strings.Join(proven, ", ") + " answered."
		}
		s.egress.store.record(ctx, EgressEvent{GroupID: g.ID, Kind: "verify", Outcome: "observed", Actor: "egress monitor", After: g.Active, Reason: reason, Evidence: evidence})
	case "drift":
		err := s.reassertEgress(ctx, g)
		after := s.egressReadback(ctx, g)
		evidence.RouteAfter = after.Route
		if err != nil || after.Status != "verified" {
			why := strings.Join(after.Missing, ", ")
			if err != nil {
				why = err.Error()
			}
			s.egress.store.record(ctx, EgressEvent{GroupID: g.ID, Kind: "verify", Outcome: "failed", Actor: "egress monitor", After: g.Active,
				Reason: "Checked at start: " + strings.Join(rt.Missing, ", ") + " missing, and putting them back failed: " + why + ".", Evidence: evidence})
			return
		}
		s.egress.store.record(ctx, EgressEvent{GroupID: g.ID, Kind: "verify", Outcome: "applied", Actor: "egress monitor", After: g.Active,
			Reason: "Checked at start: " + strings.Join(rt.Missing, ", ") + " missing (a device may have come up after the boot unit ran); restored the decided route, " + after.Route + ".", Evidence: evidence})
		s.egress.mu.Lock()
		if st := s.egress.groups[g.ID]; st != nil {
			st.runtime = &after
		}
		s.egress.mu.Unlock()
	default:
		s.egress.store.record(ctx, EgressEvent{GroupID: g.ID, Kind: "verify", Outcome: "failed", Actor: "egress monitor", After: g.Active, Reason: "Checked at start: the kernel could not be read (" + rt.Error + ")."})
	}
}

// reassertEgress re-adds a group's objects as the spec decides them; the
// spec does not change, and the journal can take the additions back.
func (s *Service) reassertEgress(ctx context.Context, g EgressGroupSpec) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sp, err := s.loadSpec()
	if err != nil {
		return err
	}
	current, _ := sp.egressGroup(g.ID)
	if current == nil || current.Fingerprint() != g.Fingerprint() || !equalIDs(current.Active, g.Active) || current.Enabled != g.Enabled {
		return fmt.Errorf("the group changed meanwhile")
	}
	add := egressCommands(nil, current)
	ops, anchors, err := s.egressOperators(ctx, *current, "")
	if err != nil {
		return err
	}
	for i := range ops {
		ops[i].carried = false
	}
	nft := renderEgressNFT(sp)
	return s.commit(ctx, sp, step{
		apply: func(ctx context.Context) error {
			if err := s.egressRunCommands(ctx, add); err != nil {
				return err
			}
			if current.Sticky && current.Enabled {
				return loadEgressNFT(ctx, sp, nft)
			}
			return nil
		},
		verify: s.verifyEgressDecision(*current, ops, anchors),
	})
}

// EgressView is the Egress page's reading.
type EgressView struct {
	Groups []EgressGroupView `json:"groups"`
	// Capacity is how many groups the reserved tables allow.
	Capacity   int                     `json:"capacity"`
	Persistent bool                    `json:"persistent"`
	Monitoring time.Time               `json:"monitoring,omitzero"`
	Running    *EgressSimulationStatus `json:"running,omitempty"`
}

// EgressGroupView is one group with its live state.
type EgressGroupView struct {
	EgressGroupSpec
	Fingerprint string             `json:"fingerprint"`
	Table       int                `json:"table"`
	Members     []EgressMemberView `json:"members"`
	Advice      *EgressDecision    `json:"advice,omitempty"`
	Runtime     *EgressRuntime     `json:"runtime,omitempty"`
	Readiness   []EgressFinding    `json:"readiness"`
	Events      []EgressEvent      `json:"events"`
	// LastSimulation is the newest run; AutomationBlock says why automation
	// cannot be turned on, empty when it can.
	LastSimulation  *EgressSimulation `json:"lastSimulation,omitempty"`
	AutomationBlock string            `json:"automationBlock,omitempty"`
}

// EgressMemberView is one member with its live state.
type EgressMemberView struct {
	EgressMember
	Table int    `json:"table"`
	Mark  string `json:"mark"`
	// ProbeSource is the address probes are sent from now: the configured
	// source, or the device's own.
	ProbeSource string `json:"probeSource,omitempty"`
	// State is unknown, up or down; Proven is up with fresh evidence.
	State   string        `json:"state"`
	Since   time.Time     `json:"since,omitzero"`
	Proven  bool          `json:"proven"`
	Active  bool          `json:"active"`
	Good    int           `json:"good"`
	Bad     int           `json:"bad"`
	Last    *EgressSample `json:"last,omitempty"`
	History []EgressPoint `json:"history"`
}

// EgressPoint is one sample in a member's history strip.
type EgressPoint struct {
	At            time.Time `json:"at"`
	LatencyMillis float64   `json:"latencyMillis,omitempty"`
	Loss          float64   `json:"loss"`
	Good          bool      `json:"good"`
}

// EgressFinding is one readiness check of a group's paths.
type EgressFinding struct {
	// Level is ok, warning or error.
	Level  string `json:"level"`
	Member int    `json:"member,omitempty"`
	Text   string `json:"text"`
}

// Egress reads every group with its members' live state, its decision
// record and readiness.
func (s *Service) Egress(ctx context.Context) (*EgressView, error) {
	sp, err := s.loadSpec()
	if err != nil {
		return nil, err
	}
	s.egress.mu.Lock()
	monitoring := s.egress.started
	s.egress.mu.Unlock()
	view := &EgressView{Groups: []EgressGroupView{}, Capacity: egressSlots, Persistent: s.egress.store.persistent(), Monitoring: monitoring}
	now := time.Now()
	for _, g := range sp.EgressGroups {
		gv := EgressGroupView{EgressGroupSpec: g, Fingerprint: g.Fingerprint(), Table: egressGroupTable(g.Slot), Members: []EgressMemberView{}, Events: []EgressEvent{}}
		s.egress.mu.Lock()
		st := s.egress.groups[g.ID]
		views := egressViews(g, st, now)
		var sources map[int]netip.Addr
		if st != nil {
			sources = st.sources
			if st.advice.Action != "" {
				advice := st.advice
				gv.Advice = &advice
			}
		}
		for _, mem := range g.Members {
			mv := EgressMemberView{EgressMember: mem, Table: egressMemberTable(g.Slot, mem.ID), Mark: fmt.Sprintf("0x%x/0x%x", egressMark(g.Slot, mem.ID), egressMarkMask),
				State: views[mem.ID].State, Since: views[mem.ID].Since, Proven: views[mem.ID].proven(), Active: slices.Contains(g.Active, mem.ID), History: []EgressPoint{}}
			if src, ok := sources[mem.ID]; ok {
				mv.ProbeSource = src.String()
			}
			if st != nil {
				if ms := st.members[mem.ID]; ms != nil {
					mv.Good, mv.Bad = ms.Good, ms.Bad
					if last := ms.last(); last != nil {
						copy := *last
						mv.Last = &copy
					}
					start := len(ms.History) - 60
					if start < 0 {
						start = 0
					}
					for _, h := range ms.History[start:] {
						mv.History = append(mv.History, EgressPoint{At: h.At, LatencyMillis: h.LatencyMillis, Loss: h.Loss, Good: h.Good})
					}
				}
			}
			gv.Members = append(gv.Members, mv)
		}
		s.egress.mu.Unlock()
		rt := s.egressReadback(ctx, g)
		gv.Runtime = &rt
		gv.Readiness = s.egressReadiness(ctx, sp, g)
		if events, err := s.egress.store.list(ctx, g.ID, 30); err == nil {
			gv.Events = events
		}
		if sims := s.egress.store.simulationsFor(ctx, g.ID); len(sims) > 0 {
			gv.LastSimulation = &sims[0]
		}
		gv.AutomationBlock = egressAutomationBlock(g, s.passedSimulation(ctx, g))
		view.Groups = append(view.Groups, gv)
	}
	view.Running = s.egress.running()
	return view, nil
}

// EgressEvents is a group's decision record, newest first.
func (s *Service) EgressEvents(ctx context.Context, id, limit int) ([]EgressEvent, error) {
	sp, err := s.loadSpec()
	if err != nil {
		return nil, err
	}
	if g, _ := sp.egressGroup(id); g == nil {
		events, err := s.egress.store.list(ctx, id, limit)
		if err == nil && len(events) > 0 {
			return events, nil
		}
		return nil, fmt.Errorf("egress group %d: %w", id, ErrNotFound)
	}
	return s.egress.store.list(ctx, id, limit)
}

// egressReadiness checks what this host can establish about a group's paths
// being separate and usable, without sending anything.
func (s *Service) egressReadiness(ctx context.Context, sp *Spec, g EgressGroupSpec) []EgressFinding {
	out := []EgressFinding{}
	devices := map[string][]string{}
	for _, m := range g.Members {
		devices[m.Device] = append(devices[m.Device], m.Name)
	}
	for dev, names := range devices {
		if len(names) > 1 {
			sort.Strings(names)
			out = append(out, EgressFinding{Level: "warning", Text: strings.Join(names, " and ") + " leave through the same device, " + dev + "; they are not independent if that device or its upstream fails."})
		}
	}
	links, _ := run(ctx, "ip", "-j", "link", "show")
	var rows []struct {
		Name  string   `json:"ifname"`
		Flags []string `json:"flags"`
	}
	_ = json.Unmarshal([]byte(links), &rows)
	up := map[string]bool{}
	present := map[string]bool{}
	for _, r := range rows {
		present[r.Name] = true
		up[r.Name] = slices.Contains(r.Flags, "UP")
	}
	for _, m := range g.Members {
		switch {
		case !present[m.Device]:
			out = append(out, EgressFinding{Level: "error", Member: m.ID, Text: m.Name + ": " + m.Device + " does not exist now."})
			continue
		case !up[m.Device]:
			out = append(out, EgressFinding{Level: "error", Member: m.ID, Text: m.Name + ": " + m.Device + " is down."})
		}
		if m.Kind == "gateway" {
			args := append(append([]string{"-j"}, familyArgs(g.Family)...), "route", "get", m.Gateway, "oif", m.Device)
			raw, err := run(ctx, "ip", args...)
			p, perr := parseRouteGet(raw, Path{Address: m.Gateway})
			if err != nil || perr != nil || p.Device != m.Device {
				out = append(out, EgressFinding{Level: "error", Member: m.ID, Text: m.Name + ": the gateway " + m.Gateway + " is not reachable on " + m.Device + "."})
			} else if p.Gateway != "" {
				out = append(out, EgressFinding{Level: "warning", Member: m.ID, Text: m.Name + ": the gateway " + m.Gateway + " is itself reached through " + p.Gateway + ", not on " + m.Device + "'s own network."})
			}
		}
		if g.Family == "inet" {
			if v, err := run(ctx, "sysctl", "-n", "net.ipv4.conf."+m.Device+".rp_filter"); err == nil {
				all, _ := run(ctx, "sysctl", "-n", "net.ipv4.conf.all.rp_filter")
				if strings.TrimSpace(v) == "1" || strings.TrimSpace(all) == "1" && strings.TrimSpace(v) != "2" {
					out = append(out, EgressFinding{Level: "warning", Member: m.ID, Text: m.Name + ": strict reverse-path filtering on " + m.Device + " drops replies that arrive on a device the main table would not use; set it to loose (2) for policy routing."})
				}
			}
		}
	}
	if g.Policy.Kind == "selector" && g.Policy.From != "" {
		if from, err := netip.ParsePrefix(g.Policy.From); err == nil && (from.Addr().IsPrivate() || from.Addr().Is4() && netip.MustParsePrefix("100.64.0.0/10").Contains(from.Addr())) {
			for _, m := range g.Members {
				covered := false
				for _, n := range sp.NAT {
					if !n.Enabled || n.Interface != m.Device {
						continue
					}
					if src, err := netip.ParsePrefix(n.Source); err == nil && src.Bits() <= from.Bits() && src.Contains(from.Addr()) {
						covered = true
					}
				}
				if !covered {
					out = append(out, EgressFinding{Level: "warning", Member: m.ID, Text: m.Name + ": traffic from " + from.String() + " leaving through " + m.Device + " is not translated by a NAT entry here; replies to a private source will not find their way back."})
				}
			}
		}
	}
	if len(out) == 0 {
		out = append(out, EgressFinding{Level: "ok", Text: "Every member's device is up and each gateway is on its device's own network. Provider routing beyond this host is not visible from here."})
	}
	return out
}
