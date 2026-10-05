package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// buildSourceProjectForDuplication commits a project with a domain, a
// managed Docker volume and both a plain and a secret variable, so a
// duplicate test can assert all three are handled the way the brief
// describes rather than copied byte for byte.
func buildSourceProjectForDuplication(t *testing.T, plans *PlanningStore, ownerID int64, name string) int64 {
	t.Helper()
	ctx := context.Background()
	draft, err := plans.Create(ctx, ownerID, "tester")
	if err != nil {
		t.Fatal(err)
	}
	draft, err = plans.Save(ctx, draft.ID, ownerID, true, DraftSaveRequest{
		Revision: draft.Revision, Step: DraftIntent,
		Intent: &DraftIntentConfig{Name: name, Profile: ProfileWorker},
	})
	if err != nil {
		t.Fatal(err)
	}
	draft, err = plans.Save(ctx, draft.ID, ownerID, true, DraftSaveRequest{
		Revision: draft.Revision, Step: DraftSource,
		Source: &DraftSourceConfig{
			Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://example.test/owner/" + name + ".git", Ref: "main",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := newDetectedCandidate("", BuildNone, DetectedCandidate{
		Name: name, Profile: ProfileWorker, Confidence: ConfidenceHigh,
		Evidence: []DetectionEvidence{{Path: "go.mod", Reason: "fixture"}}, NeedsDecision: []string{},
	})
	draft, err = plans.SaveDetection(ctx, draft.ID, ownerID, true, draft.Revision, DetectionResult{
		Source: SourceIdentity{
			Kind: SourceGit, Remote: draft.Data.Source.URL, Repository: "owner/" + name,
			Ref: "main", Revision: strings.Repeat("a", 40),
		},
		Candidates: []DetectedCandidate{candidate}, SelectedID: candidate.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	configuration := PlanConfiguration{
		Build: BuildPlanConfig{Method: BuildNone},
		Runtime: RuntimePlanConfig{
			Strategy: StrategyStopFirst,
			Mounts:   []RuntimeMount{{Source: "orig-" + name + "-data", Target: "/data", Ownership: OwnershipManaged}},
		},
		Variables: []PlannedVariable{
			{Name: "PLAIN_VAR", Sensitivity: "plain", Scopes: []string{"runtime"}, Value: "hello"},
			{Name: "SECRET_VAR", Sensitivity: "secret", Scopes: []string{"runtime"}, Generate: 32},
		},
		Dependencies: []PlannedDependency{
			{Kind: "storage", Ownership: OwnershipManaged, ResourceKind: "docker_volume", ResourceID: "orig-" + name + "-data"},
		},
		Checks: []PlannedCheck{{
			Name: "ready", Kind: "command", Phase: "smoke", Required: true, Config: json.RawMessage(`{"command":["true"]}`),
		}},
		Domains: []PlannedDomain{{Hostname: name + ".example.test", HTTPS: true, Ownership: OwnershipManaged}},
	}
	draft, err = plans.Save(ctx, draft.ID, ownerID, true, DraftSaveRequest{
		Revision: draft.Revision, Step: DraftConfiguration, Configuration: &configuration,
	})
	if err != nil {
		t.Fatal(err)
	}
	preflight, err := PreflightDraft(ctx, draft, &preflightObserverFake{observation: HostObservation{
		Facilities: map[string]FacilityObservation{"git": {Available: true}}, Paths: []PathObservation{}, Ports: []PortObservation{},
	}}, true)
	if err != nil {
		t.Fatal(err)
	}
	draft, err = plans.SavePreflight(ctx, draft.ID, ownerID, true, draft.Revision, preflight)
	if err != nil {
		t.Fatal(err)
	}
	result, err := plans.Commit(ctx, draft.ID, ownerID, true, DraftCommitRequest{Revision: draft.Revision, AcknowledgedWarnings: []string{"backup_policy_missing"}})
	if err != nil {
		t.Fatal(err)
	}
	return result.ProjectID
}

func TestDuplicateDropsDomainsFlattensVariablesAndRederivesManagedVolumes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newPlanningStoreFixture(t)
	sourceProjectID := buildSourceProjectForDuplication(t, fixture.plans, 7, "orig-app")

	draft, err := fixture.plans.Duplicate(ctx, sourceProjectID, 7, "tester", "dup-app")
	if err != nil {
		t.Fatal(err)
	}
	if draft.Data.Intent == nil || draft.Data.Intent.Name != "dup-app" || draft.Data.Intent.Profile != ProfileWorker {
		t.Fatalf("duplicated intent = %#v", draft.Data.Intent)
	}
	if draft.Data.Source == nil || draft.Data.Source.Kind != SourceGit || draft.Data.Source.URL != "https://example.test/owner/orig-app.git" {
		t.Fatalf("duplicated source = %#v", draft.Data.Source)
	}
	if draft.CurrentStep != DraftConfiguration || draft.Data.Detection != nil {
		t.Fatalf("duplicate should land mid-wizard awaiting detect: step=%s detection=%v", draft.CurrentStep, draft.Data.Detection)
	}
	configuration := draft.Data.Configuration
	if configuration == nil {
		t.Fatal("duplicated configuration is nil")
	}
	if len(configuration.Domains) != 0 {
		t.Fatalf("duplicated domains = %#v, want none: a hostname belongs to one project", configuration.Domains)
	}
	if len(configuration.Variables) != 2 {
		t.Fatalf("duplicated variables = %#v", configuration.Variables)
	}
	for _, variable := range configuration.Variables {
		if !variable.Required || variable.Value != "" || variable.Reference != "" || variable.Generate != 0 {
			t.Fatalf("variable %q was not flattened to a blank required declaration: %#v", variable.Name, variable)
		}
	}
	if len(configuration.Runtime.Mounts) != 1 || len(configuration.Dependencies) != 1 {
		t.Fatalf("duplicated mounts/dependencies = %#v / %#v", configuration.Runtime.Mounts, configuration.Dependencies)
	}
	newVolumeName := configuration.Runtime.Mounts[0].Source
	if newVolumeName == "orig-orig-app-data" || newVolumeName == "" {
		t.Fatalf("managed volume name was not re-derived: %q", newVolumeName)
	}
	if configuration.Dependencies[0].ResourceID != newVolumeName {
		t.Fatalf("mount and its storage dependency disagree after re-derivation: mount=%q dependency=%q",
			newVolumeName, configuration.Dependencies[0].ResourceID)
	}
	// Re-derivation is deterministic for the same new name and old volume, so
	// duplicating the same source twice under the same name does not
	// generate two different volumes for what would be the same mount.
	again, err := fixture.plans.Duplicate(ctx, sourceProjectID, 7, "tester", "dup-app")
	if err != nil {
		t.Fatal(err)
	}
	if again.Data.Configuration.Runtime.Mounts[0].Source != newVolumeName {
		t.Fatalf("re-derivation was not deterministic: %q vs %q", again.Data.Configuration.Runtime.Mounts[0].Source, newVolumeName)
	}
}

// TestDuplicateCommitsIntoAnIndependentProject drives the resulting draft
// through the same detect/preflight/commit sequence any draft uses, and
// checks the new project is independent: its own row, its own volume, no
// domain carried over, and the source project untouched.
func TestDuplicateCommitsIntoAnIndependentProject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newPlanningStoreFixture(t)
	sourceProjectID := buildSourceProjectForDuplication(t, fixture.plans, 3, "orig-app")

	draft, err := fixture.plans.Duplicate(ctx, sourceProjectID, 3, "tester", "dup-app")
	if err != nil {
		t.Fatal(err)
	}
	// The operator fills in the values the duplicate marked required before
	// a required-but-blank variable can pass preflight — exactly the review
	// step the brief describes, not a value carried over from the source.
	filledIn := *draft.Data.Configuration
	filledIn.Variables = []PlannedVariable{
		{Name: "PLAIN_VAR", Sensitivity: "plain", Scopes: []string{"runtime"}, Value: "duplicate-value"},
		{Name: "SECRET_VAR", Sensitivity: "secret", Scopes: []string{"runtime"}, Generate: 32},
	}
	draft, err = fixture.plans.Save(ctx, draft.ID, 3, true, DraftSaveRequest{
		Revision: draft.Revision, Step: DraftConfiguration, Configuration: &filledIn,
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := newDetectedCandidate("", BuildNone, DetectedCandidate{
		Name: "dup-app", Profile: ProfileWorker, Confidence: ConfidenceHigh,
		Evidence: []DetectionEvidence{{Path: "go.mod", Reason: "fixture"}}, NeedsDecision: []string{},
	})
	// The duplicated source still names the original repository — a
	// duplicate copies the source as-is; the operator edits it in the wizard
	// if the new project actually lives somewhere else.
	draft, err = fixture.plans.SaveDetection(ctx, draft.ID, 3, true, draft.Revision, DetectionResult{
		Source: SourceIdentity{
			Kind: SourceGit, Remote: draft.Data.Source.URL, Repository: "owner/orig-app",
			Ref: "main", Revision: strings.Repeat("b", 40),
		},
		Candidates: []DetectedCandidate{candidate}, SelectedID: candidate.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	preflight, err := PreflightDraft(ctx, draft, &preflightObserverFake{observation: HostObservation{
		Facilities: map[string]FacilityObservation{"git": {Available: true}}, Paths: []PathObservation{}, Ports: []PortObservation{},
	}}, true)
	if err != nil {
		t.Fatal(err)
	}
	draft, err = fixture.plans.SavePreflight(ctx, draft.ID, 3, true, draft.Revision, preflight)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fixture.plans.Commit(ctx, draft.ID, 3, true, DraftCommitRequest{Revision: draft.Revision, AcknowledgedWarnings: []string{"backup_policy_missing"}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Created || result.ProjectID == sourceProjectID {
		t.Fatalf("commit result = %#v, want a new project distinct from %d", result, sourceProjectID)
	}
	newConfiguration, err := fixture.plans.EnvironmentConfiguration(ctx, result.ProjectID, result.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(newConfiguration.Domains) != 0 {
		t.Fatalf("committed duplicate carries a domain: %#v", newConfiguration.Domains)
	}
	var sourceEnvironmentID int64
	if err := fixture.store.DB.QueryRow(`SELECT id FROM deploy_environments WHERE project_id = ? AND slug = 'production'`, sourceProjectID).
		Scan(&sourceEnvironmentID); err != nil {
		t.Fatal(err)
	}
	sourceConfiguration, err := fixture.plans.EnvironmentConfiguration(ctx, sourceProjectID, sourceEnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sourceConfiguration.Domains) != 1 || sourceConfiguration.Domains[0].Hostname != "orig-app.example.test" {
		t.Fatalf("source project's own domain was disturbed by duplication: %#v", sourceConfiguration.Domains)
	}
	if newConfiguration.Runtime.Mounts[0].Source == sourceConfiguration.Runtime.Mounts[0].Source {
		t.Fatalf("duplicate and source ended up sharing the same managed volume: %q", newConfiguration.Runtime.Mounts[0].Source)
	}
}

func TestDuplicateRefusesAnUnknownProjectAndAnInvalidName(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newPlanningStoreFixture(t)
	if _, err := fixture.plans.Duplicate(ctx, 999999, 1, "tester", "whatever"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("duplicate of an unknown project error = %v, want ErrNotFound", err)
	}
	sourceProjectID := buildSourceProjectForDuplication(t, fixture.plans, 1, "orig-app2")
	if _, err := fixture.plans.Duplicate(ctx, sourceProjectID, 1, "tester", "not a valid name!"); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("duplicate with an invalid name error = %v, want ErrInvalidPlan", err)
	}
}

func TestDuplicateDropsAdoptedRuntimeAuthorityAndNamespace(t *testing.T) {
	fixture := newPlanningStoreFixture(t)
	recovered := recoveredStoreFixture(t)
	recovered.Configuration.Runtime.ComposeProjectName = "original-stack"
	recovered.Adoption.BaselineConfiguration = recovered.Configuration
	recovered.Adoption.Kind, recovered.Adoption.ResourceID = "stack", "original-stack"
	recovered.Adoption.Runtime.Kind, recovered.Adoption.Runtime.RuntimeID = "compose", "original-stack"
	draft, err := fixture.plans.CreateRecoveredDraft(t.Context(), 41, "tester", DraftIntentConfig{Name: "adopted", Profile: ProfileCompose}, recovered)
	if err != nil {
		t.Fatal(err)
	}
	draft = checkRecoveredDraft(t, fixture, draft)
	ack := []string{}
	for _, finding := range draft.Findings {
		if finding.Severity == PreflightWarning {
			ack = append(ack, finding.Code)
		}
	}
	committed, err := fixture.plans.Commit(t.Context(), draft.ID, 41, false, DraftCommitRequest{Revision: draft.Revision, AcknowledgedWarnings: ack})
	if err != nil {
		t.Fatal(err)
	}
	draft, err = fixture.plans.Duplicate(t.Context(), committed.ProjectID, 41, "tester", "independent")
	if err != nil {
		t.Fatal(err)
	}
	if draft.Data.Adoption != nil || draft.Data.Configuration.Runtime.ComposeProjectName != "" {
		t.Fatal("duplicate retained the original adoption or Compose namespace")
	}
	for _, dependency := range draft.Data.Configuration.Dependencies {
		if dependency.Kind == "runtime" {
			t.Fatal("duplicate retained the original runtime reservation")
		}
	}
	var reservations int
	if err := fixture.store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_dependencies WHERE kind='runtime' AND resource_id='original-stack'`).Scan(&reservations); err != nil || reservations != 1 {
		t.Fatalf("original runtime authority changed: count=%d, err=%v", reservations, err)
	}
}

func TestDuplicateRefusesWritableExistingMountsBeforeCreatingADraft(t *testing.T) {
	fixture := newPlanningStoreFixture(t)
	projectID := buildSourceProjectForDuplication(t, fixture.plans, 7, "linked-app")
	var environmentID int64
	if err := fixture.store.DB.QueryRow(`SELECT id FROM deploy_environments WHERE project_id=? AND slug='production'`, projectID).Scan(&environmentID); err != nil {
		t.Fatal(err)
	}
	for _, mount := range []RuntimeMount{
		{Source: "original-data", Target: "/data", Ownership: OwnershipLinked},
		{Source: "/srv/original-data", Target: "/data", Ownership: OwnershipManaged},
	} {
		configuration, err := fixture.plans.EnvironmentConfiguration(t.Context(), projectID, environmentID)
		if err != nil {
			t.Fatal(err)
		}
		runtime := RuntimePlanConfig{Strategy: StrategyStopFirst, Mounts: []RuntimeMount{mount}}
		if _, err := fixture.plans.SaveEnvironmentConfiguration(t.Context(), projectID, environmentID, ConfigurationWriteRequest{Revision: configuration.Revision, Build: configuration.Build, Runtime: runtime, Dependencies: configuration.Dependencies, Checks: configuration.Checks, Domains: configuration.Domains}); err != nil {
			t.Fatal(err)
		}
		var before, after int
		if err := fixture.store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_drafts`).Scan(&before); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.plans.Duplicate(t.Context(), projectID, 7, "tester", "independent"); !errors.Is(err, ErrInvalidPlan) || !strings.Contains(err.Error(), "independent storage") {
			t.Fatalf("shared writable storage was copied: %v", err)
		}
		if err := fixture.store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_drafts`).Scan(&after); err != nil || after != before {
			t.Fatalf("unsafe duplication created a draft: before=%d after=%d err=%v", before, after, err)
		}
	}
}

func TestDuplicateComposeIsolationAllowsScopedResourcesAndRefusesSharedIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		blocked       bool
	}{
		{"project-scoped volume", "services:\n  app:\n    image: nginx:alpine\n    volumes: [data:/data]\nvolumes:\n  data: {}\n", false},
		{"anonymous volume", "services:\n  app:\n    image: nginx:alpine\n    volumes: [/data]\n", false},
		{"fixed container", "services:\n  app:\n    image: nginx:alpine\n    container_name: original-app\n", true},
		{"external network aliases", "services:\n  app:\n    image: nginx:alpine\n    networks:\n      shared:\n        aliases: [original-app]\nnetworks:\n  shared:\n    external: true\n", true},
		{"fixed default network", "services:\n  app:\n    image: nginx:alpine\nnetworks:\n  default:\n    name: original_default\n", true},
		{"fixed volume", "services:\n  app:\n    image: nginx:alpine\n    volumes: [data:/data]\nvolumes:\n  data:\n    name: original-data\n", true},
		{"external writable volume", "services:\n  app:\n    image: nginx:alpine\n    volumes: [data:/data]\nvolumes:\n  data:\n    external: true\n", true},
		{"external read-only volume", "services:\n  app:\n    image: nginx:alpine\n    volumes: [data:/data:ro]\nvolumes:\n  data:\n    external: true\n", false},
		{"bind data", "services:\n  app:\n    image: nginx:alpine\n    volumes: [/srv/original:/data]\n", true},
		{"read-only file", "services:\n  app:\n    image: nginx:alpine\n    volumes: [/srv/config:/config:ro]\n", false},
		{"ambiguous writable mode", "services:\n  app:\n    image: nginx:alpine\n    volumes: ['/srv/original:/data:ro,rw']\n", true},
		{"merged fixed container", "x-runtime: &runtime\n  image: nginx:alpine\n  container_name: original-app\nservices:\n  app:\n    <<: *runtime\n", true},
		{"long bind data", "services:\n  app:\n    image: nginx:alpine\n    volumes:\n      - type: bind\n        source: /srv/original\n        target: /data\n", true},
		{"shared namespace", "services:\n  app:\n    image: nginx:alpine\n    network_mode: container:original-app\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := DraftSourceConfig{Kind: SourceCompose, Mode: SourceModeComposePaste, ComposeFiles: []ComposeDocument{{Path: "compose.yml", Content: tc.content}}}
			err := validateDuplicateStorageAndCompose(source, PlanConfiguration{Build: BuildPlanConfig{Method: BuildCompose}})
			if tc.blocked != errors.Is(err, ErrInvalidPlan) || (!tc.blocked && err != nil) {
				t.Fatalf("blocked=%v error=%v", tc.blocked, err)
			}
		})
	}
	// A normal override may attach a volume declared in another file.
	source := DraftSourceConfig{Kind: SourceCompose, Mode: SourceModeComposePaste, ComposeFiles: []ComposeDocument{
		{Path: "compose.yml", Content: "services:\n  app:\n    image: nginx:alpine\nvolumes:\n  data: {}\n"},
		{Path: "override.yml", Order: 1, Content: "services:\n  app:\n    volumes: [data:/data]\n"},
	}}
	if err := validateDuplicateStorageAndCompose(source, PlanConfiguration{Build: BuildPlanConfig{Method: BuildCompose}}); err != nil {
		t.Fatalf("project-scoped override volume refused: %v", err)
	}
}
