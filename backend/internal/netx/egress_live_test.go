package netx

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The egress lane runs the real monitor, probes, switches, journal and boot
// files against a real kernel inside a throwaway namespace that stands for
// this host. Two upstream namespaces stand for two providers — each forwards
// only sources from its own network, as a provider's upstream does — and a
// third holds the internet: the probe targets, a TCP listener and an
// operator's address. A fourth namespace is an operator on a directly
// connected network. Nothing reaches the host's own interfaces; the probes
// and connection tracking enter the namespace in-process, so the lane runs as
// root:
//
//	go test -race -c -o netx.test ./internal/netx
//	sudo -n env JD_NETNS_LIVE=1 TMPDIR=$TMPDIR ./netx.test -test.run '^TestLiveEgress' -test.v

type egressLab struct {
	t     *testing.T
	ns    *liveNS
	s     *Service
	now   time.Time
	group EgressGroupSpec
}

func newEgressLab(t *testing.T) *egressLab {
	t.Helper()
	liveRequired(t)
	if os.Geteuid() != 0 {
		t.Skip("the in-process probes and connection tracking enter the namespace as root")
	}
	ns := newLiveNS(t)
	for _, n := range []string{"g1", "g2", "n", "c"} {
		ns.must(t, "ip", "netns", "add", n)
		ns.must(t, "ip", "-n", n, "link", "set", "lo", "up")
	}
	for k := 1; k <= 2; k++ {
		g, up := "g"+strconv.Itoa(k), "up"+strconv.Itoa(k)
		ns.must(t, "ip", "link", "add", up, "type", "veth", "peer", "name", "h"+strconv.Itoa(k), "netns", g)
		ns.must(t, "ip", "addr", "add", fmt.Sprintf("10.20%d.1.2/24", k), "dev", up)
		ns.must(t, "ip", "link", "set", up, "up")
		ns.must(t, "ip", "-n", g, "addr", "add", fmt.Sprintf("10.20%d.1.1/24", k), "dev", "h"+strconv.Itoa(k))
		ns.must(t, "ip", "-n", g, "link", "set", "h"+strconv.Itoa(k), "up")
		ns.must(t, "ip", "-n", g, "link", "add", "n"+strconv.Itoa(k), "type", "veth", "peer", "name", g, "netns", "n")
		ns.must(t, "ip", "-n", g, "addr", "add", fmt.Sprintf("10.21%d.0.1/24", k), "dev", "n"+strconv.Itoa(k))
		ns.must(t, "ip", "-n", g, "link", "set", "n"+strconv.Itoa(k), "up")
		ns.must(t, "ip", "-n", "n", "addr", "add", fmt.Sprintf("10.21%d.0.2/24", k), "dev", g)
		ns.must(t, "ip", "-n", "n", "link", "set", g, "up")
		ns.must(t, "ip", "-n", g, "route", "add", "default", "via", fmt.Sprintf("10.21%d.0.2", k))
		ns.must(t, "ip", "netns", "exec", g, "sysctl", "-qw", "net.ipv4.ip_forward=1")
		spoof := fmt.Sprintf("table inet upstream {\n chain spoof {\n  type filter hook forward priority 0; policy accept;\n  iifname \"h%d\" ip saddr != 10.20%d.1.0/24 counter drop\n }\n}\n", k, k)
		if _, err := ns.run(context.Background(), []byte(spoof), "ip", "netns", "exec", g, "nft", "-f", "-"); err != nil {
			t.Fatal(err)
		}
		ns.must(t, "ip", "-n", "n", "route", "add", fmt.Sprintf("10.20%d.1.0/24", k), "via", fmt.Sprintf("10.21%d.0.1", k))
	}
	ns.must(t, "ip", "-n", "n", "link", "add", "t0", "type", "dummy")
	ns.must(t, "ip", "-n", "n", "link", "set", "t0", "up")
	for _, a := range []string{"192.0.2.53/32", "192.0.2.80/32", "198.51.100.77/32"} {
		ns.must(t, "ip", "-n", "n", "addr", "add", a, "dev", "t0")
	}
	ns.must(t, "ip", "link", "add", "op0", "type", "veth", "peer", "name", "c0", "netns", "c")
	ns.must(t, "ip", "addr", "add", "10.203.0.1/24", "dev", "op0")
	ns.must(t, "ip", "link", "set", "op0", "up")
	ns.must(t, "ip", "-n", "c", "addr", "add", "10.203.0.2/24", "dev", "c0")
	ns.must(t, "ip", "-n", "c", "link", "set", "c0", "up")
	// The provider's default route, as the distribution configured it.
	ns.must(t, "ip", "route", "add", "default", "via", "10.201.1.1")
	// Connection tracking for the namespace's own flows.
	if _, err := ns.run(context.Background(), []byte("table inet track {\n chain out {\n  type filter hook output priority 0;\n  ct state new counter accept\n }\n}\n"), "nft", "-f", "-"); err != nil {
		t.Fatal(err)
	}
	useLive(t, ns)
	s := testService(t)
	s.egressNetns = fmt.Sprintf("/proc/%d/ns/net", ns.pid)
	s.egressNetnsRoot = fmt.Sprintf("/proc/%d/root/run/netns", ns.pid)
	lab := &egressLab{t: t, ns: ns, s: s, now: time.Now().UTC()}
	s.egress.clock = func() time.Time { return lab.now }
	stop, err := egressEchoServer(s.egressNetnsPath("n"), "192.0.2.80:80")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	return lab
}

// egressEchoServer echoes every line back, inside the internet namespace.
func egressEchoServer(netns, addr string) (func(), error) {
	var l net.Listener
	if err := inNetns(netns, func() error {
		var err error
		l, err = net.Listen("tcp", addr)
		return err
	}); err != nil {
		return nil, err
	}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					if _, err := c.Write([]byte(line)); err != nil {
						return
					}
				}
			}(c)
		}
	}()
	return func() { l.Close() }, nil
}

func egressLabRequestLive(mutate func(*EgressGroupRequest)) EgressGroupRequest {
	req := EgressGroupRequest{
		Name:   "providers",
		Policy: EgressPolicy{Kind: "all"},
		Members: []EgressMember{
			{Name: "provider-a", Kind: "gateway", Gateway: "10.201.1.1", Device: "up1", Priority: 1},
			{Name: "provider-b", Kind: "gateway", Gateway: "10.202.1.1", Device: "up2", Priority: 2},
		},
		Probes: []EgressProbe{{Kind: "icmp", Target: "192.0.2.53"}, {Kind: "tcp", Target: "192.0.2.80", Port: 80}},
		Thresholds: EgressThresholds{IntervalSeconds: 2, TimeoutMillis: 400, LossPercent: 20, LatencyMillis: 200,
			Window: 2, FailAfter: 2, RecoverAfter: 2, HoldSeconds: 2, StableSeconds: 6},
	}
	if mutate != nil {
		mutate(&req)
	}
	return req
}

const labOperator = "10.203.0.2"

// create makes and enables the group from the connected operator.
func (l *egressLab) create(req EgressGroupRequest) {
	l.t.Helper()
	g, err := l.s.CreateEgressGroup(context.Background(), req, labOperator, "lab")
	if err != nil {
		l.t.Fatal(err)
	}
	if _, err := l.s.EnableEgressGroup(context.Background(), g.ID, labOperator, "lab"); err != nil {
		l.t.Fatal(err)
	}
	l.refresh()
}

func (l *egressLab) refresh() {
	l.t.Helper()
	sp, err := l.s.loadSpec()
	if err != nil || len(sp.EgressGroups) == 0 {
		l.t.Fatalf("spec = %+v, %v", sp, err)
	}
	l.group = sp.EgressGroups[0]
}

// authorise records a passed simulation of the current configuration and
// turns automation on, as the operator would after running one.
func (l *egressLab) authorise() {
	l.t.Helper()
	l.s.egress.store.saveSimulation(context.Background(), EgressSimulation{ID: "lab", GroupID: l.group.ID, Fingerprint: l.group.Fingerprint(), Status: "passed", StartedAt: l.now, FinishedAt: l.now})
	if _, err := l.s.SetEgressAutomation(context.Background(), l.group.ID, true, "lab"); err != nil {
		l.t.Fatal(err)
	}
	l.refresh()
}

// round is one monitor interval: every member measured through its own path,
// the hysteresis applied and, with automation, the decision made real.
func (l *egressLab) round() {
	l.t.Helper()
	l.refresh()
	l.s.egress.state(context.Background(), l.group)
	l.s.egress.round(context.Background(), l.group)
	l.now = l.now.Add(time.Duration(l.group.Thresholds.IntervalSeconds) * time.Second)
	l.refresh()
}

func (l *egressLab) fault(member int, args ...string) {
	l.t.Helper()
	g, dev := "g"+strconv.Itoa(member), "h"+strconv.Itoa(member)
	if len(args) == 0 {
		_, _ = l.ns.run(context.Background(), nil, "ip", "netns", "exec", g, "tc", "qdisc", "del", "dev", dev, "root")
		return
	}
	l.ns.must(l.t, "ip", append([]string{"netns", "exec", g, "tc", "qdisc", "replace", "dev", dev, "root", "netem"}, args...)...)
}

func (l *egressLab) tableVia() string {
	l.t.Helper()
	got, err := readEgressDefault(context.Background(), "inet", egressGroupTable(l.group.Slot))
	if err != nil {
		l.t.Fatal(err)
	}
	return describeEgressRoute(got)
}

func (l *egressLab) events() []EgressEvent {
	events, _ := l.s.egress.store.list(context.Background(), l.group.ID, 200)
	return events
}

func (l *egressLab) switches() []EgressEvent {
	var out []EgressEvent
	for _, e := range l.events() {
		if e.Kind == "switch" && e.Outcome == "applied" {
			out = append(out, e)
		}
	}
	return out
}

// reaches is unmarked traffic from the host to the first target: whatever the
// group table now says.
func (l *egressLab) reaches() bool {
	_, err := l.ns.run(context.Background(), nil, "ping", "-n", "-c", "1", "-W", "1", "192.0.2.53")
	return err == nil
}

func TestLiveEgressFailoverAndFailbackWithHysteresis(t *testing.T) {
	l := newEgressLab(t)
	l.create(egressLabRequestLive(nil))
	l.authorise()
	if via := l.tableVia(); via != "through up1 via 10.201.1.1" {
		t.Fatalf("decided route = %s", via)
	}
	for i := 0; i < 3; i++ {
		l.round()
	}
	for _, m := range l.s.egress.views(l.group, l.now) {
		if m.State != "up" {
			t.Fatalf("a clean member is %s", m.State)
		}
	}
	// One lost sample is under the hysteresis: nothing moves.
	l.fault(1, "loss", "100%")
	l.round()
	if len(l.switches()) != 0 || !equalIDs(l.group.Active, []int{1}) {
		t.Fatal("switched on one bad sample")
	}
	l.round()
	if !equalIDs(l.group.Active, []int{2}) {
		t.Fatalf("no failover after two bad samples: %+v", l.events())
	}
	if via := l.tableVia(); via != "through up2 via 10.202.1.1" {
		t.Fatalf("failed over route = %s", via)
	}
	if !l.reaches() {
		t.Fatal("traffic did not follow the failover")
	}
	sw := l.switches()
	if len(sw) != 1 || sw[0].Action != "failover" || sw[0].Evidence == nil || !strings.Contains(sw[0].Reason, "provider-a is down") || sw[0].Evidence.RouteBefore == "" || sw[0].Evidence.Change == "" {
		t.Fatalf("failover record = %+v", sw)
	}
	t.Logf("failover: %s | %s -> %s", sw[0].Reason, sw[0].Evidence.RouteBefore, sw[0].Evidence.RouteAfter)
	// Recovery: two good samples make it up, then the stable window (six
	// seconds, three intervals) holds the failback.
	l.fault(1)
	upAfter := 0
	for i := 0; i < 12 && equalIDs(l.group.Active, []int{2}); i++ {
		l.round()
		upAfter++
	}
	if !equalIDs(l.group.Active, []int{1}) {
		t.Fatalf("no failback: %+v", l.events())
	}
	if upAfter < l.group.Thresholds.RecoverAfter+l.group.Thresholds.StableSeconds/l.group.Thresholds.IntervalSeconds {
		t.Fatalf("failed back after %d rounds, inside the hysteresis", upAfter)
	}
	holds := 0
	for _, e := range l.events() {
		if e.Kind == "hold" && strings.Contains(e.Reason, "stable recovery") {
			holds++
		}
	}
	if holds == 0 {
		t.Fatal("the stable-recovery hold was not recorded")
	}
	if via := l.tableVia(); via != "through up1 via 10.201.1.1" || !l.reaches() {
		t.Fatalf("failback route = %s", via)
	}
	t.Logf("failback after %d rounds: %s", upAfter, l.switches()[0].Reason)
	// The boot file follows every decision.
	links, _ := os.ReadFile(filepath.Join(l.s.paths.Dir, linksFile))
	if !strings.Contains(string(links), "route replace default via 10.201.1.1 dev up1 table 7700") {
		t.Fatalf("links.batch:\n%s", links)
	}
}

func TestLiveEgressFlapSuppression(t *testing.T) {
	l := newEgressLab(t)
	l.create(egressLabRequestLive(func(r *EgressGroupRequest) { r.Thresholds.RecoverAfter = 3 }))
	l.authorise()
	for i := 0; i < 4; i++ {
		l.round()
	}
	l.fault(1, "loss", "100%")
	l.round()
	l.round()
	if !equalIDs(l.group.Active, []int{2}) {
		t.Fatal("no failover")
	}
	// The primary alternates: it never strings three good samples together,
	// so it never comes back up and the group never fails back to it.
	for i := 0; i < 10; i++ {
		if i%2 == 0 {
			l.fault(1)
		} else {
			l.fault(1, "loss", "100%")
		}
		l.round()
		if !equalIDs(l.group.Active, []int{2}) {
			t.Fatalf("failed back to a flapping member at round %d: %+v", i, l.events())
		}
	}
	if n := len(l.switches()); n != 1 {
		t.Fatalf("%d switches while flapping", n)
	}
	if st := l.s.egress.views(l.group, l.now)[1]; st.State != "down" {
		t.Fatalf("flapping member is %s", st.State)
	}
}

func TestLiveEgressRefusesAnOperatorPathThroughAnUnprovenMember(t *testing.T) {
	l := newEgressLab(t)
	l.create(egressLabRequestLive(nil))
	// An operator on the internet, reaching this host over provider-a: its
	// replies leave from 10.201.1.2. Failing over would carry them through
	// provider-b, whose upstream drops a source that is not its own.
	internet := "198.51.100.77"
	before := l.tableVia()
	_, err := l.s.SwitchEgress(context.Background(), l.group.ID, EgressSwitchRequest{Action: "members", Members: []int{2}}, internet, "operator")
	if !errors.Is(err, ErrGuarded) || !strings.Contains(err.Error(), "a probe from 10.201.1.2 through provider-b failed") {
		t.Fatalf("switch = %v", err)
	}
	if after := l.tableVia(); after != before {
		t.Fatalf("the refused switch moved the route: %s -> %s", before, after)
	}
	l.refresh()
	if !equalIDs(l.group.Active, []int{1}) {
		t.Fatal("the decision changed")
	}
	var refused bool
	for _, e := range l.events() {
		if e.Outcome == "refused" && strings.Contains(e.Reason, "10.201.1.2") {
			refused = true
		}
	}
	if !refused {
		t.Fatalf("the refusal was not recorded: %+v", l.events())
	}
	// The connected operator the group protects is not carried, so the same
	// switch from there is made.
	if _, err := l.s.SwitchEgress(context.Background(), l.group.ID, EgressSwitchRequest{Action: "failover"}, labOperator, "operator"); err != nil {
		t.Fatal(err)
	}
	if via := l.tableVia(); via != "through up2 via 10.202.1.1" {
		t.Fatalf("switch = %s", via)
	}
	if _, err := l.ns.run(context.Background(), nil, "ip", "netns", "exec", "c", "ping", "-n", "-c", "1", "-W", "1", "10.203.0.1"); err != nil {
		t.Fatal("the protected operator lost the host")
	}
}

func TestLiveEgressBootRestoresTheDecidedMemberAndReverifies(t *testing.T) {
	l := newEgressLab(t)
	l.create(egressLabRequestLive(nil))
	if _, err := l.s.SwitchEgress(context.Background(), l.group.ID, EgressSwitchRequest{Action: "failover"}, labOperator, "operator"); err != nil {
		t.Fatal(err)
	}
	l.refresh()
	// A reboot loses every route and rule the dashboard made.
	for table := 7700; table <= 7708; table++ {
		_, _ = l.ns.run(context.Background(), nil, "ip", "route", "flush", "table", strconv.Itoa(table))
	}
	for p := 19000; p < 19016; p++ {
		_, _ = l.ns.run(context.Background(), nil, "ip", "rule", "del", "priority", strconv.Itoa(p))
	}
	if via := l.tableVia(); via != "no default route" {
		t.Fatalf("cold kernel still has %s", via)
	}
	// The unit replays the boot file.
	l.ns.must(t, "ip", "-force", "-batch", filepath.Join(l.s.paths.Dir, linksFile))
	if via := l.tableVia(); via != "through up2 via 10.202.1.1" {
		t.Fatalf("restored route = %s", via)
	}
	if !l.reaches() {
		t.Fatal("restored decision does not carry traffic")
	}
	// A fresh process checks what the unit restored before acting.
	fresh := New(Options{Paths: l.s.paths})
	fresh.egressNetns, fresh.egressNetnsRoot = l.s.egressNetns, l.s.egressNetnsRoot
	fresh.egress.clock = func() time.Time { return l.now }
	fresh.egress.state(context.Background(), l.group)
	fresh.egress.round(context.Background(), l.group)
	events, _ := fresh.egress.store.list(context.Background(), l.group.ID, 10)
	var verify *EgressEvent
	for i := range events {
		if events[i].Kind == "verify" {
			verify = &events[i]
		}
	}
	if verify == nil || verify.Outcome != "observed" || !strings.Contains(verify.Reason, "sends its traffic through up2 via 10.202.1.1, as decided") {
		t.Fatalf("verification = %+v", events)
	}
	t.Logf("verified: %s", verify.Reason)
	// A device that came up after the unit ran leaves its route missing; the
	// start check puts the decided route back through the journal.
	l.ns.must(t, "ip", "route", "flush", "table", "7700")
	again := New(Options{Paths: l.s.paths})
	again.egressNetns, again.egressNetnsRoot = l.s.egressNetns, l.s.egressNetnsRoot
	again.egress.clock = func() time.Time { return l.now }
	again.egress.state(context.Background(), l.group)
	again.egress.round(context.Background(), l.group)
	if via := l.tableVia(); via != "through up2 via 10.202.1.1" {
		t.Fatalf("drift repair = %s", via)
	}
	events, _ = again.egress.store.list(context.Background(), l.group.ID, 10)
	repaired := false
	for _, e := range events {
		if e.Kind == "verify" && e.Outcome == "applied" && strings.Contains(e.Reason, "restored the decided route, through up2 via 10.202.1.1") {
			repaired = true
			t.Logf("repaired: %s", e.Reason)
		}
	}
	if !repaired {
		t.Fatalf("repair record = %+v", events)
	}
	j, err := readChange(l.s.paths.Dir)
	if err != nil || j.Phase != "saved" {
		t.Fatalf("repair journal = %+v, %v", j, err)
	}
}

// A switch whose process dies after the kernel took the new route, before
// the spec was saved, is put back by a fresh recovery process with nothing
// but the journal.
func TestLiveEgressRecoveryAfterBackendDeathMidSwitch(t *testing.T) {
	l := newEgressLab(t)
	l.create(egressLabRequestLive(nil))
	before, _ := os.ReadFile(l.s.specPath())
	invoke := func(mode string) *exec.Cmd {
		return l.ns.command(context.Background(), "env", "JD_EGRESS_FIXTURE_DIR="+filepath.Dir(l.s.paths.Dir), "JD_EGRESS_FIXTURE_MODE="+mode, "JD_EGRESS_FIXTURE_GROUP="+strconv.Itoa(l.group.ID), os.Args[0], "-test.run=^TestEgressSwitchProcessFixture$")
	}
	out, err := invoke("switch").CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 83 {
		t.Fatalf("switching process: %v %s", err, out)
	}
	if via := l.tableVia(); via != "through up2 via 10.202.1.1" {
		t.Fatalf("the dying switch never reached the kernel: %s", via)
	}
	j, err := readChange(l.s.paths.Dir)
	if err != nil || j.Phase == "saved" || j.Runtime != "applied" {
		t.Fatalf("journal after death = %+v, %v", j, err)
	}
	if out, err := invoke("recover").CombinedOutput(); err != nil {
		t.Fatalf("standalone recovery: %v %s", err, out)
	}
	if via := l.tableVia(); via != "through up1 via 10.201.1.1" {
		t.Fatalf("recovered route = %s", via)
	}
	after, _ := os.ReadFile(l.s.specPath())
	if !bytes.Equal(before, after) {
		t.Fatal("the spec was not the one before the switch")
	}
	links, _ := os.ReadFile(filepath.Join(l.s.paths.Dir, linksFile))
	if !strings.Contains(string(links), "route replace default via 10.201.1.1 dev up1 table 7700") {
		t.Fatalf("the boot file was not restored:\n%s", links)
	}
	j, err = readChange(l.s.paths.Dir)
	if err != nil || j.Phase != "recovered" {
		t.Fatalf("journal = %+v, %v", j, err)
	}
	if !l.reaches() {
		t.Fatal("the recovered route does not carry traffic")
	}
}

// TestEgressSwitchProcessFixture is the dying switch and the recovery
// process. It runs inside the throwaway namespace with plain execution: it
// must not cross back into the host as the backend's host wrapper would.
func TestEgressSwitchProcessFixture(t *testing.T) {
	dir := os.Getenv("JD_EGRESS_FIXTURE_DIR")
	if dir == "" {
		return
	}
	local := func(ctx context.Context, stdin []byte, name string, args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if stdin != nil {
			cmd.Stdin = bytes.NewReader(stdin)
		}
		if err := cmd.Run(); err != nil {
			return out.String(), fmt.Errorf("%s: %s", name, strings.TrimSpace(out.String()))
		}
		return out.String(), nil
	}
	run = func(ctx context.Context, name string, args ...string) (string, error) {
		return local(ctx, nil, name, args...)
	}
	runStdin = local
	has = func(name string) bool { return name != "systemctl" && name != "systemd-run" && name != "wg" }
	paths := Paths{Dir: filepath.Join(dir, "network"), Sysctl: filepath.Join(dir, "sysctl.d", "90-just-dashboard.conf"), Unit: filepath.Join(dir, "systemd", UnitName),
		Resolved: filepath.Join(dir, "resolved.conf.d", "90-just-dashboard.conf"), Hosts: filepath.Join(dir, "hosts"), WireGuard: filepath.Join(dir, "wireguard")}
	if os.Getenv("JD_EGRESS_FIXTURE_MODE") == "recover" {
		if err := RecoverNetworkStandalone(context.Background(), paths.Dir, "pending"); err != nil {
			t.Fatal(err)
		}
		return
	}
	s := New(Options{Paths: paths})
	writer := writeNetworkFile
	writeNetworkFile = func(path string, data []byte, mode os.FileMode) error {
		if err := writer(path, data, mode); err != nil {
			return err
		}
		if path == s.specPath() {
			os.Exit(83)
		}
		return nil
	}
	id, _ := strconv.Atoi(os.Getenv("JD_EGRESS_FIXTURE_GROUP"))
	_, err := s.SwitchEgress(context.Background(), id, EgressSwitchRequest{Action: "members", Members: []int{2}}, "", "fixture")
	t.Fatalf("the switch did not reach its interruption: %v", err)
}

func TestLiveEgressStickyConnectionsSurviveFailbackAndFlushWithTheirMember(t *testing.T) {
	l := newEgressLab(t)
	l.create(egressLabRequestLive(func(r *EgressGroupRequest) { r.Sticky = true }))
	l.authorise()
	for i := 0; i < 3; i++ {
		l.round()
	}
	if _, err := l.s.SwitchEgress(context.Background(), l.group.ID, EgressSwitchRequest{Action: "failover"}, labOperator, "operator"); err != nil {
		t.Fatal(err)
	}
	// A connection opened through provider-b, from its address.
	var conn net.Conn
	if err := inNetns(l.s.egressNetns, func() error {
		var err error
		conn, err = (&net.Dialer{Timeout: 2 * time.Second}).Dial("tcp", "192.0.2.80:80")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	echo := func(word string) error {
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		if _, err := conn.Write([]byte(word + "\n")); err != nil {
			return err
		}
		line, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil {
			return err
		}
		if line != word+"\n" {
			return fmt.Errorf("echoed %q", line)
		}
		return nil
	}
	if err := echo("before"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(conn.LocalAddr().String(), "10.202.1.2:") {
		t.Fatalf("connection source = %s", conn.LocalAddr())
	}
	if _, err := l.s.SwitchEgress(context.Background(), l.group.ID, EgressSwitchRequest{Action: "failback"}, labOperator, "operator"); err != nil {
		t.Fatal(err)
	}
	if via := l.tableVia(); via != "through up1 via 10.201.1.1" {
		t.Fatalf("failback = %s", via)
	}
	// Without its pin, the next packet would leave through provider-a with
	// provider-b's address and be dropped there.
	if err := echo("after"); err != nil {
		t.Fatalf("the pinned connection did not survive the failback: %v", err)
	}
	sw := l.switches()
	if len(sw) == 0 || sw[0].Evidence.Connections == nil || sw[0].Evidence.Connections.Pinned == 0 {
		t.Fatalf("pinned connections were not reported: %+v", sw[0].Evidence)
	}
	t.Logf("failback connections: %+v", *sw[0].Evidence.Connections)
	// provider-b goes down while it still holds the pinned connection: the
	// monitor flushes it rather than leaving it routed into a dead path.
	l.fault(2, "loss", "100%")
	for i := 0; i < 3; i++ {
		l.round()
	}
	var flushed *EgressEvent
	for _, e := range l.events() {
		if e.Kind == "connections" {
			e := e
			flushed = &e
		}
	}
	if flushed == nil || flushed.Evidence.Connections.Flushed == 0 {
		t.Fatalf("no flush recorded: %+v", l.events())
	}
	t.Logf("flush: %s", flushed.Reason)
	count := 0
	_ = inNetns(l.s.egressNetns, func() error {
		_, _, err := conntrackDump(context.Background(), 1000, func(e ctEntry) bool {
			if e.Mark&egressMarkMask == egressMark(l.group.Slot, 2) {
				count++
			}
			return true
		})
		return err
	})
	if count != 0 {
		t.Fatalf("%d connections still pinned to the down member", count)
	}
}

func TestLiveEgressSimulationRunsInDisposableNamespaces(t *testing.T) {
	l := newEgressLab(t)
	g, err := l.s.CreateEgressGroup(context.Background(), egressLabRequestLive(nil), labOperator, "lab")
	if err != nil {
		t.Fatal(err)
	}
	// The kernel finishes IPv6 duplicate address detection on its own time,
	// so the comparison is of the dashboard's concern: rules and IPv4 routes.
	routing := func() string {
		return l.ns.must(t, "ip", "rule", "show") + l.ns.must(t, "ip", "-6", "rule", "show") + l.ns.must(t, "ip", "-4", "route", "show", "table", "all")
	}
	before := routing()
	wait := func(id string) EgressSimulation {
		t.Helper()
		deadline := time.Now().Add(5 * time.Minute)
		for time.Now().Before(deadline) {
			if l.s.egress.running() == nil {
				sim, ok := l.s.egress.store.simulation(context.Background(), id)
				if ok && sim.Status != "running" {
					return sim
				}
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Fatal("the simulation did not finish")
		return EgressSimulation{}
	}
	started, err := l.s.SimulateEgress(context.Background(), g.ID, "lab")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.s.SimulateEgress(context.Background(), g.ID, "lab"); err == nil {
		t.Fatal("a second simulation ran beside the first")
	}
	sim := wait(started.ID)
	if sim.Status != "passed" {
		b, _ := json.MarshalIndent(sim, "", " ")
		t.Fatalf("simulation %s:\n%s", sim.Status, b)
	}
	for _, e := range sim.Result.Expectations {
		t.Logf("%s: %s", e.Name, e.Detail)
	}
	switched := 0
	for _, st := range sim.Result.Steps {
		if st.Switched {
			switched++
			t.Logf("sample %d (%s): %s %v — %s", st.Index+1, st.Phase, st.Decision.Action, st.Active, st.Decision.Reason)
		}
	}
	if switched < 3 {
		t.Fatalf("only %d switches in the run", switched)
	}
	if list := l.ns.must(t, "ip", "netns", "list"); strings.Contains(list, "jds") {
		t.Fatalf("simulation namespaces left behind:\n%s", list)
	}
	if after := routing(); after != before {
		t.Fatalf("the simulation changed the host's own routing:\n%s\n---\n%s", before, after)
	}
	if _, err := os.Stat(filepath.Join(l.s.paths.Dir, egressSimFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the namespace record was left behind")
	}
	// The passed run authorises automation for exactly this configuration.
	if _, err := l.s.EnableEgressGroup(context.Background(), g.ID, labOperator, "lab"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.s.SetEgressAutomation(context.Background(), g.ID, true, "lab"); err != nil {
		t.Fatal(err)
	}
	// A configuration without hysteresis fails its simulation.
	if _, err := l.s.DisableEgressGroup(context.Background(), g.ID, labOperator, "lab"); err != nil {
		t.Fatal(err)
	}
	loose := egressLabRequestLive(func(r *EgressGroupRequest) {
		r.Thresholds.Window, r.Thresholds.FailAfter, r.Thresholds.RecoverAfter, r.Thresholds.HoldSeconds, r.Thresholds.StableSeconds = 1, 1, 1, 0, 0
	})
	if _, err := l.s.UpdateEgressGroup(context.Background(), g.ID, loose, labOperator, "lab"); err != nil {
		t.Fatal(err)
	}
	started, err = l.s.SimulateEgress(context.Background(), g.ID, "lab")
	if err != nil {
		t.Fatal(err)
	}
	sim = wait(started.ID)
	if sim.Status != "failed" {
		t.Fatalf("a configuration that fails back to a flapping member passed: %s", sim.Status)
	}
	for _, e := range sim.Result.Expectations {
		if !e.Passed {
			t.Logf("failed as expected — %s: %s", e.Name, e.Detail)
		}
	}
	if _, err := l.s.EnableEgressGroup(context.Background(), g.ID, labOperator, "lab"); err != nil {
		t.Fatal(err)
	}
	var confirm *ConfirmationError
	if _, err := l.s.SetEgressAutomation(context.Background(), g.ID, true, "lab"); !errors.As(err, &confirm) {
		t.Fatalf("automation after a failed simulation = %v", err)
	}
}
