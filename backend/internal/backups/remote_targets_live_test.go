package backups

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store/storetest"
)

// SFTP and rclone destinations, end to end against real servers: OpenSSH's
// sftp (atmoz/sftp) and a WebDAV server run by rclone itself. Each job is
// tested, backed up twice with a retention of one, and restored; the SFTP one
// is then pointed at the same address answering with new host keys, which a
// run must refuse. The rclone half needs rclone on PATH, as the backend image
// ships it.
func TestLiveSFTPAndRcloneBackupsUploadPruneAndRestore(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 to back up to real SFTP and WebDAV servers")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	docker := func(args ...string) string {
		t.Helper()
		raw, err := hostexec.Command(ctx, "docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("docker %s: %v: %s", strings.Join(args, " "), err, raw)
		}
		return strings.TrimSpace(string(raw))
	}
	secret := func() string {
		b := make([]byte, 12)
		_, _ = rand.Read(b)
		return hex.EncodeToString(b)
	}
	newStore := func() *Store {
		db, err := storetest.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		sealer, err := auth.NewSealer(strings.Repeat("5c", 32))
		if err != nil {
			t.Fatal(err)
		}
		return NewStore(db, sealer)
	}

	t.Run("sftp", func(t *testing.T) {
		password := secret()
		port := liveBackupFreePort(t)
		name := fmt.Sprintf("jd-sftp-backup-%d", time.Now().UnixNano())
		start := func() {
			docker("run", "-d", "--name", name, "-p", fmt.Sprintf("127.0.0.1:%d:22", port), "atmoz/sftp:debian", "ops:"+password+":1001::backups")
		}
		start()
		defer hostexec.Command(context.Background(), "docker", "rm", "-f", "-v", name).Run()
		liveWaitForBanner(t, ctx, port)

		backups := newStore()
		source := t.TempDir()
		if err := os.WriteFile(filepath.Join(source, "notes.txt"), []byte("first revision"), 0o600); err != nil {
			t.Fatal(err)
		}
		secrets := &TargetSecrets{Password: password}
		created, err := backups.Create(ctx, &Job{
			Name: "sftp", Sources: []string{source}, Enabled: true, Schedule: "0 3 * * *", Retention: 1,
			TargetKind: TargetSFTP, Target: TargetConfig{Host: "127.0.0.1", Port: port, Username: "ops", Path: "backups/nightly"},
		}, secrets)
		if err != nil {
			t.Fatal(err)
		}
		if err := TestTarget(ctx, created, secrets, backups.Pinner(created.ID)); err != nil {
			t.Fatal(err)
		}
		// The key pinned is one of the keys this server holds.
		served := docker("exec", name, "sh", "-c", "for k in /etc/ssh/ssh_host_*_key.pub; do ssh-keygen -lf $k; done")
		pinned, err := backups.Get(ctx, created.ID)
		if err != nil || pinned.Target.HostKeyFingerprint == "" || !strings.Contains(served, pinned.Target.HostKeyFingerprint) {
			t.Fatalf("pinned %q, the server holds\n%s", pinned.Target.HostKeyFingerprint, served)
		}

		stage := t.TempDir()
		runner := NewRunner(backups, stage, slog.New(slog.NewTextHandler(io.Discard, nil)))
		first, err := runner.Execute(ctx, created.ID, "test")
		if err != nil || first.Status != StatusSuccess || !strings.HasPrefix(first.Artifact, "backups/nightly/") {
			t.Fatalf("first run = %+v, %v\n%s", first, err, first.Log)
		}
		remote := func(artifact string) bool {
			return hostexec.Command(ctx, "docker", "exec", name, "test", "-f", "/home/ops/"+artifact).Run() == nil
		}
		if !remote(first.Artifact) || remote(first.Artifact+".partial") {
			t.Fatalf("%s is not on the server", first.Artifact)
		}
		if entries, _ := os.ReadDir(stage); len(entries) != 0 {
			t.Fatalf("staging copy left behind: %v", entries)
		}
		if err := os.WriteFile(filepath.Join(source, "notes.txt"), []byte("second revision"), 0o600); err != nil {
			t.Fatal(err)
		}
		time.Sleep(1100 * time.Millisecond)
		second, err := runner.Execute(ctx, created.ID, "test")
		if err != nil || second.Status != StatusSuccess || second.Artifact == first.Artifact {
			t.Fatalf("second run = %+v, %v\n%s", second, err, second.Log)
		}
		if remote(first.Artifact) || !remote(second.Artifact) {
			t.Fatalf("retention of one kept the wrong archives: first=%v second=%v", remote(first.Artifact), remote(second.Artifact))
		}
		liveRestoredNotes(t, ctx, runner, second.ID, "second revision")

		// The same address, new host keys: the run refuses, and says why.
		docker("rm", "-f", "-v", name)
		start()
		liveWaitForBanner(t, ctx, port)
		time.Sleep(1100 * time.Millisecond)
		third, err := runner.Execute(ctx, created.ID, "test")
		if err == nil && third.Status == StatusSuccess {
			t.Fatal("a run reached a server whose host key changed")
		}
		if third == nil || !strings.Contains(third.Log, "host key changed") {
			t.Fatalf("third run = %+v, %v", third, err)
		}
	})

	t.Run("rclone", func(t *testing.T) {
		if _, err := exec.LookPath("rclone"); err != nil {
			t.Skip("rclone is not on PATH")
		}
		password := secret()
		port := liveBackupFreePort(t)
		name := fmt.Sprintf("jd-webdav-backup-%d", time.Now().UnixNano())
		docker("run", "-d", "--name", name, "-p", fmt.Sprintf("127.0.0.1:%d:8080", port), "rclone/rclone:1.75.2",
			"serve", "webdav", "/data", "--addr", ":8080", "--user", "ops", "--pass", password)
		defer hostexec.Command(context.Background(), "docker", "rm", "-f", "-v", name).Run()
		for deadline := time.Now().Add(time.Minute); ; {
			response, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
			if err == nil {
				response.Body.Close()
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("the WebDAV server did not come up: %v", err)
			}
			time.Sleep(500 * time.Millisecond)
		}
		obscured, err := exec.CommandContext(ctx, "rclone", "obscure", password).Output()
		if err != nil {
			t.Fatal(err)
		}
		config := fmt.Sprintf("[davserver]\ntype = webdav\nurl = http://127.0.0.1:%d\nvendor = other\nuser = ops\npass = %s\n", port, strings.TrimSpace(string(obscured)))
		secrets := &TargetSecrets{RcloneConfig: config}

		backups := newStore()
		source := t.TempDir()
		if err := os.WriteFile(filepath.Join(source, "notes.txt"), []byte("first revision"), 0o600); err != nil {
			t.Fatal(err)
		}
		created, err := backups.Create(ctx, &Job{
			Name: "rclone", Sources: []string{source}, Enabled: true, Schedule: "0 3 * * *", Retention: 1,
			TargetKind: TargetRclone, Target: TargetConfig{Path: "backups/nightly"},
		}, secrets)
		if err != nil {
			t.Fatal(err)
		}
		if created.Target.RemoteType != "webdav" {
			t.Fatalf("remote type = %q", created.Target.RemoteType)
		}
		if err := TestTarget(ctx, created, secrets, nil); err != nil {
			t.Fatal(err)
		}
		wrong := &TargetSecrets{RcloneConfig: strings.Replace(config, "user = ops", "user = intruder", 1)}
		if err := TestTarget(ctx, created, wrong, nil); err == nil || !strings.Contains(err.Error(), "rclone") {
			t.Fatalf("a wrong account passed the test: %v", err)
		}
		runner := NewRunner(backups, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
		first, err := runner.Execute(ctx, created.ID, "test")
		if err != nil || first.Status != StatusSuccess || !strings.HasPrefix(first.Artifact, "backups/nightly/") {
			t.Fatalf("first run = %+v, %v\n%s", first, err, first.Log)
		}
		remote := func(artifact string) bool {
			return hostexec.Command(ctx, "docker", "exec", name, "test", "-f", "/data/"+artifact).Run() == nil
		}
		if !remote(first.Artifact) {
			t.Fatalf("%s is not on the WebDAV server", first.Artifact)
		}
		if err := os.WriteFile(filepath.Join(source, "notes.txt"), []byte("second revision"), 0o600); err != nil {
			t.Fatal(err)
		}
		time.Sleep(1100 * time.Millisecond)
		second, err := runner.Execute(ctx, created.ID, "test")
		if err != nil || second.Status != StatusSuccess {
			t.Fatalf("second run = %+v, %v\n%s", second, err, second.Log)
		}
		if remote(first.Artifact) || !remote(second.Artifact) {
			t.Fatalf("retention of one kept the wrong archives: first=%v second=%v", remote(first.Artifact), remote(second.Artifact))
		}
		liveRestoredNotes(t, ctx, runner, second.ID, "second revision")
	})
}

func liveBackupFreePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

// liveWaitForBanner waits until sshd says hello, which is when it accepts a
// session rather than only the socket.
func liveWaitForBanner(t *testing.T, ctx context.Context, port int) {
	t.Helper()
	for ctx.Err() == nil {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
		if err == nil {
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			banner := make([]byte, 4)
			n, _ := io.ReadFull(conn, banner)
			conn.Close()
			if n == 4 && string(banner) == "SSH-" {
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatal("sshd never answered")
}

func liveRestoredNotes(t *testing.T, ctx context.Context, runner *Runner, runID int64, want string) {
	t.Helper()
	destination := t.TempDir()
	result, err := runner.Restore(ctx, runID, destination)
	if err != nil || result.Entries == 0 {
		t.Fatalf("restore = %+v, %v", result, err)
	}
	var restored []byte
	if err := filepath.WalkDir(destination, func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && entry.Name() == "notes.txt" {
			restored, err = os.ReadFile(path)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if string(restored) != want {
		t.Fatalf("restored notes.txt = %q", restored)
	}
}
