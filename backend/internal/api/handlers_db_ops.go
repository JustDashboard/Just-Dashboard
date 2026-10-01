package api

import "github.com/go-chi/chi/v5"

// mountDatabaseOpsRoutes registers the routes that watch and maintain a SQL server: sessions, locks, maintenance and settings. It is called inside the /databases
// route, so paths are relative to it and each group states the capability it
// needs.
func (s *Server) mountDatabaseOpsRoutes(r chi.Router) {
}
