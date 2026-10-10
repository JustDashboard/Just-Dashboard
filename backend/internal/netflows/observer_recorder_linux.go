package netflows

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"golang.org/x/sys/unix"
)

type EventObserver interface {
	Start(context.Context) error
	Drain(context.Context, time.Time) ([]Bucket, ObserverEvidence, error)
	Stop(context.Context) (ObserverEvidence, error)
	Status() ObserverEvidence
	Acknowledge(string) error
}

type observerBatch struct {
	rows     []Bucket
	evidence ObserverEvidence
	baseline ObserverQuality
}

type KernelObserver struct {
	mu                 sync.Mutex
	docker             DockerSources
	kernel             *kernelSession
	bindings           []observerBinding
	status             ObserverEvidence
	cancel             context.CancelFunc
	done               chan struct{}
	pending            map[string]Bucket
	previous           ObserverQuality
	inflight           *observerBatch
	sequence           uint64
	session            string
	timestampUncertain bool
	readStats          func(int) ([6]uint64, error)
	readBoot           func() ([]byte, error)
	readClock          func() (time.Time, uint64, error)
	wall               time.Time
	monotonic          uint64
	target             string
	ringBytes          int
}

func NewKernelObserver(docker DockerSources) *KernelObserver {
	return &KernelObserver{docker: docker, target: "/", ringBytes: ObserverRingBytes, status: ObserverEvidence{Status: "off", Reason: "The packaged kernel observer is not enabled. Exact helper and multi-link support are checked only on explicit attachment.", Digest: observerProgramDigest(), RingBytes: ObserverRingBytes, EventsPerSecond: ObserverEventsPerSecond, SocketCapacity: 4096, PendingCapacity: ObserverMaxPendingRows, ProgramIDs: []uint32{}, LinkIDs: []uint32{}}}
}
func observerCapabilityReason() string {
	if runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		return "The fixed observer supports Linux amd64 and arm64 only."
	}
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return "Effective kernel observer capabilities are unreadable."
	}
	var caps uint64
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "CapEff:") {
			caps, _ = strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "CapEff:")), 16, 64)
		}
	}
	if caps&(1<<unix.CAP_NET_ADMIN) == 0 || caps&((1<<unix.CAP_BPF)|(1<<unix.CAP_SYS_ADMIN)) == 0 {
		return "Kernel capture requires CAP_NET_ADMIN plus CAP_BPF or CAP_SYS_ADMIN in the host privilege domain."
	}
	release, err := os.ReadFile(hostexec.HostPath("/proc/sys/kernel/osrelease"))
	if err != nil {
		return "The native kernel version is unreadable."
	}
	parts := strings.Split(strings.TrimSpace(string(release)), ".")
	if len(parts) < 2 {
		return "The native kernel version is unknown."
	}
	major, e1 := strconv.Atoi(parts[0])
	minor, e2 := strconv.Atoi(parts[1])
	if e1 != nil || e2 != nil || major < 6 || major == 6 && minor < 1 {
		return "The fixed observer requires Linux 6.1 or newer and successful verification of its exact helpers and links."
	}
	return ""
}
func (o *KernelObserver) Start(ctx context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.kernel != nil {
		return fmt.Errorf("%w: stop the previous observer before starting another", ErrUnavailable)
	}
	status := ObserverEvidence{Status: "unavailable", Digest: observerProgramDigest(), CheckedAt: time.Now().UTC(), RingBytes: o.ringBytes, EventsPerSecond: ObserverEventsPerSecond, SocketCapacity: 4096, PendingCapacity: ObserverMaxPendingRows, ProgramIDs: []uint32{}, LinkIDs: []uint32{}}
	refuse := func(err error) error {
		status.Reason = clip(err.Error(), 1024)
		status.AttachmentsRetained = o.kernel != nil
		o.status = status
		return fmt.Errorf("%w: %s", ErrUnavailable, status.Reason)
	}
	if reason := observerCapabilityReason(); reason != "" {
		return refuse(fmt.Errorf("%s", reason))
	}
	boot, err := os.ReadFile(hostexec.HostPath("/proc/sys/kernel/random/boot_id"))
	if err != nil {
		return refuse(err)
	}
	status.BootID = strings.TrimSpace(string(boot))
	if len(status.BootID) != 36 {
		return refuse(fmt.Errorf("native boot identity is unknown"))
	}
	release, err := os.ReadFile(hostexec.HostPath("/proc/sys/kernel/osrelease"))
	if err != nil {
		return refuse(err)
	}
	status.KernelRelease = strings.TrimSpace(string(release))
	group, err := openHostCgroup(o.target)
	if err != nil {
		return refuse(err)
	}
	defer group.Close()
	id, err := cgroupHandle(group)
	if err != nil {
		return refuse(err)
	}
	status.TargetCgroup = fmt.Sprint(id)
	bindCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	bindings, omitted, bindErr := captureObserverBindings(bindCtx, o.docker)
	cancel()
	status.DockerSources, status.OmittedDockerSources = len(bindings), omitted
	status.DockerCheckedAt = time.Now().UTC()
	status.DockerStatus = "observed"
	if bindErr != nil {
		status.DockerStatus = "unavailable"
		status.DockerError = clip(bindErr.Error(), 512)
	}
	if omitted > 0 {
		status.DockerStatus = "partial"
	}
	wall, mono, err := monotonicNow()
	if err != nil {
		for i := range bindings {
			bindings[i].close()
		}
		return refuse(err)
	}
	var sessionBytes [16]byte
	if _, err := rand.Read(sessionBytes[:]); err != nil {
		for i := range bindings {
			bindings[i].close()
		}
		return refuse(err)
	}
	k, err := openKernelSession(group, o.ringBytes)
	if err != nil {
		if k != nil {
			o.kernel, o.bindings = k, bindings
			o.done = make(chan struct{})
			close(o.done)
			for _, link := range k.links {
				if link.fd >= 0 {
					status.ProgramIDs = append(status.ProgramIDs, link.program)
					status.LinkIDs = append(status.LinkIDs, link.id)
				}
			}
		} else {
			for i := range bindings {
				bindings[i].close()
			}
		}
		return refuse(err)
	}
	if ctx.Err() != nil {
		if cleanupErr := k.close(); cleanupErr != nil {
			o.kernel, o.bindings = k, bindings
			o.done = make(chan struct{})
			close(o.done)
			return refuse(fmt.Errorf("cancelled attachment cleanup: %w", cleanupErr))
		}
		for i := range bindings {
			bindings[i].close()
		}
		return refuse(ctx.Err())
	}
	for _, link := range k.links {
		status.ProgramIDs = append(status.ProgramIDs, link.program)
		status.LinkIDs = append(status.LinkIDs, link.id)
	}
	now := time.Now().UTC()
	status.StartedAt = &now
	status.CheckedAt = now
	status.Status = "recording"
	status.AttachmentsRetained = true
	status.Reason = "Bounded cgroup TCP/UDP header events; transport observations include retransmitted packets and are not application or delivery totals."
	o.kernel, o.bindings, o.status, o.wall, o.monotonic = k, bindings, status, wall, mono
	o.previous = ObserverQuality{}
	o.inflight, o.sequence, o.session = nil, 0, hex.EncodeToString(sessionBytes[:])
	o.timestampUncertain = false
	o.pending = map[string]Bucket{}
	runCtx, stop := context.WithCancel(context.Background())
	o.cancel = stop
	o.done = make(chan struct{})
	go o.run(runCtx, k)
	return nil
}
func kernelQuality(v [6]uint64) ObserverQuality {
	return ObserverQuality{Events: v[0], RingDrops: v[1], BudgetOmissions: v[2], HeaderGaps: v[3], IdentityGaps: v[4], StateAdmissionGaps: v[5]}
}
func (o *KernelObserver) run(ctx context.Context, k *kernelSession) {
	defer close(o.done)
	defer func() {
		o.mu.Lock()
		if stats, err := o.kernelStats(k.stats); err == nil {
			o.setKernelStats(stats)
		} else {
			o.status.Status = "unavailable"
			o.status.Reason = "Final kernel quality counters could not be read."
		}
		o.status.Quality.ShutdownTailUnknown = true
		now := time.Now().UTC()
		o.status.StoppedAt = &now
		o.status.CheckedAt = now
		o.mu.Unlock()
	}()
	data := make([]byte, observerRecordBytes)
	for ctx.Err() == nil {
		fds := []unix.PollFd{{Fd: int32(k.ring), Events: unix.POLLIN}}
		_, err := unix.Poll(fds, 100)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			o.failReader(err)
			return
		}
		start := time.Now()
		count := 0
		for ctx.Err() == nil && count < 5000 {
			ok, err := k.read(data)
			if err != nil {
				o.failReader(err)
				return
			}
			if !ok {
				break
			}
			count++
			packet, err := decodeObserver(data)
			o.mu.Lock()
			if err != nil {
				o.status.Quality.ParserGaps++
			} else {
				o.acceptPacket(packet)
			}
			o.mu.Unlock()
			if time.Since(start) >= 5*time.Millisecond {
				o.mu.Lock()
				o.status.Quality.ReaderBudgetPauses++
				o.mu.Unlock()
				select {
				case <-ctx.Done():
				case <-time.After(20 * time.Millisecond):
				}
				break
			}
		}
	}
}

// acceptPacket runs under the recorder mutex; the outstanding immutable batch
// and current map share one row budget during storage retries.
func (o *KernelObserver) acceptPacket(packet kernelPacket) {
	if packet.byteGap {
		o.status.Quality.ByteGaps++
	}
	if packet.at < o.monotonic {
		o.status.Quality.ParserGaps++
		return
	}
	at := o.wall.Add(time.Duration(packet.at - o.monotonic))
	b := packetBucket(o.status.BootID, packet, at)
	if o.timestampUncertain {
		b.TimestampUncertain = true
		o.status.Quality.TimestampGaps++
	}
	key := fmt.Sprintf("%s:%d", b.ID, b.Hour.Unix())
	if old, exists := o.pending[key]; exists {
		o.pending[key] = merge(old, b)
	} else if len(o.pending)+o.inflightRows() < ObserverMaxPendingRows {
		o.pending[key] = b
	} else {
		o.status.Quality.PendingOmissions++
	}
}
func (o *KernelObserver) failReader(err error) {
	o.mu.Lock()
	o.status.Status = "unavailable"
	o.status.Reason = "Kernel event reader failed: " + clip(err.Error(), 512)
	o.mu.Unlock()
}
func (o *KernelObserver) setKernelStats(v [6]uint64) {
	q := kernelQuality(v)
	o.status.Quality.Events = q.Events
	o.status.Quality.RingDrops = q.RingDrops
	o.status.Quality.BudgetOmissions = q.BudgetOmissions
	o.status.Quality.HeaderGaps = q.HeaderGaps
	o.status.Quality.IdentityGaps = q.IdentityGaps
	o.status.Quality.StateAdmissionGaps = q.StateAdmissionGaps
}
func (o *KernelObserver) Status() ObserverEvidence {
	o.mu.Lock()
	defer o.mu.Unlock()
	s := o.status
	s.ProgramIDs = append([]uint32{}, s.ProgramIDs...)
	s.LinkIDs = append([]uint32{}, s.LinkIDs...)
	if s.Status == "off" {
		if reason := observerCapabilityReason(); reason != "" {
			s.Status = "unavailable"
			s.Reason = reason
		}
	}
	return s
}
func countDifference(a, b uint64) uint64 {
	if a < b {
		return 0
	}
	return a - b
}
func qualityDifference(a, b ObserverQuality) ObserverQuality {
	return ObserverQuality{Events: countDifference(a.Events, b.Events), RingDrops: countDifference(a.RingDrops, b.RingDrops), BudgetOmissions: countDifference(a.BudgetOmissions, b.BudgetOmissions), HeaderGaps: countDifference(a.HeaderGaps, b.HeaderGaps), IdentityGaps: countDifference(a.IdentityGaps, b.IdentityGaps), StateAdmissionGaps: countDifference(a.StateAdmissionGaps, b.StateAdmissionGaps), ParserGaps: countDifference(a.ParserGaps, b.ParserGaps), ByteGaps: countDifference(a.ByteGaps, b.ByteGaps), PendingOmissions: countDifference(a.PendingOmissions, b.PendingOmissions), AttributionGaps: countDifference(a.AttributionGaps, b.AttributionGaps), ReaderBudgetPauses: countDifference(a.ReaderBudgetPauses, b.ReaderBudgetPauses), UnsavedEvents: countDifference(a.UnsavedEvents, b.UnsavedEvents), TimestampGaps: countDifference(a.TimestampGaps, b.TimestampGaps), ShutdownTailUnknown: a.ShutdownTailUnknown}
}
func (o *KernelObserver) inflightRows() int {
	if o.inflight != nil {
		return len(o.inflight.rows)
	}
	return 0
}
func (o *KernelObserver) kernelStats(fd int) ([6]uint64, error) {
	if o.readStats != nil {
		return o.readStats(fd)
	}
	return bpfStats(fd)
}
func (o *KernelObserver) nativeBoot() ([]byte, error) {
	if o.readBoot != nil {
		return o.readBoot()
	}
	return os.ReadFile(hostexec.HostPath("/proc/sys/kernel/random/boot_id"))
}
func (o *KernelObserver) checkClock() {
	clock := monotonicNow
	if o.readClock != nil {
		clock = o.readClock
	}
	wall, mono, err := clock()
	reason := ""
	if err != nil || mono < o.monotonic {
		reason = "The monotonic/wall clock mapping could not be reverified."
	} else {
		drift := wall.Sub(o.wall.Add(time.Duration(mono - o.monotonic)))
		if drift > 250*time.Millisecond || drift < -250*time.Millisecond {
			reason = "The wall clock changed during this session; UTC placement is an uncertain monotonic projection."
		}
	}
	if reason != "" {
		o.timestampUncertain = true
		o.status.TimestampReason = reason
		for id, b := range o.pending {
			if !b.TimestampUncertain {
				b.TimestampUncertain = true
				o.status.Quality.TimestampGaps += b.ObservedPackets
				o.pending[id] = b
			}
		}
	}
}
func (o *KernelObserver) Drain(ctx context.Context, at time.Time) ([]Bucket, ObserverEvidence, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	// A batch is immutable until its SQLite transaction is acknowledged. New
	// events use a separately bounded map; retries do not change attribution.
	if o.inflight != nil {
		return append([]Bucket(nil), o.inflight.rows...), o.inflight.evidence, nil
	}
	if o.kernel == nil {
		return nil, o.status, nil
	}
	boot, err := o.nativeBoot()
	if err != nil || strings.TrimSpace(string(boot)) != o.status.BootID {
		return nil, o.status, fmt.Errorf("%w: native boot identity changed", ErrUnavailable)
	}
	stats, err := o.kernelStats(o.kernel.stats)
	if err != nil {
		return nil, o.status, err
	}
	o.setKernelStats(stats)
	o.checkClock()
	bindCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	fresh, omitted, bindErr := captureObserverBindings(bindCtx, o.docker)
	cancel()
	o.status.DockerCheckedAt = at
	o.status.DockerStatus, o.status.DockerError = "observed", ""
	if bindErr != nil {
		o.status.DockerStatus, o.status.DockerError = "unavailable", clip(bindErr.Error(), 512)
	} else {
		if omitted > 0 {
			o.status.DockerStatus = "partial"
		}
		for i := range fresh {
			for j := range o.bindings {
				old := &o.bindings[j]
				if old.source.ID == fresh[i].source.ID && old.source.PID == fresh[i].source.PID && old.source.StartedAt == fresh[i].source.StartedAt && old.groupID == fresh[i].groupID && old.namespaceCookie == fresh[i].namespaceCookie && old.namespaceIdentity == fresh[i].namespaceIdentity {
					fresh[i].capturedAt = old.capturedAt
					break
				}
			}
		}
		for i := range o.bindings {
			o.bindings[i].close()
		}
		o.bindings = fresh
		o.status.DockerSources, o.status.OmittedDockerSources = len(fresh), omitted
	}
	valid := map[uint64]bool{}
	verifyCtx, verifyCancel := context.WithTimeout(ctx, time.Second)
	defer verifyCancel()
	for i := range o.bindings {
		if verifyCtx.Err() != nil {
			break
		}
		valid[o.bindings[i].groupID] = bindErr == nil && verifyObserverBinding(verifyCtx, o.docker, &o.bindings[i]) == nil
	}
	rows := make([]Bucket, 0, len(o.pending))
	for _, b := range o.pending {
		owner, verified := observerOwner(&b, o.bindings, valid)
		if b.TimestampUncertain {
			owner, verified = b.Socket.Owner, false
		}
		b.Socket.Owner = owner
		if !verified {
			o.status.Quality.AttributionGaps += b.ObservedPackets
		}
		sum := sha256.Sum256([]byte(ownerIdentity(owner)))
		b.ID += ":" + hex.EncodeToString(sum[:])
		rows = append(rows, b)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	o.pending = map[string]Bucket{}
	o.status.CheckedAt = at
	status := o.status
	status.Quality = qualityDifference(o.status.Quality, o.previous)
	if status.Status == "recording" && (status.Quality.RingDrops+status.Quality.BudgetOmissions+status.Quality.HeaderGaps+status.Quality.IdentityGaps+status.Quality.StateAdmissionGaps+status.Quality.ParserGaps+status.Quality.ByteGaps+status.Quality.PendingOmissions+status.Quality.AttributionGaps+status.Quality.TimestampGaps > 0 || status.DockerStatus != "observed") {
		status.Status = "partial"
	}
	o.sequence++
	status.BatchID = fmt.Sprintf("%s:%d", o.session, o.sequence)
	o.inflight = &observerBatch{rows: rows, evidence: status, baseline: o.status.Quality}
	return append([]Bucket(nil), rows...), status, nil
}
func (o *KernelObserver) Acknowledge(id string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.inflight == nil || o.inflight.evidence.BatchID != id {
		return fmt.Errorf("%w: observer batch acknowledgement changed", ErrInvalid)
	}
	o.previous = o.inflight.baseline
	o.inflight = nil
	return nil
}
func (o *KernelObserver) Stop(ctx context.Context) (ObserverEvidence, error) {
	o.mu.Lock()
	if o.kernel == nil {
		s := o.status
		o.mu.Unlock()
		return s, nil
	}
	if o.cancel != nil {
		o.cancel()
	}
	done := o.done
	o.mu.Unlock()
	select {
	case <-done:
	case <-ctx.Done():
		return o.Status(), ctx.Err()
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := o.kernel.close(); err != nil {
		o.status.Status = "unavailable"
		o.status.AttachmentsRetained = true
		o.status.Reason = "Owned observer detach could not be proven: " + clip(err.Error(), 512)
		return o.status, err
	}
	for i := range o.bindings {
		o.bindings[i].close()
	}
	o.bindings = nil
	o.kernel = nil
	o.status.AttachmentsRetained = false
	for _, b := range o.pending {
		o.status.Quality.UnsavedEvents += b.ObservedPackets
	}
	if o.inflight != nil {
		for _, b := range o.inflight.rows {
			o.status.Quality.UnsavedEvents += b.ObservedPackets
		}
	}
	o.pending, o.inflight = nil, nil
	o.status.Status = "off"
	o.status.Reason = "Owned kernel capture detached. Unsaved pending and in-flight shutdown evidence are excluded from history."
	return o.status, nil
}
