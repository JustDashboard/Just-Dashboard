package netflows

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func observedPacket(cookie, ns, group uint64, peer string, port int, bytes *uint64) kernelPacket {
	return kernelPacket{cookie: cookie, namespace: ns, cgroup: group, direction: 1, payload: bytes, socket: Socket{Protocol: "udp", Cookie: fmt.Sprint(cookie), LocalAddress: "127.0.0.1", LocalPort: 12345, LocalEndpoint: "127.0.0.1:12345", RemoteAddress: peer, RemotePort: port, RemoteEndpoint: fmt.Sprintf("%s:%d", peer, port), Owner: Owner{Status: "unknown", Reason: "process unknown"}}}
}

func TestKernelIdentitySeparatesCookieCgroupNamespaceProtocolAndEveryEndpoint(t *testing.T) {
	at := time.Now().UTC()
	base := observedPacket(1, 2, 3, "192.0.2.1", 8080, number(257))
	variants := []kernelPacket{base, base, base, base, base, base, base, base}
	variants[1].cookie++
	variants[2].namespace++
	variants[3].cgroup++
	variants[4].socket.Protocol = "tcp"
	variants[5].socket.LocalAddress = "127.0.0.2"
	variants[6].socket.LocalPort++
	variants[7].socket.RemoteAddress = "192.0.2.2"
	variants = append(variants, base)
	variants[8].socket.RemotePort++
	seen := map[string]bool{}
	for _, p := range variants {
		b := packetBucket("boot", p, at)
		if seen[b.ID] || b.ID == "kernel-v1:" {
			t.Fatalf("kernel identity collision: %+v", b)
		}
		seen[b.ID] = true
	}
	base.direction = 0
	if packetBucket("boot", base, at).ID != packetBucket("boot", variants[0], at).ID {
		t.Fatal("directions of the same local socket should share a bucket")
	}
	if packetBucket("next-boot", base, at).ID == packetBucket("boot", base, at).ID {
		t.Fatal("boot identity omitted")
	}
}

func TestKernelRowsRetainKnownSubtotalAndPerDirectionByteGaps(t *testing.T) {
	at := time.Now().UTC()
	p := observedPacket(1, 2, 3, "192.0.2.1", 8080, number(257))
	known := packetBucket("boot", p, at)
	p.payload, p.byteGap = nil, true
	unknown := packetBucket("boot", p, at.Add(time.Millisecond))
	p.direction = 0
	rxUnknown := packetBucket("boot", p, at.Add(2*time.Millisecond))
	b := merge(merge(known, unknown), rxUnknown)
	if b.ObservedTxBytes == nil || *b.ObservedTxBytes != 257 || b.ObservedRxBytes != nil || b.ObservedPackets != 3 || b.ObservedTxPackets != 2 || b.ObservedTxKnownPackets != 1 || b.ObservedTxByteGaps != 1 || b.ObservedRxByteGaps != 1 || b.ObservedRxKnownPackets != 0 {
		t.Fatalf("byte subtotal/gaps=%+v", b)
	}
}

func fixtureObserver(at time.Time) *KernelObserver {
	o := NewKernelObserver(nil)
	o.status = ObserverEvidence{Status: "recording", BootID: "fixture-boot", AttachmentsRetained: true}
	o.kernel = &kernelSession{ring: -1, stats: -1, budget: -1, identities: -1}
	o.session, o.wall, o.monotonic = "fixture-session", at, 1000000
	o.pending = map[string]Bucket{}
	o.readBoot = func() ([]byte, error) { return []byte("fixture-boot"), nil }
	o.readStats = func(int) ([6]uint64, error) { return [6]uint64{4, 2, 3, 0, 1, 0}, nil }
	o.readClock = func() (time.Time, uint64, error) { return at, 1000000, nil }
	o.done = make(chan struct{})
	close(o.done)
	return o
}
func appendFixtureBucket(o *KernelObserver, b Bucket) {
	key := fmt.Sprintf("%s:%d", b.ID, b.Hour.Unix())
	if old, ok := o.pending[key]; ok {
		o.pending[key] = merge(old, b)
	} else {
		o.pending[key] = b
	}
}

func TestRecorderSQLiteHistoryExportKeepMultiplePeersAndQualityThroughFailedWrite(t *testing.T) {
	st, _ := testStore(t)
	at := time.Now().UTC().Truncate(time.Hour).Add(time.Minute)
	o := fixtureObserver(at)
	for i := 0; i < 4; i++ {
		appendFixtureBucket(o, packetBucket("fixture-boot", observedPacket(uint64(i+1), uint64(2+i%2), uint64(3+i%2), fmt.Sprintf("192.0.2.%d", i+1), 8080+i, number(uint64(257+i))), at))
	}
	s := New(st, functionCollector(func(context.Context) Cycle {
		return Cycle{At: at, FinishedAt: at, Sources: []Source{}, DockerStatus: "observed"}
	}))
	s.started = true
	s.state = persistedState{Settings: DefaultSettings}
	s.state.Settings.Enabled, s.state.Settings.KernelObserverEnabled = true, true
	s.observer = o
	if _, err := st.db.Exec(`CREATE TRIGGER fail_observer_batch BEFORE INSERT ON network_flow_cycles BEGIN SELECT RAISE(ABORT,'fixture storage failure'); END`); err != nil {
		t.Fatal(err)
	}
	s.sample(t.Context())
	if s.pendingRecord == nil || o.inflight == nil || o.previous.Events != 0 || s.lastError == "" {
		t.Fatal("failed transaction acknowledged or discarded its batch")
	}
	firstID := o.inflight.evidence.BatchID
	appendFixtureBucket(o, packetBucket("fixture-boot", observedPacket(99, 5, 6, "192.0.2.99", 9999, number(99)), at))
	rows, evidence, err := o.Drain(t.Context(), at.Add(time.Second))
	if err != nil || evidence.BatchID != firstID || len(rows) != 4 || evidence.Quality.RingDrops != 2 {
		t.Fatalf("retry batch changed: rows=%d evidence=%+v err=%v", len(rows), evidence, err)
	}
	if _, err = st.db.Exec(`DROP TRIGGER fail_observer_batch`); err != nil {
		t.Fatal(err)
	}
	s.sample(t.Context())
	if s.pendingRecord != nil || o.inflight != nil || o.previous.Events != 4 || len(o.pending) != 1 {
		t.Fatal("successful commit did not acknowledge exactly one bounded batch")
	}
	q := Query{From: at.Truncate(time.Hour), To: at.Truncate(time.Hour).Add(time.Hour)}
	raw, err := s.Export(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	var report Report
	if err = json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Rows) != 4 || len(report.CoverageHours) != 1 || report.CoverageHours[0].ObserverQuality.RingDrops != 2 || report.CoverageHours[0].ObserverQuality.BudgetOmissions != 3 {
		t.Fatalf("lost/merged durable rows or quality: %+v", report)
	}
	seen := map[string]bool{}
	for _, b := range report.Rows {
		if seen[b.ID] || b.ObservedPackets != 1 || b.ObservedTxBytes == nil || b.Socket.Owner.Status != "unknown" {
			t.Fatalf("stored peer collision: %+v", b)
		}
		seen[b.ID] = true
	}
	for i := 0; i < 4; i++ {
		q.Address = fmt.Sprintf("192.0.2.%d", i+1)
		got, err := s.Report(t.Context(), q)
		if err != nil || len(got.Rows) != 1 {
			t.Fatalf("address filter=%+v err=%v", got, err)
		}
	}
	if report.LastCycle.Observer.DockerStatus != "unavailable" || report.LastCycle.Observer.DockerCheckedAt.IsZero() || report.LastCycle.Observer.DockerError == "" {
		t.Fatal("failed Docker read lacks dated unavailable evidence")
	}
}

type failedAckObserver struct {
	*KernelObserver
	fail bool
}

func (o *failedAckObserver) Acknowledge(id string) error {
	if o.fail {
		o.fail = false
		return errors.New("fixture lost acknowledgement")
	}
	return o.KernelObserver.Acknowledge(id)
}
func TestDurableReceiptMakesUncertainCommitRetryIdempotent(t *testing.T) {
	st, _ := testStore(t)
	at := time.Now().UTC().Truncate(time.Hour).Add(time.Minute)
	o := &failedAckObserver{KernelObserver: fixtureObserver(at), fail: true}
	appendFixtureBucket(o.KernelObserver, packetBucket("fixture-boot", observedPacket(1, 2, 3, "192.0.2.1", 80, number(257)), at))
	s := New(st, functionCollector(func(context.Context) Cycle {
		return Cycle{At: at, FinishedAt: at, Sources: []Source{}, DockerStatus: "observed"}
	}))
	s.started = true
	s.state = persistedState{Settings: DefaultSettings}
	s.state.Settings.Enabled, s.state.Settings.KernelObserverEnabled = true, true
	s.observer = o
	s.sample(t.Context())
	if s.pendingRecord == nil || o.inflight == nil {
		t.Fatal("acknowledgement failure dropped batch")
	}
	s.sample(t.Context())
	q := Query{From: at.Truncate(time.Hour), To: at.Truncate(time.Hour).Add(time.Hour)}
	r, err := s.Report(t.Context(), q)
	if err != nil || len(r.Rows) != 1 || r.Rows[0].ObservedPackets != 1 || *r.Rows[0].ObservedTxBytes != 257 || r.CoverageHours[0].Samples != 1 || r.CoverageHours[0].ObserverQuality.RingDrops != 2 {
		t.Fatalf("uncertain commit duplicated observations: %+v err=%v", r, err)
	}
}

func TestClockStepMarksUTCAndAttributionUncertain(t *testing.T) {
	at := time.Now().UTC()
	o := fixtureObserver(at)
	appendFixtureBucket(o, packetBucket("fixture-boot", observedPacket(1, 2, 3, "192.0.2.1", 80, number(257)), at))
	o.readClock = func() (time.Time, uint64, error) { return at.Add(time.Hour), 1000000, nil }
	rows, e, err := o.Drain(t.Context(), at)
	if err != nil || len(rows) != 1 || !rows[0].TimestampUncertain || e.Quality.TimestampGaps != 1 || e.TimestampReason == "" {
		t.Fatalf("clock discontinuity hidden: %+v %+v %v", rows, e, err)
	}
}

func TestDetachFailureRetainsResourcesAndRetriesWithoutClosingReusedDescriptors(t *testing.T) {
	var closed []int
	fail := true
	k := &kernelSession{ring: 30, stats: 31, budget: 32, identities: 33, programs: []int{20, 21}, links: []ownedObserverLink{{fd: 10}, {fd: 11}}, verifyLink: func(l *ownedObserverLink) error {
		if l.fd == 11 && fail {
			return errors.New("fixture changed link identity")
		}
		return nil
	}, closeFD: func(fd int) error { closed = append(closed, fd); return nil }}
	if err := k.close(); err == nil || k.closed || !reflect.DeepEqual(closed, []int{10}) || k.links[0].fd != -1 || k.links[1].fd != 11 {
		t.Fatalf("unverified detach discarded ownership: closed=%v k=%+v err=%v", closed, k, err)
	}
	fail = false
	if err := k.close(); err != nil || !k.closed {
		t.Fatalf("retry=%v k=%+v", err, k)
	}
	if !reflect.DeepEqual(closed, []int{10, 11, 20, 21, 30, 31, 32, 33}) {
		t.Fatalf("descriptor was reused/closed twice: %v", closed)
	}
	_ = k.close()
	if len(closed) != 8 {
		t.Fatal("closed session repeated descriptor close")
	}
}
func TestObserverStopFailureKeepsStartRefusedAndServiceMarkerActive(t *testing.T) {
	st, _ := testStore(t)
	at := time.Now().UTC()
	o := fixtureObserver(at)
	fail := true
	o.kernel.links = []ownedObserverLink{{fd: 10}}
	o.kernel.verifyLink = func(*ownedObserverLink) error {
		if fail {
			return unix.EIO
		}
		return nil
	}
	o.kernel.closeFD = func(int) error { return nil }
	s := New(st, nil)
	s.started = true
	s.state = persistedState{Settings: DefaultSettings}
	s.state.Settings.Enabled, s.state.Settings.KernelObserverEnabled = true, true
	s.observer = o
	if _, err := s.KernelRecording(t.Context(), false); err == nil || !s.state.Settings.KernelObserverEnabled || o.kernel == nil {
		t.Fatalf("failed detach appeared stopped: %v", err)
	}
	if err := o.Start(t.Context()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("second session allowed: %v", err)
	}
	fail = false
	if _, err := s.KernelRecording(t.Context(), false); err != nil || s.state.Settings.KernelObserverEnabled || o.kernel != nil {
		t.Fatalf("detach retry failed: %v", err)
	}
}
func TestKernelByteExportKeepsExactUnsignedDecimalAndQuality(t *testing.T) {
	b := packetBucket("boot", observedPacket(1, 2, 3, "192.0.2.1", 80, number(9007199254740993)), time.Now())
	raw, err := json.Marshal(b)
	if err != nil || !strings.Contains(string(raw), `"observedTxBytes":"9007199254740993"`) || !strings.Contains(string(raw), `"observedTxKnownPackets":"1"`) {
		t.Fatalf("unsigned decimal export=%s err=%v", raw, err)
	}
}

// Execute the actual assembled CAS section, including register comparisons and
// the real jump targets. The interleave hook replaces the shared map immediately
// before CAS, as another CPU's newer packet can do.
func assembledBudgetClaim(t *testing.T, epoch uint64, shared *uint64, beforeCAS func()) bool {
	t.Helper()
	code, err := observerProgram(10, 11, 12, 13, 1)
	if err != nil {
		t.Fatal(err)
	}
	pc := -1
	for i, ins := range code {
		if ins.Code == 0x18 && ins.Immediate == 12 {
			pc = i + 6
			break
		}
	}
	if pc < 0 {
		t.Fatal("fixed budget map relocation missing")
	}
	var reg [11]uint64
	reg[0], reg[8] = 42, epoch<<16
	for steps := 0; steps < 100; steps++ {
		ins := code[pc]
		dst, src := ins.Registers&15, ins.Registers>>4
		if ins.Code == 0x61 && dst == 1 && src == 6 && ins.Offset == 0 {
			return true
		}
		if ins.Code == 0x62 && ins.Immediate == 2 {
			return false
		}
		next := pc + 1
		switch ins.Code {
		case 0xbf:
			reg[dst] = reg[src]
		case 0x79:
			if src != 7 {
				t.Fatalf("unexpected budget read %+v", ins)
			}
			reg[dst] = *shared
		case 0x57:
			reg[dst] &= uint64(int64(ins.Immediate))
		case 0x47:
			reg[dst] |= uint64(int64(ins.Immediate))
		case 0x07:
			reg[dst] += uint64(int64(ins.Immediate))
		case 0x05:
			next += int(ins.Offset)
		case 0x2d:
			if reg[dst] > reg[src] {
				next += int(ins.Offset)
			}
		case 0xad:
			if reg[dst] < reg[src] {
				next += int(ins.Offset)
			}
		case 0x35:
			if reg[dst] >= uint64(ins.Immediate) {
				next += int(ins.Offset)
			}
		case 0x1d:
			if reg[dst] == reg[src] {
				next += int(ins.Offset)
			}
		case 0xdb:
			if beforeCAS != nil {
				beforeCAS()
				beforeCAS = nil
			}
			old := *shared
			if old == reg[0] {
				*shared = reg[src]
			}
			reg[0] = old
		default:
			t.Fatalf("unexpected assembled budget opcode: pc%d %+v", pc, ins)
		}
		pc = next
	}
	t.Fatal("fixed budget CAS exceeded its bounded instruction section")
	return false
}
func TestAssembledBudgetRejectsDelayedEpochWithoutReopeningNewerAllowance(t *testing.T) {
	shared := uint64(101<<16 | 5000)
	if assembledBudgetClaim(t, 100, &shared, nil) || shared != 101<<16|5000 {
		t.Fatalf("delayed packet rewound budget: %d", shared)
	}
	shared = 100<<16 | 4999
	if assembledBudgetClaim(t, 100, &shared, func() { shared = 101<<16 | 5000 }) || shared != 101<<16|5000 {
		t.Fatalf("CAS interleave rewound newer epoch: %d", shared)
	}
	if !assembledBudgetClaim(t, 102, &shared, nil) || shared != 102<<16|1 {
		t.Fatalf("new epoch not admitted: %d", shared)
	}
	if !assembledBudgetClaim(t, 102, &shared, nil) || shared != 102<<16|2 {
		t.Fatalf("same epoch count not monotonic: %d", shared)
	}
}

func TestPendingRowsAndUnacknowledgedBatchShareOneFiniteBudget(t *testing.T) {
	at := time.Now().UTC()
	o := fixtureObserver(at)
	for i := 0; i < ObserverMaxPendingRows; i++ {
		p := observedPacket(uint64(i+1), 2, 3, "192.0.2.1", 8080, number(1))
		p.at = o.monotonic
		o.acceptPacket(p)
	}
	rows, e, err := o.Drain(t.Context(), at)
	if err != nil || len(rows) != ObserverMaxPendingRows {
		t.Fatalf("bounded initial batch rows=%d err=%v", len(rows), err)
	}
	for i := 0; i < 100; i++ {
		p := observedPacket(uint64(ObserverMaxPendingRows+i+1), 2, 3, "192.0.2.1", 8080, number(1))
		p.at = o.monotonic
		o.acceptPacket(p)
	}
	if len(o.pending) != 0 || o.status.Quality.PendingOmissions != 100 || len(o.inflight.rows) != ObserverMaxPendingRows {
		t.Fatal("failed-write backlog exceeded shared row cap")
	}
	if err = o.Acknowledge(e.BatchID); err != nil {
		t.Fatal(err)
	}
	p := observedPacket(9000, 2, 3, "192.0.2.1", 8080, number(1))
	p.at = o.monotonic
	o.acceptPacket(p)
	rows, e, err = o.Drain(t.Context(), at)
	if err != nil || len(rows) != 1 || e.Quality.PendingOmissions != 100 {
		t.Fatalf("acknowledgement hid backlog omissions: %+v err=%v", e, err)
	}
}

func TestRestartClearsDeadProcessAttachmentMarkerAndDoesNotAttach(t *testing.T) {
	st, _ := testStore(t)
	state := persistedState{Settings: DefaultSettings, Observer: &ObserverEvidence{Status: "recording", AttachmentsRetained: true, BatchID: "previous:1"}}
	state.Settings.KernelObserverEnabled = true
	if _, err := st.policy(t.Context(), state, time.Now()); err != nil {
		t.Fatal(err)
	}
	o := NewKernelObserver(nil)
	s := New(st, nil)
	s.SetObserver(o)
	if err := s.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	r, err := s.Report(t.Context(), Query{})
	if err != nil || r.Settings.KernelObserverEnabled || r.KernelObserver.Status != "interrupted" || r.KernelObserver.AttachmentsRetained || r.KernelObserver.BatchID != "" || o.kernel != nil || !r.KernelObserver.Quality.ShutdownTailUnknown {
		t.Fatalf("restart silently retained or reopened old session: %+v err=%v", r, err)
	}
}
