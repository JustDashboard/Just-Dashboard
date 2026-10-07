package api

import "github.com/go-chi/chi/v5"

// mountNetworkTrafficRoutes mounts per-process and per-container traffic, and eBPF under /network.
func (s *Server) mountNetworkTrafficRoutes(r chi.Router) {}
