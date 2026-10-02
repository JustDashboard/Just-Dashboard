package term

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
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
	m.SetHolderLauncherForTest(dir, testHolderLauncher)
	return m
}

func testHolderLauncher(_ context.Context, _ string, socket string) error {
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "JD_TEST_HOLDER="+socket)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

func TestDirectSessionRequiresAHolder(t *testing.T) {
	m := NewManager(true, "/bin/sh", "")
	_, err := m.Create(context.Background(), CreateOptions{})
	if !errors.Is(err, ErrPersistenceUnavailable) {
		t.Fatalf("Create without a holder = %v", err)
	}
	if len(m.List()) != 0 || m.pending != 0 {
		t.Fatal("refusing an unprotected session consumed a slot")
	}
}

func TestHolderSetupFailureExplainsWhyNewTerminalsAreUnavailable(t *testing.T) {
	m := NewManager(true, "/bin/sh", "")
	err := m.HoldSessions(filepath.Join(t.TempDir(), strings.Repeat("x", 120)))
	if err == nil {
		t.Fatal("holder setup accepted an unusable socket path")
	}
	if m.Holding() || !errors.Is(m.PersistenceError(), ErrPersistenceUnavailable) ||
		!errors.Is(m.PersistenceError(), err) {
		t.Fatalf("setup error was not retained: %v", m.PersistenceError())
	}
}

// A separate manager process is essential here: calling Shutdown alone cannot
// prove that a crash, or losing every descriptor in a container rebuild, is safe.
func runHeldManagerProcess(dir string) error {
	me, err := user.Current()
	if err != nil {
		return err
	}
	m := NewManager(true, "/bin/sh", me.Username)
	m.SetClipboardRootForTest(filepath.Join(dir, "clipboard"))
	if os.Getenv("JD_TERMINAL_SYSTEMD_LIVE") == "1" {
		if err := m.HoldSessions(dir); err != nil {
			return err
		}
	} else {
		m.SetHolderLauncherForTest(dir, testHolderLauncher)
	}
	first, err := m.Create(context.Background(), CreateOptions{Title: "agents", Owner: "tester"})
	if err != nil {
		return err
	}
	second, err := m.NewDirectWindow(context.Background(), first.WorkspaceID, "second", "", 24, 80)
	if err != nil {
		return err
	}
	sessions := []*Session{first, second}
	for _, sess := range sessions {
		marker := filepath.Join(dir, sess.ID+".finished")
		command := fmt.Sprintf("sleep 0.5; echo done > %q; echo offline-$((6*7))\n", marker)
		if _, err := sess.Write([]byte(command)); err != nil {
			return err
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(sessions); err != nil {
		return err
	}
	_, err = io.Copy(io.Discard, os.Stdin)
	m.Shutdown()
	return err
}

func TestHeldWindowsSurviveManagerProcessExit(t *testing.T) {
	live := os.Getenv("JD_TERMINAL_SYSTEMD_LIVE") == "1"
	if live && os.Geteuid() != 0 {
		t.Fatal("live systemd terminal tests must run as root")
	}
	for _, crash := range []bool{false, true} {
		name := "restart"
		if crash {
			name = "crash"
		}
		t.Run(name, func(t *testing.T) {
			dir, err := os.MkdirTemp("", "jd-held-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.RemoveAll(dir) })
			cmd := exec.Command(os.Args[0])
			cmd.Env = append(os.Environ(), "JD_TEST_TERMINAL_MANAGER="+dir)
			cmd.Stderr = os.Stderr
			in, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			out, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				in.Close()
				if cmd.ProcessState == nil {
					cmd.Process.Kill()
					cmd.Wait()
				}
			})
			// Recovery and cleanup own every holder even if an assertion fails.
			holders := dir
			if live {
				holders = filepath.Join(dir, "terminal")
			}
			back := heldManager(t, holders)
			t.Cleanup(func() {
				back.adopt()
				for _, sess := range back.List() {
					back.Kill(context.Background(), sess.ID)
				}
			})
			var started []*Session
			if err := json.NewDecoder(out).Decode(&started); err != nil {
				t.Fatal(err)
			}
			if len(started) != 2 {
				t.Fatalf("started %d windows, want two", len(started))
			}
			if live {
				// Rebuilding replaces the installed executable while old holders pin
				// its inode. A trailing byte changes the build without changing ELF.
				binary := filepath.Join(holders, holderName)
				original, err := os.ReadFile(binary)
				if err != nil {
					t.Fatal(err)
				}
				rebuilt := filepath.Join(dir, "rebuilt-holder")
				if err := os.WriteFile(rebuilt, append(original, 0), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := installHolder(rebuilt, binary); err != nil {
					t.Fatal(err)
				}
			}
			if crash {
				if err := cmd.Process.Kill(); err != nil {
					t.Fatal(err)
				}
			} else {
				in.Close()
			}
			if err := cmd.Wait(); err != nil && !crash {
				t.Fatal(err)
			}
			for _, previous := range started {
				deadline := time.Now().Add(10 * time.Second)
				for {
					if data, err := os.ReadFile(filepath.Join(dir, previous.ID+".finished")); err == nil && string(data) == "done\n" {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("work did not finish while the manager and all viewers were gone")
					}
					time.Sleep(10 * time.Millisecond)
				}
				if err := syscall.Kill(previous.PID, 0); err != nil {
					t.Fatalf("terminal ended with manager: %v", err)
				}
			}
			if live {
				if err := back.HoldSessions(dir); err != nil {
					t.Fatal(err)
				}
			} else {
				back.adopt()
			}
			for _, previous := range started {
				sess, err := back.Get(previous.ID)
				if err != nil {
					t.Fatal(err)
				}
				if sess.PID != previous.PID || sess.WorkspaceID != previous.WorkspaceID || sess.WindowName != previous.WindowName {
					t.Fatalf("window identity changed: %+v", sess)
				}
				// Advance maintenance past the old timeout while no browser is attached.
				sess.mu.Lock()
				sess.lastActive = time.Now().Add(-48 * time.Hour)
				sess.mu.Unlock()
				back.cleanupClipboardAt(time.Now().Add(72 * time.Hour))
				snapshot, id, output, err := sess.Subscribe()
				if err != nil {
					t.Fatal(err)
				}
				await(t, output, string(snapshot), "offline-42")
				if _, err := sess.Write([]byte("echo recovered-$((6*7+1))\n")); err != nil {
					t.Fatal(err)
				}
				await(t, output, "", "recovered-43")
				sess.Unsubscribe(id)
			}
		})
	}
}

func TestHolderSetupFailureStillAdoptsRunningWindows(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("holder setup requires root")
	}
	dir, err := os.MkdirTemp("", "jd-held-adopt-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	holders := filepath.Join(dir, "terminal")
	if err := os.Mkdir(holders, 0o700); err != nil {
		t.Fatal(err)
	}
	first := heldManager(t, holders)
	sess, err := first.Create(context.Background(), CreateOptions{Title: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	first.Shutdown()
	back := heldManager(t, holders)
	t.Cleanup(func() { back.Kill(context.Background(), sess.ID) })
	// Make installing a new binary fail even when one is beside the test binary.
	if err := os.Mkdir(filepath.Join(holders, holderName), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := back.HoldSessions(dir); err == nil || back.Holding() {
		t.Fatal("new holder setup should have failed")
	}
	recovered, err := back.Get(sess.ID)
	if err != nil {
		t.Fatalf("setup failure hid existing work: %v", err)
	}
	if recovered.PID != sess.PID {
		t.Fatal("setup failure replaced the running shell")
	}
	if _, err := back.Create(context.Background(), CreateOptions{}); !errors.Is(err, ErrPersistenceUnavailable) {
		t.Fatalf("setup failure allowed an unprotected terminal: %v", err)
	}
	snapshot, id, output, err := recovered.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Unsubscribe(id)
	if _, err := recovered.Write([]byte("echo survived-$((6*7))\n")); err != nil {
		t.Fatal(err)
	}
	await(t, output, string(snapshot), "survived-42")
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
