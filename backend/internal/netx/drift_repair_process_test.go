package netx

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func driftRepairFixture(paths Paths) func(string) *exec.Cmd {
	encoded, _ := json.Marshal(paths)
	return func(mode string) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=^TestDriftRepairProcessFixture$")
		cmd.Env = append(os.Environ(), "JD_DRIFT_REPAIR_PATHS="+string(encoded), "JD_DRIFT_REPAIR_MODE="+mode)
		return cmd
	}
}

func driftRepairFixtureDied(err error) bool {
	var exit *exec.ExitError
	return errors.As(err, &exit) && exit.ExitCode() == 83
}

func TestDriftRepairRecoversInFreshProcessAfterAttemptedWrite(t *testing.T) {
	h := driftRepairHost(t, emptySpec())
	path := filepath.Join(h.paths.Dir, linksFile)
	before := driftDamage(t, path, 0o640)
	unattempted := filepath.Join(h.paths.Dir, rules6File)
	driftDamage(t, unattempted, 0o640)
	invoke := driftRepairFixture(h.paths)
	out, err := invoke("apply").CombinedOutput()
	if !driftRepairFixtureDied(err) {
		t.Fatalf("applying process did not die after its selected write: %v %s", err, out)
	}
	after, _ := os.ReadFile(path)
	if string(after) == string(before) {
		t.Fatal("selected file effect never happened")
	}
	j, err := readChange(h.paths.Dir)
	if err != nil || !j.SelectedDriftRepair || len(j.BootDependencies) != 0 || len(j.Files) != 1 || j.Files[0].Path != path || j.Phase != "prepared" {
		t.Fatalf("pre-effect undo was not durable: %+v %v", j, err)
	}
	foreign := []byte("# Native replacement after the first selected write\n")
	if err := os.WriteFile(unattempted, foreign, 0o640); err != nil {
		t.Fatal(err)
	}
	if out, err := invoke("recover").CombinedOutput(); err != nil {
		t.Fatalf("fresh helper failed: %v %s", err, out)
	}
	after, _ = os.ReadFile(path)
	info, _ := os.Stat(path)
	if string(after) != string(before) || info.Mode().Perm() != 0o640 {
		t.Fatal("fresh process did not restore exact selected bytes and mode")
	}
	untouched, _ := os.ReadFile(unattempted)
	if string(untouched) != string(foreign) {
		t.Fatal("fresh recovery overwrote an unattempted foreign replacement")
	}
	j, err = readChange(h.paths.Dir)
	if err != nil || j.Phase != "recovered" {
		t.Fatalf("recovery did not record outcome: %+v %v", j, err)
	}
}

func TestDriftRepairPreparedCrashPreservesForeignResourcesAndDoesNotReplayBootDependencies(t *testing.T) {
	sp := emptySpec()
	sp.Links = []LinkSpec{{Name: "unrelated0", Kind: "dummy", Up: true}}
	h := driftRepairHost(t, sp)
	h.rec.on("ip -j -d link show", "[]")
	path := filepath.Join(h.paths.Dir, linksFile)
	driftDamage(t, path, 0o640)
	invoke := driftRepairFixture(h.paths)
	out, err := invoke("prepared_crash").CombinedOutput()
	if !driftRepairFixtureDied(err) {
		t.Fatalf("fixture did not die after initial durable prepare: %v %s", err, out)
	}
	j, err := readChange(h.paths.Dir)
	if err != nil || !j.SelectedDriftRepair || len(j.Files) != 0 || len(j.Commands) != 0 || len(j.BootDependencies) != 0 {
		t.Fatalf("initial undo was not empty and selected-only: %+v %v", j, err)
	}
	foreign := []byte("# Native owner arrived before any selected effect\n")
	if err := os.WriteFile(path, foreign, 0o640); err != nil {
		t.Fatal(err)
	}
	if out, err := invoke("recover_boot").CombinedOutput(); err != nil {
		t.Fatalf("fresh scoped boot recovery failed: %v %s", err, out)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(foreign) {
		t.Fatal("initial prepared journal overwrote a foreign resource")
	}
}

func TestDriftRepairProcessFixture(t *testing.T) {
	encoded := os.Getenv("JD_DRIFT_REPAIR_PATHS")
	if encoded == "" {
		return
	}
	var paths Paths
	if err := json.Unmarshal([]byte(encoded), &paths); err != nil {
		t.Fatal(err)
	}
	mode := os.Getenv("JD_DRIFT_REPAIR_MODE")
	if mode == "recover" || mode == "recover_boot" {
		rec := record(t)
		var err error
		if mode == "recover_boot" {
			err = RecoverNetworkBoot(context.Background(), paths.Dir, "pending")
		} else {
			err = RecoverNetwork(context.Background(), paths.Dir, "pending")
		}
		if err != nil {
			t.Fatal(err)
		}
		if len(rec.commands()) != 0 {
			t.Fatalf("fresh selected recovery reached an untouched unit or kernel object: %v", rec.commands())
		}
		return
	}
	s := New(Options{Paths: paths, IndependentRecovery: true})
	s.recoveryInstalled = true
	rec := driftIdleHost(t)
	rec.replies = append([]reply{{prefix: "systemctl show " + UnitName, out: "LoadState=not-found\nActiveState=inactive\nExecMainStartTimestampMonotonic=0\n"}}, rec.replies...)
	// The fixture records manager prerequisites; selected file effects and
	// fresh-process journal recovery are real and independent of this Service.
	rec.replies = append([]reply{{prefix: "systemctl show just-dashboard-network-recovery.service", out: "LoadState=loaded\nUnitFileState=enabled\nFragmentPath=" + filepath.Join(filepath.Dir(paths.Unit), "just-dashboard-network-recovery.service") + "\nDropInPaths=\nNeedDaemonReload=no\n"}}, rec.replies...)
	rec.on(filepath.Join(paths.Dir, recoveryBinary)+" --network-recovery-check", "").on("systemctl enable", "").on("systemd-run", "")
	rec.on("ip -j -d link show", "[]").on("ip -j addr show", "[]")
	if mode == "prepared_crash" {
		priorRun := run
		run = func(ctx context.Context, name string, args ...string) (string, error) {
			out, err := priorRun(ctx, name, args...)
			if name == "systemd-run" && err == nil {
				os.Exit(83)
			}
			return out, err
		}
	}
	r := s.Drift(context.Background())
	item := driftRepairItem(t, r, filepath.Join(paths.Dir, linksFile))
	prior := writeSelectedDriftFile
	writeSelectedDriftFile = func(path string, data []byte, mode os.FileMode, beforeRename func(string) error) error {
		if err := prior(path, data, mode, beforeRename); err != nil {
			return err
		}
		if path == item.Resource {
			os.Exit(83)
		}
		return nil
	}
	items := []OwnedRepair{item}
	if mode != "prepared_crash" {
		items = append(items, driftRepairItem(t, r, filepath.Join(paths.Dir, rules6File)))
	}
	if _, err := s.RepairDrift(WithPendingConfirmation(context.Background(), 7), driftRepairRequest(r, items...), "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	t.Fatal("fixture never reached the selected file write")
}
