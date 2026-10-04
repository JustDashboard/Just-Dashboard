package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

type reservedContainerInventory struct {
	details map[string]*dockerx.ContainerDetail
}

func (f reservedContainerInventory) Inspect(_ context.Context, id string) (*dockerx.ContainerDetail, error) {
	if detail := f.details[id]; detail != nil {
		return detail, nil
	}
	return nil, errors.New("container absent")
}

type reservedNativeInventory struct {
	available  bool
	expectedID string
	calls      int
}

func (f *reservedNativeInventory) ObserveNativeBaseline(_ context.Context, runtime deploy.ReleaseRuntime) deploy.RuntimeServices {
	f.calls++
	status := "unavailable"
	if f.available && runtime.RuntimeID == f.expectedID {
		status = "available"
	}
	return deploy.RuntimeServices{Status: status, Services: []deploy.RuntimeService{{State: "stopped"}}}
}

func seedRuntimeReservation(t *testing.T, s *Server, resourceKind, resourceID, runtimeKind string) (int64, int64, int64, deploy.PlannedDependency) {
	t.Helper()
	projectID, environmentID := seedCommittedDeployment(t, s, "reserved-"+resourceKind,
		deploy.DraftSourceConfig{Kind: deploy.SourceImage, Mode: deploy.SourceModeImageReference, Image: "fixture:local"}, deploy.SourceIdentity{Kind: deploy.SourceImage})
	kind := map[string]string{"compose_stack": "stack", "docker_container": "container", "pm2_process": "pm2", "systemd_unit": "systemd"}[resourceKind]
	candidate := deploy.WorkloadCandidate{Kind: kind, Key: kind + ":" + resourceID, ResourceID: resourceID, Name: "reserved-app", Services: []deploy.WorkloadService{}}
	config, _ := json.Marshal(candidate)
	dependency := deploy.PlannedDependency{Kind: "runtime", Ownership: deploy.OwnershipManaged, ResourceKind: resourceKind, ResourceID: resourceID, Config: config}
	if _, err := s.Store.DB.Exec(`INSERT INTO deploy_dependencies(environment_id,release_id,kind,ownership,resource_kind,resource_id,config_json,created_at)
	 VALUES(?,0,'runtime','managed',?,?,?,0)`, environmentID, resourceKind, resourceID, string(config)); err != nil {
		t.Fatal(err)
	}
	provenance, _ := json.Marshal(map[string]any{"adopted": true, "workloadKey": candidate.Key})
	result, err := s.Store.DB.Exec(`INSERT INTO deploy_runs(project_id,environment_id,started_at,status,state,plan_revision) VALUES(?,?,0,'success','succeeded',1)`, projectID, environmentID)
	if err != nil {
		t.Fatal(err)
	}
	runID, _ := result.LastInsertId()
	dependenciesJSON, _ := json.Marshal([]deploy.PlannedDependency{dependency})
	if _, err := s.Store.DB.Exec(`INSERT INTO deploy_run_plan_snapshots(run_id,environment_id,dependencies_json,digest,created_at) VALUES(?,?,?,'sha256:fixture-plan',0)`, runID, environmentID, string(dependenciesJSON)); err != nil {
		t.Fatal(err)
	}
	result, err = s.Store.DB.Exec(`INSERT INTO deploy_releases(project_id,environment_id,release_number,state,provenance_json,run_id,source_id,build_plan_id,runtime_plan_id,created_at)
	 SELECT ?,?,1,'live',?,?,s.id,b.id,r.id,0 FROM deploy_sources s
	 JOIN deploy_build_plans b ON b.environment_id=s.environment_id AND b.revision=s.revision
	 JOIN deploy_runtime_plans r ON r.environment_id=s.environment_id AND r.revision=s.revision
	 WHERE s.environment_id=? AND s.revision=1`, projectID, environmentID, string(provenance), runID, environmentID)
	if err != nil {
		t.Fatal(err)
	}
	releaseID, _ := result.LastInsertId()
	id := resourceID
	if runtimeKind == "compose" {
		id = strings.Repeat("a", 64)
	}
	metadata, _ := json.Marshal(map[string]any{"version": 1, "containerIds": []string{id}})
	if _, err := s.Store.DB.Exec(`INSERT INTO deploy_release_runtimes(release_id,environment_id,kind,runtime_id,state,metadata_json,created_at,updated_at)
	 VALUES(?,?,?,?,'stopped',?,0,0)`, releaseID, environmentID, runtimeKind, resourceID, string(metadata)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`UPDATE deploy_environments SET live_release_id=? WHERE id=?`, releaseID, environmentID); err != nil {
		t.Fatal(err)
	}
	return projectID, environmentID, releaseID, dependency
}

func observeReserved(t *testing.T, observer *deploymentDependencyObserver, dependency deploy.PlannedDependency) deploy.DependencyObservation {
	t.Helper()
	result, err := observer.ObserveDependencies(t.Context(), []deploy.PlannedDependency{dependency})
	if err != nil || len(result) != 1 {
		t.Fatalf("observation: %#v %v", result, err)
	}
	return result[0]
}

func TestRuntimeReservationsVerifyStoppedOriginalOwnersAndRejectForgedAuthority(t *testing.T) {
	for _, tc := range []struct{ resourceKind, resourceID, runtimeKind string }{
		{"docker_container", strings.Repeat("a", 64), "container"},
		{"compose_stack", "original-stack", "compose"},
		{"pm2_process", "server-account/application-space/original-app", "pm2"},
		{"systemd_unit", "original-app.service", "systemd"},
	} {
		t.Run(tc.resourceKind, func(t *testing.T) {
			s := testServer(t)
			_, _, releaseID, dependency := seedRuntimeReservation(t, s, tc.resourceKind, tc.resourceID, tc.runtimeKind)
			id := strings.Repeat("a", 64)
			detail := &dockerx.ContainerDetail{Container: dockerx.Container{ID: id, State: "exited", ComposeStack: tc.resourceID}}
			native := &reservedNativeInventory{available: true, expectedID: tc.resourceID}
			observer := newDeploymentDependencyObserver(s.Store, nil, nil).withNativeRuntime(native)
			observer.containers = reservedContainerInventory{details: map[string]*dockerx.ContainerDetail{id: detail}}
			if got := observeReserved(t, observer, dependency); !got.Available || got.Warning != "" {
				t.Fatalf("stopped owner unavailable: %+v", got)
			}
			for _, mutate := range []func(*deploy.PlannedDependency){
				func(d *deploy.PlannedDependency) { d.Ownership = deploy.OwnershipMode("external") },
				func(d *deploy.PlannedDependency) { d.ResourceID = "another-application" },
				func(d *deploy.PlannedDependency) {
					d.Config = json.RawMessage(`{"kind":"container","resourceId":"another-application"}`)
				},
				func(d *deploy.PlannedDependency) { d.ResourceKind = "unknown_external_runtime" },
			} {
				forged := dependency
				mutate(&forged)
				if got := observeReserved(t, observer, forged); got.Available {
					t.Fatalf("forged reservation available: %+v", got)
				}
			}
			if tc.runtimeKind == "pm2" || tc.runtimeKind == "systemd" {
				native.expectedID = "different-account/namespace/original-app"
			} else {
				observer.containers = reservedContainerInventory{}
			}
			if got := observeReserved(t, observer, dependency); got.Available {
				t.Fatalf("absent/external owner available: %+v", got)
			}
			if _, err := s.Store.DB.Exec(`DELETE FROM deploy_release_runtimes WHERE release_id=?`, releaseID); err != nil {
				t.Fatal(err)
			}
			native.expectedID = tc.resourceID
			observer.containers = reservedContainerInventory{details: map[string]*dockerx.ContainerDetail{id: detail}}
			if got := observeReserved(t, observer, dependency); got.Available {
				t.Fatalf("unregistered baseline available: %+v", got)
			}
		})
	}
}

func TestRuntimeReservationsFollowOwnedDockerCutoverAndRecreatedBaseline(t *testing.T) {
	for _, resourceKind := range []string{"docker_container", "compose_stack", "pm2_process", "systemd_unit"} {
		t.Run(resourceKind, func(t *testing.T) {
			s := testServer(t)
			resourceID, runtimeKind := strings.Repeat("a", 64), "container"
			switch resourceKind {
			case "compose_stack":
				resourceID, runtimeKind = "original-stack", "compose"
			case "pm2_process":
				resourceID, runtimeKind = "account/namespace/app", "pm2"
			case "systemd_unit":
				resourceID, runtimeKind = "app.service", "systemd"
			}
			projectID, environmentID, baselineID, dependency := seedRuntimeReservation(t, s, resourceKind, resourceID, runtimeKind)
			result, err := s.Store.DB.Exec(`INSERT INTO deploy_releases(project_id,environment_id,release_number,state,created_at) VALUES(?,?,2,'live',0)`, projectID, environmentID)
			if err != nil {
				t.Fatal(err)
			}
			currentID, _ := result.LastInsertId()
			containerID := strings.Repeat("b", 64)
			metadata, _ := json.Marshal(map[string]any{"version": 1, "containerIds": []string{containerID}})
			if _, err := s.Store.DB.Exec(`INSERT INTO deploy_release_runtimes(release_id,environment_id,kind,runtime_id,state,metadata_json,created_at,updated_at) VALUES(?,?,'compose','managed-project','live',?,0,0)`, currentID, environmentID, string(metadata)); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Store.DB.Exec(`UPDATE deploy_environments SET live_release_id=? WHERE id=?`, currentID, environmentID); err != nil {
				t.Fatal(err)
			}
			detail := &dockerx.ContainerDetail{Container: dockerx.Container{ID: containerID, State: "running", ComposeStack: "managed-project", Labels: map[string]string{"io.just-dashboard.managed": "true", "io.just-dashboard.environment-id": strconv.FormatInt(environmentID, 10), "io.just-dashboard.release-id": strconv.FormatInt(currentID, 10)}}}
			native := &reservedNativeInventory{available: true, expectedID: resourceID}
			observer := newDeploymentDependencyObserver(s.Store, nil, nil).withNativeRuntime(native)
			observer.containers = reservedContainerInventory{details: map[string]*dockerx.ContainerDetail{containerID: detail}}
			if got := observeReserved(t, observer, dependency); !got.Available || got.Warning != "" {
				t.Fatalf("managed cutover unavailable: %+v", got)
			}
			detail.Labels["io.just-dashboard.release-id"] = "999999"
			if got := observeReserved(t, observer, dependency); got.Available {
				t.Fatalf("foreign release accepted: %+v", got)
			}
			detail.Labels["io.just-dashboard.release-id"] = strconv.FormatInt(currentID, 10)
			native.available = false
			if got := observeReserved(t, observer, dependency); !got.Available || ((resourceKind == "pm2_process" || resourceKind == "systemd_unit") && got.Warning == "") {
				t.Fatalf("native recovery loss not reported separately: %+v", got)
			}
			if runtimeKind == "pm2" || runtimeKind == "systemd" {
				return
			}
			metadata, _ = json.Marshal(map[string]any{"version": 1, "adopted": true, "containerIds": []string{containerID}})
			if _, err := s.Store.DB.Exec(`DELETE FROM deploy_release_runtimes WHERE release_id=?`, baselineID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Store.DB.Exec(`INSERT INTO deploy_release_runtimes(release_id,environment_id,kind,runtime_id,state,metadata_json,created_at,updated_at) VALUES(?,?,'compose','managed-project','live',?,0,0)`, baselineID, environmentID, string(metadata)); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Store.DB.Exec(`UPDATE deploy_environments SET live_release_id=? WHERE id=?`, baselineID, environmentID); err != nil {
				t.Fatal(err)
			}
			detail.Labels["io.just-dashboard.release-id"] = strconv.FormatInt(baselineID, 10)
			if got := observeReserved(t, observer, dependency); !got.Available {
				t.Fatalf("recreated baseline unavailable: %+v", got)
			}
		})
	}
}

func TestOverviewCheckUsesRuntimeReservationInventoryAndBlocksMissingCurrentIdentity(t *testing.T) {
	c, s := newClient(t)
	id := strings.Repeat("a", 64)
	projectID, environmentID, _, dependency := seedRuntimeReservation(t, s, "docker_container", id, "container")
	present := true
	mutations := 0
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			mutations++
			w.WriteHeader(405)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.Header().Set("API-Version", "1.47")
			fmt.Fprint(w, "OK")
		case strings.Contains(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/json"):
			if !present {
				w.WriteHeader(404)
				fmt.Fprint(w, `{"message":"not found"}`)
				return
			}
			fmt.Fprintf(w, `{"Id":%q,"State":{"Status":"exited"},"Config":{"Image":"fixture:local","Labels":{}},"HostConfig":{},"NetworkSettings":{"Networks":{}}}`, id)
		case strings.Contains(r.URL.Path, "/images/") && strings.HasSuffix(r.URL.Path, "/json"):
			fmt.Fprintf(w, `{"Id":%q,"Os":"linux","Architecture":"amd64","Config":{}}`, "sha256:"+strings.Repeat("b", 64))
		case strings.HasSuffix(r.URL.Path, "/history"):
			fmt.Fprint(w, `[]`)
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			fmt.Fprint(w, `[]`)
		default:
			w.WriteHeader(404)
			fmt.Fprint(w, `{"message":"not found"}`)
		}
	}))
	defer daemon.Close()
	client := dockerx.New(daemon.URL)
	defer client.Close()
	observer := deploy.NewHostPreflightObserver(s.Cfg.DeployRoots, s.Cfg.DataDir, client).WithDependencies(newDeploymentDependencyObserver(s.Store, nil, client))
	for _, available := range []bool{true, false} {
		present = available
		s.modules.deployChecker = deploy.NewDeploymentChecker(s.modules.deployRuns, s.modules.deployPlanning, nil, observer)
		response := c.do(http.MethodPost, fmt.Sprintf("/api/v1/deploy/%d/environments/%d/check", projectID, environmentID), `{}`, nil)
		if response.Code != 200 {
			t.Fatalf("check %d %s", response.Code, response.Body.String())
		}
		var result deploy.DeploymentCheckResult
		decodeDeployResponse(t, response.Body.Bytes(), &result)
		found := false
		for _, finding := range result.Findings {
			if finding.FieldID == "dependencies.runtime."+dependency.ResourceID {
				if available && finding.Code == "dependency_available" && finding.Severity == deploy.PreflightPass {
					found = true
				}
				if !available && finding.Code == "runtime_unavailable" && finding.Severity == deploy.PreflightBlocked {
					found = true
				}
			}
		}
		if !found {
			t.Fatalf("actual Overview check lacks authoritative result: %+v", result.Findings)
		}
	}
	if mutations != 0 {
		t.Fatalf("read-only preflight mutated Docker %d times", mutations)
	}
}
