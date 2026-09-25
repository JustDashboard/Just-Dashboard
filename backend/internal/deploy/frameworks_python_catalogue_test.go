package deploy

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func detectPythonFixture(t *testing.T, files map[string]string) (DetectionResult, *DetectedCandidate) {
	t.Helper()
	root := t.TempDir()
	for path, content := range files {
		writeBuildFixture(t, root, path, content)
	}
	result, err := (Detector{}).DetectPath(context.Background(), root, SourceIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	return result, selectedDetectionCandidate(&result)
}

// Every framework the catalogue serves, from the layout its own starter or
// documentation uses, and the process a deployment of it runs.
func TestPythonCatalogueServesEveryPopularFramework(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name       string
		files      map[string]string
		framework  string
		start      string
		port       int
		confidence DetectionConfidence
		issue      string
	}{
		{name: "litestar", files: map[string]string{"requirements.txt": "litestar[standard]==2.13.0\n", "app.py": "from litestar import Litestar\napp = Litestar([])\n"},
			framework: "litestar", start: "uvicorn app:app --host 0.0.0.0 --port ${PORT:-8000}", port: 8000},
		{name: "starlette built in its entry", files: map[string]string{"requirements.txt": "starlette==0.41\nuvicorn\n", "main.py": "from starlette.applications import Starlette\napp = Starlette(routes=[])\n"},
			framework: "starlette", start: "uvicorn main:app --host 0.0.0.0 --port ${PORT:-8000}", port: 8000},
		{name: "sanic", files: map[string]string{"requirements.txt": "sanic==24.6.0\n", "server.py": "from sanic import Sanic\napp = Sanic(\"svc\")\n"},
			framework: "sanic", start: "sanic server:app --host 0.0.0.0 --port ${PORT:-8000}", port: 8000},
		{name: "quart", files: map[string]string{"requirements.txt": "quart==0.19.9\n", "app.py": "from quart import Quart\napp = Quart(__name__)\n"},
			framework: "quart", start: "hypercorn --bind 0.0.0.0:${PORT:-8000} --access-logfile - 'app:app'", port: 8000},
		{name: "falcon wsgi", files: map[string]string{"requirements.txt": "falcon==4.0.2\n", "app.py": "import falcon\napp = falcon.App()\n"},
			framework: "falcon", start: "gunicorn --bind 0.0.0.0:${PORT:-8000} --access-logfile - app:app", port: 8000},
		{name: "falcon asgi", files: map[string]string{"requirements.txt": "falcon==4.0.2\n", "app.py": "import falcon.asgi\napp = falcon.asgi.App()\n"},
			framework: "falcon", start: "uvicorn app:app --host 0.0.0.0 --port ${PORT:-8000}", port: 8000},
		{name: "bottle", files: map[string]string{"requirements.txt": "bottle==0.13.2\n", "app.py": "import bottle\napp = bottle.Bottle()\n"},
			framework: "bottle", start: "gunicorn --bind 0.0.0.0:${PORT:-8000} --access-logfile - app:app", port: 8000},
		{name: "aiohttp run_app", files: map[string]string{"requirements.txt": "aiohttp==3.11.10\n", "server.py": "from aiohttp import web\napp = web.Application()\nweb.run_app(app, port=9000)\n"},
			framework: "aiohttp", start: "python server.py", port: 9000},
		{name: "aiohttp object", files: map[string]string{"requirements.txt": "aiohttp==3.11.10\ngunicorn\n", "app.py": "from aiohttp import web\napp = web.Application()\n"},
			framework: "aiohttp", start: "gunicorn --bind 0.0.0.0:${PORT:-8080} --access-logfile - --worker-class aiohttp.GunicornWebWorker app:app", port: 8080},
		{name: "tornado", files: map[string]string{"requirements.txt": "tornado==6.4.2\n", "main.py": "import tornado.web\napp = tornado.web.Application([])\napp.listen(8888)\n"},
			framework: "tornado", start: "python main.py", port: 8888},
		{name: "dash behind gunicorn", files: map[string]string{"requirements.txt": "dash==2.18.2\nflask\ngunicorn\n", "app.py": "from dash import Dash, html\napp = Dash(__name__)\nserver = app.server\n"},
			framework: "dash", start: "gunicorn --bind 0.0.0.0:${PORT:-8050} --access-logfile - app:server", port: 8050},
		{name: "dash's own server", files: map[string]string{"requirements.txt": "dash==2.18.2\n", "app.py": "import dash\napp = dash.Dash(__name__)\napp.run()\n"},
			framework: "dash", start: "python app.py", port: 8050},
		{name: "panel", files: map[string]string{"requirements.txt": "panel==1.5.4\n", "app.py": "import panel as pn\npn.panel('x').servable()\n"},
			framework: "panel", start: "panel serve app.py --address 0.0.0.0 --port ${PORT:-5006}", port: 5006},
		{name: "chainlit", files: map[string]string{"requirements.txt": "chainlit==2.0.0\nfastapi\n", "app.py": "import chainlit as cl\n@cl.on_message\nasync def main(message): ...\n"},
			framework: "chainlit", start: "chainlit run app.py --host 0.0.0.0 --port ${PORT:-8000} --headless", port: 8000},
		{name: "nicegui", files: map[string]string{"requirements.txt": "nicegui==2.8.0\n", "main.py": "from nicegui import ui\nui.label('x')\nui.run(port=8081)\n"},
			framework: "nicegui", start: "python main.py", port: 8081},
		{name: "mesop", files: map[string]string{"requirements.txt": "mesop==0.14.1\n", "main.py": "import mesop as me\n@me.page(path='/')\ndef home():\n    me.text('x')\n"},
			framework: "mesop", start: "gunicorn --bind 0.0.0.0:${PORT:-8080} --access-logfile - main:me", port: 8080},
		{name: "reflex needs its own Dockerfile", files: map[string]string{"requirements.txt": "reflex==0.6.7\n", "rxconfig.py": "import reflex as rx\nconfig = rx.Config(app_name='x')\n"},
			framework: "reflex", port: 3000, confidence: ConfidenceLow, issue: "Reflex compiles a Node frontend"},
		{name: "streamlit multipage entry beside pages/", files: map[string]string{"requirements.txt": "streamlit==1.41.0\n", "Home.py": "import streamlit as st\nst.set_page_config(page_title='x')\n", "pages/1_Chart.py": "import streamlit as st\n", "utils.py": "import pandas\n"},
			framework: "streamlit", start: "streamlit run Home.py --server.port ${PORT:-8501} --server.address 0.0.0.0 --server.headless true", port: 8501},
		{name: "gradio under any name, on its own port", files: map[string]string{"requirements.txt": "gradio==5.9.1\n", "demo.py": "import gradio as gr\ngr.Interface(lambda x: x, 'text', 'text').launch(server_port=7861)\n"},
			framework: "gradio", start: "python demo.py", port: 7861},
		{name: "a FastAPI app that mounts Gradio is FastAPI", files: map[string]string{"requirements.txt": "fastapi\ngradio\nuvicorn\n", "main.py": "import gradio as gr\nfrom fastapi import FastAPI\napp = FastAPI()\napp = gr.mount_gradio_app(app, gr.Interface(lambda x: x, 'text', 'text'), path='/ui')\n"},
			framework: "fastapi", start: "uvicorn main:app --host 0.0.0.0 --port ${PORT:-8000}", port: 8000},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			_, candidate := detectPythonFixture(t, fixture.files)
			if candidate == nil {
				t.Fatal("no candidate selected")
			}
			confidence := fixture.confidence
			if confidence == "" {
				confidence = ConfidenceHigh
			}
			if candidate.Framework != fixture.framework || candidate.StartCommand != fixture.start || candidate.Port != fixture.port ||
				candidate.Confidence != confidence || !strings.Contains(candidate.RecipeIssue, fixture.issue) || candidate.Profile != ProfileWeb {
				t.Fatalf("candidate = %s %q port %d %s issue %q", candidate.Framework, candidate.StartCommand, candidate.Port, candidate.Confidence, candidate.RecipeIssue)
			}
		})
	}
}

// The application object is found where applications keep it: a factory, a
// qualified constructor, a module a run.py imports its factory from, a src
// layout, an application folder that imports its siblings by bare name.
func TestPythonApplicationObjectsAreFoundBeyondTheConventionalFiles(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name     string
		files    map[string]string
		start    string
		decision string
	}{
		{name: "a factory with a module-level instance", files: map[string]string{"requirements.txt": "fastapi\n", "main.py": "from fastapi import FastAPI\n\ndef create_app() -> FastAPI:\n    return FastAPI()\n\napp = create_app()\n"},
			start: "uvicorn main:app --host 0.0.0.0 --port ${PORT:-8000}"},
		{name: "a factory uvicorn calls itself", files: map[string]string{"requirements.txt": "fastapi\n", "app/main.py": "import fastapi\n\ndef create_app(settings=None):\n    return fastapi.FastAPI()\n"},
			start: "uvicorn --factory app.main:create_app --host 0.0.0.0 --port ${PORT:-8000}"},
		{name: "a Flask factory that takes a config uses the object built from it", files: map[string]string{"requirements.txt": "flask\ngunicorn\n", "app/__init__.py": "from flask import Flask\n\ndef create_app(config_name):\n    return Flask(__name__)\n", "flasky.py": "import os\nfrom app import create_app\napp = create_app(os.getenv('FLASK_CONFIG') or 'default')\n"},
			start: "gunicorn --bind 0.0.0.0:${PORT:-8000} --access-logfile - flasky:app"},
		{name: "a factory that needs arguments and nothing calls", files: map[string]string{"requirements.txt": "flask\n", "app/__init__.py": "from flask import Flask\n\ndef create_app(config):\n    return Flask(__name__)\n"},
			decision: "needs arguments"},
		{name: "a run.py's factory one import away", files: map[string]string{"requirements.txt": "flask\n", "run.py": "from myapp.factory import create_app\napp = create_app()\n", "myapp/factory.py": "from flask import Flask\n\ndef create_app():\n    return Flask(__name__)\n"},
			start: "gunicorn --bind 0.0.0.0:${PORT:-8000} --access-logfile - run:app"},
		{name: "an app in a script with another name", files: map[string]string{"requirements.txt": "fastapi\n", "backend.py": "from fastapi import FastAPI\napi = FastAPI()\n"},
			start: "uvicorn backend:api --host 0.0.0.0 --port ${PORT:-8000}"},
		{name: "a src layout", files: map[string]string{"pyproject.toml": "[project]\nname = \"svc\"\ndependencies = [\"fastapi\"]\n", "src/svc/__init__.py": "", "src/svc/main.py": "from fastapi import FastAPI\napp = FastAPI()\n"},
			start: "uvicorn svc.main:app --host 0.0.0.0 --port ${PORT:-8000} --app-dir src"},
		{name: "a folder whose modules import their siblings", files: map[string]string{"requirements.txt": "fastapi\n", "app/main.py": "from fastapi import FastAPI\nfrom routers import users\napp = FastAPI()\n", "app/routers/__init__.py": "", "app/routers/users.py": ""},
			start: "uvicorn main:app --host 0.0.0.0 --port ${PORT:-8000} --app-dir app"},
		{name: "a package that imports itself stays a package", files: map[string]string{"requirements.txt": "fastapi\n", "app/__init__.py": "", "app/main.py": "from fastapi import FastAPI\nfrom app.routers import users\napp = FastAPI()\n", "app/routers/__init__.py": ""},
			start: "uvicorn app.main:app --host 0.0.0.0 --port ${PORT:-8000}"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			_, candidate := detectPythonFixture(t, fixture.files)
			if candidate == nil || candidate.StartCommand != fixture.start ||
				(fixture.decision != "" && !slices.ContainsFunc(candidate.NeedsDecision, func(d string) bool { return strings.Contains(d, fixture.decision) })) {
				t.Fatalf("candidate = %+v", candidate)
			}
		})
	}
}

// Django as its layouts ship: startproject run inside the repository, split
// settings whose manage.py and wsgi.py disagree, Wagtail's development
// default, WhiteNoise with and without STATIC_ROOT, Channels.
func TestPythonDjangoLayoutsAreServedAsTheyShip(t *testing.T) {
	t.Parallel()
	nested, candidate := detectPythonFixture(t, map[string]string{
		"requirements.txt":          "Django==5.1.4\n",
		"mysite/manage.py":          "os.environ.setdefault(\"DJANGO_SETTINGS_MODULE\", \"mysite.settings\")\n",
		"mysite/mysite/__init__.py": "",
		"mysite/mysite/wsgi.py":     "application = get_wsgi_application()\n",
		"mysite/mysite/settings.py": "import os\nINSTALLED_APPS = []\nSECRET_KEY = os.environ[\"DJANGO_SECRET_KEY\"]\n",
	})
	// The project directory is the root's, not a root of its own, so what
	// its settings read is this candidate's.
	if len(nested.Candidates) != 1 || !slices.ContainsFunc(candidate.Variables, func(v DetectedVariable) bool { return v.Name == "DJANGO_SECRET_KEY" }) {
		t.Fatalf("nested roots = %+v, variables = %+v", nested.Candidates, candidate.Variables)
	}
	if candidate == nil || candidate.StartCommand != "python mysite/manage.py migrate --noinput && gunicorn --bind 0.0.0.0:${PORT:-8000} --access-logfile - --chdir mysite mysite.wsgi:application" ||
		candidate.Confidence != ConfidenceHigh || candidate.Python.Django.ManageDir != "mysite" || candidate.SchemaCommand != "" && candidate.SchemaCommand != "python mysite/manage.py migrate --noinput" {
		t.Fatalf("nested = %+v / %+v", candidate, nested.Candidates)
	}

	_, candidate = detectPythonFixture(t, map[string]string{
		"requirements/base.txt":         "django==5.1.4\ndjango-environ==0.11.2\nwhitenoise==6.8.2\n",
		"requirements/production.txt":   "-r base.txt\ngunicorn==23.0.0\n",
		"manage.py":                     "os.environ.setdefault(\"DJANGO_SETTINGS_MODULE\", \"config.settings.local\")\n",
		"config/wsgi.py":                "os.environ.setdefault(\"DJANGO_SETTINGS_MODULE\", \"config.settings.production\")\n",
		"config/settings/base.py":       "INSTALLED_APPS = ['django.contrib.staticfiles']\nMIDDLEWARE = ['whitenoise.middleware.WhiteNoiseMiddleware']\nSTATIC_ROOT = BASE_DIR / 'staticfiles'\n",
		"config/settings/local.py":      "from .base import *\nSECRET_KEY = 'dev'\n",
		"config/settings/production.py": "from .base import *\nSECRET_KEY = env('DJANGO_SECRET_KEY')\n",
	})
	if candidate == nil || !strings.Contains(candidate.StartCommand, "python manage.py collectstatic --noinput && gunicorn") ||
		candidate.Python.InstallFile != "requirements/production.txt" || candidate.Python.Django.SettingsModule != "config.settings.production" ||
		!candidate.Python.Django.Seeded {
		t.Fatalf("cookiecutter = %+v", candidate)
	}
	seeded := slices.IndexFunc(candidate.Variables, func(v DetectedVariable) bool { return v.Name == "DJANGO_SETTINGS_MODULE" })
	if seeded < 0 || candidate.Variables[seeded].Setup != "default" || candidate.Variables[seeded].DefaultValue != "config.settings.production" {
		t.Fatalf("settings module variable = %+v", candidate.Variables)
	}

	_, candidate = detectPythonFixture(t, map[string]string{
		"requirements.txt":              "wagtail==6.3\nDjango==5.1.4\n",
		"manage.py":                     "os.environ.setdefault(\"DJANGO_SETTINGS_MODULE\", \"mysite.settings.dev\")\n",
		"mysite/wsgi.py":                "os.environ.setdefault(\"DJANGO_SETTINGS_MODULE\", \"mysite.settings.dev\")\n",
		"mysite/settings/base.py":       "INSTALLED_APPS = ['django.contrib.staticfiles']\n",
		"mysite/settings/dev.py":        "from .base import *\nDEBUG = True\nSECRET_KEY = 'django-insecure-x'\n",
		"mysite/settings/production.py": "from .base import *\nDEBUG = False\n",
	})
	if candidate == nil || candidate.Python.Django.SettingsModule != "mysite.settings.dev" || !strings.Contains(candidate.Python.Django.Development, "sets no SECRET_KEY") {
		t.Fatalf("wagtail = %+v", candidate.Python.Django)
	}

	_, candidate = detectPythonFixture(t, map[string]string{
		"requirements.txt": "django==5.1\nchannels==4.2\ndaphne==4.1.2\n",
		"manage.py":        "os.environ.setdefault('DJANGO_SETTINGS_MODULE', 'chat.settings')\n",
		"chat/asgi.py":     "from channels.routing import ProtocolTypeRouter\napplication = ProtocolTypeRouter({})\n",
		"chat/wsgi.py":     "",
		"chat/settings.py": "LOGGING = {}\n",
	})
	if candidate == nil || candidate.StartCommand != "python manage.py migrate --noinput && daphne -b 0.0.0.0 -p ${PORT:-8000} chat.asgi:application" {
		t.Fatalf("channels with daphne = %+v", candidate)
	}
	_, candidate = detectPythonFixture(t, map[string]string{
		"requirements.txt": "django==5.1\nchannels==4.2\n",
		"manage.py":        "",
		"chat/asgi.py":     "application = ProtocolTypeRouter({})\n",
		"chat/wsgi.py":     "",
	})
	if candidate == nil || candidate.StartCommand != "python manage.py migrate --noinput && uvicorn chat.asgi:application --host 0.0.0.0 --port ${PORT:-8000}" {
		t.Fatalf("channels with uvicorn = %+v", candidate)
	}

	// A Procfile web process is the repository's own; the static files
	// Heroku would have collected are collected before it.
	_, candidate = detectPythonFixture(t, map[string]string{
		"requirements.txt":  "django==5.1\nwhitenoise==6.8\ngunicorn\n",
		"manage.py":         "os.environ.setdefault('DJANGO_SETTINGS_MODULE', 'site1.settings')\n",
		"site1/wsgi.py":     "",
		"site1/settings.py": "STATIC_ROOT = 'staticfiles'\n",
		"Procfile":          "web: gunicorn site1.wsgi --log-file -\n",
	})
	if candidate == nil || candidate.StartCommand != "python manage.py collectstatic --noinput && gunicorn site1.wsgi --log-file -" || candidate.Confidence != ConfidenceHigh {
		t.Fatalf("procfile = %+v", candidate)
	}
}

// A declared start is how the repository says it is served: it answers the
// framework's questions, and a site folder beside it is not what it serves.
func TestPythonDeclaredStartsOutrankFrameworkGuesses(t *testing.T) {
	t.Parallel()
	_, candidate := detectPythonFixture(t, map[string]string{
		"requirements.txt":  "fastapi\nuvicorn\n",
		"Procfile":          "web: uvicorn backend.routes:api --host 0.0.0.0 --port $PORT\n",
		"backend/routes.py": "import fastapi\napi = fastapi.FastAPI()\n",
		"static/index.html": "<h1>hi</h1>",
	})
	if candidate == nil || candidate.Framework != "fastapi" || candidate.Confidence != ConfidenceHigh || len(candidate.NeedsDecision) != 0 {
		t.Fatalf("procfile = %+v", candidate)
	}
	_, candidate = detectPythonFixture(t, map[string]string{
		"pyproject.toml": "[project]\nname = \"x\"\ndependencies = [\"litestar\"]\n\n[tool.pdm.scripts]\nstart = \"pdm run uvicorn service:app --host 0.0.0.0 --port 8000\"\n",
		"pdm.lock":       "[[package]]\nname = \"litestar\"\nversion = \"2.13.0\"\ngroups = [\"default\"]\n",
		"service.py":     "from litestar import Litestar\n\ndef build():\n    return Litestar([])\n",
	})
	if candidate == nil || candidate.StartCommand != "uvicorn service:app --host 0.0.0.0 --port 8000" || candidate.PythonInstall != "pdm.lock" ||
		!slices.ContainsFunc(candidate.Evidence, func(e DetectionEvidence) bool { return strings.Contains(e.Reason, "[tool.pdm.scripts] start") }) {
		t.Fatalf("pdm task = %+v", candidate)
	}
	_, candidate = detectPythonFixture(t, map[string]string{
		"pyproject.toml": "[project]\ndependencies = [\"fastapi\"]\n\n[tool.poe.tasks]\nserve = {sequence = [\"migrate\", \"run\"]}\n",
		"main.py":        "from fastapi import FastAPI\napp = FastAPI()\n",
	})
	if candidate == nil || candidate.StartCommand != "uvicorn main:app --host 0.0.0.0 --port ${PORT:-8000}" {
		t.Fatalf("a composite task is not a start command: %+v", candidate)
	}
}

// The manifests beyond the four marker files: requirement folders, Pipfile,
// setup files and conda environments each make a root a Python project.
func TestPythonManifestLayoutsAreDetected(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name      string
		files     map[string]string
		framework string
		install   string
		version   string
		unpinned  bool
	}{
		{name: "requirements folder with no root file", files: map[string]string{"requirements/base.txt": "Django==5.1.4\n", "requirements/production.txt": "-r base.txt\ngunicorn==23.0.0\n", "manage.py": "", "config/wsgi.py": ""},
			framework: "django", install: "requirements.txt", version: "3.13"},
		{name: "requirements-prod.txt only", files: map[string]string{"requirements-prod.txt": "flask==3.1.0\n", "requirements-dev.txt": "pytest\n", "app.py": "app = Flask(__name__)\n"},
			framework: "flask", install: "requirements.txt", version: "3.13"},
		{name: "pipenv", files: map[string]string{"Pipfile": "[packages]\nflask = \"==3.1.0\"\n\n[requires]\npython_version = \"3.11\"\n", "Pipfile.lock": `{"default": {"flask": {"version": "==3.1.0"}}}`, "app.py": "from flask import Flask\napp = Flask(__name__)\n", "templates/index.html": "<p>{{ x }}</p>"},
			framework: "flask", install: "Pipfile.lock", version: "3.11"},
		{name: "setup.py only", files: map[string]string{"setup.py": "setup(name='x', install_requires=['flask>=2', 'gunicorn'])\n", "wsgi.py": "from flask import Flask\napp = Flask(__name__)\n"},
			framework: "flask", install: "setup.py", version: "3.13", unpinned: true},
		{name: "conda environment", files: map[string]string{"environment.yml": "dependencies:\n  - python=3.11\n  - pip\n  - pip:\n    - streamlit==1.41.0\n", "streamlit_app.py": "import streamlit as st\n"},
			framework: "streamlit", install: "environment.yml", version: "3.11"},
		{name: "pyproject with extras before the framework", files: map[string]string{"pyproject.toml": "[project]\ndependencies = [\"celery[redis]>=5.4\", \"django>=5.1\", \"gunicorn\", \"psycopg[binary]\"]\n", "manage.py": "", "proj/wsgi.py": ""},
			framework: "django", install: "pyproject.toml", version: "3.13", unpinned: true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			_, candidate := detectPythonFixture(t, fixture.files)
			if candidate == nil || candidate.Framework != fixture.framework || candidate.PythonInstall != fixture.install ||
				candidate.PythonVersion != fixture.version || candidate.UnpinnedDependencies != fixture.unpinned {
				t.Fatalf("candidate = %+v", candidate)
			}
		})
	}
	// A monorepo keeps .python-version at its top.
	_, candidate := detectPythonFixture(t, map[string]string{".python-version": "3.12\n", "backend/requirements.txt": "fastapi\n", "backend/main.py": "app = FastAPI()\n"})
	if candidate == nil || candidate.PythonVersion != "3.12" || candidate.Python.VersionSource != ".python-version 3.12" ||
		!slices.ContainsFunc(candidate.Evidence, func(e DetectionEvidence) bool { return e.Path == ".python-version" }) {
		t.Fatalf("monorepo version = %+v", candidate)
	}
}

func TestPythonProjectFactsAreRecordedForPreflight(t *testing.T) {
	t.Parallel()
	_, candidate := detectPythonFixture(t, map[string]string{
		"requirements.txt": "--extra-index-url https://pypi.company.com/simple\nfastapi==0.115.6\npsycopg2==2.9.10\nlib @ git+ssh://git@github.com/org/lib.git\npywin32==306\n",
		"main.py":          "from fastapi import FastAPI\napp = FastAPI()\n",
	})
	python := candidate.Python
	if !slices.Equal(python.PrivateIndexes, []string{"pypi.company.com"}) || !slices.Equal(python.GitSSH, []string{"lib"}) ||
		!slices.Equal(python.LocalArtifacts, []string{"pywin32==306"}) || len(python.InlineCredentials) != 0 {
		t.Fatalf("facts = %+v", python)
	}
	packages := []string{}
	for _, pkg := range candidate.SystemPackages {
		if !pkg.Automatic {
			t.Fatalf("a dependency's package is installed by the recipe itself: %+v", pkg)
		}
		packages = append(packages, pkg.Name)
	}
	if !slices.Equal(packages, []string{"gcc", "libc6-dev", "libpq-dev", "git", "openssh-client"}) {
		t.Fatalf("system packages = %v", packages)
	}
	if !slices.ContainsFunc(candidate.Variables, func(v DetectedVariable) bool { return v.Name == "PIP_EXTRA_INDEX_URL" && v.Step == "install" }) {
		t.Fatalf("variables = %+v", candidate.Variables)
	}

	_, candidate = detectPythonFixture(t, map[string]string{
		"pyproject.toml": "[project]\ndependencies = [\"fastapi\"]\n\n[tool.poetry]\n\n[[tool.poetry.source]]\nname = \"private-repo\"\nurl = \"https://pypi.example.com/simple\"\n",
		"main.py":        "app = FastAPI()\n",
	})
	names := []string{}
	for _, variable := range candidate.Variables {
		names = append(names, variable.Name)
	}
	if !slices.Contains(names, "POETRY_HTTP_BASIC_PRIVATE_REPO_USERNAME") || !slices.Contains(names, "POETRY_HTTP_BASIC_PRIVATE_REPO_PASSWORD") {
		t.Fatalf("poetry source variables = %v", names)
	}

	// An Aptfile's packages are seeded into the plan under trixie's names.
	_, candidate = detectPythonFixture(t, map[string]string{"requirements.txt": "flask\n", "app.py": "app = Flask(__name__)\n", "Aptfile": "libgl1-mesa-glx\nlibmagic1\n"})
	seeded := []string{}
	for _, pkg := range candidate.SystemPackages {
		if !pkg.Automatic {
			seeded = append(seeded, pkg.Name)
		}
	}
	if !slices.Equal(seeded, []string{"libgl1", "libmagic1t64"}) {
		t.Fatalf("Aptfile packages = %+v", candidate.SystemPackages)
	}

	// st.secrets is written from the variables at start; a table is not.
	_, candidate = detectPythonFixture(t, map[string]string{
		"requirements.txt": "streamlit\n",
		"app.py":           "import streamlit as st\nkey = st.secrets[\"OPENAI_API_KEY\"]\nurl = st.secrets.get(\"API_URL\")\ndb = st.secrets[\"connections\"][\"db\"]\n",
	})
	if !slices.Equal(candidate.Python.StreamlitSecrets, []string{"API_URL", "OPENAI_API_KEY"}) || !slices.Equal(candidate.Python.StreamlitNested, []string{"connections"}) ||
		!strings.HasPrefix(candidate.StartCommand, "python -c 'import json,os;") || !strings.Contains(candidate.StartCommand, `("API_URL","OPENAI_API_KEY",)`) ||
		!strings.HasSuffix(candidate.StartCommand, "&& streamlit run app.py --server.port ${PORT:-8501} --server.address 0.0.0.0 --server.headless true") {
		t.Fatalf("streamlit = %q %+v", candidate.StartCommand, candidate.Python)
	}
	if err := rejectPlanSecretLiteral("start command", candidate.StartCommand); err != nil || secretCommandFlagRE.MatchString(candidate.StartCommand) {
		t.Fatalf("the secrets step is a valid start command: %v", err)
	}
	_, candidate = detectPythonFixture(t, map[string]string{
		"requirements.txt":        "streamlit\n",
		"app.py":                  "import streamlit as st\nkey = st.secrets[\"OPENAI_API_KEY\"]\n",
		".streamlit/secrets.toml": "OPENAI_API_KEY = \"\"\n",
	})
	if strings.HasPrefix(candidate.StartCommand, "python -c") {
		t.Fatalf("a committed secrets.toml is the application's own: %q", candidate.StartCommand)
	}

	// A uv workspace member installs from the workspace; its root is not the application.
	result, candidate := detectPythonFixture(t, map[string]string{
		"pyproject.toml":              "[tool.uv.workspace]\nmembers = [\"packages/*\"]\n",
		"uv.lock":                     "version = 1\n",
		"packages/api/pyproject.toml": "[project]\nname = \"api\"\ndependencies = [\"fastapi\", \"shared\"]\n[tool.uv.sources]\nshared = { workspace = true }\n",
		"packages/api/api/main.py":    "from fastapi import FastAPI\napp = FastAPI()\n",
	})
	if candidate == nil || candidate.Root != "packages/api" || candidate.Python.Workspace != "." || candidate.PythonInstall != "uv.lock" || candidate.RecipeIssue != "" {
		t.Fatalf("workspace member = %+v", candidate)
	}
	for _, other := range result.Candidates {
		if other.Root == "" && other.Demotion == "" {
			t.Fatalf("the workspace root is offered as an application: %+v", other)
		}
	}
}

// Django answers only the hosts ALLOWED_HOSTS names and trusts only the
// origins CSRF_TRUSTED_ORIGINS names; each is bound to the planned domain in
// the separator the settings split it on, whichever idiom reads it.
func TestPythonDjangoHostsAndOriginsFollowThePlannedDomain(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name     string
		settings string
		want     map[string]string
	}{
		{name: "django-environ", settings: "import environ\nenv = environ.Env()\nALLOWED_HOSTS = env.list('DJANGO_ALLOWED_HOSTS', default=['example.com'])\nCSRF_TRUSTED_ORIGINS = env.list('DJANGO_CSRF_TRUSTED_ORIGINS', default=[])\n",
			want: map[string]string{"DJANGO_ALLOWED_HOSTS": "{{hostname}},localhost,127.0.0.1", "DJANGO_CSRF_TRUSTED_ORIGINS": "{{scheme}}://{{hostname}}"}},
		{name: "python-decouple", settings: "from decouple import config, Csv\nALLOWED_HOSTS = config('ALLOWED_HOSTS', cast=Csv())\nCSRF_TRUSTED_ORIGINS = config('CSRF_TRUSTED_ORIGINS', cast=Csv(), default='')\n",
			want: map[string]string{"ALLOWED_HOSTS": "{{hostname}},localhost,127.0.0.1", "CSRF_TRUSTED_ORIGINS": "{{scheme}}://{{hostname}}"}},
		{name: "a space-separated variable", settings: "import os\nALLOWED_HOSTS = os.environ.get('ALLOWED_HOSTS', '').split(' ')\n",
			want: map[string]string{"ALLOWED_HOSTS": "{{hostname}} localhost 127.0.0.1"}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			_, candidate := detectPythonFixture(t, map[string]string{
				"requirements.txt": "django==5.1\n", "manage.py": "", "site1/wsgi.py": "", "site1/settings.py": fixture.settings,
			})
			for name, template := range fixture.want {
				index := slices.IndexFunc(candidate.Variables, func(v DetectedVariable) bool { return v.Name == name })
				if index < 0 || candidate.Variables[index].Setup != "domain" || candidate.Variables[index].DomainTemplate != template {
					t.Fatalf("%s = %+v", name, candidate.Variables)
				}
			}
			if candidate.Python.Django.SettingsModule != "site1.settings" {
				t.Fatalf("settings = %+v", candidate.Python.Django)
			}
		})
	}
}

// The facts quote the checkout: a path a shell would split stays out of the
// proposed start command, and a line that carries credential material stays
// out of the saved detection rather than making it unsavable.
func TestPythonFactsKeepOnlyWhatCanBeRunAndSaved(t *testing.T) {
	t.Parallel()
	_, candidate := detectPythonFixture(t, map[string]string{"requirements.txt": "streamlit\n", "my app.py": "import streamlit as st\nst.title('x')\n"})
	if candidate == nil || strings.Contains(candidate.StartCommand, "my app") {
		t.Fatalf("candidate = %+v", candidate)
	}
	candidate = &DetectedCandidate{Recipe: "python", Python: &DetectedPython{
		LocalArtifacts: []string{"lib @ https://build:hunter2@ci.example.com/lib.whl", "pywin32==306"},
		Modules:        []string{"api_token=abc", "main"},
		WheelBlockers:  map[string][]string{"3.14": {"numpy==2.1.3", "x\ny"}},
		VersionSource:  "runtime.txt password=x",
		Django:         &DetectedDjango{ManageDir: "../escape", SettingsModule: "site.settings"},
	}}
	keepValidPythonFacts(candidate)
	python := candidate.Python
	if err := validateDetectedPython(*candidate); err != nil || !slices.Equal(python.LocalArtifacts, []string{"pywin32==306"}) ||
		!slices.Equal(python.Modules, []string{"main"}) || !slices.Equal(python.WheelBlockers["3.14"], []string{"numpy==2.1.3"}) ||
		python.VersionSource != "" || python.Django != nil {
		t.Fatalf("kept %+v (%v)", python, err)
	}
}
