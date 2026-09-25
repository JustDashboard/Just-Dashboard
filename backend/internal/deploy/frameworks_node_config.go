package deploy

import (
	"encoding/json"
	"io"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// A framework's configuration file decides what its build writes as much as
// package.json does: next.config's output, the adapter svelte.config
// imports, astro.config's output, react-router.config's ssr, the Nitro
// preset, Vite's base and outDir, Nest's entry and tsconfig's rootDir. They
// are JavaScript, TypeScript or JSONC, read here as bounded text and never
// evaluated: a literal settles a question, and anything else leaves the
// catalogue's default in place with a decision instead of a guess. Detection
// and the recipe read them through the same function, so the plan detection
// proposes is the one the recipe renders.

// nodeConfigText is one configuration file: its name beside package.json
// and its text with comments removed, so a commented-out `output: 'export'`
// in a template says nothing.
type nodeConfigText struct{ name, text string }

// nodeConfigKinds are the configuration files read beside package.json, by
// kind, in each tool's own lookup order.
var nodeConfigKinds = []struct {
	kind  string
	names []string
}{
	{"next", []string{"next.config.js", "next.config.mjs", "next.config.ts", "next.config.cjs", "next.config.mts"}},
	{"svelte", []string{"svelte.config.js", "svelte.config.mjs", "svelte.config.ts"}},
	{"astro", []string{"astro.config.mjs", "astro.config.ts", "astro.config.js", "astro.config.mts", "astro.config.cjs"}},
	{"react-router", []string{"react-router.config.ts", "react-router.config.js", "react-router.config.mjs", "react-router.config.mts"}},
	{"nuxt", []string{"nuxt.config.ts", "nuxt.config.js", "nuxt.config.mjs", "nuxt.config.mts"}},
	{"vite", []string{"vite.config.ts", "vite.config.js", "vite.config.mjs", "vite.config.mts", "vite.config.cjs", "vite.config.cts"}},
	{"webpack", []string{"webpack.config.js", "webpack.config.cjs", "webpack.config.mjs", "webpack.config.ts"}},
	{"rsbuild", []string{"rsbuild.config.ts", "rsbuild.config.js", "rsbuild.config.mjs", "rsbuild.config.mts"}},
	{"rspack", []string{"rspack.config.js", "rspack.config.ts", "rspack.config.mjs", "rspack.config.cjs"}},
	{"farm", []string{"farm.config.ts", "farm.config.js", "farm.config.mjs"}},
	{"app", []string{"app.config.ts", "app.config.js", "app.config.mjs"}},
	{"nest-cli", []string{"nest-cli.json", ".nestcli.json"}},
	{"tsconfig.build", []string{"tsconfig.build.json"}},
	{"tsconfig", []string{"tsconfig.json"}},
	{"nx", []string{"nx.json"}},
}

// nodeProbedPaths are the files and directories a framework's defaults ask
// about by presence alone.
var nodeProbedPaths = []string{
	"index.html", ".meteor/release", "database/migrations", "medusa-config.ts", "medusa-config.js",
	"adonisrc.ts", "ace.js", "keystone.ts", "keystone.js", "redwood.toml", "remix.config.js", "api/db/migrations",
}

// nodeConventionalEntries are the files a server library's starter keeps its
// server in when package.json names none, in the order they are preferred.
var nodeConventionalEntries = []string{
	"src/index.ts", "src/server.ts", "src/main.ts", "src/app.ts", "index.ts", "server.ts", "main.ts", "app.ts",
	"src/index.js", "src/server.js", "src/main.js", "index.js", "server.js", "main.js", "app.js",
	"src/index.mjs", "index.mjs", "server.mjs",
}

// nodeRootFiles are the files beside package.json a framework's defaults can
// depend on, all read as data: angular.json and nest-cli.json are JSON, a
// Procfile is lines, the rest are text a literal is looked for in.
type nodeRootFiles struct {
	angularJSON []byte
	procfile    []byte
	config      map[string]nodeConfigText
	present     map[string]bool
	// entries are the conventional server entries that exist, and
	// entryHeads the start of each, which says whether it listens itself or
	// exports a fetch handler for Bun to serve.
	entries    []string
	entryHeads map[string]string
	// tsOutsideSrc are TypeScript files outside src/ and test/ (bounded),
	// any of which tsc compiles unless its tsconfig excludes it, moving the
	// common root to the package itself: Nest then writes dist/src/main.js
	// instead of dist/main.js.
	tsOutsideSrc []string
	// nextImage is a page or component that imports next/image; it is looked
	// for only when next.config exports a static site, where the default
	// image loader fails the build.
	nextImage string
	// nx is the Nx workspace nx.json and the project.json files describe.
	nx *nxWorkspace
}

// nodeConfigNames are the file names a configuration kind is read from.
func nodeConfigNames(kind string) []string {
	for _, candidate := range nodeConfigKinds {
		if candidate.kind == kind {
			return candidate.names
		}
	}
	return nil
}

func (f nodeRootFiles) configText(kind string) (nodeConfigText, bool) {
	config, ok := f.config[kind]
	return config, ok
}

func (f nodeRootFiles) has(rel string) bool { return f.present[rel] }

const (
	nodeEntryHeadBytes    = 16 << 10
	nodeSourceScanFiles   = 400
	nodeSourceScanHead    = 32 << 10
	nodeSourceScanEntries = 4096
)

// readNodeRootFiles reads a package's framework files under files, its own
// directory. The reads are bounded, charged to the install's read budget,
// and never follow a symlink.
func readNodeRootFiles(files nodeFiles, manifest nodeManifest) nodeRootFiles {
	result := nodeRootFiles{config: map[string]nodeConfigText{}, present: map[string]bool{}, entryHeads: map[string]string{}}
	if content, err := files.read("angular.json", 512<<10); err == nil {
		result.angularJSON = content
	}
	if content, err := files.read("Procfile", 64<<10); err == nil {
		result.procfile = content
	}
	for _, kind := range nodeConfigKinds {
		for _, name := range kind.names {
			content, err := files.read(name, nodeConfigMaxBytes)
			if err != nil {
				continue
			}
			text := string(manifestText(content))
			if strings.HasSuffix(name, ".json") {
				text = string(denoJSONWithoutComments([]byte(text)))
			} else {
				text = jsWithoutComments(text)
			}
			result.config[kind.kind] = nodeConfigText{name: name, text: text}
			break
		}
	}
	for _, probed := range nodeProbedPaths {
		if files.exists(probed) || files.dirExists(probed) {
			result.present[probed] = true
		}
	}
	for _, entry := range nodeConventionalEntries {
		if !files.exists(entry) {
			continue
		}
		result.entries = append(result.entries, entry)
		if head, ok := readNodeHead(files, entry, nodeEntryHeadBytes); ok {
			result.entryHeads[entry] = jsWithoutComments(head)
		}
	}
	if manifest.has("@nestjs/core") {
		result.tsOutsideSrc = nodeTypeScriptOutsideSrc(files)
	}
	if manifest.has("next") {
		if config, ok := result.config["next"]; ok && nextOutputMode(config.text) == "export" && !nextImagesUnoptimized(config.text) {
			result.nextImage = nodeSourceImporting(files, []string{"app", "src/app", "pages", "src/pages", "components", "src/components", "src"}, nextImageImportRE)
		}
	}
	if _, ok := result.config["nx"]; ok {
		result.nx = readNxWorkspace(files, result.config["nx"].text)
	}
	return result
}

// readNodeHead reads the beginning of a file, charging only what was read.
func readNodeHead(files nodeFiles, rel string, limit int64) (string, bool) {
	reader, err := files.openHead(rel, 4<<20)
	if err != nil {
		return "", false
	}
	defer reader.Close()
	content, err := io.ReadAll(io.LimitReader(reader, limit))
	if err != nil && len(content) == 0 {
		return "", false
	}
	return string(content), true
}

// nodeDirectoryEntries lists a directory's entries, directories first
// flagged, bounded; "" is the files' own directory.
func nodeDirectoryEntries(files nodeFiles, rel string, limit int) (names []string, directories map[string]bool) {
	directories = map[string]bool{}
	if files.root == nil || (rel != "" && !safeRelativePath(rel)) {
		return nil, directories
	}
	name := files.dir
	if rel != "" {
		name = files.name(rel)
	}
	if name == "" {
		name = "."
	}
	directory, err := files.root.Open(name)
	if err != nil {
		return nil, directories
	}
	defer directory.Close()
	entries, err := directory.ReadDir(limit)
	if err != nil && len(entries) == 0 {
		return nil, directories
	}
	for _, entry := range entries {
		switch {
		case entry.IsDir():
			directories[entry.Name()] = true
			names = append(names, entry.Name())
		case entry.Type().IsRegular():
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, directories
}

// nodeTypeScriptOutsideSrc lists TypeScript files tsc may compile that are
// not under src/ nor under a directory Nest's default tsconfig.build.json
// excludes: at the package root (prisma.config.ts, drizzle.config.ts), or one
// directory down (prisma/seed.ts, scripts/migrate.ts).
func nodeTypeScriptOutsideSrc(files nodeFiles) []string {
	skipped := map[string]bool{"src": true, "test": true, "tests": true, "node_modules": true, "dist": true}
	names, directories := nodeDirectoryEntries(files, "", 512)
	compiled := func(name string) bool {
		return (strings.HasSuffix(name, ".ts") || strings.HasSuffix(name, ".mts") || strings.HasSuffix(name, ".cts")) &&
			!strings.HasSuffix(name, ".d.ts") && !strings.HasSuffix(name, ".spec.ts") && !strings.HasSuffix(name, ".test.ts")
	}
	found := []string{}
	for _, name := range names {
		if !directories[name] && compiled(name) && len(found) < 32 {
			found = append(found, name)
		}
	}
	for _, name := range names {
		if !directories[name] || skipped[name] || strings.HasPrefix(name, ".") {
			continue
		}
		children, childDirectories := nodeDirectoryEntries(files, name, 256)
		for _, child := range children {
			if !childDirectories[child] && compiled(child) && len(found) < 32 {
				found = append(found, name+"/"+child)
				break
			}
		}
	}
	return found
}

// tsconfigExcludes says whether a tsconfig exclude pattern covers a file:
// the file itself, its top directory, or a `**/` pattern on its name.
func tsconfigExcludes(patterns []string, file string) bool {
	top, _, _ := strings.Cut(file, "/")
	for _, pattern := range patterns {
		pattern = strings.TrimSuffix(strings.TrimPrefix(pattern, "./"), "/")
		if rest, ok := strings.CutPrefix(pattern, "**/"); ok {
			if matched, _ := path.Match(rest, path.Base(file)); matched {
				return true
			}
			continue
		}
		for _, target := range []string{file, top} {
			if matched, _ := path.Match(pattern, target); matched {
				return true
			}
		}
		if strings.HasPrefix(file, pattern+"/") {
			return true
		}
	}
	return false
}

var nextImageImportRE = regexp.MustCompile(`(?:from\s+|require\(\s*|import\(\s*)['"]next/(?:legacy/)?image['"]`)

// nodeSourceImporting finds the first source file under the given
// directories whose head matches the import, reading at most a bounded
// number of files breadth-first.
func nodeSourceImporting(files nodeFiles, roots []string, importRE *regexp.Regexp) string {
	queue := []string{}
	for _, root := range roots {
		if files.dirExists(root) {
			queue = append(queue, root)
		}
	}
	read, listed := 0, 0
	seen := map[string]bool{}
	for len(queue) > 0 && read < nodeSourceScanFiles && listed < nodeSourceScanEntries {
		directory := queue[0]
		queue = queue[1:]
		if seen[directory] {
			continue
		}
		seen[directory] = true
		names, directories := nodeDirectoryEntries(files, directory, 512)
		listed += len(names)
		for _, name := range names {
			rel := directory + "/" + name
			if directories[name] {
				if name != "node_modules" && !strings.HasPrefix(name, ".") {
					queue = append(queue, rel)
				}
				continue
			}
			switch path.Ext(name) {
			case ".tsx", ".jsx", ".ts", ".js", ".mjs", ".mdx":
			default:
				continue
			}
			if read >= nodeSourceScanFiles {
				break
			}
			read++
			if head, ok := readNodeHead(files, rel, nodeSourceScanHead); ok && importRE.MatchString(head) {
				return rel
			}
		}
	}
	return ""
}

// jsWithoutComments removes // and /* */ comments outside string and
// template literals. Regular expression literals are not told apart from
// division; a configuration file seldom holds one, and at worst a literal
// is left unread, which keeps the default.
func jsWithoutComments(text string) string {
	var out strings.Builder
	out.Grow(len(text))
	quote := byte(0)
	for index := 0; index < len(text); index++ {
		c := text[index]
		if quote != 0 {
			out.WriteByte(c)
			switch {
			case c == '\\' && index+1 < len(text):
				index++
				out.WriteByte(text[index])
			case c == quote:
				quote = 0
			}
			continue
		}
		switch {
		case c == '"' || c == '\'' || c == '`':
			quote = c
			out.WriteByte(c)
		case c == '/' && index+1 < len(text) && text[index+1] == '/':
			for index < len(text) && text[index] != '\n' {
				index++
			}
			out.WriteByte('\n')
		case c == '/' && index+1 < len(text) && text[index+1] == '*':
			end := strings.Index(text[index+2:], "*/")
			if end < 0 {
				return out.String()
			}
			index += end + 3
			out.WriteByte(' ')
		default:
			out.WriteByte(c)
		}
	}
	return out.String()
}

// Literals the readers below look for. A value is a literal only when it is
// quoted where the key is; anything computed is left unread.
var (
	jsOutputKeyRE         = regexp.MustCompile(`(?:^|[\s{,])output\s*:`)
	nextOutputLiteralRE   = regexp.MustCompile(`(?:^|[\s{,])output\s*:\s*['"` + "`" + `](export|standalone)['"` + "`" + `]`)
	nextDistDirRE         = regexp.MustCompile(`(?:^|[\s{,])distDir\s*:\s*['"]([\w./-]+)['"]`)
	nextUnoptimizedRE     = regexp.MustCompile(`(?:^|[\s{,])unoptimized\s*:\s*true\b`)
	nextCustomLoaderRE    = regexp.MustCompile(`(?:^|[\s{,])(?:loader\s*:\s*['"]custom['"]|loaderFile\s*:)`)
	basePathRE            = regexp.MustCompile(`(?:^|[\s{,])basePath\s*:\s*['"](/[\w./-]*)['"]`)
	svelteAdapterImportRE = regexp.MustCompile(`import\s+(\w+)\s+from\s+['"]((?:@sveltejs/adapter-[\w-]+)|(?:svelte-adapter-[\w-]+)|(?:@[\w.-]+/svelte-adapter[\w-]*)|(?:svelte-kit-sst))['"]`)
	svelteAdapterCallRE   = regexp.MustCompile(`(?:^|[\s{,])adapter\s*:\s*(\w+)\s*\(`)
	svelteFallbackRE      = regexp.MustCompile(`(?:^|[\s{,])fallback\s*:\s*['"]([\w.-]+\.html)['"]`)
	sveltePagesRE         = regexp.MustCompile(`(?:^|[\s{,])pages\s*:\s*['"]([\w./-]+)['"]`)
	astroOutputRE         = regexp.MustCompile(`(?:^|[\s{,])output\s*:\s*['"](static|server|hybrid)['"]`)
	astroModeRE           = regexp.MustCompile(`(?:^|[\s{,(])mode\s*:\s*['"](standalone|middleware)['"]`)
	astroAdapterRE        = regexp.MustCompile(`from\s+['"](@astrojs/(?:node|vercel|netlify|cloudflare|deno)(?:/[\w-]+)?|astro-sst|@deno/astro-adapter)['"]`)
	jsBaseRE              = regexp.MustCompile(`(?:^|[\s{,])base\s*:\s*['"](/[\w./-]*)['"]`)
	ssrFalseRE            = regexp.MustCompile(`(?:^|[\s{,])ssr\s*:\s*false\b`)
	nitroPresetRE         = regexp.MustCompile(`(?:^|[\s{,])preset\s*:\s*['"]([\w-]+)['"]`)
	viteOutDirRE          = regexp.MustCompile(`(?:^|[\s{,])outDir\s*:\s*['"]([\w./-]+)['"]`)
	webpackOutputPathRE   = regexp.MustCompile(`(?:^|[\s{,])path\s*:\s*(?:path\.)?(?:resolve|join)\(\s*__dirname\s*,\s*['"]([\w./-]+)['"]\s*\)`)
	rsbuildDistRootRE     = regexp.MustCompile(`distPath\s*:\s*\{[^}]*?\broot\s*:\s*['"]([\w./-]+)['"]`)
	farmOutputPathRE      = regexp.MustCompile(`output\s*:\s*\{[^}]*?\bpath\s*:\s*['"]([\w./-]+)['"]`)
	prerenderTrueRE       = regexp.MustCompile(`(?:^|[\s{,(])prerender\s*:\s*(?:true\b|\{)`)
	nitroVitePluginRE     = regexp.MustCompile(`from\s+['"](?:nitro/vite|@tanstack/nitro-v2-vite-plugin|nitro-v2-vite-plugin)['"]|\bnitro(?:V2Plugin)?\s*\(`)
)

// nextOutputMode reads next.config's output: "export", "standalone", "" when
// it sets none, and "unknown" when it sets one that is not a literal.
func nextOutputMode(text string) string {
	if match := nextOutputLiteralRE.FindStringSubmatch(text); match != nil {
		return match[1]
	}
	if jsOutputKeyRE.MatchString(text) {
		return "unknown"
	}
	return ""
}

// nextImagesUnoptimized says a static export can use next/image: the images
// are served as they are, or through a loader the project supplies.
func nextImagesUnoptimized(text string) bool {
	return nextUnoptimizedRE.MatchString(text) || nextCustomLoaderRE.MatchString(text)
}

// literalRelativePath is a config literal that may be written into an
// output directory: a plain path inside the package.
func literalRelativePath(re *regexp.Regexp, text string) string {
	match := re.FindStringSubmatch(text)
	if match == nil {
		return ""
	}
	value := strings.TrimSuffix(strings.TrimPrefix(match[1], "./"), "/")
	if !safeRelativePath(value) || !nodeMemberPathRE.MatchString(value) {
		return ""
	}
	return path.Clean(value)
}

// literalBasePath is a base path a site is served under ("/docs"), or ""
// for none or for the root.
func literalBasePath(re *regexp.Regexp, text string) string {
	match := re.FindStringSubmatch(text)
	if match == nil {
		return ""
	}
	base := "/" + strings.Trim(match[1], "/")
	if base == "/" || !nodeMemberPathRE.MatchString(strings.TrimPrefix(base, "/")) || strings.Contains(base, "..") {
		return ""
	}
	return base
}

// svelteAdapter is the adapter package svelte.config passes to kit.adapter:
// the import the adapter call names, or the only adapter imported.
func svelteAdapter(text string) string {
	imports := map[string]string{}
	packages := []string{}
	for _, match := range svelteAdapterImportRE.FindAllStringSubmatch(text, -1) {
		imports[match[1]] = match[2]
		if !slices.Contains(packages, match[2]) {
			packages = append(packages, match[2])
		}
	}
	if call := svelteAdapterCallRE.FindStringSubmatch(text); call != nil && imports[call[1]] != "" {
		return imports[call[1]]
	}
	if len(packages) == 1 {
		return packages[0]
	}
	return ""
}

// nestLayout is where `nest build` writes the entry, read from nest-cli.json
// and the tsconfig it compiles with.
type nestLayout struct {
	entry  string
	reason string
}

// nestEntry predicts the file `nest build` writes the application's entry
// to: dist/apps/<app>/main.js in monorepo mode, dist/<entryFile>.js when tsc's
// common root is src, and dist/src/<entryFile>.js when a TypeScript file
// outside src makes the package itself the common root.
func nestEntry(files nodeRootFiles) nestLayout {
	var cli struct {
		SourceRoot string `json:"sourceRoot"`
		EntryFile  string `json:"entryFile"`
		Monorepo   bool   `json:"monorepo"`
		Root       string `json:"root"`
	}
	if config, ok := files.configText("nest-cli"); ok {
		_ = json.Unmarshal([]byte(config.text), &cli)
	}
	entryFile := "main"
	if cli.EntryFile != "" && nodeMemberPathRE.MatchString(cli.EntryFile) && !strings.Contains(cli.EntryFile, "..") {
		entryFile = strings.TrimSuffix(cli.EntryFile, ".ts")
	}
	if cli.Monorepo {
		app := path.Base(strings.TrimSuffix(cli.Root, "/"))
		if app == "" || app == "." || !nodeMemberPathRE.MatchString(app) {
			return nestLayout{entry: "dist/main.js"}
		}
		return nestLayout{entry: path.Join("dist", "apps", app, entryFile+".js"), reason: "nest-cli.json is a monorepo whose default project is " + app}
	}
	sourceRoot := "src"
	if cli.SourceRoot != "" && safeRelativePath(cli.SourceRoot) && nodeMemberPathRE.MatchString(cli.SourceRoot) {
		sourceRoot = path.Clean(cli.SourceRoot)
	}
	outDir, rootDir, limited := "dist", "", false
	var exclude []string
	for _, kind := range []string{"tsconfig.build", "tsconfig"} {
		config, ok := files.configText(kind)
		if !ok {
			continue
		}
		var tsconfig struct {
			CompilerOptions struct {
				OutDir  string `json:"outDir"`
				RootDir string `json:"rootDir"`
			} `json:"compilerOptions"`
			Include []string  `json:"include"`
			Exclude *[]string `json:"exclude"`
		}
		if json.Unmarshal([]byte(config.text), &tsconfig) != nil {
			continue
		}
		// The build's tsconfig extends the base one, so its own exclude
		// list, when it has one, is the one tsc applies.
		if tsconfig.Exclude != nil && exclude == nil {
			exclude = *tsconfig.Exclude
		}
		if tsconfig.CompilerOptions.OutDir != "" && outDir == "dist" {
			outDir = path.Clean(strings.TrimPrefix(tsconfig.CompilerOptions.OutDir, "./"))
		}
		if tsconfig.CompilerOptions.RootDir != "" && rootDir == "" {
			rootDir = path.Clean(strings.TrimPrefix(tsconfig.CompilerOptions.RootDir, "./"))
		}
		if len(tsconfig.Include) > 0 && !limited {
			limited = true
			for _, pattern := range tsconfig.Include {
				pattern = strings.TrimPrefix(pattern, "./")
				if pattern != sourceRoot && !strings.HasPrefix(pattern, sourceRoot+"/") {
					limited = false
				}
			}
		}
	}
	if !safeRelativePath(outDir) || !nodeMemberPathRE.MatchString(outDir) {
		outDir = "dist"
	}
	outside := ""
	for _, file := range files.tsOutsideSrc {
		if !tsconfigExcludes(exclude, file) && !strings.HasPrefix(file, sourceRoot+"/") {
			outside = file
			break
		}
	}
	switch {
	case rootDir == sourceRoot || (rootDir == "" && limited):
		return nestLayout{entry: path.Join(outDir, entryFile+".js")}
	case rootDir == "." || (rootDir == "" && outside != ""):
		reason := "tsconfig's rootDir is the package"
		if rootDir == "" {
			reason = outside + " is compiled beside " + sourceRoot + "/, so tsc writes " + sourceRoot + "/ under " + outDir + "/"
		}
		return nestLayout{entry: path.Join(outDir, sourceRoot, entryFile+".js"), reason: reason}
	}
	return nestLayout{entry: path.Join(outDir, entryFile+".js")}
}

// angularOutputMode is the application builder's outputMode ("static" or
// "server") for the application project angularOutput chose, "" when unset.
func angularOutputMode(content []byte, project string) string {
	var workspace struct {
		Projects map[string]struct {
			Architect map[string]struct {
				Options struct {
					OutputMode string `json:"outputMode"`
				} `json:"options"`
			} `json:"architect"`
			Targets map[string]struct {
				Options struct {
					OutputMode string `json:"outputMode"`
				} `json:"options"`
			} `json:"targets"`
		} `json:"projects"`
	}
	if len(content) == 0 || json.Unmarshal(content, &workspace) != nil {
		return ""
	}
	entry, ok := workspace.Projects[project]
	if !ok {
		return ""
	}
	if build, ok := entry.Architect["build"]; ok {
		return build.Options.OutputMode
	}
	return entry.Targets["build"].Options.OutputMode
}
