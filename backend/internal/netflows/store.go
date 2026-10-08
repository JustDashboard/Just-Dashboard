package netflows

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"regexp"
	"time"
)

const settingsKey = "network.flows.settings"
const maxRowBytes = 8192

var ErrInvalid = errors.New("invalid flow query or policy")
var ErrUnavailable = errors.New("socket accounting unavailable")
var fullID = regexp.MustCompile(`^[0-9a-f]{64}$`)

type persistedState struct {
	Settings Settings   `json:"settings"`
	Since    *time.Time `json:"since"`
	Pruned   int64      `json:"pruned"`
}
type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{db: db} }
func (s *Store) state(ctx context.Context) (persistedState, error) {
	st := persistedState{Settings: DefaultSettings}
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, settingsKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if json.Unmarshal([]byte(raw), &st) != nil || st.Settings.Validate() != nil {
		return st, fmt.Errorf("%w: stored recorder policy is unreadable", ErrUnavailable)
	}
	return st, nil
}
func saveState(ctx context.Context, tx *sql.Tx, st persistedState) error {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, settingsKey, string(data))
	return err
}
func add(a, b *uint64) *uint64 {
	if b == nil {
		return a
	}
	if a == nil {
		n := *b
		return &n
	}
	if *b > math.MaxInt64 || *a > math.MaxInt64-*b {
		return nil
	}
	n := *a + *b
	return &n
}
func maximum(a, b *uint64) *uint64 {
	if b == nil {
		return a
	}
	if a == nil || *b > *a {
		n := *b
		return &n
	}
	return a
}
func merge(a, b Bucket) Bucket {
	a.LastSeen, a.Socket.State = b.LastSeen, b.Socket.State
	a.Samples += b.Samples
	a.TxBytes, a.RxBytes, a.Retransmissions = add(a.TxBytes, b.TxBytes), add(a.RxBytes, b.RxBytes), add(a.Retransmissions, b.Retransmissions)
	a.LostGaugeMax = maximum(a.LostGaugeMax, b.LostGaugeMax)
	a.MeasuredIntervals += b.MeasuredIntervals
	a.TxIntervals += b.TxIntervals
	a.RxIntervals += b.RxIntervals
	a.RetransIntervals += b.RetransIntervals
	a.SkippedIntervals += b.SkippedIntervals
	return a
}
func (s *Store) record(ctx context.Context, st persistedState, c Cycle, buckets []Bucket) (persistedState, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return st, err
	}
	defer tx.Rollback()
	for _, b := range buckets {
		var oldRaw string
		err := tx.QueryRowContext(ctx, `SELECT substr(payload,1,8193) FROM network_flow_buckets WHERE id=? AND hour=?`, b.ID, b.Hour.Unix()).Scan(&oldRaw)
		if err == nil {
			var old Bucket
			if len(oldRaw) > maxRowBytes || json.Unmarshal([]byte(oldRaw), &old) != nil {
				return st, errors.New("unreadable saved socket observation")
			}
			b = merge(old, b)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return st, err
		}
		raw, err := json.Marshal(b)
		if err != nil || len(raw) > maxRowBytes {
			return st, errors.New("socket observation exceeds storage bound")
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO network_flow_buckets(id,hour,last_seen,remote_address,container_id,payload,payload_bytes) VALUES(?,?,?,?,?,?,?) ON CONFLICT(id,hour) DO UPDATE SET last_seen=excluded.last_seen,payload=excluded.payload,payload_bytes=excluded.payload_bytes`, b.ID, b.Hour.Unix(), b.LastSeen.UnixMilli(), b.Socket.RemoteAddress, b.Socket.Owner.ContainerID, string(raw), len(raw))
		if err != nil {
			return st, err
		}
	}
	hour := c.At.UTC().Truncate(time.Hour)
	cov := CoverageHour{Hour: hour, FirstSampleAt: c.At}
	var oldRaw string
	err = tx.QueryRowContext(ctx, `SELECT substr(payload,1,8193) FROM network_flow_cycles WHERE hour=?`, hour.Unix()).Scan(&oldRaw)
	if err == nil {
		if len(oldRaw) > maxRowBytes || json.Unmarshal([]byte(oldRaw), &cov) != nil {
			return st, errors.New("unreadable recorder coverage")
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return st, err
	}
	cov.LastSampleAt = c.FinishedAt
	cov.Samples++
	cov.OmittedSources += c.OmittedSources
	cov.DiscardedIntervals += c.DiscardedIntervals
	cov.CaptureMillis += c.ElapsedMillis
	cov.MaxCaptureMillis = max(cov.MaxCaptureMillis, c.ElapsedMillis)
	cov.LastCycle = c
	for _, src := range c.Sources {
		if src.Status == "unavailable" {
			cov.FailedSources++
		}
		if src.Truncated {
			cov.TruncatedSources++
		}
	}
	raw, err := json.Marshal(cov)
	if err != nil || len(raw) > maxRowBytes {
		return st, errors.New("coverage record exceeds storage bound")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO network_flow_cycles(hour,payload,payload_bytes) VALUES(?,?,?) ON CONFLICT(hour) DO UPDATE SET payload=excluded.payload,payload_bytes=excluded.payload_bytes`, hour.Unix(), string(raw), len(raw))
	if err != nil {
		return st, err
	}
	st, err = prune(ctx, tx, st, c.FinishedAt)
	if err != nil {
		return st, err
	}
	if err = saveState(ctx, tx, st); err != nil {
		return st, err
	}
	return st, tx.Commit()
}
func prune(ctx context.Context, tx *sql.Tx, st persistedState, now time.Time) (persistedState, error) {
	cutoff := now.UTC().Truncate(time.Hour).Add(-time.Duration(st.Settings.RetentionDays) * 24 * time.Hour).Unix()
	res, err := tx.ExecContext(ctx, `DELETE FROM network_flow_buckets WHERE hour<?`, cutoff)
	if err != nil {
		return st, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return st, err
	}
	st.Pruned += n
	if _, err = tx.ExecContext(ctx, `DELETE FROM network_flow_cycles WHERE hour<?`, cutoff); err != nil {
		return st, err
	}
	for {
		var count int
		var bytes int64
		err = tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(payload_bytes),0)+(SELECT COALESCE(SUM(payload_bytes),0) FROM network_flow_cycles) FROM network_flow_buckets`).Scan(&count, &bytes)
		if err != nil {
			return st, err
		}
		if count <= MaxRows && bytes <= MaxStoredBytes {
			break
		}
		if count == 0 {
			return st, errors.New("recorder coverage exceeds storage bound")
		}
		excess := min(max(count-MaxRows, 128), 1024)
		res, err = tx.ExecContext(ctx, `DELETE FROM network_flow_buckets WHERE rowid IN (SELECT rowid FROM network_flow_buckets ORDER BY hour,last_seen,id LIMIT ?)`, excess)
		if err != nil {
			return st, err
		}
		n, _ := res.RowsAffected()
		st.Pruned += n
	}
	return st, nil
}
func (s *Store) policy(ctx context.Context, st persistedState, now time.Time) (persistedState, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return st, err
	}
	defer tx.Rollback()
	st, err = prune(ctx, tx, st, now)
	if err != nil {
		return st, err
	}
	if err = saveState(ctx, tx, st); err != nil {
		return st, err
	}
	return st, tx.Commit()
}
func (s *Store) clear(ctx context.Context, st persistedState) (persistedState, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return st, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `DELETE FROM network_flow_buckets`)
	if err != nil {
		return st, err
	}
	n, _ := res.RowsAffected()
	st.Pruned += n
	if _, err = tx.ExecContext(ctx, `DELETE FROM network_flow_cycles`); err != nil {
		return st, err
	}
	if err = saveState(ctx, tx, st); err != nil {
		return st, err
	}
	return st, tx.Commit()
}
func ValidateQuery(q Query, now time.Time) (Query, error) {
	if q.From.IsZero() && q.To.IsZero() {
		q.To = now.UTC().Truncate(24 * time.Hour)
		q.From = q.To.Add(-24 * time.Hour)
	}
	if q.From.IsZero() || q.To.IsZero() || !q.From.Equal(q.From.UTC().Truncate(time.Hour)) || !q.To.Equal(q.To.UTC().Truncate(time.Hour)) || !q.To.After(q.From) || q.To.Sub(q.From) > 31*24*time.Hour {
		return q, fmt.Errorf("%w: choose UTC hour boundaries spanning at most 31 days", ErrInvalid)
	}
	if q.Address != "" {
		a, err := netip.ParseAddr(q.Address)
		if err != nil || a.Zone() != "" {
			return q, fmt.Errorf("%w: address must be a literal IP without a scope", ErrInvalid)
		}
		q.Address = a.Unmap().String()
	}
	if q.ContainerID != "" && !fullID.MatchString(q.ContainerID) {
		return q, fmt.Errorf("%w: container identity must be a full Docker ID", ErrInvalid)
	}
	q.From, q.To = q.From.UTC(), q.To.UTC()
	if q.Limit == 0 {
		q.Limit = 200
	}
	if q.Limit < 1 || q.Limit > MaxExportRows {
		return q, fmt.Errorf("%w: row limit is 1 to %d", ErrInvalid, MaxExportRows)
	}
	return q, nil
}
func (s *Store) read(ctx context.Context, q Query) (Report, error) {
	r := Report{From: q.From, To: q.To, Rows: []Bucket{}, CoverageHours: []CoverageHour{}}
	var first sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(payload_bytes),0)+(SELECT COALESCE(SUM(payload_bytes),0) FROM network_flow_cycles),MIN(hour) FROM network_flow_buckets`).Scan(&r.RetainedRows, &r.RetainedBytes, &first)
	if err != nil {
		return r, err
	}
	if first.Valid {
		at := time.Unix(first.Int64, 0).UTC()
		r.RetainedFrom = &at
	}
	rows, err := s.db.QueryContext(ctx, `SELECT substr(payload,1,8193) FROM network_flow_buckets WHERE hour>=? AND hour<? AND (?='' OR remote_address=?) AND (?='' OR container_id=?) ORDER BY hour DESC,last_seen DESC,id LIMIT ?`, q.From.Unix(), q.To.Unix(), q.Address, q.Address, q.ContainerID, q.ContainerID, q.Limit+1)
	if err != nil {
		return r, err
	}
	for rows.Next() {
		var raw string
		var b Bucket
		if err = rows.Scan(&raw); err != nil {
			break
		}
		if len(raw) > maxRowBytes || json.Unmarshal([]byte(raw), &b) != nil {
			err = errors.New("saved socket observation is unreadable")
			break
		}
		r.Rows = append(r.Rows, b)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return r, err
	}
	if len(r.Rows) > q.Limit {
		r.Truncated = true
		r.Rows = r.Rows[:q.Limit]
	}
	rows, err = s.db.QueryContext(ctx, `SELECT substr(payload,1,8193) FROM network_flow_cycles WHERE hour>=? AND hour<? ORDER BY hour LIMIT 745`, q.From.Unix(), q.To.Unix())
	if err != nil {
		return r, err
	}
	for rows.Next() {
		var raw string
		var c CoverageHour
		if err = rows.Scan(&raw); err != nil {
			break
		}
		if len(raw) > maxRowBytes || json.Unmarshal([]byte(raw), &c) != nil {
			err = errors.New("recorder coverage is unreadable")
			break
		}
		r.CoverageHours = append(r.CoverageHours, c)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return r, err
	}
	var raw string
	err = s.db.QueryRowContext(ctx, `SELECT substr(payload,1,8193) FROM network_flow_cycles ORDER BY hour DESC LIMIT 1`).Scan(&raw)
	if err == nil {
		var c CoverageHour
		if len(raw) > maxRowBytes || json.Unmarshal([]byte(raw), &c) != nil {
			return r, errors.New("latest recorder coverage is unreadable")
		}
		r.LastCycle = &c.LastCycle
	} else if !errors.Is(err, sql.ErrNoRows) {
		return r, err
	}
	return r, nil
}
