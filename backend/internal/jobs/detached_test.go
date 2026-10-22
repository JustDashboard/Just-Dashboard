package jobs

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// localUnits stands in for the host's systemd: each "unit" is an ordinary
// child writing to the files a real unit would, and it outlives whichever
// Manager started it, as a unit outlives the dashboard.
type localUnits struct {
	mu      sync.Mutex
	units   map[string]*localUnit
	stopped []string
}

type localUnit struct {
	cmd  *exec.Cmd
	done chan struct{}
	code int
}

func newLocalUnits() *localUnits { return &localUnits{units: map[string]*localUnit{}} }

func (u *localUnits) available(string) bool { return true }

func (u *localUnits) start(_ context.Context, unit, _, stdout, stderr string, env, argv []string) error {
	o, err := os.Create(stdout)
	if err != nil {
		return err
	}
	e, err := os.Create(stderr)
	if err != nil {
		return err
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr, cmd.Env = o, e, append(os.Environ(), env...)
	if err := cmd.Start(); err != nil {
		return err
	}
	lu := &localUnit{cmd: cmd, done: make(chan struct{})}
	u.mu.Lock()
	u.units[unit] = lu
	u.mu.Unlock()
	go func() {
		_ = cmd.Wait()
		lu.code = cmd.ProcessState.ExitCode()
		o.Close()
		e.Close()
		close(lu.done)
	}()
	return nil
}

// finished records a unit that ended while nothing was watching it.
func (u *localUnits) finished(unit string, code int) {
	done := make(chan struct{})
	close(done)
	u.mu.Lock()
	u.units[unit] = &localUnit{done: done, code: code}
	u.mu.Unlock()
}

func (u *localUnits) state(_ context.Context, unit string) (unitState, error) {
	u.mu.Lock()
	lu := u.units[unit]
	u.mu.Unlock()
	if lu == nil {
		// What systemd answers for a unit it has collected after success.
		return unitState{result: "success"}, nil
	}
	select {
	case <-lu.done:
		if lu.code != 0 {
			return unitState{code: lu.code, result: "exit-code"}, nil
		}
		return unitState{result: "success"}, nil
	default:
		return unitState{running: true}, nil
	}
}

func (u *localUnits) stop(_ context.Context, unit string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.stopped = append(u.stopped, unit)
	if lu := u.units[unit]; lu != nil && lu.cmd != nil {
		return lu.cmd.Process.Kill()
	}
	return nil
}

func (u *localUnits) forget(context.Context, string) {}

func detachedManager(t *testing.T, dir string, units *localUnits) *Manager {
	t.Helper()
	m := New(slog.Default())
	t.Cleanup(m.Shutdown)
	m.units = units
	if err := m.Detach(dir); err != nil {
		t.Fatal(err)
	}
	return m
}

func lineTexts(lines []Line) string {
	texts := make([]string, 0, len(lines))
	for _, l := range lines {
		texts = append(texts, l.Stream+":"+l.Text)
	}
	return strings.Join(texts, "\n")
}

func TestDetachedCommandStreamsBothStreamsAndItsCode(t *testing.T) {
	units := newLocalUnits()
	m := detachedManager(t, t.TempDir(), units)
	job := m.Start(Spec{Kind: "test", Title: "detached", Timeout: 10 * time.Second},
		func(ctx context.Context, out Emitter) error {
			code, err := out.RunDetached(ctx, []string{"GREETING=hello"}, nil,
				"sh", "-c", `echo "$GREETING"; echo warned >&2; printf 'no newline'; exit 3`)
			if err == nil && code != 3 {
				t.Errorf("code = %d, want 3", code)
			}
			return err
		})
	done := awaitJob(t, m, job.ID)
	_, lines, _ := m.Get(job.ID)
	got := lineTexts(lines)
	for _, want := range []string{"stdout:hello", "stderr:warned", "stdout:no newline", "status:$ sh -c"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q from:\n%s", want, got)
		}
	}
	if done.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3", done.ExitCode)
	}
	if len(units.units) != 1 {
		t.Errorf("the command ran as %d units, want 1", len(units.units))
	}
	if entries, _ := os.ReadDir(m.detachDir); len(entries) != 0 {
		t.Errorf("a finished command left %d files behind", len(entries))
	}
}

// The console prints what the operator asked for, not the wrapper that runs it.
func TestDetachedCommandShowsWhatItWasAskedToShow(t *testing.T) {
	m := detachedManager(t, t.TempDir(), newLocalUnits())
	job := m.Start(Spec{Kind: "test", Title: "shown", Timeout: 10 * time.Second},
		func(ctx context.Context, out Emitter) error {
			_, err := out.RunDetached(ctx, nil, []string{"apt-get", "upgrade"}, "sh", "-c", "true")
			return err
		})
	awaitJob(t, m, job.ID)
	_, lines, _ := m.Get(job.ID)
	if lines[0].Text != "$ apt-get upgrade" {
		t.Errorf("heading = %q", lines[0].Text)
	}
}

// The record is what a restarted dashboard reads, so it has to exist for as
// long as the unit runs.
func TestDetachedCommandIsRecordedWhileItRuns(t *testing.T) {
	m := detachedManager(t, t.TempDir(), newLocalUnits())
	job := m.Start(Spec{Kind: "packages.install", Title: "Installing htop", Target: "htop", StartedBy: "tester", Timeout: 10 * time.Second},
		func(ctx context.Context, out Emitter) error {
			_, err := out.RunDetached(ctx, nil, nil, "sh", "-c", "sleep 1")
			return err
		})
	d := m.detachedFor(job.ID)
	deadline := time.Now().Add(5 * time.Second)
	var rec detachedRecord
	for {
		raw, err := os.ReadFile(d.record)
		if err == nil && json.Unmarshal(raw, &rec) == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no record was written for a running detached command")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if rec.Job.ID != job.ID || rec.Job.Kind != "packages.install" || rec.Job.StartedBy != "tester" || rec.BootID != bootID() {
		t.Errorf("record = %+v", rec)
	}
	if rec.Deadline.Before(time.Now()) || rec.Deadline.After(time.Now().Add(11*time.Second)) {
		t.Errorf("deadline %v is not the job's", rec.Deadline)
	}
	awaitJob(t, m, job.ID)
	if _, err := os.Stat(d.record); !os.IsNotExist(err) {
		t.Errorf("record still there after the job ended: %v", err)
	}
}

// The case this exists for: the dashboard goes away mid-command (docker-ce's
// postinst restarting Docker), the command carries on, and the dashboard that
// comes back follows it to the end under the same job id.
func TestARestartedDashboardTakesBackARunningCommand(t *testing.T) {
	dir := t.TempDir()
	units := newLocalUnits()
	before := detachedManager(t, dir, units)
	const id = "0123456789abcdef"
	d := before.detachedFor(id)
	if err := units.start(context.Background(), d.unit, "", d.stdout, d.stderr, nil,
		[]string{"sh", "-c", "echo before; sleep 0.5; echo after; echo broke >&2; exit 100"}); err != nil {
		t.Fatal(err)
	}
	started := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
	writeRecord(t, d, detachedRecord{
		Job:      Job{ID: id, Kind: "updates.apply", Title: "Upgrading packages with apt-get", StartedBy: "tester", StartedAt: started, Status: StatusRunning},
		Shown:    []string{"apt-get", "-y", "upgrade"},
		Deadline: time.Now().Add(time.Minute),
		BootID:   bootID(),
	})

	after := detachedManager(t, dir, units)
	brackets := 0
	after.Adopt(func() func() { brackets++; return func() { brackets-- } })
	adopted, _, ok := after.Get(id)
	if !ok {
		t.Fatal("the running command was not taken back")
	}
	if adopted.Kind != "updates.apply" || adopted.StartedBy != "tester" || !adopted.StartedAt.Equal(started) {
		t.Errorf("adopted job = %+v", adopted)
	}
	done := awaitJob(t, after, id)
	if done.Status != StatusFailed || done.ExitCode != 100 || !strings.Contains(done.Error, "apt-get exited 100") {
		t.Errorf("job = %+v", done)
	}
	_, lines, _ := after.Get(id)
	got := lineTexts(lines)
	for _, want := range []string{"status:$ apt-get -y upgrade", "stdout:before", "status:The dashboard restarted", "stdout:after", "stderr:broke"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q from:\n%s", want, got)
		}
	}
	if brackets != 0 {
		t.Errorf("begin was not paired with its done: %d", brackets)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("an adopted command left %d files behind", len(entries))
	}
}

// A unit that finished while no dashboard was running still reports how.
func TestACommandThatFinishedWhileNobodyWatchedIsReported(t *testing.T) {
	dir := t.TempDir()
	units := newLocalUnits()
	m := detachedManager(t, dir, units)
	const id = "fedcba9876543210"
	d := m.detachedFor(id)
	if err := os.WriteFile(d.stdout, []byte("Setting up docker-ce\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	units.finished(d.unit, 0)
	writeRecord(t, d, detachedRecord{
		Job: Job{ID: id, Kind: "updates.apply", Title: "Upgrading"}, Shown: []string{"apt-get", "upgrade"},
		Deadline: time.Now().Add(time.Minute), BootID: bootID(),
	})
	m.Adopt(func() func() { return func() {} })
	done := awaitJob(t, m, id)
	if done.Status != StatusSucceeded || done.ExitCode != 0 {
		t.Errorf("job = %+v", done)
	}
	if _, lines, _ := m.Get(id); !strings.Contains(lineTexts(lines), "stdout:Setting up docker-ce") {
		t.Errorf("transcript not read back:\n%s", lineTexts(lines))
	}
}

// A unit is gone after a reboot too, and that must not read as success.
func TestACommandCutOffByARebootIsAFailure(t *testing.T) {
	dir := t.TempDir()
	m := detachedManager(t, dir, newLocalUnits())
	const id = "00000000000000aa"
	d := m.detachedFor(id)
	if err := os.WriteFile(d.stdout, []byte("Unpacking linux-image\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeRecord(t, d, detachedRecord{
		Job: Job{ID: id, Kind: "updates.apply"}, Shown: []string{"apt-get", "upgrade"},
		Deadline: time.Now().Add(time.Minute), BootID: "a-boot-that-is-not-this-one",
	})
	m.Adopt(func() func() { return func() {} })
	done := awaitJob(t, m, id)
	if done.Status != StatusFailed || !strings.Contains(done.Error, "server restarted") {
		t.Errorf("job = %+v", done)
	}
	if _, lines, _ := m.Get(id); !strings.Contains(lineTexts(lines), "Unpacking linux-image") {
		t.Errorf("transcript not read back:\n%s", lineTexts(lines))
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("left %d files behind", len(entries))
	}
}

func TestAnUnreadableRecordIsDropped(t *testing.T) {
	dir := t.TempDir()
	m := detachedManager(t, dir, newLocalUnits())
	d := m.detachedFor("0000000000000bad")
	if err := os.WriteFile(d.record, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.Adopt(func() func() { return func() {} })
	if len(m.List()) != 0 {
		t.Error("a job was made from a record that does not parse")
	}
	if _, err := os.Stat(d.record); !os.IsNotExist(err) {
		t.Error("the unreadable record was kept, to be tried again on every start")
	}
}

// Cancelling stops the unit and waits for it, rather than reporting a job
// cancelled while its command carries on.
func TestCancellingADetachedCommandStopsItsUnit(t *testing.T) {
	units := newLocalUnits()
	m := detachedManager(t, t.TempDir(), units)
	job := m.Start(Spec{Kind: "test", Title: "sleep", Timeout: time.Minute},
		func(ctx context.Context, out Emitter) error {
			_, err := out.RunDetached(ctx, nil, nil, "sleep", "30")
			return err
		})
	unit := m.detachedFor(job.ID).unit
	deadline := time.Now().Add(5 * time.Second)
	for {
		units.mu.Lock()
		_, started := units.units[unit]
		units.mu.Unlock()
		if started {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the unit never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	m.Cancel(job.ID)
	// Cancel marks the job at once; the runner returns once the unit is gone.
	for deadline := time.Now().Add(10 * time.Second); ; {
		done, _, _ := m.Get(job.ID)
		if done.EndedAt != nil {
			if done.Status != StatusCancelled {
				t.Errorf("status = %q", done.Status)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the job never ended")
		}
		time.Sleep(10 * time.Millisecond)
	}
	units.mu.Lock()
	defer units.mu.Unlock()
	if len(units.stopped) != 1 || units.stopped[0] != unit {
		t.Errorf("stopped = %v, want %s", units.stopped, unit)
	}
	select {
	case <-units.units[unit].done:
	default:
		t.Error("the job ended while its unit was still running")
	}
}

// Without a host systemd to hand it to, a detached command is an ordinary
// child, as every job command was before.
func TestDetachedCommandRunsAsAChildWithoutSystemd(t *testing.T) {
	m := New(slog.Default())
	t.Cleanup(m.Shutdown)
	job := m.Start(Spec{Kind: "test", Title: "child", Timeout: 10 * time.Second},
		func(ctx context.Context, out Emitter) error {
			_, err := out.RunDetached(ctx, nil, nil, "sh", "-c", "echo here; exit 4")
			return err
		})
	done := awaitJob(t, m, job.ID)
	if done.ExitCode != 4 {
		t.Errorf("ExitCode = %d, want 4", done.ExitCode)
	}
	if _, lines, _ := m.Get(job.ID); !strings.Contains(lineTexts(lines), "stdout:here") {
		t.Errorf("lines:\n%s", lineTexts(lines))
	}
}

func TestParseUnitState(t *testing.T) {
	for _, c := range []struct {
		name, out string
		want      unitState
		err       bool
	}{
		{"running", "ActiveState=active\nResult=success\nExecMainCode=0\nExecMainStatus=0\n", unitState{running: true, result: "success"}, false},
		{"stopping", "ActiveState=deactivating\nResult=success\n", unitState{running: true, result: "success"}, false},
		{"exited non-zero", "ActiveState=failed\nResult=exit-code\nExecMainCode=1\nExecMainStatus=100\n", unitState{code: 100, result: "exit-code"}, false},
		{"collected after success", "ActiveState=inactive\nResult=success\nExecMainCode=0\nExecMainStatus=0\n", unitState{result: "success"}, false},
		{"killed", "ActiveState=failed\nResult=signal\nExecMainCode=2\nExecMainStatus=9\n", unitState{code: -1, signal: 9, result: "signal"}, true},
		{"never started", "ActiveState=failed\nResult=resources\nExecMainCode=0\nExecMainStatus=0\n", unitState{code: -1, result: "resources"}, true},
	} {
		got := parseUnitState(c.out)
		if got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
		if (got.err() != nil) != c.err {
			t.Errorf("%s: err = %v", c.name, got.err())
		}
	}
}

// A line is emitted once its newline arrives, however the writes split it.
func TestTranscriptHoldsAPartialLineUntilItEnds(t *testing.T) {
	path := t.TempDir() + "/out"
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var got []string
	emit := func(_, text string) { got = append(got, text) }
	tr := &transcript{path: path, stream: "stdout"}
	defer tr.close()
	f.WriteString("Unpack")
	tr.read(emit, false)
	if len(got) != 0 {
		t.Fatalf("a partial line was emitted: %q", got)
	}
	f.WriteString("ing foo\r\nSetting")
	tr.read(emit, false)
	f.WriteString(" up foo")
	tr.read(emit, true)
	if strings.Join(got, "|") != "Unpacking foo|Setting up foo" {
		t.Errorf("lines = %q", got)
	}
}

func writeRecord(t *testing.T, d detached, rec detachedRecord) {
	t.Helper()
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(d.record, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}
