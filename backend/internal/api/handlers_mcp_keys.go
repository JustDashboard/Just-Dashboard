package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// MCP keys are made and re-pointed beside every other key, under /tokens and
// a human's session: the databases and the deployments a key reaches are part
// of the key. They are listed, revoked and deleted by the routes every key
// already has.

type mcpTokenRequest struct {
	Name string `json:"name"`
	// Role narrows the owner's; empty is the owner's own. A replacement sends
	// the role of the key it replaces.
	Role        auth.Role              `json:"role"`
	TTLDays     int                    `json:"ttlDays"`
	Connections []auth.TokenConnection `json:"connections"`
	// Deployments is what the key may do with the deployments: "" nothing,
	// "read", or "deploy".
	Deployments auth.DeployAccess `json:"deployments"`
}

type mcpConnectionsRequest struct {
	Connections []auth.TokenConnection `json:"connections"`
	// Deployments left out keeps the key's deployment grant as it is.
	Deployments *auth.DeployAccess `json:"deployments"`
}

func (s *Server) handleCreateMCPToken(w http.ResponseWriter, r *http.Request) error {
	var req mcpTokenRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	conns, err := s.mcpGrantable(r.Context(), req.Connections)
	if err != nil {
		return err
	}
	var ttl time.Duration
	if req.TTLDays > 0 {
		ttl = time.Duration(req.TTLDays) * 24 * time.Hour
	}
	tok, secret, err := s.Auth.CreateMCPToken(r.Context(), httpx.MustPrincipal(r).User, req.Name, req.Role, ttl, req.Connections, req.Deployments)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "dashboard.token.create", tok.Name, map[string]any{
		"role": tok.Role, "ttlDays": req.TTLDays, "scope": tok.Scope,
		"connections": mcpGrantedNames(conns, tok.Connections), "deployments": tok.Deployments,
	})
	// The secret is returned exactly once; only its hash is retained.
	httpx.JSON(w, http.StatusCreated, map[string]any{"token": tok, "secret": secret})
	return nil
}

func (s *Server) handleSetMCPTokenConnections(w http.ResponseWriter, r *http.Request) error {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		return httpx.BadRequest("invalid token id")
	}
	var req mcpConnectionsRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	conns, err := s.mcpGrantable(r.Context(), req.Connections)
	if err != nil {
		return err
	}
	tok, err := s.Auth.SetTokenConnections(r.Context(), httpx.MustPrincipal(r).User, id, req.Connections, req.Deployments)
	switch {
	case errors.Is(err, auth.ErrNotFound):
		return httpx.ErrNotFound
	case errors.Is(err, auth.ErrNotMCPToken):
		return httpx.Err(http.StatusConflict, "not_mcp_key", "this key opens the API, not the MCP endpoint, and reaches no databases or deployments of its own")
	case errors.Is(err, auth.ErrTokenInactive):
		return httpx.Err(http.StatusConflict, "token_inactive", "this key was revoked or has expired; make a new one")
	case err != nil:
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "dashboard.token.connections", tok.Name, map[string]any{
		"connections": mcpGrantedNames(conns, tok.Connections), "deployments": tok.Deployments,
	})
	httpx.JSON(w, http.StatusOK, tok)
	return nil
}

// mcpGrantable reads each connection a key is to reach, and refuses one the
// endpoint would refuse every call to anyway: a protected one, and one whose
// engine the tools do not speak. The endpoint asks again on every call
// (mcpCall.open), since a connection can be protected after the key was made.
func (s *Server) mcpGrantable(ctx context.Context, grants []auth.TokenConnection) (map[int64]*dbConnection, error) {
	out := map[int64]*dbConnection{}
	for _, g := range grants {
		rec, err := scanDBConn(s.Store.DB.QueryRowContext(ctx,
			`SELECT `+dbConnColumns+` FROM db_connections WHERE id = ?`, g.ConnectionID).Scan)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, httpx.BadRequest("connection %d does not exist", g.ConnectionID)
		}
		if err != nil {
			return nil, httpx.Internal(err)
		}
		conn := rec.conn
		if !dbx.Capable(conn.Driver, "", "mcp") {
			return nil, httpx.BadRequest("%s is a %s database, and MCP clients reach the SQL engines", conn.Name, driverLabels[conn.Driver].label)
		}
		if conn.ReadOnly {
			return nil, httpx.Err(http.StatusConflict, "connection_read_only",
				conn.Name+" is protected, and no MCP key reaches a protected database")
		}
		out[conn.ID] = &conn
	}
	return out, nil
}

// mcpGrantedNames are a key's connections by name, for the audit entry that
// records what a key was given.
func mcpGrantedNames(conns map[int64]*dbConnection, grants []auth.TokenConnection) []map[string]any {
	out := make([]map[string]any, 0, len(grants))
	for _, g := range grants {
		name := fmt.Sprint(g.ConnectionID)
		if conn, ok := conns[g.ConnectionID]; ok {
			name = conn.Name
		}
		out = append(out, map[string]any{"connection": name, "access": g.Access})
	}
	sort.Slice(out, func(i, j int) bool { return fmt.Sprint(out[i]["connection"]) < fmt.Sprint(out[j]["connection"]) })
	return out
}
