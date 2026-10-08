package netx

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Each child stays inside the disposable namespace. The recovery child starts
// after the applying process exits and runs real ip argv, without its parent
// Service, undo closures, backend or database.
func TestLiveIndependentRecoveryAfterProcessDeath(t *testing.T) {
	liveRequired(t)
	ns := newLiveNS(t)
	for _, phase := range []string{"runtime", "first_file", "spec"} {
		t.Run(phase, func(t *testing.T) {
			record(t, "systemctl")
			s := testService(t)
			if err := s.commit(context.Background(), emptySpec(), step{}); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(s.specPath())
			if err := os.WriteFile(filepath.Join(s.paths.Dir, "runtime.fixture"), []byte("previous"), 0o600); err != nil {
				t.Fatal(err)
			}
			invoke := func(mode string) *exec.Cmd {
				cmd := ns.command(context.Background(), os.Args[0], "-test.run=^TestNetworkRecoveryProcessFixture$")
				cmd.Env = append(os.Environ(), "JD_RECOVERY_FIXTURE_DIR="+filepath.Dir(s.paths.Dir), "JD_RECOVERY_FIXTURE_MODE="+mode, "JD_RECOVERY_FIXTURE_PHASE="+phase)
				return cmd
			}
			out, err := invoke("live_apply").CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 83 {
				t.Fatalf("applying process: %v %s", err, out)
			}
			if _, err := ns.run(context.Background(), nil, "ip", "link", "show", "dev", "crash0"); err != nil {
				t.Fatalf("actual device never created: %v", err)
			}
			if out, err := invoke("live_recover").CombinedOutput(); err != nil {
				t.Fatalf("standalone recovery: %v %s", err, out)
			}
			if _, err := ns.run(context.Background(), nil, "ip", "link", "show", "dev", "crash0"); err == nil {
				t.Fatal("actual candidate device survived recovery")
			}
			after, _ := os.ReadFile(s.specPath())
			if string(after) != string(before) {
				t.Fatal("prior spec was not recovered")
			}
			j, err := readChange(s.paths.Dir)
			if err != nil || j.Phase != "recovered" {
				t.Fatalf("journal = %+v, %v", j, err)
			}
		})
	}
}
