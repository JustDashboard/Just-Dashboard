package deploy

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"time"
)

type runtimeStorageMount struct {
	RuntimeMount
	Service     string
	ContainerID string
}

type runtimeStorageObserver interface {
	RecordedRuntimeMounts(context.Context, RuntimeServices, int64) ([]runtimeStorageMount, error)
}

// Compose owns per-service mounts rather than the plan's aggregate overrides.
// Read the exact live release containers so external volume names, anonymous
// volumes and read-only binds describe what is actually attached.
func (o *RecordedRuntimeObserver) RecordedRuntimeMounts(ctx context.Context, runtime RuntimeServices, releaseID int64) ([]runtimeStorageMount, error) {
	if o == nil || o.containers == nil || runtime.Status != statusAvailable || releaseID <= 0 {
		return nil, ErrRuntimeUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	result := []runtimeStorageMount{}
	seen := map[string]bool{}
	containers := 0
	for _, service := range runtime.Services {
		if service.ReleaseID != releaseID || !service.LiveRelease || service.ContainerID == "" || (service.Manager != "" && service.Manager != "docker") {
			continue
		}
		containers++
		if containers > 128 {
			return nil, ErrRuntimeUnavailable
		}
		detail, err := o.containers.Inspect(ctx, service.ContainerID)
		if err != nil || detail == nil || detail.ID != service.ContainerID {
			return nil, ErrRuntimeUnavailable
		}
		for _, mount := range detail.Mounts {
			source := mount.Source
			switch mount.Type {
			case "volume":
				source = mount.Name
				if source == "" {
					return nil, ErrRuntimeUnavailable
				}
			case "bind":
				if !filepath.IsAbs(source) {
					return nil, ErrRuntimeUnavailable
				}
			case "tmpfs":
				continue
			default:
				return nil, ErrRuntimeUnavailable
			}
			if mount.Destination == "" || !filepath.IsAbs(mount.Destination) {
				return nil, ErrRuntimeUnavailable
			}
			key := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%t", service.ContainerID, service.Service, source, mount.Destination, mount.RW)
			if seen[key] {
				continue
			}
			seen[key] = true
			result = append(result, runtimeStorageMount{
				RuntimeMount: RuntimeMount{Source: source, Target: mount.Destination, ReadOnly: !mount.RW, Ownership: OwnershipObserved},
				Service:      service.Service, ContainerID: service.ContainerID,
			})
		}
	}
	if containers == 0 {
		return nil, ErrRuntimeUnavailable
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i], result[j]
		if a.Service != b.Service {
			return a.Service < b.Service
		}
		if a.Target != b.Target {
			return a.Target < b.Target
		}
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		return a.ContainerID < b.ContainerID
	})
	return result, nil
}

func operationalStorageSnapshot(ctx context.Context, owner RuntimeObserver, snapshot runtimeReleaseSnapshot, runtime RuntimeServices, releaseID int64) (runtimeReleaseSnapshot, []runtimeStorageMount, error) {
	if snapshot.Compose == nil {
		return snapshot, nil, nil
	}
	reader, ok := owner.(runtimeStorageObserver)
	if !ok {
		return snapshot, nil, ErrRuntimeUnavailable
	}
	mounts, err := reader.RecordedRuntimeMounts(ctx, runtime, releaseID)
	if err != nil {
		return snapshot, nil, err
	}
	snapshot.Plan.Mounts = make([]RuntimeMount, 0, len(mounts))
	for _, mount := range mounts {
		snapshot.Plan.Mounts = append(snapshot.Plan.Mounts, mount.RuntimeMount)
	}
	return snapshot, mounts, nil
}

func recordedStorageSummary(snapshot runtimeReleaseSnapshot, observed dependencyObservations, mounts []runtimeStorageMount) StorageSummary {
	if mounts == nil {
		return storageSummary(snapshot, observed)
	}
	result := StorageSummary{Status: statusAvailable, Mounts: []StorageMount{}}
	for _, mount := range mounts {
		one := runtimeReleaseSnapshot{Plan: RuntimePlanConfig{Mounts: []RuntimeMount{mount.RuntimeMount}}}
		part := storageSummary(one, observed)
		if part.Status != statusAvailable {
			return part
		}
		row := part.Mounts[0]
		row.Service, row.ContainerID = mount.Service, mount.ContainerID
		result.Mounts = append(result.Mounts, row)
	}
	return result
}
