package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// TestLiveBuildReachesItsDatabaseAndAppliesTheSchemaFirst builds, with the
// real recipe, buildx and a PostgreSQL container, a Node package whose build
// reads a table its Drizzle migration creates. The database is reachable only
// on its own network under a db-N.jd.internal alias, as a linked database is.
// The build runs on the networked builder: the first attempt reaches the
// database and fails because the table is missing (proving DNS and the route
// work from a build step), the schema step applies the migration from the
// build's own layers on the same builder, and the second attempt reads the
// row and bakes it into the image.
//
// JD_DEPLOY_LIVE=1 runs it; JD_BUILD_DATABASE_LIVE_REPO names another
// checkout to build the same way instead of the fixture (with
// JD_BUILD_DATABASE_LIVE_BUILD, _START, _MANAGER and _SCHEMA giving its
// commands), for proving a real application.
func TestLiveBuildReachesItsDatabaseAndAppliesTheSchemaFirst(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 on a Docker/Buildx release host to run the live database build")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	client := liveC4Docker(t)
	stamp := time.Now().UnixNano() % 1_000_000_000
	environmentID := int64(900_000_000) + stamp%99_000_000
	network := fmt.Sprintf("jd-live-build-db-%d", stamp)
	alias := fmt.Sprintf("db-%d.jd.internal", stamp%1_000_000+1)
	database := fmt.Sprintf("jd-live-build-pg-%d", stamp)
	tag := fmt.Sprintf("just-dashboard-build-database:%d", stamp)
	if _, err := liveDockerOutput(ctx, "network", "create", network); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = liveDockerOutput(context.Background(), "rm", "--force", database)
		_, _ = liveDockerOutput(context.Background(), "image", "rm", "--force", tag)
		_ = client.RemoveNetworkedBuilderState(context.Background(), dockerx.NetworkedBuilderName(network))
		_, _ = liveDockerOutput(context.Background(), "network", "rm", network)
	})
	if _, err := liveDockerOutput(ctx, "run", "-d", "--name", database, "--network", network, "--network-alias", alias,
		"-e", "POSTGRES_PASSWORD=live-build-password", "postgres:16-alpine"); err != nil {
		t.Fatal(err)
	}
	ready := time.Now().Add(2 * time.Minute)
	for {
		if _, err := liveDockerOutput(ctx, "exec", database, "pg_isready", "-U", "postgres", "-h", "127.0.0.1"); err == nil {
			break
		}
		if time.Now().After(ready) {
			t.Fatal("PostgreSQL did not become ready")
		}
		time.Sleep(time.Second)
	}
	// pg_isready answers before the image's init script restarts the server.
	time.Sleep(3 * time.Second)

	root, config, schema, query := liveDatabaseBuildSource(t)
	variables := map[string]string{"DATABASE_URL": "postgres://postgres:live-build-password@" + alias + ":5432/postgres"}
	secret := map[string]bool{"DATABASE_URL": true}
	if hosts := buildDatabaseHosts(variables); len(hosts) != 1 || hosts[0] != alias {
		t.Fatalf("hosts = %v", hosts)
	}

	builder := NewArtifactBuilder(NewDockerArtifactBackend(client).WithBuilderDir(t.TempDir()))
	prepared, err := builder.Prepare(ctx, root, config, false, tag, buildVariableNames(variables)...)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	networked, closeBuilder, err := builder.StartNetworkedBuilder(ctx, BuildNetwork{EnvironmentID: environmentID, Networks: []string{network}})
	if err != nil {
		t.Fatal(err)
	}
	defer closeBuilder(context.Background())
	t.Logf("networked builder started in %s", time.Since(started).Round(time.Millisecond))

	transcript := &strings.Builder{}
	build := func() (*buildOutputCollector, error) {
		seq := int64(0)
		collector := newBuildOutputCollector(func() int64 { return seq })
		started := time.Now()
		_, err := networked.Build(ctx, root, tag, config, prepared, variables, secret, "", SourceIdentity{}, nil, func(line BuildLog) error {
			seq++
			transcript.WriteString(line.Text + "\n")
			collector.observe(line)
			return nil
		})
		t.Logf("build took %s", time.Since(started).Round(time.Millisecond))
		return collector, err
	}
	collector, err := build()
	var failure *dockerx.BuildError
	if !errors.As(err, &failure) {
		t.Fatalf("the first build must fail reading the missing table: %v\n%s", err, tail(transcript.String(), 60))
	}
	cause := buildFailureCause(err, collector, causeContext{build: config, prepared: prepared}, nil)
	if cause == nil || cause.Code != "build_database_schema_missing" {
		t.Fatalf("cause = %+v\n%s", cause, tail(transcript.String(), 60))
	}
	if strings.Contains(transcript.String(), "live-build-password") {
		t.Fatal("the database password reached the transcript")
	}
	t.Logf("first attempt named: %s", cause.sentence())

	content, err := readContainedRegular(root, prepared.Dockerfile, 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	dockerfile, ok := schemaDockerfile(string(content), config.BuildCommand, schema, []string{"DATABASE_URL"})
	if !ok {
		t.Fatalf("no schema Dockerfile from:\n%s", content)
	}
	schemaPath := ".just-dashboard/" + schemaDockerfileName
	if err := writeGeneratedFile(root, schemaDockerfileName, dockerfile); err != nil {
		t.Fatal(err)
	}
	started = time.Now()
	if err := networked.runSchemaStep(ctx, BuildInvocation{
		ContextDir: root, Dockerfile: schemaPath,
		Secrets:   []BuildSecretValue{{ID: "DATABASE_URL", Step: "build", Value: variables["DATABASE_URL"]}},
		BuildArgs: []BuildArgValue{{Name: schemaRunArg, Value: fmt.Sprint(time.Now().UnixNano())}},
	}, func(line BuildLog) error {
		transcript.WriteString(line.Text + "\n")
		return nil
	}); err != nil {
		t.Fatalf("schema step: %v\n%s", err, tail(transcript.String(), 60))
	}
	t.Logf("schema step took %s", time.Since(started).Round(time.Millisecond))
	// A real application may need rows as well as tables: name the cause its
	// build stops on before they exist (JD_BUILD_DATABASE_LIVE_EMPTY_CAUSE).
	if expected := os.Getenv("JD_BUILD_DATABASE_LIVE_EMPTY_CAUSE"); expected != "" {
		collector, err := build()
		cause := buildFailureCause(err, collector, causeContext{build: config, prepared: prepared,
			candidate: &DetectedCandidate{NodeBuild: &DetectedNodeBuild{CacheComponents: true}}}, nil)
		if err == nil || cause == nil || cause.Code != expected {
			t.Fatalf("before the rows: err = %v, cause = %+v\n%s", err, cause, tail(transcript.String(), 60))
		}
		t.Logf("before the rows, named: %s", cause.sentence())
	}
	if query != "" {
		if _, err := liveDockerOutput(ctx, "exec", database, "psql", "-U", "postgres", "-c", query); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := build(); err != nil {
		t.Fatalf("the build after the schema step: %v\n%s", err, tail(transcript.String(), 80))
	}
	if os.Getenv("JD_BUILD_DATABASE_LIVE_REPO") == "" {
		output, err := liveDockerOutput(ctx, "run", "--rm", "--entrypoint", "cat", tag, "/app/dist/slugs.json")
		if err != nil || !strings.Contains(string(output), "from-the-database") {
			t.Fatalf("the image does not hold what the build read: %v %s", err, output)
		}
	}
	// Closed, the builder waits on the network for the environment's next
	// build; removing the environment's builder takes it off.
	closeBuilder(context.Background())
	assertLiveNetworkEndpoints(t, ctx, network, "2", "after close")
	if err := client.RemoveNetworkedBuilderState(ctx, dockerx.NetworkedBuilderName(network)); err != nil {
		t.Fatal(err)
	}
	assertLiveNetworkEndpoints(t, ctx, network, "1", "after the builder's removal")
}

// assertLiveNetworkEndpoints checks how many containers a network holds.
func assertLiveNetworkEndpoints(t *testing.T, ctx context.Context, network, want, when string) {
	t.Helper()
	inspect, err := liveDockerOutput(ctx, "network", "inspect", network, "--format", "{{len .Containers}}")
	if err != nil || strings.TrimSpace(string(inspect)) != want {
		t.Fatalf("network endpoints %s = %s (%v), want %s", when, inspect, err, want)
	}
}

// liveDatabaseBuildSource is the package the live test builds: the fixture,
// or the checkout JD_BUILD_DATABASE_LIVE_REPO names.
func liveDatabaseBuildSource(t *testing.T) (string, BuildPlanConfig, string, string) {
	t.Helper()
	if repo := os.Getenv("JD_BUILD_DATABASE_LIVE_REPO"); repo != "" {
		root := t.TempDir()
		if output, err := exec.Command("cp", "-a", repo+"/.", root).CombinedOutput(); err != nil {
			t.Fatalf("copy %s: %v %s", repo, err, output)
		}
		_ = os.RemoveAll(filepath.Join(root, ".git"))
		return root, BuildPlanConfig{
			Method: BuildRecipe, Recipe: "node", PackageManager: os.Getenv("JD_BUILD_DATABASE_LIVE_MANAGER"),
			BuildCommand: os.Getenv("JD_BUILD_DATABASE_LIVE_BUILD"), StartCommand: os.Getenv("JD_BUILD_DATABASE_LIVE_START"),
		}, os.Getenv("JD_BUILD_DATABASE_LIVE_SCHEMA"), os.Getenv("JD_BUILD_DATABASE_LIVE_SQL")
	}
	root := t.TempDir()
	writeBuildFixture(t, root, "package.json", `{"name":"build-reads-database","version":"1.0.0","type":"module","private":true,`+
		`"scripts":{"build":"node scripts/build.mjs","start":"node server.mjs"},`+
		`"dependencies":{"drizzle-orm":"0.45.4","postgres":"3.4.7"},"devDependencies":{"drizzle-kit":"0.31.11"}}`)
	writeBuildFixture(t, root, "drizzle.config.js", `export default { dialect: "postgresql", schema: "./schema.js", out: "./drizzle", dbCredentials: { url: process.env.DATABASE_URL } }`+"\n")
	writeBuildFixture(t, root, "schema.js", `import { pgTable, serial, text } from "drizzle-orm/pg-core"`+"\n"+`export const posts = pgTable("posts", { id: serial("id").primaryKey(), slug: text("slug").notNull() })`+"\n")
	writeBuildFixture(t, root, "drizzle/meta/_journal.json", `{"version":"7","dialect":"postgresql","entries":[{"idx":0,"version":"7","when":1700000000000,"tag":"0000_posts","breakpoints":true}]}`)
	writeBuildFixture(t, root, "drizzle/0000_posts.sql", `CREATE TABLE "posts" ("id" serial PRIMARY KEY NOT NULL, "slug" text NOT NULL);`+"\n")
	writeBuildFixture(t, root, "scripts/build.mjs", `import { mkdirSync, writeFileSync } from "node:fs"
import postgres from "postgres"
const sql = postgres(process.env.DATABASE_URL)
const rows = await sql`+"`select slug from posts`"+`
mkdirSync("dist", { recursive: true })
writeFileSync("dist/slugs.json", JSON.stringify(rows.map((row) => row.slug)))
await sql.end()
`)
	writeBuildFixture(t, root, "server.mjs", `import { createServer } from "node:http"
createServer((_, response) => response.end("ok")).listen(process.env.PORT || 3000)
`)
	lock := exec.Command("npm", "install", "--package-lock-only", "--ignore-scripts", "--no-audit", "--no-fund")
	lock.Dir = root
	if output, err := lock.CombinedOutput(); err != nil {
		t.Fatalf("npm lockfile: %v %s", err, output)
	}
	return root, BuildPlanConfig{
		Method: BuildRecipe, Recipe: "node", PackageManager: "npm",
		BuildCommand: "npm run build", StartCommand: "npx drizzle-kit migrate && node server.mjs",
	}, "npx drizzle-kit migrate", "insert into posts(slug) values ('from-the-database')"
}

func tail(text string, lines int) string {
	parts := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	return strings.Join(parts, "\n")
}

// TestLiveBuildStepBuildsAnApplicationThatReadsItsNewDatabase runs the build
// step itself — the executor with its store, the real recipe, buildx and
// BuildKit — for an application whose build reads its new database, as a
// first deploy of a Next.js site with Cache Components does: the build joins
// the database's network, reads a table its migration creates and gets the
// schema applied first, then needs rows the new database does not have yet
// and gets the project's seed, and builds. Only the network owner (the
// environment's network is the test's) and the row count (psql in the
// database's container) stand in for the API's.
//
// JD_DEPLOY_LIVE=1 runs it on a fixture; JD_BUILD_DATABASE_LIVE_REPO names a
// real checkout instead, with JD_BUILD_DATABASE_LIVE_BUILD, _START, _MANAGER
// and _SEED giving its commands.
func TestLiveBuildStepBuildsAnApplicationThatReadsItsNewDatabase(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 on a Docker/Buildx release host to run the live database build step")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	client := liveC4Docker(t)
	stamp := time.Now().UnixNano() % 1_000_000_000
	// Far from any environment this server deploys: the step names its
	// image after the environment.
	environmentID := int64(900_000_000) + stamp%99_000_000
	network, alias, database := liveBuildDatabaseServer(t, ctx, stamp)
	t.Cleanup(func() {
		_ = client.RemoveNetworkedBuilderState(context.Background(), dockerx.NetworkedBuilderName(network))
	})

	root, config, candidate := liveSeededBuildSource(t)
	backend := NewDockerArtifactBackend(client).WithBuilderDir(t.TempDir())
	prepared, err := NewArtifactBuilder(backend).Prepare(ctx, root, config, false, "just-dashboard-live:prepare", "DATABASE_URL")
	if err != nil {
		t.Fatal(err)
	}
	executor, execution, plan := databaseBuildRun(t, databaseBuildOptions{
		backend: backend, build: config, workspace: root, prepared: &prepared, candidate: candidate, environmentID: environmentID,
		value: "postgres://postgres:live-build-password@" + alias + ":5432/postgres",
	})
	tag := releaseImageTag(execution.Run.EnvironmentID, execution.Run.ID)
	t.Cleanup(func() { _, _ = liveDockerOutput(context.Background(), "image", "rm", "--force", tag) })
	executor.buildNetworks = &staticBuildNetworks{networks: []string{network}}
	executor.databases = &psqlDatabaseContents{container: database}

	started := time.Now()
	result := executor.buildArtifact(ctx, execution, plan)
	transcript := stepTranscript(t, execution)
	if result.State != StepPassed {
		t.Fatalf("result = %+v\n%s", result, tail(transcript, 80))
	}
	t.Logf("the build step took %s", time.Since(started).Round(time.Second))
	var evidence builtStepEvidence
	if err := json.Unmarshal(result.Evidence, &evidence); err != nil {
		t.Fatal(err)
	}
	if len(evidence.DatabaseSteps) != 2 || evidence.DatabaseSteps[0].Kind != "schema" || evidence.DatabaseSteps[1].Kind != "seed" {
		t.Fatalf("database steps = %+v\n%s", evidence.DatabaseSteps, tail(transcript, 80))
	}
	for _, line := range strings.Split(transcript, "\n") {
		if strings.HasPrefix(line, "The ") && (strings.Contains(line, "network") || strings.Contains(line, "schema") || strings.Contains(line, "seed")) {
			t.Log(line)
		}
	}
	if strings.Contains(transcript, "live-build-password") || strings.Contains(transcript, "admin-password-value") {
		t.Fatal("a secret reached the transcript")
	}
	if os.Getenv("JD_BUILD_DATABASE_LIVE_REPO") == "" {
		output, err := liveDockerOutput(ctx, "run", "--rm", "--entrypoint", "cat", tag, "/app/dist/slugs.json")
		if err != nil || !strings.Contains(string(output), "from-the-seed") {
			t.Fatalf("the image does not hold what the build read: %v %s", err, output)
		}
	}
	// The builder waits on the network once the step is over, until the
	// environment's builder is removed.
	assertLiveNetworkEndpoints(t, ctx, network, "2", "after the step")
	if err := client.RemoveNetworkedBuilderState(ctx, dockerx.NetworkedBuilderName(network)); err != nil {
		t.Fatal(err)
	}
	assertLiveNetworkEndpoints(t, ctx, network, "1", "after the builder's removal")
}

// liveBuildDatabaseServer starts PostgreSQL on a network of its own under a
// db-N.jd.internal alias, as a linked database is, and removes both after.
func liveBuildDatabaseServer(t *testing.T, ctx context.Context, stamp int64) (string, string, string) {
	t.Helper()
	network := fmt.Sprintf("jd-live-build-db-%d", stamp)
	alias := fmt.Sprintf("db-%d.jd.internal", stamp%1_000_000+1)
	database := fmt.Sprintf("jd-live-build-pg-%d", stamp)
	if _, err := liveDockerOutput(ctx, "network", "create", network); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = liveDockerOutput(context.Background(), "rm", "--force", database)
		_, _ = liveDockerOutput(context.Background(), "network", "rm", network)
	})
	if _, err := liveDockerOutput(ctx, "run", "-d", "--name", database, "--network", network, "--network-alias", alias,
		"-e", "POSTGRES_PASSWORD=live-build-password", "postgres:16-alpine"); err != nil {
		t.Fatal(err)
	}
	ready := time.Now().Add(2 * time.Minute)
	for {
		if _, err := liveDockerOutput(ctx, "exec", database, "pg_isready", "-U", "postgres", "-h", "127.0.0.1"); err == nil {
			break
		}
		if time.Now().After(ready) {
			t.Fatal("PostgreSQL did not become ready")
		}
		time.Sleep(time.Second)
	}
	// pg_isready answers before the image's init script restarts the server.
	time.Sleep(3 * time.Second)
	return network, alias, database
}

type staticBuildNetworks struct{ networks []string }

func (s *staticBuildNetworks) NetworksForBuild(context.Context, int64, map[string]string) ([]string, error) {
	return s.networks, nil
}

// psqlDatabaseContents counts rows the way the API does, through psql in the
// database's own container, leaving Drizzle's records out.
type psqlDatabaseContents struct{ container string }

func (p *psqlDatabaseContents) DatabaseHasRows(ctx context.Context, _ int64) (bool, error) {
	list, err := liveDockerOutput(ctx, "exec", p.container, "psql", "-U", "postgres", "-tAc",
		`SELECT format('%I.%I', schemaname, tablename) FROM pg_tables WHERE schemaname NOT IN ('pg_catalog', 'information_schema', 'drizzle')`)
	if err != nil {
		return true, err
	}
	for _, table := range strings.Fields(string(list)) {
		present, err := liveDockerOutput(ctx, "exec", p.container, "psql", "-U", "postgres", "-tAc", "SELECT EXISTS (SELECT 1 FROM "+table+")")
		if err != nil {
			return true, err
		}
		if strings.TrimSpace(string(present)) == "t" {
			return true, nil
		}
	}
	return false, nil
}

// liveSeededBuildSource is a package whose build reads rows its seed loads,
// failing the way Next.js with Cache Components does when there are none, or
// the checkout JD_BUILD_DATABASE_LIVE_REPO names.
func liveSeededBuildSource(t *testing.T) (string, BuildPlanConfig, map[string]any) {
	t.Helper()
	if repo := os.Getenv("JD_BUILD_DATABASE_LIVE_REPO"); repo != "" {
		root, config, _, _ := liveDatabaseBuildSource(t)
		return root, config, map[string]any{
			"framework": "nextjs", "recipe": "node", "schemaTool": "drizzle", "seedResets": true,
			"seedCommand": os.Getenv("JD_BUILD_DATABASE_LIVE_SEED"), "nodeBuild": map[string]any{"cacheComponents": true},
		}
	}
	root, config, _, _ := liveDatabaseBuildSource(t)
	package_ := `{"name":"build-reads-database","version":"1.0.0","type":"module","private":true,` +
		`"scripts":{"build":"node scripts/build.mjs","start":"node server.mjs","db:seed":"node scripts/seed.mjs"},` +
		`"dependencies":{"drizzle-orm":"0.45.4","postgres":"3.4.7"},"devDependencies":{"drizzle-kit":"0.31.11"}}`
	writeBuildFixture(t, root, "package.json", package_)
	// Next.js's own words when generateStaticParams returns nothing.
	writeBuildFixture(t, root, "scripts/build.mjs", `import { mkdirSync, writeFileSync } from "node:fs"
import postgres from "postgres"
const sql = postgres(process.env.DATABASE_URL)
const rows = await sql`+"`select slug from posts`"+`
await sql.end()
if (rows.length === 0) {
  console.error("Error: When using Cache Components, all `+"`generateStaticParams`"+` functions must return at least one result.")
  console.error("Error: Failed to collect page data for /blog/[slug]")
  process.exit(1)
}
mkdirSync("dist", { recursive: true })
writeFileSync("dist/slugs.json", JSON.stringify(rows.map((row) => row.slug)))
`)
	writeBuildFixture(t, root, "scripts/seed.mjs", `import postgres from "postgres"
if (!process.env.ADMIN_PASSWORD) throw new Error("ADMIN_PASSWORD is required to seed")
const sql = postgres(process.env.DATABASE_URL)
await sql`+"`truncate table posts restart identity`"+`
await sql`+"`insert into posts(slug) values ('from-the-seed')`"+`
await sql.end()
`)
	return root, config, map[string]any{
		"framework": "nextjs", "recipe": "node", "schemaTool": "drizzle", "seedCommand": "npm run db:seed", "seedResets": true,
		"nodeBuild": map[string]any{"cacheComponents": true},
	}
}
