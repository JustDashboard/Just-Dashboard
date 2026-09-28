package deploy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// detectFixture detects a checkout of the files, with scripts executable as
// a repository commits them, and refuses a result that would not save.
func detectFixture(t *testing.T, files map[string]string) DetectionResult {
	t.Helper()
	root := t.TempDir()
	for path, content := range files {
		writeBuildFixture(t, root, path, content)
		if strings.HasSuffix(path, ".sh") || strings.HasPrefix(filepath.Base(path), "docker-entrypoint") ||
			strings.HasPrefix(path, "bin/") {
			if err := os.Chmod(filepath.Join(root, path), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	result, err := (Detector{}).DetectPath(context.Background(), root, SourceIdentity{})
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if err := validateDetectionResult(&DraftSourceConfig{Kind: SourceLocal}, withLocalSource(result)); err != nil {
		t.Fatalf("detection result does not validate: %v", err)
	}
	return result
}

func withLocalSource(result DetectionResult) DetectionResult {
	result.Source.Kind = SourceLocal
	return result
}

func fixtureCandidate(t *testing.T, result DetectionResult, method BuildMethod) DetectedCandidate {
	t.Helper()
	for _, candidate := range result.Candidates {
		if candidate.BuildMethod == method {
			return candidate
		}
	}
	t.Fatalf("no %s candidate in %+v", method, result.Candidates)
	return DetectedCandidate{}
}

const (
	railsRoutes = `Rails.application.routes.draw do
  # Reveal health status on /up that returns 200 if the app boots with no exceptions.
  get "up" => "rails/health#show", as: :rails_health_check
  root "posts#index"
end
`
	rails71Production = `Rails.application.configure do
  # config.assume_ssl = true
  config.force_ssl = true
end
`
	rails8Production = `Rails.application.configure do
  config.assume_ssl = true
  config.force_ssl = true
  config.hosts << "shop.example.com"
end
`
	minimalRailsDockerfile = "FROM ruby:3.3-slim\nWORKDIR /rails\nCOPY . .\nEXPOSE 3000\nCMD [\"./bin/rails\", \"server\"]\n"
	nodeLock               = `{}`
)

// Every stack's readiness as detection proposes it: the path, whether any
// answer counts, where it came from, and the budget.
func TestDetectedReadinessFollowsWhatTheSourceDeclares(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name      string
		files     map[string]string
		method    BuildMethod
		kind      string
		path      string
		any       bool
		source    string
		attempts  int
		interval  int
		evidence  string
		nilResult bool
	}{
		{name: "rails 7.1 dockerfile uses /up", method: BuildDockerfile,
			files: map[string]string{"Dockerfile": minimalRailsDockerfile, "config/routes.rb": railsRoutes, "config/environments/production.rb": rails71Production},
			kind:  "http", path: "/up", source: readinessFromFramework, evidence: "Rails health route"},
		{name: "rails docker-entrypoint prepares the database first", method: BuildDockerfile,
			files: map[string]string{"Dockerfile": "FROM ruby:3.3-slim\nEXPOSE 80\nENTRYPOINT [\"/rails/bin/docker-entrypoint\"]\nCMD [\"./bin/thrust\", \"./bin/rails\", \"server\"]\n",
				"config/routes.rb": railsRoutes},
			kind: "http", path: "/up", source: readinessFromFramework, attempts: 40, interval: 3},
		{name: "rails api without a health route answers anything", method: BuildDockerfile,
			files: map[string]string{"Dockerfile": minimalRailsDockerfile, "config/application.rb": "module Api\n  class Application < Rails::Application\n    config.api_only = true\n  end\nend\n"},
			kind:  "http", path: "/", any: true, source: readinessFromConvention},
		{name: "kamal healthcheck wins", method: BuildDockerfile,
			files: map[string]string{"Dockerfile": minimalRailsDockerfile, "config/routes.rb": railsRoutes, "config/deploy.yml": "service: shop\nproxy:\n  ssl: true\n  healthcheck:\n    interval: 3\n    path: /healthz\n"},
			kind:  "http", path: "/healthz", source: readinessFromPlatform, evidence: "Kamal"},
		{name: "dockerfile HEALTHCHECK on the served port", method: BuildDockerfile,
			files: map[string]string{"Dockerfile": "FROM node:22\nEXPOSE 8080\nHEALTHCHECK --interval=10s CMD curl -fsS http://localhost:8080/healthz || exit 1\nCMD [\"node\",\"server.js\"]\n"},
			kind:  "http", path: "/healthz", source: readinessFromHealthcheck},
		{name: "dockerfile HEALTHCHECK through PORT", method: BuildDockerfile,
			files: map[string]string{"Dockerfile": "FROM node:22\nEXPOSE 3000\nHEALTHCHECK CMD wget -qO- http://127.0.0.1:${PORT}/api/health\n"},
			kind:  "http", path: "/api/health", source: readinessFromHealthcheck},
		{name: "dockerfile HEALTHCHECK start period extends the budget", method: BuildDockerfile,
			files: map[string]string{"Dockerfile": "FROM eclipse-temurin:21\nEXPOSE 8080\nHEALTHCHECK --start-period=120s CMD curl -f http://localhost:8080/health\n"},
			kind:  "http", path: "/health", source: readinessFromHealthcheck, attempts: 50, interval: 3},
		{name: "dockerfile HEALTHCHECK command is Docker health", method: BuildDockerfile,
			files: map[string]string{"Dockerfile": "FROM node:22\nEXPOSE 3000\nHEALTHCHECK --interval=30s --retries=3 \\\n  CMD node healthcheck.js\n"},
			kind:  "docker_health", source: readinessFromHealthcheck, attempts: 44, interval: 3},
		{name: "dockerfile HEALTHCHECK on another port is Docker health", method: BuildDockerfile,
			files: map[string]string{"Dockerfile": "FROM node:22\nEXPOSE 8080\nHEALTHCHECK --interval=5s CMD curl -f http://localhost:9000/health\n"},
			kind:  "docker_health", source: readinessFromHealthcheck},
		{name: "only the final stage's HEALTHCHECK counts", method: BuildDockerfile,
			files:     map[string]string{"Dockerfile": "FROM node:22 AS build\nHEALTHCHECK CMD curl -f http://localhost:3000/health\nFROM nginx\nEXPOSE 80\n"},
			nilResult: true},
		{name: "dockerfile inherits the recipe's any-answer for an API", method: BuildDockerfile,
			files: map[string]string{"Dockerfile": "FROM python:3.13\nEXPOSE 8000\n", "requirements.txt": "fastapi==0.115.0\nuvicorn==0.30.0\n",
				"main.py": "from fastapi import FastAPI\napp = FastAPI()\n\n@app.get(\"/items\")\ndef items():\n    return []\n"},
			kind: "http", path: "/", any: true, source: readinessFromConvention},
		{name: "fly.toml check path", method: BuildRecipe,
			files: map[string]string{"go.mod": "module example.com/api\n\ngo 1.25\n", "main.go": "package main\nfunc main() {}\n",
				"fly.toml": "app = \"api\"\n[http_service]\n  internal_port = 8080\n[[http_service.checks]]\n  interval = \"10s\"\n  path = \"/status\"\n"},
			kind: "http", path: "/status", source: readinessFromPlatform, evidence: "fly.toml"},
		{name: "go health route in code answers anything", method: BuildRecipe,
			files: map[string]string{"go.mod": "module example.com/api\n\ngo 1.25\n",
				"main.go": "package main\nimport \"net/http\"\nfunc main() {\n\thttp.HandleFunc(\"GET /healthz\", ok)\n\thttp.ListenAndServe(\":8080\", nil)\n}\n"},
			kind: "http", path: "/healthz", any: true, source: readinessFromCode},
		{name: "go without a health route answers anything at /", method: BuildRecipe,
			files: map[string]string{"go.mod": "module example.com/api\n\ngo 1.25\n", "main.go": "package main\nfunc main() {}\n"},
			kind:  "http", path: "/", any: true, source: readinessFromConvention},
		{name: "go beside another stack's health route keeps its own", method: BuildRecipe,
			files: map[string]string{"go.mod": "module example.com/api\n\ngo 1.25\n", "main.go": "package main\nfunc main() {}\n",
				"config/routes.rb": railsRoutes, "scripts/probe.py": "@app.get(\"/health\")\ndef h(): pass\n"},
			kind: "http", path: "/", any: true, source: readinessFromConvention},
		{name: "laravel 11 health route", method: BuildRecipe,
			files: map[string]string{"composer.json": `{"require":{"laravel/framework":"^11.0"}}`, "composer.lock": "{}", "public/index.php": "<?php",
				"bootstrap/app.php": "<?php\nreturn Application::configure(basePath: dirname(__DIR__))\n    ->withRouting(\n        web: __DIR__.'/../routes/web.php',\n        health: '/up',\n    )->create();\n"},
			kind: "http", path: "/up", source: readinessFromFramework, attempts: 40, interval: 3},
		{name: "spring actuator with a context path", method: BuildRecipe,
			files: map[string]string{"pom.xml": "<project><dependencies><dependency><artifactId>spring-boot-starter-web</artifactId></dependency><dependency><artifactId>spring-boot-starter-actuator</artifactId></dependency></dependencies><parent><artifactId>spring-boot-starter-parent</artifactId></parent></project>",
				"src/main/resources/application.yml": "server:\n  servlet:\n    context-path: /shop\n"},
			kind: "http", path: "/shop/actuator/health", source: readinessFromFramework, attempts: 40, interval: 3},
		{name: "spring actuator under a context path placeholder takes its default", method: BuildRecipe,
			files: map[string]string{"pom.xml": "<project>spring-boot spring-boot-starter-web spring-boot-starter-actuator</project>",
				"src/main/resources/application.properties": "server.port=${PORT:8080}\nserver.servlet.context-path=${CONTEXT_PATH:/api}\n"},
			kind: "http", path: "/api/actuator/health", source: readinessFromFramework, attempts: 40, interval: 3},
		{name: "spring actuator under a placeholder with no default answers anything", method: BuildRecipe,
			files: map[string]string{"pom.xml": "<project>spring-boot spring-boot-starter-web spring-boot-starter-actuator</project>",
				"src/main/resources/application.properties": "server.servlet.context-path=${CONTEXT_PATH}\n"},
			kind: "http", path: "/", any: true, source: readinessFromConvention, attempts: 40, interval: 3, evidence: "no default"},
		{name: "spring actuator ignores a later profile document", method: BuildRecipe,
			files: map[string]string{"pom.xml": "<project>spring-boot spring-boot-starter-web spring-boot-starter-actuator</project>",
				"src/main/resources/application.yml": "---\nspring:\n  application:\n    name: shop\n---\nspring:\n  config:\n    activate:\n      on-profile: local\nserver:\n  servlet:\n    context-path: /local\n"},
			kind: "http", path: "/actuator/health", source: readinessFromFramework, attempts: 40, interval: 3},
		{name: "spring actuator ignores a later properties document", method: BuildRecipe,
			files: map[string]string{"pom.xml": "<project>spring-boot spring-boot-starter-web spring-boot-starter-actuator</project>",
				"src/main/resources/application.properties": "management.endpoints.web.base-path=/manage\n#---\nspring.config.activate.on-profile=local\nmanagement.endpoints.web.base-path=/local\n"},
			kind: "http", path: "/manage/health", source: readinessFromFramework, attempts: 40, interval: 3},
		{name: "spring webflux actuator under its base path", method: BuildRecipe,
			files: map[string]string{"pom.xml": "<project>spring-boot spring-boot-starter-webflux spring-boot-starter-actuator</project>",
				"src/main/resources/application.properties": "spring.webflux.base-path=/svc\n"},
			kind: "http", path: "/svc/actuator/health", source: readinessFromFramework, attempts: 40, interval: 3},
		{name: "spring health route in code under the context path", method: BuildRecipe,
			files: map[string]string{"pom.xml": "<project>spring-boot spring-boot-starter-web</project>",
				"src/main/resources/application.properties":       "server.servlet.context-path=/api\n",
				"src/main/java/com/example/HealthController.java": "@RestController\nclass HealthController {\n  @GetMapping(\"/health\")\n  String health() { return \"ok\"; }\n}\n"},
			kind: "http", path: "/api/health", any: true, source: readinessFromCode, attempts: 40, interval: 3},
		{name: "spring REST API with a root context path", method: BuildRecipe,
			files: map[string]string{"pom.xml": "<project>spring-boot spring-boot-starter-web</project>",
				"src/main/resources/application.properties": "server.servlet.context-path=/\n"},
			kind: "http", path: "/", any: true, source: readinessFromConvention, attempts: 40, interval: 3},
		{name: "spring actuator behind spring security answers anything", method: BuildRecipe,
			files: map[string]string{"pom.xml": "<project>spring-boot spring-boot-starter-actuator spring-boot-starter-security</project>"},
			kind:  "http", path: "/actuator/health", any: true, source: readinessFromFramework, attempts: 40, interval: 3},
		{name: "spring REST API without actuator answers anything", method: BuildRecipe,
			files: map[string]string{"pom.xml": "<project>spring-boot spring-boot-starter-web</project>"},
			kind:  "http", path: "/", any: true, source: readinessFromConvention, attempts: 40, interval: 3},
		{name: "quarkus smallrye health", method: BuildRecipe,
			files: map[string]string{"pom.xml": "<project>io.quarkus quarkus-rest quarkus-smallrye-health</project>"},
			kind:  "http", path: "/q/health/ready", source: readinessFromFramework, attempts: 40, interval: 3},
		{name: "aspnet MapHealthChecks", method: BuildRecipe,
			files: map[string]string{"Api.csproj": `<Project Sdk="Microsoft.NET.Sdk.Web"><PropertyGroup><TargetFramework>net9.0</TargetFramework></PropertyGroup></Project>`,
				"Program.cs": "var app = builder.Build();\napp.MapHealthChecks(\"/healthz\");\napp.Run();\n"},
			kind: "http", path: "/healthz", source: readinessFromFramework},
		{name: "aspnet webapi without a health endpoint answers anything", method: BuildRecipe,
			files: map[string]string{"Api.csproj": `<Project Sdk="Microsoft.NET.Sdk.Web"><PropertyGroup><TargetFramework>net9.0</TargetFramework></PropertyGroup></Project>`,
				"Program.cs": "app.MapGet(\"/weatherforecast\", () => 1);\n"},
			kind: "http", path: "/", any: true, source: readinessFromConvention},
		{name: "next app route handler", method: BuildRecipe,
			files: map[string]string{"package.json": `{"scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"16.0.0"}}`, "package-lock.json": nodeLock,
				"src/app/api/health/route.ts": "export function GET() { return Response.json({ ok: true }) }\n"},
			kind: "http", path: "/api/health", source: readinessFromFramework, evidence: "Next.js route handler"},
		{name: "next route group and basePath", method: BuildRecipe,
			files: map[string]string{"package.json": `{"scripts":{"build":"next build"},"dependencies":{"next":"16.0.0"}}`, "package-lock.json": nodeLock,
				"next.config.ts":                "const config = { basePath: '/docs', output: 'standalone' }\nexport default config\n",
				"app/(system)/healthz/route.ts": "export const GET = () => new Response('ok')\n"},
			kind: "http", path: "/docs/healthz", source: readinessFromFramework},
		{name: "next without a health route asks for its page", method: BuildRecipe,
			files: map[string]string{"package.json": `{"scripts":{"build":"next build"},"dependencies":{"next":"16.0.0","next-intl":"4.0.0"}}`, "package-lock.json": nodeLock},
			kind:  "http", path: "/", source: readinessFromConvention},
		{name: "a next client call is not a route", method: BuildRecipe,
			files: map[string]string{"package.json": `{"scripts":{"build":"next build"},"dependencies":{"next":"16.0.0","axios":"1.7.0"}}`, "package-lock.json": nodeLock,
				"lib/status.ts": "export const status = () => api.get('/api/health')\n"},
			kind: "http", path: "/", source: readinessFromConvention},
		{name: "next behind clerk answers anything", method: BuildRecipe,
			files: map[string]string{"package.json": `{"scripts":{"build":"next build"},"dependencies":{"next":"16.0.0","@clerk/nextjs":"6.0.0"}}`, "package-lock.json": nodeLock},
			kind:  "http", path: "/", any: true, source: readinessFromConvention, evidence: "Clerk"},
		{name: "sveltekit endpoint", method: BuildRecipe,
			files: map[string]string{"package.json": `{"scripts":{"build":"vite build"},"devDependencies":{"@sveltejs/kit":"2","@sveltejs/adapter-node":"5","vite":"6"}}`, "package-lock.json": nodeLock,
				"src/routes/(api)/health/+server.ts": "export const GET = () => new Response('ok')\n"},
			kind: "http", path: "/health", source: readinessFromFramework},
		{name: "nuxt server route with a method suffix", method: BuildRecipe,
			files: map[string]string{"package.json": `{"scripts":{"build":"nuxt build"},"dependencies":{"nuxt":"^3.15.0"}}`, "package-lock.json": nodeLock,
				"server/api/health.get.ts": "export default defineEventHandler(() => 'ok')\n"},
			kind: "http", path: "/api/health", source: readinessFromFramework},
		{name: "express route in code", method: BuildRecipe,
			files: map[string]string{"package.json": `{"scripts":{"start":"node src/server.js"},"dependencies":{"express":"^5.0.0"}}`, "package-lock.json": nodeLock,
				"src/server.js": "const app = express()\napp.get('/healthz', (req, res) => res.send('ok'))\napp.listen(process.env.PORT)\n"},
			kind: "http", path: "/healthz", any: true, source: readinessFromCode},
		{name: "express API without a health route", method: BuildRecipe,
			files: map[string]string{"package.json": `{"scripts":{"start":"node index.js"},"dependencies":{"express":"^5.0.0"}}`, "package-lock.json": nodeLock},
			kind:  "http", path: "/", any: true, source: readinessFromConvention},
		{name: "nest terminus under a global prefix", method: BuildRecipe,
			files: map[string]string{"package.json": `{"scripts":{"build":"nest build","start:prod":"node dist/main"},"dependencies":{"@nestjs/core":"^11.0.0","@nestjs/terminus":"^11.0.0"}}`, "package-lock.json": nodeLock,
				"src/main.ts":                     "const app = await NestFactory.create(AppModule)\napp.setGlobalPrefix('api')\nawait app.listen(3000)\n",
				"src/health/health.controller.ts": "@Controller('health')\nexport class HealthController {\n  @Get()\n  @HealthCheck()\n  check() {}\n}\n"},
			kind: "http", path: "/api/health", any: true, source: readinessFromCode},
		{name: "nest health handler on a root controller", method: BuildRecipe,
			files: map[string]string{"package.json": `{"scripts":{"build":"nest build","start:prod":"node dist/main"},"dependencies":{"@nestjs/core":"^11.0.0"}}`, "package-lock.json": nodeLock,
				"src/app.controller.ts": "@Controller()\nexport class AppController {\n  @Get('healthz')\n  health() { return 'ok' }\n}\n"},
			kind: "http", path: "/healthz", any: true, source: readinessFromCode},
		{name: "strapi health endpoint", method: BuildRecipe,
			files: map[string]string{"package.json": `{"scripts":{"build":"strapi build","start":"strapi start"},"dependencies":{"@strapi/strapi":"5.0.0"}}`, "package-lock.json": nodeLock},
			kind:  "http", path: "/_health", source: readinessFromFramework},
		{name: "fastapi without a health route answers anything", method: BuildRecipe,
			files: map[string]string{"requirements.txt": "fastapi==0.115.0\nuvicorn==0.30.0\n", "main.py": "from fastapi import FastAPI\napp = FastAPI()\n@app.get(\"/items\")\ndef items(): return []\n"},
			kind:  "http", path: "/", any: true, source: readinessFromConvention},
		{name: "fastapi health route", method: BuildRecipe,
			files: map[string]string{"requirements.txt": "fastapi==0.115.0\n", "app/main.py": "from fastapi import FastAPI\napp = FastAPI()\n\n@app.get(\"/health\")\nasync def health():\n    return {\"ok\": True}\n"},
			kind:  "http", path: "/health", any: true, source: readinessFromCode},
		{name: "flask routing its page", method: BuildRecipe,
			files: map[string]string{"requirements.txt": "flask==3.0.0\n", "app.py": "from flask import Flask\napp = Flask(__name__)\n\n@app.route(\"/\")\ndef index():\n    return 'hi'\n"},
			kind:  "http", path: "/", source: readinessFromConvention},
		{name: "django admin only", method: BuildRecipe,
			files: map[string]string{"requirements.txt": "django==5.2\n", "manage.py": "import os\nos.environ.setdefault('DJANGO_SETTINGS_MODULE', 'mysite.settings')\n",
				"mysite/wsgi.py": "os.environ.setdefault('DJANGO_SETTINGS_MODULE', 'mysite.settings')\n", "mysite/settings.py": "ROOT_URLCONF = 'mysite.urls'\n",
				"mysite/urls.py": "urlpatterns = [\n    path('admin/', admin.site.urls),\n    path('api/', include('api.urls')),\n]\n"},
			kind: "http", path: "/admin/login/", source: readinessFromFramework, attempts: 40, interval: 3},
		{name: "django startproject documents a root route it does not have", method: BuildRecipe,
			files: map[string]string{"requirements.txt": "django==5.2\n", "manage.py": "", "mysite/wsgi.py": "", "mysite/settings.py": "# path('', views.home)\n",
				"mysite/urls.py": "\"\"\"\nURL configuration for mysite project.\n\nFunction views\n    2. Add a URL to urlpatterns:  path('', views.home, name='home')\n\"\"\"\nfrom django.contrib import admin\nfrom django.urls import path\n\nurlpatterns = [\n    path('admin/', admin.site.urls),\n]\n"},
			kind: "http", path: "/admin/login/", source: readinessFromFramework, attempts: 40, interval: 3},
		{name: "django health check include", method: BuildRecipe,
			files: map[string]string{"requirements.txt": "django==5.2\n", "manage.py": "", "mysite/wsgi.py": "", "mysite/settings.py": "",
				"mysite/urls.py": "urlpatterns = [path('admin/', admin.site.urls), path('ht/', include('health_check.urls'))]\n"},
			kind: "http", path: "/ht/", source: readinessFromFramework, attempts: 40, interval: 3},
		{name: "gradio downloads a model at start", method: BuildRecipe,
			files: map[string]string{"requirements.txt": "gradio==5.0.0\ntransformers==4.45.0\ntorch==2.4.0\n",
				"app.py": "import gradio as gr\nfrom transformers import pipeline\n\nsummarizer = pipeline(\"summarization\")\n\ndemo = gr.Interface(fn=summarizer, inputs='text', outputs='text')\ndemo.launch()\n"},
			kind: "http", path: "/", source: readinessFromConvention, attempts: 60, interval: 10},
		{name: "a model loaded on request keeps the default budget", method: BuildRecipe,
			files: map[string]string{"requirements.txt": "gradio==5.0.0\ntransformers==4.45.0\n",
				"app.py": "import gradio as gr\nfrom transformers import pipeline\n\ndef summarize(text):\n    return pipeline(\"summarization\")(text)\n\ngr.Interface(fn=summarize, inputs='text', outputs='text').launch()\n"},
			kind: "http", path: "/", source: readinessFromConvention},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			candidate := fixtureCandidate(t, detectFixture(t, fixture.files), fixture.method)
			readiness := candidate.Readiness
			if fixture.nilResult {
				if readiness != nil {
					t.Fatalf("readiness = %+v", readiness)
				}
				return
			}
			if readiness == nil {
				t.Fatalf("no readiness for %+v", candidate)
			}
			if readiness.Kind != fixture.kind || readiness.Path != fixture.path || readiness.AcceptAnyAnswer != fixture.any ||
				readiness.Source != fixture.source || readiness.Attempts != fixture.attempts || readiness.IntervalSeconds != fixture.interval ||
				!strings.Contains(readiness.Evidence, fixture.evidence) {
				t.Fatalf("readiness = %+v", readiness)
			}
		})
	}
}

// The settings that decide how an application answers a request that is
// not the proxy's — its host allowlist and its HTTPS redirect — are read so
// preflight can say what will refuse the probe.
func TestDetectedReadinessReadsHostAndHTTPSSettings(t *testing.T) {
	t.Parallel()
	rails := fixtureCandidate(t, detectFixture(t, map[string]string{
		"Dockerfile": minimalRailsDockerfile, "config/routes.rb": railsRoutes, "config/environments/production.rb": rails71Production,
	}), BuildDockerfile).Readiness
	if rails == nil || rails.HTTPSRedirect != "config.force_ssl in config/environments/production.rb" || rails.HTTPSRedirectIgnoresProxy {
		t.Fatalf("rails 7.1 = %+v", rails)
	}
	rails8 := fixtureCandidate(t, detectFixture(t, map[string]string{
		"Dockerfile": minimalRailsDockerfile, "config/routes.rb": railsRoutes, "config/environments/production.rb": rails8Production,
	}), BuildDockerfile).Readiness
	if rails8 == nil || rails8.HTTPSRedirect != "" || strings.Join(rails8.AllowedHosts, ",") != "shop.example.com" ||
		rails8.AllowedHostsSource != "config.hosts in config/environments/production.rb" {
		t.Fatalf("rails 8 = %+v", rails8)
	}
	django := fixtureCandidate(t, detectFixture(t, map[string]string{
		"requirements.txt": "django==5.2\ngunicorn==23.0\n", "manage.py": "os.environ.setdefault('DJANGO_SETTINGS_MODULE', 'config.settings.local')\n",
		"config/wsgi.py":                "os.environ.setdefault(\"DJANGO_SETTINGS_MODULE\", \"config.settings.production\")\n",
		"config/settings/base.py":       "ROOT_URLCONF = 'config.urls'\nALLOWED_HOSTS = []\n",
		"config/settings/local.py":      "from .base import *\nALLOWED_HOSTS = ['*']\n",
		"config/settings/production.py": "from .base import *\nALLOWED_HOSTS = [\n    'app.example.com',\n    '.example.org',\n]\nSECURE_SSL_REDIRECT = True\n",
		"config/urls.py":                "urlpatterns = [path('', views.home), path('admin/', admin.site.urls)]\n",
	}), BuildRecipe)
	readiness := django.Readiness
	if readiness == nil || readiness.Path != "/" || readiness.AcceptAnyAnswer || readiness.RootRoute != "routed" ||
		strings.Join(readiness.AllowedHosts, ",") != "app.example.com,.example.org" ||
		readiness.AllowedHostsSource != "ALLOWED_HOSTS in config/settings/production.py" ||
		readiness.HTTPSRedirect != "SECURE_SSL_REDIRECT in config/settings/production.py" || !readiness.HTTPSRedirectIgnoresProxy {
		t.Fatalf("django = %+v", readiness)
	}
	// An allowlist read from the environment is the operator's to set.
	dynamic := fixtureCandidate(t, detectFixture(t, map[string]string{
		"requirements.txt": "django==5.2\n", "manage.py": "", "mysite/wsgi.py": "",
		"mysite/settings.py": "ALLOWED_HOSTS = os.environ.get('ALLOWED_HOSTS', '').split(',')\nSECURE_SSL_REDIRECT = True\nSECURE_PROXY_SSL_HEADER = ('HTTP_X_FORWARDED_PROTO', 'https')\n",
	}), BuildRecipe).Readiness
	if dynamic == nil || dynamic.AllowedHostsSource != "" || dynamic.HTTPSRedirect == "" || dynamic.HTTPSRedirectIgnoresProxy {
		t.Fatalf("dynamic django = %+v", dynamic)
	}
	// The module wsgi.py names only imports its base, which holds the list.
	split := fixtureCandidate(t, detectFixture(t, map[string]string{
		"requirements.txt": "django==5.2\n", "manage.py": "",
		"mysite/wsgi.py":                "os.environ.setdefault('DJANGO_SETTINGS_MODULE', 'mysite.settings.production')\n",
		"mysite/settings/__init__.py":   "",
		"mysite/settings/base.py":       "ALLOWED_HOSTS = ['example.org']\n",
		"mysite/settings/production.py": "from .base import *  # noqa\n",
	}), BuildRecipe).Readiness
	if split == nil || strings.Join(split.AllowedHosts, ",") != "example.org" || split.AllowedHostsSource != "ALLOWED_HOSTS in mysite/settings/base.py" {
		t.Fatalf("split django = %+v", split)
	}
	// With DEBUG on, an empty list allows the local names.
	for settings, hosts := range map[string]string{
		"DEBUG = True\nALLOWED_HOSTS = []\n":                                ".localhost,127.0.0.1,[::1]",
		"DEBUG = False\nALLOWED_HOSTS = []\n":                               "",
		"DEBUG = os.environ.get('DEBUG') == '1'\nALLOWED_HOSTS = []\n":      "",
		"DEBUG = True  # local only\nALLOWED_HOSTS = ['app.example.com']\n": "app.example.com",
	} {
		readiness := fixtureCandidate(t, detectFixture(t, map[string]string{
			"requirements.txt": "django==5.2\n", "manage.py": "", "mysite/wsgi.py": "", "mysite/settings.py": settings,
		}), BuildRecipe).Readiness
		if readiness == nil || readiness.AllowedHostsSource == "" || strings.Join(readiness.AllowedHosts, ",") != hosts {
			t.Fatalf("%q: %+v", settings, readiness)
		}
	}
}

func TestDetectedReadinessRecordsTheModelCache(t *testing.T) {
	t.Parallel()
	readiness := fixtureCandidate(t, detectFixture(t, map[string]string{
		"requirements.txt": "fastapi==0.115.0\nsentence-transformers==3.0.0\n",
		"main.py":          "from fastapi import FastAPI\nfrom sentence_transformers import SentenceTransformer\n\napp = FastAPI()\nmodel = SentenceTransformer('all-MiniLM-L6-v2')\n",
	}), BuildRecipe).Readiness
	if readiness == nil || readiness.ModelDownload != "SentenceTransformer in main.py" || readiness.ModelCache != "/root/.cache/huggingface" ||
		readiness.Attempts != 60 || readiness.IntervalSeconds != 10 || !strings.Contains(readiness.SlowStart, "downloads a model") {
		t.Fatalf("readiness = %+v", readiness)
	}
	lifespan := fixtureCandidate(t, detectFixture(t, map[string]string{
		"requirements.txt": "fastapi==0.115.0\nopenai-whisper==20240930\n",
		"main.py":          "import whisper\nfrom fastapi import FastAPI\n\n@asynccontextmanager\nasync def lifespan(app):\n    app.state.model = whisper.load_model('base')\n    yield\n\napp = FastAPI(lifespan=lifespan)\n",
	}), BuildRecipe).Readiness
	if lifespan == nil || lifespan.ModelDownload != "whisper.load_model in main.py" || lifespan.ModelCache != "/root/.cache" {
		t.Fatalf("lifespan = %+v", lifespan)
	}
}

func TestIsHealthPath(t *testing.T) {
	t.Parallel()
	for route, want := range map[string]bool{
		"/health": true, "/healthz": true, "/api/health": true, "/api/v1/health": true, "/_health": true,
		"/livez": true, "/readyz": true, "/health/ready": true, "/up": true, "/ping": true,
		"/": false, "/users": false, "/api/v1/users/health": false, "/admin/health": false, "/healthy": false,
	} {
		if got := isHealthPath(route); got != want {
			t.Fatalf("isHealthPath(%q) = %t", route, got)
		}
	}
}

func TestFileRouteDropsGroupsAndRefusesDynamicSegments(t *testing.T) {
	t.Parallel()
	for input, want := range map[string]string{
		"api/health": "/api/health", "(system)/healthz": "/healthz", "@modal/health": "/health",
		"api/[id]/health": "", "health/index": "/health", "api/health/_index": "/api/health",
	} {
		if got := fileRoute(input); got != want {
			t.Fatalf("fileRoute(%q) = %q", input, got)
		}
	}
}

func TestValidateDetectedServingRefusesMalformedEvidence(t *testing.T) {
	t.Parallel()
	for name, candidate := range map[string]DetectedCandidate{
		"kind":        {Readiness: &DetectedReadiness{Kind: "tcp", Source: readinessFromCode}},
		"path":        {Readiness: &DetectedReadiness{Kind: "http", Path: "health", Source: readinessFromCode}},
		"query":       {Readiness: &DetectedReadiness{Kind: "http", Path: "/health?token=1", Source: readinessFromCode}},
		"source":      {Readiness: &DetectedReadiness{Kind: "http", Path: "/", Source: "guess"}},
		"budget":      {Readiness: &DetectedReadiness{Kind: "http", Path: "/", Source: readinessFromCode, Attempts: 61}},
		"docker path": {Readiness: &DetectedReadiness{Kind: "docker_health", Path: "/", Source: readinessFromHealthcheck}},
		"newline":     {Readiness: &DetectedReadiness{Kind: "http", Path: "/", Source: readinessFromCode, Evidence: "a\nb"}},
		"worker":      {BackgroundWorker: &DetectedBackgroundWorker{}},
		"detach":      {StartDetaches: &DetectedStartDetach{Command: "pm2 start", Effect: "maybe"}},
	} {
		if validateDetectedServing(candidate) == nil {
			t.Fatalf("%s: malformed evidence accepted", name)
		}
	}
	if err := validateDetectedServing(DetectedCandidate{Readiness: &DetectedReadiness{Kind: "http", Path: "/up", Source: readinessFromFramework, Evidence: "Rails health route"}}); err != nil {
		t.Fatal(err)
	}
}

// What a repository writes reaches a stored detection only in a shape its
// validation accepts: an unusable path gives way to the next source, and an
// allowlist entry that is not a host drops the allowlist.
func TestDetectedReadinessSanitizesWhatTheRepositoryWrote(t *testing.T) {
	t.Parallel()
	readiness := fixtureCandidate(t, detectFixture(t, map[string]string{
		"requirements.txt": "flask==3.0.0\n", "app.py": "from flask import Flask\napp = Flask(__name__)\n",
		"render.yaml": "services:\n  - type: web\n    healthCheckPath: /health?probe=render\n",
		"settings.py": "ALLOWED_HOSTS = ['token=abc', 'app.example.com']\n",
	}), BuildRecipe).Readiness
	if readiness == nil || readiness.Source != readinessFromConvention || readiness.AllowedHostsSource != "" || readiness.AllowedHosts != nil {
		t.Fatalf("readiness = %+v", readiness)
	}
	candidate := fixtureCandidate(t, detectFixture(t, map[string]string{
		"package.json": `{"scripts":{"build":"next build"},"dependencies":{"next":"16.0.0"}}`, "package-lock.json": nodeLock,
		"next.config.js": "module.exports = { basePath: '/my docs' }\n",
	}), BuildRecipe)
	if candidate.Readiness != nil {
		t.Fatalf("unusable basePath kept: %+v", candidate.Readiness)
	}
}

// A root whose files the scanner did not all read is marked, so nothing is
// concluded from a fact it did not find; a nested root's unread file is that
// root's alone.
func TestReadinessScannerMarksRootsItDidNotReadCompletely(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeBuildFixture(t, root, "bot/index.js", "client.login(token)\n")
	scanner := newReadinessScanner()
	scanner.sourceFiles = readinessSourceMaxFiles
	scanner.visit(filepath.Join(root, "bot", "index.js"), "bot/index.js")
	roots := []string{"", "bot"}
	if !scanner.forRoot("bot", roots).has(factSourceUnread) || scanner.forRoot("", roots).has(factSourceUnread) {
		t.Fatalf("unread = %v", scanner.unread)
	}
	stopped := newReadinessScanner()
	stopped.walkStopped = true
	if !stopped.forRoot("", roots).has(factSourceUnread) {
		t.Fatal("a stopped walk read every file")
	}
	full := newReadinessScanner()
	for range readinessMaxFacts + 1 {
		full.add(readinessFact{file: "a.js", kind: factListen})
	}
	if !full.forRoot("bot", roots).has(factSourceUnread) {
		t.Fatal("a dropped fact went unnoticed")
	}
}

func TestReadinessSkippedPathKeepsJVMPackages(t *testing.T) {
	t.Parallel()
	for rel, skipped := range map[string]bool{
		"src/main/java/com/example/demo/HealthController.java": false,
		"api/src/main/kotlin/com/example/Health.kt":            false,
		"examples/basic/src/main/java/App.java":                true,
		"src/test/java/com/example/HealthTest.java":            true,
		"examples/server.js":                                   true,
		"src/server.ts":                                        false,
	} {
		if got := readinessSkippedPath(rel); got != skipped {
			t.Fatalf("%s: skipped %v", rel, got)
		}
	}
}
