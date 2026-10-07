package api

import (
	"net/http"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// handleNetworkOverview is the Overview page's one read.
func (s *Server) handleNetworkOverview(w http.ResponseWriter, r *http.Request) error {
	httpx.JSON(w, http.StatusOK, map[string]any{})
	return nil
}
