package deploy

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// A release task is how a migration runs once before a release starts —
// Heroku's release phase, Fly's release_command, Render's pre-deploy
// command. The historical runner executed it with /bin/sh in the dashboard's
// own container, over a checkout nobody had built: no node_modules, no
// virtualenv, no Ruby, and no route to a project's database network, so
// `npx prisma migrate deploy` or `python manage.py migrate` could only ever
// fail there. The image runner starts the release's own image once instead,
// with the application's variables, on its database networks, and removes it
// when the command exits.

// ReleaseTaskRuntime is the runtime owner's one-shot container boundary.
type ReleaseTaskRuntime interface {
	RunReleaseTask(context.Context, ReleaseTaskRuntimeRequest, func(BuildLog) error) (int, error)
}

type ReleaseTaskRuntimeRequest struct {
	Run       EngineRun
	Release   Release
	Image     string
	Plan      RuntimePlanConfig
	Task      ReleaseTaskConfig
	Index     int
	Variables map[string]string
	// Secret names the runtime variables whose values the task's output must
	// never show; the task's own release_task values are always redacted.
	Secret map[string]bool
}

// releaseTaskImage is the image a release's tasks run in: the built or pulled
// image, or a Compose release's primary service image.
func releaseTaskImage(snapshot runtimeReleaseSnapshot) string {
	if snapshot.Compose == nil {
		return immutableRuntimeImage(snapshot.Image)
	}
	primary := snapshot.Compose.PrimaryService
	for _, service := range snapshot.Compose.Services {
		if primary == "" || service.Plan.Name == primary {
			return immutableRuntimeImage(ResolvedImage{
				Reference: service.Reference, Digest: service.Digest, ConfigDigest: service.ConfigDigest,
			})
		}
	}
	return ""
}

// releaseTaskArgv runs a command with shell syntax through the image's
// /bin/sh, and anything else directly, so an image without a shell still
// runs `bin/migrate`.
func releaseTaskArgv(command string) (entrypoint, cmd []string) {
	command = strings.TrimSpace(command)
	if strings.ContainsAny(command, "|&;<>()$`\\\"'*?[]#~=%{}\n") {
		return []string{"/bin/sh", "-c"}, []string{command}
	}
	words := strings.Fields(command)
	return words[:1], words[1:]
}

func (o *DockerRuntimeOwner) RunReleaseTask(
	ctx context.Context,
	request ReleaseTaskRuntimeRequest,
	emit func(BuildLog) error,
) (int, error) {
	if o == nil || o.client == nil {
		return -1, ErrRuntimeUnavailable
	}
	if request.Image == "" {
		return -1, fmt.Errorf("%w: the release has no image to run its task in", ErrArtifactMissing)
	}
	plan := request.Plan
	networks := []string{}
	if o.networks != nil {
		var err error
		networks, err = o.networks.NetworksForRuntime(ctx, request.Release.EnvironmentID, plan, request.Variables)
		if err != nil {
			return -1, err
		}
	}
	if plan.PreviewIsolation {
		network, err := o.ensurePreviewResources(ctx, request.Release.EnvironmentID, plan)
		if err != nil {
			return -1, err
		}
		networks = append([]string{network}, networks...)
	}
	names := make([]string, 0, len(request.Variables))
	for name := range request.Variables {
		names = append(names, name)
	}
	sort.Strings(names)
	environment := make([]dockerx.EnvVar, 0, len(names))
	for _, name := range names {
		environment = append(environment, dockerx.EnvVar{Name: name, Value: request.Variables[name]})
	}
	entrypoint, cmd := releaseTaskArgv(request.Task.Command)
	labels := append(releaseRuntimeLabels(CandidateRuntimeRequest{Run: request.Run, Release: request.Release}),
		dockerx.LabelSpec{Name: "io.just-dashboard.release-task", Value: request.Task.Name})
	spec := dockerx.ContainerSpec{
		Name:  fmt.Sprintf("jd-e%d-run%d-task%d", request.Release.EnvironmentID, request.Run.ID, request.Index+1),
		Image: request.Image, Entrypoint: entrypoint, Command: cmd, Env: environment,
		Labels: labels, Networks: networks, Logging: dockerx.CappedLogging(), Init: true,
		Limits: dockerx.ResourceLimits{MemoryMB: plan.MemoryMB, CPUs: plan.CPUs, PidsLimit: plan.PidsLimit},
	}
	if request.Task.WorkingDirectory != "" {
		// A relative directory is relative to the image's own working
		// directory, which only the image knows; the shell resolves it.
		quoted := "'" + strings.ReplaceAll(request.Task.WorkingDirectory, "'", `'\''`) + "'"
		spec.Entrypoint, spec.Command = []string{"/bin/sh", "-c"}, []string{"cd " + quoted + " && " + request.Task.Command}
	}
	lines := make(chan dockerx.LogLine, 128)
	type outcome struct {
		code int
		err  error
	}
	done := make(chan outcome, 1)
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		code, err := o.client.RunToCompletion(child, spec, lines)
		close(lines)
		done <- outcome{code, err}
	}()
	var emitErr error
	for line := range lines {
		if emitErr != nil {
			continue
		}
		text := line.Text
		if len(text) > maxLogTextBytes {
			text = "[output line omitted: exceeds 64 KiB]"
		}
		if emitErr = emit(BuildLog{Stream: line.Stream, Text: text}); emitErr != nil {
			cancel()
		}
	}
	result := <-done
	if emitErr != nil {
		return result.code, fmt.Errorf("persist release task output: %w", emitErr)
	}
	if result.err != nil {
		return result.code, result.err
	}
	if result.code != 0 {
		return result.code, fmt.Errorf("release task exited with code %d", result.code)
	}
	return 0, nil
}

// runImageReleaseTask runs one task in the candidate release's image with
// the runtime variables the release will start with, plus the task's own
// release_task-scoped ones.
func runImageReleaseTask(
	ctx context.Context,
	runner ReleaseTaskRuntime,
	request ReleaseTaskRuntimeRequest,
	releaseValues map[string]string,
	emit func(BuildLog) error,
) (ReleaseTaskEvidence, error) {
	task := request.Task
	evidence := ReleaseTaskEvidence{Name: task.Name, VariableNames: append([]string(nil), task.Env...), ExitCode: -1, Runner: ReleaseTaskRunnerImage}
	sort.Strings(evidence.VariableNames)
	variables := map[string]string{}
	for name, value := range request.Variables {
		variables[name] = value
	}
	for _, name := range task.Env {
		value, ok := releaseValues[name]
		if !ok {
			return evidence, fmt.Errorf("release task variable %s is unavailable", name)
		}
		variables[name] = value
	}
	if _, explicit := variables["PORT"]; !explicit && request.Plan.InternalPort > 0 {
		variables["PORT"] = strconv.Itoa(request.Plan.InternalPort)
	}
	request.Variables = variables
	redacted := map[string]string{}
	for name, value := range variables {
		if request.Secret[name] || slicesContain(task.Env, name) {
			redacted[name] = value
		}
	}
	taskCtx, cancel := context.WithTimeout(ctx, time.Duration(task.TimeoutSeconds)*time.Second)
	defer cancel()
	started := time.Now()
	code, err := runner.RunReleaseTask(taskCtx, request, redactBuildEmitter(redacted, emit))
	evidence.DurationMS = time.Since(started).Milliseconds()
	evidence.ExitCode = code
	if ctx.Err() != nil {
		return evidence, fmt.Errorf("release task %s cancelled: %w", task.Name, ctx.Err())
	}
	if taskCtx.Err() != nil {
		return evidence, fmt.Errorf("release task %s timed out after %d seconds", task.Name, task.TimeoutSeconds)
	}
	return evidence, err
}

// releaseTaskImageRequest prepares what every image task of a run shares:
// the candidate release's image and runtime plan, and the runtime values the
// release will start with.
func (e *NormalizedStepExecutor) releaseTaskImageRequest(
	ctx context.Context,
	execution StepExecution,
	plan *StoredExecutionPlan,
) (ReleaseTaskRuntimeRequest, ReleaseTaskRuntime, error) {
	needed := false
	for _, task := range plan.Build.ReleaseTasks {
		needed = needed || task.Runner == ReleaseTaskRunnerImage
	}
	if !needed {
		return ReleaseTaskRuntimeRequest{}, nil, nil
	}
	runner, ok := e.runtime.(ReleaseTaskRuntime)
	if !ok || runner == nil {
		return ReleaseTaskRuntimeRequest{}, nil, fmt.Errorf("%w: release tasks cannot run in the release image on this host", ErrRuntimeUnavailable)
	}
	release, snapshot, err := e.releaseSnapshotForRun(ctx, execution.Run.ID)
	if err != nil {
		return ReleaseTaskRuntimeRequest{}, nil, err
	}
	image := releaseTaskImage(snapshot)
	if image == "" {
		return ReleaseTaskRuntimeRequest{}, nil, fmt.Errorf("%w: the candidate release has no image to run its tasks in", ErrArtifactMissing)
	}
	if e.variables == nil {
		return ReleaseTaskRuntimeRequest{}, nil, fmt.Errorf("%w: variable store is unavailable", ErrArtifactMissing)
	}
	scoped, err := e.variables.OpenRunScopedVariables(ctx, execution.Run.ID, execution.Run.EnvironmentID, "runtime")
	if err != nil {
		return ReleaseTaskRuntimeRequest{}, nil, err
	}
	values, secret := map[string]string{}, map[string]bool{}
	for _, value := range scoped {
		values[value.Name] = value.Value
		secret[value.Name] = value.Sensitivity != "plain"
	}
	return ReleaseTaskRuntimeRequest{
		Run: execution.Run, Release: release.Release, Image: image, Plan: snapshot.Plan, Variables: values, Secret: secret,
	}, runner, nil
}
