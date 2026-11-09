package procs

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

// ProcessInspect is everything else the kernel keeps about one process: who
// it runs as, how it is scheduled, where its memory goes, what it may do, the
// signals it handles, what it holds open, and the threads it runs.
//
// It is the process page's slower read. The live readings are `Detail`,
// polled every two seconds; these change rarely or are worth reading only
// when somebody asks (a descriptor list, an environment), so the page reads
// them every few seconds and none of it rides along with a table poll.
//
// Every block is best effort. A field the account cannot read, or a kernel
// that does not offer it, is left empty and its `…Ready` flag false, so the
// page can say "not readable" rather than draw a zero.
type ProcessInspect struct {
	PID        int32     `json:"pid"`
	CreateTime time.Time `json:"createTime"`
	// Argv is the command line as the words the program was handed, which a
	// joined string cannot tell apart from an argument holding a space.
	Argv []string `json:"argv"`
	Exe  string   `json:"exe,omitempty"`
	// ExeDeleted is true when the file the process was started from has been
	// replaced or removed since: an upgraded package whose old binary is still
	// the one running, which only a restart picks up.
	ExeDeleted bool `json:"exeDeleted,omitempty"`
	// Root is the directory the process sees as `/` — another one means a
	// chroot or a container's filesystem.
	Root string `json:"root,omitempty"`
	// WaitChannel is the kernel function a sleeping process is waiting in
	// (`do_epoll_wait`, `futex_wait_queue`), which is what "blocked" means.
	WaitChannel string `json:"waitChannel,omitempty"`
	// NamespacePID is the process's PID inside its own PID namespace, where
	// that is not the host's: a container's main process is 1 there.
	NamespacePID int32 `json:"namespacePid,omitempty"`

	Identity    InspectIdentity     `json:"identity"`
	Scheduling  InspectScheduling   `json:"scheduling"`
	Memory      InspectMemory       `json:"memory"`
	IO          InspectIO           `json:"io"`
	Security    InspectSecurity     `json:"security"`
	Signals     InspectSignals      `json:"signals"`
	Namespaces  []InspectNamespace  `json:"namespaces"`
	Cgroup      InspectCgroup       `json:"cgroup"`
	Limits      []InspectLimit      `json:"limits"`
	Files       InspectFiles        `json:"files"`
	Libraries   []InspectLibrary    `json:"libraries"`
	Connections []InspectConnection `json:"connections"`
	// ConnectionsTotal counts every socket that is not listening, of which
	// Connections holds the first connectionsShown.
	ConnectionsTotal int `json:"connectionsTotal"`
	// Environment is the process's environment as it was started, `NAME=value`
	// each. The API masks credential-shaped values for anybody without
	// system.admin, as it does a container's.
	Environment         []string        `json:"environment"`
	EnvironmentReadable bool            `json:"environmentReadable"`
	Threads             []InspectThread `json:"threads"`
	ThreadsTotal        int             `json:"threadsTotal"`
}

// InspectID is a numeric user or group with the name the host gives it.
type InspectID struct {
	ID   uint32 `json:"id"`
	Name string `json:"name,omitempty"`
}

// InspectIdentity is who the process runs as, and where it sits among the
// processes started with it.
type InspectIdentity struct {
	Ready bool `json:"ready"`
	// User is the real user; EffectiveUser is the one the kernel checks, which
	// differs for a setuid program or one that changed it after starting.
	User           InspectID   `json:"user"`
	EffectiveUser  InspectID   `json:"effectiveUser"`
	SavedUser      InspectID   `json:"savedUser"`
	Group          InspectID   `json:"group"`
	EffectiveGroup InspectID   `json:"effectiveGroup"`
	Groups         []InspectID `json:"groups"`
	// Terminal is the controlling terminal (`pts/3`), empty for a daemon.
	Terminal     string `json:"terminal,omitempty"`
	Session      int32  `json:"session"`
	ProcessGroup int32  `json:"processGroup"`
	// Foreground is true when the process's group holds its terminal.
	Foreground bool `json:"foreground,omitempty"`
}

// InspectScheduling is how the kernel runs the process and what it has
// spent.
type InspectScheduling struct {
	Ready bool `json:"ready"`
	// Policy is the scheduler class in words: normal, batch, idle, fifo,
	// round-robin or deadline.
	Policy           string `json:"policy"`
	RealtimePriority int    `json:"realtimePriority,omitempty"`
	Nice             int32  `json:"nice"`
	// CPUs is the list of processors it may run on, as the kernel prints it
	// (`0-3,6`); CPUCount counts them and HostCPUs is what the host has.
	CPUs     string `json:"cpus,omitempty"`
	CPUCount int    `json:"cpuCount"`
	HostCPUs int    `json:"hostCpus"`
	LastCPU  int    `json:"lastCpu"`
	// Seconds of processor time since the process started, and those of the
	// children it has already reaped.
	UserSeconds           float64 `json:"userSeconds"`
	SystemSeconds         float64 `json:"systemSeconds"`
	ChildrenUserSeconds   float64 `json:"childrenUserSeconds"`
	ChildrenSystemSeconds float64 `json:"childrenSystemSeconds"`
	// IOWaitSeconds is the time it spent waiting on block I/O, where the
	// kernel accounts it (delay accounting); zero otherwise.
	IOWaitSeconds float64 `json:"ioWaitSeconds"`
	// VoluntarySwitches are the times it gave the processor up to wait;
	// involuntary ones are the times it was made to.
	VoluntarySwitches   uint64 `json:"voluntarySwitches"`
	InvoluntarySwitches uint64 `json:"involuntarySwitches"`
	MinorFaults         uint64 `json:"minorFaults"`
	MajorFaults         uint64 `json:"majorFaults"`
}

// InspectMemory is where the process's memory goes, in bytes.
type InspectMemory struct {
	Ready bool `json:"ready"`
	// Resident is what it holds in RAM, split into its own anonymous memory,
	// pages of files it maps, and shared memory.
	Resident     uint64 `json:"resident"`
	Anonymous    uint64 `json:"anonymous"`
	FileBacked   uint64 `json:"fileBacked"`
	SharedMemory uint64 `json:"sharedMemory"`
	// Peak is the most resident memory it has held since it started.
	Peak        uint64 `json:"peak"`
	Virtual     uint64 `json:"virtual"`
	VirtualPeak uint64 `json:"virtualPeak"`
	Data        uint64 `json:"data"`
	Stack       uint64 `json:"stack"`
	Text        uint64 `json:"text"`
	Libraries   uint64 `json:"libraries"`
	Locked      uint64 `json:"locked"`
	PageTables  uint64 `json:"pageTables"`
	Swap        uint64 `json:"swap"`
	// Proportional (PSS) charges each shared page to the processes sharing it
	// in equal parts, and Unique (USS) is what only this process holds —
	// what ending it would give back.
	Proportional uint64 `json:"proportional"`
	Unique       uint64 `json:"unique"`
	RollupReady  bool   `json:"rollupReady"`
	// OOMScore is how likely the kernel is to end it first when memory runs
	// out (0–1000 and beyond), and OOMScoreAdj the bias set on it.
	OOMScore    int  `json:"oomScore"`
	OOMScoreAdj int  `json:"oomScoreAdj"`
	OOMReady    bool `json:"oomReady"`
}

// InspectIO is everything the process has read and written, as /proc/<pid>/io
// counts it.
type InspectIO struct {
	Ready bool `json:"ready"`
	// CharsRead and CharsWritten count every byte through read and write,
	// sockets and pipes and the page cache included; ReadBytes and
	// WriteBytes are what reached the disk.
	CharsRead           uint64 `json:"charsRead"`
	CharsWritten        uint64 `json:"charsWritten"`
	ReadCalls           uint64 `json:"readCalls"`
	WriteCalls          uint64 `json:"writeCalls"`
	ReadBytes           uint64 `json:"readBytes"`
	WriteBytes          uint64 `json:"writeBytes"`
	CancelledWriteBytes uint64 `json:"cancelledWriteBytes"`
}

// InspectSecurity is what the process may do beyond an ordinary user's.
type InspectSecurity struct {
	Ready bool `json:"ready"`
	// Capabilities is its effective set by name; AllCapabilities is true when
	// that is every capability the kernel has, which is root's.
	Capabilities    []string `json:"capabilities"`
	AllCapabilities bool     `json:"allCapabilities"`
	// BoundingAll is false when its bounding set has been narrowed, so even a
	// root process inside it cannot gain what was taken out.
	BoundingAll bool `json:"boundingAll"`
	NoNewPrivs  bool `json:"noNewPrivs"`
	// Seccomp is off, strict or filter.
	Seccomp        string `json:"seccomp"`
	SeccompFilters int    `json:"seccompFilters,omitempty"`
	// Label is the Linux security module's label: an AppArmor profile or an
	// SELinux context.
	Label string `json:"label,omitempty"`
}

// InspectSignals is which signals the process has a handler for, ignores,
// blocks and has waiting. A program that catches SIGHUP does something of its
// own with it — most reload — and one that does not is ended by it, which is
// the difference between a reload and an outage.
type InspectSignals struct {
	Ready   bool     `json:"ready"`
	Caught  []string `json:"caught"`
	Ignored []string `json:"ignored"`
	Blocked []string `json:"blocked"`
	Pending []string `json:"pending"`
}

// InspectNamespace is one kind of namespace, and whether the process has its
// own rather than the host's.
type InspectNamespace struct {
	Kind string `json:"kind"`
	Own  bool   `json:"own"`
}

// InspectCgroup is the control group the process is accounted in and the
// limits on it.
type InspectCgroup struct {
	Path string `json:"path,omitempty"`
	// MemoryLimit is the group's memory.max, zero when there is none.
	MemoryLimit   uint64 `json:"memoryLimit,omitempty"`
	MemoryCurrent uint64 `json:"memoryCurrent,omitempty"`
	// CPULimit is the group's cpu.max as processors, zero when uncapped.
	CPULimit    float64 `json:"cpuLimit,omitempty"`
	PidsLimit   uint64  `json:"pidsLimit,omitempty"`
	PidsCurrent uint64  `json:"pidsCurrent,omitempty"`
}

// InspectLimit is one resource limit as /proc/<pid>/limits prints it.
type InspectLimit struct {
	Name string `json:"name"`
	Soft string `json:"soft"`
	Hard string `json:"hard"`
	Unit string `json:"unit,omitempty"`
}

// InspectFiles is what the process holds open, by kind and one by one.
type InspectFiles struct {
	Ready   bool `json:"ready"`
	Total   int  `json:"total"`
	Files   int  `json:"files"`
	Sockets int  `json:"sockets"`
	Pipes   int  `json:"pipes"`
	Devices int  `json:"devices"`
	Other   int  `json:"other"`
	// Deleted counts files held open after they were removed, whose space the
	// disk gets back only when the process lets go of them.
	Deleted   int         `json:"deleted"`
	Entries   []InspectFD `json:"entries"`
	Truncated bool        `json:"truncated,omitempty"`
}

// InspectFD is one open descriptor.
type InspectFD struct {
	FD int `json:"fd"`
	// Kind is file, socket, pipe, device or other.
	Kind    string `json:"kind"`
	Target  string `json:"target"`
	Deleted bool   `json:"deleted,omitempty"`
}

// InspectLibrary is a shared library the process has mapped. A deleted one
// was replaced by an upgrade after the process loaded it.
type InspectLibrary struct {
	Path    string `json:"path"`
	Deleted bool   `json:"deleted,omitempty"`
}

// InspectConnection is one socket that is not listening.
type InspectConnection struct {
	Proto  string `json:"proto"`
	Local  string `json:"local"`
	Remote string `json:"remote,omitempty"`
	State  string `json:"state,omitempty"`
}

// InspectThread is one of the process's threads and the processor time it
// has used.
type InspectThread struct {
	TID        int32   `json:"tid"`
	Name       string  `json:"name"`
	State      string  `json:"state"`
	CPUSeconds float64 `json:"cpuSeconds"`
	LastCPU    int     `json:"lastCpu"`
}

const (
	// A process holding tens of thousands of descriptors is the case this
	// page is opened for, so they are all counted; only the first of them
	// are listed, and past descriptorsCounted the counts are a sample.
	descriptorsShown   = 500
	descriptorsCounted = 20000
	connectionsShown   = 200
	threadsShown       = 200
	librariesShown     = 400
	environmentShown   = 1000
	// clockTicks is USER_HZ, which the kernel fixes at 100 for user space on
	// every architecture it supports; /proc's times are in it.
	clockTicks = 100
)

// Inspect reads everything ProcessInspect describes for one process.
func (t *Table) Inspect(ctx context.Context, pid int32) (*ProcessInspect, error) {
	p, err := process.NewProcessWithContext(ctx, pid)
	if err != nil {
		return nil, fmt.Errorf("no process with pid %d", pid)
	}
	out := InspectAt("/proc", pid, lookupUser, lookupGroup)
	if ct, err := p.CreateTimeWithContext(ctx); err == nil {
		out.CreateTime = time.UnixMilli(ct).UTC()
	}
	out.Connections, out.ConnectionsTotal = connectionsOf(ctx, p)
	return out, nil
}

// InspectAt reads a process under the process table at root, with users and
// groups named by the two lookups. Split from Inspect so a test can hand it a
// directory laid out as /proc is.
func InspectAt(root string, pid int32, userName, groupName func(uint32) string) *ProcessInspect {
	dir := filepath.Join(root, strconv.Itoa(int(pid)))
	out := &ProcessInspect{
		PID:         pid,
		Argv:        []string{},
		Namespaces:  []InspectNamespace{},
		Limits:      []InspectLimit{},
		Libraries:   []InspectLibrary{},
		Connections: []InspectConnection{},
		Environment: []string{},
		Threads:     []InspectThread{},
	}
	out.Files.Entries = []InspectFD{}
	out.Identity.Groups = []InspectID{}
	out.Security.Capabilities = []string{}
	out.Signals = InspectSignals{Caught: []string{}, Ignored: []string{}, Blocked: []string{}, Pending: []string{}}

	if b, err := os.ReadFile(filepath.Join(dir, "cmdline")); err == nil {
		for _, arg := range strings.Split(strings.TrimRight(string(b), "\x00"), "\x00") {
			if arg != "" {
				out.Argv = append(out.Argv, arg)
			}
		}
	}
	if exe, err := os.Readlink(filepath.Join(dir, "exe")); err == nil {
		out.Exe, out.ExeDeleted = strings.CutSuffix(exe, " (deleted)")
	}
	if rootDir, err := os.Readlink(filepath.Join(dir, "root")); err == nil {
		out.Root = rootDir
	}
	if b, err := os.ReadFile(filepath.Join(dir, "wchan")); err == nil {
		if wchan := strings.TrimSpace(string(b)); wchan != "0" {
			out.WaitChannel = wchan
		}
	}

	status := readKeyed(filepath.Join(dir, "status"))
	readStatus(status, out, userName, groupName)
	readStat(filepath.Join(dir, "stat"), out)
	readRollup(filepath.Join(dir, "smaps_rollup"), &out.Memory)
	out.Memory.OOMScore, out.Memory.OOMScoreAdj, out.Memory.OOMReady = readOOM(dir)
	readIO(filepath.Join(dir, "io"), &out.IO)
	if b, err := os.ReadFile(filepath.Join(dir, "attr", "current")); err == nil {
		out.Security.Label = strings.TrimSpace(strings.TrimRight(string(b), "\x00"))
	}
	out.Namespaces = namespacesOf(root, dir)
	out.Cgroup = cgroupOf(dir, "/sys/fs/cgroup")
	out.Limits = limitsOf(filepath.Join(dir, "limits"))
	out.Files = descriptorsOf(filepath.Join(dir, "fd"))
	out.Libraries = librariesOf(filepath.Join(dir, "maps"))
	out.Environment, out.EnvironmentReadable = environmentOf(filepath.Join(dir, "environ"))
	out.Threads, out.ThreadsTotal = threadsOf(filepath.Join(dir, "task"))
	return out
}

// readKeyed reads a `Key:\tvalue` file such as status or io into a map.
func readKeyed(path string) map[string]string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	out := map[string]string{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), ":")
		if ok {
			out[key] = strings.TrimSpace(value)
		}
	}
	return out
}

func readStatus(status map[string]string, out *ProcessInspect, userName, groupName func(uint32) string) {
	if status == nil {
		return
	}
	id := func(value string, index int, name func(uint32) string) InspectID {
		fields := strings.Fields(value)
		if index >= len(fields) {
			return InspectID{}
		}
		n, err := strconv.ParseUint(fields[index], 10, 32)
		if err != nil {
			return InspectID{}
		}
		return InspectID{ID: uint32(n), Name: name(uint32(n))}
	}
	if uid, ok := status["Uid"]; ok {
		out.Identity.Ready = true
		out.Identity.User = id(uid, 0, userName)
		out.Identity.EffectiveUser = id(uid, 1, userName)
		out.Identity.SavedUser = id(uid, 2, userName)
	}
	if gid, ok := status["Gid"]; ok {
		out.Identity.Group = id(gid, 0, groupName)
		out.Identity.EffectiveGroup = id(gid, 1, groupName)
	}
	for _, g := range strings.Fields(status["Groups"]) {
		if n, err := strconv.ParseUint(g, 10, 32); err == nil {
			out.Identity.Groups = append(out.Identity.Groups, InspectID{ID: uint32(n), Name: groupName(uint32(n))})
		}
	}
	// NSpid lists the PID in each namespace from the host's inwards; the last
	// is the one the process sees itself as.
	if fields := strings.Fields(status["NSpid"]); len(fields) > 1 {
		if n, err := strconv.ParseInt(fields[len(fields)-1], 10, 32); err == nil {
			out.NamespacePID = int32(n)
		}
	}

	m := &out.Memory
	kb := func(key string) uint64 {
		fields := strings.Fields(status[key])
		if len(fields) == 0 {
			return 0
		}
		n, _ := strconv.ParseUint(fields[0], 10, 64)
		return n * 1024
	}
	if _, ok := status["VmRSS"]; ok {
		m.Ready = true
	}
	m.Resident, m.Anonymous, m.FileBacked, m.SharedMemory = kb("VmRSS"), kb("RssAnon"), kb("RssFile"), kb("RssShmem")
	m.Peak, m.Virtual, m.VirtualPeak = kb("VmHWM"), kb("VmSize"), kb("VmPeak")
	m.Data, m.Stack, m.Text, m.Libraries = kb("VmData"), kb("VmStk"), kb("VmExe"), kb("VmLib")
	m.Locked, m.PageTables, m.Swap = kb("VmLck"), kb("VmPTE"), kb("VmSwap")

	s := &out.Scheduling
	s.CPUs = status["Cpus_allowed_list"]
	s.CPUCount = cpuListCount(s.CPUs)
	s.HostCPUs = runtime.NumCPU()
	s.VoluntarySwitches, _ = strconv.ParseUint(status["voluntary_ctxt_switches"], 10, 64)
	s.InvoluntarySwitches, _ = strconv.ParseUint(status["nonvoluntary_ctxt_switches"], 10, 64)

	sec := &out.Security
	if eff, ok := status["CapEff"]; ok {
		sec.Ready = true
		sec.Capabilities, sec.AllCapabilities = capabilityNames(eff)
		_, sec.BoundingAll = capabilityNames(status["CapBnd"])
	}
	sec.NoNewPrivs = status["NoNewPrivs"] == "1"
	switch status["Seccomp"] {
	case "1":
		sec.Seccomp = "strict"
	case "2":
		sec.Seccomp = "filter"
	default:
		sec.Seccomp = "off"
	}
	sec.SeccompFilters, _ = strconv.Atoi(status["Seccomp_filters"])

	if caught, ok := status["SigCgt"]; ok {
		out.Signals = InspectSignals{
			Ready:   true,
			Caught:  signalNames(caught),
			Ignored: signalNames(status["SigIgn"]),
			Blocked: signalNames(status["SigBlk"]),
			// A signal waits for the thread it was sent to or for the whole
			// process; either way it has not been handled yet.
			Pending: mergeNames(signalNames(status["SigPnd"]), signalNames(status["ShdPnd"])),
		}
	}
}

// readStat reads the fields of /proc/<pid>/stat that status does not carry.
func readStat(path string, out *ProcessInspect) {
	fields, ok := statFields(path)
	if !ok || len(fields) < 40 {
		return
	}
	num := func(i int) int64 { n, _ := strconv.ParseInt(fields[i], 10, 64); return n }
	unum := func(i int) uint64 { n, _ := strconv.ParseUint(fields[i], 10, 64); return n }
	id := &out.Identity
	id.ProcessGroup, id.Session = int32(num(2)), int32(num(3))
	id.Terminal = terminalName(num(4))
	id.Foreground = id.Terminal != "" && num(5) == num(2)

	s := &out.Scheduling
	s.Ready = true
	s.MinorFaults, s.MajorFaults = unum(7), unum(9)
	s.UserSeconds, s.SystemSeconds = float64(unum(11))/clockTicks, float64(unum(12))/clockTicks
	s.ChildrenUserSeconds, s.ChildrenSystemSeconds = float64(unum(13))/clockTicks, float64(unum(14))/clockTicks
	s.Nice = int32(num(16))
	s.LastCPU = int(num(36))
	s.RealtimePriority = int(num(37))
	s.Policy = policyName(num(38))
	s.IOWaitSeconds = float64(unum(39)) / clockTicks
}

// statFields splits a stat line after the parenthesised name, which may hold
// spaces and parentheses of its own; field 0 is the state.
func statFields(path string) ([]string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	end := strings.LastIndexByte(string(b), ')')
	if end < 0 {
		return nil, false
	}
	return strings.Fields(string(b[end+1:])), true
}

func statName(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	start, end := strings.IndexByte(string(b), '('), strings.LastIndexByte(string(b), ')')
	if start < 0 || end <= start {
		return ""
	}
	return string(b[start+1 : end])
}

func readRollup(path string, m *InspectMemory) {
	rollup := readKeyed(path)
	if rollup == nil {
		return
	}
	kb := func(key string) uint64 {
		fields := strings.Fields(rollup[key])
		if len(fields) == 0 {
			return 0
		}
		n, _ := strconv.ParseUint(fields[0], 10, 64)
		return n * 1024
	}
	if _, ok := rollup["Pss"]; !ok {
		return
	}
	m.RollupReady = true
	m.Proportional = kb("Pss")
	m.Unique = kb("Private_Clean") + kb("Private_Dirty")
}

func readOOM(dir string) (score, adj int, ok bool) {
	b, err := os.ReadFile(filepath.Join(dir, "oom_score"))
	if err != nil {
		return 0, 0, false
	}
	score, err = strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, 0, false
	}
	if b, err := os.ReadFile(filepath.Join(dir, "oom_score_adj")); err == nil {
		adj, _ = strconv.Atoi(strings.TrimSpace(string(b)))
	}
	return score, adj, true
}

func readIO(path string, io *InspectIO) {
	counters := readKeyed(path)
	if counters == nil {
		return
	}
	n := func(key string) uint64 { v, _ := strconv.ParseUint(counters[key], 10, 64); return v }
	io.Ready = true
	io.CharsRead, io.CharsWritten = n("rchar"), n("wchar")
	io.ReadCalls, io.WriteCalls = n("syscr"), n("syscw")
	io.ReadBytes, io.WriteBytes, io.CancelledWriteBytes = n("read_bytes"), n("write_bytes"), n("cancelled_write_bytes")
}

// namespaceKinds are the namespaces a process can be given its own of, in the
// order a reader asks about them.
var namespaceKinds = []string{"pid", "net", "mnt", "user", "uts", "ipc", "cgroup", "time"}

// namespacesOf compares each of the process's namespaces with init's: one
// that differs is the process's own, which is what being in a container is.
func namespacesOf(root, dir string) []InspectNamespace {
	out := []InspectNamespace{}
	for _, kind := range namespaceKinds {
		mine, err := os.Readlink(filepath.Join(dir, "ns", kind))
		if err != nil {
			continue
		}
		host, err := os.Readlink(filepath.Join(root, "1", "ns", kind))
		out = append(out, InspectNamespace{Kind: kind, Own: err == nil && mine != host})
	}
	return out
}

// cgroupOf reads the unified hierarchy's path and the limits set on it.
func cgroupOf(dir, mount string) InspectCgroup {
	out := InspectCgroup{}
	b, err := os.ReadFile(filepath.Join(dir, "cgroup"))
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		if path, ok := strings.CutPrefix(line, "0::"); ok {
			out.Path = path
		}
	}
	if out.Path == "" {
		return out
	}
	group := filepath.Join(mount, filepath.Clean("/"+out.Path))
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(group, name))
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(b))
	}
	number := func(value string) uint64 {
		n, _ := strconv.ParseUint(value, 10, 64)
		return n
	}
	out.MemoryLimit = number(read("memory.max"))
	out.MemoryCurrent = number(read("memory.current"))
	out.PidsLimit = number(read("pids.max"))
	out.PidsCurrent = number(read("pids.current"))
	if quota, period, ok := strings.Cut(read("cpu.max"), " "); ok && quota != "max" {
		q, p := number(quota), number(period)
		if q > 0 && p > 0 {
			out.CPULimit = float64(q) / float64(p)
		}
	}
	return out
}

// limitsOf reads /proc/<pid>/limits, whose columns are laid out under a
// header rather than separated: a limit's name holds spaces of its own.
func limitsOf(path string) []InspectLimit {
	out := []InspectLimit{}
	b, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	lines := strings.Split(string(b), "\n")
	if len(lines) == 0 {
		return out
	}
	header := lines[0]
	soft, hard, unit := strings.Index(header, "Soft Limit"), strings.Index(header, "Hard Limit"), strings.Index(header, "Units")
	if soft <= 0 || hard <= soft || unit <= hard {
		return out
	}
	column := func(line string, from, to int) string {
		if from >= len(line) {
			return ""
		}
		if to < 0 || to > len(line) {
			to = len(line)
		}
		return strings.TrimSpace(line[from:to])
	}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, InspectLimit{
			Name: column(line, 0, soft),
			Soft: column(line, soft, hard),
			Hard: column(line, hard, unit),
			Unit: column(line, unit, -1),
		})
	}
	return out
}

// descriptorsOf reads every open descriptor's target and sorts it into a
// kind. A socket and a pipe are `socket:[inode]` and `pipe:[inode]`; an
// eventfd or an epoll set is an anonymous inode, which reads as "other".
func descriptorsOf(dir string) InspectFiles {
	out := InspectFiles{Entries: []InspectFD{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	out.Ready = true
	out.Total = len(entries)
	fds := make([]int, 0, len(entries))
	for _, entry := range entries {
		if n, err := strconv.Atoi(entry.Name()); err == nil {
			fds = append(fds, n)
		}
	}
	sort.Ints(fds)
	for i, fd := range fds {
		if i >= descriptorsCounted {
			break
		}
		target, err := os.Readlink(filepath.Join(dir, strconv.Itoa(fd)))
		if err != nil {
			continue
		}
		kind := descriptorKind(target)
		deleted := false
		if kind == "file" {
			target, deleted = strings.CutSuffix(target, " (deleted)")
		}
		switch kind {
		case "file":
			out.Files++
		case "socket":
			out.Sockets++
		case "pipe":
			out.Pipes++
		case "device":
			out.Devices++
		default:
			out.Other++
		}
		if deleted {
			out.Deleted++
		}
		if len(out.Entries) < descriptorsShown {
			out.Entries = append(out.Entries, InspectFD{FD: fd, Kind: kind, Target: target, Deleted: deleted})
		}
	}
	out.Truncated = len(fds) > len(out.Entries)
	return out
}

func descriptorKind(target string) string {
	switch {
	case strings.HasPrefix(target, "socket:"):
		return "socket"
	case strings.HasPrefix(target, "pipe:"):
		return "pipe"
	case strings.HasPrefix(target, "/dev/"):
		return "device"
	case strings.HasPrefix(target, "/"):
		return "file"
	default:
		return "other"
	}
}

// librariesOf lists the shared objects the process has mapped, once each,
// with the ones replaced since it loaded them first: those are what a
// restart after an upgrade is for.
func librariesOf(path string) []InspectLibrary {
	out := []InspectLibrary{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	seen := map[string]bool{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 6 {
			continue
		}
		file := strings.Join(fields[5:], " ")
		file, deleted := strings.CutSuffix(file, " (deleted)")
		base := filepath.Base(file)
		if !strings.HasPrefix(file, "/") || !(strings.HasSuffix(base, ".so") || strings.Contains(base, ".so.")) {
			continue
		}
		if seen[file] {
			continue
		}
		seen[file] = true
		out = append(out, InspectLibrary{Path: file, Deleted: deleted})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Deleted != out[j].Deleted {
			return out[i].Deleted
		}
		return out[i].Path < out[j].Path
	})
	if len(out) > librariesShown {
		out = out[:librariesShown]
	}
	return out
}

func environmentOf(path string) ([]string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return []string{}, false
	}
	out := []string{}
	for _, entry := range strings.Split(string(b), "\x00") {
		if entry == "" {
			continue
		}
		out = append(out, entry)
		if len(out) >= environmentShown {
			break
		}
	}
	sort.Strings(out)
	return out, true
}

// threadsOf reads each thread's name, state and processor time, busiest
// first: a process at 300% is the one thread spinning, and its name says
// which part of the program that is.
func threadsOf(dir string) ([]InspectThread, int) {
	out := []InspectThread{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out, 0
	}
	for _, entry := range entries {
		tid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		stat := filepath.Join(dir, entry.Name(), "stat")
		fields, ok := statFields(stat)
		if !ok || len(fields) < 37 {
			continue
		}
		utime, _ := strconv.ParseUint(fields[11], 10, 64)
		stime, _ := strconv.ParseUint(fields[12], 10, 64)
		last, _ := strconv.Atoi(fields[36])
		out = append(out, InspectThread{
			TID:        int32(tid),
			Name:       statName(stat),
			State:      threadState(fields[0]),
			CPUSeconds: float64(utime+stime) / clockTicks,
			LastCPU:    last,
		})
	}
	total := len(out)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CPUSeconds != out[j].CPUSeconds {
			return out[i].CPUSeconds > out[j].CPUSeconds
		}
		return out[i].TID < out[j].TID
	})
	if len(out) > threadsShown {
		out = out[:threadsShown]
	}
	return out, total
}

func threadState(code string) string {
	switch code {
	case "R":
		return "running"
	case "S", "I":
		return "sleeping"
	case "D":
		return "blocked"
	case "Z":
		return "zombie"
	case "T", "t":
		return "stopped"
	default:
		return code
	}
}

// connectionsOf lists the process's sockets that are not listening: the
// listening ones are the detail's own, and what is left is who it is talking
// to.
func connectionsOf(ctx context.Context, p *process.Process) ([]InspectConnection, int) {
	out := []InspectConnection{}
	conns, err := p.ConnectionsWithContext(ctx)
	if err != nil {
		return out, 0
	}
	total := 0
	for _, c := range conns {
		if c.Status == "LISTEN" || (c.Type == syscall.SOCK_DGRAM && c.Raddr.Port == 0) {
			continue
		}
		if c.Family != syscall.AF_INET && c.Family != syscall.AF_INET6 {
			continue
		}
		total++
		if len(out) >= connectionsShown {
			continue
		}
		proto := "tcp"
		if c.Type == syscall.SOCK_DGRAM {
			proto = "udp"
		}
		if c.Family == syscall.AF_INET6 {
			proto += "6"
		}
		conn := InspectConnection{Proto: proto, Local: hostPort(c.Laddr.IP, c.Laddr.Port), State: strings.ToLower(c.Status)}
		if c.Raddr.Port > 0 {
			conn.Remote = hostPort(c.Raddr.IP, c.Raddr.Port)
		}
		out = append(out, conn)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].State != out[j].State {
			return out[i].State == "established"
		}
		return out[i].Remote < out[j].Remote
	})
	return out, total
}

func hostPort(ip string, port uint32) string {
	if strings.Contains(ip, ":") {
		return fmt.Sprintf("[%s]:%d", ip, port)
	}
	return fmt.Sprintf("%s:%d", ip, port)
}

// terminalName decodes stat's tty_nr: a pseudo-terminal is major 136–143, a
// virtual console major 4, a serial line major 4 from minor 64.
func terminalName(nr int64) string {
	if nr == 0 {
		return ""
	}
	major := (nr >> 8) & 0xfff
	minor := (nr & 0xff) | ((nr >> 12) & 0xfff00)
	switch {
	case major >= 136 && major <= 143:
		return fmt.Sprintf("pts/%d", (major-136)*256+minor)
	case major == 4 && minor < 64:
		return fmt.Sprintf("tty%d", minor)
	case major == 4:
		return fmt.Sprintf("ttyS%d", minor-64)
	default:
		return fmt.Sprintf("%d:%d", major, minor)
	}
}

func policyName(policy int64) string {
	switch policy {
	case 0:
		return "normal"
	case 1:
		return "fifo"
	case 2:
		return "round-robin"
	case 3:
		return "batch"
	case 5:
		return "idle"
	case 6:
		return "deadline"
	default:
		return "normal"
	}
}

// cpuListCount counts the processors in a list such as `0-3,6`.
func cpuListCount(list string) int {
	count := 0
	for _, part := range strings.Split(list, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		from, to, isRange := strings.Cut(part, "-")
		a, errA := strconv.Atoi(from)
		if !isRange {
			if errA == nil {
				count++
			}
			continue
		}
		b, errB := strconv.Atoi(to)
		if errA == nil && errB == nil && b >= a {
			count += b - a + 1
		}
	}
	return count
}

// capabilityList is every capability by its bit, as linux/capability.h
// numbers them.
var capabilityList = []string{
	"CAP_CHOWN", "CAP_DAC_OVERRIDE", "CAP_DAC_READ_SEARCH", "CAP_FOWNER", "CAP_FSETID",
	"CAP_KILL", "CAP_SETGID", "CAP_SETUID", "CAP_SETPCAP", "CAP_LINUX_IMMUTABLE",
	"CAP_NET_BIND_SERVICE", "CAP_NET_BROADCAST", "CAP_NET_ADMIN", "CAP_NET_RAW", "CAP_IPC_LOCK",
	"CAP_IPC_OWNER", "CAP_SYS_MODULE", "CAP_SYS_RAWIO", "CAP_SYS_CHROOT", "CAP_SYS_PTRACE",
	"CAP_SYS_PACCT", "CAP_SYS_ADMIN", "CAP_SYS_BOOT", "CAP_SYS_NICE", "CAP_SYS_RESOURCE",
	"CAP_SYS_TIME", "CAP_SYS_TTY_CONFIG", "CAP_MKNOD", "CAP_LEASE", "CAP_AUDIT_WRITE",
	"CAP_AUDIT_CONTROL", "CAP_SETFCAP", "CAP_MAC_OVERRIDE", "CAP_MAC_ADMIN", "CAP_SYSLOG",
	"CAP_WAKE_ALARM", "CAP_BLOCK_SUSPEND", "CAP_AUDIT_READ", "CAP_PERFMON", "CAP_BPF",
	"CAP_CHECKPOINT_RESTORE",
}

// capabilityNames decodes a status capability mask. all is true when the mask
// holds every capability this list knows, which is how a kernel newer than the
// list still reads root as root.
func capabilityNames(mask string) (names []string, all bool) {
	names = []string{}
	value, err := strconv.ParseUint(strings.TrimSpace(mask), 16, 64)
	if err != nil {
		return names, false
	}
	all = true
	for bit, name := range capabilityList {
		if value&(1<<uint(bit)) != 0 {
			names = append(names, name)
		} else {
			all = false
		}
	}
	return names, all
}

// signalNames decodes a status signal mask: bit n-1 is signal n.
func signalNames(mask string) []string {
	names := []string{}
	value, err := strconv.ParseUint(strings.TrimSpace(mask), 16, 64)
	if err != nil {
		return names
	}
	for n := 1; n <= 64; n++ {
		if value&(1<<uint(n-1)) != 0 {
			names = append(names, signalName(n))
		}
	}
	return names
}

var standardSignals = map[int]string{
	1: "SIGHUP", 2: "SIGINT", 3: "SIGQUIT", 4: "SIGILL", 5: "SIGTRAP", 6: "SIGABRT", 7: "SIGBUS",
	8: "SIGFPE", 9: "SIGKILL", 10: "SIGUSR1", 11: "SIGSEGV", 12: "SIGUSR2", 13: "SIGPIPE",
	14: "SIGALRM", 15: "SIGTERM", 16: "SIGSTKFLT", 17: "SIGCHLD", 18: "SIGCONT", 19: "SIGSTOP",
	20: "SIGTSTP", 21: "SIGTTIN", 22: "SIGTTOU", 23: "SIGURG", 24: "SIGXCPU", 25: "SIGXFSZ",
	26: "SIGVTALRM", 27: "SIGPROF", 28: "SIGWINCH", 29: "SIGIO", 30: "SIGPWR", 31: "SIGSYS",
}

func signalName(n int) string {
	if name, ok := standardSignals[n]; ok {
		return name
	}
	// glibc keeps 32 and 33 for its threads; the realtime range a program
	// may use starts at 34.
	if n >= 34 {
		return fmt.Sprintf("SIGRTMIN+%d", n-34)
	}
	return fmt.Sprintf("SIG%d", n)
}

func mergeNames(a, b []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, name := range append(a, b...) {
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

func lookupUser(id uint32) string {
	u, err := user.LookupId(strconv.FormatUint(uint64(id), 10))
	if err != nil {
		return ""
	}
	return u.Username
}

func lookupGroup(id uint32) string {
	g, err := user.LookupGroupId(strconv.FormatUint(uint64(id), 10))
	if err != nil {
		return ""
	}
	return g.Name
}
