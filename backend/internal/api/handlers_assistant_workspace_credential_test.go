package api

import (
	"context"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
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
