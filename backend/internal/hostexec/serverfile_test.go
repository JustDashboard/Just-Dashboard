package hostexec

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"golang.org/x/sys/unix"
)

// inPrivateMounts runs fn on a thread with a mount namespace of its own, so
// the mounts it makes vanish with the thread. It needs root.
func inPrivateMounts(t *testing.T, fn func() error) error {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("making a mount namespace needs root")
	}
	done := make(chan error, 1)
	go func() {
		// Never unlocked: Go ends the thread, and its mounts with it, when
		// this goroutine returns.
		runtime.LockOSThread()
		if err := unix.Unshare(unix.CLONE_NEWNS); err != nil {
			done <- err
			return
		}
		if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
			done <- err
			return
		}
		done <- fn()
	}()
	return <-done
}

// busyLayout builds what the shipped stack does to the server's /etc/hosts:
// the directory mounted a second time (`- /etc:/etc`) and another file
// mounted over the copy of hosts inside it (Docker's own). It returns the
// path that reaches the server's file the other way, as /host/etc/hosts does.
func busyLayout(t *testing.T, dir, content string) string {
	t.Helper()
	server := filepath.Join(dir, "server-etc")
	container := filepath.Join(dir, "container-etc")
	for _, d := range []string{server, container} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(server, "hosts"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dockers-hosts"), []byte("docker's\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(server, "hosts")
}

func mountBusyLayout(dir string) error {
	if err := unix.Mount(filepath.Join(dir, "server-etc"), filepath.Join(dir, "container-etc"), "", unix.MS_BIND, ""); err != nil {
		return err
	}
	return unix.Mount(filepath.Join(dir, "dockers-hosts"), filepath.Join(dir, "container-etc", "hosts"), "", unix.MS_BIND, "")
}

func finished(t *testing.T, path, data string) string {
	t.Helper()
	f, err := os.CreateTemp(filepath.Dir(path), ".hosts.*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(data); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(f.Name()) })
	return f.Name()
}

func TestRenameOverWritesInPlaceWhereTheFileIsMountedOverElsewhere(t *testing.T) {
	for _, tc := range []struct{ name, before, after string }{
		{"longer", "127.0.0.1 localhost\n", "127.0.0.1 localhost\n127.0.1.1 luffy # Just Dashboard: server name\n"},
		{"shorter", "127.0.0.1 localhost\n127.0.1.1 old # Just Dashboard: server name\n", "127.0.0.1 localhost\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			hosts := busyLayout(t, dir, tc.before)
			before, err := os.Stat(hosts)
			if err != nil {
				t.Fatal(err)
			}
			err = inPrivateMounts(t, func() error {
				if err := mountBusyLayout(dir); err != nil {
					return err
				}
				// The layout reproduces the failure before it tests the cure.
				if err := os.Rename(finished(t, hosts, tc.after), hosts); !errors.Is(err, unix.EBUSY) {
					return errors.New("a plain rename was not refused as busy: " + errString(err))
				}
				return RenameOver(finished(t, hosts, tc.after), hosts, []byte(tc.after))
			})
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(hosts)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.after {
				t.Fatalf("hosts = %q, want %q", got, tc.after)
			}
			after, err := os.Stat(hosts)
			if err != nil {
				t.Fatal(err)
			}
			if !os.SameFile(before, after) {
				t.Fatal("the file was replaced rather than written in place")
			}
		})
	}
}

func TestRenameOverRenamesAnOrdinaryFile(t *testing.T) {
	hosts := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(hosts, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(hosts)
	tmp := finished(t, hosts, "new\n")
	if err := RenameOver(tmp, hosts, []byte("new\n")); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(hosts)
	if got, _ := os.ReadFile(hosts); string(got) != "new\n" {
		t.Fatalf("hosts = %q", got)
	}
	if os.SameFile(before, after) {
		t.Fatal("an ordinary file was written in place instead of replaced in one step")
	}
	if _, err := os.Stat(tmp); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the temporary file is still there")
	}
}

func TestRenameOverPassesOtherFailuresOn(t *testing.T) {
	dir := t.TempDir()
	err := RenameOver(filepath.Join(dir, "missing"), filepath.Join(dir, "hosts"), []byte("x\n"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want the rename's own", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "hosts")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a failed rename created the file")
	}
}

func TestOnServerPathWritesHereWhenThereIsNoOtherNamespace(t *testing.T) {
	if fd, err := hostMountNamespace(); err == nil && fd >= 0 {
		t.Skip("this process can enter another mount namespace")
	}
	for _, tc := range []struct{ real, mirrored string }{
		{"/etc/hosts", "/etc/hosts"},
		{"/etc/hosts", "/host/etc/hosts"},
	} {
		var at []string
		err := OnServerPath(tc.real, tc.mirrored, func(path string) error {
			at = append(at, path)
			return nil
		})
		if err != nil || len(at) != 1 || at[0] != tc.mirrored {
			t.Fatalf("OnServerPath(%q, %q) wrote %v, %v; want %q once", tc.real, tc.mirrored, at, err, tc.mirrored)
		}
	}
}

func TestIntoHostMountsDoesNotRunWithoutAnotherNamespace(t *testing.T) {
	if fd, err := hostMountNamespace(); err == nil && fd >= 0 {
		t.Skip("this process can enter another mount namespace")
	}
	ran, err := IntoHostMounts(func() { t.Fatal("fn ran in place") })
	if ran || err != nil {
		t.Fatalf("IntoHostMounts = %v, %v; want false, nil", ran, err)
	}
}

// TestIntoHostMountsRunsInTheHostsView runs where the backend does, like
// TestInHostMountsRunsInTheHostsView.
func TestIntoHostMountsRunsInTheHostsView(t *testing.T) {
	fd, err := hostMountNamespace()
	if err != nil || fd < 0 {
		t.Skip("no other mount namespace to enter")
	}
	host, err := os.Stat("/proc/1/ns/mnt")
	if err != nil {
		t.Fatal(err)
	}
	var inside os.FileInfo
	ran, err := IntoHostMounts(func() { inside, _ = os.Stat("/proc/thread-self/ns/mnt") })
	if !ran || err != nil {
		t.Fatalf("IntoHostMounts = %v, %v", ran, err)
	}
	if inside == nil || !os.SameFile(inside, host) {
		t.Fatal("fn did not run in the host's mount namespace")
	}
}

// TestOnServerPathReplacesAFileBusyInTheContainer is the shipped stack's own
// case, run by hand in a container laid out like the backend's (see the pull
// request that added it): JD_SERVERFILE_REAL is a file on the server that
// the container has mounted over, and JD_SERVERFILE_MIRRORED reaches it
// through /host.
func TestOnServerPathReplacesAFileBusyInTheContainer(t *testing.T) {
	real, mirrored := os.Getenv("JD_SERVERFILE_REAL"), os.Getenv("JD_SERVERFILE_MIRRORED")
	if real == "" || mirrored == "" {
		t.Skip("JD_SERVERFILE_REAL and JD_SERVERFILE_MIRRORED are not set")
	}
	if err := os.Rename(finished(t, mirrored, "x\n"), mirrored); !errors.Is(err, unix.EBUSY) {
		t.Fatalf("%s is not busy here: %v", mirrored, err)
	}
	before, err := os.Stat(mirrored)
	if err != nil {
		t.Fatal(err)
	}
	want := "replaced from the server's own namespace\n"
	err = OnServerPath(real, mirrored, func(path string) error {
		if path != real {
			t.Errorf("wrote %s, want %s", path, real)
		}
		f, err := os.CreateTemp(filepath.Dir(path), ".hosts.*")
		if err != nil {
			return err
		}
		defer os.Remove(f.Name())
		if _, err := f.WriteString(want); err != nil {
			return err
		}
		f.Close()
		return os.Rename(f.Name(), path)
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(mirrored); string(got) != want {
		t.Fatalf("%s = %q", mirrored, got)
	}
	after, _ := os.Stat(mirrored)
	if os.SameFile(before, after) {
		t.Fatal("the file was written in place, not replaced")
	}
}

func errString(err error) string {
	if err == nil {
		return "it succeeded"
	}
	return err.Error()
}
