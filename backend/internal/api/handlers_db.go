package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
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
	// What the operator says about the connection rather than what its DSN
	// does: the environment it serves, whether the dashboard may change what
	// is in it, and anything worth remembering.
	Environment string `json:"environment"`
	ReadOnly    bool   `json:"readOnly"`
	Notes       string `json:"notes"`
	// Origin is the key of the server or file on this machine the connection
	// was made from, as discovery lists it, and empty for one typed in by
	// hand.
	Origin string `json:"origin"`
	// Broken marks a row this process cannot use, with the reason: its sealed
	// DSN no longer opens, or its file is outside the file roots. The address
	// fields above are empty on such a row.
	Broken       bool   `json:"broken,omitempty"`
	BrokenReason string `json:"brokenReason,omitempty"`
}

func (s *Server) mountDatabaseRoutes(r chi.Router) {
	r.Route("/databases", func(r chi.Router) {
		r.Use(s.protectReadOnlyConnections)
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
			// And for a server whose password nobody knows: make the account
			// from the host's own shell, where peer authentication lets the
			// engine's system account in without one.
			r.Method(http.MethodPost, "/host/grant", s.handle(s.handleDBHostGrant))
			r.Method(http.MethodPost, "/sync", s.handle(s.handleDBSync))
			r.Method(http.MethodGet, "/provision/options", s.handle(s.handleDBProvisionOptions))
			r.Method(http.MethodPost, "/provision", s.handle(s.handleDBProvision))
			r.Method(http.MethodPut, "/{id}", s.handle(s.handleDBConnUpdate))
			r.Method(http.MethodGet, "/{id}/url", s.handle(s.handleDBConnURL))
			// Where the server is reachable from. Reading it lists containers
			// and the firewall; changing it recreates the container with a
			// different port binding, which is the Docker page's Recreate
			// under another name and sits in the same group.
			r.Method(http.MethodGet, "/{id}/access", s.handle(s.handleDBAccess))
			s.destructive(r, func(r chi.Router) {
				r.Method(http.MethodDelete, "/{id}", s.handle(s.handleDBConnDelete))
				r.Method(http.MethodPut, "/{id}/access", s.handle(s.handleDBAccessUpdate))
			})
		})
		// Read surface: available to any authenticated role, including readonly.
		// The fleet and the topology are every connection at once — the
		// section's landing page and its map of what talks to what.
		r.Method(http.MethodGet, "/fleet", s.handle(s.handleDBFleet))
		r.Method(http.MethodGet, "/topology", s.handle(s.handleDBTopology))
		r.Method(http.MethodGet, "/{id}/consumers", s.handle(s.handleDBConsumers))
		s.mountDatabaseAdminRoutes(r)
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
		// How the diagram of that graph was arranged. Reading it is part of
		// reading the schema; only saving sits with the other writes below.
		r.Method(http.MethodGet, "/{id}/diagram", s.handle(s.handleDBDiagramGet))
		r.Method(http.MethodGet, "/{id}/activity", s.handle(s.handleDBActivity))
		// The server's own log and the statements it recorded. Reading either
		// is reading what the server printed and the statements it ran, which
		// the activity list and /statements already show any role.
		r.Method(http.MethodGet, "/{id}/logs/sources", s.handle(s.handleDBLogSources))
		r.Method(http.MethodGet, "/{id}/querylog", s.handle(s.handleDBQueryLog))
		r.Method(http.MethodGet, "/{id}/search", s.handle(s.handleDBSearch))
		r.Method(http.MethodGet, "/{id}/overview", s.handle(s.handleDBOverview))
		r.Method(http.MethodGet, "/orm/targets", s.handle(s.handleDBTargets))
		r.Method(http.MethodPost, "/{id}/rows/sql", s.handle(s.handleDBRowSQL))
		// Redis and Mongo have their own paths rather than a pretence that a
		// key or a document is a row: the vocabulary is part of what makes each
		// engine legible. Each engine and each working surface mounts its own
		// routes from its own file.
		s.mountDatabaseRedisRoutes(r)
		s.mountDatabaseMongoRoutes(r)
		s.mountDatabaseInventoryRoutes(r)
		s.mountDatabaseConnectionRoutes(r)
		s.mountDatabaseWorkbenchRoutes(r)
		s.mountDatabaseSchemaRoutes(r)
		s.mountDatabaseOpsRoutes(r)
		s.mountDatabaseTransferRoutes(r)
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
			// A saved diagram arrangement is dashboard state, not database state:
			// resetting one loses nothing that a Tidy cannot redraw, so it sits
			// with the saved queries rather than in the destructive group.
			r.Method(http.MethodPut, "/{id}/diagram", s.handle(s.handleDBDiagramPut))
			r.Method(http.MethodDelete, "/{id}/diagram", s.handle(s.handleDBDiagramDelete))
			r.Method(http.MethodPost, "/{id}/import", s.handle(s.handleDBImport))
			// Schema changes that only add: the same capability the query
			// runner needs for the CREATE it classifies as medium risk, so the
			// form and the SQL console agree on who may do this.
			r.Method(http.MethodPost, "/{id}/ddl/table", s.handle(s.handleDDLCreateTable))
			r.Method(http.MethodPost, "/{id}/ddl/column", s.handle(s.handleDDLAddColumn))
			r.Method(http.MethodPost, "/{id}/ddl/index", s.handle(s.handleDDLCreateIndex))
			r.Method(http.MethodPost, "/{id}/ddl/rename", s.handle(s.handleDDLRename))
		})
		// Everything here uses the destructive capability, tighter budget, and audit.
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodDelete, "/{id}/rows", s.handle(s.handleDBRowDelete))
			// Killing a session stops work in flight and rolls it back, so it
			// sits here with the row delete rather than with the read-side
			// activity list it is launched from.
			r.Method(http.MethodPost, "/{id}/activity/kill", s.handle(s.handleDBKill))
			r.Method(http.MethodPost, "/{id}/restore", s.handle(s.handleDBRestore))
			// Schema changes that destroy data or remove an index.
			r.Method(http.MethodDelete, "/{id}/ddl/table", s.handle(s.handleDDLDropTable))
			r.Method(http.MethodDelete, "/{id}/ddl/column", s.handle(s.handleDDLDropColumn))
			r.Method(http.MethodDelete, "/{id}/ddl/index", s.handle(s.handleDDLDropIndex))
			r.Method(http.MethodPost, "/{id}/ddl/truncate", s.handle(s.handleDDLTruncate))
			// Removing the database itself, which is the one thing on this
			// page that cannot be undone by anything except a dump taken
			// first. system.admin on top of the destructive group: creating a
			// database needs it, and so should destroying one.
			r.With(httpx.RequireCapability(auth.CapSystemAdmin)).
				Method(http.MethodDelete, "/{id}/database", s.handle(s.handleDBDropDatabase))
		})
	})
}

// dbConnColumns is a row of db_connections in the order scanDBConn reads it.
const dbConnColumns = `id, name, driver, dsn_enc, created_at, environment, read_only, notes, origin`

// dbConnRecord is a connection as stored: everything but the address, which
// is inside the sealed DSN.
type dbConnRecord struct {
	conn   dbConnection
	dsnEnc string
}

func scanDBConn(scan func(...any) error) (*dbConnRecord, error) {
	var (
		rec      dbConnRecord
		driver   string
		created  int64
		readOnly int
	)
	if err := scan(&rec.conn.ID, &rec.conn.Name, &driver, &rec.dsnEnc, &created,
		&rec.conn.Environment, &readOnly, &rec.conn.Notes, &rec.conn.Origin); err != nil {
		return nil, err
	}
	rec.conn.Driver = dbx.Driver(driver)
	rec.conn.CreatedAt = time.Unix(created, 0).UTC()
	rec.conn.ReadOnly = readOnly != 0
	return &rec, nil
}

// openDBConn unseals a stored connection and reads its address out of the DSN.
func (s *Server) openDBConn(rec *dbConnRecord) (*dbConnection, string, error) {
	dsn, err := s.Sealer.Open(rec.dsnEnc)
	if err != nil {
		return nil, "", httpx.Internal(err)
	}
	conn := rec.conn
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
	return &conn, dsn, nil
}

func (s *Server) dbConnRow(ctx context.Context, id int64) (*dbConnection, string, error) {
	rec, err := scanDBConn(s.Store.DB.QueryRowContext(ctx,
		`SELECT `+dbConnColumns+` FROM db_connections WHERE id = ?`, id).Scan)
	if err == sql.ErrNoRows {
		return nil, "", httpx.ErrNotFound
	}
	if err != nil {
		return nil, "", httpx.Internal(err)
	}
	return s.openDBConn(rec)
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
		return nil, conn, connectFailed(dsn, err)
	}
	return pool, conn, nil
}

func (s *Server) handleDBConnList(w http.ResponseWriter, r *http.Request) error {
	out, err := s.allConnections(r.Context())
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// allConnections is every saved connection by name, the unusable ones
// included.
//
// A row whose DSN would not open used to be left out, so the connection an
// operator came to repair was the one connection the page did not show: it
// vanished from the picker the day the master key or the file roots changed,
// and stayed in the table where nothing could reach it to forget it. It is
// listed now, flagged with why, and its id still answers the routes that fix
// or remove it.
func (s *Server) allConnections(ctx context.Context) ([]*dbConnection, error) {
	rows, err := s.Store.DB.QueryContext(ctx,
		`SELECT `+dbConnColumns+` FROM db_connections ORDER BY name`)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	defer rows.Close()
	out := []*dbConnection{}
	for rows.Next() {
		rec, err := scanDBConn(rows.Scan)
		if err != nil {
			return nil, httpx.Internal(err)
		}
		conn, _, err := s.openDBConn(rec)
		if err != nil {
			broken := rec.conn
			broken.Broken, broken.BrokenReason = true, brokenReason(err)
			conn = &broken
		}
		out = append(out, conn)
	}
	if err := rows.Err(); err != nil {
		return nil, httpx.Internal(err)
	}
	return out, nil
}

// brokenReason says why a stored connection cannot be used, in a sentence the
// page prints. A containment refusal already reads that way. A DSN that will
// not unseal comes back as an internal error whose text is deliberately
// hidden, and the one cause worth naming is the key it was sealed under.
func brokenReason(err error) string {
	var apiErr *httpx.APIError
	if errors.As(err, &apiErr) && apiErr.Status < http.StatusInternalServerError {
		return apiErr.Message
	}
	return "its stored connection string could not be decrypted; JD_MASTER_KEY is not the key it was saved under"
}

// connNameRe deliberately excludes "/" and ".." so a connection name cannot
// walk out of the backup directory it names.
var connNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,63}$`)

type createDBConnRequest struct {
	Name   string     `json:"name"`
	Driver dbx.Driver `json:"driver"`
	DSN    string     `json:"dsn"`
	// Probe dials the server before the connection is saved, and refuses to
	// save one that does not answer.
	Probe       bool   `json:"probe"`
	Environment string `json:"environment"`
	ReadOnly    bool   `json:"readOnly"`
	Notes       string `json:"notes"`
}

// connEnvironmentRe bounds the environment tag the way a name is bounded: it
// is drawn as a chip beside the connection, and compared across connections.
var connEnvironmentRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,31}$`)

// maxConnNotes is the most an operator may write about one connection, in
// bytes.
const maxConnNotes = 4000

// connLabels validates what the operator says about a connection: an
// environment that is empty or a short tag, and notes that are text.
func connLabels(environment, notes string) (string, string, error) {
	environment = strings.TrimSpace(environment)
	if environment != "" && !connEnvironmentRe.MatchString(environment) {
		return "", "", httpx.BadRequest("environment is a short tag of letters, digits, spaces, dots, dashes and underscores")
	}
	if len(notes) > maxConnNotes {
		return "", "", httpx.BadRequest("notes may be at most %d bytes", maxConnNotes)
	}
	if strings.ContainsRune(notes, 0) {
		return "", "", httpx.BadRequest("notes must be text")
	}
	return environment, notes, nil
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
	// The dashboard's own store is never a connection, by any spelling of its
	// path: see refuseOwnStore.
	if err := s.refuseOwnStore(driver, resolved); err != nil {
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
	environment, notes, err := connLabels(req.Environment, req.Notes)
	if err != nil {
		return err
	}
	dsn, err := s.containDSN(req.Driver, req.DSN)
	if err != nil {
		return err
	}
	if req.Probe {
		// Dialled before it is stored, for the reason the host route gives: a
		// connection that cannot answer is a row that looks connected and
		// fails every request made of it afterwards, and the engine's own
		// refusal belongs in the form the operator is still looking at.
		ctx, cancel := timeoutCtx(r, 15*time.Second)
		_, err := dbx.ProbeIdentity(ctx, req.Driver, dsn)
		cancel()
		if err != nil {
			httpx.SetAudit(r, "database.connection.create", req.Name,
				map[string]any{"ok": false, "driver": req.Driver})
			// The refusal is printed in the form and written to the audit
			// trail beside the entry above, so it is the engine's words
			// without the password a driver quotes back in them.
			return httpx.BadRequest("%s", connectError(dsn, err))
		}
	}
	sealed, err := s.Sealer.Seal(dsn)
	if err != nil {
		return httpx.Internal(err)
	}
	res, err := s.Store.DB.ExecContext(r.Context(),
		`INSERT INTO db_connections(name, driver, dsn_enc, created_at, environment, read_only, notes)
		 VALUES(?,?,?,?,?,?,?)`,
		req.Name, string(req.Driver), sealed, time.Now().Unix(), environment, req.ReadOnly, notes)
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
	httpx.SetAudit(r, "database.connection.create", req.Name, map[string]any{
		"driver": req.Driver, "host": conn.Host, "user": conn.User, "probed": req.Probe,
	})
	httpx.JSON(w, http.StatusCreated, conn)
	return nil
}

func (s *Server) handleDBConnDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		return httpx.BadRequest("invalid id")
	}
	// Read as stored rather than opened: a connection whose DSN no longer
	// unseals is exactly the one somebody needs to be able to forget.
	conn, err := s.storedConnection(r.Context(), id)
	if err != nil {
		return err
	}
	// No typed phrase: this forgets a connection string, it does not touch the
	// server at the other end of it. Re-adding one is a form, not a restore.
	forgotten, err := s.forgetConnection(r.Context(), id, httpx.MustPrincipal(r).Username())
	if err != nil {
		return err
	}
	if !forgotten.removed {
		return httpx.Err(http.StatusConflict, "database_linked", "remove the deployment's managed database network before forgetting this linked connection")
	}
	httpx.SetAudit(r, "database.connection.delete", conn.Name, forgotten.audited(nil))
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
			httpx.JSON(w, http.StatusOK, map[string]any{"ok": false, "error": connectError(dsn, err)})
			return nil
		}
		defer client.Disconnect(context.Background())
	case dbx.DriverRedis:
		// In the database the connection string names, which is where every
		// key route goes: a string naming one the server does not have used
		// to ping healthy and then fail each of them.
		client, err := dbx.RedisClient(r.Context(), dsn, dbx.RedisDSNDatabase)
		if err != nil {
			httpx.JSON(w, http.StatusOK, map[string]any{"ok": false, "error": connectError(dsn, err)})
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
			s.dropPoolAfter(id, err)
			httpx.JSON(w, http.StatusOK, map[string]any{"ok": false, "error": connectError(dsn, err)})
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
			return connectFailed(dsn, err)
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
			return connectFailed(dsn, err)
		}
		defer client.Close()
		info, err := dbx.RedisInfo(r.Context(), client)
		if err != nil {
			return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"server": info})
		return nil
	}
	// A SQL engine answers with a snapshot of its own counters, and the
	// pool's beside them where it always was.
	return s.dbSQLStats(w, r, id)
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
			return connectFailed(dsn, err)
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
			return connectFailed(dsn, err)
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
			return connectFailed(dsn, err)
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
			return connectFailed(dsn, err)
		}
		defer client.Disconnect(context.Background())
		res, err := dbx.MongoFind(r.Context(), client, q.Get("schema"), q.Get("table"), q.Get("filter"), limit, offset)
		if err != nil {
			return httpx.BadRequest("%v", err)
		}
		httpx.JSON(w, http.StatusOK, res)
		return nil
	}
	opts, err := browseOptions(q)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	pool, conn, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	opts.Limit, opts.Offset = limit, offset
	// How much of a long text cell a page carries. The rest of a cell that was
	// cut is one GET /cell away, and a page of a thousand rows cannot afford a
	// megabyte in each.
	opts.ClipText = clampInt(atoiDefault(q.Get("cellLimit"), defaultCellLimit), minCellLimit, maxCellLimit)
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	page, err := dbx.BrowseTablePage(ctx, pool, conn.Driver, opts)
	if err != nil {
		return tableReadError(err)
	}
	httpx.JSON(w, http.StatusOK, page)
	return nil
}

// tableReadError answers a read of a table that the engine, or the dashboard
// on its behalf, would not make. The connection was open by then, so what went
// wrong is the request's: a filter the engine has no operator for, a column
// that is not there, a value that is not of the column's type — in the
// engine's own words. The page, the count, the cell and the export answer
// through it, so the grid that asks two of them about the same rows is not
// told by one that its filter is wrong and by the other that the server is.
func tableReadError(err error) error {
	if errors.Is(err, dbx.ErrCredentialsWithheld) {
		return httpx.Err(http.StatusForbidden, "credentials_withheld", err.Error())
	}
	return httpx.BadRequest("%v", err)
}

const (
	defaultCellLimit = 4 << 10
	minCellLimit     = 256
	maxCellLimit     = 64 << 10
)

func clampInt(n, low, high int) int {
	switch {
	case n < low:
		return low
	case n > high:
		return high
	}
	return n
}

// browseOptions reads what a grid request says about which rows it wants: the
// table, the conditions and how they combine, the order, and the columns.
//
// One function for the page, the count and the export, because those three
// answering different questions about "the rows I am looking at" is the bug
// the shared selection in dbx exists to prevent, and it would come straight
// back if each handler read the query string its own way.
func browseOptions(q url.Values) (dbx.BrowseOptions, error) {
	opts := dbx.BrowseOptions{Schema: q.Get("schema"), Table: q.Get("table")}
	var err error
	if opts.Filters, err = parseFilters(q.Get("filters")); err != nil {
		return opts, err
	}
	switch q.Get("match") {
	case "", "all":
	case "any":
		opts.MatchAny = true
	default:
		return opts, fmt.Errorf("match must be all or any")
	}
	if opts.Sort, err = parseSort(q.Get("sort"), q.Get("orderBy"), q.Get("dir")); err != nil {
		return opts, err
	}
	if opts.Columns, err = parseColumns(q.Get("columns")); err != nil {
		return opts, err
	}
	return opts, nil
}

// parseSort reads the order. Three spellings, most exact first: `sort` is a
// JSON array of {column, desc}, which can name any column; `orderBy=a:asc,b:desc`
// is the compact form; and `orderBy=a&dir=desc` is the single column the grid
// has always sent, which is also what any value without a direction on every
// part is read as — a column may legitimately contain a comma or a colon, and
// guessing otherwise would sort by a column that does not exist.
func parseSort(raw, orderBy, dir string) ([]dbx.SortKey, error) {
	if strings.TrimSpace(raw) != "" {
		var keys []dbx.SortKey
		if err := json.Unmarshal([]byte(raw), &keys); err != nil {
			return nil, fmt.Errorf("sort must be a JSON array of {column, desc}: %v", err)
		}
		if len(keys) > dbx.MaxBrowseColumns {
			return nil, fmt.Errorf("too many sort columns")
		}
		return keys, nil
	}
	if orderBy == "" {
		return nil, nil
	}
	parts := strings.Split(orderBy, ",")
	keys := make([]dbx.SortKey, 0, len(parts))
	for _, part := range parts {
		i := strings.LastIndexByte(part, ':')
		if i <= 0 {
			keys = nil
			break
		}
		switch strings.ToLower(part[i+1:]) {
		case "asc":
			keys = append(keys, dbx.SortKey{Column: part[:i]})
		case "desc":
			keys = append(keys, dbx.SortKey{Column: part[:i], Desc: true})
		default:
			keys = nil
		}
		if keys == nil {
			break
		}
	}
	if keys != nil {
		return keys, nil
	}
	return []dbx.SortKey{{Column: orderBy, Desc: dir == "desc"}}, nil
}

// parseColumns reads a projection: a JSON array of names, or a comma-separated
// list for names that hold no comma.
func parseColumns(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if strings.HasPrefix(raw, "[") {
		var cols []string
		if err := json.Unmarshal([]byte(raw), &cols); err != nil {
			return nil, fmt.Errorf("columns must be a JSON array of names: %v", err)
		}
		return cols, nil
	}
	return strings.Split(raw, ","), nil
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
	// QueryID is a name the client chose for this run, so it can ask for the
	// run to be stopped while the request is still open.
	QueryID string `json:"queryId"`
}

// classifyResponse is the verdict on everything in the editor, and on each
// statement of it, so a script can be marked up line by line.
type classifyResponse struct {
	dbx.Risk
	Statements []dbx.SQLStatement `json:"statements"`
}

// handleDBClassify lets the editor warn before anything is sent. It is a pure
// analysis of the text for the connection's engine and touches no database.
func (s *Server) handleDBClassify(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req queryRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	httpx.SkipAudit(r)
	// The engine decides how the text is read — a backtick is a quote on one
	// and an operator on another — so the connection is looked up, not dialled.
	conn, err := s.sqlConnection(r, id)
	if err != nil {
		return err
	}
	statements, err := dbx.ParseScript(conn.Driver, req.Query)
	if err != nil {
		// Unreadable is destructive, here as in the runner: the editor must
		// show the same verdict the run would be held to.
		httpx.JSON(w, http.StatusOK, classifyResponse{
			Risk:       dbx.ClassifyFor(conn.Driver, req.Query),
			Statements: []dbx.SQLStatement{},
		})
		return nil
	}
	if statements == nil {
		statements = []dbx.SQLStatement{}
	}
	httpx.JSON(w, http.StatusOK, classifyResponse{Risk: dbx.WorstRisk(statements), Statements: statements})
	return nil
}

// authoriseSQL turns a verdict on operator-written SQL into a permission. It
// is the one place that does: the query route, the script route and an
// executing plan all come through here, so they cannot disagree about what a
// destructive statement costs.
func (s *Server) authoriseSQL(r *http.Request, risk dbx.Risk) error {
	if !risk.Destructive {
		return nil
	}
	p := httpx.MustPrincipal(r)
	if !p.Can(auth.CapDestructive) {
		return httpx.Err(http.StatusForbidden, "forbidden",
			"this statement is destructive and your role does not permit it")
	}
	if !s.destrLim.Allow(p.Username() + "|dbquery") {
		return httpx.Err(http.StatusTooManyRequests, "rate_limited",
			"too many destructive statements, slow down")
	}
	return nil
}

// sqlConnection returns a connection's record for a route that reads its SQL
// before dialling, refusing the engines that have none.
func (s *Server) sqlConnection(r *http.Request, id int64) (*dbConnection, error) {
	conn, _, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return nil, err
	}
	if !conn.Driver.IsSQL() {
		return nil, httpx.BadRequest("this endpoint is for SQL engines; %s uses its own surface", conn.Driver)
	}
	return conn, nil
}

// handleDBQuery runs arbitrary SQL. A destructive statement additionally
// requires the destructive capability and the tighter budget.
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
	conn, err := s.sqlConnection(r, id)
	if err != nil {
		return err
	}
	statement, err := dbx.SingleStatementFor(conn.Driver, req.Query)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	risk := statement.Risk
	if err := s.authoriseSQL(r, risk); err != nil {
		return err
	}
	pool, _, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 120*time.Second)
	defer cancel()
	ctx, run, err := s.trackRun(ctx, r, id, req.QueryID)
	if err != nil {
		return err
	}
	defer run.release()
	start := time.Now()
	res, err := dbx.RunStatement(ctx, pool, conn.Driver, statement, req.MaxRows)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		err = run.explain(err)
		s.recordDBHistory(r.Context(), id, dbHistoryEntry{
			SQL: statement.SQL, Risk: risk.Level, DurationMs: elapsed, Error: err.Error(),
		})
		httpx.SetAudit(r, "database.query", strconv.FormatInt(id, 10),
			map[string]any{"risk": risk.Level, "error": err.Error(), "statement": statement.SQL})
		return err
	}
	s.recordDBHistory(r.Context(), id, dbHistoryEntry{
		SQL: statement.SQL, Risk: risk.Level, Success: true, DurationMs: elapsed,
		RowCount: res.RowCount, RowsAffected: res.Affected,
	})
	httpx.SetAudit(r, "database.query", strconv.FormatInt(id, 10), map[string]any{
		"risk": risk.Level, "destructive": risk.Destructive,
		"rowsAffected": res.Affected, "rowCount": res.RowCount, "statement": statement.SQL,
	})
	httpx.JSON(w, http.StatusOK, map[string]any{"result": res, "risk": risk})
	return nil
}

// handleDBBackup starts a dump of the connection's database as a job and
// answers at once with the job (handlers_db_transfer.go).
func (s *Server) handleDBBackup(w http.ResponseWriter, r *http.Request) error {
	return s.startDBBackup(w, r)
}

// handleDBRestore starts a restore as a job. It replaces live data, so it
// sits in the destructive group; like every restore it takes an ordinary
// confirmation and no typed phrase.
func (s *Server) handleDBRestore(w http.ResponseWriter, r *http.Request) error {
	return s.startDBRestore(w, r)
}

// handleDBConnTest verifies a DSN before it is saved. It reports what answered
// on success — the product and its version — so the operator can see they
// reached the engine they meant to, and never persists anything.
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
	dsn, err := s.containDSN(req.Driver, req.DSN)
	if err != nil {
		return err
	}
	httpx.SkipAudit(r)
	ctx, cancel := timeoutCtx(r, 15*time.Second)
	defer cancel()
	// One probe for every engine. Redis used to fall through to the SQL path
	// and be reported unreachable for having no dialect, which made the test
	// button fail for a server the save button then connected to.
	identity, err := dbx.ProbeIdentity(ctx, req.Driver, dsn)
	if err != nil {
		httpx.JSON(w, http.StatusOK, map[string]any{"ok": false, "error": connectError(dsn, err)})
		return nil
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"ok": true, "version": identity.Version, "versionNumber": identity.Number,
		"flavor": identity.Flavor, "flavorLabel": dbx.FlavorLabel(identity.Flavor),
	})
	return nil
}

// updateDBConnRequest changes what is named in it and nothing else: a field
// left out keeps its stored value, which is why the three labels are pointers
// — an absent "notes" is not a request to erase them.
type updateDBConnRequest struct {
	Name        string  `json:"name"`
	DSN         string  `json:"dsn"`
	Environment *string `json:"environment"`
	ReadOnly    *bool   `json:"readOnly"`
	Notes       *string `json:"notes"`
}

// handleDBConnUpdate edits a connection's name, its labels and, optionally,
// its DSN. The driver is fixed at creation — changing it would mean the stored
// secret no longer parses — so it is not editable here. An empty DSN leaves
// the existing one untouched, so an operator can rename without re-typing a
// password they cannot read back.
//
// It reads the row as stored rather than opening it, because this is the one
// route that repairs a broken connection: a DSN that no longer unseals is
// replaced here, and a handler that had to open it first could never run.
func (s *Server) handleDBConnUpdate(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	conn, err := s.storedConnection(r.Context(), id)
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
	// Only a name that is being changed is held to the rule. A connection
	// saved under an older rule keeps its name through an edit that does not
	// touch it, rather than refusing every change until it is renamed.
	if name != conn.Name && !connNameRe.MatchString(name) {
		return httpx.BadRequest("name may contain letters, digits, spaces, dots, dashes and underscores")
	}
	environment, notes := conn.Environment, conn.Notes
	if req.Environment != nil {
		environment = *req.Environment
	}
	if req.Notes != nil {
		notes = *req.Notes
	}
	environment, notes, err = connLabels(environment, notes)
	if err != nil {
		return err
	}
	readOnly := conn.ReadOnly
	if req.ReadOnly != nil {
		readOnly = *req.ReadOnly
	}
	set := []string{"name = ?", "environment = ?", "read_only = ?", "notes = ?"}
	args := []any{name, environment, readOnly, notes}
	if req.DSN != "" {
		dsn, err := s.containDSN(conn.Driver, req.DSN)
		if err != nil {
			return err
		}
		sealed, err := s.Sealer.Seal(dsn)
		if err != nil {
			return httpx.Internal(err)
		}
		set, args = append(set, "dsn_enc = ?"), append(args, sealed)
	}
	if _, err := s.Store.DB.ExecContext(r.Context(),
		`UPDATE db_connections SET `+strings.Join(set, ", ")+` WHERE id = ?`, append(args, id)...); err != nil {
		return httpx.BadRequest("could not update connection: %v", err)
	}
	if req.DSN != "" {
		// The pool was opened against the old DSN; drop it so the next request
		// dials the new one, and with it what the old server said it was.
		s.modules.dbs.Close(id)
		s.dbConns.forget(id)
	}
	updated, err := s.storedConnection(r.Context(), id)
	if err != nil {
		return err
	}
	detail := map[string]any{"dsnChanged": req.DSN != ""}
	// Protection is the one label whose change is worth reading back out of
	// the trail: it is what stood between this connection and a write.
	if readOnly != conn.ReadOnly {
		detail["readOnly"] = readOnly
	}
	if environment != conn.Environment {
		detail["environment"] = environment
	}
	if notes != conn.Notes {
		detail["notesChanged"] = true
	}
	httpx.SetAudit(r, "database.connection.update", name, detail)
	httpx.JSON(w, http.StatusOK, updated)
	return nil
}

// handleDBTableDetail returns a table's structure: columns, primary key,
// indexes, constraints, foreign keys in both directions and the DDL that
// would recreate it.
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
	detail, err := dbx.DescribeTable(ctx, pool, conn.Driver, q.Get("schema"), q.Get("table"))
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	// No columns and no catalogue entry is a table that is not there. It used
	// to come back as an empty structure, which a page drew as a table with
	// nothing in it.
	if len(detail.Columns) == 0 && detail.Type == "" {
		return httpx.Err(http.StatusNotFound, "not_found",
			fmt.Sprintf("no table or view named %s", q.Get("table")))
	}
	httpx.JSON(w, http.StatusOK, detail)
	return nil
}

// handleDBExport streams a table, or the rows of it the grid is showing, as a
// file. It is a read, so it needs no capability beyond the browse routes; the
// row cap is high, and a download that was cut at it or failed partway says so
// rather than passing for the whole table (handlers_db_transfer.go).
func (s *Server) handleDBExport(w http.ResponseWriter, r *http.Request) error {
	return s.exportTable(w, r)
}

// handleDBGenerateORM introspects the connection and returns generated code
// for one target: an ORM schema, a set of types, or the schema as SQL. It is
// a read of the schema catalogue, so it needs no write capability.
//
// The target and its options are checked before the connection is opened, and
// whether the target exists for this engine at all before anything is read
// from it: a Prisma schema for a ClickHouse server is refused with the reason
// rather than answered with something PostgreSQL-shaped.
func (s *Server) handleDBGenerateORM(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req dbx.ORMRequest
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
	opts, err := req.Options()
	if err != nil {
		return httpx.BadRequest("%s", dbx.ORMRequestMessage(err))
	}
	row, _, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	// An engine with no SQL at all is left to dbPool below, which names the
	// surface it does have.
	if row.Driver.IsSQL() {
		if reason := dbx.ORMUnsupported(req.Target, row.Driver); reason != "" {
			return httpx.BadRequest("%s", reason)
		}
	}
	pool, conn, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	scope, err := req.Scope(conn.Driver, conn.Database)
	if err != nil {
		return httpx.BadRequest("%s", dbx.ORMRequestMessage(err))
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	schema, err := dbx.LoadORMSchema(ctx, pool, conn.Driver, scope)
	if errors.Is(err, dbx.ErrORMRequest) {
		return httpx.BadRequest("%s", dbx.ORMRequestMessage(err))
	}
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	res, err := dbx.GenerateORMFiles(schema, opts)
	if err != nil {
		return httpx.BadRequest("%s", dbx.ORMRequestMessage(err))
	}
	httpx.SetAudit(r, "database.orm.generate", conn.Name, map[string]any{
		"target": string(req.Target), "schemas": scope.Schemas,
		"tables": res.Counts.Tables, "views": res.Counts.Views, "warnings": len(res.Warnings),
	})
	httpx.JSON(w, http.StatusOK, res)
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
		return changeError(err)
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
		return changeError(err)
	}
	// The key's columns, not its values: a key is row data like any other, and
	// row data does not belong in the audit log.
	httpx.SetAudit(r, "database.row.update", conn.Name,
		map[string]any{"table": req.Table, "key": keyColumns(req.Key)})
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
		return changeError(err)
	}
	httpx.SetAudit(r, "database.row.delete", conn.Name,
		map[string]any{"table": req.Table, "key": keyColumns(req.Key)})
	httpx.JSON(w, http.StatusOK, map[string]any{"result": res})
	return nil
}

// --- Saved queries and history ------------------------------------------

type savedQuery struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	SQL       string    `json:"sql"`
	CreatedAt time.Time `json:"createdAt"`
	// UpdatedAt is when the query was last renamed or edited; the creation
	// time until then.
	UpdatedAt time.Time `json:"updatedAt"`
}

func (s *Server) handleDBSavedList(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	rows, err := s.Store.DB.QueryContext(r.Context(),
		`SELECT id, name, sql, created_at, updated_at FROM db_saved_queries WHERE connection_id = ? ORDER BY name`, id)
	if err != nil {
		return httpx.Internal(err)
	}
	defer rows.Close()
	out := []savedQuery{}
	for rows.Next() {
		var q savedQuery
		var created, updated int64
		if err := rows.Scan(&q.ID, &q.Name, &q.SQL, &created, &updated); err != nil {
			return httpx.Internal(err)
		}
		q.setTimes(created, updated)
		out = append(out, q)
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

func (q *savedQuery) setTimes(created, updated int64) {
	q.CreatedAt = time.Unix(created, 0).UTC()
	q.UpdatedAt = q.CreatedAt
	// 0 is a query saved before edits were recorded, or never edited.
	if updated > 0 {
		q.UpdatedAt = time.Unix(updated, 0).UTC()
	}
}

type savedQueryRequest struct {
	Name string `json:"name"`
	SQL  string `json:"sql"`
}

// maxSavedQueryName keeps a saved query's name a name. The statement itself is
// bounded by the request body.
const maxSavedQueryName = 200

func (req *savedQueryRequest) validate() error {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || strings.TrimSpace(req.SQL) == "" {
		return httpx.BadRequest("name and sql are required")
	}
	if len(req.Name) > maxSavedQueryName {
		return httpx.BadRequest("a saved query's name may be at most %d characters", maxSavedQueryName)
	}
	return nil
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
	if err := req.validate(); err != nil {
		return err
	}
	if _, _, err := s.dbConnRow(r.Context(), id); err != nil {
		return err
	}
	now := time.Now()
	res, err := s.Store.DB.ExecContext(r.Context(),
		`INSERT INTO db_saved_queries(connection_id, name, sql, created_at, updated_at) VALUES(?,?,?,?,?)`,
		id, req.Name, req.SQL, now.Unix(), now.Unix())
	if err != nil {
		return httpx.BadRequest("could not save query: %v", err)
	}
	newID, _ := res.LastInsertId()
	httpx.SetAudit(r, "database.query.save", req.Name, nil)
	saved := savedQuery{ID: newID, Name: req.Name, SQL: req.SQL}
	saved.setTimes(now.Unix(), now.Unix())
	httpx.JSON(w, http.StatusCreated, saved)
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
	ID           int64     `json:"id"`
	SQL          string    `json:"sql"`
	Risk         string    `json:"risk"`
	Success      bool      `json:"success"`
	Duration     int64     `json:"durationMs"`
	RowCount     int       `json:"rowCount"`
	RowsAffected int64     `json:"rowsAffected"`
	Error        string    `json:"error,omitempty"`
	RanAt        time.Time `json:"ranAt"`
}

func (s *Server) handleDBHistory(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	limit := atoiDefault(r.URL.Query().Get("limit"), 50)
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, 200)
	// ran_at is whole seconds, and a script records several statements in one;
	// the id breaks the tie in the order they ran.
	rows, err := s.Store.DB.QueryContext(r.Context(),
		`SELECT id, sql, risk, success, duration_ms, row_count, rows_affected, error, ran_at
		 FROM db_query_history WHERE connection_id = ? ORDER BY ran_at DESC, id DESC LIMIT ?`, id, limit)
	if err != nil {
		return httpx.Internal(err)
	}
	defer rows.Close()
	out := []historyEntry{}
	for rows.Next() {
		var e historyEntry
		var success, ranAt int64
		if err := rows.Scan(&e.ID, &e.SQL, &e.Risk, &success, &e.Duration, &e.RowCount,
			&e.RowsAffected, &e.Error, &ranAt); err != nil {
			return httpx.Internal(err)
		}
		e.Success = success != 0
		e.RanAt = time.Unix(ranAt, 0).UTC()
		out = append(out, e)
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// dbHistoryEntry is one statement as the history remembers it.
type dbHistoryEntry struct {
	SQL          string
	Risk         string
	Success      bool
	DurationMs   int64
	RowCount     int
	RowsAffected int64
	Error        string
}

// maxHistoryError bounds the engine's message as it is kept. An error is one
// line of explanation in a list, and some engines answer with the statement
// repeated inside it.
const maxHistoryError = 1000

// recordDBHistory appends a statement to the per-connection history and prunes
// it to the most recent entries. It never fails the request it records: a
// history write that errors is logged and swallowed, because losing an audit
// convenience must not turn a successful query into a failed one.
func (s *Server) recordDBHistory(ctx context.Context, connID int64, e dbHistoryEntry) {
	// The request's context may be the very thing that ended the statement —
	// a cancelled query is exactly the one worth finding in the history.
	ctx = context.WithoutCancel(ctx)
	succ := 0
	if e.Success {
		succ = 1
	}
	if len(e.Error) > maxHistoryError {
		e.Error = strings.ToValidUTF8(e.Error[:maxHistoryError], "") + "…"
	}
	if _, err := s.Store.DB.ExecContext(ctx,
		`INSERT INTO db_query_history(connection_id, sql, risk, success, duration_ms, row_count, rows_affected, error, ran_at)
		 VALUES(?,?,?,?,?,?,?,?,?)`,
		connID, e.SQL, e.Risk, succ, e.DurationMs, e.RowCount, e.RowsAffected, e.Error, time.Now().Unix()); err != nil {
		s.Log.Warn("db history write failed", "err", err)
		return
	}
	// Keep only the newest 100 statements per connection. Pruning on write means
	// no separate reaper and no unbounded growth.
	_, _ = s.Store.DB.ExecContext(ctx,
		`DELETE FROM db_query_history WHERE connection_id = ? AND id NOT IN (
		    SELECT id FROM db_query_history WHERE connection_id = ? ORDER BY ran_at DESC, id DESC LIMIT 100
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
	// DefaultPort is what the engine listens on when nothing says otherwise;
	// 0 for an engine that is a file. DSNExample is a connection string in
	// the form this engine's driver takes.
	DefaultPort int    `json:"defaultPort"`
	DSNExample  string `json:"dsnExample"`
	// Capabilities is every feature flag for the driver's own product, and
	// Flavors the same reading for each product that speaks its protocol,
	// the driver's own first.
	Capabilities map[string]any `json:"capabilities"`
	Flavors      []flavorInfo   `json:"flavors"`
}

// flavorInfo is one product a driver can turn out to be connected to.
type flavorInfo struct {
	ID           string         `json:"id"`
	Label        string         `json:"label"`
	Capabilities map[string]any `json:"capabilities"`
}

// containerNameRe and dbNameRe bound the two names a provision request can
// choose. Both become arguments to something else — a Docker container name and
// a CREATE DATABASE the image runs at first boot — so neither is taken verbatim.
var (
	containerNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$`)
	dbNameRe        = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)
)

var driverLabels = map[dbx.Driver]struct {
	label, kind, placeholder string
	port                     int
}{
	dbx.DriverPostgres:   {"PostgreSQL", "sql", "postgres://user:password@127.0.0.1:5432/dbname?sslmode=disable", 5432},
	dbx.DriverMySQL:      {"MySQL / MariaDB", "sql", "user:password@tcp(127.0.0.1:3306)/dbname", 3306},
	dbx.DriverSQLite:     {"SQLite", "sql", "/var/lib/myapp/data.db", 0},
	dbx.DriverMSSQL:      {"SQL Server", "sql", "sqlserver://user:password@127.0.0.1:1433?database=dbname", 1433},
	dbx.DriverClickHouse: {"ClickHouse", "sql", "clickhouse://user:password@127.0.0.1:9000/default", 9000},
	dbx.DriverOracle:     {"Oracle", "sql", "oracle://user:password@127.0.0.1:1521/ORCLPDB1", 1521},
	dbx.DriverMongo:      {"MongoDB", "document", "mongodb://user:password@127.0.0.1:27017/dbname", 27017},
	dbx.DriverRedis:      {"Redis", "keyvalue", "redis://:password@127.0.0.1:6379/0", 6379},
}

func (s *Server) handleDBDrivers(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	out := []driverInfo{}
	for _, d := range dbx.Drivers() {
		meta := driverLabels[d]
		info := driverInfo{
			ID: d, Label: meta.label, Kind: meta.kind,
			Placeholder: meta.placeholder, SQL: d.IsSQL(),
			DefaultPort: meta.port, DSNExample: meta.placeholder,
			Capabilities: dbx.Capabilities(d, ""),
			Flavors:      []flavorInfo{},
		}
		if dl, err := dbx.DialectFor(d); err == nil {
			info.DDL = dl.SupportsDDL()
			info.ColumnTypes = dl.ColumnTypes()
			info.FilterOps = dbx.FilterOpsFor(d)
		}
		for _, flavor := range dbx.Flavors(d) {
			info.Flavors = append(info.Flavors, flavorInfo{
				ID: flavor, Label: dbx.FlavorLabel(flavor), Capabilities: dbx.Capabilities(d, flavor),
			})
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
	opts, err := browseOptions(r.URL.Query())
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	ctx, cancel := timeoutCtx(r, 120*time.Second)
	defer cancel()
	n, err := dbx.Count(ctx, pool, conn.Driver, opts)
	if err != nil {
		return tableReadError(err)
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
	q := r.URL.Query()
	outline, err := dbx.OutlineWithLimit(ctx, pool, conn.Driver, q.Get("schema"), atoiDefault(q.Get("limit"), 0))
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	httpx.JSON(w, http.StatusOK, outline)
	return nil
}

// handleDBRelations returns every foreign key in a schema, keyed by the
// schema-qualified name of the table that holds it (dbx.TableKey).
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

type explainRequest struct {
	Query string `json:"query"`
	// MaxRows is accepted because the editor sends one request shape to run
	// and to explain; a plan is never cut to it.
	MaxRows int `json:"maxRows"`
	// Analyze runs the statement and reports what happened.
	Analyze bool `json:"analyze"`
	// Format is "text" or "json".
	Format string `json:"format"`
}

// handleDBExplain returns the engine's plan for a statement.
//
// It is on the read side of the route map, with no capability beyond browsing,
// because planning is not running: every dialect's implementation describes the
// statement without executing it. That is the property the whole feature rests
// on — a "show me the plan" button that quietly executed a DELETE would be the
// worst control in the product — so it is asserted in the dialect contract and
// tested against every live engine rather than assumed here.
//
// analyze is the exception, and it is not on the read side at all: it executes
// the statement, so the handler asks of it exactly what running the statement
// through the query route would be asked — the capability to run SQL, and for
// a destructive statement the destructive capability and its budget. The plan
// of a data-changing statement is taken inside a transaction that is rolled
// back, which changes what is left behind and nothing about who may ask.
func (s *Server) handleDBExplain(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req explainRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if strings.TrimSpace(req.Query) == "" {
		return httpx.BadRequest("query is required")
	}
	conn, err := s.sqlConnection(r, id)
	if err != nil {
		return err
	}
	statement, err := dbx.ExplainTarget(conn.Driver, req.Query)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	if req.Analyze {
		if !httpx.MustPrincipal(r).Can(auth.CapServiceControl) {
			return httpx.Err(http.StatusForbidden, "forbidden",
				"an analysed plan runs the statement, and your role does not permit running SQL")
		}
		if err := s.authoriseSQL(r, statement.Risk); err != nil {
			return err
		}
	}
	pool, _, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	plan, err := dbx.Explain(ctx, pool, conn.Driver, statement, dbx.ExplainOptions{
		Analyze: req.Analyze, Format: req.Format,
	})
	if err != nil {
		if req.Analyze {
			httpx.SetAudit(r, "database.explain", conn.Name, map[string]any{
				"statement": statement.SQL, "analyze": true, "risk": statement.Risk.Level, "error": err.Error(),
			})
		}
		if errors.Is(err, dbx.ErrExplainUnsupported) {
			return httpx.Err(http.StatusBadRequest, "unsupported", err.Error())
		}
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "database.explain", conn.Name, map[string]any{
		"statement": statement.SQL, "analyze": req.Analyze, "format": plan.Format,
		"risk": statement.Risk.Level, "rolledBack": plan.RolledBack,
	})
	httpx.JSON(w, http.StatusOK, plan)
	return nil
}

// handleDBTargets lists the code generators this build offers, so the ORM tab
// is populated from the server rather than from a second list in TypeScript
// that drifts the first time a generator is added. Each one comes with the
// engines it can be pointed at and the switches it takes, for the same reason.
func (s *Server) handleDBTargets(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	httpx.JSON(w, http.StatusOK, map[string]any{"targets": dbx.ORMTargetCatalogue()})
	return nil
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
	// Written directly, because a GET never reaches the mutation middleware's
	// record. The needle is left out: it is as likely to be somebody's email
	// address as a word.
	s.recordRead(r, "database.search", conn.Name, map[string]any{"schema": q.Get("schema")}, nil)
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
	q := r.URL.Query()
	graph, err := dbx.BuildSchemaGraphWithLimit(ctx, pool, conn.Driver, q.Get("schema"), atoiDefault(q.Get("limit"), 0))
	if err != nil {
		// A catalogue the engine would not read is the engine failing, not the
		// request being wrong, and is reported the way its sibling reads are.
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	httpx.JSON(w, http.StatusOK, graph)
	return nil
}
