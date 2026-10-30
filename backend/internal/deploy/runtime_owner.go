package deploy

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
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
	// ReplicaPorts are the loopback ports leased for the release's second
	// and later replicas, in order; Port is the first replica's.
	ReplicaPorts []int
	Networks     []string
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
	// Replicas are a replicated release's containers, first replica first,
	// each with its own loopback publication. A release of one container
	// records none and is its RuntimeID, as it always was.
	Replicas []runtimeReplica `json:"replicas,omitempty"`
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

func (o *DockerRuntimeOwner) ListContainersWithLabels(ctx context.Context, labels map[string]string) ([]dockerx.Container, error) {
	if o.client == nil {
		return nil, ErrRuntimeUnavailable
	}
	return o.client.ListContainersWithLabels(ctx, labels)
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
	return o.startContainer(ctx, request, emit)
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
		// A UDP primary is a fixed publication on the plan's bind address,
		// never a leased loopback port: validation allows nothing else.
		for _, protocol := range plan.publishedProtocols() {
			ports = append(ports, dockerx.PortMapping{
				HostIP: host, HostPort: port, ContainerPort: plan.InternalPort, Protocol: protocol,
			})
		}
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

// runtimeMountSpecs are the plan's mounts as the container runtime takes
// them: a path is a bind of that host directory, anything else a named
// volume; then its writable tmpfs paths.
func runtimeMountSpecs(plan RuntimePlanConfig) []dockerx.MountSpec {
	mounts := make([]dockerx.MountSpec, 0, len(plan.Mounts)+len(plan.Tmpfs))
	for _, planned := range plan.Mounts {
		kind := "volume"
		if filepath.IsAbs(planned.Source) {
			kind = "bind"
		}
		mounts = append(mounts, dockerx.MountSpec{
			Type: kind, Source: planned.Source, Target: planned.Target, ReadOnly: planned.ReadOnly,
		})
	}
	return append(mounts, plan.tmpfsMountSpecs()...)
}

func (o *DockerRuntimeOwner) startContainer(
	ctx context.Context,
	request CandidateRuntimeRequest,
	emit func(BuildLog) error,
) (StartedRuntime, error) {
	image := immutableRuntimeImage(request.Snapshot.Image)
	if image == "" {
		return StartedRuntime{}, fmt.Errorf("%w: candidate has no immutable image", ErrArtifactMissing)
	}
	plan := request.Snapshot.Plan
	// Install files and pinned artifacts are in the volume before the
	// container that reads them exists, written by helpers that mount only
	// that volume (runtime_seeds.go).
	if err := materializeSeeds(ctx, o.client, request, image, emit); err != nil {
		return StartedRuntime{}, err
	}
	environment, variableNames := containerRuntimeEnvironment(plan, request.RuntimeVariables)
	environment = append(environment, withdrawnProxyTrust(request.Snapshot, request.RuntimeVariables)...)
	environment = append(environment, webConcurrencyEnvironment(request.Snapshot, request.RuntimeVariables)...)
	mounts := runtimeMountSpecs(plan)
	devices := make([]dockerx.DeviceSpec, 0, len(plan.Devices))
	for _, device := range plan.Devices {
		devices = append(devices, dockerx.DeviceSpec{Host: device, Container: device, Permissions: "rwm"})
	}
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
	stopSignal := plan.StopSignal
	if stopSignal == "" {
		stopSignal = "SIGTERM"
	}
	count := plan.EffectiveReplicas()
	if count > 1 && len(request.ReplicaPorts) != count-1 && plan.InternalPort > 0 {
		return StartedRuntime{}, fmt.Errorf("%w: %d replicas need %d more candidate ports", ErrInvalidPlan, count, count-1)
	}
	// Every replica is the same container from the same frozen snapshot,
	// apart from its name, its loopback port and the label saying which one
	// it is; each has the plan's limits of its own.
	replicas := make([]runtimeReplica, 0, count)
	for index := 1; index <= count; index++ {
		host, port := request.Host, request.Port
		if index > 1 && plan.InternalPort > 0 {
			port = request.ReplicaPorts[index-2]
		}
		result, err := o.client.Create(ctx, dockerx.ContainerSpec{
			Name:  replicaContainerName(request.Release.EnvironmentID, request.Release.Number, index),
			Image: image, Command: append([]string(nil), plan.Command...), Env: environment,
			Ports: runtimePortMappings(plan, host, port), Mounts: mounts, Devices: devices,
			Labels: append(append([]dockerx.LabelSpec(nil), labels...), replicaLabels(index, count)...), Networks: networks,
			NetworkMode:   map[bool]string{true: "host"}[plan.HostNetwork],
			RestartPolicy: plan.EffectiveRestartPolicy(), Logging: dockerx.CappedLogging(), StopSignal: stopSignal,
			Limits:     dockerx.ResourceLimits{MemoryMB: plan.MemoryMB, CPUs: plan.CPUs, PidsLimit: plan.PidsLimit},
			Privileged: plan.Privileged, CapAdd: append([]string(nil), plan.Capabilities...), Init: true,
			User: plan.User, ReadOnly: plan.ReadOnlyRootfs, Ulimits: plan.dockerUlimits(),
			Sysctls: plan.dockerSysctls(), GPUs: plan.dockerGPUs(),
			Pull: "missing", Start: true,
		}, nil)
		if err != nil {
			// A release is all of its replicas or none: the ones already
			// started are removed, so a failed start leaves nothing behind
			// for the compensation that follows to miss — the one the daemon
			// created and then would not start included, as a GPU, a limit
			// or a kernel parameter it refuses is refused only at start.
			if result != nil && result.ID != "" {
				replicas = append(replicas, runtimeReplica{Index: index, ContainerID: result.ID})
			}
			o.removeReplicas(ctx, replicas)
			return StartedRuntime{}, err
		}
		for _, binding := range result.Ports {
			if binding.ContainerPort == plan.InternalPort && binding.Protocol == plan.effectiveProtocol() {
				host, port = binding.HostIP, binding.HostPort
				break
			}
		}
		replicas = append(replicas, runtimeReplica{Index: index, ContainerID: result.ID, Name: result.Name, Host: host, Port: port})
	}
	primary := replicas[0]
	metadata := dockerReleaseRuntimeMetadata{
		Version: 1, Strategy: string(plan.Strategy), Image: image,
		ImageDigest: request.Snapshot.Image.Digest, ConfigDigest: request.Snapshot.Image.ConfigDigest,
		PortLeaseToken: request.PortLeaseToken, PrimaryContainerID: primary.ContainerID,
		VariableNames: variableNames,
	}
	if count > 1 {
		metadata.Replicas = replicas
		for _, replica := range replicas {
			metadata.ContainerIDs = append(metadata.ContainerIDs, replica.ContainerID)
		}
	}
	return StartedRuntime{
		Input: ReleaseRuntimeInput{
			ReleaseID: request.Release.ID, Kind: "container", RuntimeID: primary.ContainerID, Name: primary.Name,
			Host: primary.Host, Port: primary.Port, Metadata: mustJSON(metadata),
		},
		Target: CheckTarget{ContainerID: primary.ContainerID, Host: runtimeCheckHost(primary.Host), Port: primary.Port},
	}, nil
}

// removeReplicas force-removes the replicas a failed start had already
// created, bounded and detached from the request that failed.
func (o *DockerRuntimeOwner) removeReplicas(ctx context.Context, replicas []runtimeReplica) {
	if len(replicas) == 0 {
		return
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	for _, replica := range replicas {
		_ = o.client.RemoveContainer(cleanup, replica.ContainerID, true, false)
	}
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
	primaryService := resolved.Services[0].Plan.Name
	if resolved.PrimaryService != "" {
		primaryService = resolved.PrimaryService
	}
	overrideName := ".just-dashboard/release.yml"
	content, err := renderComposeReleaseOverride(request)
	if err != nil {
		return StartedRuntime{}, err
	}
	resolvedSpec := dockerx.ComposeReleaseSpec{ProjectName: project, ProjectDirectory: request.SourceRoot, Files: resolved.Files, Environment: request.RuntimeVariables}
	if request.Snapshot.Plan.PreviewIsolation {
		content, overrideName, err = o.composePreviewRelease(ctx, request, resolvedSpec, primaryService, content, emit)
		if err != nil {
			return StartedRuntime{}, err
		}
	} else if len(request.Networks) > 0 {
		content, err = o.client.ComposeRuntimeNetworks(ctx, resolvedSpec, content, request.Networks)
		if err != nil {
			return StartedRuntime{}, err
		}
	}
	override := filepath.Join(request.SourceRoot, filepath.FromSlash(overrideName))
	if err := writeImmutableRuntimeFile(request.SourceRoot, overrideName, content); err != nil {
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

// composePreviewRelease isolates every service of a preview's stack the way
// one preview container is: on the preview's own bridge and its database
// links' networks alone, every mount on a volume of the preview's own (a
// read-only file of the commit itself excepted), no secret, no fixed name,
// and no port on the host but the public service's loopback one, which the
// preview's route and tailnet address reach. The override names its port,
// so it is a file of its own per port: a retried start on another lease must
// not meet the immutable bytes of the first.
func (o *DockerRuntimeOwner) composePreviewRelease(
	ctx context.Context,
	request CandidateRuntimeRequest,
	spec dockerx.ComposeReleaseSpec,
	public, content string,
	emit func(BuildLog) error,
) (string, string, error) {
	environmentID := request.Release.EnvironmentID
	plan := request.Snapshot.Plan
	if err := validatePreviewPlan(environmentID, BuildPlanConfig{Method: BuildCompose}, plan); err != nil {
		return "", "", err
	}
	host := net.ParseIP(request.Host)
	if plan.InternalPort <= 0 || request.Port <= 0 || host == nil || !host.IsLoopback() {
		return "", "", fmt.Errorf("%w: a Compose preview publishes its public service on loopback only", ErrPreviewIsolation)
	}
	// Before Compose reads anything: the pull request's files, not
	// production's, are what runs.
	if err := composePreviewSources(spec.ProjectDirectory, spec.Files); err != nil {
		return "", "", err
	}
	network, err := o.ensurePreviewNetwork(ctx, environmentID)
	if err != nil {
		return "", "", err
	}
	isolated, err := o.client.ComposePreviewOverride(ctx, spec, content, dockerx.ComposePreviewIsolation{
		Network: network, Networks: request.Networks, Public: public,
		Publish: dockerx.PortMapping{HostIP: request.Host, HostPort: request.Port, ContainerPort: plan.InternalPort, Protocol: "tcp"},
		Volume:  func(key string) string { return previewVolumeName(environmentID, "compose:"+key) },
		Labels:  map[string]string{"io.just-dashboard.preview": "true"},
	})
	if errors.Is(err, dockerx.ErrComposePreviewRefused) {
		return "", "", fmt.Errorf("%w: %s", ErrPreviewIsolation, strings.TrimPrefix(err.Error(), dockerx.ErrComposePreviewRefused.Error()+": "))
	}
	if err != nil {
		return "", "", err
	}
	for _, volume := range isolated.Volumes {
		if err := o.ensurePreviewVolume(ctx, environmentID, volume); err != nil {
			return "", "", err
		}
	}
	if emit != nil {
		for _, item := range isolated.LeftOut {
			service, target, _ := strings.Cut(item, ":")
			_ = emit(BuildLog{Stream: "status", Text: fmt.Sprintf("Left out %s of service %s: a preview reads no path of this server", target, service)})
		}
	}
	return isolated.Override, fmt.Sprintf(".just-dashboard/preview-%d.yml", request.Port), nil
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
			for _, id := range runtimeContainerIDs(runtime) {
				if err := o.attachRuntimeNetworks(ctx, runtime.EnvironmentID, id, networks); err != nil {
					return err
				}
			}
		}
	}
	switch runtime.Kind {
	case "container":
		// Every replica, so a release that comes back comes back whole.
		for _, id := range runtimeContainerIDs(runtime) {
			if err := o.client.Lifecycle(ctx, id, dockerx.ActionStart, nil); err != nil && !errdefs.IsNotModified(err) {
				return err
			}
		}
		return nil
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
		// A replicated release's containers are stopped together, each given
		// the whole grace period, so stopping eight takes as long as one.
		ids := runtimeContainerIDs(runtime)
		results := make([]containerStop, len(ids))
		var wait sync.WaitGroup
		for index, id := range ids {
			wait.Add(1)
			go func() {
				defer wait.Done()
				results[index] = o.stopContainer(ctx, id, grace, remove)
			}()
		}
		wait.Wait()
		evidence.Removed = remove
		for _, result := range results {
			evidence.Forced = evidence.Forced || result.forced
			evidence.Removed = evidence.Removed && result.removed
			err = errors.Join(err, result.err)
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

// containerStop is what stopping one container of a release found.
type containerStop struct {
	forced, removed bool
	err             error
}

// stopContainer stops one container with the plan's grace and, when asked,
// removes it. A container already gone is stopped and removed: a candidate
// that crashed must not turn its own cleanup into an error.
func (o *DockerRuntimeOwner) stopContainer(ctx context.Context, id string, grace int, remove bool) containerStop {
	err := o.client.Lifecycle(ctx, id, dockerx.ActionStop, &grace)
	if errdefs.IsNotFound(err) {
		return containerStop{removed: remove}
	}
	if err != nil {
		return containerStop{err: err}
	}
	result := containerStop{}
	if detail, inspectErr := o.client.Inspect(ctx, id); inspectErr == nil {
		result.forced = detail.ExitCode == 137
	}
	if remove {
		err = o.client.RemoveContainer(ctx, id, false, false)
		if errdefs.IsNotFound(err) {
			err = nil
		}
		result.removed, result.err = err == nil, err
	}
	return result
}

// attachRuntimeNetworks joins one of the release's containers to the
// networks it must reach, after checking it is still this environment's.
func (o *DockerRuntimeOwner) attachRuntimeNetworks(ctx context.Context, environmentID int64, id string, networks []string) error {
	detail, err := o.client.Inspect(ctx, id)
	if err != nil {
		return err
	}
	if detail.Labels["io.just-dashboard.managed"] != "true" || detail.Labels["io.just-dashboard.environment-id"] != strconv.FormatInt(environmentID, 10) {
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
			if err := o.client.ConnectNetwork(ctx, name, id, nil); err != nil {
				return err
			}
		}
	}
	return nil
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
