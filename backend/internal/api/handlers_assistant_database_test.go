package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/user"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/assistant"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/term"
)

// A database connection's assistant, the shell it can run, what the SQL editor
// hands over with a question, and the page a server chat was opened from — over
// HTTP, against a provider answering as Anthropic's API, with the whole router
// under each call. The database is assistantRouterAs's SQLite file, whose rows say
// they must never be sent unless a call reads them.

// databaseAssistantAs is a fixture whose router has the database routes and the
// assistant's, and a person whose chats are saved.
func databaseAssistantAs(t *testing.T, role auth.Role) *assistantFixture {
	t.Helper()
	f := assistantRouterAs(t, role)
	f.s.assistant.limit = httpx.NewLimiter(6000, 100)
	// A call is made through the whole router, which this builds.
	f.s.Routes()
	if _, err := f.s.Store.DB.Exec(`INSERT INTO users(id, username, password_hash, role, created_at) VALUES(1, 'operator', 'x', ?, 0)`, string(role)); err != nil {
		t.Fatal(err)
	}
	f.claude(t)
	return f
}

func (f *assistantFixture) askDatabase(t *testing.T, id int64, body string) []map[string]any {
	t.Helper()
	return lines(t, f.do(http.MethodPost, fmt.Sprintf("/databases/%d/assistant", id), body))
}

// A connection's chat begins knowing the connection and its structure, never a
// row, is offered the whole dashboard and a shell but not a deployment's tools,
// and is listed under the connection.
func TestADatabasesChatBeginsKnowingItsStructure(t *testing.T) {
	f := databaseAssistantAs(t, auth.RoleAdmin)
	f.provider.queue(claudeAnswer("It has customers and their orders."))
	events := f.askDatabase(t, 1, `{"question":"What is in this database?"}`)
	if len(ofType(events, "done")) != 1 {
		t.Fatalf("events = %v", events)
	}
	first := f.provider.raw[0]
	for _, want := range []string{"shop", "customers", "orders", "customer_id", `"name":"api_request"`, `"name":"run_command"`, `"name":"open_page"`, "routes are under /databases/1"} {
		if !strings.Contains(first, want) {
			t.Errorf("the chat began without %q", want)
		}
	}
	for _, unwanted := range []string{assistantSentinel + "@", `"name":"call_operation"`, `"name":"recent_runs"`} {
		if strings.Contains(first, unwanted) {
			t.Errorf("the chat began with %q", unwanted)
		}
	}
	rec := f.do(http.MethodGet, "/assistant/chats?database=1", "")
	var listed struct{ Chats []chatSummary }
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil || len(listed.Chats) != 1 {
		t.Fatalf("chats = %s", rec.Body.String())
	}
	if scope := listed.Chats[0].Scope; scope.Kind != "database" || scope.Database != 1 || scope.Name != "shop" {
		t.Fatalf("scope = %+v", scope)
	}
	if rec := f.do(http.MethodGet, "/assistant/chats?scope=database", ""); !strings.Contains(rec.Body.String(), `"kind":"database"`) {
		t.Fatalf("by kind = %s", rec.Body.String())
	}
	if detail, _ := f.audit(t, "database.assistant.ask"); !strings.Contains(detail, `"databaseId":1`) {
		t.Errorf("audit = %s", detail)
	}
	// Carried on, it is still a database's chat.
	f.provider.queue(claudeAnswer("Two tables."))
	again := f.askDatabase(t, 1, fmt.Sprintf(`{"conversation":%q,"question":"How many tables?"}`, conversationOf(events)))
	if len(ofType(again, "done")) != 1 {
		t.Fatalf("carried on = %v", again)
	}
	// Another subject's route does not carry it on.
	rec = f.do(http.MethodPost, "/assistant/chat", fmt.Sprintf(`{"conversation":%q,"question":"hi"}`, conversationOf(events)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("carried on as the server's: %d %s", rec.Code, rec.Body.String())
	}
}

// A protected connection is kept from every conversation: its own assistant is
// refused, and another chat's call into it is answered with why, reading nothing.
func TestAProtectedConnectionIsKeptFromEveryChat(t *testing.T) {
	f := databaseAssistantAs(t, auth.RoleAdmin)
	if _, err := f.s.Store.DB.Exec(`UPDATE db_connections SET read_only = 1 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	rec := f.do(http.MethodPost, "/databases/1/assistant", askOrders)
	if rec.Code != http.StatusConflict || errCode(rec) != "connection_read_only" {
		t.Fatalf("its own assistant: %d %s", rec.Code, rec.Body.String())
	}
	f.provider.queue(
		claudeCalls("Reading.",
			request("/databases/1", "GET", nil, "The connection."),
			request("/databases/1/query", "POST", map[string]any{"query": "SELECT * FROM orders"}, "The orders."),
			request("/databases/1/tables", "GET", nil, "Its tables.")),
		claudeAnswer("It is protected."),
	)
	events := lines(t, f.do(http.MethodPost, "/assistant/chat", `{"question":"What orders are there?","mode":"full"}`))
	called := ofType(events, "called")
	if len(called) != 3 || called[0]["ok"] != true || called[1]["ok"] != false || called[2]["ok"] != false {
		t.Fatalf("called = %v", called)
	}
	if said := fmt.Sprint(called[1]["said"]); !strings.Contains(said, "protected") {
		t.Errorf("the refusal said %q", said)
	}
	if strings.Contains(strings.Join(f.provider.raw, ""), assistantSentinel) {
		t.Fatal("a protected connection's rows reached the model")
	}
}

// What the editor hands over goes to the model with the question, and the page
// and the saved chat draw it by its label alone.
func TestWhatIsAttachedGoesWithTheQuestion(t *testing.T) {
	f := databaseAssistantAs(t, auth.RoleAdmin)
	f.provider.queue(claudeAnswer("The join is missing its condition."))
	body := `{"question":"Why is this slow?","attached":[{"label":"Statement","text":"SELECT * FROM orders, customers"},{"label":"Error · 1 line","text":"too many rows ` + "```" + ` in output"}]}`
	events := f.askDatabase(t, 1, body)
	sent := f.provider.raw[0]
	for _, want := range []string{"Why is this slow?", "Statement, attached by the person", "SELECT * FROM orders, customers", "too many rows"} {
		if !strings.Contains(sent, want) {
			t.Errorf("the model was not sent %q", want)
		}
	}
	id := conversationOf(events)
	rec := f.do(http.MethodGet, "/assistant/chats/"+id, "")
	if !strings.Contains(rec.Body.String(), `"attached":["Statement","Error · 1 line"]`) || strings.Contains(rec.Body.String(), "orders, customers") {
		t.Errorf("the saved question = %s", rec.Body.String())
	}
	if detail, _ := f.audit(t, "database.assistant.ask"); !strings.Contains(detail, `"attached":["Statement","Error · 1 line"]`) || strings.Contains(detail, "SELECT") {
		t.Errorf("audit = %s", detail)
	}
	for _, bad := range []string{
		`{"question":"x","attached":[{"label":"","text":"a"}]}`,
		`{"question":"x","attached":[{"label":"two\nlines","text":"a"}]}`,
		`{"question":"x","attached":[{"label":"a","text":"` + strings.Repeat("y", maxAgentAttachedText+1) + `"}]}`,
		`{"question":"x","attached":[{"label":"a","text":""},{"label":"b","text":""},{"label":"c","text":""},{"label":"d","text":""},{"label":"e","text":""}]}`,
		`{"attached":[{"label":"a","text":"b"}],"decisions":{}}`,
	} {
		if rec := f.do(http.MethodPost, "/databases/1/assistant", bad); rec.Code != http.StatusBadRequest {
			t.Errorf("%.80s: %d %s", bad, rec.Code, rec.Body.String())
		}
	}
}

// A server chat opened from a page is told which, keeps it, and is listed with it.
func TestAServerChatSaysWhereItWasOpened(t *testing.T) {
	f := databaseAssistantAs(t, auth.RoleAdmin)
	f.provider.queue(claudeAnswer("It is the web container."))
	events := lines(t, f.do(http.MethodPost, "/assistant/chat", `{"question":"What is this?","about":{"path":"/docker/containers/abc123?tab=logs","title":"  web-1   container "}}`))
	if !strings.Contains(f.provider.raw[0], `web-1 container`) || !strings.Contains(f.provider.raw[0], "/docker/containers/abc123?tab=logs") {
		t.Fatalf("the prompt did not say where: %s", f.provider.raw[0])
	}
	rec := f.do(http.MethodGet, "/assistant/chats?scope=server", "")
	if !strings.Contains(rec.Body.String(), `"about":{"path":"/docker/containers/abc123?tab=logs","title":"web-1 container"}`) {
		t.Fatalf("listed = %s", rec.Body.String())
	}
	if detail, _ := f.audit(t, "assistant.chat.ask"); !strings.Contains(detail, `"about":"/docker/containers/abc123?tab=logs"`) {
		t.Errorf("audit = %s", detail)
	}
	// Only a new chat takes a page.
	f.provider.queue(claudeAnswer("Still the web container."))
	lines(t, f.do(http.MethodPost, "/assistant/chat", fmt.Sprintf(`{"conversation":%q,"question":"And now?","about":{"path":"/files","title":"Files"}}`, conversationOf(events))))
	if rec := f.do(http.MethodGet, "/assistant/chats?scope=server", ""); strings.Contains(rec.Body.String(), `"Files"`) {
		t.Errorf("a carried-on chat changed its page: %s", rec.Body.String())
	}
	for _, bad := range []string{
		`{"question":"x","about":{"path":"docker","title":"x"}}`,
		`{"question":"x","about":{"path":"//evil.example/x","title":"x"}}`,
		`{"question":"x","about":{"path":"/` + strings.Repeat("a", maxAboutPath) + `","title":"x"}}`,
		`{"question":"x","about":{"path":"/files","title":"` + strings.Repeat("t", maxAboutTitle+1) + `"}}`,
	} {
		if rec := f.do(http.MethodPost, "/assistant/chat", bad); rec.Code != http.StatusBadRequest {
			t.Errorf("%.60s: %d %s", bad, rec.Code, rec.Body.String())
		}
	}
}

// A connection's chats go when it is forgotten.
func TestAConnectionsChatsGoWithIt(t *testing.T) {
	f := databaseAssistantAs(t, auth.RoleAdmin)
	f.provider.queue(claudeAnswer("Hello."))
	f.askDatabase(t, 1, `{"question":"Hello"}`)
	if _, err := f.s.forgetConnection(t.Context(), 1, "operator"); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := f.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM assistant_chats WHERE scope_kind = 'database'`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("chats left = %d (%v)", left, err)
	}
}

// withTerminal gives the fixture a terminal that runs as this test's own account.
func withTerminal(t *testing.T, f *assistantFixture) {
	t.Helper()
	me, err := user.Current()
	if err != nil {
		t.Skip("no current account")
	}
	f.s.modules.term = term.NewManager(true, "/bin/bash", me.Username)
}

// A command that only reads runs unasked in the default mode, as the terminal
// would, and its output is masked; one that writes waits; a person whose role has
// no terminal cannot have one run.
func TestRunCommandIsTheTerminalsAndAsksBeforeItWrites(t *testing.T) {
	f := databaseAssistantAs(t, auth.RoleAdmin)
	withTerminal(t, f)
	f.provider.queue(
		claudeCalls("Looking.",
			[2]any{assistant.ToolRunCommand, map[string]any{"command": "echo hello-from-the-shell; echo password=hunter2-shell >&2", "reason": "Saying hello."}},
			[2]any{assistant.ToolRunCommand, map[string]any{"command": "touch never-made", "reason": "Making a file."}},
			[2]any{assistant.ToolRunCommand, map[string]any{"command": "rm -rf /tmp/nothing-here", "reason": "Removing."}}),
	)
	events := lines(t, f.do(http.MethodPost, "/assistant/chat", `{"question":"Say hello","mode":"changes"}`))
	called := ofType(events, "called")
	pending, _ := doneOf(t, events)["pending"].([]any)
	// The first only reads (>&2 points one stream at the other), so it runs.
	if len(called) != 1 || called[0]["ok"] != true || called[0]["said"] != "exit 0" || len(pending) != 2 {
		t.Fatalf("called = %v pending = %v", called, pending)
	}
	kinds := []string{}
	for _, p := range pending {
		kinds = append(kinds, p.(map[string]any)["kind"].(string))
	}
	if strings.Join(kinds, ",") != "change,destructive" {
		t.Fatalf("kinds = %v", kinds)
	}
	output := fmt.Sprint(called[0]["output"])
	if !strings.Contains(output, "hello-from-the-shell") || strings.Contains(output, "hunter2-shell") {
		t.Fatalf("output = %q", output)
	}
	if strings.Contains(strings.Join(f.provider.raw, ""), "hunter2-shell") {
		t.Fatal("an unmasked secret reached the model")
	}
	if detail, _ := f.audit(t, "assistant.chat.ask"); !strings.Contains(detail, "echo hello-from-the-shell") || strings.Contains(detail, "hunter2-shell") {
		t.Errorf("audit = %s", detail)
	}
	f.provider.queue(claudeAnswer("I left the file alone."))
	decided := lines(t, f.do(http.MethodPost, "/assistant/chat", fmt.Sprintf(`{"conversation":%q,"decisions":{"toolu_02":false,"toolu_03":false}}`, conversationOf(events))))
	if called := ofType(decided, "called"); len(called) != 2 || called[0]["declined"] != true {
		t.Fatalf("declined = %v", called)
	}

	// A plain read runs unasked.
	f.provider.queue(claudeCalls("Looking.", [2]any{assistant.ToolRunCommand, map[string]any{"command": "echo plain-read", "reason": "Reading."}}), claudeAnswer("Read."))
	events = lines(t, f.do(http.MethodPost, "/assistant/chat", `{"question":"Read","mode":"changes"}`))
	if called := ofType(events, "called"); len(called) != 1 || !strings.Contains(fmt.Sprint(called[0]["output"]), "plain-read") {
		t.Fatalf("a read = %v", events)
	}

	// A line that changes something, run, is said as a change with where it shows.
	made := t.TempDir() + "/made-by-the-assistant"
	f.provider.queue(claudeCalls("Making it.", [2]any{assistant.ToolRunCommand, map[string]any{"command": "touch " + made, "reason": "Making it."}}), claudeAnswer("Made."))
	events = lines(t, f.do(http.MethodPost, "/assistant/chat", `{"question":"Make it","mode":"full"}`))
	if called := ofType(events, "called"); len(called) != 1 || called[0]["change"] == nil || called[0]["change"].(map[string]any)["href"] != "/audit" {
		t.Fatalf("a change = %v", called)
	}

	// A command past its time is stopped and says so.
	f.provider.queue(claudeCalls("Waiting.", [2]any{assistant.ToolRunCommand, map[string]any{"command": "sleep 30", "timeout": 1, "reason": "Waiting."}}), claudeAnswer("It took too long."))
	events = lines(t, f.do(http.MethodPost, "/assistant/chat", `{"question":"Wait","mode":"full"}`))
	if called := ofType(events, "called"); len(called) != 1 || called[0]["ok"] != false || called[0]["said"] != "stopped after 1s" {
		t.Fatalf("a slow command = %v", called)
	}
}

func TestRunCommandTakesTheTerminalsCapability(t *testing.T) {
	f := databaseAssistantAs(t, auth.RoleLimited)
	withTerminal(t, f)
	f.provider.queue(claudeCalls("Looking.", [2]any{assistant.ToolRunCommand, map[string]any{"command": "echo no", "reason": "x"}}), claudeAnswer("I cannot."))
	events := lines(t, f.do(http.MethodPost, "/assistant/chat", `{"question":"x","mode":"full"}`))
	called := ofType(events, "called")
	if len(called) != 1 || called[0]["ok"] != false || !strings.Contains(fmt.Sprint(called[0]["said"]), "terminal") {
		t.Fatalf("called = %v", called)
	}

	off := databaseAssistantAs(t, auth.RoleAdmin)
	off.s.modules.term = term.NewManager(false, "/bin/bash", "")
	off.provider.queue(claudeCalls("Looking.", [2]any{assistant.ToolRunCommand, map[string]any{"command": "echo no", "reason": "x"}}), claudeAnswer("Off."))
	events = lines(t, off.do(http.MethodPost, "/assistant/chat", `{"question":"x","mode":"full"}`))
	if called := ofType(events, "called"); len(called) != 1 || !strings.Contains(fmt.Sprint(called[0]["said"]), "turned off") {
		t.Fatalf("with the terminal off = %v", called)
	}
}

// open_page in a database's chat goes to the connection's pages, every key the
// tool offers has a page, and every page is offered.
func TestADatabasesShortcutsAreItsPages(t *testing.T) {
	if len(agentDatabasePages) != len(assistant.DatabasePages) {
		t.Fatalf("%d pages, %d offered", len(agentDatabasePages), len(assistant.DatabasePages))
	}
	for _, key := range assistant.DatabasePages {
		if _, ok := agentDatabasePages[key]; !ok {
			t.Errorf("%q is offered and has no page", key)
		}
	}
	f := databaseAssistantAs(t, auth.RoleAdmin)
	f.provider.queue(claudeCalls("There.", [2]any{assistant.ToolOpenPage, map[string]any{"page": "performance", "note": "Its slow queries."}}), claudeAnswer("Open it."))
	events := f.askDatabase(t, 1, `{"question":"Where are its slow queries?"}`)
	links := ofType(events, "link")
	if len(links) != 1 || links[0]["link"].(map[string]any)["href"] != "/databases/1/performance" {
		t.Fatalf("links = %v", links)
	}
}
