package procs

import (
	"os"
	"slices"
	"sort"
	"strings"
	"time"
)

// WorkloadGroup is one workload, measured the way the advisor was asked to
// measure: forty Chromium renderers are one answer to "what is using the
// CPU", and the remedy for them is the group's — stop all of them, or stop
// what keeps starting them — not forty separate presses.
type WorkloadGroup struct {
	Key     string `json:"key"`
	Manager string `json:"manager"`
	Name    string `json:"name"`
	Label   string `json:"label,omitempty"`
	Count   int    `json:"count"`

	CPUPercent float64 `json:"cpuPercent"`
	// Memory counts private pages per process and the group's shared pages
	// once, as the process page's groups do.
	Memory  uint64   `json:"memory"`
	Swap    uint64   `json:"swap"`
	IORate  uint64   `json:"ioRate"`
	Handles int64    `json:"handles"`
	Users   []string `json:"users"`

	// PID and Cmdline are the group's heaviest member by the requested
	// measure, the one an Inspect opens.
	PID     int32  `json:"pid"`
	Cmdline string `json:"cmdline"`
	// Members carry their start times so a control acting on the group can
	// refuse a PID that has since been reused.
	Members   []WorkloadMember `json:"members"`
	Truncated bool             `json:"truncated,omitempty"`
	// Launcher is what started a hand-run group, where one process outside
	// it did: the test runner behind a crowd of browsers. Stopping the group
	// alone lets the launcher start it again.
	Launcher *WorkloadLauncher `json:"launcher,omitempty"`
}

type WorkloadMember struct {
	PID        int32     `json:"pid"`
	CreateTime time.Time `json:"createTime"`
}

type WorkloadLauncher struct {
	PID        int32     `json:"pid"`
	Name       string    `json:"name"`
	Cmdline    string    `json:"cmdline"`
	CreateTime time.Time `json:"createTime"`
	Username   string    `json:"username"`
}

const (
	workloadGroupsShown = 8
	// A group's member list is bounded so one runaway fork bomb does not make
	// the report megabytes long; the count stays exact.
	workloadMembersShown = 256
	// How far above a group's topmost processes a launcher may be. A common
	// ancestor further up is a terminal or an agent that started everything,
	// which is not "what started these".
	launcherDepth = 3
)

// notLaunchers are programs that start everything a person runs, so naming
// one as a launcher would offer to stop a shell, a terminal multiplexer or
// the service manager. The prefixes cover names that carry a version or a
// suffix: tmux's "tmux: server", containerd-shim-runc-v2, PM2's God Daemon.
var (
	notLaunchers = map[string]bool{
		"bash": true, "sh": true, "zsh": true, "fish": true, "dash": true, "ksh": true,
		"screen": true, "sshd": true, "login": true, "su": true, "sudo": true,
		"systemd": true, "init": true, "runc": true, "tini": true, "dumb-init": true,
	}
	notLauncherPrefixes = []string{"tmux", "containerd-shim", "docker", "pm2", "(sd-pam"}
)

// workloadGroups groups every process by workload and returns the heaviest
// by the requested measure.
func workloadGroups(rows []Process, order string) []WorkloadGroup {
	type acc struct {
		group   WorkloadGroup
		private uint64
		shared  uint64
		members []Process
		users   map[string]bool
	}
	byKey := map[string]*acc{}
	keys := []string{}
	for _, p := range rows {
		key := GroupKey(p)
		a := byKey[key]
		if a == nil {
			name := program(p.Name)
			switch {
			case p.Manager == "kernel":
				name = "Kernel threads"
			case strings.HasPrefix(key, p.Manager+":"):
				name = p.ManagerName
			}
			a = &acc{group: WorkloadGroup{Key: key, Manager: p.Manager, Name: name}, users: map[string]bool{}}
			byKey[key] = a
			keys = append(keys, key)
		}
		g := &a.group
		g.Count++
		g.CPUPercent += p.CPUPercent
		g.Swap += p.Swap
		g.IORate += p.IOReadRate + p.IOWriteRate
		if order == "handles" {
			g.Handles += int64(p.FDs)
		}
		if g.Label == "" {
			g.Label = p.ManagerLabel
		}
		if p.Username != "" {
			a.users[p.Username] = true
		}
		shared := min(p.Shared, p.RSS)
		a.private += p.RSS - shared
		a.shared = max(a.shared, shared)
		a.members = append(a.members, p)
	}

	byPID := make(map[int32]Process, len(rows))
	for _, p := range rows {
		byPID[p.PID] = p
	}
	self := int32(os.Getpid())
	out := make([]WorkloadGroup, 0, len(keys))
	for _, key := range keys {
		a := byKey[key]
		g := a.group
		g.Memory = a.private + a.shared
		g.CPUPercent = round2(g.CPUPercent)
		for user := range a.users {
			g.Users = append(g.Users, user)
		}
		sort.Strings(g.Users)
		if g.Users == nil {
			g.Users = []string{}
		}
		sortWorkloads(a.members, order)
		g.PID, g.Cmdline = a.members[0].PID, a.members[0].Cmdline
		for i, p := range a.members {
			if i == workloadMembersShown {
				g.Truncated = true
				break
			}
			g.Members = append(g.Members, WorkloadMember{PID: p.PID, CreateTime: p.CreateTime})
		}
		if strings.HasPrefix(key, "name:") {
			g.Launcher = launcherOf(a.members, byPID, self)
		}
		out = append(out, g)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := measureOf(out[i], order), measureOf(out[j], order)
		if a != b {
			return a > b
		}
		if out[i].Memory != out[j].Memory {
			return out[i].Memory > out[j].Memory
		}
		return out[i].Key < out[j].Key
	})
	return out[:min(len(out), workloadGroupsShown)]
}

func measureOf(g WorkloadGroup, order string) float64 {
	switch order {
	case "memory":
		return float64(g.Memory)
	case "swap":
		return float64(g.Swap)
	case "io":
		return float64(g.IORate)
	case "handles":
		return float64(g.Handles)
	default:
		return g.CPUPercent
	}
}

// launcherOf finds the one process outside a hand-run group that started
// all of it: the nearest common ancestor of the group's topmost members,
// within launcherDepth of each. It is nil when there is none, when it is the
// dashboard itself or init, or when it is a shell or a supervisor, whose
// stopping would take far more than this workload with it.
func launcherOf(members []Process, byPID map[int32]Process, self int32) *WorkloadLauncher {
	inGroup := make(map[int32]bool, len(members))
	for _, p := range members {
		inGroup[p.PID] = true
	}
	var chains [][]int32
	for _, p := range members {
		if inGroup[p.PPID] {
			continue
		}
		chain := []int32{}
		for parent := p.PPID; parent > 0 && len(chain) < launcherDepth; {
			chain = append(chain, parent)
			row, ok := byPID[parent]
			if !ok {
				break
			}
			parent = row.PPID
		}
		chains = append(chains, chain)
	}
	if len(chains) == 0 {
		return nil
	}
	for _, candidate := range chains[0] {
		shared := true
		for _, chain := range chains[1:] {
			if !slices.Contains(chain, candidate) {
				shared = false
				break
			}
		}
		if !shared {
			continue
		}
		row, ok := byPID[candidate]
		if !ok || candidate <= 2 || candidate == self || inGroup[candidate] || !launchable(row) {
			return nil
		}
		// Named from its command line where it has one: Node renames its
		// main thread, so a test runner's own name reads "MainThread".
		name := program(row.Name)
		if row.Cmdline != "" {
			name = program(row.Cmdline)
		}
		return &WorkloadLauncher{
			PID: row.PID, Name: name, Cmdline: row.Cmdline,
			CreateTime: row.CreateTime, Username: row.Username,
		}
	}
	return nil
}

func launchable(p Process) bool {
	if p.Manager == "kernel" || p.CreateTime.IsZero() {
		return false
	}
	name := strings.ToLower(program(p.Name))
	if notLaunchers[name] {
		return false
	}
	for _, prefix := range notLauncherPrefixes {
		if strings.HasPrefix(name, prefix) {
			return false
		}
	}
	return true
}
