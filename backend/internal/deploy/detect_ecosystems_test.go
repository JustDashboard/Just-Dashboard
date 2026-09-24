package deploy

import (
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
		{"phoenix", "phoenix", "mix phx.gen.release --docker", map[string]string{
			"mix.exs":             "defp deps do\n  [{:phoenix, \"~> 1.7\"}, {:postgrex, \">= 0.0.0\"}]\nend\n",
			"assets/package.json": `{"name":"assets","scripts":{"deploy":"esbuild js/app.js"},"devDependencies":{"esbuild":"0.24"}}`,
		}, ProfileWeb, 4000},
		{"rails with jsbundling", "rails", "commit it", map[string]string{
			"Gemfile": "source 'https://rubygems.org'\ngem 'rails'\ngem 'jsbundling-rails'\ngem 'sidekiq'\n", "Gemfile.lock": railsGemfileLock,
			"package.json": `{"name":"app","scripts":{"build":"esbuild app/javascript/*.* --bundle --outdir=app/assets/builds"},"devDependencies":{"esbuild":"0.24"}}`,
			"yarn.lock":    "",
		}, ProfileWeb, 3000},
		{"rails with importmap", "rails", "dockerfile", map[string]string{
			"Gemfile": "gem 'rails', '~> 6.1'\n", "config.ru": "run Rails.application\n",
		}, ProfileWeb, 3000},
		{"sinatra", "sinatra", "rackup", map[string]string{"Gemfile": "gem 'sinatra'\n", "config.ru": "run App\n"}, ProfileWeb, 4567},
		{"haskell", "servant", "haskell image", map[string]string{"stack.yaml": "resolver: lts-22.0\n", "package.yaml": "dependencies:\n- servant-server\n"}, ProfileWeb, 8080},
		{"crystal", "kemal", "crystallang/crystal", map[string]string{"shard.yml": "dependencies:\n  kemal:\n    github: kemalcr/kemal\n"}, ProfileWeb, 3000},
		{"zig", "zig", "zig build", map[string]string{"build.zig": "const std = @import(\"std\");\n"}, ProfileService, 0},
		{"r shiny", "shiny", "rocker/shiny", map[string]string{"app.R": "library(shiny)\n", "renv.lock": "{}"}, ProfileWeb, 3838},
		{"c++", "cpp", "CMake", map[string]string{"CMakeLists.txt": "project(server)\n"}, ProfileService, 0},
		{"hugo", "hugo", "hugo --minify", map[string]string{"hugo.toml": "baseURL = 'https://example.org/'\n", "layouts/index.html": "{{ .Title }}"}, ProfileStatic, 80},
		{"dart frog", "dart_frog", "dart compile", map[string]string{"pubspec.yaml": "name: api\ndependencies:\n  dart_frog: ^1.0.0\n"}, ProfileWeb, 8080},
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
	if len(phoenix.Candidates) != 1 || phoenix.Candidates[0].Framework != "phoenix" || setAsideKind(phoenix, "asset-pipeline") == nil {
		t.Fatalf("phoenix = %#v", phoenix)
	}
	withDockerfile := detectShapeFixture(t, map[string]string{
		"Gemfile": "gem 'rails'\ngem 'sidekiq'\n", "Gemfile.lock": railsGemfileLock,
		"package.json": `{"devDependencies":{"esbuild":"0.24"}}`, "yarn.lock": "",
		"Dockerfile": "FROM ruby:3.3\nEXPOSE 80\n",
	})
	selected := selectedOf(withDockerfile)
	if len(withDockerfile.Candidates) != 1 || selected == nil || selected.BuildMethod != BuildDockerfile || selected.Framework != "rails" ||
		len(selected.Processes) != 1 || selected.Processes[0].Command != "bundle exec sidekiq" {
		t.Fatalf("rails with Dockerfile = %#v", withDockerfile.Candidates)
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
		if !hasFinding(findings, "release_process_not_run", PreflightWarning) {
			t.Fatal("the Procfile release command was not reported")
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
		manifest, _ := parseComposerManifest([]byte(`{"require":{"laravel/horizon":"^5.0"}}`))
		if strings.Join(manifest.extensions(), ",") != "pcntl,redis" {
			t.Fatalf("horizon extensions = %v", manifest.extensions())
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
