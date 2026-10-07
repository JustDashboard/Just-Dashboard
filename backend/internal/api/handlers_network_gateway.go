package api

import "github.com/go-chi/chi/v5"

// mountNetworkGatewayRoutes mounts port forwards, NAT, rate limits, blocklists and kernel protections under /network.
func (s *Server) mountNetworkGatewayRoutes(r chi.Router) {}
