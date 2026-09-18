package deploy

import (
	"context"
	"encoding/json"
	"net/url"
	"time"
)

type RunLogSource struct {
	ContainerID   string `json:"containerId"`
	Name          string `json:"name"`
	LiveURL       string `json:"liveUrl"`
	ActivationURL string `json:"activationUrl,omitempty"`
}

type RunLogs struct {
	Status                string         `json:"status"`
	Reason                string         `json:"reason,omitempty"`
	WindowReason          string         `json:"windowReason,omitempty"`
	ActivationCompletedAt *time.Time     `json:"activationCompletedAt,omitempty"`
	Sources               []RunLogSource `json:"sources"`
}

// The step timestamp belongs to this run, unlike a release's mutable activation
// timestamp which may later be updated by another operation on the same runtime.
func runActivationCompleted(snapshot RunSnapshot, releaseID int64) *time.Time {
	var latest *RunStep
	for i := range snapshot.Steps {
		step := &snapshot.Steps[i]
		if step.Key == StepActivate && (latest == nil || step.Attempt > latest.Attempt ||
			(step.Attempt == latest.Attempt && step.ID > latest.ID)) {
			latest = step
		}
	}
	if latest == nil || latest.State != StepPassed || latest.EndedAt == nil || latest.EndedAt.IsZero() {
		return nil
	}
	var evidence activationStepEvidence
	if json.Unmarshal(latest.Evidence, &evidence) != nil || evidence.ReleaseID != releaseID || evidence.Recovery != nil {
		return nil
	}
	return latest.EndedAt
}

func ObserveRunLogs(ctx context.Context, owner RuntimeObserver, snapshot RunSnapshot) RunLogs {
	result := RunLogs{Status: "unavailable", Sources: []RunLogSource{},
		WindowReason: "This run has no completed activation evidence. Live logs remain available for any retained runtime."}
	releaseID := snapshot.Run.CandidateReleaseID
	if releaseID == 0 {
		releaseID = snapshot.Run.ReleaseID
	}
	if releaseID <= 0 {
		result.Reason = "This run has no recorded release runtime. Review its deployment transcript."
		return result
	}
	result.ActivationCompletedAt = runActivationCompleted(snapshot, releaseID)
	if result.ActivationCompletedAt != nil {
		result.WindowReason = ""
	}
	runtime := observeRuntimeServices(ctx, owner, snapshot.Run.EnvironmentID, 0, releaseID)
	result.Status, result.Reason = runtime.Status, runtime.Reason
	for _, service := range runtime.Services {
		query := url.Values{"source": {"docker:" + service.ContainerID}}
		source := RunLogSource{ContainerID: service.ContainerID, Name: service.Name, LiveURL: "/logs?" + query.Encode()}
		if at := result.ActivationCompletedAt; at != nil {
			query.Set("mode", "search")
			query.Set("since", at.Add(-5*time.Minute).UTC().Format("2006-01-02T15:04:05.000Z"))
			query.Set("until", at.Add(5*time.Minute).UTC().Format("2006-01-02T15:04:05.000Z"))
			source.ActivationURL = "/logs?" + query.Encode()
		}
		result.Sources = append(result.Sources, source)
	}
	return result
}
