package api

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
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
	store    *basestore.Store
	files    *files.Service
	networks *deploymentDatabaseNetworks
	// forget removes a connection's row with everything kept about it, on
	// behalf of actor: the same forgetting the Databases routes do, so a
	// connection removed with its deployment is not connected again by the
	// next sync.
	forget func(ctx context.Context, id int64, actor string) (forgottenConnection, error)
	actor  string
	// ignored are the found servers that forgetting put on discovery's ignore
	// list, for the audit entry of the request that removed them.
	ignored []string
}

func newDeploymentResourceRemover(s *Server, actor string) *deploymentResourceRemover {
	return &deploymentResourceRemover{
		docker: s.modules.docker, proxy: s.modules.proxy, backups: s.modules.backupStore,
		store: s.Store, files: files.New(s.Cfg.DeployRoots), networks: s.modules.deployDatabases,
		forget: s.forgetConnection, actor: actor,
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
		forgotten, err := r.forget(ctx, id, r.actor)
		if err != nil {
			return err
		}
		if !forgotten.removed {
			return errors.New("database connection is missing or still linked to a managed deployment network")
		}
		if forgotten.ignored {
			r.ignored = append(r.ignored, forgotten.origin)
		}
		return nil
	default:
		return deploy.Unavailable(fmt.Errorf("resource owner for %s is unavailable", target.Kind))
	}
}
