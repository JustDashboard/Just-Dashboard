package deploy

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"
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

func candidateVariable(candidate *DetectedCandidate, name string) *DetectedVariable {
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
		if candidateVariable(candidate, "JWT_SECRET") == nil || candidateVariable(candidate, "DATABASE_URL") == nil {
			t.Fatalf("variables = %#v", candidate.Variables)
		}
		// render.yaml's generateValue is minted by the dashboard at commit.
		if secret := candidateVariable(candidate, "JWT_SECRET"); secret.Setup != "generate" || secret.GenerateLength == 0 {
			t.Fatalf("JWT_SECRET = %#v", secret)
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
		if host := candidateVariable(candidate, "PHX_HOST"); host == nil || host.Example != "" {
			t.Fatalf("host-shaped value was imported: %#v", host)
		}
		if level := candidateVariable(candidate, "LOG_LEVEL"); level == nil || level.Example != "info" {
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
				"[[redirects]]\n  from = \"/old\"\n  to = \"/new\"\n  status = 301\n[[redirects]]\n  from = \"/*\"\n  to = \"/index.html\"\n  status = 200\n" +
				"[[redirects]]\n  from = \"/blog/:year/:slug\"\n  to = \"/posts/:slug\"\n  status = 301\n",
		})
		candidate := selectedOf(result)
		manifest := platformManifest(candidate, "netlify")
		// The plain redirect is applied by the static server; the one with
		// placeholders is what is left out.
		if candidate.OutputDirectory != "dist/client" || manifest.Redirects != 0 || !slices.Equal(manifest.Toolchains, []string{"node 20"}) ||
			candidate.StaticSite == nil || candidate.StaticSite.HostingRules != 1 || candidate.StaticSite.HostingRulesLeftOut != 1 {
			t.Fatalf("netlify = %#v", candidate)
		}
		configuration := PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe, Recipe: "node", OutputDirectory: "dist/client"}}
		findings := append(repoShapeFindings(&result, configuration), staticSiteFindings(nil, &result, configuration)...)
		if !hasFinding(findings, "static_redirects_unsupported", PreflightWarning) || !hasFinding(findings, "static_hosting_rules", PreflightPass) {
			t.Fatalf("findings = %#v", findings)
		}
	})
	t.Run("a platform file the static server does not read", func(t *testing.T) {
		// Only a file the server read has its rules applied; one it did not
		// keeps its count, and preflight names it as unread.
		result := detectShapeFixture(t, map[string]string{"index.html": "<h1>x</h1>", "_redirects": "/old /new 301\n"})
		candidate := selectedOf(result)
		if manifest := platformManifest(candidate, "netlify"); manifest == nil || manifest.Redirects != 0 || candidate.StaticSite.HostingRules != 1 {
			t.Fatalf("read = %#v", candidate)
		}
		candidate.PlatformManifests = append(candidate.PlatformManifests, DetectedPlatformManifest{File: "infra/netlify.toml", Platform: "netlify", Redirects: 2})
		findings := repoShapeFindings(&result, PlanConfiguration{Build: BuildPlanConfig{Method: BuildStatic}})
		unread := findingByCode(findings, "static_redirects_unsupported")
		if unread == nil || unread.Measured != "2 rule(s) in infra/netlify.toml" || !strings.Contains(unread.Means, "infra/netlify.toml is not among the files it reads") ||
			strings.Contains(unread.Means, "single-page fallback only") {
			t.Fatalf("finding = %+v", unread)
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
		if puma := candidateVariable(candidate, "SOLID_QUEUE_IN_PUMA"); puma == nil || puma.Example != "true" {
			t.Fatalf("allowlisted toggle = %#v", puma)
		}
		if host := candidateVariable(candidate, "DB_HOST"); host == nil || host.Example != "" {
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

// A value left open reads in linear time and ends the document; it used to
// rescan the growing value on every line, seconds for one 64 KiB file.
func TestTOMLReaderStopsAtAnUnterminatedValue(t *testing.T) {
	for name, opening := range map[string]string{"array": `items = [`, "string": `text = """`} {
		t.Run(name, func(t *testing.T) {
			document := "[build]\ndockerfile = \"Dockerfile\"\n" + opening + "\n" + strings.Repeat("\"padding value\",\n", 4000) + "[after]\nkey = \"read from inside the value\"\n"
			started := time.Now()
			entries := readTOML([]byte(document))
			if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
				t.Fatalf("readTOML took %s", elapsed)
			}
			if tomlText(entries, "build", "dockerfile") != "Dockerfile" || tomlText(entries, "after", "key") != "" {
				t.Fatalf("entries = %#v", entries)
			}
		})
	}
	closed := readTOML([]byte("list = [\n" + strings.Repeat("\"x\",\n", 200) + "]\nnext = \"y\"\n"))
	if value, _ := tomlLookup(closed, "", "list"); len(value.list) != 200 || tomlText(closed, "", "next") != "y" {
		t.Fatalf("a long closed array = %#v", closed)
	}
}

func TestPlatformManifestsKeepWhatDetectionAdds(t *testing.T) {
	t.Run("the schema step stays in front of a platform start command", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"package.json":         `{"scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"16","@prisma/client":"7"},"devDependencies":{"prisma":"7"}}`,
			"package-lock.json":    "{}",
			"prisma/schema.prisma": "datasource db {\n  provider = \"postgresql\"\n}\n",
			"prisma/migrations/20240101000000_init/migration.sql": "",
			"render.yaml": "services:\n  - type: web\n    name: web\n    runtime: node\n    startCommand: npm start\n",
		})
		candidate := selectedOf(result)
		if candidate == nil || candidate.StartCommand != "npx prisma migrate deploy && npm start" {
			t.Fatalf("start = %#v", candidate)
		}
	})
	t.Run("a platform start that migrates itself is taken as it is", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"package.json":         `{"scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"16","@prisma/client":"7"},"devDependencies":{"prisma":"7"}}`,
			"package-lock.json":    "{}",
			"prisma/schema.prisma": "datasource db {\n  provider = \"postgresql\"\n}\n",
			"railway.json":         `{"deploy":{"startCommand":"npx prisma db push && npm start"}}`,
		})
		if candidate := selectedOf(result); candidate == nil || candidate.StartCommand != "npx prisma db push && npm start" {
			t.Fatalf("start = %#v", candidate)
		}
	})
	t.Run("odd names and long commands cost only themselves", func(t *testing.T) {
		result := detectShapeFixture(t, map[string]string{
			"requirements.txt": "flask==3.1.0\n", "app.py": "from flask import Flask\napp = Flask(__name__)\n",
			"fly.toml": "[processes]\n  app = \"gunicorn app:app\"\n  \"queue worker\" = \"python worker.py\"\n  jobs = \"python jobs.py\"\n" +
				"[[http_service.checks]]\n  path = \"/" + strings.Repeat("h", 900) + "\"\n",
			"Procfile": "web: gunicorn app:app\nclock: python " + strings.Repeat("x", 2000) + ".py\n",
		}, SourceIdentity{Kind: SourceGit})
		candidate := selectedOf(result)
		if candidate == nil {
			t.Fatalf("candidates = %#v", result.Candidates)
		}
		names := []string{}
		for _, process := range candidate.Processes {
			names = append(names, process.Name)
		}
		if !slices.Equal(names, []string{"jobs"}) {
			t.Fatalf("processes = %#v", candidate.Processes)
		}
		if err := validateDetectionResult(&DraftSourceConfig{Kind: SourceGit, Mode: SourceModeLocalCheckout}, result); err != nil {
			t.Fatalf("detection refused its own result: %v", err)
		}
	})
	t.Run("past the deadline the files are left unread", func(t *testing.T) {
		scan := newRepoShapeScan(t.TempDir(), DetectionLimits{})
		scan.recordFile("", "fly.toml", []byte("[http_service]\ninternal_port = 8080\n"))
		result := DetectionResult{Candidates: []DetectedCandidate{{ID: "a", Root: "", BuildMethod: BuildRecipe, Profile: ProfileWeb}}}
		expired, cancel := context.WithCancel(t.Context())
		cancel()
		scan.applyPlatformManifests(&result, shapeContext{ctx: expired, markers: map[string]*detectedMarkers{}})
		if len(result.Candidates[0].PlatformManifests) != 0 || !result.Truncated || result.TruncatedReason != "time limit reached" {
			t.Fatalf("result = %#v", result)
		}
	})
}

// Another platform's start command is applied before each root's passes
// read the candidate, so the listener and readiness facts are the ones of
// the command the plan runs.
func TestPlatformStartCommandIsWhatThePassesRead(t *testing.T) {
	result := detectFixture(t, map[string]string{
		"requirements.txt": "fastapi==0.115\nuvicorn==0.30\n", "main.py": "from fastapi import FastAPI\napp = FastAPI()\n",
		"render.yaml": "services:\n  - type: web\n    name: api\n    runtime: python\n" +
			"    startCommand: uvicorn main:app --host 127.0.0.1 --port $PORT\n",
	})
	candidate := selectedOf(result)
	if candidate == nil || candidate.StartCommand != "uvicorn main:app --host 127.0.0.1 --port $PORT" {
		t.Fatalf("candidate = %#v", candidate)
	}
	if candidate.Listen == nil || candidate.Listen.Loopback != "127.0.0.1" || !candidate.Listen.LoopbackCertain {
		t.Fatalf("listen = %#v, want the platform command's loopback bind", candidate.Listen)
	}
}

// What another platform's file says the application keeps and needs is
// planned, not only listed: its volumes become state to keep where that
// platform kept it, and the values it asks for at creation are required.
func TestPlatformVolumesAndRequiredVariablesReachThePlan(t *testing.T) {
	t.Parallel()
	storageAt := func(candidate *DetectedCandidate, location string) *DetectedPersistentPath {
		for index := range candidate.PersistentPaths {
			if candidate.PersistentPaths[index].Path == location {
				return &candidate.PersistentPaths[index]
			}
		}
		return nil
	}
	t.Run("fly mounts", func(t *testing.T) {
		t.Parallel()
		candidate := selectedOf(detectShapeFixture(t, map[string]string{
			"package.json": expressManifest, "package-lock.json": "{}", "server.js": "require('express')().listen(process.env.PORT)\n",
			"fly.toml": "[[mounts]]\n  source = \"data\"\n  destination = \"/data\"\n",
		}))
		entry := storageAt(candidate, "/data")
		if entry == nil || entry.Kind != PersistentStorage || entry.Target != "/data" || entry.Source != "fly.toml" ||
			!strings.Contains(entry.Reason, "fly.toml mounts a volume at /data") {
			t.Fatalf("persistent paths = %+v", candidate.PersistentPaths)
		}
	})
	t.Run("render disk and sync false", func(t *testing.T) {
		t.Parallel()
		candidate := selectedOf(detectShapeFixture(t, map[string]string{
			"requirements.txt": "fastapi==0.115\nuvicorn==0.30\n", "main.py": "from fastapi import FastAPI\napp = FastAPI()\n",
			"render.yaml": "services:\n  - type: web\n    name: api\n    runtime: python\n    startCommand: uvicorn main:app --host 0.0.0.0 --port $PORT\n" +
				"    disk:\n      name: data\n      mountPath: /var/data\n      sizeGB: 1\n    envVars:\n      - key: STRIPE_KEY\n        sync: false\n      - key: LOG_LEVEL\n        value: info\n",
		}))
		if entry := storageAt(candidate, "/var/data"); entry == nil || entry.Target != "/var/data" {
			t.Fatalf("persistent paths = %+v", candidate.PersistentPaths)
		}
		if stripe := candidateVariable(candidate, "STRIPE_KEY"); stripe == nil || !stripe.Required {
			t.Fatalf("STRIPE_KEY = %+v", stripe)
		}
		if level := candidateVariable(candidate, "LOG_LEVEL"); level == nil || level.Required {
			t.Fatalf("LOG_LEVEL = %+v", level)
		}
	})
	t.Run("railway required mount path and app.json required", func(t *testing.T) {
		t.Parallel()
		candidate := selectedOf(detectShapeFixture(t, map[string]string{
			"package.json": expressManifest, "package-lock.json": "{}", "server.js": "require('express')().listen(process.env.PORT)\n",
			"railway.json": `{"deploy":{"startCommand":"node server.js","requiredMountPath":"/app/storage"}}`,
			"app.json":     `{"env":{"API_KEY":{"required":true},"WEB_CONCURRENCY":{"value":"2"}}}`,
		}))
		if entry := storageAt(candidate, "/app/storage"); entry == nil || entry.Target != "/app/storage" || entry.Source != "railway.json" {
			t.Fatalf("persistent paths = %+v", candidate.PersistentPaths)
		}
		if key := candidateVariable(candidate, "API_KEY"); key == nil || !key.Required {
			t.Fatalf("API_KEY = %+v", key)
		}
	})
	t.Run("a volume already planned there is not planned twice", func(t *testing.T) {
		t.Parallel()
		candidate := selectedOf(detectShapeFixture(t, map[string]string{
			"Dockerfile": "FROM node:22\nWORKDIR /app\nCOPY . .\nVOLUME /data\nCMD [\"node\", \"server.js\"]\n",
			"fly.toml":   "[[mounts]]\n  source = \"data\"\n  destination = \"/data\"\n",
		}))
		targets := 0
		for _, entry := range candidate.PersistentPaths {
			if entry.Target == "/data" {
				targets++
			}
		}
		if targets != 1 {
			t.Fatalf("persistent paths = %+v", candidate.PersistentPaths)
		}
	})
	t.Run("an unprivileged runtime cannot write a volume the image does not prepare", func(t *testing.T) {
		t.Parallel()
		candidate := selectedOf(detectShapeFixture(t, map[string]string{
			"go.mod": "module example.com/api\n\ngo 1.25\n", "main.go": "package main\n\nfunc main() {}\n",
			"fly.toml": "[[mounts]]\n  source = \"data\"\n  destination = \"/data\"\n",
		}))
		entry := storageAt(candidate, "/data")
		if entry == nil || entry.Target != "" {
			t.Fatalf("persistent paths = %+v", candidate.PersistentPaths)
		}
		// Named, so preflight still says what the platform kept there.
		findings := persistentStateFindings(candidate, PlanConfiguration{}, nil)
		if len(findings) == 0 || findings[0].Code != "persistent_path_unmounted" {
			t.Fatalf("findings = %+v", findings)
		}
	})
}
