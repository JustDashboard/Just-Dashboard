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

func TestDriftRepairRecoversInFreshProcessAfterAttemptedWrite(t *testing.T) {
	h := driftRepairHost(t, emptySpec())
	path := filepath.Join(h.paths.Dir, linksFile)
	before := driftDamage(t, path, 0o640)
	paths, _ := json.Marshal(h.paths)
	invoke := func(mode string) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=^TestDriftRepairProcessFixture$")
		cmd.Env = append(os.Environ(), "JD_DRIFT_REPAIR_PATHS="+string(paths), "JD_DRIFT_REPAIR_MODE="+mode)
		return cmd
	}
	out, err := invoke("apply").CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 83 {
		t.Fatalf("applying process did not die after its selected write: %v %s", err, out)
	}
	after, _ := os.ReadFile(path)
	if string(after) == string(before) {
		t.Fatal("selected file effect never happened")
	}
	j, err := readChange(h.paths.Dir)
	if err != nil || len(j.Files) != 1 || j.Files[0].Path != path || j.Phase != "prepared" {
		t.Fatalf("pre-effect undo was not durable: %+v %v", j, err)
	}
	if out, err := invoke("recover").CombinedOutput(); err != nil {
		t.Fatalf("fresh helper failed: %v %s", err, out)
	}
	after, _ = os.ReadFile(path)
	info, _ := os.Stat(path)
	if string(after) != string(before) || info.Mode().Perm() != 0o640 {
		t.Fatal("fresh process did not restore exact selected bytes and mode")
	}
	j, err = readChange(h.paths.Dir)
	if err != nil || j.Phase != "recovered" {
		t.Fatalf("recovery did not record outcome: %+v %v", j, err)
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
	if os.Getenv("JD_DRIFT_REPAIR_MODE") == "recover" {
		has = func(string) bool { return false }
		if err := RecoverNetworkStandalone(context.Background(), paths.Dir, "pending"); err != nil {
			t.Fatal(err)
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
	r := s.Drift(context.Background())
	item := driftRepairItem(t, r, filepath.Join(paths.Dir, linksFile))
	prior := writeNetworkFile
	writeNetworkFile = func(path string, data []byte, mode os.FileMode) error {
		if err := prior(path, data, mode); err != nil {
			return err
		}
		if path == item.Resource {
			os.Exit(83)
		}
		return nil
	}
	if _, err := s.RepairDrift(WithPendingConfirmation(context.Background(), 7), driftRepairRequest(r, item), "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	t.Fatal("fixture never reached the selected file write")
}
