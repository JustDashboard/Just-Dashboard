package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/Wayy01/Just-Dashboard/backend/internal/assistant"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/go-chi/chi/v5"
)

// The rest of the dashboard as the assistant reaches it: every route of the API
// under /api/v1, read off the router (chi.Walk) once per router, for find_routes
// to look up and api_request to make as the person. A route is described by its
// handler's name (handlerName), or by the deployment API's own summary, and its
// area by a sentence written here. What the assistant cannot make is listed with
// the reason, so the model is told rather than left to guess; it is short, and
// each line is something no person's role would change:
//
//   - the assistant's own routes, which would ask the model from inside a press,
//     and the routes that ask it for something else (a run's diagnosis, a
//     drawing's diagram);
//   - signing in and out, the second factor, passkeys, sessions and a new API
//     key, which are a person's own credentials;
//   - uninstalling the dashboard, linking it to a just-dashboard.com account or
//     connecting it to a Just Hub, or turning its usage count on (security
//     invariants 12, 16 and 17 ask for a person's own press);
//   - what is not a request and an answer: WebSockets and streams — a terminal,
//     a container's exec, Remote Desktop, a log or an event stream — which a
//     recorder cannot hold;
//   - what another caller makes: webhooks, the MCP endpoint, the tailnet gate,
//     a probe agent's routes.

// apiRoute is one route of the API as the assistant sees it.
type apiRoute struct {
	Method string
	// Pattern is chi's, under /api/v1; Path is how it is shown, with no
	// trailing slash.
	Pattern     string
	Path        string
	Area        string
	Words       string
	Destructive bool
	// Excluded is why the assistant cannot make it, empty when it can.
	Excluded string
}

type apiCatalogue struct {
	routes []apiRoute
	byKey  map[string]*apiRoute
	areas  []assistant.APIArea
	// excluded is what the prompt says the assistant cannot reach, in words.
	excluded []string
}

type apiCatalogueSource struct {
	once sync.Once
	cat  *apiCatalogue
}

func (s *Server) apiCatalogue() *apiCatalogue {
	src := s.deployOpenAPI.Load()
	if src == nil {
		return &apiCatalogue{byKey: map[string]*apiRoute{}}
	}
	src.routes.once.Do(func() { src.routes.cat = buildAPICatalogue(src.root, src.document()) })
	return src.routes.cat
}

// agentStreamHandlers are the handlers that upgrade to a WebSocket or stream
// until the client goes, by the name handlerName reads. A recorded request cannot
// hold one. TestEveryStreamingHandlerIsKeptFromTheAssistant reads the handlers'
// source for an upgrade and fails for one missing here.
var agentStreamHandlers = map[string]bool{
	"boardLive": true, "redisMonitor": true, "redisSubscribe": true, "deploymentRunStream": true,
	"deploymentRequestStream": true, "deploymentLifecycleStream": true, "siteRequestStream": true,
	"containerStream": true, "containerStatStream": true, "containerExec": true, "imagePull": true,
	"imageBuild": true, "stackRun": true, "dockerEventStream": true, "jobStream": true, "logStream": true,
	"remoteDesktopSession": true, "systemStream": true, "terminalAttach": true,
}

const whyStream = "a live stream (a WebSocket), which a request and its answer cannot hold; take the person to its page"

// agentExcludedRoutes are single routes the assistant does not make, by method
// and pattern under /api/v1 with no trailing slash.
var agentExcludedRoutes = map[string]string{
	"POST /deploy/{id}/runs/{run}/diagnosis": "it asks the model about the run; read the run itself (recent_runs, run_transcript)",
	"POST /boards/diagram":                   "it asks the model to draw; describe the drawing in words instead",
	"POST /dashboard/uninstall":              "uninstalling the dashboard is the person's own press, on its page",
	"PUT /dashboard/usage-ping":              "the usage count is an administrator's own yes (security invariant 16)",
	"POST /dashboard/link":                   "linking to a just-dashboard.com account is the person's own, in the browser (security invariant 12)",
	"DELETE /dashboard/link":                 "unlinking a just-dashboard.com account is the person's own, in the browser (security invariant 12)",
	"PUT /dashboard/link/scope":              "the account link's scope is the person's own, in the browser (security invariant 12)",
	"DELETE /dashboard/link/request":         "the account link is the person's own, in the browser (security invariant 12)",
	"POST /dashboard/link/sync":              "the account link is the person's own, in the browser (security invariant 12)",
	"POST /dashboard/hub":                    "connecting to a Just Hub is the person's own, in the browser (security invariant 17)",
	"DELETE /dashboard/hub":                  "disconnecting from a Just Hub is the person's own, in the browser (security invariant 17)",
	"PUT /dashboard/hub/scope":               "the hub link's scope is the person's own, in the browser (security invariant 17)",
	"DELETE /dashboard/hub/request":          "the hub link is the person's own, in the browser (security invariant 17)",
	"POST /dashboard/hub/sync":               "the hub link is the person's own, in the browser (security invariant 17)",
	"DELETE /dashboard/hub/signin/{userId}":  "whether Just Hub may sign an account in is that person's own choice, or an administrator's, in the browser (the exception to security invariant 2)",
	"POST /tokens":                           "a new key's value is shown once and would reach nobody, masked from the model and the page; the person makes it under Account → API keys",
	"POST /tokens/deploy":                    "a new key's value is shown once and would reach nobody, masked from the model and the page; the person makes it under Account → API keys",
	"POST /tokens/mcp":                       "a new key's value is shown once and would reach nobody, masked from the model and the page; the person makes it under Account → API keys",
	"POST /deploy/{id}/hooks/{triggerID}":    "a webhook authenticates by its own signature, not as a person",
}

// agentExcludedAreas are whole parts of the API, by the start of their path.
var agentExcludedAreas = []struct{ prefix, why string }{
	{"/assistant", "the assistant's own routes: its chats, its provider and its keys"},
	{"/databases/assistant", "the assistant's provider, under its old address"},
	{"/databases/{id}/assistant", "a database's assistant: this is it"},
	{"/deploy/{id}/assistant", "a deployment's assistant: this is it"},
	{"/deploy/drafts/{draft}/assistant", "a new deployment's assistant: this is it"},
	{"/auth/", "signing in and out and the second factor are the person's own"},
	{"/account/password", "the account's password is the person's own"},
	{"/account/2fa", "the account's second factor is the person's own"},
	{"/account/recovery-codes", "the account's recovery codes are the person's own"},
	{"/account/passkeys", "the account's passkeys are the person's own"},
	{"/account/sessions", "the account's sessions are the person's own"},
	{"/account/hub-signin", "letting Just Hub sign the account in is the person's own choice (the exception to security invariant 2)"},
	{"/hooks/", "a webhook authenticates by its own signature, not as a person"},
	{"/mcp", "the MCP endpoint takes an MCP key, not a person"},
	{"/tailnet/", "the tailnet gate answers the proxy alone"},
	{"/probe-agent/", "a probe agent's own routes, which take its certificate"},
	{"/remote-desktop/bridge/", whyStream},
}

// agentAreaBlurbs say what each area of the API is for, for the prompt and
// find_routes. An area with no line here is named by its routes alone.
var agentAreaBlurbs = map[string]string{
	"account":         "the person's own profile and workspace",
	"audit":           "the audit log",
	"backups":         "backup jobs, their runs, restores and the encryption key",
	"boards":          "drawings",
	"certificates":    "TLS certificates and their ACME accounts",
	"cli":             "the jd command line",
	"connections":     "the host's network connections",
	"cron":            "scheduled tasks",
	"dashboard":       "the dashboard itself: its configuration, updates and restart",
	"dashboard-users": "the dashboard's accounts and their roles",
	"databases":       "database connections, their schemas, data, SQL queries, users, backups and settings",
	"deploy":          "deployments: environments, runs, releases, variables, domains, schedules, credentials",
	"docker":          "containers, images, volumes, networks and Compose stacks",
	"exposure":        "what the host exposes to the network",
	"fail2ban":        "fail2ban jails and bans",
	"files":           "the host's files: read, write, upload, move",
	"firewall":        "the host firewall's state and rules",
	"git":             "Git repositories on the host",
	"jobs":            "long-running jobs the dashboard started",
	"logins":          "who signed in to the host",
	"logs":            "the host's log files and their search",
	"map":             "the server map",
	"metrics":         "the host's CPU, memory, disk and network readings",
	"network":         "interfaces, DNS, routing, VPN, captures and traffic",
	"packages":        "the host's packages and their updates",
	"pm2":             "PM2 processes",
	"ports":           "listening ports",
	"processes":       "the host's processes",
	"proxy":           "the reverse proxy: sites, routes, TLS and traffic",
	"remote-desktop":  "Remote Desktop computers",
	"security":        "the host's security posture",
	"ssh":             "the SSH server's settings",
	"ssh-sessions":    "SSH sessions on the host",
	"system":          "the host: its information, services and power",
	"system-accounts": "the host's system accounts",
	"system-users":    "the host's Linux users and groups",
	"systemd":         "systemd services and timers",
	"terminal":        "terminal sessions (their windows, not their keystrokes)",
	"tokens":          "API keys",
}

// agentExclusion is why the assistant does not make a route, if it does not.
func agentExclusion(method, pattern, handler string) string {
	trimmed := strings.TrimSuffix(pattern, "/")
	if why, ok := agentExcludedRoutes[method+" "+trimmed]; ok {
		return why
	}
	if agentStreamHandlers[handler] {
		return whyStream
	}
	for _, area := range agentExcludedAreas {
		if trimmed == strings.TrimSuffix(area.prefix, "/") || strings.HasPrefix(pattern, area.prefix) ||
			strings.HasPrefix(pattern, strings.TrimSuffix(area.prefix, "/")+"/") {
			return area.why
		}
	}
	return ""
}

func buildAPICatalogue(root chi.Routes, doc *openapiDocument) *apiCatalogue {
	cat := &apiCatalogue{byKey: map[string]*apiRoute{}}
	var document map[string]any
	if doc != nil {
		_ = json.Unmarshal(doc.JSON, &document)
	}
	paths, _ := document["paths"].(map[string]any)
	_ = chi.Walk(root, func(method, pattern string, handler http.Handler, middlewares ...func(http.Handler) http.Handler) error {
		if !strings.HasPrefix(pattern, "/api/v1/") || method == http.MethodHead || method == http.MethodOptions ||
			method == http.MethodConnect || method == http.MethodTrace || method == "QUERY" {
			return nil
		}
		under := strings.TrimPrefix(pattern, "/api/v1")
		name := handlerName(handler)
		route := apiRoute{
			Method: method, Pattern: under, Path: strings.TrimSuffix(under, "/"), Area: apiArea(under),
			Destructive: isDestructive(middlewares), Excluded: agentExclusion(method, under, name),
		}
		if route.Path == "" {
			route.Path = "/"
		}
		if item, ok := paths[under].(map[string]any); ok {
			if op, ok := item[strings.ToLower(method)].(map[string]any); ok {
				route.Words, _ = op["summary"].(string)
			}
		}
		if route.Words == "" {
			route.Words = handlerWords(name, method, route.Path)
		}
		cat.routes = append(cat.routes, route)
		return nil
	})
	sort.Slice(cat.routes, func(i, j int) bool {
		if cat.routes[i].Path != cat.routes[j].Path {
			return cat.routes[i].Path < cat.routes[j].Path
		}
		return cat.routes[i].Method < cat.routes[j].Method
	})
	areas := map[string]*assistant.APIArea{}
	order := []string{}
	for i := range cat.routes {
		route := &cat.routes[i]
		cat.byKey[route.Method+" "+route.Pattern] = route
		if route.Excluded != "" {
			continue
		}
		area := areas[route.Area]
		if area == nil {
			area = &assistant.APIArea{Name: route.Area, Blurb: agentAreaBlurbs[route.Area]}
			areas[route.Area] = area
			order = append(order, route.Area)
		}
		area.Routes++
		// A few of an area's reads, the shortest first, are the examples: they
		// are where a model starts.
		if route.Method == http.MethodGet && len(area.Examples) < 3 && strings.Count(route.Path, "/") <= 3 {
			area.Examples = append(area.Examples, "GET "+route.Path)
		}
	}
	sort.Strings(order)
	for _, name := range order {
		cat.areas = append(cat.areas, *areas[name])
	}
	cat.excluded = []string{
		"the assistant's own routes and the ones that ask the model",
		"signing in, the password, the second factor, passkeys, sessions and making an API key",
		"uninstalling the dashboard, its just-dashboard.com link, its Just Hub connection and its usage count",
		"live streams: a terminal, a container's exec, Remote Desktop, log and event streams",
		"webhooks, the MCP endpoint and other machines' routes",
	}
	return cat
}

// apiArea is a route's area: the first segment of its path.
func apiArea(pattern string) string {
	head, _, _ := strings.Cut(strings.TrimPrefix(pattern, "/"), "/")
	return head
}

// handlerWords is a route described by its handler's name — handleDBConnList
// is "db conn list" — or by its method and path when the name is not to be had.
func handlerWords(name, method, path string) string {
	if name == "" {
		words := []string{}
		for _, part := range strings.Split(strings.Trim(path, "/"), "/") {
			if part != "" && !strings.HasPrefix(part, "{") {
				words = append(words, strings.ReplaceAll(part, "-", " "))
			}
		}
		return strings.ToLower(method) + " " + strings.Join(words, " ")
	}
	var words []string
	runes := []rune(strings.ToUpper(name[:1]) + name[1:])
	start := 0
	for i := 1; i < len(runes); i++ {
		upper := unicode.IsUpper(runes[i])
		boundary := upper && (unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1]) ||
			(i+1 < len(runes) && unicode.IsLower(runes[i+1]) && unicode.IsUpper(runes[i-1])))
		if boundary {
			words = append(words, string(runes[start:i]))
			start = i
		}
	}
	words = append(words, string(runes[start:]))
	return readableWords(words)
}

// handlerAbbreviations are the short forms handler names use, in full.
var handlerAbbreviations = map[string]string{
	"db": "database", "dbs": "databases", "conn": "connection", "conns": "connections", "cfg": "configuration",
	"config": "configuration", "env": "environment", "envs": "environments", "var": "variable", "vars": "variables",
	"repo": "repository", "repos": "repositories", "info": "details", "stats": "statistics", "msg": "message",
	"img": "image", "imgs": "images", "pkg": "package", "pkgs": "packages", "proc": "process", "procs": "processes",
	"cert": "certificate", "certs": "certificates", "auth": "authentication", "fs": "files", "ws": "websocket",
}

// handlerVerbs are what a handler's name ends or goes on with when it says what
// it does; the label leads with it, as a person would.
var handlerVerbs = map[string]bool{
	"list": true, "get": true, "read": true, "create": true, "add": true, "update": true, "put": true, "patch": true,
	"set": true, "delete": true, "remove": true, "run": true, "start": true, "stop": true, "restart": true,
	"query": true, "test": true, "apply": true, "rename": true, "enable": true, "disable": true, "sync": true,
	"scan": true, "check": true, "preview": true, "import": true, "export": true, "restore": true, "rotate": true,
	"generate": true, "save": true, "reload": true, "pause": true, "resume": true, "pull": true, "push": true,
	"prune": true, "inspect": true, "kill": true, "upload": true, "download": true, "search": true, "explain": true,
}

// readableWords turns a handler name's words — "db conn list" — into a label —
// "list database connections": abbreviations in full, the verb first, and what a
// list lists in the plural.
func readableWords(words []string) string {
	out := make([]string, 0, len(words))
	for _, word := range words {
		word = strings.ToLower(word)
		if full, ok := handlerAbbreviations[word]; ok {
			word = full
		}
		out = append(out, word)
	}
	for i := len(out) - 1; i > 0; i-- {
		if !handlerVerbs[out[i]] {
			continue
		}
		verb := out[i]
		rest := append(append([]string{}, out[:i]...), out[i+1:]...)
		if verb == "list" && i == len(out)-1 {
			last := rest[len(rest)-1]
			switch {
			case strings.HasSuffix(last, "s"):
			case strings.HasSuffix(last, "y") && !strings.HasSuffix(last, "ey"):
				rest[len(rest)-1] = strings.TrimSuffix(last, "y") + "ies"
			default:
				rest[len(rest)-1] = last + "s"
			}
		}
		out = append([]string{verb}, rest...)
		break
	}
	return strings.Join(out, " ")
}

// find looks routes up by words: every word of the query in a route's path,
// area or description, the fewest words first. A query that matches nothing
// that way is read word by word.
func (c *apiCatalogue) find(query, method string) (open, closed []*apiRoute) {
	words := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' && r != '{' && r != '}'
	})
	type scored struct {
		route *apiRoute
		score int
	}
	var all, some []scored
	for i := range c.routes {
		route := &c.routes[i]
		if method != "" && route.Method != method {
			continue
		}
		hay := strings.ToLower(route.Path + " " + route.Area + " " + route.Words)
		matched := 0
		for _, word := range words {
			if strings.Contains(hay, word) || strings.Contains(hay, strings.TrimSuffix(word, "s")) {
				matched++
			}
		}
		// A shorter path is the more general route, and the likelier start.
		score := matched*100 - strings.Count(route.Path, "/")
		if matched == len(words) {
			all = append(all, scored{route, score})
		} else if matched > 0 {
			some = append(some, scored{route, score})
		}
	}
	found := all
	if len(found) == 0 {
		found = some
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].score > found[j].score })
	for _, f := range found {
		if f.route.Excluded != "" {
			closed = append(closed, f.route)
		} else {
			open = append(open, f.route)
		}
	}
	return open, closed
}

// agentFindLimit bounds how many routes one look-up lists.
const agentFindLimit = 40

// findRoutes answers find_routes.
func (c *apiCatalogue) findRoutes(in assistant.FindInput) agentResult {
	if strings.TrimSpace(in.Query) == "" {
		var b strings.Builder
		b.WriteString("The API's areas, each under /api/v1 (path, routes, what for):\n")
		for _, area := range c.areas {
			fmt.Fprintf(&b, "- /%s (%d)", area.Name, area.Routes)
			if area.Blurb != "" {
				b.WriteString(": " + area.Blurb)
			}
			b.WriteString("\n")
		}
		b.WriteString("Look an area's routes up with its name, or with words for what you want.")
		return agentResult{text: b.String(), said: operatorCount(len(c.areas), "area"), ok: true}
	}
	open, closed := c.find(in.Query, in.Method)
	if len(open) == 0 && len(closed) == 0 {
		return agentResult{text: fmt.Sprintf("No route matches %q. Try fewer or other words, or an area's name (an empty query lists them).", in.Query),
			said: "no route matched", ok: true}
	}
	var b strings.Builder
	shown := min(len(open), agentFindLimit)
	fmt.Fprintf(&b, "Routes under /api/v1 matching %q (%d", in.Query, len(open))
	if shown < len(open) {
		fmt.Fprintf(&b, ", the first %d shown; add words to narrow it", shown)
	}
	b.WriteString("):\n")
	for _, route := range open[:shown] {
		kind := agentKindRead
		switch {
		case route.Destructive || route.Method == http.MethodDelete:
			kind = agentKindDestructive
		case route.Method != http.MethodGet:
			kind = agentKindChange
		}
		if _, reaches := agentReachingReads[route.Method+" "+strings.TrimSuffix(route.Pattern, "/")]; reaches {
			// Said, so the model knows why a read of its waited.
			kind += ", asked first: it reaches another machine"
		}
		fmt.Fprintf(&b, "- %s %s: %s [%s]\n", route.Method, route.Path, route.Words, kind)
	}
	if len(closed) > 0 {
		b.WriteString("Matching routes the assistant cannot make:\n")
		for _, route := range closed[:min(len(closed), 10)] {
			fmt.Fprintf(&b, "- %s %s: %s\n", route.Method, route.Path, route.Excluded)
		}
	}
	return agentResult{text: strings.TrimSpace(b.String()), said: operatorCount(len(open), "route"), ok: true}
}

// lookup finds the route a request will reach, by the router's own Find, so a
// static route and a parameter one are never confused. methods lists the
// methods the path takes when this one is not among them.
func (s *Server) lookupRoute(method, path string) (route *apiRoute, methods []string) {
	src := s.deployOpenAPI.Load()
	if src == nil || !assistant.CanonicalPath(path) {
		return nil, nil
	}
	cat := s.apiCatalogue()
	find := func(m, path string) *apiRoute {
		pattern := src.root.Find(chi.NewRouteContext(), m, "/api/v1"+path)
		if pattern == "" {
			return nil
		}
		return cat.byKey[m+" "+strings.TrimPrefix(pattern, "/api/v1")]
	}
	// A list is mounted at its area's path with a slash, which the router serves
	// without one too.
	alternate := strings.TrimSuffix(path, "/")
	if alternate == path {
		alternate = path + "/"
	}
	for _, p := range []string{path, alternate} {
		if route = find(method, p); route != nil {
			return route, nil
		}
	}
	for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		if m != method && (find(m, path) != nil || find(m, alternate) != nil) {
			methods = append(methods, m)
		}
	}
	return nil, methods
}

// agentReadingPosts are routes that take a POST because what they read is a
// document — a filter, a statement to classify, an address to test — and change
// nothing, so a person's "Ask before changes" lets them run.
var agentReadingPosts = map[string]bool{
	"POST /databases/test":                          true,
	"POST /databases/{id}/classify":                 true,
	"POST /databases/{id}/rows/sql":                 true,
	"POST /databases/{id}/orm":                      true,
	"POST /databases/{id}/access/preview":           true,
	"POST /databases/{id}/imports/inspect":          true,
	"POST /databases/{id}/explain/index-candidates": true,
	"POST /databases/{id}/mongo/find":               true,
	"POST /databases/{id}/mongo/count":              true,
	"POST /databases/{id}/mongo/document":           true,
	"POST /databases/{id}/mongo/explain":            true,
	"POST /databases/{id}/mongo/aggregate/preview":  true,
	"POST /databases/{id}/mongo/schema":             true,
	"POST /databases/{id}/mongo/validation/check":   true,
	"POST /databases/{id}/mongo/command/classify":   true,
	"POST /databases/{id}/redis/classify":           true,
	"POST /databases/{id}/seed/plan":                true,
	"POST /docker/containers/preview":               true,
	"POST /docker/stacks/{name}/validate":           true,
	"POST /git/patch/check":                         true,
	"POST /network/dns/lookup":                      true,
	"POST /network/dns/tls-check":                   true,
	"POST /network/dns/dnssec-chain":                true,
	"POST /network/dns/verification-plan":           true,
	"POST /network/dns/hosts/preview":               true,
	"POST /network/gateway/preview":                 true,
	"POST /network/protection/sessions/preview":     true,
}

// agentReachingReads are reads that are sent somewhere: the route answers by
// sending what the request names — a name to look up, an address to connect to,
// a document to resolve — to a machine of the request's choosing. Nothing on the
// server changes, which is why each is a read, but a name looked up is a message
// to whoever answers for it and a connection carries what it is told to, so this
// is the way out for whatever a chat has read: text the model was shown can ask
// it to put a row or a file into a name. In "Ask before changes" they wait for
// the person as a change does; full access runs them unasked like the rest. A
// test given the request's query says when only some requests of a route do.
//
// A read is not here when it reaches only where the server was already pointed:
// a saved database connection, a deployment's own domain or repository, GitHub,
// the registry an image came from, a resolver or hub the operator configured.
// A route that takes a host, a domain or an address from its caller and reads
// by reaching it goes here, as its capability comment says it goes behind
// system.admin.
var agentReachingReads = map[string]func(query url.Values) bool{
	"GET /certificates/check":     nil,
	"GET /certificates/scan":      nil,
	"GET /certificates/scan/deep": nil,
	"GET /certificates/dns":       nil,
	"GET /remote-desktop/probe":   nil,
	// Asked about a name of the caller's; a name suggested for a deployment is
	// under the server's own domain.
	"GET /deploy/hostname": func(query url.Values) bool { return query.Get("hostname") != "" },
	"POST /databases/test": nil,
	// The source it reads is another server's address, as a test's is.
	"POST /databases/{id}/imports/inspect": nil,
	"POST /network/dns/lookup":             nil,
	"POST /network/dns/dnssec-chain":       nil,
	// Compose resolves what a file includes, from a registry or a repository
	// the file names.
	"POST /docker/stacks/{name}/validate": nil,
}

// agentReaches reports whether a read is one of agentReachingReads.
func agentReaches(method, pattern string, query url.Values) bool {
	when, listed := agentReachingReads[method+" "+strings.TrimSuffix(pattern, "/")]
	return listed && (when == nil || when(query))
}

// clickHouseReaching is a ClickHouse table function that reads from another
// machine, or runs a program: a SELECT from one sends its arguments wherever
// they say. The statement's classifier reads such a SELECT as a read (it changes
// nothing in the database), so it is held here. The name may be quoted, and a
// comment may stand between it and its bracket; a column of the same name, or
// the word in a string, costs an Allow.
var clickHouseReaching = regexp.MustCompile("(?is)\\b(url|urlCluster|remote|remoteSecure|cluster|clusterAllReplicas|s3|s3Cluster|gcs|oss|" +
	"cosn|azureBlobStorage|azureBlobStorageCluster|hdfs|hdfsCluster|hive|mysql|postgresql|mongodb|redis|sqlite|jdbc|odbc|file|" +
	"fileCluster|executable|iceberg\\w*|deltaLake\\w*|hudi\\w*|paimon\\w*|ytsaurus|arrowFlight)[\"`]?" +
	"(\\s|/\\*.*?\\*/|--[^\n]*|#[^\n]*)*\\(")

// apiRequestKind is what a request does, as the person's mode reads it. A SQL
// statement is read for what it does: one that only reads runs the way any read
// does, which is how a database is asked about without a press of Allow each time.
func (s *Server) apiRequestKind(ctx context.Context, route *apiRoute, path string, body json.RawMessage) string {
	key := route.Method + " " + strings.TrimSuffix(route.Pattern, "/")
	switch {
	case route.Destructive || route.Method == http.MethodDelete:
		return agentKindDestructive
	case route.Method == http.MethodGet || agentReadingPosts[key]:
		return agentKindRead
	case key == "POST /databases/{id}/query":
		if s.agentSQLReads(ctx, path, body) {
			return agentKindRead
		}
	case key == "POST /databases/{id}/explain":
		var explain struct {
			Analyze bool `json:"analyze"`
		}
		if json.Unmarshal(body, &explain) == nil && !explain.Analyze {
			return agentKindRead
		}
	}
	return agentKindChange
}

// agentSQLReads reports whether a query route's statement only reads, by the
// classifier the route itself holds a statement to (dbx.SingleStatementFor).
func (s *Server) agentSQLReads(ctx context.Context, path string, body json.RawMessage) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 {
		return false
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return false
	}
	var query struct {
		Query string `json:"query"`
	}
	if json.Unmarshal(body, &query) != nil || strings.TrimSpace(query.Query) == "" {
		return false
	}
	var driver string
	err = s.Store.DB.QueryRowContext(ctx, `SELECT driver FROM db_connections WHERE id = ?`, id).Scan(&driver)
	if err != nil {
		return false
	}
	if dbx.Driver(driver) == dbx.DriverClickHouse && clickHouseReaching.MatchString(query.Query) {
		return false
	}
	statement, err := dbx.SingleStatementFor(dbx.Driver(driver), query.Query)
	return err == nil && statement.Risk.Level == "read" && !statement.Risk.Destructive
}

// agentServerPages are the dashboard's pages open_page offers in a conversation
// about the whole server, by the key it names.
var agentServerPages = map[string]agentPage{
	"overview":        {Label: "Overview", Path: "/", Absolute: true},
	"deployments":     {Label: "Deployments", Path: "/deploy", Absolute: true},
	"databases":       {Label: "Databases", Path: "/databases", Absolute: true},
	"backups":         {Label: "Backups", Path: "/backups", Absolute: true},
	"docker":          {Label: "Docker", Path: "/docker", Absolute: true},
	"containers":      {Label: "Containers", Path: "/docker/containers", Absolute: true},
	"images":          {Label: "Images", Path: "/docker/images", Absolute: true},
	"volumes":         {Label: "Volumes", Path: "/docker/volumes", Absolute: true},
	"networks":        {Label: "Docker networks", Path: "/docker/networks", Absolute: true},
	"stacks":          {Label: "Stacks", Path: "/docker/stacks", Absolute: true},
	"files":           {Label: "Files", Path: "/files", Absolute: true},
	"terminal":        {Label: "Terminal", Path: "/terminal", Absolute: true},
	"logs":            {Label: "Logs", Path: "/logs", Absolute: true},
	"metrics":         {Label: "Metrics", Path: "/metrics", Absolute: true},
	"alerts":          {Label: "Alerts", Path: "/metrics/alerts", Absolute: true},
	"processes":       {Label: "Processes", Path: "/processes", Absolute: true},
	"services":        {Label: "Services", Path: "/processes/services", Absolute: true},
	"scheduled_tasks": {Label: "Scheduled tasks", Path: "/processes/scheduled", Absolute: true},
	"packages":        {Label: "Packages", Path: "/packages", Absolute: true},
	"proxy":           {Label: "Proxy", Path: "/proxy", Absolute: true},
	"sites":           {Label: "Sites", Path: "/proxy/sites", Absolute: true},
	"certificates":    {Label: "Certificates", Path: "/proxy/certificates", Absolute: true},
	"network":         {Label: "Network", Path: "/network", Absolute: true},
	"firewall":        {Label: "Firewall", Path: "/network/firewall", Absolute: true},
	"security":        {Label: "Security", Path: "/security", Absolute: true},
	"system_users":    {Label: "System users", Path: "/system-users", Absolute: true},
	"dashboard_users": {Label: "Users", Path: "/account/users", Absolute: true},
	"audit":           {Label: "Audit log", Path: "/audit", Absolute: true},
	"api_keys":        {Label: "API keys", Path: "/account/keys", Absolute: true},
	"sessions":        {Label: "Sessions", Path: "/account/sessions", Absolute: true},
	"assistant":       {Label: "Assistant", Path: "/assistant", Absolute: true},
	"settings":        {Label: "Dashboard settings", Path: "/dashboard", Absolute: true},
	"map":             {Label: "Server map", Path: "/map", Absolute: true},
	"remote_desktop":  {Label: "Remote Desktop", Path: "/remote-desktop", Absolute: true},
	"git":             {Label: "Git", Path: "/git", Absolute: true},
}

// agentAPIPages say where a change through api_request is shown, by the start of
// its path: the first that matches wins, so the narrower come first. {n} is the
// path's nth segment, which a page's address repeats.
var agentAPIPages = []struct {
	prefix, label, href string
}{
	{"/databases/", "Database", "/databases/{1}"},
	{"/databases", "Databases", "/databases"},
	{"/backups/", "Backup job", "/backups/{1}"},
	{"/backups", "Backups", "/backups"},
	{"/docker/containers/", "Container", "/docker/containers/{2}"},
	{"/docker/stacks/", "Stack", "/docker/stacks/{2}"},
	{"/docker/volumes/", "Volume", "/docker/volumes/{2}"},
	{"/docker/images", "Images", "/docker/images"},
	{"/docker/networks", "Docker networks", "/docker/networks"},
	{"/docker", "Docker", "/docker"},
	{"/proxy/sites/", "Site", "/proxy/sites/{2}"},
	{"/proxy", "Proxy", "/proxy"},
	{"/certificates", "Certificates", "/proxy/certificates"},
	{"/files", "Files", "/files"},
	{"/firewall", "Firewall", "/network/firewall"},
	{"/network", "Network", "/network"},
	{"/system-users", "System users", "/system-users"},
	{"/dashboard-users", "Users", "/account/users"},
	{"/tokens", "API keys", "/account/keys"},
	{"/packages", "Packages", "/packages"},
	{"/systemd/daemon-reload", "Services", "/processes/services"},
	{"/systemd/", "Service", "/processes/services/{1}"},
	{"/systemd", "Services", "/processes/services"},
	{"/cron", "Scheduled tasks", "/processes/scheduled"},
	{"/pm2", "PM2", "/processes/pm2"},
	{"/processes/", "Process", "/processes/{1}"},
	{"/processes", "Processes", "/processes"},
	{"/git", "Git", "/git"},
	{"/boards/", "Drawing", "/excalidraw/{1}"},
	{"/remote-desktop", "Remote Desktop", "/remote-desktop"},
	{"/fail2ban", "Intrusion", "/security/intrusion"},
	{"/ssh", "SSH", "/security/ssh"},
	{"/security", "Security", "/security"},
	{"/system/alerts", "Alerts", "/metrics/alerts"},
	{"/terminal", "Terminal", "/terminal"},
	{"/map", "Server map", "/map"},
	{"/dashboard", "Dashboard settings", "/dashboard"},
	{"/account", "Account", "/account"},
}

// agentAPIChange is the page a change through api_request is shown on, if the
// dashboard has one.
func agentAPIChange(route *apiRoute, path string) *agentChange {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if strings.HasPrefix(route.Pattern, "/deploy/{id}") && len(segments) > 1 {
		page, _ := agentPageOf(route.Pattern)
		href := "/deploy/" + url.PathEscape(segments[1]) + page.Path
		if page.Absolute {
			href = page.Path
		}
		if page.Section != "" {
			href += "#" + page.Section
		}
		return &agentChange{Label: page.Label, Href: href}
	}
	for _, line := range agentAPIPages {
		if !strings.HasPrefix(path, line.prefix) {
			continue
		}
		href := line.href
		for i, segment := range segments {
			href = strings.ReplaceAll(href, "{"+strconv.Itoa(i)+"}", url.PathEscape(segment))
		}
		if strings.Contains(href, "{") {
			continue
		}
		return &agentChange{Label: line.label, Href: href}
	}
	return nil
}

// agentDeployments are the deployments the server's prompt names.
func (s *Server) agentDeployments(ctx context.Context) []assistant.ServerDeployment {
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT p.id, p.name, COALESCE(e.name, ''), COALESCE(r.state, '')
		  FROM deploy_projects p
		  LEFT JOIN deploy_environments e ON e.project_id = p.id AND e.kind = 'production'
		  LEFT JOIN deploy_runs r ON r.id = (SELECT id FROM deploy_runs WHERE environment_id = e.id ORDER BY id DESC LIMIT 1)
		 ORDER BY p.name LIMIT 200`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []assistant.ServerDeployment{}
	for rows.Next() {
		var d assistant.ServerDeployment
		var state sql.NullString
		if rows.Scan(&d.ID, &d.Name, &d.Environment, &state) != nil {
			return out
		}
		if state.String != "" {
			d.State = "last run " + state.String
		}
		out = append(out, d)
	}
	return out
}
