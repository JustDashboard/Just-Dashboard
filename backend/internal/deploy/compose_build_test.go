package deploy

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
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
