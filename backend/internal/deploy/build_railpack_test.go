package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// railpackBackendFake plans as Railpack's planner would, from files a test
// captured from the real one (testdata/railpack), and builds as the fake
// artifact backend does.
type railpackBackendFake struct {
	*artifactBackendFake
	requests []RailpackPlanRequest
	result   RailpackPlanResult
	err      error
}

func (f *railpackBackendFake) PlanRailpack(_ context.Context, request RailpackPlanRequest) (RailpackPlanResult, error) {
	f.requests = append(f.requests, request)
	return f.result, f.err
}

func railpackTestdata(t *testing.T, name string) []byte {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("testdata", "railpack", name))
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func railpackPlanned(t *testing.T) *railpackBackendFake {
	t.Helper()
	return &railpackBackendFake{
		artifactBackendFake: &artifactBackendFake{},
		result: RailpackPlanResult{
			Plan: railpackTestdata(t, "node-plan.json"), Info: railpackTestdata(t, "node-info.json"),
			Output: []string{"\x1b[1m╭─────────────────╮\x1b[0m", "│ Railpack 0.43.0 │", "", "", "  ↳ Detected Node"},
		},
	}
}

func TestRailpackSettingsAreValidatedAgainstTheMethod(t *testing.T) {
	t.Parallel()
	valid := func(build BuildPlanConfig) PlanConfiguration {
		return canonicalConfiguration(PlanConfiguration{
			Build:   build,
			Runtime: RuntimePlanConfig{Strategy: StrategyBlueGreen, InternalPort: 8080},
		})
	}
	for name, build := range map[string]BuildPlanConfig{
		"the default release":           {Method: BuildRailpack},
		"a pinned release":              {Method: BuildRailpack, RailpackVersion: RailpackDefaultVersion},
		"a provider":                    {Method: BuildRailpack, RailpackProvider: "python"},
		"a configuration file":          {Method: BuildRailpack, RailpackConfig: "deploy/railpack.json"},
		"the plan's own commands":       {Method: BuildRailpack, BuildCommand: "npm run build", StartCommand: "node index.js"},
		"a root directory and no cache": {Method: BuildRailpack, RootDirectory: "apps/web", NoCache: true},
	} {
		if err := valid(build).Validate(); err != nil {
			t.Errorf("%s refused: %v", name, err)
		}
	}
	for name, test := range map[string]struct {
		build BuildPlanConfig
		field string
	}{
		"an unreviewed release":                {BuildPlanConfig{Method: BuildRailpack, RailpackVersion: "0.1.0"}, "build.railpackVersion"},
		"a provider Railpack does not have":    {BuildPlanConfig{Method: BuildRailpack, RailpackProvider: "cobol"}, "build.railpackProvider"},
		"a configuration file outside":         {BuildPlanConfig{Method: BuildRailpack, RailpackConfig: "../railpack.json"}, "build.railpackConfig"},
		"a configuration file that is no JSON": {BuildPlanConfig{Method: BuildRailpack, RailpackConfig: "railpack.toml"}, "build.railpackConfig"},
		"the dashboard's own files":            {BuildPlanConfig{Method: BuildRailpack, RailpackConfig: ".just-dashboard/railpack.json"}, "build.railpackConfig"},
		"an output directory":                  {BuildPlanConfig{Method: BuildRailpack, OutputDirectory: "dist"}, "build.outputDirectory"},
	} {
		err := valid(test.build).Validate()
		var validation *ValidationError
		if !errors.As(err, &validation) || validation.Field != test.field {
			t.Errorf("%s = %v; want a refusal of %s", name, err, test.field)
		}
	}
	// Explicit build-secret steps are the recipes' own; Railpack mounts every
	// build variable in every command.
	mapped := valid(BuildPlanConfig{Method: BuildRailpack, Secrets: []BuildSecretConfig{{Variable: "TOKEN", Step: "install"}}})
	mapped.Variables = []PlannedVariable{{Name: "TOKEN", Sensitivity: "secret", Scopes: []string{"build"}}}
	if err := mapped.Validate(); err == nil || !strings.Contains(err.Error(), "reviewed automatic recipes") {
		t.Fatalf("mapped secrets = %v", err)
	}
	// Leaving Railpack for a recipe drops its settings rather than refusing.
	switched := valid(BuildPlanConfig{Method: BuildRecipe, Recipe: "node", RailpackVersion: RailpackDefaultVersion, RailpackProvider: "node", RailpackConfig: "railpack.json"})
	if switched.Build.RailpackVersion != "" || switched.Build.RailpackProvider != "" || switched.Build.RailpackConfig != "" || switched.Validate() != nil {
		t.Fatalf("switched build = %+v", switched.Build)
	}
	if err := (PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe, Recipe: "node", RailpackProvider: "node"}, Runtime: RuntimePlanConfig{Strategy: StrategyBlueGreen}}).Validate(); err == nil {
		t.Fatal("a recipe plan kept a Railpack provider")
	}
	for _, kind := range []SourceKind{SourceCompose, SourceImage, SourceBlueprint, SourceImport} {
		if err := refuseRailpackWithoutCheckout(kind, BuildPlanConfig{Method: BuildRailpack}); err == nil {
			t.Errorf("a %s source took a Railpack build", kind)
		}
	}
	for _, kind := range []SourceKind{SourceGit, SourceLocal} {
		if err := refuseRailpackWithoutCheckout(kind, BuildPlanConfig{Method: BuildRailpack}); err != nil {
			t.Errorf("a %s source refused Railpack: %v", kind, err)
		}
	}
}

func TestRailpackIsTheFallbackWhereNothingElseBuilds(t *testing.T) {
	t.Parallel()
	unknown := detectShapeFixture(t, map[string]string{"README.md": "# a tool\n", "main.v": "fn main() {}\n"})
	selected := selectedOf(unknown)
	if len(unknown.Candidates) != 1 || selected == nil || selected.BuildMethod != BuildRailpack ||
		selected.Confidence != ConfidenceLow || selected.Port != railpackDefaultPort || selected.Framework != "" ||
		len(selected.Evidence) != 1 || !strings.Contains(selected.Evidence[0].Reason, "no recipe or Dockerfile matched") {
		t.Fatalf("unknown repository = %#v", unknown.Candidates)
	}
	if !strings.Contains(unknown.SelectionReason, "the Railpack build in .") {
		t.Fatalf("selection reason = %q", unknown.SelectionReason)
	}
	// What the repository's shape set aside is understood, not unknown.
	android := detectShapeFixture(t, map[string]string{
		"settings.gradle": "include ':app'\n", "app/build.gradle": "plugins { id 'com.android.application' }\n",
	})
	if len(android.Candidates) != 0 {
		t.Fatalf("an Android repository got %#v", android.Candidates)
	}
	// Beside a recipe Railpack is a choice the candidate names, never a
	// candidate of its own and never the selection.
	node := detectShapeFixture(t, map[string]string{
		"package.json": `{"name":"api","scripts":{"start":"node index.js"}}`, "package-lock.json": `{"lockfileVersion":3,"packages":{"":{"name":"api"}}}`,
		"index.js": "require('http').createServer().listen(process.env.PORT)\n",
	})
	recipe := selectedOf(node)
	if len(node.Candidates) != 1 || recipe == nil || recipe.BuildMethod != BuildRecipe || recipe.RailpackProvider != "node" {
		t.Fatalf("node repository = %#v", node.Candidates)
	}
	for recipe, provider := range map[string]string{"go": "golang", "python": "python", "site": ""} {
		candidates := []DetectedCandidate{{BuildMethod: BuildRecipe, Recipe: recipe}}
		annotateRailpackProviders(candidates)
		if candidates[0].RailpackProvider != provider {
			t.Errorf("%s recipe = provider %q", recipe, candidates[0].RailpackProvider)
		}
	}
	// A Railpack candidate at the same root as a recipe that builds ranks
	// below it, whatever their confidence.
	candidates := []DetectedCandidate{
		newDetectedCandidate("", BuildRailpack, DetectedCandidate{Name: "Railpack build", Profile: ProfileWeb, Confidence: ConfidenceLow}),
		newDetectedCandidate("", BuildRecipe, DetectedCandidate{Name: "Node", Recipe: "node", Profile: ProfileWeb, Confidence: ConfidenceLow}),
	}
	if id, _ := rankCandidates(candidates); id != candidates[0].ID || candidates[0].BuildMethod != BuildRecipe {
		t.Fatalf("ranking chose %q: %#v", id, candidates)
	}
}

func TestRailpackPlanIsPreparedFromThePlannersOwnFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeBuildFixture(t, root, "package.json", `{"name":"api","scripts":{"start":"node index.js"}}`)
	backend := railpackPlanned(t)
	builder := NewArtifactBuilder(backend).withRailpackValues(map[string]string{"RAILPACK_PACKAGES": "jq@1.7"})
	config := BuildPlanConfig{Method: BuildRailpack, BuildCommand: "npm run build", StartCommand: "node index.js"}
	prepared, err := builder.Prepare(context.Background(), root, config, false, "just-dashboard/deployment-12:run-34", "API_TOKEN", "RAILPACK_PACKAGES")
	if err != nil {
		t.Fatal(err)
	}
	if len(backend.requests) != 1 {
		t.Fatalf("planner runs = %d", len(backend.requests))
	}
	request := backend.requests[0]
	if request.Image != railpackReleases[RailpackDefaultVersion] || request.Source != root ||
		!slices.Contains(request.Args, "--error-missing-start") {
		t.Fatalf("planner request = %+v", request)
	}
	joined := strings.Join(request.Args, " ")
	for _, want := range []string{"--plan-out /out/railpack-plan.json", "--info-out /out/railpack-info.json",
		"--build-cmd npm run build", "--start-cmd node index.js", "--env RAILPACK_PACKAGES=jq@1.7"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("planner args = %q; want %q", joined, want)
		}
	}
	if strings.Contains(joined, "API_TOKEN") {
		t.Fatalf("a build value reached the planner: %q", joined)
	}
	evidence := prepared.Railpack
	if evidence == nil || evidence.Version != RailpackDefaultVersion || !evidence.Success ||
		!slices.Equal(evidence.Providers, []string{"node"}) || len(evidence.Packages) != 1 ||
		evidence.Packages[0] != (RailpackPackage{Name: "node", Requested: "lts", Resolved: "24.22.0", Source: "railpack default"}) ||
		evidence.StartCommand != "npm run start" || evidence.Facts["nodePackageManager"] != "npm" ||
		!slices.Equal(evidence.Images, []string{"ghcr.io/railwayapp/railpack-builder:mise-2026.10.14", "ghcr.io/railwayapp/railpack-runtime:mise-2026.10.14"}) ||
		!slices.Equal(evidence.Secrets, []string{"API_TOKEN", "RAILPACK_PACKAGES"}) {
		t.Fatalf("evidence = %+v", evidence)
	}
	steps := map[string][]string{}
	for _, step := range evidence.Steps {
		steps[step.Name] = step.Commands
	}
	if !slices.Contains(steps["install"], "npm install") || !slices.Contains(steps["packages:mise"], "install mise packages: node") {
		t.Fatalf("steps = %+v", evidence.Steps)
	}
	if prepared.Method != BuildRailpack || prepared.Dockerfile != railpackPlanPath || prepared.Toolchain != "node 24.22.0 · railpack 0.43.0" ||
		prepared.SecretLayerGuarantee != "buildkit_ephemeral_mount" || len(prepared.BaseImages) != 0 {
		t.Fatalf("prepared = %+v", prepared)
	}
	argv := strings.Join(prepared.BuildArgv, " ")
	if !strings.Contains(argv, "--build-arg BUILDKIT_SYNTAX="+railpackReleases[RailpackDefaultVersion]) ||
		!strings.Contains(argv, "--build-arg cache-key=deployment-12") || !strings.Contains(argv, "--secret id=API_TOKEN,env=<ephemeral>") {
		t.Fatalf("build preview = %q", argv)
	}
	if !slices.Contains(prepared.Notes, "Railpack: ↳ Detected Node") || slices.ContainsFunc(prepared.Notes, func(note string) bool {
		return strings.Contains(note, "\x1b") || strings.TrimSpace(strings.TrimPrefix(note, "Railpack:")) == "" && strings.HasSuffix(note, ":")
	}) {
		t.Fatalf("notes = %q", prepared.Notes)
	}
	written, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(railpackPlanPath)))
	if err != nil {
		t.Fatal(err)
	}
	if digestBytes(written) != prepared.DockerfileDigest || prepared.DockerfilePreview != string(written) {
		t.Fatal("the recorded plan is not the plan the build reads")
	}
	var plan map[string]any
	if err := json.Unmarshal(written, &plan); err != nil {
		t.Fatal(err)
	}
	secrets, _ := json.Marshal(plan["secrets"])
	excludes, _ := json.Marshal(plan["exclude"])
	if string(secrets) != `["API_TOKEN","RAILPACK_PACKAGES"]` || string(excludes) != `["node_modules",".git",".just-dashboard",".just-dashboard-build-metadata-*"]` {
		t.Fatalf("plan secrets = %s, excludes = %s", secrets, excludes)
	}
	if _, kept := plan["caches"]; !kept {
		t.Fatal("the frontend's part of the plan did not pass through")
	}

	// The build goes through the pinned frontend with the deployment's own
	// cache key and a hash that follows the values, never the values.
	result, err := builder.Build(context.Background(), root, "just-dashboard/deployment-12:run-34", config, prepared,
		map[string]string{"API_TOKEN": "s3cr3t-value", "RAILPACK_PACKAGES": "jq@1.7"}, map[string]bool{"API_TOKEN": true}, "", SourceIdentity{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(backend.builds) != 1 {
		t.Fatalf("builds = %d", len(backend.builds))
	}
	invocation := backend.builds[0]
	if invocation.Dockerfile != railpackPlanPath || invocation.Frontend == nil ||
		invocation.Frontend.Image != railpackReleases[RailpackDefaultVersion] || invocation.Frontend.CacheKey != "deployment-12" ||
		len(invocation.Frontend.SecretsHash) != 64 || len(invocation.Secrets) != 2 || !invocation.Pull {
		t.Fatalf("invocation = %+v", invocation)
	}
	if result.Image.Reference != "just-dashboard/deployment-12:run-34" || len(result.Artifacts) != 1 || result.Prepared.Railpack == nil {
		t.Fatalf("result = %+v", result)
	}
	changed := railpackSecretsHash([]BuildSecretValue{{ID: "API_TOKEN", Value: "another"}, {ID: "RAILPACK_PACKAGES", Value: "jq@1.7"}})
	if changed == invocation.Frontend.SecretsHash || railpackSecretsHash(invocation.Secrets) != invocation.Frontend.SecretsHash {
		t.Fatal("the secrets hash does not follow the values")
	}
}

func TestRailpackConfigurationAndProviderReachThePlanner(t *testing.T) {
	t.Parallel()
	prepare := func(t *testing.T, files map[string]string, config BuildPlanConfig) (*railpackBackendFake, PreparedBuild, string, error) {
		root := t.TempDir()
		for name, content := range files {
			writeBuildFixture(t, root, name, content)
		}
		backend := railpackPlanned(t)
		config.Method = BuildRailpack
		prepared, err := NewArtifactBuilder(backend).Prepare(context.Background(), root, config, false, "t:1")
		return backend, prepared, root, err
	}
	// A repository's railpack.json is Railpack's to read; nothing is passed.
	backend, prepared, _, err := prepare(t, map[string]string{"railpack.json": `{"provider":"node"}`}, BuildPlanConfig{})
	if err != nil || slices.Contains(backend.requests[0].Args, "--config-file") || prepared.Railpack.ConfigFile != "railpack.json" {
		t.Fatalf("own config: %v %+v %+v", err, backend.requests, prepared.Railpack)
	}
	// Another file is named to the planner, relative to the root.
	backend, prepared, _, err = prepare(t, map[string]string{"deploy/railpack.json": `{}`}, BuildPlanConfig{RailpackConfig: "deploy/railpack.json"})
	if err != nil || !strings.Contains(strings.Join(backend.requests[0].Args, " "), "--config-file deploy/railpack.json") {
		t.Fatalf("named config: %v %+v", err, backend.requests)
	}
	// A provider is added to a copy of the repository's file.
	backend, prepared, root, err := prepare(t, map[string]string{"railpack.json": `{"deploy":{"startCommand":"node a.js"}}`}, BuildPlanConfig{RailpackProvider: "node"})
	if err != nil || !strings.Contains(strings.Join(backend.requests[0].Args, " "), "--config-file "+railpackGeneratedConfig) ||
		prepared.Railpack.ConfigFile != "railpack.json with the provider setting" || prepared.Railpack.Provider != "node" {
		t.Fatalf("provider: %v %+v %+v", err, backend.requests, prepared.Railpack)
	}
	generated, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(railpackGeneratedConfig)))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(generated, &document); err != nil || string(document["provider"]) != `"node"` || len(document["deploy"]) == 0 {
		t.Fatalf("generated config = %s", generated)
	}
	// A file named that is not there, or that a provider cannot be added to.
	if _, _, _, err := prepare(t, nil, BuildPlanConfig{RailpackConfig: "railpack.prod.json"}); !errors.Is(err, ErrUnsupportedBuilder) ||
		!strings.Contains(err.Error(), "Railpack configuration file railpack.prod.json") {
		t.Fatalf("missing config = %v", err)
	}
	if _, _, _, err := prepare(t, map[string]string{"railpack.json": "// comment\n{}"}, BuildPlanConfig{RailpackProvider: "node"}); !errors.Is(err, ErrUnsupportedBuilder) {
		t.Fatalf("commented config with a provider = %v", err)
	}
	// A railpack.json listing a secret the environment does not have.
	backend = railpackPlanned(t)
	var plan map[string]any
	_ = json.Unmarshal(backend.result.Plan, &plan)
	plan["secrets"] = []string{"STRIPE_KEY"}
	backend.result.Plan, _ = json.Marshal(plan)
	if _, err := NewArtifactBuilder(backend).Prepare(context.Background(), t.TempDir(), BuildPlanConfig{Method: BuildRailpack}, false, "t:1"); !errors.Is(err, ErrUnsupportedBuilder) ||
		!strings.Contains(err.Error(), "STRIPE_KEY") || recipeRefusalField(err.Error()) != "variables" {
		t.Fatalf("plan secret without a variable = %v", err)
	}
	// A dry run reads the plan's files and runs nothing.
	dry := &ArtifactBuilder{backend: railpackPlanned(t), dryRun: true}
	if _, err := dry.Prepare(context.Background(), t.TempDir(), BuildPlanConfig{Method: BuildRailpack}, false, "t:1"); err != nil {
		t.Fatal(err)
	}
	if runs := len(dry.backend.(*railpackBackendFake).requests); runs != 0 {
		t.Fatalf("a dry run ran the planner %d times", runs)
	}
	if err := dryRunBuild(context.Background(), t.TempDir(), BuildPlanConfig{Method: BuildRailpack, RailpackConfig: "nope.json"}, nil); err == nil {
		t.Fatal("the dry run did not refuse a missing configuration file")
	} else if finding, ok := dryRunFinding(err, BuildPlanConfig{Method: BuildRailpack}); !ok || finding.FieldID != "configuration.build.railpackConfig" {
		t.Fatalf("dry run finding = %+v", finding)
	}
}

func TestRailpackRefusalsAreNamedWithAFix(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		info, code, field string
		exit              int
		subjects          []string
	}{
		{"no-provider-info.json", "railpack_no_provider", "configuration.build.railpackProvider", 1, nil},
		{"no-start-info.json", "railpack_start_command_missing", "configuration.build.startCommand", 1, nil},
		{"unresolved-info.json", "railpack_package_unresolved", "", 1, []string{"node", "99.1.0"}},
		{"no-provider-info.json", "railpack_unavailable", "", 75, nil},
	} {
		t.Run(test.code, func(t *testing.T) {
			t.Parallel()
			backend := &railpackBackendFake{artifactBackendFake: &artifactBackendFake{}, result: RailpackPlanResult{
				ExitCode: test.exit, Info: railpackTestdata(t, test.info), Output: []string{"  ✖ the planner's words"},
			}}
			_, err := NewArtifactBuilder(backend).Prepare(context.Background(), t.TempDir(), BuildPlanConfig{Method: BuildRailpack}, false, "t:1")
			var failure *RailpackPrepareError
			if !errors.As(err, &failure) || failure.Evidence == nil || failure.Evidence.Success || len(failure.Output) != 1 {
				t.Fatalf("err = %v (%+v)", err, failure)
			}
			if test.exit == 75 != errors.Is(err, ErrBuilderUnavailable) || test.exit != 75 && !errors.Is(err, ErrUnsupportedBuilder) {
				t.Fatalf("%v unwraps to the wrong sentinel", err)
			}
			cause := railpackPrepareCause(failure, causeContext{build: BuildPlanConfig{Method: BuildRailpack}})
			if cause.Code != test.code || cause.Phase != phasePlan || cause.ExitCode != test.exit || !slices.Equal(cause.Subjects, test.subjects) {
				t.Fatalf("cause = %+v", cause)
			}
			if test.field != "" && (cause.Fix == nil || cause.Fix.Kind != fixReview || cause.Fix.Field != test.field) {
				t.Fatalf("fix = %+v", cause.Fix)
			}
			sentence := cause.sentence()
			if !strings.HasPrefix(sentence, "Railpack could not plan the build") || !strings.HasSuffix(sentence, ".") ||
				strings.Contains(sentence, "\n") || causeTitle(cause.Code) == "" {
				t.Fatalf("sentence = %q", sentence)
			}
		})
	}
	// The build's own failures: a language release mise could not install,
	// and a Debian package apt does not have, each in its tool's words.
	railpack := BuildPlanConfig{Method: BuildRailpack}
	for _, test := range []buildCase{
		{
			name: "mise install", command: "mise install", exit: 1, build: railpack,
			lines: []string{
				"mise by @jdx – installing 1 tool",
				"mise ✗ node@24.99.7  348ms · failed: HTTP status client error (404 Not Found) for url (https://nodejs.org/dist/v24.99.7/node-v24.99.7.tar.gz)",
				"mise ERROR Failed to install core:node@24.99.7: HTTP status client error (404 Not Found) for url (https://nodejs.org/dist/v24.99.7/node-v24.99.7.tar.gz)",
				"mise ERROR Version: 2026.10.14 linux-x64 (2026-10-18)",
			},
			want: BuildCause{Code: "railpack_package_install_failed", Phase: phaseInstall, Command: "mise install", ExitCode: 1, Detail: "mise", Subjects: []string{"node@24.99.7"}},
		},
		{
			name: "apt package", command: "sh -c apt-get update && apt-get install -y jd-no-such-package", exit: 100, build: railpack,
			lines: []string{"Reading package lists...", "Building dependency tree...", "E: Unable to locate package jd-no-such-package"},
			want: BuildCause{Code: "build_apt_package_missing", Phase: phaseInstall, Command: "apt-get update && apt-get install -y jd-no-such-package",
				ExitCode: 100, Detail: "apt", Subjects: []string{"jd-no-such-package"}},
		},
		{
			name: "the plan's build command", command: "npm run build", exit: 3, build: railpack,
			lines: []string{"> x@1.0.0 build", "> node build.js"},
			want:  BuildCause{Code: "build_failed", Phase: phaseBuild, Command: "npm run build", ExitCode: 3},
		},
	} {
		collector, err := feedBuild(test)
		cause := buildFailureCause(err, collector, causeContext{build: test.build}, nil)
		if cause == nil {
			t.Fatalf("%s: no cause", test.name)
		}
		got := *cause
		got.LineSeq = 0
		if got.Code != test.want.Code || got.Phase != test.want.Phase || got.Command != test.want.Command ||
			got.ExitCode != test.want.ExitCode || got.Detail != test.want.Detail || !slices.Equal(got.Subjects, test.want.Subjects) {
			t.Fatalf("%s: cause = %+v\nwant %+v", test.name, got, test.want)
		}
		if sentence := cause.sentence(); !strings.HasSuffix(sentence, ".") || causeTitle(cause.Code) == "" {
			t.Fatalf("%s: sentence = %q", test.name, sentence)
		}
	}
	apt := &BuildCause{Code: "build_apt_package_missing", Subjects: []string{"libfoo"}}
	if _, action := apt.explain(); !strings.Contains(action, "RAILPACK_BUILD_APT_PACKAGES") {
		t.Fatalf("Railpack apt action = %q", action)
	}
	pythonApt := &BuildCause{Code: "build_apt_package_missing", Subjects: []string{"libfoo"}}
	pythonApt.Fix = buildCauseFix(pythonApt, causeContext{build: BuildPlanConfig{Method: BuildRecipe, Recipe: "python"}})
	if pythonApt.Fix == nil || pythonApt.Fix.Field != "configuration.build.systemPackages" {
		t.Fatalf("Python recipe apt fix = %+v", pythonApt.Fix)
	}
}

func TestRailpackEvidenceIsBoundedAndScreened(t *testing.T) {
	t.Parallel()
	info := railpackInfo{
		DetectedProviders: []string{"node", "node", "bad provider"},
		Metadata:          map[string]string{"token": "postgres://admin:hunter2@db/app", "nodeRuntime": "\x1b[31mnode\x1b[0m"},
	}
	info.Logs = append(info.Logs, struct {
		Level string `json:"level"`
		Msg   string `json:"msg"`
	}{"error", strings.Repeat("x", 2000)}, struct {
		Level string `json:"level"`
		Msg   string `json:"msg"`
	}{"debug", "not one of Railpack's levels"})
	evidence := &RailpackEvidence{}
	applyRailpackInfo(evidence, info)
	if !slices.Equal(evidence.Providers, []string{"node"}) || evidence.Facts["nodeRuntime"] != "node" ||
		!strings.HasPrefix(evidence.Facts["token"], "(withheld") || len(evidence.Logs) != 1 || len(evidence.Logs[0].Message) > railpackMessageLength {
		t.Fatalf("evidence = %+v", evidence)
	}
	if railpackCacheKey("just-dashboard/deployment-7:run-9") != "deployment-7" || railpackCacheKey("Weird Tag:1") != "weird-tag" {
		t.Fatal("cache key is not the deployment's")
	}
	if railpackStepCommand(`sh -c 'apt-get update && apt-get install -y curl'`) != "apt-get update && apt-get install -y curl" ||
		railpackStepCommand("npm ci") != "npm ci" {
		t.Fatal("Railpack's shell wrapper was not taken off")
	}
	values := railpackPlanValues(map[string]string{
		"RAILPACK_PACKAGES": "jq", "RAILPACK_SECRET_THING": "x", "RAILPACK_CONFIG_FILE": "other.json", "API_URL": "https://x",
		"RAILPACK_DATABASE_URL": "postgres://u:p@h/d",
	}, map[string]bool{"RAILPACK_SECRET_THING": true})
	if len(values) != 1 || values["RAILPACK_PACKAGES"] != "jq" {
		t.Fatalf("plan values = %v", values)
	}
}

func TestRailpackPlannerRunsWithNothingOfTheHost(t *testing.T) {
	t.Parallel()
	spec := railpackPrepareSpec(RailpackPlanRequest{Image: railpackReleases[RailpackDefaultVersion], Source: "/srv/checkout", Args: []string{"--error-missing-start"}}, "/var/lib/jd/railpack/prepare-1", "abc")
	if spec.Image != railpackReleases[RailpackDefaultVersion] || !slices.Equal(spec.Entrypoint, []string{"/railpack"}) ||
		!slices.Equal(spec.Command, []string{"prepare", "/app", "--error-missing-start"}) ||
		!spec.ReadOnly || !spec.NoNewPrivileges || !slices.Equal(spec.CapDrop, []string{"ALL"}) || spec.Privileged ||
		len(spec.CapAdd) != 0 || spec.NetworkMode != "bridge" || len(spec.Ports) != 0 || spec.Limits.MemoryMB == 0 || spec.Limits.PidsLimit == 0 {
		t.Fatalf("spec = %+v", spec)
	}
	mounts := map[string]string{}
	for _, mount := range spec.Mounts {
		mode := "rw"
		if mount.ReadOnly {
			mode = "ro"
		}
		if mount.Exec {
			mode += ",exec"
		}
		mounts[mount.Target] = mount.Type + ":" + mount.Source + ":" + mode
	}
	if len(mounts) != 3 || mounts["/app"] != "bind:/srv/checkout:ro" || mounts["/out"] != "bind:/var/lib/jd/railpack/prepare-1:rw" ||
		mounts["/tmp"] != "tmpfs::rw,exec" {
		t.Fatalf("mounts = %v", mounts)
	}
}

func TestRailpackPreflightSaysWhatItDoesNotPin(t *testing.T) {
	t.Parallel()
	configuration := PlanConfiguration{
		Build:     BuildPlanConfig{Method: BuildRailpack},
		Variables: []PlannedVariable{{Name: "RAILPACK_PACKAGES", Sensitivity: "plain", Scopes: []string{"build"}}},
	}
	detection := &DetectionResult{Candidates: []DetectedCandidate{
		newDetectedCandidate("", BuildRailpack, DetectedCandidate{Name: "Railpack build", Profile: ProfileWeb, Confidence: ConfidenceLow}),
	}}
	detection.SelectedID = detection.Candidates[0].ID
	draft := &Draft{Data: DraftData{Detection: detection}}
	codes := func(observation HostObservation) map[string]PreflightSeverity {
		found := map[string]PreflightSeverity{}
		for _, item := range railpackFindings(draft, configuration, observation) {
			found[item.Code] = item.Severity
		}
		return found
	}
	unreachable := codes(HostObservation{Railpack: &RailpackObservation{Detail: "dial tcp: lookup ghcr.io: no such host"}})
	if unreachable["railpack_unavailable"] != PreflightBlocked || unreachable["railpack_bases_unpinned"] != PreflightWarning ||
		unreachable["railpack_detection_deferred"] != PreflightWarning || unreachable["railpack_variables_configure_plan"] != PreflightPass {
		t.Fatalf("findings = %v", unreachable)
	}
	if ready := codes(HostObservation{Railpack: &RailpackObservation{Pullable: true}}); ready["railpack_ready"] != PreflightPass || ready["railpack_unavailable"] != "" {
		t.Fatalf("findings = %v", ready)
	}
	if railpackObservedImage(BuildPlanConfig{Method: BuildRecipe}) != "" || railpackObservedImage(configuration.Build) != railpackReleases[RailpackDefaultVersion] {
		t.Fatal("preflight asks about the wrong image")
	}
	if !buildMethodNeedsBuildx(BuildRailpack, nil) {
		t.Fatal("a Railpack build does not ask for Buildx")
	}
}
