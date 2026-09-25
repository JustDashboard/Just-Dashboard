package deploy

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// pythonRecipeFindings judge a Python recipe plan against what detection
// read about the source (DetectedPython): what the build will install and
// how, what it cannot, and what the application will do once it runs.
func pythonRecipeFindings(candidate *DetectedCandidate, build BuildPlanConfig) []PreflightFinding {
	python := candidate.Python
	if python == nil {
		return nil
	}
	findings := []PreflightFinding{}
	version := firstNonEmpty(build.PythonVersion, candidate.PythonVersion, defaultPythonRecipeVersion)
	if packages := plannedSystemPackages(candidate, build); len(packages) > 0 {
		findings = append(findings, finding("python_system_packages", PreflightPass,
			"System packages are installed in the image", boundedText(strings.Join(packages, "; "), 512),
			"The recipe installs them from Debian before the dependencies, so the packages that build or load against them find them.",
			"", "deploy", "configuration.build.systemPackages"))
	}
	findings = append(findings, pythonVersionWheelFindings(candidate, python, build, version)...)
	if python.VersionRaised != "" && (build.PythonVersion == "" || build.PythonVersion == candidate.PythonVersion) && candidate.PythonVersion == "3.10" {
		findings = append(findings, finding("python_version_raised", PreflightWarning,
			"The source pins a Python the recipe does not carry", "Python "+python.VersionRaised+" from "+python.VersionSource+"; building on 3.10",
			"Code written for "+python.VersionRaised+" almost always runs on 3.10, and every pinned package publishes wheels for it; the rare module 3.10 removed is the exception.",
			"Deploy on 3.10, or update the version file to 3.10 or newer.", "deploy", "configuration.build.pythonVersion"))
	}
	if lock := python.Lock; lock != nil && lock.State == LockfileStale {
		manifest := "pyproject.toml"
		regenerate := map[string]string{"uv": "uv lock", "poetry": "poetry lock", "pdm": "pdm lock", "pipenv": "pipenv lock"}[lock.Manager]
		if lock.Manager == "pipenv" {
			manifest = "Pipfile"
		}
		findings = append(findings, finding("python_lock_stale", PreflightWarning,
			lock.Path+" is older than "+manifest, boundedText(lockDriftMeasured(lock), 512),
			"The lock does not install what "+manifest+" now declares, so the build resolves the dependencies again from the index: it works, but it is not the set the lock pins.",
			"Run `"+regenerate+"` and commit "+lock.Path+".", "deploy", "configuration.build"))
	}
	if len(python.ManifestConflict) > 0 {
		findings = append(findings, finding("python_manifests_disagree", PreflightWarning,
			"requirements.txt and pyproject.toml disagree", boundedText(python.InstallFile+" installs; the requirement file omits "+strings.Join(python.ManifestConflict, ", "), 512),
			"The requirement file leaves out dependencies pyproject.toml declares, so the build installs from pyproject.toml, without the requirement file's pins.",
			"Regenerate the requirement file from pyproject.toml (pip-compile, uv export), or delete the one that is out of date.", "deploy", "configuration.build"))
	}
	switch python.DependenciesEmpty {
	case "blocked":
		findings = append(findings, finding("python_dependencies_empty", PreflightBlocked,
			"The project declares no dependencies", "no lock, requirement file or dependency list; the source imports a framework",
			"The build installs nothing, so the start command fails with ModuleNotFoundError on the first import.",
			"Add requirements.txt, or [project].dependencies in pyproject.toml.", "deploy", "configuration.build"))
	case "warning":
		findings = append(findings, finding("python_dependencies_empty", PreflightWarning,
			"The project declares no dependencies", "pyproject.toml configures tools but lists no dependencies",
			"The build installs nothing; that is right only for a program that uses the standard library alone.",
			"Add requirements.txt, or [project].dependencies in pyproject.toml.", "deploy", "configuration.build"))
	}
	if len(python.LocalArtifacts) > 0 {
		findings = append(findings, finding("python_requirements_local_artifacts", PreflightWarning,
			"Requirements were frozen from a local environment", boundedText(strings.Join(python.LocalArtifacts, "; "), 512),
			"These were captured from a conda, Windows or macOS environment and cannot install on Linux as written: the build installs a conda build's package by name from PyPI and leaves another system's packages out.",
			"Regenerate the file with `pip list --format=freeze` in a virtual environment, or with pip-compile.", "deploy", "configuration.build"))
	}
	if len(python.LocalPaths) > 0 {
		findings = append(findings, finding("python_requirements_local_paths", PreflightWarning,
			"Requirements point at a directory on another machine", boundedText(strings.Join(python.LocalPaths, "; "), 512),
			"The path exists only where the file was written, and installing the name from a package index instead could fetch an unrelated package published under it, so the build leaves these out; if the application imports one, it stops with ModuleNotFoundError.",
			"Commit the package into the repository and require it by a relative path (./libs/name), or publish it to a package index the build can read.", "deploy", "configuration.build"))
	}
	if len(python.CondaConverted) > 0 {
		findings = append(findings, finding("conda_converted", PreflightWarning,
			"The conda environment is installed with pip", boundedText(strings.Join(python.CondaConverted, ", "), 512),
			"The recipe has no conda: each conda package is installed from PyPI under the name that provides it, and conda's own libraries come with the Python image.",
			"Check the versions resolve on PyPI, or commit a requirements.txt.", "deploy", "configuration.build"))
	}
	if len(python.PrivateIndexes) > 0 {
		findings = append(findings, finding("python_private_index", PreflightWarning,
			"Dependencies come from a private package index", boundedText(strings.Join(python.PrivateIndexes, ", "), 512),
			"If the index asks for credentials, the install fails with 401 or \"No matching distribution\" until the build has them.",
			"Give the credential variables listed for install (the index address with its credentials, or the username and password) a value, scoped to the install only.", "deploy", "variables"))
	}
	if len(python.InlineCredentials) > 0 {
		findings = append(findings, finding("credential_in_manifest", PreflightWarning,
			"A committed file carries a package index credential", boundedText(strings.Join(python.InlineCredentials, ", "), 512),
			"Anyone who can read the repository can use the credential, and it reaches the build log of every run.",
			"Replace it with ${VARIABLE} in the file and set the variable for the install, then revoke the committed credential.", "deploy", "configuration.build"))
	}
	if len(python.GitSSH) > 0 {
		findings = append(findings, finding("python_private_git_dependency", PreflightDecision,
			"A dependency is cloned over SSH", boundedText(strings.Join(python.GitSSH, ", "), 512),
			"The build has no SSH key, so git cannot clone it and the install fails.",
			"Use an https URL with a token read from an install variable (git+https://${GIT_TOKEN}@host/org/repo), or publish the package to an index.", "deploy", "configuration.build"))
	}
	if python.CPUTorch {
		findings = append(findings, finding("python_cpu_torch_selected", PreflightPass,
			"PyTorch is installed as its CPU build", pythonCPUTorchIndex,
			"A deployment container is given no GPU, and PyPI's Linux torch brings several gigabytes of CUDA libraries it could never use.",
			"", "deploy", "configuration.build"))
	}
	if len(python.GPUWheels) > 0 {
		findings = append(findings, finding("python_gpu_wheels", PreflightWarning,
			"The lock installs CUDA libraries", boundedText(strings.Join(python.GPUWheels, ", "), 512),
			"A deployment container is given no GPU; these libraries take gigabytes of disk and every build downloads them.",
			"Lock torch from PyTorch's CPU index (tool.uv.sources, or a Poetry source), or keep the libraries if the disk allows.", "deploy", "configuration.build"))
	}
	if python.Workspace != "" {
		findings = append(findings, finding("python_workspace_member", PreflightPass,
			"Installed from its uv workspace", "workspace at "+python.Workspace,
			"The build context is the workspace, and uv installs this member with the sibling packages it depends on from the workspace's lock.",
			"", "deploy", "configuration.build.rootDirectory"))
	}
	findings = append(findings, pythonStartModuleFindings(candidate, build)...)
	if len(python.StreamlitNested) > 0 {
		findings = append(findings, finding("streamlit_secrets_nested", PreflightWarning,
			"Some st.secrets tables cannot come from variables", boundedText(strings.Join(python.StreamlitNested, ", "), 512),
			"The start command writes top-level keys into .streamlit/secrets.toml; a table such as st.secrets[\"connections\"] is not a variable.",
			"Read those settings from environment variables in the code, or commit a secrets.toml template without values.", "deploy", "variables"))
	}
	if python.GradioLoopback != "" {
		findings = append(findings, finding("gradio_bind_loopback", PreflightWarning,
			"Gradio is launched on localhost", python.GradioLoopback+" passes server_name=\"127.0.0.1\"",
			"An explicit server_name wins over GRADIO_SERVER_NAME, so nothing outside the container reaches the application.",
			"Remove server_name from launch(), or set it to \"0.0.0.0\".", "deploy", "configuration.build.startCommand"))
	}
	if python.Assets != "" {
		findings = append(findings, finding("python_assets_built", PreflightPass,
			"Front-end assets are built in the image", joinRoot(python.Assets, "package.json")+" build script",
			"A Node stage installs and builds the package before the Python image copies the result.", "", "deploy", "configuration.build.packageManager"))
	}
	findings = append(findings, djangoFindings(candidate, build)...)
	if pythonUsesWebsockets(candidate, build) {
		findings = append(findings, finding("python_websocket_library_missing", PreflightWarning,
			"uvicorn has no websocket library", candidate.Python.WebsocketLibraryMissing+" serves websockets; uvicorn is declared without [standard], websockets or wsproto",
			"uvicorn refuses every websocket upgrade (\"No supported WebSocket library detected\"), so the application's websocket routes fail.",
			"Depend on uvicorn[standard], or add websockets.", "deploy", "configuration.build"))
	}
	return findings
}

// plannedSystemPackages lists what the image will install, each with why.
func plannedSystemPackages(candidate *DetectedCandidate, build BuildPlanConfig) []string {
	var packages []string
	seen := map[string]bool{}
	for _, pkg := range candidate.SystemPackages {
		if pkg.Automatic {
			seen[pkg.Name] = true
			packages = append(packages, pkg.Name+" ("+pkg.Reason+")")
		}
	}
	for _, name := range build.SystemPackages {
		if seen[name] {
			continue
		}
		seen[name] = true
		reason := "added in Build settings"
		for _, pkg := range candidate.SystemPackages {
			if pkg.Name == name {
				reason = pkg.Reason
			}
		}
		packages = append(packages, name+" ("+reason+")")
	}
	return packages
}

func lockDriftMeasured(lock *DetectedLockfile) string {
	parts := []string{}
	if len(lock.Missing) > 0 {
		parts = append(parts, "missing: "+strings.Join(lock.Missing, ", "))
	}
	if len(lock.Changed) > 0 {
		parts = append(parts, "changed: "+strings.Join(lock.Changed, ", "))
	}
	return strings.Join(parts, "; ")
}

// pythonVersionWheelFindings say which pins cannot install on the family the
// build uses, and which family they can.
func pythonVersionWheelFindings(candidate *DetectedCandidate, python *DetectedPython, build BuildPlanConfig, version string) []PreflightFinding {
	blockers := python.WheelBlockers[version]
	findings := []PreflightFinding{}
	// The form seeds the setting from detection: equal to it, the choice is
	// still the one the pins made.
	if len(python.VersionLimited) > 0 && (build.PythonVersion == "" || build.PythonVersion == candidate.PythonVersion) {
		findings = append(findings, finding("python_version_limited", PreflightPass,
			"Python "+version+" is chosen for the pinned packages", boundedText(strings.Join(python.VersionLimited, ", ")+" publish no wheels for Python "+defaultPythonRecipeVersion, 512),
			"A newer Python would build these packages from source, which needs a compiler the image does not have.", "", "deploy", "configuration.build.pythonVersion"))
	}
	if len(blockers) == 0 {
		return findings
	}
	best := ""
	for index := len(pythonRecipeVersions) - 1; index >= 0; index-- {
		family := pythonRecipeVersions[index]
		if len(python.WheelBlockers[family]) == 0 && (candidate.PythonRequires == "" || pythonVersionSatisfies(family, candidate.PythonRequires)) &&
			pythonMinor(family) <= pythonMinor(defaultPythonRecipeVersion) {
			best = family
			break
		}
	}
	measured := boundedText(strings.Join(blockers, ", ")+" on Python "+version, 512)
	if best != "" {
		return append(findings, finding("python_version_wheels_missing", PreflightWarning,
			"Pinned packages publish no wheels for Python "+version, measured,
			"pip or uv builds them from source, which needs a compiler (and for some, Rust) the image does not have, so the install fails.",
			"Choose Python "+best+" in Build settings, or upgrade the pins.", "deploy", "configuration.build.pythonVersion"))
	}
	return append(findings, finding("python_native_build_unmapped", PreflightWarning,
		"A pinned package has no prebuilt wheel for Python "+version+" and needs a compiler", measured,
		"No Python the recipe builds on has a wheel for every pin, so the install compiles them from source.",
		"Add build-essential (and the library the package builds against) to the system packages, or upgrade the pins.", "deploy", "configuration.build.systemPackages"))
}

var pythonStartModuleRE = regexp.MustCompile(`^(?:uvicorn|gunicorn|hypercorn|daphne)\b`)

// pythonStartModuleFindings warn when the module a start command's server
// imports is none of the root's importable names: the server then stops
// with ModuleNotFoundError.
func pythonStartModuleFindings(candidate *DetectedCandidate, build BuildPlanConfig) []PreflightFinding {
	python := candidate.Python
	if len(python.Modules) == 0 {
		return nil
	}
	for _, segment := range strings.Split(build.StartCommand, "&&") {
		fields := strings.Fields(strings.TrimSpace(segment))
		if len(fields) > 0 && fields[0] == "cd" {
			// What follows runs somewhere the module names do not describe.
			return nil
		}
		if len(fields) == 0 || !pythonStartModuleRE.MatchString(fields[0]) {
			continue
		}
		pathDirectories := false
		module := ""
		for index := 1; index < len(fields); index++ {
			field := strings.Trim(fields[index], `'"`)
			switch {
			case field == "--app-dir" || field == "--pythonpath" || field == "--chdir" || strings.HasPrefix(field, "--app-dir=") ||
				strings.HasPrefix(field, "--pythonpath=") || strings.HasPrefix(field, "--chdir="):
				pathDirectories = true
			case strings.HasPrefix(field, "-"):
			case strings.Contains(field, ":") && !strings.Contains(field, "/") && !strings.Contains(field, "$"):
				module = strings.SplitN(field, ":", 2)[0]
			}
		}
		if module == "" || pathDirectories {
			continue
		}
		top := strings.SplitN(module, ".", 2)[0]
		if !pythonModuleRE.MatchString(module) || slices.Contains(python.Modules, top) {
			continue
		}
		return []PreflightFinding{finding("start_module_unresolved", PreflightWarning,
			"The start command's module is not in the source", fmt.Sprintf("%s imports %s; the root's modules are %s", fields[0], module, strings.Join(boundedNames(python.Modules), ", ")),
			"The server stops with ModuleNotFoundError unless the module comes from an installed package.",
			"Point the start command at the module that defines the application, or add --app-dir (uvicorn) or --pythonpath (gunicorn) for its directory.",
			"deploy", "configuration.build.startCommand")}
	}
	return nil
}

// pythonUsesWebsockets says the application serves websocket routes through
// a uvicorn that has no websocket library.
func pythonUsesWebsockets(candidate *DetectedCandidate, build BuildPlanConfig) bool {
	return candidate.Python.WebsocketLibraryMissing != "" && strings.Contains(build.StartCommand, "uvicorn")
}

// djangoLoggingSnippet sends Django's errors to the container's output.
const djangoLoggingSnippet = `LOGGING = {"version": 1, "disable_existing_loggers": False, "handlers": {"console": {"class": "logging.StreamHandler"}}, "root": {"handlers": ["console"], "level": "WARNING"}}`

func djangoFindings(candidate *DetectedCandidate, build BuildPlanConfig) []PreflightFinding {
	django := candidate.Python.Django
	// With no settings module read, what the settings lack is unknown.
	if django == nil || candidate.Framework != "django" || django.SettingsModule == "" {
		return nil
	}
	findings := []PreflightFinding{}
	if django.WhiteNoise && !django.StaticRoot {
		findings = append(findings, finding("django_static_root_missing", PreflightWarning,
			"WhiteNoise has no STATIC_ROOT to serve", "whitenoise is installed; no settings file sets STATIC_ROOT",
			"collectstatic refuses to run without STATIC_ROOT, so the start command leaves it out and WhiteNoise serves no static files.",
			"Set STATIC_ROOT = BASE_DIR / \"staticfiles\" in the settings.", "deploy", "configuration.build.startCommand"))
	}
	if django.StaticFiles && !django.WhiteNoise {
		findings = append(findings, finding("django_static_unserved", PreflightWarning,
			"Django will not serve /static/ in production", "django.contrib.staticfiles is installed without WhiteNoise",
			"With DEBUG off, gunicorn serves no static files: the admin and every stylesheet and script answer 404.",
			"Add whitenoise, put whitenoise.middleware.WhiteNoiseMiddleware right after SecurityMiddleware, and set STATIC_ROOT.", "deploy", "configuration.build"))
	}
	if django.Development != "" {
		findings = append(findings, finding("django_development_settings", PreflightWarning,
			"The server runs Django's development settings", django.Development,
			"Development settings usually turn DEBUG on and carry a committed SECRET_KEY, which show tracebacks and settings to visitors and let anyone forge sessions.",
			"Add a production settings module that reads SECRET_KEY from the environment, and set DJANGO_SETTINGS_MODULE to it.", "deploy", "variables.DJANGO_SETTINGS_MODULE"))
	}
	if !django.Logging {
		findings = append(findings, finding("django_errors_unlogged", PreflightWarning,
			"Django's errors will not reach the log", "no LOGGING setting in the settings module",
			"With DEBUG off, Django sends request errors (a 500, a DisallowedHost 400) to the admins' email, not to the output this dashboard shows, so a failed request leaves no trace.",
			"Add to the settings: "+djangoLoggingSnippet, "deploy", "configuration.build"))
	}
	return findings
}
