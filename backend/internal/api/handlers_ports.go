package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
)

// portListTimeout bounds the walk over every process's descriptors, which on
// a host with tens of thousands of them is the slow part. The page polls it,
// so a stuck walk must answer rather than pile up behind itself.
const portListTimeout = 10 * time.Second

// mountPortRoutes is the host's listening sockets. A subrouter rather than a
// single route so the page's detail views can mount beside it; chi serves the
// index at both /ports and /ports/, and the dashboard asks for the first.
func (s *Server) mountPortRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/", s.handle(s.handlePortList))
}

func (s *Server) handlePortList(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, portListTimeout)
	defer cancel()
	listeners, err := proxysvc.ListListeners(ctx)
	if errors.Is(err, context.DeadlineExceeded) {
		return httpx.Err(http.StatusGatewayTimeout, "timeout",
			"Listing the host's sockets took longer than 10 seconds.").Retry()
	}
	if err != nil {
		return httpx.Internal(err)
	}
	// Graded by the posture's own judgement, so the page cannot call a
	// database critical that the posture calls a warning.
	network := netsec.ReadHostNetwork(ctx)
	for i := range listeners {
		listeners[i].Reach = string(network.Reach(listeners[i].Address))
	}
	httpx.JSON(w, http.StatusOK, listeners)
	return nil
}
