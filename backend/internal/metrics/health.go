package metrics

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/sysinfo"
)

// Finding is one thing worth telling the operator about the machine.
//
// The dashboard has, until now, shown numbers and left every judgement to the
// reader. That is fine for someone who already knows that 3% steal is bad and
// 85% memory on a box with a large page cache is not — and useless for
// everybody else, which is most of the people running one server. Netdata and
// Cockpit both take a position here; this is ours, with the reasoning attached
// so it can be argued with rather than merely obeyed.
//
// A finding is only raised for a condition that is costing the machine
// something now or is about to. Occupancy that costs nothing — swap holding
// idle pages, packets an interface dropped weeks ago — is a reading, not a
// finding, and a list padded with readings teaches the operator to ignore it.
type Finding struct {
	// ID is stable across evaluations so a client can keep a dismissal or an
	// expanded row attached to the same finding between polls.
	ID string `json:"id"`
	// Level is "critical", "warning" or "notice", ordered by how soon it will
	// stop the server doing its job.
	Level string `json:"level"`
	Title string `json:"title"`
	// Detail says what was measured. Advice says what to do about it —
	// separate fields because the first is a fact and the second is an
	// opinion, and the UI presents them differently.
	Detail string `json:"detail"`
	Advice string `json:"advice,omitempty"`
	// Metric names the series this came from, so the UI can link the finding
	// to the chart that shows it.
	Metric string `json:"metric,omitempty"`
	// Value and Threshold are carried so the client can render a meter
	// without re-deriving either from the text.
	Value     float64 `json:"value"`
	Threshold float64 `json:"threshold"`
	// Since is when the condition started, where that can be established from
	// the recorded series rather than guessed from the current instant.
	Since *time.Time `json:"since,omitempty"`
	// Area is the part of the machine the finding belongs to, so every client
	// can draw the same coverage map without re-deriving it from IDs.
	Area string `json:"area"`
	// Evidence is the handful of measurements the verdict was drawn from,
	// already worded, so a panel can show the working rather than one number.
	Evidence []Fact `json:"evidence,omitempty"`
	// Subjects are the named things a remedy acts on — the failed units, the
	// unhealthy containers, the mount — so a fix can be offered per thing
	// rather than as a link to a page that lists them again.
	Subjects []Subject `json:"subjects,omitempty"`
	// Correlated are saved diagnostic runs from this server that met trouble
	// in the same stretch, for network findings and only for a reader who
	// may open them.
	Correlated []ProbeEvidence `json:"correlated,omitempty"`
}

// Fact is one labelled measurement behind a finding.
type Fact struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// Subject is one named thing a finding is about.
type Subject struct {
	// Kind is "unit", "container", "mount" or "interface".
	Kind string `json:"kind"`
	// ID is what the owning control addresses it by: a unit name, a container
	// id, a mountpoint, an interface.
	ID   string `json:"id"`
	Name string `json:"name"`
	// Detail is the subject's own state word — a unit's result, a container's
	// state — where it adds to the finding's.
	Detail string     `json:"detail,omitempty"`
	Since  *time.Time `json:"since,omitempty"`
}

// AreaVerdict is one part of the machine and how it reads now. Every area is
// always present, so "nothing wrong with the disks" is said rather than
// implied by a missing row, and an area whose evidence could not be read says
// so instead of passing.
type AreaVerdict struct {
	ID string `json:"id"`
	// Status is "ok", "notice", "warning", "critical", or "unknown" when the
	// area's evidence was unavailable.
	Status string `json:"status"`
	// Summary is the area's current reading in a few words.
	Summary string `json:"summary"`
}

// The areas, in the order every client draws them.
const (
	AreaCPU        = "cpu"
	AreaMemory     = "memory"
	AreaStorage    = "storage"
	AreaNetwork    = "network"
	AreaServices   = "services"
	AreaContainers = "containers"
	AreaHardware   = "hardware"
)

var areaOrder = []string{AreaCPU, AreaMemory, AreaStorage, AreaNetwork, AreaServices, AreaContainers, AreaHardware}

// Health is the verdict on the host.
type Health struct {
	// Status is the worst level among the findings, or "ok" when there are
	// none. It is what the badge in the shell reads.
	Status   string    `json:"status"`
	Findings []Finding `json:"findings"`
	// Areas is one verdict per part of the machine, in areaOrder.
	Areas []AreaVerdict `json:"areas"`
	// CheckedAt is when this was evaluated, so a stale panel can say so.
	CheckedAt time.Time `json:"checkedAt"`
	// Recorded reports whether the history-backed checks ran at all. Without
	// a recorder there is still a verdict, just a shallower one.
	Recorded bool `json:"recorded"`
	// Silences name unavailable evidence; a missing observation is never a pass.
	Silences []string `json:"silences,omitempty"`
}

// SetArea records an area's reading. known is false when the area's evidence
// could not be read, which leaves it "unknown" rather than passing it.
func (h *Health) SetArea(id, summary string, known bool) {
	status := "ok"
	if !known {
		status = "unknown"
	}
	for i := range h.Areas {
		if h.Areas[i].ID == id {
			h.Areas[i] = AreaVerdict{ID: id, Status: status, Summary: summary}
			return
		}
	}
	h.Areas = append(h.Areas, AreaVerdict{ID: id, Status: status, Summary: summary})
}

// Settle ranks the findings worst-first and derives every verdict from them:
// each area's and the host's. Whoever adds findings after Assess — the
// runtime evidence — settles again, so the badge, the coverage map and the
// list can never disagree about how bad things are.
func (h *Health) Settle() {
	// Worst first: an operator reading only the top row should be reading the
	// most urgent one.
	sort.SliceStable(h.Findings, func(i, j int) bool {
		return levelRank(h.Findings[i].Level) > levelRank(h.Findings[j].Level)
	})
	h.Status = "ok"
	if len(h.Findings) > 0 {
		h.Status = h.Findings[0].Level
	}
	rank := map[string]int{}
	for i, id := range areaOrder {
		rank[id] = i
	}
	sort.SliceStable(h.Areas, func(i, j int) bool { return rank[h.Areas[i].ID] < rank[h.Areas[j].ID] })
	for i := range h.Areas {
		worst := ""
		for _, f := range h.Findings {
			if f.Area == h.Areas[i].ID && levelRank(f.Level) > levelRank(worst) {
				worst = f.Level
			}
		}
		if worst != "" {
			h.Areas[i].Status = worst
		}
	}
}

// Thresholds are the lines the checks draw.
//
// Named constants rather than inline numbers because each of these is a claim
// about what is bad, and a claim deserves somewhere to be read, questioned and
// changed in one place.
const (
	// A disk over 90% is close enough to full that a log rotation or a
	// database vacuum can finish the job overnight.
	diskWarnPercent     = 85
	diskCriticalPercent = 93

	// Inodes are the ceiling nobody watches, and hitting it produces "no
	// space left on device" on a filesystem with gigabytes free.
	inodeWarnPercent = 90

	// Memory is deliberately judged on *available* rather than used: a Linux
	// box with a healthy page cache reads as 90% used and is perfectly fine.
	// Under 10% available is when reclaim starts costing latency.
	memAvailWarnPercent     = 10
	memAvailCriticalPercent = 5
	// Swap this full beside tight memory means the next allocation has
	// nowhere to go but the OOM killer.
	swapExhaustedPercent = 90

	// Steal is time the hypervisor gave to somebody else. A few tenths of a
	// percent is normal on shared hosting; sustained whole percents mean the
	// host is oversubscribed, and no change to your own workload will fix it.
	stealWarnPercent     = 5
	stealCriticalPercent = 15

	// Pressure is a share of wall time spent stalled, judged on the minute
	// and five-minute averages together: the ten-second window catches a
	// build finishing and calls the machine saturated, and the five-minute
	// one alone is still reporting a spike that ended four minutes ago.
	cpuPressureWarn60, cpuPressureWarn300         = 25, 15
	cpuPressureCritical60, cpuPressureCritical300 = 60, 40
	// Memory stalls are dearer than CPU queueing — a stalled task is waiting
	// for a page, not for a turn — so the line is lower.
	memPressureWarn60, memPressureWarn300 = 10, 5
	memPressureCritical60                 = 40
	memPressureFullCritical60             = 10
	ioPressureWarn60, ioPressureWarn300   = 30, 20
	ioPressureFullCritical60              = 25
	ioPressureCritical300                 = 25

	// Load per core, used only where the kernel has no pressure readings.
	// Above 1.0 per core the run queue is longer than the machine can serve;
	// 2.0 is where interactive work starts to feel it.
	loadWarnPerCore     = 1.0
	loadCriticalPerCore = 2.0

	// Without PSI there is no direct measure of thrashing, so swap nearly
	// full beside short memory is the nearest honest proxy.
	swapWarnPercent       = 80
	swapMemAvailPercent   = 20
	ioWaitWarnPercent     = 20
	timeWaitWarn          = 12000
	fileHandleWarnPercent = 80
)

// Assess judges the host from a live snapshot and, where available, the
// recorded series behind it.
//
// The snapshot alone answers "is it bad right now"; the series is what turns
// that into "and it has been for forty minutes", which is the difference
// between a spike worth ignoring and a trend worth acting on. A host with no
// recorder still gets a verdict — a shallower one, honestly labelled.
func (r *Recorder) Assess(ctx context.Context, snap *sysinfo.Snapshot) Health {
	h := Health{Status: "ok", Findings: []Finding{}, Areas: []AreaVerdict{}, CheckedAt: time.Now().UTC()}
	if snap == nil {
		return h
	}

	// A short trailing window rather than the whole retention: the question
	// is what the machine is doing now, and an hour of context is enough to
	// tell a sustained condition from a momentary one without making the
	// health panel a second history query.
	var recent *Series
	if r.Enabled() {
		to := time.Now()
		if s, err := r.Range(ctx, to.Add(-time.Hour), to, 60); err == nil && len(s.Points) > 0 {
			recent = s
			h.Recorded = true
		}
	}
	links := r.links.observe(snap.Net, time.Now())
	tcp := r.tcp.observe(snap.TCP, time.Now())

	h.Findings = append(h.Findings, storageFindings(snap)...)
	h.Findings = append(h.Findings, memoryFindings(snap)...)
	h.Findings = append(h.Findings, cpuFindings(snap, recent)...)
	h.Findings = append(h.Findings, pressureFindings(snap)...)
	h.Findings = append(h.Findings, networkFindings(snap, links)...)
	h.Findings = append(h.Findings, tcpFindings(snap, tcp, recent)...)
	h.Findings = append(h.Findings, hardwareFindings(snap)...)

	hostAreas(&h, snap, links, tcp)
	// Runtime evidence is read by the API layer; until it is merged these
	// two are honestly unread rather than absent.
	h.SetArea(AreaServices, "not read", false)
	h.SetArea(AreaContainers, "not read", false)
	h.Settle()
	return h
}

func storageFindings(snap *sysinfo.Snapshot) []Finding {
	var out []Finding
	for _, m := range snap.Mounts {
		if m.Total == 0 {
			continue
		}
		mount := []Subject{{Kind: "mount", ID: m.Mountpoint, Name: m.Mountpoint, Detail: m.FSType}}
		space := []Fact{
			{Label: "Used", Value: percent(m.UsedPercent)},
			{Label: "Free", Value: humanBytes(int64(m.Free))},
			{Label: "Size", Value: humanBytes(int64(m.Total))},
		}
		switch {
		case m.UsedPercent >= diskCriticalPercent:
			out = append(out, Finding{
				ID:     "disk:" + m.Mountpoint,
				Level:  "critical",
				Title:  fmt.Sprintf("%s is nearly full", m.Mountpoint),
				Detail: fmt.Sprintf("%.0f%% used — %s free of %s", m.UsedPercent, humanBytes(int64(m.Free)), humanBytes(int64(m.Total))),
				Advice: "Free space now: a filesystem that reaches 100% will stop writes, including the ones the database and the logs depend on.",
				Metric: "disk", Value: m.UsedPercent, Threshold: diskCriticalPercent,
				Area: AreaStorage, Evidence: space, Subjects: mount,
			})
		case m.UsedPercent >= diskWarnPercent:
			out = append(out, Finding{
				ID:     "disk:" + m.Mountpoint,
				Level:  "warning",
				Title:  fmt.Sprintf("%s is filling up", m.Mountpoint),
				Detail: fmt.Sprintf("%.0f%% used — %s free", m.UsedPercent, humanBytes(int64(m.Free))),
				Advice: "Find what is taking the space and remove what is no longer needed before writes start failing.",
				Metric: "disk", Value: m.UsedPercent, Threshold: diskWarnPercent,
				Area: AreaStorage, Evidence: space, Subjects: mount,
			})
		}
		if m.InodesTotal > 0 {
			inodes := float64(m.InodesUsed) / float64(m.InodesTotal) * 100
			if inodes >= inodeWarnPercent {
				out = append(out, Finding{
					ID:     "inodes:" + m.Mountpoint,
					Level:  "warning",
					Title:  fmt.Sprintf("%s is running out of inodes", m.Mountpoint),
					Detail: fmt.Sprintf("%.0f%% of inodes used with %.0f%% of the space", inodes, m.UsedPercent),
					Advice: "The filesystem will refuse new files while still reporting free space. Look for a directory holding very many small ones — caches and mail spools are the usual answer.",
					Metric: "inodes", Value: inodes, Threshold: inodeWarnPercent,
					Area: AreaStorage, Subjects: mount,
					Evidence: []Fact{
						{Label: "Inodes used", Value: percent(inodes)},
						{Label: "Space used", Value: percent(m.UsedPercent)},
					},
				})
			}
		}
	}
	return out
}

func memoryFindings(snap *sysinfo.Snapshot) []Finding {
	if snap.Memory.Total == 0 {
		return nil
	}
	// Available, not used. "Used" on Linux counts the page cache, which the
	// kernel will hand back the instant anything wants it; judging a server
	// by it produces a permanent, meaningless warning.
	avail := float64(snap.Memory.Available) / float64(snap.Memory.Total) * 100
	swapFull := snap.Swap.Total > 0 && snap.Swap.UsedPercent >= swapExhaustedPercent
	evidence := memoryEvidence(snap)
	measured := fmt.Sprintf("%s available of %s (%.0f%%)", humanBytes(int64(snap.Memory.Available)), humanBytes(int64(snap.Memory.Total)), avail)
	switch {
	case avail <= memAvailCriticalPercent:
		return []Finding{{
			ID:     "memory",
			Level:  "critical",
			Title:  "Almost no memory available",
			Detail: measured,
			Advice: "The OOM killer is the next thing that happens. Stop or limit whatever grew.",
			Metric: "memory", Value: avail, Threshold: memAvailCriticalPercent,
			Area: AreaMemory, Evidence: evidence,
		}}
	case avail <= memAvailWarnPercent && swapFull:
		// Tight memory with swap still free has somewhere to put idle pages;
		// with swap full too there is no headroom anywhere.
		return []Finding{{
			ID:     "memory",
			Level:  "critical",
			Title:  "Memory and swap are both nearly exhausted",
			Detail: fmt.Sprintf("%s, and swap is %.0f%% full — there is no headroom left", measured, snap.Swap.UsedPercent),
			Advice: "The next large allocation has nowhere to go but the OOM killer. Stop or limit the workload that grew.",
			Metric: "memory", Value: avail, Threshold: memAvailWarnPercent,
			Area: AreaMemory, Evidence: evidence,
		}}
	case avail <= memAvailWarnPercent:
		return []Finding{{
			ID:     "memory",
			Level:  "warning",
			Title:  "Memory is tight",
			Detail: measured,
			Advice: "Available memory counts reclaimable cache, so this is genuinely low rather than a page cache artefact. Find what grew and limit or restart it.",
			Metric: "memory", Value: avail, Threshold: memAvailWarnPercent,
			Area: AreaMemory, Evidence: evidence,
		}}
	}
	// Swap occupancy on its own is idle pages the kernel moved out once, and
	// costs nothing while memory is free. Where pressure readings exist they
	// measure thrashing directly; only without them does a nearly full swap
	// beside short memory stand in for it.
	if !snap.Pressure.Supported && snap.Swap.Total > 0 &&
		snap.Swap.UsedPercent >= swapWarnPercent && avail <= swapMemAvailPercent {
		return []Finding{{
			ID:     "swap",
			Level:  "warning",
			Title:  "Swap is nearly full and memory is short",
			Detail: fmt.Sprintf("%.0f%% of %s swapped with %s available", snap.Swap.UsedPercent, humanBytes(int64(snap.Swap.Total)), humanBytes(int64(snap.Memory.Available))),
			Advice: "This kernel has no pressure readings to tell idle swapped pages from active thrashing. Compare the memory consumers with disk activity before stopping anything.",
			Metric: "swap", Value: snap.Swap.UsedPercent, Threshold: swapWarnPercent,
			Area: AreaMemory, Evidence: evidence,
		}}
	}
	return nil
}

func memoryEvidence(snap *sysinfo.Snapshot) []Fact {
	avail := 0.0
	if snap.Memory.Total > 0 {
		avail = float64(snap.Memory.Available) / float64(snap.Memory.Total) * 100
	}
	swap := "none configured"
	if snap.Swap.Total > 0 {
		swap = fmt.Sprintf("%s of %s", humanBytes(int64(snap.Swap.Used)), humanBytes(int64(snap.Swap.Total)))
	}
	return []Fact{
		{Label: "Available", Value: fmt.Sprintf("%s (%s)", humanBytes(int64(snap.Memory.Available)), percent(avail))},
		{Label: "Page cache", Value: humanBytes(int64(snap.Memory.Cached + snap.Memory.Buffers))},
		{Label: "Swap used", Value: swap},
	}
}

func cpuFindings(snap *sysinfo.Snapshot, recent *Series) []Finding {
	var out []Finding

	// Steal is checked against the recorded mean where there is one: a single
	// two-second frame catching 6% steal says almost nothing, whereas an hour
	// averaging 6% says the host is oversubscribed.
	steal := snap.CPU.Modes.Steal
	sustained := steal
	if recent != nil {
		sustained = meanOf(recent.Points, func(p Point) float64 { return p.CPUSteal })
	}
	stealEvidence := []Fact{
		{Label: "Steal", Value: percent(sustained)},
		{Label: "Right now", Value: percent(steal)},
		{Label: "Window", Value: windowWords(recent)},
	}
	switch {
	case sustained >= stealCriticalPercent:
		out = append(out, Finding{
			ID:     "steal",
			Level:  "critical",
			Title:  "The hypervisor is taking your CPU",
			Detail: fmt.Sprintf("%.1f%% steal time%s", sustained, overWindow(recent)),
			Advice: "Steal is time your vCPU was ready to run and the host gave the core to another tenant. Nothing you change inside this server will recover it — this is a case for resizing or moving.",
			Metric: "cpuSteal", Value: sustained, Threshold: stealCriticalPercent,
			Area: AreaCPU, Evidence: stealEvidence,
		})
	case sustained >= stealWarnPercent:
		out = append(out, Finding{
			ID:     "steal",
			Level:  "warning",
			Title:  "Noticeable CPU steal",
			Detail: fmt.Sprintf("%.1f%% steal time%s", sustained, overWindow(recent)),
			Advice: "Your neighbours on this physical host are busy. Worth watching; worth escalating if it persists.",
			Metric: "cpuSteal", Value: sustained, Threshold: stealWarnPercent,
			Area: AreaCPU, Evidence: stealEvidence,
		})
	}

	// Pressure readings answer both of the questions below directly, so these
	// stand in only on a kernel without them. Reporting both is one problem
	// told twice: load counts tasks blocked on a disk as well as tasks waiting
	// for a core, so it rose with every CPU and I/O stall PSI already named.
	if snap.Pressure.Supported {
		return out
	}

	// I/O wait matters relative to the run queue: a machine at 30% iowait
	// with nothing blocked is reading a big file, which is what disks are
	// for. Blocked processes are what make it a problem.
	if snap.CPU.Modes.IOWait >= ioWaitWarnPercent && snap.Procs.Blocked > 0 {
		out = append(out, Finding{
			ID:     "iowait",
			Level:  "warning",
			Title:  "Processes are waiting on disk",
			Detail: fmt.Sprintf("%.0f%% I/O wait with %d process(es) blocked", snap.CPU.Modes.IOWait, snap.Procs.Blocked),
			Advice: "The CPU is idle because it has nothing to do until the disk answers. Find the processes reading and writing most, and check device latency rather than throughput.",
			Metric: "cpuIowait", Value: snap.CPU.Modes.IOWait, Threshold: ioWaitWarnPercent,
			Area: AreaStorage,
			Evidence: []Fact{
				{Label: "I/O wait", Value: percent(snap.CPU.Modes.IOWait)},
				{Label: "Blocked tasks", Value: strconv.Itoa(snap.Procs.Blocked)},
			},
		})
	}

	cores := snap.CPU.Cores
	if cores <= 0 {
		cores = 1
	}
	perCore := snap.CPU.LoadAvg5 / float64(cores)
	loadEvidence := []Fact{
		{Label: "Load (1 min)", Value: fmt.Sprintf("%.2f", snap.CPU.LoadAvg1)},
		{Label: "Load (5 min)", Value: fmt.Sprintf("%.2f on %d cores", snap.CPU.LoadAvg5, cores)},
		{Label: "Load (15 min)", Value: fmt.Sprintf("%.2f", snap.CPU.LoadAvg15)},
	}
	switch {
	case perCore >= loadCriticalPerCore:
		out = append(out, Finding{
			ID:     "load",
			Level:  "warning",
			Title:  "Load is well above capacity",
			Detail: fmt.Sprintf("5-minute load %.2f across %d cores (%.1f per core)", snap.CPU.LoadAvg5, cores, perCore),
			Advice: "More work is queued than the machine can run. Anything interactive will feel slow.",
			Metric: "load", Value: perCore, Threshold: loadCriticalPerCore,
			Area: AreaCPU, Evidence: loadEvidence,
		})
	case perCore >= loadWarnPerCore:
		out = append(out, Finding{
			ID:     "load",
			Level:  "notice",
			Title:  "The run queue is full",
			Detail: fmt.Sprintf("5-minute load %.2f across %d cores (%.1f per core)", snap.CPU.LoadAvg5, cores, perCore),
			Advice: "At one per core the machine is exactly saturated — fine for a batch job, tight for anything serving requests.",
			Metric: "load", Value: perCore, Threshold: loadWarnPerCore,
			Area: AreaCPU, Evidence: loadEvidence,
		})
	}
	return out
}

func pressureFindings(snap *sysinfo.Snapshot) []Finding {
	if !snap.Pressure.Supported {
		return nil
	}
	p := snap.Pressure
	var out []Finding
	stalls := func(ten, minute, five float64) []Fact {
		return []Fact{
			{Label: "Last 10 seconds", Value: percent(ten)},
			{Label: "Last minute", Value: percent(minute)},
			{Label: "Last 5 minutes", Value: percent(five)},
		}
	}

	cores := snap.CPU.Cores
	if cores <= 0 {
		cores = 1
	}
	cpu := Finding{
		ID: "psi-cpu", Metric: "psiCpu", Area: AreaCPU, Value: p.CPUSome60,
		Detail: fmt.Sprintf("Runnable tasks waited %.0f%% of the last minute and %.0f%% of the last five", p.CPUSome60, p.CPUSome300),
		Advice: "Find what is using the processors and stop, lower or limit it. Pressure counts time tasks spent queued for a core, so unlike utilisation it keeps rising as the backlog grows.",
		Evidence: append(stalls(p.CPUSome, p.CPUSome60, p.CPUSome300),
			Fact{Label: "Load (5 min)", Value: fmt.Sprintf("%.1f on %d cores", snap.CPU.LoadAvg5, cores)}),
	}
	switch {
	case p.CPUSome60 >= cpuPressureCritical60 && p.CPUSome300 >= cpuPressureCritical300:
		cpu.Level, cpu.Title, cpu.Threshold = "critical", "CPU is saturated", cpuPressureCritical60
		out = append(out, cpu)
	case p.CPUSome60 >= cpuPressureWarn60 && p.CPUSome300 >= cpuPressureWarn300:
		cpu.Level, cpu.Title, cpu.Threshold = "warning", "Work is queueing for CPU", cpuPressureWarn60
		out = append(out, cpu)
	}

	mem := Finding{
		ID: "psi-mem", Metric: "psiMem", Area: AreaMemory, Value: p.MemSome60,
		Detail:   fmt.Sprintf("Tasks waited on memory %.0f%% of the last minute and %.0f%% of the last five", p.MemSome60, p.MemSome300),
		Advice:   "Processes are being paused while the kernel finds pages. Find what grew and limit or restart it; this shows up long before the OOM killer does.",
		Evidence: append(stalls(p.MemSome, p.MemSome60, p.MemSome300)[1:], memoryEvidence(snap)[0], memoryEvidence(snap)[2]),
	}
	switch {
	case p.MemFull60 >= memPressureFullCritical60:
		// Full means every runnable task was stalled at once: the machine did
		// no work at all for that share of the minute.
		mem.Level, mem.Title = "critical", "Memory reclaim is stalling the machine"
		mem.Value, mem.Threshold = p.MemFull60, memPressureFullCritical60
		mem.Detail = fmt.Sprintf("Every task was stalled on memory %.0f%% of the last minute", p.MemFull60)
		out = append(out, mem)
	case p.MemSome60 >= memPressureCritical60:
		mem.Level, mem.Title, mem.Threshold = "critical", "Memory reclaim is stalling the machine", memPressureCritical60
		out = append(out, mem)
	case p.MemSome60 >= memPressureWarn60 && p.MemSome300 >= memPressureWarn300:
		mem.Level, mem.Title, mem.Threshold = "warning", "Memory reclaim is stalling work", memPressureWarn60
		out = append(out, mem)
	}

	io := Finding{
		ID: "psi-io", Metric: "psiIo", Area: AreaStorage, Value: p.IOSome60,
		Detail: fmt.Sprintf("Tasks waited on storage %.0f%% of the last minute and %.0f%% of the last five", p.IOSome60, p.IOSome300),
		Advice: "Find the processes reading and writing most. Look at device latency rather than throughput — a disk can be slow while moving very little data.",
		Evidence: append(stalls(p.IOSome, p.IOSome60, p.IOSome300)[1:],
			Fact{Label: "I/O wait", Value: percent(snap.CPU.Modes.IOWait)},
			Fact{Label: "Blocked tasks", Value: strconv.Itoa(snap.Procs.Blocked)}),
	}
	switch {
	case p.IOFull60 >= ioPressureFullCritical60 && p.IOSome300 >= ioPressureCritical300:
		io.Level, io.Title = "critical", "Storage is stalling the machine"
		io.Value, io.Threshold = p.IOFull60, ioPressureFullCritical60
		out = append(out, io)
	case p.IOSome60 >= ioPressureWarn60 && p.IOSome300 >= ioPressureWarn300:
		io.Level, io.Title, io.Threshold = "warning", "Storage is stalling work", ioPressureWarn60
		out = append(out, io)
	}
	return out
}

func networkFindings(snap *sysinfo.Snapshot, links linkWindows) []Finding {
	var out []Finding
	if snap.Sockets.TCPTimeWait >= timeWaitWarn {
		out = append(out, Finding{
			ID:     "timewait",
			Level:  "notice",
			Title:  "Many sockets in TIME_WAIT",
			Detail: fmt.Sprintf("%d TIME_WAIT sockets, %d in use", snap.Sockets.TCPTimeWait, snap.Sockets.TCPInUse),
			Advice: "Each one holds an ephemeral port for a minute or so. At this rate a busy client can run the range out and start failing to connect.",
			Metric: "tcpTimeWait", Value: float64(snap.Sockets.TCPTimeWait), Threshold: timeWaitWarn,
			Area: AreaNetwork,
			Evidence: []Fact{
				{Label: "TIME_WAIT", Value: thousands(uint64(snap.Sockets.TCPTimeWait))},
				{Label: "TCP in use", Value: thousands(uint64(snap.Sockets.TCPInUse))},
			},
		})
	}
	// Drops and errors are judged over a window between two checks, never on
	// the since-boot counters: a tunnel that lost four million packets during
	// an outage last month reads identically, cumulatively, to one losing
	// them now, and only the second is worth a line.
	for _, n := range snap.Net {
		w, ok := links.windows[n.Interface]
		if !ok {
			continue
		}
		subject := []Subject{{Kind: "interface", ID: n.Interface, Name: n.Interface, Detail: n.Kind}}
		share := w.lossPercent()
		evidence := []Fact{
			{Label: "Dropped", Value: thousands(w.drops)},
			{Label: "Share of packets", Value: percent(share)},
			{Label: "Window", Value: spanShort(w.span)},
		}
		detail := fmt.Sprintf("%s packets dropped in the last %s (%s of traffic)", thousands(w.drops), spanWords(w.span), percent(share))
		switch {
		case links.kind(n) == "physical" && w.drops >= physicalDropFloor && share >= physicalDropShare:
			out = append(out, Finding{
				ID:     "drops:" + n.Interface,
				Level:  "warning",
				Title:  fmt.Sprintf("%s is dropping packets", n.Interface),
				Detail: detail,
				Advice: "A physical link drops when its receive ring or queue overflows or the driver discards frames. Compare the drop rate with the interface's throughput: bursts that fill the ring need a larger ring or less traffic, not a reboot.",
				Metric: "net", Value: share, Threshold: physicalDropShare,
				Area: AreaNetwork, Evidence: evidence, Subjects: subject,
			})
		case links.kind(n) == "tunnel" && w.drops >= tunnelDropFloor && share >= tunnelDropShare:
			out = append(out, Finding{
				ID:     "drops:" + n.Interface,
				Level:  "notice",
				Title:  fmt.Sprintf("%s is dropping packets", n.Interface),
				Detail: detail,
				Advice: "A tunnel drops what it cannot deliver — a peer offline, a missing route, an MTU too small. Check the peers before the host.",
				Metric: "net", Value: share, Threshold: tunnelDropShare,
				Area: AreaNetwork, Evidence: evidence, Subjects: subject,
			})
		}
		if links.kind(n) == "physical" && w.errors >= physicalErrorFloor {
			out = append(out, Finding{
				ID:     "neterr:" + n.Interface,
				Level:  "warning",
				Title:  fmt.Sprintf("%s is reporting link errors", n.Interface),
				Detail: fmt.Sprintf("%s receive and transmit errors in the last %s", thousands(w.errors), spanWords(w.span)),
				Advice: "Errors on a physical link are corrupted or malformed frames: usually a cable, a port, a duplex mismatch or a driver. On a VPS, report it to the provider.",
				Metric: "net", Value: float64(w.errors), Threshold: physicalErrorFloor,
				Area: AreaNetwork, Subjects: subject,
				Evidence: []Fact{
					{Label: "Errors", Value: thousands(w.errors)},
					{Label: "Window", Value: spanShort(w.span)},
				},
			})
		}
	}
	return out
}

// hardwareFindings reads the two live-only figures the recorder does not
// keep: the file handle count and whatever temperatures the board reports.
//
// A sensor is judged only against its own thresholds. There is no universal
// "too hot": a CPU package idles at 60 °C where an NVMe drive at 60 °C is
// throttling, and the driver is the one party that knows which this is.
func hardwareFindings(snap *sysinfo.Snapshot) []Finding {
	var out []Finding
	if snap.Files.Max > 0 {
		used := float64(snap.Files.Open) / float64(snap.Files.Max) * 100
		if used >= fileHandleWarnPercent {
			out = append(out, Finding{
				ID:     "files",
				Level:  "warning",
				Title:  "The host is running out of file handles",
				Detail: fmt.Sprintf("%d of %d open (%.0f%%)", snap.Files.Open, snap.Files.Max, used),
				Advice: "Something is opening files or sockets faster than it closes them. Find the process holding the most and restart it; raising fs.file-max only postpones the same failure.",
				Metric: "files", Value: used, Threshold: fileHandleWarnPercent,
				Area: AreaHardware,
				Evidence: []Fact{
					{Label: "Open", Value: thousands(uint64(snap.Files.Open))},
					{Label: "Limit", Value: thousands(uint64(snap.Files.Max))},
				},
			})
		}
	}
	for _, sensor := range snap.Sensors {
		evidence := []Fact{
			{Label: "Reading", Value: fmt.Sprintf("%.0f °C", sensor.TempC)},
			{Label: "High", Value: celsius(sensor.High)},
			{Label: "Critical", Value: celsius(sensor.Critical)},
		}
		switch {
		case sensor.Critical > 0 && sensor.TempC >= sensor.Critical:
			out = append(out, Finding{
				ID:     "temp:" + sensor.Name,
				Level:  "critical",
				Title:  fmt.Sprintf("%s is at its critical temperature", sensor.Name),
				Detail: fmt.Sprintf("%.0f °C against a critical limit of %.0f °C", sensor.TempC, sensor.Critical),
				Advice: "The hardware will throttle or shut itself down to survive this. Check airflow and fans before anything else.",
				Metric: "temperature", Value: sensor.TempC, Threshold: sensor.Critical,
				Area: AreaHardware, Evidence: evidence,
			})
		case sensor.High > 0 && sensor.TempC >= sensor.High:
			out = append(out, Finding{
				ID:     "temp:" + sensor.Name,
				Level:  "warning",
				Title:  fmt.Sprintf("%s is running hot", sensor.Name),
				Detail: fmt.Sprintf("%.0f °C against a high-water mark of %.0f °C", sensor.TempC, sensor.High),
				Advice: "Sustained heat costs clock speed first and hardware second. Worth a look at cooling if it stays here.",
				Metric: "temperature", Value: sensor.TempC, Threshold: sensor.High,
				Area: AreaHardware, Evidence: evidence,
			})
		}
	}
	return out
}

// hostAreas reads the five host areas into the verdict. The runtime areas
// are the API layer's, which holds the service manager and Docker.
func hostAreas(h *Health, snap *sysinfo.Snapshot, links linkWindows, tcp *tcpDelta) {
	switch {
	case snap.Pressure.Supported:
		h.SetArea(AreaCPU, fmt.Sprintf("%s stalled", percent(snap.Pressure.CPUSome60)), true)
	case snap.CPU.Cores > 0:
		h.SetArea(AreaCPU, fmt.Sprintf("load %.1f per core", snap.CPU.LoadAvg5/float64(snap.CPU.Cores)), true)
	default:
		h.SetArea(AreaCPU, "not readable", false)
	}

	if snap.Memory.Total > 0 {
		h.SetArea(AreaMemory, humanBytes(int64(snap.Memory.Available))+" free", true)
	} else {
		h.SetArea(AreaMemory, "not readable", false)
	}

	var fullest *sysinfo.MountStats
	for i := range snap.Mounts {
		if snap.Mounts[i].Total > 0 && (fullest == nil || snap.Mounts[i].UsedPercent > fullest.UsedPercent) {
			fullest = &snap.Mounts[i]
		}
	}
	if fullest != nil {
		h.SetArea(AreaStorage, fmt.Sprintf("%s at %.0f%%", fullest.Mountpoint, fullest.UsedPercent), true)
	} else {
		h.SetArea(AreaStorage, "no filesystems", false)
	}

	switch {
	case links.judged == 0:
		h.SetArea(AreaNetwork, "no links to judge", false)
	case len(links.windows) == 0:
		// The first check after a start has no earlier counters to compare
		// with. It has not found loss; it has not looked yet.
		h.SetArea(AreaNetwork, "measuring", true)
	default:
		worst := 0.0
		for _, w := range links.windows {
			worst = max(worst, w.lossPercent())
		}
		summary := percent(worst) + " loss"
		switch {
		case worst == 0:
			summary = "no drops"
		case worst < 0.1:
			summary = "<0.1% loss"
		}
		if extra := tcpSummary(snap, tcp); extra != "" {
			summary += " · " + extra
		}
		h.SetArea(AreaNetwork, summary, true)
	}

	var parts []string
	if len(snap.Sensors) == 0 {
		parts = append(parts, "no sensors")
	} else {
		hottest := snap.Sensors[0]
		for _, s := range snap.Sensors[1:] {
			if s.TempC > hottest.TempC {
				hottest = s
			}
		}
		parts = append(parts, fmt.Sprintf("%.0f °C", hottest.TempC))
	}
	if snap.Files.Max > 0 {
		parts = append(parts, "handles "+percent(float64(snap.Files.Open)/float64(snap.Files.Max)*100))
	}
	summary := parts[0]
	for _, part := range parts[1:] {
		summary += " · " + part
	}
	// A virtual machine has no sensors to read, which is not a gap in the
	// assessment; with neither sensors nor a handle ceiling there is nothing
	// here that was judged at all.
	h.SetArea(AreaHardware, summary, len(snap.Sensors) > 0 || snap.Files.Max > 0)
}

func meanOf(points []Point, pick func(Point) float64) float64 {
	if len(points) == 0 {
		return 0
	}
	var sum float64
	for _, p := range points {
		sum += pick(p)
	}
	return round2(sum / float64(len(points)))
}

func overWindow(recent *Series) string {
	if recent == nil {
		return " right now"
	}
	return " averaged over the last hour"
}

func windowWords(recent *Series) string {
	if recent == nil {
		return "current reading"
	}
	return "last hour"
}

// percent words a share: one decimal under ten, where the decimal is still
// information, and none above it.
func percent(v float64) string {
	if v > 0 && v < 10 {
		return strconv.FormatFloat(v, 'f', 1, 64) + "%"
	}
	return strconv.FormatFloat(v, 'f', 0, 64) + "%"
}

func celsius(v float64) string {
	if v <= 0 {
		return "not reported"
	}
	return fmt.Sprintf("%.0f °C", v)
}

// thousands groups digits so a count of dropped packets reads at a glance.
func thousands(n uint64) string {
	s := strconv.FormatUint(n, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func spanWords(d time.Duration) string {
	minutes := int(d.Round(time.Minute) / time.Minute)
	if minutes <= 1 {
		return "minute"
	}
	return fmt.Sprintf("%d minutes", minutes)
}

func spanShort(d time.Duration) string {
	return fmt.Sprintf("%d min", max(1, int(d.Round(time.Minute)/time.Minute)))
}

func levelRank(level string) int {
	switch level {
	case "critical":
		return 3
	case "warning":
		return 2
	case "notice":
		return 1
	}
	return 0
}
