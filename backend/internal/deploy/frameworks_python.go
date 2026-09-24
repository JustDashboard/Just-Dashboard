package deploy

import (
	"path"
	"regexp"
	"sort"
	"strings"
)

// pythonEntry is a source file detection reads to find the application
// object a server has to be pointed at. Paths are relative to the detection
// root; content is bounded.
type pythonEntry struct {
	path    string
	content []byte
}

// pythonEntryNames are the files an application object conventionally lives
// in. Detection reads only these, so a large repository's source is never
// scanned for a class name.
var pythonEntryNames = map[string]bool{
	"main.py": true, "app.py": true, "server.py": true, "api.py": true, "application.py": true,
	"run.py": true, "index.py": true, "wsgi.py": true, "asgi.py": true, "streamlit_app.py": true,
	"__init__.py": true, "manage.py": true,
}

// pythonDependencies is what the manifests declare: normalized distribution
// names, whether the declared versions are pinned, and which file said so.
type pythonDependencies struct {
	names    map[string]bool
	unpinned bool
	source   string
}

func (d pythonDependencies) has(name string) bool { return d.names[normalizePythonName(name)] }

// normalizePythonName follows PEP 503: case-insensitive, with runs of
// `-`, `_` and `.` read as one `-`.
func normalizePythonName(name string) string {
	return pythonNameSeparatorRE.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-")
}

var (
	pythonNameSeparatorRE = regexp.MustCompile(`[-_.]+`)
	pythonRequirementRE   = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._-]*)(\[[^\]]*\])?`)
	pythonLockPackageRE   = regexp.MustCompile(`(?m)^name\s*=\s*"([^"]+)"`)
	pythonFastAPIRE       = regexp.MustCompile(`(?m)^([A-Za-z_][A-Za-z0-9_]*)\s*(?::\s*[A-Za-z_][A-Za-z0-9_.]*\s*)?=\s*FastAPI\(`)
	pythonFlaskRE         = regexp.MustCompile(`(?m)^([A-Za-z_][A-Za-z0-9_]*)\s*(?::\s*[A-Za-z_][A-Za-z0-9_.]*\s*)?=\s*Flask\(`)
	pythonFactoryRE       = regexp.MustCompile(`(?m)^def\s+create_app\s*\(`)
	pythonImportRE        = regexp.MustCompile(`(?m)^\s*(?:import\s+([A-Za-z_][A-Za-z0-9_]*)|from\s+([A-Za-z_][A-Za-z0-9_]*)[A-Za-z0-9_.]*\s+import)`)
)

// readPythonDependencies reads every manifest present at a root. A lockfile
// is authoritative for names and proves pinning; requirements.txt pins only
// when each entry does; a bare pyproject pins nothing.
func readPythonDependencies(files map[string][]byte) pythonDependencies {
	result := pythonDependencies{names: map[string]bool{}}
	add := func(raw string) {
		match := pythonRequirementRE.FindStringSubmatch(strings.TrimSpace(raw))
		if match == nil {
			return
		}
		result.names[normalizePythonName(match[1])] = true
		// fastapi[standard] carries uvicorn; the extra is the only evidence.
		if normalizePythonName(match[1]) == "fastapi" && strings.Contains(strings.ToLower(match[2]), "standard") {
			result.names["uvicorn"] = true
		}
	}
	locked := false
	for _, lock := range []string{"uv.lock", "poetry.lock"} {
		content, ok := files[lock]
		if !ok {
			continue
		}
		locked = true
		if result.source == "" {
			result.source = lock
		}
		for _, match := range pythonLockPackageRE.FindAllStringSubmatch(string(content), -1) {
			result.names[normalizePythonName(match[1])] = true
		}
	}
	if content, ok := files["requirements.txt"]; ok {
		if result.source == "" {
			result.source = "requirements.txt"
		}
		for _, raw := range strings.Split(string(content), "\n") {
			line := strings.TrimSpace(raw)
			if comment := strings.Index(line, " #"); comment >= 0 {
				line = strings.TrimSpace(line[:comment])
			}
			if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") {
				continue
			}
			add(line)
			if !locked && !strings.Contains(line, "==") && !strings.Contains(line, "@") {
				result.unpinned = true
			}
		}
	}
	if content, ok := files["pyproject.toml"]; ok {
		if result.source == "" {
			result.source = "pyproject.toml"
		}
		for _, requirement := range pyprojectDependencies(string(content)) {
			add(requirement)
			if !locked && files["requirements.txt"] == nil && !strings.Contains(requirement, "==") && !strings.Contains(requirement, "@") {
				result.unpinned = true
			}
		}
	}
	return result
}

// pyprojectDependencies reads the requirement strings of a pyproject: the
// PEP 621 `[project]` dependencies array and the keys of Poetry's own
// dependency table. It is a line reader over two known shapes, not a TOML
// parser; an unusual layout yields fewer names, never a wrong one.
func pyprojectDependencies(content string) []string {
	var result []string
	table := ""
	inArray := false
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !inArray && strings.HasPrefix(line, "[") {
			table = strings.Trim(line, "[] ")
			continue
		}
		switch {
		case table == "project" && !inArray && strings.HasPrefix(line, "dependencies"):
			_, rest, _ := strings.Cut(line, "=")
			rest = strings.TrimSpace(rest)
			if !strings.HasPrefix(rest, "[") {
				continue
			}
			inArray = true
			line = strings.TrimPrefix(rest, "[")
			fallthrough
		case inArray:
			closed := strings.Contains(line, "]")
			if closed {
				line = line[:strings.Index(line, "]")]
			}
			for _, item := range strings.Split(line, ",") {
				item = strings.Trim(strings.TrimSpace(item), `"'`)
				if item != "" {
					result = append(result, item)
				}
			}
			if closed {
				inArray = false
			}
		case table == "tool.poetry.dependencies":
			name, _, found := strings.Cut(line, "=")
			name = strings.Trim(strings.TrimSpace(name), `"'`)
			if found && name != "" && !strings.EqualFold(name, "python") {
				result = append(result, name)
			}
		}
	}
	return result
}

// pythonFramework is one catalogue entry: the distribution that names it and
// how its production process is started once the application object is
// found. Server is the process the start command runs through, installed
// by the recipe when the manifests do not declare it.
type pythonFramework struct {
	Name, Label string
	Dependency  string
	Port        int
	Server      string
	resolve     func(deps pythonDependencies, files pythonRootFiles, entries []pythonEntry) pythonResolution
}

type pythonResolution struct {
	Start      string
	Confidence DetectionConfidence
	Decisions  []string
	Evidence   []DetectionEvidence
}

// pythonRootFiles are the root-level markers a framework's defaults depend
// on beyond the manifests.
type pythonRootFiles struct {
	managePy bool
	procfile []byte
}

var pythonFrameworks = []pythonFramework{
	{
		Name: "django", Label: "Django", Dependency: "django", Port: 8000, Server: "gunicorn",
		resolve: func(deps pythonDependencies, files pythonRootFiles, entries []pythonEntry) pythonResolution {
			module := ""
			for _, name := range []string{"wsgi.py", "asgi.py"} {
				for _, entry := range sortedPythonEntries(entries) {
					if path.Base(entry.path) == name && strings.Contains(entry.path, "/") {
						module = pythonModule(entry.path)
						break
					}
				}
				if module != "" {
					break
				}
			}
			if !files.managePy {
				return pythonResolution{Confidence: ConfidenceLow, Decisions: []string{"Django is installed but manage.py is not at this root; confirm the start command"}}
			}
			if module == "" {
				return pythonResolution{Confidence: ConfidenceLow, Decisions: []string{"name the Django WSGI module for gunicorn (project.wsgi:application)"}}
			}
			start := "python manage.py migrate --noinput && "
			if deps.has("whitenoise") {
				start += "python manage.py collectstatic --noinput && "
			}
			evidence := []DetectionEvidence{{Path: "manage.py", Reason: "Django project; migrations are applied before the server starts"}}
			if strings.HasSuffix(module, ".asgi") && deps.has("uvicorn") {
				return pythonResolution{Start: start + "uvicorn " + module + ":application --host 0.0.0.0 --port 8000", Evidence: evidence}
			}
			project := strings.TrimSuffix(strings.TrimSuffix(module, ".asgi"), ".wsgi")
			return pythonResolution{Start: start + "gunicorn " + project + ".wsgi:application --bind 0.0.0.0:8000", Evidence: evidence}
		},
	},
	{
		Name: "fastapi", Label: "FastAPI", Dependency: "fastapi", Port: 8000, Server: "uvicorn",
		resolve: func(_ pythonDependencies, _ pythonRootFiles, entries []pythonEntry) pythonResolution {
			for _, entry := range sortedPythonEntries(entries) {
				if match := pythonFastAPIRE.FindSubmatch(entry.content); match != nil {
					return pythonResolution{
						Start:    "uvicorn " + pythonModule(entry.path) + ":" + string(match[1]) + " --host 0.0.0.0 --port 8000",
						Evidence: []DetectionEvidence{{Path: entry.path, Reason: "FastAPI application object " + string(match[1])}},
					}
				}
			}
			return pythonResolution{Confidence: ConfidenceLow, Decisions: []string{"name the FastAPI application for uvicorn (module:app)"}}
		},
	},
	{
		Name: "flask", Label: "Flask", Dependency: "flask", Port: 8000, Server: "gunicorn",
		resolve: func(_ pythonDependencies, _ pythonRootFiles, entries []pythonEntry) pythonResolution {
			for _, entry := range sortedPythonEntries(entries) {
				if match := pythonFlaskRE.FindSubmatch(entry.content); match != nil {
					return pythonResolution{
						Start:    "gunicorn --bind 0.0.0.0:8000 " + pythonModule(entry.path) + ":" + string(match[1]),
						Evidence: []DetectionEvidence{{Path: entry.path, Reason: "Flask application object " + string(match[1])}},
					}
				}
			}
			for _, entry := range sortedPythonEntries(entries) {
				if pythonFactoryRE.Match(entry.content) {
					return pythonResolution{
						Start:    "gunicorn --bind 0.0.0.0:8000 '" + pythonModule(entry.path) + ":create_app()'",
						Evidence: []DetectionEvidence{{Path: entry.path, Reason: "Flask application factory create_app"}},
					}
				}
			}
			return pythonResolution{Confidence: ConfidenceLow, Decisions: []string{"name the Flask application for gunicorn (module:app)"}}
		},
	},
	{
		Name: "streamlit", Label: "Streamlit", Dependency: "streamlit", Port: 8501,
		resolve: func(_ pythonDependencies, _ pythonRootFiles, entries []pythonEntry) pythonResolution {
			for _, entry := range sortedPythonEntries(entries) {
				if pythonImports(entry.content, "streamlit") {
					return pythonResolution{
						Start:    "streamlit run " + entry.path + " --server.port 8501 --server.address 0.0.0.0 --server.headless true",
						Evidence: []DetectionEvidence{{Path: entry.path, Reason: "Streamlit application"}},
					}
				}
			}
			return pythonResolution{Confidence: ConfidenceLow, Decisions: []string{"name the Streamlit script to run"}}
		},
	},
	{
		Name: "gradio", Label: "Gradio", Dependency: "gradio", Port: 7860,
		resolve: func(_ pythonDependencies, _ pythonRootFiles, entries []pythonEntry) pythonResolution {
			for _, entry := range sortedPythonEntries(entries) {
				if pythonImports(entry.content, "gradio") && strings.Contains(string(entry.content), ".launch(") {
					return pythonResolution{
						Start:    "python " + entry.path,
						Evidence: []DetectionEvidence{{Path: entry.path, Reason: "Gradio application"}},
					}
				}
			}
			return pythonResolution{Confidence: ConfidenceLow, Decisions: []string{"name the Gradio script to run"}}
		},
	},
}

// pythonFrameworkEnv is what a framework's server needs from its environment
// to answer from inside a container.
func pythonFrameworkEnv(name string) []string {
	if name == "gradio" {
		return []string{"GRADIO_SERVER_NAME=0.0.0.0"}
	}
	return nil
}

func matchPythonFramework(deps pythonDependencies) *pythonFramework {
	for index := range pythonFrameworks {
		if deps.has(pythonFrameworks[index].Dependency) {
			return &pythonFrameworks[index]
		}
	}
	return nil
}

// sortedPythonEntries orders entries shallowest first, then by the
// conventional weight of their names, so `main.py` at the root wins over a
// `tests/app.py` and `app/main.py` over `app/__init__.py`.
func sortedPythonEntries(entries []pythonEntry) []pythonEntry {
	weight := func(name string) int {
		switch name {
		case "main.py":
			return 0
		case "app.py":
			return 1
		case "streamlit_app.py", "server.py", "api.py", "application.py":
			return 2
		case "wsgi.py", "asgi.py":
			return 3
		case "run.py", "index.py":
			return 4
		case "__init__.py":
			return 5
		}
		return 6
	}
	sorted := append([]pythonEntry(nil), entries...)
	sort.SliceStable(sorted, func(i, j int) bool {
		di, dj := strings.Count(sorted[i].path, "/"), strings.Count(sorted[j].path, "/")
		if di != dj {
			return di < dj
		}
		wi, wj := weight(path.Base(sorted[i].path)), weight(path.Base(sorted[j].path))
		if wi != wj {
			return wi < wj
		}
		return sorted[i].path < sorted[j].path
	})
	return sorted
}

// pythonModule is the dotted import path of a source file relative to the
// package root: app/main.py is app.main, and a package's __init__.py is the
// package itself.
func pythonModule(file string) string {
	module := strings.TrimSuffix(file, ".py")
	module = strings.TrimSuffix(module, "/__init__")
	return strings.ReplaceAll(module, "/", ".")
}

func pythonImports(content []byte, module string) bool {
	for _, match := range pythonImportRE.FindAllSubmatch(content, -1) {
		if string(match[1]) == module || string(match[2]) == module {
			return true
		}
	}
	return false
}

// pythonCandidate builds the candidate for a root that declares Python
// dependencies. A recognised framework supplies the process and the port;
// anything else needs the operator to say how it runs.
func pythonCandidate(marker *detectedMarkers, entries []pythonEntry, rootLabel string) DetectedCandidate {
	deps := readPythonDependencies(marker.pythonFiles)
	candidate := DetectedCandidate{
		Name: "Python service in " + rootLabel, Profile: ProfileService, Confidence: ConfidenceMedium,
		Framework: "python", Recipe: "python",
		Evidence:      []DetectionEvidence{{Path: path.Join(marker.root, deps.source), Reason: "Python dependency manifest"}},
		NeedsDecision: []string{},
	}
	if _, locked := marker.pythonFiles["uv.lock"]; locked {
		candidate.Evidence[0].Reason = "locked Python dependency input"
	} else if _, locked := marker.pythonFiles["poetry.lock"]; locked {
		candidate.Evidence[0].Reason = "locked Python dependency input"
	}
	candidate.UnpinnedDependencies = deps.unpinned
	candidate.PythonRequires, candidate.PythonInstall = pythonDeclaredRange(marker.pythonFiles), pythonInstallKind(marker.pythonFiles)
	version, err := choosePythonRecipeVersion("", string(marker.pythonVersionFile), string(marker.runtimeTxt), string(marker.pythonFiles["pyproject.toml"]))
	if err != nil {
		candidate.RecipeIssue = err.Error()
	} else {
		candidate.PythonVersion = version
	}
	files := pythonRootFiles{managePy: marker.managePy, procfile: marker.procfile}
	procfileWeb := procfileProcess(marker.procfile, "web")
	if procfileWeb != "" && rejectPlanSecretLiteral("Procfile web process", procfileWeb) != nil {
		procfileWeb = ""
	}
	framework := matchPythonFramework(deps)
	if framework != nil {
		resolution := framework.resolve(deps, files, entries)
		candidate.Name = framework.Label + " application in " + rootLabel
		candidate.Framework = framework.Name
		candidate.Profile, candidate.Port = ProfileWeb, framework.Port
		candidate.Confidence = ConfidenceHigh
		if resolution.Confidence != "" {
			candidate.Confidence = resolution.Confidence
		}
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: path.Join(marker.root, deps.source), Reason: framework.Label + " dependency"})
		for _, evidence := range resolution.Evidence {
			evidence.Path = path.Join(marker.root, evidence.Path)
			candidate.Evidence = append(candidate.Evidence, evidence)
		}
		candidate.NeedsDecision = append(candidate.NeedsDecision, resolution.Decisions...)
		candidate.StartCommand = resolution.Start
	}
	switch {
	case procfileWeb != "":
		candidate.StartCommand = procfileWeb
		candidate.Profile = ProfileWeb
		if candidate.Port == 0 {
			candidate.Port = 8000
		}
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: path.Join(marker.root, "Procfile"), Reason: "web process: " + boundedEvidence(procfileWeb)})
	case framework != nil:
	default:
		script := ""
		for _, entry := range sortedPythonEntries(entries) {
			if !strings.Contains(entry.path, "/") && (path.Base(entry.path) == "main.py" || path.Base(entry.path) == "app.py") {
				script = entry.path
				break
			}
		}
		if script != "" {
			candidate.StartCommand = "python " + script
			candidate.Profile = ProfileWorker
			candidate.Confidence = ConfidenceLow
			candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: path.Join(marker.root, script), Reason: "script at the package root"})
			candidate.NeedsDecision = append(candidate.NeedsDecision, "confirm whether this script serves HTTP (web application) or runs as a worker, and its port")
		} else {
			candidate.NeedsDecision = append(candidate.NeedsDecision, "confirm ASGI/WSGI start command, port, and readiness check")
		}
	}
	return candidate
}
