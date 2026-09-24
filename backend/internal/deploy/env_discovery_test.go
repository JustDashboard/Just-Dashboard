package deploy

import (
	"context"
	"fmt"
	"reflect"
	"testing"
)

// The variables a repository documents in .env.example come first, in the
// file's own order and with their example values as hints; the ones only
// the code reads follow by name. A credential-shaped example is dropped, a
// committed .env contributes names alone, and tests, fixtures and the names
// the platform itself supplies are left out.
func TestEnvironmentDiscoveryReadsTemplatesAndCode(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeBuildFixture(t, root, "package.json", `{"scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"15","pg":"8"}}`)
	writeBuildFixture(t, root, "package-lock.json", `{}`)
	writeBuildFixture(t, root, ".env.example", "# database\nDATABASE_URL=postgres://user:pass@localhost:5432/app\nexport SESSION_SECRET=\"change me\" # rotate\nNEXT_PUBLIC_SITE_URL='https://example.test'\nPORT=3000\nLOG_LEVEL=info\n")
	writeBuildFixture(t, root, ".env", "STRIPE_KEY=sk_live_real_secret_value\nLOG_LEVEL=debug\n")
	writeBuildFixture(t, root, "src/lib/mail.ts", "const key = process.env.RESEND_API_KEY ?? process.env['MAIL_FROM']\nconst env = import.meta.env.VITE_ANALYTICS_ID\n")
	writeBuildFixture(t, root, "src/lib/mail.test.ts", "process.env.ONLY_IN_TESTS")
	writeBuildFixture(t, root, "tests/setup.js", "process.env.ALSO_ONLY_IN_TESTS")
	writeBuildFixture(t, root, "scripts/seed.py", "import os\nos.environ['SEED_TOKEN']\nos.getenv(\"DATABASE_URL\")\nconfig('ADMIN_EMAIL')\n")
	writeBuildFixture(t, root, "tools/main.go", "package main\nimport \"os\"\nfunc main() { _ = os.Getenv(\"GO_FLAG\"); _, _ = os.LookupEnv(\"HOME\") }\n")
	result, err := (Detector{}).DetectPath(context.Background(), root, SourceIdentity{})
	if err != nil || len(result.Candidates) != 1 {
		t.Fatalf("detect: %+v, %v", result, err)
	}
	got := result.Candidates[0].Variables
	want := []DetectedVariable{
		{Name: "DATABASE_URL", Sources: []string{".env.example", "scripts/seed.py"}},
		{Name: "SESSION_SECRET", Example: "change me", Sources: []string{".env.example"}},
		{Name: "NEXT_PUBLIC_SITE_URL", Example: "https://example.test", Sources: []string{".env.example"}},
		{Name: "LOG_LEVEL", Example: "info", Sources: []string{".env.example", ".env"}},
		{Name: "STRIPE_KEY", Sources: []string{".env"}},
		{Name: "ADMIN_EMAIL", Sources: []string{"scripts/seed.py"}},
		{Name: "GO_FLAG", Sources: []string{"tools/main.go"}},
		{Name: "MAIL_FROM", Sources: []string{"src/lib/mail.ts"}},
		{Name: "RESEND_API_KEY", Sources: []string{"src/lib/mail.ts"}},
		{Name: "SEED_TOKEN", Sources: []string{"scripts/seed.py"}, Required: true},
		{Name: "VITE_ANALYTICS_ID", Sources: []string{"src/lib/mail.ts"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("variables:\n got %+v\nwant %+v", got, want)
	}
	databases := result.Candidates[0].Databases
	if len(databases) != 1 || databases[0].Engine != "postgres" || databases[0].Variable != "DATABASE_URL" || databases[0].Evidence != "pg in package.json" {
		t.Fatalf("databases: %+v", databases)
	}
	if result.Truncated {
		t.Fatal("environment discovery must not count against detection's own limits")
	}
}

// A repository-level .env.example describes the application in apps/web
// even though nothing else at the repository root is that root's own; a
// nested root keeps its own file to itself.
func TestEnvironmentDiscoveryFollowsRoots(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeBuildFixture(t, root, ".env.example", "SHARED=1\n")
	writeBuildFixture(t, root, "apps/web/package.json", `{"scripts":{"build":"vite build"},"devDependencies":{"vite":"6"}}`)
	writeBuildFixture(t, root, "apps/web/package-lock.json", `{}`)
	writeBuildFixture(t, root, "apps/web/src/main.ts", "import.meta.env.VITE_API_URL")
	writeBuildFixture(t, root, "apps/api/package.json", `{"scripts":{"start":"node index.js"},"dependencies":{"express":"4","ioredis":"5"}}`)
	writeBuildFixture(t, root, "apps/api/package-lock.json", `{}`)
	writeBuildFixture(t, root, "apps/api/.env.example", "REDIS_URL=redis://localhost:6379\n")
	result, err := (Detector{}).DetectPath(context.Background(), root, SourceIdentity{})
	if err != nil || len(result.Candidates) != 2 {
		t.Fatalf("detect: %+v, %v", result, err)
	}
	for _, candidate := range result.Candidates {
		names := []string{}
		for _, variable := range candidate.Variables {
			names = append(names, variable.Name)
		}
		switch candidate.Root {
		case "apps/web":
			if !reflect.DeepEqual(names, []string{"SHARED", "VITE_API_URL"}) || len(candidate.Databases) != 0 {
				t.Fatalf("web: %v %+v", names, candidate.Databases)
			}
		case "apps/api":
			if !reflect.DeepEqual(names, []string{"REDIS_URL", "SHARED"}) {
				t.Fatalf("api: %v", names)
			}
			if len(candidate.Databases) != 1 || candidate.Databases[0].Engine != "redis" || candidate.Databases[0].Variable != "REDIS_URL" {
				t.Fatalf("api databases: %+v", candidate.Databases)
			}
		default:
			t.Fatalf("unexpected root %q", candidate.Root)
		}
	}
}

func TestDatabaseSuggestionsFromDependenciesSchemasAndNames(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name  string
		files map[string]string
		want  []DetectedDatabase
	}{
		{name: "prisma provider and a redis queue", files: map[string]string{
			"package.json":         `{"scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"15","@prisma/client":"6","bullmq":"5"},"devDependencies":{"prisma":"6"}}`,
			"package-lock.json":    `{}`,
			"prisma/schema.prisma": "generator client {\n  provider = \"prisma-client-js\"\n}\n\ndatasource db {\n  provider = \"mysql\"\n  url      = env(\"DATABASE_URL\")\n}\n",
		}, want: []DetectedDatabase{
			{Engine: "redis", Variable: "REDIS_URL", Evidence: "bullmq in package.json"},
			{Engine: "mysql", Variable: "DATABASE_URL", Evidence: "Prisma datasource in prisma/schema.prisma"},
		}},
		{name: "python drivers with documented names", files: map[string]string{
			"requirements.txt": "fastapi\nasyncpg\nredis\n",
			"main.py":          "app = FastAPI()\n",
			".env.example":     "PG_DSN=postgresql://localhost/app\nREDIS_HOST=localhost\n",
		}, want: []DetectedDatabase{
			{Engine: "postgres", Variable: "PG_DSN", Evidence: "asyncpg in requirements.txt"},
			{Engine: "redis", Variable: "REDIS_URL", Evidence: "redis in requirements.txt"},
		}},
		{name: "go module", files: map[string]string{
			"go.mod":  "module example.test/api\n\ngo 1.26\n\nrequire (\n\tgithub.com/jackc/pgx/v5 v5.7.0\n\tgo.mongodb.org/mongo-driver v1.17.0\n)\n",
			"main.go": "package main\nfunc main() {}\n",
		}, want: []DetectedDatabase{
			{Engine: "postgres", Variable: "DATABASE_URL", Evidence: "github.com/jackc/pgx in go.mod"},
			{Engine: "mongodb", Variable: "MONGODB_URI", Evidence: "go.mongodb.org/mongo-driver in go.mod"},
		}},
		{name: "two relational drivers leave a generic url unbound", files: map[string]string{
			"package.json":      `{"scripts":{"start":"node index.js"},"dependencies":{"pg":"8","mysql2":"3"}}`,
			"package-lock.json": `{}`,
			".env.example":      "DATABASE_URL=\n",
		}, want: []DetectedDatabase{
			{Engine: "postgres", Variable: "DATABASE_URL", Evidence: "pg in package.json"},
			{Engine: "mysql", Variable: "DATABASE_URL", Evidence: "mysql2 in package.json"},
		}},
		{name: "sqlite names no engine", files: map[string]string{
			"package.json":      `{"scripts":{"start":"node index.js"},"dependencies":{"better-sqlite3":"11"}}`,
			"package-lock.json": `{}`,
		}, want: []DetectedDatabase{}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			for path, content := range fixture.files {
				writeBuildFixture(t, root, path, content)
			}
			result, err := (Detector{}).DetectPath(context.Background(), root, SourceIdentity{})
			if err != nil || len(result.Candidates) != 1 {
				t.Fatalf("detect: %+v, %v", result, err)
			}
			got := result.Candidates[0].Databases
			if len(got) == 0 && len(fixture.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, fixture.want) {
				t.Fatalf("databases:\n got %+v\nwant %+v", got, fixture.want)
			}
		})
	}
}

// The scanner's budget is its own: a repository with more source files than
// it reads still detects, is not reported truncated, and keeps the names it
// found before the budget ran out.
func TestEnvironmentDiscoveryStopsQuietlyAtItsBudget(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeBuildFixture(t, root, "package.json", `{"scripts":{"start":"node index.js"}}`)
	writeBuildFixture(t, root, "package-lock.json", `{}`)
	writeBuildFixture(t, root, "aaa/first.js", "process.env.FIRST_ONE")
	for index := 0; index < envScanMaxFiles+20; index++ {
		writeBuildFixture(t, root, fmt.Sprintf("src/module-%04d.js", index), "process.env.FILLER_NAME")
	}
	writeBuildFixture(t, root, "zzz/last.js", "process.env.LAST_ONE")
	result, err := (Detector{}).DetectPath(context.Background(), root, SourceIdentity{})
	if err != nil || len(result.Candidates) != 1 || result.Truncated {
		t.Fatalf("detect: %+v, %v", result, err)
	}
	names := map[string]bool{}
	for _, variable := range result.Candidates[0].Variables {
		names[variable.Name] = true
	}
	if !names["FIRST_ONE"] || !names["FILLER_NAME"] || names["LAST_ONE"] {
		t.Fatalf("names = %v", names)
	}
}

func TestEnvTemplateFileNamesAndExampleValues(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]bool{
		".env.example": true, ".env.sample": true, ".env.template": true, ".env.dist": true, ".env.local.example": true,
		"example.env": true, ".env": true, ".env.production": false, "env.d.ts": false, ".envrc": false, "environment.ts": false,
	} {
		if got := envTemplateFile(name); got != want {
			t.Fatalf("envTemplateFile(%q) = %v", name, got)
		}
	}
	for raw, want := range map[string]string{
		`"quoted value" # comment`: "quoted value", `plain # trailing`: "plain", `'single'`: "single",
		`postgres://user:secret@db/app`: "", `-----BEGIN PRIVATE KEY-----`: "", ``: "", `https://api.example.test`: "https://api.example.test",
	} {
		if got := envExampleValue(raw); got != want {
			t.Fatalf("envExampleValue(%q) = %q", raw, got)
		}
	}
}
