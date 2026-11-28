package gitx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A relay's fetch from a remote that never answers ends at its caller's
// deadline: git's helper (git-remote-https), which outlives the killed git and
// holds its output, does not hold the fetch past it.
func TestARelayFetchEndsAtItsDeadline(t *testing.T) {
	t.Setenv("GIT_SSL_NO_VERIFY", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	release := make(chan struct{})
	stalled := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-time.After(time.Minute):
		}
	}))
	defer stalled.Close()
	defer close(release)
	s := New([]string{t.TempDir()})
	relay, err := s.OpenRelay(context.Background(), filepath.Join(t.TempDir(), "workspace-git"), stalled.URL+"/acme/shop.git")
	if err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	done := make(chan error, 1)
	go func() { done <- relay.Fetch(short, nil) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a fetch from a remote that never answers succeeded")
		}
		if took := time.Since(start); took > 15*time.Second {
			t.Fatalf("the fetch took %s past a one-second deadline", took)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the fetch was still running 30 seconds after its one-second deadline")
	}
}

// So does the account's own fetch of a workspace (no credential of the
// dashboard's), which holds the workspace's lock while it runs.
func TestAnAccountsFetchEndsAtItsDeadline(t *testing.T) {
	t.Setenv("GIT_SSL_NO_VERIFY", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	release := make(chan struct{})
	stalled := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-time.After(time.Minute):
		}
	}))
	defer stalled.Close()
	defer close(release)
	dir, git := tempRepo(t)
	git("remote", "add", "origin", stalled.URL+"/acme/shop.git")
	s := New([]string{filepath.Dir(dir)})
	short, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	done := make(chan error, 1)
	go func() { _, err := s.FetchOrigin(short, dir); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a fetch from a remote that never answers succeeded")
		}
		if took := time.Since(start); took > 15*time.Second {
			t.Fatalf("the fetch took %s past a one-second deadline", took)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the fetch was still running 30 seconds after its one-second deadline")
	}
}
