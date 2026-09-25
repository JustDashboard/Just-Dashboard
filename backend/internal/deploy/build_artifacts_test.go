package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type artifactBackendFake struct {
	builds     []BuildInvocation
	pulls      []string
	resolves   []string
	removed    []string
	failBuild  error
	inspected  map[string]ResolvedImage
	inspectErr error
}

func (f *artifactBackendFake) ResolveImage(_ context.Context, reference, _ string) (ResolvedImage, error) {
	f.resolves = append(f.resolves, reference)
	return ResolvedImage{
		Reference: reference, Digest: fakeContentDigest(reference),
		OS: "linux", Architecture: "amd64", Platforms: []string{"linux/amd64"},
	}, nil
}

func (f *artifactBackendFake) BuildImage(
	_ context.Context,
	invocation BuildInvocation,
	emit func(BuildLog) error,
) (ResolvedImage, error) {
	f.builds = append(f.builds, invocation)
	if len(invocation.Secrets) > 0 {
		if err := emit(BuildLog{Stream: "stdout", Text: "tool output " + invocation.Secrets[0].Value}); err != nil {
			return ResolvedImage{}, err
		}
	}
	if f.failBuild != nil {
		return ResolvedImage{}, f.failBuild
	}
	return ResolvedImage{
		Reference: invocation.Tag, Digest: fakeContentDigest(invocation.Tag),
		ConfigDigest: fakeContentDigest("config-" + invocation.Tag),
		OS:           "linux", Architecture: "amd64", Platforms: []string{"linux/amd64"}, SizeBytes: 1024,
	}, nil
}

func (f *artifactBackendFake) PullImage(_ context.Context, reference, _ string, _ func(BuildLog) error) (ResolvedImage, error) {
	f.pulls = append(f.pulls, reference)
	name, digest, ok := strings.Cut(reference, "@")
	if !ok {
		return ResolvedImage{}, errors.New("mutable pull")
	}
	return ResolvedImage{Reference: name, Digest: digest, OS: "linux", Architecture: "amd64"}, nil
}

func (f *artifactBackendFake) InspectImage(_ context.Context, reference string) (ResolvedImage, error) {
	if f.inspectErr != nil {
		return ResolvedImage{}, f.inspectErr
	}
	if f.inspected != nil {
		return f.inspected[reference], nil
	}
	return ResolvedImage{Reference: reference, Digest: fakeContentDigest(reference)}, nil
}

func (f *artifactBackendFake) RemoveImage(_ context.Context, reference string) error {
	f.removed = append(f.removed, reference)
	return nil
}

func TestAutomaticRecipesAndExplicitAdaptersRenderPinnedPlans(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		files  map[string]string
		config BuildPlanConfig
		want   []string
	}{
		{
			name:   "node npm",
			files:  map[string]string{"package.json": "{\"scripts\":{\"start\":\"node server.js\"}}", "package-lock.json": "{}"},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "node", StartCommand: "npm start"},
			want:   []string{"FROM node:22-alpine@sha256:", "npm ci", "CMD [\"/bin/sh\",\"-c\",\"npm start\"]"},
		},
		{
			name:   "node static",
			files:  map[string]string{"package.json": "{\"scripts\":{\"build\":\"vite build\"}}", "pnpm-lock.yaml": "lockfileVersion: 9"},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "node", BuildCommand: "pnpm run build", OutputDirectory: "dist"},
			want:   []string{"pnpm install --frozen-lockfile", "FROM nginx:1.29-alpine@sha256:", "COPY --from=build /app/dist/"},
		},
		{
			name:   "go",
			files:  map[string]string{"go.mod": "module example.test/app\n", "cmd/app/main.go": "package main\nfunc main() {}\n"},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "go"},
			want:   []string{"FROM golang:1.27-alpine@sha256:", "go mod download", "go build -trimpath", "ENTRYPOINT [\"/app\"]"},
		},
		{
			name:   "python",
			files:  map[string]string{"requirements.txt": "uvicorn==0.35.0\nfastapi==0.116.1\n", "app.py": "app = object()\n"},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "python", StartCommand: "uvicorn app:app"},
			want:   []string{"FROM python:3.13-slim-trixie@sha256:", "pip install --no-cache-dir", "uvicorn app:app"},
		},
		{
			name:   "static",
			files:  map[string]string{"public/index.html": "<h1>fixture</h1>"},
			config: BuildPlanConfig{Method: BuildStatic, OutputDirectory: "public"},
			want:   []string{"FROM nginx:1.29-alpine@sha256:", "COPY public/ /usr/share/nginx/html/"},
		},
		{
			name:   "dockerfile",
			files:  map[string]string{"containers/Appfile": "FROM scratch\n"},
			config: BuildPlanConfig{Method: BuildDockerfile, Dockerfile: "containers/Appfile"},
			want:   []string{"FROM scratch"},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			for path, content := range test.files {
				writeBuildFixture(t, root, path, content)
			}
			backend := &artifactBackendFake{}
			prepared, err := NewArtifactBuilder(backend).Prepare(
				context.Background(), root, test.config, true, "just-dashboard/test:run-1",
			)
			if err != nil {
				t.Fatal(err)
			}
			if prepared.CachePolicy != "no_cache" || !contentDigestRE.MatchString(prepared.DockerfileDigest) {
				t.Fatalf("prepared cache/digest = %#v", prepared)
			}
			for _, want := range test.want {
				if !strings.Contains(prepared.DockerfilePreview, want) {
					t.Fatalf("Dockerfile missing %q:\n%s", want, prepared.DockerfilePreview)
				}
			}
			for _, base := range prepared.BaseImages {
				if strings.Contains(prepared.DockerfilePreview, base.Reference) &&
					!strings.Contains(prepared.DockerfilePreview, base.Reference+"@"+base.Digest) {
					t.Fatalf("base %s is not digest pinned:\n%s", base.Reference, prepared.DockerfilePreview)
				}
			}
		})
	}
}

func TestRecipeBuildSecretsUseNamedMountsAndAreRedacted(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeBuildFixture(t, root, "package.json", "{\"scripts\":{\"build\":\"npm run compile\",\"start\":\"node server.js\"}}")
	writeBuildFixture(t, root, "package-lock.json", "{}")
	config := BuildPlanConfig{
		Method: BuildRecipe, Recipe: "node", BuildCommand: "npm run build", StartCommand: "npm start",
		Secrets: []BuildSecretConfig{{Variable: "NPM_TOKEN", Step: "install"}},
	}
	backend := &artifactBackendFake{}
	builder := NewArtifactBuilder(backend)
	prepared, err := builder.Prepare(context.Background(), root, config, false, "just-dashboard/test:secret")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prepared.DockerfilePreview, "--mount=type=secret,id=NPM_TOKEN,env=NPM_TOKEN") {
		t.Fatalf("generated Dockerfile has no scoped secret mount:\n%s", prepared.DockerfilePreview)
	}
	if strings.Contains(strings.Join(prepared.BuildArgv, " "), "fixture-super-secret") {
		t.Fatal("secret entered preview argv")
	}
	var logs []string
	result, err := builder.Build(
		context.Background(), root, "just-dashboard/test:secret", config, prepared,
		map[string]string{"NPM_TOKEN": "fixture-super-secret"}, "", SourceIdentity{}, nil,
		func(line BuildLog) error { logs = append(logs, line.Text); return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Artifacts) != 1 || result.Artifacts[0].Kind != ArtifactImage {
		t.Fatalf("build artifacts = %#v", result.Artifacts)
	}
	if len(backend.builds) != 1 || backend.builds[0].Secrets[0].Value != "fixture-super-secret" {
		t.Fatalf("backend did not receive the ephemeral secret: %#v", backend.builds)
	}
	serialized, _ := json.Marshal(struct {
		Prepared PreparedBuild
		Logs     []string
	}{prepared, logs})
	if strings.Contains(string(serialized), "fixture-super-secret") || !strings.Contains(string(serialized), "[REDACTED]") {
		t.Fatalf("persistable build evidence leaked or failed to redact: %s", serialized)
	}
}

func TestImageAndComposeAdaptersPinResolvedDigests(t *testing.T) {
	t.Parallel()
	backend := &artifactBackendFake{}
	builder := NewArtifactBuilder(backend)
	imageDigest := fakeContentDigest("image-v1")
	imageResult, err := builder.Build(
		context.Background(), t.TempDir(), "unused", BuildPlanConfig{Method: BuildImage},
		PreparedBuild{Method: BuildImage}, nil, "auth-out-of-band",
		SourceIdentity{Kind: SourceImage, Repository: "registry.example/app:current", Digest: imageDigest},
		nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(backend.pulls) != 1 || backend.pulls[0] != "registry.example/app:current@"+imageDigest ||
		imageResult.Artifacts[0].Digest != imageDigest {
		t.Fatalf("image adapter used mutable identity: pulls=%#v result=%#v", backend.pulls, imageResult)
	}
	compose := &ComposeAnalysis{
		Digest: fakeContentDigest("compose"), Files: []string{"compose.yml"},
		Services: []ComposeServicePlan{
			{Name: "api", Image: "example/api:current"},
			{Name: "db", Image: "example/db:16"},
		},
	}
	composeResult, err := builder.Build(
		context.Background(), t.TempDir(), "unused", BuildPlanConfig{Method: BuildCompose},
		PreparedBuild{Method: BuildCompose}, nil, "", SourceIdentity{Kind: SourceCompose},
		compose, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(composeResult.Artifacts) != 3 || composeResult.Artifacts[0].Kind != ArtifactImage ||
		composeResult.Artifacts[1].Kind != ArtifactImage || composeResult.Artifacts[2].Kind != ArtifactCompose ||
		len(backend.pulls) != 3 {
		t.Fatalf("Compose artifact/pulls = %#v / %#v", composeResult.Artifacts, backend.pulls)
	}
	for _, pull := range backend.pulls[1:] {
		if !strings.Contains(pull, "@sha256:") {
			t.Fatalf("Compose pull was mutable: %s", pull)
		}
	}
}

func TestComposeAdapterBuildsContainedServiceContexts(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeBuildFixture(t, root, "ops/compose.yml", "services:\n  api:\n    build:\n      context: ../services/api\n      dockerfile: containers/Appfile\n")
	writeBuildFixture(t, root, "services/api/containers/Appfile", "FROM scratch\n")
	analysis, err := analyzeComposeDocuments([]ComposeDocument{{
		Path: "ops/compose.yml", Content: "services:\n  api:\n    build:\n      context: ../services/api\n      dockerfile: containers/Appfile\n",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := analysis.Services[0]; got.BuildContext != "services/api" || got.BuildDockerfile != "containers/Appfile" {
		t.Fatalf("normalized Compose build = %#v", got)
	}
	backend := &artifactBackendFake{}
	result, err := NewArtifactBuilder(backend).Build(
		context.Background(), root, "just-dashboard/release:1-2",
		BuildPlanConfig{Method: BuildCompose, NoCache: true, TargetPlatform: "linux/amd64"},
		PreparedBuild{Method: BuildCompose, CachePolicy: "no_cache", TargetPlatform: "linux/amd64"},
		nil, "", SourceIdentity{Kind: SourceCompose}, &analysis, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(backend.builds) != 1 || backend.builds[0].ContextDir != filepath.Join(root, "services/api") ||
		backend.builds[0].Dockerfile != "containers/Appfile" || !backend.builds[0].NoCache ||
		backend.builds[0].Platform != "linux/amd64" {
		t.Fatalf("Compose build invocation = %#v", backend.builds)
	}
	if len(result.Artifacts) != 2 || result.Artifacts[0].Kind != ArtifactImage ||
		result.Artifacts[1].Kind != ArtifactCompose || len(result.Prepared.ComposeServices) != 1 ||
		!contentDigestRE.MatchString(result.Prepared.ComposeServices[0].DockerfileDigest) {
		t.Fatalf("Compose build artifacts = %#v", result)
	}
}

func TestComposeAdapterRejectsEscapingBuildSymlink(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := t.TempDir()
	writeBuildFixture(t, outside, "Dockerfile", "FROM scratch\n")
	if err := os.Symlink(outside, filepath.Join(root, "service")); err != nil {
		t.Fatal(err)
	}
	compose := &ComposeAnalysis{
		Digest: fakeContentDigest("compose"), Files: []string{"compose.yml"},
		Services: []ComposeServicePlan{{Name: "api", BuildContext: "service", BuildDockerfile: "Dockerfile"}},
	}
	_, err := NewArtifactBuilder(&artifactBackendFake{}).Build(
		context.Background(), root, "just-dashboard/release:1-2", BuildPlanConfig{Method: BuildCompose},
		PreparedBuild{Method: BuildCompose, CachePolicy: "reuse"}, nil, "", SourceIdentity{Kind: SourceCompose}, compose, nil,
	)
	if !errors.Is(err, ErrUnsupportedBuilder) {
		t.Fatalf("escaping Compose build symlink error = %v", err)
	}
}

func TestUnsafeRecipeInputsFailClosed(t *testing.T) {
	t.Parallel()
	builder := NewArtifactBuilder(&artifactBackendFake{})
	root := t.TempDir()
	writeBuildFixture(t, root, "package.json", "{}")
	writeBuildFixture(t, root, "package-lock.json", "{}")
	writeBuildFixture(t, root, "yarn.lock", "")
	if _, err := builder.Prepare(context.Background(), root,
		BuildPlanConfig{Method: BuildRecipe, Recipe: "node", StartCommand: "npm start"}, false, "test:tag"); !errors.Is(err, ErrUnsupportedBuilder) {
		t.Fatalf("competing lockfiles error = %v", err)
	}
	writeBuildFixture(t, root, "Dockerfile", "FROM scratch\n")
	if _, err := builder.Prepare(context.Background(), root,
		BuildPlanConfig{
			Method: BuildDockerfile, Dockerfile: "Dockerfile",
			Secrets: []BuildSecretConfig{{Variable: "TOKEN", Step: "build"}},
		}, false, "test:tag"); !errors.Is(err, ErrUnsupportedBuilder) {
		t.Fatalf("custom Dockerfile secret error = %v", err)
	}
	writeBuildFixture(t, root, "Dockerfile", "FROM alpine:3.22\nENV API_TOKEN=plain-text-value\n")
	if _, err := builder.Prepare(context.Background(), root,
		BuildPlanConfig{Method: BuildDockerfile, Dockerfile: "Dockerfile"}, false, "test:tag"); !errors.Is(err, ErrUnsupportedBuilder) {
		t.Fatalf("credential-bearing Dockerfile error = %v", err)
	}
}

func TestArtifactCleanupUsesTagOnlyWhileItNamesRecordedImage(t *testing.T) {
	t.Parallel()
	releaseDigest := fakeContentDigest("release-v1")
	configDigest := fakeContentDigest("config-v1")
	for _, test := range []struct {
		name       string
		current    ResolvedImage
		metadata   json.RawMessage
		wantRemove string
	}{
		{
			name: "tag still exact", current: ResolvedImage{Digest: releaseDigest, ConfigDigest: configDigest},
			metadata: mustJSON(map[string]any{"configDigest": configDigest}), wantRemove: "example/app:current",
		},
		{
			name: "tag moved", current: ResolvedImage{Digest: fakeContentDigest("release-v2"), ConfigDigest: fakeContentDigest("config-v2")},
			metadata: mustJSON(map[string]any{"configDigest": configDigest}), wantRemove: configDigest,
		},
		{
			name: "tag moved without config metadata", current: ResolvedImage{Digest: fakeContentDigest("release-v2")},
			metadata: json.RawMessage(`{}`), wantRemove: releaseDigest,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := &artifactBackendFake{inspected: map[string]ResolvedImage{"example/app:current": test.current}}
			err := NewDockerArtifactRemover(backend).RemoveArtifact(context.Background(), ReleaseArtifact{
				Kind: ArtifactImage, Reference: "example/app:current", Digest: releaseDigest, Metadata: test.metadata,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(backend.removed) != 1 || backend.removed[0] != test.wantRemove {
				t.Fatalf("removed identities = %#v, want %q", backend.removed, test.wantRemove)
			}
		})
	}
}

func writeBuildFixture(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fakeContentDigest(value string) string {
	return digestBytes([]byte(value))
}
