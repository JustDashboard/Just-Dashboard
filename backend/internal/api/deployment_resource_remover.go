package api

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	basestore "github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

type deploymentResourceRemover struct {
	docker   *dockerx.Client
	proxy    *proxysvc.Service
	backups  *backups.Store
	dbs      *dbx.Manager
	store    *basestore.Store
	files    *files.Service
	networks *deploymentDatabaseNetworks
}

func newDeploymentResourceRemover(s *Server) *deploymentResourceRemover {
	return &deploymentResourceRemover{
		docker: s.modules.docker, proxy: s.modules.proxy, backups: s.modules.backupStore,
		dbs: s.modules.dbs, store: s.Store, files: files.New(s.Cfg.DeployRoots), networks: s.modules.deployDatabases,
	}
}

func (r *deploymentResourceRemover) RemoveManagedResource(ctx context.Context, target deploy.RemovalTarget) error {
	switch target.Kind {
	case "deployment_database_network":
		if r.networks == nil {
			return deploy.Unavailable(errors.New("managed database networks are unavailable"))
		}
		return r.networks.RemoveNetworkByID(ctx, target.ResourceID)
	case "docker_container":
		if r.docker == nil {
			return deploy.Unavailable(errors.New("Docker is unavailable"))
		}
		return r.docker.RemoveContainer(ctx, target.ResourceID, false, false)
	case "compose_stack":
		if r.docker == nil {
			return deploy.Unavailable(errors.New("Docker is unavailable"))
		}
		if target.WorkingDirectory == "" {
			return deploy.Unavailable(errors.New("Compose working directory is unavailable"))
		}
		_, err := r.docker.RunCompose(ctx, target.WorkingDirectory, dockerx.ComposeDown, "")
		return err
	case "proxy_site":
		if r.proxy == nil {
			return deploy.Unavailable(errors.New("Proxy is unavailable"))
		}
		return r.proxy.DeleteSite(ctx, target.ResourceID)
	case "docker_volume":
		if r.docker == nil {
			return deploy.Unavailable(errors.New("Docker is unavailable"))
		}
		return r.docker.RemoveVolume(ctx, target.ResourceID, false)
	case "bind_path":
		if r.files == nil {
			return deploy.Unavailable(errors.New("deployment path guard is unavailable"))
		}
		return r.files.Delete(target.ResourceID, true)
	case "docker_image":
		if r.docker == nil {
			return deploy.Unavailable(errors.New("Docker is unavailable"))
		}
		_, err := r.docker.RemoveImage(ctx, target.ResourceID, false, false)
		return err
	case "backup_job":
		if r.backups == nil {
			return deploy.Unavailable(errors.New("Backups is unavailable"))
		}
		id, err := strconv.ParseInt(target.ResourceID, 10, 64)
		if err != nil || id <= 0 {
			return errors.New("backup job id is invalid")
		}
		return r.backups.Delete(ctx, id)
	case "database_connection":
		if r.store == nil {
			return deploy.Unavailable(errors.New("Databases is unavailable"))
		}
		id, err := strconv.ParseInt(target.ResourceID, 10, 64)
		if err != nil || id <= 0 {
			return errors.New("database connection id is invalid")
		}
		result, err := r.store.DB.ExecContext(ctx, `DELETE FROM db_connections WHERE id=? AND NOT EXISTS (SELECT 1 FROM deploy_database_bindings WHERE connection_id=?)`, id, id)
		if err != nil {
			return err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return errors.New("database connection is missing or still linked to a managed deployment network")
		}
		if r.dbs != nil {
			r.dbs.Close(id)
		}
		return nil
	default:
		return deploy.Unavailable(fmt.Errorf("resource owner for %s is unavailable", target.Kind))
	}
}
