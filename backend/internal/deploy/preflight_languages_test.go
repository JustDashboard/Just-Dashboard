package deploy

import (
	"strings"
	"testing"
)

func TestRubyLockPlatformsAreCheckedBeforeDeploy(t *testing.T) {
	mac := &DetectedToolchain{Language: "ruby", LockPlatforms: []string{"arm64-darwin-24"}, Bundler: "2.6.9"}
	recipe := &DetectedCandidate{BuildMethod: BuildRecipe, Recipe: "ruby", Toolchain: mac}
	amd64 := HostObservation{Architecture: "amd64"}
	findings := languageRecipeFindings(recipe, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe, Recipe: "ruby"}}, amd64)
	added := findingByCode(findings, "ruby_lock_platform_added")
	if added == nil || added.Severity != PreflightWarning || !strings.Contains(added.Means, "x86_64-linux") || !strings.Contains(added.Measured, "arm64-darwin-24") {
		t.Fatalf("findings = %#v", findings)
	}
	// A plan built for the other architecture is judged by it, not the host.
	arm := languageRecipeFindings(recipe, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe, TargetPlatform: "linux/arm64"}}, amd64)
	if added := findingByCode(arm, "ruby_lock_platform_added"); added == nil || !strings.Contains(added.Means, "aarch64-linux") {
		t.Fatalf("arm64 findings = %#v", arm)
	}
	linux := &DetectedCandidate{BuildMethod: BuildRecipe, Recipe: "ruby", Toolchain: &DetectedToolchain{Language: "ruby", LockPlatforms: []string{"arm64-darwin-24", "x86_64-linux"}}}
	if findings := languageRecipeFindings(linux, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe}}, amd64); len(findings) != 0 {
		t.Fatalf("a lock with the host's platform = %#v", findings)
	}
	frozen := &DetectedCandidate{BuildMethod: BuildDockerfile, Toolchain: &DetectedToolchain{Language: "ruby", LockPlatforms: mac.LockPlatforms, BundleFrozen: true}}
	if !hasFinding(languageRecipeFindings(frozen, PlanConfiguration{Build: BuildPlanConfig{Method: BuildDockerfile}}, amd64), "ruby_lock_platform_missing", PreflightBlocked) {
		t.Fatal("a frozen Dockerfile install was not blocked")
	}
	// A Dockerfile that adds the server's platform before its frozen
	// install builds: `docker build` of it succeeds.
	frozen.Toolchain.AddedPlatforms = []string{"x86_64-linux", "aarch64-linux"}
	if findings := languageRecipeFindings(frozen, PlanConfiguration{Build: BuildPlanConfig{Method: BuildDockerfile}}, amd64); len(findings) != 0 {
		t.Fatalf("a Dockerfile adding the platform = %#v", findings)
	}
	frozen.Toolchain.AddedPlatforms = []string{"aarch64-linux"}
	if !hasFinding(languageRecipeFindings(frozen, PlanConfiguration{Build: BuildPlanConfig{Method: BuildDockerfile}}, amd64), "ruby_lock_platform_missing", PreflightBlocked) {
		t.Fatal("a Dockerfile adding only the other architecture was not blocked")
	}
	frozen.Toolchain.AddedPlatforms = nil
	frozen.Toolchain.BundleFrozen = false
	if !hasFinding(languageRecipeFindings(frozen, PlanConfiguration{Build: BuildPlanConfig{Method: BuildDockerfile}}, amd64), "ruby_lock_platform_missing", PreflightWarning) {
		t.Fatal("an unfrozen Dockerfile install was not warned")
	}
	old := &DetectedCandidate{BuildMethod: BuildRecipe, Recipe: "ruby", Toolchain: &DetectedToolchain{Language: "ruby", LockPlatforms: []string{"ruby"}, Bundler: "1.17.3", GitSSH: true}}
	findings = languageRecipeFindings(old, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe}}, amd64)
	if !hasFinding(findings, "ruby_bundler_outdated", PreflightWarning) || !hasFinding(findings, "ruby_git_gem_ssh", PreflightWarning) {
		t.Fatalf("findings = %#v", findings)
	}
	if findings := languageRecipeFindings(&DetectedCandidate{Recipe: "elixir", Toolchain: &DetectedToolchain{Language: "elixir"}}, PlanConfiguration{}, amd64); len(findings) != 0 {
		t.Fatalf("elixir = %#v", findings)
	}
}

func TestBundleFrozenIsReadFromTheDockerfile(t *testing.T) {
	for dockerfile, frozen := range map[string]bool{
		"ENV RAILS_ENV=\"production\" \\\n    BUNDLE_DEPLOYMENT=\"1\"\n":      true,
		"RUN bundle config set --local deployment 'true' && bundle install\n": true,
		"RUN bundle install --frozen\n":                                       true,
		"ARG BUNDLE_FROZEN=true\n":                                            true,
		"RUN bundle install\n":                                                false,
		"ENV BUNDLE_DEPLOYMENT=0\n":                                           false,
	} {
		if bundleFrozenRE.MatchString(dockerfile) != frozen {
			t.Errorf("%q frozen = %v", dockerfile, !frozen)
		}
	}
}

func TestDockerfileAddedPlatformsAreRead(t *testing.T) {
	for dockerfile, want := range map[string]string{
		"ENV BUNDLE_DEPLOYMENT=\"1\"\nRUN bundle lock --add-platform x86_64-linux aarch64-linux && bundle install\n":   "x86_64-linux,aarch64-linux",
		"RUN bundle lock \\\n    --add-platform=x86_64-linux --add-platform aarch64-linux-gnu --normalize-platforms\n": "x86_64-linux,aarch64-linux-gnu",
		"RUN bundle lock --update && bundle install --add-platform nothing\n":                                          "",
		"RUN bundle install\n": "",
	} {
		if got := strings.Join(dockerfileAddedPlatforms([]byte(dockerfile)), ","); got != want {
			t.Errorf("%q = %q, want %q", dockerfile, got, want)
		}
	}
	result := detectShapeFixture(t, railsFiles(map[string]string{
		"Dockerfile": "FROM ruby:3.4-slim\nENV BUNDLE_DEPLOYMENT=\"1\"\nCOPY . .\nRUN bundle lock --add-platform x86_64-linux aarch64-linux && bundle install\nCMD [\"bin/rails\", \"server\"]\n",
	}))
	dockerfile := candidateAtRoot(result, "", BuildDockerfile)
	if dockerfile == nil || dockerfile.Toolchain == nil || strings.Join(dockerfile.Toolchain.AddedPlatforms, ",") != "x86_64-linux,aarch64-linux" {
		t.Fatalf("candidates = %#v", result.Candidates)
	}
	if findings := languageRecipeFindings(dockerfile, PlanConfiguration{Build: BuildPlanConfig{Method: BuildDockerfile}}, HostObservation{Architecture: "arm64"}); len(findings) != 0 {
		t.Fatalf("findings = %#v", findings)
	}
}

func TestUnpinnedDependenciesNameTheRecipesLockfile(t *testing.T) {
	for recipe, lockfile := range map[string]string{
		"ruby": "Gemfile.lock", "elixir": "mix.lock", "dart": "pubspec.lock", "gleam": "manifest.toml", "python": "uv lock",
	} {
		if _, _, action := unpinnedDependencyAdvice(&DetectedCandidate{Recipe: recipe}); !strings.Contains(action, lockfile) {
			t.Errorf("%s: %q", recipe, action)
		}
	}
}

func TestLanguageStartCommandsAreReadForTheirPort(t *testing.T) {
	for command, want := range map[string]commandListen{
		"bundle exec rails db:prepare && exec bundle exec rails server --binding 0.0.0.0 --port ${PORT:-3000}": {followsPort: true, fallback: 3000, host: "0.0.0.0"},
		"bundle exec puma --port 5000":                                              {port: 5000},
		"bundle exec rackup --host 0.0.0.0 -p 9393":                                 {port: 9393, host: "0.0.0.0"},
		"exec /app/bin/shop-web -Dhttp.port=${PORT:-9000} -Dpidfile.path=/dev/null": {followsPort: true, fallback: 9000},
	} {
		got := parseCommandListen(command, nil)
		if got.port != want.port || got.followsPort != want.followsPort || got.fallback != want.fallback || got.host != want.host {
			t.Errorf("%q = %+v", command, got)
		}
	}
}

func TestLanguageBuildFailuresAreNamed(t *testing.T) {
	for _, test := range []struct {
		line, code, detail, subject string
	}{
		{"** (Mix) Your mix.lock is out of date and must be updated without the --check-locked flag", "build_lockfile_out_of_sync", "mix.lock", ""},
		{"Unable to satisfy `pubspec.yaml` using `pubspec.lock`.", "build_lockfile_out_of_sync", "pubspec.lock", ""},
		{"The current Dart SDK version is 3.10.4.   Because api requires SDK version ^3.12.0, version solving failed.", "build_runtime_version", "dart", "^3.12.0"},
		{"Missing encryption key to decrypt file with. Ask your team for your master key and write it to config/master.key or put it in the ENV['RAILS_MASTER_KEY'].", "build_env_missing", "rails", "RAILS_MASTER_KEY"},
		{"An error occurred while installing pg (1.5.9), and Bundler cannot continue.", "build_install_script_failed", "ruby", "pg"},
		{"Can't find the 'libpq-fe.h header", "build_system_library_missing", "ruby", ""},
		{`Bundler could not find compatible versions for gem "rack":`, "build_dependency_conflict", "ruby", "rack"},
		{`Failed to use "plug" (version 1.16.1) because`, "build_dependency_conflict", "elixir", "plug"},
		{"** (Mix) No package with name phoenixx (from: mix.exs) in registry", "build_dependency_unavailable", "elixir", "phoenixx"},
		{"== Compilation error in file lib/shop_web/router.ex ==", "build_compile_error", "elixir", "lib/shop_web/router.ex"},
		{"[error] -- [E006] Not Found Error: /src/app/controllers/HomeController.scala:12:4 ", "build_compile_error", "scala", "/src/app/controllers/HomeController.scala"},
		{"Syntax error compiling at (shop/core.clj:9:3).", "build_compile_error", "clojure", "shop/core.clj"},
		{"bin/server.dart:4:3: Error: Undefined name 'serve'.", "build_compile_error", "dart", "bin/server.dart"},
		{"    ┌─ /app/src/api.gleam:12:5", "build_compile_error", "gleam", "/app/src/api.gleam"},
		{"[error] sbt.librarymanagement.ResolveException: Error downloading org.acme:missing_3:1.0.0", "build_dependency_unavailable", "scala", "org.acme:missing_3:1.0.0"},
	} {
		match := classifyLines(buildSignatures, []collectedLine{{text: test.line, seq: 1, vertex: 10}}, 1)
		if match == nil || match.code != test.code || match.detail != test.detail ||
			(test.subject != "" && (len(match.subjects) == 0 || match.subjects[0] != test.subject)) {
			t.Errorf("%q = %+v, want %s/%s %q", test.line, match, test.code, test.detail, test.subject)
		}
	}
}

func TestLanguageServersNameTheirListeningPort(t *testing.T) {
	for line, port := range map[string]string{
		"01:44:55.453 [info] Running ShopWeb.Endpoint with Bandit 1.12.5 at :::4000 (http)": "4000",
		"[info] Listening for HTTP on /[0:0:0:0:0:0:0:0]:9000":                              "9000",
		"INFO: Started ServerConnector@4c3e4790{HTTP/1.1, (http/1.1)}{0.0.0.0:3000}":        "3000",
		"* Listening on http://0.0.0.0:3000":                                                "3000",
	} {
		match := listeningPortRE.FindStringSubmatch(line)
		if printed := addFirstGroup(nil, match); len(printed) != 1 || printed[0] != port {
			t.Errorf("%q = %v", line, printed)
		}
	}
}

func TestLanguageRecipesSetUpTheirFrameworksVariables(t *testing.T) {
	legacy := selectedOf(detectShapeFixture(t, railsFiles(map[string]string{
		"config/environments/production.rb": "Rails.application.configure do\n  if ENV[\"RAILS_LOG_TO_STDOUT\"].present?\n    config.logger = Logger.new(STDOUT)\n  end\nend\n",
	})))
	sinatra := selectedOf(detectShapeFixture(t, map[string]string{
		"Gemfile": "gem 'sinatra'\ngem 'puma'\n", "config.ru": "run App\n",
		"app.rb": "require 'sinatra/base'\nclass App < Sinatra::Base\n  set :session_secret, ENV.fetch(\"SESSION_SECRET\")\nend\n",
	}))
	for _, check := range []struct {
		candidate *DetectedCandidate
		name      string
		setup     string
	}{
		{legacy, "RAILS_LOG_TO_STDOUT", "default"}, {sinatra, "SESSION_SECRET", "generate"},
	} {
		found := false
		for _, variable := range check.candidate.Variables {
			found = found || variable.Name == check.name && variable.Setup == check.setup
		}
		if !found {
			t.Errorf("%s: variables = %#v", check.name, check.candidate.Variables)
		}
	}
	for _, variable := range selectedOf(detectShapeFixture(t, railsFiles(nil))).Variables {
		if variable.Name == "RAILS_LOG_TO_STDOUT" {
			t.Fatal("Rails 7.1 and later log to STDOUT without being asked")
		}
	}
}

func TestLanguageRecipeStepsHaveTheirPhase(t *testing.T) {
	context := causeContext{build: BuildPlanConfig{Method: BuildRecipe}}
	for command, phase := range map[string]string{
		"bundle lock --add-platform x86_64-linux": phaseInstall, "BUNDLE_DEPLOYMENT=1 bundle install && rm -rf ~/.bundle": phaseInstall,
		"SECRET_KEY_BASE_DUMMY=1 bundle exec rails assets:precompile":                                                  phaseBuild,
		`SECRET_KEY_BASE="$(ruby -rsecurerandom -e 'print SecureRandom.hex(64)')" bundle exec rails assets:precompile`: phaseBuild,
		"mix deps.get --only prod --check-locked":                                                                      phaseInstall, "mix deps.compile": phaseInstall, "mix release --overwrite": phaseBuild,
		"dart pub get --enforce-lockfile": phaseInstall, "dart compile exe bin/server.dart -o /out/server": phaseBuild,
		"gleam deps download": phaseInstall, "gleam export erlang-shipment": phaseBuild,
		"sbt -batch update": phaseInstall, "sbt -batch stage": phaseBuild, "lein deps": phaseInstall, "lein uberjar": phaseBuild,
		"clojure -P && clojure -P -T:build": phaseInstall, "clojure -T:build uber": phaseBuild,
	} {
		if got := buildPhase(context, command, "RUN "+command); got != phase {
			t.Errorf("%q = %s, want %s", command, got, phase)
		}
	}
}

func TestToolchainFactsAreBoundedBeforeTheyAreSaved(t *testing.T) {
	odd := &DetectedToolchain{Language: "ruby", Release: "3.4.7", Bundler: "2.6.9 (see notes)", LockPlatforms: []string{"x86_64-linux", "bad platform"},
		AddedPlatforms: []string{"aarch64-linux", "$(TARGET)\n"}}
	if validateDetectedToolchain(odd) == nil {
		t.Fatal("prose in a toolchain fact was accepted")
	}
	bounded := odd.bounded()
	if validateDetectedToolchain(bounded) != nil || bounded.Bundler != "" || strings.Join(bounded.LockPlatforms, ",") != "x86_64-linux" || bounded.Release != "3.4.7" ||
		strings.Join(bounded.AddedPlatforms, ",") != "aarch64-linux" {
		t.Fatalf("bounded = %#v", bounded)
	}
	if validateDetectedToolchain(&DetectedToolchain{Language: "cobol"}) == nil {
		t.Fatal("a toolchain for no recipe was accepted")
	}
}
