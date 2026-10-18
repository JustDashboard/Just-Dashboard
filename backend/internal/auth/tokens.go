package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const tokenPrefix = "vpsd_"

// ScopeMCP is the scope of a key made for an AI client: it opens the MCP
// endpoint and nothing else, and reaches only the databases granted to it.
const ScopeMCP = "mcp"

var ErrTokenInvalid = errors.New("api token invalid, revoked or expired")

type APIToken struct {
	ID         int64      `json:"id"`
	UserID     int64      `json:"userId"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Role       Role       `json:"role"`
	CreatedAt  time.Time  `json:"createdAt"`
	ExpiresAt  *time.Time `json:"expiresAt"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
	Revoked    bool       `json:"revoked"`
	// Scope is "" for a key that opens every route its role allows, or
	// ScopeMCP for one that opens the MCP endpoint and nothing else.
	Scope string `json:"scope"`
	// Connections are the databases an MCP key reaches, as ListAPITokens
	// reads them. Every other key has none.
	Connections []TokenConnection `json:"connections,omitempty"`
}

// CreateAPIToken mints a scripting credential. The role may narrow the owner's
// role but never widen it, so a leaked token cannot exceed its creator.
func (s *Service) CreateAPIToken(ctx context.Context, owner *User, name string, role Role, ttl time.Duration) (*APIToken, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, "", errors.New("token name required")
	}
	if !role.Valid() {
		return nil, "", fmt.Errorf("unknown role %q", role)
	}
	if !roleAtMost(role, owner.Role) {
		return nil, "", fmt.Errorf("cannot mint a %s token from a %s account", role, owner.Role)
	}
	id, full, err := insertAPIToken(ctx, s.st.DB, owner, name, role, ttl, "")
	if err != nil {
		return nil, "", err
	}
	tok, err := s.apiTokenByID(ctx, id)
	if err != nil {
		return nil, "", err
	}
	return tok, full, nil
}

// insertAPIToken writes a new credential and returns its id and the secret,
// which exists nowhere else: only its hash is kept.
func insertAPIToken(ctx context.Context, db execer, owner *User, name string, role Role, ttl time.Duration, scope string) (int64, string, error) {
	full := tokenPrefix + RandomToken(32)
	var expires int64
	if ttl > 0 {
		expires = time.Now().Add(ttl).Unix()
	}
	prefix := full[:len(tokenPrefix)+8]
	res, err := db.ExecContext(ctx,
		`INSERT INTO api_tokens(user_id, name, prefix, token_hash, role, created_at, expires_at, scope)
		 VALUES(?,?,?,?,?,?,?,?)`,
		owner.ID, name, prefix, HashToken(full), string(role), time.Now().Unix(), expires, scope)
	if err != nil {
		return 0, "", err
	}
	id, err := res.LastInsertId()
	return id, full, err
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func roleAtMost(want, have Role) bool {
	rank := map[Role]int{RoleReadOnly: 0, RoleLimited: 1, RoleAdmin: 2}
	return rank[want] <= rank[have]
}

const apiTokenColumns = `id, user_id, name, prefix, role, created_at, expires_at, last_used_at, revoked, scope`

func (s *Service) apiTokenByID(ctx context.Context, id int64) (*APIToken, error) {
	row := s.st.DB.QueryRowContext(ctx, `SELECT `+apiTokenColumns+` FROM api_tokens WHERE id = ?`, id)
	return scanAPIToken(row)
}

func scanAPIToken(row interface{ Scan(...any) error }) (*APIToken, error) {
	var (
		t                      APIToken
		role                   string
		created, expires, used int64
		revoked                int
	)
	if err := row.Scan(&t.ID, &t.UserID, &t.Name, &t.Prefix, &role, &created, &expires, &used, &revoked, &t.Scope); err != nil {
		return nil, err
	}
	t.Role = Role(role)
	t.CreatedAt = time.Unix(created, 0).UTC()
	t.Revoked = revoked == 1
	if expires > 0 {
		e := time.Unix(expires, 0).UTC()
		t.ExpiresAt = &e
	}
	if used > 0 {
		u := time.Unix(used, 0).UTC()
		t.LastUsedAt = &u
	}
	return &t, nil
}

func (s *Service) ListAPITokens(ctx context.Context, userID int64, all bool) ([]*APIToken, error) {
	q := `SELECT ` + apiTokenColumns + ` FROM api_tokens`
	args := []any{}
	if !all {
		q += ` WHERE user_id = ?`
		args = append(args, userID)
	}
	q += ` ORDER BY created_at DESC`
	rows, err := s.st.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*APIToken{}
	for rows.Next() {
		t, err := scanAPIToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, s.fillTokenConnections(ctx, out)
}

func (s *Service) RevokeAPIToken(ctx context.Context, id int64) error {
	_, err := s.st.DB.ExecContext(ctx, `UPDATE api_tokens SET revoked = 1 WHERE id = ?`, id)
	return err
}

func (s *Service) DeleteAPIToken(ctx context.Context, id int64) error {
	_, err := s.st.DB.ExecContext(ctx, `DELETE FROM api_tokens WHERE id = ?`, id)
	return err
}

// ResolveAPIToken authenticates a Bearer credential. The effective role is the
// narrower of the token's scope and the owner's current role, so demoting an
// account immediately demotes every token it issued.
func (s *Service) ResolveAPIToken(ctx context.Context, presented string) (*APIToken, *User, Role, error) {
	if !strings.HasPrefix(presented, tokenPrefix) {
		return nil, nil, "", ErrTokenInvalid
	}
	row := s.st.DB.QueryRowContext(ctx,
		`SELECT `+apiTokenColumns+` FROM api_tokens WHERE token_hash = ?`, HashToken(presented))
	t, err := scanAPIToken(row)
	if err == sql.ErrNoRows {
		return nil, nil, "", ErrTokenInvalid
	}
	if err != nil {
		return nil, nil, "", err
	}
	if t.Revoked || (t.ExpiresAt != nil && time.Now().After(*t.ExpiresAt)) {
		return nil, nil, "", ErrTokenInvalid
	}
	u, err := s.UserByID(ctx, t.UserID)
	if err != nil || u.Disabled {
		return nil, nil, "", ErrTokenInvalid
	}
	effective := t.Role
	if !roleAtMost(effective, u.Role) {
		effective = u.Role
	}
	s.st.DB.ExecContext(ctx, `UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, time.Now().Unix(), t.ID)
	return t, u, effective, nil
}
