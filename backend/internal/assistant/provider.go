package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// A conversation is kept in one shape whoever answers it; each provider
// writes it out in its own wire format.

// Block is one part of a message.
type Block struct {
	Type BlockType
	Text string
	// ToolID, ToolName and Input are a tool call; ToolID and IsError, its result.
	ToolID   string
	ToolName string
	Input    json.RawMessage
	IsError  bool
	// Raw is a block the provider gave back that is sent back to it exactly
	// as it came: a thinking block, whose signature binds it to the
	// conversation it was written in.
	Raw json.RawMessage
}

type BlockType string

const (
	BlockText       BlockType = "text"
	BlockToolUse    BlockType = "tool_use"
	BlockToolResult BlockType = "tool_result"
	BlockRaw        BlockType = "raw"
)

type Message struct {
	Role   string // "user" or "assistant"
	Blocks []Block
}

// Tool is a tool the model may ask for. Asking is all it can do.
type Tool struct {
	Name        string
	Description string
	Schema      json.RawMessage
}

type Request struct {
	System   string
	Messages []Message
	Tools    []Tool
}

// Event is a piece of the answer as it streams: a run of text, or the model
// starting to think, which is a pause the page names rather than leaves blank.
type Event struct {
	Text     string
	Thinking bool
}

type Usage struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	CacheRead  int `json:"cacheRead,omitempty"`
	CacheWrite int `json:"cacheWrite,omitempty"`
}

// Stop is why an answer ended.
type Stop string

const (
	StopEnd     Stop = "end"
	StopTool    Stop = "tool"
	StopLength  Stop = "length"
	StopRefusal Stop = "refusal"
)

// Reply is a whole answer: what to append to the conversation, why it ended,
// what it cost and which model gave it.
type Reply struct {
	Blocks []Block
	Stop   Stop
	Usage  Usage
	Model  string
}

// Client is one configured provider.
type Client interface {
	// Stream sends the conversation and calls emit as the answer arrives.
	// Nothing is emitted before the provider has accepted the request, so an
	// error with no event before it is one the caller may still answer as a
	// plain refusal.
	Stream(ctx context.Context, req Request, emit func(Event)) (Reply, error)
	// Probe asks the provider whether the key, the address and the model
	// answer, without asking the model anything.
	Probe(ctx context.Context) error
	// Tools says whether the conversation may offer tools. An
	// OpenAI-compatible server is asked none: plenty of local models refuse
	// a request that carries them.
	Tools() bool
}

// Options are what a client is built with besides the settings.
type Options struct {
	HTTP *http.Client
	// AnthropicURL is Anthropic's API, and a test's server in a test.
	AnthropicURL string
}

// AnthropicAPI is where Anthropic's API is.
const AnthropicAPI = "https://api.anthropic.com"

// New builds the client for the stored settings.
func New(s Settings, opts Options) (Client, error) {
	if opts.HTTP == nil {
		opts.HTTP = NewHTTPClient()
	}
	switch s.Provider {
	case ProviderAnthropic:
		base := opts.AnthropicURL
		if base == "" {
			base = AnthropicAPI
		}
		return &anthropicClient{http: opts.HTTP, base: strings.TrimRight(base, "/"), key: s.Key, model: s.Model}, nil
	case ProviderOpenAI:
		return &openAIClient{http: opts.HTTP, base: strings.TrimRight(s.BaseURL, "/"), key: s.Key, model: s.Model}, nil
	}
	return nil, fmt.Errorf("unknown provider %q", s.Provider)
}

// NewHTTPClient is the client every provider request goes through. It has no
// overall timeout, because an answer streams for as long as it takes and the
// caller's context bounds it; it waits a bounded time for the provider to
// start answering. It follows no redirect: a key sent in a header would be
// sent on to wherever the redirect pointed.
func NewHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// A local model loads its weights before it answers its first request.
	transport.ResponseHeaderTimeout = 2 * time.Minute
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// ProviderError is the provider refusing, failing or not answering, in words
// for the person who asked.
type ProviderError struct {
	// Status is the provider's HTTP status, 0 when it never answered.
	Status    int
	Message   string
	Retryable bool
	// Field is the setting the refusal points at: a key the provider did not
	// accept, an address that did not answer, a model it does not have.
	Field string
	// Raw is what the provider said, for the details.
	Raw string
}

func (e *ProviderError) Error() string { return e.Message }

// retryable is an answer worth asking again for: the provider is busy or
// briefly down, not refusing.
func retryable(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusConflict ||
		status == http.StatusTooManyRequests || status >= 500
}

// send makes a request, asking again up to twice while the provider is busy
// or unreachable. Only the request is retried: once an answer has begun to
// arrive, nothing is sent twice.
func send(ctx context.Context, client *http.Client, build func() (*http.Request, error), name string) (*http.Response, error) {
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		req, err := build()
		if err != nil {
			return nil, err
		}
		res, err := client.Do(req.WithContext(ctx))
		var wait time.Duration
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			last = &ProviderError{Message: fmt.Sprintf("%s did not answer: %v", name, err), Retryable: true, Field: "baseUrl"}
		case retryable(res.StatusCode):
			last = readFailure(res, name)
			wait = retryAfter(res.Header.Get("Retry-After"))
		default:
			return res, nil
		}
		if attempt == 2 {
			break
		}
		if wait == 0 {
			wait = time.Duration(attempt+1) * time.Second
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
	return nil, last
}

// retryAfter reads the provider's own say on when to ask again, held to a
// wait the person asking will sit through.
func retryAfter(header string) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(header))
	if err != nil || seconds <= 0 {
		return 0
	}
	return min(time.Duration(seconds)*time.Second, 8*time.Second)
}

// readFailure turns a response that is not a success into an error, reading
// the provider's own message from either API's error shape.
func readFailure(res *http.Response, name string) *ProviderError {
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 16<<10))
	var shaped struct {
		Error json.RawMessage `json:"error"`
	}
	said := strings.TrimSpace(string(body))
	if json.Unmarshal(body, &shaped) == nil && len(shaped.Error) > 0 {
		var detail struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(shaped.Error, &detail) == nil && detail.Message != "" {
			said = detail.Message
		} else if text := ""; json.Unmarshal(shaped.Error, &text) == nil && text != "" {
			said = text
		}
	}
	if len(said) > 300 {
		said = said[:300] + "…"
	}
	e := &ProviderError{Status: res.StatusCode, Retryable: retryable(res.StatusCode), Raw: said}
	switch res.StatusCode {
	case http.StatusUnauthorized:
		e.Message, e.Field = fmt.Sprintf("%s did not accept the key", name), "key"
	case http.StatusForbidden:
		e.Message, e.Field = fmt.Sprintf("%s refused the key for this request: %s", name, said), "key"
	case http.StatusNotFound:
		e.Message, e.Field = fmt.Sprintf("%s has no such model or address: %s", name, said), "model"
	case http.StatusTooManyRequests:
		e.Message = fmt.Sprintf("%s is limiting this key's requests; try again in a minute", name)
	default:
		if res.StatusCode >= 500 {
			e.Message = fmt.Sprintf("%s is not answering right now (%d)", name, res.StatusCode)
		} else {
			e.Message = fmt.Sprintf("%s turned the request down: %s", name, said)
		}
	}
	return e
}

// IsProviderError reports whether err is the provider's and not the caller's.
func IsProviderError(err error) (*ProviderError, bool) {
	var pe *ProviderError
	ok := errors.As(err, &pe)
	return pe, ok
}
