// Package assistant is the SQL editor's assistant: the model provider the
// operator chose, what the dashboard tells it about a database, and how its
// answer comes back to the page.
//
// It is off until an administrator sets a provider up, and what it sends is
// decided here rather than by the page: the structure of the database —
// names, types, keys and references — and nothing from inside a table unless
// the person asking attached a result to the question themselves. Nothing it
// answers is run. A statement it writes reaches the editor as text, and the
// one thing it may ask to do on its own, reading more of the structure, waits
// for that person to allow it.
package assistant

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

// Provider is who answers: Anthropic's API with the operator's key, or any
// server that speaks OpenAI's chat completions — OpenAI itself, a gateway,
// or a model running on this machine or this network.
type Provider string

const (
	ProviderAnthropic Provider = "anthropic"
	ProviderOpenAI    Provider = "openai"
)

// Model is one of the Claude models the settings offer.
type Model struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// AnthropicModels is the closed list the settings form picks from, newest
// tier first. It is closed because the request is shaped for these models —
// adaptive thinking, an effort level, the server-side refusal fallback — and
// an older model given the same body answers with a 400.
var AnthropicModels = []Model{
	{ID: "claude-opus-5-5", Label: "Claude Opus 5.5"},
	{ID: "claude-sonnet-5-5", Label: "Claude Sonnet 5.5"},
	{ID: "claude-haiku-5-5", Label: "Claude Haiku 5.5"},
}

// DefaultAnthropicModel is what the form starts on.
const DefaultAnthropicModel = "claude-opus-5-5"

// Settings is the provider as it is stored: sealed whole, key included.
type Settings struct {
	Provider  Provider  `json:"provider"`
	Model     string    `json:"model"`
	BaseURL   string    `json:"baseUrl,omitempty"`
	Key       string    `json:"key,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
	UpdatedBy string    `json:"updatedBy"`
}

// Setup is what the settings form sends. A blank key keeps the stored one,
// but only for the same provider at the same address: a key typed for one
// server is never sent to another that was typed in after it.
type Setup struct {
	Provider Provider `json:"provider"`
	Model    string   `json:"model"`
	BaseURL  string   `json:"baseUrl"`
	Key      string   `json:"key"`
}

// View is the provider as any page may read it. The key is never in it.
type View struct {
	Configured bool      `json:"configured"`
	Provider   Provider  `json:"provider,omitempty"`
	Model      string    `json:"model,omitempty"`
	ModelLabel string    `json:"modelLabel,omitempty"`
	BaseURL    string    `json:"baseUrl,omitempty"`
	KeySet     bool      `json:"keySet"`
	UpdatedAt  time.Time `json:"updatedAt,omitzero"`
	UpdatedBy  string    `json:"updatedBy,omitempty"`
	// Models is the list the form offers for Anthropic, stated here so the
	// page holds no second copy of it.
	Models []Model `json:"models"`
}

// ViewOf is what GET answers. A nil settings is an assistant that is off.
func ViewOf(s *Settings) View {
	v := View{Models: AnthropicModels}
	if s == nil {
		return v
	}
	v.Configured, v.Provider, v.Model, v.BaseURL = true, s.Provider, s.Model, s.BaseURL
	v.ModelLabel, v.KeySet = ModelLabel(s.Provider, s.Model), s.Key != ""
	v.UpdatedAt, v.UpdatedBy = s.UpdatedAt, s.UpdatedBy
	return v
}

// ModelLabel is a model as a person names it.
func ModelLabel(p Provider, id string) string {
	if p == ProviderAnthropic {
		for _, m := range AnthropicModels {
			if m.ID == id {
				return m.Label
			}
		}
	}
	return id
}

// Revision tells one saved provider from the next. A conversation begun
// under one is not carried on under another: its system prompt and its
// history were written for the model that was there.
func (s Settings) Revision() string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		string(s.Provider), s.Model, s.BaseURL, s.Key, s.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}, "\x00")))
	return hex.EncodeToString(sum[:8])
}

// FieldError is a setup refused because of one of its fields.
type FieldError struct {
	Field   string
	Message string
}

func (e *FieldError) Error() string { return e.Message }

func refuse(field, format string, args ...any) error {
	return &FieldError{Field: field, Message: fmt.Sprintf(format, args...)}
}

// Prepare checks a setup and turns it into the settings to store, against
// what is stored now (nil for none).
func Prepare(in Setup, stored *Settings) (Settings, error) {
	out := Settings{Provider: in.Provider, Model: strings.TrimSpace(in.Model)}
	key := strings.TrimSpace(in.Key)
	if len(key) > 1024 || strings.ContainsAny(key, " \t\r\n\x00") {
		return out, refuse("key", "the key is one line with no spaces, as the provider shows it")
	}
	switch in.Provider {
	case ProviderAnthropic:
		if out.Model == "" {
			out.Model = DefaultAnthropicModel
		}
		if ModelLabel(ProviderAnthropic, out.Model) == out.Model {
			return out, refuse("model", "choose one of the Claude models listed")
		}
	case ProviderOpenAI:
		base, err := normaliseBaseURL(in.BaseURL)
		if err != nil {
			return out, err
		}
		out.BaseURL = base
		if out.Model == "" {
			return out, refuse("model", "name the model the server should answer with, as its own list names it")
		}
		if len(out.Model) > 200 || strings.ContainsAny(out.Model, "\r\n\x00") {
			return out, refuse("model", "the model's name is one line of at most 200 characters")
		}
	default:
		return out, refuse("provider", "choose Anthropic or an OpenAI-compatible server")
	}
	switch {
	case key != "":
		out.Key = key
	case stored != nil && stored.Provider == out.Provider && stored.BaseURL == out.BaseURL:
		out.Key = stored.Key
	}
	if out.Provider == ProviderAnthropic && out.Key == "" {
		return out, refuse("key", "Anthropic answers only with an API key: paste one from the Claude Console")
	}
	return out, nil
}

// normaliseBaseURL reads the address of an OpenAI-compatible server. It has
// to be HTTPS, because the key and the database's structure travel in the
// request, unless the server is on this machine or a private network, which
// is where a local model is served from and where plain HTTP is the norm.
// An address with no path is given the /v1 every such server answers under.
func normaliseBaseURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", refuse("baseUrl", "enter the server's address, such as http://127.0.0.1:11434/v1")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", refuse("baseUrl", "the address takes no user name, query or fragment; the key goes in its own field")
	}
	if u.Scheme == "http" && !nearby(u.Hostname()) {
		return "", refuse("baseUrl", "a server outside this machine and its private networks is reached over HTTPS")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if u.Path == "" {
		u.Path = "/v1"
	}
	return u.String(), nil
}

// carrierGradeNAT is the range Tailscale hands out, a private network in all
// but name.
var carrierGradeNAT = &net.IPNet{IP: net.IP{100, 64, 0, 0}, Mask: net.CIDRMask(10, 32)}

func nearby(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || carrierGradeNAT.Contains(ip))
}
