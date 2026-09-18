package procs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"github.com/shirou/gopsutil/v4/host"
)

type Unit struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	LoadState   string `json:"loadState"`
	ActiveState string `json:"activeState"`
	SubState    string `json:"subState"`
	Following   string `json:"following"`
	UnitFile    string `json:"unitFileState"`
	Enabled     bool   `json:"enabled"`
	MainPID     int    `json:"mainPid,omitempty"`
	Memory      uint64 `json:"memoryBytes,omitempty"`
	Tasks       int    `json:"tasks,omitempty"`
	SinceUnix   int64  `json:"activeSince,omitempty"`
	Fragment    string `json:"fragmentPath,omitempty"`
	Result      string `json:"result,omitempty"`
	Restarts    int    `json:"restarts,omitempty"`
}

type Systemd struct{}

func NewSystemd() *Systemd { return &Systemd{} }

func (s *Systemd) Available() bool { return binaryExists("systemctl") }

// List returns every service unit, loaded or not. `--all` matters: a failed or
// inactive unit is exactly what an operator opens this page to find, and the
// default listing hides them.
func (s *Systemd) List(ctx context.Context) ([]Unit, error) {
	if !s.Available() {
		return nil, fmt.Errorf("systemctl %w", ErrNotInstalled)
	}
	res, err := run(ctx, 30*time.Second, "systemctl",
		"list-units", "--type=service", "--all", "--no-pager", "--no-legend", "--output=json")
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Unit        string `json:"unit"`
		Load        string `json:"load"`
		Active      string `json:"active"`
		Sub         string `json:"sub"`
		Description string `json:"description"`
	}
	// systemctl exits 0 while printing nothing useful when it declines to talk
	// to the host manager at all. Reporting that as a JSON parse failure sends
	// the reader looking for a malformed unit; say what actually happened.
	if strings.TrimSpace(res.Stdout) == "" {
		detail := strings.TrimSpace(res.Stderr)
		if detail == "" {
			detail = strings.TrimSpace(res.Stdout)
		}
		if detail == "" {
			detail = "no output"
		}
		return nil, fmt.Errorf("systemctl returned no unit list: %s", detail)
	}
	if err := json.Unmarshal([]byte(res.Stdout), &raw); err != nil {
		return nil, fmt.Errorf("parse systemctl output: %w", err)
	}
	enabled := s.enabledStates(ctx)
	out := make([]Unit, 0, len(raw))
	for _, r := range raw {
		u := Unit{
			Name: r.Unit, Description: r.Description,
			LoadState: r.Load, ActiveState: r.Active, SubState: r.Sub,
		}
		if state, ok := enabled[r.Unit]; ok {
			u.UnitFile = state
			u.Enabled = state == "enabled" || state == "enabled-runtime" || state == "static"
		}
		out = append(out, u)
	}
	// Failed units first: they are why someone opened this page.
	sort.Slice(out, func(i, j int) bool {
		rank := func(u Unit) int {
			switch u.ActiveState {
			case "failed":
				return 0
			case "activating", "deactivating":
				return 1
			case "active":
				return 2
			default:
				return 3
			}
		}
		if rank(out[i]) != rank(out[j]) {
			return rank(out[i]) < rank(out[j])
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func (s *Systemd) enabledStates(ctx context.Context) map[string]string {
	states := map[string]string{}
	res, err := run(ctx, 30*time.Second, "systemctl",
		"list-unit-files", "--type=service", "--no-pager", "--no-legend", "--output=json")
	if err != nil {
		return states
	}
	var raw []struct {
		UnitFile string `json:"unit_file"`
		State    string `json:"state"`
	}
	if json.Unmarshal([]byte(res.Stdout), &raw) != nil {
		return states
	}
	for _, r := range raw {
		states[r.UnitFile] = r.State
	}
	return states
}

// Show pulls the detailed properties for one unit. `systemctl show` emits
// key=value lines, which is a stable interface across systemd versions where
// the JSON output for show is not.
func (s *Systemd) Show(ctx context.Context, name string) (*Unit, map[string]string, error) {
	if err := ValidateName(name); err != nil {
		return nil, nil, err
	}
	res, err := run(ctx, 20*time.Second, "systemctl", "show", name, "--no-pager")
	if err != nil {
		return nil, nil, err
	}
	props := map[string]string{}
	for _, line := range strings.Split(res.Stdout, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok {
			props[k] = v
		}
	}
	u := &Unit{
		Name:        props["Id"],
		Description: props["Description"],
		LoadState:   props["LoadState"],
		ActiveState: props["ActiveState"],
		SubState:    props["SubState"],
		Following:   props["Following"],
		UnitFile:    props["UnitFileState"],
		Fragment:    props["FragmentPath"],
		Result:      props["Result"],
	}
	u.Enabled = u.UnitFile == "enabled" || u.UnitFile == "enabled-runtime" || u.UnitFile == "static"
	u.MainPID = atoi(props["MainPID"])
	u.Tasks = atoi(props["TasksCurrent"])
	u.Restarts = atoi(props["NRestarts"])
	if mem, err := strconv.ParseUint(props["MemoryCurrent"], 10, 64); err == nil {
		u.Memory = mem
	}
	// The monotonic value is microseconds since boot, so combine it with host
	// uptime rather than pretending the human-formatted timestamp is an epoch
	// number (which left every "Active since" empty). This also avoids parsing
	// whatever timezone and locale systemctl happened to print.
	if monotonic, err := strconv.ParseUint(props["ActiveEnterTimestampMonotonic"], 10, 64); err == nil && monotonic > 0 {
		if uptime, upErr := host.UptimeWithContext(ctx); upErr == nil {
			u.SinceUnix = activeSinceUnix(monotonic, uptime, time.Now()).Unix()
		}
	} else if started, ok := parseSystemdTimestamp(props["ActiveEnterTimestamp"]); ok {
		u.SinceUnix = started.Unix()
	}
	if u.Name == "" {
		u.Name = name
	}
	return u, props, nil
}

func activeSinceUnix(monotonicUS, uptimeSeconds uint64, now time.Time) time.Time {
	age := time.Duration(uptimeSeconds)*time.Second - time.Duration(monotonicUS)*time.Microsecond
	if age < 0 {
		age = 0
	}
	return now.Add(-age)
}

func parseSystemdTimestamp(value string) (time.Time, bool) {
	for _, layout := range []string{
		"Mon 2006-01-02 15:04:05 MST",
		"Mon 2006-01-02 15:04:05 -0700",
		time.RFC3339,
	} {
		if parsed, err := time.Parse(layout, strings.TrimSpace(value)); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func atoi(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}

type UnitAction string

const (
	UnitStart   UnitAction = "start"
	UnitStop    UnitAction = "stop"
	UnitRestart UnitAction = "restart"
	UnitReload  UnitAction = "reload"
	UnitEnable  UnitAction = "enable"
	UnitDisable UnitAction = "disable"
	// ResetFailed clears a unit's failed state without starting it, so a
	// service that was fixed by hand stops being counted as broken.
	UnitResetFailed UnitAction = "reset-failed"
)

func (s *Systemd) Control(ctx context.Context, name string, action UnitAction) (*CommandResult, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	switch action {
	case UnitStart, UnitStop, UnitRestart, UnitReload, UnitEnable, UnitDisable, UnitResetFailed:
	default:
		return nil, fmt.Errorf("unknown systemd action %q", action)
	}
	return run(ctx, 90*time.Second, "systemctl", string(action), name)
}

// DaemonReload asks systemd to re-read its unit files. It is the step after
// editing a unit in the file manager that nobody remembers, and until it runs
// the unit page keeps describing the old file.
func (s *Systemd) DaemonReload(ctx context.Context) (*CommandResult, error) {
	if !s.Available() {
		return nil, fmt.Errorf("systemctl %w", ErrNotInstalled)
	}
	return run(ctx, 90*time.Second, "systemctl", "daemon-reload")
}

// JournalEntry is one record from journalctl's JSON output. The interesting
// fields are normalised out of the __-prefixed noise the journal emits.
type JournalEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Message   string    `json:"message"`
	Priority  int       `json:"priority"`
	Unit      string    `json:"unit,omitempty"`
	PID       string    `json:"pid,omitempty"`
	Hostname  string    `json:"hostname,omitempty"`
	Syslog    string    `json:"syslogIdentifier,omitempty"`
}

// JournalCommand builds a journalctl invocation. JSON output is used rather
// than the default text so message boundaries and priorities survive
// multi-line log records intact.
// JournalOptions is everything the unified log viewer can ask the journal
// for. It exists because the journal is the one source where narrowing the
// query is not an optimisation: `journalctl -n 300` hands back the last three
// hundred *records*, and filtering those in this process for the one level the
// operator asked about routinely leaves an empty page in front of a journal
// full of matches. Pushing the window down to journalctl is what makes "the
// last 300 errors" mean what it says.
type JournalOptions struct {
	Unit       string
	Identifier string
	// Lines caps the tail. Zero means no cap, which is what a search over an
	// explicit time window wants — there the window is the bound.
	Lines  int
	Follow bool
	Since  string
	Until  string
	// MaxPriority narrows to `-p 0..n` when non-negative. Only a maximum is
	// pushed down, never the exact set: journalctl takes a range and the
	// operator's chips are a set, so the range is the cheap over-approximation
	// and the exact test still happens here.
	MaxPriority int
	Boot        bool
	Kernel      bool
}

// JournalCommand is the tail form kept for the per-unit views, which want the
// last n records of one unit and nothing else.
func JournalCommand(ctx context.Context, unit string, lines int, follow bool, since string) (*exec.Cmd, error) {
	if lines <= 0 || lines > 20000 {
		lines = 300
	}
	return JournalCommandOpts(ctx, JournalOptions{
		Unit: unit, Lines: lines, Follow: follow, Since: since, MaxPriority: -1,
	})
}

func JournalCommandOpts(ctx context.Context, opts JournalOptions) (*exec.Cmd, error) {
	args := []string{"--output=json", "--no-pager"}
	if opts.Unit != "" {
		if err := ValidateName(opts.Unit); err != nil {
			return nil, err
		}
		args = append(args, "-u", opts.Unit)
	}
	if opts.Identifier != "" {
		if err := ValidateName(opts.Identifier); err != nil {
			return nil, err
		}
		args = append(args, "-t", opts.Identifier)
	}
	if opts.Lines > 20000 {
		opts.Lines = 20000
	}
	if opts.Lines > 0 {
		args = append(args, "-n", strconv.Itoa(opts.Lines))
	}
	for _, spec := range []struct {
		flag  string
		value string
	}{{"--since", opts.Since}, {"--until", opts.Until}} {
		if spec.value == "" {
			continue
		}
		if !safeTimeSpec.MatchString(spec.value) {
			return nil, fmt.Errorf("%w: %s=%q", ErrInvalidName, spec.flag, spec.value)
		}
		args = append(args, spec.flag, spec.value)
	}
	if opts.MaxPriority >= 0 && opts.MaxPriority <= 7 {
		args = append(args, "-p", "0.."+strconv.Itoa(opts.MaxPriority))
	}
	if opts.Boot {
		args = append(args, "-b")
	}
	if opts.Kernel {
		args = append(args, "-k")
	}
	if opts.Follow {
		args = append(args, "-f")
	}
	// The journal is host state, not container state: this image mounts
	// /var/log but not the volatile /run/log/journal where the current boot's
	// records live, so a container-local journalctl returns the previous
	// boot's flushed entries and follows nothing new. Crossing into the host's
	// namespaces reads the live journal; on a bare-metal install this runs
	// directly and behaves as before.
	cmd := hostexec.CommandOnHost(ctx, "journalctl", args...)
	// Same host-PID-namespace chroot detection that run() works around.
	cmd.Env = append(os.Environ(), "SYSTEMD_IGNORE_CHROOT=1")
	return cmd, nil
}

// journalctl accepts free-form time expressions ("2 hours ago", "today",
// timestamps). The permitted character set is narrow enough to stay safe while
// still covering the expressions people actually type.
var safeTimeSpec = regexp.MustCompile(`^[A-Za-z0-9 :\-+.]{1,64}$`)

// ParseJournalLine converts one JSON record into a JournalEntry.
func ParseJournalLine(line []byte) (JournalEntry, bool) {
	var raw map[string]any
	if err := json.Unmarshal(line, &raw); err != nil {
		return JournalEntry{}, false
	}
	e := JournalEntry{
		Message:  journalString(raw["MESSAGE"]),
		Unit:     journalString(raw["_SYSTEMD_UNIT"]),
		PID:      journalString(raw["_PID"]),
		Hostname: journalString(raw["_HOSTNAME"]),
		Syslog:   journalString(raw["SYSLOG_IDENTIFIER"]),
	}
	if p, err := strconv.Atoi(journalString(raw["PRIORITY"])); err == nil {
		e.Priority = p
	} else {
		e.Priority = 6
	}
	if us, err := strconv.ParseInt(journalString(raw["__REALTIME_TIMESTAMP"]), 10, 64); err == nil {
		e.Timestamp = time.UnixMicro(us).UTC()
	}
	return e, e.Message != ""
}

// A journal MESSAGE is a string for text records and a byte array for binary
// ones; both shapes have to be handled or binary records break the stream.
func journalString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		b := make([]byte, 0, len(t))
		for _, n := range t {
			if f, ok := n.(float64); ok {
				b = append(b, byte(int(f)))
			}
		}
		return string(b)
	}
	return ""
}
