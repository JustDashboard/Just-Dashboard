package api

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A fresh answer is served without asking the source again, however many maps
// ask at once; a stale one is read again, once.
func TestMapSourceIsReadOnceWhileFresh(t *testing.T) {
	var c mapSources
	var calls atomic.Int32
	spec := mapSourceSpec{key: "k", ttl: time.Hour, budget: time.Second, limit: time.Second}
	read := func(context.Context) (int, error) { return int(calls.Add(1)), nil }

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := readMapSource(&c, spec, read); !got.Have || got.Value != 1 {
				t.Errorf("read = %+v", got)
			}
		}()
	}
	wg.Wait()
	if n := calls.Load(); n != 1 {
		t.Fatalf("the source was read %d times for eight maps", n)
	}

	stale := spec
	stale.ttl = 0
	if got := readMapSource(&c, stale, read); got.Value != 2 {
		t.Fatalf("a stale answer was not read again: %+v", got)
	}
}

// A slow source does not hold the map back: the request waits its budget and
// draws what there is, and the read carries on for the next one.
func TestMapSourceAnswersWithinItsBudget(t *testing.T) {
	var c mapSources
	release := make(chan struct{})
	spec := mapSourceSpec{key: "slow", ttl: time.Hour, budget: 20 * time.Millisecond, limit: time.Second}
	read := func(context.Context) (string, error) {
		<-release
		return "done", nil
	}
	started := time.Now()
	got := readMapSource(&c, spec, read)
	if time.Since(started) > 500*time.Millisecond {
		t.Fatal("the map waited for a slow source past its budget")
	}
	if got.Have || !errors.Is(got.Err, errMapReading) {
		t.Fatalf("a source still being read = %+v", got)
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for {
		if got = readMapSource(&c, spec, read); got.Have {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the read that carried on never landed")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got.Value != "done" || got.Err != nil {
		t.Fatalf("after the read landed = %+v", got)
	}
}

// A failed read keeps the last good answer beside its error, so a shelf says
// "not read" over what it showed instead of going empty.
func TestMapSourceKeepsItsLastAnswerThroughAFailure(t *testing.T) {
	var c mapSources
	spec := mapSourceSpec{key: "flaky", ttl: 0, budget: time.Second, limit: time.Second}
	if got := readMapSource(&c, spec, func(context.Context) ([]string, error) { return []string{"a"}, nil }); !got.Have {
		t.Fatalf("first read = %+v", got)
	}
	boom := errors.New("boom")
	got := readMapSource(&c, spec, func(context.Context) ([]string, error) { return nil, boom })
	if !got.Have || len(got.Value) != 1 || !errors.Is(got.Err, boom) {
		t.Fatalf("after a failure = %+v", got)
	}
}
