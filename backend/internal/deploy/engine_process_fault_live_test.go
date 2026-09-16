package deploy

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	basestore "github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

// A backend that is killed mid-step is the fault the component restart matrix
// can only approximate: there, the "crash" is a test that stops calling the
// store. Here a second process really holds the lease, really writes step
// output, and is really SIGKILLed, so what survives is whatever SQLite and the
// reconciler actually leave behind.
func TestLiveBackendKillAtEveryStepLeavesOneRecoveredRun(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 to kill a real engine process")
	}
	host := buildEngineHost(t)
	for _, stalled := range DefaultStepKeys {
		t.Run(string(stalled), func(t *testing.T) {
			dir := t.TempDir()
			st, err := basestore.Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			runs := NewOrchestrationStore(st)
			projectID, environmentID := seedProcessFaultProject(t, st)
			run, created, err := runs.Enqueue(context.Background(), RunRequest{
				ProjectID: projectID, EnvironmentID: environmentID,
				Operation: OperationDeploy, Trigger: TriggerManual, Actor: "admin",
				RequestDigest: fmt.Sprintf("digest-%s-%d", stalled, time.Now().UnixNano()),
				PlanRevision:  1, SlotClass: SlotLight,
			})
			if err != nil || !created {
				t.Fatalf("enqueue = %v, created=%v", err, created)
			}

			marker := filepath.Join(dir, "stalled")
			child := exec.Command(host,
				"-dir", dir, "-worker", "killed-backend", "-stall", string(stalled),
				"-marker", marker, "-lease", "2s")
			var output strings.Builder
			child.Stdout, child.Stderr = &output, &output
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			killed := false
			defer func() {
				if !killed {
					_ = child.Process.Signal(syscall.SIGKILL)
					_ = child.Wait()
				}
				if t.Failed() && output.Len() > 0 {
					t.Logf("engine-host output:\n%s", output.String())
				}
			}()
			waitForProcessFaultMarker(t, marker, 30*time.Second)

			// SIGKILL, not SIGTERM: the engine's own shutdown path waits for
			// adapters, and a test that used it would be measuring the clean
			// path it is supposed to avoid.
			if err := child.Process.Signal(syscall.SIGKILL); err != nil {
				t.Fatal(err)
			}
			_ = child.Wait()
			killed = true

			// The step the killed process was inside must still carry its own
			// output. Losing it is how a crash becomes unexplainable.
			assertStepLogSurvived(t, runs, run.ID, stalled)

			// A second backend starts against the same database, exactly as a
			// restart would. Nothing tells it what the dead process was doing
			// except the store.
			recovered := NewEngine(runs, &recordingExecutor{}, nil, EngineConfig{
				WorkerID: "restarted-backend", PollEvery: 10 * time.Millisecond, LeaseTTL: time.Minute,
			}, nil)
			if err := recovered.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer func() {
				shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_ = recovered.Shutdown(shutdown)
			}()

			// The default reconciler refuses to guess: an interrupted step
			// whose outcome cannot be proven ends the run rather than being
			// replayed over side effects that may already exist.
			final := waitForProcessFaultTerminal(t, runs, run.ID, 30*time.Second)
			if final.State != RunFailed {
				t.Fatalf("run state = %s, want %s", final.State, RunFailed)
			}
			if final.TerminalCode != "restart_evidence_missing" {
				t.Fatalf("terminal code = %q, want restart_evidence_missing", final.TerminalCode)
			}
			assertNoActiveRuns(t, st, environmentID)
			assertStepLogSurvived(t, runs, run.ID, stalled)
		})
	}
}

func buildEngineHost(t *testing.T) string {
	t.Helper()
	tool := filepath.Join(runtime.GOROOT(), "bin", "go")
	if _, err := os.Stat(tool); err != nil {
		found, lookErr := exec.LookPath("go")
		if lookErr != nil {
			t.Fatalf("no Go toolchain to build the engine host: %v", err)
		}
		tool = found
	}
	binary := filepath.Join(t.TempDir(), "engine-host")
	build := exec.Command(tool, "build", "-o", binary, "./internal/deploy/testdata/engine-host")
	build.Dir = filepath.Join("..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build engine host: %v: %s", err, out)
	}
	return binary
}

func seedProcessFaultProject(t *testing.T, st *basestore.Store) (int64, int64) {
	t.Helper()
	now := time.Now().Unix()
	project, err := st.DB.Exec(`
		INSERT INTO deploy_projects(
		  name, repo_path, branch, compose_file, pre_command, post_command,
		  hook_secret, hook_id, enabled, created_at, profile, updated_at)
		VALUES('process-fault', '/srv/process-fault', 'main', 'compose.yml', '', '',
		       'sealed', 'process-fault-hook', 1, ?, 'compose', ?)`, now, now)
	if err != nil {
		t.Fatal(err)
	}
	projectID, err := project.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	environment, err := st.DB.Exec(`
		INSERT INTO deploy_environments(
		  project_id, name, slug, kind, desired_revision, strategy,
		  expected_downtime, protected, created_at, updated_at)
		VALUES(?, 'production', 'production', ?, 1, 'stop_first', 1, 1, ?, ?)`,
		projectID, EnvironmentProduction, now, now)
	if err != nil {
		t.Fatal(err)
	}
	environmentID, err := environment.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return projectID, environmentID
}

func waitForProcessFaultMarker(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("engine host never reached the stalled step (%s)", path)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitForProcessFaultTerminal(t *testing.T, runs *OrchestrationStore, runID int64, timeout time.Duration) EngineRun {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		run, err := runs.Run(context.Background(), runID)
		if err != nil {
			t.Fatal(err)
		}
		if run.State == RunSucceeded || run.State == RunFailed || run.State == RunCancelled {
			return *run
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %d stayed in %s", runID, run.State)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// assertStepLogSurvived is the durable-diagnostics half: output the dead
// process wrote before it was killed must still be readable afterwards.
func assertStepLogSurvived(t *testing.T, runs *OrchestrationStore, runID int64, key StepKey) {
	t.Helper()
	events, err := runs.EventsAfter(context.Background(), runID, 0, 5000)
	if err != nil {
		t.Fatalf("read %s events: %v", key, err)
	}
	want := "engine-host entered " + string(key)
	for _, event := range events {
		if strings.Contains(string(event.Data), want) {
			return
		}
	}
	t.Fatalf("step %s lost the killed process's output (%d events)", key, len(events))
}

// assertNoActiveRuns is the ownership invariant: a crash must not leave a run
// still claimable for the same environment once recovery has finished.
func assertNoActiveRuns(t *testing.T, st *basestore.Store, environmentID int64) {
	t.Helper()
	var active int
	if err := st.DB.QueryRow(`
		SELECT COUNT(*) FROM deploy_runs
		 WHERE environment_id = ? AND state NOT IN ('succeeded', 'failed', 'cancelled')`,
		environmentID).Scan(&active); err != nil {
		t.Fatalf("count active runs: %v", err)
	}
	if active != 0 {
		t.Fatalf("environment %d still has %d active runs after recovery", environmentID, active)
	}
}
