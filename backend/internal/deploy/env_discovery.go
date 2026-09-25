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
// the like. `.env` and the other files a framework loads with real values
// (envRealFile) are read for names only.
func envTemplateFile(name string) bool {
	if envRealFile(name) {
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

// envRealFile names the committed env files Next.js, Vite and dotenv load
// into a production build or process. Their values are real, so only the
// names leave the repository — and whether a value points at loopback, which
// is a fact about the value rather than the value itself.
func envRealFile(name string) bool {
	switch name {
	case ".env", ".env.local", ".env.production", ".env.production.local":
		return true
	}
	return false
}

// envReadFlags record how a name was read, per source file, so a root keeps
// only what its own sources say about it.
type envReadFlags uint16

const (
	// envReadBuild: the value is read while the build runs — a framework
	// config file, a static env import, a compile-time macro.
	envReadBuild envReadFlags = 1 << iota
	// envReadInlined: a define or env block compiles the value into the
	// browser bundle regardless of its prefix.
	envReadInlined
	// envReadRequired: read in a form that fails without a value, at a
	// position that runs when the application starts or builds.
	envReadRequired
	// envReadRequiredForm: read in a form that fails without a value
	// somewhere that may only run on one code path.
	envReadRequiredForm
	// envReadBindHost: HOST is read as the address a server listens on.
	envReadBindHost
	// envReadLocalhost: a committed real env file's value points at loopback.
	envReadLocalhost
)

var (
	envNameRE       = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,127}$`)
	envTemplateLine = regexp.MustCompile(`^(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*[=:]\s*(.*)$`)
	// envReferenceREs are how each language reads its environment. Every
	// expression captures the variable name in group 1.
	envReferenceREs = []*regexp.Regexp{
		regexp.MustCompile(`process\.env\.([A-Z][A-Z0-9_]+)`),
		regexp.MustCompile(`process\.env\[['"]([A-Z][A-Z0-9_]+)['"]\]`),
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
		// Streamlit's st.secrets reads .streamlit/secrets.toml, which the
		// Python recipe's start command writes from these variables.
		regexp.MustCompile(`\bst\.secrets\[\s*['"]([A-Z][A-Z0-9_]+)['"]\s*\]`),
		regexp.MustCompile(`\bst\.secrets\.get\(\s*['"]([A-Z][A-Z0-9_]+)['"]`),
		regexp.MustCompile(`\bst\.secrets\.([A-Z][A-Z0-9_]+)\b`),
	}
	// importMetaEnvRE is Vite's read; a handful of its names are the
	// bundler's own and are never set from the environment.
	importMetaEnvRE     = regexp.MustCompile(`import\.meta\.env\.([A-Z][A-Z0-9_]+)`)
	importMetaBuiltins  = map[string]bool{"MODE": true, "DEV": true, "PROD": true, "SSR": true, "BASE_URL": true, "SITE": true, "ASSETS_PREFIX": true}
	envJavaScriptSource = map[string]bool{".js": true, ".mjs": true, ".cjs": true, ".ts": true, ".mts": true, ".tsx": true, ".jsx": true, ".vue": true, ".svelte": true, ".astro": true}
	// envProvidedNames are set by the platform, the runtime or the shell;
	// listing them would ask the operator for values the deployment supplies.
	// HOST is not among them: the runtime injects only PORT, and an
	// application that binds HOST needs a row that says so.
	envProvidedNames = map[string]bool{
		"PORT": true, "HOSTNAME": true, "NODE_ENV": true, "PATH": true, "HOME": true, "PWD": true,
		"USER": true, "SHELL": true, "LANG": true, "LC_ALL": true, "TERM": true, "TZ": true, "CI": true,
		"NEXT_RUNTIME": true, "NEXT_PHASE": true, "PYTHONUNBUFFERED": true, "PYTHONPATH": true, "VIRTUAL_ENV": true,
		"NODE_OPTIONS": true, "npm_package_version": true,
	}
	// envProvidedInJavaScript are names JavaScript reads for its own tools:
	// DEBUG is the `debug` package's namespace list there, while in Python it
	// is the framework's debug mode and has to be seen.
	envProvidedInJavaScript = map[string]bool{"DEBUG": true}
	envSourceExtensions     = map[string]bool{
		".js": true, ".mjs": true, ".cjs": true, ".ts": true, ".mts": true, ".tsx": true, ".jsx": true,
		".py": true, ".go": true, ".rb": true, ".php": true, ".vue": true, ".svelte": true, ".astro": true,
		".java": true, ".kt": true, ".kts": true, ".scala": true, ".clj": true, ".rs": true, ".cs": true, ".fs": true,
		".ex": true, ".exs": true, ".dart": true, ".swift": true, ".cr": true, ".hs": true, ".gleam": true,
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
	// factFiles and factBytes are the fact files' own budget, so an app/
	// tree walked first cannot crowd out the credentials, Puma and
	// database.yml facts a root's classification rests on.
	factFiles, factBytes int64
	found                map[string]*DetectedVariable
	order                []string
	// positions records where each name was first seen in each source, in
	// walk order, so a template's names can be listed in the file's order.
	positions map[string]map[string]int
	// flags records how each name was read in each source.
	flags map[string]map[string]envReadFlags
	// examples keeps a documented default seen in configuration (a Spring
	// `${X:default}`) per name and source, for a root to use as its hint.
	examples  map[string]map[string]string
	counter   int
	exhausted bool
	// facts are what the environment's classification needs beyond names:
	// the few manifests and settings files that say which framework issues
	// which secret, and per-source observations. See detect_variables.go.
	facts environmentFacts
	roots []string
}

const (
	envScanMaxFiles = 400
	envScanMaxBytes = 3 << 20
	envScanMaxFile  = 256 << 10
	envFactMaxFiles = 64
	envFactMaxBytes = 1 << 20
)

func newEnvScanner() *envScanner {
	return &envScanner{
		found: map[string]*DetectedVariable{}, positions: map[string]map[string]int{},
		flags: map[string]map[string]envReadFlags{}, examples: map[string]map[string]string{},
		facts: environmentFacts{files: map[string][]byte{}},
	}
}

// scannable reports whether a source file is worth reading for references:
// application code, not tests, fixtures, documentation or generated files —
// plus the configuration templates and manifests environmentFactFile names.
func (s *envScanner) scannable(rel, name string) bool {
	if s.exhausted {
		return false
	}
	if !envSourceExtensions[path.Ext(name)] && !environmentFactFile(rel, name) && envConfigKind(rel, name) == "" {
		return false
	}
	if strings.HasSuffix(name, ".d.ts") || strings.HasSuffix(name, ".min.js") || strings.HasSuffix(name, "_test.go") ||
		strings.HasPrefix(name, "test_") || name == "conftest.py" || strings.HasSuffix(name, "_test.exs") ||
		strings.HasSuffix(name, "_spec.rb") || strings.HasSuffix(name, "tests.cs") ||
		strings.Contains(name, ".test.") || strings.Contains(name, ".spec.") || strings.Contains(name, ".stories.") {
		return false
	}
	return !envSkippedPath(rel)
}

func envSkippedPath(rel string) bool {
	for _, segment := range strings.Split(path.Dir(rel), "/") {
		if envSkippedDirs[segment] {
			return true
		}
	}
	return false
}

// factFile reports a fact file the scan still has room for, whatever the
// source budget says.
func (s *envScanner) factFile(rel, name string) bool {
	return environmentFactFile(rel, name) && !envSkippedPath(rel) && s.factFiles < envFactMaxFiles
}

// admit reserves a file from the budget that applies to it: a fact file's
// own, else the source scan's. False means it is not read.
func (s *envScanner) admit(rel, name string, size int64) bool {
	if s.factFile(rel, name) && size <= envScanMaxFile && s.factBytes+size <= envFactMaxBytes {
		s.factFiles++
		s.factBytes += size
		return true
	}
	return s.scannable(rel, name) && s.budget(size)
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
	s.recordRead(name, source, example, 0)
}

func (s *envScanner) recordRead(name, source, example string, flags envReadFlags) {
	if !envNameRE.MatchString(name) || envProvidedNames[name] ||
		(envProvidedInJavaScript[name] && envJavaScriptSource[path.Ext(source)]) {
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
		s.flags[name] = map[string]envReadFlags{}
	}
	if _, seen := s.positions[name][source]; !seen {
		s.positions[name][source] = s.counter
		s.counter++
	}
	s.flags[name][source] |= flags
	if len(variable.Sources) < 4 {
		for _, existing := range variable.Sources {
			if existing == source {
				return
			}
		}
		variable.Sources = append(variable.Sources, source)
	}
}

// recordDefault keeps a default a configuration file documents beside its
// read, the way a template's example is kept: as a hint, never a value.
func (s *envScanner) recordDefault(name, source, value string) {
	if value = envExampleValue(value); value == "" {
		return
	}
	if s.examples[name] == nil {
		s.examples[name] = map[string]string{}
	}
	if _, seen := s.examples[name][source]; !seen {
		s.examples[name][source] = value
	}
}

// scanTemplate reads a dotenv-shaped file. Values are kept as examples only
// from a documented template and only when they carry no credential
// material; a real .env's values never leave the repository — only whether
// one points at loopback, which the container cannot reach.
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
		flags := envReadFlags(0)
		if !real {
			example = envExampleValue(match[2])
		} else {
			value := unquotedEnvValue(match[2])
			if !bindAddressName(match[1]) && loopbackValue(match[1], value) {
				flags |= envReadLocalhost
			}
			for _, scheme := range databaseURLSchemes {
				if strings.HasPrefix(strings.ToLower(value), scheme.prefix) {
					s.facts.observe(rel, observeCommittedEngine, scheme.engine+"|"+match[1])
					break
				}
			}
		}
		if match[1] == "HOST" && loopbackOrAnyAddress(unquotedEnvValue(match[2])) {
			flags |= envReadBindHost
		}
		s.recordRead(match[1], rel, example, flags)
	}
}

// unquotedEnvValue strips the quoting and trailing comment of a dotenv
// value without judging it; envExampleValue is the judging variant.
func unquotedEnvValue(raw string) string {
	value := strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(value, `"`) && strings.Count(value, `"`) >= 2:
		return value[1 : 1+strings.Index(value[1:], `"`)]
	case strings.HasPrefix(value, "'") && strings.Count(value, "'") >= 2:
		return value[1 : 1+strings.Index(value[1:], "'")]
	}
	if comment := strings.Index(value, " #"); comment >= 0 {
		value = strings.TrimSpace(value[:comment])
	}
	return value
}

// envExampleValue strips quotes and trailing comments from a template value
// and drops anything credential-shaped or too long to be a hint.
func envExampleValue(raw string) string {
	value := unquotedEnvValue(raw)
	if value == "" || len(value) > 256 || strings.ContainsAny(value, "\x00\r\n") ||
		rejectPlanSecretLiteral("example", value) != nil {
		return ""
	}
	return value
}

func (s *envScanner) scanSource(rel string, content []byte) {
	name := strings.ToLower(path.Base(rel))
	s.observeFacts(rel, name, content)
	if kind := envConfigKind(rel, name); kind != "" {
		s.scanConfig(kind, rel, content)
		return
	}
	extension := path.Ext(name)
	if !envSourceExtensions[extension] {
		return
	}
	for _, expression := range envReferenceREs {
		for _, match := range expression.FindAllSubmatch(content, -1) {
			s.record(string(match[1]), rel, "")
		}
	}
	for _, match := range importMetaEnvRE.FindAllSubmatch(content, -1) {
		if !importMetaBuiltins[string(match[1])] {
			s.record(string(match[1]), rel, "")
		}
	}
	s.scanLanguage(extension, rel, name, content)
}

// variables returns the discovered variables that belong to a root: those
// found under it (and not under a nested root), plus every variable found
// outside all roots — a repository-level .env.example documents the
// application in apps/web even though nothing else at the repository root is
// the root's own. The root's own documented template comes first in file
// order, then a committed .env's names, then what the repository documents
// above the root, and finally the names only the code reads, by name — so
// the form reads the way the repository documents itself. How each name was
// read in the root's own sources becomes its phase, requiredness and
// loopback evidence.
func (s *envScanner) variables(root string, roots []string) []DetectedVariable {
	s.roots = roots
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
			if r := s.sourceRank(root, source); r >= 0 && r < bestRank {
				best, bestRank = index, r
			}
		}
		// A name whose first four sources all belong elsewhere may still be
		// read here; the flags keep every source, so they are asked too.
		if best < 0 {
			others := make([]string, 0, len(s.positions[name]))
			for source := range s.positions[name] {
				others = append(others, source)
			}
			sort.Slice(others, func(i, j int) bool { return s.positions[name][others[i]] < s.positions[name][others[j]] })
			for _, source := range others {
				if r := s.sourceRank(root, source); r >= 0 && (best < 0 || r < bestRank) {
					best, bestRank = len(variable.Sources), r
					variable.Sources = append(append([]string(nil), variable.Sources...), source)
				}
			}
		}
		if best < 0 {
			continue
		}
		sources := []string{variable.Sources[best]}
		for index, source := range variable.Sources {
			if index != best && s.sourceRank(root, source) >= 0 {
				sources = append(sources, source)
			}
		}
		if len(sources) > 4 {
			sources = sources[:4]
		}
		variable.Sources = sources
		s.applyReadFlags(root, name, &variable)
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

// sourceRank orders a source for a root, or returns -1 when the source
// belongs to another root.
func (s *envScanner) sourceRank(root, source string) int {
	owned := s.claimed(root, source)
	if !owned && !s.orphan(source) {
		return -1
	}
	name := path.Base(source)
	switch {
	case !envTemplateFile(name):
		return 4
	case owned && !envRealFile(name):
		return 0
	case owned:
		return 1
	case !envRealFile(name):
		return 2
	}
	return 3
}

func (s *envScanner) claimed(root, source string) bool {
	prefix := rootPrefix(root)
	if !strings.HasPrefix(source, prefix) {
		return false
	}
	for _, other := range s.roots {
		if other != root && strings.HasPrefix(other, prefix) && strings.HasPrefix(source, rootPrefix(other)) {
			return false
		}
	}
	return true
}

func (s *envScanner) orphan(source string) bool {
	for _, other := range s.roots {
		if strings.HasPrefix(source, rootPrefix(other)) {
			return false
		}
	}
	return true
}

// rootReadFlags merges how a name was read across the sources a root owns.
func (s *envScanner) rootReadFlags(root, name string) (envReadFlags, []string) {
	var merged envReadFlags
	localhost := []string{}
	sources := make([]string, 0, len(s.flags[name]))
	for source := range s.flags[name] {
		sources = append(sources, source)
	}
	sort.Strings(sources)
	for _, source := range sources {
		if s.sourceRank(root, source) < 0 {
			continue
		}
		flags := s.flags[name][source]
		merged |= flags
		if flags&envReadLocalhost != 0 {
			localhost = append(localhost, source)
		}
	}
	return merged, localhost
}

// rootBuildSources are the files of a root that read the variable while the
// build runs, sorted and bounded.
func (s *envScanner) rootBuildSources(root, name string) []string {
	sources := []string{}
	for source, flags := range s.flags[name] {
		if flags&envReadBuild != 0 && s.sourceRank(root, source) >= 0 {
			sources = append(sources, source)
		}
	}
	sort.Strings(sources)
	if len(sources) > 8 {
		sources = sources[:8]
	}
	return sources
}

// applyReadFlags turns the merged read flags into the variable's own
// evidence: its phase, whether it is required, and which committed file
// gives it a loopback value.
func (s *envScanner) applyReadFlags(root, name string, variable *DetectedVariable) {
	flags, localhost := s.rootReadFlags(root, name)
	if flags&envReadBuild != 0 {
		variable.Phase = "build"
		variable.BuildSources = s.rootBuildSources(root, name)
	}
	if flags&envReadInlined != 0 {
		variable.BrowserInlined = true
	}
	switch {
	case flags&envReadRequired != 0:
		variable.Required = true
	case flags&envReadRequiredForm != 0:
		variable.RequiredRead = true
	}
	if len(localhost) > 0 {
		variable.LocalhostIn = localhost[0]
	}
	if variable.Example == "" {
		sources := make([]string, 0, len(s.examples[name]))
		for source := range s.examples[name] {
			if s.sourceRank(root, source) >= 0 {
				sources = append(sources, source)
			}
		}
		sort.Strings(sources)
		if len(sources) > 0 {
			variable.Example = s.examples[name][sources[0]]
		}
	}
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
		// Adapters that name their engine: Payload's database packages, and
		// Medusa, which runs on Postgres alone.
		{"@payloadcms/db-postgres", "postgres"}, {"@payloadcms/db-vercel-postgres", "postgres"}, {"@payloadcms/db-mongodb", "mongodb"},
		{"@medusajs/medusa", "postgres"},
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
func detectDatabases(marker *detectedMarkers, variables []DetectedVariable, prismaProviders map[string]string, extra ...databaseEvidence) []DetectedDatabase {
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
	if marker.php != nil {
		for _, found := range marker.php.databaseEvidence() {
			suggest(found.engine, found.evidence)
		}
	}
	// Manifests detection does not hold as markers — Gemfile.lock, mix.exs —
	// and those of compiled languages arrive from the root's classification.
	for _, found := range extra {
		suggest(found.engine, found.evidence)
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
		// a whole URL from DB_URL — from Laravel 11 on. Laravel 10 and older
		// read DATABASE_URL and ignore DB_URL entirely, so the suggestion
		// follows whichever name the application's own config reads.
		if engine == "" && variable.Name == "DB_CONNECTION" {
			engine = map[string]string{"mysql": "mysql", "mariadb": "mariadb", "pgsql": "postgres", "postgres": "postgres"}[strings.ToLower(variable.Example)]
			if engine != "" {
				suggest(engine, variable.Name+"="+strings.ToLower(variable.Example)+" in "+variable.Sources[0])
				if engines[engine].variable == "" {
					engines[engine].variable = laravelURLVariable(marker, variables)
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
	// A generic DATABASE_URL (Payload's DATABASE_URI) belongs to the one
	// relational engine the dependencies named; with several, or with none,
	// it names nothing.
	for _, variable := range variables {
		if (variable.Name != "DATABASE_URL" && variable.Name != "DATABASE_URI") || engineForVariableName(variable.Name) != "" {
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
