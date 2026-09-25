package deploy

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// A Rust crate is read the way Cargo reads it, as data: its manifest, the
// workspace that owns it, its binary targets as Cargo discovers them, the
// packages its lockfile resolves, and the configuration the build obeys.
// Detection and the recipe both call readCargoCrate over the same files, so
// what a candidate says about the build is what the build does.

// DetectedRustBuild is what detection read about how a Rust crate builds,
// kept on the candidate so preflight judges the plan without the tree.
type DetectedRustBuild struct {
	// Workspace is the Cargo workspace root the crate builds within,
	// relative to the checkout ("." for its top); Package is the crate's
	// package name, which the build selects with -p.
	Workspace string `json:"workspace,omitempty"`
	Package   string `json:"package,omitempty"`
	// Binaries are the crate's binary targets, Binary the one served and
	// BinaryReason why; an empty Binary with several targets is a tie.
	Binaries     []string `json:"binaries,omitempty"`
	Binary       string   `json:"binary,omitempty"`
	BinaryReason string   `json:"binaryReason,omitempty"`
	// NativePackages are the Alpine packages the build stage adds, and
	// NativeCrates the crates that need them; NativeRuntime are the ones the
	// runtime stage adds when the binary links dynamically, and
	// NativeUnmapped are crates that need a system library the recipe has
	// no package for.
	NativePackages []string `json:"nativePackages,omitempty"`
	NativeCrates   []string `json:"nativeCrates,omitempty"`
	NativeRuntime  []string `json:"nativeRuntime,omitempty"`
	NativeUnmapped []string `json:"nativeUnmapped,omitempty"`
	// sqlx: its compile-time query macros are used, the offline query data
	// is committed — .sqlx, or sqlx 0.5 and 0.6's sqlx-data.json, named by
	// SQLxOfflineData relative to the checkout — and migrate!() embeds
	// migrations.
	SQLxMacros      bool   `json:"sqlxMacros,omitempty"`
	SQLxOffline     bool   `json:"sqlxOffline,omitempty"`
	SQLxOfflineData string `json:"sqlxOfflineData,omitempty"`
	SQLxMigrate     bool   `json:"sqlxMigrate,omitempty"`
	// LockVersion is Cargo.lock's format, LockStale the dependencies it does
	// not resolve, and Toolchain the rust-toolchain pin.
	LockVersion int      `json:"lockVersion,omitempty"`
	LockStale   []string `json:"lockStale,omitempty"`
	Toolchain   string   `json:"toolchain,omitempty"`
	// HeavyRelease is the release profile setting that makes the build
	// peak higher (fat LTO, one codegen unit).
	HeavyRelease string `json:"heavyRelease,omitempty"`
	// Fullstack is the web framework that also builds a browser side:
	// leptos (cargo-leptos), trunk, dioxus or shuttle.
	Fullstack string `json:"fullstack,omitempty"`
}

// cargoDependency is one entry of a dependency table.
type cargoDependency struct {
	crate, version, path string
	workspace, optional  bool
	features             []string
}

// cargoTarget is a [[bin]] declaration or a discovered binary target.
type cargoTarget struct {
	name, path       string
	requiredFeatures []string
}

// cargoLeptos is cargo-leptos's metadata for one project.
type cargoLeptos struct {
	name, binPackage, outputName, siteRoot, siteAddr, binTarget string
	// hashFiles names the site's files by content hash, which the server
	// reads back from hashFile (hash.txt unless named) beside its binary.
	hashFiles bool
	hashFile  string
}

// cargoFile is the inert view of one Cargo.toml.
type cargoFile struct {
	name, edition, defaultRun, workspacePath string
	hasPkg, workspace                        bool
	autobins                                 *bool
	bins                                     []cargoTarget
	// deps are [dependencies] and every target's; all adds dev- and
	// build-dependencies, which the lockfile resolves too.
	deps, all       map[string]cargoDependency
	features        map[string][]string
	members         []string
	exclude         []string
	workspaceDeps   map[string]cargoDependency
	leptos          []cargoLeptos
	lto, codegen    string
	unreadableTable bool
}

var cargoTableHeaderRE = regexp.MustCompile(`^\[\s*([^\[\]]+?)\s*\]$`)

// readCargoFile reads a manifest through readTOML: an unusual layout yields
// fewer facts, never wrong ones.
func readCargoFile(content []byte) cargoFile {
	file := cargoFile{deps: map[string]cargoDependency{}, all: map[string]cargoDependency{},
		features: map[string][]string{}, workspaceDeps: map[string]cargoDependency{}}
	// A table with no keys — `[workspace]` alone makes a crate its own
	// workspace root — has no entries, so headers are read on their own.
	for _, raw := range strings.Split(string(manifestText(content)), "\n") {
		line := strings.TrimSpace(stripTOMLComment(raw))
		if match := cargoTableHeaderRE.FindStringSubmatch(line); match != nil {
			switch normalizeTOMLKey(match[1]) {
			case "package":
				file.hasPkg = true
			case "workspace":
				file.workspace = true
			}
		}
	}
	leptos := map[int]*cargoLeptos{}
	for _, entry := range readTOML(content) {
		table, key, value := entry.table, entry.key, entry.value
		switch {
		case table == "package":
			file.hasPkg = true
			switch key {
			case "name":
				file.name = value.text
			case "edition":
				file.edition = value.text
			case "default-run":
				file.defaultRun = value.text
			case "workspace":
				file.workspacePath = value.text
			case "autobins":
				enabled := value.text == "true"
				file.autobins = &enabled
			}
		case table == "workspace":
			file.workspace = true
			switch key {
			case "members":
				file.members = value.list
			case "exclude":
				file.exclude = value.list
			}
		case table == "workspace.dependencies" || strings.HasPrefix(table, "workspace.dependencies."):
			applyCargoDependency(file.workspaceDeps, strings.TrimPrefix(strings.TrimPrefix(table, "workspace.dependencies"), "."), key, value)
		case table == "bin" && entry.index >= 0:
			for len(file.bins) <= entry.index {
				file.bins = append(file.bins, cargoTarget{})
			}
			switch key {
			case "name":
				file.bins[entry.index].name = value.text
			case "path":
				file.bins[entry.index].path = value.text
			case "required-features":
				file.bins[entry.index].requiredFeatures = value.list
			}
		case table == "features":
			file.features[key] = value.list
		case table == "package.metadata.leptos":
			if leptos[-1] == nil {
				leptos[-1] = &cargoLeptos{}
			}
			applyCargoLeptos(leptos[-1], key, value)
		case table == "workspace.metadata.leptos" && entry.index >= 0:
			if leptos[entry.index] == nil {
				leptos[entry.index] = &cargoLeptos{}
			}
			applyCargoLeptos(leptos[entry.index], key, value)
		case table == "profile.release":
			switch key {
			case "lto":
				file.lto = value.text
			case "codegen-units":
				file.codegen = value.text
			}
		default:
			if kind, name, ok := cargoDependencyTable(table); ok {
				if kind == "dependencies" {
					applyCargoDependency(file.deps, name, key, value)
				}
				applyCargoDependency(file.all, name, key, value)
			}
		}
	}
	for _, index := range cargoLeptosIndexes(leptos) {
		file.leptos = append(file.leptos, *leptos[index])
	}
	bins := file.bins[:0]
	for _, bin := range file.bins {
		if bin.name != "" {
			bins = append(bins, bin)
		}
	}
	file.bins = bins
	return file
}

func cargoLeptosIndexes(values map[int]*cargoLeptos) []int {
	indexes := make([]int, 0, len(values))
	for index := range values {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	return indexes
}

// cargoDependencyTable names a dependency table and, for the
// `[dependencies.axum]` form, the dependency it describes.
func cargoDependencyTable(table string) (string, string, bool) {
	parts := strings.Split(table, ".")
	if len(parts) >= 3 && parts[0] == "target" {
		// target.<cfg or triple>.dependencies[.name]
		for index := 2; index < len(parts); index++ {
			switch parts[index] {
			case "dependencies", "dev-dependencies", "build-dependencies":
				return parts[index], strings.Join(parts[index+1:], "."), true
			}
		}
		return "", "", false
	}
	switch parts[0] {
	case "dependencies", "dev-dependencies", "build-dependencies":
		return parts[0], strings.Join(parts[1:], "."), true
	}
	return "", "", false
}

// applyCargoDependency records one key of a dependency: `axum = "0.8"`,
// `axum.workspace = true`, `axum = { version = "0.8", features = [...] }`,
// or a key of the `[dependencies.axum]` table.
func applyCargoDependency(deps map[string]cargoDependency, name, key string, value tomlValue) {
	field := ""
	if name == "" {
		name, field, _ = strings.Cut(key, ".")
	} else {
		field = key
	}
	if name == "" {
		return
	}
	dep := deps[name]
	if dep.crate == "" {
		dep.crate = name
	}
	switch field {
	case "":
		dep.version = value.text
	case "version":
		dep.version = value.text
	case "package":
		dep.crate = value.text
	case "workspace":
		dep.workspace = value.text == "true"
	case "path":
		dep.path = value.text
	case "optional":
		dep.optional = value.text == "true"
	case "features":
		dep.features = value.list
	}
	deps[name] = dep
}

func applyCargoLeptos(leptos *cargoLeptos, key string, value tomlValue) {
	switch key {
	case "name":
		leptos.name = value.text
	case "bin-package":
		leptos.binPackage = value.text
	case "output-name":
		leptos.outputName = value.text
	case "site-root":
		leptos.siteRoot = value.text
	case "site-addr":
		leptos.siteAddr = value.text
	case "bin-target":
		leptos.binTarget = value.text
	case "hash-files":
		leptos.hashFiles = value.text == "true"
	case "hash-file":
		leptos.hashFile = value.text
	}
}

// crates is every crate name the manifest depends on, runtime or not,
// normalized, with workspace-inherited entries resolved through the
// workspace's own table.
func (f cargoFile) crates(workspace map[string]cargoDependency, all bool) map[string]cargoDependency {
	source := f.deps
	if all {
		source = f.all
	}
	result := map[string]cargoDependency{}
	for key, dep := range source {
		if dep.workspace {
			if inherited, ok := workspace[key]; ok {
				dep.crate, dep.version, dep.path = inherited.crate, inherited.version, inherited.path
				dep.features = append(append([]string(nil), inherited.features...), dep.features...)
			}
		}
		result[normalizeCrate(dep.crate)] = dep
	}
	return result
}

// defaultFeatures is the closure of the features `default` enables.
func (f cargoFile) defaultFeatures() map[string]bool {
	enabled := map[string]bool{}
	queue := append([]string(nil), f.features["default"]...)
	for len(queue) > 0 && len(enabled) < 1024 {
		feature := queue[0]
		queue = queue[1:]
		if enabled[feature] {
			continue
		}
		enabled[feature] = true
		queue = append(queue, f.features[feature]...)
	}
	return enabled
}

// cargoLock is what Cargo.lock resolves: its format version and every
// package, by normalized name, with the versions locked.
type cargoLock struct {
	version  int
	packages map[string][]string
}

func readCargoLock(content []byte) cargoLock {
	lock := cargoLock{packages: map[string][]string{}}
	name := ""
	inPackage := false
	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case line == "[[package]]":
			inPackage, name = true, ""
		case strings.HasPrefix(line, "["):
			inPackage = false
		case strings.HasPrefix(line, "version = "):
			value := strings.Trim(strings.TrimPrefix(line, "version = "), `"`)
			if !inPackage {
				lock.version, _ = strconv.Atoi(value)
			} else if name != "" {
				lock.packages[name] = append(lock.packages[name], value)
			}
		case inPackage && strings.HasPrefix(line, "name = "):
			name = normalizeCrate(strings.Trim(strings.TrimPrefix(line, "name = "), `"`))
		}
	}
	return lock
}

func (l cargoLock) has(name string) bool { return len(l.packages[name]) > 0 }

// cargoWorkspace is the workspace a crate builds within.
type cargoWorkspace struct {
	dir  string
	file cargoFile
	// member is the crate's directory inside the workspace; empty when the
	// workspace root is the crate itself.
	member string
}

// findCargoWorkspace finds the workspace that owns the crate at dir, inside
// boundary, the way Cargo does: the crate itself, the root package.workspace
// names, or the first ancestor whose manifest has a [workspace]. An ancestor
// workspace that does not list the crate owns nothing, and the crate builds
// on its own, which is what a build of its directory alone always did.
func findCargoWorkspace(boundary, dir string, file cargoFile) *cargoWorkspace {
	if file.workspace {
		return &cargoWorkspace{dir: dir, file: file}
	}
	candidates := []string{}
	if file.workspacePath != "" {
		candidates = append(candidates, filepath.Join(dir, filepath.FromSlash(file.workspacePath)))
	} else {
		for parent := filepath.Dir(dir); strings.HasPrefix(parent, boundary) && parent != dir; parent = filepath.Dir(parent) {
			candidates = append(candidates, parent)
			if parent == boundary || len(candidates) > 16 {
				break
			}
		}
	}
	for _, candidate := range candidates {
		if !strings.HasPrefix(candidate, boundary) || !regularExists(candidate, "Cargo.toml") {
			continue
		}
		content, err := readContainedRegular(candidate, "Cargo.toml", 512<<10)
		if err != nil {
			return nil
		}
		root := readCargoFile(content)
		if !root.workspace {
			continue
		}
		member, err := filepath.Rel(candidate, dir)
		if err != nil {
			return nil
		}
		member = filepath.ToSlash(member)
		if !cargoWorkspaceIncludes(root, member) {
			return nil
		}
		return &cargoWorkspace{dir: candidate, file: root, member: member}
	}
	return nil
}

// cargoWorkspaceIncludes says a workspace lists a member directory: a
// members glob matches it and no exclude covers it, or the root package
// depends on it by path, which makes it a member too.
func cargoWorkspaceIncludes(root cargoFile, member string) bool {
	for _, excluded := range root.exclude {
		excluded = strings.TrimSuffix(path.Clean(excluded), "/")
		if member == excluded || strings.HasPrefix(member, excluded+"/") {
			return false
		}
	}
	for _, pattern := range root.members {
		// Cargo's member globs are the path globs a .dockerignore line is.
		if expression, ok := dockerignoreRegexp(path.Clean(pattern)); ok && expression.MatchString(member) {
			return true
		}
	}
	for _, dep := range root.all {
		if dep.path != "" && path.Clean(dep.path) == member {
			return true
		}
	}
	return false
}

// cargoCrate is one crate as the build sees it.
type cargoCrate struct {
	dir       string
	file      cargoFile
	workspace *cargoWorkspace
	// deps are the crate's runtime dependencies by normalized crate name.
	deps     map[string]cargoDependency
	binaries []cargoTarget
	binary   string
	reason   string
	tied     []string
	lock     *cargoLock
	// toolchain is the rust-toolchain file's content at the context root.
	toolchain string
	// target and targetDir are .cargo/config's [build] target and
	// target-dir, and rustflags whether it sets build flags.
	target, targetDir string
	rustflags         bool
	// sqlxData is where the offline query data sits, relative to the
	// checkout: a .sqlx directory or sqlx-data.json.
	sqlxData        string
	sqlxMacros      bool
	sqlxMigrate     bool
	trunk           string
	leptos          *cargoLeptos
	dioxus, shuttle bool
}

// contextDir is the directory the build context is: the workspace root
// when the crate is a member.
func (c cargoCrate) contextDir() string {
	if c.workspace != nil {
		return c.workspace.dir
	}
	return c.dir
}

// rustSourceBudget bounds what one detection reads of Rust sources beyond
// the manifests — binary entry points and the sqlx macro scan — and reads a
// workspace's Cargo.lock once for all its members.
type rustSourceBudget struct {
	remaining int64
	locks     map[string]*cargoLock
}

func (b *rustSourceBudget) lock(dir string) (*cargoLock, bool) {
	if b == nil || b.locks == nil {
		return nil, false
	}
	lock, ok := b.locks[dir]
	return lock, ok
}

func (b *rustSourceBudget) keepLock(dir string, lock *cargoLock) {
	if b != nil && b.locks != nil {
		b.locks[dir] = lock
	}
}

// limit is the most a single read may take: n, or what is left.
func (b *rustSourceBudget) limit(n int64) int64 {
	if b == nil {
		return n
	}
	return min(n, b.remaining)
}

func (b *rustSourceBudget) take(n int64) bool {
	if b == nil {
		return true
	}
	if b.remaining < n {
		return false
	}
	b.remaining -= n
	return true
}

var (
	rustServesRE      = regexp.MustCompile(`axum::serve|axum::Server|HttpServer::new|rocket::build|rocket::custom|#\[(?:rocket::)?launch\]|warp::serve|poem::Server|salvo::Server|Server::new\(TcpListener|tide::new|loco_rs::cli::main|leptos_axum|shuttle_runtime::main|hyper::Server|\bserve\(\s*listener`)
	rustSQLxMacroRE   = regexp.MustCompile(`\b(?:sqlx::)?query(?:_as|_scalar|_file|_file_as|_file_scalar|_unchecked|_as_unchecked)?!\s*\(`)
	rustSQLxMigrateRE = regexp.MustCompile(`\b(?:sqlx::)?migrate!\s*\(`)
	rustTrunkRE       = regexp.MustCompile(`data-trunk`)
)

// rustTrunkCrates are the browser UI crates Trunk builds to WebAssembly.
var rustTrunkCrates = []string{"yew", "leptos", "dioxus-web", "dioxus", "sycamore", "seed"}

// readCargoCrate reads the crate at dir inside the checkout at boundary.
func readCargoCrate(boundary, dir string, budget *rustSourceBudget) (cargoCrate, error) {
	content, err := readContainedRegular(dir, "Cargo.toml", 512<<10)
	if err != nil {
		return cargoCrate{}, err
	}
	crate := cargoCrate{dir: dir, file: readCargoFile(content)}
	crate.workspace = findCargoWorkspace(boundary, dir, crate.file)
	inherited := map[string]cargoDependency{}
	if crate.workspace != nil {
		inherited = crate.workspace.file.workspaceDeps
	}
	crate.deps = crate.file.crates(inherited, false)
	context := crate.contextDir()
	if cached, ok := budget.lock(context); ok {
		crate.lock = cached
	} else {
		// A lock past what is left of detection's budget is left unread,
		// as too large a one is: present, resolving nothing it can name.
		if lock, err := readContainedRegular(context, "Cargo.lock", budget.limit(8<<20)); err == nil && budget.take(int64(len(lock))) {
			parsed := readCargoLock(lock)
			crate.lock = &parsed
		} else if regularExists(context, "Cargo.lock") {
			crate.lock = &cargoLock{packages: map[string][]string{}}
		}
		budget.keepLock(context, crate.lock)
	}
	for _, name := range []string{"rust-toolchain.toml", "rust-toolchain"} {
		if regularExists(context, name) {
			if file, err := readContainedRegular(context, name, 4096); err == nil {
				crate.toolchain = string(file)
			}
			break
		}
	}
	for _, name := range []string{".cargo/config.toml", ".cargo/config"} {
		config, err := readContainedRegular(context, name, 64<<10)
		if err != nil {
			continue
		}
		entries := readTOML(config)
		if target, ok := tomlLookup(entries, "build", "target"); ok {
			crate.target = target.text
			if target.isList && len(target.list) > 0 {
				crate.target = target.list[0]
			}
		}
		crate.targetDir = tomlText(entries, "build", "target-dir")
		_, crate.rustflags = tomlLookup(entries, "build", "rustflags")
		break
	}
	crate.binaries = cargoBinaries(dir, crate.file)
	sources := map[string][]byte{}
	for _, binary := range crate.binaries {
		if len(sources) >= 16 || !safeRelativePath(binary.path) {
			continue
		}
		if source, err := readContainedRegular(dir, binary.path, 256<<10); err == nil && budget.take(int64(len(source))) {
			sources[binary.name] = source
		}
	}
	crate.binary, crate.reason, crate.tied = chooseCargoBinary(crate.file, crate.binaries, sources)
	// The query macros often live in a library crate of the workspace the
	// server depends on by path; its queries compile with the server.
	workspaceDir := ""
	if crate.workspace != nil {
		workspaceDir = crate.workspace.dir
	}
	libraries := cargoPathLibraries(boundary, dir, workspaceDir, crate.deps, inherited, budget)
	sqlxDirs := []string{dir}
	if crate.deps["sqlx"].crate != "" || (crate.lock != nil && crate.lock.has("sqlx-macros")) {
		crate.sqlxMacros, crate.sqlxMigrate = scanRustSQLx(dir, budget)
	}
	for _, library := range libraries {
		macros, migrate := scanRustSQLx(library, budget)
		crate.sqlxMacros, crate.sqlxMigrate = crate.sqlxMacros || macros, crate.sqlxMigrate || migrate
		if macros {
			sqlxDirs = append(sqlxDirs, library)
		}
	}
	crate.sqlxData = rustSQLxData(boundary, append(sqlxDirs, context))
	crate.leptos = cargoLeptosFor(crate)
	if crate.leptos == nil {
		crate.trunk = cargoTrunk(dir, crate.deps)
	}
	crate.dioxus = crate.leptos == nil && crate.trunk == "" && crate.deps["dioxus"].crate != ""
	for name := range crate.deps {
		if strings.HasPrefix(name, "shuttle-") {
			crate.shuttle = true
		}
	}
	return crate, nil
}

// rustSQLxData finds the committed offline query data in the first of dirs
// that has it: .sqlx, or the sqlx-data.json sqlx 0.5 and 0.6 read.
func rustSQLxData(boundary string, dirs []string) string {
	for _, dir := range dirs {
		switch {
		case dirExists(dir, ".sqlx"):
			return joinRoot(checkoutPath(boundary, dir), ".sqlx")
		case regularExists(dir, "sqlx-data.json"):
			return joinRoot(checkoutPath(boundary, dir), "sqlx-data.json")
		}
	}
	return ""
}

// cargoPathLibraries are the crates inside the checkout the crate at dir
// depends on by path, directly or through each other, that depend on sqlx:
// at most 16 are read, each manifest charged to the budget. A dependency
// inherited from the workspace (workspaceDir) is relative to it.
func cargoPathLibraries(boundary, dir, workspaceDir string, deps, workspace map[string]cargoDependency, budget *rustSourceBudget) []string {
	libraries := []string{}
	seen := map[string]bool{dir: true}
	type pending struct {
		from string
		deps map[string]cargoDependency
	}
	queue := []pending{{dir, deps}}
	for len(queue) > 0 && len(seen) <= 16 {
		next := queue[0]
		queue = queue[1:]
		for _, name := range slices.Sorted(maps.Keys(next.deps)) {
			dep := next.deps[name]
			if dep.path == "" || filepath.IsAbs(dep.path) {
				continue
			}
			from := next.from
			if dep.workspace && workspaceDir != "" {
				from = workspaceDir
			}
			library := filepath.Join(from, filepath.FromSlash(dep.path))
			if seen[library] || len(seen) > 16 {
				continue
			}
			seen[library] = true
			if _, err := containedSubdirectory(boundary, checkoutPath(boundary, library)); err != nil {
				continue
			}
			content, err := readContainedRegular(library, "Cargo.toml", budget.limit(512<<10))
			if err != nil || !budget.take(int64(len(content))) {
				continue
			}
			libraryDeps := readCargoFile(content).crates(workspace, false)
			if libraryDeps["sqlx"].crate != "" {
				libraries = append(libraries, library)
			}
			queue = append(queue, pending{library, libraryDeps})
		}
	}
	return libraries
}

func dirExists(root, relative string) bool {
	info, err := os.Lstat(filepath.Join(root, relative))
	return err == nil && info.IsDir()
}

// cargoBinaries lists a crate's binary targets as Cargo discovers them:
// declared [[bin]]s whose required features the default ones enable, and,
// unless autobins is off, src/main.rs as the package's binary and each
// src/bin/*.rs and src/bin/*/main.rs as one named after it.
func cargoBinaries(dir string, file cargoFile) []cargoTarget {
	defaults := file.defaultFeatures()
	targets := []cargoTarget{}
	claimed := map[string]bool{}
	names := map[string]bool{}
	for _, bin := range file.bins {
		if bin.path == "" {
			bin.path = "src/bin/" + bin.name + ".rs"
			for _, guess := range []string{"src/bin/" + bin.name + ".rs", "src/bin/" + bin.name + "/main.rs"} {
				if regularExists(dir, guess) {
					bin.path = guess
					break
				}
			}
			if bin.name == file.name && !regularExists(dir, bin.path) && regularExists(dir, "src/main.rs") {
				bin.path = "src/main.rs"
			}
		}
		bin.path = path.Clean(bin.path)
		claimed[bin.path], names[bin.name] = true, true
		enabled := true
		for _, feature := range bin.requiredFeatures {
			enabled = enabled && defaults[feature]
		}
		if enabled {
			targets = append(targets, bin)
		}
	}
	auto := file.edition != "2015" || len(file.bins) == 0
	if file.autobins != nil {
		auto = *file.autobins
	}
	if !auto {
		return targets
	}
	if file.name != "" && regularExists(dir, "src/main.rs") && !claimed["src/main.rs"] && !names[file.name] {
		targets = append(targets, cargoTarget{name: file.name, path: "src/main.rs"})
	}
	entries, err := os.ReadDir(filepath.Join(dir, "src", "bin"))
	if err != nil {
		return targets
	}
	for _, entry := range entries {
		if len(targets) >= 64 {
			break
		}
		name, target := "", ""
		switch {
		case entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".rs"):
			name, target = strings.TrimSuffix(entry.Name(), ".rs"), "src/bin/"+entry.Name()
		case entry.IsDir() && regularExists(filepath.Join(dir, "src", "bin", entry.Name()), "main.rs"):
			name, target = entry.Name(), "src/bin/"+entry.Name()+"/main.rs"
		default:
			continue
		}
		if !names[name] && !claimed[target] {
			targets = append(targets, cargoTarget{name: name, path: target})
		}
	}
	return targets
}

// chooseCargoBinary picks the served binary: default-run, the only one,
// the only one whose source starts a server, then the one named after the
// package or a server by convention. A tie is left to the operator.
func chooseCargoBinary(file cargoFile, binaries []cargoTarget, sources map[string][]byte) (string, string, []string) {
	names := make([]string, 0, len(binaries))
	for _, binary := range binaries {
		names = append(names, binary.name)
	}
	switch {
	case file.defaultRun != "":
		return file.defaultRun, "package.default-run", nil
	case len(binaries) == 0 && file.name != "":
		// Cargo would build the package's binary from src/main.rs; a crate
		// that has none fails that build by name rather than here.
		return file.name, "the package's binary", nil
	case len(binaries) == 0:
		return "", "", nil
	case len(binaries) == 1:
		return binaries[0].name, "the crate's only binary", nil
	}
	serving := []string{}
	for _, name := range names {
		if rustServesRE.Match(sources[name]) {
			serving = append(serving, name)
		}
	}
	if len(serving) == 1 {
		return serving[0], "the only binary that starts a server", nil
	}
	pool := names
	if len(serving) > 1 {
		pool = serving
	}
	if slices.Contains(pool, file.name) {
		return file.name, "named after the package", nil
	}
	conventional := []string{}
	for _, name := range []string{"server", "api", "web", "app", "serve"} {
		if slices.Contains(pool, name) {
			conventional = append(conventional, name)
		}
	}
	if len(conventional) == 1 {
		return conventional[0], "conventional server name", nil
	}
	sort.Strings(pool)
	return "", "", pool
}

// scanRustSQLx reads the crate's sources, bounded, for sqlx's compile-time
// query macros and its embedded migrations.
func scanRustSQLx(dir string, budget *rustSourceBudget) (bool, bool) {
	macros, migrate := false, false
	files := 0
	errStop := errors.New("stop")
	_ = filepath.WalkDir(filepath.Join(dir, "src"), func(current string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".rs") {
			return nil
		}
		if files++; files > 400 {
			return errStop
		}
		content, n, err := readDetectionFile(current, 256<<10)
		if err != nil || !budget.take(n) {
			return nil
		}
		macros = macros || rustSQLxMacroRE.Match(content)
		migrate = migrate || rustSQLxMigrateRE.Match(content)
		if macros && migrate {
			return errStop
		}
		return nil
	})
	return macros, migrate
}

// cargoLeptosFor is cargo-leptos's metadata for the crate: its own
// [package.metadata.leptos], or the workspace project whose bin-package it is.
func cargoLeptosFor(crate cargoCrate) *cargoLeptos {
	for _, project := range crate.file.leptos {
		if project.binPackage == "" || project.binPackage == crate.file.name {
			project := project
			return &project
		}
	}
	if crate.workspace != nil && crate.workspace.member != "" {
		for _, project := range crate.workspace.file.leptos {
			if project.binPackage == crate.file.name {
				project := project
				return &project
			}
		}
	}
	return nil
}

// cargoTrunk names the UI crate a Trunk application builds, when Trunk.toml
// or an index.html with data-trunk links says Trunk builds the crate.
func cargoTrunk(dir string, deps map[string]cargoDependency) string {
	trunk := regularExists(dir, "Trunk.toml")
	if !trunk {
		if index, err := readContainedRegular(dir, "index.html", 256<<10); err == nil {
			trunk = rustTrunkRE.Match(index)
		}
	}
	if !trunk {
		return ""
	}
	for _, name := range rustTrunkCrates {
		if deps[name].crate != "" {
			return name
		}
	}
	return "trunk"
}

// rustLockStale lists the crate's dependencies Cargo.lock does not resolve:
// missing from it, or locked at no version the manifest's requirement
// accepts. Either stops `cargo build --locked` ("the lock file needs to be
// updated but --locked was passed").
func rustLockStale(crate cargoCrate) []string {
	if crate.lock == nil || len(crate.lock.packages) == 0 {
		return nil
	}
	inherited := map[string]cargoDependency{}
	if crate.workspace != nil {
		inherited = crate.workspace.file.workspaceDeps
	}
	stale := []string{}
	for name, dep := range crate.file.crates(inherited, true) {
		versions := crate.lock.packages[name]
		switch {
		case len(versions) == 0:
			stale = append(stale, name)
		case dep.path == "" && dep.version != "" && !slices.ContainsFunc(versions, func(locked string) bool { return cargoRequirementAccepts(dep.version, locked) }):
			stale = append(stale, name+" "+dep.version)
		}
	}
	sort.Strings(stale)
	return stale
}

// cargoRequirementAccepts evaluates a Cargo version requirement: a bare
// version is a caret requirement, and comparators separated by commas must
// all hold. Anything it cannot read is accepted, so a requirement is never
// reported stale on a guess.
func cargoRequirementAccepts(requirement, locked string) bool {
	version, ok := cargoVersion(locked)
	if !ok || strings.Contains(locked, "-") {
		return true
	}
	for _, comparator := range strings.Split(requirement, ",") {
		comparator = strings.TrimSpace(comparator)
		operator := strings.TrimRight(comparator, "0123456789.*xX ")
		operator = strings.TrimSpace(operator)
		bound := strings.TrimSpace(strings.TrimPrefix(comparator, operator))
		if bound == "*" || bound == "" {
			continue
		}
		parts := strings.Split(bound, ".")
		given := 0
		want := [3]int{}
		for index, part := range parts {
			if index > 2 {
				return true
			}
			if part == "*" || part == "x" || part == "X" {
				break
			}
			number, err := strconv.Atoi(part)
			if err != nil {
				return true
			}
			want[index], given = number, index+1
		}
		compare := func() int {
			for index := 0; index < 3; index++ {
				if version[index] != want[index] {
					if version[index] < want[index] {
						return -1
					}
					return 1
				}
			}
			return 0
		}
		prefix := func(count int) bool {
			for index := 0; index < count; index++ {
				if version[index] != want[index] {
					return false
				}
			}
			return true
		}
		switch operator {
		case "", "^":
			if compare() < 0 {
				return false
			}
			// ^1.2.3 := <2; ^0.2.3 := <0.3; ^0.0.3 := <0.0.4
			significant := 0
			for significant < given-1 && want[significant] == 0 {
				significant++
			}
			if !prefix(significant + 1) {
				return false
			}
		case "~":
			if compare() < 0 || !prefix(max(1, min(given, 2))) {
				return false
			}
		case "=":
			if !prefix(given) {
				return false
			}
		case ">=":
			if compare() < 0 {
				return false
			}
		case ">":
			if compare() <= 0 {
				return false
			}
		case "<":
			if compare() >= 0 {
				return false
			}
		case "<=":
			if compare() > 0 && !prefix(given) {
				return false
			}
		default:
			return true
		}
	}
	return true
}

func cargoVersion(text string) ([3]int, bool) {
	text, _, _ = strings.Cut(text, "+")
	text, _, _ = strings.Cut(text, "-")
	parts := strings.Split(text, ".")
	var version [3]int
	if len(parts) != 3 {
		return version, false
	}
	for index, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil {
			return version, false
		}
		version[index] = number
	}
	return version, true
}

// rustNativePlan is what the build stage installs and sets for the crates
// Cargo.lock resolves that build or link C, and what the runtime stage
// installs when the binary links musl dynamically.
type rustNativePlan struct {
	packages  []string
	env       []string
	rustflags string
	runtime   []string
	crates    []string
	unmapped  []string
}

// dynamic says the binary links musl and libgcc as shared libraries, which
// the runtime stage installs.
func (p rustNativePlan) dynamic() bool { return len(p.runtime) > 0 }

// rustBuildBasePackages are installed in every Rust build stage, which is
// discarded: musl-dev and static OpenSSL link the static binary the alpine
// runtime runs, and perl and make are what openssl's vendored build and
// jemalloc need.
var rustBuildBasePackages = []string{"musl-dev", "pkgconfig", "openssl-dev", "openssl-libs-static", "perl", "make"}

// rustUnmappedCrates need a system library the Alpine build has no static
// archive for, which a server rarely links: GUI and hardware bindings.
var rustUnmappedCrates = []string{"gtk-sys", "gdk-sys", "glib-sys", "webkit2gtk-sys", "javascriptcore-rs-sys", "alsa-sys", "libudev-sys", "x11", "xcb"}

func planRustNative(lock *cargoLock) rustNativePlan {
	plan := rustNativePlan{packages: append([]string(nil), rustBuildBasePackages...), env: []string{"OPENSSL_STATIC=1"}}
	if lock == nil {
		return plan
	}
	add := func(crate string, packages ...string) {
		plan.packages = append(plan.packages, packages...)
		plan.crates = append(plan.crates, crate+" ("+strings.Join(packages, ", ")+")")
	}
	if lock.has("cmake") {
		add("cmake", "cmake", "g++", "linux-headers")
	}
	for _, crate := range []string{"cxx", "cxx-build", "link-cplusplus"} {
		if lock.has(crate) {
			add(crate, "g++")
			break
		}
	}
	for _, crate := range []string{"bindgen", "clang-sys"} {
		if lock.has(crate) {
			// bindgen's build script loads libclang, which a build script
			// linked statically against musl cannot do ("Dynamic loading
			// not supported"). Without crt-static every artifact, build
			// scripts included, links musl dynamically, and the runtime
			// stage installs the libgcc the binary then needs.
			add(crate, "clang-dev")
			plan.rustflags = "-C target-feature=-crt-static"
			plan.runtime = append(plan.runtime, "libgcc")
			break
		}
	}
	vendoredProtoc := lock.has("protoc-bin-vendored") || lock.has("protobuf-src") || lock.has("protox")
	for _, crate := range []string{"prost-build", "tonic-build", "tonic-prost-build", "protobuf-codegen", "protoc-rust"} {
		if lock.has(crate) && !vendoredProtoc {
			add(crate, "protoc", "protobuf-dev")
			break
		}
	}
	if lock.has("pq-sys") && !lock.has("pq-src") {
		add("pq-sys", "libpq-dev")
		if plan.dynamic() {
			// A dynamically linked binary takes libpq.so like the rest.
			plan.runtime = append(plan.runtime, "libpq")
		} else {
			// libpq's static archive needs the libraries libpq.so would
			// have brought itself, after it on the link line.
			plan.env = append(plan.env, "PQ_LIB_STATIC=1")
			plan.rustflags = "-C link-arg=-Wl,-Bstatic -C link-arg=-lpgcommon_shlib -C link-arg=-lpgport_shlib -C link-arg=-lssl -C link-arg=-lcrypto -C link-arg=-lc"
		}
	}
	if lock.has("mysqlclient-sys") && !lock.has("mysqlclient-src") {
		add("mysqlclient-sys", "mariadb-connector-c-dev", "mariadb-static", "zlib-static", "zstd-static")
		plan.env = append(plan.env, "MYSQLCLIENT_STATIC=1", "PKG_CONFIG_ALL_STATIC=1")
	}
	if lock.has("libsqlite3-sys") {
		add("libsqlite3-sys", "sqlite-dev", "sqlite-static")
	}
	if lock.has("libz-sys") {
		add("libz-sys", "zlib-dev", "zlib-static")
	}
	if lock.has("rdkafka-sys") {
		add("rdkafka-sys", "bash", "g++", "linux-headers")
	}
	for _, crate := range rustUnmappedCrates {
		if lock.has(crate) {
			plan.unmapped = append(plan.unmapped, crate)
		}
	}
	plan.packages = uniqueOrdered(plan.packages)
	return plan
}

// rustFacts is a crate's reading as a candidate records it.
func rustFacts(checkout string, crate cargoCrate, native rustNativePlan) *DetectedRustBuild {
	facts := &DetectedRustBuild{Package: crate.file.name, Binary: crate.binary, BinaryReason: crate.reason,
		NativeCrates: compiledFactList(native.crates), NativeRuntime: native.runtime, NativeUnmapped: compiledFactList(native.unmapped),
		SQLxMacros: crate.sqlxMacros, SQLxOffline: crate.sqlxData != "", SQLxOfflineData: crate.sqlxData, SQLxMigrate: crate.sqlxMigrate,
		LockStale: compiledFactList(rustLockStale(crate))}
	if len(native.packages) > len(rustBuildBasePackages) {
		facts.NativePackages = native.packages[len(rustBuildBasePackages):]
	}
	for _, binary := range crate.binaries {
		if len(facts.Binaries) < 64 {
			facts.Binaries = append(facts.Binaries, binary.name)
		}
	}
	if crate.workspace != nil && crate.workspace.member != "" {
		facts.Workspace = rootLabelOf(checkoutPath(checkout, crate.workspace.dir))
	}
	if crate.lock != nil {
		facts.LockVersion = crate.lock.version
	}
	if match := rustToolchainRE.FindStringSubmatch(crate.toolchain); match != nil {
		facts.Toolchain = match[1]
	}
	file := crate.file
	if crate.workspace != nil {
		// A member builds with the workspace root's profiles.
		file = crate.workspace.file
	}
	switch {
	case file.lto == "true" || file.lto == "fat":
		facts.HeavyRelease = "lto = " + file.lto
	case file.codegen == "1":
		facts.HeavyRelease = "codegen-units = 1"
	}
	switch {
	case crate.leptos != nil:
		facts.Fullstack = "leptos"
	case crate.trunk != "":
		facts.Fullstack = "trunk"
	case crate.dioxus:
		facts.Fullstack = "dioxus"
	case crate.shuttle:
		facts.Fullstack = "shuttle"
	}
	return facts
}

// validateDetectedRustBuild bounds what a saved candidate carries about its
// Rust build.
func validateDetectedRustBuild(candidate DetectedCandidate) error {
	facts := candidate.Rust
	if facts == nil {
		return nil
	}
	malformed := fmt.Errorf("%w: detected Rust build is malformed", ErrInvalidPlan)
	text := func(value string, limit int) bool {
		return len(value) <= limit && !strings.ContainsAny(value, "\x00\r\n") && rejectPlanSecretLiteral("detected Rust build", value) == nil
	}
	for _, list := range [][]string{facts.Binaries, facts.NativePackages, facts.NativeCrates, facts.NativeRuntime, facts.NativeUnmapped, facts.LockStale} {
		if len(list) > 64 {
			return malformed
		}
		for _, value := range list {
			if !text(value, 256) {
				return malformed
			}
		}
	}
	for _, value := range []string{facts.Workspace, facts.Package, facts.Binary, facts.BinaryReason, facts.Toolchain, facts.HeavyRelease, facts.SQLxOfflineData} {
		if !text(value, 512) {
			return malformed
		}
	}
	if facts.Workspace != "" && facts.Workspace != "." && !safeRelativePath(facts.Workspace) || facts.LockVersion < 0 || facts.LockVersion > 64 {
		return malformed
	}
	switch facts.Fullstack {
	case "", "leptos", "trunk", "dioxus", "shuttle":
	default:
		return malformed
	}
	return nil
}
