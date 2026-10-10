package netx

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommitRecordsReachedPhasesWithoutPublishingPrivateSnapshots(t *testing.T) {
	record(t, "systemctl")
	s := testService(t)
	if err := s.commit(context.Background(), emptySpec(), step{}); err != nil {
		t.Fatal(err)
	}
	j, err := readChange(s.paths.Dir)
	if err != nil || j.Phase != "saved" || j.Runtime != "applied" || j.Persistence != "written" || j.Boot != "unsupported" {
		t.Fatalf("phase evidence = %+v, %v", j, err)
	}
	info, _ := os.Stat(filepath.Join(s.paths.Dir, recoveryFile))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("journal mode = %o", info.Mode().Perm())
	}
	p := s.PersistenceStatus(context.Background())
	if p.Change == nil || p.Change.Generation == "" {
		t.Fatalf("missing published phase evidence: %+v", p)
	}
}

func TestRecoveryPreservesTheJournalAndReportsEveryFailedStep(t *testing.T) {
	r := record(t, "systemctl").fail("ip link del one", "permission denied").fail("ip link del two", "device is busy")
	s := testService(t)
	j := &changeJournal{Paths: s.paths, ChangeStatus: ChangeStatus{ID: "test", Generation: strings.Repeat("a", 64), Phase: "runtime_applied"}, Commands: []recoveryCommand{
		{Tool: "ip", Args: []string{"link", "del", "one"}}, {Tool: "ip", Args: []string{"link", "del", "two"}},
	}}
	if err := j.save(); err != nil {
		t.Fatal(err)
	}
	if err := RecoverNetwork(context.Background(), s.paths.Dir, j.ID); err == nil {
		t.Fatal("failed recovery reported success")
	}
	got, err := readChange(s.paths.Dir)
	if err != nil || got.Phase != "degraded" || len(got.RecoveryErrors) != 2 || len(r.commands()) != 2 {
		t.Fatalf("recovery = %+v, %v; commands %v", got, err, r.commands())
	}
	if err := s.commit(context.Background(), emptySpec(), step{apply: func(context.Context) error { t.Fatal("unresolved journal allowed another apply"); return nil }}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("next change = %v", err)
	}
}

func TestOldRecoveryTimerCannotUndoANewerOrCompletedChange(t *testing.T) {
	r := record(t, "systemctl")
	s := testService(t)
	for _, phase := range []string{"prepared", "confirmed", "boot_degraded"} {
		j := &changeJournal{Paths: s.paths, ChangeStatus: ChangeStatus{ID: "new", Generation: strings.Repeat("a", 64), Phase: phase}, Commands: []recoveryCommand{{Tool: "ip", Args: []string{"link", "del", "new0"}}}}
		if err := j.save(); err != nil {
			t.Fatal(err)
		}
		if err := RecoverNetwork(context.Background(), s.paths.Dir, "old"); err != nil {
			t.Fatal(err)
		}
		if phase != "prepared" {
			if err := RecoverNetwork(context.Background(), s.paths.Dir, "new"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(r.commands()) != 0 {
		t.Fatalf("old/completed change was touched: %v", r.commands())
	}
}

func TestRecoveryRejectsUnexpectedFilesAndCommandsBeforeRestoringAnything(t *testing.T) {
	record(t, "systemctl")
	s := testService(t)
	if err := writeFileAtomic(s.specPath(), []byte("candidate"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"file", "command"} {
		j := &changeJournal{Paths: s.paths, ChangeStatus: ChangeStatus{ID: "test", Generation: strings.Repeat("a", 64), Phase: "prepared"}, Files: []recoverySnapshot{{Path: s.specPath(), Data: []byte("previous"), Exists: true, Mode: 0o600}}}
		if invalid == "file" {
			j.Files = append(j.Files, recoverySnapshot{Path: filepath.Join(t.TempDir(), "outside")})
		} else {
			j.Commands = []recoveryCommand{{Tool: "sh", Args: []string{"-c", "id"}}}
		}
		if err := j.save(); err != nil {
			t.Fatal(err)
		}
		if err := RecoverNetwork(context.Background(), s.paths.Dir, j.ID); err == nil {
			t.Fatalf("unexpected %s accepted", invalid)
		}
		data, _ := os.ReadFile(s.specPath())
		if string(data) != "candidate" {
			t.Fatalf("restored files before validating %s", invalid)
		}
	}
}

func TestRecoveryRunsAfterTheApplyingProcessDiesBetweenPhases(t *testing.T) {
	for _, phase := range []string{"runtime", "first_file", "spec", "boot"} {
		t.Run(phase, func(t *testing.T) {
			record(t, "systemctl")
			s := testService(t)
			if err := s.commit(context.Background(), emptySpec(), step{}); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(s.specPath())
			if err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(filepath.Join(s.paths.Dir, linksFile))
			if err := os.WriteFile(filepath.Join(s.paths.Dir, "runtime.fixture"), []byte("previous"), 0o600); err != nil {
				t.Fatal(err)
			}
			invoke := func(mode string) *exec.Cmd {
				cmd := exec.Command(os.Args[0], "-test.run=^TestNetworkRecoveryProcessFixture$")
				cmd.Env = append(os.Environ(), "JD_RECOVERY_FIXTURE_DIR="+filepath.Dir(s.paths.Dir), "JD_RECOVERY_FIXTURE_MODE="+mode, "JD_RECOVERY_FIXTURE_PHASE="+phase)
				return cmd
			}
			out, err := invoke("apply").CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 83 {
				t.Fatalf("process did not die at %s: %v %s", phase, err, out)
			}
			candidate, _ := os.ReadFile(filepath.Join(s.paths.Dir, "runtime.fixture"))
			if string(candidate) != "candidate" {
				t.Fatal("fixture did not apply runtime")
			}
			// This is a fresh process after the applier is gone; it has no
			// closures, memory, database or server from the original process.
			if out, err := invoke("recover").CombinedOutput(); err != nil {
				t.Fatalf("independent recovery: %v %s", err, out)
			}
			after, _ := os.ReadFile(s.specPath())
			if string(after) != string(before) {
				t.Fatalf("spec was not restored after %s", phase)
			}
			after, _ = os.ReadFile(filepath.Join(s.paths.Dir, linksFile))
			if string(after) != string(data) {
				t.Fatalf("boot render was not restored after %s", phase)
			}
			runtime, _ := os.ReadFile(filepath.Join(s.paths.Dir, "runtime.fixture"))
			if string(runtime) != "previous" {
				t.Fatalf("runtime recovery = %q", runtime)
			}
			j, err := readChange(s.paths.Dir)
			if err != nil || j.Phase != "recovered" {
				t.Fatalf("recovery phase = %+v, %v", j, err)
			}
		})
	}
}

func TestNetworkRecoveryProcessFixture(t *testing.T) {
	dir := os.Getenv("JD_RECOVERY_FIXTURE_DIR")
	if dir == "" {
		return
	}
	paths := Paths{Dir: filepath.Join(dir, "network"), Sysctl: filepath.Join(dir, "sysctl.d", "90-just-dashboard.conf"), Unit: filepath.Join(dir, "systemd", UnitName)}
	mode, phase := os.Getenv("JD_RECOVERY_FIXTURE_MODE"), os.Getenv("JD_RECOVERY_FIXTURE_PHASE")
	has = func(string) bool { return false }
	if mode == "live_recover" {
		if err := RecoverNetworkStandalone(context.Background(), paths.Dir, "pending"); err != nil {
			t.Fatal(err)
		}
		return
	}
	runStdin = func(_ context.Context, _ []byte, tool string, args ...string) (string, error) {
		if tool != "ip" || strings.Join(args, " ") != "link del crash0" {
			return "", errors.New("unexpected fixture recovery command")
		}
		return "", os.WriteFile(filepath.Join(paths.Dir, "runtime.fixture"), []byte("previous"), 0o600)
	}
	if mode == "recover" {
		if err := RecoverNetwork(context.Background(), paths.Dir, "pending"); err != nil {
			t.Fatal(err)
		}
		return
	}
	s := New(Options{Paths: paths})
	sp := emptySpec()
	sp.Links = []LinkSpec{{Name: "crash0", Kind: "dummy", Up: true}}
	if phase == "boot" {
		has = func(name string) bool { return name == "systemctl" }
		run = func(context.Context, string, ...string) (string, error) { os.Exit(83); return "", nil }
	}
	writer := writeNetworkFile
	writeNetworkFile = func(path string, data []byte, mode os.FileMode) error {
		if err := writer(path, data, mode); err != nil {
			return err
		}
		if phase == "first_file" || phase == "spec" && path == s.specPath() {
			os.Exit(83)
		}
		return nil
	}
	if err := s.commit(context.Background(), sp, step{apply: func(context.Context) error {
		if mode == "live_apply" {
			if out, err := exec.Command("ip", "link", "add", "crash0", "type", "dummy").CombinedOutput(); err != nil {
				return errors.New(string(out))
			}
		}
		if err := os.WriteFile(filepath.Join(paths.Dir, "runtime.fixture"), []byte("candidate"), 0o600); err != nil {
			return err
		}
		if phase == "runtime" {
			os.Exit(83)
		}
		return nil
	}}); err != nil {
		t.Fatal(err)
	}
	t.Fatal("fixture did not reach its interruption")
}
