package api

import (
	"github.com/go-chi/chi/v5"
)

// mountSiteOpsRoutes holds what is done to a site as a whole, as opposed to
// what the site form writes into it. It shares /proxy/sites with the builder
// but not its file, so the two grow independently.
func (s *Server) mountSiteOpsRoutes(r chi.Router) {}
