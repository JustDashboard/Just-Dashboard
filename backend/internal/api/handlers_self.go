package api

import (
	"net/http"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/storehealth"
	"github.com/go-chi/chi/v5"
)

// mountSelfRoutes mounts what the dashboard reads of itself, under /dashboard.
//
// None of it is on /healthz. Liveness is what the container's health check
// asks, and through it `compose up --wait` in the updater and the updater's
// rollback: were it to turn red when the store cannot be written, a full disk
// would restart-loop the stack and roll an update back, at the moment the
// dashboard is the tool for finding what filled the disk. A problem is said
// here instead, to somebody signed in, and changes nothing by itself.
func (s *Server) mountSelfRoutes(r chi.Router) {
	// Every role: the Overview draws it for whoever is looking, and it says
	// nothing the metrics page does not (how full a disk is) beyond whether
	// the dashboard can still save.
	r.Method(http.MethodGet, "/health", s.handle(s.handleDashboardHealth))
}

// dashboardHealth is whether the dashboard itself is in working order, with
// each thing checked under its own name so a page can say which is not.
type dashboardHealth struct {
	OK bool `json:"ok"`
	// Store is the dashboard's own database and the disk it is on.
	Store storehealth.Status `json:"store"`
}

func (s *Server) handleDashboardHealth(w http.ResponseWriter, _ *http.Request) error {
	store := s.modules.storeHealth.Status()
	httpx.JSON(w, http.StatusOK, dashboardHealth{OK: store.OK, Store: store})
	return nil
}
