package deploy

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// The language recipes' package and compiler caches, each a
// config.cache.mount (build_cache_mounts.go): environment-scoped, and
// nothing at all on a forced build, which then builds exactly as it did
// before the caches existed. A mount's content never reaches a layer, so
// none of these can ship in an image or carry one build's secret into the
// next build's layer; and each tool treats its cache as disposable, so a
// mount the BuildKit garbage collector emptied is a cold build, not a
// failed one.

// cargoHome is CARGO_HOME in the official rust images.
const cargoHome = "/usr/local/cargo"

// rustDependencyMounts are the crates Cargo downloads (the registry's index
// and archives, and git dependencies' checkouts), which cargo fetch and the
// build both read. A fetch that needs an install secret (a private
// registry's token) keeps today's layer instead: the build step has no
// token, so it must find every crate in the image rather than in a cache
// the garbage collector may have emptied between the two steps.
func rustDependencyMounts(cache recipeCache, installSecrets string) string {
	if installSecrets != "" {
		return ""
	}
	return cache.mount("cargo-registry", cargoHome+"/registry") + cache.mount("cargo-git", cargoHome+"/git")
}

// rustTargetMount caches Cargo's output directory, so a commit recompiles
// only the crates it changed rather than every dependency. name keeps the
// musl build's directory apart from the Debian one the Leptos and Trunk
// builds use: their host toolchains differ, and Cargo would rebuild each
// from the other's anyway. It renders nothing when the directory is not
// one the build can mount without hiding the source.
func rustTargetMount(cache recipeCache, recipe rustRecipe, name string) string {
	directory := rustTargetPath(recipe)
	if directory == "" {
		return ""
	}
	return cache.mount(name, directory)
}

// rustTargetPath is Cargo's output directory in the build stage, or "" when
// the build keeps it out of the cache: an absolute target-dir, which could
// name a directory of the image itself, and one holding the checkout or
// the crate, which the mount would hide.
func rustTargetPath(recipe rustRecipe) string {
	directory := recipe.targetDir
	if directory == "" {
		directory = "target"
	}
	if strings.HasPrefix(directory, "/") {
		return ""
	}
	directory = path.Clean("/src/" + directory)
	if !strings.HasPrefix(directory, "/src/") || strings.HasPrefix(rustSourceDir(recipe)+"/", directory+"/") {
		return ""
	}
	return directory
}

// The golang images' module cache (GOPATH=/go) and build cache.
const (
	goModCache   = "/go/pkg/mod"
	goBuildCache = "/root/.cache/go-build"
)

// goModuleMount caches the modules go mod download fetches, which every
// later go command reads. Like Rust's crates, a download that needs an
// install secret (a private module's token) keeps today's layer, so the
// build, which has no token, never has to fetch a private module again.
func goModuleMount(cache recipeCache, installSecrets string) string {
	if installSecrets != "" {
		return ""
	}
	return cache.mount("go-mod", goModCache)
}

// goCompileMounts are what a step that compiles reads: the modules and the
// build cache, which holds no secret and lets a commit recompile only the
// packages it changed.
func goCompileMounts(cache recipeCache, installSecrets string) string {
	return goModuleMount(cache, installSecrets) + cache.mount("go-build", goBuildCache)
}

// recipeCopyPathRE is a context path safe to write unquoted into COPY: no
// space, quote, variable or wildcard.
var recipeCopyPathRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.@+/-]{0,255}$`)

// goModuleInputs are the context paths go mod download reads, which the
// recipe copies before the source so a commit that changes only code keeps
// the download: the module's go.mod and go.sum, and the go.mod of each local
// replacement. It is nil, and the download stays after the source, where
// the inputs are not all knowable: a vendored module downloads nothing, a
// go.work workspace lists its packages' imports rather than downloading
// (goWorkspaceDownload), and a replacement without a go.mod or with a path
// COPY cannot name is left to the build as today.
func goModuleInputs(boundary, root string, plan goBuildPlan) []string {
	if plan.vendored || plan.context.workspace {
		return nil
	}
	contextRel := checkoutPath(boundary, root)
	if plan.context.dir != "" {
		contextRel = plan.context.dir
	}
	inputs := []string{path.Join(plan.context.module, "go.mod")}
	if regularExists(root, "go.sum") {
		inputs = append(inputs, path.Join(plan.context.module, "go.sum"))
	}
	for _, target := range plan.context.replaces {
		if !regularExists(filepath.Join(boundary, filepath.FromSlash(target)), "go.mod") {
			return nil
		}
		inputs = append(inputs, path.Join(relativeBuildPath(rootLabelOf(contextRel), rootLabelOf(target)), "go.mod"))
	}
	inputs = uniqueOrdered(inputs)
	for _, input := range inputs {
		if !recipeCopyPathRE.MatchString(input) || strings.Contains(input, "..") {
			return nil
		}
	}
	return inputs
}

// copyInputsTo renders the COPY instructions that place context files at
// the same paths under dir, one per directory.
func copyInputsTo(dir string, inputs []string) []string {
	byDir := map[string][]string{}
	order := []string{}
	for _, input := range inputs {
		parent := path.Dir(input)
		if _, seen := byDir[parent]; !seen {
			order = append(order, parent)
		}
		byDir[parent] = append(byDir[parent], input)
	}
	lines := make([]string, 0, len(order))
	for _, parent := range order {
		destination := dir + "/"
		if parent != "." {
			destination = dir + "/" + parent + "/"
		}
		lines = append(lines, "COPY "+strings.Join(byDir[parent], " ")+" "+destination)
	}
	return lines
}

// pythonCachedRun gives one Python install command the download caches of
// the tools it runs, in place of --no-cache-dir, which keeps pip's cache
// out of the layer when there is no mount to keep it in. A forced build
// keeps the command exactly as it was. uv copies from a mounted cache
// rather than hard-linking across file systems, and is told so.
func pythonCachedRun(cache recipeCache, command string) (mounts, cached string) {
	if !cache.enabled() {
		return "", command
	}
	cached = strings.ReplaceAll(command, "pip install --no-cache-dir", "pip install")
	cached = strings.ReplaceAll(cached, "uv pip install --no-cache", "uv pip install")
	mounts = cache.mount("pip", "/root/.cache/pip")
	if strings.Contains(cached, "uv ") {
		mounts += cache.mount("uv", "/root/.cache/uv")
		cached = "export UV_LINK_MODE=copy && " + cached
	}
	for _, tool := range []struct{ word, name, target string }{
		{"poetry ", "poetry", "/root/.cache/pypoetry"},
		{"pipenv ", "pipenv", "/root/.cache/pipenv"},
		{"pdm ", "pdm", "/root/.cache/pdm"},
	} {
		if strings.Contains(cached, tool.word) {
			mounts += cache.mount(tool.name, tool.target)
		}
	}
	return mounts, cached
}

// pythonPathDependencyRE finds a path key in a pyproject, Pipfile or lock:
// a dependency installed from a directory of the checkout, which an
// install from the manifests alone cannot find.
var pythonPathDependencyRE = regexp.MustCompile(`(?m)(?:^|[{,\s])(?:path|editable)\s*=`)

// pythonDependenciesFirst splits an install the manifests alone describe
// into the dependencies, installed before the source is copied so a commit
// that changes only code keeps them, and what needs the source: the project
// itself, installed after it. inputs are the files the dependency step
// reads. It leaves install as it was — everything after the source — when
// a lock is stale (the build locks again, and the source's lock would then
// replace the one the dependencies came from), when a dependency is a
// directory of the checkout, for a uv workspace member, for setup.py,
// `pip install .`, a conda environment and a bare pyproject, and when the
// checkout carries a .venv the source copy would land on top of the
// installed one.
func pythonDependenciesFirst(root string, project pythonProject, choice pythonInstallChoice, install pythonInstall) pythonInstall {
	if (choice.member != "" || pathExists(root, ".venv")) || install.command == "" {
		return install
	}
	if drift := project.lockDrift(); drift != nil && drift.State == LockfileStale {
		return install
	}
	raw := func(name string) string { return string(project.files[name]) }
	split := func(inputs []string, dependencies, after string) pythonInstall {
		for _, input := range inputs {
			if !recipeCopyPathRE.MatchString(input) || strings.Contains(input, "..") {
				return install
			}
		}
		install.inputs, install.dependencies, install.command = uniqueOrdered(inputs), dependencies, after
		return install
	}
	head, rest, chained := strings.Cut(install.command, " && ")
	switch install.kind {
	case "uv.lock":
		pyproject := raw("pyproject.toml")
		if !chained || strings.Contains(pyproject, "tool.uv.sources") || strings.Contains(pyproject, "tool.uv.workspace") || pythonPathDependencyRE.MatchString(pyproject) {
			return install
		}
		// The project is synced once its source is there; an application
		// that is not a package has nothing left to install.
		return split([]string{"pyproject.toml", "uv.lock"}, head+" && "+strings.Replace(rest, " --python ", " --no-install-project --python ", 1), rest)
	case "poetry.lock":
		if project.lockFile != "poetry.lock" || !chained || pythonPathDependencyRE.MatchString(raw("pyproject.toml")) || pythonPathDependencyRE.MatchString(raw("poetry.lock")) {
			return install
		}
		dependencies := head + " && " + strings.Replace(rest, "poetry install --only main --no-interaction", "poetry install --only main --no-root --no-interaction", 1)
		after := ""
		if choice.installProject {
			after = rest
		}
		return split([]string{"pyproject.toml", "poetry.lock"}, dependencies, after)
	case "pdm.lock":
		if pythonPathDependencyRE.MatchString(raw("pyproject.toml")) || pythonPathDependencyRE.MatchString(raw("pdm.lock")) {
			return install
		}
		dependencies, after := install.command, ""
		if suffix := pythonProjectInstall(project, choice); suffix != "" {
			dependencies, after = strings.TrimSuffix(install.command, suffix), strings.TrimPrefix(suffix, " && ")
		}
		return split([]string{"pyproject.toml", "pdm.lock"}, dependencies, after)
	case "Pipfile.lock":
		if pythonPathDependencyRE.MatchString(raw("Pipfile")) {
			return install
		}
		return split([]string{"Pipfile", "Pipfile.lock"}, install.command, "")
	case "requirements.txt":
		inputs, ok := pythonRequirementInputs(root, project)
		if !ok {
			return install
		}
		dependencies, after := install.command, ""
		if suffix := pythonProjectInstall(project, choice); suffix != "" {
			dependencies, after = strings.TrimSuffix(install.command, suffix), strings.TrimPrefix(suffix, " && ")
		}
		return split(inputs, dependencies, after)
	case "pyproject.toml":
		// The dependency list read with tomllib; `pip install .` builds the
		// project and needs its source.
		pyproject := raw("pyproject.toml")
		if !strings.Contains(install.command, "tomllib") || strings.Contains(pyproject, "file:") || pythonPathDependencyRE.MatchString(pyproject) {
			return install
		}
		return split([]string{"pyproject.toml"}, install.command, "")
	}
	return install
}

// pythonRequirementInputs are the requirement files an install reads (the
// file, its -r includes and -c constraints), or false when one of them
// names something of the checkout beyond requirement files: an editable or
// path install, a local wheel or archive, or a --find-links directory.
func pythonRequirementInputs(root string, project pythonProject) ([]string, bool) {
	closure := project.requirements
	if len(closure.files) == 0 || len(closure.escapes) > 0 || len(closure.missing) > 0 || len(closure.files) >= 16 {
		return nil, false
	}
	inputs := []string{}
	for _, file := range closure.files {
		inputs = append(inputs, file.path)
		for _, constraint := range file.constraints {
			if !regularExists(root, constraint) {
				return nil, false
			}
			inputs = append(inputs, constraint)
		}
		for _, line := range strings.Split(string(project.files[file.path]), "\n") {
			if comment := strings.Index(line, " #"); comment >= 0 {
				line = line[:comment]
			}
			line = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), "\\"))
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if pythonRequirementLineIsLocal(line) {
				return nil, false
			}
		}
	}
	return inputs, true
}

func pythonRequirementLineIsLocal(line string) bool {
	fields := strings.Fields(line)
	option := fields[0]
	value := ""
	if name, assigned, ok := strings.Cut(option, "="); ok && strings.HasPrefix(option, "-") {
		option, value = name, assigned
	} else if len(fields) > 1 {
		value = fields[1]
	}
	switch option {
	case "-e", "--editable":
		return true
	case "-f", "--find-links":
		return !strings.Contains(value, "://")
	}
	if strings.HasPrefix(option, "-") {
		// -r, -c and the index options; a requirement file's own options
		// name no file of the checkout.
		return false
	}
	if strings.HasPrefix(line, ".") || strings.HasPrefix(line, "/") || strings.Contains(line, "file:") {
		return true
	}
	if _, after, ok := strings.Cut(line, "@"); ok && !strings.Contains(after, "://") {
		return true
	}
	for _, suffix := range []string{".whl", ".tar.gz", ".zip"} {
		if strings.HasSuffix(fields[0], suffix) && !strings.Contains(fields[0], "://") {
			return true
		}
	}
	return false
}

func pathExists(root, name string) bool {
	_, err := os.Lstat(filepath.Join(root, name))
	return err == nil
}

// jvmDependencyMounts are the download caches of a JVM build tool, all in
// the build user's home, outside the tree the runtime stage copies from:
// Gradle's caches and wrapper distributions, Maven's and the Clojure tools'
// local repository, the git libraries tools.deps checks out, and sbt's
// Coursier and Ivy caches. The images build as root.
func jvmDependencyMounts(cache recipeCache, tool string) string {
	switch tool {
	case "gradle":
		return cache.mount("gradle-caches", "/root/.gradle/caches") + cache.mount("gradle-wrapper", "/root/.gradle/wrapper")
	case "maven", "lein":
		return cache.mount("m2", "/root/.m2/repository")
	case "tools-deps":
		return cache.mount("m2", "/root/.m2/repository") + cache.mount("gitlibs", "/root/.gitlibs")
	case "sbt-stage", "sbt-assembly":
		return cache.mount("coursier", "/root/.cache/coursier") + cache.mount("ivy", "/root/.ivy2/cache")
	}
	return ""
}
