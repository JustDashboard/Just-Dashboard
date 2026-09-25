package deploy

import (
	"bytes"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestPythonRequirementStringsAreReadAsPEP508(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		raw                          string
		name, specifier, marker, url string
		extras                       []string
		pinned                       bool
	}{
		{raw: "Django==5.1.4", name: "django", specifier: "==5.1.4", pinned: true},
		{raw: "celery[redis, msgpack]>=5.4 ; python_version >= '3.10'", name: "celery", specifier: ">=5.4", marker: "python_version >= '3.10'", extras: []string{"redis", "msgpack"}},
		{raw: "psycopg[binary] (>=3.2,<4)", name: "psycopg", specifier: ">=3.2,<4", extras: []string{"binary"}},
		{raw: "certifi @ file:///croot/certifi_1725551672989/work/certifi", name: "certifi", url: "file:///croot/certifi_1725551672989/work/certifi", pinned: true},
		{raw: "zope.interface===6.0", name: "zope-interface", specifier: "===6.0", pinned: true},
		{raw: "numpy==1.26.*", name: "numpy", specifier: "==1.26.*"},
	} {
		requirement, ok := parsePythonRequirement(fixture.raw)
		if !ok || requirement.name != fixture.name || requirement.specifier != fixture.specifier || requirement.marker != fixture.marker ||
			requirement.url != fixture.url || !slices.Equal(requirement.extras, fixture.extras) || requirement.pinned() != fixture.pinned {
			t.Fatalf("%q = %+v (pinned %v)", fixture.raw, requirement, requirement.pinned())
		}
	}
	if _, ok := parsePythonRequirement("./local/package"); ok {
		t.Fatal("a path is not a PEP 508 name")
	}
}

// A requirement file as pip reads it: continuations joined, comments dropped
// only where pip drops them, options kept apart, includes resolved against
// the including file's own directory.
func TestPythonRequirementFilesFollowPip(t *testing.T) {
	t.Parallel()
	parsed := parsePythonRequirementsFile("requirements/production.txt", []byte(strings.Join([]string{
		"-r base.txt",
		"--constraint=constraints.txt",
		"-r ../../outside.txt",
		"--extra-index-url https://${ART_USER}:${ART_TOKEN}@art.example.com/simple",
		"gunicorn==23.0.0 \\",
		"    --hash=sha256:abc",
		"git+https://github.com/org/lib.git@v1#egg=org-lib  # pinned by tag",
		"-e ./vendor/tool",
		"https://example.com/wheels/pkg_name-1.0-py3-none-any.whl",
		"# a comment",
	}, "\n")))
	if !slices.Equal(parsed.includes, []string{"requirements/base.txt"}) || !slices.Equal(parsed.constraints, []string{"requirements/constraints.txt"}) ||
		!slices.Equal(parsed.escapes, []string{"../../outside.txt"}) || !slices.Equal(parsed.variables, []string{"ART_USER", "ART_TOKEN"}) {
		t.Fatalf("options = %+v", parsed)
	}
	names := []string{}
	for _, requirement := range parsed.requirements {
		names = append(names, requirement.name)
	}
	if !slices.Equal(names, []string{"gunicorn", "org-lib", "", "pkg-name"}) || !parsed.requirements[0].pinned() {
		t.Fatalf("requirements = %+v", parsed.requirements)
	}
	if len(parsed.indexes) != 1 || parsed.indexes[0].option != "--extra-index-url" {
		t.Fatalf("indexes = %+v", parsed.indexes)
	}
}

// PowerShell's `pip freeze > requirements.txt` writes UTF-16 with a byte-order
// mark; pip installs it, so detection reads it too.
func TestPythonRequirementFilesDecodeUTF16(t *testing.T) {
	t.Parallel()
	units := utf16.Encode([]rune("Flask==3.1.0\r\ngunicorn==23.0.0\r\n"))
	var content bytes.Buffer
	content.Write([]byte{0xFF, 0xFE})
	for _, unit := range units {
		content.Write([]byte{byte(unit), byte(unit >> 8)})
	}
	deps := readPythonDependencies(map[string][]byte{"requirements.txt": content.Bytes()})
	if !deps.declares("flask") || !deps.declares("gunicorn") || deps.unpinned {
		t.Fatalf("deps = %+v", deps)
	}
}

func TestPythonInstallRequirementsPreferProductionLayouts(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		files []string
		want  string
	}{
		{files: []string{"requirements.txt", "requirements/production.txt"}, want: "requirements.txt"},
		{files: []string{"requirements/base.txt", "requirements/local.txt", "requirements/production.txt"}, want: "requirements/production.txt"},
		{files: []string{"requirements-dev.txt", "requirements-prod.txt"}, want: "requirements-prod.txt"},
		{files: []string{"requirements/base.txt", "requirements/test.txt"}, want: "requirements/base.txt"},
		{files: []string{"requirements/app.txt", "requirements/dev.txt"}, want: "requirements/app.txt"},
		{files: []string{"requirements-dev.txt"}, want: ""},
		{files: []string{"requirements/web.txt", "requirements/worker.txt"}, want: ""},
	} {
		files := map[string][]byte{}
		for _, name := range fixture.files {
			files[name] = []byte("flask\n")
		}
		if got := pythonInstallRequirements(files); got != fixture.want {
			t.Fatalf("%v chose %q, want %q", fixture.files, got, fixture.want)
		}
	}
	// A root file that only includes another reads through to it.
	deps := readPythonDependencies(map[string][]byte{
		"requirements.txt":      []byte("-r requirements/prod.txt\n"),
		"requirements/prod.txt": []byte("-r base.txt\ngunicorn==23.0.0\n"),
		"requirements/base.txt": []byte("Django==5.1.4\n"),
	})
	if !deps.declares("django") || !deps.declares("gunicorn") || deps.unpinned || deps.source != "requirements.txt" {
		t.Fatalf("closure = %+v", deps)
	}
}

func TestPyprojectIsReadAsData(t *testing.T) {
	t.Parallel()
	facts := readPyproject([]byte(`[project]
name = "svc"
requires-python = '>=3.11'
# uv add writes the list alphabetically: extras come first.
dependencies = ["celery[redis]>=5.4", "django>=5.1", 'gunicorn', "psycopg[binary]"]

[project.optional-dependencies]
docs = ["mkdocs"]

[project.scripts]
serve = "svc.cli:main"

[dependency-groups]
dev = ["pytest>=8"]

[tool.uv.sources]
shared = { workspace = true }

[[tool.uv.index]]
name = "internal"
url = "https://pypi.internal.example/simple"

[tool.setuptools.packages.find]
where = ["src"]

[tool.pdm.scripts]
start = {cmd = "gunicorn svc.wsgi"}
lint = "ruff check"
release = {composite = ["build", "publish"]}

[tool.poe.tasks]
serve = "uvicorn svc.app:app"

[build-system]
requires = ["setuptools"]
`))
	names := []string{}
	for _, requirement := range facts.dependencies {
		names = append(names, requirement.name)
	}
	if !slices.Equal(names, []string{"celery", "django", "gunicorn", "psycopg"}) || !facts.dependencies[3].hasExtra("binary") {
		t.Fatalf("dependencies = %+v", facts.dependencies)
	}
	if facts.requiresPython != ">=3.11" || len(facts.optional["docs"]) != 1 || len(facts.groups["dev"]) != 1 ||
		facts.uvSources["shared"] != "workspace" || !facts.setuptoolsSrc || !facts.buildSystem || facts.scripts["serve"] != "svc.cli:main" {
		t.Fatalf("facts = %+v", facts)
	}
	if len(facts.indexes) != 1 || facts.indexes[0].name != "internal" || facts.indexes[0].option != "uv" {
		t.Fatalf("indexes = %+v", facts.indexes)
	}
	if len(facts.tasks) != 2 || facts.tasks[0].runner != "pdm" || facts.tasks[0].name != "start" || facts.tasks[0].command != "gunicorn svc.wsgi" ||
		facts.tasks[1].runner != "poe" || facts.tasks[1].command != "uvicorn svc.app:app" {
		t.Fatalf("tasks = %+v", facts.tasks)
	}
	poetry := readPyproject([]byte(`[tool.poetry]
name = "site"
packages = [{ include = "site", from = "src" }]

[tool.poetry.dependencies]
python = "^3.11"
"zope.interface" = "^6"
fastapi = {extras = ["standard"], version = "^0.115"}
lib = { git = "https://github.com/org/lib.git" }

[tool.poetry.group.dev.dependencies]
pytest = "^8"

[[tool.poetry.source]]
name = "private-repo"
url = "https://pypi.example.com/simple"
priority = "supplemental"
`))
	names = names[:0]
	for _, requirement := range poetry.poetryMain {
		names = append(names, requirement.name)
	}
	if !poetry.poetry || poetry.poetryPython != "^3.11" || !poetry.poetryFromSrc || !slices.Equal(names, []string{"zope-interface", "fastapi", "lib"}) ||
		!poetry.poetryMain[1].hasExtra("standard") || poetry.poetryMain[2].url != "git" || !slices.Equal(poetry.poetryGroups["dev"], []string{"pytest"}) ||
		len(poetry.indexes) != 1 || poetry.indexes[0].name != "private-repo" {
		t.Fatalf("poetry = %+v", poetry)
	}
}

func TestPipfileSetupAndCondaManifests(t *testing.T) {
	t.Parallel()
	pipfile := readPipfile([]byte("[[source]]\nurl = \"https://pypi.org/simple\"\nname = \"pypi\"\n\n[[source]]\nurl = \"https://${PRIVATE_USER}:${PRIVATE_PASS}@pypi.example.com/simple\"\nname = \"private\"\n\n" +
		"[packages]\nflask = \"==3.1.0\"\nrequests = {version = \"*\", extras = [\"socks\"]}\nlib = {git = \"https://github.com/org/lib.git\"}\n\n[dev-packages]\npytest = \"*\"\n\n[requires]\npython_version = \"3.11\"\n"))
	if pipfile.python != "3.11" || len(pipfile.packages) != 3 || pipfile.packages[0].specifier != "==3.1.0" || !pipfile.packages[1].hasExtra("socks") ||
		pipfile.packages[2].url != "git" || !slices.Equal(pipfile.dev, []string{"pytest"}) || len(pipfile.indexes) != 1 {
		t.Fatalf("Pipfile = %+v", pipfile)
	}
	lock := readPipfileLock([]byte(`{"_meta": {"requires": {"python_version": "3.11"}}, "default": {"Flask": {"version": "==3.1.0"}, "lib": {"git": "https://github.com/org/lib.git"}}, "develop": {"pytest": {}}}`))
	if !lock.readable || lock.packages["flask"] != "==3.1.0" || !slices.Equal(lock.git, []string{"lib"}) || lock.python != "3.11" {
		t.Fatalf("Pipfile.lock = %+v", lock)
	}
	requirements, python, ok := setupRequirements(map[string][]byte{"setup.py": []byte("from setuptools import setup\nsetup(\n    name='x',\n    python_requires='>=3.9',\n    install_requires=['flask[async]>=2', \"gunicorn\"],\n)\n")})
	if !ok || len(requirements) != 2 || requirements[0].name != "flask" || python != ">=3.9" {
		t.Fatalf("setup.py = %+v %q %v", requirements, python, ok)
	}
	if _, _, ok := setupRequirements(map[string][]byte{"setup.py": []byte("setup(install_requires=read_requirements())\n")}); ok {
		t.Fatal("a computed list is not read")
	}
	requirements, _, ok = setupRequirements(map[string][]byte{"setup.cfg": []byte("[metadata]\nname = x\n\n[options]\npython_requires = >=3.10\ninstall_requires =\n    flask>=2\n    gunicorn\n\n[flake8]\nmax-line-length = 100\n")})
	if !ok || len(requirements) != 2 || requirements[1].name != "gunicorn" {
		t.Fatalf("setup.cfg = %+v", requirements)
	}
	environment, ok := readCondaEnvironment(map[string][]byte{"environment.yml": []byte("name: app\nchannels: [conda-forge]\ndependencies:\n  - python=3.11\n  - pip\n  - conda-forge::pandas=2.1.4=py311h1234\n  - streamlit\n  - pip:\n    - plotly==5.24.1\n")})
	if !ok || environment.python != "=3.11" || len(environment.conda) != 3 || environment.conda[1].name != "pandas" || environment.conda[1].version != "=2.1.4" ||
		len(environment.pip) != 1 || environment.pip[0].name != "plotly" {
		t.Fatalf("conda = %+v", environment)
	}
	project := readPythonProject(map[string][]byte{"environment.yml": []byte("dependencies:\n  - python=3.11\n  - pip\n  - pandas=2.1\n  - streamlit\n  - cudatoolkit=11.8\n  - pip:\n    - plotly==5.24.1\n")})
	lines, unknown := project.condaRequirements()
	if !slices.Equal(lines, []string{"pandas==2.1.*", "streamlit", "plotly==5.24.1"}) || !slices.Equal(unknown, []string{"cudatoolkit"}) {
		t.Fatalf("conda requirements = %q, unknown %q", lines, unknown)
	}
}

// A setup file or a conda environment makes its directory a Python project
// only when it installs something that serves; a vendored library's
// setup.py and a docs environment do not.
func TestPythonManifestsBeyondTheMarkersNeedSomethingToServe(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		files map[string][]byte
		want  bool
	}{
		{map[string][]byte{"setup.py": []byte("setup(install_requires=['flask'])")}, true},
		{map[string][]byte{"setup.py": []byte("setup(install_requires=['six', 'attrs'])")}, false},
		{map[string][]byte{"setup.cfg": []byte("[flake8]\nmax-line-length = 100\n")}, false},
		{map[string][]byte{"environment.yml": []byte("dependencies:\n  - sphinx\n")}, false},
		{map[string][]byte{"environment.yml": []byte("dependencies:\n  - pip:\n    - streamlit\n")}, true},
		{map[string][]byte{"requirements-dev.txt": []byte("pytest\n")}, false},
		{map[string][]byte{"requirements/base.txt": []byte("flask\n")}, true},
		{map[string][]byte{"Pipfile": []byte("[packages]\nflask = \"*\"\n")}, true},
	} {
		if got := readPythonProject(fixture.files).hasManifest(); got != fixture.want {
			t.Fatalf("%v: hasManifest = %v", fixture.files, got)
		}
	}
}

const uvLockFixture = `version = 1
requires-python = ">=3.12"

[[package]]
name = "fastapi"
version = "0.115.6"
source = { registry = "https://pypi.org/simple" }
dependencies = [
    { name = "starlette" },
]
sdist = { url = "https://files.pythonhosted.org/fastapi-0.115.6.tar.gz", hash = "sha256:x", size = 1 }
wheels = [
    { url = "https://files.pythonhosted.org/fastapi-0.115.6-py3-none-any.whl", hash = "sha256:x", size = 1 },
]

[[package]]
name = "numpy"
version = "1.26.4"
source = { registry = "https://pypi.org/simple" }
wheels = [
    { url = "https://files.pythonhosted.org/numpy-1.26.4-cp312-cp312-manylinux_2_17_x86_64.manylinux2014_x86_64.whl", hash = "sha256:x", size = 1 },
    { url = "https://files.pythonhosted.org/numpy-1.26.4-cp312-cp312-manylinux_2_17_aarch64.manylinux2014_aarch64.whl", hash = "sha256:x", size = 1 },
    { url = "https://files.pythonhosted.org/numpy-1.26.4-cp312-cp312-win_amd64.whl", hash = "sha256:x", size = 1 },
]

[[package]]
name = "pytest"
version = "8.3.4"
source = { registry = "https://pypi.org/simple" }

[[package]]
name = "shared"
version = "0.1.0"
source = { git = "https://github.com/org/shared.git?rev=v1#abc" }

[[package]]
name = "site"
version = "0.1.0"
source = { virtual = "." }
dependencies = [
    { name = "fastapi" },
    { name = "numpy" },
    { name = "shared" },
]

[package.dev-dependencies]
dev = [
    { name = "pytest" },
]

[package.metadata]
requires-dist = [
    { name = "fastapi", specifier = ">=0.115" },
    { name = "numpy", specifier = "==1.26.4" },
    { name = "shared", git = "https://github.com/org/shared.git?rev=v1" },
    { name = "redis", marker = "extra == 'cache'", specifier = ">=5" },
]

[package.metadata.requires-dev]
dev = [{ name = "pytest", specifier = ">=8" }]

[[package]]
name = "starlette"
version = "0.41.3"
source = { registry = "https://pypi.org/simple" }
`

func TestPythonLocksAreReadAsData(t *testing.T) {
	t.Parallel()
	lock := readPythonLock("uv.lock", []byte(uvLockFixture))
	if lock.root == nil || lock.root.name != "site" || lock.requiresPython != ">=3.12" || len(lock.root.requiresDist) != 4 ||
		!slices.Equal(lock.root.requiresDev["dev"], []string{"pytest"}) || lock.find("shared").source != "git" {
		t.Fatalf("uv.lock = %+v", lock)
	}
	main := lock.mainPackages()
	if !main["fastapi"] || !main["starlette"] || !main["numpy"] || main["pytest"] {
		t.Fatalf("main packages = %v", main)
	}
	support := pythonWheelSupport(lock.find("numpy").wheels, "amd64")
	if !support.binary || !support.supports(12) || support.supports(13) {
		t.Fatalf("numpy wheels = %+v", support)
	}
	poetry := readPythonLock("poetry.lock", []byte(`[[package]]
name = "flask"
version = "3.1.0"
optional = false
groups = ["main"]
files = [
    {file = "flask-3.1.0-py3-none-any.whl", hash = "sha256:x"},
]

[package.dependencies]
Werkzeug = ">=3.1"

[[package]]
name = "pytest"
version = "8.3.4"
groups = ["dev"]

[[package]]
name = "werkzeug"
version = "3.1.3"
category = "main"
`))
	if main := poetry.mainPackages(); !main["flask"] || !main["werkzeug"] || main["pytest"] || !slices.Equal(poetry.find("flask").dependencies, []string{"werkzeug"}) {
		t.Fatalf("poetry.lock main = %v", main)
	}
	compact, ok := compactPythonLock(strings.NewReader(uvLockFixture), 1<<20)
	if !ok || strings.Contains(string(compact), "win_amd64") || strings.Contains(string(compact), "hash =") {
		t.Fatalf("compact lock keeps other platforms or hashes:\n%s", compact)
	}
	reread := readPythonLock("uv.lock", compact)
	if !reflect.DeepEqual(reread.mainPackages(), lock.mainPackages()) || len(reread.find("numpy").wheels) != 2 || reread.root == nil {
		t.Fatalf("compacted lock reads differently: %+v", reread.find("numpy"))
	}
}

func TestPythonMarkersAreEvaluatedForALinuxBuild(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		marker string
		minor  int
		arch   string
		want   bool
	}{
		{`python_full_version < '3.12'`, 11, "amd64", true},
		{`python_full_version < '3.12'`, 12, "amd64", false},
		{`python_full_version >= '3.13'`, 13, "amd64", true},
		{`python_full_version >= '3.12.4'`, 12, "amd64", true},
		{`python_version < "3.11"`, 10, "amd64", true},
		{`python_version < "3.11"`, 14, "amd64", false},
		{`"3.11" > python_version`, 10, "amd64", true},
		{`python_version == "3.12.*"`, 12, "amd64", true},
		{`python_version ~= "3.11"`, 13, "amd64", true},
		{`python_version in '3.10 3.11'`, 11, "amd64", true},
		{`python_version not in '3.10 3.11'`, 11, "amd64", false},
		{`sys_platform == 'win32'`, 13, "amd64", false},
		{`sys_platform != 'darwin' and platform_machine == 'x86_64'`, 13, "amd64", true},
		{`sys_platform != 'darwin' and platform_machine == 'x86_64'`, 13, "arm64", false},
		{`platform_system == "Windows" or (python_version >= "3.12" and os_name == "posix")`, 11, "amd64", false},
		{`platform_system == "Windows" or (python_version >= "3.12" and os_name == "posix")`, 12, "amd64", true},
		{`implementation_name == 'pypy'`, 13, "amd64", false},
		// What the build does not settle holds.
		{`extra == 'cuda'`, 13, "amd64", true},
		{`platform_machine == 'x86_64'`, 13, "riscv64", true},
		{`python_version < '3.11' or`, 13, "amd64", true},
		{`sys_platform = 'linux'`, 13, "amd64", true},
		{`(python_version < '3.11'`, 13, "amd64", true},
	} {
		if got := pythonMarkerAllows(fixture.marker, fixture.minor, fixture.arch); got != fixture.want {
			t.Errorf("%q on 3.%d/%s = %v", fixture.marker, fixture.minor, fixture.arch, got)
		}
	}
}

// A lock resolved for several Pythons pins some packages once per range and
// gates others behind markers; each family is judged by what it installs.
func TestPythonLockWheelsAreJudgedPerFamily(t *testing.T) {
	t.Parallel()
	lock := `version = 1
requires-python = ">=3.10"
resolution-markers = [
    "python_full_version >= '3.12'",
    "python_full_version < '3.12'",
]

[[package]]
name = "app"
version = "0.1.0"
source = { virtual = "." }
dependencies = [
    { name = "numpy", version = "2.2.6", source = { registry = "https://pypi.org/simple" }, marker = "python_full_version < '3.12'" },
    { name = "numpy", version = "2.4.6", source = { registry = "https://pypi.org/simple" }, marker = "python_full_version >= '3.12'" },
    { name = "pydub" },
    { name = "tool", extra = ["gpu"] },
    { name = "winonly", marker = "sys_platform == 'win32'" },
]

[[package]]
name = "audioop-lts"
version = "0.2.2"
source = { registry = "https://pypi.org/simple" }
wheels = [
    { url = "https://files.pythonhosted.org/audioop_lts-0.2.2-cp313-abi3-manylinux_2_17_x86_64.whl" },
]

[[package]]
name = "numpy"
version = "2.2.6"
source = { registry = "https://pypi.org/simple" }
resolution-markers = [
    "python_full_version < '3.12'",
]
wheels = [
    { url = "https://files.pythonhosted.org/numpy-2.2.6-cp310-cp310-manylinux_2_17_x86_64.whl" },
    { url = "https://files.pythonhosted.org/numpy-2.2.6-cp311-cp311-manylinux_2_17_x86_64.whl" },
]

[[package]]
name = "numpy"
version = "2.4.6"
source = { registry = "https://pypi.org/simple" }
resolution-markers = [
    "python_full_version >= '3.12'",
]
wheels = [
    { url = "https://files.pythonhosted.org/numpy-2.4.6-cp312-cp312-manylinux_2_17_x86_64.whl" },
    { url = "https://files.pythonhosted.org/numpy-2.4.6-cp313-cp313-manylinux_2_17_x86_64.whl" },
]

[[package]]
name = "nvidia-cublas"
version = "13.1.1.3"
source = { registry = "https://pypi.org/simple" }
wheels = [
    { url = "https://files.pythonhosted.org/nvidia_cublas-13.1.1.3-py3-none-manylinux_2_27_x86_64.whl" },
    { url = "https://files.pythonhosted.org/nvidia_cublas-13.1.1.3-py3-none-manylinux_2_27_aarch64.whl" },
]

[[package]]
name = "pydub"
version = "0.25.1"
source = { registry = "https://pypi.org/simple" }
dependencies = [
    { name = "audioop-lts", marker = "python_full_version >= '3.13'" },
]
wheels = [
    { url = "https://files.pythonhosted.org/pydub-0.25.1-py2.py3-none-any.whl" },
]

[[package]]
name = "tool"
version = "1.0"
source = { registry = "https://pypi.org/simple" }
wheels = [
    { url = "https://files.pythonhosted.org/tool-1.0-py3-none-any.whl" },
]

[package.optional-dependencies]
gpu = [
    { name = "nvidia-cublas", marker = "sys_platform == 'linux'" },
]

[[package]]
name = "winonly"
version = "1.0"
source = { registry = "https://pypi.org/simple" }
wheels = [
    { url = "https://files.pythonhosted.org/winonly-1.0-cp312-cp312-win_amd64.whl" },
]
`
	parsed := readPythonLock("uv.lock", []byte(lock))
	main := parsed.mainPackages()
	if !main["numpy"] || !main["nvidia-cublas"] || !main["audioop-lts"] || !main["winonly"] {
		t.Fatalf("main packages = %v", main)
	}
	names := func(minor int) []string {
		var result []string
		for _, pkg := range parsed.installedPackages(minor, "amd64") {
			result = append(result, pkg.name+"=="+pkg.version)
		}
		slices.Sort(result)
		return result
	}
	if got := names(11); !slices.Equal(got, []string{"numpy==2.2.6", "nvidia-cublas==13.1.1.3", "pydub==0.25.1", "tool==1.0"}) {
		t.Fatalf("3.11 installs %v", got)
	}
	if got := names(13); !slices.Equal(got, []string{"audioop-lts==0.2.2", "numpy==2.4.6", "nvidia-cublas==13.1.1.3", "pydub==0.25.1", "tool==1.0"}) {
		t.Fatalf("3.13 installs %v", got)
	}
	project := readPythonProject(map[string][]byte{
		"pyproject.toml": []byte("[project]\nname = \"app\"\ndependencies = [\"numpy\", \"pydub\", \"tool[gpu]\", \"winonly; sys_platform == 'win32'\"]\n"),
		"uv.lock":        []byte(lock),
	})
	// audioop-lts' abi3 wheel serves 3.14, and the lock installs it wherever
	// pydub needs it.
	blockers := project.versionBlockers("amd64")
	want := map[int][]string{14: {"numpy==2.4.6"}}
	for _, minor := range pythonCatalogueMinors {
		if !slices.Equal(blockers[minor], want[minor]) {
			t.Fatalf("blockers = %v", blockers)
		}
	}
	for _, fixture := range []struct {
		wheel    string
		minor    int
		supports bool
	}{
		{"nvidia_cublas-13.1.1.3-py3-none-manylinux_2_27_x86_64.whl", 14, true},
		{"ruff-0.8.0-py3-none-manylinux_2_17_x86_64.manylinux2014_x86_64.whl", 10, true},
		{"x-1-py312-none-manylinux_2_17_x86_64.whl", 13, true},
		{"x-1-py312-none-manylinux_2_17_x86_64.whl", 11, false},
		{"x-1-cp312-none-manylinux_2_17_x86_64.whl", 12, true},
		{"x-1-cp312-none-manylinux_2_17_x86_64.whl", 13, false},
		{"x-1-cp311-abi3-manylinux_2_17_x86_64.whl", 14, true},
		{"x-1-cp313-cp313t-manylinux_2_17_x86_64.whl", 13, false},
		{"x-1-cp313-cp313-manylinux_2_17_aarch64.whl", 13, false},
	} {
		if got := pythonWheelSupport([]string{fixture.wheel}, "amd64").supports(fixture.minor); got != fixture.supports {
			t.Errorf("%s on 3.%d = %v", fixture.wheel, fixture.minor, got)
		}
	}
}

// Poetry and PDM record each package's own marker.
func TestPoetryAndPDMPackageMarkersLimitTheFamiliesTheyInstallOn(t *testing.T) {
	t.Parallel()
	for file, content := range map[string]string{
		"poetry.lock": `[[package]]
name = "tomli"
version = "2.0.1"
groups = ["main"]
markers = "python_version < \"3.11\""

[[package]]
name = "flask"
version = "3.1.0"
groups = ["main"]

[package.dependencies]
tomli = {version = ">=1.1.0", markers = "python_version < \"3.11\""}
`,
		"pdm.lock": `[[package]]
name = "tomli"
version = "2.0.1"
groups = ["default"]
marker = "python_version < \"3.11\""

[[package]]
name = "flask"
version = "3.1.0"
groups = ["default"]
dependencies = [
    "tomli>=1.1.0; python_version < \"3.11\"",
]
`,
	} {
		lock := readPythonLock(file, []byte(content))
		if len(lock.installedPackages(10, "amd64")) != 2 || len(lock.installedPackages(13, "amd64")) != 1 || !lock.mainPackages()["tomli"] {
			t.Fatalf("%s: %+v", file, lock.packages[0])
		}
		if edge := lock.find("flask").edges[0]; edge.name != "tomli" || edge.marker != `python_version < "3.11"` {
			t.Fatalf("%s edge = %+v", file, edge)
		}
	}
}

// Requirement lines pinned per Python by markers are judged only on the
// Pythons their markers select.
func TestPythonRequirementMarkersLimitTheirBlockers(t *testing.T) {
	t.Parallel()
	project := readPythonProject(map[string][]byte{"requirements.txt": []byte("numpy==1.24.4; python_version < \"3.12\"\nnumpy==2.1.3; python_version >= \"3.12\"\n")})
	blockers := project.versionBlockers("amd64")
	if len(blockers[10]) != 0 || len(blockers[11]) != 0 || len(blockers[12]) != 0 || len(blockers[13]) != 0 || !slices.Equal(blockers[14], []string{`numpy==2.1.3; python_version >= "3.12"`}) {
		t.Fatalf("blockers = %v", blockers)
	}
}

// A lock is compared with the manifest it was generated from, the Python
// shape of the npm incident: uv ships a package the pyproject no longer
// matches, Poetry refuses, and Pipfile.lock misses what the Pipfile adds.
func TestPythonLocksAreComparedWithTheirManifests(t *testing.T) {
	t.Parallel()
	inSync := readPythonProject(map[string][]byte{
		"pyproject.toml": []byte("[project]\nname = \"site\"\ndependencies = [\"fastapi>=0.115\", \"numpy==1.26.4\", \"shared @ git+https://github.com/org/shared.git@v1\"]\n[dependency-groups]\ndev = [\"pytest>=8\"]\n"),
		"uv.lock":        []byte(uvLockFixture),
	})
	if drift := inSync.lockDrift(); drift == nil || drift.State != LockfileInSync {
		t.Fatalf("in sync = %+v", drift)
	}
	stale := readPythonProject(map[string][]byte{
		"pyproject.toml": []byte("[project]\nname = \"site\"\ndependencies = [\"fastapi>=0.116\", \"numpy==1.26.4\", \"shared\", \"httpx\"]\n"),
		"uv.lock":        []byte(uvLockFixture),
	})
	drift := stale.lockDrift()
	if drift.State != LockfileStale || !slices.Equal(drift.Missing, []string{"httpx"}) || !slices.Equal(drift.Changed, []string{"fastapi"}) ||
		!strings.Contains(drift.Note, "uv.lock is older than pyproject.toml") {
		t.Fatalf("stale uv = %+v", drift)
	}
	poetry := readPythonProject(map[string][]byte{
		"pyproject.toml": []byte("[tool.poetry.dependencies]\npython = \"^3.11\"\nflask = \"^3\"\nhttpx = \"^0.27\"\n"),
		"poetry.lock":    []byte("[[package]]\nname = \"flask\"\nversion = \"3.1.0\"\n"),
	})
	if drift := poetry.lockDrift(); drift.State != LockfileStale || !slices.Equal(drift.Missing, []string{"httpx"}) {
		t.Fatalf("stale poetry = %+v", drift)
	}
	pipenv := readPythonProject(map[string][]byte{
		"Pipfile":      []byte("[packages]\nflask = \"*\"\nrequests = \"*\"\n"),
		"Pipfile.lock": []byte(`{"default": {"flask": {"version": "==3.1.0"}}}`),
	})
	if drift := pipenv.lockDrift(); drift.State != LockfileStale || !slices.Equal(drift.Missing, []string{"requests"}) || !strings.Contains(drift.Note, "older than Pipfile") {
		t.Fatalf("stale Pipfile.lock = %+v", drift)
	}
	conflict := readPythonProject(map[string][]byte{
		"pyproject.toml":   []byte("[project]\ndependencies = [\"fastapi\", \"sqlalchemy\", \"httpx\"]\n"),
		"requirements.txt": []byte("fastapi==0.110.0\n"),
	})
	if missing := conflict.manifestConflict(); !slices.Equal(missing, []string{"sqlalchemy", "httpx"}) {
		t.Fatalf("manifest conflict = %v", missing)
	}
}

// Framework matching reads what the project declares, not everything its
// lock installs: Gradio locks FastAPI, and uvicorn in a dev group is not in
// the production image.
func TestPythonDirectDependenciesAreKeptApartFromTheLock(t *testing.T) {
	t.Parallel()
	deps := readPythonDependencies(map[string][]byte{
		"pyproject.toml": []byte("[project]\ndependencies = [\"gradio>=5\"]\n[dependency-groups]\ndev = [\"uvicorn\"]\n"),
		"uv.lock": []byte("[[package]]\nname = \"app\"\nversion = \"0\"\nsource = { virtual = \".\" }\ndependencies = [{ name = \"gradio\" }]\n\n[package.dev-dependencies]\ndev = [{ name = \"uvicorn\" }]\n\n" +
			"[[package]]\nname = \"gradio\"\nversion = \"5\"\ndependencies = [{ name = \"fastapi\" }]\n\n[[package]]\nname = \"fastapi\"\nversion = \"0\"\n\n[[package]]\nname = \"uvicorn\"\nversion = \"0\"\n"),
	})
	if !deps.declares("gradio") || deps.declares("fastapi") || !deps.has("fastapi") || deps.has("uvicorn") {
		t.Fatalf("deps = %+v", deps)
	}
	if framework := matchPythonFramework(deps); framework == nil || framework.Name != "gradio" {
		t.Fatalf("framework = %+v", framework)
	}
}
