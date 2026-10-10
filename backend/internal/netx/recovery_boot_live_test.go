package netx

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLiveBootRecoveryRecreatesUnchangedManagedDependenciesAfterColdLoss(t *testing.T) {
	liveRequired(t)
	ns := newLiveNS(t)
	useLive(t, ns)
	s := testService(t)
	old := emptySpec()
	old.NextID = 3
	old.Namespaces = []NamespaceSpec{{Name: "coldns"}}
	old.Links = []LinkSpec{
		{Name: "coldbr", Kind: "bridge", Addresses: []string{"10.80.0.1/24"}, Up: true},
		{Name: "coldveth", Kind: "veth", Peer: "eth0", PeerNamespace: "coldns", Master: "coldbr", Up: true},
	}
	old.Addresses = []AddressSpec{{ID: 1, Link: "coldbr", CIDR: "10.81.0.1/24"}}
	old.Routes = []RouteSpec{{ID: 2, Family: "inet", Type: "unicast", Destination: "192.0.2.0/24", Gateway: "10.80.0.2", Device: "coldbr", Table: 254}}
	if err := s.commit(context.Background(), old, step{apply: func(ctx context.Context) error {
		_, err := runStdin(ctx, []byte(renderLinks(old)), "ip", "-force", "-batch", "-")
		return err
	}}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(s.specPath())
	if err != nil {
		t.Fatal(err)
	}
	invoke := func(mode string) *exec.Cmd {
		return ns.command(context.Background(), "env", "JD_BOOT_RECOVERY_DIR="+filepath.Dir(s.paths.Dir), "JD_BOOT_RECOVERY_MODE="+mode, os.Args[0], "-test.run=^TestBootRecoveryProcessFixture$")
	}
	out, err := invoke("apply").CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 83 {
		t.Fatalf("applying process did not die after candidate spec write: %v %s", err, out)
	}
	// Cold boot loses even the unchanged link/namespace objects. These are
	// confined to this test's network and private /run/netns mount.
	ns.must(t, "ip", "link", "del", "coldveth")
	ns.must(t, "ip", "link", "del", "coldbr")
	ns.must(t, "ip", "netns", "del", "coldns")
	if out, err := invoke("timer").CombinedOutput(); err != nil {
		t.Fatalf("timer-scope check failed: %v %s", err, out)
	}
	if _, err := ns.run(context.Background(), nil, "ip", "link", "show", "dev", "coldbr"); err == nil {
		t.Fatal("ordinary timer recreated an unchanged managed dependency")
	}
	if out, err := invoke("boot").CombinedOutput(); err != nil {
		t.Fatalf("fresh boot recovery process failed: %v %s", err, out)
	}
	liveHas(t, "recovered bridge", ns.must(t, "ip", "-j", "-d", "link", "show", "dev", "coldbr"), `"info_kind":"bridge"`, "UP")
	liveHas(t, "reverted address", ns.must(t, "ip", "-j", "addr", "show", "dev", "coldbr"), "10.80.0.1", "10.81.0.1")
	liveHas(t, "reverted route", ns.must(t, "ip", "-j", "route", "show", "192.0.2.0/24"), "10.80.0.2", "coldbr")
	liveHas(t, "recovered namespace peer", ns.must(t, "ip", "-n", "coldns", "-j", "link", "show", "dev", "eth0"), "UP")
	// The standalone root process restores private root-owned files.
	if after := ns.must(t, "cat", s.specPath()); after != string(before) {
		t.Fatal("boot recovery did not restore the previous spec")
	}
	j := ns.must(t, "cat", filepath.Join(s.paths.Dir, recoveryFile))
	if !strings.Contains(j, `"phase": "recovered"`) || strings.Contains(j, `"recoveryErrors"`) {
		t.Fatalf("boot recovery did not record healthy completion: %s", j)
	}
}

func TestBootRecoveryProcessFixture(t *testing.T) {
	dir := os.Getenv("JD_BOOT_RECOVERY_DIR")
	if dir == "" {
		return
	}
	paths := Paths{Dir: filepath.Join(dir, "network"), Sysctl: filepath.Join(dir, "sysctl.d", "90-just-dashboard.conf"), Unit: filepath.Join(dir, "systemd", UnitName)}
	has = func(string) bool { return false }
	switch os.Getenv("JD_BOOT_RECOVERY_MODE") {
	case "timer":
		if err := RecoverNetworkStandalone(context.Background(), paths.Dir, "pending"); err == nil {
			t.Fatal("targeted timer undo unexpectedly succeeded without its unchanged bridge")
		}
		return
	case "boot":
		if err := RecoverNetworkBootStandalone(context.Background(), paths.Dir, "pending"); err != nil {
			t.Fatal(err)
		}
		return
	case "apply":
	default:
		t.Fatal("unexpected boot fixture mode")
	}
	// This process was launched inside the disposable namespaces. Run ip
	// directly there; entering host PID 1 would escape the acceptance fixture.
	execute := func(ctx context.Context, input []byte, tool string, args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, tool, args...)
		if input != nil {
			cmd.Stdin = bytes.NewReader(input)
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			return string(out), errors.New(string(out))
		}
		return string(out), nil
	}
	run = func(ctx context.Context, tool string, args ...string) (string, error) {
		return execute(ctx, nil, tool, args...)
	}
	runStdin = execute
	s := New(Options{Paths: paths})
	old, err := s.loadSpec()
	if err != nil {
		t.Fatal(err)
	}
	next := old.clone()
	next.Addresses, next.Routes = nil, nil
	writer := writeNetworkFile
	writeNetworkFile = func(path string, data []byte, mode os.FileMode) error {
		if err := writer(path, data, mode); err != nil {
			return err
		}
		if path == s.specPath() {
			os.Exit(83)
		}
		return nil
	}
	if err := s.commit(context.Background(), next, step{apply: func(ctx context.Context) error {
		for _, r := range old.Routes {
			args, err := routeCommand("del", r)
			if err != nil {
				return err
			}
			if _, err := run(ctx, "ip", args...); err != nil {
				return err
			}
		}
		for _, a := range old.Addresses {
			if _, err := run(ctx, "ip", addressRecoveryArgs(old, a, "del")...); err != nil {
				return err
			}
		}
		return nil
	}}); err != nil {
		t.Fatal(err)
	}
	t.Fatal("applying process did not reach candidate spec write")
}
