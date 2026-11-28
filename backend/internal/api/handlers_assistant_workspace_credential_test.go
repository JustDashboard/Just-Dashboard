package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/term"
)

type workspaceMinter struct{ calls int }

func (m *workspaceMinter) InstallationToken(context.Context, int64) (string, error) {
	m.calls++
	return "ghs_workspace_read_token_0123456789", nil
}

// A deployment that fetches with the deploy GitHub App's credential clones and
// fetches its workspace with it, but never pushes with it: that token is
// granted Contents: read, and a push falls through to the next way.
func TestTheDeployAppsCredentialIsNeverUsedToPush(t *testing.T) {
	f := operatorRouterAs(t, auth.RoleAdmin)
	minter := &workspaceMinter{}
	f.s.modules.deployPlanning = f.s.modules.deployPlanning.WithInstallationTokens(minter)
	app, err := f.s.modules.deployPlanning.EnsureGitHubAppCredential(context.Background(), 501, "acme")
	if err != nil {
		t.Fatal(err)
	}
	source := &deploy.WorkspaceSource{Remote: "https://github.com/acme/shop.git", Host: "github.com", Repository: "acme/shop", CredentialID: app}
	p := &httpx.Principal{User: &auth.User{ID: 1, Username: "operator"}, Role: auth.RoleAdmin, Kind: "session"}
	ctx := context.Background()
	if env, said := f.s.fetchCredential(ctx, p, source); env == nil || !strings.Contains(said, "GitHub-App-acme") {
		t.Fatalf("the clone's credential = %v, %q", env, said)
	}
	push := f.s.pushAuthFor(ctx, p, source)
	if push.credentialID != 0 || push.access != nil || !strings.Contains(push.words, "server account") {
		t.Fatalf("a push would authenticate %+v", push)
	}
	env, revoke, err := f.s.pushEnvironment(ctx, push)
	revoke()
	if err != nil || env != nil {
		t.Fatalf("the push's environment = %v, %v", env, err)
	}
}

// A dashboard running as root never makes a workspace of root's: with no
// terminal account, or root as that account, open_workspace is refused with
// where to name one, and a workspaces folder that is not the account's is
// refused rather than used. A dashboard that is not root keeps its fallback.
func TestAWorkspaceIsNeverRoots(t *testing.T) {
	regular := term.Account{Name: "dev", UID: 1000, GID: 1000, Home: "/home/dev"}
	root := term.Account{Name: "root", UID: 0, GID: 0, Home: "/root"}
	if home, uid, _, err := workspaceAccount(0, regular, nil); err != nil || home != "/home/dev" || uid != 1000 {
		t.Fatalf("a regular account = %q %d %v", home, uid, err)
	}
	for name, c := range map[string]struct {
		account term.Account
		err     error
	}{
		"root": {root, nil}, "none": {term.Account{}, errors.New("no usable account")},
	} {
		if home, _, _, err := workspaceAccount(0, c.account, c.err); err == nil || home != "" || !strings.Contains(err.Error(), "JD_TERMINAL_USER") {
			t.Fatalf("%s as a root dashboard's account = %q, %v", name, home, err)
		}
	}
	if home, uid, _, err := workspaceAccount(1000, term.Account{}, errors.New("none")); err != nil || home != "" || uid != -1 {
		t.Fatalf("no account for a dashboard that is not root = %q %d %v", home, uid, err)
	}
	dir := t.TempDir()
	info, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := workspaceParentOwned(dir, info, os.Getuid()); err != nil {
		t.Fatalf("the account's own folder: %v", err)
	}
	if err := workspaceParentOwned(dir, info, os.Getuid()+1); err == nil || !strings.Contains(err.Error(), "belongs to uid") {
		t.Fatalf("another account's folder: %v", err)
	}
	link := filepath.Join(t.TempDir(), "jd-workspaces")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || workspaceParentOwned(link, info, os.Getuid()) == nil {
		t.Fatal("a link was taken for the workspaces folder")
	}
}
