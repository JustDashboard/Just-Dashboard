package deploy

import (
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// pythonProject is everything a Python root's manifests say, read once: the
// manifests themselves, the lock, the requirement file a deployment installs
// from with every file it includes, and what they add up to — the
// dependencies, how they are installed, and what the lock says about the
// manifest it was generated from.
type pythonProject struct {
	files       map[string][]byte
	pyproject   pyprojectFacts
	pipfile     pipfileFacts
	pipfileLock pipfileLock
	// lock is the uv, Poetry or PDM lock, in the order the recipe prefers
	// them; lockFile names it.
	lock     *pythonLock
	lockFile string
	// requirementsFile is the requirement file a deployment installs from,
	// and requirements the closure of its includes.
	requirementsFile string
	requirements     pythonRequirementClosure
	setup            []pythonRequirement
	setupFound       bool
	setupPython      string
	conda            condaEnvironment
	condaFound       bool
	deps             pythonDependencies
}

// pythonLockFiles are the locks the recipe installs from, most preferred first.
var pythonLockFiles = []string{"uv.lock", "poetry.lock", "pdm.lock"}

func readPythonProject(files map[string][]byte) pythonProject {
	project := pythonProject{files: files}
	project.pyproject = readPyproject(files["pyproject.toml"])
	if content, ok := files["Pipfile"]; ok {
		project.pipfile = readPipfile(content)
		project.pipfile.present = true
	}
	if content, ok := files["Pipfile.lock"]; ok {
		project.pipfileLock = readPipfileLock(content)
		project.pipfileLock.present = true
	}
	for _, name := range pythonLockFiles {
		if content, ok := files[name]; ok {
			project.lock, project.lockFile = readPythonLock(name, content), name
			break
		}
	}
	project.requirementsFile = pythonInstallRequirements(files)
	project.requirements = readPythonRequirementClosure(files, project.requirementsFile)
	project.setup, project.setupPython, project.setupFound = setupRequirements(files)
	project.conda, project.condaFound = readCondaEnvironment(files)
	project.deps = project.dependencies()
	return project
}

// declared is what the project itself declares as its production
// dependencies, from whichever manifest the recipe installs from and the
// manifests beside it.
func (p pythonProject) declared() []pythonRequirement {
	var result []pythonRequirement
	result = append(result, p.requirements.requirements()...)
	result = append(result, p.pyproject.dependencies...)
	result = append(result, p.pyproject.poetryMain...)
	result = append(result, p.pipfile.packages...)
	if p.lock != nil && p.lock.root != nil {
		result = append(result, p.lock.root.requiresDist...)
		for _, name := range p.lock.root.dependencies {
			result = append(result, pythonRequirement{name: name, file: p.lockFile})
		}
	}
	if p.setupFound && p.requirementsFile == "" && !p.pyproject.declaresDependencies {
		result = append(result, p.setup...)
	}
	if p.condaFound {
		result = append(result, p.conda.pip...)
		for _, pkg := range p.conda.conda {
			if name := condaPyPIName(pkg.name); name != "" {
				result = append(result, pythonRequirement{name: normalizePythonName(name), file: p.conda.file})
			}
		}
	}
	return result
}

// dependencies adds the manifests up. A lock is authoritative for what is
// installed and proves pinning; a requirement file pins only when every entry
// does; a bare pyproject, setup file or conda environment pins nothing.
func (p pythonProject) dependencies() pythonDependencies {
	deps := pythonDependencies{names: map[string]bool{}, direct: map[string]bool{}, extras: map[string][]string{}}
	for _, requirement := range p.declared() {
		if requirement.name == "" {
			continue
		}
		deps.direct[requirement.name] = true
		deps.names[requirement.name] = true
		for _, extra := range requirement.extras {
			deps.extras[requirement.name] = appendUnique(deps.extras[requirement.name], extra)
		}
		// fastapi[standard] and uvicorn[standard] carry their server and its
		// websocket library; the extra is the only evidence.
		if (requirement.name == "fastapi" || requirement.name == "fastapi-slim") && requirement.hasExtra("standard") {
			deps.names["uvicorn"] = true
			deps.extras["uvicorn"] = appendUnique(deps.extras["uvicorn"], "standard")
		}
	}
	for name := range p.lock.mainPackages() {
		deps.names[name] = true
	}
	for name := range p.pipfileLock.packages {
		deps.names[name] = true
	}
	switch {
	case p.lock != nil:
		deps.source = p.lockFile
	case p.pipfileLock.present:
		deps.source = "Pipfile.lock"
	case p.requirementsFile != "":
		deps.source = p.requirementsFile
	case p.pipfile.present:
		deps.source = "Pipfile"
	case p.pyproject.present:
		deps.source = "pyproject.toml"
	case p.setupFound:
		deps.source = "setup.py"
		if _, ok := p.files["setup.cfg"]; ok {
			deps.source = "setup.cfg"
		}
	case p.condaFound:
		deps.source = p.conda.file
	}
	locked := p.lock != nil || (p.pipfileLock.present && p.pipfileLock.readable)
	if !locked {
		for _, requirement := range p.installedRequirements() {
			if requirement.name != "" && !requirement.pinned() && !requirement.editable {
				deps.unpinned = true
				break
			}
		}
	}
	return deps
}

// installedRequirements are the requirements the recipe installs when no
// lock decides: the requirement file's closure, else the pyproject, the
// Pipfile, the setup file or the conda environment's pip list.
func (p pythonProject) installedRequirements() []pythonRequirement {
	switch {
	case p.requirementsFile != "":
		return p.requirements.requirements()
	case p.pipfile.present:
		return p.pipfile.packages
	case p.pyproject.declaresDependencies:
		return p.pyproject.dependencies
	case p.pyproject.poetry:
		return p.pyproject.poetryMain
	case p.setupFound:
		return p.setup
	case p.condaFound:
		return p.conda.pip
	}
	return nil
}

// hasManifest says the files make their directory a Python project: a
// dependency manifest, a lock, or a requirement file a deployment installs.
// A requirements-dev.txt alone, a setup.cfg that only configures linters, or
// a setup.py whose requirements are computed are not. A setup file or a
// conda environment counts only when it installs something that serves:
// vendored libraries and documentation environments carry them too.
func (p pythonProject) hasManifest() bool {
	for _, name := range []string{"requirements.txt", "pyproject.toml", "uv.lock", "poetry.lock", "pdm.lock", "Pipfile", "Pipfile.lock"} {
		if _, ok := p.files[name]; ok {
			return true
		}
	}
	if p.requirementsFile != "" {
		return true
	}
	if p.setupFound || p.condaFound {
		for _, requirement := range p.declared() {
			if pythonServes(requirement.name) {
				return true
			}
		}
	}
	return false
}

// pythonServes says a distribution is a framework or server a deployment
// runs.
func pythonServes(name string) bool {
	if name == "gunicorn" || name == "uvicorn" || name == "waitress" || name == "hypercorn" || name == "daphne" {
		return true
	}
	for _, framework := range pythonFrameworks {
		if framework.Dependency == name {
			return true
		}
	}
	return false
}

// declaresNothing says the project installs nothing: a pyproject that only
// configures tools, with no lock, requirement file or other manifest.
func (p pythonProject) declaresNothing() bool {
	return len(p.declared()) == 0 && p.lock == nil && !p.pipfileLock.present && !p.pyproject.poetry &&
		p.requirementsFile == "" && !p.condaFound && !p.setupFound
}

// pythonLockDrift compares a lock with the manifest it was generated from, by
// name — and for uv, by specifier too, since uv records what it resolved
// against. Poetry's own content hash is not recomputed: a missing name is
// what detection proves, and Poetry names any other change itself.
func (p pythonProject) lockDrift() *DetectedLockfile {
	compare := func(file, manager string, declared []pythonRequirement, locked map[string]bool, lockedSpecs map[string]string) *DetectedLockfile {
		state := &DetectedLockfile{Path: file, Manager: manager, State: LockfileInSync}
		for _, requirement := range declared {
			if requirement.name == "" {
				continue
			}
			if !locked[requirement.name] {
				state.Missing = appendUnique(state.Missing, requirement.name)
				continue
			}
			if lockedSpecs != nil {
				if spec, known := lockedSpecs[requirement.name]; known && normalizeSpecifier(spec) != normalizeSpecifier(requirement.specifier) {
					state.Changed = appendUnique(state.Changed, requirement.name)
				}
			}
		}
		if len(state.Missing) > 0 || len(state.Changed) > 0 {
			state.State = LockfileStale
			parts := []string{}
			if len(state.Missing) > 0 {
				parts = append(parts, "missing: "+strings.Join(boundedNames(state.Missing), ", "))
			}
			if len(state.Changed) > 0 {
				parts = append(parts, "changed: "+strings.Join(boundedNames(state.Changed), ", "))
			}
			state.Note = file + " is older than " + map[string]string{"pipenv": "Pipfile"}[manager] + "; " + strings.Join(parts, "; ")
			if manager != "pipenv" {
				state.Note = file + " is older than pyproject.toml; " + strings.Join(parts, "; ")
			}
		}
		return state
	}
	switch {
	case p.lock != nil && p.lock.unreadable:
		return &DetectedLockfile{Path: p.lockFile, Manager: pythonLockManager(p.lockFile), State: LockfileUnknown}
	case p.lockFile == "uv.lock" && p.lock.root != nil:
		locked, specs := map[string]bool{}, map[string]string{}
		for _, requirement := range p.lock.root.requiresDist {
			if !strings.Contains(requirement.marker, "extra ==") {
				locked[requirement.name] = true
				specs[requirement.name] = requirement.specifier
			}
		}
		if len(p.lock.root.requiresDist) == 0 {
			// A lock written before uv recorded requires-dist still names
			// the project's dependencies.
			for _, name := range p.lock.root.dependencies {
				locked[name] = true
			}
			specs = nil
		}
		declared := p.pyproject.dependencies
		drift := compare("uv.lock", "uv", declared, locked, specs)
		for group, requirements := range p.pyproject.groups {
			present := map[string]bool{}
			for _, name := range p.lock.root.requiresDev[group] {
				present[name] = true
			}
			for _, requirement := range requirements {
				if requirement.name != "" && !present[requirement.name] {
					drift.Missing = appendUnique(drift.Missing, requirement.name)
				}
			}
		}
		if len(drift.Missing) > 0 && drift.State != LockfileStale {
			drift.State = LockfileStale
			drift.Note = "uv.lock is older than pyproject.toml; missing: " + strings.Join(boundedNames(drift.Missing), ", ")
		}
		return drift
	case p.lockFile == "poetry.lock" || p.lockFile == "pdm.lock":
		locked := map[string]bool{}
		for _, pkg := range p.lock.packages {
			locked[pkg.name] = true
		}
		// A path or VCS dependency is locked under the name its own metadata
		// gives, which the manifest's key need not match.
		declared := append(append([]pythonRequirement(nil), p.pyproject.poetryMain...), p.pyproject.dependencies...)
		kept := declared[:0]
		for _, requirement := range declared {
			if requirement.url == "" {
				kept = append(kept, requirement)
			}
		}
		return compare(p.lockFile, pythonLockManager(p.lockFile), kept, locked, nil)
	case p.pipfile.present && p.pipfileLock.present && p.pipfileLock.readable:
		locked := map[string]bool{}
		for name := range p.pipfileLock.packages {
			locked[name] = true
		}
		return compare("Pipfile.lock", "pipenv", p.pipfile.packages, locked, nil)
	}
	return nil
}

func pythonLockManager(file string) string {
	return map[string]string{"uv.lock": "uv", "poetry.lock": "poetry", "pdm.lock": "pdm", "Pipfile.lock": "pipenv"}[file]
}

// normalizeSpecifier compares specifiers as sets of clauses.
func normalizeSpecifier(specifier string) string {
	parts := strings.Split(strings.ReplaceAll(specifier, " ", ""), ",")
	kept := parts[:0]
	for _, part := range parts {
		if part != "" {
			kept = append(kept, part)
		}
	}
	sort.Strings(kept)
	return strings.Join(kept, ",")
}

// manifestConflict names the pyproject dependencies a requirement file the
// recipe would install from leaves out. With no lock, the file installed is
// the one that decides what the application gets, and a pyproject that
// declares more is the newer description of the project.
func (p pythonProject) manifestConflict() []string {
	if p.requirementsFile == "" || p.lock != nil || len(p.pyproject.dependencies) == 0 {
		return nil
	}
	listed := map[string]bool{}
	for _, requirement := range p.requirements.requirements() {
		listed[requirement.name] = true
	}
	var missing []string
	for _, requirement := range p.pyproject.dependencies {
		if requirement.name != "" && !listed[requirement.name] {
			missing = appendUnique(missing, requirement.name)
		}
	}
	return missing
}

// pythonOSOnly are distributions that install only on one operating system:
// a pip freeze from Windows or macOS lists them, and pip on Linux finds no
// file it can install ("from versions: none") or fails to build one.
var pythonOSOnly = map[string]string{
	"pywin32": "win32", "pypiwin32": "win32", "pywinpty": "win32", "windows-curses": "win32",
	"pyobjc": "darwin", "pyobjc-core": "darwin",
}

func pythonOSOnlyPlatform(name string) string {
	if platform := pythonOSOnly[name]; platform != "" {
		return platform
	}
	if strings.HasPrefix(name, "pyobjc-framework-") {
		return "darwin"
	}
	return ""
}

// localArtifacts are the requirement lines a local environment left behind:
// a conda build's `name @ file:///croot/…`, an editable or plain path outside
// the checkout, and an operating system's own packages without a marker.
func (p pythonProject) localArtifacts() []string {
	var lines []string
	for _, requirement := range p.requirements.requirements() {
		switch {
		case strings.HasPrefix(requirement.url, "file:"):
			lines = append(lines, requirement.text)
		case requirement.url != "" && (strings.HasPrefix(requirement.url, "/") || windowsPathRE.MatchString(requirement.url)):
			lines = append(lines, requirement.text)
		case requirement.marker == "" && pythonOSOnlyPlatform(requirement.name) != "":
			lines = append(lines, requirement.text)
		}
	}
	return lines
}

var windowsPathRE = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

// pythonPublicIndexes are package indexes anyone can read, which a build
// needs no credential for.
var pythonPublicIndexes = map[string]bool{
	"pypi.org": true, "pypi.python.org": true, "test.pypi.org": true, "files.pythonhosted.org": true,
	"download.pytorch.org": true, "pypi.nvidia.com": true, "data.pyg.org": true, "dl.fbaipublicfiles.com": true,
	"storage.googleapis.com": true, "pypi.ngc.nvidia.com": true, "github.com": true, "objects.githubusercontent.com": true,
}

// pythonIndexFacts is what the manifests say about where packages come from
// beyond PyPI: the private indexes and their credential variables, a
// credential written into a committed file, and VCS dependencies.
type pythonIndexFacts struct {
	private     []string
	variables   []DetectedVariable
	inline      []string
	gitSSH      []string
	git         bool
	declaresAny bool
}

func (p pythonProject) indexFacts() pythonIndexFacts {
	var facts pythonIndexFacts
	add := func(variable DetectedVariable) {
		for index, existing := range facts.variables {
			if existing.Name == variable.Name {
				facts.variables[index].InstallRequired = existing.InstallRequired || variable.InstallRequired
				return
			}
		}
		facts.variables = append(facts.variables, variable)
	}
	indexes := append(append(append([]pythonIndexOption(nil), p.requirements.indexes()...), p.pyproject.indexes...), p.pipfile.indexes...)
	for _, index := range indexes {
		facts.declaresAny = true
		host, credentials := pythonIndexHost(index.url)
		if credentials {
			facts.inline = appendUnique(facts.inline, index.file)
		}
		references := pythonRequirementVariableRE.FindAllStringSubmatch(index.url, -1)
		for _, reference := range references {
			add(DetectedVariable{Name: reference[1], Step: "install", InstallRequired: true, Sources: []string{index.file},
				SetupReason: index.file + " reads it into the package index address"})
		}
		if host == "" || pythonPublicIndexes[host] || len(references) > 0 || credentials || index.option == "--find-links" {
			continue
		}
		facts.private = appendUnique(facts.private, host)
		switch index.option {
		case "--index-url":
			add(DetectedVariable{Name: "PIP_INDEX_URL", Step: "install", Sources: []string{index.file},
				SetupReason: "the address of " + host + " with its credentials, if the index asks for them"})
		case "--extra-index-url":
			add(DetectedVariable{Name: "PIP_EXTRA_INDEX_URL", Step: "install", Sources: []string{index.file},
				SetupReason: "the address of " + host + " with its credentials, if the index asks for them"})
		case "poetry", "uv":
			name := pythonSourceVariablePart(index.name)
			if name == "" {
				continue
			}
			prefix := "POETRY_HTTP_BASIC_" + name
			if index.option == "uv" {
				prefix = "UV_INDEX_" + name
			}
			for _, suffix := range []string{"_USERNAME", "_PASSWORD"} {
				add(DetectedVariable{Name: prefix + suffix, Step: "install", Sources: []string{index.file},
					SetupReason: "the credential " + map[string]string{"poetry": "Poetry", "uv": "uv"}[index.option] + " sends to the " + index.name + " index"})
			}
		}
	}
	for _, requirement := range p.allRequirements() {
		switch {
		case strings.HasPrefix(requirement.url, "git+ssh://") || strings.HasPrefix(requirement.url, "git@") || strings.Contains(requirement.url, "ssh://git@"):
			facts.gitSSH = appendUnique(facts.gitSSH, requirement.nameOrURL())
			facts.git = true
		case strings.HasPrefix(requirement.url, "git+") || requirement.url == "git":
			facts.git = true
		}
		if _, credentials := pythonIndexHost(strings.TrimPrefix(requirement.url, "git+")); credentials && requirement.file != "" {
			facts.inline = appendUnique(facts.inline, requirement.file)
		}
	}
	if p.lock != nil {
		for _, pkg := range p.lock.packages {
			if pkg.source == "git" {
				facts.git = true
				if strings.HasPrefix(pkg.sourceRef, "ssh://") || strings.HasPrefix(pkg.sourceRef, "git+ssh://") || strings.HasPrefix(pkg.sourceRef, "git@") {
					facts.gitSSH = appendUnique(facts.gitSSH, pkg.name)
				}
			}
		}
	}
	if len(p.pipfileLock.git) > 0 {
		facts.git = true
	}
	return facts
}

// allRequirements are every requirement a manifest names, production or not,
// for the facts that concern any install: credentials and VCS sources.
func (p pythonProject) allRequirements() []pythonRequirement {
	result := append([]pythonRequirement(nil), p.declared()...)
	for _, requirements := range p.pyproject.optional {
		result = append(result, requirements...)
	}
	return result
}

func (r pythonRequirement) nameOrURL() string {
	if r.name != "" {
		return r.name
	}
	return redactURLCredentials(r.url)
}

// pythonIndexHost is an index URL's host, and whether the URL carries a
// credential of its own written literally rather than read from a
// ${VARIABLE} pip expands: a password, or a token given as an https URL's
// user. SSH's `git@` user is not one.
func pythonIndexHost(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	scheme, rest, found := strings.Cut(raw, "://")
	if !found {
		return "", false
	}
	parsed, err := url.Parse(pythonRequirementVariableRE.ReplaceAllString(raw, "x"))
	if err != nil {
		return "", false
	}
	host := strings.ToLower(parsed.Hostname())
	authority, _, _ := strings.Cut(rest, "/")
	at := strings.LastIndex(authority, "@")
	if at < 0 {
		return host, false
	}
	user, password, hasPassword := strings.Cut(authority[:at], ":")
	literal := func(value string) string {
		return strings.TrimSpace(pythonRequirementVariableRE.ReplaceAllString(value, ""))
	}
	if hasPassword {
		return host, literal(password) != ""
	}
	token := literal(user)
	web := strings.HasSuffix(strings.ToLower(scheme), "http") || strings.HasSuffix(strings.ToLower(scheme), "https")
	return host, web && token != "" && token != "git" && (len(token) >= 16 || pythonTokenPrefixRE.MatchString(token))
}

var pythonTokenPrefixRE = regexp.MustCompile(`^(?:ghp_|gho_|ghs_|github_pat_|glpat-|__token__)`)

func redactURLCredentials(raw string) string {
	scheme, rest, found := strings.Cut(raw, "://")
	if !found {
		return raw
	}
	host, tail, _ := strings.Cut(rest, "/")
	if at := strings.LastIndex(host, "@"); at >= 0 {
		host = host[at+1:]
	}
	return scheme + "://" + host + "/" + tail
}

var nonVariableRE = regexp.MustCompile(`[^A-Z0-9]+`)

// pythonSourceVariablePart is a Poetry source or uv index name as both tools
// spell it in a variable: upper-case, with every other character an
// underscore.
func pythonSourceVariablePart(name string) string {
	return strings.Trim(nonVariableRE.ReplaceAllString(strings.ToUpper(name), "_"), "_")
}

// pythonTorchDistributions pull PyTorch in: from PyPI, on x86_64, that is the
// CUDA build and several gigabytes of NVIDIA libraries.
var pythonTorchDistributions = []string{
	"torch", "torchvision", "torchaudio", "sentence-transformers", "ultralytics", "openai-whisper", "easyocr",
	"timm", "accelerate", "lightning", "pytorch-lightning", "torchmetrics", "diffusers", "whisperx", "open-clip-torch",
	"transformers", "kornia", "torchtext",
}

// pythonCUDAPackages are the NVIDIA runtime wheels a lock that pins a CUDA
// build of torch installs.
func (p pythonProject) lockedCUDAPackages() []string {
	var names []string
	if p.lock == nil {
		return nil
	}
	main := p.lock.mainPackages()
	for name := range main {
		if strings.HasPrefix(name, "nvidia-") || name == "triton" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// usesTorch says the pip install pulls PyTorch in.
func (p pythonProject) usesTorch() bool {
	for _, name := range pythonTorchDistributions {
		if p.deps.direct[name] {
			if name == "transformers" && !slices.Contains(p.deps.extras["transformers"], "torch") && !p.deps.direct["torch"] {
				continue
			}
			return true
		}
	}
	return false
}

// condaPyPIName maps a conda package to the PyPI distribution that provides
// the same import, for the packages a data application commonly takes from
// conda. "" means the package is not one this table knows, and skip means
// it is a library the Python image already carries.
func condaPyPIName(name string) string {
	if mapped, ok := condaToPyPI[name]; ok {
		if mapped == "skip" {
			return ""
		}
		return mapped
	}
	return ""
}

func condaKnown(name string) bool {
	_, ok := condaToPyPI[name]
	return ok
}

var condaToPyPI = map[string]string{
	// The interpreter's own libraries and the conda toolchain.
	"pip": "skip", "setuptools": "skip", "wheel": "skip", "ca-certificates": "skip", "certifi": "certifi",
	"openssl": "skip", "libffi": "skip", "sqlite": "skip", "zlib": "skip", "xz": "skip", "tk": "skip",
	"readline": "skip", "ncurses": "skip", "bzip2": "skip", "libuuid": "skip", "tzdata": "tzdata",
	"libgcc-ng": "skip", "libstdcxx-ng": "skip", "libgcc": "skip", "libgomp": "skip", "_libgcc_mutex": "skip",
	"_openmp_mutex": "skip", "ld_impl_linux-64": "skip", "libzlib": "skip", "libsqlite": "skip",
	"libnsl": "skip", "libxcrypt": "skip", "libexpat": "skip", "libmpdec": "skip", "libedit": "skip",
	"mkl": "skip", "mkl-service": "skip", "intel-openmp": "skip", "blas": "skip", "libblas": "skip",
	"libcblas": "skip", "liblapack": "skip", "libopenblas": "skip", "openblas": "skip", "python_abi": "skip",
	// Libraries published on PyPI under the same or a known name.
	"numpy": "numpy", "pandas": "pandas", "scipy": "scipy", "scikit-learn": "scikit-learn", "matplotlib": "matplotlib",
	"matplotlib-base": "matplotlib", "seaborn": "seaborn", "plotly": "plotly", "bokeh": "bokeh", "altair": "altair",
	"flask": "flask", "django": "django", "fastapi": "fastapi", "uvicorn": "uvicorn", "gunicorn": "gunicorn",
	"requests": "requests", "httpx": "httpx", "streamlit": "streamlit", "dash": "dash", "panel": "panel",
	"gradio": "gradio", "sqlalchemy": "sqlalchemy", "psycopg2": "psycopg2-binary", "psycopg": "psycopg[binary]",
	"pymongo": "pymongo", "redis-py": "redis", "redis": "redis", "pillow": "pillow", "pyyaml": "pyyaml",
	"beautifulsoup4": "beautifulsoup4", "lxml": "lxml", "openpyxl": "openpyxl", "xlrd": "xlrd", "statsmodels": "statsmodels",
	"sympy": "sympy", "networkx": "networkx", "jinja2": "jinja2", "click": "click", "tqdm": "tqdm", "joblib": "joblib",
	"nltk": "nltk", "spacy": "spacy", "gensim": "gensim", "pyarrow": "pyarrow", "polars": "polars", "duckdb": "duckdb",
	"folium": "folium", "geopandas": "geopandas", "shapely": "shapely", "python-dotenv": "python-dotenv",
	"transformers": "transformers", "pytorch": "torch", "torchvision": "torchvision", "torchaudio": "torchaudio",
	"tensorflow": "tensorflow", "keras": "keras", "xgboost": "xgboost", "lightgbm": "lightgbm", "psutil": "psutil",
	"pytz": "pytz", "python-dateutil": "python-dateutil", "py-opencv": "opencv-python-headless", "opencv": "opencv-python-headless",
	"pydantic": "pydantic", "aiohttp": "aiohttp", "openai": "openai", "langchain": "langchain", "scikit-image": "scikit-image",
	"h5py": "h5py", "numba": "numba", "sentence-transformers": "sentence-transformers", "ipywidgets": "ipywidgets",
	"werkzeug": "werkzeug", "markdown": "markdown", "boto3": "boto3", "sqlite3": "skip",
}

// pythonSystemPackage is a Debian package a Python distribution needs from
// the image: to build (a compiler, a library's headers) or to import (a
// shared library, a tool it runs).
type pythonSystemPackageRule struct {
	packages []string
	reason   string
}

// pythonSystemPackages maps a normalized distribution to the Debian trixie
// packages it needs. The names are trixie's own — libglib2.0-0t64, not the
// libglib2.0-0 older guides name — which is why the recipe's base is the
// slim-trixie image rather than a floating slim tag.
var pythonSystemPackages = map[string]pythonSystemPackageRule{
	"psycopg2":               {[]string{"gcc", "libc6-dev", "libpq-dev"}, "builds against libpq"},
	"psycopg-c":              {[]string{"gcc", "libc6-dev", "libpq-dev"}, "builds against libpq"},
	"mysqlclient":            {[]string{"gcc", "libc6-dev", "pkg-config", "default-libmysqlclient-dev"}, "builds against the MySQL client library"},
	"mariadb":                {[]string{"gcc", "libc6-dev", "libmariadb-dev"}, "builds against MariaDB Connector/C"},
	"python-ldap":            {[]string{"gcc", "libc6-dev", "libldap-dev", "libsasl2-dev"}, "builds against OpenLDAP"},
	"uwsgi":                  {[]string{"gcc", "libc6-dev"}, "compiles its server"},
	"pycairo":                {[]string{"gcc", "libc6-dev", "pkg-config", "libcairo2-dev"}, "builds against cairo"},
	"gdal":                   {[]string{"g++", "libgdal-dev"}, "builds against GDAL"},
	"opencv-python":          {[]string{"libgl1", "libglib2.0-0t64"}, "loads libGL and GLib at import"},
	"opencv-contrib-python":  {[]string{"libgl1", "libglib2.0-0t64"}, "loads libGL and GLib at import"},
	"weasyprint":             {[]string{"libpango-1.0-0", "libpangoft2-1.0-0", "libharfbuzz-subset0"}, "renders through Pango"},
	"python-magic":           {[]string{"libmagic1t64"}, "loads libmagic"},
	"pdf2image":              {[]string{"poppler-utils"}, "runs poppler's pdftoppm"},
	"pytesseract":            {[]string{"tesseract-ocr"}, "runs tesseract"},
	"pydub":                  {[]string{"ffmpeg"}, "runs ffmpeg"},
	"moviepy":                {[]string{"ffmpeg"}, "runs ffmpeg"},
	"openai-whisper":         {[]string{"ffmpeg"}, "runs ffmpeg"},
	"ffmpeg-python":          {[]string{"ffmpeg"}, "runs ffmpeg"},
	"pyodbc":                 {[]string{"unixodbc"}, "loads unixODBC"},
	"pyzbar":                 {[]string{"libzbar0t64"}, "loads zbar"},
	"pyvips":                 {[]string{"libvips42t64"}, "loads libvips"},
	"psycopg-binary-missing": {[]string{"libpq5"}, "psycopg loads libpq itself without its binary extra"},
}

// systemPackages lists what the project needs from the image, each with the
// dependency that needs it. settings is the text of the Django settings,
// read for GeoDjango.
func (p pythonProject) systemPackages(settings string) []DetectedSystemPackage {
	byName := map[string]*DetectedSystemPackage{}
	order := []string{}
	add := func(packages []string, reason, source string) {
		for _, name := range packages {
			if existing := byName[name]; existing != nil {
				continue
			}
			byName[name] = &DetectedSystemPackage{Name: name, Reason: reason, Source: source}
			order = append(order, name)
		}
	}
	names := make([]string, 0, len(p.deps.names))
	for name := range p.deps.names {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if rule, ok := pythonSystemPackages[name]; ok && name != "psycopg-binary-missing" {
			add(rule.packages, name+" "+rule.reason, p.deps.source)
		}
	}
	// psycopg 3 imports libpq unless its binary or C extra supplies one.
	if p.deps.names["psycopg"] && !p.deps.names["psycopg-binary"] && !p.deps.names["psycopg-c"] &&
		!slices.Contains(p.deps.extras["psycopg"], "binary") && !slices.Contains(p.deps.extras["psycopg"], "c") {
		rule := pythonSystemPackages["psycopg-binary-missing"]
		add(rule.packages, "psycopg "+rule.reason, p.deps.source)
	}
	if slices.Contains(p.deps.extras["psycopg"], "c") {
		rule := pythonSystemPackages["psycopg-c"]
		add(rule.packages, "psycopg[c] "+rule.reason, p.deps.source)
	}
	if p.deps.names["django"] && strings.Contains(settings, "django.contrib.gis") {
		add([]string{"gdal-bin"}, "GeoDjango (django.contrib.gis) loads GDAL and GEOS", "settings")
	}
	if facts := p.indexFacts(); facts.git {
		add([]string{"git"}, "a dependency is installed from a Git repository", p.deps.source)
		if len(facts.gitSSH) > 0 {
			add([]string{"openssh-client"}, "a dependency is cloned over SSH", p.deps.source)
		}
	}
	result := make([]DetectedSystemPackage, 0, len(order))
	for _, name := range order {
		result = append(result, *byName[name])
	}
	return result
}

// pythonNativeBuilds names the locked packages that are compiled for some
// interpreters and publish nothing a Linux build of this family can install,
// so pip or uv builds them from source; the system package table already
// covers the ones it knows.
func (p pythonProject) nativeBuilds(version, arch string) []string {
	if p.lock == nil {
		return nil
	}
	main := p.lock.mainPackages()
	var names []string
	for _, pkg := range p.lock.packages {
		if !main[pkg.name] || pythonSystemPackages[pkg.name].packages != nil {
			continue
		}
		support := pythonWheelSupport(pkg.wheels, arch)
		if support.binary && !support.supports(pythonMinor(version)) {
			names = append(names, pkg.name+"=="+pkg.version)
		}
	}
	sort.Strings(names)
	return names
}

// pythonSourceFiles is a root's Python manifests as the recipe reads them
// from a build root: the fixed names, every requirement file, and the files
// they include, each bounded.
func readPythonSourceFiles(root string) map[string][]byte {
	files := map[string][]byte{}
	read := func(name string, limit int64) {
		if _, done := files[name]; done {
			return
		}
		if content, err := readContainedRegular(root, name, limit); err == nil {
			files[name] = manifestText(content)
		} else if regularExists(root, name) {
			files[name] = []byte("locked")
		}
	}
	for _, name := range []string{"requirements.txt", "pyproject.toml", "Pipfile", "setup.py", "setup.cfg", "environment.yml", "environment.yaml"} {
		read(name, 2<<20)
	}
	for _, name := range append(append([]string(nil), pythonLockFiles...), "Pipfile.lock") {
		if !regularExists(root, name) {
			continue
		}
		if name == "Pipfile.lock" {
			read(name, 16<<20)
			continue
		}
		if content, ok := readCompactPythonLockAt(root, name); ok {
			files[name] = content
		} else {
			files[name] = []byte("locked")
		}
	}
	for _, name := range listContainedFiles(root, ".txt") {
		if pythonRequirementsNameRE.MatchString(strings.ToLower(name)) {
			read(name, 1<<20)
		}
	}
	for _, directory := range []string{"requirements", "Requirements"} {
		for _, name := range listContainedFiles(filepath.Join(root, directory), ".txt") {
			read(directory+"/"+name, 1<<20)
		}
	}
	// Includes are followed until every file the install reads is held.
	for round := 0; round < 3; round++ {
		closure := readPythonRequirementClosure(files, pythonInstallRequirements(files))
		if len(closure.missing) == 0 {
			break
		}
		for _, name := range closure.missing {
			read(name, 1<<20)
		}
	}
	return files
}
