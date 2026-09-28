package deploy

import (
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The recipes were written ecosystem by ecosystem, and meet where every one
// of them has to agree with the rest of the product: the working and data
// directories state detection places files and volumes in, the proxy trust
// the runtime withdraws from a container published to everyone, the version
// files a repository shares between its languages, and the settings the
// configure form offers.

// recipeFixtures is a minimal project for every automatic recipe, each one
// detected as that recipe's candidate.
func recipeFixtures() map[string]map[string]string {
	sinatraLock := "GEM\n  remote: https://rubygems.org/\n  specs:\n    puma (6.6.0)\n    sinatra (4.1.1)\n\n" +
		"PLATFORMS\n  x86_64-linux\n  aarch64-linux\n\nDEPENDENCIES\n  puma\n  sinatra\n\nBUNDLED WITH\n   2.6.9\n"
	return map[string]map[string]string{
		"node": {"package.json": `{"scripts":{"start":"node server.js"},"dependencies":{"express":"4"}}`,
			"package-lock.json": `{"name":"x","lockfileVersion":3,"requires":true,"packages":{"":{"dependencies":{"express":"4"}}}}`, "server.js": ""},
		"php":    {"composer.json": `{"require":{"php":">=8.2"}}`, "index.php": "<?php echo 1;"},
		"deno":   {"deno.json": `{"tasks":{"start":"deno run -A main.ts"}}`, "main.ts": "Deno.serve(() => new Response('x'))\n"},
		"python": {"requirements.txt": "fastapi\nuvicorn\n", "main.py": "from fastapi import FastAPI\napp = FastAPI()\n"},
		"go":     {"go.mod": "module svc\ngo 1.26\n", "main.go": "package main\nfunc main() {}\n"},
		"rust":   {"Cargo.toml": "[package]\nname = \"svc\"\nversion = \"0.1.0\"\n[dependencies]\nrocket = \"0.5\"\n", "src/main.rs": "fn main() {}\n"},
		"java":   {"pom.xml": "<project><parent><artifactId>spring-boot-starter-parent</artifactId></parent></project>"},
		// The committed SQLite file seeds the volume the data directory is.
		"dotnet": {"api.csproj": `<Project Sdk="Microsoft.NET.Sdk.Web"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup>` +
			`<ItemGroup><PackageReference Include="Microsoft.EntityFrameworkCore.Sqlite" Version="8.0.0" /></ItemGroup></Project>`,
			"Program.cs": "", "appsettings.json": `{"ConnectionStrings":{"Default":"Data Source=app.db"}}`, "app.db": "sqlite"},
		"ruby":   {"Gemfile": "source \"https://rubygems.org\"\ngem \"sinatra\"\ngem \"puma\"\n", "Gemfile.lock": sinatraLock, "config.ru": "require './app'\nrun Sinatra::Application\n", "app.rb": "require 'sinatra'\n"},
		"elixir": {"mix.exs": phoenixMix, "mix.lock": "%{}\n"},
		"scala": {"build.sbt": "name := \"\"\"shop-web\"\"\"\n\nlazy val root = (project in file(\".\")).enablePlugins(PlayScala)\n",
			"project/plugins.sbt": "addSbtPlugin(\"org.playframework\" % \"sbt-plugin\" % \"3.0.11\")\n", "project/build.properties": "sbt.version=1.11.7\n",
			"conf/routes": "GET / controllers.HomeController.index()\n"},
		"clojure": {"project.clj": "(defproject shop \"0.1.0\" :dependencies [[org.clojure/clojure \"1.12.0\"] [ring/ring-jetty-adapter \"1.12.2\"]] :main shop.core :uberjar-name \"shop.jar\")\n",
			"src/shop/core.clj": "(ns shop.core)\n"},
		"dart": {"pubspec.yaml": "name: api\nenvironment:\n  sdk: '>=3.0.0 <3.11.0'\ndependencies:\n  shelf: ^1.4.0\n", "bin/server.dart": "void main() {}\n"},
		"gleam": {"gleam.toml": "name = \"api\"\n\n[dependencies]\nmist = \">= 5.0.0 and < 6.0.0\"\n", "manifest.toml": "packages = []\n",
			"src/api.gleam": "pub fn main() {\n  mist.new(handler) |> mist.bind(\"0.0.0.0\") |> mist.port(8123) |> mist.start\n}\n"},
	}
}

// preparedRecipeFixture detects a fixture and prepares its candidate the way
// a plan saved from detection builds it.
func preparedRecipeFixture(t *testing.T, recipe string, files map[string]string) (*DetectedCandidate, PreparedBuild) {
	t.Helper()
	candidate := selectedOf(detectShapeFixture(t, files))
	if candidate == nil || candidate.Recipe != recipe || candidate.BuildMethod != BuildRecipe {
		t.Fatalf("%s fixture detected as %#v", recipe, candidate)
	}
	prepared, err := prepareFixture(t, files, BuildPlanConfig{Method: BuildRecipe, Recipe: recipe, BuildCommand: candidate.BuildCommand,
		StartCommand: candidate.StartCommand, OutputDirectory: candidate.OutputDirectory})
	if err != nil {
		t.Fatalf("%s: %v", recipe, err)
	}
	return candidate, prepared
}

// finalStage is a Dockerfile's instructions from its last FROM on: what the
// container runs. A last stage built FROM an earlier one (the PHP recipe's
// php-base) starts with that stage's instructions, which it inherits.
func finalStage(dockerfile string) []string {
	lines := strings.Split(strings.TrimSpace(dockerfile), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		if !strings.HasPrefix(lines[index], "FROM ") {
			continue
		}
		parent := strings.Fields(lines[index])[1]
		for start := index - 1; start >= 0; start-- {
			if strings.HasPrefix(lines[start], "FROM ") && strings.HasSuffix(lines[start], " AS "+parent) {
				end := start + 1
				for end < index && !strings.HasPrefix(lines[end], "FROM ") {
					end++
				}
				return append(finalStage(strings.Join(lines[:end], "\n")), lines[index+1:]...)
			}
		}
		return lines[index:]
	}
	return nil
}

// Every recipe's runtime stage is what state detection assumes it is
// (candidateStateLayout): the working directory a relative path resolves
// against, whether the process runs as root, and — for an unprivileged
// process — a data directory it owns, so the volume a SQLite file or an
// upload directory is moved to starts writable and is seeded from the image.
// The proxy trust a recipe writes is the trust the runtime can withdraw.
func TestRecipeRuntimeStagesMatchTheStateLayoutAndProxyTrust(t *testing.T) {
	t.Parallel()
	fixtures := recipeFixtures()
	for _, recipe := range []string{"node", "go", "python", "rust", "java", "dotnet", "deno", "php", "ruby", "elixir", "scala", "clojure", "dart", "gleam"} {
		t.Run(recipe, func(t *testing.T) {
			t.Parallel()
			if !validRecipe(recipe) {
				t.Fatalf("%s is not a recipe", recipe)
			}
			candidate, prepared := preparedRecipeFixture(t, recipe, fixtures[recipe])
			layout, ok := candidateStateLayout(candidate, nil)
			if !ok {
				t.Fatalf("state detection has no layout for the %s recipe", recipe)
			}
			stage := finalStage(prepared.DockerfilePreview)
			user, userAt, workdir := "", -1, ""
			for index, line := range stage {
				switch {
				case strings.HasPrefix(line, "USER "):
					user, userAt = strings.TrimPrefix(line, "USER "), index
				case strings.HasPrefix(line, "WORKDIR "):
					workdir = strings.TrimPrefix(line, "WORKDIR ")
				}
			}
			root := user == "" || user == "root" || user == "0"
			if root != layout.root || workdir != layout.workdir {
				t.Fatalf("runtime stage runs as %q from %q; state detection assumes root=%v from %q:\n%s",
					user, workdir, layout.root, layout.workdir, strings.Join(stage, "\n"))
			}
			if !layout.root && layout.dataDir != "" {
				owned := false
				for index, line := range stage {
					if !strings.HasPrefix(line, "RUN ") || !strings.Contains(line, "mkdir -p") || !strings.Contains(line, layout.dataDir) {
						continue
					}
					// Created by the unprivileged user itself, or handed to it.
					_, chowned, _ := strings.Cut(line, "chown ")
					owned = owned || index > userAt || strings.Contains(chowned, layout.dataDir)
				}
				if !owned {
					t.Fatalf("%s is not created for %q:\n%s", layout.dataDir, user, strings.Join(stage, "\n"))
				}
			}
			for _, persistent := range candidate.PersistentPaths {
				if persistent.Target != layout.dataDir {
					t.Fatalf("%s state is mounted at %q, not the data directory %q", persistent.Path, persistent.Target, layout.dataDir)
				}
			}
			if recipe == "dotnet" {
				// The committed database lands in the data directory before
				// the process drops to its user, so a new volume starts from it.
				seed := "COPY --from=build --chown=app:app /src/app.db " + layout.dataDir + "/app.db"
				if len(candidate.PersistentPaths) != 1 || slices.Index(stage, seed) < 0 || slices.Index(stage, seed) > userAt {
					t.Fatalf("the committed app.db does not seed %s (%#v):\n%s", layout.dataDir, candidate.PersistentPaths, strings.Join(stage, "\n"))
				}
			}
			want := []string{}
			for _, setting := range recipeProxyTrust(recipe, candidate.Framework) {
				want = append(want, setting.name)
			}
			sort.Strings(want)
			if got := imageProxyTrust(prepared); !slices.Equal(got, want) {
				t.Fatalf("%s (%s) image trust = %v, the runtime withdraws %v:\n%s", recipe, candidate.Framework, got, want, strings.Join(stage, "\n"))
			}
		})
	}
}

// A repository keeps one .tool-versions (or mise.toml) for all its
// languages. Each recipe takes its own tool's line and none of the others',
// and the JVM languages read the JDK the Java recipe reads.
func TestSharedVersionFilesGiveEachRecipeItsOwnTool(t *testing.T) {
	t.Parallel()
	fixtures := recipeFixtures()
	shared := map[string]string{
		".tool-versions": "nodejs 20.18.1\npython 3.12.7\njava temurin-17.0.13+11\ndotnet 9.0.100\nruby 3.3.6\nelixir 1.17.3-otp-27\nerlang 27.1.2\ngolang 1.25.4\n",
		"mise.toml":      "[tools]\nnode = \"24\"\npython = \"3.11\"\njava = \"25\"\n",
	}
	for _, fixture := range []struct {
		recipe, file, image string
	}{
		{"node", ".tool-versions", "node:20-alpine"},
		{"python", ".tool-versions", "python:3.12-slim-trixie"},
		{"java", ".tool-versions", "maven:3-eclipse-temurin-17"},
		{"clojure", ".tool-versions", "clojure:temurin-17-lein"},
		{"dotnet", ".tool-versions", "mcr.microsoft.com/dotnet/sdk:9.0.100"},
		{"ruby", ".tool-versions", "ruby:3.3.6-slim"},
		{"elixir", ".tool-versions", "elixir:1.17.3-otp-27-slim"},
		{"python", "mise.toml", "python:3.11-slim-trixie"},
		{"java", "mise.toml", "maven:3-eclipse-temurin-25"},
		{"clojure", "mise.toml", "clojure:temurin-25-lein"},
	} {
		t.Run(fixture.recipe+" "+fixture.file, func(t *testing.T) {
			t.Parallel()
			files := map[string]string{fixture.file: shared[fixture.file]}
			for name, content := range fixtures[fixture.recipe] {
				files[name] = content
			}
			_, prepared := preparedRecipeFixture(t, fixture.recipe, files)
			if !strings.Contains(prepared.DockerfilePreview, "FROM "+fixture.image+"@sha256:") {
				t.Fatalf("%s did not build on %s from %s:\n%s", fixture.recipe, fixture.image, fixture.file, prepared.DockerfilePreview)
			}
		})
	}
}

// The release settings a recipe accepts are the ones the Build settings
// offer, and the package manager choice applies to exactly the recipes whose
// build installs a package through the JavaScript install planner.
func TestRecipeSettingsMatchTheConfigureForm(t *testing.T) {
	t.Parallel()
	read := func(name string) string {
		content, err := os.ReadFile("../../../frontend/src/components/deploy/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return string(content)
	}
	defaults, settings := read("deployment-defaults.ts"), read("settings/build.tsx")
	list := func(source, name string) []string {
		match := regexp.MustCompile(`const ` + name + ` = \[([^\]]*)\]`).FindStringSubmatch(source)
		if match == nil {
			t.Fatalf("the form no longer lists %s", name)
		}
		values := []string{}
		for _, value := range strings.Split(match[1], ",") {
			if value = strings.Trim(strings.TrimSpace(value), `"`); value != "" {
				values = append(values, value)
			}
		}
		return values
	}
	java := []string{}
	for _, release := range javaRecipeReleases {
		java = append(java, strconv.Itoa(release))
	}
	node := []string{}
	for _, major := range nodeMajors {
		node = append(node, strconv.Itoa(major))
	}
	plan := func(build BuildPlanConfig) PlanConfiguration {
		build.Method, build.Secrets, build.ReleaseTasks = BuildRecipe, []BuildSecretConfig{}, []ReleaseTaskConfig{}
		return PlanConfiguration{Build: build,
			Runtime: RuntimePlanConfig{Strategy: StrategyStopFirst, Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{}},
			Domains: []PlannedDomain{}, Checks: []PlannedCheck{}, Variables: []PlannedVariable{}, Dependencies: []PlannedDependency{}}
	}
	for _, catalogue := range []struct {
		name    string
		form    []string
		recipe  []string
		setting func(string) BuildPlanConfig
	}{
		{"PYTHON_VERSIONS", list(settings, "PYTHON_VERSIONS"), pythonRecipeVersions, func(v string) BuildPlanConfig { return BuildPlanConfig{Recipe: "python", PythonVersion: v} }},
		{"JAVA_VERSIONS", list(defaults, "JAVA_VERSIONS"), java, func(v string) BuildPlanConfig { return BuildPlanConfig{Recipe: "java", JavaVersion: v} }},
		{"DOTNET_VERSIONS", list(defaults, "DOTNET_VERSIONS"), dotnetRecipeVersions, func(v string) BuildPlanConfig { return BuildPlanConfig{Recipe: "dotnet", DotnetVersion: v} }},
		{"NODE_VERSIONS", list(settings, "NODE_VERSIONS"), node, func(v string) BuildPlanConfig { return BuildPlanConfig{Recipe: "node", NodeVersion: v} }},
		{"PHP_VERSIONS", list(settings, "PHP_VERSIONS"), phpRecipeVersions, func(v string) BuildPlanConfig { return BuildPlanConfig{Recipe: "php", PHPVersion: v} }},
	} {
		if !slices.Equal(catalogue.form, catalogue.recipe) {
			t.Errorf("%s: the form offers %v, the recipe builds %v", catalogue.name, catalogue.form, catalogue.recipe)
		}
		for _, value := range catalogue.form {
			if err := plan(catalogue.setting(value)).Validate(); err != nil {
				t.Errorf("%s: the plan refuses %s the form offers: %v", catalogue.name, value, err)
			}
		}
	}

	match := regexp.MustCompile(`function installsAssetsWithNode\([^)]*\) \{\s*return ([^\n]+)`).FindStringSubmatch(defaults)
	if match == nil {
		t.Fatal("the form no longer says which recipes install assets with Node")
	}
	form := []string{"node"}
	for _, recipe := range regexp.MustCompile(`recipe === "(\w+)"`).FindAllStringSubmatch(match[1], -1) {
		form = append(form, recipe[1])
	}
	sort.Strings(form)
	recipes := slices.Sorted(slices.Values(nodeInstallRecipes))
	if !slices.Equal(form, recipes) {
		t.Fatalf("the form keeps a package manager for %v, the plan accepts one for %v", form, recipes)
	}
	// The Node version is kept and accepted wherever the package manager is:
	// both choose the same install.
	for _, recipe := range []string{"node", "go", "python", "rust", "java", "dotnet", "deno", "php", "site", "ruby", "elixir", "scala", "clojure", "dart", "gleam"} {
		err := plan(BuildPlanConfig{Recipe: recipe, PackageManager: "pnpm"}).Validate()
		refused := err != nil && strings.Contains(err.Error(), "package manager")
		if refused == slices.Contains(nodeInstallRecipes, recipe) {
			t.Errorf("%s with a package manager: %v", recipe, err)
		}
		err = plan(BuildPlanConfig{Recipe: recipe, NodeVersion: "22"}).Validate()
		refused = err != nil && strings.Contains(err.Error(), "Node version")
		if refused == slices.Contains(nodeInstallRecipes, recipe) {
			t.Errorf("%s with a Node version: %v", recipe, err)
		}
	}
	for _, kept := range []string{"nodeVersion", "packageManager"} {
		if !regexp.MustCompile(kept + `:\s*next === "node" \|\| installsAssetsWithNode\(next\)`).MatchString(settings) {
			t.Errorf("Build settings no longer keep %s for the recipes that install with Node", kept)
		}
	}
}

// Django (DEBUG off, no LOGGING) and Rails up to 7.0 write request errors
// where the dashboard does not look. In one repository holding both, each
// root gets its own framework's remedy, and a silent error status is named
// as Django's hidden errors only for Django.
func TestApplicationErrorsReachTheLogForDjangoAndRailsSideBySide(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	copyFrameworkFixture(t, "django", root+"/web")
	for name, content := range railsFiles(map[string]string{
		"config/environments/production.rb": "Rails.application.configure do\n  if ENV[\"RAILS_LOG_TO_STDOUT\"].present?\n    config.logger = Logger.new(STDOUT)\n  end\nend\n",
	}) {
		writeBuildFixture(t, root, "shop/"+name, content)
	}
	result, err := (Detector{}).DetectPath(t.Context(), root, SourceIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	django, rails := candidateAtRoot(result, "web", BuildRecipe), candidateAtRoot(result, "shop", BuildRecipe)
	if django == nil || django.Framework != "django" || rails == nil || rails.Framework != "rails" {
		t.Fatalf("candidates = %#v", result.Candidates)
	}
	if !strings.Contains(django.StartCommand, "--access-logfile -") ||
		findingByCode(plannedRecipeFindings(django, BuildPlanConfig{Method: BuildRecipe, Recipe: "python", StartCommand: django.StartCommand}), "django_errors_unlogged") == nil {
		t.Fatalf("Django's errors are not brought to the log: %q", django.StartCommand)
	}
	stdout := func(candidate *DetectedCandidate) string {
		for _, variable := range candidate.Variables {
			if variable.Name == "RAILS_LOG_TO_STDOUT" {
				return variable.Setup + "=" + variable.DefaultValue
			}
		}
		return ""
	}
	if stdout(rails) != "default=1" || stdout(django) == "default=1" {
		t.Fatalf("RAILS_LOG_TO_STDOUT: rails %q, django %q", stdout(rails), stdout(django))
	}
	_, prepared := preparedRecipeFixture(t, "ruby", railsFiles(nil))
	if !strings.Contains(prepared.DockerfilePreview, "RAILS_LOG_TO_STDOUT=1") {
		t.Fatalf("the Ruby recipe's Rails image logs to a file:\n%s", prepared.DockerfilePreview)
	}
	quiet := []ContainerDiagnostics{{State: "running", Lines: []RuntimeLogLine{{Text: "* Listening on http://0.0.0.0:3000"}}}}
	failed := []CheckEvidence{{Name: "readiness", Attempts: []CheckAttemptEvidence{{StatusCode: 500}}}}
	for framework, hidden := range map[string]bool{"django": true, "rails": false} {
		build := BuildPlanConfig{Method: BuildRecipe, Recipe: map[string]string{"django": "python", "rails": "ruby"}[framework], Framework: framework}
		cause := applicationOutputCause(quiet, runtimeCauseContext{build: build, checks: failed})
		if (cause != nil && cause.Code == "runtime_errors_hidden") != hidden {
			t.Errorf("%s silent 500 = %+v", framework, cause)
		}
	}
}
