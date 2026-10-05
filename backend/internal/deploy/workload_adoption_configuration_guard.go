package deploy

import (
	"bytes"
	"context"
	"net"
	"path/filepath"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/docker/docker/api/types/container"
)

func (r *dockerRecovery) checkCapturedRuntimeConfiguration(name string, capture *dockerx.AdoptionContainer) {
	config, host := capture.Inspection.Config, capture.Inspection.HostConfig
	if config.NetworkDisabled {
		r.issue("disabled_network_unsupported", "The original container explicitly disables networking. A normal Compose network would change its isolation; resolve this setting in the original manager before adoption.", name, "Config.NetworkDisabled", true)
	}
	if len(config.Shell) > 0 {
		r.issue("custom_shell_unsupported", "The original container has an explicit shell configuration. Compose cannot override that setting faithfully, including shell-form health checks after image recovery; resolve it in the original manager before adoption.", name, "Config.Shell", true)
	}
	if config.StdinOnce || config.ArgsEscaped {
		r.issue("container_config_unsupported", "Single-use stdin or Windows escaped commands cannot be faithfully represented by Linux Compose deployments.", name, "config", true)
	}
	if config.MacAddress != "" && !capturedLegacyMACRepresented(capture) {
		r.issue("legacy_mac_unsupported", "The original legacy MAC address does not match a captured network endpoint that the recipe can preserve. Review its network identity in the original manager before adoption.", name, "Config.MacAddress", true)
	}
	if config.Tty && (host.ConsoleSize[0] != 0 || host.ConsoleSize[1] != 0) {
		r.issue("console_size_unsupported", "The original terminal has explicit dimensions that Compose cannot preserve. Resolve its interactive terminal configuration before adoption.", name, "HostConfig.ConsoleSize", true)
	}
	pathsRepresented := defaultMaskedPaths(host.MaskedPaths) && defaultReadonlyPaths(host.ReadonlyPaths)
	if host.Privileged {
		pathsRepresented = len(host.MaskedPaths) == 0 && len(host.ReadonlyPaths) == 0
	}
	if !pathsRepresented {
		r.issue("custom_kernel_path_masks", "Explicit empty or custom masked/read-only kernel paths differ from the default for this privilege mode and have no faithful Compose mapping.", name, "HostConfig.MaskedPaths/ReadonlyPaths", true)
	}
}

func (r *dockerRecovery) checkCapturedServiceMappings(stack bool) {
	for _, name := range sortedRecoveryServices(r.containers) {
		captures := r.containers[name]
		for _, other := range captures[1:] {
			if containerSettingsDigest(other) != containerSettingsDigest(captures[0]) {
				r.issue("replica_configuration_differs", "Replicas of this service have different images or runtime settings. Reconcile them before adoption.", name, "", true)
			}
			if replicaStorageDigest(other) != replicaStorageDigest(captures[0]) {
				r.issue("replica_storage_differs", "Replicas use different resolved storage. A single Compose service cannot preserve their separate volumes or bind destinations; review and reconcile the replica storage before adoption.", name, "volumes", true)
			}
			if replicaNetworkDigest(other) != replicaNetworkDigest(captures[0]) {
				r.issue("replica_network_identity_differs", "Replicas have different endpoint addresses, MAC identities or aliases. Those per-replica network settings cannot be applied as one Compose service; review the original network configuration before adoption.", name, "networks", true)
			}
		}
		for _, capture := range r.containers[name] {
			probe := *capture
			if probe.Image == nil {
				probe.Image = &dockerx.ImageDetail{}
			}
			// Validate the same adapter used by the final recipe, without
			// copying private probe inputs into that recipe or creating images.
			validation := &dockerRecovery{result: &RecoveredWorkload{Environment: map[string]string{}, Adoption: &WorkloadAdoption{}}, paths: r.paths, model: map[string]any{}}
			validation.recoverService(name, map[string]any{}, &probe, stack)
			for _, issue := range validation.result.Adoption.Issues {
				r.issue(issue.Code, issue.Message, issue.Service, issue.Field, issue.Blocking)
			}
		}
	}
}

func capturedLegacyMACRepresented(capture *dockerx.AdoptionContainer) bool {
	mode := string(capture.Inspection.HostConfig.NetworkMode)
	if mode == "host" || mode == "none" || strings.HasPrefix(mode, "container:") || capture.Inspection.NetworkSettings == nil {
		return false
	}
	original, err := net.ParseMAC(capture.Inspection.Config.MacAddress)
	if err != nil {
		return false
	}
	for _, endpoint := range capture.Inspection.NetworkSettings.Networks {
		if endpoint == nil {
			continue
		}
		actual, err := net.ParseMAC(endpoint.MacAddress)
		if err == nil && bytes.Equal(original, actual) {
			return true
		}
	}
	return false
}

func (r *dockerRecovery) recoverMissingImages(ctx context.Context, reader DockerWorkloadRecoveryReader, recoveryRoot string) error {
	recoverer, ok := reader.(dockerAdoptionImageRecoverer)
	if !ok {
		return nil
	}
	for _, name := range sortedRecoveryServices(r.containers) {
		for _, captured := range r.containers[name] {
			if captured.Image != nil || !captured.MissingImage {
				continue
			}
			if err := makePrivateDirectory(recoveryRoot); err != nil {
				return err
			}
			exclude := append([]string(nil), captured.RegenerablePaths...)
			for _, change := range captured.Changes {
				if change.Kind == container.ChangeAdd && strings.HasSuffix(change.Path, ".pyc") && filepath.Base(filepath.Dir(change.Path)) == "__pycache__" {
					exclude = append(exclude, change.Path)
				}
			}
			image, err := recoverer.RecoverAdoptionImage(ctx, captured, recoveryRoot, exclude)
			if err != nil {
				r.issue("missing_image_recovery_unavailable", err.Error(), name, "image", true)
				return nil
			}
			captured.Image = image
			r.issue("missing_image_snapshotted", "The original image is missing. Its running filesystem was captured without pausing or changing the container as a new private local image; captured environment values and mounted storage stay outside that image. Keep this recovery image available for deployment and rollback.", name, "image", false)
		}
	}
	return nil
}
