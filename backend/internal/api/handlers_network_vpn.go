package api

import "github.com/go-chi/chi/v5"

// mountNetworkVPNRoutes mounts WireGuard, Tailscale and Headscale under /network.
func (s *Server) mountNetworkVPNRoutes(r chi.Router) {}
