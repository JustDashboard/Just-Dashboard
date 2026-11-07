package api

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// unreachableDocker points the server at a Docker socket that is not there.
func unreachableDocker(t *testing.T, s *Server) string {
	t.Helper()
	host := "unix://" + filepath.Join(t.TempDir(), "no-docker.sock")
	s.modules.docker = dockerx.New(host)
	t.Cleanup(func() { _ = s.modules.docker.Close() })
	return host
}

// A daemon this dashboard cannot reach is said to be unreachable, on a page and
// in a linked peer's command result, and never "Docker refused the
// operation.": the hub's command view showed that for a container.restart
// against a socket that was not there.
func TestADaemonThatCannotBeReachedIsSaidToBeUnreachable(t *testing.T) {
	s := testServer(t)
	host := unreachableDocker(t, s)

	_, err := s.runContainerAction(t.Context(), "web", dockerx.ActionRestart, nil)
	var api *httpx.APIError
	if !errors.As(err, &api) || api.Code != "docker_unavailable" || api.Status != http.StatusServiceUnavailable ||
		!strings.Contains(api.Raw, host) {
		t.Fatalf("page error = %#v", err)
	}

	h := newAccountHost(s)
	want := "The Docker daemon is not reachable from this dashboard. Docker said: Cannot connect to the Docker daemon at " + host
	for _, action := range []string{"start", "stop", "restart"} {
		if _, err := h.Container(t.Context(), siteActor, "web", action); err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Fatalf("container.%s = %v", action, err)
		}
	}
	if _, err := h.RestartStack(t.Context(), siteActor, "shop"); err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Fatalf("stack.restart = %v", err)
	}
}

// A socket this process may not open is the daemon out of reach too, though
// the client's own words for it say "permission denied".
func TestASocketThisProcessMayNotOpenIsUnreachableNotARefusal(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root opens any socket")
	}
	s := testServer(t)
	path := filepath.Join(t.TempDir(), "docker.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	s.modules.docker = dockerx.New("unix://" + path)
	defer s.modules.docker.Close()
	_, err = newAccountHost(s).Container(t.Context(), siteActor, "web", "restart")
	if err == nil || !strings.HasPrefix(err.Error(), "The Docker daemon is not reachable from this dashboard. Docker said: ") ||
		!strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("error = %v", err)
	}
}

// A daemon that takes the connection and then says nothing fails the command
// within the bound, rather than holding it for the ten minutes a command may
// run while the hub waits for its result.
func TestADaemonThatNeverAnswersFailsTheCommandPromptly(t *testing.T) {
	s := testServer(t)
	hung := make(chan struct{})
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-hung:
		case <-r.Context().Done():
		}
	}))
	defer engine.Close()
	defer close(hung)
	s.modules.docker = dockerx.New(engine.URL)
	defer s.modules.docker.Close()
	h := newAccountHost(s)
	h.answerWithin = 200 * time.Millisecond

	began := time.Now()
	_, err := h.Container(t.Context(), siteActor, "web", "restart")
	if err == nil || !strings.Contains(err.Error(), "did not answer within 200ms; the command was not run") {
		t.Fatalf("error = %v", err)
	}
	if took := time.Since(began); took > 5*time.Second {
		t.Fatalf("the command waited %s", took)
	}
	if _, err := h.RestartStack(t.Context(), siteActor, "shop"); err == nil || !strings.Contains(err.Error(), "did not answer") {
		t.Fatalf("stack.restart = %v", err)
	}
}

// A daemon that answers and refuses is still a refusal, and the result says
// in Docker's words what it refused.
func TestADaemonThatRefusesIsStillARefusalWithItsOwnWords(t *testing.T) {
	s := testServer(t)
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.Header().Set("API-Version", "1.47")
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			_, _ = w.Write([]byte(listJSON(nil)))
		case strings.HasSuffix(r.URL.Path, "/json"):
			_, _ = w.Write([]byte(`{"Id":"web0123456789","Name":"/web","Image":"sha256:abc","Config":{"Image":"nginx:1"},"State":{"Status":"running","Running":true},"HostConfig":{},"NetworkSettings":{"Networks":{}},"Mounts":[]}`))
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"Cannot restart container web: driver failed programming external connectivity"}`))
		}
	}))
	defer engine.Close()
	s.modules.docker = dockerx.New(engine.URL)
	defer s.modules.docker.Close()
	_, err := newAccountHost(s).Container(t.Context(), siteActor, "web", "restart")
	if err == nil || !strings.HasPrefix(err.Error(), "Docker refused the operation. Docker said: ") ||
		!strings.Contains(err.Error(), "driver failed programming external connectivity") {
		t.Fatalf("error = %v", err)
	}
}
