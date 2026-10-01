package api

import "github.com/go-chi/chi/v5"

// mountDatabaseTransferRoutes registers the routes that move data in and out: import, export, dumps and generated code. It is called inside the /databases
// route, so paths are relative to it and each group states the capability it
// needs.
func (s *Server) mountDatabaseTransferRoutes(r chi.Router) {
}
