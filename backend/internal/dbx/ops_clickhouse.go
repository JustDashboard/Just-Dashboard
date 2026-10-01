package dbx

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// ClickHouse's answers to the operations questions, and the four views that
// are its own: parts, merges, mutations and running queries.
//
// A MergeTree table is a pile of immutable parts that the server merges in
// the background, and nearly everything that goes wrong with one is visible
// there before it is visible anywhere else — inserts arriving faster than
// merges can fold them, a mutation that has been rewriting the same parts
// since Tuesday, a query that has read forty billion rows. None of the other
// engines has anything like it, so these are not pressed into the shapes the
// others share.

// --- snapshot ----------------------------------------------------------------

// The three system tables the snapshot reads, which of their rows, and the
// name each is published under. Each table has hundreds of rows; these are
// the ones the page draws. The names are the dashboard's own because the
// engine's are in three different casings and several say "OS" or "HTTP".
var (
	// system.metrics: readings of now.
	clickhouseMetrics = map[string]string{
		"Query": "runningQueries", "Merge": "runningMerges", "PartMutation": "runningMutations",
		"TCPConnection": "tcpConnections", "HTTPConnection": "httpConnections",
		"MySQLConnection": "mysqlConnections", "PostgreSQLConnection": "postgresConnections",
		"InterserverConnection": "interserverConnections", "MemoryTracking": "memoryTracking",
		"DelayedInserts": "delayedInserts", "ReadonlyReplica": "readonlyReplicas",
		"BackgroundMergesAndMutationsPoolTask": "backgroundTasks",
	}
	// system.events: running totals since the server started.
	clickhouseEvents = map[string]string{
		"Query": StatQueries, "SelectQuery": "selectQueries", "InsertQuery": "insertQueries",
		"FailedQuery": "failedQueries", "FailedSelectQuery": "failedSelectQueries",
		"FailedInsertQuery": "failedInsertQueries", "InsertedRows": "insertedRows",
		"InsertedBytes": "insertedBytes", "SelectedRows": "selectedRows", "SelectedBytes": "selectedBytes",
		"MergedRows": "mergedRows", "Merge": "merges", "MergedUncompressedBytes": "mergedBytes",
		"QueryTimeMicroseconds": "queryMicroseconds", "OSReadBytes": "diskReadBytes",
		"OSWriteBytes": "diskWriteBytes", "MarkCacheHits": "markCacheHits", "MarkCacheMisses": "markCacheMisses",
		"NetworkReceiveBytes": "bytesReceived", "NetworkSendBytes": "bytesSent",
		"DelayedInserts": "delayedInserts", "RejectedInserts": "rejectedInserts",
	}
	// system.asynchronous_metrics: readings the server refreshes every second.
	clickhouseAsync = map[string]string{
		"MemoryResident": "memoryResident", "NumberOfTables": "tables", "NumberOfDatabases": "databases",
		"TotalPartsOfMergeTreeTables": "totalParts", "TotalRowsOfMergeTreeTables": "totalRows",
		"TotalBytesOfMergeTreeTables": "totalBytes", "MaxPartCountForPartition": "maxPartsPerPartition",
		"OSMemoryTotal": "hostMemoryBytes", "LoadAverage1": "loadAverage1",
	}
)

func mapKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// clickhouseNamed reads name/value rows for a fixed set of names out of one
// of the system tables. The table and column names are constants of this
// file; the names are bound.
func clickhouseNamed(ctx context.Context, db *sql.DB, table, nameCol string, names []string) (map[string]float64, error) {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(names)), ",")
	args := make([]any, len(names))
	for i, n := range names {
		args[i] = n
	}
	rows, err := db.QueryContext(ctx, "SELECT "+nameCol+", toFloat64(value) FROM "+table+" WHERE "+nameCol+" IN ("+placeholders+")", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var name string
		var value float64
		if err := rows.Scan(&name, &value); err != nil {
			return nil, err
		}
		out[name] = value
	}
	return out, rows.Err()
}

func (clickhouseDialect) ServerStats(ctx context.Context, db *sql.DB) (*ServerStats, error) {
	out := &ServerStats{Counters: map[string]float64{}, Gauges: map[string]float64{}, Facts: map[string]string{}}
	var uptime float64
	if err := db.QueryRowContext(ctx, `SELECT version(), toFloat64(uptime()), currentDatabase()`).Scan(&out.Version, &uptime, &out.Database); err != nil {
		return nil, err
	}
	out.At = time.Now().UTC()
	out.UptimeSeconds = uptime
	started := out.At.Add(-time.Duration(uptime * float64(time.Second)))
	out.StartedAt = &started
	out.Version = "ClickHouse " + out.Version

	note := func(what string, err error) {
		out.Notes = append(out.Notes, what+" could not be read: "+err.Error())
	}
	metrics, err := clickhouseNamed(ctx, db, "system.metrics", "metric", mapKeys(clickhouseMetrics))
	if err != nil {
		note("system.metrics", err)
	}
	events, err := clickhouseNamed(ctx, db, "system.events", "event", mapKeys(clickhouseEvents))
	if err != nil {
		note("system.events", err)
	}
	async, err := clickhouseNamed(ctx, db, "system.asynchronous_metrics", "metric", mapKeys(clickhouseAsync))
	if err != nil {
		note("system.asynchronous_metrics", err)
	}

	if events != nil {
		// system.events only lists an event once it has happened, so a
		// missing row is a zero and is reported as one: a counter that
		// appears halfway through a chart draws as a spike.
		for name, key := range clickhouseEvents {
			out.Counters[key] = events[name]
		}
		out.Counters[StatRowsRead] = events["SelectedRows"]
		out.Counters[StatRowsWritten] = events["InsertedRows"]
		// The mark cache is the nearest thing ClickHouse has to a buffer
		// pool: a hit is an index granule found in memory.
		out.Counters[StatBlocksHit] = events["MarkCacheHits"]
		out.Counters[StatBlocksRead] = events["MarkCacheMisses"]
	}
	for name, v := range metrics {
		out.Gauges[clickhouseMetrics[name]] = v
	}
	for name, v := range async {
		out.Gauges[clickhouseAsync[name]] = v
	}
	if metrics != nil {
		// Every protocol the server accepts clients on, as one figure.
		total := metrics["TCPConnection"] + metrics["HTTPConnection"] + metrics["MySQLConnection"] + metrics["PostgreSQLConnection"]
		conns := &ConnectionCounts{Total: int(total), Active: int(metrics["Query"])}
		conns.Idle = max(conns.Total-conns.Active, 0)
		var limit sql.NullString
		if err := db.QueryRowContext(ctx, `SELECT value FROM system.server_settings WHERE name = 'max_connections'`).Scan(&limit); err == nil {
			conns.Max = atoiOr(limit.String, 0)
		}
		out.Connections = conns
	}

	var parts, rows, bytes sql.NullFloat64
	if err := db.QueryRowContext(ctx, `
	  SELECT toFloat64(count()), toFloat64(sum(rows)), toFloat64(sum(bytes_on_disk))
	  FROM system.parts WHERE active AND database = currentDatabase()`).Scan(&parts, &rows, &bytes); err != nil {
		note("system.parts", err)
	} else {
		out.DatabaseBytes = int64(bytes.Float64)
		out.Gauges["activeParts"] = parts.Float64
		out.Gauges["rows"] = rows.Float64
	}

	// Every replica of a replicated table accepts writes, so there is no
	// primary to name; a server holding any is a replica among peers.
	out.Role = "standalone"
	var replicated float64
	if err := db.QueryRowContext(ctx, `SELECT toFloat64(count()) FROM system.replicas`).Scan(&replicated); err == nil && replicated > 0 {
		out.Role = "replica"
		out.Gauges["replicatedTables"] = replicated
	}

	// The disk the data sits on. A full disk stops merges first and inserts
	// second, and it is this server's disk the dashboard is running on.
	var free, total sql.NullFloat64
	if err := db.QueryRowContext(ctx, `SELECT toFloat64(sum(free_space)), toFloat64(sum(total_space)) FROM system.disks`).Scan(&free, &total); err == nil {
		out.Gauges["diskFreeBytes"] = free.Float64
		out.Gauges["diskTotalBytes"] = total.Float64
	}
	return out, nil
}

func atoiOr(s string, def int) int {
	n := 0
	if s == "" {
		return def
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return def
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// --- table and index statistics ------------------------------------------------

// clickhouseDatabase renders the database a query is about: the one named,
// bound, or the connection's own.
func clickhouseDatabase(schema string) (string, []any) {
	if schema == "" {
		return "currentDatabase()", nil
	}
	return "?", []any{schema}
}

// TableStats reads system.tables beside the active parts of each: how many
// parts a table is in is its health, and compressed against uncompressed
// bytes is what its codecs are buying.
func (clickhouseDialect) TableStats(ctx context.Context, db *sql.DB, opts StatsOptions) (*TableStatsReport, error) {
	expr, args := clickhouseDatabase(opts.Schema)
	args = append(args, opts.limit()+1)
	rows, err := db.QueryContext(ctx, `
	  SELECT t.database, t.name, t.engine,
	         toInt64(ifNull(t.total_rows, 0)), toInt64(ifNull(t.total_bytes, 0)),
	         toInt64(ifNull(p.parts, 0)), toInt64(ifNull(p.compressed, 0)),
	         toInt64(ifNull(p.uncompressed, 0)), toInt64(ifNull(p.marks, 0))
	  FROM system.tables t
	  LEFT JOIN (
	    SELECT database, table, count() AS parts, sum(data_compressed_bytes) AS compressed,
	           sum(data_uncompressed_bytes) AS uncompressed, sum(marks_bytes) AS marks
	    FROM system.parts WHERE active GROUP BY database, table
	  ) p ON p.database = t.database AND p.table = t.name
	  WHERE t.database = `+expr+` AND t.engine NOT LIKE '%View' AND NOT t.is_temporary
	  ORDER BY ifNull(t.total_bytes, 0) DESC, t.name
	  LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &TableStatsReport{Schema: opts.Schema, Tables: []TableStat{}}
	for rows.Next() {
		t := TableStat{Kind: "table", DeadRows: -1, BloatBytes: -1, SeqScans: -1, SeqRowsRead: -1,
			IndexScans: -1, IndexRowsRead: -1, Inserts: -1, Updates: -1, Deletes: -1, ModsSinceStats: -1}
		var compressed int64
		if err := rows.Scan(&t.Schema, &t.Table, &t.Engine, &t.Rows, &t.TotalBytes, &t.Parts,
			&compressed, &t.UncompressedBytes, &t.IndexBytes); err != nil {
			return nil, err
		}
		t.TableBytes = compressed
		out.Tables = append(out.Tables, t)
	}
	return out, rows.Err()
}

// IndexStats lists the data-skipping indexes. The sorting key is not among
// them: it is how the table is stored, not something that can be dropped, and
// ClickHouse counts the use of neither.
func (clickhouseDialect) IndexStats(ctx context.Context, db *sql.DB, opts StatsOptions) (*IndexStatsReport, error) {
	expr, args := clickhouseDatabase(opts.Schema)
	args = append(args, opts.Table, opts.Table, opts.limit()+1)
	// The size column is newer than the table; a server without it lists its
	// indexes with no size rather than not at all.
	query := func(size string) (*sql.Rows, error) {
		return db.QueryContext(ctx, `
		  SELECT database, table, name, type_full, expr, `+size+`
		  FROM system.data_skipping_indices
		  WHERE database = `+expr+` AND (? = '' OR table = ?)
		  ORDER BY 6 DESC, table, name
		  LIMIT ?`, args...)
	}
	rows, err := query("toInt64(data_compressed_bytes)")
	if err != nil {
		rows, err = query("toInt64(0)")
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &IndexStatsReport{Schema: opts.Schema, Indexes: []IndexStat{},
		Notes: []string{"ClickHouse keeps no index use counts; these are the data-skipping indexes, beside each table's sorting key."}}
	for rows.Next() {
		ix := IndexStat{Valid: true, Scans: -1, RowsRead: -1}
		var expression string
		if err := rows.Scan(&ix.Schema, &ix.Table, &ix.Name, &ix.Method, &expression, &ix.Bytes); err != nil {
			return nil, err
		}
		ix.Columns = []string{expression}
		ix.Definition = "INDEX " + ix.Name + " " + expression + " TYPE " + ix.Method
		// A skipping index is defined by its expression and type together.
		ix.signature = ix.Method + "|" + expression
		out.Indexes = append(out.Indexes, ix)
	}
	return out, rows.Err()
}

// --- maintenance -------------------------------------------------------------

func (clickhouseDialect) MaintenanceActions() []MaintenanceAction {
	return []MaintenanceAction{
		{ID: "optimize", Label: "Optimize", Scope: "table", Options: []string{"final"},
			Description: "Asks for a merge of the table's parts now rather than when the server gets to it. With final, every partition is merged down to one part, which rewrites the whole table and is heavy on a large one."},
	}
}

func clickhouseMaintenanceSQL(d clickhouseDialect, req MaintenanceRequest) (string, error) {
	if req.Action != "optimize" {
		return "", maintenanceRefused("unknown action %q", req.Action)
	}
	if req.Index != "" {
		return "", maintenanceRefused("optimize takes a table")
	}
	rel, err := qualify(d, req.Schema, req.Table)
	if err != nil {
		return "", err
	}
	stmt := "OPTIMIZE TABLE " + rel
	if req.Options.Final {
		stmt += " FINAL"
	}
	return stmt, nil
}

// Maintain runs OPTIMIZE, which prints nothing: the merge it schedules shows
// up under merges while it runs and in the part count when it is done.
func (d clickhouseDialect) Maintain(ctx context.Context, db *sql.DB, _ string, req MaintenanceRequest) (*MaintenanceResult, error) {
	stmt, err := clickhouseMaintenanceSQL(d, req)
	if err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(ctx, stmt); err != nil {
		return nil, err
	}
	return &MaintenanceResult{Statements: []string{stmt}, Output: []string{}, OK: true}, nil
}

// --- parts, merges, mutations, queries -----------------------------------------

// ClickHousePartition is one partition of a table: how many parts it is in
// and what they hold. Hundreds of parts in one partition is the state inserts
// start being delayed in.
type ClickHousePartition struct {
	Database          string     `json:"database"`
	Table             string     `json:"table"`
	Partition         string     `json:"partition"`
	Parts             int64      `json:"parts"`
	Rows              int64      `json:"rows"`
	Bytes             int64      `json:"bytes"`
	UncompressedBytes int64      `json:"uncompressedBytes"`
	Modified          *time.Time `json:"modified,omitempty"`
}

type ClickHousePart struct {
	Database          string     `json:"database"`
	Table             string     `json:"table"`
	Partition         string     `json:"partition"`
	Name              string     `json:"name"`
	Active            bool       `json:"active"`
	Rows              int64      `json:"rows"`
	Bytes             int64      `json:"bytes"`
	CompressedBytes   int64      `json:"compressedBytes"`
	UncompressedBytes int64      `json:"uncompressedBytes"`
	Marks             int64      `json:"marks"`
	Level             int64      `json:"level"`
	Type              string     `json:"type"`
	Disk              string     `json:"disk"`
	Modified          *time.Time `json:"modified,omitempty"`
}

type ClickHouseParts struct {
	Database   string                `json:"database"`
	Table      string                `json:"table,omitempty"`
	Partitions []ClickHousePartition `json:"partitions"`
	Parts      []ClickHousePart      `json:"parts"`
	// Truncated is true when either list was cut at its bound.
	Truncated bool `json:"truncated"`
}

// maxEngineRows bounds each ClickHouse view. A table partitioned by day for
// ten years has thousands of partitions; the largest five hundred are the
// ones worth reading.
const maxEngineRows = 500

// ClickHouseTableParts lists a database's partitions and parts, largest
// first. Inactive parts — merged away and waiting to be removed — are left
// out unless asked for: they are not the table.
func ClickHouseTableParts(ctx context.Context, db *sql.DB, database, table string, inactive bool) (*ClickHouseParts, error) {
	expr, base := clickhouseDatabase(database)
	out := &ClickHouseParts{Database: database, Table: table, Partitions: []ClickHousePartition{}, Parts: []ClickHousePart{}}
	if database == "" {
		if err := db.QueryRowContext(ctx, `SELECT currentDatabase()`).Scan(&out.Database); err != nil {
			return nil, err
		}
	}
	args := append(append([]any{}, base...), table, table, maxEngineRows+1)
	rows, err := db.QueryContext(ctx, `
	  SELECT database, table, partition, toInt64(count()), toInt64(sum(rows)), toInt64(sum(bytes_on_disk)),
	         toInt64(sum(data_uncompressed_bytes)), max(modification_time)
	  FROM system.parts
	  WHERE active AND database = `+expr+` AND (? = '' OR table = ?)
	  GROUP BY database, table, partition
	  ORDER BY 6 DESC, table, partition
	  LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var p ClickHousePartition
		var modified sql.NullTime
		if err := rows.Scan(&p.Database, &p.Table, &p.Partition, &p.Parts, &p.Rows, &p.Bytes, &p.UncompressedBytes, &modified); err != nil {
			rows.Close()
			return nil, err
		}
		p.Modified = timePtr(modified)
		if len(out.Partitions) == maxEngineRows {
			out.Truncated = true
			break
		}
		out.Partitions = append(out.Partitions, p)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	activeOnly := "active AND "
	if inactive {
		activeOnly = ""
	}
	rows, err = db.QueryContext(ctx, `
	  SELECT database, table, partition, name, active, toInt64(rows), toInt64(bytes_on_disk),
	         toInt64(data_compressed_bytes), toInt64(data_uncompressed_bytes), toInt64(marks), toInt64(level),
	         toString(part_type), disk_name, modification_time
	  FROM system.parts
	  WHERE `+activeOnly+`database = `+expr+` AND (? = '' OR table = ?)
	  ORDER BY bytes_on_disk DESC, name
	  LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p ClickHousePart
		var modified sql.NullTime
		if err := rows.Scan(&p.Database, &p.Table, &p.Partition, &p.Name, &p.Active, &p.Rows, &p.Bytes,
			&p.CompressedBytes, &p.UncompressedBytes, &p.Marks, &p.Level, &p.Type, &p.Disk, &modified); err != nil {
			return nil, err
		}
		p.Modified = timePtr(modified)
		if len(out.Parts) == maxEngineRows {
			out.Truncated = true
			break
		}
		out.Parts = append(out.Parts, p)
	}
	return out, rows.Err()
}

// ClickHouseMerge is one merge or mutation in flight.
type ClickHouseMerge struct {
	Database   string  `json:"database"`
	Table      string  `json:"table"`
	Elapsed    float64 `json:"elapsed"`
	Progress   float64 `json:"progress"`
	Parts      int64   `json:"parts"`
	ResultPart string  `json:"resultPart"`
	Bytes      int64   `json:"bytes"`
	RowsRead   int64   `json:"rowsRead"`
	RowsDone   int64   `json:"rowsWritten"`
	Memory     int64   `json:"memory"`
	IsMutation bool    `json:"isMutation"`
	Type       string  `json:"type,omitempty"`
	Algorithm  string  `json:"algorithm,omitempty"`
}

func ClickHouseMerges(ctx context.Context, db *sql.DB) ([]ClickHouseMerge, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT database, table, toFloat64(elapsed), toFloat64(progress), toInt64(num_parts), result_part_name,
	         toInt64(total_size_bytes_compressed), toInt64(rows_read), toInt64(rows_written),
	         toInt64(memory_usage), is_mutation, toString(merge_type), toString(merge_algorithm)
	  FROM system.merges
	  ORDER BY elapsed DESC
	  LIMIT `+itoa(maxEngineRows))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ClickHouseMerge{}
	for rows.Next() {
		var m ClickHouseMerge
		if err := rows.Scan(&m.Database, &m.Table, &m.Elapsed, &m.Progress, &m.Parts, &m.ResultPart, &m.Bytes,
			&m.RowsRead, &m.RowsDone, &m.Memory, &m.IsMutation, &m.Type, &m.Algorithm); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ClickHouseMutation is one ALTER … UPDATE or DELETE, which ClickHouse
// applies by rewriting parts in the background. One that is not done and has
// a failure reason will retry forever.
type ClickHouseMutation struct {
	Database   string     `json:"database"`
	Table      string     `json:"table"`
	ID         string     `json:"id"`
	Command    string     `json:"command"`
	Created    *time.Time `json:"created,omitempty"`
	PartsToDo  int64      `json:"partsToDo"`
	Done       bool       `json:"done"`
	FailReason string     `json:"failReason,omitempty"`
	FailedAt   *time.Time `json:"failedAt,omitempty"`
}

// ClickHouseMutations lists the unfinished mutations first, then the most
// recent finished ones.
func ClickHouseMutations(ctx context.Context, db *sql.DB, database string) ([]ClickHouseMutation, error) {
	expr, args := clickhouseDatabase(database)
	rows, err := db.QueryContext(ctx, `
	  SELECT database, table, mutation_id, command, create_time, toInt64(parts_to_do), is_done,
	         latest_fail_reason, latest_fail_time
	  FROM system.mutations
	  WHERE database = `+expr+`
	  ORDER BY is_done, create_time DESC
	  LIMIT `+itoa(maxEngineRows), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ClickHouseMutation{}
	for rows.Next() {
		var m ClickHouseMutation
		var created, failed sql.NullTime
		if err := rows.Scan(&m.Database, &m.Table, &m.ID, &m.Command, &created, &m.PartsToDo, &m.Done, &m.FailReason, &failed); err != nil {
			return nil, err
		}
		m.Created = timePtr(created)
		// The fail time is the epoch on a mutation that never failed.
		if failed.Valid && failed.Time.Unix() > 0 {
			m.FailedAt = timePtr(failed)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ClickHouseQuery is one running query with what it has consumed so far —
// the figures the shared session list has no columns for.
type ClickHouseQuery struct {
	ID         string  `json:"id"`
	User       string  `json:"user"`
	Client     string  `json:"client,omitempty"`
	ClientName string  `json:"clientName,omitempty"`
	Database   string  `json:"database,omitempty"`
	Kind       string  `json:"kind,omitempty"`
	Elapsed    float64 `json:"elapsed"`
	RowsRead   int64   `json:"rowsRead"`
	BytesRead  int64   `json:"bytesRead"`
	// TotalRows is the server's estimate of how many rows the query will
	// read; 0 when it has none, which is when Progress is -1.
	TotalRows   int64   `json:"totalRows"`
	Progress    float64 `json:"progress"`
	RowsWritten int64   `json:"rowsWritten"`
	Memory      int64   `json:"memory"`
	PeakMemory  int64   `json:"peakMemory"`
	Cancelled   bool    `json:"cancelled"`
	Query       string  `json:"query"`
	Self        bool    `json:"self,omitempty"`
}

func ClickHouseQueries(ctx context.Context, db *sql.DB) ([]ClickHouseQuery, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT toString(query_id), user, toString(address), client_name, current_database, toString(query_kind),
	         toFloat64(elapsed), toInt64(read_rows), toInt64(read_bytes), toInt64(total_rows_approx),
	         toInt64(written_rows), toInt64(memory_usage), toInt64(peak_memory_usage), is_cancelled,
	         substring(query, 1, 4000), query_id = queryID()
	  FROM system.processes
	  ORDER BY elapsed DESC
	  LIMIT `+itoa(maxEngineRows))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ClickHouseQuery{}
	for rows.Next() {
		var q ClickHouseQuery
		if err := rows.Scan(&q.ID, &q.User, &q.Client, &q.ClientName, &q.Database, &q.Kind, &q.Elapsed,
			&q.RowsRead, &q.BytesRead, &q.TotalRows, &q.RowsWritten, &q.Memory, &q.PeakMemory, &q.Cancelled,
			&q.Query, &q.Self); err != nil {
			return nil, err
		}
		q.Progress = -1
		if q.TotalRows > 0 {
			q.Progress = min(float64(q.RowsRead)/float64(q.TotalRows), 1)
		}
		out = append(out, q)
	}
	return out, rows.Err()
}
