package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/mcp"
)

// The MCP endpoint driven over the real router: the allowlist, the MCP key's
// authentication, the rate limit, the audit middleware and the handlers, with
// SQLite files for databases so that every tool runs against an engine.

type mcpBench struct {
	t      *testing.T
	s      *Server
	h      http.Handler
	shop   int64
	notes  int64
	vault  int64
	cache  int64
	admin  *auth.User
	viewer *auth.User
}

func newMCPBench(t *testing.T) *mcpBench {
	t.Helper()
	s := testServer(t)
	b := &mcpBench{t: t, s: s, h: s.Routes()}
	b.shop = b.sqlite("shop", false,
		`CREATE TABLE customers (id INTEGER PRIMARY KEY, name TEXT NOT NULL, email TEXT UNIQUE)`,
		`CREATE TABLE orders (id INTEGER PRIMARY KEY, customer_id INTEGER REFERENCES customers(id), total REAL)`,
		`INSERT INTO customers VALUES (1, 'Ann', 'ann@example.com'), (2, 'Bo', 'bo@example.com')`,
		`INSERT INTO orders VALUES (1, 1, 9.5), (2, 2, 20), (3, 1, 4.25)`,
	)
	b.notes = b.sqlite("notes", false, `CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT)`)
	b.vault = b.sqlite("vault", true, `CREATE TABLE secrets (id INTEGER PRIMARY KEY, value TEXT)`)
	sealed, err := s.Sealer.Seal("redis://127.0.0.1:1/0")
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Store.DB.Exec(`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES('cache', 'redis', ?, 0)`, sealed)
	if err != nil {
		t.Fatal(err)
	}
	b.cache, _ = res.LastInsertId()
	if b.admin, err = s.Auth.CreateUser(t.Context(), "ion", "Correct-Horse-Battery-9", auth.RoleAdmin, false); err != nil {
		t.Fatal(err)
	}
	if b.viewer, err = s.Auth.CreateUser(t.Context(), "viewer", "Correct-Horse-Battery-9", auth.RoleReadOnly, false); err != nil {
		t.Fatal(err)
	}
	return b
}

// sqlite saves a SQLite connection and runs its seed through the dashboard's
// own pool. A protected one is seeded before it is marked.
func (b *mcpBench) sqlite(name string, protected bool, seed ...string) int64 {
	b.t.Helper()
	sealed, err := b.s.Sealer.Seal(filepath.Join(b.s.Cfg.FileRoots[0], name+".db"))
	if err != nil {
		b.t.Fatal(err)
	}
	res, err := b.s.Store.DB.Exec(`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES(?, 'sqlite', ?, 0)`, name, sealed)
	if err != nil {
		b.t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	pool, _, err := b.s.dbPool(context.Background(), id)
	if err != nil {
		b.t.Fatal(err)
	}
	for _, stmt := range seed {
		if _, err := pool.Exec(stmt); err != nil {
			b.t.Fatalf("seed %s: %v", name, err)
		}
	}
	if protected {
		b.protect(id, true)
	}
	return id
}

func (b *mcpBench) protect(id int64, on bool) {
	b.t.Helper()
	if _, err := b.s.Store.DB.Exec(`UPDATE db_connections SET read_only = ? WHERE id = ?`, on, id); err != nil {
		b.t.Fatal(err)
	}
}

func (b *mcpBench) key(owner *auth.User, grants ...auth.TokenConnection) string {
	b.t.Helper()
	_, secret, err := b.s.Auth.CreateMCPToken(b.t.Context(), owner, "claude-desktop", 0, grants)
	if err != nil {
		b.t.Fatal(err)
	}
	return secret
}

func read(id int64) auth.TokenConnection  { return auth.TokenConnection{ConnectionID: id, Access: auth.AccessRead} }
func write(id int64) auth.TokenConnection { return auth.TokenConnection{ConnectionID: id, Access: auth.AccessWrite} }

func (b *mcpBench) post(key, body string, headers map[string]string) *httptest.ResponseRecorder {
	b.t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp", strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:5555"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	b.h.ServeHTTP(w, req)
	return w
}

type rpcAnswer struct {
	ID     json.RawMessage `json:"id"`
	Result map[string]any  `json:"result"`
	Error  *struct {
		Code    int            `json:"code"`
		Message string         `json:"message"`
		Data    map[string]any `json:"data"`
	} `json:"error"`
}

func (b *mcpBench) rpc(w *httptest.ResponseRecorder) rpcAnswer {
	b.t.Helper()
	var out rpcAnswer
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		b.t.Fatalf("not a JSON-RPC answer (%d): %v\n%s", w.Code, err, w.Body.String())
	}
	return out
}

var legacy = map[string]string{"MCP-Protocol-Version": "2025-11-25"}

// call is a legacy tools/call, answered with the tool's own result.
func (b *mcpBench) call(key, tool string, args any) (map[string]any, string) {
	b.t.Helper()
	raw, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args},
	})
	w := b.post(key, string(raw), legacy)
	answer := b.rpc(w)
	if w.Code != http.StatusOK || answer.Error != nil {
		b.t.Fatalf("%s: %d %s", tool, w.Code, w.Body.String())
	}
	content, _ := answer.Result["content"].([]any)
	text := ""
	if len(content) == 1 {
		text, _ = content[0].(map[string]any)["text"].(string)
	}
	return answer.Result, text
}

// ok is a call that answered, decoded from the text the model reads.
func (b *mcpBench) ok(key, tool string, args any) map[string]any {
	b.t.Helper()
	result, text := b.call(key, tool, args)
	if result["isError"] != false {
		b.t.Fatalf("%s refused: %s", tool, text)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		b.t.Fatalf("%s answered text that is not JSON: %s", tool, text)
	}
	return decoded
}

// refused is a call answered as an error the model reads, with its text.
func (b *mcpBench) refused(key, tool string, args any) string {
	b.t.Helper()
	result, text := b.call(key, tool, args)
	if result["isError"] != true {
		b.t.Fatalf("%s was not refused: %s", tool, text)
	}
	return text
}

type auditRow struct {
	Target, Detail, Actor, Username string
	Success                         bool
}

func (b *mcpBench) lastAudit(action string) auditRow {
	b.t.Helper()
	var row auditRow
	err := b.s.Store.DB.QueryRow(`SELECT target, detail, actor, username, success FROM audit_log WHERE action = ? ORDER BY id DESC LIMIT 1`, action).
		Scan(&row.Target, &row.Detail, &row.Actor, &row.Username, &row.Success)
	if err != nil {
		b.t.Fatalf("no audit entry for %s: %v", action, err)
	}
	return row
}

func (b *mcpBench) audits(prefix string) int {
	var n int
	if err := b.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action LIKE ?`, prefix+"%").Scan(&n); err != nil {
		b.t.Fatal(err)
	}
	return n
}

// An MCP key opens the MCP endpoint and nothing else, and nothing else opens
// the endpoint: not a key for the API, not a browser's session.
func TestTheMCPEndpointTakesAnMCPKeyAndNothingElse(t *testing.T) {
	b := newMCPBench(t)
	key := b.key(b.admin, read(b.shop))
	ping := `{"jsonrpc":"2.0","id":1,"method":"ping"}`

	if w := b.post(key, ping, nil); w.Code != http.StatusOK {
		t.Fatalf("an MCP key on the endpoint: %d %s", w.Code, w.Body.String())
	}
	if w := b.post("", ping, nil); w.Code != http.StatusUnauthorized || !strings.HasPrefix(w.Header().Get("WWW-Authenticate"), "Bearer") {
		t.Errorf("no key: %d %q", w.Code, w.Header().Get("WWW-Authenticate"))
	}
	if w := b.post("vpsd_not-a-key", ping, nil); w.Code != http.StatusUnauthorized ||
		!strings.Contains(w.Header().Get("WWW-Authenticate"), `error="invalid_token"`) {
		t.Errorf("a wrong key: %d %q", w.Code, w.Header().Get("WWW-Authenticate"))
	}
	_, api, err := b.s.Auth.CreateAPIToken(t.Context(), b.admin, "github-actions", auth.RoleAdmin, 0)
	if err != nil {
		t.Fatal(err)
	}
	if w := b.post(api, ping, nil); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "token_scope") {
		t.Errorf("a key for the API on the endpoint: %d %s", w.Code, w.Body.String())
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp", strings.NewReader(ping))
	req.RemoteAddr = "127.0.0.1:5555"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", signIn(t, b.s))
	req.Header.Set("X-JD-CSRF", "1")
	w := httptest.NewRecorder()
	b.h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("a browser session on the endpoint: %d %s", w.Code, w.Body.String())
	}

	tokens, _ := b.s.Auth.ListAPITokens(t.Context(), b.admin.ID, false)
	for _, tok := range tokens {
		if tok.Scope == auth.ScopeMCP {
			if err := b.s.Auth.RevokeAPIToken(t.Context(), tok.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if w := b.post(key, ping, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("a revoked MCP key: %d", w.Code)
	}
}

// Invariant 4 from the other side: walking every route of the real router
// with an MCP key reaches none of them. A route added tomorrow is covered by
// the same walk.
func TestAnMCPKeyOpensNoOtherRoute(t *testing.T) {
	b := newMCPBench(t)
	key := b.key(b.admin, write(b.shop))
	for _, rt := range apiRoutes(t, b.h) {
		if rt.pattern == "/api/v1/mcp" {
			continue
		}
		req := httptest.NewRequest(rt.method, rt.path, strings.NewReader("{}"))
		req.RemoteAddr = "127.0.0.1:5555"
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		b.h.ServeHTTP(w, req)
		if w.Code < 400 || (w.Code == http.StatusForbidden && !strings.Contains(w.Body.String(), "token_scope") &&
			!strings.Contains(w.Body.String(), "network_denied")) {
			t.Errorf("%s %s answered an MCP key with %d: %s", rt.method, rt.pattern, w.Code, strings.TrimSpace(w.Body.String()))
		}
	}
}

// What a client of either era sends, and what the specification says it is
// answered with.
func TestTheMCPEndpointSpeaksBothEras(t *testing.T) {
	b := newMCPBench(t)
	key := b.key(b.admin, read(b.shop))

	w := b.post(key, `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18",
		"capabilities":{},"clientInfo":{"name":"claude-code","version":"2.4.0"}}}`, nil)
	init := b.rpc(w)
	if w.Code != http.StatusOK || init.Result["protocolVersion"] != "2025-06-18" || w.Header().Get("Mcp-Session-Id") != "" {
		t.Fatalf("initialize: %d %s", w.Code, w.Body.String())
	}
	if tools, _ := init.Result["capabilities"].(map[string]any)["tools"].(map[string]any); tools == nil {
		t.Errorf("initialize declares no tools: %+v", init.Result)
	}
	if connect := b.lastAudit("database.mcp.connect"); connect.Target != "claude-code" || connect.Actor != "token" ||
		!strings.Contains(connect.Detail, `"protocolVersion":"2025-06-18"`) {
		t.Errorf("the connection was audited as %+v", connect)
	}
	if w := b.post(key, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, legacy); w.Code != http.StatusAccepted || w.Body.Len() != 0 {
		t.Errorf("a notification: %d %q", w.Code, w.Body.String())
	}

	listed := b.rpc(b.post(key, `{"jsonrpc":"2.0","id":"list","method":"tools/list"}`, legacy))
	if string(listed.ID) != `"list"` || listed.Result["resultType"] != nil {
		t.Errorf("a legacy list: %+v", listed)
	}
	if names := toolNames(listed.Result); names != "list_connections list_tables describe_schema sample_rows explain run_query" {
		t.Errorf("a read key is offered %s", names)
	}

	for name, c := range map[string]struct {
		body    string
		headers map[string]string
		status  int
		code    int
	}{
		"a batch":               {`[{"jsonrpc":"2.0","id":1,"method":"ping"}]`, legacy, 400, mcp.CodeInvalidRequest},
		"not JSON":              {`{"jsonrpc":`, legacy, 400, mcp.CodeParseError},
		"an unknown method":     {`{"jsonrpc":"2.0","id":1,"method":"resources/list"}`, legacy, 200, mcp.CodeMethodNotFound},
		"an unknown tool":       {`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"drop_everything"}}`, legacy, 200, mcp.CodeInvalidParams},
		"an unsupported header": {`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, map[string]string{"MCP-Protocol-Version": "2024-01-01"}, 400, mcp.CodeUnsupportedVersion},
	} {
		w := b.post(key, c.body, c.headers)
		answer := b.rpc(w)
		if w.Code != c.status || answer.Error == nil || answer.Error.Code != c.code {
			t.Errorf("%s: %d %s, want %d %d", name, w.Code, w.Body.String(), c.status, c.code)
		}
	}

	modern := func(method, params string, headers map[string]string) *httptest.ResponseRecorder {
		body := `{"jsonrpc":"2.0","id":9,"method":"` + method + `","params":{` + params + `"_meta":{
			"io.modelcontextprotocol/protocolVersion":"2026-07-28",
			"io.modelcontextprotocol/clientInfo":{"name":"claude-code","version":"3.0.0"},
			"io.modelcontextprotocol/clientCapabilities":{}}}}`
		return b.post(key, body, headers)
	}
	h := map[string]string{"MCP-Protocol-Version": "2026-07-28", "Mcp-Method": "server/discover"}
	discover := b.rpc(modern("server/discover", "", h))
	if discover.Result["resultType"] != "complete" || discover.Result["cacheScope"] != "private" || discover.Result["supportedVersions"] == nil {
		t.Errorf("server/discover: %+v", discover)
	}
	h = map[string]string{"MCP-Protocol-Version": "2026-07-28", "Mcp-Method": "tools/call", "Mcp-Name": "list_connections"}
	called := b.rpc(modern("tools/call", `"name":"list_connections","arguments":{},`, h))
	if called.Result["resultType"] != "complete" || called.Result["isError"] != false {
		t.Errorf("a modern tools/call: %+v", called)
	}
	h["Mcp-Name"] = "run_query"
	if w := modern("tools/call", `"name":"list_connections","arguments":{},`, h); w.Code != 400 || b.rpc(w).Error.Code != mcp.CodeHeaderMismatch {
		t.Errorf("a header that disagrees with the body: %d %s", w.Code, w.Body.String())
	}
	h = map[string]string{"MCP-Protocol-Version": "2026-07-28", "Mcp-Method": "initialize"}
	if w := modern("initialize", "", h); w.Code != http.StatusNotFound || b.rpc(w).Error.Code != mcp.CodeMethodNotFound {
		t.Errorf("initialize in the modern era: %d %s", w.Code, w.Body.String())
	}

	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		req := httptest.NewRequest(method, "/api/v1/mcp", nil)
		req.RemoteAddr = "127.0.0.1:5555"
		req.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		b.h.ServeHTTP(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: %d", method, w.Code)
		}
	}
	// A page on another site could rebind its name to this dashboard.
	if w := b.post(key, `{"jsonrpc":"2.0","id":1,"method":"ping"}`, map[string]string{"Origin": "https://evil.example"}); w.Code != http.StatusForbidden {
		t.Errorf("a foreign Origin: %d", w.Code)
	}
	if w := b.post(key, `{"jsonrpc":"2.0","id":1,"method":"ping"}`, map[string]string{"Origin": "http://example.com"}); w.Code != http.StatusOK {
		t.Errorf("this dashboard's own Origin: %d %s", w.Code, w.Body.String())
	}
}

func toolNames(result map[string]any) string {
	tools, _ := result["tools"].([]any)
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.(map[string]any)["name"].(string))
	}
	return strings.Join(names, " ")
}

func TestTheMCPToolsReadWhatTheKeyWasGranted(t *testing.T) {
	b := newMCPBench(t)
	key := b.key(b.admin, read(b.shop), read(b.vault), read(b.cache))

	listed := b.ok(key, mcp.ToolListConnections, map[string]any{})
	open, _ := listed["connections"].([]any)
	if len(open) != 1 || open[0].(map[string]any)["connection"] != "shop" || open[0].(map[string]any)["access"] != "read" {
		t.Fatalf("list_connections: %+v", listed)
	}
	if unavailable := fmt.Sprint(listed["unavailable"]); !strings.Contains(unavailable, "vault") || !strings.Contains(unavailable, "protected") ||
		!strings.Contains(unavailable, "cache") || strings.Contains(fmt.Sprint(listed), "notes") {
		t.Errorf("unavailable: %s", unavailable)
	}

	tables := b.ok(key, mcp.ToolListTables, map[string]any{"connection": "shop"})
	if got := fmt.Sprint(tables["tables"]); !strings.Contains(got, "customers") || !strings.Contains(got, "orders") {
		t.Errorf("list_tables: %+v", tables)
	}
	described := b.ok(key, mcp.ToolDescribeSchema, map[string]any{"connection": "SHOP", "tables": []string{"orders", "invoices"}})
	if got := fmt.Sprint(described); !strings.Contains(got, "customer_id") || !strings.Contains(got, "customers(id)") ||
		!strings.Contains(got, "invoices") || strings.Contains(got, "Ann") {
		t.Errorf("describe_schema: %s", got)
	}
	whole := b.ok(key, mcp.ToolDescribeSchema, map[string]any{"connection": "shop"})
	if got := fmt.Sprint(whole["tables"]); !strings.Contains(got, "email") || !strings.Contains(got, "primaryKey:true") {
		t.Errorf("describe_schema of the whole schema: %s", got)
	}

	sample := b.ok(key, mcp.ToolSampleRows, map[string]any{"connection": "shop", "table": "customers", "limit": 1})
	if sample["rowCount"] != float64(1) || !strings.Contains(fmt.Sprint(sample["rows"]), "Ann") {
		t.Errorf("sample_rows: %+v", sample)
	}
	query := b.ok(key, mcp.ToolRunQuery, map[string]any{"connection": "shop", "sql": "SELECT name FROM customers WHERE email = 'bo@example.com'"})
	if fmt.Sprint(query["rows"]) != "[[Bo]]" {
		t.Errorf("run_query: %+v", query)
	}
	plan := b.ok(key, mcp.ToolExplain, map[string]any{"connection": "shop", "sql": "SELECT * FROM orders WHERE customer_id = 1"})
	if text, _ := plan["plan"].(string); text == "" {
		t.Errorf("explain: %+v", plan)
	}

	if text := b.refused(key, mcp.ToolRunQuery, map[string]any{"connection": "shop", "sql": "INSERT INTO orders VALUES (4, 2, 1)"}); !strings.Contains(text, "only read shop") {
		t.Errorf("a write through run_query: %s", text)
	}
	if text := b.refused(key, mcp.ToolRunWrite, map[string]any{"connection": "shop", "sql": "INSERT INTO orders VALUES (4, 2, 1)"}); !strings.Contains(text, "may only read") {
		t.Errorf("run_write on a read grant: %s", text)
	}
	if text := b.refused(key, mcp.ToolSampleRows, map[string]any{"connection": "notes", "table": "notes"}); !strings.Contains(text, "No database named") {
		t.Errorf("a database the key was not given: %s", text)
	}
	if text := b.refused(key, mcp.ToolRunQuery, map[string]any{"connection": "shop", "sql": "SELECT 1; SELECT 2"}); !strings.Contains(text, "exactly one") {
		t.Errorf("two statements: %s", text)
	}
	if text := b.refused(key, mcp.ToolRunQuery, map[string]any{"connection": "shop", "query": "SELECT 1"}); !strings.Contains(text, `no argument "query"`) {
		t.Errorf("a misspelt argument: %s", text)
	}
	var orders int
	pool, _, _ := b.s.dbPool(context.Background(), b.shop)
	if err := pool.QueryRow(`SELECT COUNT(*) FROM orders`).Scan(&orders); err != nil || orders != 3 {
		t.Fatalf("a read-only key changed the database: %d orders (%v)", orders, err)
	}
}

// A protected database is refused for everything, reads included, and the
// refusal follows the protection the moment it is set.
func TestAProtectedDatabaseIsRefusedToEveryMCPTool(t *testing.T) {
	b := newMCPBench(t)
	key := b.key(b.admin, write(b.shop))
	b.ok(key, mcp.ToolListTables, map[string]any{"connection": "shop"})
	b.protect(b.shop, true)
	for tool, args := range map[string]map[string]any{
		mcp.ToolListTables:     {"connection": "shop"},
		mcp.ToolDescribeSchema: {"connection": "shop"},
		mcp.ToolSampleRows:     {"connection": "shop", "table": "customers"},
		mcp.ToolExplain:        {"connection": "shop", "sql": "SELECT 1"},
		mcp.ToolRunQuery:       {"connection": "shop", "sql": "SELECT 1"},
		mcp.ToolRunWrite:       {"connection": "shop", "sql": "INSERT INTO orders VALUES (9, 1, 1)"},
	} {
		if text := b.refused(key, tool, args); !strings.Contains(text, "protected") {
			t.Errorf("%s on a protected database: %s", tool, text)
		}
		if entry := b.lastAudit("database.mcp." + tool); entry.Success || !strings.Contains(entry.Detail, `"reason":"protected"`) {
			t.Errorf("%s was audited as %+v", tool, entry)
		}
	}
	if listed := b.ok(key, mcp.ToolListConnections, map[string]any{}); len(listed["connections"].([]any)) != 0 {
		t.Errorf("a protected database is still listed as open: %+v", listed)
	}
}

// A change through MCP is judged as the SQL editor judges it for the key's
// owner, and only where the key may change the database at all.
func TestAChangeThroughMCPIsHeldToTheSQLEditorsRules(t *testing.T) {
	b := newMCPBench(t)
	limited, err := b.s.Auth.CreateUser(t.Context(), "maria", "Correct-Horse-Battery-9", auth.RoleLimited, false)
	if err != nil {
		t.Fatal(err)
	}
	limitedKey := b.key(limited, write(b.shop))
	inserted := b.ok(limitedKey, mcp.ToolRunWrite, map[string]any{"connection": "shop", "sql": "INSERT INTO orders VALUES (4, 2, 15)"})
	if inserted["rowsAffected"] != float64(1) {
		t.Errorf("an insert: %+v", inserted)
	}
	if text := b.refused(limitedKey, mcp.ToolRunWrite, map[string]any{"connection": "shop", "sql": "DELETE FROM orders WHERE id = 4"}); !strings.Contains(text, "destructive") {
		t.Errorf("a delete through a limited owner's key: %s", text)
	}
	adminKey := b.key(b.admin, write(b.shop))
	if deleted := b.ok(adminKey, mcp.ToolRunWrite, map[string]any{"connection": "shop", "sql": "DELETE FROM orders WHERE id = 4"}); deleted["rowsAffected"] != float64(1) {
		t.Errorf("a delete through an administrator's key: %+v", deleted)
	}
	if entry := b.lastAudit("database.mcp.run_write"); !entry.Success || !strings.Contains(entry.Detail, `"destructive":true`) ||
		entry.Target != "shop" || entry.Username != "ion" {
		t.Errorf("the delete was audited as %+v", entry)
	}

	viewerKey := b.key(b.viewer, read(b.shop))
	listed := b.rpc(b.post(viewerKey, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, legacy))
	if names := toolNames(listed.Result); strings.Contains(names, "run_") {
		t.Errorf("a read-only owner's key is offered %s", names)
	}
	if text := b.refused(viewerKey, mcp.ToolRunQuery, map[string]any{"connection": "shop", "sql": "SELECT 1"}); !strings.Contains(text, "read-only role") {
		t.Errorf("a statement through a read-only owner's key: %s", text)
	}
	b.ok(viewerKey, mcp.ToolSampleRows, map[string]any{"connection": "shop", "table": "orders"})

	// Demoting the owner demotes the key at its next call.
	if _, err := b.s.Store.DB.Exec(`UPDATE users SET role = 'readonly' WHERE id = ?`, limited.ID); err != nil {
		t.Fatal(err)
	}
	if text := b.refused(limitedKey, mcp.ToolRunWrite, map[string]any{"connection": "shop", "sql": "INSERT INTO orders VALUES (5, 2, 1)"}); !strings.Contains(text, "read-only role") {
		t.Errorf("a demoted owner's key: %s", text)
	}
}

// Every tool call is in the trail, as the shape of what ran: a literal is
// somebody's e-mail address.
func TestEveryMCPCallIsAuditedAsAShape(t *testing.T) {
	b := newMCPBench(t)
	key := b.key(b.admin, write(b.shop))
	before := b.audits("database.mcp.")
	b.ok(key, mcp.ToolRunQuery, map[string]any{"connection": "shop", "sql": "SELECT id FROM customers WHERE email = 'ann@example.com' -- Ann's"})
	entry := b.lastAudit("database.mcp.run_query")
	if !entry.Success || entry.Target != "shop" || entry.Actor != "token" || entry.Username != "ion" ||
		!strings.Contains(entry.Detail, `SELECT id FROM customers WHERE email = ?`) ||
		strings.Contains(entry.Detail, "ann@") || strings.Contains(entry.Detail, "Ann") ||
		!strings.Contains(entry.Detail, `"key":"claude-desktop"`) {
		t.Errorf("run_query was audited as %+v", entry)
	}
	b.refused(key, mcp.ToolRunWrite, map[string]any{"connection": "shop", "sql": "INSERT INTO nowhere VALUES ('bo@example.com')"})
	failed := b.lastAudit("database.mcp.run_write")
	if failed.Success || !strings.Contains(failed.Detail, `"outcome":"failed"`) || strings.Contains(failed.Detail, "bo@") {
		t.Errorf("a failed write was audited as %+v", failed)
	}
	b.ok(key, mcp.ToolListConnections, map[string]any{})
	if after := b.audits("database.mcp."); after != before+3 {
		t.Errorf("%d audit entries for three calls", after-before)
	}
	// The protocol's own traffic is not a call.
	b.post(key, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, legacy)
	b.post(key, `{"jsonrpc":"2.0","id":2,"method":"ping"}`, legacy)
	if after := b.audits(""); after != b.audits("") || b.audits("database.mcp.") != before+3 {
		t.Errorf("listing tools or a ping was audited")
	}
}

func TestAnMCPKeyHasFourCallsInFlight(t *testing.T) {
	var st mcpState
	for range mcpCallsPerKey {
		if !st.enter(7) {
			t.Fatal("a call under the bound was refused")
		}
	}
	if st.enter(7) {
		t.Fatal("a fifth call was let in")
	}
	if !st.enter(8) {
		t.Fatal("another key shares the bound")
	}
	st.leave(7)
	if !st.enter(7) {
		t.Fatal("a call that finished did not make room")
	}
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if st.enter(9) {
				st.leave(9)
			}
		}()
	}
	wg.Wait()
	if st.busy[9] != 0 {
		t.Fatalf("calls left counted: %d", st.busy[9])
	}
}

func TestMCPKeysAreMadeForDatabasesTheEndpointReaches(t *testing.T) {
	b := newMCPBench(t)
	c := &client{t: t, h: b.h, cookie: signIn(t, b.s)}
	body := func(grants ...auth.TokenConnection) string {
		raw, _ := json.Marshal(map[string]any{"name": "cursor", "ttlDays": 30, "connections": grants})
		return string(raw)
	}
	w := c.do(http.MethodPost, "/api/v1/tokens/mcp", body(read(b.shop), write(b.notes)), nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	created := decodeJSONBody[struct {
		Token  auth.APIToken `json:"token"`
		Secret string        `json:"secret"`
	}](t, w)
	if created.Token.Scope != auth.ScopeMCP || len(created.Token.Connections) != 2 || !strings.HasPrefix(created.Secret, "vpsd_") {
		t.Fatalf("created %+v", created)
	}
	if entry := b.lastAudit("dashboard.token.create"); !strings.Contains(entry.Detail, `"scope":"mcp"`) ||
		!strings.Contains(entry.Detail, `"connection":"notes"`) || strings.Contains(entry.Detail, created.Secret) {
		t.Errorf("creation was audited as %+v", entry)
	}

	for name, c2 := range map[string]struct {
		body   string
		status int
		code   string
	}{
		"a protected database": {body(read(b.vault)), http.StatusConflict, "connection_read_only"},
		"a Redis server":       {body(read(b.cache)), http.StatusBadRequest, "bad_request"},
		"no database":          {body(), http.StatusBadRequest, "bad_request"},
		"one that is not there": {body(read(999)), http.StatusBadRequest, "bad_request"},
	} {
		if w := c.do(http.MethodPost, "/api/v1/tokens/mcp", c2.body, nil); w.Code != c2.status || !strings.Contains(w.Body.String(), c2.code) {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}

	path := fmt.Sprintf("/api/v1/tokens/%d/connections", created.Token.ID)
	repoint, _ := json.Marshal(map[string]any{"connections": []auth.TokenConnection{write(b.shop)}})
	if w := c.do(http.MethodPut, path, string(repoint), nil); w.Code != http.StatusOK {
		t.Fatalf("repoint: %d %s", w.Code, w.Body.String())
	}
	if text := fmt.Sprint(b.ok(created.Secret, mcp.ToolListConnections, map[string]any{})); !strings.Contains(text, "access:write") || strings.Contains(text, "notes") {
		t.Errorf("after repointing the key lists %s", text)
	}
	if entry := b.lastAudit("dashboard.token.connections"); entry.Target != "cursor" || !strings.Contains(entry.Detail, `"access":"write"`) {
		t.Errorf("repointing was audited as %+v", entry)
	}

	other := &client{t: t, h: b.h, cookie: signInAs(t, b.s, "maria", auth.RoleAdmin)}
	if w := other.do(http.MethodPut, path, string(repoint), nil); w.Code != http.StatusNotFound {
		t.Errorf("another account repointed the key: %d", w.Code)
	}
	api, _, err := b.s.Auth.CreateAPIToken(t.Context(), mustUser(t, b.s, "tester"), "github-actions", auth.RoleLimited, 0)
	if err != nil {
		t.Fatal(err)
	}
	if w := c.do(http.MethodPut, fmt.Sprintf("/api/v1/tokens/%d/connections", api.ID), string(repoint), nil); w.Code != http.StatusConflict {
		t.Errorf("a key for the API was given databases: %d %s", w.Code, w.Body.String())
	}

	viewer := &client{t: t, h: b.h, cookie: signInAs(t, b.s, "reader", auth.RoleReadOnly)}
	if w := viewer.do(http.MethodPost, "/api/v1/tokens/mcp", body(write(b.shop)), nil); w.Code != http.StatusBadRequest {
		t.Errorf("a read-only account made a key that changes: %d %s", w.Code, w.Body.String())
	}
	if w := viewer.do(http.MethodPost, "/api/v1/tokens/mcp", body(read(b.shop)), nil); w.Code != http.StatusCreated {
		t.Errorf("a read-only account could not make a key that reads: %d %s", w.Code, w.Body.String())
	}

	listed := decodeJSONBody[[]auth.APIToken](t, c.do(http.MethodGet, "/api/v1/tokens/", "", nil))
	for _, tok := range listed {
		if tok.ID == created.Token.ID && (tok.Scope != auth.ScopeMCP || len(tok.Connections) != 1 || tok.Connections[0].Access != auth.AccessWrite) {
			t.Errorf("the key list says %+v", tok)
		}
	}
	_ = dbx.DriverSQLite
}

func mustUser(t *testing.T, s *Server, username string) *auth.User {
	t.Helper()
	u, err := s.Auth.UserByUsername(t.Context(), username)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
