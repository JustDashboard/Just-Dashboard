package deploy

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestNodeDetectionAppliesTheSchemaBeforeServing(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name, manifest, lockfile, tool, command, start string
		files                                          []string
		decision                                       string
	}{
		{
			name:     "prisma without migrations pushes",
			manifest: `{"scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"16","@prisma/client":"6"},"devDependencies":{"prisma":"6"}}`,
			lockfile: "bun.lock", files: []string{"prisma/schema.prisma"},
			tool: "prisma", command: "prisma db push", start: "bunx prisma db push && bun run start",
		},
		{
			name:     "prisma with migrations deploys",
			manifest: `{"scripts":{"start":"node server.js"},"dependencies":{"@prisma/client":"6"},"devDependencies":{"prisma":"6"}}`,
			lockfile: "package-lock.json", files: []string{"prisma/schema.prisma", "prisma/migrations/20240101000000_init/migration.sql"},
			tool: "prisma", command: "prisma migrate deploy", start: "npx prisma migrate deploy && npm run start",
		},
		{
			name:     "drizzle with a journal migrates",
			manifest: `{"scripts":{"start":"node dist/index.js"},"dependencies":{"drizzle-orm":"0.40"},"devDependencies":{"drizzle-kit":"0.30"}}`,
			lockfile: "pnpm-lock.yaml", files: []string{"drizzle.config.ts", "drizzle/meta/_journal.json"},
			tool: "drizzle", command: "drizzle-kit migrate", start: "pnpm exec drizzle-kit migrate && pnpm run start",
		},
		{
			name:     "knex with a knexfile",
			manifest: `{"scripts":{"start":"node index.js"},"dependencies":{"knex":"3","pg":"8"}}`,
			lockfile: "yarn.lock", files: []string{"knexfile.js"},
			tool: "knex", command: "knex migrate:latest", start: "yarn knex migrate:latest && yarn run start",
		},
		{
			name:     "typeorm needs a decision",
			manifest: `{"scripts":{"start":"node dist/main.js"},"dependencies":{"typeorm":"0.3"}}`,
			lockfile: "package-lock.json",
			tool:     "typeorm", start: "npm run start", decision: "choose how TypeORM migrations run",
		},
		{
			name:     "a start script that already migrates is left alone",
			manifest: `{"scripts":{"start":"prisma migrate deploy && next start"},"dependencies":{"next":"16","@prisma/client":"6","prisma":"6"}}`,
			lockfile: "bun.lock", files: []string{"prisma/schema.prisma"},
			tool: "prisma", start: "bun run start",
		},
		{
			name:     "a prisma dependency without a schema is not a schema tool",
			manifest: `{"scripts":{"start":"node index.js"},"devDependencies":{"prisma":"6"}}`,
			lockfile: "package-lock.json",
			start:    "npm run start",
		},
		{
			name:     "the client library alone never runs a binary that is not installed",
			manifest: `{"scripts":{"start":"node index.js"},"dependencies":{"@prisma/client":"6"}}`,
			lockfile: "package-lock.json", files: []string{"prisma/schema.prisma"},
			start: "npm run start",
		},
		{
			name:     "a schema outside the tool's own lookup does not count",
			manifest: `{"scripts":{"start":"node index.js"},"devDependencies":{"prisma":"6","drizzle-kit":"0.30"}}`,
			lockfile: "package-lock.json", files: []string{"packages/db/schema.prisma", "config/drizzle.config.ts"},
			start: "npm run start",
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeBuildFixture(t, root, "package.json", fixture.manifest)
			writeBuildFixture(t, root, fixture.lockfile, "")
			for _, file := range fixture.files {
				writeBuildFixture(t, root, file, "fixture")
			}
			result, err := (Detector{}).DetectPath(context.Background(), root, SourceIdentity{})
			if err != nil || len(result.Candidates) != 1 {
				t.Fatalf("detect: %+v, %v", result, err)
			}
			candidate := result.Candidates[0]
			if candidate.SchemaTool != fixture.tool || candidate.SchemaCommand != fixture.command || candidate.StartCommand != fixture.start {
				t.Fatalf("schema defaults: tool %q command %q start %q (%+v)", candidate.SchemaTool, candidate.SchemaCommand, candidate.StartCommand, candidate)
			}
			if fixture.decision != "" && !strings.Contains(strings.Join(candidate.NeedsDecision, "\n"), fixture.decision) {
				t.Fatalf("decisions = %v", candidate.NeedsDecision)
			}
			if fixture.tool != "" {
				found := false
				for _, evidence := range candidate.Evidence {
					found = found || strings.Contains(evidence.Reason, "schema") || strings.Contains(evidence.Reason, "migrations")
				}
				if !found {
					t.Fatalf("no schema evidence: %+v", candidate.Evidence)
				}
			}
		})
	}
}

func TestSchemaToolStaysOutOfStaticOutputAndOtherRoots(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeBuildFixture(t, root, "package.json", `{"scripts":{"build":"vite build","start":"vite preview"},"devDependencies":{"vite":"8","prisma":"6"}}`)
	writeBuildFixture(t, root, "package-lock.json", "")
	writeBuildFixture(t, root, "prisma/schema.prisma", "fixture")
	writeBuildFixture(t, root, "apps/api/package.json", `{"scripts":{"start":"node index.js"},"dependencies":{"knex":"3"}}`)
	writeBuildFixture(t, root, "apps/api/package-lock.json", "")
	writeBuildFixture(t, root, "apps/api/knexfile.ts", "fixture")
	result, err := (Detector{}).DetectPath(context.Background(), root, SourceIdentity{})
	if err != nil || len(result.Candidates) != 2 {
		t.Fatalf("detect: %+v, %v", result, err)
	}
	for _, candidate := range result.Candidates {
		switch candidate.Root {
		case "":
			if candidate.SchemaTool != "prisma" || candidate.StartCommand != "" || candidate.OutputDirectory != "dist" {
				t.Fatalf("static root gained a start command: %+v", candidate)
			}
		case "apps/api":
			if candidate.SchemaTool != "knex" || candidate.StartCommand != "npx knex migrate:latest && npm run start" {
				t.Fatalf("nested root: %+v", candidate)
			}
		default:
			t.Fatalf("unexpected root %q", candidate.Root)
		}
	}
}

func TestSchemaToolIgnoresNestedPackagesAndSurvivesManyMigrations(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeBuildFixture(t, root, "package.json", `{"scripts":{"start":"turbo run start"},"devDependencies":{"prisma":"6"}}`)
	writeBuildFixture(t, root, "package-lock.json", "")
	writeBuildFixture(t, root, "packages/db/package.json", `{"scripts":{"start":"node index.js"},"devDependencies":{"prisma":"6"}}`)
	writeBuildFixture(t, root, "packages/db/package-lock.json", "")
	writeBuildFixture(t, root, "packages/db/prisma/schema.prisma", "fixture")
	for index := 0; index < 100; index++ {
		writeBuildFixture(t, root, fmt.Sprintf("packages/db/prisma/migrations/%04d_step/migration.sql", index), "select 1")
	}
	result, err := (Detector{}).DetectPath(context.Background(), root, SourceIdentity{})
	if err != nil || len(result.Candidates) != 2 {
		t.Fatalf("detect: %+v, %v", result, err)
	}
	for _, candidate := range result.Candidates {
		switch candidate.Root {
		case "":
			if candidate.SchemaTool != "" || candidate.StartCommand != "npm run start" {
				t.Fatalf("monorepo root adopted a nested schema: %+v", candidate)
			}
		case "packages/db":
			if candidate.SchemaCommand != "prisma migrate deploy" || candidate.StartCommand != "npx prisma migrate deploy && npm run start" {
				t.Fatalf("nested package with many migrations: %+v", candidate)
			}
		default:
			t.Fatalf("unexpected root %q", candidate.Root)
		}
	}
}

func TestSchemaStepConfiguredReadsStartCommandAndReleaseTasks(t *testing.T) {
	t.Parallel()
	candidate := &DetectedCandidate{SchemaTool: "prisma", SchemaCommand: "prisma db push"}
	if schemaStepConfigured(candidate, BuildPlanConfig{StartCommand: "bun run start"}) {
		t.Fatal("bare start command counted as a schema step")
	}
	if !schemaStepConfigured(candidate, BuildPlanConfig{StartCommand: "bunx prisma migrate deploy && bun run start"}) {
		t.Fatal("start command schema step not recognised")
	}
	if !schemaStepConfigured(candidate, BuildPlanConfig{StartCommand: "bun run start", ReleaseTasks: []ReleaseTaskConfig{{Name: "migrate", Command: "bunx prisma db push", Runner: ReleaseTaskRunnerImage}}}) {
		t.Fatal("release task schema step not recognised")
	}
	// The host shell runs over the unbuilt checkout, where bunx has nothing to
	// run: that task fails, so it cannot be what applies the schema.
	if schemaStepConfigured(candidate, BuildPlanConfig{StartCommand: "bun run start", ReleaseTasks: []ReleaseTaskConfig{{Name: "migrate", Command: "bunx prisma db push"}}}) {
		t.Fatal("a host release task that needs the application's toolchain counted as the schema step")
	}
	if !schemaStepConfigured(&DetectedCandidate{SchemaTool: "typeorm", SchemaInStart: true}, BuildPlanConfig{StartCommand: "npm run start"}) {
		t.Fatal("package start script schema step not recognised")
	}
	if schemaStepConfigured(&DetectedCandidate{SchemaTool: "unknown"}, BuildPlanConfig{StartCommand: "prisma db push"}) {
		t.Fatal("unknown tool counted as configured")
	}
}

func TestPreflightWarnsWhenALinkedDatabaseGetsNoSchema(t *testing.T) {
	draft := completePlanningDraftModel()
	candidate := newDetectedCandidate("", BuildRecipe, DetectedCandidate{
		Name: "app", Profile: ProfileWeb, Confidence: ConfidenceHigh, Recipe: "node", Framework: "nextjs",
		StartCommand: "bunx prisma db push && bun run start", SchemaTool: "prisma", SchemaCommand: "prisma db push",
		Evidence: []DetectionEvidence{{Path: "prisma/schema.prisma", Reason: "Prisma schema"}}, NeedsDecision: []string{},
	})
	draft.Data.Detection.Candidates = []DetectedCandidate{candidate}
	draft.Data.Detection.SelectedID = candidate.ID
	draft.Data.Configuration.Build = BuildPlanConfig{Method: BuildRecipe, Recipe: "node", StartCommand: "bun run start"}
	draft.Data.Configuration.Dependencies = []PlannedDependency{{Kind: "database", Ownership: OwnershipLinked, ResourceKind: "database_connection", ResourceID: "3"}}
	observation := HostObservation{
		Facilities: map[string]FacilityObservation{"docker": {Available: true}, "buildx": {Available: true}},
		Paths:      []PathObservation{}, Ports: []PortObservation{}, Domains: []DomainObservation{},
		Dependencies: []DependencyObservation{{Kind: "database", ResourceKind: "database_connection", ResourceID: "3", Available: true}},
		OS:           "linux", Architecture: "amd64",
	}
	result, err := PreflightDraft(context.Background(), draft, &preflightObserverFake{observation: observation}, true)
	if err != nil {
		t.Fatal(err)
	}
	if findingSeverity(result.Findings, "schema_step_missing") != PreflightWarning {
		t.Fatalf("missing schema step findings = %#v", result.Findings)
	}
	for _, finding := range result.Findings {
		if finding.Code == "schema_step_missing" && (finding.FieldID != "build.startCommand" || !strings.Contains(finding.Action, "prisma db push")) {
			t.Fatalf("finding does not point at the start command: %+v", finding)
		}
	}

	draft.Data.Configuration.Build.StartCommand = candidate.StartCommand
	result, err = PreflightDraft(context.Background(), draft, &preflightObserverFake{observation: observation}, true)
	if err != nil {
		t.Fatal(err)
	}
	if findingSeverity(result.Findings, "schema_step") != PreflightPass || findingSeverity(result.Findings, "schema_step_missing") != "" {
		t.Fatalf("configured schema step findings = %#v", result.Findings)
	}

	draft.Data.Configuration.Build.StartCommand = "bun run start"
	draft.Data.Configuration.Dependencies = []PlannedDependency{}
	observation.Dependencies = []DependencyObservation{}
	result, err = PreflightDraft(context.Background(), draft, &preflightObserverFake{observation: observation}, true)
	if err != nil {
		t.Fatal(err)
	}
	if findingSeverity(result.Findings, "schema_step_missing") != "" || findingSeverity(result.Findings, "schema_step") != "" {
		t.Fatalf("no linked database still produced schema findings = %#v", result.Findings)
	}
}
