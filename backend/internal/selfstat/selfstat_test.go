package selfstat

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestAReadingSaysWhatTheProcessUses(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	before := Read()
	for range 25 {
		go func() { <-block }()
	}
	// Something for the heap and the collector to show.
	held := make([][]byte, 0, 64)
	for range 64 {
		held = append(held, make([]byte, 64<<10))
	}
	runtime.GC()
	p := Read()
	runtime.KeepAlive(held)

	if p.PID != os.Getpid() || p.GoVersion != runtime.Version() || p.CPUs < 1 {
		t.Fatalf("got %+v", p)
	}
	if p.Goroutines < before.Goroutines+25 {
		t.Fatalf("goroutines went from %d to %d with 25 started", before.Goroutines, p.Goroutines)
	}
	if p.StartedAt.After(time.Now()) || p.UptimeSeconds < 0 {
		t.Fatalf("started %s, up %d s", p.StartedAt, p.UptimeSeconds)
	}
	m := p.Memory
	if m.HeapAllocBytes < 64*(64<<10) || m.HeapObjects == 0 || m.SysBytes < m.HeapInuseBytes || m.HeapInuseBytes < m.HeapAllocBytes {
		t.Fatalf("the heap does not add up: %+v", m)
	}
	if m.GCCount == 0 || m.LastGCAt == nil || time.Since(*m.LastGCAt) > time.Minute {
		t.Fatalf("a collection just ran and the reading says %d, last %v", m.GCCount, m.LastGCAt)
	}
	if p.CPUUserSeconds+p.CPUSystemSeconds <= 0 {
		t.Fatalf("no processor time: %+v", p)
	}
	if runtime.GOOS == "linux" {
		// The kernel's side: resident memory holds at least the live heap,
		// and the process has its three standard streams open at the least.
		if p.RSSBytes < m.HeapAllocBytes/2 || p.Threads < 1 || p.OpenFiles < 3 {
			t.Fatalf("rss %d, threads %d, open files %d", p.RSSBytes, p.Threads, p.OpenFiles)
		}
	}
}

// A host without /proc (or a line the kernel words differently) leaves the
// kernel's figures at zero rather than failing the reading.
func TestAReadingWithoutProcLeavesTheKernelsFiguresOut(t *testing.T) {
	dir := t.TempDir()
	before := procSelf
	procSelf = filepath.Join(dir, "absent")
	t.Cleanup(func() { procSelf = before })
	if p := Read(); p.RSSBytes != 0 || p.Threads != 0 || p.OpenFiles != 0 || p.Goroutines == 0 {
		t.Fatalf("got %+v", p)
	}

	status := filepath.Join(dir, "status")
	if err := os.WriteFile(status, []byte("Name:\tjd\nVmRSS:\t   91234 kB\nThreads:\t17\nVmSwap:\t0 kB\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rss, threads := readStatus(status); rss != 91234*1024 || threads != 17 {
		t.Fatalf("got %d bytes, %d threads", rss, threads)
	}
	if err := os.WriteFile(status, []byte("VmRSS:\t12 pages\nThreads:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rss, threads := readStatus(status); rss != 0 || threads != 0 {
		t.Fatalf("a unit that is not kB was read as %d, and no thread count as %d", rss, threads)
	}
}
