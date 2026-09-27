package procs

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestConcurrentSnapshotsShareOnlyTheScan(t *testing.T) {
	table := NewTable()
	started, finish := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	read := func(ctx context.Context) ([]Process, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-finish:
		}
		return []Process{{PID: 1, Name: "original"}}, nil
	}
	first := make(chan error, 1)
	ctx, cancel := context.WithCancel(t.Context())
	go func() { _, err := table.sharedSnapshot(ctx, read); first <- err }()
	<-started
	second := make(chan []Process, 1)
	go func() {
		rows, err := table.sharedSnapshot(t.Context(), read)
		if err != nil {
			t.Error(err)
		}
		second <- rows
	}()
	deadline := time.After(3 * time.Second)
	for {
		table.scanMu.Lock()
		readers := table.scan.readers
		table.scanMu.Unlock()
		if readers == 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("second reader never joined")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled reader: %v", err)
	}
	close(finish)
	rows := <-second
	if calls.Load() != 1 || len(rows) != 1 {
		t.Fatalf("calls=%d rows=%v", calls.Load(), rows)
	}
	rows[0].Name = "overlaid"
	fresh, err := table.sharedSnapshot(t.Context(), read)
	if err != nil || calls.Load() != 2 || fresh[0].Name != "original" {
		t.Fatalf("fresh snapshot: %v, %v", fresh, err)
	}
}
