package procs

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The two orders exist because the two questions have different answers: a
// leaking service can hold gigabytes while sitting at 0% CPU, and a CPU-only
// sort buries it below every busy process on the box.
func TestSortByPutsTheRightRowFirst(t *testing.T) {
	rows := []Process{
		{Name: "busy", CPUPercent: 90, RSS: 10 << 20},
		{Name: "leaky", CPUPercent: 0, RSS: 6 << 30},
		{Name: "idle", CPUPercent: 0, RSS: 1 << 20},
	}

	SortBy(rows, ByCPU)
	if rows[0].Name != "busy" {
		t.Errorf("by cpu, first row = %q, want busy", rows[0].Name)
	}
	// Tie-broken by the other measure, so a run of 0% processes still comes
	// back in a meaningful order rather than /proc's.
	if rows[1].Name != "leaky" {
		t.Errorf("by cpu, second row = %q, want leaky (tie broken by RSS)", rows[1].Name)
	}

	SortBy(rows, ByMemory)
	if rows[0].Name != "leaky" {
		t.Errorf("by memory, first row = %q, want leaky", rows[0].Name)
	}
	if rows[1].Name != "busy" {
		t.Errorf("by memory, second row = %q, want busy (tie broken by CPU)", rows[1].Name)
	}
}

// An unknown sort decides which rows a table shows. Falling back beats a 400,
// which would leave the page with no data at all over a typo in a query string.
func TestParseOrderFallsBackToCPU(t *testing.T) {
	if got := ParseOrder("memory"); got != ByMemory {
		t.Errorf(`ParseOrder("memory") = %q, want memory`, got)
	}
	if got := ParseOrder("io"); got != ByIO {
		t.Errorf(`ParseOrder("io") = %q, want io`, got)
	}
	if got := ParseOrder("uptime"); got != ByUptime {
		t.Errorf(`ParseOrder("uptime") = %q, want uptime`, got)
	}
	for _, in := range []string{"", "cpu", "banana", "MEMORY", "auto"} {
		if got := ParseOrder(in); got != ByCPU {
			t.Errorf("ParseOrder(%q) = %q, want cpu", in, got)
		}
	}
}

func TestSelectFiltersBeforeItCaps(t *testing.T) {
	rows := []Process{
		{PID: 1, Name: "heavy", Username: "root", CPUPercent: 99, State: "running", Manager: "systemd"},
		{PID: 2, Name: "also-heavy", Username: "root", CPUPercent: 98, State: "running", Manager: "systemd"},
		{PID: 3, Name: "needle", Username: "deploy", CPUPercent: 1, State: "sleeping", Manager: "pm2", ManagerName: "web"},
	}
	got := Select(rows, ListOptions{Limit: 1, Order: ByCPU, Query: "needle"})
	if got.Total != 1 || len(got.Processes) != 1 || got.Processes[0].PID != 3 {
		t.Fatalf("filtered result = %+v, want the process below the unfiltered cap", got)
	}
	if got.Truncated {
		t.Fatal("one match at a limit of one must not be reported as truncated")
	}
	if got.Available != 3 {
		t.Fatalf("available = %d, want 3", got.Available)
	}
}

func TestSelectFiltersByDetectedOwnerAndReportsFacets(t *testing.T) {
	rows := []Process{
		{PID: 10, Username: "root", State: "running", Manager: "systemd"},
		{PID: 11, Username: "deploy", State: "sleeping", Manager: "pm2", ioRateReady: true},
		{PID: 12, Username: "deploy", State: "sleeping", Manager: "pm2"},
	}
	got := Select(rows, ListOptions{Order: ByCPU, User: "deploy", Manager: "pm2"})
	if got.Total != 2 {
		t.Fatalf("total = %d, want 2", got.Total)
	}
	if len(got.Users) != 2 || got.Users[0].Value != "deploy" || got.Users[0].Count != 2 {
		t.Fatalf("user facets = %+v", got.Users)
	}
	if len(got.Managers) != 2 || got.Managers[0].Value != "pm2" || got.Managers[0].Count != 2 {
		t.Fatalf("manager facets = %+v", got.Managers)
	}
	if !got.RatesReady {
		t.Fatal("a sampled row did not mark process I/O rates ready")
	}
}

func TestCounterRateHandlesFirstSampleResetAndElapsedTime(t *testing.T) {
	if got := counterRate(100, 300, 2*time.Second); got != 100 {
		t.Fatalf("rate = %d, want 100", got)
	}
	if got := counterRate(300, 10, time.Second); got != 0 {
		t.Fatalf("reset rate = %d, want 0", got)
	}
	if got := counterRate(100, 300, 0); got != 0 {
		t.Fatalf("zero elapsed rate = %d, want 0", got)
	}
}

func TestManagerFromCgroupRecognisesCommonOwners(t *testing.T) {
	id := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	tests := []struct {
		name, cgroup, manager, owner string
	}{
		{"systemd", "0::/system.slice/postgresql.service\n", "systemd", "postgresql.service"},
		{"session", "0::/user.slice/user-1000.slice/session-4.scope\n", "session", "session-4.scope"},
		{"docker v2", "0::/system.slice/docker-" + id + ".scope\n", "container", id[:12]},
		{"containerd v1", "9:memory:/kubepods/burstable/cri-containerd-" + id + ".scope\n", "container", id[:12]},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager, owner := managerFromCgroup(tt.cgroup)
			if manager != tt.manager || owner != tt.owner {
				t.Fatalf("manager = %q %q, want %q %q", manager, owner, tt.manager, tt.owner)
			}
		})
	}
}

func TestMarkPM2OverridesAParentCgroup(t *testing.T) {
	rows := []Process{{PID: 42, Manager: "systemd", ManagerName: "pm2-root.service"}}
	MarkPM2(rows, []PM2Process{{PID: 42, Name: "api"}})
	if rows[0].Manager != "pm2" || rows[0].ManagerName != "api" {
		t.Fatalf("managed row = %+v", rows[0])
	}
}

// The ports page reads a socket's supervisor under the process table it read
// the sockets from, which HOST_PROC may move.
func TestManagerOfReadsTheGivenProcessTable(t *testing.T) {
	root := t.TempDir()
	writeCgroup(t, root, 2450808, "0::/system.slice/ssh.service\n")
	writeCgroup(t, root, 1802112, "0::/system.slice/docker-c13e58c8f7b4822ff505f3e86405a1cb386156d3cc6a3ca4068fb715fb98b2db.scope\n")
	for _, c := range []struct {
		pid           int32
		cmdline       string
		manager, name string
	}{
		{2450808, "sshd: /usr/sbin/sshd -D", "systemd", "ssh.service"},
		{1802112, "caddy run", "container", "c13e58c8f7b4"},
		{4, "/usr/bin/gone", "unmanaged", ""},
		{5, "", "kernel", "kernel"},
	} {
		manager, name := ManagerOf(root, c.pid, c.cmdline)
		if manager != c.manager || name != c.name {
			t.Errorf("ManagerOf(%d) = %s %q, want %s %q", c.pid, manager, name, c.manager, c.name)
		}
	}
}

func writeCgroup(t *testing.T, root string, pid int, content string) {
	t.Helper()
	dir := filepath.Join(root, strconv.Itoa(pid))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cgroup"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The table and an open sheet read the same process on their own clocks. A
// read landing moments after the last measured the scheduler's rounding; it
// reports the last full window instead and leaves the base where it was.
func TestRatesNeedAWholeWindow(t *testing.T) {
	start := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	created := start.Add(-time.Hour)
	read := func(cpuSeconds float64, rss uint64) *Process {
		return &Process{PID: 7, CreateTime: created, RSS: rss, cpuSeconds: cpuSeconds, cpuCounterReady: true}
	}

	first := applyProcessRates(read(10, 100), ioSample{}, start)
	row := read(12, 200)
	second := applyProcessRates(row, first, start.Add(4*time.Second))
	if !row.CPUReady || row.CPUPercent != 50 {
		t.Fatalf("4s window: cpu %v ready %v, want 50%%", row.CPUPercent, row.CPUReady)
	}

	soon := read(12.2, 300)
	kept := applyProcessRates(soon, second, start.Add(4*time.Second+50*time.Millisecond))
	if !soon.CPUReady || soon.CPUPercent != 50 {
		t.Fatalf("50ms after: cpu %v, want the last window's 50%%", soon.CPUPercent)
	}
	if !kept.at.Equal(second.at) {
		t.Fatal("a read too soon to measure moved the window's start")
	}

	later := read(14, 400)
	applyProcessRates(later, kept, start.Add(8*time.Second))
	if later.CPUPercent != 50 {
		t.Fatalf("next full window: cpu %v, want 50%%", later.CPUPercent)
	}
}

// A sheet opens on the process's recent shape, so the sampler keeps a point
// per measured window — at most one every historyStep, and only so many.
func TestHistoryKeepsRecentWindows(t *testing.T) {
	start := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	created := start.Add(-time.Hour)
	sample := ioSample{}
	for i := 0; i <= historySize+10; i++ {
		row := &Process{PID: 7, CreateTime: created, RSS: uint64(i), cpuSeconds: float64(i), cpuCounterReady: true}
		sample = applyProcessRates(row, sample, start.Add(time.Duration(i)*historyStep))
	}
	if len(sample.history) != historySize {
		t.Fatalf("history = %d points, want %d", len(sample.history), historySize)
	}
	if last := sample.history[len(sample.history)-1]; last.RSS != uint64(historySize+10) {
		t.Fatalf("last point = %+v, want the newest read", last)
	}

	// A PID reused by another process starts over rather than inheriting.
	reused := &Process{PID: 7, CreateTime: start, cpuSeconds: 1, cpuCounterReady: true}
	if fresh := applyProcessRates(reused, sample, start.Add(time.Hour)); len(fresh.history) != 0 {
		t.Fatalf("a replacement inherited %d points", len(fresh.history))
	}
}

func TestGroupsCountSharedMemoryOnce(t *testing.T) {
	mib := uint64(1 << 20)
	rows := []Process{
		{PID: 10, Name: "postgres", Manager: "systemd", ManagerName: "postgresql.service", CPUPercent: 3, RSS: 600 * mib, Shared: 512 * mib},
		{PID: 11, Name: "postgres", Manager: "systemd", ManagerName: "postgresql.service", CPUPercent: 1, RSS: 560 * mib, Shared: 512 * mib},
		{PID: 20, Name: "chrome", Manager: "session", ManagerName: "session-4.scope", CPUPercent: 40, RSS: 300 * mib, Shared: 100 * mib},
		{PID: 21, Name: "chrome --type=renderer --lang=en-US", Manager: "unmanaged", CPUPercent: 20, RSS: 200 * mib, Shared: 100 * mib},
		{PID: 2, Name: "kthreadd", Manager: "kernel", ManagerName: "kernel"},
		{PID: 30, Name: "node", Manager: "container", ManagerName: "3f9a1c0b7d2e", ManagerLabel: "api", CPUPercent: 5, RSS: 100 * mib},
	}
	got := groups(rows, 8)
	byKey := map[string]ProcessGroup{}
	for _, g := range got {
		byKey[g.Key] = g
	}
	pg := byKey["systemd:postgresql.service"]
	if pg.Count != 2 || pg.Memory != (88+48+512)*mib || pg.Name != "postgresql.service" || pg.PID != 10 {
		t.Fatalf("postgres group = %+v", pg)
	}
	chrome := byKey["name:chrome"]
	if chrome.Count != 2 || chrome.CPUPercent != 60 || chrome.PID != 20 {
		t.Fatalf("chrome group = %+v, want both copies whatever started them", chrome)
	}
	if byKey["kernel:"].Name != "Kernel threads" {
		t.Fatalf("kernel group = %+v", byKey["kernel:"])
	}
	if byKey["container:3f9a1c0b7d2e"].Label != "api" {
		t.Fatalf("container group = %+v", byKey["container:3f9a1c0b7d2e"])
	}
	if got[0].Key != "name:chrome" {
		t.Fatalf("first group = %s, want the busiest", got[0].Key)
	}

	only := Select(rows, ListOptions{Order: ByCPU, Group: "systemd:postgresql.service"})
	if only.Total != 2 || len(only.Groups) != len(got) {
		t.Fatalf("group filter = %d rows and %d groups, want 2 rows and every group", only.Total, len(only.Groups))
	}
}

// An empty command line is a kernel thread's, a zombie's, or a process's
// caught mid-exit. The flags in stat say which.
func TestManagerOfTellsAKernelThreadFromAZombie(t *testing.T) {
	root := t.TempDir()
	writeProcFile(t, root, 2, "stat", "2 (kthreadd) S 0 0 0 0 -1 2129984 0 0 0 0 0 0 0 0 20 0 1 0 2 0 0\n")
	writeProcFile(t, root, 900, "stat", "900 (sleep (1)) Z 899 899 899 0 -1 4228108 0 0 0 0 0 0 0 0 20 0 1 0 2 0 0\n")
	writeCgroup(t, root, 900, "0::/\n")
	if manager, _ := ManagerOf(root, 2, ""); manager != "kernel" {
		t.Fatalf("kthreadd = %s, want kernel", manager)
	}
	if manager, _ := ManagerOf(root, 900, ""); manager != "unmanaged" {
		t.Fatalf("zombie = %s, want unmanaged", manager)
	}
	if err := Controllable(&Process{State: "zombie"}); !errors.Is(err, ErrZombie) {
		t.Fatalf("zombie control = %v", err)
	}
	if err := Controllable(&Process{State: "sleeping", Manager: "kernel"}); !errors.Is(err, ErrKernelThread) {
		t.Fatalf("kernel control = %v", err)
	}
	if err := Controllable(&Process{State: "sleeping", Manager: "systemd"}); err != nil {
		t.Fatalf("service control = %v", err)
	}
}

func writeProcFile(t *testing.T, root string, pid int, name, content string) {
	t.Helper()
	dir := filepath.Join(root, strconv.Itoa(pid))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The table reports nice on the scale SetNice takes, so a process started
// at nice 10 reads 10, and an ordinary one 0, not the kernel's raw 20.
func TestProcessNiceIsReadOnTheSettableScale(t *testing.T) {
	for _, want := range []int32{0, 10, 19} {
		cmd := exec.Command("nice", "-n", strconv.Itoa(int(want)), "sleep", "5")
		if err := cmd.Start(); err != nil {
			t.Skip("no nice binary:", err)
		}
		pid := int32(cmd.Process.Pid)
		// nice execs sleep after setting the value; wait until it has.
		deadline := time.Now().Add(2 * time.Second)
		var got int32
		for time.Now().Before(deadline) {
			if row, err := NewTable().Detail(t.Context(), pid); err == nil && strings.HasPrefix(row.Name, "sleep") {
				got = row.Nice
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		if got != want {
			t.Fatalf("nice -n %d read as %d", want, got)
		}
	}
}
