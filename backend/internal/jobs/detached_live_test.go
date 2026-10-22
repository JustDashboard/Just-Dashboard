package jobs

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// These run real transient units on the host's systemd, uniquely named and
// running only sh and sleep, with transcripts under the test's directory.
func liveDetached(t *testing.T) *Manager {
	t.Helper()
	if os.Getenv("JD_DETACHED_JOBS_LIVE") != "1" {
		t.Skip("set JD_DETACHED_JOBS_LIVE=1, as root, to run commands as real host units")
	}
	m := New(slog.Default())
	t.Cleanup(m.Shutdown)
	if err := m.Detach(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if !m.detachable() {
		t.Skip("no host systemd this process can hand units to")
	}
	return m
}

func liveUnitLoaded(t *testing.T, unit string) string {
	t.Helper()
	out, _ := hostexec.CommandOnHost(context.Background(), "systemctl", "show", unit, "--property=LoadState,ActiveState").Output()
	return strings.TrimSpace(string(out))
}

func TestLiveDetachedCommandRunsAsAHostUnit(t *testing.T) {
	m := liveDetached(t)
	job := m.Start(Spec{Kind: "test", Title: "Just Dashboard live detached test", Timeout: time.Minute},
		func(ctx context.Context, out Emitter) error {
			_, err := out.RunDetached(ctx, []string{"JD_LIVE=from-the-unit"}, nil,
				"sh", "-c", `echo "$JD_LIVE"; echo "arg:$1"; echo "$(cat /proc/self/cgroup)"; echo warned >&2; exit 7`, "sh", "${HOME}$PATH")
			return err
		})
	unit := m.detachedFor(job.ID).unit
	done := awaitJob(t, m, job.ID)
	if done.ExitCode != 7 {
		t.Errorf("ExitCode = %d, want 7", done.ExitCode)
	}
	_, lines, _ := m.Get(job.ID)
	got := lineTexts(lines)
	// The cgroup line is the point: the command ran in the unit's, not ours.
	// And arguments arrive as written, not as systemd's expansion of them.
	for _, want := range []string{"stdout:from-the-unit", "stdout:arg:${HOME}$PATH", "stderr:warned", unit} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q from:\n%s", want, got)
		}
	}
	if state := liveUnitLoaded(t, unit); !strings.Contains(state, "LoadState=not-found") {
		t.Errorf("the failed unit was not forgotten: %s", state)
	}
}

func TestLiveCancellingStopsTheHostUnit(t *testing.T) {
	m := liveDetached(t)
	job := m.Start(Spec{Kind: "test", Title: "Just Dashboard live cancel test", Timeout: time.Minute},
		func(ctx context.Context, out Emitter) error {
			_, err := out.RunDetached(ctx, nil, nil, "sleep", "300")
			return err
		})
	unit := m.detachedFor(job.ID).unit
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(liveUnitLoaded(t, unit), "ActiveState=active"); {
		if time.Now().After(deadline) {
			t.Fatalf("%s never started", unit)
		}
		time.Sleep(50 * time.Millisecond)
	}
	m.Cancel(job.ID)
	for deadline := time.Now().Add(30 * time.Second); ; {
		if j, _, _ := m.Get(job.ID); j.EndedAt != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the job never ended")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if state := liveUnitLoaded(t, unit); strings.Contains(state, "ActiveState=active") {
		t.Errorf("the unit outlived its cancelled job: %s", state)
	}
}

// The process that started the unit is gone; a new Manager reading the same
// directory follows the unit to its end.
func TestLiveAdoptedUnitIsFollowedToItsEnd(t *testing.T) {
	m := liveDetached(t)
	id := newID()
	d := m.detachedFor(id)
	ctx := context.Background()
	if err := m.units.start(ctx, d.unit, "Just Dashboard live adopt test", d.stdout, d.stderr, nil,
		[]string{"sh", "-c", "echo before; sleep 2; echo after; exit 3"}); err != nil {
		t.Fatal(err)
	}
	writeRecord(t, d, detachedRecord{
		Job: Job{ID: id, Kind: "test", Title: "adopted"}, Shown: []string{"sh"},
		Deadline: time.Now().Add(time.Minute), BootID: bootID(),
	})
	after := New(slog.Default())
	t.Cleanup(after.Shutdown)
	if err := after.Detach(m.detachDir); err != nil {
		t.Fatal(err)
	}
	after.Adopt(func() func() { return func() {} })
	done := awaitJob(t, after, id)
	if done.ExitCode != 3 || !strings.Contains(done.Error, "exited 3") {
		t.Errorf("job = %+v", done)
	}
	if _, lines, _ := after.Get(id); !strings.Contains(lineTexts(lines), "stdout:after") {
		t.Errorf("lines:\n%s", lineTexts(lines))
	}
}
