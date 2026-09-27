package api

import (
	"net/http"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
)

// mountPortRoutes is the host's listening sockets. A subrouter rather than a
// single route so the page's detail views can mount beside it; chi serves the
// index at both /ports and /ports/, and the dashboard asks for the first.
func (s *Server) mountPortRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/", s.handle(s.handlePortList))
}

func (s *Server) handlePortList(w http.ResponseWriter, r *http.Request) error {
	listeners, err := proxysvc.ListListeners(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, listeners)
	return nil
}
