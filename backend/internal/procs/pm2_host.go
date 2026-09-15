package procs

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// pm2Home is one account's PM2 installation on the host: where its daemon
// socket lives and which binary talks to it.
//
// PM2 is per-user, not per-host. The daemon's socket sits under the account's
// own home ($HOME/.pm2) and the binary itself is routinely per-user too — an
// nvm install lands in ~/.nvm/versions/node/<version>/bin/pm2, which is on
// that account's PATH and nobody else's. The dashboard runs as root in a
// container whose own PATH has neither, so a plain `pm2 jlist` fails twice
// over: "command not found" even though the host has PM2, and — were the
// binary found — it would ask root's (empty) daemon instead of the account
// that actually runs something. Both halves have to be addressed together or
// the tab still reports "not installed" in front of running processes.
type pm2Home struct {
	// home is the account's home directory, e.g. /home/deploy.
	home string
	// bin is the PM2 binary that serves it. Absolute when a per-user copy was
	// found, plain "pm2" when only the host PATH has one.
	bin string
}

// hostPathRoots are the directories bind-mounted from the host under the same
// absolute path, so a container-local filesystem check answers for the host.
// /root is mounted as well (see docker-compose.yml); a missing entry is
// skipped rather than failing discovery.
func pm2HomeDirs() []string {
	dirs := []string{"/root"}
	entries, err := os.ReadDir("/home")
	if err != nil {
		return dirs
	}
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			dirs = append(dirs, filepath.Join("/home", e.Name()))
		}
	}
	return dirs
}

// findPM2Bin locates a PM2 binary that runs as home's owner on the host. The
// nvm layout is checked first because that is where a user-scoped install
// lives; the system paths cover a global npm install. The newest nvm version
// wins when several are present — an old toolchain directory is routinely
// left behind after an upgrade.
func findPM2Bin(home string) string {
	matches, _ := filepath.Glob(filepath.Join(home, ".nvm", "versions", "node", "*", "bin", "pm2"))
	// Glob returns matches in lexical order, which for version directories is
	// oldest-first; the newest toolchain is the one whose node actually runs.
	sort.Strings(matches)
	for i := len(matches) - 1; i >= 0; i-- {
		if st, err := os.Stat(matches[i]); err == nil && !st.IsDir() {
			return matches[i]
		}
	}
	for _, p := range []string{
		filepath.Join(home, ".local", "bin", "pm2"),
		"/usr/local/bin/pm2",
		"/usr/bin/pm2",
	} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// discoverPM2Homes lists the host accounts that have a PM2 daemon worth
// asking. A home qualifies on either half of the installation: a daemon
// directory (it runs or ran something) or a binary (it can). Pure filesystem
// checks, so this stays cheap enough to run on every poll.
func discoverPM2Homes() []pm2Home {
	return discoverPM2HomesIn(pm2HomeDirs())
}

func discoverPM2HomesIn(dirs []string) []pm2Home {
	var out []pm2Home
	for _, home := range dirs {
		bin := findPM2Bin(home)
		daemon := false
		if st, err := os.Stat(filepath.Join(home, ".pm2")); err == nil && st.IsDir() {
			daemon = true
		}
		if bin == "" && !daemon {
			continue
		}
		if bin == "" {
			bin = "pm2"
		}
		out = append(out, pm2Home{home: home, bin: bin})
	}
	return out
}

// pm2Env runs a PM2 CLI against one account's daemon. HOME is what selects
// the daemon — PM2 resolves its socket from it — and PM2_HOME pins that down
// explicitly. PATH leads with the binary's own directory so the `node` on its
// shebang line resolves to the toolchain that installed it rather than to
// whatever (if anything) the container carries.
func pm2Env(home pm2Home) []string {
	env := os.Environ()
	// Strip any inherited PM2/HOME/PATH entries; duplicates would leave the
	// resolution order to whichever the runtime consults first.
	kept := env[:0]
	for _, kv := range env {
		if strings.HasPrefix(kv, "HOME=") ||
			strings.HasPrefix(kv, "PM2_HOME=") ||
			strings.HasPrefix(kv, "PATH=") {
			continue
		}
		kept = append(kept, kv)
	}
	const hostPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	path := hostPath
	if dir := filepath.Dir(home.bin); dir != "" && dir != "." {
		path = dir + ":" + hostPath
	}
	return append(kept,
		"HOME="+home.home,
		"PM2_HOME="+filepath.Join(home.home, ".pm2"),
		"PATH="+path,
	)
}

// runPM2Host executes one PM2 CLI against one account's daemon with its own
// timeout. hostexec crosses into the host's namespaces when this process runs
// containerised (where the host's nvm tree is visible but this image's PATH
// is not) and runs directly otherwise, so a bare-metal install behaves
// identically.
func runPM2Host(ctx context.Context, home pm2Home, timeout time.Duration, args ...string) (*CommandResult, error) {
	return runWithEnv(ctx, home.bin, pm2Env(home), timeout, args...)
}
