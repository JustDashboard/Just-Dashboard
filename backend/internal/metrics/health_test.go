package metrics

import (
	"context"
	"testing"

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
		Pressure: sysinfo.Pressure{Supported: true, IOSome: 45},
	})
	f := findByID(t, h, "psi-io")
	if f.Level != "critical" {
		t.Errorf("level = %q, want critical at 45%% io pressure", f.Level)
	}
}

// High iowait with nothing blocked is a large sequential read, which is what a
// disk is for. It only becomes a finding when processes are actually stuck.
func TestAssessIOWaitNeedsBlockedProcesses(t *testing.T) {
	r := testRecorder(t, DefaultInterval, DefaultRetention)
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
