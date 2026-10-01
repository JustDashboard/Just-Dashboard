package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SQLite's answers to the operations questions.
//
// There is no server, so most of the questions change shape rather than go
// unanswered: "how busy is it" becomes "how big is the file and how much of
// it is free", "what does the server hold in memory" becomes "is there a
// write-ahead log and how long has it grown", and maintenance is the four
// commands that act on the file itself. What has no counterpart at all —
// sessions, locks held by other connections, replication — says so.

// SQLiteFile is the database as a file: what is on disk, how it is laid out,
// and what the library that opened it was built with.
type SQLiteFile struct {
	Path string `json:"path"`
	// FileBytes is the main file. WALBytes and SHMBytes are the write-ahead
	// log and its index, which exist only in WAL mode and only while
	// something has the database open.
	FileBytes int64      `json:"fileBytes"`
	WALBytes  int64      `json:"walBytes"`
	SHMBytes  int64      `json:"shmBytes"`
	Modified  *time.Time `json:"modified,omitempty"`
	PageSize  int64      `json:"pageSize"`
	PageCount int64      `json:"pageCount"`
	// FreelistPages are pages the file holds and nothing uses: what VACUUM
	// would hand back to the disk.
	FreelistPages    int64  `json:"freelistPages"`
	ReclaimableBytes int64  `json:"reclaimableBytes"`
	JournalMode      string `json:"journalMode"`
	AutoVacuum       string `json:"autoVacuum"`
	Synchronous      string `json:"synchronous"`
	Encoding         string `json:"encoding"`
	ForeignKeys      bool   `json:"foreignKeys"`
	UserVersion      int64  `json:"userVersion"`
	ApplicationID    int64  `json:"applicationId"`
	SchemaVersion    int64  `json:"schemaVersion"`
	Version          string `json:"version"`
	// Objects counts what the schema holds, by kind.
	Objects map[string]int `json:"objects"`
	// Attached lists every database the connection has open; main is the
	// file itself.
	Attached       []SQLiteAttached `json:"attached"`
	CompileOptions []string         `json:"compileOptions"`
	// SizesKnown is true when the library was built with the dbstat table,
	// without which per-table sizes cannot be read.
	SizesKnown bool `json:"sizesKnown"`
}

type SQLiteAttached struct {
	Name string `json:"name"`
	File string `json:"file"`
}

// sqliteAutoVacuum and sqliteSynchronous name the integers the pragmas answer.
var (
	sqliteAutoVacuum  = []string{"none", "full", "incremental"}
	sqliteSynchronous = []string{"off", "normal", "full", "extra"}
)

func sqliteEnum(names []string, n int64) string {
	if n >= 0 && int(n) < len(names) {
		return names[n]
	}
	return strconv.FormatInt(n, 10)
}

// sqlitePragmaInt and sqlitePragmaText read a pragma that answers one value.
// The name is always a constant of this file: a pragma cannot be bound.
func sqlitePragmaInt(ctx context.Context, db *sql.DB, name string) int64 {
	var n sql.NullInt64
	_ = db.QueryRowContext(ctx, "PRAGMA "+name).Scan(&n)
	return n.Int64
}

func sqlitePragmaText(ctx context.Context, db *sql.DB, name string) string {
	var s sql.NullString
	_ = db.QueryRowContext(ctx, "PRAGMA "+name).Scan(&s)
	return s.String
}

// ReadSQLiteFile describes the file behind a SQLite connection. The path
// comes from the connection itself rather than from the caller, so there is
// nothing here to contain: it is the file the pool already has open.
func ReadSQLiteFile(ctx context.Context, db *sql.DB) (*SQLiteFile, error) {
	out := &SQLiteFile{Objects: map[string]int{}, Attached: []SQLiteAttached{}, CompileOptions: []string{}}
	rows, err := db.QueryContext(ctx, `PRAGMA database_list`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var seq int
		var name string
		var file sql.NullString
		if err := rows.Scan(&seq, &name, &file); err != nil {
			rows.Close()
			return nil, err
		}
		out.Attached = append(out.Attached, SQLiteAttached{Name: name, File: file.String})
		if name == "main" {
			out.Path = file.String
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if out.Path != "" {
		if info, err := os.Stat(out.Path); err == nil {
			out.FileBytes = info.Size()
			mod := info.ModTime().UTC()
			out.Modified = &mod
		}
		if info, err := os.Stat(out.Path + "-wal"); err == nil {
			out.WALBytes = info.Size()
		}
		if info, err := os.Stat(out.Path + "-shm"); err == nil {
			out.SHMBytes = info.Size()
		}
	}
	out.PageSize = sqlitePragmaInt(ctx, db, "page_size")
	out.PageCount = sqlitePragmaInt(ctx, db, "page_count")
	out.FreelistPages = sqlitePragmaInt(ctx, db, "freelist_count")
	out.ReclaimableBytes = out.FreelistPages * out.PageSize
	out.JournalMode = sqlitePragmaText(ctx, db, "journal_mode")
	out.AutoVacuum = sqliteEnum(sqliteAutoVacuum, sqlitePragmaInt(ctx, db, "auto_vacuum"))
	out.Synchronous = sqliteEnum(sqliteSynchronous, sqlitePragmaInt(ctx, db, "synchronous"))
	out.Encoding = sqlitePragmaText(ctx, db, "encoding")
	out.ForeignKeys = sqlitePragmaInt(ctx, db, "foreign_keys") == 1
	out.UserVersion = sqlitePragmaInt(ctx, db, "user_version")
	out.ApplicationID = sqlitePragmaInt(ctx, db, "application_id")
	out.SchemaVersion = sqlitePragmaInt(ctx, db, "schema_version")
	_ = db.QueryRowContext(ctx, `SELECT sqlite_version()`).Scan(&out.Version)

	rows, err = db.QueryContext(ctx, `
	  SELECT type, count(*) FROM sqlite_master
	  WHERE name NOT LIKE 'sqlite\_%' ESCAPE '\' GROUP BY type`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var kind string
		var n int
		if err := rows.Scan(&kind, &n); err != nil {
			rows.Close()
			return nil, err
		}
		out.Objects[kind] = n
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	if rows, err := db.QueryContext(ctx, `PRAGMA compile_options`); err == nil {
		for rows.Next() {
			var opt string
			if rows.Scan(&opt) == nil {
				out.CompileOptions = append(out.CompileOptions, opt)
				if opt == "ENABLE_DBSTAT_VTAB" {
					out.SizesKnown = true
				}
			}
		}
		rows.Close()
	}
	return out, nil
}

// ServerStats is the file's vital signs. There are no counters: SQLite keeps
// none between connections, and a rate drawn from this connection's own
// would be a chart of the dashboard looking at itself.
func (sqliteDialect) ServerStats(ctx context.Context, db *sql.DB) (*ServerStats, error) {
	f, err := ReadSQLiteFile(ctx, db)
	if err != nil {
		return nil, err
	}
	out := &ServerStats{
		At:            time.Now().UTC(),
		Version:       "SQLite " + f.Version,
		Role:          "standalone",
		Database:      f.Path,
		DatabaseBytes: f.FileBytes + f.WALBytes,
		Counters:      map[string]float64{},
		Gauges: map[string]float64{
			"fileBytes":        float64(f.FileBytes),
			"walBytes":         float64(f.WALBytes),
			"pageSize":         float64(f.PageSize),
			"pageCount":        float64(f.PageCount),
			"freelistPages":    float64(f.FreelistPages),
			"reclaimableBytes": float64(f.ReclaimableBytes),
			"tables":           float64(f.Objects["table"]),
			"indexes":          float64(f.Objects["index"]),
		},
		Facts: map[string]string{
			"journalMode": f.JournalMode,
			"autoVacuum":  f.AutoVacuum,
			"synchronous": f.Synchronous,
			"encoding":    f.Encoding,
			"foreignKeys": strconv.FormatBool(f.ForeignKeys),
		},
	}
	return out, nil
}

// Cancel answers what Kill answers: there is no other session to stop.
func (sqliteDialect) Cancel(context.Context, *sql.DB, string) error { return ErrNoActivityView }

// --- table and index statistics ------------------------------------------------

// sqliteObjectSizes reads the bytes each table and index occupies, and the
// bytes within those pages that hold nothing, from dbstat. The table only
// exists when the library was built with it; without it both maps are nil.
func sqliteObjectSizes(ctx context.Context, db *sql.DB) (size, unused map[string]int64) {
	rows, err := db.QueryContext(ctx, `SELECT name, SUM(pgsize), SUM(unused) FROM dbstat GROUP BY name`)
	if err != nil {
		return nil, nil
	}
	defer rows.Close()
	size, unused = map[string]int64{}, map[string]int64{}
	for rows.Next() {
		var name string
		var s, u int64
		if rows.Scan(&name, &s, &u) == nil {
			size[name], unused[name] = s, u
		}
	}
	return size, unused
}

// TableStats reads sizes from dbstat and row counts from sqlite_stat1 where
// ANALYZE has written one, counting exactly where it has not. SQLite keeps no
// scan or write counters, so every one of those is -1.
func (d sqliteDialect) TableStats(ctx context.Context, db *sql.DB, opts StatsOptions) (*TableStatsReport, error) {
	out := &TableStatsReport{Schema: "main", Tables: []TableStat{}}
	rows, err := db.QueryContext(ctx, `
	  SELECT name FROM sqlite_master
	  WHERE type = 'table' AND name NOT LIKE 'sqlite\_%' ESCAPE '\' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	names := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return nil, err
		}
		names = append(names, name)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	// Which index belongs to which table, so a table's index bytes can be
	// summed from the per-object sizes.
	owner := map[string]string{}
	if irows, err := db.QueryContext(ctx, `SELECT name, tbl_name FROM sqlite_master WHERE type = 'index'`); err == nil {
		for irows.Next() {
			var index, table string
			if irows.Scan(&index, &table) == nil {
				owner[index] = table
			}
		}
		irows.Close()
	}
	size, unused := sqliteObjectSizes(ctx, db)
	if size == nil {
		out.Notes = append(out.Notes, "Per-table sizes are unavailable: this SQLite build has no dbstat table.")
	}
	indexBytes := map[string]int64{}
	for index, table := range owner {
		indexBytes[table] += size[index]
	}

	// The planner's row estimates, where ANALYZE has been run: the first
	// integer of a stat is the number of rows in the table.
	estimates := map[string]int64{}
	if srows, err := db.QueryContext(ctx, `SELECT tbl, stat FROM sqlite_stat1`); err == nil {
		for srows.Next() {
			var table string
			var stat sql.NullString
			if srows.Scan(&table, &stat) != nil {
				continue
			}
			if first, _, _ := strings.Cut(strings.TrimSpace(stat.String), " "); first != "" {
				if n, err := strconv.ParseInt(first, 10, 64); err == nil && n > estimates[table] {
					estimates[table] = n
				}
			}
		}
		srows.Close()
	}

	for _, name := range names {
		t := TableStat{Schema: "main", Table: name, Kind: "table", Rows: -1, DeadRows: -1, BloatBytes: -1,
			SeqScans: -1, SeqRowsRead: -1, IndexScans: -1, IndexRowsRead: -1,
			Inserts: -1, Updates: -1, Deletes: -1, ModsSinceStats: -1}
		if size != nil {
			t.TableBytes, t.IndexBytes = size[name], indexBytes[name]
			t.TotalBytes = t.TableBytes + t.IndexBytes
			t.BloatBytes = unused[name]
		}
		out.Tables = append(out.Tables, t)
	}
	sort.SliceStable(out.Tables, func(i, j int) bool { return out.Tables[i].TotalBytes > out.Tables[j].TotalBytes })
	if limit := opts.limit(); len(out.Tables) > limit+1 {
		out.Tables = out.Tables[:limit+1]
	}
	// Row counts only for the tables that will be shown: an exact count is a
	// scan of the table's b-tree, and a file of a thousand tables has a
	// thousand of them.
	for i := range out.Tables {
		t := &out.Tables[i]
		if n, ok := estimates[t.Table]; ok {
			t.Rows = n
			continue
		}
		q, err := d.QuoteIdent(t.Table)
		if err != nil {
			continue
		}
		var n int64
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+q).Scan(&n); err == nil {
			t.Rows = n
		}
	}
	return out, nil
}

// IndexStats walks the index list table by table. SQLite counts no index
// use, so nothing here is ever called unused; duplicates are still found.
func (d sqliteDialect) IndexStats(ctx context.Context, db *sql.DB, opts StatsOptions) (*IndexStatsReport, error) {
	out := &IndexStatsReport{Schema: "main", Indexes: []IndexStat{}}
	rows, err := db.QueryContext(ctx, `
	  SELECT m.name, m.tbl_name, COALESCE(m.sql, '') FROM sqlite_master m
	  WHERE m.type = 'index' AND (? = '' OR m.tbl_name = ?) ORDER BY m.tbl_name, m.name`, opts.Table, opts.Table)
	if err != nil {
		return nil, err
	}
	type entry struct{ name, table, sql string }
	entries := []entry{}
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.name, &e.table, &e.sql); err != nil {
			rows.Close()
			return nil, err
		}
		entries = append(entries, e)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	size, _ := sqliteObjectSizes(ctx, db)
	if size == nil {
		out.Notes = append(out.Notes, "Index sizes are unavailable: this SQLite build has no dbstat table.")
	}
	out.Notes = append(out.Notes, "SQLite keeps no index use counts, so no index can be called unused.")

	// index_list says which indexes are unique and where each came from; it
	// is read once per table rather than once per index.
	type flags struct {
		unique, partial bool
		origin          string
	}
	listed := map[string]map[string]flags{}
	for _, e := range entries {
		if _, done := listed[e.table]; done {
			continue
		}
		listed[e.table] = map[string]flags{}
		q, err := d.QuoteIdent(e.table)
		if err != nil {
			continue
		}
		lrows, err := db.QueryContext(ctx, "PRAGMA index_list("+q+")")
		if err != nil {
			continue
		}
		for lrows.Next() {
			var seq, unique, partial int
			var name, origin string
			if lrows.Scan(&seq, &name, &unique, &origin, &partial) == nil {
				listed[e.table][name] = flags{unique: unique == 1, partial: partial == 1, origin: origin}
			}
		}
		lrows.Close()
	}
	limit := opts.limit()
	for _, e := range entries {
		if len(out.Indexes) > limit {
			break
		}
		f := listed[e.table][e.name]
		ix := IndexStat{Schema: "main", Table: e.table, Name: e.name, Method: "btree", Definition: e.sql,
			Unique: f.unique, Primary: f.origin == "pk", Constraint: f.origin != "" && f.origin != "c",
			Valid: true, Scans: -1, RowsRead: -1, Columns: []string{}, plain: !f.partial}
		ix.Bytes = size[e.name]
		q, err := d.QuoteIdent(e.name)
		if err != nil {
			continue
		}
		crows, err := db.QueryContext(ctx, "PRAGMA index_info("+q+")")
		if err != nil {
			continue
		}
		for crows.Next() {
			var seqno, cid int
			var col sql.NullString
			if crows.Scan(&seqno, &cid, &col) != nil {
				continue
			}
			if !col.Valid {
				// An expression has no column name; the index is then not
				// comparable by its columns at all.
				ix.plain = false
				ix.Columns = append(ix.Columns, "(expression)")
				continue
			}
			ix.Columns = append(ix.Columns, col.String)
		}
		crows.Close()
		if !ix.plain {
			ix.signature = "partial:" + ix.Name
		}
		out.Indexes = append(out.Indexes, ix)
	}
	sortIndexStatsBySize(out.Indexes)
	return out, nil
}

// --- maintenance -------------------------------------------------------------

func (sqliteDialect) MaintenanceActions() []MaintenanceAction {
	return []MaintenanceAction{
		{ID: "analyze", Label: "Analyze", Scope: "either",
			Description: "Gathers the statistics the query planner chooses indexes from."},
		{ID: "optimize", Label: "Optimize", Scope: "database",
			Description: "Lets SQLite analyze only the tables whose statistics have gone stale. Cheap enough to run often."},
		{ID: "integrity_check", Label: "Integrity check", Scope: "database",
			Description: "Reads every page and every index entry looking for corruption. Changes nothing; on a large file it takes a while."},
		{ID: "quick_check", Label: "Quick check", Scope: "database",
			Description: "The integrity check without verifying that index entries match their rows. Much faster, and catches a damaged file."},
		{ID: "foreign_key_check", Label: "Foreign key check", Scope: "either",
			Description: "Lists rows whose foreign key points at nothing. SQLite only enforces foreign keys on connections that ask it to, so these accumulate."},
		{ID: "wal_checkpoint", Label: "Checkpoint", Scope: "database", Options: []string{"mode"},
			Description: "Moves what the write-ahead log holds into the database file. Truncate also empties the log; passive never waits for a reader."},
		{ID: "reindex", Label: "Reindex", Scope: "either",
			Description: "Rebuilds indexes from their tables, which is the repair when an index and its table disagree."},
		{ID: "vacuum", Label: "Vacuum", Scope: "database", Blocking: true,
			Description: "Rebuilds the whole file, returning free pages to the disk. Nothing else can use the database while it runs, and it needs free disk for a second copy of the file."},
	}
}

// sqliteCheckpointModes is the closed set PRAGMA wal_checkpoint takes.
var sqliteCheckpointModes = map[string]string{
	"": "TRUNCATE", "passive": "PASSIVE", "full": "FULL", "restart": "RESTART", "truncate": "TRUNCATE",
}

// sqliteMaintenanceSQL renders the one statement an action is.
func sqliteMaintenanceSQL(d sqliteDialect, req MaintenanceRequest) (string, error) {
	target := ""
	if name := orText(req.Index, req.Table); name != "" {
		q, err := d.QuoteIdent(name)
		if err != nil {
			return "", err
		}
		target = q
	}
	if req.Index != "" && req.Action != "reindex" {
		return "", maintenanceRefused("only reindex takes an index")
	}
	switch req.Action {
	case "vacuum":
		return "VACUUM", nil
	case "optimize":
		return "PRAGMA optimize", nil
	case "integrity_check":
		return "PRAGMA integrity_check", nil
	case "quick_check":
		return "PRAGMA quick_check", nil
	case "analyze":
		if target != "" {
			return "ANALYZE " + target, nil
		}
		return "ANALYZE", nil
	case "reindex":
		if target != "" {
			return "REINDEX " + target, nil
		}
		return "REINDEX", nil
	case "foreign_key_check":
		if target != "" {
			return "PRAGMA foreign_key_check(" + target + ")", nil
		}
		return "PRAGMA foreign_key_check", nil
	case "wal_checkpoint":
		mode, ok := sqliteCheckpointModes[strings.ToLower(strings.TrimSpace(req.Options.Mode))]
		if !ok {
			return "", maintenanceRefused("mode must be passive, full, restart or truncate")
		}
		return "PRAGMA wal_checkpoint(" + mode + ")", nil
	}
	return "", maintenanceRefused("unknown action %q", req.Action)
}

// orText is the first of its arguments that is not empty.
func orText(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func (d sqliteDialect) Maintain(ctx context.Context, db *sql.DB, _ string, req MaintenanceRequest) (*MaintenanceResult, error) {
	stmt, err := sqliteMaintenanceSQL(d, req)
	if err != nil {
		return nil, err
	}
	out := &MaintenanceResult{Statements: []string{stmt}, Output: []string{}, OK: true}
	switch req.Action {
	case "vacuum", "analyze", "reindex", "optimize":
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return nil, err
		}
		return out, nil
	}
	rows, err := db.QueryContext(ctx, stmt)
	if err != nil {
		return nil, err
	}
	maps, err := rowMaps(rows)
	if err != nil {
		return nil, err
	}
	switch req.Action {
	case "integrity_check", "quick_check":
		for _, row := range maps {
			for _, v := range row {
				out.Output = append(out.Output, v)
			}
		}
		// The pragma answers the single word "ok" for a sound file and one
		// line per problem otherwise.
		out.OK = len(out.Output) == 1 && out.Output[0] == "ok"
	case "foreign_key_check":
		for _, row := range maps {
			out.Output = append(out.Output, fmt.Sprintf("%s row %s references %s (foreign key %s) and nothing is there",
				row["table"], orText(row["rowid"], "without a rowid"), row["parent"], row["fkid"]))
		}
		out.OK = len(maps) == 0
	case "wal_checkpoint":
		for _, row := range maps {
			out.Output = append(out.Output, fmt.Sprintf("busy=%s log=%s checkpointed=%s", row["busy"], row["log"], row["checkpointed"]))
			// busy is 1 when a reader or writer kept the checkpoint from
			// finishing, and -1 frames when the file is not in WAL mode.
			if row["busy"] != "0" {
				out.OK = false
			}
			if row["log"] == "-1" {
				out.Output = append(out.Output, "the database is not in WAL mode, so there is no log to checkpoint")
			}
		}
	}
	return out, nil
}
