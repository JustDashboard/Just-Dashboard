package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// dbConnection is the client-facing view. The DSN is never included: it holds
// credentials, and the dashboard has no reason to hand them back out.
type dbConnection struct {
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	Driver    dbx.Driver `json:"driver"`
	Host      string     `json:"host"`
	Port      string     `json:"port"`
	User      string     `json:"user"`
	Database  string     `json:"database"`
	CreatedAt time.Time  `json:"createdAt"`
}

func (s *Server) mountDatabaseRoutes(r chi.Router) {
	r.Route("/databases", func(r chi.Router) {
		r.Method(http.MethodGet, "/", s.handle(s.handleDBConnList))
		r.Method(http.MethodGet, "/drivers", s.handle(s.handleDBDrivers))
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
			r.Method(http.MethodPost, "/", s.handle(s.handleDBConnCreate))
			r.Method(http.MethodPost, "/test", s.handle(s.handleDBConnTest))
			// Detecting a server reads container environment, and adopting one
			// reads the credentials out of it, so both sit with creating a
			// connection by hand rather than on the read surface beside them.
			r.Method(http.MethodGet, "/detected", s.handle(s.handleDBDetected))
			r.Method(http.MethodPost, "/adopt", s.handle(s.handleDBAdopt))
			// The same act for a server that is not in a container: everything
			// but the password is already known, so the request is the
			// password and nothing else is typed.
			r.Method(http.MethodPost, "/host", s.handle(s.handleDBConnectHost))
			r.Method(http.MethodPost, "/sync", s.handle(s.handleDBSync))
			r.Method(http.MethodGet, "/provision/options", s.handle(s.handleDBProvisionOptions))
			r.Method(http.MethodPost, "/provision", s.handle(s.handleDBProvision))
			r.Method(http.MethodPut, "/{id}", s.handle(s.handleDBConnUpdate))
			r.Method(http.MethodGet, "/{id}/url", s.handle(s.handleDBConnURL))
			s.destructive(r, func(r chi.Router) {
				r.Method(http.MethodDelete, "/{id}", s.handle(s.handleDBConnDelete))
			})
		})
		// Read surface: available to any authenticated role, including readonly.
		r.Method(http.MethodGet, "/{id}/ping", s.handle(s.handleDBPing))
		r.Method(http.MethodGet, "/{id}/stats", s.handle(s.handleDBStats))
		r.Method(http.MethodGet, "/{id}/schemas", s.handle(s.handleDBList))
		r.Method(http.MethodGet, "/{id}/tables", s.handle(s.handleDBTables))
		r.Method(http.MethodGet, "/{id}/columns", s.handle(s.handleDBColumns))
		r.Method(http.MethodGet, "/{id}/table", s.handle(s.handleDBTableDetail))
		r.Method(http.MethodGet, "/{id}/browse", s.handle(s.handleDBBrowse))
		r.Method(http.MethodGet, "/{id}/export", s.handle(s.handleDBExport))
		r.Method(http.MethodPost, "/{id}/classify", s.handle(s.handleDBClassify))
		r.Method(http.MethodPost, "/{id}/explain", s.handle(s.handleDBExplain))
		r.Method(http.MethodPost, "/{id}/orm", s.handle(s.handleDBGenerateORM))
		r.Method(http.MethodGet, "/{id}/queries", s.handle(s.handleDBSavedList))
		r.Method(http.MethodGet, "/{id}/history", s.handle(s.handleDBHistory))
		r.Method(http.MethodGet, "/{id}/count", s.handle(s.handleDBCount))
		r.Method(http.MethodGet, "/{id}/outline", s.handle(s.handleDBOutline))
		r.Method(http.MethodGet, "/{id}/relations", s.handle(s.handleDBRelations))
		r.Method(http.MethodGet, "/{id}/graph", s.handle(s.handleDBGraph))
		r.Method(http.MethodGet, "/{id}/activity", s.handle(s.handleDBActivity))
		r.Method(http.MethodGet, "/{id}/search", s.handle(s.handleDBSearch))
		r.Method(http.MethodGet, "/{id}/overview", s.handle(s.handleDBOverview))
		r.Method(http.MethodGet, "/orm/targets", s.handle(s.handleDBTargets))
		r.Method(http.MethodPost, "/{id}/rows/sql", s.handle(s.handleDBRowSQL))
		// Redis and Mongo reads. They are separate paths rather than a
		// pretence that a key or a document is a row: the vocabulary is part of
		// what makes each engine legible.
		r.Method(http.MethodGet, "/{id}/keys", s.handle(s.handleRedisScan))
		r.Method(http.MethodGet, "/{id}/keys/value", s.handle(s.handleRedisGet))
		r.Method(http.MethodGet, "/{id}/collections/indexes", s.handle(s.handleMongoIndexes))
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapServiceControl))
			r.Method(http.MethodPost, "/{id}/query", s.handle(s.handleDBQuery))
			r.Method(http.MethodPost, "/{id}/backup", s.handle(s.handleDBBackup))
			// A dump the operator can take away. GET rather than POST because
			// it is a read of a file the dashboard already wrote, and because
			// a browser download has to be a navigable URL.
			r.Method(http.MethodGet, "/{id}/backup/download", s.handle(s.handleDBBackupDownload))
			r.Method(http.MethodPost, "/{id}/rows", s.handle(s.handleDBRowInsert))
			r.Method(http.MethodPatch, "/{id}/rows", s.handle(s.handleDBRowUpdate))
			r.Method(http.MethodPost, "/{id}/queries", s.handle(s.handleDBSavedCreate))
			r.Method(http.MethodDelete, "/{id}/queries/{qid}", s.handle(s.handleDBSavedDelete))
			r.Method(http.MethodPost, "/{id}/import", s.handle(s.handleDBImport))
			// Schema changes that only add: the same capability the query
			// runner needs for the CREATE it classifies as medium risk, so the
			// form and the SQL console agree on who may do this.
			r.Method(http.MethodPost, "/{id}/ddl/table", s.handle(s.handleDDLCreateTable))
			r.Method(http.MethodPost, "/{id}/ddl/column", s.handle(s.handleDDLAddColumn))
			r.Method(http.MethodPost, "/{id}/ddl/index", s.handle(s.handleDDLCreateIndex))
			r.Method(http.MethodPost, "/{id}/ddl/rename", s.handle(s.handleDDLRename))
			r.Method(http.MethodPost, "/{id}/keys/value", s.handle(s.handleRedisSet))
			r.Method(http.MethodPost, "/{id}/keys/expire", s.handle(s.handleRedisExpire))
			r.Method(http.MethodPost, "/{id}/keys/rename", s.handle(s.handleRedisRename))
			r.Method(http.MethodPost, "/{id}/documents", s.handle(s.handleMongoInsert))
			r.Method(http.MethodPatch, "/{id}/documents", s.handle(s.handleMongoReplace))
			r.Method(http.MethodPost, "/{id}/aggregate", s.handle(s.handleMongoAggregate))
			r.Method(http.MethodPost, "/{id}/collections", s.handle(s.handleMongoCreateCollection))
		})
		// Everything here is destructive: the capability, the tighter budget and
		// the audit entry apply to all of it. Only some of it additionally asks
		// the operator to type a phrase, and each handler says which it is and
		// why — invariant 3 is the rule they are applying.
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodDelete, "/{id}/rows", s.handle(s.handleDBRowDelete))
			// Killing a session stops work in flight and rolls it back, so it
			// sits here with the row delete rather than with the read-side
			// activity list it is launched from.
			r.Method(http.MethodPost, "/{id}/activity/kill", s.handle(s.handleDBKill))
			r.Method(http.MethodPost, "/{id}/restore", s.handle(s.handleDBRestore))
			// Schema changes that destroy. All but the index drop demand a
			// typed confirmation naming what they remove; an index is the one
			// object here that rebuilds from its own definition.
			r.Method(http.MethodDelete, "/{id}/ddl/table", s.handle(s.handleDDLDropTable))
			r.Method(http.MethodDelete, "/{id}/ddl/column", s.handle(s.handleDDLDropColumn))
			r.Method(http.MethodDelete, "/{id}/ddl/index", s.handle(s.handleDDLDropIndex))
			r.Method(http.MethodPost, "/{id}/ddl/truncate", s.handle(s.handleDDLTruncate))
			r.Method(http.MethodDelete, "/{id}/keys", s.handle(s.handleRedisDelete))
			r.Method(http.MethodDelete, "/{id}/documents", s.handle(s.handleMongoDelete))
			r.Method(http.MethodDelete, "/{id}/collections", s.handle(s.handleMongoDropCollection))
			// Removing the database itself, which is the one thing on this
			// page that cannot be undone by anything except a dump taken
			// first. system.admin on top of the destructive group: creating a
			// database needs it, and so should destroying one.
			r.With(httpx.RequireCapability(auth.CapSystemAdmin)).
				Method(http.MethodDelete, "/{id}/database", s.handle(s.handleDBDropDatabase))
		})
	})
}

func (s *Server) dbConnRow(ctx context.Context, id int64) (*dbConnection, string, error) {
	var (
		name, driver, dsnEnc string
		created              int64
	)
	err := s.Store.DB.QueryRowContext(ctx,
		`SELECT name, driver, dsn_enc, created_at FROM db_connections WHERE id = ?`, id).
		Scan(&name, &driver, &dsnEnc, &created)
	if err == sql.ErrNoRows {
		return nil, "", httpx.ErrNotFound
	}
	if err != nil {
		return nil, "", httpx.Internal(err)
	}
	dsn, err := s.Sealer.Open(dsnEnc)
	if err != nil {
		return nil, "", httpx.Internal(err)
	}
	conn := &dbConnection{
		ID: id, Name: name, Driver: dbx.Driver(driver),
		CreatedAt: time.Unix(created, 0).UTC(),
	}
	// Contained again here rather than trusted from the store: the roots may
	// have been narrowed since this row was written, and every caller that
	// opens a pool comes through this function.
	dsn, cerr := s.containDSN(conn.Driver, dsn)
	if cerr != nil {
		return nil, "", cerr
	}
	if info, err := dbx.ParseDSN(conn.Driver, dsn); err == nil {
		conn.Host, conn.Port, conn.User, conn.Database = info.Host, info.Port, info.User, info.Database
	}
	return conn, dsn, nil
}

func (s *Server) dbPool(ctx context.Context, id int64) (*sql.DB, *dbConnection, error) {
	conn, dsn, err := s.dbConnRow(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	// Anything that is not a SQL engine has no pool to hand back. Naming the
	// engine in the refusal beats a generic "not available": the operator picked
	// this connection and needs to know which of its tabs apply.
	if !conn.Driver.IsSQL() {
		return nil, conn, httpx.BadRequest("this endpoint is for SQL engines; %s uses its own surface", conn.Driver)
	}
	pool, err := s.modules.dbs.Pool(ctx, id, conn.Driver, dsn)
	if err != nil {
		return nil, conn, httpx.Err(http.StatusBadGateway, "connect_failed", err.Error())
	}
	return pool, conn, nil
}

func (s *Server) handleDBConnList(w http.ResponseWriter, r *http.Request) error {
	rows, err := s.Store.DB.QueryContext(r.Context(),
		`SELECT id FROM db_connections ORDER BY name`)
	if err != nil {
		return httpx.Internal(err)
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return httpx.Internal(err)
		}
		ids = append(ids, id)
	}
	out := []*dbConnection{}
	for _, id := range ids {
		conn, _, err := s.dbConnRow(r.Context(), id)
		if err != nil {
			continue
		}
		out = append(out, conn)
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// connNameRe deliberately excludes "/" and ".." so a connection name cannot
// walk out of the backup directory it names.
var connNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,63}$`)

type createDBConnRequest struct {
	Name   string     `json:"name"`
	Driver dbx.Driver `json:"driver"`
	DSN    string     `json:"dsn"`
}

// containDSN puts a SQLite connection's path through the same check every
// other client-supplied path goes through.
//
// A SQLite DSN *is* a filesystem path, which makes it exactly the case
// invariant 6 names: a path that does not look like a file operation. Without
// this the connection form opens — and, because SQLite creates what is not
// there, writes — a file anywhere the backend can reach, which in the shipped
// container is the host's whole filesystem mounted at its real names. The
// Files panel already refuses those paths with `outside_root`; the two must
// not disagree about where the dashboard may write.
//
// It runs on the way in (create, update, test) so a bad path is refused where
// the operator can see why, and again on the way out of the store, so a
// connection saved before the roots were narrowed cannot keep using the path
// they used to allow.
func (s *Server) containDSN(driver dbx.Driver, dsn string) (string, error) {
	if driver != dbx.DriverSQLite {
		return dsn, nil
	}
	if s.modules.files == nil {
		return "", httpx.Err(http.StatusServiceUnavailable, "unavailable",
			"file roots are not configured, so a SQLite path cannot be contained")
	}
	path, _ := dbx.SQLiteDSNPath(dsn)
	if strings.TrimSpace(path) == "" {
		return "", httpx.BadRequest("a SQLite connection needs a file path")
	}
	resolved, err := s.modules.files.Resolve(path)
	if errors.Is(err, files.ErrOutsideRoot) {
		// Rendered the way the Files panel renders it. The bare error would
		// come back as "internal error", which tells the operator their path
		// was refused but not that JD_FILE_ROOTS is the reason — and that is
		// the one thing they need in order to fix it.
		return "", httpx.Err(http.StatusForbidden, "outside_root", err.Error())
	}
	if err != nil {
		return "", err
	}
	return dbx.SQLiteDSNWithPath(dsn, resolved), nil
}

func (s *Server) handleDBConnCreate(w http.ResponseWriter, r *http.Request) error {
	var req createDBConnRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if !req.Driver.Valid() {
		return httpx.BadRequest("driver must be one of %s", driverNames())
	}
	if req.Name == "" || req.DSN == "" {
		return httpx.BadRequest("name and dsn are required")
	}
	// The name becomes a directory under JD_BACKUP_DIR when this connection
	// is dumped, so it is bounded like any other path segment rather than
	// taken verbatim.
	if !connNameRe.MatchString(req.Name) {
		return httpx.BadRequest("name may contain letters, digits, spaces, dots, dashes and underscores")
	}
	dsn, err := s.containDSN(req.Driver, req.DSN)
	if err != nil {
		return err
	}
	sealed, err := s.Sealer.Seal(dsn)
	if err != nil {
		return httpx.Internal(err)
	}
	res, err := s.Store.DB.ExecContext(r.Context(),
		`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES(?,?,?,?)`,
		req.Name, string(req.Driver), sealed, time.Now().Unix())
	if err != nil {
		return httpx.BadRequest("could not save connection: %v", err)
	}
	id, _ := res.LastInsertId()
	conn, _, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	// The DSN itself never reaches the audit trail; the parsed host and user
	// identify the connection without leaking the password.
	httpx.SetAudit(r, "database.connection.create", req.Name,
		map[string]any{"driver": req.Driver, "host": conn.Host, "user": conn.User})
	httpx.JSON(w, http.StatusCreated, conn)
	return nil
}

func (s *Server) handleDBConnDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		return httpx.BadRequest("invalid id")
	}
	conn, _, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	// No typed phrase: this forgets a connection string, it does not touch the
	// server at the other end of it. Re-adding one is a form, not a restore.
	s.modules.dbs.Close(id)
	if _, err := s.Store.DB.ExecContext(r.Context(), `DELETE FROM db_connections WHERE id = ?`, id); err != nil {
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "database.connection.delete", conn.Name, nil)
	httpx.NoContent(w)
	return nil
}

func parseID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		return 0, httpx.BadRequest("invalid id")
	}
	return id, nil
}

func (s *Server) handleDBPing(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	// Every engine answers this, including the two that are not SQL. Falling
	// through to the pool for them reported "this endpoint is for SQL engines"
	// as though it were a connection failure, which left a healthy Redis
	// connection showing a permanently red badge.
	switch conn.Driver {
	case dbx.DriverMongo:
		client, err := dbx.MongoClient(r.Context(), dsn)
		if err != nil {
			httpx.JSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
			return nil
		}
		defer client.Disconnect(context.Background())
	case dbx.DriverRedis:
		client, err := dbx.RedisClient(r.Context(), dsn, 0)
		if err != nil {
			httpx.JSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
			return nil
		}
		defer client.Close()
	default:
		pool, _, err := s.dbPool(r.Context(), id)
		if err == nil {
			// The pool is cached, so having one says the connection worked at
			// some point in the past, not that it works now. It has to be
			// dialled: a server restarted underneath the dashboard — which is
			// every MySQL during its own first-boot initialisation, and every
			// database anybody ever upgrades — leaves a pool of connections
			// that are open and dead, and this reported them healthy while
			// every query returned "invalid connection".
			err = pool.PingContext(r.Context())
		}
		if err != nil {
			// Dropped rather than left to expire with the pool's lifetime, so
			// the next request dials again instead of failing the same way for
			// half an hour.
			s.modules.dbs.Close(id)
			httpx.JSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
			return nil
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (s *Server) handleDBStats(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	if conn.Driver == dbx.DriverMongo {
		client, err := dbx.MongoClient(r.Context(), dsn)
		if err != nil {
			return httpx.Err(http.StatusBadGateway, "connect_failed", err.Error())
		}
		defer client.Disconnect(context.Background())
		status, err := dbx.MongoServerStatus(r.Context(), client)
		if err != nil {
			return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"server": status})
		return nil
	}
	if conn.Driver == dbx.DriverRedis {
		client, err := dbx.RedisClient(r.Context(), dsn, 0)
		if err != nil {
			return httpx.Err(http.StatusBadGateway, "connect_failed", err.Error())
		}
		defer client.Close()
		info, err := dbx.RedisInfo(r.Context(), client)
		if err != nil {
			return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"server": info})
		return nil
	}
	if _, _, err := s.dbPool(r.Context(), id); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"pool": s.modules.dbs.Stats(id)})
	return nil
}

func (s *Server) handleDBList(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	if conn.Driver == dbx.DriverMongo {
		client, err := dbx.MongoClient(r.Context(), dsn)
		if err != nil {
			return httpx.Err(http.StatusBadGateway, "connect_failed", err.Error())
		}
		defer client.Disconnect(context.Background())
		dbs, err := dbx.MongoDatabases(r.Context(), client)
		if err != nil {
			return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
		}
		httpx.JSON(w, http.StatusOK, dbs)
		return nil
	}
	if conn.Driver == dbx.DriverRedis {
		client, err := dbx.RedisClient(r.Context(), dsn, 0)
		if err != nil {
			return httpx.Err(http.StatusBadGateway, "connect_failed", err.Error())
		}
		defer client.Close()
		dbs, err := dbx.RedisDatabases(r.Context(), client)
		if err != nil {
			return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
		}
		httpx.JSON(w, http.StatusOK, dbs)
		return nil
	}
	pool, conn, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	dbs, err := dbx.ListDatabases(r.Context(), pool, conn.Driver)
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	httpx.JSON(w, http.StatusOK, dbs)
	return nil
}

func (s *Server) handleDBTables(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	schema := r.URL.Query().Get("schema")
	if conn.Driver == dbx.DriverMongo {
		client, err := dbx.MongoClient(r.Context(), dsn)
		if err != nil {
			return httpx.Err(http.StatusBadGateway, "connect_failed", err.Error())
		}
		defer client.Disconnect(context.Background())
		if schema == "" {
			schema = conn.Database
		}
		if schema == "" {
			return httpx.BadRequest("schema query parameter is required for MongoDB")
		}
		cols, err := dbx.MongoCollections(r.Context(), client, schema)
		if err != nil {
			return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
		}
		httpx.JSON(w, http.StatusOK, cols)
		return nil
	}
	pool, conn, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	tables, err := dbx.ListTables(r.Context(), pool, conn.Driver, schema)
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	httpx.JSON(w, http.StatusOK, tables)
	return nil
}

func (s *Server) handleDBColumns(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	pool, conn, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	cols, err := dbx.ListColumns(r.Context(), pool, conn.Driver, q.Get("schema"), q.Get("table"))
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	httpx.JSON(w, http.StatusOK, cols)
	return nil
}

func (s *Server) handleDBBrowse(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	limit := atoiDefault(q.Get("limit"), 100)
	offset := atoiDefault(q.Get("offset"), 0)

	if conn.Driver == dbx.DriverMongo {
		client, err := dbx.MongoClient(r.Context(), dsn)
		if err != nil {
			return httpx.Err(http.StatusBadGateway, "connect_failed", err.Error())
		}
		defer client.Disconnect(context.Background())
		res, err := dbx.MongoFind(r.Context(), client, q.Get("schema"), q.Get("table"), q.Get("filter"), limit, offset)
		if err != nil {
			return httpx.BadRequest("%v", err)
		}
		httpx.JSON(w, http.StatusOK, res)
		return nil
	}
	pool, conn, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	filters, err := parseFilters(q.Get("filters"))
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	res, err := dbx.Browse(ctx, pool, conn.Driver, dbx.BrowseOptions{
		Schema: q.Get("schema"), Table: q.Get("table"),
		Limit: limit, Offset: offset,
		OrderBy: q.Get("orderBy"), Desc: q.Get("dir") == "desc",
		Filters: filters,
	})
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

// parseFilters decodes the grid's filter list, which travels as a JSON array in
// a query parameter because a GET has no body and a filter is structured. An
// unparseable list is an error rather than an ignored filter: silently
// returning unfiltered rows to somebody who asked for filtered ones is the
// worst of the available failures.
func parseFilters(raw string) ([]dbx.Filter, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var filters []dbx.Filter
	if err := json.Unmarshal([]byte(raw), &filters); err != nil {
		return nil, fmt.Errorf("filters must be a JSON array: %v", err)
	}
	if len(filters) > 12 {
		return nil, fmt.Errorf("too many filters")
	}
	return filters, nil
}

type queryRequest struct {
	Query   string `json:"query"`
	MaxRows int    `json:"maxRows"`
}

// handleDBClassify lets the editor warn before anything is sent. It is a pure
// analysis of the text and touches no database.
func (s *Server) handleDBClassify(w http.ResponseWriter, r *http.Request) error {
	var req queryRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	httpx.SkipAudit(r)
	httpx.JSON(w, http.StatusOK, dbx.Classify(req.Query))
	return nil
}

// handleDBQuery runs arbitrary SQL. A destructive statement additionally
// requires the destructive capability and the tighter budget, and a *critical*
// one — a DROP, a TRUNCATE, an UPDATE or DELETE with no WHERE — also requires a
// typed confirmation, so the statement that hits every row cannot go out on a
// single click.
func (s *Server) handleDBQuery(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req queryRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Query == "" {
		return httpx.BadRequest("query is required")
	}
	statement, err := dbx.SingleStatement(req.Query)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	req.Query = statement
	risk := dbx.Classify(req.Query)
	p := httpx.MustPrincipal(r)
	if risk.Destructive {
		if !p.Can(auth.CapDestructive) {
			return httpx.Err(http.StatusForbidden, "forbidden",
				"this statement is destructive and your role does not permit it")
		}
		// Only critical statements are typed for. "high" is a scoped UPDATE or
		// DELETE — the ordinary work of a SQL console, done dozens of times in
		// a sitting — and "run high" names nothing about what is being run, so
		// it was boilerplate to type rather than a sentence to read. Critical
		// is the unscoped version of the same statement, a DROP, a TRUNCATE:
		// rare, and the whole table either way.
		if risk.Level == "critical" {
			if err := httpx.RequireTypedConfirmation(w, r, "run "+risk.Level); err != nil {
				return err
			}
		}
		if !s.destrLim.Allow(p.Username() + "|dbquery") {
			return httpx.Err(http.StatusTooManyRequests, "rate_limited",
				"too many destructive statements, slow down")
		}
	}
	pool, _, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 120*time.Second)
	defer cancel()
	start := time.Now()
	res, err := dbx.RunQuery(ctx, pool, req.Query, req.MaxRows)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		s.recordDBHistory(r.Context(), id, req.Query, risk.Level, false, elapsed, 0)
		httpx.SetAudit(r, "database.query", strconv.FormatInt(id, 10),
			map[string]any{"risk": risk.Level, "error": err.Error()})
		return httpx.BadRequest("%v", err)
	}
	s.recordDBHistory(r.Context(), id, req.Query, risk.Level, true, elapsed, res.RowCount)
	httpx.SetAudit(r, "database.query", strconv.FormatInt(id, 10), map[string]any{
		"risk": risk.Level, "destructive": risk.Destructive,
		"rowsAffected": res.Affected, "rowCount": res.RowCount, "statement": req.Query,
	})
	httpx.JSON(w, http.StatusOK, map[string]any{"result": res, "risk": risk})
	return nil
}

type dbBackupRequest struct {
	Database string `json:"database"`
}

func (s *Server) handleDBBackup(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req dbBackupRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Minute)
	defer cancel()
	outDir := filepath.Join(s.Cfg.BackupLocalDir, "databases", conn.Name)
	res, err := dbx.Dump(ctx, conn.Driver, dsn, req.Database, outDir)
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "dump_failed", err.Error())
	}
	httpx.SetAudit(r, "database.backup", conn.Name,
		map[string]any{"database": res.Database, "path": res.Path, "size": res.Size})
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

type dbRestoreRequest struct {
	Database string `json:"database"`
	DumpPath string `json:"dumpPath"`
}

func (s *Server) handleDBRestore(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req dbRestoreRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	target := req.Database
	if target == "" {
		target = conn.Database
	}
	// A restore overwrites live data. Confirming on the target database name
	// forces the operator to read which database they are about to replace.
	if err := httpx.RequireTypedConfirmation(w, r, target); err != nil {
		return err
	}
	// Invariant 6: a client-supplied path goes through files.Resolve, which is
	// the only thing in the codebase that checks both the literal path and its
	// symlink-resolved form against JD_FILE_ROOTS. "It stats" was the whole of
	// the previous check.
	dumpPath, err := s.modules.files.Resolve(req.DumpPath)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	ctx, cancel := timeoutCtx(r, 60*time.Minute)
	defer cancel()
	out, err := dbx.Restore(ctx, conn.Driver, dsn, req.Database, dumpPath)
	if err != nil {
		httpx.SetAudit(r, "database.restore", conn.Name,
			map[string]any{"database": target, "dumpPath": req.DumpPath, "error": err.Error()})
		return httpx.Err(http.StatusBadGateway, "restore_failed", err.Error())
	}
	httpx.SetAudit(r, "database.restore", conn.Name,
		map[string]any{"database": target, "dumpPath": req.DumpPath})
	httpx.JSON(w, http.StatusOK, map[string]string{"output": out})
	return nil
}

// handleDBConnTest verifies a DSN before it is saved. It reports the server
// version on success so the operator can see they reached the engine they
// meant to, and never persists anything.
func (s *Server) handleDBConnTest(w http.ResponseWriter, r *http.Request) error {
	var req createDBConnRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if !req.Driver.Valid() {
		return httpx.BadRequest("driver must be one of %s", driverNames())
	}
	if req.DSN == "" {
		return httpx.BadRequest("dsn is required")
	}
	if _, err := s.containDSN(req.Driver, req.DSN); err != nil {
		return err
	}
	httpx.SkipAudit(r)
	ctx, cancel := timeoutCtx(r, 15*time.Second)
	defer cancel()
	if req.Driver == dbx.DriverMongo {
		client, err := dbx.MongoClient(ctx, req.DSN)
		if err != nil {
			httpx.JSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
			return nil
		}
		defer client.Disconnect(context.Background())
		httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
		return nil
	}
	probeDSN, err := s.containDSN(req.Driver, req.DSN)
	if err != nil {
		return err
	}
	version, err := dbx.Probe(ctx, req.Driver, probeDSN)
	if err != nil {
		httpx.JSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return nil
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "version": version})
	return nil
}

type updateDBConnRequest struct {
	Name string `json:"name"`
	DSN  string `json:"dsn"`
}

// handleDBConnUpdate edits a connection's name and, optionally, its DSN. The
// driver is fixed at creation — changing it would mean the stored secret no
// longer parses — so it is not editable here. An empty DSN leaves the existing
// one untouched, so an operator can rename without re-typing a password they
// cannot read back.
func (s *Server) handleDBConnUpdate(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	conn, _, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	var req updateDBConnRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	name := req.Name
	if name == "" {
		name = conn.Name
	}
	if !connNameRe.MatchString(name) {
		return httpx.BadRequest("name may contain letters, digits, spaces, dots, dashes and underscores")
	}
	if req.DSN != "" {
		dsn, err := s.containDSN(conn.Driver, req.DSN)
		if err != nil {
			return err
		}
		sealed, err := s.Sealer.Seal(dsn)
		if err != nil {
			return httpx.Internal(err)
		}
		if _, err := s.Store.DB.ExecContext(r.Context(),
			`UPDATE db_connections SET name = ?, dsn_enc = ? WHERE id = ?`, name, sealed, id); err != nil {
			return httpx.BadRequest("could not update connection: %v", err)
		}
		// The pool was opened against the old DSN; drop it so the next request
		// dials the new one.
		s.modules.dbs.Close(id)
	} else {
		if _, err := s.Store.DB.ExecContext(r.Context(),
			`UPDATE db_connections SET name = ? WHERE id = ?`, name, id); err != nil {
			return httpx.BadRequest("could not update connection: %v", err)
		}
	}
	updated, _, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.SetAudit(r, "database.connection.update", name,
		map[string]any{"dsnChanged": req.DSN != ""})
	httpx.JSON(w, http.StatusOK, updated)
	return nil
}

// handleDBTableDetail returns a table's structure: columns, primary key,
// indexes, foreign keys and the DDL that would recreate it.
func (s *Server) handleDBTableDetail(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	pool, conn, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	detail, err := dbx.Detail(ctx, pool, conn.Driver, q.Get("schema"), q.Get("table"))
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	httpx.JSON(w, http.StatusOK, detail)
	return nil
}

// handleDBExport streams an entire table as CSV or JSON. It is a read, so it
// needs no capability beyond the browse routes; the row cap is high and a
// truncated download is flagged in a trailing header rather than silently cut.
func (s *Server) handleDBExport(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	table := q.Get("table")
	if table == "" {
		return httpx.BadRequest("table is required")
	}
	format := dbx.ExportFormat(q.Get("format"))
	if !format.Valid() {
		format = dbx.ExportCSV
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 10*time.Minute)
	defer cancel()

	filename := fmt.Sprintf("%s.%s", table, format.Extension())
	w.Header().Set("Content-Type", format.ContentType())
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))

	var (
		count     int
		truncated bool
	)
	if conn.Driver == dbx.DriverMongo {
		// A collection exports through its own path: the column set is the
		// union of the documents' keys rather than a fixed result shape, and
		// the filter is a document rather than a WHERE clause.
		client, cerr := dbx.MongoClient(ctx, dsn)
		if cerr != nil {
			httpx.SetAudit(r, "database.export", conn.Name,
				map[string]any{"table": table, "error": cerr.Error()})
			return nil
		}
		defer client.Disconnect(context.Background())
		database := q.Get("schema")
		if database == "" {
			database = conn.Database
		}
		count, truncated, err = dbx.MongoExport(ctx, client, database, table,
			dbx.MongoFindOptions{Filter: q.Get("filter"), Sort: q.Get("sort")},
			format, w, atoiDefault(q.Get("limit"), 0))
	} else {
		pool, _, perr := s.dbPool(r.Context(), id)
		if perr != nil {
			return perr
		}
		count, truncated, err = dbx.ExportTable(ctx, pool, conn.Driver, q.Get("schema"), table, format, w,
			atoiDefault(q.Get("limit"), 0))
	}
	if err != nil {
		// Headers are already sent, so the error cannot become a JSON body; it is
		// recorded in the audit trail and the connection is dropped by the client
		// seeing a short file. This is the one export failure mode worth logging.
		httpx.SetAudit(r, "database.export", conn.Name,
			map[string]any{"table": table, "error": err.Error()})
		return nil
	}
	httpx.SetAudit(r, "database.export", conn.Name,
		map[string]any{"table": table, "format": string(format), "rows": count, "truncated": truncated})
	return nil
}

type ormRequest struct {
	Target dbx.ORMTarget `json:"target"`
	Schema string        `json:"schema"`
}

// handleDBGenerateORM introspects the connection and returns a generated ORM
// schema file (Prisma or Drizzle). It is a read of the schema catalogue, so it
// needs no write capability.
func (s *Server) handleDBGenerateORM(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req ormRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if !req.Target.Valid() {
		names := make([]string, 0, len(dbx.ORMTargets()))
		for _, t := range dbx.ORMTargets() {
			names = append(names, string(t))
		}
		return httpx.BadRequest("target must be one of %s", strings.Join(names, ", "))
	}
	pool, conn, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	tables, err := dbx.ListTables(ctx, pool, conn.Driver, req.Schema)
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	details := map[string]*dbx.TableDetail{}
	for _, t := range tables {
		d, err := dbx.Detail(ctx, pool, conn.Driver, t.Schema, t.Name)
		if err != nil {
			continue
		}
		details[t.Name] = d
	}
	schema, err := dbx.GenerateORM(req.Target, conn.Driver, tables, details)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	filename := ormFilename(req.Target)
	httpx.SetAudit(r, "database.orm.generate", conn.Name, map[string]any{"target": string(req.Target)})
	httpx.JSON(w, http.StatusOK, map[string]any{"schema": schema, "filename": filename})
	return nil
}

type rowRequest struct {
	Schema string         `json:"schema"`
	Table  string         `json:"table"`
	Values map[string]any `json:"values"`
	Key    map[string]any `json:"key"`
}

func (s *Server) handleDBRowInsert(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req rowRequest
	if err := httpx.DecodeJSONNumbers(r, &req); err != nil {
		return err
	}
	if req.Table == "" {
		return httpx.BadRequest("table is required")
	}
	pool, conn, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	res, err := dbx.InsertRow(ctx, pool, conn.Driver, req.Schema, req.Table, req.Values)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "database.row.insert", conn.Name,
		map[string]any{"table": req.Table, "columns": len(req.Values)})
	httpx.JSON(w, http.StatusOK, map[string]any{"result": res})
	return nil
}

func (s *Server) handleDBRowUpdate(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req rowRequest
	if err := httpx.DecodeJSONNumbers(r, &req); err != nil {
		return err
	}
	if req.Table == "" {
		return httpx.BadRequest("table is required")
	}
	if len(req.Key) == 0 {
		return httpx.BadRequest("a primary key is required to identify the row to update")
	}
	pool, conn, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	res, err := dbx.UpdateRow(ctx, pool, conn.Driver, req.Schema, req.Table, req.Values, req.Key)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "database.row.update", conn.Name,
		map[string]any{"table": req.Table, "key": req.Key})
	httpx.JSON(w, http.StatusOK, map[string]any{"result": res})
	return nil
}

func (s *Server) handleDBRowDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req rowRequest
	if err := httpx.DecodeJSONNumbers(r, &req); err != nil {
		return err
	}
	if req.Table == "" {
		return httpx.BadRequest("table is required")
	}
	if len(req.Key) == 0 {
		return httpx.BadRequest("a primary key is required to identify the row to delete")
	}
	conn, _, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	// No typed phrase. Deleting a row is what a data browser is for — a dozen
	// a day for anyone using this page as intended — and the bulk path made it
	// worse by asking for the same table name eight times in a row. A phrase in
	// front of an everyday act is not read, it is typed, and that habit is what
	// weakens the phrase everywhere it still matters. The capability check, the
	// tighter budget and the audit entry all still apply.
	pool, conn, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	res, err := dbx.DeleteRow(ctx, pool, conn.Driver, req.Schema, req.Table, req.Key)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "database.row.delete", conn.Name,
		map[string]any{"table": req.Table, "key": req.Key})
	httpx.JSON(w, http.StatusOK, map[string]any{"result": res})
	return nil
}

// --- Saved queries and history ------------------------------------------

type savedQuery struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	SQL       string    `json:"sql"`
	CreatedAt time.Time `json:"createdAt"`
}

func (s *Server) handleDBSavedList(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	rows, err := s.Store.DB.QueryContext(r.Context(),
		`SELECT id, name, sql, created_at FROM db_saved_queries WHERE connection_id = ? ORDER BY name`, id)
	if err != nil {
		return httpx.Internal(err)
	}
	defer rows.Close()
	out := []savedQuery{}
	for rows.Next() {
		var q savedQuery
		var created int64
		if err := rows.Scan(&q.ID, &q.Name, &q.SQL, &created); err != nil {
			return httpx.Internal(err)
		}
		q.CreatedAt = time.Unix(created, 0).UTC()
		out = append(out, q)
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

type savedQueryRequest struct {
	Name string `json:"name"`
	SQL  string `json:"sql"`
}

func (s *Server) handleDBSavedCreate(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req savedQueryRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Name == "" || req.SQL == "" {
		return httpx.BadRequest("name and sql are required")
	}
	if _, _, err := s.dbConnRow(r.Context(), id); err != nil {
		return err
	}
	res, err := s.Store.DB.ExecContext(r.Context(),
		`INSERT INTO db_saved_queries(connection_id, name, sql, created_at) VALUES(?,?,?,?)`,
		id, req.Name, req.SQL, time.Now().Unix())
	if err != nil {
		return httpx.BadRequest("could not save query: %v", err)
	}
	newID, _ := res.LastInsertId()
	httpx.SetAudit(r, "database.query.save", req.Name, nil)
	httpx.JSON(w, http.StatusCreated, savedQuery{
		ID: newID, Name: req.Name, SQL: req.SQL, CreatedAt: time.Now().UTC(),
	})
	return nil
}

func (s *Server) handleDBSavedDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	qid, err := strconv.ParseInt(chi.URLParam(r, "qid"), 10, 64)
	if err != nil {
		return httpx.BadRequest("invalid query id")
	}
	if _, err := s.Store.DB.ExecContext(r.Context(),
		`DELETE FROM db_saved_queries WHERE id = ? AND connection_id = ?`, qid, id); err != nil {
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "database.query.unsave", strconv.FormatInt(qid, 10), nil)
	httpx.NoContent(w)
	return nil
}

type historyEntry struct {
	ID       int64     `json:"id"`
	SQL      string    `json:"sql"`
	Risk     string    `json:"risk"`
	Success  bool      `json:"success"`
	Duration int64     `json:"durationMs"`
	RowCount int       `json:"rowCount"`
	RanAt    time.Time `json:"ranAt"`
}

func (s *Server) handleDBHistory(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	limit := atoiDefault(r.URL.Query().Get("limit"), 50)
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.Store.DB.QueryContext(r.Context(),
		`SELECT id, sql, risk, success, duration_ms, row_count, ran_at
		 FROM db_query_history WHERE connection_id = ? ORDER BY ran_at DESC LIMIT ?`, id, limit)
	if err != nil {
		return httpx.Internal(err)
	}
	defer rows.Close()
	out := []historyEntry{}
	for rows.Next() {
		var e historyEntry
		var success, ranAt int64
		if err := rows.Scan(&e.ID, &e.SQL, &e.Risk, &success, &e.Duration, &e.RowCount, &ranAt); err != nil {
			return httpx.Internal(err)
		}
		e.Success = success != 0
		e.RanAt = time.Unix(ranAt, 0).UTC()
		out = append(out, e)
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// recordDBHistory appends a statement to the per-connection history and prunes
// it to the most recent entries. It never fails the request it records: a
// history write that errors is logged and swallowed, because losing an audit
// convenience must not turn a successful query into a failed one.
func (s *Server) recordDBHistory(ctx context.Context, connID int64, query, risk string, success bool, durationMs int64, rowCount int) {
	succ := 0
	if success {
		succ = 1
	}
	if _, err := s.Store.DB.ExecContext(ctx,
		`INSERT INTO db_query_history(connection_id, sql, risk, success, duration_ms, row_count, ran_at)
		 VALUES(?,?,?,?,?,?,?)`,
		connID, query, risk, succ, durationMs, rowCount, time.Now().Unix()); err != nil {
		s.Log.Warn("db history write failed", "err", err)
		return
	}
	// Keep only the newest 100 statements per connection. Pruning on write means
	// no separate reaper and no unbounded growth.
	_, _ = s.Store.DB.ExecContext(ctx,
		`DELETE FROM db_query_history WHERE connection_id = ? AND id NOT IN (
		    SELECT id FROM db_query_history WHERE connection_id = ? ORDER BY ran_at DESC LIMIT 100
		 )`, connID, connID)
}

// --- Engine catalogue and schema reads -----------------------------------

// driverInfo tells the frontend what an engine can do, so the UI does not keep
// its own copy of that knowledge and drift from what the server enforces. A tab
// that would 400 on every request should not be offered at all.
type driverInfo struct {
	ID          dbx.Driver `json:"id"`
	Label       string     `json:"label"`
	Kind        string     `json:"kind"`
	Placeholder string     `json:"placeholder"`
	SQL         bool       `json:"sql"`
	DDL         bool       `json:"ddl"`
	ColumnTypes []string   `json:"columnTypes,omitempty"`
	FilterOps   []string   `json:"filterOps,omitempty"`
}

// containerNameRe and dbNameRe bound the two names a provision request can
// choose. Both become arguments to something else — a Docker container name and
// a CREATE DATABASE the image runs at first boot — so neither is taken verbatim.
var (
	containerNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$`)
	dbNameRe        = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)
)

var driverLabels = map[dbx.Driver]struct{ label, kind, placeholder string }{
	dbx.DriverPostgres:   {"PostgreSQL", "sql", "postgres://user:password@127.0.0.1:5432/dbname?sslmode=disable"},
	dbx.DriverMySQL:      {"MySQL / MariaDB", "sql", "user:password@tcp(127.0.0.1:3306)/dbname"},
	dbx.DriverSQLite:     {"SQLite", "sql", "/var/lib/myapp/data.db"},
	dbx.DriverMSSQL:      {"SQL Server", "sql", "sqlserver://user:password@127.0.0.1:1433?database=dbname"},
	dbx.DriverClickHouse: {"ClickHouse", "sql", "clickhouse://user:password@127.0.0.1:9000/default"},
	dbx.DriverOracle:     {"Oracle", "sql", "oracle://user:password@127.0.0.1:1521/ORCLPDB1"},
	dbx.DriverMongo:      {"MongoDB", "document", "mongodb://user:password@127.0.0.1:27017/dbname"},
	dbx.DriverRedis:      {"Redis", "keyvalue", "redis://:password@127.0.0.1:6379/0"},
}

func (s *Server) handleDBDrivers(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	out := []driverInfo{}
	for _, d := range dbx.Drivers() {
		meta := driverLabels[d]
		info := driverInfo{
			ID: d, Label: meta.label, Kind: meta.kind,
			Placeholder: meta.placeholder, SQL: d.IsSQL(),
		}
		if dl, err := dbx.DialectFor(d); err == nil {
			info.DDL = dl.SupportsDDL()
			info.ColumnTypes = dl.ColumnTypes()
			info.FilterOps = dbx.FilterOps()
		}
		out = append(out, info)
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// handleDBCount answers how many rows match the current filters. It is its own
// request because COUNT(*) is a scan on most engines and the page fetch must
// stay cheap whether or not anyone asked for a total.
func (s *Server) handleDBCount(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	pool, conn, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	filters, err := parseFilters(q.Get("filters"))
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	ctx, cancel := timeoutCtx(r, 120*time.Second)
	defer cancel()
	n, err := dbx.Count(ctx, pool, conn.Driver, dbx.BrowseOptions{
		Schema: q.Get("schema"), Table: q.Get("table"), Filters: filters,
	})
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"count": n})
	return nil
}

// handleDBOutline feeds the SQL editor's completion.
func (s *Server) handleDBOutline(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	pool, conn, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.SkipAudit(r)
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	outline, err := dbx.Outline(ctx, pool, conn.Driver, r.URL.Query().Get("schema"))
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	httpx.JSON(w, http.StatusOK, outline)
	return nil
}

// handleDBRelations returns the foreign-key graph the entity diagram draws.
func (s *Server) handleDBRelations(w http.ResponseWriter, r *http.Request) error {
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
	rels, err := dbx.Relations(ctx, pool, conn.Driver, r.URL.Query().Get("schema"))
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	httpx.JSON(w, http.StatusOK, rels)
	return nil
}

// handleDBExplain returns the engine's plan for a statement.
//
// It is on the read side of the route map, with no capability beyond browsing,
// because planning is not running: every dialect's implementation describes the
// statement without executing it. That is the property the whole feature rests
// on — a "show me the plan" button that quietly executed a DELETE would be the
// worst control in the product — so it is asserted in the dialect contract and
// tested against every live engine rather than assumed here.
func (s *Server) handleDBExplain(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req queryRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if strings.TrimSpace(req.Query) == "" {
		return httpx.BadRequest("query is required")
	}
	pool, conn, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	d, err := dbx.DialectFor(conn.Driver)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	res, err := d.ExplainPlan(ctx, pool, req.Query)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "database.explain", conn.Name, map[string]any{"statement": req.Query})
	httpx.JSON(w, http.StatusOK, map[string]any{"result": res})
	return nil
}

// ormFilename is the name the generated file should be saved under. It lives
// here rather than in dbx because it is a download-header concern, not a
// property of the schema — but it stays in one place so a new generator cannot
// be added without deciding what its file is called.
func ormFilename(t dbx.ORMTarget) string {
	switch t {
	case dbx.ORMPrisma:
		return "schema.prisma"
	case dbx.ORMDrizzle:
		return "schema.ts"
	case dbx.ORMTypeScript:
		return "types.ts"
	case dbx.ORMZod:
		return "schemas.ts"
	default:
		return "schema.txt"
	}
}

// handleDBTargets lists the code generators this build offers, so the ORM tab
// is populated from the server rather than from a second list in TypeScript
// that drifts the first time a generator is added.
func (s *Server) handleDBTargets(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	out := make([]map[string]string, 0, len(dbx.ORMTargets()))
	for _, t := range dbx.ORMTargets() {
		out = append(out, map[string]string{
			"id": string(t), "label": ormTargetLabel(t), "filename": ormFilename(t),
			"description": ormTargetBlurb(t),
		})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"targets": out})
	return nil
}

func ormTargetLabel(t dbx.ORMTarget) string {
	switch t {
	case dbx.ORMPrisma:
		return "Prisma"
	case dbx.ORMDrizzle:
		return "Drizzle"
	case dbx.ORMTypeScript:
		return "TypeScript types"
	case dbx.ORMZod:
		return "Zod schemas"
	}
	return string(t)
}

func ormTargetBlurb(t dbx.ORMTarget) string {
	switch t {
	case dbx.ORMPrisma:
		return "A schema.prisma to drop into an existing Prisma project."
	case dbx.ORMDrizzle:
		return "Drizzle ORM table definitions."
	case dbx.ORMTypeScript:
		return "Plain interfaces — no runtime dependency, useful with any client."
	case dbx.ORMZod:
		return "Runtime validators, plus an insert variant with defaults optional."
	}
	return ""
}

// --- activity -------------------------------------------------------------

// handleDBActivity lists what the server is currently running.
//
// It is on the read side: seeing which query is stuck is diagnosis, and an
// operator who can browse the data can already see everything the query text
// would reveal. Killing one is not — that route is below, in the destructive
// group.
func (s *Server) handleDBActivity(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	pool, conn, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.SkipAudit(r)
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	list, err := dbx.ListActivity(ctx, pool, conn.Driver)
	if err != nil {
		// An engine with no session list is not a broken connection. Saying so
		// in the body lets the UI render an explanation where the table would
		// be, which is the ErrorState convention for a module a host lacks.
		if errors.Is(err, dbx.ErrNoActivityView) {
			httpx.JSON(w, http.StatusOK, map[string]any{
				"sessions": []dbx.Activity{}, "supported": false,
				"reason": err.Error(),
			})
			return nil
		}
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"sessions": list, "supported": true})
	return nil
}

type killRequest struct {
	PID string `json:"pid"`
}

// handleDBKill terminates a session on the database server.
//
// Destructive: whatever the session had done rolls back, and an application
// holding that connection sees it drop. It is still not typed for — see the
// handler body. The pid is validated rather than escaped, which is the guard
// that carries the weight here, because no engine binds a session id.
func (s *Server) handleDBKill(w http.ResponseWriter, r *http.Request) error {
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
	// No typed phrase: nothing is lost that was not already going to roll back,
	// and this button is pressed repeatedly under exactly the time pressure
	// that makes a typing exercise counterproductive. The session id is on the
	// row and in the dialog, which is what makes the right one identifiable.
	pool, conn, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	if err := dbx.KillQuery(ctx, pool, conn.Driver, req.PID); err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "database.session.kill", conn.Name, map[string]any{"pid": req.PID})
	httpx.JSON(w, http.StatusOK, map[string]any{"killed": req.PID})
	return nil
}

// --- global search --------------------------------------------------------

// handleDBSearch looks for a value across every table in a schema.
//
// The work is bounded inside dbx rather than by a caller-supplied limit: the
// bounds are what make the feature safe to offer against a production server,
// and a request parameter that could raise them would be the first thing
// somebody raised.
func (s *Server) handleDBSearch(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	needle := q.Get("q")
	if strings.TrimSpace(needle) == "" {
		return httpx.BadRequest("q is required")
	}
	pool, conn, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	// A search reads the whole schema, so it is worth an audit entry even
	// though it changes nothing: it is the one read that touches every table.
	httpx.SetAudit(r, "database.search", conn.Name, map[string]any{"schema": q.Get("schema")})
	ctx, cancel := timeoutCtx(r, 120*time.Second)
	defer cancel()
	res, err := dbx.Search(ctx, pool, conn.Driver, q.Get("schema"), needle)
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

// --- storage overview -----------------------------------------------------

// handleDBOverview reports per-table size and the pool's state.
func (s *Server) handleDBOverview(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	pool, conn, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.SkipAudit(r)
	ctx, cancel := timeoutCtx(r, 120*time.Second)
	defer cancel()
	res, err := dbx.StorageOverview(ctx, pool, conn.Driver, r.URL.Query().Get("schema"))
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

// --- copy a row as SQL ----------------------------------------------------

type rowSQLRequest struct {
	Schema string           `json:"schema"`
	Table  string           `json:"table"`
	Rows   []map[string]any `json:"rows"`
}

// handleDBRowSQL renders selected rows as INSERT statements.
//
// Rendered on the server for the reason the docker run line is: there is one
// implementation of "what does this row mean in this engine's syntax", and a
// second one in TypeScript would quote a value differently on the day it
// mattered. Nothing here executes the statement — it is text for a clipboard.
func (s *Server) handleDBRowSQL(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req rowSQLRequest
	if err := httpx.DecodeJSONNumbers(r, &req); err != nil {
		return err
	}
	if req.Table == "" {
		return httpx.BadRequest("table is required")
	}
	if len(req.Rows) == 0 {
		return httpx.BadRequest("at least one row is required")
	}
	conn, _, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.SkipAudit(r)
	out, err := dbx.RowsInsertSQL(conn.Driver, req.Schema, req.Table, req.Rows)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"sql": out})
	return nil
}

// driverNames renders the supported engines for a refusal message. Derived
// from dbx.Drivers() rather than written out, because the hand-written list
// this replaces still named four engines long after there were eight.
func driverNames() string {
	names := make([]string, 0, len(dbx.Drivers()))
	for _, d := range dbx.Drivers() {
		names = append(names, string(d))
	}
	return strings.Join(names, ", ")
}

// handleDBGraph returns the whole schema in the shape a diagram needs.
//
// One request rather than the forty the diagram used to make — the table list,
// the relations, and then the columns of each table one at a time. Beyond being
// slow, that was not atomic: what it drew was forty answers from forty moments,
// and a table created halfway through appeared with no columns.
func (s *Server) handleDBGraph(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	id, err := parseID(r)
	if err != nil {
		return err
	}
	pool, conn, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	// Introspecting a large schema is many catalogue queries, so it gets a
	// longer budget than a page of rows and a bound on how much it will do.
	ctx, cancel := timeoutCtx(r, 90*time.Second)
	defer cancel()
	graph, err := dbx.BuildSchemaGraph(ctx, pool, conn.Driver, r.URL.Query().Get("schema"))
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.JSON(w, http.StatusOK, graph)
	return nil
}
