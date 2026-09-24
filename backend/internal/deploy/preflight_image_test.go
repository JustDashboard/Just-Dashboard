package deploy

import (
	"strings"
	"testing"
)

func imageFindingDraft(candidates []DetectedCandidate, build BuildPlanConfig, variables ...PlannedVariable) (*Draft, PlanConfiguration) {
	detection := &DetectionResult{Source: SourceIdentity{Kind: SourceGit}, Candidates: candidates}
	if len(candidates) > 0 {
		detection.SelectedID = candidates[0].ID
	}
	configuration := PlanConfiguration{Build: build, Variables: variables}
	return &Draft{Data: DraftData{
		Intent:        &DraftIntentConfig{Name: "app", Profile: ProfileWeb},
		Source:        &DraftSourceConfig{Kind: SourceGit, Mode: SourceModeGitURL},
		Detection:     detection,
		Configuration: &configuration,
	}}, configuration
}

func TestPreflightReportsDockerfileIssuesForThePlannedFile(t *testing.T) {
	dockerfile := newDetectedCandidate("", BuildDockerfile, DetectedCandidate{
		Name: "Dockerfile in .", Dockerfile: "Dockerfile", Confidence: ConfidenceHigh,
		ImageBuildIssues: []ImageBuildIssue{
			newImageBuildIssue("dockerfile_copy_source_missing", PreflightBlocked, 3, ".env", "line 3 COPY .env: not in the build context ."),
			newImageBuildIssue("dockerfile_copy_source_missing", PreflightBlocked, 4, "certs", "line 4 COPY certs: not in the build context ."),
			newImageBuildIssue("dockerfile_dev_server", PreflightWarning, 9, "Dockerfile", "line 9 of Dockerfile starts a file watcher"),
		},
	})
	draft, configuration := imageFindingDraft([]DetectedCandidate{dockerfile}, BuildPlanConfig{Method: BuildDockerfile, Dockerfile: "Dockerfile"})
	findings := imageBuildFindings(draft, configuration, HostObservation{})
	missing, ok := findingWithCode(findings, "dockerfile_copy_source_missing")
	if !ok || missing.Severity != PreflightBlocked || !strings.Contains(missing.Measured, ".env") || !strings.Contains(missing.Measured, "certs") {
		t.Fatalf("missing COPY sources = %+v", findings)
	}
	if dev, ok := findingWithCode(findings, "dockerfile_dev_server"); !ok || dev.Severity != PreflightWarning {
		t.Fatalf("dev server = %+v", findings)
	}
	if err := validatePreflightFindings(findings); err != nil {
		t.Fatal(err)
	}

	// A stage the file does not have stops buildx before it builds anything.
	staged := dockerfile
	staged.DockerfileStages = []string{"deps", "runner"}
	draft, configuration = imageFindingDraft([]DetectedCandidate{staged}, BuildPlanConfig{Method: BuildDockerfile, Dockerfile: "Dockerfile", Target: "production"})
	if item, ok := findingWithCode(imageBuildFindings(draft, configuration, HostObservation{}), "dockerfile_target_missing"); !ok ||
		item.Severity != PreflightBlocked || item.Measured != "production: its stages are deps, runner" || item.FieldID != "configuration.build.target" {
		t.Fatalf("missing stage = %+v", item)
	}
	configuration.Build.Target = "runner"
	if item, ok := findingWithCode(imageBuildFindings(draft, configuration, HostObservation{}), "dockerfile_target_missing"); ok {
		t.Fatalf("an existing stage = %+v", item)
	}

	// Another file is evidence about something detection did not read.
	draft, configuration = imageFindingDraft([]DetectedCandidate{dockerfile}, BuildPlanConfig{Method: BuildDockerfile, Dockerfile: "docker/prod.Dockerfile"})
	findings = imageBuildFindings(draft, configuration, HostObservation{})
	if _, ok := findingWithCode(findings, "dockerfile_copy_source_missing"); ok {
		t.Fatalf("issues of the detected file applied to another one: %+v", findings)
	}
	if unchecked, ok := findingWithCode(findings, "dockerfile_unchecked"); !ok || unchecked.Severity != PreflightWarning {
		t.Fatalf("a Dockerfile detection never read = %+v", findings)
	}

	// The recipe was chosen over a Dockerfile that would not build.
	recipe := newDetectedCandidate("", BuildRecipe, DetectedCandidate{Name: "web", Recipe: "node", Confidence: ConfidenceHigh})
	draft, configuration = imageFindingDraft([]DetectedCandidate{recipe, dockerfile}, BuildPlanConfig{Method: BuildRecipe, Recipe: "node"})
	if item, ok := findingWithCode(imageBuildFindings(draft, configuration, HostObservation{}), "dockerfile_not_selected"); !ok || !strings.Contains(item.Measured, "COPY .env") {
		t.Fatalf("Dockerfile not selected = %+v", item)
	}
}

func TestPreflightPassesOnlyPlainPublicBuildArguments(t *testing.T) {
	dockerfile := newDetectedCandidate("", BuildDockerfile, DetectedCandidate{
		Name: "Dockerfile in .", Dockerfile: "Dockerfile", Confidence: ConfidenceHigh,
		DockerfileArgs: []DockerfileArg{
			{Name: "NEXT_PUBLIC_API_URL", Consumed: true}, {Name: "NPM_TOKEN", Consumed: true},
			{Name: "VITE_SITE", Consumed: true}, {Name: "NODE_VERSION", HasDefault: true, UsedInFrom: true},
			{Name: "NEXT_PUBLIC_KEY", Consumed: true},
		},
	})
	draft, configuration := imageFindingDraft([]DetectedCandidate{dockerfile}, BuildPlanConfig{Method: BuildDockerfile},
		PlannedVariable{Name: "NEXT_PUBLIC_API_URL", Sensitivity: "plain", Scopes: []string{"build", "runtime"}},
		PlannedVariable{Name: "NPM_TOKEN", Sensitivity: "secret", Scopes: []string{"build"}},
		PlannedVariable{Name: "NEXT_PUBLIC_KEY", Sensitivity: "secret", Scopes: []string{"build"}},
	)
	findings := imageBuildFindings(draft, configuration, HostObservation{})
	passed, ok := findingWithCode(findings, "dockerfile_build_args")
	if !ok || passed.Measured != "NEXT_PUBLIC_API_URL" {
		t.Fatalf("passed build arguments = %+v", findings)
	}
	unpassed, ok := findingWithCode(findings, "dockerfile_arg_not_passed")
	if !ok || !strings.Contains(unpassed.Measured, "NPM_TOKEN") || !strings.Contains(unpassed.Measured, "VITE_SITE") ||
		!strings.Contains(unpassed.Measured, "NEXT_PUBLIC_KEY (marked secret") || strings.Contains(unpassed.Measured, "NODE_VERSION") {
		t.Fatalf("unpassed build arguments = %+v", unpassed)
	}
}

func TestPreflightNamesForeignArchitectureImages(t *testing.T) {
	dockerfile := newDetectedCandidate("", BuildDockerfile, DetectedCandidate{
		Name: "Dockerfile in .", Dockerfile: "Dockerfile", Confidence: ConfidenceHigh, DockerfilePlatforms: []string{"linux/amd64"},
	})
	draft, configuration := imageFindingDraft([]DetectedCandidate{dockerfile}, BuildPlanConfig{Method: BuildDockerfile})
	for _, fixture := range []struct {
		name        string
		observation HostObservation
		severity    PreflightSeverity
		found       bool
	}{
		{"same architecture", HostObservation{OS: "linux", Architecture: "amd64", EmulationObserved: true}, "", false},
		{"no emulator", HostObservation{OS: "linux", Architecture: "arm64", EmulationObserved: true}, PreflightBlocked, true},
		{"emulated", HostObservation{OS: "linux", Architecture: "arm64", EmulationObserved: true, Emulators: []string{"amd64"}}, PreflightWarning, true},
		{"binfmt unreadable", HostObservation{OS: "linux", Architecture: "arm64"}, PreflightWarning, true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			item, found := findingWithCode(imageBuildFindings(draft, configuration, fixture.observation), "foreign_architecture_build")
			if found != fixture.found || (found && item.Severity != fixture.severity) {
				t.Fatalf("finding = %+v (%v)", item, found)
			}
		})
	}

	compose := &ComposeAnalysis{PrimaryService: "web", Services: []ComposeServicePlan{
		{Name: "db", Image: "mcr.microsoft.com/mssql/server:2022-latest", ImagePlatforms: []string{"linux/amd64"}},
		{Name: "web", BuildContext: "vendor/laravel/sail/runtimes/8.4", BuildContextMissing: true, EnvFiles: []ComposeEnvFile{{Path: ".env", Required: true, Missing: true}}},
	}}
	composeDraft := &Draft{Data: DraftData{
		Intent:    &DraftIntentConfig{Name: "stack", Profile: ProfileCompose},
		Source:    &DraftSourceConfig{Kind: SourceCompose, Mode: SourceModeComposeGit},
		Detection: &DetectionResult{Source: SourceIdentity{Kind: SourceCompose}, Compose: compose},
	}}
	findings := imageBuildFindings(composeDraft, PlanConfiguration{Build: BuildPlanConfig{Method: BuildCompose}},
		HostObservation{OS: "linux", Architecture: "arm64"})
	for _, code := range []string{"compose_image_platform_missing", "compose_build_context_missing", "compose_env_file_missing"} {
		if item, ok := findingWithCode(findings, code); !ok || item.Severity != PreflightBlocked {
			t.Fatalf("%s = %+v", code, findings)
		}
	}
	if item, ok := findingWithCode(findings, "compose_primary_service"); !ok || item.Measured != "web" {
		t.Fatalf("primary service = %+v", findings)
	}
	// Pinned to the platform the image has, the service runs under the
	// installed emulator: the platform finding decides, and agrees.
	compose.Services[0].Platform = "linux/amd64"
	emulated := HostObservation{OS: "linux", Architecture: "arm64", EmulationObserved: true, Emulators: []string{"amd64"}}
	findings = imageBuildFindings(composeDraft, PlanConfiguration{Build: BuildPlanConfig{Method: BuildCompose}}, emulated)
	if item, ok := findingWithCode(findings, "compose_image_platform_missing"); ok {
		t.Fatalf("a pinned, emulated service is still refused: %+v", item)
	}
	if item, ok := findingWithCode(findings, "foreign_architecture_build"); !ok || item.Severity != PreflightWarning {
		t.Fatalf("emulated service platform = %+v", findings)
	}
	compose.Services[0].Platform = ""
	findings = imageBuildFindings(composeDraft, PlanConfiguration{Build: BuildPlanConfig{Method: BuildCompose}}, emulated)
	if item, ok := findingWithCode(findings, "compose_image_platform_missing"); !ok || item.Severity != PreflightBlocked ||
		!strings.Contains(item.Action, "platform: linux/amd64") {
		t.Fatalf("an unpinned image with an emulator available = %+v", item)
	}

	gitCompose := newDetectedCandidate("", BuildCompose, DetectedCandidate{Name: "Compose stack in .", Profile: ProfileCompose, Confidence: ConfidenceHigh})
	draft, configuration = imageFindingDraft([]DetectedCandidate{gitCompose}, BuildPlanConfig{Method: BuildCompose})
	if item, ok := findingWithCode(imageBuildFindings(draft, configuration, HostObservation{}), "compose_analysis_missing"); !ok || item.Severity != PreflightBlocked {
		t.Fatalf("a Git Compose candidate that can never build = %+v", item)
	}
}

func TestPreflightChecksWhereReleaseTasksRun(t *testing.T) {
	candidate := newDetectedCandidate("", BuildDockerfile, DetectedCandidate{
		Name: "Dockerfile in .", Dockerfile: "Dockerfile", Confidence: ConfidenceHigh, ReleaseCommand: "python manage.py migrate",
	})
	draft, configuration := imageFindingDraft([]DetectedCandidate{candidate}, BuildPlanConfig{Method: BuildDockerfile, ReleaseTasks: []ReleaseTaskConfig{
		{Name: "migrate", Command: "cd api && npx prisma migrate deploy", TimeoutSeconds: 60},
		{Name: "push", Command: "node_modules/.bin/prisma db push", TimeoutSeconds: 60},
		{Name: "notify", Command: "curl -fsS https://hooks.example.com/deployed", TimeoutSeconds: 60},
		{Name: "check", Command: "psql \"$DATABASE_URL\" -c 'select 1'", TimeoutSeconds: 60},
	}})
	request := preflightObservationRequest(draft, configuration)
	if strings.Join(request.ReleaseTaskTools, ",") != "curl,npx,psql" {
		t.Fatalf("tools looked up = %v", request.ReleaseTaskTools)
	}
	// The dashboard's own image has no Node, and never an installed prisma.
	observation := HostObservation{Facilities: map[string]FacilityObservation{
		releaseTaskToolFacility("curl"): {Available: true}, releaseTaskToolFacility("npx"): {Available: false},
		releaseTaskToolFacility("psql"): {Available: true},
	}}
	findings := imageBuildFindings(draft, configuration, observation)
	tools := []string{}
	for _, item := range findings {
		if item.Code == "release_task_tool_missing" {
			tools = append(tools, item.Measured)
		}
	}
	if strings.Join(tools, "|") != "migrate: npx|push: node_modules/.bin/prisma" {
		t.Fatalf("tool findings = %v", tools)
	}
	if item, _ := findingWithCode(findings, "release_task_tool_missing"); item.FieldID != "configuration.build.releaseTasks[0].command" {
		t.Fatalf("tool finding field = %q", item.FieldID)
	}
	if _, ok := findingWithCode(findings, "release_command_unmapped"); !ok {
		t.Fatalf("an unplanned release command = %+v", findings)
	}

	configuration.Build.ReleaseTasks = []ReleaseTaskConfig{{Name: "migrate", Command: "python manage.py migrate", Runner: ReleaseTaskRunnerImage, TimeoutSeconds: 60}}
	findings = imageBuildFindings(draft, configuration, HostObservation{})
	if _, ok := findingWithCode(findings, "release_task_tool_missing"); ok {
		t.Fatalf("an image task was checked against the host: %+v", findings)
	}
	if _, ok := findingWithCode(findings, "release_command_unmapped"); ok {
		t.Fatalf("a planned image task still reads as unmapped: %+v", findings)
	}
}

func TestPreflightWarnsWhenPHPServesTheRepositoryRoot(t *testing.T) {
	php := newDetectedCandidate("", BuildRecipe, DetectedCandidate{Name: "PHP application in .", Recipe: "php", Confidence: ConfidenceMedium})
	for _, fixture := range []struct {
		start string
		warn  bool
	}{
		{"frankenphp php-server --listen :80 --root /app", true},
		{"frankenphp php-server --listen :80 --root /app/public", false},
		{"php artisan migrate --force && frankenphp php-server --listen :80 --root /app/public", false},
	} {
		draft, configuration := imageFindingDraft([]DetectedCandidate{php}, BuildPlanConfig{Method: BuildRecipe, Recipe: "php", StartCommand: fixture.start})
		if _, found := findingWithCode(imageBuildFindings(draft, configuration, HostObservation{}), "php_docroot_is_repository_root"); found != fixture.warn {
			t.Fatalf("%q: warning = %v", fixture.start, found)
		}
	}
}

func TestPreflightSaysWhyACandidateWasSelected(t *testing.T) {
	candidate := newDetectedCandidate("", BuildDockerfile, DetectedCandidate{Name: "Dockerfile in .", Dockerfile: "Dockerfile", Confidence: ConfidenceHigh})
	draft, configuration := imageFindingDraft([]DetectedCandidate{candidate}, BuildPlanConfig{Method: BuildDockerfile})
	draft.Data.Detection.SelectionReason = "Dockerfile over the nextjs recipe in .: the repository's own Dockerfile builds it as written"
	findings := preflightFindings(draft, configuration, HostObservation{Facilities: map[string]FacilityObservation{}}, false)
	if item, ok := findingWithCode(findings, "detection_selected"); !ok || item.Measured != draft.Data.Detection.SelectionReason {
		t.Fatalf("detection_selected = %+v", item)
	}
}

func TestPreflightNamesImageTasksOutsideAComposeStack(t *testing.T) {
	task := ReleaseTaskConfig{Name: "migrate", Command: "npx prisma migrate deploy", Runner: ReleaseTaskRunnerImage, TimeoutSeconds: 60}
	for _, fixture := range []struct {
		name     string
		services []ComposeServicePlan
		severity PreflightSeverity
	}{
		{"the stack runs its own database", []ComposeServicePlan{{Name: "db", Image: "postgres:17"}, {Name: "web", BuildContext: "."}}, PreflightBlocked},
		{"the stack is only the application", []ComposeServicePlan{{Name: "web", BuildContext: "."}}, PreflightWarning},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			draft := &Draft{Data: DraftData{
				Intent:    &DraftIntentConfig{Name: "stack", Profile: ProfileCompose},
				Source:    &DraftSourceConfig{Kind: SourceCompose, Mode: SourceModeComposeGit},
				Detection: &DetectionResult{Source: SourceIdentity{Kind: SourceCompose}, Compose: &ComposeAnalysis{PrimaryService: "web", Services: fixture.services}},
			}}
			configuration := PlanConfiguration{Build: BuildPlanConfig{Method: BuildCompose, ReleaseTasks: []ReleaseTaskConfig{task}}}
			item, ok := findingWithCode(imageBuildFindings(draft, configuration, HostObservation{}), "release_task_outside_compose_stack")
			if !ok || item.Severity != fixture.severity || item.FieldID != "configuration.build.releaseTasks[0].runner" {
				t.Fatalf("finding = %+v (%v)", item, ok)
			}
			if fixture.severity == PreflightBlocked && !strings.Contains(item.Measured, "db (postgres)") {
				t.Fatalf("the unreachable service is not named: %q", item.Measured)
			}
		})
	}
	draft, configuration := imageFindingDraft(nil, BuildPlanConfig{Method: BuildDockerfile, ReleaseTasks: []ReleaseTaskConfig{task}})
	if item, ok := findingWithCode(imageBuildFindings(draft, configuration, HostObservation{}), "release_task_outside_compose_stack"); ok {
		t.Fatalf("a Dockerfile release's task = %+v", item)
	}
}

func TestPreflightRefusesBuildsInAPastedComposeFile(t *testing.T) {
	analysis := &ComposeAnalysis{PrimaryService: "web", Services: []ComposeServicePlan{
		{Name: "db", Image: "postgres:17"}, {Name: "web", BuildContext: "."},
	}}
	for mode, blocked := range map[SourceMode]bool{SourceModeComposePaste: true, SourceModeComposeUpload: true, SourceModeComposeGit: false} {
		draft := &Draft{Data: DraftData{
			Intent:    &DraftIntentConfig{Name: "stack", Profile: ProfileCompose},
			Source:    &DraftSourceConfig{Kind: SourceCompose, Mode: mode},
			Detection: &DetectionResult{Source: SourceIdentity{Kind: SourceCompose}, Compose: analysis},
		}}
		item, ok := findingWithCode(imageBuildFindings(draft, PlanConfiguration{Build: BuildPlanConfig{Method: BuildCompose}}, HostObservation{}), "compose_build_without_checkout")
		if ok != blocked || (ok && (item.Severity != PreflightBlocked || item.Measured != "web")) {
			t.Fatalf("%s: finding = %+v (%v)", mode, item, ok)
		}
	}
}
