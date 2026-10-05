package deploy

import (
	"context"
	"database/sql"
	"sort"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/docker/docker/errdefs"
)

type RecordedContainerReader interface {
	Inspect(context.Context, string) (*dockerx.ContainerDetail, error)
}

type NativeBaselineObserver interface {
	ObserveNativeBaseline(context.Context, ReleaseRuntime) RuntimeServices
}

// RecordedRuntimeObserver joins live owning-feature evidence with exact IDs
// captured at adoption. It never changes labels on a production container.
type RecordedRuntimeObserver struct {
	store      *OrchestrationStore
	docker     RuntimeObserver
	containers RecordedContainerReader
	native     NativeBaselineObserver
}

func NewRecordedRuntimeObserver(store *OrchestrationStore, docker RuntimeObserver, containers RecordedContainerReader, native NativeBaselineObserver) *RecordedRuntimeObserver {
	return &RecordedRuntimeObserver{store: store, docker: docker, containers: containers, native: native}
}

func (o *RecordedRuntimeObserver) ListContainersWithLabels(ctx context.Context, labels map[string]string) ([]dockerx.Container, error) {
	if o == nil || o.docker == nil {
		return nil, ErrRuntimeUnavailable
	}
	return o.docker.ListContainersWithLabels(ctx, labels)
}

func (o *RecordedRuntimeObserver) RecordedRuntimeServices(ctx context.Context, environmentID, liveReleaseID, onlyReleaseID int64) RuntimeServices {
	if o == nil {
		return unavailableRecordedRuntime()
	}
	result := observeRuntimeServices(ctx, o.docker, environmentID, liveReleaseID, onlyReleaseID)
	if o == nil || o.store == nil || environmentID <= 0 {
		return result
	}
	// Native authority verification reads installed launchers and hashes the
	// captured executable/source tree. Keep that bounded without giving it the
	// shorter budget used for an ordinary Docker container list.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	query := `SELECT ` + qualifiedRuntimeColumns() + ` FROM deploy_release_runtimes runtime
	 JOIN deploy_releases release ON release.id=runtime.release_id
	 WHERE runtime.environment_id=? AND (json_extract(release.provenance_json,'$.adopted')=1 OR runtime.kind IN ('pm2','systemd') OR json_extract(runtime.metadata_json,'$.adopted')=1)`
	args := []any{environmentID}
	if onlyReleaseID > 0 {
		query += ` AND runtime.release_id=?`
		args = append(args, onlyReleaseID)
	} else {
		query += ` AND runtime.state IN ('live','stopped','ready')`
	}
	query += ` ORDER BY (runtime.release_id=?) DESC, runtime.release_id DESC`
	args = append(args, liveReleaseID)
	rows, err := o.store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return unavailableRecordedRuntime()
	}
	var runtimes []ReleaseRuntime
	for rows.Next() {
		runtime, err := scanReleaseRuntime(rows)
		if err != nil {
			rows.Close()
			return unavailableRecordedRuntime()
		}
		runtimes = append(runtimes, *runtime)
	}
	readErr := rows.Err()
	rows.Close()
	if readErr != nil {
		return unavailableRecordedRuntime()
	}
	seen := map[string]bool{}
	seenNative := map[string]bool{}
	for _, service := range result.Services {
		seen["docker:"+service.ContainerID] = true
	}
	for _, runtime := range runtimes {
		if runtime.Kind == "pm2" || runtime.Kind == "systemd" {
			identity := runtime.Kind + "\x00" + runtime.RuntimeID
			if seenNative[identity] {
				continue
			}
			if o.native == nil {
				return unavailableRecordedRuntime()
			}
			observed := o.native.ObserveNativeBaseline(ctx, runtime)
			if observed.Status != "available" {
				return unavailableRecordedRuntime()
			}
			seenNative[identity] = true
			for _, service := range observed.Services {
				service.ReleaseID = runtime.ReleaseID
				service.LiveRelease = runtime.ReleaseID == liveReleaseID
				result.Services = append(result.Services, service)
			}
			result.Status = "available"
			result.Reason = ""
			continue
		}
		if o.containers == nil {
			return unavailableRecordedRuntime()
		}
		for _, id := range runtimeContainerIDs(runtime) {
			if seen["docker:"+id] {
				continue
			}
			detail, err := o.containers.Inspect(ctx, id)
			if errdefs.IsNotFound(err) {
				continue
			}
			if err != nil || detail == nil || detail.ID != id {
				return unavailableRecordedRuntime()
			}
			seen["docker:"+id] = true
			health := detail.Health
			if health == "" {
				health = "unavailable"
			}
			result.Services = append(result.Services, RuntimeService{ContainerID: id, Name: detail.Name, ReleaseID: runtime.ReleaseID, LiveRelease: runtime.ReleaseID == liveReleaseID,
				State: detail.State, Health: health, ImageID: detail.ImageID, Image: detail.Image, Stack: detail.ComposeStack, Service: detail.ComposeSvc, StartedAt: detail.StartedAt,
				Manager: "docker", ResourceID: id, LogSource: "docker:" + id})
			result.Status = "available"
			result.Reason = ""
		}
	}
	result.ObservedAt = time.Now().UTC()
	sort.Slice(result.Services, func(i, j int) bool {
		if result.Services[i].Name != result.Services[j].Name {
			return result.Services[i].Name < result.Services[j].Name
		}
		return result.Services[i].ResourceID < result.Services[j].ResourceID
	})
	return result
}

func qualifiedRuntimeColumns() string {
	return `runtime.release_id,runtime.environment_id,runtime.kind,runtime.runtime_id,runtime.name,runtime.working_directory,
	 runtime.host,runtime.port,runtime.state,runtime.metadata_json,runtime.created_at,runtime.updated_at`
}

func unavailableRecordedRuntime() RuntimeServices {
	return RuntimeServices{Status: "unavailable", Reason: "The adopted runtime could not be verified with its current manager.", Services: []RuntimeService{}}
}

// RuntimeByIdentity returns only a registered native baseline, allowing
// preflight to check its live listener without trusting a request-supplied PID.
func (s *OrchestrationStore) RuntimeByIdentity(ctx context.Context, kind, id string) (*ReleaseRuntime, error) {
	if kind != "pm2" && kind != "systemd" {
		return nil, ErrArtifactMissing
	}
	runtime, err := scanReleaseRuntime(s.db.QueryRowContext(ctx, `SELECT `+qualifiedRuntimeColumns()+` FROM deploy_release_runtimes runtime
	 JOIN deploy_releases release ON release.id=runtime.release_id
	 WHERE runtime.kind=? AND runtime.runtime_id=?
	 ORDER BY runtime.release_id DESC LIMIT 1`, kind, id))
	if err == sql.ErrNoRows {
		return nil, ErrArtifactMissing
	}
	return runtime, err
}
