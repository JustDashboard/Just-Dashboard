package procs

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

type PM2Process struct {
	LogsAvailable         bool    `json:"logsAvailable"`
	LogsUnavailableReason string  `json:"logsUnavailableReason,omitempty"`
	ID                    int     `json:"id"`
	DaemonID              string  `json:"daemonId"`
	Name                  string  `json:"name"`
	Namespace             string  `json:"namespace"`
	Status                string  `json:"status"`
	PID                   int     `json:"pid"`
	CPU                   float64 `json:"cpu"`
	Memory                int64   `json:"memory"`
	Restarts              int     `json:"restarts"`
	Unstable              int     `json:"unstableRestarts"`
	UptimeMS              int64   `json:"uptimeMs"`
	ExecMode              string  `json:"execMode"`
	Instances             int     `json:"instances"`
	ScriptPath            string  `json:"scriptPath"`
	CWD                   string  `json:"cwd"`
	NodeVersion           string  `json:"nodeVersion"`
	OutLogPath            string  `json:"outLogPath"`
	ErrLogPath            string  `json:"errLogPath"`
	User                  string  `json:"user"`
	Watching              bool    `json:"watching"`
	// The settings PM2 was given for this process, so the detail sheet can
	// say how it is run without a second `pm2 describe` round trip.
	Interpreter      string `json:"interpreter,omitempty"`
	Version          string `json:"version,omitempty"`
	Autorestart      bool   `json:"autorestart"`
	MaxMemoryRestart int64  `json:"maxMemoryRestart,omitempty"`
	CreatedAtMS      int64  `json:"createdAtMs,omitempty"`
}

// pm2Raw mirrors the subset of `pm2 jlist` output we consume. PM2's schema is
// wide and unstable at the edges, so only stable fields are mapped.
type pm2Raw struct {
	PID       int    `json:"pid"`
	Name      string `json:"name"`
	PMID      int    `json:"pm_id"`
	Namespace string `json:"namespace"`
	Monit     struct {
		Memory int64   `json:"memory"`
		CPU    float64 `json:"cpu"`
	} `json:"monit"`
	PM2Env struct {
		Status           string `json:"status"`
		PMUptime         int64  `json:"pm_uptime"`
		RestartTime      int    `json:"restart_time"`
		UnstableRestarts int    `json:"unstable_restarts"`
		ExecMode         string `json:"exec_mode"`
		Instances        any    `json:"instances"`
		PMExecPath       string `json:"pm_exec_path"`
		PMCwd            string `json:"pm_cwd"`
		NodeVersion      string `json:"node_version"`
		PMOutLogPath     string `json:"pm_out_log_path"`
		PMErrLogPath     string `json:"pm_err_log_path"`
		Username         string `json:"username"`
		Watch            any    `json:"watch"`
		ExecInterpreter  string `json:"exec_interpreter"`
		Version          string `json:"version"`
		Autorestart      any    `json:"autorestart"`
		MaxMemoryRestart any    `json:"max_memory_restart"`
		CreatedAt        int64  `json:"created_at"`
	} `json:"pm2_env"`
}

type PM2 struct {
	mu       sync.Mutex
	cached   []PM2Process
	cachedAt time.Time
}

func NewPM2() *PM2 { return &PM2{} }

// Available reports whether any PM2 installation can be driven from here:
// a binary on this process's PATH, one on the host's, or a per-user daemon
// home bind-mounted from the host.
func (p *PM2) Available() bool {
	if binaryExists("pm2") {
		return true
	}
	if len(discoverPM2Homes()) > 0 {
		return true
	}
	return hostexec.Available("pm2")
}

func (p *PM2) List(ctx context.Context) ([]PM2Process, error) {
	// Both the PM2 tab and the live process inventory ask for this list. One
	// `pm2 jlist` every few seconds is enough; spawning two CLI clients on every
	// poll costs more than the process table itself and they return the same
	// daemon snapshot.
	p.mu.Lock()
	defer p.mu.Unlock()
	if time.Since(p.cachedAt) < 3*time.Second {
		return append([]PM2Process(nil), p.cached...), nil
	}
	now := time.Now().UnixMilli()
	// Non-nil: a nil slice serialises as JSON null, and the PM2 tab reads
	// `.length` off it.
	out := make([]PM2Process, 0)
	if homes := discoverPM2Homes(); len(homes) > 0 {
		// One daemon per account, each asked in its own home. A stopped
		// daemon for one account must not hide another's running processes.
		var firstErr error
		for _, home := range homes {
			res, err := runPM2Host(ctx, home, 20*time.Second, "jlist")
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			account, accountErr := pm2Account(home.home)
			if accountErr != nil {
				return nil, accountErr
			}
			procs, err := parsePM2List([]byte(res.Stdout), now, account.Username)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			out = append(out, procs...)
		}
		if len(out) == 0 && firstErr != nil {
			return nil, firstErr
		}
	} else {
		return out, nil
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].User < out[j].User
	})
	p.cached = append(p.cached[:0], out...)
	p.cachedAt = time.Now()
	return append([]PM2Process(nil), out...), nil
}

// parsePM2List converts one `pm2 jlist` document into processes. fallbackUser
// names the trusted account whose daemon answered. Daemon-supplied usernames
// never select execution credentials.
func parsePM2List(data []byte, nowMilli int64, fallbackUser string) ([]PM2Process, error) {
	var raw []pm2Raw
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse pm2 jlist output: %w", err)
	}
	out := make([]PM2Process, 0, len(raw))
	for _, r := range raw {
		proc := PM2Process{
			ID: r.PMID, Name: r.Name, Namespace: r.Namespace,
			Status: r.PM2Env.Status, PID: r.PID,
			CPU: r.Monit.CPU, Memory: r.Monit.Memory,
			Restarts: r.PM2Env.RestartTime, Unstable: r.PM2Env.UnstableRestarts,
			ExecMode: r.PM2Env.ExecMode, ScriptPath: r.PM2Env.PMExecPath,
			CWD: r.PM2Env.PMCwd, NodeVersion: r.PM2Env.NodeVersion,
			OutLogPath: r.PM2Env.PMOutLogPath, ErrLogPath: r.PM2Env.PMErrLogPath,
			User: fallbackUser, DaemonID: fallbackUser,
			Interpreter: r.PM2Env.ExecInterpreter, Version: r.PM2Env.Version,
			CreatedAtMS: r.PM2Env.CreatedAt,
		}
		if r.PM2Env.Status == "online" && r.PM2Env.PMUptime > 0 {
			proc.UptimeMS = nowMilli - r.PM2Env.PMUptime
		}
		proc.Instances = coerceInt(r.PM2Env.Instances)
		proc.Watching = coerceBool(r.PM2Env.Watch)
		// PM2 defaults autorestart to on and only writes the key when set, so
		// an absent value is "yes" rather than "no".
		proc.Autorestart = r.PM2Env.Autorestart == nil || coerceBool(r.PM2Env.Autorestart)
		proc.MaxMemoryRestart = int64(coerceInt(r.PM2Env.MaxMemoryRestart))
		out = append(out, proc)
	}
	return out, nil
}

// PM2 reports these fields as number, string or bool depending on version and
// how the ecosystem file was written.
func coerceInt(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	}
	return 0
}

func coerceBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case []any:
		return len(t) > 0
	case string:
		return t != "" && t != "false"
	}
	return false
}

type PM2Action string

const (
	PM2Start   PM2Action = "start"
	PM2Stop    PM2Action = "stop"
	PM2Restart PM2Action = "restart"
	PM2Reload  PM2Action = "reload"
	PM2Delete  PM2Action = "delete"
	// Reset zeroes the restart counters, which is how "it restarted 40 times
	// last week" stops masking "it restarted once today".
	PM2Reset PM2Action = "reset"
	// Flush truncates the process's stdout and stderr files in place.
	PM2Flush PM2Action = "flush"
)

func (p *PM2) Control(ctx context.Context, name string, action PM2Action) (*CommandResult, error) {
	return p.ControlTarget(ctx, name, "", -1, action)
}

func (p *PM2) ControlTarget(ctx context.Context, name, daemon string, id int, action PM2Action) (*CommandResult, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	switch action {
	case PM2Start, PM2Stop, PM2Restart, PM2Reload, PM2Delete, PM2Reset, PM2Flush:
	default:
		return nil, fmt.Errorf("unknown pm2 action %q", action)
	}
	// Resolve mutations against a fresh daemon listing; a poll cache may refer
	// to an id the user has removed and reassigned in the meantime.
	p.mu.Lock()
	p.cachedAt = time.Time{}
	p.mu.Unlock()
	proc, err := p.target(ctx, name, daemon, id)
	if err != nil {
		return nil, err
	}
	for _, home := range discoverPM2Homes() {
		account, err := pm2Account(home.home)
		if err != nil || account.Username != proc.DaemonID {
			continue
		}
		// Flush is addressed by name: every PM2 release matches a flush
		// target by name, while matching by id arrived later, and the
		// cluster instances a name covers share their log files anyway.
		target := strconv.Itoa(proc.ID)
		if action == PM2Flush {
			target = proc.Name
		}
		result, err := runPM2Host(ctx, home, 60*time.Second, string(action), target)
		if err == nil {
			p.mu.Lock()
			p.cachedAt = time.Time{}
			p.mu.Unlock()
		}
		return result, err
	}
	return nil, fmt.Errorf("PM2 daemon is no longer available")
}

func selectPM2Target(list []PM2Process, name, daemon string, id int) (PM2Process, error) {
	var matched []PM2Process
	for _, proc := range list {
		if proc.Name == name && (daemon == "" || proc.DaemonID == daemon) && (id < 0 || proc.ID == id) {
			matched = append(matched, proc)
		}
	}
	if len(matched) == 0 {
		return PM2Process{}, fmt.Errorf("PM2 process is no longer available")
	}
	if len(matched) != 1 {
		return PM2Process{}, fmt.Errorf("PM2 process name is ambiguous; select its account and process id")
	}
	return matched[0], nil
}

func (p *PM2) target(ctx context.Context, name, daemon string, id int) (PM2Process, error) {
	list, err := p.List(ctx)
	if err != nil {
		return PM2Process{}, err
	}
	return selectPM2Target(list, name, daemon, id)
}

// Save persists PM2's current inventory for its startup hook to resurrect on
// boot. It does not install or rewrite the init integration: PM2 owns that
// platform-specific configuration, while this action makes the current list
// match what an existing hook will restore.
func (p *PM2) Save(ctx context.Context) (*CommandResult, error) {
	if homes := discoverPM2Homes(); len(homes) > 0 {
		// Each account's resurrection list is its own: saving only one leaves
		// the others unrestored after a reboot.
		var last *CommandResult
		for _, home := range homes {
			res, err := runPM2Host(ctx, home, 60*time.Second, "save")
			if err != nil {
				return res, err
			}
			last = res
		}
		return last, nil
	}
	if !p.Available() {
		return nil, fmt.Errorf("pm2 %w", ErrNotInstalled)
	}
	return nil, fmt.Errorf("no PM2 account daemon is available")
}

// LogPaths returns the on-disk log files for a process so the log tailer can
// follow them directly, which survives `pm2 logs` being killed and gives the
// same view after a restart.
func (p *PM2) LogPaths(ctx context.Context, name string) (string, string, error) {
	return p.LogPathsTarget(ctx, name, "", -1)
}

func (p *PM2) LogPathsTarget(ctx context.Context, name, daemon string, id int) (string, string, error) {
	proc, err := p.target(ctx, name, daemon, id)
	if err != nil {
		return "", "", err
	}
	return proc.OutLogPath, proc.ErrLogPath, nil
}
