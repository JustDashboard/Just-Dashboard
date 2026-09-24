package deploy

import (
	"encoding/json"
	"path"
	"regexp"
	"strings"
)

// The Node catalogue in schema_tools.go is keyed on package.json. These are
// the migration tools of the other recipes, recognised from Python manifests
// and .NET project files, so a linked database gets its schema — or the
// missing step is named before deploy — whatever the application is written
// in. They share schemaToolByName, schemaStepConfigured and the preflight
// findings with the Node tools.
var runtimeSchemaTools = []schemaTool{
	{
		Name: "django", Label: "Django", Deploy: djangoMigrate, Push: djangoMigrate,
		applied: commandMentions("manage.py migrate"),
	},
	{
		Name: "flask-migrate", Label: "Flask-Migrate", Deploy: "flask db upgrade", Push: "flask db upgrade",
		applied: commandMentions("db upgrade"),
	},
	{
		Name: "alembic", Label: "Alembic", Deploy: "alembic upgrade head", Push: "alembic upgrade head",
		applied: func(command string) bool {
			return strings.Contains(command, "alembic") && strings.Contains(command, "upgrade")
		},
	},
	{
		Name: "aerich", Label: "Aerich", Deploy: "aerich upgrade", Push: "aerich upgrade",
		applied: commandMentions("aerich upgrade"),
	},
	{
		// EF Core's command-line migration needs the SDK and the design-time
		// project, neither of which is in the runtime image; an application
		// that migrates itself says so in its code.
		Name: "ef-core", Label: "EF Core",
		advice:  "Call Database.Migrate() (or MigrateAsync) on the DbContext at startup, so the application applies its committed migrations before it serves.",
		applied: commandMentions("ef database update", "efbundle"),
	},
}

const djangoMigrate = "python manage.py migrate --noinput"

// schemaGenerateCommand is how a tool that pushes the declared model commits
// migrations instead, for the push warning's action.
var schemaGenerateCommand = map[string]string{
	"prisma":  "prisma migrate dev",
	"drizzle": "drizzle-kit generate",
}

var (
	alembicScriptLocationRE = regexp.MustCompile(`(?m)^\s*script_location\s*=\s*(\S+)`)
	efCoreDesignRE          = regexp.MustCompile(`Include="Microsoft\.EntityFrameworkCore\.(?:Design|Tools)"`)
	efCoreMigratesRE        = regexp.MustCompile(`\.(?:Migrate|MigrateAsync|EnsureCreated|EnsureCreatedAsync)\(`)
)

// applySchemaDetection completes the schema step for the recipes whose tools
// schema_tools.go does not read, and records whether a Node schema step
// pushes the declared model rather than applying migrations.
func applySchemaDetection(candidate *DetectedCandidate, marker *detectedMarkers, view stateRoot) {
	switch {
	case candidate.Recipe == "python" && marker.hasPythonManifest():
		applyPythonSchemaTool(candidate, marker, view)
	case candidate.Recipe == "dotnet":
		applyEFCoreSchemaTool(candidate, marker, view)
	case candidate.Recipe == "node" && candidate.SchemaTool != "":
		tool := schemaToolByName(candidate.SchemaTool)
		if tool == nil || tool.Push == "" || tool.Push == tool.Deploy {
			return
		}
		var manifest nodeManifest
		script := ""
		if parseNodeManifest(marker.packageJSON, &manifest) {
			script = manifest.Scripts["start"]
		}
		candidate.SchemaPush = candidate.SchemaCommand == tool.Push ||
			(candidate.SchemaInStart && strings.Contains(script, tool.Push) && !strings.Contains(script, tool.Deploy))
	}
}

func applyPythonSchemaTool(candidate *DetectedCandidate, marker *detectedMarkers, view stateRoot) {
	deps := readPythonDependencies(marker.pythonFiles)
	name, command, evidence := "", "", DetectionEvidence{}
	switch {
	case candidate.Framework == "django":
		name, command = "django", djangoMigrate
		evidence = DetectionEvidence{Path: joinRoot(view.root, "manage.py"), Reason: "Django migrations"}
	case deps.has("flask-migrate") && view.contents["migrations/alembic.ini"] != nil:
		name, command = "flask-migrate", "flask db upgrade"
		if module := flaskAppModule(candidate.StartCommand); module != "" {
			command = "flask --app " + module + " db upgrade"
		}
		evidence = DetectionEvidence{Path: joinRoot(view.root, "migrations/alembic.ini"), Reason: "Flask-Migrate migrations"}
	case deps.has("alembic") || deps.has("flask-migrate"):
		ini := ""
		for _, candidateName := range append([]string{"alembic.ini"}, view.sorted(func(name string) bool {
			return path.Base(name) == "alembic.ini" && strings.Count(name, "/") == 1 && !strings.HasPrefix(name, "migrations/")
		})...) {
			if view.contents[candidateName] != nil {
				ini = candidateName
				break
			}
		}
		if ini == "" {
			return
		}
		name, command = "alembic", "alembic upgrade head"
		if ini != "alembic.ini" {
			command = "alembic -c " + ini + " upgrade head"
		}
		reason := "Alembic migrations"
		if alembicRevisions(view, ini) {
			reason += " with committed revisions"
		}
		evidence = DetectionEvidence{Path: joinRoot(view.root, ini), Reason: reason}
	case deps.has("aerich") && strings.Contains(string(marker.pythonFiles["pyproject.toml"]), "[tool.aerich]"):
		name, command = "aerich", "aerich upgrade"
		evidence = DetectionEvidence{Path: joinRoot(view.root, "pyproject.toml"), Reason: "Aerich migrations"}
	default:
		return
	}
	tool := schemaToolByName(name)
	candidate.SchemaTool, candidate.SchemaCommand = name, command
	start := candidate.StartCommand
	procfileWeb := procfileProcess(marker.procfile, "web")
	switch {
	case start == "":
	case tool.applied(start):
		evidence.Reason += "; the start command applies them before serving"
	case prestartRunsTool(start, view, tool):
		candidate.SchemaInStart = true
		evidence.Reason += "; the prestart script the start command runs applies them"
	case start == procfileWeb:
		// The repository declared its own process; a missing step is the
		// preflight warning's to name, not a command to rewrite.
		evidence.Reason += "; the Procfile's web process does not apply them"
	default:
		candidate.StartCommand = command + " && " + start
		evidence.Reason += "; the start command applies them with " + command + " before serving"
	}
	candidate.Evidence = append(candidate.Evidence, evidence)
}

// flaskAppModule is the module a gunicorn start command serves, which is what
// the flask command needs to find the application.
func flaskAppModule(start string) string {
	for _, field := range strings.Fields(start) {
		field = strings.Trim(field, `'"`)
		module, object, found := strings.Cut(field, ":")
		if !found || object == "" || strings.HasPrefix(field, "-") || strings.Contains(module, "/") || strings.Contains(module, "=") {
			continue
		}
		if pythonModuleRE.MatchString(module) {
			return module
		}
	}
	return ""
}

var pythonModuleRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)

// alembicRevisions reports whether the ini's script location holds committed
// revisions.
func alembicRevisions(view stateRoot, ini string) bool {
	location := "alembic"
	if match := alembicScriptLocationRE.FindSubmatch(view.contents[ini]); match != nil {
		location = strings.TrimPrefix(string(match[1]), "%(here)s/")
	}
	versions := path.Join(path.Dir(ini), location, "versions") + "/"
	for name := range view.present {
		if strings.HasPrefix(name, versions) {
			return true
		}
	}
	return false
}

// prestartRunsTool reports whether the start command runs a committed
// prestart script that applies the schema, the way full-stack-fastapi-template
// does.
func prestartRunsTool(start string, view stateRoot, tool *schemaTool) bool {
	if !strings.Contains(start, "prestart") {
		return false
	}
	for _, name := range []string{"prestart.sh", "scripts/prestart.sh"} {
		if content, ok := view.contents[name]; ok && strings.Contains(start, path.Base(name)) && tool.applied(string(content)) {
			return true
		}
	}
	return false
}

func applyEFCoreSchemaTool(candidate *DetectedCandidate, marker *detectedMarkers, view stateRoot) {
	projects := ""
	for _, content := range marker.csprojs {
		projects += string(content)
	}
	if !efCoreDesignRE.MatchString(projects) {
		return
	}
	snapshot := ""
	for name := range view.present {
		if strings.HasSuffix(strings.ToLower(name), "modelsnapshot.cs") && (snapshot == "" || name < snapshot) {
			snapshot = name
		}
	}
	if snapshot == "" {
		return
	}
	candidate.SchemaTool = "ef-core"
	evidence := DetectionEvidence{Path: joinRoot(view.root, snapshot), Reason: "EF Core migrations"}
	for _, name := range view.sorted(func(name string) bool { return strings.HasSuffix(name, ".cs") }) {
		if efCoreMigratesRE.Match(view.contents[name]) {
			candidate.SchemaInStart = true
			evidence.Reason += "; " + name + " applies them when the application starts"
			break
		}
	}
	candidate.Evidence = append(candidate.Evidence, evidence)
}

// Seeds -------------------------------------------------------------------

var (
	seedResetRE     = regexp.MustCompile(`(?i)deleteMany\(|\bTRUNCATE\b|[.>:]truncate\(|destroy_all|delete_all|DELETE FROM`)
	seedPathRE      = regexp.MustCompile(`([A-Za-z0-9_./-]+\.(?:ts|mts|cts|js|mjs|cjs))\b`)
	prismaConfigRE  = regexp.MustCompile(`\bseed\s*:\s*["'\x60]`)
	rubyCodeLineRE  = regexp.MustCompile(`(?m)^\s*[^#\s]`)
	laravelSeedCall = regexp.MustCompile(`->call\(|::create\(|->insert\(|::factory\(|->create\(|::firstOrCreate\(|::updateOrCreate\(`)
)

// applySeedDetection records the command that loads a project's seed data —
// the first administrator, the lookup rows its forms need — which nothing
// runs against a database created here.
func applySeedDetection(candidate *DetectedCandidate, marker *detectedMarkers, view stateRoot) {
	command, file := "", ""
	switch {
	case len(marker.packageJSON) > 0 && (candidate.Recipe == "node" || candidate.BuildMethod == BuildDockerfile) && candidate.OutputDirectory == "":
		command, file = nodeSeedCommand(candidate, marker, view)
	case len(marker.composerJSON) > 0 && (candidate.Recipe == "php" || candidate.BuildMethod == BuildDockerfile):
		if manifest, ok := parseComposerManifest(marker.composerJSON); ok && manifest.has("laravel/framework") {
			if content, ok := view.contents["database/seeders/DatabaseSeeder.php"]; ok && laravelSeederSeeds(string(content)) {
				command, file = "php artisan db:seed --force", "database/seeders/DatabaseSeeder.php"
			}
		}
	case candidate.BuildMethod == BuildDockerfile || candidate.Recipe == "ruby":
		if content, ok := view.contents["db/seeds.rb"]; ok && rubyCodeLineRE.Match(content) {
			command, file = "bin/rails db:seed", "db/seeds.rb"
		}
	}
	if command == "" || rejectPlanSecretLiteral("seed command", command) != nil {
		return
	}
	candidate.SeedCommand = command
	reason := "seed data is loaded with " + command
	if content, ok := view.contents[file]; ok && seedResetRE.Match(content) {
		candidate.SeedResets = true
		reason += "; the seed clears tables before inserting"
	}
	candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(view.root, firstNonEmpty(file, "package.json")), Reason: reason})
}

// nodeSeedCommand is Prisma's own seed step when one is configured, else a
// seed script, with the file it runs when that can be read from the command.
func nodeSeedCommand(candidate *DetectedCandidate, marker *detectedMarkers, view stateRoot) (string, string) {
	var manifest struct {
		Scripts map[string]string `json:"scripts"`
		Prisma  struct {
			Seed string `json:"seed"`
		} `json:"prisma"`
	}
	if json.Unmarshal(marker.packageJSON, &manifest) != nil {
		return "", ""
	}
	runner := candidate.PackageManager
	if runner == "" {
		runner = "npm"
	}
	seedFile := func(command string, defaults ...string) string {
		for _, match := range seedPathRE.FindAllStringSubmatch(command, -1) {
			if _, ok := view.contents[strings.TrimPrefix(match[1], "./")]; ok {
				return strings.TrimPrefix(match[1], "./")
			}
		}
		for _, name := range defaults {
			if _, ok := view.contents[name]; ok {
				return name
			}
		}
		return ""
	}
	prismaDefaults := []string{"prisma/seed.ts", "prisma/seed.js", "prisma/seed.mjs", "prisma/seed.mts", "prisma/seed.cjs"}
	if manifest.Prisma.Seed != "" {
		return nodeExecRunner(runner) + " prisma db seed", seedFile(manifest.Prisma.Seed, prismaDefaults...)
	}
	if _, config := view.file("prisma.config.ts", "prisma.config.mts", "prisma.config.js", "prisma.config.mjs"); config != nil && prismaConfigRE.Match(config) {
		return nodeExecRunner(runner) + " prisma db seed", seedFile(string(config), prismaDefaults...)
	}
	for _, script := range []string{"db:seed", "seed", "prisma:seed"} {
		if body := manifest.Scripts[script]; body != "" {
			return runner + " run " + script, seedFile(body)
		}
	}
	return "", ""
}

// laravelSeederSeeds reports whether DatabaseSeeder does more than the
// skeleton's test user, which needs Faker from require-dev and is no seed a
// production database wants.
func laravelSeederSeeds(content string) bool {
	var code []string
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "#") {
			continue
		}
		code = append(code, line)
	}
	body := strings.Join(code, "\n")
	if !laravelSeedCall.MatchString(body) {
		return false
	}
	skeleton := strings.Contains(body, "'Test User'") && strings.Contains(body, "test@example.com") && !strings.Contains(body, "->call(")
	return !skeleton
}
