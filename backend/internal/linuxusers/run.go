package linuxusers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// runner runs one host tool and returns what it printed. stdin is how a secret
// travels: an argv is readable by every process on the machine, and this is the
// one seam a test replaces to see what would have been run.
type runner func(ctx context.Context, stdin, name string, args ...string) (string, error)

// outputCap bounds what one tool may hand back. `find` over a large disk is the
// tool it exists for; the rest never come close.
const outputCap = 2 << 20

// cappedBuffer keeps the first outputCap bytes and quietly discards the rest, so
// a tool that prints without end cannot fill the dashboard's memory.
type cappedBuffer struct{ bytes.Buffer }

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := outputCap - b.Len(); room > 0 {
		if len(p) > room {
			b.Buffer.Write(p[:room])
		} else {
			b.Buffer.Write(p)
		}
	}
	return len(p), nil
}

var (
	hostToolsMu sync.Mutex
	hostTools   = map[string]bool{}
)

// hostHas asks once per tool it finds. The answer costs a process, and a host does
// not lose usermod while a page is polling. One it does not find is asked about
// again: somebody may install sudo without restarting the dashboard.
func hostHas(name string) bool {
	hostToolsMu.Lock()
	defer hostToolsMu.Unlock()
	if hostTools[name] {
		return true
	}
	found := hostexec.AvailableOnHost(name)
	if found {
		hostTools[name] = true
	}
	return found
}

// runOnHost prefers the host's own copy: that is the one built against the
// host's PAM, login.defs and shadow suite, and it is what the operator would get
// over SSH. Where the host has none — a minimal image — the copy shipped in this
// image still works, because /etc is the host's own directory in here too.
func runOnHost(ctx context.Context, stdin, name string, args ...string) (string, error) {
	// A caller that knows its tool takes longer says so with a deadline of its
	// own: archiving a home is minutes, and thirty seconds would refuse it.
	if _, bounded := ctx.Deadline(); !bounded {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	cmd := hostexec.Command(ctx, name, args...)
	if hostHas(name) {
		cmd = hostexec.CommandOnHost(ctx, name, args...)
	}
	var buf cappedBuffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	if err := cmd.Run(); err != nil {
		var execErr *exec.Error
		if errors.As(err, &execErr) {
			return "", fmt.Errorf("%w: %s", ErrToolMissing, name)
		}
		if ctx.Err() != nil {
			return buf.String(), fmt.Errorf("%s did not finish in time: %w", name, ctx.Err())
		}
		// The tool's words are the error. A secret never reaches them: stdin is
		// not echoed by anything run here.
		return buf.String(), fmt.Errorf("%s: %s", name, strings.TrimSpace(buf.String()))
	}
	return buf.String(), nil
}

// ErrToolMissing says the host cannot do this at all, which is a different
// answer from having tried and failed.
var ErrToolMissing = errors.New("this host does not have the tool")

func stringPtr(v string) *string { return &v }

// NewForTest is a service over a made-up /etc and made-up tools, for another
// package's tests: the routes in front of this one must never reach the host's
// real accounts from a test.
func NewForTest(etc string, run func(ctx context.Context, stdin, name string, args ...string) (string, error)) *Service {
	return &Service{etc: etc, run: run, now: time.Now}
}
