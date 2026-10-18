package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
	"github.com/go-chi/chi/v5"
)

// Bringing a database over from another server.
//
// The source is a connection string typed on the page, not a saved
// connection: the server it names is somebody else's, and keeping a standing
// login to it for the length of one move is the thing to avoid. So the string
// is sealed in the one place it has to outlive the request (a replication
// import, whose cutover needs the source again hours later), emptied when the
// import ends, returned by no route, and left out of every job line, audit
// entry and error.
//
// Where the engine can, the target stays current while the source keeps taking
// writes (Postgres logical replication, dbx/online_pg.go) and the move ends in
// a cutover the operator chooses the moment of. Everywhere else it is a dump
// read from the source and loaded into the target as it is written, with no
// file in between (dbx/copy_stream.go).

const (
	importReplication = "replication"
	importStream      = "stream"

	importRunning     = "running"
	importReplicating = "replicating"
	importCutting     = "cutting"
	importDone        = "done"
	importFailed      = "failed"
	importCancelled   = "cancelled"
)

// importSettle is how long a row that says it is running is believed without
// a job to show for it, so a read between the row being written and its job
// starting does not call it abandoned.
const importSettle = 15 * time.Second

// importRecord is a row of db_online_imports as the page reads it. The sealed
// connection string is not in it.
type importRecord struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Mode     string `json:"mode"`
	Status   string `json:"status"`
	Database string `json:"database"`
	// Source is where it came from, without the credentials.
	Source          importSource `json:"source"`
	StructureOnly   bool         `json:"structureOnly"`
	PublicationMade bool         `json:"publicationMade"`
	Detail          string       `json:"detail,omitempty"`
	StartedBy       string       `json:"startedBy"`
	CreatedAt       time.Time    `json:"createdAt"`
	UpdatedAt       time.Time    `json:"updatedAt"`
	FinishedAt      *time.Time   `json:"finishedAt,omitempty"`
	// Job is the dump, load or cutover running for it now.
	Job *jobs.Job `json:"job,omitempty"`
}

type importSource struct {
	Address  string `json:"address"`
	User     string `json:"user,omitempty"`
	Database string `json:"database,omitempty"`
}

func (src importSource) label() string {
	if src.Database == "" {
		return src.Address
	}
	return src.Address + "/" + src.Database
}

// importRow is the stored row with what the page does not see.
type importRow struct {
	importRecord
	sourceEnc string
	conn      int64
}

const importColumns = `id, connection_id, name, mode, status, target_database, source_label, source_user, source_enc,
	structure_only, publication_made, detail, started_by, created_at, updated_at, finished_at`

func scanImport(scan func(...any) error) (*importRow, error) {
	var (
		row                        importRow
		label                      string
		structure, made            int
		created, updated, finished int64
	)
	if err := scan(&row.ID, &row.conn, &row.Name, &row.Mode, &row.Status, &row.Database, &label, &row.Source.User,
		&row.sourceEnc, &structure, &made, &row.Detail, &row.StartedBy, &created, &updated, &finished); err != nil {
		return nil, err
	}
	// The last slash: a Unix socket's address has slashes of its own.
	if cut := strings.LastIndex(label, "/"); cut >= 0 {
		row.Source.Address, row.Source.Database = label[:cut], label[cut+1:]
	} else {
		row.Source.Address = label
	}
	row.StructureOnly, row.PublicationMade = structure != 0, made != 0
	row.CreatedAt, row.UpdatedAt = time.Unix(created, 0).UTC(), time.Unix(updated, 0).UTC()
	if finished > 0 {
		at := time.Unix(finished, 0).UTC()
		row.FinishedAt = &at
	}
	return &row, nil
}

func (s *Server) loadImport(ctx context.Context, conn, id int64) (*importRow, error) {
	row, err := scanImport(s.Store.DB.QueryRowContext(ctx,
		`SELECT `+importColumns+` FROM db_online_imports WHERE id = ? AND connection_id = ?`, id, conn).Scan)
	if err == sql.ErrNoRows {
		return nil, httpx.ErrNotFound
	}
	if err != nil {
		return nil, httpx.Internal(err)
	}
	return row, nil
}

func (s *Server) loadImports(ctx context.Context, conn int64) ([]*importRow, error) {
	rows, err := s.Store.DB.QueryContext(ctx,
		`SELECT `+importColumns+` FROM db_online_imports WHERE connection_id = ? ORDER BY id DESC`, conn)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	defer rows.Close()
	out := []*importRow{}
	for rows.Next() {
		row, err := scanImport(rows.Scan)
		if err != nil {
			return nil, httpx.Internal(err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// setImport moves a row to a status. A status that ends the import also
// empties the sealed connection string: nothing is left to cut over, and a
// credential kept for no use is only a liability.
func (s *Server) setImport(id int64, status, detail string) {
	now := time.Now().Unix()
	finished := int64(0)
	keep := status == importRunning || status == importReplicating || status == importCutting
	if !keep {
		finished = now
	}
	query := `UPDATE db_online_imports SET status = ?, detail = ?, updated_at = ?, finished_at = ?`
	args := []any{status, detail, now, finished}
	if !keep {
		query += `, source_enc = ''`
	}
	// The job's context may be what ended; the row still has to say so.
	_, _ = s.Store.DB.ExecContext(context.Background(), query+` WHERE id = ?`, append(args, id)...)
}

// importKindOf is the job kind for the work an import does.
func importKind(conn int64, what string) string { return transferPrefix(conn) + what }

// runningImportJob is the import work running against a connection now.
func (s *Server) runningImportJob(conn int64) *jobs.Job {
	job, ok := s.runningTransfer(conn)
	if !ok || !strings.HasPrefix(job.Kind, importKind(conn, "import")) {
		return nil
	}
	return &job
}

// settleImports gives the rows that claim work nothing is doing the status
// they have: a stream that was running when the dashboard stopped did not
// finish; a replication one is still replicating if its subscription is there,
// and failed if it never got that far.
func (s *Server) settleImports(ctx context.Context, conn *dbConnection, dsn string, rows []*importRow) {
	busy := s.runningImportJob(conn.ID)
	for _, row := range rows {
		if (row.Status != importRunning && row.Status != importCutting) || busy != nil ||
			time.Since(row.UpdatedAt) < importSettle {
			continue
		}
		next, detail := importFailed, "The dashboard stopped while this was running."
		if row.Mode == importReplication {
			state, err := dbx.ReadSubscription(ctx, dbx.DSNForDatabase(conn.Driver, dsn, row.Database), row.Name)
			if err == nil && state.Exists {
				next, detail = importReplicating, "The dashboard stopped while this was running; the subscription is still there."
			}
		}
		s.setImport(row.ID, next, detail)
		row.Status, row.Detail = next, detail
		if next == importFailed {
			row.sourceEnc = ""
		}
	}
}

func (s *Server) mountDatabaseImportRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/{id}/imports", s.handle(s.handleImportList))
	r.Method(http.MethodGet, "/{id}/imports/{importId}", s.handle(s.handleImportRead))
}

// mountDatabaseImportWrites registers what changes a server: it sits in the
// group that needs system.admin, beside the copy it extends.
func (s *Server) mountDatabaseImportWrites(r chi.Router) {
	r.Method(http.MethodPost, "/{id}/imports/inspect", s.handle(s.handleImportInspect))
	r.Method(http.MethodPost, "/{id}/imports", s.handle(s.handleImportStart))
	r.Method(http.MethodPost, "/{id}/imports/{importId}/cutover", s.handle(s.handleImportCutover))
	s.destructive(r, func(r chi.Router) {
		r.Method(http.MethodDelete, "/{id}/imports/{importId}", s.handle(s.handleImportRemove))
	})
}

func importID(r *http.Request) (int64, error) {
	var id int64
	if _, err := fmt.Sscan(chi.URLParam(r, "importId"), &id); err != nil || id < 1 {
		return 0, httpx.BadRequest("invalid import id")
	}
	return id, nil
}

// sourceView is the address of a connection string, without its credentials.
func sourceView(driver dbx.Driver, dsn string) (importSource, error) {
	info, err := dbx.ParseDSN(driver, dsn)
	if err != nil {
		return importSource{}, err
	}
	address := info.Host
	if info.Port != "" {
		address += ":" + info.Port
	}
	return importSource{Address: address, User: info.User, Database: info.Database}, nil
}

// scrub takes every connection string's password out of an error.
func scrub(err error, dsns ...string) error {
	for _, dsn := range dsns {
		err = withoutSecrets(dsn, err)
	}
	return err
}

// importOffered refuses an engine none of whose products can be brought over,
// before anything is dialled.
func importOffered(driver dbx.Driver) error {
	if form, _ := dbx.Capabilities(driver, "")["onlineImport"].(string); form == "" {
		return httpx.BadRequest("%s cannot be brought over from another server", dbx.FlavorLabel(dbx.DefaultFlavor(driver)))
	}
	return nil
}

// importForm is what this server's engine offers, read off the capability
// table for the product that answered: "replication", "stream", or neither.
func (s *Server) importForm(ctx context.Context, conn *dbConnection, dsn string) (string, error) {
	identity, err := dbx.ProbeIdentity(ctx, conn.Driver, dsn)
	if err != nil {
		return "", connectFailed(dsn, err)
	}
	form, _ := dbx.Capabilities(conn.Driver, identity.Flavor)["onlineImport"].(string)
	if form == "" {
		return "", httpx.BadRequest("%s cannot be brought over from another server", dbx.FlavorLabel(identity.Flavor))
	}
	return form, nil
}

// importName is what a publication, a subscription and a slot are called when
// the operator did not say: the database it brings, in the shape they need.
var importNameUnsafe = regexp.MustCompile(`[^a-z0-9_]+`)

func suggestedImportName(database string) string {
	name := strings.Trim(importNameUnsafe.ReplaceAllString(strings.ToLower(database), "_"), "_")
	if name == "" {
		name = "database"
	}
	name = "jd_import_" + name
	if len(name) > 50 {
		name = name[:50]
	}
	return name
}

// sameCluster reports whether the source and the target are the one Postgres
// cluster. A subscription to its own cluster waits for ever for a slot only it
// could make, so it is refused before it is tried. A cluster is told by the
// moment its postmaster started, which any login may read.
func sameCluster(ctx context.Context, sourceDSN, targetDSN string) bool {
	started := func(dsn string) string {
		db, err := dbx.OpenDatabase(ctx, dbx.DriverPostgres, dsn, "")
		if err != nil {
			return ""
		}
		defer db.Close()
		var at string
		if err := db.QueryRowContext(ctx, `SELECT pg_postmaster_start_time()::text`).Scan(&at); err != nil {
			return ""
		}
		return at
	}
	source := started(sourceDSN)
	return source != "" && source == started(targetDSN)
}

// --- inspecting a source -----------------------------------------------------

type importInspectRequest struct {
	Source string `json:"source"`
	// Name is what the publication, subscription and slot will be called; the
	// statement handed back for a publication made by hand is written with it.
	Name string `json:"name"`
}

type importInspection struct {
	Reachable bool   `json:"reachable"`
	Error     string `json:"error,omitempty"`
	// Forms are the ways this engine can bring a database over, the better
	// one first.
	Forms  []string        `json:"forms"`
	Source importSourceNow `json:"source"`
	Name   string          `json:"name"`
	// Ready is whether a dump can be loaded; Blockers and Warnings are what
	// stands in the way of any form, and a replication's own are in Replication.
	Ready    bool     `json:"ready"`
	Blockers []string `json:"blockers"`
	Warnings []string `json:"warnings"`
	// Replication is what the source and the target say about replicating,
	// for an engine that can.
	Replication      *dbx.ImportSourceReport `json:"replication,omitempty"`
	ReplicationReady bool                    `json:"replicationReady"`
	// PublicationSQL is the statement that publishes what the target would
	// subscribe to, for an operator who will run it themselves.
	PublicationSQL string `json:"publicationSql,omitempty"`
}

type importSourceNow struct {
	importSource
	Version     string `json:"version,omitempty"`
	Flavor      string `json:"flavor,omitempty"`
	FlavorLabel string `json:"flavorLabel,omitempty"`
}

var loopbackHost = regexp.MustCompile(`^(localhost|127\.\d+\.\d+\.\d+|::1|\[::1\])$`)

// handleImportInspect reads a source and says what bringing it over would
// take. It changes nothing on either server and keeps nothing.
func (s *Server) handleImportInspect(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req importInspectRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.SkipAudit(r)
	if err := importOffered(conn.Driver); err != nil {
		return err
	}
	source := strings.TrimSpace(req.Source)
	if source == "" {
		return httpx.BadRequest("source is required")
	}
	view, err := sourceView(conn.Driver, source)
	if err != nil {
		return httpx.BadRequest("%v", scrub(err, source))
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = suggestedImportName(view.Database)
	}
	if !dbx.ImportNameRe.MatchString(name) {
		return httpx.BadRequest("a name is lowercase letters, digits and underscores, starting with a letter or underscore, at most 50 long")
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	form, err := s.importForm(ctx, conn, dsn)
	if err != nil {
		return err
	}
	out := &importInspection{
		Forms: []string{importStream}, Name: name,
		Source: importSourceNow{importSource: view}, Blockers: []string{}, Warnings: []string{},
	}
	if form == importReplication {
		out.Forms = []string{importReplication, importStream}
	}

	identity, err := dbx.ProbeIdentity(ctx, conn.Driver, source)
	if err != nil {
		out.Error = connectError(source, err)
		httpx.JSON(w, http.StatusOK, out)
		return nil
	}
	out.Reachable = true
	out.Source.Version, out.Source.Flavor, out.Source.FlavorLabel = identity.Version, identity.Flavor, dbx.FlavorLabel(identity.Flavor)
	if conn.Driver != dbx.DriverRedis && view.Database == "" {
		out.Blockers = append(out.Blockers, "The connection string names no database to bring over.")
	}
	if target, err := dbx.ProbeIdentity(ctx, conn.Driver, dsn); err == nil && target.Flavor != identity.Flavor {
		out.Warnings = append(out.Warnings, fmt.Sprintf(
			"The source is %s and this server is %s. What one has and the other lacks — a collation such as MariaDB's utf8mb4_uca1400_ai_ci, a type, a feature — stops the load at the statement that uses it.",
			dbx.FlavorLabel(identity.Flavor), dbx.FlavorLabel(target.Flavor)))
	}
	if form == importReplication {
		report, err := dbx.InspectReplicationSource(ctx, source)
		if err != nil {
			out.Error, out.Reachable = connectError(source, err), false
			httpx.JSON(w, http.StatusOK, out)
			return nil
		}
		// What only a replication needs is the replication's own: a source
		// that is not logical can still be dumped.
		if info, err := dbx.ParseDSN(conn.Driver, source); err == nil && loopbackHost.MatchString(info.Host) {
			report.Warnings = append(report.Warnings,
				"The source address is this machine's own. A replication subscription is dialled by the target server, where that address may mean the target.")
		}
		target, err := dbx.InspectReplicationTarget(ctx, dsn)
		if err != nil {
			return connectFailed(dsn, err)
		}
		if !target.CanSubscribe {
			report.Blockers = append(report.Blockers,
				"This server's login cannot create a subscription. It needs to be a superuser, or on PostgreSQL 16 and later a member of pg_create_subscription.")
		}
		if sameCluster(ctx, source, dsn) {
			report.Blockers = append(report.Blockers, "The source is this server itself. Copy the database from its Backups page instead.")
		}
		out.Replication = report
		out.ReplicationReady = len(out.Blockers) == 0 && report.Ready()
		out.PublicationSQL = dbx.PublicationSQL(out.Name, report.Tables)
	}
	out.Ready = len(out.Blockers) == 0
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// --- listing and reading -----------------------------------------------------

type importList struct {
	Imports []*importRecord `json:"imports"`
}

func (s *Server) recordsOf(conn *dbConnection, rows []*importRow) []*importRecord {
	job := s.runningImportJob(conn.ID)
	out := make([]*importRecord, 0, len(rows))
	for _, row := range rows {
		rec := row.importRecord
		if job != nil && (row.Status == importRunning || row.Status == importCutting) {
			rec.Job = job
		}
		out = append(out, &rec)
	}
	return out
}

func (s *Server) handleImportList(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	rows, err := s.loadImports(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 15*time.Second)
	defer cancel()
	s.settleImports(ctx, conn, dsn, rows)
	httpx.JSON(w, http.StatusOK, importList{Imports: s.recordsOf(conn, rows)})
	return nil
}

// importLive is a replication import as the two servers describe it now.
type importLive struct {
	Import *importRecord `json:"import"`
	// Progress is how far a dump being streamed has got.
	Progress *dbx.CopyProgress `json:"progress,omitempty"`
	// Subscription is the target's account of the copy and of the changes.
	Subscription *dbx.SubscriptionState `json:"subscription,omitempty"`
	// Lag is the source's account of how far behind the target is.
	Lag *dbx.SlotLag `json:"lag,omitempty"`
	// SourceError is why the source could not be asked, with nothing secret in it.
	SourceError string `json:"sourceError,omitempty"`
}

func (s *Server) handleImportRead(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	number, err := importID(r)
	if err != nil {
		return err
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	row, err := s.loadImport(r.Context(), id, number)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	s.settleImports(ctx, conn, dsn, []*importRow{row})
	live := importLive{Import: s.recordsOf(conn, []*importRow{row})[0]}
	if kept, ok := s.dbConns.importProgress.Load(row.ID); ok && (row.Status == importRunning) {
		progress := kept.(dbx.CopyProgress)
		live.Progress = &progress
	}
	if row.Mode == importReplication && (row.Status == importReplicating || row.Status == importCutting || row.Status == importRunning) {
		target := dbx.DSNForDatabase(conn.Driver, dsn, row.Database)
		if state, err := dbx.ReadSubscription(ctx, target, row.Name); err == nil && state.Exists {
			live.Subscription = state
			if row.sourceEnc != "" {
				source, err := s.Sealer.Open(row.sourceEnc)
				if err != nil {
					return httpx.Internal(err)
				}
				if lag, err := dbx.ReadSlotLag(ctx, source, row.Name); err != nil {
					live.SourceError = connectError(source, err)
				} else if lag.Exists {
					live.Lag = &lag
				}
			}
		}
	}
	httpx.JSON(w, http.StatusOK, live)
	return nil
}

// --- starting ----------------------------------------------------------------

type importStartRequest struct {
	Source string `json:"source"`
	// Mode is "replication" or "stream"; the engine's better one when absent.
	Mode string `json:"mode"`
	// Database is the new database on this server that receives it.
	Database string `json:"database"`
	// Name is what a replication's publication, subscription and slot are called.
	Name          string `json:"name"`
	StructureOnly bool   `json:"structureOnly"`
	// PublicationExists says the publication was made on the source by hand,
	// from the statement the inspection handed back.
	PublicationExists bool `json:"publicationExists"`
}

func (s *Server) handleImportStart(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req importStartRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	if err := importOffered(conn.Driver); err != nil {
		return err
	}
	database := strings.TrimSpace(req.Database)
	if err := newDatabaseName(conn, database); err != nil {
		return httpx.BadRequest("%v", err)
	}
	source := strings.TrimSpace(req.Source)
	if source == "" {
		return httpx.BadRequest("source is required")
	}
	view, err := sourceView(conn.Driver, source)
	if err != nil {
		return httpx.BadRequest("%v", scrub(err, source))
	}
	if conn.Driver != dbx.DriverRedis && view.Database == "" {
		return httpx.BadRequest("the connection string names no database to bring over")
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	form, err := s.importForm(ctx, conn, dsn)
	if err != nil {
		return err
	}
	mode := strings.TrimSpace(req.Mode)
	switch {
	case mode == "":
		mode = form
	case mode == importStream:
	case mode == importReplication && form == importReplication:
	default:
		return httpx.BadRequest("%q is not a way to bring a database to this server", mode)
	}
	name := strings.TrimSpace(req.Name)
	if mode == importReplication {
		if name == "" {
			name = suggestedImportName(view.Database)
		}
		if !dbx.ImportNameRe.MatchString(name) {
			return httpx.BadRequest("a name is lowercase letters, digits and underscores, starting with a letter or underscore, at most 50 long")
		}
		if req.StructureOnly {
			return httpx.BadRequest("a replication carries the rows; a structure alone is a plain dump")
		}
		if sameCluster(ctx, source, dsn) {
			return httpx.BadRequest("the source is this server itself; copy the database from its Backups page")
		}
	} else {
		name = database
		if req.StructureOnly && !dbx.DumpCapabilities(conn.Driver).SchemaOnly {
			return httpx.BadRequest("a %s database cannot be brought over without its data", conn.Driver)
		}
	}
	// Reached before a row is written or a job begun, so that a string that
	// does not open is an answer and not a failed import to forget.
	if _, err := dbx.ProbeIdentity(ctx, conn.Driver, source); err != nil {
		return httpx.Err(http.StatusBadGateway, "connect_failed", "the source: "+connectError(source, err))
	}

	sealed := ""
	if mode == importReplication {
		if sealed, err = s.Sealer.Seal(source); err != nil {
			return httpx.Internal(err)
		}
	}
	by := httpx.MustPrincipal(r).Username()
	now := time.Now().Unix()
	res, err := s.Store.DB.ExecContext(r.Context(), `
		INSERT INTO db_online_imports(connection_id, name, mode, status, target_database, source_label, source_user,
			source_enc, structure_only, started_by, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, name, mode, importRunning, database, view.label(), view.User, sealed, boolInt(req.StructureOnly), by, now, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return httpx.Err(http.StatusConflict, "import_exists", fmt.Sprintf("an import called %s is already under way on this connection", name))
		}
		return httpx.Internal(err)
	}
	number, _ := res.LastInsertId()

	base := auditBaseOf(r)
	detail := map[string]any{"name": name, "mode": mode, "database": database, "source": view.label(), "sourceUser": view.User,
		"structureOnly": req.StructureOnly}
	httpx.SetAudit(r, "database.import.start", conn.Name, detail)

	job, err := s.launchTransfer(r, conn, "import", "Import "+database, func(ctx context.Context, out jobs.Emitter) error {
		var err error
		if mode == importReplication {
			err = s.runReplicationImport(ctx, conn, dsn, source, number, name, database, req, out)
		} else {
			err = s.runStreamImport(ctx, conn, dsn, source, number, database, view, req.StructureOnly, out)
		}
		s.dbConns.importProgress.Delete(number)
		s.recordTransfer(base, "database.import.finish", conn.Name, detail, err)
		return err
	})
	if err != nil {
		// Another transfer holds the connection: the row was never a job.
		_, _ = s.Store.DB.ExecContext(context.Background(), `DELETE FROM db_online_imports WHERE id = ?`, number)
		return err
	}
	row, err := s.loadImport(r.Context(), id, number)
	if err != nil {
		return err
	}
	rec := row.importRecord
	rec.Job = &job
	httpx.JSON(w, http.StatusAccepted, importStarted{Import: &rec, Job: job})
	return nil
}

// importStarted answers a start: the record to follow, and the job doing the
// first part of it.
type importStarted struct {
	Import *importRecord `json:"import"`
	Job    jobs.Job      `json:"job"`
}

// importScratch is where an engine whose dump is a file stages it.
func (s *Server) importScratch() string { return filepath.Join(s.Cfg.BackupLocalDir, "databases") }

// tableReport writes a line when a table finishes. A job's lines are a ring of
// a few thousand; a figure written every second would push the tool's own
// words out of it within the hour.
func tableReport(out jobs.Emitter) func(dbx.CopyProgress) {
	var last string
	return func(p dbx.CopyProgress) {
		if p.Table != "" && p.Table != last {
			last = p.Table
			out.Status("%s done (%d of %d tables)", p.Table, p.TablesDone, p.Tables)
		}
	}
}

// reportProgress is tableReport that also keeps the latest figures where the
// import's page reads them.
func (s *Server) reportProgress(number int64, out jobs.Emitter) func(dbx.CopyProgress) {
	lines := tableReport(out)
	return func(p dbx.CopyProgress) {
		s.dbConns.importProgress.Store(number, p)
		lines(p)
	}
}

// makeTargetDatabase creates the database an import lands in, refusing one
// that is already there.
func (s *Server) makeTargetDatabase(ctx context.Context, conn *dbConnection, dsn, database string, out jobs.Emitter) error {
	exists, err := dbx.DatabaseExists(ctx, conn.Driver, dsn, database)
	if err != nil {
		return scrub(fmt.Errorf("could not check whether %s exists: %w", database, err), dsn)
	}
	if exists {
		return fmt.Errorf("a database called %s already exists; nothing was imported", database)
	}
	out.Status("Creating database %s", database)
	if err := dbx.CreateDatabase(ctx, conn.Driver, dsn, database); err != nil {
		return scrub(fmt.Errorf("could not create %s: %w", database, err), dsn)
	}
	return nil
}

// dropTargetDatabase removes a database an import made and did not finish.
func (s *Server) dropTargetDatabase(conn *dbConnection, dsn, database string, out jobs.Emitter) {
	if _, err := dbx.DropDatabase(context.Background(), conn.Driver, dsn, database); err == nil {
		out.Status("Removed %s, which the failed import had created", database)
	}
}

func (s *Server) runStreamImport(ctx context.Context, conn *dbConnection, dsn, source string, number int64, database string, view importSource, structureOnly bool, out jobs.Emitter) (err error) {
	defer func() {
		if err != nil {
			s.setImport(number, importFailed, err.Error())
		}
	}()
	if err := s.makeTargetDatabase(ctx, conn, dsn, database, out); err != nil {
		return err
	}
	out.Status("Reading %s and loading it into %s as it is read", view.label(), database)
	opts := dbx.CopyOptions{
		TargetDatabase: database, SchemaOnly: structureOnly, ScratchDir: s.importScratch(),
		Progress: func(line string) { out.Line("stdout", line) }, Report: s.reportProgress(number, out),
	}
	if conn.Driver == dbx.DriverRedis {
		opts.SourceDatabase = view.Database
		if opts.SourceDatabase == "" {
			opts.SourceDatabase = "0"
		}
	}
	res, err := dbx.CopyBetween(ctx, conn.Driver, source, dsn, opts)
	if err != nil {
		s.dropTargetDatabase(conn, dsn, database, out)
		return scrub(err, source, dsn)
	}
	out.Status("%s holds %s", database, res.Summary)
	emitResult(out, transferResult{Database: database, Summary: res.Summary, Tool: res.Tool})
	s.setImport(number, importDone, res.Summary)
	return nil
}

func (s *Server) runReplicationImport(ctx context.Context, conn *dbConnection, dsn, source string, number int64, name, database string, req importStartRequest, out jobs.Emitter) (err error) {
	var madePublication, madeSlot, madeDatabase bool
	defer func() {
		if err == nil {
			return
		}
		// What the import made and cannot use is taken away, so a second try
		// does not find the name taken or the slot held.
		clean := context.WithoutCancel(ctx)
		if madeSlot {
			if derr := dbx.DropSlot(clean, source, name); derr == nil {
				out.Status("Removed slot %s from the source", name)
			}
		}
		if madePublication {
			if derr := dbx.DropPublication(clean, source, name); derr == nil {
				out.Status("Removed publication %s from the source", name)
			}
		}
		if madeDatabase {
			s.dropTargetDatabase(conn, dsn, database, out)
		}
		s.setImport(number, importFailed, err.Error())
	}()

	out.Status("Checking the source")
	report, rerr := dbx.InspectReplicationSource(ctx, source)
	if rerr != nil {
		return scrub(rerr, source)
	}
	if !report.Ready() {
		return fmt.Errorf("the source cannot be replicated yet: %s", strings.Join(report.Blockers, " "))
	}
	target, rerr := dbx.InspectReplicationTarget(ctx, dsn)
	if rerr != nil {
		return scrub(rerr, dsn)
	}
	if !target.CanSubscribe {
		return fmt.Errorf("this server's login cannot create a subscription; it needs to be a superuser, or a member of pg_create_subscription on PostgreSQL 16 and later")
	}

	if err := s.makeTargetDatabase(ctx, conn, dsn, database, out); err != nil {
		return err
	}
	madeDatabase = true
	targetDSN := dbx.DSNForDatabase(conn.Driver, dsn, database)

	out.Status("Copying the structure of %s", report.Database)
	_, cerr := dbx.CopyBetween(ctx, conn.Driver, source, dsn, dbx.CopyOptions{
		TargetDatabase: database, SchemaOnly: true, Progress: func(line string) { out.Line("stdout", line) },
	})
	if cerr != nil {
		return scrub(cerr, source, dsn)
	}

	// The publication is made of the tables both servers have: a table the
	// structure copy left out (a member of an extension, say) would make the
	// subscription fail on a table that is not there.
	have, terr := dbx.TablesOf(ctx, targetDSN)
	if terr != nil {
		return scrub(terr, dsn)
	}
	var publish []dbx.ReplicationTable
	for _, t := range report.Tables {
		if t.Has(have) {
			publish = append(publish, t)
		}
	}
	if len(publish) == 0 {
		return fmt.Errorf("none of the source's tables arrived in %s, so there is nothing to replicate", database)
	}

	if req.PublicationExists {
		exists, tables, perr := dbx.PublicationExists(ctx, source, name)
		if perr != nil {
			return scrub(perr, source)
		}
		if !exists {
			return fmt.Errorf("the source has no publication called %s; run the statement shown and start again", name)
		}
		out.Status("Using the publication %s, which publishes %d tables", name, tables)
	} else {
		out.Status("Publishing %d tables on the source as %s", len(publish), name)
		if perr := dbx.CreatePublication(ctx, source, name, publish); perr != nil {
			return scrub(perr, source)
		}
		madePublication = true
		_, _ = s.Store.DB.ExecContext(context.WithoutCancel(ctx), `UPDATE db_online_imports SET publication_made = 1 WHERE id = ?`, number)
	}

	conninfo, cerr2 := dbx.ReplicationConninfo(source)
	if cerr2 != nil {
		return scrub(cerr2, source)
	}
	out.Status("Opening a replication slot called %s on the source", name)
	if serr := dbx.CreateSlot(ctx, source, name); serr != nil {
		return scrub(serr, source)
	}
	madeSlot = true
	out.Status("Subscribing %s to %s; the copy of the rows starts now", database, name)
	if serr := dbx.CreateSubscription(ctx, targetDSN, name, conninfo, name); serr != nil {
		return scrub(serr, source, dsn)
	}
	madePublication, madeSlot, madeDatabase = false, false, false
	s.setImport(number, importReplicating, "")
	out.Status("Replicating. The rows are being copied, and every change on the source after this follows.")
	emitResult(out, transferResult{Database: database, Summary: fmt.Sprintf("%d tables", len(publish))})
	return nil
}

// --- cutover -----------------------------------------------------------------

type importCutoverRequest struct {
	// WritesStopped is the operator's word that nothing is writing to the
	// source any more. The cutover waits for what was written until now, and
	// has no way to see what is written after.
	WritesStopped bool `json:"writesStopped"`
}

func (s *Server) handleImportCutover(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	number, err := importID(r)
	if err != nil {
		return err
	}
	var req importCutoverRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if !req.WritesStopped {
		return httpx.BadRequest("stop the writes to the source first, and say so with writesStopped")
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	row, err := s.loadImport(r.Context(), id, number)
	if err != nil {
		return err
	}
	if row.Mode != importReplication || row.Status != importReplicating {
		return httpx.Err(http.StatusConflict, "import_not_replicating", "only an import that is replicating can be cut over")
	}
	source, err := s.Sealer.Open(row.sourceEnc)
	if err != nil {
		return httpx.Internal(err)
	}
	// Before the job: a copy that has not finished is an answer to the
	// request, not a job that fails a moment after it began.
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	targetDSN := dbx.DSNForDatabase(conn.Driver, dsn, row.Database)
	state, err := dbx.ReadSubscription(ctx, targetDSN, row.Name)
	if err != nil {
		return connectFailed(dsn, err)
	}
	if !state.Exists {
		return httpx.Err(http.StatusConflict, "import_not_replicating",
			"the subscription is not on this server; stop the replication to clear what is left of it")
	}
	if state.Ready < len(state.Tables) {
		return httpx.Err(http.StatusConflict, "import_copying",
			fmt.Sprintf("%d of %d tables are still being copied; the cutover waits for all of them", len(state.Tables)-state.Ready, len(state.Tables)))
	}

	base := auditBaseOf(r)
	detail := map[string]any{"name": row.Name, "database": row.Database, "source": row.Source.label()}
	httpx.SetAudit(r, "database.import.cutover", conn.Name, detail)
	err = s.startTransfer(w, r, conn, "import-cutover", "Cut over "+row.Database, func(ctx context.Context, out jobs.Emitter) error {
		s.setImport(row.ID, importCutting, "")
		err := s.runCutover(ctx, conn, dsn, source, row, out)
		if err != nil {
			// Nothing was taken away before the step that failed, and every step
			// can be run again: the import is where it was.
			s.setImport(row.ID, importReplicating, err.Error())
		}
		s.recordTransfer(base, "database.import.cutover.finish", conn.Name, detail, err)
		return err
	})
	return err
}

func (s *Server) runCutover(ctx context.Context, conn *dbConnection, dsn, source string, row *importRow, out jobs.Emitter) error {
	targetDSN := dbx.DSNForDatabase(conn.Driver, dsn, row.Database)
	out.Status("Waiting for %s to apply everything the source has written", row.Database)
	last := int64(-1)
	if err := dbx.WaitCaughtUp(ctx, source, row.Name, func(remaining int64) {
		if remaining != last {
			last = remaining
			out.Status("%d bytes of changes still to apply", remaining)
		}
	}); err != nil {
		return scrub(err, source, dsn)
	}
	out.Status("Setting the sequences to the source's positions")
	set, missing, err := dbx.SyncSequences(ctx, source, targetDSN)
	if err != nil {
		return scrub(err, source, dsn)
	}
	out.Status("%d sequences set", set)
	if len(missing) > 0 {
		out.Status("%d sequences are on the source and not here: %s", len(missing), strings.Join(missing, ", "))
	}
	out.Status("Removing the subscription")
	if err := dbx.DropSubscription(ctx, targetDSN, row.Name); err != nil {
		return scrub(err, dsn)
	}
	// From here the data is the target's whatever happens to the source's side:
	// what could not be removed there is a note to the operator, not a failure
	// that would put the import back to following a source it has left.
	var left []string
	if err := dbx.DropSlot(ctx, source, row.Name); err != nil {
		left = append(left, fmt.Sprintf("The slot %s could not be removed from the source (%v). Run SELECT pg_drop_replication_slot('%s') there once it is idle, or it keeps the source's WAL.",
			row.Name, scrub(err, source), row.Name))
	}
	if row.PublicationMade {
		if err := dbx.DropPublication(ctx, source, row.Name); err != nil {
			left = append(left, fmt.Sprintf("The publication %s could not be removed from the source (%v). Run DROP PUBLICATION %s there.",
				row.Name, scrub(err, source), row.Name))
		}
	}
	for _, note := range left {
		out.Status("%s", note)
	}
	summary := fmt.Sprintf("%s is a copy of %s, and no longer follows it", row.Database, row.Source.label())
	out.Status("%s", summary)
	emitResult(out, transferResult{Database: row.Database, Summary: summary})
	s.setImport(row.ID, importDone, strings.TrimSpace(summary+". "+strings.Join(left, " ")))
	return nil
}

// --- cancelling and forgetting -----------------------------------------------

type importRemoved struct {
	// Warnings are what could not be removed from the source, each with what
	// to run there.
	Warnings []string `json:"warnings"`
}

// handleImportRemove ends an import that is replicating by taking away what it
// made on both servers, and forgets one that has ended. The database it made
// here is left: it holds what was copied, and dropping it is the server page's.
func (s *Server) handleImportRemove(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	number, err := importID(r)
	if err != nil {
		return err
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	row, err := s.loadImport(r.Context(), id, number)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 90*time.Second)
	defer cancel()
	s.settleImports(ctx, conn, dsn, []*importRow{row})
	if job := s.runningImportJob(conn.ID); job != nil && (row.Status == importRunning || row.Status == importCutting) {
		return httpx.Err(http.StatusConflict, "transfer_running", "this import is running; stop its job first").Describe("import", job.ID)
	}

	out := importRemoved{Warnings: []string{}}
	detail := map[string]any{"name": row.Name, "database": row.Database, "source": row.Source.label()}
	if row.Mode == importReplication && row.Status == importReplicating {
		targetDSN := dbx.DSNForDatabase(conn.Driver, dsn, row.Database)
		source, err := s.Sealer.Open(row.sourceEnc)
		if err != nil {
			return httpx.Internal(err)
		}
		// The target's half first, and a failure there stops: a subscription
		// that is still enabled must not lose its slot.
		if err := dbx.DropSubscription(ctx, targetDSN, row.Name); err != nil {
			return httpx.Err(http.StatusBadGateway, "query_failed", scrub(err, dsn).Error())
		}
		if err := dbx.DropSlot(ctx, source, row.Name); err != nil {
			out.Warnings = append(out.Warnings, fmt.Sprintf("The slot %s could not be removed from the source (%s). Run SELECT pg_drop_replication_slot('%s') there once it is idle, or the source keeps its WAL.",
				row.Name, scrub(err, source), row.Name))
		}
		if row.PublicationMade {
			if err := dbx.DropPublication(ctx, source, row.Name); err != nil {
				out.Warnings = append(out.Warnings, fmt.Sprintf("The publication %s could not be removed from the source (%s). Run DROP PUBLICATION %s there.",
					row.Name, scrub(err, source), row.Name))
			}
		}
		s.setImport(row.ID, importCancelled, strings.Join(out.Warnings, " "))
		httpx.SetAudit(r, "database.import.cancel", conn.Name, detail)
		httpx.JSON(w, http.StatusOK, out)
		return nil
	}
	if row.Status == importReplicating {
		return httpx.Err(http.StatusConflict, "import_replicating", "stop the replication before forgetting it")
	}
	if _, err := s.Store.DB.ExecContext(r.Context(), `DELETE FROM db_online_imports WHERE id = ? AND connection_id = ?`, row.ID, id); err != nil {
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "database.import.forget", conn.Name, detail)
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// importsActive says whether a connection has an import that holds something
// on a server: forgetting the connection would lose the way back to it.
func (s *Server) importsActive(ctx context.Context, conn int64) (bool, error) {
	var n int
	err := s.Store.DB.QueryRowContext(ctx,
		`SELECT count(*) FROM db_online_imports WHERE connection_id = ? AND status IN ('running','replicating','cutting')`, conn).Scan(&n)
	return n > 0, err
}

// refuseServerOfImport stops a server being replaced while an import into any
// of its databases is under way. The subscription lives in the server, so after
// an upgrade, its revert or a recovered copy switched in, the import would wait
// on a subscription that is no longer there while its slot holds the source's
// log.
func (s *Server) refuseServerOfImport(ctx context.Context, through *dbConnection) error {
	all, err := s.allConnections(ctx)
	if err != nil {
		return err
	}
	server := addressKey(through.Host, atoiDefault(through.Port, 0))
	for _, other := range all {
		if other.Driver != through.Driver || addressKey(other.Host, atoiDefault(other.Port, 0)) != server {
			continue
		}
		if active, err := s.importsActive(ctx, other.ID); err != nil {
			return httpx.Internal(err)
		} else if active {
			return httpx.Err(http.StatusConflict, "import_active",
				fmt.Sprintf("an import into %s on this server is still under way; finish it or stop it first", other.Name))
		}
	}
	return nil
}
