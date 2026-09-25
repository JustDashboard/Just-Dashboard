package deploy

import (
	"strings"
	"testing"
)

// A framework that owns its migrations (AdonisJS's Lucid, Medusa, Keystone,
// Redwood's Prisma) records them as its schema step, like a package's own
// Prisma or Drizzle: preflight passes the step while the start command runs
// it, names it when an edited start command drops it, and a start command
// another platform's file declares gets it chained in front.
func TestFrameworkMigrationsAreSchemaSteps(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name          string
		files         map[string]string
		tool, command string
		start         string
		// edited is a start command without the step; chained is what a
		// declared start command becomes with it.
		edited, chained string
	}{
		{name: "adonisjs lucid", files: map[string]string{
			"package.json":                   `{"type":"module","scripts":{"start":"node ace migration:run --force && node bin/server.js","build":"node ace build"},"dependencies":{"@adonisjs/core":"^6.17.0","@adonisjs/lucid":"^21.6.0"}}`,
			"database/migrations/1_users.ts": "export default class extends BaseSchema {}",
		}, tool: "lucid", command: "node build/ace.js migration:run --force",
			start:  "node build/ace.js migration:run --force && node build/bin/server.js",
			edited: "node build/bin/server.js", chained: "node build/ace.js migration:run --force && node build/bin/server.js"},
		{name: "adonisjs 5 lucid", files: map[string]string{
			"package.json":                   `{"scripts":{"start":"node server.js","build":"node ace build --production"},"dependencies":{"@adonisjs/core":"^5.9.0","@adonisjs/lucid":"^18.4.0"}}`,
			"database/migrations/1_users.ts": "export default class extends BaseSchema {}",
		}, tool: "lucid", command: "node build/ace migration:run --force",
			start:  "node build/ace migration:run --force && node build/server.js",
			edited: "node build/server.js", chained: "node build/ace migration:run --force && node build/server.js"},
		{name: "medusa", files: map[string]string{
			"package.json": `{"scripts":{"build":"medusa build","start":"medusa start"},"dependencies":{"@medusajs/medusa":"2.8.4","@medusajs/framework":"2.8.4"}}`,
		}, tool: "medusa", command: "(cd .medusa/server && medusa db:migrate)",
			start:  "cd .medusa/server && medusa db:migrate && medusa start",
			edited: "cd .medusa/server && medusa start", chained: "(cd .medusa/server && medusa db:migrate) && cd .medusa/server && medusa start"},
		{name: "keystone", files: map[string]string{
			"package.json": `{"scripts":{"build":"keystone build","start":"keystone start"},"dependencies":{"@keystone-6/core":"^6.3.0"}}`,
		}, tool: "keystone", command: "keystone prisma migrate deploy",
			start:  "npx keystone start --with-migrations",
			edited: "npx keystone start", chained: "npx keystone prisma migrate deploy && npx keystone start"},
		{name: "redwood", files: map[string]string{
			"package.json":                           `{"workspaces":["api","web"],"devDependencies":{"@redwoodjs/core":"8.4.0"}}`,
			"redwood.toml":                           "[web]\n  port = 8910\n",
			"api/db/migrations/1_init/migration.sql": "CREATE TABLE a (id int);",
		}, tool: "redwood-prisma", command: "rw prisma migrate deploy",
			start:  "npx rw prisma migrate deploy && npx rw serve",
			edited: "npx rw serve", chained: "npx rw prisma migrate deploy && npx rw serve"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			candidate := fixtureCandidate(t, detectFixture(t, withLockfile(fixture.files)), BuildRecipe)
			if candidate.SchemaTool != fixture.tool || candidate.SchemaCommand != fixture.command || candidate.SchemaInStart ||
				candidate.StartCommand != fixture.start {
				t.Fatalf("schema = %q %q in start %v, start %q", candidate.SchemaTool, candidate.SchemaCommand, candidate.SchemaInStart, candidate.StartCommand)
			}
			build := BuildPlanConfig{Method: BuildRecipe, Recipe: "node", StartCommand: candidate.StartCommand}
			if item := schemaStepFinding(&candidate, build); item.Code != "schema_step" {
				t.Fatalf("detected start = %+v", item)
			}
			build.StartCommand = fixture.edited
			if item := schemaStepFinding(&candidate, build); item.Code != "schema_step_missing" || !strings.Contains(item.Action, fixture.command) {
				t.Fatalf("edited start = %+v", item)
			}
			if chained := withSchemaStep(&candidate, fixture.edited); chained != fixture.chained {
				t.Fatalf("declared start = %q, want %q", chained, fixture.chained)
			}
		})
	}
}
