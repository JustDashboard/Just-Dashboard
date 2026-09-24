package deploy

import (
	"bytes"
	"path"
	"regexp"
	"sort"
	"strings"
)

// The readiness scanner reads what a source says about how it is served and
// how it proves it is healthy: health routes it registers, the health
// endpoints its framework or its previous platform declares, whether anything
// in it listens at all, and the host and HTTPS settings that decide how it
// answers a request. Everything is read as bounded text, never evaluated, and
// under its own budget apart from detection's limits, so a large repository
// only stops contributing facts. What it finds is attributed to a file; the
// candidate a file belongs to is decided after the walk, when the roots are
// known (detect_readiness.go).

// readinessFact is one thing a file says. Kind is one of the fact* names
// below; value is the path, module, host or call it names; stack is the
// language or framework that says it, since one root can hold another
// stack's files; label is how evidence names it.
type readinessFact struct {
	file  string
	kind  string
	value string
	stack string
	label string
}

const (
	// A health route registered in code: a literal the application's router
	// was given, whose final prefix detection cannot always see.
	factCodeHealth = "code_health"
	// A health endpoint a framework, a platform file or a route file
	// declares, whose path is exact.
	factDeclaredHealth = "declared_health"
	factPlatformHealth = "platform_health"
	factRouteFile      = "route_file"
	factRootRoute      = "root_route"
	factRootStatic     = "root_static"
	factListen         = "listen"
	factSocketMode     = "socket_mode"
	factPythonServes   = "python_serves"
	factPythonWorker   = "python_worker"
	factCeleryApp      = "celery_app"
	factDramatiqActor  = "dramatiq_actor"
	factArqSettings    = "arq_settings"
	factSchedulerLoop  = "scheduler_loop"
	factModelLoad      = "model_load"
	factNextBasePath   = "next_base_path"
	factNestPrefix     = "nest_global_prefix"
	factRailsForceSSL  = "rails_force_ssl"
	factRailsAssumeSSL = "rails_assume_ssl"
	factRailsAPIOnly   = "rails_api_only"
	factRailsHost      = "rails_host"
	factRailsHostsOpen = "rails_hosts_open"
	factDjangoSettings = "django_settings_module"
	factDjangoURLConf  = "django_root_urlconf"
	factDjangoHosts    = "django_allowed_hosts"
	factDjangoSSL      = "django_ssl_redirect"
	factDjangoProxySSL = "django_proxy_ssl_header"
	factDjangoURL      = "django_url"
	factJVMSetting     = "jvm_setting"
)

// Budgets: configuration files are few and decisive, so they have their own
// allowance a large source tree cannot use up.
const (
	readinessConfigMaxFiles = 96
	readinessConfigMaxBytes = 2 << 20
	readinessSourceMaxFiles = 400
	readinessSourceMaxBytes = 3 << 20
	readinessMaxFileBytes   = 128 << 10
	readinessMaxFacts       = 4096
)

type readinessScanner struct {
	configFiles, sourceFiles int
	configBytes, sourceBytes int64
	facts                    []readinessFact
}

func newReadinessScanner() *readinessScanner { return &readinessScanner{} }

// visit is called for every regular file the detector walks. It records
// path-only facts, and reads the file when it is one the scanner has a
// question for and the budget allows.
func (s *readinessScanner) visit(file, rel string) {
	rel = path.Clean(rel)
	name := strings.ToLower(path.Base(rel))
	if readinessSkippedPath(rel) {
		return
	}
	if routeFileCandidate(rel, name) {
		s.add(readinessFact{file: rel, kind: factRouteFile, value: rel})
	}
	config := readinessConfigFile(rel, name)
	if !config && !readinessSourceFile(name) {
		return
	}
	if config {
		if s.configFiles >= readinessConfigMaxFiles || s.configBytes >= readinessConfigMaxBytes {
			return
		}
	} else if s.sourceFiles >= readinessSourceMaxFiles || s.sourceBytes >= readinessSourceMaxBytes {
		return
	}
	content, n, err := readDetectionFile(file, readinessMaxFileBytes)
	if config {
		s.configFiles++
		s.configBytes += n
	} else {
		s.sourceFiles++
		s.sourceBytes += n
	}
	if err != nil {
		return
	}
	s.scan(rel, name, content)
}

func (s *readinessScanner) add(fact readinessFact) {
	if len(s.facts) < readinessMaxFacts {
		s.facts = append(s.facts, fact)
	}
}

// readinessSkippedPath leaves out what is not the application: tests,
// fixtures, examples and documentation, as environment discovery does.
func readinessSkippedPath(rel string) bool {
	for _, segment := range strings.Split(path.Dir(rel), "/") {
		if envSkippedDirs[segment] {
			return true
		}
	}
	name := strings.ToLower(path.Base(rel))
	return strings.HasSuffix(name, ".d.ts") || strings.HasSuffix(name, ".min.js") || strings.HasSuffix(name, "_test.go") ||
		strings.HasPrefix(name, "test_") || name == "conftest.py" || strings.Contains(name, ".test.") ||
		strings.Contains(name, ".spec.") || strings.Contains(name, ".stories.")
}

func readinessConfigFile(rel, name string) bool {
	lower := strings.ToLower(rel)
	for _, suffix := range []string{
		"config/routes.rb", "config/environments/production.rb", "config/application.rb", "config/deploy.yml",
		"bootstrap/app.php", "src/main/resources/application.properties", "src/main/resources/application.yml",
		"src/main/resources/application.yaml",
	} {
		if lower == suffix || strings.HasSuffix(lower, "/"+suffix) {
			return true
		}
	}
	switch name {
	case "fly.toml", "render.yaml", "render.yml", "railway.json", "railway.toml", "program.cs", "startup.cs",
		"settings.py", "urls.py", "wsgi.py", "asgi.py", "manage.py":
		return true
	}
	if strings.HasPrefix(name, "next.config.") {
		return true
	}
	// A settings package: settings/production.py and its siblings.
	return strings.HasSuffix(name, ".py") && path.Base(path.Dir(rel)) == "settings"
}

func readinessSourceFile(name string) bool {
	switch path.Ext(name) {
	case ".js", ".mjs", ".cjs", ".ts", ".mts", ".py", ".go", ".rs", ".java", ".kt":
		return true
	}
	return false
}

// healthRouteNames are the paths a service conventionally answers health on.
var healthRouteNames = map[string]bool{
	"health": true, "healthz": true, "healthcheck": true, "health-check": true, "_health": true,
	"livez": true, "readyz": true, "ready": true, "ping": true, "up": true,
}

// isHealthPath reports whether a route is one of the conventional health
// paths, directly or under /api (optionally versioned).
func isHealthPath(route string) bool {
	route = strings.Trim(strings.ToLower(route), "/")
	if route == "" {
		return false
	}
	segments := strings.Split(route, "/")
	last := segments[len(segments)-1]
	if !healthRouteNames[last] {
		return false
	}
	switch len(segments) {
	case 1:
		return true
	case 2:
		return segments[0] == "api" || segments[0] == "health" || segments[0] == "_health"
	case 3:
		return segments[0] == "api" && apiVersionRE.MatchString(segments[1])
	}
	return false
}

var apiVersionRE = regexp.MustCompile(`^v[0-9]+$`)

// routeFileCandidate keeps the paths a file-routed framework could serve a
// health route from; which framework's convention applies is decided once
// the file's candidate is known.
func routeFileCandidate(rel, name string) bool {
	switch path.Ext(name) {
	case ".js", ".mjs", ".cjs", ".ts", ".mts", ".jsx", ".tsx":
	default:
		return false
	}
	lower := strings.ToLower(rel)
	for _, token := range []string{"health", "livez", "readyz", "/ping", "ping."} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

var (
	jsListenRE        = regexp.MustCompile(`\.listen\s*\(|\bcreateServer\s*\(|\bBun\.serve\s*\(|\bDeno\.serve\s*\(|\bserve\s*\(\s*\{[^}]{0,200}\bfetch\b`)
	jsSocketModeRE    = regexp.MustCompile(`\bsocketMode\s*:\s*true`)
	jsRouteRE         = regexp.MustCompile(`\.\s*(?:get|all|head|route|use)\s*\(\s*['"` + "`" + `](/[A-Za-z0-9_./-]*)['"` + "`" + `]`)
	nestControllerRE  = regexp.MustCompile(`@Controller\(\s*(?:['"]([^'"]*)['"])?\s*\)`)
	nestGetRE         = regexp.MustCompile(`@Get\(\s*(?:['"]([^'"]*)['"])?\s*\)`)
	nestPrefixRE      = regexp.MustCompile(`setGlobalPrefix\(\s*['"]([^'"]*)['"]\s*(,[^)]*)?\)`)
	pythonAppVarRE    = regexp.MustCompile(`(?m)^[ \t]*(\w+)\s*(?::[^=\n]+)?=\s*(?:\w+\.)?(FastAPI|Flask|APIFlask|Quart|Sanic|Starlette|Litestar|Bottle)\(`)
	pythonDecoratorRE = regexp.MustCompile(`(?m)^[ \t]*@(\w+)\.(?:get|route|api_route|head)\(\s*['"](/[^'"]*)['"]`)
	pythonRouteRE     = regexp.MustCompile(`\bRoute\(\s*['"](/[^'"]*)['"]`)
	pythonMountRootRE = regexp.MustCompile(`\.mount\(\s*['"]/['"]`)
	pythonServesRE    = regexp.MustCompile(`\brun_app\(|\buvicorn\.run\(|\.serve_forever\(|\bHTTPServer\(|\brun_webhook\(|\bstart_webhook\(|\bweb\.TCPSite\(|\bwaitress\.serve\(|\bsocketio\.run\(|\.run\([^)\n]*\bhost\s*=`)
	pythonRunRE       = regexp.MustCompile(`\.run\(|\brun_polling\(|\bstart_polling\(|\binfinity_polling\(|\.polling\(|\bidle\(\)|\brun_until_disconnected\(|\.start\(\)|\basyncio\.run\(`)
	pythonCeleryRE    = regexp.MustCompile(`=\s*Celery\(`)
	pythonDramatiqRE  = regexp.MustCompile(`@dramatiq\.actor|\bdramatiq\.set_broker\(`)
	pythonArqRE       = regexp.MustCompile(`(?m)^class\s+WorkerSettings\b`)
	pythonSchedulerRE = regexp.MustCompile(`\bBlockingScheduler\(|\brun_pending\(`)
	pythonModelLoadRE = regexp.MustCompile(`\b(?:\w+\.)*from_pretrained\(|\bpipeline\(|\bSentenceTransformer\(|\bCrossEncoder\(|\bwhisper\.load_model\(|\bWhisperModel\(|\bYOLO\(|\bhf_hub_download\(|\bsnapshot_download\(`)
	goRouteRE         = regexp.MustCompile(`\b(?:HandleFunc|Handle|GET|Get|HEAD|Head|Any)\(\s*"(?:(?:GET|HEAD)\s+)?(/[^"\s]*)"`)
	rustRouteRE       = regexp.MustCompile(`\.route\(\s*"(/[^"]*)"|#\[(?:get|head)\(\s*"(/[^"]*)"|\bweb::resource\(\s*"(/[^"]*)"|\.at\(\s*"(/[^"]*)"|\bwarp::path!?\(\s*"([A-Za-z0-9_-]+)"`)
	jvmRouteRE        = regexp.MustCompile(`@(?:Get|Request)Mapping\(\s*(?:(?:value|path)\s*=\s*)?\{?\s*"(/?[^"]*)"|@Path\(\s*"(/?[^"]*)"\s*\)|\bget\(\s*"(/[^"]*)"`)
	dotnetDeclaredRE  = regexp.MustCompile(`\b(?:MapHealthChecks|UseHealthChecks)\(\s*"(/[^"]*)"`)
	dotnetRouteRE     = regexp.MustCompile(`\bMapGet\(\s*"(/[^"]*)"`)
	railsHealthRE     = regexp.MustCompile(`(?m)^\s*get\s+['"]([^'"]+)['"]\s*(?:=>|,\s*to:)\s*['"]rails/health#show['"]`)
	railsForceSSLRE   = regexp.MustCompile(`(?m)^\s*config\.force_ssl\s*=\s*true\b`)
	railsAssumeSSLRE  = regexp.MustCompile(`(?m)^\s*config\.assume_ssl\s*=\s*true\b`)
	railsAPIOnlyRE    = regexp.MustCompile(`(?m)^\s*config\.api_only\s*=\s*true\b`)
	railsHostsRE      = regexp.MustCompile(`(?m)^\s*config\.hosts\s*(?:<<|\+=|=|\.push\(|\.concat\()\s*(.*)$`)
	railsHostsOpenRE  = regexp.MustCompile(`(?m)^\s*config\.hosts\s*(?:\.clear\b|=\s*nil\b)`)
	laravelHealthRE   = regexp.MustCompile(`\bhealth\s*:\s*['"](/[^'"]*)['"]`)
	nextBasePathRE    = regexp.MustCompile(`\bbasePath\s*:\s*['"](/[^'"]*)['"]`)
	renderHealthRE    = regexp.MustCompile(`(?m)^\s*healthCheckPath\s*:\s*['"]?(/[^\s'"#]*)`)
	railwayHealthRE   = regexp.MustCompile(`["']?healthcheckPath["']?\s*[:=]\s*["'](/[^"']*)["']`)
	tomlPathRE        = regexp.MustCompile(`^\s*path\s*=\s*["'](/[^"']*)["']`)
	stringLiteralRE   = regexp.MustCompile(`"([^"\\]*)"|'([^'\\]*)'`)
	djangoURLConfRE   = regexp.MustCompile(`(?m)^ROOT_URLCONF\s*=\s*['"]([\w.]+)['"]`)
	djangoHostsRE     = regexp.MustCompile(`(?m)^ALLOWED_HOSTS\s*(?::[^=\n]+)?=\s*[\[(]([^\])]*)[\])]\s*$`)
	djangoSSLRE       = regexp.MustCompile(`(?m)^SECURE_SSL_REDIRECT\s*=\s*True\b`)
	djangoProxySSLRE  = regexp.MustCompile(`(?m)^SECURE_PROXY_SSL_HEADER\s*=`)
	djangoModuleRE    = regexp.MustCompile(`DJANGO_SETTINGS_MODULE['"]\s*,\s*['"]([\w.]+)['"]`)
	djangoPathRE      = regexp.MustCompile(`\b(?:path|re_path)\(\s*r?['"]\^?([^'"$]*)\$?['"]\s*,\s*((?:[^()\n]|\([^()\n]*\)){0,160})`)
)

func (s *readinessScanner) scan(rel, name string, content []byte) {
	switch {
	case strings.HasSuffix(rel, "config/routes.rb"):
		for _, match := range railsHealthRE.FindAllSubmatch(content, -1) {
			s.add(readinessFact{file: rel, kind: factDeclaredHealth, value: "/" + strings.Trim(string(match[1]), "/"), stack: "rails", label: "Rails health route in config/routes.rb"})
		}
		return
	case strings.HasSuffix(rel, "config/environments/production.rb") || strings.HasSuffix(rel, "config/application.rb"):
		s.scanRailsConfig(rel, content)
		return
	case strings.HasSuffix(rel, "config/deploy.yml"):
		if route := kamalHealthcheckPath(content); route != "" {
			s.add(readinessFact{file: rel, kind: factPlatformHealth, value: route, label: "Kamal proxy healthcheck in config/deploy.yml"})
		}
		return
	case strings.HasSuffix(rel, "bootstrap/app.php"):
		if match := laravelHealthRE.FindSubmatch(content); match != nil {
			s.add(readinessFact{file: rel, kind: factDeclaredHealth, value: string(match[1]), stack: "laravel", label: "Laravel health route in bootstrap/app.php"})
		}
		return
	case strings.HasPrefix(name, "next.config."):
		if match := nextBasePathRE.FindSubmatch(content); match != nil {
			s.add(readinessFact{file: rel, kind: factNextBasePath, value: strings.TrimSuffix(string(match[1]), "/")})
		}
		return
	case name == "fly.toml":
		for _, route := range flyHealthPaths(content) {
			s.add(readinessFact{file: rel, kind: factPlatformHealth, value: route, label: "HTTP check in fly.toml"})
		}
		return
	case name == "render.yaml" || name == "render.yml":
		for _, match := range renderHealthRE.FindAllSubmatch(content, -1) {
			s.add(readinessFact{file: rel, kind: factPlatformHealth, value: string(match[1]), label: "healthCheckPath in " + path.Base(rel)})
		}
		return
	case name == "railway.json" || name == "railway.toml":
		for _, match := range railwayHealthRE.FindAllSubmatch(content, -1) {
			s.add(readinessFact{file: rel, kind: factPlatformHealth, value: string(match[1]), label: "healthcheckPath in " + path.Base(rel)})
		}
		return
	case strings.HasPrefix(name, "application.") && strings.Contains(rel, "src/main/resources/"):
		for key, value := range jvmSettings(name, content) {
			s.add(readinessFact{file: rel, kind: factJVMSetting, value: value, label: key})
		}
		return
	}
	switch path.Ext(name) {
	case ".js", ".mjs", ".cjs", ".ts", ".mts":
		s.scanJavaScript(rel, content)
	case ".py":
		s.scanPython(rel, name, content)
	case ".go":
		s.scanCodeRoutes(rel, content, goRouteRE, "go", "Go")
	case ".rs":
		s.scanCodeRoutes(rel, content, rustRouteRE, "rust", "Rust")
	case ".java", ".kt":
		s.scanCodeRoutes(rel, content, jvmRouteRE, "jvm", "JVM")
	case ".cs":
		for _, match := range dotnetDeclaredRE.FindAllSubmatch(content, -1) {
			s.add(readinessFact{file: rel, kind: factDeclaredHealth, value: string(match[1]), stack: "dotnet", label: "ASP.NET health checks endpoint in " + path.Base(rel)})
		}
		s.scanCodeRoutes(rel, content, dotnetRouteRE, "dotnet", ".NET")
	}
}

var healthTokens = [][]byte{[]byte("health"), []byte("livez"), []byte("readyz"), []byte("ready"), []byte("ping"), []byte("/up")}

func mentionsHealth(content []byte) bool {
	lower := bytes.ToLower(content)
	for _, token := range healthTokens {
		if bytes.Contains(lower, token) {
			return true
		}
	}
	return false
}

// scanCodeRoutes records the health routes a router is given in code. Every
// capture group of the expression is a candidate path; the first non-empty
// one is the route.
func (s *readinessScanner) scanCodeRoutes(rel string, content []byte, expression *regexp.Regexp, stack, language string) {
	if !mentionsHealth(content) {
		return
	}
	for _, match := range expression.FindAllSubmatch(content, -1) {
		for _, group := range match[1:] {
			if len(group) == 0 {
				continue
			}
			route := "/" + strings.TrimPrefix(string(group), "/")
			if isHealthPath(route) {
				s.add(readinessFact{file: rel, kind: factCodeHealth, value: route, stack: stack, label: language + " route " + route + " in " + path.Base(rel)})
			}
			break
		}
	}
}

func (s *readinessScanner) scanJavaScript(rel string, content []byte) {
	if jsListenRE.Match(content) {
		s.add(readinessFact{file: rel, kind: factListen})
	}
	if jsSocketModeRE.Match(content) {
		s.add(readinessFact{file: rel, kind: factSocketMode})
	}
	if match := nestPrefixRE.FindSubmatch(content); match != nil {
		prefix := "/" + strings.Trim(string(match[1]), "/")
		// A health route excluded from the prefix keeps its own path.
		if bytes.Contains(bytes.ToLower(match[2]), []byte("health")) {
			prefix = "/"
		}
		s.add(readinessFact{file: rel, kind: factNestPrefix, value: prefix})
	}
	if !mentionsHealth(content) {
		return
	}
	s.scanCodeRoutes(rel, content, jsRouteRE, "js", "JavaScript")
	if controller := nestControllerRE.FindSubmatch(content); controller != nil {
		for _, get := range nestGetRE.FindAllSubmatch(content, -1) {
			route := joinURLPath(string(controller[1]), string(get[1]))
			if isHealthPath(route) {
				s.add(readinessFact{file: rel, kind: factCodeHealth, value: route, stack: "nest", label: "NestJS controller " + route + " in " + path.Base(rel)})
				break
			}
		}
	}
}

func (s *readinessScanner) scanPython(rel, name string, content []byte) {
	// Django's generated urls.py documents `path('', views.home)` in its
	// docstring; examples are not routes.
	content = stripPythonDocumentation(content)
	imports := map[string]bool{}
	for _, match := range pythonImportRE.FindAllSubmatch(content, -1) {
		imports[string(match[1])+string(match[2])] = true
	}
	if match := djangoModuleRE.FindSubmatch(content); match != nil {
		s.add(readinessFact{file: rel, kind: factDjangoSettings, value: string(match[1])})
	}
	if name == "settings.py" || path.Base(path.Dir(rel)) == "settings" {
		s.scanDjangoSettings(rel, content)
	}
	if name == "urls.py" {
		s.scanDjangoURLs(rel, content)
	}
	apps := map[string]bool{}
	for _, match := range pythonAppVarRE.FindAllSubmatch(content, -1) {
		apps[string(match[1])] = true
	}
	for _, match := range pythonDecoratorRE.FindAllSubmatch(content, -1) {
		route := string(match[2])
		switch {
		case route == "/" && apps[string(match[1])]:
			s.add(readinessFact{file: rel, kind: factRootRoute, value: string(match[1])})
		case isHealthPath(route):
			s.add(readinessFact{file: rel, kind: factCodeHealth, value: route, stack: "python", label: "Python route " + route + " in " + path.Base(rel)})
		}
	}
	for _, match := range pythonRouteRE.FindAllSubmatch(content, -1) {
		if route := string(match[1]); route == "/" && len(apps) > 0 {
			s.add(readinessFact{file: rel, kind: factRootRoute})
		} else if isHealthPath(route) {
			s.add(readinessFact{file: rel, kind: factCodeHealth, value: route, stack: "python", label: "Python route " + route + " in " + path.Base(rel)})
		}
	}
	if pythonMountRootRE.Match(content) {
		s.add(readinessFact{file: rel, kind: factRootStatic})
	}
	if pythonServesRE.Match(content) {
		s.add(readinessFact{file: rel, kind: factPythonServes})
	}
	runs := pythonRunRE.Match(content)
	for _, module := range pythonWorkerModuleNames {
		// Bolt over HTTP listens like any server; only socket mode connects out.
		if module == "slack_bolt" && !bytes.Contains(content, []byte("SocketModeHandler")) {
			continue
		}
		if imports[module] && runs {
			s.add(readinessFact{file: rel, kind: factPythonWorker, value: module})
		}
	}
	if pythonCeleryRE.Match(content) {
		s.add(readinessFact{file: rel, kind: factCeleryApp})
	}
	if imports["dramatiq"] && pythonDramatiqRE.Match(content) {
		s.add(readinessFact{file: rel, kind: factDramatiqActor})
	}
	if pythonArqRE.Match(content) {
		s.add(readinessFact{file: rel, kind: factArqSettings})
	}
	if (imports["apscheduler"] || imports["schedule"]) && pythonSchedulerRE.Match(content) {
		s.add(readinessFact{file: rel, kind: factSchedulerLoop})
	}
	if pythonImportsModelLibrary(imports) {
		if call := startupModelLoad(content); call != "" {
			s.add(readinessFact{file: rel, kind: factModelLoad, value: call})
		}
	}
}

// stripPythonDocumentation blanks triple-quoted strings and comment lines,
// keeping line breaks so what follows keeps its indentation and position.
func stripPythonDocumentation(content []byte) []byte {
	result := make([]byte, 0, len(content))
	for index := 0; index < len(content); {
		if bytes.HasPrefix(content[index:], []byte(`"""`)) || bytes.HasPrefix(content[index:], []byte(`'''`)) {
			quote := content[index : index+3]
			end := bytes.Index(content[index+3:], quote)
			if end < 0 {
				end = len(content) - index - 3
			}
			for _, character := range content[index : index+3+end] {
				if character == '\n' {
					result = append(result, '\n')
				}
			}
			index += 3 + end + 3
			if index > len(content) {
				index = len(content)
			}
			continue
		}
		result = append(result, content[index])
		index++
	}
	lines := bytes.Split(result, []byte("\n"))
	for index, line := range lines {
		if bytes.HasPrefix(bytes.TrimSpace(line), []byte("#")) {
			lines[index] = nil
		}
	}
	return bytes.Join(lines, []byte("\n"))
}

// pythonWorkerModules maps a bot library's import name to what it is.
var pythonWorkerModules = map[string]string{
	"discord": "Discord bot", "nextcord": "Discord bot", "disnake": "Discord bot", "hikari": "Discord bot",
	"interactions": "Discord bot", "telegram": "Telegram bot", "telebot": "Telegram bot", "aiogram": "Telegram bot",
	"pyrogram": "Telegram client", "telethon": "Telegram client", "slack_bolt": "Slack bot", "twitchio": "Twitch bot",
}

// pythonWorkerModuleNames is pythonWorkerModules' keys in a fixed order, so
// a file importing two libraries records them the same way every time.
var pythonWorkerModuleNames = func() []string {
	names := make([]string, 0, len(pythonWorkerModules))
	for name := range pythonWorkerModules {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}()

var pythonModelModules = []string{
	"transformers", "diffusers", "sentence_transformers", "whisper", "faster_whisper", "ultralytics",
	"huggingface_hub", "torch", "timm", "open_clip", "easyocr", "TTS",
}

func pythonImportsModelLibrary(imports map[string]bool) bool {
	for _, module := range pythonModelModules {
		if imports[module] {
			return true
		}
	}
	return false
}

// startupModelLoad finds a model load that runs while the application
// starts rather than on a request: in module-level code (a statement, or a
// block such as `if __name__ == "__main__":` or `with ...:`), or inside a
// function that is a lifespan or startup hook by its name or its decorator.
// A load inside any other function waits for the request that calls it.
func startupModelLoad(content []byte) string {
	// block is what the enclosing top-level statement is: "module" code runs
	// at import, "startup" is a hook, anything else a function or class body.
	block, decorators := "module", ""
	for _, raw := range strings.Split(string(content), "\n") {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !strings.HasPrefix(raw, " ") && !strings.HasPrefix(raw, "\t") {
			lower := strings.ToLower(trimmed)
			switch {
			case strings.HasPrefix(trimmed, "@"):
				decorators += lower
				continue
			case strings.HasPrefix(trimmed, "def ") || strings.HasPrefix(trimmed, "async def "):
				block = "function"
				if hook := lower + decorators; strings.Contains(hook, "lifespan") || strings.Contains(hook, "startup") {
					block = "startup"
				}
				decorators = ""
				continue
			case strings.HasPrefix(trimmed, "class "):
				block, decorators = "class", ""
				continue
			default:
				block, decorators = "module", ""
			}
		}
		if block != "module" && block != "startup" {
			continue
		}
		if call := pythonModelLoadRE.FindString(trimmed); call != "" {
			return strings.TrimSuffix(call, "(")
		}
	}
	return ""
}

func (s *readinessScanner) scanRailsConfig(rel string, content []byte) {
	if railsForceSSLRE.Match(content) {
		s.add(readinessFact{file: rel, kind: factRailsForceSSL})
	}
	if railsAssumeSSLRE.Match(content) {
		s.add(readinessFact{file: rel, kind: factRailsAssumeSSL})
	}
	if railsAPIOnlyRE.Match(content) {
		s.add(readinessFact{file: rel, kind: factRailsAPIOnly})
	}
	if railsHostsOpenRE.Match(content) {
		s.add(readinessFact{file: rel, kind: factRailsHostsOpen})
	}
	for _, match := range railsHostsRE.FindAllSubmatch(content, -1) {
		hosts := literalStrings(string(match[1]))
		// A host built from the environment, or a pattern, cannot be
		// compared with a domain; the allowlist is treated as open.
		if len(hosts) == 0 || strings.Contains(string(match[1]), "ENV") || strings.Contains(string(match[1]), "/") {
			s.add(readinessFact{file: rel, kind: factRailsHostsOpen})
			continue
		}
		for _, host := range hosts {
			s.add(readinessFact{file: rel, kind: factRailsHost, value: host})
		}
	}
}

func (s *readinessScanner) scanDjangoSettings(rel string, content []byte) {
	if match := djangoURLConfRE.FindSubmatch(content); match != nil {
		s.add(readinessFact{file: rel, kind: factDjangoURLConf, value: string(match[1])})
	}
	if match := djangoHostsRE.FindSubmatch(content); match != nil {
		body := string(match[1])
		hosts := literalStrings(body)
		// Only a list of literal strings is the allowlist; one read from the
		// environment is the operator's to set.
		remainder := strings.TrimSpace(strings.Trim(stringLiteralRE.ReplaceAllString(body, ""), " ,\t\n"))
		if remainder == "" || strings.Trim(remainder, ", \t\n") == "" {
			s.add(readinessFact{file: rel, kind: factDjangoHosts, value: strings.Join(hosts, " ")})
		}
	}
	if djangoSSLRE.Match(content) {
		s.add(readinessFact{file: rel, kind: factDjangoSSL})
	}
	if djangoProxySSLRE.Match(content) {
		s.add(readinessFact{file: rel, kind: factDjangoProxySSL})
	}
}

// scanDjangoURLs records the routes a urls.py declares: the root, a health
// route, the admin and django-health-check's include, each as
// "<kind>:<path>".
func (s *readinessScanner) scanDjangoURLs(rel string, content []byte) {
	for _, match := range djangoPathRE.FindAllSubmatch(content, -1) {
		route, target := string(match[1]), string(match[2])
		switch {
		case strings.Contains(target, "admin.site.urls"):
			s.add(readinessFact{file: rel, kind: factDjangoURL, value: "admin:/" + route})
		case strings.Contains(target, "health_check.urls"):
			s.add(readinessFact{file: rel, kind: factDjangoURL, value: "health:/" + route})
		case route == "":
			if !strings.HasPrefix(strings.TrimSpace(target), "include(") {
				s.add(readinessFact{file: rel, kind: factDjangoURL, value: "root:/"})
			}
		case isHealthPath("/" + route):
			s.add(readinessFact{file: rel, kind: factDjangoURL, value: "health:/" + route})
		}
	}
}

func literalStrings(text string) []string {
	var values []string
	for _, match := range stringLiteralRE.FindAllStringSubmatch(text, -1) {
		value := match[1] + match[2]
		if value != "" && len(value) <= 253 && !strings.ContainsAny(value, " \t") {
			values = append(values, value)
		}
	}
	return values
}

// flyHealthPaths reads the path of every HTTP check fly.toml declares, in
// [[http_service.checks]] or [[services.http_checks]] tables.
func flyHealthPaths(content []byte) []string {
	var paths []string
	table := ""
	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "[") {
			table = strings.Trim(line, "[] ")
			continue
		}
		if !strings.HasSuffix(table, "http_service.checks") && !strings.HasSuffix(table, "http_checks") {
			continue
		}
		if match := tomlPathRE.FindStringSubmatch(line); match != nil {
			paths = append(paths, match[1])
		}
	}
	return paths
}

// kamalHealthcheckPath reads proxy.healthcheck.path from a Kamal deploy.yml:
// the first `path:` indented under a `healthcheck:` key.
func kamalHealthcheckPath(content []byte) string {
	lines := strings.Split(string(content), "\n")
	for index, raw := range lines {
		if strings.TrimSpace(raw) != "healthcheck:" {
			continue
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " "))
		for _, next := range lines[index+1:] {
			trimmed := strings.TrimSpace(next)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			if len(next)-len(strings.TrimLeft(next, " ")) <= indent {
				break
			}
			if value, ok := strings.CutPrefix(trimmed, "path:"); ok {
				value = strings.Trim(strings.TrimSpace(value), `"'`)
				if strings.HasPrefix(value, "/") && !strings.ContainsAny(value, " \t") {
					return value
				}
			}
		}
	}
	return ""
}

// jvmSettings reads the few keys that move a JVM service's health endpoint:
// context paths and the management endpoint's base path and port. A
// properties file is key=value lines; a YAML file is flattened by
// indentation, which is enough for the nested-map shape these keys take.
func jvmSettings(name string, content []byte) map[string]string {
	wanted := map[string]bool{
		"server.servlet.context-path": true, "management.endpoints.web.base-path": true,
		"management.server.port": true, "management.server.base-path": true,
		"quarkus.http.root-path": true, "quarkus.http.non-application-root-path": true,
		"quarkus.smallrye-health.root-path": true, "micronaut.server.context-path": true,
	}
	settings := map[string]string{}
	if strings.HasSuffix(name, ".properties") {
		for _, raw := range strings.Split(string(content), "\n") {
			line := strings.TrimSpace(raw)
			if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
				continue
			}
			key, value, found := strings.Cut(line, "=")
			if !found {
				key, value, found = strings.Cut(line, ":")
			}
			key = strings.TrimSpace(key)
			if found && wanted[key] {
				settings[key] = strings.TrimSpace(value)
			}
		}
		return settings
	}
	type level struct {
		indent int
		key    string
	}
	var stack []level
	for _, raw := range strings.Split(string(content), "\n") {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "-") {
			continue
		}
		if trimmed == "---" {
			// A second document is another profile; the default one decides.
			break
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " "))
		key, value, found := strings.Cut(trimmed, ":")
		if !found {
			continue
		}
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		parts := make([]string, 0, len(stack)+1)
		for _, entry := range stack {
			parts = append(parts, entry.key)
		}
		parts = append(parts, strings.TrimSpace(key))
		full := strings.Join(parts, ".")
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if value == "" {
			stack = append(stack, level{indent: indent, key: strings.TrimSpace(key)})
			continue
		}
		if wanted[full] {
			settings[full] = value
		}
	}
	return settings
}
