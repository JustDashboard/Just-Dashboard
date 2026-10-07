package netx

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// recorder stands behind run: it keeps every command in order and answers
// each from the first reply whose prefix matches the command line. A command
// nothing matches fails the test, so an apply that runs something unexpected
// cannot pass by accident.
type recorder struct {
	t       *testing.T
	mu      sync.Mutex
	calls   []string
	replies []reply
	stdin   map[string][]byte
}

type reply struct {
	prefix string
	out    string
	err    error
}

// record installs a recorder for the test and returns it. Tools are reported
// present unless missing names them.
func record(t *testing.T, missing ...string) *recorder {
	t.Helper()
	rec := &recorder{t: t, stdin: map[string][]byte{}}
	prevRun, prevStdin, prevHas, prevAnchors := run, runStdin, has, anchorPaths
	// The anchors are their own test's; everywhere else a host has none, so
	// a transcript about a bridge is not also a transcript about 1.1.1.1.
	anchorPaths = func(context.Context) []anchorPath { return nil }
	run = func(ctx context.Context, name string, args ...string) (string, error) {
		return rec.answer(nil, name, args...)
	}
	runStdin = func(ctx context.Context, stdin []byte, name string, args ...string) (string, error) {
		return rec.answer(stdin, name, args...)
	}
	absent := map[string]bool{}
	for _, m := range missing {
		absent[m] = true
	}
	has = func(name string) bool { return !absent[name] }
	t.Cleanup(func() { run, runStdin, has, anchorPaths = prevRun, prevStdin, prevHas, prevAnchors })
	return rec
}

// on answers every command line starting with prefix.
func (r *recorder) on(prefix, out string) *recorder {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.replies = append(r.replies, reply{prefix: prefix, out: out})
	return r
}

// fail makes every command line starting with prefix fail.
func (r *recorder) fail(prefix, out string) *recorder {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.replies = append(r.replies, reply{prefix: prefix, out: out, err: fmt.Errorf("%s", out)})
	return r
}

func (r *recorder) answer(stdin []byte, name string, args ...string) (string, error) {
	line := strings.Join(append([]string{name}, args...), " ")
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, line)
	if stdin != nil {
		r.stdin[line] = stdin
	}
	for _, rep := range r.replies {
		if strings.HasPrefix(line, rep.prefix) {
			return rep.out, rep.err
		}
	}
	r.t.Errorf("unexpected command: %s", line)
	return "", fmt.Errorf("unexpected command: %s", line)
}

// commands is every command run so far, in order.
func (r *recorder) commands() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

// ran reports whether a command line starting with prefix was run.
func (r *recorder) ran(prefix string) bool {
	for _, c := range r.commands() {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

// testService is a Service whose paths all live in a temporary directory.
func testService(t *testing.T, allowlist ...string) *Service {
	t.Helper()
	dir := t.TempDir()
	return New(Options{
		Paths: Paths{
			Dir:       filepath.Join(dir, "network"),
			Sysctl:    filepath.Join(dir, "sysctl.d", "90-just-dashboard.conf"),
			Unit:      filepath.Join(dir, "systemd", UnitName),
			Resolved:  filepath.Join(dir, "resolved.conf.d", "90-just-dashboard.conf"),
			Hosts:     filepath.Join(dir, "hosts"),
			WireGuard: filepath.Join(dir, "wireguard"),
		},
		Allowlist: allowlist,
		Seal:      func(s string) (string, error) { return "sealed:" + s, nil },
		Open:      func(s string) (string, error) { return strings.TrimPrefix(s, "sealed:"), nil },
	})
}

// fixture reads a file from testdata.
func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
