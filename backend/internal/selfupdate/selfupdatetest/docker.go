// Package selfupdatetest stands in for the docker CLI that the lifecycle
// operations (update, restart, uninstall) start their sibling containers with,
// so a test can see which daemon each call was aimed at without one being
// there.
package selfupdatetest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// InheritedHost is the DOCKER_HOST the fake puts in the process environment:
// the daemon a CLI reaches when it is not told JD_DOCKER_HOST. A call recorded
// with it is a call that ignored the setting.
const InheritedHost = "unix:///var/run/docker.sock"

// Docker puts a `docker` first on PATH that records each call as
// "<DOCKER_HOST>|<args>" and does nothing else. When DOCKER_HOST names a socket
// with "missing" in it, the call fails the way the real CLI does against a
// daemon that is not there. It returns a reader of the calls, oldest first.
func Docker(t testing.TB) func() []string {
	t.Helper()
	bin := t.TempDir()
	record := filepath.Join(bin, "calls")
	script := "#!/bin/sh\n" +
		"printf '%s|%s\\n' \"${DOCKER_HOST-unset}\" \"$*\" >> '" + record + "'\n" +
		"case \"$DOCKER_HOST\" in *missing*)\n" +
		"  echo \"failed to connect to the docker API at $DOCKER_HOST; check if the path is correct and if the daemon is running\" >&2\n" +
		"  exit 1;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOCKER_HOST", InheritedHost)
	return func() []string {
		b, err := os.ReadFile(record)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			t.Fatal(err)
		}
		return strings.Split(strings.TrimSpace(string(b)), "\n")
	}
}

// MissingHost is a unix socket that does not exist, which the fake answers as
// a daemon that cannot be reached.
func MissingHost(t testing.TB) string {
	t.Helper()
	return "unix://" + filepath.Join(t.TempDir(), "missing.sock")
}

// AimedAt describes the first way calls differ from want — argument prefixes,
// one per call, every one aimed at host — or returns "" when they match.
func AimedAt(calls []string, host string, want ...string) string {
	if len(calls) != len(want) {
		return fmt.Sprintf("calls = %q, want %d aimed at %s: %q", calls, len(want), host, want)
	}
	for n, call := range calls {
		if !strings.HasPrefix(call, host+"|"+want[n]) {
			return fmt.Sprintf("call %d = %q, want %q aimed at %s", n, call, want[n], host)
		}
	}
	return ""
}
