package api

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// What Start launches itself (the block expiry, the store's health) writes to
// the store, which main closes as soon as Shutdown returns.
func TestShutdownStopsAndWaitsForWhatStartLaunched(t *testing.T) {
	s := testServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	s.stopBackground = cancel
	var finished atomic.Bool
	s.background.Go(func() {
		<-ctx.Done()
		time.Sleep(100 * time.Millisecond) // the write it was in the middle of
		finished.Store(true)
	})
	s.Shutdown()
	if !finished.Load() {
		t.Fatal("Shutdown returned while background work was still running")
	}
	// main's deferred call after a stop by signal, and a test's cleanup.
	s.Shutdown()
}

func TestShutdownGivesUpWhenItsBudgetIsSpent(t *testing.T) {
	s := testServer(t)
	stuck := make(chan struct{})
	t.Cleanup(func() { close(stuck) })
	s.background.Go(func() { <-stuck })

	budget, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	began := time.Now()
	s.ShutdownContext(budget)
	if took := time.Since(began); took > 5*time.Second {
		t.Fatalf("shutdown took %s on a 200ms budget", took)
	}
}
