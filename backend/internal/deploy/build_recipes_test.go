package deploy

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The generated Dockerfiles for the catalogue's frameworks and the Python
// install shapes: the runtime environment a framework needs, the check that
// the build wrote its entry, the nginx fallback for a single-page site, and
// where an undeclared process manager is installed so the start command
// finds it.
func TestRecipesRenderFrameworkEnvironmentsEntriesAndFallbacks(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		files   map[string]string
		config  BuildPlanConfig
		want    []string
		absent  []string
		failure string
	}{
		{
			name:   "astro node gets its host and entry check",
			files:  map[string]string{"package.json": `{"scripts":{"build":"astro build"},"dependencies":{"astro":"5","@astrojs/node":"9"}}`, "package-lock.json": "{}"},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "node", BuildCommand: "npm run build", StartCommand: "node ./dist/server/entry.mjs"},
			want: []string{"RUN test -f /app/dist/server/entry.mjs || (echo 'Astro must produce dist/server/entry.mjs; configure its output and start command together'",
				"ENV HOST=0.0.0.0", "CMD [\"/bin/sh\",\"-c\",\"node ./dist/server/entry.mjs\"]"},
		},
		{
			name:   "nuxt with a schema step still checks the entry",
			files:  map[string]string{"package.json": `{"scripts":{"build":"nuxt build"},"dependencies":{"nuxt":"3"}}`, "pnpm-lock.yaml": "lockfileVersion: 9"},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "node", BuildCommand: "pnpm run build", StartCommand: "pnpm exec prisma migrate deploy && node .output/server/index.mjs"},
			want:   []string{"RUN test -f /app/.output/server/index.mjs || (echo 'Nuxt must produce"},
			absent: []string{"ENV HOST"},
		},
		{
			name:   "custom start command skips the entry check",
			files:  map[string]string{"package.json": `{"scripts":{"build":"nuxt build"},"dependencies":{"nuxt":"3"}}`, "pnpm-lock.yaml": "lockfileVersion: 9"},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "node", BuildCommand: "pnpm run build", StartCommand: "node server.js"},
			absent: []string{"RUN test -f"},
		},
		{
			name:   "sveltekit adapter-node keeps its check",
			files:  map[string]string{"package.json": `{"scripts":{"build":"vite build"},"devDependencies":{"vite":"6","@sveltejs/kit":"2","@sveltejs/adapter-node":"5"}}`, "bun.lock": ""},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "node", BuildCommand: "bun run build", StartCommand: "bun ./build/index.js"},
			want:   []string{"RUN test -f /app/build/index.js || (echo 'SvelteKit must produce build/index.js"},
		},
		{
			name:   "single-page site falls back to index.html",
			files:  map[string]string{"package.json": `{"scripts":{"build":"vite build"},"devDependencies":{"vite":"6"}}`, "package-lock.json": "{}"},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "node", BuildCommand: "npm run build", OutputDirectory: "dist", SPAFallback: true},
			want: []string{"FROM nginx:1.29-alpine@sha256:", "try_files $uri $uri/ /index.html;", "> /etc/nginx/conf.d/default.conf",
				"COPY --from=build /app/dist/ /usr/share/nginx/html/"},
		},
		{
			name:   "multi-page site keeps nginx defaults",
			files:  map[string]string{"package.json": `{"scripts":{"build":"astro build"},"dependencies":{"astro":"5"}}`, "package-lock.json": "{}"},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "node", BuildCommand: "npm run build", OutputDirectory: "dist"},
			absent: []string{"try_files", "default.conf"},
		},
		{
			name:   "static files with the fallback",
			files:  map[string]string{"public/index.html": "<h1>x</h1>"},
			config: BuildPlanConfig{Method: BuildStatic, OutputDirectory: "public", SPAFallback: true},
			want:   []string{"try_files $uri $uri/ /index.html;", "COPY public/ /usr/share/nginx/html/"},
		},
		{
			name:   "unpinned requirements install and the undeclared server is added",
			files:  map[string]string{"requirements.txt": "flask\n", ".python-version": "3.12\n", "app.py": "app = Flask(__name__)\n"},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "python", StartCommand: "gunicorn --bind 0.0.0.0:8000 app:app"},
			want: []string{"FROM python:3.12-slim@sha256:", "ENV PYTHONUNBUFFERED=1", "pip install --no-cache-dir --requirement requirements.txt",
				"RUN pip install --no-cache-dir gunicorn==23.0.0", "CMD [\"/bin/sh\",\"-c\",\"gunicorn --bind 0.0.0.0:8000 app:app\"]"},
		},
		{
			name:   "declared server is not installed twice",
			files:  map[string]string{"requirements.txt": "fastapi[standard]==0.116.1\n", "main.py": "app = FastAPI()\n"},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "python", StartCommand: "uvicorn main:app --host 0.0.0.0 --port 8000"},
			want:   []string{"FROM python:3.13-slim@sha256:"},
			absent: []string{"uvicorn==0.35.0"},
		},
		{
			name: "uv lock installs into the project environment",
			files: map[string]string{"pyproject.toml": "[project]\nname = \"x\"\nrequires-python = \">=3.11\"\ndependencies = [\"fastapi\"]\n",
				"uv.lock": "[[package]]\nname = \"fastapi\"\n", "main.py": "app = FastAPI()\n"},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "python", StartCommand: "uvicorn main:app --host 0.0.0.0 --port 8000"},
			want: []string{"ENV VIRTUAL_ENV=/app/.venv", "ENV PATH=/app/.venv/bin:$PATH", "uv sync --frozen --no-dev",
				"RUN uv pip install --no-cache uvicorn==0.35.0"},
		},
		{
			name: "poetry installs into the interpreter",
			files: map[string]string{"pyproject.toml": "[tool.poetry]\nname = \"x\"\n[tool.poetry.dependencies]\npython = \"^3.11\"\nflask = \"^3\"\n",
				"poetry.lock": "[[package]]\nname = \"flask\"\n", "app.py": "app = Flask(__name__)\n"},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "python", StartCommand: "gunicorn --bind 0.0.0.0:8000 app:app"},
			want:   []string{"ENV POETRY_VIRTUALENVS_CREATE=false", "poetry install --only main --no-root --no-interaction", "RUN pip install --no-cache-dir gunicorn==23.0.0"},
		},
		{
			name:   "bare pyproject reads its dependency list",
			files:  map[string]string{"pyproject.toml": "[project]\nname = \"x\"\ndependencies = [\"streamlit\"]\n", "streamlit_app.py": "import streamlit\n"},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "python", StartCommand: "streamlit run streamlit_app.py --server.port 8501 --server.address 0.0.0.0 --server.headless true"},
			want:   []string{"import tomllib", "/tmp/jd-requirements.txt", "pip install --no-cache-dir --requirement /tmp/jd-requirements.txt"},
			absent: []string{"tomli=="},
		},
		{
			name:   "bare pyproject on 3.10 brings its own toml reader",
			files:  map[string]string{"pyproject.toml": "[project]\nname = \"x\"\nrequires-python = \"==3.10.*\"\ndependencies = [\"gradio\"]\n", "app.py": "import gradio as gr\ndemo.launch()\n"},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "python", StartCommand: "python app.py"},
			want:   []string{"FROM python:3.10-slim@sha256:", "pip install --no-cache-dir tomli==2.0.1", "import tomli as tomllib", "ENV GRADIO_SERVER_NAME=0.0.0.0"},
		},
		{
			name:   "explicit interpreter setting wins over the version file",
			files:  map[string]string{"requirements.txt": "flask==3.1.0\n", ".python-version": "3.12\n", "app.py": ""},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "python", PythonVersion: "3.11", StartCommand: "gunicorn app:app"},
			want:   []string{"FROM python:3.11-slim@sha256:"},
		},
		{
			name:    "python without a start command is refused with the frameworks named",
			files:   map[string]string{"requirements.txt": "flask==3.1.0\n"},
			config:  BuildPlanConfig{Method: BuildRecipe, Recipe: "python"},
			failure: "Django, FastAPI, Flask, Streamlit and Gradio",
		},
		{
			name:    "unsupported interpreter is refused",
			files:   map[string]string{"requirements.txt": "flask==3.1.0\n", "runtime.txt": "python-3.9.19\n"},
			config:  BuildPlanConfig{Method: BuildRecipe, Recipe: "python", StartCommand: "gunicorn app:app"},
			failure: "3.10 to 3.13",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			for path, content := range test.files {
				writeBuildFixture(t, root, path, content)
			}
			backend := &artifactBackendFake{}
			prepared, err := NewArtifactBuilder(backend).Prepare(context.Background(), root, test.config, false, "just-dashboard/test:run-1")
			if test.failure != "" {
				if err == nil || !errors.Is(err, ErrUnsupportedBuilder) || !strings.Contains(err.Error(), test.failure) {
					t.Fatalf("err = %v, want %q", err, test.failure)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range test.want {
				if !strings.Contains(prepared.DockerfilePreview, want) {
					t.Fatalf("Dockerfile missing %q:\n%s", want, prepared.DockerfilePreview)
				}
			}
			for _, absent := range test.absent {
				if strings.Contains(prepared.DockerfilePreview, absent) {
					t.Fatalf("Dockerfile carries %q:\n%s", absent, prepared.DockerfilePreview)
				}
			}
			if test.config.Recipe == "python" && prepared.PythonVersion == "" {
				t.Fatalf("no Python version recorded: %+v", prepared)
			}
			for _, base := range prepared.BaseImages {
				if strings.Contains(prepared.DockerfilePreview, base.Reference) &&
					!strings.Contains(prepared.DockerfilePreview, base.Reference+"@"+base.Digest) {
					t.Fatalf("base %s is not digest pinned:\n%s", base.Reference, prepared.DockerfilePreview)
				}
			}
		})
	}
}

func TestPlanValidationBoundsTheNewBuildFields(t *testing.T) {
	t.Parallel()
	base := PlanConfiguration{
		Build:        BuildPlanConfig{Method: BuildRecipe, Recipe: "python", Secrets: []BuildSecretConfig{}, ReleaseTasks: []ReleaseTaskConfig{}},
		Runtime:      RuntimePlanConfig{Strategy: StrategyStopFirst, BindAddress: "127.0.0.1"},
		Variables:    []PlannedVariable{},
		Dependencies: []PlannedDependency{},
		Checks:       []PlannedCheck{},
		Domains:      []PlannedDomain{},
	}
	for _, fixture := range []struct {
		name  string
		build BuildPlanConfig
		ok    bool
	}{
		{"python version in range", BuildPlanConfig{Method: BuildRecipe, Recipe: "python", PythonVersion: "3.12"}, true},
		{"python version out of range", BuildPlanConfig{Method: BuildRecipe, Recipe: "python", PythonVersion: "3.9"}, false},
		{"python version on a node recipe", BuildPlanConfig{Method: BuildRecipe, Recipe: "node", PythonVersion: "3.12"}, false},
		{"fallback on a static site", BuildPlanConfig{Method: BuildStatic, OutputDirectory: "public", SPAFallback: true}, true},
		{"fallback on a recipe", BuildPlanConfig{Method: BuildRecipe, Recipe: "node", OutputDirectory: "dist", SPAFallback: true}, true},
		{"fallback on a dockerfile", BuildPlanConfig{Method: BuildDockerfile, SPAFallback: true}, false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			configuration := base
			configuration.Build = fixture.build
			configuration.Build.Secrets = []BuildSecretConfig{}
			configuration.Build.ReleaseTasks = []ReleaseTaskConfig{}
			if err := configuration.Validate(); (err == nil) != fixture.ok {
				t.Fatalf("Validate = %v", err)
			}
		})
	}
}
