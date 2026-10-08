package netx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAdmissionRecoveryIncludesOnlyObservedChainsAndRetainsOwnedPositions(t *testing.T) {
	h := newGwHost(t)
	sp := emptySpec()
	sp.Forwards = []ForwardSpec{{ID: 1, Target: "10.0.0.5", Enabled: true}}
	h.first("iptables -S FORWARD", "-P FORWARD DROP\n-A FORWARD -j foreign\n-A FORWARD "+strings.Join(admissionRule(), " ")+"\n", nil)
	h.first("iptables -S INPUT", "-P INPUT DROP\n", nil)
	h.first("iptables -S DOCKER-USER", "No chain/target/match by that name.", errors.New("no chain"))
	commands, err := snapshotAdmissionRecovery(context.Background(), sp, emptySpec())
	if err != nil {
		t.Fatal(err)
	}
	inserted := 0
	for _, c := range commands {
		if c.Tool != "iptables" || strings.Contains(strings.Join(c.Args, " "), "DOCKER-USER") {
			t.Fatalf("unsupported family/chain was journaled: %+v", c)
		}
		if c.Args[0] == "-I" {
			inserted++
			if c.Args[1] != "FORWARD" || c.Args[2] != "2" {
				t.Fatalf("owned position not preserved: %+v", c)
			}
		}
	}
	if inserted != 1 {
		t.Fatalf("insert commands = %d: %+v", inserted, commands)
	}
}

func TestAdmissionRecoveryDoesNotIgnorePermissionFailures(t *testing.T) {
	h := newGwHost(t)
	h.first("ip6tables -S INPUT", "Permission denied", errors.New("Permission denied"))
	if _, err := snapshotAdmissionRecovery(context.Background(), dualStackAdmissionSpec(), emptySpec()); err == nil {
		t.Fatal("unreadable original chain produced a recovery plan")
	}
	if recoveryExpectedAbsence("iptables", "Permission denied", errors.New("permission denied")) {
		t.Fatal("recovery ignored a permission error")
	}
	if !recoveryExpectedAbsence("iptables", "Bad rule (does a matching rule exist in that chain?).", errors.New("rule absent")) {
		t.Fatal("a missing owned rule made bounded deletion fail")
	}
}

func TestFetchedCacheIsRestoredWhenAWriterReplacesItThenReportsFailure(t *testing.T) {
	srv := newFeedServer(t)
	srv.set("/feed", "203.0.113.0/24\n")
	h := newGwHost(t)
	v, err := h.AddBlocklist(context.Background(), BlocklistRequest{Name: "list", Kind: "feed", URL: srv.URL + "/feed"}, gwClient, "ops")
	if err != nil {
		t.Fatal(err)
	}
	path := blocklistFile(filepath.Join(h.paths.Dir, "lists"), v.ID)
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	srv.set("/feed", "198.51.100.0/24\n")
	priorWriter := writeNetworkFile
	writeNetworkFile = func(p string, b []byte, mode os.FileMode) error {
		if err := priorWriter(p, b, mode); err != nil {
			return err
		}
		if p == path {
			return errors.New("directory fsync failed after rename")
		}
		return nil
	}
	t.Cleanup(func() { writeNetworkFile = priorWriter })
	if err := h.RefreshBlocklist(context.Background(), v.ID); err == nil {
		t.Fatal("failed cache write was reported successful")
	}
	b, _ := os.ReadFile(path)
	info, _ := os.Stat(path)
	if string(b) != "203.0.113.0/24\n" || info.Mode().Perm() != 0o640 {
		t.Fatalf("previous cache/content mode lost: %q %o", b, info.Mode().Perm())
	}
	j, err := readChange(h.paths.Dir)
	if err != nil || j.Phase != "recovered" {
		t.Fatalf("failed cache replacement recovery = %+v, %v", j, err)
	}
}

func TestCacheSnapshotRefusesSymlinksAndNonOwnedNames(t *testing.T) {
	dir := t.TempDir()
	if recoveryBlocklistPath(dir, filepath.Join(dir, "lists", "../other")) || recoveryBlocklistPath(dir, filepath.Join(dir, "lists", "01.txt")) || recoveryBlocklistPath(dir, filepath.Join(dir, "lists", "0.txt")) {
		t.Fatal("noncanonical cache snapshot path accepted")
	}
	if err := os.Mkdir(filepath.Join(dir, "lists"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "foreign.txt")
	if err := os.WriteFile(target, []byte("203.0.113.0/24\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := blocklistFile(filepath.Join(dir, "lists"), 1)
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if err := validateRecoveryBlocklistPath(dir, path); err == nil {
		t.Fatal("a symlink cache could be staged for replacement")
	}
	b, _ := os.ReadFile(target)
	if string(b) != "203.0.113.0/24\n" {
		t.Fatal("validation touched foreign data")
	}
}

func TestGatewayUndoRecordsFailedKernelRestorationAsDegraded(t *testing.T) {
	h := newGwHost(t)
	if _, err := h.AddForward(context.Background(), gwWebForward(), gwClient, "ops", gwProtected); err != nil {
		t.Fatal(err)
	}
	h.first("nft list set", "verify failed", errors.New("verify failed"))
	loads := 0
	priorRun := run
	run = func(ctx context.Context, tool string, args ...string) (string, error) {
		if tool == "nft" && len(args) == 2 && args[0] == "-f" {
			loads++
			if loads == 2 {
				return "restore denied", errors.New("restore denied")
			}
		}
		return priorRun(ctx, tool, args...)
	}
	t.Cleanup(func() { run = priorRun })
	req := gwWebForward()
	req.TargetPort = "81"
	if _, err := h.UpdateForward(context.Background(), 1, req, gwClient, "ops", gwProtected); err == nil {
		t.Fatal("failed verification was ignored")
	}
	j, err := readChange(h.paths.Dir)
	if err != nil || j.Phase != "degraded" || len(j.RecoveryErrors) == 0 || !strings.Contains(strings.Join(j.RecoveryErrors, " "), "restore denied") {
		t.Fatalf("kernel recovery failure was hidden: %+v, %v", j, err)
	}
}

func TestCacheReplacementCrashRestoresCacheSpecRenderAndRuntimeTogether(t *testing.T) {
	record(t, "systemctl")
	s := testService(t)
	sp := emptySpec()
	sp.NextID = 2
	sp.Blocklists = []BlocklistSpec{{ID: 1, Name: "list", Kind: "feed", URL: "https://example.com/list", Count: 1, Enabled: true, Refreshed: time.Now().UTC().Add(-time.Hour)}}
	if err := writeBlocklistCache(filepath.Join(s.paths.Dir, "lists"), 1, []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}); err != nil {
		t.Fatal(err)
	}
	rec := record(t, "systemctl")
	rec.on("nft -c -f", "")
	if err := s.commit(context.Background(), sp, step{}); err != nil {
		t.Fatal(err)
	}
	beforeSpec, _ := os.ReadFile(s.specPath())
	beforeRender, _ := os.ReadFile(filepath.Join(s.paths.Dir, gatewayFile))
	if err := os.WriteFile(filepath.Join(s.paths.Dir, "runtime.fixture"), beforeRender, 0o600); err != nil {
		t.Fatal(err)
	}
	invoke := func(mode string) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=^TestGatewayCacheRecoveryProcessFixture$")
		cmd.Env = append(os.Environ(), "JD_GATEWAY_RECOVERY_DIR="+filepath.Dir(s.paths.Dir), "JD_GATEWAY_RECOVERY_MODE="+mode)
		return cmd
	}
	out, err := invoke("apply").CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 83 {
		t.Fatalf("child did not die after replacing cache: %v %s", err, out)
	}
	candidate, _ := os.ReadFile(blocklistFile(filepath.Join(s.paths.Dir, "lists"), 1))
	if !strings.Contains(string(candidate), "198.51.100.0/24") {
		t.Fatalf("candidate cache was not written: %q", candidate)
	}
	j, err := readChange(s.paths.Dir)
	if err != nil || j.Phase != "prepared" || len(j.Files) == 0 {
		t.Fatalf("cache changed before durable recovery preparation: %+v, %v", j, err)
	}
	if out, err := invoke("recover").CombinedOutput(); err != nil {
		t.Fatalf("fresh process recovery failed: %v %s", err, out)
	}
	restored, _ := os.ReadFile(blocklistFile(filepath.Join(s.paths.Dir, "lists"), 1))
	afterSpec, _ := os.ReadFile(s.specPath())
	afterRender, _ := os.ReadFile(filepath.Join(s.paths.Dir, gatewayFile))
	runtime, _ := os.ReadFile(filepath.Join(s.paths.Dir, "runtime.fixture"))
	if string(restored) != "203.0.113.0/24\n" || string(beforeSpec) != string(afterSpec) || string(beforeRender) != string(afterRender) || string(beforeRender) != string(runtime) {
		t.Fatal("cache/spec/render/runtime did not recover one generation")
	}
	recovered, _ := s.loadSpec()
	newRender, err := renderGateway(recovered, s.trustedFor(recovered))
	if err != nil || newRender != string(beforeRender) {
		t.Fatal("a later render would silently apply uncommitted fetched data")
	}
}

func TestGatewayCacheRecoveryProcessFixture(t *testing.T) {
	dir := os.Getenv("JD_GATEWAY_RECOVERY_DIR")
	if dir == "" {
		return
	}
	s := New(Options{Paths: Paths{Dir: filepath.Join(dir, "network"), Sysctl: filepath.Join(dir, "sysctl.d", "90-just-dashboard.conf"), Unit: filepath.Join(dir, "systemd", UnitName)}})
	has = func(string) bool { return false }
	mode := os.Getenv("JD_GATEWAY_RECOVERY_MODE")
	directNft := func(ctx context.Context, tool string, args ...string) (string, error) {
		if tool != "nft" {
			return "", errors.New("unexpected live fixture tool")
		}
		out, err := exec.CommandContext(ctx, tool, args...).CombinedOutput()
		if err != nil {
			return string(out), fmt.Errorf("nft: %s: %w", out, err)
		}
		return string(out), nil
	}
	if mode == "live_inspect" {
		run = directNft
		v, err := s.Blocklist(context.Background(), 1, "")
		if err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("JD_GATEWAY_VIEW:%s\n", b)
		return
	}
	if mode == "live_recover" {
		if err := RecoverNetworkStandalone(context.Background(), s.paths.Dir, "pending"); err != nil {
			t.Fatal(err)
		}
		return
	}
	runStdin = func(_ context.Context, _ []byte, tool string, args ...string) (string, error) {
		if tool != "nft" || len(args) != 2 || args[0] != "-f" {
			return "", errors.New("unexpected gateway recovery command")
		}
		b, err := os.ReadFile(args[1])
		if err != nil {
			return "", err
		}
		return "", os.WriteFile(filepath.Join(s.paths.Dir, "runtime.fixture"), b, 0o600)
	}
	if mode == "recover" {
		if err := RecoverNetwork(context.Background(), s.paths.Dir, "pending"); err != nil {
			t.Fatal(err)
		}
		return
	}
	run = func(context.Context, string, ...string) (string, error) { return "", nil }
	if mode == "live_apply" {
		run = func(ctx context.Context, tool string, args ...string) (string, error) {
			out, err := directNft(ctx, tool, args...)
			if err != nil {
				return out, err
			}
			if len(args) == 2 && args[0] == "-f" && os.Getenv("JD_GATEWAY_RECOVERY_PHASE") == "runtime" {
				os.Exit(83)
			}
			return out, nil
		}
	}
	writer := writeNetworkFile
	writeNetworkFile = func(path string, b []byte, mode os.FileMode) error {
		if err := writer(path, b, mode); err != nil {
			return err
		}
		if path == blocklistFile(filepath.Join(s.paths.Dir, "lists"), 1) && os.Getenv("JD_GATEWAY_RECOVERY_PHASE") != "runtime" {
			os.Exit(83)
		}
		return nil
	}
	if err := s.mutateGatewayWithCache(context.Background(), func(_, next *Spec, stage func(int, []netip.Prefix) error) (bool, error) {
		next.Blocklists[0].Count, next.Blocklists[0].Refreshed = 2, time.Now().UTC()
		return false, stage(1, []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24")})
	}); err != nil {
		t.Fatal(err)
	}
	t.Fatal("cache replacement did not reach its interruption")
}
