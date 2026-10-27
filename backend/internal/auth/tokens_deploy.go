package auth

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ScopeDeploy is the scope of a key made for the jd command line and for CI.
// Like an MCP key it narrows where the key goes rather than what its role may
// do there: it opens the deployment routes the API's table lists, including
// the writes that are otherwise a session's alone, and nothing outside them.
// It is minted with the administrator role, because those writes ask for it,
// and like every key it never holds more than its owner does now.
const ScopeDeploy = "deploy"

// maxTokenDeployments bounds one deploy key's list. A key for a handful of
// pipelines names a handful; one that needs more should reach them all.
const maxTokenDeployments = 64

// ErrDeployKeyOwner is an account that may not mint a deploy key.
var ErrDeployKeyOwner = errors.New("only an administrator can make a deploy key")

// CreateDeployToken mints a deploy key. Its role is the administrator's, which
// its owner must hold: the writes it exists for are system.admin on the
// dashboard's own pages. deployments narrows it to those deployments; none
// leaves it every deployment, the ones made later included.
func (s *Service) CreateDeployToken(ctx context.Context, owner *User, name string, ttl time.Duration, deployments []int64) (*APIToken, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, "", errors.New("token name required")
	}
	if !owner.Role.Can(CapSystemAdmin) {
		return nil, "", ErrDeployKeyOwner
	}
	ids, err := cleanDeployments(deployments)
	if err != nil {
		return nil, "", err
	}
	tx, err := s.st.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback()
	id, full, err := insertAPIToken(ctx, tx, owner, name, RoleAdmin, ScopeDeploy, ttl)
	if err != nil {
		return nil, "", err
	}
	for _, project := range ids {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO api_token_deployments(token_id, project_id) VALUES(?,?)`, id, project); err != nil {
			return nil, "", err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, "", err
	}
	tok, err := s.apiTokenByID(ctx, id)
	if err != nil {
		return nil, "", err
	}
	tok.DeploymentIDs = ids
	return tok, full, nil
}

// cleanDeployments refuses a list a person could not read back as meant: an id
// that cannot be a deployment, one named twice, more than a key may hold.
func cleanDeployments(ids []int64) ([]int64, error) {
	if len(ids) > maxTokenDeployments {
		return nil, fmt.Errorf("a deploy key reaches at most %d chosen deployments; leave the list empty to reach every one", maxTokenDeployments)
	}
	seen := map[int64]bool{}
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		switch {
		case id <= 0:
			return nil, errors.New("a deployment id is missing")
		case seen[id]:
			return nil, fmt.Errorf("deployment %d is listed twice", id)
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// TokenDeployments is the deployments a deploy key was narrowed to, read with
// the key on every request so a key is never trusted for more than its rows
// say. Empty is every deployment.
func (s *Service) TokenDeployments(ctx context.Context, tokenID int64) ([]int64, error) {
	rows, err := s.st.DB.QueryContext(ctx,
		`SELECT project_id FROM api_token_deployments WHERE token_id = ? ORDER BY project_id`, tokenID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// fillTokenDeployments lays each deploy key's deployments on it, in one read
// for the whole list.
func (s *Service) fillTokenDeployments(ctx context.Context, tokens []*APIToken) error {
	byID := map[int64]*APIToken{}
	for _, t := range tokens {
		if t.Scope == ScopeDeploy {
			t.DeploymentIDs = []int64{}
			byID[t.ID] = t
		}
	}
	if len(byID) == 0 {
		return nil
	}
	rows, err := s.st.DB.QueryContext(ctx,
		`SELECT token_id, project_id FROM api_token_deployments ORDER BY token_id, project_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var token, project int64
		if err := rows.Scan(&token, &project); err != nil {
			return err
		}
		if t, ok := byID[token]; ok {
			t.DeploymentIDs = append(t.DeploymentIDs, project)
		}
	}
	return rows.Err()
}

// ReachesDeployment reports whether a deploy key's list lets it reach a
// deployment: every one when the list is empty, else only those it names.
func (t *APIToken) ReachesDeployment(projectID int64) bool {
	if t == nil || t.Scope != ScopeDeploy {
		return false
	}
	if len(t.DeploymentIDs) == 0 {
		return true
	}
	for _, id := range t.DeploymentIDs {
		if id == projectID {
			return true
		}
	}
	return false
}

// EveryDeployment reports whether a deploy key reaches every deployment, which
// is what creating one, or touching what every deployment shares, asks of it.
func (t *APIToken) EveryDeployment() bool {
	return t != nil && t.Scope == ScopeDeploy && len(t.DeploymentIDs) == 0
}
