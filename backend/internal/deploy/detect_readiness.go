package deploy

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Readiness detection decides how a release proves it serves before traffic
// moves to it. The plan's default used to be GET / expecting a 2xx for every
// web workload, which failed healthy releases whose root is not a page — an
// API with no route at /, a Spring service answering its Whitelabel 404, a
// page that redirects visitors to a hosted sign-in — and ignored the health
// endpoint the source already declared. The rules, strongest first:
//
//  1. a Dockerfile HEALTHCHECK, and the health path a previous platform's
//     file (fly.toml, render.yaml, railway, Kamal) names;
//  2. a health endpoint the framework declares (Rails /up, Laravel's
//     health route, Spring Actuator, Quarkus and Micronaut health, ASP.NET
//     MapHealthChecks, Strapi, Medusa, Directus) or a file-routed one (a
//     Next.js, Nuxt, SvelteKit, Remix or Astro health route file);
//  3. a health route registered in code, whose final prefix a router may
//     change, so it is probed accepting any answer (checks_http.go);
//  4. the framework's own convention: a page framework is asked for a page at
//     /, anything else for any answer from /.
//
// The budget follows what the source says about its start: a JVM, a start
// command that migrates first, or a model downloaded at start each wait
// longer than the default minute.

// DetectedReadiness is the readiness check detection proposes for a
// candidate, with what it learned about how the application answers.
type DetectedReadiness struct {
	// Kind is the check kind: http, or docker_health for a Dockerfile
	// HEALTHCHECK that is not a plain request to the served port.
	Kind            string `json:"kind"`
	Path            string `json:"path,omitempty"`
	AcceptAnyAnswer bool   `json:"acceptAnyAnswer,omitempty"`
	Attempts        int    `json:"attempts,omitempty"`
	IntervalSeconds int    `json:"intervalSeconds,omitempty"`
	// Source is where the check came from: healthcheck, platform, framework,
	// code or convention. Only convention is detection's own guess.
	Source   string `json:"source"`
	Evidence string `json:"evidence"`
	// SlowStart says why the budget is longer than the default minute.
	SlowStart string `json:"slowStart,omitempty"`
	// ModelDownload names the model load the application runs while it
	// starts, and ModelCache the directory the download is kept in.
	ModelDownload string `json:"modelDownload,omitempty"`
	ModelCache    string `json:"modelCache,omitempty"`
	// RootRoute is "unrouted" when the application's own router was read
	// and nothing serves /, and "routed" when something does.
	RootRoute string `json:"rootRoute,omitempty"`
	// HTTPSRedirect names the setting that redirects plain-HTTP requests to
	// HTTPS; HTTPSRedirectIgnoresProxy says it does so even when the proxy
	// reports the visitor's request was HTTPS, which loops behind any proxy.
	HTTPSRedirect             string `json:"httpsRedirect,omitempty"`
	HTTPSRedirectIgnoresProxy bool   `json:"httpsRedirectIgnoresProxy,omitempty"`
	// AllowedHosts is a literal host allowlist the application enforces, and
	// AllowedHostsSource the setting it was read from; an empty list with a
	// source allows no host at all.
	AllowedHosts       []string `json:"allowedHosts,omitempty"`
	AllowedHostsSource string   `json:"allowedHostsSource,omitempty"`
}

const (
	readinessFromHealthcheck = "healthcheck"
	readinessFromPlatform    = "platform"
	readinessFromFramework   = "framework"
	readinessFromCode        = "code"
	readinessFromConvention  = "convention"
)

// Budgets. The default is the plan's own (20 attempts, 3 s apart); a slow
// start doubles it; a model downloaded at start waits up to ten minutes.
const (
	readinessDefaultAttempts = 20
	readinessDefaultInterval = 3
	readinessSlowAttempts    = 40
	readinessLongAttempts    = 60
	readinessLongInterval    = 10
)

// refineServing settles, for the candidates of one root, what the walk's
// bounded reads say about serving: whether a package is a background worker
// rather than a server, whether its start command stays in the foreground,
// and how its readiness is proved.
func refineServing(candidates []DetectedCandidate, marker *detectedMarkers, scanner *readinessScanner, root string, roots []string) {
	facts := scanner.forRoot(root, roots)
	for index := range candidates {
		candidate := &candidates[index]
		if candidate.BuildMethod != BuildRecipe {
			continue
		}
		classifyBackgroundWorker(candidate, marker, facts)
		settleStartCommand(candidate, marker)
	}
	for index := range candidates {
		candidate := &candidates[index]
		if candidate.BuildMethod == BuildRecipe {
			candidate.Readiness = recipeReadiness(candidate, marker, facts)
		}
	}
	for index := range candidates {
		candidate := &candidates[index]
		if candidate.BuildMethod == BuildDockerfile {
			candidate.Readiness = dockerfileReadiness(candidate, marker, facts, candidates)
			settleDockerfileStart(candidate, marker)
		}
		sanitizeServing(candidate)
	}
}

// sanitizeServing keeps what a repository wrote from reaching a stored
// detection in a shape validateDetectedServing refuses: a path with a query
// or whitespace, a line break in a file name, text that reads like a
// credential. A path the probe cannot use leaves the plan's default check.
func sanitizeServing(candidate *DetectedCandidate) {
	if readiness := candidate.Readiness; readiness != nil {
		if readiness.Kind == string(CheckHTTP) && !isSafeReadinessPath(readiness.Path) {
			candidate.Readiness = nil
		} else {
			readiness.Evidence = servingText(readiness.Evidence)
			readiness.SlowStart = servingText(readiness.SlowStart)
			readiness.ModelDownload = servingText(readiness.ModelDownload)
			readiness.HTTPSRedirect = servingText(readiness.HTTPSRedirect)
			readiness.AllowedHostsSource = servingText(readiness.AllowedHostsSource)
			for _, host := range readiness.AllowedHosts {
				// An allowlist with an entry that is not a host pattern is not one
				// this dashboard can compare a domain with.
				if !allowlistHostRE.MatchString(host) || len(readiness.AllowedHosts) > 64 {
					readiness.AllowedHosts, readiness.AllowedHostsSource = nil, ""
					break
				}
			}
			if readiness.AllowedHostsSource == "" {
				readiness.AllowedHosts = nil
			}
		}
	}
	if worker := candidate.BackgroundWorker; worker != nil {
		worker.Library, worker.Kind, worker.Evidence = servingText(worker.Library), servingText(worker.Kind), servingText(worker.Evidence)
	}
	if detach := candidate.StartDetaches; detach != nil {
		detach.Command, detach.Script, detach.Source = servingText(detach.Command), servingText(detach.Script), servingText(detach.Source)
		if detach.Command == "" {
			candidate.StartDetaches = nil
		}
	}
}

var allowlistHostRE = regexp.MustCompile(`^[A-Za-z0-9*.:\[\]_-]{1,253}$`)

// servingText is a repository-derived string as evidence may carry it: one
// line, bounded, and withheld when it reads like a credential.
func servingText(value string) string {
	value = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, value)
	return boundedEvidence(value)
}

// rootFacts are the facts of the files one root owns, with paths relative
// to that root.
type rootFacts []readinessFact

func (s *readinessScanner) forRoot(root string, roots []string) rootFacts {
	prefix := rootPrefix(root)
	var inside []string
	for _, other := range roots {
		if other != root && strings.HasPrefix(other, prefix) {
			inside = append(inside, rootPrefix(other))
		}
	}
	owns := func(file string) bool {
		if !strings.HasPrefix(file, prefix) {
			return false
		}
		for _, other := range inside {
			if strings.HasPrefix(file, other) {
				return false
			}
		}
		return true
	}
	var owned rootFacts
	for _, fact := range s.facts {
		if owns(fact.file) {
			fact.file = strings.TrimPrefix(fact.file, prefix)
			if fact.kind == factRouteFile {
				fact.value = fact.file
			}
			owned = append(owned, fact)
		}
	}
	// A fact's absence is evidence only when every file it could be in was
	// read; the root says when one was not.
	incomplete := s.walkStopped || s.factsDropped
	for _, file := range s.unread {
		incomplete = incomplete || owns(file)
	}
	if incomplete {
		owned = append(owned, readinessFact{kind: factSourceUnread})
	}
	return owned
}

func (f rootFacts) of(kind string) []readinessFact {
	var result []readinessFact
	for _, fact := range f {
		if fact.kind == kind {
			result = append(result, fact)
		}
	}
	return result
}

func (f rootFacts) has(kind string) bool { return len(f.of(kind)) > 0 }

// fromStack keeps the facts one of the given stacks recorded.
func fromStack(facts []readinessFact, stacks ...string) []readinessFact {
	var result []readinessFact
	for _, fact := range facts {
		for _, stack := range stacks {
			if stack != "" && fact.stack == stack {
				result = append(result, fact)
				break
			}
		}
	}
	return result
}

func (f rootFacts) inFile(file, kind string) []readinessFact {
	var result []readinessFact
	for _, fact := range f {
		if fact.file == file && fact.kind == kind {
			result = append(result, fact)
		}
	}
	return result
}

func strictReadiness(route, source, evidence string) *DetectedReadiness {
	return &DetectedReadiness{Kind: string(CheckHTTP), Path: route, Source: source, Evidence: evidence}
}

func answeredReadiness(route, source, evidence string) *DetectedReadiness {
	readiness := strictReadiness(route, source, evidence)
	readiness.AcceptAnyAnswer = true
	return readiness
}

// healthPreference orders the health routes a source may register several
// of: the plain /health a service is probed on first, a liveness ping last.
func healthPreference(route string) int {
	route = strings.Trim(strings.ToLower(route), "/")
	for rank, name := range []string{"health", "healthz", "api/health", "readyz", "api/healthz", "healthcheck", "livez", "_health"} {
		if route == name {
			return rank
		}
	}
	if strings.HasSuffix(route, "/ping") || route == "ping" {
		return 20
	}
	return 10
}

func bestHealthFact(facts []readinessFact) *readinessFact {
	if len(facts) == 0 {
		return nil
	}
	sorted := append([]readinessFact(nil), facts...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if a, b := healthPreference(sorted[i].value), healthPreference(sorted[j].value); a != b {
			return a < b
		}
		return sorted[i].file < sorted[j].file
	})
	return &sorted[0]
}

// platformReadiness is the health path a platform file the repository
// carries names. Several files naming different paths are not one answer.
func platformReadiness(facts rootFacts) *DetectedReadiness {
	declared := facts.of(factPlatformHealth)
	if len(declared) == 0 || !isSafeReadinessPath(declared[0].value) {
		return nil
	}
	for _, fact := range declared[1:] {
		if fact.value != declared[0].value {
			return nil
		}
	}
	return strictReadiness(declared[0].value, readinessFromPlatform, declared[0].label)
}

func recipeReadiness(candidate *DetectedCandidate, marker *detectedMarkers, facts rootFacts) *DetectedReadiness {
	if (candidate.Profile != ProfileWeb && candidate.Profile != ProfileService) || candidate.OutputDirectory != "" {
		return nil
	}
	readiness := platformReadiness(facts)
	if readiness == nil {
		switch candidate.Recipe {
		case "node":
			readiness = nodeReadiness(candidate, marker, facts)
		case "python":
			readiness = pythonReadiness(candidate, facts)
		case "java":
			readiness = javaReadiness(marker, facts)
		case "dotnet":
			readiness = declaredOrCodeReadiness(facts, "ASP.NET Core", "dotnet", "dotnet")
		case "go":
			readiness = declaredOrCodeReadiness(facts, "the Go service", "", "go")
		case "rust":
			label := "the Rust service"
			if candidate.Framework != "rust" && candidate.Framework != "" {
				label = candidate.Framework
			}
			readiness = declaredOrCodeReadiness(facts, label, "", "rust")
		case "deno":
			if candidate.Framework == "fresh" {
				readiness = strictReadiness("/", readinessFromConvention, "Fresh serves its pages at /")
			} else {
				readiness = declaredOrCodeReadiness(facts, "Deno", "", "js")
			}
		case "php":
			readiness = phpReadiness(candidate, facts)
		}
	}
	if readiness == nil {
		return nil
	}
	if candidate.Recipe == "python" {
		addDjangoConcerns(readiness, facts)
	}
	addRailsConcerns(readiness, facts)
	applyReadinessBudget(readiness, candidate, marker, facts, candidate.StartCommand)
	return readiness
}

// declaredOrCodeReadiness serves stacks whose root is an API by default: a
// health endpoint the stack's framework declares, else a health route found
// in the stack's own language, else any answer from /. The facts of a root
// can include another stack's files — a Go module beside a Rails app that is
// not a root of its own — so each kind is taken only from its own stack.
func declaredOrCodeReadiness(facts rootFacts, label, declaredBy, stack string) *DetectedReadiness {
	if declaredBy != "" {
		if declared := bestHealthFact(fromStack(facts.of(factDeclaredHealth), declaredBy)); declared != nil {
			return strictReadiness(declared.value, readinessFromFramework, declared.label)
		}
	}
	if code := bestHealthFact(fromStack(facts.of(factCodeHealth), stack)); code != nil {
		return answeredReadiness(code.value, readinessFromCode, code.label+"; any answer counts, since a router prefix may move it")
	}
	if label == "" {
		label = "this service"
	}
	return answeredReadiness("/", readinessFromConvention, "no health route found; any answer from "+label+" at / shows it is serving")
}

// nodePageFrameworks serve pages at their root; everything else a Node
// package runs is treated as an API.
var nodePageFrameworks = map[string]bool{
	"nextjs": true, "sveltekit": true, "astro": true, "nuxt": true, "remix": true, "react-router": true,
	"solid-start": true, "tanstack-start": true, "angular": true,
}

// nodeHostedSignIn are the authentication SDKs whose middleware sends an
// anonymous visitor at / to a sign-in page on the provider's own site.
var nodeHostedSignIn = []struct{ dependency, label string }{
	{"@clerk/nextjs", "Clerk"}, {"@clerk/remix", "Clerk"}, {"@clerk/astro", "Clerk"}, {"@clerk/nuxt", "Clerk"},
	{"@clerk/react-router", "Clerk"}, {"@clerk/tanstack-react-start", "Clerk"}, {"@auth0/nextjs-auth0", "Auth0"},
	{"@kinde-oss/kinde-auth-nextjs", "Kinde"}, {"@workos-inc/authkit-nextjs", "WorkOS AuthKit"},
	{"@logto/next", "Logto"}, {"@descope/nextjs-sdk", "Descope"},
}

// nodeFrameworkHealth are the health endpoints Node applications built on a
// platform answer without any route of their own.
var nodeFrameworkHealth = []struct{ dependency, route, label string }{
	{"@strapi/strapi", "/_health", "Strapi health endpoint /_health"},
	{"@medusajs/medusa", "/health", "Medusa health endpoint /health"},
	{"@medusajs/framework", "/health", "Medusa health endpoint /health"},
	{"directus", "/server/health", "Directus health endpoint /server/health"},
}

func nodeReadiness(candidate *DetectedCandidate, marker *detectedMarkers, facts rootFacts) *DetectedReadiness {
	var manifest nodeManifest
	parseNodeManifest(marker.packageJSON, &manifest)
	base := ""
	if candidate.Framework == "nextjs" {
		if configured := facts.of(factNextBasePath); len(configured) > 0 {
			base = configured[0].value
		}
	}
	if route, label, strict := nodeRouteFile(candidate.Framework, facts); route != "" {
		if strict {
			return strictReadiness(base+route, readinessFromFramework, label)
		}
		return answeredReadiness(base+route, readinessFromCode, label)
	}
	for _, known := range nodeFrameworkHealth {
		if manifest.has(known.dependency) {
			return strictReadiness(known.route, readinessFromFramework, known.label)
		}
	}
	// A file-routed framework's routes are its files; a `.get('/health')` in
	// its code is far more often a client calling some other service.
	if code := bestHealthFact(fromStack(facts.of(factCodeHealth), "js", "nest")); code != nil && !nodePageFrameworks[candidate.Framework] {
		route := code.value
		if candidate.Framework == "nestjs" && code.stack == "nest" {
			if prefix := facts.of(factNestPrefix); len(prefix) > 0 {
				route = strings.TrimSuffix(prefix[0].value, "/") + route
			}
		}
		return answeredReadiness(route, readinessFromCode, code.label+"; any answer counts, since a router prefix may move it")
	}
	if nodePageFrameworks[candidate.Framework] {
		for _, provider := range nodeHostedSignIn {
			if manifest.has(provider.dependency) {
				return answeredReadiness(base+"/", readinessFromConvention,
					provider.label+" may send an anonymous visitor at / to its own sign-in site; any answer from / shows the server is up")
			}
		}
		label := nodeFrameworkLabel(candidate.Framework)
		if base != "" {
			return strictReadiness(base, readinessFromFramework, label+" serves under basePath "+base+" from next.config")
		}
		return strictReadiness("/", readinessFromConvention, label+" serves its pages at /")
	}
	label := candidate.Framework
	if label == "" {
		label = "the server"
	} else if framework := nodeFrameworkByName(label); framework != nil {
		label = framework.Label
	}
	return answeredReadiness("/", readinessFromConvention, "no health route found; any answer from "+label+" at / shows it is serving")
}

func nodeFrameworkLabel(name string) string {
	if framework := nodeFrameworkByName(name); framework != nil {
		return framework.Label
	}
	return name
}

var (
	nextAppRouteRE    = regexp.MustCompile(`^(?:src/)?app/(.+)/route\.(?:ts|js|mjs|tsx|jsx)$`)
	nextPagesRouteRE  = regexp.MustCompile(`^(?:src/)?pages/api/(.+)\.(?:ts|js|mjs|tsx|jsx)$`)
	nitroRouteRE      = regexp.MustCompile(`^(?:src/)?(?:server/)?(api|routes)/(.+?)(?:\.(?:get|head))?\.(?:ts|js|mjs)$`)
	svelteRouteRE     = regexp.MustCompile(`^src/routes/(.+)/\+server\.(?:ts|js)$`)
	remixRouteRE      = regexp.MustCompile(`^app/routes/([^/]+?)(?:/route)?\.(?:tsx|ts|jsx|js)$`)
	astroEndpointRE   = regexp.MustCompile(`^src/pages/(.+)\.(?:ts|js|mjs)$`)
	routeGroupSegment = regexp.MustCompile(`^(?:\(.*\)|@.*)$`)
)

// nodeRouteFile finds a health route a file-routed framework serves from a
// file's location, which is exact: the framework maps the path itself.
// React Router's config-based routes are the exception, reported as not
// strict.
func nodeRouteFile(framework string, facts rootFacts) (string, string, bool) {
	var found []readinessFact
	for _, fact := range facts.of(factRouteFile) {
		route, label := "", ""
		switch framework {
		case "nextjs":
			if match := nextAppRouteRE.FindStringSubmatch(fact.value); match != nil {
				route, label = fileRoute(match[1]), "Next.js route handler "+fact.value
			} else if match := nextPagesRouteRE.FindStringSubmatch(fact.value); match != nil {
				route, label = "/api"+fileRoute(match[1]), "Next.js API route "+fact.value
			}
		case "nuxt", "nitro", "solid-start", "tanstack-start":
			if match := nitroRouteRE.FindStringSubmatch(fact.value); match != nil {
				route = fileRoute(match[2])
				if match[1] == "api" {
					route = "/api" + route
				}
				label = "server route " + fact.value
			}
		case "sveltekit":
			if match := svelteRouteRE.FindStringSubmatch(fact.value); match != nil {
				route, label = fileRoute(match[1]), "SvelteKit endpoint "+fact.value
			}
		case "remix", "react-router":
			if match := remixRouteRE.FindStringSubmatch(fact.value); match != nil {
				route, label = fileRoute(strings.ReplaceAll(match[1], ".", "/")), "resource route "+fact.value
			}
		case "astro":
			if match := astroEndpointRE.FindStringSubmatch(fact.value); match != nil {
				route, label = fileRoute(match[1]), "Astro endpoint "+fact.value
			}
		}
		if route != "" && isHealthPath(route) {
			found = append(found, readinessFact{file: fact.file, value: route, label: label})
		}
	}
	best := bestHealthFact(found)
	if best == nil {
		return "", "", false
	}
	return best.value, best.label, framework != "react-router"
}

// fileRoute turns a route file's directory into its URL path, dropping the
// route groups and parallel-route slots a URL never shows, and refusing a
// dynamic segment, which is not one path.
func fileRoute(relative string) string {
	var segments []string
	for _, segment := range strings.Split(relative, "/") {
		switch {
		case segment == "" || segment == "index" || segment == "_index" || routeGroupSegment.MatchString(segment):
			continue
		case strings.ContainsAny(segment, "[]$:"):
			return ""
		}
		segments = append(segments, segment)
	}
	return "/" + strings.Join(segments, "/")
}

func pythonReadiness(candidate *DetectedCandidate, facts rootFacts) *DetectedReadiness {
	switch candidate.Framework {
	case "streamlit":
		return strictReadiness("/", readinessFromConvention, "Streamlit serves its page at /")
	case "gradio":
		return strictReadiness("/", readinessFromConvention, "Gradio serves its page at /")
	case "django":
		return djangoReadiness(facts)
	}
	rootRoute := "unrouted"
	if facts.has(factRootRoute) || facts.has(factRootStatic) {
		rootRoute = "routed"
	}
	var readiness *DetectedReadiness
	if app := bestHealthFact(fromStack(facts.of(factCodeHealth), "python")); app != nil {
		readiness = answeredReadiness(app.value, readinessFromCode, app.label+"; any answer counts, since a router prefix may move it")
	} else if candidate.Framework == "flask" && rootRoute == "routed" {
		// A Flask page behind a login redirects to its own login page, which
		// the probe follows; a FastAPI route behind a dependency answers 401.
		readiness = strictReadiness("/", readinessFromConvention, "the Flask application routes /")
	} else {
		label := map[string]string{"fastapi": "FastAPI", "flask": "Flask"}[candidate.Framework]
		if label == "" {
			label = "the server"
		}
		readiness = answeredReadiness("/", readinessFromConvention, "no health route found; any answer from "+label+" at / shows it is serving")
	}
	if candidate.Framework == "fastapi" || candidate.Framework == "flask" {
		readiness.RootRoute = rootRoute
	}
	return readiness
}

// djangoReadiness reads the project's root URLconf: a health route, then a
// view at /, then the admin's login page — each answers 200 without a
// session — and otherwise any answer from /.
func djangoReadiness(facts rootFacts) *DetectedReadiness {
	settings := djangoSettingsFile(facts)
	urls := djangoRootURLs(facts, settings)
	routes := map[string]string{}
	for _, fact := range facts.inFile(urls, factDjangoURL) {
		kind, route, _ := strings.Cut(fact.value, ":")
		if _, seen := routes[kind]; !seen {
			routes[kind] = route
		}
	}
	switch {
	case routes["health"] != "":
		return strictReadiness(routes["health"], readinessFromFramework, "Django health route "+routes["health"]+" in "+urls)
	case routes["root"] != "":
		readiness := strictReadiness("/", readinessFromConvention, "Django routes / in "+urls)
		readiness.RootRoute = "routed"
		return readiness
	case routes["admin"] != "":
		login := strings.TrimSuffix(routes["admin"], "/") + "/login/"
		return strictReadiness(login, readinessFromFramework, "Django admin login page "+login+" from "+urls)
	}
	readiness := answeredReadiness("/", readinessFromConvention, "no health route or view at / found; any answer from Django at / shows it is serving")
	if urls != "" {
		readiness.RootRoute = "unrouted"
	}
	return readiness
}

// djangoSettingsFile is the settings module the served process loads: the
// one wsgi.py or asgi.py names, else manage.py's, else the only settings.py.
func djangoSettingsFile(facts rootFacts) string {
	named := facts.of(factDjangoSettings)
	sort.SliceStable(named, func(i, j int) bool {
		rank := func(file string) int {
			switch path.Base(file) {
			case "wsgi.py", "asgi.py":
				return 0
			case "manage.py":
				return 1
			}
			return 2
		}
		return rank(named[i].file) < rank(named[j].file)
	})
	files := map[string]bool{}
	for _, fact := range facts {
		files[fact.file] = true
	}
	for _, fact := range named {
		module := strings.ReplaceAll(fact.value, ".", "/")
		for _, candidate := range []string{module + ".py", module + "/__init__.py"} {
			if files[candidate] {
				return candidate
			}
		}
	}
	settings := ""
	for _, fact := range facts {
		if path.Base(fact.file) == "settings.py" {
			if settings != "" && settings != fact.file {
				return ""
			}
			settings = fact.file
		}
	}
	return settings
}

// djangoRootURLs is the URLconf ROOT_URLCONF names, else the urls.py beside
// the settings, else the only urls.py.
func djangoRootURLs(facts rootFacts, settings string) string {
	urls := map[string]bool{}
	for _, fact := range facts.of(factDjangoURL) {
		urls[fact.file] = true
	}
	for _, fact := range djangoSettingsFacts(facts, settings, factDjangoURLConf) {
		if file := strings.ReplaceAll(fact.value, ".", "/") + ".py"; urls[file] {
			return file
		}
	}
	if settings != "" {
		directory := path.Dir(settings)
		if path.Base(directory) == "settings" {
			directory = path.Dir(directory)
		}
		if candidate := path.Join(directory, "urls.py"); urls[candidate] {
			return candidate
		}
	}
	if len(urls) == 1 {
		for file := range urls {
			return file
		}
	}
	return ""
}

// djangoSettingsFacts are one kind of fact from the settings module, falling
// back to the base module a split settings package imports it from.
func djangoSettingsFacts(facts rootFacts, settings, kind string) []readinessFact {
	if settings == "" {
		return nil
	}
	if own := facts.inFile(settings, kind); len(own) > 0 {
		return own
	}
	directory := path.Dir(settings)
	for _, base := range []string{"base.py", "common.py", "defaults.py", "__init__.py", "settings.py"} {
		if file := path.Join(directory, base); file != settings {
			if inherited := facts.inFile(file, kind); len(inherited) > 0 {
				return inherited
			}
		}
	}
	return nil
}

func addDjangoConcerns(readiness *DetectedReadiness, facts rootFacts) {
	settings := djangoSettingsFile(facts)
	if hosts := djangoSettingsFacts(facts, settings, factDjangoHosts); len(hosts) > 0 {
		readiness.AllowedHosts = strings.Fields(hosts[0].value)
		readiness.AllowedHostsSource = "ALLOWED_HOSTS in " + hosts[0].file
		// With DEBUG on, Django reads an empty list as the local names.
		if debug := djangoSettingsFacts(facts, settings, factDjangoDebug); len(readiness.AllowedHosts) == 0 && len(debug) > 0 && debug[0].value == "True" {
			readiness.AllowedHosts = []string{".localhost", "127.0.0.1", "[::1]"}
			readiness.AllowedHostsSource += " (empty, with DEBUG = True)"
		}
	}
	if redirect := djangoSettingsFacts(facts, settings, factDjangoSSL); len(redirect) > 0 {
		readiness.HTTPSRedirect = "SECURE_SSL_REDIRECT in " + redirect[0].file
		readiness.HTTPSRedirectIgnoresProxy = len(djangoSettingsFacts(facts, settings, factDjangoProxySSL)) == 0
	}
}

func addRailsConcerns(readiness *DetectedReadiness, facts rootFacts) {
	if forced := facts.of(factRailsForceSSL); len(forced) > 0 && !facts.has(factRailsAssumeSSL) {
		readiness.HTTPSRedirect = "config.force_ssl in " + forced[0].file
	}
	if hosts := facts.of(factRailsHost); len(hosts) > 0 && !facts.has(factRailsHostsOpen) {
		readiness.AllowedHosts = nil
		for _, host := range hosts {
			readiness.AllowedHosts = append(readiness.AllowedHosts, host.value)
		}
		readiness.AllowedHostsSource = "config.hosts in " + hosts[0].file
	}
}

// javaReadiness maps the health modules a JVM build declares to their
// endpoints, under the context path the service's configuration sets; a
// health route found in code is served under that path too.
func javaReadiness(marker *detectedMarkers, facts rootFacts) *DetectedReadiness {
	build := string(marker.pomXML) + string(marker.gradleBuild)
	settings := map[string]string{}
	for _, fact := range facts.of(factJVMSetting) {
		if _, seen := settings[fact.label]; !seen {
			settings[fact.label] = fact.value
		}
	}
	context := firstNonEmpty(settings["server.servlet.context-path"], settings["spring.webflux.base-path"],
		settings["micronaut.server.context-path"], settings["quarkus.http.root-path"])
	// A setting that moves the endpoint but names a variable with no default
	// is decided at deploy time. The framework's path would then be a guess a
	// healthy service may answer 404, so readiness asks only for an answer.
	for _, key := range []string{
		"server.servlet.context-path", "spring.webflux.base-path", "micronaut.server.context-path",
		"management.endpoints.web.base-path", "quarkus.http.root-path", "quarkus.http.non-application-root-path",
		"quarkus.smallrye-health.root-path",
	} {
		if strings.Contains(settings[key], "${") {
			readiness := declaredOrCodeReadiness(facts, "the JVM service", "", "jvm")
			if readiness.Source == readinessFromConvention {
				readiness.Evidence = key + " is set from a variable with no default; any answer from / shows the JVM service is serving"
			}
			return readiness
		}
	}
	switch {
	case strings.Contains(build, "spring-boot-starter-actuator") && settings["management.server.port"] == "":
		route := joinURLPath(context, firstNonEmpty(settings["management.endpoints.web.base-path"], "/actuator"), "health")
		if strings.Contains(build, "spring-boot-starter-security") {
			return answeredReadiness(route, readinessFromFramework,
				"Spring Boot Actuator health endpoint; Spring Security may answer 401 there, which counts")
		}
		return strictReadiness(route, readinessFromFramework, "Spring Boot Actuator health endpoint (spring-boot-starter-actuator)")
	case strings.Contains(build, "quarkus-smallrye-health"):
		nonApplication := firstNonEmpty(settings["quarkus.http.non-application-root-path"], "q")
		base := joinURLPath(settings["quarkus.http.root-path"], nonApplication)
		if strings.HasPrefix(nonApplication, "/") {
			base = nonApplication
		}
		health := firstNonEmpty(settings["quarkus.smallrye-health.root-path"], "health")
		route := joinURLPath(base, health, "ready")
		if strings.HasPrefix(health, "/") {
			route = joinURLPath(health, "ready")
		}
		return strictReadiness(route, readinessFromFramework, "Quarkus SmallRye Health readiness endpoint (quarkus-smallrye-health)")
	case strings.Contains(build, "micronaut-management"):
		return strictReadiness(joinURLPath(context, "health"), readinessFromFramework, "Micronaut management health endpoint (micronaut-management)")
	}
	readiness := declaredOrCodeReadiness(facts, "the JVM service", "", "jvm")
	switch {
	case context == "":
	case readiness.Source == readinessFromConvention:
		readiness.Path = strings.TrimSuffix(joinURLPath(context), "/") + "/"
	case readiness.Source == readinessFromCode:
		readiness.Path = joinURLPath(context, readiness.Path)
	}
	return readiness
}

func phpReadiness(candidate *DetectedCandidate, facts rootFacts) *DetectedReadiness {
	if candidate.Framework == "laravel" {
		for _, fact := range facts.of(factDeclaredHealth) {
			if fact.stack == "laravel" {
				return strictReadiness(fact.value, readinessFromFramework, fact.label)
			}
		}
	}
	switch candidate.Framework {
	case "symfony", "slim":
		return answeredReadiness("/", readinessFromConvention, "no health route found; any answer from "+candidate.Framework+" at / shows it is serving")
	}
	return strictReadiness("/", readinessFromConvention, "the PHP application serves its pages at /")
}

// dockerfileReadiness proves a custom image is serving: by its own
// HEALTHCHECK when it has one, then by the health endpoints the source
// declares, then as the recipe candidate for the same root would.
func dockerfileReadiness(candidate *DetectedCandidate, marker *detectedMarkers, facts rootFacts, siblings []DetectedCandidate) *DetectedReadiness {
	var readiness *DetectedReadiness
	dockerfile := marker.dockerfileFor(candidate)
	healthcheck := parseDockerfileHealthcheck(dockerfile.content)
	if healthcheck != nil && !healthcheck.disabled {
		readiness = healthcheck.readiness(candidate.Port)
	}
	if readiness == nil {
		readiness = platformReadiness(facts)
	}
	if readiness == nil {
		for _, fact := range facts.of(factDeclaredHealth) {
			if fact.stack == "rails" || fact.stack == "laravel" {
				readiness = strictReadiness(fact.value, readinessFromFramework, fact.label)
				break
			}
		}
	}
	if readiness == nil {
		for _, sibling := range siblings {
			if sibling.BuildMethod == BuildRecipe && sibling.Readiness != nil && sibling.Profile == ProfileWeb {
				copied := *sibling.Readiness
				copied.ModelCache, copied.Attempts, copied.IntervalSeconds, copied.SlowStart = "", 0, 0, ""
				readiness = &copied
				break
			}
		}
	}
	if readiness == nil && facts.has(factRailsAPIOnly) {
		readiness = answeredReadiness("/", readinessFromConvention, "a Rails API application has no page at /; any answer shows it is serving")
	}
	if readiness == nil && (facts.has(factRailsForceSSL) || facts.has(factRailsHost)) {
		readiness = strictReadiness("/", readinessFromConvention, "the application serves its pages at /")
	}
	if readiness == nil {
		return nil
	}
	addRailsConcerns(readiness, facts)
	if len(marker.pythonFiles) > 0 {
		addDjangoConcerns(readiness, facts)
	}
	applyReadinessBudget(readiness, candidate, marker, facts, dockerfileStartText(dockerfile.content))
	if healthcheck != nil && !healthcheck.disabled {
		healthcheck.extendBudget(readiness)
	}
	return readiness
}

// migrationStartRE recognises a start command that applies a schema before
// the server listens.
var migrationStartRE = regexp.MustCompile(`\bmigrate\b|\bdb:prepare\b|\bdb:migrate\b|\balembic\s+upgrade\b|\bdb\s+push\b|\bmigration:run\b|\bmigration:up\b`)

var pythonModelDistributions = []string{
	"transformers", "diffusers", "sentence-transformers", "torch", "openai-whisper", "faster-whisper",
	"ultralytics", "huggingface-hub", "timm", "open-clip-torch", "easyocr", "tts",
}

// applyReadinessBudget gives a slow start the time it needs: a JVM and a
// start command that migrates first get twice the default; a model
// downloaded while the application starts gets ten minutes.
func applyReadinessBudget(readiness *DetectedReadiness, candidate *DetectedCandidate, marker *detectedMarkers, facts rootFacts, start string) {
	attempts, interval := readinessDefaultAttempts, readinessDefaultInterval
	slow := func(reason string, a, i int) {
		if a*i > attempts*interval {
			attempts, interval, readiness.SlowStart = a, i, reason
		}
	}
	jvm := candidate.Recipe == "java" || (candidate.BuildMethod == BuildDockerfile && (len(marker.pomXML) > 0 || len(marker.gradleBuild) > 0))
	if jvm {
		slow("a JVM service can take minutes to start on a small server", readinessSlowAttempts, readinessDefaultInterval)
	}
	// Rails' generated docker-entrypoint runs db:prepare before the server
	// it is given, so its start migrates although the CMD does not say so.
	railsEntrypoint := candidate.BuildMethod == BuildDockerfile && railsDockerEntrypoint(marker.dockerfileFor(candidate).content) &&
		(facts.has(factRailsForceSSL) || facts.has(factRailsAPIOnly) || len(facts.of(factDeclaredHealth)) > 0)
	if candidate.SchemaCommand != "" || candidate.SchemaInStart || migrationStartRE.MatchString(start) || railsEntrypoint {
		slow("the start command applies database migrations before the server listens", readinessSlowAttempts, readinessDefaultInterval)
		if deps := readPythonDependencies(marker.pythonFiles); deps.has("wagtail") {
			slow("Wagtail's first migrations take minutes before the server listens", readinessLongAttempts, 5)
		}
	}
	if len(marker.pythonFiles) > 0 {
		deps := readPythonDependencies(marker.pythonFiles)
		model := false
		for _, name := range pythonModelDistributions {
			model = model || deps.has(name)
		}
		if loads := facts.of(factModelLoad); model && len(loads) > 0 {
			readiness.ModelDownload = loads[0].value + " in " + loads[0].file
			if candidate.BuildMethod == BuildRecipe {
				readiness.ModelCache = "/root/.cache"
				if deps.has("transformers") || deps.has("diffusers") || deps.has("sentence-transformers") || deps.has("huggingface-hub") {
					readiness.ModelCache = "/root/.cache/huggingface"
				}
			}
			slow("the application downloads a model ("+loads[0].value+") before it listens", readinessLongAttempts, readinessLongInterval)
		}
	}
	if attempts != readinessDefaultAttempts || interval != readinessDefaultInterval {
		readiness.Attempts, readiness.IntervalSeconds = attempts, interval
	}
}

func railsDockerEntrypoint(content []byte) bool {
	for _, instruction := range dockerfileFinalStage(content) {
		if instruction[0] == "ENTRYPOINT" && strings.Contains(instruction[1], "bin/docker-entrypoint") {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

// joinURLPath joins path pieces into one absolute URL path without a
// trailing slash.
func joinURLPath(parts ...string) string {
	var segments []string
	for _, part := range parts {
		if part = strings.Trim(strings.TrimSpace(part), "/"); part != "" {
			segments = append(segments, part)
		}
	}
	return "/" + strings.Join(segments, "/")
}

// dockerfileHealthcheck is the final stage's HEALTHCHECK.
type dockerfileHealthcheck struct {
	disabled                              bool
	command                               string
	interval, startPeriod, startInterval  int
	retries                               int
	path                                  string
	port                                  int
	portFromEnvironment, plainHTTPRequest bool
}

var (
	healthcheckURLRE = regexp.MustCompile(`https?://(?:localhost|127\.0\.0\.1|0\.0\.0\.0|\[::1\])(?::(\d+|\$\{?PORT\}?))?(/[^\s'"\]\\;&|)]*)?`)
	durationRE       = regexp.MustCompile(`^([0-9]+)(ms|s|m|h)?$`)
)

// dockerfileFinalStage returns the final stage's instructions, keyword
// upper-cased, with continuation lines joined and comments dropped.
func dockerfileFinalStage(content []byte) [][2]string {
	var stage [][2]string
	var pending strings.Builder
	flush := func() {
		line := strings.TrimSpace(pending.String())
		pending.Reset()
		if line == "" {
			return
		}
		keyword, arguments, _ := strings.Cut(line, " ")
		keyword = strings.ToUpper(keyword)
		if keyword == "FROM" {
			stage = nil
		}
		stage = append(stage, [2]string{keyword, strings.TrimSpace(arguments)})
	}
	for _, raw := range strings.Split(string(content), "\n") {
		trimmed := strings.TrimSpace(raw)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasSuffix(trimmed, "\\") {
			pending.WriteString(strings.TrimSuffix(trimmed, "\\") + " ")
			continue
		}
		pending.WriteString(trimmed)
		flush()
	}
	flush()
	return stage
}

func parseDockerfileHealthcheck(content []byte) *dockerfileHealthcheck {
	var result *dockerfileHealthcheck
	for _, instruction := range dockerfileFinalStage(content) {
		if instruction[0] != "HEALTHCHECK" {
			continue
		}
		healthcheck := &dockerfileHealthcheck{interval: 30, retries: 3, startInterval: 5}
		fields := strings.Fields(instruction[1])
		if len(fields) > 0 && strings.EqualFold(fields[0], "NONE") {
			healthcheck.disabled = true
			result = healthcheck
			continue
		}
		rest := instruction[1]
		for len(fields) > 0 && strings.HasPrefix(fields[0], "--") {
			name, value, _ := strings.Cut(strings.TrimPrefix(fields[0], "--"), "=")
			switch name {
			case "interval":
				healthcheck.interval = dockerDurationSeconds(value, healthcheck.interval)
			case "start-period":
				healthcheck.startPeriod = dockerDurationSeconds(value, 0)
			case "start-interval":
				healthcheck.startInterval = dockerDurationSeconds(value, healthcheck.startInterval)
			case "retries":
				if retries, err := strconv.Atoi(value); err == nil && retries > 0 && retries <= 100 {
					healthcheck.retries = retries
				}
			}
			rest = strings.TrimSpace(strings.TrimPrefix(rest, fields[0]))
			fields = fields[1:]
		}
		command, found := strings.CutPrefix(rest, "CMD")
		if !found {
			command, found = strings.CutPrefix(rest, "cmd")
		}
		if !found {
			continue
		}
		healthcheck.command = strings.TrimSpace(command)
		lower := strings.ToLower(healthcheck.command)
		if match := healthcheckURLRE.FindStringSubmatch(healthcheck.command); match != nil &&
			(strings.Contains(lower, "curl") || strings.Contains(lower, "wget")) {
			healthcheck.plainHTTPRequest = true
			healthcheck.path = firstNonEmpty(match[2], "/")
			switch {
			case strings.Contains(match[1], "PORT"):
				healthcheck.portFromEnvironment = true
			case match[1] != "":
				healthcheck.port, _ = strconv.Atoi(match[1])
			case strings.HasPrefix(match[0], "https"):
				healthcheck.port = 443
			default:
				healthcheck.port = 80
			}
		}
		result = healthcheck
	}
	return result
}

func dockerDurationSeconds(value string, fallback int) int {
	match := durationRE.FindStringSubmatch(strings.TrimSpace(value))
	if match == nil {
		return fallback
	}
	amount, _ := strconv.Atoi(match[1])
	switch match[2] {
	case "ms":
		amount /= 1000
	case "m":
		amount *= 60
	case "h":
		amount *= 3600
	}
	if amount < 1 {
		amount = 1
	}
	return amount
}

// readiness turns the HEALTHCHECK into the release's own check: its request
// when it is a plain HTTP request to the port the release serves on (the
// probe then answers in seconds rather than on Docker's schedule), otherwise
// the container's health state.
func (h *dockerfileHealthcheck) readiness(port int) *DetectedReadiness {
	if h.plainHTTPRequest && (h.portFromEnvironment || (port > 0 && h.port == port)) && isSafeReadinessPath(h.path) {
		return strictReadiness(h.path, readinessFromHealthcheck, "HEALTHCHECK in the Dockerfile requests "+h.path)
	}
	first := strings.Fields(strings.Trim(h.command, `[]"`))
	label := "a command"
	if len(first) > 0 {
		label = strings.Trim(first[0], `",`)
	}
	return &DetectedReadiness{
		Kind: string(CheckDockerHealth), Source: readinessFromHealthcheck,
		Evidence: "HEALTHCHECK in the Dockerfile runs " + boundedEvidence(label) + "; readiness waits for Docker to report the container healthy",
	}
}

// extendBudget waits at least as long as Docker's own schedule takes to
// call the container healthy or unhealthy: the start period, then each
// retry one interval apart.
func (h *dockerfileHealthcheck) extendBudget(readiness *DetectedReadiness) {
	seconds := h.startPeriod + h.interval*(h.retries+1) + 10
	if readiness.Kind == string(CheckHTTP) {
		// The probe asks the application directly; only the start period is
		// the image's own statement of how long it takes.
		seconds = h.startPeriod + 30
	}
	attempts, interval := readiness.Attempts, readiness.IntervalSeconds
	if attempts == 0 {
		attempts, interval = readinessDefaultAttempts, readinessDefaultInterval
	}
	if attempts*interval >= seconds {
		return
	}
	interval = readinessDefaultInterval
	attempts = (seconds + interval - 1) / interval
	if attempts > readinessLongAttempts {
		attempts = readinessLongAttempts
		interval = (seconds + attempts - 1) / attempts
		if interval > 60 {
			interval = 60
		}
	}
	readiness.Attempts, readiness.IntervalSeconds = attempts, interval
	if readiness.SlowStart == "" {
		readiness.SlowStart = "the Dockerfile's HEALTHCHECK allows " + strconv.Itoa(seconds) + " s before it calls the container unhealthy"
	}
}

func isSafeReadinessPath(route string) bool {
	return strings.HasPrefix(route, "/") && len(route) <= 512 && !strings.ContainsAny(route, "\x00\r\n\t ?#\\")
}

// Validation of what a stored detection may carry: closed vocabularies,
// bounded text, and no credential material in anything a page shows.
func validateDetectedServing(candidate DetectedCandidate) error {
	if readiness := candidate.Readiness; readiness != nil {
		switch {
		case readiness.Kind != string(CheckHTTP) && readiness.Kind != string(CheckDockerHealth):
			return errors.New("detected readiness kind is invalid")
		case readiness.Kind == string(CheckHTTP) && !isSafeReadinessPath(readiness.Path):
			return errors.New("detected readiness path is invalid")
		case readiness.Kind == string(CheckDockerHealth) && (readiness.Path != "" || readiness.AcceptAnyAnswer):
			return errors.New("detected Docker-health readiness carries HTTP fields")
		case readiness.Attempts < 0 || readiness.Attempts > 60 || readiness.IntervalSeconds < 0 || readiness.IntervalSeconds > 60:
			return errors.New("detected readiness budget is outside the check bounds")
		case readiness.RootRoute != "" && readiness.RootRoute != "routed" && readiness.RootRoute != "unrouted":
			return errors.New("detected root route state is invalid")
		case len(readiness.AllowedHosts) > 64:
			return errors.New("detected host allowlist is too long")
		}
		switch readiness.Source {
		case readinessFromHealthcheck, readinessFromPlatform, readinessFromFramework, readinessFromCode, readinessFromConvention:
		default:
			return errors.New("detected readiness source is invalid")
		}
		if readiness.ModelCache != "" && (!strings.HasPrefix(readiness.ModelCache, "/") || len(readiness.ModelCache) > 256) {
			return errors.New("detected model cache directory is invalid")
		}
		texts := append([]string{readiness.Evidence, readiness.SlowStart, readiness.ModelDownload, readiness.HTTPSRedirect,
			readiness.AllowedHostsSource}, readiness.AllowedHosts...)
		if err := validateServingTexts(texts...); err != nil {
			return err
		}
	}
	if worker := candidate.BackgroundWorker; worker != nil {
		if worker.Library == "" {
			return errors.New("detected background worker names no library")
		}
		if err := validateServingTexts(worker.Library, worker.Kind, worker.Evidence); err != nil {
			return err
		}
	}
	if detach := candidate.StartDetaches; detach != nil {
		if detach.Command == "" || (detach.Effect != startDetachExits && detach.Effect != startDetachBackgrounds) {
			return errors.New("detected start command finding is malformed")
		}
		if err := validateServingTexts(detach.Command, detach.Script, detach.Source, detach.Reason, detach.Action); err != nil {
			return err
		}
	}
	return nil
}

func validateServingTexts(values ...string) error {
	for _, value := range values {
		if len(value) > 512 || strings.ContainsAny(value, "\x00\r\n") {
			return fmt.Errorf("detected serving evidence is malformed")
		}
		if rejectPlanSecretLiteral("detected serving evidence", value) != nil {
			return fmt.Errorf("detected serving evidence contains credential material")
		}
	}
	return nil
}
