package jobs

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
)

func TestSubscribeAfterCompletionReturnsBacklogAndClosedStream(t *testing.T) {
	for _, failed := range []bool{false, true} {
		name := "succeeded"
		if failed {
			name = "failed"
		}
		t.Run(name, func(t *testing.T) {
			m := testManager(t)
			job := m.Start(Spec{Kind: "test"}, func(_ context.Context, out Emitter) error {
				out.Line("stdout", "first")
				out.Line("stderr", "last")
				if failed {
					return errors.New("command failed")
				}
				return nil
			})
			waitFor(t, "completion", func() bool {
				got, _, _ := m.Get(job.ID)
				return got.EndedAt != nil
			})
			got, backlog, live, unsubscribe, ok := m.Subscribe(job.ID, 1)
			if !ok {
				t.Fatal("completed job disappeared")
			}
			defer unsubscribe()
			if got.EndedAt == nil || len(backlog) != 1 || backlog[0].Text != "last" {
				t.Fatalf("completed job or backlog missing: %+v, %+v", got, backlog)
			}
			select {
			case _, open := <-live:
				if open {
					t.Fatal("completed job delivered a live line")
				}
			default:
				t.Fatal("completed job left its live stream open")
			}
		})
	}
}

func TestDisconnectWhileJobProducesOutput(t *testing.T) {
	m := testManager(t)
	start := make(chan struct{})
	job := m.Start(Spec{Kind: "test"}, func(_ context.Context, out Emitter) error {
		<-start
		for i := 0; i < 10000; i++ {
			out.Line("stdout", "working")
			runtime.Gosched()
		}
		return nil
	})
	var readers sync.WaitGroup
	for i := 0; i < 4; i++ {
		readers.Go(func() {
			<-start
			for j := 0; j < 1000; j++ {
				_, _, _, unsubscribe, ok := m.Subscribe(job.ID, 10000)
				if !ok {
					t.Error("job disappeared while subscribing")
					return
				}
				runtime.Gosched()
				unsubscribe()
			}
		})
	}
	close(start)
	readers.Wait()
	waitFor(t, "output to finish after disconnects", func() bool {
		got, _, _ := m.Get(job.ID)
		return got.EndedAt != nil
	})
	got, _, _ := m.Get(job.ID)
	if got.Status != StatusSucceeded || got.Lines != 10000 {
		t.Fatalf("disconnect interrupted the runner: %+v", got)
	}
}

func TestConcurrentJobOutputKeepsSequenceOrder(t *testing.T) {
	m := testManager(t)
	start := make(chan struct{})
	job := m.Start(Spec{Kind: "test"}, func(_ context.Context, out Emitter) error {
		<-start
		var writers sync.WaitGroup
		for i := 0; i < 4; i++ {
			writers.Go(func() {
				for j := 0; j < 64; j++ {
					out.Line("stdout", "line")
					runtime.Gosched()
				}
			})
		}
		writers.Wait()
		return nil
	})
	_, _, live, unsubscribe, ok := m.Subscribe(job.ID, 0)
	if !ok {
		t.Fatal("job disappeared")
	}
	defer unsubscribe()
	close(start)
	count := 0
	for line := range live {
		count++
		if line.Seq != count {
			t.Errorf("line %d arrived with sequence %d", count, line.Seq)
		}
	}
	if count != 256 {
		t.Fatalf("received %d lines, want 256", count)
	}
}
