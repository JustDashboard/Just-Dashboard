package deploy

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/docker/docker/errdefs"
)

var ErrRuntimeUnavailable = errors.New("deployment runtime is unavailable")

type CandidateRuntimeRequest struct {
	Run              EngineRun
	Release          Release
	Snapshot         runtimeReleaseSnapshot
	SourceRoot       string
	RuntimeVariables map[string]string
	Host             string
	Port             int
	PortLeaseToken   string
	Networks         []string
}

type StartedRuntime struct {
	Input  ReleaseRuntimeInput `json:"runtime"`
	Target CheckTarget         `json:"target"`
}

type RuntimeStopEvidence struct {
	RuntimeID    string    `json:"runtimeId"`
	Signal       string    `json:"signal"`
	GraceSeconds int       `json:"graceSeconds"`
	StartedAt    time.Time `json:"startedAt"`
	CompletedAt  time.Time `json:"completedAt"`
	Forced       bool      `json:"forced"`
	Removed      bool      `json:"removed"`
}

type RuntimeOwner interface {
	StartCandidate(context.Context, CandidateRuntimeRequest, func(BuildLog) error) (StartedRuntime, error)
	StartExisting(context.Context, ReleaseRuntime, map[string]string, func(BuildLog) error) error
	Stop(context.Context, ReleaseRuntime, RuntimePlanConfig, map[string]string, bool, func(BuildLog) error) (RuntimeStopEvidence, error)
}

type RuntimeStorageOwner interface {
	PersistentSources(context.Context, CandidateRuntimeRequest) ([]string, error)
}

// RuntimeDiagnoser reads a runtime's state and recent output without changing
// it. A failed readiness gate used to leave the operator with "could not
// connect" and nothing else, because compensation removed the candidate before
// anyone could read why it never listened. Diagnostics are captured first.
type RuntimeDiagnoser interface {
	DiagnoseRuntime(context.Context, ReleaseRuntime) (RuntimeDiagnostics, error)
}

type RuntimeDiagnostics struct {
	Containers []ContainerDiagnostics `json:"containers"`
}

type ContainerDiagnostics struct {
	ID           string           `json:"id"`
	Name         string           `json:"name"`
	State        string           `json:"state"`
	ExitCode     int              `json:"exitCode"`
	OOMKilled    bool             `json:"oomKilled,omitempty"`
	RestartCount int              `json:"restartCount,omitempty"`
	Error        string           `json:"error,omitempty"`
	Lines        []RuntimeLogLine `json:"-"`
	Truncated    bool             `json:"truncated,omitempty"`
	// Listening is what a running container listens on, read from its own
	// /proc/net/tcp.
	Listening []ListeningSocket `json:"-"`
}

type RuntimeLogLine struct {
	Stream string
	Text   string
}

// Bounds for captured application output. Enough to show a stack trace or a
// missing-variable complaint, small enough that a chatty process cannot turn a
// failed gate into a multi-megabyte transcript.
const (
	diagnosticLogLines      = 200
	diagnosticLogBytes      = 32 << 10
	diagnosticContainers    = 5
	diagnosticReadDeadline  = 8 * time.Second
	diagnosticLineMaxLength = 2000
)

type RuntimeNetworkOwner interface {
	NetworksForRuntime(context.Context, int64, RuntimePlanConfig, map[string]string) ([]string, error)
	RemoveRuntimeNetworks(context.Context, int64) error
}

type dockerReleaseRuntimeMetadata struct {
	Version            int      `json:"version"`
	Strategy           string   `json:"strategy"`
	Image              string   `json:"image,omitempty"`
	ImageDigest        string   `json:"imageDigest,omitempty"`
	ConfigDigest       string   `json:"configDigest,omitempty"`
	PortLeaseToken     string   `json:"portLeaseToken,omitempty"`
	ProjectName        string   `json:"projectName,omitempty"`
	ProjectDirectory   string   `json:"projectDirectory,omitempty"`
	ComposeFiles       []string `json:"composeFiles,omitempty"`
	OverrideFile       string   `json:"overrideFile,omitempty"`
	PrimaryContainerID string   `json:"primaryContainerId,omitempty"`
	ContainerIDs       []string `json:"containerIds,omitempty"`
	VariableNames      []string `json:"variableNames"`
}

// DockerRuntimeOwner is the only deployment adapter allowed to own runtime
// containers. It consumes immutable release snapshots and never resolves a
// mutable tag during activation.
type DockerRuntimeOwner struct {
	client   *dockerx.Client
	networks RuntimeNetworkOwner
}

func NewDockerRuntimeOwner(client *dockerx.Client) *DockerRuntimeOwner {
	return &DockerRuntimeOwner{client: client}
}

func (o *DockerRuntimeOwner) WithNetworks(owner RuntimeNetworkOwner) *DockerRuntimeOwner {
	o.networks = owner
	return o
}

// DiagnoseRuntime inspects every container the runtime record names and reads
// a bounded tail of its output. It never stops, starts or removes anything.
func (o *DockerRuntimeOwner) DiagnoseRuntime(ctx context.Context, runtime ReleaseRuntime) (RuntimeDiagnostics, error) {
	if o.client == nil {
		return RuntimeDiagnostics{}, fmt.Errorf("docker runtime owner is unavailable")
	}
	ids := runtimeContainerIDs(runtime)
	if len(ids) == 0 {
		return RuntimeDiagnostics{}, fmt.Errorf("runtime %q names no container", runtime.RuntimeID)
	}
	if len(ids) > diagnosticContainers {
		ids = ids[:diagnosticContainers]
	}
	result := RuntimeDiagnostics{Containers: make([]ContainerDiagnostics, 0, len(ids))}
	var firstErr error
	for _, id := range ids {
		detail, err := o.client.Inspect(ctx, id)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		item := ContainerDiagnostics{
			ID: detail.ID, Name: detail.Name, State: detail.State, ExitCode: detail.ExitCode,
			OOMKilled: detail.OOMKilled, RestartCount: detail.RestartNum, Error: detail.Error,
		}
		item.Lines, item.Truncated = o.tailContainerLogs(ctx, id)
		if item.State == "running" {
			item.Listening = o.listeningSockets(ctx, id)
		}
		result.Containers = append(result.Containers, item)
	}
	if len(result.Containers) == 0 && firstErr != nil {
		return result, firstErr
	}
	return result, nil
}

func (o *DockerRuntimeOwner) tailContainerLogs(ctx context.Context, id string) ([]RuntimeLogLine, bool) {
	readCtx, cancel := context.WithTimeout(ctx, diagnosticReadDeadline)
	defer cancel()
	lines, closer, err := o.client.Logs(readCtx, id, dockerx.LogOptions{Tail: strconv.Itoa(diagnosticLogLines)})
	if err != nil {
		return nil, false
	}
	defer closer.Close()
	collected := make([]RuntimeLogLine, 0, 64)
	total := 0
	truncated := false
	for {
		select {
		case <-readCtx.Done():
			return collected, truncated
		case line, ok := <-lines:
			if !ok {
				return collected, truncated
			}
			text := line.Text
			if len(text) > diagnosticLineMaxLength {
				text = text[:diagnosticLineMaxLength] + "…"
			}
			total += len(text)
			if total > diagnosticLogBytes {
				// Keep the most recent output: a startup crash prints its
				// reason last, after whatever banner came first.
				for total > diagnosticLogBytes && len(collected) > 0 {
					total -= len(collected[0].Text)
					collected = collected[1:]
				}
				truncated = true
			}
			collected = append(collected, RuntimeLogLine{Stream: line.Stream, Text: text})
		}
	}
}

// runtimeContainerIDs lists the containers a runtime record names, most
// important first: the primary container, then the rest of a Compose stack.
func runtimeContainerIDs(runtime ReleaseRuntime) []string {
	var metadata dockerReleaseRuntimeMetadata
	_ = json.Unmarshal(runtime.Metadata, &metadata)
	seen := map[string]bool{}
	ids := []string{}
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		ids = append(ids, id)
	}
	add(metadata.PrimaryContainerID)
	for _, id := range metadata.ContainerIDs {
		add(id)
	}
	if runtime.Kind == "container" {
		add(runtime.RuntimeID)
	}
	return ids
}

func (o *DockerRuntimeOwner) PersistentSources(ctx context.Context, request CandidateRuntimeRequest) ([]string, error) {
	if o == nil || o.client == nil {
		return nil, ErrRuntimeUnavailable
	}
	if request.Snapshot.Compose == nil {
		return nil, fmt.Errorf("%w: Compose storage snapshot is missing", ErrInvalidPlan)
	}
	return o.client.ComposePersistentSources(ctx, dockerx.ComposeReleaseSpec{
		ProjectName:      fmt.Sprintf("jd-e%d", request.Release.EnvironmentID),
		ProjectDirectory: request.SourceRoot, Files: request.Snapshot.Compose.Files,
		Environment: request.RuntimeVariables,
	})
}

func (o *DockerRuntimeOwner) StartCandidate(
	ctx context.Context,
	request CandidateRuntimeRequest,
	emit func(BuildLog) error,
) (StartedRuntime, error) {
	if o == nil || o.client == nil {
		return StartedRuntime{}, ErrRuntimeUnavailable
	}
	if request.Release.ID <= 0 || request.Release.EnvironmentID != request.Run.EnvironmentID ||
		request.Release.RunID != request.Run.ID {
		return StartedRuntime{}, fmt.Errorf("%w: candidate release identity is inconsistent", ErrInvalidPlan)
	}
	if o.networks != nil {
		var err error
		request.Networks, err = o.networks.NetworksForRuntime(ctx, request.Release.EnvironmentID, request.Snapshot.Plan, request.RuntimeVariables)
		if err != nil {
			return StartedRuntime{}, err
		}
	}
	if request.Snapshot.Compose != nil {
		return o.startCompose(ctx, request, emit)
	}
	return o.startContainer(ctx, request)
}

// runtimePortMappings publishes the routed port on the leased or fixed host
// port and every additional published port on the number the plan pins. Host
// networking publishes nothing: the container already owns the host's ports.
func runtimePortMappings(plan RuntimePlanConfig, host string, port int) []dockerx.PortMapping {
	ports := []dockerx.PortMapping{}
	if plan.HostNetwork {
		return ports
	}
	if plan.InternalPort > 0 && port > 0 {
		ports = append(ports, dockerx.PortMapping{
			HostIP: host, HostPort: port, ContainerPort: plan.InternalPort, Protocol: "tcp",
		})
	}
	for _, published := range plan.Ports {
		ports = append(ports, dockerx.PortMapping{
			HostIP: published.BindAddress, HostPort: published.HostPort,
			ContainerPort: published.ContainerPort, Protocol: published.effectiveProtocol(),
		})
	}
	return ports
}

// PORT is derived from the frozen runtime plan, not a host publication that
// may move. Explicit runtime variables remain authoritative.
func containerRuntimeEnvironment(plan RuntimePlanConfig, variables map[string]string) ([]dockerx.EnvVar, []string) {
	names := make([]string, 0, len(variables))
	for name := range variables {
		names = append(names, name)
	}
	sort.Strings(names)
	environment := make([]dockerx.EnvVar, 0, len(names)+1)
	for _, name := range names {
		environment = append(environment, dockerx.EnvVar{Name: name, Value: variables[name]})
	}
	if _, explicit := variables["PORT"]; !explicit && plan.InternalPort > 0 && !plan.HostNetwork {
		environment = append(environment, dockerx.EnvVar{Name: "PORT", Value: strconv.Itoa(plan.InternalPort)})
	}
	return environment, names
}

func (o *DockerRuntimeOwner) startContainer(
	ctx context.Context,
	request CandidateRuntimeRequest,
) (StartedRuntime, error) {
	image := immutableRuntimeImage(request.Snapshot.Image)
	if image == "" {
		return StartedRuntime{}, fmt.Errorf("%w: candidate has no immutable image", ErrArtifactMissing)
	}
	plan := request.Snapshot.Plan
	environment, variableNames := containerRuntimeEnvironment(plan, request.RuntimeVariables)
	environment = append(environment, withdrawnProxyTrust(request.Snapshot, request.RuntimeVariables)...)
	mounts := make([]dockerx.MountSpec, 0, len(plan.Mounts))
	for _, planned := range plan.Mounts {
		kind := "volume"
		if filepath.IsAbs(planned.Source) {
			kind = "bind"
		}
		mounts = append(mounts, dockerx.MountSpec{
			Type: kind, Source: planned.Source, Target: planned.Target, ReadOnly: planned.ReadOnly,
		})
	}
	devices := make([]dockerx.DeviceSpec, 0, len(plan.Devices))
	for _, device := range plan.Devices {
		devices = append(devices, dockerx.DeviceSpec{Host: device, Container: device, Permissions: "rwm"})
	}
	ports := runtimePortMappings(plan, request.Host, request.Port)
	labels := releaseRuntimeLabels(request)
	networks := append([]string(nil), request.Networks...)
	if plan.PreviewIsolation {
		network, err := o.ensurePreviewResources(ctx, request.Release.EnvironmentID, plan)
		if err != nil {
			return StartedRuntime{}, err
		}
		networks = append([]string{network}, networks...)
		labels = append(labels, dockerx.LabelSpec{Name: "io.just-dashboard.preview", Value: "true"})
	}
	name := fmt.Sprintf("jd-e%d-r%d", request.Release.EnvironmentID, request.Release.Number)
	stopSignal := plan.StopSignal
	if stopSignal == "" {
		stopSignal = "SIGTERM"
	}
	result, err := o.client.Create(ctx, dockerx.ContainerSpec{
		Name: name, Image: image, Command: append([]string(nil), plan.Command...), Env: environment,
		Ports: ports, Mounts: mounts, Devices: devices, Labels: labels, Networks: networks,
		NetworkMode:   map[bool]string{true: "host"}[plan.HostNetwork],
		RestartPolicy: plan.EffectiveRestartPolicy(), Logging: dockerx.CappedLogging(), StopSignal: stopSignal,
		Limits:     dockerx.ResourceLimits{MemoryMB: plan.MemoryMB, CPUs: plan.CPUs, PidsLimit: plan.PidsLimit},
		Privileged: plan.Privileged, CapAdd: append([]string(nil), plan.Capabilities...), Init: true,
		Pull: "missing", Start: true,
	}, nil)
	if err != nil {
		return StartedRuntime{}, err
	}
	for _, binding := range result.Ports {
		if binding.ContainerPort == plan.InternalPort && binding.Protocol == "tcp" {
			request.Host, request.Port = binding.HostIP, binding.HostPort
			break
		}
	}
	metadata := dockerReleaseRuntimeMetadata{
		Version: 1, Strategy: string(plan.Strategy), Image: image,
		ImageDigest: request.Snapshot.Image.Digest, ConfigDigest: request.Snapshot.Image.ConfigDigest,
		PortLeaseToken: request.PortLeaseToken, PrimaryContainerID: result.ID,
		VariableNames: variableNames,
	}
	return StartedRuntime{
		Input: ReleaseRuntimeInput{
			ReleaseID: request.Release.ID, Kind: "container", RuntimeID: result.ID, Name: result.Name,
			Host: request.Host, Port: request.Port, Metadata: mustJSON(metadata),
		},
		Target: CheckTarget{ContainerID: result.ID, Host: runtimeCheckHost(request.Host), Port: request.Port},
	}, nil
}

func (o *DockerRuntimeOwner) startCompose(
	ctx context.Context,
	request CandidateRuntimeRequest,
	emit func(BuildLog) error,
) (StartedRuntime, error) {
	resolved := request.Snapshot.Compose
	if resolved == nil || len(resolved.Services) == 0 || request.SourceRoot == "" {
		return StartedRuntime{}, fmt.Errorf("%w: Compose runtime snapshot is incomplete", ErrArtifactMissing)
	}
	project := fmt.Sprintf("jd-e%d", request.Release.EnvironmentID)
	override := filepath.Join(request.SourceRoot, ".just-dashboard", "release.yml")
	content, err := renderComposeReleaseOverride(request)
	if err != nil {
		return StartedRuntime{}, err
	}
	if len(request.Networks) > 0 {
		content, err = o.client.ComposeRuntimeNetworks(ctx, dockerx.ComposeReleaseSpec{ProjectName: project, ProjectDirectory: request.SourceRoot, Files: resolved.Files, Environment: request.RuntimeVariables}, content, request.Networks)
		if err != nil {
			return StartedRuntime{}, err
		}
	}
	if err := writeImmutableRuntimeFile(request.SourceRoot, ".just-dashboard/release.yml", content); err != nil {
		return StartedRuntime{}, err
	}
	spec := dockerx.ComposeReleaseSpec{
		ProjectName: project, ProjectDirectory: request.SourceRoot,
		Files: append([]string(nil), resolved.Files...), OverrideFile: override,
		Environment: request.RuntimeVariables,
	}
	if err := o.client.RunComposeRelease(ctx, spec, dockerx.ComposeReleaseUp, runtimeGrace(request.Snapshot.Plan), composeBuildEmitter(emit)); err != nil {
		return StartedRuntime{}, err
	}
	primaryService := resolved.Services[0].Plan.Name
	containers, err := o.client.ListContainersWithLabels(ctx, map[string]string{
		"io.just-dashboard.managed":        "true",
		"io.just-dashboard.environment-id": strconv.FormatInt(request.Release.EnvironmentID, 10),
		"io.just-dashboard.release-id":     strconv.FormatInt(request.Release.ID, 10),
	})
	if err != nil {
		return StartedRuntime{}, err
	}
	containerIDs, primaryID := composeRuntimeIdentities(containers, request.Release.EnvironmentID, request.Release.ID, project, primaryService)
	if primaryID == "" {
		return StartedRuntime{}, fmt.Errorf("%w: Compose primary service was not created", ErrRuntimeUnavailable)
	}
	for _, candidate := range containers {
		if candidate.ID != primaryID {
			continue
		}
		for _, port := range candidate.Ports {
			if int(port.PrivatePort) == request.Snapshot.Plan.InternalPort && port.Type == "tcp" && port.PublicPort > 0 {
				request.Host, request.Port = port.IP, int(port.PublicPort)
				break
			}
		}
	}
	variableNames := make([]string, 0, len(request.RuntimeVariables))
	for name := range request.RuntimeVariables {
		variableNames = append(variableNames, name)
	}
	sort.Strings(variableNames)
	metadata := dockerReleaseRuntimeMetadata{
		Version: 1, Strategy: string(request.Snapshot.Plan.Strategy), PortLeaseToken: request.PortLeaseToken,
		ProjectName: project, ProjectDirectory: request.SourceRoot,
		ComposeFiles: append([]string(nil), resolved.Files...), OverrideFile: override,
		PrimaryContainerID: primaryID, ContainerIDs: containerIDs, VariableNames: variableNames,
	}
	return StartedRuntime{
		Input: ReleaseRuntimeInput{
			ReleaseID: request.Release.ID, Kind: "compose", RuntimeID: project, Name: project,
			WorkingDirectory: request.SourceRoot, Host: request.Host, Port: request.Port, Metadata: mustJSON(metadata),
		},
		Target: CheckTarget{ContainerID: primaryID, Host: runtimeCheckHost(request.Host), Port: request.Port},
	}, nil
}

func composeRuntimeIdentities(containers []dockerx.Container, environmentID, releaseID int64, project, primaryService string) ([]string, string) {
	ids := []string{}
	primaryIDs := []string{}
	seen := map[string]bool{}
	for _, container := range containers {
		labels := container.Labels
		if container.ID == "" || seen[container.ID] || labels["io.just-dashboard.managed"] != "true" ||
			labels["io.just-dashboard.environment-id"] != strconv.FormatInt(environmentID, 10) ||
			labels["io.just-dashboard.release-id"] != strconv.FormatInt(releaseID, 10) ||
			labels["com.docker.compose.project"] != project || strings.EqualFold(labels["com.docker.compose.oneoff"], "true") {
			continue
		}
		seen[container.ID] = true
		ids = append(ids, container.ID)
		if labels["com.docker.compose.service"] == primaryService {
			primaryIDs = append(primaryIDs, container.ID)
		}
	}
	sort.Strings(ids)
	sort.Strings(primaryIDs)
	if len(primaryIDs) == 0 {
		return ids, ""
	}
	return ids, primaryIDs[0]
}

func immutableRuntimeImage(image ResolvedImage) string {
	if contentDigestRE.MatchString(image.ConfigDigest) {
		return image.ConfigDigest
	}
	if image.Reference != "" && contentDigestRE.MatchString(image.Digest) {
		return strings.Split(image.Reference, "@")[0] + "@" + image.Digest
	}
	return ""
}

func releaseRuntimeLabels(request CandidateRuntimeRequest) []dockerx.LabelSpec {
	return []dockerx.LabelSpec{
		{Name: "io.just-dashboard.managed", Value: "true"},
		{Name: "io.just-dashboard.environment-id", Value: strconv.FormatInt(request.Release.EnvironmentID, 10)},
		{Name: "io.just-dashboard.release-id", Value: strconv.FormatInt(request.Release.ID, 10)},
		{Name: "io.just-dashboard.release-number", Value: strconv.FormatInt(request.Release.Number, 10)},
		{Name: "io.just-dashboard.run-id", Value: strconv.FormatInt(request.Run.ID, 10)},
	}
}

func renderComposeReleaseOverride(request CandidateRuntimeRequest) (string, error) {
	if request.Snapshot.Compose == nil {
		return "", ErrArtifactMissing
	}
	var output strings.Builder
	output.WriteString("# Generated by Just Dashboard from immutable release artifacts.\nservices:\n")
	seen := map[string]bool{}
	for _, service := range request.Snapshot.Compose.Services {
		if service.Plan.Name == "" || strings.ContainsAny(service.Plan.Name, "\x00\r\n:") || seen[service.Plan.Name] {
			return "", fmt.Errorf("%w: invalid Compose service identity", ErrInvalidPlan)
		}
		seen[service.Plan.Name] = true
		image := immutableRuntimeImage(ResolvedImage{
			Reference: service.Reference, Digest: service.Digest, ConfigDigest: service.ConfigDigest,
		})
		if image == "" {
			return "", fmt.Errorf("%w: Compose service %s has no immutable image", ErrArtifactMissing, service.Plan.Name)
		}
		output.WriteString("  " + service.Plan.Name + ":\n")
		output.WriteString("    image: " + strconv.Quote(image) + "\n")
		output.WriteString("    pull_policy: never\n")
		output.WriteString("    labels:\n")
		for _, label := range releaseRuntimeLabels(request) {
			output.WriteString("      " + label.Name + ": " + strconv.Quote(label.Value) + "\n")
		}
	}
	return output.String(), nil
}

func writeImmutableRuntimeFile(root, relative, content string) error {
	if !safeRelativePath(relative) {
		return fmt.Errorf("%w: runtime override escapes its source", ErrInvalidPlan)
	}
	directory, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer directory.Close()
	if err := directory.MkdirAll(filepath.Dir(relative), 0o700); err != nil {
		return err
	}
	// A checkout can contain symlinks. Publish only complete bytes within its
	// root, and never replace an override already retained for a release.
	temporary := filepath.Join(filepath.Dir(relative), ".runtime-"+rand.Text())
	file, err := directory.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer directory.Remove(temporary)
	_, writeErr := file.WriteString(content)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	if err := directory.Link(temporary, relative); err == nil {
		return nil
	} else if !os.IsExist(err) {
		return err
	}
	existing, err := directory.OpenFile(relative, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer existing.Close()
	info, err := existing.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != int64(len(content)) {
		return fmt.Errorf("%w: runtime override already contains different bytes", ErrInvalidPlan)
	}
	bytes, err := io.ReadAll(io.LimitReader(existing, int64(len(content))+1))
	if err == nil {
		if string(bytes) != content {
			return fmt.Errorf("%w: runtime override already contains different bytes", ErrInvalidPlan)
		}
	}
	return err
}

func (o *DockerRuntimeOwner) StartExisting(
	ctx context.Context,
	runtime ReleaseRuntime,
	variables map[string]string,
	emit func(BuildLog) error,
) error {
	if runtime.State == "quarantined" {
		return ErrPreviewIsolation
	}
	if o == nil || o.client == nil {
		return ErrRuntimeUnavailable
	}
	if o.networks != nil {
		networks, err := o.networks.NetworksForRuntime(ctx, runtime.EnvironmentID, RuntimePlanConfig{}, variables)
		if err != nil {
			return err
		}
		if runtime.Kind == "container" && len(networks) > 0 {
			detail, err := o.client.Inspect(ctx, runtime.RuntimeID)
			if err != nil {
				return err
			}
			if detail.Labels["io.just-dashboard.managed"] != "true" || detail.Labels["io.just-dashboard.environment-id"] != strconv.FormatInt(runtime.EnvironmentID, 10) {
				return fmt.Errorf("%w: runtime network ownership changed", ErrInvalidPlan)
			}
			for _, name := range networks {
				attached := false
				for _, endpoint := range detail.NetworkList {
					if endpoint.Name == name {
						attached = true
					}
				}
				if !attached {
					if err := o.client.ConnectNetwork(ctx, name, runtime.RuntimeID, nil); err != nil {
						return err
					}
				}
			}
		}
	}
	switch runtime.Kind {
	case "container":
		err := o.client.Lifecycle(ctx, runtime.RuntimeID, dockerx.ActionStart, nil)
		if errdefs.IsNotModified(err) {
			return nil
		}
		return err
	case "compose":
		metadata, err := decodeDockerRuntimeMetadata(runtime.Metadata)
		if err != nil {
			return err
		}
		return o.client.RunComposeRelease(ctx, composeSpecFromMetadata(metadata, variables),
			dockerx.ComposeReleaseUp, 0, composeBuildEmitter(emit))
	default:
		return ErrRuntimeUnavailable
	}
}

func (o *DockerRuntimeOwner) Stop(
	ctx context.Context,
	runtime ReleaseRuntime,
	plan RuntimePlanConfig,
	variables map[string]string,
	remove bool,
	emit func(BuildLog) error,
) (RuntimeStopEvidence, error) {
	evidence := RuntimeStopEvidence{
		RuntimeID: runtime.RuntimeID, Signal: plan.StopSignal, GraceSeconds: runtimeGrace(plan), StartedAt: time.Now().UTC(),
	}
	if evidence.Signal == "" {
		evidence.Signal = "SIGTERM"
	}
	if o == nil || o.client == nil {
		return evidence, ErrRuntimeUnavailable
	}
	grace := evidence.GraceSeconds
	var err error
	switch runtime.Kind {
	case "container":
		err = o.client.Lifecycle(ctx, runtime.RuntimeID, dockerx.ActionStop, &grace)
		if errdefs.IsNotFound(err) {
			evidence.Removed = remove
			err = nil
			break
		}
		if err == nil {
			if detail, inspectErr := o.client.Inspect(ctx, runtime.RuntimeID); inspectErr == nil {
				evidence.Forced = detail.ExitCode == 137
			}
		}
		if err == nil && remove {
			err = o.client.RemoveContainer(ctx, runtime.RuntimeID, false, false)
			if errdefs.IsNotFound(err) {
				err = nil
			}
			evidence.Removed = err == nil
		}
	case "compose":
		metadata, decodeErr := decodeDockerRuntimeMetadata(runtime.Metadata)
		if decodeErr != nil {
			err = decodeErr
			break
		}
		spec := composeSpecFromMetadata(metadata, variables)
		action := dockerx.ComposeReleaseStop
		if remove {
			action = dockerx.ComposeReleaseDown
		}
		err = o.client.RunComposeRelease(ctx, spec, action, grace, composeBuildEmitter(emit))
		if err != nil {
			killCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			killErr := o.client.RunComposeRelease(killCtx, spec, dockerx.ComposeReleaseKill, 0, composeBuildEmitter(emit))
			cancel()
			if killErr == nil {
				evidence.Forced = true
				err = nil
			}
		}
		evidence.Removed = remove && err == nil
	default:
		err = ErrRuntimeUnavailable
	}
	evidence.CompletedAt = time.Now().UTC()
	return evidence, err
}

func runtimeGrace(plan RuntimePlanConfig) int {
	if plan.GracePeriodSeconds > 0 {
		return plan.GracePeriodSeconds
	}
	return 10
}

func decodeDockerRuntimeMetadata(raw json.RawMessage) (dockerReleaseRuntimeMetadata, error) {
	var metadata dockerReleaseRuntimeMetadata
	if err := json.Unmarshal(raw, &metadata); err != nil || metadata.Version != 1 {
		return metadata, fmt.Errorf("%w: release runtime metadata is malformed", ErrArtifactMissing)
	}
	return metadata, nil
}

func composeSpecFromMetadata(metadata dockerReleaseRuntimeMetadata, variables map[string]string) dockerx.ComposeReleaseSpec {
	return dockerx.ComposeReleaseSpec{
		ProjectName: metadata.ProjectName, ProjectDirectory: metadata.ProjectDirectory,
		Files: append([]string(nil), metadata.ComposeFiles...), OverrideFile: metadata.OverrideFile,
		Environment: variables,
	}
}

func composeBuildEmitter(emit func(BuildLog) error) func(dockerx.LogLine) error {
	if emit == nil {
		return nil
	}
	return func(line dockerx.LogLine) error { return emit(BuildLog{Stream: line.Stream, Text: line.Text}) }
}
