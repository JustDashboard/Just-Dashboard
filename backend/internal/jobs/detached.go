package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// Detached commands: work handed to the host's systemd as a transient unit
// instead of being run as a child of this process.
//
// A child of this process lives in this container's cgroup, and everything in
// that cgroup is killed when the container stops. A package upgrade causes
// exactly that by itself: docker-ce's maintainer script restarts the daemon,
// the daemon stops this container, and dpkg dies halfway through configuring
// the package that restarted it. Every apt command after that fails with "dpkg
// was interrupted" until somebody runs `dpkg --configure -a` at a terminal.
//
// A transient unit belongs to the host's systemd, so it carries on whatever
// happens to the dashboard. Its output goes to files rather than to a pipe
// this process holds, because a pipe whose reader has gone kills the writer
// with SIGPIPE just as surely. The job follows the files, and a record beside
// them says what the job was, so a dashboard that comes back while the unit is
// still running — or after it has finished — takes it back under the same id
// and the console that was watching reconnects to it.

// unitPrefix names every unit this starts. The job id completes it, which is
// what lets a restarted dashboard find the unit from the record alone.
const unitPrefix = "just-dashboard-job-"

// detachedID is the shape of a job id that may name a unit and a file:
// newID's hex, or its timestamp fallback.
var detachedID = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)

const (
	// followEvery is how often the transcripts are read. A console that
	// updates five times a second reads as live; a faster poll buys nothing.
	followEvery = 200 * time.Millisecond
	// askEvery is how many reads go by between asking systemd whether the
	// unit is still running — once a second, because each ask is a process.
	askEvery = 5
	// lostAfter is how many asks in a row may fail before the job gives up on
	// a unit it can no longer see.
	lostAfter = 30
)

// units is what a detached command needs from the host's service manager.
// Tests substitute one that runs the command as an ordinary child.
type units interface {
	// available reports whether commands can be handed over at all, with
	// their transcripts kept in dir.
	available(dir string) bool
	start(ctx context.Context, unit, description, stdout, stderr string, env, argv []string) error
	state(ctx context.Context, unit string) (unitState, error)
	stop(ctx context.Context, unit string) error
	// forget drops what systemd keeps of a failed unit once its code is read.
	forget(ctx context.Context, unit string)
}

// unitState is what systemd says about a unit's main process.
type unitState struct {
	running bool
	code    int
	signal  int
	result  string
}

// err is how the unit ended when that is not an exit code: a code is the
// caller's to judge, the way a command's is.
func (st unitState) err() error {
	switch st.result {
	case "", "success", "exit-code":
		return nil
	case "signal", "core-dump":
		return fmt.Errorf("stopped by signal %d", st.signal)
	default:
		return fmt.Errorf("systemd ended it: %s", st.result)
	}
}

// parseUnitState reads `systemctl show` for the properties systemdUnits asks
// for. A unit systemd no longer knows reads as inactive and successful, which
// is right: a transient unit that succeeded is collected as soon as it stops,
// and one that failed stays loaded until forget.
func parseUnitState(out string) unitState {
	props := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			props[k] = v
		}
	}
	st := unitState{result: props["Result"]}
	switch props["ActiveState"] {
	case "active", "activating", "deactivating", "reloading", "refreshing":
		st.running = true
	}
	status, _ := strconv.Atoi(props["ExecMainStatus"])
	switch props["ExecMainCode"] {
	case "1": // CLD_EXITED
		st.code = status
	case "2", "3": // CLD_KILLED, CLD_DUMPED
		st.code, st.signal = -1, status
	default:
		if st.err() != nil {
			st.code = -1
		}
	}
	return st
}

// systemdUnits hands commands to the host's systemd through systemd-run.
type systemdUnits struct {
	once sync.Once
	ok   bool
}

func (u *systemdUnits) available(dir string) bool {
	u.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// The unit opens its transcript by the path this process reads it
		// from, so the directory has to be the same one on both sides — the
		// data directory is mounted at its own path for exactly that.
		u.ok = os.Geteuid() == 0 &&
			hostexec.CommandOnHost(ctx, "test", "-d", "/run/systemd/system").Run() == nil &&
			hostexec.AvailableOnHost("systemd-run") &&
			hostexec.CommandOnHost(ctx, "test", "-d", dir).Run() == nil
	})
	return u.ok
}

func (*systemdUnits) start(ctx context.Context, unit, description, stdout, stderr string, env, argv []string) error {
	args := []string{
		"--unit", unit, "--description", description, "--quiet",
		"--property", "StandardOutput=file:" + stdout,
		"--property", "StandardError=file:" + stderr,
	}
	for _, kv := range env {
		args = append(args, "--setenv", kv)
	}
	// Through env, as the uninstaller's unit does: older systemd wants an
	// absolute path, and this resolves the name on the host's own PATH.
	args = append(args, "--", "/usr/bin/env")
	// systemd expands $VAR and ${VAR} in a unit's command line, so an
	// argument that is "$HOME" would arrive as the unit's HOME, or as
	// nothing. $$ is its literal dollar.
	for _, arg := range argv {
		args = append(args, strings.ReplaceAll(arg, "$", "$$"))
	}
	out, err := hostexec.CommandOnHost(ctx, "systemd-run", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

func (*systemdUnits) state(ctx context.Context, unit string) (unitState, error) {
	out, err := hostexec.CommandOnHost(ctx, "systemctl", "show", unit,
		"--property=ActiveState,Result,ExecMainCode,ExecMainStatus").Output()
	if err != nil {
		return unitState{}, err
	}
	return parseUnitState(string(out)), nil
}

func (*systemdUnits) stop(ctx context.Context, unit string) error {
	return hostexec.CommandOnHost(ctx, "systemctl", "stop", "--no-block", unit).Run()
}

func (*systemdUnits) forget(ctx context.Context, unit string) {
	_ = hostexec.CommandOnHost(ctx, "systemctl", "reset-failed", unit).Run()
}

// detachedRecord is what a restarted dashboard needs to take a unit back.
type detachedRecord struct {
	Job      Job       `json:"job"`
	Shown    []string  `json:"shown"`
	Deadline time.Time `json:"deadline"`
	// BootID tells a unit that is gone because it finished from one that is
	// gone because the server restarted under it.
	BootID string `json:"bootId"`
}

// detached is one detached command's unit and files.
type detached struct {
	unit, record, stdout, stderr string
}

func (m *Manager) detachedFor(id string) detached {
	base := filepath.Join(m.detachDir, id)
	return detached{
		unit:   unitPrefix + id + ".service",
		record: base + ".json",
		stdout: base + ".out",
		stderr: base + ".err",
	}
}

// remove deletes what the command left, once its outcome is on the job.
func (d detached) remove() {
	for _, path := range []string{d.record, d.stdout, d.stderr} {
		_ = os.Remove(path)
	}
}

// Detach lets RunDetached hand commands to the host's systemd, keeping their
// transcripts in dir. Without it every command runs as a child, as Run does.
func (m *Manager) Detach(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	m.detachDir = dir
	return nil
}

// detachable reports whether RunDetached can hand a command over.
func (m *Manager) detachable() bool {
	return m.detachDir != "" && m.units.available(m.detachDir)
}

// Adopt takes back the detached commands an earlier run of this process
// started: still running, or finished while nothing was watching. Each comes
// back as a job with its old id, so a console that was attached to it
// reconnects to the same thing. begin brackets each one's run, as the runner
// that started it bracketed its own.
func (m *Manager) Adopt(begin func() (done func())) {
	if m.detachDir == "" {
		return
	}
	entries, err := os.ReadDir(m.detachDir)
	if err != nil {
		return
	}
	boot := bootID()
	for _, entry := range entries {
		id, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok || !detachedID.MatchString(id) {
			continue
		}
		d := m.detachedFor(id)
		var rec detachedRecord
		raw, err := os.ReadFile(d.record)
		if err == nil {
			err = json.Unmarshal(raw, &rec)
		}
		if err != nil || len(rec.Shown) == 0 {
			if m.log != nil {
				m.log.Warn("dropping an unreadable detached job record", "id", id, "err", err)
			}
			d.remove()
			continue
		}
		job := rec.Job
		job.ID, job.Status, job.ExitCode, job.Error, job.EndedAt, job.Lines = id, StatusRunning, 0, "", nil, 0
		ctx, cancel := context.WithDeadline(context.Background(), rec.Deadline)
		e := m.register(job, cancel, false)
		go m.run(ctx, cancel, e, func(ctx context.Context, out Emitter) error {
			done := begin()
			defer done()
			em := out.(*emitter)
			em.Status("$ %s", strings.Join(rec.Shown, " "))
			name := rec.Shown[0]
			if rec.BootID != "" && boot != "" && rec.BootID != boot {
				em.replay(d)
				d.remove()
				return fmt.Errorf("the server restarted before %s finished, so it stopped where the lines above end", name)
			}
			code, err := em.follow(ctx, d, func() {
				em.Status("The dashboard restarted while this ran. It carried on as %s; the lines above were read back from its transcript.", d.unit)
			})
			em.entry.setExitCode(code)
			if err != nil {
				return err
			}
			if code != 0 {
				return fmt.Errorf("%s exited %d — the last lines above say why", name, code)
			}
			return nil
		})
	}
}

// RunDetached is RunEnv for a command that must not die with this process.
//
// shown is what the console prints, when the argv that runs is a wrapper
// around the command the operator asked for. Where the host's systemd cannot
// take the command, it runs as a child exactly as RunEnv runs it, and the
// console says why.
func (em *emitter) RunDetached(ctx context.Context, env, shown []string, name string, args ...string) (int, error) {
	argv := append([]string{name}, args...)
	if len(shown) == 0 {
		shown = argv
	}
	m := em.manager
	if m == nil || !m.detachable() {
		return em.RunCmd(hostCommand(ctx, env, name, args), shown)
	}
	job := em.entry.snapshotJob()
	if !detachedID.MatchString(job.ID) {
		return em.RunCmd(hostCommand(ctx, env, name, args), shown)
	}
	em.Status("$ %s", strings.Join(shown, " "))
	em.lastCode = -1
	defer func() { em.entry.setExitCode(em.lastCode) }()

	d := m.detachedFor(job.ID)
	startCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	err := m.units.start(startCtx, d.unit, job.Title, d.stdout, d.stderr, env, argv)
	cancel()
	if err != nil {
		em.Status("systemd would not run this (%v), so it runs under the dashboard, where a Docker restart can interrupt it.", err)
		return em.runCmd(hostCommand(ctx, env, name, args), shown[0])
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(defaultTimeout)
	}
	// Written once the unit exists, so a record never names one that does
	// not. A restart between the two leaves a unit nobody adopts, which still
	// finishes — the part that matters.
	rec, _ := json.Marshal(detachedRecord{Job: job, Shown: shown, Deadline: deadline, BootID: bootID()})
	if err := os.WriteFile(d.record, rec, 0o600); err != nil && m.log != nil {
		m.log.Warn("detached job record not written; a restart will not take this job back", "id", job.ID, "err", err)
	}
	code, err := em.follow(ctx, d, nil)
	em.lastCode = code
	return code, err
}

func hostCommand(ctx context.Context, env []string, name string, args []string) *exec.Cmd {
	cmd := hostexec.CommandOnHost(ctx, name, args...)
	if len(env) > 0 {
		cmd.Env = append(cmd.Environ(), env...)
	}
	return cmd
}

// follow streams a unit's transcripts until it stops, and returns its code.
// caughtUp, when set, runs once after the first read — an adopted job's
// moment to say where the read-back ends.
//
// Cancelling the job stops the unit, and follow still waits for it to end: a
// job reported cancelled while its apt is still running is the orphan Cancel
// exists to prevent.
func (em *emitter) follow(ctx context.Context, d detached, caughtUp func()) (int, error) {
	m := em.manager
	defer func() {
		forgetCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		m.units.forget(forgetCtx, d.unit)
		d.remove()
	}()
	out := &transcript{path: d.stdout, stream: "stdout"}
	errs := &transcript{path: d.stderr, stream: "stderr"}
	defer out.close()
	defer errs.close()
	emit := em.entry.appendLine

	tick := time.NewTicker(followEvery)
	defer tick.Stop()
	done := ctx.Done()
	lost := 0
	for n := 0; ; n++ {
		out.read(emit, false)
		errs.read(emit, false)
		if n == 0 && caughtUp != nil {
			caughtUp()
		}
		if n%askEvery == 0 {
			askCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			st, err := m.units.state(askCtx, d.unit)
			cancel()
			switch {
			case err != nil:
				if lost++; lost >= lostAfter {
					return -1, fmt.Errorf("lost track of %s: %w", d.unit, err)
				}
			case !st.running:
				out.read(emit, true)
				errs.read(emit, true)
				if ctx.Err() != nil {
					return st.code, ctx.Err()
				}
				return st.code, st.err()
			default:
				lost = 0
			}
		}
		select {
		case <-done:
			stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			if err := m.units.stop(stopCtx, d.unit); err != nil {
				em.Status("Could not stop %s: %v", d.unit, err)
			}
			cancel()
			done = nil
		case <-tick.C:
		}
	}
}

// replay emits whatever a unit's transcripts hold, for one that cannot be
// followed any further.
func (em *emitter) replay(d detached) {
	for _, t := range []*transcript{{path: d.stdout, stream: "stdout"}, {path: d.stderr, stream: "stderr"}} {
		t.read(em.entry.appendLine, true)
		t.close()
	}
}

// transcript follows one output file of a detached command.
type transcript struct {
	path, stream string
	f            *os.File
	partial      []byte
}

// read emits every complete line written since the last read. final also
// emits a last line that has no newline, because nothing will finish it now.
func (t *transcript) read(emit func(stream, text string), final bool) {
	if t.f == nil {
		// systemd creates the file as it starts the command, which can be a
		// moment after systemd-run returns.
		f, err := os.Open(t.path)
		if err != nil {
			return
		}
		t.f = f
	}
	buf := make([]byte, 32*1024)
	for {
		n, err := t.f.Read(buf)
		t.partial = append(t.partial, buf[:n]...)
		if err != nil || n == 0 {
			break
		}
	}
	rest := t.partial
	for {
		i := bytes.IndexByte(rest, '\n')
		if i < 0 {
			break
		}
		emit(t.stream, strings.TrimRight(string(rest[:i]), "\r"))
		rest = rest[i+1:]
	}
	if len(rest) >= maxLineBytes || (final && len(rest) > 0) {
		emit(t.stream, strings.TrimRight(string(rest), "\r"))
		rest = nil
	}
	t.partial = append([]byte(nil), rest...)
}

func (t *transcript) close() {
	if t.f != nil {
		t.f.Close()
		t.f = nil
	}
}

// bootID is the kernel's, which this container shares with the host.
func bootID() string {
	b, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
