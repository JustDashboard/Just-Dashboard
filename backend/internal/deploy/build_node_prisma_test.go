package deploy

import (
	"context"
	"strings"
	"testing"
)

const (
	prisma7Manifest = `{"name":"shop","scripts":{"postinstall":"prisma generate","build":"prisma generate && next build","start":"next start"},` +
		`"dependencies":{"next":"16.0.0","@prisma/client":"^7.0.0"},"devDependencies":{"prisma":"^7.0.0"}}`
	prisma7Config = "import 'dotenv/config'\nimport { defineConfig, env } from 'prisma/config'\n\nexport default defineConfig({\n  schema: 'prisma/schema.prisma',\n  migrations: { path: 'prisma/migrations' },\n  datasource: { url: env('DATABASE_URL') },\n})\n"
	prismaSchema  = "generator client {\n  provider = \"prisma-client\"\n  output   = \"../src/generated/prisma\"\n}\n\ndatasource db {\n  provider = \"%s\"\n}\n"
)

func prismaSchemaFor(provider string) string { return strings.Replace(prismaSchema, "%s", provider, 1) }

// The incident's stack after its lockfile is fixed: Prisma 7 generates in
// postinstall and in the build, and prisma.config.ts reads DATABASE_URL
// through env(), which throws when it is unset. Every step that may run
// generate gets a provider-shaped placeholder a real value overrides; the
// real value never reaches the install unless it is mapped there; generate
// runs on its own after the install whatever the install policy ran.
func TestNodeRecipeGeneratesPrismaWithoutADatabaseValue(t *testing.T) {
	t.Parallel()
	placeholder := `export DATABASE_URL="${DATABASE_URL:-postgresql://127.0.0.1:5432/prisma-generate}" && `
	for _, test := range []struct {
		name     string
		files    map[string]string
		config   BuildPlanConfig
		secrets  []string
		want     []string
		absent   []string
		findings []string
	}{
		{
			name: "Prisma 7 with nothing bound",
			files: map[string]string{"package.json": prisma7Manifest, "prisma.config.ts": prisma7Config,
				"prisma/schema.prisma": prismaSchemaFor("postgresql")},
			config: BuildPlanConfig{BuildCommand: "npm run build", StartCommand: "npm run start"},
			want: []string{
				" AS build\nRUN apk add --no-cache openssl\n",
				"RUN " + placeholder + "npm install --no-audit --no-fund\n",
				"ENV PATH=/app/node_modules/.bin:$PATH\nRUN " + placeholder + "npx prisma generate\nRUN " + placeholder + "npm run build\n",
			},
			findings: []string{"prisma_generate_added", "prisma_config_env"},
		},
		{
			name: "a build value wins and still stays out of the install",
			files: map[string]string{"package.json": prisma7Manifest, "prisma.config.ts": prisma7Config,
				"prisma/schema.prisma": prismaSchemaFor("postgresql")},
			config:  BuildPlanConfig{BuildCommand: "npm run build", StartCommand: "npm run start"},
			secrets: []string{"DATABASE_URL"},
			want: []string{
				"RUN " + placeholder + "npm install --no-audit --no-fund\n",
				"RUN --mount=type=secret,id=DATABASE_URL,env=DATABASE_URL,required=true " + placeholder + "npx prisma generate\n",
				"RUN --mount=type=secret,id=DATABASE_URL,env=DATABASE_URL,required=true " + placeholder + "npm run build\n",
			},
			absent: []string{"required=true " + placeholder + "npm install"},
		},
		{
			name: "mapped to install and build, the install gets the real value too",
			files: map[string]string{"package.json": prisma7Manifest, "prisma.config.ts": prisma7Config,
				"prisma/schema.prisma": prismaSchemaFor("postgresql")},
			config: BuildPlanConfig{BuildCommand: "npm run build", StartCommand: "npm run start",
				Secrets: []BuildSecretConfig{{Variable: "DATABASE_URL", Step: "install_and_build"}}},
			secrets: []string{"DATABASE_URL"},
			want:    []string{"RUN --mount=type=secret,id=DATABASE_URL,env=DATABASE_URL,required=true " + placeholder + "npm install --no-audit --no-fund\n"},
		},
		{
			name: "the placeholder follows the provider",
			files: map[string]string{"package.json": prisma7Manifest, "prisma/schema.prisma": prismaSchemaFor("mysql"),
				"prisma.config.ts": "export default defineConfig({ datasource: { url: env<{ DATABASE_URL: string }>(\"DATABASE_URL\"), shadowDatabaseUrl: env('SHADOW_DATABASE_URL') } })"},
			config: BuildPlanConfig{BuildCommand: "npm run build", StartCommand: "npm run start"},
			want:   []string{`export DATABASE_URL="${DATABASE_URL:-mysql://127.0.0.1:3306/prisma-generate}" SHADOW_DATABASE_URL="${SHADOW_DATABASE_URL:-mysql://127.0.0.1:3306/prisma-generate}" && npx prisma generate`},
		},
		{
			name: "SQLite and a name that is not a URL",
			files: map[string]string{"package.json": prisma7Manifest, "prisma/schema.prisma": prismaSchemaFor("sqlite"),
				"prisma.config.ts": "export default defineConfig({ datasource: { url: env('TURSO_DATABASE_URL') }, adapter: token(env('TURSO_AUTH_KEY')) })"},
			config: BuildPlanConfig{BuildCommand: "npm run build", StartCommand: "npm run start"},
			want:   []string{`TURSO_DATABASE_URL="${TURSO_DATABASE_URL:-file:./prisma-generate.db}" TURSO_AUTH_KEY="${TURSO_AUTH_KEY:-prisma-generate}"`},
		},
		{
			name: "a build that migrates gets no placeholder",
			files: map[string]string{"package.json": strings.Replace(prisma7Manifest, "prisma generate && next build", "prisma migrate deploy && next build", 1),
				"prisma.config.ts": prisma7Config, "prisma/schema.prisma": prismaSchemaFor("postgresql")},
			config: BuildPlanConfig{BuildCommand: "npm run build", StartCommand: "npm run start"},
			want:   []string{"RUN " + placeholder + "npx prisma generate\nRUN npm run build\n"},
		},
		{
			name:   "Prisma 6 generates without placeholders",
			files:  map[string]string{"package.json": `{"name":"api","scripts":{"start":"node index.js"},"dependencies":{"@prisma/client":"^6.2.0"},"devDependencies":{"prisma":"^6.2.0"}}`, "prisma/schema.prisma": prismaSchemaFor("postgresql")},
			config: BuildPlanConfig{StartCommand: "npm run start"},
			want:   []string{"RUN npm install --no-audit --no-fund\nENV PATH=/app/node_modules/.bin:$PATH\nRUN npx prisma generate\n"},
			absent: []string{"export"}, findings: []string{"prisma_generate_added"},
		},
		{
			name:   "the client library alone never runs a CLI that is not installed",
			files:  map[string]string{"package.json": `{"name":"api","scripts":{"start":"node index.js"},"dependencies":{"@prisma/client":"^6.2.0"}}`, "prisma/schema.prisma": prismaSchemaFor("postgresql")},
			config: BuildPlanConfig{StartCommand: "npm run start"},
			want:   []string{"apk add --no-cache openssl"}, absent: []string{"prisma generate"},
		},
		{
			name: "a schema at a declared path, and a pnpm runner",
			files: map[string]string{"package.json": `{"name":"api","scripts":{"start":"node index.js"},"prisma":{"schema":"src/db/schema.prisma"},"dependencies":{"@prisma/client":"^6.2.0"},"devDependencies":{"prisma":"^6.2.0"}}`,
				"pnpm-lock.yaml": "lockfileVersion: '9.0'\n", "src/db/schema.prisma": prismaSchemaFor("postgresql")},
			config: BuildPlanConfig{StartCommand: "pnpm run start"},
			want:   []string{"RUN pnpm exec prisma generate\n"},
		},
		{
			name: "a multi-file schema directory",
			files: map[string]string{"package.json": `{"name":"api","scripts":{"start":"node index.js"},"dependencies":{"@prisma/client":"^6.7.0"},"devDependencies":{"prisma":"^6.7.0"}}`,
				"prisma/schema/user.prisma": "model User { id Int @id }\n", "prisma/schema/schema.prisma": prismaSchemaFor("postgresql")},
			config: BuildPlanConfig{StartCommand: "npm run start"},
			want:   []string{"RUN npx prisma generate\n"},
		},
		{
			name:   "no schema, no generate",
			files:  map[string]string{"package.json": `{"name":"api","scripts":{"start":"node index.js"},"devDependencies":{"prisma":"^6.2.0"}}`},
			config: BuildPlanConfig{StartCommand: "npm run start"},
			absent: []string{"prisma generate"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config := test.config
			config.Method, config.Recipe = BuildRecipe, "node"
			prepared, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), writeNodeTree(t, test.files), config, false, "t:1", test.secrets...)
			if err != nil {
				t.Fatal(err)
			}
			assertDockerfile(t, prepared.DockerfilePreview, test.want, test.absent)
			if strings.Contains(prepared.DockerfilePreview, "hunter2") {
				t.Fatal("a value entered the Dockerfile")
			}
			plan := planNodeInstall(readNodeTree(t, test.files, "").facts, nodeInstallChoice{build: test.config.BuildCommand, start: test.config.StartCommand})
			for _, code := range test.findings {
				if findingByCode(plan.findings, code) == nil {
					t.Fatalf("findings %+v lack %s", plan.findings, code)
				}
			}
		})
	}
}

func TestPrismaConfigurationIsReadAsText(t *testing.T) {
	t.Parallel()
	source := readNodeTree(t, map[string]string{
		"package.json": prisma7Manifest,
		".config/prisma.ts": "export default defineConfig({\n  schema: \"./db/schema.prisma\",\n  migrations: { seed: 'tsx seed.ts', path: 'db/migrations' },\n" +
			"  datasource: { url: env('DATABASE_URL'), directUrl: env(\"DIRECT_URL\"), other: process.env.NOT_ENV, lower: env('lowercase') },\n})\n",
		"db/schema.prisma": prismaSchemaFor("cockroachdb"),
	}, "")
	prisma := source.facts.prisma
	if prisma.config != ".config/prisma.ts" || strings.Join(prisma.env, ",") != "DATABASE_URL,DIRECT_URL" || prisma.schema != "db/schema.prisma" ||
		prisma.migrations != "db/migrations" || !prisma.found || prisma.provider != "cockroachdb" {
		t.Fatalf("prisma facts = %+v", prisma)
	}
	if got := prismaPlaceholder("cockroachdb", "DATABASE_URL"); got != "postgresql://127.0.0.1:5432/prisma-generate" {
		t.Fatalf("placeholder = %q", got)
	}
	if escaped := readNodeTree(t, map[string]string{"package.json": `{"prisma":{"schema":"../outside.prisma"}}`}, "").facts.prisma; escaped.schema != "" || escaped.found {
		t.Fatalf("a schema path outside the package was accepted: %+v", escaped)
	}
}

// A schema at the path Prisma's own configuration declares still gets its
// migrations applied before the server starts.
func TestSchemaToolFollowsPrismasDeclaredSchemaPath(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, manifest, config string
		files                  []string
		command                string
	}{
		{name: "package.json prisma.schema with migrations beside it",
			manifest: `{"scripts":{"start":"node index.js"},"prisma":{"schema":"src/db/schema.prisma"},"dependencies":{"@prisma/client":"6"},"devDependencies":{"prisma":"6"}}`,
			files:    []string{"src/db/schema.prisma", "src/db/migrations/20240101000000_init/migration.sql"}, command: "prisma migrate deploy"},
		{name: "prisma.config schema without migrations pushes",
			manifest: `{"scripts":{"start":"node index.js"},"dependencies":{"@prisma/client":"7"},"devDependencies":{"prisma":"7"}}`,
			config:   "export default defineConfig({ schema: 'database/schema.prisma' })", files: []string{"database/schema.prisma"}, command: "prisma db push"},
		{name: "prisma.config migrations path",
			manifest: `{"scripts":{"start":"node index.js"},"dependencies":{"@prisma/client":"7"},"devDependencies":{"prisma":"7"}}`,
			config:   "export default defineConfig({ schema: 'database/schema', migrations: { path: 'database/history' } })",
			files:    []string{"database/schema/models.prisma", "database/history/20240101000000_init/migration.sql"}, command: "prisma migrate deploy"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeBuildFixture(t, root, "package.json", test.manifest)
			writeBuildFixture(t, root, "package-lock.json", "")
			if test.config != "" {
				writeBuildFixture(t, root, "prisma.config.ts", test.config)
			}
			for _, file := range test.files {
				writeBuildFixture(t, root, file, "fixture")
			}
			result, err := (Detector{}).DetectPath(context.Background(), root, SourceIdentity{})
			if err != nil || len(result.Candidates) != 1 {
				t.Fatalf("detect: %+v, %v", result, err)
			}
			if candidate := result.Candidates[0]; candidate.SchemaTool != "prisma" || candidate.SchemaCommand != test.command ||
				candidate.StartCommand != "npx "+test.command+" && npm run start" {
				t.Fatalf("schema = %q %q start %q", candidate.SchemaTool, candidate.SchemaCommand, candidate.StartCommand)
			}
		})
	}
}
