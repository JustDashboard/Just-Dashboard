package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/logdrain"
)

// drainCatalogue is every environment of every project that is not
// archived, with the ingress route its requests are recorded under when it
// has a domain. One without a domain has no request record to read, and
// asking for it would cost an exec into the ingress every poll for nothing.
type drainCatalogue struct{ s *Server }

func (c drainCatalogue) DrainTargets(ctx context.Context) ([]logdrain.Target, error) {
	rows, err := c.s.Store.DB.QueryContext(ctx, `
		SELECT p.id, p.name, e.id, e.slug,
		       EXISTS(SELECT 1 FROM deploy_dependencies d WHERE d.environment_id = e.id AND d.kind = 'domain')
		  FROM deploy_environments e JOIN deploy_projects p ON p.id = e.project_id
		 WHERE e.archived_at = 0 AND p.archived_at = 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []logdrain.Target{}
	for rows.Next() {
		var target logdrain.Target
		var routed bool
		if err := rows.Scan(&target.ProjectID, &target.Project, &target.EnvironmentID, &target.Environment, &routed); err != nil {
			return nil, err
		}
		if routed {
			target.Route = deploy.RouteNameFor(target.EnvironmentID)
		}
		out = append(out, target)
	}
	return out, rows.Err()
}

// drainContainers reads deployment containers through the dashboard's own
// Docker client: the ones the runtime owner labelled as managed, running.
type drainContainers struct{ docker *dockerx.Client }

func (d drainContainers) DeploymentContainers(ctx context.Context) ([]logdrain.Container, error) {
	if d.docker == nil {
		return nil, errors.New("docker is unavailable")
	}
	containers, err := d.docker.ListContainersWithLabels(ctx, map[string]string{"io.just-dashboard.managed": "true"})
	if err != nil {
		return nil, err
	}
	out := []logdrain.Container{}
	for _, container := range containers {
		if container.State != "running" {
			continue
		}
		environment, err := strconv.ParseInt(container.Labels["io.just-dashboard.environment-id"], 10, 64)
		if err != nil || environment <= 0 {
			continue
		}
		out = append(out, logdrain.Container{ID: container.ID, Name: container.Name, EnvironmentID: environment, Created: container.CreatedAt})
	}
	return out, nil
}

// Follow streams a container's output with Docker's own timestamps, which
// are both each line's instant and where a follow resumes.
func (d drainContainers) Follow(ctx context.Context, id string, since time.Time) (<-chan logdrain.Line, io.Closer, error) {
	if d.docker == nil {
		return nil, nil, errors.New("docker is unavailable")
	}
	lines, closer, err := d.docker.Logs(ctx, id, dockerx.LogOptions{
		Follow: true, Timestamps: true, Stdout: true, Stderr: true, Tail: "all",
		Since: since.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return nil, nil, err
	}
	out := make(chan logdrain.Line, 256)
	go func() {
		defer close(out)
		for line := range lines {
			at, text := splitDockerTimestamp(line.Text)
			select {
			case out <- logdrain.Line{At: at, Stream: line.Stream, Text: text}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, closer, nil
}

// splitDockerTimestamp reads the RFC 3339 instant Docker puts before each
// line when asked for timestamps.
func splitDockerTimestamp(text string) (time.Time, string) {
	stamp, rest, ok := strings.Cut(text, " ")
	if !ok {
		stamp, rest = text, ""
	}
	at, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return time.Now().UTC(), text
	}
	return at.UTC(), rest
}

// drainRequests is the ingress request record, unfiltered.
type drainRequests struct{ store *accesslog.Store }

func (d drainRequests) After(ctx context.Context, route string, after uint64, limit int) ([]accesslog.Entry, uint64, error) {
	return d.store.After(ctx, route, after, accesslog.Filter{}, limit)
}

func (s *Server) startLogDrains(ctx context.Context) {
	var requests logdrain.RequestSource
	if s.modules.requests != nil {
		requests = drainRequests{store: s.modules.requests}
	}
	s.modules.logDrainManager = logdrain.NewManager(s.modules.logDrains, drainCatalogue{s: s}, drainContainers{docker: s.modules.docker}, requests, s.Log)
	if err := s.modules.logDrainManager.Start(ctx); err != nil && s.Log != nil {
		s.Log.Warn("log drains did not start", "err", err)
	}
}

func (s *Server) mountLogDrainReads(r chi.Router) {
	r.Method(http.MethodGet, "/log-drains", s.handle(s.handleLogDrains))
}

func (s *Server) mountLogDrainWrites(r chi.Router) {
	r.Method(http.MethodPost, "/log-drains", s.handle(s.handleLogDrainCreate))
	r.Method(http.MethodPut, "/log-drains/{drain}", s.handle(s.handleLogDrainUpdate))
	r.Method(http.MethodPut, "/log-drains/{drain}/enabled", s.handle(s.handleLogDrainEnabled))
	r.Method(http.MethodPost, "/log-drains/{drain}/test", s.handle(s.handleLogDrainTest))
	r.Method(http.MethodDelete, "/log-drains/{drain}", s.handle(s.handleLogDrainDelete))
}

func mapLogDrainError(err error) error {
	switch {
	case errors.Is(err, logdrain.ErrNotFound):
		return httpx.ErrNotFound
	case errors.Is(err, logdrain.ErrNameTaken):
		return httpx.Err(http.StatusConflict, "name_taken", err.Error())
	case errors.Is(err, logdrain.ErrInvalid):
		return httpx.BadRequest("%v", err)
	default:
		return httpx.Internal(err)
	}
}

func drainParam(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, "drain"), 10, 64)
	if err != nil || id <= 0 {
		return 0, httpx.BadRequest("invalid log drain id")
	}
	return id, nil
}

// withQueued adds what each drain has waiting in memory, which only the
// running manager knows.
func (s *Server) withQueued(drains ...logdrain.Drain) []logdrain.Drain {
	if s.modules.logDrainManager == nil {
		return drains
	}
	queued := s.modules.logDrainManager.Queued()
	for i := range drains {
		drains[i].Status.Queued = queued[drains[i].ID]
	}
	return drains
}

func (s *Server) reloadLogDrains(ctx context.Context) {
	if s.modules.logDrainManager == nil {
		return
	}
	if err := s.modules.logDrainManager.Reload(ctx); err != nil && s.Log != nil {
		s.Log.Warn("log drains did not reload", "err", err)
	}
}

func (s *Server) handleLogDrains(w http.ResponseWriter, r *http.Request) error {
	drains, err := s.modules.logDrains.List(r.Context())
	if err != nil {
		return mapLogDrainError(err)
	}
	httpx.JSON(w, http.StatusOK, s.withQueued(drains...))
	return nil
}

func (s *Server) handleLogDrainCreate(w http.ResponseWriter, r *http.Request) error {
	var req logdrain.Write
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	drain, err := s.modules.logDrains.Create(r.Context(), req)
	if err != nil {
		return mapLogDrainError(err)
	}
	s.reloadLogDrains(r.Context())
	httpx.SetAudit(r, "deploy.log_drain.create", drain.Name, map[string]any{"kind": drain.Kind, "streams": drain.Streams, "projects": drain.Projects})
	httpx.JSON(w, http.StatusCreated, s.withQueued(*drain)[0])
	return nil
}

func (s *Server) handleLogDrainUpdate(w http.ResponseWriter, r *http.Request) error {
	id, err := drainParam(r)
	if err != nil {
		return err
	}
	var req logdrain.Write
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	drain, err := s.modules.logDrains.Update(r.Context(), id, req)
	if err != nil {
		return mapLogDrainError(err)
	}
	s.reloadLogDrains(r.Context())
	httpx.SetAudit(r, "deploy.log_drain.update", drain.Name, map[string]any{"kind": drain.Kind, "streams": drain.Streams, "projects": drain.Projects, "headersReplaced": req.Headers != nil})
	httpx.JSON(w, http.StatusOK, s.withQueued(*drain)[0])
	return nil
}

func (s *Server) handleLogDrainEnabled(w http.ResponseWriter, r *http.Request) error {
	id, err := drainParam(r)
	if err != nil {
		return err
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	drain, err := s.modules.logDrains.SetEnabled(r.Context(), id, req.Enabled)
	if err != nil {
		return mapLogDrainError(err)
	}
	s.reloadLogDrains(r.Context())
	action := "deploy.log_drain.pause"
	if req.Enabled {
		action = "deploy.log_drain.resume"
	}
	httpx.SetAudit(r, action, drain.Name, nil)
	httpx.JSON(w, http.StatusOK, s.withQueued(*drain)[0])
	return nil
}

func (s *Server) handleLogDrainTest(w http.ResponseWriter, r *http.Request) error {
	id, err := drainParam(r)
	if err != nil {
		return err
	}
	drain, err := s.modules.logDrains.Get(r.Context(), id)
	if err != nil {
		return mapLogDrainError(err)
	}
	if s.modules.logDrainManager == nil {
		return httpx.Err(http.StatusServiceUnavailable, "unavailable", "log drains are not running")
	}
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	testErr := s.modules.logDrainManager.Test(ctx, id)
	httpx.SetAudit(r, "deploy.log_drain.test", drain.Name, map[string]any{"ok": testErr == nil})
	if testErr != nil {
		return httpx.Err(http.StatusBadGateway, "drain_unreachable", testErr.Error())
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (s *Server) handleLogDrainDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := drainParam(r)
	if err != nil {
		return err
	}
	drain, err := s.modules.logDrains.Get(r.Context(), id)
	if err != nil {
		return mapLogDrainError(err)
	}
	if err := s.modules.logDrains.Delete(r.Context(), id); err != nil {
		return mapLogDrainError(err)
	}
	s.reloadLogDrains(r.Context())
	httpx.SetAudit(r, "deploy.log_drain.delete", drain.Name, nil)
	w.WriteHeader(http.StatusNoContent)
	return nil
}
