package api

import (
	"errors"
	"net/http"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/usageping"
	"github.com/go-chi/chi/v5"
)

// The optional daily count of servers; see docs/internal/backend/usage-ping.md.
//
// Mounted inside the admin-only /dashboard group. Answering is session-only for
// the reason linking is: whether this server reports anything is a person's
// decision, and an API key must not be able to make it for them.
func (s *Server) mountUsagePingRoutes(r chi.Router) {
	r.Route("/usage-ping", func(r chi.Router) {
		r.Method(http.MethodGet, "/", s.handle(s.handleUsagePingStatus))
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireSession)
			r.Method(http.MethodPut, "/", s.handle(s.handleUsagePingChoice))
		})
	})
}

func (s *Server) handleUsagePingStatus(w http.ResponseWriter, r *http.Request) error {
	st, err := s.modules.usagePing.Status(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, st)
	return nil
}

func (s *Server) handleUsagePingChoice(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		On *bool `json:"on"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	if body.On == nil {
		return httpx.BadRequest("on must be true or false")
	}
	action := "usage.ping.off"
	if *body.On {
		action = "usage.ping.on"
	}
	httpx.SetAudit(r, action, "", nil)
	if err := s.modules.usagePing.SetChoice(r.Context(), *body.On); err != nil {
		if errors.Is(err, usageping.ErrDisabled) {
			return httpx.Err(http.StatusForbidden, "usage_ping_disabled", err.Error())
		}
		return httpx.Internal(err)
	}
	return s.handleUsagePingStatus(w, r)
}
