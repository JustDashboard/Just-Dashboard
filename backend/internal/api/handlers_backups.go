package api

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

func (s *Server) mountBackupRoutes(r chi.Router) {
	r.Route("/backups", func(r chi.Router) {
		r.Method(http.MethodGet, "/", s.handle(s.handleBackupJobList))
		r.Method(http.MethodGet, "/{id}", s.handle(s.handleBackupJobGet))
		r.Method(http.MethodGet, "/{id}/runs", s.handle(s.handleBackupRuns))
		r.Method(http.MethodGet, "/runs/{runID}/contents", s.handle(s.handleBackupContents))

		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
			r.Method(http.MethodPost, "/", s.handle(s.handleBackupJobCreate))
			r.Method(http.MethodPut, "/{id}", s.handle(s.handleBackupJobUpdate))
			r.Method(http.MethodPost, "/{id}/test", s.handle(s.handleBackupTestTarget))
			r.Method(http.MethodPost, "/runs/{runID}/verify-restore", s.handle(s.handleBackupVerifyRestore))
		})
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapServiceControl))
			r.Method(http.MethodPost, "/{id}/run", s.handle(s.handleBackupRunNow))
		})
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodDelete, "/{id}", s.handle(s.handleBackupJobDelete))
			r.Method(http.MethodPost, "/runs/{runID}/restore", s.handle(s.handleBackupRestore))
			r.Method(http.MethodPost, "/runs/{runID}/restore-database", s.handle(s.handleBackupRestoreDatabase))
		})
	})
}

// jobRequest carries credentials inbound only. They are sealed on arrival and
// never echoed back in any response.
type jobRequest struct {
	Name                   string                 `json:"name"`
	Sources                []string               `json:"sources"`
	Excludes               []string               `json:"excludes"`
	TargetKind             backups.TargetKind     `json:"targetKind"`
	Target                 backups.TargetConfig   `json:"target"`
	Schedule               string                 `json:"schedule"`
	Retention              int                    `json:"retention"`
	Enabled                bool                   `json:"enabled"`
	Secrets                *backups.TargetSecrets `json:"secrets,omitempty"`
	Recovery               *backups.RecoveryPlan  `json:"recovery,omitempty"`
	SQLitePaths            []string               `json:"sqlitePaths,omitempty"`
	DatabaseDumps          []int64                `json:"databaseDumps,omitempty"`
	ExpectedRecoveryOutput *string                `json:"expectedRecoveryOutput,omitempty"`
}

func (req *jobRequest) toJob() *backups.Job {
	if req.Recovery != nil && req.ExpectedRecoveryOutput != nil {
		digest := sha256.Sum256([]byte(strings.TrimSpace(*req.ExpectedRecoveryOutput)))
		req.Recovery.ExpectedOutputDigest = "sha256:" + hex.EncodeToString(digest[:])
	}
	return &backups.Job{
		Name: req.Name, Sources: req.Sources, Excludes: req.Excludes,
		TargetKind: req.TargetKind, Target: req.Target,
		Schedule: req.Schedule, Retention: req.Retention, Enabled: req.Enabled,
		Recovery: req.Recovery, SQLitePaths: req.SQLitePaths, DatabaseDumps: req.DatabaseDumps,
	}
}

func (s *Server) handleBackupVerifyRestore(w http.ResponseWriter, r *http.Request) error {
	runID, err := strconv.ParseInt(chi.URLParam(r, "runID"), 10, 64)
	if err != nil || runID <= 0 {
		return httpx.BadRequest("invalid run id")
	}
	httpx.SetAudit(r, "backup.restore.verify", strconv.FormatInt(runID, 10), nil)
	ctx, cancel := timeoutCtx(r, 10*time.Minute)
	defer cancel()
	record, err := s.modules.backupRunner.VerifyRestore(ctx, runID)
	if err != nil {
		if record != nil {
			return httpx.Err(http.StatusBadGateway, "restore_verification_failed", record.Detail)
		}
		return mapBackupError(err)
	}
	httpx.JSON(w, http.StatusOK, record)
	return nil
}

func mapBackupError(err error) error {
	switch {
	case errors.Is(err, backups.ErrNotFound):
		return httpx.ErrNotFound
	case errors.Is(err, backups.ErrAlreadyRunning):
		return httpx.Err(http.StatusConflict, "already_running", err.Error())
	case errors.Is(err, backups.ErrRecoveryUnavailable):
		return httpx.Err(http.StatusServiceUnavailable, "unavailable", err.Error())
	default:
		return httpx.BadRequest("%v", err)
	}
}

func (s *Server) handleBackupJobList(w http.ResponseWriter, r *http.Request) error {
	jobs, err := s.modules.backupStore.List(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	for _, j := range jobs {
		j.NextRun = s.modules.backupSched.NextRun(j.ID)
	}
	httpx.JSON(w, http.StatusOK, jobs)
	return nil
}

func (s *Server) handleBackupJobGet(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	job, err := s.modules.backupStore.Get(r.Context(), id)
	if err != nil {
		return mapBackupError(err)
	}
	job.NextRun = s.modules.backupSched.NextRun(id)
	if last, err := s.modules.backupStore.LastRun(r.Context(), id); err == nil {
		job.LastRun = last
	}
	httpx.JSON(w, http.StatusOK, job)
	return nil
}

func (s *Server) handleBackupJobCreate(w http.ResponseWriter, r *http.Request) error {
	var req jobRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if err := backups.ValidateSchedule(req.Schedule); err != nil {
		return httpx.BadRequest("schedule is not a valid cron expression: %v", err)
	}
	job, err := s.modules.backupStore.Create(r.Context(), req.toJob(), req.Secrets)
	if err != nil {
		return mapBackupError(err)
	}
	if err := s.modules.backupSched.Reload(r.Context()); err != nil {
		s.Log.Warn("backup schedule reload failed", "err", err)
	}
	httpx.SetAudit(r, "backup.job.create", job.Name,
		map[string]any{"target": job.TargetKind, "schedule": job.Schedule, "sources": len(job.Sources), "recoveryCheck": job.Recovery != nil, "sqliteSnapshots": len(job.SQLitePaths), "databaseDumps": len(job.DatabaseDumps)})
	httpx.JSON(w, http.StatusCreated, job)
	return nil
}

func (s *Server) handleBackupJobUpdate(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req jobRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if err := backups.ValidateSchedule(req.Schedule); err != nil {
		return httpx.BadRequest("schedule is not a valid cron expression: %v", err)
	}
	job, err := s.modules.backupStore.Update(r.Context(), id, req.toJob(), req.Secrets)
	if err != nil {
		return mapBackupError(err)
	}
	if err := s.modules.backupSched.Reload(r.Context()); err != nil {
		s.Log.Warn("backup schedule reload failed", "err", err)
	}
	httpx.SetAudit(r, "backup.job.update", job.Name,
		map[string]any{"target": job.TargetKind, "schedule": job.Schedule, "enabled": job.Enabled, "recoveryCheck": job.Recovery != nil, "sqliteSnapshots": len(job.SQLitePaths), "databaseDumps": len(job.DatabaseDumps)})
	httpx.JSON(w, http.StatusOK, job)
	return nil
}

func (s *Server) handleBackupJobDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	job, err := s.modules.backupStore.Get(r.Context(), id)
	if err != nil {
		return mapBackupError(err)
	}
	// No typed phrase: this deletes a schedule, not the archives it produced.
	// Restoring one of those is the route that still asks.
	if err := s.modules.backupStore.Delete(r.Context(), id); err != nil {
		return mapBackupError(err)
	}
	if err := s.modules.backupSched.Reload(r.Context()); err != nil {
		s.Log.Warn("backup schedule reload failed", "err", err)
	}
	// Stored artifacts are deliberately left alone: deleting the job should
	// not destroy the backups it already took.
	httpx.SetAudit(r, "backup.job.delete", job.Name, nil)
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleBackupTestTarget(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	job, err := s.modules.backupStore.Get(r.Context(), id)
	if err != nil {
		return mapBackupError(err)
	}
	if err := s.modules.backupStore.ValidatePaths(job); err != nil {
		return mapBackupError(err)
	}
	secrets, err := s.modules.backupStore.Secrets(r.Context(), id)
	if err != nil {
		return httpx.Internal(err)
	}
	if err := backups.TestTarget(r.Context(), job, secrets); err != nil {
		httpx.JSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return nil
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (s *Server) handleBackupRunNow(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	job, err := s.modules.backupStore.Get(r.Context(), id)
	if err != nil {
		return mapBackupError(err)
	}
	if err := s.modules.backupStore.ValidatePaths(job); err != nil {
		return mapBackupError(err)
	}
	if s.modules.backupRunner.IsRunning(id) {
		return httpx.Err(http.StatusConflict, "already_running", backups.ErrAlreadyRunning.Error())
	}
	// A backup can take hours, so it runs detached from the request rather
	// than holding a connection open for the whole transfer. The run row that
	// Execute creates is the progress record the UI polls.
	go func() {
		ctx, cancel := detachedContext(12 * 60 * 60)
		defer cancel()
		if _, err := s.modules.backupRunner.Execute(ctx, id, "manual"); err != nil {
			s.Log.Error("manual backup failed", "job", job.Name, "err", err)
		}
	}()
	httpx.SetAudit(r, "backup.run", job.Name, nil)
	httpx.JSON(w, http.StatusAccepted, map[string]any{"started": true, "job": job.Name})
	return nil
}

func (s *Server) handleBackupRuns(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	runs, err := s.modules.backupStore.Runs(r.Context(), id, atoiDefault(r.URL.Query().Get("limit"), 50))
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"runs":    runs,
		"running": s.modules.backupRunner.IsRunning(id),
	})
	return nil
}

func (s *Server) handleBackupContents(w http.ResponseWriter, r *http.Request) error {
	runID, err := strconv.ParseInt(chi.URLParam(r, "runID"), 10, 64)
	if err != nil {
		return httpx.BadRequest("invalid run id")
	}
	entries, err := s.modules.backupRunner.ListArchive(r.Context(), runID,
		atoiDefault(r.URL.Query().Get("limit"), 2000))
	if err != nil {
		return mapBackupError(err)
	}
	httpx.JSON(w, http.StatusOK, entries)
	return nil
}

type restoreRequest struct {
	Destination string `json:"destination"`
}

type restoreDatabaseRequest struct {
	ConnectionID int64  `json:"connectionId"`
	Database     string `json:"database"`
}

// handleBackupRestoreDatabase loads one native dump from a run back into its
// saved connection, or into another database on the same server for a drill.
// It overwrites live data, so the operator types the target database's name.
func (s *Server) handleBackupRestoreDatabase(w http.ResponseWriter, r *http.Request) error {
	runID, err := strconv.ParseInt(chi.URLParam(r, "runID"), 10, 64)
	if err != nil {
		return httpx.BadRequest("invalid run id")
	}
	var req restoreDatabaseRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.ConnectionID <= 0 {
		return httpx.BadRequest("connectionId is required")
	}
	conn, _, err := s.dbConnRow(r.Context(), req.ConnectionID)
	if err != nil {
		return err
	}
	target := strings.TrimSpace(req.Database)
	if target == "" {
		target = conn.Database
	}
	if target == "" {
		return httpx.BadRequest("the connection names no database; specify one explicitly")
	}
	if err := httpx.RequireTypedConfirmation(w, r, target); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 60*time.Minute)
	defer cancel()
	output, err := s.modules.backupRunner.RestoreDatabase(ctx, runID, req.ConnectionID, req.Database)
	if err != nil {
		httpx.SetAudit(r, "backup.restore.database", strconv.FormatInt(runID, 10),
			map[string]any{"connectionId": req.ConnectionID, "connection": conn.Name, "database": target, "error": err.Error()})
		return httpx.Err(http.StatusBadGateway, "restore_failed", err.Error())
	}
	httpx.SetAudit(r, "backup.restore.database", strconv.FormatInt(runID, 10),
		map[string]any{"connectionId": req.ConnectionID, "connection": conn.Name, "database": target})
	httpx.JSON(w, http.StatusOK, map[string]any{"connection": conn.Name, "database": target, "output": output})
	return nil
}

func (s *Server) handleBackupRestore(w http.ResponseWriter, r *http.Request) error {
	runID, err := strconv.ParseInt(chi.URLParam(r, "runID"), 10, 64)
	if err != nil {
		return httpx.BadRequest("invalid run id")
	}
	var req restoreRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Destination == "" {
		return httpx.BadRequest("destination is required")
	}
	// A restore overwrites whatever is at the destination, so the operator
	// types the destination path back to confirm they read it.
	if err := httpx.RequireTypedConfirmation(w, r, req.Destination); err != nil {
		return err
	}
	// The typed phrase is a guard against a slip, not an authorisation check —
	// it is a string the caller supplied twice. Invariant 6 is what bounds
	// where an archive may be unpacked, and it is files.Resolve that enforces
	// it; "not exactly /" was the only rule this destination had to satisfy.
	dest, err := s.modules.files.Resolve(req.Destination)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	ctx, cancel := timeoutCtx(r, 6*time.Hour)
	defer cancel()
	res, err := s.modules.backupRunner.Restore(ctx, runID, dest)
	if err != nil {
		httpx.SetAudit(r, "backup.restore", strconv.FormatInt(runID, 10),
			map[string]any{"destination": req.Destination, "error": err.Error()})
		return mapBackupError(err)
	}
	httpx.SetAudit(r, "backup.restore", strconv.FormatInt(runID, 10),
		map[string]any{"destination": res.Destination, "entries": res.Entries, "bytes": res.Bytes})
	httpx.JSON(w, http.StatusOK, res)
	return nil
}
