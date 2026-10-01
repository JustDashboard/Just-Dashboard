package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// PostgreSQL's answers to the operations questions. Everything here reads the
// statistics views and catalogues a monitoring role can read; nothing needs a
// superuser except where the comment beside it says so.

// --- snapshot ----------------------------------------------------------------

// ServerStats reads pg_stat_database for the connection's own database and
// the server-wide figures beside it.
//
// The counters are the database's, not the server's: a connection opens one
// database, and summing every database's counters would chart the load of
// databases this page cannot otherwise see. Connections are the exception —
// max_connections is one budget for the whole server, so they are counted
// across it.
func (postgresDialect) ServerStats(ctx context.Context, db *sql.DB) (*ServerStats, error) {
	out := &ServerStats{Counters: map[string]float64{}, Gauges: map[string]float64{}, Facts: map[string]string{}}
	var (
		started, reset                                    sql.NullTime
		recovery                                          bool
		limit, reserved                                   int
		commit, rollback, blksRead, blksHit               float64
		returned, fetched, inserted, updated, deleted     float64
		conflicts, tempFiles, tempBytes, deadlocks        float64
		readTime, writeTime, backends, xidAge, wal, repls float64
		checkpointer                                      bool
	)
	// pg_current_wal_lsn() raises on a standby, which is why the two halves
	// sit in a CASE: only the branch that applies is evaluated.
	err := db.QueryRowContext(ctx, `
	  SELECT version(), pg_postmaster_start_time(), pg_is_in_recovery(), current_database(),
	         pg_database_size(current_database()),
	         current_setting('max_connections')::int,
	         current_setting('superuser_reserved_connections')::int,
	         d.xact_commit, d.xact_rollback, d.blks_read, d.blks_hit,
	         d.tup_returned, d.tup_fetched, d.tup_inserted, d.tup_updated, d.tup_deleted,
	         d.conflicts, d.temp_files, d.temp_bytes, d.deadlocks,
	         COALESCE(d.blk_read_time, 0)::float8, COALESCE(d.blk_write_time, 0)::float8,
	         d.stats_reset, d.numbackends,
	         (SELECT age(datfrozenxid) FROM pg_database WHERE datname = current_database()),
	         (CASE WHEN pg_is_in_recovery()
	               THEN pg_wal_lsn_diff(pg_last_wal_replay_lsn(), '0/0')
	               ELSE pg_wal_lsn_diff(pg_current_wal_lsn(), '0/0') END)::float8,
	         (SELECT count(*) FROM pg_stat_replication),
	         to_regclass('pg_catalog.pg_stat_checkpointer') IS NOT NULL
	  FROM pg_stat_database d
	  WHERE d.datname = current_database()`).Scan(
		&out.Version, &started, &recovery, &out.Database, &out.DatabaseBytes, &limit, &reserved,
		&commit, &rollback, &blksRead, &blksHit, &returned, &fetched, &inserted, &updated, &deleted,
		&conflicts, &tempFiles, &tempBytes, &deadlocks, &readTime, &writeTime, &reset, &backends,
		&xidAge, &wal, &repls, &checkpointer)
	if err != nil {
		return nil, err
	}
	out.At = time.Now().UTC()
	out.StartedAt = timePtr(started)
	if out.StartedAt != nil {
		out.UptimeSeconds = math.Round(out.At.Sub(*out.StartedAt).Seconds())
	}
	out.CountersSince = timePtr(reset)
	switch {
	case recovery:
		out.Role = "replica"
	case repls > 0:
		out.Role = "primary"
	default:
		out.Role = "standalone"
	}
	out.Counters[StatTransactionsCommitted] = commit
	out.Counters[StatTransactionsRolledBack] = rollback
	out.Counters[StatBlocksRead] = blksRead
	out.Counters[StatBlocksHit] = blksHit
	// "Rows read" is both ways a row leaves a table: handed back by a scan
	// and fetched through an index.
	out.Counters[StatRowsRead] = returned + fetched
	out.Counters[StatRowsWritten] = inserted + updated + deleted
	out.Counters["rowsReturned"] = returned
	out.Counters["rowsFetched"] = fetched
	out.Counters["rowsInserted"] = inserted
	out.Counters["rowsUpdated"] = updated
	out.Counters["rowsDeleted"] = deleted
	out.Counters[StatDeadlocks] = deadlocks
	out.Counters[StatTempFiles] = tempFiles
	out.Counters[StatTempBytes] = tempBytes
	out.Counters["conflicts"] = conflicts
	out.Counters["blockReadMs"] = readTime
	out.Counters["blockWriteMs"] = writeTime
	out.Counters["walBytes"] = wal
	out.Gauges["databaseConnections"] = backends
	// Transaction-id age is the distance to wraparound: at two billion the
	// server stops accepting writes until a vacuum freezes the old rows.
	out.Gauges["transactionIdAge"] = xidAge
	out.Gauges["replicas"] = repls

	conns := &ConnectionCounts{Max: limit, Reserved: reserved}
	rows, err := db.QueryContext(ctx, `
	  SELECT COALESCE(state, ''), count(*), count(*) FILTER (WHERE wait_event_type = 'Lock')
	  FROM pg_stat_activity
	  WHERE backend_type = 'client backend'
	  GROUP BY 1`)
	if err != nil {
		out.Notes = append(out.Notes, "Sessions could not be counted: "+err.Error())
	} else {
		for rows.Next() {
			var state string
			var n, waiting int
			if err := rows.Scan(&state, &n, &waiting); err != nil {
				rows.Close()
				return nil, err
			}
			conns.Total += n
			conns.Waiting += waiting
			switch {
			case state == "active" || state == "fastpath function call":
				conns.Active += n
			case strings.HasPrefix(state, "idle in transaction"):
				conns.IdleInTransaction += n
			default:
				conns.Idle += n
			}
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		out.Connections = conns
	}

	var oldestTx, longestQuery float64
	if err := db.QueryRowContext(ctx, `
	  SELECT COALESCE(EXTRACT(EPOCH FROM (clock_timestamp() - min(xact_start))), 0)::float8,
	         COALESCE(EXTRACT(EPOCH FROM (clock_timestamp() - min(query_start) FILTER (WHERE state = 'active'))), 0)::float8
	  FROM pg_stat_activity
	  WHERE backend_type = 'client backend' AND pid <> pg_backend_pid()`).Scan(&oldestTx, &longestQuery); err == nil {
		out.Gauges["oldestTransactionSeconds"] = math.Max(oldestTx, 0)
		out.Gauges["longestQuerySeconds"] = math.Max(longestQuery, 0)
	}
	if recovery {
		var lag float64
		if err := db.QueryRowContext(ctx, `
		  SELECT COALESCE(EXTRACT(EPOCH FROM (clock_timestamp() - pg_last_xact_replay_timestamp())), -1)::float8`).Scan(&lag); err == nil && lag >= 0 {
			out.Gauges["replicationLagSeconds"] = lag
		}
	}
	// The checkpoint counters moved from pg_stat_bgwriter to
	// pg_stat_checkpointer in 17. Which view to read was asked above, of the
	// catalogue: trying the new one and falling back on the error would work,
	// and would write that error to the server's log on every poll of every
	// server older than 17.
	checkpoints := `SELECT checkpoints_timed, checkpoints_req FROM pg_stat_bgwriter`
	if checkpointer {
		checkpoints = `SELECT num_timed, num_requested FROM pg_stat_checkpointer`
	}
	var timed, requested float64
	if err := db.QueryRowContext(ctx, checkpoints).Scan(&timed, &requested); err == nil {
		out.Counters["checkpointsTimed"] = timed
		out.Counters["checkpointsRequested"] = requested
	}
	return out, nil
}

// --- sessions ----------------------------------------------------------------

// Sessions reads pg_stat_activity with the columns Activity left out: what
// state the session is in and since when, which application opened it, when
// its transaction began. pg_blocking_pids takes the lock manager's own lock,
// so it is asked only about sessions that are actually waiting for one.
func (postgresDialect) Sessions(ctx context.Context, db *sql.DB) ([]Activity, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT a.pid::text,
	         COALESCE(a.usename, ''),
	         COALESCE(a.datname, ''),
	         COALESCE(a.state, ''),
	         GREATEST(COALESCE(EXTRACT(EPOCH FROM (clock_timestamp() - a.query_start)), 0), 0)::float8,
	         GREATEST(COALESCE(EXTRACT(EPOCH FROM (clock_timestamp() - a.state_change)), 0), 0)::float8,
	         GREATEST(COALESCE(EXTRACT(EPOCH FROM (clock_timestamp() - a.xact_start)), 0), 0)::float8,
	         COALESCE(a.query, ''),
	         COALESCE(host(a.client_addr), ''),
	         COALESCE(a.application_name, ''),
	         COALESCE(a.wait_event_type, ''),
	         COALESCE(a.wait_event, ''),
	         CASE WHEN a.wait_event_type = 'Lock'
	              THEN COALESCE((SELECT string_agg(b::text, ',') FROM unnest(pg_blocking_pids(a.pid)) b), '')
	              ELSE '' END,
	         a.xact_start, a.query_start, a.state_change, a.backend_start,
	         CASE WHEN a.pid = pg_backend_pid() THEN 1 ELSE 0 END
	  FROM pg_stat_activity a
	  WHERE a.backend_type = 'client backend'
	  ORDER BY a.query_start NULLS LAST`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Activity{}
	for rows.Next() {
		var (
			a                               Activity
			sinceQuery, sinceState, sinceTx float64
			blocked                         string
			txStart, qStart, stStart, begun sql.NullTime
			self                            int
		)
		if err := rows.Scan(&a.PID, &a.User, &a.Database, &a.State, &sinceQuery, &sinceState, &sinceTx,
			&a.Query, &a.Client, &a.Application, &a.WaitType, &a.WaitEvent, &blocked,
			&txStart, &qStart, &stStart, &begun, &self); err != nil {
			return nil, err
		}
		a.Self = self != 0
		a.BlockedBy = blocked
		if a.WaitType != "" {
			a.Wait = a.WaitType + ":" + a.WaitEvent
		}
		a.TransactionStart, a.QueryStart = timePtr(txStart), timePtr(qStart)
		a.StateSince, a.ConnectedAt = timePtr(stStart), timePtr(begun)
		if txStart.Valid {
			a.TransactionSeconds = sinceTx
		}
		switch {
		case a.State == "active" || a.State == "fastpath function call":
			a.Status = SessionActive
			a.Seconds = sinceQuery
		case strings.HasPrefix(a.State, "idle in transaction"):
			a.Status = SessionIdleInTransaction
			a.IdleSeconds = sinceState
		default:
			a.Status = SessionIdle
			a.IdleSeconds = sinceState
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Cancel stops the statement and leaves the session. The function answers
// false for a pid that is no longer a backend, which is reported rather than
// passed off as a success.
func (postgresDialect) Cancel(ctx context.Context, db *sql.DB, pid string) error {
	if err := validatePID(pid); err != nil {
		return err
	}
	var sent bool
	if err := db.QueryRowContext(ctx, "SELECT pg_cancel_backend($1::int)", pid).Scan(&sent); err != nil {
		return err
	}
	if !sent {
		return fmt.Errorf("session %s is no longer running", pid)
	}
	return nil
}

// --- locks -------------------------------------------------------------------

// pgLockObject names what a pg_locks row is on. A relation is resolved to its
// name only inside this database: an oid from another database's catalogue
// would resolve to whatever shares the number here.
const pgLockObject = `CASE
	  WHEN l.relation IS NOT NULL AND l.database = (SELECT oid FROM pg_database WHERE datname = current_database())
	    THEN COALESCE((SELECT n.nspname || '.' || c.relname FROM pg_class c
	                   JOIN pg_namespace n ON n.oid = c.relnamespace WHERE c.oid = l.relation),
	                  'relation ' || l.relation::text)
	         || CASE WHEN l.locktype = 'tuple' THEN ' (row ' || l.page::text || ',' || l.tuple::text || ')' ELSE '' END
	  WHEN l.relation IS NOT NULL
	    THEN 'relation ' || l.relation::text || ' in '
	         || COALESCE((SELECT datname FROM pg_database WHERE oid = l.database), 'another database')
	  WHEN l.locktype = 'transactionid' THEN 'transaction ' || l.transactionid::text
	  WHEN l.locktype = 'virtualxid' THEN 'virtual transaction ' || l.virtualxid
	  WHEN l.locktype = 'advisory' THEN 'advisory lock ' || l.classid::text || ',' || l.objid::text
	  ELSE l.locktype
	END`

// Locks joins each ungranted lock to the sessions pg_blocking_pids says are
// in its way. waitstart only exists from 14, so it is read through to_jsonb —
// a missing key is NULL rather than an error — and an older server falls back
// to when the waiter last changed state, which is when it began to wait.
func (postgresDialect) Locks(ctx context.Context, db *sql.DB) (*LocksReport, error) {
	out := &LocksReport{Supported: true, Waits: []LockWait{}, Locks: []HeldLock{}}
	rows, err := db.QueryContext(ctx, `
	  SELECT l.pid::text, bp.pid::text,
	         COALESCE(wa.usename, ''), COALESCE(ba.usename, ''),
	         COALESCE(LEFT(wa.query, 2000), ''), COALESCE(LEFT(ba.query, 2000), ''),
	         COALESCE(ba.state, ''),
	         `+pgLockObject+`,
	         l.locktype, l.mode,
	         GREATEST(COALESCE(EXTRACT(EPOCH FROM (clock_timestamp()
	           - COALESCE((to_jsonb(l) ->> 'waitstart')::timestamptz, wa.state_change))), 0), 0)::float8,
	         GREATEST(COALESCE(EXTRACT(EPOCH FROM (clock_timestamp() - ba.xact_start)), 0), 0)::float8
	  FROM pg_locks l
	  JOIN pg_stat_activity wa ON wa.pid = l.pid
	  CROSS JOIN LATERAL unnest(pg_blocking_pids(l.pid)) AS bp(pid)
	  LEFT JOIN pg_stat_activity ba ON ba.pid = bp.pid
	  WHERE NOT l.granted
	  ORDER BY 11 DESC
	  LIMIT `+itoa(maxHeldLocks))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var w LockWait
		if err := rows.Scan(&w.WaitingPID, &w.BlockingPID, &w.WaitingUser, &w.BlockingUser,
			&w.WaitingQuery, &w.BlockingQuery, &w.BlockingState, &w.Object, &w.LockType, &w.Mode,
			&w.WaitSeconds, &w.BlockingSeconds); err != nil {
			rows.Close()
			return nil, err
		}
		out.Waits = append(out.Waits, w)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	// The table itself, for the page that wants to see what a session holds.
	// Virtual transaction ids are left out: every session holds one on itself
	// for as long as it has a transaction, and they say nothing.
	rows, err = db.QueryContext(ctx, `
	  SELECT l.pid::text, COALESCE(a.usename, ''), l.locktype, l.mode, l.granted,
	         `+pgLockObject+`,
	         COALESCE(a.state, ''), COALESCE(LEFT(a.query, 500), '')
	  FROM pg_locks l
	  LEFT JOIN pg_stat_activity a ON a.pid = l.pid
	  WHERE l.pid IS DISTINCT FROM pg_backend_pid()
	    AND l.locktype <> 'virtualxid'
	    AND (l.database IS NULL OR l.database = 0
	         OR l.database = (SELECT oid FROM pg_database WHERE datname = current_database()))
	  ORDER BY l.granted, l.pid
	  LIMIT `+itoa(maxHeldLocks+1))
	if err != nil {
		out.Notes = append(out.Notes, "The lock table could not be read: "+err.Error())
		return out, nil
	}
	defer rows.Close()
	for rows.Next() {
		var h HeldLock
		var pid sql.NullString
		if err := rows.Scan(&pid, &h.User, &h.LockType, &h.Mode, &h.Granted, &h.Object, &h.State, &h.Query); err != nil {
			return nil, err
		}
		h.PID = pid.String
		if len(out.Locks) == maxHeldLocks {
			out.Truncated = true
			break
		}
		out.Locks = append(out.Locks, h)
	}
	return out, rows.Err()
}

// --- replication -------------------------------------------------------------

// pgCurrentLSN is the position lag is measured from: what a primary has
// written, or what a standby has received when it is itself being followed.
const pgCurrentLSN = `CASE WHEN pg_is_in_recovery() THEN pg_last_wal_receive_lsn() ELSE pg_current_wal_lsn() END`

// Replication reads both directions and both kinds. Each part is read on its
// own and a refusal becomes a note: pg_stat_replication hides its columns from
// a role that is not a superuser or a member of pg_monitor, and that should
// cost the page that one table rather than all five.
func (postgresDialect) Replication(ctx context.Context, db *sql.DB) (*ReplicationReport, error) {
	out := &ReplicationReport{Facts: map[string]string{}}
	var recovery bool
	if err := db.QueryRowContext(ctx, `SELECT pg_is_in_recovery()`).Scan(&recovery); err != nil {
		return nil, err
	}
	note := func(what string, err error) {
		out.Notes = append(out.Notes, what+" could not be read: "+err.Error())
	}

	for _, name := range []string{"wal_level", "max_wal_senders", "max_replication_slots", "synchronous_standby_names", "hot_standby"} {
		var v sql.NullString
		if err := db.QueryRowContext(ctx, `SELECT current_setting($1, true)`, name).Scan(&v); err == nil && v.Valid {
			out.Facts[name] = v.String
		}
	}

	rows, err := db.QueryContext(ctx, `
	  SELECT COALESCE(r.application_name, ''), COALESCE(host(r.client_addr), ''),
	         COALESCE(r.state, ''), COALESCE(r.sync_state, ''),
	         COALESCE(r.sent_lsn::text, ''), COALESCE(r.replay_lsn::text, ''),
	         COALESCE(pg_wal_lsn_diff(`+pgCurrentLSN+`, r.replay_lsn), -1)::float8,
	         COALESCE(EXTRACT(EPOCH FROM r.replay_lag), -1)::float8,
	         r.backend_start
	  FROM pg_stat_replication r
	  ORDER BY r.application_name, r.client_addr`)
	if err != nil {
		note("Replicas", err)
	} else {
		for rows.Next() {
			var r Replica
			var lag float64
			var since sql.NullTime
			if err := rows.Scan(&r.Name, &r.Client, &r.State, &r.SyncState, &r.SentLSN, &r.ReplayLSN,
				&lag, &r.LagSeconds, &since); err != nil {
				rows.Close()
				return nil, err
			}
			r.LagBytes, r.Since = int64(lag), timePtr(since)
			out.Replicas = append(out.Replicas, r)
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}

	// wal_status arrived in 13; to_jsonb turns its absence into an empty
	// string on the versions before it.
	rows, err = db.QueryContext(ctx, `
	  SELECT s.slot_name, COALESCE(s.slot_type, ''), COALESCE(s.plugin, ''), COALESCE(s.database, ''),
	         s.active, s.temporary,
	         COALESCE(pg_wal_lsn_diff(`+pgCurrentLSN+`, s.restart_lsn), -1)::float8,
	         COALESCE(to_jsonb(s) ->> 'wal_status', '')
	  FROM pg_replication_slots s
	  ORDER BY s.slot_name`)
	if err != nil {
		note("Replication slots", err)
	} else {
		for rows.Next() {
			var s ReplicationSlot
			var retained float64
			if err := rows.Scan(&s.Name, &s.Type, &s.Plugin, &s.Database, &s.Active, &s.Temporary, &retained, &s.WalStatus); err != nil {
				rows.Close()
				return nil, err
			}
			s.RetainedBytes = int64(retained)
			out.Slots = append(out.Slots, s)
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}

	rows, err = db.QueryContext(ctx, `
	  SELECT p.pubname, pg_get_userbyid(p.pubowner), p.puballtables, p.pubinsert, p.pubupdate, p.pubdelete,
	         COALESCE((to_jsonb(p) ->> 'pubtruncate')::boolean, false),
	         (SELECT count(*) FROM pg_publication_tables t WHERE t.pubname = p.pubname)
	  FROM pg_publication p
	  ORDER BY p.pubname`)
	if err != nil {
		note("Publications", err)
	} else {
		for rows.Next() {
			var p Publication
			if err := rows.Scan(&p.Name, nullText{&p.Owner}, &p.AllTables, &p.Insert, &p.Update, &p.Delete, &p.Truncate, &p.Tables); err != nil {
				rows.Close()
				return nil, err
			}
			out.Publications = append(out.Publications, p)
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}

	// A subscription belongs to one database. Its connection string is not
	// read: it holds the publisher's password, and only a superuser may.
	rows, err = db.QueryContext(ctx, `
	  SELECT s.subname, pg_get_userbyid(s.subowner), s.subenabled,
	         array_to_string(s.subpublications, chr(31)),
	         COALESCE(st.pid, 0) <> 0, COALESCE(st.received_lsn::text, ''), st.latest_end_time
	  FROM pg_subscription s
	  LEFT JOIN LATERAL (
	    SELECT x.pid, x.received_lsn, x.latest_end_time FROM pg_stat_subscription x
	    WHERE x.subid = s.oid AND x.relid IS NULL
	    ORDER BY x.pid NULLS LAST LIMIT 1) st ON true
	  WHERE s.subdbid = (SELECT oid FROM pg_database WHERE datname = current_database())
	  ORDER BY s.subname`)
	if err != nil {
		note("Subscriptions", err)
	} else {
		for rows.Next() {
			var s Subscription
			var pubs string
			var last sql.NullTime
			if err := rows.Scan(&s.Name, nullText{&s.Owner}, &s.Enabled, nullText{&pubs}, &s.Running, &s.ReceivedLSN, &last); err != nil {
				rows.Close()
				return nil, err
			}
			s.Publications = splitUnit(pubs)
			s.LastMessage = timePtr(last)
			out.Subscriptions = append(out.Subscriptions, s)
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}

	if recovery {
		src := ReplicaSource{LagSeconds: -1}
		var status, received, replayed string
		err := db.QueryRowContext(ctx, `
		  SELECT COALESCE(w.status, ''), COALESCE(w.sender_host, ''), COALESCE(w.sender_port::text, ''),
		         COALESCE(pg_last_wal_receive_lsn()::text, ''), COALESCE(pg_last_wal_replay_lsn()::text, ''),
		         COALESCE(EXTRACT(EPOCH FROM (clock_timestamp() - pg_last_xact_replay_timestamp())), -1)::float8
		  FROM (SELECT 1) one
		  LEFT JOIN pg_stat_wal_receiver w ON true`).Scan(&status, &src.Host, &src.Port, &received, &replayed, &src.LagSeconds)
		if err != nil {
			note("The WAL receiver", err)
		} else {
			src.State = status
			// One process receives and one replays; the receiver is the half
			// that can be down while the standby keeps answering reads.
			src.IORunning = status == "streaming"
			src.SQLRunning = true
			src.Position = replayed
			if received != "" && received != replayed {
				src.Position = replayed + " (received " + received + ")"
			}
			out.Sources = append(out.Sources, src)
		}
	}

	switch {
	case recovery:
		out.Role = "replica"
	case len(out.Replicas) > 0:
		out.Role = "primary"
	default:
		out.Role = "standalone"
	}
	return out, nil
}

// splitUnit splits the unit-separator-joined lists the catalogue queries
// return, which is how an array crosses database/sql without a driver type.
func splitUnit(s string) []string {
	if s == "" {
		return []string{}
	}
	return strings.Split(s, "\x1f")
}

// --- table and index statistics ------------------------------------------------

// opsPgSchemaFilter matches one schema, or every schema that is not the
// engine's own when none is named. $1 is the schema.
const opsPgSchemaFilter = `(($1 <> '' AND n.nspname = $1)
	   OR ($1 = '' AND n.nspname NOT IN ('pg_catalog', 'information_schema')
	       AND n.nspname NOT LIKE 'pg\_toast%' AND n.nspname NOT LIKE 'pg\_temp%'))`

// TableStats picks the largest tables first and only then asks for their
// statistics. The other order — compute everything, sort, cut — runs a size
// function and a pg_stats lookup for every table in a schema of ten thousand
// to show two hundred of them.
func (postgresDialect) TableStats(ctx context.Context, db *sql.DB, opts StatsOptions) (*TableStatsReport, error) {
	rows, err := db.QueryContext(ctx, `
	  WITH t AS (
	    SELECT c.oid, n.nspname, c.relname, c.relkind, c.reltuples, c.relpages, c.reltoastrelid, c.reloptions,
	           COALESCE(pg_total_relation_size(c.oid), 0) AS total
	    FROM pg_class c
	    JOIN pg_namespace n ON n.oid = c.relnamespace
	    WHERE c.relkind IN ('r', 'm', 'p') AND `+opsPgSchemaFilter+`
	    ORDER BY total DESC, n.nspname, c.relname
	    LIMIT $2
	  )
	  SELECT t.nspname, t.relname, t.relkind::text,
	         GREATEST(t.reltuples, 0)::bigint,
	         COALESCE(s.n_dead_tup, -1),
	         t.total,
	         COALESCE(pg_relation_size(t.oid), 0),
	         COALESCE(pg_indexes_size(t.oid), 0),
	         CASE WHEN t.reltoastrelid <> 0 THEN COALESCE(pg_total_relation_size(t.reltoastrelid), 0) ELSE 0 END,
	         COALESCE(s.seq_scan, -1), COALESCE(s.seq_tup_read, -1),
	         COALESCE(s.idx_scan, -1), COALESCE(s.idx_tup_fetch, -1),
	         COALESCE(s.n_tup_ins, -1), COALESCE(s.n_tup_upd, -1), COALESCE(s.n_tup_del, -1),
	         COALESCE(s.n_mod_since_analyze, -1),
	         s.last_vacuum, s.last_autovacuum, s.last_analyze, s.last_autoanalyze,
	         t.reltuples::float8, t.relpages::bigint,
	         COALESCE(w.width, 0)::float8, COALESCE(w.cols, 0),
	         COALESCE(substring(array_to_string(t.reloptions, ',') FROM 'fillfactor=([0-9]+)')::int, 100),
	         current_setting('block_size')::int
	  FROM t
	  LEFT JOIN pg_stat_all_tables s ON s.relid = t.oid
	  LEFT JOIN LATERAL (
	    SELECT sum((1 - st.null_frac) * st.avg_width) AS width, count(*) AS cols
	    FROM pg_stats st
	    WHERE st.schemaname = t.nspname AND st.tablename = t.relname AND NOT st.inherited
	  ) w ON true
	  ORDER BY t.total DESC, t.nspname, t.relname`, opts.Schema, opts.limit()+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &TableStatsReport{Schema: opts.Schema, Tables: []TableStat{}}
	for rows.Next() {
		var (
			t                                 TableStat
			kind                              string
			vac, autovac, an, autoan          sql.NullTime
			tuples, width                     float64
			pages                             int64
			statCols, fillfactor, blockSizeIn int
		)
		if err := rows.Scan(&t.Schema, &t.Table, &kind, &t.Rows, &t.DeadRows, &t.TotalBytes, &t.TableBytes,
			&t.IndexBytes, &t.ToastBytes, &t.SeqScans, &t.SeqRowsRead, &t.IndexScans, &t.IndexRowsRead,
			&t.Inserts, &t.Updates, &t.Deletes, &t.ModsSinceStats, &vac, &autovac, &an, &autoan,
			&tuples, &pages, &width, &statCols, &fillfactor, &blockSizeIn); err != nil {
			return nil, err
		}
		switch kind {
		case "m":
			t.Kind = "materialized view"
		case "p":
			t.Kind = "partitioned table"
		default:
			t.Kind = "table"
		}
		t.LastVacuum, t.LastAutovacuum = timePtr(vac), timePtr(autovac)
		t.LastAnalyze, t.LastAutoanalyze = timePtr(an), timePtr(autoan)
		t.BloatBytes = -1
		// Without column statistics there is no row width to estimate from,
		// and a table never analysed has neither rows nor widths to trust.
		if statCols > 0 && tuples >= 0 && kind != "p" {
			t.BloatBytes = estimateHeapBloat(tuples, pages, width, fillfactor, blockSizeIn)
		}
		out.Tables = append(out.Tables, t)
	}
	return out, rows.Err()
}

// estimateHeapBloat is the space a heap occupies beyond what its rows need.
//
// It is the arithmetic every bloat query does, without the sampling
// extension: rows times the width of a row — the 24-byte tuple header, its
// 4-byte line pointer and the average data width rounded up to the 8-byte
// alignment every tuple gets — against the usable part of a page at the
// table's fill factor. What the table holds beyond that many pages is dead
// space. It reads the planner's statistics, so it is as fresh as the last
// ANALYZE and no fresher, and it cannot see TOAST.
func estimateHeapBloat(rows float64, pages int64, avgWidth float64, fillfactor, blockSize int) int64 {
	if blockSize <= 24 || pages <= 0 {
		return 0
	}
	if fillfactor < 10 || fillfactor > 100 {
		fillfactor = 100
	}
	const pageHeader, tupleHeader, linePointer = 24, 24, 4
	tuple := tupleHeader + linePointer + math.Ceil(avgWidth/8)*8
	usable := float64(blockSize-pageHeader) * float64(fillfactor) / 100
	needed := int64(math.Ceil(rows * tuple / usable))
	if needed >= pages {
		return 0
	}
	return (pages - needed) * int64(blockSize)
}

// IndexStats reads every index with its size and scan count. The signature is
// what PostgreSQL itself would compare to decide two indexes are one: the key
// columns, operator classes, ordering options, collations, access method, and
// the expression and predicate texts. indnkeyatts (11+) keeps INCLUDE columns
// out of the column list, read through to_jsonb for the servers before it.
func (postgresDialect) IndexStats(ctx context.Context, db *sql.DB, opts StatsOptions) (*IndexStatsReport, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT n.nspname, t.relname, i.relname, am.amname,
	         x.indisunique, x.indisprimary, x.indisvalid,
	         EXISTS (SELECT 1 FROM pg_constraint con WHERE con.conindid = i.oid),
	         COALESCE(pg_relation_size(i.oid), 0),
	         COALESCE(s.idx_scan, -1), COALESCE(s.idx_tup_read, -1),
	         pg_get_indexdef(i.oid),
	         COALESCE((SELECT string_agg(pg_get_indexdef(i.oid, k, true), chr(31) ORDER BY k)
	                   FROM generate_series(1, COALESCE((to_jsonb(x) ->> 'indnkeyatts')::int, x.indnatts)) k), ''),
	         x.indkey::text || '|' || x.indclass::text || '|' || x.indoption::text || '|' || x.indcollation::text
	           || '|' || am.amname
	           || '|' || COALESCE(pg_get_expr(x.indexprs, x.indrelid), '')
	           || '|' || COALESCE(pg_get_expr(x.indpred, x.indrelid), ''),
	         x.indexprs IS NULL AND x.indpred IS NULL
	  FROM pg_index x
	  JOIN pg_class i ON i.oid = x.indexrelid
	  JOIN pg_class t ON t.oid = x.indrelid
	  JOIN pg_namespace n ON n.oid = t.relnamespace
	  JOIN pg_am am ON am.oid = i.relam
	  LEFT JOIN pg_stat_all_indexes s ON s.indexrelid = i.oid
	  WHERE t.relkind IN ('r', 'm', 'p') AND `+opsPgSchemaFilter+`
	    AND ($2 = '' OR t.relname = $2)
	  ORDER BY pg_relation_size(i.oid) DESC NULLS LAST, n.nspname, t.relname, i.relname
	  LIMIT $3`, opts.Schema, opts.Table, opts.limit()+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &IndexStatsReport{Schema: opts.Schema, Indexes: []IndexStat{}}
	for rows.Next() {
		var ix IndexStat
		var cols string
		if err := rows.Scan(&ix.Schema, &ix.Table, &ix.Name, &ix.Method, &ix.Unique, &ix.Primary, &ix.Valid,
			&ix.Constraint, &ix.Bytes, &ix.Scans, &ix.RowsRead, nullText{&ix.Definition}, &cols,
			&ix.signature, &ix.plain); err != nil {
			return nil, err
		}
		ix.Columns = splitUnit(cols)
		out.Indexes = append(out.Indexes, ix)
	}
	return out, rows.Err()
}

// --- maintenance -------------------------------------------------------------

func (postgresDialect) MaintenanceActions() []MaintenanceAction {
	return []MaintenanceAction{
		{ID: "vacuum", Label: "Vacuum", Scope: "either",
			Description: "Reclaims the space dead rows hold so the table can reuse it. Runs alongside reads and writes."},
		{ID: "vacuum_analyze", Label: "Vacuum and analyze", Scope: "either",
			Description: "Vacuums, then refreshes the planner's statistics in the same pass."},
		{ID: "analyze", Label: "Analyze", Scope: "either",
			Description: "Refreshes the statistics the planner chooses plans from. Reads a sample; blocks nothing."},
		{ID: "vacuum_full", Label: "Vacuum full", Scope: "either", Blocking: true,
			Description: "Rewrites the table into a new file and returns the freed space to the disk. Takes an exclusive lock: nothing can read or write the table until it finishes, and it needs free disk for the copy."},
		{ID: "reindex", Label: "Reindex", Scope: "either", Blocking: true, Options: []string{"concurrently"},
			Description: "Rebuilds indexes from the table. Blocks writes to the table while it runs, unless run concurrently, which is slower and blocks nothing."},
	}
}

// pgMaintenanceSQL renders the one statement an action is. Every identifier
// is quoted by the dialect and nothing else in the request reaches the text.
func pgMaintenanceSQL(d postgresDialect, req MaintenanceRequest, database string) (string, error) {
	target := ""
	if req.Table != "" {
		schema := req.Schema
		if schema == "" {
			schema = d.DefaultSchema()
		}
		rel, err := qualify(d, schema, req.Table)
		if err != nil {
			return "", err
		}
		target = " " + rel
	}
	if req.Index != "" && req.Action != "reindex" {
		return "", maintenanceRefused("only reindex takes an index")
	}
	if req.Table == "" && req.Schema != "" && req.Action != "reindex" {
		return "", maintenanceRefused("%s takes a table or the whole database, not a schema", req.Action)
	}
	switch req.Action {
	case "vacuum":
		return "VACUUM (VERBOSE)" + target, nil
	case "vacuum_analyze":
		return "VACUUM (VERBOSE, ANALYZE)" + target, nil
	case "vacuum_full":
		return "VACUUM (FULL, VERBOSE)" + target, nil
	case "analyze":
		return "ANALYZE VERBOSE" + target, nil
	case "reindex":
		concurrently := ""
		if req.Options.Concurrently {
			concurrently = " CONCURRENTLY"
		}
		switch {
		case req.Index != "":
			schema := req.Schema
			if schema == "" {
				schema = d.DefaultSchema()
			}
			ix, err := qualify(d, schema, req.Index)
			if err != nil {
				return "", err
			}
			return "REINDEX (VERBOSE) INDEX" + concurrently + " " + ix, nil
		case req.Table != "":
			return "REINDEX (VERBOSE) TABLE" + concurrently + target, nil
		case req.Schema != "":
			s, err := d.QuoteIdent(req.Schema)
			if err != nil {
				return "", err
			}
			return "REINDEX (VERBOSE) SCHEMA" + concurrently + " " + s, nil
		}
		// REINDEX DATABASE takes the name of the database the session is in
		// and no other; before 15 it could not be left out.
		name, err := d.QuoteIdent(database)
		if err != nil {
			return "", err
		}
		return "REINDEX (VERBOSE) DATABASE" + concurrently + " " + name, nil
	}
	return "", maintenanceRefused("unknown action %q", req.Action)
}

// Maintain runs the statement on a connection of its own.
//
// PostgreSQL reports a maintenance command through notices — the lines
// VACUUM VERBOSE prints in psql — and a notice goes to a handler fixed when
// the connection is opened. The pool's connections were opened without one,
// so the pool cannot hear them.
func (d postgresDialect) Maintain(ctx context.Context, db *sql.DB, dsn string, req MaintenanceRequest) (*MaintenanceResult, error) {
	var database string
	if err := db.QueryRowContext(ctx, `SELECT current_database()`).Scan(&database); err != nil {
		return nil, err
	}
	stmt, err := pgMaintenanceSQL(d, req, database)
	if err != nil {
		return nil, err
	}
	out := &MaintenanceResult{Statements: []string{stmt}, Output: []string{}, OK: true}
	if err := pgRunWithNotices(ctx, d.NormaliseDSN(dsn), stmt, out); err != nil {
		return nil, err
	}
	return out, nil
}

// pgRunWithNotices runs one statement on a connection opened for it and keeps
// the notices it raises.
//
// The statement ends with the context. The driver closes a connection whose
// context was cancelled mid-statement, and as it does it sends the server a
// cancel request — what psql sends on Ctrl-C — so a VACUUM FULL whose request
// was closed stops and gives up its lock rather than running on unwatched.
// TestLiveOpsPostgresMaintenanceStopsWithTheRequest holds the driver to that.
func pgRunWithNotices(ctx context.Context, dsn, stmt string, out *MaintenanceResult) error {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return err
	}
	cfg.OnNotice = func(_ *pgconn.PgConn, n *pgconn.Notice) {
		out.keep(noticeLines(n)...)
	}
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())
	// The simple protocol, one statement: VACUUM refuses to run inside the
	// implicit transaction a multi-statement or pipelined message opens.
	_, err = conn.PgConn().Exec(ctx, stmt).ReadAll()
	return err
}

// noticeLines renders one notice the way psql prints it: the severity on the
// first line, the detail beneath.
func noticeLines(n *pgconn.Notice) []string {
	lines := strings.Split(strings.TrimRight(n.Message, "\n"), "\n")
	lines[0] = n.Severity + ": " + lines[0]
	if n.Detail != "" {
		lines = append(lines, strings.Split(strings.TrimRight(n.Detail, "\n"), "\n")...)
	}
	return lines
}
