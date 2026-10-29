package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// An MCP key is an API key for an AI client. It opens the MCP endpoint and no
// other route, and it reaches only the databases granted to it, each either to
// read or to change, and the deployments if it was given them, either to read
// or to deploy as well.
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

// DeployAccess is what an MCP key may do with the deployments. It is one grant
// for every deployment, as the deployment pages are one section: a key that
// reads one reads them all.
type DeployAccess string

const (
	// DeployNone reaches no deployment: what every key made before the grant
	// existed holds.
	DeployNone DeployAccess = ""
	// DeployRead lists deployments, their runs, releases and run transcripts.
	DeployRead DeployAccess = "read"
	// DeployRun also queues a deploy or a rollback, held to what the key's
	// owner may do on the deployment pages.
	DeployRun DeployAccess = "deploy"
)

// Valid reports whether a is one of the three grants.
func (a DeployAccess) Valid() bool {
	return a == DeployNone || a == DeployRead || a == DeployRun
}

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

// CreateMCPToken mints an MCP key reaching the databases granted and, as
// deployments says, the deployments: every one, or only those deploymentIDs
// names, as a deploy key is narrowed. An empty role is the owner's.
func (s *Service) CreateMCPToken(ctx context.Context, owner *User, name string, role Role, ttl time.Duration, grants []TokenConnection, deployments DeployAccess, deploymentIDs []int64) (*APIToken, string, error) {
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
	if err := checkTokenGrants(role, grants, deployments); err != nil {
		return nil, "", err
	}
	ids, err := cleanMCPDeployments(deployments, deploymentIDs)
	if err != nil {
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
	if _, err := tx.ExecContext(ctx, `UPDATE api_tokens SET deploy_access = ? WHERE id = ?`, string(deployments), id); err != nil {
		return nil, "", err
	}
	if err := writeTokenDeployments(ctx, tx, id, ids); err != nil {
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
	tok.DeploymentIDs = ids
	return tok, full, nil
}

// cleanMCPDeployments is the deployments an MCP key's grant is narrowed to,
// held to the rules a deploy key's list is, and refused for a key given no
// deployments at all, which a list would only make look as if it reached some.
func cleanMCPDeployments(access DeployAccess, ids []int64) ([]int64, error) {
	out, err := cleanDeployments(ids)
	if err != nil {
		return nil, err
	}
	if len(out) > 0 && access == DeployNone {
		return nil, errors.New("a key given no deployments is not narrowed to some: choose read or deploy for them first")
	}
	return out, nil
}

// writeTokenDeployments replaces the deployments a key is narrowed to.
func writeTokenDeployments(ctx context.Context, db execer, tokenID int64, ids []int64) error {
	if _, err := db.ExecContext(ctx, `DELETE FROM api_token_deployments WHERE token_id = ?`, tokenID); err != nil {
		return err
	}
	for _, project := range ids {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO api_token_deployments(token_id, project_id) VALUES(?,?)`, tokenID, project); err != nil {
			return err
		}
	}
	return nil
}

// SetTokenConnections replaces what an owner's MCP key reaches: its databases,
// its deployment grant unless deployments is nil, which keeps it, and the
// deployments that grant is narrowed to unless deploymentIDs is nil, which
// keeps them (a key given no deployments keeps none). The key keeps working,
// so a client already set up with it sees one database more or one fewer
// without a new key being pasted anywhere. Another account's key is not
// found: an administrator may revoke it, never re-point it.
func (s *Service) SetTokenConnections(ctx context.Context, owner *User, tokenID int64, grants []TokenConnection, deployments *DeployAccess, deploymentIDs *[]int64) (*APIToken, error) {
	tx, err := s.st.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var (
		userID, expires     int64
		revoked             int
		scope, role, deploy string
	)
	err = tx.QueryRowContext(ctx, `SELECT user_id, revoked, expires_at, scope, role, deploy_access FROM api_tokens WHERE id = ?`, tokenID).
		Scan(&userID, &revoked, &expires, &scope, &role, &deploy)
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
	access := DeployAccess(deploy)
	if deployments != nil {
		access = *deployments
	}
	if err := checkTokenGrants(effective, grants, access); err != nil {
		return nil, err
	}
	var ids []int64
	switch {
	case access == DeployNone:
		// No deployments: nothing to be narrowed to, whatever was kept.
		if deploymentIDs != nil && len(*deploymentIDs) > 0 {
			return nil, errors.New("a key given no deployments is not narrowed to some: choose read or deploy for them first")
		}
	case deploymentIDs != nil:
		if ids, err = cleanMCPDeployments(access, *deploymentIDs); err != nil {
			return nil, err
		}
	default:
		if ids, err = tokenDeploymentsTx(ctx, tx, tokenID); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM api_token_connections WHERE token_id = ?`, tokenID); err != nil {
		return nil, err
	}
	if err := writeTokenConnections(ctx, tx, tokenID, grants); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE api_tokens SET deploy_access = ? WHERE id = ?`, string(access), tokenID); err != nil {
		return nil, err
	}
	if err := writeTokenDeployments(ctx, tx, tokenID, ids); err != nil {
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
	tok.DeploymentIDs = append([]int64{}, ids...)
	return tok, nil
}

// tokenDeploymentsTx is TokenDeployments inside a transaction.
func tokenDeploymentsTx(ctx context.Context, tx *sql.Tx, tokenID int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT project_id FROM api_token_deployments WHERE token_id = ? ORDER BY project_id`, tokenID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
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

// checkTokenGrants refuses a key that reaches nothing, a grant list that is
// too long or names a connection twice, and a grant to change a database or to
// deploy through an owner whose role may do neither anywhere: both are what
// service.control allows on the dashboard's own pages.
func checkTokenGrants(role Role, grants []TokenConnection, deployments DeployAccess) error {
	switch {
	case !deployments.Valid():
		return fmt.Errorf("deployments must be %q, %q or %q", DeployNone, DeployRead, DeployRun)
	case deployments == DeployRun && !role.Can(CapServiceControl):
		return fmt.Errorf("a %s account cannot deploy, so its keys can only read the deployments", role)
	case len(grants) == 0 && deployments == DeployNone:
		return errors.New("choose at least one database or the deployments for the key to reach")
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
