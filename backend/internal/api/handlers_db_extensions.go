package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// A database's extensions: the list, with what the catalogue knows of each and
// what the server loads at start; one extension's detail, which is what a
// dialog reads before it acts; creating, updating and dropping one; and adding
// a library to shared_preload_libraries for the extensions that need it.
//
// The three changes to an extension end in runDDL, so each can be shown with
// `?preview=1` before it runs, and what the dialog showed is the text that
// runs and is audited. The preload is the same shape with the list it leaves
// beside its statements. Restarting the server, the step between a preload and
// the create it was for, is the power route's, with that route's capability
// and confirmation: nothing here restarts anything.

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
		return queryFailed(err)
	}
	// Whether they can be changed from here: Postgres yes, MySQL's plugins no.
	_, editable := dbx.ExtensionManagerFor(conn.Driver)
	reply := map[string]any{"extensions": list, "supported": true, "editable": editable}
	if preloader, ok := dbx.PreloaderFor(conn.Driver); ok {
		state, err := preloader.ReadPreload(ctx, pool)
		if err != nil {
			return queryFailed(err)
		}
		reply["preload"] = state
	}
	httpx.JSON(w, http.StatusOK, reply)
	return nil
}

// extensionManager is the engine's extension manager, or the refusal of an
// engine whose extensions are not changed from a session, in words the page
// prints: MySQL lists its plugins and loads them from its configuration, and
// the rest have none.
func (s *Server) extensionManager(r *http.Request, conn *dbConnection) (dbx.ExtensionManager, error) {
	manager, ok := dbx.ExtensionManagerFor(conn.Driver)
	if ok {
		return manager, nil
	}
	if dbx.Capable(conn.Driver, "", "extensions") {
		return nil, ddlInvalid(r, "%s extensions are installed from the server's own configuration", conn.Driver)
	}
	return nil, ddlInvalid(r, "%s has no extensions", conn.Driver)
}

// extensionConn is ddlDecode for the routes that name the extension in the
// path and carry no body.
func (s *Server) extensionConn(r *http.Request) (*dbConnection, error) {
	stopped := func(err error) (*dbConnection, error) {
		if ddlPreview(r) {
			httpx.SkipAudit(r)
		}
		return nil, err
	}
	id, err := parseID(r)
	if err != nil {
		return stopped(err)
	}
	conn, _, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return stopped(err)
	}
	if !conn.Driver.IsSQL() {
		return stopped(httpx.BadRequest("%s has no extensions", conn.Driver))
	}
	return conn, nil
}

// handleDBExtensionDetail is what a dialog needs before it creates, updates
// or drops one extension: the versions the server ships, where it can go,
// what it can be updated to, what it added and what outside it is built on it.
func (s *Server) handleDBExtensionDetail(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	conn, err := s.extensionConn(r)
	if err != nil {
		return err
	}
	manager, err := s.extensionManager(r, conn)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	pool, _, err := s.dbPool(ctx, conn.ID)
	if err != nil {
		return err
	}
	detail, err := manager.ExtensionDetail(ctx, pool, httpx.URLParam(r, "name"))
	if errors.Is(err, dbx.ErrPlanRead) {
		return queryFailed(err)
	}
	if err != nil {
		return httpx.Err(http.StatusNotFound, "not_found", err.Error())
	}
	httpx.JSON(w, http.StatusOK, detail)
	return nil
}

type extensionRequest struct {
	Name string `json:"name"`
	// Schema and Version empty leave the choice to the server.
	Schema  string `json:"schema"`
	Version string `json:"version"`
	// Cascade creates the extensions it requires that are not there yet.
	Cascade bool `json:"cascade"`
}

func (s *Server) handleDBExtensionCreate(w http.ResponseWriter, r *http.Request) error {
	var req extensionRequest
	conn, err := s.ddlDecode(r, &req)
	if err != nil {
		return err
	}
	manager, err := s.extensionManager(r, conn)
	if err != nil {
		return err
	}
	spec := dbx.ExtensionSpec{Name: strings.TrimSpace(req.Name), Schema: strings.TrimSpace(req.Schema),
		Version: strings.TrimSpace(req.Version), Cascade: req.Cascade}
	if spec.Name == "" {
		return ddlInvalid(r, "an extension name is required")
	}
	detail := map[string]any{"extension": spec.Name, "cascade": spec.Cascade}
	if spec.Schema != "" {
		detail["schema"] = spec.Schema
	}
	if spec.Version != "" {
		detail["version"] = spec.Version
	}
	return s.runDDL(w, r, conn, 60*time.Second, "database.extension.create", detail, "",
		func(ctx context.Context, pool *sql.DB) (*dbx.DDLPlan, error) {
			return manager.PlanCreateExtension(ctx, pool, spec)
		})
}

type extensionUpdateRequest struct {
	// Version empty is the server's default version.
	Version string `json:"version"`
}

// handleDBExtensionUpdate runs ALTER EXTENSION … UPDATE, to the version named
// or to the server's default, along a path the server ships.
func (s *Server) handleDBExtensionUpdate(w http.ResponseWriter, r *http.Request) error {
	var req extensionUpdateRequest
	conn, err := s.ddlDecode(r, &req)
	if err != nil {
		return err
	}
	manager, err := s.extensionManager(r, conn)
	if err != nil {
		return err
	}
	name := httpx.URLParam(r, "name")
	version := strings.TrimSpace(req.Version)
	detail := map[string]any{"extension": name}
	if version != "" {
		detail["version"] = version
	}
	return s.runDDL(w, r, conn, 120*time.Second, "database.extension.update", detail, "",
		func(ctx context.Context, pool *sql.DB) (*dbx.DDLPlan, error) {
			return manager.PlanUpdateExtension(ctx, pool, name, version)
		})
}

// handleDBExtensionDrop drops an extension, and with `?cascade=1` everything
// outside it that is built on it. The detail route lists that first; the
// engine refuses a drop without CASCADE while anything depends on it, so the
// choice is the reader's and never inferred.
func (s *Server) handleDBExtensionDrop(w http.ResponseWriter, r *http.Request) error {
	conn, err := s.extensionConn(r)
	if err != nil {
		return err
	}
	manager, err := s.extensionManager(r, conn)
	if err != nil {
		return err
	}
	name := httpx.URLParam(r, "name")
	cascade := queryFlag(r, "cascade")
	return s.runDDL(w, r, conn, 120*time.Second, "database.extension.drop",
		map[string]any{"extension": name, "cascade": cascade}, "",
		func(ctx context.Context, pool *sql.DB) (*dbx.DDLPlan, error) {
			return manager.PlanDropExtension(ctx, pool, name, cascade)
		})
}

type preloadRequest struct {
	// Library is the library, or the extension it is loaded for, from the
	// catalogue's closed list.
	Library string `json:"library"`
}

// handleDBPreload adds one library to shared_preload_libraries, keeping every
// entry already there, and sets the parameters the library reads at start
// that it is pointless without. It changes the configuration and nothing
// else: the server loads the library when it is next restarted, which the
// reply says and the power route does.
//
// It is runDDL's shape, with the list the next start loads in the reply: a
// preview runs nothing and is not on the audit trail, a run is recorded with
// its statements whether or not the engine accepted them.
func (s *Server) handleDBPreload(w http.ResponseWriter, r *http.Request) error {
	var req preloadRequest
	conn, err := s.ddlDecode(r, &req)
	if err != nil {
		return err
	}
	preloader, ok := dbx.PreloaderFor(conn.Driver)
	if !ok {
		return ddlInvalid(r, "%s loads no libraries at start from here", conn.Driver)
	}
	if strings.TrimSpace(req.Library) == "" {
		return ddlInvalid(r, "a library is required")
	}
	unreadable := func(err error) error {
		if ddlPreview(r) {
			httpx.SkipAudit(r)
		}
		return err
	}
	pool, _, err := s.dbPool(r.Context(), conn.ID)
	if err != nil {
		return unreadable(err)
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	plan, err := preloader.PlanPreload(ctx, pool, req.Library)
	if errors.Is(err, dbx.ErrPlanRead) {
		return unreadable(httpx.Err(http.StatusBadGateway, "query_failed", err.Error()))
	}
	if err != nil {
		return ddlInvalid(r, "%v", err)
	}
	reply := map[string]any{"statement": plan.String(), "statements": plan.Statements,
		"library": plan.Library, "libraries": plan.Libraries, "notes": plan.Notes, "restartRequired": true}
	if ddlPreview(r) {
		httpx.SkipAudit(r)
		reply["preview"] = true
		httpx.JSON(w, http.StatusOK, reply)
		return nil
	}
	httpx.SetAudit(r, "database.extension.preload", conn.Name, map[string]any{
		"library": plan.Library, "libraries": plan.Libraries, "statements": plan.Statements})
	if err := plan.Exec(ctx, pool); err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.JSON(w, http.StatusOK, reply)
	return nil
}
