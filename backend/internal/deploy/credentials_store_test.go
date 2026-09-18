package deploy

import (
	"context"
	"encoding/pem"
	"errors"
	"testing"
)

// testPrivateKeyPEM only needs the PEM envelope a git_ssh credential's shape
// check looks at (a "...PRIVATE KEY" block type over valid base64); nothing
// in this pass ever asks OpenSSH to parse the key material itself.
var testPrivateKeyPEM = string(pem.EncodeToMemory(&pem.Block{
	Type: "OPENSSH PRIVATE KEY", Bytes: []byte("fixture-key-material-not-a-real-key"),
}))

func newCredentialsFixture(t *testing.T) *planningStoreFixture {
	t.Helper()
	return newPlanningStoreFixture(t)
}

func TestCreateCredentialValidatesShapePerKindAndSealsTheSecret(t *testing.T) {
	t.Parallel()
	fixture := newCredentialsFixture(t)
	ctx := context.Background()

	tests := []struct {
		name    string
		request CredentialCreateRequest
		wantErr error
	}{
		{"bad name", CredentialCreateRequest{Name: "not a name!", Kind: CredentialGitBearer, Secret: "tok-en"}, ErrInvalidCredential},
		{"unsupported kind", CredentialCreateRequest{Name: "cred-a", Kind: "ftp", Secret: "x"}, ErrInvalidCredential},
		{"bearer secret has bad characters", CredentialCreateRequest{Name: "cred-b", Kind: CredentialGitBearer, Secret: "has a space"}, ErrInvalidCredential},
		{"registry missing username", CredentialCreateRequest{Name: "cred-c", Kind: CredentialRegistry, Target: "registry.example.test", Secret: "pw"}, ErrInvalidCredential},
		{"registry missing target", CredentialCreateRequest{Name: "cred-d", Kind: CredentialRegistry, Username: "u", Secret: "pw"}, ErrInvalidCredential},
		{"git_ssh non-PEM secret", CredentialCreateRequest{Name: "cred-e", Kind: CredentialGitSSH, Secret: "not-a-key"}, ErrInvalidCredential},
		{"target with a slash is not a bare host", CredentialCreateRequest{Name: "cred-f", Kind: CredentialGitBearer, Target: "example.test/x", Secret: "tok-en"}, ErrInvalidCredential},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := fixture.plans.CreateCredential(ctx, test.request); !errors.Is(err, test.wantErr) {
				t.Fatalf("CreateCredential(%q) error = %v, want %v", test.name, err, test.wantErr)
			}
		})
	}

	created, err := fixture.plans.CreateCredential(ctx, CredentialCreateRequest{
		Name: "github-bearer", Kind: CredentialGitBearer, Target: "github.com", Secret: "ghp_realtoken123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Target != "github.com" || created.Kind != CredentialGitBearer || created.UsedBy != 0 || created.LastUsedAt != nil {
		t.Fatalf("created credential = %#v", created)
	}
	var sealed string
	if err := fixture.store.DB.QueryRow(`SELECT secret_enc FROM deploy_credentials WHERE id = ?`, created.ID).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if sealed == "" || sealed == "ghp_realtoken123" {
		t.Fatalf("secret was not sealed: %q", sealed)
	}
	material, err := fixture.plans.OpenCredential(ctx, created.ID)
	if err != nil || material.Secret != "ghp_realtoken123" {
		t.Fatalf("OpenCredential after create = %+v, %v", material, err)
	}

	sshCredential, err := fixture.plans.CreateCredential(ctx, CredentialCreateRequest{
		Name: "deploy-key", Kind: CredentialGitSSH, Target: "gitlab.example.test", Secret: testPrivateKeyPEM,
	})
	if err != nil {
		t.Fatalf("git_ssh create with a PEM key: %v", err)
	}
	if sshCredential.Kind != CredentialGitSSH {
		t.Fatalf("ssh credential kind = %q", sshCredential.Kind)
	}
}

func TestUpdateCredentialKeepsTheStoredSecretWhenOmitted(t *testing.T) {
	t.Parallel()
	fixture := newCredentialsFixture(t)
	ctx := context.Background()
	created, err := fixture.plans.CreateCredential(ctx, CredentialCreateRequest{
		Name: "registry-a", Kind: CredentialRegistry, Target: "registry.example.test", Username: "svc", Secret: "s3cret",
	})
	if err != nil {
		t.Fatal(err)
	}
	renamed, err := fixture.plans.UpdateCredential(ctx, created.ID, CredentialUpdateRequest{
		Name: strPtr("registry-a-renamed"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Name != "registry-a-renamed" || renamed.Target != "registry.example.test" || renamed.Username != "svc" {
		t.Fatalf("renamed credential = %#v", renamed)
	}
	material, err := fixture.plans.OpenCredential(ctx, created.ID)
	if err != nil || material.Secret != "s3cret" {
		t.Fatalf("secret survived rename = %+v, %v", material, err)
	}

	updated, err := fixture.plans.UpdateCredential(ctx, created.ID, CredentialUpdateRequest{
		Secret: strPtr("new-secret"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Target != "registry.example.test" || updated.Username != "svc" {
		t.Fatalf("target/username lost on secret-only update: %#v", updated)
	}
	material, err = fixture.plans.OpenCredential(ctx, created.ID)
	if err != nil || material.Secret != "new-secret" {
		t.Fatalf("secret after update = %+v, %v", material, err)
	}

	other, err := fixture.plans.CreateCredential(ctx, CredentialCreateRequest{
		Name: "registry-b", Kind: CredentialRegistry, Target: "registry.example.test", Username: "svc", Secret: "s3cret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.plans.UpdateCredential(ctx, other.ID, CredentialUpdateRequest{Name: strPtr("registry-a-renamed")}); !errors.Is(err, ErrCredentialNameTaken) {
		t.Fatalf("renaming onto a taken name error = %v, want ErrCredentialNameTaken", err)
	}

	if _, err := fixture.plans.UpdateCredential(ctx, 999999, CredentialUpdateRequest{Name: strPtr("x")}); !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("update of an unknown id error = %v, want ErrCredentialNotFound", err)
	}
}

func TestCredentialUsageCountingAndDeleteRefusesWhileInUse(t *testing.T) {
	t.Parallel()
	fixture := newCredentialsFixture(t)
	ctx := context.Background()
	credential, err := fixture.plans.CreateCredential(ctx, CredentialCreateRequest{
		Name: "in-use-cred", Kind: CredentialGitBearer, Target: "github.com", Secret: "ghp_token",
	})
	if err != nil {
		t.Fatal(err)
	}
	projectID, environmentID := insertConfigurationFixture(t, fixture)
	// deploy_sources rows are append-only (deploy_source_revision_immutable);
	// replace revision 1 rather than updating it in place.
	if _, err := fixture.store.DB.Exec(`DELETE FROM deploy_sources WHERE environment_id = ? AND revision = 1`, environmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DB.Exec(`
		INSERT INTO deploy_sources(environment_id, revision, kind, config_json, credential_id, identity_json, digest, created_at)
		VALUES(?, 1, 'git', '{}', ?, '{}', ?, ?)`,
		environmentID, credential.ID, fakeContentDigest("source-with-credential"), fixture.now.Unix()); err != nil {
		t.Fatal(err)
	}

	listed, err := fixture.plans.ListCredentials(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].UsedBy != 1 {
		t.Fatalf("listed credentials = %#v", listed)
	}
	fetched, err := fixture.plans.GetCredential(ctx, credential.ID)
	if err != nil || fetched.UsedBy != 1 {
		t.Fatalf("GetCredential usedBy = %#v, %v", fetched, err)
	}

	if err := fixture.plans.DeleteCredential(ctx, credential.ID); !errors.Is(err, ErrCredentialInUse) {
		t.Fatalf("delete while in use error = %v, want ErrCredentialInUse", err)
	}

	// Archiving the referencing project frees the credential, mirroring the
	// invariants' own "non-archived source" scoping for in-use checks.
	if _, err := fixture.store.DB.Exec(`UPDATE deploy_projects SET archived_at = ? WHERE id = ?`, fixture.now.Unix(), projectID); err != nil {
		t.Fatal(err)
	}
	if err := fixture.plans.DeleteCredential(ctx, credential.ID); err != nil {
		t.Fatalf("delete after the referencing project is archived: %v", err)
	}
	if _, err := fixture.plans.GetCredential(ctx, credential.ID); !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("get after delete error = %v, want ErrCredentialNotFound", err)
	}
	if err := fixture.plans.DeleteCredential(ctx, credential.ID); !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("delete twice error = %v, want ErrCredentialNotFound", err)
	}
}

func TestCredentialExistsFailsFastForADraftSourceStep(t *testing.T) {
	t.Parallel()
	fixture := newCredentialsFixture(t)
	ctx := context.Background()
	if exists, err := fixture.plans.credentialExists(ctx, 0); err != nil || !exists {
		t.Fatalf("credentialExists(0) = %v, %v, want true, nil", exists, err)
	}
	if exists, err := fixture.plans.credentialExists(ctx, 999999); err != nil || exists {
		t.Fatalf("credentialExists(unknown) = %v, %v, want false, nil", exists, err)
	}
	credential, err := fixture.plans.CreateCredential(ctx, CredentialCreateRequest{
		Name: "exists-cred", Kind: CredentialGitBearer, Secret: "tok-en",
	})
	if err != nil {
		t.Fatal(err)
	}
	if exists, err := fixture.plans.credentialExists(ctx, credential.ID); err != nil || !exists {
		t.Fatalf("credentialExists(created) = %v, %v, want true, nil", exists, err)
	}

	draft, err := fixture.plans.Create(ctx, 1, "tester")
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.plans.Save(ctx, draft.ID, 1, true, DraftSaveRequest{
		Revision: draft.Revision, Step: DraftSource,
		Source: &DraftSourceConfig{
			Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://example.test/owner/repo.git",
			CredentialID: 424242,
		},
	})
	if !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("saving a source with an unknown credential id error = %v, want ErrInvalidSource", err)
	}
	saved, err := fixture.plans.Save(ctx, draft.ID, 1, true, DraftSaveRequest{
		Revision: draft.Revision, Step: DraftSource,
		Source: &DraftSourceConfig{
			Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://example.test/owner/repo.git",
			CredentialID: credential.ID,
		},
	})
	if err != nil {
		t.Fatalf("saving a source with a real credential id: %v", err)
	}
	if saved.Data.Source.CredentialID != credential.ID {
		t.Fatalf("saved source credential id = %d", saved.Data.Source.CredentialID)
	}
}

func strPtr(value string) *string { return &value }
