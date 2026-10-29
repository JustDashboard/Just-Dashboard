package api

import (
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
	"github.com/go-chi/chi/v5"
)

// The deployment operator over HTTP, against a provider answering as
// Anthropic's Messages API: a read waits for the person and runs nothing
// until they allow it, an allowed read reaches the model and is audited, the
// model's answer quotes what it read, and a proposal runs nothing at all.

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
			next.ServeHTTP(w, req.WithContext(httpx.WithPrincipal(req.Context(), p)))
		})
	})
	r.Use(httpx.AuditMutations(s.Audit))
	s.mountDeployRoutes(r)
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

// A read the model asks for runs nothing until the person decides, and a
// declined one sends the model the refusal and none of the data.
func TestAnOperatorReadIsRefusedWithoutTheirLeave(t *testing.T) {
	f := operatorRouterAs(t, auth.RoleAdmin)
	f.provider.queue(
		claudeCalls("Let me look at the recent runs.", [2]any{assistant.ToolRecentRuns, map[string]any{"limit": 5}}),
		claudeAnswer("I could not read the runs, so I cannot say why it failed."),
	)
	events := f.ask(t, `{"question":"Why is shop down?"}`)
	done := doneOf(t, events)
	pending, _ := done["pending"].([]any)
	if len(pending) != 1 {
		t.Fatalf("the read did not wait for the person: %v", done)
	}
	call := pending[0].(map[string]any)
	if call["tool"] != assistant.ToolRecentRuns || call["label"] != "Read the last 5 runs" || call["id"] != "toolu_01" {
		t.Fatalf("pending = %v", call)
	}
	// The first request offered the operator's tools and carried no data.
	first := f.provider.raw[0]
	if !strings.Contains(first, `"name":"recent_runs"`) || !strings.Contains(first, `"name":"propose_action"`) || strings.Contains(first, operatorSentinel) {
		t.Fatalf("the first request: %s", first)
	}
	conversation := ofType(events, "start")[0]["conversation"].(string)

	events = f.ask(t, `{"conversation":"`+conversation+`","decisions":{"toolu_01":false}}`)
	ran := ofType(events, "ran")
	if len(ran) != 1 || ran[0]["calls"].([]any)[0].(map[string]any)["allowed"] != false {
		t.Fatalf("ran = %v", ran)
	}
	sent := f.provider.raw[len(f.provider.raw)-1]
	if !strings.Contains(sent, "The person declined this read") || strings.Contains(sent, operatorSentinel) {
		t.Fatalf("a declined read reached the model: %s", sent)
	}
	detail, status := f.audit(t, "deploy.assistant.tool")
	if status != http.StatusOK || !strings.Contains(detail, `"declined":["recent_runs"]`) || !strings.Contains(detail, `"allowed":[]`) {
		t.Fatalf("audit = %d %s", status, detail)
	}
}

// An allowed read runs through the dashboard's own store, is audited, and
// reaches the model; the model's answer quotes it.
func TestAnAllowedOperatorReadReachesTheModelWhichQuotesIt(t *testing.T) {
	f := operatorRouterAs(t, auth.RoleAdmin)
	quoted := regexp.MustCompile(`health gate failed: [^"\\]+`)
	f.provider.queue(
		claudeCalls("Let me look at the recent runs.", [2]any{assistant.ToolRecentRuns, map[string]any{}}),
		func(w http.ResponseWriter) {
			// The model's answer quotes the reason it was sent.
			f.provider.mu.Lock()
			last := f.provider.raw[len(f.provider.raw)-1]
			f.provider.mu.Unlock()
			claudeAnswer("Run #1 failed: " + quoted.FindString(last))(w)
		},
	)
	events := f.ask(t, `{"question":"Why did the last deploy fail?"}`)
	conversation := ofType(events, "start")[0]["conversation"].(string)
	events = f.ask(t, `{"conversation":"`+conversation+`","decisions":{"toolu_01":true}}`)
	ran := ofType(events, "ran")[0]["calls"].([]any)[0].(map[string]any)
	if ran["allowed"] != true || ran["read"] != "2 runs" {
		t.Fatalf("ran = %v", ran)
	}
	sent := f.provider.raw[len(f.provider.raw)-1]
	if !strings.Contains(sent, operatorSentinel) || !strings.Contains(sent, "health_gate_failed") {
		t.Fatalf("the allowed read did not reach the model: %s", sent)
	}
	text := ""
	for _, e := range ofType(events, "text") {
		text += e["text"].(string)
	}
	if !strings.Contains(text, "Run #1 failed: "+operatorSentinel) {
		t.Fatalf("the answer does not quote what was read: %q", text)
	}
	detail, _ := f.audit(t, "deploy.assistant.tool")
	if !strings.Contains(detail, `"allowed":["recent_runs"]`) || !strings.Contains(detail, `"outcome":"answered"`) {
		t.Fatalf("audit = %s", detail)
	}
}

// A run's reason carries what its commands printed, so a secret variable's
// value in it is masked before the model is sent it: in the prompt's last
// run and in the runs it reads.
func TestAnOperatorReadMasksTheEnvironmentsSecrets(t *testing.T) {
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
		claudeCalls("Reading the runs.", [2]any{assistant.ToolRecentRuns, map[string]any{"limit": 5}}),
		claudeAnswer("The migration failed."),
	)
	events := f.ask(t, `{"question":"Why did it fail?"}`)
	conversation := ofType(events, "start")[0]["conversation"].(string)
	f.ask(t, `{"conversation":"`+conversation+`","decisions":{"toolu_01":true}}`)
	if len(f.provider.raw) != 2 {
		t.Fatalf("%d requests reached the model", len(f.provider.raw))
	}
	for i, sent := range f.provider.raw {
		if strings.Contains(sent, secret) || !strings.Contains(sent, "migrate printed API_KEY=") {
			t.Fatalf("request %d was sent unmasked or without the run: %s", i, sent)
		}
	}
}

// A proposal runs nothing: the page is sent a card, the model is told the
// person decides, and no run is queued until the person presses the card,
// which calls the route the project page's own verb calls.
func TestAnOperatorProposalRunsOnlyWhenItsCardIsPressed(t *testing.T) {
	f := operatorRouterAs(t, auth.RoleAdmin)
	if _, err := f.s.Store.DB.Exec(`INSERT INTO deploy_releases(project_id, environment_id, release_number, state, created_at, activated_at)
		VALUES(?, ?, 1, 'live', 1, 1)`, f.projectID, f.environmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Store.DB.Exec(`UPDATE deploy_environments SET live_release_id = (SELECT id FROM deploy_releases WHERE environment_id = ?) WHERE id = ?`,
		f.environmentID, f.environmentID); err != nil {
		t.Fatal(err)
	}
	var runsBefore int
	_ = f.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_runs`).Scan(&runsBefore)
	f.provider.queue(
		claudeCalls("The container keeps failing its health check.",
			[2]any{assistant.ToolProposeAction, map[string]any{"action": "restart", "reason": "Its health check fails after a config change."}},
			[2]any{assistant.ToolProposeAction, map[string]any{"action": "rollback", "release": 9, "reason": "Release 9 was the last good one."}}),
		claudeAnswer("I proposed a restart; approve it if you agree."),
	)
	events := f.ask(t, `{"question":"Fix it"}`)
	proposals := ofType(events, "proposal")
	if len(proposals) != 1 {
		t.Fatalf("proposals = %v", proposals)
	}
	card := proposals[0]["proposal"].(map[string]any)
	if card["action"] != "restart" || card["label"] != "Restart production" || card["destructive"] != true || card["capability"] != string(auth.CapDestructive) {
		t.Fatalf("card = %v", card)
	}
	// Carried on at once: the model was told the person decides, and that
	// release 9 does not exist, in one message.
	sent := f.provider.raw[len(f.provider.raw)-1]
	if !strings.Contains(sent, "Nothing has run") || !strings.Contains(sent, "has no release #9") {
		t.Fatalf("the proposals were answered as %s", sent)
	}
	if done := doneOf(t, events); done["pending"] != nil {
		t.Fatalf("a proposal waited for the person: %v", done)
	}
	var runsAfter int
	_ = f.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_runs`).Scan(&runsAfter)
	if runsAfter != runsBefore {
		t.Fatalf("a proposal queued a run: %d → %d", runsBefore, runsAfter)
	}
	detail, _ := f.audit(t, "deploy.assistant.ask")
	if !strings.Contains(detail, `"proposed":["restart"]`) {
		t.Fatalf("audit = %s", detail)
	}
	// The card's press is the project page's own Restart: the runs route,
	// which queues the run and audits it as any restart is.
	rec := f.do(http.MethodPost, fmt.Sprintf("/deploy/%d/environments/%d/runs", f.projectID, f.environmentID), `{"operation":"restart"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("the approved restart = %d %s", rec.Code, rec.Body.String())
	}
	_ = f.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_runs`).Scan(&runsAfter)
	if runsAfter != runsBefore+1 {
		t.Fatalf("the approved restart queued %d runs", runsAfter-runsBefore)
	}
	if detail, _ := f.audit(t, "deploy.run.request"); !strings.Contains(detail, `"operation":"restart"`) {
		t.Fatalf("the approved restart was audited as %s", detail)
	}
}

// A call for anything but the operator's tools, or with an input outside a
// tool's bounds, runs nothing and keeps nothing.
func TestAnOperatorCallOutsideItsToolsIsRefused(t *testing.T) {
	f := operatorRouterAs(t, auth.RoleAdmin)
	f.provider.queue(claudeCalls("", [2]any{"run_shell", map[string]any{"command": "id"}}))
	events := f.ask(t, `{"question":"Run id"}`)
	if errors := ofType(events, "error"); len(errors) != 1 || !strings.Contains(errors[0]["error"].(map[string]any)["message"].(string), "does not offer") {
		t.Fatalf("events = %v", events)
	}
	for _, input := range []map[string]any{{"limit": 500}, {"limit": 3, "path": "/etc"}} {
		if _, err := assistant.OperatorCalls([]assistant.Block{{Type: assistant.BlockToolUse, ToolID: "t", ToolName: assistant.ToolRecentRuns, Input: mustMarshal(input)}}); err == nil {
			t.Fatalf("%v was read", input)
		}
	}
	if _, err := assistant.OperatorCalls([]assistant.Block{{Type: assistant.BlockToolUse, ToolID: "t", ToolName: assistant.ToolProposeAction,
		Input: mustMarshal(map[string]any{"action": "rollback", "reason": "x"})}}); err == nil {
		t.Fatal("a rollback with no release was read")
	}
}

// The operator is a session's, refused to a role without service.control,
// and needs the provider set up.
func TestTheOperatorAsksForItsCapabilityAndAProvider(t *testing.T) {
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
}

func mustMarshal(v any) json.RawMessage {
	raw, _ := json.Marshal(v)
	return raw
}
