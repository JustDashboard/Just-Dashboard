package deploy

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// compiledRuntimeHome is the unprivileged user's home in the Go and Rust
// runtime images. It is the working directory, so a relative path the
// service opens — templates/, ./app.db — resolves somewhere the service can
// read what the build shipped and write what it creates, instead of against
// a filesystem root owned by root. The binary stays at /app, where saved
// start commands already name it.
const compiledRuntimeHome = "/home/app"

// The .NET runtime's directories its app user owns: where data is kept, and
// where ASP.NET Core keeps the Data Protection key ring for a non-root user.
const (
	dotnetRuntimeDataDir   = "/app/data"
	dotnetDataProtectionAt = "/home/app/.aspnet/DataProtection-Keys"
)

// compiledRuntimeAssetNames are the root-level files and directories a
// compiled service conventionally reads at runtime rather than embedding:
// templates, static files, migrations, translations and configuration.
var compiledRuntimeAssetNames = []string{
	"templates", "views", "static", "public", "assets", "migrations", "locales", "i18n", "config",
}

// compiledRuntimeWalkEntries bounds the source scan the way detection's
// MaxFiles bounds its walk.
const compiledRuntimeWalkEntries = 20000

var compiledConfigFileRE = regexp.MustCompile(`^config[A-Za-z0-9._-]*\.(?:ya?ml|toml|json)$`)

// Literal paths a Go or Rust service hands to a template loader, a file
// server or a migration source. Group 1 is the path.
var (
	goRuntimeFileREs = []*regexp.Regexp{
		regexp.MustCompile(`(?:LoadHTMLGlob|LoadHTMLFiles|ParseGlob|ParseFiles|http\.Dir|os\.DirFS|NewFileSystem|html\.New)\(\s*"([^"]+)"`),
		regexp.MustCompile(`\.(?:Static|StaticFile|StaticFS|File)\(\s*"[^"]*"\s*,\s*"([^"]+)"`),
		regexp.MustCompile(`"file://([^"]+)"`),
	}
	rustRuntimeFileREs = []*regexp.Regexp{
		regexp.MustCompile(`(?:ServeDir::new|ServeFile::new|Tera::new|NamedFile::open(?:_async)?|FileServer::from|path_loader)\(\s*"([^"]+)"`),
		regexp.MustCompile(`Files::new\(\s*"[^"]*"\s*,\s*"([^"]+)"`),
		regexp.MustCompile(`"file://([^"]+)"`),
	}
	runtimeAssetNameRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$`)
)

// compiledRuntimeAssets lists the root-level entries the runtime stage copies
// beside the binary: the conventional names that exist, and the first
// segment of every literal path the sources pass to a loader. Sources are
// read as text under a fixed budget; nothing is executed. Symlinks are never
// copied, so the image cannot pick up a file from outside the checkout.
func compiledRuntimeAssets(root, extension string) []string {
	found := map[string]bool{}
	ignored := dockerIgnoredPaths(root)
	add := func(name string) {
		name = strings.TrimSuffix(strings.TrimPrefix(name, "./"), "/")
		if first, _, _ := strings.Cut(name, "/"); first != "" {
			name = first
		}
		if !runtimeAssetNameRE.MatchString(name) || strings.ContainsAny(name, "*?[") || name == ".git" ||
			name == "target" || name == "vendor" || name == ".just-dashboard" || name == "node_modules" || ignored(name) {
			return
		}
		info, err := os.Lstat(filepath.Join(root, name))
		if err != nil || info.Mode()&fs.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return
		}
		found[name] = true
	}
	for _, name := range compiledRuntimeAssetNames {
		add(name)
	}
	if entries, err := os.ReadDir(root); err == nil {
		for _, entry := range entries {
			if compiledConfigFileRE.MatchString(entry.Name()) {
				add(entry.Name())
			}
		}
	}
	expressions := goRuntimeFileREs
	if extension == ".rs" {
		expressions = rustRuntimeFileREs
	}
	files, bytes, entries := 0, int64(0), 0
	_ = filepath.WalkDir(root, func(current string, entry fs.DirEntry, err error) error {
		// Every entry counts, not only the sources read: a checkout of a
		// hundred thousand assets must not keep Prepare walking.
		if entries++; entries > compiledRuntimeWalkEntries {
			return filepath.SkipAll
		}
		if err != nil {
			return nil
		}
		rel, relErr := filepath.Rel(root, current)
		if relErr != nil {
			return nil
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "vendor", "target", "node_modules", "testdata", ".just-dashboard":
				return filepath.SkipDir
			}
			if rel != "." && strings.Count(filepath.ToSlash(rel), "/") >= 6 {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() || path.Ext(entry.Name()) != extension || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil || info.Size() > 256<<10 {
			return nil
		}
		if files >= 400 || bytes+info.Size() > 4<<20 {
			return filepath.SkipAll
		}
		files, bytes = files+1, bytes+info.Size()
		content, _, readErr := readDetectionFile(current, 256<<10)
		if readErr != nil {
			return nil
		}
		for _, expression := range expressions {
			for _, match := range expression.FindAllSubmatch(content, 16) {
				literal := string(match[1])
				if path.IsAbs(literal) || strings.HasPrefix(literal, "..") {
					continue
				}
				add(literal)
			}
		}
		return nil
	})
	names := make([]string, 0, len(found))
	for name := range found {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// dockerIgnoredPaths reports whether the repository's .dockerignore keeps a
// root-relative path out of the build context, where a COPY of it from the
// build stage would fail the build. A path is out when a rule matches it or
// a directory above it. Anything a rule might touch — including an entry a
// later "!" rule re-includes — counts as ignored: leaving a file out only
// loses the convenience, copying a missing one loses the build.
func dockerIgnoredPaths(root string) func(string) bool {
	content, err := readContainedRegular(root, ".dockerignore", 64<<10)
	if err != nil {
		return func(string) bool { return false }
	}
	var patterns [][]string
	for _, line := range strings.Split(string(content), "\n") {
		pattern := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "!"))
		pattern = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(pattern, "/"), "./"), "/")
		if pattern == "" || strings.HasPrefix(pattern, "#") {
			continue
		}
		patterns = append(patterns, strings.Split(pattern, "/"))
	}
	return func(name string) bool {
		segments := strings.Split(name, "/")
		for depth := 1; depth <= len(segments); depth++ {
			for _, pattern := range patterns {
				if dockerIgnoreMatches(pattern, segments[:depth]) {
					return true
				}
			}
		}
		return false
	}
}

// dockerIgnoreMatches matches one rule's segments against a path's: "**"
// stands for any number of directories, every other segment follows
// path.Match, and a segment that cannot be read matches anything. It walks
// the rule once over the set of reachable positions, so no rule — however
// many "**" a repository writes — costs more than its length times the
// path's.
func dockerIgnoreMatches(pattern, segments []string) bool {
	reached := make([]bool, len(segments)+1)
	reached[0] = true
	for _, part := range pattern {
		next := make([]bool, len(segments)+1)
		if part == "**" {
			for index, any := range reached {
				next[index] = any || (index > 0 && next[index-1])
			}
		} else {
			for index := range segments {
				if !reached[index] {
					continue
				}
				if matched, err := path.Match(part, segments[index]); err != nil || matched {
					next[index+1] = true
				}
			}
		}
		reached = next
	}
	return reached[len(segments)]
}

// dotnetSQLiteSeeds are the committed SQLite files the project's connection
// strings name, by root-relative path. Detection moves such a database into
// dotnetRuntimeDataDir (detect_state.go); the recipe copies the committed
// file there too, so a new volume mounted on that directory starts from it —
// Docker fills an empty named volume from the image the first time it is
// mounted — rather than empty. The ASP.NET Core Identity template commits an
// app.db with its schema and never migrates, so an empty file there fails
// every sign-in. Later releases find the volume filled and copy nothing.
func dotnetSQLiteSeeds(root string, project dotnetProject) []string {
	content, err := readContainedRegular(root, project.file, 512<<10)
	if err != nil || !dotnetSQLitePackageRE.Match(content) {
		return nil
	}
	ignored := dockerIgnoredPaths(root)
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil
	}
	var seeds []string
	bases := map[string]bool{}
	for _, name := range []string{"appsettings.Production.json", "appsettings.json"} {
		settings, err := readContainedRegular(root, name, 256<<10)
		if err != nil {
			continue
		}
		for _, match := range dotnetConnectionRE.FindAllStringSubmatch(string(settings), 4) {
			file := path.Clean(strings.TrimPrefix(strings.TrimSpace(match[3]), "./"))
			base := path.Base(file)
			if !sqliteFileName(file) || strings.Contains(file, ":memory:") || !runtimeAssetPathRE.MatchString(file) ||
				!safePersistentName(base) || bases[base] || ignored(file) {
				continue
			}
			// The path itself must be the committed file, not a link to one.
			real, err := containedRegularPath(root, file, 1<<30)
			if err != nil || real != filepath.Join(realRoot, filepath.FromSlash(file)) {
				continue
			}
			bases[base] = true
			seeds = append(seeds, file)
		}
		// The same file detection read: Production's connection strings
		// replace the base file's.
		break
	}
	return seeds
}

// runtimeAssetPathRE is a relative path whose every segment is a plain name,
// safe to write unquoted into a COPY instruction.
var runtimeAssetPathRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}(?:/[A-Za-z0-9_][A-Za-z0-9._-]{0,127}){0,7}$`)

// compiledRuntime is what one compiled recipe's runtime stage carries: the
// files the service reads at runtime (assets, copied from source, the
// directory of the build stage they sit in), the system packages a
// dynamically linked binary needs, the start command, and the recipe's own
// ENV instructions, such as where the framework listens.
type compiledRuntime struct {
	assets   []string
	source   string
	packages []string
	start    string
	env      []string
}

// compiledRuntimeLines is the runtime stage the Go and Rust recipes share:
// the binary at /app, run as an unprivileged user from its own home, with the
// files the service reads at runtime owned by that user, and a data
// directory it owns so a volume mounted there starts writable. Alpine carries
// no zone database, so tzdata is installed for a TZ variable and a Rust
// time-zone crate to find one; a Go binary also embeds its own.
func compiledRuntimeLines(base ResolvedImage, runtime compiledRuntime) []string {
	source := runtime.source
	if source == "" {
		source = "/src"
	}
	packages := append([]string{"tzdata"}, runtime.packages...)
	lines := []string{
		"FROM " + immutableImageReference(base),
		"RUN apk add --no-cache " + strings.Join(packages, " ") + " && adduser -D -u 10001 app",
		"USER app",
		"WORKDIR " + compiledRuntimeHome,
		"COPY --from=build /out/app /app",
	}
	linked := true
	for _, asset := range runtime.assets {
		lines = append(lines, "COPY --from=build --chown=app:app "+source+"/"+asset+" "+compiledRuntimeHome+"/"+asset)
		linked = linked && asset != "app"
	}
	prepare := "RUN mkdir -p " + compiledRuntimeHome + "/data"
	if linked {
		// The working directory used to be /, where a saved start command's
		// ./app found the binary; the link keeps it finding it.
		prepare += " && ln -s /app " + compiledRuntimeHome + "/app"
	}
	lines = append(lines, prepare)
	lines = append(lines, runtime.env...)
	if strings.TrimSpace(runtime.start) == "" {
		return append(lines, `ENTRYPOINT ["/app"]`)
	}
	return append(lines, shellCMD(runtime.start))
}
