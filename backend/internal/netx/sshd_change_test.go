package netx

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type sshHost struct {
	*Service
	rec        *recorder
	config     string
	dropIn     string
	socketFile string
}

// pendingSSHHost is a service whose SSH paths live in a temporary directory,
// with an armed watchdog and an sshd_config holding the operator's port.
func pendingSSHHost(t *testing.T) *sshHost {
	t.Helper()
	rec := record(t)
	svc := testService(t)
	svc.paths.SSH = filepath.Join(t.TempDir(), "ssh")
	svc.independentRecovery, svc.recoveryInstalled = true, true
	rec.on(filepath.Join(svc.paths.Dir, recoveryBinary)+" --network-recovery-check", "")
	rec.on("systemd-run", "armed")
	rec.on("systemctl", "")
	h := &sshHost{
		Service:    svc,
		rec:        rec,
		config:     filepath.Join(svc.paths.SSH, "sshd_config"),
		dropIn:     filepath.Join(svc.paths.SSH, "sshd_config.d", sshManagedDropIn),
		socketFile: filepath.Join(filepath.Dir(svc.paths.Unit), "ssh.socket.d", sshSocketDropIn),
	}
	if err := os.MkdirAll(filepath.Dir(h.dropIn), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.config, []byte("Include /etc/ssh/sshd_config.d/*.conf\nPort 22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *sshHost) begin(t *testing.T, socket bool) *SSHChange {
	t.Helper()
	files := []SSHFile{{Path: h.dropIn, Candidate: []byte("Port 2222\n")}}
	unit := ""
	if socket {
		files = append(files, SSHFile{Path: h.socketFile, Candidate: []byte("[Socket]\nListenStream=\nListenStream=0.0.0.0:2222\n")})
		unit = "ssh.socket"
	}
	change, err := h.BeginSSHChange(WithPendingConfirmation(context.Background(), 7), files, unit)
	if err != nil {
		t.Fatal(err)
	}
	return change
}

// write stands in for netsec's apply: the candidate files reach the disk
// after the journal and the watchdog exist.
func (h *sshHost) write(t *testing.T, socket bool) {
	t.Helper()
	if err := os.WriteFile(h.dropIn, []byte("Port 2222\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if socket {
		if err := os.MkdirAll(filepath.Dir(h.socketFile), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(h.socketFile, []byte("[Socket]\nListenStream=\nListenStream=0.0.0.0:2222\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSSHChangeWaitsForAFreshResponseAndKeepsAConfirmedConfiguration(t *testing.T) {
	h := pendingSSHHost(t)
	change := h.begin(t, false)
	if time.Until(change.ExpiresAt) > confirmationWindow || change.ID == "" {
		t.Fatalf("change=%+v", change)
	}
	if !h.rec.ran("systemd-run --collect --unit=just-dashboard-network-recover-" + change.ID) {
		t.Fatalf("watchdog not armed before the write: %v", h.rec.commands())
	}
	h.write(t, false)
	status, err := h.FinishSSHChange(context.Background(), change, nil)
	if err != nil || status.Phase != "awaiting_confirmation" || status.Subsystem != "sshd" || status.Boot != "not_applicable" || status.Runtime != "applied" {
		t.Fatalf("finish=%+v,%v", status, err)
	}
	view, err := h.ConfirmationStatus(context.Background(), 7)
	if err != nil || !view.Owned || view.Change.Subsystem != "sshd" {
		t.Fatalf("view=%+v,%v", view, err)
	}
	verified, err := h.VerifyReconnection(context.Background(), change.ID, 7, "session", "192.0.2.17")
	if err != nil {
		t.Fatal(err)
	}
	confirmed, err := h.ConfirmChange(context.Background(), change.ID, 7, "session", verified.Challenge, "192.0.2.17")
	if err != nil || confirmed.Phase != "confirmed" {
		t.Fatalf("confirm=%+v,%v", confirmed, err)
	}
	// The timer firing after confirmation is a no-op.
	if err := RecoverNetwork(context.Background(), h.paths.Dir, change.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(h.dropIn)
	if string(got) != "Port 2222\n" {
		t.Fatalf("confirmed drop-in was undone: %q", got)
	}
}

func TestUnconfirmedSSHChangeIsRestoredByTheHostWithoutTheBackend(t *testing.T) {
	h := pendingSSHHost(t)
	change := h.begin(t, true)
	h.write(t, true)
	if _, err := h.FinishSSHChange(context.Background(), change, nil); err != nil {
		t.Fatal(err)
	}
	before := len(h.rec.commands())
	// What the timer runs: a fresh read of the journal by directory and ID,
	// with no Service, request or database.
	if err := RecoverNetwork(context.Background(), h.paths.Dir, change.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(h.dropIn); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a drop-in that did not exist before survived recovery: %v", err)
	}
	if _, err := os.Stat(h.socketFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the socket drop-in survived recovery: %v", err)
	}
	if got, _ := os.ReadFile(h.config); !strings.Contains(string(got), "Port 22\n") {
		t.Fatalf("sshd_config changed: %q", got)
	}
	ran := h.rec.commands()[before:]
	want := []string{"systemctl daemon-reload", "systemctl restart ssh.socket", "systemctl reload-or-restart ssh"}
	if strings.Join(ran, "\n") != strings.Join(want, "\n") {
		t.Fatalf("recovery ran %q, want %q", ran, want)
	}
	j, _ := readChange(h.paths.Dir)
	if j.Phase != "recovered" || j.Watchdog != "recovered" || j.Persistence != "restored" {
		t.Fatalf("journal=%+v", j.ChangeStatus)
	}
	verified, err := h.VerifyReconnection(context.Background(), change.ID, 7, "session", "192.0.2.17")
	if err == nil || verified != nil {
		t.Fatal("a recovered SSH change offered confirmation")
	}
}

func TestBootRecoveryOfAnSSHChangeDoesNotWaitOnSSHJobs(t *testing.T) {
	h := pendingSSHHost(t)
	change := h.begin(t, true)
	h.write(t, true)
	if _, err := h.FinishSSHChange(context.Background(), change, nil); err != nil {
		t.Fatal(err)
	}
	before := len(h.rec.commands())
	if err := RecoverNetworkBoot(context.Background(), h.paths.Dir, "pending"); err != nil {
		t.Fatal(err)
	}
	ran := strings.Join(h.rec.commands()[before:], "\n")
	want := "systemctl daemon-reload\nsystemctl --no-block restart ssh.socket\nsystemctl --no-block try-reload-or-restart ssh"
	if ran != want {
		t.Fatalf("boot recovery ran %q", ran)
	}
}

func TestFailedSSHApplyIsRestoredImmediately(t *testing.T) {
	h := pendingSSHHost(t)
	if err := os.WriteFile(h.dropIn, []byte("PasswordAuthentication yes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	change := h.begin(t, false)
	h.write(t, false)
	status, err := h.FinishSSHChange(context.Background(), change, errors.New("sshd did not reload"))
	if err == nil || status == nil || status.Phase != "recovered" {
		t.Fatalf("failed apply=%+v,%v", status, err)
	}
	got, _ := os.ReadFile(h.dropIn)
	if string(got) != "PasswordAuthentication yes\n" {
		t.Fatalf("previous drop-in not restored: %q", got)
	}
	info, _ := os.Stat(h.dropIn)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("restored mode=%v", info.Mode().Perm())
	}
}

func TestSSHChangeAcceptsOnlyAPendingOwnerAndAnArmedWatchdog(t *testing.T) {
	files := func(h *sshHost) []SSHFile { return []SSHFile{{Path: h.dropIn, Candidate: []byte("x")}} }
	t.Run("immediate", func(t *testing.T) {
		h := pendingSSHHost(t)
		if _, err := h.BeginSSHChange(context.Background(), files(h), ""); err == nil {
			t.Fatal("an apply with no pending owner enrolled")
		}
	})
	t.Run("no independent recovery", func(t *testing.T) {
		h := pendingSSHHost(t)
		h.independentRecovery = false
		if _, err := h.BeginSSHChange(WithPendingConfirmation(context.Background(), 7), files(h), ""); err == nil {
			t.Fatal("enrolled without a watchdog")
		}
	})
	t.Run("arming fails", func(t *testing.T) {
		h := pendingSSHHost(t)
		h.rec.replies = append([]reply{{prefix: "systemd-run", err: errors.New("no manager")}}, h.rec.replies...)
		if _, err := h.BeginSSHChange(WithPendingConfirmation(context.Background(), 7), files(h), ""); err == nil {
			t.Fatal("enrolled with an unarmed watchdog")
		}
		j, _ := readChange(h.paths.Dir)
		if j.Phase != "recovered" || j.Watchdog != "failed_to_arm" {
			t.Fatalf("unarmed journal=%+v", j.ChangeStatus)
		}
	})
	t.Run("another pending change", func(t *testing.T) {
		h := pendingSSHHost(t)
		h.begin(t, false)
		if _, err := h.BeginSSHChange(WithPendingConfirmation(context.Background(), 7), files(h), ""); err == nil {
			t.Fatal("a second change enrolled over an unresolved one")
		}
	})
}

func TestSSHJournalRefusesForeignFilesAndCommands(t *testing.T) {
	h := pendingSSHHost(t)
	ctx := WithPendingConfirmation(context.Background(), 7)
	for _, f := range []SSHFile{{Path: "/etc/passwd"}, {Path: filepath.Join(h.paths.SSH, "ssh_config")}, {Path: filepath.Join(filepath.Dir(h.paths.Unit), "evil.socket.d", sshSocketDropIn)}} {
		if _, err := h.BeginSSHChange(ctx, []SSHFile{f}, ""); err == nil {
			t.Fatalf("enrolled %s", f.Path)
		}
	}
	if _, err := h.BeginSSHChange(ctx, []SSHFile{{Path: h.dropIn}}, "getty.socket"); err == nil {
		t.Fatal("enrolled a foreign socket unit")
	}

	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, tamper := range map[string]func(*changeJournal){
		"file": func(j *changeJournal) {
			j.Files = append(j.Files, recoverySnapshot{Path: victim, Data: []byte("x"), Exists: true})
		},
		"command": func(j *changeJournal) {
			j.Commands = append(j.Commands, recoveryCommand{Tool: "sshd", Args: []string{"exec", "/bin/sh"}})
		},
		"tool": func(j *changeJournal) {
			j.Commands = []recoveryCommand{{Tool: "ip", Args: []string{"link", "del", "eth0"}}}
		},
		"owner": func(j *changeJournal) { j.Subsystem = "nginx" },
	} {
		t.Run(name, func(t *testing.T) {
			h := pendingSSHHost(t)
			change := h.begin(t, false)
			h.write(t, false)
			j, err := readChange(h.paths.Dir)
			if err != nil {
				t.Fatal(err)
			}
			tamper(j)
			if err := j.save(); err != nil {
				t.Fatal(err)
			}
			before := len(h.rec.commands())
			if err := RecoverNetwork(context.Background(), h.paths.Dir, change.ID); err == nil {
				t.Fatal("a tampered SSH journal was recovered")
			}
			if got, _ := os.ReadFile(victim); string(got) != "keep" {
				t.Fatal("recovery wrote outside sshd's files")
			}
			if got, _ := os.ReadFile(h.dropIn); string(got) != "Port 2222\n" {
				t.Fatal("recovery acted before validating the journal")
			}
			if len(h.rec.commands()) != before {
				t.Fatalf("recovery ran commands: %v", h.rec.commands()[before:])
			}
		})
	}
}

func TestSSHChangeStatusKeepsSnapshotsOutOfTheAPI(t *testing.T) {
	h := pendingSSHHost(t)
	change := h.begin(t, false)
	h.write(t, false)
	if _, err := h.FinishSSHChange(context.Background(), change, nil); err != nil {
		t.Fatal(err)
	}
	view, err := h.ConfirmationStatus(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(view)
	if strings.Contains(string(body), "Include") || strings.Contains(string(body), "files") || strings.Contains(string(body), h.dropIn) {
		t.Fatalf("status leaked the private snapshot: %s", body)
	}
	info, err := os.Stat(filepath.Join(h.paths.Dir, recoveryFile))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("journal mode=%v,%v", info, err)
	}
}

// The backend applies, dies, and the standalone recovery entry point the timer
// runs restores sshd's files from the journal alone. Its systemctl is a fake
// on a PATH holding nothing else, so the host's own sshd is never touched.
func TestSSHRecoveryRunsInTheStandaloneHelperAfterTheBackendDies(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(dir, "systemctl.calls")
	script := "#!/bin/sh\necho \"$*\" >> " + calls + "\n"
	if err := os.WriteFile(filepath.Join(bin, "systemctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	invoke := func(mode string) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=^TestSSHRecoveryProcessFixture$")
		cmd.Env = append(os.Environ(), "JD_SSH_FIXTURE_DIR="+dir, "JD_SSH_FIXTURE_MODE="+mode)
		if mode == "recover" {
			cmd.Env = append(cmd.Env, "PATH="+bin)
		}
		return cmd
	}
	out, err := invoke("apply").CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 84 {
		t.Fatalf("backend fixture did not die after the SSH apply: %v %s", err, out)
	}
	dropIn := filepath.Join(dir, "ssh", "sshd_config.d", sshManagedDropIn)
	if got, _ := os.ReadFile(dropIn); string(got) != "PasswordAuthentication no\n" {
		t.Fatalf("candidate not in place before recovery: %q", got)
	}
	if out, err := invoke("recover").CombinedOutput(); err != nil {
		t.Fatalf("standalone recovery: %v %s", err, out)
	}
	if got, _ := os.ReadFile(dropIn); string(got) != "PasswordAuthentication yes\n" {
		t.Fatalf("previous drop-in not restored: %q", got)
	}
	if got, _ := os.ReadFile(calls); string(got) != "reload-or-restart ssh\n" {
		t.Fatalf("standalone helper ran %q", got)
	}
	j, err := readChange(filepath.Join(dir, "network"))
	if err != nil || j.Phase != "recovered" || j.Subsystem != "sshd" {
		t.Fatalf("journal=%+v,%v", j, err)
	}
}

func TestSSHRecoveryProcessFixture(t *testing.T) {
	dir := os.Getenv("JD_SSH_FIXTURE_DIR")
	if dir == "" {
		return
	}
	paths := Paths{Dir: filepath.Join(dir, "network"), Unit: filepath.Join(dir, "systemd", UnitName), SSH: filepath.Join(dir, "ssh")}
	if os.Getenv("JD_SSH_FIXTURE_MODE") == "recover" {
		if err := RecoverNetworkStandalone(context.Background(), paths.Dir, "pending"); err != nil {
			t.Fatal(err)
		}
		return
	}
	dropIn := filepath.Join(paths.SSH, "sshd_config.d", sshManagedDropIn)
	if err := os.MkdirAll(filepath.Dir(dropIn), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dropIn, []byte("PasswordAuthentication yes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	has = func(name string) bool { return name == "systemctl" || name == "systemd-run" }
	run = func(context.Context, string, ...string) (string, error) { return "", nil }
	s := New(Options{Paths: paths, IndependentRecovery: true})
	s.recoveryInstalled = true
	change, err := s.BeginSSHChange(WithPendingConfirmation(context.Background(), 7), []SSHFile{{Path: dropIn, Candidate: []byte("PasswordAuthentication no\n")}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dropIn, []byte("PasswordAuthentication no\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishSSHChange(context.Background(), change, nil); err != nil {
		t.Fatal(err)
	}
	os.Exit(84)
}
