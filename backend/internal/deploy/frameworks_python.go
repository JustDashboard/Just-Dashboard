package deploy

import (
	"path"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
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
// in. The walk reads these wherever they are; the root's other scripts are
// read after it (readPythonSources), so a large repository's source is never
// scanned for a class name.
var pythonEntryNames = map[string]bool{
	"main.py": true, "app.py": true, "server.py": true, "api.py": true, "application.py": true,
	"run.py": true, "index.py": true, "wsgi.py": true, "asgi.py": true, "streamlit_app.py": true,
	"__init__.py": true, "manage.py": true,
}

// pythonDependencies is what the manifests declare: normalized distribution
// names, whether the declared versions are pinned, and which file said so.
// names is everything the install puts in the environment that the
// manifests show — every declared requirement and every locked production
// package — while direct is what the project itself declares, which is what
// a framework is recognised from: Gradio locks FastAPI, and Dash Flask.
type pythonDependencies struct {
	names    map[string]bool
	direct   map[string]bool
	extras   map[string][]string
	unpinned bool
	source   string
}

func (d pythonDependencies) has(name string) bool { return d.names[normalizePythonName(name)] }

func (d pythonDependencies) declares(name string) bool { return d.direct[normalizePythonName(name)] }

// normalizePythonName follows PEP 503: case-insensitive, with runs of
// `-`, `_` and `.` read as one `-`.
func normalizePythonName(name string) string {
	return pythonNameSeparatorRE.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-")
}

var pythonNameSeparatorRE = regexp.MustCompile(`[-_.]+`)

// readPythonDependencies reads every manifest present at a root.
func readPythonDependencies(files map[string][]byte) pythonDependencies {
	return readPythonProject(files).deps
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
	// entryOnly marks a framework many other packages install, which counts
	// only when an entry file builds its application: aiohttp, Tornado and
	// Starlette are dependencies of half the catalogue.
	entryOnly bool
	resolve   func(ctx pythonResolveContext) pythonResolution
}

// pythonResolveContext is what a framework's resolution reads.
type pythonResolveContext struct {
	deps    pythonDependencies
	files   pythonRootFiles
	entries []pythonEntry
}

type pythonResolution struct {
	Start      string
	Confidence DetectionConfidence
	Decisions  []string
	Evidence   []DetectionEvidence
	// found says an entry file built the framework's application; a
	// framework that only appears in the manifests yields to one that does.
	found bool
	// Port is the port the entry names, when it fixes one.
	Port        int
	RecipeIssue string
}

// pythonRootFiles are the root-level markers a framework's defaults depend
// on beyond the manifests.
type pythonRootFiles struct {
	managePy bool
	procfile []byte
	source   *pythonSource
	django   *DetectedDjango
}

var (
	pythonImportRE       = regexp.MustCompile(`(?m)^\s*(?:import\s+([A-Za-z_][A-Za-z0-9_]*)|from\s+([A-Za-z_][A-Za-z0-9_]*)[A-Za-z0-9_.]*\s+import)`)
	pythonFromImportRE   = regexp.MustCompile(`(?m)^from\s+([A-Za-z_][A-Za-z0-9_.]*)\s+import\s+\(?\s*([A-Za-z_][A-Za-z0-9_, \t]*)`)
	pythonFactoryDefRE   = regexp.MustCompile(`(?m)^(?:async\s+)?def\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(([^)]*)\)\s*(?:->\s*([A-Za-z_][A-Za-z0-9_.]*))?\s*:`)
	pythonModuleAssignRE = regexp.MustCompile(`(?m)^([A-Za-z_][A-Za-z0-9_]*)\s*(?::\s*[A-Za-z_][A-Za-z0-9_.\[\]]*\s*)?=\s*([A-Za-z_][A-Za-z0-9_.]*)\(`)
	pythonPortArgRE      = regexp.MustCompile(`\bport\s*=\s*(\d{2,5})\b`)
	pythonServerPortRE   = regexp.MustCompile(`\bserver_port\s*=\s*(\d{2,5})\b`)
	pythonServerNameRE   = regexp.MustCompile(`\bserver_name\s*=\s*["'](127\.0\.0\.1|localhost)["']`)
	pythonListenRE       = regexp.MustCompile(`\.listen\(\s*(\d{2,5})`)
	pythonDashServerRE   = regexp.MustCompile(`(?m)^([A-Za-z_][A-Za-z0-9_]*)\s*=\s*([A-Za-z_][A-Za-z0-9_]*)\.server\b`)
	pythonProtocolRouter = regexp.MustCompile(`\bProtocolTypeRouter\s*\(`)
)

// pythonConstructorREs are the calls that build each framework's
// application at module level, qualified or not, by the pattern that names
// the constructor.
var pythonConstructorREs = func() map[string]*regexp.Regexp {
	compiled := map[string]*regexp.Regexp{}
	for _, pattern := range []string{
		`(?:fastapi\.)?FastAPI`, `(?:flask\.)?Flask`, `(?:litestar\.)?Litestar`, `(?:starlette\.applications\.|starlette\.)?Starlette`,
		`(?:sanic\.)?Sanic`, `(?:quart\.)?Quart`, `falcon\.asgi\.App`, `falcon\.(?:App|API)`, `(?:bottle\.)?Bottle`,
		`(?:web|aiohttp\.web)\.Application`, `(?:dash\.)?Dash`,
	} {
		compiled[pattern] = regexp.MustCompile(`(?m)^([A-Za-z_][A-Za-z0-9_]*)\s*(?::\s*[A-Za-z_][A-Za-z0-9_.\[\]]*\s*)?=\s*(?:` + pattern + `)\(`)
	}
	return compiled
}()

func pythonConstructorRE(pattern string) *regexp.Regexp { return pythonConstructorREs[pattern] }

// pythonObject is an application object found in an entry: the module and
// the name a server imports, and whether it is a factory to call.
type pythonObject struct {
	entry   pythonEntry
	name    string
	factory bool
	// arguments is a factory call that needs arguments; the server is then
	// pointed at the module-level object built from it instead.
	arguments bool
}

// findPythonObject finds a framework's application object: a module-level
// constructor call, then a module-level object a factory in the entries
// returns, then a factory the server can call itself.
func findPythonObject(entries []pythonEntry, constructor, typeName string) (pythonObject, bool) {
	pattern := pythonConstructorRE(constructor)
	for _, entry := range sortedPythonEntries(entries) {
		if match := pattern.FindSubmatch(entry.content); match != nil {
			return pythonObject{entry: entry, name: string(match[1])}, true
		}
	}
	factories := pythonFactories(entries, constructor, typeName)
	if len(factories) == 0 {
		return pythonObject{}, false
	}
	for _, entry := range sortedPythonEntries(entries) {
		for _, match := range pythonModuleAssignRE.FindAllSubmatch(entry.content, -1) {
			called := string(match[2])
			if index := strings.LastIndex(called, "."); index >= 0 {
				called = called[index+1:]
			}
			if _, ok := factories[called]; ok {
				return pythonObject{entry: entry, name: string(match[1])}, true
			}
		}
	}
	names := make([]string, 0, len(factories))
	for name := range factories {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		// create_app is the conventional factory name.
		if (names[i] == "create_app") != (names[j] == "create_app") {
			return names[i] == "create_app"
		}
		return names[i] < names[j]
	})
	factory := factories[names[0]]
	return pythonObject{entry: factory.entry, name: names[0], factory: true, arguments: factory.required}, true
}

type pythonFactory struct {
	entry    pythonEntry
	required bool
}

// pythonFactories are the functions in the entries that return a framework's
// application: annotated with its type, or building it in their body.
func pythonFactories(entries []pythonEntry, constructor, typeName string) map[string]pythonFactory {
	factories := map[string]pythonFactory{}
	body := regexp.MustCompile(`(?:^|[^A-Za-z0-9_.])` + constructor + `\(`)
	for _, entry := range sortedPythonEntries(entries) {
		content := string(entry.content)
		definitions := pythonFactoryDefRE.FindAllStringSubmatchIndex(content, -1)
		for index, location := range definitions {
			name := content[location[2]:location[3]]
			parameters := content[location[4]:location[5]]
			returns := ""
			if location[6] >= 0 {
				returns = content[location[6]:location[7]]
			}
			end := len(content)
			if index+1 < len(definitions) {
				end = definitions[index+1][0]
			}
			builds := strings.HasSuffix(returns, typeName) || body.MatchString(content[location[1]:end])
			if !builds || strings.HasPrefix(name, "_") && name != "_create_app" {
				continue
			}
			if _, seen := factories[name]; !seen {
				factories[name] = pythonFactory{entry: entry, required: pythonRequiredParameters(parameters)}
			}
		}
	}
	return factories
}

// pythonRequiredParameters says a def's parameter list has one without a
// default, which a server calling the factory cannot supply.
func pythonRequiredParameters(parameters string) bool {
	for _, parameter := range strings.Split(parameters, ",") {
		parameter = strings.TrimSpace(parameter)
		if parameter == "" || parameter == "*" || parameter == "/" || strings.HasPrefix(parameter, "*") || parameter == "self" {
			continue
		}
		if !strings.Contains(parameter, "=") {
			return true
		}
	}
	return false
}

// pythonTarget is how a server names an object: the module path, and the
// directory the server has to add to the import path for it.
type pythonTarget struct {
	module, directory string
}

// pythonTargetFor is the import target of an entry. A src/ layout imports by
// package name with src on the path; an application folder whose modules
// import their siblings by bare name is run from inside that folder.
func pythonTargetFor(entry pythonEntry, files pythonRootFiles) pythonTarget {
	file := entry.path
	if strings.HasPrefix(file, "src/") && strings.Count(file, "/") >= 2 {
		return pythonTarget{module: pythonModule(strings.TrimPrefix(file, "src/")), directory: "src"}
	}
	directory := path.Dir(file)
	if directory != "." && !strings.Contains(directory, "/") && files.source != nil {
		siblings := files.source.siblings[directory]
		own := path.Base(directory)
		for _, match := range pythonImportRE.FindAllSubmatch(entry.content, -1) {
			imported := string(match[1]) + string(match[2])
			if imported != own && siblings[imported] && !(files.source.modules[imported]) {
				return pythonTarget{module: pythonModule(strings.TrimPrefix(file, directory+"/")), directory: directory}
			}
		}
	}
	return pythonTarget{module: pythonModule(file)}
}

// uvicornCommand, gunicornCommand: the detected commands read ${PORT:-N}
// rather than a fixed port, so a port changed in Build settings moves the
// server with it, and gunicorn writes its access log to the container's
// output, so a request that fails is at least a status code in the log.
func uvicornCommand(target pythonTarget, object string, factory bool) string {
	command := "uvicorn "
	if factory {
		command += "--factory "
	}
	command += target.module + ":" + object + " --host 0.0.0.0 --port ${PORT:-8000}"
	if target.directory != "" {
		command += " --app-dir " + target.directory
	}
	return command
}

func gunicornCommand(target pythonTarget, object string, port int, options string) string {
	command := "gunicorn --bind 0.0.0.0:${PORT:-" + strconv.Itoa(port) + "} --access-logfile -"
	if options != "" {
		command += " " + options
	}
	if target.directory != "" {
		command += " --pythonpath " + target.directory
	}
	application := target.module + ":" + object
	if strings.Contains(object, "(") {
		// A factory call is one shell word.
		application = "'" + application + "'"
	}
	return command + " " + application
}

// objectEvidence names an application object for the evidence.
func objectEvidence(label string, object pythonObject) DetectionEvidence {
	kind := "application object"
	if object.factory {
		kind = "application factory"
	}
	return DetectionEvidence{Path: object.entry.path, Reason: label + " " + kind + " " + object.name}
}

// resolveObjectFramework is the resolution of a framework served by pointing
// a server at its application object.
func resolveObjectFramework(ctx pythonResolveContext, label, constructor, typeName string, command func(pythonTarget, pythonObject) string) pythonResolution {
	object, ok := findPythonObject(ctx.entries, constructor, typeName)
	if !ok {
		return pythonResolution{Confidence: ConfidenceLow, Decisions: []string{"name the " + label + " application for the server (module:object)"}}
	}
	if object.factory && object.arguments {
		return pythonResolution{Confidence: ConfidenceLow, found: true, Evidence: []DetectionEvidence{objectEvidence(label, object)},
			Decisions: []string{"the " + label + " factory " + object.name + " needs arguments; name the application object for the server"}}
	}
	return pythonResolution{Start: command(pythonTargetFor(object.entry, ctx.files), object), found: true,
		Evidence: []DetectionEvidence{objectEvidence(label, object)}}
}

// scriptImporting is the entry that imports a module, preferring the
// conventional script names, and whether one does.
func scriptImporting(entries []pythonEntry, module string, also func(pythonEntry) bool) (pythonEntry, bool) {
	for _, entry := range sortedPythonEntries(entries) {
		if pythonImports(entry.content, module) && (also == nil || also(entry)) {
			return entry, true
		}
	}
	return pythonEntry{}, false
}

func entryContains(content []byte, needle string) bool {
	return strings.Contains(string(content), needle)
}

var pythonFrameworks = []pythonFramework{
	// The frameworks built on FastAPI, Flask or Starlette come first, since
	// their own dependency pulls the host framework in.
	{
		Name: "gradio", Label: "Gradio", Dependency: "gradio", Port: 7860,
		resolve: func(ctx pythonResolveContext) pythonResolution {
			entry, ok := scriptImporting(ctx.entries, "gradio", func(entry pythonEntry) bool { return entryContains(entry.content, ".launch(") })
			if !ok {
				return pythonResolution{Confidence: ConfidenceLow, Decisions: []string{"name the Gradio script to run"}}
			}
			resolution := pythonResolution{Start: "python " + entry.path, found: true,
				Evidence: []DetectionEvidence{{Path: entry.path, Reason: "Gradio application"}}}
			if match := pythonServerPortRE.FindSubmatch(entry.content); match != nil {
				resolution.Port, _ = strconv.Atoi(string(match[1]))
			}
			return resolution
		},
	},
	{
		Name: "chainlit", Label: "Chainlit", Dependency: "chainlit", Port: 8000,
		resolve: func(ctx pythonResolveContext) pythonResolution {
			entry, ok := scriptImporting(ctx.entries, "chainlit", nil)
			if !ok {
				return pythonResolution{Confidence: ConfidenceLow, Decisions: []string{"name the Chainlit script to run"}}
			}
			return pythonResolution{Start: "chainlit run " + entry.path + " --host 0.0.0.0 --port ${PORT:-8000} --headless", found: true,
				Evidence: []DetectionEvidence{{Path: entry.path, Reason: "Chainlit application"}}}
		},
	},
	{
		Name: "nicegui", Label: "NiceGUI", Dependency: "nicegui", Port: 8080,
		resolve: func(ctx pythonResolveContext) pythonResolution {
			entry, ok := scriptImporting(ctx.entries, "nicegui", func(entry pythonEntry) bool { return entryContains(entry.content, "ui.run(") })
			if !ok {
				return pythonResolution{Confidence: ConfidenceLow, Decisions: []string{"name the NiceGUI script that calls ui.run()"}}
			}
			resolution := pythonResolution{Start: "python " + entry.path, found: true,
				Evidence: []DetectionEvidence{{Path: entry.path, Reason: "NiceGUI application"}}}
			if match := pythonPortArgRE.FindSubmatch(entry.content); match != nil {
				resolution.Port, _ = strconv.Atoi(string(match[1]))
			}
			return resolution
		},
	},
	{
		Name: "reflex", Label: "Reflex", Dependency: "reflex", Port: 3000,
		resolve: func(ctx pythonResolveContext) pythonResolution {
			if ctx.files.source == nil || !ctx.files.source.rxconfig {
				return pythonResolution{Confidence: ConfidenceLow, Decisions: []string{"confirm how this Reflex application is served"}}
			}
			return pythonResolution{found: true, Confidence: ConfidenceLow,
				Evidence:    []DetectionEvidence{{Path: "rxconfig.py", Reason: "Reflex application"}},
				RecipeIssue: "Reflex compiles a Node frontend and runs a separate backend; build it from a Dockerfile (reflex's own docker example)"}
		},
	},
	{
		Name: "mesop", Label: "Mesop", Dependency: "mesop", Port: 8080, Server: "gunicorn",
		resolve: func(ctx pythonResolveContext) pythonResolution {
			entry, ok := scriptImporting(ctx.entries, "mesop", func(entry pythonEntry) bool { return entryContains(entry.content, ".page(") })
			if !ok {
				return pythonResolution{Confidence: ConfidenceLow, Decisions: []string{"name the Mesop module that declares its pages"}}
			}
			target := pythonTargetFor(entry, ctx.files)
			return pythonResolution{Start: gunicornCommand(target, "me", 8080, ""), found: true,
				Evidence: []DetectionEvidence{{Path: entry.path, Reason: "Mesop application"}}}
		},
	},
	{
		Name: "dash", Label: "Dash", Dependency: "dash", Port: 8050,
		resolve: func(ctx pythonResolveContext) pythonResolution {
			object, ok := findPythonObject(ctx.entries, `(?:dash\.)?Dash`, "Dash")
			if !ok || object.factory {
				return pythonResolution{Confidence: ConfidenceLow, Decisions: []string{"name the Dash application script"}}
			}
			evidence := []DetectionEvidence{objectEvidence("Dash", object)}
			// Dash's development server is what `python app.py` runs; its
			// Flask server is what gunicorn serves in production.
			for _, match := range pythonDashServerRE.FindAllSubmatch(object.entry.content, -1) {
				if string(match[2]) == object.name {
					return pythonResolution{Start: gunicornCommand(pythonTargetFor(object.entry, ctx.files), string(match[1]), 8050, ""), found: true, Evidence: evidence}
				}
			}
			return pythonResolution{Start: "python " + object.entry.path, found: true, Evidence: evidence}
		},
	},
	{
		Name: "panel", Label: "Panel", Dependency: "panel", Port: 5006,
		resolve: func(ctx pythonResolveContext) pythonResolution {
			entry, ok := scriptImporting(ctx.entries, "panel", func(entry pythonEntry) bool { return entryContains(entry.content, ".servable(") })
			if !ok {
				return pythonResolution{Confidence: ConfidenceLow, Decisions: []string{"name the Panel script that marks its layout .servable()"}}
			}
			return pythonResolution{Start: "panel serve " + entry.path + " --address 0.0.0.0 --port ${PORT:-5006}", found: true,
				Evidence: []DetectionEvidence{{Path: entry.path, Reason: "Panel application"}}}
		},
	},
	{
		Name: "streamlit", Label: "Streamlit", Dependency: "streamlit", Port: 8501,
		resolve: func(ctx pythonResolveContext) pythonResolution {
			entry, ok := streamlitEntry(ctx)
			if !ok {
				return pythonResolution{Confidence: ConfidenceLow, Decisions: []string{"name the Streamlit script to run"}}
			}
			return pythonResolution{
				Start: "streamlit run " + entry.path + " --server.port ${PORT:-8501} --server.address 0.0.0.0 --server.headless true", found: true,
				Evidence: []DetectionEvidence{{Path: entry.path, Reason: "Streamlit application"}},
			}
		},
	},
	{
		Name: "django", Label: "Django", Dependency: "django", Port: 8000, Server: "gunicorn",
		resolve: resolveDjango,
	},
	{
		Name: "fastapi", Label: "FastAPI", Dependency: "fastapi", Port: 8000, Server: "uvicorn",
		resolve: func(ctx pythonResolveContext) pythonResolution {
			return resolveObjectFramework(ctx, "FastAPI", `(?:fastapi\.)?FastAPI`, "FastAPI", func(target pythonTarget, object pythonObject) string {
				return uvicornCommand(target, object.name, object.factory)
			})
		},
	},
	{
		Name: "litestar", Label: "Litestar", Dependency: "litestar", Port: 8000, Server: "uvicorn",
		resolve: func(ctx pythonResolveContext) pythonResolution {
			return resolveObjectFramework(ctx, "Litestar", `(?:litestar\.)?Litestar`, "Litestar", func(target pythonTarget, object pythonObject) string {
				return uvicornCommand(target, object.name, object.factory)
			})
		},
	},
	{
		Name: "starlette", Label: "Starlette", Dependency: "starlette", Port: 8000, Server: "uvicorn", entryOnly: true,
		resolve: func(ctx pythonResolveContext) pythonResolution {
			return resolveObjectFramework(ctx, "Starlette", `(?:starlette\.applications\.|starlette\.)?Starlette`, "Starlette", func(target pythonTarget, object pythonObject) string {
				return uvicornCommand(target, object.name, object.factory)
			})
		},
	},
	{
		Name: "sanic", Label: "Sanic", Dependency: "sanic", Port: 8000,
		resolve: func(ctx pythonResolveContext) pythonResolution {
			return resolveObjectFramework(ctx, "Sanic", `(?:sanic\.)?Sanic`, "Sanic", func(target pythonTarget, object pythonObject) string {
				command := "sanic " + target.module + ":" + object.name + " --host 0.0.0.0 --port ${PORT:-8000}"
				if object.factory {
					command += " --factory"
				}
				if target.directory != "" {
					command = "cd " + target.directory + " && " + command
				}
				return command
			})
		},
	},
	{
		Name: "quart", Label: "Quart", Dependency: "quart", Port: 8000,
		resolve: func(ctx pythonResolveContext) pythonResolution {
			// Hypercorn is Quart's own server and one of its dependencies.
			return resolveObjectFramework(ctx, "Quart", `(?:quart\.)?Quart`, "Quart", func(target pythonTarget, object pythonObject) string {
				name := object.name
				if object.factory {
					name += "()"
				}
				command := "hypercorn --bind 0.0.0.0:${PORT:-8000} --access-logfile - '" + target.module + ":" + name + "'"
				if target.directory != "" {
					command = "cd " + target.directory + " && " + command
				}
				return command
			})
		},
	},
	{
		Name: "falcon", Label: "Falcon", Dependency: "falcon", Port: 8000,
		resolve: func(ctx pythonResolveContext) pythonResolution {
			if object, ok := findPythonObject(ctx.entries, `falcon\.asgi\.App`, "falcon.asgi.App"); ok && !object.factory {
				return pythonResolution{Start: uvicornCommand(pythonTargetFor(object.entry, ctx.files), object.name, false), found: true,
					Evidence: []DetectionEvidence{objectEvidence("Falcon ASGI", object)}}
			}
			return resolveObjectFramework(ctx, "Falcon", `falcon\.(?:App|API)`, "falcon.App", func(target pythonTarget, object pythonObject) string {
				if object.factory {
					return gunicornCommand(target, object.name+"()", 8000, "")
				}
				return gunicornCommand(target, object.name, 8000, "")
			})
		},
	},
	{
		Name: "bottle", Label: "Bottle", Dependency: "bottle", Port: 8000, Server: "gunicorn",
		resolve: func(ctx pythonResolveContext) pythonResolution {
			return resolveObjectFramework(ctx, "Bottle", `(?:bottle\.)?Bottle`, "Bottle", func(target pythonTarget, object pythonObject) string {
				return gunicornCommand(target, object.name, 8000, "")
			})
		},
	},
	{
		Name: "flask", Label: "Flask", Dependency: "flask", Port: 8000, Server: "gunicorn",
		resolve: resolveFlask,
	},
	{
		Name: "aiohttp", Label: "aiohttp", Dependency: "aiohttp", Port: 8080, entryOnly: true,
		resolve: func(ctx pythonResolveContext) pythonResolution {
			for _, entry := range sortedPythonEntries(ctx.entries) {
				if !pythonConstructorRE(`(?:web|aiohttp\.web)\.Application`).Match(entry.content) && !entryContains(entry.content, "web.Application(") {
					continue
				}
				if entryContains(entry.content, "run_app(") {
					resolution := pythonResolution{Start: "python " + entry.path, found: true,
						Evidence: []DetectionEvidence{{Path: entry.path, Reason: "aiohttp application run by web.run_app"}}}
					if match := pythonPortArgRE.FindSubmatch(entry.content); match != nil {
						resolution.Port, _ = strconv.Atoi(string(match[1]))
					}
					return resolution
				}
				if match := pythonConstructorRE(`(?:web|aiohttp\.web)\.Application`).FindSubmatch(entry.content); match != nil {
					return pythonResolution{Start: gunicornCommand(pythonTargetFor(entry, ctx.files), string(match[1]), 8080, "--worker-class aiohttp.GunicornWebWorker"),
						found: true, Evidence: []DetectionEvidence{{Path: entry.path, Reason: "aiohttp application object " + string(match[1])}}}
				}
			}
			return pythonResolution{Confidence: ConfidenceLow}
		},
	},
	{
		Name: "tornado", Label: "Tornado", Dependency: "tornado", Port: 8888, entryOnly: true,
		resolve: func(ctx pythonResolveContext) pythonResolution {
			for _, entry := range sortedPythonEntries(ctx.entries) {
				if !entryContains(entry.content, "tornado.web.Application(") && !entryContains(entry.content, "web.Application(") || !pythonImports(entry.content, "tornado") {
					continue
				}
				match := pythonListenRE.FindSubmatch(entry.content)
				if match == nil {
					continue
				}
				resolution := pythonResolution{Start: "python " + entry.path, found: true,
					Evidence: []DetectionEvidence{{Path: entry.path, Reason: "Tornado application listening on " + string(match[1])}}}
				resolution.Port, _ = strconv.Atoi(string(match[1]))
				return resolution
			}
			return pythonResolution{Confidence: ConfidenceLow}
		},
	},
}

// streamlitEntry is the script Streamlit runs: a root script that imports
// streamlit, the conventional names first. In a multipage application the
// entry is the script beside pages/, never one inside it.
func streamlitEntry(ctx pythonResolveContext) (pythonEntry, bool) {
	var found []pythonEntry
	for _, entry := range sortedPythonEntries(ctx.entries) {
		if strings.HasPrefix(entry.path, "pages/") || !pythonImports(entry.content, "streamlit") {
			continue
		}
		found = append(found, entry)
	}
	if len(found) == 0 {
		return pythonEntry{}, false
	}
	sort.SliceStable(found, func(i, j int) bool {
		rank := func(entry pythonEntry) int {
			switch strings.ToLower(path.Base(entry.path)) {
			case "streamlit_app.py":
				return 0
			case "app.py":
				return 1
			case "main.py":
				return 2
			case "home.py", "dashboard.py", "hello.py":
				return 3
			}
			if !strings.Contains(entry.path, "/") && entryContains(entry.content, "st.set_page_config") {
				return 4
			}
			return 5
		}
		di, dj := strings.Count(found[i].path, "/"), strings.Count(found[j].path, "/")
		if di != dj {
			return di < dj
		}
		return rank(found[i]) < rank(found[j])
	})
	return found[0], true
}

func resolveFlask(ctx pythonResolveContext) pythonResolution {
	options := ""
	if ctx.deps.declares("flask-socketio") {
		// Flask-SocketIO keeps each client on one process; gunicorn's
		// default worker blocks, so a single worker serves them on threads,
		// or on eventlet or gevent when those are installed.
		switch {
		case ctx.deps.has("eventlet"):
			options = "-k eventlet -w 1"
		case ctx.deps.has("gevent-websocket"):
			options = "-k geventwebsocket.gunicorn.workers.GeventWebSocketWorker -w 1"
		default:
			options = "-k gthread --threads 100 -w 1"
		}
	}
	object, ok := findPythonObject(ctx.entries, `(?:flask\.)?Flask`, "Flask")
	switch {
	case !ok:
		return pythonResolution{Confidence: ConfidenceLow, Decisions: []string{"name the Flask application for gunicorn (module:app)"}}
	case object.factory && object.arguments:
		return pythonResolution{Confidence: ConfidenceLow, found: true, Evidence: []DetectionEvidence{objectEvidence("Flask", object)},
			Decisions: []string{"the Flask factory " + object.name + " needs arguments; name the application object for gunicorn"}}
	case object.factory:
		return pythonResolution{Start: gunicornCommand(pythonTargetFor(object.entry, ctx.files), object.name+"()", 8000, options), found: true,
			Evidence: []DetectionEvidence{objectEvidence("Flask", object)}}
	}
	return pythonResolution{Start: gunicornCommand(pythonTargetFor(object.entry, ctx.files), object.name, 8000, options), found: true,
		Evidence: []DetectionEvidence{objectEvidence("Flask", object)}}
}

// pythonFrameworkEnv is what a framework's server needs from its environment
// to answer from inside a container: Gradio and Dash bind loopback unless
// told otherwise.
func pythonFrameworkEnv(name string) []string {
	switch name {
	case "gradio":
		return []string{"GRADIO_SERVER_NAME=0.0.0.0"}
	case "dash":
		return []string{"HOST=0.0.0.0"}
	}
	return nil
}

// matchPythonFramework is the first framework in catalogue order the project
// declares, which is what the recipe knows without reading the source: it
// decides only the environment the framework's server needs.
func matchPythonFramework(deps pythonDependencies) *pythonFramework {
	for index := range pythonFrameworks {
		if deps.declares(pythonFrameworks[index].Dependency) && !pythonFrameworks[index].entryOnly {
			return &pythonFrameworks[index]
		}
	}
	return nil
}

// resolvePythonFramework runs the catalogue over a root: the first framework
// whose application an entry builds wins; failing that, the first declared
// one, with the question its resolution asks.
func resolvePythonFramework(ctx pythonResolveContext) (*pythonFramework, pythonResolution) {
	var fallback *pythonFramework
	var fallbackResolution pythonResolution
	for index := range pythonFrameworks {
		framework := &pythonFrameworks[index]
		if !ctx.deps.declares(framework.Dependency) {
			continue
		}
		resolution := framework.resolve(ctx)
		if resolution.found {
			return framework, resolution
		}
		if fallback == nil && !framework.entryOnly {
			fallback, fallbackResolution = framework, resolution
		}
	}
	return fallback, fallbackResolution
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
// dependencies. A declared start (a Procfile web process, a task runner's
// start task) is how the repository says it is served; otherwise a
// recognised framework supplies the process and the port, and anything else
// needs the operator to say how it runs.
func pythonCandidate(marker *detectedMarkers, entries []pythonEntry, rootLabel string) DetectedCandidate {
	project := readPythonProject(marker.pythonFiles)
	source := marker.python
	if source == nil {
		source = &pythonSource{}
	}
	entries = slices.DeleteFunc(mergePythonEntries(entries, source.scripts), func(entry pythonEntry) bool {
		return !pythonShellPathRE.MatchString(entry.path)
	})
	deps := project.deps
	candidate := DetectedCandidate{
		Name: "Python service in " + rootLabel, Profile: ProfileService, Confidence: ConfidenceMedium,
		Framework: "python", Recipe: "python",
		Evidence:      []DetectionEvidence{{Path: joinRoot(marker.root, deps.source), Reason: "Python dependency manifest"}},
		NeedsDecision: []string{},
	}
	if project.lock != nil || project.pipfileLock.present {
		candidate.Evidence[0].Reason = "locked Python dependency input"
	}
	python := &DetectedPython{}
	candidate.Python = python
	candidate.UnpinnedDependencies = deps.unpinned
	versionInputs := pythonVersionInputsFor("", source.versionFiles, project, runtime.GOARCH)
	if versionInputs.constraint != "" && len(versionInputs.constraint) <= 128 && !strings.ContainsAny(versionInputs.constraint, "\x00\r\n") {
		candidate.PythonRequires = versionInputs.constraint
	}
	choice, versionErr := resolvePythonVersion(versionInputs)
	if versionErr != nil {
		candidate.RecipeIssue = versionErr.Error()
	} else {
		candidate.PythonVersion = choice.version
		python.VersionSource = boundedText(choice.source, 256)
		python.VersionRaised = choice.raised
		python.VersionLimited = boundedList(choice.limited, 8)
		if choice.file != "" {
			// Version files are named by their place in the checkout, since
			// one above the root may decide; manifests are the root's own.
			file := choice.file
			switch file {
			case "pyproject.toml", "uv.lock", "setup.py", "Pipfile", "runtime.txt", "environment.yml", "environment.yaml":
				file = joinRoot(marker.root, file)
			}
			candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: file, Reason: "Python " + choice.version + " from " + boundedText(choice.source, 200)})
		}
		if len(choice.limited) > 0 {
			candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, deps.source),
				Reason: boundedText(strings.Join(boundedNames(choice.limited), ", ")+" publish no wheels for Python "+defaultPythonRecipeVersion+"; using "+choice.version, 480)})
		}
		if choice.patch != "" {
			candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, ".python-version"), Reason: choice.patch + " is served by the catalogue's " + choice.version + " image"})
		}
	}
	python.WheelBlockers = map[string][]string{}
	for minor, blockers := range versionInputs.blockers {
		python.WheelBlockers["3."+strconv.Itoa(minor)] = boundedList(blockers, 8)
	}
	if len(python.WheelBlockers) == 0 {
		python.WheelBlockers = nil
	}
	files := pythonRootFiles{managePy: marker.managePy, procfile: marker.procfile, source: source}
	settings := ""
	if deps.declares("django") {
		files.django = readDjangoFacts(source, deps)
		python.Django = files.django
		settings = source.djangoSettingsText
	}
	ctx := pythonResolveContext{deps: deps, files: files, entries: entries}
	framework, resolution := resolvePythonFramework(ctx)
	if framework != nil {
		candidate.Name = framework.Label + " application in " + rootLabel
		candidate.Framework = framework.Name
		candidate.Profile, candidate.Port = ProfileWeb, framework.Port
		if resolution.Port > 0 {
			candidate.Port = resolution.Port
		}
		candidate.Confidence = ConfidenceHigh
		if resolution.Confidence != "" {
			candidate.Confidence = resolution.Confidence
		}
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, deps.source), Reason: framework.Label + " dependency"})
		for _, evidence := range resolution.Evidence {
			evidence.Path = joinRoot(marker.root, evidence.Path)
			candidate.Evidence = append(candidate.Evidence, evidence)
		}
		candidate.NeedsDecision = append(candidate.NeedsDecision, resolution.Decisions...)
		candidate.StartCommand = resolution.Start
		if resolution.RecipeIssue != "" && candidate.RecipeIssue == "" {
			candidate.RecipeIssue = resolution.RecipeIssue
		}
	}
	declared, declaredEvidence := pythonDeclaredStart(marker, project, candidate.StartCommand)
	switch {
	case declared != "":
		// The repository has said how it is served: the framework's guesses
		// about its application object are answered, and a site folder
		// beside it is not what it serves.
		if framework == nil {
			candidate.Port = 8000
		}
		candidate.StartCommand = pythonDeclaredStartSteps(declared, candidate.Framework, files.django)
		candidate.Profile = ProfileWeb
		candidate.Confidence = ConfidenceHigh
		candidate.NeedsDecision = pythonAnsweredDecisions(candidate.NeedsDecision)
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, declaredEvidence.Path), Reason: declaredEvidence.Reason})
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
			candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, script), Reason: "script at the package root"})
			candidate.NeedsDecision = append(candidate.NeedsDecision, "confirm whether this script serves HTTP (web application) or runs as a worker, and its port")
		} else {
			candidate.NeedsDecision = append(candidate.NeedsDecision, "confirm ASGI/WSGI start command, port, and readiness check")
		}
	}
	if declared == "" && framework != nil {
		if where := pythonWebsocketSource(entries, files.django, source); where != "" {
			if single := pythonSingleWorker(candidate.StartCommand); single != candidate.StartCommand {
				candidate.StartCommand = single
				candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, where),
					Reason: "one uvicorn worker: websocket clients and what they share live in one process"})
			}
		}
	}
	applyPythonProjectFacts(&candidate, marker, project, source, entries, settings, choice.version)
	return candidate
}

// pythonWebsocketSource names the file that serves websockets: a route in an
// entry, or the ASGI module that routes Channels consumers.
func pythonWebsocketSource(entries []pythonEntry, django *DetectedDjango, source *pythonSource) string {
	for _, entry := range sortedPythonEntries(entries) {
		if pythonWebsocketRouteRE.Match(entry.content) {
			return entry.path
		}
	}
	if django == nil || !django.Channels {
		return ""
	}
	names := make([]string, 0, len(source.manage))
	for name, content := range source.manage {
		if path.Base(name) == "asgi.py" && pythonProtocolRouter.Match(content) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

// pythonSingleWorker keeps a generated uvicorn command on one process. A
// websocket application holds its connections — and usually the list it
// broadcasts to, FastAPI's documented ConnectionManager or Channels'
// in-memory layer — in the process, so with WEB_CONCURRENCY workers clients
// on different workers would never see each other's messages.
func pythonSingleWorker(start string) string {
	segments := strings.Split(start, "&&")
	for index := len(segments) - 1; index >= 0; index-- {
		fields := strings.Fields(segments[index])
		if len(fields) > 0 && fields[0] == "uvicorn" {
			if !pythonWorkersFlagRE.MatchString(segments[index]) {
				segments[index] = strings.TrimRight(segments[index], " ") + " --workers 1"
			}
			return strings.Join(segments, "&&")
		}
	}
	return start
}

// pythonDeclaredStart is the start command the repository declares, and the
// evidence naming where: the Procfile's web process, then a task runner's
// start task in pyproject. Only a plain command counts; a task that chains
// other tasks is a question, not a command. The Procfile is what a platform
// ran in production; a task is as often what a developer runs, so one that
// starts a development server, binds loopback, or runs a script where the
// framework has a production server (Flask's app.run(), a uvicorn.run() on
// a fixed port) leaves the framework's command in place.
func pythonDeclaredStart(marker *detectedMarkers, project pythonProject, frameworkStart string) (string, DetectionEvidence) {
	if web := procfileProcess(marker.procfile, "web"); web != "" && rejectPlanSecretLiteral("Procfile web process", web) == nil {
		return web, DetectionEvidence{Path: "Procfile", Reason: "web process: " + boundedEvidence(web)}
	}
	for _, task := range project.pyproject.tasks {
		if task.composite || task.command == "" {
			continue
		}
		command := strings.TrimSpace(task.command)
		for _, runner := range []string{"pdm run ", "hatch run ", "poetry run ", "uv run "} {
			if strings.HasPrefix(command, runner) {
				command = strings.TrimSpace(strings.TrimPrefix(command, runner))
			}
		}
		if command == "" || strings.ContainsAny(command, "\n\r") || rejectPlanSecretLiteral("pyproject task", command) != nil ||
			!pythonTaskServesProduction(command, frameworkStart) {
			continue
		}
		table := map[string]string{"pdm": "tool.pdm.scripts", "poe": "tool.poe.tasks", "taskipy": "tool.taskipy.tasks", "hatch": "tool.hatch.envs.default.scripts"}[task.runner]
		return command, DetectionEvidence{Path: "pyproject.toml", Reason: "[" + table + "] " + task.name + ": " + boundedEvidence(command)}
	}
	return "", DetectionEvidence{}
}

// pythonTaskServesProduction says a task's command can serve a deployment:
// not a development server (runserver, flask run, fastapi dev, --reload),
// not bound to loopback where the recipe's environment does not move it, and
// not a script run directly when the framework names a production server.
func pythonTaskServesProduction(command, frameworkStart string) bool {
	listen := parseCommandListen(command, nil)
	if listen.devServer != "" {
		return false
	}
	if host, _ := listen.boundHost(map[string]bool{"UVICORN_HOST": true, "FLASK_RUN_HOST": true}); loopbackHost(host) {
		return false
	}
	return frameworkStart == "" || !strings.HasSuffix(listen.tool, ".py") || listen.tool == "manage.py"
}

// pythonDeclaredStartSteps keeps what a platform would have done before the
// declared process: Heroku's Python buildpack runs collectstatic at build
// time, so a Procfile's Django web process never names it, and WhiteNoise
// then serves an empty STATIC_ROOT.
func pythonDeclaredStartSteps(start, framework string, django *DetectedDjango) string {
	if framework != "django" || django == nil || !django.WhiteNoise || !django.StaticRoot || strings.Contains(start, "collectstatic") {
		return start
	}
	manage := joinRoot(django.ManageDir, "manage.py")
	return "python " + manage + " collectstatic --noinput && " + start
}

// pythonAnsweredDecisions drops the questions a declared start command
// answers: which object the server imports, and how it is started.
func pythonAnsweredDecisions(decisions []string) []string {
	kept := []string{}
	for _, decision := range decisions {
		lower := strings.ToLower(decision)
		if strings.Contains(lower, "name the ") || strings.Contains(lower, "start command") || strings.Contains(lower, "module:") {
			continue
		}
		kept = append(kept, decision)
	}
	return kept
}

func mergePythonEntries(entries, extra []pythonEntry) []pythonEntry {
	seen := map[string]bool{}
	result := make([]pythonEntry, 0, len(entries)+len(extra))
	for _, list := range [][]pythonEntry{entries, extra} {
		for _, entry := range list {
			if seen[entry.path] {
				continue
			}
			seen[entry.path] = true
			result = append(result, entry)
		}
	}
	return result
}

func boundedList(values []string, limit int) []string {
	if len(values) == 0 {
		return nil
	}
	result := []string{}
	for _, value := range values {
		if len(result) == limit {
			break
		}
		if value = boundedText(strings.TrimSpace(value), 256); value != "" && !strings.ContainsAny(value, "\x00\r\n") {
			result = append(result, value)
		}
	}
	return result
}
