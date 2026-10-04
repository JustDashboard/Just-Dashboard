package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

func populationContainer(id, service string, number int, environmentID, releaseID int64) dockerx.Container {
	labels := map[string]string{"com.docker.compose.project": "original", "com.docker.compose.service": service, "com.docker.compose.container-number": strconv.Itoa(number)}
	if environmentID > 0 {
		labels["io.just-dashboard.managed"] = "true"
		labels["io.just-dashboard.environment-id"] = strconv.FormatInt(environmentID, 10)
		labels["io.just-dashboard.release-id"] = strconv.FormatInt(releaseID, 10)
	}
	return dockerx.Container{ID: id, State: "running", Labels: labels}
}

func TestSharedComposePopulationRejectsExternalIncludedReplicasAndAcceptsOnlyExpectedAuthority(t *testing.T) {
	base := populationContainer("captured", "web", 1, 0, 0)
	for _, test := range []struct {
		name      string
		extra     dockerx.Container
		permitted bool
	}{
		{"external_extra_replica", populationContainer("external", "web", 2, 0, 0), false},
		{"short_captured_id", populationContainer("cap", "web", 2, 0, 0), false},
		{"wrong_environment", populationContainer("managed", "web", 2, 99, 34), false},
		{"wrong_release", populationContainer("managed", "web", 2, 12, 99), false},
		{"expected_previous_release", populationContainer("managed", "web", 2, 12, 33), true},
		{"expected_candidate_release", populationContainer("managed", "web", 2, 12, 34), true},
		{"unrelated_service", populationContainer("external", "unrelated", 1, 0, 0), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateSharedComposePopulation([]dockerx.Container{base, test.extra}, []string{"web"}, map[string]bool{"captured": true}, 12, 33, 34)
			if (err == nil) != test.permitted {
				t.Fatal("shared namespace reservation was treated as broad container authority", err)
			}
		})
	}
	oneoff := populationContainer("external-job", "web", 2, 0, 0)
	oneoff.Labels["com.docker.compose.oneoff"] = "True"
	if err := validateSharedComposePopulation([]dockerx.Container{base, oneoff}, []string{"web"}, map[string]bool{"captured": true}, 12, 34); err != nil {
		t.Fatal("external oneoff was claimed", err)
	}
	if scope, err := composeBaselineScope([]dockerx.Container{base, populationContainer("external", "web", 2, 0, 0)}, []AdoptedContainer{{ID: "captured", Service: "web", Number: 1}}, ReleaseRuntime{EnvironmentID: 12, ReleaseID: 34}, true); !errors.Is(err, ErrInvalidPlan) || len(scope) != 0 {
		t.Fatal("baseline actions escaped full population refusal")
	}
}

func TestSharedComposeLegacyServiceScopeDerivesOnlyFromCompleteRecordedEvidence(t *testing.T) {
	metadata := dockerReleaseRuntimeMetadata{ContainerIDs: []string{"captured"}}
	services, err := sharedComposeRecordedServices(metadata, []dockerx.Container{populationContainer("captured", "web", 1, 0, 0)})
	if err != nil || strings.Join(services, ",") != "web" {
		t.Fatal("legacy scope could not be derived from captured IDs")
	}
	if _, err := sharedComposeRecordedServices(metadata, nil); !errors.Is(err, ErrInvalidPlan) {
		t.Fatal("missing original identity guessed the legacy service population")
	}
	metadata.BaselineContainers = []AdoptedContainer{{ID: "captured", Service: "web", Number: 1}}
	if services, err := sharedComposeRecordedServices(metadata, nil); err != nil || strings.Join(services, ",") != "web" {
		t.Fatal("baseline service names did not preserve legacy scope")
	}
}

type scopeCountingRuntime struct {
	RuntimeOwner
	stops, starts int
}

func (o *scopeCountingRuntime) ValidateCandidateScope(ctx context.Context, request CandidateRuntimeRequest) error {
	return o.RuntimeOwner.(RuntimeCandidateScopeValidator).ValidateCandidateScope(ctx, request)
}

func (o *scopeCountingRuntime) Stop(ctx context.Context, runtime ReleaseRuntime, plan RuntimePlanConfig, variables map[string]string, remove bool, emit func(BuildLog) error) (RuntimeStopEvidence, error) {
	o.stops++
	return o.RuntimeOwner.Stop(ctx, runtime, plan, variables, remove, emit)
}

func (o *scopeCountingRuntime) StartCandidate(ctx context.Context, request CandidateRuntimeRequest, emit func(BuildLog) error) (StartedRuntime, error) {
	o.starts++
	return o.RuntimeOwner.StartCandidate(ctx, request, emit)
}

func TestSharedComposeCandidatePopulationRefusalPrecedesEveryStopAndUp(t *testing.T) {
	for _, foreignService := range []string{"web", "new_worker"} {
		t.Run(foreignService, func(t *testing.T) {
			fixture := newReleaseStoreFixture(t)
			plan := RuntimePlanConfig{Strategy: StrategyStopFirst, ComposeProjectName: "original"}
			fixture.addPlanWithRuntime(t, 1, strings.Repeat("a", 40), plan)
			firstRun, firstLease := fixture.claimedRun(t, 1)
			live := createRuntimeCandidate(t, fixture, *firstRun, firstLease, plan, "population-v1")
			if _, err := fixture.runs.RecordCandidateRuntime(t.Context(), *firstRun, firstLease.Token, ReleaseRuntimeInput{ReleaseID: live.Release.ID, Kind: "compose", RuntimeID: "original", Metadata: mustJSON(dockerReleaseRuntimeMetadata{Version: 1, SharedProject: true, ProjectName: "original", ServiceNames: []string{"web"}, ContainerIDs: []string{"captured"}})}); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.runs.ActivateCandidate(t.Context(), firstRun.ID, firstLease.Token, live.Release.ID); err != nil {
				t.Fatal(err)
			}
			fixture.finishActivatedRun(t, firstRun.ID, live.Release.ID, firstLease.Token)
			fixture.now = fixture.now.Add(time.Minute)
			fixture.addPlanWithRuntime(t, 2, strings.Repeat("b", 40), plan)
			secondRun, secondLease := fixture.claimedRun(t, 2)
			digest := fakeContentDigest("population-v2")
			origin := WorkloadCandidate{Kind: "stack", ResourceID: "original", Services: []WorkloadService{{ResourceID: "captured"}}}
			snapshot := runtimeReleaseSnapshot{Version: 1, Plan: plan, Image: ResolvedImage{Digest: digest}, Compose: &ResolvedComposeSnapshot{Services: []ResolvedComposeService{{Plan: ComposeServicePlan{Name: "web"}, Digest: digest}, {Plan: ComposeServicePlan{Name: "new_worker"}, Digest: digest}}}, Dependencies: []PlannedDependency{{Kind: "runtime", Ownership: OwnershipManaged, ResourceKind: "compose_stack", ResourceID: "original", Config: mustJSON(origin)}}}
			if _, err := fixture.runs.CreateCandidateRelease(t.Context(), *secondRun, secondLease.Token, CandidateReleaseInput{Artifacts: []ReleaseArtifactInput{{Kind: ArtifactImage, Reference: "fixture:latest", Digest: digest}}, Prepared: PreparedBuild{Method: BuildDockerfile, Dockerfile: "Dockerfile", DockerfileDigest: fakeContentDigest("Dockerfile")}, RuntimeSnapshot: mustJSON(snapshot), RuntimeDigest: digestBytes(mustJSON(snapshot))}); err != nil {
				t.Fatal(err)
			}
			mutations := 0
			engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.HasSuffix(r.URL.Path, "/_ping"):
					w.Header().Set("API-Version", "1.47")
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/containers/json"):
					base, extra := populationContainer("captured", "web", 1, 0, 0), populationContainer("external", foreignService, 2, 0, 0)
					json.NewEncoder(w).Encode([]map[string]any{{"Id": base.ID, "State": base.State, "Labels": base.Labels}, {"Id": extra.ID, "State": extra.State, "Labels": extra.Labels}})
				case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/json"):
					json.NewEncoder(w).Encode(map[string]any{"State": map[string]any{"Running": true}})
				default:
					if r.Method != http.MethodGet && r.Method != http.MethodHead {
						mutations++
					}
					t.Errorf("unexpected Engine request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusInternalServerError)
				}
			}))
			defer engine.Close()
			client := dockerx.New(engine.URL)
			defer client.Close()
			owner := &scopeCountingRuntime{RuntimeOwner: NewNativeRuntimeOwner(NewDockerRuntimeOwner(client), nil, nil)}
			executor := &NormalizedStepExecutor{store: fixture.runs, variables: fixture.variables, runtime: owner}
			result := executor.startCandidate(t.Context(), StepExecution{Run: *secondRun, ClaimToken: secondLease.Token, Output: discardStepOutput{}}, mustExecutionPlan(t, fixture, *secondRun))
			if result.State != StepFailed || result.ErrorCode != "candidate_scope_changed" || owner.stops != 0 || owner.starts != 0 || mutations != 0 {
				t.Fatalf("external included-service population mutated before refusal: state=%s code=%s stops=%d starts=%d engineActions=%d", result.State, result.ErrorCode, owner.stops, owner.starts, mutations)
			}
		})
	}
}

func TestSharedComposeBaselineExtraCleanupRequiresWholeExpectedReleaseScope(t *testing.T) {
	previous := populationContainer("previous-extra", "previous_service", 1, 12, 33)
	previous.State = "exited"
	candidate := populationContainer("candidate-extra", "candidate_service", 1, 12, 34)
	candidate.State = "created"
	foreign := populationContainer("other-release-extra", "other_service", 1, 12, 99)
	foreign.State = "exited"
	paused := previous
	paused.State = "paused"
	oneoff := foreign
	oneoff.Labels = map[string]string{}
	for key, value := range foreign.Labels {
		oneoff.Labels[key] = value
	}
	oneoff.Labels["com.docker.compose.oneoff"] = "True"
	for _, test := range []struct {
		name       string
		containers []dockerx.Container
		removed    int
		blocked    bool
	}{
		{"previous_release", []dockerx.Container{previous}, 1, false},
		{"candidate_release", []dockerx.Container{candidate}, 1, false},
		{"other_release", []dockerx.Container{foreign}, 0, true},
		{"validate_all_before_removal", []dockerx.Container{previous, foreign}, 0, true},
		{"paused_runtime", []dockerx.Container{paused}, 0, true},
		{"true_oneoff", []dockerx.Container{oneoff}, 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			removed := 0
			engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.HasSuffix(r.URL.Path, "/_ping"):
					w.Header().Set("API-Version", "1.47")
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/containers/json"):
					items := []map[string]any{}
					for _, current := range test.containers {
						items = append(items, map[string]any{"Id": current.ID, "State": current.State, "Labels": current.Labels})
					}
					json.NewEncoder(w).Encode(items)
				case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/containers/"):
					removed++
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Errorf("unexpected Engine request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusInternalServerError)
				}
			}))
			defer engine.Close()
			client := dockerx.New(engine.URL)
			defer client.Close()
			err := NewDockerRuntimeOwner(client).removeOwnedBaselineExtras(t.Context(), "original", 12, []AdoptedContainer{{Service: "web", Number: 1}}, 34, 33)
			if errors.Is(err, ErrInvalidPlan) != test.blocked || (err != nil && !test.blocked) || removed != test.removed {
				t.Fatalf("baseline cleanup escaped expected authority: blocked=%t removed=%d err=%v", test.blocked, removed, err)
			}
		})
	}
}
