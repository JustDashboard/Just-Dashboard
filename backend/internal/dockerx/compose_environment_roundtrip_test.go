package dockerx

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestComposeEnvironmentValuesRoundTripThroughRealParser(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("Docker Compose CLI is unavailable")
	}
	values := map[string]string{"DOLLARS": "prefix$UNSET-${HOME}-$$-${UNSET}", "APOSTROPHE": "a'b", "BACKSLASH": `back\slash`, "SLASH_QUOTE": `back\'quote`, "TRAILING_SLASH": `end\`, "MULTILINE": "a\nb", "TAB": "a\tb", "CR": "a\rb", "CONTROL": "a\x01b", "UNICODE": "雪😀", "EMPTY": "", "REFERENCE": "${{credential.unrelated}}"}
	root := t.TempDir()
	env, err := writeComposeReleaseEnv(root, values)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(env)
	var source strings.Builder
	source.WriteString("services:\n  app:\n    image: unused-local-fixture\n    environment:\n")
	for name := range values {
		source.WriteString("      " + name + ": ${" + name + "}\n")
	}
	file := filepath.Join(root, "compose.yml")
	if err := os.WriteFile(file, []byte(source.String()), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("docker", "compose", "--project-name", "jd-parser-proof", "--env-file", env, "-f", file, "config", "--format", "json")
	cmd.Env = composeReleaseProcessEnvironment("", root)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Compose parser: %v %s", err, output)
	}
	var model struct {
		Services map[string]struct {
			Environment map[string]string `json:"environment"`
		} `json:"services"`
	}
	if err := json.Unmarshal(output, &model); err != nil {
		t.Fatal(err)
	}
	for name, want := range values {
		// config emits a reusable Compose document, so it re-escapes literal
		// dollars. Runtime lifecycle tests separately inspect actual Engine Env.
		got := strings.ReplaceAll(model.Services["app"].Environment[name], "$$", "$")
		if got != want {
			t.Errorf("%s changed through Compose parsing: %q != %q", name, got, want)
		}
	}
}
