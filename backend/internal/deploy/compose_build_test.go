package deploy

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

const selfHostedCompose = `services:
  db:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: ${POSTGRES_USER:-postgres}
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:?set a database password}
  web:
    build:
      context: .
      target: production
      args:
        NEXT_PUBLIC_API_URL: ${API_URL}
        NODE_VERSION: "22"
        BUILD_REVISION:
    platform: linux/amd64
    env_file:
      - .env
      - path: ./optional.env
        required: false
    ports:
      - "${PORT:-3000}:3000"
`

func TestComposeAnalysisReadsBuildArgsDefaultsEnvFilesAndPrimary(t *testing.T) {
	t.Parallel()
	analysis, err := analyzeComposeDocuments([]ComposeDocument{{Path: "compose.yml", Content: selfHostedCompose}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(analysis.Variables, ",") != "API_URL,POSTGRES_PASSWORD" {
		t.Fatalf("required variables = %v", analysis.Variables)
	}
	optional := map[string]string{}
	for _, variable := range analysis.OptionalVariables {
		optional[variable.Name] = variable.Default
	}
	if len(optional) != 2 || optional["PORT"] != "3000" || optional["POSTGRES_USER"] != "postgres" {
		t.Fatalf("optional variables = %+v", analysis.OptionalVariables)
	}
	if analysis.PrimaryService != "web" {
		t.Fatalf("primary service = %q", analysis.PrimaryService)
	}
	var web ComposeServicePlan
	for _, service := range analysis.Services {
		if service.Name == "web" {
			web = service
		}
	}
	if web.BuildTarget != "production" || web.Platform != "linux/amd64" || len(web.BuildArgs) != 3 ||
		web.BuildArgs[0] != (ComposeBuildArg{Name: "BUILD_REVISION", FromEnvironment: true}) ||
		web.BuildArgs[1] != (ComposeBuildArg{Name: "NEXT_PUBLIC_API_URL", Value: "${API_URL}"}) {
		t.Fatalf("web build = %+v", web)
	}
	if len(web.EnvFiles) != 2 || !web.EnvFiles[0].Required || web.EnvFiles[1].Required {
		t.Fatalf("env files = %+v", web.EnvFiles)
	}
	if !validComposeBuildEvidence(analysis) {
		t.Fatal("analysis evidence does not validate")
	}

	if _, err := analyzeComposeDocuments([]ComposeDocument{{Path: "compose.yml",
		Content: "services:\n  web:\n    build:\n      context: .\n      args:\n        - NPM_TOKEN=npm_literal\n"}}); err == nil {
		t.Fatal("a literal secret build argument was accepted")
	}
}

func TestComposeTreeAnalysisFindsMissingContextsEnvFilesAndDockerfileIssues(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	compose := "services:\n  laravel.test:\n    build:\n      context: ./vendor/laravel/sail/runtimes/8.4\n  api:\n    build:\n      context: api\n    env_file: .env\n"
	writeBuildFixture(t, root, "compose.yml", compose)
	writeBuildFixture(t, root, "api/Dockerfile", "FROM node:22\nENV API_TOKEN=ghp_abcdef0123456789\n")
	analysis, err := analyzeComposeDocuments([]ComposeDocument{{Path: "compose.yml", Content: compose}})
	if err != nil {
		t.Fatal(err)
	}
	analyzeComposeTree(root, &analysis)
	for _, service := range analysis.Services {
		switch service.Name {
		case "laravel.test":
			if !service.BuildContextMissing {
				t.Fatalf("Sail's vendor/ context = %+v", service)
			}
		case "api":
			if len(service.EnvFiles) != 1 || !service.EnvFiles[0].Missing {
				t.Fatalf("missing env_file = %+v", service.EnvFiles)
			}
			if len(service.DockerfileIssues) != 1 || service.DockerfileIssues[0].Code != "dockerfile_refused" {
				t.Fatalf("service Dockerfile issues = %+v", service.DockerfileIssues)
			}
		}
	}
	patched := composeValidationDocuments([]ComposeDocument{{Path: "compose.yml", Content: compose}}, analysis)
	if !strings.Contains(patched[0].Content, "required: false") || !strings.Contains(patched[0].Content, "path: .env") {
		t.Fatalf("validation copy = %s", patched[0].Content)
	}
}

func TestComposeBuildPassesTargetAndInterpolatedArgs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeBuildFixture(t, root, "Dockerfile", "FROM scratch AS production\n")
	analysis, err := analyzeComposeDocuments([]ComposeDocument{{Path: "compose.yml", Content: selfHostedCompose}})
	if err != nil {
		t.Fatal(err)
	}
	backend := &artifactBackendFake{}
	result, err := NewArtifactBuilder(backend).Build(
		context.Background(), root, "just-dashboard/release:1-2", BuildPlanConfig{Method: BuildCompose},
		PreparedBuild{Method: BuildCompose, CachePolicy: "reuse"},
		map[string]string{"API_URL": "https://api.example.com", "BUILD_REVISION": "abc123"}, "",
		SourceIdentity{Kind: SourceCompose}, &analysis, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(backend.builds) != 1 || backend.builds[0].Target != "production" || backend.builds[0].ContextDir != filepath.Clean(root) {
		t.Fatalf("Compose build = %+v", backend.builds)
	}
	args := backend.builds[0].BuildArgs
	if len(args) != 3 || args[0] != (BuildArgValue{"BUILD_REVISION", "abc123"}) ||
		args[1] != (BuildArgValue{"NEXT_PUBLIC_API_URL", "https://api.example.com"}) || args[2] != (BuildArgValue{"NODE_VERSION", "22"}) {
		t.Fatalf("build arguments = %+v", args)
	}
	argv := strings.Join(result.Prepared.ComposeServices[0].BuildArgv, " ")
	if !strings.Contains(argv, "--target production") || !strings.Contains(argv, "--build-arg NEXT_PUBLIC_API_URL") || strings.Contains(argv, "api.example.com") {
		t.Fatalf("recorded argv = %s", argv)
	}
	if result.Compose.PrimaryService != "web" {
		t.Fatalf("snapshot primary service = %q", result.Compose.PrimaryService)
	}

	for _, fixture := range []struct {
		expression string
		values     map[string]string
		want       string
		fails      bool
	}{
		{"${X:-fallback}", nil, "fallback", false},
		{"${X-fallback}", map[string]string{"X": ""}, "", false},
		{"prefix-${X}-$$literal", map[string]string{"X": "v"}, "prefix-v-$literal", false},
		{"${X:+set}", map[string]string{"X": "1"}, "set", false},
		{"${X:?needed}", nil, "", true},
	} {
		got, err := interpolateComposeValue(fixture.expression, fixture.values)
		if got != fixture.want || (err != nil) != fixture.fails {
			t.Fatalf("%q = %q, %v", fixture.expression, got, err)
		}
	}
}

func TestComposePrimaryServiceNeverChoosesADatabase(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		services []ComposeServicePlan
		want     string
	}{
		{[]ComposeServicePlan{{Name: "db", Image: "postgres:16", Ports: []string{"5432:5432"}}, {Name: "web", Image: "ghcr.io/o/app:1", Ports: []string{"8080:80"}}}, "web"},
		{[]ComposeServicePlan{{Name: "cache", Image: "redis:7"}, {Name: "backend", BuildContext: "."}}, "backend"},
		{[]ComposeServicePlan{{Name: "api", BuildContext: "api"}, {Name: "frontend", BuildContext: "web"}}, "frontend"},
		{[]ComposeServicePlan{{Name: "db", Image: "mariadb:11"}, {Name: "worker", Image: "ghcr.io/o/worker:1"}}, "worker"},
	} {
		if got := composePrimaryService(fixture.services); got != fixture.want {
			t.Fatalf("primary of %+v = %q, want %q", fixture.services, got, fixture.want)
		}
	}
}

func TestDockerfileBuildReceivesOnlyDeclaredPublicArgsAndItsTarget(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeBuildFixture(t, root, "Dockerfile", "FROM node:22 AS production\nARG NEXT_PUBLIC_API_URL\nARG NPM_CONFIG_LOGLEVEL\nRUN echo build\nFROM production AS dev\n")
	backend := &artifactBackendFake{}
	builder := NewArtifactBuilder(backend)
	config := BuildPlanConfig{Method: BuildDockerfile, Dockerfile: "Dockerfile", Target: "production", Secrets: []BuildSecretConfig{}}
	prepared, err := builder.Prepare(context.Background(), root, config, false, "just-dashboard/release:1-2",
		"NEXT_PUBLIC_API_URL", "NPM_CONFIG_LOGLEVEL", "VITE_UNDECLARED")
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Target != "production" || !slices.Equal(prepared.BuildArgNames, []string{"NEXT_PUBLIC_API_URL"}) {
		t.Fatalf("prepared = %+v", prepared)
	}
	argv := strings.Join(prepared.BuildArgv, " ")
	if !strings.Contains(argv, "--target production") || !strings.Contains(argv, "--build-arg NEXT_PUBLIC_API_URL") {
		t.Fatalf("argv = %s", argv)
	}
	if _, err := builder.Build(context.Background(), root, "just-dashboard/release:1-2", config, prepared,
		map[string]string{"NEXT_PUBLIC_API_URL": "https://api.example.com", "NPM_CONFIG_LOGLEVEL": "warn"}, "",
		SourceIdentity{Kind: SourceGit}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if invocation := backend.builds[0]; invocation.Target != "production" ||
		!slices.Equal(invocation.BuildArgs, []BuildArgValue{{"NEXT_PUBLIC_API_URL", "https://api.example.com"}}) || len(invocation.Secrets) != 0 {
		t.Fatalf("build invocation = %+v", invocation)
	}
	config.Target = "staging"
	if _, err := builder.Prepare(context.Background(), root, config, false, "just-dashboard/release:1-3"); err == nil {
		t.Fatal("a target that is not a stage was prepared")
	}
}

type slowRegistryFake struct {
	planningDockerFake
	delay             time.Duration
	active, highWater atomic.Int32
}

func (f *slowRegistryFake) ResolveDistributionImage(ctx context.Context, reference, _ string) (*dockerx.DistributionImage, error) {
	current := f.active.Add(1)
	defer f.active.Add(-1)
	for {
		high := f.highWater.Load()
		if current <= high || f.highWater.CompareAndSwap(high, current) {
			break
		}
	}
	select {
	case <-time.After(f.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &dockerx.DistributionImage{Reference: reference, Platforms: []string{"linux/amd64"}}, nil
}

func TestComposeImagePlatformLookupsRunConcurrently(t *testing.T) {
	registry := &slowRegistryFake{delay: 150 * time.Millisecond}
	analysis := &ComposeAnalysis{}
	for index := range 12 {
		analysis.Services = append(analysis.Services, ComposeServicePlan{Name: fmt.Sprintf("s%d", index), Image: fmt.Sprintf("example/image-%d:1", index)})
	}
	analysis.Services = append(analysis.Services, ComposeServicePlan{Name: "web", BuildContext: "."})
	started := time.Now()
	resolveComposeImagePlatforms(t.Context(), registry, analysis)
	// Twelve lookups one after another take 1.8 s; four at a time, 0.45 s.
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("lookups took %s", elapsed)
	}
	if high := registry.highWater.Load(); high < 2 || high > composeImageLookupWorkers {
		t.Fatalf("%d lookups ran at once, want between 2 and %d", high, composeImageLookupWorkers)
	}
	for _, service := range analysis.Services {
		if service.BuildContext == "" && !slices.Equal(service.ImagePlatforms, []string{"linux/amd64"}) {
			t.Fatalf("service %s platforms = %v", service.Name, service.ImagePlatforms)
		}
	}
}

func TestComposeBuildArgumentsReadOnlyPlainRuntimeAndBuildValues(t *testing.T) {
	t.Parallel()
	analysis, err := analyzeComposeDocuments([]ComposeDocument{{Path: "compose.yml", Content: selfHostedCompose}})
	if err != nil {
		t.Fatal(err)
	}
	// The form plans a Compose file's variables for runtime only; Compose
	// itself reads one environment for the whole file.
	runtime := []ScopedVariableValue{
		{Name: "API_URL", Sensitivity: "plain", Value: "https://api.example.com"},
		{Name: "POSTGRES_PASSWORD", Sensitivity: "secret", Value: "database-secret"},
	}
	build := []ScopedVariableValue{{Name: "BUILD_REVISION", Sensitivity: "plain", Value: "abc123"}}
	values, err := composeBuildArgValues(&analysis, runtime, build)
	if err != nil {
		t.Fatal(err)
	}
	if values["API_URL"] != "https://api.example.com" || values["BUILD_REVISION"] != "abc123" {
		t.Fatalf("values = %v", values)
	}
	if _, leaked := values["POSTGRES_PASSWORD"]; leaked {
		t.Fatal("a secret no build argument reads reached the build")
	}
	root := t.TempDir()
	writeBuildFixture(t, root, "Dockerfile", "FROM scratch AS production\n")
	backend := &artifactBackendFake{}
	if _, err := NewArtifactBuilder(backend).Build(
		context.Background(), root, "just-dashboard/release:1-2", BuildPlanConfig{Method: BuildCompose},
		PreparedBuild{Method: BuildCompose, CachePolicy: "reuse"}, values, "",
		SourceIdentity{Kind: SourceCompose}, &analysis, nil,
	); err != nil {
		t.Fatal(err)
	}
	if args := backend.builds[0].BuildArgs; len(args) != 3 || args[1] != (BuildArgValue{"NEXT_PUBLIC_API_URL", "https://api.example.com"}) {
		t.Fatalf("a runtime-planned variable did not reach its build argument: %+v", args)
	}

	for name, fixture := range map[string]struct {
		runtime []ScopedVariableValue
		build   []ScopedVariableValue
	}{
		"a secret build argument":            {runtime: []ScopedVariableValue{{Name: "API_URL", Sensitivity: "secret", Value: "https://api.example.com"}}},
		"a secret from the build scope":      {build: []ScopedVariableValue{{Name: "BUILD_REVISION", Sensitivity: "secret", Value: "abc123"}}},
		"a secret read from the environment": {runtime: []ScopedVariableValue{{Name: "BUILD_REVISION", Sensitivity: "secret", Value: "abc123"}}},
	} {
		if _, err := composeBuildArgValues(&analysis, fixture.runtime, fixture.build); !errors.Is(err, ErrUnsupportedBuilder) || !strings.Contains(err.Error(), "image's history") {
			t.Fatalf("%s = %v", name, err)
		}
	}
	targeted := ComposeAnalysis{Services: []ComposeServicePlan{{Name: "web", BuildContext: ".", BuildTarget: "${STAGE:-production}"}}}
	if _, err := composeBuildArgValues(&targeted, []ScopedVariableValue{{Name: "STAGE", Sensitivity: "secret", Value: "x"}}); !errors.Is(err, ErrUnsupportedBuilder) {
		t.Fatalf("a secret build target = %v", err)
	}
}

func TestPreflightNamesComposeBuildArgumentsThatCannotBePassed(t *testing.T) {
	t.Parallel()
	analysis, err := analyzeComposeDocuments([]ComposeDocument{{Path: "compose.yml", Content: selfHostedCompose}})
	if err != nil {
		t.Fatal(err)
	}
	findings := composeBuildArgFindings(&analysis, PlanConfiguration{Variables: []PlannedVariable{
		{Name: "API_URL", Sensitivity: "secret", Scopes: []string{"runtime"}},
		{Name: "BUILD_REVISION", Sensitivity: "plain", Scopes: []string{"release_task"}},
	}})
	secret, ok := findingByCode(findings, "compose_build_arg_secret")
	if !ok || secret.Severity != PreflightBlocked || secret.Measured != "service web: build argument NEXT_PUBLIC_API_URL reads API_URL" {
		t.Fatalf("secret build argument = %+v", findings)
	}
	if unscoped, ok := findingByCode(findings, "compose_build_arg_unscoped"); !ok || unscoped.Severity != PreflightWarning ||
		!strings.Contains(unscoped.Measured, "BUILD_REVISION") {
		t.Fatalf("unscoped build argument = %+v", findings)
	}
	if clean := composeBuildArgFindings(&analysis, PlanConfiguration{Variables: []PlannedVariable{
		{Name: "API_URL", Sensitivity: "plain", Scopes: []string{"runtime"}},
	}}); len(clean) != 0 {
		t.Fatalf("a plain runtime variable = %+v", clean)
	}
}

func TestOperatorChoosesTheComposePrimaryService(t *testing.T) {
	t.Parallel()
	analysis, err := analyzeComposeDocuments([]ComposeDocument{{Path: "compose.yml", Content: selfHostedCompose}})
	if err != nil {
		t.Fatal(err)
	}
	if primary, err := chosenComposePrimaryService(BuildPlanConfig{Method: BuildCompose}, analysis); err != nil || primary != "web" {
		t.Fatalf("detected primary = %q, %v", primary, err)
	}
	if primary, err := chosenComposePrimaryService(BuildPlanConfig{Method: BuildCompose, PrimaryService: "db"}, analysis); err != nil || primary != "db" {
		t.Fatalf("chosen primary = %q, %v", primary, err)
	}
	if _, err := chosenComposePrimaryService(BuildPlanConfig{Method: BuildCompose, PrimaryService: "api"}, analysis); !errors.Is(err, ErrUnsupportedBuilder) {
		t.Fatalf("a primary service the stack lacks = %v", err)
	}

	root := t.TempDir()
	writeBuildFixture(t, root, "Dockerfile", "FROM scratch AS production\n")
	result, err := NewArtifactBuilder(&artifactBackendFake{}).Build(
		context.Background(), root, "just-dashboard/release:1-2", BuildPlanConfig{Method: BuildCompose, PrimaryService: "db"},
		PreparedBuild{Method: BuildCompose, CachePolicy: "reuse"}, map[string]string{"API_URL": "x"}, "",
		SourceIdentity{Kind: SourceCompose}, &analysis, nil,
	)
	if err != nil || result.Compose.PrimaryService != "db" {
		t.Fatalf("snapshot primary = %+v, %v", result.Compose, err)
	}

	configuration := PlanConfiguration{
		Build:   BuildPlanConfig{Method: BuildCompose, PrimaryService: "web", Secrets: []BuildSecretConfig{}, ReleaseTasks: []ReleaseTaskConfig{}},
		Runtime: RuntimePlanConfig{Strategy: StrategyStopFirst},
	}
	if err := configuration.Validate(); err != nil {
		t.Fatal(err)
	}
	configuration.Build.PrimaryService = "web; rm"
	if err := configuration.Validate(); err == nil {
		t.Fatal("a malformed primary service was accepted")
	}
	configuration.Build.Method, configuration.Build.PrimaryService = BuildDockerfile, "web"
	if canonicalConfiguration(configuration).Build.PrimaryService != "" {
		t.Fatal("a primary service outlived the switch away from Compose")
	}

	draft := &Draft{Data: DraftData{
		Intent:    &DraftIntentConfig{Name: "stack", Profile: ProfileCompose},
		Source:    &DraftSourceConfig{Kind: SourceCompose, Mode: SourceModeComposeGit},
		Detection: &DetectionResult{Source: SourceIdentity{Kind: SourceCompose}, Compose: &analysis},
	}}
	findings := imageBuildFindings(draft, PlanConfiguration{Build: BuildPlanConfig{Method: BuildCompose, PrimaryService: "api"}}, HostObservation{})
	if item, ok := findingByCode(findings, "compose_primary_service_missing"); !ok || item.Severity != PreflightBlocked {
		t.Fatalf("a chosen service the stack lacks = %+v", findings)
	}
	findings = imageBuildFindings(draft, PlanConfiguration{Build: BuildPlanConfig{Method: BuildCompose, PrimaryService: "db"}}, HostObservation{})
	if item, ok := findingByCode(findings, "compose_primary_service"); !ok || item.Measured != "db" {
		t.Fatalf("a chosen primary service = %+v", findings)
	}
}
