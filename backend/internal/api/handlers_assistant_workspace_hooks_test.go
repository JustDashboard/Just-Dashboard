package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/gitx"
)

// A credential is in git's environment for one fetch or push, and no program the
// checkout names is run beside it: not a hook in .git/hooks, not one core.hooksPath
// points at. Whoever can write the workspace — its account, the Files page, the
// model — could otherwise read the deployment's saved token there.
func TestNoHookOfTheWorkspaceSeesThePushCredential(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_SSL_NO_VERIFY", "1")
	f := operatorRouterAs(t, auth.RoleAdmin)
	root := f.s.Cfg.FileRoots[0]
	base := filepath.Join(t.TempDir(), "served")
	bare := filepath.Join(base, "acme", "shop.git")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, bare, "init", "--bare", "-q")
	gitIn(t, bare, "config", "http.receivepack", "true")
	seed := filepath.Join(t.TempDir(), "seed")
	gitIn(t, filepath.Dir(seed), "clone", "-q", bare, "seed")
	if err := os.WriteFile(filepath.Join(seed, "app.js"), []byte("console.log('one')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, seed, "add", "-A")
	gitIn(t, seed, "commit", "-q", "-m", "Begin")
	gitIn(t, seed, "push", "-q", "origin", "HEAD:refs/heads/main")

	const token = "ghp_workspace_hook_token_0123456789"
	server := gitOverHTTPS(t, base, token)
	credential, err := f.s.modules.deployPlanning.CreateCredential(context.Background(), deploy.CredentialCreateRequest{
		Name: "shop-token", Kind: deploy.CredentialGitBearer, Target: "127.0.0.1", Secret: token,
	})
	if err != nil {
		t.Fatal(err)
	}
	remote := server.URL + "/acme/shop.git"
	projectID, _ := seedCommittedDeployment(t, f.s, "shop-hooks",
		deploy.DraftSourceConfig{Kind: deploy.SourceGit, Mode: deploy.SourceModeGitURL, URL: remote, Ref: "main", CredentialID: credential.ID},
		deploy.SourceIdentity{Kind: deploy.SourceGit, Revision: strings.Repeat("a", 40), Ref: "main"})
	f.s.modules.git = gitx.New([]string{root})
	f.s.workspaceHomeOverride = root
	folder := filepath.Join(root, workspaceFolder, "shop-hooks")

	f.provider.queue(claudeCalls("Opening it.", [2]any{"open_workspace", map[string]any{"reason": "fix it"}}), claudeAnswer("Open."))
	lines(t, f.do(http.MethodPost, fmt.Sprintf("/deploy/%d/assistant", projectID), `{"question":"Open it","mode":"full"}`))

	// A hook in .git/hooks, and one where core.hooksPath points, each writing out
	// the environment it was run with.
	leak := filepath.Join(t.TempDir(), "leak")
	hook := "#!/bin/sh\nenv >> " + leak + "\n"
	for _, dir := range []string{filepath.Join(folder, ".git", "hooks"), filepath.Join(folder, ".husky")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "pre-push"), []byte(hook), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.provider.queue(
		claudeCalls("Fixing.", [2]any{"edit_file", map[string]any{"path": filepath.Join(folder, "app.js"), "old_text": "'one'", "new_text": "'two'", "reason": "fix"}}),
		claudeCalls("Pushing.", [2]any{"push_changes", map[string]any{"folder": folder, "message": "Print two", "reason": "ship"}}),
		claudeAnswer("Done."),
	)
	for _, hooksPath := range []string{"", ".husky"} {
		if hooksPath != "" {
			gitIn(t, folder, "config", "core.hooksPath", hooksPath)
			if err := os.WriteFile(filepath.Join(folder, "app.js"), []byte("console.log('three')\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			f.provider.queue(
				claudeCalls("Pushing.", [2]any{"push_changes", map[string]any{"folder": folder, "message": "Print three", "reason": "ship"}}),
				claudeAnswer("Done."),
			)
		}
		lines(t, f.do(http.MethodPost, fmt.Sprintf("/deploy/%d/assistant", projectID), `{"question":"Ship it","mode":"full"}`))
	}
	if subject := gitIn(t, bare, "log", "-1", "--format=%s", "main"); subject != "Print three" {
		t.Fatalf("the remote's last commit is %q", subject)
	}
	authorization := deploy.GitBasicAuthorization("127.0.0.1", token)
	if raw, err := os.ReadFile(leak); err == nil && (strings.Contains(string(raw), authorization) || strings.Contains(string(raw), token)) {
		t.Fatal("a hook of the workspace was run with the push's credential in its environment")
	}
}
