package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

func validateSharedComposePopulation(containers []dockerx.Container, services []string, capturedIDs map[string]bool, environmentID int64, releaseIDs ...int64) error {
	included := map[string]bool{}
	for _, service := range services {
		if !validComposeServiceName(service) {
			return fmt.Errorf("%w: shared Compose service scope is invalid", ErrInvalidPlan)
		}
		included[service] = true
	}
	if len(included) == 0 {
		return fmt.Errorf("%w: shared Compose service scope is unavailable", ErrInvalidPlan)
	}
	for _, current := range containers {
		if strings.EqualFold(current.Labels["com.docker.compose.oneoff"], "true") || !included[current.Labels["com.docker.compose.service"]] {
			continue
		}
		if current.ID != "" && capturedIDs[current.ID] {
			continue
		}
		owned := false
		for _, releaseID := range releaseIDs {
			if adoptedContainerAuthorized(current, AdoptedContainer{}, ReleaseRuntime{EnvironmentID: environmentID, ReleaseID: releaseID}) {
				owned = true
				break
			}
		}
		if !owned {
			return fmt.Errorf("%w: an unowned regular container belongs to an included Compose service; review it in the original manager before deploying", ErrInvalidPlan)
		}
	}
	return nil
}

func (o *DockerRuntimeOwner) ValidateCandidateScope(ctx context.Context, request CandidateRuntimeRequest) error {
	project := request.Snapshot.Plan.ComposeProjectName
	if project == "" {
		return nil
	}
	if o == nil || o.client == nil || request.Snapshot.Compose == nil {
		return fmt.Errorf("%w: shared Compose candidate scope is unavailable", ErrInvalidPlan)
	}
	services := []string{}
	for _, service := range request.Snapshot.Compose.Services {
		services = append(services, service.Plan.Name)
	}
	captured := map[string]bool{}
	for _, entry := range request.Snapshot.ComposeBaseline {
		if entry.ID != "" {
			captured[entry.ID] = true
		}
	}
	for _, dependency := range request.Snapshot.Dependencies {
		if dependency.Kind != "runtime" || dependency.Ownership != OwnershipManaged || dependency.ResourceKind != "compose_stack" || dependency.ResourceID != project {
			continue
		}
		var origin WorkloadCandidate
		if json.Unmarshal(dependency.Config, &origin) != nil || origin.ResourceID != project || origin.Kind != "stack" {
			return fmt.Errorf("%w: original Compose ownership evidence is invalid", ErrInvalidPlan)
		}
		for _, service := range origin.Services {
			if service.ResourceID != "" {
				captured[service.ResourceID] = true
			}
		}
	}
	containers, err := o.client.ListContainersWithLabels(ctx, map[string]string{"com.docker.compose.project": project})
	if err != nil {
		return err
	}
	return validateSharedComposePopulation(containers, services, captured, request.Release.EnvironmentID, request.Release.ID, request.Release.PredecessorReleaseID)
}

func sharedComposeRecordedServices(metadata dockerReleaseRuntimeMetadata, containers []dockerx.Container) ([]string, error) {
	if len(metadata.ServiceNames) > 0 {
		return metadata.ServiceNames, nil
	}
	names := map[string]bool{}
	for _, entry := range metadata.BaselineContainers {
		names[entry.Service] = true
	}
	if len(names) == 0 {
		for _, id := range metadata.ContainerIDs {
			found := false
			for _, current := range containers {
				if current.ID == id {
					names[current.Labels["com.docker.compose.service"]] = true
					found = true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("%w: original shared Compose service scope cannot be safely derived", ErrInvalidPlan)
			}
		}
	}
	result := []string{}
	for name := range names {
		result = append(result, name)
	}
	sort.Strings(result)
	return result, nil
}

func baselinePopulationScope(containers []dockerx.Container, baseline []AdoptedContainer, runtime ReleaseRuntime) error {
	services, ids := []string{}, map[string]bool{}
	for _, entry := range baseline {
		services = append(services, entry.Service)
		if entry.ID != "" {
			ids[entry.ID] = true
		}
	}
	return validateSharedComposePopulation(containers, services, ids, runtime.EnvironmentID, runtime.ReleaseID)
}

func sharedExistingPopulationScope(containers []dockerx.Container, metadata dockerReleaseRuntimeMetadata, runtime ReleaseRuntime) error {
	services, err := sharedComposeRecordedServices(metadata, containers)
	if err != nil {
		return err
	}
	ids := map[string]bool{}
	for _, id := range metadata.ContainerIDs {
		ids[id] = true
	}
	return validateSharedComposePopulation(containers, services, ids, runtime.EnvironmentID, runtime.ReleaseID)
}
