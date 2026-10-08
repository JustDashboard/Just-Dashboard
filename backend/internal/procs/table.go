package procs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/process"
)

// Process is one row of the htop-style table.
type Process struct {
	PID         int32   `json:"pid"`
	PPID        int32   `json:"ppid"`
	Name        string  `json:"name"`
	Cmdline     string  `json:"cmdline"`
	Username    string  `json:"username"`
	Status      string  `json:"status"`
	CPUPercent  float64 `json:"cpuPercent"`
	CPUReady    bool    `json:"cpuReady"`
	CPUWindow   float64 `json:"cpuWindowSeconds"`
	MemPercent  float64 `json:"memPercent"`
	RSS         uint64  `json:"rss"`
	VMS         uint64  `json:"vms"`
	Swap        uint64  `json:"swap"`
	MemoryReady bool    `json:"memoryReady"`
	// Shared is the resident memory backed by files and shared mappings —
	// the part a group of processes holds once between them, not once each.
	Shared      uint64    `json:"shared,omitempty"`
	Threads     int32     `json:"threads"`
	Nice        int32     `json:"nice"`
	CreateTime  time.Time `json:"createTime"`
	CWD         string    `json:"cwd,omitempty"`
	Exe         string    `json:"exe,omitempty"`
	IORead      uint64    `json:"ioReadBytes,omitempty"`
	IOWrite     uint64    `json:"ioWriteBytes,omitempty"`
	IOReadRate  uint64    `json:"ioReadRate,omitempty"`
	IOWriteRate uint64    `json:"ioWriteRate,omitempty"`
	IOReady     bool      `json:"ioReady"`
	FDs         int32     `json:"fileDescriptors,omitempty"`
	FDReady     bool      `json:"fdReady"`
	Children    int       `json:"children,omitempty"`
	State       string    `json:"state"`
	Manager     string    `json:"manager"`
	ManagerName string    `json:"managerName,omitempty"`
	// ManagerLabel is the supervisor as its owner names it where the
	// cgroup does not: a container's name for the id its cgroup carries.
	ManagerLabel string `json:"managerLabel,omitempty"`
	// Detail-only readings. A snapshot leaves them empty: reading every
	// process's sockets and limits on each poll would cost more than the
	// table itself, and the table asks "what is heavy", not "what is on 3000".
	Listening      []ListeningPort `json:"listening,omitempty"`
	Connections    int             `json:"connections,omitempty"`
	OpenFilesLimit uint64          `json:"openFilesLimit,omitempty"`
	// History is the process's recent readings, oldest first, from every
	// read the sampler has taken of it — the table's polls and an open
	// sheet's — so a sheet opens on a shape rather than an empty line.
	History         []ProcessSample `json:"history,omitempty"`
	ioRateReady     bool
	cpuSeconds      float64
	cpuCounterReady bool
	ioCounterReady  bool
}

// ProcessSample is one measured interval of a process: its CPU over the
// interval, its resident memory at the end of it, and its disk rates.
type ProcessSample struct {
	At    time.Time `json:"at"`
	CPU   float64   `json:"cpu"`
	RSS   uint64    `json:"rss"`
	Read  uint64    `json:"read"`
	Write uint64    `json:"write"`
}

const (
	// Two reads closer together than this measure the scheduler's rounding
	// rather than the process. The table and an open sheet poll on their own
	// clocks, and a sheet read landing 50ms after a table scan drew a worker
	// at 0% and then at 180% on alternate polls.
	minRateWindow = time.Second
	// The history keeps a point at most this often, and this many of them:
	// a few minutes at the table's cadence, the span a sheet's trend draws.
	historyStep = 2 * time.Second
	historySize = 90
)

type ioSample struct {
	read, write uint64
	created     int64
	at          time.Time
	cpu         float64
	cpuReady    bool
	ioReady     bool
	// The rates last measured over a full window, handed back to a read that
	// lands too soon after the previous one to measure its own.
	rates   measured
	history []ProcessSample
}

type measured struct {
	cpu         float64
	window      float64
	read, write uint64
	cpuOK, ioOK bool
}

func (m measured) apply(row *Process) {
	if m.ioOK {
		row.IOReadRate, row.IOWriteRate = m.read, m.write
		row.ioRateReady, row.IOReady = true, true
	}
	if m.cpuOK {
		row.CPUPercent, row.CPUReady, row.CPUWindow = m.cpu, true, m.window
	}
}

type Table struct {
	mu      sync.Mutex
	samples map[int32]ioSample
	now     func() time.Time
	scanMu  sync.Mutex
	scan    *processScan
}

type processScan struct {
	done    chan struct{}
	cancel  context.CancelFunc
	readers int
	rows    []Process
	err     error
}

func NewTable() *Table {
	return &Table{samples: map[int32]ioSample{}, now: time.Now}
}

// Snapshot walks the process table. Errors on individual processes are ignored:
// a short-lived process disappearing mid-scan is normal, not a failure of the
// whole listing.
func (t *Table) Snapshot(ctx context.Context) ([]Process, error) {
	return t.sharedSnapshot(ctx, t.snapshot)
}

func (t *Table) sharedSnapshot(ctx context.Context, read func(context.Context) ([]Process, error)) ([]Process, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	t.scanMu.Lock()
	scan := t.scan
	if scan == nil {
		owner, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		scan = &processScan{done: make(chan struct{}), cancel: cancel}
		t.scan = scan
		go func() {
			defer cancel()
			scan.rows, scan.err = read(owner)
			t.scanMu.Lock()
			if t.scan == scan {
				t.scan = nil
			}
			close(scan.done)
			t.scanMu.Unlock()
		}()
	}
	scan.readers++
	t.scanMu.Unlock()
	defer func() {
		t.scanMu.Lock()
		defer t.scanMu.Unlock()
		scan.readers--
		if scan.readers == 0 {
			scan.cancel()
			if t.scan == scan {
				t.scan = nil
			}
		}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-scan.done:
		// Handlers overlay PM2 ownership and sort in place. Each gets its own
		// rows while simultaneous readers share only the expensive OS scan.
		return slices.Clone(scan.rows), scan.err
	}
}

func (t *Table) snapshot(ctx context.Context) ([]Process, error) {
	procs, err := process.ProcessesWithContext(ctx)
	if err != nil {
		return nil, err
	}
	// Read once per scan. gopsutil's MemoryPercent reads /proc/meminfo again
	// for every process it is asked about, which on a host of five hundred
	// processes was five hundred reads of the same file per poll.
	total := hostMemoryTotal(ctx)
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
		if cpu, err := p.TimesWithContext(ctx); err == nil && cpu != nil {
			row.cpuSeconds = cpu.User + cpu.System
			row.cpuCounterReady = true
		}
		readMemory(ctx, p, &row, total)
		if io, err := p.IOCountersWithContext(ctx); err == nil && io != nil {
			row.IORead, row.IOWrite = io.ReadBytes, io.WriteBytes
			row.ioCounterReady = true
		}
		row.Threads, _ = p.NumThreadsWithContext(ctx)
		row.Nice = niceOf(ctx, p)
		if ct, err := p.CreateTimeWithContext(ctx); err == nil {
			row.CreateTime = time.UnixMilli(ct).UTC()
		}
		row.State = processState(row.Status)
		row.Manager, row.ManagerName = processManager(row.PID, row.Cmdline)
		out = append(out, row)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
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
		next[row.PID] = applyProcessRates(row, t.samples[row.PID], now)
	}
	t.samples = next
}

func applyProcessRates(row *Process, previous ioSample, now time.Time) ioSample {
	created := row.CreateTime.UnixMilli()
	current := ioSample{read: row.IORead, write: row.IOWrite, created: created, at: now, cpu: row.cpuSeconds, cpuReady: row.cpuCounterReady, ioReady: row.ioCounterReady}
	if previous.created != created || row.CreateTime.IsZero() || previous.at.IsZero() {
		return current
	}
	elapsed := now.Sub(previous.at)
	if elapsed < minRateWindow {
		// Too soon to measure: say what the last full window measured and
		// keep its start, so the next read measures a whole one.
		previous.rates.apply(row)
		return previous
	}
	var m measured
	if previous.ioReady && current.ioReady {
		m.read = counterRate(previous.read, current.read, elapsed)
		m.write = counterRate(previous.write, current.write, elapsed)
		m.ioOK = true
	}
	if previous.cpuReady && current.cpuReady && current.cpu >= previous.cpu {
		m.cpu = round2((current.cpu - previous.cpu) / elapsed.Seconds() * 100)
		m.window = elapsed.Seconds()
		m.cpuOK = true
	}
	m.apply(row)
	current.rates = m
	current.history = previous.history
	if m.cpuOK && (len(current.history) == 0 || now.Sub(current.history[len(current.history)-1].At) >= historyStep) {
		current.history = append(current.history, ProcessSample{At: now.UTC(), CPU: m.cpu, RSS: row.RSS, Read: m.read, Write: m.write})
		if len(current.history) > historySize {
			current.history = current.history[len(current.history)-historySize:]
		}
	}
	return current
}

// lastCPU is the CPU the sampler last measured for a process, for a view
// that lists processes without scanning them itself. A gopsutil handle's own
// CPUPercent is the average over the process's whole life, which put a
// worker that had been idle for a day at 0% while it spun.
func (t *Table) lastCPU(pid int32, created time.Time) (float64, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	sample, ok := t.samples[pid]
	if !ok || sample.created != created.UnixMilli() || !sample.rates.cpuOK {
		return 0, false
	}
	return sample.rates.cpu, true
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
	// Group is a ProcessGroup key: the processes of one workload.
	Group string
}

// ProcessGroup is one workload: the processes one supervisor runs, or the
// copies of one program started by hand. Fifty Chrome renderers or twenty
// Postgres backends are one answer to "what is using the machine", and the
// remedy for any of them is the group's, not a single PID's.
type ProcessGroup struct {
	Key     string `json:"key"`
	Manager string `json:"manager"`
	Name    string `json:"name"`
	// Label is the supervisor's own name where it has one the key does not
	// carry: a container's name beside its id.
	Label      string  `json:"label,omitempty"`
	Count      int     `json:"count"`
	CPUPercent float64 `json:"cpuPercent"`
	// Memory counts each process's private pages and the group's shared ones
	// once: summed RSS counted Postgres's shared buffers once per backend and
	// drew a database holding more memory than the machine has.
	Memory uint64 `json:"memory"`
	IORate uint64 `json:"ioRate"`
	// PID is the group's heaviest process, the one a press opens.
	PID int32 `json:"pid"`
}

type ProcessList struct {
	Processes []Process      `json:"processes"`
	Total     int            `json:"total"`
	Available int            `json:"available"`
	Truncated bool           `json:"truncated"`
	Users     []ProcessFacet `json:"users"`
	States    []ProcessFacet `json:"states"`
	Managers  []ProcessFacet `json:"managers"`
	// Groups are the workloads heaviest by CPU or by memory, over the whole
	// snapshot like the facets, so narrowing the table never narrows them.
	Groups     []ProcessGroup `json:"groups"`
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
		Groups: groups(rows, groupsShown),
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
		if opts.Group != "" && GroupKey(p) != opts.Group {
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
		strings.Contains(strings.ToLower(p.ManagerLabel), needle) ||
		strings.Contains(strconv.Itoa(int(p.PID)), needle)
}

// GroupKey names the workload a process belongs to. A supervisor's processes
// are its own group; anything started by hand — a login session's or nobody's
// — is grouped by program, because "chrome ×40" is the reading and the
// session scope it happened to start in is not.
func GroupKey(p Process) string {
	switch p.Manager {
	case "systemd", "pm2", "container":
		if p.ManagerName != "" {
			return p.Manager + ":" + p.ManagerName
		}
	case "kernel":
		return "kernel:"
	}
	return "name:" + program(p.Name)
}

// program is the word a process's name starts with. A name is usually the
// kernel's fifteen-character comm, but gopsutil reads a longer one from the
// command line, and Chrome and Node rewrite theirs to the whole argv: forty
// renderers were forty groups of one, each named by its flags.
func program(name string) string {
	fields := strings.Fields(name)
	if len(fields) == 0 {
		return name
	}
	return strings.TrimRight(filepath.Base(fields[0]), ":")
}

// groupsShown is how many workloads each measure contributes: the heaviest
// by CPU and the heaviest by memory, which are seldom the same list.
const groupsShown = 8

func groups(rows []Process, each int) []ProcessGroup {
	type acc struct {
		group     ProcessGroup
		private   uint64
		shared    uint64
		heaviest  Process
		hasLeader bool
	}
	byKey := map[string]*acc{}
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
			a = &acc{group: ProcessGroup{Key: key, Manager: p.Manager, Name: name}}
			byKey[key] = a
		}
		g := &a.group
		g.Count++
		g.CPUPercent += p.CPUPercent
		g.IORate += p.IOReadRate + p.IOWriteRate
		if g.Label == "" {
			g.Label = p.ManagerLabel
		}
		shared := min(p.Shared, p.RSS)
		a.private += p.RSS - shared
		a.shared = max(a.shared, shared)
		if !a.hasLeader || p.CPUPercent > a.heaviest.CPUPercent ||
			(p.CPUPercent == a.heaviest.CPUPercent && p.RSS > a.heaviest.RSS) {
			a.heaviest, a.hasLeader = p, true
		}
	}
	all := make([]ProcessGroup, 0, len(byKey))
	for _, a := range byKey {
		a.group.Memory = a.private + a.shared
		a.group.CPUPercent = round2(a.group.CPUPercent)
		a.group.PID = a.heaviest.PID
		all = append(all, a.group)
	}
	byMemory := slices.Clone(all)
	sort.Slice(byMemory, func(i, j int) bool {
		if byMemory[i].Memory != byMemory[j].Memory {
			return byMemory[i].Memory > byMemory[j].Memory
		}
		return byMemory[i].Key < byMemory[j].Key
	})
	sort.Slice(all, func(i, j int) bool {
		if all[i].CPUPercent != all[j].CPUPercent {
			return all[i].CPUPercent > all[j].CPUPercent
		}
		if all[i].Memory != all[j].Memory {
			return all[i].Memory > all[j].Memory
		}
		return all[i].Key < all[j].Key
	})
	out := make([]ProcessGroup, 0, 2*each)
	seen := map[string]bool{}
	for _, list := range [][]ProcessGroup{all[:min(each, len(all))], byMemory[:min(each, len(byMemory))]} {
		for _, g := range list {
			if !seen[g.Key] {
				seen[g.Key] = true
				out = append(out, g)
			}
		}
	}
	return out
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
	if cpu, err := p.TimesWithContext(ctx); err == nil && cpu != nil {
		row.cpuSeconds = cpu.User + cpu.System
		row.cpuCounterReady = true
	}
	readMemory(ctx, p, row, hostMemoryTotal(ctx))
	if io, err := p.IOCountersWithContext(ctx); err == nil && io != nil {
		row.IORead, row.IOWrite = io.ReadBytes, io.WriteBytes
		row.ioCounterReady = true
	}
	if fds, err := p.NumFDsWithContext(ctx); err == nil {
		row.FDs, row.FDReady = fds, true
	}
	if children, err := p.ChildrenWithContext(ctx); err == nil {
		row.Children = len(children)
	}
	row.Threads, _ = p.NumThreadsWithContext(ctx)
	row.Nice = niceOf(ctx, p)
	if ct, err := p.CreateTimeWithContext(ctx); err == nil {
		row.CreateTime = time.UnixMilli(ct).UTC()
	}
	t.mu.Lock()
	sample := applyProcessRates(row, t.samples[row.PID], t.now())
	t.samples[row.PID] = sample
	row.History = slices.Clone(sample.history)
	t.mu.Unlock()
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

func processManager(pid int32, cmdline string) (string, string) {
	return ManagerOf("/proc", pid, cmdline)
}

// ManagerOf reads the cgroup membership the kernel has already assigned,
// under the process table at root. That is more reliable than guessing from
// executable names: nginx started by systemd and nginx started in a shell are
// the same binary but not the same thing to restart. PM2 is overlaid by the
// API from PM2's own PID list.
//
// Exported for the database page, which follows a listening port to the
// unit whose journal and whose log files are that server's, and for the
// ports page, which reads it under the host's process table.
func ManagerOf(root string, pid int32, cmdline string) (string, string) {
	b, err := os.ReadFile(filepath.Join(root, strconv.Itoa(int(pid)), "cgroup"))
	if err == nil {
		if manager, name := managerFromCgroup(string(b)); manager != "" {
			return manager, name
		}
	}
	if cmdline == "" {
		// An empty command line is a kernel thread's, and also a zombie's
		// and a process's whose memory is gone mid-exit. The task flags say
		// which; a table whose stat cannot be read keeps the old reading.
		if kernel, ok := kernelThread(root, pid); !ok || kernel {
			return "kernel", "kernel"
		}
	}
	return "unmanaged", ""
}

// pfKthread is PF_KTHREAD among a task's flags (include/linux/sched.h).
const pfKthread = 0x00200000

// kernelThread reads the ninth field of /proc/<pid>/stat, the task's flags.
// The name before it is parenthesised and may itself hold spaces and
// parentheses, so the fields are counted from the last closing one.
func kernelThread(root string, pid int32) (kernel, ok bool) {
	b, err := os.ReadFile(filepath.Join(root, strconv.Itoa(int(pid)), "stat"))
	if err != nil {
		return false, false
	}
	end := strings.LastIndexByte(string(b), ')')
	if end < 0 {
		return false, false
	}
	fields := strings.Fields(string(b[end+1:]))
	if len(fields) < 7 {
		return false, false
	}
	flags, err := strconv.ParseUint(fields[6], 10, 64)
	if err != nil {
		return false, false
	}
	return flags&pfKthread != 0, true
}

// readMemory fills a row's memory from statm, one read for what gopsutil's
// MemoryInfo and MemoryPercent read three times, and its swap from status:
// gopsutil's MemoryInfo leaves Swap at zero on Linux, so every process read
// as swapping nothing and the advisor's swap ranking ranked nothing.
func readMemory(ctx context.Context, p *process.Process, row *Process, total uint64) {
	mi, err := p.MemoryInfoExWithContext(ctx)
	if err != nil || mi == nil {
		return
	}
	row.RSS, row.VMS, row.Shared, row.MemoryReady = mi.RSS, mi.VMS, mi.Shared, true
	if total > 0 {
		row.MemPercent = round2(float64(mi.RSS) / float64(total) * 100)
	}
	row.Swap = statusSwap(p.Pid)
}

func statusSwap(pid int32) uint64 {
	b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(int(pid)), "status"))
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		value, ok := strings.CutPrefix(line, "VmSwap:")
		if !ok {
			continue
		}
		fields := strings.Fields(value)
		if len(fields) == 0 {
			return 0
		}
		kb, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return 0
		}
		return kb * 1024
	}
	return 0
}

func hostMemoryTotal(ctx context.Context) uint64 {
	vm, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return 0
	}
	return vm.Total
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

// niceOf reads a process's nice value on the -20…19 scale SetNice and every
// tool speak. gopsutil hands back getpriority(2)'s raw kernel value, which is
// 20 minus the nice value: read as-is, every ordinary process was "nice 20",
// past the end of the range, and the advisor's Lower priority was disabled
// for all of them.
func niceOf(ctx context.Context, p *process.Process) int32 {
	raw, err := p.NiceWithContext(ctx)
	if err != nil || raw < 1 || raw > 40 {
		return 0
	}
	return 20 - raw
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

var (
	// ErrKernelThread: the kernel does not deliver a signal sent from user
	// space to one of its own threads, so a Kill would report success and
	// change nothing.
	ErrKernelThread = errors.New("a kernel thread does not take signals")
	// ErrZombie: the process has already exited. What remains is its exit
	// status, which only its parent can collect.
	ErrZombie = errors.New("the process has already exited and is waiting for its parent to reap it")
)

// Controllable says whether a signal or a priority could reach the process
// at all, so the route can refuse with the reason rather than report a
// success the kernel quietly ignored.
func Controllable(p *Process) error {
	switch {
	case p.State == "zombie":
		return ErrZombie
	case p.Manager == "kernel":
		return ErrKernelThread
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
