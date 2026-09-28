package updates

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type installedTestManager struct {
	manager
	read func(context.Context) ([]InstalledPackage, error)
}

func (installedTestManager) Name() string { return "fixture" }
func (m installedTestManager) ListInstalled(ctx context.Context) ([]InstalledPackage, error) {
	return m.read(ctx)
}

func installedTestContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func waitInstalledRead(t *testing.T, entered <-chan struct{}) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for package-manager read")
	}
}

func TestInstalledCachePreservesArchitecturesAndOwnsItsSnapshot(t *testing.T) {
	var calls atomic.Int32
	m := installedTestManager{read: func(context.Context) ([]InstalledPackage, error) {
		calls.Add(1)
		return []InstalledPackage{
			{Name: "libc6:amd64", Architecture: "amd64", Version: "1"},
			{Name: "libc6:i386", Architecture: "i386", Version: "1"},
		}, nil
	}}
	s := New()
	ctx := installedTestContext(t)
	first, err := s.installed(ctx, m, false)
	if err != nil || len(first) != 2 {
		t.Fatalf("first read = %+v, %v", first, err)
	}
	first[0].Name = "caller edit"
	for range 2 {
		got, err := s.installed(ctx, m, false)
		if err != nil || len(got) != 2 || got[0].Name != "libc6:amd64" || got[1].Name != "libc6:i386" {
			t.Fatalf("cache changed the installed set: %+v, %v", got, err)
		}
		got[0].Name = "another caller edit"
	}
	if calls.Load() != 1 {
		t.Fatalf("cached reads ran the manager %d times", calls.Load())
	}
	s.mu.Lock()
	s.indexedAt = time.Now().Add(-installedTTL - time.Second)
	s.mu.Unlock()
	if _, err := s.installed(ctx, m, false); err != nil || calls.Load() != 2 {
		t.Fatalf("expired index was reused: calls=%d, err=%v", calls.Load(), err)
	}
}

func TestInstalledCacheCoalescesReadsAndLetsAReaderCancel(t *testing.T) {
	entered, hold := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(hold) })
	defer release()
	var calls atomic.Int32
	m := installedTestManager{read: func(context.Context) ([]InstalledPackage, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-hold
		return []InstalledPackage{{Name: "package"}}, nil
	}}
	s := New()
	ctx := installedTestContext(t)
	firstCtx, cancel := context.WithCancel(ctx)
	first := make(chan error, 1)
	go func() { _, err := s.installed(firstCtx, m, false); first <- err }()
	waitInstalledRead(t, entered)
	second := make(chan error, 1)
	go func() { _, err := s.installed(ctx, m, false); second <- err }()
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled reader returned %v", err)
	}
	release()
	if err := <-second; err != nil || calls.Load() != 1 {
		t.Fatalf("shared read: err=%v calls=%d", err, calls.Load())
	}
}

func TestInstalledCacheDiscardsReadsStartedBeforeInvalidation(t *testing.T) {
	entered, hold := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(hold) })
	defer release()
	var calls atomic.Int32
	m := installedTestManager{read: func(context.Context) ([]InstalledPackage, error) {
		read := calls.Add(1)
		if read == 1 {
			close(entered)
			<-hold
		}
		return []InstalledPackage{{Name: "package", Version: fmt.Sprint(read)}}, nil
	}}
	s := New()
	ctx := installedTestContext(t)
	old := make(chan []InstalledPackage, 1)
	go func() { list, _ := s.installed(ctx, m, false); old <- list }()
	waitInstalledRead(t, entered)
	s.Invalidate()
	got, err := s.installed(ctx, m, false)
	if err != nil || len(got) != 1 || got[0].Version != "2" {
		t.Fatalf("post-mutation read joined the old request: %+v, %v", got, err)
	}
	release()
	if got := <-old; len(got) != 1 || got[0].Version != "2" {
		t.Fatalf("old read returned an invalidated version: %+v", got)
	}
	if got, err := s.installed(ctx, m, false); err != nil || len(got) != 1 || got[0].Version != "2" || calls.Load() != 2 {
		t.Fatalf("old fill replaced the cache: %+v, %v, calls=%d", got, err, calls.Load())
	}
}

func TestInstalledCacheDoesNotKeepPartialPackageJobs(t *testing.T) {
	var calls atomic.Int32
	m := installedTestManager{read: func(context.Context) ([]InstalledPackage, error) {
		return []InstalledPackage{{Name: "package", Version: fmt.Sprint(calls.Add(1))}}, nil
	}}
	s := New()
	ctx := installedTestContext(t)
	read := func(want int32) {
		t.Helper()
		got, err := s.installed(ctx, m, false)
		if err != nil || len(got) != 1 || got[0].Version != fmt.Sprint(want) || calls.Load() != want {
			t.Fatalf("read=%+v err=%v calls=%d, want version %d", got, err, calls.Load(), want)
		}
	}
	read(1)
	finishFirst := s.BeginMutation()
	read(2)
	finishSecond := s.BeginMutation()
	read(3)
	// Finishing one job, even unsuccessfully, cannot enable caching while
	// another package manager invocation still changes the set.
	finishFirst()
	read(4)
	read(5)
	finishSecond()
	read(6)
	read(6)
}

func TestInstalledCacheRetriesFailuresButReusesEmptyInventory(t *testing.T) {
	var calls atomic.Int32
	m := installedTestManager{read: func(context.Context) ([]InstalledPackage, error) {
		if calls.Add(1) == 1 {
			return nil, errors.New("manager unavailable")
		}
		return nil, nil
	}}
	s := New()
	ctx := installedTestContext(t)
	if _, err := s.installed(ctx, m, false); err == nil {
		t.Fatal("expected failed package-manager read")
	}
	for range 2 {
		got, err := s.installed(ctx, m, false)
		if err != nil || got == nil || len(got) != 0 {
			t.Fatalf("empty inventory = %+v, %v", got, err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("empty inventory was not cached: %d reads", calls.Load())
	}
}
