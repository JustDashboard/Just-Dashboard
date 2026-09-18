package deploy

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/docker/docker/errdefs"
)

type PreviewResourceOwner interface {
	RemovePreviewResources(context.Context, int64) error
}

func previewLabels(environmentID int64) map[string]string {
	return map[string]string{
		"io.just-dashboard.managed":        "true",
		"io.just-dashboard.preview":        "true",
		"io.just-dashboard.environment-id": strconv.FormatInt(environmentID, 10),
	}
}

func matchesPreviewLabels(labels map[string]string, environmentID int64) bool {
	for key, value := range previewLabels(environmentID) {
		if labels[key] != value {
			return false
		}
	}
	return true
}

func previewNetworkName(environmentID int64) string {
	return fmt.Sprintf("jd-preview-e%d", environmentID)
}

func (o *DockerRuntimeOwner) QuarantinePreview(ctx context.Context, target PreviewQuarantineTarget) error {
	if o == nil || o.client == nil || target.EnvironmentID <= 0 {
		return ErrRuntimeUnavailable
	}
	labels := map[string]string{
		"io.just-dashboard.managed":        "true",
		"io.just-dashboard.environment-id": strconv.FormatInt(target.EnvironmentID, 10),
	}
	containers, err := o.client.ListContainersWithLabels(ctx, labels)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, item := range containers {
		seen[item.ID] = true
	}
	var failures []error
	if target.IncompleteOwnership {
		failures = append(failures, fmt.Errorf("%w: recorded container ownership is incomplete", ErrPreviewIsolation))
	}
	for _, id := range target.ContainerIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		observed, err := o.client.Inspect(ctx, id)
		if errdefs.IsNotFound(err) {
			continue
		}
		if err != nil {
			failures = append(failures, err)
			continue
		}
		containers = append(containers, observed.Container)
	}
	ownedReleases := map[int64]bool{}
	for _, id := range target.ReleaseIDs {
		ownedReleases[id] = true
	}
	for _, item := range containers {
		releaseID, parseErr := strconv.ParseInt(item.Labels["io.just-dashboard.release-id"], 10, 64)
		if parseErr != nil || !ownedReleases[releaseID] || item.Labels["io.just-dashboard.managed"] != "true" || item.Labels["io.just-dashboard.environment-id"] != labels["io.just-dashboard.environment-id"] {
			failures = append(failures, fmt.Errorf("%w: container release ownership is unavailable", ErrPreviewIsolation))
			continue
		}
		owner := map[string]string{
			"io.just-dashboard.managed":        "true",
			"io.just-dashboard.environment-id": labels["io.just-dashboard.environment-id"],
			"io.just-dashboard.release-id":     strconv.FormatInt(releaseID, 10),
		}
		if err := o.client.StopOwnedForIsolation(ctx, item.ID, owner); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (o *DockerRuntimeOwner) ensurePreviewResources(ctx context.Context, environmentID int64, plan RuntimePlanConfig) (string, error) {
	if err := validatePreviewPlan(environmentID, BuildPlanConfig{}, plan); err != nil {
		return "", err
	}
	labels := previewLabels(environmentID)
	for _, mount := range plan.Mounts {
		volume, err := o.client.InspectVolume(ctx, mount.Source)
		if errdefs.IsNotFound(err) {
			if _, err = o.client.CreateVolume(ctx, dockerx.VolumeSpec{Name: mount.Source, Labels: labels}); err != nil {
				return "", err
			}
			volume, err = o.client.InspectVolume(ctx, mount.Source)
		}
		if err != nil {
			return "", err
		}
		if !matchesPreviewLabels(volume.Labels, environmentID) || volume.Driver != "local" || len(volume.Options) > 0 {
			return "", fmt.Errorf("%w: preview volume name belongs to other storage", ErrPreviewIsolation)
		}
	}
	name := previewNetworkName(environmentID)
	network, err := o.client.InspectNetwork(ctx, name)
	if errdefs.IsNotFound(err) {
		// A dedicated bridge preserves Docker's inter-network isolation while
		// allowing loopback publication for the existing proxy owner. Docker
		// does not publish ports on an internal-only container network.
		if _, err = o.client.CreateNetwork(ctx, dockerx.NetworkSpec{Name: name, Labels: labels}); err != nil {
			return "", err
		}
		network, err = o.client.InspectNetwork(ctx, name)
	}
	if err != nil {
		return "", err
	}
	if network.Internal || network.Driver != "bridge" || !matchesPreviewLabels(network.Labels, environmentID) {
		return "", fmt.Errorf("%w: preview network name belongs to another network", ErrPreviewIsolation)
	}
	return name, nil
}

func (o *DockerRuntimeOwner) RemovePreviewResources(ctx context.Context, environmentID int64) error {
	if o == nil || o.client == nil || environmentID <= 0 {
		return ErrRuntimeUnavailable
	}
	// Cleanup needs both the exact name namespace and owner labels. A matching
	// prefix alone never authorizes deletion of an operator's resource.
	containers, err := o.client.ListContainersWithLabels(ctx, previewLabels(environmentID))
	if err != nil {
		return err
	}
	for _, item := range containers {
		if !matchesPreviewLabels(item.Labels, environmentID) {
			return ErrPreviewIsolation
		}
		if err := o.client.RemoveContainer(ctx, item.ID, true, true); err != nil && !errdefs.IsNotFound(err) {
			return err
		}
	}
	volumes, err := o.client.ListVolumes(ctx)
	if err != nil {
		return err
	}
	prefix := fmt.Sprintf("jd-preview-e%d-", environmentID)
	for _, volume := range volumes {
		if !strings.HasPrefix(volume.Name, prefix) || !matchesPreviewLabels(volume.Labels, environmentID) {
			continue
		}
		if err := o.client.RemoveVolume(ctx, volume.Name, false); err != nil && !errdefs.IsNotFound(err) {
			return err
		}
	}
	if o.networks != nil {
		if err := o.networks.RemoveRuntimeNetworks(ctx, environmentID); err != nil {
			return err
		}
	}
	name := previewNetworkName(environmentID)
	network, err := o.client.InspectNetwork(ctx, name)
	if errdefs.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !matchesPreviewLabels(network.Labels, environmentID) {
		return ErrPreviewIsolation
	}
	return o.client.RemoveNetwork(ctx, network.ID)
}
