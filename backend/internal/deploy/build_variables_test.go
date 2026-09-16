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
	result, err := builder.Build(t.Context(), root, "fixture:variables", config, prepared, variables, "", SourceIdentity{}, nil, nil)
	if err != nil || len(backend.builds) != 1 || len(backend.builds[0].Secrets) != 2 {
		t.Fatalf("scoped build delivery failed: %v, %+v", err, backend.builds)
	}
	if strings.Contains(string(mustJSON(result)), "private-install-token") || strings.Contains(prepared.DockerfilePreview, variables["NEXT_PUBLIC_API_URL"]) {
		t.Fatal("variable value entered build evidence or generated Dockerfile")
	}
}
