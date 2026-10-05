package procs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// PM2Daemon is one account's PM2 as a thing with state of its own: whether
// what it runs now would come back after a reboot, and when that was last
// made true. The process list cannot say this — a daemon with three online
// apps and no saved list restores nothing.
type PM2Daemon struct {
	Account string `json:"account"`
	Home    string `json:"home"`
	// When `pm2 save` last wrote the resurrection list, if ever.
	DumpSavedAt *time.Time `json:"dumpSavedAt,omitempty"`
	// Whether a boot hook exists for this account. PM2's own `startup`
	// writes a pm2-<user>.service; the dashboard reports it and never
	// installs it, because that step is platform-specific and PM2 owns it.
	StartupUnit string `json:"startupUnit,omitempty"`
}

// Daemons lists the accounts a PM2 can be driven for, with their boot facts.
func (p *PM2) Daemons() []PM2Daemon {
	out := []PM2Daemon{}
	for _, home := range discoverPM2Homes() {
		account, err := pm2Account(home.home)
		if err != nil {
			continue
		}
		daemon := PM2Daemon{Account: account.Username, Home: home.home}
		if st, err := os.Stat(filepath.Join(home.home, ".pm2", "dump.pm2")); err == nil && !st.IsDir() {
			at := st.ModTime().UTC()
			daemon.DumpSavedAt = &at
		}
		daemon.StartupUnit = pm2StartupUnit(account.Username)
		out = append(out, daemon)
	}
	return out
}

// pm2StartupUnit reports the systemd unit `pm2 startup` installs for an
// account, if it is present. /etc is the host's, so the check is a stat.
func pm2StartupUnit(account string) string {
	unit := "pm2-" + account + ".service"
	for _, dir := range []string{"/etc/systemd/system", "/lib/systemd/system", "/usr/lib/systemd/system"} {
		if st, err := os.Stat(filepath.Join(dir, unit)); err == nil && !st.IsDir() {
			return unit
		}
	}
	return ""
}

// ControlAll runs one lifecycle verb over everything an account's daemon
// manages. Restricted to the verbs PM2 itself accepts with `all`, and to one
// account at a time: "restart everything" across accounts is a sentence
// nobody means literally.
func (p *PM2) ControlAll(ctx context.Context, account string, action PM2Action) (*CommandResult, error) {
	switch action {
	case PM2Start, PM2Stop, PM2Restart, PM2Reload:
	default:
		return nil, fmt.Errorf("pm2 %s cannot be applied to every process", action)
	}
	home, err := p.homeFor(account)
	if err != nil {
		return nil, err
	}
	p.invalidate()
	res, err := runPM2Host(ctx, home, 120*time.Second, string(action), "all")
	p.invalidate()
	return res, err
}

// Scale sets the number of instances of one cluster-mode application.
func (p *PM2) Scale(ctx context.Context, name, daemon string, id, instances int) (*CommandResult, error) {
	if instances < 1 || instances > 128 {
		return nil, fmt.Errorf("instances must be between 1 and 128")
	}
	p.invalidate()
	proc, err := p.target(ctx, name, daemon, id)
	if err != nil {
		return nil, err
	}
	if proc.ExecMode != "cluster_mode" && proc.ExecMode != "cluster" {
		return nil, fmt.Errorf("%s runs in fork mode; only a cluster-mode application can be scaled", proc.Name)
	}
	home, err := p.homeFor(proc.DaemonID)
	if err != nil {
		return nil, err
	}
	res, err := runPM2Host(ctx, home, 120*time.Second, "scale", proc.Name, strconv.Itoa(instances))
	p.invalidate()
	return res, err
}

// PM2StartRequest is what `pm2 start` needs, as fields rather than a command
// line. Every value becomes one argv element; nothing here reaches a shell.
type PM2StartRequest struct {
	Account string `json:"account"`
	// Script is the file to run, or an ecosystem file (`*.config.js`,
	// `*.json`, `*.yml`), which carries its own settings and ignores the rest.
	Script      string `json:"script"`
	Name        string `json:"name,omitempty"`
	Cwd         string `json:"cwd,omitempty"`
	Interpreter string `json:"interpreter,omitempty"`
	// Instances: 0 or 1 runs one fork; 2 and above runs a cluster of that
	// size; -1 runs one instance per CPU.
	Instances int  `json:"instances,omitempty"`
	Watch     bool `json:"watch,omitempty"`
	// MaxMemoryRestart is PM2's own size syntax: "300M", "1G".
	MaxMemoryRestart string   `json:"maxMemoryRestart,omitempty"`
	Args             []string `json:"args,omitempty"`
}

var (
	pm2Interpreter = regexp.MustCompile(`^(?:node|bash|sh|python|python3|ruby|perl|php|none|/[A-Za-z0-9._/+-]+)$`)
	pm2MemorySize  = regexp.MustCompile(`^[0-9]{1,6}[KMG]?$`)
)

// isEcosystemFile reports whether PM2 would read the path as a process file
// rather than run it. PM2 decides by extension, so this does too.
func isEcosystemFile(script string) bool {
	base := strings.ToLower(filepath.Base(script))
	if strings.HasSuffix(base, ".json") || strings.HasSuffix(base, ".yml") || strings.HasSuffix(base, ".yaml") {
		return true
	}
	for _, suffix := range []string{".config.js", ".config.cjs", ".config.mjs"} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	return false
}

// pm2StartArgs turns a request into the argument vector, or says which field
// is wrong. Separate from running it so the shape is testable without a
// daemon.
func pm2StartArgs(req PM2StartRequest) ([]string, error) {
	script := strings.TrimSpace(req.Script)
	if !filepath.IsAbs(script) || strings.ContainsRune(script, 0) {
		return nil, fmt.Errorf("script must be an absolute path on the host")
	}
	args := []string{"start", filepath.Clean(script)}
	if isEcosystemFile(script) {
		if req.Name != "" {
			if err := ValidateName(req.Name); err != nil {
				return nil, err
			}
			args = append(args, "--only", req.Name)
		}
		return args, nil
	}
	if req.Name != "" {
		if err := ValidateName(req.Name); err != nil {
			return nil, err
		}
		args = append(args, "--name", req.Name)
	}
	if req.Cwd != "" {
		if !filepath.IsAbs(req.Cwd) || strings.ContainsRune(req.Cwd, 0) {
			return nil, fmt.Errorf("working directory must be an absolute path on the host")
		}
		args = append(args, "--cwd", filepath.Clean(req.Cwd))
	}
	if req.Interpreter != "" {
		if !pm2Interpreter.MatchString(req.Interpreter) {
			return nil, fmt.Errorf("interpreter must be a known runtime or an absolute path")
		}
		args = append(args, "--interpreter", req.Interpreter)
	}
	switch {
	case req.Instances == -1:
		args = append(args, "-i", "max")
	case req.Instances > 1:
		if req.Instances > 128 {
			return nil, fmt.Errorf("instances must be at most 128")
		}
		args = append(args, "-i", strconv.Itoa(req.Instances))
	case req.Instances < -1:
		return nil, fmt.Errorf("instances must be -1 (one per CPU), or a count")
	}
	if req.Watch {
		args = append(args, "--watch")
	}
	if req.MaxMemoryRestart != "" {
		if !pm2MemorySize.MatchString(req.MaxMemoryRestart) {
			return nil, fmt.Errorf("memory limit must be a size such as 300M or 1G")
		}
		args = append(args, "--max-memory-restart", req.MaxMemoryRestart)
	}
	if len(req.Args) > 0 {
		for _, a := range req.Args {
			if strings.ContainsRune(a, 0) {
				return nil, fmt.Errorf("arguments may not contain NUL")
			}
		}
		args = append(args, "--")
		args = append(args, req.Args...)
	}
	return args, nil
}

// Start registers and runs a new application under one account's daemon.
func (p *PM2) Start(ctx context.Context, req PM2StartRequest) (*CommandResult, error) {
	args, err := pm2StartArgs(req)
	if err != nil {
		return nil, err
	}
	home, err := p.homeFor(req.Account)
	if err != nil {
		return nil, err
	}
	p.invalidate()
	res, err := runPM2Host(ctx, home, 120*time.Second, args...)
	p.invalidate()
	return res, err
}

// homeFor resolves an account name to its discovered PM2 home. Only accounts
// the filesystem discovery found qualify: an arbitrary username must never
// choose an execution identity.
func (p *PM2) homeFor(account string) (pm2Home, error) {
	if err := ValidateName(account); err != nil {
		return pm2Home{}, err
	}
	for _, home := range discoverPM2Homes() {
		if info, err := pm2Account(home.home); err == nil && info.Username == account {
			return home, nil
		}
	}
	return pm2Home{}, fmt.Errorf("no PM2 installation for account %q", account)
}

func (p *PM2) invalidate() {
	p.mu.Lock()
	p.cachedAt = time.Time{}
	p.mu.Unlock()
}
