package procs

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/host"
)

// UnitPoint is one measured window of a unit: its CPU share of one core and
// the memory charged to its cgroup at the end of the window.
type UnitPoint struct {
	At     int64   `json:"t"`
	CPU    float64 `json:"cpu"`
	Memory uint64  `json:"memory"`
}

// Manager is what systemd says of itself: its version, whether every unit it
// was asked to start is running ("running") or one has failed ("degraded"),
// and when the boot it is managing happened.
type Manager struct {
	Version  string `json:"version"`
	State    string `json:"state"`
	BootedAt int64  `json:"bootedAt,omitempty"`
}

// Inventory is the Services page's read: every service unit with what it is
// using now, and the manager.
type Inventory struct {
	Units   []Unit   `json:"units"`
	Manager *Manager `json:"manager,omitempty"`
	// RatesReady is false until some unit has been read twice: a CPU share
	// needs two readings of the cgroup's counter a window apart.
	RatesReady bool `json:"ratesReady"`
}

// The properties the list reads for every loaded unit, in one `systemctl
// show` over all of them. Each is a D-Bus property of the unit's cgroup or
// its last run, so this costs PID 1 a few milliseconds a unit and nothing is
// read from /proc.
var readingProps = []string{
	"Id", "Type", "ActiveState", "InvocationID", "MainPID",
	"MemoryCurrent", "TasksCurrent", "CPUUsageNSec",
	"NRestarts", "Result", "ExecMainCode", "ExecMainStatus",
	"ActiveEnterTimestampMonotonic", "StateChangeTimestampMonotonic",
	"FragmentPath",
}

const (
	// An inactive unit's properties change only when it runs, so one read
	// again within this of its last is answered from that read; a oneshot a
	// timer fired and finished between two polls is seen by the next re-read.
	idleReread = time.Minute
	// The unit files change on enable, disable and daemon-reload, which this
	// package runs and forgets the cache for, and on the same commands run in
	// a shell, which a list read is at most this late to show.
	unitFilesFor = 30 * time.Second
)

// readings is the state the list keeps between reads: each unit's last
// CPU counter, its recent windows, and inactive units' last properties.
type readings struct {
	mu      sync.Mutex
	cpu     map[string]cpuSample
	history map[string][]UnitPoint
	idle    map[string]idleRead
	files   map[string]string
	filesAt time.Time
}

type cpuSample struct {
	invocation string
	nsec       uint64
	at         time.Time
	rate       float64
	ready      bool
}

type idleRead struct {
	props map[string]string
	at    time.Time
}

// Inventory lists every service unit with its live readings and the
// manager's state. The readings are best-effort: a systemd that will not
// answer `show` still has its units listed, without figures.
func (s *Systemd) Inventory(ctx context.Context) (*Inventory, error) {
	units, _, err := s.ListInstalled(ctx)
	if err != nil {
		return nil, err
	}
	uptime, _ := host.UptimeWithContext(ctx)
	now := time.Now()
	props := s.unitReadings(ctx, units, now)
	ready := false
	s.state.mu.Lock()
	for i := range units {
		p, ok := props[units[i].Name]
		if !ok {
			continue
		}
		s.applyReading(&units[i], p, uptime, now)
		ready = ready || units[i].CPUReady
	}
	s.prune(units)
	s.state.mu.Unlock()
	return &Inventory{Units: units, Manager: s.manager(ctx, uptime, now), RatesReady: ready}, nil
}

// unitReadings reads the properties of every loaded unit, reusing an inactive
// unit's last read while it stays inactive and is younger than idleReread.
func (s *Systemd) unitReadings(ctx context.Context, units []Unit, now time.Time) map[string]map[string]string {
	out := make(map[string]map[string]string, len(units))
	ask := make([]string, 0, len(units))
	s.state.mu.Lock()
	for _, u := range units {
		if u.LoadState != "loaded" || ValidateName(u.Name) != nil {
			continue
		}
		if last, ok := s.state.idle[u.Name]; ok && u.ActiveState == "inactive" &&
			last.props["ActiveState"] == "inactive" && now.Sub(last.at) < idleReread {
			out[u.Name] = last.props
			continue
		}
		ask = append(ask, u.Name)
	}
	s.state.mu.Unlock()
	if len(ask) == 0 {
		return out
	}
	args := append([]string{"show", "--no-pager", "-p", strings.Join(readingProps, ","), "--"}, ask...)
	res, err := run(ctx, 20*time.Second, "systemctl", args...)
	if err != nil || res.ExitCode != 0 {
		return out
	}
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	if s.state.idle == nil {
		s.state.idle = map[string]idleRead{}
	}
	for _, record := range parseShowRecords(res.Stdout) {
		name := record["Id"]
		if name == "" {
			continue
		}
		out[name] = record
		if record["ActiveState"] == "inactive" {
			s.state.idle[name] = idleRead{props: record, at: now}
		} else {
			delete(s.state.idle, name)
		}
	}
	return out
}

// parseShowRecords splits `systemctl show` over several units into one map
// per unit; the records are separated by a blank line.
func parseShowRecords(stdout string) []map[string]string {
	var records []map[string]string
	current := map[string]string{}
	for _, line := range strings.Split(stdout, "\n") {
		if strings.TrimSpace(line) == "" {
			if len(current) > 0 {
				records = append(records, current)
				current = map[string]string{}
			}
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			current[k] = v
		}
	}
	if len(current) > 0 {
		records = append(records, current)
	}
	return records
}

// applyReading fills a listed unit from its `show` properties. The caller
// holds the state lock.
func (s *Systemd) applyReading(u *Unit, p map[string]string, uptime uint64, now time.Time) {
	u.Type = p["Type"]
	u.MainPID = atoi(p["MainPID"])
	u.Restarts = atoi(p["NRestarts"])
	u.Result = p["Result"]
	if u.Fragment == "" {
		u.Fragment = p["FragmentPath"]
	}
	u.ExitCode, u.ExitStatus = exitOf(p)
	// A stopped or failed unit's last activation is history, not how long
	// it has been up; when it stopped is ChangedAt.
	u.SinceUnix = 0
	if mono := monotonic(p["ActiveEnterTimestampMonotonic"]); mono > 0 && u.ActiveState != "inactive" && u.ActiveState != "failed" {
		u.SinceUnix = activeSinceUnix(mono, uptime, now).Unix()
	}
	if mono := monotonic(p["StateChangeTimestampMonotonic"]); mono > 0 {
		u.ChangedAt = activeSinceUnix(mono, uptime, now).Unix()
	}
	memory, _ := counter(p["MemoryCurrent"])
	u.Memory = memory
	tasks, _ := counter(p["TasksCurrent"])
	u.Tasks = int(tasks)
	if u.ActiveState == "inactive" || u.ActiveState == "failed" {
		return
	}
	nsec, ok := counter(p["CPUUsageNSec"])
	if !ok {
		return
	}
	u.CPUPercent, u.CPUReady = s.measure(u.Name, p["InvocationID"], nsec, memory, now)
}

// measure turns a unit's cumulative CPU counter into a share of one core
// over the window since its last reading, and keeps the window as a point of
// its history. A new invocation is a new cgroup whose counter starts again,
// so it starts a new window rather than measuring a negative one. The
// caller holds the state lock.
func (s *Systemd) measure(name, invocation string, nsec, memory uint64, now time.Time) (float64, bool) {
	if s.state.cpu == nil {
		s.state.cpu = map[string]cpuSample{}
		s.state.history = map[string][]UnitPoint{}
	}
	last, ok := s.state.cpu[name]
	if !ok || last.invocation != invocation || nsec < last.nsec {
		s.state.cpu[name] = cpuSample{invocation: invocation, nsec: nsec, at: now}
		return 0, false
	}
	elapsed := now.Sub(last.at)
	if elapsed < minRateWindow {
		return last.rate, last.ready
	}
	rate := float64(nsec-last.nsec) / float64(elapsed.Nanoseconds()) * 100
	s.state.cpu[name] = cpuSample{invocation: invocation, nsec: nsec, at: now, rate: rate, ready: true}
	points := s.state.history[name]
	if len(points) == 0 || now.Unix()-points[len(points)-1].At >= int64(historyStep/time.Second) {
		points = append(points, UnitPoint{At: now.Unix(), CPU: rate, Memory: memory})
		if len(points) > historySize {
			points = points[len(points)-historySize:]
		}
		s.state.history[name] = points
	}
	return rate, true
}

// History is the unit's recent measured windows, oldest first.
func (s *Systemd) History(name string) []UnitPoint {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	return append([]UnitPoint(nil), s.state.history[name]...)
}

// prune forgets units that are no longer listed. The caller holds the lock.
func (s *Systemd) prune(units []Unit) {
	listed := make(map[string]bool, len(units))
	for _, u := range units {
		listed[u.Name] = true
	}
	for name := range s.state.cpu {
		if !listed[name] {
			delete(s.state.cpu, name)
			delete(s.state.history, name)
		}
	}
	for name := range s.state.idle {
		if !listed[name] {
			delete(s.state.idle, name)
		}
	}
}

// manager reads systemd's own version and state. Nil when it will not say.
func (s *Systemd) manager(ctx context.Context, uptime uint64, now time.Time) *Manager {
	// Not FinishTimestamp for how long the boot took: a job that never
	// finishes holds it open, and one host read a day and a half.
	res, err := run(ctx, 10*time.Second, "systemctl", "show", "--no-pager",
		"-p", "Version,SystemState")
	if err != nil || res.ExitCode != 0 {
		return nil
	}
	records := parseShowRecords(res.Stdout)
	if len(records) == 0 {
		return nil
	}
	p := records[0]
	m := &Manager{Version: p["Version"], State: p["SystemState"]}
	if uptime > 0 {
		m.BootedAt = now.Add(-time.Duration(uptime) * time.Second).Unix()
	}
	return m
}

// counter reads a cgroup counter property. systemd prints "[not set]" when
// the accounting is off and, on older releases, the all-ones value.
func counter(value string) (uint64, bool) {
	n, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	if err != nil || n == ^uint64(0) {
		return 0, false
	}
	return n, true
}

func monotonic(value string) uint64 {
	n, _ := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	return n
}

// exitOf is how the unit's main process last ended: ExecMainCode is the
// kernel's CLD_* code, and ExecMainStatus the exit status or, when killed,
// the signal's number.
func exitOf(p map[string]string) (string, int) {
	status := atoi(p["ExecMainStatus"])
	switch p["ExecMainCode"] {
	case "1":
		return "exited", status
	case "2":
		return "killed", status
	case "3":
		return "dumped", status
	}
	return "", 0
}

// Detail is Show with the unit's recent windows, the one read of a unit that
// also measures it: an open sheet polls faster than the list.
func (s *Systemd) Detail(ctx context.Context, name string) (*Unit, map[string]string, error) {
	u, props, err := s.Show(ctx, name)
	if err != nil {
		return nil, nil, err
	}
	uptime, _ := host.UptimeWithContext(ctx)
	now := time.Now()
	s.state.mu.Lock()
	s.applyReading(u, props, uptime, now)
	s.state.mu.Unlock()
	u.History = s.History(name)
	return u, props, nil
}
