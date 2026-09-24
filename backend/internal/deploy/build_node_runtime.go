package deploy

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
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
	if images := recipeBaseCatalogue[key]; len(images) > 0 {
		return images[0]
	}
	return recipeBaseCatalogue["node:npm"][0]
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
	nodeExactSpecRE = regexp.MustCompile(`^v?([0-9]+)(?:\.[0-9]+){0,2}$`)
	nodeMinorLineRE = regexp.MustCompile(`^[0-9]+\.[0-9]+$`)
)

// nodeSpecMajor reads a version file's declaration as a major: 22,
// v22.11.0, 22.11, lts/jod, lts/*, node. ok is false for anything else.
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

func nodeMajorSupported(major int) bool {
	for _, candidate := range nodeMajors {
		if candidate == major {
			return true
		}
	}
	return false
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
			if !nodeMajorSupported(major) {
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
		case containsInt(majors, nodeDefaultMajor):
			release.major = nodeDefaultMajor
		default:
			release.major = majors[len(majors)-1]
		}
		return release
	}
	return nodeRelease{major: nodeDefaultMajor, source: "recipe default"}
}

func containsInt(values []int, wanted int) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
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

// installedVersion is the version of a package the install puts in place:
// the exact one a lockfile records, else the lowest its range allows, which
// is the one that matters for a "before this release" rule.
func (f nodeInstallFacts) installedVersion(name string) (nodeVersion, bool) {
	for _, reading := range f.readings {
		if version, ok := parseNodeVersion(reading.names[name]); ok {
			return version, true
		}
	}
	for _, manifest := range []nodeInstallManifest{f.manifest, f.settings} {
		spec := manifest.version(name)
		if spec == "" {
			continue
		}
		if interval, ok := nodeRangeInterval(strings.TrimSpace(strings.Split(spec, "||")[0])); ok && interval.low.set {
			return interval.low.v, true
		}
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
		"Node "+major+" builds and runs this package", release.label(),
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
