package api

import "github.com/go-chi/chi/v5"

// mountDatabaseWorkbenchRoutes registers the routes the table editor, the SQL editor and the schema browser are built on. It is called inside the /databases
// route, so paths are relative to it and each group states the capability it
// needs.
func (s *Server) mountDatabaseWorkbenchRoutes(r chi.Router) {
}
