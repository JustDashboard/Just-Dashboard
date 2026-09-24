package deploy

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestApplicationOutputCauseNamesTheMissingTable(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"The table `public.products` does not exist in the current database.": "public.products",
		`ERROR: relation "orders" does not exist`:                             "orders",
		`Error: Table 'shop.customers' doesn't exist`:                         "shop.customers",
		"SqliteError: no such table: sessions":                                "sessions",
	}
	for text, table := range cases {
		cause := applicationOutputCause([]ContainerDiagnostics{{Lines: []RuntimeLogLine{
			{Stream: "stdout", Text: "> next start"},
			{Stream: "stderr", Text: text},
		}}}, runtimeCauseContext{})
		if cause == nil || cause.Code != "schema_missing" || cause.Table != table {
			t.Fatalf("%q diagnosed as %+v", text, cause)
		}
		if sentence := cause.sentence(); !strings.Contains(sentence, "table "+table+" does not exist") || !strings.Contains(sentence, "prisma migrate deploy") {
			t.Fatalf("sentence for %q = %q", text, sentence)
		}
	}
}

// Each case is what a real application prints when it fails its readiness
// check for that reason; the cause carries identifiers only, and its remedy
// is computed from the plan the release ran with.
func TestApplicationOutputCauseNamesRuntimeFailures(t *testing.T) {
	t.Parallel()
	recipe := BuildPlanConfig{Method: BuildRecipe, Recipe: "python", StartCommand: "uvicorn main:app --port 8000"}
	variables := []ReleaseVariableSnapshot{{Name: "SECRET_KEY", Scopes: "build"}}
	cases := []struct {
		name      string
		lines     []string
		container ContainerDiagnostics
		context   runtimeCauseContext
		want      OutputCause
		sentence  string
	}{
		{
			name:      "OOM killed",
			container: ContainerDiagnostics{State: "exited", ExitCode: 137, OOMKilled: true},
			want:      OutputCause{Code: "runtime_oom", Fix: &CauseFix{Kind: fixReview, Field: "runtime.memoryMb"}},
			sentence:  "exceeding its memory limit",
		},
		{
			name:     "Prisma P1012",
			lines:    []string{"error: Environment variable not found: DATABASE_URL.", "  -->  schema.prisma:10"},
			want:     OutputCause{Code: "runtime_env_missing", Detail: "prisma", Subjects: []string{"DATABASE_URL"}, Fix: &CauseFix{Kind: fixAddVariable, Field: "variables.DATABASE_URL", Scope: "runtime"}},
			sentence: "reads DATABASE_URL and was not given it; add DATABASE_URL as a variable with the runtime scope",
		},
		{
			name:    "Django secret key scoped only to build",
			lines:   []string{"django.core.exceptions.ImproperlyConfigured: The SECRET_KEY setting must not be empty."},
			context: runtimeCauseContext{variables: variables},
			want:    OutputCause{Code: "runtime_env_missing", Detail: "django", Subjects: []string{"SECRET_KEY"}, Fix: &CauseFix{Kind: fixVariableScope, Field: "variables.SECRET_KEY", Value: "runtime", Scope: "runtime"}},
		},
		{
			name: "t3-env",
			lines: []string{
				"❌ Invalid environment variables: {",
				"  DATABASE_URL: [ 'Required' ],",
				"  NEXTAUTH_SECRET: [ 'Required' ]",
				"}",
			},
			want: OutputCause{Code: "runtime_env_missing", Detail: "t3-env", Subjects: []string{"DATABASE_URL", "NEXTAUTH_SECRET"}, Fix: &CauseFix{Kind: fixAddVariable, Field: "variables.DATABASE_URL", Scope: "runtime"}},
		},
		{
			name:  "pydantic settings",
			lines: []string{"pydantic_core._pydantic_core.ValidationError: 1 validation error for Settings", "database_url", "  Field required [type=missing, input_value={}, input_type=dict]"},
			want:  OutputCause{Code: "runtime_env_missing", Detail: "pydantic", Subjects: []string{"database_url"}, Fix: &CauseFix{Kind: fixAddVariable, Field: "variables.DATABASE_URL", Scope: "runtime"}},
		},
		{
			name:     "database on localhost",
			lines:    []string{"PrismaClientInitializationError: Can't reach database server at `localhost:5432`"},
			want:     OutputCause{Code: "runtime_database_localhost", Detail: "PostgreSQL", Subjects: []string{"5432"}, Fix: &CauseFix{Kind: fixReview, Field: "dependencies"}},
			sentence: "connects to PostgreSQL on localhost",
		},
		{
			name:  "Prisma engine on Alpine",
			lines: []string{"prisma:warn Prisma failed to detect the libssl/openssl version to use, and may not work as expected."},
			want:  OutputCause{Code: "runtime_prisma_engine_missing", Detail: "prisma"},
		},
		{
			name:  "Django DisallowedHost",
			lines: []string{"Invalid HTTP_HOST header: '127.0.0.1:49153'. You may need to add '127.0.0.1' to ALLOWED_HOSTS."},
			want:  OutputCause{Code: "runtime_host_disallowed", Detail: "django", Subjects: []string{"127.0.0.1"}},
		},
		{
			name:  "Next without a production build",
			lines: []string{"Error: Could not find a production build in the '.next' directory. Try building your app with 'next build' before starting the production server."},
			want:  OutputCause{Code: "runtime_entry_missing", Subjects: []string{".next"}, Fix: &CauseFix{Kind: fixReview, Field: "configuration.build.startCommand"}},
		},
		{
			name:  "gunicorn attribute",
			lines: []string{"Failed to find attribute 'app' in 'main'."},
			want:  OutputCause{Code: "runtime_entry_missing", Subjects: []string{"app"}, Fix: &CauseFix{Kind: fixReview, Field: "configuration.build.startCommand"}},
		},
		{
			name:  "missing Python module",
			lines: []string{"ModuleNotFoundError: No module named 'psycopg2'"},
			want:  OutputCause{Code: "runtime_module_missing", Subjects: []string{"psycopg2"}},
		},
		{
			name: "psycopg without libpq",
			lines: []string{
				"ImportError: no pq wrapper available.", "Attempts made:",
				"- couldn't import psycopg 'c' implementation: No module named 'psycopg_c'",
				"- couldn't import psycopg 'binary' implementation: No module named 'psycopg_binary'",
				"- couldn't import psycopg 'python' implementation: libpq library not found",
			},
			want: OutputCause{Code: "runtime_library_missing", Detail: "psycopg", Subjects: []string{"libpq"}},
		},
		{
			name:  "libGL",
			lines: []string{"ImportError: libGL.so.1: cannot open shared object file: No such file or directory"},
			want:  OutputCause{Code: "runtime_library_missing", Subjects: []string{"libGL.so.1"}},
		},
		{
			name:  "go-sqlite3 without cgo",
			lines: []string{"Binary was compiled with 'CGO_ENABLED=0', go-sqlite3 requires cgo to work. This is a stub"},
			want:  OutputCause{Code: "runtime_cgo_required", Detail: "go"},
		},
		{
			name:  "start runner missing",
			lines: []string{"/bin/sh: pnpm: not found"},
			want:  OutputCause{Code: "runtime_command_not_found", Subjects: []string{"pnpm"}},
		},
		{
			name:  "exec format",
			lines: []string{"exec /app/server: exec format error"},
			want:  OutputCause{Code: "runtime_exec_format"},
		},
		{
			name:  "Phoenix origin",
			lines: []string{"[error] Could not check origin for Phoenix.Socket transport."},
			want: OutputCause{Code: "runtime_origin_rejected", Detail: "phoenix", Subjects: []string{"PHX_HOST"},
				Fix: &CauseFix{Kind: fixAddVariable, Field: "variables.PHX_HOST", Scope: "runtime"}},
		},
		{
			name:    "uvicorn on loopback",
			lines:   []string{"INFO:     Uvicorn running on http://127.0.0.1:8000 (Press CTRL+C to quit)"},
			context: runtimeCauseContext{build: recipe, runtime: RuntimePlanConfig{InternalPort: 8000}},
			want:    OutputCause{Code: "runtime_loopback_bind", Subjects: []string{"8000"}, Fix: &CauseFix{Kind: fixSetBuild, Field: "configuration.build.startCommand", Value: "uvicorn main:app --port 8000 --host 0.0.0.0"}},
		},
		{
			name:    "Next on a hardcoded port",
			lines:   []string{"   ▲ Next.js 16.0.1", "   - Local:        http://localhost:3000", " ✓ Ready in 412ms"},
			context: runtimeCauseContext{runtime: RuntimePlanConfig{InternalPort: 8080}},
			want:    OutputCause{Code: "runtime_port_mismatch", Subjects: []string{"3000"}, Fix: &CauseFix{Kind: fixSetRuntime, Field: "runtime.internalPort", Value: "3000"}},
		},
		{
			name:    "a warning beside the port it really listens on",
			lines:   []string{"warn: SENTRY_DSN is not set, error reporting disabled", "Server listening on port 3000"},
			context: runtimeCauseContext{runtime: RuntimePlanConfig{InternalPort: 8080}},
			want:    OutputCause{Code: "runtime_port_mismatch", Subjects: []string{"3000"}, Fix: &CauseFix{Kind: fixSetRuntime, Field: "runtime.internalPort", Value: "3000"}},
		},
		{
			name:      "a variable a Go program exits over",
			lines:     []string{"2026/09/24 12:00:00 DATABASE_URL is required"},
			container: ContainerDiagnostics{State: "restarting", ExitCode: 1, RestartCount: 2},
			context:   runtimeCauseContext{runtime: RuntimePlanConfig{InternalPort: 8080}},
			want:      OutputCause{Code: "runtime_env_missing", Subjects: []string{"DATABASE_URL"}, Fix: &CauseFix{Kind: fixAddVariable, Field: "variables.DATABASE_URL", Scope: "runtime"}},
		},
		{
			name:      "pm2 daemonized",
			lines:     []string{"[PM2] Spawning PM2 daemon with pm2_home=/root/.pm2", "[PM2] Done."},
			container: ContainerDiagnostics{State: "restarting", ExitCode: 0, RestartCount: 3},
			want:      OutputCause{Code: "runtime_start_exited", Fix: &CauseFix{Kind: fixReview, Field: "configuration.build.startCommand"}},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			container := test.container
			if container.State == "" {
				container.State = "running"
			}
			for _, text := range test.lines {
				container.Lines = append(container.Lines, RuntimeLogLine{Stream: "stderr", Text: text})
			}
			cause := applicationOutputCause([]ContainerDiagnostics{container}, test.context)
			if cause == nil || !reflect.DeepEqual(*cause, test.want) {
				t.Fatalf("cause = %+v\nwant    %+v", cause, test.want)
			}
			if cause.Fix != nil && cause.Fix.Value != "" && strings.ContainsAny(cause.Fix.Value, "\n\r") {
				t.Fatalf("fix value is not a single token: %q", cause.Fix.Value)
			}
			sentence := cause.sentence()
			if sentence == "" || (test.sentence != "" && !strings.Contains(sentence, test.sentence)) {
				t.Fatalf("sentence = %q, want it to contain %q", sentence, test.sentence)
			}
			if outputCauseTitles[cause.Code] == "" {
				t.Fatalf("%s has no title", cause.Code)
			}
		})
	}
}

func TestApplicationOutputCauseStaysSilentWithoutEvidence(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		port  int
		lines []string
	}{
		"a database on the project network": {3000, []string{"Error: connect ECONNREFUSED 10.0.0.2:5432"}},
		"Next printing the port it serves":  {3000, []string{"   - Local:        http://localhost:3000"}},
		"werkzeug bound to every address": {5000, []string{
			" * Running on all addresses (0.0.0.0)", " * Running on http://127.0.0.1:5000",
		}},
		"a cache the application connects to": {3000, []string{"Connected to redis server at localhost:6379"}},
		"a metrics listener beside the application's own": {3000, []string{
			"metrics server listening on port 9090", "Server listening on port 3000",
		}},
		"a JavaScript reference error":           {3000, []string{"ReferenceError: API_URL is not defined"}},
		"a request refused for its token":        {3000, []string{"Error: JWT is required to access /api/health"}},
		"a variable warning it carried on after": {3000, []string{"WARNING: REDIS_URL is not set, caching disabled"}},
	} {
		cause := applicationOutputCause([]ContainerDiagnostics{{State: "running", Lines: runtimeLines(test.lines...)}},
			runtimeCauseContext{runtime: RuntimePlanConfig{InternalPort: test.port}})
		if cause != nil {
			t.Fatalf("%s diagnosed as %+v", name, cause)
		}
	}
	if got := diagnosticsSuffix(&runtimeDiagnosticsEvidence{Available: true, Lines: 2}); got != "; the application's last output is in the build log" {
		t.Fatalf("suffix without a cause = %q", got)
	}
	if got := diagnosticsSuffix(&runtimeDiagnosticsEvidence{Available: true}); got != "" {
		t.Fatalf("suffix without output = %q", got)
	}
	if got := (&OutputCause{Code: "unknown"}).sentence(); got != "" {
		t.Fatalf("unknown cause sentence = %q", got)
	}
}

func TestReleaseTaskCauseNamesMigrationAndToolFailures(t *testing.T) {
	t.Parallel()
	cases := []struct {
		lines []string
		exit  int
		want  OutputCause
	}{
		{
			lines: []string{"Error: P3009", "migrate found failed migrations in the target database, new migrations will not be applied.", "The `20250101_init` migration started at 2026-09-01 failed"},
			want:  OutputCause{Code: "release_migration_failed_before", Detail: "prisma", Subjects: []string{"20250101_init"}},
		},
		{
			lines: []string{"Error: P3018", "A migration failed to apply. New migrations cannot be applied before the error is recovered from.", "Migration name: 20250102_users"},
			want:  OutputCause{Code: "release_migration_failed", Detail: "prisma", Subjects: []string{"20250102_users"}},
		},
		{
			lines: []string{"Error: P1001: Can't reach database server at `db-4.jd.internal:5432`"},
			want:  OutputCause{Code: "release_database_unreachable", Subjects: []string{"db-4.jd.internal:5432"}},
		},
		{
			lines: []string{"/bin/sh: npx: not found"},
			exit:  127,
			want:  OutputCause{Code: "release_task_command_not_found", Subjects: []string{"npx"}, Fix: &CauseFix{Kind: fixReview, Field: "configuration.build.releaseTasks"}},
		},
		{
			lines: []string{"warn: SHADOW_DATABASE_URL is not set", "Error: P1001: Can't reach database server at `db-4.jd.internal:5432`"},
			want:  OutputCause{Code: "release_database_unreachable", Subjects: []string{"db-4.jd.internal:5432"}},
		},
		{
			lines: []string{"Error: MIGRATION_TOKEN must be set"},
			want:  OutputCause{Code: "release_env_missing", Subjects: []string{"MIGRATION_TOKEN"}, Fix: &CauseFix{Kind: fixAddVariable, Field: "variables.MIGRATION_TOKEN", Scope: "release_task"}},
		},
		{
			lines: []string{"error: Environment variable not found: DATABASE_URL."},
			want:  OutputCause{Code: "release_env_missing", Detail: "prisma", Subjects: []string{"DATABASE_URL"}, Fix: &CauseFix{Kind: fixVariableScope, Field: "variables.DATABASE_URL", Value: "release_task", Scope: "release_task"}},
		},
	}
	variables := []ReleaseVariableSnapshot{{Name: "DATABASE_URL", Scopes: "runtime"}}
	for _, test := range cases {
		lines := []collectedLine{}
		for index, text := range test.lines {
			lines = append(lines, collectedLine{text: text, seq: int64(index + 1)})
		}
		cause := releaseTaskCause(lines, test.exit, variables)
		if cause == nil || !reflect.DeepEqual(*cause, test.want) {
			t.Fatalf("%v diagnosed as %+v, want %+v", test.lines, cause, test.want)
		}
		if cause.releaseSentence() == "" || outputCauseTitles[cause.Code] == "" {
			t.Fatalf("%s has no sentence or title", cause.Code)
		}
	}
	if cause := releaseTaskCause([]collectedLine{{text: "Applying migration 20250103"}}, 1, nil); cause != nil {
		t.Fatalf("unremarkable output diagnosed as %+v", cause)
	}
}

func runtimeLines(texts ...string) []RuntimeLogLine {
	lines := make([]RuntimeLogLine, 0, len(texts))
	for _, text := range texts {
		lines = append(lines, RuntimeLogLine{Stream: "stdout", Text: text})
	}
	return lines
}

// The causes read from environments and exit status, in the one cause table:
// each names its variable or port and never repeats the line it was read from.
func TestApplicationOutputCauseNamesTheEnvironment(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		line    string
		code    string
		subject string
	}{
		{"ArgumentError: Missing `secret_key_base` for 'production' environment, set this string with `bin/rails credentials:edit`", "runtime_env_missing", "SECRET_KEY_BASE"},
		{"ActiveSupport::MessageEncryptor::InvalidMessage (ActiveSupport::MessageEncryptor::InvalidMessage)", "runtime_master_key_invalid", "RAILS_MASTER_KEY"},
		{"django.core.exceptions.ImproperlyConfigured: The SECRET_KEY setting must not be empty.", "runtime_env_missing", "SECRET_KEY"},
		{"[auth][error] MissingSecret: Please define a `secret`.", "runtime_env_missing", "AUTH_SECRET"},
		{"[auth][error] UntrustedHost: Host must be trusted. URL was: https://app.example.com/api/auth/session", "runtime_auth_untrusted_host", "AUTH_TRUST_HOST"},
		{"[error] Could not check origin for Phoenix.Socket transport.", "runtime_origin_rejected", "PHX_HOST"},
		{"** (RuntimeError) environment variable DATABASE_URL is missing.", "runtime_env_missing", "DATABASE_URL"},
		{"2025/01/01 Error loading .env file", "runtime_dotenv_missing", ""},
		{"Error: connect ECONNREFUSED 127.0.0.1:5432", "runtime_database_localhost", "5432"},
		{"dial tcp [::1]:6379: connect: connection refused", "runtime_database_localhost", "6379"},
		{`connection to server at "localhost" (127.0.0.1), port 5432 failed: Connection refused`, "runtime_database_localhost", "5432"},
	} {
		cause := applicationOutputCause([]ContainerDiagnostics{{Lines: []RuntimeLogLine{{Text: test.line}}}}, runtimeCauseContext{})
		subject := ""
		if cause != nil && len(cause.Subjects) > 0 {
			subject = cause.Subjects[0]
		}
		if cause == nil || cause.Code != test.code || subject != test.subject || outputCauseTitles[cause.Code] == "" {
			t.Fatalf("%q = %+v", test.line, cause)
		}
		sentence := cause.sentence()
		if sentence == "" || strings.Contains(sentence, test.line) {
			t.Fatalf("%q sentence = %q", test.line, sentence)
		}
		if err := rejectPlanSecretLiteral("cause", sentence); err != nil {
			t.Fatalf("%q sentence is refused: %v", test.line, err)
		}
	}
	if cause := applicationOutputCause([]ContainerDiagnostics{{Lines: []RuntimeLogLine{{Text: "connect ECONNREFUSED 10.0.0.5:5432"}}}}, runtimeCauseContext{}); cause != nil {
		t.Fatalf("a remote refusal is not loopback: %+v", cause)
	}
	// Auth.js's untrusted host is answered with the variable that trusts it.
	untrusted := applicationOutputCause([]ContainerDiagnostics{{Lines: []RuntimeLogLine{{Text: "[auth][error] UntrustedHost: Host must be trusted."}}}}, runtimeCauseContext{})
	if untrusted.Fix == nil || untrusted.Fix.Kind != fixAddVariable || untrusted.Fix.Field != "variables.AUTH_TRUST_HOST" || untrusted.Fix.Value != "true" {
		t.Fatalf("untrusted host fix = %+v", untrusted.Fix)
	}
}

func TestApplicationOutputNamesARefusedPushAndAnUnwritableDatabase(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct{ line, code string }{
		{"Error: Use the --accept-data-loss flag to ignore the data loss warnings like prisma migrate reset", "runtime_schema_push_refused"},
		{"⚠️ We found changes that cannot be executed:", "runtime_schema_push_refused"},
		{"? Do you still want to push changes? » (y/N)", "runtime_schema_push_refused"},
		{"Is display_name column in users table created or renamed from another column?", "runtime_schema_push_refused"},
		{"Error: Interactive prompts require a TTY terminal (process.stdin.isTTY or process.stdout.isTTY is false)", "runtime_schema_push_refused"},
		{"SqliteError: attempt to write a readonly database", "runtime_sqlite_not_writable"},
		{"sqlite3.OperationalError: unable to open database file", "runtime_sqlite_not_writable"},
		{`relation "users" does not exist`, "schema_missing"},
	} {
		cause := applicationOutputCause([]ContainerDiagnostics{{Lines: []RuntimeLogLine{{Text: "starting"}, {Text: fixture.line}}}}, runtimeCauseContext{})
		if cause == nil || cause.Code != fixture.code || cause.sentence() == "" || outputCauseTitles[cause.Code] == "" {
			t.Errorf("%q: cause = %+v", fixture.line, cause)
		}
	}
	// "data loss" alone is a sentence any program may print.
	if cause := applicationOutputCause([]ContainerDiagnostics{{State: "running", Lines: []RuntimeLogLine{{Text: "backups prevent data loss"}}}}, runtimeCauseContext{}); cause != nil {
		t.Fatalf("an unrelated sentence was read as a refused push: %+v", cause)
	}
}

func TestStartCommandExitedIsDiagnosedFromTheExitCode(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		containers []ContainerDiagnostics
		cause      bool
	}{
		"exited 0":           {[]ContainerDiagnostics{{State: "exited", ExitCode: 0}}, true},
		"restarting after 0": {[]ContainerDiagnostics{{State: "restarting", ExitCode: 0, RestartCount: 4}}, true},
		"crashed":            {[]ContainerDiagnostics{{State: "exited", ExitCode: 1}}, false},
		"killed for memory":  {[]ContainerDiagnostics{{State: "exited", ExitCode: 0, OOMKilled: true}}, false},
		"still running":      {[]ContainerDiagnostics{{State: "running", ExitCode: 0, RestartCount: 3}}, false},
		"a compose one-shot": {[]ContainerDiagnostics{{State: "exited"}, {State: "running"}}, false},
	} {
		cause := applicationOutputCause(test.containers, runtimeCauseContext{})
		if (cause != nil && cause.Code == "runtime_start_exited") != test.cause {
			t.Fatalf("%s: %+v", name, cause)
		}
		if cause != nil && cause.Code == "runtime_start_exited" && !strings.Contains(cause.sentence(), "pm2-runtime") {
			t.Fatalf("%s: sentence %q", name, cause.sentence())
		}
	}
	// A missing table in the output is the more specific cause.
	cause := applicationOutputCause([]ContainerDiagnostics{{State: "exited", Lines: []RuntimeLogLine{{Text: `relation "users" does not exist`}}}}, runtimeCauseContext{})
	if cause == nil || cause.Code != "schema_missing" {
		t.Fatalf("schema cause = %+v", cause)
	}
}

// The run page titles a cause from its own table (failure-cause.ts); every
// code the build, runtime and release-task classifiers can name has a title
// on both sides, so a cause is never shown as its bare code.
func TestEveryNamedCauseHasATitleOnThePage(t *testing.T) {
	t.Parallel()
	content, err := os.ReadFile("../../../frontend/src/components/deploy/failure-cause.ts")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, table := range []map[string]string{causeTitles, outputCauseTitles} {
		for code, title := range table {
			// Codes that say where a run failed rather than why are labelled
			// by the stage on the page (failureLabel).
			if code == "health_gate_failed" || code == "candidate_start_failed" {
				continue
			}
			if !strings.Contains(page, code+`: "`+title+`"`) && !strings.Contains(page, `"`+code+`": "`+title+`"`) {
				t.Errorf("%s is %q here and not so on the run page", code, title)
			}
		}
	}
}
