package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/hostname"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// serverNamer is the hostname package as the handlers use it, so a test can
// rename a server that is not the one it runs on.
type serverNamer interface {
	Current(ctx context.Context) (*hostname.State, error)
	Rename(ctx context.Context, name string) (*hostname.Result, error)
}

// mountHostnameRoutes is the server's own name. Reading it here is the
// operator's as much as changing it: anyone signed in sees the name at
// /system/host, and what this adds is the tailnet name beside it, which is
// asked of Tailscale on the host.
func (s *Server) mountHostnameRoutes(r chi.Router) {
	r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
	r.Method(http.MethodGet, "/", s.handle(s.handleHostname))
	r.Method(http.MethodPut, "/", s.handle(s.handleHostnameSet))
}

func (s *Server) handleHostname(w http.ResponseWriter, r *http.Request) error {
	state, err := s.modules.hostname.Current(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, state)
	return nil
}

// handleHostnameSet renames the server. The audit row is written whether or
// not the rename went through, with the name that was asked for.
func (s *Server) handleHostnameSet(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Hostname string `json:"hostname"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	name, err := hostname.Normalize(in.Hostname)
	if err != nil {
		return httpx.Err(http.StatusBadRequest, "invalid_hostname", err.Error())
	}
	httpx.SetAudit(r, "system.hostname.set", name, nil)
	result, err := s.modules.hostname.Rename(r.Context(), name)
	if err != nil {
		var invalid *hostname.InvalidError
		if errors.As(err, &invalid) {
			return httpx.Err(http.StatusBadRequest, "invalid_hostname", err.Error())
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return httpx.Timeout(err)
		}
		// The host's own words are the whole of what an operator can act
		// on here, so they are the message rather than a line in the log.
		return httpx.Err(http.StatusBadGateway, "hostname_failed", "The server could not be renamed: "+err.Error())
	}
	httpx.SetAudit(r, "system.hostname.set", result.Hostname, map[string]any{"previous": result.Previous})
	httpx.JSON(w, http.StatusOK, result)
	return nil
}
