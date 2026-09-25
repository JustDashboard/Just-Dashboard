package deploy

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// What the Deno recipe reads beyond deno.json, as bounded data shared by
// detection and preparation: the Deno release the repository declares,
// whether deno.lock still records what deno.json and package.json ask for
// (the Deno twin of a stale package-lock.json), and the file the start
// command runs, whose own imports `deno install` alone does not cache.

// DetectedDeno is what the Deno recipe read, kept on the candidate so
// preflight can judge a plan without the tree.
type DetectedDeno struct {
	// Version is the Deno release the image carries; VersionFrom where the
	// repository declared it, empty for the catalogue default; Declared a
	// declaration the catalogue has no image for, which is built on the
	// nearest release instead.
	Version     string `json:"version,omitempty"`
	VersionFrom string `json:"versionFrom,omitempty"`
	Declared    string `json:"declared,omitempty"`
	// LockStale lists the jsr:, npm: and package.json specifiers deno.lock
	// does not record, which a frozen install refuses.
	LockStale []string `json:"lockStale,omitempty"`
	// WatchStart says the start task runs a development watcher and nothing
	// in the repository serves the built application instead.
	WatchStart bool `json:"watchStart,omitempty"`
}

// denoReleases are the exact releases the catalogue builds each major on:
// Docker Hub publishes no major-only Alpine tag, and a floating "alpine" tag
// moved every rebuild onto whatever major was newest.
var denoReleases = map[int]string{1: "1.46.3", 2: "2.9.7"}

const denoDefaultMajor = 2

var (
	denoExactRE      = regexp.MustCompile(`^v?([0-9]+)\.([0-9]+)\.([0-9]+)$`)
	denoMajorRE      = regexp.MustCompile(`^v?([0-9]+)(?:\.(?:[0-9]+|x))*(?:\.x)?$`)
	denoWorkflowRE   = regexp.MustCompile(`(?m)^\s*deno-version:\s*['"]?([A-Za-z0-9.]+)`)
	denoToolRE       = regexp.MustCompile(`(?m)^\s*deno\s+(\S+)`)
	denoSpecifierRE  = regexp.MustCompile(`^(jsr|npm):(@[A-Za-z0-9._-]+/[A-Za-z0-9._-]+|[A-Za-z0-9._-]+)(?:@([^/]+))?`)
	denoMajorOnlyRE  = regexp.MustCompile(`^\^([0-9]+)\.0\.0$`)
	denoMinorOnlyRE  = regexp.MustCompile(`^~([0-9]+)\.([0-9]+)\.0$`)
	denoEntryFileRE  = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_./-]*\.(?:ts|tsx|js|jsx|mjs|mts)$`)
	denoWatchFlagRE  = regexp.MustCompile(`(?:^|\s)--watch(?:[=\s]|$)`)
	denoDevScriptsRE = regexp.MustCompile(`(?:^|\s)(?:\./)?dev\.ts(?:\s|$)`)
)

// denoProject is everything the recipe decides from a Deno root.
type denoProject struct {
	config         denoConfig
	packageScripts map[string]string
	packageDeps    map[string]string
	release        string
	image          string
	versionFrom    string
	declared       string
	lock           bool
	lockVersion    int
	stale          []string
	// entries says, for each file a task or the Procfile's web process runs,
	// whether the checkout has it; one the build writes it has not.
	entries map[string]bool
}

// denoRelease is where a repository's own declaration puts the build: the
// exact image of an exact release, or the catalogue's release of a major.
func denoRelease(spec string) (release, image, declared string) {
	spec = strings.TrimSpace(spec)
	major := denoDefaultMajor
	if match := denoExactRE.FindStringSubmatch(spec); match != nil {
		major, _ = strconv.Atoi(match[1])
		if _, known := denoReleases[major]; known {
			exact := match[1] + "." + match[2] + "." + match[3]
			return exact, "denoland/deno:alpine-" + exact, ""
		}
	} else if match := denoMajorRE.FindStringSubmatch(spec); match != nil {
		major, _ = strconv.Atoi(match[1])
	}
	if _, known := denoReleases[major]; !known {
		declared = spec
		major = denoDefaultMajor
	}
	return denoReleases[major], recipeBaseCatalogue["deno:"+strconv.Itoa(major)][0], declared
}

// denoVersionDeclaration reads the release a repository asks for: dvm's
// .dvmrc and asdf's .tool-versions from the root upwards to the checkout,
// then the deno-version of setup-deno in the checkout's workflows.
func denoVersionDeclaration(files nodeFiles, top nodeFiles) (spec, source string) {
	directory := files.dir
	for {
		here := nodeFiles{root: files.root, dir: directory, budget: files.budget}
		if content, err := here.read(".dvmrc", 1024); err == nil {
			if value := strings.TrimSpace(strings.SplitN(string(content), "\n", 2)[0]); value != "" {
				return value, path.Join(directory, ".dvmrc")
			}
		}
		if content, err := here.read(".tool-versions", 4096); err == nil {
			if match := denoToolRE.FindSubmatch(content); match != nil {
				return string(match[1]), path.Join(directory, ".tool-versions")
			}
		}
		if directory == "" || directory == "." || directory == top.dir {
			break
		}
		directory = path.Dir(directory)
		if directory == "." {
			directory = ""
		}
	}
	workflows := top.sub(".github/workflows")
	for index, entry := range phpListDirectory(workflows, "") {
		if index >= 16 {
			break
		}
		if !strings.HasSuffix(entry.Name(), ".yml") && !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		if content, err := workflows.read(entry.Name(), 64<<10); err == nil {
			if match := denoWorkflowRE.FindSubmatch(content); match != nil {
				return string(match[1]), path.Join(workflows.dir, entry.Name())
			}
		}
	}
	return "", ""
}

// readDenoProject reads a Deno root; top is the checkout it sits in.
func readDenoProject(files nodeFiles, top nodeFiles) denoProject {
	project := denoProject{}
	for _, name := range []string{"deno.json", "deno.jsonc"} {
		if content, err := files.read(name, 512<<10); err == nil {
			project.config = parseDenoConfig(content)
			break
		}
	}
	if content, err := files.read("package.json", 512<<10); err == nil {
		var manifest nodeManifest
		if parseNodeManifest(content, &manifest) {
			project.packageScripts = manifest.Scripts
			project.packageDeps = map[string]string{}
			for name, spec := range manifest.Dependencies {
				project.packageDeps[name] = spec
			}
			for name, spec := range manifest.DevDependencies {
				project.packageDeps[name] = spec
			}
		}
	}
	spec, source := denoVersionDeclaration(files, top)
	project.release, project.image, project.declared = denoRelease(spec)
	if spec != "" && project.declared == "" {
		project.versionFrom = source
	}
	project.lock = files.exists("deno.lock")
	if project.lock {
		project.readLock(files)
	}
	project.entries = map[string]bool{}
	commands := []string{}
	for name := range project.config.Tasks {
		commands = append(commands, project.config.task(name))
	}
	for _, script := range project.packageScripts {
		commands = append(commands, script)
	}
	project.readEntries(files, commands)
	return project
}

func (p *denoProject) readEntries(files nodeFiles, commands []string) {
	for _, command := range commands {
		if entry := denoCommandEntry(command, p.task); entry != "" && len(p.entries) < 32 {
			p.entries[entry] = files.exists(entry)
		}
	}
}

// denoLockFile is the part of deno.lock that records what the workspace
// asked for, by the specifiers deno.json and package.json wrote.
type denoLockFile struct {
	Version   string `json:"version"`
	Workspace struct {
		Dependencies []string `json:"dependencies"`
		PackageJSON  struct {
			Dependencies []string `json:"dependencies"`
		} `json:"packageJson"`
	} `json:"workspace"`
}

// readLock compares deno.lock with what the workspace asks for. Deno writes
// a requested specifier the way it was written except that ^X.0.0 becomes X
// and ~X.Y.0 becomes X.Y; one missing from the lock is one a frozen install
// refuses. Locks before version 3 record no workspace and are not judged.
func (p *denoProject) readLock(files nodeFiles) {
	file, err := files.open("deno.lock", 512<<10)
	if err != nil {
		return
	}
	defer file.Close()
	var lock denoLockFile
	if json.NewDecoder(io.LimitReader(file, 512<<10)).Decode(&lock) != nil {
		return
	}
	p.lockVersion, _ = strconv.Atoi(lock.Version)
	if p.lockVersion < 3 {
		return
	}
	recorded := map[string]bool{}
	for _, specifier := range append(lock.Workspace.Dependencies, lock.Workspace.PackageJSON.Dependencies...) {
		recorded[specifier] = true
	}
	requested := []string{}
	for _, value := range p.config.Imports {
		if specifier := denoLockSpecifier(value); specifier != "" {
			requested = append(requested, specifier)
		}
	}
	for name, spec := range p.packageDeps {
		if strings.HasPrefix(spec, "npm:") || strings.HasPrefix(spec, "jsr:") {
			if specifier := denoLockSpecifier(spec); specifier != "" {
				requested = append(requested, specifier)
			}
			continue
		}
		if spec == "" || strings.ContainsAny(spec, ":/") {
			// A path, a URL or a workspace link is not a registry package.
			continue
		}
		requested = append(requested, "npm:"+name+"@"+denoNormalizedRange(spec))
	}
	sort.Strings(requested)
	seen := map[string]bool{}
	for _, specifier := range requested {
		if !recorded[specifier] && !seen[specifier] && len(p.stale) < 32 {
			seen[specifier] = true
			p.stale = append(p.stale, specifier)
		}
	}
}

// denoLockSpecifier is an import's registry package the way deno.lock
// records it, without a subpath.
func denoLockSpecifier(value string) string {
	match := denoSpecifierRE.FindStringSubmatch(strings.TrimSpace(value))
	if match == nil {
		return ""
	}
	specifier := match[1] + ":" + match[2]
	if match[3] != "" {
		specifier += "@" + denoNormalizedRange(match[3])
	}
	return specifier
}

func denoNormalizedRange(spec string) string {
	if match := denoMajorOnlyRE.FindStringSubmatch(spec); match != nil {
		return match[1]
	}
	if match := denoMinorOnlyRE.FindStringSubmatch(spec); match != nil {
		return match[1] + "." + match[2]
	}
	return spec
}

// denoCommandEntry is the file a start command runs: `deno run … <file>`
// or `deno serve … <file>`, through `deno task` when it names a task.
func denoCommandEntry(command string, tasks func(string) string) string {
	for depth := 0; depth < 3; depth++ {
		fields := strings.Fields(command)
		if len(fields) >= 3 && fields[0] == "deno" && fields[1] == "task" {
			command = tasks(fields[2])
			continue
		}
		break
	}
	for _, segment := range strings.Split(command, "&&") {
		fields := strings.Fields(segment)
		if len(fields) < 3 || fields[0] != "deno" || (fields[1] != "run" && fields[1] != "serve") {
			continue
		}
		for _, field := range fields[2:] {
			if strings.HasPrefix(field, "-") {
				continue
			}
			entry := strings.TrimPrefix(field, "./")
			if denoEntryFileRE.MatchString(entry) && safeRelativePath(entry) {
				return entry
			}
		}
	}
	return ""
}

// task reads a task from deno.json, else package.json's scripts, which
// `deno task` runs too.
func (p denoProject) task(name string) string {
	if task := p.config.task(name); task != "" {
		return task
	}
	return strings.TrimSpace(p.packageScripts[name])
}

// denoWatcher says a task runs a development server: a file watcher, or
// Fresh 1's dev.ts.
func denoWatcher(task string) bool {
	return denoWatchFlagRE.MatchString(task) || denoDevScriptsRE.MatchString(task)
}

// denoRunsPackage says a root's package.json is a Deno project's: its start
// script runs deno, or deno.lock is the only lockfile. A build script that
// calls deno on the way to another server does not make Deno the runtime.
func (m *detectedMarkers) denoRunsPackage() bool {
	if len(m.packageJSON) == 0 {
		return false
	}
	var manifest nodeManifest
	if !parseNodeManifest(m.packageJSON, &manifest) {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(manifest.Scripts["start"]), "deno ") || (m.denoLock && len(m.lockfiles) == 0)
}

// readDenoProjects reads every Deno root. A package.json project Deno runs
// is described to the Deno candidate as the deno.json it would have, whose
// tasks are the scripts, since `deno task` runs those.
func readDenoProjects(checkout string, markers map[string]*detectedMarkers) {
	root, err := os.OpenRoot(checkout)
	if err != nil {
		return
	}
	defer root.Close()
	top := nodeFiles{root: root, budget: newNodeReadBudget()}
	for _, marker := range markers {
		if len(marker.denoJSON) == 0 && marker.denoRunsPackage() {
			var manifest nodeManifest
			if parseNodeManifest(marker.packageJSON, &manifest) {
				tasks := map[string]string{}
				for name, script := range manifest.Scripts {
					tasks[name] = script
				}
				if synthesized, err := json.Marshal(map[string]any{"tasks": tasks}); err == nil {
					marker.denoJSON, marker.denoJSONPath = synthesized, "package.json"
				}
			}
		}
		if len(marker.denoJSON) == 0 {
			continue
		}
		files := nodeFiles{root: root, dir: filepath.ToSlash(marker.root), budget: top.budget}
		project := readDenoProject(files, top)
		project.readEntries(files, []string{procfileProcess(marker.procfile, "web")})
		marker.deno = &project
	}
}

// openDenoProject reads the Deno root at a build root, the way detection
// read it, looking for version declarations up to the checkout.
func openDenoProject(boundary, dir string) (denoProject, error) {
	root, err := os.OpenRoot(boundary)
	if err != nil {
		return denoProject{}, err
	}
	defer root.Close()
	budget := newNodeReadBudget()
	return readDenoProject(nodeFiles{root: root, dir: checkoutPath(boundary, dir), budget: budget}, nodeFiles{root: root, budget: budget}), nil
}

// demoteNodeForDeno lowers the Node candidate of a package.json that Deno
// runs: its scripts call deno, which the Node image does not have.
func demoteNodeForDeno(marker *detectedMarkers, candidates []DetectedCandidate) {
	if !marker.denoRunsPackage() {
		return
	}
	for index := range candidates {
		if candidates[index].Recipe == "node" {
			candidates[index].Confidence = ConfidenceLow
			candidates[index].Evidence = append(candidates[index].Evidence, DetectionEvidence{
				Path: joinRoot(marker.root, "package.json"), Reason: "scripts run deno, or deno.lock is the only lockfile: the Deno candidate builds this package",
			})
		}
	}
}

// validateDetectedPHPDeno bounds the PHP and Deno facts a saved draft
// carries, the way every other detected field is.
func validateDetectedPHPDeno(candidate DetectedCandidate) error {
	malformed := fmt.Errorf("%w: detected PHP or Deno facts are malformed", ErrInvalidPlan)
	text := func(value string, limit int) bool {
		return len(value) <= limit && !strings.ContainsAny(value, "\x00\r\n") && rejectPlanSecretLiteral("detected facts", value) == nil
	}
	list := func(values []string) bool {
		if len(values) > phpListedNames {
			return false
		}
		for _, value := range values {
			if value == "" || !text(value, 512) {
				return false
			}
		}
		return true
	}
	if facts := candidate.PHP; facts != nil {
		if !text(facts.Version, 8) || !list(facts.Constraints) || !list(facts.LockMissing) || !list(facts.LockOutdated) ||
			!list(facts.DevLockOutdated) || !list(facts.DevProviders) || len(facts.Extensions) > phpListedNames ||
			len(facts.Unsupported) > phpListedNames || !text(facts.Lock, 16) || !text(facts.AssetOutput, 256) ||
			(facts.AssetOutput != "" && !safeRelativePath(facts.AssetOutput)) || !text(facts.WordPress, 16) || !text(facts.ServerConfig, 512) {
			return malformed
		}
		for _, extension := range append(append([]DetectedPHPExtension{}, facts.Extensions...), facts.Unsupported...) {
			if extension.Name == "" || !text(extension.Name, 64) || !text(extension.Reason, 512) {
				return malformed
			}
		}
	}
	if facts := candidate.Deno; facts != nil {
		if !text(facts.Version, 32) || !text(facts.VersionFrom, 512) || !text(facts.Declared, 64) || !list(facts.LockStale) {
			return malformed
		}
	}
	return nil
}
