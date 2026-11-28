package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

// The workspace's marker is written by the dashboard, which runs as root, into a
// .git the checkout's account owns. A link that account left there is never
// followed: not as the marker, and not as .git itself.
func TestAWorkspaceMarkerNeverFollowsALink(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	f := operatorRouterAs(t, auth.RoleAdmin)
	projectID, repo, _ := workspaceDeployment(t, f)
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(repo, ".git", workspaceMarker)); err != nil {
		t.Fatal(err)
	}
	f.provider.queue(
		claudeCalls("Opening it.", [2]any{"open_workspace", map[string]any{"reason": "to change its code"}}),
		claudeAnswer("Open."),
	)
	lines(t, f.do(http.MethodPost, fmt.Sprintf("/deploy/%d/assistant", projectID), `{"question":"Open it","mode":"full"}`))
	if raw, _ := os.ReadFile(victim); string(raw) != "keep\n" {
		t.Fatalf("the marker was written through a link: the file outside reads %q", raw)
	}
}
