package term

import (
	"context"
	"os"
	"os/exec"
	"os/user"
	"strings"
	"syscall"
	"testing"
	"time"
)

// heldManager is a manager whose holders are this test binary, started as a
// detached process of its own rather than as a systemd unit.
func heldManager(t *testing.T, dir string) *Manager {
	t.Helper()
	me, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(true, "/bin/sh", me.Username)
	if _, err := m.Account(); err != nil {
		t.Skipf("no account to open a session as: %v", err)
	}
	m.SetClipboardRootForTest(t.TempDir())
	m.holders = dir
	m.launch = func(_ context.Context, _ string, socket string) error {
		cmd := exec.Command(os.Args[0])
		cmd.Env = append(os.Environ(), "JD_TEST_HOLDER="+socket)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			return err
		}
		go cmd.Wait()
		return nil
	}
	return m
}

// await reads a subscription until its output contains want.
func await(t *testing.T, out <-chan []byte, seen, want string) string {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for !strings.Contains(seen, want) {
		select {
		case chunk, ok := <-out:
			if !ok {
				t.Fatalf("output ended waiting for %q; saw %q", want, seen)
			}
			seen += string(chunk)
		case <-timeout:
			t.Fatalf("timed out waiting for %q; saw %q", want, seen)
		}
	}
	return seen
}

// The reason held sessions exist: the dashboard going away — a restart, an
// upgrade — leaves the shell running, the next dashboard takes it back under
// the same name with what it printed meanwhile, and closing it still ends it.
func TestHeldSessionOutlivesTheManager(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	first := heldManager(t, dir)
	sess, err := first.Create(ctx, CreateOptions{Title: "agent", Owner: "tester", Rows: 24, Cols: 80})
	if err != nil {
		t.Fatal(err)
	}
	if sess.holder == nil {
		t.Fatal("the session was not held")
	}
	_, id, out, err := sess.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Write([]byte("echo marker-$((6*7))\n")); err != nil {
		t.Fatal(err)
	}
	await(t, out, "", "marker-42")
	sess.Unsubscribe(id)
	// Printed while no dashboard is there to read it.
	if _, err := sess.Write([]byte("sleep 1; echo meanwhile-$((6*7+1))\n")); err != nil {
		t.Fatal(err)
	}
	first.Shutdown()
	if err := syscall.Kill(sess.PID, 0); err != nil {
		t.Fatalf("the shell ended with the manager: %v", err)
	}
	time.Sleep(1500 * time.Millisecond)

	second := heldManager(t, dir)
	second.adopt()
	back, err := second.Get(sess.ID)
	if err != nil {
		t.Fatalf("the new manager did not take the session back: %v", err)
	}
	if meta := back.Meta(); meta.Title != "agent" || !meta.Named {
		t.Fatalf("title came back as %+v", meta)
	}
	if back.WorkspaceID != sess.WorkspaceID || back.WindowName != sess.WindowName ||
		back.Owner != "tester" || back.PID != sess.PID {
		t.Fatalf("adopted %+v, want the session %+v", back, sess)
	}
	snapshot, _, out, err := back.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(snapshot), "marker-42") ||
		!strings.Contains(string(snapshot), "meanwhile-43") {
		t.Fatalf("history after adoption = %q", snapshot)
	}
	if _, err := back.Write([]byte("echo again-$((6*7+2))\n")); err != nil {
		t.Fatal(err)
	}
	await(t, out, "", "again-44")

	if err := second.KillWorkspace(ctx, sess.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for syscall.Kill(sess.PID, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("shell %d is still running after its session was closed", sess.PID)
		}
		time.Sleep(50 * time.Millisecond)
	}
	for {
		if _, err := os.Stat(dir + "/" + sess.ID + ".sock"); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the holder outlived its session")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A socket nobody answers on is what a holder killed outright leaves behind,
// and adoption clears it rather than tripping over it at every boot.
func TestAdoptClearsAbandonedSockets(t *testing.T) {
	dir := t.TempDir()
	m := heldManager(t, dir)
	stale := dir + "/0123456789abcdef.sock"
	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_SEQPACKET, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Bind(fd, &syscall.SockaddrUnix{Name: stale}); err != nil {
		t.Fatal(err)
	}
	syscall.Close(fd)
	m.adopt()
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale socket survived adoption: %v", err)
	}
	if len(m.List()) != 0 {
		t.Fatalf("adopted %d sessions from a dead socket", len(m.List()))
	}
}
