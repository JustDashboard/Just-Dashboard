package netdiag

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
	storedb "github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

func diagnosticService(t *testing.T, runner Runner) (*Service, *storedb.Store) {
	t.Helper()
	db, err := storedb.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := New(NewStore(db.DB), jobs.New(nil), runner)
	if err := s.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			t.Error(err)
		}
		s.jobs.Shutdown()
		db.Close()
	})
	return s, db
}

func awaitRun(t *testing.T, s *Service, id string) Run {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		run, err := s.Get(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if terminal(run.Status) {
			return run
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("diagnostic did not finish")
	return Run{}
}

func TestSavedDiagnosticSurvivesReopenAndRerunIsExplicit(t *testing.T) {
	var calls atomic.Int32
	s, db := diagnosticService(t, func(ctx context.Context, req netsec.ProbeRequest) (*netsec.ProbeResult, error) {
		calls.Add(1)
		return &netsec.ProbeResult{Tool: req.Tool, Target: req.Target, OK: true, Records: []string{"2001:db8::1"}, Duration: "3ms"}, nil
	})
	run, err := s.Create(t.Context(), " IPv6 resolver ", netsec.ProbeRequest{Tool: "dns", Target: "example.test.", Record: "aaaa", Port: 99, Option: "ignored"}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	finished := awaitRun(t, s, run.ID)
	if finished.Status != "completed" || finished.Request.Record != "AAAA" || finished.Request.Port != 0 || finished.Request.Option != "" || finished.StartedAt == nil || finished.EndedAt == nil || len(finished.Stages) != 3 || finished.Stages[2].Outcome != "saved" {
		t.Fatalf("run = %+v", finished)
	}
	reopened := New(NewStore(db.DB), jobs.New(nil), s.runner)
	if err := reopened.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	read, err := reopened.Get(t.Context(), run.ID)
	if err != nil || read.Result == nil || read.Result.Records[0] != "2001:db8::1" || calls.Load() != 1 {
		t.Fatalf("reopen = %+v, %v; calls=%d", read, err, calls.Load())
	}
	rerun, err := reopened.Rerun(t.Context(), run.ID, "second-admin")
	if err != nil {
		t.Fatal(err)
	}
	if rerun.ID == run.ID || rerun.RerunOf != run.ID || rerun.Request != read.Request || rerun.CreatedBy != "second-admin" {
		t.Fatalf("rerun = %+v", rerun)
	}
	awaitRun(t, reopened, rerun.ID)
	if calls.Load() != 2 {
		t.Fatalf("calls=%d", calls.Load())
	}
	reopened.jobs.Shutdown()
}

func TestStartupInterruptsUnfinishedWithoutExecuting(t *testing.T) {
	var calls atomic.Int32
	s, _ := diagnosticService(t, func(context.Context, netsec.ProbeRequest) (*netsec.ProbeResult, error) { calls.Add(1); return nil, nil })
	now := s.now()
	for _, status := range []string{"queued", "running", "cancelling"} {
		run := Run{ID: status, Name: "before restart", Request: netsec.ProbeRequest{Tool: "ping", Target: "example.test"}, Status: status, CreatedAt: now, UpdatedAt: now, Stages: []Stage{{ID: "probe", Status: status}}}
		if err := s.store.insert(t.Context(), run); err != nil {
			t.Fatal(err)
		}
	}
	restarted := New(s.store, jobs.New(nil), s.runner)
	if err := restarted.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"queued", "running", "cancelling"} {
		run, err := restarted.Get(t.Context(), id)
		if err != nil || run.Status != "interrupted" || run.OutcomeSource != "recovery" || run.EndedAt == nil || !strings.Contains(run.Error, "not rerun") || !strings.Contains(run.Error, "not independently observed") {
			t.Fatalf("%s = %+v, %v", id, run, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("startup emitted probe traffic")
	}
	restarted.jobs.Shutdown()
}

func TestCancellationWaitsForCleanupAndPreservesSavedName(t *testing.T) {
	started, draining, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	s, _ := diagnosticService(t, func(ctx context.Context, _ netsec.ProbeRequest) (*netsec.ProbeResult, error) {
		close(started)
		<-ctx.Done()
		close(draining)
		<-release
		return nil, ctx.Err()
	})
	run, err := s.Create(t.Context(), "original", netsec.ProbeRequest{Tool: "ping", Target: "2001:db8::1"}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err := s.Save(t.Context(), run.ID, "saved during execution"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Cancel(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	<-draining
	read, _ := s.Get(t.Context(), run.ID)
	job, _, _ := s.jobs.Get(run.JobID)
	if read.Status != "cancelling" || job.Status != jobs.StatusRunning {
		t.Fatalf("premature cancellation: run=%s job=%s", read.Status, job.Status)
	}
	if err := s.Delete(t.Context(), run.ID); !errors.Is(err, ErrRunning) {
		t.Fatalf("delete running=%v", err)
	}
	close(release)
	finished := awaitRun(t, s, run.ID)
	if finished.Status != "cancelled" || finished.OutcomeSource != "context" || finished.Name != "saved during execution" || finished.Scope.Family != "inet6" {
		t.Fatalf("finished = %+v", finished)
	}
	if _, err := s.Cancel(t.Context(), run.ID); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("cancel finished=%v", err)
	}
	if err := s.Delete(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
}

func TestDiagnosticCancellationStopsForkedDescendants(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	s, _ := diagnosticService(t, func(ctx context.Context, _ netsec.ProbeRequest) (*netsec.ProbeResult, error) {
		// Fixed test fixture, never a command assembled from a request.
		cmd := exec.CommandContext(ctx, "/bin/sh", "-c", `trap '' TERM; /bin/sh -c 'trap "" TERM; while :; do sleep 1; done' & echo $! > "$1"; wait`, "sh", pidFile)
		_, err := hostexec.RunGroup(ctx, cmd, 75*time.Millisecond)
		return nil, err
	})
	run, err := s.Create(t.Context(), "process tree", netsec.ProbeRequest{Tool: "ping", Target: "127.0.0.1"}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	var pid int
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(pidFile)
		if err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("forked child never started")
	}
	if _, err := s.Cancel(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	if run := awaitRun(t, s, run.ID); run.Status != "cancelled" {
		t.Fatalf("run=%+v", run)
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("forked descendant %d survived a terminal cancellation", pid)
}

func TestRetentionPersistsAndEvictsOnlyFinishedRuns(t *testing.T) {
	s, _ := diagnosticService(t, func(context.Context, netsec.ProbeRequest) (*netsec.ProbeResult, error) {
		return &netsec.ProbeResult{OK: true}, nil
	})
	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	for i, status := range []string{"completed", "failed", "cancelled", "interrupted", "running"} {
		at := now.Add(time.Duration(i-5) * time.Hour)
		if err := s.store.insert(t.Context(), Run{ID: strconv.Itoa(i), Name: "retained", Status: status, CreatedAt: at, UpdatedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	policy := Retention{MaxRuns: 2, MaxAgeHours: 4}
	if err := s.SetRetention(t.Context(), policy); err != nil {
		t.Fatal(err)
	}
	runs, err := s.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 3 || runs[0].ID != "4" || runs[1].ID != "3" || runs[2].ID != "2" {
		t.Fatalf("retained=%+v", runs)
	}
	if got, err := s.store.retention(t.Context()); err != nil || got != policy {
		t.Fatalf("policy=%+v,%v", got, err)
	}
	now = now.Add(5 * time.Hour)
	if _, err := s.Export(t.Context(), "3"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired export=%v", err)
	}
	if _, err := s.Save(t.Context(), "2", "revive"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired save=%v", err)
	}
	if _, err := s.Get(t.Context(), "4"); err != nil {
		t.Fatalf("running record evicted: %v", err)
	}
	for _, p := range []Retention{{0, 24}, {257, 24}, {2, 0}, {2, 2161}} {
		if err := s.SetRetention(t.Context(), p); !errors.Is(err, ErrInvalid) {
			t.Errorf("accepted policy=%+v", p)
		}
	}
}

func TestConcurrencyValidationAndFailedSaveDoNotLaunch(t *testing.T) {
	var calls atomic.Int32
	s, db := diagnosticService(t, func(ctx context.Context, _ netsec.ProbeRequest) (*netsec.ProbeResult, error) {
		calls.Add(1)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	for _, req := range []netsec.ProbeRequest{{Tool: "shell", Target: "id"}, {Tool: "ping", Target: "--help"}, {Tool: "port", Target: "example.test", Port: 65536}} {
		if _, err := s.Create(t.Context(), "test", req, "admin"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid request=%v", err)
		}
	}
	if _, err := s.Create(t.Context(), "bad\nname", netsec.ProbeRequest{Tool: "ping", Target: "example.test"}, "admin"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid name=%v", err)
	}
	for i := 0; i < MaxRunning; i++ {
		if _, err := s.Create(t.Context(), "bounded", netsec.ProbeRequest{Tool: "ping", Target: "example.test"}, "admin"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Create(t.Context(), "fifth", netsec.ProbeRequest{Tool: "ping", Target: "example.test"}, "admin"); !errors.Is(err, ErrBusy) {
		t.Fatalf("fifth=%v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if calls.Load() > MaxRunning {
		t.Fatalf("calls=%d", calls.Load())
	}
	broken := New(NewStore(db.DB), jobs.New(nil), func(context.Context, netsec.ProbeRequest) (*netsec.ProbeResult, error) {
		calls.Add(100)
		return nil, nil
	})
	if err := broken.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.Exec(`DROP TABLE network_diagnostic_runs`); err != nil {
		t.Fatal(err)
	}
	before := calls.Load()
	if _, err := broken.Create(t.Context(), "storage must precede traffic", netsec.ProbeRequest{Tool: "ping", Target: "example.test"}, "admin"); err == nil {
		t.Fatal("accepted unavailable store")
	}
	if calls.Load() != before {
		t.Fatal("failed durable write launched traffic")
	}
	broken.jobs.Shutdown()
}

func TestTimeoutAndOutcomeProvenance(t *testing.T) {
	s, _ := diagnosticService(t, func(ctx context.Context, _ netsec.ProbeRequest) (*netsec.ProbeResult, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	s.timeout = 15 * time.Millisecond
	run, err := s.Create(t.Context(), "timeout", netsec.ProbeRequest{Tool: "ping", Target: "example.test"}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	finished := awaitRun(t, s, run.ID)
	if finished.Status != "failed" || finished.Outcome != "timed_out" || finished.OutcomeSource != "context" {
		t.Fatalf("finished=%+v", finished)
	}
	for message, want := range map[string]string{"executable file not found": "unsupported", "operation not permitted": "permission_denied", "lookup: no such host": "dns_failure", "x509: certificate expired": "invalid_certificate", "connection refused": "refused"} {
		got, source := outcome(nil, nil, errors.New(message))
		if got != want || source != "error_text" {
			t.Errorf("%q=%s/%s", message, got, source)
		}
	}
}

func TestFailedFinalRecordingIsExplicitAndRetryDoesNotReplayProbe(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	s, db := diagnosticService(t, func(ctx context.Context, req netsec.ProbeRequest) (*netsec.ProbeResult, error) {
		calls.Add(1)
		close(started)
		<-release
		return &netsec.ProbeResult{Tool: req.Tool, Target: req.Target, OK: true, Records: []string{"retained after write recovery"}}, nil
	})
	run, err := s.Create(t.Context(), "recording failure", netsec.ProbeRequest{Tool: "ping", Target: "127.0.0.1"}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err := db.DB.Exec(`CREATE TRIGGER reject_diagnostic_result BEFORE UPDATE ON network_diagnostic_runs WHEN NEW.status IN ('completed','failed','cancelled','interrupted') BEGIN SELECT RAISE(FAIL,'recording unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		job, _, _ := s.jobs.Get(run.JobID)
		if job.Status == jobs.StatusFailed {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := s.Get(t.Context(), run.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("failed recording read=%v", err)
	}
	if _, err := s.Create(t.Context(), "admission must wait", run.Request, "admin"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("failed recording admission=%v", err)
	}
	if _, err := db.DB.Exec(`DROP TRIGGER reject_diagnostic_result`); err != nil {
		t.Fatal(err)
	}
	finished, err := s.Get(t.Context(), run.ID)
	if err != nil || finished.Status != "completed" || !finished.HasResult || finished.Result.Records[0] != "retained after write recovery" || calls.Load() != 1 {
		t.Fatalf("recording recovery=%+v, %v; calls=%d", finished, err, calls.Load())
	}
}

func TestCancellationSignalsEvenWhenRecordingRequestFails(t *testing.T) {
	started := make(chan struct{})
	s, db := diagnosticService(t, func(ctx context.Context, _ netsec.ProbeRequest) (*netsec.ProbeResult, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	run, err := s.Create(t.Context(), "stop despite recording fault", netsec.ProbeRequest{Tool: "ping", Target: "127.0.0.1"}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err := db.DB.Exec(`CREATE TRIGGER reject_cancel_request BEFORE UPDATE ON network_diagnostic_runs WHEN NEW.status='cancelling' BEGIN SELECT RAISE(FAIL,'recording unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Cancel(t.Context(), run.ID); !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "signalled") {
		t.Fatalf("cancel=%v", err)
	}
	if finished := awaitRun(t, s, run.ID); finished.Status != "cancelled" {
		t.Fatalf("finished=%+v", finished)
	}
}

func TestArtifactExportAndComparisonBounds(t *testing.T) {
	var calls atomic.Int32
	s, _ := diagnosticService(t, func(_ context.Context, req netsec.ProbeRequest) (*netsec.ProbeResult, error) {
		count := calls.Add(1)
		records := []string{"stable", "old"}
		duration := "2ms"
		if count > 1 {
			records = []string{"new", "stable", "stable"}
			duration = "5ms"
		}
		return &netsec.ProbeResult{Tool: req.Tool, Target: req.Target, OK: true, Records: records, Output: strings.Repeat("<\u0000界", MaxOutputBytes), Duration: duration}, nil
	})
	a, err := s.Create(t.Context(), "bounded", netsec.ProbeRequest{Tool: "dns", Target: "example.test"}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	finished := awaitRun(t, s, a.ID)
	if !finished.ResultTruncated || len(finished.Result.Output) > MaxOutputBytes || !utf8.ValidString(finished.Result.Output) {
		t.Fatal("artifact was not bounded")
	}
	data, err := s.Export(t.Context(), a.ID)
	if err != nil || len(data) > MaxExportBytes || !json.Valid(data) {
		t.Fatalf("export bytes=%d err=%v", len(data), err)
	}
	b, err := s.Rerun(t.Context(), a.ID, "admin")
	if err != nil {
		t.Fatal(err)
	}
	awaitRun(t, s, b.ID)
	comparison, err := s.Compare(t.Context(), a.ID, b.ID)
	if err != nil || len(comparison.Records.Added) != 1 || comparison.Records.Added[0] != "new" || len(comparison.Records.Removed) != 1 || comparison.Records.Removed[0] != "old" || comparison.Records.Unchanged != 1 || comparison.DurationDeltaMS == nil || *comparison.DurationDeltaMS != 3 || !comparison.Partial {
		t.Fatalf("comparison=%+v err=%v", comparison, err)
	}
	bad := finished
	bad.ID = "different-port"
	bad.Request.Port = 8443
	if err := s.store.insert(t.Context(), bad); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Compare(t.Context(), a.ID, bad.ID); !errors.Is(err, ErrIncompatible) {
		t.Fatalf("incompatible=%v", err)
	}
	lines := make([]string, MaxDiffLines+50)
	for i := range lines {
		lines[i] = strconv.Itoa(i)
	}
	if diff := difference(nil, lines); len(diff.Added) != MaxDiffLines || !diff.Truncated {
		t.Fatalf("unbounded diff=%+v", diff)
	}
	large := &netsec.ProbeResult{Output: strings.Repeat("界", MaxOutputBytes), Records: make([]string, MaxRecords+1)}
	for i := range large.Records {
		large.Records[i] = strings.Repeat("x", MaxRecordBytes+1)
	}
	bounded, trimmed := boundedResult(large)
	encoded, _ := json.Marshal(bounded)
	if !trimmed || len(encoded) > MaxArtifactBytes || len(bounded.Records) > MaxRecords {
		t.Fatal("record artifact bound failed")
	}
}
