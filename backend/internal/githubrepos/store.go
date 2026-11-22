package githubrepos

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

// Store keeps the repository App and the connections made through it. Every
// secret is sealed under the master key, as every other credential at rest
// is: the App's client secret and private key in one document, each
// connection's two tokens in another. The public facts stay readable.
//
// Every read of a connection takes the dashboard account it belongs to. There
// is no read by id alone, so no caller can reach another account's
// connection by forgetting a WHERE clause.
type Store struct {
	db     *sql.DB
	sealer *auth.Sealer
}

func NewStore(db *sql.DB, sealer *auth.Sealer) *Store {
	return &Store{db: db, sealer: sealer}
}

// App is the repository App as it is stored. The two secrets are unexported
// fields, so no encoding of an App can carry them.
type App struct {
	ID           int64
	Slug         string
	Name         string
	Owner        string
	OwnerType    string
	HTMLURL      string
	ClientID     string
	Public       bool
	CallbackURLs []string
	Permissions  map[string]string
	CreatedAt    time.Time

	clientSecret string
	privateKey   string
}

type appSecrets struct {
	ClientSecret string `json:"clientSecret"`
	PrivateKey   string `json:"privateKey"`
}

// SaveApp replaces whatever App was stored. The caller removes the old App's
// connections first (ForgetApp); a connection's tokens were issued to one
// client id and mean nothing to another.
func (s *Store) SaveApp(ctx context.Context, app App) error {
	secrets, err := json.Marshal(appSecrets{ClientSecret: app.clientSecret, PrivateKey: app.privateKey})
	if err != nil {
		return err
	}
	sealed, err := s.sealer.Seal(string(secrets))
	if err != nil {
		return err
	}
	callbacks, err := json.Marshal(nonNilStrings(app.CallbackURLs))
	if err != nil {
		return err
	}
	permissions, err := json.Marshal(nonNilMap(app.Permissions))
	if err != nil {
		return err
	}
	created := app.CreatedAt
	if created.IsZero() {
		created = time.Now()
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO github_repo_app(id, app_id, slug, name, owner, owner_type, html_url, client_id, public, callback_urls_json, permissions_json, secret_enc, created_at)
		VALUES(1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET app_id=excluded.app_id, slug=excluded.slug, name=excluded.name, owner=excluded.owner,
		  owner_type=excluded.owner_type, html_url=excluded.html_url, client_id=excluded.client_id, public=excluded.public,
		  callback_urls_json=excluded.callback_urls_json, permissions_json=excluded.permissions_json,
		  secret_enc=excluded.secret_enc, created_at=excluded.created_at`,
		app.ID, app.Slug, app.Name, app.Owner, app.OwnerType, app.HTMLURL, app.ClientID, boolInt(app.Public),
		string(callbacks), string(permissions), sealed, created.UTC().Unix())
	return err
}

// LoadApp returns the stored App with its secrets opened, or
// ErrNotConfigured.
func (s *Store) LoadApp(ctx context.Context) (App, error) {
	var app App
	var public int
	var callbacks, permissions, sealed string
	var created int64
	err := s.db.QueryRowContext(ctx, `
		SELECT app_id, slug, name, owner, owner_type, html_url, client_id, public, callback_urls_json, permissions_json, secret_enc, created_at
		FROM github_repo_app WHERE id = 1`).
		Scan(&app.ID, &app.Slug, &app.Name, &app.Owner, &app.OwnerType, &app.HTMLURL, &app.ClientID, &public, &callbacks, &permissions, &sealed, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return App{}, ErrNotConfigured
	}
	if err != nil {
		return App{}, err
	}
	app.Public = public != 0
	_ = json.Unmarshal([]byte(callbacks), &app.CallbackURLs)
	_ = json.Unmarshal([]byte(permissions), &app.Permissions)
	app.CallbackURLs = nonNilStrings(app.CallbackURLs)
	app.Permissions = nonNilMap(app.Permissions)
	app.CreatedAt = time.Unix(created, 0).UTC()
	opened, err := s.sealer.Open(sealed)
	if err != nil {
		return App{}, err
	}
	var secrets appSecrets
	if err := json.Unmarshal([]byte(opened), &secrets); err != nil {
		return App{}, err
	}
	app.clientSecret, app.privateKey = secrets.ClientSecret, secrets.PrivateKey
	return app, nil
}

// SetCallbackURLs replaces the sign-in addresses the App was told about.
func (s *Store) SetCallbackURLs(ctx context.Context, urls []string) error {
	encoded, err := json.Marshal(nonNilStrings(urls))
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE github_repo_app SET callback_urls_json = ? WHERE id = 1`, string(encoded))
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrNotConfigured
	}
	return nil
}

// DeleteApp forgets the App and every connection made through it, in one
// transaction: a connection without its App could neither refresh nor be
// revoked, and would only ever answer "connect again".
func (s *Store) DeleteApp(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM github_connections`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM github_repo_app WHERE id = 1`); err != nil {
		return err
	}
	return tx.Commit()
}

// Connection is one GitHub account a dashboard account connected, as the page
// shows it (GitHubConnection). The owner, the GitHub id and the tokens are
// kept out of its encoding.
type Connection struct {
	ID                  int64          `json:"id"`
	OwnerUserID         int64          `json:"-"`
	GitHubID            int64          `json:"-"`
	Login               string         `json:"login"`
	Name                string         `json:"name,omitempty"`
	HTMLURL             string         `json:"htmlUrl"`
	Status              string         `json:"status"`
	StatusMessage       string         `json:"statusMessage,omitempty"`
	Method              string         `json:"method"`
	CreatedAt           time.Time      `json:"createdAt,omitzero"`
	UpdatedAt           time.Time      `json:"updatedAt,omitzero"`
	Installations       []Installation `json:"installations"`
	InstallationsReadAt time.Time      `json:"installationsReadAt,omitzero"`

	tokens tokenPair
}

// Connection statuses.
const (
	StatusActive  = "active"
	StatusExpired = "expired"
	StatusRevoked = "revoked"
)

// tokenPair is a connection's credentials. The expiries are zero when GitHub
// gave none: an App with "expire user authorization tokens" turned off
// issues tokens that last until revoked, and no refresh token.
type tokenPair struct {
	Access         string    `json:"access"`
	Refresh        string    `json:"refresh,omitempty"`
	AccessExpires  time.Time `json:"-"`
	RefreshExpires time.Time `json:"-"`
}

// profile is who GitHub says a token belongs to.
type profile struct {
	ID      int64  `json:"id"`
	Login   string `json:"login"`
	Name    string `json:"name"`
	HTMLURL string `json:"html_url"`
}

const connectionColumns = `id, owner_user_id, github_id, login, name, html_url, method, status, status_message, tokens_enc,
	access_expires_at, refresh_expires_at, installations_json, installations_read_at, created_at, updated_at`

type rowScanner interface{ Scan(...any) error }

func (s *Store) scanConnection(row rowScanner) (Connection, error) {
	var c Connection
	var sealed, installations string
	var accessExpires, refreshExpires, installationsRead, created, updated int64
	if err := row.Scan(&c.ID, &c.OwnerUserID, &c.GitHubID, &c.Login, &c.Name, &c.HTMLURL, &c.Method, &c.Status, &c.StatusMessage, &sealed,
		&accessExpires, &refreshExpires, &installations, &installationsRead, &created, &updated); err != nil {
		return Connection{}, err
	}
	opened, err := s.sealer.Open(sealed)
	if err != nil {
		return Connection{}, err
	}
	if err := json.Unmarshal([]byte(opened), &c.tokens); err != nil {
		return Connection{}, err
	}
	c.tokens.AccessExpires, c.tokens.RefreshExpires = unixOrZero(accessExpires), unixOrZero(refreshExpires)
	_ = json.Unmarshal([]byte(installations), &c.Installations)
	if c.Installations == nil {
		c.Installations = []Installation{}
	}
	c.InstallationsReadAt = unixOrZero(installationsRead)
	c.CreatedAt, c.UpdatedAt = unixOrZero(created), unixOrZero(updated)
	return c, nil
}

// Connections lists one dashboard account's connections, oldest first.
func (s *Store) Connections(ctx context.Context, ownerUserID int64) ([]Connection, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+connectionColumns+` FROM github_connections WHERE owner_user_id = ? ORDER BY id`, ownerUserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Connection{}
	for rows.Next() {
		c, err := s.scanConnection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Connection is one of the account's own connections, or
// ErrConnectionNotFound — which is also the answer for another account's id.
func (s *Store) Connection(ctx context.Context, ownerUserID, id int64) (Connection, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+connectionColumns+` FROM github_connections WHERE id = ? AND owner_user_id = ?`, id, ownerUserID)
	c, err := s.scanConnection(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Connection{}, ErrConnectionNotFound
	}
	return c, err
}

// allConnections is every connection on the dashboard, for forgetting the
// App, which revokes each.
func (s *Store) allConnections(ctx context.Context) ([]Connection, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+connectionColumns+` FROM github_connections ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Connection{}
	for rows.Next() {
		c, err := s.scanConnection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// othersOfGitHubUser counts the connections on this dashboard, other than
// id, that are the same GitHub account. A grant on GitHub is per account and
// App, so revoking it would end theirs too.
func (s *Store) othersOfGitHubUser(ctx context.Context, githubID, id int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM github_connections WHERE github_id = ? AND id != ?`, githubID, id).Scan(&n)
	return n, err
}

// upsertConnection records a sign-in: a new row, or the existing one for the
// same dashboard account and GitHub account updated in place, which is what
// reconnecting an expired or revoked connection is.
func (s *Store) upsertConnection(ctx context.Context, ownerUserID int64, who profile, method string, tokens tokenPair, now time.Time) (int64, error) {
	sealed, err := s.sealTokens(tokens)
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO github_connections(owner_user_id, github_id, login, name, html_url, method, status, status_message, tokens_enc,
		  access_expires_at, refresh_expires_at, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, 'active', '', ?, ?, ?, ?, ?)
		ON CONFLICT(owner_user_id, github_id) DO UPDATE SET login=excluded.login, name=excluded.name, html_url=excluded.html_url,
		  method=excluded.method, status='active', status_message='', tokens_enc=excluded.tokens_enc,
		  access_expires_at=excluded.access_expires_at, refresh_expires_at=excluded.refresh_expires_at, updated_at=excluded.updated_at
		RETURNING id`,
		ownerUserID, who.ID, who.Login, who.Name, who.HTMLURL, method, sealed,
		unixOrNone(tokens.AccessExpires), unixOrNone(tokens.RefreshExpires), now.UTC().Unix(), now.UTC().Unix()).Scan(&id)
	return id, err
}

func (s *Store) sealTokens(tokens tokenPair) (string, error) {
	encoded, err := json.Marshal(tokens)
	if err != nil {
		return "", err
	}
	return s.sealer.Seal(string(encoded))
}

// saveTokens persists a refreshed pair. It is written before the new access
// token is used: GitHub's refresh tokens rotate, so a pair that was used and
// then lost to a crash would leave the connection with neither.
func (s *Store) saveTokens(ctx context.Context, id int64, tokens tokenPair, now time.Time) error {
	sealed, err := s.sealTokens(tokens)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE github_connections SET tokens_enc = ?, access_expires_at = ?, refresh_expires_at = ?,
		status = 'active', status_message = '', updated_at = ? WHERE id = ?`,
		sealed, unixOrNone(tokens.AccessExpires), unixOrNone(tokens.RefreshExpires), now.UTC().Unix(), id)
	return err
}

func (s *Store) setStatus(ctx context.Context, id int64, status, message string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE github_connections SET status = ?, status_message = ?, updated_at = ? WHERE id = ?`,
		status, message, now.UTC().Unix(), id)
	return err
}

func (s *Store) setProfile(ctx context.Context, id int64, who profile, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE github_connections SET login = ?, name = ?, html_url = ?, updated_at = ? WHERE id = ?`,
		who.Login, who.Name, who.HTMLURL, now.UTC().Unix(), id)
	return err
}

func (s *Store) setInstallations(ctx context.Context, id int64, installations []Installation, now time.Time) error {
	if installations == nil {
		installations = []Installation{}
	}
	encoded, err := json.Marshal(installations)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE github_connections SET installations_json = ?, installations_read_at = ? WHERE id = ?`,
		string(encoded), now.UTC().Unix(), id)
	return err
}

func (s *Store) deleteConnection(ctx context.Context, ownerUserID, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM github_connections WHERE id = ? AND owner_user_id = ?`, id, ownerUserID)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrConnectionNotFound
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func unixOrZero(seconds int64) time.Time {
	if seconds <= 0 {
		return time.Time{}
	}
	return time.Unix(seconds, 0).UTC()
}

func unixOrNone(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UTC().Unix()
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func nonNilMap(values map[string]string) map[string]string {
	if values == nil {
		return map[string]string{}
	}
	return values
}
