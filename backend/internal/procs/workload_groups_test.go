package procs

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestWorkloadGroupsRankByTheRequestedMeasure(t *testing.T) {
	start := time.Unix(1000, 0)
	rows := []Process{
		{PID: 10, Name: "postgres", Manager: "systemd", ManagerName: "postgresql.service", RSS: 900 << 20, CPUPercent: 5, Username: "postgres", CreateTime: start},
		{PID: 11, Name: "postgres", Manager: "systemd", ManagerName: "postgresql.service", RSS: 100 << 20, CPUPercent: 1, Username: "postgres", CreateTime: start},
		{PID: 20, Name: "ffmpeg -i in.mp4", Manager: "session", ManagerName: "session-4.scope", CPUPercent: 380, Swap: 1 << 20, Username: "ubuntu", CreateTime: start},
		{PID: 30, Name: "java", Manager: "container", ManagerName: "3f9a1c0b7d2e", ManagerLabel: "elastic", Swap: 2 << 30, Username: "root", CreateTime: start},
	}
	for order, first := range map[string]string{"cpu": "name:ffmpeg", "memory": "systemd:postgresql.service", "swap": "container:3f9a1c0b7d2e"} {
		groups := workloadGroups(rows, order)
		if groups[0].Key != first {
			t.Fatalf("%s: first = %s, want %s", order, groups[0].Key, first)
		}
	}
	postgres := workloadGroups(rows, "memory")[0]
	if postgres.Count != 2 || postgres.PID != 10 || postgres.Name != "postgresql.service" ||
		len(postgres.Members) != 2 || postgres.Members[0].PID != 10 || postgres.Users[0] != "postgres" {
		t.Fatalf("postgres group = %+v", postgres)
	}
	if postgres.Launcher != nil {
		t.Fatal("a supervised group has its supervisor, not a launcher")
	}
	if elastic := workloadGroups(rows, "swap")[0]; elastic.Label != "elastic" || elastic.Swap != 2<<30 {
		t.Fatalf("container group = %+v", elastic)
	}
}

func TestWorkloadGroupsCapMembersButKeepTheCount(t *testing.T) {
	var rows []Process
	for pid := int32(100); pid < 400; pid++ {
		rows = append(rows, Process{PID: pid, PPID: 99, Name: "worker", Manager: "unmanaged", CPUPercent: float64(pid), CreateTime: time.Unix(int64(pid), 0)})
	}
	g := workloadGroups(rows, "cpu")[0]
	if g.Count != 300 || len(g.Members) != workloadMembersShown || !g.Truncated || g.Members[0].PID != 399 {
		t.Fatalf("group = count %d members %d truncated %v first %d", g.Count, len(g.Members), g.Truncated, g.Members[0].PID)
	}
}

// Forty renderers from several test workers share one runner: that runner is
// what keeps starting them, and is offered as the thing to stop.
func TestWorkloadGroupsFindTheLauncher(t *testing.T) {
	start := time.Unix(1000, 0)
	rows := []Process{
		{PID: 50, PPID: 40, Name: "bash", Manager: "session", CreateTime: start},
		{PID: 60, PPID: 50, Name: "MainThread", Cmdline: "/usr/bin/node playwright test", Manager: "session", CreateTime: start, Username: "ubuntu"},
		{PID: 61, PPID: 60, Name: "node", Cmdline: "node worker 1", Manager: "session", CreateTime: start},
		{PID: 62, PPID: 60, Name: "node", Cmdline: "node worker 2", Manager: "session", CreateTime: start},
		{PID: 70, PPID: 61, Name: "chrome-headless-shell --headless", Manager: "session", CPUPercent: 40, CreateTime: start},
		{PID: 71, PPID: 70, Name: "chrome-headless-shell --type=renderer", Manager: "session", CPUPercent: 90, CreateTime: start},
		{PID: 80, PPID: 62, Name: "chrome-headless-shell --headless", Manager: "session", CPUPercent: 30, CreateTime: start},
		{PID: 81, PPID: 80, Name: "chrome-headless-shell --type=renderer", Manager: "session", CPUPercent: 80, CreateTime: start},
	}
	var chrome WorkloadGroup
	for _, g := range workloadGroups(rows, "cpu") {
		if g.Key == "name:chrome-headless-shell" {
			chrome = g
		}
	}
	if chrome.Count != 4 || chrome.CPUPercent != 240 || chrome.PID != 71 {
		t.Fatalf("chrome group = %+v", chrome)
	}
	if chrome.Launcher == nil || chrome.Launcher.PID != 60 || chrome.Launcher.Name != "node" || chrome.Launcher.Username != "ubuntu" {
		t.Fatalf("launcher = %+v, want the runner both workers share", chrome.Launcher)
	}
}

// A group started straight from a shell has no launcher worth stopping: the
// shell is the operator's own session.
func TestWorkloadGroupsNeverOfferAShellAsLauncher(t *testing.T) {
	start := time.Unix(1000, 0)
	rows := []Process{
		{PID: 50, PPID: 1, Name: "zsh", Manager: "session", CreateTime: start},
		{PID: 70, PPID: 50, Name: "stress", Manager: "session", CPUPercent: 100, CreateTime: start},
		{PID: 71, PPID: 50, Name: "stress", Manager: "session", CPUPercent: 100, CreateTime: start},
		{PID: 90, PPID: 1, Name: "orphan", Manager: "unmanaged", CPUPercent: 50, CreateTime: start},
	}
	for _, g := range workloadGroups(rows, "cpu") {
		if g.Launcher != nil {
			t.Fatalf("%s offered launcher %+v", g.Key, g.Launcher)
		}
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
