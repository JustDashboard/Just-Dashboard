package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// SQL Server's answers to the operations questions, from its dynamic
// management views. Most of them need VIEW SERVER STATE, which an ordinary
// application login does not have; each part is therefore read on its own, so
// a login that may see its own database's sizes but not the server's counters
// still gets the half it is allowed.

// mssqlRefused reports whether an error is SQL Server refusing a view rather
// than a query going wrong: "VIEW SERVER STATE permission was denied", "The
// user does not have permission to perform this action". That is "this login
// may not", which a page says in a sentence instead of failing.
func mssqlRefused(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "permission")
}

// --- snapshot ----------------------------------------------------------------

func (mssqlDialect) ServerStats(ctx context.Context, db *sql.DB) (*ServerStats, error) {
	out := &ServerStats{Counters: map[string]float64{}, Gauges: map[string]float64{}, Facts: map[string]string{}}
	var limit int
	var updateability sql.NullString
	if err := db.QueryRowContext(ctx, `
	  SELECT @@VERSION, DB_NAME(), @@MAX_CONNECTIONS,
	         CAST(DATABASEPROPERTYEX(DB_NAME(), 'Updateability') AS NVARCHAR(32))`).
		Scan(&out.Version, &out.Database, &limit, &updateability); err != nil {
		return nil, err
	}
	out.At = time.Now().UTC()
	// The banner is four lines; the first is the product and build.
	out.Version, _, _ = strings.Cut(out.Version, "\n")
	out.Version = strings.TrimSpace(out.Version)
	// A readable secondary reports its databases as read-only; nothing else
	// visible to an ordinary login says which side of an availability group
	// this is.
	out.Role = "standalone"
	if strings.EqualFold(updateability.String, "READ_ONLY") {
		out.Role = "replica"
	}
	note := func(what string, err error) {
		out.Notes = append(out.Notes, what+" could not be read: "+err.Error())
	}

	var uptime sql.NullFloat64
	if err := db.QueryRowContext(ctx, `
	  SELECT CAST(DATEDIFF(SECOND, sqlserver_start_time, SYSDATETIME()) AS FLOAT) FROM sys.dm_os_sys_info`).Scan(&uptime); err != nil {
		note("The start time", err)
	} else if uptime.Valid {
		out.UptimeSeconds = uptime.Float64
		started := out.At.Add(-time.Duration(uptime.Float64 * float64(time.Second)))
		out.StartedAt = &started
	}

	var size sql.NullFloat64
	if err := db.QueryRowContext(ctx, `SELECT CAST(SUM(CAST(size AS BIGINT)) * 8192 AS FLOAT) FROM sys.database_files`).Scan(&size); err != nil {
		note("The database size", err)
	}
	out.DatabaseBytes = int64(size.Float64)

	// The "/sec" counters are running totals despite their names: the view
	// reports the raw value and leaves the division to whoever samples it.
	rows, err := db.QueryContext(ctx, `
	  SELECT RTRIM(counter_name), CAST(cntr_value AS FLOAT)
	  FROM sys.dm_os_performance_counters
	  WHERE (object_name LIKE '%:Databases%' AND instance_name = DB_NAME()
	         AND counter_name IN ('Transactions/sec', 'Write Transactions/sec', 'Log Bytes Flushed/sec', 'Active Transactions'))
	     OR (object_name LIKE '%:SQL Statistics%'
	         AND counter_name IN ('Batch Requests/sec', 'SQL Compilations/sec', 'SQL Re-Compilations/sec'))
	     OR (object_name LIKE '%:Buffer Manager%'
	         AND counter_name IN ('Page reads/sec', 'Page writes/sec', 'Page lookups/sec', 'Page life expectancy'))
	     OR (object_name LIKE '%:Locks%' AND instance_name = '_Total'
	         AND counter_name IN ('Number of Deadlocks/sec', 'Lock Waits/sec'))
	     OR (object_name LIKE '%:General Statistics%'
	         AND counter_name IN ('User Connections', 'Logins/sec', 'Processes blocked'))
	     OR (object_name LIKE '%:Access Methods%'
	         AND counter_name IN ('Full Scans/sec', 'Index Searches/sec'))`)
	if err != nil {
		note("The performance counters", err)
	} else {
		perf := map[string]float64{}
		for rows.Next() {
			var name string
			var value float64
			if err := rows.Scan(&name, &value); err != nil {
				rows.Close()
				return nil, err
			}
			perf[name] = value
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		counters := map[string]string{
			"transactions": "Transactions/sec", "writeTransactions": "Write Transactions/sec",
			"logBytesFlushed": "Log Bytes Flushed/sec", StatQueries: "Batch Requests/sec",
			"compilations": "SQL Compilations/sec", "recompilations": "SQL Re-Compilations/sec",
			StatBlocksRead: "Page reads/sec", "pageWrites": "Page writes/sec",
			StatDeadlocks: "Number of Deadlocks/sec", "lockWaits": "Lock Waits/sec",
			"logins": "Logins/sec", "fullScans": "Full Scans/sec", "indexSearches": "Index Searches/sec",
		}
		for key, name := range counters {
			if v, ok := perf[name]; ok {
				out.Counters[key] = v
			}
		}
		// SQL Server counts transactions started rather than committed and
		// rolled back apart; the write transactions are the ones that
		// changed something, which is the nearer reading of "committed".
		if v, ok := perf["Write Transactions/sec"]; ok {
			out.Counters[StatTransactionsCommitted] = v
		}
		if lookups, ok := perf["Page lookups/sec"]; ok {
			// A lookup is a request for a page; a read is one that had to go
			// to disk for it.
			out.Counters[StatBlocksHit] = max0(lookups - perf["Page reads/sec"])
		}
		for key, name := range map[string]string{
			"pageLifeExpectancy": "Page life expectancy", "userConnections": "User Connections",
			"processesBlocked": "Processes blocked", "activeTransactions": "Active Transactions",
		} {
			if v, ok := perf[name]; ok {
				out.Gauges[key] = v
			}
		}
	}

	conns := &ConnectionCounts{Max: limit}
	rows, err = db.QueryContext(ctx, `
	  SELECT ISNULL(r.status, s.status), COUNT(*),
	         SUM(CASE WHEN ISNULL(r.blocking_session_id, 0) > 0 THEN 1 ELSE 0 END),
	         SUM(CASE WHEN r.session_id IS NULL AND s.open_transaction_count > 0 THEN 1 ELSE 0 END)
	  FROM sys.dm_exec_sessions s
	  LEFT JOIN sys.dm_exec_requests r ON r.session_id = s.session_id
	  WHERE s.is_user_process = 1
	  GROUP BY ISNULL(r.status, s.status)`)
	if err != nil {
		note("Sessions", err)
		return out, nil
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var n, blocked, idleInTx int
		if err := rows.Scan(&status, &n, &blocked, &idleInTx); err != nil {
			return nil, err
		}
		conns.Total += n
		conns.Waiting += blocked
		if strings.EqualFold(status, "sleeping") || strings.EqualFold(status, "dormant") {
			conns.IdleInTransaction += idleInTx
			conns.Idle += n - idleInTx
		} else {
			conns.Active += n
		}
	}
	out.Connections = conns
	return out, rows.Err()
}

func max0(v float64) float64 {
	if v < 0 {
		return 0
	}
	return v
}

// --- sessions ----------------------------------------------------------------

// Sessions lists every user session, not only the ones with a request in
// flight. Activity read sys.dm_exec_requests alone, which is why a session
// sleeping inside an open transaction — the usual holder of the lock
// everything else is waiting on — never appeared in the list it was blocking.
func (mssqlDialect) Sessions(ctx context.Context, db *sql.DB) ([]Activity, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT CAST(s.session_id AS NVARCHAR(16)),
	         ISNULL(s.login_name, ''),
	         ISNULL(DB_NAME(ISNULL(r.database_id, s.database_id)), ''),
	         ISNULL(r.status, s.status),
	         CAST(ISNULL(r.total_elapsed_time, 0) / 1000.0 AS FLOAT),
	         CAST(CASE WHEN r.session_id IS NULL
	                   THEN DATEDIFF(SECOND, s.last_request_end_time, SYSDATETIME()) ELSE 0 END AS FLOAT),
	         CAST((SELECT MAX(DATEDIFF(SECOND, a.transaction_begin_time, SYSDATETIME()))
	               FROM sys.dm_tran_session_transactions st
	               JOIN sys.dm_tran_active_transactions a ON a.transaction_id = st.transaction_id
	               WHERE st.session_id = s.session_id) AS FLOAT),
	         ISNULL(SUBSTRING(ISNULL(rt.text, ct.text), 1, 4000), ''),
	         ISNULL(s.host_name, ''),
	         ISNULL(s.program_name, ''),
	         ISNULL(r.wait_type, ''),
	         CASE WHEN ISNULL(r.blocking_session_id, 0) = 0 THEN ''
	              ELSE CAST(r.blocking_session_id AS NVARCHAR(16)) END,
	         CASE WHEN r.session_id IS NULL THEN 0 ELSE 1 END,
	         ISNULL(s.open_transaction_count, 0),
	         CASE WHEN s.session_id = @@SPID THEN 1 ELSE 0 END
	  FROM sys.dm_exec_sessions s
	  LEFT JOIN sys.dm_exec_requests r ON r.session_id = s.session_id
	  LEFT JOIN sys.dm_exec_connections c ON c.session_id = s.session_id AND c.parent_connection_id IS NULL
	  OUTER APPLY sys.dm_exec_sql_text(r.sql_handle) rt
	  OUTER APPLY sys.dm_exec_sql_text(c.most_recent_sql_handle) ct
	  WHERE s.is_user_process = 1
	  ORDER BY ISNULL(r.total_elapsed_time, 0) DESC`)
	if mssqlRefused(err) {
		// An application's login: the views that say what each session is
		// running are not its to read. That is said, not failed.
		return nil, errSessionsRefused{reason: "This login may not read the session list (it needs VIEW SERVER STATE): " + err.Error()}
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := time.Now().UTC()
	out := []Activity{}
	for rows.Next() {
		var a Activity
		var running, idle float64
		// NULL is "no transaction". A number cannot stand for that: the begin
		// time is a DATETIME, rounded to a three-hundredth of a second, and
		// one that rounded up past the clock reads as a second in the future.
		var txSeconds sql.NullFloat64
		var hasRequest, openTx, self int
		if err := rows.Scan(&a.PID, &a.User, &a.Database, &a.State, &running, &idle, &txSeconds, &a.Query,
			&a.Client, &a.Application, &a.Wait, &a.BlockedBy, &hasRequest, &openTx, &self); err != nil {
			return nil, err
		}
		a.Self = self != 0
		a.WaitEvent = a.Wait
		switch {
		case hasRequest == 1:
			a.Status = SessionActive
			a.Seconds = running
			start := now.Add(-time.Duration(running * float64(time.Second)))
			a.QueryStart = &start
		case openTx > 0:
			a.Status = SessionIdleInTransaction
			a.IdleSeconds = max0(idle)
		default:
			a.Status = SessionIdle
			a.IdleSeconds = max0(idle)
		}
		if txSeconds.Valid {
			a.TransactionSeconds = max0(txSeconds.Float64)
			start := now.Add(-time.Duration(a.TransactionSeconds * float64(time.Second)))
			a.TransactionStart = &start
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// --- locks -------------------------------------------------------------------

// Locks reads the waiting tasks, which name their blocker directly, and
// resolves what each is waiting for through the lock it has requested. A key
// or page lock is on a partition rather than an object, so those go through
// sys.partitions to find the table — which only resolves in this database.
func (mssqlDialect) Locks(ctx context.Context, db *sql.DB) (*LocksReport, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT TOP (`+itoa(maxHeldLocks)+`)
	         CAST(w.session_id AS NVARCHAR(16)), CAST(w.blocking_session_id AS NVARCHAR(16)),
	         ISNULL(ws.login_name, ''), ISNULL(bs.login_name, ''),
	         ISNULL(SUBSTRING(wt.text, 1, 2000), ''), ISNULL(SUBSTRING(bt.text, 1, 2000), ''),
	         ISNULL(br.status, ISNULL(bs.status, '')),
	         ISNULL(lk.object_name, ISNULL(w.resource_description, '')),
	         ISNULL(lk.resource_type, ''), ISNULL(lk.request_mode, ISNULL(w.wait_type, '')),
	         CAST(w.wait_duration_ms / 1000.0 AS FLOAT),
	         CAST(ISNULL((SELECT MAX(DATEDIFF(SECOND, a.transaction_begin_time, SYSDATETIME()))
	                      FROM sys.dm_tran_session_transactions st
	                      JOIN sys.dm_tran_active_transactions a ON a.transaction_id = st.transaction_id
	                      WHERE st.session_id = w.blocking_session_id), 0) AS FLOAT)
	  FROM sys.dm_os_waiting_tasks w
	  JOIN sys.dm_exec_sessions ws ON ws.session_id = w.session_id
	  LEFT JOIN sys.dm_exec_sessions bs ON bs.session_id = w.blocking_session_id
	  LEFT JOIN sys.dm_exec_requests wr ON wr.session_id = w.session_id
	  LEFT JOIN sys.dm_exec_requests br ON br.session_id = w.blocking_session_id
	  LEFT JOIN sys.dm_exec_connections bc ON bc.session_id = w.blocking_session_id AND bc.parent_connection_id IS NULL
	  OUTER APPLY sys.dm_exec_sql_text(wr.sql_handle) wt
	  OUTER APPLY sys.dm_exec_sql_text(ISNULL(br.sql_handle, bc.most_recent_sql_handle)) bt
	  OUTER APPLY (
	    SELECT TOP 1 l.resource_type, l.request_mode,
	           CASE WHEN l.resource_type = 'OBJECT'
	                THEN OBJECT_SCHEMA_NAME(l.resource_associated_entity_id, l.resource_database_id) + '.'
	                     + OBJECT_NAME(l.resource_associated_entity_id, l.resource_database_id)
	                WHEN l.resource_type IN ('KEY', 'PAGE', 'RID', 'HOBT') AND l.resource_database_id = DB_ID()
	                THEN (SELECT TOP 1 OBJECT_SCHEMA_NAME(p.object_id) + '.' + OBJECT_NAME(p.object_id)
	                      FROM sys.partitions p WHERE p.hobt_id = l.resource_associated_entity_id)
	           END AS object_name
	    FROM sys.dm_tran_locks l
	    WHERE l.request_session_id = w.session_id AND l.request_status = 'WAIT'
	  ) lk
	  WHERE w.blocking_session_id IS NOT NULL AND w.blocking_session_id <> w.session_id
	    AND ws.is_user_process = 1
	  ORDER BY w.wait_duration_ms DESC`)
	if mssqlRefused(err) {
		return &LocksReport{Waits: []LockWait{}, Locks: []HeldLock{},
			Reason: "This login may not read who is waiting on whom (it needs VIEW SERVER STATE): " + err.Error()}, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &LocksReport{Supported: true, Waits: []LockWait{}, Locks: []HeldLock{},
		Notes: []string{"Only the waits are listed; the full lock table is not read on SQL Server."}}
	for rows.Next() {
		var w LockWait
		if err := rows.Scan(&w.WaitingPID, &w.BlockingPID, &w.WaitingUser, &w.BlockingUser, &w.WaitingQuery,
			&w.BlockingQuery, &w.BlockingState, &w.Object, &w.LockType, &w.Mode, &w.WaitSeconds, &w.BlockingSeconds); err != nil {
			return nil, err
		}
		out.Waits = append(out.Waits, w)
	}
	return out, rows.Err()
}

// --- table and index statistics ------------------------------------------------

// TableStats reads sizes from the partition statistics and use from the index
// usage view, which the server empties at every restart: a table with no row
// there has not been touched since, and is reported as zero rather than
// unknown only when the view can be read at all.
func (mssqlDialect) TableStats(ctx context.Context, db *sql.DB, opts StatsOptions) (*TableStatsReport, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT TOP (@p2) s.name, t.name,
	         SUM(CASE WHEN p.index_id < 2 THEN p.row_count ELSE 0 END),
	         SUM(p.reserved_page_count) * 8192,
	         SUM(CASE WHEN p.index_id < 2 THEN p.used_page_count ELSE 0 END) * 8192,
	         SUM(CASE WHEN p.index_id >= 2 THEN p.used_page_count ELSE 0 END) * 8192,
	         (SUM(p.reserved_page_count) - SUM(p.used_page_count)) * 8192,
	         ISNULL((SELECT SUM(u.user_scans) FROM sys.dm_db_index_usage_stats u
	                 WHERE u.database_id = DB_ID() AND u.object_id = t.object_id), 0),
	         ISNULL((SELECT SUM(u.user_seeks + u.user_lookups) FROM sys.dm_db_index_usage_stats u
	                 WHERE u.database_id = DB_ID() AND u.object_id = t.object_id), 0),
	         ISNULL((SELECT MAX(u.user_updates) FROM sys.dm_db_index_usage_stats u
	                 WHERE u.database_id = DB_ID() AND u.object_id = t.object_id AND u.index_id < 2), 0),
	         (SELECT MAX(STATS_DATE(st.object_id, st.stats_id)) FROM sys.stats st WHERE st.object_id = t.object_id)
	  FROM sys.dm_db_partition_stats p
	  JOIN sys.tables t ON t.object_id = p.object_id
	  JOIN sys.schemas s ON s.schema_id = t.schema_id
	  WHERE (@p1 = '' OR s.name = @p1)
	  GROUP BY s.name, t.name, t.object_id
	  ORDER BY SUM(p.reserved_page_count) DESC, s.name, t.name`, opts.Schema, opts.limit()+1)
	if mssqlRefused(err) {
		return &TableStatsReport{Schema: opts.Schema, Tables: []TableStat{},
			Reason: "This login may not read the table statistics (it needs VIEW DATABASE STATE): " + err.Error()}, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &TableStatsReport{Schema: opts.Schema, Tables: []TableStat{}}
	for rows.Next() {
		// The usage view counts every insert, update and delete against the
		// heap or clustered index as one figure, reported here as updates.
		t := TableStat{Kind: "table", DeadRows: -1, SeqRowsRead: -1, IndexRowsRead: -1,
			Inserts: -1, Deletes: -1, ModsSinceStats: -1}
		var analysed sql.NullTime
		if err := rows.Scan(&t.Schema, &t.Table, &t.Rows, &t.TotalBytes, &t.TableBytes, &t.IndexBytes,
			&t.BloatBytes, &t.SeqScans, &t.IndexScans, &t.Updates, &analysed); err != nil {
			return nil, err
		}
		t.LastAnalyze = timePtr(analysed)
		out.Tables = append(out.Tables, t)
	}
	return out, rows.Err()
}

// IndexStats lists every index with its size and how often it was sought,
// scanned or looked up since the server started. The key columns are joined
// with a tab: FOR XML is how the versions before 2017 aggregate strings, and
// a tab is the one separator that is both legal there and never in a name.
func (mssqlDialect) IndexStats(ctx context.Context, db *sql.DB, opts StatsOptions) (*IndexStatsReport, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT TOP (@p3) s.name, t.name, i.name, LOWER(i.type_desc),
	         i.is_unique, i.is_primary_key, i.is_disabled, i.is_unique_constraint, i.has_filter,
	         ISNULL((SELECT SUM(ps.used_page_count) * 8192 FROM sys.dm_db_partition_stats ps
	                 WHERE ps.object_id = i.object_id AND ps.index_id = i.index_id), 0),
	         ISNULL(u.user_seeks + u.user_scans + u.user_lookups, 0),
	         ISNULL(STUFF((SELECT CHAR(9) + c.name
	                       FROM sys.index_columns ic
	                       JOIN sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id
	                       WHERE ic.object_id = i.object_id AND ic.index_id = i.index_id AND ic.is_included_column = 0
	                       ORDER BY ic.key_ordinal
	                       FOR XML PATH(''), TYPE).value('.', 'NVARCHAR(MAX)'), 1, 1, ''), '')
	  FROM sys.indexes i
	  JOIN sys.tables t ON t.object_id = i.object_id
	  JOIN sys.schemas s ON s.schema_id = t.schema_id
	  LEFT JOIN sys.dm_db_index_usage_stats u
	    ON u.database_id = DB_ID() AND u.object_id = i.object_id AND u.index_id = i.index_id
	  WHERE i.index_id > 0 AND i.is_hypothetical = 0
	    AND (@p1 = '' OR s.name = @p1) AND (@p2 = '' OR t.name = @p2)
	  ORDER BY 10 DESC, s.name, t.name, i.name`, opts.Schema, opts.Table, opts.limit()+1)
	if mssqlRefused(err) {
		return &IndexStatsReport{Schema: opts.Schema, Indexes: []IndexStat{},
			Reason: "This login may not read the index statistics (it needs VIEW DATABASE STATE): " + err.Error()}, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &IndexStatsReport{Schema: opts.Schema, Indexes: []IndexStat{},
		Notes: []string{"Use counts run from the last server start, so an index is only called unused since then."}}
	for rows.Next() {
		ix := IndexStat{RowsRead: -1}
		var disabled, constraint, filtered bool
		var cols string
		if err := rows.Scan(&ix.Schema, &ix.Table, &ix.Name, &ix.Method, &ix.Unique, &ix.Primary, &disabled,
			&constraint, &filtered, &ix.Bytes, &ix.Scans, &cols); err != nil {
			return nil, err
		}
		ix.Valid = !disabled
		ix.Constraint = constraint || ix.Primary
		ix.Columns = []string{}
		if cols != "" {
			ix.Columns = strings.Split(cols, "\t")
		}
		ix.plain = !filtered
		if filtered {
			ix.signature = "filtered:" + ix.Name
		}
		out.Indexes = append(out.Indexes, ix)
	}
	return out, rows.Err()
}

// --- maintenance -------------------------------------------------------------

func (mssqlDialect) MaintenanceActions() []MaintenanceAction {
	return []MaintenanceAction{
		{ID: "update_statistics", Label: "Update statistics", Scope: "either",
			Description: "Refreshes the statistics the optimiser chooses plans from. With no table it runs sp_updatestats, which updates only the statistics whose tables have changed. Reads a sample; blocks nothing."},
		{ID: "reorganize", Label: "Reorganize indexes", Scope: "table",
			Description: "Defragments the leaf level of the table's indexes in place. Always online: it takes short locks, and stopping it keeps what it has done."},
		{ID: "rebuild", Label: "Rebuild indexes", Scope: "table", Blocking: true, Options: []string{"online"},
			Description: "Drops and re-creates the table's indexes. Offline, the table cannot be read or written until it finishes. Online — Enterprise and Developer editions only — it blocks briefly at the start and the end, and needs room for a second copy of each index."},
		{ID: "check", Label: "Check integrity", Scope: "either", ReadOnly: true,
			Description: "DBCC CHECKTABLE, or CHECKDB with no table: reads every page looking for corruption. It works from a snapshot and changes nothing, and is heavy on the disk while it runs."},
	}
}

// mssqlMaintenanceSQL renders the one statement an action is. Every identifier
// is quoted by the dialect and nothing else in the request reaches the text.
func mssqlMaintenanceSQL(d mssqlDialect, req MaintenanceRequest) (string, error) {
	rel := ""
	if req.Table != "" {
		schema := req.Schema
		if schema == "" {
			schema = d.DefaultSchema()
		}
		var err error
		if rel, err = qualify(d, schema, req.Table); err != nil {
			return "", err
		}
	} else if req.Schema != "" {
		return "", maintenanceRefused("%s takes a table or the whole database, not a schema", req.Action)
	}
	if req.Index != "" && req.Action != "reorganize" && req.Action != "rebuild" {
		return "", maintenanceRefused("only reorganize and rebuild take an index")
	}
	if req.Options.Online && req.Action != "rebuild" {
		return "", maintenanceRefused("only rebuild can be run online")
	}
	switch req.Action {
	case "update_statistics":
		if rel == "" {
			return "EXEC sys.sp_updatestats", nil
		}
		return "UPDATE STATISTICS " + rel, nil
	case "check":
		// TABLERESULTS hands the report back as rows. Without it DBCC prints
		// messages, which arrive on a channel this driver's pool does not read.
		if rel == "" {
			return "DBCC CHECKDB WITH TABLERESULTS", nil
		}
		return "DBCC CHECKTABLE (" + dumpString(DriverMSSQL, rel) + ") WITH TABLERESULTS", nil
	case "reorganize", "rebuild":
		index := "ALL"
		if req.Index != "" {
			var err error
			if index, err = d.QuoteIdent(req.Index); err != nil {
				return "", err
			}
		}
		stmt := "ALTER INDEX " + index + " ON " + rel + " " + strings.ToUpper(req.Action)
		if req.Options.Online {
			stmt += " WITH (ONLINE = ON)"
		}
		return stmt, nil
	}
	return "", maintenanceRefused("unknown action %q", req.Action)
}

// Maintain runs the statement and reports what it left behind.
//
// DBCC answers in rows of its own, one per message, with the severity beside
// each: anything above 10 is a problem it found. The other three print nothing
// useful to a client, so what is returned for them is read from the catalogue
// afterwards — when each statistic was last updated and over how many rows,
// how fragmented each index now is — which is what an operator would go and
// look up next.
//
// The statement ends with the context: this driver answers a cancelled context
// by sending the server an attention signal, which stops the batch and rolls
// its work back.
func (d mssqlDialect) Maintain(ctx context.Context, db *sql.DB, _ string, req MaintenanceRequest) (*MaintenanceResult, error) {
	stmt, err := mssqlMaintenanceSQL(d, req)
	if err != nil {
		return nil, err
	}
	out := &MaintenanceResult{Statements: []string{stmt}, Output: []string{}, OK: true}
	// One session, so the clock read before the statement and the catalogue
	// read after it are the same server's.
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if req.Action == "check" {
		rows, err := conn.QueryContext(ctx, stmt)
		if err != nil {
			return nil, err
		}
		return out, mssqlKeepDBCC(rows, out)
	}
	// Kept as the server's own text, and compared there: its clock and its
	// time zone are not this process's.
	var before string
	if err := conn.QueryRowContext(ctx, `SELECT CONVERT(VARCHAR(27), SYSDATETIME(), 121)`).Scan(&before); err != nil {
		return nil, err
	}
	if _, err := conn.ExecContext(ctx, stmt); err != nil {
		return nil, err
	}
	rel := ""
	if req.Table != "" {
		rel, _ = qualify(d, orText(req.Schema, d.DefaultSchema()), req.Table)
	}
	// What follows is a courtesy: a login that may run the statement and not
	// read these views still ran the statement.
	switch {
	case req.Action == "update_statistics" && req.Table == "":
		var updated int
		if err := conn.QueryRowContext(ctx, `
		  SELECT COUNT(*) FROM sys.stats s
		  CROSS APPLY sys.dm_db_stats_properties(s.object_id, s.stats_id) p
		  WHERE OBJECTPROPERTY(s.object_id, 'IsUserTable') = 1
		    AND p.last_updated >= CONVERT(DATETIME2, @p1, 121)`, before).Scan(&updated); err == nil {
			out.keep(fmt.Sprintf("%d statistic%s updated; the rest had no changes to account for", updated,
				map[bool]string{true: " was", false: "s were"}[updated == 1]))
		}
	case req.Action == "update_statistics":
		rows, err := conn.QueryContext(ctx, `
		  SELECT s.name, CONVERT(VARCHAR(19), p.last_updated, 120), ISNULL(p.rows, 0), ISNULL(p.rows_sampled, 0)
		  FROM sys.stats s
		  CROSS APPLY sys.dm_db_stats_properties(s.object_id, s.stats_id) p
		  WHERE s.object_id = OBJECT_ID(@p1)
		  ORDER BY s.name`, rel)
		if err != nil {
			break
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			var updated sql.NullString
			var total, sampled int64
			if rows.Scan(&name, &updated, &total, &sampled) != nil {
				break
			}
			if !updated.Valid {
				out.keep(name + ": no statistics yet (the table is empty)")
				continue
			}
			out.keep(fmt.Sprintf("%s: updated %s, %d rows, %d sampled", name, updated.String, total, sampled))
		}
	default:
		rows, err := conn.QueryContext(ctx, `
		  SELECT ISNULL(i.name, '(heap)'), p.avg_fragmentation_in_percent, p.page_count
		  FROM sys.dm_db_index_physical_stats(DB_ID(), OBJECT_ID(@p1), NULL, NULL, 'LIMITED') p
		  JOIN sys.indexes i ON i.object_id = p.object_id AND i.index_id = p.index_id
		  WHERE p.alloc_unit_type_desc = 'IN_ROW_DATA'
		  ORDER BY i.name`, rel)
		if err != nil {
			break
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			var fragmented float64
			var pages int64
			if rows.Scan(&name, &fragmented, &pages) != nil {
				break
			}
			out.keep(name + ": " + strconv.FormatFloat(fragmented, 'f', 1, 64) + "% fragmented over " +
				strconv.FormatInt(pages, 10) + " page" + pluralS(int(pages)))
		}
	}
	return out, nil
}

// mssqlKeepDBCC reads a DBCC … WITH TABLERESULTS report: the message of each
// row, and from its level whether DBCC found anything. Every row is read for
// the verdict even once no more lines are kept. The columns are found by
// name; their number differs between CHECKDB and CHECKTABLE and by version.
func mssqlKeepDBCC(rows *sql.Rows, out *MaintenanceResult) error {
	defer rows.Close()
	for {
		cols, err := rows.Columns()
		if err != nil {
			return err
		}
		text, level := -1, -1
		for i, c := range cols {
			switch strings.ToLower(c) {
			case "messagetext":
				text = i
			case "level":
				level = i
			}
		}
		for rows.Next() {
			cells := make([]sql.NullString, len(cols))
			dest := make([]any, len(cols))
			for i := range cells {
				dest[i] = &cells[i]
			}
			if err := rows.Scan(dest...); err != nil {
				return err
			}
			if text >= 0 {
				out.keep(cells[text].String)
			}
			// Level 10 is information; DBCC reports what is wrong at 16 and up.
			if level >= 0 && atoiOr(cells[level].String, 0) > 10 {
				out.OK = false
			}
		}
		if !rows.NextResultSet() {
			break
		}
	}
	return rows.Err()
}
