package api

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/docker/docker/errdefs"
	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/ssh"
)

// What a destination holds, and taking it on: the catalogue of the archives
// another server (or this one, before a rebuild) wrote to a destination, the
// import that adopts them as imported runs, and the key import that lets this
// server open the ones that were sealed. Each is an administrator's act — an
// archive is the whole of what it covers, and a destination's keys are being
// used — and each is audited.

// backupDestinationRequest names a destination: one of this server's jobs'
// (its keys stay sealed in the job), or one entered with its keys, which are
// used for the request and kept only with the imported job an import makes.
type backupDestinationRequest struct {
	JobID      int64                  `json:"jobId,omitempty"`
	TargetKind backups.TargetKind     `json:"targetKind,omitempty"`
	Target     backups.TargetConfig   `json:"target"`
	Secrets    *backups.TargetSecrets `json:"secrets,omitempty"`
}

type backupImportRequest struct {
	backupDestinationRequest
	// Keys are the archives to adopt; or OriginID and OriginJobID name one
	// job of one install, every archive of which is adopted.
	Keys        []string `json:"keys,omitempty"`
	OriginID    string   `json:"originId,omitempty"`
	OriginJobID int64    `json:"originJobId,omitempty"`
	// CopyTo keeps a copy of each archive in a directory on this server.
	CopyTo string `json:"copyTo,omitempty"`
}

// noteHostKey stands in for a job's pinner when a destination was entered:
// the SFTP client records the presented key on the destination itself, the
// catalogue answers it, and only an import keeps it, with the imported job.
func noteHostKey(context.Context, ssh.PublicKey) error { return nil }

func (s *Server) backupDestination(ctx context.Context, req backupDestinationRequest) (backups.Destination, error) {
	if req.JobID > 0 {
		job, err := s.modules.backupStore.Get(ctx, req.JobID)
		if err != nil {
			return backups.Destination{}, mapBackupError(err)
		}
		secrets, err := s.modules.backupStore.Secrets(ctx, req.JobID)
		if err != nil {
			return backups.Destination{}, httpx.Internal(err)
		}
		return backups.Destination{Kind: job.TargetKind, Target: job.Target, Secrets: secrets, Pin: s.modules.backupStore.Pinner(job.ID)}, nil
	}
	if !req.TargetKind.Valid() {
		return backups.Destination{}, httpx.BadRequest("name a destination: a job's (jobId) or a targetKind with its target")
	}
	target := req.Target
	if req.TargetKind == backups.TargetLocal {
		// files.Resolve bounds which directories on this server a catalogue
		// may list and an import may read.
		resolved, err := s.modules.files.Resolve(target.Path)
		if err != nil {
			return backups.Destination{}, httpx.BadRequest("%v", err)
		}
		target.Path = resolved
	}
	secrets := req.Secrets
	if secrets == nil {
		secrets = &backups.TargetSecrets{}
	}
	return backups.Destination{Kind: req.TargetKind, Target: target, Secrets: secrets, Pin: noteHostKey}, nil
}

func backupDestinationAudit(req backupDestinationRequest, dest backups.Destination) map[string]any {
	detail := map[string]any{"target": dest.Kind}
	if req.JobID > 0 {
		detail["jobId"] = req.JobID
	}
	return detail
}

// handleBackupCatalogue lists the archives a destination holds that this
// server did not write, grouped by the server and job that took them.
func (s *Server) handleBackupCatalogue(w http.ResponseWriter, r *http.Request) error {
	var req backupDestinationRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	dest, err := s.backupDestination(r.Context(), req)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 3*time.Minute)
	defer cancel()
	catalogue, err := s.modules.backupRunner.Catalogue(ctx, dest)
	detail := backupDestinationAudit(req, dest)
	if err != nil {
		detail["error"] = err.Error()
		httpx.SetAudit(r, "backup.catalogue", string(dest.Kind), detail)
		return httpx.Err(http.StatusBadGateway, "destination_unreadable", err.Error())
	}
	detail["archives"], detail["ours"] = catalogue.Archives, catalogue.Ours
	httpx.SetAudit(r, "backup.catalogue", catalogue.Destination, detail)
	httpx.JSON(w, http.StatusOK, catalogue)
	return nil
}

// handleBackupImport adopts archives from a destination as imported runs.
// Each is proved against its manifest's digest before it is recorded.
func (s *Server) handleBackupImport(w http.ResponseWriter, r *http.Request) error {
	var req backupImportRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	dest, err := s.backupDestination(r.Context(), req.backupDestinationRequest)
	if err != nil {
		return err
	}
	copyTo := ""
	if strings.TrimSpace(req.CopyTo) != "" {
		resolved, err := s.modules.files.Resolve(req.CopyTo)
		if err != nil {
			return httpx.BadRequest("%v", err)
		}
		copyTo = resolved
	}
	ctx, cancel := timeoutCtx(r, 6*time.Hour)
	defer cancel()
	result, err := s.modules.backupRunner.Import(ctx, backups.ImportRequest{
		Destination: dest, Keys: req.Keys, OriginID: req.OriginID, OriginJobID: req.OriginJobID, CopyTo: copyTo,
	})
	detail := backupDestinationAudit(req.backupDestinationRequest, dest)
	detail["keys"], detail["originId"], detail["originJobId"] = len(req.Keys), req.OriginID, req.OriginJobID
	if copyTo != "" {
		detail["copyTo"] = copyTo
	}
	if err != nil {
		detail["error"] = err.Error()
		httpx.SetAudit(r, "backup.import", string(dest.Kind), detail)
		return mapBackupError(err)
	}
	runs, jobs := make([]int64, 0, len(result.Runs)), make([]int64, 0, len(result.Jobs))
	for _, run := range result.Runs {
		runs = append(runs, run.ID)
	}
	for _, job := range result.Jobs {
		jobs = append(jobs, job.ID)
	}
	detail["runs"], detail["jobs"], detail["skipped"] = runs, jobs, len(result.Skipped)
	httpx.SetAudit(r, "backup.import", result.Destination, detail)
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

type backupKeyImportRequest struct {
	// Key is the exported key file, or its AGE-SECRET-KEY-1 line.
	Key string `json:"key"`
}

// handleBackupKeyImport takes a key exported from another server (or this one
// before a rebuild), so the archives sealed to it open here. The audit entry
// names the key by its public half; the secret half is sealed on arrival and
// never written anywhere else.
func (s *Server) handleBackupKeyImport(w http.ResponseWriter, r *http.Request) error {
	var req backupKeyImportRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	result, err := s.modules.backupStore.ImportArchiveKey(r.Context(), req.Key)
	if err != nil {
		httpx.SetAudit(r, "backup.key.import", "", map[string]any{"error": err.Error()})
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "backup.key.import", result.Recipient, map[string]any{"became": result.Became})
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

// handleBackupRestorePlan says where a restore in place of a run would write
// on this host and what it would have to make first — a volume, a directory —
// from the manifest alone, for the restore dialog to show before it confirms.
func (s *Server) handleBackupRestorePlan(w http.ResponseWriter, r *http.Request) error {
	runID, err := strconv.ParseInt(chi.URLParam(r, "runID"), 10, 64)
	if err != nil || runID <= 0 {
		return httpx.BadRequest("invalid run id")
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	targets, err := s.modules.backupRunner.PlanRestoreInPlace(ctx, runID, backups.RestoreOptions{Paths: r.URL.Query()["path"]})
	if err != nil {
		return mapBackupError(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"targets": targets})
	return nil
}

// backupVolumeProvisioner is the Docker owner's side of a volume a job
// archives: which volume a source directory is, and on a restore the
// volume's directory on this host, made with its labels when it is absent.
type backupVolumeProvisioner struct {
	docker *dockerx.Client
}

func (p *backupVolumeProvisioner) VolumeAt(ctx context.Context, path string) (backups.LocalVolume, bool, error) {
	clean := filepath.Clean(path)
	dir, data := filepath.Split(clean)
	name := filepath.Base(filepath.Clean(dir))
	if data != "_data" || filepath.Base(filepath.Dir(filepath.Clean(dir))) != "volumes" {
		return backups.LocalVolume{}, false, nil
	}
	volume, err := p.docker.InspectVolume(ctx, name)
	if errdefs.IsNotFound(err) {
		return backups.LocalVolume{}, false, nil
	}
	if err != nil {
		return backups.LocalVolume{}, false, err
	}
	if volume.Driver != "local" || filepath.Clean(volume.Mountpoint) != clean {
		return backups.LocalVolume{}, false, nil
	}
	return backups.LocalVolume{Name: volume.Name, Mountpoint: volume.Mountpoint, Labels: volume.Labels}, true, nil
}

func (p *backupVolumeProvisioner) VolumeMountpoint(ctx context.Context, name string) (string, bool, error) {
	volume, err := p.docker.InspectVolume(ctx, name)
	if errdefs.IsNotFound(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if volume.Driver != "local" || !filepath.IsAbs(volume.Mountpoint) {
		return "", false, errors.New("the volume is not a local one, so its data is not a directory here")
	}
	return volume.Mountpoint, true, nil
}

func (p *backupVolumeProvisioner) EnsureVolume(ctx context.Context, name string, labels map[string]string) (string, bool, error) {
	mountpoint, exists, err := p.VolumeMountpoint(ctx, name)
	if err != nil || exists {
		return mountpoint, false, err
	}
	created, err := p.docker.CreateVolume(ctx, dockerx.VolumeSpec{Name: name, Labels: labels})
	if err != nil {
		return "", false, err
	}
	return created.Mountpoint, true, nil
}
