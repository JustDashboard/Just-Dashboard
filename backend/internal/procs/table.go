package procs

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

// Process is one row of the htop-style table.
type Process struct {
	PID         int32     `json:"pid"`
	PPID        int32     `json:"ppid"`
	Name        string    `json:"name"`
	Cmdline     string    `json:"cmdline"`
	Username    string    `json:"username"`
	Status      string    `json:"status"`
	CPUPercent  float64   `json:"cpuPercent"`
	MemPercent  float64   `json:"memPercent"`
	RSS         uint64    `json:"rss"`
	VMS         uint64    `json:"vms"`
	Threads     int32     `json:"threads"`
	Nice        int32     `json:"nice"`
	CreateTime  time.Time `json:"createTime"`
	CWD         string    `json:"cwd,omitempty"`
	Exe         string    `json:"exe,omitempty"`
	IORead      uint64    `json:"ioReadBytes,omitempty"`
	IOWrite     uint64    `json:"ioWriteBytes,omitempty"`
	IOReadRate  uint64    `json:"ioReadRate,omitempty"`
	IOWriteRate uint64    `json:"ioWriteRate,omitempty"`
	FDs         int32     `json:"fileDescriptors,omitempty"`
	Children    int       `json:"children,omitempty"`
	State       string    `json:"state"`
	Manager     string    `json:"manager"`
	ManagerName string    `json:"managerName,omitempty"`
	// Detail-only readings. A snapshot leaves them empty: reading every
	// process's sockets and limits on each poll would cost more than the
	// table itself, and the table asks "what is heavy", not "what is on 3000".
	Listening      []ListeningPort `json:"listening,omitempty"`
	Connections    int             `json:"connections,omitempty"`
	OpenFilesLimit uint64          `json:"openFilesLimit,omitempty"`
	ioRateReady    bool
}

type ioSample struct {
	read, write uint64
	created     int64
	at          time.Time
}

type Table struct {
	mu      sync.Mutex
	samples map[int32]ioSample
	now     func() time.Time
}

func NewTable() *Table {
	return &Table{samples: map[int32]ioSample{}, now: time.Now}
}

// Snapshot walks the process table. Errors on individual processes are ignored:
// a short-lived process disappearing mid-scan is normal, not a failure of the
// whole listing.
func (t *Table) Snapshot(ctx context.Context) ([]Process, error) {
	procs, err := process.ProcessesWithContext(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Process, 0, len(procs))
	for _, p := range procs {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		row := Process{PID: p.Pid}
		row.Name, _ = p.NameWithContext(ctx)
		if row.Name == "" {
			continue
		}
		row.PPID, _ = p.PpidWithContext(ctx)
		row.Username, _ = p.UsernameWithContext(ctx)
		if cmd, err := p.CmdlineWithContext(ctx); err == nil {
			row.Cmdline = cmd
		}
		if st, err := p.StatusWithContext(ctx); err == nil && len(st) > 0 {
			row.Status = strings.Join(st, ",")
		}
		row.CPUPercent, _ = p.CPUPercentWithContext(ctx)
		row.CPUPercent = round2(row.CPUPercent)
		if mp, err := p.MemoryPercentWithContext(ctx); err == nil {
			row.MemPercent = round2(float64(mp))
		}
		if mi, err := p.MemoryInfoWithContext(ctx); err == nil && mi != nil {
			row.RSS, row.VMS = mi.RSS, mi.VMS
		}
		if io, err := p.IOCountersWithContext(ctx); err == nil && io != nil {
			row.IORead, row.IOWrite = io.ReadBytes, io.WriteBytes
		}
		row.Threads, _ = p.NumThreadsWithContext(ctx)
		row.Nice, _ = p.NiceWithContext(ctx)
		if ct, err := p.CreateTimeWithContext(ctx); err == nil {
			row.CreateTime = time.UnixMilli(ct).UTC()
		}
		row.State = processState(row.Status)
		row.Manager, row.ManagerName = processManager(row.PID, row.Cmdline)
		out = append(out, row)
	}
	t.applyIORates(out)
	return out, nil
}

// applyIORates turns the cumulative counters exposed by /proc into the rate an
// operator can act on. A PID is not an identity: Linux reuses it, so the start
// time participates in the key and a replacement begins at zero instead of
// inheriting the old process's last counter.
func (t *Table) applyIORates(rows []Process) {
	now := t.now()
	t.mu.Lock()
	defer t.mu.Unlock()
	next := make(map[int32]ioSample, len(rows))
	for i := range rows {
		row := &rows[i]
		created := row.CreateTime.UnixMilli()
		current := ioSample{read: row.IORead, write: row.IOWrite, created: created, at: now}
		if previous, ok := t.samples[row.PID]; ok && previous.created == created {
			elapsed := now.Sub(previous.at)
			row.IOReadRate = counterRate(previous.read, current.read, elapsed)
			row.IOWriteRate = counterRate(previous.write, current.write, elapsed)
			row.ioRateReady = true
		}
		next[row.PID] = current
	}
	t.samples = next
}

func counterRate(previous, current uint64, elapsed time.Duration) uint64 {
	if current < previous || elapsed <= 0 {
		return 0
	}
	return uint64(float64(current-previous) / elapsed.Seconds())
}

type ProcessFacet struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

type ListOptions struct {
	Limit   int
	Order   Order
	Query   string
	User    string
	State   string
	Manager string
}

type ProcessList struct {
	Processes  []Process      `json:"processes"`
	Total      int            `json:"total"`
	Available  int            `json:"available"`
	Truncated  bool           `json:"truncated"`
	Users      []ProcessFacet `json:"users"`
	States     []ProcessFacet `json:"states"`
	Managers   []ProcessFacet `json:"managers"`
	RatesReady bool           `json:"ratesReady"`
}

// Select applies every filter before the cap. The old handler cut to the 200
// heaviest rows first and searched that slice, so its promise that filtering
// could reach the rest of the process table was not true.
func Select(rows []Process, opts ListOptions) ProcessList {
	result := ProcessList{
		Available: len(rows),
		Users:     facets(rows, func(p Process) (string, string) { return p.Username, p.Username }),
		States:    facets(rows, func(p Process) (string, string) { return p.State, stateLabel(p.State) }),
		Managers: facets(rows, func(p Process) (string, string) {
			return p.Manager, managerLabel(p.Manager)
		}),
	}
	for _, p := range rows {
		if p.ioRateReady {
			result.RatesReady = true
			break
		}
	}
	needle := strings.ToLower(strings.TrimSpace(opts.Query))
	for _, p := range rows {
		if opts.User != "" && p.Username != opts.User {
			continue
		}
		if opts.State != "" && p.State != opts.State {
			continue
		}
		if opts.Manager != "" && p.Manager != opts.Manager {
			continue
		}
		if needle != "" && !processMatches(p, needle) {
			continue
		}
		result.Processes = append(result.Processes, p)
	}
	result.Total = len(result.Processes)
	SortBy(result.Processes, opts.Order)
	if opts.Limit > 0 && len(result.Processes) > opts.Limit {
		result.Processes = result.Processes[:opts.Limit]
		result.Truncated = true
	}
	if result.Processes == nil {
		result.Processes = []Process{}
	}
	return result
}

func processMatches(p Process, needle string) bool {
	return strings.Contains(strings.ToLower(p.Name), needle) ||
		strings.Contains(strings.ToLower(p.Cmdline), needle) ||
		strings.Contains(strings.ToLower(p.Username), needle) ||
		strings.Contains(strings.ToLower(p.ManagerName), needle) ||
		strings.Contains(strconv.Itoa(int(p.PID)), needle)
}

func facets(rows []Process, value func(Process) (string, string)) []ProcessFacet {
	type entry struct {
		label string
		count int
	}
	counts := map[string]entry{}
	for _, p := range rows {
		key, label := value(p)
		if key == "" {
			continue
		}
		e := counts[key]
		e.label, e.count = label, e.count+1
		counts[key] = e
	}
	out := make([]ProcessFacet, 0, len(counts))
	for key, e := range counts {
		out = append(out, ProcessFacet{Value: key, Label: e.label, Count: e.count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Label < out[j].Label
	})
	return out
}

// Order is what "heaviest" means for a particular question.
//
// "What is eating my CPU" and "what is eating my RAM" are asked about equally
// often and have completely different answers — a leaking service can sit at
// 0% CPU while holding six gigabytes, and sorting only by CPU puts it below
// two hundred rows of nothing.
type Order string

const (
	ByCPU    Order = "cpu"
	ByMemory Order = "memory"
	ByIO     Order = "io"
	ByUptime Order = "uptime"
)

// ParseOrder reads the sort parameter, defaulting to CPU rather than
// rejecting an unknown value: this decides which rows a table shows, and a
// 400 is a worse answer than the usual one.
func ParseOrder(s string) Order {
	switch Order(s) {
	case ByMemory, ByIO, ByUptime:
		return Order(s)
	}
	return ByCPU
}

// SortBy orders the table, using the other measure as the tie-break so a run
// of processes all reporting 0% CPU still comes back in a stable, meaningful
// order rather than in whatever order /proc was read.
func SortBy(rows []Process, order Order) {
	sort.Slice(rows, func(i, j int) bool {
		switch order {
		case ByMemory:
			if rows[i].RSS != rows[j].RSS {
				return rows[i].RSS > rows[j].RSS
			}
			return rows[i].CPUPercent > rows[j].CPUPercent
		case ByIO:
			iRate := rows[i].IOReadRate + rows[i].IOWriteRate
			jRate := rows[j].IOReadRate + rows[j].IOWriteRate
			if iRate != jRate {
				return iRate > jRate
			}
			return rows[i].CPUPercent > rows[j].CPUPercent
		case ByUptime:
			if !rows[i].CreateTime.Equal(rows[j].CreateTime) {
				return rows[i].CreateTime.Before(rows[j].CreateTime)
			}
			return rows[i].PID < rows[j].PID
		}
		if rows[i].CPUPercent != rows[j].CPUPercent {
			return rows[i].CPUPercent > rows[j].CPUPercent
		}
		return rows[i].RSS > rows[j].RSS
	})
}

func (t *Table) Detail(ctx context.Context, pid int32) (*Process, error) {
	p, err := process.NewProcessWithContext(ctx, pid)
	if err != nil {
		return nil, err
	}
	row := &Process{PID: pid}
	row.Name, _ = p.NameWithContext(ctx)
	row.PPID, _ = p.PpidWithContext(ctx)
	row.Username, _ = p.UsernameWithContext(ctx)
	row.Cmdline, _ = p.CmdlineWithContext(ctx)
	row.Exe, _ = p.ExeWithContext(ctx)
	row.CWD, _ = p.CwdWithContext(ctx)
	if st, err := p.StatusWithContext(ctx); err == nil {
		row.Status = strings.Join(st, ",")
	}
	row.CPUPercent, _ = p.CPUPercentWithContext(ctx)
	if mp, err := p.MemoryPercentWithContext(ctx); err == nil {
		row.MemPercent = round2(float64(mp))
	}
	if mi, err := p.MemoryInfoWithContext(ctx); err == nil && mi != nil {
		row.RSS, row.VMS = mi.RSS, mi.VMS
	}
	if io, err := p.IOCountersWithContext(ctx); err == nil && io != nil {
		row.IORead, row.IOWrite = io.ReadBytes, io.WriteBytes
	}
	row.FDs, _ = p.NumFDsWithContext(ctx)
	if children, err := p.ChildrenWithContext(ctx); err == nil {
		row.Children = len(children)
	}
	row.Threads, _ = p.NumThreadsWithContext(ctx)
	row.Nice, _ = p.NiceWithContext(ctx)
	if ct, err := p.CreateTimeWithContext(ctx); err == nil {
		row.CreateTime = time.UnixMilli(ct).UTC()
	}
	row.State = processState(row.Status)
	row.Manager, row.ManagerName = processManager(row.PID, row.Cmdline)
	row.Listening, row.Connections = sockets(ctx, p)
	row.OpenFilesLimit = openFilesLimit(ctx, p)
	return row, nil
}

func processState(status string) string {
	s := strings.ToLower(status)
	switch {
	case strings.Contains(s, "zombie"):
		return "zombie"
	case strings.Contains(s, "disk-sleep"), strings.Contains(s, "blocked"):
		return "blocked"
	case strings.Contains(s, "stopped"), strings.Contains(s, "tracing-stop"):
		return "stopped"
	case strings.Contains(s, "running"), strings.Contains(s, "waking"):
		return "running"
	case strings.Contains(s, "sleep"), strings.Contains(s, "idle"), strings.Contains(s, "parked"):
		return "sleeping"
	default:
		return "other"
	}
}

func stateLabel(state string) string {
	switch state {
	case "zombie":
		return "Zombie"
	case "blocked":
		return "Blocked"
	case "stopped":
		return "Stopped"
	case "running":
		return "Running"
	case "sleeping":
		return "Sleeping"
	default:
		return "Other"
	}
}

func managerLabel(manager string) string {
	switch manager {
	case "pm2":
		return "PM2"
	case "systemd":
		return "systemd"
	case "container":
		return "Container"
	case "session":
		return "Login session"
	case "kernel":
		return "Kernel"
	default:
		return "Unmanaged"
	}
}

// processManager reads the cgroup membership the kernel has already assigned.
// That is more reliable than guessing from executable names: nginx started by
// systemd and nginx started in a shell are the same binary but not the same
// thing to restart. PM2 is overlaid by the API from PM2's own PID list.
func processManager(pid int32, cmdline string) (string, string) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", pid))
	if err == nil {
		if manager, name := managerFromCgroup(string(b)); manager != "" {
			return manager, name
		}
	}
	if cmdline == "" {
		return "kernel", "kernel"
	}
	return "unmanaged", ""
}

func managerFromCgroup(content string) (string, string) {
	var service, session string
	for _, line := range strings.Split(content, "\n") {
		_, path, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		_, path, ok = strings.Cut(path, ":")
		if !ok {
			continue
		}
		for _, component := range strings.Split(path, "/") {
			if id := containerID(component); id != "" {
				if len(id) > 12 {
					id = id[:12]
				}
				return "container", id
			}
			if strings.HasSuffix(component, ".service") {
				service = component
			}
			if strings.HasPrefix(component, "session-") && strings.HasSuffix(component, ".scope") {
				session = component
			}
		}
	}
	if service != "" {
		return "systemd", service
	}
	if session != "" {
		return "session", session
	}
	return "", ""
}

func containerID(component string) string {
	value := strings.TrimSuffix(component, ".scope")
	for _, prefix := range []string{"docker-", "cri-containerd-", "crio-", "libpod-"} {
		value = strings.TrimPrefix(value, prefix)
	}
	if len(value) < 32 {
		return ""
	}
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return ""
		}
	}
	return value
}

func MarkPM2(rows []Process, processes []PM2Process) {
	byPID := make(map[int32]string, len(processes))
	for _, p := range processes {
		if p.PID > 0 {
			byPID[int32(p.PID)] = p.Name
		}
	}
	for i := range rows {
		if name, ok := byPID[rows[i].PID]; ok {
			rows[i].Manager, rows[i].ManagerName = "pm2", name
		}
	}
}

func (t *Table) SetNice(ctx context.Context, pid int32, nice int) error {
	if nice < -20 || nice > 19 {
		return fmt.Errorf("nice value must be between -20 and 19")
	}
	if pid <= 1 || pid == int32(os.Getpid()) {
		return fmt.Errorf("refusing to reprioritise pid %d", pid)
	}
	if _, err := process.NewProcessWithContext(ctx, pid); err != nil {
		return fmt.Errorf("no process with pid %d", pid)
	}
	if err := syscall.Setpriority(syscall.PRIO_PROCESS, int(pid), nice); err != nil {
		return fmt.Errorf("set priority for pid %d: %w", pid, err)
	}
	return nil
}

var allowedSignals = map[string]syscall.Signal{
	"SIGTERM": syscall.SIGTERM,
	"SIGKILL": syscall.SIGKILL,
	"SIGINT":  syscall.SIGINT,
	"SIGHUP":  syscall.SIGHUP,
	"SIGUSR1": syscall.SIGUSR1,
	"SIGUSR2": syscall.SIGUSR2,
	"SIGSTOP": syscall.SIGSTOP,
	"SIGCONT": syscall.SIGCONT,
}

// Signal delivers a signal to one process. PID 1 is refused outright: on a
// normal host that is init, and on a containerised deployment it is the
// dashboard's own supervisor — either way, signalling it takes the machine
// down and is never what the operator meant to click.
func (t *Table) Signal(ctx context.Context, pid int32, sig string) error {
	if pid <= 1 {
		return fmt.Errorf("refusing to signal pid %d", pid)
	}
	if pid == int32(os.Getpid()) {
		return fmt.Errorf("refusing to signal the dashboard's own process")
	}
	signal, ok := allowedSignals[strings.ToUpper(sig)]
	if !ok {
		return fmt.Errorf("signal %q is not permitted", sig)
	}
	p, err := process.NewProcessWithContext(ctx, pid)
	if err != nil {
		return fmt.Errorf("no process with pid %d", pid)
	}
	return p.SendSignalWithContext(ctx, signal)
}

func round2(f float64) float64 { return float64(int64(f*100+0.5)) / 100 }
