package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/portalloc"
	"github.com/docker/docker/errdefs"
	"github.com/go-chi/chi/v5"
)

// The point-in-time recovery routes of one connection's server. Reading is
// on the read surface, as the dumps are. A base backup and a restore point
// are routine, as a dump is. Setting archiving up or down restarts the
// server, switching a copy in replaces it, and deleting an archive throws
// away the moments it held, so those four are destructive; a recovery into a
// new container is an administrator's, as starting a server is. Setting up
// shares its path with the plan the form reads as it is filled in, which
// changes nothing, so that one asks for the destructive capability and its
// budget by hand, for the request that is not a preview.
func (s *Server) mountDatabasePITRRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/{id}/pitr", s.handle(s.handleDBPITR))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapServiceControl))
		r.Method(http.MethodPost, "/{id}/pitr/backup", s.handle(s.handleDBPITRBackup))
		r.Method(http.MethodPost, "/{id}/pitr/restore-points", s.handle(s.handleDBPITRRestorePoint))
	})
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodPost, "/{id}/pitr", s.handle(s.handleDBPITREnable))
		r.Method(http.MethodPut, "/{id}/pitr", s.handle(s.handleDBPITRUpdate))
		r.Method(http.MethodPost, "/{id}/pitr/restore", s.handle(s.handleDBPITRRestore))
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodDelete, "/{id}/pitr", s.handle(s.handleDBPITRDisable))
			r.Method(http.MethodPost, "/{id}/pitr/restores/{rid}/switch", s.handle(s.handleDBPITRSwitch))
			r.Method(http.MethodDelete, "/{id}/pitr/archives/{aid}", s.handle(s.handleDBPITRArchiveDelete))
		})
	})
}

// dbPITR is what the Backups page reads about a server's point-in-time
// recovery.
type dbPITR struct {
	// Available says whether this server can be set up for it, and Reason
	// why not. A server that already archives is read whatever Available
	// says: what it can recover to is the point.
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	Container string `json:"container,omitempty"`
	Running   bool   `json:"running"`
	// Archive is the archive the server writes into, absent when it writes
	// none; Earlier are the ones it wrote into before, still kept.
	Archive *pitrArchive   `json:"archive,omitempty"`
	Earlier []*pitrArchive `json:"earlier"`
	// Drift is an archive this dashboard set up on a server whose command no
	// longer archives into it: the container was made again without the
	// settings, from somewhere other than this page.
	Drift    bool             `json:"drift,omitempty"`
	Archiver *dbx.WALArchiver `json:"archiver,omitempty"`
	// ArchiveError is what WAL-G last said in the server's log about a
	// segment it could not archive.
	ArchiveError string `json:"archiveError,omitempty"`
	// Window is what the archive can recover to now: its kept listing, up to
	// the last segment the server says it handed over since.
	Window     dbx.WALWindow `json:"window"`
	NextBackup *time.Time    `json:"nextBackup,omitempty"`
	// ReadError is why the destination could not be listed just now; the
	// archive's kept reading is answered beside it.
	ReadError     string             `json:"readError,omitempty"`
	RestorePoints []pitrRestorePoint `json:"restorePoints"`
	Restores      []*pitrRestore     `json:"restores"`
	// Job is the point-in-time recovery job running for the server now.
	Job      *jobs.Job    `json:"job,omitempty"`
	Defaults pitrDefaults `json:"defaults"`
}

// pitrDefaults is what the setup form starts from.
type pitrDefaults struct {
	Schedule      string `json:"schedule"`
	RetentionDays int    `json:"retentionDays"`
	Directory     string `json:"directory"`
}

func (s *Server) pitrDefaults() pitrDefaults {
	dir := s.Cfg.BackupLocalDir
	if dir == "" {
		dir = "/var/backups/just-dashboard"
	}
	return pitrDefaults{Schedule: pitrDefaultSchedule, RetentionDays: pitrDefaultDays, Directory: strings.TrimRight(dir, "/") + "/wal"}
}

// pitrToolProblem is why this dashboard cannot hand WAL-G to a server.
func pitrToolProblem() string {
	if _, err := os.Stat(dbx.WALGBinary); err != nil {
		return "This dashboard has no WAL-G to archive with. It is installed in the dashboard's image from 0.7.5 on; update the dashboard to set point-in-time recovery up."
	}
	return ""
}

func (s *Server) handleDBPITR(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	id, err := parseID(r)
	if err != nil {
		return err
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	out, err := s.readDBPITR(ctx, conn, dsn)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) readDBPITR(ctx context.Context, conn *dbConnection, dsn string) (*dbPITR, error) {
	out := &dbPITR{Earlier: []*pitrArchive{}, RestorePoints: []pitrRestorePoint{}, Restores: []*pitrRestore{}, Defaults: s.pitrDefaults()}
	srv := s.pitrServerOf(ctx, conn, dsn)
	out.Container, out.Running, out.Reason = srv.name, srv.running, srv.reason
	if out.Reason == "" {
		out.Reason = pitrToolProblem()
	}
	out.Available = out.Reason == ""
	if srv.name == "" {
		return out, nil
	}
	archives, err := s.pitrArchivesOf(ctx, srv.name)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	for _, a := range archives {
		if a.EndedAt == nil {
			out.Archive = a
		} else {
			out.Earlier = append(out.Earlier, a)
		}
	}
	if job, running := s.runningPITRJob(srv.name); running {
		out.Job = &job
	}
	if a := out.Archive; a != nil {
		// The destination is asked again only through the running server:
		// a reading must not start a container of its own.
		if srv.running && srv.reason == "" {
			out.ReadError = s.freshPITRReading(ctx, srv, a)
		}
		out.Drift = srv.spec != nil && !dbx.ArchivesWithWALG(srv.spec.Command)
		if a.Reading != nil {
			out.Window = a.Reading.Window
		}
		if srv.running {
			if pool, err := s.modules.dbs.Pool(ctx, conn.ID, conn.Driver, dsn); err == nil {
				if archiver, err := dbx.ReadWALArchiver(ctx, pool); err == nil {
					out.Archiver = archiver
				}
			}
		}
		if out.Archiver != nil && out.Archiver.LastArchivedAt != nil && out.Window.To != nil &&
			out.Archiver.LastArchivedAt.After(*out.Window.To) {
			latest := *out.Archiver.LastArchivedAt
			out.Window.To = &latest
		}
		if out.Archiver.Failing() {
			out.ArchiveError = s.pitrArchiveError(ctx, srv.id)
		}
		out.NextBackup = s.nextPITRBackup(a.ID)
		if out.RestorePoints, err = s.pitrRestorePoints(ctx, a.ID); err != nil {
			return nil, httpx.Internal(err)
		}
	}
	restores, err := s.pitrRestoresOf(ctx, srv.name)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	for _, restore := range restores {
		// A copy still recovering is its job's, shown as the job.
		if restore.State == "recovering" {
			continue
		}
		// A copy kept beside the server is a database of its own, and is
		// removed like one; once it is gone it is not listed here.
		if restore.State == "ready" {
			if _, err := s.modules.docker.Inspect(ctx, restore.Container); errdefs.IsNotFound(err) {
				_ = s.deletePITRRestore(ctx, restore.ID)
				continue
			}
		}
		out.Restores = append(out.Restores, restore)
	}
	return out, nil
}

// pitrArchiveError reads the end of the server's log for what WAL-G said
// when it last could not archive.
func (s *Server) pitrArchiveError(ctx context.Context, container string) string {
	lines, closer, err := s.modules.docker.Logs(ctx, container, dockerx.LogOptions{Tail: "400"})
	if err != nil {
		return ""
	}
	defer closer.Close()
	var text []string
	for line := range lines {
		text = append(text, line.Text)
	}
	return dbx.LastArchiveError(text)
}

// pitrSetupRequest is what an archive is set up with.
type pitrSetupRequest struct {
	TargetKind backups.TargetKind     `json:"targetKind"`
	Target     backups.TargetConfig   `json:"target"`
	Secrets    *backups.TargetSecrets `json:"secrets"`
	// FromJob takes the destination, keys and all, from a backup job, so a
	// bucket's keys are not typed in twice.
	FromJob int64 `json:"fromJob"`
	// Schedule is when base backups are taken, as a backup job's; absent is
	// every night, and empty is only by hand.
	Schedule *string `json:"schedule"`
	// RetentionDays is how many days of moments are kept; absent is a week
	// and zero is everything.
	RetentionDays *int `json:"retentionDays"`
	// Preview answers what would be done, and does none of it.
	Preview bool `json:"preview"`
}

// readPITRSchedule reads a request's schedule and retention against the
// archive's current ones.
func readPITRSchedule(schedule *string, days *int, current string, currentDays int) (string, int, error) {
	if schedule != nil {
		current = strings.TrimSpace(*schedule)
	}
	if err := backups.ValidateSchedule(current); err != nil {
		return "", 0, httpx.BadRequest("the base backup schedule is not a cron expression: %v", err)
	}
	if days != nil {
		currentDays = *days
	}
	if currentDays < 0 || currentDays > 3650 {
		return "", 0, httpx.BadRequest("keep between 0 days (everything) and 3650")
	}
	return current, currentDays, nil
}

// pitrDestination resolves where a request asks an archive to go: a backup
// job's destination, or the one it names. A directory goes through the file
// roots like every client path.
func (s *Server) pitrDestination(ctx context.Context, req pitrSetupRequest) (backups.TargetKind, backups.TargetConfig, *backups.TargetSecrets, error) {
	kind, target, secrets := req.TargetKind, req.Target, req.Secrets
	if req.FromJob > 0 {
		job, err := s.modules.backupStore.Get(ctx, req.FromJob)
		if errors.Is(err, backups.ErrNotFound) {
			return "", backups.TargetConfig{}, nil, httpx.BadRequest("no backup job %d", req.FromJob)
		}
		if err != nil {
			return "", backups.TargetConfig{}, nil, httpx.Internal(err)
		}
		if secrets, err = s.modules.backupStore.Secrets(ctx, req.FromJob); err != nil {
			return "", backups.TargetConfig{}, nil, httpx.Internal(err)
		}
		kind, target = job.TargetKind, job.Target
		if kind == backups.TargetLocal {
			// The job's own directory holds its archives; the log goes in
			// a folder beside them.
			target.Path = strings.TrimRight(target.Path, "/") + "/wal"
		}
	}
	if !kind.Valid() {
		return "", backups.TargetConfig{}, nil, httpx.BadRequest("the destination is one of local, s3, b2")
	}
	switch kind {
	case backups.TargetLocal:
		if strings.TrimSpace(target.Path) == "" {
			return "", backups.TargetConfig{}, nil, httpx.BadRequest("a directory on this server is needed")
		}
		resolved, err := s.modules.files.Resolve(strings.TrimSpace(target.Path))
		if err != nil {
			return "", backups.TargetConfig{}, nil, httpx.BadRequest("%v", err)
		}
		target = backups.TargetConfig{Path: resolved}
	case backups.TargetS3, backups.TargetB2:
		target.Path = ""
		if strings.TrimSpace(target.Bucket) == "" {
			return "", backups.TargetConfig{}, nil, httpx.BadRequest("an object storage destination needs a bucket")
		}
		if kind == backups.TargetB2 && strings.TrimSpace(target.Endpoint) == "" {
			return "", backups.TargetConfig{}, nil, httpx.BadRequest("Backblaze B2 needs its S3-compatible endpoint, for example s3.us-west-004.backblazeb2.com")
		}
	}
	return kind, target, secrets, nil
}

func (s *Server) pitrRequestServer(r *http.Request) (*pitrServer, error) {
	id, err := parseID(r)
	if err != nil {
		return nil, err
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return nil, err
	}
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	return s.pitrServerOf(ctx, conn, dsn), nil
}

// activePITRArchive is the archive a server writes into, or a refusal.
func (s *Server) activePITRArchive(ctx context.Context, srv *pitrServer) (*pitrArchive, error) {
	if srv.name == "" {
		return nil, pitrRefused(srv.reason)
	}
	archives, err := s.pitrArchivesOf(ctx, srv.name)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	for _, a := range archives {
		if a.EndedAt == nil {
			return a, nil
		}
	}
	return nil, httpx.Err(http.StatusConflict, "pitr_off", fmt.Sprintf("%s does not archive its log; set point-in-time recovery up first", srv.name))
}

func (s *Server) handleDBPITREnable(w http.ResponseWriter, r *http.Request) error {
	var req pitrSetupRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if !req.Preview {
		if err := s.requireDestructive(r, "pitr", "setting point-in-time recovery up restarts the server, which"); err != nil {
			return err
		}
	}
	srv, err := s.pitrRequestServer(r)
	if err != nil {
		return err
	}
	if srv.reason != "" {
		return pitrRefused(srv.reason)
	}
	if problem := pitrToolProblem(); problem != "" {
		return pitrRefused(problem)
	}
	if !srv.running {
		return pitrRefused(fmt.Sprintf("%s is not running; start it, then set point-in-time recovery up", srv.name))
	}
	ctx, cancel := timeoutCtx(r, time.Minute)
	defer cancel()
	if active, err := s.activePITRArchive(ctx, srv); err == nil {
		return httpx.Err(http.StatusConflict, "pitr_on", fmt.Sprintf("%s already archives into %s", srv.name, pitrLocation(active)))
	}
	kind, target, secrets, err := s.pitrDestination(ctx, req)
	if err != nil {
		return err
	}
	schedule, days, err := readPITRSchedule(req.Schedule, req.RetentionDays, pitrDefaultSchedule, pitrDefaultDays)
	if err != nil {
		return err
	}
	principal := httpx.MustPrincipal(r)
	a := &pitrArchive{
		Container: srv.name, ConnectionID: srv.conn.ID, Stream: dbx.WALStreamName(srv.name, time.Now()),
		TargetKind: kind, Target: target, Schedule: schedule, RetentionDays: days, Image: srv.image,
		CreatedBy: principal.Username(),
	}
	walLevel := s.pitrWALLevel(ctx, srv)
	plan := planPITR(srv, a, walLevel)
	if req.Preview {
		httpx.SkipAudit(r)
		httpx.JSON(w, http.StatusOK, plan)
		return nil
	}
	// Before anything is touched, as a backup job's destination is tested.
	if err := backups.TestTarget(ctx, &backups.Job{TargetKind: kind, Target: backups.TargetConfig{
		Bucket: target.Bucket, Region: target.Region, Endpoint: target.Endpoint, Prefix: target.Prefix, Path: target.Path,
	}}, secrets); err != nil {
		return httpx.Err(http.StatusBadRequest, "destination_unreachable", fmt.Sprintf("the destination could not be used: %v", err))
	}
	detail := map[string]any{
		"connection": srv.conn.Name, "targetKind": kind, "location": plan.Location,
		"schedule": schedule, "retentionDays": days, "fromJob": req.FromJob,
	}
	httpx.SetAudit(r, "database.pitr.enable", srv.name, detail)
	base := auditBaseOf(r)
	return s.startPITRJob(w, r, srv, "enable", "Set up point-in-time recovery for "+srv.name, pitrBackupTimeout,
		func(ctx context.Context, out jobs.Emitter) error {
			err := s.enablePITR(ctx, srv, a, secrets, walLevel, out)
			s.recordTransfer(base, "database.pitr.enable.finish", srv.name, map[string]any{"archive": a.ID, "location": plan.Location}, err)
			return err
		})
}

type pitrUpdateRequest struct {
	Schedule      *string `json:"schedule"`
	RetentionDays *int    `json:"retentionDays"`
}

func (s *Server) handleDBPITRUpdate(w http.ResponseWriter, r *http.Request) error {
	var req pitrUpdateRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	srv, err := s.pitrRequestServer(r)
	if err != nil {
		return err
	}
	a, err := s.activePITRArchive(r.Context(), srv)
	if err != nil {
		return err
	}
	schedule, days, err := readPITRSchedule(req.Schedule, req.RetentionDays, a.Schedule, a.RetentionDays)
	if err != nil {
		return err
	}
	if err := s.updatePITRSchedule(r.Context(), a.ID, schedule, days); err != nil {
		return httpx.Internal(err)
	}
	s.reloadPITRSchedule(context.WithoutCancel(r.Context()))
	httpx.SetAudit(r, "database.pitr.update", srv.name, map[string]any{
		"archive": a.ID, "schedule": schedule, "retentionDays": days,
		"was": map[string]any{"schedule": a.Schedule, "retentionDays": a.RetentionDays},
	})
	a, err = s.pitrArchiveByID(r.Context(), a.ID)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, a)
	return nil
}

type pitrDisableRequest struct {
	// DeleteArchive also deletes everything the archive holds.
	DeleteArchive bool `json:"deleteArchive"`
}

func (s *Server) handleDBPITRDisable(w http.ResponseWriter, r *http.Request) error {
	var req pitrDisableRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	srv, err := s.pitrRequestServer(r)
	if err != nil {
		return err
	}
	a, err := s.activePITRArchive(r.Context(), srv)
	if err != nil {
		return err
	}
	if srv.spec == nil {
		return pitrRefused(srv.reason)
	}
	if !srv.running {
		return pitrRefused(fmt.Sprintf("%s is not running; start it, then turn archiving off", srv.name))
	}
	httpx.SetAudit(r, "database.pitr.disable", srv.name, map[string]any{
		"archive": a.ID, "location": pitrLocation(a), "deleteArchive": req.DeleteArchive,
	})
	base := auditBaseOf(r)
	return s.startPITRJob(w, r, srv, "disable", "Turn point-in-time recovery off for "+srv.name, pitrBackupTimeout,
		func(ctx context.Context, out jobs.Emitter) error {
			err := s.disablePITR(ctx, srv, a, req.DeleteArchive, out)
			s.recordTransfer(base, "database.pitr.disable.finish", srv.name, map[string]any{"archive": a.ID, "deleteArchive": req.DeleteArchive}, err)
			return err
		})
}

func (s *Server) handleDBPITRBackup(w http.ResponseWriter, r *http.Request) error {
	srv, err := s.pitrRequestServer(r)
	if err != nil {
		return err
	}
	a, err := s.activePITRArchive(r.Context(), srv)
	if err != nil {
		return err
	}
	if !srv.running {
		return pitrRefused(fmt.Sprintf("%s is not running, so no base backup can be taken", srv.name))
	}
	httpx.SetAudit(r, "database.pitr.backup", srv.name, map[string]any{"archive": a.ID})
	base := auditBaseOf(r)
	return s.startPITRJob(w, r, srv, "backup", "Base backup of "+srv.name, pitrBackupTimeout,
		func(ctx context.Context, out jobs.Emitter) error {
			err := s.runPITRBackup(ctx, srv, a, out)
			s.recordTransfer(base, "database.pitr.backup.finish", srv.name, map[string]any{"archive": a.ID}, err)
			return err
		})
}

type pitrRestorePointRequest struct {
	Name string `json:"name"`
}

func (s *Server) handleDBPITRRestorePoint(w http.ResponseWriter, r *http.Request) error {
	var req pitrRestorePointRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	name := strings.TrimSpace(req.Name)
	if err := dbx.ValidRestorePointName(name); err != nil {
		return httpx.BadRequest("%v", err)
	}
	srv, err := s.pitrRequestServer(r)
	if err != nil {
		return err
	}
	a, err := s.activePITRArchive(r.Context(), srv)
	if err != nil {
		return err
	}
	if !srv.running {
		return pitrRefused(fmt.Sprintf("%s is not running, so no moment can be named in its log", srv.name))
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	pool, err := s.modules.dbs.Pool(ctx, srv.conn.ID, srv.conn.Driver, srv.dsn)
	if err != nil {
		return connectFailed(srv.dsn, err)
	}
	lsn, err := dbx.CreateRestorePoint(ctx, pool, name)
	if err != nil && lsn == "" {
		return httpx.BadRequest("the restore point could not be made: %v", withoutSecrets(srv.dsn, err))
	}
	point, insertErr := s.insertPITRRestorePoint(ctx, a.ID, name, lsn, httpx.MustPrincipal(r).Username())
	if insertErr != nil {
		return httpx.Internal(insertErr)
	}
	httpx.SetAudit(r, "database.pitr.restore_point", srv.name, map[string]any{"archive": a.ID, "name": name, "lsn": lsn})
	httpx.JSON(w, http.StatusCreated, point)
	return nil
}

type pitrRestoreRequest struct {
	// ArchiveID is the archive to recover from; absent is the one the server
	// writes into.
	ArchiveID int64              `json:"archiveId"`
	Target    dbx.RecoveryTarget `json:"target"`
	// Name is the copy's container; absent is the server's name and when.
	Name    string `json:"name"`
	Preview bool   `json:"preview"`
}

func (s *Server) handleDBPITRRestore(w http.ResponseWriter, r *http.Request) error {
	var req pitrRestoreRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if err := req.Target.Validate(); err != nil {
		return httpx.BadRequest("%v", err)
	}
	srv, err := s.pitrRequestServer(r)
	if err != nil {
		return err
	}
	if srv.spec == nil {
		return pitrRefused(srv.reason)
	}
	if problem := pitrToolProblem(); problem != "" {
		return pitrRefused(problem)
	}
	ctx, cancel := timeoutCtx(r, 2*time.Minute)
	defer cancel()
	var a *pitrArchive
	if req.ArchiveID > 0 {
		if a, err = s.pitrArchiveByID(ctx, req.ArchiveID); err != nil {
			return err
		}
		if a.Container != srv.name {
			return errNoPITRArchive
		}
	} else if a, err = s.activePITRArchive(ctx, srv); err != nil {
		return err
	}
	if srv.running && a.EndedAt == nil {
		s.freshPITRReading(ctx, srv, a)
	}
	if a.Reading == nil {
		return pitrRefused("the archive has not been listed yet; take a base backup first")
	}
	backup, err := s.pitrBaseFor(ctx, a, a.Reading, req.Target)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = pitrCopyName(srv.name, time.Now())
	}
	if !containerNameRe.MatchString(name) {
		return httpx.BadRequest("a container name may contain letters, digits, dots, dashes and underscores")
	}
	if _, err := s.modules.docker.Inspect(ctx, name); err == nil {
		return httpx.Err(http.StatusConflict, "container_exists", "a container named "+name+" already exists; choose another name")
	}
	if exists, err := s.modules.docker.VolumeExists(ctx, name+"-data"); err != nil || exists {
		return httpx.Err(http.StatusConflict, "volume_exists", "a volume named "+name+"-data already exists; choose another name")
	}
	port, err := s.pitrCopyPort(ctx)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	plan := pitrRestorePlan{
		Container: name, Port: port, Backup: backup.Name, BackupAt: backup.FinishedAt,
		Settings: req.Target.RecoverySettings(),
	}
	if req.Preview {
		httpx.SkipAudit(r)
		httpx.JSON(w, http.StatusOK, plan)
		return nil
	}
	httpx.SetAudit(r, "database.pitr.restore", srv.name, map[string]any{
		"archive": a.ID, "target": req.Target, "backup": backup.Name, "container": name, "port": port,
	})
	base, by := auditBaseOf(r), httpx.MustPrincipal(r).Username()
	return s.startPITRJob(w, r, srv, "restore", "Recover "+srv.name+" into "+name, pitrRestoreTimeout,
		func(ctx context.Context, out jobs.Emitter) error {
			restore, err := s.restorePITR(ctx, srv, a, plan, req.Target, by, out)
			detail := map[string]any{"archive": a.ID, "container": name, "target": req.Target}
			if err == nil {
				detail["connectionId"] = restore.ConnectionID
				summary := "Recovered to the end of the archive"
				if restore.RecoveredTo != nil {
					summary = "Recovered to " + restore.RecoveredTo.Format(time.RFC3339)
					detail["recoveredTo"] = restore.RecoveredTo
				}
				out.Status("%s; %s runs on 127.0.0.1:%d", summary, name, port)
				emitPITRResult(out, pitrResult{
					Archive: a.ID, Backup: plan.Backup, Container: name, ConnectionID: restore.ConnectionID,
					Port: port, RecoveredTo: restore.RecoveredTo, Summary: summary,
				})
			}
			s.recordTransfer(base, "database.pitr.restore.finish", srv.name, detail, err)
			return err
		})
}

// pitrCopyPort is a loopback port for a recovered copy: free now, and kept
// for no stopped container either. The server being recovered from is often
// the stopped one, and its port is the first free one; given to the copy, it
// would point the server's own connection at the copy and leave the server
// unable to start again.
func (s *Server) pitrCopyPort(ctx context.Context) (int, error) {
	bindings, err := s.modules.docker.HostPortBindings(ctx)
	if err != nil {
		return 0, err
	}
	reserved := map[int]bool{}
	for _, b := range bindings {
		if b.Protocol == "" || b.Protocol == "tcp" {
			reserved[b.HostPort] = true
		}
	}
	return portalloc.Select(5432, 1024, reserved, func(port int) error {
		return portalloc.Available("127.0.0.1", "tcp", port)
	})
}

func (s *Server) handleDBPITRSwitch(w http.ResponseWriter, r *http.Request) error {
	rid, err := strconv.ParseInt(chi.URLParam(r, "rid"), 10, 64)
	if err != nil {
		return httpx.BadRequest("invalid restored copy id")
	}
	srv, err := s.pitrRequestServer(r)
	if err != nil {
		return err
	}
	if srv.spec == nil || srv.reason != "" {
		return pitrRefused(srv.reason)
	}
	restore, err := s.pitrRestoreByID(r.Context(), rid)
	if err != nil {
		return err
	}
	if restore.Source != srv.name {
		return httpx.Err(http.StatusNotFound, "not_found", "no such restored copy of "+srv.name)
	}
	if restore.State != "ready" {
		return httpx.Err(http.StatusConflict, "pitr_switched", restore.Container+" was already switched in")
	}
	if _, err := s.modules.docker.Inspect(r.Context(), restore.Container); err != nil {
		return httpx.Err(http.StatusConflict, "copy_missing", restore.Container+" is not on this server any more")
	}
	httpx.SetAudit(r, "database.pitr.switch", srv.name, map[string]any{
		"restore": restore.ID, "copy": restore.Container, "target": restore.Target,
	})
	base := auditBaseOf(r)
	return s.startPITRJob(w, r, srv, "switch", "Switch "+srv.name+" to "+restore.Container, pitrBackupTimeout,
		func(ctx context.Context, out jobs.Emitter) error {
			replaced, err := s.switchPITR(ctx, srv, restore, out)
			if err == nil {
				out.Status("%s now runs the copy; the server it replaced is kept, stopped, as %s", srv.name, replaced)
				emitPITRResult(out, pitrResult{Container: srv.name, Replaced: replaced,
					Summary: srv.name + " runs the recovered copy; the server it replaced is kept as " + replaced})
			}
			s.recordTransfer(base, "database.pitr.switch.finish", srv.name, map[string]any{"restore": restore.ID, "replaced": replaced}, err)
			return err
		})
}

func (s *Server) handleDBPITRArchiveDelete(w http.ResponseWriter, r *http.Request) error {
	aid, err := strconv.ParseInt(chi.URLParam(r, "aid"), 10, 64)
	if err != nil {
		return httpx.BadRequest("invalid archive id")
	}
	srv, err := s.pitrRequestServer(r)
	if err != nil {
		return err
	}
	a, err := s.pitrArchiveByID(r.Context(), aid)
	if err != nil {
		return err
	}
	if a.Container != srv.name {
		return errNoPITRArchive
	}
	if a.EndedAt == nil {
		return httpx.Err(http.StatusConflict, "pitr_on", "the server still writes into this archive; turn point-in-time recovery off to delete it")
	}
	httpx.SetAudit(r, "database.pitr.archive.delete", srv.name, map[string]any{"archive": a.ID, "location": pitrLocation(a)})
	base := auditBaseOf(r)
	return s.startPITRJob(w, r, srv, "delete", "Delete an archive of "+srv.name, pitrBackupTimeout,
		func(ctx context.Context, out jobs.Emitter) error {
			err := s.deletePITRArchiveContents(ctx, srv, a, out)
			s.recordTransfer(base, "database.pitr.archive.delete.finish", srv.name, map[string]any{"archive": a.ID}, err)
			return err
		})
}
