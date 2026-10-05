package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

var ErrRecoveryBlocked = errors.New("this workload cannot yet be safely adopted")

type WorkloadRecoveryScope string

const (
	RecoveryAllServices      WorkloadRecoveryScope = "all_services"
	RecoveryExistingServices WorkloadRecoveryScope = "existing_services"
)

func (scope WorkloadRecoveryScope) Normalized() WorkloadRecoveryScope {
	if scope == "" {
		return RecoveryAllServices
	}
	return scope
}
func (scope WorkloadRecoveryScope) ValidForKind(kind string) bool {
	scope = scope.Normalized()
	return scope == RecoveryAllServices || (scope == RecoveryExistingServices && kind == "stack")
}

type AdoptedContainer struct {
	ID          string `json:"id"`
	Service     string `json:"service"`
	Number      int    `json:"number"`
	Running     bool   `json:"running"`
	StopTimeout *int   `json:"stopTimeout,omitempty"`
}

type AdoptionIssue struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	Service  string `json:"service,omitempty"`
	Field    string `json:"field,omitempty"`
	Blocking bool   `json:"blocking"`
}

// RecoveredBuildSource describes verified build inputs without exposing private
// files or claiming that a source build is byte-identical to the live image.
type RecoveredBuildSource struct {
	Service        string              `json:"service"`
	Name           string              `json:"name,omitempty"`
	Framework      string              `json:"framework,omitempty"`
	Language       string              `json:"language,omitempty"`
	Role           WorkloadProfile     `json:"role,omitempty"`
	Confidence     DetectionConfidence `json:"confidence,omitempty"`
	Status         string              `json:"status"`
	Reason         string              `json:"reason,omitempty"`
	SnapshotDigest string              `json:"snapshotDigest,omitempty"`
}

// WorkloadAdoption contains only sanitized, server-produced recovery evidence.
// Baseline values are stored separately under encryption; a source document
// references their variable names rather than containing their values.
type WorkloadAdoption struct {
	Inputs                      []RecoveredInput       `json:"inputs,omitempty"`
	Key                         string                 `json:"key"`
	Digest                      string                 `json:"digest"`
	Kind                        string                 `json:"kind"`
	ResourceID                  string                 `json:"resourceId"`
	Manager                     string                 `json:"manager"`
	Name                        string                 `json:"name"`
	Scope                       WorkloadRecoveryScope  `json:"scope,omitempty"`
	ExcludedServices            []string               `json:"excludedServices"`
	OriginalConfigurationDigest string                 `json:"originalConfigurationDigest,omitempty"`
	Warnings                    []string               `json:"warnings"`
	Blockers                    []string               `json:"blockers"`
	Issues                      []AdoptionIssue        `json:"issues"`
	ServiceCount                int                    `json:"serviceCount"`
	RunningCount                int                    `json:"runningCount"`
	OriginalSourcePath          string                 `json:"originalSourcePath,omitempty"`
	ConfigFiles                 []string               `json:"configFiles,omitempty"`
	BaselineSource              DraftSourceConfig      `json:"baselineSource"`
	BaselineDetection           DetectionResult        `json:"baselineDetection"`
	BuildSources                []RecoveredBuildSource `json:"buildSources,omitempty"`
	BaselineConfiguration       PlanConfiguration      `json:"baselineConfiguration"`
	BaselineDigest              string                 `json:"baselineDigest"`
	Runtime                     ReleaseRuntimeInput    `json:"runtime"`
	Snapshot                    json.RawMessage        `json:"snapshot"`
	RecoveryDirectory           string                 `json:"recoveryDirectory,omitempty"`
}

// WorkloadAdoptionOrigin is kept as a named alias for feature owners which
// need the capture origin independently of the recovered draft.
type WorkloadAdoptionOrigin = WorkloadAdoption

type RecoveredWorkload struct {
	Source              DraftSourceConfig `json:"source"`
	Configuration       PlanConfiguration `json:"configuration"`
	Detection           DetectionResult   `json:"detection"`
	Adoption            *WorkloadAdoption `json:"adoption"`
	Environment         map[string]string `json:"-"`
	BaselineEnvironment map[string]string `json:"-"`
}

type DockerWorkloadRecoveryReader interface {
	CaptureAdoptionContainer(context.Context, string) (*dockerx.AdoptionContainer, error)
	InspectImage(context.Context, string) (*dockerx.ImageDetail, error)
	ReadComposeAdoptionConfiguration(context.Context, string, string, []string, []string) ([]byte, error)
}
