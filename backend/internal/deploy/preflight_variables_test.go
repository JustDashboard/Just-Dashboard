package deploy

import (
	"context"
	"encoding/base64"
	"reflect"
	"strings"
	"testing"
)

func variablesDraft(candidate DetectedCandidate, configuration PlanConfiguration, staged map[string]string) *Draft {
	candidate = newDetectedCandidate(candidate.Root, candidate.BuildMethod, candidate)
	if configuration.Build.Method == "" {
		configuration.Build.Method = candidate.BuildMethod
	}
	return &Draft{
		ID: "draft", Data: DraftData{
			Intent:        &DraftIntentConfig{Name: "app", Profile: ProfileWeb},
			Source:        &DraftSourceConfig{Kind: SourceGit},
			Detection:     &DetectionResult{Candidates: []DetectedCandidate{candidate}, SelectedID: candidate.ID},
			Configuration: &configuration,
		},
		environment: staged,
	}
}

func findingWithCode(findings []PreflightFinding, code string) (PreflightFinding, bool) {
	for _, finding := range findings {
		if finding.Code == code {
			return finding, true
		}
	}
	return PreflightFinding{}, false
}

func planned(names ...PlannedVariable) []PlannedVariable { return names }

var appDomain = []PlannedDomain{{Hostname: "app.example.com", HTTPS: true, Ownership: OwnershipManaged}}

// Each environment finding fires on exactly the evidence that causes the
// failure it names, and stays quiet once the plan answers it.
func TestEnvironmentFindings(t *testing.T) {
	t.Parallel()
	nextAuth := DetectedCandidate{
		Name: "web", BuildMethod: BuildRecipe, Recipe: "node", Framework: "nextjs", Port: 3000,
		BrowserPrefixes: []string{"NEXT_PUBLIC_"},
		Variables: []DetectedVariable{
			{Name: "AUTH_SECRET", Sources: []string{".env.example"}, Setup: "generate", SetupReason: "Auth.js signs sessions with it", GenerateLength: 32, GenerateFormat: "base64"},
			{Name: "AUTH_URL", Sources: []string{".env.example"}, Setup: "domain", SetupReason: "Auth.js builds its callback URLs from it", DomainTemplate: "{{scheme}}://{{hostname}}"},
			{Name: "NEXT_PUBLIC_API_URL", Sources: []string{"src/api.ts"}, Phase: "build", BrowserInlined: true},
			{Name: "RESEND_API_KEY", Sources: []string{".env.example"}},
			{Name: "SEED_TOKEN", Sources: []string{"scripts/seed.py"}, RequiredRead: true},
		},
	}
	for _, test := range []struct {
		name          string
		candidate     DetectedCandidate
		configuration PlanConfiguration
		staged        map[string]string
		observation   HostObservation
		want          map[string]PreflightSeverity
		absent        []string
	}{
		{
			name:      "generated and bound variables pass",
			candidate: nextAuth,
			configuration: PlanConfiguration{Domains: appDomain, Variables: planned(
				PlannedVariable{Name: "AUTH_SECRET", Sensitivity: "secret", Scopes: []string{"runtime", "build"}, Generate: 32, GenerateFormat: "base64"},
				PlannedVariable{Name: "AUTH_URL", Sensitivity: "plain", Scopes: []string{"runtime"}, DomainTemplate: "{{scheme}}://{{hostname}}", Value: "https://app.example.com"},
			)},
			staged: map[string]string{"NEXT_PUBLIC_API_URL": "https://api.example.com", "RESEND_API_KEY": "re_123", "SEED_TOKEN": "x"},
			want: map[string]PreflightSeverity{
				"secrets_generated": PreflightPass, "public_url_bound_auth_url": PreflightPass,
			},
			absent: []string{"secret_unset_auth_secret", "browser_variables_unbuilt", "documented_variables_unset", "variable_likely_required"},
		},
		{
			name:      "a cleared secret, an unbound URL and an unbuilt browser variable warn",
			candidate: nextAuth,
			configuration: PlanConfiguration{Variables: planned(
				PlannedVariable{Name: "AUTH_URL", Sensitivity: "plain", Scopes: []string{"runtime"}, DomainTemplate: "{{scheme}}://{{hostname}}"},
			)},
			staged: map[string]string{},
			want: map[string]PreflightSeverity{
				"secret_unset_auth_secret": PreflightWarning, "public_url_unbound_auth_url": PreflightWarning,
				"browser_variables_unbuilt": PreflightWarning, "documented_variables_unset": PreflightWarning,
				"variable_likely_required": PreflightWarning,
			},
			absent: []string{"secrets_generated"},
		},
		{
			name:      "a typed URL for another host is a mismatch",
			candidate: nextAuth,
			configuration: PlanConfiguration{Domains: appDomain, Variables: planned(
				PlannedVariable{Name: "AUTH_SECRET", Sensitivity: "secret", Scopes: []string{"runtime"}, Generate: 32},
			)},
			staged: map[string]string{"AUTH_URL": "https://old-host.example.org"},
			want:   map[string]PreflightSeverity{"public_url_mismatch_auth_url": PreflightWarning},
		},
		{
			name:      "localhost values",
			candidate: nextAuth,
			configuration: PlanConfiguration{Variables: planned(
				PlannedVariable{Name: "AUTH_SECRET", Sensitivity: "secret", Scopes: []string{"runtime"}, Generate: 32},
			)},
			staged: map[string]string{
				"DATABASE_URL": "postgresql://postgres:postgres@localhost:5432/app", "NEXT_PUBLIC_API_URL": "http://localhost:8000",
				"HOST": "127.0.0.1", "REDIS_URL": "redis://cache.internal:6379",
			},
			want: map[string]PreflightSeverity{
				"variable_points_to_localhost_database_url": PreflightWarning, "build_inlines_localhost_next_public_api_url": PreflightWarning,
				"host_variable_loopback_host": PreflightWarning,
			},
			absent: []string{"variable_points_to_localhost_redis_url"},
		},
		{
			name: "secrets compiled into the browser",
			candidate: DetectedCandidate{Name: "spa", BuildMethod: BuildRecipe, BrowserPrefixes: []string{"VITE_"}, Variables: []DetectedVariable{
				{Name: "GEMINI_API_KEY", Sources: []string{"vite.config.ts"}, Phase: "build", BrowserInlined: true},
			}},
			staged: map[string]string{
				"GEMINI_API_KEY": "AIzaSyExample", "VITE_OPENAI_API_KEY": "abc", "VITE_STRIPE_KEY": "sk_live_abc",
				"VITE_SUPABASE_SERVICE_KEY": serviceRoleJWT(), "VITE_SUPABASE_ANON_KEY": anonJWT(), "VITE_FIREBASE_API_KEY": "AIza",
			},
			want: map[string]PreflightSeverity{
				"public_variable_secret_vite_openai_api_key":       PreflightWarning,
				"public_variable_secret_vite_stripe_key":           PreflightBlocked,
				"public_variable_secret_vite_supabase_service_key": PreflightBlocked,
			},
			absent: []string{"public_variable_secret_vite_supabase_anon_key", "public_variable_secret_vite_firebase_api_key"},
		},
		{
			name: "a static env import with no build value blocks",
			candidate: DetectedCandidate{Name: "kit", BuildMethod: BuildRecipe, Variables: []DetectedVariable{
				{Name: "DATABASE_URL", Sources: []string{"src/lib/server/db.ts"}, Phase: "build", Required: true},
			}},
			configuration: PlanConfiguration{Variables: planned(
				PlannedVariable{Name: "DATABASE_URL", Sensitivity: "secret", Scopes: []string{"runtime"}, Reference: "${{database.4}}"},
			)},
			want: map[string]PreflightSeverity{"build_variable_missing_database_url": PreflightBlocked},
		},
		{
			name: "a staged value reaches the build",
			candidate: DetectedCandidate{Name: "kit", BuildMethod: BuildRecipe, Variables: []DetectedVariable{
				{Name: "DATABASE_URL", Sources: []string{"src/lib/server/db.ts"}, Phase: "build", Required: true},
			}},
			staged: map[string]string{"DATABASE_URL": "${{database.4}}"},
			absent: []string{"build_variable_missing_database_url", "variable_likely_required"},
		},
		{
			name: "Rails without a secret base blocks, without its master key warns or blocks",
			candidate: DetectedCandidate{Name: "rails", BuildMethod: BuildDockerfile, Framework: "rails",
				Variables: []DetectedVariable{
					{Name: "SECRET_KEY_BASE", Sources: []string{"Gemfile.lock"}, Setup: "generate", SetupReason: "Rails", GenerateLength: 128, GenerateFormat: "hex", Required: true},
					{Name: "RAILS_MASTER_KEY", Sources: []string{"config/credentials.yml.enc"}, Setup: "paste", SetupReason: "decrypts", Required: true},
				},
				EnvironmentNotes: []EnvironmentNote{{Code: "rails_credentials", Path: "config/credentials.yml.enc"}, {Code: "rails_require_master_key", Path: "config/environments/production.rb"}},
			},
			want: map[string]PreflightSeverity{"rails_secret_missing": PreflightBlocked, "rails_master_key_missing": PreflightBlocked},
		},
		{
			name: "Rails with its generated secret and pasted key passes",
			candidate: DetectedCandidate{Name: "rails", BuildMethod: BuildDockerfile, Framework: "rails",
				EnvironmentNotes: []EnvironmentNote{{Code: "rails_credentials", Path: "config/credentials.yml.enc"}}},
			configuration: PlanConfiguration{Variables: planned(PlannedVariable{Name: "SECRET_KEY_BASE", Sensitivity: "secret", Scopes: []string{"runtime"}, Generate: 128, GenerateFormat: "hex"})},
			staged:        map[string]string{"RAILS_MASTER_KEY": "0123456789abcdef0123456789abcdef"},
			absent:        []string{"rails_secret_missing", "rails_master_key_missing"},
		},
		{
			name: "Django debug and a committed key",
			candidate: DetectedCandidate{Name: "django", BuildMethod: BuildRecipe, Framework: "django", EnvironmentNotes: []EnvironmentNote{
				{Code: "secret_key_literal", Detail: "django-insecure", Path: "mysite/settings.py"}, {Code: "debug_literal", Path: "mysite/settings.py"},
			}},
			want: map[string]PreflightSeverity{"django_insecure_settings": PreflightWarning},
		},
		{
			name: "a debug default the dashboard turns off is answered",
			candidate: DetectedCandidate{Name: "django", BuildMethod: BuildRecipe, Framework: "django", EnvironmentNotes: []EnvironmentNote{
				{Code: "debug_default_true", Detail: "DJANGO_DEBUG|False", Path: "config/settings/base.py"},
			}},
			configuration: PlanConfiguration{Variables: planned(PlannedVariable{Name: "DJANGO_DEBUG", Sensitivity: "plain", Scopes: []string{"runtime"}, Value: "False"})},
			absent:        []string{"django_insecure_settings"},
		},
		{
			// DEBUG itself being set says nothing when the settings read another name.
			name: "a debug default read under another name stays on",
			candidate: DetectedCandidate{Name: "django", BuildMethod: BuildRecipe, Framework: "django", EnvironmentNotes: []EnvironmentNote{
				{Code: "debug_default_true", Detail: "DJANGO_DEBUG|False", Path: "config/settings/base.py"},
			}},
			configuration: PlanConfiguration{Variables: planned(PlannedVariable{Name: "DEBUG", Sensitivity: "plain", Scopes: []string{"runtime"}, Value: "False"})},
			want:          map[string]PreflightSeverity{"django_insecure_settings": PreflightWarning},
		},
		{
			name: "a host list whose separator is unknown is not bound",
			candidate: DetectedCandidate{Name: "django", BuildMethod: BuildRecipe, Framework: "django", EnvironmentNotes: []EnvironmentNote{
				{Code: "allowed_hosts_unbound", Detail: "ALLOWED_HOSTS", Path: ".env.example"},
			}},
			want: map[string]PreflightSeverity{"allowed_hosts_unbound_allowed_hosts": PreflightWarning},
		},
		{
			name: "a host list set by hand answers its note",
			candidate: DetectedCandidate{Name: "django", BuildMethod: BuildRecipe, Framework: "django", EnvironmentNotes: []EnvironmentNote{
				{Code: "allowed_hosts_unbound", Detail: "ALLOWED_HOSTS", Path: ".env.example"},
			}},
			staged: map[string]string{"ALLOWED_HOSTS": "app.example.com"},
			absent: []string{"allowed_hosts_unbound_allowed_hosts"},
		},
		{
			name: "a space-separated host list bound to the domain passes",
			candidate: DetectedCandidate{Name: "django", BuildMethod: BuildRecipe, Framework: "django", Variables: []DetectedVariable{
				{Name: "DJANGO_ALLOWED_HOSTS", Sources: []string{"app/settings.py"}, Setup: "domain", SetupReason: "Django answers only the hosts it allows", DomainTemplate: "{{hostname}} localhost 127.0.0.1"},
			}},
			configuration: PlanConfiguration{Domains: appDomain, Variables: planned(
				PlannedVariable{Name: "DJANGO_ALLOWED_HOSTS", Sensitivity: "plain", Scopes: []string{"runtime"}, DomainTemplate: "{{hostname}} localhost 127.0.0.1", Value: "app.example.com localhost 127.0.0.1"},
			)},
			want: map[string]PreflightSeverity{"public_url_bound_django_allowed_hosts": PreflightPass},
		},
		{
			name: "a comma-joined value for a space-separated list is one host that matches nothing",
			candidate: DetectedCandidate{Name: "django", BuildMethod: BuildRecipe, Framework: "django", Variables: []DetectedVariable{
				{Name: "DJANGO_ALLOWED_HOSTS", Sources: []string{"app/settings.py"}, Setup: "domain", SetupReason: "Django answers only the hosts it allows", DomainTemplate: "{{hostname}} localhost 127.0.0.1"},
			}},
			configuration: PlanConfiguration{Domains: appDomain},
			staged:        map[string]string{"DJANGO_ALLOWED_HOSTS": "app.example.com,localhost"},
			want:          map[string]PreflightSeverity{"public_url_mismatch_django_allowed_hosts": PreflightWarning},
		},
		{
			name: "an empty .env in a recipe image passes; a Dockerfile must provide it",
			candidate: DetectedCandidate{Name: "go", BuildMethod: BuildRecipe, EnvironmentNotes: []EnvironmentNote{
				{Code: "dotenv_file_required", Detail: "godotenv", Path: "main.go"},
			}},
			want: map[string]PreflightSeverity{"dotenv_file_provided": PreflightPass},
		},
		{
			name: "a hosted driver on a local database, and a URL where JDBC is read",
			candidate: DetectedCandidate{Name: "web", BuildMethod: BuildRecipe, Databases: []DetectedDatabase{
				{Engine: "postgres", Variable: "DATABASE_URL", Evidence: "drizzle-orm/neon-http", Hosted: "neon-http"},
				{Engine: "mysql", Variable: "SPRING_DATASOURCE_URL", Evidence: "mysql-connector-j", Format: "jdbc"},
			}},
			staged: map[string]string{"DATABASE_URL": "${{database.4}}", "SPRING_DATASOURCE_URL": "${{database.5}}"},
			want:   map[string]PreflightSeverity{"hosted_driver_local_database": PreflightWarning, "database_url_format": PreflightWarning},
		},
		{
			name: "MariaDB Connector/J refuses the mysql JDBC scheme",
			candidate: DetectedCandidate{Name: "api", BuildMethod: BuildRecipe, Databases: []DetectedDatabase{
				{Engine: "mariadb", Variable: "SPRING_DATASOURCE_URL", Evidence: "mariadb-java-client", Format: "jdbc-mariadb"},
			}},
			staged: map[string]string{"SPRING_DATASOURCE_URL": "${{database.5.jdbc}}"},
			want:   map[string]PreflightSeverity{"database_url_format": PreflightWarning},
		},
		{
			name: "the JDBC form of the link satisfies the consumer",
			candidate: DetectedCandidate{Name: "api", BuildMethod: BuildRecipe, Databases: []DetectedDatabase{
				{Engine: "postgres", Variable: "SPRING_DATASOURCE_URL", Evidence: "org.postgresql", Format: "jdbc"},
			}},
			staged: map[string]string{"SPRING_DATASOURCE_URL": "${{database.5.jdbc}}"},
			absent: []string{"database_url_format"},
		},
		{
			name: "a linked server without the schema's extension, and MongoDB without AVX",
			candidate: DetectedCandidate{Name: "rag", BuildMethod: BuildRecipe, Databases: []DetectedDatabase{
				{Engine: "postgres", Variable: "DATABASE_URL", Evidence: "pgvector", Extensions: []string{"vector"}},
				{Engine: "mongodb", Variable: "MONGODB_URI", Evidence: "mongoose"},
			}},
			configuration: PlanConfiguration{Dependencies: []PlannedDependency{{Kind: "database", Ownership: OwnershipLinked, ResourceKind: "database_connection", ResourceID: "4"}}},
			observation: HostObservation{Architecture: "amd64", CPUFeatures: []string{}, Dependencies: []DependencyObservation{
				{Kind: "database", ResourceKind: "database_connection", ResourceID: "4", Available: true, Status: "jd-postgres", Extensions: []string{"postgis"}},
			}},
			want: map[string]PreflightSeverity{"database_extension_missing": PreflightBlocked, "database_cpu_unsupported": PreflightWarning},
		},
		{
			name: "Laravel's start migrates a database nobody linked",
			candidate: DetectedCandidate{Name: "laravel", BuildMethod: BuildRecipe, Framework: "laravel", StartCommand: "php artisan migrate --force && php-server",
				Databases: []DetectedDatabase{{Engine: "mysql", Variable: "DB_URL", Evidence: "DB_CONNECTION=mysql"}}},
			want: map[string]PreflightSeverity{"database_required_for_start": PreflightBlocked},
		},
		{
			name: "Rails' start prepares a database nobody linked",
			candidate: DetectedCandidate{Name: "rails", BuildMethod: BuildRecipe, Framework: "rails",
				StartCommand: "bundle exec rails db:prepare && exec bundle exec rails server --binding 0.0.0.0 --port ${PORT:-3000}",
				Databases:    []DetectedDatabase{{Engine: "postgres", Variable: "DATABASE_URL", Evidence: "pg in Gemfile.lock"}}},
			want: map[string]PreflightSeverity{"database_required_for_start": PreflightBlocked},
		},
		{
			name: "Laravel on SQLite needs nothing linked",
			candidate: DetectedCandidate{Name: "laravel", BuildMethod: BuildRecipe, Framework: "laravel", StartCommand: "php artisan migrate --force && php-server",
				Databases: []DetectedDatabase{{Engine: "mysql", Variable: "DB_URL", Evidence: "DB_CONNECTION=mysql"}}},
			staged: map[string]string{"DB_CONNECTION": "sqlite"},
			absent: []string{"database_required_for_start"},
		},
		{
			name: "callbacks to register, and a Clerk key for another domain",
			candidate: DetectedCandidate{Name: "web", BuildMethod: BuildRecipe, EnvironmentNotes: []EnvironmentNote{
				{Code: "auth_callback", Detail: "github|/api/auth/callback/github", Path: "auth.ts"},
				{Code: "stripe_webhook", Detail: "/api/webhooks/stripe", Path: "app/api/webhooks/stripe/route.ts"},
				{Code: "auth_service", Detail: "firebase", Path: "lib/firebase.ts"},
			}},
			configuration: PlanConfiguration{Domains: appDomain},
			staged:        map[string]string{"NEXT_PUBLIC_CLERK_PUBLISHABLE_KEY": "pk_live_" + base64.RawStdEncoding.EncodeToString([]byte("clerk.old-host.com$"))},
			want:          map[string]PreflightSeverity{"external_callback_registration": PreflightWarning, "clerk_key_domain_mismatch": PreflightBlocked},
		},
		{
			name:          "a Clerk key for another host on the same site warns",
			candidate:     DetectedCandidate{Name: "web", BuildMethod: BuildRecipe},
			configuration: PlanConfiguration{Domains: []PlannedDomain{{Hostname: "shop.example.com", HTTPS: true, Ownership: OwnershipManaged}}},
			staged:        map[string]string{"NEXT_PUBLIC_CLERK_PUBLISHABLE_KEY": "pk_live_" + base64.RawStdEncoding.EncodeToString([]byte("clerk.app.example.com$"))},
			want:          map[string]PreflightSeverity{"clerk_key_domain_mismatch": PreflightWarning},
		},
		{
			name:          "a Clerk key for the planned domain passes",
			candidate:     DetectedCandidate{Name: "web", BuildMethod: BuildRecipe},
			configuration: PlanConfiguration{Domains: []PlannedDomain{{Hostname: "shop.example.com", HTTPS: true, Ownership: OwnershipManaged}}},
			staged:        map[string]string{"NEXT_PUBLIC_CLERK_PUBLISHABLE_KEY": "pk_live_" + base64.RawStdEncoding.EncodeToString([]byte("clerk.example.com$"))},
			absent:        []string{"clerk_key_domain_mismatch"},
		},
		{
			name: "Phoenix migrations and host",
			candidate: DetectedCandidate{Name: "phoenix", BuildMethod: BuildDockerfile, Framework: "phoenix",
				Variables:        []DetectedVariable{{Name: "PHX_HOST", Sources: []string{"config/runtime.exs"}, Setup: "domain", SetupReason: "Phoenix accepts sockets only from this host", DomainTemplate: "{{hostname}}"}},
				EnvironmentNotes: []EnvironmentNote{{Code: "phoenix_migrate_overlay", Path: "rel/overlays/bin/migrate"}}},
			want: map[string]PreflightSeverity{"migrations_not_run": PreflightWarning, "phoenix_host_unbound_phx_host": PreflightWarning},
		},
		{
			// The Dockerfile reading made the overlay the release command,
			// which release_command_unmapped answers instead.
			name: "Phoenix migrations the release command runs",
			candidate: DetectedCandidate{Name: "phoenix", BuildMethod: BuildDockerfile, Framework: "phoenix", ReleaseCommand: "bin/migrate",
				EnvironmentNotes: []EnvironmentNote{{Code: "phoenix_migrate_overlay", Path: "rel/overlays/bin/migrate"}}},
			absent: []string{"migrations_not_run"},
		},
		{
			name: "a build-time read a repository Dockerfile does not declare",
			candidate: DetectedCandidate{Name: "web", BuildMethod: BuildDockerfile, Variables: []DetectedVariable{
				{Name: "DATABASE_URL", Sources: []string{"prisma.config.ts"}, Phase: "build", Required: true},
			}},
			staged: map[string]string{"DATABASE_URL": "${{database.4}}"},
			want:   map[string]PreflightSeverity{"build_variable_unreachable_database_url": PreflightWarning},
		},
		{
			// dockerfile_build_args and dockerfile_arg_not_passed answer it.
			name: "a build-time read the Dockerfile declares with ARG",
			candidate: DetectedCandidate{Name: "web", BuildMethod: BuildDockerfile, DockerfileArgs: []DockerfileArg{{Name: "NEXT_PUBLIC_API_URL", Consumed: true}},
				Variables: []DetectedVariable{{Name: "NEXT_PUBLIC_API_URL", Sources: []string{"src/env.ts"}, Phase: "build", Required: true}}},
			staged: map[string]string{"NEXT_PUBLIC_API_URL": "https://api.example.com"},
			absent: []string{"build_variable_unreachable_next_public_api_url"},
		},
		{
			name: "build scripts that fetch from localhost",
			candidate: DetectedCandidate{Name: "web", BuildMethod: BuildRecipe, EnvironmentNotes: []EnvironmentNote{
				{Code: "build_fetches_localhost", Detail: "prebuild script", Path: "package.json"},
			}},
			want: map[string]PreflightSeverity{"build_fetches_localhost": PreflightWarning},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			draft := variablesDraft(test.candidate, test.configuration, test.staged)
			findings := environmentFindings(draft, *draft.Data.Configuration, test.observation)
			for code, severity := range test.want {
				found, ok := findingWithCode(findings, code)
				if !ok || found.Severity != severity {
					t.Fatalf("%s = %+v (present %v), want %s\nfindings: %+v", code, found, ok, severity, findings)
				}
			}
			for _, code := range test.absent {
				if found, ok := findingWithCode(findings, code); ok {
					t.Fatalf("%s fired: %+v", code, found)
				}
			}
			if err := validatePreflightFindings(findings); err != nil {
				t.Fatalf("findings do not validate: %v", err)
			}
			for _, finding := range findings {
				text := finding.Title + finding.Measured + finding.Means + finding.Action
				for _, value := range test.staged {
					if len(value) > 8 && !strings.HasPrefix(value, "${{") && !strings.HasPrefix(value, "pk_live_") && strings.Contains(text, value) {
						t.Fatalf("%s repeats a staged value: %+v", finding.Code, finding)
					}
				}
			}
		})
	}
}

// A plan read back at release time carries names without values, so a
// declared variable counts as set there.
func TestEnvironmentFindingsOnARecordedPlan(t *testing.T) {
	t.Parallel()
	candidate := DetectedCandidate{Name: "web", BuildMethod: BuildRecipe, Variables: []DetectedVariable{
		{Name: "AUTH_SECRET", Sources: []string{".env.example"}, Setup: "generate", SetupReason: "Auth.js", GenerateLength: 32},
		{Name: "AUTH_URL", Sources: []string{".env.example"}, Setup: "domain", SetupReason: "Auth.js", DomainTemplate: "{{scheme}}://{{hostname}}"},
	}}
	draft := variablesDraft(candidate, PlanConfiguration{Variables: planned(
		PlannedVariable{Name: "AUTH_SECRET", Sensitivity: "secret", Scopes: []string{"runtime"}},
		PlannedVariable{Name: "AUTH_URL", Sensitivity: "plain", Scopes: []string{"runtime"}},
	)}, nil)
	draft.ID = ""
	findings := environmentFindings(draft, *draft.Data.Configuration, HostObservation{})
	for _, code := range []string{"secret_unset_auth_secret", "public_url_unbound_auth_url", "secrets_generated"} {
		if found, ok := findingWithCode(findings, code); ok {
			t.Fatalf("%s fired on a recorded plan: %+v", code, found)
		}
	}
}

// The findings reach a real preflight and survive its validation.
func TestPreflightDraftCarriesEnvironmentFindings(t *testing.T) {
	draft := completePlanningDraftModel()
	draft.environment = map[string]string{"DATABASE_URL": "postgres://app:app@localhost:5432/app"}
	result, err := PreflightDraft(context.Background(), draft, &preflightObserverFake{observation: HostObservation{
		Facilities: map[string]FacilityObservation{"docker": {Available: true}}, Paths: []PathObservation{}, Ports: []PortObservation{},
		Domains: []DomainObservation{}, OS: "linux", Architecture: "amd64",
	}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if findingSeverity(result.Findings, "variable_points_to_localhost_database_url") != PreflightWarning {
		t.Fatalf("findings = %+v", result.Findings)
	}
}

func TestClerkKeyDomainAndRegistrableDomain(t *testing.T) {
	t.Parallel()
	live := "pk_live_" + base64.RawStdEncoding.EncodeToString([]byte("clerk.shop.example.co.uk$"))
	if got := clerkKeyDomain(live); got != "shop.example.co.uk" {
		t.Fatalf("clerkKeyDomain = %q", got)
	}
	if clerkKeyDomain("pk_test_"+base64.RawStdEncoding.EncodeToString([]byte("clerk.dev$"))) != "" {
		t.Fatal("a development key works on any domain")
	}
	for host, want := range map[string]string{"app.example.com": "example.com", "shop.example.co.uk": "example.co.uk", "localhost": "localhost"} {
		if got := registrableDomain(host); got != want {
			t.Fatalf("registrableDomain(%s) = %s", host, got)
		}
	}
}

func serviceRoleJWT() string { return fakeJWT(`{"role":"service_role","iss":"supabase"}`) }
func anonJWT() string        { return fakeJWT(`{"role":"anon","iss":"supabase"}`) }

func fakeJWT(payload string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256"}`)) + "." +
		base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".signature"
}

func TestCPUFeaturesAndMongoSupport(t *testing.T) {
	t.Parallel()
	x86 := cpuFeaturesFrom("processor\t: 0\nflags\t\t: fpu vme sse4_2 avx avx2\nprocessor\t: 1\nflags\t\t: fpu\n")
	noAVX := cpuFeaturesFrom("flags\t\t: fpu vme sse4_2 aes\n")
	arm := cpuFeaturesFrom("Features\t: fp asimd evtstrm aes pmull sha1 sha2 crc32 atomics fphp asimdhp\n")
	pi4 := cpuFeaturesFrom("Features\t: fp asimd evtstrm crc32 cpuid\n")
	if !reflect.DeepEqual(x86, []string{"avx"}) || len(noAVX) != 0 || !reflect.DeepEqual(arm, []string{"atomics"}) || len(pi4) != 0 {
		t.Fatalf("features: %v %v %v %v", x86, noAVX, arm, pi4)
	}
	for _, test := range []struct {
		architecture string
		features     []string
		want         string
	}{
		{"amd64", x86, ""}, {"amd64", noAVX, "x86-64 without AVX"}, {"arm64", arm, ""}, {"arm64", pi4, "arm64 older than ARMv8.2"}, {"riscv64", nil, ""},
	} {
		if got := MongoCPUUnsupported(test.architecture, test.features); got != test.want {
			t.Fatalf("%s %v = %q", test.architecture, test.features, got)
		}
	}
}

// A finding's action is the concrete next step: the shaped reference that
// resolves to the same database, and on a host the PostGIS image does not
// run on, a server the operator brings rather than one quick setup cannot
// start.
func TestEnvironmentFindingActionsNameTheFix(t *testing.T) {
	t.Parallel()
	spring := DetectedCandidate{Name: "api", BuildMethod: BuildRecipe, Databases: []DetectedDatabase{
		{Engine: "mariadb", Variable: "SPRING_DATASOURCE_URL", Evidence: "mariadb-java-client", Format: "jdbc-mariadb"},
	}}
	draft := variablesDraft(spring, PlanConfiguration{}, map[string]string{"SPRING_DATASOURCE_URL": "${{database.5.jdbc}}"})
	format, ok := findingWithCode(environmentFindings(draft, *draft.Data.Configuration, HostObservation{}), "database_url_format")
	if !ok || !strings.Contains(format.Action, "${{database.5.jdbc-mariadb}}") {
		t.Fatalf("database_url_format = %+v", format)
	}
	geo := DetectedCandidate{Name: "geo", BuildMethod: BuildRecipe, Databases: []DetectedDatabase{
		{Engine: "postgres", Variable: "DATABASE_URL", Evidence: "geoalchemy2", Extensions: []string{"postgis"}},
	}}
	link := PlanConfiguration{Dependencies: []PlannedDependency{{Kind: "database", Ownership: OwnershipLinked, ResourceKind: "database_connection", ResourceID: "4"}}}
	for architecture, want := range map[string]string{"amd64": "which quick setup offers", "arm64": "x86-64 only"} {
		observation := HostObservation{Architecture: architecture, Dependencies: []DependencyObservation{
			{Kind: "database", ResourceKind: "database_connection", ResourceID: "4", Available: true, Status: "jd-postgres", Extensions: []string{}},
		}}
		draft := variablesDraft(geo, link, nil)
		missing, ok := findingWithCode(environmentFindings(draft, *draft.Data.Configuration, observation), "database_extension_missing")
		if !ok || !strings.Contains(missing.Action, want) {
			t.Fatalf("%s: database_extension_missing = %+v", architecture, missing)
		}
	}
	if !PostGISImageSupported("amd64") || PostGISImageSupported("arm64") {
		t.Fatal("postgis/postgis is published for amd64 only")
	}
}

// Linked databases are asked about extensions only when the schema needs one.
func TestObservationRequestAsksForTheSchemasExtensions(t *testing.T) {
	t.Parallel()
	plain := variablesDraft(DetectedCandidate{Name: "web", BuildMethod: BuildRecipe, Databases: []DetectedDatabase{
		{Engine: "postgres", Variable: "DATABASE_URL", Evidence: "pg"},
	}}, PlanConfiguration{}, nil)
	if request := preflightObservationRequest(plain, *plain.Data.Configuration); request.DatabaseExtensions != nil {
		t.Fatalf("extensions = %v", request.DatabaseExtensions)
	}
	vector := variablesDraft(DetectedCandidate{Name: "rag", BuildMethod: BuildRecipe, Databases: []DetectedDatabase{
		{Engine: "postgres", Variable: "DATABASE_URL", Evidence: "pgvector", Extensions: []string{"vector", "postgis"}},
	}}, PlanConfiguration{}, nil)
	if request := preflightObservationRequest(vector, *vector.Data.Configuration); !reflect.DeepEqual(request.DatabaseExtensions, []string{"postgis", "vector"}) {
		t.Fatalf("extensions = %v", request.DatabaseExtensions)
	}
}
