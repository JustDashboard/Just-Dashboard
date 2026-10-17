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

// OpenAI's chat completions, which OpenAI, the gateways and every local model
// server speak: Ollama, LM Studio, vLLM, llama.cpp. What is sent is what all of
// them take — the model, the messages, the stream, and the ask for the usage
// in the last chunk, which a server that does not know it ignores — so no
// field one of them lacks turns the request down. The answer's length is left
// to the server, whose context the dashboard does not know.

type openAIClient struct {
	http  *http.Client
	base  string
	key   string
	model string
}

// Tools is false: a model that has none refuses a request offering them,
// and most local models have none.
func (c *openAIClient) Tools() bool { return false }

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

// Probe reads the server's list of models, which every one of them serves
// and which costs nothing, and checks the model is on it.
func (c *openAIClient) Probe(ctx context.Context) error {
	res, err := send(ctx, c.http, func() (*http.Request, error) {
		req, err := http.NewRequest(http.MethodGet, c.base+"/models", nil)
		if err == nil {
			c.authorise(req)
		}
		return req, err
	}, c.name())
	if err != nil {
		return err
	}
	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		// A server that lists nothing still answers completions; the first
		// question will say whether the model is there.
		res.Body.Close()
		return nil
	default:
		failure := readFailure(res, c.name())
		if failure.Field == "model" {
			failure.Field = "baseUrl"
		}
		if res.StatusCode == http.StatusUnauthorized && c.key == "" {
			failure.Message = c.name() + " asks for a key"
		}
		return failure
	}
	defer res.Body.Close()
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&list); err != nil {
		return &ProviderError{Message: c.name() + " answered, but not with a list of models: is the address the one that ends in /v1?", Field: "baseUrl"}
	}
	ids := make([]string, 0, len(list.Data))
	for _, m := range list.Data {
		if m.ID == c.model {
			return nil
		}
		ids = append(ids, m.ID)
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return &ProviderError{Message: c.name() + " has no models yet", Field: "model"}
	}
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
	Role    string `json:"role"`
	Content string `json:"content"`
}

func (c *openAIClient) body(req Request) ([]byte, error) {
	messages := []openAIMessage{}
	if req.System != "" {
		messages = append(messages, openAIMessage{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		var text []string
		for _, b := range m.Blocks {
			if b.Type == BlockText {
				text = append(text, b.Text)
			}
		}
		if len(text) > 0 {
			messages = append(messages, openAIMessage{Role: m.Role, Content: strings.Join(text, "\n\n")})
		}
	}
	return json.Marshal(map[string]any{
		"model": c.model, "messages": messages, "stream": true,
		// The usage arrives in a last chunk of its own; a server that does not
		// know the option ignores it, and the turn is recorded without counts.
		"stream_options": map[string]bool{"include_usage": true},
	})
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
					Reasoning        string `json:"reasoning"`
					ReasoningContent string `json:"reasoning_content"`
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
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				finish = *choice.FinishReason
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, errStreamDone) {
		if ctx.Err() != nil {
			return Reply{}, ctx.Err()
		}
		return Reply{}, err
	}
	if !finished && finish == "" {
		if ctx.Err() != nil {
			return Reply{}, ctx.Err()
		}
		return Reply{}, &ProviderError{Message: c.name() + "'s answer was cut off before it finished", Retryable: true}
	}
	out := Reply{Usage: usage, Model: model, Stop: openAIStop(finish)}
	if model == "" {
		out.Model = c.model
	}
	if text.Len() > 0 {
		out.Blocks = []Block{{Type: BlockText, Text: text.String()}}
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
