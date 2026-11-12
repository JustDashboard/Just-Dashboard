package api

import (
	"net/http"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/safego"
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
	// What the process uses and how its background work is going, and
	// profiles of it: an administrator's (handlers_self_stat.go).
	s.mountSelfStatRoutes(r)
}

// dashboardHealth is whether the dashboard itself is in working order, with
// each thing checked under its own name so a page can say which is not.
type dashboardHealth struct {
	OK bool `json:"ok"`
	// Store is the dashboard's own database and the disk it is on.
	Store storehealth.Status `json:"store"`
	// Tasks are the background tasks that panicked in the last half hour
	// (internal/safego). Each was logged with its stack and started again;
	// one listed here is one that keeps failing, or just did.
	Tasks []safego.Task `json:"tasks"`
}

// taskTroubleWindow is how long a panic keeps its task in the health report.
// The registry remembers every panic since the process started; a page that
// said so for as long would still be warning about last week's.
const taskTroubleWindow = 30 * time.Minute

func (s *Server) handleDashboardHealth(w http.ResponseWriter, _ *http.Request) error {
	store := s.modules.storeHealth.Status()
	// The registry is the process's, not this server's: a test that wants a
	// crashing task hands its own list in rather than leave one behind for
	// every other server in the test binary.
	faulted := safego.Faulted
	if s.faultedTasks != nil {
		faulted = s.faultedTasks
	}
	tasks := troubledTasks(faulted(), time.Now())
	httpx.JSON(w, http.StatusOK, dashboardHealth{OK: store.OK && len(tasks) == 0, Store: store, Tasks: tasks})
	return nil
}

// troubledTasks keeps the tasks whose last panic is recent enough to say.
func troubledTasks(faulted []safego.Task, now time.Time) []safego.Task {
	out := []safego.Task{}
	for _, task := range faulted {
		if task.LastPanicAt != nil && now.Sub(*task.LastPanicAt) < taskTroubleWindow {
			out = append(out, task)
		}
	}
	return out
}
