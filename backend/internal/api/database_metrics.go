package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

const databaseMetricEvery = 30 * time.Second

// The same bounded statistics read serves live requests and the recorder.
// It reads engine statistics views only, never application tables or keys.
func (s *Server) readDatabaseStats(ctx context.Context, conn *dbConnection, dsn string) (any, error) {
	switch conn.Driver {
	case dbx.DriverMongo:
		client, err := dbx.MongoClient(ctx, dsn)
		if err != nil {
			return nil, connectFailed(dsn, err)
		}
		defer client.Disconnect(context.Background())
		status, err := dbx.MongoServerStatus(ctx, client)
		if err != nil {
			return nil, queryFailed(err)
		}
		return map[string]any{"server": status}, nil
	case dbx.DriverRedis:
		client, err := dbx.RedisOpen(ctx, dsn, dbx.RedisOpenOptions{DB: 0, RespectContext: true})
		if err != nil {
			return nil, connectFailed(dsn, err)
		}
		defer client.Close()
		info, err := dbx.RedisInfo(ctx, client)
		if err != nil {
			return nil, queryFailed(err)
		}
		return map[string]any{"server": info}, nil
	default:
		pool, err := s.modules.dbs.Pool(ctx, conn.ID, conn.Driver, dsn)
		if err != nil {
			return nil, connectFailed(dsn, err)
		}
		stats, err := dbx.ReadServerStats(ctx, pool, conn.Driver)
		if err != nil {
			stats = &dbx.ServerStats{
				At: time.Now().UTC(), Driver: conn.Driver, Reason: err.Error(),
				Counters: map[string]float64{}, Gauges: map[string]float64{},
			}
		}
		return dbStatsResponse{ServerStats: stats, Pool: s.modules.dbs.Stats(conn.ID)}, nil
	}
}

func databaseMetricIdentity(rec *dbConnRecord) string {
	sum := sha256.Sum256([]byte(string(rec.conn.Driver) + "\x00" + rec.dsnEnc))
	return hex.EncodeToString(sum[:])
}

func (s *Server) startDatabaseMetrics(ctx context.Context) {
	ctx, s.dbMetricsStop = context.WithCancel(ctx)
	s.dbMetricsDone = make(chan struct{})
	go func() {
		defer close(s.dbMetricsDone)
		timer := time.NewTicker(databaseMetricEvery)
		defer timer.Stop()
		for {
			s.recordDatabaseMetrics(ctx)
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
		}
	}()
	s.startStatementHistory(ctx)
}

func (s *Server) stopDatabaseMetrics() {
	if s.dbMetricsStop != nil {
		s.dbMetricsStop()
		<-s.dbMetricsDone
		<-s.dbStatements.done
	}
}

// recordedDatabaseConns are the saved connections the recorder reads. SQLite is
// a file, with no activity counters or sessions to record.
func (s *Server) recordedDatabaseConns(ctx context.Context) ([]*dbConnRecord, error) {
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT `+dbConnColumns+` FROM db_connections ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []*dbConnRecord
	for rows.Next() {
		rec, err := scanDBConn(rows.Scan)
		if err != nil {
			return nil, err
		}
		if rec.conn.Driver != dbx.DriverSQLite {
			records = append(records, rec)
		}
	}
	return records, rows.Err()
}

func (s *Server) recordDatabaseMetrics(ctx context.Context) {
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	// The alerts are judged on this pass's readings, so what they need beyond
	// the snapshot is read in it: rules are read first to know what that is.
	alerts := s.modules.dbAlerts
	rules, err := alerts.Rules(ctx, nil, true)
	if err != nil {
		s.Log.Warn("database alert rules could not be read", "err", err)
	}
	records, err := s.recordedDatabaseConns(ctx)
	if err != nil {
		s.Log.Warn("database activity connections could not be read", "err", err)
		return
	}
	if len(records) > 0 {
		start := s.dbMetricOffset % len(records)
		records = append(records[start:], records[:start]...)
		s.dbMetricOffset = (start + 4) % len(records)
	}
	// Rotate the queue so a deadline reached by many offline servers cannot
	// leave the connections at the end unrecorded on every cycle.
	// Close the store cursor before dialing. Four workers bound connection
	// pressure, and one slow/offline server cannot hold up the other three.
	jobs := make(chan *dbConnRecord)
	var workers sync.WaitGroup
	var observedMu sync.Mutex
	observed := map[int64]dbAlertObservation{}
	for range 4 {
		workers.Go(func() {
			for rec := range jobs {
				obs := s.recordDatabaseMetric(ctx, rec, rules)
				observedMu.Lock()
				observed[rec.conn.ID] = obs
				observedMu.Unlock()
			}
		})
	}
send:
	for _, rec := range records {
		select {
		case jobs <- rec:
		case <-ctx.Done():
			break send
		}
	}
	close(jobs)
	workers.Wait()
	// Pruning has its own deadline so an unavailable engine cannot prevent it.
	pruneCtx, pruneCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer pruneCancel()
	s.pruneDatabaseMetrics(pruneCtx)
	if len(rules) == 0 {
		return
	}
	conns := make([]dbAlertConn, 0, len(records))
	for _, rec := range records {
		conns = append(conns, dbAlertConn{ID: rec.conn.ID, Name: rec.conn.Name, Driver: rec.conn.Driver})
	}
	// Its own deadline: the collection may have spent the pass's, and a
	// verdict that was reached is worth telling.
	judge, judged := context.WithTimeout(context.WithoutCancel(parent), 20*time.Second)
	defer judged()
	alerts.Evaluate(judge, conns, observed)
}

// recordDatabaseMetric keeps one server's snapshot, and hands back what the
// alerts make of the same read; `rules` are the enabled alert rules, which
// decide what is read beyond the snapshot.
func (s *Server) recordDatabaseMetric(ctx context.Context, rec *dbConnRecord, rules []dbAlertRule) dbAlertObservation {
	pass := ctx
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	conn, dsn, err := s.openDBConn(rec)
	if err != nil {
		return dbAlertObservation{
			Conn:   dbAlertConn{ID: rec.conn.ID, Name: rec.conn.Name, Driver: rec.conn.Driver},
			Unread: "The saved connection to " + rec.conn.Name + " cannot be opened by this dashboard, so its server is not read.",
		}
	}
	snapshot, err := s.readDatabaseStats(ctx, conn, dsn)
	var obs dbAlertObservation
	if len(rules) > 0 {
		obs = s.observeDatabase(ctx, conn, dsn, snapshot, err, rules)
	}
	if pass.Err() != nil {
		// The pass ended under the read: what it failed to hear says
		// nothing about the server.
		obs.Unread = "The last pass ran out of time before " + conn.Name + " was read."
	}
	if err != nil {
		return obs
	}
	if sql, ok := snapshot.(dbStatsResponse); ok && !sql.Supported {
		return obs
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return obs
	}
	// A connection edited or forgotten while the read ran cannot acquire a
	// sample of its previous target. Credentials never enter the history.
	_, err = s.Store.DB.ExecContext(ctx, `INSERT INTO db_metric_samples(connection_id, identity, at, snapshot)
		SELECT id, ?, ?, ? FROM db_connections WHERE id=? AND driver=? AND dsn_enc=?`,
		databaseMetricIdentity(rec), time.Now().UnixMilli(), string(data), conn.ID, conn.Driver, rec.dsnEnc)
	if err != nil && ctx.Err() == nil {
		s.Log.Warn("database activity sample could not be saved", "connection", conn.ID, "err", err)
	}
	return obs
}

type databaseMetricSample struct {
	At    int64           `json:"at"`
	Stats json.RawMessage `json:"stats"`
	Gap   bool            `json:"gap"`
}

func (s *Server) handleDBStatsHistory(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	// Reading retained history needs no dial, including when the server is down.
	rec, err := scanDBConn(s.Store.DB.QueryRowContext(r.Context(),
		`SELECT `+dbConnColumns+` FROM db_connections WHERE id=?`, id).Scan)
	if err == sql.ErrNoRows {
		return httpx.ErrNotFound
	}
	if err != nil {
		return httpx.Internal(err)
	}
	retentionDays, err := s.databaseMetricRetentionDays(r.Context())
	var bad unreadableRetention
	if errors.As(err, &bad) {
		// Reading deletes nothing, so a week stands in until a window is chosen.
		retentionDays, err = databaseMetricRetentionOptions[0], nil
	}
	if err != nil {
		return httpx.Internal(err)
	}
	retentionHours := retentionDays * 24
	hours := 1
	if raw := r.URL.Query().Get("hours"); raw != "" {
		hours, err = strconv.Atoi(raw)
		maxHours := databaseMetricRetentionOptions[len(databaseMetricRetentionOptions)-1] * 24
		if err != nil || hours < 1 || hours > maxHours {
			return httpx.BadRequest("hours must be between 1 and %d", maxHours)
		}
		// A range chosen while a longer retention was set is still a range the
		// reader may have saved: it answers with what is kept, and says how
		// much that is in retentionHours, rather than failing every poll.
		hours = min(hours, retentionHours)
	}
	window := time.Duration(hours) * time.Hour
	bucket := max(databaseMetricEvery.Milliseconds(), window.Milliseconds()/720)
	// Keep the newest raw totals per bucket so rates remain differences of
	// totals. Carry missed intervals through bucketing instead of drawing
	// an outage as continuous activity. Samples past the raw window are one
	// per coarse step, so a gap there is measured in steps, not in 30 seconds.
	// The winners are picked on timestamps alone: a 90-day window would
	// otherwise carry every snapshot through the window functions.
	now := time.Now()
	rows, err := s.Store.DB.QueryContext(r.Context(), `WITH intervals AS (
		SELECT at, at - LAG(at) OVER (ORDER BY at) > CASE WHEN LAG(at) OVER (ORDER BY at) < ? THEN ? ELSE ? END AS gap
		FROM db_metric_samples WHERE connection_id=? AND identity=? AND at>=?
	), buckets AS (
		SELECT at, MAX(COALESCE(gap, 0)) OVER (PARTITION BY at / ?) AS gap,
		ROW_NUMBER() OVER (PARTITION BY at / ? ORDER BY at DESC) AS position FROM intervals
	) SELECT b.at, s.snapshot, b.gap FROM buckets b
	JOIN db_metric_samples s ON s.connection_id=? AND s.identity=? AND s.at=b.at
	WHERE b.position=1 ORDER BY b.at`,
		now.Add(-databaseMetricRawWindow).UnixMilli(), 3*databaseMetricCoarseEvery.Milliseconds(), 3*databaseMetricEvery.Milliseconds(),
		id, databaseMetricIdentity(rec), now.Add(-window).UnixMilli(), bucket, bucket,
		id, databaseMetricIdentity(rec))
	if err != nil {
		return httpx.Internal(err)
	}
	defer rows.Close()
	samples := []databaseMetricSample{}
	for rows.Next() {
		var sample databaseMetricSample
		var data string
		if err := rows.Scan(&sample.At, &data, &sample.Gap); err != nil {
			return httpx.Internal(err)
		}
		sample.Stats = json.RawMessage(data)
		samples = append(samples, sample)
	}
	if err := rows.Err(); err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"samples": samples, "everySeconds": int(databaseMetricEvery.Seconds()), "retentionHours": retentionHours,
	})
	return nil
}
