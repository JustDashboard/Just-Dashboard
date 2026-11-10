package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// How a stop spends its time. The container is given stop_grace_period (60s in
// docker-compose.yml) between SIGTERM and SIGKILL, and everything here has to
// fit inside it with room to spare: before, the HTTP drain alone could take
// 15 seconds and the deployment engine's 30 came after it, against Docker's
// default of 10, so a stop with one answer still streaming was a kill, and the
// engine never got the drain written for it.
const (
	// requestGrace is how long a request already under way has to finish as
	// it would have. After it the base context ends, which is what ends a
	// response nothing else would: an assistant's answer or an export
	// streams for as long as its client listens.
	requestGrace = 3 * time.Second
	// httpDrain bounds the wait for handlers to return once told to.
	httpDrain = 8 * time.Second
	// shutdownBudget is all of it: the HTTP drain, then api.Server's
	// shutdown, whose longest part is the deployment engine's 30 seconds.
	// TestTheContainerIsGivenLongerToStopThanShutdownTakes holds it under
	// the compose file's stop_grace_period.
	shutdownBudget = 50 * time.Second
)

// newHTTPServer is the server for handler, and the function that ends every
// request it is answering.
//
// Every request's context descends from one base context, which is the only
// way to reach them all at shutdown: see drainHTTP.
func newHTTPServer(addr string, handler http.Handler) (*http.Server, context.CancelFunc) {
	requests, endRequests := context.WithCancel(context.Background())
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return requests },
		// No WriteTimeout: log tails, metric pushes and PTY sockets are
		// long-lived by nature and would be severed by one.
	}, endRequests
}

// drainHTTP stops srv accepting requests, lets those under way finish for
// grace, then ends every request's context and waits up to limit in all for
// their handlers to return.
//
// endRequests cancels the context srv hands every request (its BaseContext).
// http.Server.Shutdown does not cancel request contexts by itself: it waits
// for a streaming response for as long as it is given, and it neither closes
// nor waits for hijacked connections, which every live socket is. Ending the
// base context reaches both.
func drainHTTP(srv *http.Server, endRequests context.CancelFunc, grace, limit time.Duration, log *slog.Logger) {
	timer := time.AfterFunc(grace, endRequests)
	defer timer.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	err := srv.Shutdown(ctx)
	endRequests()
	if err != nil {
		// A handler that ignored its context. Closing its connection is what
		// is left; the rest of shutdown does not wait for it.
		log.Warn("requests were still being answered when the drain ended; their connections were closed", "err", err)
		srv.Close()
	}
}

// waitFor waits for done until ctx ends and reports whether done came first.
func waitFor(ctx context.Context, done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}
