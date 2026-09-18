package api

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// deploymentBackupGate is an adapter, not a second backup implementation.
// Backups remains responsible for jobs, artifacts, retention, credentials and
// execution; Deployments receives only the small evidence record it may store
// safely in a run transcript.
type deploymentBackupGate struct {
	store           *backups.Store
	runner          *backups.Runner
	now             func() time.Time
	volumePath      func(context.Context, string) (string, error)
	databaseSources func(context.Context, int64) ([]string, error)
}

func (g *deploymentBackupGate) WithDatabaseSources(resolve func(context.Context, int64) ([]string, error)) *deploymentBackupGate {
	g.databaseSources = resolve
	return g
}

func newDeploymentBackupGate(store *backups.Store, runner *backups.Runner, docker *dockerx.Client) *deploymentBackupGate {
	gate := &deploymentBackupGate{store: store, runner: runner, now: time.Now}
	if docker != nil {
		gate.volumePath = func(ctx context.Context, name string) (string, error) {
			volume, err := docker.InspectVolume(ctx, name)
			if err != nil {
				return "", err
			}
			if volume.Driver != "local" || !filepath.IsAbs(volume.Mountpoint) {
				return "", errors.New("volume requires a backup adapter for its storage driver")
			}
			return volume.Mountpoint, nil
		}
	}
	return gate
}

func (g *deploymentBackupGate) Evaluate(ctx context.Context, request deploy.BackupGateRequest) (deploy.BackupGateEvidence, error) {
	evidence := deploy.BackupGateEvidence{JobID: request.JobID, Status: "unavailable"}
	if g == nil || g.store == nil || g.runner == nil {
		return evidence, errors.New("Backups feature is unavailable")
	}
	job, err := g.store.Get(ctx, request.JobID)
	if err != nil {
		evidence.Detail = "backup job was not found"
		return evidence, err
	}
	if err := g.store.ValidatePaths(job); err != nil {
		evidence.Detail = "backup job paths are no longer accessible"
		return evidence, err
	}
	sources := append([]string(nil), request.PersistentSources...)
	var nativeDumps []int64
	for _, id := range request.DatabaseConnections {
		// A job that dumps this connection natively is the coverage a
		// database deserves: a transaction boundary the engine chose. Only a
		// job without that dump falls back to covering the engine's files.
		if slices.Contains(job.DatabaseDumps, id) {
			nativeDumps = append(nativeDumps, id)
			continue
		}
		if g.databaseSources == nil {
			evidence.Detail = "linked database backup coverage is unavailable"
			return evidence, errors.New(evidence.Detail)
		}
		paths, err := g.databaseSources(ctx, id)
		if err != nil || len(paths) == 0 {
			evidence.Detail = "linked database persistent data could not be resolved; add the connection to the backup job's database dumps"
			return evidence, errors.New(evidence.Detail)
		}
		sources = append(sources, paths...)
	}
	evidence.DatabaseDumps = nativeDumps
	persistent := make([]string, 0, len(sources))
	for _, source := range sources {
		if !filepath.IsAbs(source) {
			if g.volumePath == nil || source == "" || strings.ContainsAny(source, "/\\:$\x00") {
				evidence.Detail = "persistent volume identity could not be resolved"
				return evidence, errors.New(evidence.Detail)
			}
			source, err = g.volumePath(ctx, source)
			if err != nil {
				evidence.Detail = "persistent volume could not be inspected"
				return evidence, err
			}
		}
		source, err = g.store.ResolveSource(source)
		if err != nil {
			evidence.Detail = "persistent path could not be resolved within the allowed roots"
			return evidence, err
		}
		persistent = append(persistent, source)
		if !backupCoversPath(job.Sources, source) || len(job.Excludes) > 0 {
			evidence.Detail = "backup job does not cover every persistent path"
			return evidence, fmt.Errorf("backup job %d does not cover persistent path %s", request.JobID, source)
		}
	}
	if !request.RequiredBeforeDeploy && request.MaxAgeSeconds == 0 && !request.RequireRestoreTest {
		evidence.Status, evidence.Fresh = "not_required", true
		return evidence, nil
	}
	var run *backups.Run
	if request.RequiredBeforeDeploy {
		run, err = g.runner.Execute(ctx, request.JobID, "deployment")
	} else {
		run, err = g.store.LastRun(ctx, request.JobID)
	}
	if err != nil {
		evidence.Detail = "backup run could not be obtained"
		return evidence, err
	}
	evidence.RunID, evidence.Status = run.ID, string(run.Status)
	evidence.StartedAt, evidence.EndedAt = run.StartedAt, run.EndedAt
	if run.Status != backups.StatusSuccess || run.EndedAt == nil {
		evidence.Detail = "backup run did not succeed"
		return evidence, errors.New("backup run did not succeed")
	}
	for _, source := range persistent {
		if !run.Manifest.Covers(source) {
			evidence.Detail = "backup archive has no complete manifest covering every persistent source; run a new unfiltered backup"
			return evidence, errors.New(evidence.Detail)
		}
	}
	if err := g.runner.VerifyCoverage(ctx, run.ID, persistent); err != nil {
		evidence.Detail = "backup artifact integrity or persistent-source coverage could not be verified"
		return evidence, err
	}
	for _, id := range nativeDumps {
		if !run.Manifest.CoversDatabase(id) {
			evidence.Detail = "backup archive has no native dump of a linked database; run a new backup with the dump configured"
			return evidence, errors.New(evidence.Detail)
		}
	}
	if len(nativeDumps) > 0 {
		if err := g.runner.VerifyDatabaseCoverage(ctx, run.ID, nativeDumps); err != nil {
			evidence.Detail = "backup archive does not hold the recorded database dumps"
			return evidence, err
		}
	}
	if run.Manifest != nil {
		evidence.ManifestDigest = run.Manifest.ArtifactDigest
	}
	evidence.Fresh = request.MaxAgeSeconds == 0 ||
		g.now().UTC().Sub(run.EndedAt.UTC()) <= time.Duration(request.MaxAgeSeconds)*time.Second
	if !evidence.Fresh {
		evidence.Detail = "latest successful backup is older than the policy maximum"
	}
	verification, err := g.store.RestoreVerification(ctx, run.ID)
	if err != nil {
		return evidence, err
	}
	if request.RequireRestoreTest && job.Recovery != nil && !verification.Matches(run, job.Recovery) {
		verification, err = g.runner.VerifyRestore(ctx, run.ID)
		if verification != nil {
			evidence.RestoreVerificationID = verification.ID
			evidence.RestoreApplicationImage = verification.ApplicationImage
			evidence.RestoreSchemaVersion = verification.SchemaVersion
		}
		if err != nil {
			evidence.Detail = "isolated application restore verification did not pass"
			return evidence, err
		}
	}
	evidence.RestoreTested = verification.Matches(run, job.Recovery)
	if verification != nil {
		evidence.RestoreVerificationID = verification.ID
		evidence.RestoreApplicationImage = verification.ApplicationImage
		evidence.RestoreSchemaVersion = verification.SchemaVersion
	}
	return evidence, nil
}

func backupCoversPath(sources []string, wanted string) bool {
	wanted = filepath.Clean(wanted)
	for _, source := range sources {
		source = filepath.Clean(strings.TrimSpace(source))
		if source == wanted {
			return true
		}
		relative, err := filepath.Rel(source, wanted)
		if err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
