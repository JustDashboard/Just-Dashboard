package deploy

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Ruby, Elixir, Scala, Clojure, Dart and Gleam have recipes of their own
// (frameworks_ruby.go, frameworks_elixir.go, frameworks_jvm_languages.go,
// frameworks_dart.go, frameworks_gleam.go). Their roots are recognised by
// the repository-shape pass (detect_ecosystems.go), which also takes their
// asset pipelines out of the Node candidates; this file turns such a root
// into a recipe candidate, reading the same files with the same functions
// the recipe prepares from, so what detection proposes is what the build
// does.

// languageRecipes are the recipes detection builds from an ecosystem match
// rather than from a root's markers.
var languageRecipes = []string{"ruby", "elixir", "scala", "clojure", "dart", "gleam"}

func languageRecipe(recipe string) bool { return slices.Contains(languageRecipes, recipe) }

// DetectedToolchain is what one of those recipes read from the repository's
// own files: the release it builds on and the file that chose it, and the
// facts preflight judges against the host and the plan without the tree.
type DetectedToolchain struct {
	// Language is the recipe the facts belong to.
	Language string `json:"language"`
	// Release is the image family or exact release ("3.4", "3.4.7",
	// "1.19-otp-28"), From the file that chose it.
	Release string `json:"release,omitempty"`
	From    string `json:"from,omitempty"`
	// Tool is how the project builds when the language has several ways:
	// sbt-stage or sbt-assembly, lein or tools-deps, dart-frog or dart.
	Tool string `json:"tool,omitempty"`
	// LockPlatforms are Gemfile.lock's PLATFORMS. BundleFrozen says a
	// repository Dockerfile installs the bundle frozen, where a lock
	// without the server's platform fails the install, and AddedPlatforms
	// are those its `bundle lock --add-platform` adds to the lock first.
	LockPlatforms  []string `json:"lockPlatforms,omitempty"`
	BundleFrozen   bool     `json:"bundleFrozen,omitempty"`
	AddedPlatforms []string `json:"addedPlatforms,omitempty"`
	// Bundler is the lock's BUNDLED WITH release, which RubyGems runs;
	// GitSSH says a gem comes from a Git repository over SSH, which the
	// build has no key for.
	Bundler string `json:"bundler,omitempty"`
	GitSSH  bool   `json:"gitSsh,omitempty"`
}

var toolchainTextRE = regexp.MustCompile(`^[A-Za-z0-9._@+/:-]{0,128}$`)

func validateDetectedToolchain(toolchain *DetectedToolchain) error {
	if toolchain == nil {
		return nil
	}
	malformed := fmt.Errorf("%w: detected toolchain is malformed", ErrInvalidPlan)
	if !languageRecipe(toolchain.Language) || len(toolchain.LockPlatforms) > 32 || len(toolchain.AddedPlatforms) > 32 {
		return malformed
	}
	values := append([]string{toolchain.Release, toolchain.From, toolchain.Tool, toolchain.Bundler}, toolchain.LockPlatforms...)
	for _, value := range append(values, toolchain.AddedPlatforms...) {
		if !toolchainTextRE.MatchString(value) {
			return malformed
		}
	}
	return nil
}

// bounded drops what a repository wrote that a saved detection would
// refuse — a BUNDLED WITH line of prose, a platform with a space — so one odd
// file costs that fact and never the import.
func (t *DetectedToolchain) bounded() *DetectedToolchain {
	if t == nil {
		return nil
	}
	clean := *t
	for _, field := range []*string{&clean.Release, &clean.From, &clean.Tool, &clean.Bundler} {
		if !toolchainTextRE.MatchString(*field) {
			*field = ""
		}
	}
	clean.LockPlatforms, clean.AddedPlatforms = boundedPlatforms(t.LockPlatforms), boundedPlatforms(t.AddedPlatforms)
	return &clean
}

func boundedPlatforms(platforms []string) []string {
	var kept []string
	for _, platform := range platforms {
		if toolchainTextRE.MatchString(platform) && len(kept) < 32 {
			kept = append(kept, platform)
		}
	}
	return kept
}

// readRecipeFile reads a small file a language recipe decides from, bounded
// and never through a symlink out of the root; a missing or unreadable file
// reads as absent.
func readRecipeFile(root, name string, limit int64) []byte {
	if !regularExists(root, name) {
		return nil
	}
	content, err := readContainedRegular(root, name, limit)
	if err != nil {
		return nil
	}
	return content
}

// directoryHasEntries says a real directory (not a link) holds anything.
func directoryHasEntries(root, name string) bool {
	info, err := os.Lstat(filepath.Join(root, filepath.Clean(name)))
	if err != nil || !info.IsDir() {
		return false
	}
	entries, err := os.ReadDir(filepath.Join(root, filepath.Clean(name)))
	return err == nil && len(entries) > 0
}

// Version constraints. RubyGems, Hex and pub write them differently but
// mean the same few things: `~> 1.4` is 1.4 up to 2.0 and `~> 1.4.2` is
// 1.4.2 up to 1.5 (RubyGems and Hex alike), pub's `^3.5.0` is 3.5.0 up to
// 4.0, a bare version is that version, and clauses are joined by `,` or
// `and` and alternatives by `or` or `||`.

type languageVersion [3]int

var languageVersionRE = regexp.MustCompile(`^v?(\d+)(?:\.(\d+))?(?:\.(\d+))?`)

// parseLanguageVersion reads the leading numbers of a version, counting how
// many were written, so `3.4` and `3.4.0` can be told apart.
func parseLanguageVersion(text string) (languageVersion, int, bool) {
	match := languageVersionRE.FindStringSubmatch(strings.TrimSpace(text))
	if match == nil {
		return languageVersion{}, 0, false
	}
	var version languageVersion
	parts := 0
	for index := 1; index <= 3; index++ {
		if match[index] == "" {
			break
		}
		version[index-1], _ = strconv.Atoi(match[index])
		parts++
	}
	return version, parts, true
}

func (v languageVersion) less(other languageVersion) bool {
	for index := range v {
		if v[index] != other[index] {
			return v[index] < other[index]
		}
	}
	return false
}

func (v languageVersion) String() string {
	return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2])
}

type versionClause struct {
	operator string
	version  languageVersion
	parts    int
}

func (c versionClause) allows(version languageVersion) bool {
	switch c.operator {
	case "=", "==", "":
		// A version written with fewer parts names the family: `3.4` is
		// every 3.4 release.
		for index := 0; index < c.parts; index++ {
			if version[index] != c.version[index] {
				return false
			}
		}
		return true
	case "!=":
		return version != c.version
	case ">":
		return c.version.less(version)
	case ">=":
		return !version.less(c.version)
	case "<":
		return version.less(c.version)
	case "<=":
		return !c.version.less(version)
	case "~>":
		upper := c.version
		switch {
		case c.parts <= 1:
			upper = languageVersion{c.version[0] + 1, 0, 0}
		case c.parts == 2:
			upper = languageVersion{c.version[0] + 1, 0, 0}
		default:
			upper = languageVersion{c.version[0], c.version[1] + 1, 0}
		}
		return !version.less(c.version) && version.less(upper)
	case "^":
		upper := languageVersion{c.version[0] + 1, 0, 0}
		if c.version[0] == 0 {
			upper = languageVersion{0, c.version[1] + 1, 0}
		}
		return !version.less(c.version) && version.less(upper)
	}
	return false
}

var versionClauseRE = regexp.MustCompile(`(~>|\^|>=|<=|==|!=|>|<|=)?\s*(v?\d+(?:\.\d+){0,2})`)

// versionConstraint is a parsed requirement: alternatives of clauses that
// must all hold. A constraint that names nothing ("any", "*", "") allows
// every version.
type versionConstraint [][]versionClause

func parseVersionConstraint(text string) (versionConstraint, bool) {
	text = strings.TrimSpace(strings.Trim(strings.TrimSpace(text), `"'`))
	if text == "" || text == "any" || text == "*" {
		return nil, true
	}
	var constraint versionConstraint
	for _, alternative := range regexp.MustCompile(`\s+or\s+|\|\|`).Split(text, -1) {
		var clauses []versionClause
		for _, match := range versionClauseRE.FindAllStringSubmatch(alternative, -1) {
			version, parts, ok := parseLanguageVersion(match[2])
			if !ok {
				return nil, false
			}
			clauses = append(clauses, versionClause{operator: match[1], version: version, parts: parts})
		}
		if len(clauses) == 0 {
			return nil, false
		}
		constraint = append(constraint, clauses)
	}
	return constraint, true
}

func (c versionConstraint) allows(version languageVersion) bool {
	if len(c) == 0 {
		return true
	}
	for _, clauses := range c {
		all := true
		for _, clause := range clauses {
			all = all && clause.allows(version)
		}
		if all {
			return true
		}
	}
	return false
}

// allowsFamily says whether any release of a major.minor family the
// constraint could accept: its first release, or one far along in it.
func (c versionConstraint) allowsFamily(family string) bool {
	version, _, ok := parseLanguageVersion(family)
	if !ok {
		return false
	}
	for _, patch := range []int{0, 1, 5, 9, 20, 99} {
		version[2] = patch
		if c.allows(version) {
			return true
		}
	}
	return false
}

// toolVersionsEntry reads one tool's line from asdf's or mise's
// .tool-versions: `ruby 3.4.7`, `elixir 1.17.3-otp-27`.
func toolVersionsEntry(content []byte, tool string) string {
	for _, raw := range strings.Split(string(content), "\n") {
		fields := strings.Fields(strings.SplitN(raw, "#", 2)[0])
		if len(fields) >= 2 && fields[0] == tool {
			return fields[1]
		}
	}
	return ""
}

// Node in a language's build stage. A Rails or Hanami asset pipeline, and a
// Phoenix application's npm dependencies, run inside the language's own
// build: `rails assets:precompile` invokes the package manager itself. The
// JavaScript install is planned by the Node recipe's planner — the same
// lockfile rules, pinned manager releases and findings — on the Debian Node
// image, whose toolchain is copied into the language's Debian image under
// /opt/node rather than over its /usr/local.

type languageNodeAssets struct {
	plan   nodeInstallPlan
	source nodeInstallSource
	// dir is the package's directory under the build root ("" or "assets").
	dir    string
	inputs []string
	// runtimeOnly is Node without an install: ExecJS's runtime for a
	// Sprockets pipeline that compresses with Terser or Uglifier.
	runtimeOnly bool
}

// planLanguageNodeAssets plans the install of the package at dir under root
// for a Debian build stage; choice carries the plan's package manager and
// Node version, and the language's own programs the stage runs beside Node.
func planLanguageNodeAssets(root, dir, arch string, choice nodeInstallChoice) (*languageNodeAssets, error) {
	source, err := readNodeInstallSource(root, dir, arch, newNodeReadBudget())
	if err != nil {
		return nil, err
	}
	choice.build, choice.assets = "npm run build", true
	plan := planNodeInstall(source.facts, choice)
	if plan.blocked != nil {
		return nil, plan.blockedError()
	}
	// Node's binary is copied into a Debian image, so it has to be the
	// glibc build, and Bun's with it.
	plan.family = nodeFamilyGlibc
	if plan.bun != "" {
		plan.bun = bunImage(bunReleaseOf(plan.bun), nodeFamilyGlibc)
	}
	inputs := []string{}
	for _, input := range source.installInputs(plan) {
		inputs = append(inputs, path.Join(dir, input))
	}
	return &languageNodeAssets{plan: plan, source: source, dir: dir, inputs: inputs}, nil
}

// languageNodeRuntime is Node alone, for ExecJS.
func languageNodeRuntime() *languageNodeAssets {
	return &languageNodeAssets{runtimeOnly: true, plan: nodeInstallPlan{node: nodeRelease{major: nodeDefaultMajor}, family: nodeFamilyGlibc}}
}

// bases are the images the stage copies from, in the order the Dockerfile
// names them.
func (a *languageNodeAssets) bases() []string {
	images := []string{a.plan.nodeImage()}
	if a.plan.bun != "" {
		images = append(images, a.plan.bun)
	}
	return images
}

// toolchainStage renders the Node stage the build stage copies from.
func (a *languageNodeAssets) toolchainStage(bases []ResolvedImage) ([]string, error) {
	node, err := resolveCatalogueImage(bases, a.plan.nodeImage())
	if err != nil {
		return nil, err
	}
	bun := ""
	if a.plan.bun != "" {
		image, err := resolveCatalogueImage(bases, a.plan.bun)
		if err != nil {
			return nil, err
		}
		bun = immutableImageReference(image)
	}
	return append([]string{"FROM " + immutableImageReference(node) + " AS node-toolchain"}, a.plan.toolchainLines(bun)...), nil
}

// copyLines put the toolchain on the build stage's PATH. The relative
// links of npm, npx, Corepack's shims and bunx still resolve under
// /opt/node; Yarn 1 lives in /opt already.
func (a *languageNodeAssets) copyLines() []string {
	lines := []string{
		"COPY --from=node-toolchain /usr/local/ /opt/node/",
		"COPY --from=node-toolchain /opt/ /opt/",
		"ENV PATH=/opt/node/bin:$PATH",
	}
	if len(a.plan.corepack) > 0 {
		env := "ENV COREPACK_HOME=/opt/corepack COREPACK_ENABLE_DOWNLOAD_PROMPT=0 COREPACK_ENABLE_AUTO_PIN=0 COREPACK_ENABLE_NETWORK=0"
		if !a.plan.strict {
			env += " COREPACK_ENABLE_STRICT=0"
		}
		lines = append(lines, env)
	}
	return lines
}

// installLines run the planned install in the package's directory.
func (a *languageNodeAssets) installLines(installSecrets string) []string {
	if a.runtimeOnly {
		return nil
	}
	lines := []string{}
	if a.dir != "" {
		lines = append(lines, "WORKDIR /app/"+a.dir)
	}
	if a.plan.berry {
		// Plug'n'Play resolves from the cache, which has to stay in the
		// application the build copies.
		lines = append(lines, "ENV YARN_ENABLE_GLOBAL_CACHE=false")
	}
	lines = append(lines, nodeRunWith(installSecrets, a.plan.installDefaults(), a.plan.installLine()))
	if a.dir != "" {
		lines = append(lines, "WORKDIR /app")
	}
	return lines
}

// debianPackagesLine installs Debian packages in one layer.
func debianPackagesLine(packages []string) string {
	return "RUN apt-get update -qq && apt-get install --no-install-recommends -y " + strings.Join(packages, " ") + " && rm -rf /var/lib/apt/lists/*"
}

// largestJarLine copies the largest jar a JVM build left under dirs to
// /out/app.jar. An uberjar holds every dependency, so it outweighs the thin
// jar, the sources and the javadoc a build writes beside it.
func largestJarLine(tool string, dirs ...string) string {
	return `RUN mkdir -p /out && jar="$(find ` + strings.Join(dirs, " ") + ` -name '*.jar' -not -name '*-sources.jar' -not -name '*-javadoc.jar' -type f -exec ls -S {} + 2>/dev/null | head -n 1)" && [ -n "$jar" ] && cp "$jar" /out/app.jar || (echo '` + tool + ` produced no jar under ` + strings.Join(dirs, " ") + `' >&2; exit 1)`
}

// unprivilegedDebianUser is the Debian and Ubuntu form of the compiled
// recipes' `app` user, with a data directory a volume can stand on.
const unprivilegedDebianUser = "RUN (getent passwd 10001 >/dev/null || useradd --uid 10001 --user-group --home-dir /app --no-create-home --shell /usr/sbin/nologin app) && mkdir -p /app/data && chown 10001:10001 /app /app/data"

// languageStartListen reads the port and bind of a language recipe's start
// command, which passes $PORT to the server rather than leaving it to code
// detection cannot read. Facts the recipe's reader took from the code
// (Gleam's mist builder) stand: that start command is only a launcher.
func languageStartListen(c *DetectedCandidate) {
	if strings.TrimSpace(c.StartCommand) == "" || c.Listen != nil {
		return
	}
	settleListen(c, listenInputs{command: parseCommandListen(c.StartCommand, nil), hasCommand: true})
}

// addLanguageRecipeCandidate adds the recipe candidate for an ecosystem the
// repository-shape pass recognised at root, with the processes and
// databases its manifests name. The registry credentials its install reads
// are the root's variables, whichever of the root's candidates is built.
func (s *repoShapeScan) addLanguageRecipeCandidate(result *DetectionResult, root string, match *ecosystemMatch, context shapeContext, evidence []DetectionEvidence) {
	buildRoot := filepath.Join(s.root, filepath.FromSlash(root))
	var candidate DetectedCandidate
	switch match.recipe {
	case "ruby":
		candidate = rubyCandidate(buildRoot, root, match)
	case "elixir":
		candidate = elixirCandidate(buildRoot, root, match)
	case "scala", "clojure":
		candidate = jvmLanguageCandidate(buildRoot, root, match)
	case "dart":
		candidate = dartCandidate(buildRoot, root, match)
	case "gleam":
		candidate = gleamCandidate(buildRoot, root, match)
	default:
		return
	}
	candidate.Evidence = append(append([]DetectionEvidence(nil), evidence...), candidate.Evidence...)
	candidate.Toolchain = candidate.Toolchain.bounded()
	install := []DetectedVariable{}
	for _, variable := range candidate.Variables {
		if variable.Step == "install" {
			install = append(install, variable)
		}
	}
	variables := withInstallVariables(context.variables(root), install)
	candidate.Variables = variables
	candidate.Processes = appendProcesses(nil, match.processes...)
	candidate.Databases = appendDatabases(context.databases(root, variables), match.databases...)
	if len(install) > 0 {
		for index := range result.Candidates {
			if result.Candidates[index].Root == root {
				result.Candidates[index].Variables = withInstallVariables(result.Candidates[index].Variables, install)
			}
		}
	}
	result.Candidates = append(result.Candidates, newDetectedCandidate(root, BuildRecipe, candidate))
}

// languageReadiness is the check a language recipe's candidate carries: the
// health route Rails declares, else a strict / for a Rails application with
// pages, else any answer from / — a Rails API, a Rack or Plug service, a
// JVM, Dart or Gleam server may well have nothing at the root.
func languageReadiness(candidate *DetectedCandidate, facts rootFacts) *DetectedReadiness {
	if candidate.Recipe == "ruby" && candidate.Framework == "rails" {
		for _, fact := range facts.of(factDeclaredHealth) {
			if fact.stack == "rails" {
				return strictReadiness(fact.value, readinessFromFramework, fact.label)
			}
		}
		if facts.has(factRailsAPIOnly) {
			return answeredReadiness("/", readinessFromConvention, "a Rails API application has no page at /; any answer shows it is serving")
		}
	}
	label := map[string]string{"rails": "Rails", "hanami": "Hanami", "sinatra": "Sinatra", "phoenix": "Phoenix", "play": "Play"}[candidate.Framework]
	if label == "" {
		label = "the " + recipeLabel(candidate.Recipe) + " service"
	}
	readiness := answeredReadiness("/", readinessFromConvention, "no health route found; any answer from "+label+" at / shows it is serving")
	// Any answer passes readiness, a redirect included, so a Phoenix
	// endpoint that sends every plain-HTTP request to HTTPS is named here.
	if forced := facts.of(factPhoenixForceSSL); candidate.Recipe == "elixir" && len(forced) > 0 {
		readiness.HTTPSRedirect = "force_ssl in " + forced[0].file
		readiness.HTTPSRedirectIgnoresProxy = forced[0].value != "proxy"
	}
	return readiness
}

// languageToolRecipe is the recipe that installs a language, by the name
// the build-failure remedies give it.
var languageToolRecipe = map[string]string{
	"Ruby": "ruby", "Elixir": "elixir", "Scala": "scala", "Clojure": "clojure", "Dart": "dart", "Gleam": "gleam",
}
