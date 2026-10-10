package netx

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLiveCacheAndGatewayRecoverOneGenerationAfterApplyingProcessDies(t *testing.T) {
	gwLiveRequired(t)
	ns := gwLiveNS(t, "cache-recovery")
	prevRun, prevHas := run, has
	run, has = gwLiveRun(ns), func(string) bool { return false }
	t.Cleanup(func() { run, has = prevRun, prevHas })
	for _, phase := range []string{"cache", "runtime"} {
		t.Run(phase, func(t *testing.T) {
			s := testService(t)
			sp := emptySpec()
			sp.NextID = 2
			sp.Blocklists = []BlocklistSpec{{ID: 1, Name: "list", Kind: "feed", URL: "https://example.com/list", Count: 1, Enabled: true, Refreshed: time.Now().Add(-time.Hour)}}
			if err := writeBlocklistCache(filepath.Join(s.paths.Dir, "lists"), 1, []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}); err != nil {
				t.Fatal(err)
			}
			if err := s.commit(context.Background(), sp, step{apply: func(ctx context.Context) error { return s.loadGateway(ctx, sp) }}); err != nil {
				t.Fatal(err)
			}
			before := readBlocklistRuntime(context.Background(), 1)
			if before.Status != "present" {
				t.Fatalf("initial live sets = %+v", before)
			}
			invoke := func(mode string) *exec.Cmd {
				// sudo removes custom process environment; pass this fixture's
				// nonsecret settings to env inside the privileged command.
				args := []string{"env", "JD_GATEWAY_RECOVERY_DIR=" + filepath.Dir(s.paths.Dir), "JD_GATEWAY_RECOVERY_MODE=" + mode, "JD_GATEWAY_RECOVERY_PHASE=" + phase, "ip", "netns", "exec", ns, os.Args[0], "-test.run=^TestGatewayCacheRecoveryProcessFixture$"}
				cmd := gwLiveCmd(context.Background(), args...)
				return cmd
			}
			out, err := invoke("live_apply").CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 83 {
				t.Fatalf("applying process did not die after %s: %v %s", phase, err, out)
			}
			candidate := readBlocklistRuntime(context.Background(), 1)
			if phase == "runtime" && candidate.Generation == before.Generation {
				t.Fatal("live runtime did not reach the fetched candidate")
			}
			if out, err := invoke("live_recover").CombinedOutput(); err != nil {
				t.Fatalf("fresh host process recovery failed: %v %s", err, out)
			}
			// Recovery writes private files as root. Verify through a fresh
			// root reader in the namespace, matching the production backend.
			out, err = invoke("live_inspect").CombinedOutput()
			if err != nil {
				t.Fatalf("reading recovered state: %v %s", err, out)
			}
			var v BlocklistView
			found := false
			for _, line := range strings.Split(string(out), "\n") {
				if strings.HasPrefix(line, "JD_GATEWAY_VIEW:") {
					found = true
					if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "JD_GATEWAY_VIEW:")), &v); err != nil {
						t.Fatal(err)
					}
				}
			}
			if !found || v.Enforcement != "verified" || v.Cache.Count != 1 || v.Cache.Generation != before.Generation || v.Runtime.Generation != before.Generation {
				t.Fatalf("cache/spec/render/kernel did not recover one generation: %+v", v)
			}
		})
	}
}
