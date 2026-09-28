package procs

import (
	"os"
	"path/filepath"
	"strconv"
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
