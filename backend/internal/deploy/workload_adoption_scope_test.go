package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
)

func scopedComposeFixture(t *testing.T) (WorkloadCandidate, *adoptionReaderFake, *files.Service, string) {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, "compose.yml")
	if err := os.WriteFile(source, []byte("services:\n  web: {image: example/web:latest}\n  worker: {image: example/worker:latest}\n  missing: {image: example/unavailable:latest}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	web, worker := adoptionCaptureFixture(t, "web", true), adoptionCaptureFixture(t, "worker", false)
	for name, capture := range map[string]*dockerx.AdoptionContainer{"web": web, "worker": worker} {
		capture.Inspection.Config.Labels = map[string]string{"com.docker.compose.project": "original", "com.docker.compose.service": name, "com.docker.compose.container-number": "1", "com.docker.compose.project.working_dir": root, "com.docker.compose.project.config_files": source}
	}
	reader := &adoptionReaderFake{captures: map[string]*dockerx.AdoptionContainer{web.Inspection.ID: web, worker.Inspection.ID: worker}, compose: []byte(`{"services":{"web":{"image":"example/web:latest"},"worker":{"image":"example/worker:latest"},"missing":{"image":"example/unavailable:latest","environment":{"PASSWORD":"excluded-private-value"}}},"volumes":{"unused":{"name":"unavailable_unused_volume"}},"networks":{"unused":{"name":"unavailable_unused_network"}}}`)}
	candidate := WorkloadCandidate{Key: "stack:original", Kind: "stack", Name: "original", ResourceID: "original", Total: 3, Running: 1, Services: []WorkloadService{{Name: "web", ResourceID: web.Inspection.ID}, {Name: "worker", ResourceID: worker.Inspection.ID}, {Name: "missing"}}}
	return candidate, reader, files.New([]string{root}), filepath.Join(root, "recovery")
}

func TestRecoveryScopeExplicitlyExcludesOnlyAbsentServicesAndFencesOriginalConfiguration(t *testing.T) {
	candidate, reader, paths, root := scopedComposeFixture(t)
	all, err := RecoverDockerWorkload(context.Background(), candidate, reader, paths, root)
	if !errors.Is(err, ErrRecoveryBlocked) || len(all.Adoption.ExcludedServices) != 0 || all.Adoption.Scope != RecoveryAllServices {
		t.Fatal("default recovery silently excluded missing image")
	}
	scoped, err := RecoverDockerWorkloadWithScope(context.Background(), candidate, reader, paths, root, RecoveryExistingServices)
	if err != nil {
		t.Fatal("scoped recovery failed", err, scoped.Adoption.Blockers)
	}
	if scoped.Adoption.ServiceCount != 2 || strings.Join(scoped.Adoption.ExcludedServices, ",") != "missing" || scoped.Adoption.Scope != RecoveryExistingServices {
		t.Fatal("incorrect explicit scope metadata")
	}
	var metadata dockerReleaseRuntimeMetadata
	json.Unmarshal(scoped.Adoption.Runtime.Metadata, &metadata)
	if len(metadata.BaselineContainers) != 2 || metadata.BaselineContainers[0].Running == metadata.BaselineContainers[1].Running {
		t.Fatal("scope dropped stopped/running original shape")
	}
	if strings.Contains(scoped.Source.ComposeFiles[0].Content, "unavailable") || strings.Contains(scoped.Source.ComposeFiles[0].Content, "excluded-private-value") {
		t.Fatal("excluded service or resources remain in managed recipe")
	}
	acknowledgement := false
	for _, issue := range scoped.Adoption.Issues {
		if issue.Code == "compose_services_excluded" && issue.Service == "missing" && !issue.Blocking {
			acknowledgement = true
		}
	}
	if !acknowledgement {
		t.Fatal("scope exclusions have no review acknowledgement")
	}
	fixture := newPlanningStoreFixture(t)
	draft, err := fixture.plans.CreateRecoveredDraft(t.Context(), 41, "operator", DraftIntentConfig{Name: "scoped-original", Profile: ProfileCompose}, scoped)
	if err != nil {
		t.Fatal(err)
	}
	configuration := *draft.Data.Configuration
	configuration.Runtime.MemoryMB = 64
	draft, err = fixture.plans.Save(t.Context(), draft.ID, 41, false, DraftSaveRequest{Revision: draft.Revision, Step: DraftConfiguration, Configuration: &configuration})
	if err != nil || draft.Data.Adoption.Scope != RecoveryExistingServices || strings.Join(draft.Data.Adoption.ExcludedServices, ",") != "missing" || draft.Data.Adoption.BaselineDigest != scoped.Adoption.BaselineDigest {
		t.Fatal("review save replaced server-owned scope or original baseline", err)
	}
	var raw, originalEncrypted, desiredEncrypted string
	if err := fixture.store.DB.QueryRow(`SELECT data_json,adoption_enc,environment_enc FROM deploy_drafts WHERE id=?`, draft.ID).Scan(&raw, &originalEncrypted, &desiredEncrypted); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{raw, originalEncrypted, desiredEncrypted} {
		if strings.Contains(value, "sensitive-original-value") || strings.Contains(value, "excluded-private-value") {
			t.Fatal("scope save retained unsealed original secret")
		}
	}
	reader.compose = []byte(strings.ReplaceAll(string(reader.compose), "excluded-private-value", "changed-excluded-private-value"))
	fresh, err := RecoverDockerWorkloadWithScope(context.Background(), candidate, reader, paths, root, RecoveryExistingServices)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Adoption.BaselineDigest == scoped.Adoption.BaselineDigest || fresh.Adoption.OriginalConfigurationDigest == scoped.Adoption.OriginalConfigurationDigest {
		t.Fatal("excluded original settings change escaped fresh adoption fence")
	}
	public, _ := json.Marshal(fresh)
	if strings.Contains(string(public), "changed-excluded-private-value") {
		t.Fatal("excluded original secret leaked into public draft")
	}
}

func TestRecoveryScopeBlocksExcludedServiceRelationships(t *testing.T) {
	for _, relation := range []string{`"depends_on":["missing"]`, `"depends_on":{"missing":{"condition":"service_started"}}`, `"links":["missing:alias"]`, `"volumes_from":["missing:ro"]`, `"network_mode":"service:missing"`, `"pid":"service:missing"`, `"ipc":"service:missing"`, `"build":{"additional_contexts":{"base":"service:missing"}}`, `"build":{"additional_contexts":["base=service:missing"]}`} {
		t.Run(strings.Split(relation, ":")[0], func(t *testing.T) {
			candidate, reader, paths, root := scopedComposeFixture(t)
			var model map[string]any
			json.Unmarshal(reader.compose, &model)
			var addition map[string]any
			json.Unmarshal([]byte("{"+relation+"}"), &addition)
			for key, value := range addition {
				object(object(model["services"])["web"])[key] = value
			}
			reader.compose = mustJSON(model)
			result, err := RecoverDockerWorkloadWithScope(context.Background(), candidate, reader, paths, root, RecoveryExistingServices)
			if !errors.Is(err, ErrRecoveryBlocked) {
				t.Fatal("excluded dependency semantics changed silently", err)
			}
			found := false
			for _, issue := range result.Adoption.Issues {
				if issue.Code == "scope_dependency_excluded" && issue.Service == "web" && issue.Blocking {
					found = true
				}
			}
			if !found {
				t.Fatal("no actionable dependency scope blocker")
			}
		})
	}
}

func TestRecoveryScopeCannotHideFailedContainerCaptureOrApplyToOtherManagers(t *testing.T) {
	candidate, reader, paths, root := scopedComposeFixture(t)
	delete(reader.captures, candidate.Services[1].ResourceID)
	result, err := RecoverDockerWorkloadWithScope(context.Background(), candidate, reader, paths, root, RecoveryExistingServices)
	if !errors.Is(err, ErrRecoveryBlocked) || strings.Contains(strings.Join(result.Adoption.ExcludedServices, ","), "worker") {
		t.Fatal("failed existing container capture was silently excluded")
	}
	for _, kind := range []string{"container", "pm2", "systemd", "process"} {
		if RecoveryExistingServices.ValidForKind(kind) {
			t.Fatal("scope applied outside Compose")
		}
	}
	if WorkloadRecoveryScope("unknown").ValidForKind("stack") || WorkloadRecoveryScope("").Normalized() != RecoveryAllServices {
		t.Fatal("scope validation/backward compatibility failed")
	}
}

type composeDiagnosticReader struct{ *dockerx.Client }

func (r composeDiagnosticReader) CaptureAdoptionContainer(ctx context.Context, id string) (*dockerx.AdoptionContainer, error) {
	capture, err := r.Client.CaptureAdoptionContainer(ctx, id)
	if err == nil && capture.Image == nil {
		// Diagnostic-only pin: no export/import, build, registration or runtime
		// action occurs. This isolates parsing from a missing image store.
		capture.Image = &dockerx.ImageDetail{ID: capture.Inspection.Image, OS: "linux", Architecture: "amd64"}
		capture.MissingImage = false
	}
	return capture, err
}

func TestLiveComposeRecoveryParserDiagnostics(t *testing.T) {
	project := os.Getenv("JD_COMPOSE_ADOPTION_DIAGNOSTIC_PROJECT")
	if project == "" {
		t.Skip("set JD_COMPOSE_ADOPTION_DIAGNOSTIC_PROJECT for read-only original Compose parsing diagnostics")
	}
	client := dockerx.New("unix:///var/run/docker.sock")
	containers, err := client.ListContainersWithLabels(t.Context(), map[string]string{"com.docker.compose.project": project})
	if err != nil || len(containers) == 0 {
		t.Fatal("original project containers unavailable")
	}
	candidate := WorkloadCandidate{Key: "stack:" + project, Kind: "stack", ResourceID: project, Name: project, Total: len(containers)}
	for _, current := range containers {
		candidate.Services = append(candidate.Services, WorkloadService{Name: current.Labels["com.docker.compose.service"], ResourceID: current.ID})
		if current.State == "running" {
			candidate.Running++
		}
	}
	for _, scope := range []WorkloadRecoveryScope{RecoveryAllServices, RecoveryExistingServices} {
		result, err := RecoverDockerWorkloadWithScope(t.Context(), candidate, composeDiagnosticReader{client}, files.New([]string{"/home/ubuntu"}), t.TempDir(), scope)
		if result == nil {
			t.Fatal("read-only diagnostic capture failed")
		}
		codes := []string{}
		for _, issue := range result.Adoption.Issues {
			if issue.Blocking {
				codes = append(codes, issue.Code+":"+issue.Service+":"+issue.Field)
			}
		}
		_, parseErr := analyzeComposeDocuments(result.Source.ComposeFiles)
		category := "valid"
		if parseErr != nil {
			category = "other_invalid_compose"
			for _, match := range []struct{ text, code string }{{"build context", "build_context"}, {"scoped variable", "unsealed_field"}, {"argv", "credential_arguments"}, {"no image", "missing_service_image"}, {"Dockerfile", "build_dockerfile"}} {
				if strings.Contains(parseErr.Error(), match.text) {
					category = match.code
					break
				}
			}
		}
		t.Logf("scope=%s blocked=%v blocker_codes=%v parser_category=%s excluded_services=%v", scope, errors.Is(err, ErrRecoveryBlocked), codes, category, result.Adoption.ExcludedServices)
		if scope == RecoveryExistingServices && err != nil {
			t.Fatal("scoped original Compose recovery still blocked; inspect sanitized codes above")
		}
	}
}
