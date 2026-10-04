package deploy

import (
	"context"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

// These live tests isolate lifecycle behavior from unrelated host facilities,
// but runtime reservations must still use the same registered inventory checks
// as production before analyze_plan admits a real Docker mutation.
type liveReservationPreflight struct {
	base         PreflightObserver
	reservations *RuntimeReservationObserver
}

func livePreflightWithReservations(base PreflightObserver, store *store.Store, containers RecordedContainerReader, native NativeBaselineObserver) PreflightObserver {
	return liveReservationPreflight{base: base, reservations: NewRuntimeReservationObserver(store, containers, native)}
}

func (o liveReservationPreflight) Observe(ctx context.Context, request ObservationRequest) (HostObservation, error) {
	observation, err := o.base.Observe(ctx, request)
	if err != nil {
		return observation, err
	}
	dependencies, err := o.reservations.ObserveDependencies(ctx, request.Dependencies)
	observation.Dependencies = append(observation.Dependencies, dependencies...)
	return observation, err
}

func TestRuntimeReservationPreflightRequiresEvidenceAndReportsLostBaseline(t *testing.T) {
	dependency := PlannedDependency{Kind: "runtime", Ownership: OwnershipManaged, ResourceKind: "docker_container", ResourceID: "original"}
	configuration := PlanConfiguration{Build: BuildPlanConfig{Method: BuildNone}, Runtime: RuntimePlanConfig{Strategy: StrategyStopFirst}, Dependencies: []PlannedDependency{dependency}}
	draft := &Draft{Data: DraftData{Source: &DraftSourceConfig{Kind: SourceImage, Mode: SourceModeImageReference, Image: "fixture:local"}, Intent: &DraftIntentConfig{Name: "existing", Profile: ProfileWorker}, Detection: &DetectionResult{Source: SourceIdentity{Kind: SourceImage}}}}
	findings := preflightFindings(draft, configuration, HostObservation{}, false)
	assertC6Finding(t, findings, "dependency_unavailable", PreflightBlocked, "")
	for _, finding := range findings {
		if finding.Code == "dependency_unavailable" && !strings.Contains(finding.Action, "cannot be removed or relinked") {
			t.Fatalf("immutable reservation remedy: %+v", finding)
		}
	}
	observation := HostObservation{Dependencies: []DependencyObservation{{Kind: "runtime", ResourceKind: "docker_container", ResourceID: "original", Available: true, Warning: "Original native manager is unavailable."}}}
	findings = preflightFindings(draft, configuration, observation, false)
	assertC6Finding(t, findings, "dependency_available", PreflightPass, "")
	assertC6Finding(t, findings, "runtime_baseline_unavailable", PreflightWarning, "")
}
