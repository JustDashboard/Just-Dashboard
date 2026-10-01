package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
	"github.com/go-chi/chi/v5"
)

// Every database on this machine, and the act of connecting one.
//
// The Databases section used to know only its saved connections, and it made
// them by saving everything it could read the credentials of, unasked and
// untested, every time the page opened. So a database was either a connection
// or it did not exist: a stopped server, a service nobody had brought up, a
// SQLite file in an application's volume and a cluster installed on the host
// had nowhere to appear, and a container whose volume was initialised under
// another password became a saved connection that failed every request.
//
// This file separates the two. The inventory is a read: everything found, why
// it was taken for what it is, and — where it cannot be connected — the reason
// in a sentence. Connecting is one explicit request about one instance, named
// by its key rather than by an address the browser was told: the server looks
// the instance up again, signs in, and only then saves. A connection that was
// never able to sign in is not kept.

// mountDatabaseInventoryRoutes registers the routes that find database servers on this machine and control the ones found. It is called inside the /databases
// route, so paths are relative to it and each group states the capability it
// needs.
func (s *Server) mountDatabaseInventoryRoutes(r chi.Router) {
	// Read surface. What a role without system.admin is shown is built without
	// reading any container's environment — see handleDBInventory.
	r.Method(http.MethodGet, "/inventory", s.handle(s.handleDBInventory))
	r.Group(func(r chi.Router) {
		// Connecting reads a container's credentials and saves a connection,
		// and ignoring changes what the next reconcile does, so both sit with
		// creating a connection by hand. The scan walks the filesystem at the
		// caller's bidding, which is not something to hand a read-only role.
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodPost, "/inventory/scan", s.handle(s.handleDBInventoryScan))
		r.Method(http.MethodPost, "/inventory/connect", s.handle(s.handleDBInventoryConnect))
		r.Method(http.MethodPost, "/inventory/ignore", s.handle(s.handleDBInventoryIgnore))
	})
}

// dbInventoryFresh is how long one reading of the machine answers for. The
// page polls, and each reading lists Docker, walks every process's sockets and
// asks systemd; a server does not appear or vanish between two polls.
const dbInventoryFresh = 20 * time.Second

// dbInventoryState is what the inventory keeps between requests.
type dbInventoryState struct {
	mu sync.Mutex
	// full is the reading an administrator is shown, reduced the one built
	// without container environment for everybody else. They are kept apart
	// because they are different readings, not one filtered into the other.
	full    *dbInventoryKept
	reduced *dbInventoryKept
	// building serialises a reading per level of detail, so a burst of polls
	// reads the machine once.
	buildingFull    sync.Mutex
	buildingReduced sync.Mutex
	files           dbFileScanState
	// refused is the sign-ins the reconcile tried and was refused, by instance
	// key, so it does not send the same failed login on every page load.
	refused map[string]refusedSignIn

	// host, dial and secret are the machine, the sign-in and the secret file
	// read. Nil means the real ones; a test hands in its own.
	host   *dbInventoryHost
	dial   func(ctx context.Context, driver dbx.Driver, dsn string) error
	secret func(ctx context.Context, container, path string) ([]byte, error)
}

type dbInventoryKept struct {
	at        time.Time
	instances []dbx.Instance
	scans     []inventoryScan
}

// inventoryResponse is the whole inventory as one reading.
type inventoryResponse struct {
	Instances []dbx.Instance  `json:"instances"`
	Scans     []inventoryScan `json:"scans"`
	// Ignored is every key the operator said to leave alone, including ones
	// for instances that are no longer there to carry the flag.
	Ignored []string `json:"ignored"`
	// Detail is "full" or "reduced": whether container environment was read
	// to produce the list.
	Detail    string    `json:"detail"`
	CheckedAt time.Time `json:"checkedAt"`
}

// handleDBInventory lists everything on this machine that holds a database.
//
// Any role may read it, and what it reads depends on the role. For
// system.admin every container is inspected, so a database is recognised from
// its environment and command and its credentials are classified. For every
// other role nothing is inspected: the list is built from image names, the
// container listing, listening sockets, units and files — facts those roles
// can already see on the Docker, Ports, Processes and Files pages — and says
// nothing about credentials. The reduced list is a separate reading, not the
// full one with fields removed, so nothing derived from an environment
// variable can leak into it.
func (s *Server) handleDBInventory(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	ctx, cancel := timeoutCtx(r, 45*time.Second)
	defer cancel()
	return s.writeInventory(ctx, w, httpx.MustPrincipal(r).Can(auth.CapSystemAdmin), false)
}

// handleDBInventoryScan walks for database files now and answers with the
// inventory that results. It changes nothing but the dashboard's own memory of
// what it found, so there is nothing to audit.
func (s *Server) handleDBInventoryScan(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	s.fileScan(ctx, true)
	return s.writeInventory(ctx, w, true, true)
}

func (s *Server) writeInventory(ctx context.Context, w http.ResponseWriter, detail, fresh bool) error {
	kept := s.inventory(ctx, detail, fresh)
	instances, ignored, err := s.annotateInventory(ctx, kept.instances)
	if err != nil {
		return err
	}
	out := inventoryResponse{Instances: instances, Scans: kept.scans, Ignored: ignored, Detail: "reduced", CheckedAt: kept.at}
	if detail {
		out.Detail = "full"
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// inventory is the machine as last read at that level of detail, read again
// when that is stale or the caller asks.
func (s *Server) inventory(ctx context.Context, detail, fresh bool) dbInventoryKept {
	state := &s.dbInventory
	building := &state.buildingReduced
	if detail {
		building = &state.buildingFull
	}
	building.Lock()
	defer building.Unlock()

	state.mu.Lock()
	kept := state.reduced
	if detail {
		kept = state.full
	}
	state.mu.Unlock()
	if kept != nil && !fresh && time.Since(kept.at) < dbInventoryFresh {
		return *kept
	}

	found, _ := s.fileScan(ctx, false)
	inv, scans := s.buildInventory(ctx, detail, found)
	next := dbInventoryKept{at: time.Now().UTC(), instances: inv.Instances, scans: scans}
	// A reading cut short by the request's deadline is not kept: the next
	// asking may have the time this one did not.
	if ctx.Err() == nil {
		state.mu.Lock()
		if detail {
			state.full = &next
		} else {
			state.reduced = &next
		}
		state.mu.Unlock()
	}
	return next
}

// dropInventory forgets both kept readings, so the next request reads again.
func (s *Server) dropInventory() {
	s.dbInventory.mu.Lock()
	s.dbInventory.full, s.dbInventory.reduced = nil, nil
	s.dbInventory.mu.Unlock()
}

// buildInventory reads the machine once and classifies what it found.
//
// The Docker reads share one snapshot: the container list is asked for by
// three collectors, and each container is inspected at most once. The
// snapshot is for this read only and is never a precondition for a change.
func (s *Server) buildInventory(ctx context.Context, detail bool, found dbFileScanResult) (dbx.Inventory, []inventoryScan) {
	ctx = s.modules.docker.WithReadSnapshot(ctx)
	facts := dbx.Facts{Files: found.files, DataDirs: found.dirs, Embedded: found.embedded}
	scans := make([]inventoryScan, 0, 6)
	var (
		containers []dockerx.Container
		stacks     []dockerx.ComposeStack
		scan       inventoryScan
	)
	facts.Containers, containers, scan = s.collectContainers(ctx, detail)
	scans = append(scans, scan)
	facts.Declared, stacks, scan = s.collectDeclared(ctx)
	scans = append(scans, scan)
	facts.Listeners, scan = s.collectListeners(ctx)
	scans = append(scans, scan)
	facts.Sockets, scan = s.collectSockets(ctx)
	scans = append(scans, scan)
	facts.Units, scan = s.collectUnits(ctx)
	scans = append(scans, scan)
	if found.scan.Source != "" {
		scans = append(scans, found.scan)
	}
	facts.Places = s.inventoryPlaces(ctx, containers, stacks)
	return dbx.Discover(facts), scans
}

// annotateInventory adds what the store knows to a reading: which saved
// connections point at each instance, and which the operator said to ignore.
// It works on a copy, because the reading is shared between requests.
func (s *Server) annotateInventory(ctx context.Context, kept []dbx.Instance) ([]dbx.Instance, []string, error) {
	instances := slices.Clone(kept)
	conns, _, err := s.savedConnections(ctx)
	if err != nil {
		return nil, nil, err
	}
	dbx.AttachConnections(instances, conns)
	ignored, err := s.ignoredOrigins(ctx)
	if err != nil {
		return nil, nil, err
	}
	keys := make([]string, 0, len(ignored))
	for key := range ignored {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for i := range instances {
		instances[i].Ignored = ignored[instances[i].Key]
	}
	return instances, keys, nil
}

// savedConnections is every saved connection in the terms the inventory
// matches on, and every connection's name by id. A row whose DSN cannot be
// opened is left out of the first and kept in the second: it points nowhere,
// and its name is still taken.
func (s *Server) savedConnections(ctx context.Context) ([]dbx.SavedConnection, map[int64]string, error) {
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT id, name, driver, dsn_enc, origin FROM db_connections ORDER BY id`)
	if err != nil {
		return nil, nil, httpx.Internal(err)
	}
	defer rows.Close()
	conns := []dbx.SavedConnection{}
	names := map[int64]string{}
	for rows.Next() {
		var (
			id                           int64
			name, driver, sealed, origin string
		)
		if err := rows.Scan(&id, &name, &driver, &sealed, &origin); err != nil {
			return nil, nil, httpx.Internal(err)
		}
		names[id] = name
		dsn, err := s.Sealer.Open(sealed)
		if err != nil {
			continue
		}
		conn := dbx.SavedConnection{ID: id, Driver: dbx.Driver(driver), Origin: origin}
		if conn.Driver == dbx.DriverSQLite {
			path, _ := dbx.SQLiteDSNPath(dsn)
			conn.Path = filepath.Clean(path)
		} else if info, err := dbx.ParseDSN(conn.Driver, dsn); err == nil {
			conn.Host, conn.Port = info.Host, atoiDefault(info.Port, 0)
		}
		conns = append(conns, conn)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, httpx.Internal(err)
	}
	return conns, names, nil
}

// ignoredOrigins is the set of inventory keys the operator said to leave alone.
func (s *Server) ignoredOrigins(ctx context.Context) (map[string]bool, error) {
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT origin FROM db_inventory_ignored`)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, httpx.Internal(err)
		}
		out[key] = true
	}
	return out, rows.Err()
}

// setIgnored sets or clears the mark on one key, and reports whether that
// changed anything: clearing a mark that was not there is not an event.
func (s *Server) setIgnored(ctx context.Context, key, by string, ignored bool) (bool, error) {
	var (
		res sql.Result
		err error
	)
	if ignored {
		res, err = s.Store.DB.ExecContext(ctx,
			`INSERT INTO db_inventory_ignored(origin, ignored_at, ignored_by) VALUES(?,?,?)
			 ON CONFLICT(origin) DO NOTHING`, key, time.Now().Unix(), by)
	} else {
		res, err = s.Store.DB.ExecContext(ctx, `DELETE FROM db_inventory_ignored WHERE origin = ?`, key)
	}
	if err != nil {
		return false, httpx.Internal(err)
	}
	changed, _ := res.RowsAffected()
	return changed > 0, nil
}

type inventoryIgnoreRequest struct {
	Key     string `json:"key"`
	Ignored *bool  `json:"ignored"`
}

// handleDBInventoryIgnore records that a found database is to be left alone,
// or takes that back. An ignored instance stays in the inventory, flagged; it
// is the reconcile in POST /databases/sync that stops connecting it.
func (s *Server) handleDBInventoryIgnore(w http.ResponseWriter, r *http.Request) error {
	var req inventoryIgnoreRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if !dbx.ValidInstanceKey(req.Key) {
		return httpx.BadRequest("key must name a found database, as the inventory lists it")
	}
	if req.Ignored == nil {
		return httpx.BadRequest("ignored must be true or false")
	}
	if _, err := s.setIgnored(r.Context(), req.Key, httpx.MustPrincipal(r).Username(), *req.Ignored); err != nil {
		return err
	}
	action := "database.inventory.ignore"
	if !*req.Ignored {
		action = "database.inventory.unignore"
	}
	httpx.SetAudit(r, action, req.Key, nil)
	httpx.JSON(w, http.StatusOK, map[string]any{"key": req.Key, "ignored": *req.Ignored})
	return nil
}

// originOfConnection is the inventory key of the database a saved connection
// points at: the one it recorded when it was made, and for a connection older
// than that record, the instance found at its address now. Empty when it
// points at nothing on this machine.
//
// The route that forgets a connection calls this before it deletes the row and
// hands the answer to ignoreOriginOnForget after.
func (s *Server) originOfConnection(ctx context.Context, id int64) string {
	var origin string
	if err := s.Store.DB.QueryRowContext(ctx, `SELECT origin FROM db_connections WHERE id = ?`, id).Scan(&origin); err != nil {
		return ""
	}
	if origin != "" {
		return origin
	}
	instances, _, err := s.annotateInventory(ctx, s.inventory(ctx, true, false).instances)
	if err != nil {
		return ""
	}
	for _, inst := range instances {
		if slices.Contains(inst.Connections, id) {
			return inst.Key
		}
	}
	return ""
}

// ignoreOriginOnForget keeps a forgotten server forgotten.
//
// The reconcile connects every server it can sign in to, so forgetting a
// connection to one used to be pointless: it was back the next time the page
// opened. When the last connection to a found server is forgotten, the server
// is marked ignored instead, which the reconcile respects; connecting it
// again from the inventory clears the mark.
//
// Only a server is marked. A file is never connected unasked, so there is
// nothing to keep from coming back.
//
// It reports whether it marked anything, so the route that forgot the
// connection can say so in its own audit entry: the mark is a second thing
// that request changed, and it decides what the next reconcile does.
func (s *Server) ignoreOriginOnForget(ctx context.Context, origin, by string) (bool, error) {
	if !strings.HasPrefix(origin, "docker:") && !strings.HasPrefix(origin, "compose:") && !strings.HasPrefix(origin, "host:") {
		return false, nil
	}
	var others int
	if err := s.Store.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM db_connections WHERE origin = ?`, origin).Scan(&others); err != nil {
		return false, httpx.Internal(err)
	}
	if others > 0 {
		// Still connected under another login; nothing would come back.
		return false, nil
	}
	return s.setIgnored(ctx, origin, by, true)
}

// dashboardStorePath is the dashboard's own database file.
func (s *Server) dashboardStorePath() string {
	return filepath.Join(filepath.Clean(s.Cfg.DataDir), store.DatabaseFile)
}

// refuseOwnStore keeps the dashboard's own store from being opened as a
// database connection.
//
// A SQLite connection is writable, and the file behind this one is where the
// sessions, the sealed secrets and the audit trail live. Editing it through
// the table editor is a way to promote an account or delete an audit entry
// with no route having been asked. It is listed in the inventory, marked as
// the dashboard's own, and that is as far as it goes.
func (s *Server) refuseOwnStore(driver dbx.Driver, dsn string) error {
	if driver != dbx.DriverSQLite {
		return nil
	}
	path, _ := dbx.SQLiteDSNPath(dsn)
	if path == "" {
		return nil
	}
	own := s.dashboardStorePath()
	clean := filepath.Clean(path)
	if clean == own || dbx.HostView(clean) == own || sameFile(clean, own) {
		return httpx.Err(http.StatusConflict, "self_database",
			"this file is the dashboard's own store; it is listed, and never connected")
	}
	return nil
}

// sameFile reports two paths naming one file, which a symbolic link or a
// second mount of the same directory makes possible.
func sameFile(a, b string) bool {
	left, err := os.Stat(a)
	if err != nil {
		return false
	}
	right, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(left, right)
}

// dialDatabase signs in to a server once, keeping nothing.
func (s *Server) dialDatabase(ctx context.Context, driver dbx.Driver, dsn string) error {
	if s.dbInventory.dial != nil {
		return s.dbInventory.dial(ctx, driver, dsn)
	}
	return s.probeConnection(ctx, driver, dsn)
}

// maxSecretBytes bounds a password read out of a container's secret file.
const maxSecretBytes = 4096

// containerSecret reads the password a container keeps in a file, where its
// environment names the file instead of stating the value. This is the same
// secret the environment would have stated, read at the same moment — when the
// operator asks for the connection — and it goes no further than the DSN.
func (s *Server) containerSecret(ctx context.Context, container, path string) (string, error) {
	var (
		raw []byte
		err error
	)
	switch {
	case s.dbInventory.secret != nil:
		raw, err = s.dbInventory.secret(ctx, container, path)
	case s.modules.docker == nil:
		err = errors.New("this host has no Docker socket")
	default:
		raw, err = s.modules.docker.ReadContainerFile(ctx, container, path)
	}
	if err != nil {
		return "", err
	}
	if len(raw) > maxSecretBytes {
		return "", errors.New("the file is too large to be a password")
	}
	// The images read the file and strip the line ending a text editor adds.
	return strings.TrimRight(string(raw), "\r\n"), nil
}

type inventoryConnectRequest struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	User     string `json:"user"`
	Password string `json:"password"`
	Database string `json:"database"`
}

// handleDBInventoryConnect makes a saved connection to one found database.
//
// The request names an instance by its key and nothing else about where it is.
// The server looks the instance up again — what a listing said twenty seconds
// ago is not what is true now — reads whatever credentials the container
// states, signs in, and saves the connection only if that worked. The engine's
// own refusal comes back in the dialog the operator is still looking at.
func (s *Server) handleDBInventoryConnect(w http.ResponseWriter, r *http.Request) error {
	var req inventoryConnectRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if !dbx.ValidInstanceKey(req.Key) {
		return httpx.BadRequest("key must name a found database, as the inventory lists it")
	}
	for _, c := range req.Password {
		if c < 0x20 || c == 0x7f {
			return httpx.BadRequest("the password contains a control character")
		}
	}
	if !strings.HasPrefix(req.Key, "file:") {
		// A file is opened by its path, which the key already is; neither
		// field is read for one.
		if err := validConnectionNames(req.User, req.Database); err != nil {
			return err
		}
	}
	ctx, cancel := timeoutCtx(r, 45*time.Second)
	defer cancel()
	inst, access, err := s.resolveInstance(ctx, req.Key)
	if err != nil {
		return err
	}
	return s.connectInstance(ctx, w, r, inst, access, connectOptions{
		name: req.Name, user: req.User, password: req.Password, database: req.Database,
		action: "database.inventory.connect",
	})
}

// resolveInstance finds one instance by key in a fresh reading of the machine.
//
// A file is checked on its own rather than looked for in the last scan: the
// operator may be connecting a file the scan's budget did not reach, and the
// question "is this path a database" does not need a walk to answer.
func (s *Server) resolveInstance(ctx context.Context, key string) (*dbx.Instance, *dbx.Access, error) {
	if path, ok := strings.CutPrefix(key, "file:"); ok {
		return s.resolveFileInstance(ctx, path)
	}
	// Two kinds of thing are listed that no connection can be made to,
	// whatever a fresh reading says about them.
	if strings.HasPrefix(key, "data:") {
		return nil, nil, httpx.Err(http.StatusConflict, "not_connectable",
			"a data directory is not something a driver opens: it needs its server running on it")
	}
	if strings.HasPrefix(key, "embedded:") {
		return nil, nil, httpx.Err(http.StatusConflict, "not_connectable",
			"this file is inside a container's own writable layer, which nothing outside the container can open")
	}
	inv, _ := s.buildInventory(ctx, true, dbFileScanResult{})
	inst, ok := inv.Find(key)
	if !ok {
		return nil, nil, httpx.Err(http.StatusNotFound, "not_found",
			"nothing on this machine answers to "+key+" any more")
	}
	if access, ok := inv.Access(key); ok {
		return inst, &access, nil
	}
	return inst, nil, nil
}

func (s *Server) resolveFileInstance(ctx context.Context, path string) (*dbx.Instance, *dbx.Access, error) {
	if s.modules.files == nil {
		return nil, nil, httpx.Err(http.StatusServiceUnavailable, "unavailable",
			"file roots are not configured, so a file cannot be contained")
	}
	if !filepath.IsAbs(path) {
		return nil, nil, httpx.BadRequest("key must name a file by its full path")
	}
	resolved, err := s.modules.files.Resolve(path)
	if errors.Is(err, files.ErrOutsideRoot) {
		return nil, nil, httpx.Err(http.StatusForbidden, "outside_root", err.Error())
	}
	if err != nil {
		return nil, nil, httpx.BadRequest("%v", err)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return nil, nil, httpx.Err(http.StatusNotFound, "not_found", "there is no file at "+path+" any more")
	}
	engine, ok := fileEngine(resolved)
	if !ok {
		return nil, nil, httpx.Err(http.StatusNotFound, "not_found", path+" is not a database file")
	}
	facts := dbx.FileFacts{Path: resolved, Engine: engine, Size: info.Size(), Modified: info.ModTime().UTC()}
	inv := dbx.Discover(dbx.Facts{Files: []dbx.FileFacts{facts}, Places: dbx.Places{
		StorePath: s.dashboardStorePath(), DataDir: filepath.Clean(s.Cfg.DataDir),
	}})
	inst, ok := inv.Find("file:" + resolved)
	if !ok {
		return nil, nil, httpx.Err(http.StatusNotFound, "not_found", path+" is not a database file")
	}
	if sameFile(resolved, s.dashboardStorePath()) {
		inst.Self, inst.Connectable = true, false
		inst.Reason = "this is the dashboard's own store — it is listed, and never connected"
	}
	return inst, nil, nil
}

// validConnectionNames refuses a user or a database that a connection string
// could read as something other than a name. dbx.BuildDSN refuses them too;
// saying so here is what turns "no connection string could be built" into a
// sentence about the field that was wrong.
func validConnectionNames(user, database string) error {
	if !dbx.ValidDSNUser(strings.TrimSpace(user)) {
		return httpx.BadRequest("user may not contain control characters or any of ? / @ :")
	}
	if !dbx.ValidDSNDatabase(strings.TrimSpace(database)) {
		return httpx.BadRequest("database may not contain control characters, ? or /")
	}
	return nil
}

type connectOptions struct {
	name, user, password, database string
	// action is the audit action a new connection is recorded under.
	action string
}

// connectInstance signs in to a resolved instance and saves the connection.
// Adopting a container and connecting from the inventory both end here.
func (s *Server) connectInstance(ctx context.Context, w http.ResponseWriter, r *http.Request, inst *dbx.Instance, access *dbx.Access, opts connectOptions) error {
	if inst.Self {
		return httpx.Err(http.StatusConflict, "self_database", inst.Reason)
	}
	if !inst.Connectable {
		return httpx.Err(http.StatusConflict, "not_connectable", inst.Name+" cannot be connected: "+inst.Reason)
	}
	cand := dbx.Candidate{Driver: inst.Driver}
	if inst.Kind != dbx.KindFile {
		if access == nil {
			return httpx.Err(http.StatusConflict, "not_connectable",
				inst.Name+" cannot be connected: nothing it listens on can be dialled from here")
		}
		cand = access.Candidate
		if database := strings.TrimSpace(opts.database); database != "" {
			cand.Database = database
		}
	}
	otherUser := false
	if user := strings.TrimSpace(opts.user); inst.Kind != dbx.KindFile && user != "" && user != cand.User {
		cand.User, otherUser = user, true
	}
	if err := s.refuseOwnStore(cand.Driver, inst.Database); err != nil {
		return err
	}

	// Connecting the same instance twice is not an error, it is the same
	// request arriving again — which it does, because the caller that creates
	// a server retries until the engine inside it is actually answering. The
	// same server, database and account is the same connection.
	lookup := inst.Database
	if inst.Kind != dbx.KindFile {
		lookup = dbx.BuildDSN(cand, "")
	}
	if lookup == "" {
		return httpx.BadRequest("no connection string could be built for %s", inst.Label)
	}
	conn, stored, names, err := s.adoptedDatabaseConnection(ctx, cand.Driver, lookup)
	if err != nil {
		return err
	}

	typed := opts.password != ""
	password, retry, err := s.instancePassword(ctx, inst, access, opts.password, otherUser)
	if err != nil {
		if conn != nil {
			// Already connected, and nothing new to sign in with: the
			// connection that is there is the answer.
			return s.keepConnection(ctx, w, r, inst, conn)
		}
		return err
	}
	if conn != nil {
		return s.refreshConnection(ctx, w, r, inst, cand, conn, stored, password, typed)
	}

	dsn := lookup
	if inst.Kind != dbx.KindFile {
		dsn = dbx.BuildDSN(cand, password)
	}
	if err := s.signIn(ctx, cand, &dsn, retry); err != nil {
		httpx.SetAudit(r, opts.action, inst.Key, map[string]any{"ok": false, "driver": string(cand.Driver), "user": cand.User})
		return signInError(inst, typed, err)
	}
	// Whatever the reconcile remembered being refused, it has been let in now.
	s.forgetRefusal(inst.Key)
	name := strings.TrimSpace(opts.name)
	if name == "" {
		name = defaultConnectionName(inst, cand)
	}
	if !connNameRe.MatchString(name) {
		return httpx.BadRequest("name may contain letters, digits, spaces, dots, dashes and underscores")
	}
	name = uniqueConnectionName(name, names)
	detail := map[string]any{"key": inst.Key, "engine": inst.Engine, "source": inst.Source}
	if inst.Container != nil {
		detail["container"], detail["image"] = inst.Container.Name, inst.Container.Image
	}
	if inst.Host != nil && inst.Host.Process != "" {
		detail["process"] = inst.Host.Process
	}
	return s.saveConnectionFrom(w, r, inst.Key, name, cand.Driver, dsn, opts.action, detail)
}

// instancePassword is the password a connection to an instance signs in with:
// the one the operator typed, or the one its container states. Where neither
// exists it says so rather than trying without — a wrong guess against the
// operator's own server is an authentication failure in their logs, and on a
// host with fail2ban a step towards banning this dashboard.
//
// retry reports a password from a variable the server may never have read, so
// a refusal is worth one more attempt with none.
func (s *Server) instancePassword(ctx context.Context, inst *dbx.Instance, access *dbx.Access, typed string, otherUser bool) (password string, retry bool, err error) {
	if inst.Kind == dbx.KindFile {
		return "", false, nil
	}
	if typed != "" {
		return typed, false, nil
	}
	needed := func() error {
		who := access.Candidate.User
		if who == "" {
			who = "it"
		}
		return httpx.Err(http.StatusConflict, "credentials_required",
			inst.Name+" needs a password for "+who+", and nothing on this machine states one")
	}
	if otherUser {
		// Whatever the container states is another account's password.
		return "", false, httpx.Err(http.StatusConflict, "credentials_required",
			inst.Name+" states no password for that account; type it")
	}
	switch inst.Credentials {
	case dbx.CredentialsEnv, dbx.CredentialsArgs:
		return access.Password, access.Unverified, nil
	case dbx.CredentialsOpen:
		return "", false, nil
	case dbx.CredentialsSecretFile:
		if access.SecretFile == "" || inst.Container == nil {
			return "", false, needed()
		}
		secret, err := s.containerSecret(ctx, inst.Container.ID, access.SecretFile)
		if err != nil {
			return "", false, httpx.Err(http.StatusConflict, "credentials_required", fmt.Sprintf(
				"%s keeps its password in %s inside the container, which could not be read (%v); type the password instead",
				inst.Name, access.SecretFile, err))
		}
		return secret, false, nil
	}
	return "", false, needed()
}

// keepConnection answers a connect with the connection that already exists,
// recording which instance it belongs to if it did not say.
//
// Nothing new was made, and that is not the same as nothing having changed: a
// connection learning which server it is, and a server the operator had said
// to leave alone being taken back, are both changes to what the next
// reconcile does. Each is recorded; only a request that changed neither is
// left out of the audit trail.
func (s *Server) keepConnection(ctx context.Context, w http.ResponseWriter, r *http.Request, inst *dbx.Instance, conn *dbConnection) error {
	res, err := s.Store.DB.ExecContext(ctx,
		`UPDATE db_connections SET origin = ? WHERE id = ? AND origin = ''`, inst.Key, conn.ID)
	if err != nil {
		return httpx.Internal(err)
	}
	linked, _ := res.RowsAffected()
	unignored, err := s.setIgnored(ctx, inst.Key, "", false)
	if err != nil {
		return err
	}
	switch {
	case unignored:
		httpx.SetAudit(r, "database.inventory.unignore", inst.Key,
			map[string]any{"connection": conn.Name, "linked": linked > 0, "by": "connect"})
	case linked > 0:
		httpx.SetAudit(r, "database.inventory.connect", inst.Key,
			map[string]any{"connection": conn.Name, "linked": true})
	default:
		httpx.SkipAudit(r)
	}
	httpx.JSON(w, http.StatusOK, conn)
	return nil
}

// signIn dials a DSN. Where the password came from a variable the server may
// never have read, a refusal is tried once more with no password at all: that
// sends no guess, only the absence of one, and an open server answers it.
func (s *Server) signIn(ctx context.Context, cand dbx.Candidate, dsn *string, retryOpen bool) error {
	err := s.dialDatabase(ctx, cand.Driver, *dsn)
	if err == nil || !retryOpen {
		return err
	}
	open := dbx.BuildDSN(cand, "")
	if open == "" || s.dialDatabase(ctx, cand.Driver, open) != nil {
		return err
	}
	*dsn = open
	return nil
}

// signInError words a failed sign-in. A password the operator typed and the
// engine refused is the request's own fault; credentials the container stated
// and the engine refused are a disagreement between two things on the server,
// which is a conflict the operator resolves by typing the real one.
//
// Not every failure is a refusal of the credentials. A listener asked for a
// service it does not have, or a server that is still starting, says nothing
// about the password, and telling the operator to type another one sends them
// to fix what is not broken.
func signInError(inst *dbx.Instance, typed bool, err error) error {
	if typed || inst.Kind == dbx.KindFile {
		// The engine's own words: "password authentication failed for user
		// postgres" is the entire diagnosis and names the account it refused.
		return httpx.Err(http.StatusBadRequest, "sign_in_failed", err.Error())
	}
	if !credentialRefusal(err) {
		return httpx.Err(http.StatusConflict, "sign_in_failed", fmt.Sprintf(
			"%s could not be signed in to, and not because of the credentials found for it: %v", inst.Name, err))
	}
	return httpx.Err(http.StatusConflict, "sign_in_failed", fmt.Sprintf(
		"%s did not accept the credentials found for it (%v); type the password it actually uses", inst.Name, err))
}

// refreshConnection handles connecting an instance that an existing connection
// already describes: the same server, database and user. The row keeps its
// identity — a deployment references it by id — and takes the credentials that
// work now, but only once they have been seen to work.
func (s *Server) refreshConnection(
	ctx context.Context, w http.ResponseWriter, r *http.Request,
	inst *dbx.Instance, cand dbx.Candidate, conn *dbConnection, stored, password string, typed bool,
) error {
	dsn := stored
	if cand.Driver != dbx.DriverSQLite {
		// Preserve this login's database and transport options. A replacement
		// container may rotate its password without changing its identity.
		refreshed, err := refreshedDatabasePassword(cand.Driver, stored, password)
		if err != nil {
			return httpx.Internal(err)
		}
		dsn = refreshed
	}
	if stored == dsn {
		return s.keepConnection(ctx, w, r, inst, conn)
	}
	if err := s.dialDatabase(ctx, cand.Driver, dsn); err != nil {
		httpx.SetAudit(r, "database.connection.refresh", conn.Name, map[string]any{"ok": false, "driver": string(cand.Driver), "user": cand.User})
		return signInError(inst, typed, err)
	}
	sealed, err := s.Sealer.Seal(dsn)
	if err != nil {
		return httpx.Internal(err)
	}
	if _, err := s.Store.DB.ExecContext(ctx,
		`UPDATE db_connections SET dsn_enc = ?, origin = CASE WHEN origin = '' THEN ? ELSE origin END WHERE id = ?`,
		sealed, inst.Key, conn.ID); err != nil {
		return httpx.BadRequest("could not update connection: %v", err)
	}
	unignored, err := s.setIgnored(ctx, inst.Key, "", false)
	if err != nil {
		return err
	}
	s.forgetRefusal(inst.Key)
	// The pool, if any, was dialled with the old DSN and would keep failing.
	s.modules.dbs.Close(conn.ID)
	conn, _, err = s.dbConnRow(ctx, conn.ID)
	if err != nil {
		return err
	}
	detail := map[string]any{"key": inst.Key, "driver": cand.Driver, "host": conn.Host, "user": conn.User}
	if inst.Container != nil {
		detail["container"], detail["image"] = inst.Container.Name, inst.Container.Image
	}
	if unignored {
		detail["unignored"] = true
	}
	httpx.SetAudit(r, "database.connection.refresh", conn.Name, detail)
	httpx.JSON(w, http.StatusOK, conn)
	return nil
}

var connNameUnsafe = regexp.MustCompile(`[^A-Za-z0-9 ._-]+`)

// defaultConnectionName is what a connection is called when the operator did
// not say: the container, the file, or the engine and where it is. It is made
// to fit the rule every connection name follows, because the name becomes a
// directory when the connection is dumped.
func defaultConnectionName(inst *dbx.Instance, cand dbx.Candidate) string {
	name := inst.Name
	switch {
	case inst.Container != nil:
		name = inst.Container.Name
	case inst.Kind == dbx.KindFile:
		name = strings.TrimSuffix(inst.Name, filepath.Ext(inst.Name))
	case inst.Source == dbx.SourceHost:
		// The process name would be the obvious choice and is the wrong one:
		// "mysqld" names the program rather than the thing connected to.
		name = dbx.HostConnectionName(cand)
		if primary := cand.Port; primary > 0 && inst.Host != nil && inst.Host.Cluster != "" {
			name = string(cand.Driver) + " " + strings.ReplaceAll(inst.Host.Cluster, "/", " ") + " on this host"
		}
	}
	name = strings.Trim(connNameUnsafe.ReplaceAllString(name, "-"), " ._-")
	if len(name) > 60 {
		name = strings.TrimRight(name[:60], " ._-")
	}
	if name == "" {
		name = string(inst.Driver) + "-" + strconv.FormatInt(time.Now().Unix(), 36)
	}
	return name
}

// connectionNames is the names of the connections with those ids.
func connectionNames(ids []int64, names map[int64]string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if name, ok := names[id]; ok {
			out = append(out, name)
		}
	}
	return out
}
