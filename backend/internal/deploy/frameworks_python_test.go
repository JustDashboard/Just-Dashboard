package deploy

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// Every Python framework the catalogue recognises, from the layout its own
// starter produces, and the process a deployment of it runs. The FastAPI row
// with a bare `fastapi` line is the ordinary case: an unpinned requirements
// file and no server declared, which used to be refused outright.
func TestPythonFrameworkCatalogueDetectsServingDefaults(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name              string
		files             map[string]string
		framework, start  string
		port              int
		profile           WorkloadProfile
		confidence        DetectionConfidence
		unpinned          bool
		version, decision string
	}{
		{name: "fastapi at the root", files: map[string]string{
			"requirements.txt": "fastapi\nsqlalchemy>=2\n",
			"main.py":          "from fastapi import FastAPI\n\napp = FastAPI()\n",
		}, framework: "fastapi", start: "uvicorn main:app --host 0.0.0.0 --port ${PORT:-8000}", port: 8000, profile: ProfileWeb, confidence: ConfidenceHigh, unpinned: true, version: "3.13"},
		{name: "fastapi in a package with a version file", files: map[string]string{
			"requirements.txt": "fastapi[standard]==0.116.1\n",
			".python-version":  "3.12.4\n",
			"app/__init__.py":  "",
			"app/main.py":      "from fastapi import FastAPI\n\napi: FastAPI = FastAPI(title=\"x\")\n",
			"tests/main.py":    "app = FastAPI()\n",
		}, framework: "fastapi", start: "uvicorn app.main:api --host 0.0.0.0 --port ${PORT:-8000}", port: 8000, profile: ProfileWeb, confidence: ConfidenceHigh, version: "3.12"},
		{name: "fastapi without an application object", files: map[string]string{
			"requirements.txt":   "fastapi==0.116.1\n",
			"src/server/core.py": "app = FastAPI()\n",
		}, framework: "fastapi", port: 8000, profile: ProfileWeb, confidence: ConfidenceLow, version: "3.13", decision: "module:app"},
		{name: "flask module", files: map[string]string{
			"requirements.txt": "Flask==3.1.0\ngunicorn==23.0.0\n",
			"app.py":           "from flask import Flask\napp = Flask(__name__)\n",
		}, framework: "flask", start: "gunicorn --bind 0.0.0.0:${PORT:-8000} app:app", port: 8000, profile: ProfileWeb, confidence: ConfidenceHigh, version: "3.13"},
		{name: "flask factory", files: map[string]string{
			"pyproject.toml":  "[project]\nname = \"site\"\nrequires-python = \">=3.11\"\ndependencies = [\n  \"flask>=3\",\n  \"psycopg[binary]\",\n]\n",
			"app/__init__.py": "from flask import Flask\n\ndef create_app():\n    return Flask(__name__)\n",
		}, framework: "flask", start: "gunicorn --bind 0.0.0.0:${PORT:-8000} 'app:create_app()'", port: 8000, profile: ProfileWeb, confidence: ConfidenceHigh, unpinned: true, version: "3.13"},
		{name: "django", files: map[string]string{
			"requirements.txt": "Django==5.1.4\n",
			"manage.py":        "#!/usr/bin/env python\n",
			"mysite/wsgi.py":   "application = get_wsgi_application()\n",
			"mysite/asgi.py":   "application = get_asgi_application()\n",
		}, framework: "django", start: "python manage.py migrate --noinput && gunicorn mysite.wsgi:application --bind 0.0.0.0:${PORT:-8000}", port: 8000, profile: ProfileWeb, confidence: ConfidenceHigh, version: "3.13"},
		{name: "django with whitenoise from a uv lock", files: map[string]string{
			"pyproject.toml": "[project]\nname = \"site\"\nrequires-python = \">=3.12,<3.13\"\ndependencies = [\"django\", \"whitenoise\"]\n",
			"uv.lock":        "version = 1\n\n[[package]]\nname = \"django\"\nversion = \"5.1.4\"\n\n[[package]]\nname = \"whitenoise\"\nversion = \"6.8.2\"\n",
			"manage.py":      "",
			"config/wsgi.py": "",
		}, framework: "django", start: "python manage.py migrate --noinput && python manage.py collectstatic --noinput && gunicorn config.wsgi:application --bind 0.0.0.0:${PORT:-8000}", port: 8000, profile: ProfileWeb, confidence: ConfidenceHigh, version: "3.12"},
		{name: "django without its wsgi module", files: map[string]string{
			"requirements.txt": "django\n",
			"manage.py":        "",
		}, framework: "django", port: 8000, profile: ProfileWeb, confidence: ConfidenceLow, unpinned: true, version: "3.13", decision: "project.wsgi:application"},
		{name: "streamlit", files: map[string]string{
			"requirements.txt": "streamlit\npandas\n",
			"streamlit_app.py": "import streamlit as st\nst.title('x')\n",
		}, framework: "streamlit", start: "streamlit run streamlit_app.py --server.port ${PORT:-8501} --server.address 0.0.0.0 --server.headless true", port: 8501, profile: ProfileWeb, confidence: ConfidenceHigh, unpinned: true, version: "3.13"},
		{name: "gradio", files: map[string]string{
			"requirements.txt": "gradio==5.9.0\n",
			"app.py":           "import gradio as gr\n\ndemo = gr.Interface(fn=lambda x: x, inputs='text', outputs='text')\ndemo.launch()\n",
		}, framework: "gradio", start: "python app.py", port: 7860, profile: ProfileWeb, confidence: ConfidenceHigh, version: "3.13"},
		{name: "poetry flask", files: map[string]string{
			"pyproject.toml": "[tool.poetry]\nname = \"site\"\n\n[tool.poetry.dependencies]\npython = \"^3.11\"\nflask = \"^3.1\"\n",
			"poetry.lock":    "[[package]]\nname = \"flask\"\nversion = \"3.1.0\"\n",
			"main.py":        "app = Flask(__name__)\n",
		}, framework: "flask", start: "gunicorn --bind 0.0.0.0:${PORT:-8000} main:app", port: 8000, profile: ProfileWeb, confidence: ConfidenceHigh, version: "3.13"},
		{name: "plain script", files: map[string]string{
			"requirements.txt": "requests==2.32.3\n",
			"main.py":          "print('hi')\n",
		}, framework: "python", start: "python main.py", profile: ProfileWorker, confidence: ConfidenceLow, version: "3.13", decision: "web application) or runs as a worker"},
		{name: "procfile", files: map[string]string{
			"requirements.txt": "fastapi==0.116.1\n",
			"Procfile":         "web: uvicorn server.api:application --host 0.0.0.0 --port $PORT\n",
		}, framework: "fastapi", start: "uvicorn server.api:application --host 0.0.0.0 --port $PORT", port: 8000, profile: ProfileWeb, confidence: ConfidenceLow, version: "3.13"},
		{name: "unsupported interpreter", files: map[string]string{
			"requirements.txt": "flask==3.1.0\n",
			"runtime.txt":      "python-3.9.19\n",
			"app.py":           "app = Flask(__name__)\n",
		}, framework: "flask", start: "gunicorn --bind 0.0.0.0:${PORT:-8000} app:app", port: 8000, profile: ProfileWeb, confidence: ConfidenceHigh, decision: ""},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			for path, content := range fixture.files {
				writeBuildFixture(t, root, path, content)
			}
			result, err := (Detector{}).DetectPath(context.Background(), root, SourceIdentity{})
			if err != nil || len(result.Candidates) != 1 {
				t.Fatalf("detect: %+v, %v", result, err)
			}
			candidate := result.Candidates[0]
			if candidate.Recipe != "python" || candidate.BuildMethod != BuildRecipe ||
				candidate.Framework != fixture.framework || candidate.StartCommand != fixture.start ||
				candidate.Port != fixture.port || candidate.Profile != fixture.profile ||
				candidate.Confidence != fixture.confidence || candidate.UnpinnedDependencies != fixture.unpinned ||
				candidate.PythonVersion != fixture.version {
				t.Fatalf("candidate = %+v", candidate)
			}
			if fixture.name == "unsupported interpreter" && !strings.Contains(candidate.RecipeIssue, "3.10 to 3.13") {
				t.Fatalf("recipe issue = %q", candidate.RecipeIssue)
			}
			if fixture.decision != "" && !slices.ContainsFunc(candidate.NeedsDecision, func(d string) bool { return strings.Contains(d, fixture.decision) }) {
				t.Fatalf("decisions %q lack %q", candidate.NeedsDecision, fixture.decision)
			}
		})
	}
}

func TestPythonVersionSelection(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name, explicit, versionFile, runtimeFile, pyproject, want string
		fails                                                     bool
	}{
		{name: "default", want: "3.13"},
		{name: "explicit wins", explicit: "3.11", versionFile: "3.12", want: "3.11"},
		{name: "version file", versionFile: "# pinned\n3.12.4\n", want: "3.12"},
		{name: "runtime.txt", runtimeFile: "python-3.11.9", want: "3.11"},
		{name: "requires floor", pyproject: "[project]\nrequires-python = \">=3.11\"\n", want: "3.13"},
		{name: "requires ceiling", pyproject: "[project]\nrequires-python = \">=3.10,<3.13\"\n", want: "3.12"},
		{name: "requires inclusive ceiling", pyproject: "[project]\nrequires-python = \"<=3.11\"\n", want: "3.11"},
		{name: "compatible release family", pyproject: "[project]\nrequires-python = \"~=3.11.0\"\n", want: "3.11"},
		{name: "compatible release floor", pyproject: "[project]\nrequires-python = \"~=3.11\"\n", want: "3.13"},
		{name: "exact family", pyproject: "[project]\nrequires-python = \"==3.12.*\"\n", want: "3.12"},
		{name: "poetry caret", pyproject: "[tool.poetry.dependencies]\npython = \"^3.10\"\n", want: "3.13"},
		{name: "old floor keeps the default", pyproject: "[project]\nrequires-python = \">=3.8\"\n", want: "3.13"},
		{name: "unsupported family", versionFile: "3.9", fails: true},
		{name: "unsupported explicit", explicit: "3.14", fails: true},
		{name: "future exact family", pyproject: "[project]\nrequires-python = \"==3.14.*\"\n", fails: true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			got, err := choosePythonRecipeVersion(fixture.explicit, fixture.versionFile, fixture.runtimeFile, fixture.pyproject)
			if fixture.fails {
				if err == nil {
					t.Fatalf("selected %q", got)
				}
				return
			}
			if err != nil || got != fixture.want {
				t.Fatalf("selected %q, %v", got, err)
			}
		})
	}
}

func TestPythonDependencyReading(t *testing.T) {
	t.Parallel()
	deps := readPythonDependencies(map[string][]byte{
		"requirements.txt": []byte("-r base.txt\n# comment\nFastAPI[standard]>=0.110  # inline\npsycopg2-binary==2.9.10\n--extra-index-url https://x\n"),
	})
	if !deps.has("fastapi") || !deps.has("uvicorn") || !deps.has("psycopg2_binary") || !deps.unpinned || deps.source != "requirements.txt" {
		t.Fatalf("requirements: %+v", deps)
	}
	deps = readPythonDependencies(map[string][]byte{
		"pyproject.toml": []byte("[project]\nname = \"x\"\ndependencies = [\"django>=5\", 'celery[redis]']\n\n[project.optional-dependencies]\ndev = [\"pytest\"]\n"),
	})
	if !deps.has("django") || !deps.has("celery") || deps.has("pytest") || !deps.unpinned {
		t.Fatalf("pyproject: %+v", deps)
	}
	deps = readPythonDependencies(map[string][]byte{
		"pyproject.toml": []byte("[project]\ndependencies = [\"flask\"]\n"),
		"uv.lock":        []byte("[[package]]\nname = \"flask\"\n[[package]]\nname = \"werkzeug\"\n"),
	})
	if !deps.has("werkzeug") || deps.unpinned || deps.source != "uv.lock" {
		t.Fatalf("lock: %+v", deps)
	}
	if got := undeclaredPythonServers("python manage.py migrate && gunicorn app:app", deps); !slices.Equal(got, []string{"gunicorn"}) {
		t.Fatal(got)
	}
	deps.names["gunicorn"] = true
	if got := undeclaredPythonServers("gunicorn app:app", deps); len(got) != 0 {
		t.Fatal(got)
	}
}

// A nested Python project keeps its own entries: the FastAPI object in
// services/api belongs to that root, not to the repository root's script.
func TestPythonEntriesStayUnderTheirOwnRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeBuildFixture(t, root, "requirements.txt", "requests\n")
	writeBuildFixture(t, root, "main.py", "print('root script')\n")
	writeBuildFixture(t, root, "services/api/requirements.txt", "fastapi\n")
	writeBuildFixture(t, root, "services/api/app/main.py", "app = FastAPI()\n")
	result, err := (Detector{}).DetectPath(context.Background(), root, SourceIdentity{})
	if err != nil || len(result.Candidates) != 2 {
		t.Fatalf("detect: %+v, %v", result, err)
	}
	for _, candidate := range result.Candidates {
		switch candidate.Root {
		case "":
			if candidate.StartCommand != "python main.py" || candidate.Framework != "python" {
				t.Fatalf("root: %+v", candidate)
			}
		case "services/api":
			if candidate.StartCommand != "uvicorn app.main:app --host 0.0.0.0 --port ${PORT:-8000}" {
				t.Fatalf("nested: %+v", candidate)
			}
			if !slices.ContainsFunc(candidate.Evidence, func(e DetectionEvidence) bool { return e.Path == "services/api/app/main.py" }) {
				t.Fatalf("evidence: %+v", candidate.Evidence)
			}
		default:
			t.Fatalf("unexpected root %q", candidate.Root)
		}
	}
}

// Unpinned dependencies are a warning the operator acknowledges, not a
// refusal: the deployment works today, and the finding says what a rebuild
// may do differently and how to pin.
func TestPreflightWarnsAboutUnpinnedDependencies(t *testing.T) {
	candidate := DetectedCandidate{
		ID: "root", Name: "api", Profile: ProfileWeb, BuildMethod: BuildRecipe, Recipe: "python", Framework: "fastapi",
		Confidence: ConfidenceHigh, StartCommand: "uvicorn main:app --host 0.0.0.0 --port ${PORT:-8000}", Port: 8000,
		UnpinnedDependencies: true, Evidence: []DetectionEvidence{}, NeedsDecision: []string{},
	}
	draft := &Draft{Data: DraftData{
		Intent: &DraftIntentConfig{Name: "api", Profile: ProfileWeb},
		Source: &DraftSourceConfig{Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://example.test/o/r.git", Ref: "main"},
		Detection: &DetectionResult{
			Source:     SourceIdentity{Kind: SourceGit, Revision: strings.Repeat("a", 40)},
			Candidates: []DetectedCandidate{candidate}, SelectedID: "root",
		},
	}}
	configuration := PlanConfiguration{
		Build:   BuildPlanConfig{Method: BuildRecipe, Recipe: "python", StartCommand: candidate.StartCommand, Secrets: []BuildSecretConfig{}, ReleaseTasks: []ReleaseTaskConfig{}},
		Runtime: RuntimePlanConfig{Strategy: StrategyStopFirst, InternalPort: 8000, Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{}},
		Domains: []PlannedDomain{}, Variables: []PlannedVariable{}, Dependencies: []PlannedDependency{},
		Checks: []PlannedCheck{{Name: "readiness", Kind: string(CheckHTTP), Phase: "readiness", Required: true, Config: json.RawMessage(`{"path":"/"}`)}},
	}
	observation := HostObservation{Facilities: map[string]FacilityObservation{"docker": {Available: true}}}
	findings := preflightFindings(draft, configuration, observation, false)
	index := slices.IndexFunc(findings, func(item PreflightFinding) bool { return item.Code == "dependencies_unpinned" })
	if index < 0 || findings[index].Severity != PreflightWarning || !strings.Contains(findings[index].Action, "pip freeze") {
		t.Fatalf("unpinned warning: %#v", findings)
	}
	draft.Data.Detection.Candidates[0].UnpinnedDependencies = false
	if findings := preflightFindings(draft, configuration, observation, false); slices.ContainsFunc(findings, func(item PreflightFinding) bool { return item.Code == "dependencies_unpinned" }) {
		t.Fatal("pinned dependencies still warned")
	}
}
