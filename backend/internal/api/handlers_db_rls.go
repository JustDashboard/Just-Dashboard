package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// Row-level security: which rows a table shows to whom.
//
// A policy is access control as a grant is, so its forms are not among the
// schema changes that only add: creating, changing and forcing need
// system.admin, which is what granting a privilege needs, and what takes the
// filter away — dropping a policy, turning security off — is destructive on
// top of that. Turning it off is a PATCH, not a DELETE, so the handler asks
// for the capability by hand (runDDL's destructive reason) the way the
// column-type change does. Each form plans its statement for runDDL, which
// shows it for ?preview=1 and runs it otherwise, so the statement in the
// dialog is the one that runs.
//
// The tester is a read. It becomes another role for one transaction that is
// never committed, and is held to the query runner's own rules for a read: a
// single SELECT by the classifier, and on a protected connection a read is
// what the guard lets through.

func (s *Server) mountDatabaseRLSRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/{id}/rls", s.handle(s.handleRLSRead))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapServiceControl))
		r.Method(http.MethodPost, "/{id}/rls/test", s.handle(s.handleRLSTest))
	})
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodPost, "/{id}/ddl/policy", s.handle(s.handleDDLCreatePolicy))
		r.Method(http.MethodPatch, "/{id}/ddl/policy", s.handle(s.handleDDLAlterPolicy))
		r.Method(http.MethodPatch, "/{id}/ddl/row-security", s.handle(s.handleDDLRowSecurity))
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodDelete, "/{id}/ddl/policy", s.handle(s.handleDDLDropPolicy))
		})
	})
}

// handleRLSRead returns a table's row-level security: whether it is on, who is
// held to it, and its policies.
func (s *Server) handleRLSRead(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	id, err := parseID(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	table := strings.TrimSpace(q.Get("table"))
	if table == "" {
		return httpx.BadRequest("table is required")
	}
	if _, err := s.sqlConnection(r, id); err != nil {
		return err
	}
	pool, conn, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	state, err := dbx.ReadRowSecurity(ctx, pool, conn.Driver, strings.TrimSpace(q.Get("schema")), table)
	switch {
	case err == nil:
	case errors.Is(err, dbx.ErrObjectNotFound):
		return httpx.Err(http.StatusNotFound, "not_found", "no table named "+table)
	case conn.Driver != dbx.DriverPostgres:
		return httpx.BadRequest("%v", err)
	default:
		return queryFailed(err)
	}
	httpx.JSON(w, http.StatusOK, state)
	return nil
}

type policyRequest struct {
	Schema      string `json:"schema"`
	Table       string `json:"table"`
	Name        string `json:"name"`
	Command     string `json:"command"`
	Restrictive bool   `json:"restrictive"`
	// Roles is absent to leave a policy's roles as they are in a change, and
	// empty for everybody.
	Roles     *[]string `json:"roles"`
	Using     string    `json:"using"`
	WithCheck string    `json:"withCheck"`
}

func (r *policyRequest) roles() []string {
	if r.Roles == nil {
		return nil
	}
	return *r.Roles
}

func (s *Server) policyContext(r *http.Request) (*policyRequest, *dbConnection, error) {
	var req policyRequest
	conn, err := s.ddlDecode(r, &req)
	if err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(req.Table) == "" {
		return nil, nil, ddlInvalid(r, "table is required")
	}
	return &req, conn, nil
}

func (s *Server) handleDDLCreatePolicy(w http.ResponseWriter, r *http.Request) error {
	req, conn, err := s.policyContext(r)
	if err != nil {
		return err
	}
	return s.runDDL(w, r, conn, 60*time.Second, "database.ddl.create_policy",
		map[string]any{"table": req.Table, "policy": req.Name, "command": req.Command, "restrictive": req.Restrictive,
			"roles": req.roles()}, "",
		func(context.Context, *sql.DB) (*dbx.DDLPlan, error) {
			return dbx.PlanCreatePolicy(conn.Driver, dbx.PolicySpec{
				Schema: req.Schema, Table: req.Table, Name: req.Name, Command: req.Command,
				Restrictive: req.Restrictive, Roles: req.roles(), Using: req.Using, WithCheck: req.WithCheck,
			})
		})
}

func (s *Server) handleDDLAlterPolicy(w http.ResponseWriter, r *http.Request) error {
	req, conn, err := s.policyContext(r)
	if err != nil {
		return err
	}
	if strings.TrimSpace(req.Name) == "" {
		return ddlInvalid(r, "the policy to change is required")
	}
	return s.runDDL(w, r, conn, 60*time.Second, "database.ddl.alter_policy",
		map[string]any{"table": req.Table, "policy": req.Name, "roles": req.Roles}, "",
		func(ctx context.Context, pool *sql.DB) (*dbx.DDLPlan, error) {
			return dbx.PlanAlterPolicy(ctx, pool, conn.Driver, dbx.PolicyChange{
				Schema: req.Schema, Table: req.Table, Name: req.Name, SetRoles: req.Roles != nil, Roles: req.roles(),
				Using: req.Using, WithCheck: req.WithCheck,
			})
		})
}

func (s *Server) handleDDLDropPolicy(w http.ResponseWriter, r *http.Request) error {
	req, conn, err := s.policyContext(r)
	if err != nil {
		return err
	}
	if strings.TrimSpace(req.Name) == "" {
		return ddlInvalid(r, "the policy to drop is required")
	}
	return s.runDDL(w, r, conn, 60*time.Second, "database.ddl.drop_policy",
		map[string]any{"table": req.Table, "policy": req.Name}, "",
		func(context.Context, *sql.DB) (*dbx.DDLPlan, error) {
			return dbx.PlanDropPolicy(conn.Driver, req.Schema, req.Table, req.Name)
		})
}

type rowSecurityRequest struct {
	Schema  string `json:"schema"`
	Table   string `json:"table"`
	Enabled *bool  `json:"enabled"`
	Force   *bool  `json:"force"`
}

// handleDDLRowSecurity turns a table's row-level security on or off, and holds
// its owner to it or not. Turning it off, or no longer holding the owner to
// it, is destructive: rows are shown to whoever it was filtering, and the route
// cannot know that from its path.
func (s *Server) handleDDLRowSecurity(w http.ResponseWriter, r *http.Request) error {
	var req rowSecurityRequest
	conn, err := s.ddlDecode(r, &req)
	if err != nil {
		return err
	}
	if strings.TrimSpace(req.Table) == "" {
		return ddlInvalid(r, "table is required")
	}
	change := dbx.RowSecurityChange{Schema: req.Schema, Table: req.Table, Enabled: req.Enabled, Force: req.Force}
	destructive := ""
	if change.TurnsOff() {
		destructive = "turning row-level security off, or no longer forcing it on the owner, shows rows to everyone it was filtering"
	}
	return s.runDDL(w, r, conn, 60*time.Second, "database.ddl.row_security",
		map[string]any{"table": req.Table, "enabled": req.Enabled, "force": req.Force}, destructive,
		func(context.Context, *sql.DB) (*dbx.DDLPlan, error) {
			return dbx.PlanRowSecurity(conn.Driver, change)
		})
}

// withoutClaimValues takes the values of a test's settings out of an engine's
// message, which quotes a value it rejected ("invalid input syntax for type
// uuid: …").
func withoutClaimValues(message string, claims []dbx.Claim) string {
	for _, claim := range claims {
		if claim.Value != "" {
			message = strings.ReplaceAll(message, claim.Value, "…")
		}
	}
	return message
}

type roleTestRequest struct {
	Role    string      `json:"role"`
	Claims  []dbx.Claim `json:"claims"`
	Query   string      `json:"query"`
	Schema  string      `json:"schema"`
	Table   string      `json:"table"`
	MaxRows int         `json:"maxRows"`
}

// handleRLSTest runs a SELECT as another role and shows the rows that role
// sees.
//
// The statement is read before the engine is dialled, by the classifier the
// query runner uses, and the run itself is a read-only transaction that is
// rolled back (dbx.RunAsRole). What is recorded is the role, the names of the
// settings it was given — their values are the operator's test data and may
// be a token — and the statement. A failure is recorded by its SQLSTATE: the
// engine's text can quote a value it rejected.
func (s *Server) handleRLSTest(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req roleTestRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	conn, err := s.sqlConnection(r, id)
	if err != nil {
		return err
	}
	test := dbx.RoleTest{
		Role: strings.TrimSpace(req.Role), Claims: req.Claims, Query: req.Query,
		Schema: strings.TrimSpace(req.Schema), Table: strings.TrimSpace(req.Table), MaxRows: req.MaxRows,
	}
	if _, err := dbx.CheckRoleTest(conn.Driver, test); err != nil {
		return httpx.BadRequest("%v", err)
	}
	pool, _, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	claims := make([]string, len(test.Claims))
	for i, claim := range test.Claims {
		claims[i] = claim.Name
	}
	detail := map[string]any{"role": test.Role, "claims": claims, "statement": strings.TrimSpace(test.Query)}
	res, err := dbx.RunAsRole(ctx, pool, conn.Driver, test)
	if err != nil {
		var refused dbx.ErrRoleTestRefused
		refusal := errors.As(err, &refused)
		if refusal {
			detail["error"] = err.Error()
		} else {
			detail["error"] = "the engine refused it"
			var state interface{ SQLState() string }
			if errors.As(err, &state) {
				detail["sqlstate"] = state.SQLState()
			}
		}
		httpx.SetAudit(r, "database.rls.test", conn.Name, detail)
		if refusal {
			return httpx.BadRequest("%v", err)
		}
		// The middleware writes the error it is handed into the trail and
		// the log, so the values are taken out before it is handed one.
		return retryable(httpx.Err(http.StatusBadGateway, "query_failed", withoutClaimValues(err.Error(), test.Claims)), err)
	}
	if res.Skipped {
		detail["skipped"] = true
	} else {
		detail["rowCount"] = res.Result.RowCount
	}
	httpx.SetAudit(r, "database.rls.test", conn.Name, detail)
	httpx.JSON(w, http.StatusOK, res)
	return nil
}
