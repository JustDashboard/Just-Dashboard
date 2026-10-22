package updates

import (
	"os"
	"path/filepath"
	"syscall"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// aptLockWait makes a mutating apt-get wait for the dpkg lock instead of
// failing on it at once. unattended-upgrades holds it for minutes at a time,
// and "Could not get lock /var/lib/dpkg/lock-frontend" is a refusal the
// operator can do nothing about except click again later.
var aptLockWait = []string{"-o", "DPkg::Lock::Timeout=600"}

// finishDpkgScript completes what an interrupted dpkg left before the
// operator's command runs, in the same process tree so nothing can separate
// the two. It is a constant: the command arrives as positional arguments,
// never as text in the script.
const finishDpkgScript = `dpkg --force-confold --configure -a || { s=$?; echo "dpkg --configure -a exited $s, so $1 did not run" >&2; exit $s; }; exec "$@"`

// DpkgInterrupted reports whether an earlier dpkg run stopped partway, which
// makes every apt-get that changes anything refuse with "dpkg was
// interrupted, you must manually run 'dpkg --configure -a'".
func (s *Service) DpkgInterrupted() bool {
	m := detect()
	return m != nil && m.Name() == "apt" && dpkgInterrupted(hostexec.HostPath("/var/lib/dpkg"))
}

// dpkgInterrupted is apt's own test — a numbered journal entry under
// updates/ — but only while nothing holds dpkg's locks: a dpkg running right
// now keeps its journal there too, and finishing its work from underneath it
// is not something to do.
func dpkgInterrupted(admin string) bool {
	entries, err := os.ReadDir(filepath.Join(admin, "updates"))
	if err != nil {
		return false
	}
	journal := false
	for _, e := range entries {
		if allDigits(e.Name()) {
			journal = true
			break
		}
	}
	return journal && !locked(filepath.Join(admin, "lock-frontend")) && !locked(filepath.Join(admin, "lock"))
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// locked asks whether another process holds a write lock on path, the kind
// apt and dpkg take. A file that cannot be opened is not held by anybody.
func locked(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	lk := syscall.Flock_t{Type: syscall.F_WRLCK, Whence: 0}
	if err := syscall.FcntlFlock(f.Fd(), syscall.F_GETLK, &lk); err != nil {
		return false
	}
	return lk.Type != syscall.F_UNLCK
}

// FinishingDpkgFirst wraps an apt command so it first completes an
// interrupted dpkg run. Both halves have to be one command: finishing dpkg is
// usually configuring docker-ce, which restarts Docker, and a dashboard
// running them as two would be stopped between them.
func FinishingDpkgFirst(name string, args []string) (string, []string) {
	return "sh", append([]string{"-c", finishDpkgScript, "sh", name}, args...)
}
