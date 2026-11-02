package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
)

// OpenAI's chat completions, which OpenAI, the gateways and every local model
// server speak: Ollama, LM Studio, vLLM, llama.cpp. What is sent is what all of
// them take — the model, the messages, the stream, and the ask for the usage
// in the last chunk, which a server that does not know it ignores — so no
// field one of them lacks turns the request down. The answer's length is left
// to the server, whose context the dashboard does not know. Tools are sent only
// to a provider whose setup says it takes them.

type openAIClient struct {
	http  *http.Client
	base  string
	key   string
	model string
	tools bool
}

// Tools is what the setup says: a model that has none refuses a request
// offering them, and many local models have none.
func (c *openAIClient) Tools() bool { return c.tools }

// Models reads the server's own list, which every one of them serves and which
// costs nothing. A server with no list has nothing to choose from here.
func (c *openAIClient) Models(ctx context.Context) ([]Model, error) {
	ids, err := c.modelIDs(ctx)
	if err != nil {
		return nil, err
	}
	sort.Strings(ids)
	out := make([]Model, 0, len(ids))
	for _, id := range ids {
		out = append(out, Model{ID: id, Label: id})
	}
	return out, nil
}

// modelIDs is the server's list, nil for a server that keeps none.
func (c *openAIClient) modelIDs(ctx context.Context) ([]string, error) {
	res, err := send(ctx, c.http, func() (*http.Request, error) {
		req, err := http.NewRequest(http.MethodGet, c.base+"/models", nil)
		if err == nil {
			c.authorise(req)
		}
		return req, err
	}, c.name())
	if err != nil {
		return nil, err
	}
	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		res.Body.Close()
		return nil, nil
	default:
		failure := readFailure(res, c.name())
		if failure.Field == "model" {
			failure.Field = "baseUrl"
		}
		if res.StatusCode == http.StatusUnauthorized && c.key == "" {
			failure.Message = c.name() + " asks for a key"
		}
		return nil, failure
	}
	defer res.Body.Close()
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&list); err != nil {
		return nil, &ProviderError{Message: c.name() + " answered, but not with a list of models: is the address the one that ends in /v1?", Field: "baseUrl"}
	}
	ids := make([]string, 0, len(list.Data))
	for _, m := range list.Data {
		ids = append(ids, m.ID)
	}
	return ids, nil
}

// name is how a refusal names the server: by its address, since "OpenAI"
// would be wrong for most of them.
func (c *openAIClient) name() string {
	if u, err := url.Parse(c.base); err == nil && u.Host != "" {
		return "The server at " + u.Host
	}
	return "The server"
}

func (c *openAIClient) authorise(req *http.Request) {
	if c.key != "" {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
}

// Probe reads the server's list of models and checks the model is on it. A
// server that lists nothing still answers completions; the first question will
// say whether the model is there.
func (c *openAIClient) Probe(ctx context.Context) error {
	ids, err := c.modelIDs(ctx)
	if err != nil {
		return err
	}
	if ids == nil {
		return nil
	}
	if len(ids) == 0 {
		return &ProviderError{Message: c.name() + " has no models yet", Field: "model"}
	}
	if slices.Contains(ids, c.model) {
		return nil
	}
	sort.Strings(ids)
	shown := ids[:min(len(ids), 12)]
	more := ""
	if len(ids) > len(shown) {
		more = fmt.Sprintf(" and %d more", len(ids)-len(shown))
	}
	return &ProviderError{
		Message: fmt.Sprintf("%s has no model called %s; it has %s%s", c.name(), c.model, strings.Join(shown, ", "), more),
		Field:   "model",
	}
}

type openAIMessage struct {
	Role string `json:"role"`
	// Content is empty on an assistant message that only calls tools, which is
	// sent as null: several servers refuse an empty string there.
	Content    *string          `json:"content"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func textMessage(role, text string) openAIMessage {
	return openAIMessage{Role: role, Content: &text}
}

// openAIMessages writes one message of the conversation in this API's shape: a
// tool's result is a message of its own, each answering its call by id, and
// they come before any text that was sent with them, straight after the call.
func openAIMessages(m Message) []openAIMessage {
	var out []openAIMessage
	var text []string
	var calls []openAIToolCall
	for _, b := range m.Blocks {
		switch b.Type {
		case BlockText:
			text = append(text, b.Text)
		case BlockToolUse:
			call := openAIToolCall{ID: b.ToolID, Type: "function"}
			call.Function.Name, call.Function.Arguments = b.ToolName, string(b.Input)
			if call.Function.Arguments == "" {
				call.Function.Arguments = "{}"
			}
			calls = append(calls, call)
		case BlockToolResult:
			result := b.Text
			out = append(out, openAIMessage{Role: "tool", ToolCallID: b.ToolID, Content: &result})
		}
	}
	if len(calls) > 0 {
		message := openAIMessage{Role: m.Role, ToolCalls: calls}
		if len(text) > 0 {
			joined := strings.Join(text, "\n\n")
			message.Content = &joined
		}
		return append(out, message)
	}
	if len(text) > 0 {
		out = append(out, textMessage(m.Role, strings.Join(text, "\n\n")))
	}
	return out
}

func (c *openAIClient) body(req Request) ([]byte, error) {
	messages := []openAIMessage{}
	if req.System != "" {
		messages = append(messages, textMessage("system", req.System))
	}
	for _, m := range req.Messages {
		messages = append(messages, openAIMessages(m)...)
	}
	body := map[string]any{
		"model": c.model, "messages": messages, "stream": true,
		// The usage arrives in a last chunk of its own; a server that does not
		// know the option ignores it, and the turn is recorded without counts.
		"stream_options": map[string]bool{"include_usage": true},
	}
	if len(req.Tools) > 0 && c.tools {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{"type": "function", "function": map[string]any{
				"name": t.Name, "description": t.Description, "parameters": t.Schema,
			}})
		}
		body["tools"] = tools
	}
	return json.Marshal(body)
}

// openAICallDelta is a piece of a tool call as it streams: the first names it,
// and the rest carry pieces of its arguments, by the index of the call.
type openAICallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// openAICalls gathers the calls of an answer in the order they were begun.
type openAICalls struct {
	order []int
	byAt  map[int]*Block
	args  map[int]*strings.Builder
}

func (o *openAICalls) add(delta openAICallDelta) {
	if o.byAt == nil {
		o.byAt, o.args = map[int]*Block{}, map[int]*strings.Builder{}
	}
	block := o.byAt[delta.Index]
	if block == nil {
		block = &Block{Type: BlockToolUse}
		o.byAt[delta.Index], o.args[delta.Index] = block, &strings.Builder{}
		o.order = append(o.order, delta.Index)
	}
	if delta.ID != "" {
		block.ToolID = delta.ID
	}
	if delta.Function.Name != "" {
		block.ToolName = delta.Function.Name
	}
	o.args[delta.Index].WriteString(delta.Function.Arguments)
}

// blocks is the calls whole. A server that gives a call no id is given one here,
// since the result has to answer it by something.
func (o *openAICalls) blocks() []Block {
	out := make([]Block, 0, len(o.order))
	for _, at := range o.order {
		block := *o.byAt[at]
		block.Input = json.RawMessage(o.args[at].String())
		if len(block.Input) == 0 {
			block.Input = json.RawMessage("{}")
		}
		if block.ToolID == "" {
			block.ToolID = fmt.Sprintf("call_%d", at)
		}
		out = append(out, block)
	}
	return out
}

func (c *openAIClient) Stream(ctx context.Context, req Request, emit func(Event)) (Reply, error) {
	body, err := c.body(req)
	if err != nil {
		return Reply{}, err
	}
	res, err := send(ctx, c.http, func() (*http.Request, error) {
		r, err := http.NewRequest(http.MethodPost, c.base+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		c.authorise(r)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "text/event-stream")
		return r, nil
	}, c.name())
	if err != nil {
		return Reply{}, err
	}
	if res.StatusCode != http.StatusOK {
		return Reply{}, readFailure(res, c.name())
	}
	defer res.Body.Close()
	var (
		text     strings.Builder
		finish   string
		usage    Usage
		model    string
		finished bool
		thinking bool
		calls    openAICalls
	)
	err = readEvents(res.Body, func(_, data string) error {
		if strings.TrimSpace(data) == "[DONE]" {
			finished = true
			return errStreamDone
		}
		var chunk struct {
			Model   string `json:"model"`
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
					// What reasoning models served this way think before
					// they answer, under either of the names servers use.
					Reasoning        string            `json:"reasoning"`
					ReasoningContent string            `json:"reasoning_content"`
					ToolCalls        []openAICallDelta `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				Prompt     int `json:"prompt_tokens"`
				Completion int `json:"completion_tokens"`
			} `json:"usage"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return &ProviderError{Message: c.name() + " sent an answer this dashboard could not read", Raw: data}
		}
		if chunk.Error != nil {
			return &ProviderError{Message: c.name() + " stopped answering: " + chunk.Error.Message, Retryable: true, Raw: data}
		}
		if chunk.Model != "" {
			model = chunk.Model
		}
		if chunk.Usage != nil {
			usage = Usage{Input: chunk.Usage.Prompt, Output: chunk.Usage.Completion}
		}
		for _, choice := range chunk.Choices {
			if (choice.Delta.Reasoning != "" || choice.Delta.ReasoningContent != "") && !thinking {
				thinking = true
				emit(Event{Thinking: true})
			}
			if choice.Delta.Content != "" {
				text.WriteString(choice.Delta.Content)
				emit(Event{Text: choice.Delta.Content})
			}
			for _, delta := range choice.Delta.ToolCalls {
				calls.add(delta)
			}
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				finish = *choice.FinishReason
			}
		}
		return nil
	})
	// A turn that ends early still cost what the server counted by then.
	spent := Reply{Usage: usage, Model: model}
	if err != nil && !errors.Is(err, errStreamDone) {
		if ctx.Err() != nil {
			return spent, ctx.Err()
		}
		return spent, err
	}
	if !finished && finish == "" {
		if ctx.Err() != nil {
			return spent, ctx.Err()
		}
		return spent, &ProviderError{Message: c.name() + "'s answer was cut off before it finished", Retryable: true}
	}
	out := Reply{Usage: usage, Model: model, Stop: openAIStop(finish)}
	if model == "" {
		out.Model = c.model
	}
	if text.Len() > 0 {
		out.Blocks = []Block{{Type: BlockText, Text: text.String()}}
	}
	// A server may end an answer that called tools with any finish reason, and
	// the calls are what it asked for whichever it was.
	if tools := calls.blocks(); len(tools) > 0 && finish != "length" {
		out.Blocks = append(out.Blocks, tools...)
		out.Stop = StopTool
	}
	return out, nil
}

func openAIStop(reason string) Stop {
	switch reason {
	case "length":
		return StopLength
	case "content_filter":
		return StopRefusal
	}
	return StopEnd
}
