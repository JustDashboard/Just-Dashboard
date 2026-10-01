package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// Watching a SQL server and keeping it healthy: how busy it is, who is
// waiting on whom, what its tables and indexes cost, the maintenance commands
// that fix what those show, its parameters, and what each account may do.
//
// The capability on each route follows what the act does, as everywhere:
//
//   - Reading any of it is on the read surface. A lock table, a replication
//     position and a table's scan count carry nothing the activity list and
//     the storage overview do not already hand any role.
//   - Running a maintenance command and zeroing the statement statistics are
//     service.control: they change how the server performs and no data. The
//     one maintenance action that can lose rows — MySQL's REPAIR — demands the
//     destructive capability by hand, because the route cannot know from its
//     path which action the body names.
//   - Stopping a statement sits with ending a session, in the destructive
//     group: work in flight is thrown away either way.
//   - Changing a server parameter and granting or revoking a privilege are
//     system.admin, beside creating an account.
//
// Nothing here takes SQL from the request. An action, a privilege, a sort and
// a checkpoint mode are each one of a closed set; a table, a role and a
// parameter are names the engine layer validates and quotes, or looks up in
// the engine's own list.

func (s *Server) mountDatabaseOpsRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/{id}/locks", s.handle(s.handleDBLocks))
	r.Method(http.MethodGet, "/{id}/replication", s.handle(s.handleDBReplication))
	r.Method(http.MethodGet, "/{id}/tablestats", s.handle(s.handleDBTableStats))
	r.Method(http.MethodGet, "/{id}/indexstats", s.handle(s.handleDBIndexStats))
	// Which maintenance actions this engine has, so the page builds its menu
	// from the server's list rather than from one of its own.
	r.Method(http.MethodGet, "/{id}/maintenance", s.handle(s.handleDBMaintenanceActions))
	r.Method(http.MethodGet, "/{id}/settings", s.handle(s.handleDBSettingsList))
	r.Method(http.MethodGet, "/{id}/server/roles/{name}", s.handle(s.handleDBRoleDetail))
	r.Method(http.MethodGet, "/{id}/server/grants", s.handle(s.handleDBGrants))
	r.Method(http.MethodGet, "/{id}/server/privileges", s.handle(s.handleDBPrivilegeLevels))
	// The views that are one engine's own.
	r.Method(http.MethodGet, "/{id}/clickhouse/parts", s.handle(s.handleClickHouseParts))
	r.Method(http.MethodGet, "/{id}/clickhouse/merges", s.handle(s.handleClickHouseMerges))
	r.Method(http.MethodGet, "/{id}/clickhouse/mutations", s.handle(s.handleClickHouseMutations))
	r.Method(http.MethodGet, "/{id}/clickhouse/queries", s.handle(s.handleClickHouseQueries))
	r.Method(http.MethodGet, "/{id}/sqlite/file", s.handle(s.handleSQLiteFile))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapServiceControl))
		r.Method(http.MethodPost, "/{id}/maintenance", s.handle(s.handleDBMaintenance))
		r.Method(http.MethodPost, "/{id}/statements/reset", s.handle(s.handleDBStatementsReset))
	})
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodPut, "/{id}/settings", s.handle(s.handleDBSettingChange))
		r.Method(http.MethodPost, "/{id}/server/roles/{name}/privileges", s.handle(s.handleDBPrivilegeGrant))
		r.Method(http.MethodPost, "/{id}/server/roles/{name}/privileges/revoke", s.handle(s.handleDBPrivilegeRevoke))
	})
	s.destructive(r, func(r chi.Router) {
		// Cancelling stops the statement and keeps the session; killing, in
		// the group beside this one, ends the session. Both discard work in
		// flight, which is what puts them here.
		r.Method(http.MethodPost, "/{id}/activity/cancel", s.handle(s.handleDBCancel))
	})
}

// --- the snapshot ------------------------------------------------------------

// dbStatsResponse is a SQL engine's snapshot with the dashboard's own pool
// beside it. The pool was the whole answer before there was a snapshot, and
// stays under its old key.
type dbStatsResponse struct {
	*dbx.ServerStats
	Pool *dbx.PoolStats `json:"pool"`
}

// dbSQLStats answers GET /{id}/stats for a SQL engine: one reading of the
// server's counters and gauges, stamped with this server's clock.
//
// It is polled, so it is one round of cheap queries against the statistics
// views and nothing that scans a table. The counters are raw; the page keeps
// the previous reading and divides by the time between the two.
func (s *Server) dbSQLStats(w http.ResponseWriter, r *http.Request, id int64) error {
	pool, conn, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 15*time.Second)
	defer cancel()
	stats, err := dbx.ReadServerStats(ctx, pool, conn.Driver)
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	httpx.JSON(w, http.StatusOK, dbStatsResponse{ServerStats: stats, Pool: s.modules.dbs.Stats(id)})
	return nil
}

// --- sessions ----------------------------------------------------------------

// handleDBCancel stops the statement a session is running and leaves the
// session connected.
//
// It is the first thing to reach for: the application's connection survives,
// so its pool never notices, and only the one statement's work is lost.
// What it cannot do is free a session that is idle inside a transaction —
// there is no statement to stop — and that is what kill is for. No typed
// phrase, for the reason kill has none.
func (s *Server) handleDBCancel(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req killRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if strings.TrimSpace(req.PID) == "" {
		return httpx.BadRequest("a session id is required")
	}
	pool, conn, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	if err := dbx.CancelQuery(ctx, pool, conn.Driver, req.PID); err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "database.session.cancel", conn.Name, map[string]any{"pid": req.PID})
	httpx.JSON(w, http.StatusOK, map[string]any{"cancelled": req.PID})
	return nil
}

// --- locks, replication, statistics --------------------------------------------

func (s *Server) handleDBLocks(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	id, err := parseID(r)
	if err != nil {
		return err
	}
	pool, conn, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	report, err := dbx.ListLocks(ctx, pool, conn.Driver)
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	httpx.JSON(w, http.StatusOK, report)
	return nil
}

func (s *Server) handleDBReplication(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	id, err := parseID(r)
	if err != nil {
		return err
	}
	pool, conn, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	report, err := dbx.ReadReplication(ctx, pool, conn.Driver)
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	httpx.JSON(w, http.StatusOK, report)
	return nil
}

// statsOptions reads the bounds a statistics read takes. The limit is clamped
// inside dbx; a caller cannot raise it past what is safe to ask of a server.
func statsOptions(r *http.Request) dbx.StatsOptions {
	q := r.URL.Query()
	return dbx.StatsOptions{
		Schema: strings.TrimSpace(q.Get("schema")),
		Table:  strings.TrimSpace(q.Get("table")),
		Limit:  atoiDefault(q.Get("limit"), 0),
	}
}

func (s *Server) handleDBTableStats(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	id, err := parseID(r)
	if err != nil {
		return err
	}
	pool, conn, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	report, err := dbx.ReadTableStats(ctx, pool, conn.Driver, statsOptions(r))
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	httpx.JSON(w, http.StatusOK, report)
	return nil
}

func (s *Server) handleDBIndexStats(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	id, err := parseID(r)
	if err != nil {
		return err
	}
	pool, conn, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	report, err := dbx.ReadIndexStats(ctx, pool, conn.Driver, statsOptions(r))
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	httpx.JSON(w, http.StatusOK, report)
	return nil
}

// --- maintenance -------------------------------------------------------------

// handleDBMaintenanceActions lists what the engine can be asked to do to
// itself. It reads the saved connection and nothing else: the list depends on
// the engine, not on whether the server is up.
func (s *Server) handleDBMaintenanceActions(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	id, err := parseID(r)
	if err != nil {
		return err
	}
	conn, _, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	actions := dbx.MaintenanceActionsFor(conn.Driver)
	httpx.JSON(w, http.StatusOK, map[string]any{"actions": actions, "supported": len(actions) > 0})
	return nil
}

type maintenanceRequest struct {
	Action  string                 `json:"action"`
	Schema  string                 `json:"schema"`
	Table   string                 `json:"table"`
	Index   string                 `json:"index"`
	Options dbx.MaintenanceOptions `json:"options"`
}

// handleDBMaintenance runs one maintenance action and returns what the engine
// printed.
//
// The action is looked up before anything is dialled, for two reasons. An
// unknown one is refused without a connection. And whether the action needs
// the destructive capability is a property of the action, read from the same
// closed list the page drew its menu from — so the check cannot be skipped by
// naming an action the list does not have: that request has already failed.
func (s *Server) handleDBMaintenance(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req maintenanceRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	if !conn.Driver.IsSQL() {
		return httpx.BadRequest("this endpoint is for SQL engines; %s uses its own surface", conn.Driver)
	}
	action, ok := dbx.MaintenanceActionFor(conn.Driver, strings.TrimSpace(req.Action))
	if !ok {
		ids := []string{}
		for _, a := range dbx.MaintenanceActionsFor(conn.Driver) {
			ids = append(ids, a.ID)
		}
		if len(ids) == 0 {
			return httpx.BadRequest("%s has no maintenance actions from here", conn.Driver)
		}
		return httpx.BadRequest("action must be one of %s", strings.Join(ids, ", "))
	}
	if action.Destructive {
		p := httpx.MustPrincipal(r)
		if !p.Can(auth.CapDestructive) {
			return httpx.Err(http.StatusForbidden, "forbidden",
				action.ID+" can discard data it cannot recover and your role does not permit it")
		}
		if !s.destrLim.Allow(p.Username() + "|dbmaintenance") {
			return httpx.Err(http.StatusTooManyRequests, "rate_limited",
				"too many destructive maintenance actions, slow down")
		}
	}
	pool, _, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	// As long as an index build is given: a VACUUM FULL of a large table is
	// the same order of work.
	ctx, cancel := timeoutCtx(r, 30*time.Minute)
	defer cancel()
	detail := map[string]any{"action": action.ID, "driver": conn.Driver}
	for key, value := range map[string]string{"schema": req.Schema, "table": req.Table, "index": req.Index} {
		if strings.TrimSpace(value) != "" {
			detail[key] = strings.TrimSpace(value)
		}
	}
	result, err := dbx.RunMaintenance(ctx, pool, conn.Driver, dsn, dbx.MaintenanceRequest{
		Action: action.ID, Schema: req.Schema, Table: req.Table, Index: req.Index, Options: req.Options,
	})
	if err != nil {
		detail["error"] = err.Error()
		httpx.SetAudit(r, "database.maintenance", conn.Name, detail)
		// The engine's own refusal — a table that is not there, a lock it
		// could not take — is about what was asked, as a rejected DDL is.
		return httpx.BadRequest("%v", err)
	}
	detail["statements"], detail["ok"] = result.Statements, result.OK
	httpx.SetAudit(r, "database.maintenance", conn.Name, detail)
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

// handleDBStatementsReset zeroes the engine's statement statistics. Nothing
// in the database changes; what goes is the history the statements list is
// drawn from, which is why this is an ordinary write.
func (s *Server) handleDBStatementsReset(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	pool, conn, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	statement, err := dbx.ResetStatements(ctx, pool, conn.Driver)
	if errors.Is(err, dbx.ErrUnsupported) {
		return httpx.BadRequest("%s keeps no statement statistics that can be reset from here", conn.Driver)
	}
	if err != nil {
		httpx.SetAudit(r, "database.statements.reset", conn.Name, map[string]any{"error": err.Error()})
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "database.statements.reset", conn.Name, map[string]any{"statement": statement})
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "statement": statement})
	return nil
}

// --- settings ----------------------------------------------------------------

// sensitiveSetting matches the parameters whose value can carry a credential:
// a replication connection string with its password, a command line with a
// token in it, the passphrase of a private key. The engine shows these to a
// superuser, which is what the dashboard's connection usually is — so the
// engine's own guard does not apply to the person looking at this page.
var sensitiveSetting = regexp.MustCompile(`(?i)conninfo|password|passphrase|secret|token|_command$|private_key`)

// redactSettings blanks the values that may hold a credential for a viewer
// who is not an administrator. The parameter stays in the list, marked, so
// the list is complete and says what it is not showing.
func redactSettings(list []dbx.Setting, admin bool) []dbx.Setting {
	if admin {
		return list
	}
	for i := range list {
		if list[i].Value != "" && sensitiveSetting.MatchString(list[i].Name) {
			list[i].Value, list[i].Default, list[i].Redacted = "", "", true
		}
	}
	return list
}

// handleDBSettingsList returns the server's parameters: the ones an operator
// asks about, or with ?all=1 every one the engine has, each with its type,
// its bounds, its default and whether a change needs a restart.
func (s *Server) handleDBSettingsList(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	id, err := parseID(r)
	if err != nil {
		return err
	}
	conn, _, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	if !conn.Driver.IsSQL() {
		// Redis and MongoDB report their server through the older route's
		// shape; it is the same list under this address.
		return s.handleDBSettings(w, r)
	}
	all := r.URL.Query().Get("all") == "1"
	pool, _, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	list, err := dbx.ListSettings(ctx, pool, conn.Driver, all)
	if errors.Is(err, dbx.ErrUnsupported) {
		httpx.JSON(w, http.StatusOK, map[string]any{"settings": []dbx.Setting{}, "supported": false, "all": all,
			"writable": false, "reason": fmt.Sprintf("%s parameters are not read from here", conn.Driver)})
		return nil
	}
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	admin := httpx.MustPrincipal(r).Can(auth.CapSystemAdmin)
	httpx.JSON(w, http.StatusOK, map[string]any{
		"settings": redactSettings(list, admin), "supported": true, "all": all,
		// Whether the engine can persist a change at all; whether this
		// viewer may ask for one is the capability's to say.
		"writable": dbx.SettingsWritable(conn.Driver),
	})
	return nil
}

type settingRequest struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	// Reset returns the parameter to its default instead of setting Value.
	Reset bool `json:"reset"`
}

// handleDBSettingChange sets one server parameter, or resets it, and persists
// the change where the engine can.
//
// The name is not trusted to be a name: it is looked up in the engine's own
// list and the list's spelling is what reaches the statement. The value is
// checked against the type and range the engine published before the engine
// sees it. A value that may be a credential is kept out of the audit trail,
// which is read by more people than may read the parameter.
func (s *Server) handleDBSettingChange(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req settingRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return httpx.BadRequest("a parameter name is required")
	}
	pool, conn, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	action := "database.setting.set"
	if req.Reset {
		action = "database.setting.reset"
	}
	sensitive := sensitiveSetting.MatchString(name)
	detail := map[string]any{"name": name, "driver": conn.Driver}
	if !req.Reset && !sensitive {
		detail["value"] = req.Value
	}
	change, err := dbx.ChangeSetting(ctx, pool, conn.Driver, name, req.Value, req.Reset)
	if err != nil {
		var refused dbx.ErrSettingRequest
		if !errors.As(err, &refused) && !errors.Is(err, dbx.ErrUnsupported) {
			detail["error"] = err.Error()
			httpx.SetAudit(r, action, conn.Name, detail)
		}
		// A refusal of ours and a refusal of the engine's are both about the
		// value that was sent, and both are printed beside the field.
		return httpx.BadRequest("%v", err)
	}
	detail["persisted"], detail["restartRequired"] = change.Persisted, change.RestartRequired
	if !sensitive {
		detail["statements"] = change.Statements
	}
	httpx.SetAudit(r, action, conn.Name, detail)
	httpx.JSON(w, http.StatusOK, change)
	return nil
}

// --- roles and privileges ------------------------------------------------------

// handleDBRoleDetail describes one account: its attributes, its memberships
// and what it holds. For Redis and MongoDB it is the row the list already
// shows, in the same shape.
func (s *Server) handleDBRoleDetail(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	name := httpx.URLParam(r, "name")
	host := r.URL.Query().Get("host")
	_, conn, dsn, err := s.dbAdmin(r)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	if !conn.Driver.IsSQL() {
		roles, err := s.documentRoles(ctx, conn, dsn)
		if err != nil {
			return err
		}
		for _, role := range roles {
			if role.Name == name {
				httpx.JSON(w, http.StatusOK, dbx.RoleDetail{Role: role, Attributes: map[string]bool{},
					Members: []string{}, Config: []string{}, Grants: []dbx.Grant{},
					Editable: dbx.EditableRoleAttributes(conn.Driver)})
				return nil
			}
		}
		return httpx.ErrNotFound
	}
	pool, _, err := s.dbPool(ctx, conn.ID)
	if err != nil {
		return err
	}
	detail, err := dbx.ReadRoleDetail(ctx, pool, conn.Driver, name, host)
	if errors.Is(err, dbx.ErrNoSuchRole) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	httpx.JSON(w, http.StatusOK, detail)
	return nil
}

// documentRoles lists a Redis or MongoDB server's accounts.
func (s *Server) documentRoles(ctx context.Context, conn *dbConnection, dsn string) ([]dbx.Role, error) {
	if conn.Driver == dbx.DriverMongo {
		client, err := dbx.MongoClient(ctx, dsn)
		if err != nil {
			return nil, httpx.Err(http.StatusBadGateway, "connect_failed", err.Error())
		}
		defer client.Disconnect(context.Background())
		roles, err := dbx.MongoUsers(ctx, client)
		if err != nil {
			return nil, httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
		}
		return roles, nil
	}
	client, err := dbx.RedisClient(ctx, dsn, 0)
	if err != nil {
		return nil, httpx.Err(http.StatusBadGateway, "connect_failed", err.Error())
	}
	defer client.Close()
	roles, err := dbx.RedisUsers(ctx, client)
	if err != nil {
		return nil, httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	return withoutACLSecrets(roles), nil
}

// handleDBPrivilegeLevels publishes the closed set a grant is made from: the
// levels this engine grants at, the privileges each takes, and which names
// identify the object. No server is dialled; the set belongs to the engine.
func (s *Server) handleDBPrivilegeLevels(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	id, err := parseID(r)
	if err != nil {
		return err
	}
	conn, _, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	levels := dbx.PrivilegeLevelsFor(conn.Driver)
	httpx.JSON(w, http.StatusOK, map[string]any{
		"levels": levels, "supported": len(levels) > 0,
		"editable": dbx.EditableRoleAttributes(conn.Driver),
	})
	return nil
}

// handleDBGrants lists what roles hold: one role's grants with ?role=, or
// everybody's on one schema or table with ?schema= and ?table=.
func (s *Server) handleDBGrants(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	id, err := parseID(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	pool, conn, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	grants, truncated, err := dbx.ListGrants(ctx, pool, conn.Driver, dbx.GrantFilter{
		Role: strings.TrimSpace(q.Get("role")), Host: strings.TrimSpace(q.Get("host")),
		Schema: strings.TrimSpace(q.Get("schema")), Table: strings.TrimSpace(q.Get("table")),
	})
	if errors.Is(err, dbx.ErrUnsupported) {
		httpx.JSON(w, http.StatusOK, map[string]any{"grants": []dbx.Grant{}, "truncated": false, "supported": false,
			"reason": fmt.Sprintf("%s grants are not listed from here", conn.Driver)})
		return nil
	}
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"grants": grants, "truncated": truncated, "supported": true})
	return nil
}

type privilegeRequest struct {
	Host       string   `json:"host"`
	Level      string   `json:"level"`
	Database   string   `json:"database"`
	Schema     string   `json:"schema"`
	Table      string   `json:"table"`
	Privileges []string `json:"privileges"`
	// MemberOf is the role to grant, at the role level.
	MemberOf    string `json:"memberOf"`
	GrantOption bool   `json:"grantOption"`
	// Future extends the change to objects created later.
	Future bool `json:"future"`
}

func (s *Server) handleDBPrivilegeGrant(w http.ResponseWriter, r *http.Request) error {
	return s.changePrivileges(w, r, false)
}

func (s *Server) handleDBPrivilegeRevoke(w http.ResponseWriter, r *http.Request) error {
	return s.changePrivileges(w, r, true)
}

// changePrivileges grants or revokes privileges at one level, on the
// database that holds the object.
//
// With ?preview=1 the statements are rendered and returned without being
// run, so the page can show the server's own SQL before the operator commits
// to it — the same capability either way, since a preview of a grant is only
// useful to somebody who may make it.
func (s *Server) changePrivileges(w http.ResponseWriter, r *http.Request, revoke bool) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req privilegeRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	if !conn.Driver.IsSQL() {
		return httpx.BadRequest("%s privileges are not granted object by object from here", conn.Driver)
	}
	change := dbx.PrivilegeChange{
		Role: httpx.URLParam(r, "name"), Host: strings.TrimSpace(req.Host), Level: strings.TrimSpace(req.Level),
		Database: strings.TrimSpace(req.Database), Schema: strings.TrimSpace(req.Schema), Table: strings.TrimSpace(req.Table),
		Privileges: req.Privileges, MemberOf: strings.TrimSpace(req.MemberOf),
		GrantOption: req.GrantOption, Future: req.Future, Revoke: revoke,
	}
	// The connection's own database is the one meant when none is named.
	if change.Database == "" {
		change.Database = conn.Database
	}
	statements, err := dbx.PrivilegeStatements(conn.Driver, change)
	if err != nil {
		return privilegeError(conn.Driver, err)
	}
	// A database reached by rewriting the connection string is bounded like
	// every other database name that becomes part of one.
	if privilegeElsewhere(conn, change) && !dbNameRe.MatchString(change.Database) {
		return httpx.BadRequest("a database name is letters, digits and underscores")
	}
	if r.URL.Query().Get("preview") == "1" {
		httpx.SkipAudit(r)
		httpx.JSON(w, http.StatusOK, map[string]any{"statements": statements, "preview": true})
		return nil
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	action := "database.role.grant"
	if revoke {
		action = "database.role.revoke"
	}
	detail := map[string]any{"role": change.Role, "level": change.Level, "driver": conn.Driver}
	for key, value := range map[string]string{"host": change.Host, "database": change.Database,
		"schema": change.Schema, "table": change.Table, "memberOf": change.MemberOf} {
		if value != "" {
			detail[key] = value
		}
	}
	if len(change.Privileges) > 0 {
		detail["privileges"] = change.Privileges
	}
	statements, err = s.runPrivilegeChange(ctx, conn, dsn, change)
	if err != nil {
		var refused dbx.ErrPrivilegeRequest
		if errors.As(err, &refused) || errors.Is(err, dbx.ErrUnsupported) {
			return privilegeError(conn.Driver, err)
		}
		detail["error"] = err.Error()
		httpx.SetAudit(r, action, conn.Name, detail)
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	detail["statements"] = statements
	httpx.SetAudit(r, action, conn.Name, detail)
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "statements": statements})
	return nil
}

func privilegeError(driver dbx.Driver, err error) error {
	if errors.Is(err, dbx.ErrUnsupported) {
		return httpx.BadRequest("%s privileges are not granted object by object from here", driver)
	}
	return httpx.BadRequest("%v", err)
}

// privilegeElsewhere reports whether a change has to run on a connection to
// another database of the same server than the one this connection opens.
func privilegeElsewhere(conn *dbConnection, change dbx.PrivilegeChange) bool {
	return dbx.PrivilegesNeedDatabase(conn.Driver, change.Level) && change.Database != "" && change.Database != conn.Database
}

// runPrivilegeChange runs a change connected to the database that holds the
// object, where the engine keeps its privileges per database. The sibling
// pool lives for this request, as the one a database grant opens does.
func (s *Server) runPrivilegeChange(ctx context.Context, conn *dbConnection, dsn string, change dbx.PrivilegeChange) ([]string, error) {
	if privilegeElsewhere(conn, change) {
		db, err := dbx.OpenDatabase(ctx, conn.Driver, dsn, change.Database)
		if err != nil {
			return nil, err
		}
		defer db.Close()
		return dbx.ChangePrivileges(ctx, db, conn.Driver, change)
	}
	pool, err := s.modules.dbs.Pool(ctx, conn.ID, conn.Driver, dsn)
	if err != nil {
		return nil, err
	}
	return dbx.ChangePrivileges(ctx, pool, conn.Driver, change)
}

// --- engine views ------------------------------------------------------------

// engineView resolves a connection for a route that belongs to one engine,
// refusing the others by name.
func (s *Server) engineView(r *http.Request, want dbx.Driver, label string) (*sql.DB, *dbConnection, error) {
	id, err := parseID(r)
	if err != nil {
		return nil, nil, err
	}
	conn, _, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return nil, nil, err
	}
	if conn.Driver != want {
		return nil, nil, httpx.BadRequest("this endpoint is for %s; this connection is %s", label, conn.Driver)
	}
	pool, _, err := s.dbPool(r.Context(), id)
	if err != nil {
		return nil, nil, err
	}
	return pool, conn, nil
}

func (s *Server) handleClickHouseParts(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	pool, _, err := s.engineView(r, dbx.DriverClickHouse, "ClickHouse")
	if err != nil {
		return err
	}
	q := r.URL.Query()
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	parts, err := dbx.ClickHouseTableParts(ctx, pool, strings.TrimSpace(q.Get("database")),
		strings.TrimSpace(q.Get("table")), q.Get("inactive") == "1")
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	httpx.JSON(w, http.StatusOK, parts)
	return nil
}

func (s *Server) handleClickHouseMerges(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	pool, _, err := s.engineView(r, dbx.DriverClickHouse, "ClickHouse")
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	merges, err := dbx.ClickHouseMerges(ctx, pool)
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"merges": merges})
	return nil
}

func (s *Server) handleClickHouseMutations(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	pool, _, err := s.engineView(r, dbx.DriverClickHouse, "ClickHouse")
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	mutations, err := dbx.ClickHouseMutations(ctx, pool, strings.TrimSpace(r.URL.Query().Get("database")))
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"mutations": mutations})
	return nil
}

func (s *Server) handleClickHouseQueries(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	pool, _, err := s.engineView(r, dbx.DriverClickHouse, "ClickHouse")
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	queries, err := dbx.ClickHouseQueries(ctx, pool)
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"queries": queries})
	return nil
}

// handleSQLiteFile describes the database as a file. The path it reports is
// the one the connection already shows as its database; nothing here reads a
// path from the request.
func (s *Server) handleSQLiteFile(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	pool, _, err := s.engineView(r, dbx.DriverSQLite, "SQLite")
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	file, err := dbx.ReadSQLiteFile(ctx, pool)
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	httpx.JSON(w, http.StatusOK, file)
	return nil
}
