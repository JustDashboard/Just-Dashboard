package netx

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func pendingHost(t *testing.T) *gwHost {
	t.Helper()
	h := newGwHost(t)
	h.independentRecovery, h.recoveryInstalled = true, true
	h.rec.on("systemd-run", "armed")
	return h
}

func applyPending(t *testing.T, h *gwHost) *changeJournal {
	t.Helper()
	ctx := WithPendingConfirmation(context.Background(), 7)
	if err := h.commit(ctx, emptySpec(), step{}); err != nil {
		t.Fatal(err)
	}
	j, err := readChange(h.paths.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if j.Phase != "awaiting_confirmation" || j.OwnerUserID != 7 || j.Watchdog != "armed" || j.ExpiresAt.IsZero() || j.AppliedAt.IsZero() || time.Until(j.ExpiresAt) > confirmationWindow {
		t.Fatalf("pending evidence=%+v", j)
	}
	return j
}

func TestPendingApplyRequiresFreshOwnedSessionResponse(t *testing.T) {
	h := pendingHost(t)
	j := applyPending(t, h)
	ctx := context.Background()
	// commit returned and released flock; new independent requests can acquire it.
	for _, tc := range []struct {
		id      string
		owner   int64
		session string
	}{{j.ID, 8, "other"}, {"old-id", 7, "one"}, {j.ID, 7, ""}} {
		if _, err := h.VerifyReconnection(ctx, tc.id, tc.owner, tc.session, "192.0.2.17"); err == nil {
			t.Fatalf("invalid verification accepted: %+v", tc)
		}
	}
	if _, err := h.ConfirmChange(ctx, j.ID, 7, "one", strings.Repeat("a", 64), "192.0.2.17"); err == nil {
		t.Fatal("a route prediction or unavailable response confirmed the change")
	}
	first, err := h.VerifyReconnection(ctx, j.ID, 7, "one", "192.0.2.17")
	if err != nil {
		t.Fatal(err)
	}
	if !first.VerifiedAt.After(j.AppliedAt) {
		t.Fatal("verification was not new after apply")
	}
	second, err := h.VerifyReconnection(ctx, j.ID, 7, "one", "192.0.2.17")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		owner              int64
		session, challenge string
	}{{8, "one", second.Challenge}, {7, "new-session", second.Challenge}, {7, "one", first.Challenge}, {7, "one", "invalid"}} {
		if _, err := h.ConfirmChange(ctx, j.ID, tc.owner, tc.session, tc.challenge, "192.0.2.17"); err == nil {
			t.Fatalf("invalid confirmation accepted: %+v", tc)
		}
	}
	status, err := h.ConfirmChange(ctx, j.ID, 7, "one", second.Challenge, "192.0.2.17")
	if err != nil || status.Phase != "confirmed" || status.Watchdog != "completed" {
		t.Fatalf("confirmation=%+v,%v", status, err)
	}
	if err := RecoverNetwork(ctx, h.paths.Dir, j.ID); err != nil {
		t.Fatal(err)
	}
	after, _ := readChange(h.paths.Dir)
	if after.Phase != "confirmed" {
		t.Fatal("stale timer undid a confirmed change")
	}
}

func TestPendingDeadlineRestoresFilesAndRejectsConfirmation(t *testing.T) {
	h := pendingHost(t)
	if err := h.commit(context.Background(), emptySpec(), step{}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(h.specPath())
	next := emptySpec()
	next.Sysctls = map[string]string{"net.ipv4.tcp_syncookies": "1"}
	h.rec.on("sysctl", "1")
	if err := h.commit(WithPendingConfirmation(context.Background(), 7), next, step{}); err != nil {
		t.Fatal(err)
	}
	j, _ := readChange(h.paths.Dir)
	challenge, err := h.VerifyReconnection(context.Background(), j.ID, 7, "same-session", "192.0.2.17")
	if err != nil {
		t.Fatal(err)
	}
	j, _ = readChange(h.paths.Dir)
	j.ExpiresAt = time.Now().Add(-time.Second)
	if err := j.save(); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ConfirmChange(context.Background(), j.ID, 7, "same-session", challenge.Challenge, "192.0.2.17"); err == nil {
		t.Fatal("expired change confirmed")
	}
	after, _ := os.ReadFile(h.specPath())
	if string(after) != string(before) {
		t.Fatal("deadline left candidate spec in place")
	}
	j, _ = readChange(h.paths.Dir)
	if j.Phase != "recovered" {
		t.Fatalf("deadline phase=%s", j.Phase)
	}
}

func TestPendingApplyRefusesUnavailableWatchdogBeforeMutation(t *testing.T) {
	for _, failure := range []string{"unsupported", "arming failed"} {
		t.Run(failure, func(t *testing.T) {
			h := pendingHost(t)
			if failure == "unsupported" {
				h.independentRecovery = false
			} else {
				h.first("systemd-run", "host manager unavailable", errors.New("host manager unavailable"))
			}
			touched := false
			err := h.commit(WithPendingConfirmation(context.Background(), 7), emptySpec(), step{apply: func(context.Context) error { touched = true; return nil }})
			if err == nil || touched {
				t.Fatalf("unarmed pending apply ran: touched=%v,err=%v", touched, err)
			}
		})
	}
}

func TestPendingVerificationExpiresAndDifferentSessionMustReconnect(t *testing.T) {
	h := pendingHost(t)
	j := applyPending(t, h)
	response, err := h.VerifyReconnection(context.Background(), j.ID, 7, "old-session", "192.0.2.17")
	if err != nil {
		t.Fatal(err)
	}
	// A new source/session can verify again, but the old session's token does
	// not certify a response on the new authenticated dashboard transport.
	if _, err := h.ConfirmChange(context.Background(), j.ID, 7, "new-session", response.Challenge, "192.0.2.17"); err == nil {
		t.Fatal("source/session change bypassed reconnection")
	}
	response, err = h.VerifyReconnection(context.Background(), j.ID, 7, "new-session", "192.0.2.17")
	if err != nil {
		t.Fatal(err)
	}
	j, _ = readChange(h.paths.Dir)
	j.VerifiedAt = time.Now().Add(-verificationFreshness - time.Second)
	if err := j.save(); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ConfirmChange(context.Background(), j.ID, 7, "new-session", response.Challenge, "192.0.2.17"); err == nil {
		t.Fatal("stale response confirmed change")
	}
}

func TestPendingSourceChangeNeedsItsOwnSuccessfulResponse(t *testing.T) {
	h := pendingHost(t)
	j := applyPending(t, h)
	ctx := context.Background()
	response, err := h.VerifyReconnection(ctx, j.ID, 7, "same-session", "192.0.2.17")
	if err != nil {
		t.Fatal(err)
	}
	// A surviving kernel route and an unchanged session cookie cannot transfer
	// an old source's received response to a new dashboard transport.
	if _, err := h.ConfirmChange(ctx, j.ID, 7, "same-session", response.Challenge, "192.0.2.18"); err == nil {
		t.Fatal("old-source response confirmed from a new source")
	}
	if _, err := h.ConfirmChange(ctx, j.ID, 7, "same-session", strings.Repeat("a", 64), "192.0.2.18"); err == nil {
		t.Fatal("unavailable new-source response confirmed a surviving route")
	}
	response, err = h.VerifyReconnection(ctx, j.ID, 7, "same-session", "192.0.2.18")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.ConfirmChange(ctx, j.ID, 7, "same-session", response.Challenge, "192.0.2.18"); err != nil {
		t.Fatal(err)
	}
}

func TestPendingRecoveryRunsAfterTheBackendProcessDies(t *testing.T) {
	dir := t.TempDir()
	invoke := func(mode string) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=^TestPendingConfirmationProcessFixture$")
		cmd.Env = append(os.Environ(), "JD_PENDING_FIXTURE_DIR="+dir, "JD_PENDING_FIXTURE_MODE="+mode)
		return cmd
	}
	out, err := invoke("apply").CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 83 {
		t.Fatalf("backend fixture did not die after pending apply: %v %s", err, out)
	}
	j, err := readChange(filepath.Join(dir, "network"))
	if err != nil || j.Phase != "awaiting_confirmation" {
		t.Fatalf("pending journal=%+v,%v", j, err)
	}
	// The host worker has no API, sessions, mutexes or closures from that process.
	if out, err := invoke("recover").CombinedOutput(); err != nil {
		t.Fatalf("independent timeout worker: %v %s", err, out)
	}
	runtime, _ := os.ReadFile(filepath.Join(dir, "runtime.fixture"))
	if string(runtime) != "previous" {
		t.Fatalf("dead backend's runtime survived: %s", runtime)
	}
	j, err = readChange(filepath.Join(dir, "network"))
	if err != nil || j.Phase != "recovered" {
		t.Fatalf("recovered journal=%+v,%v", j, err)
	}
}

func TestPendingConfirmationProcessFixture(t *testing.T) {
	dir := os.Getenv("JD_PENDING_FIXTURE_DIR")
	if dir == "" {
		return
	}
	paths := Paths{Dir: filepath.Join(dir, "network"), Sysctl: filepath.Join(dir, "sysctl.conf"), Unit: filepath.Join(dir, "unit")}
	if os.Getenv("JD_PENDING_FIXTURE_MODE") == "recover" {
		has = func(string) bool { return false }
		runStdin = func(ctx context.Context, input []byte, tool string, args ...string) (string, error) {
			if tool != "ip" || strings.Join(args, " ") != "link del pending0" {
				return "", errors.New("unexpected fixture command")
			}
			return "", os.WriteFile(filepath.Join(dir, "runtime.fixture"), []byte("previous"), 0o600)
		}
		if err := RecoverNetwork(context.Background(), paths.Dir, "pending"); err != nil {
			t.Fatal(err)
		}
		return
	}
	has = func(name string) bool { return name == "systemctl" || name == "systemd-run" }
	run = func(ctx context.Context, tool string, args ...string) (string, error) {
		if tool == "systemctl" && len(args) > 0 && args[0] == "is-enabled" {
			return "enabled", nil
		}
		return "", nil
	}
	s := New(Options{Paths: paths, IndependentRecovery: true})
	s.recoveryInstalled = true
	if err := s.commit(WithPendingConfirmation(context.Background(), 7), emptySpec(), step{apply: func(context.Context) error {
		return os.WriteFile(filepath.Join(dir, "runtime.fixture"), []byte("candidate"), 0o600)
	}, recovery: []recoveryCommand{{Tool: "ip", Args: []string{"link", "del", "pending0"}}}}); err != nil {
		t.Fatal(err)
	}
	os.Exit(83)
}
