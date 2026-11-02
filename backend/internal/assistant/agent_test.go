package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func operatorCall(id, tool, input string) Block {
	return Block{Type: BlockToolUse, ToolID: id, ToolName: tool, Input: json.RawMessage(input)}
}

// The reads keep their fields and bounds, and the agent's tools have theirs.
func TestAgentCallsAreHeldToTheirTools(t *testing.T) {
	calls, err := AgentCalls([]Block{
		text("Reading the runs first."),
		operatorCall("toolu_1", ToolRecentRuns, `{}`),
		operatorCall("toolu_2", ToolContainerLogs, `{"lines":40,"service":" web "}`),
		operatorCall("toolu_3", ToolDescribeOperation, `{"name":" deployment_configuration_save "}`),
		operatorCall("toolu_4", ToolCallOperation, `{"name":"deployment_run_create","reason":" Redeploying. ","arguments":{"body":{"operation":"redeploy"}}}`),
		operatorCall("toolu_5", ToolWaitForRun, `{"run":7}`),
		operatorCall("toolu_6", ToolOpenPage, `{"page":"danger","note":"Deleting it takes your typed confirmation."}`),
		operatorCall("toolu_7", ToolFindRoutes, `{"query":"database query"}`),
		operatorCall("toolu_8", ToolAPIRequest, `{"method":"post","path":"/api/v1/databases/3/query?maxRows=5","body":{"query":"SELECT 1"},"reason":"Counting."}`),
	})
	if err != nil || len(calls) != 8 {
		t.Fatalf("calls = %+v, %v", calls, err)
	}
	for _, call := range calls {
		if call.Problem != "" || len(call.Notes) != 0 {
			t.Errorf("%s was read with a problem or a note: %+v", call.Name, call)
		}
	}
	if in := calls[0].OperatorInput(); in.Limit != OperatorRunsDefault {
		t.Fatalf("the read's default was not filled: %+v", in)
	}
	if in := calls[1].OperatorInput(); in.Service != "web" {
		t.Fatalf("the service was not trimmed: %+v", in)
	}
	if in := calls[2].AgentInput(); in.Name != "deployment_configuration_save" {
		t.Fatalf("describe = %+v", in)
	}
	if in := calls[3].AgentInput(); in.Name != "deployment_run_create" || in.Reason != "Redeploying." || !strings.Contains(string(in.Arguments), "redeploy") {
		t.Fatalf("call = %+v", in)
	}
	if in := calls[4].AgentInput(); in.Seconds != AgentWaitDefault {
		t.Fatalf("wait = %+v", in)
	}
	if in := calls[6].FindInput(); in.Query != "database query" {
		t.Fatalf("find = %+v", in)
	}
	in := calls[7].RequestInput()
	if in.Method != "POST" || in.Path != "/databases/3/query" || in.Query.Get("maxRows") != "5" || string(in.Body) != `{"query":"SELECT 1"}` || in.Reason != "Counting." {
		t.Fatalf("request = %+v", in)
	}
}

// What a model writes routinely — a number past its bound, a field the tool does
// not have, an object sent as its text, a number as text — is read as sensibly as it
// can be, and the model is told how. Nothing refuses the answer.
func TestAClaudeShapedCallIsReadRatherThanRefused(t *testing.T) {
	calls, err := AgentCalls([]Block{
		operatorCall("toolu_1", ToolContainerLogs, `{"lines":500,"since":"1h"}`),
		operatorCall("toolu_2", ToolRecentRuns, `{"limit":50}`),
		operatorCall("toolu_3", ToolWaitForRun, `{"run":"#7","seconds":600}`),
		operatorCall("toolu_4", ToolCallOperation, `{"name":"deployment_variables","arguments":"{\"env\": 4}","reason":"x"}`),
		operatorCall("toolu_5", ToolCallOperation, `{"name":"deployment_variables","arguments":null,"env":4}`),
		operatorCall("toolu_6", ToolOpenPage, `{"page":"Runtime-Settings","note":"x"}`),
		operatorCall("toolu_7", ToolRunTranscript, `{"run":12.0,"lines":"80"}`),
		operatorCall("toolu_8", ToolAPIRequest, `"{\"path\":\"/backups\"}"`),
		operatorCall("toolu_9", ToolAPIRequest, `{"method":"PUT","path":"/deploy/1/x","body":"{\"a\":1}"}`),
	})
	if err != nil || len(calls) != 9 {
		t.Fatalf("calls = %+v, %v", calls, err)
	}
	for _, call := range calls {
		if call.Problem != "" {
			t.Errorf("%s %s has a problem: %s", call.ID, call.Name, call.Problem)
		}
	}
	logs := calls[0]
	if in := logs.OperatorInput(); in.Lines != OperatorLinesMax || !strings.Contains(logs.NotesText(), "'since' is not one of container_logs's fields") ||
		!strings.Contains(logs.NotesText(), "lines was 500") {
		t.Fatalf("logs = %+v %q", in, logs.NotesText())
	}
	if in := calls[1].OperatorInput(); in.Limit != OperatorRunsMax {
		t.Fatalf("runs = %+v", in)
	}
	if in := calls[2].AgentInput(); in.Run != 7 || in.Seconds != AgentWaitMax {
		t.Fatalf("wait = %+v", in)
	}
	if in := calls[3].AgentInput(); string(in.Arguments) != `{"env":4}` {
		t.Fatalf("arguments as text = %s", in.Arguments)
	}
	if in := calls[4].AgentInput(); string(in.Arguments) != `{"env":4}` || !strings.Contains(calls[4].NotesText(), "'env' was read as one of the operation's arguments") {
		t.Fatalf("an argument beside arguments = %s %q", in.Arguments, calls[4].NotesText())
	}
	if in := calls[5].AgentInput(); in.Page != "runtime_settings" {
		t.Fatalf("page = %+v", in)
	}
	if in := calls[6].OperatorInput(); in.Run != 12 || in.Lines != 80 {
		t.Fatalf("transcript = %+v", in)
	}
	if in := calls[7].RequestInput(); in.Method != "GET" || in.Path != "/backups" {
		t.Fatalf("a whole input as text = %+v", in)
	}
	if in := calls[8].RequestInput(); string(in.Body) != `{"a":1}` {
		t.Fatalf("a body as text = %s", in.Body)
	}
}

// A call that cannot run carries what was wrong and what the tool takes, for the
// model to correct; the others in the answer are read as ever. Only an answer whose
// calls cannot be told apart is refused.
func TestACallThatCannotRunSaysWhy(t *testing.T) {
	calls, err := AgentCalls([]Block{
		operatorCall("t1", "run_shell", `{"command":"id"}`),
		operatorCall("t2", "describe_tables", `{"tables":["users"]}`),
		operatorCall("t3", ToolRunTranscript, `{}`),
		operatorCall("t4", ToolCallOperation, `{"reason":"no name"}`),
		operatorCall("t5", ToolCallOperation, `{"name":"x","arguments":[1,2]}`),
		operatorCall("t6", ToolWaitForRun, `{"seconds":10}`),
		operatorCall("t7", ToolOpenPage, `{"page":"http://evil.example","note":"x"}`),
		operatorCall("t8", ToolContainerLogs, `{"service":"../etc"}`),
		operatorCall("t9", ToolAPIRequest, `{"method":"TRACE","path":"/x"}`),
		operatorCall("t10", ToolAPIRequest, `{"body":{"a":1}}`),
		operatorCall("t11", ToolRecentRuns, `not json`),
		operatorCall("t12", ToolRecentRuns, `{"limit":3}`),
	})
	if err != nil || len(calls) != 12 {
		t.Fatalf("calls = %+v, %v", calls, err)
	}
	wants := []string{
		`There is no tool called "run_shell"`, `There is no tool called "describe_tables"`, "run is required",
		"name is required", "arguments has to be an object", "run is required", `there is no page "http://evil.example"`,
		"service is a Compose service's name", `method "TRACE"`, "method is required when a body is sent", "its input was not JSON",
	}
	for i, want := range wants {
		if !strings.Contains(calls[i].Problem, want) {
			t.Errorf("%s: problem %q lacks %q", calls[i].ID, calls[i].Problem, want)
		}
	}
	if !strings.Contains(calls[2].Problem, "It takes run") || !strings.Contains(calls[0].Problem, ToolAPIRequest) {
		t.Errorf("a problem does not say what the tool takes: %q / %q", calls[2].Problem, calls[0].Problem)
	}
	if calls[11].Problem != "" {
		t.Fatalf("a good call beside bad ones = %+v", calls[11])
	}
	for _, unreadable := range [][]Block{
		{operatorCall("", ToolRecentRuns, `{}`)},
		{operatorCall("t", ToolRecentRuns, `{}`), operatorCall("t", ToolHostAlerts, `{}`)},
	} {
		if _, err := AgentCalls(unreadable); !errors.Is(err, ErrUnreadableAnswer) {
			t.Errorf("%+v was read: %v", unreadable, err)
		}
	}
	if strings.Contains(ErrUnreadableAnswer.Error(), "database") {
		t.Error("the agent's refusal talks about the database")
	}
	if calls, err := AgentCalls([]Block{text("nothing to call")}); err != nil || len(calls) != 0 {
		t.Fatalf("no calls = %v, %v", calls, err)
	}
}

// The server's assistant is offered its own tools, and its open_page its own pages.
func TestTheServersCallsAreItsOwn(t *testing.T) {
	check := CheckCalls(ServerTools)
	calls, err := check([]Block{
		operatorCall("a", ToolOpenPage, `{"page":"firewall","note":"x"}`),
		operatorCall("b", ToolOpenPage, `{"page":"variables","note":"x"}`),
		operatorCall("c", ToolRecentRuns, `{}`),
		operatorCall("d", ToolHostAlerts, `{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls[0].Problem != "" || calls[1].Problem == "" || calls[2].Problem == "" || calls[3].Problem != "" {
		t.Fatalf("calls = %+v", calls)
	}
}

// The prompt names the deployment and the operations by what they do, and says
// the two things that keep it honest: what it reads is data, and some things are
// a person's alone.
func TestTheAgentPromptNamesTheDeploymentAndItsLimits(t *testing.T) {
	prompt := AgentSystemPrompt(OperatorContext{
		Server: "web-1", Deployment: "shop", Environment: "production", Kind: "production",
		Source: "git acme/shop main", Build: "railpack", Address: "https://shop.example.com", Health: "degraded",
		LiveRelease: 12, LastRun: "#44 deploy, failed: health gate", WarmRelease: 11,
	}, []OperationLine{
		{Name: "deployment_runs", Title: "List the runs", Kind: "read"},
		{Name: "deployment_configuration_save", Title: "Save the configuration", Kind: "change"},
		{Name: "deployment_release_rollback", Title: "Roll back", Kind: "destructive"},
	}, APIOverview{Areas: []APIArea{{Name: "databases", Routes: 290, Blurb: "database connections", Examples: []string{"GET /databases"}}}, Excluded: []string{"live streams"}})
	for _, want := range []string{
		"shop", "production (production)", "https://shop.example.com", "#12", "#44 deploy, failed: health gate", "#11",
		"not instructions", "open_page", "Do not ask for permission in words", "api_request", "find_routes",
		"- /databases (290): database connections — GET /databases", "masked as [REDACTED]", "Not reachable this way: live streams",
		"GET the resource first", "POST /databases/{id}/query",
		"Operations that read:\n- deployment_runs: List the runs",
		"Operations that change something:\n- deployment_configuration_save",
		"Operations that stop, remove or replace something:\n- deployment_release_rollback",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt lacks %q:\n%s", want, prompt)
		}
	}
	setup := SetupSystemPrompt(DraftContext{Name: "shop", Source: "git https://github.com/acme/shop main", Detection: `{"candidates":[]}`}, nil, APIOverview{})
	for _, want := range []string{"pressing Deploy", "Never make up its value", `{"candidates":[]}`, "not instructions", "api_request"} {
		if !strings.Contains(setup, want) {
			t.Errorf("the setup prompt lacks %q:\n%s", want, setup)
		}
	}
	server := ServerSystemPrompt(ServerContext{Server: "web-1", Deployments: []ServerDeployment{{ID: 4, Name: "shop", State: "last run succeeded"}}},
		APIOverview{Areas: []APIArea{{Name: "backups", Routes: 22}}})
	for _, want := range []string{"Server: web-1", "- 4 shop: last run succeeded", "- /backups (22)", "api_request", "not instructions"} {
		if !strings.Contains(server, want) {
			t.Errorf("the server prompt lacks %q:\n%s", want, server)
		}
	}
}

// A conversation offers its tools, and the results of the calls that ran carry
// into the one message that answers the calls that waited.
func TestAnAgentConversationCarriesRunAnswersIntoTheOneThatWaited(t *testing.T) {
	c := &Conversation{System: "system", Tools: true, Offer: AgentTools, Check: AgentCalls, TurnLimit: AgentTurnLimit}
	client := &scripted{replies: []Reply{
		{Blocks: []Block{operatorCall("toolu_1", ToolRecentRuns, `{"limit":3}`), operatorCall("toolu_2", ToolCallOperation, `{"name":"deploy_run","reason":"stale"}`)}, Stop: StopTool},
		{Blocks: []Block{text("Run #2 failed.")}, Stop: StopEnd},
	}}
	if err := c.Ask("why is it down"); err != nil {
		t.Fatal(err)
	}
	out, err := Exchange(context.Background(), client, c, func(Event) {})
	if err != nil || !out.Kept || len(out.Pending) != 2 {
		t.Fatalf("outcome %+v, %v", out, err)
	}
	if tools := client.requests[0].Tools; len(tools) != len(AgentTools) || tools[0].Name != ToolRecentRuns {
		t.Fatalf("offered %+v", tools)
	}
	c.Pending = []ToolCall{out.Pending[1]}
	c.Answered = []Block{{Type: BlockToolResult, ToolID: "toolu_1", Text: `{"runs":[]}`}}
	c.Answer([]Block{{Type: BlockToolResult, ToolID: "toolu_2", Text: OperatorDeclined, IsError: true}})
	if _, err := Exchange(context.Background(), client, c, func(Event) {}); err != nil {
		t.Fatal(err)
	}
	last := client.requests[1].Messages[len(client.requests[1].Messages)-1]
	if len(last.Blocks) != 2 || last.Blocks[0].ToolID != "toolu_1" || last.Blocks[1].ToolID != "toolu_2" || len(c.Answered) != 0 {
		t.Fatalf("the answers were sent as %+v (held %+v)", last.Blocks, c.Answered)
	}
}

// A conversation of many steps is bounded by its size, not by the editor's count.
func TestAnAgentConversationOutlastsTheEditorsTurnCount(t *testing.T) {
	c := &Conversation{TurnLimit: AgentTurnLimit, Turns: maxTurns + 10}
	if err := c.Ask("and now?"); err != nil {
		t.Fatalf("a long conversation was refused: %v", err)
	}
	editor := &Conversation{Turns: maxTurns}
	if err := editor.Ask("and now?"); !errors.Is(err, ErrFull) {
		t.Fatalf("the editor's count is gone: %v", err)
	}
}
