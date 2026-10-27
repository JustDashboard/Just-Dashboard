package api

import (
	"net/http"
	"strconv"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// transcriptLine is one line of a run's output, with the step that printed it
// named the way the run page names it.
type transcriptLine struct {
	deploy.RunTranscriptLine
	Step string `json:"step,omitempty"`
}

// handleDeploymentRunTranscript answers a run's output after a sequence
// number, with the run as it stands, so a client can follow a run by asking
// again from next until the run has ended and more is false. It is what the
// stream route sends over its socket, read in pages: the same lines, masked
// as they were written.
func (s *Server) handleDeploymentRunTranscript(w http.ResponseWriter, r *http.Request) error {
	projectID, runID, err := deploymentRunIDs(r)
	if err != nil {
		return err
	}
	query := r.URL.Query()
	after := int64(0)
	if raw := query.Get("after"); raw != "" {
		if after, err = strconv.ParseInt(raw, 10, 64); err != nil || after < 0 {
			return httpx.BadRequest("after must be a non-negative sequence")
		}
	}
	limit := 1000
	if raw := query.Get("limit"); raw != "" {
		if limit, err = strconv.Atoi(raw); err != nil || limit < 1 || limit > deploy.RunEventReplayLimit {
			return httpx.BadRequest("limit must be between 1 and %d", deploy.RunEventReplayLimit)
		}
	}
	snapshot, err := s.modules.deployRuns.Snapshot(r.Context(), runID)
	if err != nil {
		return mapDeployError(err)
	}
	if snapshot.Run.ProjectID != projectID {
		return mapDeployError(deploy.ErrRunNotFound)
	}
	lines, more, err := s.modules.deployRuns.RunTranscriptAfter(r.Context(), runID, after, limit)
	if err != nil {
		return mapDeployError(err)
	}
	steps := map[int64]string{}
	for _, step := range snapshot.Steps {
		label := deploy.StepLabel(step.Key, snapshot.Run.Operation)
		if label == "" {
			label = string(step.Key)
		}
		steps[step.ID] = label
	}
	out := make([]transcriptLine, 0, len(lines))
	next := after
	for _, line := range lines {
		out = append(out, transcriptLine{RunTranscriptLine: line, Step: steps[line.StepID]})
		next = line.Seq
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"run": snapshot.Run, "steps": snapshot.Steps, "lines": out, "next": next, "more": more,
	})
	return nil
}
