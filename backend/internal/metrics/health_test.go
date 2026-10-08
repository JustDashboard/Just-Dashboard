package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/sysinfo"
)

func TestAssessHealthyHostHasNothingToSay(t *testing.T) {
	r := testRecorder(t, DefaultInterval, DefaultRetention)
	h := r.Assess(context.Background(), &sysinfo.Snapshot{
		CPU:    sysinfo.CPUStats{Cores: 4, LoadAvg5: 0.4},
		Memory: sysinfo.MemoryStats{Total: 8 << 30, Available: 5 << 30, UsedPercent: 88},
		Mounts: []sysinfo.MountStats{{Mountpoint: "/", Total: 100 << 30, Free: 60 << 30, UsedPercent: 40}},
	})
	if h.Status != "ok" {
		t.Fatalf("status = %q with findings %+v, want ok", h.Status, h.Findings)
	}
}

// 88% "used" on a box with plenty available is the single most common false
// alarm in server monitoring, and the reason the memory check reads Available.
func TestAssessIgnoresPageCacheAsMemoryPressure(t *testing.T) {
	r := testRecorder(t, DefaultInterval, DefaultRetention)
	h := r.Assess(context.Background(), &sysinfo.Snapshot{
		Memory: sysinfo.MemoryStats{Total: 8 << 30, Available: 4 << 30, UsedPercent: 95},
	})
	for _, f := range h.Findings {
		if f.ID == "memory" {
			t.Fatalf("reported %q from used%%, not available: %+v", f.Title, f)
		}
	}
}

func TestAssessFlagsExhaustedMemory(t *testing.T) {
	r := testRecorder(t, DefaultInterval, DefaultRetention)
	h := r.Assess(context.Background(), &sysinfo.Snapshot{
		Memory: sysinfo.MemoryStats{Total: 8 << 30, Available: 200 << 20, UsedPercent: 97},
	})
	f := findByID(t, h, "memory")
	if f.Level != "critical" {
		t.Errorf("level = %q, want critical", f.Level)
	}
	if h.Status != "critical" {
		t.Errorf("status = %q, want critical", h.Status)
	}
}

// A filesystem can be half empty and still refuse to create a file. The bytes
// chart cannot show that, which is the whole reason for the inode check.
func TestAssessFlagsInodeExhaustionOnAnEmptyDisk(t *testing.T) {
	r := testRecorder(t, DefaultInterval, DefaultRetention)
	h := r.Assess(context.Background(), &sysinfo.Snapshot{
		Memory: sysinfo.MemoryStats{Total: 8 << 30, Available: 6 << 30},
		Mounts: []sysinfo.MountStats{{
			Mountpoint: "/var", Total: 100 << 30, Free: 70 << 30, UsedPercent: 30,
			InodesTotal: 1_000_000, InodesUsed: 960_000,
		}},
	})
	f := findByID(t, h, "inodes:/var")
	if f.Level != "warning" {
		t.Errorf("level = %q, want warning", f.Level)
	}
}

func TestAssessRanksWorstFindingFirst(t *testing.T) {
	r := testRecorder(t, DefaultInterval, DefaultRetention)
	h := r.Assess(context.Background(), &sysinfo.Snapshot{
		CPU:     sysinfo.CPUStats{Cores: 2, LoadAvg5: 2.2},
		Memory:  sysinfo.MemoryStats{Total: 8 << 30, Available: 6 << 30},
		Mounts:  []sysinfo.MountStats{{Mountpoint: "/", Total: 100 << 30, Free: 2 << 30, UsedPercent: 98}},
		Sockets: sysinfo.Sockets{TCPTimeWait: 20000},
	})
	if len(h.Findings) < 3 {
		t.Fatalf("expected several findings, got %+v", h.Findings)
	}
	if h.Findings[0].Level != "critical" {
		t.Errorf("first finding is %q (%s), want the critical one first",
			h.Findings[0].Level, h.Findings[0].Title)
	}
	if h.Status != "critical" {
		t.Errorf("status = %q, want critical", h.Status)
	}
}

// Steal is the finding a VPS panel exists to surface: no change inside the
// guest can fix it, so it has to be named rather than folded into "CPU busy".
func TestAssessNamesCPUSteal(t *testing.T) {
	r := testRecorder(t, DefaultInterval, 0) // no recorder, so the live frame decides
	h := r.Assess(context.Background(), &sysinfo.Snapshot{
		CPU:    sysinfo.CPUStats{Cores: 4, Modes: sysinfo.CPUModes{Steal: 22, User: 40, Idle: 38}},
		Memory: sysinfo.MemoryStats{Total: 8 << 30, Available: 6 << 30},
	})
	f := findByID(t, h, "steal")
	if f.Level != "critical" {
		t.Errorf("level = %q, want critical at 22%% steal", f.Level)
	}
	if f.Advice == "" {
		t.Error("steal finding carries no advice, which is the only useful part of it")
	}
}

// PSI is optional in the kernel. Absent it, the checks must stay quiet rather
// than reading three missing files as three healthy zeroes.
func TestAssessSkipsPressureWithoutPSI(t *testing.T) {
	r := testRecorder(t, DefaultInterval, DefaultRetention)
	h := r.Assess(context.Background(), &sysinfo.Snapshot{
		Memory:   sysinfo.MemoryStats{Total: 8 << 30, Available: 6 << 30},
		Pressure: sysinfo.Pressure{Supported: false, CPUSome: 0},
	})
	for _, f := range h.Findings {
		if f.Metric == "psiCpu" || f.Metric == "psiMem" || f.Metric == "psiIo" {
			t.Fatalf("pressure finding %q raised on a kernel without PSI", f.ID)
		}
	}
}

func TestAssessFlagsPressureWhenSupported(t *testing.T) {
	r := testRecorder(t, DefaultInterval, DefaultRetention)
	h := r.Assess(context.Background(), &sysinfo.Snapshot{
		Memory:   sysinfo.MemoryStats{Total: 8 << 30, Available: 6 << 30},
		Pressure: sysinfo.Pressure{Supported: true, IOSome: 45, IOSome60: 40, IOSome300: 30, IOFull60: 30},
	})
	f := findByID(t, h, "psi-io")
	if f.Level != "critical" {
		t.Errorf("level = %q, want critical with every task stalled on storage", f.Level)
	}
	if f.Area != AreaStorage || len(f.Evidence) == 0 {
		t.Errorf("area/evidence = %q/%+v, want storage with its working", f.Area, f.Evidence)
	}
}

// A ten-second spike — a build finishing, a test suite starting — is not a
// saturated machine. The verdict waits for the minute and the five minutes to
// agree, and reports the sustained figure, not the spike.
func TestAssessIgnoresAShortPressureSpike(t *testing.T) {
	r := testRecorder(t, DefaultInterval, DefaultRetention)
	h := r.Assess(context.Background(), &sysinfo.Snapshot{
		CPU:      sysinfo.CPUStats{Cores: 8, LoadAvg5: 22},
		Memory:   sysinfo.MemoryStats{Total: 8 << 30, Available: 6 << 30},
		Pressure: sysinfo.Pressure{Supported: true, CPUSome: 74, CPUSome60: 20, CPUSome300: 8},
	})
	for _, f := range h.Findings {
		if f.Area == AreaCPU {
			t.Fatalf("a spike raised %q: %+v", f.ID, f)
		}
	}

	sustained := r.Assess(context.Background(), &sysinfo.Snapshot{
		CPU:      sysinfo.CPUStats{Cores: 8, LoadAvg5: 22},
		Memory:   sysinfo.MemoryStats{Total: 8 << 30, Available: 6 << 30},
		Pressure: sysinfo.Pressure{Supported: true, CPUSome: 74, CPUSome60: 65, CPUSome300: 45},
	})
	f := findByID(t, sustained, "psi-cpu")
	if f.Level != "critical" || f.Value != 65 {
		t.Errorf("level/value = %q/%v, want critical judged on the minute", f.Level, f.Value)
	}
}

// Load counts tasks waiting for a core and tasks blocked on a disk, so on a
// kernel with pressure readings it only repeats what they already said.
func TestAssessReportsLoadOnlyWithoutPSI(t *testing.T) {
	r := testRecorder(t, DefaultInterval, DefaultRetention)
	busy := sysinfo.Snapshot{
		CPU:      sysinfo.CPUStats{Cores: 8, LoadAvg5: 22},
		Memory:   sysinfo.MemoryStats{Total: 8 << 30, Available: 6 << 30},
		Pressure: sysinfo.Pressure{Supported: true, CPUSome60: 65, CPUSome300: 45},
	}
	h := r.Assess(context.Background(), &busy)
	for _, f := range h.Findings {
		if f.ID == "load" {
			t.Fatal("load reported beside CPU pressure: one problem told twice")
		}
	}
	busy.Pressure = sysinfo.Pressure{}
	findByID(t, r.Assess(context.Background(), &busy), "load")
}

// Swap full of idle pages beside gigabytes of free memory costs nothing, and
// is the single noisiest line the old check produced.
func TestAssessDoesNotReportIdleSwap(t *testing.T) {
	r := testRecorder(t, DefaultInterval, DefaultRetention)
	for _, psi := range []bool{true, false} {
		h := r.Assess(context.Background(), &sysinfo.Snapshot{
			Memory:   sysinfo.MemoryStats{Total: 24 << 30, Available: 8 << 30},
			Swap:     sysinfo.SwapStats{Total: 4 << 30, Used: 4 << 30, UsedPercent: 100},
			Pressure: sysinfo.Pressure{Supported: psi},
		})
		if len(h.Findings) != 0 {
			t.Fatalf("psi=%v: findings = %+v, want none for idle swap", psi, h.Findings)
		}
	}
}

// Without pressure readings, nearly full swap beside short memory is the
// nearest honest proxy for thrashing; with tight memory the memory finding
// says it, escalated because there is no headroom left anywhere.
func TestAssessJudgesSwapAgainstMemory(t *testing.T) {
	r := testRecorder(t, DefaultInterval, DefaultRetention)
	short := r.Assess(context.Background(), &sysinfo.Snapshot{
		Memory: sysinfo.MemoryStats{Total: 10 << 30, Available: 1536 << 20},
		Swap:   sysinfo.SwapStats{Total: 4 << 30, Used: 3600 << 20, UsedPercent: 88},
	})
	if f := findByID(t, short, "swap"); f.Level != "warning" {
		t.Errorf("swap level = %q, want warning", f.Level)
	}
	exhausted := r.Assess(context.Background(), &sysinfo.Snapshot{
		Memory: sysinfo.MemoryStats{Total: 10 << 30, Available: 800 << 20},
		Swap:   sysinfo.SwapStats{Total: 4 << 30, Used: 4 << 30, UsedPercent: 100},
	})
	if f := findByID(t, exhausted, "memory"); f.Level != "critical" {
		t.Errorf("memory level = %q, want critical with swap full too", f.Level)
	}
	for _, f := range exhausted.Findings {
		if f.ID == "swap" {
			t.Fatal("swap reported beside the memory finding that already names it")
		}
	}
}

// High iowait with nothing blocked is a large sequential read, which is what a
// disk is for. It only becomes a finding when processes are actually stuck.
func TestAssessIOWaitNeedsBlockedProcesses(t *testing.T) {
	r := testRecorder(t, DefaultInterval, DefaultRetention)
	// Without PSI, which would otherwise answer this directly.
	base := sysinfo.Snapshot{
		CPU:    sysinfo.CPUStats{Cores: 8, Modes: sysinfo.CPUModes{IOWait: 35}},
		Memory: sysinfo.MemoryStats{Total: 8 << 30, Available: 6 << 30},
	}
	quiet := r.Assess(context.Background(), &base)
	for _, f := range quiet.Findings {
		if f.ID == "iowait" {
			t.Fatal("raised iowait with no blocked processes")
		}
	}

	base.Procs = sysinfo.ProcCounts{Blocked: 11, Running: 1, Total: 300}
	busy := r.Assess(context.Background(), &base)
	findByID(t, busy, "iowait")
}

func TestAssessHandlesNilSnapshot(t *testing.T) {
	r := testRecorder(t, DefaultInterval, DefaultRetention)
	h := r.Assess(context.Background(), nil)
	if h.Status != "ok" || len(h.Findings) != 0 {
		t.Fatalf("nil snapshot produced %+v", h)
	}
}

func findByID(t *testing.T, h Health, id string) Finding {
	t.Helper()
	for _, f := range h.Findings {
		if f.ID == id {
			return f
		}
	}
	t.Fatalf("no finding %q in %+v", id, h.Findings)
	return Finding{}
}

// A leaking service exhausts file handles while every utilisation chart reads
// idle, so the ceiling is judged where the kernel reports a real one.
func TestAssessFlagsFileHandleExhaustion(t *testing.T) {
	r := testRecorder(t, DefaultInterval, DefaultRetention)
	h := r.Assess(context.Background(), &sysinfo.Snapshot{
		Files: sysinfo.FileHandles{Open: 85_000, Max: 100_000},
	})
	if len(h.Findings) != 1 || h.Findings[0].ID != "files" || h.Findings[0].Level != "warning" {
		t.Fatalf("findings = %+v, want one file-handle warning", h.Findings)
	}
}

// An unbounded ceiling reads as Max 0, and a percentage of nothing is not a
// finding.
func TestAssessIgnoresFileHandlesWithoutACeiling(t *testing.T) {
	r := testRecorder(t, DefaultInterval, DefaultRetention)
	h := r.Assess(context.Background(), &sysinfo.Snapshot{
		Files: sysinfo.FileHandles{Open: 85_000, Max: 0},
	})
	if len(h.Findings) != 0 {
		t.Fatalf("findings = %+v, want none", h.Findings)
	}
}

// A sensor is judged against its own limits only: a reading with no
// thresholds is a fact, not a verdict, and one past its critical mark ranks
// above one past its high-water mark.
func TestAssessJudgesSensorsByTheirOwnThresholds(t *testing.T) {
	r := testRecorder(t, DefaultInterval, DefaultRetention)
	h := r.Assess(context.Background(), &sysinfo.Snapshot{
		Sensors: []sysinfo.Sensor{
			{Name: "nvme", TempC: 71, High: 70, Critical: 85},
			{Name: "coretemp_Package id 0", TempC: 101, High: 95, Critical: 100},
			{Name: "acpitz", TempC: 90},
		},
	})
	if h.Status != "critical" {
		t.Fatalf("status = %q with %+v, want critical", h.Status, h.Findings)
	}
	if len(h.Findings) != 2 {
		t.Fatalf("findings = %+v, want the two sensors past their limits", h.Findings)
	}
	if h.Findings[0].ID != "temp:coretemp_Package id 0" || h.Findings[1].ID != "temp:nvme" {
		t.Fatalf("order = %q, %q; want critical first", h.Findings[0].ID, h.Findings[1].ID)
	}
}

func TestAssessDrawsEveryAreaOnAHealthyHost(t *testing.T) {
	r := testRecorder(t, DefaultInterval, DefaultRetention)
	h := r.Assess(context.Background(), &sysinfo.Snapshot{
		CPU:      sysinfo.CPUStats{Cores: 4},
		Memory:   sysinfo.MemoryStats{Total: 8 << 30, Available: 5 << 30},
		Mounts:   []sysinfo.MountStats{{Mountpoint: "/", Total: 100 << 30, Free: 29 << 30, UsedPercent: 71}, {Mountpoint: "/boot", Total: 1 << 30, UsedPercent: 20}},
		Pressure: sysinfo.Pressure{Supported: true, CPUSome60: 12},
		Files:    sysinfo.FileHandles{Open: 3000, Max: 100_000},
		Net:      []sysinfo.NetStats{{Interface: "ens3", Kind: "physical"}},
	})
	want := map[string]AreaVerdict{
		AreaCPU:        {AreaCPU, "ok", "12% stalled"},
		AreaMemory:     {AreaMemory, "ok", "5.0 GB free"},
		AreaStorage:    {AreaStorage, "ok", "/ at 71%"},
		AreaNetwork:    {AreaNetwork, "ok", "measuring"},
		AreaServices:   {AreaServices, "unknown", "not read"},
		AreaContainers: {AreaContainers, "unknown", "not read"},
		AreaHardware:   {AreaHardware, "ok", "no sensors · handles 3.0%"},
	}
	if len(h.Areas) != len(areaOrder) {
		t.Fatalf("areas = %+v, want all seven", h.Areas)
	}
	for i, area := range h.Areas {
		if area.ID != areaOrder[i] || area != want[area.ID] {
			t.Errorf("area %d = %+v, want %+v", i, area, want[areaOrder[i]])
		}
	}
}

func TestAssessRaisesTheAreaOfItsFindings(t *testing.T) {
	r := testRecorder(t, DefaultInterval, DefaultRetention)
	h := r.Assess(context.Background(), &sysinfo.Snapshot{
		Memory: sysinfo.MemoryStats{Total: 8 << 30, Available: 6 << 30},
		Mounts: []sysinfo.MountStats{{Mountpoint: "/", Total: 100 << 30, Free: 2 << 30, UsedPercent: 98}},
	})
	for _, area := range h.Areas {
		if area.ID == AreaStorage && area.Status != "critical" {
			t.Fatalf("storage = %+v, want critical", area)
		}
	}
	f := findByID(t, h, "disk:/")
	if len(f.Subjects) != 1 || f.Subjects[0].Kind != "mount" || f.Subjects[0].ID != "/" {
		t.Fatalf("subjects = %+v, want the mount", f.Subjects)
	}
}

func netSnap(nets ...sysinfo.NetStats) *sysinfo.Snapshot {
	return &sysinfo.Snapshot{Memory: sysinfo.MemoryStats{Total: 8 << 30, Available: 6 << 30}, Net: nets}
}

// Loss is judged on what an interface dropped since the last check: four
// million drops since boot with none since is history, not a finding.
func TestLinkLossIsJudgedOverAWindow(t *testing.T) {
	var w linkWatch
	start := time.Unix(10_000, 0)
	tunnel := sysinfo.NetStats{Interface: "tailscale0", Kind: "tunnel", DropOut: 4_214_828, PacketsSent: 212_000_000}
	uplink := sysinfo.NetStats{Interface: "ens3", Kind: "physical", PacketsRecv: 1_000_000}
	veth := sysinfo.NetStats{Interface: "veth1", Kind: "virtual", DropIn: 9_000_000}

	first := w.observe([]sysinfo.NetStats{tunnel, uplink, veth}, start)
	if len(first.windows) != 0 || first.judged != 2 {
		t.Fatalf("first check = %+v, want no window yet and two judged links", first)
	}
	if got := networkFindings(netSnap(tunnel, uplink, veth), first); len(got) != 0 {
		t.Fatalf("since-boot counters raised %+v", got)
	}

	// Thirty seconds on: too short to close a window.
	uplink.PacketsRecv += 10_000
	uplink.DropIn += 900
	if mid := w.observe([]sysinfo.NetStats{tunnel, uplink, veth}, start.Add(30*time.Second)); len(mid.windows) != 0 {
		t.Fatalf("window closed early: %+v", mid.windows)
	}

	uplink.PacketsRecv += 10_000
	uplink.DropIn += 300
	tunnel.PacketsSent += 50_000
	tunnel.DropOut += 20
	veth.DropIn += 50_000
	links := w.observe([]sysinfo.NetStats{tunnel, uplink, veth}, start.Add(90*time.Second))
	got := networkFindings(netSnap(tunnel, uplink, veth), links)
	if len(got) != 1 || got[0].ID != "drops:ens3" || got[0].Level != "warning" {
		t.Fatalf("findings = %+v, want only the uplink's current loss", got)
	}
	if got[0].Subjects[0].ID != "ens3" || got[0].Area != AreaNetwork {
		t.Fatalf("subject/area = %+v/%q", got[0].Subjects, got[0].Area)
	}

	// A check shortly after reports what the window found rather than nothing.
	again := w.observe([]sysinfo.NetStats{tunnel, uplink, veth}, start.Add(100*time.Second))
	if len(networkFindings(netSnap(tunnel, uplink, veth), again)) != 1 {
		t.Fatal("verdict dropped between windows")
	}

	// Counters going backwards are a recreated interface: no window, no
	// negative arithmetic, and a fresh baseline.
	uplink = sysinfo.NetStats{Interface: "ens3", Kind: "physical", PacketsRecv: 10}
	reset := w.observe([]sysinfo.NetStats{uplink}, start.Add(200*time.Second))
	if _, ok := reset.windows["ens3"]; ok {
		t.Fatalf("reset interface kept a window: %+v", reset.windows)
	}
}

func TestLinkLossOnATunnelIsANoticeOnlyWhenHeavy(t *testing.T) {
	var w linkWatch
	start := time.Unix(10_000, 0)
	tunnel := sysinfo.NetStats{Interface: "wg0", Kind: "tunnel", PacketsSent: 1000}
	w.observe([]sysinfo.NetStats{tunnel}, start)
	tunnel.PacketsSent += 5_000
	tunnel.DropOut += 1_000
	links := w.observe([]sysinfo.NetStats{tunnel}, start.Add(2*time.Minute))
	got := networkFindings(netSnap(tunnel), links)
	if len(got) != 1 || got[0].Level != "notice" {
		t.Fatalf("findings = %+v, want one notice", got)
	}
	if got[0].Detail != "1,000 packets dropped in the last 2 minutes (17% of traffic)" {
		t.Fatalf("detail = %q", got[0].Detail)
	}
}

// A baseline nobody advanced for a long stretch describes a past nobody was
// watching; reporting it would put an old incident on screen as a new one.
func TestLinkLossForgetsAStaleBaseline(t *testing.T) {
	var w linkWatch
	start := time.Unix(10_000, 0)
	uplink := sysinfo.NetStats{Interface: "eth0", Kind: "physical", PacketsRecv: 1000}
	w.observe([]sysinfo.NetStats{uplink}, start)
	uplink.DropIn += 50_000
	uplink.PacketsRecv += 1000
	if late := w.observe([]sysinfo.NetStats{uplink}, start.Add(time.Hour)); len(late.windows) != 0 {
		t.Fatalf("stale baseline produced %+v", late.windows)
	}
}
