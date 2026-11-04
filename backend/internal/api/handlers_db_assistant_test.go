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
			// A call the assistant makes starts from this request's address.
			req.RemoteAddr = "127.0.0.1:1234"
			next.ServeHTTP(w, req.WithContext(httpx.WithPrincipal(req.Context(), p)))
		})
	})
	r.Use(httpx.AuditMutations(s.Audit))
	s.mountDatabaseRoutes(r)
	s.mountAssistantRoutes(r)

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

const askOrders = `{"question":"count the orders for each customer"}`

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
	raw, _, _ := f.s.Store.Setting(t.Context(), assistantRingKey)
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
	if again, _, _ := f.s.Store.Setting(t.Context(), assistantRingKey); again != raw {
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
