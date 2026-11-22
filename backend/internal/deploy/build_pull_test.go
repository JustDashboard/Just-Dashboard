package deploy

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestOnlyFullyPinnedGeneratedDockerfilesBuildWithoutPull(t *testing.T) {
	t.Parallel()
	pin := "@sha256:" + strings.Repeat("a", 64)
	pinned := recipeDockerfileSyntax + "\nFROM node:22-alpine" + pin + " AS build\n" +
		"COPY --from=oven/bun:1-alpine" + pin + " /usr/local/bin/bun /usr/local/bin/bun\n" +
		"RUN --mount=type=cache,id=jd-x,target=/root/.npm --mount=type=bind,from=build,source=/a,target=/b true\n" +
		"FROM nginx:1.29-alpine" + pin + "\nCOPY --from=build /app/dist/ /usr/share/nginx/html/\nCOPY --from=0 /x /y\n"
	for name, test := range map[string]struct {
		method     BuildMethod
		dockerfile string
		pulls      bool
	}{
		"recipe, every image pinned":   {BuildRecipe, pinned, false},
		"static, every image pinned":   {BuildStatic, recipeDockerfileSyntax + "\nFROM nginx:1.29-alpine" + pin + "\n", false},
		"scratch":                      {BuildRecipe, "FROM scratch\nCOPY a /a\n", false},
		"a base by tag":                {BuildRecipe, "FROM node:22-alpine\n", true},
		"an image copied from by tag":  {BuildRecipe, "FROM node:22-alpine" + pin + "\nCOPY --from=oven/bun:1 /b /b\n", true},
		"a mount from an image by tag": {BuildRecipe, "FROM node:22-alpine" + pin + "\nRUN --mount=type=bind,from=alpine:3.22,target=/x true\n", true},
		"a frontend by tag":            {BuildRecipe, "# syntax=docker/dockerfile:1\nFROM node:22-alpine" + pin + "\n", true},
		"a base from an argument":      {BuildRecipe, "ARG BASE=node:22-alpine" + pin + "\nFROM ${BASE}\n", true},
		"a stage index not yet built":  {BuildRecipe, "FROM node:22-alpine" + pin + "\nCOPY --from=1 /x /y\n", true},
		"nothing to read":              {BuildRecipe, "", true},
		"a user Dockerfile, pinned":    {BuildDockerfile, pinned, true},
		"Railpack":                     {BuildRailpack, pinned, true},
	} {
		if got := buildPulls(PreparedBuild{Method: test.method, DockerfilePreview: test.dockerfile}); got != test.pulls {
			t.Errorf("%s: pulls = %v, want %v", name, got, test.pulls)
		}
	}
}

// A recipe builds and previews without --pull; a user Dockerfile and a
// Compose service, whose tags can move, keep it.
func TestRecipeBuildsSkipPullAndUserDockerfilesKeepIt(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeBuildFixture(t, root, "package.json", `{"scripts":{"start":"node server.js"}}`)
	writeBuildFixture(t, root, "package-lock.json", "{}")
	writeBuildFixture(t, root, "Dockerfile", "FROM node:22-alpine\nCOPY . .\n")
	writeBuildFixture(t, root, "api/Dockerfile", "FROM alpine:3.22\n")
	backend := &artifactBackendFake{}
	builder := NewArtifactBuilder(backend)
	for _, test := range []struct {
		config BuildPlanConfig
		pulls  bool
	}{
		{BuildPlanConfig{Method: BuildRecipe, Recipe: "node", StartCommand: "npm start"}, false},
		{BuildPlanConfig{Method: BuildDockerfile}, true},
	} {
		prepared, err := builder.Prepare(context.Background(), root, test.config, false, "just-dashboard/deployment-1:run-1")
		if err != nil {
			t.Fatal(err)
		}
		if got := slices.Contains(prepared.BuildArgv, "--pull"); got != test.pulls {
			t.Fatalf("%s preview argv = %v, want --pull %v", test.config.Method, prepared.BuildArgv, test.pulls)
		}
		if _, err := builder.Build(context.Background(), root, "just-dashboard/deployment-1:run-1", test.config, prepared,
			nil, nil, "", SourceIdentity{}, nil, nil); err != nil {
			t.Fatal(err)
		}
		if last := backend.builds[len(backend.builds)-1]; last.Pull != test.pulls {
			t.Fatalf("%s build pull = %v, want %v", test.config.Method, last.Pull, test.pulls)
		}
	}
	compose := &ComposeAnalysis{
		Digest: fakeContentDigest("compose"), Files: []string{"compose.yml"},
		Services: []ComposeServicePlan{{Name: "api", BuildContext: "api"}},
	}
	result, err := builder.Build(context.Background(), root, "just-dashboard/deployment-1:run-2", BuildPlanConfig{Method: BuildCompose},
		PreparedBuild{Method: BuildCompose}, nil, nil, "", SourceIdentity{Kind: SourceCompose}, compose, nil)
	if err != nil {
		t.Fatal(err)
	}
	if last := backend.builds[len(backend.builds)-1]; !last.Pull || !slices.Contains(result.Prepared.ComposeServices[0].BuildArgv, "--pull") {
		t.Fatalf("Compose service build = %#v / %v, want --pull", last, result.Prepared.ComposeServices[0].BuildArgv)
	}
}
