package deploy

import (
	"slices"
	"strings"
	"testing"
)

func TestTOMLReaderReadsManifestShapes(t *testing.T) {
	entries := readTOML([]byte(`
app = "demo" # trailing comment
[build]
  dockerfile = 'Dockerfile.prod'
[http_service]
  internal_port = 8080
  force_https = true
[[http_service.checks]]
  path = "/healthz"
[processes]
  app = "bin/rails server"
  "worker" = "bin/jobs"
[phases.setup]
aptPkgs = [
  "libpq-dev", # needed by psycopg
  "ffmpeg",
]
[deploy]
release_command = """
bin/rails db:prepare"""
[env]
inline = { a = "1", b = "two" }
`))
	if tomlText(entries, "", "app") != "demo" || tomlText(entries, "build", "dockerfile") != "Dockerfile.prod" ||
		tomlText(entries, "http_service", "internal_port") != "8080" {
		t.Fatalf("scalars = %#v", entries)
	}
	if checks := tomlArrayTables(entries, "http_service.checks"); len(checks) != 1 || checks[0]["path"].text != "/healthz" {
		t.Fatalf("array tables = %#v", checks)
	}
	if value, ok := tomlLookup(entries, "phases.setup", "aptPkgs"); !ok || !slices.Equal(value.list, []string{"libpq-dev", "ffmpeg"}) {
		t.Fatalf("multi-line array = %#v", value)
	}
	if tomlText(entries, "processes", "worker") != "bin/jobs" || tomlText(entries, "deploy", "release_command") != "bin/rails db:prepare" {
		t.Fatalf("quoted key or multi-line string = %#v", entries)
	}
	if tomlText(entries, "env", "inline.b") != "two" {
		t.Fatalf("inline table = %#v", entries)
	}
}

func platformManifest(candidate *DetectedCandidate, platform string) *DetectedPlatformManifest {
	for index := range candidate.PlatformManifests {
		if candidate.PlatformManifests[index].Platform == platform {
			return &candidate.PlatformManifests[index]
		}
	}
	return nil
}

func variableNamed(candidate *DetectedCandidate, name string) *DetectedVariable {
	for index := range candidate.Variables {
		if candidate.Variables[index].Name == name {
			return &candidate.Variables[index]
		}
	}
	return nil
}

func TestOtherPlatformsManifestsAreReadAsData(t *testing.T) {
	t.Run("render blueprint", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"requirements.txt": "fastapi==0.115\nuvicorn==0.30\n", "src/server.py": "",
			"render.yaml": `services:
  - type: web
    name: api
    runtime: python
    buildCommand: pip install -r requirements.txt
    startCommand: uvicorn src.server:app --host 0.0.0.0 --port $PORT
    healthCheckPath: /api/health
    envVars:
      - key: JWT_SECRET
        generateValue: true
      - key: STRIPE_KEY
        sync: false
      - key: DATABASE_URL
        fromDatabase:
          name: db
          property: connectionString
  - type: worker
    name: mailer
    startCommand: python -m src.mailer
  - type: cron
    name: nightly
    schedule: "0 3 * * *"
    startCommand: python -m src.nightly
databases:
  - name: db
`,
		})
		candidate := selectedOf(result)
		manifest := platformManifest(candidate, "render")
		if candidate == nil || manifest == nil || candidate.StartCommand != "uvicorn src.server:app --host 0.0.0.0 --port $PORT" ||
			candidate.BuildCommand != "" || manifest.HealthPath != "/api/health" ||
			!slices.Equal(manifest.GeneratedVariables, []string{"JWT_SECRET"}) || !slices.Equal(manifest.RequiredVariables, []string{"STRIPE_KEY"}) {
			t.Fatalf("render = %#v", candidate)
		}
		if variableNamed(candidate, "JWT_SECRET") == nil || variableNamed(candidate, "DATABASE_URL") == nil {
			t.Fatalf("variables = %#v", candidate.Variables)
		}
		if !slices.ContainsFunc(candidate.Databases, func(database DetectedDatabase) bool {
			return database.Engine == "postgres" && database.Variable == "DATABASE_URL"
		}) {
			t.Fatalf("databases = %#v", candidate.Databases)
		}
		kinds := map[string]string{}
		for _, process := range candidate.Processes {
			kinds[process.Name] = process.Kind
		}
		if kinds["mailer"] != "worker" || kinds["nightly"] != "scheduler" {
			t.Fatalf("processes = %#v", candidate.Processes)
		}
		for _, decision := range candidate.NeedsDecision {
			if strings.Contains(decision, "uvicorn") {
				t.Fatalf("a declared start command still owes a decision: %q", decision)
			}
		}
	})
	t.Run("fly", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"Dockerfile": "FROM node:22\n",
			"fly.toml": "[http_service]\n  internal_port = 8080\n[[http_service.checks]]\n  path = \"/up\"\n" +
				"[processes]\n  app = \"node server.js\"\n  worker = \"node worker.js\"\n[deploy]\n  release_command = \"node migrate.js\"\n" +
				"[[mounts]]\n  source = \"data\"\n  destination = \"/data\"\n[env]\n  PHX_HOST = \"app.fly.dev\"\n  LOG_LEVEL = \"info\"\n",
		})
		candidate := selectedOf(result)
		manifest := platformManifest(candidate, "fly")
		if candidate.Port != 8080 || manifest == nil || manifest.HealthPath != "/up" || !slices.Equal(manifest.Volumes, []string{"/data"}) ||
			manifest.ReleaseCommand != "node migrate.js" {
			t.Fatalf("fly = %#v", candidate)
		}
		if host := variableNamed(candidate, "PHX_HOST"); host == nil || host.Example != "" {
			t.Fatalf("host-shaped value was imported: %#v", host)
		}
		if level := variableNamed(candidate, "LOG_LEVEL"); level == nil || level.Example != "info" {
			t.Fatalf("plain value was not kept as an example: %#v", level)
		}
	})
	t.Run("heroku app.json", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"package.json": expressManifest, "package-lock.json": "{}",
			"app.json": `{"env":{"SESSION_SECRET":{"generator":"secret"},"API_KEY":{"required":true},"WEB_CONCURRENCY":{"value":"2"}},"addons":["heroku-postgresql",{"plan":"heroku-redis:mini"}]}`,
		})
		candidate := selectedOf(result)
		manifest := platformManifest(candidate, "heroku")
		if manifest == nil || !slices.Equal(manifest.GeneratedVariables, []string{"SESSION_SECRET"}) || !slices.Equal(manifest.RequiredVariables, []string{"API_KEY"}) {
			t.Fatalf("heroku = %#v", manifest)
		}
		engines := []string{}
		for _, database := range candidate.Databases {
			engines = append(engines, database.Engine)
		}
		if !slices.Contains(engines, "postgres") || !slices.Contains(engines, "redis") {
			t.Fatalf("add-ons = %#v", candidate.Databases)
		}
	})
	t.Run("expo app.json is not heroku", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"package.json": expressManifest, "package-lock.json": "{}", "app.json": `{"expo":{"name":"x"}}`,
		})
		if platformManifest(selectedOf(result), "heroku") != nil {
			t.Fatal("an Expo config was read as a Heroku manifest")
		}
	})
	t.Run("netlify publish and redirects", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"package.json":      `{"name":"site","scripts":{"build":"vite build"},"devDependencies":{"vite":"6","@cloudflare/something":"1"}}`,
			"package-lock.json": "{}",
			"netlify.toml": "[build]\n  command = \"npm run build\"\n  publish = \"dist/client\"\n[build.environment]\n  NODE_VERSION = \"20\"\n" +
				"[[redirects]]\n  from = \"/old\"\n  to = \"/new\"\n  status = 301\n[[redirects]]\n  from = \"/*\"\n  to = \"/index.html\"\n  status = 200\n",
		})
		candidate := selectedOf(result)
		manifest := platformManifest(candidate, "netlify")
		if candidate.OutputDirectory != "dist/client" || manifest.Redirects != 1 || !slices.Equal(manifest.Toolchains, []string{"node 20"}) {
			t.Fatalf("netlify = %#v", candidate)
		}
		findings := repoShapeFindings(&result, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe}})
		if !hasFinding(findings, "static_redirects_unsupported", PreflightWarning) {
			t.Fatalf("findings = %#v", findings)
		}
	})
	t.Run("kamal", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"Dockerfile": "FROM ruby:3.3\nEXPOSE 80\n",
			"config/deploy.yml": `service: app
image: me/app
servers:
  web:
    - 192.168.0.1
  job:
    hosts:
      - 192.168.0.1
    cmd: bin/jobs
proxy:
  ssl: true
  host: app.example.com
  app_port: 80
  healthcheck:
    path: /up
env:
  secret:
    - RAILS_MASTER_KEY
  clear:
    SOLID_QUEUE_IN_PUMA: true
    DB_HOST: 192.168.0.2
volumes:
  - "app_storage:/rails/storage"
accessories:
  db:
    image: mysql:8.0
    host: 192.168.0.2
    port: "127.0.0.1:3306:3306"
    env:
      secret:
        - MYSQL_ROOT_PASSWORD <%= ENV["X"] %>
`,
		})
		candidate := selectedOf(result)
		manifest := platformManifest(candidate, "kamal")
		if manifest == nil || manifest.HealthPath != "/up" || !slices.Equal(manifest.RequiredVariables, []string{"RAILS_MASTER_KEY"}) ||
			!slices.Equal(manifest.Volumes, []string{"/rails/storage"}) {
			t.Fatalf("kamal = %#v", manifest)
		}
		if puma := variableNamed(candidate, "SOLID_QUEUE_IN_PUMA"); puma == nil || puma.Example != "true" {
			t.Fatalf("allowlisted toggle = %#v", puma)
		}
		if host := variableNamed(candidate, "DB_HOST"); host == nil || host.Example != "" {
			t.Fatalf("an accessory IP was imported: %#v", host)
		}
		if len(candidate.Processes) != 1 || candidate.Processes[0].Command != "bin/jobs" ||
			!slices.ContainsFunc(candidate.Databases, func(database DetectedDatabase) bool { return database.Engine == "mysql" }) {
			t.Fatalf("roles and accessories = %#v %#v", candidate.Processes, candidate.Databases)
		}
	})
	t.Run("nixpacks system packages", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"requirements.txt": "flask==3.1\n", "app.py": "from flask import Flask\napp = Flask(__name__)\n",
			"nixpacks.toml": "[phases.setup]\naptPkgs = [\"libpq-dev\"]\n[start]\ncmd = \"gunicorn app:app\"\n",
		})
		candidate := selectedOf(result)
		if candidate.StartCommand != "gunicorn app:app" {
			t.Fatalf("nixpacks start = %q", candidate.StartCommand)
		}
		findings := repoShapeFindings(&result, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe}})
		if !hasFinding(findings, "platform_system_packages_ignored", PreflightWarning) {
			t.Fatalf("findings = %#v", findings)
		}
	})
	t.Run("procfile outranks a platform file", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"requirements.txt": "flask==3.1\n", "Procfile": "web: gunicorn wsgi:app\n",
			"railway.json": `{"deploy":{"startCommand":"python app.py","healthcheckPath":"/health"}}`,
		})
		if selectedOf(result).StartCommand != "gunicorn wsgi:app" {
			t.Fatalf("start = %q", selectedOf(result).StartCommand)
		}
	})
	t.Run("a start command for another package manager is not taken", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"package.json": expressManifest, "package-lock.json": "{}",
			"railway.toml": "[deploy]\nstartCommand = \"yarn start\"\n",
		})
		candidate := selectedOf(result)
		if candidate.StartCommand != "npm run start" || !evidenceMentions(candidate, "lockfile builds with npm") {
			t.Fatalf("candidate = %#v", candidate)
		}
	})
	t.Run("digitalocean app spec for a subdirectory", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"api/package.json": expressManifest, "api/package-lock.json": "{}",
			".do/app.yaml": "services:\n  - name: api\n    source_dir: api\n    run_command: node server.js\n    http_port: 8080\n" +
				"workers:\n  - name: queue\n    source_dir: api\n    run_command: node queue.js\n",
		})
		candidate := selectedOf(result)
		if candidate.Root != "api" || candidate.StartCommand != "node server.js" || candidate.Port != 8080 ||
			len(candidate.Processes) != 1 || candidate.Processes[0].Name != "queue" {
			t.Fatalf("app spec = %#v", candidate)
		}
	})
	t.Run("hugging face space", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"requirements.txt": "streamlit==1.40\n", "dashboard.py": "import streamlit as st\n",
			"README.md": "---\ntitle: Demo\nsdk: streamlit\napp_file: dashboard.py\n---\n# Demo\n",
		})
		candidate := selectedOf(result)
		if !strings.HasPrefix(candidate.StartCommand, "streamlit run dashboard.py") {
			t.Fatalf("space = %#v", candidate)
		}
	})
	t.Run("vercel output and rewrites", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"package.json": `{"name":"site","scripts":{"build":"vite build"},"devDependencies":{"vite":"6"}}`, "package-lock.json": "{}",
			"vercel.json": `{"outputDirectory":"build","rewrites":[{"source":"/(.*)","destination":"/index.html"}]}`,
		})
		candidate := selectedOf(result)
		if candidate.OutputDirectory != "build" || !candidate.SPAFallback {
			t.Fatalf("vercel = %#v", candidate)
		}
	})
}

func TestPlatformBuildCommandsLoseTheirInstallSteps(t *testing.T) {
	for input, want := range map[string]string{
		"npm install && npm run build":                         "npm run build",
		"pip install -r requirements.txt":                      "",
		"yarn; yarn build":                                     "yarn build",
		"bundle install && bundle exec rake assets:precompile": "bundle exec rake assets:precompile",
		"API_TOKEN=plain-secret npm run build":                 "",
	} {
		if got := cleanPlatformCommand(input, true); got != want {
			t.Fatalf("cleanPlatformCommand(%q) = %q, want %q", input, got, want)
		}
	}
}
