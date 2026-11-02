package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/Wayy01/Just-Dashboard/backend/internal/assistant"
	"github.com/Wayy01/Just-Dashboard/backend/internal/mcp"
	"github.com/Wayy01/Just-Dashboard/backend/internal/openapigen"
	"github.com/go-chi/chi/v5"
)

// What the dashboard's assistant may call: the operations it is offered, read off
// the router and the deployment API's description, so they cannot drift from the
// routes. It starts from everything a deploy key may do (deployKeyRoutes), with
// that key's every rule — variables write-only, nothing that reaches past a
// deployment's own container to the host, no saved credential sent somewhere new.
// assistantRoutes opens a few routes a key may not, because a person is at the
// dashboard asking, and agentClosedRoutes closes some a key may.
//
// What stays closed whichever the person's role, and the words the model is told:
// a secret's value, saved credentials, secret stores and notification channels
// (their secrets would go to somebody else's model); the routes that take a typed
// confirmation (a person's by their nature); merging, commenting and previews (they
// speak as the dashboard on GitHub, or run a pull request's code); and whatever
// reaches the host.

// assistantRoutes are the routes the assistant may make and a deploy key may not.
// Each is a person's own act in the dashboard; it is a key's lack of a person behind
// it that closed them.
var assistantRoutes = map[string]string{
	// Automation. A schedule runs later with nobody there, so what it may run is held
	// to deploying and restarting this deployment (refuseAssistantSchedule): the step
	// that runs a command names its container, and could name any on the server.
	"POST /deploy/{id}/environments/{env}/schedules":              whyAutomations,
	"PUT /deploy/{id}/environments/{env}/schedules/{schedule}":    whyAutomations,
	"DELETE /deploy/{id}/environments/{env}/schedules/{schedule}": whyAutomations,
	"POST /deploy/{id}/alerts":                                    whyAutomations,
	"PUT /deploy/{id}/alerts/{alert}":                             whyAutomations,
	"DELETE /deploy/{id}/alerts/{alert}":                          whyAutomations,
	"POST /deploy/{id}/alerts/{alert}/test":                       whyAutomations,
	// The terms a release is kept warm on and whether its images are scanned.
	"PUT /deploy/{id}/environments/{env}/rollback-policy": whyRollbackTerms,
	"PUT /deploy/{id}/environments/{env}/vulnerabilities": whyScanTerms,
	// A domain's record, with the DNS credentials saved under Certificates, which
	// the route uses and never answers with.
	"GET /deploy/{id}/domains/{domain}/dns-record":  whyDNSRecord,
	"POST /deploy/{id}/domains/{domain}/dns-record": whyDNSRecord,
	// Backups, which are the whole server's and not this deployment's: reading them
	// and starting one, each waiting for the person in every mode (agentKindServer).
	// A job's destination holds credentials and its sources reach anything, so making
	// or changing one stays on the Backups page.
	"GET /backups":           "backups are not deployment routes",
	"GET /backups/resources": "backups are not deployment routes",
	"GET /backups/{id}":      "backups are not deployment routes",
	"GET /backups/{id}/runs": "backups are not deployment routes",
	"POST /backups/{id}/run": "backups are not deployment routes",
}

// assistantOpens reports whether assistantRoutes opens a route, by its method and
// its chi pattern under /api/v1.
func assistantOpens(method, pattern string) bool {
	_, open := assistantRoutes[method+" "+strings.TrimSuffix(pattern, "/")]
	return open
}

// agentClosedRoutes are routes a key may make and the assistant does not offer.
var agentClosedRoutes = map[string]string{
	"POST /deploy/{id}/runs/{run}/diagnosis": "it spends the model budget asking the model, and the assistant can read the run itself",
	// These answer with a new secret, which would go to the model, and which the
	// person could then never read, since it is masked from the page as well.
	"POST /deploy/{id}/environments/{env}/triggers/{trigger}/rotate-secret": "its answer is a secret",
	"POST /deploy/{id}/environments/{env}/triggers":                         "its answer is a secret",
	// A second deployment is not what a conversation about one is for.
	"POST /deploy/{id}/duplicate": "a conversation is about one deployment",
	// A person presses Deploy, and the page beside the assistant holds the draft.
	"POST /deploy/drafts":                                            "a conversation is about the draft it began on",
	"POST /deploy/drafts/{draft}/commit":                             "creating the deployment is the person's press of Deploy",
	"DELETE /deploy/drafts/{draft}":                                  "the page discards a setup",
	"GET /deploy/drafts":                                             "a conversation is about the draft it began on",
	"POST /deploy/{id}/environments/{env}/variables/{name}/generate": "a person is shown the value once, and a key never",
}

// agentScopeKind is what a conversation is about.
type agentScopeKind int

const (
	agentScopeDeployment agentScopeKind = iota
	agentScopeDraft
)

// agentOperation is one operation the assistant may call.
type agentOperation struct {
	mcp.Operation
	// Page is where what it changes is shown.
	Page agentPage
	// Scope is the conversations it is offered to.
	Scope agentScopeKind
	// Server is an operation on what the whole server shares rather than the
	// deployment the conversation is about, which waits for the person in every mode.
	Server bool
}

// Kind is what the operation does, as a person's mode decides whether it runs: a
// read, a change, or one that stops, removes or replaces something.
func (op agentOperation) Kind() string {
	switch {
	case op.Server:
		return agentKindServer
	case op.Grant == mcp.GrantRead:
		return agentKindRead
	case op.Grant == mcp.GrantDestructive || op.Tool.Annotations.DestructiveHint:
		return agentKindDestructive
	}
	return agentKindChange
}

const (
	agentKindFree        = "free"
	agentKindRead        = "read"
	agentKindChange      = "change"
	agentKindDestructive = "destructive"
	agentKindServer      = "server"
)

type agentCatalogue struct {
	ops    []agentOperation
	byName map[string]*agentOperation
	err    error
}

type agentCatalogueSource struct {
	once sync.Once
	cat  *agentCatalogue
}

func (s *Server) agentCatalogue() *agentCatalogue {
	src := s.deployOpenAPI.Load()
	if src == nil {
		return &agentCatalogue{byName: map[string]*agentOperation{}, err: fmt.Errorf("the routes were not built")}
	}
	src.agent.once.Do(func() { src.agent.cat = buildAgentCatalogue(src.root, src.document()) })
	return src.agent.cat
}

// agentScopeOf says which conversations are offered a route, if any. A deployment's
// conversation reaches that deployment's routes and a few reads of
// what they are chosen from; a draft's, that draft and those reads.
func agentScopeOf(pattern string, method string) (agentScopeKind, bool) {
	catalogue := method == http.MethodGet && (strings.HasPrefix(pattern, "/deploy/templates") ||
		strings.HasPrefix(pattern, "/deploy/blueprints") || pattern == "/deploy/hostname")
	switch {
	case strings.HasPrefix(pattern, "/deploy/drafts/{draft}") || catalogue:
		return agentScopeDraft, true
	case strings.HasPrefix(pattern, "/deploy/{id}"):
		return agentScopeDeployment, true
	}
	return 0, false
}

func buildAgentCatalogue(root chi.Routes, doc *openapiDocument) *agentCatalogue {
	cat := &agentCatalogue{byName: map[string]*agentOperation{}}
	type walked struct {
		method, pattern string
		middlewares     []func(http.Handler) http.Handler
	}
	var routes []walked
	_ = chi.Walk(root, func(method, pattern string, _ http.Handler, middlewares ...func(http.Handler) http.Handler) error {
		if strings.HasPrefix(pattern, "/api/v1/deploy/") {
			routes = append(routes, walked{method, strings.TrimPrefix(pattern, "/api/v1"), middlewares})
		}
		return nil
	})
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].pattern != routes[j].pattern {
			return routes[i].pattern < routes[j].pattern
		}
		return routes[i].method < routes[j].method
	})
	var document map[string]any
	if err := json.Unmarshal(doc.JSON, &document); err != nil {
		cat.err = err
		return cat
	}
	paths, _ := document["paths"].(map[string]any)
	for _, rt := range routes {
		key := rt.method + " " + rt.pattern
		trimmed := rt.method + " " + strings.TrimSuffix(rt.pattern, "/")
		if _, closed := agentClosedRoutes[trimmed]; closed {
			continue
		}
		rule, listed := ruleForDeployKey(rt.method, rt.pattern)
		open := listed && rule.session == "" && rule.deny == ""
		if !open && !assistantOpens(rt.method, rt.pattern) {
			continue
		}
		scope, reachable := agentScopeOf(rt.pattern, rt.method)
		if !reachable {
			continue
		}
		item, _ := paths[rt.pattern].(map[string]any)
		op, _ := item[strings.ToLower(rt.method)].(map[string]any)
		if op == nil || mcpUnanswerable(op) != "" {
			continue
		}
		grant := mcp.GrantRead
		switch {
		case isDestructive(rt.middlewares):
			grant = mcp.GrantDestructive
		case rt.method != http.MethodGet:
			grant = mcp.GrantWrite
		}
		id, _ := op["operationId"].(string)
		name := openapigen.Snake(id)
		built, err := mcp.NewOperation(document, name, rt.method, rt.pattern, item, op, grant,
			rt.method == http.MethodDelete || mcpDestructiveHints[key], agentParameterHint)
		if err != nil {
			cat.err = err
			return cat
		}
		page, _ := agentPageOf(rt.pattern)
		cat.ops = append(cat.ops, agentOperation{Operation: built, Page: page, Scope: scope})
	}
	cat.ops = append(cat.ops, agentBackupOperations()...)
	for i := range cat.ops {
		name := cat.ops[i].Tool.Name
		if _, taken := cat.byName[name]; taken {
			cat.err = fmt.Errorf("two operations would be named %s", name)
			return cat
		}
		cat.byName[name] = &cat.ops[i]
	}
	return cat
}

// agentParameterHint says where the model finds a path parameter's value. The
// deployment's own are filled in for it.
func agentParameterHint(pattern, name string) string {
	switch name {
	case "id":
		if strings.HasPrefix(pattern, "/deploy/{id}") {
			return "Filled in for you."
		}
	case "env":
		return "Filled in with this conversation's environment; deployment_environments lists the others."
	case "draft":
		return "Filled in for you."
	case "run":
		return "recent_runs gives it as id (not the run's number)."
	case "release":
		return "The release's id, as the environment's releases list gives it."
	case "schedule":
		return "As the environment's schedules list gives it."
	case "trigger":
		return "As the environment's triggers list gives it."
	}
	return ""
}

func objectSchema(required []string, properties map[string]any) map[string]any {
	schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// agentBackupOperations are backups as operations, by hand: they are not in the
// deployment API's description. What they answer is the Backups page's own read,
// which never carries a destination's credentials, though it does name every job's
// folders and destination, which is why even a read waits for the person.
func agentBackupOperations() []agentOperation {
	page := agentPage{Label: "Backups", Path: "/backups", Absolute: true}
	idParam := map[string]any{"type": "integer", "description": "The job's id, as backup_jobs gives it."}
	reading := func(name, title, description, pattern string, path []mcp.Parameter, properties map[string]any) agentOperation {
		required := []string{}
		for _, p := range path {
			required = append(required, p.Name)
		}
		return agentOperation{Page: page, Scope: agentScopeDeployment, Server: true, Operation: mcp.Operation{
			ID: name, Method: http.MethodGet, Pattern: pattern, Path: path, Grant: mcp.GrantRead,
			Tool: mcp.Tool{Name: name, Title: title, Description: description + " It only reads.", InputSchema: objectSchema(required, properties),
				Annotations: mcp.Annotations{Title: title, ReadOnlyHint: true, IdempotentHint: true}},
		}}
	}
	changing := func(name, title, description, pattern string, properties map[string]any, body bool) agentOperation {
		return agentOperation{Page: page, Scope: agentScopeDeployment, Server: true, Operation: mcp.Operation{
			ID: name, Method: http.MethodPost, Pattern: pattern, Path: []mcp.Parameter{{Name: "id", Type: "integer"}}, Grant: mcp.GrantWrite, Body: body, BodyRequired: body,
			Tool: mcp.Tool{Name: name, Title: title, Description: description, InputSchema: objectSchema([]string{"id"}, properties),
				Annotations: mcp.Annotations{Title: title}},
		}}
	}
	return []agentOperation{
		reading("backup_jobs", "List the server's backup jobs", "List the backup jobs: each one's name, what it backs up (folders, and the databases it dumps), where it goes, its schedule, whether it is enabled and when it last ran.", "/backups/", nil, map[string]any{}),
		reading("backup_resources", "List what can be backed up", "List what a backup job can cover on this server: the databases, volumes and folders.", "/backups/resources", nil, map[string]any{}),
		reading("backup_job", "Read one backup job", "Read one backup job.", "/backups/{id}", []mcp.Parameter{{Name: "id", Type: "integer"}}, map[string]any{"id": idParam}),
		reading("backup_runs", "Read a backup job's runs", "Read a backup job's recent runs: when each ran, whether it succeeded and how large it was.", "/backups/{id}/runs", []mcp.Parameter{{Name: "id", Type: "integer"}}, map[string]any{"id": idParam}),
		changing("backup_run", "Run a backup now", "Start a backup job now. It runs on its own, however long it takes: read backup_runs for how it went.", "/backups/{id}/run", map[string]any{"id": idParam}, false),
	}
}

// lines lists a conversation's operations for its prompt.
func (c *agentCatalogue) lines(scope agentScopeKind) []assistant.OperationLine {
	out := []assistant.OperationLine{}
	for _, op := range c.ops {
		if op.Scope != scope {
			continue
		}
		out = append(out, assistant.OperationLine{Name: op.Tool.Name, Title: op.Tool.Title, Kind: op.Kind()})
	}
	return out
}

// agentPage is where a change is shown: the page and, where it has one, the
// section on it. Path is under the project unless it is Absolute.
type agentPage struct {
	Label    string
	Path     string
	Section  string
	Absolute bool
}

// agentPages says where each operation's change is shown, by a piece of its route.
// The first that matches wins, so the narrower come first.
// TestEveryAgentChangeHasAPage fails for a write with no line here.
var agentPages = []struct {
	match string
	page  agentPage
}{
	{"/variables", agentPage{Label: "Variables", Path: "/settings/variables"}},
	{"/shared-variables", agentPage{Label: "Variables", Path: "/settings/variables"}},
	{"/dns-record", agentPage{Label: "Domains", Path: "/settings/domains", Section: "dns"}},
	{"/domains", agentPage{Label: "Domains", Path: "/settings/domains"}},
	{"/maintenance", agentPage{Label: "Overview", Path: ""}},
	{"/edge-rules", agentPage{Label: "Domains", Path: "/settings/domains", Section: "edge-rules"}},
	{"/schedules", agentPage{Label: "Automation", Path: "/settings/automation", Section: "schedules"}},
	{"/triggers", agentPage{Label: "Automation", Path: "/settings/automation", Section: "triggers"}},
	{"/git-policy", agentPage{Label: "General", Path: "/settings/general", Section: "automatic-deployment"}},
	{"/source", agentPage{Label: "General", Path: "/settings/general", Section: "source"}},
	{"/rollback-policy", agentPage{Label: "Automation", Path: "/settings/automation", Section: "rollback-watch"}},
	{"/alerts", agentPage{Label: "Automation", Path: "/settings/automation", Section: "alerts"}},
	{"/vulnerabilities", agentPage{Label: "Runtime", Path: "/runtime"}},
	{"/database-links", agentPage{Label: "Databases & backups", Path: "/settings/databases"}},
	{"/backup", agentPage{Label: "Databases & backups", Path: "/settings/databases"}},
	{"/volumes", agentPage{Label: "Storage", Path: "/settings/storage"}},
	{"/configuration", agentPage{Label: "Runtime", Path: "/settings/runtime"}},
	{"/detect", agentPage{Label: "Build", Path: "/settings/build"}},
	{"/runs", agentPage{Label: "Deployments", Path: "/deployments"}},
	{"/run", agentPage{Label: "Deployments", Path: "/deployments"}},
	{"/rollback", agentPage{Label: "Deployments", Path: "/deployments"}},
	{"/promote", agentPage{Label: "Deployments", Path: "/deployments"}},
	{"/canary", agentPage{Label: "Deployments", Path: "/deployments"}},
	{"/releases", agentPage{Label: "Deployments", Path: "/deployments"}},
	{"/sleep", agentPage{Label: "Runtime", Path: "/runtime"}},
	{"/wake", agentPage{Label: "Runtime", Path: "/runtime"}},
	{"/requests", agentPage{Label: "Logs", Path: "/logs"}},
	{"/logs", agentPage{Label: "Logs", Path: "/logs"}},
	{"/watch-paths", agentPage{Label: "General", Path: "/settings/general", Section: "automatic-deployment"}},
	{"/environments", agentPage{Label: "Overview", Path: ""}},
}

func agentPageOf(pattern string) (agentPage, bool) {
	for _, line := range agentPages {
		if strings.Contains(pattern, line.match) {
			return line.page, true
		}
	}
	return agentPage{Label: "Overview", Path: ""}, false
}

// agentConfigurationPages are the parts of an environment's configuration and
// where each is set, the narrower first, for a change to say where to look.
var agentConfigurationPages = []struct {
	prefix string
	page   agentPage
}{
	{"runtime.port", agentPage{Label: "Runtime", Path: "/settings/runtime", Section: "listen"}},
	{"runtime.hostPort", agentPage{Label: "Runtime", Path: "/settings/runtime", Section: "listen"}},
	{"runtime.ports", agentPage{Label: "Runtime", Path: "/settings/runtime", Section: "listen"}},
	{"runtime.memoryMb", agentPage{Label: "Runtime", Path: "/settings/runtime", Section: "resources"}},
	{"runtime.cpus", agentPage{Label: "Runtime", Path: "/settings/runtime", Section: "resources"}},
	{"runtime.strategy", agentPage{Label: "Runtime", Path: "/settings/runtime", Section: "releases"}},
	{"runtime.mounts", agentPage{Label: "Storage", Path: "/settings/storage"}},
	{"runtime", agentPage{Label: "Runtime", Path: "/settings/runtime"}},
	{"build", agentPage{Label: "Build", Path: "/settings/build"}},
	{"variables", agentPage{Label: "Variables", Path: "/settings/variables"}},
	{"domains", agentPage{Label: "Domains", Path: "/settings/domains"}},
	{"checks", agentPage{Label: "Runtime", Path: "/settings/runtime", Section: "health-checks"}},
	{"dependencies", agentPage{Label: "Databases & backups", Path: "/settings/databases"}},
	{"source", agentPage{Label: "General", Path: "/settings/general", Section: "source"}},
}

// agentConfigurationPage is where the part of a configuration at a path is set.
func agentConfigurationPage(path string) (agentPage, bool) {
	for _, line := range agentConfigurationPages {
		if path == line.prefix || strings.HasPrefix(path, line.prefix+".") || strings.HasPrefix(path, line.prefix+"[") {
			return line.page, true
		}
	}
	return agentPage{}, false
}

// agentOpenablePages are the pages open_page may take a person to.
var agentOpenablePages = map[string]agentPage{
	"overview":         {Label: "Overview", Path: ""},
	"deployments":      {Label: "Deployments", Path: "/deployments"},
	"logs":             {Label: "Logs", Path: "/logs"},
	"runtime":          {Label: "Runtime", Path: "/runtime"},
	"console":          {Label: "Console", Path: "/console"},
	"general":          {Label: "General", Path: "/settings/general"},
	"build":            {Label: "Build", Path: "/settings/build"},
	"runtime_settings": {Label: "Runtime settings", Path: "/settings/runtime"},
	"variables":        {Label: "Variables", Path: "/settings/variables"},
	"domains":          {Label: "Domains", Path: "/settings/domains"},
	"storage":          {Label: "Storage", Path: "/settings/storage"},
	"databases":        {Label: "Databases & backups", Path: "/settings/databases"},
	"automation":       {Label: "Automation", Path: "/settings/automation"},
	"danger":           {Label: "Danger zone", Path: "/settings/danger"},
	"backups":          {Label: "Backups", Path: "/backups", Absolute: true},
}
