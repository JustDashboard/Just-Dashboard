package deploy

import (
	"slices"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// The schema Dockerfile is the recipe's own cut at the build command, so
// every instruction before it is one BuildKit has just cached; the step that
// replaces it mounts the step's values and is never served from the cache.
func TestSchemaDockerfileReplacesTheBuildStep(t *testing.T) {
	t.Parallel()
	recipe := strings.Join([]string{
		"# syntax=docker/dockerfile:1.10",
		"FROM node:22-alpine@sha256:1 AS build",
		"WORKDIR /app",
		"COPY . .",
		"RUN bun install --frozen-lockfile",
		"ENV PATH=/app/node_modules/.bin:$PATH",
		"RUN --mount=type=secret,id=DATABASE_URL,env=DATABASE_URL,required=true export NEXT_TELEMETRY_DISABLED=1 && bun run build",
		"FROM node:22-alpine@sha256:1",
		"COPY --from=build /app /app",
		`CMD ["/bin/sh","-c","bunx drizzle-kit migrate && bun run start"]`,
	}, "\n") + "\n"
	got, ok := schemaDockerfile(recipe, "bun run build", "bunx drizzle-kit migrate", []string{"SESSION_SECRET", "DATABASE_URL"})
	if !ok {
		t.Fatal("no schema Dockerfile")
	}
	want := strings.Join([]string{
		"# syntax=docker/dockerfile:1.10",
		"FROM node:22-alpine@sha256:1 AS build",
		"WORKDIR /app",
		"COPY . .",
		"RUN bun install --frozen-lockfile",
		"ENV PATH=/app/node_modules/.bin:$PATH",
		"ARG JD_SCHEMA_RUN",
		"RUN --mount=type=secret,id=DATABASE_URL,env=DATABASE_URL,required=true --mount=type=secret,id=SESSION_SECRET,env=SESSION_SECRET,required=true bunx drizzle-kit migrate",
	}, "\n") + "\n"
	if got != want {
		t.Fatalf("schema Dockerfile =\n%s\nwant\n%s", got, want)
	}
	for _, test := range []struct{ name, content, build, schema string }{
		{"no build step", "FROM node\nRUN npm ci\n", "npm run build", "npx prisma migrate deploy"},
		{"no build command", recipe, "", "npx prisma migrate deploy"},
		{"a command spanning lines", recipe, "bun run build", "npx prisma migrate deploy\nrm -rf /"},
		{"the build step first", "RUN bun run build\n", "bun run build", "npx prisma migrate deploy"},
	} {
		if _, ok := schemaDockerfile(test.content, test.build, test.schema, nil); ok {
			t.Errorf("%s: a schema Dockerfile was made", test.name)
		}
	}
}

// The schema step is the one the plan already runs after the build: the
// start command's leading migration, or an image release task's. A tool
// that migrates from what the build writes cannot run before it.
func TestPlanSchemaStepIsTheMigrationThePlanRunsAfterTheBuild(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		build BuildPlanConfig
		want  buildSchemaStep
		ok    bool
	}{
		{"drizzle in the start command", BuildPlanConfig{StartCommand: "bunx drizzle-kit migrate && bun run start"},
			buildSchemaStep{Command: "bunx drizzle-kit migrate", Scope: "runtime"}, true},
		{"prisma after another step", BuildPlanConfig{StartCommand: "node scripts/check.js && npx prisma migrate deploy && node server.js"},
			buildSchemaStep{Command: "npx prisma migrate deploy", Scope: "runtime"}, true},
		{"a release task", BuildPlanConfig{StartCommand: "node server.js", ReleaseTasks: []ReleaseTaskConfig{
			{Name: "migrate", Command: "npx prisma migrate deploy", Runner: ReleaseTaskRunnerImage, Env: []string{"DIRECT_URL"}}}},
			buildSchemaStep{Command: "npx prisma migrate deploy", Scope: "release_task", Names: []string{"DIRECT_URL"}}, true},
		{"a host shell task", BuildPlanConfig{StartCommand: "node server.js", ReleaseTasks: []ReleaseTaskConfig{
			{Name: "migrate", Command: "npx prisma migrate deploy"}}}, buildSchemaStep{}, false},
		{"the server alone", BuildPlanConfig{StartCommand: "npx prisma migrate deploy"}, buildSchemaStep{}, false},
		{"Adonis migrates from its build", BuildPlanConfig{StartCommand: "node build/ace.js migration:run --force && node build/bin/server.js"}, buildSchemaStep{}, false},
		{"no migration", BuildPlanConfig{StartCommand: "npm run start"}, buildSchemaStep{}, false},
	} {
		got, ok := planSchemaStep(test.build)
		if ok != test.ok || got.Command != test.want.Command || got.Scope != test.want.Scope || !slices.Equal(got.Names, test.want.Names) {
			t.Errorf("%s: step = %+v, %v", test.name, got, ok)
		}
	}
}

// Only the dashboard's own database hosts send a build to the network, and
// only a saved connection's name is one a seed can be judged against.
func TestBuildDatabaseHostsAndTheirConnections(t *testing.T) {
	t.Parallel()
	hosts := buildDatabaseHosts(map[string]string{
		"DATABASE_URL": "postgres://app:secret@db-8.jd.internal:5432/app",
		"POOLED_URL":   "postgres://app:secret@db-8-pool.jd.internal:6432/app",
		"BRANCH_URL":   "postgres://app:secret@db-3-branch.jd.internal:5432/app",
		"HOSTED_URL":   "postgres://app:secret@ep-cool.neon.tech/app",
		"LINK_TEXT":    "${{database.8}}",
		"SITE_URL":     "https://shop.example",
	})
	if want := []string{"db-3-branch.jd.internal", "db-8-pool.jd.internal", "db-8.jd.internal"}; !slices.Equal(hosts, want) {
		t.Fatalf("hosts = %v", hosts)
	}
	if ids := databaseConnectionIDs(hosts); !slices.Equal(ids, []int64{8}) {
		t.Fatalf("ids = %v", ids)
	}
}

// A build that reads a table its migrations have not created is named as
// such, whichever driver says it; a NOTICE from a DROP … IF EXISTS is not.
func TestBuildSchemaMissingIsNamedFromEveryDriver(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, line, subject string
	}{
		{"postgres.js", `#14 62.76   [cause]: PostgresError: relation "posts" does not exist`, "posts"},
		{"node-postgres", `#14 41.2 error: column "video" does not exist`, "video"},
		{"qualified column", `#14 41.2 PostgresError: column posts.video does not exist`, "posts.video"},
		{"prisma table", "#14 30.1 The table `public.Post` does not exist in the current database.", "public.Post"},
		{"prisma column", "#14 30.1 The column `Post.video` does not exist in the current database.", "Post.video"},
		{"mysql", `#14 22.0 Error: Table 'app.posts' doesn't exist`, "app.posts"},
		{"mysql column", `#14 22.0 Error: Unknown column 'video' in 'field list'`, "video"},
		{"sqlite", `#14 3.1 SqliteError: no such table: posts`, "posts"},
	} {
		lines := []collectedLine{{text: "#14 59.27   Collecting page data using 11 workers ..."}, {text: test.line, seq: 7}}
		match := classifyLines(buildSignatures, lines, 1)
		if match == nil || match.code != "build_database_schema_missing" || !slices.Contains(match.subjects, test.subject) {
			t.Errorf("%s: match = %+v", test.name, match)
		}
	}
	notice := []collectedLine{{text: `#14 2.0 NOTICE:  relation "old_posts" does not exist, skipping`}}
	if match := classifyLines(buildSignatures, notice, 1); match != nil && match.code == "build_database_schema_missing" {
		t.Fatalf("a NOTICE was named: %+v", match)
	}
}

// Next.js with Cache Components refuses a generateStaticParams that returns
// nothing; the page is the subject.
func TestEmptyStaticParamsAreNamedWithTheirPage(t *testing.T) {
	t.Parallel()
	lines := []collectedLine{
		{text: "#14 53.37 Error: When using Cache Components, all `generateStaticParams` functions must return at least one result. This is to ensure that we can perform build-time validation that there is no other dynamic accesses that would cause a runtime error.", seq: 3},
		{text: "#14 53.56 Error: Failed to collect page data for /[locale]/blog/[slug]", seq: 4},
	}
	match := classifyLines(buildSignatures, lines, 1)
	if match == nil || match.code != "build_static_params_empty" || !slices.Equal(match.subjects, []string{"/[locale]/blog/[slug]"}) {
		t.Fatalf("match = %+v", match)
	}
}

// What a database cause suggests depends on what Next.js allows: with Cache
// Components a route segment config is refused, so the remedy never names
// one; and an address that means the build container itself says so.
func TestDatabaseRemediesFollowTheApplication(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		cause BuildCause
		want  []string
		never []string
	}{
		{"cache components", BuildCause{Code: "build_database_schema_missing", Phase: phaseBuild, Command: "bun run build", ExitCode: 1, Subjects: []string{"posts"}, Detail: detailNextCacheComponents},
			[]string{"the build read posts", "before `bun run build`", "<Suspense>"}, []string{"force-dynamic"}},
		{"without cache components", BuildCause{Code: "build_database_schema_missing", Phase: phaseBuild, Command: "npm run build", ExitCode: 1, Subjects: []string{"posts"}},
			[]string{"force-dynamic"}, []string{"<Suspense>"}},
		{"after the schema step", BuildCause{Code: "build_database_schema_missing", Phase: phaseBuild, Command: "npm run build", ExitCode: 1, Subjects: []string{"posts"}, Detail: detailSchemaApplied},
			[]string{"still does not have after the plan's migrations ran"}, nil},
		{"a linked database that did not answer", BuildCause{Code: "build_database_unreachable", Phase: phaseBuild, Command: "npm run build", ExitCode: 1, Subjects: []string{"db-8.jd.internal"}},
			[]string{"joined the environment's network", "Databases page"}, nil},
		{"loopback", BuildCause{Code: "build_database_unreachable", Phase: phaseBuild, Command: "npm run build", ExitCode: 1, Subjects: []string{"127.0.0.1:5432"}},
			[]string{"inside the build is the build container itself", "Link the database in Variables"}, nil},
		{"empty params", BuildCause{Code: "build_static_params_empty", Phase: phaseBuild, Command: "bun run build", ExitCode: 1, Subjects: []string{"/[locale]/blog/[slug]"}, Detail: detailNextCacheComponents},
			[]string{"generateStaticParams of page /[locale]/blog/[slug] returned no params", "notFound()"}, []string{"force-dynamic"}},
	} {
		sentence := test.cause.sentence()
		for _, want := range test.want {
			if !strings.Contains(sentence, want) {
				t.Errorf("%s: %q lacks %q", test.name, sentence, want)
			}
		}
		for _, never := range test.never {
			if strings.Contains(sentence, never) {
				t.Errorf("%s: %q says %q", test.name, sentence, never)
			}
		}
		if causeTitle(test.cause.Code) == "" {
			t.Errorf("%s: %s has no title", test.name, test.cause.Code)
		}
	}
}

// The detail comes from what detection read, not from the output.
func TestCacheComponentsComesFromNextConfig(t *testing.T) {
	t.Parallel()
	for text, want := range map[string]bool{
		"const nextConfig: NextConfig = {\n  cacheComponents: true,\n}": true,
		"module.exports = { experimental: { cacheComponents: true } }":  true,
		"export default { experimental: { dynamicIO: true } }":          true,
		"export default { cacheComponents: false }":                     false,
		"export default { output: 'standalone' }":                       false,
		"export default { myCacheComponents: true }":                    false,
	} {
		if got := nextCacheComponents(text); got != want {
			t.Errorf("%q: %v", text, got)
		}
	}
	collector := newBuildOutputCollector(nil)
	for _, line := range []string{
		"#14 [build 4/4] RUN bun run build",
		"#14 62.76 PostgresError: relation \"posts\" does not exist",
	} {
		collector.observe(BuildLog{Stream: "stderr", Text: line})
	}
	context := causeContext{
		build:     BuildPlanConfig{Method: BuildRecipe, Recipe: "node", BuildCommand: "bun run build"},
		candidate: &DetectedCandidate{NodeBuild: &DetectedNodeBuild{CacheComponents: true}},
	}
	cause := buildFailureCause(&dockerx.BuildError{Step: 14, Command: "bun run build", ExitCode: 1}, collector, context, nil)
	if cause == nil || cause.Code != "build_database_schema_missing" || cause.Detail != detailNextCacheComponents {
		t.Fatalf("cause = %+v", cause)
	}
	context.schemaApplied = true
	if cause := buildFailureCause(&dockerx.BuildError{Step: 14, Command: "bun run build", ExitCode: 1}, collector, context, nil); cause == nil || cause.Detail != detailSchemaApplied {
		t.Fatalf("cause = %+v", cause)
	}
}
