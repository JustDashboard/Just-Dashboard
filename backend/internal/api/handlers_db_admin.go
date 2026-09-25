package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
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
			return httpx.Err(http.StatusBadGateway, "connect_failed", err.Error())
		}
		defer client.Disconnect(context.Background())
		roles, err = dbx.MongoUsers(ctx, client)
		if err != nil {
			return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
		}
	case dbx.DriverRedis:
		client, err := dbx.RedisClient(ctx, dsn, 0)
		if err != nil {
			return httpx.Err(http.StatusBadGateway, "connect_failed", err.Error())
		}
		defer client.Close()
		roles, err = dbx.RedisUsers(ctx, client)
		if err != nil {
			httpx.JSON(w, http.StatusOK, map[string]any{"roles": []dbx.Role{}, "supported": false, "reason": err.Error()})
			return nil
		}
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

type roleRequest struct {
	Name       string `json:"name"`
	Host       string `json:"host"`
	Password   string `json:"password"`
	Login      *bool  `json:"login"`
	Superuser  bool   `json:"superuser"`
	CreateDB   bool   `json:"createDb"`
	CreateRole bool   `json:"createRole"`
	ConnLimit  int    `json:"connectionLimit"`
	// Database and Level, given on create, grant the new role that database
	// in the same request — the one-step "make an account for this app".
	Database string `json:"database"`
	Level    string `json:"level"`
}

func (req roleRequest) spec(create bool) dbx.RoleSpec {
	login := true
	if req.Login != nil {
		login = *req.Login
	}
	return dbx.RoleSpec{
		Name: strings.TrimSpace(req.Name), Host: strings.TrimSpace(req.Host),
		Password: req.Password, SetPassword: req.Password != "" || create,
		Login: login, Superuser: req.Superuser, CreateDB: req.CreateDB,
		CreateRole: req.CreateRole, ConnLimit: req.ConnLimit,
	}
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
	if err := s.withRoleClient(ctx, conn, dsn, admin,
		func(a dbx.Admin, pool *sql.DB) error { return a.CreateRole(ctx, pool, spec) },
		func(c *mongo.Client) error { return dbx.MongoCreateUser(ctx, c, spec) },
		func(c *redis.Client) error { return dbx.RedisCreateUser(ctx, c, spec) },
	); err != nil {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	detail := map[string]any{"role": spec.Name, "superuser": spec.Superuser, "driver": conn.Driver}
	if req.Database != "" && req.Level != "" {
		if err := s.grantRole(ctx, conn, dsn, admin, spec.Name, spec.Host, req.Database, dbx.GrantLevel(req.Level)); err != nil {
			// The account exists; say what was not done rather than roll
			// back an act the operator may want to keep.
			httpx.SetAudit(r, "database.role.create", conn.Name, detail)
			return httpx.Err(http.StatusBadGateway, "grant_failed",
				fmt.Sprintf("%s was created but could not be granted %s: %v", spec.Name, req.Database, err))
		}
		detail["database"], detail["level"] = req.Database, req.Level
	}
	httpx.SetAudit(r, "database.role.create", conn.Name, detail)
	httpx.JSON(w, http.StatusCreated, map[string]any{"ok": true})
	return nil
}

func (s *Server) handleDBRoleAlter(w http.ResponseWriter, r *http.Request) error {
	var req roleRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	req.Name = chi.URLParam(r, "name")
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
	if err := s.withRoleClient(ctx, conn, dsn, admin,
		func(a dbx.Admin, pool *sql.DB) error { return a.AlterRole(ctx, pool, spec) },
		func(c *mongo.Client) error { return dbx.MongoAlterUser(ctx, c, spec) },
		func(c *redis.Client) error { return dbx.RedisAlterUser(ctx, c, spec) },
	); err != nil {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	httpx.SetAudit(r, "database.role.alter", conn.Name,
		map[string]any{"role": spec.Name, "password": spec.SetPassword, "superuser": spec.Superuser, "driver": conn.Driver})
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (s *Server) handleDBRoleDrop(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
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
	if err := s.withRoleClient(ctx, conn, dsn, admin,
		func(a dbx.Admin, pool *sql.DB) error { return a.DropRole(ctx, pool, name, host) },
		func(c *mongo.Client) error { return dbx.MongoDropUser(ctx, c, name) },
		func(c *redis.Client) error { return dbx.RedisDropUser(ctx, c, name) },
	); err != nil {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	httpx.SetAudit(r, "database.role.drop", conn.Name, map[string]any{"role": name, "host": host, "driver": conn.Driver})
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

type grantRequest struct {
	Host     string `json:"host"`
	Database string `json:"database"`
	Level    string `json:"level"`
}

func (s *Server) handleDBRoleGrant(w http.ResponseWriter, r *http.Request) error {
	var req grantRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	name := chi.URLParam(r, "name")
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
	if err := s.grantRole(ctx, conn, dsn, admin, name, req.Host, database, level); err != nil {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	httpx.SetAudit(r, "database.role.grant", conn.Name,
		map[string]any{"role": name, "database": database, "level": string(level), "driver": conn.Driver})
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

// grantRole hands a role a database, connected to that database where the
// engine grants from inside it. The sibling pool is opened for the request
// and closed with it rather than cached: it is one statement, and a pool per
// database an operator ever granted would outlive its use.
func (s *Server) grantRole(ctx context.Context, conn *dbConnection, dsn string, admin dbx.Admin,
	role, host, database string, level dbx.GrantLevel) error {
	switch conn.Driver {
	case dbx.DriverMongo:
		client, err := dbx.MongoClient(ctx, dsn)
		if err != nil {
			return err
		}
		defer client.Disconnect(context.Background())
		return dbx.MongoGrant(ctx, client, role, database, level)
	case dbx.DriverRedis:
		return errors.New("Redis grants are ACL rules; edit the user's rule instead")
	}
	if admin.GrantNeedsDatabase() && database != conn.Database {
		db, err := dbx.OpenDatabase(ctx, conn.Driver, dsn, database)
		if err != nil {
			return err
		}
		defer db.Close()
		return admin.Grant(ctx, db, role, host, database, level)
	}
	pool, err := s.modules.dbs.Pool(ctx, conn.ID, conn.Driver, dsn)
	if err != nil {
		return err
	}
	return admin.Grant(ctx, pool, role, host, database, level)
}

// withRoleClient runs the SQL, Mongo or Redis form of one account operation,
// whichever the connection's engine has.
func (s *Server) withRoleClient(ctx context.Context, conn *dbConnection, dsn string, admin dbx.Admin,
	sqlOp func(dbx.Admin, *sql.DB) error, mongoOp func(*mongo.Client) error, redisOp func(*redis.Client) error) error {
	switch conn.Driver {
	case dbx.DriverMongo:
		client, err := dbx.MongoClient(ctx, dsn)
		if err != nil {
			return err
		}
		defer client.Disconnect(context.Background())
		return mongoOp(client)
	case dbx.DriverRedis:
		client, err := dbx.RedisClient(ctx, dsn, 0)
		if err != nil {
			return err
		}
		defer client.Close()
		return redisOp(client)
	}
	pool, err := s.modules.dbs.Pool(ctx, conn.ID, conn.Driver, dsn)
	if err != nil {
		return err
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
			return httpx.Err(http.StatusBadGateway, "connect_failed", err.Error())
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
	name := uniqueConnectionName(conn.Name+" · "+database, names)
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
	name := chi.URLParam(r, "name")
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
			return httpx.Err(http.StatusBadGateway, "connect_failed", err.Error())
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
			return httpx.Err(http.StatusBadGateway, "connect_failed", err.Error())
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
	// The one finding no catalogue can make: where the server is reachable
	// from. It is read here, beside the engine's own, so the page is one list.
	if p := httpx.MustPrincipal(r); p.Can(auth.CapSystemAdmin) {
		_, dsn, err := s.dbConnRow(ctx, id)
		if err == nil {
			access := s.describeDBAccess(ctx, conn, dsn)
			if access.Exposure == exposurePublic && (!access.Firewall.Active || access.Firewall.Open) {
				report.Findings = append([]dbx.Advice{{
					ID: "published-everywhere", Level: "warning", Category: "security",
					Title:  "The server is reachable from the internet",
					Detail: "Its port is published on every interface and the firewall lets it through, so the only thing between the internet and the data is the password.",
					Advice: "Keep it if applications elsewhere need it and the passwords are strong; otherwise switch it to this server only under Connection.",
				}}, report.Findings...)
			}
		}
	}
	httpx.JSON(w, http.StatusOK, report)
	return nil
}

func (s *Server) handleDBStatements(w http.ResponseWriter, r *http.Request) error {
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
	report, err := dbx.TopStatements(ctx, pool, conn.Driver, atoiDefault(r.URL.Query().Get("limit"), 25))
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
	// Format is what the file's name says it is: a pg_dump archive, SQL text,
	// a gzipped JSON Lines document set.
	Format string `json:"format"`
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
	httpx.JSON(w, http.StatusOK, map[string]any{"dir": dir, "files": out})
	return nil
}

// readDirSorted lists a connection's dumps, newest first; a directory that
// does not exist yet is an empty list, not an error.
func readDirSorted(dir string) ([]dbBackupFile, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []dbBackupFile{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []dbBackupFile{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, dbBackupFile{
			File: e.Name(), Size: info.Size(), TakenAt: info.ModTime().UTC(), Format: dumpFormat(e.Name()),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TakenAt.After(out[j].TakenAt) })
	return out, nil
}

func dumpFormat(name string) string {
	switch {
	case strings.HasSuffix(name, ".dump"), strings.HasSuffix(name, ".pgdump"):
		return "pg_dump archive"
	case strings.HasSuffix(name, ".sql.gz"):
		return "compressed SQL"
	case strings.HasSuffix(name, ".sql"):
		return "SQL"
	case strings.HasSuffix(name, ".jsonl.gz"):
		return "JSON Lines"
	case strings.HasSuffix(name, ".archive"), strings.HasSuffix(name, ".gz"):
		return "archive"
	case strings.HasSuffix(name, ".db"), strings.HasSuffix(name, ".sqlite"):
		return "SQLite file"
	}
	return filepath.Ext(name)
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
	if req.File == "" {
		return httpx.BadRequest("file is required")
	}
	// Contained against the connection's own dump directory, exactly as the
	// download is, so a name cannot walk out of it.
	dir := s.dbDumpDir(conn.Name)
	fs := files.New([]string{dir})
	path, err := fs.Resolve(filepath.Join(dir, req.File))
	if err != nil {
		return mapFileError(err)
	}
	if err := os.Remove(path); err != nil {
		return mapFileError(err)
	}
	httpx.SetAudit(r, "database.backup.delete", conn.Name, map[string]any{"file": req.File})
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}
