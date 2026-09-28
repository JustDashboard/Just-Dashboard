package gitx

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCloneBranchDepthAndSparseAgainstGitDaemon(t *testing.T) {
	dir, git := tempRepo(t)
	git("checkout", "-qb", "feature")
	write(t, dir, "src/app.txt", "application\n")
	write(t, dir, "other/large.txt", "not selected\n")
	git("add", "-A")
	git("commit", "-qm", "feature files")
	git("config", "uploadpack.allowFilter", "true")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	cmd := exec.Command("git", "daemon", "--listen=127.0.0.1", fmt.Sprintf("--port=%d", port),
		"--export-all", "--base-path="+filepath.Dir(dir), "--reuseaddr")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	address := fmt.Sprintf("127.0.0.1:%d", port)
	ready := false
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		conn, err := net.DialTimeout("tcp", address, 50*time.Millisecond)
		if err == nil {
			conn.Close()
			ready = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ready {
		t.Fatal("temporary git daemon did not start")
	}
	destination := t.TempDir()
	s := New([]string{destination})
	target, result, err := s.CloneWithOptions(context.Background(), destination,
		"git://"+address+"/"+filepath.Base(dir), "checkout", CloneOptions{Branch: "feature", Depth: 1, Sparse: []string{"src"}})
	if err != nil {
		t.Fatalf("clone: %+v, %v", result, err)
	}
	if data, err := os.ReadFile(filepath.Join(target, "src/app.txt")); err != nil || string(data) != "application\n" {
		t.Fatalf("selected file: %s, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(target, "other/large.txt")); !os.IsNotExist(err) {
		t.Fatalf("sparse checkout included other folder: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, ".git/shallow")); err != nil {
		t.Fatal("clone is not shallow:", err)
	}
	branch, err := s.CurrentBranch(context.Background(), target)
	if err != nil || branch != "feature" {
		t.Fatalf("branch %q, %v", branch, err)
	}
	commits, err := s.Log(context.Background(), target, LogQuery{})
	if err != nil || len(commits) != 1 {
		t.Fatalf("depth: %+v, %v", commits, err)
	}
}

func TestSignatureReportsUncheckedAndTrustedSSHKeys(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen is not installed")
	}
	dir, git := tempRepo(t)
	key := filepath.Join(t.TempDir(), "key")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("keygen: %s, %v", out, err)
	}
	git("-c", "gpg.format=ssh", "-c", "user.signingkey="+key, "-c", "commit.gpgsign=true", "commit", "--allow-empty", "-qm", "signed")
	s := New([]string{dir})
	sig, err := s.Signature(context.Background(), dir, "HEAD")
	if err != nil || sig.Status != "E" {
		t.Fatalf("unchecked signature: %+v, %v", sig, err)
	}
	pub, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	allowed := filepath.Join(t.TempDir(), "allowed")
	if err := os.WriteFile(allowed, []byte("t@e "+strings.TrimSpace(string(pub))+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("config", "gpg.ssh.allowedSignersFile", allowed)
	sig, err = s.Signature(context.Background(), dir, "HEAD")
	if err != nil || sig.Status != "G" || sig.Signer != "t@e" || sig.Fingerprint == "" {
		t.Fatalf("trusted signature: %+v, %v", sig, err)
	}
}
