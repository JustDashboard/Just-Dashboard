package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/docker/docker/errdefs"
	"os"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	basestore "github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

type deploymentDependencyObserver struct {
	store   *basestore.Store
	backups *backups.Store
	docker  *dockerx.Client
	now     func() time.Time
	// extensions reads which schema extensions a linked PostgreSQL offers,
	// so preflight can refuse a pgvector schema on a server without it
	// before the first migration fails. Preflight asks through
	// DatabaseExtensions, and only when detection says the schema needs one.
	extensions func(context.Context, int64) ([]string, error)
}

// DatabaseExtensions answers which of the wanted extensions a linked
// connection offers; nil when it cannot be asked.
func (o *deploymentDependencyObserver) DatabaseExtensions(ctx context.Context, resourceID string, wanted []string) ([]string, error) {
	id, err := strconv.ParseInt(resourceID, 10, 64)
	if err != nil || id <= 0 || o.extensions == nil || len(wanted) == 0 {
		return nil, errors.New("database extensions cannot be asked")
	}
	return o.extensions(ctx, id)
}

func (o *deploymentDependencyObserver) withExtensionProbe(probe func(context.Context, int64) ([]string, error)) *deploymentDependencyObserver {
	o.extensions = probe
	return o
}

func newDeploymentDependencyObserver(
	store *basestore.Store,
	backupStore *backups.Store,
	docker *dockerx.Client,
) *deploymentDependencyObserver {
	return &deploymentDependencyObserver{store: store, backups: backupStore, docker: docker, now: time.Now}
}

func (o *deploymentDependencyObserver) ObserveDependencies(
	ctx context.Context,
	dependencies []deploy.PlannedDependency,
) ([]deploy.DependencyObservation, error) {
	result := make([]deploy.DependencyObservation, 0, len(dependencies))
	for _, dependency := range dependencies {
		observed := deploy.DependencyObservation{
			Kind: dependency.Kind, ResourceKind: dependency.ResourceKind, ResourceID: dependency.ResourceID,
		}
		switch dependency.ResourceKind {
		case "backup_job":
			// The list until the job is known to exist; its own page after.
			observed.DeepLink = "/backups"
			id, err := strconv.ParseInt(dependency.ResourceID, 10, 64)
			if err != nil || id <= 0 || o.backups == nil {
				observed.Detail = "backup job id is invalid or Backups is unavailable"
				break
			}
			_, err = o.backups.Get(ctx, id)
			if err != nil {
				observed.Detail = "backup job was not found"
				break
			}
			observed.DeepLink = "/backups/" + strconv.FormatInt(id, 10)
			observed.Available, observed.Status = true, "never run"
			maxAge := 0
			var config struct {
				MaxAgeSeconds int `json:"maxAgeSeconds"`
			}
			_ = json.Unmarshal(dependency.Config, &config)
			maxAge = config.MaxAgeSeconds
			last, lastErr := o.backups.LastRun(ctx, id)
			switch {
			case lastErr == nil:
				observed.Status = string(last.Status)
				observed.Fresh = last.Status == backups.StatusSuccess && last.EndedAt != nil &&
					(maxAge == 0 || o.now().UTC().Sub(last.EndedAt.UTC()) <= time.Duration(maxAge)*time.Second)
			case errors.Is(lastErr, backups.ErrNotFound):
				observed.Fresh = maxAge == 0
			default:
				return nil, lastErr
			}
		case "database_connection":
			// Databases selects a connection by query, not by path.
			observed.DeepLink = "/databases"
			id, err := strconv.ParseInt(dependency.ResourceID, 10, 64)
			if err != nil || id <= 0 || o.store == nil {
				observed.Detail = "database connection id is invalid"
				break
			}
			var name string
			if err := o.store.DB.QueryRowContext(ctx, `SELECT name FROM db_connections WHERE id = ?`, id).Scan(&name); err != nil {
				if err == sql.ErrNoRows {
					observed.Detail = "database connection was not found"
					break
				}
				return nil, err
			}
			observed.DeepLink = "/databases/connection?conn=" + strconv.FormatInt(id, 10)
			observed.Available, observed.Status = true, name
		case "docker_volume":
			observed.DeepLink = "/docker/volumes/" + dependency.ResourceID
			if o.docker == nil {
				observed.Detail = "Docker volume inventory is unavailable"
				break
			}
			if _, err := o.docker.InspectVolume(ctx, dependency.ResourceID); err != nil {
				observed.Detail = "Docker volume was not found"
				observed.Missing = errdefs.IsNotFound(err)
				if !observed.Missing {
					observed.Detail = "Docker volume could not be inspected"
				}
				break
			}
			observed.Available, observed.Status = true, "available"
		case "bind_path":
			observed.DeepLink = "/files?path=" + dependency.ResourceID
			info, err := os.Stat(dependency.ResourceID)
			if err != nil || !info.IsDir() {
				observed.Detail = "persistent bind path was not found"
				break
			}
			observed.Available, observed.Status = true, "available"
		default:
			// Unknown external owners are preserved, but not falsely presented
			// as verified by a feature adapter that does not own them.
			observed.Detail = fmt.Sprintf("no inventory adapter for %s", dependency.ResourceKind)
		}
		result = append(result, observed)
	}
	return result, nil
}
