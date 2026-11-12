// Package selfstat reads what the dashboard's own process is using: its
// memory as the kernel and as the Go runtime count it, its goroutines and
// threads, its open files and the processor time it has taken.
//
// The dashboard measured the host, every container and every database, and
// nothing of itself. "Why is the backend at 130 MB" or "what is it doing at
// 3% CPU with nobody signed in" could be answered only from outside, with
// docker stats and a guess. One reading here is cheap enough to take on
// request: runtime.ReadMemStats stops the world for tens of microseconds,
// and the rest is two small files under /proc/self.
package selfstat

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// started is when this package was initialised, which is the process start
// to within the few milliseconds the runtime takes to reach it.
var started = time.Now()

// procSelf is where the kernel describes this process. A variable so a test
// can stand a directory in for it.
var procSelf = "/proc/self"

// Process is one reading of the dashboard's own process.
type Process struct {
	PID           int       `json:"pid"`
	StartedAt     time.Time `json:"startedAt,omitzero"`
	UptimeSeconds int64     `json:"uptimeSeconds"`
	GoVersion     string    `json:"goVersion"`
	// CPUs is how many processors the runtime schedules onto (GOMAXPROCS),
	// which a container's CPU limit lowers.
	CPUs       int `json:"cpus"`
	Goroutines int `json:"goroutines"`
	// Threads and OpenFiles are the kernel's counts, zero where /proc is
	// not there to read.
	Threads   int `json:"threads"`
	OpenFiles int `json:"openFiles"`
	// CPUUserSeconds and CPUSystemSeconds are the processor time taken
	// since the process started. Two readings a known time apart give the
	// share of a core in between; over the uptime they give the average.
	CPUUserSeconds   float64 `json:"cpuUserSeconds"`
	CPUSystemSeconds float64 `json:"cpuSystemSeconds"`
	// RSSBytes is the resident memory the kernel charges the process, the
	// figure docker stats and the host's process table show. It is larger
	// than the Go heap by the runtime's own structures, goroutine stacks,
	// memory freed but not yet returned, and anything mapped outside the
	// heap (SQLite's page cache is the dashboard's largest such part).
	RSSBytes uint64 `json:"rssBytes"`
	Memory   Memory `json:"memory"`
}

// Memory is the Go runtime's own account of the process's memory.
type Memory struct {
	// HeapAllocBytes is what live objects and those not yet collected hold.
	HeapAllocBytes uint64 `json:"heapAllocBytes"`
	HeapObjects    uint64 `json:"heapObjects"`
	// HeapInuseBytes is the heap spans in use; HeapIdleBytes those kept for
	// reuse, of which HeapReleasedBytes have been given back to the system.
	HeapInuseBytes    uint64 `json:"heapInuseBytes"`
	HeapIdleBytes     uint64 `json:"heapIdleBytes"`
	HeapReleasedBytes uint64 `json:"heapReleasedBytes"`
	StackInuseBytes   uint64 `json:"stackInuseBytes"`
	// SysBytes is everything the runtime has obtained from the system.
	SysBytes uint64 `json:"sysBytes"`
	// NextGCBytes is the heap size the next collection is aimed at.
	NextGCBytes uint64 `json:"nextGCBytes"`
	GCCount     uint32 `json:"gcCount"`
	// LastGCAt is when the last collection finished, absent before the
	// first.
	LastGCAt *time.Time `json:"lastGCAt,omitempty"`
	// GCPauseTotalSeconds is the stop-the-world time of every collection,
	// and GCCPUFraction the share of the process's available CPU time the
	// collector has used since it started.
	GCPauseTotalSeconds float64 `json:"gcPauseTotalSeconds"`
	GCCPUFraction       float64 `json:"gcCPUFraction"`
}

// Read takes one reading.
func Read() Process {
	now := time.Now()
	p := Process{
		PID:           os.Getpid(),
		StartedAt:     started,
		UptimeSeconds: int64(now.Sub(started) / time.Second),
		GoVersion:     runtime.Version(),
		CPUs:          runtime.GOMAXPROCS(0),
		Goroutines:    runtime.NumGoroutine(),
	}
	var usage syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &usage) == nil {
		p.CPUUserSeconds = seconds(usage.Utime)
		p.CPUSystemSeconds = seconds(usage.Stime)
	}
	p.RSSBytes, p.Threads = readStatus(procSelf + "/status")
	p.OpenFiles = countOpenFiles(procSelf + "/fd")

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	p.Memory = Memory{
		HeapAllocBytes:      m.HeapAlloc,
		HeapObjects:         m.HeapObjects,
		HeapInuseBytes:      m.HeapInuse,
		HeapIdleBytes:       m.HeapIdle,
		HeapReleasedBytes:   m.HeapReleased,
		StackInuseBytes:     m.StackInuse,
		SysBytes:            m.Sys,
		NextGCBytes:         m.NextGC,
		GCCount:             m.NumGC,
		GCPauseTotalSeconds: float64(m.PauseTotalNs) / float64(time.Second),
		GCCPUFraction:       m.GCCPUFraction,
	}
	if m.LastGC != 0 {
		at := time.Unix(0, int64(m.LastGC))
		p.Memory.LastGCAt = &at
	}
	return p
}

func seconds(t syscall.Timeval) float64 {
	return float64(t.Sec) + float64(t.Usec)/1e6
}

// readStatus reads the resident size and the thread count from a
// /proc/<pid>/status. A line it cannot read leaves its figure at zero.
func readStatus(path string) (rss uint64, threads int) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		fields := strings.Fields(value)
		if len(fields) == 0 {
			continue
		}
		switch key {
		case "VmRSS":
			// "VmRSS:	   91234 kB": the kernel only ever prints kB here.
			if kb, err := strconv.ParseUint(fields[0], 10, 64); err == nil && len(fields) == 2 && fields[1] == "kB" {
				rss = kb * 1024
			}
		case "Threads":
			if n, err := strconv.Atoi(fields[0]); err == nil {
				threads = n
			}
		}
	}
	return rss, threads
}

// countOpenFiles counts the entries of a /proc/<pid>/fd, less the one the
// listing itself holds open.
func countOpenFiles(dir string) int {
	handle, err := os.Open(dir)
	if err != nil {
		return 0
	}
	defer handle.Close()
	names, err := handle.Readdirnames(-1)
	if err != nil || len(names) == 0 {
		return 0
	}
	return len(names) - 1
}
