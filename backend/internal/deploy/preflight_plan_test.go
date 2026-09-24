package deploy

import (
	"strings"
	"testing"
)

func TestPythonVersionSatisfiesDeclaredRanges(t *testing.T) {
	for _, fixture := range []struct {
		version, constraint string
		want                bool
	}{
		{"3.12", ">=3.12", true},
		{"3.11", ">=3.12", false},
		{"3.11", ">3.11", true},
		{"3.10", ">3.11", false},
		{"3.13", ">=3.10,<3.13", false},
		{"3.12", ">=3.10,<3.13", true},
		{"3.12", "<3.12.4", true},
		{"3.13", "<=3.12", false},
		{"3.12", "==3.12.*", true},
		{"3.13", "==3.12.*", false},
		{"3.11", "~=3.11.0", true},
		{"3.12", "~=3.11.0", false},
		{"3.13", "~=3.11", true},
		{"3.10", "^3.11", false},
		{"3.12", "^3.11", true},
		{"3.12", "!=3.9", true},
		{"3.12", ">=3.8, <4", true},
	} {
		if got := pythonVersionSatisfies(fixture.version, fixture.constraint); got != fixture.want {
			t.Errorf("%s in %q = %v, want %v", fixture.version, fixture.constraint, got, fixture.want)
		}
	}
}

// uv and Poetry refuse an interpreter outside requires-python, so the build
// would fail; pip installs anyway, so it is a warning. The plan's own choice
// is judged, and the detected family stands in while it has none.
func TestPythonVersionOutsideRequiresPythonIsFoundBeforeDeploy(t *testing.T) {
	candidate := &DetectedCandidate{Recipe: "python", PythonVersion: "3.11", PythonRequires: ">=3.12", PythonInstall: "uv.lock"}
	build := BuildPlanConfig{Method: BuildRecipe, Recipe: "python", StartCommand: "uvicorn app:app"}
	item := findingByCode(plannedRecipeFindings(candidate, build), "python_version_unsupported")
	if item == nil || item.Severity != PreflightBlocked || item.Measured != "3.11; requires-python >=3.12" ||
		!strings.Contains(item.Action, "Python 3.13") || item.FieldID != "configuration.build.pythonVersion" {
		t.Fatalf("locked install = %#v", item)
	}
	candidate.PythonInstall = "requirements.txt"
	if item := findingByCode(plannedRecipeFindings(candidate, build), "python_version_unsupported"); item == nil || item.Severity != PreflightWarning {
		t.Fatalf("pip install = %#v", item)
	}
	build.PythonVersion = "3.12"
	if item := findingByCode(plannedRecipeFindings(candidate, build), "python_version_unsupported"); item != nil {
		t.Fatalf("a version inside the range was reported: %#v", item)
	}

	root := t.TempDir()
	writeBuildFixture(t, root, "pyproject.toml", "[project]\nname = \"api\"\nrequires-python = \">=3.12\"\ndependencies = [\"fastapi==0.116.1\"]\n")
	writeBuildFixture(t, root, "uv.lock", "version = 1\n")
	writeBuildFixture(t, root, ".python-version", "3.11\n")
	detection, err := (Detector{}).DetectPath(t.Context(), root, SourceIdentity{})
	if err != nil || len(detection.Candidates) != 1 {
		t.Fatalf("detect: %#v, %v", detection, err)
	}
	detected := detection.Candidates[0]
	if detected.PythonRequires != ">=3.12" || detected.PythonInstall != "uv.lock" || detected.PythonVersion != "3.11" {
		t.Fatalf("python facts = %#v", detected)
	}
}

func TestStartCommandIsAskedForBeforeTheRecipeRefusesIt(t *testing.T) {
	for _, fixture := range []struct {
		name  string
		build BuildPlanConfig
		want  bool
	}{
		{"python", BuildPlanConfig{Method: BuildRecipe, Recipe: "python"}, true},
		{"node service", BuildPlanConfig{Method: BuildRecipe, Recipe: "node"}, true},
		{"node static", BuildPlanConfig{Method: BuildRecipe, Recipe: "node", OutputDirectory: "dist"}, false},
		{"deno", BuildPlanConfig{Method: BuildRecipe, Recipe: "deno"}, true},
		{"php", BuildPlanConfig{Method: BuildRecipe, Recipe: "php"}, true},
		{"rust builds its own entrypoint", BuildPlanConfig{Method: BuildRecipe, Recipe: "rust"}, false},
		{"set", BuildPlanConfig{Method: BuildRecipe, Recipe: "python", StartCommand: "gunicorn app:app"}, false},
		{"dockerfile", BuildPlanConfig{Method: BuildDockerfile}, false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			candidate := &DetectedCandidate{Recipe: fixture.build.Recipe, NeedsDecision: []string{"confirm ASGI/WSGI start command, port, and readiness check"}}
			item := findingByCode(plannedRecipeFindings(candidate, fixture.build), "start_command_missing")
			if (item != nil) != fixture.want {
				t.Fatalf("start_command_missing = %#v, want %v", item, fixture.want)
			}
			if item != nil && (item.Severity != PreflightBlocked || item.FieldID != "configuration.build.startCommand" ||
				(fixture.build.Recipe == "python" && !strings.Contains(item.Measured, "ASGI"))) {
				t.Fatalf("finding = %#v", item)
			}
		})
	}
}

// Pinning a Go version the recipe builds with clears a version problem the
// automatic choice had; detection no longer freezes it into RecipeIssue.
func TestGoVersionIsJudgedAgainstThePlansOwnChoice(t *testing.T) {
	root := t.TempDir()
	writeBuildFixture(t, root, "go.mod", "module example.test/app\n\ngo 1.24\n")
	writeBuildFixture(t, root, ".go-version", "1.24.3\n")
	writeBuildFixture(t, root, "main.go", "package main\n\nfunc main() {}\n")
	detection, err := (Detector{}).DetectPath(t.Context(), root, SourceIdentity{})
	if err != nil || len(detection.Candidates) != 1 {
		t.Fatalf("detect: %#v, %v", detection, err)
	}
	candidate := detection.Candidates[0]
	if candidate.RecipeIssue != "" || candidate.GoVersionFile != "1.24.3" || candidate.GoVersion != "" {
		t.Fatalf("a version choice was frozen into the candidate: %#v", candidate)
	}
	build := BuildPlanConfig{Method: BuildRecipe, Recipe: "go"}
	item := findingByCode(plannedRecipeFindings(&candidate, build), "go_version_unsupported")
	if item == nil || item.Severity != PreflightBlocked || !strings.Contains(item.Measured, ".go-version 1.24.3") {
		t.Fatalf("the unsupported pin was not found: %#v", item)
	}
	build.GoVersion = "1.26"
	if item := findingByCode(plannedRecipeFindings(&candidate, build), "go_version_unsupported"); item != nil {
		t.Fatalf("a supported pin was still refused: %#v", item)
	}
	if _, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(t.Context(), root, build, false, "fixture:go"); err != nil {
		t.Fatalf("the recipe refuses what preflight accepted: %v", err)
	}

	toolchain := &DetectedCandidate{Recipe: "go", GoMinimumVersion: "1.26.0", GoToolchain: "go1.99.1", GoMainPackages: []string{"."}, GoPackage: "."}
	if item := findingByCode(plannedRecipeFindings(toolchain, BuildPlanConfig{Method: BuildRecipe, Recipe: "go"}), "go_version_unsupported"); item == nil {
		t.Fatal("a toolchain line outside the catalogue was accepted")
	}
	if item := findingByCode(plannedRecipeFindings(toolchain, BuildPlanConfig{Method: BuildRecipe, Recipe: "go", GoVersion: "1.26"}), "go_version_unsupported"); item != nil {
		t.Fatalf("an explicit supported version still read the toolchain line: %#v", item)
	}
}

// A root typed after detection is judged by what detection found there. The
// selected candidate described another directory, so its facts neither pass
// nor block the edited plan.
func TestEditedRootIsJudgedByTheCandidateAtThatRoot(t *testing.T) {
	web := newDetectedCandidate("apps/web", BuildRecipe, DetectedCandidate{
		Name: "web", Recipe: "node", Confidence: ConfidenceHigh, PackageManager: "bun", PackageManagers: []string{"bun"},
	})
	api := newDetectedCandidate("apps/api", BuildRecipe, DetectedCandidate{
		Name: "api", Recipe: "node", Confidence: ConfidenceMedium, PackageManagers: []string{"bun", "npm"},
	})
	detection := &DetectionResult{Candidates: []DetectedCandidate{api, web}, SelectedID: web.ID}
	build := BuildPlanConfig{Method: BuildRecipe, Recipe: "node", RootDirectory: "apps/api", StartCommand: "npm start"}
	if planned := plannedDetectionCandidate(detection, build); planned == nil || planned.ID != api.ID {
		t.Fatalf("planned candidate = %#v", planned)
	}
	draft := &Draft{Data: DraftData{Detection: detection, Source: &DraftSourceConfig{Kind: SourceGit}, Intent: &DraftIntentConfig{Profile: ProfileWeb}}}
	findings := preflightFindings(draft, PlanConfiguration{Build: build}, HostObservation{}, true)
	if item := findingByCode(findings, "package_manager_ambiguous"); item == nil {
		t.Fatalf("the edited root's competing lockfiles were judged by another directory: %#v", findings)
	}
	if findingByCode(findings, "detection_root_mismatch") != nil {
		t.Fatal("a root detection found was reported as unknown")
	}
	build.RootDirectory = "apps/admin"
	findings = preflightFindings(draft, PlanConfiguration{Build: build}, HostObservation{}, true)
	if item := findingByCode(findings, "detection_root_mismatch"); item == nil || item.Severity != PreflightDecision ||
		!strings.Contains(item.Action, "source subdirectory apps/admin") {
		t.Fatalf("an unknown root was not named: %#v", findings)
	}
	if findingByCode(findings, "package_manager_ambiguous") != nil {
		t.Fatal("another directory's lockfiles were reported for an unknown root")
	}
}

// The variables detection read that the plan leaves unset are named before
// Deploy by the environment check (preflight_variables.go): the ones read
// without a default together, the documented rest together, and a staged
// value or a declaration counts as set.
func TestDetectedVariablesLeftUnsetAreNamedBeforeDeploy(t *testing.T) {
	candidate := DetectedCandidate{
		ID: "c", Root: "", BuildMethod: BuildRecipe, Recipe: "node",
		Variables: []DetectedVariable{
			{Name: "DATABASE_URL", Sources: []string{"prisma/schema.prisma"}, Required: true},
			{Name: "AUTH_SECRET", Sources: []string{"src/env.ts"}, Required: true},
			{Name: "SENTRY_DSN", Example: "https://key@sentry.example/1", Sources: []string{".env.example"}},
			{Name: "LOG_LEVEL", Example: "info", Sources: []string{".env.example"}},
		},
		Databases: []DetectedDatabase{{Engine: "postgres", Variable: "DATABASE_URL"}},
	}
	detection := DetectionResult{Candidates: []DetectedCandidate{candidate}, SelectedID: "c"}
	draft := nodeDraft(detection)
	draft.environment = map[string]string{"AUTH_SECRET": "staged"}
	configuration := nodeTestConfiguration(BuildPlanConfig{Method: BuildRecipe, Recipe: "node", StartCommand: "npm run start"})
	configuration.Variables = []PlannedVariable{{Name: "LOG_LEVEL", Sensitivity: "plain", Scopes: []string{"runtime"}}}
	findings := environmentFindings(draft, configuration, HostObservation{})
	likely := findingByCode(findings, "variable_likely_required")
	if likely == nil || likely.Severity != PreflightWarning || likely.Measured != "DATABASE_URL" {
		t.Fatalf("required variables = %#v", likely)
	}
	documented := findingByCode(findings, "documented_variables_unset")
	if documented == nil || documented.Measured != "SENTRY_DSN" {
		t.Fatalf("documented remainder = %#v", documented)
	}
}

// A variable name may be as long as a finding code, so a per-name code is
// cut and told apart by a digest: the draft's preflight still saves, and two
// long names sharing a prefix keep two codes.
func TestLongVariableNamesKeepTheirFindingCodesWithinBounds(t *testing.T) {
	prefix := "A" + strings.Repeat("B", 120)
	first, second := prefix+"_FIRST", prefix+"_SECOND"
	candidate := DetectedCandidate{ID: "c", BuildMethod: BuildRecipe, Recipe: "node", Variables: []DetectedVariable{
		{Name: first, Sources: []string{"next.config.ts"}, Phase: "build", Required: true},
		{Name: second, Sources: []string{"next.config.ts"}, Phase: "build", Required: true},
	}}
	draft := nodeDraft(DetectionResult{Candidates: []DetectedCandidate{candidate}, SelectedID: "c"})
	configuration := nodeTestConfiguration(BuildPlanConfig{Method: BuildRecipe, Recipe: "node", StartCommand: "npm run start"})
	findings := []PreflightFinding{}
	for _, item := range environmentFindings(draft, configuration, HostObservation{}) {
		if strings.HasPrefix(item.Code, "build_variable_missing_") {
			findings = append(findings, item)
		}
	}
	if len(findings) != 2 || findings[0].Code == findings[1].Code {
		t.Fatalf("findings = %#v", findings)
	}
	for _, item := range findings {
		if len(item.Code) > 128 || !strings.HasPrefix(item.Code, "build_variable_missing_ab") ||
			!strings.HasPrefix(item.FieldID, "variables."+prefix) {
			t.Fatalf("finding = %#v", item)
		}
	}
	if err := validatePreflightFindings(findings); err != nil {
		t.Fatalf("a long variable name made the preflight unsaveable: %v", err)
	}
	if code := variableFindingCode("compose_variable_", "APP_TAG"); code != "compose_variable_app_tag" {
		t.Fatalf("a short name's code changed: %q", code)
	}
}

func TestEnvironmentDiscoveryMarksOnlyReadsThatFailWithoutAValue(t *testing.T) {
	root := t.TempDir()
	for path, content := range map[string]string{
		"package.json":               `{"name":"app","scripts":{"start":"node server.js"}}`,
		"bun.lock":                   "{}",
		"app/settings.py":            "import os\nSECRET = os.environ['DJANGO_SECRET']\nos.environ['FORCED'] = 'x'\nDEBUG = os.environ.get('DJANGO_DEBUG')\n",
		"config/app.rb":              "ENV.fetch('RAILS_KEY')\nENV.fetch('RAILS_PORT', 3000)\nENV.fetch('RAILS_HOST') { 'localhost' }\n",
		"src/client.ts":              "const key = process.env.STRIPE_SECRET!\nif (process.env.OPTIONAL_FLAG !== 'x') {}\nif (!process.env.SMTP_URL) throw new Error('missing')\n",
		"src/env.ts":                 "import { createEnv } from \"@t3-oss/env-nextjs\"\nexport const env = createEnv({\n  server: {\n    AUTH_SECRET: z.string(),\n    AUTH_TRUST_HOST: z.string().optional(),\n    RESEND_KEY: z\n      .string()\n      .default(\"none\"),\n  },\n  runtimeEnv: { AUTH_SECRET: process.env.AUTH_SECRET },\n})\n",
		"src/routes/+page.server.ts": "import { PRIVATE_TOKEN, PUBLIC_NAME as NAME } from '$env/static/private'\n",
		"prisma/schema.prisma":       "datasource db {\n  provider = \"postgresql\"\n  url = env(\"DATABASE_URL\")\n}\n",
		"prisma.config.ts":           "import { env } from 'prisma/config'\nexport default { datasource: { url: env('DIRECT_URL') } }\n",
		"api/config.py":              "from pydantic_settings import BaseSettings\n\nclass Settings(BaseSettings):\n    redis_url: str\n    workers: int = 2\n    api_token: str | None\n\nsettings = Settings()\n",
	} {
		writeBuildFixture(t, root, path, content)
	}
	detection, err := (Detector{}).DetectPath(t.Context(), root, SourceIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{}
	seen := map[string]bool{}
	for _, candidate := range detection.Candidates {
		for _, variable := range candidate.Variables {
			seen[variable.Name] = true
			// RequiredRead is the same form where it may run on one path
			// only (a guard inside a function); both fail without a value.
			if variable.Required || variable.RequiredRead {
				required[variable.Name] = true
			}
		}
	}
	for _, name := range []string{"DJANGO_SECRET", "RAILS_KEY", "STRIPE_SECRET", "SMTP_URL", "AUTH_SECRET",
		"PRIVATE_TOKEN", "PUBLIC_NAME", "DATABASE_URL", "DIRECT_URL", "REDIS_URL", "API_TOKEN"} {
		if !required[name] {
			t.Errorf("%s was not marked required (seen %v)", name, seen[name])
		}
	}
	for _, name := range []string{"FORCED", "DJANGO_DEBUG", "RAILS_PORT", "RAILS_HOST", "OPTIONAL_FLAG", "AUTH_TRUST_HOST", "RESEND_KEY", "WORKERS"} {
		if required[name] {
			t.Errorf("%s was marked required", name)
		}
	}
}
