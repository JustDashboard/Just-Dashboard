package api

import "github.com/go-chi/chi/v5"

// mountDatabaseSchemaRoutes registers the routes that read and change the objects a schema holds: the catalogue, definitions and structure changes. It is called inside the /databases
// route, so paths are relative to it and each group states the capability it
// needs.
func (s *Server) mountDatabaseSchemaRoutes(r chi.Router) {
}
