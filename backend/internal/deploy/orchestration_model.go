package deploy

import (
	"encoding/json"
	"time"
)

type SlotClass string

const (
	SlotLight SlotClass = "light"
	SlotHeavy SlotClass = "heavy"
)

func (s SlotClass) valid() bool { return s == SlotLight || s == SlotHeavy }

// RunRequest is the immutable request envelope persisted before an enqueue
// caller receives its run id. RequestDigest must describe the normalized plan
// and request, not the raw JSON bytes, so semantically identical retries join.
type RunRequest struct {
	ProjectID            int64
	EnvironmentID        int64
	Operation            Operation
	Trigger              TriggerKind
	Actor                string
	IdempotencyKey       string
	RequestDigest        string
	PlanRevision         int
	SourceRevision       string
	ExpectedPlanRevision int
	ExpectedGitPolicy    string
	RetryOfRunID         int64
	// VariableSnapshotRunID is internal retry provenance. A normal enqueue
	// captures the environment's active revisions; a retry copies this run's
	// already-frozen set instead of observing later rotations.
	VariableSnapshotRunID int64
	Priority              int
	SlotClass             SlotClass
	Metadata              json.RawMessage
	Steps                 []StepKey
}

type EngineRun struct {
	ID                 int64           `json:"id"`
	RunNumber          int64           `json:"runNumber"`
	SourceRevision     string          `json:"sourceRevision,omitempty"`
	ProjectID          int64           `json:"projectId"`
	EnvironmentID      int64           `json:"environmentId"`
	State              RunState        `json:"state"`
	Operation          Operation       `json:"operation"`
	Trigger            TriggerKind     `json:"trigger"`
	Actor              string          `json:"actor"`
	RequestedAt        time.Time       `json:"requestedAt"`
	QueuedAt           *time.Time      `json:"queuedAt,omitempty"`
	ClaimedAt          *time.Time      `json:"claimedAt,omitempty"`
	HeartbeatAt        *time.Time      `json:"heartbeatAt,omitempty"`
	EndedAt            *time.Time      `json:"endedAt,omitempty"`
	CancelRequested    bool            `json:"cancelRequested"`
	SupersededBy       int64           `json:"supersededBy,omitempty"`
	RetryOfRunID       int64           `json:"retryOfRunId,omitempty"`
	IdempotencyKey     string          `json:"idempotencyKey,omitempty"`
	RequestDigest      string          `json:"requestDigest"`
	PlanRevision       int             `json:"planRevision"`
	ReleaseID          int64           `json:"releaseId,omitempty"`
	CandidateReleaseID int64           `json:"candidateReleaseId,omitempty"`
	TerminalCode       string          `json:"terminalCode,omitempty"`
	TerminalReason     string          `json:"terminalReason,omitempty"`
	LeaseUntil         *time.Time      `json:"leaseUntil,omitempty"`
	Priority           int             `json:"priority"`
	SlotClass          SlotClass       `json:"slotClass"`
	Metadata           json.RawMessage `json:"metadata"`
}

type RunStep struct {
	ID             int64           `json:"id"`
	RunID          int64           `json:"runId"`
	Key            StepKey         `json:"key"`
	Ordinal        int             `json:"ordinal"`
	State          StepState       `json:"state"`
	Attempt        int             `json:"attempt"`
	TimeoutSeconds int             `json:"timeoutSeconds"`
	StartedAt      *time.Time      `json:"startedAt,omitempty"`
	EndedAt        *time.Time      `json:"endedAt,omitempty"`
	Evidence       json.RawMessage `json:"evidence"`
	ErrorCode      string          `json:"errorCode,omitempty"`
	ErrorMessage   string          `json:"errorMessage,omitempty"`
	Cleanup        json.RawMessage `json:"cleanup"`
	LastSeq        int64           `json:"lastSeq"`
}

type QueueLease struct {
	RunID         int64     `json:"runId"`
	EnvironmentID int64     `json:"environmentId"`
	Token         string    `json:"-"`
	SlotClass     SlotClass `json:"slotClass"`
	ClaimedBy     string    `json:"claimedBy"`
	ClaimedAt     time.Time `json:"claimedAt"`
	HeartbeatAt   time.Time `json:"heartbeatAt"`
	ExpiresAt     time.Time `json:"expiresAt"`
}

type RunSnapshot struct {
	Run   EngineRun `json:"run"`
	Steps []RunStep `json:"steps"`
}

type TransitionDetail struct {
	Code   string
	Reason string
}
