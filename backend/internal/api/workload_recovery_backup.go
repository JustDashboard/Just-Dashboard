package api

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

const recoveredBackupMaxAgeSeconds = 24 * 60 * 60

// Reuse an existing, verified backup policy. Import never creates or executes a
// job; its normal deployment gate verifies the same coverage again before stop.
func (s *Server) recoverExistingBackupPolicy(ctx context.Context, recovered *deploy.RecoveredWorkload) {
	if recovered == nil || recovered.Adoption == nil || s.modules.backupStore == nil || s.modules.backupRunner == nil {
		return
	}
	sources := deploy.PersistentStorageSources(&recovered.Source, recovered.Configuration.Runtime)
	if len(sources) == 0 {
		return
	}
	paths := make([]string, 0, len(sources))
	for _, source := range sources {
		if filepath.IsAbs(source) {
			paths = append(paths, source)
			continue
		}
		if s.modules.docker == nil {
			return
		}
		volume, err := s.modules.docker.InspectVolume(ctx, source)
		if err != nil || volume.Driver != "local" || !filepath.IsAbs(volume.Mountpoint) {
			return
		}
		paths = append(paths, volume.Mountpoint)
	}
	jobs, err := s.modules.backupStore.List(ctx)
	if err != nil {
		return
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].ID < jobs[j].ID })
	gate := newDeploymentBackupGate(s.modules.backupStore, s.modules.backupRunner, s.modules.docker)
	verification, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for _, job := range jobs {
		if verification.Err() != nil {
			return
		}
		if !recoverableBackupJob(job, paths, time.Now()) {
			continue
		}
		evidence, verifyErr := gate.Evaluate(verification, deploy.BackupGateRequest{
			JobID: job.ID, PersistentSources: sources, MaxAgeSeconds: recoveredBackupMaxAgeSeconds,
		})
		if verifyErr != nil || !evidence.Fresh || evidence.ManifestDigest == "" {
			continue
		}
		config, _ := json.Marshal(map[string]any{
			"maxAgeSeconds": recoveredBackupMaxAgeSeconds,
		})
		recovered.Configuration.Dependencies = append(recovered.Configuration.Dependencies, deploy.PlannedDependency{
			Kind: "backup", Ownership: deploy.OwnershipLinked, ResourceKind: "backup_job", ResourceID: strconv.FormatInt(job.ID, 10), Config: config,
		})
		return
	}
}

func recoverableBackupJob(job *backups.Job, paths []string, now time.Time) bool {
	if !job.Enabled || len(job.Excludes) != 0 || job.LastRun == nil || job.LastRun.Status != backups.StatusSuccess || job.LastRun.EndedAt == nil || job.LastRun.SizeBytes > 256<<20 {
		return false
	}
	age := now.Sub(*job.LastRun.EndedAt)
	if age < 0 || age > recoveredBackupMaxAgeSeconds*time.Second {
		return false
	}
	for _, path := range paths {
		if !backupCoversPath(job.Sources, path) {
			return false
		}
	}
	return len(paths) > 0
}
