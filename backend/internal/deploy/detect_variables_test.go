package deploy

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func detectFixture(t *testing.T, files map[string]string) DetectionResult {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		writeBuildFixture(t, root, name, content)
	}
	result, err := (Detector{}).DetectPath(context.Background(), root, SourceIdentity{})
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	return result
}

func candidateByMethod(t *testing.T, result DetectionResult, method BuildMethod) DetectedCandidate {
	t.Helper()
	for _, candidate := range result.Candidates {
		if candidate.BuildMethod == method {
			return candidate
		}
	}
	t.Fatalf("no %s candidate in %+v", method, result.Candidates)
	return DetectedCandidate{}
}

func variableNamed(variables []DetectedVariable, name string) (DetectedVariable, bool) {
	for _, variable := range variables {
		if variable.Name == name {
			return variable, true
		}
	}
	return DetectedVariable{}, false
}

func noteCodes(notes []EnvironmentNote) []string {
	codes := []string{}
	for _, note := range notes {
		codes = append(codes, note.Code)
	}
	return codes
}

// Self-issued secrets arrive generated in their framework's shape, public
// URLs arrive bound to the planned domain, and a provider's credential —
// however much its name looks like a secret the application issues — is
// left for the operator.
func TestVariableClassificationPerFramework(t *testing.T) {
	t.Parallel()
	type expectation struct {
		setup, format, template, value string
		length                         int
	}
	for _, fixture := range []struct {
		name  string
		files map[string]string
		want  map[string]expectation
		// unclassified names must arrive with no setup at all.
		unclassified []string
	}{
		{
			name: "Next.js with Auth.js v5",
			files: map[string]string{
				"package.json":      `{"scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"16.0.0","next-auth":"5.0.0-beta.29","stripe":"18"}}`,
				"package-lock.json": `{}`,
				".env.example":      "AUTH_SECRET=\nAUTH_URL=http://localhost:3000\nAUTH_GITHUB_ID=\nAUTH_GITHUB_SECRET=\nSTRIPE_SECRET_KEY=\nSTRIPE_WEBHOOK_SECRET=\nSUPABASE_JWT_SECRET=\n",
			},
			want: map[string]expectation{
				"AUTH_SECRET":     {setup: "generate", format: "base64", length: 32},
				"AUTH_URL":        {setup: "domain", template: "{{scheme}}://{{hostname}}"},
				"AUTH_TRUST_HOST": {setup: "default", value: "true"},
			},
			unclassified: []string{"AUTH_GITHUB_ID", "AUTH_GITHUB_SECRET", "STRIPE_SECRET_KEY", "STRIPE_WEBHOOK_SECRET", "SUPABASE_JWT_SECRET"},
		},
		{
			name: "NextAuth v4 needs its secret and URL even when undocumented",
			files: map[string]string{
				"package.json":      `{"scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"14.2.0","next-auth":"^4.24.7"}}`,
				"package-lock.json": `{}`,
			},
			want: map[string]expectation{
				"NEXTAUTH_SECRET": {setup: "generate", format: "base64", length: 32},
				"NEXTAUTH_URL":    {setup: "domain", template: "{{scheme}}://{{hostname}}"},
			},
		},
		{
			name: "Laravel",
			files: map[string]string{
				"composer.json":      `{"require":{"php":"^8.2","laravel/framework":"^11.0"}}`,
				"composer.lock":      `{}`,
				"artisan":            "#!/usr/bin/env php\n",
				"public/index.php":   "<?php\n",
				".env.example":       "APP_KEY=\nAPP_URL=http://localhost\nLOG_LEVEL=debug\nDB_CONNECTION=sqlite\nSESSION_DRIVER=database\nAPP_ENV=local\n",
				"config/app.php":     "<?php return ['key' => env('APP_KEY')];\n",
				"config/logging.php": "<?php env('LOG_LEVEL', 'debug');\n",
			},
			want: map[string]expectation{
				"APP_KEY":        {setup: "generate", format: "laravel", length: 32},
				"APP_URL":        {setup: "domain", template: "{{scheme}}://{{hostname}}"},
				"LOG_LEVEL":      {setup: "default", value: "info"},
				"DB_CONNECTION":  {setup: "default", value: "sqlite"},
				"SESSION_DRIVER": {setup: "default", value: "database"},
			},
			unclassified: []string{"APP_ENV"},
		},
		{
			name: "Strapi",
			files: map[string]string{
				"package.json":      `{"scripts":{"build":"strapi build","start":"strapi start"},"dependencies":{"@strapi/strapi":"5.0.0"}}`,
				"package-lock.json": `{}`,
				"config/server.js":  "module.exports = ({ env }) => ({ app: { keys: env.array('APP_KEYS') } })\n",
				"config/admin.js":   "module.exports = ({ env }) => ({ auth: { secret: env('ADMIN_JWT_SECRET') }, apiToken: { salt: env('API_TOKEN_SALT') }, transfer: { token: { salt: env('TRANSFER_TOKEN_SALT') } } })\n",
			},
			want: map[string]expectation{
				"APP_KEYS":            {setup: "generate", format: "keylist", length: 16},
				"ADMIN_JWT_SECRET":    {setup: "generate", format: "base64", length: 16},
				"API_TOKEN_SALT":      {setup: "generate", format: "base64", length: 16},
				"TRANSFER_TOKEN_SALT": {setup: "generate", format: "base64", length: 16},
			},
		},
		{
			name: "Express sessions and JWTs",
			files: map[string]string{
				"package.json":      `{"scripts":{"start":"node server.js"},"dependencies":{"express":"5","express-session":"1","jsonwebtoken":"9"}}`,
				"package-lock.json": `{}`,
				"server.js":         "session({ secret: process.env.SESSION_SECRET }); jwt.sign(p, process.env.JWT_SECRET); process.env.GITHUB_CLIENT_SECRET\n",
			},
			want: map[string]expectation{
				"SESSION_SECRET": {setup: "generate", length: 64},
				"JWT_SECRET":     {setup: "generate", length: 64},
			},
			unclassified: []string{"GITHUB_CLIENT_SECRET"},
		},
		{
			name: "Django settings read the key and default DEBUG on",
			files: map[string]string{
				"requirements.txt":       "Django==5.2\n",
				"manage.py":              "import os\n",
				"mysite/settings.py":     "import os\nSECRET_KEY = os.environ['SECRET_KEY']\nDEBUG = os.environ.get('DEBUG', 'True') == 'True'\nALLOWED_HOSTS = os.environ.get('ALLOWED_HOSTS', '').split(',')\nCSRF_TRUSTED_ORIGINS = os.environ.get('CSRF_TRUSTED_ORIGINS', '').split(',')\n",
				"mysite/wsgi.py":         "application = get_wsgi_application()\n",
				"payments/stripe_api.py": "import os\nSTRIPE = os.environ.get('SECRET_KEY_STRIPE')\n",
			},
			want: map[string]expectation{
				"SECRET_KEY":           {setup: "generate", length: 50},
				"DEBUG":                {setup: "default", value: "False"},
				"ALLOWED_HOSTS":        {setup: "domain", template: "{{hostname}},localhost,127.0.0.1"},
				"CSRF_TRUSTED_ORIGINS": {setup: "domain", template: "{{scheme}}://{{hostname}}"},
			},
			unclassified: []string{"SECRET_KEY_STRIPE"},
		},
		{
			name: "SvelteKit on the Node adapter",
			files: map[string]string{
				"package.json":            `{"scripts":{"build":"vite build"},"devDependencies":{"@sveltejs/kit":"2","@sveltejs/adapter-node":"5","vite":"6"}}`,
				"package-lock.json":       `{}`,
				"svelte.config.js":        "import adapter from '@sveltejs/adapter-node'\n",
				"src/lib/server/db.ts":    "import { DATABASE_URL } from '$env/static/private'\n",
				"src/routes/+page.svelte": "<script>import { PUBLIC_SITE_NAME } from '$env/static/public'</script>\n",
			},
			want: map[string]expectation{
				"ORIGIN": {setup: "domain", template: "{{scheme}}://{{hostname}}"},
			},
		},
		{
			name: "a loopback example on the application's own port is its own address",
			files: map[string]string{
				"package.json":      `{"scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"16"}}`,
				"package-lock.json": `{}`,
				".env.example":      "NEXT_PUBLIC_WEB_URL=http://localhost:3000/app\nAPI_URL=http://localhost:8000\nDATABASE_URL=postgresql://localhost:5432/app\nREDIS_URL=redis://localhost:6379\n",
			},
			want: map[string]expectation{
				"NEXT_PUBLIC_WEB_URL": {setup: "domain", template: "{{scheme}}://{{hostname}}/app"},
			},
			unclassified: []string{"API_URL", "DATABASE_URL", "REDIS_URL"},
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			result := detectFixture(t, fixture.files)
			if len(result.Candidates) == 0 {
				t.Fatalf("no candidates")
			}
			variables := result.Candidates[0].Variables
			for name, want := range fixture.want {
				variable, ok := variableNamed(variables, name)
				if !ok {
					t.Fatalf("%s missing from %+v", name, variables)
				}
				got := expectation{setup: variable.Setup, format: variable.GenerateFormat, template: variable.DomainTemplate,
					value: variable.DefaultValue, length: variable.GenerateLength}
				if got != want {
					t.Fatalf("%s = %+v, want %+v", name, got, want)
				}
				if variable.SetupReason == "" {
					t.Fatalf("%s carries no reason", name)
				}
			}
			for _, name := range fixture.unclassified {
				variable, ok := variableNamed(variables, name)
				if !ok {
					t.Fatalf("%s missing", name)
				}
				if variable.Setup != "" {
					t.Fatalf("%s was classified %q; a provider credential or another service's address must be left alone", name, variable.Setup)
				}
			}
			for _, candidate := range result.Candidates {
				if err := validateDetectedEnvironment(candidate); err != nil {
					t.Fatalf("detected environment does not validate: %v", err)
				}
			}
		})
	}
}

// SvelteKit's static env modules fail the build without a value, so the
// name is required and build-phase; a public one is also compiled into the
// browser.
func TestVariablePhaseRequirednessAndBrowserInlining(t *testing.T) {
	t.Parallel()
	result := detectFixture(t, map[string]string{
		"package.json":            `{"scripts":{"build":"vite build"},"devDependencies":{"@sveltejs/kit":"2","@sveltejs/adapter-node":"5","vite":"6"}}`,
		"package-lock.json":       `{}`,
		"src/lib/server/db.ts":    "import { DATABASE_URL, SESSION_KEY as key } from '$env/static/private'\nimport { env } from '$env/dynamic/private'\nenv.RUNTIME_TOKEN\n",
		"src/routes/+page.svelte": "<script>import { PUBLIC_SITE_NAME } from '$env/static/public'</script>\n",
		"src/lib/client.ts":       "import.meta.env.VITE_API_URL; import.meta.env.MODE; import.meta.env.BASE_URL; import.meta.env.DEV\n",
	})
	candidate := result.Candidates[0]
	if !reflect.DeepEqual(candidate.BrowserPrefixes, []string{"VITE_", "PUBLIC_"}) {
		t.Fatalf("prefixes = %v", candidate.BrowserPrefixes)
	}
	for name, want := range map[string][3]bool{
		// required, build, inlined
		"DATABASE_URL":     {true, true, false},
		"SESSION_KEY":      {true, true, false},
		"PUBLIC_SITE_NAME": {true, true, true},
		"VITE_API_URL":     {false, true, true},
		"RUNTIME_TOKEN":    {false, false, false},
	} {
		variable, ok := variableNamed(candidate.Variables, name)
		if !ok {
			t.Fatalf("%s missing from %+v", name, candidate.Variables)
		}
		got := [3]bool{variable.Required, variable.Phase == "build", variable.BrowserInlined}
		if got != want {
			t.Fatalf("%s: required/build/inlined = %v, want %v", name, got, want)
		}
	}
	prisma := detectFixture(t, map[string]string{
		"package.json":      `{"scripts":{"build":"prisma generate && next build","start":"next start"},"dependencies":{"next":"16","@prisma/client":"7"},"devDependencies":{"prisma":"7"}}`,
		"package-lock.json": `{}`,
		"prisma.config.ts":  "import { defineConfig, env } from 'prisma/config'\nexport default defineConfig({ datasource: { url: env('DATABASE_URL') } })\n",
	})
	if variable, _ := variableNamed(prisma.Candidates[0].Variables, "DATABASE_URL"); !variable.Required || variable.Phase != "build" {
		t.Fatalf("Prisma 7's config needs DATABASE_URL while the build runs: %+v", variable)
	}
	for _, builtin := range []string{"MODE", "BASE_URL", "DEV"} {
		if _, ok := variableNamed(candidate.Variables, builtin); ok {
			t.Fatalf("Vite's own %s was listed as a variable", builtin)
		}
	}
}

// The Google AI Studio export compiles a server key into the bundle through
// vite's define block; next.config's env block does the same.
func TestBundlerDefinesMarkVariablesInlined(t *testing.T) {
	t.Parallel()
	vite := detectFixture(t, map[string]string{
		"package.json":      `{"scripts":{"build":"vite build"},"devDependencies":{"vite":"6"}}`,
		"package-lock.json": `{}`,
		"vite.config.ts":    "export default defineConfig(({ mode }) => { const env = loadEnv(mode, '.', ''); return { define: { 'process.env.GEMINI_API_KEY': JSON.stringify(env.GEMINI_API_KEY) } } })\n",
		"index.html":        "<div id=root></div>",
	})
	variable, ok := variableNamed(vite.Candidates[0].Variables, "GEMINI_API_KEY")
	if !ok || !variable.BrowserInlined || variable.Phase != "build" {
		t.Fatalf("GEMINI_API_KEY = %+v", variable)
	}
	next := detectFixture(t, map[string]string{
		"package.json":      `{"scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"16"}}`,
		"package-lock.json": `{}`,
		"next.config.mjs":   "export default { env: { MAPS_KEY: process.env.MAPS_KEY }, images: { domains: [process.env.CDN_HOST] } }\n",
	})
	maps, _ := variableNamed(next.Candidates[0].Variables, "MAPS_KEY")
	cdn, _ := variableNamed(next.Candidates[0].Variables, "CDN_HOST")
	if !maps.BrowserInlined || maps.Phase != "build" || cdn.BrowserInlined || cdn.Phase != "build" {
		t.Fatalf("MAPS_KEY = %+v, CDN_HOST = %+v", maps, cdn)
	}
}

// HOST is a bind address when it is read beside a listen; otherwise it is a
// name like any other, because some applications build public URLs from it.
func TestHostVariableIsABindAddressOnlyWhereItIsListenedOn(t *testing.T) {
	t.Parallel()
	bind := detectFixture(t, map[string]string{
		"go.mod":  "module example.com/app\n\ngo 1.25\n",
		"main.go": "package main\nimport (\"net/http\"; \"os\")\nfunc main() { host := os.Getenv(\"HOST\"); http.ListenAndServe(host+\":\"+os.Getenv(\"PORT\"), nil) }\n",
	})
	host, ok := variableNamed(bind.Candidates[0].Variables, "HOST")
	if !ok || host.Setup != "default" || host.DefaultValue != "0.0.0.0" {
		t.Fatalf("bind HOST = %+v", host)
	}
	public := detectFixture(t, map[string]string{
		"package.json":      `{"scripts":{"start":"node mail.js"},"dependencies":{"nodemailer":"6"}}`,
		"package-lock.json": `{}`,
		"mail.js":           "const link = `https://${process.env.HOST}/verify`\n",
	})
	host, ok = variableNamed(public.Candidates[0].Variables, "HOST")
	if !ok || host.Setup != "" {
		t.Fatalf("public HOST = %+v", host)
	}
}

// A committed .env.production's value is never kept, but that it points at
// loopback is.
func TestCommittedEnvFilesRecordLoopbackWithoutValues(t *testing.T) {
	t.Parallel()
	result := detectFixture(t, map[string]string{
		"package.json":      `{"scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"16","pg":"8"}}`,
		"package-lock.json": `{}`,
		".env.production":   "NEXT_PUBLIC_API_URL=http://localhost:8000\nDATABASE_URL=postgres://app:hunter2@127.0.0.1:5432/app\nHOST=127.0.0.1\nPUBLIC_NAME=Shop\n",
	})
	variables := result.Candidates[0].Variables
	api, _ := variableNamed(variables, "NEXT_PUBLIC_API_URL")
	database, _ := variableNamed(variables, "DATABASE_URL")
	host, _ := variableNamed(variables, "HOST")
	name, _ := variableNamed(variables, "PUBLIC_NAME")
	if api.LocalhostIn != ".env.production" || database.LocalhostIn != ".env.production" || host.LocalhostIn != "" || name.LocalhostIn != "" {
		t.Fatalf("loopback evidence: api %+v database %+v host %+v name %+v", api, database, host, name)
	}
	for _, variable := range variables {
		if variable.Example != "" || strings.Contains(variable.SetupReason, "hunter2") {
			t.Fatalf("a real env file's value leaked into %+v", variable)
		}
	}
	if len(result.Candidates[0].Databases) != 1 || result.Candidates[0].Databases[0].Engine != "postgres" {
		t.Fatalf("databases = %+v", result.Candidates[0].Databases)
	}
}

// A Rails app deployed from its own Dockerfile needs SECRET_KEY_BASE, which no
// line of its code reads, its master key for committed credentials, Solid
// Queue inside Puma, and a URL for each of Rails 8's databases.
func TestRailsDockerfileCandidateGetsFrameworkVariables(t *testing.T) {
	t.Parallel()
	result := detectFixture(t, map[string]string{
		"Dockerfile":                        "FROM ruby:3.4-slim\nEXPOSE 80\nCMD [\"./bin/thrust\", \"./bin/rails\", \"server\"]\n",
		"Gemfile":                           "source 'https://rubygems.org'\ngem 'rails'\n",
		"Gemfile.lock":                      "GEM\n  remote: https://rubygems.org/\n  specs:\n    pg (1.5.9)\n    railties (8.0.3)\n    rails (8.0.3)\n    solid_queue (1.1.5)\n    omniauth-google-oauth2 (1.2.1)\n\nPLATFORMS\n  x86_64-linux\n",
		"config/application.rb":             "module Shop\n  class Application < Rails::Application\n  end\nend\n",
		"config/credentials.yml.enc":        "c2VjcmV0",
		"config/puma.rb":                    "plugin :solid_queue if ENV[\"SOLID_QUEUE_IN_PUMA\"]\n",
		"config/environments/production.rb": "Rails.application.configure do\n  config.require_master_key = true\nend\n",
		"config/database.yml":               "production:\n  primary: &primary_production\n    <<: *default\n    database: shop_production\n  cache:\n    <<: *primary_production\n  queue:\n    <<: *primary_production\n  cable:\n    <<: *primary_production\n",
	})
	candidate := candidateByMethod(t, result, BuildDockerfile)
	if candidate.Framework != "rails" {
		t.Fatalf("framework = %q", candidate.Framework)
	}
	secret, _ := variableNamed(candidate.Variables, "SECRET_KEY_BASE")
	master, _ := variableNamed(candidate.Variables, "RAILS_MASTER_KEY")
	queue, _ := variableNamed(candidate.Variables, "SOLID_QUEUE_IN_PUMA")
	if secret.Setup != "generate" || secret.GenerateFormat != "hex" || secret.GenerateLength != 128 {
		t.Fatalf("SECRET_KEY_BASE = %+v", secret)
	}
	if master.Setup != "paste" || !master.Required {
		t.Fatalf("RAILS_MASTER_KEY = %+v", master)
	}
	if queue.Setup != "default" || queue.DefaultValue != "true" {
		t.Fatalf("SOLID_QUEUE_IN_PUMA = %+v", queue)
	}
	if len(candidate.Databases) != 1 || candidate.Databases[0].Engine != "postgres" ||
		!reflect.DeepEqual(candidate.Databases[0].AlsoVariables, []string{"CACHE_DATABASE_URL", "QUEUE_DATABASE_URL", "CABLE_DATABASE_URL"}) {
		t.Fatalf("databases = %+v", candidate.Databases)
	}
	codes := noteCodes(candidate.EnvironmentNotes)
	for _, code := range []string{"auth_callback", "rails_credentials", "rails_require_master_key"} {
		if !stringSliceContains(codes, code) {
			t.Fatalf("notes %v lack %s", codes, code)
		}
	}
	for _, note := range candidate.EnvironmentNotes {
		if note.Code == "auth_callback" && note.Detail != "google_oauth2|/auth/google_oauth2/callback" {
			t.Fatalf("omniauth callback = %+v", note)
		}
	}
	if err := validateDetectedEnvironment(candidate); err != nil {
		t.Fatal(err)
	}
}

// phx.gen.release's Dockerfile exposes nothing; runtime.exs reads its
// variables with `|| raise`, and the migrate overlay is never called.
func TestPhoenixDockerfileCandidate(t *testing.T) {
	t.Parallel()
	result := detectFixture(t, map[string]string{
		"Dockerfile":               "FROM elixir:1.18 AS build\nFROM debian:bookworm-slim\nCMD [\"/app/bin/server\"]\n",
		"mix.exs":                  "defmodule Shop.MixProject do\n  defp deps do\n    [\n      {:phoenix, \"~> 1.7\"},\n      {:postgrex, \">= 0.0.0\"},\n      {:ecto_sql, \"~> 3.10\"}\n    ]\n  end\nend\n",
		"config/runtime.exs":       "database_url =\n  System.get_env(\"DATABASE_URL\") ||\n    raise \"environment variable DATABASE_URL is missing.\"\nsecret_key_base =\n  System.get_env(\"SECRET_KEY_BASE\") ||\n    raise \"environment variable SECRET_KEY_BASE is missing.\"\nhost = System.get_env(\"PHX_HOST\") || \"example.com\"\npool = String.to_integer(System.get_env(\"POOL_SIZE\") || \"10\")\n",
		"rel/overlays/bin/migrate": "#!/bin/sh\n./shop eval Shop.Release.migrate\n",
	})
	candidate := candidateByMethod(t, result, BuildDockerfile)
	if candidate.Framework != "phoenix" || candidate.Port != 4000 {
		t.Fatalf("framework %q port %d", candidate.Framework, candidate.Port)
	}
	database, _ := variableNamed(candidate.Variables, "DATABASE_URL")
	secret, _ := variableNamed(candidate.Variables, "SECRET_KEY_BASE")
	host, _ := variableNamed(candidate.Variables, "PHX_HOST")
	pool, _ := variableNamed(candidate.Variables, "POOL_SIZE")
	if !database.Required || secret.Setup != "generate" || !secret.Required || host.Setup != "domain" || host.DomainTemplate != "{{hostname}}" ||
		host.Example != "example.com" || pool.Example != "10" || pool.Required {
		t.Fatalf("variables = %+v", candidate.Variables)
	}
	if len(candidate.Databases) != 1 || candidate.Databases[0].Engine != "postgres" || candidate.Databases[0].Variable != "DATABASE_URL" {
		t.Fatalf("databases = %+v", candidate.Databases)
	}
	if !stringSliceContains(noteCodes(candidate.EnvironmentNotes), "phoenix_migrate_overlay") {
		t.Fatalf("notes = %+v", candidate.EnvironmentNotes)
	}
}

// Django's settings commit a key and turn debug on literally; both are notes
// for preflight, and neither carries the key.
func TestDjangoInsecureSettingsAreNoted(t *testing.T) {
	t.Parallel()
	result := detectFixture(t, map[string]string{
		"requirements.txt":   "Django==5.2\n",
		"manage.py":          "import os\n",
		"mysite/settings.py": "SECRET_KEY = 'django-insecure-abc123'\nDEBUG = True\n",
	})
	notes := result.Candidates[0].EnvironmentNotes
	if !reflect.DeepEqual(noteCodes(notes), []string{"debug_literal", "secret_key_literal"}) {
		t.Fatalf("notes = %+v", notes)
	}
	for _, note := range notes {
		if strings.Contains(note.Detail, "abc123") {
			t.Fatalf("the committed key leaked into %+v", note)
		}
	}
}

// Auth.js providers, a Stripe webhook route, Firebase and Supabase sign-in
// and a Clerk dependency all name addresses a provider must be told about.
func TestExternalCallbacksAreNoted(t *testing.T) {
	t.Parallel()
	result := detectFixture(t, map[string]string{
		"package.json":      `{"scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"16","next-auth":"5.0.0-beta.29","stripe":"18","@clerk/nextjs":"6","firebase":"11","@supabase/supabase-js":"2"}}`,
		"package-lock.json": `{}`,
		"auth.ts":           "import GitHub from 'next-auth/providers/github'\nimport Credentials from 'next-auth/providers/credentials'\n",
		"app/(billing)/api/webhooks/stripe/route.ts": "stripe.webhooks.constructEvent(body, sig, secret)\n",
		"lib/firebase.ts": "import { getAuth } from 'firebase/auth'\ngetAuth(app)\n",
		"lib/supabase.ts": "supabase.auth.signInWithOAuth({ provider: 'google' })\n",
	})
	details := map[string]string{}
	for _, note := range result.Candidates[0].EnvironmentNotes {
		details[note.Code+":"+note.Detail] = note.Path
	}
	for _, want := range []string{
		"auth_callback:github|/api/auth/callback/github", "stripe_webhook:/api/webhooks/stripe",
		"auth_service:firebase", "auth_service:supabase", "auth_service:clerk",
	} {
		if _, ok := details[want]; !ok {
			t.Fatalf("notes %v lack %s", details, want)
		}
	}
	if _, ok := details["auth_callback:credentials|/api/auth/callback/credentials"]; ok {
		t.Fatal("the credentials provider has no third-party callback")
	}
}

// A Go or Rust binary that exits when .env is missing is noted, and so is a
// build script that fetches from localhost.
func TestDotenvAndBuildLocalhostNotes(t *testing.T) {
	t.Parallel()
	goResult := detectFixture(t, map[string]string{
		"go.mod":  "module example.com/app\n\ngo 1.25\n\nrequire github.com/joho/godotenv v1.5.1\n",
		"main.go": "package main\nimport (\"log\"; \"github.com/joho/godotenv\")\nfunc main() {\n\tif err := godotenv.Load(); err != nil {\n\t\tlog.Fatal(\"Error loading .env file\")\n\t}\n}\n",
	})
	if !stringSliceContains(noteCodes(goResult.Candidates[0].EnvironmentNotes), "dotenv_file_required") {
		t.Fatalf("go notes = %+v", goResult.Candidates[0].EnvironmentNotes)
	}
	ignored := detectFixture(t, map[string]string{
		"go.mod":  "module example.com/app\n\ngo 1.25\n",
		"main.go": "package main\nimport \"github.com/joho/godotenv\"\nfunc main() { _ = godotenv.Load() }\n",
	})
	if len(ignored.Candidates[0].EnvironmentNotes) != 0 {
		t.Fatalf("an ignored load is not fatal: %+v", ignored.Candidates[0].EnvironmentNotes)
	}
	rust := detectFixture(t, map[string]string{
		"Cargo.toml":  "[package]\nname = \"api\"\nversion = \"0.1.0\"\n\n[dependencies]\naxum = \"0.8\"\ndotenvy = \"0.15\"\n",
		"src/main.rs": "fn main() { dotenvy::dotenv().expect(\".env file not found\"); }\n",
	})
	if !stringSliceContains(noteCodes(rust.Candidates[0].EnvironmentNotes), "dotenv_file_required") {
		t.Fatalf("rust notes = %+v", rust.Candidates[0].EnvironmentNotes)
	}
	codegen := detectFixture(t, map[string]string{
		"package.json":      `{"scripts":{"build":"npm run codegen && next build","codegen":"graphql-codegen","start":"next start"},"dependencies":{"next":"16"}}`,
		"package-lock.json": `{}`,
		"codegen.yml":       "schema: http://localhost:4000/graphql\n",
	})
	if !stringSliceContains(noteCodes(codegen.Candidates[0].EnvironmentNotes), "codegen_localhost") {
		t.Fatalf("codegen notes = %+v", codegen.Candidates[0].EnvironmentNotes)
	}
	script := detectFixture(t, map[string]string{
		"package.json":      `{"scripts":{"prebuild":"openapi-typescript http://localhost:8000/openapi.json -o api.ts","build":"vite build"},"devDependencies":{"vite":"6"}}`,
		"package-lock.json": `{}`,
		"index.html":        "<div></div>",
	})
	notes := script.Candidates[0].EnvironmentNotes
	if len(notes) != 1 || notes[0].Code != "build_fetches_localhost" || notes[0].Detail != "prebuild script" {
		t.Fatalf("script notes = %+v", notes)
	}
}

func TestValidateDetectedEnvironmentRefusesSmuggledValues(t *testing.T) {
	t.Parallel()
	base := DetectedCandidate{Variables: []DetectedVariable{{Name: "APP_URL", Sources: []string{".env.example"}}}}
	for name, mutate := range map[string]func(*DetectedCandidate){
		"unknown setup": func(c *DetectedCandidate) { c.Variables[0].Setup = "execute" },
		"template without a hostname": func(c *DetectedCandidate) {
			c.Variables[0].Setup, c.Variables[0].DomainTemplate = "domain", "https://evil.test"
		},
		"template with credentials": func(c *DetectedCandidate) {
			c.Variables[0].Setup, c.Variables[0].DomainTemplate = "domain", "https://user:pass@{{hostname}}"
		},
		"default carrying a secret": func(c *DetectedCandidate) {
			c.Variables[0].Setup, c.Variables[0].DefaultValue = "default", "password=hunter2"
		},
		"generated without length": func(c *DetectedCandidate) { c.Variables[0].Setup = "generate" },
		"unknown format": func(c *DetectedCandidate) {
			c.Variables[0].Setup, c.Variables[0].GenerateLength, c.Variables[0].GenerateFormat = "generate", 32, "shell"
		},
		"note carrying a secret":   func(c *DetectedCandidate) { c.EnvironmentNotes = []EnvironmentNote{{Code: "x", Detail: "token=abc"}} },
		"malformed note code":      func(c *DetectedCandidate) { c.EnvironmentNotes = []EnvironmentNote{{Code: "Bad Code"}} },
		"malformed browser prefix": func(c *DetectedCandidate) { c.BrowserPrefixes = []string{"next_public"} },
	} {
		candidate := base
		candidate.Variables = append([]DetectedVariable(nil), base.Variables...)
		mutate(&candidate)
		if validateDetectedEnvironment(candidate) == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	if err := validateDetectedEnvironment(base); err != nil {
		t.Fatal(err)
	}
}

func TestLoopbackValueReadsEveryConnectionShape(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, value string
		want        bool
	}{
		{"DATABASE_URL", "postgresql://postgres:postgres@localhost:5432/app", true},
		{"DATABASE_URL", "postgres://db-4.jd.internal:5432/app", false},
		{"MONGODB_URI", "mongodb://127.0.0.1:27017,127.0.0.2:27018/app", true},
		{"SPRING_DATASOURCE_URL", "jdbc:postgresql://localhost:5432/app", true},
		{"CONNECTIONSTRINGS__DEFAULT", "Host=localhost;Database=app;Username=u;Password=p", true},
		{"CONNECTIONSTRINGS__DEFAULT", "Server=db.example.com;Database=app", false},
		{"REDIS_HOST", "localhost:6379", true},
		{"REDIS_HOST", "cache.internal", false},
		{"API_URL", "http://[::1]:8000", true},
		{"API_URL", "http://0.0.0.0:8000", true},
		{"SITE_NAME", "localhost", false},
		{"DATABASE_URL", "${{database.4}}", false},
	} {
		if got := loopbackValue(test.name, test.value); got != test.want {
			t.Fatalf("loopbackValue(%s, %s) = %v", test.name, test.value, got)
		}
	}
}
