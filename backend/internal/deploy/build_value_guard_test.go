package deploy

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestBuildValueGuardsKeyEachRunOnTheValuesItMounts(t *testing.T) {
	t.Parallel()
	lines := []string{
		"FROM node AS build",
		"WORKDIR /app",
		"RUN --mount=type=secret,id=NPM_TOKEN,env=NPM_TOKEN,required=true npm ci",
		"RUN echo --mount=type=secret,id=NOT_A_FLAG",
		"RUN --mount=type=cache,id=jd-x-npm,target=/root/.npm,sharing=locked --mount=type=secret,id=SITE_URL,env=SITE_URL,required=true " +
			"--mount=type=secret,id=API_KEY,env=API_KEY,required=true npm run build \\",
		"  RUN --mount=type=secret,id=CONTINUED,env=CONTINUED npm test",
		"RUN --mount=type=secret,id=SITE_URL,env=SITE_URL,required=true --mount=type=secret,id=API_KEY,env=API_KEY,required=true node check.js",
		"FROM node",
		"RUN --mount=type=secret,id=NPM_TOKEN,env=NPM_TOKEN,required=true npm prune",
	}
	got, guards := withBuildValueGuards(lines)
	want := []string{
		"FROM node AS build",
		"WORKDIR /app",
		"ARG JD_BUILD_VALUES_1",
		lines[2],
		lines[3],
		"ARG JD_BUILD_VALUES_2",
		lines[4],
		lines[5],
		lines[6],
		"FROM node",
		"ARG JD_BUILD_VALUES_1",
		lines[8],
	}
	if !slices.Equal(got, want) {
		t.Fatalf("guarded lines:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	wantGuards := []BuildValueGuard{
		{Arg: "JD_BUILD_VALUES_1", IDs: []string{"NPM_TOKEN"}},
		{Arg: "JD_BUILD_VALUES_2", IDs: []string{"API_KEY", "SITE_URL"}},
	}
	if !slices.EqualFunc(guards, wantGuards, func(a, b BuildValueGuard) bool { return a.Arg == b.Arg && slices.Equal(a.IDs, b.IDs) }) {
		t.Fatalf("guards = %+v", guards)
	}
}

func TestBuildValueMarkerIsAKeyedDigestOfTheGuardsValuesOnly(t *testing.T) {
	t.Parallel()
	key := []byte("0123456789abcdef0123456789abcdef")
	build := BuildValueGuard{Arg: "JD_BUILD_VALUES_2", IDs: []string{"SITE_URL"}}
	install := BuildValueGuard{Arg: "JD_BUILD_VALUES_1", IDs: []string{"NPM_TOKEN"}}
	values := func(site, token string) []BuildSecretValue {
		return []BuildSecretValue{{ID: "NPM_TOKEN", Step: "install", Value: token}, {ID: "SITE_URL", Step: "build", Value: site}}
	}
	first := buildValueMarker(key, build, values("https://one.example.test", "tok"))
	if again := buildValueMarker(key, build, values("https://one.example.test", "tok")); again != first {
		t.Fatalf("equal values gave %s and %s", first, again)
	}
	if changed := buildValueMarker(key, build, values("https://two.example.test", "tok")); changed == first {
		t.Fatal("a changed build value kept its marker")
	}
	// The install's guard does not name SITE_URL, so a build-step change
	// leaves the install's marker, and with it the install layer, alone.
	if buildValueMarker(key, install, values("https://one.example.test", "tok")) !=
		buildValueMarker(key, install, values("https://two.example.test", "tok")) {
		t.Fatal("a build-step value changed the install's marker")
	}
	if buildValueMarker(key, install, values("", "tok")) == buildValueMarker(key, install, values("", "tok2")) {
		t.Fatal("an install value change kept the install's marker")
	}
	if buildValueMarker([]byte("another key of thirty-two bytes!"), build, values("https://one.example.test", "tok")) == first {
		t.Fatal("the marker does not depend on the key")
	}
	// An absent value and an empty one are different inputs.
	empty := buildValueMarker(key, build, []BuildSecretValue{{ID: "SITE_URL", Value: ""}})
	if absent := buildValueMarker(key, build, nil); absent == empty {
		t.Fatal("an absent value and an empty one share a marker")
	}
	for _, value := range []string{"https://one.example.test", "tok"} {
		if strings.Contains(first, value) {
			t.Fatalf("marker %s contains a value", first)
		}
	}
	if len(first) != 64 || strings.Trim(first, "0123456789abcdef") != "" {
		t.Fatalf("marker %q is not 64 hex characters", first)
	}
}

// Every recipe kind gets its guards from the shared rendering path, and the
// build hands them to buildx as named arguments whose values are keyed
// digests: no value in the Dockerfile, the argv or the stored preparation.
func TestRecipeBuildValuesBustTheStepsThatMountThem(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeBuildFixture(t, root, "package.json", "{\"scripts\":{\"build\":\"vite build\",\"start\":\"node server.js\"}}")
	writeBuildFixture(t, root, "package-lock.json", "{}")
	config := BuildPlanConfig{
		Method: BuildRecipe, Recipe: "node", BuildCommand: "npm run build", StartCommand: "npm start",
		Secrets: []BuildSecretConfig{{Variable: "NPM_TOKEN", Step: "install"}},
	}
	backend := &artifactBackendFake{}
	builder := NewArtifactBuilder(backend).WithBuildValueKey([]byte("fixture key, thirty-two bytes!!!"))
	tag := "just-dashboard/deployment-7:run-1"
	prepared, err := builder.Prepare(context.Background(), root, config, false, tag, "VITE_SITE_URL")
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.BuildValueGuards) != 2 {
		t.Fatalf("guards = %+v\n%s", prepared.BuildValueGuards, prepared.DockerfilePreview)
	}
	lines := strings.Split(prepared.DockerfilePreview, "\n")
	for _, guard := range prepared.BuildValueGuards {
		index := slices.Index(lines, "ARG "+guard.Arg)
		if index < 0 || !strings.HasPrefix(lines[index+1], "RUN ") || !slices.Equal(runSecretIDs(lines[index+1]), guard.IDs) {
			t.Fatalf("guard %+v is not declared just before its RUN:\n%s", guard, prepared.DockerfilePreview)
		}
		if !slices.Contains(prepared.BuildArgv, guard.Arg) {
			t.Fatalf("preview argv %v has no --build-arg %s", prepared.BuildArgv, guard.Arg)
		}
	}
	build := func(site string) map[string]string {
		backend.builds = nil
		if _, err := builder.Build(context.Background(), root, tag, config, prepared,
			map[string]string{"NPM_TOKEN": "fixture-token", "VITE_SITE_URL": site}, map[string]bool{"NPM_TOKEN": true}, "",
			SourceIdentity{}, nil, nil); err != nil {
			t.Fatal(err)
		}
		args := map[string]string{}
		for _, arg := range backend.builds[0].BuildArgs {
			args[arg.Name] = arg.Value
		}
		return args
	}
	first, same, changed := build("https://one.example.test"), build("https://one.example.test"), build("https://two.example.test")
	install, compile := prepared.BuildValueGuards[0].Arg, prepared.BuildValueGuards[1].Arg
	if first[install] == "" || first[compile] == "" || first[install] != same[install] || first[compile] != same[compile] {
		t.Fatalf("unchanged values changed the markers: %v then %v", first, same)
	}
	if changed[install] != first[install] || changed[compile] == first[compile] {
		t.Fatalf("a build-step change gave install %q→%q, build %q→%q", first[install], changed[install], first[compile], changed[compile])
	}
	serialized, _ := json.Marshal(prepared)
	for _, value := range []string{"fixture-token", "one.example.test", first[install], first[compile]} {
		if strings.Contains(string(serialized), value) {
			t.Fatalf("the prepared build carries %q:\n%s", value, serialized)
		}
	}
}

// A language whose renderer knows nothing of the guard gets one anyway.
func TestBuildValueGuardsReachEveryRecipeKind(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeBuildFixture(t, root, "go.mod", "module example.test/app\n\ngo 1.24\n")
	writeBuildFixture(t, root, "main.go", "package main\n\nfunc main() {}\n")
	builder := NewArtifactBuilder(&artifactBackendFake{})
	prepared, err := builder.Prepare(context.Background(), root, BuildPlanConfig{
		Method: BuildRecipe, Recipe: "go", StartCommand: "/app/server",
	}, false, "just-dashboard/deployment-8:run-1", "APP_VERSION")
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.BuildValueGuards) == 0 || !strings.Contains(prepared.DockerfilePreview, "ARG "+prepared.BuildValueGuards[0].Arg+"\nRUN ") {
		t.Fatalf("the Go recipe has no guard:\n%s", prepared.DockerfilePreview)
	}
}
