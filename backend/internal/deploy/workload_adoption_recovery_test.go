package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"
	"gopkg.in/yaml.v3"
)

type adoptionReaderFake struct {
	captures map[string]*dockerx.AdoptionContainer
	images   map[string]*dockerx.ImageDetail
	compose  []byte
	reads    int
}

func (f *adoptionReaderFake) CaptureAdoptionContainer(_ context.Context, id string) (*dockerx.AdoptionContainer, error) {
	if result := f.captures[id]; result != nil {
		return result, nil
	}
	return nil, errors.New("not found")
}
func (f *adoptionReaderFake) InspectImage(_ context.Context, id string) (*dockerx.ImageDetail, error) {
	if result := f.images[id]; result != nil {
		return result, nil
	}
	return nil, errors.New("not found")
}
func (f *adoptionReaderFake) ReadComposeAdoptionConfiguration(_ context.Context, _, _ string, _, _ []string) ([]byte, error) {
	f.reads++
	return f.compose, nil
}

func adoptionCaptureFixture(t *testing.T, name string, running bool) *dockerx.AdoptionContainer {
	t.Helper()
	id := strings.Repeat("a", 64)
	if name != "web" {
		id = strings.Repeat("b", 64)
	}
	init := false
	port := nat.Port("3000/tcp")
	return &dockerx.AdoptionContainer{Inspection: container.InspectResponse{
		ContainerJSONBase: &container.ContainerJSONBase{ID: id, Name: "/original-" + name, Image: fakeContentDigest(name), State: &container.State{Running: running}, HostConfig: &container.HostConfig{
			NetworkMode: "original_default", PortBindings: nat.PortMap{port: {{HostIP: "127.0.0.1", HostPort: "3000"}}}, RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyOnFailure, MaximumRetryCount: 4}, Init: &init, ReadonlyRootfs: true, ShmSize: 64 << 20, DNS: []string{"1.1.1.1"}, DNSOptions: []string{"ndots:0"}, ExtraHosts: []string{"host.docker.internal:host-gateway"}, CapDrop: []string{"NET_RAW"}, LogConfig: container.LogConfig{Type: "local", Config: map[string]string{"max-size": "5m"}}, Resources: container.Resources{Memory: 128 << 20, NanoCPUs: 1500000000, MemoryReservation: 64 << 20, MemorySwap: -1},
		}},
		Config:          &container.Config{Image: "example/" + name + ":latest", Hostname: "original-host", User: "1001:1002", WorkingDir: "/application", Entrypoint: []string{"/bin/server"}, Cmd: []string{"--serve"}, Env: []string{"PASSWORD=sensitive-original-value", "PORT=3000", "EMPTY="}, Labels: map[string]string{"custom.label": "original-label-value"}, StopSignal: "SIGQUIT", Healthcheck: &container.HealthConfig{Test: []string{"CMD", "true"}, Interval: 5 * time.Second}},
		Mounts:          []container.MountPoint{{Type: mount.TypeVolume, Name: "existing_data", Source: "/var/lib/docker/volumes/existing_data/_data", Destination: "/data", RW: true}},
		NetworkSettings: &container.NetworkSettings{Networks: map[string]*network.EndpointSettings{"original_default": {Aliases: []string{"custom-alias"}, IPAMConfig: &network.EndpointIPAMConfig{IPv4Address: "172.22.0.8"}}}},
	}, Image: &dockerx.ImageDetail{ID: fakeContentDigest(name), OS: "linux", Architecture: "amd64"}}
}

func TestRecoverDockerContainerKeepsFullSettingsAndPrivateValues(t *testing.T) {
	root := t.TempDir()
	capture := adoptionCaptureFixture(t, "web", true)
	candidate := WorkloadCandidate{Key: "container:" + capture.Inspection.ID, Kind: "container", ResourceID: capture.Inspection.ID, Name: "original-web", Total: 1, Running: 1, Services: []WorkloadService{{Name: "original-web", ResourceID: capture.Inspection.ID}}}
	reader := &adoptionReaderFake{captures: map[string]*dockerx.AdoptionContainer{capture.Inspection.ID: capture}}
	recovered, err := RecoverDockerWorkload(context.Background(), candidate, reader, files.New([]string{root}), filepath.Join(root, "recovery"))
	if err != nil {
		t.Fatalf("recover: %v; blockers %v", err, recovered.Adoption.Blockers)
	}
	if reader.reads != 0 {
		t.Fatal("standalone recovery tried to guess a Compose source")
	}
	var model map[string]any
	if err := yaml.Unmarshal([]byte(recovered.Source.ComposeFiles[0].Content), &model); err != nil {
		t.Fatal(err)
	}
	service := object(object(model["services"])["app"])
	for field, want := range map[string]string{"user": "1001:1002", "working_dir": "/application", "stop_signal": "SIGQUIT", "restart": "on-failure:4", "hostname": "original-host"} {
		if service[field] != want {
			t.Fatalf("%s=%v want %s", field, service[field], want)
		}
	}
	if _, exists := service["container_name"]; exists {
		t.Fatal("new managed container would conflict with the recovery container's name")
	}
	for _, field := range []string{"entrypoint", "command", "healthcheck", "logging", "dns", "dns_opt", "extra_hosts", "cap_drop", "mem_limit", "mem_reservation", "memswap_limit", "cpus", "shm_size", "volumes", "networks", "ports", "init", "read_only"} {
		if _, exists := service[field]; !exists {
			t.Fatalf("lost original field %s", field)
		}
	}
	if service["image"] != capture.Image.ID {
		t.Fatal("image not pinned to the exact running image")
	}
	raw, _ := json.Marshal(recovered)
	for _, secret := range []string{"sensitive-original-value", "original-label-value"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("private captured value leaked into the public recovery payload")
		}
	}
	if len(recovered.Environment) < 4 {
		t.Fatal("captured values were not staged privately")
	}
	for _, variable := range recovered.Configuration.Variables {
		if recovered.Environment[variable.Name] == "" && variable.Required {
			t.Fatal("an intentionally empty original value is incorrectly required to be nonempty")
		}
	}
	if recovered.Adoption.Runtime.RuntimeID != capture.Inspection.ID || recovered.Adoption.Runtime.Kind != "container" {
		t.Fatal("baseline does not point to the original runtime")
	}
	baseline, err := decodeReleaseRuntimeSnapshot(&ReleaseWithArtifacts{Release: Release{ConfigDigest: digestBytes(recovered.Adoption.Snapshot), Strategy: StrategyStopFirst}, Artifacts: []ReleaseArtifact{{Kind: ArtifactRuntimeConfig, State: "available", Digest: digestBytes(recovered.Adoption.Snapshot), Metadata: mustJSON(map[string]any{"snapshot": recovered.Adoption.Snapshot})}}})
	if err != nil || len(baseline.ComposeBaseline) != 1 || !baseline.ComposeBaseline[0].Running {
		t.Fatalf("baseline snapshot %v, %v", baseline, err)
	}
	for _, path := range []string{"compose.yml", "release.yml"} {
		info, err := os.Stat(filepath.Join(recovered.Adoption.RecoveryDirectory, path))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("baseline file is not private: %v", err)
		}
	}
	if recovered.Adoption.BaselineDigest != recoveredDockerBaselineDigest(recovered) {
		t.Fatal("baseline fingerprint differs from its source/settings/private values")
	}
}

func TestRecoverDockerBlocksUncapturedWritableLayerAndUnsupportedEngineOptions(t *testing.T) {
	for _, kind := range []string{"writable", "option", "secretargv", "bind"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			capture := adoptionCaptureFixture(t, "web", true)
			switch kind {
			case "writable":
				capture.Changes = []container.FilesystemChange{{Path: "/application/database.sqlite", Kind: container.ChangeModify}}
			case "option":
				capture.Inspection.HostConfig.AutoRemove = true
			case "secretargv":
				capture.Inspection.Config.Cmd = []string{"--password", "do-not-leak"}
			case "bind":
				capture.Inspection.Mounts = []container.MountPoint{{Type: mount.TypeBind, Source: filepath.Join(t.TempDir(), "data"), Destination: "/data", RW: true}}
			}
			candidate := WorkloadCandidate{Kind: "container", Services: []WorkloadService{{Name: "web", ResourceID: capture.Inspection.ID}}}
			reader := &adoptionReaderFake{captures: map[string]*dockerx.AdoptionContainer{capture.Inspection.ID: capture}}
			result, err := RecoverDockerWorkload(context.Background(), candidate, reader, files.New([]string{root}), filepath.Join(root, "recovery"))
			if !errors.Is(err, ErrRecoveryBlocked) || len(result.Adoption.Blockers) == 0 {
				t.Fatalf("unsafe recovery accepted: %v, %v", err, result.Adoption.Blockers)
			}
			raw, _ := json.Marshal(result.Adoption)
			if strings.Contains(string(raw), "do-not-leak") {
				t.Fatal("blocked command leaked into diagnostics")
			}
			if _, err := os.Stat(filepath.Join(root, "recovery")); !os.IsNotExist(err) {
				t.Fatal("blocked recovery staged executable source")
			}
		})
	}
}

func TestRecoverComposePreservesProjectAndStoppedMissingServiceShape(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "compose.yml")
	if err := os.WriteFile(source, []byte("services:\n  web: {image: example/web:latest}\n  worker: {image: example/worker:latest}\n  missing: {image: example/missing:latest}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	web, worker := adoptionCaptureFixture(t, "web", true), adoptionCaptureFixture(t, "worker", false)
	for name, capture := range map[string]*dockerx.AdoptionContainer{"web": web, "worker": worker} {
		capture.Inspection.Config.Labels = map[string]string{"com.docker.compose.project": "original", "com.docker.compose.service": name, "com.docker.compose.container-number": "1", "com.docker.compose.project.working_dir": root, "com.docker.compose.project.config_files": source}
	}
	reader := &adoptionReaderFake{captures: map[string]*dockerx.AdoptionContainer{web.Inspection.ID: web, worker.Inspection.ID: worker}, images: map[string]*dockerx.ImageDetail{"example/missing:latest": {ID: fakeContentDigest("missing")}}, compose: []byte(`{"services":{"web":{"image":"example/web:latest"},"worker":{"image":"example/worker:latest"},"missing":{"image":"example/missing:latest","environment":{"PASSWORD":"different-service-password"}}}}`)}
	candidate := WorkloadCandidate{Key: "stack:original", Kind: "stack", Name: "original", ResourceID: "original", Total: 3, Running: 1, Services: []WorkloadService{{Name: "web", ResourceID: web.Inspection.ID}, {Name: "worker", ResourceID: worker.Inspection.ID}, {Name: "missing"}}}
	result, err := RecoverDockerWorkload(context.Background(), candidate, reader, files.New([]string{root}), filepath.Join(root, "recovery"))
	if err != nil {
		t.Fatalf("recover: %v %v", err, result.Adoption.Blockers)
	}
	if result.Configuration.Runtime.ComposeProjectName != "original" {
		t.Fatal("future redeploy changed project identity")
	}
	var metadata dockerReleaseRuntimeMetadata
	if json.Unmarshal(result.Adoption.Runtime.Metadata, &metadata) != nil || len(metadata.BaselineContainers) != 2 {
		t.Fatal("baseline must include only the two actual containers")
	}
	states := map[string]bool{}
	for _, entry := range metadata.BaselineContainers {
		states[entry.Service] = entry.Running
	}
	if !states["web"] || states["worker"] {
		t.Fatal("baseline forgot active/inactive services")
	}
	if _, exists := states["missing"]; exists {
		t.Fatal("baseline would create a previously absent service")
	}
	if strings.Contains(result.Source.ComposeFiles[0].Content, "different-service-password") {
		t.Fatal("inactive service secret leaked")
	}
	if reader.reads != 1 {
		t.Fatal("did not use the authoritative Compose parser")
	}
}

func TestComposeBaselineScopeHasExplicitCountsAndNeverStartsMissingServices(t *testing.T) {
	baseline := []AdoptedContainer{{ID: "one", Service: "web", Number: 1, Running: true}, {ID: "two", Service: "web", Number: 2, Running: false}, {ID: "three", Service: "worker", Number: 1, Running: true}}
	var spec dockerx.ComposeReleaseSpec
	if err := configureComposeBaseline(&spec, baseline); err != nil {
		t.Fatal(err)
	}
	if !spec.NoStart || !spec.KeepOrphans || spec.Scales["web"] != 2 || strings.Join(spec.Services, ",") != "web,worker" {
		t.Fatalf("unsafe baseline invocation: %+v", spec)
	}
	if err := configureComposeBaseline(&dockerx.ComposeReleaseSpec{}, []AdoptedContainer{{Service: "web", Number: 2}}); err == nil {
		t.Fatal("accepted a scale with an absent original replica")
	}
	containers := []dockerx.Container{{ID: "new-one", Labels: map[string]string{"com.docker.compose.service": "web", "com.docker.compose.container-number": "1"}}, {ID: "oneoff", Labels: map[string]string{"com.docker.compose.service": "web", "com.docker.compose.container-number": "2", "com.docker.compose.oneoff": "True"}}}
	if result := composeContainerForBaseline(containers, baseline[0]); result == nil || result.ID != "new-one" {
		t.Fatal("did not remap a recreated baseline replica")
	}
	if result := composeContainerForBaseline(containers, baseline[1]); result != nil {
		t.Fatal("one-off task substituted for original replica")
	}
}

func TestRecoveredWorkloadDigestFencesPrivateValues(t *testing.T) {
	source := DraftSourceConfig{Kind: SourceCompose, Mode: SourceModeComposePaste, ComposeFiles: []ComposeDocument{{Path: "compose.yml", Content: "services: {}"}}}
	plan := PlanConfiguration{Build: BuildPlanConfig{Method: BuildCompose}, Runtime: RuntimePlanConfig{Strategy: StrategyStopFirst}}
	first := RecoveredWorkloadDigest(source, plan, map[string]string{"PASSWORD": "old"})
	if first == RecoveredWorkloadDigest(source, plan, map[string]string{"PASSWORD": "new"}) {
		t.Fatal("changed private environment passed the baseline fence")
	}
}

func TestComposeAdoptedLocalImagesNeverResolveOrPull(t *testing.T) {
	id := fakeContentDigest("adopted-local")
	if normalized, err := normalizeImageReference(id); err != nil || normalized != id {
		t.Fatalf("local identity normalized into a mutable registry reference: %q %v", normalized, err)
	}
	backend := &artifactBackendFake{inspected: map[string]ResolvedImage{id: {Reference: id, Digest: id, ConfigDigest: id, OS: "linux", Architecture: "amd64"}}}
	analysis := &ComposeAnalysis{Digest: fakeContentDigest("compose"), Files: []string{"compose.yml"}, Services: []ComposeServicePlan{{Name: "app", Image: id}, {Name: "worker", Image: id}}}
	result, err := NewArtifactBuilder(backend).Build(context.Background(), t.TempDir(), "adopted", BuildPlanConfig{Method: BuildCompose}, PreparedBuild{Method: BuildCompose}, nil, nil, "", SourceIdentity{Kind: SourceCompose, Digest: analysis.Digest}, analysis, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(backend.pulls) > 0 || len(backend.resolves) > 0 || len(backend.builds) > 0 {
		t.Fatal("local image adoption contacted a registry or rebuilt the source")
	}
	if result.Compose == nil || len(result.Compose.Services) != 2 || result.Compose.Services[0].ConfigDigest != id || len(result.Artifacts) != 2 {
		t.Fatal("local image artifact did not retain its exact identity")
	}
	backend.inspected[id] = ResolvedImage{Reference: id, Digest: id, ConfigDigest: fakeContentDigest("different")}
	if _, err := NewArtifactBuilder(backend).Build(context.Background(), t.TempDir(), "adopted", BuildPlanConfig{Method: BuildCompose}, PreparedBuild{}, nil, nil, "", SourceIdentity{}, analysis, nil); err == nil {
		t.Fatal("accepted an image whose inspected identity did not match the capture")
	}
}
