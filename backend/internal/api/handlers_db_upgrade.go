package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
	"github.com/docker/docker/errdefs"
	"github.com/go-chi/chi/v5"
)

// Moving a database server to a newer major release, from its Settings.
//
// A server Databases started runs one release of a closed list of images,
// and a newer release of the same image is in that list too. Changing the
// tag is what the other self-hosted panels offer, and it is how a database is
// lost: a major release does not open the data directory of the one before
// it, and the container either refuses to start or starts on nothing. So the
// upgrade is the managed providers' shape, Railway's in particular: a
// preflight report read before anything changes, a safety dump, the new
// release made beside the old server with everything copied into it, a
// cutover that puts it in the old one's place, and the old one kept, stopped
// and with its data, until the operator either finishes — which removes it —
// or reverts, which puts it back.
//
// Five routes, all an administrator's. Reading and checking change nothing on
// the server: the check pulls the new release and starts it beside the old
// server only to ask it what it carries. The upgrade, the revert and the
// finish are in the destructive group: the upgrade takes the server down for
// the seconds of the cutover and ends every session, a revert discards what
// was written to the new server, and finishing removes the old server's data.
// The three that take minutes are jobs, one at a time per connection under
// the transfer prefix, so neither a dump nor a restore runs beside them.

// mountDatabaseUpgradeRoutes registers the upgrade's routes. It is called
// inside the /databases route, so paths are relative to it.
func (s *Server) mountDatabaseUpgradeRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodGet, "/{id}/upgrade", s.handle(s.handleDBUpgrade))
		r.Method(http.MethodPost, "/{id}/upgrade/check", s.handle(s.handleDBUpgradeCheck))
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodPost, "/{id}/upgrade", s.handle(s.handleDBUpgradeStart))
			r.Method(http.MethodPost, "/{id}/upgrade/revert", s.handle(s.handleDBUpgradeRevert))
			r.Method(http.MethodPost, "/{id}/upgrade/complete", s.handle(s.handleDBUpgradeComplete))
		})
	})
}

// The three upgrade jobs, as the end of their kind under the transfer prefix.
const (
	upgradeCheckJob  = "major-upgrade-check"
	upgradeJob       = "major-upgrade"
	upgradeRevertJob = "major-upgrade-revert"
)

// The states a record of an upgrade is in.
const (
	upgradeRunning  = "running"
	upgradeUpgraded = "upgraded"
	upgradeFinished = "finished"
	upgradeReverted = "reverted"
	upgradeFailed   = "failed"
)

// dbUpgradeRecord is one upgrade, as the page reads it.
type dbUpgradeRecord struct {
	ID          int64  `json:"id"`
	State       string `json:"state"`
	Phase       string `json:"phase,omitempty"`
	Engine      string `json:"engine"`
	FromVersion string `json:"fromVersion"`
	ToVersion   string `json:"toVersion"`
	FromImage   string `json:"fromImage"`
	ToImage     string `json:"toImage"`
	// Container is the name the server answers under, before and after.
	Container string `json:"container"`
	// Previous is where the old container is kept, and PreviousState what
	// Docker says of it now: exited, running, or missing once removed.
	Previous       string     `json:"previous,omitempty"`
	PreviousState  string     `json:"previousState,omitempty"`
	PreviousVolume string     `json:"previousVolume,omitempty"`
	Volume         string     `json:"volume,omitempty"`
	SafetyDump     string     `json:"safetyDump,omitempty"`
	Error          string     `json:"error,omitempty"`
	StartedBy      string     `json:"startedBy,omitempty"`
	StartedAt      time.Time  `json:"startedAt"`
	UpgradedAt     *time.Time `json:"upgradedAt,omitempty"`
	EndedAt        *time.Time `json:"endedAt,omitempty"`

	connectionID    int64
	previousID      string
	previousRestart string
	candidate       string
	readOnlyPrior   string
}

// dbUpgradeView is what GET /{id}/upgrade answers: whether the server is one
// this upgrades and to what, the upgrade whose old server is still kept, the
// last one that ended, and the job running now.
type dbUpgradeView struct {
	Offered bool `json:"offered"`
	// Reason says why it is not, in a sentence the page shows as it is.
	Reason    string                   `json:"reason,omitempty"`
	Engine    string                   `json:"engine,omitempty"`
	Label     string                   `json:"label,omitempty"`
	Container string                   `json:"container,omitempty"`
	Version   string                   `json:"version,omitempty"`
	Image     string                   `json:"image,omitempty"`
	Volume    string                   `json:"volume,omitempty"`
	Targets   []provisionVersionOption `json:"targets"`
	Window    *dbUpgradeRecord         `json:"window,omitempty"`
	Last      *dbUpgradeRecord         `json:"last,omitempty"`
	Job       *jobs.Job                `json:"job,omitempty"`
}

func (s *Server) handleDBUpgrade(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	id, err := parseID(r)
	if err != nil {
		return err
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	view := dbUpgradeView{Targets: []provisionVersionOption{}}
	view.Window, view.Last, err = s.upgradeRecords(ctx, id)
	if err != nil {
		return err
	}
	if view.Window != nil && view.Window.Previous != "" {
		view.Window.PreviousState = s.previousState(ctx, view.Window)
	}
	if job, ok := s.runningTransfer(id); ok && strings.HasPrefix(job.Kind[strings.LastIndex(job.Kind, ".")+1:], upgradeJob) {
		view.Job = &job
	}
	sub, err := s.upgradeSubject(ctx, conn, dsn)
	if err != nil {
		var refusal *httpx.APIError
		if !errors.As(err, &refusal) {
			return err
		}
		view.Reason = refusal.Message
		httpx.JSON(w, http.StatusOK, view)
		return nil
	}
	view.Engine, view.Label, view.Container = sub.engine, sub.tmpl.label, sub.detail.Name
	view.Version, view.Image, view.Volume = sub.from.id, sub.from.image, sub.data.Name
	for _, v := range sub.targets {
		view.Targets = append(view.Targets, provisionVersionOption{Version: v.id, Image: v.image})
	}
	view.Offered = len(sub.targets) > 0 && view.Window == nil
	switch {
	case view.Window != nil && view.Window.State == upgradeUpgraded:
		view.Reason = fmt.Sprintf("Finish or revert the upgrade from %s first.", view.Window.FromVersion)
	case view.Window != nil:
		view.Reason = "An upgrade is running."
	case len(sub.targets) == 0:
		view.Reason = fmt.Sprintf("%s %s is the newest release Databases offers.", sub.tmpl.label, sub.from.id)
	}
	httpx.JSON(w, http.StatusOK, view)
	return nil
}

// previousState is what Docker says of the kept container now.
func (s *Server) previousState(ctx context.Context, rec *dbUpgradeRecord) string {
	if s.modules.docker == nil {
		return ""
	}
	detail, err := s.modules.docker.Inspect(ctx, rec.previousRef())
	switch {
	case errdefs.IsNotFound(err):
		return "missing"
	case err != nil:
		return ""
	}
	return detail.State
}

// previousRef names the kept container by id where the record has one, so a
// rename of it by hand does not lose it.
func (rec *dbUpgradeRecord) previousRef() string {
	if rec.previousID != "" {
		return rec.previousID
	}
	return rec.Previous
}

type dbUpgradeRequest struct {
	Version string `json:"version"`
}

// upgradeRequestPlan reads a check's or an upgrade's request and plans it,
// refusing what cannot be planned before a job exists.
func (s *Server) upgradeRequestPlan(r *http.Request) (*upgradePlan, error) {
	id, err := parseID(r)
	if err != nil {
		return nil, err
	}
	var req dbUpgradeRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return nil, err
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return nil, err
	}
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	sub, err := s.upgradeSubject(ctx, conn, dsn)
	if err != nil {
		return nil, err
	}
	return sub.plan(req.Version)
}

// handleDBUpgradeCheck answers POST /{id}/upgrade/check: the preflight report
// for one release, as a job, since the release may have to be pulled first.
func (s *Server) handleDBUpgradeCheck(w http.ResponseWriter, r *http.Request) error {
	plan, err := s.upgradeRequestPlan(r)
	if err != nil {
		return err
	}
	base := auditBaseOf(r)
	detail := map[string]any{"from": plan.from.id, "to": plan.to.id, "image": plan.to.image}
	httpx.SetAudit(r, "database.upgrade.check", plan.conn.Name, detail)
	title := fmt.Sprintf("Check %s for %s %s", plan.conn.Name, plan.tmpl.label, plan.to.id)
	return s.startTransfer(w, r, plan.conn, upgradeCheckJob, title, func(ctx context.Context, out jobs.Emitter) error {
		report, err := s.runUpgradeCheck(ctx, plan, out)
		if err == nil {
			if b, merr := json.Marshal(report); merr == nil {
				out.Line("result", string(b))
			}
			detail["ready"] = report.Ready
			detail["blockers"] = len(report.blockers())
		}
		s.recordTransfer(base, "database.upgrade.check.finish", plan.conn.Name, detail, err)
		return err
	})
}

// handleDBUpgradeStart answers POST /{id}/upgrade: the upgrade itself.
func (s *Server) handleDBUpgradeStart(w http.ResponseWriter, r *http.Request) error {
	plan, err := s.upgradeRequestPlan(r)
	if err != nil {
		return err
	}
	if err := s.refuseServerOfProtected(r.Context(), plan.conn); err != nil {
		return err
	}
	if err := s.refuseServerOfImport(r.Context(), plan.conn); err != nil {
		return err
	}
	window, _, err := s.upgradeRecords(r.Context(), plan.conn.ID)
	if err != nil {
		return err
	}
	if window != nil {
		return httpx.Err(http.StatusConflict, "upgrade_open",
			fmt.Sprintf("The upgrade from %s %s is not finished: finish it or revert it before upgrading again.", plan.tmpl.label, window.FromVersion))
	}
	base, by := auditBaseOf(r), httpx.MustPrincipal(r).Username()
	detail := map[string]any{"from": plan.from.id, "to": plan.to.id, "image": plan.to.image, "container": plan.detail.Name}
	httpx.SetAudit(r, "database.upgrade", plan.conn.Name, detail)
	title := fmt.Sprintf("Upgrade %s to %s %s", plan.conn.Name, plan.tmpl.label, plan.to.id)
	return s.startTransfer(w, r, plan.conn, upgradeJob, title, func(ctx context.Context, out jobs.Emitter) error {
		record, err := s.insertUpgrade(context.WithoutCancel(ctx), plan, by)
		if err != nil {
			return err
		}
		u := &upgradeRun{s: s, plan: plan, record: record, out: out, base: base, by: by}
		result, err := s.runUpgrade(ctx, u)
		if err == nil {
			if b, merr := json.Marshal(result); merr == nil {
				out.Line("result", string(b))
			}
			detail["previous"], detail["volume"], detail["safetyDump"] = result.Previous, result.Volume, result.SafetyDump
			detail["tables"], detail["rows"] = result.Tables, result.Rows
		}
		s.recordTransfer(base, "database.upgrade.finish", plan.conn.Name, detail, err)
		return err
	})
}

type dbUpgradeRevertRequest struct {
	// DumpFirst takes a dump of the new server's database before what it
	// holds is discarded.
	DumpFirst bool `json:"dumpFirst"`
}

// handleDBUpgradeRevert answers POST /{id}/upgrade/revert: the old server
// takes its place back, and the new one is removed with what was written to
// it since the cutover.
func (s *Server) handleDBUpgradeRevert(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req dbUpgradeRevertRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	if s.modules.docker == nil {
		return httpx.Err(http.StatusServiceUnavailable, "docker_unavailable", "this host has no Docker socket")
	}
	if err := s.refuseServerOfProtected(r.Context(), conn); err != nil {
		return err
	}
	if err := s.refuseServerOfImport(r.Context(), conn); err != nil {
		return err
	}
	rec, err := s.openUpgrade(r.Context(), id)
	if err != nil {
		return err
	}
	base, by := auditBaseOf(r), httpx.MustPrincipal(r).Username()
	detail := map[string]any{"from": rec.ToVersion, "to": rec.FromVersion, "container": rec.Container, "dumpFirst": req.DumpFirst}
	httpx.SetAudit(r, "database.upgrade.revert", conn.Name, detail)
	title := fmt.Sprintf("Revert %s to %s", conn.Name, upgradeEngineWords(rec))
	return s.startTransfer(w, r, conn, upgradeRevertJob, title, func(ctx context.Context, out jobs.Emitter) error {
		dump, err := s.runUpgradeRevert(ctx, conn, dsn, rec, req.DumpFirst, by, out)
		if err == nil {
			res := map[string]any{"container": rec.Container, "version": rec.FromVersion, "removed": rec.Volume}
			if dump != "" {
				res["safetyDump"] = dump
				detail["safetyDump"] = dump
			}
			if b, merr := json.Marshal(res); merr == nil {
				out.Line("result", string(b))
			}
		}
		s.recordTransfer(base, "database.upgrade.revert.finish", conn.Name, detail, err)
		return err
	})
}

// upgradeEngineWords names a record's old release: "PostgreSQL 16".
func upgradeEngineWords(rec *dbUpgradeRecord) string {
	if tmpl, ok := provisionTemplates[rec.Engine]; ok {
		return tmpl.label + " " + rec.FromVersion
	}
	return rec.FromVersion
}

// runUpgradeRevert puts the kept container back in the place of the new one
// and removes the new one with its volume. Until the old server answers,
// every step can be taken back: the new container is only set aside, and is
// put back if the old one does not come up.
func (s *Server) runUpgradeRevert(ctx context.Context, conn *dbConnection, dsn string, rec *dbUpgradeRecord, dumpFirst bool, by string, out jobs.Emitter) (string, error) {
	current, err := s.modules.docker.Inspect(ctx, rec.Container)
	if err != nil {
		return "", fmt.Errorf("the new server %s could not be read: %w", rec.Container, err)
	}
	previous, err := s.modules.docker.Inspect(ctx, rec.previousRef())
	if err != nil {
		return "", fmt.Errorf("the old server %s is no longer on this machine, so there is nothing to revert to: %w", rec.Previous, err)
	}
	if previous.State == "running" {
		return "", fmt.Errorf("%s has been started by hand; stop it before reverting", previous.Name)
	}
	dump := ""
	if dumpFirst {
		out.Status("Dumping %s on %s %s first", conn.Database, upgradeLabel(rec), rec.ToVersion)
		res, err := s.takeDump(ctx, conn, dsn, dbx.DumpOptions{},
			fmt.Sprintf("Before reverting to %s", upgradeEngineWords(rec)), dbx.DumpOriginSafety, by, out)
		if err != nil {
			return "", fmt.Errorf("the dump failed, so nothing was reverted: %w", err)
		}
		dump = res.File
		out.Status("%s written", dump)
	}
	// From here the work runs to its end however the job is stopped.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), upgradeCutover)
	defer cancel()
	info, err := dbx.ParseDSN(conn.Driver, dsn)
	if err != nil {
		return dump, err
	}
	aside := rec.Container + "-pg" + rec.ToVersion + "-reverted"
	out.Status("Stopping %s %s", upgradeLabel(rec), rec.ToVersion)
	grace := int(dbStopGrace.Seconds())
	if err := s.modules.docker.Lifecycle(ctx, current.ID, dockerx.ActionStop, &grace); err != nil {
		return dump, fmt.Errorf("could not stop %s: %w", rec.Container, err)
	}
	putBack := func(cause error) error {
		out.Status("Undoing: %s %s takes its place again", upgradeLabel(rec), rec.ToVersion)
		problems := []string{}
		cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Minute)
		defer done()
		grace := 30
		_ = s.modules.docker.Lifecycle(cleanup, previous.ID, dockerx.ActionStop, &grace)
		// It may not have got as far as taking the name.
		if now, err := s.modules.docker.Inspect(cleanup, previous.ID); err == nil && now.Name != previous.Name {
			if err := s.modules.docker.Rename(cleanup, previous.ID, previous.Name); err != nil {
				problems = append(problems, err.Error())
			}
		}
		if _, err := s.modules.docker.UpdateRestartPolicy(cleanup, previous.ID, "no", 0); err != nil {
			problems = append(problems, err.Error())
		}
		problems = append(problems, s.restorePrevious(cleanup, conn, dsn, current.ID, rec.Container, true, current.RestartPol)...)
		if len(problems) > 0 {
			return fmt.Errorf("%w; and it was not all undone: %s", cause, strings.Join(problems, "; "))
		}
		return cause
	}
	if err := s.modules.docker.Rename(ctx, current.ID, aside); err != nil {
		if serr := s.modules.docker.Lifecycle(ctx, current.ID, dockerx.ActionStart, nil); serr != nil {
			return dump, fmt.Errorf("could not set %s aside (%v), and it could not be started again: %w", rec.Container, err, serr)
		}
		return dump, fmt.Errorf("could not set %s aside: %w", rec.Container, err)
	}
	out.Status("Starting %s as %s", upgradeEngineWords(rec), rec.Container)
	if problems := s.restorePrevious(ctx, conn, dsn, previous.ID, rec.Container, true, rec.previousRestart); len(problems) > 0 {
		return dump, putBack(errors.New(strings.Join(problems, "; ")))
	}
	out.Status("%s answers at %s:%s", upgradeEngineWords(rec), info.Host, info.Port)
	out.Status("Removing %s and its volume %s", aside, rec.Volume)
	warnings := s.removeScratch(ctx, current.ID, rec.Volume)
	for _, w := range warnings {
		out.Line("stderr", w)
	}
	s.endUpgrade(ctx, rec.ID, upgradeReverted, strings.Join(warnings, "; "))
	return dump, nil
}

func upgradeLabel(rec *dbUpgradeRecord) string {
	if tmpl, ok := provisionTemplates[rec.Engine]; ok {
		return tmpl.label
	}
	return rec.Engine
}

// handleDBUpgradeComplete answers POST /{id}/upgrade/complete: the upgrade
// is kept, and the old server is removed with its data.
func (s *Server) handleDBUpgradeComplete(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	conn, err := s.storedConnection(r.Context(), id)
	if err != nil {
		return err
	}
	if s.modules.docker == nil {
		return httpx.Err(http.StatusServiceUnavailable, "docker_unavailable", "this host has no Docker socket")
	}
	rec, err := s.openUpgrade(r.Context(), id)
	if err != nil {
		return err
	}
	// A revert running now is starting the server this would remove.
	if job, running := s.runningTransfer(id); running {
		return httpx.Err(http.StatusConflict, "transfer_running",
			fmt.Sprintf("%s is running for this connection; wait for it to end.", job.Title)).Describe(upgradeJob, job.ID)
	}
	ctx, cancel := timeoutCtx(r, 2*time.Minute)
	defer cancel()
	detail := map[string]any{"from": rec.FromVersion, "to": rec.ToVersion, "previous": rec.Previous}
	removed, warnings := []string{}, []string{}
	previous, err := s.modules.docker.Inspect(ctx, rec.previousRef())
	switch {
	case errdefs.IsNotFound(err):
		warnings = append(warnings, rec.Previous+" was already removed")
	case err != nil:
		return s.dockerErr(err)
	case previous.State == "running":
		return httpx.Err(http.StatusConflict, "previous_running",
			fmt.Sprintf("%s has been started by hand; stop it before finishing the upgrade", previous.Name))
	default:
		volumes := []string{}
		for _, m := range previous.Mounts {
			if m.Type == "volume" && m.Name != "" {
				volumes = append(volumes, m.Name)
			}
		}
		if err := s.modules.docker.RemoveContainer(ctx, previous.ID, true, true); err != nil {
			detail["error"] = err.Error()
			httpx.SetAudit(r, "database.upgrade.complete", conn.Name, detail)
			return s.dockerErr(err)
		}
		for _, name := range volumes {
			if err := s.modules.docker.RemoveVolume(ctx, name, false); err != nil {
				warnings = append(warnings, fmt.Sprintf("volume %s was kept: %v", name, err))
				continue
			}
			removed = append(removed, name)
		}
	}
	if err := s.endUpgrade(ctx, rec.ID, upgradeFinished, strings.Join(warnings, "; ")); err != nil {
		return err
	}
	detail["volumes"], detail["warnings"] = removed, warnings
	httpx.SetAudit(r, "database.upgrade.complete", conn.Name, detail)
	httpx.JSON(w, http.StatusOK, map[string]any{
		"previous": rec.Previous, "volumes": removed, "warnings": warnings,
		"detail": fmt.Sprintf("%s %s is kept; %s was removed with its data", upgradeLabel(rec), rec.ToVersion, rec.Previous),
	})
	return nil
}

// --- records -------------------------------------------------------------------

const upgradeColumns = `id, connection_id, state, phase, engine, from_version, to_version, from_image, to_image,
  container, previous_id, previous_name, previous_restart, previous_volume, candidate, volume, safety_dump,
  read_only_prior, error, started_by, started_at, upgraded_at, ended_at`

func scanUpgrade(scan func(...any) error) (*dbUpgradeRecord, error) {
	var rec dbUpgradeRecord
	var started, upgraded, ended int64
	if err := scan(&rec.ID, &rec.connectionID, &rec.State, &rec.Phase, &rec.Engine, &rec.FromVersion, &rec.ToVersion,
		&rec.FromImage, &rec.ToImage, &rec.Container, &rec.previousID, &rec.Previous, &rec.previousRestart,
		&rec.PreviousVolume, &rec.candidate, &rec.Volume, &rec.SafetyDump, &rec.readOnlyPrior, &rec.Error,
		&rec.StartedBy, &started, &upgraded, &ended); err != nil {
		return nil, err
	}
	rec.StartedAt = time.Unix(started, 0).UTC()
	if upgraded > 0 {
		at := time.Unix(upgraded, 0).UTC()
		rec.UpgradedAt = &at
	}
	if ended > 0 {
		at := time.Unix(ended, 0).UTC()
		rec.EndedAt = &at
	}
	return &rec, nil
}

// upgradeRecords are a connection's upgrade still open — running, or done
// with its old server kept — and the last one that ended.
func (s *Server) upgradeRecords(ctx context.Context, connectionID int64) (open, last *dbUpgradeRecord, err error) {
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT `+upgradeColumns+` FROM db_upgrades WHERE connection_id = ? ORDER BY id DESC LIMIT 2`, connectionID)
	if err != nil {
		return nil, nil, httpx.Internal(err)
	}
	defer rows.Close()
	for rows.Next() {
		rec, err := scanUpgrade(rows.Scan)
		if err != nil {
			return nil, nil, httpx.Internal(err)
		}
		switch {
		case rec.State == upgradeRunning || rec.State == upgradeUpgraded:
			if open == nil && last == nil {
				open = rec
			}
		case last == nil:
			last = rec
		}
	}
	return open, last, rows.Err()
}

// openUpgrade is the upgrade whose old server is kept, which reverting and
// finishing act on.
func (s *Server) openUpgrade(ctx context.Context, connectionID int64) (*dbUpgradeRecord, error) {
	open, _, err := s.upgradeRecords(ctx, connectionID)
	if err != nil {
		return nil, err
	}
	if open == nil || open.State != upgradeUpgraded {
		if open != nil {
			return nil, httpx.Err(http.StatusConflict, "upgrade_running", "The upgrade is still running.")
		}
		return nil, httpx.Err(http.StatusConflict, "no_upgrade", "No upgrade of this server is waiting to be finished or reverted.")
	}
	return open, nil
}

func (s *Server) insertUpgrade(ctx context.Context, plan *upgradePlan, by string) (int64, error) {
	res, err := s.Store.DB.ExecContext(ctx, `INSERT INTO db_upgrades(connection_id, state, phase, engine, from_version, to_version,
		from_image, to_image, container, previous_id, previous_name, previous_volume, candidate, volume, started_by, started_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		plan.conn.ID, upgradeRunning, "prepare", plan.engine, plan.from.id, plan.to.id, plan.from.image, plan.to.image,
		plan.detail.Name, plan.detail.ID, plan.previous, plan.data.Name, plan.candidate, plan.volume, by, time.Now().UTC().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// The record is written as each step is taken, on a context of its own: a
// job that was stopped still has to say how far it got.
func (s *Server) upgradeWrite(ctx context.Context, query string, args ...any) error {
	write, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	_, err := s.Store.DB.ExecContext(write, query, args...)
	if err != nil {
		s.Log.Warn("an upgrade's record could not be written", "err", err)
	}
	return err
}

func (s *Server) setUpgradePhase(ctx context.Context, id int64, phase, readOnlyPrior string) {
	_ = s.upgradeWrite(ctx, `UPDATE db_upgrades SET phase = ?, read_only_prior = ? WHERE id = ?`, phase, readOnlyPrior, id)
}

func (s *Server) setUpgradeSafetyDump(ctx context.Context, id int64, file string) {
	_ = s.upgradeWrite(ctx, `UPDATE db_upgrades SET safety_dump = ? WHERE id = ?`, file, id)
}

func (s *Server) setUpgradePrevious(ctx context.Context, id int64, previousID, restart, volume string) {
	_ = s.upgradeWrite(ctx, `UPDATE db_upgrades SET previous_id = ?, previous_restart = ?, previous_volume = ? WHERE id = ?`,
		previousID, restart, volume, id)
}

func (s *Server) markUpgraded(ctx context.Context, id int64) {
	_ = s.upgradeWrite(ctx, `UPDATE db_upgrades SET state = ?, phase = '', upgraded_at = ? WHERE id = ?`,
		upgradeUpgraded, time.Now().UTC().Unix(), id)
}

func (s *Server) endUpgrade(ctx context.Context, id int64, state, message string) error {
	if err := s.upgradeWrite(ctx, `UPDATE db_upgrades SET state = ?, phase = '', error = ?, ended_at = ? WHERE id = ?`,
		state, message, time.Now().UTC().Unix(), id); err != nil {
		return httpx.Internal(err)
	}
	return nil
}

// --- after a restart -----------------------------------------------------------

// recoverDatabaseUpgrades puts back the servers of upgrades a previous run of
// the dashboard was in the middle of. The job that was carrying one out went
// with that process, so nothing else will: an old server left taking reads
// only, a copy's container and volume, an old container set aside with no new
// one in its place. Each is put back as it was before the upgrade began, and
// the upgrade is recorded as failed.
func (s *Server) recoverDatabaseUpgrades(ctx context.Context) {
	if s.modules.docker == nil {
		return
	}
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT `+upgradeColumns+` FROM db_upgrades WHERE state = ?`, upgradeRunning)
	if err != nil {
		s.Log.Warn("interrupted database upgrades could not be read", "err", err)
		return
	}
	interrupted := []*dbUpgradeRecord{}
	for rows.Next() {
		if rec, err := scanUpgrade(rows.Scan); err == nil {
			interrupted = append(interrupted, rec)
		}
	}
	rows.Close()
	for _, rec := range interrupted {
		problems := s.undoInterruptedUpgrade(ctx, rec)
		message := "The dashboard stopped during the upgrade; the server was put back as it was."
		if len(problems) > 0 {
			message = "The dashboard stopped during the upgrade, and putting the server back did not all work: " + strings.Join(problems, "; ")
		}
		_ = s.endUpgrade(ctx, rec.ID, upgradeFailed, message)
		raw, _ := json.Marshal(map[string]any{"from": rec.FromVersion, "to": rec.ToVersion, "phase": rec.Phase, "problems": problems})
		s.Audit.Record(context.Background(), audit.Entry{
			Actor: "system", Action: "database.upgrade.recover", Target: rec.Container,
			Success: len(problems) == 0, Detail: string(raw),
		})
	}
	// The release a check started and never removed.
	if leftovers, err := s.modules.docker.ListContainersWithLabels(ctx, map[string]string{upgradeRoleLabel: "check"}); err == nil {
		for _, c := range leftovers {
			_ = s.modules.docker.RemoveContainer(ctx, c.ID, true, true)
		}
	}
}

// undoInterruptedUpgrade puts one server back, as far as its record says the
// upgrade got.
func (s *Server) undoInterruptedUpgrade(ctx context.Context, rec *dbUpgradeRecord) []string {
	problems := []string{}
	conn, dsn, err := s.dbConnRow(ctx, rec.connectionID)
	if err != nil {
		return []string{"its connection could not be read: " + err.Error()}
	}
	// Whatever now answers under the server's name and is not the old
	// container is the upgrade's, as is the copy's container.
	if current, err := s.modules.docker.Inspect(ctx, rec.Container); err == nil && current.ID != rec.previousID {
		problems = append(problems, s.removeScratch(ctx, current.ID, "")...)
	}
	problems = append(problems, s.removeScratch(ctx, rec.candidate, rec.Volume)...)
	previous, err := s.modules.docker.Inspect(ctx, rec.previousID)
	if err != nil {
		return append(problems, "the old server's container could not be read: "+err.Error())
	}
	renamed := previous.Name != rec.Container
	restart := rec.previousRestart
	if restart == "" {
		restart = previous.RestartPol
	}
	problems = append(problems, s.restorePrevious(ctx, conn, dsn, previous.ID, rec.Container, renamed, restart)...)
	if rec.Phase == "copy" || rec.Phase == "cutover" {
		env := envMap(previous.Env)
		user := env["POSTGRES_USER"]
		if user == "" {
			user = "postgres"
		}
		side := pgSide{container: previous.ID, user: user, password: env["POSTGRES_PASSWORD"]}
		if err := s.psqlRun(ctx, side, "postgres", dbx.PostgresTakeWrites(rec.readOnlyPrior, true)...); err != nil {
			problems = append(problems, fmt.Sprintf("%s may still take reads only: %v", rec.Container, err))
		}
	}
	return problems
}
