package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"gopkg.in/yaml.v3"
)

type unrepresentedImageRecoveryReader struct {
	*adoptionReaderFake
	imageRecoveries int
}

func TestRecoverDockerKnownUnrepresentableConfigurationBlocksEveryImageRecovery(t *testing.T) {
	for _, kind := range []string{"container", "stack"} {
		for _, option := range []string{"network_disabled", "shell", "legacy_mac", "console_size", "empty_masks", "empty_readonly", "privileged_masks", "unknown_raw", "stdin_once", "escaped_args", "unmapped_host"} {
			t.Run(kind+"/"+option, func(t *testing.T) {
				root := t.TempDir()
				unsafe := adoptionCaptureFixture(t, "worker", true)
				code := ""
				switch option {
				case "network_disabled":
					unsafe.Inspection.Config.NetworkDisabled = true
					code = "disabled_network_unsupported"
				case "shell":
					unsafe.Inspection.Config.Shell = []string{"/bin/bash", "-c", "artificial-private-shell-value"}
					unsafe.Inspection.Config.Healthcheck.Test = []string{"CMD-SHELL", "true"}
					code = "custom_shell_unsupported"
				case "legacy_mac":
					unsafe.Inspection.Config.MacAddress = "02:42:aa:bb:cc:dd"
					code = "legacy_mac_unsupported"
				case "console_size":
					unsafe.Inspection.Config.Tty = true
					unsafe.Inspection.HostConfig.ConsoleSize = [2]uint{24, 80}
					code = "console_size_unsupported"
				case "empty_masks":
					unsafe.Inspection.HostConfig.MaskedPaths = []string{}
					code = "custom_kernel_path_masks"
				case "empty_readonly":
					unsafe.Inspection.HostConfig.ReadonlyPaths = []string{}
					code = "custom_kernel_path_masks"
				case "privileged_masks":
					unsafe.Inspection.HostConfig.Privileged = true
					unsafe.Inspection.HostConfig.ReadonlyPaths = []string{"/proc/bus", "/proc/fs", "/proc/irq", "/proc/sys", "/proc/sysrq-trigger"}
					code = "custom_kernel_path_masks"
				case "unknown_raw":
					unsafe.UnrepresentedOptions = []string{"HostConfig.FutureMode"}
					code = "unknown_engine_configuration"
				case "stdin_once":
					unsafe.Inspection.Config.StdinOnce = true
					code = "container_config_unsupported"
				case "escaped_args":
					unsafe.Inspection.Config.ArgsEscaped = true
					code = "container_config_unsupported"
				case "unmapped_host":
					unsafe.Inspection.HostConfig.AutoRemove = true
					code = "engine_option_unsupported"
				}
				unsafe.Image, unsafe.MissingImage = nil, true
				candidate := WorkloadCandidate{Key: kind + ":original", Kind: kind, ResourceID: unsafe.Inspection.ID, Name: "original", Total: 1, Running: 1, Services: []WorkloadService{{Name: "worker", ResourceID: unsafe.Inspection.ID}}}
				fake := &adoptionReaderFake{captures: map[string]*dockerx.AdoptionContainer{unsafe.Inspection.ID: unsafe}}
				if kind == "stack" {
					// The first service could be exported safely by itself. The
					// later unrepresentable member must prevent that export too.
					safe := adoptionCaptureFixture(t, "web", true)
					safe.Image, safe.MissingImage = nil, true
					fake.captures[safe.Inspection.ID] = safe
					candidate.ResourceID = "original"
					candidate.Total, candidate.Running = 2, 2
					candidate.Services = append([]WorkloadService{{Name: "web", ResourceID: safe.Inspection.ID}}, candidate.Services...)
					source := filepath.Join(root, "compose.yml")
					if err := os.WriteFile(source, []byte("services:\n  web: {image: example/web}\n  worker: {image: example/worker}\n"), 0o600); err != nil {
						t.Fatal(err)
					}
					for name, capture := range map[string]*dockerx.AdoptionContainer{"web": safe, "worker": unsafe} {
						capture.Inspection.Config.Labels = map[string]string{"com.docker.compose.project": "original", "com.docker.compose.service": name, "com.docker.compose.container-number": "1", "com.docker.compose.project.working_dir": root, "com.docker.compose.project.config_files": source}
					}
					fake.compose = []byte(`{"services":{"web":{"image":"example/web"},"worker":{"image":"example/worker"}}}`)
				}
				reader := &unrepresentedImageRecoveryReader{adoptionReaderFake: fake}
				result, err := RecoverDockerWorkload(t.Context(), candidate, reader, files.New([]string{root}), filepath.Join(root, "recovery"))
				if !errors.Is(err, ErrRecoveryBlocked) || reader.imageRecoveries != 0 {
					t.Fatalf("unsafe original configuration reached export/import: err=%v recoveries=%d", err, reader.imageRecoveries)
				}
				found := false
				for _, issue := range result.Adoption.Issues {
					found = found || (issue.Code == code && issue.Blocking)
				}
				if !found {
					t.Fatal("unsafe configuration has no precise blocker")
				}
				raw, _ := json.Marshal(result.Adoption)
				if strings.Contains(string(raw), "artificial-private-shell-value") {
					t.Fatal("private shell configuration leaked through recovery diagnostics")
				}
				if _, err := os.Stat(filepath.Join(root, "recovery")); !os.IsNotExist(err) {
					t.Fatal("blocked capture created private image/source artifacts")
				}
			})
		}
	}
}

func TestRecoverDockerExplicitZeroResourcesAndMatchedLegacyMACRemainFaithful(t *testing.T) {
	root := t.TempDir()
	capture := adoptionCaptureFixture(t, "web", true)
	zero := int64(0)
	capture.Inspection.HostConfig.MemorySwappiness = &zero
	capture.Inspection.HostConfig.PidsLimit = &zero
	capture.Inspection.Config.MacAddress = "02:42:AA:BB:CC:DD"
	capture.Inspection.NetworkSettings.Networks["original_default"].MacAddress = "02:42:aa:bb:cc:dd"
	capture.Inspection.HostConfig.ConsoleSize = [2]uint{24, 80}
	candidate := WorkloadCandidate{Kind: "container", ResourceID: capture.Inspection.ID, Services: []WorkloadService{{Name: "web", ResourceID: capture.Inspection.ID}}}
	reader := &adoptionReaderFake{captures: map[string]*dockerx.AdoptionContainer{capture.Inspection.ID: capture}}
	result, err := RecoverDockerWorkload(t.Context(), candidate, reader, files.New([]string{root}), filepath.Join(root, "recovery"))
	if err != nil {
		t.Fatal("represented zero resources, non-terminal metadata or matched MAC were refused", err, result.Adoption.Blockers)
	}
	var model map[string]any
	if err := yaml.Unmarshal([]byte(result.Source.ComposeFiles[0].Content), &model); err != nil {
		t.Fatal(err)
	}
	service := object(object(model["services"])["app"])
	for _, field := range []string{"mem_swappiness", "pids_limit"} {
		if value, exists := service[field]; !exists || value != 0 {
			t.Fatal("explicit zero resource setting was replaced by a daemon default", field)
		}
	}
	for _, raw := range object(service["networks"]) {
		if object(raw)["mac_address"] != "02:42:aa:bb:cc:dd" {
			t.Fatal("matched legacy MAC did not reach the recovered endpoint")
		}
	}
}

func TestCapturedKernelPathDefaultsRespectPrivilegeAndNilVersusEmpty(t *testing.T) {
	for _, test := range []struct {
		name       string
		privileged bool
		masked     []string
		readonly   []string
		blocked    bool
	}{
		{"implicit_unprivileged_defaults", false, nil, nil, false},
		{"standard_unprivileged_defaults", false, []string{"/proc/asound", "/proc/acpi", "/proc/interrupts", "/proc/kcore", "/proc/keys", "/proc/latency_stats", "/proc/timer_list", "/proc/timer_stats", "/proc/sched_debug", "/proc/scsi", "/sys/firmware", "/sys/devices/virtual/powercap"}, []string{"/proc/bus", "/proc/fs", "/proc/irq", "/proc/sys", "/proc/sysrq-trigger"}, false},
		{"explicit_unprivileged_empty_masks", false, []string{}, nil, true},
		{"explicit_unprivileged_empty_readonly", false, nil, []string{}, true},
		{"privileged_implicit_unmasked", true, nil, nil, false},
		{"privileged_explicit_unmasked", true, []string{}, []string{}, false},
		{"privileged_remasked", true, []string{"/proc/kcore"}, nil, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			capture := adoptionCaptureFixture(t, "web", true)
			capture.Inspection.HostConfig.Privileged = test.privileged
			capture.Inspection.HostConfig.MaskedPaths, capture.Inspection.HostConfig.ReadonlyPaths = test.masked, test.readonly
			r := &dockerRecovery{result: &RecoveredWorkload{Adoption: &WorkloadAdoption{}}}
			r.checkCapturedRuntimeConfiguration("web", capture)
			if (len(r.result.Adoption.Blockers) > 0) != test.blocked {
				t.Fatal("kernel path defaults confused nil, explicit empty or privilege semantics")
			}
		})
	}
	capture := adoptionCaptureFixture(t, "web", true)
	capture.Inspection.Config.MacAddress = "02:42:aa:bb:cc:dd"
	capture.Inspection.HostConfig.NetworkMode = container.NetworkMode("host")
	capture.Inspection.NetworkSettings.Networks["host"] = &network.EndpointSettings{MacAddress: capture.Inspection.Config.MacAddress}
	if capturedLegacyMACRepresented(capture) {
		t.Fatal("host network metadata was claimed as a reproducible endpoint MAC")
	}
}

func (r *unrepresentedImageRecoveryReader) RecoverAdoptionImage(_ context.Context, _ *dockerx.AdoptionContainer, _ string, _ []string) (*dockerx.ImageDetail, error) {
	r.imageRecoveries++
	return nil, errors.New("image recovery must not run for unknown original settings")
}

func TestRecoverDockerUnknownEffectiveOptionsBlockBeforeImageRecovery(t *testing.T) {
	root := t.TempDir()
	capture := adoptionCaptureFixture(t, "web", true)
	capture.Image, capture.MissingImage = nil, true
	capture.UnrepresentedOptions = []string{"HostConfig.FutureMode"}
	candidate := WorkloadCandidate{Key: "container:" + capture.Inspection.ID, Kind: "container", ResourceID: capture.Inspection.ID, Name: "original-web", Total: 1, Running: 1, Services: []WorkloadService{{Name: "original-web", ResourceID: capture.Inspection.ID}}}
	reader := &unrepresentedImageRecoveryReader{adoptionReaderFake: &adoptionReaderFake{captures: map[string]*dockerx.AdoptionContainer{capture.Inspection.ID: capture}}}
	result, err := RecoverDockerWorkload(t.Context(), candidate, reader, files.New([]string{root}), filepath.Join(root, "recovery"))
	if !errors.Is(err, ErrRecoveryBlocked) || reader.imageRecoveries != 0 {
		t.Fatal("unknown original settings did not block before image export/import")
	}
	for _, issue := range result.Adoption.Issues {
		if issue.Code == "unknown_engine_configuration" && issue.Field == "HostConfig.FutureMode" && issue.Blocking {
			return
		}
	}
	t.Fatal("unknown original settings have no actionable blocker")
}
