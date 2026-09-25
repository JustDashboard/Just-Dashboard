package deploy

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// The interpreter a project runs on: 3.14 in the catalogue, the default kept
// at 3.13 and lowered for a pin with no wheels there, a declared 3.8 or 3.9
// raised to 3.10 when its pins allow, and every version file read.
func TestPythonVersionResolutionReadsEveryDeclaration(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name     string
		files    pythonVersionFiles
		manifest map[string][]byte
		want     string
		source   string
		raised   string
		limited  bool
		fails    string
	}{
		{name: "default", manifest: map[string][]byte{"requirements.txt": []byte("flask\n")}, want: "3.13", source: "the maintained default"},
		{name: "a pin without 3.13 wheels lowers the default", manifest: map[string][]byte{"requirements.txt": []byte("numpy==1.26.4\npandas==2.1.4\n")}, want: "3.12", limited: true},
		{name: "a range that stops before 3.13 wheels lowers it too", manifest: map[string][]byte{"requirements.txt": []byte("numpy<2\n")}, want: "3.12", limited: true},
		{name: "python-telegram-bot 13 imports imghdr", manifest: map[string][]byte{"requirements.txt": []byte("python-telegram-bot==13.15\n")}, want: "3.12", limited: true},
		{name: "pydub with audioop-lts runs on 3.13", manifest: map[string][]byte{"requirements.txt": []byte("pydub==0.25.1\naudioop-lts==0.2.1\n")}, want: "3.13"},
		{name: "django 3.2 imports cgi", manifest: map[string][]byte{"requirements.txt": []byte("Django>=3.2,<4\n")}, want: "3.12", limited: true},
		{name: "pydantic 1 is pure Python", manifest: map[string][]byte{"requirements.txt": []byte("pydantic==1.10.13\n")}, want: "3.13"},
		{name: "pydantic 2.5 pins a core without 3.13 wheels", manifest: map[string][]byte{"requirements.txt": []byte("pydantic==2.5.3\n")}, want: "3.12", limited: true},
		{name: "a 3.14 floor selects 3.14", manifest: map[string][]byte{"pyproject.toml": []byte("[project]\nrequires-python = \">=3.14\"\ndependencies = []\n")}, want: "3.14"},
		{name: "a floor the catalogue cannot meet is refused by name", manifest: map[string][]byte{"pyproject.toml": []byte("[project]\nrequires-python = \">=3.15\"\n")}, fails: "requires-python >=3.15"},
		{name: ".python-version 3.14", files: pythonVersionFiles{pythonVersion: "3.14\n"}, manifest: map[string][]byte{"requirements.txt": []byte("flask\n")}, want: "3.14"},
		{name: "an ancestor's .python-version", files: pythonVersionFiles{pythonVersion: "3.12.4\n", pythonVersionPath: ".python-version"}, manifest: map[string][]byte{"requirements.txt": []byte("flask\n")}, want: "3.12", source: ".python-version 3.12.4"},
		{name: ".tool-versions", files: pythonVersionFiles{toolVersions: "nodejs 22.1.0\npython 3.11.9 3.10.4\n"}, manifest: map[string][]byte{"requirements.txt": []byte("flask\n")}, want: "3.11"},
		{name: "mise.toml", files: pythonVersionFiles{mise: "[tools]\npython = \"3.12\"\n"}, manifest: map[string][]byte{"requirements.txt": []byte("flask\n")}, want: "3.12"},
		{name: "Pipfile python_version", manifest: map[string][]byte{"Pipfile": []byte("[packages]\nflask = \"*\"\n[requires]\npython_version = \"3.11\"\n")}, want: "3.11"},
		{name: "environment.yml python", manifest: map[string][]byte{"environment.yml": []byte("dependencies:\n  - python=3.11\n  - pip:\n    - flask\n")}, want: "3.11"},
		{name: "uv.lock requires-python", manifest: map[string][]byte{"uv.lock": []byte("version = 1\nrequires-python = \">=3.11, <3.13\"\n")}, want: "3.12"},
		{name: "runtime.txt 3.9 is raised", files: pythonVersionFiles{runtimeTxt: "python-3.9.19\n"}, manifest: map[string][]byte{"requirements.txt": []byte("flask==3.0.0\n")}, want: "3.10", raised: "3.9"},
		{name: "3.9 with a pin that has no 3.10 wheels is refused", files: pythonVersionFiles{runtimeTxt: "python-3.9.19\n"}, manifest: map[string][]byte{"requirements.txt": []byte("numpy==1.19.5\n")}, fails: "numpy==1.19.5 cannot install on Python 3.10"},
		{name: "3.9 against a range that excludes 3.10 is refused", files: pythonVersionFiles{pythonVersion: "3.9\n"}, manifest: map[string][]byte{"pyproject.toml": []byte("[project]\nrequires-python = \"<3.10\"\n")}, fails: "pins Python 3.9"},
		{name: "python 2", files: pythonVersionFiles{runtimeTxt: "python-2.7.18\n"}, manifest: map[string][]byte{"requirements.txt": []byte("flask\n")}, fails: "pins Python 2.7"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			project := readPythonProject(fixture.manifest)
			choice, err := resolvePythonVersion(pythonVersionInputsFor("", fixture.files, project, "amd64"))
			if fixture.fails != "" {
				if err == nil || !errors.Is(err, ErrUnsupportedBuilder) || !strings.Contains(err.Error(), fixture.fails) {
					t.Fatalf("err = %v, want %q", err, fixture.fails)
				}
				return
			}
			if err != nil || choice.version != fixture.want || choice.raised != fixture.raised || (len(choice.limited) > 0) != fixture.limited ||
				(fixture.source != "" && choice.source != fixture.source) {
				t.Fatalf("choice = %+v, %v", choice, err)
			}
		})
	}
	// A lock decides from its wheel file names, exactly.
	project := readPythonProject(map[string][]byte{"uv.lock": []byte(uvLockFixture), "pyproject.toml": []byte("[project]\ndependencies = [\"numpy==1.26.4\"]\n")})
	choice, err := resolvePythonVersion(pythonVersionInputsFor("", pythonVersionFiles{}, project, "amd64"))
	if err != nil || choice.version != "3.12" || !slices.Equal(choice.limited, []string{"numpy==1.26.4"}) {
		t.Fatalf("lock choice = %+v, %v", choice, err)
	}
	// An explicit setting is kept, and the pins it cannot install are named.
	choice, err = resolvePythonVersion(pythonVersionInputsFor("3.14", pythonVersionFiles{}, project, "amd64"))
	if err != nil || choice.version != "3.14" || !slices.Equal(choice.blockers, []string{"numpy==1.26.4"}) {
		t.Fatalf("explicit choice = %+v, %v", choice, err)
	}
}

func TestPythonInstallPlansFollowTheManifests(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name   string
		files  map[string][]byte
		choice pythonInstallChoice
		kind   string
		want   []string
		absent []string
		notes  string
		fails  string
	}{
		{name: "uv lock in sync installs locked", files: map[string][]byte{"pyproject.toml": []byte("[project]\nname = \"site\"\ndependencies = [\"fastapi>=0.115\", \"numpy==1.26.4\", \"shared\"]\n"), "uv.lock": []byte(uvLockFixture)},
			kind: "uv.lock", want: []string{"uv==" + pythonUVRelease, "uv sync --locked --no-dev"}},
		{name: "a stale uv lock is resolved again", files: map[string][]byte{"pyproject.toml": []byte("[project]\nname = \"site\"\ndependencies = [\"fastapi>=0.115\", \"numpy==1.26.4\", \"shared\", \"httpx\"]\n"), "uv.lock": []byte(uvLockFixture)},
			kind: "uv.lock", want: []string{"uv sync --no-dev"}, absent: []string{"--locked"}, notes: "missing: httpx"},
		{name: "a uv workspace member installs its package", files: map[string][]byte{"pyproject.toml": []byte("[tool.uv.workspace]\nmembers = [\"packages/*\"]\n"), "uv.lock": []byte("version = 1\n")},
			choice: pythonInstallChoice{member: "api"}, kind: "uv.lock", want: []string{"uv sync --locked --no-dev --package api"}},
		{name: "poetry", files: map[string][]byte{"pyproject.toml": []byte("[tool.poetry.dependencies]\npython = \"^3.11\"\nflask = \"^3\"\n"), "poetry.lock": []byte("[[package]]\nname = \"flask\"\n")},
			kind: "poetry.lock", want: []string{"poetry==" + pythonPoetryRelease, "poetry install --only main --no-root --no-interaction"}},
		{name: "poetry stale locks first", files: map[string][]byte{"pyproject.toml": []byte("[tool.poetry.dependencies]\nflask = \"^3\"\nhttpx = \"*\"\n"), "poetry.lock": []byte("[[package]]\nname = \"flask\"\n")},
			kind: "poetry.lock", want: []string{"poetry lock --no-interaction && poetry install"}, notes: "missing: httpx"},
		{name: "poetry src package installs the project", files: map[string][]byte{"pyproject.toml": []byte("[tool.poetry]\npackages = [{include = \"app\", from = \"src\"}]\n[tool.poetry.dependencies]\nflask = \"^3\"\n"), "poetry.lock": []byte("[[package]]\nname = \"flask\"\n")},
			choice: pythonInstallChoice{installProject: true}, kind: "poetry.lock", want: []string{"poetry install --only main --no-interaction"}, absent: []string{"--no-root"}},
		{name: "pdm exports its lock", files: map[string][]byte{"pyproject.toml": []byte("[project]\ndependencies = [\"litestar\"]\n"), "pdm.lock": []byte("[[package]]\nname = \"litestar\"\ngroups = [\"default\"]\n")},
			kind: "pdm.lock", want: []string{"pdm==" + pythonPDMRelease, "pdm export --prod -o /tmp/jd-requirements.txt", "pip install --no-cache-dir --requirement /tmp/jd-requirements.txt"}},
		{name: "pipenv deploys its lock", files: map[string][]byte{"Pipfile": []byte("[packages]\nflask = \"*\"\n[requires]\npython_version = \"3.12\"\n"), "Pipfile.lock": []byte(`{"default": {"flask": {"version": "==3.1.0"}}}`)},
			choice: pythonInstallChoice{version: "3.12"}, kind: "Pipfile.lock", want: []string{"pipenv==" + pythonPipenvRelease, "pipenv install --system --deploy"}},
		{name: "pipenv on another Python ignores the Pipfile", files: map[string][]byte{"Pipfile": []byte("[packages]\nflask = \"*\"\n[requires]\npython_version = \"3.9\"\n"), "Pipfile.lock": []byte(`{"default": {"flask": {"version": "==3.1.0"}}}`)},
			choice: pythonInstallChoice{version: "3.10"}, kind: "Pipfile.lock", want: []string{"pipenv install --system --ignore-pipfile"}},
		{name: "a Pipfile without a lock", files: map[string][]byte{"Pipfile": []byte("[packages]\nflask = \"*\"\n")},
			kind: "Pipfile", want: []string{"pipenv install --system --skip-lock"}},
		{name: "requirements closure", files: map[string][]byte{"requirements/base.txt": []byte("django\n"), "requirements/production.txt": []byte("-r base.txt\ngunicorn\n")},
			kind: "requirements.txt", want: []string{"pip install --no-cache-dir --requirement requirements/production.txt"}},
		{name: "an include outside the root is refused", files: map[string][]byte{"requirements.txt": []byte("-r ../shared.txt\n")}, fails: "outside the build root"},
		{name: "an include the repository lacks is refused", files: map[string][]byte{"requirements.txt": []byte("-r base.txt\n")}, fails: "includes base.txt"},
		{name: "freeze leftovers are rewritten before pip reads them", files: map[string][]byte{"requirements.txt": []byte("flask==3.1.0\npywin32==306\ncertifi @ file:///croot/certifi/work\n")},
			kind: "requirements.txt", want: []string{"pip install --no-cache-dir --requirement requirements.txt"}, notes: "local environment"},
		{name: "a pyproject that declares more than requirements.txt wins", files: map[string][]byte{"requirements.txt": []byte("fastapi==0.110.0\n"), "pyproject.toml": []byte("[project]\ndependencies = [\"fastapi\", \"httpx\"]\n")},
			kind: "pyproject.toml", want: []string{"tomllib"}, notes: "omits httpx"},
		{name: "a bare pyproject on 3.10 brings tomli", files: map[string][]byte{"pyproject.toml": []byte("[project]\ndependencies = [\"flask\"]\n")},
			choice: pythonInstallChoice{version: "3.10"}, kind: "pyproject.toml", want: []string{"tomli==2.0.1", "import tomli as tomllib"}},
		{name: "a src package is installed", files: map[string][]byte{"pyproject.toml": []byte("[project]\ndependencies = [\"fastapi\"]\n[build-system]\nrequires = [\"hatchling\"]\n")},
			choice: pythonInstallChoice{installProject: true}, kind: "pyproject.toml", want: []string{"pip install --no-cache-dir ."}},
		{name: "setup.py", files: map[string][]byte{"setup.py": []byte("setup(install_requires=['flask'])")}, kind: "setup.py", want: []string{"pip install --no-cache-dir ."}},
		{name: "conda is installed with pip", files: map[string][]byte{"environment.yml": []byte("dependencies:\n  - python=3.11\n  - pandas=2.1\n  - pip:\n    - streamlit==1.41.0\n")},
			kind: "environment.yml", want: []string{"printf '%s\\n' 'pandas==2.1.*' 'streamlit==1.41.0' > /tmp/jd-requirements.txt"}},
		{name: "a conda-only package is refused by name", files: map[string][]byte{"environment.yml": []byte("dependencies:\n  - cudatoolkit=11.8\n  - pip:\n    - streamlit\n")}, fails: "cudatoolkit"},
		{name: "torch comes from the CPU index", files: map[string][]byte{"requirements.txt": []byte("sentence-transformers==3.3.1\n")},
			kind: "requirements.txt", want: []string{`PIP_EXTRA_INDEX_URL="${PIP_EXTRA_INDEX_URL:+$PIP_EXTRA_INDEX_URL }https://download.pytorch.org/whl/cpu" pip install`}},
		{name: "a declared index is left alone", files: map[string][]byte{"requirements.txt": []byte("--extra-index-url https://download.pytorch.org/whl/cu121\ntorch==2.5.1\n")},
			kind: "requirements.txt", absent: []string{"whl/cpu"}},
		{name: "a tool-only pyproject installs nothing", files: map[string][]byte{"pyproject.toml": []byte("[tool.ruff]\nline-length = 100\n")}, kind: "pyproject.toml", want: []string{"true"}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			if fixture.choice.version == "" {
				fixture.choice.version = "3.13"
			}
			install, err := planPythonInstall(readPythonProject(fixture.files), fixture.choice)
			if fixture.fails != "" {
				if err == nil || !strings.Contains(err.Error(), fixture.fails) {
					t.Fatalf("err = %v, want %q", err, fixture.fails)
				}
				return
			}
			if err != nil || install.kind != fixture.kind {
				t.Fatalf("install = %+v, %v", install, err)
			}
			for _, want := range fixture.want {
				if !strings.Contains(install.command, want) {
					t.Fatalf("command %q lacks %q", install.command, want)
				}
			}
			for _, absent := range fixture.absent {
				if strings.Contains(install.command, absent) {
					t.Fatalf("command %q carries %q", install.command, absent)
				}
			}
			if fixture.notes != "" && !strings.Contains(strings.Join(install.notes, " "), fixture.notes) {
				t.Fatalf("notes %q lack %q", install.notes, fixture.notes)
			}
			if !validPythonInstallKind(install.kind) {
				t.Fatalf("install kind %q is not a valid saved kind", install.kind)
			}
		})
	}
}

// Freeze leftovers are rewritten by generic expressions, never by lines of
// the file, so no repository content enters the Dockerfile.
func TestPythonSanitizeExpressionsAreGeneric(t *testing.T) {
	t.Parallel()
	install, err := planPythonInstall(readPythonProject(map[string][]byte{
		"requirements.txt":      []byte("-r requirements/base.txt\npywin32==306\n"),
		"requirements/base.txt": []byte("certifi @ file:///croot/certifi/work\n"),
	}), pythonInstallChoice{version: "3.13"})
	if err != nil {
		t.Fatal(err)
	}
	line := install.sanitizeLine()
	if !strings.HasPrefix(line, "RUN sed -i -E -e '") || !strings.HasSuffix(line, " requirements.txt requirements/base.txt") ||
		strings.Contains(line, "croot") || strings.Contains(line, "306") || !strings.Contains(line, "pywin32|") {
		t.Fatalf("sanitize line = %q", line)
	}
}

func TestPythonRecipeRendersSystemPackagesAssetsAndWorkspaces(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name   string
		files  map[string]string
		root   string
		config BuildPlanConfig
		want   []string
		absent []string
		fails  string
	}{
		{
			name:   "native dependencies bring their Debian packages before the source",
			files:  map[string]string{"requirements.txt": "Django==5.1.4\npsycopg2==2.9.10\nmysqlclient==2.2.6\n", "Aptfile": "libgl1\n"},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "python", StartCommand: "gunicorn app.wsgi", SystemPackages: []string{"libgl1"}},
			want: []string{"FROM python:3.13-slim-trixie@sha256:",
				"RUN apt-get update && apt-get install -y --no-install-recommends default-libmysqlclient-dev gcc libc6-dev libgl1 libpq-dev pkg-config && rm -rf /var/lib/apt/lists/*\nCOPY . ."},
		},
		{
			name:   "psycopg 3 without its binary extra loads libpq",
			files:  map[string]string{"requirements.txt": "psycopg==3.2.3\nfastapi\n"},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "python", StartCommand: "uvicorn main:app"},
			want:   []string{"apt-get install -y --no-install-recommends libpq5 &&"},
		},
		{
			name:   "psycopg with its binary extra needs nothing",
			files:  map[string]string{"requirements.txt": "psycopg[binary]==3.2.3\nfastapi\n"},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "python", StartCommand: "uvicorn main:app"},
			absent: []string{"apt-get"},
		},
		{
			name:   "a git requirement brings git",
			files:  map[string]string{"requirements.txt": "lib @ git+https://github.com/org/lib.git@v1\nflask\n"},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "python", StartCommand: "gunicorn app:app"},
			want:   []string{"apt-get install -y --no-install-recommends git &&"},
		},
		{
			name:   "freeze leftovers are rewritten",
			files:  map[string]string{"requirements.txt": "flask==3.1.0\npywin32==306\n"},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "python", StartCommand: "gunicorn app:app"},
			want:   []string{"COPY . .\nRUN sed -i -E -e", " requirements.txt\nRUN pip install --no-cache-dir --requirement requirements.txt"},
		},
		{
			name:   "uv never downloads its own interpreter",
			files:  map[string]string{"pyproject.toml": "[project]\nname = \"x\"\ndependencies = [\"fastapi\"]\n", "uv.lock": "[[package]]\nname = \"x\"\nversion = \"0\"\nsource = { virtual = \".\" }\ndependencies = [{ name = \"fastapi\" }]\n", ".python-version": "3.13.1\n"},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "python", StartCommand: "uvicorn main:app"},
			want:   []string{"ENV UV_PYTHON_DOWNLOADS=never", "uv sync --locked --no-dev --python /usr/local/bin/python", "RUN uv pip install --no-cache 'uvicorn[standard]==0.35.0'"},
			absent: []string{"ENV UV_PYTHON="},
		},
		{
			name: "a Django project builds its Tailwind assets in a Node stage",
			files: map[string]string{
				"requirements.txt": "django==5.1\ngunicorn\n", "manage.py": "",
				"package.json":      `{"scripts": {"build": "tailwindcss -i in.css -o static/dist/out.css"}, "devDependencies": {"tailwindcss": "3.4.17"}}`,
				"package-lock.json": `{"name": "x", "lockfileVersion": 3, "packages": {"": {"devDependencies": {"tailwindcss": "3.4.17"}}, "node_modules/tailwindcss": {"version": "3.4.17"}}}`,
			},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "python", StartCommand: "gunicorn site.wsgi"},
			want:   []string{" AS assets\nWORKDIR /app\nCOPY . .\n", "RUN npm run build\nRUN rm -rf node_modules\nFROM python:3.13-slim-trixie", "COPY --from=assets /app /app\nRUN pip install"},
			absent: []string{"\nCOPY . .\nRUN pip"},
		},
		{
			name: "django-tailwind's theme package builds in its own directory",
			files: map[string]string{
				"requirements.txt": "django==5.1\n", "manage.py": "",
				"theme/static_src/package.json":      `{"scripts": {"build": "tailwindcss -o ../static/css/dist/styles.css"}, "devDependencies": {"tailwindcss": "3.4.17"}}`,
				"theme/static_src/package-lock.json": `{"name": "x", "lockfileVersion": 3, "packages": {"": {"devDependencies": {"tailwindcss": "3.4.17"}}, "node_modules/tailwindcss": {"version": "3.4.17"}}}`,
			},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "python", StartCommand: "gunicorn site.wsgi"},
			want:   []string{"COPY . .\nWORKDIR /app/theme/static_src\nRUN npm ci", "COPY --from=assets /app /app"},
		},
		{
			name: "a uv workspace member builds from the workspace root",
			files: map[string]string{
				"pyproject.toml": "[tool.uv.workspace]\nmembers = [\"packages/*\"]\n", "uv.lock": "version = 1\n",
				"packages/api/pyproject.toml": "[project]\nname = \"api\"\ndependencies = [\"fastapi\", \"shared\"]\n[tool.uv.sources]\nshared = { workspace = true }\n",
			},
			root:   "packages/api",
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "python", StartCommand: "uvicorn api.main:app"},
			want:   []string{"uv sync --locked --no-dev --package api", "WORKDIR /app/packages/api\n"},
		},
		{
			name: "a workspace member whose directory the Dockerfile cannot name is refused",
			files: map[string]string{
				"pyproject.toml": "[tool.uv.workspace]\nmembers = [\"packages/*\"]\n", "uv.lock": "version = 1\n",
				"packages/my api/pyproject.toml": "[project]\nname = \"api\"\ndependencies = [\"fastapi\"]\n",
			},
			root:   "packages/my api",
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "python", StartCommand: "uvicorn api.main:app"},
			fails:  "build it with a Dockerfile",
		},
		{
			name:   "a workspace source without its workspace is refused",
			files:  map[string]string{"pyproject.toml": "[project]\nname = \"api\"\ndependencies = [\"fastapi\", \"shared\"]\n[tool.uv.sources]\nshared = { workspace = true }\n"},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "python", StartCommand: "uvicorn api.main:app"},
			fails:  "build from the workspace root",
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			boundary := t.TempDir()
			for path, content := range fixture.files {
				writeBuildFixture(t, boundary, path, content)
			}
			root := boundary
			if fixture.root != "" {
				root = boundary + "/" + fixture.root
			}
			prepared, err := NewArtifactBuilder(&artifactBackendFake{}).PrepareWithin(context.Background(), boundary, root, fixture.config, false, "just-dashboard/test:run-1")
			if fixture.fails != "" {
				if err == nil || !strings.Contains(err.Error(), fixture.fails) {
					t.Fatalf("err = %v, want %q", err, fixture.fails)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range fixture.want {
				if !strings.Contains(prepared.DockerfilePreview, want) {
					t.Fatalf("Dockerfile missing %q:\n%s", want, prepared.DockerfilePreview)
				}
			}
			for _, absent := range fixture.absent {
				if strings.Contains(prepared.DockerfilePreview, absent) {
					t.Fatalf("Dockerfile carries %q:\n%s", absent, prepared.DockerfilePreview)
				}
			}
			if fixture.root != "" && prepared.ContextDirectory != "." {
				t.Fatalf("context directory = %q", prepared.ContextDirectory)
			}
		})
	}
}

func TestPythonRecipeRecordsWhatItDecided(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeBuildFixture(t, root, "requirements.txt", "numpy==1.26.4\nflask==3.1.0\ngunicorn==23.0.0\n")
	writeBuildFixture(t, root, "app.py", "app = Flask(__name__)\n")
	prepared, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), root,
		BuildPlanConfig{Method: BuildRecipe, Recipe: "python", StartCommand: "gunicorn app:app"}, false, "t")
	if err != nil {
		t.Fatal(err)
	}
	notes := strings.Join(prepared.Notes, "\n")
	if prepared.PythonVersion != "3.12" || !strings.Contains(notes, "numpy==1.26.4 publish no wheels for Python 3.13; using 3.12") ||
		!prepared.WebConcurrency || prepared.Toolchain != "python 3.12" {
		t.Fatalf("prepared = %+v", prepared)
	}
	prepared, err = NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), root,
		BuildPlanConfig{Method: BuildRecipe, Recipe: "python", StartCommand: "gunicorn -w 4 app:app"}, false, "t")
	if err != nil || prepared.WebConcurrency {
		t.Fatalf("an explicit worker count is the command's own: %+v, %v", prepared, err)
	}
}

func TestPythonStartCommandsLeaveWorkersToWebConcurrency(t *testing.T) {
	t.Parallel()
	for command, want := range map[string]bool{
		"gunicorn --bind 0.0.0.0:${PORT:-8000} app:app":                          true,
		"python manage.py migrate --noinput && gunicorn config.wsgi:application": true,
		"uvicorn main:app --host 0.0.0.0 --port ${PORT:-8000}":                   true,
		"gunicorn -w 4 app:app":                                                  false,
		"gunicorn --workers=3 app:app":                                           false,
		"uvicorn main:app --workers 2":                                           false,
		"uvicorn main:app --reload":                                              false,
		"gunicorn -k eventlet -w 1 app:app":                                      false,
		"streamlit run app.py":                                                   false,
		"python app.py":                                                          false,
	} {
		if got := pythonReadsWebConcurrency(command); got != want {
			t.Fatalf("%q: %v, want %v", command, got, want)
		}
	}
}
