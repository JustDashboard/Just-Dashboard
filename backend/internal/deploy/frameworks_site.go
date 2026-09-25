package deploy

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Site generators turn content and templates into a directory of pages. A
// generator's root used to be read as something else: Hugo's templates
// became a site serving `{{ .Content }}`, a Hugo Modules go.mod a Go service
// with no main package, MkDocs a Python service asking for a start command,
// a Jekyll index.html raw Liquid, mdBook nothing at all. The same readers
// below decide the candidate at detection and the recipe at preparation, from
// the root's files read as bounded data; nothing in them is run on the host.

// siteTree is a site root's files as the generator readers see them: read
// returns a bounded regular file, dir says a directory holds files, file that
// a regular file exists, and workflows are the checkout's GitHub workflows,
// where a site's CI pins its generator release.
type siteTree struct {
	read      siteFiles
	dir       func(relative string) bool
	file      func(relative string) bool
	workflows func() [][]byte
}

// containedSiteTree reads a root of a checkout at boundary.
func containedSiteTree(boundary, root string) siteTree {
	read := func(relative string) ([]byte, bool) {
		if !safeRelativePath(relative) {
			return nil, false
		}
		content, err := readContainedRegular(root, relative, 256<<10)
		if err != nil {
			return nil, false
		}
		return manifestText(content), true
	}
	return siteTree{
		read: read,
		dir: func(relative string) bool {
			if !safeRelativePath(relative) {
				return false
			}
			info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(relative)))
			return err == nil && info.IsDir()
		},
		file: func(relative string) bool { return safeRelativePath(relative) && regularExists(root, relative) },
		workflows: func() [][]byte {
			directory := filepath.Join(boundary, ".github", "workflows")
			entries, err := os.ReadDir(directory)
			if err != nil {
				return nil
			}
			var contents [][]byte
			for _, entry := range entries {
				name := strings.ToLower(entry.Name())
				if len(contents) >= 16 || !entry.Type().IsRegular() || (!strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml")) {
					continue
				}
				if content, err := readContainedRegular(boundary, ".github/workflows/"+entry.Name(), 64<<10); err == nil {
					contents = append(contents, content)
				}
			}
			return contents
		},
	}
}

// siteGenerator is what a generator root's files say: which generator, how
// the recipe builds it, what it writes and which release it was written for.
type siteGenerator struct {
	name   string
	recipe string
	// config is the file that names the generator, relative to the root.
	config string
	build  string
	output string
	// version is the release the recipe builds with; declared what the
	// repository asks for and where, unpinned that nothing asks.
	version, declared string
	unpinned          bool
	versionIssue      string
	// issue is why the recipe cannot build the site (RecipeIssue).
	issue    string
	evidence []DetectionEvidence
	// themes are the directories the configured theme is read from.
	themes []string
	// Hugo: package.json declares dependencies Hugo Pipes run.
	nodeDependencies bool
	// Jekyll: the Ruby release, whether a Gemfile and lock are committed,
	// and whether the lock has to gain a Linux platform first.
	ruby, rubyDeclared string
	gemfile, gemLock   bool
	addLinuxPlatform   bool
	// mdBook: which of the two catalogued release lines builds.
	mdbookLine string
	// Python sites: what a root with no requirements installs, and whether
	// a plugin needs git in the image.
	pythonPackages []string
	needsGit       bool
	// sphinxSource is the directory holding conf.py.
	sphinxSource string
	// pythonDeclared is the interpreter family Read the Docs builds with.
	pythonDeclared string
}

func (g siteGenerator) label() string { return siteGeneratorNames[g.name] }

func (g *siteGenerator) note(file, reason string) {
	if len(g.evidence) < 16 {
		g.evidence = append(g.evidence, DetectionEvidence{Path: file, Reason: boundedText(reason, 512)})
	}
}

// siteGeneratorReaders recognise a generator from the files at a root, most
// specific first: Hugo's config.toml and Zola's share a name, and Hexo's
// _config.yml is not Jekyll's.
var siteGeneratorReaders = []func(siteTree) (siteGenerator, bool){
	hugoSite, zolaSite, mdbookSite, zensicalSite, mkdocsSite, pelicanSite, sphinxSite, jekyllSite,
}

func readSiteGenerator(tree siteTree) (siteGenerator, bool) {
	for _, recognise := range siteGeneratorReaders {
		if generator, ok := recognise(tree); ok {
			return generator, true
		}
	}
	return siteGenerator{}, false
}

// siteConfig is a generator's configuration flattened to lower-cased dotted
// keys: TOML through the line reader, YAML and JSON through the YAML parser.
type siteConfig map[string]tomlValue

func readSiteConfig(name string, content []byte) siteConfig {
	config := siteConfig{}
	if strings.HasSuffix(name, ".toml") {
		for _, entry := range readTOML(content) {
			key := strings.ToLower(entry.key)
			if entry.table != "" {
				key = strings.ToLower(entry.table) + "." + key
			}
			if _, seen := config[key]; !seen {
				config[key] = entry.value
			}
		}
		return config
	}
	var document map[string]any
	if yaml.Unmarshal(content, &document) != nil {
		return config
	}
	var flatten func(prefix string, value any, depth int)
	flatten = func(prefix string, value any, depth int) {
		switch typed := value.(type) {
		case map[string]any:
			if depth > 3 {
				return
			}
			for key, inner := range typed {
				name := strings.ToLower(key)
				if prefix != "" {
					name = prefix + "." + name
				}
				flatten(name, inner, depth+1)
			}
		case []any:
			list := []string{}
			for _, item := range typed {
				if text, ok := item.(string); ok {
					list = append(list, text)
				}
			}
			config[prefix] = tomlValue{list: list, isList: true}
		case string:
			config[prefix] = tomlValue{text: typed}
		case nil:
		default:
			config[prefix] = tomlValue{text: strings.TrimSpace(strings.Trim(jsonText(typed), `"`))}
		}
	}
	flatten("", document, 0)
	return config
}

func jsonText(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func (c siteConfig) text(key string) string { return strings.TrimSpace(c[key].text) }

// values reads a key that may be a string or a list of them.
func (c siteConfig) values(key string) []string {
	value := c[key]
	if value.isList {
		return value.list
	}
	if text := strings.TrimSpace(value.text); text != "" {
		return []string{text}
	}
	return nil
}

func firstSiteFile(tree siteTree, names ...string) (string, []byte) {
	for _, name := range names {
		if content, ok := tree.read(name); ok {
			return name, content
		}
	}
	return "", nil
}

// siteOutput is a directory a configuration names, when it is a plain path
// inside the root; the generator's default otherwise. The path becomes part
// of a COPY line and, for Pelican and Sphinx, of the build command, so only
// letters, digits and ._/- pass.
func siteOutput(value, fallback string) string {
	value = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(value), "./"), "/")
	if value == "" || !safeRelativePath(value) || !sitePathRE.MatchString(value) {
		return fallback
	}
	return path.Clean(value)
}

var sitePathRE = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

var (
	siteVersionRE      = regexp.MustCompile(`^v?([0-9]+\.[0-9]+(?:\.[0-9]+)?)$`)
	siteVersionRangeRE = regexp.MustCompile(`^[\^~=>v ]*([0-9]+\.[0-9]+(?:\.[0-9]+)?)`)
)

// declaredSiteVersion is one place a repository names its generator release.
type declaredSiteVersion struct {
	version, source string
	// minimum says the release is a floor, not a pin.
	minimum bool
	latest  bool
}

func compareSiteVersions(a, b string) int {
	left, right := strings.Split(a, "."), strings.Split(b, ".")
	for index := 0; index < 3; index++ {
		var x, y int
		if index < len(left) {
			x, _ = strconv.Atoi(left[index])
		}
		if index < len(right) {
			y, _ = strconv.Atoi(right[index])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// workflowSiteVersion reads the release a CI workflow installs: an action's
// `<tool>-version:` input, a `<TOOL>_VERSION:` variable, or `<tool>@x.y.z`.
func workflowSiteVersion(workflows [][]byte, tool string) (declaredSiteVersion, bool) {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?m)\b` + tool + `-version\s*:\s*['"]?(latest|v?[0-9]+\.[0-9]+(?:\.[0-9]+)?)`),
		regexp.MustCompile(`(?m)\b` + strings.ToUpper(tool) + `_VERSION\s*[:=]\s*['"]?(v?[0-9]+\.[0-9]+(?:\.[0-9]+)?)`),
		regexp.MustCompile(`\b` + tool + `@v?([0-9]+\.[0-9]+(?:\.[0-9]+)?)\b`),
	}
	for _, content := range workflows {
		for _, pattern := range patterns {
			if match := pattern.FindSubmatch(content); match != nil {
				value := string(match[1])
				if value == "latest" {
					return declaredSiteVersion{latest: true, source: "a GitHub workflow"}, true
				}
				return declaredSiteVersion{version: strings.TrimPrefix(value, "v"), source: "a GitHub workflow"}, true
			}
		}
	}
	return declaredSiteVersion{}, false
}

// toolVersionsSite reads asdf's or mise's .tool-versions line for a tool;
// Hugo's plugin spells the extended edition `extended_0.139.0`.
func toolVersionsSite(tree siteTree, tool string) (declaredSiteVersion, bool) {
	content, ok := tree.read(".tool-versions")
	if !ok {
		return declaredSiteVersion{}, false
	}
	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == tool {
			value := strings.TrimPrefix(strings.TrimPrefix(fields[1], "extended_"), "v")
			if match := siteVersionRE.FindStringSubmatch(value); match != nil {
				return declaredSiteVersion{version: match[1], source: ".tool-versions"}, true
			}
		}
	}
	return declaredSiteVersion{}, false
}

// netlifySiteVersion reads a *_VERSION a netlify.toml build environment sets.
func netlifySiteVersion(tree siteTree, variable string) (declaredSiteVersion, bool) {
	content, ok := tree.read("netlify.toml")
	if !ok {
		return declaredSiteVersion{}, false
	}
	entries := readTOML(content)
	for _, table := range []string{"build.environment", "context.production.environment"} {
		if value := strings.TrimPrefix(strings.TrimSpace(tomlText(entries, table, variable)), "v"); value != "" {
			if value == "latest" {
				return declaredSiteVersion{latest: true, source: "netlify.toml"}, true
			}
			if match := siteVersionRE.FindStringSubmatch(value); match != nil {
				return declaredSiteVersion{version: match[1], source: "netlify.toml"}, true
			}
		}
	}
	return declaredSiteVersion{}, false
}

// Hugo publishes an official image, extended edition included, from 0.141.0
// on; the reviewed default is the release the catalogue names.
const (
	hugoDefaultVersion = "0.166.0"
	hugoOldestImage    = "0.141.0"
	zolaDefaultVersion = "0.23.6"
	zolaOldestImage    = "0.15.3"
	jekyllDefaultRuby  = "3.3"
	hugoBuildCommand   = `hugo --gc --minify --baseURL "${HUGO_BASEURL:-/}"`
	zolaBuildCommand   = "zola build --base-url /"
	mdbookBuildCommand = "mdbook build"
	jekyllBuildCommand = `bundle exec jekyll build --baseurl ""`
	// jekyllURLOverride is the configuration the Jekyll recipe writes for a
	// site whose _config.yml sets no url: the GitHub Pages gem otherwise
	// derives it from the repository's GitHub name, which a checkout here
	// does not carry, and stops the build.
	jekyllURLOverride   = "/tmp/jd-jekyll.yml"
	mkdocsBuildCommand  = "mkdocs build"
	zensicalBuildComand = "zensical build"
)

var jekyllRubyVersions = []string{"3.1", "3.2", "3.3", "3.4"}

var hugoConfigNames = []string{
	"hugo.toml", "hugo.yaml", "hugo.yml", "hugo.json",
	"config/_default/hugo.toml", "config/_default/hugo.yaml", "config/_default/hugo.yml", "config/_default/hugo.json",
	"config/_default/config.toml", "config/_default/config.yaml", "config/_default/config.yml", "config/_default/config.json",
}

// hugoContentDirectories are the directories only a Hugo site has together
// with a config.toml: what separates Hugo's legacy configuration name from
// every other tool's config.toml.
var hugoContentDirectories = []string{"content", "layouts", "archetypes", "themes"}

func hugoSite(tree siteTree) (siteGenerator, bool) {
	name, content := firstSiteFile(tree, hugoConfigNames...)
	legacy := false
	if name == "" {
		name, content = firstSiteFile(tree, "config.toml", "config.yaml", "config.yml", "config.json")
		if name == "" {
			return siteGenerator{}, false
		}
		config := readSiteConfig(name, content)
		hugoKeys := config.text("baseurl") != "" || len(config.values("theme")) > 0 || config.text("languagecode") != "" ||
			config.text("publishdir") != "" || config.text("module.hugoversion.min") != ""
		directory := false
		for _, candidate := range hugoContentDirectories {
			directory = directory || tree.dir(candidate)
		}
		if config.text("base_url") != "" || !hugoKeys || !directory {
			return siteGenerator{}, false
		}
		legacy = true
	}
	config := readSiteConfig(name, content)
	generator := siteGenerator{name: "hugo", recipe: "site", config: name, build: hugoBuildCommand,
		output: siteOutput(config.text("publishdir"), "public")}
	if legacy {
		generator.note(name, "Hugo configuration (baseURL beside content/, layouts/ or themes/)")
	} else {
		generator.note(name, "Hugo configuration")
	}
	themesDir := siteOutput(config.text("themesdir"), "themes")
	for _, theme := range config.values("theme") {
		if directory := path.Join(themesDir, strings.TrimSpace(theme)); safeRelativePath(directory) && len(generator.themes) < 8 {
			generator.themes = append(generator.themes, directory)
		}
	}
	if tree.file("go.mod") {
		generator.note("go.mod", "go.mod declares Hugo Modules, which the build downloads with Go")
	}
	if manifest, ok := tree.read("package.json"); ok {
		var node nodeManifest
		if parseNodeManifest(manifest, &node) {
			for name := range node.Dependencies {
				generator.nodeDependencies = generator.nodeDependencies || !hugoBinaryPackage(name)
			}
			for name := range node.DevDependencies {
				generator.nodeDependencies = generator.nodeDependencies || !hugoBinaryPackage(name)
			}
			if generator.nodeDependencies {
				generator.note("package.json", "package.json's dependencies are installed for Hugo Pipes (PostCSS, Tailwind, Babel)")
			}
		}
	}
	if tree.dir("assets") {
		generator.note("assets", "the official Hugo image is the extended edition, which compiles Sass in assets/")
	}
	declared := []declaredSiteVersion{}
	if hvm, ok := tree.read(".hvm"); ok {
		if match := siteVersionRE.FindStringSubmatch(strings.TrimSpace(firstVersionLine(string(hvm)))); match != nil {
			declared = append(declared, declaredSiteVersion{version: match[1], source: ".hvm"})
		}
	}
	if version, ok := netlifySiteVersion(tree, "HUGO_VERSION"); ok {
		declared = append(declared, version)
	}
	if version, ok := toolVersionsSite(tree, "hugo"); ok {
		declared = append(declared, version)
	}
	if tree.workflows != nil {
		if version, ok := workflowSiteVersion(tree.workflows(), "hugo"); ok {
			declared = append(declared, version)
		}
	}
	if manifest, ok := tree.read("package.json"); ok {
		var node nodeManifest
		if parseNodeManifest(manifest, &node) {
			// hugo-extended's version is the Hugo release it downloads.
			if match := siteVersionRangeRE.FindStringSubmatch(node.version("hugo-extended")); match != nil {
				declared = append(declared, declaredSiteVersion{version: match[1], source: "package.json hugo-extended"})
			}
		}
	}
	if minimum := strings.TrimPrefix(config.text("module.hugoversion.min"), "v"); minimum != "" {
		if match := siteVersionRE.FindStringSubmatch(minimum); match != nil {
			declared = append(declared, declaredSiteVersion{version: match[1], source: name + " module.hugoVersion.min", minimum: true})
		}
	}
	for _, theme := range generator.themes {
		if content, ok := tree.read(path.Join(theme, "theme.toml")); ok {
			if minimum := strings.TrimPrefix(tomlText(readTOML(content), "", "min_version"), "v"); siteVersionRE.MatchString(minimum) {
				declared = append(declared, declaredSiteVersion{version: siteVersionRE.FindStringSubmatch(minimum)[1], source: path.Join(theme, "theme.toml") + " min_version", minimum: true})
			}
		}
	}
	generator.chooseVersion(declared, hugoDefaultVersion, hugoOldestImage, "the oldest official Hugo image is "+hugoOldestImage)
	return generator, true
}

// hugoBinaryPackage names the npm packages that only download Hugo itself.
func hugoBinaryPackage(name string) bool {
	return name == "hugo-extended" || name == "hugo-bin" || name == "hugo-installer"
}

// chooseVersion settles the release a generator builds with: the first pin
// that has an image, else the default — a floor the default meets is no pin,
// and a pin older than any image is named as an issue.
func (g *siteGenerator) chooseVersion(declared []declaredSiteVersion, fallback, oldest, oldestReason string) {
	g.version = fallback
	pinned := false
	for _, pin := range declared {
		if pin.latest {
			g.declared = "latest (" + pin.source + ")"
			continue
		}
		if pin.minimum {
			if compareSiteVersions(fallback, pin.version) < 0 {
				g.declared = pin.version + " or newer (" + pin.source + ")"
				g.versionIssue = pin.source + " requires " + g.label() + " " + pin.version + ", newer than the " + fallback + " the recipe builds with"
			} else if g.declared == "" {
				g.declared = pin.version + " or newer (" + pin.source + ")"
			}
			continue
		}
		g.declared, pinned = pin.version+" ("+pin.source+")", true
		if len(strings.Split(pin.version, ".")) == 2 {
			pin.version += ".0"
		}
		if compareSiteVersions(pin.version, oldest) < 0 {
			g.versionIssue = pin.source + " pins " + g.label() + " " + pin.version + "; " + oldestReason + ", so the build uses " + fallback
		} else {
			g.version, g.versionIssue = pin.version, ""
		}
		break
	}
	// A floor is not a pin: the next rebuild may take a newer release.
	g.unpinned = !pinned
	switch {
	case g.versionIssue != "":
		g.note(g.config, g.versionIssue)
	case g.unpinned && g.declared != "":
		g.note(g.config, g.label()+" "+g.declared+"; the recipe builds with "+g.version)
	case g.unpinned:
		g.note(g.config, "no "+g.label()+" release is pinned; the recipe builds with "+g.version)
	default:
		g.note(g.config, g.label()+" "+g.declared)
	}
}

func zolaSite(tree siteTree) (siteGenerator, bool) {
	content, ok := tree.read("config.toml")
	if !ok {
		return siteGenerator{}, false
	}
	config := readSiteConfig("config.toml", content)
	directory := tree.dir("templates") || tree.dir("content") || tree.dir("themes") || tree.dir("sass")
	if config.text("base_url") == "" || !directory {
		return siteGenerator{}, false
	}
	generator := siteGenerator{name: "zola", recipe: "site", config: "config.toml", build: zolaBuildCommand,
		output: siteOutput(config.text("output_dir"), "public")}
	generator.note("config.toml", "Zola configuration (base_url beside templates/ or content/)")
	if theme := strings.TrimSpace(config.text("theme")); theme != "" && safeRelativePath(path.Join("themes", theme)) {
		generator.themes = append(generator.themes, path.Join("themes", theme))
	}
	declared := []declaredSiteVersion{}
	if version, ok := netlifySiteVersion(tree, "ZOLA_VERSION"); ok {
		declared = append(declared, version)
	}
	if version, ok := toolVersionsSite(tree, "zola"); ok {
		declared = append(declared, version)
	}
	if tree.workflows != nil {
		if version, ok := workflowSiteVersion(tree.workflows(), "zola"); ok {
			declared = append(declared, version)
		}
	}
	generator.chooseVersion(declared, zolaDefaultVersion, zolaOldestImage, "the oldest official Zola image is "+zolaOldestImage)
	return generator, true
}

// mdbookReleases are the mdBook releases the recipe installs, from the
// project's own release archives, each checked against its digest.
var mdbookReleases = map[string]struct {
	version                      string
	amd64Checksum, arm64Checksum string
}{
	"0.5": {version: "0.5.4",
		amd64Checksum: "5222beabd3e37dc5be0d18ff99b79058469354db5c220153a1b92db5ba12be89",
		arm64Checksum: "753e5c5c363ee8a56972344dcf91466f005a51db84a7aeffe427ae3ef83d6d44"},
	"0.4": {version: "0.4.52",
		amd64Checksum: "c96bdabf3754d9e016fb803c1565a41050434479b2dc1e02a87c8d0da7524c6c",
		arm64Checksum: "7273dda980915a1e2f114d63d432aa6284551e37f0358e3ce7653d1e49e6fa3f"},
}

// mdbook04Keys are book.toml settings mdBook 0.5 removed; a book that still
// uses them was written for 0.4.
var mdbook04Keys = []string{"output.html.curly-quotes", "output.html.google-analytics", "book.multilingual", "output.html.copy-fonts"}

var mdbookTableRE = regexp.MustCompile(`(?m)^\s*\[(preprocessor|output)\.([A-Za-z0-9_-]+)\]`)

// mdbookBuiltins are the preprocessors and renderers mdBook itself provides.
var mdbookBuiltins = map[string]bool{"links": true, "index": true, "html": true, "markdown": true}

func mdbookSite(tree siteTree) (siteGenerator, bool) {
	content, ok := tree.read("book.toml")
	if !ok {
		return siteGenerator{}, false
	}
	entries := readTOML(content)
	config := readSiteConfig("book.toml", content)
	generator := siteGenerator{name: "mdbook", recipe: "site", config: "book.toml", build: mdbookBuildCommand,
		output: siteOutput(config.text("build.build-dir"), "book")}
	generator.note("book.toml", "mdBook configuration")
	missing := []string{}
	seen := map[string]bool{}
	outputs := map[string]bool{}
	// A table with no keys ([preprocessor.katex] alone) is how most books
	// enable an extension, so the tables are read from their headers.
	for _, match := range mdbookTableRE.FindAllSubmatch(content, 64) {
		kind, extension := string(match[1]), string(match[2])
		table := kind + "." + extension
		if kind == "output" {
			outputs[extension] = true
		}
		if mdbookBuiltins[extension] || seen[table] {
			continue
		}
		seen[table] = true
		if strings.EqualFold(tomlText(entries, table, "optional"), "true") {
			generator.note("book.toml", "the optional "+kind+" mdbook-"+extension+" is skipped: the recipe installs mdBook alone")
			continue
		}
		missing = append(missing, "mdbook-"+extension)
	}
	sort.Strings(missing)
	switch {
	case len(outputs) > 0 && !outputs["html"]:
		generator.issue = "book.toml names renderers but not html, so mdBook writes no pages to serve; add an [output.html] table"
	case len(outputs) > 1:
		// With more than one renderer each writes its own directory.
		generator.output = path.Join(generator.output, "html")
		generator.note("book.toml", "several renderers are configured, so the pages are in "+generator.output)
	}
	if len(missing) > 0 {
		generator.issue = "book.toml runs " + strings.Join(missing, ", ") + ", which the mdBook recipe does not install; mark it optional = true, or commit a Dockerfile that installs it"
	}
	declared := []declaredSiteVersion{}
	if version, ok := toolVersionsSite(tree, "mdbook"); ok {
		declared = append(declared, version)
	}
	if tree.workflows != nil {
		if version, ok := workflowSiteVersion(tree.workflows(), "mdbook"); ok {
			declared = append(declared, version)
		}
	}
	generator.mdbookLine = "0.5"
	generator.unpinned = true
	for _, pin := range declared {
		if pin.latest || pin.version == "" {
			continue
		}
		generator.unpinned = false
		generator.declared = pin.version + " (" + pin.source + ")"
		switch {
		case strings.HasPrefix(pin.version, "0.4."):
			generator.mdbookLine = "0.4"
		case strings.HasPrefix(pin.version, "0.5."):
		default:
			generator.versionIssue = pin.source + " pins mdBook " + pin.version + "; the recipe builds with 0.4 or 0.5 releases"
		}
		break
	}
	if generator.unpinned {
		for _, key := range mdbook04Keys {
			if _, ok := config[key]; ok {
				generator.mdbookLine = "0.4"
				generator.note("book.toml", "book.toml sets "+key+", which mdBook 0.5 removed; built with mdBook 0.4")
				break
			}
		}
		if generator.mdbookLine == "0.5" && tree.dir("theme") {
			generator.mdbookLine = "0.4"
			generator.note("theme", "theme/ overrides mdBook's templates, which changed in 0.5; built with mdBook 0.4")
		}
	}
	generator.version = mdbookReleases[generator.mdbookLine].version
	if generator.versionIssue != "" {
		generator.note("book.toml", generator.versionIssue)
	} else if !generator.unpinned {
		generator.note("book.toml", "mdBook "+generator.version+" for "+generator.declared)
	}
	return generator, true
}

var (
	rubyVersionLineRE   = regexp.MustCompile(`(?m)^\s*ruby\s+['"]([0-9]+\.[0-9]+(?:\.[0-9]+)?)['"]`)
	rubyVersionFileRE   = regexp.MustCompile(`([0-9]+\.[0-9]+)(\.[0-9]+)?`)
	gemLockRubyRE       = regexp.MustCompile(`(?m)^RUBY VERSION\s*\n\s*ruby ([0-9]+\.[0-9]+(?:\.[0-9]+)?)`)
	gemLockPlatformRE   = regexp.MustCompile(`(?m)^PLATFORMS\s*\n((?:  \S+\s*\n)+)`)
	jekyllThemeRE       = regexp.MustCompile(`(?m)^(?:remote_)?theme\s*:`)
	jekyllURLRE         = regexp.MustCompile(`(?m)^url\s*:`)
	jekyllBaseURLRE     = siteYAMLKeyRE("baseurl")
	jekyllDestinationRE = siteYAMLKeyRE("destination")
)

func jekyllSite(tree siteTree) (siteGenerator, bool) {
	name, content := firstSiteFile(tree, "_config.yml", "_config.yaml")
	if name == "" || tree.file(".nojekyll") {
		// .nojekyll is how a GitHub Pages site says it is served as it is.
		return siteGenerator{}, false
	}
	gemfile, hasGemfile := tree.read("Gemfile")
	lock, hasLock := tree.read("Gemfile.lock")
	gems := rubyGems(gemfile, lock)
	if manifest, ok := tree.read("package.json"); ok {
		var node nodeManifest
		if parseNodeManifest(manifest, &node) && node.has("hexo") {
			return siteGenerator{}, false
		}
	}
	jekyll := has(gems, "jekyll", "github-pages")
	shaped := tree.dir("_posts") || tree.dir("_layouts") || tree.dir("_includes") || jekyllThemeRE.Match(content)
	if !jekyll && (hasGemfile || !shaped) {
		// A Gemfile that names neither is some other Ruby project's.
		return siteGenerator{}, false
	}
	generator := siteGenerator{name: "jekyll", recipe: "site", config: name, build: jekyllBuildCommand,
		gemfile: hasGemfile, gemLock: hasLock, output: "_site"}
	if !jekyllURLRE.Match(content) {
		generator.build += " --config " + name + "," + jekyllURLOverride
	}
	if match := jekyllBaseURLRE.FindSubmatch(content); match != nil && cleanStaticBasePath(string(match[1])) != "" {
		generator.note(name, "baseurl "+cleanStaticBasePath(string(match[1]))+" is for a GitHub Pages project site; the recipe builds the site for the root it is served from")
	}
	if match := jekyllDestinationRE.FindSubmatch(content); match != nil {
		generator.output = siteOutput(string(match[1]), "_site")
	}
	switch {
	case has(gems, "github-pages"):
		generator.note("Gemfile", "Jekyll through the github-pages gem")
	case jekyll:
		generator.note("Gemfile", strings.TrimSpace("Jekyll "+gems["jekyll"]))
	default:
		generator.note(name, "Jekyll site (_config.yml with _posts/, _layouts/ or a theme) and no Gemfile; the build installs the github-pages gem")
	}
	if !hasLock {
		generator.unpinned = true
	}
	for _, theme := range []string{"_layouts", "_includes"} {
		if tree.dir(theme) {
			generator.themes = append(generator.themes, theme)
		}
	}
	// The Ruby release: .ruby-version, the Gemfile's ruby line, the lock's.
	switch {
	case tree.file(".ruby-version"):
		version, _ := tree.read(".ruby-version")
		if match := rubyVersionFileRE.FindStringSubmatch(firstVersionLine(string(version))); match != nil {
			generator.rubyDeclared = match[0] + " (.ruby-version)"
			generator.ruby = match[1]
		}
	case rubyVersionLineRE.Match(gemfile):
		match := rubyVersionLineRE.FindSubmatch(gemfile)
		generator.rubyDeclared = string(match[1]) + " (Gemfile)"
		generator.ruby = rubyVersionFileRE.FindStringSubmatch(string(match[1]))[1]
		if strings.Count(string(match[1]), ".") == 2 {
			generator.versionIssue = "the Gemfile requires Ruby " + string(match[1]) + " exactly, and the image runs the newest " +
				generator.ruby + " release, which Bundler refuses unless they match"
		}
	case gemLockRubyRE.Match(lock):
		match := gemLockRubyRE.FindSubmatch(lock)
		generator.rubyDeclared = string(match[1]) + " (Gemfile.lock)"
		generator.ruby = rubyVersionFileRE.FindStringSubmatch(string(match[1]))[1]
	}
	switch {
	case generator.ruby == "":
		generator.ruby = jekyllDefaultRuby
	case !slices.Contains(jekyllRubyVersions, generator.ruby):
		generator.versionIssue = generator.rubyDeclared + " asks for Ruby " + generator.ruby + "; the Jekyll recipe builds with Ruby 3.1 to 3.4, so it uses " + jekyllDefaultRuby
		generator.ruby = jekyllDefaultRuby
	}
	generator.version = generator.ruby
	if generator.versionIssue != "" {
		generator.note(name, generator.versionIssue)
	} else {
		generator.note(name, "Ruby "+generator.ruby+" builds the site")
	}
	if hasLock {
		if match := gemLockPlatformRE.FindSubmatch(lock); match != nil {
			linux := false
			for _, platform := range strings.Fields(string(match[1])) {
				linux = linux || platform == "ruby" || strings.Contains(platform, "linux")
			}
			if !linux {
				generator.addLinuxPlatform = true
				generator.note("Gemfile.lock", "Gemfile.lock lists no Linux platform; the build adds one before installing")
			}
		}
	}
	return generator, true
}

// The Python site generators install from the site's own requirements when
// it has any. A root that declares nothing installs the releases below, each
// pinned, for the theme and plugins its configuration names; a plugin not in
// this table has no package the recipe can be sure of, so the recipe refuses
// and asks for a requirements file instead of guessing.
var pythonSitePackages = map[string]string{
	"mkdocs": "mkdocs==1.6.1", "mkdocs-material": "mkdocs-material==9.7.7", "zensical": "zensical==0.0.65",
	"pelican": "pelican==4.12.0", "markdown": "markdown==3.10.3",
	"sphinx-rtd-theme": "sphinx-rtd-theme==3.1.0", "furo": "furo==2025.12.19",
	"pydata-sphinx-theme": "pydata-sphinx-theme==0.21.0", "sphinx-book-theme": "sphinx-book-theme==1.4.0",
	"myst-parser": "myst-parser==5.1.0", "sphinx-copybutton": "sphinx-copybutton==0.5.2", "sphinx-design": "sphinx-design==0.7.0",
	"sphinxcontrib-mermaid": "sphinxcontrib-mermaid==2.1.1", "sphinx-tabs": "sphinx-tabs==3.5.0", "sphinx-togglebutton": "sphinx-togglebutton==0.4.5",
	"mkdocs-minify-plugin": "mkdocs-minify-plugin==0.8.0", "mkdocs-redirects": "mkdocs-redirects==1.2.3",
	"mkdocs-awesome-pages-plugin": "mkdocs-awesome-pages-plugin==2.10.1", "mkdocs-macros-plugin": "mkdocs-macros-plugin==1.5.0",
	"mkdocs-git-revision-date-localized-plugin": "mkdocs-git-revision-date-localized-plugin==1.6.0", "mkdocs-glightbox": "mkdocs-glightbox==0.5.2",
	"mkdocs-mermaid2-plugin": "mkdocs-mermaid2-plugin==1.2.3", "mkdocs-rss-plugin": "mkdocs-rss-plugin==1.19.0",
}

// sphinxRelease is the Sphinx release a root with no requirements installs:
// the newest one the interpreter family runs.
func sphinxRelease(python string) string {
	switch python {
	case "3.10":
		return "sphinx==8.1.3"
	case "3.11":
		return "sphinx==9.0.4"
	}
	return "sphinx==9.1.0"
}

// mkdocsPlugins maps the plugin names mkdocs.yml uses to their packages;
// "" is a plugin MkDocs or Material for MkDocs provides itself.
var mkdocsPlugins = map[string]string{
	"search": "", "blog": "", "tags": "", "offline": "", "privacy": "", "info": "", "group": "", "meta": "",
	"optimize": "", "typeset": "", "projects": "",
	"minify": "mkdocs-minify-plugin", "redirects": "mkdocs-redirects", "awesome-pages": "mkdocs-awesome-pages-plugin",
	"macros": "mkdocs-macros-plugin", "git-revision-date-localized": "mkdocs-git-revision-date-localized-plugin",
	"glightbox": "mkdocs-glightbox", "mermaid2": "mkdocs-mermaid2-plugin", "rss": "mkdocs-rss-plugin",
}

// mkdocsExtensionModules are Markdown extension packages mkdocs.yml names by
// module; Python-Markdown's own and PyMdown's (a Material dependency) need
// nothing more.
var mkdocsExtensionPrefixes = []string{"pymdownx.", "admonition", "abbr", "attr_list", "def_list", "footnotes", "md_in_html",
	"tables", "toc", "codehilite", "fenced_code", "meta", "nl2br", "sane_lists", "smarty", "wikilinks", "extra", "legacy_attrs", "material."}

var (
	mkdocsListHeadRE = func(key string) *regexp.Regexp { return regexp.MustCompile(`(?m)^` + key + `\s*:\s*$`) }
	mkdocsListItemRE = regexp.MustCompile(`^\s+-\s+['"]?([A-Za-z0-9_.-]+)['"]?\s*:?`)
	mkdocsThemeRE    = regexp.MustCompile(`(?m)^theme\s*:\s*['"]?([A-Za-z0-9_-]+)['"]?\s*$`)
	mkdocsThemeName  = regexp.MustCompile(`(?m)^theme\s*:\s*\n(?:\s+.*\n)*?\s+name\s*:\s*['"]?([A-Za-z0-9_-]+)`)
	mkdocsSiteDirRE  = siteYAMLKeyRE("site_dir")
)

// siteYAMLKeyRE reads a top-level scalar of a YAML file by line.
func siteYAMLKeyRE(key string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^` + key + `\s*:\s*['"]?([^'"#\n]*?)['"]?\s*(?:#.*)?$`)
}

// yamlBlockList reads the items of a top-level block list (`plugins:` then
// `  - search`), stopping at the next top-level key; a !!python tag or an
// anchor elsewhere in the file costs nothing.
func yamlBlockList(content []byte, key string) []string {
	location := mkdocsListHeadRE(key).FindIndex(content)
	if location == nil {
		return nil
	}
	var items []string
	for _, line := range strings.Split(string(content[location[1]:]), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if line[0] != ' ' && line[0] != '\t' && line[0] != '-' {
			break
		}
		if match := mkdocsListItemRE.FindStringSubmatch(line); match != nil && len(items) < 64 {
			items = append(items, match[1])
		}
	}
	return items
}

func mkdocsSite(tree siteTree) (siteGenerator, bool) {
	name, content := firstSiteFile(tree, "mkdocs.yml", "mkdocs.yaml")
	if name == "" {
		return siteGenerator{}, false
	}
	generator := siteGenerator{name: "mkdocs", recipe: "python", config: name, build: mkdocsBuildCommand, output: "site"}
	if match := mkdocsSiteDirRE.FindSubmatch(content); match != nil {
		generator.output = siteOutput(string(match[1]), "site")
	}
	generator.note(name, "MkDocs configuration")
	packages := []string{"mkdocs"}
	theme := ""
	if match := mkdocsThemeRE.FindSubmatch(content); match != nil {
		theme = string(match[1])
	} else if match := mkdocsThemeName.FindSubmatch(content); match != nil {
		theme = string(match[1])
	}
	unknown := []string{}
	switch theme {
	case "", "mkdocs", "readthedocs":
	case "material":
		packages = append(packages, "mkdocs-material")
	default:
		unknown = append(unknown, "theme "+theme)
	}
	for _, plugin := range yamlBlockList(content, "plugins") {
		packageName, known := mkdocsPlugins[plugin]
		switch {
		case !known:
			unknown = append(unknown, "plugin "+plugin)
		case packageName != "":
			packages = append(packages, packageName)
		}
		if strings.HasPrefix(plugin, "git-") {
			generator.needsGit = true
		}
	}
	for _, extension := range yamlBlockList(content, "markdown_extensions") {
		builtin := false
		for _, prefix := range mkdocsExtensionPrefixes {
			builtin = builtin || extension == strings.TrimSuffix(prefix, ".") || strings.HasPrefix(extension, prefix)
		}
		if strings.HasPrefix(extension, "pymdownx.") && !slices.Contains(packages, "mkdocs-material") {
			packages = append(packages, "pymdown-extensions")
		}
		if !builtin {
			unknown = append(unknown, "Markdown extension "+extension)
		}
	}
	generator.pythonSiteInstall(tree, "mkdocs", packages, unknown)
	return generator, true
}

func zensicalSite(tree siteTree) (siteGenerator, bool) {
	content, ok := tree.read("zensical.toml")
	if !ok {
		return siteGenerator{}, false
	}
	config := readSiteConfig("zensical.toml", content)
	generator := siteGenerator{name: "zensical", recipe: "python", config: "zensical.toml", build: zensicalBuildComand,
		output: siteOutput(config.text("project.site_dir"), "site")}
	generator.note("zensical.toml", "Zensical configuration")
	generator.pythonSiteInstall(tree, "zensical", []string{"zensical"}, nil)
	return generator, true
}

var (
	pelicanPathRE    = regexp.MustCompile(`(?m)^PATH\s*=\s*['"]([^'"]+)['"]`)
	pelicanOutputRE  = regexp.MustCompile(`(?m)^OUTPUT_PATH\s*=\s*['"]([^'"]+)['"]`)
	pelicanPluginsRE = regexp.MustCompile(`(?m)^PLUGINS\s*=\s*\[\s*['"]`)
)

func pelicanSite(tree siteTree) (siteGenerator, bool) {
	content, ok := tree.read("pelicanconf.py")
	if !ok {
		return siteGenerator{}, false
	}
	settings := "pelicanconf.py"
	if tree.file("publishconf.py") {
		settings = "publishconf.py"
	}
	source := "content"
	if match := pelicanPathRE.FindSubmatch(content); match != nil {
		source = siteOutput(string(match[1]), "content")
	}
	output := "output"
	if match := pelicanOutputRE.FindSubmatch(content); match != nil {
		output = siteOutput(string(match[1]), "output")
	}
	generator := siteGenerator{name: "pelican", recipe: "python", config: "pelicanconf.py", output: output,
		build: "pelican " + source + " -o " + output + " -s " + settings}
	generator.note("pelicanconf.py", "Pelican settings; the build uses "+settings)
	var unknown []string
	if pelicanPluginsRE.Match(content) {
		unknown = append(unknown, "PLUGINS")
	}
	generator.pythonSiteInstall(tree, "pelican", []string{"pelican", "markdown"}, unknown)
	return generator, true
}

// sphinxConfigPlaces are where a Sphinx project keeps conf.py relative to
// the root that builds it.
var sphinxConfigPlaces = []string{"docs", "doc", "documentation", "docs/source", "doc/source", "source", "."}

var (
	sphinxThemeRE      = regexp.MustCompile(`(?m)^html_theme\s*=\s*['"]([A-Za-z0-9_.-]+)['"]`)
	sphinxExtensionsRE = regexp.MustCompile(`(?ms)^extensions\s*=\s*\[(.*?)\]`)
	pythonStringRE     = regexp.MustCompile(`['"]([A-Za-z0-9_.-]+)['"]`)
	sphinxMarkerRE     = regexp.MustCompile(`(?m)^(?:extensions|html_theme|master_doc|root_doc|project)\s*=`)
)

// sphinxThemes and sphinxExtensions map conf.py's names to their packages;
// "" is part of Sphinx.
var sphinxThemes = map[string]string{
	"alabaster": "", "classic": "", "sphinxdoc": "", "scrolls": "", "agogo": "", "traditional": "", "nature": "",
	"haiku": "", "pyramid": "", "bizstyle": "", "basic": "",
	"sphinx_rtd_theme": "sphinx-rtd-theme", "furo": "furo", "pydata_sphinx_theme": "pydata-sphinx-theme",
	"sphinx_book_theme": "sphinx-book-theme",
}

var sphinxExtensions = map[string]string{
	"myst_parser": "myst-parser", "sphinx_copybutton": "sphinx-copybutton", "sphinx_design": "sphinx-design",
	"sphinxcontrib.mermaid": "sphinxcontrib-mermaid", "sphinx_tabs.tabs": "sphinx-tabs", "sphinx_togglebutton": "sphinx-togglebutton",
	"sphinx_rtd_theme": "sphinx-rtd-theme",
}

func sphinxSite(tree siteTree) (siteGenerator, bool) {
	source, content := "", []byte(nil)
	if rtd, ok := readTheDocsConfig(tree); ok && rtd.sphinx != "" && path.Base(rtd.sphinx) == "conf.py" {
		if text, found := tree.read(rtd.sphinx); found {
			source, content = path.Dir(rtd.sphinx), text
		}
	}
	for _, place := range sphinxConfigPlaces {
		if content != nil {
			break
		}
		text, ok := tree.read(path.Join(place, "conf.py"))
		if !ok {
			continue
		}
		index := false
		for _, name := range []string{"index.rst", "index.md", "index.txt", "contents.rst"} {
			index = index || tree.file(path.Join(place, name))
		}
		if index || sphinxMarkerRE.Match(text) && strings.Contains(string(text), "sphinx") {
			source, content = place, text
		}
	}
	if content == nil {
		return siteGenerator{}, false
	}
	output := path.Join(source, "_build", "html")
	generator := siteGenerator{name: "sphinx", recipe: "python", config: path.Join(source, "conf.py"), output: path.Clean(output),
		build: "sphinx-build -b html " + source + " " + path.Clean(output), sphinxSource: source}
	generator.note(generator.config, "Sphinx configuration")
	packages := []string{"sphinx"}
	var unknown []string
	if match := sphinxThemeRE.FindSubmatch(content); match != nil {
		packageName, known := sphinxThemes[string(match[1])]
		switch {
		case !known:
			unknown = append(unknown, "html_theme "+string(match[1]))
		case packageName != "":
			packages = append(packages, packageName)
		}
	}
	if match := sphinxExtensionsRE.FindSubmatch(content); match != nil {
		for _, extension := range pythonStringRE.FindAllSubmatch(match[1], 32) {
			name := string(extension[1])
			if strings.HasPrefix(name, "sphinx.ext.") {
				continue
			}
			if packageName, known := sphinxExtensions[name]; known {
				if !slices.Contains(packages, packageName) {
					packages = append(packages, packageName)
				}
				continue
			}
			unknown = append(unknown, "extension "+name)
		}
	}
	generator.pythonSiteInstall(tree, "sphinx", packages, unknown)
	return generator, true
}

// readTheDocs is what .readthedocs.yaml says about the documentation build.
type readTheDocs struct {
	sphinx, mkdocs string
	python         string
	requirements   []string
	// packages are `pip install <path>[extras]` installs of the project.
	packages []string
}

var readTheDocsInstallRE = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

func readTheDocsConfig(tree siteTree) (readTheDocs, bool) {
	_, content := firstSiteFile(tree, ".readthedocs.yaml", ".readthedocs.yml")
	if content == nil {
		return readTheDocs{}, false
	}
	var document struct {
		Build struct {
			Tools map[string]any `yaml:"tools"`
		} `yaml:"build"`
		Sphinx struct {
			Configuration string `yaml:"configuration"`
		} `yaml:"sphinx"`
		MkDocs struct {
			Configuration string `yaml:"configuration"`
		} `yaml:"mkdocs"`
		Python struct {
			Install []struct {
				Requirements      string   `yaml:"requirements"`
				Method            string   `yaml:"method"`
				Path              string   `yaml:"path"`
				ExtraRequirements []string `yaml:"extra_requirements"`
			} `yaml:"install"`
		} `yaml:"python"`
	}
	if yaml.Unmarshal(content, &document) != nil {
		return readTheDocs{}, false
	}
	config := readTheDocs{}
	if safeRelativePath(document.Sphinx.Configuration) && readTheDocsInstallRE.MatchString(document.Sphinx.Configuration) {
		config.sphinx = path.Clean(document.Sphinx.Configuration)
	}
	if safeRelativePath(document.MkDocs.Configuration) && readTheDocsInstallRE.MatchString(document.MkDocs.Configuration) {
		config.mkdocs = path.Clean(document.MkDocs.Configuration)
	}
	if python, ok := document.Build.Tools["python"].(string); ok && pythonRecipeVersionRE.MatchString(python) {
		config.python = python
	}
	for _, install := range document.Python.Install {
		switch {
		case install.Requirements != "" && safeRelativePath(install.Requirements) && readTheDocsInstallRE.MatchString(install.Requirements):
			config.requirements = append(config.requirements, path.Clean(install.Requirements))
		case install.Method == "pip" && (install.Path == "." || (safeRelativePath(install.Path) && readTheDocsInstallRE.MatchString(install.Path))):
			target := install.Path
			if target != "." {
				target = "./" + path.Clean(target)
			}
			extras := []string{}
			for _, extra := range install.ExtraRequirements {
				if simpleWordRE.MatchString(strings.ToLower(extra)) {
					extras = append(extras, extra)
				}
			}
			if len(extras) > 0 {
				target += "[" + strings.Join(extras, ",") + "]"
			}
			config.packages = append(config.packages, target)
		}
	}
	return config, true
}

// pythonDocsRequirements are the files a documentation site conventionally
// keeps its own requirements in, apart from the project's.
var pythonDocsRequirements = []string{
	"docs/requirements.txt", "doc/requirements.txt", "requirements-docs.txt", "docs-requirements.txt",
	"requirements/docs.txt", "docs/requirements-docs.txt",
}

// pythonSiteInstall settles what a Python site installs, recorded on the
// generator for detection's evidence; the recipe decides the same way when
// it prepares the build (build_site.go).
func (g *siteGenerator) pythonSiteInstall(tree siteTree, generator string, packages, unknown []string) {
	g.pythonPackages = packages
	if rtd, ok := readTheDocsConfig(tree); ok {
		g.pythonDeclared = rtd.python
		if len(rtd.requirements) > 0 || len(rtd.packages) > 0 {
			g.note(".readthedocs.yaml", "installs what .readthedocs.yaml lists for the documentation build")
			return
		}
	}
	if g.sphinxSource != "" && g.sphinxSource != "." {
		if tree.file(path.Join(g.sphinxSource, "requirements.txt")) {
			g.note(path.Join(g.sphinxSource, "requirements.txt"), "documentation requirements")
			return
		}
	}
	for _, name := range pythonDocsRequirements {
		if tree.file(name) {
			g.note(name, "documentation requirements")
			return
		}
	}
	for _, manifest := range []string{"uv.lock", "poetry.lock", "requirements.txt", "pyproject.toml"} {
		if content, ok := tree.read(manifest); ok {
			if manifest != "pyproject.toml" || strings.Contains(strings.ToLower(string(content)), generator) {
				g.note(manifest, "the site's Python dependencies")
				return
			}
		}
	}
	if len(unknown) > 0 {
		g.issue = g.config + " names " + strings.Join(unknown, ", ") + ", and no requirements file says which packages provide them; commit a requirements.txt that lists them"
		return
	}
	g.unpinned = true
	g.note(g.config, "no requirements file; the build installs "+strings.Join(pythonSitePackageList(packages, g.pythonDeclared), " "))
}

// pythonSitePackageList pins the packages a site with no requirements
// installs.
func pythonSitePackageList(packages []string, python string) []string {
	pinned := make([]string, 0, len(packages))
	for _, name := range packages {
		if name == "sphinx" {
			pinned = append(pinned, sphinxRelease(python))
			continue
		}
		if release := pythonSitePackages[name]; release != "" {
			pinned = append(pinned, release)
		}
	}
	return pinned
}

var lumeDestRE = regexp.MustCompile(`\bdest\s*:\s*['"` + "`" + `]([^'"` + "`" + `]+)['"` + "`" + `]`)

// lumeSite recognises a Lume site from deno.json's imports: the site is the
// build task's output, served by nginx, not a server.
func lumeSite(tree siteTree, config denoConfig) (siteGenerator, bool) {
	lume := false
	for key, value := range config.Imports {
		lume = lume || key == "lume/" || key == "lume" || strings.Contains(value, "deno.land/x/lume") || strings.Contains(value, "jsr:@lume/lume")
	}
	if !lume {
		return siteGenerator{}, false
	}
	generator := siteGenerator{name: "lume", recipe: "deno", config: "deno.json", output: "_site"}
	switch {
	case config.task("build") != "":
		generator.build = "deno task build"
	case config.task("lume") != "":
		generator.build = "deno task lume"
	default:
		generator.issue = "deno.json imports Lume but defines no build or lume task; add \"build\": \"deno task lume\" as Lume's own template does"
	}
	for _, name := range []string{"_config.ts", "_config.js"} {
		if content, ok := tree.read(name); ok {
			if match := lumeDestRE.FindSubmatch(content); match != nil {
				generator.output = siteOutput(string(match[1]), "_site")
			}
			generator.note(name, "Lume configuration; the build writes "+generator.output+"/")
			break
		}
	}
	return generator, true
}
