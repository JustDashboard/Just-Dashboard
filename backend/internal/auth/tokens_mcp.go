package auth

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// An MCP key is an API key for an AI client. It opens the MCP endpoint and no
// other route, and it reaches only the databases granted to it, each either to
// read or to change.
//
// It carries its owner's role, or a narrower one chosen when it is made — a
// replacement keeps the key's it replaces — and like any key it never holds
// more than its owner does now: what one statement may do is decided as the
// SQL editor decides it for that role, so a destructive one still needs the
// destructive capability. The grants narrow
// where, and read narrows what. Which connections may be granted at all — not
// a protected one, not an engine the endpoint does not speak — is the API's
// to say, since it is the API that knows connections.

// TokenAccess is what an MCP key may do in one database.
type TokenAccess string

const (
	// AccessRead reads structure and rows and runs statements that only read.
	AccessRead TokenAccess = "read"
	// AccessWrite also runs statements that change what the database holds.
	AccessWrite TokenAccess = "write"
)

// TokenConnection is one database an MCP key reaches.
type TokenConnection struct {
	ConnectionID int64       `json:"connectionId"`
	Access       TokenAccess `json:"access"`
}

// maxTokenConnections bounds one key's grants. A key that reaches every
// database on a large server is still one list a person can read.
const maxTokenConnections = 64

var (
	ErrNotMCPToken   = errors.New("this key is not an MCP key")
	ErrTokenInactive = errors.New("this key was revoked or has expired")
)

// CreateMCPToken mints an MCP key reaching the databases granted. An empty
// role is the owner's.
func (s *Service) CreateMCPToken(ctx context.Context, owner *User, name string, role Role, ttl time.Duration, grants []TokenConnection) (*APIToken, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, "", errors.New("token name required")
	}
	if role == "" {
		role = owner.Role
	}
	if !role.Valid() {
		return nil, "", fmt.Errorf("unknown role %q", role)
	}
	if !roleAtMost(role, owner.Role) {
		return nil, "", fmt.Errorf("cannot mint a %s token from a %s account", role, owner.Role)
	}
	if err := checkTokenConnections(role, grants); err != nil {
		return nil, "", err
	}
	tx, err := s.st.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback()
	id, full, err := insertAPIToken(ctx, tx, owner, name, role, ScopeMCP, ttl)
	if err != nil {
		return nil, "", err
	}
	if err := writeTokenConnections(ctx, tx, id, grants); err != nil {
		return nil, "", err
	}
	if err := tx.Commit(); err != nil {
		return nil, "", err
	}
	tok, err := s.apiTokenByID(ctx, id)
	if err != nil {
		return nil, "", err
	}
	tok.Connections = sortedConnections(grants)
	return tok, full, nil
}

// SetTokenConnections replaces what an owner's MCP key reaches. The key keeps
// working, so a client already set up with it sees one database more or one
// fewer without a new key being pasted anywhere. Another account's key is not
// found: an administrator may revoke it, never re-point it.
func (s *Service) SetTokenConnections(ctx context.Context, owner *User, tokenID int64, grants []TokenConnection) (*APIToken, error) {
	tx, err := s.st.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var (
		userID, expires int64
		revoked         int
		scope, role     string
	)
	err = tx.QueryRowContext(ctx, `SELECT user_id, revoked, expires_at, scope, role FROM api_tokens WHERE id = ?`, tokenID).
		Scan(&userID, &revoked, &expires, &scope, &role)
	switch {
	case err != nil || userID != owner.ID:
		return nil, ErrNotFound
	case scope != ScopeMCP:
		return nil, ErrNotMCPToken
	case revoked == 1 || (expires > 0 && expires <= time.Now().Unix()):
		return nil, ErrTokenInactive
	}
	// What the key may do is its own role, narrowed to its owner's now, as
	// ResolveAPIToken narrows it on every call.
	effective := Role(role)
	if !roleAtMost(effective, owner.Role) {
		effective = owner.Role
	}
	if err := checkTokenConnections(effective, grants); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM api_token_connections WHERE token_id = ?`, tokenID); err != nil {
		return nil, err
	}
	if err := writeTokenConnections(ctx, tx, tokenID, grants); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	tok, err := s.apiTokenByID(ctx, tokenID)
	if err != nil {
		return nil, err
	}
	tok.Connections = sortedConnections(grants)
	return tok, nil
}

// TokenConnections is what an MCP key reaches now, read for every call it
// makes: a grant taken away answers the very next request.
func (s *Service) TokenConnections(ctx context.Context, tokenID int64) ([]TokenConnection, error) {
	rows, err := s.st.DB.QueryContext(ctx,
		`SELECT connection_id, access FROM api_token_connections WHERE token_id = ? ORDER BY connection_id`, tokenID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TokenConnection{}
	for rows.Next() {
		var c TokenConnection
		if err := rows.Scan(&c.ConnectionID, &c.Access); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// fillTokenConnections lays each MCP key's grants on it, in one read for the
// whole list.
func (s *Service) fillTokenConnections(ctx context.Context, tokens []*APIToken) error {
	byID := map[int64]*APIToken{}
	for _, t := range tokens {
		if t.Scope == ScopeMCP {
			t.Connections = []TokenConnection{}
			byID[t.ID] = t
		}
	}
	if len(byID) == 0 {
		return nil
	}
	rows, err := s.st.DB.QueryContext(ctx,
		`SELECT token_id, connection_id, access FROM api_token_connections ORDER BY token_id, connection_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id int64
			c  TokenConnection
		)
		if err := rows.Scan(&id, &c.ConnectionID, &c.Access); err != nil {
			return err
		}
		if t, ok := byID[id]; ok {
			t.Connections = append(t.Connections, c)
		}
	}
	return rows.Err()
}

// checkTokenConnections refuses a grant list that is empty, too long, names a
// connection twice, or asks to change a database through an owner whose role
// may not change one anywhere.
func checkTokenConnections(role Role, grants []TokenConnection) error {
	if len(grants) == 0 {
		return errors.New("choose at least one database the key reaches")
	}
	if len(grants) > maxTokenConnections {
		return fmt.Errorf("a key reaches at most %d databases", maxTokenConnections)
	}
	seen := map[int64]bool{}
	for _, g := range grants {
		switch {
		case g.ConnectionID <= 0:
			return errors.New("a connection id is missing")
		case seen[g.ConnectionID]:
			return fmt.Errorf("connection %d is listed twice", g.ConnectionID)
		case g.Access != AccessRead && g.Access != AccessWrite:
			return fmt.Errorf("access must be %q or %q", AccessRead, AccessWrite)
		case g.Access == AccessWrite && !role.Can(CapServiceControl):
			return fmt.Errorf("a %s account cannot change a database, so its keys can only read", role)
		}
		seen[g.ConnectionID] = true
	}
	return nil
}

func writeTokenConnections(ctx context.Context, db execer, tokenID int64, grants []TokenConnection) error {
	for _, g := range grants {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO api_token_connections(token_id, connection_id, access) VALUES(?,?,?)`,
			tokenID, g.ConnectionID, string(g.Access)); err != nil {
			return err
		}
	}
	return nil
}

func sortedConnections(grants []TokenConnection) []TokenConnection {
	out := append([]TokenConnection(nil), grants...)
	sort.Slice(out, func(i, j int) bool { return out[i].ConnectionID < out[j].ConnectionID })
	return out
}
