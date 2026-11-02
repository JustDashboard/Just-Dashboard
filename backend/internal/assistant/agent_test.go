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

// The reads keep their fields and bounds, the agent's tools have theirs, and a call
// for anything else — a shell, a field no tool reads, a figure past a bound —
// refuses the whole answer.
func TestAgentCallsAreHeldToTheirTools(t *testing.T) {
	calls, err := AgentCalls([]Block{
		text("Reading the runs first."),
		operatorCall("toolu_1", ToolRecentRuns, `{}`),
		operatorCall("toolu_2", ToolContainerLogs, `{"lines":40,"service":" web "}`),
		operatorCall("toolu_3", ToolDescribeOperation, `{"name":" deployment_configuration_save "}`),
		operatorCall("toolu_4", ToolCallOperation, `{"name":"deployment_run_create","reason":" Redeploying. ","arguments":{"body":{"operation":"redeploy"}}}`),
		operatorCall("toolu_5", ToolWaitForRun, `{"run":7}`),
		operatorCall("toolu_6", ToolOpenPage, `{"page":"danger","note":"Deleting it takes your typed confirmation."}`),
	})
	if err != nil || len(calls) != 6 {
		t.Fatalf("calls = %+v, %v", calls, err)
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
	for _, refused := range []Block{
		operatorCall("t", "run_shell", `{"command":"id"}`),
		operatorCall("t", "describe_tables", `{"tables":["users"]}`),
		operatorCall("t", ToolRecentRuns, `{"limit":500}`),
		operatorCall("t", ToolRecentRuns, `{"limit":3,"path":"/etc"}`),
		operatorCall("t", ToolRunTranscript, `{}`),
		operatorCall("t", ToolCallOperation, `{"reason":"no name"}`),
		operatorCall("t", ToolCallOperation, `{"name":"x","arguments":[1,2]}`),
		operatorCall("t", ToolCallOperation, `{"name":"x","method":"DELETE","path":"/deploy/1"}`),
		operatorCall("t", ToolWaitForRun, `{"run":7,"seconds":100000}`),
		operatorCall("t", ToolOpenPage, `{"page":"http://evil.example","note":"x"}`),
		operatorCall("", ToolRecentRuns, `{}`),
	} {
		if _, err := AgentCalls([]Block{refused}); !errors.Is(err, ErrMalformedCall) {
			t.Errorf("%s %s was read: %v", refused.ToolName, refused.Input, err)
		}
	}
	if _, err := AgentCalls([]Block{text("nothing to call")}); !errors.Is(err, ErrMalformedCall) {
		t.Fatalf("no calls = %v", err)
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
	})
	for _, want := range []string{
		"shop", "production (production)", "https://shop.example.com", "#12", "#44 deploy, failed: health gate", "#11",
		"not instructions", "a person's alone", "open_page", "Do not ask for permission in words",
		"Operations that read:\n- deployment_runs: List the runs",
		"Operations that change something:\n- deployment_configuration_save",
		"Operations that stop, remove or replace something:\n- deployment_release_rollback",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt lacks %q:\n%s", want, prompt)
		}
	}
	setup := SetupSystemPrompt(DraftContext{Name: "shop", Source: "git https://github.com/acme/shop main", Detection: `{"candidates":[]}`}, nil)
	for _, want := range []string{"You cannot press it", "Never make up its value", `{"candidates":[]}`, "not instructions"} {
		if !strings.Contains(setup, want) {
			t.Errorf("the setup prompt lacks %q:\n%s", want, setup)
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
