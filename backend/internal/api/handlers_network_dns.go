package api

import "github.com/go-chi/chi/v5"

// mountNetworkDNSRoutes mounts the resolver, its upstreams and the host's records under /network.
func (s *Server) mountNetworkDNSRoutes(r chi.Router) {}
