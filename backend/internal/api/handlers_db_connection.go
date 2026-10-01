package api

import "github.com/go-chi/chi/v5"

// mountDatabaseConnectionRoutes registers the routes about one saved connection as a thing: its summary, its power state and its protection. It is called inside the /databases
// route, so paths are relative to it and each group states the capability it
// needs.
func (s *Server) mountDatabaseConnectionRoutes(r chi.Router) {
}
