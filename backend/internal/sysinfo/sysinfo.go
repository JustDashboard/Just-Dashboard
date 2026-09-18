// Package sysinfo collects host metrics through gopsutil rather than parsing
// /proc by hand, so the same code path works across kernels and inside a
// container with the host's /proc bind-mounted.
package sysinfo

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
)

type HostInfo struct {
	Hostname        string    `json:"hostname"`
	OS              string    `json:"os"`
	Platform        string    `json:"platform"`
	PlatformVersion string    `json:"platformVersion"`
	KernelVersion   string    `json:"kernelVersion"`
	KernelArch      string    `json:"kernelArch"`
	Virtualization  string    `json:"virtualization"`
	BootTime        time.Time `json:"bootTime"`
	UptimeSeconds   uint64    `json:"uptimeSeconds"`
	Procs           uint64    `json:"processes"`
	CPUModel        string    `json:"cpuModel"`
	CPUCores        int       `json:"cpuCores"`
	CPUMhz          float64   `json:"cpuMhz"`
}

type CPUStats struct {
	TotalPercent float64   `json:"totalPercent"`
	PerCore      []float64 `json:"perCore"`
	LoadAvg1     float64   `json:"loadAvg1"`
	LoadAvg5     float64   `json:"loadAvg5"`
	LoadAvg15    float64   `json:"loadAvg15"`
	Cores        int       `json:"cores"`

	// Where the time actually went. A single "68% busy" figure cannot tell
	// apart the three situations an operator most needs to tell apart: the
	// server is working (user/system), the server is waiting for a disk
	// (iowait), or the hypervisor is running somebody else on this core
	// (steal). On a VPS the last one is the difference between "I need a
	// bigger box" and "I need a different host", and no amount of staring at
	// a total will separate them.
	Modes CPUModes `json:"modes"`
}

// CPUModes is the breakdown of one interval, as percentages of total CPU time
// that sum to roughly 100 across all fields including Idle.
type CPUModes struct {
	User    float64 `json:"user"`
	System  float64 `json:"system"`
	Nice    float64 `json:"nice"`
	IOWait  float64 `json:"iowait"`
	IRQ     float64 `json:"irq"`
	SoftIRQ float64 `json:"softirq"`
	Steal   float64 `json:"steal"`
	Idle    float64 `json:"idle"`
}

type MemoryStats struct {
	Total       uint64  `json:"total"`
	Used        uint64  `json:"used"`
	Free        uint64  `json:"free"`
	Available   uint64  `json:"available"`
	Cached      uint64  `json:"cached"`
	Buffers     uint64  `json:"buffers"`
	UsedPercent float64 `json:"usedPercent"`
}

type SwapStats struct {
	Total       uint64  `json:"total"`
	Used        uint64  `json:"used"`
	Free        uint64  `json:"free"`
	UsedPercent float64 `json:"usedPercent"`
}

type MountStats struct {
	Device      string  `json:"device"`
	Mountpoint  string  `json:"mountpoint"`
	FSType      string  `json:"fstype"`
	Total       uint64  `json:"total"`
	Used        uint64  `json:"used"`
	Free        uint64  `json:"free"`
	UsedPercent float64 `json:"usedPercent"`
	InodesTotal uint64  `json:"inodesTotal"`
	InodesUsed  uint64  `json:"inodesUsed"`
	ReadBytes   uint64  `json:"readBytes"`
	WriteBytes  uint64  `json:"writeBytes"`
	ReadRate    float64 `json:"readRate"`
	WriteRate   float64 `json:"writeRate"`

	// Operations per second and the mean time each took. Bytes per second
	// describes a bulk copy well and a database badly: a disk saturated by
	// small random writes moves very little data while being completely
	// unable to accept another request, and only the operation count and the
	// service time show it.
	ReadOps      float64 `json:"readOps"`
	WriteOps     float64 `json:"writeOps"`
	ReadLatency  float64 `json:"readLatencyMs"`
	WriteLatency float64 `json:"writeLatencyMs"`
	// BusyPercent is the share of the interval the device had at least one
	// request in flight — the classic iostat %util.
	BusyPercent float64 `json:"busyPercent"`
}

type NetStats struct {
	Interface   string   `json:"interface"`
	BytesSent   uint64   `json:"bytesSent"`
	BytesRecv   uint64   `json:"bytesRecv"`
	PacketsSent uint64   `json:"packetsSent"`
	PacketsRecv uint64   `json:"packetsRecv"`
	ErrIn       uint64   `json:"errIn"`
	ErrOut      uint64   `json:"errOut"`
	DropIn      uint64   `json:"dropIn"`
	DropOut     uint64   `json:"dropOut"`
	SendRate    float64  `json:"sendRate"`
	RecvRate    float64  `json:"recvRate"`
	Addrs       []string `json:"addrs"`
	IsUp        bool     `json:"isUp"`
}

// Snapshot is one frame of the live dashboard. Rates are per second and are
// derived from the delta against the previous snapshot, which is why the
// Collector is stateful.
type Snapshot struct {
	TS     time.Time    `json:"ts"`
	CPU    CPUStats     `json:"cpu"`
	Memory MemoryStats  `json:"memory"`
	Swap   SwapStats    `json:"swap"`
	Mounts []MountStats `json:"mounts"`
	Net    []NetStats   `json:"net"`
	Uptime uint64       `json:"uptimeSeconds"`

	// Pressure and Sockets are read straight from the kernel rather than
	// derived from anything above them. They answer questions the
	// utilisation figures structurally cannot: whether the machine is
	// stalling, and whether it is running out of connections.
	Pressure Pressure   `json:"pressure"`
	Sockets  Sockets    `json:"sockets"`
	Procs    ProcCounts `json:"procs"`

	// Files and Sensors are live-only readings: neither survives a
	// downsampled chart, so the recorder does not keep them, and the page
	// reads both from the newest frame.
	Files   FileHandles `json:"files"`
	Sensors []Sensor    `json:"sensors"`
}

// ProcCounts is the run queue, straight from /proc/stat.
//
// Blocked is the field worth having: it counts tasks in uninterruptible sleep,
// which is what a process waiting on a disk looks like. Read next to iowait it
// turns "the CPU is 90% idle but everything is slow" from a mystery into a
// sentence — eleven processes are queued behind a device.
type ProcCounts struct {
	Running int `json:"running"`
	Blocked int `json:"blocked"`
	Total   int `json:"total"`
}

type Collector struct {
	mu       sync.Mutex
	lastNet  map[string]net.IOCountersStat
	lastDisk map[string]disk.IOCountersStat
	lastAt   time.Time
	// lastCPU is the cumulative time-per-mode counter from the previous call.
	// The mode breakdown is a delta like every other rate here, which is why
	// the first Collect after New reports no breakdown at all rather than the
	// since-boot average — an average over three weeks of uptime would say
	// nothing about the last fifteen seconds while looking exactly like it
	// did.
	lastCPU    cpu.TimesStat
	lastCPUSet bool
}

func NewCollector() *Collector {
	return &Collector{
		lastNet:  map[string]net.IOCountersStat{},
		lastDisk: map[string]disk.IOCountersStat{},
	}
}

func (c *Collector) Host(ctx context.Context) (*HostInfo, error) {
	info, err := host.InfoWithContext(ctx)
	if err != nil {
		return nil, err
	}
	h := &HostInfo{
		Hostname:        info.Hostname,
		OS:              info.OS,
		Platform:        info.Platform,
		PlatformVersion: info.PlatformVersion,
		KernelVersion:   info.KernelVersion,
		KernelArch:      info.KernelArch,
		Virtualization:  strings.TrimSpace(info.VirtualizationSystem + " " + info.VirtualizationRole),
		BootTime:        time.Unix(int64(info.BootTime), 0).UTC(),
		UptimeSeconds:   info.Uptime,
		Procs:           info.Procs,
	}
	if cpus, err := cpu.InfoWithContext(ctx); err == nil && len(cpus) > 0 {
		h.CPUModel = cpus[0].ModelName
		h.CPUMhz = cpus[0].Mhz
	}
	if n, err := cpu.CountsWithContext(ctx, true); err == nil {
		h.CPUCores = n
	}
	return h, nil
}

// Collect samples every subsystem. CPU percentages come from the cumulative
// counters with interval 0 — the delta is against the previous call, which
// keeps the push loop non-blocking instead of sleeping for a sample window.
func (c *Collector) Collect(ctx context.Context) (*Snapshot, error) {
	now := time.Now()
	c.mu.Lock()
	elapsed := now.Sub(c.lastAt).Seconds()
	if c.lastAt.IsZero() || elapsed <= 0 {
		elapsed = 0
	}
	c.mu.Unlock()

	snap := &Snapshot{TS: now.UTC()}

	if perCore, err := cpu.PercentWithContext(ctx, 0, true); err == nil {
		snap.CPU.PerCore = round1s(perCore)
		snap.CPU.Cores = len(perCore)
	}
	if total, err := cpu.PercentWithContext(ctx, 0, false); err == nil && len(total) > 0 {
		snap.CPU.TotalPercent = round1(total[0])
	}
	if l, err := load.AvgWithContext(ctx); err == nil {
		snap.CPU.LoadAvg1, snap.CPU.LoadAvg5, snap.CPU.LoadAvg15 = l.Load1, l.Load5, l.Load15
	}
	snap.CPU.Modes = c.cpuModes(ctx)
	if v, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		snap.Memory = MemoryStats{
			Total: v.Total, Used: v.Used, Free: v.Free, Available: v.Available,
			Cached: v.Cached, Buffers: v.Buffers, UsedPercent: round1(v.UsedPercent),
		}
	}
	if sw, err := mem.SwapMemoryWithContext(ctx); err == nil {
		snap.Swap = SwapStats{Total: sw.Total, Used: sw.Used, Free: sw.Free, UsedPercent: round1(sw.UsedPercent)}
	}
	if up, err := host.UptimeWithContext(ctx); err == nil {
		snap.Uptime = up
	}
	snap.Mounts = c.mounts(ctx, elapsed)
	snap.Net = c.network(ctx, elapsed)
	// Two small file reads, done unconditionally: both are a few hundred
	// bytes from a pseudo filesystem, which is cheaper than the branch that
	// would decide whether to bother.
	snap.Pressure = ReadPressure()
	snap.Sockets = ReadSockets()
	snap.Files = ReadFileHandles()
	snap.Sensors = ReadSensors(ctx)
	if misc, err := load.MiscWithContext(ctx); err == nil {
		snap.Procs = ProcCounts{
			Running: misc.ProcsRunning,
			Blocked: misc.ProcsBlocked,
			Total:   misc.ProcsTotal,
		}
	}

	c.mu.Lock()
	c.lastAt = now
	c.mu.Unlock()
	return snap, nil
}

// cpuModes turns the cumulative per-mode counters into the share of the
// interval each mode took.
//
// Percentages of the delta rather than of wall time: the counters are summed
// across every core, so dividing by their own total is what makes the result
// read the same on a 2-core box and a 64-core one, and is what makes the
// fields sum to 100.
func (c *Collector) cpuModes(ctx context.Context) CPUModes {
	times, err := cpu.TimesWithContext(ctx, false)
	if err != nil || len(times) == 0 {
		return CPUModes{}
	}
	cur := times[0]

	c.mu.Lock()
	prev, had := c.lastCPU, c.lastCPUSet
	c.lastCPU, c.lastCPUSet = cur, true
	c.mu.Unlock()

	if !had {
		return CPUModes{}
	}
	// Guest time is already counted inside User (and GuestNice inside Nice)
	// by the kernel, so it is deliberately not added again here — doing so
	// would push the total past 100% on a machine running VMs.
	d := CPUModes{
		User:    cur.User - prev.User,
		System:  cur.System - prev.System,
		Nice:    cur.Nice - prev.Nice,
		IOWait:  cur.Iowait - prev.Iowait,
		IRQ:     cur.Irq - prev.Irq,
		SoftIRQ: cur.Softirq - prev.Softirq,
		Steal:   cur.Steal - prev.Steal,
		Idle:    cur.Idle - prev.Idle,
	}
	total := d.User + d.System + d.Nice + d.IOWait + d.IRQ + d.SoftIRQ + d.Steal + d.Idle
	// A counter that went backwards means the CPU was hot-plugged or the
	// host was migrated; reporting nothing for one interval beats reporting
	// a negative percentage.
	if total <= 0 {
		return CPUModes{}
	}
	scale := 100 / total
	return CPUModes{
		User:    round1(max0(d.User) * scale),
		System:  round1(max0(d.System) * scale),
		Nice:    round1(max0(d.Nice) * scale),
		IOWait:  round1(max0(d.IOWait) * scale),
		IRQ:     round1(max0(d.IRQ) * scale),
		SoftIRQ: round1(max0(d.SoftIRQ) * scale),
		Steal:   round1(max0(d.Steal) * scale),
		Idle:    round1(max0(d.Idle) * scale),
	}
}

func max0(f float64) float64 {
	if f < 0 {
		return 0
	}
	return f
}

// mounts reports real filesystems only. Pseudo filesystems (tmpfs, overlay
// layers, cgroup mounts) would otherwise bury the handful of mounts an
// operator actually cares about.
func (c *Collector) mounts(ctx context.Context, elapsed float64) []MountStats {
	parts, err := disk.PartitionsWithContext(ctx, false)
	if err != nil {
		return nil
	}
	io, _ := disk.IOCountersWithContext(ctx)

	out := make([]MountStats, 0, len(parts))
	seen := map[string]bool{}
	for _, p := range parts {
		if skipFS(p.Fstype) || seen[p.Mountpoint] {
			continue
		}
		seen[p.Mountpoint] = true
		u, err := disk.UsageWithContext(ctx, p.Mountpoint)
		if err != nil {
			continue
		}
		m := MountStats{
			Device: p.Device, Mountpoint: p.Mountpoint, FSType: p.Fstype,
			Total: u.Total, Used: u.Used, Free: u.Free, UsedPercent: round1(u.UsedPercent),
			InodesTotal: u.InodesTotal, InodesUsed: u.InodesUsed,
		}
		if st, ok := io[deviceName(p.Device)]; ok {
			m.ReadBytes, m.WriteBytes = st.ReadBytes, st.WriteBytes
			c.mu.Lock()
			if prev, ok := c.lastDisk[st.Name]; ok && elapsed > 0 {
				m.ReadRate = rate(st.ReadBytes, prev.ReadBytes, elapsed)
				m.WriteRate = rate(st.WriteBytes, prev.WriteBytes, elapsed)
				m.ReadOps = rate(st.ReadCount, prev.ReadCount, elapsed)
				m.WriteOps = rate(st.WriteCount, prev.WriteCount, elapsed)
				m.ReadLatency = latency(st.ReadTime, prev.ReadTime, st.ReadCount, prev.ReadCount)
				m.WriteLatency = latency(st.WriteTime, prev.WriteTime, st.WriteCount, prev.WriteCount)
				m.BusyPercent = busy(st.IoTime, prev.IoTime, elapsed)
			}
			c.lastDisk[st.Name] = st
			c.mu.Unlock()
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Mountpoint < out[j].Mountpoint })
	return out
}

func (c *Collector) network(ctx context.Context, elapsed float64) []NetStats {
	counters, err := net.IOCountersWithContext(ctx, true)
	if err != nil {
		return nil
	}
	ifaces, _ := net.InterfacesWithContext(ctx)
	meta := map[string]net.InterfaceStat{}
	for _, i := range ifaces {
		meta[i.Name] = i
	}
	out := make([]NetStats, 0, len(counters))
	for _, ct := range counters {
		if ct.Name == "lo" {
			continue
		}
		n := NetStats{
			Interface: ct.Name,
			BytesSent: ct.BytesSent, BytesRecv: ct.BytesRecv,
			PacketsSent: ct.PacketsSent, PacketsRecv: ct.PacketsRecv,
			ErrIn: ct.Errin, ErrOut: ct.Errout, DropIn: ct.Dropin, DropOut: ct.Dropout,
			Addrs: []string{},
		}
		if m, ok := meta[ct.Name]; ok {
			for _, a := range m.Addrs {
				n.Addrs = append(n.Addrs, a.Addr)
			}
			for _, f := range m.Flags {
				if f == "up" {
					n.IsUp = true
				}
			}
		}
		c.mu.Lock()
		if prev, ok := c.lastNet[ct.Name]; ok && elapsed > 0 {
			n.SendRate = rate(ct.BytesSent, prev.BytesSent, elapsed)
			n.RecvRate = rate(ct.BytesRecv, prev.BytesRecv, elapsed)
		}
		c.lastNet[ct.Name] = ct
		c.mu.Unlock()
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Interface < out[j].Interface })
	return out
}

// rate guards against counter resets (interface reset, device re-enumerated),
// which would otherwise show as an enormous spike.
func rate(cur, prev uint64, elapsed float64) float64 {
	if cur < prev || elapsed <= 0 {
		return 0
	}
	return round1(float64(cur-prev) / elapsed)
}

// latency is the mean service time of one request over the interval, in
// milliseconds. The kernel's *Time counters are cumulative milliseconds spent
// on requests of that kind, so dividing by the number of requests in the same
// interval gives what iostat calls await.
func latency(curMs, prevMs, curOps, prevOps uint64) float64 {
	if curMs < prevMs || curOps <= prevOps {
		return 0
	}
	ops := curOps - prevOps
	if ops == 0 {
		return 0
	}
	return round1(float64(curMs-prevMs) / float64(ops))
}

// busy is iostat's %util: the share of the interval during which the device
// had at least one request outstanding. It saturates at 100 because a device
// with a queue can accumulate more busy-milliseconds than there are
// milliseconds, and a chart reading 340% is not more informative than one
// reading 100.
func busy(curMs, prevMs uint64, elapsed float64) float64 {
	if curMs < prevMs || elapsed <= 0 {
		return 0
	}
	pct := float64(curMs-prevMs) / (elapsed * 1000) * 100
	if pct > 100 {
		pct = 100
	}
	return round1(pct)
}

func deviceName(dev string) string {
	return strings.TrimPrefix(dev, "/dev/")
}

var pseudoFS = map[string]bool{
	"tmpfs": true, "devtmpfs": true, "squashfs": true, "overlay": true,
	"proc": true, "sysfs": true, "cgroup": true, "cgroup2": true, "devpts": true,
	"mqueue": true, "hugetlbfs": true, "debugfs": true, "tracefs": true,
	"fusectl": true, "configfs": true, "pstore": true, "bpf": true,
	"binfmt_misc": true, "autofs": true, "securityfs": true, "efivarfs": true,
	"ramfs": true, "nsfs": true, "rpc_pipefs": true,
}

func skipFS(fs string) bool { return pseudoFS[strings.ToLower(fs)] }

func round1(f float64) float64 {
	return float64(int64(f*10+0.5)) / 10
}

func round1s(fs []float64) []float64 {
	out := make([]float64, len(fs))
	for i, f := range fs {
		out[i] = round1(f)
	}
	return out
}

// DirEntry is one row of the per-directory size breakdown.
type DirEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	IsDir   bool   `json:"isDir"`
	Entries int    `json:"entries"`
}

var ErrDirScanLimit = errors.New("directory scan exceeded its work limit")

const (
	maxDirBreakdownChildren = 10_000
	maxDirBreakdownVisits   = 250_000
)

// DirBreakdown answers "what is eating this mount". It walks one level deep
// and sums each child recursively, staying on the starting filesystem so a
// scan of / does not wander into network mounts or /proc.
func DirBreakdown(ctx context.Context, root string, limit int) ([]DirEntry, error) {
	return dirBreakdown(ctx, root, limit, maxDirBreakdownChildren, maxDirBreakdownVisits)
}

func dirBreakdown(ctx context.Context, root string, limit, maxChildren, maxVisits int) ([]DirEntry, error) {
	dir, err := os.Open(root)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(maxChildren + 1)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if len(entries) > maxChildren {
		return nil, ErrDirScanLimit
	}
	rootDev, err := deviceOf(root)
	if err != nil {
		return nil, err
	}
	out := make([]DirEntry, 0, len(entries))
	budget := dirScanBudget{remaining: maxVisits}
	for _, e := range entries {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !budget.take() {
			return nil, ErrDirScanLimit
		}
		full := filepath.Join(root, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		if !e.IsDir() {
			out = append(out, DirEntry{Name: e.Name(), Path: full, Size: info.Size()})
			continue
		}
		size, count, err := dirSize(ctx, full, rootDev, &budget)
		if err != nil {
			return nil, err
		}
		out = append(out, DirEntry{Name: e.Name(), Path: full, Size: size, IsDir: true, Entries: count})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Size > out[j].Size })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

type dirScanBudget struct{ remaining int }

func (b *dirScanBudget) take() bool {
	if b.remaining <= 0 {
		return false
	}
	b.remaining--
	return true
}

func dirSize(ctx context.Context, root string, dev uint64, budget *dirScanBudget) (int64, int, error) {
	var total int64
	var count int
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			// A successful response must not look complete when part of the tree
			// could not be read. The handler reports this failure to the caller.
			return err
		}
		if !budget.take() {
			return ErrDirScanLimit
		}
		if d.IsDir() {
			// Do not descend into a different filesystem: the caller asked
			// about this mount's consumption, not everything beneath it.
			if path != root {
				childDev, err := deviceOf(path)
				if err != nil {
					return err
				}
				if childDev != dev {
					return filepath.SkipDir
				}
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		total += info.Size()
		count++
		return nil
	})
	return total, count, err
}
