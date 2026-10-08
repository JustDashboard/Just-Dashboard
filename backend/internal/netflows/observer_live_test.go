package netflows

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

func observerCgroup(t *testing.T) *os.File {
	t.Helper()
	if os.Getenv("JD_NETFLOWS_OBSERVER_LIVE") != "1" {
		t.Skip("set JD_NETFLOWS_OBSERVER_LIVE=1 for the declared disposable kernel fixture")
	}
	if os.Geteuid() != 0 {
		t.Fatal("observer fixture requires root; no production target is attached")
	}
	path := filepath.Join("/sys/fs/cgroup", fmt.Sprintf("jd-flow-fixture-%x", time.Now().UnixNano()))
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Remove(path); err != nil {
			t.Errorf("remove exact fixture cgroup: %v", err)
		}
	})
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })
	return file
}
func TestLiveFixedObserverVerifierAndOwnedLinks(t *testing.T) {
	cg := observerCgroup(t)
	k, err := openKernelSession(cg, ObserverRingBytes)
	if err != nil {
		t.Fatal(err)
	}
	for _, link := range k.links {
		if err = link.verify(); err != nil {
			t.Fatal(err)
		}
	}
	stats, err := bpfStats(k.stats)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("programDigest=%s links=%+v stats=%v", observerProgramDigest(), k.links, stats)
	if err = k.close(); err != nil {
		t.Fatal(err)
	}
}

func TestLiveKernelShortTCPAndUDPByteObservations(t *testing.T) {
	cg := observerCgroup(t)
	ns := fmt.Sprintf("jdfo-%x", time.Now().UnixNano())
	nativeExec(t, "ip", "netns", "add", ns)
	t.Cleanup(func() { nativeExec(t, "ip", "netns", "delete", ns) })
	nativeExec(t, "ip", "-n", ns, "link", "set", "lo", "up")
	k, err := openKernelSession(cg, ObserverRingBytes)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := k.close(); err != nil {
			t.Error(err)
		}
	})
	cmd := exec.CommandContext(t.Context(), "ip", "netns", "exec", ns, os.Args[0], "-test.run=^TestFlowFixtureProcess$")
	cmd.ExtraFiles = []*os.File{cg}
	cmd.Env = append(os.Environ(), "JD_NETFLOWS_HELPER=1", "JD_NETFLOWS_TEST_CGROUP="+cg.Name(), "JD_NETFLOWS_TEST_CGROUP_FD=3")
	p := startFixture(t, cmd)
	read := func() []kernelPacket {
		var rows []kernelPacket
		data := make([]byte, observerRecordBytes)
		deadline := time.Now().Add(200 * time.Millisecond)
		for time.Now().Before(deadline) {
			ok, err := k.read(data)
			if err != nil {
				t.Fatal(err)
			}
			if ok {
				r, err := decodeObserver(data)
				if err != nil {
					t.Fatal(err)
				}
				rows = append(rows, r)
			} else {
				time.Sleep(time.Millisecond)
			}
		}
		return rows
	}
	initial := read()
	t.Logf("initial events=%d", len(initial))
	p.in.Write([]byte("short\n"))
	short := p.read(t)
	rows := read()
	seen := map[int]bool{}
	fin := map[int]bool{}
	for _, row := range rows {
		if row.direction == 1 && row.socket.Protocol == "tcp" && row.socket.RemotePort == p.ready.ServerPort {
			seen[row.socket.LocalPort] = true
			if row.flags&1 != 0 {
				fin[row.socket.LocalPort] = true
			}
		}
	}
	for _, port := range short.ShortPorts {
		if !seen[port] || !fin[port] {
			t.Errorf("short TCP port%d observed=%v fin=%v", port, seen[port], fin[port])
		}
	}
	p.in.Write([]byte("datagrams\n"))
	udp := p.read(t)
	rows = read()
	wanted := map[int]bool{}
	for _, port := range udp.ShortPorts {
		wanted[port] = true
	}
	var tx, rx uint64
	var sent, received int
	for _, row := range rows {
		if row.socket.Protocol != "udp" {
			continue
		}
		if row.payload == nil {
			t.Fatalf("ordinary UDP bytes unknown %+v", row)
		}
		if row.direction == 1 && wanted[row.socket.LocalPort] {
			tx += *row.payload
			sent++
		}
		if row.direction == 0 && wanted[row.socket.RemotePort] {
			rx += *row.payload
			received++
		}
	}
	if tx != uint64(udp.Payload) || rx != uint64(udp.Payload) || sent != 32 || received != 32 {
		t.Fatalf("UDP bytes tx=%d rx=%d packets=%d/%d wanted=%d", tx, rx, sent, received, udp.Payload)
	}
	stats, err := bpfStats(k.stats)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("short TCP observed=%d/32 FIN=%d/32; UDP tx=%d rx=%d datagrams=%d/%d; stats=%v; digest=%s", len(seen), len(fin), tx, rx, sent, received, stats, observerProgramDigest())
	if stats[1] != 0 || stats[2] != 0 || stats[5] != 0 {
		t.Errorf("fixture exceeded declared capacity: %v", stats)
	}
	if strings.Contains(cg.Name(), "..") {
		t.Fatal("fixture ownership changed")
	}
}

func TestLiveKernelRecorderPersistsDistinctShortTCPAndUDPPeers(t *testing.T) {
	cg := observerCgroup(t)
	ns := fmt.Sprintf("jdfo-%x", time.Now().UnixNano())
	nativeExec(t, "ip", "netns", "add", ns)
	t.Cleanup(func() { nativeExec(t, "ip", "netns", "delete", ns) })
	nativeExec(t, "ip", "-n", ns, "link", "set", "lo", "up")
	o := NewKernelObserver(nil)
	o.target = strings.TrimPrefix(cg.Name(), "/sys/fs/cgroup")
	if err := o.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, err := o.Stop(ctx); err != nil {
			t.Error(err)
		}
	})
	cmd := exec.CommandContext(t.Context(), "ip", "netns", "exec", ns, os.Args[0], "-test.run=^TestFlowFixtureProcess$")
	cmd.ExtraFiles = []*os.File{cg}
	cmd.Env = append(os.Environ(), "JD_NETFLOWS_HELPER=1", "JD_NETFLOWS_TEST_CGROUP="+cg.Name(), "JD_NETFLOWS_TEST_CGROUP_FD=3")
	p := startFixture(t, cmd)
	short := p.command(t, "short")
	udp := p.command(t, "datagrams")
	want := map[int]bool{}
	for _, port := range udp.ShortPorts {
		want[port] = true
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		o.mu.Lock()
		seen := map[int]bool{}
		for _, b := range o.pending {
			if b.Socket.Protocol == "udp" && want[b.Socket.LocalPort] && b.ObservedTxPackets > 0 {
				seen[b.Socket.LocalPort] = true
			}
		}
		o.mu.Unlock()
		if len(seen) == 32 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("reader missed short UDP senders: %d/32", len(seen))
		}
		time.Sleep(10 * time.Millisecond)
	}
	at := time.Now().UTC()
	rows, evidence, err := o.Drain(t.Context(), at)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := testStore(t)
	state := persistedState{Settings: DefaultSettings, Observer: &evidence}
	state.Settings.Enabled, state.Settings.KernelObserverEnabled = true, true
	c := Cycle{At: at, FinishedAt: at, Sources: []Source{}, BootID: evidence.BootID, Observer: &evidence, DroppedEvents: &evidence.Quality.RingDrops, DockerStatus: evidence.DockerStatus}
	state, err = st.record(t.Context(), state, c, rows)
	if err != nil {
		t.Fatal(err)
	}
	if err = o.Acknowledge(evidence.BatchID); err != nil {
		t.Fatal(err)
	}
	s := New(st, nil)
	s.started = true
	s.state = state
	s.observer = o
	q := Query{From: at.Truncate(time.Hour), To: at.Truncate(time.Hour).Add(time.Hour), Limit: 1000}
	raw, err := s.Export(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	var report Report
	if err = json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	seen, fin := map[int]bool{}, map[int]bool{}
	udpTX, udpRX := uint64(0), uint64(0)
	udpSenders, udpReceivers := map[int]bool{}, map[int]bool{}
	ids := map[string]bool{}
	for _, b := range report.Rows {
		if ids[b.ID] {
			t.Fatalf("duplicate native stored identity: %s", b.ID)
		}
		ids[b.ID] = true
		if b.Socket.Protocol == "tcp" && b.Socket.RemotePort == p.ready.ServerPort {
			seen[b.Socket.LocalPort] = true
			if b.ObservedFIN > 0 {
				fin[b.Socket.LocalPort] = true
			}
		}
		if b.Socket.Protocol == "udp" && want[b.Socket.LocalPort] {
			if b.ObservedTxBytes == nil || b.ObservedTxByteGaps != 0 {
				t.Fatalf("ordinary UDP sender quality=%+v", b)
			}
			udpTX += *b.ObservedTxBytes
			udpSenders[b.Socket.LocalPort] = true
		}
		if b.Socket.Protocol == "udp" && want[b.Socket.RemotePort] {
			if b.ObservedRxBytes == nil || b.ObservedRxByteGaps != 0 {
				t.Fatalf("ordinary UDP receiver quality=%+v", b)
			}
			udpRX += *b.ObservedRxBytes
			udpReceivers[b.Socket.RemotePort] = true
		}
	}
	for _, port := range short.ShortPorts {
		if !seen[port] || !fin[port] {
			t.Errorf("stored short sender %d observed=%v FIN=%v", port, seen[port], fin[port])
		}
	}
	if udpTX != uint64(udp.Payload) || udpRX != uint64(udp.Payload) || len(udpSenders) != 32 || len(udpReceivers) != 32 {
		t.Fatalf("stored UDP differential: tx=%d rx=%d senders=%d receivers=%d want=%d", udpTX, udpRX, len(udpSenders), len(udpReceivers), udp.Payload)
	}
	if report.CoverageHours[0].ObserverQuality.RingDrops != 0 || report.CoverageHours[0].ObserverQuality.BudgetOmissions != 0 {
		t.Fatalf("native persistence fixture loss: %+v", report.CoverageHours[0].ObserverQuality)
	}
	t.Logf("digest=%s storedRows=%d shortSenderFIN=%d/32 UDPsender/receiver=%d/%d bytes=%d/%d quality=%+v exportBytes=%d", evidence.Digest, len(report.Rows), len(fin), len(udpSenders), len(udpReceivers), udpTX, udpRX, report.CoverageHours[0].ObserverQuality, len(raw))
}

func TestLiveKernelObserverPreservesForeignProgramThroughRefusedDetachAndRetry(t *testing.T) {
	cg := observerCgroup(t)
	prog, err := bpfLoad([]bpfInstruction{{Code: 0xb7, Immediate: 1}, {Code: 0x95}}, unix.BPF_PROG_TYPE_CGROUP_SKB, unix.BPF_CGROUP_INET_INGRESS)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unix.Close(prog) })
	info, err := bpfInfo(prog)
	if err != nil {
		t.Fatal(err)
	}
	fd, err := bpfAttach(prog, int(cg.Fd()), unix.BPF_CGROUP_INET_INGRESS)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unix.Close(fd) })
	infoLink, err := bpfInfo(fd)
	if err != nil {
		t.Fatal(err)
	}
	group, err := cgroupHandle(cg)
	if err != nil {
		t.Fatal(err)
	}
	foreign := ownedObserverLink{fd: fd, id: binary.LittleEndian.Uint32(infoLink[4:8]), program: binary.LittleEndian.Uint32(info[4:8]), attach: unix.BPF_CGROUP_INET_INGRESS, cgroup: group}
	k, err := openKernelSession(cg, ObserverRingBytes)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := k.close(); err != nil {
			t.Error(err)
		}
	})
	saved := k.links[1].id
	k.links[1].id++
	if err = k.close(); err == nil || k.closed || k.links[1].fd < 0 || k.stats < 0 {
		t.Fatalf("unverified descriptor was released: k=%+v err=%v", k, err)
	}
	if err = foreign.verify(); err != nil {
		t.Fatalf("foreign attachment changed after refusal: %v", err)
	}
	k.links[1].id = saved
	if err = k.close(); err != nil || !k.closed {
		t.Fatalf("owned detach retry=%v", err)
	}
	if err = foreign.verify(); err != nil {
		t.Fatalf("foreign attachment changed after retry: %v", err)
	}
	t.Logf("foreignLink=%d foreignProgram=%d preserved through owned refusal and retry; digest=%s", foreign.id, foreign.program, observerProgramDigest())
}

func fixtureBudgetWrite(fd int, value uint64) error {
	key := uint32(0)
	var pin runtime.Pinner
	pin.Pin(&key)
	pin.Pin(&value)
	defer pin.Unpin()
	attr := struct {
		FD, Pad           uint32
		Key, Value, Flags uint64
	}{FD: uint32(fd), Key: uint64(uintptr(unsafe.Pointer(&key))), Value: uint64(uintptr(unsafe.Pointer(&value)))}
	_, err := bpfCall(unix.BPF_MAP_UPDATE_ELEM, unsafe.Pointer(&attr), unsafe.Sizeof(attr))
	return err
}
func TestLiveKernelObserverReportsRingSaturationAndBudgetOmissions(t *testing.T) {
	cg := observerCgroup(t)
	ns := fmt.Sprintf("jdfo-%x", time.Now().UnixNano())
	nativeExec(t, "ip", "netns", "add", ns)
	t.Cleanup(func() { nativeExec(t, "ip", "netns", "delete", ns) })
	nativeExec(t, "ip", "-n", ns, "link", "set", "lo", "up")
	cmd := exec.CommandContext(t.Context(), "ip", "netns", "exec", ns, os.Args[0], "-test.run=^TestFlowFixtureProcess$")
	cmd.ExtraFiles = []*os.File{cg}
	cmd.Env = append(os.Environ(), "JD_NETFLOWS_HELPER=1", "JD_NETFLOWS_TEST_CGROUP="+cg.Name(), "JD_NETFLOWS_UDP_ONLY=1", "JD_NETFLOWS_TEST_CGROUP_FD=3")
	p := startFixture(t, cmd)
	baseline := p.command(t, "burst")
	k, err := openKernelSession(cg, 4096)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := k.close(); err != nil {
			t.Error(err)
		}
	})
	// The original peer predates attachment and cannot acquire an identity by
	// inference. First prove that it contributes an identity gap, not bytes.
	unknown := p.command(t, "burst")
	stats, err := bpfStats(k.stats)
	if err != nil {
		t.Fatal(err)
	}
	if stats[4] == 0 || stats[0] != 0 {
		t.Fatalf("preexisting socket falsely attributed: %v", stats)
	}
	p.command(t, "renew")
	_, mono, err := monotonicNow()
	if err != nil {
		t.Fatal(err)
	}
	for mono%1000000000 > 700000000 {
		time.Sleep(10 * time.Millisecond)
		_, mono, err = monotonicNow()
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = fixtureBudgetWrite(k.budget, (mono/1000000000)<<16|ObserverEventsPerSecond); err != nil {
		t.Fatal(err)
	}
	p.command(t, "datagrams")
	// Each newly created short UDP peer and the listener's supported direction
	// share a finite ring. No reader runs, so a second burst of new peers fills it.
	p.command(t, "datagrams")
	stats, err = bpfStats(k.stats)
	if err != nil {
		t.Fatal(err)
	}
	if stats[2] == 0 {
		t.Fatalf("forced full epoch did not report omissions: %v", stats)
	}
	// Open the next owned epoch explicitly; admitted events now exceed ring size.
	_, mono, err = monotonicNow()
	if err != nil {
		t.Fatal(err)
	}
	if err = fixtureBudgetWrite(k.budget, (mono/1000000000)<<16); err != nil {
		t.Fatal(err)
	}
	p.command(t, "datagrams")
	p.command(t, "datagrams")
	measured := p.command(t, "burst")
	stats, err = bpfStats(k.stats)
	if err != nil {
		t.Fatal(err)
	}
	if stats[1] == 0 {
		t.Fatalf("tiny ring saturation omitted drops: %v", stats)
	}
	t.Logf("owned 4KiB ring saturation stats=%v; 10k-datagram baseline=%dns identity-gap=%dns saturated observer=%dns ratio=%.3f; digest=%s", stats, baseline.ElapsedNanos, unknown.ElapsedNanos, measured.ElapsedNanos, float64(measured.ElapsedNanos)/float64(baseline.ElapsedNanos), observerProgramDigest())
}

func TestLiveNestedBPFBuffersSurviveConcurrentGCAndStackGrowth(t *testing.T) {
	cg := observerCgroup(t)
	k, err := openKernelSession(cg, ObserverRingBytes)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := k.close(); err != nil {
			t.Error(err)
		}
	})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for i := 0; i < 40; i++ {
			runtime.GC()
		}
	}()
	for i := 0; i < 100; i++ {
		if _, err = bpfStats(k.stats); err != nil {
			t.Fatal(err)
		}
		for _, l := range k.links {
			if err = l.verify(); err != nil {
				t.Fatal(err)
			}
		}
	}
	<-finished
	t.Log("pinned nested info/stat buffers survived 100 reads during 40 concurrent garbage collections")
}
