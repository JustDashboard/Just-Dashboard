package api

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/assistant"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// The assistant with the whole dashboard: robust calls, requests as the person,
// the server's chat, and saved chats carried on and taken over.

// as is the fixture's router signed in as another person, on the same server.
func (f *operatorFixture) as(t *testing.T, id int64, username string) *operatorFixture {
	t.Helper()
	if _, err := f.s.Store.DB.Exec(`INSERT INTO users(id, username, password_hash, role, created_at) VALUES(?, ?, 'x', 'admin', 0)`, id, username); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			p := &httpx.Principal{User: &auth.User{ID: id, Username: username}, Role: auth.RoleAdmin, Kind: "session", IP: "127.0.0.1"}
			req.RemoteAddr = "127.0.0.1:1234"
			next.ServeHTTP(w, req.WithContext(httpx.WithPrincipal(req.Context(), p)))
		})
	})
	r.Use(httpx.AuditMutations(f.s.Audit))
	f.s.mountDeployRoutes(r)
	f.s.mountAssistantRoutes(r)
	other := *f
	fixture := *f.assistantFixture
	fixture.router = r
	other.assistantFixture = &fixture
	return &other
}

func request(path string, method string, body any, reason string) [2]any {
	in := map[string]any{"method": method, "path": path, "reason": reason}
	if body != nil {
		in["body"] = body
	}
	return [2]any{assistant.ToolAPIRequest, in}
}

func conversationOf(events []map[string]any) string {
	start := ofType(events, "start")
	if len(start) == 0 {
		return ""
	}
	id, _ := start[0]["conversation"].(string)
	return id
}

// A Claude answer with a call past its bounds, an arguments object written as its
// text, and a call for a tool that does not exist runs what it can, answers the
// rest with why, and the turn goes on to its answer. The page is shown the two
// calls that ran, not the one that was the model's slip.
func TestAClaudeAnswerWithABadCallGoesOnThroughTheRouter(t *testing.T) {
	f := operatorRouterAs(t, auth.RoleAdmin)
	operatorLive(t, f)
	f.provider.queue(
		claudeCalls("Looking.",
			[2]any{assistant.ToolRecentRuns, map[string]any{"limit": 50, "since": "1h"}},
			[2]any{assistant.ToolCallOperation, map[string]any{"name": "deployment_variables", "reason": "Listing them.", "arguments": fmt.Sprintf(`{"env": %d}`, f.environmentID)}},
			[2]any{"run_shell", map[string]any{"command": "env"}},
		),
		claudeAnswer("Two of three worked."),
	)
	events := f.ask(t, `{"question":"What is set?","mode":"changes"}`)
	if errs := ofType(events, "error"); len(errs) != 0 {
		t.Fatalf("the answer was refused: %v", errs)
	}
	called := ofType(events, "called")
	if len(called) != 2 || called[0]["ok"] != true || called[1]["ok"] != true || len(ofType(events, "call")) != 2 {
		t.Fatalf("called = %v", called)
	}
	if doneOf(t, events)["kept"] != true || len(f.provider.raw) != 2 {
		t.Fatalf("the turn did not go on: %v", events)
	}
	sent := f.provider.raw[1]
	for _, want := range []string{`"is_error":true`, `There is no tool called \"run_shell\"`, "Note: limit was 50", "'since' is not one of recent_runs's fields"} {
		if !strings.Contains(sent, want) {
			t.Errorf("the model was not told %q: %s", want, sent)
		}
	}
	if detail, _ := f.audit(t, "deploy.assistant.ask"); !strings.Contains(detail, `"malformed":["run_shell"]`) {
		t.Fatalf("audit = %s", detail)
	}
}

// api_request is the person through the router: it lists the environment's
// variables with the secret's value masked, and the route's audit entry, for a
// change, is the person's marked as the assistant's.
func TestTheAssistantReadsVariablesMaskedThroughTheAPI(t *testing.T) {
	f := operatorRouterAs(t, auth.RoleAdmin)
	const secret = "violet-meadow-compass-91"
	variables := fmt.Sprintf("/deploy/%d/environments/%d/variables", f.projectID, f.environmentID)
	if rec := f.do(http.MethodPut, variables+"/STRIPE_KEY", fmt.Sprintf(`{"revision":1,"value":%q,"sensitivity":"secret","scopes":["runtime"]}`, secret)); rec.Code != http.StatusOK {
		t.Fatalf("variable = %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.do(http.MethodPut, variables+"/NODE_ENV", `{"revision":2,"value":"production","sensitivity":"plain","scopes":["runtime"]}`); rec.Code != http.StatusOK {
		t.Fatalf("variable = %d %s", rec.Code, rec.Body.String())
	}
	f.provider.queue(
		claudeCalls("Listing.", request(variables, "GET", nil, "Listing the variables."),
			request(variables+"/STRIPE_KEY/reveal", "GET", nil, "Reading it.")),
		claudeAnswer("STRIPE_KEY and NODE_ENV are set."),
	)
	events := f.ask(t, `{"question":"Which variables are set?","mode":"changes"}`)
	called := ofType(events, "called")
	if len(called) != 2 || called[0]["ok"] != true || called[1]["ok"] != true {
		t.Fatalf("called = %v", called)
	}
	call := ofType(events, "call")[0]["call"].(map[string]any)
	if call["tool"] != assistant.ToolAPIRequest || call["kind"] != "read" || call["route"] != "GET "+variables {
		t.Fatalf("call = %v", call)
	}
	sent := f.provider.raw[1]
	if strings.Contains(sent, secret) || !strings.Contains(sent, "STRIPE_KEY") || !strings.Contains(sent, "production") {
		t.Fatalf("the variables reached the model as %s", sent)
	}
	for _, line := range called {
		if strings.Contains(fmt.Sprint(line["output"]), secret) {
			t.Fatalf("the page was shown the secret: %v", line)
		}
	}
}

// The server's backups through the API: a list runs in "changes", starting one waits
// for the person there, and runs unasked in full access.
func TestTheAssistantListsAndRunsABackup(t *testing.T) {
	f := operatorRouterAs(t, auth.RoleAdmin)
	f.s.assistant.limit = httpx.NewLimiter(6000, 100)
	admin := &client{t: t, h: f.s.Routes(), cookie: signInAs(t, f.s, "backup-admin", auth.RoleAdmin)}
	job, _ := fileBackupJob(t, f.s, admin, "nightly", nil)
	runs := func() int {
		var n int
		if err := f.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM backup_runs WHERE job_id = ?`, job.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	run := request(fmt.Sprintf("/backups/%d/run", job.ID), "POST", nil, "Backing up now.")
	f.provider.queue(claudeCalls("Listing.", request("/backups", "GET", nil, "Listing the jobs."), run))
	events := f.ask(t, `{"question":"Back it all up","mode":"changes"}`)
	called := ofType(events, "called")
	if len(called) != 1 || called[0]["ok"] != true || !strings.Contains(f.provider.raw[0], "nightly") && !strings.Contains(fmt.Sprint(called[0]["output"]), "nightly") {
		t.Fatalf("the list = %v", called)
	}
	pending, _ := doneOf(t, events)["pending"].([]any)
	if len(pending) != 1 || pending[0].(map[string]any)["kind"] != "change" || runs() != 0 {
		t.Fatalf("starting a backup did not wait: %v", pending)
	}
	f.provider.queue(claudeCalls("Backing up.", run), claudeAnswer("Started."))
	events = f.ask(t, `{"question":"Back it up now","mode":"full"}`)
	called = ofType(events, "called")
	if len(called) != 1 || called[0]["ok"] != true {
		t.Fatalf("full access = %v", events)
	}
	change, _ := called[0]["change"].(map[string]any)
	if change == nil || change["href"] != fmt.Sprintf("/backups/%d", job.ID) {
		t.Fatalf("change = %v", called[0])
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		var state string
		_ = f.s.Store.DB.QueryRow(`SELECT status FROM backup_runs WHERE job_id = ? ORDER BY id DESC LIMIT 1`, job.ID).Scan(&state)
		if state != "" && state != "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the backup the assistant started did not finish (%q)", state)
		}
		time.Sleep(50 * time.Millisecond)
	}
	var actor string
	if err := f.s.Store.DB.QueryRow(`SELECT actor FROM audit_log WHERE action = 'backup.run' ORDER BY id DESC LIMIT 1`).Scan(&actor); err != nil || actor != "assistant" {
		t.Fatalf("the backup was audited as %q (%v)", actor, err)
	}
}

// A database is asked about through its own routes: a statement that only reads
// runs in "changes", and one that writes waits.
func TestADatabaseQueryThatOnlyReadsRunsUnasked(t *testing.T) {
	f := operatorRouterAs(t, auth.RoleAdmin)
	f.s.assistant.limit = httpx.NewLimiter(6000, 100)
	path := filepath.Join(f.s.Cfg.FileRoots[0], "shop.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE orders (id INTEGER PRIMARY KEY, status TEXT); INSERT INTO orders(status) VALUES ('paid-sentinel-73')`); err != nil {
		t.Fatal(err)
	}
	sealed, err := f.s.Sealer.Seal(path)
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.s.Store.DB.Exec(`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES('shop', ?, ?, 0)`, string(dbx.DriverSQLite), sealed)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	query := fmt.Sprintf("/databases/%d/query", id)
	f.provider.queue(
		claudeCalls("Reading.",
			request("/databases", "GET", nil, "Finding the connection."),
			request(query, "POST", map[string]any{"query": "SELECT status FROM orders", "maxRows": 10}, "Reading the orders."),
			request(query, "POST", map[string]any{"query": "DELETE FROM orders"}, "Clearing them.")),
	)
	events := f.ask(t, `{"question":"What are the orders?","mode":"changes"}`)
	called := ofType(events, "called")
	if len(called) != 2 || called[0]["ok"] != true || called[1]["ok"] != true {
		t.Fatalf("called = %v", called)
	}
	if !strings.Contains(fmt.Sprint(called[1]["output"]), "paid-sentinel-73") {
		t.Fatalf("the query's rows = %v", called[1])
	}
	pending, _ := doneOf(t, events)["pending"].([]any)
	if len(pending) != 1 || pending[0].(map[string]any)["kind"] == "read" {
		t.Fatalf("a statement that writes did not wait: %v", pending)
	}
	var left int
	if err := db.QueryRow(`SELECT COUNT(*) FROM orders`).Scan(&left); err != nil || left != 1 {
		t.Fatalf("the orders = %d (%v)", left, err)
	}
}

// What the assistant cannot make is refused with the reason, and nothing runs.
func TestAnExcludedRouteIsRefusedWithItsReason(t *testing.T) {
	f := operatorRouterAs(t, auth.RoleAdmin)
	f.provider.queue(
		claudeCalls("Trying.",
			request("/auth/logout", "POST", map[string]any{}, "x"),
			request("/logs/stream", "GET", nil, "x"),
			request("/deploy/"+fmt.Sprint(f.projectID)+"/assistant", "POST", map[string]any{"question": "hi"}, "x"),
			request("/nothing/here", "GET", nil, "x"),
			[2]any{assistant.ToolFindRoutes, map[string]any{"query": "terminal attach"}}),
		claudeAnswer("I cannot."),
	)
	events := f.ask(t, `{"question":"Try","mode":"full"}`)
	called := ofType(events, "called")
	if len(called) != 5 {
		t.Fatalf("called = %v", called)
	}
	for i, want := range []string{"signing in and out", "live stream", "this is it", "No route answers"} {
		if called[i]["ok"] != false || !strings.Contains(called[i]["said"].(string), want) {
			t.Errorf("call %d = %v, want %q", i, called[i], want)
		}
	}
	if sent := f.provider.raw[1]; !strings.Contains(sent, "Matching routes the assistant cannot make") || !strings.Contains(sent, "/terminal/{id}/attach") {
		t.Fatalf("find_routes did not say what it cannot make: %s", sent)
	}
}

// The server's chat reaches the whole API as the person, and every answer is masked:
// a field named for a secret, key material, and every deployment's secret values.
func TestTheServersChatMasksWhatItReads(t *testing.T) {
	f := operatorRouterAs(t, auth.RoleAdmin)
	const value = "orange-harbour-lantern-55"
	variables := fmt.Sprintf("/deploy/%d/environments/%d/variables", f.projectID, f.environmentID)
	if rec := f.do(http.MethodPut, variables+"/API_KEY", fmt.Sprintf(`{"revision":1,"value":%q,"sensitivity":"secret","scopes":["runtime"]}`, value)); rec.Code != http.StatusOK {
		t.Fatalf("variable = %d %s", rec.Code, rec.Body.String())
	}
	secrets := map[string]any{
		"name": "prod-vault", "password": "hunter2-not-a-shape", "apiKey": "plainwordskey",
		"nested":  map[string]any{"connectionString": "Server=db;Password=pw-in-a-string", "kept": "visible-note"},
		"tokens":  []any{map[string]any{"name": "ci", "token": "tok-123"}},
		"pem":     "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAA\n-----END OPENSSH PRIVATE KEY-----",
		"random":  "Zx8Kq2Lm9Pw4Rt7Yb3Nc6Vd1Hf5Jg0QsAa",
		"message": "the variable was " + value,
	}
	root := chi.NewRouter()
	root.Route("/api/v1", func(r chi.Router) {
		r.Use(f.s.Authn.AuthenticateFeatures(nil, f.s.Audit))
		r.Use(httpx.RequireCSRF)
		r.Get("/vault/entries", func(w http.ResponseWriter, _ *http.Request) { httpx.JSON(w, http.StatusOK, secrets) })
	})
	f.s.deployOpenAPI.Store(&deployOpenAPISource{root: root})
	f.provider.queue(claudeCalls("Reading.", request("/vault/entries", "GET", nil, "Reading the vault.")), claudeAnswer("Read."))
	rec := f.do(http.MethodPost, "/assistant/chat", `{"question":"What is in the vault?","mode":"changes"}`)
	events := lines(t, rec)
	if called := ofType(events, "called"); len(called) != 1 || called[0]["ok"] != true {
		t.Fatalf("called = %v", called)
	}
	sent := f.provider.raw[1]
	for _, kept := range []string{"prod-vault", "visible-note", `\"name\":\"ci\"`, "the variable was"} {
		if !strings.Contains(sent, kept) {
			t.Errorf("%q was masked: %s", kept, sent)
		}
	}
	for _, hidden := range []string{"hunter2", "plainwordskey", "pw-in-a-string", "tok-123", "b3BlbnNzaC1", "Zx8Kq2Lm9Pw4", value} {
		if strings.Contains(sent, hidden) {
			t.Errorf("%q reached the model: %s", hidden, sent)
		}
	}
	first := f.provider.raw[0]
	for _, want := range []string{`"name":"find_routes"`, `"name":"api_request"`, `"name":"host_alerts"`, "shop", "/vault"} {
		if !strings.Contains(first, want) {
			t.Errorf("the server's chat began without %q", want)
		}
	}
	if strings.Contains(first, `"name":"call_operation"`) || strings.Contains(first, `"name":"recent_runs"`) {
		t.Errorf("the server's chat was offered a deployment's tools")
	}
}

// A chat is saved as it goes: it lists, it reads back as the page saw it, and it is
// carried on after the store that held it is gone, as after a restart.
func TestAChatIsSavedAndCarriedOnAfterARestart(t *testing.T) {
	f := operatorRouterAs(t, auth.RoleAdmin)
	f.provider.queue(
		claudeCalls("Reading the runs.", [2]any{assistant.ToolRecentRuns, map[string]any{"limit": 2}}),
		claudeAnswer("The first run failed its health gate."),
	)
	events := f.ask(t, `{"question":"Why did the deploy fail last week? It has been flaky since we moved to the new host and I need to know"}`)
	id := conversationOf(events)
	if id == "" || ofType(events, "start")[0]["title"] == "" {
		t.Fatalf("events = %v", events)
	}
	list := f.do(http.MethodGet, fmt.Sprintf("/assistant/chats?deployment=%d", f.projectID), "")
	var listed struct {
		Chats []chatSummary `json:"chats"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil || len(listed.Chats) != 1 {
		t.Fatalf("list = %d %s", list.Code, list.Body.String())
	}
	chat := listed.Chats[0]
	if chat.ID != id || chat.Scope.Kind != "deployment" || chat.Scope.Deployment != f.projectID || chat.Steps != 1 || chat.Changes != 0 ||
		chat.Running || chat.Waiting || !strings.HasPrefix(chat.Title, "Why did the deploy fail last week?") || len([]rune(chat.Title)) > agentTitleRunes+1 {
		t.Fatalf("chat = %+v", chat)
	}
	if other := f.do(http.MethodGet, "/assistant/chats?scope=server", ""); !strings.Contains(other.Body.String(), `"chats":[]`) {
		t.Fatalf("the server's chats = %s", other.Body.String())
	}
	var sealed string
	if err := f.s.Store.DB.QueryRow(`SELECT conversation_enc FROM assistant_chats WHERE id = ?`, id).Scan(&sealed); err != nil || strings.Contains(sealed, "health gate") {
		t.Fatalf("the conversation was stored as %q (%v)", sealed, err)
	}

	// A restart: nothing is held in memory.
	f.s.assistant.operators = assistant.NewStore()
	f.provider.queue(claudeAnswer("It was the health gate, as I said."))
	events = f.ask(t, `{"conversation":"`+id+`","question":"Remind me?"}`)
	if conversationOf(events) != id || doneOf(t, events)["kept"] != true {
		t.Fatalf("carried on = %v", events)
	}
	sent := f.provider.raw[len(f.provider.raw)-1]
	if !strings.Contains(sent, "Why did the deploy fail last week?") || !strings.Contains(sent, "The first run failed its health gate.") || !strings.Contains(sent, "Remind me?") {
		t.Fatalf("the chat was not carried on as it was: %s", sent)
	}

	read := f.do(http.MethodGet, "/assistant/chats/"+id, "")
	var got struct {
		Chat  chatSummary      `json:"chat"`
		Lines []map[string]any `json:"lines"`
	}
	if err := json.Unmarshal(read.Body.Bytes(), &got); err != nil {
		t.Fatalf("read = %d %s", read.Code, read.Body.String())
	}
	kinds := []string{}
	for _, line := range got.Lines {
		if line["at"] == nil {
			t.Fatalf("a line has no time: %v", line)
		}
		kinds = append(kinds, line["type"].(string))
	}
	want := "question start text call called thinking text done question start thinking text done"
	if strings.Join(kinds, " ") != want {
		t.Fatalf("lines = %s", strings.Join(kinds, " "))
	}
	if got.Lines[2]["text"] != "Reading the runs." {
		t.Fatalf("an answer's text was not kept whole: %v", got.Lines[2])
	}

	renamed := f.do(http.MethodPatch, "/assistant/chats/"+id, `{"title":"  The flaky deploy  "}`)
	if renamed.Code != http.StatusOK || !strings.Contains(renamed.Body.String(), `"title":"The flaky deploy"`) {
		t.Fatalf("rename = %d %s", renamed.Code, renamed.Body.String())
	}
	if gone := f.do(http.MethodDelete, "/assistant/chats/"+id, ""); gone.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", gone.Code, gone.Body.String())
	}
	if rec := f.do(http.MethodPost, fmt.Sprintf("/deploy/%d/assistant", f.projectID), `{"conversation":"`+id+`","question":"and?"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("a deleted chat carried on: %d %s", rec.Code, rec.Body.String())
	}
}

// A chat is its owner's alone: nobody else lists, reads, renames, deletes or
// carries it on.
func TestAChatIsItsOwnersAlone(t *testing.T) {
	f := operatorRouterAs(t, auth.RoleAdmin)
	f.provider.queue(claudeAnswer("Fine."))
	id := conversationOf(f.ask(t, `{"question":"How is it?"}`))
	other := f.as(t, 2, "someone-else")
	if rec := other.do(http.MethodGet, "/assistant/chats", ""); !strings.Contains(rec.Body.String(), `"chats":[]`) {
		t.Fatalf("list = %s", rec.Body.String())
	}
	for _, try := range []struct{ method, body string }{{http.MethodGet, ""}, {http.MethodPatch, `{"title":"mine"}`}, {http.MethodDelete, ""}} {
		if rec := other.do(try.method, "/assistant/chats/"+id, try.body); rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d %s", try.method, rec.Code, rec.Body.String())
		}
	}
	rec := other.do(http.MethodPost, fmt.Sprintf("/deploy/%d/assistant", f.projectID), `{"conversation":"`+id+`","question":"mine now?","takeOver":true}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("somebody else carried it on: %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.do(http.MethodGet, "/assistant/chats/"+id, ""); rec.Code != http.StatusOK {
		t.Fatalf("the owner = %d", rec.Code)
	}
}

// A chat answering somewhere is busy; taking it over stops that press — its stream
// says so and ends — and carries the chat on as it stood.
func TestTakingAChatOverStopsThePressRunningIt(t *testing.T) {
	f := operatorRouterAs(t, auth.RoleAdmin)
	release := make(chan struct{})
	f.provider.queue(func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, e := range []map[string]any{
			{"type": "message_start", "message": map[string]any{"model": "claude-opus-5-5"}},
			{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}},
			{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "Thinking about it"}},
		} {
			data, _ := json.Marshal(e)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e["type"], data)
		}
		w.(http.Flusher).Flush()
		<-release
	})
	server := httptest.NewServer(f.router)
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })
	res, err := http.Post(server.URL+fmt.Sprintf("/deploy/%d/assistant", f.projectID), "application/json", strings.NewReader(`{"question":"Take your time"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	reader := bufio.NewScanner(res.Body)
	var id string
	for reader.Scan() {
		var line map[string]any
		_ = json.Unmarshal(reader.Bytes(), &line)
		if line["type"] == "start" {
			id = line["conversation"].(string)
		}
		if line["type"] == "text" {
			break
		}
	}
	if id == "" {
		t.Fatal("the first press never began")
	}
	busy := f.do(http.MethodPost, fmt.Sprintf("/deploy/%d/assistant", f.projectID), `{"conversation":"`+id+`","question":"Hello?"}`)
	if busy.Code != http.StatusConflict || errCode(busy) != "chat_busy" {
		t.Fatalf("a second press = %d %s", busy.Code, busy.Body.String())
	}
	if list := f.do(http.MethodGet, "/assistant/chats", ""); !strings.Contains(list.Body.String(), `"running":true`) {
		t.Fatalf("the running chat is not listed as running: %s", list.Body.String())
	}
	f.provider.queue(claudeAnswer("Carried on here."))
	events := lines(t, f.do(http.MethodPost, fmt.Sprintf("/deploy/%d/assistant", f.projectID), `{"conversation":"`+id+`","question":"Hello?","takeOver":true}`))
	if conversationOf(events) != id || doneOf(t, events)["kept"] != true {
		t.Fatalf("the take-over = %v", events)
	}
	var rest []map[string]any
	for reader.Scan() {
		var line map[string]any
		_ = json.Unmarshal(reader.Bytes(), &line)
		rest = append(rest, line)
	}
	last := rest[len(rest)-1]
	if last["type"] != "error" || last["error"].(map[string]any)["code"] != "taken_over" {
		t.Fatalf("the first press ended with %v", rest)
	}
	read := f.do(http.MethodGet, "/assistant/chats/"+id, "")
	if strings.Contains(read.Body.String(), "taken_over") || strings.Count(read.Body.String(), `"type":"question"`) != 2 {
		t.Fatalf("lines = %s", read.Body.String())
	}
	if detail, _ := f.audit(t, "deploy.assistant.ask"); !strings.Contains(detail, `"takeOver":true`) {
		t.Fatalf("audit = %s", detail)
	}
}

// Each person keeps their newest chatRetention chats; the oldest go when a new one
// is saved.
func TestChatsAreKeptToTheNewest(t *testing.T) {
	f := operatorRouterAs(t, auth.RoleAdmin)
	for i := 0; i < chatRetention; i++ {
		if _, err := f.s.Store.DB.Exec(`INSERT INTO assistant_chats(id, owner_id, scope_kind, created_at, updated_at) VALUES(?, 1, 'server', ?, ?)`,
			fmt.Sprintf("old-%03d", i), 1000+i, 1000+i); err != nil {
			t.Fatal(err)
		}
	}
	f.provider.queue(claudeAnswer("Fine."))
	id := conversationOf(f.ask(t, `{"question":"How is it?"}`))
	var count int
	if err := f.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM assistant_chats WHERE owner_id = 1`).Scan(&count); err != nil || count != chatRetention {
		t.Fatalf("%d chats kept (%v)", count, err)
	}
	var oldest, newest int
	_ = f.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM assistant_chats WHERE id = 'old-000'`).Scan(&oldest)
	_ = f.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM assistant_chats WHERE id = ?`, id).Scan(&newest)
	if oldest != 0 || newest != 1 {
		t.Fatalf("oldest %d, newest %d", oldest, newest)
	}
}

// A deployment's chats go with it.
func TestADeploymentsChatsGoWithIt(t *testing.T) {
	f := operatorRouterAs(t, auth.RoleAdmin)
	f.provider.queue(claudeAnswer("Fine."))
	id := conversationOf(f.ask(t, `{"question":"How is it?"}`))
	if _, err := f.s.Store.DB.Exec(`DELETE FROM deploy_projects WHERE id = ?`, f.projectID); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := f.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM assistant_chats WHERE id = ?`, id).Scan(&left); err != nil || left != 0 {
		t.Fatalf("%d chats left (%v)", left, err)
	}
}

// The masks every answer goes through, piece by piece.
func TestTheAnswerMasksAreWhatTheySay(t *testing.T) {
	for key, secret := range map[string]bool{
		"password": true, "dbPassword": true, "api_key": true, "apiKey": true, "privateKey": true, "SECRET": true,
		"clientSecret": true, "credentials": true, "dsn": true, "connectionString": true, "authorization": true,
		"Set-Cookie": true, "sessionToken": true, "name": false, "tokens": false, "secretStore": false, "hasPassword": true,
		"keyName": false, "sensitivity": false, "accessKeyId": false,
	} {
		if agentSecretField(key) != secret {
			t.Errorf("%s secret = %v", key, !secret)
		}
	}
	for text, random := range map[string]bool{
		"Zx8Kq2Lm9Pw4Rt7Yb3Nc6Vd1Hf5Jg0QsAa":                                    true,
		"sha256:4f9c2d6a8e1b3c5d7f9a0b2c4d6e8f0a1b3c5d7e9f0a2b4c6d8e0f1a3b5c7d": false,
		"/var/lib/just-dashboard/deployments/Shop2024/releases/42":              false,
		"DeploymentEnvironmentConfigurationSaveRequest":                         false,
		"0f8fad5b-d9cb-469f-a165-70867728950e":                                  false,
	} {
		if masked := agentMaskShapes(text) != text; masked != random {
			t.Errorf("%q masked = %v", text, masked)
		}
	}
	masker := deploy.NewDiagnosisMasker([]string{"value-of-a-secret"})
	got, _ := json.Marshal(agentMaskValue(map[string]any{"list": []any{"has value-of-a-secret in it"}, "password": []any{"a", "b"}, "count": 3.0}, masker))
	if strings.Contains(string(got), "value-of-a-secret") || strings.Contains(string(got), `"a"`) || !strings.Contains(string(got), `"count":3`) {
		t.Fatalf("masked = %s", got)
	}
}

// Taking a chat over with nothing typed carries it on from where the press that
// was running it put it down: the question it was answering is answered here.
func TestTakingAChatOverWithNothingToSayCarriesItOn(t *testing.T) {
	f := operatorRouterAs(t, auth.RoleAdmin)
	release := make(chan struct{})
	f.provider.queue(func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, e := range []map[string]any{
			{"type": "message_start", "message": map[string]any{"model": "claude-opus-5-5"}},
			{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}},
			{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "Looking"}},
		} {
			data, _ := json.Marshal(e)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e["type"], data)
		}
		w.(http.Flusher).Flush()
		<-release
	})
	server := httptest.NewServer(f.router)
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })
	res, err := http.Post(server.URL+fmt.Sprintf("/deploy/%d/assistant", f.projectID), "application/json", strings.NewReader(`{"question":"Why is it slow?"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	reader := bufio.NewScanner(res.Body)
	var id string
	for reader.Scan() {
		var line map[string]any
		_ = json.Unmarshal(reader.Bytes(), &line)
		if line["type"] == "start" {
			id = line["conversation"].(string)
		}
		if line["type"] == "text" {
			break
		}
	}
	if id == "" {
		t.Fatal("the first press never began")
	}
	f.provider.queue(claudeAnswer("It is slow because of the cache."))
	events := lines(t, f.do(http.MethodPost, fmt.Sprintf("/deploy/%d/assistant", f.projectID), `{"conversation":"`+id+`","takeOver":true}`))
	if conversationOf(events) != id || doneOf(t, events)["kept"] != true {
		t.Fatalf("the take-over = %v", events)
	}
	// The model was sent the question the first press was answering.
	sent, _ := json.Marshal(f.provider.last())
	if !strings.Contains(string(sent), "Why is it slow?") {
		t.Fatalf("the carried-on request did not hold the question: %s", sent)
	}
	read := f.do(http.MethodGet, "/assistant/chats/"+id, "")
	if !strings.Contains(read.Body.String(), "because of the cache") || strings.Count(read.Body.String(), `"type":"question"`) != 1 {
		t.Fatalf("lines = %s", read.Body.String())
	}
	// Nothing to carry on: a whole answer is not answered again.
	if events := lines(t, f.do(http.MethodPost, fmt.Sprintf("/deploy/%d/assistant", f.projectID), `{"conversation":"`+id+`","takeOver":true}`)); doneOf(t, events)["kept"] != true {
		t.Fatalf("a settled take-over = %v", events)
	}
	if bad := f.do(http.MethodPost, fmt.Sprintf("/deploy/%d/assistant", f.projectID), `{"conversation":"`+id+`"}`); bad.Code != http.StatusBadRequest {
		t.Fatalf("a press with nothing in it and no take-over = %d", bad.Code)
	}
}
