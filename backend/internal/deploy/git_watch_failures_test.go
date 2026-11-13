package deploy

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// lockedLog is a log a running loop writes to while the test reads it.
type lockedLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lockedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *lockedLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func (l *lockedLog) waitFor(t *testing.T, text string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !strings.Contains(l.String(), text) {
		if time.Now().After(deadline) {
			t.Fatalf("log = %q, want %q within %s", l.String(), text, within)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The watch ran `_ = w.poll(ctx)` and `_ = w.check(ctx, target)`: when its
// cursor could not be written, automatic deployments stopped and no line
// anywhere said why.
func TestGitWatcherSaysWhenItsChecksFailAndWhenTheyWorkAgain(t *testing.T) {
	f, _, watcher := gitWatchFixture(t)
	ctx := context.Background()
	enqueueGitFixture(t, f, strings.Repeat("a", 40), TriggerManual)
	if err := watcher.poll(ctx); err != nil {
		t.Fatal(err)
	}

	// What a schema problem looks like to the watch: its cursor table gone.
	if _, err := f.base.DB.Exec(`ALTER TABLE deploy_git_watches RENAME TO deploy_git_watches_gone`); err != nil {
		t.Fatal(err)
	}
	err := watcher.poll(ctx)
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("(environment %d)", f.envID)) || !strings.Contains(err.Error(), "deploy_git_watches") {
		t.Fatalf("poll = %v, want the failed check with its environment and the store's reason", err)
	}

	out := &lockedLog{}
	watcher.log = slog.New(slog.NewTextHandler(out, nil))
	watcher.Start(ctx)
	defer watcher.Stop()
	out.waitFor(t, "background task failed", 5*time.Second)
	if logged := out.String(); !strings.Contains(logged, `task="git branch watch"`) || !strings.Contains(logged, "deploy_git_watches") {
		t.Fatalf("log = %s, want the watch named with the store's reason", logged)
	}

	if _, err := f.base.DB.Exec(`ALTER TABLE deploy_git_watches_gone RENAME TO deploy_git_watches`); err != nil {
		t.Fatal(err)
	}
	out.waitFor(t, "background task recovered", 3*GitWatchInterval)
	watcher.Stop()
	if logged := out.String(); strings.Count(logged, "background task failed") != 1 {
		t.Fatalf("log = %s, want the failure said once", logged)
	}
}
