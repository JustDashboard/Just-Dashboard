package assistant

import (
	"slices"
	"strings"
	"time"
)

// The dashboard asks one model, set up once. Every page that uses the assistant
// — the SQL editor, a drawing's Describe, a failed run's Ask why, a deployment's
// assistant, a new deployment's setup — reads the provider that is active here,
// so a place added later has the provider too.
//
// More than one may be saved at a time, each with its own key, so switching is a
// press rather than typing a key again. A saved key stays with its own provider
// and address, and is never sent to another (Prepare).

// Preset is a provider the settings offer by name. All but Anthropic speak
// OpenAI's chat completions, so a preset is a logo, an address and whether it
// needs a key.
type Preset struct {
	ID string `json:"id"`
	// Label is its name and Detail one line of what it is.
	Label  string `json:"label"`
	Detail string `json:"detail"`
	// Product names its logo (components/product-logo.tsx).
	Product  string   `json:"product"`
	Provider Provider `json:"provider"`
	// BaseURL is where it answers. Empty is an address the person types; a
	// local server's is the address it listens on by default, which they may
	// change.
	BaseURL string `json:"baseUrl,omitempty"`
	// Local is a server on this machine or this network, whose address and
	// whether it takes tools are the person's to say.
	Local bool `json:"local,omitempty"`
	// KeyRequired is a provider that answers nothing without a key, and KeyURL
	// where a person gets one.
	KeyRequired bool   `json:"keyRequired"`
	KeyURL      string `json:"keyUrl,omitempty"`
	KeyHint     string `json:"keyHint,omitempty"`
}

// The presets the settings offer, in the order they are drawn.
const (
	PresetAnthropic  = "anthropic"
	PresetOpenAI     = "openai"
	PresetOpenRouter = "openrouter"
	PresetGemini     = "gemini"
	PresetGroq       = "groq"
	PresetMistral    = "mistral"
	PresetDeepSeek   = "deepseek"
	PresetXAI        = "xai"
	PresetOllama     = "ollama"
	PresetLMStudio   = "lmstudio"
	PresetCustom     = "custom"
)

// Presets is the closed list, which the page reads rather than keeps a copy of.
var Presets = []Preset{
	{ID: PresetAnthropic, Label: "Anthropic", Detail: "Claude, with your API key", Product: "claude", Provider: ProviderAnthropic,
		KeyRequired: true, KeyURL: "https://console.anthropic.com/settings/keys", KeyHint: "sk-ant-…"},
	{ID: PresetOpenAI, Label: "OpenAI", Detail: "GPT models, with your API key", Product: "openai", Provider: ProviderOpenAI,
		BaseURL: "https://api.openai.com/v1", KeyRequired: true, KeyURL: "https://platform.openai.com/api-keys", KeyHint: "sk-…"},
	{ID: PresetOpenRouter, Label: "OpenRouter", Detail: "Hundreds of models behind one key", Product: "openrouter", Provider: ProviderOpenAI,
		BaseURL: "https://openrouter.ai/api/v1", KeyRequired: true, KeyURL: "https://openrouter.ai/keys", KeyHint: "sk-or-…"},
	{ID: PresetGemini, Label: "Google Gemini", Detail: "Gemini, with an AI Studio key", Product: "gemini", Provider: ProviderOpenAI,
		BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", KeyRequired: true, KeyURL: "https://aistudio.google.com/apikey", KeyHint: "AIza…"},
	{ID: PresetGroq, Label: "Groq", Detail: "Open models, answered quickly", Product: "groq", Provider: ProviderOpenAI,
		BaseURL: "https://api.groq.com/openai/v1", KeyRequired: true, KeyURL: "https://console.groq.com/keys", KeyHint: "gsk_…"},
	{ID: PresetMistral, Label: "Mistral", Detail: "Mistral's own models", Product: "mistral", Provider: ProviderOpenAI,
		BaseURL: "https://api.mistral.ai/v1", KeyRequired: true, KeyURL: "https://console.mistral.ai/api-keys", KeyHint: ""},
	{ID: PresetDeepSeek, Label: "DeepSeek", Detail: "DeepSeek's own models", Product: "deepseek", Provider: ProviderOpenAI,
		BaseURL: "https://api.deepseek.com/v1", KeyRequired: true, KeyURL: "https://platform.deepseek.com/api_keys", KeyHint: "sk-…"},
	{ID: PresetXAI, Label: "xAI", Detail: "Grok, with your API key", Product: "xai", Provider: ProviderOpenAI,
		BaseURL: "https://api.x.ai/v1", KeyRequired: true, KeyURL: "https://console.x.ai", KeyHint: "xai-…"},
	{ID: PresetOllama, Label: "Ollama", Detail: "A model running on this machine", Product: "ollama", Provider: ProviderOpenAI,
		BaseURL: "http://127.0.0.1:11434/v1", Local: true},
	{ID: PresetLMStudio, Label: "LM Studio", Detail: "A model served by LM Studio", Product: "lmstudio", Provider: ProviderOpenAI,
		BaseURL: "http://127.0.0.1:1234/v1", Local: true},
	{ID: PresetCustom, Label: "Another server", Detail: "A gateway, or anything OpenAI-compatible", Product: "", Provider: ProviderOpenAI, Local: true},
}

// PresetOf finds a preset by its id.
func PresetOf(id string) (Preset, bool) {
	for _, p := range Presets {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}

// presetFor names the preset settings saved before there were presets belong
// to: the one at the same address, or another server. A setup saved that way
// keeps the tools it was answering without.
func presetFor(s Settings) string {
	if s.Provider == ProviderAnthropic {
		return PresetAnthropic
	}
	for _, p := range Presets {
		if p.BaseURL != "" && strings.EqualFold(p.BaseURL, s.BaseURL) {
			return p.ID
		}
	}
	return PresetCustom
}

// Mode is how much the assistant may do before it has to ask the person. It is
// decided by the server on every press: a call the mode holds runs only once
// the person answers it.
type Mode string

const (
	// ModeAsk holds every read and every change.
	ModeAsk Mode = "ask"
	// ModeChanges runs reads and holds what changes something. The default,
	// because what it reads can include text somebody else wrote: a request's
	// path, a log line.
	ModeChanges Mode = "changes"
	// ModeFull runs everything the assistant can reach without asking.
	ModeFull Mode = "full"
)

// Valid reports whether the mode is one of the three.
func (m Mode) Valid() bool { return m == ModeAsk || m == ModeChanges || m == ModeFull }

// Profile is one saved provider.
type Profile struct {
	Settings
	Preset string `json:"preset"`
}

// Keyring is what is stored, sealed whole, keys included.
type Keyring struct {
	// Active is the preset every page asks; empty is an assistant that is off.
	Active   string    `json:"active,omitempty"`
	Profiles []Profile `json:"profiles"`
	// DefaultMode is the mode a conversation starts in.
	DefaultMode Mode `json:"defaultMode,omitempty"`
}

// RingOf is the keyring settings saved before there were several: the one
// profile, active. Nil is an empty keyring.
func RingOf(s *Settings) *Keyring {
	ring := &Keyring{Profiles: []Profile{}}
	if s == nil {
		return ring
	}
	preset := presetFor(*s)
	ring.Active = preset
	ring.Profiles = append(ring.Profiles, Profile{Settings: *s, Preset: preset})
	return ring
}

// Find is a saved profile by its preset, nil when it has none.
func (r *Keyring) Find(preset string) *Profile {
	for i := range r.Profiles {
		if r.Profiles[i].Preset == preset {
			return &r.Profiles[i]
		}
	}
	return nil
}

// Current is what every page asks: the active profile's settings, nil when the
// assistant is off.
func (r *Keyring) Current() *Settings {
	if r == nil || r.Active == "" {
		return nil
	}
	if p := r.Find(r.Active); p != nil {
		settings := p.Settings
		return &settings
	}
	return nil
}

// Put saves a profile, in place of the one it replaces.
func (r *Keyring) Put(p Profile) {
	if existing := r.Find(p.Preset); existing != nil {
		*existing = p
		return
	}
	r.Profiles = append(r.Profiles, p)
}

// Remove forgets a profile and its key. The active one falling away hands over
// to the next saved one, so a page that worked a moment ago still does.
func (r *Keyring) Remove(preset string) {
	r.Profiles = slices.DeleteFunc(r.Profiles, func(p Profile) bool { return p.Preset == preset })
	if r.Active == preset {
		r.Active = ""
		if len(r.Profiles) > 0 {
			r.Active = r.Profiles[0].Preset
		}
	}
}

// Mode is the mode a conversation starts in.
func (r *Keyring) Mode() Mode {
	if r != nil && r.DefaultMode.Valid() {
		return r.DefaultMode
	}
	return ModeChanges
}

// ProfileView is a saved provider as any page may read it, without its key.
type ProfileView struct {
	Preset     string    `json:"preset"`
	Provider   Provider  `json:"provider"`
	Model      string    `json:"model"`
	ModelLabel string    `json:"modelLabel"`
	BaseURL    string    `json:"baseUrl,omitempty"`
	KeySet     bool      `json:"keySet"`
	ToolCalls  bool      `json:"toolCalls"`
	Active     bool      `json:"active"`
	UpdatedAt  time.Time `json:"updatedAt,omitzero"`
	UpdatedBy  string    `json:"updatedBy,omitempty"`
}

// RingView is what GET answers.
func RingView(r *Keyring) View {
	var current *Settings
	if r != nil {
		current = r.Current()
	}
	v := ViewOf(current)
	v.Presets = Presets
	v.Profiles = []ProfileView{}
	v.DefaultMode = r.Mode()
	if r == nil {
		return v
	}
	v.Preset = r.Active
	for _, p := range r.Profiles {
		v.Profiles = append(v.Profiles, ProfileView{
			Preset: p.Preset, Provider: p.Provider, Model: p.Model, ModelLabel: ModelLabel(p.Provider, p.Model),
			BaseURL: p.BaseURL, KeySet: p.Key != "", ToolCalls: p.answersTools(), Active: p.Preset == r.Active,
			UpdatedAt: p.UpdatedAt, UpdatedBy: p.UpdatedBy,
		})
	}
	return v
}

// PrepareProfile checks a setup for one preset against what the keyring holds
// for it. A hosted provider's address is the preset's, whatever was sent, so a
// form cannot point its key elsewhere; a local one's is the person's.
func PrepareProfile(preset string, in Setup, ring *Keyring) (Profile, error) {
	p, ok := PresetOf(preset)
	if !ok {
		return Profile{}, refuse("preset", "choose one of the providers listed")
	}
	in.Provider = p.Provider
	if p.Provider == ProviderOpenAI && !p.Local {
		in.BaseURL = p.BaseURL
	}
	if p.Provider == ProviderAnthropic {
		in.BaseURL = ""
	}
	var stored *Settings
	if existing := ring.Find(preset); existing != nil {
		settings := existing.Settings
		stored = &settings
	}
	settings, err := Prepare(in, stored)
	if err != nil {
		return Profile{}, err
	}
	if p.KeyRequired && settings.Key == "" {
		return Profile{}, refuse("key", "%s answers only with an API key", p.Label)
	}
	switch {
	case p.Provider == ProviderAnthropic, !p.Local:
		settings.ToolCalls = true
	case in.ToolCalls != nil:
		settings.ToolCalls = *in.ToolCalls
	case stored != nil:
		settings.ToolCalls = stored.ToolCalls
	}
	return Profile{Settings: settings, Preset: preset}, nil
}

// answersTools is whether this provider is offered tools.
func (s Settings) answersTools() bool { return s.Provider == ProviderAnthropic || s.ToolCalls }

// ModelToList is a model name to prepare a provider with only to list its models:
// Anthropic's closed list refuses any other, and the rest take any name.
func ModelToList(preset string) string {
	if preset == PresetAnthropic {
		return DefaultAnthropicModel
	}
	return "list"
}

// PresetForSetup names the preset a setup with no preset belongs to, for the
// route that predates them.
func PresetForSetup(in Setup) string {
	settings := Settings{Provider: in.Provider}
	if in.Provider != ProviderAnthropic {
		if base, err := normaliseBaseURL(in.BaseURL); err == nil {
			settings.BaseURL = base
		}
	}
	return presetFor(settings)
}
