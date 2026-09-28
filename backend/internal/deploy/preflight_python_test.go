package deploy

import (
	"strings"
	"testing"
)

// What detection read about a Python root becomes findings before Deploy,
// judged against the plan's own settings.
func TestPythonPreflightFindingsComeFromTheSource(t *testing.T) {
	t.Parallel()
	base := func() *DetectedCandidate {
		return &DetectedCandidate{Recipe: "python", Framework: "fastapi", PythonVersion: "3.13", StartCommand: "uvicorn main:app", Python: &DetectedPython{}}
	}
	build := BuildPlanConfig{Method: BuildRecipe, Recipe: "python", StartCommand: "uvicorn main:app --host 0.0.0.0"}
	for _, fixture := range []struct {
		name     string
		mutate   func(*DetectedCandidate, *BuildPlanConfig)
		code     string
		severity PreflightSeverity
		measured string
		action   string
	}{
		{name: "system packages", code: "python_system_packages", severity: PreflightPass, measured: "libpq-dev (psycopg2 builds against libpq); libgl1 (added in Build settings)",
			mutate: func(c *DetectedCandidate, b *BuildPlanConfig) {
				c.SystemPackages = []DetectedSystemPackage{{Name: "libpq-dev", Reason: "psycopg2 builds against libpq", Automatic: true}}
				b.SystemPackages = []string{"libgl1"}
			}},
		{name: "a pin without wheels for the chosen family", code: "python_version_wheels_missing", severity: PreflightWarning, measured: "numpy==1.26.4 on Python 3.13", action: "Choose Python 3.12",
			mutate: func(c *DetectedCandidate, b *BuildPlanConfig) {
				c.Python.WheelBlockers = map[string][]string{"3.13": {"numpy==1.26.4"}, "3.14": {"numpy==1.26.4"}}
				b.PythonVersion = "3.13"
			}},
		{name: "a pin no family has wheels for", code: "python_native_build_unmapped", severity: PreflightWarning, action: "build-essential",
			mutate: func(c *DetectedCandidate, b *BuildPlanConfig) {
				c.Python.WheelBlockers = map[string][]string{"3.10": {"x==1"}, "3.11": {"x==1"}, "3.12": {"x==1"}, "3.13": {"x==1"}, "3.14": {"x==1"}}
			}},
		{name: "a raised version", code: "python_version_raised", severity: PreflightWarning, measured: "Python 3.9 from runtime.txt 3.9.19; building on 3.10",
			mutate: func(c *DetectedCandidate, b *BuildPlanConfig) {
				c.PythonVersion, c.Python.VersionRaised, c.Python.VersionSource = "3.10", "3.9", "runtime.txt 3.9.19"
			}},
		{name: "a stale lock", code: "python_lock_stale", severity: PreflightWarning, measured: "missing: httpx", action: "`uv lock`",
			mutate: func(c *DetectedCandidate, b *BuildPlanConfig) {
				c.Python.Lock = &DetectedLockfile{Path: "uv.lock", Manager: "uv", State: LockfileStale, Missing: []string{"httpx"}}
			}},
		{name: "a stale Pipfile.lock", code: "python_lock_stale", severity: PreflightWarning, action: "`pipenv lock`",
			mutate: func(c *DetectedCandidate, b *BuildPlanConfig) {
				c.Python.Lock = &DetectedLockfile{Path: "Pipfile.lock", Manager: "pipenv", State: LockfileStale, Missing: []string{"requests"}}
			}},
		{name: "manifests that disagree", code: "python_manifests_disagree", severity: PreflightWarning, measured: "omits sqlalchemy, httpx",
			mutate: func(c *DetectedCandidate, b *BuildPlanConfig) {
				c.Python.InstallFile, c.Python.ManifestConflict = "pyproject.toml", []string{"sqlalchemy", "httpx"}
			}},
		{name: "no dependencies for a framework the code imports", code: "python_dependencies_empty", severity: PreflightBlocked,
			mutate: func(c *DetectedCandidate, b *BuildPlanConfig) { c.Python.DependenciesEmpty = "blocked" }},
		{name: "no dependencies at all", code: "python_dependencies_empty", severity: PreflightWarning,
			mutate: func(c *DetectedCandidate, b *BuildPlanConfig) { c.Python.DependenciesEmpty = "warning" }},
		{name: "freeze leftovers", code: "python_requirements_local_artifacts", severity: PreflightWarning, measured: "pywin32==306",
			mutate: func(c *DetectedCandidate, b *BuildPlanConfig) { c.Python.LocalArtifacts = []string{"pywin32==306"} }},
		{name: "a developer's own package", code: "python_requirements_local_paths", severity: PreflightWarning, measured: "mylib @ file:///Users/me/code/mylib", action: "publish it",
			mutate: func(c *DetectedCandidate, b *BuildPlanConfig) {
				c.Python.LocalPaths = []string{"mylib @ file:///Users/me/code/mylib"}
			}},
		{name: "pins that chose an older Python, with the setting the form seeded from it", code: "python_version_limited", severity: PreflightPass, measured: "numpy==1.26.4 publish no wheels for Python 3.13",
			mutate: func(c *DetectedCandidate, b *BuildPlanConfig) {
				c.PythonVersion, c.Python.VersionLimited = "3.12", []string{"numpy==1.26.4"}
				b.PythonVersion = "3.12"
			}},
		{name: "a converted conda environment", code: "conda_converted", severity: PreflightWarning, measured: "pandas==2.1.*",
			mutate: func(c *DetectedCandidate, b *BuildPlanConfig) { c.Python.CondaConverted = []string{"pandas==2.1.*"} }},
		{name: "a private index", code: "python_private_index", severity: PreflightWarning, measured: "pypi.company.com",
			mutate: func(c *DetectedCandidate, b *BuildPlanConfig) { c.Python.PrivateIndexes = []string{"pypi.company.com"} }},
		{name: "a committed credential", code: "credential_in_manifest", severity: PreflightWarning, measured: "requirements.txt",
			mutate: func(c *DetectedCandidate, b *BuildPlanConfig) {
				c.Python.InlineCredentials = []string{"requirements.txt"}
			}},
		{name: "an SSH dependency", code: "python_private_git_dependency", severity: PreflightDecision, measured: "lib",
			mutate: func(c *DetectedCandidate, b *BuildPlanConfig) { c.Python.GitSSH = []string{"lib"} }},
		{name: "CPU torch", code: "python_cpu_torch_selected", severity: PreflightPass,
			mutate: func(c *DetectedCandidate, b *BuildPlanConfig) { c.Python.CPUTorch = true }},
		{name: "a lock that pins CUDA", code: "python_gpu_wheels", severity: PreflightWarning, measured: "nvidia-cudnn-cu12",
			mutate: func(c *DetectedCandidate, b *BuildPlanConfig) { c.Python.GPUWheels = []string{"nvidia-cudnn-cu12"} }},
		{name: "a start module that is not in the source", code: "start_module_unresolved", severity: PreflightWarning, measured: "uvicorn imports server.api",
			mutate: func(c *DetectedCandidate, b *BuildPlanConfig) {
				c.Python.Modules = []string{"app", "main"}
				b.StartCommand = "uvicorn server.api:app --host 0.0.0.0"
			}},
		{name: "st.secrets tables", code: "streamlit_secrets_nested", severity: PreflightWarning, measured: "connections",
			mutate: func(c *DetectedCandidate, b *BuildPlanConfig) { c.Python.StreamlitNested = []string{"connections"} }},
		{name: "gradio on localhost", code: "gradio_bind_loopback", severity: PreflightWarning, measured: "app.py",
			mutate: func(c *DetectedCandidate, b *BuildPlanConfig) { c.Python.GradioLoopback = "app.py" }},
		{name: "websockets without a library", code: "python_websocket_library_missing", severity: PreflightWarning, measured: "main.py",
			mutate: func(c *DetectedCandidate, b *BuildPlanConfig) { c.Python.WebsocketLibraryMissing = "main.py" }},
		{name: "WhiteNoise without STATIC_ROOT", code: "django_static_root_missing", severity: PreflightWarning,
			mutate: func(c *DetectedCandidate, b *BuildPlanConfig) {
				c.Framework, c.Python.Django = "django", &DetectedDjango{SettingsModule: "site.settings", WhiteNoise: true, StaticFiles: true, Logging: true}
			}},
		{name: "static files nobody serves", code: "django_static_unserved", severity: PreflightWarning,
			mutate: func(c *DetectedCandidate, b *BuildPlanConfig) {
				c.Framework, c.Python.Django = "django", &DetectedDjango{SettingsModule: "site.settings", StaticFiles: true, StaticRoot: true, Logging: true}
			}},
		{name: "development settings", code: "django_development_settings", severity: PreflightWarning, measured: "mysite.settings.dev is a development module",
			mutate: func(c *DetectedCandidate, b *BuildPlanConfig) {
				c.Framework, c.Python.Django = "django", &DetectedDjango{SettingsModule: "mysite.settings.dev", Development: "mysite.settings.dev is a development module", Logging: true}
			}},
		{name: "errors that never reach the log", code: "django_errors_unlogged", severity: PreflightWarning, action: "StreamHandler",
			mutate: func(c *DetectedCandidate, b *BuildPlanConfig) {
				c.Framework, c.Python.Django = "django", &DetectedDjango{SettingsModule: "site.settings"}
			}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			candidate, plan := base(), build
			fixture.mutate(candidate, &plan)
			item := findingByCode(plannedRecipeFindings(candidate, plan), fixture.code)
			if item == nil || item.Severity != fixture.severity || !strings.Contains(item.Measured, fixture.measured) || !strings.Contains(item.Action, fixture.action) {
				t.Fatalf("%s = %#v", fixture.code, item)
			}
			if len(item.Measured) > 512 || len(item.Action) > 512 || len(item.Means) > 512 {
				t.Fatalf("%s is past the finding bounds: %#v", fixture.code, item)
			}
		})
	}
	// A plain candidate has nothing to say.
	for _, item := range plannedRecipeFindings(base(), build) {
		if item.Code != "start_command_missing" {
			t.Fatalf("a clean candidate raised %#v", item)
		}
	}
	// The start module is judged only where it can be: a directory added to
	// the path, or a module the source has, says nothing.
	candidate := base()
	candidate.Python.Modules = []string{"main"}
	for _, start := range []string{"uvicorn svc.main:app --app-dir src", "uvicorn main:app", "cd web && gunicorn app:app", "streamlit run x.py"} {
		plan := build
		plan.StartCommand = start
		if item := findingByCode(plannedRecipeFindings(candidate, plan), "start_module_unresolved"); item != nil {
			t.Fatalf("%q raised %#v", start, item)
		}
	}
}

// The platform's Aptfile warning is answered once the plan carries the
// packages under trixie's names.
func TestPlatformSystemPackagesArePlannedForThePythonRecipe(t *testing.T) {
	t.Parallel()
	build := BuildPlanConfig{Method: BuildRecipe, Recipe: "python", SystemPackages: []string{"libgl1", "ffmpeg"}}
	if !platformPackagesPlanned([]string{"libgl1-mesa-glx", "ffmpeg"}, build) {
		t.Fatal("renamed packages were not matched")
	}
	if platformPackagesPlanned([]string{"libgl1", "tesseract-ocr"}, build) {
		t.Fatal("a package the plan lacks was matched")
	}
	build.Recipe = "node"
	if platformPackagesPlanned([]string{"ffmpeg"}, build) {
		t.Fatal("only the Python recipe installs system packages")
	}
}

func TestPlanValidationBoundsSystemPackages(t *testing.T) {
	t.Parallel()
	valid := func(build BuildPlanConfig) error {
		return PlanConfiguration{Build: build, Runtime: RuntimePlanConfig{Strategy: StrategyStopFirst}}.Validate()
	}
	if err := valid(BuildPlanConfig{Method: BuildRecipe, Recipe: "python", SystemPackages: []string{"libpq-dev", "g++"}}); err != nil {
		t.Fatal(err)
	}
	for _, build := range []BuildPlanConfig{
		{Method: BuildRecipe, Recipe: "python", SystemPackages: []string{"libpq-dev; rm -rf /"}},
		{Method: BuildRecipe, Recipe: "python", SystemPackages: []string{"LibPQ"}},
		{Method: BuildRecipe, Recipe: "node", SystemPackages: []string{"ffmpeg"}},
		{Method: BuildDockerfile, SystemPackages: []string{"ffmpeg"}},
		{Method: BuildRecipe, Recipe: "python", SystemPackages: make([]string, 33)},
	} {
		if err := valid(build); err == nil {
			t.Fatalf("%+v was accepted", build.SystemPackages)
		}
	}
	if err := valid(BuildPlanConfig{Method: BuildRecipe, Recipe: "python", PythonVersion: "3.14"}); err != nil {
		t.Fatalf("3.14: %v", err)
	}
}
