package deploy

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

type storageContainerReader struct {
	details map[string]*dockerx.ContainerDetail
	read    []string
}

func (r *storageContainerReader) Inspect(_ context.Context, id string) (*dockerx.ContainerDetail, error) {
	r.read = append(r.read, id)
	if detail, ok := r.details[id]; ok {
		return detail, nil
	}
	return nil, errors.New("container unavailable")
}

func TestRecordedComposeStorageUsesExactLiveContainersAndActualVolumeNames(t *testing.T) {
	reader := &storageContainerReader{details: map[string]*dockerx.ContainerDetail{
		"web": {Container: dockerx.Container{ID: "web"}, Mounts: []dockerx.MountPoint{
			{Type: "volume", Name: "external-shared-data", Source: "/var/lib/docker/volumes/external-shared-data/_data", Destination: "/data", RW: true},
			{Type: "bind", Source: "/srv/app/public", Destination: "/public", RW: false},
			{Type: "tmpfs", Destination: "/tmp", RW: true},
		}},
		"worker": {Container: dockerx.Container{ID: "worker"}, Mounts: []dockerx.MountPoint{
			{Type: "volume", Name: "external-shared-data", Destination: "/data", RW: true},
		}},
	}}
	owner := NewRecordedRuntimeObserver(nil, nil, reader, nil)
	runtime := RuntimeServices{Status: statusAvailable, Services: []RuntimeService{
		{ContainerID: "web", Service: "web", Manager: "docker", ReleaseID: 9, LiveRelease: true},
		{ContainerID: "worker", Service: "worker", Manager: "docker", ReleaseID: 9, LiveRelease: true},
		{ContainerID: "old", Service: "web", Manager: "docker", ReleaseID: 8},
		{ContainerID: "foreign", Service: "web", Manager: "docker", ReleaseID: 8, LiveRelease: true},
	}}
	original := runtimeReleaseSnapshot{Compose: &ResolvedComposeSnapshot{}, Plan: RuntimePlanConfig{Mounts: []RuntimeMount{{Source: "pending-only", Target: "/later"}}}}
	snapshot, mounts, err := operationalStorageSnapshot(t.Context(), owner, original, runtime, 9)
	if err != nil || len(mounts) != 3 || !reflect.DeepEqual(reader.read, []string{"web", "worker"}) {
		t.Fatalf("unexpected verified storage inventory: mounts=%+v read=%v error=%v", mounts, reader.read, err)
	}
	if len(original.Plan.Mounts) != 1 || original.Plan.Mounts[0].Source != "pending-only" {
		t.Fatal("operational storage mutated the frozen plan")
	}
	dependencies := &countingDependencyObserver{byKey: map[string]DependencyObservation{
		dependencyKey("docker_volume", "external-shared-data"): {Available: true},
		dependencyKey("bind_path", "/srv/app/public"):          {Available: true},
	}}
	observed := observeDependencies(t.Context(), dependencies, snapshot)
	if len(dependencies.requested) != 1 || len(dependencies.requested[0]) != 2 {
		t.Fatal("storage inventory was not deduplicated into one owning-feature read")
	}
	result := recordedStorageSummary(snapshot, observed, mounts)
	if result.Status != statusAvailable || len(result.Mounts) != 3 {
		t.Fatalf("Compose storage disappeared: %+v", result)
	}
	for _, mount := range result.Mounts {
		if mount.Status != "present" || mount.Ownership != OwnershipObserved || mount.Service == "" || mount.ContainerID == "" {
			t.Fatalf("storage row lost read-only runtime provenance: %+v", mount)
		}
	}
	if result.Mounts[0].Source != "external-shared-data" || result.Mounts[0].Kind != "volume" ||
		result.Mounts[1].Source != "/srv/app/public" || !result.Mounts[1].ReadOnly || result.Mounts[1].Kind != "bind" || result.Mounts[2].Service != "worker" {
		t.Fatalf("actual mounts lost service, volume name or read-only fidelity: %+v", result.Mounts)
	}
}

func TestRecordedComposeStorageRefusesMissingOrMismatchedEvidence(t *testing.T) {
	runtime := RuntimeServices{Status: statusAvailable, Services: []RuntimeService{{ContainerID: "expected", Service: "app", Manager: "docker", ReleaseID: 9, LiveRelease: true}}}
	snapshot := runtimeReleaseSnapshot{Compose: &ResolvedComposeSnapshot{}}
	for _, detail := range []*dockerx.ContainerDetail{nil, {Container: dockerx.Container{ID: "foreign"}}} {
		owner := NewRecordedRuntimeObserver(nil, nil, &storageContainerReader{details: map[string]*dockerx.ContainerDetail{"expected": detail}}, nil)
		if _, _, err := operationalStorageSnapshot(t.Context(), owner, snapshot, runtime, 9); err == nil {
			t.Fatal("an unreadable or wrong container became empty verified storage")
		}
	}
	if _, _, err := operationalStorageSnapshot(t.Context(), &countingRuntimeObserver{}, snapshot, runtime, 9); err == nil {
		t.Fatal("an absent storage adapter became verified empty Compose storage")
	}
}
