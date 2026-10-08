package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netcapture"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netdiag"
	"github.com/go-chi/chi/v5"
)

// The operations that take longer than a request should.
//
// The compose runner streams over a socket that *owns* the command: closing
// the tab kills it, and the frontend refuses to reconnect because reconnecting
// would run it again. That is a defensible trade for `docker compose up` and
// the wrong one for certbot and apt, which is why these are jobs instead —
// started by a POST, watched by id, and unaffected by anything the watcher
// does.
func privateNetworkJob(kind string) bool {
	return strings.HasPrefix(kind, netdiag.JobPrefix) || strings.HasPrefix(kind, netcapture.JobPrefix)
}

func (s *Server) mountJobRoutes(r chi.Router) {
	r.Route("/jobs", func(r chi.Router) {
		r.Method(http.MethodGet, "/", s.handle(s.handleJobList))
		r.Method(http.MethodGet, "/{id}", s.handle(s.handleJobGet))
		r.Method(http.MethodGet, "/{id}/stream", s.handle(s.handleJobStream))
		// Stopping a running upgrade needs the capability that started it.
		// It is not in the destructive group: interrupting is how you *avoid*
		// a bad outcome, and a typed phrase in front of a stop button is a
		// phrase somebody types while something is going wrong.
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapServiceControl))
			r.Method(http.MethodPost, "/{id}/cancel", s.handle(s.handleJobCancel))
		})
	})
}

func (s *Server) handleJobList(w http.ResponseWriter, r *http.Request) error {
	list := s.modules.jobs.List()
	if !httpx.MustPrincipal(r).Can(auth.CapSystemAdmin) {
		visible := make([]jobs.Job, 0, len(list))
		for _, job := range list {
			if !privateNetworkJob(job.Kind) {
				visible = append(visible, job)
			}
		}
		list = visible
	}
	diagnosticPrivate(w)
	httpx.JSON(w, http.StatusOK, list)
	return nil
}

func (s *Server) handleJobGet(w http.ResponseWriter, r *http.Request) error {
	job, lines, ok := s.modules.jobs.Get(chi.URLParam(r, "id"))
	if !ok {
		return httpx.ErrNotFound
	}
	if privateNetworkJob(job.Kind) {
		if !httpx.MustPrincipal(r).Can(auth.CapSystemAdmin) {
			return httpx.ErrForbidden
		}
		diagnosticPrivate(w)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"job": job, "lines": lines})
	return nil
}

func (s *Server) handleJobCancel(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "id")
	job, _, ok := s.modules.jobs.Get(id)
	if !ok {
		return httpx.BadRequest("that operation is not running")
	}
	if privateNetworkJob(job.Kind) {
		if !httpx.MustPrincipal(r).Can(auth.CapSystemAdmin) {
			return httpx.ErrForbidden
		}
		if strings.HasPrefix(job.Kind, netcapture.JobPrefix) {
			captureID := strings.Split(strings.TrimPrefix(job.Kind, netcapture.JobPrefix), ".")[0]
			httpx.SetAudit(r, "network.capture.cancel", captureID, map[string]any{"jobId": id})
			if _, err := s.modules.captures.Cancel(r.Context(), captureID); err != nil {
				return mapCaptureError(err)
			}
			httpx.NoContent(w)
			return nil
		}
		parts := strings.Split(strings.TrimPrefix(job.Kind, netdiag.JobPrefix), ".")
		httpx.SetAudit(r, "network.diagnostic.cancel", parts[0], map[string]any{"jobId": id})
		if _, err := s.modules.diagnostics.Cancel(r.Context(), parts[0]); err != nil {
			return mapDiagnosticError(err)
		}
		httpx.NoContent(w)
		return nil
	}
	if !s.modules.jobs.Cancel(id) {
		return httpx.BadRequest("that operation is not running")
	}
	httpx.SetAudit(r, "job.cancel", id, nil)
	httpx.NoContent(w)
	return nil
}

// handleJobStream tails a job.
//
// `after` is what makes reconnecting cheap and correct: a client that saw up
// to sequence 400 gets everything after 400 and nothing it already has. The
// compose runner could not offer this, because there the socket was the job.
func (s *Server) handleJobStream(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "id")
	after := atoiDefault(r.URL.Query().Get("after"), 0)

	job, backlog, lines, unsubscribe, ok := s.modules.jobs.Subscribe(id, after)
	if !ok {
		return httpx.ErrNotFound
	}
	defer unsubscribe()
	if privateNetworkJob(job.Kind) && !httpx.MustPrincipal(r).Can(auth.CapSystemAdmin) {
		return httpx.ErrForbidden
	}

	conn, err := s.WS.Upgrade(w, r)
	if err != nil {
		return nil
	}
	defer conn.Close()
	ctx, cancel := contextWithCancel(r)
	defer cancel()
	go conn.Keepalive(ctx)
	go conn.DrainControl(cancel)

	// The job first, so a client attaching to something already finished sees
	// the outcome before the output rather than after it.
	if err := conn.Send("job", job); err != nil {
		return nil
	}
	if len(backlog) > 0 {
		if err := conn.Send("output", backlog); err != nil {
			return nil
		}
	}

	// Batched on a tick for the same reason the compose runner batches: a
	// build emits hundreds of lines a second and a frame each would spend more
	// time in the socket than in the terminal.
	batch := make([]jobs.Line, 0, 64)
	flush := time.NewTicker(120 * time.Millisecond)
	defer flush.Stop()
	send := func() bool {
		if len(batch) == 0 {
			return true
		}
		if err := conn.Send("output", batch); err != nil {
			return false
		}
		batch = batch[:0]
		return true
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case line, open := <-lines:
			if !open {
				send()
				// The job's final state, which is the whole reason a client
				// stays attached to the end.
				if final, _, ok := s.modules.jobs.Get(id); ok {
					conn.Send("job", final)
				}
				return nil
			}
			batch = append(batch, line)
			if len(batch) >= 64 && !send() {
				return nil
			}
		case <-flush.C:
			if !send() {
				return nil
			}
		}
	}
}

// startJob is the shared tail of every handler that hands work to the runner:
// record who asked, and answer 202 with the job so the client can attach.
func (s *Server) startJob(w http.ResponseWriter, r *http.Request, spec jobs.Spec, run jobs.Runner) {
	spec.StartedBy = httpx.MustPrincipal(r).Username()
	job := s.modules.jobs.Start(spec, run)
	httpx.JSON(w, http.StatusAccepted, job)
}
