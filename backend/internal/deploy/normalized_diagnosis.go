package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// lastLogSeq is the sequence of the line a step's output last persisted, when
// the output is the engine's own; test outputs have none.
func lastLogSeq(output StepOutput) int64 {
	if sequenced, ok := output.(interface{ LastSeq() int64 }); ok {
		return sequenced.LastSeq()
	}
	return 0
}

// buildFailure names a failed build from its own output: the failed step's
// cause as the step's code, its sentence as the message and the cause itself
// as evidence. It returns nil for an error the build did not print, which
// keeps normalizedStepFailure's code.
func (e *NormalizedStepExecutor) buildFailure(
	ctx context.Context,
	execution StepExecution,
	plan *StoredExecutionPlan,
	prepared PreparedBuild,
	redacted map[string]string,
	collector *buildOutputCollector,
	err error,
) *StepResult {
	context := causeContext{
		build: plan.Build, runtime: plan.Runtime, prepared: prepared,
		candidate: e.causeCandidate(ctx, execution.Run.ID, plan), variables: plan.Variables,
		hostMemory: hostMemoryTotal(),
	}
	cause := buildFailureCause(err, collector, context, buildRedactor(redacted))
	if cause == nil {
		return nil
	}
	message := cause.sentence()
	_ = stepLog(execution, "status", "Diagnosis: "+message)
	return &StepResult{
		State: StepFailed, ErrorCode: cause.Code, ErrorMessage: message,
		Evidence: mustJSON(map[string]any{"cause": cause}),
	}
}

// buildFailureCleanup keeps the process-group evidence of a build stopped at
// its time limit beside the workspace cleanup.
func buildFailureCleanup(cleanup map[string]any, err error) map[string]any {
	var timeout *dockerx.BuildTimeoutError
	if errors.As(err, &timeout) {
		cleanup["processGroup"] = timeout.Result
	}
	return cleanup
}

// causeCandidate is what detection read in the commit this run builds: the
// candidate analyze_plan recorded after detecting the acquired checkout, or,
// for a run whose check recorded none, the candidate the plan was committed
// with that describes its build.
func (e *NormalizedStepExecutor) causeCandidate(ctx context.Context, runID int64, plan *StoredExecutionPlan) *DetectedCandidate {
	if snapshot, err := e.store.Snapshot(ctx, runID); err == nil {
		var latest *RunStep
		for index := range snapshot.Steps {
			step := &snapshot.Steps[index]
			if step.Key == StepAnalyzePlan && (latest == nil || step.Attempt > latest.Attempt) {
				latest = step
			}
		}
		if latest != nil {
			var evidence struct {
				Candidate *DetectedCandidate `json:"candidate"`
			}
			if json.Unmarshal(latest.Evidence, &evidence) == nil && evidence.Candidate != nil {
				return evidence.Candidate
			}
		}
	}
	return plannedDetectionCandidate(&DetectionResult{Candidates: plan.BuildEvidence.Candidates}, plan.Build)
}

// baseImageReferenceRE is the reviewed reference resolveBases names in its
// own error: a catalogue tag, never anything the repository wrote.
var baseImageReferenceRE = regexp.MustCompile(`resolve reviewed base image ([\w./:@-]+):`)

const builderDetailLength = 300

// builderUnavailableFailure keeps the state a builder fault has always had,
// and names which fault it was from fixed substrings of the daemon's error.
func builderUnavailableFailure(err error) StepResult {
	result := StepResult{
		State: StepUnavailable, ErrorCode: "builder_unavailable",
		ErrorMessage: "BuildKit or a reviewed base image is unavailable",
	}
	text := err.Error()
	image := ""
	if match := baseImageReferenceRE.FindStringSubmatch(text); match != nil {
		image = match[1]
	}
	lower := strings.ToLower(text)
	subject := "a reviewed base image"
	if image != "" {
		subject = "the reviewed base image " + image
	}
	switch {
	case strings.Contains(lower, "toomanyrequests") || strings.Contains(lower, "pull rate limit") ||
		strings.Contains(lower, "429 too many requests"):
		result.ErrorCode = "registry_rate_limited"
		result.ErrorMessage = "Could not resolve " + subject + ": Docker Hub's anonymous pull limit for this server's address was reached; sign the server in to Docker Hub (`docker login`) or wait for the limit to reset"
	case strings.Contains(lower, "no such host") || strings.Contains(lower, "i/o timeout") ||
		strings.Contains(lower, "tls handshake timeout") || strings.Contains(lower, "temporary failure in name resolution") ||
		strings.Contains(lower, "network is unreachable") || strings.Contains(lower, "connection refused") ||
		strings.Contains(lower, "deadline exceeded") || strings.Contains(lower, "client.timeout exceeded") ||
		strings.Contains(lower, "request canceled while waiting for connection") || strings.Contains(lower, "server misbehaving"):
		result.ErrorCode = "registry_unreachable"
		result.ErrorMessage = "Could not resolve " + subject + ": the registry could not be reached from this server; check its outbound network and DNS"
	case strings.Contains(lower, "manifest unknown") || strings.Contains(lower, "not found"):
		result.ErrorCode = "base_image_missing"
		result.ErrorMessage = "Could not resolve " + subject + ": the registry has no such image or tag"
	case strings.Contains(lower, "repository does not exist"):
		// Docker Hub's "pull access denied … repository does not exist or may
		// require 'docker login'": a public catalogue base it will not serve
		// is gone, whatever the denial says about logging in.
		result.ErrorCode = "base_image_missing"
		result.ErrorMessage = "Could not resolve " + subject + ": the registry says the repository does not exist"
	case strings.Contains(lower, "unauthorized") || strings.Contains(lower, "denied") ||
		strings.Contains(lower, "authentication required"):
		result.ErrorCode = "registry_auth_failed"
		result.ErrorMessage = "Could not resolve " + subject + ": the registry refused this server; a stale `docker login` on the server is the usual cause"
	case err == ErrBuilderUnavailable:
		// Only the builder itself returns the bare sentinel: no backend, so
		// no Docker to build with.
		result.ErrorCode = "builder_missing"
		result.ErrorMessage = "Docker or its Buildx plugin is not available to the deployment builder"
	}
	return result
}

// builderUnavailableDetail is the daemon's own reason, bounded and redacted,
// for the transcript. Bases resolve without registry credentials, so the text
// carries none of the server's.
func builderUnavailableDetail(err error, redact func(string) string) string {
	text := err.Error()
	if redact != nil {
		text = redact(text)
	}
	text = strings.Join(strings.Fields(text), " ")
	return truncateUTF8Prefix(text, builderDetailLength)
}

// releaseTaskFailure is a failed release task's code and message, named from
// its own output when the output proves why.
func releaseTaskFailure(
	task ReleaseTaskConfig,
	evidence ReleaseTaskEvidence,
	err error,
	lines []collectedLine,
	variables []ReleaseVariableSnapshot,
) (string, string, *OutputCause) {
	message := "release task " + task.Name + " did not complete"
	if strings.Contains(err.Error(), "timed out after") {
		return "release_task_timeout", message + ": it ran past its time limit", nil
	}
	cause := releaseTaskCause(lines, evidence.ExitCode, variables)
	if cause == nil && evidence.ExitCode == 127 {
		// The shell's own exit status for a program it cannot find, when the
		// line naming it did not survive.
		cause = &OutputCause{Code: "release_task_command_not_found", Fix: &CauseFix{Kind: fixReview, Field: "configuration.build.releaseTasks"}}
	}
	if cause == nil {
		return "release_task_failed", message, nil
	}
	switch cause.Code {
	case "release_task_command_not_found", "release_database_unreachable":
		// Where the task ran decides what it could reach (release_task.go).
		cause.Detail = releaseTaskPlace(task)
	}
	return cause.Code, message + ": " + cause.releaseSentence(), cause
}

// releaseTaskPlace names where a release task runs, as its cause's detail.
func releaseTaskPlace(task ReleaseTaskConfig) string {
	if task.Runner == ReleaseTaskRunnerImage {
		return "release image"
	}
	return "dashboard shell"
}

func releaseTaskStepEvidence(tasks []ReleaseTaskEvidence, cause *OutputCause) map[string]any {
	evidence := map[string]any{"tasks": tasks}
	if cause != nil {
		evidence["cause"] = cause
	}
	return evidence
}

var dockerPortRE = regexp.MustCompile(`(?:0\.0\.0\.0|127\.0\.0\.1|\[::\]|::|[\d.]+):(\d{1,5})`)

// candidateStartFailure names the Docker API errors a candidate's start
// commonly meets. The message is fixed text and a port number: the daemon's
// own error can quote a container's configuration.
func candidateStartFailure(err error) (string, string) {
	text := err.Error()
	switch {
	case strings.Contains(text, "port is already allocated") || strings.Contains(text, "address already in use"):
		message := "a port the candidate publishes is already in use on this server"
		if match := dockerPortRE.FindStringSubmatch(text); match != nil {
			message = "port " + match[1] + " is already in use on this server by another process or container"
		}
		return "runtime_port_in_use", message + "; free it or publish another port"
	case strings.Contains(text, "No such image"):
		return "image_missing", "the release's image is no longer on this server; deploy again to rebuild it"
	case strings.Contains(text, "invalid mount") || strings.Contains(text, "bind source path does not exist") ||
		strings.Contains(text, "mount denied") || strings.Contains(text, "invalid volume specification"):
		return "mount_invalid", "a mount could not be created: its host path is missing or not allowed"
	}
	return "candidate_start_failed", "the candidate runtime did not start"
}
