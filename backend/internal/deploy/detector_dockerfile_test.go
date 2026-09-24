package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const nextManifest = `{"name":"web","scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"16.0.0","react":"19.0.0"}}`

func detectFixture(t *testing.T, files map[string]string) DetectionResult {
	t.Helper()
	root := t.TempDir()
	for relative, content := range files {
		writeBuildFixture(t, root, relative, content)
		if strings.HasSuffix(relative, ".sh") || strings.HasPrefix(filepath.Base(relative), "docker-entrypoint") ||
			strings.HasPrefix(relative, "bin/") {
			if err := os.Chmod(filepath.Join(root, relative), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	result, err := (Detector{}).DetectPath(t.Context(), root, SourceIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	if err := validateDetectionResult(&DraftSourceConfig{}, result); err != nil {
		t.Fatalf("detection result does not validate: %v\n%+v", err, result)
	}
	return result
}

func selectedFixtureCandidate(t *testing.T, result DetectionResult) DetectedCandidate {
	t.Helper()
	for _, candidate := range result.Candidates {
		if candidate.ID == result.SelectedID {
			return candidate
		}
	}
	t.Fatalf("nothing selected (%q) among %+v", result.SelectionReason, result.Candidates)
	return DetectedCandidate{}
}

func fixtureIssue(candidate DetectedCandidate, code string) (ImageBuildIssue, bool) {
	for _, issue := range candidate.ImageBuildIssues {
		if issue.Code == code {
			return issue, true
		}
	}
	return ImageBuildIssue{}, false
}

func TestDetectionResolvesDockerfileAndRecipeTies(t *testing.T) {
	for _, fixture := range []struct {
		name       string
		files      map[string]string
		method     BuildMethod
		dockerfile string
		reason     string
	}{
		{
			"a production Dockerfile beside Next.js is the repository's own build",
			map[string]string{"package.json": nextManifest, "bun.lock": "{}", "Dockerfile": "FROM oven/bun:1\nCOPY . .\nRUN bun install\nCMD [\"bun\", \"run\", \"start\"]\n"},
			BuildDockerfile, "Dockerfile", "the repository's own Dockerfile builds it as written",
		},
		{
			"the incident: competing lockfiles leave only the Dockerfile buildable",
			map[string]string{"package.json": nextManifest, "bun.lock": "{}", "package-lock.json": "{}", "Dockerfile": "FROM oven/bun:1\nCOPY . .\nRUN bun install --frozen-lockfile\n"},
			BuildDockerfile, "Dockerfile", "needs a package manager chosen",
		},
		{
			"a Dockerfile copying a file the checkout lacks loses to the recipe",
			map[string]string{"package.json": nextManifest, "bun.lock": "{}", "Dockerfile": "FROM node:22\nCOPY .env.production .\nCOPY . .\n"},
			BuildRecipe, "", "not in the build context",
		},
		{
			"a Dockerfile that runs a dev server loses to the Django recipe",
			map[string]string{
				"requirements.txt": "django==5.2\n", "manage.py": "", "mysite/wsgi.py": "application = None\n",
				"Dockerfile": "FROM python:3.13\nCOPY . .\nCMD python manage.py runserver 0.0.0.0:8000\n",
			},
			BuildRecipe, "", "development server",
		},
		{
			"a Dockerfile without the ARG a public variable needs loses to the recipe",
			map[string]string{"package.json": nextManifest, "bun.lock": "{}", ".env.example": "NEXT_PUBLIC_API_URL=http://localhost:4000\n", "Dockerfile": "FROM oven/bun:1\nCOPY . .\n"},
			BuildRecipe, "", "declares no ARG for NEXT_PUBLIC_API_URL",
		},
		{
			"docker/Dockerfile built from the repository root beats a medium Express recipe",
			map[string]string{
				"package.json":      `{"name":"api","scripts":{"start":"node index.js"},"dependencies":{"express":"5"}}`,
				"package-lock.json": "{}", "index.js": "",
				"docker/Dockerfile": "FROM node:22\nWORKDIR /app\nCOPY package.json package-lock.json ./\nRUN npm ci\nCOPY . .\nCMD [\"node\", \"index.js\"]\n",
			},
			BuildDockerfile, "docker/Dockerfile", "",
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			result := detectFixture(t, fixture.files)
			selected := selectedFixtureCandidate(t, result)
			if selected.BuildMethod != fixture.method || (fixture.dockerfile != "" && selected.Dockerfile != fixture.dockerfile) {
				t.Fatalf("selected %s %q, want %s %q (%s)", selected.BuildMethod, selected.Dockerfile, fixture.method, fixture.dockerfile, result.SelectionReason)
			}
			if !strings.Contains(result.SelectionReason, fixture.reason) {
				t.Fatalf("reason = %q, want it to say %q", result.SelectionReason, fixture.reason)
			}
			if result.Candidates[0].ID != result.SelectedID {
				t.Fatalf("the selected candidate is not listed first: %+v", result.Candidates)
			}
		})
	}
}

func TestDetectionFindsDockerfileVariantsAndContexts(t *testing.T) {
	result := detectFixture(t, map[string]string{
		"Dockerfile.prod": "FROM nginx\nCOPY site/ /usr/share/nginx/html/\nEXPOSE 80\n",
		"Dockerfile.dev":  "FROM node:22\nCMD [\"npm\", \"run\", \"dev\"]\n",
		"site/index.html": "<html></html>",
	})
	selected := selectedFixtureCandidate(t, result)
	if selected.Dockerfile != "Dockerfile.prod" || selected.DockerfileRole != DockerfileRoleProduction || selected.Port != 80 {
		t.Fatalf("selected = %+v", selected)
	}
	for _, candidate := range result.Candidates {
		if candidate.Dockerfile == "Dockerfile.dev" && (candidate.DockerfileRole != DockerfileRoleDevelopment || candidate.Confidence != ConfidenceLow) {
			t.Fatalf("development Dockerfile = %+v", candidate)
		}
	}

	only := detectFixture(t, map[string]string{"Dockerfile.dev": "FROM node:22\n"})
	if only.SelectedID != "" || !strings.Contains(only.SelectionReason, "written for development") {
		t.Fatalf("a development Dockerfile was chosen on its own: %+v", only)
	}

	layout := detectFixture(t, map[string]string{
		"go.mod":                   "module example.com/app\n\ngo 1.26\n",
		"main.go":                  "package main\nfunc main() {}\n",
		"build/package/Dockerfile": "FROM golang:1.26\nCOPY go.mod ./\nCOPY . .\n",
		"build/output.txt":         "ignored",
	})
	found := false
	for _, candidate := range layout.Candidates {
		if candidate.BuildMethod == BuildDockerfile {
			found = candidate.Root == "" && candidate.Dockerfile == "build/package/Dockerfile"
		}
	}
	if !found {
		t.Fatalf("build/package/Dockerfile was not found with the root context: %+v", layout.Candidates)
	}

	tooling := detectFixture(t, map[string]string{
		".devcontainer/Dockerfile":        "FROM mcr.microsoft.com/devcontainers/base\n",
		".devcontainer/compose.yaml":      "services:\n  app:\n    build: .\n",
		".github/actions/lint/Dockerfile": "FROM alpine\n",
		"package.json":                    nextManifest,
		"bun.lock":                        "{}",
	})
	if len(tooling.Candidates) != 1 || tooling.Candidates[0].BuildMethod != BuildRecipe {
		t.Fatalf("tooling Dockerfiles became candidates: %+v", tooling.Candidates)
	}

	composed := detectFixture(t, map[string]string{
		"deploy/app.Dockerfile": "FROM node:22 AS base\nCOPY package.json .\nFROM base AS production\nCMD [\"node\", \"server.js\"]\nFROM base AS development\nCMD [\"npm\", \"run\", \"dev\"]\n",
		"package.json":          `{"name":"svc"}`,
		"docker-compose.yml":    "services:\n  web:\n    build:\n      context: .\n      dockerfile: deploy/app.Dockerfile\n      target: production\n  db:\n    image: postgres:16\n",
	})
	for _, candidate := range composed.Candidates {
		if candidate.BuildMethod != BuildDockerfile {
			continue
		}
		if candidate.Root != "" || candidate.Dockerfile != "deploy/app.Dockerfile" || candidate.DockerfileTarget != "production" {
			t.Fatalf("Compose-referenced Dockerfile = %+v", candidate)
		}
		if _, dev := fixtureIssue(candidate, "dockerfile_dev_server"); dev {
			t.Fatalf("the production target was read as a dev server: %+v", candidate.ImageBuildIssues)
		}
	}
}

func TestDockerfileExposedPortResolution(t *testing.T) {
	for _, fixture := range []struct {
		name, dockerfile string
		port             int
		reason           string
	}{
		{"literal", "FROM node\nEXPOSE 8080\n", 8080, "EXPOSE 8080/tcp"},
		{"argument default", "FROM node\nARG PORT=3000\nEXPOSE $PORT\n", 3000, "EXPOSE $PORT = 3000"},
		{"env default", "FROM python\nENV PORT=8000\nEXPOSE ${PORT}\n", 8000, "EXPOSE ${PORT} = 8000"},
		{"global argument redeclared", "ARG PORT=4000\nFROM node\nARG PORT\nEXPOSE ${PORT}\n", 4000, ""},
		{"inspector beside the server", "FROM node\nEXPOSE 3000 9229\n", 3000, ""},
		{"inherited from the base stage", "FROM node AS base\nEXPOSE 3000\nFROM base\nCMD [\"node\"]\n", 3000, ""},
		{"unresolved", "FROM node\nEXPOSE $PORT\n", 0, ""},
		{"several", "FROM nginx\nEXPOSE 80 443\n", 0, ""},
		{"earlier stage only", "FROM node AS build\nEXPOSE 3000\nFROM nginx\n", 0, ""},
		{"target stage", "FROM node AS production\nEXPOSE 3000\nFROM node AS dev\nEXPOSE 5173\n", 3000, ""},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			model := modelDockerfile([]byte(fixture.dockerfile))
			port, reason := model.exposedPort(model.preferredTarget())
			if port != fixture.port || !strings.Contains(reason, fixture.reason) {
				t.Fatalf("port = %d (%q), want %d (%q)", port, reason, fixture.port, fixture.reason)
			}
		})
	}
}

func TestDockerfileStaticBuildabilityIssues(t *testing.T) {
	for _, fixture := range []struct {
		name     string
		files    map[string]string
		code     string
		severity PreflightSeverity
		detail   string
	}{
		{"missing COPY source", map[string]string{"Dockerfile": "FROM node\nCOPY .env ./\n"}, "dockerfile_copy_source_missing", PreflightBlocked, "line 2 COPY .env: not in the build context ."},
		{"missing glob", map[string]string{"Dockerfile": "FROM node\nCOPY *.csproj ./\n"}, "dockerfile_copy_source_missing", PreflightBlocked, "*.csproj"},
		{"ignored COPY source", map[string]string{"Dockerfile": "FROM node\nCOPY dist/ ./\n", "dist/index.js": "", ".dockerignore": "node_modules\ndist\n"}, "dockerfile_copy_ignored", PreflightBlocked, "excluded by .dockerignore rule dist"},
		{"Dockerfile-specific ignore file", map[string]string{"Dockerfile": "FROM node\nCOPY secret.txt ./\n", "secret.txt": "x", "Dockerfile.dockerignore": "*.txt\n"}, "dockerfile_copy_ignored", PreflightBlocked, "Dockerfile.dockerignore"},
		{"argument in FROM without default", map[string]string{"Dockerfile": "ARG RUBY_VERSION\nFROM ruby:${RUBY_VERSION}-slim\n"}, "dockerfile_arg_required", PreflightBlocked, "RUBY_VERSION"},
		{"ssh mount", map[string]string{"Dockerfile": "FROM node\nRUN --mount=type=ssh git clone git@github.com:o/r.git\n"}, "dockerfile_ssh_mount", PreflightBlocked, "line 2"},
		{"standalone output missing", map[string]string{
			"Dockerfile":     "FROM node AS builder\nCOPY . .\nFROM node\nCOPY --from=builder /app/.next/standalone ./\n",
			"next.config.ts": "export default { reactStrictMode: true }\n", "package.json": nextManifest,
		}, "dockerfile_standalone_missing", PreflightBlocked, "output: 'standalone'"},
		{"refused literal", map[string]string{"Dockerfile": "FROM node\nENV API_TOKEN=ghp_abcdef0123456789\n"}, "dockerfile_refused", PreflightBlocked, "line 2 (ENV) sets a literal value for API_TOKEN"},
		{"entrypoint with CRLF", map[string]string{"Dockerfile": "FROM alpine\nWORKDIR /app\nCOPY . .\nENTRYPOINT [\"./docker-entrypoint.sh\"]\n", "docker-entrypoint.sh": "#!/bin/sh\r\nexec \"$@\"\r\n"}, "script_crlf", PreflightBlocked, "Windows line endings"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			result := detectFixture(t, fixture.files)
			for _, candidate := range result.Candidates {
				if candidate.BuildMethod != BuildDockerfile {
					continue
				}
				issue, found := fixtureIssue(candidate, fixture.code)
				if !found || issue.Severity != fixture.severity || !strings.Contains(issue.Detail, fixture.detail) {
					t.Fatalf("issues = %+v, want %s %s %q", candidate.ImageBuildIssues, fixture.code, fixture.severity, fixture.detail)
				}
				return
			}
			t.Fatalf("no Dockerfile candidate: %+v", result.Candidates)
		})
	}

	root := t.TempDir()
	writeBuildFixture(t, root, "Dockerfile", "FROM alpine\nCOPY start.sh /usr/local/bin/\nCOPY run.sh /usr/local/bin/\nRUN chmod +x /usr/local/bin/run.sh\nENTRYPOINT [\"start.sh\"]\n")
	writeBuildFixture(t, root, "start.sh", "#!/bin/sh\nexec \"$@\"\n")
	writeBuildFixture(t, root, "run.sh", "#!/bin/sh\n")
	result, err := (Detector{}).DetectPath(t.Context(), root, SourceIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	issue, found := fixtureIssue(result.Candidates[0], "script_not_executable")
	if !found || issue.Subject != "start.sh" || issue.Severity != PreflightBlocked {
		t.Fatalf("exec bit: %+v", result.Candidates[0].ImageBuildIssues)
	}
	if err := os.Chmod(filepath.Join(root, "start.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	result, _ = (Detector{}).DetectPath(t.Context(), root, SourceIdentity{})
	if len(result.Candidates[0].ImageBuildIssues) != 0 {
		t.Fatalf("an executable, chmod-ed tree still has issues: %+v", result.Candidates[0].ImageBuildIssues)
	}

	rails := detectFixture(t, map[string]string{
		"Dockerfile":            railsDockerfile,
		"Gemfile":               "source 'https://rubygems.org'\ngem 'rails'\n",
		"Gemfile.lock":          "GEM\n  specs:\n    railties (8.0.1)\n",
		"bin/docker-entrypoint": "#!/bin/bash -e\nexec \"${@}\"\n",
		"bin/thrust":            "#!/usr/bin/env ruby\n",
		"bin/rails":             "#!/usr/bin/env ruby\n",
	})
	selected := selectedFixtureCandidate(t, rails)
	if selected.Framework != "rails" || selected.Port != 80 || len(selected.ImageBuildIssues) != 0 || selected.DockerfileRole != DockerfileRoleProduction {
		t.Fatalf("Rails 8 Dockerfile = %+v", selected)
	}
}

func TestDetectionReadsRepositoryComposeFiles(t *testing.T) {
	sail := detectFixture(t, map[string]string{
		"composer.json":      `{"require":{"php":"^8.2","laravel/framework":"^12.0"}}`,
		"artisan":            "",
		"public/index.php":   "<?php",
		"docker-compose.yml": "services:\n  laravel.test:\n    build:\n      context: ./vendor/laravel/sail/runtimes/8.4\n    ports: ['80:80']\n  mysql:\n    image: 'mysql/mysql-server:8.0'\n  redis:\n    image: 'redis:alpine'\n  mailpit:\n    image: 'axllent/mailpit:latest'\n",
	})
	selected := selectedFixtureCandidate(t, sail)
	if selected.Recipe != "php" || !databaseSuggested(selected.Databases, "mysql") || !databaseSuggested(selected.Databases, "redis") {
		t.Fatalf("Laravel beside Sail = %+v", selected)
	}
	for _, candidate := range sail.Candidates {
		if candidate.BuildMethod == BuildCompose && candidate.Confidence != ConfidenceLow {
			t.Fatalf("Sail compose = %+v", candidate)
		}
	}

	symfony := detectFixture(t, map[string]string{
		"composer.json": `{"require":{"php":"^8.2","symfony/framework-bundle":"^7.2"}}`,
		"bin/console":   "", "public/index.php": "<?php",
		"compose.yaml": "services:\n  database:\n    image: postgres:${POSTGRES_VERSION:-16}-alpine\n    environment:\n      POSTGRES_DB: app\n",
	})
	if len(symfony.Candidates) != 1 || symfony.Candidates[0].BuildMethod != BuildRecipe {
		t.Fatalf("a Postgres-only compose.yaml became a candidate: %+v", symfony.Candidates)
	}

	nextWithDatabase := detectFixture(t, map[string]string{
		"package.json": nextManifest, "bun.lock": "{}",
		"docker-compose.yml": "services:\n  db:\n    image: postgres:16-alpine\n  cache:\n    image: redis:7\n",
	})
	selected = selectedFixtureCandidate(t, nextWithDatabase)
	if len(nextWithDatabase.Candidates) != 1 || !databaseSuggested(selected.Databases, "postgres") {
		t.Fatalf("Next.js beside a development database = %+v", nextWithDatabase.Candidates)
	}

	stack := detectFixture(t, map[string]string{
		"docker-compose.yml": "services:\n  app:\n    build: .\n    ports: ['8080:8080']\n  db:\n    image: postgres:16\n",
		"Dockerfile":         "FROM swift:6.0 AS build\nCOPY . .\nRUN swift build -c release\nFROM swift:6.0-slim\nEXPOSE 8080\nCMD [\"./App\", \"serve\", \"--hostname\", \"0.0.0.0\", \"--port\", \"8080\"]\n",
		"Package.swift":      `.package(url: "https://github.com/vapor/vapor.git", from: "4.99.0")`,
	})
	selected = selectedFixtureCandidate(t, stack)
	if selected.BuildMethod != BuildDockerfile || selected.Framework != "vapor" || selected.Port != 8080 {
		t.Fatalf("the Vapor template's Dockerfile was not selected over its compose file: %+v (%s)", selected, stack.SelectionReason)
	}

	onlyCompose := detectFixture(t, map[string]string{
		"docker-compose.yml": "services:\n  app:\n    image: ghcr.io/example/app:1\n    ports: ['8080:8080']\n  db:\n    image: postgres:16\n",
	})
	if selected := selectedFixtureCandidate(t, onlyCompose); selected.BuildMethod != BuildCompose {
		t.Fatalf("a Compose-only repository = %+v", selected)
	}

	swift := detectFixture(t, map[string]string{"Package.swift": `.package(url: "https://github.com/hummingbird-project/hummingbird.git", from: "2.0.0")`})
	if len(swift.Candidates) != 1 || swift.Candidates[0].Framework != "hummingbird" {
		t.Fatalf("a Swift server package without a Dockerfile = %+v", swift.Candidates)
	}
	if issue, found := fixtureIssue(swift.Candidates[0], "dockerfile_missing"); !found || issue.Severity != PreflightBlocked {
		t.Fatalf("Swift without a Dockerfile names no cause: %+v", swift.Candidates[0])
	}
}

func TestDetectionReadsDeclaredReleaseCommands(t *testing.T) {
	for _, fixture := range []struct {
		name    string
		files   map[string]string
		command string
	}{
		{"Procfile", map[string]string{"requirements.txt": "django==5.2\n", "manage.py": "", "Procfile": "web: gunicorn app.wsgi\nrelease: python manage.py migrate --noinput\n"}, "python manage.py migrate --noinput"},
		{"fly.toml", map[string]string{"Dockerfile": "FROM node\n", "fly.toml": "app = 'x'\n\n[deploy]\n  release_command = \"npx prisma migrate deploy\"\n"}, "npx prisma migrate deploy"},
		{"render.yaml", map[string]string{"Dockerfile": "FROM node\n", "render.yaml": "services:\n  - type: web\n    name: app\n    preDeployCommand: bundle exec rails db:migrate\n"}, "bundle exec rails db:migrate"},
		{"Phoenix release", map[string]string{
			"Dockerfile": "FROM elixir AS build\nCOPY mix.exs ./\nRUN mix release\nFROM debian\nWORKDIR /app\nCMD [\"/app/bin/server\"]\n",
			"mix.exs":    "defp deps do [{:phoenix, \"~> 1.7\"}] end", "rel/overlays/bin/migrate": "#!/bin/sh\n",
		}, "bin/migrate"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			result := detectFixture(t, fixture.files)
			if selected := selectedFixtureCandidate(t, result); selected.ReleaseCommand != fixture.command {
				t.Fatalf("release command = %q, want %q", selected.ReleaseCommand, fixture.command)
			}
		})
	}
}

func TestRecipeCandidatesCarryContextIssues(t *testing.T) {
	result := detectFixture(t, map[string]string{
		"package.json":      `{"name":"api","scripts":{"start":"./bin/start"},"dependencies":{"express":"5"}}`,
		"package-lock.json": "{}",
		".dockerignore":     "*\n!dist\n",
		"bin/start":         "#!/bin/sh\r\nnode index.js\r\n",
	})
	candidate := selectedFixtureCandidate(t, result)
	if issue, found := fixtureIssue(candidate, "dockerignore_drops_recipe_input"); !found || issue.Severity != PreflightWarning || !strings.Contains(issue.Detail, "rule *") {
		t.Fatalf("allowlist .dockerignore = %+v", candidate.ImageBuildIssues)
	}
	if issue, found := fixtureIssue(candidate, "script_crlf"); !found || issue.Subject != "bin/start" {
		t.Fatalf("CRLF start script = %+v", candidate.ImageBuildIssues)
	}

	php := detectFixture(t, map[string]string{"index.php": "<?php", "logs/.htaccess": "Require all denied\n"})
	if issue, found := fixtureIssue(selectedFixtureCandidate(t, php), "php_htaccess_ignored"); !found || issue.Subject != "logs/.htaccess" {
		t.Fatalf("PHP .htaccess = %+v", php.Candidates)
	}
}

func TestDetectionSurvivesRepositoryNamesShapedLikeCredentials(t *testing.T) {
	result := detectFixture(t, map[string]string{
		"docker-compose.yml": "services:\n  cache:\n    image: registry.example.com/api_token=abc:1\n  web:\n    image: postgres:16\n",
		"package.json":       nextManifest, "bun.lock": "{}",
		"Dockerfile":         "FROM node:22\nARG API_TOKEN_PORT=3000\nEXPOSE ${API_TOKEN_PORT}\n",
	})
	if len(result.Candidates) == 0 {
		t.Fatal("no candidates")
	}
}
