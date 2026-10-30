package updates

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDpkgInterruptedReadsTheJournalAsAptDoes(t *testing.T) {
	admin := t.TempDir()
	if dpkgInterrupted(admin) {
		t.Fatal("no updates directory at all reads as interrupted")
	}
	updates := filepath.Join(admin, "updates")
	if err := os.Mkdir(updates, 0o755); err != nil {
		t.Fatal(err)
	}
	// dpkg's own temporary file is not a journal entry.
	if err := os.WriteFile(filepath.Join(updates, "tmp.i"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if dpkgInterrupted(admin) {
		t.Fatal("tmp.i alone reads as interrupted")
	}
	if err := os.WriteFile(filepath.Join(updates, "0006"), []byte("Package: docker-ce\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !dpkgInterrupted(admin) {
		t.Fatal("a numbered journal entry with nothing running is the interrupted state apt refuses on")
	}
}

// A dpkg running right now has journal entries too; finishing its work from
// underneath it would be the damage this exists to repair.
func TestDpkgInterruptedIsNotARunningDpkg(t *testing.T) {
	admin := t.TempDir()
	if err := os.MkdirAll(filepath.Join(admin, "updates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(admin, "updates", "0000"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"lock-frontend", "lock"} {
		t.Run(name, func(t *testing.T) {
			f, err := os.OpenFile(filepath.Join(admin, name), os.O_RDWR|os.O_CREATE, 0o640)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			// An open-file-description lock, so this process's own F_GETLK
			// sees it the way it sees apt's from another process.
			lk := unix.Flock_t{Type: unix.F_WRLCK}
			if err := unix.FcntlFlock(f.Fd(), unix.F_OFD_SETLK, &lk); err != nil {
				t.Skipf("no OFD locks here: %v", err)
			}
			if dpkgInterrupted(admin) {
				t.Errorf("%s is held, and it still reads as interrupted", name)
			}
		})
	}
	if !dpkgInterrupted(admin) {
		t.Error("released locks still read as held")
	}
}

// The wrapper is a shell script, so it is run rather than read: the
// operator's command must run after dpkg succeeds, with its arguments intact,
// and not at all after dpkg fails.
func TestFinishingDpkgFirst(t *testing.T) {
	bin := t.TempDir()
	marker := filepath.Join(bin, "configured")
	dpkg := "#!/bin/sh\n[ \"$*\" = \"--force-confold --configure -a\" ] || exit 9\ntouch " + marker + "\nexit ${DPKG_EXIT:-0}\n"
	if err := os.WriteFile(filepath.Join(bin, "dpkg"), []byte(dpkg), 0o755); err != nil {
		t.Fatal(err)
	}
	name, args := FinishingDpkgFirst("printf", []string{"%s|", "install", "a b", "-o", "x=y"})
	run := func(dpkgExit string) (string, string, int) {
		cmd := exec.Command(name, args...)
		cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "DPKG_EXIT="+dpkgExit)
		var stdout, stderr strings.Builder
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		_ = cmd.Run()
		return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
	}

	out, _, code := run("0")
	if code != 0 || out != "install|a b|-o|x=y|" {
		t.Errorf("after dpkg succeeded: code %d, output %q", code, out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Error("dpkg --configure -a did not run first")
	}

	out, stderr, code := run("2")
	if code != 2 || out != "" {
		t.Errorf("after dpkg failed: code %d, output %q", code, out)
	}
	if !strings.Contains(stderr, "dpkg --configure -a exited 2, so printf did not run") {
		t.Errorf("stderr = %q", stderr)
	}
}

// Every apt command that takes dpkg's lock waits for it, so an operator who
// clicks while unattended-upgrades runs gets a wait rather than a refusal.
func TestAptMutationsWaitForTheLock(t *testing.T) {
	wait := strings.Join(aptLockWait, " ")
	_, upgrade, _ := aptManager{}.UpgradeCommand(false)
	_, security, _ := aptManager{}.UpgradeCommand(true)
	_, install, _ := aptManager{}.InstallCommand([]string{"htop"})
	_, remove, _ := aptManager{}.RemoveCommand([]string{"htop"}, true)
	for name, args := range map[string][]string{"upgrade": upgrade, "security": security, "install": install, "remove": remove} {
		if !strings.Contains(strings.Join(args, " "), wait) {
			t.Errorf("%s does not wait for the lock: %q", name, args)
		}
	}
}
