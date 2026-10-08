package api

import (
	"net/http"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

func (s *Server) mountNetworkDriftRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/drift", s.handle(s.handleNetworkDrift))
}

func (s *Server) handleNetworkDrift(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 45*time.Second)
	defer cancel()
	httpx.JSON(w, http.StatusOK, s.modules.network.Drift(ctx))
	return nil
}
