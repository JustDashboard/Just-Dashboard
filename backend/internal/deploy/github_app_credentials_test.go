package deploy

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type minterFake struct {
	calls int
	err   error
}

func (m *minterFake) InstallationToken(_ context.Context, installationID int64) (string, error) {
	m.calls++
	if m.err != nil {
		return "", m.err
	}
	return "ghs_" + itoa(installationID), nil
}

// A GitHub App credential is a pointer to an installation: opening it mints
// a bearer token, nothing can paste or edit one, and disconnecting the App
// removes the ones no project uses.
func TestGitHubAppCredentialsAreMintedNotStored(t *testing.T) {
	ctx := context.Background()
	fixture := newReleaseStoreFixture(t)
	minter := &minterFake{}
	store := fixture.variables.WithInstallationTokens(minter)
	id, err := store.EnsureGitHubAppCredential(ctx, 501, "acme")
	if err != nil || id == 0 {
		t.Fatalf("ensure: %d, %v", id, err)
	}
	if again, err := store.EnsureGitHubAppCredential(ctx, 501, "acme"); err != nil || again != id {
		t.Fatalf("second ensure = %d, %v (first %d)", again, err, id)
	}
	other, err := store.EnsureGitHubAppCredential(ctx, 502, "zed")
	if err != nil || other == id {
		t.Fatalf("second installation = %d, %v", other, err)
	}
	summary, err := store.GetCredential(ctx, id)
	if err != nil || summary.Kind != CredentialGitHubApp || summary.Name != "GitHub-App-acme" || summary.Target != "github.com" || summary.Username != "acme" {
		t.Fatalf("summary = %+v, %v", summary, err)
	}
	material, err := store.OpenCredential(ctx, id)
	if err != nil || material.Kind != CredentialGitBearer || material.Secret != "ghs_501" || minter.calls != 1 {
		t.Fatalf("material = %+v, %v (calls %d)", material, err, minter.calls)
	}
	if !strings.Contains(string(material.Config), `"installationId":501`) {
		t.Fatalf("config = %s", material.Config)
	}
	var sealed string
	if err := fixture.base.DB.QueryRow(`SELECT secret_enc FROM deploy_credentials WHERE id = ?`, id).Scan(&sealed); err != nil || sealed != "" {
		t.Fatalf("stored secret = %q, %v", sealed, err)
	}
	minter.err = errors.New("installation was removed")
	if _, err := store.OpenCredential(ctx, id); !errors.Is(err, ErrSourceUnavailable) || !strings.Contains(err.Error(), "installation was removed") {
		t.Fatalf("minting failure: %v", err)
	}
	if _, err := store.CreateCredential(ctx, CredentialCreateRequest{Name: "pasted", Kind: CredentialGitHubApp, Secret: "x"}); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("pasted app credential: %v", err)
	}
	name := "renamed"
	if _, err := store.UpdateCredential(ctx, id, CredentialUpdateRequest{Name: &name}); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("edit: %v", err)
	}
	// One in use by a project's current source survives the disconnect.
	if _, err := fixture.base.DB.Exec(`INSERT INTO deploy_sources(environment_id, revision, kind, config_json, credential_id, identity_json, digest, created_at) VALUES(?, 1, 'git', '{}', ?, '{}', 'source-1', 1)`, fixture.envID, id); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveGitHubAppCredentials(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetCredential(ctx, id); err != nil {
		t.Fatalf("in-use credential removed: %v", err)
	}
	if _, err := store.GetCredential(ctx, other); !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("unused credential kept: %v", err)
	}
	unconnected := fixture.variables.WithInstallationTokens(nil)
	if _, err := unconnected.OpenCredential(ctx, id); !errors.Is(err, ErrSourceUnavailable) || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("open without the app: %v", err)
	}
}
