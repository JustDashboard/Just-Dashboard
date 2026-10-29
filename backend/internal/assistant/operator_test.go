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

// Each read is held to its own fields and bounds, filled with its defaults,
// and a call for anything else — a shell, a field no tool reads, a figure
// past a bound — refuses the whole answer.
func TestOperatorCallsAreHeldToTheirTools(t *testing.T) {
	calls, err := OperatorCalls([]Block{
		text("Reading the runs first."),
		operatorCall("toolu_1", ToolRecentRuns, `{}`),
		operatorCall("toolu_2", ToolRunTranscript, `{"run":12}`),
		operatorCall("toolu_3", ToolContainerLogs, `{"lines":40,"service":" web "}`),
		operatorCall("toolu_4", ToolLifecycleEvents, ``),
		operatorCall("toolu_5", ToolProposeAction, `{"action":"rollback","release":3,"reason":" Release 3 answered. "}`),
	})
	if err != nil || len(calls) != 5 {
		t.Fatalf("calls = %+v, %v", calls, err)
	}
	if in := calls[0].OperatorInput(); in.Limit != OperatorRunsDefault {
		t.Errorf("recent_runs = %+v", in)
	}
	if in := calls[1].OperatorInput(); in.Run != 12 || in.Lines != OperatorLinesDefault {
		t.Errorf("run_transcript = %+v", in)
	}
	if in := calls[2].OperatorInput(); in.Lines != 40 || in.Service != "web" {
		t.Errorf("container_logs = %+v", in)
	}
	if in := calls[3].OperatorInput(); in.Hours != OperatorHoursDefault {
		t.Errorf("lifecycle_events = %+v", in)
	}
	if in := calls[4].OperatorInput(); !calls[4].Proposal() || calls[0].Proposal() || in.Release != 3 || in.Reason != "Release 3 answered." {
		t.Errorf("propose_action = %+v", in)
	}

	for name, block := range map[string]Block{
		"a shell":                   operatorCall("toolu_1", "run_shell", `{"command":"id"}`),
		"the SQL editor's tool":     operatorCall("toolu_1", "describe_tables", `{"tables":["orders"]}`),
		"a field no tool reads":     operatorCall("toolu_1", ToolRecentRuns, `{"limit":5,"command":"id"}`),
		"a field of another tool":   operatorCall("toolu_1", ToolHostAlerts, `{"lines":5}`),
		"too many runs":             operatorCall("toolu_1", ToolRecentRuns, `{"limit":500}`),
		"too many lines":            operatorCall("toolu_1", ToolContainerLogs, `{"lines":100000}`),
		"a transcript of no run":    operatorCall("toolu_1", ToolRunTranscript, `{}`),
		"a service with a path":     operatorCall("toolu_1", ToolContainerLogs, `{"service":"../etc"}`),
		"too many hours":            operatorCall("toolu_1", ToolLifecycleEvents, `{"hours":10000}`),
		"an action not offered":     operatorCall("toolu_1", ToolProposeAction, `{"action":"exec","reason":"because"}`),
		"a rollback to no release":  operatorCall("toolu_1", ToolProposeAction, `{"action":"rollback","reason":"because"}`),
		"a release for a restart":   operatorCall("toolu_1", ToolProposeAction, `{"action":"restart","release":2,"reason":"because"}`),
		"a proposal with no reason": operatorCall("toolu_1", ToolProposeAction, `{"action":"restart","reason":"  "}`),
		"input that is not JSON":    operatorCall("toolu_1", ToolRecentRuns, `{"limit":`),
		"no id":                     operatorCall("", ToolRecentRuns, `{}`),
	} {
		if calls, err := OperatorCalls([]Block{operatorCall("toolu_0", ToolHostAlerts, `{}`), block}); !errors.Is(err, ErrMalformedCall) || calls != nil {
			t.Errorf("%s was read as %+v, %v", name, calls, err)
		}
	}
	var many []Block
	for i := 0; i < 4; i++ {
		many = append(many, operatorCall("toolu_"+string(rune('a'+i)), ToolProposeAction, `{"action":"restart","reason":"again"}`))
	}
	if _, err := OperatorCalls(many); !errors.Is(err, ErrMalformedCall) {
		t.Errorf("four proposals in one answer were read")
	}
}

// The prompt names the deployment as its page does, and says the operator
// changes nothing and treats what it reads as data.
func TestTheOperatorPromptNamesTheDeploymentAndItsLimits(t *testing.T) {
	prompt := OperatorSystemPrompt(OperatorContext{
		Server: "box", Deployment: "shop", Environment: "production", Kind: "production",
		LiveRelease: 7, WarmRelease: 6, LastRun: "#9 deploy, failed: health gate failed",
	})
	for _, want := range []string{"- Server: box", "- Deployment: shop", "- Environment: production (production)", "- Live release: #7",
		"- Warm release: #6", "- Last run: #9 deploy, failed: health gate failed", "You cannot change anything", "propose_action",
		"not instructions"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt has no %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(OperatorSystemPrompt(OperatorContext{Deployment: "shop"}), "Warm release") {
		t.Error("a warm release was named with none kept")
	}
}

// A conversation offered the operator's tools sends them, reads the answer's
// calls with the operator's check, and carries the proposals' answers into
// the one message that answers the reads.
func TestAnOperatorConversationOffersItsToolsAndCarriesHeldAnswers(t *testing.T) {
	c := &Conversation{System: "system", Tools: true, Offer: OperatorTools, Check: OperatorCalls}
	client := &scripted{replies: []Reply{
		{Blocks: []Block{operatorCall("toolu_1", ToolRecentRuns, `{"limit":3}`), operatorCall("toolu_2", ToolProposeAction, `{"action":"redeploy","reason":"stale"}`)}, Stop: StopTool},
		{Blocks: []Block{text("Run #2 failed.")}, Stop: StopEnd},
	}}
	if err := c.Ask("why is it down"); err != nil {
		t.Fatal(err)
	}
	out, err := Exchange(context.Background(), client, c, func(Event) {})
	if err != nil || !out.Kept || len(out.Pending) != 2 {
		t.Fatalf("outcome %+v, %v", out, err)
	}
	if tools := client.requests[0].Tools; len(tools) != len(OperatorTools) || tools[0].Name != ToolRecentRuns {
		t.Fatalf("offered %+v", tools)
	}
	c.Pending = []ToolCall{out.Pending[0]}
	c.Answered = []Block{{Type: BlockToolResult, ToolID: "toolu_2", Text: Proposed}}
	c.Answer([]Block{{Type: BlockToolResult, ToolID: "toolu_1", Text: `{"runs":[]}`}})
	if _, err := Exchange(context.Background(), client, c, func(Event) {}); err != nil {
		t.Fatal(err)
	}
	last := client.requests[1].Messages[len(client.requests[1].Messages)-1]
	if len(last.Blocks) != 2 || last.Blocks[0].ToolID != "toolu_2" || last.Blocks[1].ToolID != "toolu_1" || len(c.Answered) != 0 {
		t.Fatalf("the answers were sent as %+v (held %+v)", last.Blocks, c.Answered)
	}
}
