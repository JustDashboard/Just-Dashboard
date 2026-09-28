package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// The fleet scan: a TLS report of every name the sites serve and every
// watched endpoint, run as a job so it survives the page and can be stopped,
// and the table of each one's latest stored report.

const fleetJobKind = "tls.scan-all"

// fleetTargets is the fleet as the sites and the watch list stand now.
func (s *Server) fleetTargets(ctx context.Context) ([]proxysvc.FleetTarget, error) {
	vhosts, err := s.modules.proxy.ListVHosts(ctx)
	if err != nil {
		return nil, err
	}
	watched, err := watchStore{s}.WatchedEndpoints(ctx)
	if err != nil {
		return nil, err
	}
	return proxysvc.FleetTargets(vhosts, watched), nil
}

// runningFleetScan is the fleet scan in flight, if one is.
func (s *Server) runningFleetScan() *jobs.Job {
	for _, j := range s.modules.jobs.List() {
		if j.Kind == fleetJobKind && j.Status == jobs.StatusRunning {
			return &j
		}
	}
	return nil
}

// handleTLSScanAll starts the fleet scan. A second one while the first runs
// would double every handshake against every host, so it is refused.
func (s *Server) handleTLSScanAll(w http.ResponseWriter, r *http.Request) error {
	if s.runningFleetScan() != nil {
		return httpx.Err(http.StatusConflict, "scan_running", "a scan of every site is already running")
	}
	targets, err := s.fleetTargets(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	if len(targets) == 0 {
		return httpx.BadRequest("no enabled TLS site or watched endpoint to scan")
	}
	httpx.SetAudit(r, "certificates.scan-all", "", map[string]any{"targets": len(targets)})
	s.startJob(w, r, jobs.Spec{
		Kind:   fleetJobKind,
		Title:  "Scan every site",
		Target: fmt.Sprintf("%d targets", len(targets)),
		// Each report is allowed a minute, as a single one is.
		Timeout: time.Duration(len(targets)/proxysvc.FleetConcurrency+1)*time.Minute + time.Minute,
	}, func(ctx context.Context, out jobs.Emitter) error {
		out.Status("Scanning %d targets, %d at a time", len(targets), proxysvc.FleetConcurrency)
		var saveErr error
		proxysvc.ScanFleet(ctx, targets,
			func(ctx context.Context, host string, port int) *proxysvc.TLSScan {
				ctx, cancel := context.WithTimeout(ctx, time.Minute)
				defer cancel()
				return proxysvc.ScanTLS(ctx, host, port)
			},
			func(finished int, t proxysvc.FleetTarget, report *proxysvc.TLSScan) {
				verdict := report.Grade
				if report.Summary != "" {
					verdict += " — " + report.Summary
				}
				out.Line("stdout", fmt.Sprintf("[%d/%d] %s:%d %s", finished, len(targets), t.Host, t.Port, verdict))
				if _, err := s.storeScan(context.WithoutCancel(ctx), report); err != nil && saveErr == nil {
					saveErr = err
				}
			})
		if saveErr != nil {
			return fmt.Errorf("a report could not be kept: %w", saveErr)
		}
		return ctx.Err()
	})
	return nil
}

type fleetScan struct {
	ID         int64     `json:"id"`
	Grade      string    `json:"grade"`
	Summary    string    `json:"summary"`
	DaysLeft   *int64    `json:"daysLeft,omitempty"`
	Expiring   bool      `json:"expiring"`
	Expired    bool      `json:"expired"`
	Reachable  bool      `json:"reachable"`
	Negotiated string    `json:"negotiated,omitempty"`
	Issues     int       `json:"issues"`
	CheckedAt  time.Time `json:"checkedAt"`
}

type fleetRow struct {
	proxysvc.FleetTarget
	// Scan is the target's latest stored report, from the fleet scan or a
	// single one; absent for a target never scanned.
	Scan *fleetScan `json:"scan,omitempty"`
}

type fleetView struct {
	Targets []fleetRow `json:"targets"`
	// Job is the fleet scan in flight, so a page opened during one follows it.
	Job *jobs.Job `json:"job,omitempty"`
}

// handleLatestTLSScans is the fleet with each target's latest stored report.
// It reads what is stored and sends nothing anywhere, as the history does.
func (s *Server) handleLatestTLSScans(w http.ResponseWriter, r *http.Request) error {
	targets, err := s.fleetTargets(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	view := fleetView{Targets: make([]fleetRow, 0, len(targets)), Job: s.runningFleetScan()}
	for _, t := range targets {
		row := fleetRow{FleetTarget: t}
		var sc fleetScan
		var days sql.NullInt64
		var at int64
		var report string
		err := s.Store.DB.QueryRowContext(r.Context(),
			`SELECT id, grade, days_left, reachable, checked_at, report FROM tls_scans
			  WHERE domain = ? AND port = ? ORDER BY checked_at DESC, id DESC LIMIT 1`,
			t.Host, t.Port).Scan(&sc.ID, &sc.Grade, &days, &sc.Reachable, &at, &report)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return httpx.Internal(err)
		default:
			var full proxysvc.TLSScan
			if err := json.Unmarshal([]byte(report), &full); err != nil {
				return httpx.Internal(err)
			}
			if days.Valid {
				sc.DaysLeft = &days.Int64
			}
			sc.CheckedAt = time.Unix(at, 0).UTC()
			sc.Summary, sc.Negotiated = full.Summary, full.Negotiated
			if c := full.Certificate; c != nil {
				sc.Expiring, sc.Expired = c.Expiring, c.Expired
			}
			for _, f := range full.Findings {
				if f.Level == "critical" || f.Level == "warning" {
					sc.Issues++
				}
			}
			row.Scan = &sc
		}
		view.Targets = append(view.Targets, row)
	}
	httpx.JSON(w, http.StatusOK, view)
	return nil
}
