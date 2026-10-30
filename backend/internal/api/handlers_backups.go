package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
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
		// What the other modules hold that a job could protect, and which
		// jobs already do. Listed here rather than on each module's page so
		// "what is not backed up" has one answer.
		r.Method(http.MethodGet, "/resources", s.handle(s.handleBackupResources))
		// The backup key's public half and what depends on it, never the key.
		r.Method(http.MethodGet, "/key", s.handle(s.handleBackupKey))
		r.Method(http.MethodGet, "/{id}", s.handle(s.handleBackupJobGet))
		r.Method(http.MethodGet, "/{id}/runs", s.handle(s.handleBackupRuns))
		r.Method(http.MethodGet, "/runs/{runID}/contents", s.handle(s.handleBackupContents))
		// Where a restore in place would write on this host, and what it
		// would make first, read from the manifest before anything is done.
		r.Method(http.MethodGet, "/runs/{runID}/restore-plan", s.handle(s.handleBackupRestorePlan))

		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
			r.Method(http.MethodPost, "/", s.handle(s.handleBackupJobCreate))
			r.Method(http.MethodPut, "/{id}", s.handle(s.handleBackupJobUpdate))
			r.Method(http.MethodPost, "/{id}/enabled", s.handle(s.handleBackupJobEnabled))
			r.Method(http.MethodPost, "/{id}/database-dumps", s.handle(s.handleBackupJobAddDatabaseDump))
			r.Method(http.MethodPost, "/{id}/test", s.handle(s.handleBackupTestTarget))
			r.Method(http.MethodPost, "/runs/{runID}/verify-restore", s.handle(s.handleBackupVerifyRestore))
			// An archive is the whole of what it covers — dumps, secrets,
			// configuration — so taking one off the server is an
			// administrator's act, not a reader's.
			r.Method(http.MethodGet, "/runs/{runID}/download", s.handle(s.handleBackupDownload))
			// What a destination holds that this server did not write, and
			// taking it on as imported runs; the key that opens what another
			// server sealed. POSTs, because each carries a destination's keys
			// or a backup key, never in a URL.
			r.Method(http.MethodPost, "/catalogue", s.handle(s.handleBackupCatalogue))
			r.Method(http.MethodPost, "/import", s.handle(s.handleBackupImport))
			r.Method(http.MethodPost, "/key/import", s.handle(s.handleBackupKeyImport))
			// The key opens every encrypted archive this server wrote, wherever
			// they are copied to, and nothing here can take an exported copy
			// back: an irreversible act, so the destructive budget too.
			s.destructive(r, func(r chi.Router) {
				r.Method(http.MethodPost, "/key/export", s.handle(s.handleBackupKeyExport))
			})
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
	ExpectedRevision       int64                  `json:"expectedRevision,omitempty"`
	Name                   string                 `json:"name"`
	Sources                []string               `json:"sources"`
	Excludes               []string               `json:"excludes"`
	TargetKind             backups.TargetKind     `json:"targetKind"`
	Target                 backups.TargetConfig   `json:"target"`
	Schedule               string                 `json:"schedule"`
	Retention              int                    `json:"retention"`
	RetentionDays          int                    `json:"retentionDays"`
	Enabled                bool                   `json:"enabled"`
	Secrets                *backups.TargetSecrets `json:"secrets,omitempty"`
	Recovery               *backups.RecoveryPlan  `json:"recovery,omitempty"`
	SQLitePaths            []string               `json:"sqlitePaths,omitempty"`
	DatabaseDumps          []int64                `json:"databaseDumps,omitempty"`
	PauseContainers        []string               `json:"pauseContainers,omitempty"`
	ExpectedRecoveryOutput *string                `json:"expectedRecoveryOutput,omitempty"`
	// Encrypt is omitted by a caller that predates it: a new job then takes
	// the default for its destination and an edited one keeps its setting.
	Encrypt *bool `json:"encrypt,omitempty"`
	// ForgetHostKey drops an SFTP destination's pinned host key, for a
	// server that was reinstalled; it is pinned again when next reached.
	ForgetHostKey bool `json:"forgetHostKey,omitempty"`
}

func (req *jobRequest) toJob() *backups.Job {
	if req.Recovery != nil && req.ExpectedRecoveryOutput != nil {
		digest := sha256.Sum256([]byte(strings.TrimSpace(*req.ExpectedRecoveryOutput)))
		req.Recovery.ExpectedOutputDigest = "sha256:" + hex.EncodeToString(digest[:])
	}
	return &backups.Job{
		Revision: req.ExpectedRevision,
		Name:     req.Name, Sources: req.Sources, Excludes: req.Excludes,
		TargetKind: req.TargetKind, Target: req.Target,
		Schedule: req.Schedule, Retention: req.Retention, RetentionDays: req.RetentionDays, Enabled: req.Enabled,
		Recovery: req.Recovery, SQLitePaths: req.SQLitePaths, DatabaseDumps: req.DatabaseDumps,
		PauseContainers: req.PauseContainers, Encrypt: req.encrypt(), ForgetHostKey: req.ForgetHostKey,
	}
}

func (req *jobRequest) encrypt() bool {
	if req.Encrypt != nil {
		return *req.Encrypt
	}
	// An archive that leaves the server is encrypted unless the operator says
	// otherwise. One kept on this server's disk sits beside the key that
	// would open it, so it is not by default.
	return req.TargetKind != backups.TargetLocal
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
	var keyErr *backups.KeyError
	switch {
	case errors.As(err, &keyErr):
		// The key the manifest names, as its own field, so the page can offer
		// to import that one.
		refused := httpx.Err(http.StatusConflict, "archive_key_unavailable", err.Error())
		refused.Resource = keyErr.Recipient
		return refused
	case errors.Is(err, backups.ErrDigestMismatch):
		return httpx.Err(http.StatusUnprocessableEntity, "archive_digest_mismatch", err.Error())
	case errors.Is(err, backups.ErrImportedJob):
		return httpx.Err(http.StatusConflict, "backup_job_imported", err.Error())
	case errors.Is(err, backups.ErrNothingToImport):
		return httpx.Err(http.StatusNotFound, "nothing_to_import", err.Error())
	case errors.Is(err, backups.ErrNotFound):
		return httpx.ErrNotFound
	case errors.Is(err, backups.ErrConflict):
		return httpx.Err(http.StatusConflict, "backup_job_conflict", err.Error())
	case errors.Is(err, backups.ErrAlreadyRunning):
		return httpx.Err(http.StatusConflict, "already_running", err.Error())
	case errors.Is(err, backups.ErrRecoveryUnavailable):
		return httpx.Err(http.StatusServiceUnavailable, "unavailable", err.Error())
	case errors.Is(err, backups.ErrNoArchiveKey), errors.Is(err, backups.ErrArchiveKey):
		return httpx.Err(http.StatusConflict, "archive_key_unavailable", err.Error())
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

func (s *Server) handleBackupResources(w http.ResponseWriter, r *http.Request) error {
	jobs, err := s.modules.backupStore.List(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	// Discovery walks the Git roots and asks Docker; a slow host must not
	// hold the page for longer than it takes to read the answer.
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	httpx.JSON(w, http.StatusOK, s.backupResources(ctx, jobs))
	return nil
}

type enabledRequest struct {
	Enabled bool `json:"enabled"`
}

// handleBackupJobEnabled pauses or resumes a schedule. It is its own route
// rather than an edit so that pausing a job never re-sends its definition —
// and so the audit log says "paused" rather than "updated".
func (s *Server) handleBackupJobEnabled(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req enabledRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	job, err := s.modules.backupStore.SetEnabled(r.Context(), id, req.Enabled)
	if err != nil {
		return mapBackupError(err)
	}
	if err := s.modules.backupSched.Reload(r.Context()); err != nil {
		s.Log.Warn("backup schedule reload failed", "err", err)
	}
	job.NextRun = s.modules.backupSched.NextRun(id)
	action := "backup.job.pause"
	if req.Enabled {
		action = "backup.job.resume"
	}
	httpx.SetAudit(r, action, job.Name, nil)
	httpx.JSON(w, http.StatusOK, job)
	return nil
}

// handleBackupDownload streams one run's verified artifact to the browser.
// A remote artifact is fetched into private staging first, so the checksum
// is proven before a byte leaves — the same path a restore takes. An
// encrypted one leaves as the .tar.gz inside it: the administrator taking it
// off the server wants an archive that opens, and the key stays here.
func (s *Server) handleBackupDownload(w http.ResponseWriter, r *http.Request) error {
	runID, err := strconv.ParseInt(chi.URLParam(r, "runID"), 10, 64)
	if err != nil || runID <= 0 {
		return httpx.BadRequest("invalid run id")
	}
	ctx, cancel := timeoutCtx(r, 6*time.Hour)
	defer cancel()
	run, archive, cleanup, err := s.modules.backupRunner.OpenArtifact(ctx, runID)
	if err != nil {
		return mapBackupError(err)
	}
	defer cleanup()
	target := strconv.FormatInt(runID, 10)
	name := filepath.Base(run.Artifact)
	w.Header().Set("Content-Type", "application/gzip")
	if !archive.Encrypted() {
		f, err := os.Open(archive.Path)
		if err != nil {
			return httpx.Internal(err)
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			return httpx.Internal(err)
		}
		// A GET passes the audit middleware by, so the entry is written here.
		s.recordRead(r, "backup.download", target, map[string]any{"artifact": run.Artifact, "bytes": st.Size()}, nil)
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
		http.ServeContent(w, r, name, st.ModTime(), f)
		return nil
	}
	plain, err := archive.Open()
	if err != nil {
		return mapBackupError(err)
	}
	defer plain.Close()
	size := run.Manifest.Encryption.PlaintextBytes
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": strings.TrimSuffix(name, ".age")}))
	// The length is how the browser knows a download that stops short of it
	// is not whole.
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	_, err = io.Copy(w, plain)
	s.recordRead(r, "backup.download", target, map[string]any{"artifact": run.Artifact, "bytes": size, "decrypted": true}, err)
	if err != nil {
		// Too late for an error response: a chunk that fails to authenticate
		// ends the transfer short of its length instead.
		panic(http.ErrAbortHandler)
	}
	return nil
}

// handleBackupKey describes the install's backup key: its public half, when
// it was made and last exported, and how much depends on it.
func (s *Server) handleBackupKey(w http.ResponseWriter, r *http.Request) error {
	status, err := s.modules.backupStore.ArchiveKeyStatus(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, status)
	return nil
}

// handleBackupKeyExport answers with the backup key as an age identity file,
// for opening archives where this dashboard is not. The audit entry names the
// key by its public half and never holds the secret one.
func (s *Server) handleBackupKeyExport(w http.ResponseWriter, r *http.Request) error {
	recipient, file, err := s.modules.backupStore.ExportArchiveKey(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "backup.key.export", recipient, nil)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": "just-dashboard-backup-key.txt"}))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(file)
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
		map[string]any{"target": job.TargetKind, "schedule": job.Schedule, "sources": len(job.Sources), "recoveryCheck": job.Recovery != nil, "sqliteSnapshots": len(job.SQLitePaths), "databaseDumps": len(job.DatabaseDumps), "encrypt": job.Encrypt})
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
	if req.ExpectedRevision < 0 {
		return httpx.BadRequest("expectedRevision must be positive when supplied")
	}
	if err := backups.ValidateSchedule(req.Schedule); err != nil {
		return httpx.BadRequest("schedule is not a valid cron expression: %v", err)
	}
	update := req.toJob()
	if req.Encrypt == nil {
		current, err := s.modules.backupStore.Get(r.Context(), id)
		if err != nil {
			return mapBackupError(err)
		}
		update.Encrypt = current.Encrypt
	}
	job, err := s.modules.backupStore.Update(r.Context(), id, update, req.Secrets)
	if err != nil {
		return mapBackupError(err)
	}
	if err := s.modules.backupSched.Reload(r.Context()); err != nil {
		s.Log.Warn("backup schedule reload failed", "err", err)
	}
	httpx.SetAudit(r, "backup.job.update", job.Name,
		map[string]any{"target": job.TargetKind, "schedule": job.Schedule, "enabled": job.Enabled, "recoveryCheck": job.Recovery != nil, "sqliteSnapshots": len(job.SQLitePaths), "databaseDumps": len(job.DatabaseDumps), "encrypt": job.Encrypt})
	httpx.JSON(w, http.StatusOK, job)
	return nil
}

func (s *Server) handleBackupJobAddDatabaseDump(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req struct {
		ConnectionID int64 `json:"connectionId"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.ConnectionID <= 0 {
		return httpx.BadRequest("connectionId is required")
	}
	job, err := s.modules.backupStore.AddDatabaseDump(r.Context(), id, req.ConnectionID)
	if err != nil {
		return mapBackupError(err)
	}
	if err := s.modules.backupSched.Reload(r.Context()); err != nil {
		s.Log.Warn("backup schedule reload failed", "err", err)
	}
	httpx.SetAudit(r, "backup.job.database_dump.add", job.Name, map[string]any{"connectionId": req.ConnectionID})
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
	// Restoring one of those also uses ordinary confirmation.
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
	pinned := job.Target.HostKeyFingerprint
	err = backups.TestTarget(r.Context(), job, secrets, s.modules.backupStore.Pinner(id))
	result := map[string]any{"ok": err == nil}
	if err != nil {
		result["error"] = err.Error()
	}
	// An SFTP server's key is said with every answer, and once as newly
	// pinned, so the operator can compare it with the server's own.
	if job.Target.HostKeyFingerprint != "" {
		result["hostKeyFingerprint"] = job.Target.HostKeyFingerprint
		if pinned == "" {
			result["hostKeyPinned"] = true
			httpx.SetAudit(r, "backup.target.pin", job.Name, map[string]any{"fingerprint": job.Target.HostKeyFingerprint})
		}
	}
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

func (s *Server) handleBackupRunNow(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	job, err := s.startBackupRun(r.Context(), httpx.MustPrincipal(r).Username(), id)
	if err != nil {
		return err
	}
	httpx.SetAudit(r, "backup.run", job.Name, nil)
	httpx.JSON(w, http.StatusAccepted, map[string]any{"started": true, "job": job.Name})
	return nil
}

// startBackupRun starts a job's run now and returns once it has begun, for the
// Run now button and for a linked site's backup.run alike. A backup can take
// hours, so it runs detached from the caller rather than holding a connection
// open for the whole transfer. The run row that Execute creates is the progress
// record the UI polls.
//
// The run is recorded as manual, or as coming from the linked account when the
// actor is one, so the job's history says which.
func (s *Server) startBackupRun(ctx context.Context, actor string, id int64) (*backups.Job, error) {
	job, err := s.modules.backupStore.Get(ctx, id)
	if err != nil {
		return nil, mapBackupError(err)
	}
	if job.Imported {
		return nil, mapBackupError(backups.ErrImportedJob)
	}
	if err := s.modules.backupStore.ValidatePaths(job); err != nil {
		return nil, mapBackupError(err)
	}
	if s.modules.backupRunner.IsRunning(id) {
		return nil, httpx.Err(http.StatusConflict, "already_running", backups.ErrAlreadyRunning.Error())
	}
	trigger, failure := "manual", "manual backup failed"
	if strings.HasPrefix(actor, "account:") {
		trigger, failure = "account_link", "backup started by a linked account failed"
	}
	go func() {
		ctx, cancel := detachedContext(12 * 60 * 60)
		defer cancel()
		if _, err := s.modules.backupRunner.Execute(ctx, id, trigger); err != nil {
			s.Log.Error(failure, "job", job.Name, "err", err)
		}
	}()
	return job, nil
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
	// InPlace writes every recorded source back over the path it came from.
	InPlace bool `json:"inPlace"`
	// Paths narrows the restore to these archive entries and what is under
	// them — one file, one directory — rather than the whole run.
	Paths []string `json:"paths"`
}

type restoreDatabaseRequest struct {
	ConnectionID int64  `json:"connectionId"`
	Database     string `json:"database"`
	// Dump names the dump by where it lies in the archive (database-0001/…),
	// and ConnectionID is then the connection it goes into, of the same
	// engine. An imported run's dumps are named this way: the connection ids
	// its manifest records are the other server's.
	Dump string `json:"dump,omitempty"`
}

// handleBackupRestoreDatabase loads one native dump from a run back into its
// saved connection, or into another database on the same server for a drill.
// It overwrites live data, so the route uses the destructive gate and audit.
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
	if req.Dump == "" {
		if run, err := s.modules.backupStore.Run(r.Context(), runID); err == nil && run.Imported {
			return httpx.BadRequest("this run was imported, and the connection ids its manifest records are another server's; name the dump by its archive path and the connection here it goes into")
		}
	}
	conn, _, err := s.dbConnRow(r.Context(), req.ConnectionID)
	if err != nil {
		return err
	}
	// The second way into a database's restore, and the one the middleware in
	// front of /databases/{id} does not stand in front of.
	if conn.ReadOnly {
		return protectedRefusal("%s is protected: a dump cannot be restored into it until protection is turned off in its settings", conn.Name)
	}
	target := strings.TrimSpace(req.Database)
	if target == "" {
		target = conn.Database
	}
	if target == "" {
		return httpx.BadRequest("the connection names no database; specify one explicitly")
	}
	// A drill database named here may be the one a protected connection to
	// the same server is on.
	if err := s.refuseDatabaseOfProtected(r.Context(), conn, target); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 60*time.Minute)
	defer cancel()
	var output string
	if req.Dump != "" {
		output, err = s.modules.backupRunner.RestoreDatabaseDump(ctx, runID, req.Dump, req.ConnectionID, req.Database)
	} else {
		output, err = s.modules.backupRunner.RestoreDatabase(ctx, runID, req.ConnectionID, req.Database)
	}
	if err != nil {
		httpx.SetAudit(r, "backup.restore.database", strconv.FormatInt(runID, 10),
			map[string]any{"connectionId": req.ConnectionID, "connection": conn.Name, "database": target, "dump": req.Dump, "error": err.Error()})
		var keyErr *backups.KeyError
		if errors.As(err, &keyErr) || errors.Is(err, backups.ErrDigestMismatch) {
			return mapBackupError(err)
		}
		// The adapter refuses a protected database by whichever road it is
		// reached, and that refusal keeps its own status.
		var refused *httpx.APIError
		if errors.As(err, &refused) && refused.Code == "connection_read_only" {
			return refused
		}
		return httpx.Err(http.StatusBadGateway, "restore_failed", err.Error())
	}
	httpx.SetAudit(r, "backup.restore.database", strconv.FormatInt(runID, 10),
		map[string]any{"connectionId": req.ConnectionID, "connection": conn.Name, "database": target, "dump": req.Dump})
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
	opts := backups.RestoreOptions{Paths: req.Paths}
	if req.InPlace {
		ctx, cancel := timeoutCtx(r, 6*time.Hour)
		defer cancel()
		res, err := s.modules.backupRunner.RestoreInPlace(ctx, runID, opts)
		if err != nil {
			httpx.SetAudit(r, "backup.restore", strconv.FormatInt(runID, 10),
				map[string]any{"inPlace": true, "paths": len(req.Paths), "error": err.Error()})
			return mapBackupError(err)
		}
		httpx.SetAudit(r, "backup.restore", strconv.FormatInt(runID, 10),
			map[string]any{"inPlace": true, "targets": res.Targets, "created": res.Created, "paths": len(req.Paths), "entries": res.Entries, "bytes": res.Bytes})
		httpx.JSON(w, http.StatusOK, res)
		return nil
	}
	if req.Destination == "" {
		return httpx.BadRequest("destination is required")
	}
	// files.Resolve bounds where an archive may be unpacked, including symlink targets.
	dest, err := s.modules.files.Resolve(req.Destination)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	ctx, cancel := timeoutCtx(r, 6*time.Hour)
	defer cancel()
	res, err := s.modules.backupRunner.Restore(ctx, runID, dest, opts)
	if err != nil {
		httpx.SetAudit(r, "backup.restore", strconv.FormatInt(runID, 10),
			map[string]any{"destination": req.Destination, "paths": len(req.Paths), "error": err.Error()})
		return mapBackupError(err)
	}
	httpx.SetAudit(r, "backup.restore", strconv.FormatInt(runID, 10),
		map[string]any{"destination": res.Destination, "paths": len(req.Paths), "entries": res.Entries, "bytes": res.Bytes})
	httpx.JSON(w, http.StatusOK, res)
	return nil
}
