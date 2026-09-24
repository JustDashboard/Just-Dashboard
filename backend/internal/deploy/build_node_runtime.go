package deploy

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The image a JavaScript build runs on is decided here, from the same files
// the install plan reads and inside the same plan: which Node release, which
// Bun release when Bun is copied in, and whether the image is Alpine or
// Debian. Detection records the result on the candidate, preflight judges
// it, and the recipe renders it, so the three cannot disagree about the Node
// a build gets.

// nodeMajors are the Node releases the catalogue builds on. 22 stays the
// default a repository that declares nothing gets until a recipe version
// bump re-verifies the live fixtures on 24: moving it would rebuild every
// Node deployment on another major at once. Node 25 and later images ship
// without Corepack, which the pinned pnpm and Yarn releases are installed
// through, so they are not in the catalogue.
var nodeMajors = []int{20, 22, 24}

const (
	nodeDefaultMajor = 22
	// nodeNewestLTS is what nvm's lts/* and a bare "node" resolve to here.
	nodeNewestLTS = 24
)

// nodeEndOfLife are catalogue majors upstream no longer patches.
var nodeEndOfLife = map[int]string{20: "April 2026"}

// nodeLTSCodenames are nvm's lts/<codename> aliases.
var nodeLTSCodenames = map[string]int{"hydrogen": 18, "iron": 20, "jod": 22, "krypton": 24}

// Image families: Alpine (musl) is the default; Debian slim (glibc) is chosen
// only for packages that ship no musl binary at all.
const (
	nodeFamilyAlpine = "alpine"
	nodeFamilyGlibc  = "glibc"
)

// nodeImage is the catalogue reference of a Node major in a family.
func nodeImage(major int, family string) string {
	key := "node:" + strconv.Itoa(major)
	if family == nodeFamilyGlibc {
		key += "-glibc"
	}
	return recipeBaseCatalogue[key][0]
}

// bunImage is the Bun image a release is copied from: the Alpine build for
// the Alpine family, the Debian slim build for glibc, since a Bun binary only
// runs on the C library it was linked against.
func bunImage(release, family string) string {
	if release == "" {
		release = "1"
	}
	if family == nodeFamilyGlibc {
		return "oven/bun:" + release + "-slim"
	}
	return "oven/bun:" + release + "-alpine"
}

// bunReleaseOf is the release a Bun image reference names: "1", "1.2.21".
func bunReleaseOf(image string) string {
	release := strings.TrimPrefix(image, "oven/bun:")
	return strings.TrimSuffix(strings.TrimSuffix(release, "-alpine"), "-slim")
}

// nodeReleaseDeclaration is one place a repository names its Node or Bun
// release: a version file (with its path in the checkout) or a package.json
// field. exact is a version file's reading — "22.11.0" means Node 22 —
// where a package.json range is judged as a range.
type nodeReleaseDeclaration struct {
	source, spec string
	exact        bool
}

// nodeRuntimeFacts are the declarations and packages the image depends on,
// read with the install facts.
type nodeRuntimeFacts struct {
	// node lists Node declarations in precedence order: version files from
	// the package's own directory up to the checkout's top, nearest first
	// (.nvmrc, .node-version, .tool-versions), then volta.node,
	// devEngines.runtime and engines.node.
	node []nodeReleaseDeclaration
	// engines is engines.node, which Yarn 1 and an engine-strict npm or pnpm
	// enforce on the root package.
	engines      string
	engineStrict bool
	// bun is the first Bun declaration outside packageManager: .bun-version,
	// .tool-versions or engines.bun.
	bun nodeReleaseDeclaration
}

var nodeVersionFileNames = []string{".nvmrc", ".node-version", ".tool-versions"}

// readNodeRuntimeFacts reads the release declarations for the package at
// member under files. Version files are looked for upwards to the top of
// the checkout, the way nvm, fnm and asdf look for them.
func readNodeRuntimeFacts(files nodeFiles, member string, manifest, settings nodeInstallManifest, engineStrict bool) nodeRuntimeFacts {
	facts := nodeRuntimeFacts{engineStrict: engineStrict}
	directory := path.Join(files.dir, member)
	if directory == "." {
		directory = ""
	}
	for {
		here := nodeFiles{root: files.root, dir: directory, budget: files.budget}
		for _, name := range nodeVersionFileNames {
			content, err := here.read(name, 1024)
			if err != nil {
				continue
			}
			source := path.Join(directory, name)
			node, bun := readNodeVersionFile(name, string(content))
			if node != "" {
				facts.node = append(facts.node, nodeReleaseDeclaration{source: source, spec: node, exact: true})
			}
			if bun != "" && facts.bun.spec == "" {
				facts.bun = nodeReleaseDeclaration{source: source, spec: bun, exact: true}
			}
		}
		if facts.bun.spec == "" {
			if content, err := here.read(".bun-version", 1024); err == nil {
				if version := firstVersionLine(string(content)); version != "" {
					facts.bun = nodeReleaseDeclaration{source: path.Join(directory, ".bun-version"), spec: version, exact: true}
				}
			}
		}
		if directory == "" {
			break
		}
		directory = path.Dir(directory)
		if directory == "." {
			directory = ""
		}
	}
	// Only the nearest version file counts; the manifest fields follow it.
	if len(facts.node) > 1 {
		facts.node = facts.node[:1]
	}
	for _, source := range []nodeInstallManifest{manifest, settings} {
		var volta struct {
			Node string `json:"node"`
		}
		if len(source.Volta) > 0 && json.Unmarshal(source.Volta, &volta) == nil && strings.TrimSpace(volta.Node) != "" {
			facts.node = append(facts.node, nodeReleaseDeclaration{source: "volta.node", spec: boundedSpec(volta.Node), exact: true})
			break
		}
	}
	for _, source := range []nodeInstallManifest{manifest, settings} {
		if spec := devEnginesRuntime(source.DevEngines, "node"); spec != "" {
			facts.node = append(facts.node, nodeReleaseDeclaration{source: "devEngines.runtime", spec: spec})
			break
		}
	}
	for _, source := range []nodeInstallManifest{manifest, settings} {
		engines := manifestEngines(source.Engines)
		if spec := engines["node"]; spec != "" {
			facts.node = append(facts.node, nodeReleaseDeclaration{source: "engines.node", spec: spec})
			facts.engines = spec
			break
		}
	}
	if facts.bun.spec == "" {
		for _, source := range []nodeInstallManifest{manifest, settings} {
			if spec := manifestEngines(source.Engines)["bun"]; spec != "" {
				facts.bun = nodeReleaseDeclaration{source: "engines.bun", spec: spec}
				break
			}
		}
	}
	return facts
}

// readNodeVersionFile reads the Node and Bun releases a version file names:
// the first line of .nvmrc and .node-version, the nodejs/node and bun lines
// of .tool-versions (asdf and mise).
func readNodeVersionFile(name, content string) (node, bun string) {
	if name != ".tool-versions" {
		return firstVersionLine(content), ""
	}
	for _, line := range strings.Split(content, "\n") {
		if index := strings.Index(line, "#"); index >= 0 {
			line = line[:index]
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "nodejs", "node":
			if node == "" {
				node = boundedSpec(fields[1])
			}
		case "bun":
			if bun == "" {
				bun = boundedSpec(fields[1])
			}
		}
	}
	return node, bun
}

func firstVersionLine(content string) string {
	for _, line := range strings.Split(content, "\n") {
		if index := strings.Index(line, "#"); index >= 0 {
			line = line[:index]
		}
		if line = strings.TrimSpace(line); line != "" {
			return boundedSpec(line)
		}
	}
	return ""
}

// boundedSpec keeps a declaration short enough to name in a finding.
func boundedSpec(spec string) string {
	spec = strings.TrimSpace(spec)
	if len(spec) > 32 || strings.ContainsAny(spec, "\x00\r\n\"'`$\\") {
		return ""
	}
	return spec
}

func manifestEngines(raw json.RawMessage) map[string]string {
	engines := map[string]string{}
	var values map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &values) != nil {
		return engines
	}
	for name, value := range values {
		if text, ok := value.(string); ok {
			engines[name] = boundedSpec(text)
		}
	}
	return engines
}

// devEnginesRuntime reads devEngines.runtime, an object or a list of them.
func devEnginesRuntime(raw json.RawMessage, name string) string {
	var engines struct {
		Runtime json.RawMessage `json:"runtime"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &engines) != nil || len(engines.Runtime) == 0 {
		return ""
	}
	type entry struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	var entries []entry
	var single entry
	if json.Unmarshal(engines.Runtime, &single) == nil && single.Name != "" {
		entries = []entry{single}
	} else if json.Unmarshal(engines.Runtime, &entries) != nil {
		return ""
	}
	for _, runtime := range entries {
		if runtime.Name == name {
			return boundedSpec(runtime.Version)
		}
	}
	return ""
}

// nodeRelease is the Node major a build runs on and why.
type nodeRelease struct {
	major  int
	source string
	// declared is the declaration as written when it named no catalogue
	// major, so the major is the nearest one instead.
	declared string
	// exact says a version file or volta chose it; spec is the range that
	// chose it otherwise, empty for the default.
	exact bool
	spec  string
}

func (r nodeRelease) label() string {
	label := strconv.Itoa(r.major) + " (" + r.source + ")"
	if len(label) > 64 {
		label = label[:61] + "..."
	}
	return label
}

var (
	nodeExactSpecRE = regexp.MustCompile(`^v?([0-9]+)(?:\.(?:[0-9]+|x|\*)){0,2}$`)
	nodeMinorLineRE = regexp.MustCompile(`^[0-9]+\.[0-9]+$`)
)

// nodeSpecMajor reads a version file's declaration as a major: 22,
// v22.11.0, 22.11, 22.x, lts/jod, lts/*, node. ok is false for anything
// else.
func nodeSpecMajor(spec string) (int, bool) {
	spec = strings.ToLower(strings.TrimSpace(spec))
	if match := nodeExactSpecRE.FindStringSubmatch(spec); match != nil {
		major, err := strconv.Atoi(match[1])
		return major, err == nil
	}
	switch spec {
	case "lts/*", "lts/latest", "lts":
		return nodeNewestLTS, true
	case "node", "stable", "latest", "current":
		return nodeMajors[len(nodeMajors)-1], true
	}
	if codename, found := strings.CutPrefix(spec, "lts/"); found {
		major, ok := nodeLTSCodenames[codename]
		return major, ok
	}
	return 0, false
}

// nodeRangeMajors lists the catalogue majors whose newest release a range
// allows; known is false when the range is outside what the reader models.
func nodeRangeMajors(spec string) (majors []int, known bool) {
	for _, major := range nodeMajors {
		satisfied, ok := nodeRangeSatisfies(fmt.Sprintf("%d.99.99", major), spec)
		if !ok {
			return nil, false
		}
		if satisfied {
			majors = append(majors, major)
		}
	}
	return majors, true
}

// nearestNodeMajor is the catalogue major closest to one it lacks: the
// oldest newer one, or the newest when every one is older.
func nearestNodeMajor(major int) int {
	for _, candidate := range nodeMajors {
		if candidate >= major {
			return candidate
		}
	}
	return nodeMajors[len(nodeMajors)-1]
}

// rangeLowMajor is the major a range starts at, for choosing the nearest
// catalogue major to a range none of them satisfies.
func rangeLowMajor(spec string) int {
	low := 0
	for _, alternative := range strings.Split(spec, "||") {
		interval, ok := nodeRangeInterval(strings.TrimSpace(alternative))
		if ok && interval.low.set && (low == 0 || interval.low.v.major > low) {
			low = interval.low.v.major
		}
	}
	return low
}

// selectNodeRelease decides the Node major from the declarations, in their
// order; a declaration this reader cannot read gives way to the next one.
// An exact declaration picks its major, or the nearest catalogue major; a
// range keeps the default when it allows it, since most engines fields are a
// floor, and otherwise the newest major it allows.
func selectNodeRelease(facts nodeRuntimeFacts) nodeRelease {
	for _, declaration := range facts.node {
		if declaration.exact {
			major, ok := nodeSpecMajor(declaration.spec)
			if !ok {
				continue
			}
			release := nodeRelease{major: major, source: declaration.source, exact: true}
			if !slices.Contains(nodeMajors, major) {
				release.major, release.declared = nearestNodeMajor(major), declaration.spec
			}
			return release
		}
		majors, known := nodeRangeMajors(declaration.spec)
		if !known {
			continue
		}
		release := nodeRelease{source: declaration.source + " " + declaration.spec, spec: declaration.spec}
		switch {
		case len(majors) == 0:
			release.major, release.declared = nearestNodeMajor(rangeLowMajor(declaration.spec)), declaration.spec
		case slices.Contains(majors, nodeDefaultMajor):
			release.major = nodeDefaultMajor
		default:
			release.major = majors[len(majors)-1]
		}
		return release
	}
	return nodeRelease{major: nodeDefaultMajor, source: "recipe default"}
}

// nodeBunRelease is the Bun image release a declaration outside
// packageManager asks for: an exact release, a major.minor line, or nothing
// when the newest 1.x image satisfies it.
func nodeBunRelease(declaration nodeReleaseDeclaration) string {
	spec := strings.TrimPrefix(strings.TrimSpace(declaration.spec), "v")
	if spec == "" {
		return ""
	}
	if declaration.exact {
		if nodeExactVersionRE.MatchString(spec) && !strings.Contains(spec, "-") {
			return spec
		}
		if nodeMinorLineRE.MatchString(spec) {
			return spec
		}
		return ""
	}
	if satisfied, known := nodeRangeSatisfies("1.99.99", spec); !known || satisfied {
		return ""
	}
	interval, ok := nodeRangeInterval(spec)
	if !ok || !interval.low.set || interval.low.v.major != 1 {
		return ""
	}
	return fmt.Sprintf("%d.%d", interval.low.v.major, interval.low.v.minor)
}

// nodeSassMajor is the newest Node node-sass supports: node-sass 9, its last
// release, builds on Node 20 at most, and older releases on less.
const nodeSassMajor = 20

// nodeReleaseFor is the release a package builds on: its declaration, moved
// to Node 20 for node-sass when nothing pins a newer one, because node-sass
// has no binary for later Node and its sources do not compile against it.
func nodeReleaseFor(facts nodeInstallFacts) nodeRelease {
	release := selectNodeRelease(facts.runtime)
	if release.major <= nodeSassMajor || release.exact || !facts.present("node-sass") {
		return release
	}
	if release.spec != "" {
		if satisfied, known := nodeRangeSatisfies(fmt.Sprintf("%d.99.99", nodeSassMajor), release.spec); !known || !satisfied {
			return release
		}
	}
	return nodeRelease{major: nodeSassMajor, source: "node-sass supports Node 20 at most", spec: release.spec}
}

// present says the install puts a package in place: the package's manifest
// or its workspace root's names it, or a lockfile lists it among what it
// installs.
func (f nodeInstallFacts) present(name string) bool {
	if f.manifest.has(name) || f.settings.has(name) {
		return true
	}
	for _, reading := range f.readings {
		if _, ok := reading.names[name]; ok {
			return true
		}
	}
	return false
}

// directVersion is the version the install puts in place of a package the
// package's own manifest depends on: the one the installed lockfile records
// while its range still allows it, else the lowest the range allows, which
// is the one that matters for a "before this release" rule. A package only
// another dependency pulls in — webpack 4 under Storybook 6 — is not the
// build's toolchain, and a lockfile the install does not use says nothing.
func (f nodeInstallFacts) directVersion(name string, reading *nodeLockfileReading) (nodeVersion, bool) {
	spec := f.manifest.version(name)
	if spec == "" {
		return nodeVersion{}, false
	}
	if reading != nil {
		locked := reading.names[name]
		if version, ok := parseNodeVersion(locked); ok {
			if satisfied, known := nodeRangeSatisfies(locked, spec); !known || satisfied {
				return version, true
			}
		}
	}
	if interval, ok := nodeRangeInterval(strings.TrimSpace(strings.Split(spec, "||")[0])); ok && interval.low.set {
		return interval.low.v, true
	}
	return nodeVersion{}, false
}

// planNodeRelease records the release decision. It refuses a build only
// where the mismatch is certain to fail: Yarn 1, and npm or pnpm with
// engine-strict, check the root package's engines field against the Node
// they run on and stop the install; everywhere else engines is advice.
func planNodeRelease(facts nodeInstallFacts, plan *nodeInstallPlan) {
	release := plan.node
	major := strconv.Itoa(release.major)
	plan.findings = append(plan.findings, nodeFinding("node_version_selected", PreflightPass,
		"Node "+major+" for this package", release.label(),
		"The build and the server run on "+nodeImage(release.major, plan.family)+". A version file (.nvmrc, .node-version, .tool-versions) or package.json (volta, devEngines, engines) chooses the major; without one the recipe's default does.",
		"", "configuration.build"))
	mismatch := false
	if engines := facts.runtime.engines; engines != "" {
		if satisfied, known := nodeRangeSatisfies(major+".99.99", engines); known && !satisfied {
			mismatch = true
		}
	}
	enforcer := ""
	switch {
	case mismatch && plan.manager == "yarn" && !plan.berry:
		enforcer = "Yarn 1"
	case mismatch && facts.runtime.engineStrict && (plan.manager == "npm" || plan.manager == "pnpm"):
		enforcer = plan.manager + " with engine-strict in .npmrc"
	}
	if release.declared != "" || mismatch {
		declared, spec := strings.TrimSuffix(release.source, " "+release.declared), release.declared
		if release.declared == "" {
			declared, spec = "engines.node", facts.runtime.engines
		}
		item := nodeFinding("node_version_unsupported", PreflightWarning,
			"The declared Node release is not one the recipe builds on",
			fmt.Sprintf("%s asks for %s; the build runs Node %d", declared, spec, release.major),
			"The catalogue builds on Node 20, 22 and 24, and the nearest runs this package; that works unless the code relies on an API or a native binary of the release it asked for.",
			"Declare Node 20, 22 or 24 (.nvmrc or engines.node), or build with a Dockerfile for another release.", "configuration.build")
		if enforcer != "" {
			item.Severity = PreflightBlocked
			item.Means = enforcer + " refuses to install a package whose engines field excludes the Node it runs on, so the install would stop."
			item.Action = fmt.Sprintf("Widen engines.node to include Node %d, or declare one of Node 20, 22 and 24 that it allows.", release.major)
			plan.blocked = &item
			return
		}
		plan.findings = append(plan.findings, item)
	}
	if facts.present("node-sass") && release.major > nodeSassMajor {
		plan.findings = append(plan.findings, nodeFinding("node_sass_unsupported", PreflightWarning,
			"node-sass does not build on Node "+major, "node-sass with Node "+major+" ("+release.source+")",
			"node-sass, deprecated since 2024, has no binary for this release and its sources do not compile against it, so the install will most likely fail.",
			"Replace node-sass with sass, which sass-loader and Vite use as is, or declare Node 20 in .nvmrc.", "configuration.build"))
	}
	if eol := nodeEndOfLife[release.major]; eol != "" {
		action := "Declare Node 22 or 24 (.nvmrc or engines.node) once the package builds on it."
		if strings.HasPrefix(release.source, "node-sass") {
			action = "Replace node-sass with sass; the build then runs on Node 22."
		}
		plan.findings = append(plan.findings, nodeFinding("node_version_eol", PreflightWarning,
			"Node "+major+" no longer receives security fixes", "Node "+major+" reached end of life in "+eol,
			"The image is still built and served, but vulnerabilities found in this release are not fixed upstream.",
			action, "configuration.build"))
	}
}

// nodePackageSet names system packages in each image family.
type nodePackageSet struct{ alpine, debian []string }

func (s nodePackageSet) in(family string) []string {
	if family == nodeFamilyGlibc {
		return s.debian
	}
	return s.alpine
}

var (
	// nodeCompilers are what node-gyp needs to build a binding from source.
	nodeCompilers = nodePackageSet{alpine: []string{"python3", "make", "g++"}, debian: []string{"python3", "make", "g++"}}
	nodeGit       = nodePackageSet{alpine: []string{"git"}, debian: []string{"git", "ca-certificates"}}
	nodeSSH       = nodePackageSet{alpine: []string{"openssh-client"}, debian: []string{"openssh-client"}}
	// nodeOpenSSL lets Prisma's engines find the TLS library they load and
	// name the right binary for it.
	nodeOpenSSL = nodePackageSet{alpine: []string{"openssl"}, debian: []string{"openssl", "ca-certificates"}}
	// node-canvas publishes glibc binaries with cairo bundled, and none for
	// musl, where it compiles against the system's cairo and pango.
	nodeCanvasHeaders   = nodePackageSet{alpine: []string{"pkgconf", "cairo-dev", "pango-dev", "jpeg-dev", "giflib-dev", "librsvg-dev", "pixman-dev"}}
	nodeCanvasLibraries = nodePackageSet{alpine: []string{"cairo", "pango", "jpeg", "giflib", "librsvg", "pixman"}}
	nodeChromium        = nodePackageSet{
		alpine: []string{"chromium", "nss", "freetype", "harfbuzz", "ca-certificates", "font-freefont"},
		debian: []string{"chromium", "fonts-freefont-ttf"},
	}
)

// nodeNativeAddons compile a native binding with node-gyp when no prebuilt
// binary matches the platform, the Node release and the C library — an old
// release on a newer Node, an arm64 host, musl — and fail the install with
// "gyp ERR! find Python" on an image without compilers.
var nodeNativeAddons = []string{
	"better-sqlite3", "sqlite3", "bcrypt", "argon2", "canvas", "node-sass", "re2", "isolated-vm", "cpu-features",
	"libxmljs", "libxmljs2", "node-pty", "@serialport/bindings-cpp", "leveldown", "microtime", "farmhash",
	"kerberos", "deasync", "node-expat", "@discordjs/opus", "ffi-napi", "ref-napi",
}

// nodeGlibcOnly ship binaries linked against glibc and none for musl: they
// install on Alpine and fail to load ("Error loading shared library
// ld-linux-x86-64.so.2"). nodeGlibcImplied are packages that depend on one,
// for lockfiles that do not list what they install.
var (
	nodeGlibcOnly    = []string{"onnxruntime-node", "@tensorflow/tfjs-node", "@tensorflow/tfjs-node-gpu"}
	nodeGlibcImplied = map[string]string{"@xenova/transformers": "onnxruntime-node", "@huggingface/transformers": "onnxruntime-node"}
)

var (
	nodeGitPrefixRE    = regexp.MustCompile(`^(?:git\+[a-z]+://|git://|github:|gitlab:|bitbucket:|gist:)`)
	nodeGitShorthandRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9_.-]+(?:#\S*)?$`)
)

// nodeGitSource says whether a package.json dependency specification is a
// Git repository the install clones, and whether it is reached over SSH. A
// tarball URL, a registry range and a local path are not.
func nodeGitSource(spec string) (cloned, ssh bool) {
	if cloned, ssh = nodeGitURL(spec); cloned {
		return cloned, ssh
	}
	// npm reads owner/repo as a GitHub repository.
	return nodeGitShorthandRE.MatchString(strings.TrimSpace(spec)), false
}

// nodeGitURL is nodeGitSource for a lockfile's resolved source, which npm
// always writes as a full URL: there, owner/repo is a workspace or file:
// directory's path ("packages/shared"), never a GitHub shorthand.
func nodeGitURL(source string) (cloned, ssh bool) {
	source = strings.TrimSpace(source)
	switch {
	case strings.HasPrefix(source, "git+ssh://"), strings.HasPrefix(source, "ssh://"), strings.HasPrefix(source, "git@"):
		return true, true
	case nodeGitPrefixRE.MatchString(source):
		return true, false
	case strings.HasPrefix(source, "https://") || strings.HasPrefix(source, "http://"):
		address, _, _ := strings.Cut(source, "#")
		return strings.HasSuffix(address, ".git"), false
	}
	return false, false
}

// gitDependencies lists the packages the install clones, from the package's
// and its workspace root's manifests and from npm's lockfile, and those it
// clones over SSH; a lockfile that resolves one over SSH without naming
// which is counted as the lockfile.
func (f nodeInstallFacts) gitDependencies() (names, ssh []string) {
	cloned, overSSH := map[string]bool{}, map[string]bool{}
	for _, manifest := range []nodeInstallManifest{f.manifest, f.settings} {
		for _, kind := range []map[string]string{manifest.Dependencies, manifest.DevDependencies, manifest.OptionalDependencies} {
			for name, spec := range kind {
				if git, viaSSH := nodeGitSource(spec); git {
					cloned[name] = true
					overSSH[name] = overSSH[name] || viaSSH
				}
			}
		}
	}
	for _, reading := range f.readings {
		for _, name := range reading.git {
			cloned[name] = true
		}
		if reading.gitSSH {
			overSSH[reading.Path] = true
		}
	}
	for name, viaSSH := range overSSH {
		if !viaSSH {
			delete(overSSH, name)
		}
	}
	return sortedNames(cloned), sortedNames(overSSH)
}

// nodeImagePlan is what the image adds to the install: the family, the
// system packages each stage installs, and what the browsers a runtime
// dependency drives need.
type nodeImagePlan struct {
	// buildPackages are installed in the build stage before the source is
	// copied, so they are cached apart from it; runtimePackages in a
	// server's runtime stage.
	buildPackages, runtimePackages []string
	// installEnv is set on the install RUN when the variable has no value of
	// its own; buildEnv and runtimeEnv are ENV lines of the build and
	// runtime stages; runtimeRuns run after the application is copied.
	installEnv             []nodeEnvDefault
	buildEnv, runtimeEnv   []string
	buildRuns, runtimeRuns []string
}

// nodeEnvDefault is a value a RUN gives a variable only when the build
// supplies none, so an operator's own build variable of the same name wins.
type nodeEnvDefault struct{ name, value string }

// assignment is the default as the RUN's shell reads it: "${NAME:-value}"
// is expanded by that shell, never by the Dockerfile parser, and only recipe
// constants are ever written this way.
func (d nodeEnvDefault) assignment() string {
	return d.name + `="${` + d.name + `:-` + d.value + `}"`
}

// nodeImageFamily chooses Debian slim when a package the application loads
// ships glibc binaries only; everything else stays on Alpine.
func nodeImageFamily(facts nodeInstallFacts, assets bool) (string, []string) {
	if assets {
		return nodeFamilyAlpine, nil
	}
	reasons := []string{}
	for _, name := range nodeGlibcOnly {
		if facts.present(name) {
			reasons = append(reasons, name)
		}
	}
	for name, needs := range nodeGlibcImplied {
		if facts.present(name) && !facts.present(needs) {
			reasons = append(reasons, name+" (through "+needs+")")
		}
	}
	for _, name := range []string{"playwright", "playwright-core"} {
		if facts.manifest.Dependencies[name] != "" {
			reasons = append(reasons, name)
		}
	}
	if len(reasons) == 0 {
		return nodeFamilyAlpine, nil
	}
	slices.Sort(reasons)
	return nodeFamilyGlibc, reasons
}

// planNodeImage adds the system packages and settings the application's
// dependencies need, with a finding for each.
func planNodeImage(facts nodeInstallFacts, plan *nodeInstallPlan, glibc []string, assets bool) {
	image := &plan.image
	family := plan.family
	build := map[string]bool{}
	runtime := map[string]bool{}
	add := func(set map[string]bool, packages nodePackageSet) {
		for _, name := range packages.in(family) {
			set[name] = true
		}
	}
	if len(glibc) > 0 {
		plan.findings = append(plan.findings, nodeFinding("glibc_image_selected", PreflightPass,
			"The image is Debian, for packages built for glibc only", strings.Join(glibc, ", "),
			"They ship binaries linked against glibc and none for Alpine's musl, which install and then fail to load; the build and the server run on "+plan.nodeImage()+".",
			"", "configuration.build"))
	}
	addons := []string{}
	for _, name := range nodeNativeAddons {
		if facts.present(name) {
			addons = append(addons, name)
		}
	}
	if len(addons) > 0 {
		add(build, nodeCompilers)
		means := "These compile their native binding when no prebuilt binary matches the platform, Node release and C library; the build stage installs python3, make and g++ for that."
		if slices.Contains(addons, "canvas") && family == nodeFamilyAlpine {
			add(build, nodeCanvasHeaders)
			add(runtime, nodeCanvasLibraries)
			means = "These compile their native binding when no prebuilt binary matches the platform, Node release and C library; the build stage installs python3, make and g++, canvas also cairo, pango and image headers, and the runtime image the libraries canvas links."
		}
		plan.findings = append(plan.findings, nodeFinding("native_addon_toolchain", PreflightPass,
			"Native addons can compile", strings.Join(addons, ", "), means, "", "configuration.build"))
	}
	if names, ssh := facts.gitDependencies(); len(names) > 0 {
		add(build, nodeGit)
		plan.findings = append(plan.findings, nodeFinding("git_dependencies", PreflightPass,
			"Git dependencies are cloned", strings.Join(boundedNames(names), ", "),
			"The package manager clones these from Git, which the Node image lacks; the build stage installs git.", "", "configuration.build"))
		if len(ssh) > 0 {
			add(build, nodeSSH)
			plan.findings = append(plan.findings, nodeFinding("git_dependency_credentials", PreflightWarning,
				"A Git dependency is fetched over SSH", strings.Join(boundedNames(ssh), ", "),
				"The build has no SSH key or known host, so cloning a repository over SSH fails unless the package manager fetches it over HTTPS instead.",
				"Use an https:// URL — for a private repository with a token variable mapped to the install step — or publish the package to a registry.", "configuration.build"))
		}
	}
	if facts.present("prisma") || facts.present("@prisma/client") {
		add(build, nodeOpenSSL)
		if !assets {
			add(runtime, nodeOpenSSL)
		}
		plan.notes = append(plan.notes, "Prisma: openssl is installed so its engines find the TLS library they load")
	}
	if puppeteer := nodePuppeteerPackage(facts.manifest); puppeteer != "" && !assets {
		add(runtime, nodeChromium)
		chromium := "/usr/bin/chromium-browser"
		if family == nodeFamilyGlibc {
			chromium = "/usr/bin/chromium"
		}
		// Puppeteer 19 and later read the first name, older releases the second.
		image.installEnv = append(image.installEnv, nodeEnvDefault{"PUPPETEER_SKIP_DOWNLOAD", "true"}, nodeEnvDefault{"PUPPETEER_SKIP_CHROMIUM_DOWNLOAD", "true"})
		image.runtimeEnv = append(image.runtimeEnv, "PUPPETEER_EXECUTABLE_PATH="+chromium)
		means := "Puppeteer's own Chrome download lands outside the application and never reaches the server image, so the install skips it and the server uses the system Chromium: the image grows by about 300 MB and each browser needs a few hundred MB of memory."
		if puppeteer == "puppeteer-core" {
			means = "puppeteer-core launches the browser the code names; the image installs Chromium and names it in PUPPETEER_EXECUTABLE_PATH for the code to pass as executablePath. The image grows by about 300 MB and each browser needs a few hundred MB of memory."
		}
		plan.findings = append(plan.findings, nodeFinding("headless_browser", PreflightWarning,
			"The server drives a headless browser", puppeteer+" with the image's Chromium ("+chromium+")", means,
			"Launch with args: ['--no-sandbox'], since the container runs as root, and give the service a memory limit with room for the browser.", "configuration.build"))
	}
	if playwright := nodePlaywrightPackage(facts.manifest); playwright != "" && !assets {
		runner := nodeExecRunner(plan.manager)
		image.buildEnv = append(image.buildEnv, "PLAYWRIGHT_BROWSERS_PATH="+nodePlaywrightBrowsers)
		image.runtimeEnv = append(image.runtimeEnv, "PLAYWRIGHT_BROWSERS_PATH="+nodePlaywrightBrowsers)
		image.buildRuns = append(image.buildRuns, runner+" "+playwright+" install chromium")
		image.runtimeRuns = append(image.runtimeRuns, runner+" "+playwright+" install-deps chromium")
		plan.findings = append(plan.findings, nodeFinding("headless_browser", PreflightWarning,
			"The server drives a headless browser", playwright+" with Chromium in "+nodePlaywrightBrowsers,
			"The build downloads Chromium inside the application so it reaches the server, whose image installs Chromium's system libraries: several hundred MB more image and a few hundred MB of memory per browser. Only Chromium is installed.",
			"Launch Chromium (chromium.launch), with chromiumSandbox off since the container runs as root, and give the service a memory limit with room for the browser.", "configuration.build"))
	}
	image.buildPackages, image.runtimePackages = sortedNames(build), sortedNames(runtime)
}

// nodePlaywrightBrowsers keeps Playwright's browsers inside the application
// directory, which is what the runtime stage copies.
const nodePlaywrightBrowsers = "/app/.cache/ms-playwright"

func nodePuppeteerPackage(manifest nodeInstallManifest) string {
	for _, name := range []string{"puppeteer", "puppeteer-core"} {
		if manifest.Dependencies[name] != "" {
			return name
		}
	}
	return ""
}

func nodePlaywrightPackage(manifest nodeInstallManifest) string {
	for _, name := range []string{"playwright", "playwright-core"} {
		if manifest.Dependencies[name] != "" {
			return name
		}
	}
	return ""
}

// nodePackagesLine installs system packages in the family's own way.
func nodePackagesLine(family string, packages []string) string {
	if family == nodeFamilyGlibc {
		return "RUN apt-get update && apt-get install -y --no-install-recommends " + strings.Join(packages, " ") + " && rm -rf /var/lib/apt/lists/*"
	}
	return "RUN apk add --no-cache " + strings.Join(packages, " ")
}

// nodeRunWith renders a RUN that gives each default to the commands that
// follow, unless the build supplied the variable.
func nodeRunWith(mounts string, defaults []nodeEnvDefault, command string) string {
	if len(defaults) == 0 {
		return "RUN " + mounts + command
	}
	assignments := make([]string, 0, len(defaults))
	for _, value := range defaults {
		assignments = append(assignments, value.assignment())
	}
	return "RUN " + mounts + "export " + strings.Join(assignments, " ") + " && " + command
}

func boundedNames(names []string) []string {
	if len(names) > nodeListedNames {
		return append(append([]string(nil), names[:nodeListedNames]...), fmt.Sprintf("and %d more", len(names)-nodeListedNames))
	}
	return names
}
