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

// The Python recipe builds on the maintained CPython releases. A version
// outside this set is a Dockerfile, not a guess at an image tag. 3.13 stays
// the default for a project that declares nothing: a 3.14 default would
// break every pin whose wheels stop at 3.13, and the wheel check below
// lowers it further when a pin needs that.
const defaultPythonRecipeVersion = "3.13"

var (
	pythonRecipeVersionRE  = regexp.MustCompile(`^3\.(10|11|12|13|14)$`)
	pythonRecipeVersions   = []string{"3.10", "3.11", "3.12", "3.13", "3.14"}
	pythonVersionLiteralRE = regexp.MustCompile(`([23])\.([0-9]{1,2})((?:\.[0-9]+)?)`)
	// requires-python, or Poetry's python key, in either TOML quote style.
	pythonRequiresRE  = regexp.MustCompile(`(?m)^\s*(?:requires-python|python)\s*=\s*(?:"([^"\n]+)"|'([^'\n]+)')`)
	pythonSpecifierRE = regexp.MustCompile(`(>=|~=|\^|==|>|<=|<)\s*(3\.[0-9]{1,2})(\.[0-9*]+)?`)
	// pythonPackageNameRE is a PEP 503 normalized distribution name.
	pythonPackageNameRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)
)

// The tools the recipe installs a locked project with are pinned, so two
// builds of one commit run the same resolver: recent enough to read the lock
// revisions their current releases write.
const (
	pythonUVRelease     = "0.12.18"
	pythonPoetryRelease = "2.5.1"
	pythonPDMRelease    = "2.29.2"
	pythonPipenvRelease = "2026.8.0"
)

// pythonServerPackages are the process managers detection points a start
// command at. When the manifests do not declare one, the recipe installs
// this exact release into the same environment as the application, so a
// detected start command never runs a binary the image does not have.
// uvicorn comes with its standard extras: without a websocket library it
// refuses every websocket upgrade.
var pythonServerPackages = map[string]string{
	"gunicorn": "gunicorn==23.0.0",
	"uvicorn":  "uvicorn[standard]==0.35.0",
}

// pythonCPUTorchIndex is PyTorch's own index of CPU-only builds. A deployment
// container is never given a GPU, and PyPI's Linux x86_64 torch is the CUDA
// build with several gigabytes of NVIDIA libraries.
const pythonCPUTorchIndex = "https://download.pytorch.org/whl/cpu"

// pythonVersionChoice is the interpreter family a build uses and what chose
// it, for the evidence and for preflight.
type pythonVersionChoice struct {
	version string
	source  string
	// file is where the choice was read, for the evidence.
	file string
	// patch is a patch release the source pinned (3.13.1), which the
	// catalogue's family serves; raised is a declared family below the
	// catalogue (3.9) that the build moved to 3.10.
	patch  string
	raised string
	// limited are the pins that kept an undeclared version below the
	// default; blockers the ones the chosen family cannot install.
	limited  []string
	blockers []string
}

// pythonVersionInputs are the places a project declares its interpreter, in
// the order they decide, and the pins that constrain it.
type pythonVersionInputs struct {
	explicit string
	declared []pythonVersionDeclaration
	// constraint is a range (requires-python, Poetry's python, a setup
	// python_requires), constraintSource what declared it and
	// constraintFile the file.
	constraint, constraintSource, constraintFile string
	blockers                                     map[int][]string
}

// pythonVersionDeclaration is a file that pins a family: source is how the
// evidence names it and file the file itself.
type pythonVersionDeclaration struct{ source, file, value string }

// choosePythonRecipeVersion resolves the interpreter family for a build from
// an explicit setting, a .python-version, a runtime.txt and a pyproject.
func choosePythonRecipeVersion(explicit, versionFile, runtimeFile, pyproject string) (string, error) {
	inputs := pythonVersionInputs{explicit: explicit,
		declared: []pythonVersionDeclaration{{".python-version", ".python-version", versionFile}, {"runtime.txt", "runtime.txt", runtimeFile}}}
	inputs.constraint = pythonRequiresText(pyproject)
	choice, err := resolvePythonVersion(inputs)
	return choice.version, err
}

// resolvePythonVersion picks the family: an explicit setting, else the first
// file that pins one, else the newest catalogue family within the declared
// range that is not above the default, preferring one every pin publishes
// wheels for. A floor above the default selects the family it asks for; a
// range the catalogue cannot satisfy is refused by name, never replaced by
// the default.
func resolvePythonVersion(in pythonVersionInputs) (pythonVersionChoice, error) {
	choice := pythonVersionChoice{}
	finish := func(version, source string) (pythonVersionChoice, error) {
		choice.version, choice.source = version, source
		choice.blockers = in.blockers[pythonMinor(version)]
		return choice, nil
	}
	if explicit := strings.TrimSpace(in.explicit); explicit != "" {
		if !pythonRecipeVersionRE.MatchString(explicit) {
			return choice, fmt.Errorf("%w: the Python recipe supports Python 3.10 to 3.14; select a supported version or use a Dockerfile", ErrUnsupportedBuilder)
		}
		return finish(explicit, "selected in Build settings")
	}
	for _, declaration := range in.declared {
		family, patch := pythonVersionFromFileValue(declaration.value)
		if family == "" {
			continue
		}
		source := declaration.source + " " + firstNonEmpty(patch, family)
		choice.file = declaration.file
		if pythonRecipeVersionRE.MatchString(family) {
			if patch != "" && patch != family {
				choice.patch = patch
			}
			return finish(family, source)
		}
		if strings.HasPrefix(family, "3.") && pythonMinor(family) < 10 {
			// The oldest family the catalogue carries runs code written for
			// 3.8 or 3.9 unless a pin publishes nothing for it.
			if len(in.blockers[10]) == 0 && (in.constraint == "" || pythonVersionSatisfies("3.10", in.constraint)) {
				choice.raised = family
				return finish("3.10", source)
			}
			reason := ""
			if len(in.blockers[10]) > 0 {
				reason = "; " + strings.Join(boundedNames(in.blockers[10]), ", ") + " cannot install on Python 3.10"
			}
			return choice, fmt.Errorf("%w: %s pins Python %s; the Python recipe supports Python 3.10 to 3.14%s; use a Dockerfile", ErrUnsupportedBuilder, declaration.source, family, reason)
		}
		return choice, fmt.Errorf("%w: %s pins Python %s; the Python recipe supports Python 3.10 to 3.14; select a supported version or use a Dockerfile", ErrUnsupportedBuilder, declaration.source, family)
	}
	allowed := []string{}
	for _, version := range pythonRecipeVersions {
		if in.constraint == "" || pythonVersionSatisfies(version, in.constraint) {
			allowed = append(allowed, version)
		}
	}
	if len(allowed) == 0 {
		return choice, fmt.Errorf("%w: %s %s allows no Python the recipe builds (3.10 to 3.14); change the range or use a Dockerfile",
			ErrUnsupportedBuilder, firstNonEmpty(in.constraintSource, "requires-python"), in.constraint)
	}
	source := "the maintained default"
	if in.constraint != "" {
		source = firstNonEmpty(in.constraintSource, "requires-python") + " " + in.constraint
		choice.file = firstNonEmpty(in.constraintFile, "pyproject.toml")
	}
	// Newest first up to the default, then upwards from it for a floor that
	// asks for more.
	order := []string{}
	for index := len(allowed) - 1; index >= 0; index-- {
		if pythonMinor(allowed[index]) <= pythonMinor(defaultPythonRecipeVersion) {
			order = append(order, allowed[index])
		}
	}
	for _, version := range allowed {
		if pythonMinor(version) > pythonMinor(defaultPythonRecipeVersion) {
			order = append(order, version)
		}
	}
	for _, version := range order {
		if len(in.blockers[pythonMinor(version)]) == 0 {
			if version != order[0] {
				choice.limited = in.blockers[pythonMinor(order[0])]
			}
			return finish(version, source)
		}
	}
	return finish(order[0], source)
}

// pythonVersionFromFile reads the family out of the first meaningful line of
// a version file: "3.12", "3.12.4", "python-3.12.4", "cpython@3.12".
func pythonVersionFromFile(content string) string {
	family, _ := pythonVersionFromFileValue(content)
	return family
}

// pythonVersionFromFileValue reads a family and, when the file names one, a
// patch release.
func pythonVersionFromFileValue(content string) (string, string) {
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		match := pythonVersionLiteralRE.FindStringSubmatch(line)
		if match == nil {
			return "", ""
		}
		family := match[1] + "." + match[2]
		return family, family + match[3]
	}
	return "", ""
}

// pythonRequiresText is the requires-python (or Poetry python) value of a
// pyproject, in either quote style.
func pythonRequiresText(pyproject string) string {
	match := pythonRequiresRE.FindStringSubmatch(pyproject)
	if match == nil {
		return ""
	}
	return firstNonEmpty(match[1], match[2])
}

// pythonVersionForConstraint picks a catalogue family inside a PEP 440 or
// Poetry range: the newest at or below the default, else the oldest above
// it. "" means the catalogue has none.
func pythonVersionForConstraint(constraint string) string {
	choice, err := resolvePythonVersion(pythonVersionInputs{constraint: constraint})
	if err != nil {
		return ""
	}
	return choice.version
}

func pythonMinor(version string) int {
	_, minor, _ := strings.Cut(version, ".")
	value := 0
	for _, r := range minor {
		if r < '0' || r > '9' {
			break
		}
		value = value*10 + int(r-'0')
	}
	return value
}

// pythonVersionFiles are the interpreter declarations beside a project that
// are not dependency manifests, read from the root or the nearest directory
// above it that has one.
type pythonVersionFiles struct {
	pythonVersion, pythonVersionPath string
	runtimeTxt                       string
	toolVersions, toolVersionsPath   string
	mise, misePath                   string
}

var (
	toolVersionsPythonRE = regexp.MustCompile(`(?m)^\s*python\s+(\S+)`)
	misePythonRE         = regexp.MustCompile(`(?m)^\s*python\s*=\s*(?:\[\s*)?["']([^"']+)["']|(?m)^\s*python\s*=\s*\{[^}]*version\s*=\s*["']([^"']+)["']`)
)

// pythonVersionInputsFor gathers a project's declarations in the order they
// decide.
func pythonVersionInputsFor(explicit string, files pythonVersionFiles, project pythonProject, arch string) pythonVersionInputs {
	inputs := pythonVersionInputs{explicit: explicit, blockers: project.versionBlockers(arch)}
	add := func(source, file, value string) {
		if strings.TrimSpace(value) != "" {
			inputs.declared = append(inputs.declared, pythonVersionDeclaration{source: source, file: file, value: value})
		}
	}
	add(firstNonEmpty(files.pythonVersionPath, ".python-version"), files.pythonVersionPath, files.pythonVersion)
	add("runtime.txt", "runtime.txt", files.runtimeTxt)
	if match := toolVersionsPythonRE.FindStringSubmatch(files.toolVersions); match != nil {
		add(firstNonEmpty(files.toolVersionsPath, ".tool-versions"), files.toolVersionsPath, match[1])
	}
	if match := misePythonRE.FindStringSubmatch(files.mise); match != nil {
		add(firstNonEmpty(files.misePath, "mise.toml"), files.misePath, firstNonEmpty(match[1], match[2]))
	}
	add("Pipfile python_version", "Pipfile", project.pipfile.python)
	if project.condaFound && project.conda.python != "" {
		add(project.conda.file+" python", project.conda.file, strings.TrimLeft(project.conda.python, "=<>!~"))
	}
	switch {
	case project.pyproject.requiresPython != "":
		inputs.constraint, inputs.constraintSource, inputs.constraintFile = project.pyproject.requiresPython, "requires-python", "pyproject.toml"
	case project.pyproject.poetryPython != "":
		inputs.constraint, inputs.constraintSource, inputs.constraintFile = project.pyproject.poetryPython, "Poetry's python", "pyproject.toml"
	case project.lock != nil && project.lock.requiresPython != "":
		inputs.constraint, inputs.constraintSource, inputs.constraintFile = project.lock.requiresPython, "uv.lock requires-python", "uv.lock"
	case project.setupPython != "":
		inputs.constraint, inputs.constraintSource, inputs.constraintFile = project.setupPython, "python_requires", "setup.py"
	default:
		inputs.constraint, inputs.constraintFile = pythonRequiresText(string(project.files["pyproject.toml"])), "pyproject.toml"
	}
	return inputs
}

// readPythonVersionFiles reads the version files a build root declares, or
// the nearest directory above it within boundary that declares them: a
// monorepo keeps .python-version at its top.
func readPythonVersionFiles(boundary, root string) pythonVersionFiles {
	files := pythonVersionFiles{}
	relative := checkoutPath(boundary, root)
	directories := []string{relative}
	for directory := relative; directory != "" && directory != "."; {
		directory = path.Dir(directory)
		if directory == "." {
			directory = ""
		}
		directories = append(directories, directory)
		if directory == "" {
			break
		}
	}
	read := func(name string) (string, string) {
		for _, directory := range directories {
			content, err := readContainedRegular(boundary, joinRoot(directory, name), 4096)
			if err != nil {
				continue
			}
			return string(content), joinRoot(directory, name)
		}
		return "", ""
	}
	files.pythonVersion, files.pythonVersionPath = read(".python-version")
	if content, err := readContainedRegular(root, "runtime.txt", 4096); err == nil {
		files.runtimeTxt = string(content)
	}
	files.toolVersions, files.toolVersionsPath = read(".tool-versions")
	files.mise, files.misePath = read("mise.toml")
	if files.mise == "" {
		files.mise, files.misePath = read(".mise.toml")
	}
	return files
}

// pythonInstall is how the recipe installs dependencies at a root: which
// manifest it reads, the exact install command, and the environment the
// installed packages live in so the start command finds them.
type pythonInstall struct {
	kind    string
	file    string
	command string
	env     []string
	// serverInstall installs an undeclared process manager into the same
	// environment as the dependencies.
	serverInstall string
	toolchain     string
	notes         []string
	// sanitize are the requirement files whose local-environment lines the
	// build rewrites before pip reads them.
	sanitize []string
}

// pythonInstallChoice is what the install depends on beyond the manifests.
type pythonInstallChoice struct {
	version string
	// member is a uv workspace member's package name, installed from the
	// workspace root.
	member string
	// installProject says the start command needs the project itself
	// installed: a src/ layout, or one of its console scripts.
	installProject bool
}

// planPythonInstall reads the manifests in order of how much they pin: a uv,
// Poetry or PDM lock, then Pipfile, then a requirement file, then a bare
// pyproject, setup file or conda environment. A lock that no longer matches
// its manifest is resolved again inside the build — the run log says so and
// preflight warns — rather than installing a set of packages that is missing
// what the manifest now declares. Unpinned requirements are accepted and
// preflight names what a rebuild may resolve differently.
func planPythonInstall(project pythonProject, choice pythonInstallChoice) (pythonInstall, error) {
	drift := project.lockDrift()
	stale := drift != nil && drift.State == LockfileStale
	switch {
	case project.lockFile == "uv.lock":
		sync := "uv sync --locked --no-dev"
		install := pythonInstall{kind: "uv.lock", file: "uv.lock",
			// uv installs into the project's .venv; putting it first on PATH is
			// what lets the start command say `uvicorn` rather than a path. uv
			// may only use the image's own interpreter, the one the release
			// evidence names by digest, never download another. The
			// interpreter is named on the sync alone: as UV_PYTHON it would
			// also be where `uv pip install` puts a server, outside the .venv.
			env:           []string{"VIRTUAL_ENV=/app/.venv", "PATH=/app/.venv/bin:$PATH", "UV_PYTHON_DOWNLOADS=never"},
			serverInstall: "uv pip install --no-cache", toolchain: "uv " + pythonUVRelease}
		if stale {
			sync = "uv sync --no-dev"
			install.notes = append(install.notes, drift.Note+"; the build resolves it again, so versions may differ from the lock")
		}
		if choice.member != "" {
			sync += " --package " + choice.member
		}
		install.command = "pip install --no-cache-dir uv==" + pythonUVRelease + " && " + sync + " --python /usr/local/bin/python"
		return install, nil
	case project.lockFile == "poetry.lock" || (project.lock == nil && project.pyproject.poetry && !project.pipfileLock.present):
		command := "poetry install --only main --no-root --no-interaction"
		if choice.installProject {
			command = "poetry install --only main --no-interaction"
		}
		install := pythonInstall{kind: "poetry.lock", file: firstNonEmpty(project.lockFile, "pyproject.toml"),
			// Installing into the interpreter itself rather than a Poetry
			// virtualenv keeps the start command's `gunicorn` on PATH.
			env:           []string{"POETRY_VIRTUALENVS_CREATE=false"},
			serverInstall: "pip install --no-cache-dir", toolchain: "poetry " + pythonPoetryRelease}
		if stale {
			command = "poetry lock --no-interaction && " + command
			install.notes = append(install.notes, drift.Note+"; the build locks again first (Poetry keeps the versions it can), so versions may differ from the lock")
		}
		install.command = "pip install --no-cache-dir poetry==" + pythonPoetryRelease + " && " + command
		return install, nil
	case project.lockFile == "pdm.lock":
		export := "pdm export --prod -o /tmp/jd-requirements.txt"
		install := pythonInstall{kind: "pdm.lock", file: "pdm.lock", serverInstall: "pip install --no-cache-dir", toolchain: "pdm " + pythonPDMRelease}
		if stale {
			export = "pdm lock --update-reuse && " + export
			install.notes = append(install.notes, drift.Note+"; the build locks again first, so versions may differ from the lock")
		}
		install.command = "pip install --no-cache-dir pdm==" + pythonPDMRelease + " && " + export +
			" && pip install --no-cache-dir --requirement /tmp/jd-requirements.txt" + pythonProjectInstall(project, choice)
		return install, nil
	case project.pipfile.present && (project.pipfileLock.present || project.requirementsFile == ""):
		install := pythonInstall{kind: "Pipfile.lock", file: "Pipfile.lock", serverInstall: "pip install --no-cache-dir", toolchain: "pipenv " + pythonPipenvRelease}
		var command string
		switch {
		case !project.pipfileLock.present:
			install.kind, install.file = "Pipfile", "Pipfile"
			command = "pipenv install --system --skip-lock"
		case stale:
			command = "pipenv install --system --skip-lock"
			install.notes = append(install.notes, drift.Note+"; the build installs from the Pipfile, so versions may differ from the lock")
		case project.pipfile.python != "" && pythonVersionFromFile(project.pipfile.python) != choice.version:
			// --deploy also refuses an interpreter other than the Pipfile's.
			command = "pipenv install --system --ignore-pipfile"
			install.notes = append(install.notes, "Pipfile asks for Python "+project.pipfile.python+"; the build installs Pipfile.lock on Python "+choice.version)
		default:
			command = "pipenv install --system --deploy"
		}
		install.command = "pip install --no-cache-dir pipenv==" + pythonPipenvRelease + " && " + command
		return install, nil
	case project.requirementsFile != "" && len(project.manifestConflict()) == 0:
		if len(project.requirements.escapes) > 0 {
			return pythonInstall{}, fmt.Errorf("%w: %s includes %s, outside the build root; move it inside or build from a Dockerfile",
				ErrUnsupportedBuilder, project.requirementsFile, project.requirements.escapes[0])
		}
		if len(project.requirements.missing) > 0 {
			return pythonInstall{}, fmt.Errorf("%w: %s includes %s, which is not in the repository", ErrUnsupportedBuilder, project.requirementsFile, project.requirements.missing[0])
		}
		if !pythonShellSafePathRE.MatchString(project.requirementsFile) {
			// The file is named unquoted in the install command.
			return pythonInstall{}, fmt.Errorf("%w: %q is installed by name in a shell command, which needs letters, digits and . _ - /; rename it or build with a Dockerfile",
				ErrUnsupportedBuilder, boundedText(project.requirementsFile, 128))
		}
		install := pythonInstall{kind: "requirements.txt", file: project.requirementsFile,
			command:       project.torchIndexPrefix() + "pip install --no-cache-dir --requirement " + project.requirementsFile + pythonProjectInstall(project, choice),
			serverInstall: "pip install --no-cache-dir"}
		artifacts, paths := project.localArtifacts(), project.localPaths()
		if len(artifacts)+len(paths) > 0 {
			for _, file := range project.requirements.paths() {
				if pythonShellSafePathRE.MatchString(file) {
					install.sanitize = append(install.sanitize, file)
				}
			}
		}
		if len(artifacts) > 0 {
			install.notes = append(install.notes, fmt.Sprintf("%d requirement line(s) name a conda build's artifact or another operating system's package; the build installs the conda ones by name and leaves the others to their own system", len(artifacts)))
		}
		if len(paths) > 0 {
			install.notes = append(install.notes, fmt.Sprintf("%d requirement line(s) point at a path on the machine that wrote them; the build leaves them out rather than installing their names from an index", len(paths)))
		}
		return install, nil
	case project.pyproject.declaresDependencies || (project.requirementsFile != "" && project.pyproject.present):
		install := pythonInstall{kind: "pyproject.toml", file: "pyproject.toml", serverInstall: "pip install --no-cache-dir"}
		if missing := project.manifestConflict(); len(missing) > 0 {
			install.notes = append(install.notes, project.requirementsFile+" omits "+strings.Join(boundedNames(missing), ", ")+
				" that pyproject.toml declares; the build installs from pyproject.toml")
		}
		if choice.installProject && project.pyproject.buildSystem {
			install.command = project.torchIndexPrefix() + "pip install --no-cache-dir ."
			return install, nil
		}
		// A PEP 621 project with no lock: read its dependency list with the
		// standard library rather than `pip install .`, which needs a build
		// backend and package discovery an application never set up.
		loader, prefix := `import tomllib`, ""
		if choice.version == "3.10" {
			loader, prefix = `import tomli as tomllib`, "pip install --no-cache-dir tomli==2.0.1 && "
		}
		install.command = prefix + `python -c '` + loader + `; print("\n".join(tomllib.load(open("pyproject.toml","rb")).get("project",{}).get("dependencies",[])))' > /tmp/jd-requirements.txt` +
			" && " + project.torchIndexPrefix() + "pip install --no-cache-dir --requirement /tmp/jd-requirements.txt"
		return install, nil
	case project.setupFound:
		file := "setup.py"
		if _, ok := project.files["setup.cfg"]; ok {
			file = "setup.cfg"
		}
		return pythonInstall{kind: "setup.py", file: file, command: project.torchIndexPrefix() + "pip install --no-cache-dir .",
			serverInstall: "pip install --no-cache-dir"}, nil
	case project.condaFound:
		lines, unknown, refused := project.condaRequirements()
		if len(unknown) > 0 {
			return pythonInstall{}, fmt.Errorf("%w: %s needs conda packages with no PyPI equivalent the recipe knows (%s); use a Dockerfile with micromamba",
				ErrUnsupportedBuilder, project.conda.file, strings.Join(boundedNames(unknown), ", "))
		}
		if len(refused) > 0 {
			return pythonInstall{}, fmt.Errorf("%w: %s lists %s, which the recipe cannot write into the image (a quote outside a marker, a backslash, a credential or a character outside printable ASCII); change the line or commit a requirements.txt",
				ErrUnsupportedBuilder, project.conda.file, strings.Join(boundedNames(refused), ", "))
		}
		if pipList := project.conda.pipList; len(pipList.escapes) > 0 {
			return pythonInstall{}, fmt.Errorf("%w: %s includes %s, outside the build root; move it inside or build from a Dockerfile", ErrUnsupportedBuilder, project.conda.file, pipList.escapes[0])
		} else if len(pipList.missing) > 0 {
			return pythonInstall{}, fmt.Errorf("%w: %s includes %s, which is not in the repository", ErrUnsupportedBuilder, project.conda.file, pipList.missing[0])
		}
		quoted := make([]string, 0, len(lines))
		for _, line := range lines {
			quoted = append(quoted, "'"+line+"'")
		}
		// Written beside environment.yml, where conda writes it, so the
		// list's -r includes and relative paths resolve as they do there.
		command := ": > " + pythonCondaRequirements
		if len(quoted) > 0 {
			command = "printf '%s\\n' " + strings.Join(quoted, " ") + " > " + pythonCondaRequirements
		}
		return pythonInstall{kind: "environment.yml", file: project.conda.file, serverInstall: "pip install --no-cache-dir",
			command: command + " && " + project.torchIndexPrefix() + "pip install --no-cache-dir --requirement " + pythonCondaRequirements + " && rm " + pythonCondaRequirements,
			notes:   []string{project.conda.file + " is installed with pip: " + strings.Join(boundedNames(lines), ", ")}}, nil
	case project.pyproject.present:
		// A pyproject that only configures tools declares nothing to
		// install; preflight says so before the start command fails.
		return pythonInstall{kind: "pyproject.toml", file: "pyproject.toml", command: "true", serverInstall: "pip install --no-cache-dir"}, nil
	}
	return pythonInstall{}, fmt.Errorf("%w: Python recipe requires requirements.txt, pyproject.toml, Pipfile, setup.py, environment.yml or a uv, Poetry or PDM lock", ErrUnsupportedBuilder)
}

// pythonShellSafePathRE is a requirement file path the generated Dockerfile
// may name in a shell command.
var pythonShellSafePathRE = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

// torchIndexPrefix adds PyTorch's CPU index for a pip install that pulls torch
// in, unless the manifests already name an index: pip takes the newest
// matching release across both, and a CPU build's +cpu local version sorts
// above the PyPI release of the same number. An index the operator passes as
// an install variable is kept beside it.
func (p pythonProject) torchIndexPrefix() string {
	if !p.usesTorch() || p.indexFacts().declaresAny {
		return ""
	}
	return `PIP_EXTRA_INDEX_URL="${PIP_EXTRA_INDEX_URL:+$PIP_EXTRA_INDEX_URL }` + pythonCPUTorchIndex + `" `
}

// pythonCondaRequirements is the requirement file the recipe writes a conda
// environment into.
const pythonCondaRequirements = ".jd-conda-requirements.txt"

var condaRequirementRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*(\[[A-Za-z0-9,._-]+\])?([<>=!~]=?[A-Za-z0-9.*+!-]+(,[<>=!~]=?[A-Za-z0-9.*+!-]+)*)?$`)

// condaRequirements turns a conda environment into the lines of the
// requirement file the build writes: each conda package the table maps to
// PyPI, with conda's fuzzy `=1.26` read as the release family it means, then
// the pip list as conda hands it to pip — markers, direct references, index
// options and -r includes. unknown lists the conda packages with no mapping,
// and refused the lines the Dockerfile cannot carry inside single quotes.
func (p pythonProject) condaRequirements() ([]string, []string, []string) {
	var lines, refused []string
	for _, pkg := range p.conda.conda {
		name := condaPyPIName(pkg.name)
		if name == "" {
			continue
		}
		version := pkg.version
		if strings.HasPrefix(version, "=") && !strings.HasPrefix(version, "==") {
			version = "==" + strings.TrimSuffix(strings.TrimPrefix(version, "="), ".*") + ".*"
		}
		if line := name + version; condaRequirementRE.MatchString(line) {
			lines = append(lines, line)
		} else {
			refused = append(refused, boundedText(pkg.name+version, 128))
		}
	}
	for _, raw := range p.conda.pipLines {
		line, ok := condaPipLine(raw)
		switch {
		case !ok:
			refused = append(refused, boundedText(redactURLCredentials(raw), 128))
		case line != "":
			lines = append(lines, line)
		}
	}
	return lines, p.conda.unmapped(), refused
}

// condaPipLine is a pip list entry as the Dockerfile carries it inside single
// quotes: printable ASCII with no quote of its own and no backslash, no
// credential, and nothing BuildKit would read as a heredoc. A marker's
// quotes become double quotes, which PEP 508 reads the same. A comment is
// dropped.
func condaPipLine(raw string) (string, bool) {
	line := strings.TrimSpace(raw)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", true
	}
	if body, marker, found := strings.Cut(line, ";"); found {
		line = body + ";" + strings.ReplaceAll(marker, "'", `"`)
	}
	if len(line) > 1024 || strings.ContainsAny(line, "'\\`") || strings.Contains(line, "<<") {
		return "", false
	}
	for _, url := range embeddedURLRE.FindAllString(line, -1) {
		if _, credentials := pythonIndexHost(url); credentials {
			return "", false
		}
	}
	for _, r := range line {
		if r < 0x20 || r > 0x7e {
			return "", false
		}
	}
	return line, true
}

// pythonSanitizeExpressions rewrite what a local environment left in a
// requirement file: a conda build's `name @ file:///croot/…` becomes the bare
// name, since conda repackaged that PyPI distribution; any other
// `name @ file:` is a developer's own package and is commented out rather
// than installed by name from an index that may publish something else
// under it; and an operating system's own package, or a path outside the
// checkout, is commented out — together with its --hash continuation lines,
// which are joined to it first, since pip reads a comment's trailing
// backslash as the end of the comment. A URL pip expands from a variable is
// left alone. They name no line of the file, so no repository content enters
// the Dockerfile.
func pythonSanitizeExpressions() []string {
	names := []string{"pyobjc([-_.][A-Za-z0-9_.-]+)?"}
	for name := range pythonOSOnly {
		if !strings.HasPrefix(name, "pyobjc") {
			names = append(names, strings.ReplaceAll(name, "-", "[-_.]"))
		}
	}
	sort.Strings(names)
	return []string{
		`s%^[[:space:]]*([A-Za-z0-9][A-Za-z0-9._-]*)[[:space:]]*(\[[^]]*\])?[[:space:]]*@[[:space:]]*file:(//(localhost)?)?/` + pythonCondaBuildDirectories + `[^;$]*(;.*)?$%\1\2 \7%`,
		`/^[[:space:]]*[A-Za-z0-9][A-Za-z0-9._-]*[[:space:]]*(\[[^]]*\])?[[:space:]]*@[[:space:]]*file:[^$]*$/{`,
		`:joinpath`, `/\\$/{`, `N`, `b joinpath`, `}`, `s/\\\n/ /g`, `s/^/# /`, `}`,
		`/^[[:space:]]*(` + strings.Join(names, "|") + `)([[:space:]]*([<>=!~;\\]|--).*)?$/I{`,
		`:join`, `/\\$/{`, `N`, `b join`, `}`, `s/\\\n/ /g`, `s/^/# /`, `}`,
		`s%^[[:space:]]*((-e|--editable)[[:space:]]+)?(/|[A-Za-z]:[\\/]).*$%# &%`,
	}
}

func (install pythonInstall) sanitizeLine() string {
	if len(install.sanitize) == 0 {
		return ""
	}
	parts := []string{"sed -i -E"}
	for _, expression := range pythonSanitizeExpressions() {
		parts = append(parts, "-e '"+expression+"'")
	}
	return "RUN " + strings.Join(append(parts, install.sanitize...), " ")
}

// undeclaredPythonServers lists the process managers a start command runs
// that no manifest declares, in the order they are run.
func undeclaredPythonServers(start string, deps pythonDependencies) []string {
	var result []string
	seen := map[string]bool{}
	for _, segment := range strings.Split(start, "&&") {
		fields := strings.Fields(segment)
		if len(fields) == 0 {
			continue
		}
		program := fields[0]
		if _, ok := pythonServerPackages[program]; ok && !deps.has(program) && !seen[program] {
			seen[program] = true
			result = append(result, program)
		}
	}
	return result
}

func pythonServerRequirement(server string) string {
	return pythonServerPackages[server]
}

// pythonRecipe is everything the Python recipe renders from.
type pythonRecipe struct {
	version   pythonVersionChoice
	install   pythonInstall
	framework string
	servers   []string
	// systemPackages are installed from Debian before the source is copied
	// in: what the dependencies need, and what the plan adds.
	systemPackages []string
	// assets is the Node install that builds front-end assets in dir (a
	// package.json at the root, or in a static_src or theme directory).
	assets    *nodeInstallPlan
	assetsDir string
	inputs    []string
	// contextDir is a uv workspace's root when the build installs a member
	// from it, and workdir the member's directory inside it.
	contextDir, workdir string
	// env is what the framework's server needs from its environment.
	env []string
	// webConcurrency says the start command's server reads its worker
	// count from WEB_CONCURRENCY and nothing loads a model per worker.
	webConcurrency bool
}

// selectPythonRecipe reads a Python root as the build will install it.
func selectPythonRecipe(boundary, root string, config BuildPlanConfig) (pythonRecipe, error) {
	recipe := pythonRecipe{}
	arch := pythonBuildArch(config.TargetPlatform)
	installRoot := root
	workspace := findPythonWorkspace(boundary, root)
	if workspace != nil && (!safeRelativePath(workspace.member) || !nodeMemberPathRE.MatchString(workspace.member) || !pythonPackageNameRE.MatchString(workspace.name)) {
		// The member's directory and name are written unquoted into the
		// recipe's WORKDIR and uv sync lines.
		return recipe, fmt.Errorf("%w: the uv workspace member %q (package %q) needs a directory of letters, digits and . _ @ + - / and a package name of letters, digits and -; build it with a Dockerfile", ErrUnsupportedBuilder, boundedText(workspace.member, 128), boundedText(workspace.name, 128))
	}
	if workspace != nil {
		installRoot = filepath.Join(boundary, filepath.FromSlash(workspace.root))
		recipe.contextDir, recipe.workdir = firstNonEmpty(workspace.root, "."), workspace.member
	}
	files := readPythonSourceFiles(installRoot)
	project := readPythonProject(files)
	if workspace == nil && !project.hasManifest() {
		return recipe, fmt.Errorf("%w: Python recipe requires requirements.txt, pyproject.toml, Pipfile, setup.py, environment.yml or a uv, Poetry or PDM lock", ErrUnsupportedBuilder)
	}
	if name := pythonWorkspaceMemberSource(files, project, root); name != "" && workspace == nil {
		return recipe, fmt.Errorf("%w: %s comes from a uv workspace (tool.uv.sources %s = { workspace = true }); build from the workspace root", ErrUnsupportedBuilder, name, name)
	}
	versionFiles := readPythonVersionFiles(boundary, root)
	memberProject := project
	if workspace != nil {
		memberProject = readPythonProject(readPythonSourceFiles(root))
	}
	inputs := pythonVersionInputsFor(config.PythonVersion, versionFiles, memberProject, arch)
	choice, err := resolvePythonVersion(inputs)
	if err != nil {
		return recipe, err
	}
	// The form seeds the setting from detection, so a setting equal to what
	// the source declares is that declaration, and the run log names it and
	// the pins that chose it.
	if inputs.explicit != "" {
		inputs.explicit = ""
		if declared, declaredErr := resolvePythonVersion(inputs); declaredErr == nil && declared.version == choice.version {
			choice = declared
		}
	}
	recipe.version = choice
	if strings.TrimSpace(config.StartCommand) == "" {
		return recipe, fmt.Errorf("%w: Python recipe requires a start command; detection proposes one for the frameworks it recognises", ErrUnsupportedBuilder)
	}
	installChoice := pythonInstallChoice{version: choice.version, installProject: pythonStartNeedsProject(config.StartCommand, project)}
	if workspace != nil {
		installChoice.member = workspace.name
	}
	install, err := planPythonInstall(project, installChoice)
	if err != nil {
		return recipe, err
	}
	recipe.install = install
	deps := memberProject.deps
	recipe.servers = undeclaredPythonServers(config.StartCommand, deps)
	if framework := matchPythonFramework(deps); framework != nil {
		recipe.framework = framework.Name
	}
	recipe.env = pythonFrameworkEnv(recipe.framework)
	recipe.webConcurrency = pythonReadsWebConcurrency(config.StartCommand) && !pythonLoadsModels(deps)
	settings := ""
	if deps.names["django"] {
		settings = pythonDjangoSettingsText(root)
	}
	recipe.systemPackages = mergeSystemPackages(memberProject.systemPackages(settings), config.SystemPackages)
	if dir, ok := pythonAssetsDirectory(root); ok && workspace == nil {
		source, plan, err := pythonAssetInstall(root, dir, config.PackageManager, nodeTargetArch(config.TargetPlatform))
		if err != nil {
			return recipe, err
		}
		if plan.blocked != nil {
			return recipe, fmt.Errorf("%w: the Python recipe builds %s's front-end assets with a Node stage: %s", ErrUnsupportedBuilder, joinRoot(dir, "package.json"), plan.blocked.Measured)
		}
		recipe.assets, recipe.assetsDir = &plan, dir
		for _, input := range source.installInputs(plan) {
			recipe.inputs = append(recipe.inputs, joinRoot(dir, input))
		}
	}
	return recipe, nil
}

// mergeSystemPackages is what the dependencies need and what the plan adds,
// sorted and without duplicates.
func mergeSystemPackages(detected []DetectedSystemPackage, planned []string) []string {
	set := map[string]bool{}
	for _, pkg := range detected {
		set[pkg.Name] = true
	}
	for _, name := range planned {
		if systemPackageRE.MatchString(name) {
			set[name] = true
		}
	}
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// systemPackageRE is a Debian package name.
var systemPackageRE = regexp.MustCompile(`^[a-z0-9][a-z0-9.+-]+$`)

// pythonStartNeedsProject says the start command needs the project installed
// as a package: it runs one of its console scripts, or its code lives under
// src/ and is imported by package name.
func pythonStartNeedsProject(start string, project pythonProject) bool {
	fields := strings.Fields(start)
	for _, field := range fields {
		if _, ok := project.pyproject.scripts[field]; ok {
			return true
		}
	}
	return project.pyproject.poetryFromSrc || project.pyproject.setuptoolsSrc ||
		slices.ContainsFunc(project.pyproject.hatchPackages, func(pkg string) bool { return strings.HasPrefix(pkg, "src/") })
}

// pythonProjectInstall installs the project itself after a requirement file
// or a PDM export installed its dependencies, when the start command runs one
// of its console scripts or imports it by package name from src/. Only a
// project that names a build backend can be installed; --no-deps keeps the
// versions the requirements pinned.
func pythonProjectInstall(project pythonProject, choice pythonInstallChoice) string {
	if !choice.installProject || !project.pyproject.buildSystem {
		return ""
	}
	return " && pip install --no-cache-dir --no-deps ."
}

// pythonRecipeBases lists the images the recipe resolves, in the order the
// Dockerfile references them: the Python image, then the Node image of the
// asset stage when there is one. The Python tag names its Debian release, so
// the system package names cannot drift under the recipe.
func pythonRecipeBases(recipe pythonRecipe) []string {
	bases := []string{"python:" + recipe.version.version + "-slim-trixie"}
	if recipe.assets != nil {
		bases = append(bases, recipe.assets.baseImages(false)...)
	}
	return bases
}

// pythonRecipeToolchain names what the build ran with, for the evidence.
func pythonRecipeToolchain(recipe pythonRecipe) string {
	parts := []string{"python " + recipe.version.version}
	if recipe.install.toolchain != "" {
		parts = append(parts, recipe.install.toolchain)
	}
	if recipe.assets != nil {
		parts = append(parts, "assets: "+recipe.assets.toolchain)
	}
	return strings.Join(parts, " · ")
}

// pythonRecipeNotes are the decisions preparation made that the run log
// states.
func pythonRecipeNotes(recipe pythonRecipe) []string {
	notes := []string{"Python " + recipe.version.version + " from " + recipe.version.source}
	if recipe.version.patch != "" {
		notes = append(notes, recipe.version.patch+" is served by the catalogue's "+recipe.version.version+" image")
	}
	if recipe.version.raised != "" {
		notes = append(notes, "the source pins Python "+recipe.version.raised+", which the recipe does not carry; building on 3.10")
	}
	if len(recipe.version.limited) > 0 {
		notes = append(notes, strings.Join(boundedNames(recipe.version.limited), ", ")+" publish no wheels for Python "+defaultPythonRecipeVersion+"; using "+recipe.version.version)
	}
	if len(recipe.systemPackages) > 0 {
		notes = append(notes, "system packages: "+strings.Join(recipe.systemPackages, " "))
	}
	notes = append(notes, recipe.install.notes...)
	if recipe.assets != nil {
		notes = append(notes, "front-end assets in "+rootLabelOf(recipe.assetsDir)+" are built by a Node stage inside the Python recipe")
		notes = append(notes, recipe.assets.notes...)
	}
	return notes
}

func renderPythonDockerfile(recipe pythonRecipe, config BuildPlanConfig, bases []ResolvedImage, installSecrets, buildSecrets string) ([]string, error) {
	if len(bases) < 1 || (recipe.assets != nil && len(bases) < 2) {
		return nil, ErrBuilderUnavailable
	}
	var lines []string
	if recipe.assets != nil {
		stage, _, err := nodeInstallStage(*recipe.assets, bases[1], bases, "assets-toolchain", "assets", installSecrets)
		if err != nil {
			return nil, err
		}
		if recipe.assetsDir != "" {
			// The install and the build run in the asset package's own
			// directory, over a copy of the whole application.
			for index, line := range stage {
				if line == "COPY . ." {
					stage = append(stage[:index+1], append([]string{"WORKDIR /app/" + recipe.assetsDir}, stage[index+1:]...)...)
					break
				}
			}
		}
		lines = append(stage, recipe.assets.buildRun(buildSecrets, recipe.assets.build, boundToBuild(config.Secrets)), "RUN rm -rf node_modules")
	}
	lines = append(lines, "FROM "+immutableImageReference(bases[0]), "WORKDIR /app",
		"ENV PYTHONUNBUFFERED=1 PYTHONDONTWRITEBYTECODE=1 PIP_DISABLE_PIP_VERSION_CHECK=1 PIP_ROOT_USER_ACTION=ignore",
		pythonRecipeNetworkEnv())
	if len(recipe.systemPackages) > 0 {
		// Before the source, so the layer is reused across commits.
		lines = append(lines, "RUN apt-get update && apt-get install -y --no-install-recommends "+strings.Join(recipe.systemPackages, " ")+" && rm -rf /var/lib/apt/lists/*")
	}
	for _, env := range recipe.install.env {
		lines = append(lines, "ENV "+env)
	}
	if recipe.assets != nil {
		lines = append(lines, "COPY --from=assets /app /app")
	} else {
		lines = append(lines, "COPY . .")
	}
	if sanitize := recipe.install.sanitizeLine(); sanitize != "" {
		lines = append(lines, sanitize)
	}
	lines = append(lines, "RUN "+installSecrets+recipe.install.command)
	for _, server := range recipe.servers {
		lines = append(lines, "RUN "+installSecrets+recipe.install.serverInstall+" '"+pythonServerRequirement(server)+"'")
	}
	if recipe.workdir != "" {
		lines = append(lines, "WORKDIR /app/"+recipe.workdir)
	}
	if command := strings.TrimSpace(config.BuildCommand); command != "" {
		lines = append(lines, "RUN "+buildSecrets+command)
	}
	for _, env := range recipe.env {
		lines = append(lines, "ENV "+env)
	}
	return append(lines, shellCMD(config.StartCommand)), nil
}

var pythonWorkersFlagRE = regexp.MustCompile(`(?:^|\s)(?:-w|--workers)(?:[=\s]|\d)|\s--reload\b|\s-k\s+(?:eventlet|gevent)`)

// pythonReadsWebConcurrency says a start command runs gunicorn or uvicorn
// without choosing a worker count: both then read WEB_CONCURRENCY.
func pythonReadsWebConcurrency(start string) bool {
	for _, segment := range strings.Split(start, "&&") {
		fields := strings.Fields(segment)
		if len(fields) == 0 || (fields[0] != "gunicorn" && fields[0] != "uvicorn") {
			continue
		}
		return !pythonWorkersFlagRE.MatchString(segment)
	}
	return false
}

// pythonLoadsModels says the application holds a machine-learning model in
// memory, which each extra worker would load again.
func pythonLoadsModels(deps pythonDependencies) bool {
	for _, name := range append(append([]string(nil), pythonModelDistributions...), pythonTorchDistributions...) {
		if deps.has(name) {
			return true
		}
	}
	for _, name := range []string{"tensorflow", "keras", "jax", "onnxruntime", "xgboost", "lightgbm", "spacy"} {
		if deps.has(name) {
			return true
		}
	}
	return false
}
