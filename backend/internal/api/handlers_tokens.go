package api

import (
	"net/http"
	"strconv"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

func (s *Server) handleDeleteTokenRecord(w http.ResponseWriter, r *http.Request) error {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		return httpx.BadRequest("invalid token id")
	}
	// The route requires system.admin as well as the destructive capability:
	// administrators may remove inactive records belonging to any account.
	tokens, err := s.Auth.ListAPITokens(r.Context(), httpx.MustPrincipal(r).UserID(), true)
	if err != nil {
		return httpx.Internal(err)
	}
	for _, token := range tokens {
		if token.ID != id {
			continue
		}
		deleted, err := s.Auth.DeleteInactiveAPIToken(r.Context(), id)
		if err != nil {
			return httpx.Internal(err)
		}
		if !deleted {
			return httpx.Err(http.StatusConflict, "token_active", "Revoke this key before deleting its record.")
		}
		httpx.SetAudit(r, "dashboard.token.delete", token.Name, map[string]any{"tokenId": id})
		httpx.NoContent(w)
		return nil
	}
	return httpx.ErrNotFound
}
