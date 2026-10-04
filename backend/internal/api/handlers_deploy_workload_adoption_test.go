package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

type adoptionPreflightFake struct{}

func (adoptionPreflightFake) Observe(context.Context, deploy.ObservationRequest) (deploy.HostObservation, error) {
	return deploy.HostObservation{Facilities: map[string]deploy.FacilityObservation{"docker": {Available: true}, "compose": {Available: true}}}, nil
}

func TestWorkloadAdoptionAPIRecoversReviewsAndRegistersWithoutDockerMutations(t *testing.T) {
	s := testServer(t)
	root := t.TempDir()
	s.Cfg.DeployRoots = []string{root}
	s.Cfg.ComposeRoots = []string{root}
	id := strings.Repeat("a", 64)
	image := "sha256:" + strings.Repeat("b", 64)
	var mutations, version atomic.Int32
	version.Store(1)
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			mutations.Add(1)
			w.WriteHeader(500)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping") || r.URL.Path == "/_ping":
			w.Header().Set("API-Version", "1.47")
			fmt.Fprint(w, "OK")
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			if r.URL.Query().Get("filters") != "" {
				fmt.Fprint(w, `[]`)
				return
			}
			fmt.Fprintf(w, `[{"Id":%q,"Names":["/external-worker"],"Image":"fixture:local","State":"running","Created":1000,"Labels":{}}]`, id)
		case strings.Contains(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/json"):
			fmt.Fprintf(w, `{"Id":%q,"Name":"/external-worker","Image":%q,"Created":"2026-10-04T00:00:00Z","State":{"Running":true,"Status":"running"},"Config":{"Image":"fixture:local","Env":["API_TOKEN=private-capture-%d"],"Cmd":["sleep","300"],"Labels":{}},"HostConfig":{"NetworkMode":"bridge","ReadonlyRootfs":true,"RestartPolicy":{"Name":"unless-stopped"},"LogConfig":{"Type":"json-file","Config":{}}},"Mounts":[],"NetworkSettings":{"Networks":{}}}`, id, image, version.Load())
		case strings.HasSuffix(r.URL.Path, "/changes"):
			fmt.Fprint(w, `[]`)
		case strings.Contains(r.URL.Path, "/images/") && strings.HasSuffix(r.URL.Path, "/json"):
			fmt.Fprintf(w, `{"Id":%q,"Os":"linux","Architecture":"amd64","Config":{},"RootFS":{"Layers":[]}}`, image)
		case strings.Contains(r.URL.Path, "/images/") && strings.HasSuffix(r.URL.Path, "/history"):
			fmt.Fprint(w, `[]`)
		default:
			w.WriteHeader(404)
			fmt.Fprint(w, `{"message":"not found"}`)
		}
	}))
	defer daemon.Close()
	s.modules.docker = dockerx.New(daemon.URL)
	defer s.modules.docker.Close()
	s.modules.pm2 = nil
	s.modules.systemd = nil
	s.modules.deployPlanning = deploy.NewPlanningStore(s.Store, s.Sealer, []string{root})
	s.modules.deploySources = deploy.NewHostSourceAnalyzer([]string{root}, []string{root}, filepath.Join(s.Cfg.DataDir, "detect"), s.modules.docker, s.modules.deployPlanning)
	s.modules.deployPreflight = adoptionPreflightFake{}
	c := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	inspected := doPlanningJSON(t, c, http.MethodPost, "/api/v1/deploy/import/inspect", map[string]any{"key": "container:" + id})
	if inspected.Code != 200 {
		t.Fatalf("inspect %d %s", inspected.Code, inspected.Body.String())
	}
	var candidate deploy.WorkloadCandidate
	decodePlanningResponse(t, inspected.Body.Bytes(), &candidate)
	response := doPlanningJSON(t, c, http.MethodPost, "/api/v1/deploy/import/recover", map[string]any{"key": candidate.Key, "digest": candidate.Digest, "name": "managed-worker"})
	if response.Code != 201 {
		t.Fatalf("recover %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "private-capture-") {
		t.Fatal("capture leaked into recovery JSON")
	}
	var draft deploy.Draft
	decodePlanningResponse(t, response.Body.Bytes(), &draft)
	if draft.Data.Adoption == nil || draft.Data.Source.Kind != deploy.SourceCompose {
		t.Fatalf("not a managed source: %+v", draft.Data)
	}
	checked := doPlanningJSON(t, c, http.MethodPost, "/api/v1/deploy/drafts/"+draft.ID+"/preflight", map[string]any{"revision": draft.Revision})
	if checked.Code != 200 {
		t.Fatalf("preflight %d %s", checked.Code, checked.Body.String())
	}
	var review struct {
		Draft deploy.Draft `json:"draft"`
	}
	decodePlanningResponse(t, checked.Body.Bytes(), &review)
	draft = review.Draft
	ack := []string{}
	for _, finding := range draft.Findings {
		if finding.Severity == deploy.PreflightWarning {
			ack = append(ack, finding.Code)
		}
	}
	bypass := doPlanningJSON(t, c, http.MethodPost, "/api/v1/deploy/drafts/"+draft.ID+"/commit", deploy.DraftCommitRequest{Revision: draft.Revision, AcknowledgedWarnings: ack})
	if bypass.Code != 400 || !strings.Contains(bypass.Body.String(), "import_adopt_required") {
		t.Fatalf("bypass %d %s", bypass.Code, bypass.Body.String())
	}
	version.Store(2)
	request := importAdoptRequest{DraftID: draft.ID, Revision: draft.Revision, AcknowledgedWarnings: ack}
	stale := doPlanningJSON(t, c, http.MethodPost, "/api/v1/deploy/import/adopt", request)
	if stale.Code != 409 || !strings.Contains(stale.Body.String(), "workload_changed") {
		t.Fatalf("stale capture %d %s", stale.Code, stale.Body.String())
	}
	var projects int
	if err := s.Store.DB.QueryRow(`SELECT count(*) FROM deploy_projects`).Scan(&projects); err != nil || projects != 0 {
		t.Fatalf("failed import changed records: %d %v", projects, err)
	}
	version.Store(1)
	adopted := doPlanningJSON(t, c, http.MethodPost, "/api/v1/deploy/import/adopt", request)
	if adopted.Code != 201 {
		t.Fatalf("adopt %d %s", adopted.Code, adopted.Body.String())
	}
	var result deploy.DraftCommitResult
	decodePlanningResponse(t, adopted.Body.Bytes(), &result)
	repeat := doPlanningJSON(t, c, http.MethodPost, "/api/v1/deploy/import/adopt", request)
	if repeat.Code != 200 {
		t.Fatalf("repeat %d %s", repeat.Code, repeat.Body.String())
	}
	var queued int
	if err := s.Store.DB.QueryRow(`SELECT count(*) FROM deploy_runs WHERE state NOT IN ('succeeded','failed','cancelled')`).Scan(&queued); err != nil || queued != 0 {
		t.Fatalf("queued %d %v", queued, err)
	}
	s.modules.deployRuns = deploy.NewOrchestrationStore(s.Store)
	live, err := s.modules.deployRuns.LiveRelease(t.Context(), result.EnvironmentID)
	if err != nil || live.Release.State != "live" || !live.Release.Pinned {
		t.Fatalf("live %+v %v", live, err)
	}
	variables, err := s.modules.deployPlanning.OpenRunScopedVariables(t.Context(), live.Release.RunID, result.EnvironmentID, "runtime")
	if err != nil || len(variables) == 0 || variables[0].Value != "private-capture-1" {
		t.Fatalf("baseline values missing: %v", err)
	}
	if mutations.Load() != 0 {
		t.Fatalf("recovery mutated Docker %d times", mutations.Load())
	}
	// Settings edits use the ordinary source endpoint, but can only change
	// the desired recipe. The captured release and its sealed values remain
	// the rollback baseline even when the operator changes an image or command.
	base := fmt.Sprintf("/api/v1/deploy/%d/environments/%d", result.ProjectID, result.EnvironmentID)
	configuration, err := s.modules.deployPlanning.EnvironmentConfiguration(t.Context(), result.ProjectID, result.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	changedSource := *draft.Data.Source
	changedSource.ComposeFiles = append([]deploy.ComposeDocument(nil), changedSource.ComposeFiles...)
	changedSource.ComposeFiles[0].Content = "services: [broken YAML"
	invalid := doPlanningJSON(t, c, http.MethodPut, base+"/source", deploymentSourceUpdateRequest{Revision: configuration.Revision, DraftSourceConfig: changedSource})
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid source edit %d %s", invalid.Code, invalid.Body.String())
	}
	unchanged, err := s.modules.deployPlanning.EnvironmentConfiguration(t.Context(), result.ProjectID, result.EnvironmentID)
	if err != nil || unchanged.Revision != configuration.Revision {
		t.Fatalf("refused source advanced revision: %+v %v", unchanged, err)
	}
	changedSource.ComposeFiles[0].Content = strings.Replace(draft.Data.Source.ComposeFiles[0].Content, "300", "301", 1)
	if changedSource.ComposeFiles[0].Content == draft.Data.Source.ComposeFiles[0].Content {
		t.Fatal("fixture command was not captured")
	}
	edited := doPlanningJSON(t, c, http.MethodPut, base+"/source", deploymentSourceUpdateRequest{Revision: configuration.Revision, DraftSourceConfig: changedSource})
	if edited.Code != http.StatusOK {
		t.Fatalf("valid source edit %d %s", edited.Code, edited.Body.String())
	}
	pending, err := s.modules.deployPlanning.PendingState(t.Context(), result.ProjectID, result.EnvironmentID)
	if err != nil || !pending.Pending {
		t.Fatalf("source edit was not pending: %+v %v", pending, err)
	}
	liveAfter, err := s.modules.deployRuns.LiveRelease(t.Context(), result.EnvironmentID)
	if err != nil || liveAfter.Release.ID != live.Release.ID || string(liveAfter.Release.Provenance) != string(live.Release.Provenance) || liveAfter.Release.ConfigDigest != live.Release.ConfigDigest {
		t.Fatalf("source edit replaced the live baseline: %v", err)
	}
	variablesAfter, err := s.modules.deployPlanning.OpenRunScopedVariables(t.Context(), live.Release.RunID, result.EnvironmentID, "runtime")
	if err != nil || len(variablesAfter) != len(variables) || variablesAfter[0].Value != variables[0].Value {
		t.Fatalf("source edit changed original private settings: %v", err)
	}
	if mutations.Load() != 0 {
		t.Fatalf("source review mutated Docker %d times", mutations.Load())
	}
}
