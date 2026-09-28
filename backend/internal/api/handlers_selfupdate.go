package api

import (
	"errors"
	"net/http"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/selfupdate"
	"github.com/go-chi/chi/v5"
)

// The dashboard's own version, its changelog, and the button that moves it on
// to the next one.
//
// Mounted under /dashboard rather than /system because it is the one part of
// this API that is not about the server: /packages is the host's software, and
// the two being confusable is exactly why they are named apart.
func (s *Server) mountSelfUpdateRoutes(r chi.Router) {
	r.Route("/dashboard", func(r chi.Router) {
		// Readable by every role. What version this is and what changed in it
		// is not privileged information — it is on the sign-in page — and a
		// read-only operator seeing "0.6 is out" is how the person who *can*
		// install it finds out.
		r.Method(http.MethodGet, "/update", s.handle(s.handleSelfUpdateStatus))
		// The same audience as the report, which already carries this
		// transcript's last 64 KB: this is the rest of it, asked for once.
		r.Method(http.MethodGet, "/update/log", s.handle(s.handleSelfUpdateLog))

		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
			// Installing an update replaces every container in this stack with
			// one built from code that is not on the machine yet, and restarts
			// the dashboard doing it. It is destructive by any reading, and it
			// is nested here for the reason every other admin-only destructive
			// route is: admin holds every capability, so "which routes are
			// destructive" keeps one answer.
			s.destructive(r, func(r chi.Router) {
				r.Method(http.MethodPost, "/update/install", s.handle(s.handleSelfUpdateInstall))
			})
			// Clearing a finished run is not destructive: it forgets a notice,
			// and the version it describes is still installed.
			r.Method(http.MethodDelete, "/update/run", s.handle(s.handleSelfUpdateDismiss))

			// The dashboard's own settings. Reading them is admin-only rather
			// than open to every role: the allowlist and the ports are the
			// shape of this install's perimeter, and that is not something a
			// read-only operator needs on screen.
			r.Method(http.MethodGet, "/config", s.handle(s.handleSelfConfigStatus))
			r.Method(http.MethodDelete, "/config/run", s.handle(s.handleSelfConfigDismiss))
			r.Method(http.MethodGet, "/config/log", s.handle(s.handleSelfConfigLog))
			// Applying settings and restarting both take the dashboard away
			// for a minute and can leave it answering somewhere else. The
			// rollback makes them recoverable rather than irreversible, but
			// they belong in the same budget as everything else that stops a
			// running service.
			s.destructive(r, func(r chi.Router) {
				r.Method(http.MethodPut, "/config", s.handle(s.handleSelfConfigApply))
				r.Method(http.MethodPost, "/restart", s.handle(s.handleSelfConfigRestart))
			})
			// Asking for the certificate now rather than at the next check.
			// Not in the destructive budget: it fetches a file and restarts
			// the proxy for a second or two, which is the same interruption a
			// renewal makes on its own at three in the morning.
			r.Method(http.MethodPost, "/config/certificate", s.handle(s.handleSelfConfigCertificate))
		})
	})
}

// handleSelfUpdateStatus answers everything the update panel and the sidebar
// notice need in one request, because they are on screen together and two
// endpoints would mean two polls of the same three facts.
//
// Three freshnesses, and the middle one is the interesting one. refresh=true
// is the operator pressing "check now" and waits on the network. nudge=true is
// a browser that has just loaded the dashboard: it answers from the cache
// immediately and starts a check behind the request, which is what makes a
// reload a way of asking without making a reload wait. Neither parameter is
// the ordinary poll, which asks for nothing.
func (s *Server) handleSelfUpdateStatus(w http.ResponseWriter, r *http.Request) error {
	freshness := selfupdate.Cached
	switch {
	case r.URL.Query().Get("refresh") == "true":
		freshness = selfupdate.Forced
	case r.URL.Query().Get("nudge") == "true":
		freshness = selfupdate.OnLoad
	}
	rep := s.modules.selfUpdate.Report(r.Context(), freshness)
	httpx.JSON(w, http.StatusOK, rep)
	return nil
}

// handleSelfUpdateInstall starts the upgrade and answers before it finishes.
//
// It has to: the work replaces the process serving this request, so a handler
// that waited for the outcome would be waiting for its own termination. 202
// with the run record is the honest shape — the browser then follows the
// record, which is on disk and survives everything that is about to happen.
func (s *Server) handleSelfUpdateInstall(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		// Version is what the browser believed was newest when the operator
		// pressed the button. It is a check, not an instruction: the only
		// version that can be installed is whatever the tracked branch carries
		// now, and a stale tab asking for a superseded one has to be told
		// rather than quietly given something else.
		Version string `json:"version"`
	}
	if r.ContentLength > 0 {
		if err := httpx.DecodeJSON(r, &body); err != nil {
			return err
		}
	}

	rep := s.modules.selfUpdate.Report(r.Context(), selfupdate.Cached)
	if !rep.Install.Supported {
		return httpx.Err(http.StatusServiceUnavailable, "update_unsupported", rep.Install.Reason)
	}
	if !rep.Available {
		if rep.Check.Error != "" {
			return httpx.Err(http.StatusBadGateway, "check_failed",
				"the newest version could not be looked up: "+rep.Check.Error)
		}
		return httpx.BadRequest("this dashboard is already on %s, which is the newest published version", rep.Version)
	}
	target := rep.Latest
	if body.Version != "" && selfupdate.Compare(body.Version, target) != 0 {
		return httpx.Err(http.StatusConflict, "version_moved",
			"the newest version is now "+target+", not "+body.Version+"; reload and read what changed before installing")
	}

	// The phrase is the version being installed.
	//
	// It is short, which is the point: what has to be read here is *which*
	// version, and a phrase that names the object is the convention every
	// other typed route in this codebase follows — a stack's name for compose
	// down, a table's name for drop table. The frequency test that governs
	// which routes ask at all puts this firmly inside: a release lands every
	// few weeks, and an install that comes back broken is recovered over ssh,
	// not from here.
	if err := httpx.RequireTypedConfirmation(w, r, target); err != nil {
		return err
	}

	actor := httpx.MustPrincipal(r).Username()
	run, err := s.modules.selfUpdate.Install(r.Context(), target, actor)
	httpx.SetAudit(r, "dashboard.update.install", target, map[string]any{
		"from": rep.Version, "to": target, "dir": rep.Install.Dir, "ok": err == nil,
	})
	switch {
	case errors.Is(err, selfupdate.ErrInProgress):
		return httpx.Err(http.StatusConflict, "update_running", "an update is already running")
	case errors.Is(err, selfupdate.ErrNotNewer):
		return httpx.BadRequest("%s is not newer than the installed %s", target, rep.Version)
	case errors.Is(err, selfupdate.ErrNoLocation):
		return httpx.Err(http.StatusServiceUnavailable, "update_unsupported", "this install cannot update itself in place")
	case err != nil:
		return httpx.Err(http.StatusBadGateway, "update_failed", err.Error())
	}
	// 202: the work has been handed to a container that outlives this process.
	httpx.JSON(w, http.StatusAccepted, run)
	return nil
}

// handleSelfUpdateLog answers the whole transcript of the last upgrade as
// plain text. The report is polled every two seconds during a run and carries
// only the end; this is read when somebody wants the build from its first line.
func (s *Server) handleSelfUpdateLog(w http.ResponseWriter, r *http.Request) error {
	writeTranscript(w, s.modules.selfUpdate.Transcript())
	return nil
}

// writeTranscript answers a run's output as text. It is never cached: the
// file is rewritten by every run, and a stale copy is the previous run's story.
func writeTranscript(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(text))
}

// handleSelfUpdateDismiss forgets a finished run, which is what clears the
// "updated to 0.6" notice for everyone once somebody has read it.
func (s *Server) handleSelfUpdateDismiss(w http.ResponseWriter, r *http.Request) error {
	if err := s.modules.selfUpdate.Dismiss(); err != nil {
		if errors.Is(err, selfupdate.ErrInProgress) {
			return httpx.Err(http.StatusConflict, "update_running",
				"the update is still running; it can be dismissed once it has finished")
		}
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "dashboard.update.dismiss", "", nil)
	w.WriteHeader(http.StatusNoContent)
	return nil
}
