package accesslog

import (
	"context"
	"testing"
	"time"
)

// Requests are written as they are answered, so two that overlapped land in
// the file a moment out of time order. One written just after memory's first
// request but stamped before the cut is the history's: memory skips it even
// though it sits past the index the running maximum finds, and the window and
// the export each count it once.
func TestARequestWrittenOutOfOrderAtTheCutIsCountedOnce(t *testing.T) {
	reader, clock := newFake()
	now := clock.Now()
	archive := &fakeArchive{recording: true}
	first := now.Add(-time.Hour)
	// The file: memory's first request, one three seconds later, then one
	// stamped half a second after the first but written after both, then an
	// hour of the rest. The history holds all of it.
	order := []time.Duration{0, 3 * time.Second, 500 * time.Millisecond}
	for i := 1; i < 58; i++ {
		order = append(order, 3*time.Second+time.Duration(i)*time.Minute)
	}
	for _, d := range order {
		reader.append(line(first.Add(d), "/x", 200))
		archive.keep(parsed(t, first.Add(d), "/x"))
	}
	store := newTestStore(reader, clock).WithArchive(archive)
	ctx := context.Background()
	window, err := store.Window(ctx, "r", Filter{Since: now.Add(-7 * 24 * time.Hour), Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	if got := window.Result.Summary.Total; got != len(order) {
		t.Fatalf("the week counts %d requests, want %d: the late one at the cut counted from both sides", got, len(order))
	}
	n, err := store.Export(ctx, "r", Filter{Since: now.Add(-7 * 24 * time.Hour)}, 0, func(Entry) {})
	if err != nil || n != len(order) {
		t.Fatalf("the export has %d requests (%v), want %d", n, err, len(order))
	}
}
