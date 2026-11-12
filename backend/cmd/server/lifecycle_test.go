package main

import (
	"bufio"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// serve starts the server run() builds, on a loopback port of its own.
func serve(t *testing.T, handler http.Handler) (srv *http.Server, endRequests func(), base string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv, end := newHTTPServer(listener.Addr().String(), handler)
	go srv.Serve(listener)
	t.Cleanup(func() { end(); srv.Close() })
	return srv, end, "http://" + listener.Addr().String()
}

// An assistant's answer and an export hold a plain response open for as long
// as the client listens. Shutdown used to wait its whole limit for one, which
// was longer than Docker waited before killing the container.
func TestShutdownEndsAStreamInsteadOfWaitingForIt(t *testing.T) {
	ended := make(chan error, 1)
	srv, end, base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("first line\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		ended <- r.Context().Err()
	}))
	resp, err := http.Get(base + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if line, err := bufio.NewReader(resp.Body).ReadString('\n'); err != nil || line != "first line\n" {
		t.Fatalf("stream began with %q, %v", line, err)
	}

	began := time.Now()
	drainHTTP(srv, end, 50*time.Millisecond, 10*time.Second, quietLog())
	if took := time.Since(began); took > 3*time.Second {
		t.Fatalf("the drain took %s with a stream open; it must end the stream after its grace", took)
	}
	select {
	case err := <-ended:
		if err == nil {
			t.Fatal("the stream's handler returned without its context ending")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the stream's handler was never told to stop")
	}
}

func TestARequestUnderWayFinishesInsideTheGrace(t *testing.T) {
	started := make(chan struct{})
	srv, end, base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-time.After(150 * time.Millisecond):
			w.Write([]byte("saved"))
		case <-r.Context().Done():
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	answer := make(chan string, 1)
	go func() {
		resp, err := http.Get(base + "/save")
		if err != nil {
			answer <- err.Error()
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		answer <- resp.Status + " " + string(body)
	}()
	<-started
	drainHTTP(srv, end, 5*time.Second, 10*time.Second, quietLog())
	if got := <-answer; got != "200 OK saved" {
		t.Fatalf("a request under way at shutdown was answered %q, want it to finish", got)
	}
}

// Shutdown neither closes nor waits for a hijacked connection, and every live
// socket in the product is one.
func TestAHijackedConnectionIsToldToStop(t *testing.T) {
	hijacked, told := make(chan struct{}), make(chan struct{})
	srv, end, base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		close(hijacked)
		<-r.Context().Done()
		close(told)
	}))
	conn, err := net.Dial("tcp", strings.TrimPrefix(base, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("GET /socket HTTP/1.1\r\nHost: test\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	<-hijacked
	drainHTTP(srv, end, time.Minute, 10*time.Second, quietLog())
	select {
	case <-told:
	case <-time.After(3 * time.Second):
		t.Fatal("the socket's handler was never told to stop")
	}
}

// The budget means nothing if Docker kills the container before it is spent.
func TestTheContainerIsGivenLongerToStopThanShutdownTakes(t *testing.T) {
	compose, err := os.ReadFile("../../../docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	// The backend is the first service, and the only one with a grace period.
	found := regexp.MustCompile(`(?m)^    stop_grace_period:\s*(\S+)\s*$`).FindAllSubmatch(compose, -1)
	if len(found) != 1 {
		t.Fatalf("docker-compose.yml sets stop_grace_period %d times, want once, on the backend", len(found))
	}
	grace, err := time.ParseDuration(string(found[0][1]))
	if err != nil {
		t.Fatal(err)
	}
	if grace < shutdownBudget+5*time.Second {
		t.Fatalf("stop_grace_period is %s and shutdown may take %s: Docker would kill the backend mid-shutdown", grace, shutdownBudget)
	}
	if requestGrace >= httpDrain || httpDrain+30*time.Second > shutdownBudget {
		t.Fatalf("the budget (%s) does not hold the HTTP drain (%s) and the engine's 30s after it", shutdownBudget, httpDrain)
	}
	backend := regexp.MustCompile(`(?s)\n  backend:\n.*?\n  frontend:\n`).Find(compose)
	if !strings.Contains(string(backend), "stop_grace_period") {
		t.Fatal("stop_grace_period is not on the backend service")
	}
}
