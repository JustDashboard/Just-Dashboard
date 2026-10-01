package api

import "github.com/go-chi/chi/v5"

// mountDatabaseInventoryRoutes registers the routes that find database servers on this machine and control the ones found. It is called inside the /databases
// route, so paths are relative to it and each group states the capability it
// needs.
func (s *Server) mountDatabaseInventoryRoutes(r chi.Router) {
}
