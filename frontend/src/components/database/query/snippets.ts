/**
 * The diagnostics an operator reaches for, per engine, as data: what is
 * large, what is bloated, who is waiting on whom, what has been running too
 * long, which indexes nobody reads.
 *
 * Every one is a single statement that only reads — the server classifies
 * each as a read, and a test holds them to it — written in the engine's own
 * catalogue so it runs on a stock server with no extension beyond the ones it
 * names. They open in a tab of their own: a starting point to narrow, not a
 * report.
 */

export type SnippetGroup = "Size" | "Activity" | "Indexes" | "Health" | "Configuration"

export interface Snippet {
  id: string
  group: SnippetGroup
  title: string
  /** What it answers, in a line. */
  about: string
  sql: string
}

export const SNIPPET_GROUPS: readonly SnippetGroup[] = [
  "Size",
  "Activity",
  "Indexes",
  "Health",
  "Configuration",
]

const POSTGRES: Snippet[] = [
  {
    id: "table-sizes",
    group: "Size",
    title: "Largest tables",
    about: "Each table with its indexes and TOAST, largest first.",
    sql: `SELECT
  n.nspname AS schema,
  c.relname AS "table",
  pg_size_pretty(pg_total_relation_size(c.oid)) AS total,
  pg_size_pretty(pg_relation_size(c.oid)) AS heap,
  pg_size_pretty(pg_indexes_size(c.oid)) AS indexes,
  c.reltuples::bigint AS estimated_rows
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN ('r', 'm', 'p')
  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
ORDER BY pg_total_relation_size(c.oid) DESC
LIMIT 50;`,
  },
  {
    id: "index-sizes",
    group: "Size",
    title: "Largest indexes",
    about: "Every index with its size and how often it has been read.",
    sql: `SELECT
  s.schemaname AS schema,
  s.relname AS "table",
  s.indexrelname AS index,
  pg_size_pretty(pg_relation_size(s.indexrelid)) AS size,
  s.idx_scan AS scans
FROM pg_stat_user_indexes s
ORDER BY pg_relation_size(s.indexrelid) DESC
LIMIT 50;`,
  },
  {
    id: "database-sizes",
    group: "Size",
    title: "Databases on this server",
    about: "Every database the server holds, by size.",
    sql: `SELECT
  datname AS database,
  pg_size_pretty(pg_database_size(datname)) AS size
FROM pg_database
WHERE NOT datistemplate
ORDER BY pg_database_size(datname) DESC;`,
  },
  {
    id: "dead-rows",
    group: "Health",
    title: "Bloat: dead rows",
    about: "Tables carrying the most dead rows, and when autovacuum last reached them.",
    sql: `SELECT
  schemaname AS schema,
  relname AS "table",
  n_live_tup AS live_rows,
  n_dead_tup AS dead_rows,
  round(100.0 * n_dead_tup / NULLIF(n_live_tup + n_dead_tup, 0), 1) AS dead_percent,
  last_autovacuum,
  last_vacuum
FROM pg_stat_user_tables
ORDER BY n_dead_tup DESC
LIMIT 50;`,
  },
  {
    id: "cache-hit",
    group: "Health",
    title: "Cache hit rate",
    about: "How much of each table is answered from memory rather than from disk.",
    sql: `SELECT
  schemaname AS schema,
  relname AS "table",
  heap_blks_hit AS from_cache,
  heap_blks_read AS from_disk,
  round(100.0 * heap_blks_hit / NULLIF(heap_blks_hit + heap_blks_read, 0), 1) AS hit_percent
FROM pg_statio_user_tables
WHERE heap_blks_hit + heap_blks_read > 0
ORDER BY heap_blks_read DESC
LIMIT 50;`,
  },
  {
    id: "locks",
    group: "Activity",
    title: "Who is waiting on whom",
    about: "Each blocked session beside the session holding the lock it waits for.",
    sql: `SELECT
  waiting.pid AS waiting_pid,
  waiting.usename AS waiting_user,
  now() - waiting.query_start AS waiting_for,
  left(waiting.query, 120) AS waiting_statement,
  holder.pid AS holding_pid,
  holder.usename AS holding_user,
  holder.state AS holding_state,
  left(holder.query, 120) AS holding_statement
FROM pg_stat_activity waiting
JOIN LATERAL unnest(pg_blocking_pids(waiting.pid)) AS blocker(pid) ON true
JOIN pg_stat_activity holder ON holder.pid = blocker.pid
ORDER BY waiting.query_start;`,
  },
  {
    id: "long-running",
    group: "Activity",
    title: "Long-running statements",
    about: "What is running now, longest first, idle sessions left out.",
    sql: `SELECT
  pid,
  usename AS "user",
  application_name AS application,
  state,
  wait_event_type || ': ' || wait_event AS waiting_on,
  now() - query_start AS running_for,
  left(query, 200) AS statement
FROM pg_stat_activity
WHERE state <> 'idle'
  AND pid <> pg_backend_pid()
ORDER BY query_start;`,
  },
  {
    id: "connections",
    group: "Activity",
    title: "Sessions by application",
    about: "Who is connected, by application and state, against the server's limit.",
    sql: `SELECT
  coalesce(nullif(application_name, ''), '(unnamed)') AS application,
  usename AS "user",
  state,
  count(*) AS sessions,
  current_setting('max_connections')::int AS "limit"
FROM pg_stat_activity
WHERE backend_type = 'client backend'
GROUP BY 1, 2, 3
ORDER BY sessions DESC;`,
  },
  {
    id: "unused-indexes",
    group: "Indexes",
    title: "Unused indexes",
    about: "Indexes never read since statistics were last reset, keys and unique ones left out.",
    sql: `SELECT
  s.schemaname AS schema,
  s.relname AS "table",
  s.indexrelname AS index,
  pg_size_pretty(pg_relation_size(s.indexrelid)) AS size
FROM pg_stat_user_indexes s
JOIN pg_index i ON i.indexrelid = s.indexrelid
WHERE s.idx_scan = 0
  AND NOT i.indisunique
  AND NOT i.indisprimary
ORDER BY pg_relation_size(s.indexrelid) DESC;`,
  },
  {
    id: "seq-scans",
    group: "Indexes",
    title: "Tables read without an index",
    about: "Where sequential scans read the most rows: the first place an index may be missing.",
    sql: `SELECT
  schemaname AS schema,
  relname AS "table",
  seq_scan AS sequential_scans,
  seq_tup_read AS rows_read,
  idx_scan AS index_scans,
  n_live_tup AS live_rows
FROM pg_stat_user_tables
WHERE seq_scan > 0
ORDER BY seq_tup_read DESC
LIMIT 50;`,
  },
  {
    id: "no-primary-key",
    group: "Health",
    title: "Tables without a primary key",
    about:
      "Tables whose rows nothing identifies: they cannot be replicated logically or edited safely.",
    sql: `SELECT
  n.nspname AS schema,
  c.relname AS "table",
  c.reltuples::bigint AS estimated_rows
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind = 'r'
  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
  AND NOT EXISTS (
    SELECT 1
    FROM pg_index i
    WHERE i.indrelid = c.oid AND i.indisprimary
  )
ORDER BY c.reltuples DESC;`,
  },
  {
    id: "settings",
    group: "Configuration",
    title: "Settings changed from their defaults",
    about: "Every parameter that is not at its built-in value, and where it was set.",
    sql: `SELECT
  name,
  setting,
  unit,
  source,
  pending_restart
FROM pg_settings
WHERE source NOT IN ('default', 'override')
ORDER BY name;`,
  },
]

const MYSQL: Snippet[] = [
  {
    id: "table-sizes",
    group: "Size",
    title: "Largest tables",
    about: "Each table of this database with its data and its indexes, largest first.",
    sql: `SELECT
  table_name AS \`table\`,
  engine,
  table_rows AS estimated_rows,
  round(data_length / 1048576, 1) AS data_mb,
  round(index_length / 1048576, 1) AS index_mb,
  round((data_length + index_length) / 1048576, 1) AS total_mb
FROM information_schema.tables
WHERE table_schema = database()
  AND table_type = 'BASE TABLE'
ORDER BY data_length + index_length DESC
LIMIT 50;`,
  },
  {
    id: "database-sizes",
    group: "Size",
    title: "Databases on this server",
    about: "Every database the account can see, by size.",
    sql: `SELECT
  table_schema AS \`database\`,
  count(*) AS tables,
  round(sum(data_length + index_length) / 1048576, 1) AS total_mb
FROM information_schema.tables
GROUP BY table_schema
ORDER BY sum(data_length + index_length) DESC;`,
  },
  {
    id: "fragmentation",
    group: "Health",
    title: "Bloat: space a table could give back",
    about: "Free space inside each table's file, which a rebuild of the table would release.",
    sql: `SELECT
  table_name AS \`table\`,
  engine,
  round(data_length / 1048576, 1) AS data_mb,
  round(data_free / 1048576, 1) AS free_mb,
  round(100 * data_free / NULLIF(data_length + index_length + data_free, 0), 1) AS free_percent
FROM information_schema.tables
WHERE table_schema = database()
  AND data_free > 0
ORDER BY data_free DESC
LIMIT 50;`,
  },
  {
    id: "long-running",
    group: "Activity",
    title: "Long-running statements",
    about: "What is running now, longest first, sleeping sessions left out.",
    sql: `SELECT
  id,
  user,
  host,
  db,
  command,
  time AS seconds,
  state,
  left(info, 200) AS statement
FROM information_schema.processlist
WHERE command <> 'Sleep'
  AND id <> connection_id()
ORDER BY time DESC;`,
  },
  {
    id: "connections",
    group: "Activity",
    title: "Sessions by account",
    about: "Who is connected, by account and what they are doing.",
    sql: `SELECT
  user,
  substring_index(host, ':', 1) AS client,
  db,
  command,
  count(*) AS sessions
FROM information_schema.processlist
GROUP BY user, client, db, command
ORDER BY sessions DESC;`,
  },
  {
    id: "transactions",
    group: "Activity",
    title: "Open transactions",
    about:
      "Every InnoDB transaction, oldest first: the ones that hold locks and block a purge. Needs the PROCESS privilege.",
    sql: `SELECT
  trx_mysql_thread_id AS session,
  trx_state AS state,
  trx_started AS started,
  timestampdiff(SECOND, trx_started, now()) AS seconds,
  trx_rows_locked AS rows_locked,
  trx_rows_modified AS rows_changed,
  left(trx_query, 200) AS statement
FROM information_schema.innodb_trx
ORDER BY trx_started;`,
  },
  {
    id: "indexes",
    group: "Indexes",
    title: "Indexes and what they cover",
    about: "Every index of this database with its columns in order and how selective it is.",
    sql: `SELECT
  table_name AS \`table\`,
  index_name AS \`index\`,
  group_concat(column_name ORDER BY seq_in_index) AS columns,
  max(non_unique) = 0 AS is_unique,
  max(cardinality) AS distinct_values
FROM information_schema.statistics
WHERE table_schema = database()
GROUP BY table_name, index_name
ORDER BY table_name, index_name;`,
  },
  {
    id: "no-primary-key",
    group: "Health",
    title: "Tables without a primary key",
    about: "Tables whose rows nothing identifies: slow to replicate and unsafe to edit by row.",
    sql: `SELECT
  t.table_name AS \`table\`,
  t.engine,
  t.table_rows AS estimated_rows
FROM information_schema.tables t
LEFT JOIN information_schema.table_constraints c
  ON c.table_schema = t.table_schema
  AND c.table_name = t.table_name
  AND c.constraint_type = 'PRIMARY KEY'
WHERE t.table_schema = database()
  AND t.table_type = 'BASE TABLE'
  AND c.constraint_name IS NULL
ORDER BY t.table_rows DESC;`,
  },
]

const MYSQL_ONLY: Snippet[] = [
  {
    id: "locks",
    group: "Activity",
    title: "Who is waiting on whom",
    about:
      "Each waiting transaction beside the one holding the lock it waits for. Needs the right to read the sys schema.",
    sql: `SELECT
  waiting_pid,
  left(waiting_query, 120) AS waiting_statement,
  wait_age AS waiting_for,
  blocking_pid,
  left(blocking_query, 120) AS blocking_statement,
  locked_table,
  locked_index
FROM sys.innodb_lock_waits
ORDER BY wait_started;`,
  },
  {
    id: "unused-indexes",
    group: "Indexes",
    title: "Unused indexes",
    about:
      "Indexes not read since the server started, primary keys left out. Needs the right to read the sys schema.",
    sql: `SELECT
  object_schema AS \`database\`,
  object_name AS \`table\`,
  index_name AS \`index\`
FROM sys.schema_unused_indexes
WHERE object_schema = database()
ORDER BY object_name, index_name;`,
  },
  {
    id: "cache-hit",
    group: "Health",
    title: "Cache hit rate",
    about: "How many page reads the buffer pool answered without going to disk.",
    sql: `SELECT
  requests.variable_value AS logical_reads,
  disk.variable_value AS from_disk,
  round(100 * (1 - disk.variable_value / NULLIF(requests.variable_value, 0)), 2) AS hit_percent
FROM performance_schema.global_status requests
JOIN performance_schema.global_status disk
  ON disk.variable_name = 'Innodb_buffer_pool_reads'
WHERE requests.variable_name = 'Innodb_buffer_pool_read_requests';`,
  },
  {
    id: "slowest",
    group: "Activity",
    title: "Statements that took the longest",
    about:
      "Statement shapes by total time since the server started. Needs the right to read performance_schema.",
    sql: `SELECT
  left(digest_text, 200) AS statement,
  count_star AS calls,
  round(sum_timer_wait / 1e12, 3) AS total_seconds,
  round(avg_timer_wait / 1e9, 2) AS mean_ms,
  sum_rows_examined AS rows_examined
FROM performance_schema.events_statements_summary_by_digest
WHERE schema_name = database()
ORDER BY sum_timer_wait DESC
LIMIT 25;`,
  },
]

const MARIADB_ONLY: Snippet[] = [
  {
    id: "locks",
    group: "Activity",
    title: "Who is waiting on whom",
    about:
      "Each waiting transaction beside the one holding the lock it waits for. Needs the PROCESS privilege.",
    sql: `SELECT
  waiting.trx_mysql_thread_id AS waiting_session,
  left(waiting.trx_query, 120) AS waiting_statement,
  waiting.trx_wait_started AS waiting_since,
  holder.trx_mysql_thread_id AS holding_session,
  left(holder.trx_query, 120) AS holding_statement
FROM information_schema.innodb_lock_waits w
JOIN information_schema.innodb_trx waiting ON waiting.trx_id = w.requesting_trx_id
JOIN information_schema.innodb_trx holder ON holder.trx_id = w.blocking_trx_id
ORDER BY waiting.trx_wait_started;`,
  },
  {
    id: "cache-hit",
    group: "Health",
    title: "Cache hit rate",
    about: "How many page reads the buffer pool answered without going to disk.",
    sql: `SELECT
  requests.variable_value AS logical_reads,
  disk.variable_value AS from_disk,
  round(100 * (1 - disk.variable_value / NULLIF(requests.variable_value, 0)), 2) AS hit_percent
FROM information_schema.global_status requests
JOIN information_schema.global_status disk
  ON disk.variable_name = 'INNODB_BUFFER_POOL_READS'
WHERE requests.variable_name = 'INNODB_BUFFER_POOL_READ_REQUESTS';`,
  },
  {
    id: "unused-indexes",
    group: "Indexes",
    title: "Indexes with nothing to tell apart",
    about:
      "Indexes whose columns hold one value or none: the optimizer has no reason to read them.",
    sql: `SELECT
  table_name AS \`table\`,
  index_name AS \`index\`,
  group_concat(column_name ORDER BY seq_in_index) AS columns,
  max(cardinality) AS distinct_values
FROM information_schema.statistics
WHERE table_schema = database()
  AND non_unique = 1
GROUP BY table_name, index_name
HAVING coalesce(max(cardinality), 0) <= 1
ORDER BY table_name, index_name;`,
  },
]

const SQLITE: Snippet[] = [
  {
    id: "table-sizes",
    group: "Size",
    title: "Largest tables and indexes",
    about: "The pages each table and index takes in the file. Reads every page once.",
    sql: `SELECT
  name,
  count(*) AS pages,
  sum(pgsize) AS bytes,
  sum(unused) AS unused_bytes
FROM dbstat
GROUP BY name
ORDER BY sum(pgsize) DESC;`,
  },
  {
    id: "file",
    group: "Size",
    title: "The file in figures",
    about: "Page size, pages in the file, pages on the free list, and how it journals.",
    sql: `SELECT
  page_size,
  page_count,
  page_size * page_count AS file_bytes,
  freelist_count AS free_pages,
  journal_mode
FROM pragma_page_size, pragma_page_count, pragma_freelist_count, pragma_journal_mode;`,
  },
  {
    id: "integrity",
    group: "Health",
    title: "Integrity check",
    about: "Reads the whole file and reports what is damaged; one row saying ok when nothing is.",
    sql: `SELECT integrity_check AS finding
FROM pragma_integrity_check;`,
  },
  {
    id: "foreign-keys",
    group: "Health",
    title: "Rows that break a foreign key",
    about: "Every row pointing at a parent that is not there. Empty when all keys hold.",
    sql: `SELECT
  "table",
  rowid,
  parent,
  fkid AS key_number
FROM pragma_foreign_key_check;`,
  },
  {
    id: "indexes",
    group: "Indexes",
    title: "Indexes and what they cover",
    about: "Every index with its table, whether it is unique, and where it came from.",
    sql: `SELECT
  m.name AS "table",
  il.name AS "index",
  il."unique" AS is_unique,
  il.origin,
  il.partial
FROM sqlite_master m
JOIN pragma_index_list(m.name) il
WHERE m.type = 'table'
ORDER BY m.name, il.name;`,
  },
  {
    id: "no-primary-key",
    group: "Health",
    title: "Tables without a primary key",
    about: "Tables whose rows only their rowid tells apart.",
    sql: `SELECT m.name AS "table"
FROM sqlite_master m
WHERE m.type = 'table'
  AND m.name NOT LIKE 'sqlite_%'
  AND NOT EXISTS (
    SELECT 1
    FROM pragma_table_info(m.name) c
    WHERE c.pk > 0
  )
ORDER BY m.name;`,
  },
  {
    id: "schema",
    group: "Configuration",
    title: "Everything the file defines",
    about: "Each table, index, view and trigger with the statement that made it.",
    sql: `SELECT
  type,
  name,
  tbl_name AS "on",
  sql
FROM sqlite_master
ORDER BY type, name;`,
  },
  {
    id: "compile-options",
    group: "Configuration",
    title: "What this SQLite was built with",
    about: "The compile-time options: which extensions and limits this build has.",
    sql: `SELECT compile_options AS option
FROM pragma_compile_options
ORDER BY 1;`,
  },
]

const SQLSERVER: Snippet[] = [
  {
    id: "table-sizes",
    group: "Size",
    title: "Largest tables",
    about: "Each table with the space it reserves and uses, largest first.",
    sql: `SELECT TOP 50
  s.name AS [schema],
  t.name AS [table],
  SUM(CASE WHEN p.index_id IN (0, 1) THEN p.row_count ELSE 0 END) AS [rows],
  SUM(p.reserved_page_count) * 8 / 1024.0 AS reserved_mb,
  SUM(p.used_page_count) * 8 / 1024.0 AS used_mb
FROM sys.dm_db_partition_stats p
JOIN sys.tables t ON t.object_id = p.object_id
JOIN sys.schemas s ON s.schema_id = t.schema_id
GROUP BY s.name, t.name
ORDER BY SUM(p.reserved_page_count) DESC;`,
  },
  {
    id: "fragmentation",
    group: "Health",
    title: "Bloat: fragmented indexes",
    about: "How fragmented each index of more than a few pages is.",
    sql: `SELECT TOP 50
  OBJECT_SCHEMA_NAME(f.object_id) AS [schema],
  OBJECT_NAME(f.object_id) AS [table],
  i.name AS [index],
  f.page_count,
  ROUND(f.avg_fragmentation_in_percent, 1) AS fragmented_percent
FROM sys.dm_db_index_physical_stats(DB_ID(), NULL, NULL, NULL, 'LIMITED') f
JOIN sys.indexes i ON i.object_id = f.object_id AND i.index_id = f.index_id
WHERE f.page_count > 8
ORDER BY f.avg_fragmentation_in_percent DESC;`,
  },
  {
    id: "locks",
    group: "Activity",
    title: "Who is waiting on whom",
    about: "Each blocked request beside the session that blocks it.",
    sql: `SELECT
  r.session_id AS waiting_session,
  r.wait_type,
  r.wait_time / 1000.0 AS waiting_seconds,
  LEFT(t.text, 200) AS waiting_statement,
  r.blocking_session_id AS blocking_session,
  b.login_name AS blocking_login,
  b.program_name AS blocking_program
FROM sys.dm_exec_requests r
JOIN sys.dm_exec_sessions b ON b.session_id = r.blocking_session_id
CROSS APPLY sys.dm_exec_sql_text(r.sql_handle) t
WHERE r.blocking_session_id <> 0
ORDER BY r.wait_time DESC;`,
  },
  {
    id: "long-running",
    group: "Activity",
    title: "Long-running statements",
    about: "What is running now, longest first.",
    sql: `SELECT
  r.session_id AS session,
  s.login_name AS login,
  s.program_name AS program,
  r.status,
  r.command,
  r.total_elapsed_time / 1000.0 AS seconds,
  r.cpu_time AS cpu_ms,
  LEFT(t.text, 200) AS statement
FROM sys.dm_exec_requests r
JOIN sys.dm_exec_sessions s ON s.session_id = r.session_id
CROSS APPLY sys.dm_exec_sql_text(r.sql_handle) t
WHERE r.session_id <> @@SPID
ORDER BY r.total_elapsed_time DESC;`,
  },
  {
    id: "slowest",
    group: "Activity",
    title: "Statements that took the longest",
    about: "Cached statements by total time since they were compiled.",
    sql: `SELECT TOP 25
  LEFT(t.text, 200) AS statement,
  q.execution_count AS calls,
  q.total_elapsed_time / 1000000.0 AS total_seconds,
  q.total_elapsed_time / 1000.0 / q.execution_count AS mean_ms,
  q.total_logical_reads AS logical_reads
FROM sys.dm_exec_query_stats q
CROSS APPLY sys.dm_exec_sql_text(q.sql_handle) t
ORDER BY q.total_elapsed_time DESC;`,
  },
  {
    id: "unused-indexes",
    group: "Indexes",
    title: "Unused indexes",
    about: "Indexes written to and never read since the server started, keys left out.",
    sql: `SELECT
  OBJECT_SCHEMA_NAME(i.object_id) AS [schema],
  OBJECT_NAME(i.object_id) AS [table],
  i.name AS [index],
  COALESCE(u.user_updates, 0) AS writes
FROM sys.indexes i
JOIN sys.tables t ON t.object_id = i.object_id
LEFT JOIN sys.dm_db_index_usage_stats u
  ON u.object_id = i.object_id
  AND u.index_id = i.index_id
  AND u.database_id = DB_ID()
WHERE i.index_id > 1
  AND i.is_primary_key = 0
  AND i.is_unique_constraint = 0
  AND COALESCE(u.user_seeks, 0) + COALESCE(u.user_scans, 0) + COALESCE(u.user_lookups, 0) = 0
ORDER BY writes DESC;`,
  },
  {
    id: "missing-indexes",
    group: "Indexes",
    title: "Indexes the optimizer asked for",
    about: "What the optimizer would have used had it existed, by how much it would have saved.",
    sql: `SELECT TOP 25
  d.statement AS [table],
  d.equality_columns,
  d.inequality_columns,
  d.included_columns,
  s.user_seeks AS seeks,
  ROUND(s.avg_total_user_cost * s.avg_user_impact * s.user_seeks / 100.0, 1) AS estimated_gain
FROM sys.dm_db_missing_index_details d
JOIN sys.dm_db_missing_index_groups g ON g.index_handle = d.index_handle
JOIN sys.dm_db_missing_index_group_stats s ON s.group_handle = g.index_group_handle
WHERE d.database_id = DB_ID()
ORDER BY estimated_gain DESC;`,
  },
  {
    id: "cache",
    group: "Health",
    title: "Memory held per database",
    about: "How much of the buffer cache each database's pages take.",
    sql: `SELECT
  COALESCE(DB_NAME(database_id), 'resource') AS [database],
  COUNT(*) * 8 / 1024.0 AS cached_mb
FROM sys.dm_os_buffer_descriptors
GROUP BY database_id
ORDER BY COUNT(*) DESC;`,
  },
  {
    id: "no-primary-key",
    group: "Health",
    title: "Tables without a primary key",
    about: "Tables whose rows nothing identifies.",
    sql: `SELECT
  s.name AS [schema],
  t.name AS [table]
FROM sys.tables t
JOIN sys.schemas s ON s.schema_id = t.schema_id
WHERE OBJECTPROPERTY(t.object_id, 'TableHasPrimaryKey') = 0
ORDER BY s.name, t.name;`,
  },
]

const CLICKHOUSE: Snippet[] = [
  {
    id: "table-sizes",
    group: "Size",
    title: "Largest tables",
    about:
      "Each table by what it holds on disk, with how well it compresses and how many parts it is in.",
    sql: `SELECT
  database,
  table,
  sum(rows) AS rows,
  formatReadableSize(sum(bytes_on_disk)) AS on_disk,
  formatReadableSize(sum(data_uncompressed_bytes)) AS uncompressed,
  round(sum(data_uncompressed_bytes) / sum(data_compressed_bytes), 2) AS ratio,
  count() AS parts
FROM system.parts
WHERE active
GROUP BY database, table
ORDER BY sum(bytes_on_disk) DESC
LIMIT 50;`,
  },
  {
    id: "column-sizes",
    group: "Size",
    title: "Largest columns",
    about: "Each column by its compressed size: where a codec or a narrower type would pay.",
    sql: `SELECT
  table,
  name AS column,
  type,
  formatReadableSize(data_compressed_bytes) AS compressed,
  formatReadableSize(data_uncompressed_bytes) AS uncompressed,
  round(data_uncompressed_bytes / nullIf(data_compressed_bytes, 0), 2) AS ratio
FROM system.columns
WHERE database = currentDatabase()
ORDER BY data_compressed_bytes DESC
LIMIT 50;`,
  },
  {
    id: "keys",
    group: "Indexes",
    title: "Sorting keys",
    about: "How each table is ordered and partitioned: what a query can skip by.",
    sql: `SELECT
  name AS table,
  engine,
  partition_key,
  sorting_key,
  primary_key,
  total_rows
FROM system.tables
WHERE database = currentDatabase()
ORDER BY name;`,
  },
  {
    id: "skip-indexes",
    group: "Indexes",
    title: "Data-skipping indexes",
    about: "Every skip index of this database with what it is built over and what it weighs.",
    sql: `SELECT
  table,
  name AS index,
  type,
  expr AS over,
  granularity,
  formatReadableSize(data_compressed_bytes) AS size
FROM system.data_skipping_indices
WHERE database = currentDatabase()
ORDER BY table, name;`,
  },
  {
    id: "parts",
    group: "Health",
    title: "Parts per partition",
    about: "Partitions made of many parts: merges are behind, or inserts come too small.",
    sql: `SELECT
  database,
  table,
  partition,
  count() AS parts,
  sum(rows) AS rows,
  formatReadableSize(sum(bytes_on_disk)) AS on_disk
FROM system.parts
WHERE active
GROUP BY database, table, partition
ORDER BY parts DESC
LIMIT 50;`,
  },
  {
    id: "long-running",
    group: "Activity",
    title: "Long-running queries",
    about: "What is running now, longest first, with what it has read so far.",
    sql: `SELECT
  query_id,
  user,
  elapsed AS seconds,
  read_rows,
  formatReadableSize(read_bytes) AS read,
  formatReadableSize(memory_usage) AS memory,
  substring(query, 1, 200) AS statement
FROM system.processes
ORDER BY elapsed DESC;`,
  },
  {
    id: "merges",
    group: "Activity",
    title: "Merges in flight",
    about: "The merges running now and how far each has got.",
    sql: `SELECT
  database,
  table,
  round(elapsed, 1) AS seconds,
  round(progress * 100, 1) AS percent,
  num_parts AS parts,
  formatReadableSize(total_size_bytes_compressed) AS size
FROM system.merges
ORDER BY elapsed DESC;`,
  },
  {
    id: "mutations",
    group: "Activity",
    title: "Mutations not finished",
    about: "Every mutation still rewriting parts, with why the last attempt failed where one did.",
    sql: `SELECT
  database,
  table,
  mutation_id,
  command,
  create_time,
  parts_to_do,
  latest_fail_reason
FROM system.mutations
WHERE NOT is_done
ORDER BY create_time;`,
  },
  {
    id: "slowest",
    group: "Activity",
    title: "Slowest recent queries",
    about: "Finished queries of the last day, slowest first, from the server's query log.",
    sql: `SELECT
  event_time,
  query_duration_ms AS ms,
  read_rows,
  formatReadableSize(read_bytes) AS read,
  formatReadableSize(memory_usage) AS memory,
  substring(query, 1, 200) AS statement
FROM system.query_log
WHERE type = 'QueryFinish'
  AND event_time > now() - INTERVAL 1 DAY
ORDER BY query_duration_ms DESC
LIMIT 25;`,
  },
  {
    id: "disks",
    group: "Health",
    title: "Disks",
    about: "Each disk the server writes to, with what is free on it.",
    sql: `SELECT
  name,
  path,
  formatReadableSize(free_space) AS free,
  formatReadableSize(total_space) AS total,
  round(100 * (1 - free_space / total_space), 1) AS used_percent
FROM system.disks;`,
  },
  {
    id: "settings",
    group: "Configuration",
    title: "Settings changed from their defaults",
    about: "Every setting of this session that is not at its built-in value.",
    sql: `SELECT
  name,
  value,
  description
FROM system.settings
WHERE changed
ORDER BY name;`,
  },
]

const ORACLE: Snippet[] = [
  {
    id: "table-sizes",
    group: "Size",
    title: "Largest segments",
    about: "The tables, indexes and LOBs of this schema by the space they take.",
    sql: `SELECT
  segment_name,
  segment_type,
  ROUND(bytes / 1048576, 1) AS mb
FROM user_segments
ORDER BY bytes DESC
FETCH FIRST 50 ROWS ONLY;`,
  },
  {
    id: "tables",
    group: "Size",
    title: "Tables and their statistics",
    about: "Each table with its rows as last gathered, and when that was.",
    sql: `SELECT
  table_name,
  num_rows,
  blocks,
  last_analyzed
FROM user_tables
ORDER BY num_rows DESC NULLS LAST;`,
  },
  {
    id: "long-running",
    group: "Activity",
    title: "Long-running sessions",
    about: "Sessions working now, longest first. Needs the right to read V$SESSION.",
    sql: `SELECT
  sid,
  serial#,
  username,
  program,
  status,
  last_call_et AS seconds,
  sql_id
FROM v$session
WHERE status = 'ACTIVE'
  AND username IS NOT NULL
ORDER BY last_call_et DESC;`,
  },
  {
    id: "locks",
    group: "Activity",
    title: "Who is waiting on whom",
    about:
      "Each waiting session beside the session that blocks it. Needs the right to read V$SESSION.",
    sql: `SELECT
  waiting.sid AS waiting_sid,
  waiting.username AS waiting_user,
  waiting.seconds_in_wait AS waiting_seconds,
  waiting.blocking_session AS blocking_sid,
  holder.username AS blocking_user,
  holder.program AS blocking_program
FROM v$session waiting
JOIN v$session holder ON holder.sid = waiting.blocking_session
ORDER BY waiting.seconds_in_wait DESC;`,
  },
  {
    id: "indexes",
    group: "Indexes",
    title: "Indexes and their state",
    about: "Every index of this schema: its table, whether it is unique, and whether it is usable.",
    sql: `SELECT
  table_name,
  index_name,
  uniqueness,
  status,
  distinct_keys,
  last_analyzed
FROM user_indexes
ORDER BY table_name, index_name;`,
  },
  {
    id: "invalid",
    group: "Health",
    title: "Objects that do not compile",
    about: "Views, packages, procedures and triggers the database marks invalid.",
    sql: `SELECT
  object_type,
  object_name,
  status,
  last_ddl_time
FROM user_objects
WHERE status <> 'VALID'
ORDER BY object_type, object_name;`,
  },
  {
    id: "no-primary-key",
    group: "Health",
    title: "Tables without a primary key",
    about: "Tables whose rows nothing identifies.",
    sql: `SELECT t.table_name
FROM user_tables t
WHERE NOT EXISTS (
  SELECT 1
  FROM user_constraints c
  WHERE c.table_name = t.table_name AND c.constraint_type = 'P'
)
ORDER BY t.table_name;`,
  },
  {
    id: "settings",
    group: "Configuration",
    title: "Parameters changed from their defaults",
    about: "Every parameter that is not at its default. Needs the right to read V$PARAMETER.",
    sql: `SELECT
  name,
  value,
  ismodified
FROM v$parameter
WHERE isdefault = 'FALSE'
ORDER BY name;`,
  },
]

/**
 * By what the server is, then by how it is spoken to: MariaDB keeps its lock
 * waits and its counters in other views than MySQL does, and everything the
 * two share is written once.
 */
const SNIPPETS: Record<string, Snippet[]> = {
  postgres: POSTGRES,
  mysql: [...MYSQL, ...MYSQL_ONLY],
  mariadb: [...MYSQL, ...MARIADB_ONLY],
  sqlite: SQLITE,
  sqlserver: SQLSERVER,
  clickhouse: CLICKHOUSE,
  oracle: ORACLE,
}

/** The engines that have diagnostics written for them. */
export const SNIPPET_ENGINES = Object.keys(SNIPPETS)

/** The diagnostics for an engine: by its own id where it has a list, else by its driver's. */
export function snippetsFor(engineId: string, driver: string): Snippet[] {
  if (Object.hasOwn(SNIPPETS, engineId)) return SNIPPETS[engineId]
  if (Object.hasOwn(SNIPPETS, driver)) return SNIPPETS[driver]
  return []
}
