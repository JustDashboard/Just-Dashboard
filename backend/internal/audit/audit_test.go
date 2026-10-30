package audit

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

func TestRecordOutlivesACanceledRequest(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	l := New(st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	l.Record(ctx, Entry{Username: "admin", Action: "network.route.add", Target: "10.240.1.0/24", Status: 201, Success: true})
	entries, total, err := l.List(context.Background(), Filter{Action: "network.route.add"})
	if err != nil || total != 1 || len(entries) != 1 || entries[0].Target != "10.240.1.0/24" {
		t.Fatalf("audit row after a disconnected client = %+v, %d, %v", entries, total, err)
	}
}

// A field a request chose is written twice, and bounded in both.
func TestRecordBoundsTheFieldsARequestChooses(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var logged strings.Builder
	l := New(st, slog.New(slog.NewTextHandler(&logged, nil)))
	long := strings.Repeat("a", 100_000)
	l.Record(context.Background(), Entry{Username: long, Target: long, Path: "/" + long, Detail: long, KeyID: 1, KeyName: long, Action: "auth.login"})
	entries, _, err := l.List(context.Background(), Filter{Action: "auth.login"})
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries = %+v, %v", entries, err)
	}
	e := entries[0]
	for name, tc := range map[string]struct {
		got string
		max int
	}{
		"username": {e.Username, maxUsernameLen},
		"target":   {e.Target, maxTargetLen},
		"path":     {e.Path, maxPathLen},
		"detail":   {e.Detail, maxDetailLen},
		"key name": {e.KeyName, maxKeyNameLen},
	} {
		if len(tc.got) > tc.max+len("…") {
			t.Errorf("%s kept %d bytes, limit %d", name, len(tc.got), tc.max)
		}
		if !strings.HasSuffix(tc.got, "…") {
			t.Errorf("%s does not say it was cut", name)
		}
	}
	if n := logged.Len(); n > 2*(maxUsernameLen+maxTargetLen+maxKeyNameLen) {
		t.Errorf("the mirrored log line is %d bytes", n)
	}
}

func TestRecordKeepsWhatFitsAndCutsOnACharacter(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Errorf("truncate = %q", got)
	}
	if got := truncate(strings.Repeat("é", 10), 5); !utf8.ValidString(got) || got != "éé…" {
		t.Errorf("truncate = %q", got)
	}
}
