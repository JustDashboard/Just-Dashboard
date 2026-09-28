package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
)

// Stored TLS reports and the watch list's own schedule. Reading either sends
// nothing anywhere, so every signed-in account may; what sends a handshake
// stays behind system.admin in mountTLSRoutes.

// Retention: a report is tens of kilobytes, so its history is bounded by
// count as well as age, and a watched endpoint checked every five minutes
// makes 288 checks a day.
const (
	scansPerTarget     = 100
	scanRetention      = 180 * 24 * time.Hour
	checksPerEndpoint  = 2000
	checkRetention     = 90 * 24 * time.Hour
	watchIntervalKey   = "certificates.watch.interval"
	storedHistoryLimit = 500
)

// storeScan keeps a finished report and trims its target's history.
func (s *Server) storeScan(ctx context.Context, scan *proxysvc.TLSScan) (int64, error) {
	report, err := json.Marshal(scan)
	if err != nil {
		return 0, err
	}
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`INSERT INTO tls_scans(domain, port, grade, days_left, fingerprint, reachable, checked_at, report)
		 VALUES(?,?,?,?,?,?,?,?)`,
		scan.Domain, scan.Port, scan.Grade, daysLeft(scan.Certificate), scan.Fingerprint,
		scan.Reachable, scan.CheckedAt.Unix(), string(report))
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM tls_scans WHERE domain = ? AND port = ? AND (checked_at < ? OR id NOT IN (
		   SELECT id FROM tls_scans WHERE domain = ? AND port = ? ORDER BY checked_at DESC, id DESC LIMIT ?))`,
		scan.Domain, scan.Port, time.Now().Add(-scanRetention).Unix(),
		scan.Domain, scan.Port, scansPerTarget); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// daysLeft is NULL for a scan or check that read no certificate, so the
// sparkline shows a gap there rather than a plunge to zero.
func daysLeft(cert *proxysvc.Certificate) sql.NullInt64 {
	if cert == nil || cert.NotAfter.IsZero() {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(cert.DaysLeft), Valid: true}
}

type scanSummary struct {
	ID          int64     `json:"id"`
	Grade       string    `json:"grade"`
	DaysLeft    *int64    `json:"daysLeft,omitempty"`
	Fingerprint string    `json:"fingerprint,omitempty"`
	Reachable   bool      `json:"reachable"`
	CheckedAt   time.Time `json:"checkedAt"`
}

// handleTLSScans lists a target's stored reports, newest first.
func (s *Server) handleTLSScans(w http.ResponseWriter, r *http.Request) error {
	target, err := scanTarget(r)
	if err != nil {
		return err
	}
	rows, err := s.Store.DB.QueryContext(r.Context(),
		`SELECT id, grade, days_left, fingerprint, reachable, checked_at FROM tls_scans
		  WHERE domain = ? AND port = ? ORDER BY checked_at DESC, id DESC`,
		target.Host, target.Port)
	if err != nil {
		return httpx.Internal(err)
	}
	defer rows.Close()
	scans := []scanSummary{}
	for rows.Next() {
		var sc scanSummary
		var days sql.NullInt64
		var at int64
		if err := rows.Scan(&sc.ID, &sc.Grade, &days, &sc.Fingerprint, &sc.Reachable, &at); err != nil {
			return httpx.Internal(err)
		}
		if days.Valid {
			sc.DaysLeft = &days.Int64
		}
		sc.CheckedAt = time.Unix(at, 0).UTC()
		scans = append(scans, sc)
	}
	if err := rows.Err(); err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, scans)
	return nil
}

type storedScan struct {
	ID     int64             `json:"id"`
	Report *proxysvc.TLSScan `json:"report"`
	// PreviousID is the target's report before this one, and Changes what
	// differs from it; both are absent for a target's first report.
	PreviousID *int64                `json:"previousId,omitempty"`
	Changes    []proxysvc.ScanChange `json:"changes"`
}

// handleTLSStoredScan is one stored report and what changed since the one
// before it.
func (s *Server) handleTLSStoredScan(w http.ResponseWriter, r *http.Request) error {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		return httpx.BadRequest("invalid id")
	}
	var domain, report string
	var port int
	var at int64
	if err := s.Store.DB.QueryRowContext(r.Context(),
		`SELECT domain, port, checked_at, report FROM tls_scans WHERE id = ?`, id).
		Scan(&domain, &port, &at, &report); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return httpx.ErrNotFound
		}
		return httpx.Internal(err)
	}
	out := storedScan{ID: id, Changes: []proxysvc.ScanChange{}}
	if err := json.Unmarshal([]byte(report), &out.Report); err != nil {
		return httpx.Internal(err)
	}
	var prevID int64
	var prevReport string
	err = s.Store.DB.QueryRowContext(r.Context(),
		`SELECT id, report FROM tls_scans WHERE domain = ? AND port = ?
		   AND (checked_at < ? OR (checked_at = ? AND id < ?))
		 ORDER BY checked_at DESC, id DESC LIMIT 1`,
		domain, port, at, at, id).Scan(&prevID, &prevReport)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return httpx.Internal(err)
	default:
		var prev proxysvc.TLSScan
		if err := json.Unmarshal([]byte(prevReport), &prev); err != nil {
			return httpx.Internal(err)
		}
		out.PreviousID = &prevID
		out.Changes = proxysvc.DiffScans(&prev, out.Report)
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// handleClearTLSScans forgets one target's stored reports.
func (s *Server) handleClearTLSScans(w http.ResponseWriter, r *http.Request) error {
	target, err := scanTarget(r)
	if err != nil {
		return err
	}
	res, err := s.Store.DB.ExecContext(r.Context(),
		`DELETE FROM tls_scans WHERE domain = ? AND port = ?`, target.Host, target.Port)
	if err != nil {
		return httpx.Internal(err)
	}
	removed, _ := res.RowsAffected()
	httpx.SetAudit(r, "certificates.scans.clear", target.Host, map[string]any{"port": target.Port, "removed": removed})
	httpx.NoContent(w)
	return nil
}

// handleCheckWatchedNow checks every watched endpoint now instead of when the
// schedule next reaches it, under the 30-second budget the page waits for,
// and answers with the list as it then stands.
func (s *Server) handleCheckWatchedNow(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	// A budget spent waiting for a scheduled pass still answers with the
	// list, which that pass has just brought up to date.
	if _, err := s.modules.proxyExtras.tlsMonitor.CheckAll(ctx); err != nil && ctx.Err() == nil {
		return httpx.Internal(err)
	}
	readCtx := r.Context()
	if readCtx.Err() != nil {
		// The checks used the caller's last second. Finish the bounded read
		// from the stored results so the response still describes what ran.
		var done context.CancelFunc
		readCtx, done = context.WithTimeout(context.WithoutCancel(r.Context()), time.Second)
		defer done()
	}
	domains, err := s.watchedEndpoints(readCtx)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "certificates.watch.check", "", map[string]any{"endpoints": len(domains)})
	httpx.JSON(w, http.StatusOK, domains)
	return nil
}

type watchedCheck struct {
	CheckedAt   time.Time `json:"checkedAt"`
	DaysLeft    *int64    `json:"daysLeft,omitempty"`
	Fingerprint string    `json:"fingerprint,omitempty"`
	Error       string    `json:"error,omitempty"`
}

// handleWatchedHistory is one endpoint's recent checks, oldest first.
func (s *Server) handleWatchedHistory(w http.ResponseWriter, r *http.Request) error {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		return httpx.BadRequest("invalid id")
	}
	var exists int
	if err := s.Store.DB.QueryRowContext(r.Context(),
		`SELECT 1 FROM watched_endpoints WHERE id = ?`, id).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return httpx.ErrNotFound
		}
		return httpx.Internal(err)
	}
	rows, err := s.Store.DB.QueryContext(r.Context(),
		`SELECT checked_at, days_left, fingerprint, error FROM (
		   SELECT id, checked_at, days_left, fingerprint, error FROM watched_checks
		    WHERE endpoint_id = ? ORDER BY checked_at DESC, id DESC LIMIT ?)
		 ORDER BY checked_at, id`, id, storedHistoryLimit)
	if err != nil {
		return httpx.Internal(err)
	}
	defer rows.Close()
	checks := []watchedCheck{}
	for rows.Next() {
		var c watchedCheck
		var at int64
		var days sql.NullInt64
		if err := rows.Scan(&at, &days, &c.Fingerprint, &c.Error); err != nil {
			return httpx.Internal(err)
		}
		c.CheckedAt = time.Unix(at, 0).UTC()
		if days.Valid {
			c.DaysLeft = &days.Int64
		}
		checks = append(checks, c)
	}
	if err := rows.Err(); err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, checks)
	return nil
}

type watchSettings struct {
	IntervalSeconds int `json:"intervalSeconds"`
}

func (s *Server) handleWatchSettings(w http.ResponseWriter, r *http.Request) error {
	interval, err := watchStore{s}.WatchInterval(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, watchSettings{IntervalSeconds: int(interval / time.Second)})
	return nil
}

func (s *Server) handleSetWatchSettings(w http.ResponseWriter, r *http.Request) error {
	var req watchSettings
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	interval := time.Duration(req.IntervalSeconds) * time.Second
	if interval < proxysvc.MinWatchInterval || interval > proxysvc.MaxWatchInterval {
		return httpx.BadRequest("the interval must be between %d and %d seconds",
			int(proxysvc.MinWatchInterval/time.Second), int(proxysvc.MaxWatchInterval/time.Second))
	}
	if err := s.Store.SetSetting(r.Context(), watchIntervalKey, strconv.Itoa(req.IntervalSeconds)); err != nil {
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "certificates.watch.interval", "", req)
	httpx.JSON(w, http.StatusOK, req)
	return nil
}

// watchStore is the monitor's view of the watch list in SQLite.
type watchStore struct{ s *Server }

func (ws watchStore) WatchInterval(ctx context.Context) (time.Duration, error) {
	raw, ok, err := ws.s.Store.Setting(ctx, watchIntervalKey)
	if err != nil || !ok {
		return proxysvc.DefaultWatchInterval, err
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil {
		return proxysvc.DefaultWatchInterval, nil
	}
	return min(max(time.Duration(seconds)*time.Second, proxysvc.MinWatchInterval), proxysvc.MaxWatchInterval), nil
}

func (ws watchStore) WatchedEndpoints(ctx context.Context) ([]proxysvc.WatchedEndpoint, error) {
	rows, err := ws.s.Store.DB.QueryContext(ctx,
		`SELECT id, domain, port, ip, checked_at FROM watched_endpoints`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var endpoints []proxysvc.WatchedEndpoint
	for rows.Next() {
		var e proxysvc.WatchedEndpoint
		var at int64
		if err := rows.Scan(&e.ID, &e.Domain, &e.Port, &e.IP, &at); err != nil {
			return nil, err
		}
		if at > 0 {
			e.CheckedAt = time.Unix(at, 0).UTC()
		}
		endpoints = append(endpoints, e)
	}
	return endpoints, rows.Err()
}

func (ws watchStore) SaveWatchChecks(ctx context.Context, checks []proxysvc.WatchCheck) error {
	tx, err := ws.s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	oldest := time.Now().Add(-checkRetention).Unix()
	for _, c := range checks {
		cert, err := json.Marshal(c.Cert)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE watched_endpoints SET checked_at = ?, certificate = ? WHERE id = ?`,
			c.CheckedAt.Unix(), string(cert), c.EndpointID); err != nil {
			return err
		}
		// An endpoint removed while its check was out is not brought back
		// as history pointing at nothing.
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO watched_checks(endpoint_id, checked_at, days_left, fingerprint, error)
			 SELECT ?, ?, ?, ?, ? WHERE EXISTS (SELECT 1 FROM watched_endpoints WHERE id = ?)`,
			c.EndpointID, c.CheckedAt.Unix(), daysLeft(c.Cert), c.Cert.Fingerprint, c.Cert.Error,
			c.EndpointID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM watched_checks WHERE endpoint_id = ? AND (checked_at < ? OR id NOT IN (
			   SELECT id FROM watched_checks WHERE endpoint_id = ? ORDER BY checked_at DESC, id DESC LIMIT ?))`,
			c.EndpointID, oldest, c.EndpointID, checksPerEndpoint); err != nil {
			return err
		}
	}
	return tx.Commit()
}
