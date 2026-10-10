package audit

import (
	"context"
	"io"
	"log/slog"
	"testing"

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
