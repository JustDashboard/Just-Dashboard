package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The OpenAI-compatible adapter against a server that answers as Ollama,
// LM Studio and the others do. Nothing here reaches a real model.

func openAIFor(t *testing.T, key string, handler func(w http.ResponseWriter, r *http.Request)) (Client, *captured) {
	t.Helper()
	got := &captured{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.take(r)
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	client, err := New(Settings{Provider: ProviderOpenAI, Model: "llama3.1:8b", BaseURL: server.URL + "/v1", Key: key}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	return client, got
}

func chunks(w http.ResponseWriter, lines ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, line := range lines {
		fmt.Fprintf(w, "data: %s\n\n", line)
		w.(http.Flusher).Flush()
	}
}

func delta(content string) string {
	b, _ := json.Marshal(map[string]any{"model": "llama3.1:8b", "choices": []any{map[string]any{"index": 0, "delta": map[string]string{"content": content}, "finish_reason": nil}}})
	return string(b)
}

func finish(reason string) string {
	b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]string{}, "finish_reason": reason}}})
	return string(b)
}

func TestOpenAISendsPlainChatAndStreamsTheAnswer(t *testing.T) {
	client, got := openAIFor(t, "", func(w http.ResponseWriter, r *http.Request) {
		chunks(w, delta("SELECT "), delta("count(*) FROM orders;"), finish("stop"),
			`{"choices":[],"usage":{"prompt_tokens":321,"completion_tokens":12}}`, "[DONE]")
	})
	if client.Tools() {
		t.Error("an OpenAI-compatible server is offered tools")
	}
	var streamed strings.Builder
	reply, err := client.Stream(context.Background(), Request{
		System: "rules and schema",
		Messages: []Message{
			{Role: "user", Blocks: []Block{{Type: BlockText, Text: "count the orders"}}},
			{Role: "assistant", Blocks: []Block{{Type: BlockRaw, Raw: json.RawMessage(`{"type":"thinking"}`)}, {Type: BlockText, Text: "SELECT 1;"}}},
			{Role: "user", Blocks: []Block{{Type: BlockText, Text: "only shipped ones"}}},
		},
	}, func(e Event) { streamed.WriteString(e.Text) })
	if err != nil {
		t.Fatal(err)
	}
	r, body := got.requests[0], got.bodies[0]
	if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "" {
		t.Errorf("%s, authorization %q", r.URL.Path, r.Header.Get("Authorization"))
	}
	if body["model"] != "llama3.1:8b" || body["stream"] != true {
		t.Errorf("body %v", body)
	}
	for _, unknown := range []string{"tools", "max_tokens", "temperature"} {
		if _, set := body[unknown]; set {
			t.Errorf("%s was sent to a server that may not know it", unknown)
		}
	}
	messages := body["messages"].([]any)
	roles := []string{}
	for _, m := range messages {
		roles = append(roles, m.(map[string]any)["role"].(string))
	}
	if strings.Join(roles, ",") != "system,user,assistant,user" || messages[2].(map[string]any)["content"] != "SELECT 1;" {
		t.Errorf("messages %v", messages)
	}
	if streamed.String() != "SELECT count(*) FROM orders;" || reply.Blocks[0].Text != streamed.String() {
		t.Errorf("streamed %q, reply %+v", streamed.String(), reply.Blocks)
	}
	if reply.Stop != StopEnd || reply.Usage != (Usage{Input: 321, Output: 12}) || reply.Model != "llama3.1:8b" {
		t.Errorf("reply %+v", reply)
	}
}

func TestOpenAIReadsHowAnAnswerEnded(t *testing.T) {
	for reason, want := range map[string]Stop{"length": StopLength, "content_filter": StopRefusal, "stop": StopEnd} {
		client, _ := openAIFor(t, "k", func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer k" {
				t.Error("the key was not sent")
			}
			chunks(w, delta("x"), finish(reason))
		})
		reply, err := client.Stream(context.Background(), Request{Messages: []Message{{Role: "user", Blocks: []Block{{Type: BlockText, Text: "q"}}}}}, func(Event) {})
		if err != nil || reply.Stop != want {
			t.Errorf("%s: %q, %v", reason, reply.Stop, err)
		}
	}
}

func TestOpenAIReportsFailures(t *testing.T) {
	refused, _ := openAIFor(t, "", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":{"message":"model \"llama9\" not found, try pulling it first"}}`)
	})
	_, err := refused.Stream(context.Background(), Request{Messages: []Message{{Role: "user", Blocks: []Block{{Type: BlockText, Text: "q"}}}}}, func(Event) {})
	pe, ok := IsProviderError(err)
	if !ok || !strings.Contains(pe.Message, "try pulling it first") || !strings.HasPrefix(pe.Message, "The server at 127.0.0.1:") {
		t.Errorf("refusal %+v", pe)
	}

	broken, _ := openAIFor(t, "", func(w http.ResponseWriter, _ *http.Request) {
		chunks(w, delta("SEL"), `{"error":{"message":"the model crashed"}}`)
	})
	_, err = broken.Stream(context.Background(), Request{Messages: []Message{{Role: "user", Blocks: []Block{{Type: BlockText, Text: "q"}}}}}, func(Event) {})
	if pe, ok := IsProviderError(err); !ok || !strings.Contains(pe.Message, "the model crashed") {
		t.Errorf("a failure part-way: %v", err)
	}
}

func TestOpenAIProbeChecksTheModelIsServed(t *testing.T) {
	list := `{"object":"list","data":[{"id":"qwen2.5-coder:7b"},{"id":"llama3.1:8b"}]}`
	client, got := openAIFor(t, "", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, list) })
	if err := client.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got.requests[0].URL.Path != "/v1/models" || got.requests[0].Method != http.MethodGet {
		t.Errorf("probed %s %s", got.requests[0].Method, got.requests[0].URL.Path)
	}

	other, _ := openAIFor(t, "", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":"mistral:7b"},{"id":"gemma3:4b"}]}`)
	})
	pe, _ := IsProviderError(other.Probe(context.Background()))
	if pe == nil || pe.Field != "model" || !strings.Contains(pe.Message, "has no model called llama3.1:8b; it has gemma3:4b, mistral:7b") {
		t.Errorf("a model the server does not have: %+v", pe)
	}

	locked, _ := openAIFor(t, "", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
	pe, _ = IsProviderError(locked.Probe(context.Background()))
	if pe == nil || pe.Field != "key" || !strings.HasSuffix(pe.Message, "asks for a key") {
		t.Errorf("a server that wants a key: %+v", pe)
	}

	unlisted, _ := openAIFor(t, "", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
	if err := unlisted.Probe(context.Background()); err != nil {
		t.Errorf("a server with no list of models: %v", err)
	}

	page, _ := openAIFor(t, "", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "<html>it works</html>") })
	pe, _ = IsProviderError(page.Probe(context.Background()))
	if pe == nil || pe.Field != "baseUrl" {
		t.Errorf("an address that is not the API: %+v", pe)
	}
}
