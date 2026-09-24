package deploy

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func detectNodeTree(t *testing.T, files map[string]string) (DetectionResult, DetectedCandidate) {
	t.Helper()
	result, err := (Detector{}).DetectPath(context.Background(), writeNodeTree(t, files),
		SourceIdentity{Kind: SourceGit, Revision: strings.Repeat("a", 40)})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Candidates) == 0 {
		t.Fatal("no candidate")
	}
	selected := selectedDetectionCandidate(&result)
	if selected == nil {
		selected = &result.Candidates[0]
		result.SelectedID = selected.ID
	}
	return result, *selected
}

func nodeDraft(result DetectionResult) *Draft {
	return &Draft{Data: DraftData{
		Intent:    &DraftIntentConfig{Name: "svc", Profile: ProfileWeb},
		Source:    &DraftSourceConfig{Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://example.test/o/r.git", Ref: "main"},
		Detection: &result,
	}}
}

var dockerHost = HostObservation{Facilities: map[string]FacilityObservation{"docker": {Available: true}}}

// A repository without a lockfile is ordinary, not a question: detection
// keeps its confidence, the build installs unfrozen, and preflight says what
// a rebuild may do differently.
func TestNoLockfileIsAWarningNotARefusal(t *testing.T) {
	t.Parallel()
	result, candidate := detectNodeTree(t, map[string]string{
		"package.json": `{"name":"api","scripts":{"start":"node index.js"},"dependencies":{"express":"^5.1.0"}}`,
	})
	if candidate.Confidence == ConfidenceLow || len(candidate.NeedsDecision) != 0 || candidate.PackageManager != "npm" || candidate.RecipeIssue != "" {
		t.Fatalf("candidate = confidence %q decisions %v manager %q issue %q", candidate.Confidence, candidate.NeedsDecision, candidate.PackageManager, candidate.RecipeIssue)
	}
	findings := preflightFindings(nodeDraft(result), nodeTestConfiguration(BuildPlanConfig{Method: BuildRecipe, Recipe: "node", StartCommand: candidate.StartCommand}), dockerHost, false)
	unpinned := findingByCode(findings, "dependencies_unpinned")
	if unpinned == nil || unpinned.Severity != PreflightWarning || !strings.Contains(unpinned.Means, "npm install --no-audit --no-fund") ||
		!strings.Contains(unpinned.Action, "npm install") {
		t.Fatalf("unpinned = %+v", unpinned)
	}
	if strings.Contains(unpinned.Action, "uv lock") {
		t.Fatal("a Node project was told how to pin Python")
	}
}

// Detection records the install each manager would run, with the commands
// for that manager's runner, so the form swaps whole commands and preflight
// judges a choice without reading the source.
func TestDetectionRecordsTheInstallForEveryManager(t *testing.T) {
	t.Parallel()
	_, candidate := detectNodeTree(t, map[string]string{
		"package.json": `{"name":"kit","scripts":{"build":"vite build"},"devDependencies":{"vite":"6","@sveltejs/kit":"2","@sveltejs/adapter-node":"5"}}`,
		"bun.lock":     `{"lockfileVersion":1,"workspaces":{"":{"devDependencies":{"vite":"6","@sveltejs/kit":"2","@sveltejs/adapter-node":"5"}}},"packages":{}}`,
	})
	installs := map[string]DetectedNodeInstall{}
	for _, install := range candidate.NodeInstalls {
		installs[install.Manager] = install
	}
	if bun := installs["bun"]; bun.Install != "bun install --frozen-lockfile" || bun.StartCommand != "bun ./build/index.js" || bun.BuildCommand != "bun run build" {
		t.Fatalf("bun install = %+v", bun)
	}
	npm := installs["npm"]
	if npm.Install != "" || npm.StartCommand != "node build" || npm.BuildCommand != "npm run build" ||
		len(npm.Findings) != 1 || npm.Findings[0].Code != "package_manager_lockfile_missing" || npm.Findings[0].Severity != PreflightBlocked {
		t.Fatalf("npm install = %+v", npm)
	}
	if candidate.NodeVersion == "" {
		t.Fatal("the Node release the recipe builds on is not recorded")
	}
	// The draft bounds accept what detection records.
	source := DraftSourceConfig{Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://example.test/o/r.git", Ref: "main"}
	result := DetectionResult{Source: SourceIdentity{Kind: SourceGit, Revision: strings.Repeat("a", 40)}, Candidates: []DetectedCandidate{candidate}, SelectedID: candidate.ID}
	var err error
	result.Source.Remote, result.Source.Repository, err = remoteForSource(source)
	if err != nil {
		t.Fatal(err)
	}
	result.Source.Ref = canonicalSourceConfig(source).Ref
	if err := validateDetectionResult(&source, result); err != nil {
		t.Fatalf("detection does not validate: %v", err)
	}
	candidate.NodeInstalls[0].Findings[0].Severity = "fatal"
	result.Candidates = []DetectedCandidate{candidate}
	if err := validateDetectionResult(&source, result); err == nil {
		t.Fatal("an unknown finding severity was accepted")
	}
}

// The operator's choice is judged by the record for that manager: one with
// no lockfile while another is committed is refused before Deploy, and a
// command still naming another manager's runner is named with the command
// the build will actually run.
func TestPreflightJudgesThePackageManagerChoice(t *testing.T) {
	t.Parallel()
	result, candidate := detectNodeTree(t, map[string]string{
		"package.json": `{"name":"api","scripts":{"build":"tsc","start":"node dist/index.js"},"dependencies":{"express":"^5.1.0"}}`,
		"bun.lock":     `{"lockfileVersion":1,"workspaces":{"":{"dependencies":{"express":"^5.1.0"}}},"packages":{}}`,
	})
	configuration := nodeTestConfiguration(BuildPlanConfig{Method: BuildRecipe, Recipe: "node", PackageManager: "pnpm", BuildCommand: "pnpm run build", StartCommand: "pnpm run start"})
	missing := findingByCode(preflightFindings(nodeDraft(result), configuration, dockerHost, false), "package_manager_lockfile_missing")
	if missing == nil || missing.Severity != PreflightBlocked || !strings.Contains(missing.Measured, "bun.lock is committed") {
		t.Fatalf("missing lockfile = %+v", missing)
	}

	configuration.Build.PackageManager = ""
	configuration.Build.BuildCommand, configuration.Build.StartCommand = "npm run build", candidate.StartCommand
	findings := preflightFindings(nodeDraft(result), configuration, dockerHost, false)
	mismatch := findingByCode(findings, "runner_mismatch")
	if mismatch == nil || mismatch.Severity != PreflightWarning || mismatch.Measured != "`npm run build` runs as `bun run build`" ||
		mismatch.FieldID != "configuration.build.buildCommand" {
		t.Fatalf("runner mismatch = %+v", mismatch)
	}
	commands := findingByCode(findings, "build_commands")
	if commands == nil || commands.Measured != "bun install --frozen-lockfile · bun run build · start: bun run start" {
		t.Fatalf("build commands = %+v", commands)
	}
	if runtime := findingByCode(findings, "runtime_selected"); runtime == nil || runtime.Severity != PreflightPass {
		t.Fatalf("runtime selected = %+v", runtime)
	}

	configuration.Build.BuildCommand = "npm install && npm run build"
	if again := findingByCode(preflightFindings(nodeDraft(result), configuration, dockerHost, false), "install_in_build_command"); again == nil {
		t.Fatal("a second install in the build command was not named")
	}
}

// A registry credential .npmrc names is found, offered as a variable for
// the install step, mapped there when the draft gives it a value, and
// refused before Deploy when a scoped dependency needs it and it cannot
// reach the install.
func TestRegistryCredentialsReachTheInstall(t *testing.T) {
	t.Parallel()
	result, candidate := detectNodeTree(t, map[string]string{
		"package.json":      `{"name":"app","scripts":{"start":"node index.js"},"dependencies":{"@acme/ui":"^1.0.0","left-pad":"^1.3.0"}}`,
		"package-lock.json": `{"lockfileVersion":3,"packages":{"":{"dependencies":{"@acme/ui":"^1.0.0","left-pad":"^1.3.0"}}}}`,
		".npmrc":            "@acme:registry=https://npm.pkg.github.com\n//npm.pkg.github.com/:_authToken=${NODE_AUTH_TOKEN}\n//registry.npmjs.org/:_authToken=${NPM_TOKEN}\n",
		".env.example":      "DATABASE_URL=postgres://localhost/app\n",
	})
	variable := func(name string) *DetectedVariable {
		index := slices.IndexFunc(candidate.Variables, func(variable DetectedVariable) bool { return variable.Name == name })
		if index < 0 {
			t.Fatalf("variables = %+v lack %s", candidate.Variables, name)
		}
		return &candidate.Variables[index]
	}
	if token := variable("NODE_AUTH_TOKEN"); token.Step != "install" || !token.InstallRequired || !slices.Equal(token.Sources, []string{".npmrc"}) {
		t.Fatalf("NODE_AUTH_TOKEN = %+v", token)
	}
	if public := variable("NPM_TOKEN"); public.Step != "install" || public.InstallRequired {
		t.Fatalf("NPM_TOKEN = %+v", public)
	}
	if database := variable("DATABASE_URL"); database.Step != "" {
		t.Fatalf("DATABASE_URL = %+v", database)
	}

	draft := nodeDraft(result)
	configuration := nodeTestConfiguration(BuildPlanConfig{Method: BuildRecipe, Recipe: "node", StartCommand: "npm run start"})
	findings := preflightFindings(draft, configuration, dockerHost, false)
	required := slices.IndexFunc(findings, func(item PreflightFinding) bool {
		return item.Code == "registry_token_missing" && item.Measured == "NODE_AUTH_TOKEN"
	})
	optional := slices.IndexFunc(findings, func(item PreflightFinding) bool {
		return item.Code == "registry_token_missing" && item.Measured == "NPM_TOKEN"
	})
	if required < 0 || findings[required].Severity != PreflightBlocked || optional < 0 || findings[optional].Severity != PreflightWarning {
		t.Fatalf("registry findings = %+v", findings)
	}

	// A value supplied with the draft is mapped to the install step.
	draft.environment = map[string]string{"NODE_AUTH_TOKEN": "ghp_example", "NPM_TOKEN": "npm_example"}
	mapped := draft.withEnvironmentMetadata(configuration)
	if err := mapped.Validate(); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(mapped.Build.Secrets, BuildSecretConfig{Variable: "NODE_AUTH_TOKEN", Step: "install"}) ||
		!slices.Contains(mapped.Build.Secrets, BuildSecretConfig{Variable: "NPM_TOKEN", Step: "install"}) {
		t.Fatalf("secrets = %+v", mapped.Build.Secrets)
	}
	if slices.ContainsFunc(preflightFindings(draft, mapped, dockerHost, false), func(item PreflightFinding) bool { return item.Code == "registry_token_missing" }) {
		t.Fatal("a mapped credential was still reported missing")
	}
	// And the install RUN, not the build RUN, mounts it.
	prepared, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), writeNodeTree(t, map[string]string{
		"package.json": `{"name":"app","scripts":{"start":"node index.js"},"dependencies":{"left-pad":"^1.3.0"}}`,
	}), mapped.Build, false, "t:1", "NODE_AUTH_TOKEN", "NPM_TOKEN")
	if err != nil {
		t.Fatal(err)
	}
	assertDockerfile(t, prepared.DockerfilePreview, []string{
		"RUN --mount=type=secret,id=NODE_AUTH_TOKEN,env=NODE_AUTH_TOKEN,required=true --mount=type=secret,id=NPM_TOKEN,env=NPM_TOKEN,required=true npm install",
	}, []string{"ghp_example", "npm_example"})
}

// Yarn Berry aborts every install when a credential it expands is unset
// and has no default, public packages included; Bun reads its own file.
func TestYarnAndBunRegistryCredentials(t *testing.T) {
	t.Parallel()
	yarn := readYarnConfig([]byte("npmScopes:\n  acme:\n    npmAuthToken: \"${ACME_TOKEN}\"\nnpmAuthToken: \"${NPM_TOKEN:-}\"\nnodeLinker: node-modules\n"))
	if yarn.linker != "node-modules" || len(yarn.variables) != 2 {
		t.Fatalf("yarn config = %+v", yarn)
	}
	for _, variable := range yarn.variables {
		if variable.InstallRequired != (variable.Name == "ACME_TOKEN") {
			t.Fatalf("yarn variable = %+v", variable)
		}
	}
	bun := readBunfigRegistry([]byte("[install]\nregistry = { url = \"https://r.example/\", token = \"$REGISTRY_TOKEN\" }\n\n[install.scopes]\nacme = { token = \"$ACME_TOKEN\", url = \"https://npm.acme.dev/\" }\nother = \"https://user:${OTHER_PASS}@npm.other.dev/\"\n"),
		map[string]bool{"@acme": true})
	got := map[string]bool{}
	for _, variable := range bun {
		got[variable.Name] = variable.InstallRequired
	}
	if !got["REGISTRY_TOKEN"] || !got["ACME_TOKEN"] || got["OTHER_PASS"] || len(got) != 3 {
		t.Fatalf("bunfig variables = %v", got)
	}
	npmrc := readNPMRC([]byte("//registry.npmjs.org/:_authToken=npm_abcdef\n"), ".npmrc", nil)
	if !npmrc.literal || len(npmrc.variables) != 0 {
		t.Fatalf("a literal token = %+v", npmrc)
	}
}

// The PHP recipe's asset stage installs through the same planner, so a
// Laravel application's competing lockfiles or missing lockfile are named
// before Deploy, and its package manager is a choice the plan may carry.
func TestPHPAssetStageSharesTheNodeInstall(t *testing.T) {
	t.Parallel()
	laravel := map[string]string{
		"composer.json":    `{"require":{"php":"^8.3","laravel/framework":"^12.0"}}`,
		"composer.lock":    "{}",
		"artisan":          "",
		"public/index.php": "<?php",
		"package.json":     `{"private":true,"scripts":{"build":"vite build"},"devDependencies":{"vite":"^7","laravel-vite-plugin":"^2"}}`,
	}
	competing := map[string]string{"bun.lock": "{}", "package-lock.json": "{}"}
	for name, content := range laravel {
		competing[name] = content
	}
	result, candidate := detectNodeTree(t, competing)
	if candidate.Recipe != "php" || !slices.Equal(candidate.PackageManagers, []string{"bun", "npm"}) ||
		!slices.ContainsFunc(candidate.NeedsDecision, func(decision string) bool { return strings.Contains(decision, "competing lockfiles") }) {
		t.Fatalf("candidate = %+v", candidate)
	}
	configuration := nodeTestConfiguration(BuildPlanConfig{Method: BuildRecipe, Recipe: "php", StartCommand: candidate.StartCommand})
	if ambiguous := findingByCode(preflightFindings(nodeDraft(result), configuration, dockerHost, false), "package_manager_ambiguous"); ambiguous == nil {
		t.Fatal("the asset stage's competing lockfiles were not raised before Deploy")
	}
	configuration.Build.PackageManager = "bun"
	if err := configuration.Validate(); err != nil {
		t.Fatalf("a PHP plan cannot choose its asset manager: %v", err)
	}
	if commands := findingByCode(preflightFindings(nodeDraft(result), configuration, dockerHost, false), "build_commands"); commands == nil ||
		commands.Measured != "bun install --frozen-lockfile · bun run build" {
		t.Fatalf("asset commands = %+v", commands)
	}

	result, candidate = detectNodeTree(t, laravel)
	configuration = nodeTestConfiguration(BuildPlanConfig{Method: BuildRecipe, Recipe: "php", StartCommand: candidate.StartCommand})
	if unpinned := findingByCode(preflightFindings(nodeDraft(result), configuration, dockerHost, false), "assets_dependencies_unpinned"); unpinned == nil || unpinned.Severity != PreflightWarning {
		t.Fatalf("an asset stage without a lockfile = %+v", unpinned)
	}
	prepared, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), writeNodeTree(t, laravel), configuration.Build, false, "t:1")
	if err != nil {
		t.Fatal(err)
	}
	assertDockerfile(t, prepared.DockerfilePreview, []string{"FROM node:22-alpine@sha256:", " AS assets\n", "RUN npm install --no-audit --no-fund\n", nodeBuildRun("npm run build\n")}, nil)
}
