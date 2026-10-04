package deploy

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/docker/docker/errdefs"
)

func configureComposeBaseline(spec *dockerx.ComposeReleaseSpec, baseline []AdoptedContainer) error {
	if len(baseline) == 0 {
		return fmt.Errorf("%w: adopted Compose baseline has no containers", ErrInvalidPlan)
	}
	spec.NoStart, spec.KeepOrphans, spec.Scales = true, true, map[string]int{}
	seen := map[string]bool{}
	for _, entry := range baseline {
		if !validComposeServiceName(entry.Service) || entry.Number < 1 || entry.Number > 1000 {
			return fmt.Errorf("%w: adopted Compose replica identity is invalid", ErrInvalidPlan)
		}
		key := entry.Service + ":" + strconv.Itoa(entry.Number)
		if seen[key] {
			return fmt.Errorf("%w: adopted Compose replica identity is duplicated", ErrInvalidPlan)
		}
		seen[key] = true
		if entry.Number > spec.Scales[entry.Service] {
			spec.Scales[entry.Service] = entry.Number
		}
	}
	for service, count := range spec.Scales {
		for number := 1; number <= count; number++ {
			if !seen[service+":"+strconv.Itoa(number)] {
				return fmt.Errorf("%w: adopted Compose replica numbers have gaps", ErrInvalidPlan)
			}
		}
		spec.Services = append(spec.Services, service)
	}
	sort.Strings(spec.Services)
	return nil
}

func composeContainerForBaseline(containers []dockerx.Container, entry AdoptedContainer) *dockerx.Container {
	for i := range containers {
		candidate := &containers[i]
		if strings.EqualFold(candidate.Labels["com.docker.compose.oneoff"], "true") {
			continue
		}
		number, _ := strconv.Atoi(candidate.Labels["com.docker.compose.container-number"])
		if candidate.Labels["com.docker.compose.service"] == entry.Service && number == entry.Number {
			return candidate
		}
	}
	return nil
}

func updatedComposeBaseline(containers []dockerx.Container, baseline []AdoptedContainer) []AdoptedContainer {
	result := append([]AdoptedContainer(nil), baseline...)
	for i := range result {
		if container := composeContainerForBaseline(containers, result[i]); container != nil {
			result[i].ID = container.ID
		}
	}
	return result
}

func (o *DockerRuntimeOwner) startComposeBaselineContainers(ctx context.Context, containers []dockerx.Container, baseline []AdoptedContainer) error {
	for _, entry := range baseline {
		container := composeContainerForBaseline(containers, entry)
		if container == nil {
			return fmt.Errorf("%w: adopted Compose replica was not restored", ErrRuntimeUnavailable)
		}
		if !entry.Running {
			continue
		}
		if err := o.client.Lifecycle(ctx, container.ID, dockerx.ActionStart, nil); err != nil && !errdefs.IsNotModified(err) {
			return err
		}
	}
	return nil
}

func (o *DockerRuntimeOwner) restoreComposeBaseline(ctx context.Context, metadata dockerReleaseRuntimeMetadata, variables map[string]string, emit func(BuildLog) error) error {
	containers, err := o.client.ListContainersWithLabels(ctx, map[string]string{"com.docker.compose.project": metadata.ProjectName})
	if err != nil {
		return err
	}
	intact := true
	for _, entry := range metadata.BaselineContainers {
		container := composeContainerForBaseline(containers, entry)
		intact = intact && container != nil && container.ID == entry.ID
	}
	if !intact {
		spec := composeSpecFromMetadata(metadata, variables)
		if err := configureComposeBaseline(&spec, metadata.BaselineContainers); err != nil {
			return err
		}
		if err := o.client.RunComposeRelease(ctx, spec, dockerx.ComposeReleaseUp, 0, composeBuildEmitter(emit)); err != nil {
			return err
		}
		containers, err = o.client.ListContainersWithLabels(ctx, map[string]string{"com.docker.compose.project": metadata.ProjectName})
		if err != nil {
			return err
		}
	}
	return o.startComposeBaselineContainers(ctx, containers, metadata.BaselineContainers)
}

func (o *DockerRuntimeOwner) stopComposeBaseline(ctx context.Context, metadata dockerReleaseRuntimeMetadata, grace int, remove bool) error {
	containers, err := o.client.ListContainersWithLabels(ctx, map[string]string{"com.docker.compose.project": metadata.ProjectName})
	if err != nil {
		return err
	}
	for _, entry := range metadata.BaselineContainers {
		container := composeContainerForBaseline(containers, entry)
		if container == nil {
			continue
		}
		// An external replacement cannot be claimed through a reused service
		// name. Recreated baselines have dashboard ownership labels.
		if container.ID != entry.ID && container.Labels["io.just-dashboard.managed"] != "true" {
			return fmt.Errorf("%w: adopted runtime ownership changed", ErrInvalidPlan)
		}
		if err := o.client.Lifecycle(ctx, container.ID, dockerx.ActionStop, entry.StopTimeout); err != nil && !errdefs.IsNotFound(err) && !errdefs.IsNotModified(err) {
			return err
		}
		if remove {
			if err := o.client.RemoveContainer(ctx, container.ID, false, false); err != nil && !errdefs.IsNotFound(err) {
				return err
			}
		}
	}
	return nil
}
