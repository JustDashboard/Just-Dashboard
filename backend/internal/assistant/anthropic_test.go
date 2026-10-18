package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// The Anthropic adapter against a server that answers the way the Messages
// API does. Nothing here reaches Anthropic.

// sse writes a stream of server-sent events, each a name and its JSON.
func sse(w http.ResponseWriter, events ...[2]string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	for _, e := range events {
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e[0], e[1])
		w.(http.Flusher).Flush()
	}
}

func ev(name string, v any) [2]string {
	b, _ := json.Marshal(v)
	return [2]string{name, string(b)}
}

// messageEvents is a whole answer: a thinking block, the text, and a stop.
func messageEvents(text, stop string, extra ...[2]string) [][2]string {
	out := [][2]string{
		ev("message_start", map[string]any{"type": "message_start", "message": map[string]any{
			"id": "msg_1", "model": "claude-opus-5-5",
			"usage": map[string]int{"input_tokens": 1200, "cache_read_input_tokens": 900, "cache_creation_input_tokens": 0, "output_tokens": 1},
		}}),
		ev("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "thinking", "thinking": "", "signature": ""}}),
		ev("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "signature_delta", "signature": "sig-abc"}}),
		ev("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0}),
		ev("content_block_start", map[string]any{"type": "content_block_start", "index": 1, "content_block": map[string]any{"type": "text", "text": ""}}),
	}
	half := len(text) / 2
	for _, part := range []string{text[:half], text[half:]} {
		out = append(out, ev("content_block_delta", map[string]any{"type": "content_block_delta", "index": 1, "delta": map[string]any{"type": "text_delta", "text": part}}))
	}
	out = append(out, ev("content_block_stop", map[string]any{"type": "content_block_stop", "index": 1}))
	out = append(out, extra...)
	out = append(out,
		ev("ping", map[string]any{"type": "ping"}),
		ev("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop}, "usage": map[string]int{"output_tokens": 87}}),
		ev("message_stop", map[string]any{"type": "message_stop"}),
	)
	return out
}

type captured struct {
	mu       sync.Mutex
	requests []*http.Request
	bodies   []map[string]any
	raw      []string
}

func (c *captured) take(r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	var decoded map[string]any
	_ = json.Unmarshal(body, &decoded)
	c.requests = append(c.requests, r)
	c.bodies = append(c.bodies, decoded)
	c.raw = append(c.raw, string(body))
}

func anthropicFor(t *testing.T, model string, handler func(w http.ResponseWriter, r *http.Request, n int)) (Client, *captured) {
	t.Helper()
	got := &captured{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.take(r)
		handler(w, r, len(got.requests))
	}))
	t.Cleanup(server.Close)
	client, err := New(Settings{Provider: ProviderAnthropic, Model: model, Key: "sk-ant-test"}, Options{AnthropicURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	return client, got
}

func TestAnthropicSendsTheRequestTheModelExpects(t *testing.T) {
	client, got := anthropicFor(t, "claude-opus-5-5", func(w http.ResponseWriter, _ *http.Request, _ int) {
		sse(w, messageEvents("SELECT 1;", "end_turn")...)
	})
	thinking := json.RawMessage(`{"type":"thinking","thinking":"","signature":"earlier"}`)
	reply, err := client.Stream(context.Background(), Request{
		System: "the system prompt",
		Messages: []Message{
			{Role: "user", Blocks: []Block{{Type: BlockText, Text: "count the orders"}}},
			{Role: "assistant", Blocks: []Block{{Type: BlockRaw, Raw: thinking}, {Type: BlockToolUse, ToolID: "toolu_1", ToolName: "describe_tables", Input: json.RawMessage(`{"tables":["orders"]}`)}}},
			{Role: "user", Blocks: []Block{{Type: BlockToolResult, ToolID: "toolu_1", Text: "TABLE orders"}}},
		},
		Tools: []Tool{DescribeTables},
	}, func(Event) {})
	if err != nil {
		t.Fatal(err)
	}
	if reply.Stop != StopEnd {
		t.Errorf("stop %q", reply.Stop)
	}
	r, body := got.requests[0], got.bodies[0]
	if r.URL.Path != "/v1/messages" || r.Method != http.MethodPost {
		t.Errorf("%s %s", r.Method, r.URL.Path)
	}
	for header, want := range map[string]string{
		"x-api-key": "sk-ant-test", "anthropic-version": "2023-06-01",
		"anthropic-beta": "server-side-fallback-2026-07-01", "Content-Type": "application/json",
	} {
		if r.Header.Get(header) != want {
			t.Errorf("%s = %q, want %q", header, r.Header.Get(header), want)
		}
	}
	if body["model"] != "claude-opus-5-5" || body["stream"] != true || body["max_tokens"] != float64(64000) || body["fallbacks"] != "default" {
		t.Errorf("body %v", body)
	}
	if _, set := body["thinking"]; set {
		t.Error("thinking is configured; on these models it is adaptive by itself and any other setting is a 400")
	}
	if effort := body["output_config"].(map[string]any)["effort"]; effort != "medium" {
		t.Errorf("effort %v", effort)
	}
	if body["cache_control"].(map[string]any)["type"] != "ephemeral" {
		t.Error("the conversation is not cached")
	}
	system := body["system"].([]any)[0].(map[string]any)
	if system["text"] != "the system prompt" || system["cache_control"] == nil {
		t.Errorf("system %v", system)
	}
	tool := body["tools"].([]any)[0].(map[string]any)
	if tool["name"] != "describe_tables" || tool["eager_input_streaming"] != true || tool["input_schema"] == nil {
		t.Errorf("tool %v", tool)
	}
	messages := body["messages"].([]any)
	if !strings.Contains(got.raw[0], `"content":[`+string(thinking)+`,{"id":"toolu_1"`) {
		t.Errorf("a thinking block was not sent back as it came: %s", got.raw[0])
	}
	result := messages[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	if result["type"] != "tool_result" || result["tool_use_id"] != "toolu_1" || result["content"] != "TABLE orders" {
		t.Errorf("tool result %v", result)
	}
}

// Haiku has no server-side fallback: the parameter is left off rather than
// sent to be ignored.
func TestAnthropicOffersTheFallbackOnlyWhereThereIsOne(t *testing.T) {
	client, got := anthropicFor(t, "claude-haiku-5-5", func(w http.ResponseWriter, _ *http.Request, _ int) {
		sse(w, messageEvents("ok", "end_turn")...)
	})
	if _, err := client.Stream(context.Background(), Request{Messages: []Message{{Role: "user", Blocks: []Block{{Type: BlockText, Text: "hi"}}}}}, func(Event) {}); err != nil {
		t.Fatal(err)
	}
	if _, set := got.bodies[0]["fallbacks"]; set || got.requests[0].Header.Get("anthropic-beta") != "" {
		t.Errorf("fallback offered to Haiku: %v", got.bodies[0])
	}
	if _, set := got.bodies[0]["tools"]; set {
		t.Error("tools sent when the request had none")
	}
}

func TestAnthropicStreamsAndAssemblesTheAnswer(t *testing.T) {
	toolUse := [][2]string{
		ev("content_block_start", map[string]any{"type": "content_block_start", "index": 2, "content_block": map[string]any{"type": "tool_use", "id": "toolu_9", "name": "describe_tables", "input": map[string]any{}}}),
		ev("content_block_delta", map[string]any{"type": "content_block_delta", "index": 2, "delta": map[string]any{"type": "input_json_delta", "partial_json": `{"tables": ["ord`}}),
		ev("content_block_delta", map[string]any{"type": "content_block_delta", "index": 2, "delta": map[string]any{"type": "input_json_delta", "partial_json": `ers"]}`}}),
		ev("content_block_stop", map[string]any{"type": "content_block_stop", "index": 2}),
	}
	client, _ := anthropicFor(t, "claude-opus-5-5", func(w http.ResponseWriter, _ *http.Request, _ int) {
		sse(w, messageEvents("Let me read orders.", "tool_use", toolUse...)...)
	})
	var text strings.Builder
	thought := 0
	reply, err := client.Stream(context.Background(), Request{Messages: []Message{{Role: "user", Blocks: []Block{{Type: BlockText, Text: "x"}}}}}, func(e Event) {
		if e.Thinking {
			thought++
		}
		text.WriteString(e.Text)
	})
	if err != nil {
		t.Fatal(err)
	}
	if text.String() != "Let me read orders." || thought != 1 {
		t.Errorf("streamed %q, thinking %d", text.String(), thought)
	}
	if reply.Stop != StopTool || reply.Model != "claude-opus-5-5" {
		t.Errorf("stop %q model %q", reply.Stop, reply.Model)
	}
	if reply.Usage != (Usage{Input: 1200, Output: 87, CacheRead: 900}) {
		t.Errorf("usage %+v", reply.Usage)
	}
	if len(reply.Blocks) != 3 {
		t.Fatalf("blocks %+v", reply.Blocks)
	}
	if string(reply.Blocks[0].Raw) != `{"signature":"sig-abc","thinking":"","type":"thinking"}` {
		t.Errorf("thinking block %s", reply.Blocks[0].Raw)
	}
	if reply.Blocks[1].Text != "Let me read orders." {
		t.Errorf("text block %+v", reply.Blocks[1])
	}
	if b := reply.Blocks[2]; b.Type != BlockToolUse || b.ToolID != "toolu_9" || string(b.Input) != `{"tables": ["orders"]}` {
		t.Errorf("tool block %+v", b)
	}
	calls, err := Calls(reply.Blocks)
	if err != nil || len(calls) != 1 || calls[0].Tables[0] != "orders" {
		t.Errorf("calls %+v, %v", calls, err)
	}
}

// After a refusal retried on a fallback model, what the declined model began
// is not sent back, except the text the fallback model continued from.
func TestAnthropicDropsWhatTheDeclinedModelBeganBeforeAFallback(t *testing.T) {
	client, _ := anthropicFor(t, "claude-opus-5-5", func(w http.ResponseWriter, _ *http.Request, _ int) {
		sse(w,
			ev("message_start", map[string]any{"type": "message_start", "message": map[string]any{"model": "claude-opus-4-8", "usage": map[string]int{"input_tokens": 5}}}),
			ev("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "thinking", "thinking": ""}}),
			ev("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "signature_delta", "signature": "declined"}}),
			ev("content_block_start", map[string]any{"type": "content_block_start", "index": 1, "content_block": map[string]any{"type": "text", "text": "Partial "}}),
			ev("content_block_start", map[string]any{"type": "content_block_start", "index": 2, "content_block": map[string]any{"type": "fallback", "from": map[string]string{"model": "claude-opus-5-5"}, "to": map[string]string{"model": "claude-opus-4-8"}}}),
			ev("content_block_start", map[string]any{"type": "content_block_start", "index": 3, "content_block": map[string]any{"type": "text", "text": "answer."}}),
			ev("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn"}, "usage": map[string]int{"output_tokens": 4}}),
			ev("message_stop", map[string]any{"type": "message_stop"}),
		)
	})
	reply, err := client.Stream(context.Background(), Request{Messages: []Message{{Role: "user", Blocks: []Block{{Type: BlockText, Text: "x"}}}}}, func(Event) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.Blocks) != 2 || reply.Blocks[0].Text != "Partial " || reply.Blocks[1].Text != "answer." {
		t.Errorf("blocks %+v", reply.Blocks)
	}
	if reply.Model != "claude-opus-4-8" {
		t.Errorf("served by %q", reply.Model)
	}
}

func TestAnthropicReportsARefusal(t *testing.T) {
	client, _ := anthropicFor(t, "claude-opus-5-5", func(w http.ResponseWriter, _ *http.Request, _ int) {
		sse(w,
			ev("message_start", map[string]any{"type": "message_start", "message": map[string]any{"model": "claude-opus-5-5"}}),
			ev("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "refusal"}}),
			ev("message_stop", map[string]any{"type": "message_stop"}),
		)
	})
	reply, err := client.Stream(context.Background(), Request{Messages: []Message{{Role: "user", Blocks: []Block{{Type: BlockText, Text: "x"}}}}}, func(Event) {})
	if err != nil || reply.Stop != StopRefusal || len(reply.Blocks) != 0 {
		t.Errorf("reply %+v, %v", reply, err)
	}
}

func TestAnthropicFailuresAreSaidInWords(t *testing.T) {
	for _, c := range []struct {
		name   string
		status int
		field  string
		says   string
	}{
		{"a refused key", http.StatusUnauthorized, "key", "did not accept the key"},
		{"a missing model", http.StatusNotFound, "model", "no such model"},
		{"a bad request", http.StatusBadRequest, "", "max_tokens: too big"},
	} {
		client, got := anthropicFor(t, "claude-opus-5-5", func(w http.ResponseWriter, _ *http.Request, _ int) {
			w.WriteHeader(c.status)
			fmt.Fprint(w, `{"type":"error","error":{"type":"invalid_request_error","message":"max_tokens: too big"}}`)
		})
		_, err := client.Stream(context.Background(), Request{Messages: []Message{{Role: "user", Blocks: []Block{{Type: BlockText, Text: "x"}}}}}, func(Event) {})
		pe, ok := IsProviderError(err)
		if !ok || pe.Status != c.status || pe.Field != c.field || !strings.Contains(pe.Message, c.says) || pe.Retryable {
			t.Errorf("%s: %+v", c.name, pe)
		}
		if len(got.requests) != 1 {
			t.Errorf("%s: asked %d times; a refusal is not retried", c.name, len(got.requests))
		}
	}
}

func TestAnthropicAsksAgainWhileBusy(t *testing.T) {
	client, got := anthropicFor(t, "claude-opus-5-5", func(w http.ResponseWriter, _ *http.Request, n int) {
		if n == 1 {
			w.WriteHeader(529)
			fmt.Fprint(w, `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)
			return
		}
		sse(w, messageEvents("SELECT 1;", "end_turn")...)
	})
	reply, err := client.Stream(context.Background(), Request{Messages: []Message{{Role: "user", Blocks: []Block{{Type: BlockText, Text: "x"}}}}}, func(Event) {})
	if err != nil || len(got.requests) != 2 || reply.Blocks[1].Text != "SELECT 1;" {
		t.Errorf("after an overload: %d requests, %+v, %v", len(got.requests), reply, err)
	}
}

func TestAnthropicReportsAnAnswerThatFailsPartWay(t *testing.T) {
	client, _ := anthropicFor(t, "claude-opus-5-5", func(w http.ResponseWriter, _ *http.Request, _ int) {
		sse(w,
			ev("message_start", map[string]any{"type": "message_start", "message": map[string]any{"model": "claude-opus-5-5"}}),
			ev("error", map[string]any{"type": "error", "error": map[string]string{"type": "overloaded_error", "message": "Overloaded"}}),
		)
	})
	_, err := client.Stream(context.Background(), Request{Messages: []Message{{Role: "user", Blocks: []Block{{Type: BlockText, Text: "x"}}}}}, func(Event) {})
	if pe, ok := IsProviderError(err); !ok || !strings.Contains(pe.Message, "Overloaded") {
		t.Errorf("err %v", err)
	}

	cut, _ := anthropicFor(t, "claude-opus-5-5", func(w http.ResponseWriter, _ *http.Request, _ int) {
		sse(w, ev("message_start", map[string]any{"type": "message_start", "message": map[string]any{
			"model": "claude-opus-5-5",
			"usage": map[string]int{"input_tokens": 1200, "cache_read_input_tokens": 900, "output_tokens": 1},
		}}))
	})
	reply, err := cut.Stream(context.Background(), Request{Messages: []Message{{Role: "user", Blocks: []Block{{Type: BlockText, Text: "x"}}}}}, func(Event) {})
	if pe, ok := IsProviderError(err); !ok || !strings.Contains(pe.Message, "cut off") {
		t.Errorf("a stream that ended early: %v", err)
	}
	// The audit entry of a turn that did not finish still says what it cost.
	if reply.Usage != (Usage{Input: 1200, Output: 1, CacheRead: 900}) || reply.Model != "claude-opus-5-5" {
		t.Errorf("an early end's usage %+v, model %q", reply.Usage, reply.Model)
	}
}

func TestAnthropicProbeReadsTheModelWithoutAskingIt(t *testing.T) {
	client, got := anthropicFor(t, "claude-sonnet-5-5", func(w http.ResponseWriter, r *http.Request, _ int) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models/claude-sonnet-5-5" {
			w.WriteHeader(http.StatusTeapot)
			return
		}
		fmt.Fprint(w, `{"id":"claude-sonnet-5-5","type":"model"}`)
	})
	if err := client.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got.requests[0].Header.Get("x-api-key") != "sk-ant-test" {
		t.Error("the probe did not carry the key")
	}

	missing, _ := anthropicFor(t, "claude-opus-5-5", func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"type":"error","error":{"type":"not_found_error","message":"model: claude-opus-5-5"}}`)
	})
	pe, _ := IsProviderError(missing.Probe(context.Background()))
	if pe == nil || pe.Field != "model" || pe.Message != "this key cannot use Claude Opus 5.5" {
		t.Errorf("probe of a model the key lacks: %+v", pe)
	}
}

// A redirect is not followed: the key travels in a header, and a header goes
// wherever the redirect points.
func TestProviderRequestsDoNotFollowRedirects(t *testing.T) {
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the redirect was followed, with key %q", r.Header.Get("x-api-key"))
	}))
	defer elsewhere.Close()
	client, _ := anthropicFor(t, "claude-opus-5-5", func(w http.ResponseWriter, r *http.Request, _ int) {
		http.Redirect(w, r, elsewhere.URL+"/v1/models/x", http.StatusTemporaryRedirect)
	})
	if err := client.Probe(context.Background()); err == nil {
		t.Error("a redirect passed for the provider's answer")
	}
}
