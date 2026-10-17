package assistant

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func fieldOf(t *testing.T, err error) string {
	t.Helper()
	var field *FieldError
	if !errors.As(err, &field) {
		t.Fatalf("want a field error, got %v", err)
	}
	return field.Field
}

func TestPrepareChecksEachProvidersFields(t *testing.T) {
	for _, c := range []struct {
		name  string
		in    Setup
		field string
	}{
		{"no provider", Setup{Model: "x"}, "provider"},
		{"anthropic without a key", Setup{Provider: ProviderAnthropic}, "key"},
		{"a model not on the list", Setup{Provider: ProviderAnthropic, Model: "claude-3-opus", Key: "sk-ant-1"}, "model"},
		{"a key over two lines", Setup{Provider: ProviderAnthropic, Key: "sk-ant\nmore"}, "key"},
		{"a key with a space", Setup{Provider: ProviderAnthropic, Key: "sk ant"}, "key"},
		{"openai without an address", Setup{Provider: ProviderOpenAI, Model: "m"}, "baseUrl"},
		{"openai without a model", Setup{Provider: ProviderOpenAI, BaseURL: "http://127.0.0.1:11434/v1"}, "model"},
		{"a credential in the address", Setup{Provider: ProviderOpenAI, Model: "m", BaseURL: "https://user:pw@api.example.com/v1"}, "baseUrl"},
		{"a query in the address", Setup{Provider: ProviderOpenAI, Model: "m", BaseURL: "https://api.example.com/v1?key=1"}, "baseUrl"},
		{"plain HTTP across the internet", Setup{Provider: ProviderOpenAI, Model: "m", BaseURL: "http://api.example.com/v1"}, "baseUrl"},
		{"plain HTTP to a public address", Setup{Provider: ProviderOpenAI, Model: "m", BaseURL: "http://203.0.113.9:8000/v1"}, "baseUrl"},
		{"not an address", Setup{Provider: ProviderOpenAI, Model: "m", BaseURL: "ollama"}, "baseUrl"},
	} {
		_, err := Prepare(c.in, nil)
		if got := fieldOf(t, err); got != c.field {
			t.Errorf("%s: refused on %q, want %q", c.name, got, c.field)
		}
	}
}

func TestPrepareAcceptsNearbyHTTPAndDefaultsThePath(t *testing.T) {
	for raw, want := range map[string]string{
		"http://127.0.0.1:11434":       "http://127.0.0.1:11434/v1",
		"http://localhost:1234/v1/":    "http://localhost:1234/v1",
		"http://192.168.1.20:8000/v1":  "http://192.168.1.20:8000/v1",
		"http://100.101.102.103:11434": "http://100.101.102.103:11434/v1",
		"http://[::1]:8080/openai/v1":  "http://[::1]:8080/openai/v1",
		"https://api.openai.com/v1":    "https://api.openai.com/v1",
		" https://gateway.example/ ":   "https://gateway.example/v1",
	} {
		got, err := Prepare(Setup{Provider: ProviderOpenAI, Model: "llama3.1:8b", BaseURL: raw}, nil)
		if err != nil {
			t.Errorf("%s: %v", raw, err)
			continue
		}
		if got.BaseURL != want {
			t.Errorf("%s became %s, want %s", raw, got.BaseURL, want)
		}
		if got.Key != "" {
			t.Errorf("%s: a key appeared from nowhere", raw)
		}
	}
}

func TestPrepareDefaultsTheModelToTheNewestOpus(t *testing.T) {
	got, err := Prepare(Setup{Provider: ProviderAnthropic, Key: "sk-ant-api03-x"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "claude-opus-5-5" || DefaultAnthropicModel != "claude-opus-5-5" {
		t.Errorf("model %q", got.Model)
	}
}

// A blank key keeps the stored one only where it was typed for: the same
// provider at the same address. A key is never carried to a server typed in
// after it.
func TestABlankKeyIsKeptOnlyForTheSameServer(t *testing.T) {
	stored := &Settings{Provider: ProviderOpenAI, Model: "m", BaseURL: "https://gateway.example/v1", Key: "gw-secret"}
	same, err := Prepare(Setup{Provider: ProviderOpenAI, Model: "other", BaseURL: "https://gateway.example/v1"}, stored)
	if err != nil || same.Key != "gw-secret" {
		t.Errorf("same server: key %q, %v", same.Key, err)
	}
	moved, err := Prepare(Setup{Provider: ProviderOpenAI, Model: "m", BaseURL: "https://elsewhere.example/v1"}, stored)
	if err != nil || moved.Key != "" {
		t.Errorf("another server was handed the key: %q, %v", moved.Key, err)
	}
	_, err = Prepare(Setup{Provider: ProviderAnthropic}, stored)
	if fieldOf(t, err) != "key" {
		t.Error("switching to Anthropic kept the gateway's key")
	}
	replaced, err := Prepare(Setup{Provider: ProviderOpenAI, Model: "m", BaseURL: "https://gateway.example/v1", Key: " new-key "}, stored)
	if err != nil || replaced.Key != "new-key" {
		t.Errorf("a typed key did not replace the stored one: %q, %v", replaced.Key, err)
	}
}

func TestTheViewNeverCarriesTheKey(t *testing.T) {
	s := &Settings{Provider: ProviderAnthropic, Model: "claude-sonnet-5-5", Key: "sk-ant-very-secret", UpdatedBy: "ann", UpdatedAt: time.Unix(1, 0)}
	encoded, err := json.Marshal(ViewOf(s))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "very-secret") {
		t.Fatalf("the view carries the key: %s", encoded)
	}
	var view View
	_ = json.Unmarshal(encoded, &view)
	if !view.Configured || !view.KeySet || view.ModelLabel != "Claude Sonnet 5.5" || len(view.Models) != 3 {
		t.Errorf("view %+v", view)
	}
	off := ViewOf(nil)
	if off.Configured || off.KeySet || len(off.Models) == 0 {
		t.Errorf("an assistant that is off reads %+v", off)
	}
}

func TestARevisionChangesWithEverySave(t *testing.T) {
	a := Settings{Provider: ProviderAnthropic, Model: "claude-opus-5-5", Key: "k", UpdatedAt: time.Unix(10, 0)}
	b := a
	b.UpdatedAt = time.Unix(11, 0)
	c := a
	c.Key = "k2"
	if a.Revision() == b.Revision() || a.Revision() == c.Revision() || a.Revision() != a.Revision() {
		t.Error("revisions do not tell saves apart")
	}
}
