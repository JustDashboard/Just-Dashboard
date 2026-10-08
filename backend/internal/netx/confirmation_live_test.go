package netx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The API fixture, packet loss and recovery worker all stay in one disposable
// network/mount namespace. Host systemd arming is mocked; a separate process
// waits the real journal's 90-second deadline and runs the standalone worker.
func TestLivePendingBackendDeathAndUnavailableNewSource(t *testing.T) {
	liveRequired(t)
	for _, name := range []string{"curl", "nft"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("%s is unavailable", name)
		}
	}
	ns := newLiveNS(t)
	dir := t.TempDir()
	t.Cleanup(func() {
		_, _ = ns.run(context.Background(), nil, "chmod", "-R", "ugo+rwX", dir)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	invoke := func(mode string) *exec.Cmd {
		return ns.command(ctx, "env", "JD_PENDING_LIVE_DIR="+dir, "JD_PENDING_LIVE_MODE="+mode, os.Args[0], "-test.run=^TestPendingLiveProcessFixture$")
	}
	backend := invoke("backend")
	if err := backend.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pid, _ := ns.run(context.Background(), nil, "cat", filepath.Join(dir, "backend.pid"))
		if strings.TrimSpace(pid) != "" {
			_, _ = ns.run(context.Background(), nil, "kill", "-9", strings.TrimSpace(pid))
		}
		_ = backend.Wait()
	})
	for deadline := time.Now().Add(5 * time.Second); ; {
		if _, err := ns.run(ctx, nil, "cat", filepath.Join(dir, "ready")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pending backend did not become ready")
		}
		time.Sleep(20 * time.Millisecond)
	}
	ns.must(t, "ip", "link", "show", "dev", "pending0")
	var before changeJournal
	if err := json.Unmarshal([]byte(ns.must(t, "cat", filepath.Join(dir, "network", recoveryFile))), &before); err != nil || before.Phase != "awaiting_confirmation" {
		t.Fatalf("candidate journal=%+v,%v", before, err)
	}
	worker := invoke("worker")
	if err := worker.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pid, _ := ns.run(context.Background(), nil, "cat", filepath.Join(dir, "worker.pid"))
		if strings.TrimSpace(pid) != "" {
			_, _ = ns.run(context.Background(), nil, "kill", "-9", strings.TrimSpace(pid))
		}
		_ = worker.Wait()
	})
	url := "http://127.0.0.1:43819/verify"
	answer := ns.must(t, "curl", "--noproxy", "*", "--silent", "--show-error", "--fail", "--interface", "127.0.0.1", "--request", "POST", url)
	var received ReconnectionVerification
	if err := json.Unmarshal([]byte(answer), &received); err != nil || received.Challenge == "" {
		t.Fatalf("fresh authenticated transport response=%s,%v", answer, err)
	}
	// Both source addresses retain the same device, destination and kernel
	// route. A refused new-source dashboard request is still no confirmation.
	priorRoute := ns.must(t, "ip", "route", "get", "127.0.0.1", "from", "127.0.0.2")
	rules := "table inet lost_response {\n chain input {\n type filter hook input priority 0; policy accept;\n ip saddr 127.0.0.2 drop;\n }\n}\n"
	if _, err := ns.run(ctx, []byte(rules), "nft", "-f", "-"); err != nil {
		t.Fatal(err)
	}
	if _, err := ns.run(ctx, nil, "curl", "--noproxy", "*", "--silent", "--show-error", "--fail", "--max-time", "1", "--interface", "127.0.0.2", "--request", "POST", url); err == nil {
		t.Fatal("new-source request unexpectedly received a dashboard response")
	}
	if afterRoute := ns.must(t, "ip", "route", "get", "127.0.0.1", "from", "127.0.0.2"); priorRoute != afterRoute {
		t.Fatalf("source packet loss changed the kernel route: %s -> %s", priorRoute, afterRoute)
	}
	var blocked changeJournal
	if err := json.Unmarshal([]byte(ns.must(t, "cat", filepath.Join(dir, "network", recoveryFile))), &blocked); err != nil || blocked.Phase != "awaiting_confirmation" || blocked.VerificationTransport != "127.0.0.1" {
		t.Fatalf("unavailable source confirmed or replaced evidence: %+v,%v", blocked, err)
	}
	pid := strings.TrimSpace(ns.must(t, "cat", filepath.Join(dir, "backend.pid")))
	ns.must(t, "kill", "-9", pid)
	if err := backend.Wait(); err == nil {
		t.Fatal("backend fixture survived its kill")
	}
	// No API process or its undo closures exist while the independent worker
	// waits to restore at the captured deadline.
	if err := worker.Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := ns.run(ctx, nil, "ip", "link", "show", "dev", "pending0"); err == nil {
		t.Fatal("candidate device survived independent timeout recovery")
	}
	var recovered changeJournal
	if err := json.Unmarshal([]byte(ns.must(t, "cat", filepath.Join(dir, "network", recoveryFile))), &recovered); err != nil || recovered.Phase != "recovered" || recovered.Runtime != "restored" || recovered.UpdatedAt.Before(before.ExpiresAt) {
		t.Fatalf("deadline recovery=%+v,%v", recovered, err)
	}
}

func TestPendingLiveProcessFixture(t *testing.T) {
	dir := os.Getenv("JD_PENDING_LIVE_DIR")
	if dir == "" {
		return
	}
	paths := Paths{Dir: filepath.Join(dir, "network"), Sysctl: filepath.Join(dir, "sysctl.conf"), Unit: filepath.Join(dir, "systemd", UnitName)}
	mode := os.Getenv("JD_PENDING_LIVE_MODE")
	if err := os.WriteFile(filepath.Join(dir, mode+".pid"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	if mode == "worker" {
		has = func(string) bool { return false }
		j, err := readChange(paths.Dir)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(max(0, time.Until(j.ExpiresAt)))
		if err := RecoverNetworkStandalone(context.Background(), paths.Dir, j.ID); err != nil {
			t.Fatal(err)
		}
		return
	}
	has = func(name string) bool { return name == "systemctl" || name == "systemd-run" }
	run = func(context.Context, string, ...string) (string, error) { return "enabled", nil }
	s := New(Options{Paths: paths, IndependentRecovery: true})
	s.recoveryInstalled = true
	sp := emptySpec()
	sp.Links = []LinkSpec{{Name: "pending0", Kind: "dummy", Up: true}}
	if err := s.commit(WithPendingConfirmation(context.Background(), 7), sp, step{apply: func(context.Context) error {
		if out, err := exec.Command("ip", "link", "add", "pending0", "type", "dummy").CombinedOutput(); err != nil {
			return errors.New(string(out))
		}
		return nil
	}}); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:43819")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ready"), []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		j, err := readChange(paths.Dir)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		source, _, _ := net.SplitHostPort(r.RemoteAddr)
		result, err := s.VerifyReconnection(r.Context(), j.ID, 7, "fixture-session", source)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(result); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	})
	if err := http.Serve(listener, handler); err != nil {
		t.Fatal(err)
	}
}
