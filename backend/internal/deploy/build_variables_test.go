package deploy

import (
	"strings"
	"testing"
)

func TestRecipeBuildScopeWorksWithoutExplicitMappingAndInstallCredentialsStayNarrow(t *testing.T) {
	root := t.TempDir()
	writeBuildFixture(t, root, "package.json", `{"name":"fixture"}`)
	writeBuildFixture(t, root, "package-lock.json", `{}`)
	backend := &artifactBackendFake{}
	builder := NewArtifactBuilder(backend)
	config := BuildPlanConfig{Method: BuildRecipe, Recipe: "node", BuildCommand: "node build.js", StartCommand: "node server.js", Secrets: []BuildSecretConfig{{Variable: "NPM_TOKEN", Step: "install"}}}
	variables := map[string]string{"NPM_TOKEN": "private-install-token", "NEXT_PUBLIC_API_URL": "https://build-value.test"}
	prepared, err := builder.Prepare(t.Context(), root, config, false, "fixture:variables", buildVariableNames(variables)...)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(prepared.DockerfilePreview, "\n") {
		if strings.Contains(line, "node build.js") && (!strings.Contains(line, "env=NEXT_PUBLIC_API_URL") || strings.Contains(line, "NPM_TOKEN")) {
			t.Fatalf("build stage received incorrect variables: %s", line)
		}
		if strings.Contains(line, "npm ci") && (!strings.Contains(line, "env=NPM_TOKEN") || strings.Contains(line, "NEXT_PUBLIC_API_URL")) {
			t.Fatalf("install stage received incorrect variables: %s", line)
		}
	}
	result, err := builder.Build(t.Context(), root, "fixture:variables", config, prepared, variables, map[string]bool{"NPM_TOKEN": true}, "", SourceIdentity{}, nil, nil)
	if err != nil || len(backend.builds) != 1 || len(backend.builds[0].Secrets) != 2 {
		t.Fatalf("scoped build delivery failed: %v, %+v", err, backend.builds)
	}
	if strings.Contains(string(mustJSON(result)), "private-install-token") || strings.Contains(prepared.DockerfilePreview, variables["NEXT_PUBLIC_API_URL"]) {
		t.Fatal("variable value entered build evidence or generated Dockerfile")
	}
}

// A root package's own postinstall runs inside the install, so a value it
// reads has to reach the install RUN as well as the build RUN; one mapping
// mounts the same secret in both, and the value still never enters the
// Dockerfile.
func TestInstallAndBuildMappingMountsOneValueInBothSteps(t *testing.T) {
	root := t.TempDir()
	writeBuildFixture(t, root, "package.json", `{"name":"fixture","scripts":{"postinstall":"node gen.js","build":"node build.js"}}`)
	writeBuildFixture(t, root, "package-lock.json", `{}`)
	backend := &artifactBackendFake{}
	builder := NewArtifactBuilder(backend)
	config := BuildPlanConfig{Method: BuildRecipe, Recipe: "node", BuildCommand: "node build.js", StartCommand: "node server.js",
		Secrets: []BuildSecretConfig{{Variable: "DATABASE_URL", Step: "install_and_build"}}}
	variables := map[string]string{"DATABASE_URL": "postgres://app:hunter2@db.internal/app", "API_URL": "https://build-value.test"}
	prepared, err := builder.Prepare(t.Context(), root, config, false, "fixture:variables", buildVariableNames(variables)...)
	if err != nil {
		t.Fatal(err)
	}
	mount := "--mount=type=secret,id=DATABASE_URL,env=DATABASE_URL,required=true"
	for _, line := range strings.Split(prepared.DockerfilePreview, "\n") {
		if strings.Contains(line, "npm ci") && (!strings.Contains(line, mount) || strings.Contains(line, "API_URL,")) {
			t.Fatalf("install stage received incorrect variables: %s", line)
		}
		if strings.Contains(line, "node build.js") && (!strings.Contains(line, mount) || !strings.Contains(line, "env=API_URL")) {
			t.Fatalf("build stage received incorrect variables: %s", line)
		}
	}
	if _, err := builder.Build(t.Context(), root, "fixture:variables", config, prepared, variables, map[string]bool{"DATABASE_URL": true}, "", SourceIdentity{}, nil, nil); err != nil ||
		len(backend.builds) != 1 || len(backend.builds[0].Secrets) != 2 {
		t.Fatalf("one secret per variable must reach BuildKit: %v, %+v", err, backend.builds)
	}
	if strings.Contains(prepared.DockerfilePreview, "hunter2") {
		t.Fatal("variable value entered the generated Dockerfile")
	}
	plan := func(secrets ...BuildSecretConfig) PlanConfiguration {
		return PlanConfiguration{
			Build:     BuildPlanConfig{Method: BuildRecipe, Recipe: "node", StartCommand: "node server.js", Secrets: secrets},
			Runtime:   RuntimePlanConfig{Strategy: StrategyStopFirst},
			Variables: []PlannedVariable{{Name: "DATABASE_URL", Sensitivity: "secret", Scopes: []string{"runtime", "build"}}},
		}
	}
	for _, step := range []string{"install", "build", "install_and_build"} {
		if err := plan(BuildSecretConfig{Variable: "DATABASE_URL", Step: step}).Validate(); err != nil {
			t.Fatalf("step %s refused: %v", step, err)
		}
	}
	for _, secrets := range [][]BuildSecretConfig{
		{{Variable: "DATABASE_URL", Step: "install"}, {Variable: "DATABASE_URL", Step: "build"}},
		{{Variable: "DATABASE_URL", Step: "both"}},
	} {
		if plan(secrets...).Validate() == nil {
			t.Fatalf("%+v was accepted; install_and_build is the one way to reach both steps", secrets)
		}
	}
}
