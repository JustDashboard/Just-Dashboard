package deploy

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

const railsBinRails = "#!/usr/bin/env ruby\nAPP_PATH = File.expand_path(\"../config/application\", __dir__)\nrequire_relative \"../config/boot\"\nrequire \"rails/commands\"\n"

// railsLock is a Rails 8 lock written on a Mac: its only platform is the
// laptop's, which a frozen install on Linux refuses.
const railsLock = `GEM
  remote: https://rubygems.org/
  specs:
    activerecord (8.0.2)
    nokogiri (1.18.8-arm64-darwin)
    pg (1.5.9)
    propshaft (1.1.0)
    puma (6.6.0)
    rails (8.0.2)
    railties (8.0.2)
    ruby-vips (2.2.3)
    solid_queue (1.1.5)

PLATFORMS
  arm64-darwin-24

DEPENDENCIES
  rails (~> 8.0.2)

RUBY VERSION
   ruby 3.4.4p34

BUNDLED WITH
   2.6.9
`

func railsFiles(extra map[string]string) map[string]string {
	files := map[string]string{
		"Gemfile":               "source \"https://rubygems.org\"\nruby file: \".ruby-version\"\ngem \"rails\", \"~> 8.0.2\"\ngem \"pg\"\ngem \"puma\"\ngem \"propshaft\"\n",
		"Gemfile.lock":          railsLock,
		".ruby-version":         "ruby-3.4.4\n",
		"Rakefile":              "require_relative \"config/application\"\nRails.application.load_tasks\n",
		"bin/rails":             railsBinRails,
		"config.ru":             "require_relative \"config/environment\"\nrun Rails.application\n",
		"config/application.rb": "require_relative \"boot\"\nrequire \"rails/all\"\nmodule Shop\n  class Application < Rails::Application\n  end\nend\n",
		"config/database.yml":   "production:\n  adapter: postgresql\n  url: <%= ENV[\"DATABASE_URL\"] %>\n",
		"config/routes.rb":      "Rails.application.routes.draw do\n  get \"up\" => \"rails/health#show\", as: :rails_health_check\nend\n",
	}
	for name, content := range extra {
		files[name] = content
	}
	return files
}

func prepareFixture(t *testing.T, files map[string]string, config BuildPlanConfig) (PreparedBuild, error) {
	t.Helper()
	root := t.TempDir()
	for path, content := range files {
		writeBuildFixture(t, root, path, content)
	}
	return NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), root, config, false, "just-dashboard/test:run-1")
}

func assertRendered(t *testing.T, prepared PreparedBuild, want, absent []string) {
	t.Helper()
	for _, line := range want {
		if !strings.Contains(prepared.DockerfilePreview, line) {
			t.Fatalf("Dockerfile missing %q:\n%s", line, prepared.DockerfilePreview)
		}
	}
	for _, line := range absent {
		if strings.Contains(prepared.DockerfilePreview, line) {
			t.Fatalf("Dockerfile carries %q:\n%s", line, prepared.DockerfilePreview)
		}
	}
	for _, base := range prepared.BaseImages {
		if !strings.Contains(prepared.DockerfilePreview, base.Reference+"@"+base.Digest) {
			t.Fatalf("base %s is not used digest-pinned:\n%s", base.Reference, prepared.DockerfilePreview)
		}
	}
}

func TestLanguageVersionConstraints(t *testing.T) {
	for _, test := range []struct {
		constraint, version string
		want                bool
	}{
		{"~> 3.3", "3.4.7", true}, {"~> 3.3", "4.0.0", false}, {"~> 3.3.0", "3.4.0", false}, {"~> 3.3.0", "3.3.9", true},
		{">= 3.1, < 3.4", "3.3.6", true}, {">= 3.1, < 3.4", "3.4.0", false}, {"3.4.7", "3.4.7", true}, {"3.4.7", "3.4.8", false},
		{"^3.5.0", "3.13.0", true}, {"^3.5.0", "4.0.0", false}, {">=3.0.0 <3.10.0", "3.9.2", true}, {">=3.0.0 <3.10.0", "3.10.0", false},
		{">= 1.4.0 and < 2.0.0", "1.18.1", true}, {"~> 1.15", "1.19.6", true}, {"~> 1.15 or ~> 2.0", "2.1.0", true}, {"any", "9.9.9", true},
	} {
		constraint, ok := parseVersionConstraint(test.constraint)
		version, _, _ := parseLanguageVersion(test.version)
		if !ok || constraint.allows(version) != test.want {
			t.Errorf("%q allows %s = %v, want %v", test.constraint, test.version, constraint.allows(version), test.want)
		}
	}
	if constraint, _ := parseVersionConstraint("~> 3.3.0"); constraint.allowsFamily("3.4") || !constraint.allowsFamily("3.3") {
		t.Fatal("allowsFamily")
	}
}

func TestRubyVersionIsReadFromTheProjectsOwnFiles(t *testing.T) {
	for _, test := range []struct {
		name                           string
		versionFile, toolVersions, gem string
		lock                           string
		want, source, failure          string
	}{
		{name: "an exact .ruby-version", versionFile: "ruby-3.3.6\n", want: "3.3.6", source: ".ruby-version"},
		{name: "a family", versionFile: "3.4\n", want: "3.4", source: ".ruby-version"},
		{name: ".tool-versions", toolVersions: "nodejs 22.1.0\nruby 4.0.1\n", want: "4.0.1", source: ".tool-versions"},
		{name: "the Gemfile's exact ruby", gem: "ruby \"3.3.9\"\n", want: "3.3.9", source: "Gemfile"},
		{name: "a Gemfile range takes the reviewed family", gem: "ruby \">= 3.1\"\n", want: "3.4", source: "Gemfile"},
		{name: "a Gemfile range below the default", gem: "ruby \"~> 3.3.0\"\n", want: "3.3", source: "Gemfile"},
		{name: "the lock's ruby as a family", lock: "RUBY VERSION\n   ruby 3.3.6p108\n", want: "3.3", source: "Gemfile.lock"},
		{name: "nothing declared", want: "3.4"},
		{name: "a release the catalogue lacks", versionFile: "3.2.2\n", failure: "names 3.2.2"},
		{name: "JRuby", versionFile: "jruby-9.4.5.0\n", failure: "names jruby-9.4.5.0"},
		{name: "an engine in the Gemfile", gem: "ruby \"3.1.4\", engine: \"jruby\", engine_version: \"9.4.5.0\"\n", failure: "asks for jruby"},
		{name: "a version file the Gemfile refuses", versionFile: "3.3.6\n", gem: "ruby \"~> 3.4\"\n", failure: "make them agree"},
	} {
		t.Run(test.name, func(t *testing.T) {
			choice, err := chooseRubyRecipeVersion([]byte(test.versionFile), []byte(test.toolVersions), []byte(test.gem), parseGemfileLock([]byte(test.lock)))
			if test.failure != "" {
				if err == nil || !errors.Is(err, ErrUnsupportedBuilder) || !strings.Contains(err.Error(), test.failure) {
					t.Fatalf("err = %v, want %q", err, test.failure)
				}
				return
			}
			if err != nil || choice.release() != test.want || choice.source != test.source {
				t.Fatalf("choice = %+v, %v", choice, err)
			}
		})
	}
}

func TestGemfileLockIsReadAsData(t *testing.T) {
	lock := parseGemfileLock([]byte(railsLock + "GIT\n  remote: git@github.com:acme/private-gem.git\n  revision: abc\n  specs:\n    private-gem (0.1.0)\n"))
	withHTTPS := parseGemfileLock([]byte("GIT\n  remote: https://github.com/acme/public-gem.git\n  specs:\n    public-gem (0.2.0)\n"))
	if strings.Join(withHTTPS.gitHosts, ",") != "github.com" || withHTTPS.gitSSH {
		t.Fatalf("https git source = %+v", withHTTPS)
	}
	if variables := (rubyProject{lock: withHTTPS}).sourceVariables("web"); len(variables) != 1 || variables[0].Name != "BUNDLE_GITHUB__COM" ||
		variables[0].InstallRequired || variables[0].Sources[0] != "web/Gemfile.lock" {
		t.Fatalf("variables = %#v", variables)
	}
	if lock.specs["nokogiri"] != "1.18.8" || lock.specs["rails"] != "8.0.2" || strings.Join(lock.platforms, ",") != "arm64-darwin-24" ||
		lock.bundledWith != "2.6.9" || lock.ruby != "3.4.4" || !lock.gitSSH || len(lock.gitGems) != 1 {
		t.Fatalf("lock = %+v", lock)
	}
	for _, test := range []struct {
		platforms []string
		arch      string
		add       string
	}{
		{[]string{"arm64-darwin-24"}, "amd64", "x86_64-linux"},
		{[]string{"arm64-darwin-24", "x86_64-linux"}, "arm64", "aarch64-linux"},
		{[]string{"x86_64-linux-gnu"}, "amd64", ""},
		{[]string{"ruby"}, "arm64", ""},
		{[]string{"x86_64-linux-musl"}, "amd64", "x86_64-linux"},
	} {
		if add, ok := rubyLockPlatform(test.platforms, test.arch); add != test.add || ok != (test.add == "") {
			t.Errorf("%v on %s: add %q, ok %v", test.platforms, test.arch, add, ok)
		}
	}
}

func TestRubyApplicationsAreRecipeCandidates(t *testing.T) {
	t.Run("rails", func(t *testing.T) {
		result := detectShapeFixture(t, railsFiles(map[string]string{"Procfile": "worker: bundle exec rails solid_queue:start\n"}), SourceIdentity{Kind: SourceLocal})
		candidate := selectedOf(result)
		if candidate == nil || candidate.Recipe != "ruby" || candidate.Framework != "rails" || candidate.Port != 3000 || candidate.Confidence != ConfidenceHigh ||
			candidate.StartCommand != "bundle exec rails db:prepare && exec bundle exec rails server --binding 0.0.0.0 --port ${PORT:-3000}" {
			t.Fatalf("candidate = %#v", candidate)
		}
		if candidate.Toolchain == nil || candidate.Toolchain.Release != "3.4.4" || candidate.Toolchain.From != ".ruby-version" ||
			strings.Join(candidate.Toolchain.LockPlatforms, ",") != "arm64-darwin-24" || candidate.Toolchain.Bundler != "2.6.9" {
			t.Fatalf("toolchain = %#v", candidate.Toolchain)
		}
		if candidate.Readiness == nil || candidate.Readiness.Path != "/up" || candidate.Readiness.AcceptAnyAnswer {
			t.Fatalf("readiness = %#v", candidate.Readiness)
		}
		if candidate.Listen == nil || !candidate.Listen.ReadsPort {
			t.Fatalf("listen = %#v", candidate.Listen)
		}
		generated := false
		for _, variable := range candidate.Variables {
			generated = generated || variable.Name == "SECRET_KEY_BASE" && variable.Setup == "generate"
		}
		if !generated || len(candidate.Databases) == 0 || candidate.Databases[0].Engine != "postgres" {
			t.Fatalf("variables = %#v, databases = %#v", candidate.Variables, candidate.Databases)
		}
		if err := validateDetectionResult(&DraftSourceConfig{Kind: SourceLocal}, withSelection(result)); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("rails with jsbundling installs its package in the Ruby build", func(t *testing.T) {
		result := detectShapeFixture(t, railsFiles(map[string]string{
			"Gemfile.lock": strings.Replace(railsLock, "    propshaft (1.1.0)\n", "    jsbundling-rails (1.3.1)\n    propshaft (1.1.0)\n", 1),
			"package.json": `{"name":"shop","private":true,"scripts":{"build":"esbuild app/javascript/*.* --bundle --outdir=app/assets/builds"},"devDependencies":{"esbuild":"0.25.0"}}`,
			"yarn.lock":    "# yarn lockfile v1\n\n\nesbuild@0.25.0:\n  version \"0.25.0\"\n",
		}))
		if len(result.Candidates) != 1 || result.Candidates[0].Recipe != "ruby" || result.Candidates[0].PackageManager != "yarn" ||
			len(result.Candidates[0].NodeInstalls) == 0 || setAsideKind(result, "asset-pipeline") == nil {
			t.Fatalf("candidates = %#v", result.Candidates)
		}
	})
	t.Run("rails without bin/rails asks for it", func(t *testing.T) {
		files := railsFiles(nil)
		delete(files, "bin/rails")
		candidate := selectedOf(detectShapeFixture(t, files))
		if candidate == nil || candidate.StartCommand != "" || len(candidate.NeedsDecision) != 1 || !strings.Contains(candidate.NeedsDecision[0], "bin/rails") {
			t.Fatalf("candidate = %#v", candidate)
		}
	})
	t.Run("rails beside its Dockerfile", func(t *testing.T) {
		result := detectShapeFixture(t, railsFiles(map[string]string{
			"Dockerfile": "FROM ruby:3.4-slim\nWORKDIR /rails\nENV RAILS_ENV=\"production\" \\\n    BUNDLE_DEPLOYMENT=\"1\"\nCOPY . .\nRUN bundle install\nEXPOSE 3000\nCMD [\"bundle\", \"exec\", \"rails\", \"server\"]\n",
		}))
		dockerfile := candidateAtRoot(result, "", BuildDockerfile)
		recipe := candidateAtRoot(result, "", BuildRecipe)
		if dockerfile == nil || recipe == nil || selectedOf(result) == nil || selectedOf(result).ID != dockerfile.ID {
			t.Fatalf("candidates = %#v", result.Candidates)
		}
		if dockerfile.Toolchain == nil || !dockerfile.Toolchain.BundleFrozen || strings.Join(dockerfile.Toolchain.LockPlatforms, ",") != "arm64-darwin-24" {
			t.Fatalf("dockerfile toolchain = %#v", dockerfile.Toolchain)
		}
	})
	t.Run("sinatra behind puma", func(t *testing.T) {
		candidate := selectedOf(detectShapeFixture(t, map[string]string{
			"Gemfile": "gem 'sinatra'\ngem 'puma'\n", "Gemfile.lock": "GEM\n  specs:\n    puma (6.6.0)\n    rack (3.1.8)\n    sinatra (4.1.1)\n\nPLATFORMS\n  ruby\n  x86_64-linux\n",
			"config.ru": "require_relative 'app'\nrun App\n", "app.rb": "require 'sinatra/base'\nclass App < Sinatra::Base; end\n",
		}))
		if candidate == nil || candidate.Recipe != "ruby" || candidate.Framework != "sinatra" || candidate.StartCommand != "bundle exec puma --port ${PORT:-4567}" ||
			candidate.Readiness == nil || !candidate.Readiness.AcceptAnyAnswer || candidate.UnpinnedDependencies {
			t.Fatalf("candidate = %#v", candidate)
		}
	})
	t.Run("a classic sinatra script", func(t *testing.T) {
		candidate := selectedOf(detectShapeFixture(t, map[string]string{
			"Gemfile": "gem 'sinatra'\ngem 'rackup'\ngem 'puma'\n", "app.rb": "require 'sinatra'\nget('/') { 'hi' }\n",
		}))
		if candidate == nil || candidate.StartCommand != "bundle exec ruby app.rb -o 0.0.0.0 -p ${PORT:-4567}" || !candidate.UnpinnedDependencies {
			t.Fatalf("candidate = %#v", candidate)
		}
	})
	t.Run("rack without a server", func(t *testing.T) {
		candidate := selectedOf(detectShapeFixture(t, map[string]string{
			"Gemfile": "gem 'roda'\n", "Gemfile.lock": "GEM\n  specs:\n    rack (3.1.8)\n    roda (3.88.0)\n\nPLATFORMS\n  ruby\n",
			"config.ru": "require_relative 'app'\nrun App.freeze.app\n",
		}))
		if candidate == nil || candidate.StartCommand != "" || candidate.Confidence != ConfidenceLow || !strings.Contains(strings.Join(candidate.NeedsDecision, " "), "no Rack server") {
			t.Fatalf("candidate = %#v", candidate)
		}
		findings := plannedRecipeFindings(candidate, BuildPlanConfig{Method: BuildRecipe, Recipe: "ruby"})
		if !hasFinding(findings, "start_command_missing", PreflightBlocked) || !strings.Contains(findingByCode(findings, "start_command_missing").Measured, "no Rack server") {
			t.Fatalf("findings = %#v", findings)
		}
	})
	t.Run("hanami migrates before puma", func(t *testing.T) {
		candidate := selectedOf(detectShapeFixture(t, map[string]string{
			"Gemfile": "gem 'hanami', '~> 2.2'\ngem 'hanami-db'\ngem 'puma'\n", "config.ru": "require 'hanami/boot'\nrun Hanami.app\n",
			"config/db/migrate/20240101000000_create_books.rb": "ROM::SQL.migration do\nend\n",
		}))
		if candidate == nil || candidate.Framework != "hanami" || candidate.Port != 2300 ||
			candidate.StartCommand != "bundle exec hanami db migrate && exec bundle exec puma --port ${PORT:-2300}" {
			t.Fatalf("candidate = %#v", candidate)
		}
	})
	t.Run("a private gem server's credential reaches the install", func(t *testing.T) {
		candidate := selectedOf(detectShapeFixture(t, map[string]string{
			"Gemfile":   "source 'https://rubygems.org'\nsource 'https://gems.contribsys.com/' do\n  gem 'sidekiq-pro'\nend\ngem 'sinatra'\ngem 'puma'\n",
			"config.ru": "run App\n",
		}))
		found := false
		for _, variable := range candidate.Variables {
			found = found || variable.Name == "BUNDLE_GEMS__CONTRIBSYS__COM" && variable.Step == "install" && variable.InstallRequired
		}
		if !found {
			t.Fatalf("variables = %#v", candidate.Variables)
		}
	})
}

func TestRubyRecipeRendersTheBundleAssetsAndRuntime(t *testing.T) {
	t.Run("rails on a Mac lock", func(t *testing.T) {
		prepared, err := prepareFixture(t, railsFiles(nil), BuildPlanConfig{Method: BuildRecipe, Recipe: "ruby", TargetPlatform: "linux/amd64",
			StartCommand: "bundle exec rails server --binding 0.0.0.0 --port ${PORT:-3000}"})
		if err != nil {
			t.Fatal(err)
		}
		assertRendered(t, prepared, []string{
			"FROM ruby:3.4.4-slim@sha256:", "AS build", "libpq-dev", "RUN bundle lock --add-platform x86_64-linux",
			"RUN BUNDLE_DEPLOYMENT=1 bundle install", "RUN SECRET_KEY_BASE_DUMMY=1 bundle exec rails assets:precompile",
			"RUN apt-get update -qq && apt-get install --no-install-recommends -y libpq5 libvips",
			"ENV RAILS_ENV=production RACK_ENV=production RAILS_LOG_TO_STDOUT=1 RAILS_SERVE_STATIC_FILES=1 BUNDLE_PATH=/usr/local/bundle BUNDLE_WITHOUT=development:test BUNDLE_DEPLOYMENT=1",
			"COPY --from=build /usr/local/bundle /usr/local/bundle",
		}, []string{"node-toolchain", "gem install bundler"})
		if prepared.Toolchain != "ruby 3.4.4" || len(prepared.Notes) == 0 {
			t.Fatalf("prepared = %+v", prepared)
		}
	})
	t.Run("rails 7.0 precompiles with a throwaway key, never a planned value", func(t *testing.T) {
		files := railsFiles(map[string]string{"Gemfile.lock": strings.NewReplacer("rails (8.0.2)", "rails (7.0.8)", "railties (8.0.2)", "railties (7.0.8)", "arm64-darwin-24", "x86_64-linux").Replace(railsLock)})
		prepared, err := prepareFixture(t, files, BuildPlanConfig{Method: BuildRecipe, Recipe: "ruby", TargetPlatform: "linux/amd64", StartCommand: "bundle exec puma"})
		if err != nil {
			t.Fatal(err)
		}
		assertRendered(t, prepared, []string{`SECRET_KEY_BASE="$(ruby -rsecurerandom -e 'print SecureRandom.hex(64)')" bundle exec rails assets:precompile`},
			[]string{"bundle lock --add-platform", "SECRET_KEY_BASE_DUMMY"})
	})
	t.Run("rails with jsbundling borrows Node and installs with the lockfile's manager", func(t *testing.T) {
		files := railsFiles(map[string]string{
			"package.json":   `{"name":"shop","private":true,"packageManager":"pnpm@9.15.0","scripts":{"build":"esbuild app/javascript/*.* --bundle --outdir=app/assets/builds"},"devDependencies":{"esbuild":"0.25.0"}}`,
			"pnpm-lock.yaml": "lockfileVersion: '9.0'\n\nimporters:\n\n  .:\n    devDependencies:\n      esbuild:\n        specifier: 0.25.0\n        version: 0.25.0\n",
		})
		prepared, err := prepareFixture(t, files, BuildPlanConfig{Method: BuildRecipe, Recipe: "ruby", TargetPlatform: "linux/amd64", StartCommand: "bundle exec puma"})
		if err != nil {
			t.Fatal(err)
		}
		assertRendered(t, prepared, []string{
			"FROM node:22-bookworm-slim@sha256:", "AS node-toolchain", "corepack install -g pnpm@9.15.0",
			"COPY --from=node-toolchain /usr/local/ /opt/node/", "COPY --from=node-toolchain /opt/ /opt/", "ENV PATH=/opt/node/bin:$PATH",
			"COREPACK_HOME=/opt/corepack", "pnpm install --frozen-lockfile", "RUN rm -rf node_modules tmp/cache tmp/pids",
		}, nil)
		if !strings.Contains(prepared.Toolchain, "assets: pnpm 9.15.0") || prepared.Install == "" {
			t.Fatalf("prepared = %+v", prepared)
		}
	})
	t.Run("sprockets compressing through ExecJS borrows Node alone", func(t *testing.T) {
		files := railsFiles(map[string]string{"Gemfile.lock": strings.NewReplacer("    propshaft (1.1.0)\n", "    execjs (2.9.1)\n    sprockets-rails (3.5.2)\n", "arm64-darwin-24", "x86_64-linux").Replace(railsLock)})
		prepared, err := prepareFixture(t, files, BuildPlanConfig{Method: BuildRecipe, Recipe: "ruby", TargetPlatform: "linux/amd64", StartCommand: "bundle exec puma"})
		if err != nil {
			t.Fatal(err)
		}
		assertRendered(t, prepared, []string{"AS node-toolchain", "COPY --from=node-toolchain /usr/local/ /opt/node/", "ENV PATH=/opt/node/bin:$PATH"},
			[]string{"npm ci", "yarn install", "corepack"})
		if prepared.Install != "" || prepared.Toolchain != "ruby 3.4.4" {
			t.Fatalf("prepared = %+v", prepared)
		}
	})
	t.Run("an unlocked sinatra app installs unfrozen", func(t *testing.T) {
		prepared, err := prepareFixture(t, map[string]string{"Gemfile": "gem 'sinatra'\ngem 'puma'\n", "config.ru": "run App\n"},
			BuildPlanConfig{Method: BuildRecipe, Recipe: "ruby", StartCommand: "bundle exec puma --port ${PORT:-4567}"})
		if err != nil {
			t.Fatal(err)
		}
		assertRendered(t, prepared, []string{"FROM ruby:3.4-slim@sha256:", "RUN bundle install", "ENV RACK_ENV=production APP_ENV=production", " git "},
			[]string{"BUNDLE_DEPLOYMENT", "assets:precompile"})
	})
	t.Run("the recipe refuses a plan without a start command", func(t *testing.T) {
		_, err := prepareFixture(t, map[string]string{"Gemfile": "gem 'roda'\n", "config.ru": "run App\n"}, BuildPlanConfig{Method: BuildRecipe, Recipe: "ruby"})
		if err == nil || !errors.Is(err, ErrUnsupportedBuilder) || !strings.Contains(err.Error(), "no Rack server") {
			t.Fatalf("err = %v", err)
		}
	})
}

// fallbackBackend resolves every reference but the ones it is told the
// registry lacks.
type fallbackBackend struct {
	artifactBackendFake
	missing map[string]bool
}

func (f *fallbackBackend) ResolveImage(ctx context.Context, reference, auth string) (ResolvedImage, error) {
	if f.missing[reference] {
		return ResolvedImage{}, errors.New("manifest unknown")
	}
	return f.artifactBackendFake.ResolveImage(ctx, reference, auth)
}

func TestExactPinsWithoutAnImageBuildOnTheirFamily(t *testing.T) {
	root := t.TempDir()
	for path, content := range map[string]string{"Gemfile": "gem 'sinatra'\ngem 'puma'\n", "config.ru": "run App\n", ".ruby-version": "3.4.0\n"} {
		writeBuildFixture(t, root, path, content)
	}
	backend := &fallbackBackend{missing: map[string]bool{"ruby:3.4.0-slim": true}}
	prepared, err := NewArtifactBuilder(backend).Prepare(context.Background(), root, BuildPlanConfig{Method: BuildRecipe, Recipe: "ruby", StartCommand: "bundle exec puma"}, false, "t:1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prepared.DockerfilePreview, "FROM ruby:3.4-slim@sha256:") || prepared.Toolchain != "ruby 3.4" ||
		len(prepared.Notes) != 1 || !strings.Contains(prepared.Notes[0], "no ruby:3.4.0-slim image") {
		t.Fatalf("prepared = %+v\n%s", prepared, prepared.DockerfilePreview)
	}
}

const phoenixMix = `defmodule Shop.MixProject do
  use Mix.Project

  def project do
    [
      app: :shop,
      version: "0.1.0",
      elixir: "~> 1.15",
      deps: deps(),
      aliases: aliases()
    ]
  end

  defp deps do
    [
      {:phoenix, "~> 1.7.14"},
      {:ecto_sql, "~> 3.10"},
      {:postgrex, ">= 0.0.0"},
      {:bandit, "~> 1.5"}
    ]
  end

  defp aliases do
    [
      "assets.setup": ["tailwind.install --if-missing", "esbuild.install --if-missing"],
      "assets.deploy": ["tailwind shop --minify", "esbuild shop --minify", "phx.digest"]
    ]
  end
end
`

func TestElixirVersionIsReadFromTheProjectsOwnFiles(t *testing.T) {
	for _, test := range []struct {
		name, tools, versionFile, buildpack, mix string
		want, failure                            string
	}{
		{name: ".tool-versions with the OTP suffix", tools: "elixir 1.17.3-otp-27\nerlang 27.1.2\n", want: "1.17.3-otp-27"},
		{name: "erlang names the OTP", tools: "elixir 1.18.4\nerlang 26.2.5\n", want: "1.18.4-otp-26"},
		{name: "the buildpack's config", buildpack: "elixir_version=1.18.1\nerlang_version=27.2\n", want: "1.18.1-otp-27"},
		{name: "mix.exs alone takes the reviewed family", mix: `elixir: "~> 1.15"`, want: "1.19-otp-28"},
		{name: "a floor above the default", mix: `elixir: "~> 1.20"`, want: "1.20-otp-28"},
		{name: "nothing declared", want: "1.19-otp-28"},
		{name: "a release the catalogue lacks", tools: "elixir 1.14.5-otp-25\n", failure: "names 1.14.5-otp-25"},
		{name: "an OTP the image is not published for", tools: "elixir 1.19.1\nerlang 25.3\n", failure: "OTP 25"},
		{name: "mix.exs refuses the declared release", tools: "elixir 1.17.3\n", mix: `elixir: "~> 1.18"`, failure: "make them agree"},
	} {
		t.Run(test.name, func(t *testing.T) {
			choice, err := chooseElixirRecipeVersion([]byte(test.tools), []byte(test.versionFile), []byte(test.buildpack), []byte(test.mix))
			if test.failure != "" {
				if err == nil || !strings.Contains(err.Error(), test.failure) {
					t.Fatalf("err = %v, want %q", err, test.failure)
				}
				return
			}
			if err != nil || choice.release() != test.want {
				t.Fatalf("choice = %+v, %v", choice, err)
			}
		})
	}
}

func TestMixReleaseNames(t *testing.T) {
	for _, test := range []struct {
		mix, want, failure string
	}{
		{mix: `[app: :shop]`, want: "shop"},
		{mix: "[app: :shop, releases: [shop_web: [include_executables_for: [:unix], applications: [shop: :permanent]], worker: []]]", want: "shop_web"},
		{mix: "[app: :shop, releases: releases()]\n  defp releases do\n    [\n      edge: [steps: [:assemble]]\n    ]\n  end", want: "edge"},
		{mix: "[app: :shop, default_release: :worker, releases: [web: [], worker: []]]", want: "worker"},
		{mix: `[apps_path: "apps"]`, failure: "umbrella"},
	} {
		app := ""
		if match := mixAppRE.FindStringSubmatch(test.mix); match != nil {
			app = match[1]
		}
		name, err := mixReleaseName([]byte(test.mix), app)
		if test.failure != "" {
			if err == nil || !strings.Contains(err.Error(), test.failure) {
				t.Errorf("%s: err = %v", test.mix, err)
			}
			continue
		}
		if err != nil || name != test.want {
			t.Errorf("%s: %q, %v", test.mix, name, err)
		}
	}
}

func TestElixirApplicationsAreRecipeCandidates(t *testing.T) {
	result := detectShapeFixture(t, map[string]string{
		"mix.exs": phoenixMix, "mix.lock": "%{}\n", ".tool-versions": "elixir 1.18.4-otp-27\nerlang 27.3\n",
		"lib/shop/release.ex":                            "defmodule Shop.Release do\n  @app :shop\n  def migrate do\n  end\nend\n",
		"priv/repo/migrations/20240101_create_users.exs": "defmodule Shop.Repo.Migrations.CreateUsers do\nend\n",
		"config/runtime.exs":                             "import Config\nif config_env() == :prod do\n  database_url = System.get_env(\"DATABASE_URL\") || raise \"environment variable DATABASE_URL is missing.\"\n  host = System.get_env(\"PHX_HOST\") || \"example.com\"\nend\n",
		"assets/package.json":                            `{"name":"assets","dependencies":{"alpinejs":"3.14.1"}}`, "assets/package-lock.json": `{"name":"assets","lockfileVersion":3,"requires":true,"packages":{"":{"name":"assets","dependencies":{"alpinejs":"3.14.1"}},"node_modules/alpinejs":{"version":"3.14.1"}}}`,
	}, SourceIdentity{Kind: SourceLocal})
	candidate := selectedOf(result)
	if candidate == nil || candidate.Recipe != "elixir" || candidate.Framework != "phoenix" || candidate.Port != 4000 || candidate.Profile != ProfileWeb ||
		candidate.StartCommand != `/app/bin/shop eval "Shop.Release.migrate" && exec /app/bin/shop start` || candidate.UnpinnedDependencies ||
		candidate.Toolchain == nil || candidate.Toolchain.Release != "1.18.4-otp-27" || candidate.PackageManager != "npm" {
		t.Fatalf("candidate = %#v", candidate)
	}
	if setAsideKind(result, "asset-pipeline") == nil || len(result.Candidates) != 1 {
		t.Fatalf("the assets package was offered: %#v", result.Candidates)
	}
	variables := map[string]DetectedVariable{}
	for _, variable := range candidate.Variables {
		variables[variable.Name] = variable
	}
	if variables["SECRET_KEY_BASE"].Setup != "generate" || variables["PHX_HOST"].Setup != "domain" || len(candidate.Databases) == 0 {
		t.Fatalf("variables = %#v databases = %#v", candidate.Variables, candidate.Databases)
	}
	if err := validateDetectionResult(&DraftSourceConfig{Kind: SourceLocal}, withSelection(result)); err != nil {
		t.Fatal(err)
	}
	overlay := selectedOf(detectShapeFixture(t, map[string]string{
		"mix.exs": phoenixMix, "rel/overlays/bin/migrate": "#!/bin/sh\nexec ./shop eval Shop.Release.migrate\n",
		"rel/overlays/bin/server":                 "#!/bin/sh\nPHX_SERVER=true exec ./shop start\n",
		"priv/repo/migrations/1_create_users.exs": "",
	}))
	if overlay.StartCommand != "/app/bin/server" || overlay.ReleaseCommand != "bin/migrate" || !overlay.UnpinnedDependencies {
		t.Fatalf("overlay = %#v", overlay)
	}
	plug := selectedOf(detectShapeFixture(t, map[string]string{
		"mix.exs":                "defmodule Api.MixProject do\n  def project, do: [app: :api, deps: [{:bandit, \"~> 1.5\"}]]\nend\n",
		"lib/api/application.ex": "children = [{Bandit, plug: Api.Router, port: String.to_integer(System.get_env(\"PORT\") || \"8080\")}]\n# port: 8080\n",
	}))
	if plug.Framework != "elixir" || plug.Profile != ProfileWeb || plug.Port != 8080 || plug.StartCommand != "/app/bin/api start" {
		t.Fatalf("plug = %#v", plug)
	}
	sqlite := selectedOf(detectShapeFixture(t, map[string]string{
		"mix.exs":            "defmodule Notes.MixProject do\n  def project, do: [app: :notes, deps: [{:phoenix, \"~> 1.7\"}, {:ecto_sqlite3, \"~> 0.17\"}]]\nend\n",
		"config/runtime.exs": "database_path = System.get_env(\"DATABASE_PATH\") || raise \"environment variable DATABASE_PATH is missing.\"\n",
	}))
	if len(sqlite.PersistentPaths) != 1 || sqlite.PersistentPaths[0].Target != "/app/data" || sqlite.PersistentPaths[0].Value != "/app/data/notes.db" {
		t.Fatalf("persistent paths = %#v", sqlite.PersistentPaths)
	}
}

func TestElixirRecipeRendersARelease(t *testing.T) {
	files := map[string]string{
		"mix.exs": phoenixMix, "mix.lock": "%{}\n", ".tool-versions": "elixir 1.18.4-otp-27\n",
		"assets/package.json": `{"name":"assets","dependencies":{"alpinejs":"3.14.1"}}`, "assets/package-lock.json": `{"name":"assets","lockfileVersion":3,"requires":true,"packages":{"":{"name":"assets","dependencies":{"alpinejs":"3.14.1"}},"node_modules/alpinejs":{"version":"3.14.1"}}}`,
	}
	prepared, err := prepareFixture(t, files, BuildPlanConfig{Method: BuildRecipe, Recipe: "elixir", StartCommand: "/app/bin/shop start"})
	if err != nil {
		t.Fatal(err)
	}
	assertRendered(t, prepared, []string{
		"FROM elixir:1.18.4-otp-27-slim@sha256:", "build-essential ca-certificates git", "RUN mix local.hex --force && mix local.rebar --force",
		"RUN mix deps.get --only prod --check-locked", "RUN mix deps.compile", "RUN mix assets.setup", "WORKDIR /app/assets", "npm ci",
		"RUN mix compile", "RUN mix assets.deploy", "RUN mix release --overwrite", "ENV MIX_ENV=prod LANG=C.UTF-8 PHX_SERVER=true",
		"COPY --from=build --chown=10001:10001 /app/_build/prod/rel/shop/ /app/", "USER 10001", `CMD ["/bin/sh","-c","/app/bin/shop start"]`,
	}, nil)
	// The release runs on the image that built it.
	images := strings.Count(prepared.DockerfilePreview, "FROM elixir:1.18.4-otp-27-slim@")
	if images != 2 || prepared.Toolchain != "elixir 1.18.4-otp-27 · assets: npm (bundled with Node 22)" && !strings.HasPrefix(prepared.Toolchain, "elixir 1.18.4-otp-27 · assets: ") {
		t.Fatalf("elixir images %d, toolchain %q", images, prepared.Toolchain)
	}
	named, err := prepareFixture(t, map[string]string{"mix.exs": "[apps_path: \"apps\", releases: [shop: [applications: [web: :permanent]]]]"},
		BuildPlanConfig{Method: BuildRecipe, Recipe: "elixir"})
	if err != nil {
		t.Fatal(err)
	}
	assertRendered(t, named, []string{"RUN mix release shop --overwrite", "/app/_build/prod/rel/shop/ /app/", `CMD ["/bin/sh","-c","/app/bin/shop start"]`},
		[]string{"PHX_SERVER", "--check-locked"})
	umbrella := map[string]string{
		"mix.exs":                  "[apps_path: \"apps\", releases: [shop: [applications: [shop: :permanent, shop_web: :permanent]]]]",
		"apps/shop/mix.exs":        "[app: :shop, build_path: \"../../_build\", deps: [{:ecto_sql, \"~> 3.12\"}]]",
		"apps/shop_web/mix.exs":    "[app: :shop_web, build_path: \"../../_build\", deps: [{:phoenix, \"~> 1.7\"}], aliases: [\"assets.deploy\": [\"esbuild shop_web --minify\", \"phx.digest\"]]]",
		"apps/shop_web/lib/web.ex": "",
	}
	root := selectedOf(detectShapeFixture(t, umbrella))
	child := candidateAtRoot(detectShapeFixture(t, umbrella), "apps/shop_web", BuildRecipe)
	if root == nil || root.Root != "" || root.Framework != "phoenix" || root.Profile != ProfileWeb || root.StartCommand != "/app/bin/shop start" ||
		child == nil || !strings.Contains(child.RecipeIssue, "umbrella") {
		t.Fatalf("umbrella root = %#v, child = %#v", root, child)
	}
	secret := false
	for _, variable := range root.Variables {
		secret = secret || variable.Name == "SECRET_KEY_BASE" && variable.Setup == "generate"
	}
	if !secret {
		t.Fatalf("the umbrella's Phoenix application gave its root no secret: %#v", root.Variables)
	}
	prepared, err = prepareFixture(t, umbrella, BuildPlanConfig{Method: BuildRecipe, Recipe: "elixir"})
	if err != nil {
		t.Fatal(err)
	}
	assertRendered(t, prepared, []string{"RUN cd apps/shop_web && mix assets.deploy", "RUN mix release shop --overwrite", "PHX_SERVER=true"}, nil)
	if _, err := prepareFixture(t, map[string]string{"mix.exs": `[apps_path: "apps"]`}, BuildPlanConfig{Method: BuildRecipe, Recipe: "elixir"}); err == nil ||
		!strings.Contains(err.Error(), "umbrella") {
		t.Fatalf("umbrella err = %v", err)
	}
}

func TestScalaAndClojureBuildOnTheJVM(t *testing.T) {
	play := map[string]string{
		"build.sbt":                "name := \"\"\"shop-web\"\"\"\n\nlazy val root = (project in file(\".\")).enablePlugins(PlayScala)\n\nscalaVersion := \"3.3.8\"\n",
		"project/plugins.sbt":      "addSbtPlugin(\"org.playframework\" % \"sbt-plugin\" % \"3.0.11\")\n",
		"project/build.properties": "sbt.version=1.11.7\n",
		"conf/routes":              "GET / controllers.HomeController.index()\n",
	}
	candidate := selectedOf(detectShapeFixture(t, play))
	if candidate == nil || candidate.Recipe != "scala" || candidate.Framework != "play" || candidate.Port != 9000 ||
		candidate.StartCommand != "exec /app/bin/shop-web -Dconfig.file=/app/conf/just-dashboard.conf -Dhttp.port=${PORT:-9000} -Dpidfile.path=/dev/null -Dplay.filters.hosts.allowed.0=." ||
		candidate.Readiness == nil || candidate.Readiness.Attempts != readinessSlowAttempts {
		t.Fatalf("play = %#v", candidate)
	}
	generated := false
	for _, variable := range candidate.Variables {
		generated = generated || variable.Name == "APPLICATION_SECRET" && variable.Setup == "generate" && variable.GenerateLength == 64
	}
	if !generated {
		t.Fatalf("variables = %#v", candidate.Variables)
	}
	prepared, err := prepareFixture(t, play, BuildPlanConfig{Method: BuildRecipe, Recipe: "scala"})
	if err != nil {
		t.Fatal(err)
	}
	assertRendered(t, prepared, []string{
		"FROM sbtscala/scala-sbt:eclipse-temurin-21_1.x@sha256:", "RUN sbt -batch update", "RUN sbt -batch stage",
		"FROM eclipse-temurin:21-jre@sha256:", "COPY --from=build --chown=10001:10001 /src/target/universal/stage/ /app/",
		// A stock application.conf leaves the secret at "changeme"; the
		// configuration the start loads reads it from the environment.
		`RUN mkdir -p /app/conf && printf '%s\n' 'include "application"' 'play.http.secret.key = ${?APPLICATION_SECRET}' > /app/conf/just-dashboard.conf` + "\nUSER 10001",
		`ENV JAVA_TOOL_OPTIONS="-XX:MaxRAMPercentage=75"`, "exec /app/bin/shop-web -Dconfig.file=/app/conf/just-dashboard.conf -Dhttp.port=${PORT:-9000}",
	}, []string{"APPLICATION_SECRET=", "-Dplay.http.secret.key"})
	release := selectedOf(detectShapeFixture(t, map[string]string{
		"build.sbt":           "name := \"api\"\njavacOptions ++= Seq(\"--release\", \"17\")\nlazy val root = (project in file(\".\")).enablePlugins(JavaAppPackaging)\n",
		"project/plugins.sbt": "addSbtPlugin(\"com.github.sbt\" % \"sbt-native-packager\" % \"1.10.4\")\nlibraryDependencies += \"org.http4s\" %% \"http4s-ember-server\" % \"0.23.30\"\n",
	}, SourceIdentity{Kind: SourceLocal}))
	if release == nil || release.Toolchain == nil || release.Toolchain.Release != "17" || release.Toolchain.From != "build.sbt" {
		t.Fatalf("release = %#v", release)
	}
	// A lower release is the bytecode the build targets, which 17 builds.
	for build, target := range map[string]string{
		"javacOptions ++= Seq(\"--release\", \"11\")\n": "11",
		"scalacOptions += \"-release:8\"\n":             "8",
	} {
		lower := selectedOf(detectShapeFixture(t, map[string]string{
			"build.sbt":           "name := \"api\"\n" + build + "lazy val root = (project in file(\".\")).enablePlugins(JavaAppPackaging)\n",
			"project/plugins.sbt": "addSbtPlugin(\"com.github.sbt\" % \"sbt-native-packager\" % \"1.10.4\")\n",
		}))
		if lower == nil || lower.RecipeIssue != "" || lower.Toolchain == nil || lower.Toolchain.Release != "17" ||
			!slices.ContainsFunc(lower.Evidence, func(e DetectionEvidence) bool {
				return e.Reason == "compiles for Java "+target+", which Java 17 builds and runs"
			}) {
			t.Fatalf("release %s = %#v", target, lower)
		}
	}
	if _, err := prepareFixture(t, map[string]string{"build.sbt": "name := \"x\"\n", ".java-version": "11\n"}, BuildPlanConfig{Method: BuildRecipe, Recipe: "scala"}); err == nil ||
		!strings.Contains(err.Error(), ".java-version names 11") {
		t.Fatalf(".java-version 11 err = %v", err)
	}
	if err := validateDetectionResult(&DraftSourceConfig{Kind: SourceLocal}, withSelection(DetectionResult{Source: SourceIdentity{Kind: SourceLocal}, Candidates: []DetectedCandidate{*release}})); err != nil {
		t.Fatal(err)
	}
	multi := map[string]string{
		"build.sbt":           "lazy val core = project\nlazy val server = (project in file(\"server\"))\n  .enablePlugins(JavaAppPackaging)\n  .settings(name := \"Api Server\")\n",
		"project/plugins.sbt": "addSbtPlugin(\"com.github.sbt\" % \"sbt-native-packager\" % \"1.10.4\")\n",
		".java-version":       "17\n",
	}
	prepared, err = prepareFixture(t, multi, BuildPlanConfig{Method: BuildRecipe, Recipe: "scala"})
	if err != nil {
		t.Fatal(err)
	}
	assertRendered(t, prepared, []string{"eclipse-temurin-17_1.x", "/src/server/target/universal/stage/ /app/", "exec /app/bin/api-server"}, nil)
	// build.sbt text reaches the Dockerfile only as a plain path: a line
	// break in a project's directory (sbt loads one written inside a
	// comment) cannot start instructions of its own.
	injected := map[string]string{
		"build.sbt":           "/*\nlazy val web = (project in file(\"web\nFROM alpine AS injected\nRUN echo injected-instruction\n\")).enablePlugins(JavaAppPackaging)\n*/\n",
		"project/plugins.sbt": "addSbtPlugin(\"com.github.sbt\" % \"sbt-native-packager\" % \"1.10.4\")\n",
	}
	prepared, err = prepareFixture(t, injected, BuildPlanConfig{Method: BuildRecipe, Recipe: "scala"})
	if err != nil {
		t.Fatal(err)
	}
	assertRendered(t, prepared, []string{"/src/web/target/universal/stage/ /app/"}, []string{"alpine", "injected"})
	// Whatever reaches the renderer, no instruction spans lines.
	bases := []ResolvedImage{{Reference: "sbtscala/scala-sbt:eclipse-temurin-21_1.x", Digest: "sha256:" + strings.Repeat("a", 64)},
		{Reference: "eclipse-temurin:21-jre", Digest: "sha256:" + strings.Repeat("b", 64)}}
	crafted := selectedRecipe{kind: "scala", language: languageBuild{jvm: jvmLanguageRecipe{language: "scala", tool: "sbt-stage", jdk: "21", sbt: "1",
		stage: "web\nFROM alpine AS injected", script: "web"}}}
	if _, err := renderLanguageDockerfile(crafted, BuildPlanConfig{}, bases, "", ""); err == nil || !errors.Is(err, ErrUnsupportedBuilder) {
		t.Fatalf("a line break reached the Dockerfile: %v", err)
	}
	for name, build := range map[string]string{
		"a directory with a space": "lazy val web = (project in file(\"web app\")).enablePlugins(JavaAppPackaging)\n",
		"a directory above":        "lazy val web = (project in file(\"../web\")).enablePlugins(JavaAppPackaging)\n",
		"a script name with a $":   "enablePlugins(JavaAppPackaging)\nexecutableScriptName := \"$(id)\"\n",
	} {
		injected["build.sbt"] = build
		if _, err := prepareFixture(t, injected, BuildPlanConfig{Method: BuildRecipe, Recipe: "scala"}); err == nil || !errors.Is(err, ErrUnsupportedBuilder) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	assembly := map[string]string{"build.sbt": "name := \"worker\"\n", "project/plugins.sbt": "addSbtPlugin(\"com.eed3si9n\" % \"sbt-assembly\" % \"2.3.0\")\n"}
	prepared, err = prepareFixture(t, assembly, BuildPlanConfig{Method: BuildRecipe, Recipe: "scala"})
	if err != nil {
		t.Fatal(err)
	}
	assertRendered(t, prepared, []string{"RUN sbt -batch assembly", "FROM eclipse-temurin:21-jre-alpine@sha256:", "exec java -jar /app/app.jar"}, nil)
	if _, err := prepareFixture(t, map[string]string{"build.sbt": "name := \"x\"\n"}, BuildPlanConfig{Method: BuildRecipe, Recipe: "scala"}); err == nil ||
		!strings.Contains(err.Error(), "sbt-native-packager") {
		t.Fatalf("plain sbt err = %v", err)
	}

	lein := map[string]string{
		"project.clj":       "(defproject shop \"0.1.0\"\n  :dependencies [[org.clojure/clojure \"1.12.6\"] [ring/ring-jetty-adapter \"1.15.3\"] [org.postgresql/postgresql \"42.7.5\"]]\n  :main ^:skip-aot shop.core\n  :profiles {:uberjar {:aot :all}})\n",
		"src/shop/core.clj": "(ns shop.core)\n",
	}
	clojure := selectedOf(detectShapeFixture(t, lein))
	if clojure == nil || clojure.Recipe != "clojure" || clojure.Framework != "ring" || clojure.Port != 3000 || clojure.StartCommand != "exec java -jar /app/app.jar" ||
		len(clojure.Databases) == 0 || clojure.Databases[0].Engine != "postgres" {
		t.Fatalf("clojure = %#v", clojure)
	}
	prepared, err = prepareFixture(t, lein, BuildPlanConfig{Method: BuildRecipe, Recipe: "clojure"})
	if err != nil {
		t.Fatal(err)
	}
	assertRendered(t, prepared, []string{"FROM clojure:temurin-21-lein@sha256:", "RUN lein deps", "RUN lein uberjar", "cp \"$jar\" /out/app.jar",
		"FROM eclipse-temurin:21-jre-alpine@sha256:", "COPY --from=build /out/app.jar /app/app.jar"}, nil)
	notAOT := map[string]string{"project.clj": "(defproject shop \"0.1.0\" :main ^:skip-aot shop.core)\n"}
	if issue := selectedOf(detectShapeFixture(t, notAOT)).RecipeIssue; !strings.Contains(issue, ":aot :all") {
		t.Fatalf("issue = %q", issue)
	}
	deps := map[string]string{
		"deps.edn":  "{:deps {ring/ring-jetty-adapter {:mvn/version \"1.15.3\"}}\n :aliases {:build {:deps {io.github.clojure/tools.build {:mvn/version \"0.10.14\"}} :ns-default build}}}\n",
		"build.clj": "(ns build (:require [clojure.tools.build.api :as b]))\n(defn uber [_] (b/uber {}))\n",
	}
	prepared, err = prepareFixture(t, deps, BuildPlanConfig{Method: BuildRecipe, Recipe: "clojure"})
	if err != nil {
		t.Fatal(err)
	}
	assertRendered(t, prepared, []string{"FROM clojure:temurin-21-tools-deps@sha256:", "RUN clojure -P && clojure -P -T:build", "RUN clojure -T:build uber"}, nil)
	if _, err := prepareFixture(t, map[string]string{"deps.edn": "{:deps {}}\n"}, BuildPlanConfig{Method: BuildRecipe, Recipe: "clojure"}); err == nil ||
		!strings.Contains(err.Error(), "b/uber") {
		t.Fatalf("deps.edn without a build err = %v", err)
	}
}

func TestDartAndGleamServers(t *testing.T) {
	frog := map[string]string{
		"pubspec.yaml": "name: api\nenvironment:\n  sdk: ^3.5.0\ndependencies:\n  dart_frog: ^1.1.0\n", "pubspec.lock": "sdks:\n  dart: \">=3.5.0 <4.0.0\"\n",
		"routes/index.dart": "Response onRequest(RequestContext context) => Response(body: 'ok');\n", "public/favicon.ico": "x",
	}
	candidate := selectedOf(detectShapeFixture(t, frog))
	if candidate == nil || candidate.Recipe != "dart" || candidate.Framework != "dart_frog" || candidate.Port != 8080 || candidate.StartCommand != "/app/server" ||
		candidate.Toolchain == nil || candidate.Toolchain.Tool != "dart-frog" || candidate.UnpinnedDependencies {
		t.Fatalf("frog = %#v", candidate)
	}
	prepared, err := prepareFixture(t, frog, BuildPlanConfig{Method: BuildRecipe, Recipe: "dart"})
	if err != nil {
		t.Fatal(err)
	}
	assertRendered(t, prepared, []string{"FROM dart:3.13@sha256:", "RUN dart pub get --enforce-lockfile", "dart pub global activate dart_frog_cli " + dartFrogCLIVersion,
		"dart_frog build && cd build && dart pub get --offline && dart compile exe bin/server.dart -o /out/server", "FROM debian:trixie-slim@sha256:",
		"COPY --from=build --chown=10001:10001 /app/build/public/ /app/public/", "USER 10001"}, nil)
	shelf := map[string]string{"pubspec.yaml": "name: api\nenvironment:\n  sdk: '>=3.0.0 <3.11.0'\ndependencies:\n  shelf: ^1.4.0\n", "bin/server.dart": "void main() {}\n"}
	prepared, err = prepareFixture(t, shelf, BuildPlanConfig{Method: BuildRecipe, Recipe: "dart"})
	if err != nil {
		t.Fatal(err)
	}
	assertRendered(t, prepared, []string{"FROM dart:3.10@sha256:", "RUN dart pub get\n", "RUN dart compile exe bin/server.dart -o /out/server"}, []string{"dart_frog"})
	if _, err := prepareFixture(t, map[string]string{"pubspec.yaml": "name: api\nenvironment:\n  sdk: ^4.0.0\n", "bin/main.dart": ""}, BuildPlanConfig{Method: BuildRecipe, Recipe: "dart"}); err == nil ||
		!strings.Contains(err.Error(), "^4.0.0") {
		t.Fatalf("sdk err = %v", err)
	}

	gleam := map[string]string{
		"gleam.toml":    "name = \"api\"\ngleam = \">= 1.4.0\"\n\n[dependencies]\nmist = \">= 5.0.0 and < 6.0.0\"\n",
		"manifest.toml": "packages = []\n",
		"src/api.gleam": "pub fn main() {\n  let port = envoy.get(\"PORT\")\n  mist.new(handler) |> mist.port(8123) |> mist.start\n}\n",
	}
	candidate = selectedOf(detectShapeFixture(t, gleam))
	if candidate == nil || candidate.Recipe != "gleam" || candidate.Framework != "mist" || candidate.Port != 8123 || candidate.StartCommand != "/app/entrypoint.sh run" {
		t.Fatalf("gleam = %#v", candidate)
	}
	prepared, err = prepareFixture(t, gleam, BuildPlanConfig{Method: BuildRecipe, Recipe: "gleam", StartCommand: candidate.StartCommand})
	if err != nil {
		t.Fatal(err)
	}
	assertRendered(t, prepared, []string{"FROM " + gleamImage + "@sha256:", "RUN gleam deps download", "RUN gleam export erlang-shipment",
		"COPY --from=build --chown=app:app /app/build/erlang-shipment/ /app/", "USER app"}, nil)
	for _, refused := range []map[string]string{
		{"gleam.toml": "name = \"api\"\ntarget = \"javascript\"\n"},
		{"gleam.toml": "name = \"api\"\ngleam = \"< 1.0.0\"\n"},
	} {
		if _, err := prepareFixture(t, refused, BuildPlanConfig{Method: BuildRecipe, Recipe: "gleam"}); err == nil || !errors.Is(err, ErrUnsupportedBuilder) {
			t.Fatalf("gleam %v: err = %v", refused, err)
		}
	}
}
