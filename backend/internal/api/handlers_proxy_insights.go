package api

import (
	"github.com/go-chi/chi/v5"
)

// mountProxyInsightRoutes holds the readings derived from the engine rather
// than from one site or certificate. Mounted inside /proxy beside the engine,
// and kept in a file of its own so those readings grow without reopening the
// engine's routes.
func (s *Server) mountProxyInsightRoutes(r chi.Router) {
	s.mountProxyMetricRoutes(r)
	s.mountServedCertificateRoutes(r)
}
