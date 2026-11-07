package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/assistant"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/mcp"
	"github.com/go-chi/chi/v5"
)

// The deployment's assistant over HTTP, against a provider answering as
// Anthropic's Messages API, with the whole router under it. What the person's mode
// holds runs nothing until they answer; what runs goes through the dashboard's own
// routes as them, audited by each; and what stays a person's cannot be reached.

const operatorSentinel = "health gate failed: GET /healthz answered 503 after 3 attempts"

// claudeCalls is an answer that writes text and ends on the tool calls given,
// each a name and its input.
func claudeCalls(text string, calls ...[2]any) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		events := []map[string]any{
			{"type": "message_start", "message": map[string]any{"model": "claude-opus-5-5", "usage": map[string]int{"input_tokens": 900}}},
			{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}},
			{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": text}},
		}
		for i, call := range calls {
			input, _ := json.Marshal(call[1])
			index := i + 1
			events = append(events,
				map[string]any{"type": "content_block_start", "index": index, "content_block": map[string]any{"type": "tool_use", "id": fmt.Sprintf("toolu_%02d", index), "name": call[0], "input": map[string]any{}}},
				map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(input)}},
			)
		}
		stop := "end_turn"
		if len(calls) > 0 {
			stop = "tool_use"
		}
		events = append(events,
			map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop}, "usage": map[string]int{"output_tokens": 40}},
			map[string]any{"type": "message_stop"},
		)
		sseEvents(w, events...)
	}
}

type operatorFixture struct {
	*assistantFixture
	projectID     int64
	environmentID int64
}

func operatorRouterAs(t *testing.T, role auth.Role) *operatorFixture {
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
	s.mountDeployRoutes(r)
	s.mountAssistantRoutes(r)
	// A call is made through the whole router, which this builds.
	s.Routes()
	// The person the fixture signs in as is a user, whose chats are saved.
	if _, err := s.Store.DB.Exec(`INSERT INTO users(id, username, password_hash, role, created_at) VALUES(1, 'operator', 'x', ?, 0)`, string(role)); err != nil {
		t.Fatal(err)
	}
	projectID, environmentID := seedCommittedDeployment(t, s, "shop",
		deploy.DraftSourceConfig{Kind: deploy.SourceGit, Mode: deploy.SourceModeGitURL, URL: "https://github.com/acme/shop.git", Ref: "main"},
		deploy.SourceIdentity{Kind: deploy.SourceGit, Revision: strings.Repeat("e", 40)})
	// The failure is the older run, so the reason reaches the model only
	// through a read: the prompt names the last run, which succeeded.
	for i, run := range []struct{ state, code, reason string }{
		{"failed", "health_gate_failed", operatorSentinel},
		{"succeeded", "", ""},
	} {
		if _, err := s.Store.DB.Exec(`INSERT INTO deploy_runs(project_id, environment_id, started_at, ended_at, status, state, operation, trigger, actor,
			requested_at, terminal_code, terminal_reason) VALUES(?, ?, ?, ?, ?, ?, 'deploy', 'git_push', 'github', ?, ?, ?)`,
			projectID, environmentID, 1000+i, 1100+i, run.state, run.state, 1000+i, run.code, run.reason); err != nil {
			t.Fatal(err)
		}
	}
	provider := &fakeProvider{}
	server := httptest.NewServer(provider)
	t.Cleanup(server.Close)
	s.assistant.options.AnthropicURL = server.URL
	f := &operatorFixture{assistantFixture: &assistantFixture{s: s, router: r, provider: provider, url: server.URL}, projectID: projectID, environmentID: environmentID}
	f.claude(t)
	return f
}

func (f *operatorFixture) ask(t *testing.T, body string) []map[string]any {
	t.Helper()
	rec := f.do(http.MethodPost, fmt.Sprintf("/deploy/%d/assistant", f.projectID), body)
	return lines(t, rec)
}

func doneOf(t *testing.T, events []map[string]any) map[string]any {
	t.Helper()
	done := ofType(events, "done")
	if len(done) != 1 {
		t.Fatalf("no done line in %v", events)
	}
	return done[0]
}

// In Ask every time a read runs nothing until the person decides, and a declined
// one sends the model the refusal and none of the data.
func TestAReadWaitsWhenTheModeSaysAsk(t *testing.T) {
	f := operatorRouterAs(t, auth.RoleAdmin)
	f.provider.queue(
		claudeCalls("Let me look at the recent runs.", [2]any{assistant.ToolRecentRuns, map[string]any{"limit": 5}}),
		claudeAnswer("I could not read the runs, so I cannot say why it failed."),
	)
	events := f.ask(t, `{"question":"Why is shop down?","mode":"ask"}`)
	done := doneOf(t, events)
	pending, _ := done["pending"].([]any)
	if len(pending) != 1 {
		t.Fatalf("the read did not wait for the person: %v", done)
	}
	call := pending[0].(map[string]any)
	if call["tool"] != assistant.ToolRecentRuns || call["label"] != "Read the last 5 runs" || call["id"] != "toolu_01" || call["kind"] != "read" {
		t.Fatalf("pending = %v", call)
	}
	first := f.provider.raw[0]
	if !strings.Contains(first, `"name":"recent_runs"`) || !strings.Contains(first, `"name":"call_operation"`) || strings.Contains(first, operatorSentinel) {
		t.Fatalf("the first request: %s", first)
	}
	conversation := ofType(events, "start")[0]["conversation"].(string)

	events = f.ask(t, `{"conversation":"`+conversation+`","decisions":{"toolu_01":false},"mode":"ask"}`)
	called := ofType(events, "called")
	if len(called) != 1 || called[0]["declined"] != true {
		t.Fatalf("called = %v", called)
	}
	sent := f.provider.raw[len(f.provider.raw)-1]
	if !strings.Contains(sent, "The person declined this") || strings.Contains(sent, operatorSentinel) {
		t.Fatalf("a declined read reached the model: %s", sent)
	}
	detail, status := f.audit(t, "deploy.assistant.tool")
	if status != http.StatusOK || !strings.Contains(detail, `"declined":["recent_runs"]`) || !strings.Contains(detail, `"allowed":[]`) {
		t.Fatalf("audit = %d %s", status, detail)
	}
}

// By default a read runs on its own and reaches the model, and the model's answer
// quotes it, in one press.
func TestAReadRunsOnItsOwnByDefaultAndTheModelQuotesIt(t *testing.T) {
	f := operatorRouterAs(t, auth.RoleAdmin)
	quoted := regexp.MustCompile(`health gate failed: [^"\\]+`)
	f.provider.queue(
		claudeCalls("Let me look at the recent runs.", [2]any{assistant.ToolRecentRuns, map[string]any{}}),
		func(w http.ResponseWriter) {
			f.provider.mu.Lock()
			last := f.provider.raw[len(f.provider.raw)-1]
			f.provider.mu.Unlock()
			claudeAnswer("Run #1 failed: " + quoted.FindString(last))(w)
		},
	)
	events := f.ask(t, `{"question":"Why did the last deploy fail?"}`)
	if ofType(events, "start")[0]["mode"] != "changes" {
		t.Fatalf("the default mode = %v", ofType(events, "start")[0])
	}
	if done := doneOf(t, events); done["pending"] != nil {
		t.Fatalf("a read waited in the default mode: %v", done)
	}
	running := ofType(events, "call")
	if len(running) != 1 || running[0]["call"].(map[string]any)["label"] != "Read the last 10 runs" {
		t.Fatalf("call = %v", running)
	}
	called := ofType(events, "called")
	if len(called) != 1 || called[0]["ok"] != true || called[0]["said"] != "2 runs" {
		t.Fatalf("called = %v", called)
	}
	sent := f.provider.raw[len(f.provider.raw)-1]
	if !strings.Contains(sent, operatorSentinel) || !strings.Contains(sent, "health_gate_failed") {
		t.Fatalf("the read did not reach the model: %s", sent)
	}
	text := ""
	for _, e := range ofType(events, "text") {
		text += e["text"].(string)
	}
	if !strings.Contains(text, "Run #1 failed: "+operatorSentinel) {
		t.Fatalf("the answer does not quote what was read: %q", text)
	}
	detail, _ := f.audit(t, "deploy.assistant.ask")
	if !strings.Contains(detail, `"ran":["recent_runs"]`) || !strings.Contains(detail, `"outcome":"answered"`) || !strings.Contains(detail, `"mode":"changes"`) {
		t.Fatalf("audit = %s", detail)
	}
}

// A run's reason carries what its commands printed, so a secret variable's value in
// it is masked before the model is sent it: in the prompt's last run and in the
// runs it reads.
func TestAReadMasksTheEnvironmentsSecrets(t *testing.T) {
	f := operatorRouterAs(t, auth.RoleAdmin)
	const secret = "sk-live-operator-never-sent-77"
	put := fmt.Sprintf(`{"revision":1,"value":%q,"sensitivity":"secret","scopes":["build"]}`, secret)
	if rec := f.do(http.MethodPut, fmt.Sprintf("/deploy/%d/environments/%d/variables/API_KEY", f.projectID, f.environmentID), put); rec.Code != http.StatusOK {
		t.Fatalf("variable = %d %s", rec.Code, rec.Body.String())
	}
	if _, err := f.s.Store.DB.Exec(`INSERT INTO deploy_runs(project_id, environment_id, started_at, ended_at, status, state, operation, trigger, actor,
		requested_at, terminal_code, terminal_reason) VALUES(?, ?, 2000, 2100, 'failed', 'failed', 'deploy', 'manual', 'operator', 2000, 'release_task_failed', ?)`,
		f.projectID, f.environmentID, "migrate printed API_KEY="+secret+" and exited 1"); err != nil {
		t.Fatal(err)
	}
	f.provider.queue(
		claudeCalls("Reading the runs.",
			[2]any{assistant.ToolRecentRuns, map[string]any{"limit": 5}},
			[2]any{assistant.ToolCallOperation, map[string]any{"name": "deploy_runs", "reason": "Reading the runs through the route."}}),
		claudeAnswer("The migration failed."),
	)
	f.ask(t, `{"question":"Why did it fail?"}`)
	if len(f.provider.raw) != 2 {
		t.Fatalf("%d requests reached the model", len(f.provider.raw))
	}
	for i, sent := range f.provider.raw {
		if strings.Contains(sent, secret) || !strings.Contains(sent, "migrate printed API_KEY=") {
			t.Fatalf("request %d was sent unmasked or without the run: %s", i, sent)
		}
	}
	// And the page is shown what the model was: the route's answer masked.
	sent := f.provider.raw[1]
	if strings.Count(sent, deploy.DiagnosisMasked) < 2 {
		t.Fatalf("the route's answer was not masked: %s", sent)
	}
}

func operatorLive(t *testing.T, f *operatorFixture) {
	t.Helper()
	if _, err := f.s.Store.DB.Exec(`INSERT INTO deploy_releases(project_id, environment_id, release_number, state, created_at, activated_at)
		VALUES(?, ?, 1, 'live', 1, 1)`, f.projectID, f.environmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Store.DB.Exec(`UPDATE deploy_environments SET live_release_id = (SELECT id FROM deploy_releases WHERE environment_id = ?) WHERE id = ?`,
		f.environmentID, f.environmentID); err != nil {
		t.Fatal(err)
	}
}

func runCount(t *testing.T, f *operatorFixture) int {
	t.Helper()
	var runs int
	if err := f.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_runs`).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	return runs
}

var restart = [2]any{assistant.ToolCallOperation, map[string]any{
	"name": "deployment_run_create", "reason": "Its health check fails after a config change.",
	"arguments": map[string]any{"body": map[string]any{"operation": "restart"}},
}}

// By default a change waits for the person, runs nothing until they allow it, and
// then runs through the route the project's own button calls, audited as that route
// is and under the assistant's name. The model is told, and the page gets a shortcut.
func TestAChangeWaitsForThePersonAndThenRunsThroughItsRoute(t *testing.T) {
	f := operatorRouterAs(t, auth.RoleAdmin)
	operatorLive(t, f)
	before := runCount(t, f)
	f.provider.queue(claudeCalls("It needs a restart.", restart), claudeAnswer("Restarted; run started."))
	events := f.ask(t, `{"question":"Restart it"}`)
	pending, _ := doneOf(t, events)["pending"].([]any)
	if len(pending) != 1 {
		t.Fatalf("the change did not wait: %v", events)
	}
	call := pending[0].(map[string]any)
	if call["kind"] != "destructive" || call["operation"] != "deployment_run_create" || call["reason"] != "Its health check fails after a config change." ||
		!strings.HasPrefix(call["route"].(string), "POST /deploy/{id}/environments/{env}/runs") {
		t.Fatalf("pending = %v", call)
	}
	if got := runCount(t, f); got != before {
		t.Fatalf("a waiting change queued a run: %d → %d", before, got)
	}
	conversation := ofType(events, "start")[0]["conversation"].(string)
	events = f.ask(t, `{"conversation":"`+conversation+`","decisions":{"toolu_01":true}}`)
	if got := runCount(t, f); got != before+1 {
		t.Fatalf("the allowed restart queued %d runs: %v", got-before, events)
	}
	called := ofType(events, "called")
	if len(called) != 1 || called[0]["ok"] != true {
		t.Fatalf("called = %v", called)
	}
	change, _ := called[0]["change"].(map[string]any)
	if change == nil || !strings.HasPrefix(change["label"].(string), "Run #") || !strings.Contains(change["href"].(string), fmt.Sprintf("/deploy/%d/runs/", f.projectID)) {
		t.Fatalf("change = %v", called[0])
	}
	if detail, _ := f.audit(t, "deploy.run.request"); !strings.Contains(detail, `"operation":"restart"`) {
		t.Fatalf("the restart was audited as %s", detail)
	}
	// The route's own entry is the person's, marked as the assistant's.
	var actor, username, key string
	if err := f.s.Store.DB.QueryRow(`SELECT actor, username, COALESCE(key_name, '') FROM audit_log WHERE action = 'deploy.run.request' ORDER BY id DESC LIMIT 1`).Scan(&actor, &username, &key); err != nil {
		t.Fatal(err)
	}
	if actor != "assistant" || username != "operator" || key != "" {
		t.Fatalf("the audit entry says it was %q by %q (key %q)", actor, username, key)
	}
}

// Full access runs the same change without asking, in the one press.
func TestFullAccessRunsAChangeWithoutAsking(t *testing.T) {
	f := operatorRouterAs(t, auth.RoleAdmin)
	operatorLive(t, f)
	before := runCount(t, f)
	f.provider.queue(claudeCalls("Restarting.", restart), claudeAnswer("Done."))
	events := f.ask(t, `{"question":"Restart it","mode":"full"}`)
	if done := doneOf(t, events); done["pending"] != nil {
		t.Fatalf("full access waited: %v", done)
	}
	if got := runCount(t, f); got != before+1 {
		t.Fatalf("queued %d runs: %v", got-before, events)
	}
	if detail, _ := f.audit(t, "deploy.assistant.ask"); !strings.Contains(detail, `"mode":"full"`) || !strings.Contains(detail, `"ran":["deployment_run_create"]`) {
		t.Fatalf("audit = %s", detail)
	}
}

// Full access is not more than the person's role: the route refuses, and the model
// is told so, and nothing runs.
func TestFullAccessIsHeldToThePersonsRole(t *testing.T) {
	f := operatorRouterAs(t, auth.RoleLimited)
	operatorLive(t, f)
	before := runCount(t, f)
	f.provider.queue(claudeCalls("Restarting.", restart), claudeAnswer("Your role cannot restart it."))
	f.ask(t, `{"question":"Restart it","mode":"full"}`)
	if got := runCount(t, f); got != before {
		t.Fatalf("a role that may not restart queued one: %d → %d", before, got)
	}
	sent := f.provider.raw[len(f.provider.raw)-1]
	if !strings.Contains(sent, `"is_error":true`) {
		t.Fatalf("the refusal did not reach the model: %s", sent)
	}
}

// A configuration's change is spelled out, and its shortcut names the page that part
// is set on.
func TestAConfigurationChangeSaysWhatIsDifferentAndWhereToSeeIt(t *testing.T) {
	f := operatorRouterAs(t, auth.RoleAdmin)
	path := fmt.Sprintf("/deploy/%d/environments/%d/configuration", f.projectID, f.environmentID)
	rec := f.do(http.MethodGet, path, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("configuration = %d %s", rec.Code, rec.Body.String())
	}
	var configuration map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &configuration); err != nil {
		t.Fatal(err)
	}
	build := configuration["build"].(map[string]any)
	build["outputDirectory"] = "dist-by-assistant"
	f.provider.queue(
		claudeCalls("Changing the output directory.", [2]any{assistant.ToolCallOperation, map[string]any{
			"name": "deployment_configuration_save", "reason": "As asked.",
			"arguments": map[string]any{"body": map[string]any{"revision": configuration["revision"], "build": build, "runtime": configuration["runtime"]}},
		}}),
		claudeAnswer("Saved."),
	)
	events := f.ask(t, `{"question":"Set the output directory","mode":"full"}`)
	called := ofType(events, "called")
	if len(called) != 1 {
		t.Fatalf("called = %v", events)
	}
	if called[0]["ok"] != true {
		t.Fatalf("the save was refused: %v (%s)", called[0], f.provider.raw[len(f.provider.raw)-1])
	}
	change := called[0]["change"].(map[string]any)
	if change["label"] != "Build" || change["href"] != fmt.Sprintf("/deploy/%d/settings/build", f.projectID) {
		t.Fatalf("change = %v", change)
	}
	raw, _ := json.Marshal(change["diff"])
	if !strings.Contains(string(raw), "dist-by-assistant") || !strings.Contains(string(raw), "build.outputDirectory") {
		t.Fatalf("diff = %s", raw)
	}
}

// call_operation is about the conversation's deployment. Its id is filled in, naming
// another is pointed at api_request, and a route of another's environment is refused.
func TestAConversationReachesOneDeployment(t *testing.T) {
	f := operatorRouterAs(t, auth.RoleAdmin)
	f.provider.queue(
		claudeCalls("Reading another deployment.",
			[2]any{assistant.ToolCallOperation, map[string]any{"name": "deploy_get", "reason": "x", "arguments": map[string]any{"id": f.projectID + 1000}}},
			[2]any{assistant.ToolCallOperation, map[string]any{"name": "deploy_runs", "reason": "x", "arguments": map[string]any{"limit": 2}}},
			[2]any{assistant.ToolCallOperation, map[string]any{"name": "deployment_configuration", "reason": "x", "arguments": map[string]any{"env": f.environmentID + 1000}}}),
		claudeAnswer("One read answered."),
	)
	events := f.ask(t, `{"question":"Look around","mode":"full"}`)
	called := ofType(events, "called")
	if len(called) != 3 || called[0]["ok"] != false || called[1]["ok"] != true || called[2]["ok"] != false {
		t.Fatalf("called = %v", called)
	}
	if !strings.Contains(called[0]["said"].(string), "api_request") {
		t.Fatalf("another deployment's id = %v", called[0])
	}
}

// Past a number of steps what it asks next waits for the person whatever the mode,
// so a loop spends nothing more without somebody looking.
func TestALongTurnPausesForThePerson(t *testing.T) {
	f := operatorRouterAs(t, auth.RoleAdmin)
	answers := []func(http.ResponseWriter){}
	for range agentSteps {
		answers = append(answers, claudeCalls("Reading.", [2]any{assistant.ToolRecentRuns, map[string]any{"limit": 1}}))
	}
	f.provider.queue(answers...)
	f.s.assistant.limit = httpx.NewLimiter(6000, 100)
	events := f.ask(t, `{"question":"Loop","mode":"full"}`)
	done := doneOf(t, events)
	if done["paused"] != true || len(done["pending"].([]any)) != 1 {
		t.Fatalf("done = %v", done)
	}
	if len(f.provider.raw) != agentSteps {
		t.Fatalf("%d requests reached the model", len(f.provider.raw))
	}
}

// A person's answer is not refused because the steps before it spent the budget a
// moment ago: it waits a little, as a step does. That refusal stranded what was waiting.
func TestAnAnswerWaitsForTheBudgetRatherThanBeingRefused(t *testing.T) {
	f := operatorRouterAs(t, auth.RoleAdmin)
	operatorLive(t, f)
	f.s.assistant.limit = httpx.NewLimiter(60, 1)
	f.provider.queue(claudeCalls("Restarting.", restart), claudeAnswer("Done."))
	events := f.ask(t, `{"question":"Restart it"}`)
	conversation := ofType(events, "start")[0]["conversation"].(string)
	events = f.ask(t, `{"conversation":"`+conversation+`","decisions":{"toolu_01":true}}`)
	if called := ofType(events, "called"); len(called) != 1 || called[0]["ok"] != true {
		t.Fatalf("the answer was refused: %v", events)
	}
}

// A schedule that runs a command is the person's to allow, as any change is, and
// full access writes it: what the assistant used to be refused is asked instead.
func TestAScheduleThatRunsACommandIsAskedForNotRefused(t *testing.T) {
	f := operatorRouterAs(t, auth.RoleAdmin)
	f.s.assistant.limit = httpx.NewLimiter(6000, 100)
	schedule := func(action string, config map[string]any) [2]any {
		return [2]any{assistant.ToolCallOperation, map[string]any{"name": "deployment_schedule_create", "reason": "x", "arguments": map[string]any{
			"body": map[string]any{"name": "nightly", "expression": "0 3 * * *", "timezone": "UTC", "enabled": true,
				"steps": []any{map[string]any{"action": action, "config": config}}},
		}}}
	}
	command := schedule("container_command", map[string]any{"containerId": "some-container", "argv": []string{"sh", "-c", "env"}})
	f.provider.queue(claudeCalls("Scheduling.", command))
	events := f.ask(t, `{"question":"Schedule it","mode":"changes"}`)
	if pending, _ := doneOf(t, events)["pending"].([]any); len(pending) != 1 || len(ofType(events, "called")) != 0 {
		t.Fatalf("a command's schedule did not wait: %v", events)
	}
	f.provider.queue(claudeCalls("Scheduling.", command), claudeAnswer("Done."))
	events = f.ask(t, `{"question":"Schedule it","mode":"full"}`)
	called := ofType(events, "called")
	if len(called) != 1 || strings.Contains(fmt.Sprint(called[0]["said"]), "Automation page") {
		t.Fatalf("full access was refused by the old guard: %v", called)
	}
	var saved int
	if err := f.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_schedules`).Scan(&saved); err != nil || saved != 1 {
		t.Fatalf("full access saved %d schedules (%v)", saved, err)
	}
}

// What is masked is all of the deployment: a value since rotated, and a shared variable
// its references can read, as well as what is set now.
func TestWhatIsSentIsMaskedAcrossTheDeployment(t *testing.T) {
	f := operatorRouterAs(t, auth.RoleAdmin)
	const rotated, shared = "sk-live-rotated-away-old-value-11", "sk-live-shared-across-project-22"
	variable := fmt.Sprintf("/deploy/%d/environments/%d/variables/API_KEY", f.projectID, f.environmentID)
	if rec := f.do(http.MethodPut, variable, fmt.Sprintf(`{"revision":1,"value":%q,"sensitivity":"secret","scopes":["runtime"]}`, rotated)); rec.Code != http.StatusOK {
		t.Fatalf("variable = %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.do(http.MethodPut, variable, `{"revision":2,"value":"sk-live-the-current-one-is-33","sensitivity":"secret","scopes":["runtime"]}`); rec.Code != http.StatusOK {
		t.Fatalf("variable = %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.do(http.MethodPut, fmt.Sprintf("/deploy/%d/shared-variables/SHARED_KEY", f.projectID), fmt.Sprintf(`{"value":%q,"sensitivity":"secret"}`, shared)); rec.Code != http.StatusCreated {
		t.Fatalf("shared = %d %s", rec.Code, rec.Body.String())
	}
	if _, err := f.s.Store.DB.Exec(`INSERT INTO deploy_runs(project_id, environment_id, started_at, ended_at, status, state, operation, trigger, actor,
		requested_at, terminal_code, terminal_reason) VALUES(?, ?, 2000, 2100, 'failed', 'failed', 'deploy', 'manual', 'operator', 2000, 'task_failed', ?)`,
		f.projectID, f.environmentID, "old="+rotated+" shared="+shared); err != nil {
		t.Fatal(err)
	}
	f.provider.queue(claudeCalls("Reading.", [2]any{assistant.ToolRecentRuns, map[string]any{"limit": 5}}), claudeAnswer("It failed."))
	f.ask(t, `{"question":"Why?"}`)
	sent := f.provider.raw[len(f.provider.raw)-1]
	if strings.Contains(sent, rotated) || strings.Contains(sent, shared) || !strings.Contains(sent, "old=") {
		t.Fatalf("sent unmasked: %s", sent)
	}
}

// A route's answer too long to be read whole is not sent in part, since it could not
// be masked; the model is told to ask for less.
func TestAnAnswerTooLongToMaskIsNotSent(t *testing.T) {
	rec := &boundedRecorder{header: http.Header{}, status: http.StatusOK, limit: 16}
	if _, err := rec.Write([]byte(`{"value":"sk-live-a-secret-past-the-cut"}`)); err != nil {
		t.Fatal(err)
	}
	text, whole := agentAnswer(rec, deploy.NewDiagnosisMasker([]string{"sk-live-a-secret-past-the-cut"}))
	if whole || strings.Contains(text, "sk-live") || !strings.Contains(text, "too long") {
		t.Fatalf("answer = %q %v", text, whole)
	}
	broken := &boundedRecorder{header: http.Header{}, status: http.StatusOK}
	if _, err := broken.Write([]byte(`not json sk-live-a-secret`)); err != nil {
		t.Fatal(err)
	}
	if text, whole := agentAnswer(broken, deploy.NewDiagnosisMasker(nil)); whole || strings.Contains(text, "sk-live") {
		t.Fatalf("answer = %q %v", text, whole)
	}
}

// Steps that read nothing are still steps: a model that only looks operations up
// pauses like any other, and what it asks next waits.
func TestLookingThingsUpCountsTowardsTheSteps(t *testing.T) {
	f := operatorRouterAs(t, auth.RoleAdmin)
	f.s.assistant.limit = httpx.NewLimiter(6000, 100)
	answers := []func(http.ResponseWriter){}
	for range agentSteps {
		answers = append(answers, claudeCalls("Looking.", [2]any{assistant.ToolDescribeOperation, map[string]any{"name": "nothing_at_all"}}))
	}
	f.provider.queue(answers...)
	done := doneOf(t, f.ask(t, `{"question":"Loop","mode":"ask"}`))
	if done["paused"] != true {
		t.Fatalf("done = %v", done)
	}
	if len(f.provider.raw) != agentSteps {
		t.Fatalf("%d requests reached the model", len(f.provider.raw))
	}
}

// Who answers and what it may do alone are a person's to choose: an ordinary key is
// refused, and a key that could would be handed what everybody asks.
func TestAKeyCannotChooseWhoAnswers(t *testing.T) {
	s := testServer(t)
	root := s.Routes().(chi.Routes)
	for _, route := range [][2]string{
		{http.MethodPut, "/api/v1/assistant/preferences"},
		{http.MethodPut, "/api/v1/assistant/providers/{preset}"},
		{http.MethodPost, "/api/v1/assistant/providers/{preset}/activate"},
		{http.MethodDelete, "/api/v1/assistant/providers/{preset}"},
		{http.MethodPost, "/api/v1/assistant/models"},
		{http.MethodPut, "/api/v1/databases/assistant"},
		{http.MethodDelete, "/api/v1/databases/assistant"},
	} {
		found := false
		_ = chi.Walk(root, func(method, pattern string, _ http.Handler, middlewares ...func(http.Handler) http.Handler) error {
			if method == route[0] && pattern == route[1] {
				found = requiresSession(middlewares)
			}
			return nil
		})
		if !found {
			t.Errorf("%s %s does not require a session", route[0], route[1])
		}
	}
}

// A press goes no further than the session that made it: revoked partway, what it asks
// next does not run.
func TestAPressStopsWithItsSession(t *testing.T) {
	f := operatorRouterAs(t, auth.RoleAdmin)
	operatorLive(t, f)
	before := runCount(t, f)
	cookie := signInAs(t, f.s, "someone", auth.RoleAdmin)
	var userID int64
	if err := f.s.Store.DB.QueryRow(`SELECT id FROM users WHERE username = 'someone'`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	f.provider.queue(func(w http.ResponseWriter) {
		if err := f.s.Auth.RevokeAllSessions(context.Background(), userID); err != nil {
			t.Error(err)
		}
		claudeCalls("Restarting.", restart)(w)
	}, claudeAnswer("Never reached."))
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/deploy/%d/assistant", f.projectID), strings.NewReader(`{"question":"Restart it","mode":"full"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", cookie)
	req.Header.Set(httpx.CSRFHeader, "1")
	req.RemoteAddr = "127.0.0.1:1234"
	rec := httptest.NewRecorder()
	f.s.Routes().ServeHTTP(rec, req)
	if got := runCount(t, f); got != before {
		t.Fatalf("a revoked session queued a run: %d → %d (%s)", before, got, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "session this was asked under has ended") {
		t.Fatalf("answer = %s", rec.Body.String())
	}
}

// Asking while a change waits declines it, in words the model reads.
func TestAQuestionDeclinesAChangeThatWaits(t *testing.T) {
	f := operatorRouterAs(t, auth.RoleAdmin)
	operatorLive(t, f)
	before := runCount(t, f)
	f.provider.queue(claudeCalls("Restarting.", restart), claudeAnswer("Leaving it alone."))
	events := f.ask(t, `{"question":"Restart it"}`)
	conversation := ofType(events, "start")[0]["conversation"].(string)
	f.ask(t, `{"conversation":"`+conversation+`","question":"Actually, do not"}`)
	if got := runCount(t, f); got != before {
		t.Fatalf("a declined change ran: %d → %d", before, got)
	}
}

// A call for anything but its tools runs nothing, and the model is told so and
// goes on; the person is not shown the model's slip. A call for an operation not
// offered is told so too.
func TestACallOutsideItsToolsIsAnsweredWithWhy(t *testing.T) {
	f := operatorRouterAs(t, auth.RoleAdmin)
	f.s.assistant.limit = httpx.NewLimiter(6000, 100)
	f.provider.queue(claudeCalls("", [2]any{"run_shell", map[string]any{"command": "id"}}), claudeAnswer("I have no shell."))
	events := f.ask(t, `{"question":"Run id"}`)
	if len(ofType(events, "call")) != 0 || len(ofType(events, "called")) != 0 || len(ofType(events, "error")) != 0 {
		t.Fatalf("the page was shown the slip: %v", events)
	}
	if doneOf(t, events)["kept"] != true {
		t.Fatalf("the turn did not go on: %v", events)
	}
	if sent := f.provider.raw[len(f.provider.raw)-1]; !strings.Contains(sent, `"is_error":true`) || !strings.Contains(sent, "api_request") {
		t.Fatalf("the model was not told: %s", sent)
	}
	f.provider.queue(
		claudeCalls("Trying.", [2]any{assistant.ToolCallOperation, map[string]any{"name": "nothing_at_all", "reason": "x"}}),
		claudeAnswer("I cannot."),
	)
	events = f.ask(t, `{"question":"Try","mode":"full"}`)
	called := ofType(events, "called")
	if len(called) != 1 || called[0]["ok"] != false || !strings.Contains(called[0]["said"].(string), "no operation") {
		t.Fatalf("nothing_at_all = %v", events)
	}
}

// Letting Just Hub sign an account in weakens invariant 2 for that account, so
// it is the person's own press: the assistant is offered neither the opt-in,
// nor turning it off, nor an administrator's removal of someone's choice.
func TestTheAssistantIsNotOfferedHubSignIn(t *testing.T) {
	s := testServer(t)
	s.Routes()
	cat := s.apiCatalogue()
	for _, key := range []string{"PUT /account/hub-signin", "DELETE /account/hub-signin", "DELETE /dashboard/hub/signin/{userId}"} {
		route := cat.byKey[key]
		if route == nil {
			t.Fatalf("the catalogue has no %s", key)
		}
		if route.Excluded == "" || !strings.Contains(route.Excluded, "invariant 2") {
			t.Errorf("%s is offered to the assistant (excluded: %q)", key, route.Excluded)
		}
	}
}

// The catalogue is every route under /deploy the person could make, but for what no
// request of the assistant's makes; every line that names a route names a real one,
// and every change says where it is shown.
func TestTheCatalogueIsTheRoutesItSaysItIs(t *testing.T) {
	s := testServer(t)
	root := s.Routes().(chi.Routes)
	cat := s.agentCatalogue()
	if cat.err != nil {
		t.Fatalf("the catalogue cannot be built: %v", cat.err)
	}
	routes := map[string]bool{}
	handlers := map[string]bool{}
	_ = chi.Walk(root, func(method, pattern string, h http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes[method+" "+strings.TrimSuffix(strings.TrimPrefix(pattern, "/api/v1"), "/")] = true
		handlers[handlerName(h)] = true
		return nil
	})
	for route := range agentExcludedRoutes {
		if !routes[route] {
			t.Errorf("agentExcludedRoutes names %s, which the router does not have", route)
		}
	}
	for route := range agentReadingPosts {
		if !routes[route] {
			t.Errorf("agentReadingPosts names %s, which the router does not have", route)
		}
	}
	for handler := range agentStreamHandlers {
		if !handlers[handler] {
			t.Errorf("agentStreamHandlers names %s, which no route has", handler)
		}
	}
	offered := map[string]bool{}
	for _, op := range cat.ops {
		offered[op.Method+" "+op.Pattern] = true
		for _, closed := range []string{"/assistant", "/hooks/", "/stream"} {
			if strings.Contains(op.Pattern, closed) {
				t.Errorf("%s %s is offered and no request of the assistant's makes it", op.Method, op.Pattern)
			}
		}
		if op.Grant != mcp.GrantRead && op.offered(agentScopeDeployment) {
			if _, listed := agentPageOf(op.Pattern); !listed {
				t.Errorf("%s %s changes something and no line says where it is shown (agentPages)", op.Method, op.Pattern)
			}
		}
	}
	// What the old narrowing kept from it is the person's to allow now.
	for _, route := range []string{
		"GET /deploy/{id}/environments/{env}/variables", "GET /deploy/{id}/environments/{env}/variables/{name}/reveal",
		"GET /deploy/credentials", "POST /deploy/{id}/duplicate", "POST /deploy/drafts/{draft}/commit",
		"POST /deploy/{id}/environments/{env}/variables/{name}/generate", "DELETE /deploy/{id}/permanent",
	} {
		if !offered[route] {
			t.Errorf("%s is not offered", route)
		}
	}
	if offered["POST /deploy/{id}/runs/{run}/diagnosis"] || offered["POST /deploy/{id}/assistant"] {
		t.Error("the assistant can ask itself")
	}
}

// A draft's conversation saves a draft and reads back, without asking in any mode,
// since nothing runs until it is committed; committing it is pressing Deploy, which
// it asks for unless the mode is full access.
func TestADraftsConversationConfiguresItWithoutAsking(t *testing.T) {
	f := operatorRouterAs(t, auth.RoleAdmin)
	rec := f.do(http.MethodPost, "/deploy/drafts", `{}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("draft = %d %s", rec.Code, rec.Body.String())
	}
	var draft struct {
		ID       string `json:"id"`
		Revision int64  `json:"revision"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &draft); err != nil {
		t.Fatal(err)
	}
	f.provider.queue(
		claudeCalls("Naming it.", [2]any{assistant.ToolCallOperation, map[string]any{
			"name": "deployment_draft_save", "reason": "Naming it.",
			"arguments": map[string]any{"body": map[string]any{"revision": draft.Revision, "step": "intent", "intent": map[string]any{"name": "shop-by-assistant", "profile": "web"}}},
		}}),
		claudeCalls("Trying to deploy.", [2]any{assistant.ToolCallOperation, map[string]any{"name": "deployment_draft_commit", "reason": "x"}}),
		claudeAnswer("Named. Press Deploy when you are ready."),
	)
	stream := f.do(http.MethodPost, "/deploy/drafts/"+draft.ID+"/assistant", `{"question":"Set it up","mode":"ask"}`)
	events := lines(t, stream)
	called := ofType(events, "called")
	if len(called) != 1 || called[0]["ok"] != true {
		t.Fatalf("called = %v (%s)", called, stream.Body.String())
	}
	pending, _ := doneOf(t, events)["pending"].([]any)
	if len(pending) != 1 || pending[0].(map[string]any)["operation"] != "deployment_draft_commit" {
		t.Fatalf("pressing Deploy did not wait for the person: %v", events)
	}
	got := f.do(http.MethodGet, "/deploy/drafts/"+draft.ID, "")
	if !strings.Contains(got.Body.String(), "shop-by-assistant") {
		t.Fatalf("the draft was not saved: %s", got.Body.String())
	}
	first := f.provider.raw[0]
	if !strings.Contains(first, "deployment_draft_save") || strings.Contains(first, "deployment_run_create") || strings.Contains(first, `"name":"recent_runs"`) ||
		!strings.Contains(first, `"name":"api_request"`) {
		t.Fatalf("the draft's conversation was offered the wrong tools: %s", first)
	}
}

// The assistant is a session's, refused to a role without service.control, and needs
// the provider set up.
func TestTheAssistantAsksForItsCapabilityAndAProvider(t *testing.T) {
	viewer := operatorRouterAs(t, auth.RoleReadOnly)
	if rec := viewer.do(http.MethodPost, fmt.Sprintf("/deploy/%d/assistant", viewer.projectID), `{"question":"hi"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("a viewer = %d %s", rec.Code, rec.Body.String())
	}
	off := operatorRouterAs(t, auth.RoleAdmin)
	if err := off.s.Store.SetSetting(t.Context(), assistantSettingKey, ""); err != nil {
		t.Fatal(err)
	}
	if rec := off.do(http.MethodPost, fmt.Sprintf("/deploy/%d/assistant", off.projectID), `{"question":"hi"}`); rec.Code != http.StatusConflict || errCode(rec) != "assistant_off" {
		t.Fatalf("no provider = %d %s", rec.Code, rec.Body.String())
	}
	if rec := off.do(http.MethodPost, fmt.Sprintf("/deploy/%d/assistant", off.projectID), `{"question":"hi","decisions":{}}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("a question and decisions together = %d", rec.Code)
	}
	if rec := off.do(http.MethodPost, fmt.Sprintf("/deploy/%d/assistant", off.projectID), `{"question":"hi","mode":"godmode"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("an unknown mode = %d", rec.Code)
	}
}

// Several providers can be saved, one is active, and every page's assistant asks that
// one; a key is never in an answer and a blank one keeps what is saved.
func TestSeveralProvidersOneActive(t *testing.T) {
	f := assistantRouterAs(t, auth.RoleAdmin)
	f.claude(t)
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			p := &httpx.Principal{User: &auth.User{ID: 1, Username: "operator"}, Role: auth.RoleAdmin, Kind: "session", IP: "127.0.0.1"}
			next.ServeHTTP(w, req.WithContext(httpx.WithPrincipal(req.Context(), p)))
		})
	})
	router.Use(httpx.AuditMutations(f.s.Audit))
	f.s.mountAssistantRoutes(router)
	f.router = router

	view := f.do(http.MethodGet, "/assistant", "")
	if view.Code != http.StatusOK || strings.Contains(view.Body.String(), "sk-ant-test-key") {
		t.Fatalf("view = %d %s", view.Code, view.Body.String())
	}
	var read struct {
		Preset   string `json:"preset"`
		Tools    bool   `json:"tools"`
		Profiles []struct {
			Preset string `json:"preset"`
			Active bool   `json:"active"`
		} `json:"profiles"`
		DefaultMode string `json:"defaultMode"`
	}
	_ = json.Unmarshal(view.Body.Bytes(), &read)
	if read.Preset != assistant.PresetAnthropic || len(read.Profiles) != 1 || !read.Tools || read.DefaultMode != "changes" {
		t.Fatalf("read = %+v", read)
	}
	// A local server, whose models it lists itself; it does not become active.
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":"qwen"},{"id":"llama"}]}`)
	}))
	t.Cleanup(local.Close)
	listed := f.do(http.MethodPost, "/assistant/models", fmt.Sprintf(`{"preset":"custom","baseUrl":%q}`, local.URL+"/v1"))
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"llama"`) {
		t.Fatalf("models = %d %s", listed.Code, listed.Body.String())
	}
	saved := f.do(http.MethodPut, "/assistant/providers/custom", fmt.Sprintf(`{"model":"qwen","baseUrl":%q,"toolCalls":true}`, local.URL+"/v1"))
	if saved.Code != http.StatusOK {
		t.Fatalf("save = %d %s", saved.Code, saved.Body.String())
	}
	_ = json.Unmarshal(saved.Body.Bytes(), &read)
	if read.Preset != assistant.PresetAnthropic || len(read.Profiles) != 2 {
		t.Fatalf("saving became active: %+v", read)
	}
	// Activating it is what every page reads.
	on := f.do(http.MethodPost, "/assistant/providers/custom/activate", `{"model":"llama"}`)
	if on.Code != http.StatusOK {
		t.Fatalf("activate = %d %s", on.Code, on.Body.String())
	}
	settings, err := f.s.assistantSettings(t.Context())
	if err != nil || settings.Provider != assistant.ProviderOpenAI || settings.Model != "llama" || !settings.ToolCalls {
		t.Fatalf("settings = %+v, %v", settings, err)
	}
	if detail, _ := f.audit(t, "assistant.provider.activate"); !strings.Contains(detail, `"model":"llama"`) {
		t.Fatalf("audit = %s", detail)
	}
	// The stored keyring is sealed; nothing of the key shows.
	var sealed string
	_ = f.s.Store.DB.QueryRow(`SELECT value FROM settings WHERE key = ?`, assistantRingKey).Scan(&sealed)
	if sealed == "" || strings.Contains(sealed, "sk-ant-test-key") {
		t.Fatalf("the keyring was stored as %q", sealed)
	}
	// Removing it hands back to Claude, with Claude's own key.
	off := f.do(http.MethodDelete, "/assistant/providers/custom", "")
	if off.Code != http.StatusOK {
		t.Fatalf("remove = %d %s", off.Code, off.Body.String())
	}
	settings, _ = f.s.assistantSettings(t.Context())
	if settings == nil || settings.Provider != assistant.ProviderAnthropic || settings.Key != "sk-ant-test-key" {
		t.Fatalf("after removal = %+v", settings)
	}
	// The mode a conversation starts in.
	if rec := f.do(http.MethodPut, "/assistant/preferences", `{"defaultMode":"full"}`); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"defaultMode":"full"`) {
		t.Fatalf("preferences = %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.do(http.MethodPut, "/assistant/preferences", `{"defaultMode":"yes"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("an unknown mode = %d", rec.Code)
	}
}

func mustMarshal(v any) json.RawMessage {
	raw, _ := json.Marshal(v)
	return raw
}
