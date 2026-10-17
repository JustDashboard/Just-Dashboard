package api

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/assistant"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// The assistant's routes over HTTP: who reaches them, what is sent to the
// provider and what is not, how an answer streams, how a tool call waits for
// the person, and what the audit trail keeps. The provider is a test server
// answering as Anthropic's or an OpenAI-compatible API would; nothing here
// reaches a model. The database is a real SQLite file whose rows say they
// must never be sent.

const assistantSentinel = "row-value-never-sent"

// fakeProvider answers as Anthropic's Messages API, or as chat completions,
// from a queue of answers the test sets; it records every request.
type fakeProvider struct {
	mu      sync.Mutex
	paths   []string
	bodies  []map[string]any
	raw     []string
	answers []func(w http.ResponseWriter)
}

func (p *fakeProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var decoded map[string]any
	_ = json.Unmarshal(body, &decoded)
	p.mu.Lock()
	p.paths = append(p.paths, r.Method+" "+r.URL.Path)
	p.bodies, p.raw = append(p.bodies, decoded), append(p.raw, string(body))
	var answer func(w http.ResponseWriter)
	if r.Method == http.MethodPost && len(p.answers) > 0 {
		answer, p.answers = p.answers[0], p.answers[1:]
	}
	p.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/models"):
		fmt.Fprint(w, `{"id":"claude-opus-5-5","data":[{"id":"llama3.1:8b"}]}`)
	case answer != nil:
		answer(w)
	default:
		claudeAnswer("SELECT 1;")(w)
	}
}

func (p *fakeProvider) asked() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, path := range p.paths {
		if strings.HasPrefix(path, "POST") {
			n++
		}
	}
	return n
}

func (p *fakeProvider) last() map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.bodies[len(p.bodies)-1]
}

func (p *fakeProvider) queue(answers ...func(w http.ResponseWriter)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.answers = append(p.answers, answers...)
}

func sseEvents(w http.ResponseWriter, events ...map[string]any) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, e := range events {
		data, _ := json.Marshal(e)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e["type"], data)
	}
}

// claudeAnswer is an answer that thinks, writes text, and ends — or, given
// tables, ends asking describe_tables for them.
func claudeAnswer(text string, tables ...string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		events := []map[string]any{
			{"type": "message_start", "message": map[string]any{"model": "claude-opus-5-5", "usage": map[string]int{"input_tokens": 1500, "cache_read_input_tokens": 1200}}},
			{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "thinking", "thinking": ""}},
			{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "signature_delta", "signature": "sig-" + text}},
			{"type": "content_block_start", "index": 1, "content_block": map[string]any{"type": "text", "text": ""}},
			{"type": "content_block_delta", "index": 1, "delta": map[string]any{"type": "text_delta", "text": text}},
		}
		stop := "end_turn"
		if len(tables) > 0 {
			input, _ := json.Marshal(map[string]any{"tables": tables})
			events = append(events,
				map[string]any{"type": "content_block_start", "index": 2, "content_block": map[string]any{"type": "tool_use", "id": "toolu_01", "name": "describe_tables", "input": map[string]any{}}},
				map[string]any{"type": "content_block_delta", "index": 2, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(input)}},
			)
			stop = "tool_use"
		}
		events = append(events,
			map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop}, "usage": map[string]int{"output_tokens": 64}},
			map[string]any{"type": "message_stop"},
		)
		sseEvents(w, events...)
	}
}

type assistantFixture struct {
	s        *Server
	router   http.Handler
	db       *sql.DB
	provider *fakeProvider
	url      string
}

func assistantRouterAs(t *testing.T, role auth.Role) *assistantFixture {
	t.Helper()
	s := testServer(t)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			p := &httpx.Principal{User: &auth.User{ID: 1, Username: "operator"}, Role: role, Kind: "session", IP: "127.0.0.1"}
			next.ServeHTTP(w, req.WithContext(httpx.WithPrincipal(req.Context(), p)))
		})
	})
	r.Use(httpx.AuditMutations(s.Audit))
	s.mountDatabaseRoutes(r)

	path := filepath.Join(s.Cfg.FileRoots[0], "shop.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, stmt := range []string{
		`CREATE TABLE customers (id INTEGER PRIMARY KEY, email TEXT NOT NULL UNIQUE, note TEXT DEFAULT '` + assistantSentinel + `-default')`,
		`CREATE TABLE orders (id INTEGER PRIMARY KEY, customer_id INTEGER NOT NULL REFERENCES customers(id), total NUMERIC, status TEXT NOT NULL)`,
		`INSERT INTO customers(email, note) VALUES ('` + assistantSentinel + `@example.com', 'vip')`,
		`INSERT INTO orders(customer_id, total, status) VALUES (1, 42.5, '` + assistantSentinel + `')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%v\n%s", err, stmt)
		}
	}
	sealed, err := s.Sealer.Seal(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES(?,?,?,0)`,
		"shop", string(dbx.DriverSQLite), sealed); err != nil {
		t.Fatal(err)
	}
	provider := &fakeProvider{}
	server := httptest.NewServer(provider)
	t.Cleanup(server.Close)
	s.assistant.options.AnthropicURL = server.URL
	return &assistantFixture{s: s, router: r, db: db, provider: provider, url: server.URL}
}

// set stores a provider as the settings route would, without probing it.
func (f *assistantFixture) set(t *testing.T, settings assistant.Settings) {
	t.Helper()
	if settings.UpdatedAt.IsZero() {
		settings.UpdatedAt = time.Now()
	}
	plain, _ := json.Marshal(settings)
	sealed, err := f.s.Sealer.Seal(string(plain))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.Store.SetSetting(t.Context(), assistantSettingKey, sealed); err != nil {
		t.Fatal(err)
	}
}

func (f *assistantFixture) claude(t *testing.T) {
	f.set(t, assistant.Settings{Provider: assistant.ProviderAnthropic, Model: "claude-opus-5-5", Key: "sk-ant-test-key"})
}

func (f *assistantFixture) do(method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

// lines reads a streamed answer, one JSON object a line.
func lines(t *testing.T, rec *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/x-ndjson" {
		t.Fatalf("not a stream: %d %s %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
	var out []map[string]any
	scanner := bufio.NewScanner(strings.NewReader(rec.Body.String()))
	for scanner.Scan() {
		var line map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("a line that is not JSON: %q", scanner.Text())
		}
		out = append(out, line)
	}
	return out
}

func ofType(events []map[string]any, kind string) []map[string]any {
	var out []map[string]any
	for _, e := range events {
		if e["type"] == kind {
			out = append(out, e)
		}
	}
	return out
}

func errCode(rec *httptest.ResponseRecorder) string {
	var body struct {
		Error struct {
			Code  string `json:"code"`
			Field string `json:"field"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body.Error.Code
}

func (f *assistantFixture) audit(t *testing.T, action string) (detail string, status int) {
	t.Helper()
	err := f.s.Store.DB.QueryRow(`SELECT detail, status FROM audit_log WHERE action = ? ORDER BY id DESC LIMIT 1`, action).Scan(&detail, &status)
	if err != nil {
		t.Fatalf("no %s entry: %v", action, err)
	}
	return detail, status
}

const askOrders = `{"task":{"kind":"sql","prompt":"count the orders for each customer"}}`

func TestAssistantRoutesAskForTheirCapability(t *testing.T) {
	setup := `{"provider":"anthropic","model":"claude-opus-5-5","key":"sk-ant-test-key"}`
	for _, c := range []struct {
		role                     auth.Role
		configure, ask, readable bool
	}{
		{auth.RoleReadOnly, false, false, true},
		{auth.RoleLimited, false, true, true},
		{auth.RoleAdmin, true, true, true},
	} {
		f := assistantRouterAs(t, c.role)
		if rec := f.do(http.MethodGet, "/databases/assistant", ""); (rec.Code == http.StatusOK) != c.readable {
			t.Errorf("%s: GET /assistant answered %d", c.role, rec.Code)
		}
		for _, rt := range []struct{ method, body string }{{http.MethodPut, setup}, {http.MethodDelete, ""}} {
			rec := f.do(rt.method, "/databases/assistant", rt.body)
			if got := rec.Code != http.StatusForbidden; got != c.configure {
				t.Errorf("%s: %s /assistant passed = %v (%d %s)", c.role, rt.method, got, rec.Code, rec.Body.String())
			}
		}
		f.claude(t)
		rec := f.do(http.MethodPost, "/databases/1/assistant", askOrders)
		if got := rec.Code != http.StatusForbidden; got != c.ask {
			t.Errorf("%s: asking passed = %v (%d)", c.role, got, rec.Code)
		}
	}
}

func TestTheAssistantIsOffUntilItIsSetUp(t *testing.T) {
	f := assistantRouterAs(t, auth.RoleAdmin)
	var view assistant.View
	rec := f.do(http.MethodGet, "/databases/assistant", "")
	_ = json.Unmarshal(rec.Body.Bytes(), &view)
	if view.Configured || len(view.Models) != 3 || view.Models[0].ID != "claude-opus-5-5" {
		t.Errorf("view %+v", view)
	}
	rec = f.do(http.MethodPost, "/databases/1/assistant", askOrders)
	if rec.Code != http.StatusConflict || errCode(rec) != "assistant_off" {
		t.Errorf("asked with no provider: %d %s", rec.Code, rec.Body.String())
	}
	if f.provider.asked() != 0 {
		t.Error("a provider was asked")
	}
	if _, status := f.audit(t, "database.assistant.ask"); status != http.StatusConflict {
		t.Errorf("the refusal was recorded as %d", status)
	}
}

func TestSettingUpTheAssistantChecksTheKeyAndSealsIt(t *testing.T) {
	f := assistantRouterAs(t, auth.RoleAdmin)
	rec := f.do(http.MethodPut, "/databases/assistant", `{"provider":"anthropic","model":"claude-sonnet-5-5","key":"sk-ant-test-key"}`)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "sk-ant-test-key") {
		t.Fatalf("set up: %d %s", rec.Code, rec.Body.String())
	}
	if f.provider.paths[0] != "GET /v1/models/claude-sonnet-5-5" {
		t.Errorf("checked with %v", f.provider.paths)
	}
	raw, _, _ := f.s.Store.Setting(t.Context(), assistantSettingKey)
	if raw == "" || strings.Contains(raw, "sk-ant") {
		t.Fatalf("the key is not sealed: %q", raw)
	}
	opened, err := f.s.Sealer.Open(raw)
	if err != nil || !strings.Contains(opened, "sk-ant-test-key") {
		t.Fatalf("the sealed settings: %v", err)
	}
	rec = f.do(http.MethodGet, "/databases/assistant", "")
	var view assistant.View
	_ = json.Unmarshal(rec.Body.Bytes(), &view)
	if !view.Configured || !view.KeySet || view.ModelLabel != "Claude Sonnet 5.5" || view.UpdatedBy != "operator" || strings.Contains(rec.Body.String(), "sk-ant") {
		t.Errorf("view %s", rec.Body.String())
	}
	detail, status := f.audit(t, "database.assistant.configure")
	if status != http.StatusOK || strings.Contains(detail, "sk-ant") || !strings.Contains(detail, `"model":"claude-sonnet-5-5"`) {
		t.Errorf("audit %d %s", status, detail)
	}

	// A key the provider refuses is said under its field, and nothing is saved.
	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
	}))
	defer refusing.Close()
	f.s.assistant.options.AnthropicURL = refusing.URL
	rec = f.do(http.MethodPut, "/databases/assistant", `{"provider":"anthropic","model":"claude-opus-5-5","key":"sk-ant-wrong"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"field":"key"`) || !strings.Contains(rec.Body.String(), "did not accept the key") {
		t.Errorf("a refused key: %d %s", rec.Code, rec.Body.String())
	}
	if again, _, _ := f.s.Store.Setting(t.Context(), assistantSettingKey); again != raw {
		t.Error("a refused key replaced the saved provider")
	}

	// An address nobody should send a key to in the clear is refused before
	// anything is asked.
	before := len(f.provider.paths)
	rec = f.do(http.MethodPut, "/databases/assistant", `{"provider":"openai","model":"gpt","baseUrl":"http://api.example.com/v1","key":"k"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"field":"baseUrl"`) || len(f.provider.paths) != before {
		t.Errorf("plain HTTP across the internet: %d %s", rec.Code, rec.Body.String())
	}

	rec = f.do(http.MethodDelete, "/databases/assistant", "")
	_ = json.Unmarshal(rec.Body.Bytes(), &view)
	if rec.Code != http.StatusOK || view.Configured {
		t.Errorf("turned off: %d %s", rec.Code, rec.Body.String())
	}
	if _, status := f.audit(t, "database.assistant.remove"); status != http.StatusOK {
		t.Error("turning it off was not recorded")
	}
}

func TestAQuestionIsAnsweredFromTheStructureAlone(t *testing.T) {
	f := assistantRouterAs(t, auth.RoleLimited)
	f.claude(t)
	f.provider.queue(claudeAnswer("```sql\nSELECT customer_id, count(*) FROM orders GROUP BY customer_id;\n```"))
	events := lines(t, f.do(http.MethodPost, "/databases/1/assistant", askOrders))
	start := events[0]
	if start["type"] != "start" || start["model"] != "claude-opus-5-5" || start["modelLabel"] != "Claude Opus 5.5" || start["conversation"] == "" {
		t.Fatalf("start %v", start)
	}
	if context := start["context"].(map[string]any); context["described"] != float64(2) || context["schema"] != "main" {
		t.Errorf("context %v", context)
	}
	if len(ofType(events, "thinking")) != 1 || ofType(events, "text")[0]["text"] == "" {
		t.Errorf("events %v", events)
	}
	done := events[len(events)-1]
	if done["type"] != "done" || done["stop"] != "end" || done["kept"] != true || done["usage"].(map[string]any)["output"] != float64(64) {
		t.Errorf("done %v", done)
	}

	sent := f.provider.raw[0]
	if strings.Contains(sent, assistantSentinel) {
		t.Fatalf("a value from inside the database was sent:\n%s", sent)
	}
	body := f.provider.last()
	system := body["system"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(system, "customer_id INTEGER NOT NULL REFERENCES customers(id)") || !strings.Contains(system, "SQLite") {
		t.Errorf("the system prompt:\n%s", system)
	}
	question := body["messages"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)["text"]
	if question != "count the orders for each customer\n" {
		t.Errorf("question %q", question)
	}

	detail, status := f.audit(t, "database.assistant.ask")
	for _, want := range []string{`"task":"sql"`, `"usage":{"cacheRead":1200,"input":1500,"output":64}`, `"outcome":"answered"`, `"provider":"anthropic"`} {
		if !strings.Contains(detail, want) {
			t.Errorf("audit lacks %s: %s", want, detail)
		}
	}
	if status != http.StatusOK || strings.Contains(detail, "count the orders") || strings.Contains(detail, "SELECT") {
		t.Errorf("the audit entry carries the question: %s", detail)
	}

	// The next question carries the conversation on: the same standing
	// instructions, byte for byte, and the turns before it.
	f.provider.queue(claudeAnswer("Add WHERE status = 'shipped'."))
	lines(t, f.do(http.MethodPost, "/databases/1/assistant",
		fmt.Sprintf(`{"conversation":%q,"task":{"kind":"sql","prompt":"only shipped ones"}}`, start["conversation"])))
	next := f.provider.last()
	if again := next["system"].([]any)[0].(map[string]any)["text"]; again != system {
		t.Error("the system prompt changed between turns")
	}
	messages := next["messages"].([]any)
	if len(messages) != 3 || messages[1].(map[string]any)["role"] != "assistant" {
		t.Fatalf("messages %v", messages)
	}
	if !strings.Contains(f.provider.raw[1], `"signature":"sig-`+"```sql") {
		t.Error("the first answer's thinking block was not sent back")
	}
}

func TestAnAttachedResultIsTheOnlyWayARowIsSent(t *testing.T) {
	f := assistantRouterAs(t, auth.RoleLimited)
	f.claude(t)
	body := `{"task":{"kind":"sql","prompt":"why is this total odd","statement":"SELECT * FROM orders",` +
		`"result":{"columns":["id","status"],"rows":[[1,"` + assistantSentinel + `"]],"total":1}}}`
	lines(t, f.do(http.MethodPost, "/databases/1/assistant", body))
	if !strings.Contains(f.provider.raw[0], "| 1 | "+assistantSentinel+" |") || !strings.Contains(f.provider.raw[0], "SELECT * FROM orders") {
		t.Errorf("the attached result was not sent:\n%s", f.provider.raw[0])
	}
	detail, _ := f.audit(t, "database.assistant.ask")
	if strings.Contains(detail, assistantSentinel) || !strings.Contains(detail, `"resultRows":1`) || !strings.Contains(detail, `"statement":true`) {
		t.Errorf("audit %s", detail)
	}
}

func TestAToolCallWaitsForThePersonToAllowIt(t *testing.T) {
	f := assistantRouterAs(t, auth.RoleLimited)
	f.claude(t)
	f.provider.queue(claudeAnswer("I need the columns of orders.", "ORDERS"))
	events := lines(t, f.do(http.MethodPost, "/databases/1/assistant", askOrders))
	done := events[len(events)-1]
	pending, _ := done["pending"].([]any)
	if done["stop"] != "tool" || len(pending) != 1 || pending[0].(map[string]any)["tables"].([]any)[0] != "ORDERS" {
		t.Fatalf("done %v", done)
	}
	if f.provider.asked() != 1 {
		t.Fatal("the call ran without being allowed")
	}
	conversation := events[0]["conversation"].(string)
	if detail, _ := f.audit(t, "database.assistant.ask"); !strings.Contains(detail, `"outcome":"asked"`) {
		t.Errorf("audit %s", detail)
	}

	f.provider.queue(claudeAnswer("SELECT count(*) FROM orders;"))
	events = lines(t, f.do(http.MethodPost, "/databases/1/assistant", fmt.Sprintf(`{"conversation":%q,"allow":true}`, conversation)))
	described := ofType(events, "described")
	if len(described) != 1 {
		t.Fatalf("events %v", events)
	}
	if call := described[0]["calls"].([]any)[0].(map[string]any); call["tables"].([]any)[0] != "orders" {
		t.Errorf("described %v", call)
	}
	messages := f.provider.last()["messages"].([]any)
	result := messages[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	if result["type"] != "tool_result" || result["tool_use_id"] != "toolu_01" || result["is_error"] != nil ||
		!strings.Contains(result["content"].(string), "customer_id INTEGER NOT NULL REFERENCES customers(id)") {
		t.Errorf("the tool's result %v", result)
	}
	if strings.Contains(f.provider.raw[1], assistantSentinel) {
		t.Error("reading the structure sent a value")
	}
	detail, _ := f.audit(t, "database.assistant.tool")
	if !strings.Contains(detail, `"allowed":true`) || !strings.Contains(detail, `"tables":1`) || !strings.Contains(detail, `"outcome":"answered"`) {
		t.Errorf("audit %s", detail)
	}

	rec := f.do(http.MethodPost, "/databases/1/assistant", fmt.Sprintf(`{"conversation":%q,"allow":true}`, conversation))
	if rec.Code != http.StatusConflict || errCode(rec) != "nothing_pending" {
		t.Errorf("allowing a call that is not waiting: %d %s", rec.Code, rec.Body.String())
	}
}

func TestADeclinedCallIsAnsweredWithoutReadingAnything(t *testing.T) {
	f := assistantRouterAs(t, auth.RoleLimited)
	f.claude(t)
	f.provider.queue(claudeAnswer("I need customers.", "customers"), claudeAnswer("Without it: SELECT 1;"))
	events := lines(t, f.do(http.MethodPost, "/databases/1/assistant", askOrders))
	events = lines(t, f.do(http.MethodPost, "/databases/1/assistant", fmt.Sprintf(`{"conversation":%q,"allow":false}`, events[0]["conversation"])))
	if len(ofType(events, "described")) != 0 {
		t.Error("a declined call was read")
	}
	messages := f.provider.last()["messages"].([]any)
	result := messages[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	if result["is_error"] != true || result["content"] != assistant.Declined {
		t.Errorf("the declined call's result %v", result)
	}
	if detail, _ := f.audit(t, "database.assistant.tool"); !strings.Contains(detail, `"allowed":false`) || !strings.Contains(detail, `"tables":0`) {
		t.Errorf("audit %s", detail)
	}
}

func TestAProtectedConnectionIsNeverSentToAModel(t *testing.T) {
	f := assistantRouterAs(t, auth.RoleAdmin)
	f.claude(t)
	if _, err := f.s.Store.DB.Exec(`UPDATE db_connections SET read_only = 1 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	rec := f.do(http.MethodPost, "/databases/1/assistant", askOrders)
	if rec.Code != http.StatusConflict || errCode(rec) != "connection_read_only" {
		t.Errorf("a protected connection: %d %s", rec.Code, rec.Body.String())
	}
	if f.provider.asked() != 0 {
		t.Error("a protected connection's schema reached the provider")
	}
}

func TestTheAssistantRefusesWhatItCannotServe(t *testing.T) {
	f := assistantRouterAs(t, auth.RoleAdmin)
	f.claude(t)
	sealed, _ := f.s.Sealer.Seal("redis://127.0.0.1:1/0")
	if _, err := f.s.Store.DB.Exec(`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES('cache','redis',?,0)`, sealed); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, path, body string
		status           int
		code             string
	}{
		{"a key–value store", "/databases/2/assistant", askOrders, http.StatusBadRequest, "bad_request"},
		{"a policy on SQLite", "/databases/1/assistant", `{"task":{"kind":"policy","table":"orders","prompt":"own rows"}}`, http.StatusBadRequest, "bad_request"},
		{"an empty question", "/databases/1/assistant", `{"task":{"kind":"sql","prompt":" "}}`, http.StatusBadRequest, "bad_request"},
		{"a question and an answer at once", "/databases/1/assistant", `{"task":{"kind":"sql","prompt":"x"},"allow":true}`, http.StatusBadRequest, "bad_request"},
		{"a conversation that is not here", "/databases/1/assistant", `{"conversation":"nope","task":{"kind":"sql","prompt":"x"}}`, http.StatusNotFound, "conversation_gone"},
		{"an answer with no conversation", "/databases/1/assistant", `{"allow":true}`, http.StatusBadRequest, "bad_request"},
	} {
		rec := f.do(http.MethodPost, c.path, c.body)
		if rec.Code != c.status || errCode(rec) != c.code {
			t.Errorf("%s: %d %s", c.name, rec.Code, rec.Body.String())
		}
	}
	if f.provider.asked() != 0 {
		t.Error("a refused question reached the provider")
	}

	// A conversation belongs to the connection it began on.
	events := lines(t, f.do(http.MethodPost, "/databases/1/assistant", askOrders))
	sealed, _ = f.s.Sealer.Seal(filepath.Join(f.s.Cfg.FileRoots[0], "shop.db"))
	if _, err := f.s.Store.DB.Exec(`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES('shop again','sqlite',?,0)`, sealed); err != nil {
		t.Fatal(err)
	}
	rec := f.do(http.MethodPost, "/databases/3/assistant", fmt.Sprintf(`{"conversation":%q,"task":{"kind":"sql","prompt":"x"}}`, events[0]["conversation"]))
	if rec.Code != http.StatusNotFound {
		t.Errorf("a conversation carried on through another connection: %d", rec.Code)
	}
}

func TestTheInstallsBudgetIsShared(t *testing.T) {
	f := assistantRouterAs(t, auth.RoleLimited)
	f.claude(t)
	f.s.assistant.limit = httpx.NewLimiter(1, 1)
	lines(t, f.do(http.MethodPost, "/databases/1/assistant", askOrders))
	rec := f.do(http.MethodPost, "/databases/1/assistant", askOrders)
	if rec.Code != http.StatusTooManyRequests || errCode(rec) != "rate_limited" {
		t.Errorf("past the budget: %d %s", rec.Code, rec.Body.String())
	}
	if f.provider.asked() != 1 {
		t.Errorf("%d questions reached the provider", f.provider.asked())
	}
}

func TestAnOpenAICompatibleServerIsAskedWithoutTools(t *testing.T) {
	f := assistantRouterAs(t, auth.RoleLimited)
	f.set(t, assistant.Settings{Provider: assistant.ProviderOpenAI, Model: "llama3.1:8b", BaseURL: f.url + "/v1"})
	f.provider.queue(func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range []string{
			`{"model":"llama3.1:8b","choices":[{"delta":{"content":"SELECT count(*) "}}]}`,
			`{"choices":[{"delta":{"content":"FROM orders;"},"finish_reason":"stop"}]}`,
			`{"choices":[],"usage":{"prompt_tokens":800,"completion_tokens":9}}`,
			`[DONE]`,
		} {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
		}
	})
	events := lines(t, f.do(http.MethodPost, "/databases/1/assistant", askOrders))
	var text strings.Builder
	for _, e := range ofType(events, "text") {
		text.WriteString(e["text"].(string))
	}
	if text.String() != "SELECT count(*) FROM orders;" {
		t.Errorf("streamed %q", text.String())
	}
	body := f.provider.last()
	if f.provider.paths[len(f.provider.paths)-1] != "POST /v1/chat/completions" || body["tools"] != nil {
		t.Errorf("asked %v with %v", f.provider.paths, body)
	}
	system := body["messages"].([]any)[0].(map[string]any)["content"].(string)
	if strings.Contains(system, "describe_tables") || !strings.Contains(system, "TABLE orders") || strings.Contains(f.provider.raw[0], assistantSentinel) {
		t.Errorf("the system message:\n%s", system)
	}
	if detail, _ := f.audit(t, "database.assistant.ask"); !strings.Contains(detail, `"usage":{"input":800,"output":9}`) || !strings.Contains(detail, `"provider":"openai"`) {
		t.Errorf("audit %s", detail)
	}
}

func TestAProviderFailureLeavesNothingBehind(t *testing.T) {
	f := assistantRouterAs(t, auth.RoleLimited)
	f.claude(t)
	f.provider.queue(func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
	})
	rec := f.do(http.MethodPost, "/databases/1/assistant", askOrders)
	if rec.Code != http.StatusBadGateway || errCode(rec) != "assistant_failed" || !strings.Contains(rec.Body.String(), "did not accept the key") {
		t.Errorf("a refused key: %d %s", rec.Code, rec.Body.String())
	}
	if detail, _ := f.audit(t, "database.assistant.ask"); !strings.Contains(detail, `"outcome":"failed"`) {
		t.Errorf("audit %s", detail)
	}

	// A conversation whose answer fails part-way goes back to where it was.
	events := lines(t, f.do(http.MethodPost, "/databases/1/assistant", askOrders))
	conversation := events[0]["conversation"].(string)
	f.provider.queue(func(w http.ResponseWriter) {
		sseEvents(w,
			map[string]any{"type": "message_start", "message": map[string]any{"model": "claude-opus-5-5"}},
			map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": "Half an answ"}},
			map[string]any{"type": "error", "error": map[string]string{"type": "overloaded_error", "message": "Overloaded"}},
		)
	})
	events = lines(t, f.do(http.MethodPost, "/databases/1/assistant", fmt.Sprintf(`{"conversation":%q,"task":{"kind":"sql","prompt":"again"}}`, conversation)))
	if last := events[len(events)-1]; last["type"] != "error" || !strings.Contains(last["error"].(map[string]any)["message"].(string), "Overloaded") {
		t.Errorf("the failed answer ended with %v", last)
	}
	lines(t, f.do(http.MethodPost, "/databases/1/assistant", fmt.Sprintf(`{"conversation":%q,"task":{"kind":"sql","prompt":"third"}}`, conversation)))
	if messages := f.provider.last()["messages"].([]any); len(messages) != 3 {
		t.Errorf("after a failed turn the next question was sent with %d messages, want 3", len(messages))
	}
}

func TestASettingsChangeEndsTheConversation(t *testing.T) {
	f := assistantRouterAs(t, auth.RoleLimited)
	f.claude(t)
	events := lines(t, f.do(http.MethodPost, "/databases/1/assistant", askOrders))
	f.set(t, assistant.Settings{Provider: assistant.ProviderAnthropic, Model: "claude-haiku-5-5", Key: "sk-ant-test-key", UpdatedAt: time.Now().Add(time.Second)})
	rec := f.do(http.MethodPost, "/databases/1/assistant", fmt.Sprintf(`{"conversation":%q,"task":{"kind":"sql","prompt":"x"}}`, events[0]["conversation"]))
	if rec.Code != http.StatusConflict || errCode(rec) != "conversation_ended" {
		t.Errorf("carried on under another model: %d %s", rec.Code, rec.Body.String())
	}
}
