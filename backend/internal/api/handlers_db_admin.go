package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/mongo"
)

// The server behind a connection: its accounts, its databases, its
// extensions, its settings, what the advisor finds wrong with it, which
// statements cost it the most, and the dumps taken of it.
//
// Reading any of it is on the read surface — a list of role names, a list of
// parameters and a list of findings carry nothing an operator with read
// access to the data could not already learn from the catalogue. Making an
// account, granting it a database and enabling an extension are administrative
// acts, so they sit with creating a connection under system.admin. Dropping an
// account, an extension or a dump is destructive and sits there.

func (s *Server) mountDatabaseAdminRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/{id}/server/roles", s.handle(s.handleDBRoles))
	r.Method(http.MethodGet, "/{id}/server/extensions", s.handle(s.handleDBExtensions))
	r.Method(http.MethodGet, "/{id}/server/settings", s.handle(s.handleDBSettings))
	r.Method(http.MethodGet, "/{id}/advisor", s.handle(s.handleDBAdvisor))
	r.Method(http.MethodGet, "/{id}/statements", s.handle(s.handleDBStatements))
	r.Method(http.MethodGet, "/{id}/backups", s.handle(s.handleDBBackupList))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodPost, "/{id}/server/roles", s.handle(s.handleDBRoleCreate))
		r.Method(http.MethodPut, "/{id}/server/roles/{name}", s.handle(s.handleDBRoleAlter))
		r.Method(http.MethodPost, "/{id}/server/roles/{name}/grant", s.handle(s.handleDBRoleGrant))
		r.Method(http.MethodPost, "/{id}/server/databases", s.handle(s.handleDBDatabaseCreate))
		// A sibling connection to another database on the same server, with
		// the same credentials: the way from "I can see it in the list" to
		// "I can browse it" without typing a password that is already sealed.
		r.Method(http.MethodPost, "/{id}/server/databases/connect", s.handle(s.handleDBDatabaseConnect))
		r.Method(http.MethodPost, "/{id}/server/extensions", s.handle(s.handleDBExtensionCreate))
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodDelete, "/{id}/server/roles/{name}", s.handle(s.handleDBRoleDrop))
			r.Method(http.MethodDelete, "/{id}/server/extensions/{name}", s.handle(s.handleDBExtensionDrop))
			r.Method(http.MethodDelete, "/{id}/backups", s.handle(s.handleDBBackupDelete))
		})
	})
}

// dbAdmin resolves the connection and the engine's server surface, refusing
// the engines that have none with a sentence the page can print.
func (s *Server) dbAdmin(r *http.Request) (dbx.Admin, *dbConnection, string, error) {
	id, err := parseID(r)
	if err != nil {
		return nil, nil, "", err
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return nil, nil, "", err
	}
	if !conn.Driver.IsSQL() {
		return nil, conn, dsn, nil
	}
	admin, err := dbx.AdminFor(conn.Driver)
	if errors.Is(err, dbx.ErrUnsupported) {
		return nil, conn, dsn, httpx.Err(http.StatusBadRequest, "unsupported",
			fmt.Sprintf("%s has no server-level management from here", conn.Driver))
	}
	if err != nil {
		return nil, conn, dsn, httpx.Internal(err)
	}
	return admin, conn, dsn, nil
}

func (s *Server) handleDBRoles(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	admin, conn, dsn, err := s.dbAdmin(r)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	var roles []dbx.Role
	switch conn.Driver {
	case dbx.DriverMongo:
		client, err := dbx.MongoClient(ctx, dsn)
		if err != nil {
			return connectFailed(dsn, err)
		}
		defer client.Disconnect(context.Background())
		roles, err = dbx.MongoUsers(ctx, client)
		if err != nil {
			return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
		}
	case dbx.DriverRedis:
		client, err := dbx.RedisClient(ctx, dsn, 0)
		if err != nil {
			return connectFailed(dsn, err)
		}
		defer client.Close()
		roles, err = dbx.RedisUsers(ctx, client)
		if err != nil {
			httpx.JSON(w, http.StatusOK, map[string]any{"roles": []dbx.Role{}, "supported": false, "reason": err.Error()})
			return nil
		}
		roles = withoutACLSecrets(roles)
	default:
		pool, _, err := s.dbPool(ctx, conn.ID)
		if err != nil {
			return err
		}
		roles, err = admin.Roles(ctx, pool)
		if err != nil {
			return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"roles": roles, "supported": true})
	return nil
}

// withoutACLSecrets drops the password entries from the ACL rule a Redis
// account is listed with. ACL LIST prints each password's SHA-256 beside the
// rule ("#5e88…"), and this list is on the read surface: a hash of a password
// short enough to remember is as good as the password to anyone with a
// wordlist. The rule's shape — which keys, which commands — is what the page
// shows, and none of that is in the tokens removed here.
func withoutACLSecrets(roles []dbx.Role) []dbx.Role {
	for i := range roles {
		for j, rule := range roles[i].MemberOf {
			kept := []string{}
			for _, token := range strings.Fields(rule) {
				// "#hash" and ">password" add one, "!hash" and "<password"
				// take one away; all four carry the secret itself.
				if strings.HasPrefix(token, "#") || strings.HasPrefix(token, ">") ||
					strings.HasPrefix(token, "!") || strings.HasPrefix(token, "<") {
					continue
				}
				kept = append(kept, token)
			}
			roles[i].MemberOf[j] = strings.Join(kept, " ")
		}
	}
	return roles
}

// roleRequest is a create or an alter. Every attribute an alter can change is
// a pointer, because an alter changes exactly what was sent: a request that
// carries only a password must leave a superuser a superuser. With plain
// booleans a field that was absent and a field that was false were the same
// thing, and a password change read as "and clear every attribute".
type roleRequest struct {
	Name       string `json:"name"`
	Host       string `json:"host"`
	Password   string `json:"password"`
	Login      *bool  `json:"login"`
	Superuser  *bool  `json:"superuser"`
	CreateDB   *bool  `json:"createDb"`
	CreateRole *bool  `json:"createRole"`
	// ConnLimit is -1 for unlimited and 0 for "leave it".
	ConnLimit   int     `json:"connectionLimit"`
	Inherit     *bool   `json:"inherit"`
	Replication *bool   `json:"replication"`
	BypassRLS   *bool   `json:"bypassRls"`
	Locked      *bool   `json:"locked"`
	ValidUntil  *string `json:"validUntil"`
	// Database and Level, given on create, grant the new role that database
	// in the same request — the one-step "make an account for this app".
	Database string `json:"database"`
	Level    string `json:"level"`
	// Schema narrows that grant to one schema on PostgreSQL.
	Schema string `json:"schema"`
}

func (req roleRequest) spec(create bool) dbx.RoleSpec {
	flag := func(v *bool, def bool) bool {
		if v == nil {
			return def
		}
		return *v
	}
	return dbx.RoleSpec{
		Name: strings.TrimSpace(req.Name), Host: strings.TrimSpace(req.Host),
		Password: req.Password, SetPassword: req.Password != "" || create,
		// A new account signs in unless told otherwise; everything else is
		// off unless asked for. On an alter the defaults are never read: the
		// Set* fields say which of these the request carried.
		Login: flag(req.Login, true), Superuser: flag(req.Superuser, false),
		CreateDB: flag(req.CreateDB, false), CreateRole: flag(req.CreateRole, false),
		ConnLimit: req.ConnLimit,
		SetLogin:  req.Login != nil, SetSuperuser: req.Superuser != nil,
		SetCreateDB: req.CreateDB != nil, SetCreateRole: req.CreateRole != nil,
		Inherit: req.Inherit, Replication: req.Replication, BypassRLS: req.BypassRLS,
		Locked: req.Locked, ValidUntil: req.ValidUntil,
	}
}

// roleError renders what went wrong with an account operation: a request the
// engine's accounts cannot express is the caller's to fix, anything else is
// the server's answer.
func roleError(err error) error {
	var attr dbx.ErrRoleAttribute
	if errors.As(err, &attr) {
		return httpx.BadRequest("%v", err)
	}
	return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
}

func (s *Server) handleDBRoleCreate(w http.ResponseWriter, r *http.Request) error {
	var req roleRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if strings.TrimSpace(req.Name) == "" {
		return httpx.BadRequest("a name is required")
	}
	admin, conn, dsn, err := s.dbAdmin(r)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	spec := req.spec(true)
	// Before anything is made: an account created without part of what was
	// asked for is worse than none, because the answer would still be "done".
	if err := dbx.CheckRoleRequest(conn.Driver, spec, true); err != nil {
		return roleError(err)
	}
	if err := s.withRoleClient(ctx, conn, dsn, admin,
		func(a dbx.Admin, pool *sql.DB) error { return a.CreateRole(ctx, pool, spec) },
		func(c *mongo.Client) error { return dbx.MongoCreateUser(ctx, c, spec) },
		func(c *redis.Client) error { return dbx.RedisCreateUser(ctx, c, spec) },
	); err != nil {
		return roleError(err)
	}
	detail := map[string]any{"role": spec.Name, "superuser": spec.Superuser, "driver": conn.Driver}
	out := map[string]any{"ok": true}
	if req.Database != "" && req.Level != "" {
		grant := dbx.DatabaseGrant{Role: spec.Name, Host: spec.Host, Database: req.Database,
			Schema: strings.TrimSpace(req.Schema), Level: dbx.GrantLevel(req.Level)}
		granted, err := s.grantRole(ctx, conn, dsn, admin, grant)
		if err != nil {
			// The account exists; say what was not done rather than roll
			// back an act the operator may want to keep.
			httpx.SetAudit(r, "database.role.create", conn.Name, detail)
			return httpx.Err(http.StatusBadGateway, "grant_failed",
				fmt.Sprintf("%s was created but could not be granted %s: %v", spec.Name, req.Database, err))
		}
		detail["database"], detail["level"] = req.Database, req.Level
		grantOutcome(granted, detail, out)
	}
	httpx.SetAudit(r, "database.role.create", conn.Name, detail)
	httpx.JSON(w, http.StatusCreated, out)
	return nil
}

// grantOutcome copies what a database grant covered into the audit detail and
// the response. The schemas it left alone are in both: an operator reading
// either should not have to infer them from the statements.
func grantOutcome(granted *dbx.GrantResult, detail, out map[string]any) {
	detail["statements"], out["statements"] = granted.Statements, granted.Statements
	if len(granted.Schemas) > 0 {
		detail["schemas"], out["schemas"] = granted.Schemas, granted.Schemas
	}
	if len(granted.SkippedSchemas) > 0 {
		names := make([]string, len(granted.SkippedSchemas))
		for i, skipped := range granted.SkippedSchemas {
			names[i] = skipped.Name
		}
		detail["skippedSchemas"], out["skippedSchemas"] = names, granted.SkippedSchemas
	}
	if len(granted.Notes) > 0 {
		out["notes"] = granted.Notes
	}
}

func (s *Server) handleDBRoleAlter(w http.ResponseWriter, r *http.Request) error {
	var req roleRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	req.Name = httpx.URLParam(r, "name")
	if req.Host == "" {
		req.Host = r.URL.Query().Get("host")
	}
	admin, conn, dsn, err := s.dbAdmin(r)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	spec := req.spec(false)
	changes := spec.Changes()
	// Checked here, for every engine, before the engine is asked: Redis and
	// MongoDB read two fields of a request and ignore the rest, and an ignored
	// "locked" answered 200 is an operator believing an account is shut.
	if err := dbx.CheckRoleRequest(conn.Driver, spec, false); err != nil {
		return roleError(err)
	}
	if err := ownAccountRefusal(conn, spec); err != nil {
		return err
	}
	// An account is the server's: one a protected connection signs in with is
	// not changed through a neighbour either.
	if err := s.refuseNeighbourAccount(ctx, conn, spec.Name); err != nil {
		return err
	}
	if err := s.withRoleClient(ctx, conn, dsn, admin,
		func(a dbx.Admin, pool *sql.DB) error { return a.AlterRole(ctx, pool, spec) },
		func(c *mongo.Client) error { return dbx.MongoAlterUser(ctx, c, spec) },
		func(c *redis.Client) error { return dbx.RedisAlterUser(ctx, c, spec) },
	); err != nil {
		return roleError(err)
	}
	// The names of what changed, never the password itself.
	detail := map[string]any{"role": spec.Name, "password": spec.SetPassword, "superuser": spec.Superuser,
		"changed": changes, "driver": conn.Driver}
	out := map[string]any{"ok": true}
	if spec.SetPassword && strings.EqualFold(spec.Name, conn.User) {
		// The account this connection signs in with. Its saved password is
		// now wrong, and every page of this database would fail on the next
		// dial — so it is replaced, once the new one has been seen to work.
		updated := s.resealOwnPassword(ctx, conn, dsn, spec.Password)
		detail["connectionUpdated"], out["connectionUpdated"] = updated, updated
	}
	httpx.SetAudit(r, "database.role.alter", conn.Name, detail)
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// ownAccountRefusal refuses the changes that would cut the dashboard off from
// the server it is making them on: the account this connection signs in with
// may not be barred from signing in, nor demoted. The open pool would carry
// on for a while and then every page of this database would fail on its next
// dial, with nothing here able to undo it. Dropping that account is refused
// for the same reason; the name is compared the same way, without the host,
// because on MySQL which of a name's accounts the connection matched is the
// server's to decide and refusing one too many costs a trip to the console.
func ownAccountRefusal(conn *dbConnection, spec dbx.RoleSpec) error {
	if conn.User == "" || !strings.EqualFold(spec.Name, conn.User) {
		return nil
	}
	switch {
	case (spec.SetLogin && !spec.Login) || (spec.Locked != nil && *spec.Locked):
		return httpx.BadRequest("%s is the account this connection signs in with; it cannot be kept from signing in from here", spec.Name)
	case spec.SetSuperuser && !spec.Superuser:
		return httpx.BadRequest("%s is the account this connection signs in with; its administrator rights cannot be taken away from here", spec.Name)
	}
	return nil
}

// resealOwnPassword stores a connection's new password after its own account
// was changed from here. The new connection string is dialled first: with
// MySQL's user@host accounts the one that was altered need not be the one the
// connection matches, and a string that does not open is not saved over one
// that still might. It reports whether the saved connection was updated.
func (s *Server) resealOwnPassword(ctx context.Context, conn *dbConnection, dsn, password string) bool {
	if conn.Driver == dbx.DriverSQLite {
		return false
	}
	next, err := refreshedDatabasePassword(conn.Driver, dsn, password)
	if err != nil || next == dsn {
		return false
	}
	if err := s.probeConnection(ctx, conn.Driver, next); err != nil {
		return false
	}
	sealed, err := s.Sealer.Seal(next)
	if err != nil {
		return false
	}
	if _, err := s.Store.DB.ExecContext(ctx, `UPDATE db_connections SET dsn_enc = ? WHERE id = ?`, sealed, conn.ID); err != nil {
		return false
	}
	// The pool was opened with the old password; its idle connections still
	// work, and the next one it dials would not.
	s.modules.dbs.Close(conn.ID)
	return true
}

func (s *Server) handleDBRoleDrop(w http.ResponseWriter, r *http.Request) error {
	name := httpx.URLParam(r, "name")
	host := r.URL.Query().Get("host")
	admin, conn, dsn, err := s.dbAdmin(r)
	if err != nil {
		return err
	}
	if strings.EqualFold(name, conn.User) {
		return httpx.BadRequest("%s is the account this connection signs in with", name)
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	if err := s.refuseNeighbourAccount(ctx, conn, name); err != nil {
		return err
	}
	if err := s.withRoleClient(ctx, conn, dsn, admin,
		func(a dbx.Admin, pool *sql.DB) error { return a.DropRole(ctx, pool, name, host) },
		func(c *mongo.Client) error { return dbx.MongoDropUser(ctx, c, name) },
		func(c *redis.Client) error { return dbx.RedisDropUser(ctx, c, name) },
	); err != nil {
		return roleError(err)
	}
	httpx.SetAudit(r, "database.role.drop", conn.Name, map[string]any{"role": name, "host": host, "driver": conn.Driver})
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

type grantRequest struct {
	Host     string `json:"host"`
	Database string `json:"database"`
	Level    string `json:"level"`
	// Schema narrows the grant to one schema on PostgreSQL; empty covers
	// every schema of the database that is not the engine's own.
	Schema string `json:"schema"`
}

// handleDBRoleGrant hands a role a database at one of three levels.
//
// With ?preview=1 nothing runs: the statements come back with the schemas
// they would reach and the ones they would leave alone, which on PostgreSQL
// is the only way to know before the fact what "this database" will cover.
func (s *Server) handleDBRoleGrant(w http.ResponseWriter, r *http.Request) error {
	var req grantRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	name := httpx.URLParam(r, "name")
	level := dbx.GrantLevel(req.Level)
	if !level.Valid() {
		return httpx.BadRequest("level must be read, write or all")
	}
	admin, conn, dsn, err := s.dbAdmin(r)
	if err != nil {
		return err
	}
	database := strings.TrimSpace(req.Database)
	if database == "" {
		database = conn.Database
	}
	if database == "" {
		return httpx.BadRequest("a database is required")
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	preview := r.URL.Query().Get("preview") == "1"
	if preview {
		// Nothing changes, whether it answers or fails.
		httpx.SkipAudit(r)
	}
	grant := dbx.DatabaseGrant{Role: name, Host: req.Host, Database: database, Schema: strings.TrimSpace(req.Schema),
		Level: level, Preview: preview}
	granted, err := s.grantRole(ctx, conn, dsn, admin, grant)
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	detail := map[string]any{"role": name, "database": database, "schema": grant.Schema, "level": string(level),
		"driver": conn.Driver}
	out := map[string]any{}
	grantOutcome(granted, detail, out)
	if preview {
		out["preview"] = true
		httpx.JSON(w, http.StatusOK, out)
		return nil
	}
	out["ok"] = true
	httpx.SetAudit(r, "database.role.grant", conn.Name, detail)
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// grantRole hands a role a database, connected to that database where the
// engine grants from inside it, and returns what ran and what it covered. The
// sibling pool is opened for the request and closed with it rather than
// cached: it is one transaction, and a pool per database an operator ever
// granted would outlive its use.
func (s *Server) grantRole(ctx context.Context, conn *dbConnection, dsn string, admin dbx.Admin, grant dbx.DatabaseGrant) (*dbx.GrantResult, error) {
	switch conn.Driver {
	case dbx.DriverMongo:
		// A built-in role on the database: there is no statement to show.
		if grant.Preview {
			return &dbx.GrantResult{Statements: []string{}}, nil
		}
		client, err := dbx.MongoClient(ctx, dsn)
		if err != nil {
			return nil, withoutSecrets(dsn, err)
		}
		defer client.Disconnect(context.Background())
		return &dbx.GrantResult{Statements: []string{}}, dbx.MongoGrant(ctx, client, grant.Role, grant.Database, grant.Level)
	case dbx.DriverRedis:
		return nil, errors.New("Redis grants are ACL rules; edit the user's rule instead")
	}
	if admin.GrantNeedsDatabase() && grant.Database != conn.Database {
		db, err := dbx.OpenDatabase(ctx, conn.Driver, dsn, grant.Database)
		if err != nil {
			return nil, withoutSecrets(dsn, err)
		}
		defer db.Close()
		return admin.Grant(ctx, db, grant)
	}
	pool, err := s.modules.dbs.Pool(ctx, conn.ID, conn.Driver, dsn)
	if err != nil {
		return nil, withoutSecrets(dsn, err)
	}
	return admin.Grant(ctx, pool, grant)
}

// withRoleClient runs the SQL, Mongo or Redis form of one account operation,
// whichever the connection's engine has.
func (s *Server) withRoleClient(ctx context.Context, conn *dbConnection, dsn string, admin dbx.Admin,
	sqlOp func(dbx.Admin, *sql.DB) error, mongoOp func(*mongo.Client) error, redisOp func(*redis.Client) error) error {
	switch conn.Driver {
	case dbx.DriverMongo:
		client, err := dbx.MongoClient(ctx, dsn)
		if err != nil {
			return withoutSecrets(dsn, err)
		}
		defer client.Disconnect(context.Background())
		return mongoOp(client)
	case dbx.DriverRedis:
		client, err := dbx.RedisClient(ctx, dsn, 0)
		if err != nil {
			return withoutSecrets(dsn, err)
		}
		defer client.Close()
		return redisOp(client)
	}
	pool, err := s.modules.dbs.Pool(ctx, conn.ID, conn.Driver, dsn)
	if err != nil {
		return withoutSecrets(dsn, err)
	}
	return sqlOp(admin, pool)
}

type createDatabaseRequest struct {
	Name  string `json:"name"`
	Owner string `json:"owner"`
	// Connect saves a sibling connection to the new database straight away.
	Connect bool `json:"connect"`
}

func (s *Server) handleDBDatabaseCreate(w http.ResponseWriter, r *http.Request) error {
	var req createDatabaseRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	name := strings.TrimSpace(req.Name)
	if !dbNameRe.MatchString(name) {
		return httpx.BadRequest("a database name is letters, digits and underscores")
	}
	admin, conn, dsn, err := s.dbAdmin(r)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	switch conn.Driver {
	case dbx.DriverMongo:
		// A Mongo database exists once something is in it; an empty
		// collection is the smallest something.
		client, err := dbx.MongoClient(ctx, dsn)
		if err != nil {
			return connectFailed(dsn, err)
		}
		defer client.Disconnect(context.Background())
		if err := dbx.MongoCreateCollection(ctx, client, name, "_init"); err != nil {
			return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
		}
	case dbx.DriverRedis:
		return httpx.BadRequest("Redis numbers its keyspaces itself; there is nothing to create")
	default:
		pool, _, err := s.dbPool(ctx, conn.ID)
		if err != nil {
			return err
		}
		if err := admin.CreateDatabase(ctx, pool, name, strings.TrimSpace(req.Owner)); err != nil {
			return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
		}
	}
	httpx.SetAudit(r, "database.create", conn.Name, map[string]any{"database": name, "owner": req.Owner, "driver": conn.Driver})
	if !req.Connect {
		httpx.JSON(w, http.StatusCreated, map[string]any{"ok": true})
		return nil
	}
	return s.saveSiblingConnection(w, r, conn, dsn, name)
}

type connectDatabaseRequest struct {
	Database string `json:"database"`
}

func (s *Server) handleDBDatabaseConnect(w http.ResponseWriter, r *http.Request) error {
	var req connectDatabaseRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	name := strings.TrimSpace(req.Database)
	if !dbNameRe.MatchString(name) {
		return httpx.BadRequest("a database name is letters, digits and underscores")
	}
	_, conn, dsn, err := s.dbAdmin(r)
	if err != nil {
		return err
	}
	return s.saveSiblingConnection(w, r, conn, dsn, name)
}

// saveSiblingConnection stores a connection to another database on the same
// server, under the same credentials, named after both.
func (s *Server) saveSiblingConnection(w http.ResponseWriter, r *http.Request, conn *dbConnection, dsn, database string) error {
	if conn.Driver == dbx.DriverSQLite || conn.Driver == dbx.DriverOracle {
		return httpx.BadRequest("%s has one database per connection", conn.Driver)
	}
	sibling := dbx.DSNForDatabase(conn.Driver, dsn, database)
	if sibling == dsn && database != conn.Database {
		return httpx.BadRequest("this engine's connection string cannot name another database")
	}
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	if err := s.probeConnection(ctx, conn.Driver, sibling); err != nil {
		return httpx.BadRequest("%v", err)
	}
	existing, err := s.existingConnections(ctx)
	if err != nil {
		return err
	}
	for _, have := range existing {
		if have.Driver == conn.Driver && have.Host == conn.Host && have.Port == conn.Port && have.Database == database {
			httpx.SkipAudit(r)
			httpx.JSON(w, http.StatusOK, have)
			return nil
		}
	}
	names := map[string]string{}
	for _, have := range existing {
		names[have.Name] = have.Name
	}
	name := uniqueConnectionName(siblingConnectionName(conn.Name, database), names)
	return s.saveConnection(w, r, name, conn.Driver, sibling, "database.connection.sibling",
		map[string]any{"from": conn.Name, "database": database})
}

// existingConnections is every saved connection with its address decoded, for
// the checks that need more than the host:port map existingDSNs keeps.
func (s *Server) existingConnections(ctx context.Context) ([]*dbConnection, error) {
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT id FROM db_connections`)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, httpx.Internal(err)
		}
		ids = append(ids, id)
	}
	out := []*dbConnection{}
	for _, id := range ids {
		conn, _, err := s.dbConnRow(ctx, id)
		if err != nil {
			continue
		}
		out = append(out, conn)
	}
	return out, nil
}

func (s *Server) handleDBExtensions(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	admin, conn, _, err := s.dbAdmin(r)
	if err != nil {
		return err
	}
	if admin == nil {
		httpx.JSON(w, http.StatusOK, map[string]any{"extensions": []dbx.Extension{}, "supported": false,
			"reason": "this engine has no extension catalogue"})
		return nil
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	pool, _, err := s.dbPool(ctx, conn.ID)
	if err != nil {
		return err
	}
	list, err := admin.Extensions(ctx, pool)
	if errors.Is(err, dbx.ErrUnsupported) {
		httpx.JSON(w, http.StatusOK, map[string]any{"extensions": []dbx.Extension{}, "supported": false,
			"reason": "this engine has no extension catalogue"})
		return nil
	}
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	// Whether they can be changed from here: Postgres yes, MySQL's plugins no.
	httpx.JSON(w, http.StatusOK, map[string]any{"extensions": list, "supported": true,
		"editable": conn.Driver == dbx.DriverPostgres})
	return nil
}

type extensionRequest struct {
	Name string `json:"name"`
}

func (s *Server) handleDBExtensionCreate(w http.ResponseWriter, r *http.Request) error {
	var req extensionRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	admin, conn, _, err := s.dbAdmin(r)
	if err != nil {
		return err
	}
	if admin == nil {
		return httpx.BadRequest("%s has no extensions", conn.Driver)
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	pool, _, err := s.dbPool(ctx, conn.ID)
	if err != nil {
		return err
	}
	if err := admin.CreateExtension(ctx, pool, strings.TrimSpace(req.Name)); err != nil {
		if errors.Is(err, dbx.ErrUnsupported) {
			return httpx.BadRequest("%s extensions are installed from the server's own configuration", conn.Driver)
		}
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	httpx.SetAudit(r, "database.extension.create", conn.Name, map[string]any{"extension": req.Name})
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (s *Server) handleDBExtensionDrop(w http.ResponseWriter, r *http.Request) error {
	name := httpx.URLParam(r, "name")
	admin, conn, _, err := s.dbAdmin(r)
	if err != nil {
		return err
	}
	if admin == nil {
		return httpx.BadRequest("%s has no extensions", conn.Driver)
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	pool, _, err := s.dbPool(ctx, conn.ID)
	if err != nil {
		return err
	}
	if err := admin.DropExtension(ctx, pool, name); err != nil {
		if errors.Is(err, dbx.ErrUnsupported) {
			return httpx.BadRequest("%s extensions are removed from the server's own configuration", conn.Driver)
		}
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	httpx.SetAudit(r, "database.extension.drop", conn.Name, map[string]any{"extension": name})
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (s *Server) handleDBSettings(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	admin, conn, dsn, err := s.dbAdmin(r)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	// The two non-SQL engines report their server through the stats route
	// already; here they hand back the same facts as settings so the page
	// draws one list.
	switch conn.Driver {
	case dbx.DriverMongo:
		client, err := dbx.MongoClient(ctx, dsn)
		if err != nil {
			return connectFailed(dsn, err)
		}
		defer client.Disconnect(context.Background())
		status, err := dbx.MongoServerStatus(ctx, client)
		if err != nil {
			return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"settings": flattenSettings(status), "supported": true})
		return nil
	case dbx.DriverRedis:
		client, err := dbx.RedisClient(ctx, dsn, 0)
		if err != nil {
			return connectFailed(dsn, err)
		}
		defer client.Close()
		info, err := dbx.RedisInfo(ctx, client)
		if err != nil {
			return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"settings": flattenSettings(info), "supported": true})
		return nil
	}
	pool, _, err := s.dbPool(ctx, conn.ID)
	if err != nil {
		return err
	}
	list, err := admin.Settings(ctx, pool)
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"settings": list, "supported": true})
	return nil
}

// flattenSettings turns a nested status document into name/value rows, one
// level deep, in a stable order.
func flattenSettings(doc map[string]any) []dbx.Setting {
	out := []dbx.Setting{}
	keys := make([]string, 0, len(doc))
	for k := range doc {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		switch v := doc[k].(type) {
		case map[string]any:
			sub := make([]string, 0, len(v))
			for sk := range v {
				sub = append(sub, sk)
			}
			sort.Strings(sub)
			for _, sk := range sub {
				out = append(out, dbx.Setting{Name: k + "." + sk, Value: stringify(v[sk]), Category: k})
			}
		default:
			out = append(out, dbx.Setting{Name: k, Value: stringify(v)})
		}
	}
	return out
}

func stringify(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return fmt.Sprint(x)
		}
		return strings.Trim(string(b), `"`)
	}
}

func (s *Server) handleDBAdvisor(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	id, err := parseID(r)
	if err != nil {
		return err
	}
	pool, conn, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 120*time.Second)
	defer cancel()
	report, err := dbx.Advise(ctx, pool, conn.Driver, r.URL.Query().Get("schema"))
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	// The findings no catalogue can make: where the server is reachable
	// from, whether anything would bring it back, and what its container is
	// allowed to take. They are read here, beside the engine's own, so the
	// page is one list.
	panel, silences := s.panelAdvice(ctx, r, conn)
	report.Findings = append(panel, report.Findings...)
	report.Silences = append(report.Silences, silences...)
	dbx.SortAdvice(report.Findings)
	// A finding names the page its fix is made on by that page's own name;
	// which connection it belongs to is only known here.
	for i := range report.Findings {
		if link := report.Findings[i].Link; link != "" {
			report.Findings[i].Link = fmt.Sprintf("/databases/%d/%s", id, link)
		}
	}
	httpx.JSON(w, http.StatusOK, report)
	return nil
}

// dbBackupStaleAfter is how old the newest dump may be before the advisor
// says so. A week: long enough that a weekly schedule never trips it, short
// enough that a schedule which stopped is noticed before the dump is needed.
const dbBackupStaleAfter = 7 * 24 * time.Hour

// panelAdvice is what this dashboard knows about a database that the
// database does not: its dumps, its published port, its container's limits.
//
// The dumps are read by anyone who may read the backups list. The port and
// the container are read through the same calls as the access page, which
// lists containers and the firewall and is an administrator's; for everyone
// else those two are named as not assessed rather than left out, so their
// absence does not read as a clean result.
func (s *Server) panelAdvice(ctx context.Context, r *http.Request, conn *dbConnection) ([]dbx.Advice, []string) {
	out := []dbx.Advice{}
	silences := []string{}

	database := []dbx.AdviceTarget{{Kind: "database", Name: conn.Name}}
	switch newest := s.newestDump(conn.Name); {
	case newest == nil:
		out = append(out, dbx.Advice{
			ID: "no-backup", Level: "warning", Category: dbx.AdviceReliability,
			Title:   "No backup of this database has been taken from here",
			Detail:  "Nothing in this dashboard's backup directory would bring the database back after a bad migration, a dropped table or a lost disk. A backup taken by some other tool is not seen here.",
			Advice:  "Take one now, and schedule them under Backups.",
			Targets: database, Link: "backups",
		})
	case time.Since(*newest) > dbBackupStaleAfter:
		days := int(time.Since(*newest).Hours() / 24)
		out = append(out, dbx.Advice{
			ID: "stale-backup", Level: "warning", Category: dbx.AdviceReliability,
			Title:   fmt.Sprintf("The newest backup is %d days old", days),
			Detail:  "Restoring it would lose everything written since. A schedule that stopped running looks exactly like this.",
			Advice:  "Take a backup now, and check the schedule under Backups is still running.",
			Targets: database, Link: "backups",
		})
	}

	if p := httpx.MustPrincipal(r); !p.Can(auth.CapSystemAdmin) {
		silences = append(silences, "Where the server is reachable from, and its container's limits, are assessed for an administrator only.")
		return out, silences
	}
	_, dsn, err := s.dbConnRow(ctx, conn.ID)
	if err != nil {
		return out, silences
	}
	access := s.describeDBAccess(ctx, conn, dsn)
	if access.Exposure == exposurePublic && (!access.Firewall.Active || access.Firewall.Open) {
		out = append(out, dbx.Advice{
			ID: "published-everywhere", Level: "warning", Category: dbx.AdviceSecurity,
			Title:   "The server is reachable from the internet",
			Detail:  "Its port is published on every interface and the firewall lets it through, so the only thing between the internet and the data is the password.",
			Advice:  "Keep it if applications elsewhere need it and the passwords are strong; otherwise switch it to this server only under Settings.",
			Targets: []dbx.AdviceTarget{{Kind: "server", Name: fmt.Sprintf("port %d", access.Port)}}, Link: "settings",
		})
	}
	if access.Container != "" && s.modules.docker != nil {
		// The container's own ceiling. Without one a database's cache grows
		// until the kernel has to kill something, and what it kills is
		// whichever process is largest — which may not be the database.
		if spec, err := s.modules.docker.SpecOf(ctx, access.Container); err != nil {
			silences = append(silences, "The container's limits could not be read: "+err.Error())
		} else if spec.Limits.MemoryMB == 0 {
			out = append(out, dbx.Advice{
				ID: "container-no-memory-limit", Level: "notice", Category: dbx.AdviceReliability,
				Title:   "The container has no memory limit",
				Detail:  "The server may take as much memory as the machine has. When the machine runs out the kernel kills a process to get some back, and it chooses by size, not by which one grew.",
				Advice:  "Give the container a memory limit a little above what the database is configured to use, so it is the one stopped and restarted rather than its neighbours.",
				Targets: []dbx.AdviceTarget{{Kind: "container", Name: access.Container}},
			})
		}
	}
	return out, silences
}

// handleDBStatements lists the statements that cost the server most. sort
// and limit choose which and how many; both are closed, because the sort
// becomes a column name.
func (s *Server) handleDBStatements(w http.ResponseWriter, r *http.Request) error {
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
	report, err := dbx.TopStatements(ctx, pool, conn.Driver,
		dbx.StatementsOptions{Sort: strings.TrimSpace(q.Get("sort")), Limit: atoiDefault(q.Get("limit"), 0)})
	var bad dbx.ErrBadStatementsOption
	if errors.As(err, &bad) {
		return httpx.BadRequest("%v", err)
	}
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	httpx.JSON(w, http.StatusOK, report)
	return nil
}

// --- the dumps on disk -------------------------------------------------------

type dbBackupFile struct {
	File    string    `json:"file"`
	Size    int64     `json:"size"`
	TakenAt time.Time `json:"takenAt"`
	// Format is what the file is, read from its first bytes: a pg_dump
	// archive, SQL text, a gzipped JSON Lines document set.
	Format string `json:"format"`
	// The rest is what the dump's description says, where it has one. A file
	// put in the directory by hand has none of it.
	DurationMs  *int64            `json:"durationMs,omitempty"`
	Database    string            `json:"database,omitempty"`
	Tool        string            `json:"tool,omitempty"`
	ToolVersion string            `json:"toolVersion,omitempty"`
	Summary     string            `json:"summary,omitempty"`
	Contents    *dbx.DumpContents `json:"contents,omitempty"`
	Note        string            `json:"note,omitempty"`
	Origin      string            `json:"origin,omitempty"`
	By          string            `json:"by,omitempty"`
}

func (s *Server) handleDBBackupList(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	id, err := parseID(r)
	if err != nil {
		return err
	}
	conn, _, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	dir := s.dbDumpDir(conn.Name)
	out, err := readDirSorted(dir)
	if err != nil {
		return httpx.Internal(err)
	}
	answer := map[string]any{"dir": dir, "files": out, "options": dbx.DumpCapabilities(conn.Driver)}
	if job, running := s.runningTransfer(conn.ID); running {
		answer["job"] = job
	}
	httpx.JSON(w, http.StatusOK, answer)
	return nil
}

// readDirSorted lists a connection's dumps, newest first; a directory that
// does not exist yet is an empty list, not an error.
func readDirSorted(dir string) ([]dbBackupFile, error) {
	entries, err := dumpEntries(dir)
	if err != nil {
		return nil, err
	}
	out := make([]dbBackupFile, 0, len(entries))
	for _, info := range entries {
		out = append(out, describeDump(dir, info.Name(), info))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TakenAt.After(out[j].TakenAt) })
	return out, nil
}

type deleteBackupRequest struct {
	File string `json:"file"`
}

func (s *Server) handleDBBackupDelete(w http.ResponseWriter, r *http.Request) error {
	var req deleteBackupRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	id, err := parseID(r)
	if err != nil {
		return err
	}
	conn, _, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	// Contained against the connection's own dump directory, exactly as the
	// download is, so a name cannot walk out of it.
	path, err := s.resolveDumpName(conn.Name, req.File)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return mapFileError(err)
	}
	// Its description goes with it. One left behind describes nothing.
	_ = dbx.RemoveDumpMeta(path)
	httpx.SetAudit(r, "database.backup.delete", conn.Name, map[string]any{"file": req.File})
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}
