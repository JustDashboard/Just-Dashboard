package deploy

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// DetectedNetworkVariable is a plain runtime variable the deployment's place
// behind the managed proxy decides rather than the operator: a framework's
// proxy-trust switch, its public URL, or HOST for a server that binds it.
// The form seeds each as a visible plan variable the operator can remove.
type DetectedNetworkVariable struct {
	Name           string `json:"name"`
	Value          string `json:"value,omitempty"`
	DomainTemplate string `json:"domainTemplate,omitempty"`
	Reason         string `json:"reason"`
}

// networkDetection gathers the listen facts of every root. Go and JVM
// sources are read during the walk — Go's by the walk itself, which already
// reads every Go file — and the few files that decide a root's listener
// (the served entry, application.yml, appsettings.json) are read afterwards
// by name, under their own budget. A large repository stops contributing
// facts quietly; it never makes detection itself truncated.
type networkDetection struct {
	ctx    context.Context
	source string
	files  int
	bytes  int64
	// goMains is each Go main package directory's report.
	goMains map[string]*goMainFacts
	// jvm is each JVM source file's report.
	jvm      map[string]listenReport
	jvmFiles int
	jvmBytes int64
}

type goMainFacts struct {
	report   listenReport
	http     bool
	emptyRun *sourceMark
}

const (
	networkReadFiles   = 64
	networkReadBytes   = 2 << 20
	networkFileBytes   = 128 << 10
	networkJVMFiles    = 96
	networkJVMBytes    = 3 << 20
	networkGoMainFiles = 256
)

func newNetworkDetection(ctx context.Context, source string) *networkDetection {
	return &networkDetection{ctx: ctx, source: source, goMains: map[string]*goMainFacts{}, jvm: map[string]listenReport{}}
}

var goPackageMainRE = regexp.MustCompile(`(?m)^package\s+main\b`)

// observeGo records a Go file's listeners when it belongs to a main package.
func (n *networkDetection) observeGo(rel string, content []byte) {
	if !goPackageMainRE.Match(content) || strings.ContainsAny(rel, "\x00\r\n") {
		return
	}
	directory := path.Dir(rel)
	facts := n.goMains[directory]
	if facts == nil {
		if len(n.goMains) >= networkGoMainFiles {
			return
		}
		facts = &goMainFacts{}
		n.goMains[directory] = facts
	}
	facts.report.merge(scanGoListen(rel, content, false))
	facts.http = facts.http || goHTTPServerRE.Match(content)
	if loc := goEmptyRunRE.FindIndex(content); loc != nil && facts.emptyRun == nil && !commentedAt(content, loc[0], "//") {
		facts.emptyRun = &sourceMark{path: rel, line: lineOf(content, loc[0]), port: 8080, what: "gin Run() follows PORT, else 8080"}
	}
}

// observeFile reads the JVM sources whose code can fix a port: Vert.x,
// Javalin and Ktor's embedded server set it there.
func (n *networkDetection) observeFile(fullPath, rel, name string) {
	if !strings.HasSuffix(name, ".java") && !strings.HasSuffix(name, ".kt") {
		return
	}
	if !strings.Contains("/"+rel, "/src/main/") || strings.ContainsAny(rel, "\x00\r\n") ||
		n.jvmFiles >= networkJVMFiles || n.jvmBytes >= networkJVMBytes {
		return
	}
	content, read, err := readDetectionFile(fullPath, networkFileBytes)
	n.jvmFiles++
	n.jvmBytes += read
	if err != nil {
		return
	}
	if report := scanJVMSource(rel, content); !report.empty() {
		n.jvm[rel] = report
	}
}

// read loads one file of a root by name, contained in that root, under the
// targeted-read budget.
func (n *networkDetection) read(root, rel string) ([]byte, bool) {
	if n.ctx.Err() != nil || n.files >= networkReadFiles || n.bytes >= networkReadBytes || !safeRelativePath(rel) {
		return nil, false
	}
	directory := n.source
	if root != "" {
		directory = filepath.Join(n.source, filepath.FromSlash(root))
	}
	if !regularExists(directory, rel) {
		return nil, false
	}
	n.files++
	content, err := readContainedRegular(directory, rel, networkFileBytes)
	if err != nil {
		return nil, false
	}
	n.bytes += int64(len(content))
	return content, true
}

// listenInputs is what one candidate's handler found, for settleListen.
type listenInputs struct {
	command    commandListen
	hasCommand bool
	// useCode says the command runs the code the report was read from, so
	// its listeners are the server's.
	useCode bool
	code    listenReport
	// defaultsCertain says a framework default read in the code certainly
	// runs: the served script itself calls it.
	defaultsCertain bool
	// bridgedBy names the setting the recipe writes that moves a
	// configured port onto PORT.
	bridgedBy string
	// recipeHosts are the host variables the recipe's runtime sets.
	recipeHosts map[string]bool
	// loopbackRecipeFix names the recipe setting that moves a framework
	// default off loopback (Rocket, Kestrel).
	loopbackRecipeFix string
	loopbackDefault   string
	loopbackDefaultAt string
}

// settleListen folds a candidate's command and code facts into its port,
// its evidence and the listen facts preflight re-checks.
func settleListen(c *DetectedCandidate, in listenInputs) {
	listen := DetectedListen{}
	evidence := func(file, reason string) {
		if len(c.Evidence) < 120 {
			c.Evidence = append(c.Evidence, DetectionEvidence{Path: file, Reason: listenText(reason)})
		}
	}
	// A start command is read from the manifest or Procfile the candidate
	// names first.
	commandSource := joinRoot(c.Root, "Procfile")
	if len(c.Evidence) > 0 {
		commandSource = c.Evidence[0].Path
	}
	code := listenReport{}
	if in.useCode {
		code = in.code
	}
	segment := boundedEvidence(in.command.segment)
	switch {
	case in.hasCommand && in.command.port > 0:
		c.Port = in.command.port
		listen.Port, listen.PortFrom = in.command.port, "start command: "+segment
		evidence(commandSource, fmt.Sprintf("start command listens on %d: %s", in.command.port, segment))
	case in.hasCommand && in.command.followsPort:
		listen.ReadsPort, listen.ReadsPortFrom = true, "start command passes $PORT: "+segment
		if in.command.fallback > 0 {
			c.Port = in.command.fallback
		}
		evidence(commandSource, "start command listens on $PORT: "+segment)
	case in.hasCommand && in.command.defaultPort > 0 && !in.useCode:
		// A server run with no port flag listens on its own default and
		// ignores PORT.
		c.Port = in.command.defaultPort
		listen.Port, listen.PortFrom = in.command.defaultPort, in.command.tool+" default port"
		evidence(commandSource, fmt.Sprintf("%s listens on %d by default", in.command.tool, in.command.defaultPort))
	default:
		if mark, ok := singlePort(code.configPorts); ok {
			c.Port = mark.port
			if in.bridgedBy != "" {
				listen.ReadsPort, listen.ReadsPortFrom = true, "the recipe passes PORT as "+in.bridgedBy
				evidence(mark.path, fmt.Sprintf("%s%s; the recipe moves it to PORT with %s", mark.what, lineSuffix(mark), in.bridgedBy))
			} else {
				listen.Port, listen.PortFrom = mark.port, mark.at()+" "+mark.what
				evidence(mark.path, fmt.Sprintf("listens on %d: %s%s", mark.port, mark.what, lineSuffix(mark)))
			}
			break
		}
		if code.readsPort == nil {
			if mark, ok := singlePort(code.ports); ok {
				c.Port = mark.port
				listen.Port, listen.PortFrom = mark.port, mark.at()+" "+mark.what
				evidence(mark.path, fmt.Sprintf("listens on %d without reading PORT: %s%s", mark.port, mark.what, lineSuffix(mark)))
			}
			break
		}
		listen.ReadsPort, listen.ReadsPortFrom = true, code.readsPort.at()
		if mark, ok := singlePort(code.fallbacks); ok {
			c.Port = mark.port
			evidence(mark.path, fmt.Sprintf("listens on PORT, else %d%s", mark.port, lineSuffix(mark)))
		} else if mark, ok := singlePort(code.ports); ok {
			c.Port = mark.port
			evidence(mark.path, fmt.Sprintf("listens on %d and reads PORT%s", mark.port, lineSuffix(mark)))
		} else {
			evidence(code.readsPort.path, "listens on the PORT the runtime injects"+lineSuffix(*code.readsPort))
		}
	}
	host, movedBy := in.command.boundHost(in.recipeHosts)
	switch {
	case in.hasCommand && host != "" && loopbackHost(host):
		listen.Loopback, listen.LoopbackFrom, listen.LoopbackCertain = host, "start command: "+segment, true
		evidence(commandSource, "start command binds "+host+", which nothing outside the container reaches")
	case in.hasCommand && movedBy != "":
		listen.Loopback, listen.LoopbackFrom = in.command.defaultHost, in.command.tool+" without a host binds "+in.command.defaultHost
		listen.LoopbackRecipeFix = movedBy + "=0.0.0.0"
	case len(code.loopback) > 0:
		first := code.loopback[0]
		listen.Loopback, listen.LoopbackFrom = first.host, first.at()+" "+first.what
		listen.LoopbackCertain = len(code.open) == 0
		evidence(first.path, fmt.Sprintf("binds %s%s, which nothing outside the container reaches", first.host, lineSuffix(first)))
	case in.loopbackDefault != "":
		listen.Loopback, listen.LoopbackFrom = in.loopbackDefault, in.loopbackDefaultAt
		listen.LoopbackRecipeFix = in.loopbackRecipeFix
	case len(code.defaults) > 0 && len(code.open) == 0:
		first := code.defaults[0]
		listen.Loopback, listen.LoopbackFrom = first.host, first.at()+" "+first.what
		listen.LoopbackCertain = in.defaultsCertain
		evidence(first.path, fmt.Sprintf("%s%s is given no host, so it binds %s", first.what, lineSuffix(first), first.host))
	case code.hostFallback != nil:
		first := *code.hostFallback
		listen.Loopback = first.host
		if listen.Loopback == "" {
			listen.Loopback = "localhost"
		}
		listen.LoopbackFrom = first.at() + " " + first.what
		if in.recipeHosts["HOST"] {
			listen.LoopbackRecipeFix = "HOST=0.0.0.0"
		} else {
			listen.LoopbackVariable = "HOST"
		}
	}
	if listen != (DetectedListen{}) {
		for _, field := range []*string{&listen.PortFrom, &listen.ReadsPortFrom, &listen.Loopback, &listen.LoopbackFrom, &listen.LoopbackRecipeFix} {
			*field = listenText(*field)
		}
		c.Listen = &listen
	}
}

func lineSuffix(mark sourceMark) string {
	if mark.line <= 0 {
		return ""
	}
	return fmt.Sprintf(" (line %d)", mark.line)
}

// apply folds the listen facts into one root's candidates and adds the
// variables their place behind the proxy needs.
func (n *networkDetection) apply(marker *detectedMarkers, candidates []DetectedCandidate, pythonEntries []pythonEntry, goRoots, jvmRoots []string) {
	var manifest nodeManifest
	hasManifest := len(marker.packageJSON) > 0 && parseNodeManifest(marker.packageJSON, &manifest)
	var code listenReport
	order := make([]int, 0, len(candidates))
	for index := range candidates {
		if candidates[index].BuildMethod != BuildDockerfile {
			order = append(order, index)
		}
	}
	for index := range candidates {
		if candidates[index].BuildMethod == BuildDockerfile {
			order = append(order, index)
		}
	}
	for _, index := range order {
		c := &candidates[index]
		switch {
		case c.BuildMethod == BuildRecipe && c.Recipe == "node" && hasManifest:
			code.merge(n.node(marker, c, manifest))
		case c.BuildMethod == BuildRecipe && c.Recipe == "python":
			code.merge(n.python(marker, c, pythonEntries))
		case c.BuildMethod == BuildRecipe && c.Recipe == "go":
			code.merge(n.golang(marker, c, goRoots))
		case c.BuildMethod == BuildRecipe && c.Recipe == "rust":
			code.merge(n.rust(marker, c))
		case c.BuildMethod == BuildRecipe && c.Recipe == "java":
			code.merge(n.java(marker, c, jvmRoots))
		case c.BuildMethod == BuildRecipe && c.Recipe == "dotnet":
			code.merge(n.dotnet(marker, c))
		case c.BuildMethod == BuildRecipe && c.Recipe == "deno":
			code.merge(n.deno(marker, c))
		case c.BuildMethod == BuildDockerfile:
			n.dockerfile(marker, c, code)
		}
		if hasManifest && (c.BuildMethod == BuildDockerfile || c.Recipe == "node") && c.OutputDirectory == "" && c.BuildMethod != BuildCompose {
			c.NetworkVariables = append(c.NetworkVariables, authNetworkVariables(manifest, marker.packagePath)...)
		}
		if c.Listen != nil && c.Listen.LoopbackVariable == "HOST" {
			c.NetworkVariables = append(c.NetworkVariables, DetectedNetworkVariable{
				Name: "HOST", Value: "0.0.0.0",
				Reason: listenText("the server binds the address in HOST (" + c.Listen.LoopbackFrom + "); inside a container it must be every interface"),
			})
		}
	}
}

// Node: the served command and, when it runs a file, that file's listeners.

var nodeEntryRunners = map[string]bool{"node": true, "bun": true, "tsx": true, "ts-node": true, "nodemon": true}

// nodeValueFlags are the Node and Bun flags that take the next argument.
var nodeValueFlags = map[string]bool{
	"-r": true, "--require": true, "--import": true, "--loader": true, "--env-file": true, "--experimental-loader": true, "-e": true,
}

// commandEntry is the file a node, bun or tsx command runs.
func commandEntry(segment string) string {
	fields := strings.Fields(segment)
	for len(fields) > 0 && (envAssignmentRE.MatchString(fields[0]) || fields[0] == "exec" || fields[0] == "env" || fields[0] == "cross-env") {
		fields = fields[1:]
	}
	if len(fields) == 0 || !nodeEntryRunners[path.Base(fields[0])] {
		return ""
	}
	for index := 1; index < len(fields); index++ {
		field := fields[index]
		if (field == "run" && path.Base(fields[0]) == "bun") || (field == "watch" && path.Base(fields[0]) == "tsx") {
			continue
		}
		if strings.HasPrefix(field, "-") {
			if nodeValueFlags[field] {
				index++
			}
			continue
		}
		entry := strings.TrimPrefix(strings.Trim(field, `'"`), "./")
		if safeRelativePath(entry) {
			return entry
		}
		return ""
	}
	return ""
}

// nodeSourceFiles lists the files that may hold a served entry's
// listener: the entry itself, its source when it is a build output, and the
// conventional names.
func nodeSourceFiles(entry string, manifest nodeManifest) (primary, fallback []string) {
	add := func(list *[]string, name string) {
		for _, existing := range *list {
			if existing == name {
				return
			}
		}
		*list = append(*list, name)
	}
	if entry != "" {
		ext := path.Ext(entry)
		base := strings.TrimSuffix(entry, ext)
		if ext == "" {
			add(&primary, entry+".js")
			add(&primary, path.Join(entry, "index.js"))
		} else {
			add(&primary, entry)
		}
		// A build output's listener is in the source it was compiled from.
		for _, output := range []string{"dist/", "build/", "out/", "lib/"} {
			if strings.HasPrefix(base, output) {
				rest := strings.TrimPrefix(base, output)
				for _, source := range []string{"src/" + rest, rest} {
					add(&primary, source+".ts")
					add(&primary, source+".js")
					add(&primary, source+".mts")
				}
			}
		}
	}
	if main := nodeMainEntry(manifest); main != "" {
		add(&fallback, main)
	}
	for _, name := range []string{
		"server.js", "server.ts", "server.mjs", "index.js", "index.ts", "index.mjs", "app.js", "app.ts", "main.js", "main.ts",
		"src/server.ts", "src/server.js", "src/index.ts", "src/index.js", "src/main.ts", "src/main.js", "src/app.ts", "src/app.js",
		"server/index.ts", "server/index.js",
	} {
		add(&fallback, name)
	}
	return primary, fallback
}

// nodeFrameworkServes names the framework start commands that listen
// through the framework rather than the application's own code.
func nodeFrameworkServes(c *DetectedCandidate) bool {
	switch c.Framework {
	case "", "express", "fastify", "hono", "koa", "elysia", "hapi", "nestjs":
		return false
	}
	return true
}

func (n *networkDetection) node(marker *detectedMarkers, c *DetectedCandidate, manifest nodeManifest) listenReport {
	if c.OutputDirectory != "" || c.StartCommand == "" {
		return listenReport{}
	}
	command := parseCommandListen(c.StartCommand, manifest.Scripts)
	if (strings.HasPrefix(command.segment, "vite preview") || strings.HasPrefix(command.segment, "astro preview")) && command.host == "" {
		// Both preview servers answer on localhost only unless told
		// otherwise; the start runs the preview directly with the flag, as
		// its script would have with it added.
		runner := c.PackageManager
		if runner == "" {
			runner = "npm"
		}
		segments := strings.Split(c.StartCommand, "&&")
		segments[len(segments)-1] = " " + nodeExecRunner(runner) + " " + command.segment + " --host 0.0.0.0"
		c.StartCommand = strings.TrimSpace(strings.Join(segments, "&&"))
		c.Evidence = append(c.Evidence, DetectionEvidence{Path: marker.packagePath, Reason: command.tool + " binds localhost; the start command adds --host 0.0.0.0"})
		command = parseCommandListen(c.StartCommand, manifest.Scripts)
	}
	in := listenInputs{command: command, hasCommand: true, recipeHosts: map[string]bool{"HOST": true}}
	entry := commandEntry(command.segment)
	if entry == "." {
		entry = nodeMainEntry(manifest)
	}
	if entry != "" || c.Framework == "nestjs" {
		in.useCode = true
		primary, fallback := nodeSourceFiles(entry, manifest)
		if entry == "" {
			primary = []string{"src/main.ts", "src/main.js"}
		}
		in.code = n.scriptReport(marker.root, primary)
		if in.code.empty() && !nodeFrameworkServes(c) {
			in.code = n.scriptReport(marker.root, fallback)
		}
	}
	settleListen(c, in)
	return in.code
}

// scriptReport reads the first few of the named files that exist and merges
// their listeners.
func (n *networkDetection) scriptReport(root string, names []string) listenReport {
	var report listenReport
	read := 0
	for _, name := range names {
		if read >= 4 {
			break
		}
		content, ok := n.read(root, name)
		if !ok {
			continue
		}
		read++
		report.merge(scanScriptListen(joinRoot(root, name), content))
	}
	return report
}

// authNetworkVariables are the variables Auth.js needs behind a proxy: v5
// refuses every request as an untrusted host without AUTH_TRUST_HOST, and
// v4 builds its callback URLs from NEXTAUTH_URL.
func authNetworkVariables(manifest nodeManifest, manifestPath string) []DetectedNetworkVariable {
	for _, name := range []string{"@auth/core", "@auth/sveltekit", "@auth/express", "@auth/qwik", "@auth/solid-start"} {
		if manifest.has(name) {
			return []DetectedNetworkVariable{{Name: "AUTH_TRUST_HOST", Value: "true",
				Reason: name + " in " + manifestPath + " refuses requests whose host it does not trust; the proxy sets that host"}}
		}
	}
	if !manifest.has("next-auth") {
		return nil
	}
	version := manifest.version("next-auth")
	major, _ := strconv.Atoi(semverMajor(version))
	if major >= 5 || (major == 0 && strings.Contains(version, "beta")) {
		return []DetectedNetworkVariable{{Name: "AUTH_TRUST_HOST", Value: "true",
			Reason: "next-auth " + boundedEvidence(version) + " in " + manifestPath + " refuses requests whose host it does not trust; the proxy sets that host"}}
	}
	return []DetectedNetworkVariable{{Name: "NEXTAUTH_URL", DomainTemplate: "{{scheme}}://{{hostname}}",
		Reason: "next-auth " + boundedEvidence(version) + " in " + manifestPath + " builds its callback URLs from NEXTAUTH_URL"}}
}

// Python: the start command, a served script's own run call, and gunicorn's
// configuration file.

func (n *networkDetection) python(marker *detectedMarkers, c *DetectedCandidate, entries []pythonEntry) listenReport {
	if c.StartCommand == "" {
		return listenReport{}
	}
	command := parseCommandListen(c.StartCommand, nil)
	in := listenInputs{command: command, hasCommand: true, recipeHosts: map[string]bool{"UVICORN_HOST": true, "FLASK_RUN_HOST": true}}
	switch {
	case strings.HasSuffix(command.tool, ".py") && command.tool != "manage.py":
		script := pythonScript(command.segment)
		in.useCode, in.defaultsCertain = true, true
		for _, entry := range entries {
			if entry.path == script {
				in.code = scanPythonListen(joinRoot(marker.root, script), entry.content)
			}
		}
		if in.code.empty() {
			if content, ok := n.read(marker.root, script); ok {
				in.code = scanPythonListen(joinRoot(marker.root, script), content)
			}
		}
	case command.tool == "gunicorn" && command.host == "" && command.port == 0 && !command.followsPort:
		// Without --bind, gunicorn takes its bind from the configuration
		// file it loads — ./gunicorn.conf.py unless -c names another — and
		// only then from PORT.
		config := command.configFile
		if config == "" {
			config = "gunicorn.conf.py"
		}
		if content, ok := n.read(marker.root, config); ok {
			in.useCode = true
			in.code = scanGunicornConfig(joinRoot(marker.root, config), content)
		}
		if in.code.empty() {
			in.useCode = true
			read := sourceMark{path: joinRoot(marker.root, "Procfile"), what: "gunicorn binds 0.0.0.0:$PORT"}
			in.code.readsPort = &read
		}
	}
	settleListen(c, in)
	return in.code
}

// pythonScript is the file a `python <file>` command runs.
func pythonScript(segment string) string {
	fields := strings.Fields(segment)
	for index, field := range fields {
		if strings.HasSuffix(field, ".py") && index > 0 && strings.HasPrefix(path.Base(fields[index-1]), "python") {
			return strings.TrimPrefix(field, "./")
		}
	}
	return ""
}

// Go: the main package's listeners, and the web frameworks go.mod requires.

var goWebFrameworks = []struct{ module, name string }{
	{"github.com/gin-gonic/gin", "gin"}, {"github.com/labstack/echo", "echo"}, {"github.com/gofiber/fiber", "fiber"},
	{"github.com/go-chi/chi", "chi"}, {"github.com/gorilla/mux", "gorilla"}, {"connectrpc.com/connect", "connect"},
}

func (n *networkDetection) golang(marker *detectedMarkers, c *DetectedCandidate, goRoots []string) listenReport {
	module := string(marker.goModContent)
	framework := ""
	for _, candidate := range goWebFrameworks {
		if strings.Contains(module, candidate.module) {
			framework = candidate.name
			break
		}
	}
	directories := make([]string, 0, len(n.goMains))
	for directory := range n.goMains {
		directories = append(directories, directory)
	}
	sort.Strings(directories)
	var owned []string
	for _, directory := range pathsUnderRoot(prefixEach(directories), marker.root, goRoots) {
		owned = append(owned, strings.TrimSuffix(directory, "/"))
	}
	if len(owned) != 1 {
		// Several main packages are the recipe's own question; none is a
		// library.
		return listenReport{}
	}
	facts := n.goMains[path.Clean(joinRoot(marker.root, owned[0]))]
	if facts == nil {
		return listenReport{}
	}
	report := facts.report
	if framework == "gin" && facts.emptyRun != nil {
		read := *facts.emptyRun
		if report.readsPort == nil {
			report.readsPort = &read
		}
		report.fallbacks = append(report.fallbacks, read)
		report.open = append(report.open, read)
	}
	in := listenInputs{useCode: true, code: report}
	if c.StartCommand != "" {
		in.command, in.hasCommand = parseCommandListen(c.StartCommand, nil), true
	}
	settleListen(c, in)
	serves := framework != "" || facts.http
	if report.readsPort != nil && c.Port == 0 && serves {
		c.Port = 8080
		c.Evidence = append(c.Evidence, DetectionEvidence{Path: report.readsPort.path, Reason: "follows PORT; 8080 is the suggested port"})
	}
	if serves && c.Port > 0 {
		c.Profile = ProfileWeb
		if framework != "" && framework != "connect" {
			c.Framework = framework
		}
		c.Evidence = append(c.Evidence, DetectionEvidence{Path: joinRoot(marker.root, "go.mod"), Reason: goServesReason(framework)})
	}
	return report
}

func goServesReason(framework string) string {
	if framework == "" {
		return "net/http server in the main package"
	}
	return framework + " HTTP server"
}

func prefixEach(directories []string) []string {
	result := make([]string, 0, len(directories))
	for _, directory := range directories {
		if directory == "." {
			result = append(result, "./")
			continue
		}
		result = append(result, directory+"/")
	}
	return result
}

// Rust: the served binary's bind, Rocket.toml, and Loco's own CLI.

var rocketPortRE = regexp.MustCompile(`(?m)^\s*port\s*=\s*(\d{2,5})\s*$`)

func (n *networkDetection) rust(marker *detectedMarkers, c *DetectedCandidate) listenReport {
	manifest := parseCargoManifest(marker.cargoToml)
	files := []string{"src/main.rs"}
	if binary := manifest.binary(); binary != "" && safeRelativePath(binary) && !strings.ContainsAny(binary, "/\\") {
		files = append(files, "src/bin/"+binary+".rs", "src/bin/"+binary+"/main.rs")
	}
	var report listenReport
	for _, name := range files {
		if content, ok := n.read(marker.root, name); ok {
			report.merge(scanRustListen(joinRoot(marker.root, name), content))
		}
	}
	defaultStart := c.StartCommand == ""
	in := listenInputs{useCode: true, code: report}
	switch c.Framework {
	case "rocket":
		if content, ok := n.read(marker.root, "Rocket.toml"); ok {
			if match := rocketPortRE.FindSubmatchIndex(content); match != nil {
				in.code.configPorts = append(in.code.configPorts, sourceMark{
					path: joinRoot(marker.root, "Rocket.toml"), line: lineOf(content, match[0]),
					port: listenPort(string(content[match[2]:match[3]])), what: "port = " + string(content[match[2]:match[3]]),
				})
			}
		}
		// Rocket binds 127.0.0.1 unless configured; the recipe's image sets
		// ROCKET_ADDRESS, and its default start ROCKET_PORT, both of which
		// outrank Rocket.toml.
		in.loopbackDefault, in.loopbackDefaultAt = "127.0.0.1", "Rocket's default address"
		in.loopbackRecipeFix = "ROCKET_ADDRESS=0.0.0.0"
		if defaultStart {
			in.bridgedBy = "ROCKET_PORT"
			if len(in.code.configPorts) == 0 && in.code.readsPort == nil {
				in.code.readsPort = &sourceMark{path: joinRoot(marker.root, "Cargo.toml"), what: "ROCKET_PORT"}
			}
		}
	case "loco":
		if defaultStart {
			c.StartCommand = "/app start --binding 0.0.0.0 --port ${PORT:-5150}"
			c.Evidence = append(c.Evidence, DetectionEvidence{Path: joinRoot(marker.root, "Cargo.toml"),
				Reason: "Loco serves with its CLI's start command, bound to every interface on PORT"})
		}
	}
	if c.StartCommand != "" {
		in.command, in.hasCommand = parseCommandListen(c.StartCommand, nil), true
	}
	settleListen(c, in)
	if c.Listen != nil && (c.Listen.Port > 0 || c.Listen.ReadsPort) || in.code.readsPort != nil || len(in.code.ports) > 0 {
		c.NeedsDecision = withoutDecision(c.NeedsDecision, "confirm the port the service binds; the recipe passes PORT")
	}
	return in.code
}

func withoutDecision(decisions []string, decision string) []string {
	kept := decisions[:0]
	for _, existing := range decisions {
		if existing != decision {
			kept = append(kept, existing)
		}
	}
	return kept
}

// JVM: application configuration, and the frameworks whose code fixes the
// port.

var jvmConfigFiles = []string{
	"src/main/resources/application.properties", "src/main/resources/application.yml", "src/main/resources/application.yaml",
	"src/main/resources/application.conf",
}

// jvmPortBridge is the environment variable the recipe's start command sets
// from PORT for each framework that reads its port from configuration.
var jvmPortBridge = map[string]string{
	"spring-boot": "SERVER_PORT", "helidon": "SERVER_PORT", "quarkus": "QUARKUS_HTTP_PORT", "micronaut": "MICRONAUT_SERVER_PORT",
}

func (n *networkDetection) java(marker *detectedMarkers, c *DetectedCandidate, jvmRoots []string) listenReport {
	var report listenReport
	for _, name := range jvmConfigFiles {
		if content, ok := n.read(marker.root, name); ok {
			report.merge(scanJVMConfig(joinRoot(marker.root, name), content))
		}
	}
	paths := make([]string, 0, len(n.jvm))
	for file := range n.jvm {
		paths = append(paths, file)
	}
	sort.Strings(paths)
	for _, file := range pathsUnderRoot(paths, marker.root, jvmRoots) {
		report.merge(n.jvm[joinRoot(marker.root, file)])
	}
	in := listenInputs{useCode: true, code: report}
	if c.StartCommand == "" {
		if bridge := jvmPortBridge[c.Framework]; bridge != "" {
			in.bridgedBy = bridge
			if len(report.configPorts) == 0 && report.readsPort == nil {
				in.code.readsPort = &sourceMark{path: c.Evidence[0].Path, what: bridge}
			}
		}
	} else {
		in.command, in.hasCommand = parseCommandListen(c.StartCommand, nil), true
	}
	settleListen(c, in)
	if c.Framework == "spring-boot" && springSecurityRE.Match(append(append([]byte{}, marker.pomXML...), marker.gradleBuild...)) {
		c.Evidence = append(c.Evidence, DetectionEvidence{Path: c.Evidence[0].Path,
			Reason: "Spring Security builds its redirect URIs from the forwarded scheme, which the recipe trusts"})
	}
	return in.code
}

var (
	dotnetExternalSignInRE = regexp.MustCompile(`Include="Microsoft\.(?:AspNetCore\.Authentication\.[A-Za-z]+|Identity\.Web)"`)
	springSecurityRE       = regexp.MustCompile(`spring-boot-starter-(?:oauth2-client|security)`)
)

// .NET: appsettings and Program.cs.

func (n *networkDetection) dotnet(marker *detectedMarkers, c *DetectedCandidate) listenReport {
	if c.Framework != "aspnet" {
		return listenReport{}
	}
	var report listenReport
	settings := readKestrelSettings(func(name string) ([]byte, bool) { return n.read(marker.root, name) })
	for _, name := range []string{"Program.cs", "Startup.cs"} {
		if content, ok := n.read(marker.root, name); ok {
			report.merge(scanDotnetSource(joinRoot(marker.root, name), content))
		}
	}
	in := listenInputs{useCode: true, code: report}
	defaultStart := c.StartCommand == ""
	// A URL Program.cs passes in code outranks every setting and every
	// variable, so appsettings no longer decides anything.
	codeListens := len(report.ports)+len(report.loopback)+len(report.open) > 0
	if codeListens {
		settings = kestrelSettings{}
	}
	bridge, unbridged := kestrelBridge(settings)
	appsettings := joinRoot(marker.root, "appsettings.json")
	for _, url := range kestrelURLs(settings) {
		found, ok := dotnetURLListener(appsettings, 0, url, "appsettings "+url)
		if !ok {
			continue
		}
		switch {
		case defaultStart && unbridged == "":
			if loopbackHost(found.host) && in.loopbackDefault == "" {
				in.loopbackDefault, in.loopbackDefaultAt = found.host, "appsettings.json "+url
				in.loopbackRecipeFix = strings.Join(bridge, " ")
			}
			if found.port > 0 {
				in.code.configPorts = append(in.code.configPorts, found)
			}
		default:
			in.code.addListener(found)
		}
	}
	if defaultStart && !codeListens {
		in.bridgedBy = "ASPNETCORE_HTTP_PORTS"
		if len(bridge) > 0 {
			in.bridgedBy = strings.Join(bridgeNames(bridge), ", ")
		}
		if len(in.code.configPorts) == 0 && in.code.readsPort == nil && len(in.code.ports) == 0 {
			in.code.readsPort = &sourceMark{path: c.Evidence[0].Path, what: "ASPNETCORE_HTTP_PORTS"}
		}
	} else if !defaultStart {
		in.command, in.hasCommand = parseCommandListen(c.StartCommand, nil), true
	}
	settleListen(c, in)
	names := make([]string, 0, len(marker.csprojs))
	for name := range marker.csprojs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if dotnetExternalSignInRE.Match(marker.csprojs[name]) {
			c.Evidence = append(c.Evidence, DetectionEvidence{Path: joinRoot(marker.root, name),
				Reason: "external sign-in builds its redirect URIs from the forwarded scheme, which the recipe trusts"})
			break
		}
	}
	if unbridged != "" {
		if c.Listen == nil {
			c.Listen = &DetectedListen{}
		}
		c.Listen.Unbridged = unbridged
	}
	return in.code
}

// kestrelURLs lists the addresses an appsettings file makes Kestrel listen
// on: its endpoints when it names any, which replace Urls, else Urls.
func kestrelURLs(settings kestrelSettings) []string {
	if len(settings.endpoints) > 0 {
		names := make([]string, 0, len(settings.endpoints))
		for name := range settings.endpoints {
			names = append(names, name)
		}
		sort.Strings(names)
		urls := make([]string, 0, len(names))
		for _, name := range names {
			urls = append(urls, settings.endpoints[name])
		}
		return urls
	}
	if settings.urls != "" {
		return strings.Split(settings.urls, ";")
	}
	return nil
}

// Deno: the start task's flags and the served file's Deno.serve.

func (n *networkDetection) deno(marker *detectedMarkers, c *DetectedCandidate) listenReport {
	config := parseDenoConfig(marker.denoJSON)
	command := c.StartCommand
	if command == "deno task start" {
		command = config.task("start")
	}
	facts := parseCommandListen(command, nil)
	fields := strings.Fields(facts.segment)
	serve := len(fields) > 1 && fields[0] == "deno" && fields[1] == "serve"
	entry := ""
	for index := 2; index < len(fields); index++ {
		if strings.HasPrefix(fields[index], "-") {
			continue
		}
		candidate := strings.TrimPrefix(fields[index], "./")
		switch path.Ext(candidate) {
		case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".mts":
			if safeRelativePath(candidate) {
				entry = candidate
			}
		}
		if entry != "" {
			break
		}
	}
	in := listenInputs{command: facts, hasCommand: true, useCode: true}
	if serve {
		// `deno serve` owns the listener: its --port, else 8000, and never
		// PORT.
		if facts.port == 0 && !facts.followsPort {
			in.command.defaultPort, in.command.tool, in.useCode = 8000, "deno serve", false
		}
	} else if entry != "" {
		if content, ok := n.read(marker.root, entry); ok {
			in.code = scanScriptListen(joinRoot(marker.root, entry), content)
		}
	}
	before := c.Port
	settleListen(c, in)
	known := c.Listen != nil && (c.Listen.Port > 0 || c.Listen.ReadsPort)
	switch {
	case known:
	case len(in.code.open) > 0 || len(in.code.loopback) > 0:
		// Deno.serve with no port listens on 8000.
		c.Port = before
		c.Evidence = append(c.Evidence, DetectionEvidence{Path: joinRoot(marker.root, entry), Reason: "Deno.serve default port 8000"})
	default:
		// Nothing readable names a port, so it is Deno.serve's own default;
		// the port field is where a different answer goes, not a question on
		// the first screen.
		c.Evidence = append(c.Evidence, DetectionEvidence{Path: joinRoot(marker.root, marker.denoJSONPath), Reason: "Deno.serve default port 8000"})
	}
	return in.code
}

// Dockerfile: its own PORT, else what the code says, else a Phoenix
// runtime configuration's PORT default.

func (n *networkDetection) dockerfile(marker *detectedMarkers, c *DetectedCandidate, code listenReport) {
	in := listenInputs{useCode: true, code: code}
	if c.Port == 0 {
		if port := dockerfileEnvPort(marker.dockerfileContent); port > 0 {
			c.Port = port
			c.Evidence = append(c.Evidence, DetectionEvidence{Path: marker.dockerfile, Reason: fmt.Sprintf("ENV PORT=%d", port)})
		}
	}
	if c.Port == 0 {
		if content, ok := n.read(marker.root, "config/runtime.exs"); ok {
			if match := elixirPortRE.FindSubmatchIndex(content); match != nil {
				c.Port = listenPort(string(content[match[2]:match[3]]))
				c.Evidence = append(c.Evidence, DetectionEvidence{Path: joinRoot(marker.root, "config/runtime.exs"),
					Reason: fmt.Sprintf("Phoenix listens on PORT, else %d", c.Port)})
			}
		}
	}
	if c.Port != 0 {
		// The image says where it listens; the code only adds a loopback.
		in.code.ports, in.code.fallbacks, in.code.configPorts, in.code.readsPort = nil, nil, nil, nil
	}
	settleListen(c, in)
}

// bridgeNames is the variable each `NAME=value` bridge sets.
func bridgeNames(bridge []string) []string {
	names := make([]string, 0, len(bridge))
	for _, setting := range bridge {
		name, _, _ := strings.Cut(setting, "=")
		names = append(names, name)
	}
	return names
}
