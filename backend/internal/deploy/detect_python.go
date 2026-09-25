package deploy

import (
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// A Python root's candidate needs more of the tree than its manifests and
// conventional entry files, all of it read after the walk under a budget of
// its own (readPythonSources): the root's other scripts (a Streamlit Home.py,
// an app in backend.py), the module a run.py imports its factory from, a
// manage.py below the manifest, the Django settings module, the version
// files of the directories above a monorepo member, and the requirement
// files an install file includes. What it finds becomes DetectedPython.

// DetectedPython is what detection read about a Python root beyond its
// framework, for the recipe's evidence and for preflight to judge the plan
// against without the tree.
type DetectedPython struct {
	// InstallFile is the manifest the recipe installs from and Toolchain
	// the pinned tool it installs with.
	InstallFile string `json:"installFile,omitempty"`
	Toolchain   string `json:"toolchain,omitempty"`
	// Lock is the lock compared with its manifest; ManifestConflict the
	// pyproject dependencies a requirement file installed instead omits.
	Lock             *DetectedLockfile `json:"lock,omitempty"`
	ManifestConflict []string          `json:"manifestConflict,omitempty"`
	// DependenciesEmpty is set when the manifests install nothing: "warning"
	// in general, "blocked" when the source certainly needs a framework.
	DependenciesEmpty string `json:"dependenciesEmpty,omitempty"`
	// VersionSource is what chose the interpreter; VersionRaised a declared
	// family below the catalogue the build moved to 3.10; VersionLimited
	// the pins that kept the default lower; WheelBlockers, per family, the
	// pins that publish nothing that family can install.
	VersionSource  string              `json:"versionSource,omitempty"`
	VersionRaised  string              `json:"versionRaised,omitempty"`
	VersionLimited []string            `json:"versionLimited,omitempty"`
	WheelBlockers  map[string][]string `json:"wheelBlockers,omitempty"`
	// LocalArtifacts are pip freeze leftovers the build rewrites: conda
	// builds installed by name, another system's packages left out;
	// LocalPaths the requirements on a path of the machine that wrote them,
	// which the build leaves out; CondaConverted the requirements a conda
	// environment became.
	LocalArtifacts []string `json:"localArtifacts,omitempty"`
	LocalPaths     []string `json:"localPaths,omitempty"`
	CondaConverted []string `json:"condaConverted,omitempty"`
	// PrivateIndexes are package index hosts beyond the public ones,
	// InlineCredentials the files that commit a credential in an index or
	// dependency URL, and GitSSH the dependencies cloned over SSH.
	PrivateIndexes    []string `json:"privateIndexes,omitempty"`
	InlineCredentials []string `json:"inlineCredentials,omitempty"`
	GitSSH            []string `json:"gitSSH,omitempty"`
	// CPUTorch says the pip install takes PyTorch's CPU build; GPUWheels are
	// the NVIDIA libraries a lock pins.
	CPUTorch  bool     `json:"cpuTorch,omitempty"`
	GPUWheels []string `json:"gpuWheels,omitempty"`
	// Workspace is the uv workspace root a member installs from.
	Workspace string `json:"workspace,omitempty"`
	// Modules are the top-level names the root's code can be imported by,
	// for judging a start command's module.
	Modules []string `json:"modules,omitempty"`
	// StreamlitSecrets are the st.secrets keys the start command writes into
	// .streamlit/secrets.toml, and StreamlitNested the tables it cannot.
	StreamlitSecrets []string `json:"streamlitSecrets,omitempty"`
	StreamlitNested  []string `json:"streamlitNested,omitempty"`
	// GradioLoopback names the script whose launch() binds loopback, and
	// WebsocketLibraryMissing the one that serves websockets through a
	// uvicorn without a websocket library.
	GradioLoopback          string `json:"gradioLoopback,omitempty"`
	WebsocketLibraryMissing string `json:"websocketLibraryMissing,omitempty"`
	// Assets is the directory of the package.json the recipe's Node stage
	// builds ("." for the root).
	Assets string          `json:"assets,omitempty"`
	Django *DetectedDjango `json:"django,omitempty"`
}

// DetectedDjango is what a Django project's settings say, read as text.
type DetectedDjango struct {
	// ManageDir is manage.py's directory below the root, when it is not the
	// root itself.
	ManageDir string `json:"manageDir,omitempty"`
	// SettingsModule is the module the server runs with; SettingsSource
	// where each entry point names it; Seeded that the plan sets it, since
	// manage.py and the server default to different modules.
	SettingsModule string `json:"settingsModule,omitempty"`
	SettingsSource string `json:"settingsSource,omitempty"`
	Seeded         bool   `json:"seeded,omitempty"`
	// Development says the module is a development one with nothing better
	// beside it that would run, and why.
	Development string `json:"development,omitempty"`
	StaticFiles bool   `json:"staticFiles,omitempty"`
	StaticRoot  bool   `json:"staticRoot,omitempty"`
	WhiteNoise  bool   `json:"whiteNoise,omitempty"`
	Logging     bool   `json:"logging,omitempty"`
	Channels    bool   `json:"channels,omitempty"`
}

// pythonSource is what readPythonSources read for one Python root.
type pythonSource struct {
	versionFiles pythonVersionFiles
	// scripts are the root's scripts beyond the conventional entries, and
	// the modules a run.py, wsgi.py or manage.py imports from.
	scripts []pythonEntry
	// manageDirs are the directories holding a manage.py, the root's own
	// as "", at most two levels down; manage holds each one's content.
	manageDirs []string
	manage     map[string][]byte
	// settings are the Django settings files of the module the entry points
	// name, with their package's siblings; djangoSettingsText joins them.
	settings           map[string][]byte
	djangoSettingsText string
	// modules are the root's importable top-level names, and siblings the
	// names beside each first-level directory's modules.
	modules  map[string]bool
	siblings map[string]map[string]bool
	rxconfig bool
	// streamlitSecrets says a .streamlit/secrets.toml is committed.
	streamlitSecrets bool
	workspace        *pythonWorkspace
	// workspaceMembers says this root's [tool.uv.workspace] covers other
	// Python roots of the repository.
	workspaceMembers bool
	assetsDir        string
	hasAssets        bool
}

// pythonReadBudget bounds what readPythonSources reads across the whole
// repository.
type pythonReadBudget struct {
	files int
	bytes int64
}

func (b *pythonReadBudget) read(tree detectionTree, relative string, limit int64) ([]byte, bool) {
	if b.files >= 160 || b.bytes >= 6<<20 {
		return nil, false
	}
	content, ok := tree.read(relative, limit)
	if ok {
		b.files++
		b.bytes += int64(len(content))
	}
	return content, ok
}

// readPythonSources completes every Python root's markers after the walk.
func readPythonSources(tree detectionTree, markers map[string]*detectedMarkers, entries []pythonEntry) {
	if tree.root == nil {
		return
	}
	budget := &pythonReadBudget{}
	roots := make([]string, 0, len(markers))
	for key, marker := range markers {
		if marker.hasPythonManifest() {
			roots = append(roots, key)
		}
	}
	sort.Strings(roots)
	pythonRoots := make([]string, 0, len(roots))
	for _, key := range roots {
		pythonRoots = append(pythonRoots, slashRoot(markers[key].root))
	}
	for _, key := range roots {
		marker := markers[key]
		if marker == nil {
			continue
		}
		root := slashRoot(marker.root)
		source := &pythonSource{manage: map[string][]byte{}, settings: map[string][]byte{}, modules: map[string]bool{}, siblings: map[string]map[string]bool{}}
		marker.python = source
		completePythonManifests(tree, marker, root, budget)
		source.versionFiles = readTreeVersionFiles(tree, root, budget)
		known := map[string]bool{}
		for _, entry := range pythonEntriesUnderRoot(entries, root, pythonRoots) {
			known[entry.path] = true
		}
		listing := tree.entries(rootOrDot(root))
		sort.Strings(listing)
		packages := []string{}
		for _, name := range listing {
			relative := joinRoot(root, name)
			info, ok := tree.lstat(relative)
			if !ok {
				continue
			}
			switch {
			case info.IsDir() && pythonModuleNameRE.MatchString(name) && !pythonEntryDirsSkipped[name] && !strings.HasPrefix(name, "."):
				if tree.exists(joinRoot(relative, "__init__.py")) {
					source.modules[name] = true
					packages = append(packages, name)
				}
				if name == "src" {
					inners := tree.entries(relative)
					sort.Strings(inners)
					for _, inner := range inners {
						if pythonModuleNameRE.MatchString(inner) && tree.exists(joinRoot(joinRoot(relative, inner), "__init__.py")) {
							source.modules[inner] = true
							packages = append(packages, "src/"+inner)
						}
					}
				}
			case info.Mode().IsRegular() && strings.HasSuffix(name, ".py"):
				stem := strings.TrimSuffix(name, ".py")
				if pythonModuleNameRE.MatchString(stem) {
					source.modules[stem] = true
				}
				if known[name] || name == "manage.py" || name == "setup.py" || name == "conftest.py" || len(source.scripts) >= 32 {
					continue
				}
				if content, ok := budget.read(tree, relative, 64<<10); ok {
					source.scripts = append(source.scripts, pythonEntry{path: name, content: content})
				}
			case name == "rxconfig.py":
				source.rxconfig = true
			}
		}
		readPythonPackageEntries(tree, root, packages, known, pythonRoots, source, budget)
		source.rxconfig = source.rxconfig || tree.exists(joinRoot(root, "rxconfig.py"))
		source.streamlitSecrets = tree.exists(joinRoot(root, ".streamlit/secrets.toml"))
		if facts := readPyproject(marker.pythonFiles["pyproject.toml"]); len(facts.uvWorkspaceMembers) > 0 {
			for _, other := range pythonRoots {
				if other != root && underRoot(other, root) && pythonWorkspaceCovers(facts, strings.TrimPrefix(other, rootPrefix(root))) {
					source.workspaceMembers = true
				}
			}
		}
		// A first-level application folder's siblings: app/main.py importing
		// `routers` needs app/ on the import path.
		for _, entry := range pythonEntriesUnderRoot(entries, root, pythonRoots) {
			directory := path.Dir(entry.path)
			if directory == "." || strings.Contains(directory, "/") || source.siblings[directory] != nil {
				continue
			}
			names := map[string]bool{}
			for _, name := range tree.entries(joinRoot(root, directory)) {
				names[strings.TrimSuffix(name, ".py")] = true
			}
			source.siblings[directory] = names
		}
		readPythonManage(tree, markers, marker, root, pythonRoots, budget)
		if len(source.manageDirs) == 1 && source.manageDirs[0] != "" {
			// The directory startproject made inside the repository is this
			// root's project, not a root of its own: its settings' variables
			// and routes are this candidate's.
			nested := filepath.FromSlash(joinRoot(root, source.manageDirs[0]))
			if other := markers[nested]; other != nil && markerHoldsOnlyManagePy(other) {
				delete(markers, nested)
			}
		}
		readPythonImportHop(tree, root, source, pythonEntriesUnderRoot(entries, root, pythonRoots), budget)
		source.workspace = treePythonWorkspace(tree, markers, root, budget)
		if dir, ok := treePythonAssets(tree, marker, root); ok {
			source.assetsDir, source.hasAssets = dir, true
			// The package is a stage of this root's image, not a site of
			// its own.
			if owner := markers[filepath.FromSlash(joinRoot(root, dir))]; owner != nil {
				owner.pythonAssets = true
			}
		}
	}
}

var pythonModuleNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// pythonShellPathRE is a checkout path a proposed start command can name
// unquoted: a space, a quote or a $ would split or expand it, and a leading
// dash would read as an option.
var pythonShellPathRE = regexp.MustCompile(`^[\p{L}\p{N}_.][\p{L}\p{N}_./@+-]*$`)

func slashRoot(root string) string {
	return strings.ReplaceAll(root, "\\", "/")
}

func rootOrDot(root string) string {
	if root == "" {
		return "."
	}
	return root
}

// completePythonManifests reads what the walk could not: the files a
// requirement file includes, and a lock too large for the walk's budget,
// streamed into its reduced form.
func completePythonManifests(tree detectionTree, marker *detectedMarkers, root string, budget *pythonReadBudget) {
	for _, name := range pythonLockFiles {
		if string(marker.pythonFiles[name]) != "locked" {
			continue
		}
		file, err := tree.root.Open(joinRoot(root, name))
		if err != nil {
			continue
		}
		if content, ok := compactPythonLock(file, 4<<20); ok {
			marker.pythonFiles[name] = content
		}
		file.Close()
	}
	for round := 0; round < 3; round++ {
		missing := pythonMissingIncludes(marker.pythonFiles)
		if len(missing) == 0 {
			return
		}
		for _, name := range missing {
			if content, ok := budget.read(tree, joinRoot(root, name), 1<<20); ok {
				marker.pythonFiles[name] = manifestText(content)
			} else {
				return
			}
		}
	}
}

// readTreeVersionFiles reads the interpreter declarations of a root or the
// nearest directory above it that has them.
func readTreeVersionFiles(tree detectionTree, root string, budget *pythonReadBudget) pythonVersionFiles {
	files := pythonVersionFiles{}
	directories := []string{root}
	for directory := root; directory != ""; {
		directory = path.Dir(directory)
		if directory == "." {
			directory = ""
		}
		directories = append(directories, directory)
	}
	read := func(name string) (string, string) {
		for _, directory := range directories {
			if content, ok := budget.read(tree, joinRoot(directory, name), 4096); ok {
				return string(content), joinRoot(directory, name)
			}
		}
		return "", ""
	}
	files.pythonVersion, files.pythonVersionPath = read(".python-version")
	if content, ok := budget.read(tree, joinRoot(root, "runtime.txt"), 4096); ok {
		files.runtimeTxt = string(content)
	}
	files.toolVersions, files.toolVersionsPath = read(".tool-versions")
	files.mise, files.misePath = read("mise.toml")
	if files.mise == "" {
		files.mise, files.misePath = read(".mise.toml")
	}
	return files
}

// readPythonManage finds the root's manage.py, or the only one at most two
// levels down (a `django-admin startproject` run inside the repository), and
// the settings module its entry points name.
func readPythonManage(tree detectionTree, markers map[string]*detectedMarkers, marker *detectedMarkers, root string, pythonRoots []string, budget *pythonReadBudget) {
	source := marker.python
	if marker.managePy {
		source.manageDirs = append(source.manageDirs, "")
	} else {
		for _, other := range markers {
			otherRoot := slashRoot(other.root)
			if !other.managePy || otherRoot == root || !underRoot(otherRoot, root) {
				continue
			}
			relative := strings.TrimPrefix(otherRoot, rootPrefix(root))
			if strings.Count(relative, "/") > 1 || !pythonShellPathRE.MatchString(relative) || ownedByNestedRoot(otherRoot, root, pythonRoots) {
				continue
			}
			source.manageDirs = append(source.manageDirs, relative)
		}
		sort.Strings(source.manageDirs)
	}
	for _, directory := range source.manageDirs {
		if content, ok := budget.read(tree, joinRoot(root, joinRoot(directory, "manage.py")), 64<<10); ok {
			source.manage[joinRoot(directory, "manage.py")] = content
		}
	}
	if len(source.manageDirs) != 1 {
		return
	}
	directory := source.manageDirs[0]
	modules := []string{}
	if match := djangoModuleRE.FindSubmatch(source.manage[joinRoot(directory, "manage.py")]); match != nil {
		modules = append(modules, string(match[1]))
	}
	for _, name := range []string{"wsgi.py", "asgi.py"} {
		for _, candidate := range tree.entries(joinRoot(root, rootOrDot(directory))) {
			if !pythonModuleNameRE.MatchString(candidate) {
				continue
			}
			relative := joinRoot(joinRoot(root, directory), joinRoot(candidate, name))
			if content, ok := budget.read(tree, relative, 64<<10); ok {
				source.manage[joinRoot(directory, joinRoot(candidate, name))] = content
				if match := djangoModuleRE.FindSubmatch(content); match != nil {
					modules = append(modules, string(match[1]))
				}
			}
		}
	}
	if len(modules) == 0 {
		// Nothing names the module: startproject's own is <project>.settings,
		// beside the WSGI module.
		for name := range source.manage {
			if path.Base(name) == "wsgi.py" && path.Dir(name) != "." {
				modules = append(modules, strings.ReplaceAll(strings.TrimPrefix(path.Dir(name), rootPrefix(directory)), "/", ".")+".settings")
			}
		}
		sort.Strings(modules)
	}
	// The settings module and its package's siblings: a split settings
	// package imports its base, and production.py may be the one to run.
	read := map[string]bool{}
	for _, module := range modules {
		file := joinRoot(directory, strings.ReplaceAll(module, ".", "/"))
		for _, candidate := range []string{file + ".py", file + "/__init__.py"} {
			if read[candidate] {
				continue
			}
			read[candidate] = true
			if content, ok := budget.read(tree, joinRoot(root, candidate), 128<<10); ok {
				source.settings[candidate] = content
			}
		}
		packageDir := path.Dir(file)
		for _, name := range tree.entries(joinRoot(root, packageDir)) {
			candidate := joinRoot(packageDir, name)
			if !strings.HasSuffix(name, ".py") || read[candidate] || len(source.settings) >= 12 {
				continue
			}
			read[candidate] = true
			if strings.HasPrefix(name, "settings") || path.Base(packageDir) == "settings" {
				if content, ok := budget.read(tree, joinRoot(root, candidate), 128<<10); ok {
					source.settings[candidate] = content
				}
			}
		}
	}
	names := make([]string, 0, len(source.settings))
	for name := range source.settings {
		names = append(names, name)
	}
	sort.Strings(names)
	var text strings.Builder
	for _, name := range names {
		text.Write(source.settings[name])
		text.WriteString("\n")
	}
	source.djangoSettingsText = text.String()
}

// ownedByNestedRoot says a path belongs to another Python root between it
// and root.
func ownedByNestedRoot(file, root string, pythonRoots []string) bool {
	for _, other := range pythonRoots {
		if other != root && underRoot(other, root) && (file == other || underRoot(file, other)) {
			return true
		}
	}
	return false
}

// readPythonPackageEntries reads the conventional entry files of the root's
// packages — <pkg>/ and src/<pkg>/ — that the walk left unread because they
// sit deeper than it looks for entries: `uv init --package` in a workspace
// member puts main.py five levels down (apps/api/src/api/main.py). A package
// that is another Python root is that root's own.
func readPythonPackageEntries(tree detectionTree, root string, packages []string, known map[string]bool, pythonRoots []string, source *pythonSource, budget *pythonReadBudget) {
	read := 0
	for _, directory := range packages {
		if ownedByNestedRoot(joinRoot(root, directory), root, pythonRoots) {
			continue
		}
		for _, name := range []string{"main.py", "app.py", "server.py", "api.py", "application.py", "asgi.py", "wsgi.py", "__init__.py"} {
			relative := joinRoot(directory, name)
			if known[relative] || read >= 16 {
				continue
			}
			if content, ok := budget.read(tree, joinRoot(root, relative), 64<<10); ok {
				known[relative] = true
				read++
				source.scripts = append(source.scripts, pythonEntry{path: relative, content: content})
			}
		}
	}
}

// readPythonImportHop follows one `from X import Y` from the scripts that
// assemble an application elsewhere — run.py, wsgi.py, manage.py, app.py,
// main.py — so an application factory in myapp/factory.py is found.
func readPythonImportHop(tree detectionTree, root string, source *pythonSource, entries []pythonEntry, budget *pythonReadBudget) {
	have := map[string]bool{}
	for _, entry := range append(append([]pythonEntry(nil), entries...), source.scripts...) {
		have[entry.path] = true
	}
	read := 0
	for _, entry := range append(append([]pythonEntry(nil), entries...), source.scripts...) {
		switch path.Base(entry.path) {
		case "run.py", "wsgi.py", "asgi.py", "app.py", "main.py", "server.py", "application.py":
		default:
			continue
		}
		if strings.Count(entry.path, "/") > 1 {
			continue
		}
		for _, match := range pythonFromImportRE.FindAllSubmatch(entry.content, -1) {
			module := strings.ReplaceAll(string(match[1]), ".", "/")
			for _, candidate := range []string{module + ".py", module + "/__init__.py", "src/" + module + ".py", "src/" + module + "/__init__.py"} {
				if have[candidate] || read >= 16 {
					continue
				}
				if content, ok := budget.read(tree, joinRoot(root, candidate), 64<<10); ok {
					have[candidate] = true
					read++
					source.scripts = append(source.scripts, pythonEntry{path: candidate, content: content})
					break
				}
			}
		}
	}
}

// pythonWorkspace is the uv workspace a member root installs from: the
// workspace root, the member's directory below it, and its package name.
type pythonWorkspace struct {
	root, member, name string
}

// treePythonWorkspace finds the uv workspace a root is a member of: the
// nearest directory above it with a uv.lock and a pyproject whose
// [tool.uv.workspace] members cover it.
func treePythonWorkspace(tree detectionTree, markers map[string]*detectedMarkers, root string, budget *pythonReadBudget) *pythonWorkspace {
	if root == "" {
		return nil
	}
	own := markers[filepath.FromSlash(root)]
	for directory := path.Dir(root); ; directory = path.Dir(directory) {
		if directory == "." {
			directory = ""
		}
		var pyproject []byte
		lock := false
		if marker := markers[filepath.FromSlash(directory)]; marker != nil {
			pyproject = marker.pythonFiles["pyproject.toml"]
			_, lock = marker.pythonFiles["uv.lock"]
		} else if content, ok := budget.read(tree, joinRoot(directory, "pyproject.toml"), 256<<10); ok {
			pyproject, lock = content, tree.exists(joinRoot(directory, "uv.lock"))
		}
		if len(pyproject) > 0 && lock {
			facts := readPyproject(pyproject)
			member := strings.TrimPrefix(root, rootPrefix(directory))
			if pythonWorkspaceCovers(facts, member) {
				name := member
				if own != nil {
					if memberFacts := readPyproject(own.pythonFiles["pyproject.toml"]); memberFacts.name != "" {
						name = memberFacts.name
					}
				}
				return &pythonWorkspace{root: directory, member: member, name: normalizePythonName(name)}
			}
		}
		if directory == "" {
			return nil
		}
	}
}

// pythonWorkspaceCovers says a member path matches a workspace's member
// globs and none of its excludes.
func pythonWorkspaceCovers(facts pyprojectFacts, member string) bool {
	matched := false
	for _, glob := range facts.uvWorkspaceMembers {
		if ok, _ := path.Match(strings.TrimSuffix(glob, "/"), member); ok {
			matched = true
		}
	}
	for _, glob := range facts.uvWorkspaceExclude {
		if ok, _ := path.Match(strings.TrimSuffix(glob, "/"), member); ok {
			return false
		}
	}
	return matched
}

// findPythonWorkspace is treePythonWorkspace for a build root inside a
// checkout.
func findPythonWorkspace(boundary, root string) *pythonWorkspace {
	relative := checkoutPath(boundary, root)
	if relative == "" {
		return nil
	}
	for directory := path.Dir(relative); ; directory = path.Dir(directory) {
		if directory == "." {
			directory = ""
		}
		pyproject, err := readContainedRegular(boundary, joinRoot(directory, "pyproject.toml"), 512<<10)
		if err == nil && regularExists(boundary, joinRoot(directory, "uv.lock")) {
			facts := readPyproject(pyproject)
			member := strings.TrimPrefix(relative, rootPrefix(directory))
			if pythonWorkspaceCovers(facts, member) {
				name := member
				if content, err := readContainedRegular(root, "pyproject.toml", 512<<10); err == nil {
					if memberFacts := readPyproject(content); memberFacts.name != "" {
						name = memberFacts.name
					}
				}
				return &pythonWorkspace{root: directory, member: member, name: normalizePythonName(name)}
			}
		}
		if directory == "" {
			return nil
		}
	}
}

// pythonWorkspaceMemberSource names a dependency tool.uv.sources resolves
// from a workspace or a path, which pip would instead fetch from PyPI under
// the same name: a different package, or none.
func pythonWorkspaceMemberSource(files map[string][]byte, project pythonProject, root string) string {
	if _, locked := files["uv.lock"]; locked {
		return ""
	}
	names := make([]string, 0, len(project.pyproject.uvSources))
	for name, kind := range project.pyproject.uvSources {
		if kind == "workspace" || kind == "path" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		for _, requirement := range project.pyproject.dependencies {
			if requirement.name == name {
				return name
			}
		}
	}
	return ""
}

// pythonAssetDirectories are where a Python web application keeps the
// package.json that builds its CSS and JavaScript: the root (Tailwind's CLI,
// django-vite), and django-tailwind's theme app.
var pythonAssetDirectories = []string{"", "static_src", "theme/static_src", "theme", "assets"}

// pythonAssetBuilders are the dependencies that make a package.json an asset
// build rather than an application of its own.
var pythonAssetBuilders = []string{"tailwindcss", "@tailwindcss/cli", "@tailwindcss/postcss", "vite", "webpack", "webpack-cli", "esbuild",
	"postcss", "postcss-cli", "sass", "parcel", "rollup", "@parcel/core"}

// pythonBuildsAssets says a package.json is a Python application's asset
// build: a build script, a bundler or CSS tool, and no server of its own. A
// start script that only rebuilds the assets as they change is not one:
// django-tailwind's theme package runs `"start": "npm run dev"`, a
// `tailwindcss … -w`, for `manage.py tailwind start`.
func pythonBuildsAssets(content []byte) bool {
	var manifest nodeManifest
	if !parseNodeManifest(content, &manifest) || manifest.Scripts["build"] == "" || !pythonAssetWatcher(manifest.Scripts, "start", 0) {
		return false
	}
	if matchNodeServerLibrary(manifest) != "" {
		return false
	}
	if framework := matchNodeFramework(manifest); framework != nil && framework.Name != "vite" && framework.Name != "parcel" {
		return false
	}
	for _, name := range pythonAssetBuilders {
		if manifest.has(name) {
			return true
		}
	}
	return false
}

// pythonAssetWatcher says a package script, followed through the scripts it
// runs, only rebuilds assets as their sources change: every step passes a
// watch flag or starts a bundler's development server. A package without
// the script has none to run.
func pythonAssetWatcher(scripts map[string]string, name string, depth int) bool {
	body := strings.TrimSpace(scripts[name])
	if body == "" {
		return true
	}
	if depth >= 3 {
		return false
	}
	for _, segment := range strings.Split(body, "&&") {
		segment = strings.TrimSpace(segment)
		if match := scriptRunRE.FindStringSubmatch(segment); match != nil && match[1] != name && scripts[match[1]] != "" {
			if !pythonAssetWatcher(scripts, match[1], depth+1) {
				return false
			}
			continue
		}
		if !pythonWatchCommand(segment) {
			return false
		}
	}
	return true
}

// pythonWatchCommand says one command rebuilds assets on change: tailwindcss,
// postcss, sass or esbuild with -w/--watch, or Vite's, webpack's, Parcel's or
// esbuild's development server.
func pythonWatchCommand(segment string) bool {
	fields := strings.Fields(segment)
	for len(fields) > 0 && (envAssignmentRE.MatchString(fields[0]) || fields[0] == "cross-env" || fields[0] == "npx" || fields[0] == "env" || fields[0] == "exec") {
		fields = fields[1:]
	}
	if len(fields) == 0 {
		return false
	}
	for _, field := range fields[1:] {
		if field == "-w" || field == "--watch" || strings.HasPrefix(field, "--watch=") || field == "--serve" || strings.HasPrefix(field, "--serve=") {
			return true
		}
	}
	sub := ""
	if len(fields) > 1 && !strings.HasPrefix(fields[1], "-") {
		sub = fields[1]
	}
	switch path.Base(fields[0]) {
	case "vite":
		return sub == "" || sub == "dev" || sub == "serve"
	case "webpack-dev-server":
		return true
	case "webpack":
		return sub == "serve"
	case "parcel":
		return sub != "build"
	}
	return false
}

// treePythonAssets finds the asset package a Python web root builds.
func treePythonAssets(tree detectionTree, marker *detectedMarkers, root string) (string, bool) {
	deps := readPythonDependencies(marker.pythonFiles)
	web := false
	for _, framework := range []string{"django", "flask", "fastapi", "quart", "litestar", "starlette", "bottle", "falcon", "sanic"} {
		web = web || deps.declares(framework)
	}
	if !web {
		return "", false
	}
	for _, directory := range pythonAssetDirectories {
		content, ok := tree.read(joinRoot(joinRoot(root, directory), "package.json"), 512<<10)
		if ok && pythonBuildsAssets(content) {
			return directory, true
		}
	}
	return "", false
}

// pythonAssetsDirectory is treePythonAssets for a build root.
func pythonAssetsDirectory(root string) (string, bool) {
	files := readPythonSourceFiles(root)
	deps := readPythonProject(files).deps
	web := false
	for _, framework := range []string{"django", "flask", "fastapi", "quart", "litestar", "starlette", "bottle", "falcon", "sanic"} {
		web = web || deps.declares(framework)
	}
	if !web {
		return "", false
	}
	for _, directory := range pythonAssetDirectories {
		content, err := readContainedRegular(root, joinRoot(directory, "package.json"), 512<<10)
		if err == nil && pythonBuildsAssets(content) {
			return directory, true
		}
	}
	return "", false
}

// pythonAssetInstall plans the asset stage's install for the package in dir.
func pythonAssetInstall(root, dir, selected, arch string) (nodeInstallSource, nodeInstallPlan, error) {
	source, err := readNodeInstallSource(root, dir, arch, newNodeReadBudget())
	if err != nil {
		return nodeInstallSource{}, nodeInstallPlan{}, err
	}
	if source.context != source.dir {
		return nodeInstallSource{}, nodeInstallPlan{}, fmt.Errorf("%w: %s belongs to a JavaScript workspace; build the assets from a Dockerfile", ErrUnsupportedBuilder, joinRoot(dir, "package.json"))
	}
	plan := planNodeInstall(source.facts, nodeInstallChoice{selected: selected, build: "npm run build", assets: true})
	return source, plan, nil
}

// pythonDjangoSettingsText reads a build root's Django settings the way
// detection does, for GeoDjango's system packages.
func pythonDjangoSettingsText(root string) string {
	tree := openDetectionTree(root)
	defer tree.close()
	marker := &detectedMarkers{managePy: regularExists(root, "manage.py"), pythonFiles: map[string][]byte{}}
	marker.python = &pythonSource{manage: map[string][]byte{}, settings: map[string][]byte{}}
	readPythonManage(tree, map[string]*detectedMarkers{}, marker, "", nil, &pythonReadBudget{})
	return marker.python.djangoSettingsText
}

var (
	djangoStaticRootRE   = regexp.MustCompile(`(?m)^\s*STATIC_ROOT\s*(?::[^=\n]+)?=`)
	djangoStaticFilesRE  = regexp.MustCompile(`["']django\.contrib\.staticfiles["']`)
	djangoWhiteNoiseRE   = regexp.MustCompile(`whitenoise`)
	djangoLoggingRE      = regexp.MustCompile(`(?m)^\s*LOGGING\s*(?::[^=\n]+)?=|^\s*LOGGING\s*\[|logging\.config\.dictConfig|^\s*LOGGING_CONFIG\s*=`)
	djangoDevModuleRE    = regexp.MustCompile(`(?:^|\.)(?:dev|development|local|debug)$`)
	djangoSecretKeyRE    = regexp.MustCompile(`(?m)^\s*SECRET_KEY\s*(?::[^=\n]+)?=`)
	djangoProductionName = []string{"production", "prod"}
)

// readDjangoFacts reads what the settings say about how the project must be
// served, and chooses the settings module: the one the server's WSGI (or
// ASGI) module names, unless that is a development module with a production
// sibling that sets a SECRET_KEY, which is then the one to run.
func readDjangoFacts(source *pythonSource, deps pythonDependencies) *DetectedDjango {
	django := &DetectedDjango{}
	if len(source.manageDirs) == 1 {
		django.ManageDir = source.manageDirs[0]
	}
	text := source.djangoSettingsText
	django.StaticFiles = djangoStaticFilesRE.MatchString(text)
	django.StaticRoot = djangoStaticRootRE.MatchString(text)
	django.WhiteNoise = deps.has("whitenoise") || djangoWhiteNoiseRE.MatchString(text)
	django.Logging = djangoLoggingRE.MatchString(text)
	manageModule, serverModule, serverFile := "", "", ""
	names := make([]string, 0, len(source.manage))
	for name := range source.manage {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if path.Base(name) == "asgi.py" && pythonProtocolRouter.Match(source.manage[name]) && deps.declares("channels") {
			django.Channels = true
		}
		match := djangoModuleRE.FindSubmatch(source.manage[name])
		if match == nil {
			continue
		}
		switch path.Base(name) {
		case "manage.py":
			manageModule = string(match[1])
		case "wsgi.py":
			serverModule, serverFile = string(match[1]), name
		case "asgi.py":
			if serverModule == "" {
				serverModule, serverFile = string(match[1]), name
			}
		}
	}
	chosen := firstNonEmpty(serverModule, manageModule)
	if chosen == "" {
		if len(source.settings) > 0 {
			names := make([]string, 0, len(source.settings))
			for name := range source.settings {
				names = append(names, name)
			}
			sort.Strings(names)
			django.SettingsModule = pythonModule(strings.TrimPrefix(names[0], rootPrefix(django.ManageDir)))
			django.SettingsSource = "the settings beside the WSGI module"
		}
		return django
	}
	sources := []string{}
	if manageModule != "" {
		sources = append(sources, joinRoot(django.ManageDir, "manage.py")+" names "+manageModule)
	}
	if serverModule != "" {
		sources = append(sources, serverFile+" names "+serverModule)
	}
	if djangoDevModuleRE.MatchString(chosen) {
		packageDir := strings.ReplaceAll(chosen[:max(strings.LastIndex(chosen, "."), 0)], ".", "/")
		switched := false
		for _, name := range djangoProductionName {
			file := joinRoot(django.ManageDir, joinRoot(packageDir, name+".py"))
			content, ok := source.settings[file]
			if !ok {
				continue
			}
			// A production module that never sets SECRET_KEY, itself or
			// through the base it imports, refuses to start; the development
			// one is then what runs, and preflight says so.
			base := ""
			for settingsFile, settingsContent := range source.settings {
				if path.Dir(settingsFile) == path.Dir(file) && path.Base(settingsFile) != path.Base(file) &&
					!djangoDevModuleRE.MatchString(strings.TrimSuffix(path.Base(settingsFile), ".py")) {
					base += string(settingsContent)
				}
			}
			if !djangoSecretKeyRE.Match(content) && !djangoSecretKeyRE.MatchString(base) {
				django.Development = chosen + " is a development module, and " + file + " sets no SECRET_KEY"
				break
			}
			chosen = strings.TrimSuffix(chosen[:max(strings.LastIndex(chosen, "."), 0)]+"."+name, ".")
			switched = true
			sources = append(sources, "the production module beside it is "+chosen)
			break
		}
		if !switched && django.Development == "" {
			django.Development = chosen + " is a development module"
		}
	}
	django.SettingsModule = chosen
	django.SettingsSource = boundedText(strings.Join(sources, "; "), 256)
	django.Seeded = manageModule != "" && (manageModule != chosen || (serverModule != "" && serverModule != chosen))
	return django
}

// resolveDjango builds a Django project's start: migrations, the static
// files WhiteNoise serves when STATIC_ROOT says where, then gunicorn — or an
// ASGI server for Channels, whose websocket routes WSGI cannot serve.
func resolveDjango(ctx pythonResolveContext) pythonResolution {
	source := ctx.files.source
	if source == nil || len(source.manageDirs) == 0 {
		return pythonResolution{Confidence: ConfidenceLow, Decisions: []string{"Django is installed but no manage.py was found; confirm the start command"}}
	}
	if len(source.manageDirs) > 1 {
		return pythonResolution{Confidence: ConfidenceLow, found: true,
			Decisions: []string{"several manage.py files (" + strings.Join(boundedNames(source.manageDirs), ", ") + "); choose the Django project to run"}}
	}
	django := ctx.files.django
	if django == nil {
		django = &DetectedDjango{}
	}
	directory := source.manageDirs[0]
	project, server := "", ""
	for _, name := range []string{"wsgi.py", "asgi.py"} {
		files := []string{}
		for file := range source.manage {
			if path.Base(file) == name && path.Dir(file) != "." && path.Dir(path.Dir(file)) == rootOrDot(directory) {
				files = append(files, file)
			}
		}
		sort.Strings(files)
		if len(files) > 0 {
			project, server = path.Base(path.Dir(files[0])), name
			break
		}
	}
	if project == "" {
		for _, entry := range sortedPythonEntries(ctx.entries) {
			base, dir := path.Base(entry.path), path.Dir(entry.path)
			if (base == "wsgi.py" || base == "asgi.py") && dir != "." && path.Dir(dir) == rootOrDot(directory) {
				project, server = path.Base(dir), base
				break
			}
		}
	}
	if project == "" {
		return pythonResolution{Confidence: ConfidenceLow, found: true, Decisions: []string{"name the Django WSGI module for gunicorn (project.wsgi:application)"}}
	}
	manage := joinRoot(directory, "manage.py")
	start := "python " + manage + " migrate --noinput && "
	if django.WhiteNoise && django.StaticRoot {
		start += "python " + manage + " collectstatic --noinput && "
	}
	evidence := []DetectionEvidence{{Path: manage, Reason: "Django project"}}
	target := pythonTarget{module: project, directory: directory}
	asgi := django.Channels || (server == "asgi.py" && !hasFileNamed(source.manage, joinRoot(directory, project+"/wsgi.py")))
	switch {
	case asgi && ctx.deps.declares("daphne"):
		command := "daphne -b 0.0.0.0 -p ${PORT:-8000} " + project + ".asgi:application"
		if directory != "" {
			command = "cd " + directory + " && " + command
		}
		evidence = append(evidence, DetectionEvidence{Path: joinRoot(directory, project+"/asgi.py"), Reason: "ASGI application served by Daphne"})
		return pythonResolution{Start: start + command, found: true, Evidence: evidence}
	case asgi:
		evidence = append(evidence, DetectionEvidence{Path: joinRoot(directory, project+"/asgi.py"), Reason: "ASGI application served by uvicorn with its websocket support"})
		return pythonResolution{Start: start + uvicornCommand(pythonTarget{module: project + ".asgi", directory: directory}, "application", false), found: true, Evidence: evidence}
	}
	// Nested, gunicorn runs from manage.py's directory, as the project does
	// in development.
	server = gunicornCommand(pythonTarget{module: target.module + ".wsgi"}, "application", 8000, "")
	if directory != "" {
		server = strings.Replace(server, " --access-logfile -", " --access-logfile - --chdir "+directory, 1)
	}
	return pythonResolution{Start: start + server, found: true, Evidence: evidence}
}

func hasFileNamed(files map[string][]byte, name string) bool {
	_, ok := files[name]
	return ok
}

var (
	pythonWebsocketRouteRE = regexp.MustCompile(`\.websocket\(|\bWebSocketRoute\(|\bwebsocket_route\(|@\w+\.websocket\b|(?m)^\s*@websocket(?:_listener)?\(`)
	streamlitSecretRE      = regexp.MustCompile(`\bst\.secrets(?:\[\s*["']([A-Za-z_][A-Za-z0-9_]*)["']\s*\]|\.get\(\s*["']([A-Za-z_][A-Za-z0-9_]*)["']|\.([A-Za-z_][A-Za-z0-9_]*)\b)(\s*\[|\.[A-Za-z_])?`)
)

// streamlitSecrets reads the st.secrets keys the sources use: top-level keys
// are variables the start command can write into secrets.toml; a table
// (st.secrets["connections"]["x"]) is not.
func streamlitSecrets(entries []pythonEntry) ([]string, []string) {
	var keys, nested []string
	for _, entry := range entries {
		for _, match := range streamlitSecretRE.FindAllSubmatch(entry.content, -1) {
			name := string(match[1]) + string(match[2]) + string(match[3])
			if name == "" || name == "get" || name == "to_dict" || name == "load_if_toml_exists" || name == "keys" || name == "items" {
				continue
			}
			if len(match[4]) > 0 {
				nested = appendUnique(nested, name)
				continue
			}
			keys = appendUnique(keys, name)
		}
	}
	filtered := keys[:0]
	for _, key := range keys {
		if !slices.Contains(nested, key) && ValidateEnvKey(key) == nil {
			filtered = append(filtered, key)
		}
	}
	sort.Strings(filtered)
	sort.Strings(nested)
	return filtered, nested
}

// streamlitSecretsStep writes .streamlit/secrets.toml from the named
// variables when the container starts, since st.secrets reads only that
// file: a key the environment does not set is left out, and each value is
// written as a JSON string, which is a valid TOML basic string. Values never
// enter the image.
func streamlitSecretsStep(keys []string) string {
	quoted := make([]string, 0, len(keys))
	for _, key := range keys {
		quoted = append(quoted, `"`+key+`"`)
	}
	return `python -c 'import json,os; os.makedirs(".streamlit",exist_ok=True); open(".streamlit/secrets.toml","w",encoding="utf-8").write("".join(k+" = "+json.dumps(os.environ[k],ensure_ascii=False).replace(chr(127),"\\u007f")+"\n" for k in (` +
		strings.Join(quoted, ",") + `,) if k in os.environ))'`
}

// applyPythonProjectFacts records what the project's manifests and sources
// say beyond the framework: the install, the lock, system packages, private
// indexes, freeze leftovers, the workspace, assets and the sources' own
// settings.
func applyPythonProjectFacts(candidate *DetectedCandidate, marker *detectedMarkers, project pythonProject, source *pythonSource, entries []pythonEntry, settings, version string) {
	python := candidate.Python
	if version == "" {
		version = defaultPythonRecipeVersion
	}
	installChoice := pythonInstallChoice{version: version, installProject: pythonStartNeedsProject(candidate.StartCommand, project)}
	if source.workspace != nil {
		installChoice.member = source.workspace.name
		python.Workspace = rootLabelOf(source.workspace.root)
		python.InstallFile, python.Toolchain = joinRoot(source.workspace.root, "uv.lock"), "uv "+pythonUVRelease
		candidate.PythonInstall = "uv.lock"
		candidate.UnpinnedDependencies = false
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(source.workspace.root, "uv.lock"),
			Reason: "installed from the uv workspace at " + rootLabelOf(source.workspace.root) + " as its member " + source.workspace.name})
	} else if install, err := planPythonInstall(project, installChoice); err != nil {
		if candidate.RecipeIssue == "" {
			candidate.RecipeIssue = recipeRefusalText(err, "")
		}
	} else {
		candidate.PythonInstall = install.kind
		python.InstallFile, python.Toolchain = install.file, install.toolchain
		if install.file != "" && install.file != install.kind && install.file != joinRoot("", install.kind) {
			candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, install.file), Reason: "the build installs from " + install.file})
		}
		if name := pythonWorkspaceMemberSource(project.files, project, marker.root); name != "" && candidate.RecipeIssue == "" {
			candidate.RecipeIssue = name + " comes from a uv workspace (tool.uv.sources " + name + " = { workspace = true }); build from the workspace root"
		}
	}
	if drift := project.lockDrift(); drift != nil && source.workspace == nil {
		drift.Missing, drift.Changed = boundedList(drift.Missing, 16), boundedList(drift.Changed, 16)
		drift.Note = boundedText(drift.Note, 512)
		python.Lock = drift
	}
	python.ManifestConflict = boundedList(project.manifestConflict(), 16)
	if project.declaresNothing() && source.workspace == nil {
		python.DependenciesEmpty = "warning"
		for _, entry := range entries {
			for _, framework := range []string{"fastapi", "flask", "django", "streamlit", "gradio", "starlette", "litestar"} {
				if pythonImports(entry.content, framework) {
					python.DependenciesEmpty = "blocked"
				}
			}
		}
		if marker.managePy {
			python.DependenciesEmpty = "blocked"
		}
	}
	python.LocalArtifacts = boundedList(project.localArtifacts(), 8)
	python.LocalPaths = boundedList(project.localPaths(), 8)
	if project.condaFound && project.lock == nil && project.requirementsFile == "" && !project.pyproject.declaresDependencies {
		lines, _, _ := project.condaRequirements()
		python.CondaConverted = boundedList(lines, 32)
	}
	indexes := project.indexFacts()
	python.PrivateIndexes = boundedList(indexes.private, 8)
	python.InlineCredentials = boundedList(indexes.inline, 8)
	python.GitSSH = boundedList(indexes.gitSSH, 8)
	candidate.Variables = append(candidate.Variables, indexes.variables...)
	python.CPUTorch = project.torchIndexPrefix() != "" && (candidate.PythonInstall == "requirements.txt" || candidate.PythonInstall == "pyproject.toml" ||
		candidate.PythonInstall == "setup.py" || candidate.PythonInstall == "environment.yml")
	python.GPUWheels = boundedList(project.lockedCUDAPackages(), 16)
	candidate.SystemPackages = project.systemPackages(settings)
	for index := range candidate.SystemPackages {
		candidate.SystemPackages[index].Automatic = true
	}
	modules := make([]string, 0, len(source.modules))
	for name := range source.modules {
		modules = append(modules, name)
	}
	sort.Strings(modules)
	if len(modules) > 64 {
		modules = modules[:64]
	}
	python.Modules = modules
	if source.hasAssets {
		python.Assets = rootLabelOf(source.assetsDir)
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, joinRoot(source.assetsDir, "package.json")),
			Reason: "front-end assets are built by a Node stage inside the Python recipe"})
	}
	if candidate.Framework == "streamlit" {
		keys, nested := streamlitSecrets(entries)
		python.StreamlitNested = boundedList(nested, 16)
		if len(keys) > 0 && !source.streamlitSecrets && strings.HasPrefix(candidate.StartCommand, "streamlit run ") {
			python.StreamlitSecrets = boundedList(keys, 32)
			candidate.StartCommand = streamlitSecretsStep(python.StreamlitSecrets) + " && " + candidate.StartCommand
			candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, ".streamlit/secrets.toml"),
				Reason: "st.secrets reads only this file; the start command writes " + strings.Join(boundedNames(python.StreamlitSecrets), ", ") + " into it from the variables"})
		}
	}
	if candidate.Framework == "gradio" {
		for _, entry := range entries {
			if pythonImports(entry.content, "gradio") && pythonServerNameRE.Match(entry.content) {
				python.GradioLoopback = joinRoot(marker.root, entry.path)
				break
			}
		}
	}
	if project.deps.declares("uvicorn") && !project.deps.has("websockets") && !project.deps.has("wsproto") &&
		!slices.Contains(project.deps.extras["uvicorn"], "standard") {
		for _, entry := range entries {
			if pythonWebsocketRouteRE.Match(entry.content) {
				python.WebsocketLibraryMissing = joinRoot(marker.root, entry.path)
				break
			}
		}
	}
	if python.Django != nil && python.Django.ManageDir != "" {
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, joinRoot(python.Django.ManageDir, "manage.py")),
			Reason: "manage.py is in " + python.Django.ManageDir + "; the start command runs it there"})
	}
	if python.Django != nil && python.Django.SettingsModule != "" {
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, "manage.py"),
			Reason: "settings module " + python.Django.SettingsModule + " (" + python.Django.SettingsSource + ")"})
	}
	// uv's own workspace root has a [project] table too, with no dependencies
	// of its own: the applications are still its members.
	if source.workspace == nil && project.pyproject.present && len(project.pyproject.uvWorkspaceMembers) > 0 &&
		(!project.pyproject.project || (source.workspaceMembers && len(project.pyproject.dependencies) == 0)) {
		candidate.Demotion = "uv workspace root; the applications are its member packages"
	}
}

// DetectedSystemPackage is a Debian package the source needs in its image,
// with the dependency that needs it. Automatic ones are what the recipe
// installs by itself; the others (an Aptfile's) are seeded into the plan.
type DetectedSystemPackage struct {
	Name      string `json:"name"`
	Reason    string `json:"reason,omitempty"`
	Source    string `json:"source,omitempty"`
	Automatic bool   `json:"automatic,omitempty"`
}

// validateDetectedPython bounds what a saved detection carries for Python.
func validateDetectedPython(candidate DetectedCandidate) error {
	malformed := fmt.Errorf("%w: detected Python facts are malformed", ErrInvalidPlan)
	if len(candidate.SystemPackages) > 48 {
		return malformed
	}
	for _, pkg := range candidate.SystemPackages {
		if !systemPackageRE.MatchString(pkg.Name) || len(pkg.Name) > 128 || !pythonFactText(pkg.Reason, 256) || !pythonFactText(pkg.Source, 512) {
			return malformed
		}
	}
	python := candidate.Python
	if python == nil {
		return nil
	}
	lists := [][]string{python.VersionLimited, python.ManifestConflict, python.LocalArtifacts, python.LocalPaths, python.CondaConverted, python.PrivateIndexes,
		python.InlineCredentials, python.GitSSH, python.GPUWheels, python.Modules, python.StreamlitSecrets, python.StreamlitNested}
	for _, list := range lists {
		if len(list) > 64 {
			return malformed
		}
		for _, value := range list {
			if !pythonFactText(value, 256) {
				return malformed
			}
		}
	}
	if len(python.WheelBlockers) > len(pythonRecipeVersions) {
		return malformed
	}
	for family, blockers := range python.WheelBlockers {
		if !pythonRecipeVersionRE.MatchString(family) || len(blockers) > 16 {
			return malformed
		}
		for _, value := range blockers {
			if !pythonFactText(value, 256) {
				return malformed
			}
		}
	}
	for _, text := range []string{python.InstallFile, python.Toolchain, python.VersionSource, python.VersionRaised, python.Workspace, python.GradioLoopback,
		python.WebsocketLibraryMissing, python.Assets} {
		if !pythonFactText(text, 512) {
			return malformed
		}
	}
	switch python.DependenciesEmpty {
	case "", "warning", "blocked":
	default:
		return malformed
	}
	for _, key := range python.StreamlitSecrets {
		if ValidateEnvKey(key) != nil {
			return malformed
		}
	}
	if lock := python.Lock; lock != nil {
		if pythonLockManager(lock.Path) == "" || lock.Manager != pythonLockManager(lock.Path) || !pythonFactText(lock.Note, 512) ||
			(lock.State != LockfileInSync && lock.State != LockfileStale && lock.State != LockfileUnknown) || len(lock.Missing) > 16 || len(lock.Changed) > 16 {
			return malformed
		}
	}
	if django := python.Django; django != nil {
		for _, text := range []string{django.ManageDir, django.SettingsModule, django.SettingsSource, django.Development} {
			if !pythonFactText(text, 512) {
				return malformed
			}
		}
		if django.ManageDir != "" && !safeRelativePath(django.ManageDir) {
			return malformed
		}
	}
	return nil
}

// pythonFactText is a fact the saved detection can carry: bounded, one line,
// and without credential material, even a URL's inside a requirement line.
func pythonFactText(value string, limit int) bool {
	return len(value) <= limit && !strings.ContainsAny(value, "\x00\r\n") && rejectPlanSecretLiteral("detected Python fact", value) == nil &&
		!containsURLCredentials(value)
}

// aptTrixieNames are the Debian names an Aptfile written for Ubuntu or an
// older Debian uses and trixie renamed or dropped.
var aptTrixieNames = map[string]string{
	"libglib2.0-0": "libglib2.0-0t64", "libmagic1": "libmagic1t64", "libgl1-mesa-glx": "libgl1",
	"libzbar0": "libzbar0t64", "libvips42": "libvips42t64", "libgdk-pixbuf2.0-0": "libgdk-pixbuf-2.0-0",
	"mime-support": "media-types", "libssl1.1": "libssl3t64", "libssl3": "libssl3t64", "libffi7": "libffi8",
	"libpython3-dev": "python3-dev",
}

// applyPythonEnvironment adds what a Python root's own settings imply once
// its environment is described: the Django settings module both manage.py
// and the server must use, Panel's websocket origin, and the system packages
// another platform's files ask for, which the plan carries so they can be
// edited.
func applyPythonEnvironment(marker *detectedMarkers, candidates []DetectedCandidate) {
	for index := range candidates {
		candidate := &candidates[index]
		if candidate.Recipe != "python" || candidate.Python == nil {
			continue
		}
		add := func(variable DetectedVariable) {
			for position, existing := range candidate.Variables {
				if existing.Name == variable.Name {
					if existing.Setup == "" {
						variable.Sources = existing.Sources
						candidate.Variables[position] = variable
					}
					return
				}
			}
			if len(candidate.Variables) < 64 {
				candidate.Variables = append(candidate.Variables, variable)
			}
		}
		if django := candidate.Python.Django; django != nil && django.Seeded && django.SettingsModule != "" {
			add(DetectedVariable{Name: "DJANGO_SETTINGS_MODULE", Sources: []string{joinRoot(marker.root, joinRoot(django.ManageDir, "manage.py"))},
				Setup: "default", DefaultValue: django.SettingsModule,
				SetupReason: boundedText("migrate, collectstatic and the server must load the same settings: "+django.SettingsSource, 512)})
		}
		if candidate.Framework == "panel" {
			add(DetectedVariable{Name: "BOKEH_ALLOW_WS_ORIGIN", Sources: []string{joinRoot(marker.root, "requirements.txt")},
				Setup: "domain", DomainTemplate: "{{hostname}}", SetupReason: "Panel accepts its page's websocket only from the origins it is told"})
		}
		seen := map[string]bool{}
		for _, pkg := range candidate.SystemPackages {
			seen[pkg.Name] = true
		}
		for _, manifest := range candidate.PlatformManifests {
			for _, name := range manifest.SystemPackages {
				reason := manifest.File + " installs it"
				if renamed := aptTrixieNames[name]; renamed != "" {
					reason = manifest.File + " installs " + name + ", which Debian trixie names " + renamed
					name = renamed
				}
				if seen[name] || !systemPackageRE.MatchString(name) || len(candidate.SystemPackages) >= 48 {
					continue
				}
				seen[name] = true
				candidate.SystemPackages = append(candidate.SystemPackages, DetectedSystemPackage{Name: name, Reason: boundedText(reason, 256), Source: manifest.File})
			}
		}
		keepValidPythonFacts(candidate)
	}
}

// keepValidPythonFacts drops what validateDetectedPython refuses: the facts
// quote the checkout's own paths and lines, which can read like a
// credential, and one such line must not leave the detection unsavable.
func keepValidPythonFacts(candidate *DetectedCandidate) {
	invalid := func(limit int) func(string) bool {
		return func(value string) bool { return !pythonFactText(value, limit) }
	}
	candidate.SystemPackages = slices.DeleteFunc(candidate.SystemPackages, func(pkg DetectedSystemPackage) bool {
		return !systemPackageRE.MatchString(pkg.Name) || !pythonFactText(pkg.Reason, 256) || !pythonFactText(pkg.Source, 512)
	})
	python := candidate.Python
	for _, list := range []*[]string{&python.VersionLimited, &python.ManifestConflict, &python.LocalArtifacts, &python.LocalPaths, &python.CondaConverted,
		&python.PrivateIndexes, &python.InlineCredentials, &python.GitSSH, &python.GPUWheels, &python.Modules, &python.StreamlitNested} {
		*list = slices.DeleteFunc(*list, invalid(256))
	}
	python.StreamlitSecrets = slices.DeleteFunc(python.StreamlitSecrets, func(key string) bool { return ValidateEnvKey(key) != nil })
	for family, blockers := range python.WheelBlockers {
		python.WheelBlockers[family] = slices.DeleteFunc(blockers, invalid(256))
	}
	for _, text := range []*string{&python.InstallFile, &python.Toolchain, &python.VersionSource, &python.VersionRaised, &python.Workspace,
		&python.GradioLoopback, &python.WebsocketLibraryMissing, &python.Assets} {
		if !pythonFactText(*text, 512) {
			*text = ""
		}
	}
	if lock := python.Lock; lock != nil {
		lock.Missing, lock.Changed = slices.DeleteFunc(lock.Missing, invalid(256)), slices.DeleteFunc(lock.Changed, invalid(256))
		if !pythonFactText(lock.Note, 512) {
			lock.Note = ""
		}
	}
	if django := python.Django; django != nil {
		if (django.ManageDir != "" && !safeRelativePath(django.ManageDir)) || !pythonFactText(django.ManageDir, 512) || !pythonFactText(django.SettingsModule, 512) {
			python.Django = nil
			return
		}
		for _, text := range []*string{&django.SettingsSource, &django.Development} {
			if !pythonFactText(*text, 512) {
				*text = ""
			}
		}
	}
}

// markerHoldsOnlyManagePy says a directory was a marker only because it
// holds a manage.py.
func markerHoldsOnlyManagePy(marker *detectedMarkers) bool {
	return marker.managePy && len(marker.pythonFiles) == 0 && len(marker.packageJSON) == 0 && len(marker.dockerfiles) == 0 &&
		len(marker.compose) == 0 && marker.staticFile == "" && marker.goMod == "" && len(marker.cargoToml) == 0 &&
		len(marker.pomXML) == 0 && len(marker.gradleBuild) == 0 && len(marker.csprojs) == 0 && len(marker.denoJSON) == 0 &&
		len(marker.composerJSON) == 0 && !marker.phpIndex && !marker.phpPublicIndex && len(marker.procfile) == 0
}
