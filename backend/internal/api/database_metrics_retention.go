package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// Samples are kept whole for a week. Older ones are thinned to the last
// reading of every five minutes: the history derives rates from differences of
// stored totals, so the newest total in a step stands in for the step, where
// an average would corrupt every counter it was taken over.
const (
	databaseMetricRawWindow   = 7 * 24 * time.Hour
	databaseMetricCoarseEvery = 5 * time.Minute
	databaseMetricThinEvery   = time.Hour

	// A saved row also holds its 64-character identity twice, once in the
	// table and once in the primary key's index, beside two integers.
	databaseMetricRowOverhead = 200

	databaseMetricRetentionKey = "databases.metrics.retention_days"
	// databaseMetricsPath is under /api/v1; the endpoint the page shows is the
	// full path.
	databaseMetricsPath     = "/metrics/databases"
	databaseMetricsEndpoint = "/api/v1" + databaseMetricsPath
)

// The windows an operator may choose, in days. A free number would make the
// storage estimate a guess about a value nobody has tried.
var databaseMetricRetentionOptions = []int{7, 14, 30, 90}

// databaseMetricRetentionDays is the saved retention, or a week where none was
// ever chosen. A read that fails is an error, never the default: pruning to a
// week on the strength of a failed read would delete what the operator kept.
func (s *Server) databaseMetricRetentionDays(ctx context.Context) (int, error) {
	value, ok, err := s.Store.Setting(ctx, databaseMetricRetentionKey)
	if err != nil {
		return 0, err
	}
	if !ok {
		return databaseMetricRetentionOptions[0], nil
	}
	days, err := strconv.Atoi(value)
	if err != nil || !slices.Contains(databaseMetricRetentionOptions, days) {
		return 0, unreadableRetention(value)
	}
	return days, nil
}

// unreadableRetention is a saved value that is not one of the windows. The
// recorder will not prune on it, and the page treats it as not chosen yet, so
// choosing a window repairs it.
type unreadableRetention string

func (e unreadableRetention) Error() string {
	return fmt.Sprintf("saved retention %q is not one of %v", string(e), databaseMetricRetentionOptions)
}

// pruneDatabaseMetrics drops what is past the retention and thins what is past
// the raw window. Neither runs when the retention cannot be read.
func (s *Server) pruneDatabaseMetrics(ctx context.Context) {
	days, err := s.databaseMetricRetentionDays(ctx)
	if err != nil {
		s.Log.Warn("database activity retention could not be read, nothing was pruned", "err", err)
		return
	}
	if _, err := s.Store.DB.ExecContext(ctx, `DELETE FROM db_metric_samples WHERE at < ?`,
		time.Now().Add(-time.Duration(days)*24*time.Hour).UnixMilli()); err != nil {
		s.Log.Warn("database activity history could not be pruned", "err", err)
		return
	}
	if days > 7 && time.Since(s.dbMetricThinnedAt) >= databaseMetricThinEvery {
		s.thinDatabaseMetrics(ctx)
	}
}

// thinDatabaseMetrics keeps the newest sample of each five-minute step older
// than the raw window. It only looks at what has aged past the window since it
// last ran, one day at a time, so no statement holds the store for long and a
// slow pass does not repeat the whole history; the first pass after a start
// has the backlog to do. An attempt counts as one whether or not it finished,
// so a failure is tried again in an hour and not at the next sample.
func (s *Server) thinDatabaseMetrics(ctx context.Context) {
	s.dbMetricThinnedAt = time.Now()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	step := databaseMetricCoarseEvery.Milliseconds()
	chunk := 288 * step
	cutoff := time.Now().Add(-databaseMetricRawWindow).UnixMilli() / step * step
	from := s.dbMetricThinnedTo
	if from > 0 {
		from -= step
	} else if err := s.Store.DB.QueryRowContext(ctx, `SELECT COALESCE(MIN(at), 0) FROM db_metric_samples`).Scan(&from); err != nil {
		s.Log.Warn("database activity history could not be thinned", "err", err)
		return
	}
	from = from / step * step
	for lo := from; lo < cutoff; lo += chunk {
		// The range on b.at keeps each lookup inside one step of the primary
		// key instead of walking the connection's whole history per row.
		_, err := s.Store.DB.ExecContext(ctx, `DELETE FROM db_metric_samples WHERE rowid IN (
			SELECT o.rowid FROM db_metric_samples o WHERE o.at >= ? AND o.at < ? AND o.at < (
				SELECT MAX(b.at) FROM db_metric_samples b
				WHERE b.connection_id=o.connection_id AND b.identity=o.identity
				AND b.at >= o.at / ? * ? AND b.at < (o.at / ? + 1) * ?))`,
			lo, min(lo+chunk, cutoff), step, step, step, step)
		if err != nil {
			s.Log.Warn("database activity history could not be thinned", "err", err)
			return
		}
	}
	s.dbMetricThinnedTo = cutoff
}

// databaseMetricSamplesIn is how many samples a window of this many days holds
// for one connection: whole ones for the raw window, thinned beyond it.
func databaseMetricSamplesIn(days int) int64 {
	raw := min(time.Duration(days)*24*time.Hour, databaseMetricRawWindow)
	coarse := time.Duration(days)*24*time.Hour - raw
	return int64(raw/databaseMetricEvery) + int64(coarse/databaseMetricCoarseEvery)
}

type databaseMetricsRetentionOption struct {
	Days int `json:"days"`
	// EstimatedBytes is nil until the recorder has saved something to measure.
	EstimatedBytes *int64 `json:"estimatedBytes"`
}

type databaseMetricsExport struct {
	// RetentionDays is 0 when the saved value is not one of the windows.
	RetentionDays int                              `json:"retentionDays"`
	Options       []databaseMetricsRetentionOption `json:"options"`
	// Connections is how many servers the recorder has saved a reading of in
	// the last day, the multiplier of every estimate.
	Connections int   `json:"connections"`
	Samples     int64 `json:"samples"`
	// StoredBytes is the history's current size, estimated from the average
	// row: summing every snapshot's length would read the whole table.
	StoredBytes   *int64 `json:"storedBytes"`
	EverySeconds  int    `json:"everySeconds"`
	RawDays       int    `json:"rawDays"`
	CoarseMinutes int    `json:"coarseMinutes"`
	Endpoint      string `json:"endpoint"`
}

func (s *Server) databaseMetricsExportReport(ctx context.Context) (*databaseMetricsExport, error) {
	days, err := s.databaseMetricRetentionDays(ctx)
	var bad unreadableRetention
	if err != nil && !errors.As(err, &bad) {
		return nil, err
	}
	report := &databaseMetricsExport{
		RetentionDays: days,
		Options:       []databaseMetricsRetentionOption{},
		EverySeconds:  int(databaseMetricEvery.Seconds()),
		RawDays:       int(databaseMetricRawWindow / (24 * time.Hour)),
		CoarseMinutes: int(databaseMetricCoarseEvery / time.Minute),
		Endpoint:      databaseMetricsEndpoint,
	}
	if err := s.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM db_metric_samples`).Scan(&report.Samples); err != nil {
		return nil, err
	}
	if err := s.Store.DB.QueryRowContext(ctx, `SELECT COUNT(DISTINCT connection_id) FROM db_metric_samples WHERE at >= ?`,
		time.Now().Add(-24*time.Hour).UnixMilli()).Scan(&report.Connections); err != nil {
		return nil, err
	}
	var average float64
	if err := s.Store.DB.QueryRowContext(ctx, `SELECT COALESCE(AVG(LENGTH(snapshot)), 0) FROM (
		SELECT snapshot FROM db_metric_samples ORDER BY at DESC LIMIT 600)`).Scan(&average); err != nil {
		return nil, err
	}
	row := average + databaseMetricRowOverhead
	if report.Samples > 0 {
		stored := int64(row * float64(report.Samples))
		report.StoredBytes = &stored
	}
	for _, option := range databaseMetricRetentionOptions {
		entry := databaseMetricsRetentionOption{Days: option}
		if report.Connections > 0 && average > 0 {
			estimate := int64(row * float64(databaseMetricSamplesIn(option)) * float64(report.Connections))
			entry.EstimatedBytes = &estimate
		}
		report.Options = append(report.Options, entry)
	}
	return report, nil
}

func (s *Server) handleDBMetricsExportGet(w http.ResponseWriter, r *http.Request) error {
	report, err := s.databaseMetricsExportReport(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, report)
	return nil
}

// handleDBMetricsExportPut chooses how long the history is kept. A shorter
// window deletes what falls outside it at the recorder's next pass, which is
// why the route sits behind the destructive capability and its budget.
func (s *Server) handleDBMetricsExportPut(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		RetentionDays int `json:"retentionDays"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if !slices.Contains(databaseMetricRetentionOptions, req.RetentionDays) {
		return httpx.BadRequest("retentionDays must be one of %v", databaseMetricRetentionOptions)
	}
	previous, err := s.databaseMetricRetentionDays(r.Context())
	detail := map[string]any{"days": req.RetentionDays, "previousDays": previous}
	var bad unreadableRetention
	if errors.As(err, &bad) {
		detail["previousValue"] = string(bad)
	} else if err != nil {
		return httpx.Internal(err)
	}
	if err := s.Store.SetSetting(r.Context(), databaseMetricRetentionKey, strconv.Itoa(req.RetentionDays)); err != nil {
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "database.metrics.retention", "database metrics", detail)
	report, err := s.databaseMetricsExportReport(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, report)
	return nil
}
