package githubapp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

// Store keeps the one App this dashboard is. Its three secrets are sealed
// together under the master key, the same way every other credential at rest
// is; the public facts (id, slug, owner) stay readable for the settings page.
type Store struct {
	db     *sql.DB
	sealer *auth.Sealer
}

func NewStore(db *sql.DB, sealer *auth.Sealer) *Store {
	return &Store{db: db, sealer: sealer}
}

type sealedSecrets struct {
	ClientSecret  string `json:"clientSecret"`
	WebhookSecret string `json:"webhookSecret"`
	PrivateKey    string `json:"privateKey"`
}

// Save replaces whatever App was connected before. Connecting a second App
// is how an operator recovers from a deleted one; two at once is never
// meaningful because one webhook address is one App.
func (s *Store) Save(ctx context.Context, credentials Credentials) error {
	secrets, err := json.Marshal(sealedSecrets{ClientSecret: credentials.ClientSecret, WebhookSecret: credentials.WebhookSecret, PrivateKey: credentials.PrivateKey})
	if err != nil {
		return err
	}
	sealed, err := s.sealer.Seal(string(secrets))
	if err != nil {
		return err
	}
	created := credentials.CreatedAt
	if created.IsZero() {
		created = time.Now()
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO github_app(id, app_id, slug, name, owner, html_url, client_id, secret_enc, created_at)
		VALUES(1, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET app_id=excluded.app_id, slug=excluded.slug, name=excluded.name, owner=excluded.owner,
		  html_url=excluded.html_url, client_id=excluded.client_id, secret_enc=excluded.secret_enc, created_at=excluded.created_at`,
		credentials.ID, credentials.Slug, credentials.Name, credentials.Owner, credentials.HTMLURL, credentials.ClientID, sealed, created.UTC().Unix())
	return err
}

// Load returns the connected App with its secrets unsealed, or
// ErrNotConfigured.
func (s *Store) Load(ctx context.Context) (Credentials, error) {
	var credentials Credentials
	var sealed string
	var created int64
	err := s.db.QueryRowContext(ctx, `SELECT app_id, slug, name, owner, html_url, client_id, secret_enc, created_at FROM github_app WHERE id = 1`).
		Scan(&credentials.ID, &credentials.Slug, &credentials.Name, &credentials.Owner, &credentials.HTMLURL, &credentials.ClientID, &sealed, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return Credentials{}, ErrNotConfigured
	}
	if err != nil {
		return Credentials{}, err
	}
	opened, err := s.sealer.Open(sealed)
	if err != nil {
		return Credentials{}, err
	}
	var secrets sealedSecrets
	if err := json.Unmarshal([]byte(opened), &secrets); err != nil {
		return Credentials{}, err
	}
	credentials.ClientSecret, credentials.WebhookSecret, credentials.PrivateKey = secrets.ClientSecret, secrets.WebhookSecret, secrets.PrivateKey
	credentials.CreatedAt = time.Unix(created, 0).UTC()
	return credentials, nil
}

func (s *Store) Delete(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM github_app WHERE id = 1`)
	return err
}
