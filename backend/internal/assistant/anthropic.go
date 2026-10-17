package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// The Messages API, streamed, spoken directly over HTTP. Its wire format is
// small and stable, and the OpenAI-compatible half is plain HTTP regardless;
// the official SDK would add a module tree for the one request this sends.

const (
	anthropicVersion = "2023-06-01"
	// A refusal by one model's safety classifier is retried on the model
	// Anthropic recommends for that kind of refusal, inside the same request.
	anthropicFallbackBeta = "server-side-fallback-2026-07-01"
	// Thinking counts toward it as well as the answer.
	anthropicMaxTokens = 64000
	// Drafting a statement and explaining an error or a plan are routine; the
	// model's default effort answers them well and quickly.
	anthropicEffort = "medium"
)

// fallbackModels are the models the server-side fallback is offered for.
// Haiku has none: with it a declined request simply stays declined.
var fallbackModels = map[string]bool{"claude-opus-5-5": true, "claude-sonnet-5-5": true}

type anthropicClient struct {
	http  *http.Client
	base  string
	key   string
	model string
}

func (c *anthropicClient) Tools() bool { return true }

func (c *anthropicClient) headers(req *http.Request) {
	req.Header.Set("x-api-key", c.key)
	req.Header.Set("anthropic-version", anthropicVersion)
}

func (c *anthropicClient) Probe(ctx context.Context) error {
	res, err := send(ctx, c.http, func() (*http.Request, error) {
		req, err := http.NewRequest(http.MethodGet, c.base+"/v1/models/"+url.PathEscape(c.model), nil)
		if err == nil {
			c.headers(req)
		}
		return req, err
	}, "Anthropic")
	if err != nil {
		return err
	}
	if res.StatusCode == http.StatusOK {
		res.Body.Close()
		return nil
	}
	failure := readFailure(res, "Anthropic")
	if res.StatusCode == http.StatusNotFound {
		failure.Message = fmt.Sprintf("this key cannot use %s", ModelLabel(ProviderAnthropic, c.model))
	}
	return failure
}

type cacheControl struct {
	Type string `json:"type"`
}

var ephemeral = &cacheControl{Type: "ephemeral"}

type anthropicText struct {
	Type         string        `json:"type"`
	Text         string        `json:"text"`
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
	// Fragments of the input stream as they are written rather than after
	// the server has buffered and checked them; the input is checked here.
	EagerInputStreaming bool `json:"eager_input_streaming"`
}

type anthropicMessage struct {
	Role    string            `json:"role"`
	Content []json.RawMessage `json:"content"`
}

type anthropicRequest struct {
	Model        string             `json:"model"`
	MaxTokens    int                `json:"max_tokens"`
	Stream       bool               `json:"stream"`
	System       []anthropicText    `json:"system,omitempty"`
	Messages     []anthropicMessage `json:"messages"`
	Tools        []anthropicTool    `json:"tools,omitempty"`
	OutputConfig map[string]string  `json:"output_config"`
	// The system prompt carries its own breakpoint, because the schema in it
	// is the large part every turn shares; this one follows the conversation
	// as it grows.
	CacheControl *cacheControl `json:"cache_control"`
	Fallbacks    string        `json:"fallbacks,omitempty"`
}

func (c *anthropicClient) body(req Request) ([]byte, error) {
	out := anthropicRequest{
		Model: c.model, MaxTokens: anthropicMaxTokens, Stream: true,
		OutputConfig: map[string]string{"effort": anthropicEffort},
		CacheControl: ephemeral,
	}
	if req.System != "" {
		out.System = []anthropicText{{Type: "text", Text: req.System, CacheControl: ephemeral}}
	}
	if fallbackModels[c.model] {
		out.Fallbacks = "default"
	}
	for _, t := range req.Tools {
		out.Tools = append(out.Tools, anthropicTool{Name: t.Name, Description: t.Description, InputSchema: t.Schema, EagerInputStreaming: true})
	}
	for _, m := range req.Messages {
		msg := anthropicMessage{Role: m.Role, Content: []json.RawMessage{}}
		for _, b := range m.Blocks {
			encoded, err := encodeAnthropicBlock(b)
			if err != nil {
				return nil, err
			}
			msg.Content = append(msg.Content, encoded)
		}
		out.Messages = append(out.Messages, msg)
	}
	return json.Marshal(out)
}

func encodeAnthropicBlock(b Block) (json.RawMessage, error) {
	switch b.Type {
	case BlockText:
		return json.Marshal(map[string]string{"type": "text", "text": b.Text})
	case BlockToolUse:
		input := b.Input
		if len(input) == 0 {
			input = json.RawMessage(`{}`)
		}
		return json.Marshal(map[string]any{"type": "tool_use", "id": b.ToolID, "name": b.ToolName, "input": input})
	case BlockToolResult:
		result := map[string]any{"type": "tool_result", "tool_use_id": b.ToolID, "content": b.Text}
		if b.IsError {
			result["is_error"] = true
		}
		return json.Marshal(result)
	case BlockRaw:
		return b.Raw, nil
	}
	return nil, fmt.Errorf("a %q block cannot be sent to Anthropic", b.Type)
}

func (c *anthropicClient) Stream(ctx context.Context, req Request, emit func(Event)) (Reply, error) {
	body, err := c.body(req)
	if err != nil {
		return Reply{}, err
	}
	res, err := send(ctx, c.http, func() (*http.Request, error) {
		r, err := http.NewRequest(http.MethodPost, c.base+"/v1/messages", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		c.headers(r)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "text/event-stream")
		if fallbackModels[c.model] {
			r.Header.Set("anthropic-beta", anthropicFallbackBeta)
		}
		return r, nil
	}, "Anthropic")
	if err != nil {
		return Reply{}, err
	}
	if res.StatusCode != http.StatusOK {
		return Reply{}, readFailure(res, "Anthropic")
	}
	defer res.Body.Close()
	s := &anthropicStream{blocks: map[int]*streamBlock{}, emit: emit}
	if err := readEvents(res.Body, s.event); err != nil && !errors.Is(err, errStreamDone) {
		if ctx.Err() != nil {
			return Reply{}, ctx.Err()
		}
		return Reply{}, err
	}
	if !s.done {
		if ctx.Err() != nil {
			return Reply{}, ctx.Err()
		}
		return Reply{}, &ProviderError{Message: "Anthropic's answer was cut off before it finished", Retryable: true}
	}
	return s.reply(), nil
}

var errStreamDone = errors.New("message_stop")

type streamBlock struct {
	kind      string
	start     json.RawMessage
	text      strings.Builder
	signature string
	input     strings.Builder
	id, name  string
}

type anthropicStream struct {
	blocks map[int]*streamBlock
	emit   func(Event)
	usage  Usage
	model  string
	stop   string
	done   bool
}

func (s *anthropicStream) event(name, data string) error {
	var head struct {
		Type    string          `json:"type"`
		Index   int             `json:"index"`
		Message json.RawMessage `json:"message"`
		Block   json.RawMessage `json:"content_block"`
		Delta   json.RawMessage `json:"delta"`
		Usage   *anthropicUsage `json:"usage"`
		Error   *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(data), &head); err != nil {
		return &ProviderError{Message: "Anthropic sent an answer this dashboard could not read", Raw: data}
	}
	switch head.Type {
	case "message_start":
		var msg struct {
			Model string          `json:"model"`
			Usage *anthropicUsage `json:"usage"`
		}
		_ = json.Unmarshal(head.Message, &msg)
		s.model = msg.Model
		s.usage.merge(msg.Usage)
	case "content_block_start":
		var block struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Thinking string `json:"thinking"`
			ID       string `json:"id"`
			Name     string `json:"name"`
		}
		_ = json.Unmarshal(head.Block, &block)
		b := &streamBlock{kind: block.Type, start: head.Block, id: block.ID, name: block.Name}
		b.text.WriteString(block.Text + block.Thinking)
		s.blocks[head.Index] = b
		switch block.Type {
		case "thinking", "redacted_thinking":
			s.emit(Event{Thinking: true})
		case "text":
			if block.Text != "" {
				s.emit(Event{Text: block.Text})
			}
		}
	case "content_block_delta":
		b := s.blocks[head.Index]
		if b == nil {
			return nil
		}
		var delta struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			Thinking    string `json:"thinking"`
			Signature   string `json:"signature"`
			PartialJSON string `json:"partial_json"`
		}
		_ = json.Unmarshal(head.Delta, &delta)
		switch delta.Type {
		case "text_delta":
			b.text.WriteString(delta.Text)
			s.emit(Event{Text: delta.Text})
		case "thinking_delta":
			b.text.WriteString(delta.Thinking)
		case "signature_delta":
			b.signature += delta.Signature
		case "input_json_delta":
			b.input.WriteString(delta.PartialJSON)
		}
	case "message_delta":
		var delta struct {
			StopReason string `json:"stop_reason"`
		}
		_ = json.Unmarshal(head.Delta, &delta)
		if delta.StopReason != "" {
			s.stop = delta.StopReason
		}
		s.usage.merge(head.Usage)
	case "message_stop":
		s.done = true
		return errStreamDone
	case "error":
		message := "the answer failed part-way"
		if head.Error != nil && head.Error.Message != "" {
			message = head.Error.Message
		}
		return &ProviderError{Message: "Anthropic stopped answering: " + message, Retryable: true, Raw: data}
	}
	return nil
}

type anthropicUsage struct {
	Input      int `json:"input_tokens"`
	Output     int `json:"output_tokens"`
	CacheRead  int `json:"cache_read_input_tokens"`
	CacheWrite int `json:"cache_creation_input_tokens"`
}

// merge keeps the larger of each count: message_delta repeats the totals
// message_start began, and the output count only grows.
func (u *Usage) merge(a *anthropicUsage) {
	if a == nil {
		return
	}
	u.Input, u.Output = max(u.Input, a.Input), max(u.Output, a.Output)
	u.CacheRead, u.CacheWrite = max(u.CacheRead, a.CacheRead), max(u.CacheWrite, a.CacheWrite)
}

// reply assembles the answer in the order the blocks were written.
//
// A refusal that was retried on a fallback model leaves a `fallback` block at
// the point it switched. What the declined model wrote before it is not sent
// back — its thinking and any tool call it began — except its text, which
// the fallback model continued from.
func (s *anthropicStream) reply() Reply {
	indexes := make([]int, 0, len(s.blocks))
	for i := range s.blocks {
		indexes = append(indexes, i)
	}
	sort.Ints(indexes)
	boundary := -1
	for at, i := range indexes {
		if s.blocks[i].kind == "fallback" {
			boundary = at
		}
	}
	out := Reply{Usage: s.usage, Model: s.model, Stop: anthropicStop(s.stop)}
	for at, i := range indexes {
		b := s.blocks[i]
		switch b.kind {
		case "text":
			if b.text.Len() > 0 {
				out.Blocks = append(out.Blocks, Block{Type: BlockText, Text: b.text.String()})
			}
		case "thinking":
			if at < boundary {
				continue
			}
			raw, _ := json.Marshal(map[string]string{"type": "thinking", "thinking": b.text.String(), "signature": b.signature})
			out.Blocks = append(out.Blocks, Block{Type: BlockRaw, Raw: raw})
		case "redacted_thinking":
			if at < boundary {
				continue
			}
			out.Blocks = append(out.Blocks, Block{Type: BlockRaw, Raw: b.start})
		case "tool_use":
			if at < boundary {
				continue
			}
			input := json.RawMessage(strings.TrimSpace(b.input.String()))
			if len(input) == 0 || !json.Valid(input) {
				// Asked for with input that is not JSON: nothing will run
				// for it, and the call is checked as an empty request.
				input = json.RawMessage(`{}`)
			}
			out.Blocks = append(out.Blocks, Block{Type: BlockToolUse, ToolID: b.id, ToolName: b.name, Input: input})
		}
	}
	return out
}

func anthropicStop(reason string) Stop {
	switch reason {
	case "tool_use":
		return StopTool
	case "max_tokens", "model_context_window_exceeded":
		return StopLength
	case "refusal":
		return StopRefusal
	}
	return StopEnd
}
