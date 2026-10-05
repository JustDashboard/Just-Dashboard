package deploy

import (
	"context"
	"encoding/json"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

// RuntimeReservationObserver verifies only registered, server-owned adoption
// reservations. Other dependency inventories remain with their feature adapters.
type RuntimeReservationObserver struct {
	store      *store.Store
	containers RecordedContainerReader
	native     NativeBaselineObserver
}

func NewRuntimeReservationObserver(base *store.Store, containers RecordedContainerReader, native NativeBaselineObserver) *RuntimeReservationObserver {
	return &RuntimeReservationObserver{store: base, containers: containers, native: native}
}

func (o *RuntimeReservationObserver) ObserveDependencies(ctx context.Context, dependencies []PlannedDependency) ([]DependencyObservation, error) {
	result := []DependencyObservation{}
	for _, dependency := range dependencies {
		if dependency.Kind == "runtime" {
			result = append(result, o.ObserveRuntimeDependency(ctx, dependency))
		}
	}
	return result, nil
}

// A runtime dependency reserves original provenance permanently. After cutover,
// its original container IDs are not the current release's container IDs.
func (o *RuntimeReservationObserver) ObserveRuntimeDependency(ctx context.Context, dependency PlannedDependency) DependencyObservation {
	observed := DependencyObservation{Kind: dependency.Kind, ResourceKind: dependency.ResourceKind, ResourceID: dependency.ResourceID,
		Detail: "The server-owned runtime reservation and registered baseline could not be verified."}
	if o.store == nil || dependency.Kind != "runtime" || dependency.Ownership != OwnershipManaged {
		return observed
	}
	kind := map[string]string{"compose_stack": "stack", "docker_container": "container", "pm2_process": "pm2", "systemd_unit": "systemd"}[dependency.ResourceKind]
	if kind == "" {
		return observed
	}
	rows, err := o.store.DB.QueryContext(ctx, `SELECT d.environment_id,e.live_release_id,d.config_json FROM deploy_dependencies d
	 JOIN deploy_environments e ON e.id=d.environment_id WHERE d.release_id=0 AND d.kind='runtime' AND d.ownership='managed'
	 AND d.resource_kind=? AND d.resource_id=?`, dependency.ResourceKind, dependency.ResourceID)
	if err != nil {
		return observed
	}
	var environmentID, liveReleaseID int64
	var config string
	count := 0
	for rows.Next() {
		if err := rows.Scan(&environmentID, &liveReleaseID, &config); err != nil {
			rows.Close()
			return observed
		}
		count++
	}
	readErr := rows.Err()
	rows.Close()
	var original WorkloadCandidate
	var storedConfig, suppliedConfig any
	if readErr != nil || count != 1 || liveReleaseID <= 0 || json.Unmarshal([]byte(config), &original) != nil ||
		original.Kind != kind || original.ResourceID != dependency.ResourceID || original.Key == "" ||
		json.Unmarshal([]byte(config), &storedConfig) != nil || json.Unmarshal(dependency.Config, &suppliedConfig) != nil || !reflect.DeepEqual(storedConfig, suppliedConfig) {
		return observed
	}
	var baselineID int64
	if err := o.store.DB.QueryRowContext(ctx, `SELECT id FROM deploy_releases WHERE environment_id=?
	 AND json_extract(provenance_json,'$.adopted')=1 AND json_extract(provenance_json,'$.workloadKey')=?
	 ORDER BY release_number LIMIT 1`, environmentID, original.Key).Scan(&baselineID); err != nil {
		return observed
	}
	runs := NewOrchestrationStore(o.store)
	baseline, err := runs.RuntimeForRelease(ctx, baselineID)
	expectedKind := map[string]string{"stack": "compose", "container": "container", "pm2": "pm2", "systemd": "systemd"}[kind]
	if err != nil || baseline == nil || baseline.EnvironmentID != environmentID {
		return observed
	}
	// Docker rollback may recreate the original baseline as managed Compose
	// containers. Its immutable provenance still reserves the original identity.
	var baselineMetadata struct {
		Adopted bool `json:"adopted"`
	}
	_ = json.Unmarshal(baseline.Metadata, &baselineMetadata)
	initialIdentity := baseline.Kind == expectedKind && baseline.RuntimeID == dependency.ResourceID
	recreatedDockerBaseline := (kind == "stack" || kind == "container") && baseline.Kind == "compose" && baselineMetadata.Adopted
	if !initialIdentity && !recreatedDockerBaseline {
		return observed
	}
	current, err := runs.RuntimeForRelease(ctx, liveReleaseID)
	if err != nil || current == nil || current.EnvironmentID != environmentID {
		return observed
	}
	switch kind {
	case "stack":
		observed.DeepLink = "/docker/stacks/" + url.PathEscape(original.ResourceID)
	case "container":
		observed.DeepLink = "/docker/containers/" + url.PathEscape(original.ResourceID)
	case "pm2":
		observed.DeepLink = "/processes/pm2"
	case "systemd":
		observed.DeepLink = "/processes/services"
	}
	if kind == "stack" || kind == "container" {
		if current.Kind == "compose" {
			observed.DeepLink = "/docker/stacks/" + url.PathEscape(current.RuntimeID)
		} else if current.Kind == "container" {
			observed.DeepLink = "/docker/containers/" + url.PathEscape(current.RuntimeID)
		}
	}
	currentNative := current.Kind == "pm2" || current.Kind == "systemd"
	if currentNative {
		// Rollback creates a new live release using the retained native manager.
		// It must carry the exact original captured authority, even though its
		// release ID differs from the initial adopted release.
		var baselineAuthority, currentAuthority any
		if current.Kind != expectedKind || current.RuntimeID != original.ResourceID ||
			json.Unmarshal(baseline.Metadata, &baselineAuthority) != nil || json.Unmarshal(current.Metadata, &currentAuthority) != nil ||
			!reflect.DeepEqual(baselineAuthority, currentAuthority) || o.native == nil || o.native.ObserveNativeBaseline(ctx, *current).Status != "available" {
			observed.Detail = "The original manager configuration, source or retained process authority could not be verified."
			return observed
		}
	} else if !o.reservedDockerRuntimeAvailable(ctx, *current, liveReleaseID != baselineID || !initialIdentity) {
		observed.Detail = "The registered Docker runtime is missing or its exact container ownership could not be verified."
		return observed
	}
	observed.Available, observed.Status, observed.Detail = true, "available", "The owning environment's registered runtime is present; stopped containers and retained native applications are valid."
	if !currentNative && (kind == "pm2" || kind == "systemd") &&
		(o.native == nil || o.native.ObserveNativeBaseline(ctx, *baseline).Status != "available") {
		observed.Warning = "The current managed Docker runtime is verified, but the original native manager or source is unavailable. Rollback to the imported baseline cannot be verified until its original authority is restored."
	}
	return observed
}

func (o *RuntimeReservationObserver) reservedDockerRuntimeAvailable(ctx context.Context, runtime ReleaseRuntime, managed bool) bool {
	if o.containers == nil || (runtime.Kind != "container" && runtime.Kind != "compose") {
		return false
	}
	var metadata struct {
		Adopted            bool               `json:"adopted"`
		ContainerIDs       []string           `json:"containerIds"`
		PrimaryContainerID string             `json:"primaryContainerId"`
		BaselineContainers []AdoptedContainer `json:"baselineContainers"`
	}
	if json.Unmarshal(runtime.Metadata, &metadata) != nil {
		return false
	}
	if runtime.Kind == "compose" && metadata.Adopted && len(metadata.BaselineContainers) > 0 {
		inventory, ok := o.containers.(RuntimeObserver)
		if !ok {
			return false
		}
		containers, err := inventory.ListContainersWithLabels(ctx, map[string]string{"com.docker.compose.project": runtime.RuntimeID})
		if err != nil {
			return false
		}
		// Compensation can recreate a baseline without changing its immutable
		// runtime record. Reuse the lifecycle authority rules, and require the
		// exact captured replica count rather than any owned container in a stack.
		scope, err := composeBaselineScope(containers, metadata.BaselineContainers, runtime, false)
		if err != nil || len(scope) != len(metadata.BaselineContainers) {
			return false
		}
		expected, services := map[string]bool{}, map[string]bool{}
		for _, entry := range metadata.BaselineContainers {
			services[entry.Service] = true
			expected[entry.Service+":"+strconv.Itoa(entry.Number)] = true
		}
		for _, container := range containers {
			service := container.Labels["com.docker.compose.service"]
			if strings.EqualFold(container.Labels["com.docker.compose.oneoff"], "true") || !services[service] {
				continue
			}
			number, err := strconv.Atoi(container.Labels["com.docker.compose.container-number"])
			if err != nil || !expected[service+":"+strconv.Itoa(number)] {
				return false
			}
		}
		for _, target := range scope {
			if !o.reservedDockerContainerAvailable(ctx, runtime, target.container.ID, managed || target.container.ID != target.entry.ID) {
				return false
			}
			detail, err := o.containers.Inspect(ctx, target.container.ID)
			number := 0
			if detail != nil {
				number, _ = strconv.Atoi(detail.Labels["com.docker.compose.container-number"])
			}
			if err != nil || detail == nil || detail.Labels["com.docker.compose.service"] != target.entry.Service || number != target.entry.Number {
				return false
			}
		}
		return true
	}
	ids := map[string]bool{}
	for _, id := range metadata.ContainerIDs {
		ids[id] = true
	}
	for _, member := range metadata.BaselineContainers {
		ids[member.ID] = true
	}
	if metadata.PrimaryContainerID != "" {
		ids[metadata.PrimaryContainerID] = true
	}
	if len(ids) == 0 {
		return false
	}
	for id := range ids {
		if !o.reservedDockerContainerAvailable(ctx, runtime, id, managed) {
			return false
		}
	}
	return true
}

func (o *RuntimeReservationObserver) reservedDockerContainerAvailable(ctx context.Context, runtime ReleaseRuntime, id string, managed bool) bool {
	if len(id) != 64 {
		return false
	}
	detail, err := o.containers.Inspect(ctx, id)
	if err != nil || detail == nil || detail.ID != id || (runtime.Kind == "compose" && detail.ComposeStack != runtime.RuntimeID) {
		return false
	}
	// Original unlabeled containers are admitted only by their recorded full
	// IDs. Every managed replacement must belong to this exact live release.
	if managed || detail.Labels["io.just-dashboard.managed"] != "" || detail.Labels["io.just-dashboard.environment-id"] != "" || detail.Labels["io.just-dashboard.release-id"] != "" {
		return detail.Labels["io.just-dashboard.managed"] == "true" &&
			detail.Labels["io.just-dashboard.environment-id"] == strconv.FormatInt(runtime.EnvironmentID, 10) &&
			detail.Labels["io.just-dashboard.release-id"] == strconv.FormatInt(runtime.ReleaseID, 10)
	}
	return true
}
