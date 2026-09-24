package deploy

import (
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
			want:  OutputCause{Code: "runtime_origin_rejected", Detail: "phoenix"},
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
			want:  OutputCause{Code: "release_task_command_not_found", Subjects: []string{"npx"}, Fix: &CauseFix{Kind: fixReview, Field: "configuration.build.startCommand"}},
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
