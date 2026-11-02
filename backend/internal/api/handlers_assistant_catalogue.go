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

// What the dashboard's assistant may call by name: a deployment's operations,
// read off the router and the deployment API's description, so they cannot drift
// from the routes. It is every route under /deploy the person could make — the
// assistant makes it as them, so their role is what decides — but for what no
// request of the assistant's makes (agentExclusion): its own routes, a run's
// diagnosis, which asks the model, a webhook, and the live streams. The rest of
// the dashboard is api_request's, by method and path (handlers_assistant_routes.go).

// agentScopeKind is what a conversation is about.
type agentScopeKind int

const (
	agentScopeDeployment agentScopeKind = iota
	agentScopeDraft
	agentScopeServer
)

// word is a scope as a saved chat names it.
func (k agentScopeKind) word() string {
	switch k {
	case agentScopeDraft:
		return "draft"
	case agentScopeServer:
		return "server"
	}
	return "deployment"
}

// agentOperation is one operation the assistant may call.
type agentOperation struct {
	mcp.Operation
	// Page is where what it changes is shown.
	Page agentPage
	// Scope is the conversations it is offered to: a deployment's routes to a
	// deployment's, a draft's to a draft's, and the rest under /deploy to both.
	Scope agentScopeKind
	Both  bool
}

// Kind is what the operation does, as a person's mode decides whether it runs: a
// read, a change, or one that stops, removes or replaces something.
func (op agentOperation) Kind() string {
	switch {
	case op.Grant == mcp.GrantRead:
		return agentKindRead
	case op.Grant == mcp.GrantDestructive || op.Tool.Annotations.DestructiveHint:
		return agentKindDestructive
	}
	return agentKindChange
}

// offered reports whether a conversation of this scope is offered it.
func (op agentOperation) offered(scope agentScopeKind) bool {
	return op.Both && scope != agentScopeServer || op.Scope == scope
}

const (
	agentKindFree        = "free"
	agentKindRead        = "read"
	agentKindChange      = "change"
	agentKindDestructive = "destructive"
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

// agentScopeOf says which conversations are offered a route: a deployment's
// routes to its conversation, a draft's to its, and every other route under
// /deploy — the lists, credentials, templates, new drafts — to both.
func agentScopeOf(pattern string) (agentScopeKind, bool) {
	switch {
	case strings.HasPrefix(pattern, "/deploy/drafts/{draft}"):
		return agentScopeDraft, false
	case strings.HasPrefix(pattern, "/deploy/{id}"):
		return agentScopeDeployment, false
	}
	return agentScopeDeployment, true
}

func buildAgentCatalogue(root chi.Routes, doc *openapiDocument) *agentCatalogue {
	cat := &agentCatalogue{byName: map[string]*agentOperation{}}
	type walked struct {
		method, pattern string
		handler         string
		middlewares     []func(http.Handler) http.Handler
	}
	var routes []walked
	_ = chi.Walk(root, func(method, pattern string, handler http.Handler, middlewares ...func(http.Handler) http.Handler) error {
		if strings.HasPrefix(pattern, "/api/v1/deploy/") {
			routes = append(routes, walked{method, strings.TrimPrefix(pattern, "/api/v1"), handlerName(handler), middlewares})
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
		if agentExclusion(rt.method, rt.pattern, rt.handler) != "" {
			continue
		}
		item, _ := paths[rt.pattern].(map[string]any)
		op, _ := item[strings.ToLower(rt.method)].(map[string]any)
		if op == nil {
			continue
		}
		if responses, _ := op["responses"].(map[string]any); responses["101"] != nil {
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
		scope, both := agentScopeOf(rt.pattern)
		cat.ops = append(cat.ops, agentOperation{Operation: built, Page: page, Scope: scope, Both: both})
	}
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

// lines lists a conversation's operations for its prompt.
func (c *agentCatalogue) lines(scope agentScopeKind) []assistant.OperationLine {
	out := []assistant.OperationLine{}
	for _, op := range c.ops {
		if !op.offered(scope) {
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
	// What the whole install's deployments share, each on a page of its own.
	{"/deploy/shared-variables", agentPage{Label: "Shared variables", Path: "/deploy/variables", Absolute: true}},
	{"/deploy/credentials", agentPage{Label: "Credentials", Path: "/deploy/credentials", Absolute: true}},
	{"/deploy/secret-stores", agentPage{Label: "Secret stores", Path: "/deploy/secret-stores", Absolute: true}},
	{"/deploy/notifications", agentPage{Label: "Notifications", Path: "/deploy/notifications", Absolute: true}},
	{"/deploy/log-drains", agentPage{Label: "Log drains", Path: "/deploy/drains", Absolute: true}},
	{"/deploy/templates", agentPage{Label: "Templates", Path: "/deploy/templates", Absolute: true}},
	{"/deploy/drafts", agentPage{Label: "New deployment", Path: "/deploy/new", Absolute: true}},
	{"/deploy/blueprints", agentPage{Label: "New deployment", Path: "/deploy/new", Absolute: true}},
	{"/deploy/game", agentPage{Label: "New deployment", Path: "/deploy/new", Absolute: true}},
	{"/deploy/import", agentPage{Label: "Deployments", Path: "/deploy", Absolute: true}},
	{"/deploy/github-app", agentPage{Label: "Deployments", Path: "/deploy", Absolute: true}},
	{"/variables", agentPage{Label: "Variables", Path: "/settings/variables"}},
	{"/shared-variables", agentPage{Label: "Variables", Path: "/settings/variables"}},
	{"/dns-record", agentPage{Label: "Domains", Path: "/settings/domains"}},
	{"/domains", agentPage{Label: "Domains", Path: "/settings/domains"}},
	{"/maintenance", agentPage{Label: "Domains", Path: "/settings/domains", Section: "maintenance"}},
	{"/edge-rules", agentPage{Label: "Domains", Path: "/settings/domains"}},
	{"/schedules", agentPage{Label: "Automation", Path: "/settings/automation", Section: "schedules"}},
	{"/triggers", agentPage{Label: "Automation", Path: "/settings/automation", Section: "webhooks"}},
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
	// Whatever else a deployment's route changes is its overview's, and a route
	// under /deploy with no deployment is the list's.
	{"/deploy/{id}", agentPage{Label: "Overview", Path: ""}},
	{"/deploy/", agentPage{Label: "Deployments", Path: "/deploy", Absolute: true}},
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
