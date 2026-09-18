package deploy

import (
	"path"
	"regexp"
	"sort"
	"strings"
)

// Environment discovery reads the variables a source expects — from the
// example env file a repository ships and from the code's own reads of its
// environment — so the new-project form opens with the names already listed
// instead of a blank first row. Names are the whole of the evidence: an
// example file's values are placeholders and are kept only as hints, and a
// committed real .env contributes names alone.

// envTemplateFile recognises the files a repository documents its variables
// in: .env.example, .env.sample, .env.template, .env.dist, example.env and
// the like. `.env` itself is read for names only.
func envTemplateFile(name string) bool {
	if name == ".env" {
		return true
	}
	base := strings.TrimPrefix(name, ".")
	if !strings.HasPrefix(base, "env") && !strings.HasSuffix(base, ".env") {
		return false
	}
	for _, suffix := range []string{".example", ".sample", ".template", ".dist", ".defaults", ".default"} {
		if strings.HasSuffix(base, suffix) || strings.Contains(base, suffix+".") {
			return true
		}
	}
	for _, prefix := range []string{"example.env", "sample.env", "template.env"} {
		if base == prefix {
			return true
		}
	}
	return false
}

var (
	envNameRE       = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,127}$`)
	envTemplateLine = regexp.MustCompile(`^(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*[=:]\s*(.*)$`)
	// envReferenceREs are how each language reads its environment. Every
	// expression captures the variable name in group 1.
	envReferenceREs = []*regexp.Regexp{
		regexp.MustCompile(`process\.env\.([A-Z][A-Z0-9_]+)`),
		regexp.MustCompile(`process\.env\[['"]([A-Z][A-Z0-9_]+)['"]\]`),
		regexp.MustCompile(`import\.meta\.env\.([A-Z][A-Z0-9_]+)`),
		regexp.MustCompile(`Bun\.env\.([A-Z][A-Z0-9_]+)`),
		regexp.MustCompile(`Deno\.env\.get\(['"]([A-Z][A-Z0-9_]+)['"]\)`),
		regexp.MustCompile(`os\.environ\[['"]([A-Z][A-Z0-9_]+)['"]\]`),
		regexp.MustCompile(`os\.environ\.get\(\s*['"]([A-Z][A-Z0-9_]+)['"]`),
		regexp.MustCompile(`os\.getenv\(\s*['"]([A-Z][A-Z0-9_]+)['"]`),
		regexp.MustCompile(`\bconfig\(\s*['"]([A-Z][A-Z0-9_]+)['"]`),
		regexp.MustCompile(`\benv(?:\.[a-z_]+)?\(\s*['"]([A-Z][A-Z0-9_]+)['"]`),
		regexp.MustCompile(`os\.(?:Getenv|LookupEnv)\("([A-Z][A-Z0-9_]+)"\)`),
		regexp.MustCompile(`\bgetenv\(\s*['"]([A-Z][A-Z0-9_]+)['"]`),
		regexp.MustCompile(`\$_ENV\[['"]([A-Z][A-Z0-9_]+)['"]\]`),
		regexp.MustCompile(`\bENV(?:\.fetch\(|\[)\s*['"]([A-Z][A-Z0-9_]+)['"]`),
	}
	// envProvidedNames are set by the platform, the runtime or the shell;
	// listing them would ask the operator for values the deployment supplies.
	envProvidedNames = map[string]bool{
		"PORT": true, "HOST": true, "HOSTNAME": true, "NODE_ENV": true, "PATH": true, "HOME": true, "PWD": true,
		"USER": true, "SHELL": true, "LANG": true, "LC_ALL": true, "TERM": true, "TZ": true, "CI": true,
		"NEXT_RUNTIME": true, "NEXT_PHASE": true, "PYTHONUNBUFFERED": true, "PYTHONPATH": true, "VIRTUAL_ENV": true,
		"NODE_OPTIONS": true, "DEBUG": true, "npm_package_version": true,
	}
	envSourceExtensions = map[string]bool{
		".js": true, ".mjs": true, ".cjs": true, ".ts": true, ".mts": true, ".tsx": true, ".jsx": true,
		".py": true, ".go": true, ".rb": true, ".php": true, ".vue": true, ".svelte": true, ".astro": true,
	}
	envSkippedDirs = map[string]bool{
		"test": true, "tests": true, "__tests__": true, "spec": true, "e2e": true, "fixtures": true, "mocks": true,
		"__mocks__": true, "docs": true, "examples": true, "example": true, "migrations": true, "public": true, "static": true,
	}
)

// envScanner accumulates references across the walk under its own budget,
// so a large repository stops contributing names quietly instead of pushing
// detection itself over its limits.
type envScanner struct {
	files, bytes int64
	found        map[string]*DetectedVariable
	order        []string
	// positions records where each name was first seen in each source, in
	// walk order, so a template's names can be listed in the file's order.
	positions map[string]map[string]int
	counter   int
	exhausted bool
}

const (
	envScanMaxFiles = 400
	envScanMaxBytes = 3 << 20
	envScanMaxFile  = 256 << 10
)

func newEnvScanner() *envScanner {
	return &envScanner{found: map[string]*DetectedVariable{}, positions: map[string]map[string]int{}}
}

// scannable reports whether a source file is worth reading for references:
// application code, not tests, fixtures, documentation or generated files.
func (s *envScanner) scannable(rel, name string) bool {
	if s.exhausted || !envSourceExtensions[path.Ext(name)] {
		return false
	}
	if strings.HasSuffix(name, ".d.ts") || strings.HasSuffix(name, ".min.js") || strings.HasSuffix(name, "_test.go") ||
		strings.HasPrefix(name, "test_") || name == "conftest.py" ||
		strings.Contains(name, ".test.") || strings.Contains(name, ".spec.") || strings.Contains(name, ".stories.") {
		return false
	}
	for _, segment := range strings.Split(path.Dir(rel), "/") {
		if envSkippedDirs[segment] {
			return false
		}
	}
	return true
}

// budget reserves a file of the given size; false means the scan is over.
func (s *envScanner) budget(size int64) bool {
	if s.exhausted || size > envScanMaxFile {
		return false
	}
	if s.files+1 > envScanMaxFiles || s.bytes+size > envScanMaxBytes {
		s.exhausted = true
		return false
	}
	s.files++
	s.bytes += size
	return true
}

func (s *envScanner) record(name, source, example string) {
	if !envNameRE.MatchString(name) || envProvidedNames[name] {
		return
	}
	variable := s.found[name]
	if variable == nil {
		variable = &DetectedVariable{Name: name, Sources: []string{}}
		s.found[name] = variable
		s.order = append(s.order, name)
	}
	if example != "" && variable.Example == "" {
		variable.Example = example
	}
	if s.positions[name] == nil {
		s.positions[name] = map[string]int{}
	}
	if _, seen := s.positions[name][source]; !seen {
		s.positions[name][source] = s.counter
		s.counter++
	}
	if len(variable.Sources) < 4 {
		for _, existing := range variable.Sources {
			if existing == source {
				return
			}
		}
		variable.Sources = append(variable.Sources, source)
	}
}

// scanTemplate reads a dotenv-shaped file. Values are kept as examples only
// from a documented template and only when they carry no credential
// material; a real .env's values never leave the repository.
func (s *envScanner) scanTemplate(rel string, content []byte, real bool) {
	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		match := envTemplateLine.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		example := ""
		if !real {
			example = envExampleValue(match[2])
		}
		s.record(match[1], rel, example)
	}
}

// envExampleValue strips quotes and trailing comments from a template value
// and drops anything credential-shaped or too long to be a hint.
func envExampleValue(raw string) string {
	value := strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(value, `"`) && strings.Count(value, `"`) >= 2:
		value = value[1 : 1+strings.Index(value[1:], `"`)]
	case strings.HasPrefix(value, "'") && strings.Count(value, "'") >= 2:
		value = value[1 : 1+strings.Index(value[1:], "'")]
	default:
		if comment := strings.Index(value, " #"); comment >= 0 {
			value = strings.TrimSpace(value[:comment])
		}
	}
	if value == "" || len(value) > 256 || strings.ContainsAny(value, "\x00\r\n") ||
		rejectPlanSecretLiteral("example", value) != nil {
		return ""
	}
	return value
}

func (s *envScanner) scanSource(rel string, content []byte) {
	for _, expression := range envReferenceREs {
		for _, match := range expression.FindAllSubmatch(content, -1) {
			s.record(string(match[1]), rel, "")
		}
	}
}

// variables returns the discovered variables that belong to a root: those
// found under it (and not under a nested root), plus every variable found
// outside all roots — a repository-level .env.example documents the
// application in apps/web even though nothing else there is the root's own.
// The root's own documented template comes first in file order, then a
// committed .env's names, then what the repository documents above the
// root, and finally the names only the code reads, by name — so the form
// reads the way the repository documents itself.
func (s *envScanner) variables(root string, roots []string) []DetectedVariable {
	prefix := rootPrefix(root)
	claimed := func(source string) bool {
		if !strings.HasPrefix(source, prefix) {
			return false
		}
		for _, other := range roots {
			if other != root && strings.HasPrefix(other, prefix) && strings.HasPrefix(source, rootPrefix(other)) {
				return false
			}
		}
		return true
	}
	orphan := func(source string) bool {
		for _, other := range roots {
			if strings.HasPrefix(source, rootPrefix(other)) {
				return false
			}
		}
		return true
	}
	rank := func(source string) int {
		owned := claimed(source)
		if !owned && !orphan(source) {
			return -1
		}
		name := path.Base(source)
		switch {
		case !envTemplateFile(name):
			return 4
		case owned && name != ".env":
			return 0
		case owned:
			return 1
		case name != ".env":
			return 2
		}
		return 3
	}
	type ranked struct {
		variable DetectedVariable
		rank     int
		seen     int
	}
	var items []ranked
	for _, name := range s.order {
		variable := *s.found[name]
		best, bestRank := -1, 99
		for index, source := range variable.Sources {
			if r := rank(source); r >= 0 && r < bestRank {
				best, bestRank = index, r
			}
		}
		if best < 0 {
			continue
		}
		sources := []string{variable.Sources[best]}
		for index, source := range variable.Sources {
			if index != best {
				sources = append(sources, source)
			}
		}
		variable.Sources = sources
		items = append(items, ranked{variable: variable, rank: bestRank, seen: s.positions[name][sources[0]]})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].rank != items[j].rank {
			return items[i].rank < items[j].rank
		}
		if items[i].rank == 4 {
			return items[i].variable.Name < items[j].variable.Name
		}
		return items[i].seen < items[j].seen
	})
	result := make([]DetectedVariable, 0, len(items))
	for _, item := range items {
		result = append(result, item.variable)
	}
	if len(result) > 64 {
		result = result[:64]
	}
	return result
}

// Database suggestions: the engine a source connects to is written in its
// dependencies and in the variables it documents, which is enough to offer
// the right quick-setup engine wired to the right variable.

var (
	nodeDatabaseDrivers = []struct{ dependency, engine string }{
		{"pg", "postgres"}, {"postgres", "postgres"}, {"pg-promise", "postgres"}, {"@vercel/postgres", "postgres"},
		{"mysql2", "mysql"}, {"mysql", "mysql"}, {"mariadb", "mariadb"},
		{"mongoose", "mongodb"}, {"mongodb", "mongodb"},
		{"ioredis", "redis"}, {"redis", "redis"}, {"bullmq", "redis"}, {"connect-redis", "redis"},
	}
	pythonDatabaseDrivers = []struct{ dependency, engine string }{
		{"psycopg", "postgres"}, {"psycopg2", "postgres"}, {"psycopg2-binary", "postgres"}, {"asyncpg", "postgres"}, {"psycopg-binary", "postgres"},
		{"pymysql", "mysql"}, {"mysqlclient", "mysql"}, {"aiomysql", "mysql"}, {"mysql-connector-python", "mysql"},
		{"pymongo", "mongodb"}, {"motor", "mongodb"}, {"beanie", "mongodb"}, {"mongoengine", "mongodb"},
		{"redis", "redis"}, {"aioredis", "redis"}, {"celery", "redis"}, {"rq", "redis"}, {"django-redis", "redis"},
	}
	goDatabaseDrivers = []struct{ module, engine string }{
		{"github.com/jackc/pgx", "postgres"}, {"github.com/lib/pq", "postgres"}, {"gorm.io/driver/postgres", "postgres"},
		{"github.com/go-sql-driver/mysql", "mysql"}, {"gorm.io/driver/mysql", "mysql"},
		{"go.mongodb.org/mongo-driver", "mongodb"},
		{"github.com/redis/go-redis", "redis"}, {"github.com/go-redis/redis", "redis"},
	}
	prismaProviderRE   = regexp.MustCompile(`(?s)datasource\s+\w+\s*\{[^}]*provider\s*=\s*"(\w+)"`)
	databaseURLSchemes = []struct{ prefix, engine string }{
		{"postgres://", "postgres"}, {"postgresql://", "postgres"}, {"mysql://", "mysql"}, {"mariadb://", "mariadb"},
		{"mongodb://", "mongodb"}, {"mongodb+srv://", "mongodb"}, {"redis://", "redis"}, {"rediss://", "redis"},
	}
	// databaseVariableNames are the conventional variables each engine's
	// URL is read from, used when the source documents none of its own.
	databaseVariableNames = map[string]string{
		"postgres": "DATABASE_URL", "mysql": "DATABASE_URL", "mariadb": "DATABASE_URL",
		"mongodb": "MONGODB_URI", "redis": "REDIS_URL",
	}
)

// prismaProvider reads the datasource provider of a Prisma schema and maps
// it to a quick-setup engine; SQLite and unknown providers yield nothing.
func prismaProvider(content []byte) string {
	match := prismaProviderRE.FindSubmatch(content)
	if match == nil {
		return ""
	}
	switch string(match[1]) {
	case "postgresql", "postgres":
		return "postgres"
	case "mysql":
		return "mysql"
	case "mongodb":
		return "mongodb"
	}
	return ""
}

// detectDatabases lists the engines a root's manifests and variables name,
// each with the variable the connection belongs in and the evidence that
// named it, in a stable order.
func detectDatabases(marker *detectedMarkers, variables []DetectedVariable, prismaProviders map[string]string) []DetectedDatabase {
	type suggestion struct {
		evidence string
		variable string
	}
	engines := map[string]*suggestion{}
	order := []string{}
	suggest := func(engine, evidence string) {
		if engine == "" {
			return
		}
		if engines[engine] == nil {
			engines[engine] = &suggestion{evidence: evidence}
			order = append(order, engine)
		}
	}
	if len(marker.packageJSON) > 0 {
		var manifest nodeManifest
		if parseNodeManifest(marker.packageJSON, &manifest) {
			for _, driver := range nodeDatabaseDrivers {
				if manifest.has(driver.dependency) {
					suggest(driver.engine, driver.dependency+" in "+path.Join(marker.root, "package.json"))
				}
			}
		}
	}
	if marker.hasPythonManifest() {
		deps := readPythonDependencies(marker.pythonFiles)
		for _, driver := range pythonDatabaseDrivers {
			if deps.has(driver.dependency) {
				suggest(driver.engine, driver.dependency+" in "+path.Join(marker.root, deps.source))
			}
		}
	}
	if len(marker.goModContent) > 0 {
		module := string(marker.goModContent)
		for _, driver := range goDatabaseDrivers {
			if strings.Contains(module, driver.module) {
				suggest(driver.engine, driver.module+" in "+path.Join(marker.root, "go.mod"))
			}
		}
	}
	prismaPaths := make([]string, 0, len(prismaProviders))
	for schema := range prismaProviders {
		prismaPaths = append(prismaPaths, schema)
	}
	sort.Strings(prismaPaths)
	for _, schema := range prismaPaths {
		if strings.HasPrefix(schema, rootPrefix(marker.root)) {
			suggest(prismaProviders[schema], "Prisma datasource in "+schema)
		}
	}
	for _, variable := range variables {
		engine := ""
		for _, scheme := range databaseURLSchemes {
			if strings.HasPrefix(strings.ToLower(variable.Example), scheme.prefix) {
				engine = scheme.engine
			}
		}
		if engine == "" {
			engine = engineForVariableName(variable.Name)
		}
		// Laravel's .env.example names its engine in DB_CONNECTION and reads
		// a whole URL from DB_URL.
		if engine == "" && variable.Name == "DB_CONNECTION" {
			engine = map[string]string{"mysql": "mysql", "mariadb": "mariadb", "pgsql": "postgres", "postgres": "postgres"}[strings.ToLower(variable.Example)]
			if engine != "" {
				suggest(engine, variable.Name+"="+strings.ToLower(variable.Example)+" in "+variable.Sources[0])
				if engines[engine].variable == "" {
					engines[engine].variable = "DB_URL"
				}
				continue
			}
		}
		if engine == "" {
			continue
		}
		suggest(engine, variable.Name+" in "+variable.Sources[0])
		if engines[engine].variable == "" && (strings.Contains(variable.Name, "URL") || strings.Contains(variable.Name, "URI") || strings.Contains(variable.Name, "DSN")) {
			engines[engine].variable = variable.Name
		}
	}
	// A generic DATABASE_URL belongs to the one relational engine the
	// dependencies named; with several, or with none, it names nothing.
	for _, variable := range variables {
		if variable.Name != "DATABASE_URL" || engineForVariableName(variable.Name) != "" {
			continue
		}
		relational := []string{}
		for _, engine := range order {
			if engine != "redis" && engine != "mongodb" && engines[engine].variable == "" {
				relational = append(relational, engine)
			}
		}
		if len(relational) == 1 {
			engines[relational[0]].variable = variable.Name
		}
	}
	result := make([]DetectedDatabase, 0, len(order))
	for _, engine := range order {
		variable := engines[engine].variable
		if variable == "" {
			variable = databaseVariableNames[engine]
		}
		result = append(result, DetectedDatabase{Engine: engine, Variable: variable, Evidence: engines[engine].evidence})
	}
	return result
}

// engineForVariableName reads an engine out of a variable's own name:
// REDIS_URL, MONGODB_URI, POSTGRES_HOST. A plain DATABASE_URL names none.
func engineForVariableName(name string) string {
	switch {
	case strings.HasPrefix(name, "REDIS"):
		return "redis"
	case strings.HasPrefix(name, "MONGO"):
		return "mongodb"
	case strings.HasPrefix(name, "POSTGRES") || strings.HasPrefix(name, "PG"):
		return "postgres"
	case strings.HasPrefix(name, "MARIADB"):
		return "mariadb"
	case strings.HasPrefix(name, "MYSQL"):
		return "mysql"
	}
	return ""
}
