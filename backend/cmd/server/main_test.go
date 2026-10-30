package main

import (
	"os"
	"runtime"
	"sync"
	"testing"

	"golang.org/x/sys/unix"
)

// A goroutine that ends while locked takes its thread with it, and the one
// thread that cannot go is parked forever in whatever namespace it was moved
// to. So no such goroutine may ever be given the main thread.
func TestLockedGoroutinesNeverGetTheMainThread(t *testing.T) {
	var wg sync.WaitGroup
	for range 4000 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runtime.LockOSThread()
			if unix.Gettid() == os.Getpid() {
				t.Error("a goroutine was scheduled on the main thread")
			}
		}()
		runtime.Gosched()
	}
	wg.Wait()
}
