package deploy

import (
	"path"
	"strings"
)

// A schemaTool is a migration tool the Node recipe recognises: what names it
// in package.json, the files that prove it is configured, and the commands that
// apply its schema to a database without a terminal to answer. A database
// created in the dashboard is empty, and none of these tools create a table
// until told to, so a detected service applies its schema before it serves.
type schemaTool struct {
	Name         string
	Label        string
	Dependencies []string
	// configFile matches a path (relative to the package root) that proves the
	// tool is configured; nil means the dependency alone is proof.
	configFile func(string) bool
	// migrationFile matches a committed migration; when one exists the
	// deploy command runs, otherwise the push command creates the schema from
	// the declared model. An empty push means the tool has no non-interactive
	// way to do that and the operator has to decide.
	migrationFile func(string) bool
	Deploy        string
	Push          string
	// applied reports whether a command already runs this tool's schema step.
	applied func(string) bool
	// advice is the remedy when the tool has no command a start can run.
	advice string
}

// The dependency named for each tool is its command-line package, not its
// client library: npx and bunx fetch a binary they cannot find locally from
// the registry, and a schema step must never turn a container start into a
// download. The file rules follow each tool's own default lookup, so a config
// the command would not find on its own does not count.
var schemaTools = []schemaTool{
	{
		Name: "prisma", Label: "Prisma", Dependencies: []string{"prisma"},
		configFile: func(p string) bool {
			return strings.HasSuffix(p, ".prisma") && (!strings.Contains(p, "/") || strings.HasPrefix(p, "prisma/"))
		},
		migrationFile: func(p string) bool {
			return path.Base(p) == "migration.sql" && (strings.HasPrefix(p, "prisma/migrations/") || strings.HasPrefix(p, "migrations/"))
		},
		Deploy: "prisma migrate deploy", Push: "prisma db push",
		applied: commandMentions("prisma migrate", "prisma db push"),
	},
	{
		Name: "drizzle", Label: "Drizzle", Dependencies: []string{"drizzle-kit"},
		configFile:    func(p string) bool { return strings.HasPrefix(p, "drizzle.config.") },
		migrationFile: func(p string) bool { return path.Base(p) == "_journal.json" },
		Deploy:        "drizzle-kit migrate", Push: "drizzle-kit push",
		applied: commandMentions("drizzle-kit migrate", "drizzle-kit push"),
	},
	{
		Name: "mikro-orm", Label: "MikroORM", Dependencies: []string{"@mikro-orm/cli"},
		Deploy: "mikro-orm migration:up", Push: "mikro-orm migration:up",
		applied: commandMentions("migration:up", "migration:fresh", "schema:create", "schema:update"),
	},
	{
		Name: "typeorm", Label: "TypeORM", Dependencies: []string{"typeorm"},
		// migration:run needs the data source module and, for TypeScript, a
		// loader; neither can be guessed from the manifest.
		applied: commandMentions("migration:run", "schema:sync"),
	},
	{
		Name: "sequelize", Label: "Sequelize", Dependencies: []string{"sequelize-cli"},
		Deploy: "sequelize-cli db:migrate", Push: "sequelize-cli db:migrate",
		applied: commandMentions("db:migrate"),
	},
	{
		Name: "knex", Label: "Knex", Dependencies: []string{"knex"},
		configFile: func(p string) bool { return strings.HasPrefix(p, "knexfile.") },
		Deploy:     "knex migrate:latest", Push: "knex migrate:latest",
		applied: commandMentions("knex migrate"),
	},
}

func commandMentions(phrases ...string) func(string) bool {
	return func(command string) bool {
		for _, phrase := range phrases {
			if strings.Contains(command, phrase) {
				return true
			}
		}
		return false
	}
}

// schemaMarkerFile is the walk's filter: the few file names that prove a
// schema tool is configured or has committed migrations.
func schemaMarkerFile(name string) bool {
	return strings.HasSuffix(name, ".prisma") || name == "migration.sql" || name == "_journal.json" ||
		strings.HasPrefix(name, "drizzle.config.") || strings.HasPrefix(name, "knexfile.")
}

type detectedSchemaTool struct {
	Tool     schemaTool
	Command  string
	Evidence DetectionEvidence
}

// detectSchemaTool picks the first configured tool the manifest depends on.
// Paths are relative to the package root. The precedence matters only when a
// project carries two tools, where the earlier one is the one that owns
// migrations in practice. prisma is what Prisma's own configuration says
// about where its schema and migrations are, which moves them out of the
// default lookup.
func detectSchemaTool(dependencies map[string]string, paths []string, prisma nodePrismaFacts) *detectedSchemaTool {
	for _, tool := range schemaTools {
		if tool.Name == "prisma" && prisma.schema != "" {
			tool.configFile, tool.migrationFile = prismaDeclaredLocations(prisma, paths)
		}
		declared := ""
		for _, name := range tool.Dependencies {
			if dependencies[name] != "" {
				declared = name
				break
			}
		}
		if declared == "" {
			continue
		}
		config, migration := "", ""
		for _, candidate := range paths {
			if config == "" && tool.configFile != nil && tool.configFile(candidate) {
				config = candidate
			}
			if migration == "" && tool.migrationFile != nil && tool.migrationFile(candidate) {
				migration = candidate
			}
		}
		if tool.configFile != nil && config == "" {
			continue
		}
		result := &detectedSchemaTool{Tool: tool, Command: tool.Push}
		reason := tool.Label + " dependency " + declared
		if config != "" {
			reason = tool.Label + " schema"
		}
		if migration != "" {
			result.Command = tool.Deploy
			reason += " with committed migrations"
		}
		switch {
		case result.Command == "":
			reason += "; its migrations need a command of their own"
		default:
			reason += "; the start command applies it with " + result.Command + " before serving"
		}
		evidencePath := config
		if evidencePath == "" {
			evidencePath = "package.json"
		}
		result.Evidence = DetectionEvidence{Path: evidencePath, Reason: reason}
		return result
	}
	return nil
}

// prismaDeclaredLocations matches a schema at the path package.json or
// prisma.config declares — a file, or a directory of .prisma files — and
// the migrations Prisma keeps beside it or at the declared path.
func prismaDeclaredLocations(prisma nodePrismaFacts, paths []string) (func(string) bool, func(string) bool) {
	schema := prisma.schema
	directory := path.Dir(schema)
	for _, candidate := range paths {
		if strings.HasPrefix(candidate, schema+"/") {
			directory = schema
			break
		}
	}
	migrations := path.Join(directory, "migrations")
	if prisma.migrations != "" {
		migrations = prisma.migrations
	}
	config := func(p string) bool {
		return p == schema || (directory == schema && strings.HasPrefix(p, schema+"/") && strings.HasSuffix(p, ".prisma"))
	}
	migration := func(p string) bool {
		return path.Base(p) == "migration.sql" && strings.HasPrefix(p, migrations+"/")
	}
	return config, migration
}

func schemaToolByName(name string) *schemaTool {
	for _, tools := range [][]schemaTool{schemaTools, runtimeSchemaTools} {
		for index := range tools {
			if tools[index].Name == name {
				return &tools[index]
			}
		}
	}
	return nil
}

// schemaStepConfigured reports whether the plan already runs the named tool's
// schema step somewhere: the start command, a release task, or the package's
// own start script.
func schemaStepConfigured(candidate *DetectedCandidate, build BuildPlanConfig) bool {
	tool := schemaToolByName(candidate.SchemaTool)
	if tool == nil {
		return false
	}
	if candidate.SchemaInStart || tool.applied(build.StartCommand) {
		return true
	}
	for _, task := range build.ReleaseTasks {
		// A host task that needs the application's toolchain cannot apply
		// anything; counting it cleared the warning for a step that fails.
		if _, unbuilt := releaseTaskNeedsApplication(task.Command); unbuilt && task.Runner != ReleaseTaskRunnerImage {
			continue
		}
		if tool.applied(task.Command) || (candidate.SchemaInRelease && sameReleaseCommand(task.Command, candidate.ReleaseCommand)) {
			return true
		}
	}
	return false
}

// releaseAppliesSchema says whether the repository's declared release
// command runs the tool's schema step, itself or through the package
// scripts it runs. The release task runs it once before each release, so
// chained into the start command too it would run twice — the second time
// in every container start, racing the release it follows.
func releaseAppliesSchema(tool *schemaTool, release string, scripts map[string]string) bool {
	if tool == nil || strings.TrimSpace(release) == "" {
		return false
	}
	for _, segment := range nodeReachedSegments(scripts, []string{release}, false) {
		if tool.applied(segment) {
			return true
		}
	}
	return false
}

// nodeExecRunner is how each package manager runs a dependency's binary.
func nodeExecRunner(manager string) string {
	switch manager {
	case "bun":
		return "bunx"
	case "pnpm":
		return "pnpm exec"
	case "yarn":
		return "yarn"
	default:
		return "npx"
	}
}

// pathsUnderRoot narrows slash-separated repository paths to one package root
// and re-expresses them relative to it. A file inside a nested package belongs
// to that package: a monorepo root that depends on prisma must not adopt the
// schema of packages/db and run a command that cannot find it.
func pathsUnderRoot(paths []string, root string, packageRoots []string) []string {
	prefix := rootPrefix(root)
	result := make([]string, 0, len(paths))
	for _, candidate := range paths {
		if !strings.HasPrefix(candidate, prefix) {
			continue
		}
		nested := false
		for _, other := range packageRoots {
			if other != root && strings.HasPrefix(other, prefix) && strings.HasPrefix(candidate, rootPrefix(other)) {
				nested = true
				break
			}
		}
		if !nested {
			result = append(result, strings.TrimPrefix(candidate, prefix))
		}
	}
	return result
}

func rootPrefix(root string) string {
	if root == "" {
		return ""
	}
	return strings.TrimSuffix(root, "/") + "/"
}
