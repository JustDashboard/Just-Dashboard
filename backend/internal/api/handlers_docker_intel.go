package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// The half of the Docker surface that explains rather than reports.
//
// Everything here is derived: a breakdown worked out by walking a filesystem,
// a cause inferred from four facts that individually say nothing, a deploy
// preview assembled by comparing a file against a record of the last one. None
// of it is a reading, so none of it is presented as one — every response
// carries how it was arrived at, and the routes are all lazy, because the cost
// of computing them does not belong in a table that polls.

// handleWritableLayer breaks a container's writable layer down by directory.
//
// Expensive and therefore explicitly asked for: it runs `du` inside the
// container. The result is cached in the client for ten minutes; `refresh=true`
// takes the walk again, which is what "analyze again" after a cleanup means.
func (s *Server) handleWritableLayer(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 2*time.Minute)
	defer cancel()
	report, err := s.modules.docker.AnalyzeWritableLayer(ctx,
		httpx.URLParam(r, "id"), r.URL.Query().Get("refresh") == "true")
	if err != nil {
		return s.dockerErr(err)
	}
	httpx.JSON(w, http.StatusOK, report)
	return nil
}

// handleMigrationPlan writes out how to move a directory onto a volume.
//
// It produces a plan and nothing else. Carrying it out means stopping a
// service and copying data the operator has just been told they cannot afford
// to lose, so the dashboard describes every step and the exact commands and
// lets them run it — see dockerx.MigrationPlan for the argument.
func (s *Server) handleMigrationPlan(w http.ResponseWriter, r *http.Request) error {
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if path == "" {
		return httpx.BadRequest("a path inside the container is required")
	}
	plan, err := s.modules.docker.PlanMigration(r.Context(), httpx.URLParam(r, "id"), path)
	if err != nil {
		if errors.Is(err, dockerx.ErrUnavailable) {
			return s.dockerErr(err)
		}
		return httpx.BadRequest("%s", err.Error())
	}
	httpx.JSON(w, http.StatusOK, plan)
	return nil
}

// handleContainerFailure explains why a container is not working.
func (s *Server) handleContainerFailure(w http.ResponseWriter, r *http.Request) error {
	d, err := s.modules.docker.DiagnoseFailure(r.Context(),
		httpx.URLParam(r, "id"), s.modules.dockerEvents)
	if err != nil {
		return s.dockerErr(err)
	}
	httpx.JSON(w, http.StatusOK, d)
	return nil
}

// handleContainerAnomalies reads a container's recorded history for changes
// worth reporting.
//
// The history is the metric recorder's, keyed by container name so it survives
// a redeploy. A container with no recorded history produces an empty list
// rather than an error: the dashboard may simply not have been running long
// enough, which is not a failure and is worth saying.
func (s *Server) handleContainerAnomalies(w http.ResponseWriter, r *http.Request) error {
	detail, err := s.modules.docker.Inspect(r.Context(), httpx.URLParam(r, "id"))
	if err != nil {
		return s.dockerErr(err)
	}
	out := struct {
		Container string            `json:"container"`
		Window    string            `json:"window"`
		Samples   int               `json:"samples"`
		Anomalies []dockerx.Anomaly `json:"anomalies"`
		Note      string            `json:"note,omitempty"`
	}{Container: detail.Name, Anomalies: []dockerx.Anomaly{}}

	hours := 24
	if raw := r.URL.Query().Get("hours"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 24*14 {
			hours = n
		}
	}
	to := time.Now().UTC()
	from := to.Add(-time.Duration(hours) * time.Hour)
	series, err := s.modules.metrics.ContainerRange(r.Context(), detail.Name, from, to, 240)
	if err != nil || series == nil || len(series.Points) == 0 {
		out.Note = "No recorded history for this container yet. Samples are kept from the moment the dashboard starts, so a container that has just been created — or a dashboard that has just been restarted — has nothing to compare against."
		httpx.JSON(w, http.StatusOK, out)
		return nil
	}

	points := make([]dockerx.MetricPoint, 0, len(series.Points))
	for _, p := range series.Points {
		points = append(points, dockerx.MetricPoint{
			TS: p.TS, CPU: p.CPU, CPUPeak: p.CPUPeak,
			MemBytes: p.MemBytes, MemLimit: p.MemLimit, MemPeak: p.MemPeak,
			SizeRw: p.SizeRw,
		})
	}
	out.Samples = len(points)
	out.Window = strconv.Itoa(hours) + "h"
	out.Anomalies = dockerx.DetectAnomalies(detail.Name, detail.ID, points)
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// handleStackPreview says what a deploy is expected to change.
func (s *Server) handleStackPreview(w http.ResponseWriter, r *http.Request) error {
	name := httpx.URLParam(r, "name")
	stack, err := s.findStack(r, name)
	if err != nil {
		return err
	}
	if len(stack.ConfigFiles) == 0 {
		return httpx.BadRequest("stack %q has no compose file this dashboard can read, so there is nothing to compare", name)
	}
	current, err := dockerx.ReadComposeFile(stack.ConfigFiles[0])
	if err != nil {
		return httpx.Wrap(http.StatusInternalServerError, "read_failed", err)
	}
	previous, _ := s.modules.dockerDeploys.Latest(r.Context(), stack.Name)

	action := r.URL.Query().Get("action")
	if action == "" {
		action = "up"
	}
	httpx.JSON(w, http.StatusOK,
		s.modules.docker.PreviewDeploy(r.Context(), stack, current, previous, action))
	return nil
}

// handleStackDeployments lists what this dashboard has recorded for a stack.
func (s *Server) handleStackDeployments(w http.ResponseWriter, r *http.Request) error {
	name := httpx.URLParam(r, "name")
	list, err := s.modules.dockerDeploys.List(r.Context(), name, 50)
	if err != nil {
		return httpx.Wrap(http.StatusInternalServerError, "history_failed", err)
	}
	httpx.JSON(w, http.StatusOK, list)
	return nil
}

// handleStackDeployment returns one record with its compose file, which is
// what a rollback would restore and what the operator has to read first.
func (s *Server) handleStackDeployment(w http.ResponseWriter, r *http.Request) error {
	id, err := strconv.ParseInt(httpx.URLParam(r, "id"), 10, 64)
	if err != nil {
		return httpx.BadRequest("a deployment id is required")
	}
	record, err := s.modules.dockerDeploys.Get(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return httpx.Wrap(http.StatusInternalServerError, "history_failed", err)
	}
	if record.Project != httpx.URLParam(r, "name") {
		// The id is global and the route is scoped to a stack. Refusing the
		// mismatch rather than serving it keeps a deployment id from being a
		// way to read another stack's compose file, which may hold anything.
		return httpx.ErrNotFound
	}

	// What a rollback would actually be able to do. An image that is no longer
	// on this host and no longer in its registry cannot be restored, and
	// saying so before the operator commits is the difference between a
	// rollback and an outage.
	out := struct {
		*dockerx.StackDeployment
		Restorable bool     `json:"restorable"`
		Missing    []string `json:"missing"`
	}{StackDeployment: record, Restorable: true, Missing: []string{}}
	for service, digest := range record.ImageDigests {
		if digest == "" {
			continue
		}
		if _, err := s.modules.docker.InspectImage(r.Context(), digest); err != nil {
			out.Restorable = false
			out.Missing = append(out.Missing, service+" ("+digest+")")
		}
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// handleCleanupPreview measures each category of removable object.
func (s *Server) handleCleanupPreview(w http.ResponseWriter, r *http.Request) error {
	preview, err := s.modules.docker.PreviewCleanup(r.Context())
	if err != nil {
		return s.dockerErr(err)
	}
	httpx.JSON(w, http.StatusOK, preview)
	return nil
}

// handleCleanupRun carries out the selected categories.
//
// The route is already behind the destructive gate. Volumes get a second,
// stricter check here — a typed phrase — because they are the only category
// whose contents cannot be recovered from a registry or a rebuild, and because
// a category list is easy to submit with one more box ticked than intended.
func (s *Server) handleCleanupRun(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Categories []string `json:"categories"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if len(req.Categories) == 0 {
		return httpx.BadRequest("choose at least one category to remove")
	}
	wantsVolumes := false
	for _, c := range req.Categories {
		if strings.TrimSpace(c) == "volumes" {
			wantsVolumes = true
		}
	}
	if wantsVolumes {
		if err := httpx.RequireTypedConfirmation(w, r, "delete volumes"); err != nil {
			return err
		}
	}
	reports, err := s.modules.docker.RunCleanup(r.Context(), req.Categories)
	if err != nil {
		return s.dockerErr(err)
	}
	var reclaimed int64
	for _, rep := range reports {
		reclaimed += int64(rep.SpaceReclaimed)
	}
	httpx.SetAudit(r, "docker.cleanup", strings.Join(req.Categories, ","),
		map[string]any{"reclaimed": reclaimed, "reports": reports})
	httpx.JSON(w, http.StatusOK, map[string]any{"reports": reports, "reclaimed": reclaimed})
	return nil
}
