package metrics

import (
	"context"
	"fmt"
	"sort"
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
}

// Health is the verdict on the host.
type Health struct {
	// Status is the worst level among the findings, or "ok" when there are
	// none. It is what the badge in the shell reads.
	Status   string    `json:"status"`
	Findings []Finding `json:"findings"`
	// CheckedAt is when this was evaluated, so a stale panel can say so.
	CheckedAt time.Time `json:"checkedAt"`
	// Recorded reports whether the history-backed checks ran at all. Without
	// a recorder there is still a verdict, just a shallower one.
	Recorded bool `json:"recorded"`
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

	// Steal is time the hypervisor gave to somebody else. A few tenths of a
	// percent is normal on shared hosting; sustained whole percents mean the
	// host is oversubscribed, and no change to your own workload will fix it.
	stealWarnPercent     = 5
	stealCriticalPercent = 15

	// Pressure is a share of wall time spent stalled. Anything sustained
	// above a few percent is a machine that is waiting rather than working.
	pressureWarnPercent     = 10
	pressureCriticalPercent = 30

	// Load per core. Above 1.0 per core the run queue is longer than the
	// machine can serve; 2.0 is where interactive work starts to feel it.
	loadWarnPerCore     = 1.0
	loadCriticalPerCore = 2.0

	// Swap in use is not itself a problem — Linux swaps out idle pages on
	// purpose. Half the swap device in use is.
	swapWarnPercent = 50

	// Ephemeral ports are a real ceiling around 28k by default, and TIME_WAIT
	// is what fills it.
	timeWaitWarn = 12000

	// Open file handles against the kernel's ceiling. A leak reaches this
	// while every utilisation chart reads idle, and the failure mode past it
	// is every new socket and log line refused at once.
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
	h := Health{Status: "ok", Findings: []Finding{}, CheckedAt: time.Now().UTC()}
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

	h.Findings = append(h.Findings, storageFindings(snap)...)
	h.Findings = append(h.Findings, memoryFindings(snap)...)
	h.Findings = append(h.Findings, cpuFindings(snap, recent)...)
	h.Findings = append(h.Findings, pressureFindings(snap, recent)...)
	h.Findings = append(h.Findings, networkFindings(snap)...)
	h.Findings = append(h.Findings, hardwareFindings(snap)...)

	// Worst first: an operator reading only the top row should be reading the
	// most urgent one.
	sort.SliceStable(h.Findings, func(i, j int) bool {
		return levelRank(h.Findings[i].Level) > levelRank(h.Findings[j].Level)
	})
	if len(h.Findings) > 0 {
		h.Status = h.Findings[0].Level
	}
	return h
}

func storageFindings(snap *sysinfo.Snapshot) []Finding {
	var out []Finding
	for _, m := range snap.Mounts {
		if m.Total == 0 {
			continue
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
			})
		case m.UsedPercent >= diskWarnPercent:
			out = append(out, Finding{
				ID:     "disk:" + m.Mountpoint,
				Level:  "warning",
				Title:  fmt.Sprintf("%s is filling up", m.Mountpoint),
				Detail: fmt.Sprintf("%.0f%% used — %s free", m.UsedPercent, humanBytes(int64(m.Free))),
				Advice: "Scan the mount from the Filesystems panel to see what is taking the space.",
				Metric: "disk", Value: m.UsedPercent, Threshold: diskWarnPercent,
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
				})
			}
		}
	}
	return out
}

func memoryFindings(snap *sysinfo.Snapshot) []Finding {
	var out []Finding
	if snap.Memory.Total > 0 {
		// Available, not used. "Used" on Linux counts the page cache, which
		// the kernel will hand back the instant anything wants it; judging a
		// server by it produces a permanent, meaningless warning.
		avail := float64(snap.Memory.Available) / float64(snap.Memory.Total) * 100
		switch {
		case avail <= memAvailCriticalPercent:
			out = append(out, Finding{
				ID:     "memory",
				Level:  "critical",
				Title:  "Almost no memory available",
				Detail: fmt.Sprintf("%s available of %s (%.0f%%)", humanBytes(int64(snap.Memory.Available)), humanBytes(int64(snap.Memory.Total)), avail),
				Advice: "The OOM killer is the next thing that happens. Stop or limit whatever grew.",
				Metric: "memory", Value: avail, Threshold: memAvailCriticalPercent,
			})
		case avail <= memAvailWarnPercent:
			out = append(out, Finding{
				ID:     "memory",
				Level:  "warning",
				Title:  "Memory is tight",
				Detail: fmt.Sprintf("%s available of %s (%.0f%%)", humanBytes(int64(snap.Memory.Available)), humanBytes(int64(snap.Memory.Total)), avail),
				Advice: "Available memory counts reclaimable cache, so this is genuinely low rather than a page cache artefact.",
				Metric: "memory", Value: avail, Threshold: memAvailWarnPercent,
			})
		}
	}
	if snap.Swap.Total > 0 && snap.Swap.UsedPercent >= swapWarnPercent {
		out = append(out, Finding{
			ID:     "swap",
			Level:  "warning",
			Title:  "Swap is heavily used",
			Detail: fmt.Sprintf("%.0f%% of %s swapped", snap.Swap.UsedPercent, humanBytes(int64(snap.Swap.Total))),
			Advice: "Some swapping is normal and healthy. This much means the working set no longer fits in RAM, and every touched page costs a disk read.",
			Metric: "swap", Value: snap.Swap.UsedPercent, Threshold: swapWarnPercent,
		})
	}
	return out
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
	switch {
	case sustained >= stealCriticalPercent:
		out = append(out, Finding{
			ID:     "steal",
			Level:  "critical",
			Title:  "The hypervisor is taking your CPU",
			Detail: fmt.Sprintf("%.1f%% steal time%s", sustained, overWindow(recent)),
			Advice: "Steal is time your vCPU was ready to run and the host gave the core to another tenant. Nothing you change inside this server will recover it — this is a case for resizing or moving.",
			Metric: "cpuSteal", Value: sustained, Threshold: stealCriticalPercent,
		})
	case sustained >= stealWarnPercent:
		out = append(out, Finding{
			ID:     "steal",
			Level:  "warning",
			Title:  "Noticeable CPU steal",
			Detail: fmt.Sprintf("%.1f%% steal time%s", sustained, overWindow(recent)),
			Advice: "Your neighbours on this physical host are busy. Worth watching; worth escalating if it persists.",
			Metric: "cpuSteal", Value: sustained, Threshold: stealWarnPercent,
		})
	}

	// I/O wait matters relative to the run queue: a machine at 30% iowait
	// with nothing blocked is reading a big file, which is what disks are
	// for. Blocked processes are what make it a problem.
	if snap.CPU.Modes.IOWait >= 20 && snap.Procs.Blocked > 0 {
		out = append(out, Finding{
			ID:     "iowait",
			Level:  "warning",
			Title:  "Processes are waiting on disk",
			Detail: fmt.Sprintf("%.0f%% I/O wait with %d process(es) blocked", snap.CPU.Modes.IOWait, snap.Procs.Blocked),
			Advice: "The CPU is idle because it has nothing to do until the disk answers. Check the throughput and latency charts rather than the CPU one.",
			Metric: "cpuIowait", Value: snap.CPU.Modes.IOWait, Threshold: 20,
		})
	}

	cores := snap.CPU.Cores
	if cores <= 0 {
		cores = 1
	}
	perCore := snap.CPU.LoadAvg5 / float64(cores)
	switch {
	case perCore >= loadCriticalPerCore:
		out = append(out, Finding{
			ID:     "load",
			Level:  "warning",
			Title:  "Load is well above capacity",
			Detail: fmt.Sprintf("5-minute load %.2f across %d cores (%.1f per core)", snap.CPU.LoadAvg5, cores, perCore),
			Advice: "More work is queued than the machine can run. Anything interactive will feel slow.",
			Metric: "load", Value: perCore, Threshold: loadCriticalPerCore,
		})
	case perCore >= loadWarnPerCore:
		out = append(out, Finding{
			ID:     "load",
			Level:  "notice",
			Title:  "The run queue is full",
			Detail: fmt.Sprintf("5-minute load %.2f across %d cores (%.1f per core)", snap.CPU.LoadAvg5, cores, perCore),
			Advice: "At one per core the machine is exactly saturated — fine for a batch job, tight for anything serving requests.",
			Metric: "load", Value: perCore, Threshold: loadWarnPerCore,
		})
	}
	return out
}

func pressureFindings(snap *sysinfo.Snapshot, recent *Series) []Finding {
	if !snap.Pressure.Supported {
		return nil
	}
	var out []Finding
	check := func(id, title, metric string, value float64, advice string) {
		switch {
		case value >= pressureCriticalPercent:
			out = append(out, Finding{
				ID: id, Level: "critical", Title: title, Metric: metric,
				Detail: fmt.Sprintf("%.0f%% of the last 10 seconds stalled", value),
				Advice: advice, Value: value, Threshold: pressureCriticalPercent,
			})
		case value >= pressureWarnPercent:
			out = append(out, Finding{
				ID: id, Level: "warning", Title: title, Metric: metric,
				Detail: fmt.Sprintf("%.0f%% of the last 10 seconds stalled", value),
				Advice: advice, Value: value, Threshold: pressureWarnPercent,
			})
		}
	}
	check("psi-cpu", "Tasks are waiting for CPU", "psiCpu", snap.Pressure.CPUSome,
		"Pressure counts time runnable tasks spent queued. Unlike a utilisation percentage it does not saturate, so it keeps rising as the backlog grows.")
	check("psi-mem", "Memory reclaim is stalling work", "psiMem", snap.Pressure.MemSome,
		"Processes are being paused while the kernel finds pages. This shows up long before the OOM killer does.")
	check("psi-io", "Storage is stalling work", "psiIo", snap.Pressure.IOSome,
		"Tasks are blocked on I/O. Look at device latency rather than throughput — a disk can be slow while moving very little data.")
	return out
}

func networkFindings(snap *sysinfo.Snapshot) []Finding {
	var out []Finding
	if snap.Sockets.TCPTimeWait >= timeWaitWarn {
		out = append(out, Finding{
			ID:     "timewait",
			Level:  "notice",
			Title:  "Many sockets in TIME_WAIT",
			Detail: fmt.Sprintf("%d TIME_WAIT sockets, %d in use", snap.Sockets.TCPTimeWait, snap.Sockets.TCPInUse),
			Advice: "Each one holds an ephemeral port for a minute or so. At this rate a busy client can run the range out and start failing to connect.",
			Metric: "tcpTimeWait", Value: float64(snap.Sockets.TCPTimeWait), Threshold: timeWaitWarn,
		})
	}
	// Errors and drops are cumulative since boot, so this is not a rate — it
	// is "this interface has a history", which is worth one notice and not a
	// recurring alarm.
	for _, n := range snap.Net {
		if drops := n.DropIn + n.DropOut; drops > 1000 {
			out = append(out, Finding{
				ID:     "drops:" + n.Interface,
				Level:  "notice",
				Title:  fmt.Sprintf("%s has dropped packets", n.Interface),
				Detail: fmt.Sprintf("%d dropped, %d errors since boot", drops, n.ErrIn+n.ErrOut),
				Advice: "Counted since boot rather than recently, so an old incident looks the same as a current one. Worth correlating with the interface's throughput chart.",
				Metric: "net", Value: float64(drops), Threshold: 1000,
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
				Advice: "Something is opening files or sockets faster than it closes them. Sort the process table by open files to find it; raising fs.file-max only postpones the same failure.",
				Metric: "files", Value: used, Threshold: fileHandleWarnPercent,
			})
		}
	}
	for _, sensor := range snap.Sensors {
		switch {
		case sensor.Critical > 0 && sensor.TempC >= sensor.Critical:
			out = append(out, Finding{
				ID:     "temp:" + sensor.Name,
				Level:  "critical",
				Title:  fmt.Sprintf("%s is at its critical temperature", sensor.Name),
				Detail: fmt.Sprintf("%.0f °C against a critical limit of %.0f °C", sensor.TempC, sensor.Critical),
				Advice: "The hardware will throttle or shut itself down to survive this. Check airflow and fans before anything else.",
				Metric: "temperature", Value: sensor.TempC, Threshold: sensor.Critical,
			})
		case sensor.High > 0 && sensor.TempC >= sensor.High:
			out = append(out, Finding{
				ID:     "temp:" + sensor.Name,
				Level:  "warning",
				Title:  fmt.Sprintf("%s is running hot", sensor.Name),
				Detail: fmt.Sprintf("%.0f °C against a high-water mark of %.0f °C", sensor.TempC, sensor.High),
				Advice: "Sustained heat costs clock speed first and hardware second. Worth a look at cooling if it stays here.",
				Metric: "temperature", Value: sensor.TempC, Threshold: sensor.High,
			})
		}
	}
	return out
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
