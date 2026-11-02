package assistant

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// A setup saved before there were several reads as a keyring of that one, active,
// under the preset at its address; one that was not offered tools still is not.
func TestAnEarlierSetupReadsAsAKeyringOfOne(t *testing.T) {
	ring := RingOf(&Settings{Provider: ProviderOpenAI, BaseURL: "http://127.0.0.1:11434/v1", Model: "qwen", Key: "k"})
	if ring.Active != PresetOllama || ring.Find(PresetOllama) == nil || ring.Current().Model != "qwen" {
		t.Fatalf("ring = %+v", ring)
	}
	if ring.Current().ToolCalls {
		t.Fatal("an earlier setup gained tools")
	}
	if got := RingOf(&Settings{Provider: ProviderOpenAI, BaseURL: "http://10.0.0.5:8000/v1", Model: "m"}); got.Active != PresetCustom {
		t.Fatalf("another server = %+v", got)
	}
	if got := RingOf(&Settings{Provider: ProviderAnthropic, Model: "claude-opus-5-5", Key: "k"}); got.Active != PresetAnthropic || !RingView(got).Tools {
		t.Fatalf("claude = %+v", got)
	}
	if off := RingOf(nil); off.Current() != nil || off.Mode() != ModeChanges {
		t.Fatalf("off = %+v", off)
	}
}

// A hosted provider's address is its own whatever a form sends, so a key typed for
// it cannot be pointed elsewhere, and it answers only with a key.
func TestAHostedPresetKeepsItsAddress(t *testing.T) {
	ring := RingOf(nil)
	profile, err := PrepareProfile(PresetOpenRouter, Setup{Model: "x/y", BaseURL: "https://evil.example/v1", Key: "sk-or-1"}, ring)
	if err != nil {
		t.Fatal(err)
	}
	if profile.BaseURL != "https://openrouter.ai/api/v1" || !profile.ToolCalls || profile.Provider != ProviderOpenAI {
		t.Fatalf("profile = %+v", profile)
	}
	var field *FieldError
	if _, err := PrepareProfile(PresetGroq, Setup{Model: "m"}, ring); !errors.As(err, &field) || field.Field != "key" {
		t.Fatalf("no key = %v", err)
	}
	if _, err := PrepareProfile("somebody-else", Setup{Model: "m", Key: "k"}, ring); !errors.As(err, &field) || field.Field != "preset" {
		t.Fatalf("unknown preset = %v", err)
	}
}

// A blank key keeps the saved one for the same provider at the same address, and
// a local server's address moving forgets it.
func TestASavedKeyStaysWithItsAddress(t *testing.T) {
	ring := RingOf(nil)
	ring.Put(Profile{Preset: PresetCustom, Settings: Settings{Provider: ProviderOpenAI, BaseURL: "http://10.0.0.5:8000/v1", Model: "a", Key: "sealed"}})
	same, err := PrepareProfile(PresetCustom, Setup{Model: "b", BaseURL: "http://10.0.0.5:8000/v1"}, ring)
	if err != nil || same.Key != "sealed" {
		t.Fatalf("same address = %+v %v", same, err)
	}
	moved, err := PrepareProfile(PresetCustom, Setup{Model: "b", BaseURL: "http://10.0.0.9:8000/v1"}, ring)
	if err != nil || moved.Key != "" {
		t.Fatalf("a moved address kept the key: %+v %v", moved, err)
	}
}

// Removing the active provider hands over to the next saved one, and the last going
// turns the assistant off.
func TestRemovingTheActiveProviderHandsOver(t *testing.T) {
	ring := RingOf(&Settings{Provider: ProviderAnthropic, Model: "claude-opus-5-5", Key: "k"})
	ring.Put(Profile{Preset: PresetOpenAI, Settings: Settings{Provider: ProviderOpenAI, BaseURL: "https://api.openai.com/v1", Model: "gpt", Key: "o"}})
	ring.Remove(PresetAnthropic)
	if ring.Active != PresetOpenAI || ring.Current().Key != "o" {
		t.Fatalf("after removal: %+v", ring)
	}
	ring.Remove(PresetOpenAI)
	if ring.Active != "" || ring.Current() != nil {
		t.Fatalf("after the last: %+v", ring)
	}
}

// No view of the keyring carries a key.
func TestTheKeyringViewCarriesNoKey(t *testing.T) {
	ring := RingOf(&Settings{Provider: ProviderAnthropic, Model: "claude-opus-5-5", Key: "sk-ant-secret"})
	ring.Put(Profile{Preset: PresetOpenAI, Settings: Settings{Provider: ProviderOpenAI, BaseURL: "https://api.openai.com/v1", Model: "gpt", Key: "sk-openai-secret", ToolCalls: true}})
	raw, _ := json.Marshal(RingView(ring))
	if strings.Contains(string(raw), "secret") {
		t.Fatalf("a key is in the view: %s", raw)
	}
	view := RingView(ring)
	if len(view.Profiles) != 2 || !view.Profiles[0].Active || view.Profiles[1].Active || !view.Profiles[1].KeySet || !view.Tools || len(view.Presets) != len(Presets) {
		t.Fatalf("view = %+v", view)
	}
}
