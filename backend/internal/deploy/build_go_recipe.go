package deploy

import (
	"errors"
	"fmt"
	"go/version"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

// What the Go recipe reads from a module beyond its main package and its
// toolchain: which C code it compiles, where its dependencies come from
// (vendor/, go.sum, a private host, a workspace or a local replacement), what
// it generates before compiling, and the front-end build it embeds. Detection
// and the recipe both call planGoBuild over the same files, so what the
// candidate says about a build is what the build does, and its refusals are
// said before Deploy. Everything is read as bounded data; nothing in the
// repository runs on the host.

var cgoEnabledCommandRE = regexp.MustCompile(`(^|[\s;&])CGO_ENABLED\s*=\s*['"]?1(['"]|[\s;&]|$)`)

// compiledDynamicAlpine is the Alpine release a Go build that links a system
// library dynamically builds and runs on. The build image and the runtime
// must be the same release, or the binary meets libraries it was not linked
// against; golang:<family>-alpine<release> pins the build side of that pair.
const compiledDynamicAlpine = "3.24"

// goCGOModule is a dependency whose Go package is a cgo binding with no
// pure-Go path: built with CGO_ENABLED=0 it compiles a stub, or not at all.
// go-sqlite3's stub builds and fails its first query at runtime ("Binary was
// compiled with 'CGO_ENABLED=0', go-sqlite3 requires cgo to work").
type goCGOModule struct {
	module, label string
	// links are the system libraries the binding links, as goCgoLinks names
	// them; empty means it compiles the C it needs from its own source.
	links []string
	// tags are the build tags it needs on Alpine: confluent-kafka-go links
	// its bundled librdkafka for musl under `musl`.
	tags        []string
	alternative string
}

var goCGOModules = []goCGOModule{
	{module: "github.com/mattn/go-sqlite3", label: "SQLite", alternative: "modernc.org/sqlite, or github.com/glebarez/sqlite for GORM"},
	{module: "github.com/mutecomm/go-sqlcipher", label: "SQLCipher"},
	{module: "github.com/confluentinc/confluent-kafka-go", label: "librdkafka", tags: []string{"musl"}, alternative: "github.com/twmb/franz-go or github.com/segmentio/kafka-go"},
	{module: "github.com/h2non/bimg", label: "libvips", links: []string{"pkg-config:vips"}},
	{module: "github.com/davidbyttow/govips", label: "libvips", links: []string{"pkg-config:vips"}},
	{module: "gopkg.in/gographics/imagick.v3", label: "ImageMagick 7", links: []string{"pkg-config:MagickWand"}},
	{module: "github.com/otiai10/gosseract", label: "Tesseract", links: []string{"lib:tesseract", "lib:lept", "lib:stdc++"}},
}

// goSystemLibrary is the Alpine packages that provide a library a cgo file
// links: build packages for the build stage, and runtime packages when the
// library is linked dynamically. A library without runtime packages is
// linked into a static binary.
type goSystemLibrary struct {
	build, runtime []string
}

// goSystemLibraries map the names in `#cgo pkg-config:` and `#cgo LDFLAGS:
// -l` to Alpine packages. musl's own libraries need none.
var goSystemLibraries = func() map[string]goSystemLibrary {
	table := map[string]goSystemLibrary{}
	add := func(library goSystemLibrary, names ...string) {
		for _, name := range names {
			table[name] = library
		}
	}
	add(goSystemLibrary{}, "lib:m", "lib:c", "lib:pthread", "lib:dl", "lib:rt", "lib:resolv", "lib:util")
	add(goSystemLibrary{build: []string{"g++"}}, "lib:stdc++")
	add(goSystemLibrary{build: []string{"sqlite-dev", "sqlite-static"}}, "lib:sqlite3", "pkg-config:sqlite3")
	add(goSystemLibrary{build: []string{"openssl-dev", "openssl-libs-static"}},
		"lib:ssl", "lib:crypto", "pkg-config:openssl", "pkg-config:libssl", "pkg-config:libcrypto")
	add(goSystemLibrary{build: []string{"zlib-dev", "zlib-static"}}, "lib:z", "pkg-config:zlib")
	add(goSystemLibrary{build: []string{"vips-dev"}, runtime: []string{"vips"}}, "lib:vips", "pkg-config:vips", "pkg-config:vips-cpp")
	add(goSystemLibrary{build: []string{"imagemagick-dev"}, runtime: []string{"imagemagick-libs"}},
		"pkg-config:MagickWand", "pkg-config:MagickCore", "pkg-config:MagickWand-7.Q16HDRI", "pkg-config:MagickCore-7.Q16HDRI",
		"lib:MagickWand-7.Q16HDRI", "lib:MagickCore-7.Q16HDRI")
	add(goSystemLibrary{build: []string{"tesseract-ocr-dev"}, runtime: []string{"tesseract-ocr"}}, "lib:tesseract", "pkg-config:tesseract")
	add(goSystemLibrary{build: []string{"leptonica-dev"}, runtime: []string{"leptonica"}}, "lib:lept", "lib:leptonica", "pkg-config:lept")
	add(goSystemLibrary{build: []string{"libpq-dev"}, runtime: []string{"libpq"}}, "lib:pq", "pkg-config:libpq")
	add(goSystemLibrary{build: []string{"librdkafka-dev"}, runtime: []string{"librdkafka"}}, "lib:rdkafka", "pkg-config:rdkafka")
	add(goSystemLibrary{build: []string{"zeromq-dev"}, runtime: []string{"libzmq"}}, "lib:zmq", "pkg-config:libzmq")
	add(goSystemLibrary{build: []string{"libpcap-dev"}, runtime: []string{"libpcap"}}, "lib:pcap", "pkg-config:libpcap")
	add(goSystemLibrary{build: []string{"libwebp-dev"}, runtime: []string{"libwebp"}}, "lib:webp", "pkg-config:libwebp")
	add(goSystemLibrary{build: []string{"libheif-dev"}, runtime: []string{"libheif"}}, "lib:heif", "pkg-config:libheif")
	return table
}()

var (
	goCgoDirectiveRE = regexp.MustCompile(`^#cgo\s+((?:[!\w,.]+\s+)*?)(pkg-config|LDFLAGS)\s*:\s*(.*)$`)
	goEmbedLineRE    = regexp.MustCompile(`(?m)^//go:embed\s+(.+)$`)
)

// goEmbedsKept bounds the embed patterns and templ components one module
// records.
const goEmbedsKept = 64

// goCgoLinks reads the `#cgo pkg-config:` and `#cgo LDFLAGS: -l` lines of a
// cgo file's preamble whose constraint admits linux.
func goCgoLinks(content []byte) []string {
	var links []string
	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(raw), "/*"))
		match := goCgoDirectiveRE.FindStringSubmatch(line)
		if match == nil || !goCgoDirectiveForLinux(strings.Fields(match[1])) {
			continue
		}
		for _, field := range strings.Fields(match[3]) {
			switch {
			case match[2] == "pkg-config" && !strings.HasPrefix(field, "-"):
				links = append(links, "pkg-config:"+field)
			case match[2] == "LDFLAGS" && strings.HasPrefix(field, "-l") && len(field) > 2:
				links = append(links, "lib:"+field[2:])
			}
		}
		if len(links) > 64 {
			break
		}
	}
	return links
}

// goCgoDirectiveForLinux evaluates a #cgo line's constraint: options
// separated by spaces, each a comma-separated conjunction of terms.
func goCgoDirectiveForLinux(options []string) bool {
	if len(options) == 0 {
		return true
	}
	for _, option := range options {
		satisfied := true
		for _, term := range strings.Split(option, ",") {
			negated := strings.HasPrefix(term, "!")
			term = strings.TrimPrefix(term, "!")
			holds := term == "linux" || term == "unix" || term == "cgo" || term == "gc" || term == runtime.GOARCH
			if holds == negated {
				satisfied = false
			}
		}
		if satisfied {
			return true
		}
	}
	return false
}

// goEmbedPatterns reads a file's //go:embed patterns, quoted or bare.
func goEmbedPatterns(content []byte) []string {
	if !strings.Contains(string(content), "//go:embed") {
		return nil
	}
	var patterns []string
	for _, match := range goEmbedLineRE.FindAllStringSubmatch(string(content), goEmbedsKept) {
		rest := strings.TrimSpace(match[1])
		for rest != "" && len(patterns) < goEmbedsKept {
			var pattern string
			switch rest[0] {
			case '"', '`':
				end := strings.IndexByte(rest[1:], rest[0])
				if end < 0 {
					rest = ""
					continue
				}
				pattern, rest = rest[1:end+1], strings.TrimSpace(rest[end+2:])
				if unquoted, err := strconv.Unquote(`"` + pattern + `"`); err == nil {
					pattern = unquoted
				}
			default:
				pattern, rest, _ = strings.Cut(rest, " ")
				rest = strings.TrimSpace(rest)
			}
			if pattern != "" {
				patterns = append(patterns, pattern)
			}
		}
	}
	return patterns
}

// goModFile is the inert view of a go.mod or go.work: the directives the
// recipe acts on.
type goModFile struct {
	module, goVersion, toolchain string
	requires                     []goRequirement
	replaces                     []goReplacement
	tools                        []string
	uses                         []string
}

type goRequirement struct {
	path, version string
	indirect      bool
}

type goReplacement struct {
	path, version, target, targetVersion string
}

// local says the replacement is a directory rather than another module.
func (r goReplacement) local() bool {
	return r.target == "." || r.target == ".." || strings.HasPrefix(r.target, "./") || strings.HasPrefix(r.target, "../") ||
		strings.HasPrefix(r.target, "/") || strings.HasPrefix(r.target, `.\`) || strings.HasPrefix(r.target, `..\`)
}

// parseGoMod reads go.mod and go.work directives, single-line and in blocks.
// An unusual layout yields fewer facts, never wrong ones.
func parseGoMod(content []byte) goModFile {
	var file goModFile
	block := ""
	for _, raw := range strings.Split(string(content), "\n") {
		line, comment, _ := strings.Cut(raw, "//")
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		verb := block
		if block == "" {
			verb, fields = fields[0], fields[1:]
			if len(fields) == 1 && fields[0] == "(" {
				block = verb
				continue
			}
		} else if fields[0] == ")" {
			block = ""
			continue
		}
		for index := range fields {
			fields[index] = strings.Trim(fields[index], "\"`")
		}
		switch verb {
		case "module":
			if len(fields) == 1 {
				file.module = fields[0]
			}
		case "go":
			if len(fields) == 1 {
				file.goVersion = fields[0]
			}
		case "toolchain":
			if len(fields) == 1 {
				file.toolchain = fields[0]
			}
		case "require":
			if len(fields) == 2 && len(file.requires) < 4096 {
				file.requires = append(file.requires, goRequirement{path: fields[0], version: fields[1],
					indirect: strings.Contains(comment, "indirect")})
			}
		case "replace":
			arrow := slices.Index(fields, "=>")
			if arrow < 1 || arrow > 2 || arrow+1 >= len(fields) || len(file.replaces) >= 256 {
				continue
			}
			replacement := goReplacement{path: fields[0], target: fields[arrow+1]}
			if arrow == 2 {
				replacement.version = fields[1]
			}
			if arrow+2 < len(fields) {
				replacement.targetVersion = fields[arrow+2]
			}
			file.replaces = append(file.replaces, replacement)
		case "tool":
			if len(fields) == 1 && len(file.tools) < 256 {
				file.tools = append(file.tools, fields[0])
			}
		case "use":
			if len(fields) == 1 && len(file.uses) < 256 {
				file.uses = append(file.uses, fields[0])
			}
		}
	}
	return file
}

// requirement is the version go.mod requires of a module, or empty.
func (f goModFile) requirement(module string) string {
	for _, required := range f.requires {
		if required.path == module || strings.HasPrefix(required.path, module+"/") {
			return required.version
		}
	}
	return ""
}

// replacementOf is what replaces a module at a version, if anything.
func (f goModFile) replacementOf(module, version string) (goReplacement, bool) {
	for _, replacement := range f.replaces {
		if replacement.path == module && (replacement.version == "" || replacement.version == version) {
			return replacement, true
		}
	}
	return goReplacement{}, false
}

// goSumMissing lists the required modules go.sum has no go.mod hash for.
// The go command needs one for every module in the requirement graph before
// it builds anything ("missing go.sum entry"); a local replacement needs
// none, and a replacement module needs its own.
func goSumMissing(module goModFile, sum []byte) []string {
	hashes := map[string]bool{}
	for _, line := range strings.Split(string(sum), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 {
			hashes[fields[0]+" "+strings.TrimSuffix(fields[1], "/go.mod")] = true
		}
	}
	var missing []string
	for _, required := range module.requires {
		path, version := required.path, required.version
		if replacement, ok := module.replacementOf(path, version); ok {
			if replacement.local() {
				continue
			}
			path, version = replacement.target, replacement.targetVersion
		}
		if !hashes[path+" "+version] {
			missing = append(missing, path)
		}
	}
	return missing
}

// goModuleOwner is the host and owner a module path shares with the
// modules of the same account: github.com/acme for github.com/acme/app.
func goModuleOwner(module string) string {
	segments := strings.Split(module, "/")
	if len(segments) < 3 || !strings.Contains(segments[0], ".") {
		return ""
	}
	return segments[0] + "/" + segments[1]
}

// goOwnerModules are the requirements from the main module's own account,
// the ones a private repository's build most often cannot fetch anonymously.
// A module a local replacement or the go.work (local) provides is in the
// checkout, not fetched.
func goOwnerModules(module goModFile, local []string) []string {
	owner := goModuleOwner(module.module)
	if owner == "" {
		return nil
	}
	var owned []string
	for _, required := range module.requires {
		if strings.HasPrefix(required.path, owner+"/") && required.path != module.module && !slices.Contains(local, required.path) {
			if replacement, ok := module.replacementOf(required.path, required.version); ok && replacement.local() {
				continue
			}
			owned = append(owned, required.path)
		}
	}
	return owned
}

// goPrivateTokenVariable is the build variable that lets the install fetch
// modules from the source's own account: a token for the Git host, bound to
// the install step, never written anywhere but that step's secret mount.
const goPrivateTokenVariable = "GIT_TOKEN"

// goBuildContext is where a module builds from: its own directory, or an
// ancestor that a go.work or a local replacement reaches into.
type goBuildContext struct {
	// dir is the context relative to the checkout ("" for the module itself)
	// and module the module's directory inside it ("" for the same).
	dir, module string
	// workspace says a go.work at dir uses the module; replaceOnly says
	// the context widened for local replacements alone.
	workspace, replaceOnly bool
	work                   goModFile
	// workModules are the module paths of the directories the go.work
	// uses, which the build finds in the checkout rather than fetching.
	workModules []string
	// replaces are the local replacement targets, relative to the checkout.
	replaces []string
}

// goContextFor finds the build context of the module at root inside the
// checkout at boundary: the directory that holds the module and everything
// it builds from — with a go.work that uses it, the go.work and every module
// it uses; otherwise the targets of its local replace directives. The go
// command uses the first go.work above the module, and one that does not
// list the module is left out, which is what a build of the module alone
// did. A target outside the checkout is refused; the context still covers
// the rest, for the facts detection records.
func goContextFor(boundary, root string, module goModFile) (goBuildContext, error) {
	context := goBuildContext{}
	rel := checkoutPath(boundary, root)
	anchors := []string{rel}
	var refusal error
	keep := func(target string, err error) {
		if err != nil {
			if refusal == nil {
				refusal = err
			}
			return
		}
		anchors = append(anchors, target)
	}
	if workRel, work, ok := goWorkFor(boundary, root, rel); ok {
		context.workspace, context.work = true, work
		anchors = append(anchors, workRel)
		for _, use := range work.uses {
			target, err := goWorkUseTarget(workRel, use)
			keep(target, err)
			if err != nil || len(context.workModules) >= 64 {
				continue
			}
			if used, err := readContainedRegular(filepath.Join(boundary, filepath.FromSlash(target)), "go.mod", 64<<10); err == nil {
				if modulePath := goModulePath(used); modulePath != "" {
					context.workModules = append(context.workModules, modulePath)
				}
			}
		}
		for _, replacement := range work.replaces {
			if replacement.local() {
				target, err := goReplaceTarget(workRel, replacement)
				keep(target, err)
				if err == nil {
					context.replaces = append(context.replaces, target)
				}
			}
		}
	}
	for _, replacement := range module.replaces {
		if replacement.local() {
			target, err := goReplaceTarget(rel, replacement)
			keep(target, err)
			if err == nil {
				context.replaces = append(context.replaces, target)
			}
		}
	}
	common := rel
	for _, anchor := range anchors {
		for !underRoot(anchor, common) {
			if common = path.Dir(common); common == "." {
				common = ""
			}
		}
	}
	if common != rel {
		context.dir, context.replaceOnly = rootLabelOf(common), !context.workspace
		context.module = strings.TrimPrefix(strings.TrimPrefix(rel, common), "/")
	}
	return context, refusal
}

// goWorkFor is the go.work the go command would use for the module at root,
// when it lists the module: the first one above it, within the checkout.
func goWorkFor(boundary, root, rel string) (string, goModFile, bool) {
	for dir := root; strings.HasPrefix(dir, boundary); dir = filepath.Dir(dir) {
		if regularExists(dir, "go.work") {
			content, err := readContainedRegular(dir, "go.work", 64<<10)
			if err != nil {
				return "", goModFile{}, false
			}
			work := parseGoMod(content)
			workRel := checkoutPath(boundary, dir)
			for _, use := range work.uses {
				if path.Clean(path.Join(workRel, filepath.ToSlash(use))) == path.Clean(rel) {
					return workRel, work, true
				}
			}
			return "", goModFile{}, false
		}
		if dir == boundary {
			break
		}
	}
	return "", goModFile{}, false
}

// goWorkUseTarget is a go.work use directory relative to the checkout.
func goWorkUseTarget(workRel, use string) (string, error) {
	joined := path.Clean(path.Join(workRel, filepath.ToSlash(use)))
	if strings.HasPrefix(filepath.ToSlash(use), "/") || joined == ".." || strings.HasPrefix(joined, "../") {
		return "", fmt.Errorf("%w: go.work uses %s, which is outside the repository; the build context holds only the checkout", ErrUnsupportedBuilder, use)
	}
	if joined == "." {
		return "", nil
	}
	return joined, nil
}

// goReplaceTarget is a local replacement's directory relative to the
// checkout, refused when it leaves it: the build has only the checkout.
func goReplaceTarget(from string, replacement goReplacement) (string, error) {
	target := filepath.ToSlash(replacement.target)
	if strings.HasPrefix(target, "/") {
		return "", fmt.Errorf("%w: go.mod replaces %s with the absolute path %s, which only exists on the machine that wrote it; point it inside the repository or remove it",
			ErrUnsupportedBuilder, replacement.path, target)
	}
	joined := path.Clean(path.Join(from, target))
	if joined == ".." || strings.HasPrefix(joined, "../") {
		return "", fmt.Errorf("%w: go.mod replaces %s with %s, which is outside the repository; the build context holds only the checkout",
			ErrUnsupportedBuilder, replacement.path, target)
	}
	if joined == "." {
		return "", nil
	}
	return joined, nil
}

// goCGOPlan is what the build needs to compile C: why, the Alpine packages
// of the build stage and of the runtime stage, and the build tags.
type goCGOPlan struct {
	modules, local []string
	command        bool
	build, runtime []string
	unknown        []string
	tags           []string
}

func (p goCGOPlan) enabled() bool { return len(p.modules) > 0 || len(p.local) > 0 || p.command }

// dynamic says a library is linked as a shared object, so the runtime stage
// installs it and both stages share an Alpine release.
func (p goCGOPlan) dynamic() bool { return len(p.runtime) > 0 }

// planGoCGO decides whether the build compiles C: a dependency that is a
// cgo binding, local cgo files with no pure-Go twin, or a build command that
// turns cgo on.
func planGoCGO(module goModFile, packages goModulePackages, buildCommand string) goCGOPlan {
	plan := goCGOPlan{local: packages.cgo, command: cgoEnabledCommandRE.MatchString(buildCommand)}
	links := append([]string(nil), packages.cgoLinks...)
	for _, known := range goCGOModules {
		if module.requirement(known.module) == "" {
			continue
		}
		if replacement, ok := module.replacementOf(known.module, module.requirement(known.module)); ok && replacement.local() {
			continue
		}
		plan.modules = append(plan.modules, known.module)
		links = append(links, known.links...)
		plan.tags = append(plan.tags, known.tags...)
	}
	if !plan.enabled() {
		return plan
	}
	plan.build = []string{"gcc", "musl-dev"}
	for _, link := range uniqueSorted(links) {
		library, ok := goSystemLibraries[link]
		if !ok {
			plan.unknown = append(plan.unknown, link)
			continue
		}
		plan.build = append(plan.build, library.build...)
		plan.runtime = append(plan.runtime, library.runtime...)
	}
	if plan.dynamic() {
		plan.build = append(plan.build, "pkgconf")
	} else if slices.ContainsFunc(links, func(link string) bool { return strings.HasPrefix(link, "pkg-config:") }) {
		plan.build = append(plan.build, "pkgconf")
	}
	plan.build = uniqueOrdered(plan.build)
	plan.runtime = uniqueOrdered(plan.runtime)
	plan.tags = uniqueOrdered(plan.tags)
	return plan
}

// describe names why cgo is on, for evidence and findings.
func (p goCGOPlan) describe() string {
	reasons := []string{}
	for _, name := range p.modules {
		for _, known := range goCGOModules {
			if known.module == name {
				reasons = append(reasons, name+" ("+known.label+")")
			}
		}
	}
	for _, dir := range p.local {
		reasons = append(reasons, goPackageArgument(dir)+" imports \"C\"")
	}
	if p.command {
		reasons = append(reasons, "the build command sets CGO_ENABLED=1")
	}
	return strings.Join(reasons, "; ")
}

func (p goCGOPlan) unknownText() string {
	names := make([]string, 0, len(p.unknown))
	for _, link := range p.unknown {
		kind, name, _ := strings.Cut(link, ":")
		if kind == "lib" {
			names = append(names, "-l"+name)
		} else {
			names = append(names, "pkg-config "+name)
		}
	}
	return strings.Join(names, ", ")
}

func uniqueOrdered(values []string) []string {
	seen := map[string]bool{}
	kept := values[:0:0]
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			kept = append(kept, value)
		}
	}
	return kept
}

// goEmbedResolution is one //go:embed pattern: the directory it names inside
// the module, whether the checkout has it, and the package.json build that
// produces it when it does not.
type goEmbedResolution struct {
	pattern, dir string
	present      bool
	frontend     string
	output       string
}

// goFrontendStage is the Node stage that builds what the module embeds,
// with the same lockfile-driven install the JavaScript recipe runs. Paths
// are relative to the build context.
type goFrontendStage struct {
	root, output, target string
	framework            string
	plan                 nodeInstallPlan
	inputs               []string
}

// resolveGoEmbeds reads each embed pattern's directory in the checkout. A
// directory that is missing, or holds nothing but dotfiles (a .gitkeep in
// an ignored dist/), is looked for among the package.json builds inside the
// module: the package it sits in, or one whose Vite outDir writes it.
func resolveGoEmbeds(moduleRoot string, embeds []goEmbed) []goEmbedResolution {
	var resolved []goEmbedResolution
	seen := map[string]bool{}
	for _, embed := range embeds {
		pattern := strings.TrimPrefix(embed.pattern, "all:")
		literal := []string{}
		for _, segment := range strings.Split(pattern, "/") {
			if strings.ContainsAny(segment, "*?[\\") {
				break
			}
			literal = append(literal, segment)
		}
		if len(literal) == 0 {
			continue
		}
		dir := path.Clean(path.Join(embed.dir, strings.Join(literal, "/")))
		if dir == "." || strings.HasPrefix(dir, "..") || seen[dir] {
			continue
		}
		seen[dir] = true
		resolution := goEmbedResolution{pattern: embed.pattern, dir: dir, present: goEmbedPresent(moduleRoot, dir)}
		if !resolution.present {
			resolution.frontend, resolution.output = goEmbedFrontend(moduleRoot, dir)
		}
		resolved = append(resolved, resolution)
	}
	return resolved
}

// goEmbedPresent says the checkout has something at an embedded path other
// than dotfiles, which a pattern without all: would not match anyway.
func goEmbedPresent(moduleRoot, dir string) bool {
	info, err := os.Lstat(filepath.Join(moduleRoot, filepath.FromSlash(dir)))
	if err != nil {
		return false
	}
	if !info.IsDir() {
		return info.Mode().IsRegular()
	}
	entries, err := os.ReadDir(filepath.Join(moduleRoot, filepath.FromSlash(dir)))
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".") {
			return true
		}
	}
	return false
}

// goEmbedFrontend finds the package.json build that writes dir: the
// package whose directory holds it, else one whose Vite outDir names it.
func goEmbedFrontend(moduleRoot, dir string) (string, string) {
	for ancestor := path.Dir(dir); ; ancestor = path.Dir(ancestor) {
		if goFrontendBuilds(moduleRoot, ancestor) {
			return ancestor, dir
		}
		if ancestor == "." {
			break
		}
	}
	entries := 0
	found, output := "", ""
	errStop := errors.New("stop")
	_ = filepath.WalkDir(moduleRoot, func(current string, entry os.DirEntry, err error) error {
		if entries++; entries > 4000 {
			return errStop
		}
		if err != nil || !entry.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(moduleRoot, current)
		rel = filepath.ToSlash(rel)
		if rel != "." && (goSkippedDirectory(entry.Name()) || entry.Name() == "dist" || entry.Name() == "build" || strings.Count(rel, "/") >= 3) {
			return filepath.SkipDir
		}
		if !goFrontendBuilds(moduleRoot, rel) {
			return nil
		}
		for _, name := range nodeConfigNames("vite") {
			content, err := readContainedRegular(filepath.Join(moduleRoot, filepath.FromSlash(rel)), name, 256<<10)
			if err != nil {
				continue
			}
			if match := viteOutDirRE.FindSubmatch(jsBlankComments(content)); match != nil {
				// An embed inside the output is copied on its own, to where
				// the pattern names it, not the whole output into it.
				if written := path.Clean(path.Join(rel, string(match[1]))); written == dir || underRoot(dir, written) {
					found, output = rel, dir
					return errStop
				}
			}
		}
		return nil
	})
	return found, output
}

// goFrontendBuilds says a directory is a package with a build script.
func goFrontendBuilds(moduleRoot, dir string) bool {
	content, err := readContainedRegular(filepath.Join(moduleRoot, filepath.FromSlash(dir)), "package.json", 512<<10)
	if err != nil {
		return false
	}
	var manifest nodeManifest
	return parseNodeManifest(content, &manifest) && strings.TrimSpace(manifest.Scripts["build"]) != ""
}

// goTemplGenerate is the command that writes templ's generated Go when
// components have none committed: the module's own tool directive, else the
// generator at the runtime's required version, so the two agree.
func goTemplGenerate(module goModFile, packages goModulePackages) (string, string) {
	if len(packages.templMissing) == 0 {
		return "", ""
	}
	if slices.Contains(module.tools, "github.com/a-h/templ/cmd/templ") {
		return "go tool templ generate", "go.mod's tool directive"
	}
	required := module.requirement("github.com/a-h/templ")
	if required == "" || !goModuleVersionRE.MatchString(required) {
		return "", ""
	}
	return "go run github.com/a-h/templ/cmd/templ@" + required + " generate", "github.com/a-h/templ " + required
}

var goModuleVersionRE = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+incompatible)?$`)

// goBuildPlan is everything the recipe renders beyond the version and the
// main package.
type goBuildPlan struct {
	context   goBuildContext
	module    goModFile
	cgo       goCGOPlan
	vendored  bool
	sumAbsent bool
	sumStale  []string
	// sumBytes is how much of go.sum the plan read.
	sumBytes int64
	// owner is the GOPRIVATE pattern when the install has the token.
	owner, ownerHost string
	ownerModules     []string
	templ, templFrom string
	embeds           []goEmbedResolution
	frontend         *goFrontendStage
}

// modFlag is the -mod the build runs with: vendor/ when the module vendors,
// and mod when go.sum cannot describe the requirements, so the go command
// records what it downloads instead of refusing.
func (p goBuildPlan) modFlag() string {
	switch {
	case p.vendored:
		return "-mod=vendor"
	case p.sumAbsent || len(p.sumStale) > 0:
		return "-mod=mod"
	}
	return ""
}

// goEmbedsMain is the main package whose embeds the build must produce: the
// one the recipe compiles. A build command — even the old `go build ./...`,
// which compiles every package — and a module whose command is not chosen
// yet leave it empty, which keeps every embed in the module.
func goEmbedsMain(main string, err error, config BuildPlanConfig) string {
	if err != nil || strings.TrimSpace(config.BuildCommand) != "" {
		return ""
	}
	return main
}

// goSumReadLimit is the largest go.sum the recipe compares with go.mod; a
// larger one is left to the go command, which checks it anyway.
const goSumReadLimit = 16 << 20

// planGoBuild reads what the module at root needs to build inside the
// checkout at boundary, for the main package at main ("" when none is
// chosen), reading at most sumLimit bytes of go.sum. Its error is the
// recipe's first refusal; the plan still carries every fact it could read,
// which detection records so preflight names each problem rather than only
// the first.
func planGoBuild(boundary, root string, content []byte, packages goModulePackages, main string, config BuildPlanConfig, sumLimit int64) (goBuildPlan, error) {
	plan := goBuildPlan{module: parseGoMod(content)}
	var refusal error
	refuse := func(err error) {
		if refusal == nil {
			refusal = err
		}
	}
	context, err := goContextFor(boundary, root, plan.module)
	if err != nil {
		refuse(err)
	}
	plan.context = context
	plan.cgo = planGoCGO(plan.module, packages, config.BuildCommand)
	if len(plan.cgo.unknown) > 0 {
		refuse(fmt.Errorf("%w: cgo links %s, which the Go recipe has no Alpine package for; use a Dockerfile that installs it",
			ErrUnsupportedBuilder, plan.cgo.unknownText()))
	}
	plan.vendored = !context.workspace && regularExists(root, "vendor/modules.txt")
	// A requirement every local replacement satisfies needs no checksum.
	if !plan.vendored && !context.workspace && len(goSumMissing(plan.module, nil)) > 0 {
		if sum, err := readContainedRegular(root, "go.sum", min(sumLimit, goSumReadLimit)); err == nil {
			plan.sumStale, plan.sumBytes = goSumMissing(plan.module, sum), int64(len(sum))
		} else if !regularExists(root, "go.sum") {
			plan.sumAbsent = true
		}
	}
	plan.ownerModules = goOwnerModules(plan.module, context.workModules)
	if len(plan.ownerModules) > 0 && !plan.vendored {
		for _, secret := range config.Secrets {
			if secret.Variable == goPrivateTokenVariable && buildSecretReaches(secret.Step, "install") {
				plan.owner = goModuleOwner(plan.module.module)
				plan.ownerHost, _, _ = strings.Cut(plan.owner, "/")
			}
		}
	}
	plan.templ, plan.templFrom = goTemplGenerate(plan.module, packages)
	custom := strings.TrimSpace(config.BuildCommand) != "" && strings.TrimSpace(config.BuildCommand) != "go build ./..."
	inContext := func(rel string) string { return path.Clean(path.Join(context.module, rel)) }
	plan.embeds = resolveGoEmbeds(root, packages.embedsOf(main, plan.module.module))
	for _, embed := range plan.embeds {
		switch {
		case embed.present:
		case embed.frontend == "":
			if !custom {
				refuse(fmt.Errorf("%w: //go:embed %s matches nothing in this commit and no package.json in the module builds %s; commit the files or build them in the build command",
					ErrUnsupportedBuilder, embed.pattern, embed.dir))
			}
		case plan.frontend != nil:
			if plan.frontend.root != inContext(embed.frontend) {
				refuse(fmt.Errorf("%w: //go:embed names the builds of two front-end packages (%s and %s); the Go recipe builds one",
					ErrUnsupportedBuilder, plan.frontend.root, inContext(embed.frontend)))
			}
		default:
			stage, err := planGoFrontend(root, embed, inContext, config)
			if err != nil {
				refuse(err)
				continue
			}
			plan.frontend = stage
		}
	}
	return plan, refusal
}

// planGoFrontend plans the Node stage that builds an embedded directory
// from the package.json that writes it.
func planGoFrontend(root string, embed goEmbedResolution, inContext func(string) string, config BuildPlanConfig) (*goFrontendStage, error) {
	frontendAbs := filepath.Join(root, filepath.FromSlash(embed.frontend))
	source, err := readNodeInstallSource(frontendAbs, "", nodeTargetArch(config.TargetPlatform), newNodeReadBudget())
	if err != nil {
		return nil, fmt.Errorf("%w: the front-end package the Go module embeds: %v", ErrUnsupportedBuilder, err)
	}
	node := planNodeInstall(source.facts, nodeInstallChoice{build: "npm run build", assets: true})
	if node.blocked != nil {
		return nil, fmt.Errorf("%w: the Go recipe builds %s's front end with a Node stage: %s", ErrUnsupportedBuilder, rootLabelOf(embed.frontend), node.blocked.Measured)
	}
	stage := &goFrontendStage{root: inContext(embed.frontend), output: inContext(embed.output), target: inContext(embed.dir), plan: node}
	if content, err := readContainedRegular(frontendAbs, "package.json", 512<<10); err == nil {
		var manifest nodeManifest
		if parseNodeManifest(content, &manifest) {
			if framework := matchNodeFramework(manifest); framework != nil {
				stage.framework = framework.Label
			}
		}
	}
	for _, input := range source.installInputs(node) {
		stage.inputs = append(stage.inputs, path.Join(stage.root, input))
	}
	return stage, nil
}

// goVersionInputs are the go and toolchain lines the toolchain is chosen
// from. A go.work governs the build of every module it uses, so its own
// lines count too: the higher go minimum, and its toolchain line first.
func goVersionInputs(module []byte, context goBuildContext) []byte {
	if !context.workspace {
		return module
	}
	parsed := parseGoMod(module)
	return goModuleForVersionCheck(goWorkspaceVersionLines(parsed.goVersion, parsed.toolchain, context.work.goVersion, context.work.toolchain))
}

// goWorkspaceVersionLines folds a go.work's go and toolchain lines into a
// module's, as goVersionInputs does.
func goWorkspaceVersionLines(minimum, toolchain, workGo, workToolchain string) (string, string) {
	if stableGoVersionRE.MatchString(workGo) && (minimum == "" || version.Compare("go"+workGo, "go"+minimum) > 0) {
		minimum = workGo
	}
	if workToolchain != "" {
		toolchain = workToolchain
	}
	return minimum, toolchain
}

// goCandidateVersionModule is what a candidate's toolchain is judged
// against: its go.mod's go and toolchain lines, with those of the go.work
// that uses it folded in, as the recipe folds them.
func goCandidateVersionModule(candidate *DetectedCandidate) []byte {
	minimum, toolchain := candidate.GoMinimumVersion, candidate.GoToolchain
	if facts := candidate.Go; facts != nil {
		minimum, toolchain = goWorkspaceVersionLines(minimum, toolchain, facts.WorkGo, facts.WorkToolchain)
	}
	return goModuleForVersionCheck(minimum, toolchain)
}

// ldflags are the flags the default build links with: a static binary
// when every library it links has a static archive, the ordinary external
// link when one is a shared object the runtime stage installs.
func (p goBuildPlan) ldflags() string {
	if p.cgo.enabled() && !p.cgo.dynamic() {
		return `-ldflags='-s -w -linkmode external -extldflags "-static"'`
	}
	return "-ldflags='-s -w'"
}

// buildTags are timetzdata, which embeds the zone database so
// time.LoadLocation works on any runtime image, and the cgo bindings' own.
func (p goBuildPlan) buildTags() string {
	return strings.Join(append([]string{"timetzdata"}, p.cgo.tags...), ",")
}

// goRecipeBases are the images the Go recipe resolves, in the order the
// Dockerfile refers to them: the toolchain, the runtime, then the Node
// images of a front-end stage.
func goRecipeBases(recipe selectedRecipe) []string {
	toolchain, runtime := "golang:"+recipe.goVersion+"-alpine", recipeBaseCatalogue["go"][1]
	if recipe.goBuild.cgo.dynamic() {
		toolchain = "golang:" + strings.TrimPrefix(version.Lang("go"+recipe.goVersion), "go") + "-alpine" + compiledDynamicAlpine
		runtime = "alpine:" + compiledDynamicAlpine
	}
	bases := []string{toolchain, runtime}
	if stage := recipe.goBuild.frontend; stage != nil {
		bases = append(bases, stage.plan.baseImages(false)...)
	}
	return bases
}

// goRecipeNotes are the decisions preparing a Go build made that the run
// log states.
func goRecipeNotes(recipe selectedRecipe) []string {
	plan := recipe.goBuild
	notes := []string{}
	if note := recipe.goChoice.note(); note != "" {
		notes = append(notes, note)
	}
	if plan.context.workspace {
		note := "Building the module in the go.work workspace that uses it"
		if plan.context.dir != "" {
			note += ", from " + plan.context.dir
		}
		notes = append(notes, note)
	} else if plan.context.replaceOnly {
		notes = append(notes, "Building from "+plan.context.dir+", which holds the module and its local replacements ("+strings.Join(plan.context.replaces, ", ")+")")
	}
	if plan.cgo.enabled() {
		linking := "linked statically"
		if plan.cgo.dynamic() {
			linking = "linked against " + strings.Join(plan.cgo.runtime, ", ") + " on alpine " + compiledDynamicAlpine
		}
		notes = append(notes, "Compiling with cgo for "+plan.cgo.describe()+", "+linking)
	}
	switch {
	case plan.vendored:
		notes = append(notes, "Building from vendor/ (vendor/modules.txt); no module download")
	case plan.sumAbsent:
		notes = append(notes, "go.sum is not committed; the build records the checksums of what it downloads (-mod=mod)")
	case len(plan.sumStale) > 0:
		notes = append(notes, "go.sum has no entry for "+strings.Join(boundedNames(plan.sumStale), ", ")+"; the build records them (-mod=mod)")
	}
	if plan.owner != "" {
		notes = append(notes, "Fetching "+plan.owner+" modules directly with "+goPrivateTokenVariable)
	}
	if plan.templ != "" {
		notes = append(notes, "Generating templ components before the build: "+plan.templ)
	}
	if stage := plan.frontend; stage != nil {
		notes = append(notes, "Building the embedded front end in "+rootLabelOf(stage.root)+" with "+stage.plan.installLine()+" and "+stage.plan.build+" into "+stage.target)
	}
	return notes
}

func renderGoDockerfile(recipe selectedRecipe, config BuildPlanConfig, bases []ResolvedImage, installSecrets, buildSecrets string) ([]string, error) {
	plan := recipe.goBuild
	if len(bases) < 2 || (plan.frontend != nil && len(bases) < 3) {
		return nil, ErrBuilderUnavailable
	}
	var lines []string
	if stage := plan.frontend; stage != nil {
		web, _, err := nodeInstallStage(stage.plan, bases[2], bases, "web-toolchain", "web", installSecrets)
		if err != nil {
			return nil, err
		}
		// The stage copies the whole context, so a package that reads a
		// file beside it still finds it, and installs where package.json is.
		if stage.root != "." {
			if copied := slices.Index(web, "COPY . ."); copied >= 0 {
				web = slices.Insert(web, copied+1, "WORKDIR /app/"+stage.root)
			}
		}
		lines = append(web, stage.plan.buildRun(buildSecrets, stage.plan.build, boundToBuild(config.Secrets)))
	}
	packagePath := "./"
	if recipe.mainPackage != "." {
		packagePath += filepath.ToSlash(recipe.mainPackage)
	}
	lines = append(lines, "FROM "+immutableImageReference(bases[0])+" AS build", "WORKDIR /src")
	system := append([]string(nil), plan.cgo.build...)
	if plan.owner != "" {
		system = append(system, "git")
	}
	if len(system) > 0 {
		lines = append(lines, "RUN apk add --no-cache "+strings.Join(system, " "))
	}
	lines = append(lines, "COPY . .")
	if plan.context.module != "" {
		lines = append(lines, "WORKDIR /src/"+plan.context.module)
	}
	env := []string{"CGO_ENABLED=0", "GOTOOLCHAIN=local"}
	if plan.cgo.enabled() {
		env[0] = "CGO_ENABLED=1"
	}
	if flag := plan.modFlag(); flag != "" {
		env = append(env, "GOFLAGS="+flag)
	}
	if plan.context.replaceOnly {
		env = append(env, "GOWORK=off")
	}
	if plan.owner != "" {
		env = append(env, "GOPRIVATE="+plan.owner)
	}
	lines = append(lines, "ENV "+strings.Join(env, " "))
	if !plan.vendored {
		download := "go mod download"
		if plan.context.workspace {
			// In a workspace, go mod download fetches every requirement,
			// even a sibling module the go.work provides and no proxy has
			// ("unrecognized import path"); listing the packages' imports
			// fetches only what the build compiles. -e leaves a package that
			// is not there yet — generated code, an embed the web stage
			// writes — to the build.
			download = "go list -e -deps ./... >/dev/null"
		}
		if plan.owner != "" {
			// The token rewrites the host's URLs for this one command's git;
			// it lives in the secret mount and this RUN's environment only.
			download = `export GIT_CONFIG_COUNT="1" GIT_CONFIG_KEY_0="url.https://x-access-token:${` + goPrivateTokenVariable +
				`}@` + plan.ownerHost + `/.insteadOf" GIT_CONFIG_VALUE_0="https://` + plan.ownerHost + `/" && ` + download
		}
		lines = append(lines, "RUN "+installSecrets+download)
	}
	if plan.templ != "" {
		// `go run pkg@version` runs outside the module, where -mod is not
		// allowed; `go tool` runs inside it and keeps the build's flags.
		command := plan.templ
		if strings.HasPrefix(command, "go run ") {
			command = "GOFLAGS= " + command
		}
		lines = append(lines, "RUN "+command)
	}
	if stage := plan.frontend; stage != nil {
		lines = append(lines, "COPY --from=web /app/"+stage.output+"/ /src/"+stage.target+"/")
	}
	command := strings.TrimSpace(config.BuildCommand)
	if command == "go build ./..." {
		// Old detection saved this literal default without an output path.
		// Execute it, then produce the runtime binary under the current contract.
		lines = append(lines, "RUN "+buildSecrets+command)
		command = ""
	}
	if command == "" {
		command = "go build -trimpath -tags " + plan.buildTags() + " " + plan.ldflags() + " -o /out/app " + packagePath
	}
	lines = append(lines, "RUN "+buildSecrets+command,
		`RUN test -f /out/app && test -x /out/app || (echo 'Go build command must write an executable to /out/app' >&2; exit 1)`)
	source := "/src"
	if plan.context.module != "" {
		source += "/" + plan.context.module
	}
	lines = append(lines, compiledRuntimeLines(bases[1], compiledRuntime{
		assets: recipe.runtimeAssets, source: source, packages: plan.cgo.runtime, start: config.StartCommand,
	})...)
	return lines, nil
}
