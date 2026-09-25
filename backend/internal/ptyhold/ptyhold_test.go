package ptyhold

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// startHolder runs a holder in this process, as the unit would on the host.
func startHolder(t *testing.T) (socket string, done <-chan error) {
	t.Helper()
	socket = filepath.Join(t.TempDir(), "h.sock")
	errs := make(chan error, 1)
	go func() {
		h := &holder{clients: map[*client]bool{}}
		errs <- h.serve(socket, func() {})
	}()
	return socket, errs
}

// until reads packets until the output seen so far contains want.
func until(t *testing.T, c *Conn, want string) {
	t.Helper()
	buf := make([]byte, MaxPacket)
	deadline := time.Now().Add(10 * time.Second)
	c.conn.SetReadDeadline(deadline)
	defer c.conn.SetReadDeadline(time.Time{})
	seen := ""
	for !strings.Contains(seen, want) {
		kind, data, err := c.Read(buf)
		if err != nil {
			t.Fatalf("waiting for %q (saw %q): %v", want, seen, err)
		}
		if kind != Output {
			t.Fatalf("waiting for %q, got a %q packet", want, kind)
		}
		seen += string(data)
	}
}

// The whole promise: the session runs while nobody is attached, a dashboard
// that comes back is handed the same terminal with what it missed, the
// record it left is returned, and the end of the session reaches everybody.
func TestHolderKeepsTheSessionAcrossAttaches(t *testing.T) {
	socket, done := startHolder(t)
	script := `echo ready; read line; echo "got:$line"; read line; echo "late:$line"`
	first, hello, ptmx, err := Spawn(socket, Spec{
		Argv: []string{"/bin/sh", "-c", script},
		Env:  []string{"PATH=/usr/bin:/bin"},
		Rows: 24, Cols: 80,
		Meta: []byte(`{"name":"agent"}`),
	}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if hello.PID <= 0 || string(hello.Meta) != `{"name":"agent"}` {
		t.Fatalf("hello = %+v", hello)
	}
	until(t, first, "ready")

	// Keystrokes go straight to the master the holder handed over.
	if _, err := ptmx.Write([]byte("one\n")); err != nil {
		t.Fatal(err)
	}
	until(t, first, "got:one")
	if err := first.SetMeta([]byte(`{"name":"renamed"}`)); err != nil {
		t.Fatal(err)
	}
	if rows, cols, err := Size(ptmx); err != nil || rows != 24 || cols != 80 {
		t.Fatalf("Size = %dx%d, %v", rows, cols, err)
	}
	if err := SetSize(ptmx, 30, 100); err != nil {
		t.Fatal(err)
	}
	// Letting go is not ending: the dashboard restarting closes its end.
	first.Close()
	ptmx.Close()

	second, hello, ptmx, err := Attach(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	defer ptmx.Close()
	if hello.PID <= 0 || string(hello.Meta) != `{"name":"renamed"}` {
		t.Fatalf("second hello = %+v", hello)
	}
	if rows, cols, err := Size(ptmx); err != nil || rows != 30 || cols != 100 {
		t.Fatalf("Size after reattach = %dx%d, %v", rows, cols, err)
	}
	if past := string(hello.Past); !strings.Contains(past, "ready") || !strings.Contains(past, "got:one") {
		t.Fatalf("reattach replayed %q", past)
	}
	if _, err := ptmx.Write([]byte("two\n")); err != nil {
		t.Fatal(err)
	}
	until(t, second, "late:two")

	buf := make([]byte, MaxPacket)
	second.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	for {
		kind, _, err := second.Read(buf)
		if err != nil {
			t.Fatalf("the session ended without an exit: %v", err)
		}
		if kind == Exit {
			break
		}
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the holder did not exit with its session")
	}
	if _, err := os.Stat(socket); !os.IsNotExist(err) {
		t.Fatalf("the socket outlived the holder: %v", err)
	}
}

// Closing a session from the dashboard ends what runs in it, however long it
// meant to run.
func TestHangupEndsTheSession(t *testing.T) {
	socket, done := startHolder(t)
	conn, hello, ptmx, err := Spawn(socket, Spec{
		Argv: []string{"/bin/sh", "-c", "echo up; exec sleep 600"},
		Env:  []string{"PATH=/usr/bin:/bin"},
		Rows: 24, Cols: 80,
	}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	until(t, conn, "up")
	if err := conn.Hangup(); err != nil {
		t.Fatal(err)
	}
	ptmx.Close()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the holder did not exit after a hangup")
	}
	conn.Close()
	if err := syscall.Kill(hello.PID, 0); err == nil {
		t.Fatalf("process %d is still running after the hangup", hello.PID)
	}
}

// A command that cannot start is reported to the dashboard, not left for it
// to time out on.
func TestSpawnReportsAStartFailure(t *testing.T) {
	socket, done := startHolder(t)
	_, _, _, err := Spawn(socket, Spec{
		Argv: []string{filepath.Join(t.TempDir(), "missing")},
		Rows: 24, Cols: 80,
	}, 5*time.Second)
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("missing")) {
		t.Fatalf("Spawn error = %v, want the start failure", err)
	}
	if err := <-done; err == nil {
		t.Fatal("the holder did not report its failure")
	}
}
