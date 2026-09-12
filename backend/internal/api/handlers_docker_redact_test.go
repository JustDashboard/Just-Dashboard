package api

import (
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// The raw inspect route must not be the way around the redaction the typed
// route applies. Both answer the same container; one of them showing the
// master key to a readonly account would make the other pointless.
func TestRedactRawEnvMasksOnlyCredentials(t *testing.T) {
	doc := map[string]any{
		"Id": "abc",
		"Config": map[string]any{
			"Image": "app:1.2.3",
			"Env": []any{
				"PATH=/usr/local/bin",
				"JD_MASTER_KEY=real-key",
				"DATABASE_URL=postgres://user:pw@host/db",
				"LOG_LEVEL=debug",
				// Not a KEY=VALUE pair at all, and must survive untouched.
				"MALFORMED",
			},
			// A label mentioning "key" is not a credential, and a blanket
			// scrub over the document would have mangled it.
			"Labels": map[string]any{"description": "holds the key to the cache"},
		},
	}
	redactRawEnv(doc)

	env := doc["Config"].(map[string]any)["Env"].([]any)
	want := []string{
		"PATH=/usr/local/bin",
		"JD_MASTER_KEY=" + dockerx.RedactedEnvValue,
		"DATABASE_URL=" + dockerx.RedactedEnvValue,
		"LOG_LEVEL=debug",
		"MALFORMED",
	}
	for i, expected := range want {
		if got := env[i].(string); got != expected {
			t.Errorf("Env[%d] = %q, want %q", i, got, expected)
		}
	}

	labels := doc["Config"].(map[string]any)["Labels"].(map[string]any)
	if labels["description"] != "holds the key to the cache" {
		t.Errorf("a label mentioning a key was rewritten: %v", labels["description"])
	}
	// The placeholder is fixed-width so it cannot leak the original's length.
	if strings.Contains(dockerx.RedactedEnvValue, "real-key") {
		t.Error("the placeholder carries the value it replaced")
	}
}

// A document shaped differently from what the Engine sends must not panic the
// handler — the raw view exists precisely to show fields this code does not
// model.
func TestRedactRawEnvSurvivesUnexpectedShapes(t *testing.T) {
	for name, doc := range map[string]map[string]any{
		"no config":        {"Id": "abc"},
		"config not a map": {"Config": "surprise"},
		"env not a list":   {"Config": map[string]any{"Env": "PATH=/bin"}},
		"env of numbers":   {"Config": map[string]any{"Env": []any{1, 2, 3}}},
		"empty":            {},
	} {
		t.Run(name, func(t *testing.T) {
			redactRawEnv(doc) // must not panic
		})
	}
}
