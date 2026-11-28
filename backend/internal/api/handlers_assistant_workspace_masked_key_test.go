package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

// A key block reaches the model as "[REDACTED PRIVATE KEY]", not "[REDACTED]":
// a whole file written back as it was read must not put that over the real key,
// and neither may an edit that adds a masked value the file did not hold. A file
// that holds the words "[REDACTED]" as they are is no exception for its secrets.
func TestAMaskedKeyIsNeverWrittenBackOverTheRealOne(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	f := operatorRouterAs(t, auth.RoleAdmin)
	projectID, repo, _ := workspaceDeployment(t, f)
	key := filepath.Join(repo, "deploy.pem")
	real := "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASC\n-----END PRIVATE KEY-----\nmode=one\n"
	if err := os.WriteFile(key, []byte(real), 0o600); err != nil {
		t.Fatal(err)
	}
	env := filepath.Join(repo, ".env")
	const jwt = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"
	envText := "NOTE=[REDACTED]\nTOKEN=" + jwt + "\n"
	if err := os.WriteFile(env, []byte(envText), 0o600); err != nil {
		t.Fatal(err)
	}
	f.provider.queue(
		claudeCalls("Rewriting.", [2]any{"write_file", map[string]any{"path": key, "content": "[REDACTED PRIVATE KEY]\nmode=two\n", "reason": "change the mode"}}),
		claudeCalls("Rewriting.", [2]any{"write_file", map[string]any{"path": env, "content": "NOTE=[REDACTED]\nTOKEN=[REDACTED]\nDEBUG=1\n", "reason": "turn on debug"}}),
		claudeCalls("Editing.", [2]any{"edit_file", map[string]any{"path": key, "old_text": "mode=one", "new_text": "[REDACTED PRIVATE KEY]\nmode=two", "reason": "change the mode"}}),
		claudeAnswer("I could not."),
	)
	events := lines(t, f.do(http.MethodPost, fmt.Sprintf("/deploy/%d/assistant", projectID), `{"question":"Change the mode","mode":"full"}`))
	for _, line := range calledLines(events) {
		if line["ok"] == true {
			t.Fatalf("a masked key was written: %v", line)
		}
	}
	if raw, _ := os.ReadFile(key); string(raw) != real {
		t.Fatalf("deploy.pem = %q", raw)
	}
	if raw, _ := os.ReadFile(env); string(raw) != envText {
		t.Fatalf(".env = %q", raw)
	}
	if told := f.provider.lastToolResults(t); !strings.Contains(told, "masked for you") {
		t.Fatalf("the model was told: %s", told)
	}
}
