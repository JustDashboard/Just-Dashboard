package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/mcp"
	"github.com/Wayy01/Just-Dashboard/backend/internal/openapigen"
	"github.com/go-chi/chi/v5"
)

// The MCP tools generated from the deployment API. Every route the deploy key
// table opens to a key that may reach it (keyOpen, keyEvery) is a tool named
// for its operationId, read off the router Routes built and the description
// generated from it, so the tools cannot drift from the routes: a route added
// with a line in deployKeyRoutes is a tool, and a route without one is not.
// A session-only route, and one the table keeps from a deploy key, never is.
//
// A call is made through the dashboard's own router, as a request whose
// principal is the MCP key (httpx.WithMCPDelegate): the allowlist, the rate
// limit, the deploy key gate — the table's line, the key's chosen deployments
// and environments — the route's capability checks, its handler's deploy key
// rules (variables write-only, keyReferenceGuard, keySourceGuard,
// hostAuthority, a narrowed key's drafts) and its own audit entry all apply
// as they do to a deploy key. The key's grant decides what it is offered and
// may call: reads under read, changes under deploy, and the routes behind
// s.destructive under deploy for an owner whose role may take destructive
// actions. Each call is audited as deploy.mcp.<tool> beside the route's own
// entry.

// mcpHandWritten is the deploy key routes a hand-written deployment tool
// already is: the generated tool is left out and the hand-written one kept,
// with its names and its bounds.
var mcpHandWritten = map[string]string{
	"GET /deploy/":                                  mcp.ToolListDeployments,
	"GET /deploy/{id}/runs":                         mcp.ToolListRuns,
	"GET /deploy/{id}/runs/{run}/transcript":        mcp.ToolReadRunLog,
	"GET /deploy/{id}/environments/{env}/releases":  mcp.ToolListReleases,
	"POST /deploy/{id}/run":                         mcp.ToolDeploy,
	"POST /deploy/{id}/environments/{env}/rollback": mcp.ToolRollback,
}

// mcpNotATool is the deploy key routes that are not tools for another
// reason, by route.
var mcpNotATool = map[string]string{
	"GET /deploy/openapi.json": "the description the tools are generated from: it describes the API, not the dashboard, and is a megabyte",
}

// mcpDestructiveHints is the routes a client should confirm before it calls
// them although they are not behind s.destructive: a DELETE always, and these,
// which stop, cancel or replace what was there.
var mcpDestructiveHints = map[string]bool{
	"POST /deploy/{id}/environments/{env}/runs":                             true, // stop and restart, decided from the body
	"POST /deploy/{id}/runs/{run}/cancel":                                   true,
	"POST /deploy/{id}/environments/{env}/canary/abort":                     true,
	"POST /deploy/{id}/environments/{env}/variables/{name}/rotate":          true,
	"POST /deploy/{id}/environments/{env}/triggers/{trigger}/rotate-secret": true,
}

// mcpParameterHint says where a model finds a path parameter's value: the
// tool that lists what the parameter names.
func mcpParameterHint(pattern, name string) string {
	switch name {
	case "id":
		switch {
		case strings.HasPrefix(pattern, "/deploy/blueprints/"):
			return "blueprint_list gives it as id."
		case strings.HasPrefix(pattern, "/deploy/{id}"):
			return "list_deployments gives it as id."
		}
	case "env":
		return "list_deployments gives production's as environmentId; deployment_environments lists every one."
	case "run":
		return "list_runs gives it as id (not the run's number)."
	case "release":
		return "list_releases gives it as release."
	case "draft":
		return "deployment_draft_list and deployment_draft_create give it as id."
	case "trigger":
		return "deployment_triggers lists them."
	case "schedule":
		return "deployment_schedules lists them."
	case "channel":
		return "deployment_notifications lists them."
	case "number":
		return "deployment_pull_requests lists them."
	}
	return ""
}

// MCPGeneratedTools is how many tools the deployment API generates: the
// deploy key routes, less the hand-written tools', the WebSocket streams,
// the answers that are not JSON and the description itself. The MCP
// documentation quotes it (and the total with the 13 hand-written tools), so
// TestEveryDeployKeyRouteIsAnMCPTool fails until a change that adds or
// removes a deploy key route bumps it and the documentation with it.
const MCPGeneratedTools = 97

// mcpSkip is a deploy key route that is not a generated tool, and why.
type mcpSkip struct {
	Route, Reason string
}

// mcpCatalogue is the generated tools of one router, and what was left out.
type mcpCatalogue struct {
	ops     []mcp.Operation
	byName  map[string]*mcp.Operation
	skipped []mcpSkip
	err     error
}

// buildMCPCatalogue reads the generated tools off the router and its
// description.
func buildMCPCatalogue(root chi.Routes, doc *openapiDocument) *mcpCatalogue {
	cat := &mcpCatalogue{byName: map[string]*mcp.Operation{}}
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
	// The description encodes through JSON, so the tools are built from what
	// a client of it reads rather than from the Go maps.
	var document map[string]any
	if err := json.Unmarshal(doc.JSON, &document); err != nil {
		cat.err = err
		return cat
	}
	paths, _ := document["paths"].(map[string]any)
	for _, rt := range routes {
		key := rt.method + " " + rt.pattern
		rule, listed := ruleForDeployKey(rt.method, rt.pattern)
		if !listed || rule.session != "" || rule.deny != "" {
			continue
		}
		item, _ := paths[rt.pattern].(map[string]any)
		op, _ := item[strings.ToLower(rt.method)].(map[string]any)
		if op == nil {
			cat.err = fmt.Errorf("%s is not in the description", key)
			return cat
		}
		if tool, ok := mcpHandWritten[key]; ok {
			cat.skipped = append(cat.skipped, mcpSkip{key, "the hand-written tool " + tool + " is this route"})
			continue
		}
		if reason, ok := mcpNotATool[key]; ok {
			cat.skipped = append(cat.skipped, mcpSkip{key, reason})
			continue
		}
		if reason := mcpUnanswerable(op); reason != "" {
			cat.skipped = append(cat.skipped, mcpSkip{key, reason})
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
		if mcp.Known(name) {
			cat.err = fmt.Errorf("%s would be named %s, which is a hand-written tool", key, name)
			return cat
		}
		if _, taken := cat.byName[name]; taken {
			cat.err = fmt.Errorf("%s would be named %s twice", key, name)
			return cat
		}
		tool, err := mcp.NewOperation(document, name, rt.method, rt.pattern, item, op, grant,
			rt.method == http.MethodDelete || mcpDestructiveHints[key], mcpParameterHint)
		if err != nil {
			cat.err = err
			return cat
		}
		cat.ops = append(cat.ops, tool)
		cat.byName[name] = nil
	}
	// byName points into ops, which no longer moves.
	for i := range cat.ops {
		cat.byName[cat.ops[i].Tool.Name] = &cat.ops[i]
	}
	return cat
}

// mcpUnanswerable is why an operation cannot be a tool, or "": a WebSocket
// stream has no answer to return, and an answer that is not JSON (an image,
// a page, a CSV export, YAML) has a JSON route beside it that is the tool.
func mcpUnanswerable(op map[string]any) string {
	responses, _ := op["responses"].(map[string]any)
	if _, ok := responses["101"]; ok {
		return "a WebSocket stream: there is no answer to return, and its JSON reads are tools"
	}
	for code, raw := range responses {
		if code == "default" {
			continue
		}
		response, _ := raw.(map[string]any)
		content, _ := response["content"].(map[string]any)
		for media := range content {
			if media != "application/json" {
				return "its answer is " + media + ", not JSON"
			}
		}
	}
	return ""
}

// mcpCatalogueSource builds the catalogue once per router, the first time the
// endpoint is asked for its tools, from the description built the same way.
type mcpCatalogueSource struct {
	once sync.Once
	cat  *mcpCatalogue
}

func (s *Server) mcpCatalogue() *mcpCatalogue {
	src := s.deployOpenAPI.Load()
	if src == nil {
		return &mcpCatalogue{byName: map[string]*mcp.Operation{}, err: fmt.Errorf("the routes were not built")}
	}
	src.tools.once.Do(func() { src.tools.cat = buildMCPCatalogue(src.root, src.document()) })
	return src.tools.cat
}

// mcpOperations is the generated tools, or none when they could not be
// built; the hand-written tools are offered either way.
func (s *Server) mcpOperations() []mcp.Operation {
	cat := s.mcpCatalogue()
	if cat.err != nil {
		return nil
	}
	return cat.ops
}

func (s *Server) mcpOperation(name string) (mcp.Operation, bool) {
	cat := s.mcpCatalogue()
	if cat.err != nil {
		return mcp.Operation{}, false
	}
	op, ok := cat.byName[name]
	if !ok {
		return mcp.Operation{}, false
	}
	return *op, true
}

// mcpAnswerBytes bounds what one dispatched route's answer is read to: a tool
// returns at most mcp.ResultBytes of it, and the rest is only counted.
const mcpAnswerBytes = mcp.ResultBytes + 1

// boundedRecorder is the response of a dispatched route: its status, its
// headers, and the first part of its body, with the whole counted.
type boundedRecorder struct {
	header http.Header
	status int
	body   bytes.Buffer
	total  int
}

func (b *boundedRecorder) Header() http.Header { return b.header }

func (b *boundedRecorder) WriteHeader(status int) {
	if b.status == 0 {
		b.status = status
	}
}

func (b *boundedRecorder) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	b.total += len(p)
	if room := mcpAnswerBytes - b.body.Len(); room > 0 {
		if len(p) < room {
			room = len(p)
		}
		b.body.Write(p[:room])
	}
	return len(p), nil
}

// dispatchMCP makes a generated tool's request through the router, as the
// MCP key.
func (s *Server) dispatchMCP(r *http.Request, p *httpx.Principal, method string, req mcp.RouteRequest) (*boundedRecorder, error) {
	src := s.deployOpenAPI.Load()
	if src == nil {
		return nil, fmt.Errorf("the routes were not built")
	}
	handler, ok := src.root.(http.Handler)
	if !ok {
		return nil, fmt.Errorf("the router does not serve requests")
	}
	target := "/api/v1" + req.Path
	if q := req.Query.Encode(); q != "" {
		target += "?" + q
	}
	// The request starts from the router's root as any other does: a fresh
	// route context, the MCP key as its credential, nothing of the MCP
	// request's own audit entry.
	ctx := context.WithValue(httpx.WithMCPDelegate(r.Context(), p), chi.RouteCtxKey, nil)
	var body *bytes.Reader
	if req.Body != nil {
		body = bytes.NewReader(req.Body)
	}
	var inner *http.Request
	var err error
	if body != nil {
		inner, err = http.NewRequestWithContext(ctx, method, target, body)
	} else {
		inner, err = http.NewRequestWithContext(ctx, method, target, nil)
	}
	if err != nil {
		return nil, err
	}
	// The address the MCP request was resolved to, so the allowlist and the
	// trail see the client, not a proxy in front of it.
	inner.RemoteAddr = net.JoinHostPort(httpx.ClientIP(r), "0")
	inner.Header.Set("Accept", "application/json")
	inner.Header.Set("User-Agent", r.UserAgent())
	if req.Body != nil {
		inner.Header.Set("Content-Type", "application/json")
	}
	rec := &boundedRecorder{header: http.Header{}}
	handler.ServeHTTP(rec, inner)
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	return rec, nil
}

// operation is a generated tool's call.
func (c *mcpCall) operation(op mcp.Operation, raw json.RawMessage) map[string]any {
	c.detail["operation"] = op.ID
	c.detail["route"] = op.Method + " " + op.Pattern
	access := mcpDeployAccess(c.p)
	switch {
	case access == auth.DeployNone:
		return c.refuse("not_granted", "This key was not given the deployments. Its owner can give it them under Databases → MCP server.")
	case op.Grant != mcp.GrantRead && access != auth.DeployRun:
		return c.refuse("not_granted", "This key may read the deployments, not change them.")
	case op.Grant != mcp.GrantRead && !c.p.Can(auth.CapServiceControl):
		return c.refuse("forbidden", "The key's owner's role does not change deployments.")
	case op.Grant == mcp.GrantDestructive && !c.p.Can(auth.CapDestructive):
		return c.refuse("forbidden", "The key's owner's role does not take destructive actions.")
	}
	req, err := mcp.DecodeRequest(op, raw)
	if err != nil {
		return c.refuse("arguments", err.Error())
	}
	c.target = clipTarget("/api/v1" + req.Path)
	rec, err := c.s.dispatchMCP(c.r, c.p, op.Method, req)
	if err != nil {
		return c.fail("the route could not be called", nil)
	}
	c.detail["status"] = rec.status
	if rec.status >= http.StatusBadRequest {
		var envelope struct {
			Error *httpx.APIError `json:"error"`
		}
		_ = json.Unmarshal(rec.body.Bytes(), &envelope)
		code, message := "failed", http.StatusText(rec.status)
		if envelope.Error != nil && envelope.Error.Code != "" {
			code, message = envelope.Error.Code, envelope.Error.Message
		}
		if rec.status >= http.StatusInternalServerError {
			c.failed = true
			c.detail["outcome"], c.detail["error"] = "failed", code
			if code == "internal" {
				return mcp.Failure("The dashboard could not answer; try again.")
			}
			return mcp.Failure(message)
		}
		return c.refuse(code, message)
	}
	c.detail["outcome"] = "ok"
	return mcp.OperationResult(rec.status, rec.body.Bytes(), rec.total)
}
