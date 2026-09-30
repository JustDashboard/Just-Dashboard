package procs

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestCPUIntervalsUseElapsedTimeAndRejectPIDReuse(t *testing.T) {
	table := NewTable()
	now := time.Unix(1000, 0)
	table.now = func() time.Time { return now }
	start := now.Add(-time.Hour)
	first := []Process{{PID: 42, CreateTime: start, cpuSeconds: 100, cpuCounterReady: true, ioCounterReady: true, IORead: 100}}
	table.applyIORates(first)
	if first[0].CPUReady || first[0].IOReady {
		t.Fatal("first counter treated as current usage")
	}
	now = now.Add(2 * time.Second)
	second := []Process{{PID: 42, CreateTime: start, cpuSeconds: 103, cpuCounterReady: true, ioCounterReady: true, IORead: 500}}
	table.applyIORates(second)
	if !second[0].CPUReady || second[0].CPUPercent != 150 || second[0].CPUWindow != 2 || second[0].IOReadRate != 200 {
		t.Fatalf("interval = %+v", second[0])
	}
	now = now.Add(time.Second)
	replaced := []Process{{PID: 42, CreateTime: now, cpuSeconds: 500, cpuCounterReady: true, ioCounterReady: true, IORead: 99999}}
	table.applyIORates(replaced)
	if replaced[0].CPUReady || replaced[0].IOReady || replaced[0].CPUPercent != 0 {
		t.Fatal("replacement inherited interval")
	}
	now = now.Add(time.Second)
	unreadable := []Process{{PID: 42, CreateTime: replaced[0].CreateTime}}
	table.applyIORates(unreadable)
	if unreadable[0].CPUReady || unreadable[0].IOReady {
		t.Fatal("unreadable counters treated as zero usage")
	}
	now = now.Add(time.Second)
	recovery := []Process{{PID: 42, CreateTime: replaced[0].CreateTime, cpuSeconds: 501, cpuCounterReady: true}}
	table.applyIORates(recovery)
	if recovery[0].CPUReady {
		t.Fatal("missing baseline invented a rate")
	}
}

func TestCPUIntervalRejectsResetAndUnknownStart(t *testing.T) {
	for _, test := range []struct {
		name          string
		start         time.Time
		before, after float64
	}{{"reset", time.Unix(900, 0), 100, 2}, {"unknown start", time.Time{}, 10, 20}} {
		t.Run(test.name, func(t *testing.T) {
			table := NewTable()
			now := time.Unix(1000, 0)
			table.now = func() time.Time { return now }
			table.applyIORates([]Process{{PID: 2, CreateTime: test.start, cpuSeconds: test.before, cpuCounterReady: true}})
			now = now.Add(time.Second)
			rows := []Process{{PID: 2, CreateTime: test.start, cpuSeconds: test.after, cpuCounterReady: true}}
			table.applyIORates(rows)
			if rows[0].CPUReady {
				t.Fatal("invalid rate reported")
			}
		})
	}
}

func TestWorkloadsSortsEachResource(t *testing.T) {
	for _, test := range []struct {
		order string
		pid   int32
	}{{"cpu", 1}, {"memory", 2}, {"swap", 3}, {"io", 4}, {"handles", 5}} {
		rows := []Process{{PID: 1, CPUPercent: 99}, {PID: 2, RSS: 1000}, {PID: 3, Swap: 1000}, {PID: 4, IOReadRate: 200, IOWriteRate: 100}, {PID: 5, FDs: 100}}
		sortWorkloads(rows, test.order)
		if rows[0].PID != test.pid {
			t.Fatalf("%s first=%d, want %d", test.order, rows[0].PID, test.pid)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewTable().Workloads(ctx, "cpu"); err == nil {
		t.Fatal("canceled investigation continued")
	}
}

func TestWorkloadsNativeLinuxReadings(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	table := NewTable()
	report, err := table.Workloads(ctx, "handles")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Processes) == 0 || report.Total < 1 {
		t.Fatal("native process table is empty")
	}
	self, err := table.Detail(ctx, int32(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	if self.CreateTime.IsZero() || !self.MemoryReady || !self.FDReady || !self.cpuCounterReady {
		t.Fatalf("native evidence unavailable: %+v", self)
	}
	if self.FDs < 3 {
		t.Fatalf("open descriptors=%d, want at least stdin/stdout/stderr", self.FDs)
	}
}

func TestWorkloadsConcurrentReadersKeepIndependentEvidence(t *testing.T) {
	table := NewTable()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	done := make(chan error, 2)
	for _, order := range []string{"cpu", "memory"} {
		go func() {
			report, err := table.Workloads(ctx, order)
			if err == nil && report.Sort != order {
				err = fmt.Errorf("wrong evidence order: %s", report.Sort)
			}
			done <- err
		}()
	}
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}
