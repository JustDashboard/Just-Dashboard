package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/version"
	"github.com/go-chi/chi/v5"
)

// Moving data: out of a database as a file, into one from a file, and whole
// databases to and from the dumps kept beside the dashboard.
//
// The three kinds of work here are held to three different shapes, each for
// what it is.
//
// An export is a response body. It streams, so a table larger than memory is
// still a download, and it has to say how it ended in a way the file itself
// cannot always carry — see exportStream.
//
// An import is a request body. It streams too, and it is one transaction: the
// request is what holds it open, and the answer is the report.
//
// A dump, a restore and a copy are jobs. Each can take longer than a request
// should be held for and none should stop because a tab closed, so each is
// started by a POST that answers at once, watched through /jobs like a
// certificate order or a package upgrade, and stopped there too.

// mountDatabaseTransferRoutes registers the routes that move data in and out: import, export, dumps and generated code. It is called inside the /databases
// route, so paths are relative to it and each group states the capability it
// needs.
func (s *Server) mountDatabaseTransferRoutes(r chi.Router) {
	// Read surface. The newest dump of every connection in one read, for the
	// page that shows them all, and how an export this caller started ended.
	r.Method(http.MethodGet, "/backups/summary", s.handle(s.handleDBBackupSummary))
	r.Method(http.MethodGet, "/{id}/export/status", s.handle(s.handleDBExportStatus))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapServiceControl))
		// A statement's result as a file. It runs what the caller wrote, so
		// it sits with the query runner rather than beside the table export —
		// and takes only what the runner would call a read.
		r.Method(http.MethodPost, "/{id}/export/query", s.handle(s.handleDBExportQuery))
		// The same import as /{id}/import with the file as the body rather
		// than inside it. Replacing a table's contents is destructive and is
		// checked from the options, which the path cannot know.
		r.Method(http.MethodPost, "/{id}/import/upload", s.handle(s.handleDBImportUpload))
		// A dump made somewhere else, put where this connection's dumps are
		// so it can be restored. It adds a file and changes no database.
		r.Method(http.MethodPost, "/{id}/backups/upload", s.handle(s.handleDBBackupUpload))
	})
	r.Group(func(r chi.Router) {
		// A copy makes a database, which is what creating one from the
		// server page needs too.
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodPost, "/{id}/copy", s.handle(s.handleDBCopy))
	})
}

// --- exports -----------------------------------------------------------------

// The trailers an export declares. A client that reads trailers learns from
// them how a response that ended properly ended: with every row, or at the
// limit.
const (
	exportStatusTrailer = "X-JD-Export-Status"
	exportRowsTrailer   = "X-JD-Export-Rows"
)

// exportIDRe is the token a caller names an export by so it can ask about it
// afterwards. The caller makes it up; it only has to be unlikely to collide
// with another of the caller's own.
var exportIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

// exportState is how one export is going, or went.
type exportState struct {
	Status     string     `json:"status"`
	Rows       int        `json:"rows"`
	Format     string     `json:"format"`
	Error      string     `json:"error,omitempty"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

const (
	// exportStatesKept bounds the registry; exportStateTTL is how long an
	// outcome is worth asking about.
	exportStatesKept = 256
	exportStateTTL   = 15 * time.Minute
	exportRunning    = "running"
)

// exportRegistry remembers how recent exports ended.
//
// A browser saving a download never sees a trailer, and a CSV has nowhere to
// say "this is the first hundred thousand rows". So the page that started the
// export names it, and asks here once the download is done. It is memory, not
// a table: the answer matters for the minute after the download and to nobody
// after a restart.
type exportRegistry struct {
	mu      sync.Mutex
	entries map[string]*exportState
	order   []string
}

func exportKey(user string, conn int64, id string) string {
	return user + "\x00" + strconv.FormatInt(conn, 10) + "\x00" + id
}

func (e *exportRegistry) begin(key, format string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.entries == nil {
		e.entries = map[string]*exportState{}
	}
	now := time.Now().UTC()
	// Forget what is too old to be asked about, then the oldest of the rest.
	kept := e.order[:0]
	for _, k := range e.order {
		st := e.entries[k]
		if st != nil && now.Sub(st.StartedAt) < exportStateTTL && k != key {
			kept = append(kept, k)
			continue
		}
		delete(e.entries, k)
	}
	e.order = kept
	for len(e.order) >= exportStatesKept {
		delete(e.entries, e.order[0])
		e.order = e.order[1:]
	}
	e.entries[key] = &exportState{Status: exportRunning, Format: format, StartedAt: now}
	e.order = append(e.order, key)
}

func (e *exportRegistry) finish(key string, status dbx.ExportStatus, rows int, reason string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	st := e.entries[key]
	if st == nil {
		return
	}
	now := time.Now().UTC()
	st.Status, st.Rows, st.Error, st.FinishedAt = string(status), rows, reason, &now
}

func (e *exportRegistry) get(key string) (exportState, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	st := e.entries[key]
	if st == nil {
		return exportState{}, false
	}
	return *st, true
}

// exportStream is the response an export writes to.
//
// The headers are not sent until the first byte of the file is: until then a
// statement the engine refuses, or a server that cannot be reached, is still
// an error the caller can be answered with rather than a file called .csv with
// a sentence of JSON in it — or, as it was, an empty file and a 200.
//
// The first of the file is held for the same reason. An engine may accept a
// statement and refuse it at the third row, and an export small enough to
// still be held when that happens is answered with the error and nothing
// else.
type exportStream struct {
	w        http.ResponseWriter
	format   dbx.ExportFormat
	filename string
	held     []byte
	started  bool
}

// exportHeldBytes is how much of an export is written before any of it is
// sent.
const exportHeldBytes = 64 << 10

func (e *exportStream) Write(p []byte) (int, error) {
	if !e.started && len(e.held)+len(p) <= exportHeldBytes {
		e.held = append(e.held, p...)
		return len(p), nil
	}
	if err := e.start(); err != nil {
		return 0, err
	}
	return e.w.Write(p)
}

// start sends the headers and whatever was being held.
func (e *exportStream) start() error {
	if e.started {
		return nil
	}
	e.started = true
	h := e.w.Header()
	h.Set("Content-Type", e.format.ContentType())
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": e.filename}))
	// A file somebody asked for by pressing a button, holding rows of their
	// database: not something for a cache between here and there to keep.
	h.Set("Cache-Control", "no-store")
	h.Set("Trailer", exportStatusTrailer+", "+exportRowsTrailer)
	e.w.WriteHeader(http.StatusOK)
	held := e.held
	e.held = nil
	_, err := e.w.Write(held)
	return err
}

// exportRun is one export from the moment its options are known.
type exportRun struct {
	conn   *dbConnection
	action string
	detail map[string]any
	key    string
	stream *exportStream
}

// finishExport closes an export out: the trailers, the registry, the audit
// entry — and, when the file was cut off by a failure after it had begun, the
// connection.
//
// A response that has begun cannot turn into an error response, and one that
// simply ends looks complete: the browser saves a short file and calls it
// done. So it is not allowed to end. The connection is aborted, which every
// client reports as a failed transfer — a browser marks the download failed,
// fetch rejects, curl exits non-zero — and that is the one signal none of
// them can miss.
func (s *Server) finishExport(r *http.Request, run exportRun, rows int, truncated bool, err error) error {
	status := dbx.ExportComplete
	if truncated {
		status = dbx.ExportTruncated
	}
	detail := map[string]any{"rows": rows, "truncated": truncated}
	for k, v := range run.detail {
		detail[k] = v
	}
	if err != nil {
		status = dbx.ExportFailed
		detail["error"] = err.Error()
		delete(detail, "truncated")
	}
	if run.key != "" {
		reason := ""
		if err != nil {
			reason = err.Error()
		}
		s.dbExports.finish(run.key, status, rows, reason)
	}
	if err != nil && !run.stream.started {
		// Nothing has been sent, so this is an ordinary refusal. A GET is
		// recorded here because nothing else records one; a POST's refusal is
		// recorded by the mutation middleware like any other.
		if r.Method == http.MethodGet {
			s.recordTransfer(auditBaseOf(r), run.action, run.conn.Name, detail, err)
		} else {
			httpx.SetAudit(r, run.action, run.conn.Name, detail)
		}
		return exportError(err)
	}
	if err != nil {
		s.recordTransfer(auditBaseOf(r), run.action, run.conn.Name, detail, err)
		// What was written goes out first, the format's own closing remark
		// with it: a client that keeps what it received holds a file that
		// says it is not whole.
		_ = http.NewResponseController(run.stream.w).Flush()
		panic(http.ErrAbortHandler)
	}
	if r.Method == http.MethodGet {
		// Written directly: the mutation middleware passes a GET through with
		// nothing to annotate, and a table leaving the server is worth a line.
		s.recordAudit(r, run.action, run.conn.Name, detail)
	} else {
		httpx.SetAudit(r, run.action, run.conn.Name, detail)
	}
	if err := run.stream.start(); err != nil {
		// The client went away with the last of it unsent. There is nobody
		// left to tell.
		return nil
	}
	h := run.stream.w.Header()
	h.Set(exportStatusTrailer, string(status))
	h.Set(exportRowsTrailer, strconv.Itoa(rows))
	return nil
}

// exportError turns what stopped an export before it began into a response.
// The engine's own text is what an operator needs to see, and it describes
// their request rather than the dashboard.
func exportError(err error) error {
	var apiErr *httpx.APIError
	if errors.As(err, &apiErr) {
		return apiErr
	}
	return httpx.BadRequest("%v", err)
}

// exportBrowseOptions reads which rows an export is of, from the same
// parameters the grid's page request carries.
//
// It is the one place the export reads them, so that the day the grid's own
// reader learns a new parameter there is one line to change for the export to
// follow it.
func exportBrowseOptions(q url.Values) (dbx.BrowseOptions, error) {
	filters, err := parseFilters(q.Get("filters"))
	if err != nil {
		return dbx.BrowseOptions{}, err
	}
	return dbx.BrowseOptions{
		Schema: q.Get("schema"), Table: q.Get("table"),
		OrderBy: q.Get("orderBy"), Desc: q.Get("dir") == "desc",
		Filters: filters,
	}, nil
}

// exportColumns reads a projection: a JSON array of names, or a list
// separated by commas for names that hold none.
func exportColumns(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var cols []string
	if strings.HasPrefix(raw, "[") {
		if err := json.Unmarshal([]byte(raw), &cols); err != nil {
			return nil, fmt.Errorf("columns must be a JSON array of names: %v", err)
		}
	} else {
		cols = strings.Split(raw, ",")
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(cols))
	for _, c := range cols {
		c = strings.TrimSpace(c)
		if c == "" {
			return nil, fmt.Errorf("a column name is empty")
		}
		if seen[c] {
			return nil, fmt.Errorf("column %q is named twice", c)
		}
		seen[c] = true
		out = append(out, c)
	}
	if len(out) > 500 {
		return nil, fmt.Errorf("too many columns")
	}
	return out, nil
}

// exportFormat reads the format. None is CSV, as it always was; one this
// cannot write is refused rather than quietly written as CSV.
func exportFormat(raw string) (dbx.ExportFormat, error) {
	if raw == "" {
		return dbx.ExportCSV, nil
	}
	format := dbx.ExportFormat(strings.ToLower(raw))
	if !format.Valid() {
		return "", fmt.Errorf("format %q is not one of csv, tsv, json, ndjson, sql", raw)
	}
	return format, nil
}

// beginExport registers an export under the name its caller gave it.
func (s *Server) beginExport(r *http.Request, conn *dbConnection, exportID string, format dbx.ExportFormat) (string, error) {
	if exportID == "" {
		return "", nil
	}
	if !exportIDRe.MatchString(exportID) {
		return "", httpx.BadRequest("exportId is 8 to 64 letters, digits, dashes or underscores")
	}
	key := exportKey(httpx.MustPrincipal(r).Username(), conn.ID, exportID)
	s.dbExports.begin(key, string(format))
	return key, nil
}

// exportTable streams a table, or the part of one the grid is showing.
func (s *Server) exportTable(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	table := q.Get("table")
	if table == "" {
		return httpx.BadRequest("table is required")
	}
	format, err := exportFormat(q.Get("format"))
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	// The grid's conditions travel with the export, so a download taken from a
	// narrowed view is that view rather than the whole table. They are parsed
	// before a single response header is written: once the body has started, a
	// rejected filter can only arrive as JSON inside a file called .csv.
	browse, err := exportBrowseOptions(q)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	columns, err := exportColumns(q.Get("columns"))
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	if conn.Driver == dbx.DriverMongo {
		if format != dbx.ExportCSV && format != dbx.ExportJSON {
			return httpx.BadRequest("a collection exports as csv or json; %s is for SQL engines", format)
		}
		if len(columns) > 0 {
			return httpx.BadRequest("choosing columns is for SQL engines; a collection exports every field")
		}
	}
	key, err := s.beginExport(r, conn, q.Get("exportId"), format)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 10*time.Minute)
	defer cancel()

	limit := atoiDefault(q.Get("limit"), 0)
	run := exportRun{
		conn: conn, action: "database.export", key: key,
		detail: map[string]any{"table": table, "format": string(format), "filtered": q.Get("filters") != "" || q.Get("filter") != ""},
		stream: &exportStream{w: w, format: format, filename: exportFilename(table, format)},
	}
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
			return s.finishExport(r, run, 0, false,
				httpx.Err(http.StatusBadGateway, "connect_failed", cerr.Error()))
		}
		defer client.Disconnect(context.Background())
		database := q.Get("schema")
		if database == "" {
			database = conn.Database
		}
		count, truncated, err = dbx.MongoExport(ctx, client, database, table,
			dbx.MongoFindOptions{Filter: q.Get("filter"), Sort: q.Get("sort")},
			format, run.stream, dbx.ClampExportRows(limit))
	} else {
		pool, _, perr := s.dbPool(r.Context(), id)
		if perr != nil {
			return s.finishExport(r, run, 0, false, perr)
		}
		count, truncated, err = dbx.ExportSelection(ctx, pool, conn.Driver, browse,
			dbx.ExportOptions{Format: format, MaxRows: limit, Columns: columns}, run.stream)
	}
	return s.finishExport(r, run, count, truncated, err)
}

func exportFilename(name string, format dbx.ExportFormat) string {
	base := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, name)
	if base == "" {
		base = "export"
	}
	return base + "." + format.Extension()
}

type exportQueryRequest struct {
	SQL    string `json:"sql"`
	Format string `json:"format"`
	// Limit caps the rows written; nothing means the default.
	Limit   int      `json:"limit"`
	Columns []string `json:"columns"`
	// Schema and Table name the relation the SQL format's INSERT statements
	// are written for. A statement has no table of its own.
	Schema string `json:"schema"`
	Table  string `json:"table"`
	// Filename is the download's name, without an extension.
	Filename string `json:"filename"`
	ExportID string `json:"exportId"`
}

// handleDBExportQuery streams the result of one statement as a file.
//
// It is the query runner with a file for an answer, and holds to the runner's
// rules: one statement, classified before it runs. What it will not do is run
// anything the classifier does not read as a read — a statement that changes
// something is run from the Query page, where what it did is what is shown.
func (s *Server) handleDBExportQuery(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req exportQueryRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if strings.TrimSpace(req.SQL) == "" {
		return httpx.BadRequest("sql is required")
	}
	statement, err := dbx.SingleStatement(req.SQL)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	if risk := dbx.Classify(statement); risk.Level != "read" {
		return httpx.BadRequest("only a statement that reads can be exported; this one %s",
			strings.Join(risk.Reasons, ", "))
	}
	format, err := exportFormat(req.Format)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	for _, c := range req.Columns {
		if strings.TrimSpace(c) == "" {
			return httpx.BadRequest("a column name is empty")
		}
	}
	table := strings.TrimSpace(req.Table)
	if format == dbx.ExportSQL && table == "" {
		table = "query_result"
	}
	pool, conn, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	key, err := s.beginExport(r, conn, req.ExportID, format)
	if err != nil {
		return err
	}
	name := strings.TrimSpace(req.Filename)
	if name == "" {
		name = "query-" + time.Now().UTC().Format("20060102-150405")
	}
	ctx, cancel := timeoutCtx(r, 10*time.Minute)
	defer cancel()
	run := exportRun{
		conn: conn, action: "database.export.query", key: key,
		detail: map[string]any{"format": string(format), "statement": statement},
		stream: &exportStream{w: w, format: format, filename: exportFilename(name, format)},
	}
	count, truncated, err := dbx.ExportQuery(ctx, pool, conn.Driver, statement, dbx.ExportOptions{
		Format: format, MaxRows: req.Limit, Columns: req.Columns, Schema: req.Schema, Table: table,
	}, run.stream)
	return s.finishExport(r, run, count, truncated, err)
}

// handleDBExportStatus answers how an export the caller started and named
// ended. It is keyed by the caller as well as the name, so one account cannot
// ask after another's.
func (s *Server) handleDBExportStatus(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	id, err := parseID(r)
	if err != nil {
		return err
	}
	exportID := r.URL.Query().Get("exportId")
	if !exportIDRe.MatchString(exportID) {
		return httpx.BadRequest("exportId is 8 to 64 letters, digits, dashes or underscores")
	}
	state, ok := s.dbExports.get(exportKey(httpx.MustPrincipal(r).Username(), id, exportID))
	if !ok {
		return httpx.ErrNotFound
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, http.StatusOK, state)
	return nil
}

// --- imports -----------------------------------------------------------------

// defaultDBUploadBytes bounds an uploaded import or dump when the install
// sets no limit of its own: the same ceiling the file manager's upload has.
const defaultDBUploadBytes = maxUploadBytes

// dbUploadLimit is the most one uploaded import file or dump may hold.
func (s *Server) dbUploadLimit() int64 {
	if s.Cfg.DBUploadMaxMB > 0 {
		return int64(s.Cfg.DBUploadMaxMB) << 20
	}
	return defaultDBUploadBytes
}

type importUploadOptions struct {
	Schema    string            `json:"schema"`
	Table     string            `json:"table"`
	Format    string            `json:"format"`
	Delimiter string            `json:"delimiter"`
	Quote     *string           `json:"quote"`
	Header    *bool             `json:"header"`
	Encoding  string            `json:"encoding"`
	NullToken *string           `json:"nullToken"`
	Mapping   map[string]string `json:"mapping"`
	// Columns names each column's target by position, for a file with no
	// header row.
	Columns  []string `json:"columns"`
	Mode     string   `json:"mode"`
	Conflict *struct {
		Constraint string   `json:"constraint"`
		Columns    []string `json:"columns"`
	} `json:"conflict"`
	SkipBadRows bool `json:"skipBadRows"`
	BatchSize   int  `json:"batchSize"`
	DryRun      bool `json:"dryRun"`
	CreateTable *struct {
		Columns []dbx.NewColumn `json:"columns"`
	} `json:"createTable"`
	MaxErrors int `json:"maxErrors"`
}

// importOptionsBytes bounds the options part. A mapping of a few hundred
// columns is a few kilobytes; nothing legitimate is near this.
const importOptionsBytes = 256 << 10

// handleDBImportUpload loads an uploaded file into a table.
//
// The body is multipart, read as it arrives: an "options" part of JSON, then
// the "file" part, which goes to the importer a row at a time and is never
// held whole. The options come first because they decide what the bytes that
// follow are, and whether this caller may do what they ask.
func (s *Server) handleDBImportUpload(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, s.dbUploadLimit())
	defer drainUpload(r)
	id, err := parseID(r)
	if err != nil {
		return err
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	if conn.Driver == dbx.DriverRedis {
		return httpx.BadRequest("Redis has no tables to import into; restore a dump instead")
	}
	reader, err := r.MultipartReader()
	if err != nil {
		return httpx.BadRequest("expected a multipart upload: %v", err)
	}
	var opts *importUploadOptions
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			return httpx.BadRequest("no file part found in the upload")
		}
		if err != nil {
			return s.dbUploadError(err)
		}
		switch part.FormName() {
		case "options":
			var decoded importUploadOptions
			dec := json.NewDecoder(io.LimitReader(part, importOptionsBytes))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&decoded); err != nil {
				part.Close()
				return httpx.BadRequest("malformed import options: %v", err)
			}
			opts = &decoded
			part.Close()
			continue
		case "file":
		default:
			part.Close()
			continue
		}
		if opts == nil {
			part.Close()
			return httpx.BadRequest("the options part has to come before the file part")
		}
		defer part.Close()
		return s.importUploaded(w, r, conn, dsn, opts, part.FileName(), part)
	}
}

// importFormatFor picks the format: the one named, or the one the file's name
// says, or CSV.
func importFormatFor(named, filename string) dbx.ImportFormat {
	if named != "" {
		return dbx.ImportFormat(strings.ToLower(named))
	}
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".tsv", ".tab":
		return dbx.ImportFormatTSV
	case ".json":
		return dbx.ImportFormatJSON
	case ".ndjson", ".jsonl":
		return dbx.ImportFormatNDJSON
	}
	return dbx.ImportFormatCSV
}

func (s *Server) importUploaded(w http.ResponseWriter, r *http.Request, conn *dbConnection, dsn string, opts *importUploadOptions, filename string, body io.Reader) error {
	if strings.TrimSpace(opts.Table) == "" {
		return httpx.BadRequest("table is required")
	}
	spec := dbx.ImportSpec{
		Schema: opts.Schema, Table: opts.Table, Format: importFormatFor(opts.Format, filename),
		Delimiter: opts.Delimiter, Quote: opts.Quote, Header: opts.Header, Encoding: opts.Encoding,
		NullToken: opts.NullToken, Mapping: opts.Mapping, Columns: opts.Columns,
		Mode: dbx.ImportMode(strings.ToLower(opts.Mode)), SkipBadRows: opts.SkipBadRows,
		BatchSize: opts.BatchSize, DryRun: opts.DryRun, MaxErrors: opts.MaxErrors,
	}
	if spec.Mode == "" {
		spec.Mode = dbx.ImportModeInsert
	}
	if !spec.Mode.Valid() {
		return httpx.BadRequest("mode %q is not one of insert, upsert, replace", opts.Mode)
	}
	if opts.Conflict != nil {
		spec.ConflictConstraint, spec.ConflictColumns = opts.Conflict.Constraint, opts.Conflict.Columns
	}
	if opts.CreateTable != nil {
		spec.Create = &dbx.ImportCreate{Columns: opts.CreateTable.Columns}
	}
	// Replacing a table's contents is destructive, and the path cannot know
	// that this request does — exactly as the query runner cannot know from
	// its path whether the SQL in it deletes anything. A dry run writes
	// nothing whatever its mode.
	if spec.Mode == dbx.ImportModeReplace && !spec.DryRun {
		p := httpx.MustPrincipal(r)
		if !p.Can(auth.CapDestructive) {
			return httpx.Err(http.StatusForbidden, "forbidden",
				"replacing a table's contents is destructive and your role does not permit it")
		}
		if !s.destrLim.Allow(p.Username() + "|dbimport") {
			return httpx.Err(http.StatusTooManyRequests, "rate_limited",
				"too many destructive imports, slow down")
		}
	}
	ctx, cancel := timeoutCtx(r, 2*time.Hour)
	defer cancel()

	var (
		report *dbx.ImportReport
		err    error
		noun   = "table"
	)
	if conn.Driver == dbx.DriverMongo {
		noun = "collection"
		client, cerr := dbx.MongoClient(ctx, dsn)
		if cerr != nil {
			return httpx.Err(http.StatusBadGateway, "connect_failed", cerr.Error())
		}
		defer client.Disconnect(context.Background())
		database := opts.Schema
		if database == "" {
			database = conn.Database
		}
		if database == "" {
			return httpx.BadRequest("a database is required")
		}
		report, err = dbx.MongoImportStream(ctx, client, database, opts.Table, body, spec)
	} else {
		pool, _, perr := s.dbPool(r.Context(), conn.ID)
		if perr != nil {
			return perr
		}
		report, err = dbx.Import(ctx, pool, conn.Driver, body, spec)
	}
	if err != nil {
		if uerr := tooLarge(err, s.dbUploadLimit()); uerr != nil {
			return uerr
		}
		if spec.DryRun {
			httpx.SkipAudit(r)
		} else {
			httpx.SetAudit(r, "database.import", conn.Name,
				map[string]any{noun: opts.Table, "mode": string(spec.Mode), "error": err.Error()})
		}
		return httpx.BadRequest("%v", err)
	}
	if spec.DryRun {
		// Nothing was written, so there is nothing to record.
		httpx.SkipAudit(r)
	} else {
		detail := map[string]any{
			noun: opts.Table, "mode": string(report.Mode), "format": string(report.Format),
			"inserted": report.Inserted, "updated": report.Updated, "skipped": report.Skipped,
		}
		if report.Create != nil && report.Create.Created {
			detail["created"] = true
			detail["statement"] = report.Create.Statement
		}
		httpx.SetAudit(r, "database.import", conn.Name, detail)
	}
	httpx.JSON(w, http.StatusOK, report)
	return nil
}

// drainUpload reads what is left of an upload once its handler has finished
// with it. A refusal sent while the client is still sending is one the client
// never reads: it sees its connection close and reports a network error, with
// the reason — a table that does not exist, a row the table refused — lost. A
// dry run ends the same way, having looked at the start of the file and no
// further. The read is bounded by the limit the body already carries.
func drainUpload(r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
}

// tooLarge recognises an upload that ran into the size limit, wherever in the
// reading it surfaced.
func tooLarge(err error, limit int64) error {
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		return httpx.Err(http.StatusRequestEntityTooLarge, "too_large",
			fmt.Sprintf("the upload exceeds the %d MiB limit for one file (JD_DB_UPLOAD_MAX_MB)", limit>>20))
	}
	return nil
}

func (s *Server) dbUploadError(err error) error {
	if uerr := tooLarge(err, s.dbUploadLimit()); uerr != nil {
		return uerr
	}
	return httpx.BadRequest("upload failed: %v", err)
}

// --- the dumps on disk -------------------------------------------------------

// dumpNameRe is what a dump may be called when its name comes from a client.
// The dashboard's own names are narrower than this; an uploaded dump keeps the
// name it arrived with as long as it is a plain file name.
var dumpNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,180}$`)

// validDumpName refuses a name that is not a plain file name, and the two
// kinds of file in a dump directory that are not dumps.
func validDumpName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("file is required")
	case !dumpNameRe.MatchString(name):
		return fmt.Errorf("a dump's name is letters, digits, dots, dashes and underscores, and starts with a letter or digit")
	case dbx.IsDumpMetaFile(name):
		return fmt.Errorf("%s describes a dump; it is not one", name)
	}
	return nil
}

// resolveDumpName returns the path of one of a connection's own dumps.
//
// The client supplies a name and the containment is against the connection's
// dump directory, through the same check every other path goes through — so a
// symlink left in the directory is not a way out of it. JD_FILE_ROOTS is not
// consulted: the roots say what an operator may browse, and narrowing them
// must not stop the dashboard reading a file it wrote itself.
func (s *Server) resolveDumpName(connName, name string) (string, error) {
	if err := validDumpName(name); err != nil {
		return "", httpx.BadRequest("%v", err)
	}
	dir := s.dbDumpDir(connName)
	path, err := files.New([]string{dir}).Resolve(filepath.Join(dir, name))
	if err != nil {
		return "", mapFileError(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		return "", mapFileError(err)
	}
	if !st.Mode().IsRegular() {
		return "", httpx.BadRequest("%s is not a file", name)
	}
	return path, nil
}

// describeDump builds one listing row.
func describeDump(dir, name string, info os.FileInfo) dbBackupFile {
	path := filepath.Join(dir, name)
	row := dbBackupFile{File: name, Size: info.Size(), TakenAt: info.ModTime().UTC(), Format: dbx.DumpKind(path)}
	meta := dbx.ReadDumpMeta(path)
	if meta == nil {
		return row
	}
	if !meta.StartedAt.IsZero() {
		row.TakenAt = meta.StartedAt.UTC()
	}
	if meta.Origin != dbx.DumpOriginUpload {
		duration := meta.DurationMs
		row.DurationMs = &duration
	}
	contents := meta.Contents
	row.Database, row.Tool, row.ToolVersion = meta.Database, meta.Tool, meta.ToolVersion
	row.Summary, row.Contents, row.Note, row.Origin, row.By = meta.Summary, &contents, meta.Note, meta.Origin, meta.By
	return row
}

// dumpEntries lists the dump files in a directory, without opening any. A
// directory that does not exist yet is an empty list, not an error.
func dumpEntries(dir string) ([]os.FileInfo, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := []os.FileInfo{}
	for _, e := range entries {
		// Not a dump: a description, or a file still being uploaded or
		// written under a temporary name.
		if e.IsDir() || dbx.IsDumpMetaFile(e.Name()) || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		out = append(out, info)
	}
	return out, nil
}

type dbBackupSummaryRow struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	// Count and TotalSize are of every dump the connection has.
	Count     int           `json:"count"`
	TotalSize int64         `json:"totalSize"`
	Newest    *dbBackupFile `json:"newest"`
	// Job is the dump, restore or copy running against it now.
	Job *jobs.Job `json:"job,omitempty"`
}

// handleDBBackupSummary reports the newest dump of every connection in one
// read: what the page that lists them all needs to say which have a backup
// and how old it is, without a request per connection. It reads directory
// entries and one description each; it dials nothing.
func (s *Server) handleDBBackupSummary(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT id, name FROM db_connections ORDER BY name`)
	if err != nil {
		return httpx.Internal(err)
	}
	defer rows.Close()
	out := []dbBackupSummaryRow{}
	for rows.Next() {
		var row dbBackupSummaryRow
		if err := rows.Scan(&row.ID, &row.Name); err != nil {
			return httpx.Internal(err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return httpx.Internal(err)
	}
	for i := range out {
		dir := s.dbDumpDir(out[i].Name)
		entries, err := dumpEntries(dir)
		if err != nil {
			// A directory that cannot be read has no dumps this can vouch for.
			continue
		}
		var newest os.FileInfo
		for _, info := range entries {
			out[i].Count++
			out[i].TotalSize += info.Size()
			if newest == nil || info.ModTime().After(newest.ModTime()) {
				newest = info
			}
		}
		if newest != nil {
			described := describeDump(dir, newest.Name(), newest)
			out[i].Newest = &described
		}
		if job, running := s.runningTransfer(out[i].ID); running {
			out[i].Job = &job
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"connections": out})
	return nil
}

// maxDumpNoteBytes bounds the note kept with a dump.
const maxDumpNoteBytes = 500

// handleDBBackupUpload puts a dump made elsewhere among a connection's own.
//
// The file is streamed into the connection's dump directory through the same
// contained writer the file manager uploads with — into a temporary name and
// renamed, so a transfer that stops halfway leaves nothing that looks like a
// dump. Nothing is restored: that is the restore route's, and its capability.
func (s *Server) handleDBBackupUpload(w http.ResponseWriter, r *http.Request) error {
	limit := s.dbUploadLimit()
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	defer drainUpload(r)
	id, err := parseID(r)
	if err != nil {
		return err
	}
	conn, _, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	note := strings.TrimSpace(q.Get("note"))
	if len(note) > maxDumpNoteBytes {
		return httpx.BadRequest("a note is at most %d bytes", maxDumpNoteBytes)
	}
	reader, err := r.MultipartReader()
	if err != nil {
		return httpx.BadRequest("expected a multipart upload: %v", err)
	}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			return httpx.BadRequest("no file part found in the upload")
		}
		if err != nil {
			return s.dbUploadError(err)
		}
		if part.FormName() != "file" {
			part.Close()
			continue
		}
		defer part.Close()
		// Only the base name is honoured: a client can put a path in the
		// filename field, and joining it blindly would let an upload land
		// anywhere.
		name := q.Get("name")
		if name == "" {
			name = filepath.Base(part.FileName())
		}
		if err := validDumpName(name); err != nil {
			return httpx.BadRequest("%v", err)
		}
		dir := s.dbDumpDir(conn.Name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return httpx.Internal(err)
		}
		path := filepath.Join(dir, name)
		size, err := files.New([]string{dir}).Upload(path, part, false)
		if err != nil {
			if uerr := tooLarge(err, limit); uerr != nil {
				return uerr
			}
			return mapFileError(err)
		}
		// A dump is a database's contents. It is kept as private as the ones
		// the dashboard writes itself.
		if err := os.Chmod(path, 0o600); err != nil {
			return httpx.Internal(err)
		}
		meta := dbx.DumpMeta{
			File: name, Connection: conn.Name, Driver: conn.Driver, StartedAt: time.Now().UTC(),
			Size: size, Note: note, Origin: dbx.DumpOriginUpload, By: httpx.MustPrincipal(r).Username(),
		}
		if err := dbx.WriteDumpMeta(path, meta); err != nil {
			return httpx.Internal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			return httpx.Internal(err)
		}
		httpx.SetAudit(r, "database.backup.upload", conn.Name, map[string]any{"file": name, "size": size})
		httpx.JSON(w, http.StatusCreated, describeDump(dir, name, info))
		return nil
	}
}

// --- dump, restore and copy as jobs ------------------------------------------

// transferTimeout bounds one dump, restore or copy. It is generous because
// the work is the size of somebody's database, and stopping it is a button.
const transferTimeout = 12 * time.Hour

// transferPrefix is the kind every transfer job of one connection starts
// with. The trailing dot is what keeps connection 1 from matching 12.
func transferPrefix(id int64) string { return fmt.Sprintf("database.transfer.%d.", id) }

// runningTransfer returns the dump, restore or copy running against a
// connection now.
func (s *Server) runningTransfer(id int64) (jobs.Job, bool) {
	prefix := transferPrefix(id)
	for _, job := range s.modules.jobs.List() {
		if job.Status == jobs.StatusRunning && strings.HasPrefix(job.Kind, prefix) {
			return job, true
		}
	}
	return jobs.Job{}, false
}

// startTransfer starts a job unless the connection already has one.
//
// One at a time, and the check is the same lock as the start, so two clicks
// cannot both get through. A second dump of a database being dumped doubles
// the load for a second copy of the same thing; a dump of a database being
// restored is a dump of neither state; a restore into one being dumped pulls
// the tables out from under the reader.
func (s *Server) startTransfer(w http.ResponseWriter, r *http.Request, conn *dbConnection, what, title string, run jobs.Runner) error {
	spec := jobs.Spec{
		Kind: transferPrefix(conn.ID) + what, Title: title, Target: conn.Name,
		Timeout: transferTimeout, StartedBy: httpx.MustPrincipal(r).Username(),
	}
	job, started := s.modules.jobs.StartExclusive(transferPrefix(conn.ID), spec, run)
	if !started {
		return httpx.Err(http.StatusConflict, "transfer_running",
			fmt.Sprintf("%s is already running for this connection (started by %s). Wait for it to finish, or stop it.",
				job.Title, job.StartedBy)).Describe(what, job.ID)
	}
	httpx.JSON(w, http.StatusAccepted, job)
	return nil
}

// transferResult is the last line a transfer job writes, on the "result"
// stream: what it produced, for the page that started it.
type transferResult struct {
	// File is the dump written, or the dump restored from.
	File     string `json:"file,omitempty"`
	Size     int64  `json:"size,omitempty"`
	Database string `json:"database,omitempty"`
	Summary  string `json:"summary,omitempty"`
	Tool     string `json:"tool,omitempty"`
	// SafetyDump is the dump taken of the database before it was replaced.
	SafetyDump string `json:"safetyDump,omitempty"`
	Output     string `json:"output,omitempty"`
}

func emitResult(out jobs.Emitter, res transferResult) {
	if b, err := json.Marshal(res); err == nil {
		out.Line("result", string(b))
	}
}

// auditBaseOf captures who is asking, for an entry written after the request
// that asked has been answered.
func auditBaseOf(r *http.Request) audit.Entry {
	p := httpx.MustPrincipal(r)
	return audit.Entry{
		UserID: p.UserID(), Username: p.Username(), Role: string(p.Role),
		IP: httpx.ClientIP(r), Actor: p.Kind, Method: r.Method, Path: r.URL.Path,
	}
}

// recordTransfer writes how a transfer ended. The request that started a job
// is recorded when it is answered, which is before anything has happened; what
// happened is this second entry. An export that failed after its response had
// begun is recorded the same way, since its request never completes.
func (s *Server) recordTransfer(base audit.Entry, action, target string, detail map[string]any, err error) {
	base.Action, base.Target = action, target
	base.Status, base.Success = http.StatusOK, true
	if err != nil {
		base.Status, base.Success = http.StatusBadGateway, false
		if detail == nil {
			detail = map[string]any{}
		}
		if _, has := detail["error"]; !has {
			detail["error"] = err.Error()
		}
	}
	base.Detail = audit.Detail(detail)
	// Background, not the job's context: a job that was stopped still has an
	// ending worth recording.
	s.Audit.Record(context.Background(), base)
}

type dbBackupRequest struct {
	Database      string   `json:"database"`
	SchemaOnly    bool     `json:"schemaOnly"`
	DataOnly      bool     `json:"dataOnly"`
	Tables        []string `json:"tables"`
	ExcludeTables []string `json:"excludeTables"`
	Compression   string   `json:"compression"`
	// Databases is which numbered databases a Redis dump covers; none means
	// every one that holds a key.
	Databases []int  `json:"databases"`
	Note      string `json:"note"`
}

func (req dbBackupRequest) options() dbx.DumpOptions {
	return dbx.DumpOptions{
		Database: strings.TrimSpace(req.Database), SchemaOnly: req.SchemaOnly, DataOnly: req.DataOnly,
		Tables: req.Tables, ExcludeTables: req.ExcludeTables,
		Compression: strings.ToLower(strings.TrimSpace(req.Compression)), RedisDatabases: req.Databases,
	}
}

// takeDump writes a dump into the connection's directory and its description
// beside it.
func (s *Server) takeDump(ctx context.Context, conn *dbConnection, dsn string, opts dbx.DumpOptions, note, origin, by string, out jobs.Emitter) (*dbx.DumpResult, error) {
	opts.Progress = func(line string) { out.Line("stdout", line) }
	dir := s.dbDumpDir(conn.Name)
	res, err := dbx.DumpWith(ctx, conn.Driver, dsn, dir, opts)
	if err != nil {
		return nil, err
	}
	meta := dbx.MetaOf(res, opts)
	meta.Connection, meta.Note, meta.Origin, meta.By = conn.Name, note, origin, by
	if res.Tool == dbx.BuiltInDumpTool {
		meta.ToolVersion = version.Version
	}
	if err := dbx.WriteDumpMeta(res.Path, meta); err != nil {
		// The dump is whole and where it should be. Without its description
		// it is listed as a file of unknown provenance, which is a loss worth
		// saying and not one worth failing the dump for.
		out.Status("The dump was written, but its description could not be saved: %v", err)
	}
	return res, nil
}

// startDBBackup answers POST /{id}/backup.
func (s *Server) startDBBackup(w http.ResponseWriter, r *http.Request) error {
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
	opts := req.options()
	note := strings.TrimSpace(req.Note)
	if len(note) > maxDumpNoteBytes {
		return httpx.BadRequest("a note is at most %d bytes", maxDumpNoteBytes)
	}
	// Refused here rather than inside the job: an option the engine cannot
	// honour is something wrong with the request, and the answer to that is
	// a 400 now, not a failed job a moment later.
	if err := dbx.ValidateDumpOptions(conn.Driver, opts); err != nil {
		return httpx.BadRequest("%v", err)
	}
	base, by := auditBaseOf(r), httpx.MustPrincipal(r).Username()
	detail := map[string]any{"database": opts.Database, "contents": dbx.ContentsOf(opts)}
	httpx.SetAudit(r, "database.backup", conn.Name, detail)
	return s.startTransfer(w, r, conn, "backup", "Dump "+conn.Name, func(ctx context.Context, out jobs.Emitter) error {
		out.Status("Dumping %s", conn.Name)
		res, err := s.takeDump(ctx, conn, dsn, opts, note, dbx.DumpOriginDump, by, out)
		if err != nil {
			s.recordTransfer(base, "database.backup.finish", conn.Name, map[string]any{"database": opts.Database}, err)
			return err
		}
		out.Status("%s written: %s", res.File, res.Summary)
		emitResult(out, transferResult{File: res.File, Size: res.Size, Database: res.Database, Summary: res.Summary, Tool: res.Tool})
		s.recordTransfer(base, "database.backup.finish", conn.Name,
			map[string]any{"database": res.Database, "file": res.File, "size": res.Size, "tool": res.Tool}, nil)
		return nil
	})
}

type dbRestoreRequest struct {
	// Database is an existing database on the same server to restore into
	// instead of the connection's own.
	Database string `json:"database"`
	// File names one of the connection's own dumps. DumpPath is the older
	// spelling, and also the way to name a file elsewhere under the roots.
	File     string `json:"file"`
	DumpPath string `json:"dumpPath"`
	// Target is "this", or {"newDatabase": name} to restore into a database
	// made for it.
	Target json.RawMessage `json:"target"`
	// DumpFirst takes a dump of the database as it is before replacing it.
	DumpFirst bool `json:"dumpFirst"`
}

// restoreTarget reads the target: the empty string for the database itself,
// or the name of the one to create.
func (req dbRestoreRequest) restoreTarget() (string, error) {
	if len(req.Target) == 0 || string(req.Target) == "null" {
		return "", nil
	}
	var word string
	if err := json.Unmarshal(req.Target, &word); err == nil {
		if word == "this" || word == "" {
			return "", nil
		}
		return "", fmt.Errorf(`target is "this" or {"newDatabase": name}`)
	}
	var object struct {
		NewDatabase string `json:"newDatabase"`
	}
	dec := json.NewDecoder(strings.NewReader(string(req.Target)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&object); err != nil || strings.TrimSpace(object.NewDatabase) == "" {
		return "", fmt.Errorf(`target is "this" or {"newDatabase": name}`)
	}
	return strings.TrimSpace(object.NewDatabase), nil
}

// resolveRestoreSource finds the dump a restore reads.
//
// One of the connection's own dumps is resolved against its dump directory —
// the scope download and delete use — whether it was named or given as the
// path the listing reports. So narrowing JD_FILE_ROOTS cannot stop the
// dashboard restoring a dump it took. Anything else is a client-supplied path
// and goes through files.Resolve like every other one (invariant 6).
func (s *Server) resolveRestoreSource(conn *dbConnection, req dbRestoreRequest) (string, error) {
	if req.File != "" {
		return s.resolveDumpName(conn.Name, req.File)
	}
	if strings.TrimSpace(req.DumpPath) == "" {
		return "", httpx.BadRequest("file is required")
	}
	dir := s.dbDumpDir(conn.Name)
	if abs, err := filepath.Abs(filepath.Clean(req.DumpPath)); err == nil && filepath.Dir(abs) == filepath.Clean(dir) {
		return s.resolveDumpName(conn.Name, filepath.Base(abs))
	}
	path, err := s.modules.files.Resolve(req.DumpPath)
	if err != nil {
		return "", httpx.BadRequest("%v", err)
	}
	st, err := os.Stat(path)
	if err != nil {
		return "", mapFileError(err)
	}
	if !st.Mode().IsRegular() {
		return "", httpx.BadRequest("%s is not a file", req.DumpPath)
	}
	return path, nil
}

// newDatabaseName checks a database to be created for a restore or a copy.
func newDatabaseName(conn *dbConnection, name string) error {
	if !dbx.DumpCapabilities(conn.Driver).NewDatabase {
		return fmt.Errorf("%s has no second database to restore into on the same connection", conn.Driver)
	}
	if conn.Driver == dbx.DriverRedis {
		if n, err := strconv.Atoi(name); err != nil || n < 0 {
			return fmt.Errorf("Redis databases are numbered; %q is not a number", name)
		}
		return nil
	}
	if !dbNameRe.MatchString(name) {
		return fmt.Errorf("a database name is letters, digits and underscores")
	}
	if name == conn.Database {
		return fmt.Errorf("%s is the database this connection is already on", name)
	}
	return nil
}

// startDBRestore answers POST /{id}/restore.
func (s *Server) startDBRestore(w http.ResponseWriter, r *http.Request) error {
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
	create, err := req.restoreTarget()
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	target := strings.TrimSpace(req.Database)
	if create != "" {
		if target != "" && target != create {
			return httpx.BadRequest("database and target name two different databases")
		}
		if err := newDatabaseName(conn, create); err != nil {
			return httpx.BadRequest("%v", err)
		}
		// Creating a database is the server page's act, and takes what it
		// takes there. The path cannot know this request does.
		if !httpx.MustPrincipal(r).Can(auth.CapSystemAdmin) {
			return httpx.Err(http.StatusForbidden, "forbidden",
				"restoring into a new database creates one, and your role does not permit it")
		}
		if req.DumpFirst {
			return httpx.BadRequest("a new database has no current state to dump first")
		}
		target = create
	}
	// Invariant 6: the dump is resolved before anything else happens, through
	// the dump directory's own scope or files.Resolve.
	dumpPath, err := s.resolveRestoreSource(conn, req)
	if err != nil {
		return err
	}
	shown := target
	if shown == "" {
		shown = conn.Database
	}
	file := filepath.Base(dumpPath)
	base, by := auditBaseOf(r), httpx.MustPrincipal(r).Username()
	detail := map[string]any{"database": shown, "file": file, "dumpFirst": req.DumpFirst, "newDatabase": create != ""}
	httpx.SetAudit(r, "database.restore", conn.Name, detail)

	title := "Restore " + conn.Name
	if create != "" {
		title = "Restore into " + create
	}
	return s.startTransfer(w, r, conn, "restore", title, func(ctx context.Context, out jobs.Emitter) error {
		result := transferResult{File: file, Database: shown}
		output, err := s.runRestore(ctx, conn, dsn, dumpPath, target, create != "", req.DumpFirst, by, out, &result)
		outcome := map[string]any{"database": shown, "file": file}
		if result.SafetyDump != "" {
			outcome["safetyDump"] = result.SafetyDump
		}
		if err != nil {
			// Even a failed restore says where the copy of what was there is.
			emitResult(out, result)
			s.recordTransfer(base, "database.restore.finish", conn.Name, outcome, err)
			return err
		}
		result.Output = output
		out.Status("Restored %s into %s", file, shown)
		emitResult(out, result)
		s.recordTransfer(base, "database.restore.finish", conn.Name, outcome, nil)
		return nil
	})
}

// runRestore is the work of a restore job: the optional dump of what is
// there, the optional new database, and the load.
func (s *Server) runRestore(ctx context.Context, conn *dbConnection, dsn, dumpPath, target string, create, dumpFirst bool, by string, out jobs.Emitter, result *transferResult) (string, error) {
	file := filepath.Base(dumpPath)
	if dumpFirst {
		// Before anything is touched. There is no point-in-time recovery on a
		// server like this one: a dump taken now is the only way back from a
		// restore of the wrong file.
		out.Status("Dumping the database as it is, before replacing it")
		safety, err := s.takeDump(ctx, conn, dsn, dbx.DumpOptions{Database: target},
			"Taken before restoring "+file, dbx.DumpOriginSafety, by, out)
		if err != nil {
			return "", fmt.Errorf("the dump of the current state failed, so nothing was restored: %w", err)
		}
		result.SafetyDump = safety.File
		out.Status("Current state kept as %s", safety.File)
	}
	if create {
		exists, err := dbx.DatabaseExists(ctx, conn.Driver, dsn, target)
		if err != nil {
			return "", fmt.Errorf("could not check whether %s exists: %w", target, err)
		}
		if exists {
			return "", fmt.Errorf("a database called %s already exists; nothing was restored", target)
		}
		out.Status("Creating database %s", target)
		if err := dbx.CreateDatabase(ctx, conn.Driver, dsn, target); err != nil {
			return "", fmt.Errorf("could not create %s: %w", target, err)
		}
	}
	// The dashboard's own connections to the database go first. A SQLite file
	// is loaded through the engine and an open handle would hold a lock on
	// it; on the servers, a pooled session would otherwise carry plans for
	// tables that are about to be replaced.
	if !create && conn.Driver.IsSQL() {
		s.modules.dbs.Close(conn.ID)
	}
	out.Status("Restoring %s", file)
	opts := dbx.RestoreOptions{Database: target, Progress: func(line string) { out.Line("stdout", line) }}
	if meta := dbx.ReadDumpMeta(dumpPath); meta != nil {
		opts.SourceDatabase = meta.Database
	}
	output, err := dbx.RestoreWith(ctx, conn.Driver, dsn, dumpPath, opts)
	if err != nil && create {
		// Made a moment ago for this restore and never whole: it goes, so a
		// second attempt does not find the name taken.
		if _, derr := dbx.DropDatabase(context.WithoutCancel(ctx), conn.Driver, dsn, target); derr == nil {
			out.Status("Removed %s, which the failed restore had created", target)
		}
	}
	if !create && conn.Driver.IsSQL() {
		s.modules.dbs.Close(conn.ID)
	}
	return output, err
}

type dbCopyRequest struct {
	Name          string `json:"name"`
	StructureOnly bool   `json:"structureOnly"`
}

// handleDBCopy copies the connection's database into a new one on the same
// server: a dump, a database made to receive it, and a restore, as one job.
//
// The dump is the job's own and is removed when it ends; it is not one of the
// connection's dumps and is never listed as one.
func (s *Server) handleDBCopy(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req dbCopyRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	name := strings.TrimSpace(req.Name)
	if err := newDatabaseName(conn, name); err != nil {
		return httpx.BadRequest("%v", err)
	}
	opts := dbx.DumpOptions{SchemaOnly: req.StructureOnly}
	if conn.Driver == dbx.DriverRedis {
		// The connection's own numbered database, not every one on the
		// server: a copy is of one database into one.
		opts.Database = conn.Database
		if opts.Database == "" {
			opts.Database = "0"
		}
	}
	if err := dbx.ValidateDumpOptions(conn.Driver, opts); err != nil {
		return httpx.BadRequest("%v", err)
	}
	base := auditBaseOf(r)
	detail := map[string]any{"from": conn.Database, "to": name, "structureOnly": req.StructureOnly}
	httpx.SetAudit(r, "database.copy", conn.Name, detail)
	return s.startTransfer(w, r, conn, "copy", "Copy "+conn.Name+" to "+name, func(ctx context.Context, out jobs.Emitter) error {
		err := s.runCopy(ctx, conn, dsn, name, opts, out)
		s.recordTransfer(base, "database.copy.finish", conn.Name, detail, err)
		return err
	})
}

func (s *Server) runCopy(ctx context.Context, conn *dbConnection, dsn, name string, opts dbx.DumpOptions, out jobs.Emitter) error {
	exists, err := dbx.DatabaseExists(ctx, conn.Driver, dsn, name)
	if err != nil {
		return fmt.Errorf("could not check whether %s exists: %w", name, err)
	}
	if exists {
		return fmt.Errorf("a database called %s already exists; nothing was copied", name)
	}
	// Beside the dump directories rather than in the system's temporary one:
	// a copy is the size of a database, and that is the volume sized for it.
	parent := filepath.Join(s.Cfg.BackupLocalDir, "databases")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	dir, err := os.MkdirTemp(parent, ".copy-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	out.Status("Dumping %s", conn.Database)
	opts.Progress = func(line string) { out.Line("stdout", line) }
	res, err := dbx.DumpWith(ctx, conn.Driver, dsn, dir, opts)
	if err != nil {
		return err
	}
	out.Status("Creating database %s", name)
	if err := dbx.CreateDatabase(ctx, conn.Driver, dsn, name); err != nil {
		return fmt.Errorf("could not create %s: %w", name, err)
	}
	out.Status("Loading the dump into %s", name)
	_, err = dbx.RestoreWith(ctx, conn.Driver, dsn, res.Path, dbx.RestoreOptions{
		Database: name, SourceDatabase: res.Database,
		Progress: func(line string) { out.Line("stdout", line) },
	})
	if err != nil {
		if _, derr := dbx.DropDatabase(context.WithoutCancel(ctx), conn.Driver, dsn, name); derr == nil {
			out.Status("Removed %s, which the failed copy had created", name)
		}
		return err
	}
	out.Status("%s is a copy of %s: %s", name, res.Database, res.Summary)
	emitResult(out, transferResult{Database: name, Summary: res.Summary, Tool: res.Tool})
	return nil
}
