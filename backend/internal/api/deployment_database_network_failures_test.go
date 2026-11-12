package api

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// passLog is a log a running loop writes to while the test reads it.
type passLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *passLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *passLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// The loop ran `_ = o.Reconcile(ctx)` every five seconds: a reconciler that
// could not read its bindings reconnected nothing and said nothing.
func TestTheDatabaseNetworkReconcilerSaysWhenItsPassFails(t *testing.T) {
	s := testServer(t)
	out := &passLog{}
	s.Log = slog.New(slog.NewTextHandler(out, nil))
	if _, err := s.Store.DB.Exec(`ALTER TABLE deploy_database_bindings RENAME TO deploy_database_bindings_gone`); err != nil {
		t.Fatal(err)
	}
	networks := &deploymentDatabaseNetworks{server: s}
	networks.Start(context.Background())
	defer networks.Stop()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(out.String(), "background task failed") {
		if time.Now().After(deadline) {
			t.Fatalf("log = %q, want the failed pass said", out.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if logged := out.String(); !strings.Contains(logged, `task="database network reconciler"`) || !strings.Contains(logged, "deploy_database_bindings") {
		t.Fatalf("log = %s, want the reconciler named with the store's reason", logged)
	}
}
