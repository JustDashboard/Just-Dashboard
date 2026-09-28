package deploy

import (
	"slices"
	"strings"
	"testing"
)

const railsGemfileLock = `GEM
  remote: https://rubygems.org/
  specs:
    jsbundling-rails (1.3.1)
    rails (7.2.1)
    railties (7.2.1)
    sidekiq (7.3.0)
    pg (1.5.7)

DEPENDENCIES
  rails (~> 7.2)
`

func TestEcosystemsWithoutARecipeAreNamed(t *testing.T) {
	for _, fixture := range []struct {
		name, framework, remedy string
		files                   map[string]string
		profile                 WorkloadProfile
		port                    int
	}{
		{"haskell", "servant", "haskell image", map[string]string{"stack.yaml": "resolver: lts-22.0\n", "package.yaml": "dependencies:\n- servant-server\n"}, ProfileWeb, 8080},
		{"crystal", "kemal", "crystallang/crystal", map[string]string{"shard.yml": "dependencies:\n  kemal:\n    github: kemalcr/kemal\n"}, ProfileWeb, 3000},
		{"zig", "zig", "zig build", map[string]string{"build.zig": "const std = @import(\"std\");\n"}, ProfileService, 0},
		{"r shiny", "shiny", "rocker/shiny", map[string]string{"app.R": "library(shiny)\n", "renv.lock": "{}"}, ProfileWeb, 3838},
		{"c++", "cpp", "CMake", map[string]string{"CMakeLists.txt": "project(server)\n"}, ProfileService, 0},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			result := detectShapeFixture(t, fixture.files)
			if len(result.Candidates) != 1 {
				t.Fatalf("candidates = %#v", result.Candidates)
			}
			candidate := result.Candidates[0]
			if candidate.Framework != fixture.framework || candidate.Recipe != "" || candidate.Profile != fixture.profile ||
				candidate.Port != fixture.port || !strings.Contains(candidate.RecipeIssue, "no automatic recipe") ||
				!strings.Contains(candidate.RecipeIssue, fixture.remedy) {
				t.Fatalf("candidate = %#v", candidate)
			}
			if result.SelectedID != candidate.ID {
				t.Fatal("the only candidate was not selected")
			}
		})
	}
}

func TestAssetPipelinesBelongToTheirApplication(t *testing.T) {
	phoenix := detectShapeFixture(t, map[string]string{
		"mix.exs":             "[{:phoenix, \"~> 1.7\"}]",
		"assets/package.json": `{"devDependencies":{"esbuild":"0.24"}}`, "assets/package-lock.json": "{}",
	})
	if len(phoenix.Candidates) != 1 || phoenix.Candidates[0].Framework != "phoenix" || phoenix.Candidates[0].Recipe != "elixir" ||
		setAsideKind(phoenix, "asset-pipeline") == nil {
		t.Fatalf("phoenix = %#v", phoenix)
	}
	withDockerfile := detectShapeFixture(t, map[string]string{
		"Gemfile": "gem 'rails'\ngem 'sidekiq'\n", "Gemfile.lock": railsGemfileLock,
		"package.json": `{"devDependencies":{"esbuild":"0.24"}}`, "yarn.lock": "",
		"Dockerfile": "FROM ruby:3.3\nEXPOSE 80\n",
	})
	// The Dockerfile and the Ruby recipe are two ways to build the same
	// application; the repository's own Dockerfile is chosen.
	selected := selectedOf(withDockerfile)
	if len(withDockerfile.Candidates) != 2 || selected == nil || selected.BuildMethod != BuildDockerfile || selected.Framework != "rails" ||
		len(selected.Processes) != 1 || selected.Processes[0].Command != "bundle exec sidekiq" ||
		candidateAtRoot(withDockerfile, "", BuildRecipe) == nil || candidateAtRoot(withDockerfile, "", BuildRecipe).Recipe != "ruby" {
		t.Fatalf("rails with Dockerfile = %#v", withDockerfile.Candidates)
	}
	hanami := detectShapeFixture(t, map[string]string{
		"Gemfile": "gem 'hanami', '~> 2.1'\ngem 'hanami-assets'\n", "config.ru": "run Hanami.app\n",
		"package.json": `{"name":"assets","dependencies":{"hanami-assets":"2"}}`, "package-lock.json": "{}",
	})
	if len(hanami.Candidates) != 1 || hanami.Candidates[0].Framework != "hanami" || hanami.Candidates[0].Recipe != "ruby" {
		t.Fatalf("hanami = %#v", hanami.Candidates)
	}
	cocoapods := detectShapeFixture(t, map[string]string{
		"Gemfile": "gem 'cocoapods'\ngem 'fastlane'\n", "package.json": expressManifest, "package-lock.json": "{}",
	})
	if len(cocoapods.Candidates) != 1 || cocoapods.Candidates[0].Recipe != "node" {
		t.Fatalf("a CocoaPods Gemfile claimed the package: %#v", cocoapods.Candidates)
	}
	nested := detectShapeFixture(t, map[string]string{
		"go.mod": "module example.test/app\n\ngo 1.26\n", "main.go": "package main\n\nfunc main() {}\n",
		"third_party/zlib/CMakeLists.txt": "project(zlib)\n",
	})
	if len(nested.Candidates) != 1 || nested.Candidates[0].Recipe != "go" {
		t.Fatalf("vendored C code became a candidate: %#v", nested.Candidates)
	}
}

func TestSecondaryProcessesAreDetected(t *testing.T) {
	t.Run("procfile worker and release", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"requirements.txt": "django==5.2\ncelery==5.4\n", "manage.py": "", "config/wsgi.py": "",
			"Procfile": "web: gunicorn config.wsgi\nworker: celery -A config.celery_app worker -l info\nbeat: celery -A config.celery_app beat\nrelease: python manage.py migrate\n",
		})
		candidate := selectedOf(result)
		kinds := map[string]string{}
		for _, process := range candidate.Processes {
			kinds[process.Name] = process.Kind
		}
		if kinds["worker"] != "worker" || kinds["beat"] != "scheduler" || kinds["release"] != "release" || len(candidate.Processes) != 3 {
			t.Fatalf("processes = %#v", candidate.Processes)
		}
		findings := repoShapeFindings(&result, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe, StartCommand: candidate.StartCommand}})
		if !hasFinding(findings, "secondary_process_not_deployed_worker", PreflightWarning) ||
			!hasFinding(findings, "secondary_process_not_deployed_beat", PreflightWarning) {
			t.Fatalf("findings = %#v", findings)
		}
		// The Procfile's release process is the candidate's release command,
		// planned as a release task in the image and asked for by
		// release_command_unmapped while it is not.
		if candidate.ReleaseCommand != "python manage.py migrate" || hasFinding(findings, "release_process_not_run", PreflightWarning) {
			t.Fatalf("release command %q, findings = %#v", candidate.ReleaseCommand, findings)
		}
		// One nothing else plans is reported, pointing at a release task.
		undeclared := result
		undeclared.Candidates = append([]DetectedCandidate(nil), result.Candidates...)
		for index := range undeclared.Candidates {
			undeclared.Candidates[index].ReleaseCommand = ""
		}
		unplanned := repoShapeFindings(&undeclared, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe, StartCommand: candidate.StartCommand}})
		if !hasFinding(unplanned, "release_process_not_run", PreflightWarning) {
			t.Fatal("the Procfile release command was not reported")
		}
		planned := repoShapeFindings(&undeclared, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe, StartCommand: candidate.StartCommand,
			ReleaseTasks: []ReleaseTaskConfig{{Name: "release", Command: "python manage.py migrate", Runner: ReleaseTaskRunnerImage}}}})
		if hasFinding(planned, "release_process_not_run", PreflightWarning) {
			t.Fatal("a release command a release task runs was reported")
		}
		migrating := repoShapeFindings(&result, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe,
			StartCommand: "python manage.py migrate --noinput && gunicorn config.wsgi"}})
		if hasFinding(migrating, "release_process_not_run", PreflightWarning) {
			t.Fatal("a migration release was reported although the start command migrates")
		}
		deployingWorker := repoShapeFindings(&result, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe,
			StartCommand: "celery -A config.celery_app worker -l info"}})
		if hasFinding(deployingWorker, "secondary_process_not_deployed_worker", PreflightWarning) {
			t.Fatal("the worker's own project was told the worker is not deployed")
		}
	})
	t.Run("celery app and beat schedule", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"requirements.txt": "django==5.2\ncelery==5.4\ndjango-celery-beat==2.7\n", "manage.py": "", "proj/wsgi.py": "",
			"proj/celery.py": "from celery import Celery\napp = Celery('proj')\n",
		})
		commands := []string{}
		for _, process := range selectedOf(result).Processes {
			commands = append(commands, process.Command)
		}
		want := []string{"celery -A proj.celery worker --loglevel=info",
			"celery -A proj.celery beat --loglevel=info --scheduler django_celery_beat.schedulers:DatabaseScheduler"}
		if strings.Join(commands, "|") != strings.Join(want, "|") {
			t.Fatalf("commands = %q", commands)
		}
	})
	t.Run("rq worker", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"requirements.txt": "flask==3.1\nrq==2.0\n", "app.py": "from flask import Flask\napp = Flask(__name__)\n",
			".env.example": "REDIS_URL=redis://localhost:6379\n",
		})
		processes := selectedOf(result).Processes
		if len(processes) != 1 || processes[0].Command != `rq worker --url "$REDIS_URL"` {
			t.Fatalf("processes = %#v", processes)
		}
	})
	t.Run("laravel queue scheduler and reverb", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"composer.json": `{"require":{"laravel/framework":"^12.0","laravel/reverb":"^1.0"}}`, "composer.lock": "{}", "artisan": "",
			"app/Jobs/SendInvoice.php": "<?php\nnamespace App\\Jobs;\nclass SendInvoice implements ShouldQueue {}\n",
			"routes/console.php":       "<?php\nSchedule::command('reports:daily')->daily();\n",
			".env.example":             "QUEUE_CONNECTION=database\n",
		})
		names := map[string]string{}
		for _, process := range selectedOf(result).Processes {
			names[process.Name] = process.Command
		}
		if names["queue"] != "php artisan queue:work --tries=3 --max-time=3600" || names["scheduler"] != "php artisan schedule:work" ||
			names["reverb"] != "php artisan reverb:start --host=0.0.0.0 --port=8080" {
			t.Fatalf("processes = %#v", names)
		}
		synchronous := detectShapeFixture(t, map[string]string{
			"composer.json": `{"require":{"laravel/framework":"^12.0"}}`, "artisan": "",
			"app/Jobs/SendInvoice.php": "<?php\nclass SendInvoice implements ShouldQueue {}\n",
			".env.example":             "QUEUE_CONNECTION=sync\n",
		})
		if len(selectedOf(synchronous).Processes) != 0 {
			t.Fatalf("a sync queue needs no worker: %#v", selectedOf(synchronous).Processes)
		}
	})
	t.Run("horizon", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"composer.json": `{"require":{"laravel/framework":"^12.0","laravel/horizon":"^5.0"}}`, "artisan": "",
		})
		processes := selectedOf(result).Processes
		if len(processes) != 1 || processes[0].Command != "php artisan horizon" {
			t.Fatalf("processes = %#v", processes)
		}
		extensions := []string{}
		for _, extension := range selectedOf(result).PHP.Extensions {
			extensions = append(extensions, extension.Name)
		}
		if !slices.Contains(extensions, "pcntl") || !slices.Contains(extensions, "redis") {
			t.Fatalf("horizon extensions = %v", extensions)
		}
	})
	t.Run("a Procfile worker is the Gemfile's worker", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"Gemfile": "gem 'rails'\ngem 'sidekiq'\n", "Gemfile.lock": railsGemfileLock,
			"Procfile": "web: bin/rails server\nworker: bundle exec sidekiq\n",
		})
		candidate := candidateAtRoot(result, "", BuildRecipe)
		if candidate == nil || len(candidate.Processes) != 1 || candidate.Processes[0].Name != "worker" ||
			candidate.Processes[0].Source != "Procfile" {
			t.Fatalf("processes = %#v", candidate)
		}
		if merged := appendProcesses([]DetectedProcess{{Name: "sidekiq", Kind: "worker", Command: "bundle exec sidekiq"}},
			DetectedProcess{Name: "jobs", Kind: "worker", Command: "bundle exec sidekiq"}); len(merged) != 1 {
			t.Fatalf("one command became two processes: %#v", merged)
		}
	})
	t.Run("solid queue inside puma", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"Gemfile":        "gem 'rails', '~> 8.0'\ngem 'puma'\ngem 'solid_queue'\n",
			"Gemfile.lock":   "GEM\n  specs:\n    puma (6.5.0)\n    rails (8.0.1)\n    railties (8.0.1)\n    solid_queue (1.1.2)\n\nDEPENDENCIES\n  rails (~> 8.0)\n",
			"config/puma.rb": "port ENV.fetch(\"PORT\", 3000)\nplugin :solid_queue if ENV[\"SOLID_QUEUE_IN_PUMA\"]\n",
			"Dockerfile":     "FROM ruby:3.3-slim\nWORKDIR /rails\nCOPY . .\nEXPOSE 3000\nCMD [\"./bin/rails\", \"server\"]\n",
		})
		candidate := selectedOf(result)
		jobs := false
		for _, process := range candidate.Processes {
			jobs = jobs || process.Command == "bin/jobs"
		}
		if !jobs {
			t.Fatalf("processes = %#v", candidate.Processes)
		}
		inPuma := PlanConfiguration{Build: BuildPlanConfig{Method: BuildDockerfile},
			Variables: []PlannedVariable{{Name: "SOLID_QUEUE_IN_PUMA", Value: "true", Sensitivity: "plain", Scopes: []string{"runtime"}}}}
		if hasFinding(repoShapeFindings(&result, inPuma), "secondary_process_not_deployed_jobs", PreflightWarning) {
			t.Fatal("the jobs Puma runs were reported as not deployed")
		}
		if !hasFinding(repoShapeFindings(&result, PlanConfiguration{Build: BuildPlanConfig{Method: BuildDockerfile}}), "secondary_process_not_deployed_jobs", PreflightWarning) {
			t.Fatal("jobs nothing runs were not reported")
		}
	})
	t.Run("fly release command is the release overlay", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"mix.exs": "defp deps do [{:phoenix, \"~> 1.7\"}] end", "rel/overlays/bin/migrate": "#!/bin/sh\n",
			"Dockerfile": "FROM elixir:1.17\nWORKDIR /app\nCOPY . .\nCMD [\"/app/bin/server\"]\n",
			"fly.toml":   "app = \"shop\"\n\n[deploy]\n  release_command = \"/app/bin/migrate\"\n",
		})
		candidate := selectedOf(result)
		if candidate.ReleaseCommand != "bin/migrate" || len(candidate.Processes) != 1 || candidate.Processes[0].Command != "/app/bin/migrate" {
			t.Fatalf("release command %q, processes %#v", candidate.ReleaseCommand, candidate.Processes)
		}
		planned := PlanConfiguration{Build: BuildPlanConfig{Method: BuildDockerfile,
			ReleaseTasks: []ReleaseTaskConfig{{Name: "release", Command: "bin/migrate", Runner: ReleaseTaskRunnerImage}}}}
		if findings := repoShapeFindings(&result, planned); hasFinding(findings, "release_process_not_run", PreflightWarning) {
			t.Fatalf("the planned release task was reported as not run: %#v", findings)
		}
		if sameReleaseCommand("/app/bin/migrate", "app/bin/migrate x") || sameReleaseCommand("/app/bin/migrate", "") ||
			!sameReleaseCommand(" bin/migrate", "/app/bin/migrate") {
			t.Fatal("sameReleaseCommand")
		}
	})
	t.Run("bullmq worker file", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"package.json":      `{"name":"api","scripts":{"start":"node server.js"},"dependencies":{"express":"4","bullmq":"5"}}`,
			"package-lock.json": "{}", "server.js": "",
			"worker.js": "import { Worker } from 'bullmq'\nnew Worker('mail', async () => {})\n",
		})
		processes := selectedOf(result).Processes
		if len(processes) != 1 || processes[0].Command != "node worker.js" {
			t.Fatalf("processes = %#v", processes)
		}
	})
}

// A site generator's root package.json of Tailwind or PostCSS is its tooling,
// not a Node worker that hides the generator: the generator's own recipe
// builds the site and installs the package for it.
func TestSiteGeneratorOwnsItsToolingPackage(t *testing.T) {
	tooling := `{"name":"site","scripts":{"build:css":"tailwindcss -i assets/in.css -o assets/out.css"},"devDependencies":{"tailwindcss":"4","postcss":"8"}}`
	for name, files := range map[string]map[string]string{
		"hugo":   {"hugo.toml": "baseURL = \"https://example.test/\"\n", "content/_index.md": ""},
		"mkdocs": {"mkdocs.yml": "site_name: Docs\n", "docs/index.md": ""},
		"jekyll": {"Gemfile": "gem 'jekyll'\n", "_config.yml": "title: Blog\n", "_posts/2024-01-01-hello.md": ""},
	} {
		t.Run(name, func(t *testing.T) {
			files["package.json"], files["package-lock.json"] = tooling, "{}"
			result := detectShapeFixture(t, files)
			if len(result.Candidates) != 1 || result.Candidates[0].Framework != name || result.Candidates[0].RecipeIssue != "" ||
				result.Candidates[0].Recipe == "" || result.Candidates[0].Profile != ProfileStatic {
				t.Fatalf("candidates = %#v", result.Candidates)
			}
			if setAsideKind(result, "asset-pipeline") == nil {
				t.Fatalf("the tooling package was not named: %#v", result.SetAside)
			}
		})
	}
	withServer := detectShapeFixture(t, map[string]string{
		"hugo.toml": "baseURL = \"https://example.test/\"\n", "package.json": expressManifest, "package-lock.json": "{}", "server.js": "",
	})
	if candidateAtRoot(withServer, "", BuildRecipe) == nil || candidateAtRoot(withServer, "", BuildRecipe).Recipe != "node" {
		t.Fatalf("an Express server beside hugo.toml was taken for tooling: %#v", withServer.Candidates)
	}
}
