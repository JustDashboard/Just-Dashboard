package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A Claude answer as the Messages API streams it with fine-grained tool input:
// thinking, text, then parallel calls whose input arrives in fragments — one with
// a field the tool lacks and a number past its bound, one whose arguments object
// was written as the text of one, one for a tool that does not exist, and one
// whose fragments do not add up to JSON.
func claudeParallelCalls() [][2]string {
	events := [][2]string{
		ev("message_start", map[string]any{"type": "message_start", "message": map[string]any{"model": "claude-sonnet-5-5", "usage": map[string]int{"input_tokens": 3000}}}),
		ev("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "thinking", "thinking": ""}}),
		ev("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "thinking_delta", "thinking": "Read the logs and the variables."}}),
		ev("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "signature_delta", "signature": "sig-1"}}),
		ev("content_block_start", map[string]any{"type": "content_block_start", "index": 1, "content_block": map[string]any{"type": "text", "text": ""}}),
		ev("content_block_delta", map[string]any{"type": "content_block_delta", "index": 1, "delta": map[string]any{"type": "text_delta", "text": "Let me look."}}),
	}
	calls := []struct {
		id, name string
		parts    []string
	}{
		{"toolu_logs", ToolContainerLogs, []string{`{"lines": 5`, `00, "since": "1h", "service": "web"}`}},
		{"toolu_vars", ToolCallOperation, []string{`{"name": "deployment_variables", "reason": "Listing them.", `, `"arguments": "{\"env\": 3}"}`}},
		{"toolu_shell", "run_shell", []string{`{"command": "id"}`}},
		{"toolu_broken", ToolRecentRuns, []string{`{"limit": `, `3, "x`}},
	}
	for i, call := range calls {
		index := i + 2
		events = append(events, ev("content_block_start", map[string]any{"type": "content_block_start", "index": index,
			"content_block": map[string]any{"type": "tool_use", "id": call.id, "name": call.name, "input": map[string]any{}}}))
		for _, part := range call.parts {
			events = append(events, ev("content_block_delta", map[string]any{"type": "content_block_delta", "index": index,
				"delta": map[string]any{"type": "input_json_delta", "partial_json": part}}))
		}
		events = append(events, ev("content_block_stop", map[string]any{"type": "content_block_stop", "index": index}))
	}
	return append(events,
		ev("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "tool_use"}, "usage": map[string]int{"output_tokens": 300}}),
		ev("message_stop", map[string]any{"type": "message_stop"}),
	)
}

// Every call of a Claude answer is read on its own: the clear ones run with what
// was read differently said, the broken ones are answered with what was wrong, and
// the round trip goes back to Anthropic whole — the thinking as it came, every
// call's input an object, every call answered by its id.
func TestAClaudeAnswerWithABadCallGoesOn(t *testing.T) {
	client, got := anthropicFor(t, "claude-sonnet-5-5", func(w http.ResponseWriter, _ *http.Request, n int) {
		if n == 1 {
			sse(w, claudeParallelCalls()...)
			return
		}
		sse(w, messageEvents("The web service crashed.", "end_turn")...)
	})
	c := &Conversation{System: "system", Tools: true, Offer: AgentTools, Check: CheckCalls(AgentTools), TurnLimit: AgentTurnLimit}
	if err := c.Ask("why is it down"); err != nil {
		t.Fatal(err)
	}
	out, err := Exchange(context.Background(), client, c, func(Event) {})
	if err != nil || !out.Kept || len(out.Pending) != 4 {
		t.Fatalf("outcome %+v, %v", out, err)
	}
	logs, vars, shell, broken := out.Pending[0], out.Pending[1], out.Pending[2], out.Pending[3]
	if logs.Problem != "" || logs.OperatorInput().Lines != OperatorLinesMax || logs.OperatorInput().Service != "web" ||
		!strings.Contains(logs.NotesText(), "'since'") {
		t.Fatalf("logs = %+v", logs)
	}
	if vars.Problem != "" || string(vars.AgentInput().Arguments) != `{"env":3}` {
		t.Fatalf("variables = %+v", vars)
	}
	if !strings.Contains(shell.Problem, `no tool called "run_shell"`) || !strings.Contains(broken.Problem, "not JSON") {
		t.Fatalf("shell = %q, broken = %q", shell.Problem, broken.Problem)
	}
	results := ToolResults(out.Pending, func(call ToolCall) (string, bool) {
		if call.Problem != "" {
			return call.Problem, false
		}
		return call.NotesText() + `{"ok":true}`, true
	})
	c.Answer(results)
	if _, err := Exchange(context.Background(), client, c, func(Event) {}); err != nil {
		t.Fatal(err)
	}
	var sent struct {
		Messages []struct {
			Role    string           `json:"role"`
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(got.raw[1]), &sent); err != nil {
		t.Fatal(err)
	}
	answer := sent.Messages[1].Content
	if answer[0]["type"] != "thinking" || answer[0]["signature"] != "sig-1" || answer[1]["type"] != "text" {
		t.Fatalf("the thinking did not go back as it came: %v", answer[:2])
	}
	for _, block := range answer[2:] {
		if _, isObject := block["input"].(map[string]any); block["type"] != "tool_use" || !isObject {
			t.Fatalf("a call went back as %v", block)
		}
	}
	results2 := sent.Messages[2].Content
	if len(results2) != 4 {
		t.Fatalf("results = %v", results2)
	}
	for i, id := range []string{"toolu_logs", "toolu_vars", "toolu_shell", "toolu_broken"} {
		if results2[i]["tool_use_id"] != id {
			t.Fatalf("result %d answers %v", i, results2[i]["tool_use_id"])
		}
	}
	if results2[2]["is_error"] != true || results2[0]["is_error"] != nil || !strings.HasPrefix(results2[0]["content"].(string), "Note:") {
		t.Fatalf("results = %v", results2)
	}
}

// A call's input that only its first event carries is read from there.
func TestAClaudeCallWithItsInputInItsFirstEventIsRead(t *testing.T) {
	client, _ := anthropicFor(t, "claude-sonnet-5-5", func(w http.ResponseWriter, _ *http.Request, _ int) {
		sse(w,
			ev("message_start", map[string]any{"type": "message_start", "message": map[string]any{"model": "claude-sonnet-5-5"}}),
			ev("content_block_start", map[string]any{"type": "content_block_start", "index": 0,
				"content_block": map[string]any{"type": "tool_use", "id": "toolu_1", "name": ToolRecentRuns, "input": map[string]any{"limit": 4}}}),
			ev("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0}),
			ev("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "tool_use"}}),
			ev("message_stop", map[string]any{"type": "message_stop"}),
		)
	})
	c := &Conversation{Tools: true, Offer: AgentTools, Check: AgentCalls, TurnLimit: AgentTurnLimit}
	_ = c.Ask("runs?")
	out, err := Exchange(context.Background(), client, c, func(Event) {})
	if err != nil || len(out.Pending) != 1 || out.Pending[0].OperatorInput().Limit != 4 {
		t.Fatalf("outcome %+v, %v", out, err)
	}
}

// The OpenAI-compatible path reads the same way: arguments written as the text of
// the text of an object, a bound passed, a call with arguments that are not JSON
// beside a good one. The calls go back as they came.
func TestAnOpenAICompatibleAnswerWithABadCallGoesOn(t *testing.T) {
	var bodies []map[string]any
	client, got := openAIFor(t, "", func(w http.ResponseWriter, r *http.Request) {
		chunks(w,
			`{"model":"gpt","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","function":{"name":"api_request","arguments":"\"{\\\"path\\\":\\\"/databases\\\"}\""}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_b","function":{"name":"recent_runs","arguments":"{\"limit\":99}"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":2,"id":"call_c","function":{"name":"lifecycle_events","arguments":"{hours: 3"}}]}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
			`[DONE]`,
		)
	})
	_ = bodies
	openai := client.(*openAIClient)
	openai.tools = true
	c := &Conversation{Tools: true, Offer: AgentTools, Check: AgentCalls, TurnLimit: AgentTurnLimit}
	_ = c.Ask("databases?")
	out, err := Exchange(context.Background(), client, c, func(Event) {})
	if err != nil || len(out.Pending) != 3 {
		t.Fatalf("outcome %+v, %v", out, err)
	}
	if in := out.Pending[0].RequestInput(); out.Pending[0].Problem != "" || in.Path != "/databases" || in.Method != "GET" {
		t.Fatalf("request = %+v", out.Pending[0])
	}
	if out.Pending[1].OperatorInput().Limit != OperatorRunsMax || !strings.Contains(out.Pending[2].Problem, "not JSON") {
		t.Fatalf("pending = %+v", out.Pending)
	}
	c.Answer(ToolResults(out.Pending, func(call ToolCall) (string, bool) { return "x", call.Problem == "" }))
	if _, err := Exchange(context.Background(), client, c, func(Event) {}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.raw[1], `{hours: 3`) {
		t.Fatalf("the call did not go back as it came: %s", got.raw[1])
	}
}

// A saved chat is carried on exactly as it was, inputs a model wrote that are not
// JSON and all.
func TestAConversationIsSavedAndLoadedAsItWas(t *testing.T) {
	c := &Conversation{ID: "chat", System: "system", Revision: "r1", Tools: true, Connection: 7, Turns: 2,
		Messages: []Message{
			{Role: "user", Blocks: []Block{text("why")}},
			{Role: "assistant", Blocks: []Block{{Type: BlockRaw, Raw: json.RawMessage(`{"type":"thinking","signature":"s"}`)},
				{Type: BlockToolUse, ToolID: "t1", ToolName: ToolRecentRuns, Input: json.RawMessage(`{hours: 3`)}}},
		},
		Pending:  []ToolCall{{ID: "t1", Name: ToolRecentRuns, Input: json.RawMessage(`{"limit":10}`), Notes: []string{"n"}}},
		Answered: []Block{{Type: BlockToolResult, ToolID: "t0", Text: "done"}},
	}
	raw, err := c.State()
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConversation("chat", raw)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.System != "system" || loaded.Revision != "r1" || !loaded.Tools || loaded.Connection != 7 || loaded.Turns != 2 ||
		len(loaded.Messages) != 2 || string(loaded.Messages[1].Blocks[1].Input) != `{hours: 3` ||
		string(loaded.Messages[1].Blocks[0].Raw) != `{"type":"thinking","signature":"s"}` ||
		len(loaded.Pending) != 1 || loaded.Pending[0].OperatorInput().Limit != 10 || loaded.Answered[0].Text != "done" {
		t.Fatalf("loaded = %+v", loaded)
	}
	loaded.ForgetReasoning()
	if len(loaded.Messages[1].Blocks) != 1 || loaded.Messages[1].Blocks[0].Type != BlockToolUse {
		t.Fatalf("the reasoning stayed: %+v", loaded.Messages[1].Blocks)
	}
}

// A long saved chat leaves out its oldest results rather than ending, and ends only
// when that is not enough.
func TestALongChatLeavesOutItsOldestResults(t *testing.T) {
	c := &Conversation{TurnLimit: AgentTurnLimit}
	big := strings.Repeat("r", maxHistory/10)
	for i := 0; i < 16; i++ {
		c.Messages = append(c.Messages,
			Message{Role: "assistant", Blocks: []Block{{Type: BlockToolUse, ToolID: "t", ToolName: ToolRecentRuns, Input: json.RawMessage(`{}`)}}},
			Message{Role: "user", Blocks: []Block{{Type: BlockToolResult, ToolID: "t", Text: big}}})
	}
	if err := c.Ask("and now?"); err != nil {
		t.Fatalf("a long chat was refused: %v", err)
	}
	// The question goes with the results nothing answered yet, in their message.
	if last := c.Messages[len(c.Messages)-1].Blocks; last[len(last)-1].Text != "and now?" {
		t.Fatalf("the question was not asked with the last results: %+v", last[len(last)-1])
	}
	if c.size() > maxHistory || c.Messages[1].Blocks[0].Text != LeftOut || c.Messages[len(c.Messages)-1].Blocks[0].Text != big {
		t.Fatalf("size %d, oldest %q", c.size(), c.Messages[1].Blocks[0].Text[:20])
	}
	words := &Conversation{TurnLimit: AgentTurnLimit, System: strings.Repeat("s", maxHistory+1)}
	if err := words.Ask("q"); !errors.Is(err, ErrFull) {
		t.Fatalf("a chat too long to shrink = %v", err)
	}
}

// A saved chat is loaded when it is not in memory, is its owner's alone, and a
// press that takes it over stops the one running it and carries on once it has put
// it down.
func TestASavedChatIsTakenOver(t *testing.T) {
	s := NewStore()
	loads := 0
	saved := map[string]*Conversation{"chat": {ID: "chat", OwnerID: 5, Connection: 9, Chat: &ChatInfo{Kind: "server"}}}
	load := func(_ context.Context, id string) (*Conversation, error) {
		loads++
		if c := saved[id]; c != nil {
			copied := *c
			return &copied, nil
		}
		return nil, ErrGone
	}
	ctx := context.Background()
	if _, err := s.TakeChat(ctx, "nothing", 5, 9, false, load); !errors.Is(err, ErrGone) {
		t.Fatalf("an unsaved chat = %v", err)
	}
	if _, err := s.TakeChat(ctx, "chat", 6, 9, false, load); !errors.Is(err, ErrGone) {
		t.Fatalf("somebody else's chat = %v", err)
	}
	first, err := s.TakeChat(ctx, "chat", 5, 9, false, load)
	if err != nil {
		t.Fatal(err)
	}
	pressCtx, stop := context.WithCancelCause(ctx)
	s.Running(first, stop)
	if !s.Busy("chat") {
		t.Fatal("a running chat is not busy")
	}
	if _, err := s.TakeChat(ctx, "chat", 5, 9, false, load); !errors.Is(err, ErrBusy) {
		t.Fatalf("a second press = %v", err)
	}
	// The first press stops when it is taken over, and puts the chat down.
	go func() {
		<-pressCtx.Done()
		if !errors.Is(context.Cause(pressCtx), ErrTakenOver) {
			t.Error("the press was not told it was taken over")
		}
		time.Sleep(20 * time.Millisecond)
		first.Messages = append(first.Messages, Message{Role: "user", Blocks: []Block{text("kept")}})
		s.Release(first)
	}()
	second, err := s.TakeChat(ctx, "chat", 5, 9, true, load)
	if err != nil {
		t.Fatal(err)
	}
	if second != first || len(second.Messages) != 1 {
		t.Fatalf("the chat was not carried on as it stood: %+v", second)
	}
	if err := s.Forget("chat"); !errors.Is(err, ErrBusy) {
		t.Fatalf("a running chat was forgotten: %v", err)
	}
	s.Retitle("chat", "renamed")
	s.Release(second)
	third, _ := s.TakeChat(ctx, "chat", 5, 9, false, load)
	if third.Chat.Title != "renamed" || loads != 2 {
		t.Fatalf("title %q after %d loads", third.Chat.Title, loads)
	}
	s.Release(third)
	if err := s.Forget("chat"); err != nil {
		t.Fatal(err)
	}
}

// A path is classified by the route it names as written and sent by the route it
// parses to, so a path that would parse to another — a fragment, a space, a host —
// is not a route's path.
func TestARequestPathReadsAsItIsRouted(t *testing.T) {
	for _, path := range []string{"/settings/a#/b", "/deploy/1/variables x", "/databases/%zz", "/deploy/1/x?"} {
		if CanonicalPath(path) {
			t.Errorf("%q reads as a route's path", path)
		}
	}
	for _, path := range []string{"/databases/3/query", "/deploy/12/environments/4/variables", "/files/a%2Fb", "/"} {
		if !CanonicalPath(path) {
			t.Errorf("%q does not read as a route's path", path)
		}
	}
	blocks := []Block{{Type: BlockToolUse, ToolID: "t1", ToolName: ToolAPIRequest, Input: json.RawMessage(`{"method":"GET","path":"/settings/a#/b"}`)}}
	calls, err := CheckCalls(AgentTools)(blocks)
	if err != nil || len(calls) != 1 || calls[0].Problem == "" {
		t.Fatalf("a path with a fragment was let through: %+v %v", calls, err)
	}
}
