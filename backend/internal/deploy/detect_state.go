package deploy

import (
	"context"
	"encoding/json"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Persistent state detection. An application that writes to its own
// filesystem — a SQLite file, an uploads directory, a key ring — loses all of
// it when the next release replaces the container, and nothing about a
// successful deploy says so. Detection reads the source as data to find where
// that state lives, and says where a managed volume can hold it without hiding
// code or migrations the image ships: a code-free directory, or a directory
// the state can be moved into through a variable the application already
// reads. Everything here is bounded text matching; no repository file is
// executed.

// DetectedPersistentPath is one place an application keeps state between
// requests. Path is where it lands in the container today. Target is the
// directory a managed volume can be mounted on; it is empty when no directory
// can hold the state without also hiding code, and the state is then only
// reported. Variable and Value move the state under Target through a variable
// the application reads; DatabaseVariable names the variable through which a
// linked server database replaces the file altogether, and
// ConnectionVariable the one choosing the database driver, which set to
// anything but sqlite takes the file out of use (Laravel's DB_CONNECTION).
type DetectedPersistentPath struct {
	Kind               string `json:"kind"`
	Path               string `json:"path"`
	Target             string `json:"target,omitempty"`
	Variable           string `json:"variable,omitempty"`
	Value              string `json:"value,omitempty"`
	DatabaseVariable   string `json:"databaseVariable,omitempty"`
	ConnectionVariable string `json:"connectionVariable,omitempty"`
	Source             string `json:"source"`
	Reason             string `json:"reason"`
}

// The kinds of state detection recognises. Each has its own preflight code so
// a finding names what would be lost.
const (
	PersistentSQLite  = "sqlite"
	PersistentUploads = "uploads"
	PersistentStorage = "storage"
	PersistentVolume  = "volume"
	PersistentKeys    = "keys"
)

func validPersistentKind(kind string) bool {
	switch kind {
	case PersistentSQLite, PersistentUploads, PersistentStorage, PersistentVolume, PersistentKeys:
		return true
	}
	return false
}

// validPersistentPaths bounds what a saved detection may carry, the way the
// other candidate fields are bounded: absolute clean container paths, variable
// names the planner accepts, and nothing credential-shaped.
func validPersistentPaths(paths []DetectedPersistentPath) bool {
	if len(paths) > 16 {
		return false
	}
	absolute := func(value string, allowRoot bool) bool {
		return len(value) <= 4096 && path.IsAbs(value) && path.Clean(value) == value &&
			(allowRoot || value != "/") && !strings.ContainsAny(value, "\x00\r\n")
	}
	for _, entry := range paths {
		if !validPersistentKind(entry.Kind) || !absolute(entry.Path, true) ||
			(entry.Target != "" && !absolute(entry.Target, false)) ||
			(entry.Variable != "" && ValidateEnvKey(entry.Variable) != nil) ||
			(entry.DatabaseVariable != "" && ValidateEnvKey(entry.DatabaseVariable) != nil) ||
			(entry.ConnectionVariable != "" && ValidateEnvKey(entry.ConnectionVariable) != nil) ||
			(entry.Value != "" && entry.Variable == "") ||
			len(entry.Value) > 512 || strings.ContainsAny(entry.Value, "\x00\r\n") ||
			rejectPlanSecretLiteral("persistent path value", entry.Value) != nil ||
			URLHasCredentials(entry.Value) ||
			len(entry.Source) > 4096 || strings.ContainsAny(entry.Source, "\x00\r\n") ||
			entry.Reason == "" || len(entry.Reason) > 512 || strings.ContainsAny(entry.Reason, "\x00\r\n") ||
			rejectPlanSecretLiteral("persistent path reason", entry.Reason) != nil {
			return false
		}
	}
	return true
}

// stateScanner collects, during the detection walk, the files state and
// schema detection read. It has its own budgets apart from detection's
// limits, like the environment scanner: running out only means fewer
// findings. Configuration files and source files are budgeted separately so a
// large source tree walked first cannot crowd out the one file that says
// where the database lives.
type stateScanner struct {
	config, source stateBudget
	// contents holds the text of the configuration files and the few source
	// files that mention a state or schema library, by repository path.
	contents map[string][]byte
	// present records files whose existence is the evidence (a migration, a
	// model snapshot), by repository path.
	present map[string]bool
	// committed records every directory holding a committed file; filled the
	// ones holding a file other than a placeholder. A volume mounted over a
	// filled directory would hide what the image ships there.
	committed, filled map[string]bool
}

type stateBudget struct {
	files, bytes, maxFiles, maxBytes int64
	exhausted                        bool
}

func (b *stateBudget) reserve(size int64) bool {
	if b.exhausted || size > stateScanMaxFile {
		return false
	}
	if b.files+1 > b.maxFiles || b.bytes+size > b.maxBytes {
		b.exhausted = true
		return false
	}
	b.files++
	b.bytes += size
	return true
}

const (
	stateScanMaxFile = 256 << 10
	stateMaxPresent  = 2048
	stateMaxDirs     = 20000
)

func newStateScanner() *stateScanner {
	return &stateScanner{
		config:   stateBudget{maxFiles: 96, maxBytes: 2 << 20},
		source:   stateBudget{maxFiles: 300, maxBytes: 3 << 20},
		contents: map[string][]byte{}, present: map[string]bool{},
		committed: map[string]bool{}, filled: map[string]bool{},
	}
}

// statePlaceholderFiles keep an empty directory in Git; they are not content
// a volume could hide.
var statePlaceholderFiles = map[string]bool{".gitkeep": true, ".keep": true, ".gitignore": true, ".empty": true}

// stateConfigFile names the files a framework declares its storage, database
// and seed configuration in. lowerRel is the lowercased repository path.
func stateConfigFile(lowerRel, name string) bool {
	parent := path.Base(path.Dir(lowerRel))
	switch name {
	case "database.yml", "storage.yml", "deploy.yml", "database.php", "database.js", "database.ts":
		return parent == "config"
	case "production.rb":
		return parent == "environments"
	case "seeds.rb":
		return parent == "db"
	case "databaseseeder.php":
		return parent == "seeders"
	case "alembic.ini", "prestart.sh", "settings.py", "appsettings.json", "appsettings.production.json",
		"gemfile.lock", "gemfile":
		return true
	case "env.py":
		// Alembic's environment script, read for where it connects.
		return true
	case "mix.exs":
		return true
	case "runtime.exs":
		return parent == "config"
	}
	if strings.HasSuffix(name, ".prisma") || strings.HasPrefix(name, "prisma.config.") || strings.HasPrefix(name, "drizzle.config.") {
		return true
	}
	// Split Django settings (settings/base.py, settings/production.py).
	if strings.HasSuffix(name, ".py") && parent == "settings" {
		return true
	}
	// A seed file is read to learn whether it clears tables first.
	return strings.HasPrefix(name, "seed.") && stateSourceExtensions[path.Ext(name)]
}

var stateSourceExtensions = map[string]bool{
	".js": true, ".mjs": true, ".cjs": true, ".ts": true, ".mts": true, ".cts": true,
	".py": true, ".go": true, ".rs": true, ".cs": true,
}

// stateSourceTokens are what a source file must mention to be kept: a SQLite
// library or URL, an upload handler, a Data Protection or EF Core call.
var stateSourceTokens = []string{
	"sqlite", "Sqlite", "multer", "MEDIA_ROOT", "SQLALCHEMY", "create_engine",
	"AddIdentity", "AddDefaultIdentity", "AddCookie", "AddRazorPages", "AddControllersWithViews", "AddAntiforgery",
	"AddServerSideBlazor", "AddRazorComponents", "PersistKeysTo", "Migrate(", "MigrateAsync(", "EnsureCreated",
	"JSONFile",
}

var stateSkippedDirs = map[string]bool{
	"test": true, "tests": true, "__tests__": true, "spec": true, "e2e": true, "fixtures": true, "mocks": true,
	"__mocks__": true, "docs": true, "examples": true, "example": true, "bin": true, "obj": true,
	"migrations": true, "versions": true,
}

// observe sees every regular file the detection walk visits. rel is the
// slash-separated repository path and name the lowercased base name.
func (s *stateScanner) observe(rel, name, fullPath string, entry fs.DirEntry) {
	placeholder := statePlaceholderFiles[name]
	for dir, depth := path.Dir(rel), 0; dir != "." && dir != "/" && depth < 8 && len(s.committed) < stateMaxDirs; dir, depth = path.Dir(dir), depth+1 {
		if s.committed[dir] && (placeholder || s.filled[dir]) {
			break
		}
		s.committed[dir] = true
		if !placeholder {
			s.filled[dir] = true
		}
	}
	lower := strings.ToLower(rel)
	if len(s.present) < stateMaxPresent && stateEvidenceFile(lower, name) {
		s.present[rel] = true
	}
	config := stateConfigFile(lower, name)
	source := !config && stateSourceExtensions[path.Ext(name)] && !stateSkippedSource(lower, name)
	if !config && !source {
		return
	}
	info, err := entry.Info()
	if err != nil {
		return
	}
	budget := &s.source
	if config {
		budget = &s.config
	}
	if !budget.reserve(info.Size()) {
		return
	}
	content, _, err := readDetectionFile(fullPath, stateScanMaxFile)
	if err != nil {
		return
	}
	if source && !mentionsAny(content, stateSourceTokens) {
		return
	}
	s.contents[rel] = content
}

// stateEvidenceFile names the files whose presence alone is evidence: an
// Alembic revision, an EF Core model snapshot, a committed SQLite database.
func stateEvidenceFile(lowerRel, name string) bool {
	switch {
	case strings.HasSuffix(name, ".py") && path.Base(path.Dir(lowerRel)) == "versions":
		return true
	case strings.HasSuffix(name, "modelsnapshot.cs"):
		return true
	case strings.HasSuffix(name, ".db") || strings.HasSuffix(name, ".sqlite") || strings.HasSuffix(name, ".sqlite3"):
		return true
	case path.Base(path.Dir(lowerRel)) == "fixtures" && djangoFixtureExtensions[path.Ext(name)]:
		// A Django application's fixtures, which loaddata seeds from.
		return true
	}
	return false
}

var djangoFixtureExtensions = map[string]bool{".json": true, ".yaml": true, ".yml": true, ".xml": true}

func stateSkippedSource(lowerRel, name string) bool {
	if strings.HasSuffix(name, ".d.ts") || strings.HasSuffix(name, ".min.js") || strings.HasSuffix(name, "_test.go") ||
		strings.HasPrefix(name, "test_") || strings.Contains(name, ".test.") || strings.Contains(name, ".spec.") {
		return true
	}
	for _, segment := range strings.Split(path.Dir(lowerRel), "/") {
		if stateSkippedDirs[segment] {
			return true
		}
	}
	return false
}

func mentionsAny(content []byte, tokens []string) bool {
	text := string(content)
	for _, token := range tokens {
		if strings.Contains(text, token) {
			return true
		}
	}
	return false
}

// stateRoot is one detection root's view of what the scanner collected, with
// paths relative to that root. A file under a nested root belongs to it.
type stateRoot struct {
	root              string
	contents          map[string][]byte
	present           map[string]bool
	committed, filled map[string]bool
}

func (s *stateScanner) forRoot(root string, roots []string) stateRoot {
	view := stateRoot{
		root: root, contents: map[string][]byte{}, present: map[string]bool{},
		committed: map[string]bool{}, filled: map[string]bool{},
	}
	prefix := rootPrefix(root)
	contentPaths := make([]string, 0, len(s.contents))
	for rel := range s.contents {
		contentPaths = append(contentPaths, rel)
	}
	for _, rel := range pathsUnderRoot(contentPaths, root, roots) {
		view.contents[rel] = s.contents[prefix+rel]
	}
	presentPaths := make([]string, 0, len(s.present))
	for rel := range s.present {
		presentPaths = append(presentPaths, rel)
	}
	for _, rel := range pathsUnderRoot(presentPaths, root, roots) {
		view.present[rel] = true
	}
	for dir := range s.committed {
		if strings.HasPrefix(dir, prefix) {
			view.committed[strings.TrimPrefix(dir, prefix)] = true
		}
	}
	for dir := range s.filled {
		if strings.HasPrefix(dir, prefix) {
			view.filled[strings.TrimPrefix(dir, prefix)] = true
		}
	}
	return view
}

// file returns the content of the first of the given root-relative paths the
// scan read.
func (v stateRoot) file(names ...string) (string, []byte) {
	for _, name := range names {
		if content, ok := v.contents[name]; ok {
			return name, content
		}
	}
	return "", nil
}

// sorted lists the root-relative paths of read files matching a predicate,
// in a stable order.
func (v stateRoot) sorted(match func(string) bool) []string {
	var names []string
	for name := range v.contents {
		if match(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// stateLayout is where a candidate's files land in its container: the working
// directory relative paths resolve against, the directory the image prepares
// for data (empty when there is none a volume can safely replace), and
// whether the process may write a directory the image does not already have.
type stateLayout struct {
	workdir string
	dataDir string
	root    bool
	// repoDirsOnly limits mount targets to directories the repository itself
	// commits: an image built by its own Dockerfile as an unprivileged user
	// owns only what it copied in, and a volume over a directory the image
	// lacks would be owned by root.
	repoDirsOnly bool
}

func candidateStateLayout(candidate *DetectedCandidate, marker *detectedMarkers) (stateLayout, bool) {
	switch candidate.BuildMethod {
	case BuildRecipe:
		switch candidate.Recipe {
		case "node", "python", "php", "deno", "ruby":
			return stateLayout{workdir: "/app", dataDir: "/data", root: true}, true
		case "go", "rust":
			return stateLayout{workdir: compiledRuntimeHome, dataDir: compiledRuntimeHome + "/data"}, true
		case "dotnet":
			return stateLayout{workdir: "/app", dataDir: dotnetRuntimeDataDir}, true
		case "java":
			return stateLayout{workdir: "/app"}, true
		}
	case BuildDockerfile:
		workdir, user := dockerfileFinalWorkdirUser(marker.dockerfileContent)
		layout := stateLayout{workdir: workdir, root: user == "" || user == "root" || user == "0" || strings.HasPrefix(user, "0:") || strings.HasPrefix(user, "root:")}
		if layout.root {
			layout.dataDir = "/data"
		} else {
			layout.repoDirsOnly = true
		}
		return layout, true
	}
	return stateLayout{}, false
}

// containerPath places a root-relative path in the container.
func (l stateLayout) containerPath(rel string) string {
	if path.IsAbs(rel) {
		return path.Clean(rel)
	}
	return path.Join(l.workdir, rel)
}

// stateDataDirectoryNames are the directory names applications keep data in
// and nothing else. A volume over one of them hides nothing the image ships,
// provided the repository commits nothing there but placeholders.
var stateDataDirectoryNames = map[string]bool{
	"data": true, ".data": true, "storage": true, "var": true, "uploads": true, "upload": true,
	"media": true, "sqlite": true, "pb_data": true, "instance": true, ".tmp": true, "db-data": true,
}

// mountableDirectory is the directory holding a root-relative file when a
// volume can replace it: a conventional data directory the repository fills
// with nothing but placeholders.
func (l stateLayout) mountableDirectory(view stateRoot, relFile string) string {
	dir := path.Dir(path.Clean(strings.TrimPrefix(relFile, "./")))
	if dir == "." || dir == "/" || strings.HasPrefix(dir, "..") || path.IsAbs(dir) ||
		!stateDataDirectoryNames[strings.ToLower(path.Base(dir))] {
		return ""
	}
	return l.writableTarget(view, dir)
}

// writableTarget is the container directory for a root-relative directory
// when a volume may stand there: it hides nothing the repository ships, and
// the process can write it. A process running as root writes anything; an
// image built by its own Dockerfile as an unprivileged user owns only what it
// copied in, so the directory has to be committed; a recipe running
// unprivileged prepares only its data directory.
func (l stateLayout) writableTarget(view stateRoot, dir string) string {
	if view.filled[dir] {
		return ""
	}
	target := l.containerPath(dir)
	switch {
	case l.root:
		return target
	case l.repoDirsOnly && view.committed[dir]:
		return target
	case target == l.dataDir:
		return target
	}
	return ""
}

// applyStateDetection completes the candidates of one root with the state
// they keep, their schema and seed steps. It runs after the framework
// catalogues so it can read the start command they chose.
func applyStateDetection(marker *detectedMarkers, candidates []DetectedCandidate, view stateRoot, variables []DetectedVariable) {
	for index := range candidates {
		candidate := &candidates[index]
		applySchemaDetection(candidate, marker, view)
		applySeedDetection(candidate, marker, view)
		layout, ok := candidateStateLayout(candidate, marker)
		if !ok {
			continue
		}
		var found []DetectedPersistentPath
		found = append(found, nodeStatePaths(candidate, marker, view, variables, layout)...)
		found = append(found, pythonStatePaths(candidate, marker, view, variables, layout)...)
		found = append(found, laravelStatePaths(candidate, marker, view, variables, layout)...)
		found = append(found, railsStatePaths(candidate, marker, view, layout)...)
		found = append(found, compiledStatePaths(candidate, marker, view, variables, layout)...)
		found = append(found, dotnetStatePaths(candidate, marker, view, layout)...)
		found = append(found, elixirStatePaths(candidate, view, layout)...)
		if candidate.BuildMethod == BuildDockerfile {
			for _, volume := range detectedDockerfileVolumes(marker.dockerfileContent) {
				found = append(found, DetectedPersistentPath{
					Kind: PersistentVolume, Path: volume, Target: volume, Source: marker.dockerfile,
					Reason: "the Dockerfile declares VOLUME " + volume,
				})
			}
		}
		candidate.PersistentPaths = mergePersistentPaths(found)
		for _, entry := range candidate.PersistentPaths {
			candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: entry.Source, Reason: persistentEvidence(entry)})
		}
	}
}

func persistentEvidence(entry DetectedPersistentPath) string {
	reason := entry.Reason
	switch {
	case entry.Target != "" && entry.Variable != "" && entry.Value != "":
		reason += "; a volume at " + entry.Target + " keeps it, with " + entry.Variable + " pointing there"
	case entry.Target != "":
		reason += "; a volume at " + entry.Target + " keeps it between releases"
	default:
		reason += "; it is lost on every release and no directory can hold it without hiding code"
	}
	if len(reason) > 512 {
		reason = reason[:509] + "..."
	}
	return reason
}

// mergePersistentPaths keeps one entry per kind and path, and one per
// variable — a variable holds one value, and the detector that read it more
// closely comes first — in a stable order, within the bound a saved detection
// allows.
func mergePersistentPaths(found []DetectedPersistentPath) []DetectedPersistentPath {
	seen := map[string]bool{}
	var result []DetectedPersistentPath
	for _, entry := range found {
		key := entry.Kind + "\x00" + entry.Path
		if entry.Path == "" || seen[key] || (entry.Variable != "" && seen["variable\x00"+entry.Variable]) {
			continue
		}
		seen[key] = true
		if entry.Variable != "" {
			seen["variable\x00"+entry.Variable] = true
		}
		result = append(result, entry)
	}
	if !validPersistentPaths(result) {
		// A path shaped in a way the planner would refuse is dropped rather
		// than letting it make the whole detection malformed.
		kept := result[:0]
		for _, entry := range result {
			if len(kept) < 16 && validPersistentPaths([]DetectedPersistentPath{entry}) {
				kept = append(kept, entry)
			}
		}
		result = kept
	}
	return result
}

// sqliteLocation splits a SQLite connection value into the scheme prefix a
// driver expects back, the file path and a trailing query. ok is false for a
// value that names no file: a server URL, an in-memory database, a remote
// libSQL database.
func sqliteLocation(value string) (prefix, file, query string, ok bool) {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, ":memory:") || strings.Contains(value, "mode=memory") {
		return "", "", "", false
	}
	if index := strings.IndexByte(value, '?'); index >= 0 {
		value, query = value[:index], value[index:]
	}
	switch {
	case strings.HasPrefix(value, "file://"):
		prefix, file = "file:", strings.TrimPrefix(value, "file://")
	case strings.HasPrefix(value, "file:"):
		prefix, file = "file:", strings.TrimPrefix(value, "file:")
	case strings.HasPrefix(value, "sqlite://"):
		prefix, file = "sqlite://", strings.TrimPrefix(value, "sqlite://")
	case strings.HasPrefix(value, "sqlite:"):
		prefix, file = "sqlite:", strings.TrimPrefix(value, "sqlite:")
	case strings.Contains(value, "://"):
		return "", "", "", false
	default:
		file = value
	}
	if file == "" || strings.ContainsAny(file, "\x00\r\n\"'`$") {
		return "", "", "", false
	}
	return prefix, file, query, true
}

// sqliteFileName reports whether a value looks like a SQLite file rather than
// any other string a variable might hold.
func sqliteFileName(value string) bool {
	_, file, _, ok := sqliteLocation(value)
	if !ok {
		return false
	}
	lower := strings.ToLower(file)
	for _, suffix := range []string{".db", ".sqlite", ".sqlite3", ".db3"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "file:")
}

// sqliteFileExample reports whether a variable's documented example leaves
// it free to name a file on a volume: it names a SQLite file, or nothing. An
// example naming a hosted database — Turso's libsql://, an https:// endpoint
// — says the data lives elsewhere, and a local file would silently replace it.
func sqliteFileExample(example string) bool {
	return strings.TrimSpace(example) == "" || sqliteFileName(example)
}

// relocatedSQLite is the value that keeps a SQLite file named like example in
// dir, in the example's own scheme so the driver reads it the same way.
func relocatedSQLite(example, dir, defaultPrefix string) string {
	prefix, file, query, ok := sqliteLocation(example)
	base := "app.db"
	if ok {
		if name := path.Base(file); name != "." && name != "/" && name != "" {
			base = name
		}
	} else {
		prefix = defaultPrefix
	}
	if !safePersistentName(base) {
		base = "app.db"
	}
	if !safeSQLiteQuery(query) {
		query = ""
	}
	return prefix + path.Join(dir, base) + query
}

var persistentNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func safePersistentName(name string) bool { return persistentNameRE.MatchString(name) }

// A query a driver reads (mode=rwc, cache=shared, _journal=WAL) is kept; one
// that could carry anything else is not.
var sqliteQueryRE = regexp.MustCompile(`^\?[A-Za-z0-9_]+=[A-Za-z0-9_.-]+(&[A-Za-z0-9_]+=[A-Za-z0-9_.-]+)*$`)

func safeSQLiteQuery(query string) bool {
	return query == "" || (len(query) <= 128 && sqliteQueryRE.MatchString(query))
}

// persistentFileVariables are the detected variables whose documented example
// is a SQLite file: the variables a source reads its database location from.
func persistentFileVariables(variables []DetectedVariable) []DetectedVariable {
	var result []DetectedVariable
	for _, variable := range variables {
		if variable.Example != "" && sqliteFileName(variable.Example) && sqliteVariableName(variable.Name) {
			result = append(result, variable)
		}
	}
	return result
}

func sqliteVariableName(name string) bool {
	for _, part := range []string{"DATABASE", "DB", "SQLITE", "STORE"} {
		if strings.Contains(name, part) {
			return true
		}
	}
	return false
}

// uploadVariableNames are variables whose name says the application writes
// uploaded files to the directory they hold.
var uploadVariableNames = map[string]bool{
	"UPLOAD_DIR": true, "UPLOADS_DIR": true, "UPLOAD_PATH": true, "UPLOADS_PATH": true, "UPLOAD_FOLDER": true,
	"UPLOADS_FOLDER": true, "MEDIA_ROOT": true, "MEDIA_DIR": true, "UPLOAD_DIRECTORY": true,
}

// uploadVariables relocates an upload directory the application reads from a
// variable into its own volume. Only a process running as root can be given
// a directory the image does not have.
func uploadVariables(variables []DetectedVariable, layout stateLayout) []DetectedPersistentPath {
	var result []DetectedPersistentPath
	for _, variable := range variables {
		if !uploadVariableNames[variable.Name] {
			continue
		}
		example := strings.TrimSpace(variable.Example)
		if strings.Contains(example, "://") || strings.HasPrefix(example, "s3:") {
			continue
		}
		entry := DetectedPersistentPath{
			Kind: PersistentUploads, Path: layout.containerPath(firstNonEmpty(strings.TrimPrefix(example, "./"), "uploads")),
			Variable: variable.Name, Source: firstNonEmpty(firstSource(variable), "."),
			Reason: "uploaded files are written to the directory " + variable.Name + " names",
		}
		if layout.root && layout.dataDir != "" {
			entry.Target = path.Join(layout.dataDir, "uploads")
			entry.Value = entry.Target
		}
		result = append(result, entry)
	}
	return result
}

func firstSource(variable DetectedVariable) string {
	if len(variable.Sources) == 0 {
		return ""
	}
	return variable.Sources[0]
}

func variableByName(variables []DetectedVariable, name string) (DetectedVariable, bool) {
	for _, variable := range variables {
		if variable.Name == name {
			return variable, true
		}
	}
	return DetectedVariable{}, false
}

// Node --------------------------------------------------------------------

var (
	prismaDatasourceRE = regexp.MustCompile(`(?s)datasource\s+\w+\s*\{([^}]*)\}`)
	prismaFieldRE      = regexp.MustCompile(`(?m)^\s*(provider|url)\s*=\s*(env\(\s*"([A-Za-z_][A-Za-z0-9_]*)"\s*\)|"([^"]*)")`)
	// A config file's datasource url: env("X"), process.env.X or a literal.
	configEnvURLRE     = regexp.MustCompile(`\burl\s*:\s*(?:env\(\s*["']([A-Za-z_][A-Za-z0-9_]*)["']|process\.env\.([A-Za-z_][A-Za-z0-9_]*)|process\.env\[\s*["']([A-Za-z_][A-Za-z0-9_]*)["']\s*\])`)
	configLiteralURLRE = regexp.MustCompile(`\burl\s*:\s*["'\x60]([^"'\x60]+)["'\x60]`)
	drizzleDialectRE   = regexp.MustCompile(`\bdialect\s*:\s*["'](sqlite|turso)["']`)
	jsSQLiteImportRE   = regexp.MustCompile(`(?:from\s+|require\(\s*)["'](better-sqlite3|sqlite3|sqlite|bun:sqlite|@libsql/client|libsql|drizzle-orm/better-sqlite3|drizzle-orm/libsql|drizzle-orm/bun-sqlite|node:sqlite)["']`)
	jsSQLiteLiteralRE  = regexp.MustCompile(`(?:Database|DatabaseSync|createClient|drizzle|open)\s*\(\s*(?:\{[^}]*?(?:url|filename)\s*:\s*)?["'\x60]([^"'\x60\s]+)["'\x60]`)
	multerDestRE       = regexp.MustCompile(`\b(?:dest|destination)\s*:\s*(?:["'\x60]([^"'\x60]+)["'\x60]|process\.env\.([A-Z][A-Z0-9_]*))`)
	multerCallbackRE   = regexp.MustCompile(`\bcb\(\s*null\s*,\s*["'\x60]([^"'\x60]+)["'\x60]\s*\)`)
	lowdbFileRE        = regexp.MustCompile(`\bJSONFile(?:Sync)?(?:Preset)?\(\s*["'\x60]([^"'\x60]+)["'\x60]`)
)

// nodeSQLiteLibraries are the packages that open a SQLite file themselves;
// libsqlLibraries want a file: URL rather than a plain path.
var (
	nodeSQLiteLibraries = []string{"better-sqlite3", "sqlite3", "sqlite", "@libsql/client", "libsql", "@payloadcms/db-sqlite", "@databases/sqlite"}
	libsqlLibraries     = map[string]bool{"@libsql/client": true, "libsql": true, "@payloadcms/db-sqlite": true}
)

func nodeStatePaths(candidate *DetectedCandidate, marker *detectedMarkers, view stateRoot, variables []DetectedVariable, layout stateLayout) []DetectedPersistentPath {
	if len(marker.packageJSON) == 0 || (candidate.Recipe != "node" && candidate.BuildMethod != BuildDockerfile) {
		return nil
	}
	if candidate.OutputDirectory != "" {
		// A static site is served by nginx; nothing it builds writes at runtime.
		return nil
	}
	var manifest nodeManifest
	if !parseNodeManifest(marker.packageJSON, &manifest) {
		return nil
	}
	var result []DetectedPersistentPath
	prisma := prismaSQLitePaths(manifest, view, variables, layout)
	result = append(result, prisma...)
	libsql := false
	sqlite := false
	for _, library := range nodeSQLiteLibraries {
		if manifest.has(library) {
			sqlite = true
			libsql = libsql || libsqlLibraries[library]
		}
	}
	defaultPrefix := ""
	if libsql {
		defaultPrefix = "file:"
	}
	claimed := map[string]bool{}
	for _, entry := range prisma {
		claimed[entry.Variable] = true
	}
	if sqlite && len(prisma) == 0 {
		drizzleConfigs := view.sorted(func(name string) bool { return strings.HasPrefix(path.Base(name), "drizzle.config.") })
		// The turso dialect talks to a hosted libSQL database even when
		// development points it at a local file; that file is not where
		// production keeps its data, and a volume would silently stand in for
		// the hosted database. Its variable is never relocated.
		for _, name := range drizzleConfigs {
			content := view.contents[name]
			if dialect := drizzleDialectRE.FindSubmatch(content); dialect != nil && string(dialect[1]) == "turso" {
				if match := configEnvURLRE.FindSubmatch(content); match != nil {
					claimed[firstNonEmpty(string(match[1]), string(match[2]), string(match[3]))] = true
				}
			}
		}
		for _, variable := range persistentFileVariables(variables) {
			if claimed[variable.Name] || strings.HasPrefix(variable.Name, "TURSO_") {
				continue
			}
			claimed[variable.Name] = true
			result = append(result, relocatableSQLite(variable, layout, defaultPrefix, "the source opens its SQLite database from "+variable.Name))
		}
		for _, name := range drizzleConfigs {
			content := view.contents[name]
			if dialect := drizzleDialectRE.FindSubmatch(content); dialect == nil || string(dialect[1]) == "turso" {
				continue
			}
			if match := configEnvURLRE.FindSubmatch(content); match != nil {
				variable := firstNonEmpty(string(match[1]), string(match[2]), string(match[3]))
				detected, _ := variableByName(variables, variable)
				if claimed[variable] || !sqliteFileExample(detected.Example) {
					continue
				}
				claimed[variable] = true
				detected.Name = variable
				if len(detected.Sources) == 0 {
					detected.Sources = []string{joinRoot(view.root, name)}
				}
				result = append(result, relocatableSQLite(detected, layout, defaultPrefix, "Drizzle's SQLite database is read from "+variable))
			} else if match := configLiteralURLRE.FindSubmatch(content); match != nil {
				if entry, ok := literalSQLite(string(match[1]), joinRoot(view.root, name), "Drizzle's SQLite database is the file "+boundedEvidence(string(match[1])), view, layout); ok {
					result = append(result, entry)
				}
			}
		}
		for _, name := range view.sorted(func(name string) bool { return jsSourceFile(name) }) {
			content := view.contents[name]
			if !jsSQLiteImportRE.Match(content) {
				continue
			}
			for _, match := range jsSQLiteLiteralRE.FindAllSubmatch(content, 8) {
				literal := string(match[1])
				if !sqliteFileName(literal) {
					continue
				}
				if entry, ok := literalSQLite(literal, joinRoot(view.root, name), "the source opens the SQLite file "+boundedEvidence(literal), view, layout); ok {
					result = append(result, entry)
				}
			}
		}
	}
	if manifest.has("@strapi/strapi") {
		client, _ := variableByName(variables, "DATABASE_CLIENT")
		_, config := view.file("config/database.ts", "config/database.js")
		if strings.EqualFold(client.Example, "sqlite") || (client.Example == "" && strings.Contains(string(config), "'sqlite'")) ||
			(client.Example == "" && strings.Contains(string(config), `"sqlite"`)) {
			result = append(result, DetectedPersistentPath{
				Kind: PersistentSQLite, Path: layout.containerPath(".tmp/data.db"), Target: layout.writableTarget(view, ".tmp"),
				Source: joinRoot(view.root, "package.json"), Reason: "Strapi keeps its SQLite database in .tmp/data.db",
			})
		}
		result = append(result, DetectedPersistentPath{
			Kind: PersistentUploads, Path: layout.containerPath("public/uploads"), Target: layout.writableTarget(view, "public/uploads"),
			Source: joinRoot(view.root, "package.json"), Reason: "Strapi's local upload provider writes public/uploads",
		})
	}
	if manifest.has("multer") {
		for _, name := range view.sorted(func(name string) bool { return jsSourceFile(name) }) {
			content := view.contents[name]
			if !strings.Contains(string(content), "multer") {
				continue
			}
			for _, match := range multerDestRE.FindAllSubmatch(content, 4) {
				if variable := string(match[2]); variable != "" {
					if _, listed := variableByName(variables, variable); listed && uploadVariableNames[variable] {
						// uploadVariables below plans this one.
						continue
					}
					entry := DetectedPersistentPath{
						Kind: PersistentUploads, Path: layout.containerPath("uploads"), Variable: variable,
						Source: joinRoot(view.root, name), Reason: "multer writes uploads to the directory " + variable + " names",
					}
					if layout.root && layout.dataDir != "" {
						entry.Target = path.Join(layout.dataDir, "uploads")
						entry.Value = entry.Target
					}
					result = append(result, entry)
					continue
				}
				result = append(result, literalUploads(string(match[1]), joinRoot(view.root, name), view, layout)...)
			}
			for _, match := range multerCallbackRE.FindAllSubmatch(content, 4) {
				result = append(result, literalUploads(string(match[1]), joinRoot(view.root, name), view, layout)...)
			}
		}
	}
	if manifest.has("lowdb") {
		for _, name := range view.sorted(func(name string) bool { return jsSourceFile(name) }) {
			for _, match := range lowdbFileRE.FindAllSubmatch(view.contents[name], 4) {
				if entry, ok := lowdbState(string(match[1]), joinRoot(view.root, name), view, layout); ok {
					result = append(result, entry)
				}
			}
		}
	}
	result = append(result, uploadVariables(variables, layout)...)
	return result
}

// lowdbState is the JSON file lowdb keeps its whole database in. Like a
// SQLite file, it can be kept only when it sits in a directory of its own.
func lowdbState(literal, source string, view stateRoot, layout stateLayout) (DetectedPersistentPath, bool) {
	file := strings.TrimPrefix(strings.TrimSpace(literal), "./")
	if file == "" || path.IsAbs(file) || strings.HasPrefix(file, "..") || strings.ContainsAny(file, "$`{}") || !safeRelativePath(file) {
		return DetectedPersistentPath{}, false
	}
	return DetectedPersistentPath{
		Kind: PersistentStorage, Path: layout.containerPath(file), Target: layout.mountableDirectory(view, file),
		Source: source, Reason: "lowdb keeps its database in the JSON file " + boundedEvidence(file),
	}, true
}

func jsSourceFile(name string) bool {
	switch path.Ext(name) {
	case ".js", ".mjs", ".cjs", ".ts", ".mts", ".cts":
		return !strings.HasPrefix(path.Base(name), "drizzle.config.") && !strings.HasPrefix(path.Base(name), "prisma.config.")
	}
	return false
}

func literalUploads(literal, source string, view stateRoot, layout stateLayout) []DetectedPersistentPath {
	dir := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(literal), "./"), "/")
	if dir == "" || path.IsAbs(dir) || strings.HasPrefix(dir, "..") || strings.ContainsAny(dir, "\x00\r\n$`") || !safeRelativePath(dir) {
		return nil
	}
	entry := DetectedPersistentPath{
		Kind: PersistentUploads, Path: layout.containerPath(dir), Source: source,
		Reason: "uploads are written to " + boundedEvidence(dir),
	}
	if strings.Contains(dir, "/") {
		entry.Target = layout.mountableDirectory(view, dir+"/file")
	} else if stateDataDirectoryNames[strings.ToLower(dir)] {
		entry.Target = layout.writableTarget(view, dir)
	}
	return []DetectedPersistentPath{entry}
}

// relocatableSQLite moves a SQLite file the source reads from a variable into
// the image's data directory.
func relocatableSQLite(variable DetectedVariable, layout stateLayout, defaultPrefix, reason string) DetectedPersistentPath {
	entry := DetectedPersistentPath{
		Kind: PersistentSQLite, Variable: variable.Name, Source: firstNonEmpty(firstSource(variable), "."), Reason: reason,
		Path: layout.containerPath("app.db"),
	}
	if _, file, _, ok := sqliteLocation(variable.Example); ok {
		entry.Path = layout.containerPath(strings.TrimPrefix(file, "./"))
	}
	if layout.dataDir != "" {
		entry.Target = layout.dataDir
		entry.Value = relocatedSQLite(variable.Example, layout.dataDir, defaultPrefix)
	}
	return entry
}

// literalSQLite is a SQLite file named in code or configuration. It can be
// kept only when it sits in a directory of its own.
func literalSQLite(literal, source, reason string, view stateRoot, layout stateLayout) (DetectedPersistentPath, bool) {
	_, file, _, ok := sqliteLocation(literal)
	if !ok {
		return DetectedPersistentPath{}, false
	}
	file = strings.TrimPrefix(file, "./")
	if strings.HasPrefix(file, "..") || strings.ContainsAny(file, "\x00\r\n") {
		return DetectedPersistentPath{}, false
	}
	entry := DetectedPersistentPath{Kind: PersistentSQLite, Path: layout.containerPath(file), Source: source, Reason: reason}
	if path.IsAbs(file) {
		if layout.dataDir != "" && strings.HasPrefix(path.Clean(file), layout.dataDir+"/") {
			entry.Target = layout.dataDir
		}
	} else {
		entry.Target = layout.mountableDirectory(view, file)
	}
	return entry, true
}

// prismaSQLitePaths reads a Prisma schema whose datasource is SQLite. Its URL
// comes from the schema, or — Prisma 7 — from prisma.config.
func prismaSQLitePaths(manifest nodeManifest, view stateRoot, variables []DetectedVariable, layout stateLayout) []DetectedPersistentPath {
	if !manifest.has("prisma") && !manifest.has("@prisma/client") {
		return nil
	}
	var result []DetectedPersistentPath
	for _, name := range view.sorted(func(name string) bool {
		return strings.HasSuffix(name, ".prisma") && (!strings.Contains(name, "/") || strings.HasPrefix(name, "prisma/"))
	}) {
		block := prismaDatasourceRE.FindSubmatch(view.contents[name])
		if block == nil {
			continue
		}
		provider, variable, literal := "", "", ""
		for _, field := range prismaFieldRE.FindAllSubmatch(block[1], -1) {
			switch string(field[1]) {
			case "provider":
				provider = string(field[4])
			case "url":
				variable, literal = string(field[3]), string(field[4])
			}
		}
		if provider != "sqlite" {
			continue
		}
		source := joinRoot(view.root, name)
		if variable == "" && literal == "" {
			configName, config := view.file("prisma.config.ts", "prisma.config.mts", "prisma.config.js", "prisma.config.mjs", "prisma.config.cts", "prisma.config.cjs")
			if match := configEnvURLRE.FindSubmatch(config); match != nil {
				variable, source = firstNonEmpty(string(match[1]), string(match[2]), string(match[3])), joinRoot(view.root, configName)
			} else if match := configLiteralURLRE.FindSubmatch(config); match != nil {
				literal, source = string(match[1]), joinRoot(view.root, configName)
			} else {
				variable = "DATABASE_URL"
			}
		}
		if variable != "" {
			detected, _ := variableByName(variables, variable)
			detected.Name = variable
			if !sqliteFileExample(detected.Example) {
				// A driver adapter points the sqlite provider at a hosted
				// libSQL database; its data is not in the container.
				continue
			}
			if len(detected.Sources) == 0 {
				detected.Sources = []string{source}
			}
			entry := relocatableSQLite(detected, layout, "file:", "Prisma's SQLite datasource is read from "+variable)
			entry.Source = source
			// Prisma resolves a relative file: URL against the schema's own
			// directory, which is where the file lands today.
			if _, file, _, ok := sqliteLocation(detected.Example); ok && !path.IsAbs(file) {
				entry.Path = layout.containerPath(path.Join(path.Dir(name), file))
			} else if !ok {
				entry.Path = layout.containerPath(path.Join(path.Dir(name), "dev.db"))
			}
			result = append(result, entry)
			continue
		}
		file := strings.TrimPrefix(strings.TrimPrefix(literal, "file:"), "//")
		if _, _, _, ok := sqliteLocation(literal); !ok {
			continue
		}
		relative := file
		if !path.IsAbs(file) {
			relative = path.Join(path.Dir(name), file)
		}
		entry, ok := literalSQLite(relative, source, "Prisma's SQLite datasource is the file "+boundedEvidence(literal), view, layout)
		if ok {
			result = append(result, entry)
		}
	}
	return result
}

// Python ------------------------------------------------------------------

var (
	djangoSQLiteRE     = regexp.MustCompile(`django\.db\.backends\.sqlite3`)
	djangoNameRE       = regexp.MustCompile(`["']NAME["']\s*:\s*([^\n}]+)`)
	pythonPathPartRE   = regexp.MustCompile(`["']([^"']+)["']`)
	djangoEnvDBRE      = regexp.MustCompile(`dj_database_url\.(?:config|parse)\(|\benv\.db(?:_url)?\(|["']DATABASE_URL["']`)
	djangoMediaRootRE  = regexp.MustCompile(`(?m)^\s*MEDIA_ROOT\s*=\s*(.+)$`)
	sqlalchemySQLiteRE = regexp.MustCompile(`["'](sqlite(?:\+\w+)?:///[^"']*)["']`)
	pythonEnvReadRE    = regexp.MustCompile(`os\.(?:environ\.get|getenv)\(\s*["']([A-Z][A-Z0-9_]*)["']|os\.environ\[\s*["']([A-Z][A-Z0-9_]*)["']\s*\]|\benv(?:\.\w+)?\(\s*["']([A-Z][A-Z0-9_]*)["']`)
	pydanticFieldRE    = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_]*)\s*:\s*[A-Za-z_][A-Za-z0-9_.\[\] |]*=\s*$`)
	flaskSQLAlchemy2RE = regexp.MustCompile(`(?i)flask[-_.]sqlalchemy\s*(?:==|~=|<|<=)\s*2\.|name\s*=\s*"flask-sqlalchemy"\s*\nversion\s*=\s*"2\.`)
)

// pythonPathExpression reads the literal path segments of an expression such
// as BASE_DIR / "data" / "db.sqlite3" or os.path.join(BASE_DIR, "media"),
// relative to the project root the settings name as BASE_DIR.
func pythonPathExpression(expression string) (string, bool) {
	var segments []string
	for _, part := range pythonPathPartRE.FindAllStringSubmatch(expression, 6) {
		segments = append(segments, part[1])
	}
	if len(segments) == 0 {
		return "", false
	}
	joined := strings.TrimPrefix(path.Join(segments...), "./")
	if path.IsAbs(joined) || strings.HasPrefix(joined, "..") || !safeRelativePath(joined) {
		return "", false
	}
	return joined, true
}

// pythonEnvRead is the variable an expression reads, if it reads one.
func pythonEnvRead(expression string) string {
	if match := pythonEnvReadRE.FindStringSubmatch(expression); match != nil {
		return firstNonEmpty(match[1], match[2], match[3])
	}
	return ""
}

func pythonStatePaths(candidate *DetectedCandidate, marker *detectedMarkers, view stateRoot, variables []DetectedVariable, layout stateLayout) []DetectedPersistentPath {
	if !marker.hasPythonManifest() || (candidate.Recipe != "python" && candidate.BuildMethod != BuildDockerfile) {
		return nil
	}
	deps := readPythonDependencies(marker.pythonFiles)
	var result []DetectedPersistentPath
	for _, name := range view.sorted(func(name string) bool { return strings.HasSuffix(name, ".py") }) {
		content := string(view.contents[name])
		source := joinRoot(view.root, name)
		if location := djangoSQLiteRE.FindStringIndex(content); location != nil {
			result = append(result, djangoSQLitePath(content, location, source, view, layout))
		}
		if match := djangoMediaRootRE.FindStringSubmatch(content); match != nil && strings.Contains(content, "STATIC") {
			if variable := pythonEnvRead(match[1]); variable != "" {
				entry := DetectedPersistentPath{
					Kind: PersistentUploads, Path: layout.containerPath("media"), Variable: variable, Source: source,
					Reason: "Django writes uploaded media to MEDIA_ROOT, read from " + variable,
				}
				if layout.root && layout.dataDir != "" {
					entry.Target = path.Join(layout.dataDir, "media")
					entry.Value = entry.Target
				}
				result = append(result, entry)
			} else if dir, ok := pythonPathExpression(match[1]); ok {
				result = append(result, DetectedPersistentPath{
					Kind: PersistentUploads, Path: layout.containerPath(dir), Target: layout.mountableDirectory(view, dir+"/file"),
					Source: source, Reason: "Django writes uploaded media to " + boundedEvidence(dir),
				})
			}
		}
		for _, match := range sqlalchemySQLiteRE.FindAllStringSubmatchIndex(content, 4) {
			if entry, ok := sqlalchemySQLitePath(content, match, source, deps, marker, view, layout); ok {
				result = append(result, entry)
			}
		}
	}
	return append(result, uploadVariables(variables, layout)...)
}

// djangoSQLitePath reads the NAME of the database whose ENGINE is SQLite. The
// NAME is looked for inside the same dictionary, never in a password
// validator's entry further down the settings.
func djangoSQLitePath(content string, location []int, source string, view stateRoot, layout stateLayout) DetectedPersistentPath {
	entry := DetectedPersistentPath{Kind: PersistentSQLite, Source: source, Reason: "Django's database is a SQLite file"}
	open := strings.LastIndex(content[:location[0]], "{")
	closing := strings.Index(content[location[1]:], "}")
	scope := content[max(open, 0):]
	if closing >= 0 {
		scope = content[max(open, 0) : location[1]+closing]
	}
	file := "db.sqlite3"
	if match := djangoNameRE.FindStringSubmatch(scope); match != nil {
		if variable := pythonEnvRead(match[1]); variable != "" {
			// The file's location is itself a variable.
			entry.Path, entry.Variable = layout.containerPath(file), variable
			entry.Reason = "Django's SQLite database file is named by " + variable
			if layout.dataDir != "" {
				entry.Target = layout.dataDir
				entry.Value = path.Join(layout.dataDir, file)
			}
			return entry
		}
		if parsed, ok := pythonPathExpression(match[1]); ok {
			file = parsed
		}
	}
	entry.Path = layout.containerPath(file)
	entry.Target = layout.mountableDirectory(view, file)
	if djangoEnvDBRE.MatchString(content) {
		// The settings read DATABASE_URL, so a linked server database replaces
		// the file; the file is only what runs without one.
		entry.DatabaseVariable = "DATABASE_URL"
		entry.Reason = "Django's database comes from DATABASE_URL and falls back to a SQLite file"
	}
	return entry
}

// sqlalchemySQLitePath reads one SQLAlchemy SQLite URL: a literal, a literal
// joined to the project directory, or the fallback of a variable read.
func sqlalchemySQLitePath(content string, match []int, source string, deps pythonDependencies, marker *detectedMarkers, view stateRoot, layout stateLayout) (DetectedPersistentPath, bool) {
	literal := content[match[2]:match[3]]
	lineStart := strings.LastIndex(content[:match[0]], "\n") + 1
	lineEnd := strings.Index(content[match[1]:], "\n")
	if lineEnd < 0 {
		lineEnd = len(content) - match[1]
	}
	before, after := content[lineStart:match[0]], content[match[1]:match[1]+lineEnd]
	file := literal[strings.Index(literal, ":///")+4:]
	joined := false
	if file == "" || file == "/" {
		// "sqlite:///" + os.path.join(basedir, "app.db"): the file is joined to
		// the directory of the module, which is the project root in practice.
		parsed, ok := pythonPathExpression(after)
		if !ok {
			return DetectedPersistentPath{}, false
		}
		file, joined = parsed, true
	}
	if index := strings.LastIndex(file, "}/"); index >= 0 {
		// f"sqlite:///{BASE_DIR}/app.db"
		file, joined = file[index+2:], true
	}
	file = strings.TrimPrefix(file, "./")
	if file == "" || strings.Contains(file, ":memory:") || strings.ContainsAny(file, "{}%") {
		return DetectedPersistentPath{}, false
	}
	switch {
	case strings.HasPrefix(file, "/"):
		// Four slashes: an absolute path.
		file = path.Clean(file)
	case !joined && deps.has("flask-sqlalchemy") && !flaskSQLAlchemy2(marker.pythonFiles):
		// Flask-SQLAlchemy 3 resolves a relative SQLite path against the
		// application's instance folder.
		file = path.Join("instance", file)
	}
	entry, ok := literalSQLite(file, source, "a SQLAlchemy URL opens the SQLite file "+boundedEvidence(file), view, layout)
	if !ok {
		return DetectedPersistentPath{}, false
	}
	variable := pythonEnvRead(before)
	if field := pydanticFieldRE.FindStringSubmatch(before); variable == "" && field != nil && strings.Contains(content, "BaseSettings") {
		// A pydantic settings field reads the variable of its own name.
		variable = strings.ToUpper(field[1])
	}
	if variable != "" && ValidateEnvKey(variable) == nil {
		// DATABASE_URL = os.environ.get("DATABASE_URL", "sqlite:///app.db"):
		// the literal is only the fallback.
		entry.DatabaseVariable = variable
		entry.Reason = "the database comes from " + variable + " and falls back to the SQLite file " + boundedEvidence(file)
	}
	return entry, true
}

func flaskSQLAlchemy2(files map[string][]byte) bool {
	for _, content := range files {
		if flaskSQLAlchemy2RE.Match(content) {
			return true
		}
	}
	return false
}

// pythonDatabaseSuggestions offers a server database on the variable a
// SQLite default is read from, so linking one takes the file out of use.
func pythonDatabaseSuggestions(candidate *DetectedCandidate) {
	for _, entry := range candidate.PersistentPaths {
		if entry.Kind != PersistentSQLite || entry.DatabaseVariable == "" || len(candidate.Databases) >= 8 {
			continue
		}
		covered := false
		for _, database := range candidate.Databases {
			covered = covered || database.Variable == entry.DatabaseVariable
		}
		if !covered && candidate.Recipe == "python" {
			// The slice is shared by the root's candidates; this one gets its own.
			candidate.Databases = append(append([]DetectedDatabase(nil), candidate.Databases...), DetectedDatabase{
				Engine: "postgres", Variable: entry.DatabaseVariable,
				Evidence: entry.DatabaseVariable + " replaces the SQLite default in " + entry.Source,
			})
		}
	}
}

// PHP ---------------------------------------------------------------------

var laravelMajorRE = regexp.MustCompile(`(\d+)`)

func laravelStatePaths(candidate *DetectedCandidate, marker *detectedMarkers, view stateRoot, variables []DetectedVariable, layout stateLayout) []DetectedPersistentPath {
	manifest, ok := parseComposerManifest(marker.composerJSON)
	if !ok || !manifest.has("laravel/framework") || (candidate.Recipe != "php" && candidate.BuildMethod != BuildDockerfile) {
		return nil
	}
	var result []DetectedPersistentPath
	composer := joinRoot(view.root, "composer.json")
	storage := layout.containerPath("storage")
	connection, documented := variableByName(variables, "DB_CONNECTION")
	sqlite := strings.EqualFold(connection.Example, "sqlite")
	if !documented || connection.Example == "" {
		// Laravel 11 made SQLite the connection nobody configured.
		if match := laravelMajorRE.FindString(manifest.Require["laravel/framework"]); match != "" {
			major, _ := strconv.Atoi(match)
			sqlite = major >= 11
		}
	}
	if sqlite {
		entry := DetectedPersistentPath{
			Kind: PersistentSQLite, Path: layout.containerPath("database/database.sqlite"), DatabaseVariable: "DB_URL",
			ConnectionVariable: "DB_CONNECTION", Source: firstNonEmpty(firstSource(connection), composer),
			Reason: "Laravel's database connection is SQLite in database/database.sqlite, which also holds sessions, cache and queued jobs",
		}
		// config/database.php, when the application keeps its own copy, has to
		// read the location from DB_DATABASE for the file to move.
		_, config := view.file("config/database.php")
		if config == nil || strings.Contains(string(config), "'DB_DATABASE'") || strings.Contains(string(config), `"DB_DATABASE"`) {
			entry.Target, entry.Variable = storage, "DB_DATABASE"
			entry.Value = path.Join(storage, "database.sqlite")
		}
		result = append(result, entry)
	}
	disk, _ := variableByName(variables, "FILESYSTEM_DISK")
	if manifest.has("filament/filament") || manifest.has("spatie/laravel-medialibrary") || strings.EqualFold(disk.Example, "public") {
		result = append(result, DetectedPersistentPath{
			Kind: PersistentUploads, Path: layout.containerPath("storage/app"), Target: storage, Source: composer,
			Reason: "uploaded files are stored on Laravel's local disks under storage/app",
		})
	}
	if manifest.has("statamic/cms") {
		for _, dir := range []string{"content", "users"} {
			result = append(result, DetectedPersistentPath{
				Kind: PersistentStorage, Path: layout.containerPath(dir), Source: composer,
				Reason: "Statamic writes Control Panel edits to " + dir + "/ as flat files",
			})
		}
	}
	return result
}

// Rails -------------------------------------------------------------------

var (
	yamlTopKeyRE      = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_-]*):`)
	yamlAnchorRE      = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_-]*):\s*&(\w+)`)
	yamlMergeRE       = regexp.MustCompile(`<<:\s*\*(\w+)`)
	railsAdapterRE    = regexp.MustCompile(`(?m)^\s+adapter:\s*["']?(\w+)`)
	railsDatabaseRE   = regexp.MustCompile(`(?m)^\s+database:\s*["']?([^"'\s#]+)`)
	railsLocalRE      = regexp.MustCompile(`(?m)^\s*config\.active_storage\.service\s*=\s*:local\b`)
	kamalVolumeLineRE = regexp.MustCompile(`^\s*-\s*["']?[^"'\s:]+:(/[^"'\s:]*)(?::[a-z]+)?["']?\s*$`)
)

// yamlBlocks splits a YAML document into its top-level keys' text. It reads
// structure by indentation only, which is all database.yml's ERB allows.
func yamlBlocks(content string) (map[string]string, map[string]string) {
	blocks, anchors := map[string]string{}, map[string]string{}
	current := ""
	for _, line := range strings.Split(content, "\n") {
		if match := yamlTopKeyRE.FindStringSubmatch(line); match != nil {
			current = match[1]
			if anchor := yamlAnchorRE.FindStringSubmatch(line); anchor != nil {
				anchors[anchor[2]] = current
			}
			continue
		}
		if current != "" && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") || strings.TrimSpace(line) == "") {
			blocks[current] += line + "\n"
		}
	}
	return blocks, anchors
}

func railsStatePaths(candidate *DetectedCandidate, marker *detectedMarkers, view stateRoot, layout stateLayout) []DetectedPersistentPath {
	if candidate.BuildMethod != BuildDockerfile && candidate.Recipe != "ruby" {
		return nil
	}
	_, gemfile := view.file("Gemfile.lock", "Gemfile")
	if !strings.Contains(string(gemfile), "rails") {
		return nil
	}
	var result []DetectedPersistentPath
	storage := layout.containerPath("storage")
	if name, content := view.file("config/database.yml"); content != nil {
		blocks, anchors := yamlBlocks(string(content))
		production := blocks["production"]
		adapter := ""
		if match := railsAdapterRE.FindStringSubmatch(production); match != nil {
			adapter = match[1]
		}
		for _, merge := range yamlMergeRE.FindAllStringSubmatch(production, -1) {
			if adapter != "" {
				break
			}
			if match := railsAdapterRE.FindStringSubmatch(blocks[anchors[merge[1]]]); match != nil {
				adapter = match[1]
			}
		}
		if adapter == "sqlite3" {
			for _, match := range railsDatabaseRE.FindAllStringSubmatch(production, 8) {
				file := strings.TrimPrefix(match[1], "./")
				if strings.Contains(file, "<%") || path.IsAbs(file) || strings.HasPrefix(file, "..") || !safeRelativePath(file) {
					continue
				}
				entry := DetectedPersistentPath{
					Kind: PersistentSQLite, Path: layout.containerPath(file), DatabaseVariable: "DATABASE_URL",
					Source: joinRoot(view.root, name), Reason: "Rails keeps its production SQLite database in " + boundedEvidence(file),
				}
				if strings.HasPrefix(file, "storage/") {
					entry.Target = storage
				}
				result = append(result, entry)
			}
		}
	}
	if name, content := view.file("config/storage.yml"); content != nil && strings.Contains(string(content), "service: Disk") {
		_, production := view.file("config/environments/production.rb")
		if railsLocalRE.Match(production) {
			result = append(result, DetectedPersistentPath{
				Kind: PersistentUploads, Path: storage, Target: storage, Source: joinRoot(view.root, name),
				Reason: "Active Storage keeps uploaded files on the local disk in storage/",
			})
		}
	}
	if name, content := view.file("config/deploy.yml"); content != nil {
		inVolumes := false
		for _, line := range strings.Split(string(content), "\n") {
			if yamlTopKeyRE.MatchString(line) {
				inVolumes = strings.HasPrefix(line, "volumes:")
				continue
			}
			if !inVolumes {
				continue
			}
			if match := kamalVolumeLineRE.FindStringSubmatch(line); match != nil {
				target := path.Clean(match[1])
				if target == "/" {
					continue
				}
				result = append(result, DetectedPersistentPath{
					Kind: PersistentStorage, Path: target, Target: target, Source: joinRoot(view.root, name),
					Reason: "Kamal keeps a volume at " + target,
				})
			}
		}
	}
	return result
}

// Go and Rust -------------------------------------------------------------

var (
	goSQLiteModules = []string{
		"modernc.org/sqlite", "github.com/mattn/go-sqlite3", "github.com/glebarez/sqlite", "github.com/glebarez/go-sqlite",
		"github.com/ncruces/go-sqlite3", "gorm.io/driver/sqlite", "zombiezen.com/go/sqlite", "crawshaw.io/sqlite",
		"github.com/tursodatabase/go-libsql",
	}
	rustSQLiteCrateRE = regexp.MustCompile(`(?m)^\s*(?:rusqlite|libsql|sqlite)\s*=|["']sqlite["']|["']sqlx-sqlite["']|["']runtime-[a-z-]+-sqlite["']`)
	goSQLiteOpenRE    = regexp.MustCompile(`(?:sql|sqlx)\.(?:Open|Connect|MustConnect)\(\s*"(?:sqlite3?|libsql)"\s*,\s*"([^"]+)"|sqlite\.Open\(\s*"([^"]+)"`)
	rustSQLiteOpenRE  = regexp.MustCompile(`Connection::open\(\s*"([^"]+)"|(?:connect|from_str|filename|new)\(\s*"((?:sqlite:)[^"]+)"`)
)

func compiledStatePaths(candidate *DetectedCandidate, marker *detectedMarkers, view stateRoot, variables []DetectedVariable, layout stateLayout) []DetectedPersistentPath {
	goModule := len(marker.goModContent) > 0 && (candidate.Recipe == "go" || candidate.BuildMethod == BuildDockerfile)
	rustCrate := len(marker.cargoToml) > 0 && (candidate.Recipe == "rust" || candidate.BuildMethod == BuildDockerfile)
	if !goModule && !rustCrate {
		return nil
	}
	sqlite := false
	var result []DetectedPersistentPath
	if goModule {
		module := string(marker.goModContent)
		for _, name := range goSQLiteModules {
			sqlite = sqlite || strings.Contains(module, name)
		}
		if strings.Contains(module, "github.com/pocketbase/pocketbase") {
			result = append(result, pocketBaseState(candidate, view, layout))
		}
	}
	if rustCrate && rustSQLiteCrateRE.Match(marker.cargoToml) {
		sqlite = true
	}
	if !sqlite {
		return result
	}
	for _, variable := range persistentFileVariables(variables) {
		result = append(result, relocatableSQLite(variable, layout, "", "the service opens its SQLite database from "+variable.Name))
	}
	for _, name := range view.sorted(func(name string) bool {
		return (goModule && strings.HasSuffix(name, ".go")) || (rustCrate && strings.HasSuffix(name, ".rs"))
	}) {
		content := view.contents[name]
		expression := goSQLiteOpenRE
		if strings.HasSuffix(name, ".rs") {
			expression = rustSQLiteOpenRE
		}
		for _, match := range expression.FindAllSubmatch(content, 4) {
			literal := firstNonEmpty(string(match[1]), string(match[2]))
			if entry, ok := literalSQLite(literal, joinRoot(view.root, name), "the service opens the SQLite file "+boundedEvidence(literal), view, layout); ok {
				result = append(result, entry)
			}
		}
	}
	return result
}

// pocketBaseState is where a PocketBase application built from Go keeps its
// SQLite databases and uploaded files. Without --dir it is pb_data beside the
// executable — /pb_data for the recipe's /app, which the unprivileged user
// cannot create — and with no serve command the binary only prints its help.
// A recipe candidate with no start command of its own is given one that
// serves on the planned port from the data directory the image prepares.
func pocketBaseState(candidate *DetectedCandidate, view stateRoot, layout stateLayout) DetectedPersistentPath {
	entry := DetectedPersistentPath{
		Kind: PersistentSQLite, Path: "/pb_data", Source: joinRoot(view.root, "go.mod"),
		Reason: "PocketBase keeps pb_data beside its executable unless serve is given --dir",
	}
	if candidate.BuildMethod != BuildRecipe || candidate.Recipe != "go" || candidate.StartCommand != "" || layout.dataDir == "" {
		return entry
	}
	candidate.StartCommand = "/app serve --http=0.0.0.0:${PORT:-8090} --dir=" + layout.dataDir
	if candidate.Port == 0 {
		candidate.Port = 8090
	}
	candidate.Evidence = append(candidate.Evidence, DetectionEvidence{
		Path: entry.Source, Reason: "PocketBase application: the start command serves on all interfaces with its data in " + layout.dataDir,
	})
	entry.Path, entry.Target = layout.dataDir, layout.dataDir
	entry.Reason = "PocketBase keeps its databases and uploaded files in the directory serve --dir names"
	return entry
}

// .NET --------------------------------------------------------------------

var (
	dotnetSQLitePackageRE = regexp.MustCompile(`Include="(?:Microsoft\.EntityFrameworkCore\.Sqlite|Microsoft\.Data\.Sqlite)(?:\.Core)?"`)
	dotnetConnectionRE    = regexp.MustCompile(`"([A-Za-z0-9_]+)"\s*:\s*"\s*((?:Data\s?Source|Filename)\s*=\s*([^";]+))((?:;[^"]*)?)"`)
	dotnetConnectionOptRE = regexp.MustCompile(`(?i)^\s*(Cache|Mode|Foreign Keys|Pooling|Default Timeout)\s*=\s*([A-Za-z0-9]+)\s*$`)
	dotnetCookieAuthRE    = regexp.MustCompile(`\.(?:AddIdentity|AddDefaultIdentity|AddIdentityCore|AddIdentityApiEndpoints|AddCookie|AddRazorPages|AddControllersWithViews|AddAntiforgery|AddServerSideBlazor|AddRazorComponents)\b`)
	dotnetPersistKeysRE   = regexp.MustCompile(`\.PersistKeysTo\w+`)
)

func dotnetStatePaths(candidate *DetectedCandidate, marker *detectedMarkers, view stateRoot, layout stateLayout) []DetectedPersistentPath {
	if (candidate.Recipe != "dotnet" && candidate.BuildMethod != BuildDockerfile) || len(marker.csprojs) == 0 {
		return nil
	}
	var result []DetectedPersistentPath
	projects := ""
	for _, content := range marker.csprojs {
		projects += string(content)
	}
	web := candidate.Framework == "aspnet" || (candidate.BuildMethod == BuildDockerfile && strings.Contains(projects, "Microsoft.NET.Sdk.Web"))
	if dotnetSQLitePackageRE.MatchString(projects) {
		for _, name := range []string{"appsettings.Production.json", "appsettings.json"} {
			content, ok := view.contents[name]
			if !ok {
				continue
			}
			for _, match := range dotnetConnectionRE.FindAllStringSubmatch(string(content), 4) {
				file := strings.TrimPrefix(strings.TrimSpace(match[3]), "./")
				// A SQL Server "Data Source=(localdb)..." names a server, not a file.
				if !sqliteFileName(file) || strings.Contains(file, ":memory:") || path.IsAbs(file) || strings.HasPrefix(file, "..") {
					continue
				}
				entry := DetectedPersistentPath{
					Kind: PersistentSQLite, Path: layout.containerPath(file), Source: joinRoot(view.root, name),
					Reason: "the " + match[1] + " connection string opens the SQLite file " + boundedEvidence(file),
				}
				if layout.dataDir != "" && safePersistentName(path.Base(file)) {
					// Configuration reads ConnectionStrings__<name> from the
					// environment ahead of appsettings.json, so the file can
					// move without touching the source.
					entry.Target, entry.Variable = layout.dataDir, "ConnectionStrings__"+match[1]
					entry.Value = "Data Source=" + path.Join(layout.dataDir, path.Base(file))
					if candidate.BuildMethod == BuildRecipe && view.present[file] {
						// The recipe copies it into the data directory
						// (dotnetSQLiteSeeds).
						entry.Reason += "; the committed copy seeds the volume the first time it is mounted"
					}
					for _, option := range strings.Split(strings.TrimPrefix(match[4], ";"), ";") {
						if kept := dotnetConnectionOptRE.FindStringSubmatch(option); kept != nil {
							entry.Value += ";" + kept[1] + "=" + kept[2]
						}
					}
				}
				result = append(result, entry)
			}
			break
		}
	}
	if web {
		cookies, persisted, source := false, false, ""
		for _, name := range view.sorted(func(name string) bool { return strings.HasSuffix(name, ".cs") }) {
			content := view.contents[name]
			if dotnetCookieAuthRE.Match(content) && !cookies {
				cookies, source = true, joinRoot(view.root, name)
			}
			persisted = persisted || dotnetPersistKeysRE.Match(content)
		}
		if cookies && !persisted {
			// The recipe prepares the app user's key ring directory. An image
			// built by its own Dockerfile keeps it in the running user's home,
			// which only root can be given as a volume it does not have.
			ring, target := dotnetDataProtectionAt, dotnetDataProtectionAt
			if candidate.BuildMethod == BuildDockerfile {
				ring, target = dotnetDataProtectionAt, ""
				if layout.root {
					ring = "/root/.aspnet/DataProtection-Keys"
					target = ring
				}
			}
			result = append(result, DetectedPersistentPath{
				Kind: PersistentKeys, Path: ring, Target: target, Source: source,
				Reason: "ASP.NET Core signs cookies and antiforgery tokens with a Data Protection key ring kept in the container",
			})
		}
	}
	return result
}

// Elixir ------------------------------------------------------------------

var (
	mixAppRE          = regexp.MustCompile(`\bapp:\s*:([a-z][a-z0-9_]{0,63})\b`)
	elixirEnvPathRE   = regexp.MustCompile(`System\.(?:get_env|fetch_env!)\(\s*"([A-Z][A-Z0-9_]*)"`)
	elixirSQLiteDepRE = regexp.MustCompile(`\{\s*:ecto_sqlite3\b`)
)

// elixirStatePaths reads a Phoenix release built by its own Dockerfile
// (there is no Elixir recipe) whose Ecto repository is SQLite: the file the
// release's runtime.exs reads its location from, DATABASE_PATH in the
// generator's own config.
func elixirStatePaths(candidate *DetectedCandidate, view stateRoot, layout stateLayout) []DetectedPersistentPath {
	_, mix := view.file("mix.exs")
	if candidate.BuildMethod != BuildDockerfile || !elixirSQLiteDepRE.Match(mix) {
		return nil
	}
	source, runtime := view.file("config/runtime.exs")
	variable := ""
	for _, match := range elixirEnvPathRE.FindAllSubmatch(runtime, 16) {
		name := string(match[1])
		if (strings.Contains(name, "DATABASE") || strings.Contains(name, "DB")) && !strings.Contains(name, "URL") {
			variable = name
			break
		}
	}
	base := "app.db"
	if match := mixAppRE.FindSubmatch(mix); match != nil {
		base = string(match[1]) + ".db"
	}
	entry := DetectedPersistentPath{
		Kind: PersistentSQLite, Path: layout.containerPath(base), Source: joinRoot(view.root, firstNonEmpty(source, "mix.exs")),
		Reason: "the Ecto repository is a SQLite file (ecto_sqlite3)",
	}
	if variable != "" {
		entry.Variable = variable
		entry.Reason = "the Ecto repository is the SQLite file " + variable + " names (ecto_sqlite3)"
		if layout.dataDir != "" {
			entry.Target, entry.Value = layout.dataDir, path.Join(layout.dataDir, base)
		}
	}
	return []DetectedPersistentPath{entry}
}

// Dockerfile --------------------------------------------------------------

// dockerfileInstructions joins continuation lines and drops comments, so a
// VOLUME or WORKDIR split across lines reads as one instruction.
func dockerfileInstructions(content []byte) [][]string {
	var result [][]string
	pending := ""
	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if pending == "" && (trimmed == "" || strings.HasPrefix(trimmed, "#")) {
			continue
		}
		if strings.HasSuffix(trimmed, "\\") {
			pending += strings.TrimSuffix(trimmed, "\\") + " "
			continue
		}
		pending += trimmed
		if fields := strings.Fields(pending); len(fields) > 0 {
			result = append(result, fields)
		}
		pending = ""
	}
	if fields := strings.Fields(pending); len(fields) > 0 {
		result = append(result, fields)
	}
	return result
}

// dockerfileFinalWorkdirUser reads the final stage's working directory and user. A
// stage built FROM an earlier one inherits both, as Rails' generated
// Dockerfile relies on.
func dockerfileFinalWorkdirUser(content []byte) (string, string) {
	type stage struct{ workdir, user string }
	stages := map[string]stage{}
	current := stage{workdir: "/"}
	name := ""
	for _, instruction := range dockerfileInstructions(content) {
		switch strings.ToUpper(instruction[0]) {
		case "FROM":
			if name != "" {
				stages[name] = current
			}
			current, name = stage{workdir: "/"}, ""
			arguments := []string{}
			for _, argument := range instruction[1:] {
				if !strings.HasPrefix(argument, "--") {
					arguments = append(arguments, argument)
				}
			}
			if len(arguments) > 0 {
				if parent, ok := stages[strings.ToLower(arguments[0])]; ok {
					current = parent
				}
			}
			if len(arguments) >= 3 && strings.EqualFold(arguments[1], "AS") {
				name = strings.ToLower(arguments[2])
			}
		case "WORKDIR":
			// Phoenix's generated Dockerfile writes WORKDIR "/app".
			if dir := strings.Trim(strings.Join(instruction[1:], " "), `"'`); dir != "" && !strings.Contains(dir, "$") {
				if path.IsAbs(dir) {
					current.workdir = path.Clean(dir)
				} else {
					current.workdir = path.Join(current.workdir, dir)
				}
			}
		case "USER":
			if len(instruction) > 1 {
				current.user = strings.Trim(instruction[1], `"'`)
			}
		}
	}
	return current.workdir, current.user
}

// detectedDockerfileVolumes are the literal absolute paths the final stage
// declares with VOLUME. Docker gives each a fresh anonymous volume per
// container, so without a planned mount their data resets every release.
func detectedDockerfileVolumes(content []byte) []string {
	stages := map[string][]string{}
	var volumes []string
	name := ""
	for _, instruction := range dockerfileInstructions(content) {
		switch strings.ToUpper(instruction[0]) {
		case "FROM":
			if name != "" {
				stages[name] = volumes
			}
			volumes, name = nil, ""
			arguments := []string{}
			for _, argument := range instruction[1:] {
				if !strings.HasPrefix(argument, "--") {
					arguments = append(arguments, argument)
				}
			}
			if len(arguments) > 0 {
				// A stage built FROM an earlier one inherits its volumes.
				volumes = append(volumes, stages[strings.ToLower(arguments[0])]...)
			}
			if len(arguments) >= 3 && strings.EqualFold(arguments[1], "AS") {
				name = strings.ToLower(arguments[2])
			}
		case "VOLUME":
			arguments := strings.Join(instruction[1:], " ")
			var paths []string
			if strings.HasPrefix(arguments, "[") {
				if json.Unmarshal([]byte(arguments), &paths) != nil {
					continue
				}
			} else {
				paths = instruction[1:]
			}
			for _, value := range paths {
				value = strings.TrimSpace(value)
				if !path.IsAbs(value) || strings.ContainsAny(value, "$\x00\r\n") || path.Clean(value) == "/" || scratchVolumePath(value) {
					continue
				}
				volumes = append(volumes, path.Clean(value))
			}
		}
	}
	sort.Strings(volumes)
	return uniqueSorted(volumes)
}

// scratchVolumeRoots are where a process keeps what it can lose: temporary
// files, sockets and pid files, caches. An image declares VOLUME /tmp (as the
// Spring Boot guide's Dockerfile does) for speed, not to keep data, and a
// managed volume there would keep scratch files across releases, force
// stop-first releases and ask for a backup of nothing.
var scratchVolumeRoots = []string{"/tmp", "/var/tmp", "/run", "/var/run", "/var/cache", "/dev/shm"}

func scratchVolumePath(value string) bool {
	value = path.Clean(value)
	for _, root := range scratchVolumeRoots {
		if value == root || strings.HasPrefix(value, root+"/") {
			return true
		}
	}
	return false
}

// imagePersistentPaths turns the volumes an image's configuration declares
// into planned state, the way a Dockerfile's VOLUME lines are.
func imagePersistentPaths(reference string, volumes []string) []DetectedPersistentPath {
	var result []DetectedPersistentPath
	for _, volume := range volumes {
		volume = strings.TrimSpace(volume)
		if !path.IsAbs(volume) || path.Clean(volume) == "/" || strings.ContainsAny(volume, "\x00\r\n") || scratchVolumePath(volume) {
			continue
		}
		volume = path.Clean(volume)
		result = append(result, DetectedPersistentPath{
			Kind: PersistentVolume, Path: volume, Target: volume, Source: reference,
			Reason: "the image declares VOLUME " + volume,
		})
	}
	return mergePersistentPaths(result)
}

// imageDeclaredVolumes are the paths a locally present image declares with
// VOLUME. An image this host has not pulled declares nothing yet; the
// registry manifest does not carry its configuration.
func (a *HostSourceAnalyzer) imageDeclaredVolumes(ctx context.Context, reference string) []string {
	inspectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	detail, err := a.docker.InspectImage(inspectCtx, reference)
	if err != nil || detail == nil {
		return nil
	}
	return detail.VolumePaths
}
