package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// InstallationTokenMinter is the GitHub App service as the credential store
// needs it: a token for one installation, minted or reused.
type InstallationTokenMinter interface {
	InstallationToken(ctx context.Context, installationID int64) (string, error)
}

// WithInstallationTokens connects the App to the credential store, so a
// github_app credential opens as a bearer token the same Git path the other
// HTTPS credentials take.
func (s *PlanningStore) WithInstallationTokens(minter InstallationTokenMinter) *PlanningStore {
	s.installationTokens = minter
	return s
}

// openGitHubAppCredential turns an installation into the material a clone
// needs. GitHub's git endpoints accept an installation token as a bearer
// token, which is exactly what the bearer path already sends, so the adapter
// never learns the difference.
func (s *PlanningStore) openGitHubAppCredential(ctx context.Context, config json.RawMessage) (CredentialMaterial, error) {
	if s.installationTokens == nil {
		return CredentialMaterial{}, fmt.Errorf("%w: the GitHub App is not connected", ErrSourceUnavailable)
	}
	var parsed credentialConfig
	if json.Unmarshal(config, &parsed) != nil || parsed.InstallationID <= 0 {
		return CredentialMaterial{}, fmt.Errorf("%w: the GitHub App credential names no installation", ErrSourceUnavailable)
	}
	token, err := s.installationTokens.InstallationToken(ctx, parsed.InstallationID)
	if err != nil {
		return CredentialMaterial{}, fmt.Errorf("%w: GitHub did not issue an installation token: %v", ErrSourceUnavailable, err)
	}
	return CredentialMaterial{Kind: CredentialGitBearer, Config: config, Secret: token}, nil
}

// EnsureGitHubAppCredential keeps one credential per installation, named for
// the account so the import page and the credential list both read
// "GitHub App - acme". Nothing secret is stored: the row is a pointer to an
// installation.
func (s *PlanningStore) EnsureGitHubAppCredential(ctx context.Context, installationID int64, account string) (int64, error) {
	account = strings.TrimSpace(account)
	if installationID <= 0 || account == "" {
		return 0, fmt.Errorf("%w: an installation needs an id and an account", ErrInvalidCredential)
	}
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM deploy_credentials WHERE kind = ? AND json_extract(config_json, '$.installationId') = ?`, CredentialGitHubApp, installationID).Scan(&id)
	if err == nil {
		return id, nil
	}
	config, _ := json.Marshal(credentialConfig{Target: "github.com", Username: account, InstallationID: installationID, Account: account})
	name := "GitHub-App-" + strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		}
		return '-'
	}, account)
	now := s.now().UTC().Unix()
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO deploy_credentials(name, kind, config_json, secret_enc, created_at, updated_at)
		VALUES(?, ?, ?, '', ?, ?)
		ON CONFLICT(name) DO UPDATE SET kind = excluded.kind, config_json = excluded.config_json, updated_at = excluded.updated_at`,
		name, CredentialGitHubApp, string(config), now, now)
	if err != nil {
		return 0, err
	}
	if id, err = result.LastInsertId(); err == nil && id > 0 {
		return id, nil
	}
	err = s.db.QueryRowContext(ctx, `SELECT id FROM deploy_credentials WHERE name = ?`, name).Scan(&id)
	return id, err
}

// RemoveGitHubAppCredentials deletes the App's credentials that no current
// source uses. One a project still points at stays, and opens with a clear
// "not connected" refusal until the App is connected again or the source is
// changed; silently deleting it would turn a clone failure into a mystery.
func (s *PlanningStore) RemoveGitHubAppCredentials(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM deploy_credentials WHERE kind = ? AND id NOT IN (
			SELECT c.id`+credentialUsageJoin+` WHERE c.kind = ? AND p.id IS NOT NULL)`, CredentialGitHubApp, CredentialGitHubApp)
	return err
}
