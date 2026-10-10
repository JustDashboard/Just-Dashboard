package netx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"golang.org/x/net/dns/dnsmessage"
)

// Automation stays off until the operator has watched the monitor decide in
// a simulation. The simulation models the group's members in disposable
// network namespaces this process creates and removes: a namespace for the
// host with the group's own tables and rules, one per member standing for
// its gateway (which, like a real upstream, drops sources not its own), and
// one for the internet holding the probe targets. It then injects latency,
// failure, flapping and a total outage with tc netem on the members' links,
// and runs the real probes and the real decision rules through each phase,
// with time standing still between samples so hold and stability windows
// pass at the group's own interval. Nothing on the host's own interfaces,
// routes, rules, queues or firewall is touched.

const (
	egressSimFile     = "egress-sim.json"
	egressSimMaxSteps = 160
	egressSimTimeout  = 15 * time.Minute
)

// EgressSimulation is one run.
type EgressSimulation struct {
	ID          string `json:"id"`
	GroupID     int    `json:"groupId"`
	Fingerprint string `json:"fingerprint"`
	// Status is running, passed, failed (an expectation did not hold),
	// error (the run could not complete) or interrupted.
	Status     string                  `json:"status"`
	Actor      string                  `json:"actor,omitempty"`
	StartedAt  time.Time               `json:"startedAt"`
	FinishedAt time.Time               `json:"finishedAt,omitzero"`
	Result     *EgressSimulationResult `json:"result,omitempty"`
	Error      string                  `json:"error,omitempty"`
}

// EgressSimulationResult is what the monitor decided, step by step.
type EgressSimulationResult struct {
	Phases       []EgressSimPhase    `json:"phases"`
	Steps        []EgressSimStep     `json:"steps"`
	Expectations []EgressExpectation `json:"expectations"`
	Topology     []string            `json:"topology"`
	Limits       []string            `json:"limits"`
	Cleanup      string              `json:"cleanup"`
	// IntervalSeconds is the virtual time between steps.
	IntervalSeconds int `json:"intervalSeconds"`
}

// EgressSimPhase is one stretch of injected conditions.
type EgressSimPhase struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Start       int    `json:"start"`
	End         int    `json:"end"`
	// Faults are the netem conditions per member id.
	Faults map[string]string `json:"faults"`
}

// EgressSimStep is one sample of every member and the decision on it.
type EgressSimStep struct {
	Index    int               `json:"index"`
	Phase    string            `json:"phase"`
	Seconds  int               `json:"seconds"`
	Members  []EgressSimMember `json:"members"`
	Active   []int             `json:"active"`
	Decision EgressDecision    `json:"decision"`
	// Switched is a decision made real in the simulated host's group table;
	// DataPath is whether unmarked traffic through that table reached the
	// first target afterwards.
	Switched bool   `json:"switched,omitempty"`
	DataPath string `json:"dataPath"`
}

// EgressSimMember is one member at one step.
type EgressSimMember struct {
	ID            int     `json:"id"`
	State         string  `json:"state"`
	Good          bool    `json:"good"`
	LatencyMillis float64 `json:"latencyMillis,omitempty"`
	Loss          float64 `json:"loss"`
	OK            int     `json:"ok"`
	Total         int     `json:"total"`
	Fault         string  `json:"fault,omitempty"`
}

// EgressExpectation is one behaviour the run checked.
type EgressExpectation struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail"`
}

// EgressSimulationStatus is a running simulation's progress.
type EgressSimulationStatus struct {
	ID        string    `json:"id"`
	GroupID   int       `json:"groupId"`
	Step      int       `json:"step"`
	Steps     int       `json:"steps"`
	Phase     string    `json:"phase"`
	StartedAt time.Time `json:"startedAt"`
}

type egressSimRun struct {
	mu     sync.Mutex
	status EgressSimulationStatus
	cancel context.CancelFunc
}

func (m *egressMonitor) running() *EgressSimulationStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sim == nil {
		return nil
	}
	m.sim.mu.Lock()
	defer m.sim.mu.Unlock()
	status := m.sim.status
	return &status
}

func sortSimulations(sims []EgressSimulation) {
	sort.Slice(sims, func(i, j int) bool { return sims[i].StartedAt.After(sims[j].StartedAt) })
}

// egressSimPlan is the scripted run for a group.
type egressSimPlan struct {
	phases  []egressSimPhasePlan
	primary int
}

type egressSimPhasePlan struct {
	name, description string
	steps             int
	// fault returns the netem arguments for a member at a step of the
	// phase, nil for a clean link.
	fault func(member, step int) []string
}

func netemDelay(ms int) []string { return []string{"delay", strconv.Itoa(ms) + "ms"} }

var netemLoss = []string{"loss", "100%"}

// planEgressSimulation derives the phases from the group's own rules, so
// every window is long enough for the behaviour it shows.
func planEgressSimulation(g EgressGroupSpec) egressSimPlan {
	t := g.Thresholds
	primary := 0
	for _, id := range g.firstTier() {
		if primary == 0 || id < primary {
			primary = id
		}
	}
	stable := int(math.Ceil(float64(t.StableSeconds) / float64(t.IntervalSeconds)))
	hold := int(math.Ceil(float64(t.HoldSeconds) / float64(t.IntervalSeconds)))
	tolerable := t.LatencyMillis * 4 / 10
	breach := t.LatencyMillis * 3 / 2
	if breach > t.TimeoutMillis*3/2 {
		breach = t.TimeoutMillis * 3 / 2
	}
	onPrimary := func(args []string) func(int, int) []string {
		return func(member, _ int) []string {
			if member == primary {
				return args
			}
			return nil
		}
	}
	// A clean sample is judged good only once the loss window has cleared
	// of the failures before it, so every recovery waits for the window too.
	flap := max(2*t.RecoverAfter+2, 6)
	recovery := t.Window + t.RecoverAfter + max(stable, hold) + 3
	p := egressSimPlan{primary: primary}
	p.phases = []egressSimPhasePlan{
		{"warmup", "Every link clean, until every member has earned its first verdict.", t.RecoverAfter + 1, func(int, int) []string { return nil }},
		{"tolerable-latency", fmt.Sprintf("%s delayed by %d ms, under the %d ms threshold.", egressMemberName(g, primary), tolerable, t.LatencyMillis), t.FailAfter + 1, onPrimary(netemDelay(tolerable))},
		{"failure", egressMemberName(g, primary) + " drops every packet.", t.FailAfter + 2, onPrimary(netemLoss)},
		{"flap", egressMemberName(g, primary) + " alternates between dropping everything and a clean link, sample by sample.", flap, func(member, step int) []string {
			if member == primary && step%2 == 0 {
				return netemLoss
			}
			return nil
		}},
		{"recovery", egressMemberName(g, primary) + " clean again, long enough for its recovery, the stable window and the hold time.", recovery, func(int, int) []string { return nil }},
		{"latency-breach", fmt.Sprintf("%s delayed by %d ms, over the %d ms threshold.", egressMemberName(g, primary), breach, t.LatencyMillis), t.FailAfter + 2, onPrimary(netemDelay(breach))},
		{"outage", "Every member drops every packet.", t.FailAfter + 2, func(int, int) []string { return netemLoss }},
		{"restore", "Every link clean again.", t.Window + t.RecoverAfter + 1, func(int, int) []string { return nil }},
	}
	return p
}

func (p egressSimPlan) steps() int {
	n := 0
	for _, ph := range p.phases {
		n += ph.steps
	}
	return n
}

// SimulateEgress starts a simulation of a group's current configuration.
func (s *Service) SimulateEgress(ctx context.Context, id int, actor string) (*EgressSimulation, error) {
	sp, err := s.loadSpec()
	if err != nil {
		return nil, err
	}
	g, _ := sp.egressGroup(id)
	if g == nil {
		return nil, fmt.Errorf("egress group %d: %w", id, ErrNotFound)
	}
	for _, tool := range []string{"ip", "tc"} {
		if !has(tool) {
			return nil, &UnavailableError{Tool: tool, Package: map[string]string{"ip": "iproute2", "tc": "iproute2"}[tool]}
		}
	}
	plan := planEgressSimulation(*g)
	if plan.steps() > egressSimMaxSteps {
		return nil, fmt.Errorf("this configuration's windows need %d simulated samples, more than the %d a run may take; shorten the stable or hold time", plan.steps(), egressSimMaxSteps)
	}
	var raw [6]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, err
	}
	sim := EgressSimulation{ID: hex.EncodeToString(raw[:]), GroupID: id, Fingerprint: g.Fingerprint(), Status: "running", Actor: actor, StartedAt: time.Now().UTC()}
	m := s.egress
	m.mu.Lock()
	if m.sim != nil {
		m.mu.Unlock()
		return nil, &ConfirmationError{"Another egress simulation is running; wait for it to finish."}
	}
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), egressSimTimeout)
	run := &egressSimRun{cancel: cancel, status: EgressSimulationStatus{ID: sim.ID, GroupID: id, Steps: plan.steps(), Phase: "building", StartedAt: sim.StartedAt}}
	m.sim = run
	m.mu.Unlock()
	m.store.saveSimulation(ctx, sim)
	m.store.record(ctx, EgressEvent{GroupID: id, Kind: "simulation", Outcome: "applying", Actor: actor, Reason: "Simulation " + sim.ID + " started in disposable namespaces.", Evidence: &EgressEvidence{Fingerprint: sim.Fingerprint}})
	go func() {
		defer cancel()
		result, err := s.runEgressSimulation(runCtx, *g, sim.ID[:4], plan, run)
		sim.FinishedAt = time.Now().UTC()
		sim.Result = result
		switch {
		case err != nil:
			sim.Status, sim.Error = "error", err.Error()
		case egressExpectationsPass(result.Expectations):
			sim.Status = "passed"
		default:
			sim.Status = "failed"
		}
		m.store.saveSimulation(context.Background(), sim)
		reason := "Simulation " + sim.ID + " "
		switch sim.Status {
		case "passed":
			reason += "passed: every expectation held, so automation may be turned on for this configuration."
		case "failed":
			var failed []string
			for _, e := range result.Expectations {
				if !e.Passed {
					failed = append(failed, e.Name)
				}
			}
			reason += "failed: " + strings.Join(failed, ", ") + "."
		default:
			reason += "could not complete: " + sim.Error
		}
		m.store.record(context.Background(), EgressEvent{GroupID: id, Kind: "simulation", Outcome: map[string]string{"passed": "applied", "failed": "failed", "error": "failed"}[sim.Status], Actor: actor, Reason: reason, Evidence: &EgressEvidence{Fingerprint: sim.Fingerprint}})
		m.mu.Lock()
		m.sim = nil
		m.mu.Unlock()
	}()
	return &sim, nil
}

// EgressSimulations is a group's runs, newest first.
func (s *Service) EgressSimulations(ctx context.Context, id int) []EgressSimulation {
	sims := s.egress.store.simulationsFor(ctx, id)
	if sims == nil {
		sims = []EgressSimulation{}
	}
	return sims
}

// EgressSimulationByID is one run.
func (s *Service) EgressSimulationByID(ctx context.Context, id string) (*EgressSimulation, error) {
	sim, ok := s.egress.store.simulation(ctx, id)
	if !ok {
		return nil, fmt.Errorf("simulation %s: %w", id, ErrNotFound)
	}
	return &sim, nil
}

// passedSimulation is the newest passed run of a configuration.
func (s *Service) passedSimulation(ctx context.Context, g EgressGroupSpec) *EgressSimulation {
	for _, sim := range s.egress.store.simulationsFor(ctx, g.ID) {
		if sim.Status == "passed" && sim.Fingerprint == g.Fingerprint() {
			return &sim
		}
	}
	return nil
}

func egressExpectationsPass(es []EgressExpectation) bool {
	if len(es) == 0 {
		return false
	}
	for _, e := range es {
		if !e.Passed {
			return false
		}
	}
	return true
}

// egressSimNamespaces names a run's namespaces.
type egressSimNamespaces struct {
	Host     string         `json:"host"`
	Internet string         `json:"internet"`
	Members  map[int]string `json:"members"`
}

func (n egressSimNamespaces) all() []string {
	out := []string{n.Host, n.Internet}
	ids := make([]int, 0, len(n.Members))
	for id := range n.Members {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		out = append(out, n.Members[id])
	}
	return out
}

func (s *Service) egressNetnsPath(name string) string {
	if s.egressNetnsRoot != "" {
		return filepath.Join(s.egressNetnsRoot, name)
	}
	return hostexec.HostPath(filepath.Join("/run/netns", name))
}

// sweepEgressSimulations removes the namespaces a stopped process recorded
// before creating them, and nothing else.
func (s *Service) sweepEgressSimulations(ctx context.Context) {
	path := filepath.Join(s.paths.Dir, egressSimFile)
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	var names egressSimNamespaces
	if err == nil && json.Unmarshal(b, &names) == nil {
		for _, ns := range names.all() {
			if ValidNamespace(ns) == nil && strings.HasPrefix(ns, "jds") {
				if _, err := run(ctx, "ip", "netns", "del", ns); err != nil && !isGone(err) && !strings.Contains(err.Error(), "No such file") {
					s.log.Warn("network: removing a leftover egress simulation namespace", "namespace", ns, "err", err)
					continue
				}
			}
		}
	}
	_ = os.Remove(path)
}

// egressSimAddressing is the simulated topology's addresses for a member.
type egressSimAddressing struct {
	host, gateway, upstream, internet netip.Addr
	hostNet, upstreamNet              netip.Prefix
}

func egressSimAddrs(family string, id int) egressSimAddressing {
	if family == "inet6" {
		return egressSimAddressing{
			host: netip.MustParseAddr(fmt.Sprintf("fd5e:a:%x::2", id)), gateway: netip.MustParseAddr(fmt.Sprintf("fd5e:a:%x::1", id)),
			upstream: netip.MustParseAddr(fmt.Sprintf("fd5e:b:%x::1", id)), internet: netip.MustParseAddr(fmt.Sprintf("fd5e:b:%x::2", id)),
			hostNet: netip.MustParsePrefix(fmt.Sprintf("fd5e:a:%x::/64", id)), upstreamNet: netip.MustParsePrefix(fmt.Sprintf("fd5e:b:%x::/64", id)),
		}
	}
	return egressSimAddressing{
		host: netip.AddrFrom4([4]byte{198, 18, byte(id), 2}), gateway: netip.AddrFrom4([4]byte{198, 18, byte(id), 1}),
		upstream: netip.AddrFrom4([4]byte{198, 19, byte(id), 1}), internet: netip.AddrFrom4([4]byte{198, 19, byte(id), 2}),
		hostNet: netip.PrefixFrom(netip.AddrFrom4([4]byte{198, 18, byte(id), 0}), 30), upstreamNet: netip.PrefixFrom(netip.AddrFrom4([4]byte{198, 19, byte(id), 0}), 30),
	}
}

func egressSimAddrArgs(family string, prefix netip.Prefix, addr netip.Addr, dev string) []string {
	args := []string{"addr", "add", netip.PrefixFrom(addr, prefix.Bits()).String(), "dev", dev}
	if family == "inet6" {
		args = append(args, "nodad")
	}
	return args
}

// runEgressSimulation builds, drives and removes the disposable topology.
func (s *Service) runEgressSimulation(ctx context.Context, g EgressGroupSpec, tag string, plan egressSimPlan, progress *egressSimRun) (*EgressSimulationResult, error) {
	result := &EgressSimulationResult{IntervalSeconds: g.Thresholds.IntervalSeconds, Phases: []EgressSimPhase{}, Steps: []EgressSimStep{}, Expectations: []EgressExpectation{}}
	names := egressSimNamespaces{Host: "jds" + tag + "h", Internet: "jds" + tag + "n", Members: map[int]string{}}
	for _, m := range g.Members {
		names.Members[m.ID] = "jds" + tag + "g" + strconv.Itoa(m.ID)
	}
	record, _ := json.Marshal(names)
	if err := writeFileAtomic(filepath.Join(s.paths.Dir, egressSimFile), record, 0o600); err != nil {
		return result, fmt.Errorf("recording the simulation's namespaces before creating them: %w", err)
	}
	var listeners []func()
	defer func() {
		for _, stop := range listeners {
			stop()
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		var failed []string
		for _, ns := range names.all() {
			if _, err := run(cleanup, "ip", "netns", "del", ns); err != nil && !isGone(err) && !strings.Contains(err.Error(), "No such file") {
				failed = append(failed, ns+": "+err.Error())
			}
		}
		if len(failed) == 0 {
			result.Cleanup = "Every simulation namespace, with its devices and queues, was removed."
			_ = os.Remove(filepath.Join(s.paths.Dir, egressSimFile))
		} else {
			result.Cleanup = "Removing " + strings.Join(failed, "; ") + " failed; the next start retries."
		}
	}()
	ip := func(args ...string) error {
		_, err := run(ctx, "ip", args...)
		if err != nil {
			return fmt.Errorf("building the simulation (ip %s): %w", strings.Join(args, " "), err)
		}
		return nil
	}
	inNS := func(ns string, args ...string) error {
		return ip(append([]string{"netns", "exec", ns}, args...)...)
	}
	family := g.Family
	fam := familyArgs(family)
	for _, ns := range names.all() {
		if err := ip("netns", "add", ns); err != nil {
			return result, err
		}
		if err := ip("-n", ns, "link", "set", "lo", "up"); err != nil {
			return result, err
		}
	}
	forward := "net.ipv4.ip_forward=1"
	if family == "inet6" {
		forward = "net.ipv6.conf.all.forwarding=1"
	}
	simGroup := g
	simGroup.Members = nil
	simGroup.Enabled, simGroup.Automation, simGroup.Protected = true, true, nil
	simGroup.Policy = EgressPolicy{Kind: "all"}
	result.Topology = append(result.Topology, fmt.Sprintf("%s models this host with the group's own tables %d–%d, rules from priority %d and marks 0x%x–0x%x, routing all of its own traffic through the group so the data path can be checked.",
		names.Host, egressGroupTable(g.Slot), egressMemberTable(g.Slot, egressMembersMax), egressPriority(g.Slot, 0), egressMark(g.Slot, 1), egressMark(g.Slot, egressMembersMax)))
	spoofing := has("nft")
	for _, m := range g.Members {
		a := egressSimAddrs(family, m.ID)
		host, up, down, edge := "m"+strconv.Itoa(m.ID), "u"+strconv.Itoa(m.ID), "d"+strconv.Itoa(m.ID), "i"+strconv.Itoa(m.ID)
		gw := names.Members[m.ID]
		steps := [][]string{
			{"-n", names.Host, "link", "add", host, "type", "veth", "peer", "name", up, "netns", gw},
			{"-n", gw, "link", "add", down, "type", "veth", "peer", "name", edge, "netns", names.Internet},
			append([]string{"-n", names.Host}, egressSimAddrArgs(family, a.hostNet, a.host, host)...),
			append([]string{"-n", gw}, egressSimAddrArgs(family, a.hostNet, a.gateway, up)...),
			append([]string{"-n", gw}, egressSimAddrArgs(family, a.upstreamNet, a.upstream, down)...),
			append([]string{"-n", names.Internet}, egressSimAddrArgs(family, a.upstreamNet, a.internet, edge)...),
			{"-n", names.Host, "link", "set", host, "up"},
			{"-n", gw, "link", "set", up, "up"},
			{"-n", gw, "link", "set", down, "up"},
			{"-n", names.Internet, "link", "set", edge, "up"},
			append(append([]string{"-n", gw}, fam...), "route", "add", map[string]string{"inet": "default", "inet6": "::/0"}[family], "via", a.internet.String()),
			append(append([]string{"-n", names.Internet}, fam...), "route", "add", a.hostNet.String(), "via", a.upstream.String()),
		}
		for _, args := range steps {
			if err := ip(args...); err != nil {
				return result, err
			}
		}
		if err := inNS(gw, "sysctl", "-qw", forward); err != nil {
			return result, err
		}
		if spoofing {
			rules := fmt.Sprintf("table inet jdsim {\n chain spoof {\n  type filter hook forward priority 0; policy accept;\n  iifname \"%s\" %s saddr != %s drop\n }\n}\n", up, map[string]string{"inet": "ip", "inet6": "ip6"}[family], a.hostNet)
			if _, err := runStdin(ctx, []byte(rules), "ip", "netns", "exec", gw, "nft", "-f", "-"); err != nil {
				spoofing = false
			}
		}
		sm := m
		sm.Kind, sm.Device, sm.Gateway, sm.Source = "gateway", host, a.gateway.String(), a.host.String()
		simGroup.Members = append(simGroup.Members, sm)
		result.Topology = append(result.Topology, fmt.Sprintf("%s stands for %s: %s on %s via %s, then on to the internet namespace.", gw, m.Name, a.host, host, a.gateway))
	}
	if spoofing {
		result.Topology = append(result.Topology, "Each member namespace drops forwarded packets whose source is not its own network, as a provider's upstream does.")
	}
	if err := ip("-n", names.Internet, "link", "add", "t0", "type", "dummy"); err != nil {
		return result, err
	}
	if err := ip("-n", names.Internet, "link", "set", "t0", "up"); err != nil {
		return result, err
	}
	seen := map[string]bool{}
	for _, p := range g.Probes {
		if seen[p.Target] {
			continue
		}
		seen[p.Target] = true
		t := netip.MustParseAddr(p.Target)
		if err := ip(append([]string{"-n", names.Internet}, egressSimAddrArgs(family, netip.PrefixFrom(t, t.BitLen()), t, "t0")...)...); err != nil {
			return result, err
		}
	}
	result.Topology = append(result.Topology, names.Internet+" holds the probe targets "+strings.Join(sortedKeys(seen), ", ")+", answering ICMP, TCP and DNS as configured.")
	internet := s.egressNetnsPath(names.Internet)
	for _, p := range g.Probes {
		stop, err := egressSimResponder(internet, p)
		if err != nil {
			return result, fmt.Errorf("starting the simulated %s responder on %s: %w", p.Kind, p.Target, err)
		}
		if stop != nil {
			listeners = append(listeners, stop)
		}
	}
	simGroup.Active = simGroup.firstTier()
	for _, o := range egressObjects(simGroup) {
		if err := ip(append([]string{"-n", names.Host}, o.argv(o.add)...)...); err != nil {
			return result, err
		}
	}
	result.Limits = []string{
		"Each member is modelled as a gateway on a veth link; a tunnel's encapsulation, MTU and handshake are not.",
		"Time stands still between samples: hold and stability windows pass at the group's interval, while probes still wait for real answers.",
		"netem shapes the link a member's replies return on; provider behaviour beyond a lost or delayed packet is not modelled.",
		"The simulation shows the decisions this configuration makes; it does not prove the real members are independent upstreams.",
	}
	host := s.egressNetnsPath(names.Host)
	states := map[int]*egressMemberState{}
	for _, m := range simGroup.Members {
		states[m.ID] = newEgressMemberState("")
	}
	faults := map[int]string{}
	setFault := func(id int, args []string) error {
		want := strings.Join(args, " ")
		if faults[id] == want {
			return nil
		}
		dev := "u" + strconv.Itoa(id)
		ns := names.Members[id]
		if args == nil {
			if _, err := run(ctx, "ip", "netns", "exec", ns, "tc", "qdisc", "del", "dev", dev, "root"); err != nil && !isGone(err) && !strings.Contains(err.Error(), "No such file") && !strings.Contains(err.Error(), "Cannot delete qdisc with handle of zero") {
				return fmt.Errorf("clearing netem on %s: %w", dev, err)
			}
		} else if err := inNS(ns, append([]string{"tc", "qdisc", "replace", "dev", dev, "root", "netem"}, args...)...); err != nil {
			return err
		}
		faults[id] = want
		return nil
	}
	epoch := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	interval := time.Duration(g.Thresholds.IntervalSeconds) * time.Second
	timeout := time.Duration(g.Thresholds.TimeoutMillis) * time.Millisecond
	index := 0
	for _, phase := range plan.phases {
		ph := EgressSimPhase{Name: phase.name, Description: phase.description, Start: index, End: index + phase.steps - 1, Faults: map[string]string{}}
		for step := 0; step < phase.steps; step++ {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			progress.mu.Lock()
			progress.status.Step, progress.status.Phase = index, phase.name
			progress.mu.Unlock()
			now := epoch.Add(time.Duration(index) * interval)
			for _, m := range simGroup.Members {
				args := phase.fault(m.ID, step)
				if args != nil {
					ph.Faults[strconv.Itoa(m.ID)] = strings.Join(args, " ")
				}
				if err := setFault(m.ID, args); err != nil {
					return result, err
				}
			}
			samples := map[int]EgressSample{}
			var wg sync.WaitGroup
			var lock sync.Mutex
			for _, m := range simGroup.Members {
				wg.Add(1)
				go func(m EgressMember) {
					defer wg.Done()
					path := egressPath{Mark: egressMark(simGroup.Slot, m.ID), Device: m.Device, Source: netip.MustParseAddr(m.Source), Netns: host}
					sample := sampleMember(ctx, path, simGroup.Probes, timeout, now)
					lock.Lock()
					samples[m.ID] = sample
					lock.Unlock()
				}(m)
			}
			wg.Wait()
			st := egressSimStep(&simGroup, states, samples, faults, now, epoch, index, phase.name)
			if st.Switched {
				route, _ := egressGroupRoute(simGroup)
				args := append(append(append([]string{"-n", names.Host}, fam...), "route", "replace", map[string]string{"inet": "default", "inet6": "::/0"}[family]), route...)
				args = append(args, "table", strconv.Itoa(egressGroupTable(simGroup.Slot)))
				if err := ip(args...); err != nil {
					return result, err
				}
			}
			first := simGroup.Probes[0]
			data := egressProbe(ctx, egressPath{Netns: host}, EgressProbe{Kind: "icmp", Target: first.Target}, timeout)
			if data.OK {
				st.DataPath = "reached " + first.Target + " through the group table"
			} else {
				st.DataPath = "did not reach " + first.Target + ": " + data.Error
			}
			result.Steps = append(result.Steps, st)
			index++
		}
		result.Phases = append(result.Phases, ph)
	}
	result.Expectations = judgeEgressSimulation(g, simGroup, plan, result)
	return result, nil
}

// egressSimStep judges one step's samples with the real hysteresis rules,
// decides with the real decision rules, and takes the decision.
func egressSimStep(group *EgressGroupSpec, states map[int]*egressMemberState, samples map[int]EgressSample, faults map[int]string, now, epoch time.Time, index int, phase string) EgressSimStep {
	views := map[int]egressMemberView{}
	st := EgressSimStep{Index: index, Phase: phase, Seconds: int(now.Sub(epoch) / time.Second)}
	for _, m := range group.Members {
		_, judged := states[m.ID].observe(samples[m.ID], group.Thresholds)
		ms := states[m.ID]
		views[m.ID] = egressMemberView{State: ms.State, Since: ms.Since, Fresh: true, Why: judged.Why}
		st.Members = append(st.Members, EgressSimMember{ID: m.ID, State: ms.State, Good: judged.Good, LatencyMillis: judged.LatencyMillis, Loss: judged.Loss, OK: judged.OK, Total: judged.Total, Fault: faults[m.ID]})
	}
	d := decideEgress(*group, views, now)
	st.Decision = d
	if d.switches() {
		group.Active, group.DecidedAt = d.Target, now
		st.Switched = true
	}
	st.Active = append([]int{}, group.Active...)
	return st
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// egressSimResponder answers a TCP or DNS probe target inside the simulated
// internet. ICMP needs nothing: the kernel answers it.
func egressSimResponder(netns string, p EgressProbe) (func(), error) {
	addr := netip.AddrPortFrom(netip.MustParseAddr(p.Target), uint16(p.Port)).String()
	switch p.Kind {
	case "tcp":
		var l net.Listener
		err := inNetns(netns, func() error {
			var err error
			l, err = net.Listen("tcp", addr)
			return err
		})
		if err != nil {
			if strings.Contains(err.Error(), "address already in use") {
				return nil, nil
			}
			return nil, err
		}
		go func() {
			for {
				c, err := l.Accept()
				if err != nil {
					return
				}
				c.Close()
			}
		}()
		return func() { l.Close() }, nil
	case "dns":
		var pc net.PacketConn
		err := inNetns(netns, func() error {
			var err error
			pc, err = net.ListenPacket("udp", addr)
			return err
		})
		if err != nil {
			if strings.Contains(err.Error(), "address already in use") {
				return nil, nil
			}
			return nil, err
		}
		go func() {
			buf := make([]byte, 1500)
			for {
				n, from, err := pc.ReadFrom(buf)
				if err != nil {
					return
				}
				var parser dnsmessage.Parser
				header, err := parser.Start(buf[:n])
				if err != nil {
					continue
				}
				q, err := parser.Question()
				if err != nil {
					continue
				}
				reply, err := (&dnsmessage.Message{Header: dnsmessage.Header{ID: header.ID, Response: true, RecursionDesired: header.RecursionDesired, RecursionAvailable: true}, Questions: []dnsmessage.Question{q}}).Pack()
				if err == nil {
					_, _ = pc.WriteTo(reply, from)
				}
			}
		}()
		return func() { pc.Close() }, nil
	}
	return nil, nil
}

// judgeEgressSimulation checks the run against what the rules promise.
func judgeEgressSimulation(g, sim EgressGroupSpec, plan egressSimPlan, r *EgressSimulationResult) []EgressExpectation {
	phase := map[string]EgressSimPhase{}
	for _, p := range r.Phases {
		phase[p.Name] = p
	}
	stepsIn := func(name string) []EgressSimStep {
		p := phase[name]
		if p.End < p.Start || p.End >= len(r.Steps) {
			return nil
		}
		return r.Steps[p.Start : p.End+1]
	}
	primary := plan.primary
	name := egressMemberName(g, primary)
	t := g.Thresholds
	var out []EgressExpectation
	add := func(n string, ok bool, detail string) {
		out = append(out, EgressExpectation{Name: n, Passed: ok, Detail: detail})
	}

	warm := stepsIn("warmup")
	allUp := len(warm) > 0
	if allUp {
		for _, m := range warm[len(warm)-1].Members {
			if m.State != "up" {
				allUp = false
			}
		}
	}
	add("Every member proves its path", allUp, fmt.Sprintf("After %d clean samples every member must be up through its own mark, source and device.", len(warm)))

	switched := func(steps []EgressSimStep) []EgressSimStep {
		var s []EgressSimStep
		for _, st := range steps {
			if st.Switched {
				s = append(s, st)
			}
		}
		return s
	}
	tol := switched(stepsIn("tolerable-latency"))
	add("Latency under the threshold moves nothing", len(tol) == 0, fmt.Sprintf("%s delayed below %d ms: %d switches.", name, t.LatencyMillis, len(tol)))

	fail := stepsIn("failure")
	failOK, failDetail := false, "No failover happened while "+name+" dropped every packet."
	for i, st := range fail {
		if st.Switched && !slices.Contains(st.Active, primary) {
			failOK = i <= t.FailAfter
			failDetail = fmt.Sprintf("Failed over to %s at sample %d of the failure, after the %d bad samples the rules require.", egressNames(g, st.Active), i+1, t.FailAfter)
			if i < t.FailAfter-1 {
				failOK = false
				failDetail = fmt.Sprintf("Failed over at sample %d, before %d bad samples.", i+1, t.FailAfter)
			}
			break
		}
	}
	add("Fails over after a sustained failure", failOK, failDetail)

	flap := stepsIn("flap")
	flapBack := 0
	for _, st := range flap {
		if st.Switched && slices.Contains(st.Active, primary) {
			flapBack++
		}
	}
	add("A flapping member is not failed back to", flapBack == 0, fmt.Sprintf("%s alternated between failing and answering for %d samples: %d failbacks to it.", name, len(flap), flapBack))

	rec := stepsIn("recovery")
	if g.Failback == "manual" {
		back := 0
		held := false
		for _, st := range rec {
			if st.Switched && slices.Contains(st.Active, primary) {
				back++
			}
			if st.Decision.Action == "hold" && strings.Contains(st.Decision.Reason, "by hand") {
				held = true
			}
		}
		add("Manual failback waits for the operator", back == 0 && held, fmt.Sprintf("%d automatic failbacks; the rules said failback is available: %v.", back, held))
	} else {
		upAt, backAt := -1, -1
		already := len(rec) > 0 && slices.Contains(r.Steps[phase["recovery"].Start].Active, primary) && !rec[0].Switched
		for i, st := range rec {
			for _, m := range st.Members {
				if m.ID == primary && m.State == "up" && upAt < 0 {
					upAt = i
				}
			}
			if st.Switched && slices.Contains(st.Active, primary) && backAt < 0 {
				backAt = i
			}
		}
		stable := int(math.Ceil(float64(t.StableSeconds) / float64(t.IntervalSeconds)))
		ok := upAt >= 0 && backAt >= 0 && backAt-upAt >= stable
		detail := fmt.Sprintf("%s was up again at recovery sample %d and failed back to at sample %d; the stable window is %d samples.", name, upAt+1, backAt+1, stable)
		switch {
		case already:
			ok, detail = false, fmt.Sprintf("%s was already carrying traffic when its recovery began: it was failed back to while it flapped.", name)
		case backAt < 0:
			detail = fmt.Sprintf("%s never failed back to during %d clean samples (up at sample %d).", name, len(rec), upAt+1)
		}
		add("Fails back only after a stable recovery", ok, detail)
	}

	breach := stepsIn("latency-breach")
	if len(breach) > 0 && slices.Contains(r.Steps[phase["latency-breach"].Start].Active, primary) && len(r.Steps[phase["latency-breach"].Start].Active) > 0 {
		moved := false
		for _, st := range breach {
			if st.Switched && !slices.Contains(st.Active, primary) {
				moved = true
			}
		}
		add("Latency over the threshold fails over", moved, fmt.Sprintf("%s delayed above %d ms while carrying traffic: failed over %v.", name, t.LatencyMillis, moved))
	}

	outage := stepsIn("outage")
	outageSwitch := switched(outage)
	held := false
	for _, st := range outage {
		if st.Decision.Action == "hold" && st.Decision.key == "none-proven" {
			held = true
		}
	}
	add("With nothing proven the route is kept", len(outageSwitch) == 0 && held, fmt.Sprintf("Every member failed: %d switches, and the rules held the current route: %v.", len(outageSwitch), held))

	neverDown := true
	var bad string
	for _, st := range r.Steps {
		if !st.Switched {
			continue
		}
		for _, id := range st.Active {
			for _, m := range st.Members {
				if m.ID == id && m.State != "up" {
					neverDown, bad = false, fmt.Sprintf("Sample %d switched to %s while it was %s.", st.Index+1, egressMemberName(g, id), m.State)
				}
			}
		}
	}
	if neverDown {
		bad = "Every switch went to members proven up at that sample."
	}
	add("Never switches to an unproven member", neverDown, bad)

	dataOK := true
	dataDetail := "Whenever the decided members were up and clean, unmarked traffic through the group table reached the first target."
	for _, st := range r.Steps {
		healthy := len(st.Active) > 0
		for _, id := range st.Active {
			for _, m := range st.Members {
				if m.ID == id && (m.State != "up" || !m.Good || m.Fault != "") {
					healthy = false
				}
			}
		}
		if healthy && !strings.HasPrefix(st.DataPath, "reached") {
			dataOK, dataDetail = false, fmt.Sprintf("Sample %d: %s.", st.Index+1, st.DataPath)
			break
		}
	}
	add("Traffic follows the decision", dataOK, dataDetail)

	restore := stepsIn("restore")
	restoreOK := len(restore) > 0
	if restoreOK {
		last := restore[len(restore)-1]
		for _, id := range last.Active {
			for _, m := range last.Members {
				if m.ID == id && m.State != "up" {
					restoreOK = false
				}
			}
		}
	}
	add("Recovers to a proven member", restoreOK, "After the outage, the decided members are up again by the end of the run.")
	_ = sim
	return out
}
