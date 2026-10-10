package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/metrics"
	"github.com/Wayy01/Just-Dashboard/backend/internal/sysinfo"
	"github.com/go-chi/chi/v5"
)

func (s *Server) mountSystemRoutes(r chi.Router) {
	r.Route("/system", func(r chi.Router) {
		s.mountAdvisorRoutes(r)
		r.Method(http.MethodGet, "/host", s.handle(s.handleSystemHost))
		r.Method(http.MethodGet, "/metrics", s.handle(s.handleSystemMetrics))
		r.Method(http.MethodGet, "/metrics/history", s.handle(s.handleMetricsHistory))
		r.Method(http.MethodGet, "/metrics/storage", s.handle(s.handleStorageHistory))
		r.Method(http.MethodGet, "/metrics/events", s.handle(s.handleMetricsEvents))
		r.Method(http.MethodGet, "/health", s.handle(s.handleSystemHealth))
		r.Method(http.MethodGet, "/disk-usage", s.handle(s.handleDiskBreakdown))
		r.Method(http.MethodGet, "/stream", s.handle(s.handleSystemStream))
	})
}

func (s *Server) handleSystemHost(w http.ResponseWriter, r *http.Request) error {
	info, err := s.modules.sys.Host(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, info)
	return nil
}

func (s *Server) handleSystemMetrics(w http.ResponseWriter, r *http.Request) error {
	snap, err := s.modules.sys.Collect(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, snap)
	return nil
}

// handleMetricsHistory serves the recorded past, which is the only part of
// the metrics story that survives the browser tab being closed.
//
// The window is expressed either as a range back from now — the normal case,
// "show me the last 24 hours" — or as an explicit from/to for a client that
// wants to hold a window still while time moves. Either way the server picks
// the bucket width from the number of points asked for, so a week and a minute
// cost the same to render.
func (s *Server) handleMetricsHistory(w http.ResponseWriter, r *http.Request) error {
	if !s.modules.metrics.Enabled() {
		return httpx.Err(http.StatusServiceUnavailable, "metrics_history_disabled",
			"metrics history is not being recorded on this host (JD_METRICS_RETENTION=0)")
	}
	from, to, points, err := historyWindow(r)
	if err != nil {
		return err
	}
	series, err := s.modules.metrics.Range(r.Context(), from, to, points)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, series)
	return nil
}

// handleStorageHistory answers "which filesystem is filling up", which the
// single fullest-mount figure in the host series can summarise but not answer.
func (s *Server) handleStorageHistory(w http.ResponseWriter, r *http.Request) error {
	if !s.modules.metrics.Enabled() {
		return httpx.Err(http.StatusServiceUnavailable, "metrics_history_disabled",
			"metrics history is not being recorded on this host (JD_METRICS_RETENTION=0)")
	}
	from, to, points, err := historyWindow(r)
	if err != nil {
		return err
	}
	series, err := s.modules.metrics.StorageRange(r.Context(), from, to, points)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, series)
	return nil
}

// handleMetricsEvents serves what happened during a window, so a chart can say
// *why* a line moved rather than only that it did.
//
// Unlike the series endpoints this one works with history recording turned
// off: deploys, backups and audited actions are stored regardless of
// JD_METRICS_RETENTION, and the markers are worth having on a live chart too.
// Only the reboot markers, which are inferred from the recorded uptime column,
// go quiet in that configuration.
func (s *Server) handleMetricsEvents(w http.ResponseWriter, r *http.Request) error {
	from, to, _, err := historyWindow(r)
	if err != nil {
		return err
	}
	limit := atoiDefault(r.URL.Query().Get("limit"), 200)
	if limit > 500 {
		limit = 500
	}
	events, err := s.modules.metrics.Events(r.Context(), from, to, limit)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, events)
	return nil
}

// handleSystemHealth turns the numbers into a verdict.
//
// Evaluated on the server rather than in the browser for two reasons: the
// thresholds are a claim the product is making and belong with the code that
// records the data, and the checks that look at an hour of history would
// otherwise mean shipping an hour of history to every client that wants a
// badge in the top bar.
func (s *Server) handleSystemHealth(w http.ResponseWriter, r *http.Request) error {
	snap, err := s.modules.sys.Collect(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	health := s.runtimeHealth(r.Context(), s.modules.metrics.Assess(r.Context(), snap), snap)
	s.correlateProbes(r, &health)
	httpx.JSON(w, http.StatusOK, health)
	return nil
}

// correlateProbes cites the saved diagnostic runs of the last half hour
// beside the network verdict. Saved runs name their targets and are an
// administrator's to read, so a reader without the capability gets the
// verdict without them.
func (s *Server) correlateProbes(r *http.Request, health *metrics.Health) {
	if s.modules.diagnostics == nil || !httpx.MustPrincipal(r).Can(auth.CapSystemAdmin) {
		return
	}
	runs, err := s.modules.diagnostics.List(r.Context())
	if err != nil {
		return
	}
	evidence := make([]metrics.ProbeEvidence, 0, len(runs))
	for _, run := range runs {
		if run.EndedAt == nil {
			continue
		}
		evidence = append(evidence, metrics.ProbeEvidence{
			ID: run.ID, Name: run.Name, Tool: run.Request.Tool, Target: run.Request.Target,
			Outcome: run.Outcome, EndedAt: *run.EndedAt,
		})
	}
	metrics.CorrelateProbes(health, evidence, time.Now())
}

// historyWindow reads the window every history endpoint accepts, so the host
// series and a container's series are asked for the same way.
func historyWindow(r *http.Request) (from, to time.Time, points int, err error) {
	q := r.URL.Query()

	points = atoiDefault(q.Get("points"), 240)
	if points < 2 {
		points = 2
	}
	// The ceiling is about the chart, not the database: a series with more
	// points than the canvas has pixels costs bandwidth to say nothing.
	if points > 2000 {
		points = 2000
	}

	to = time.Now()
	if v := q.Get("to"); v != "" {
		parsed, perr := parseInstant(v)
		if perr != nil {
			return from, to, points, httpx.BadRequest("to: %v", perr)
		}
		to = parsed
	}
	from = to.Add(-time.Hour)
	if v := q.Get("from"); v != "" {
		parsed, perr := parseInstant(v)
		if perr != nil {
			return from, to, points, httpx.BadRequest("from: %v", perr)
		}
		from = parsed
	} else if v := q.Get("range"); v != "" {
		window, werr := metrics.ParseWindow(v)
		if werr != nil {
			return from, to, points, httpx.BadRequest("range: %v", werr)
		}
		from = to.Add(-window)
	}
	if !from.Before(to) {
		return from, to, points, httpx.BadRequest("the requested window ends before it starts")
	}
	return from, to, points, nil
}

// parseInstant accepts either RFC3339 or unix seconds, because the first is
// what a human writes into a URL and the second is what Date.now()/1000 gives
// the frontend without a formatting round trip.
func parseInstant(v string) (time.Time, error) {
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		return time.Unix(n, 0), nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, fmt.Errorf("expected unix seconds or an RFC3339 timestamp, got %q", v)
	}
	return t, nil
}

// handleDiskBreakdown answers "what is filling this mount". It is a recursive
// walk, so it runs under a hard deadline and returns whatever it measured if
// the tree is too large to finish.
func (s *Server) handleDiskBreakdown(w http.ResponseWriter, r *http.Request) error {
	path := r.URL.Query().Get("path")
	if path == "" {
		path = "/"
	}
	limit := atoiDefault(r.URL.Query().Get("limit"), 25)
	if limit < 1 {
		return httpx.BadRequest("limit must be positive")
	}
	if limit > 100 {
		limit = 100
	}
	resolved, err := s.modules.files.Resolve(path)
	if err != nil {
		return mapFileError(err)
	}
	select {
	case s.diskScans <- struct{}{}:
		defer func() { <-s.diskScans }()
	default:
		return httpx.Err(http.StatusTooManyRequests, "disk_scan_busy",
			"two disk usage scans are already running")
	}
	ctx, cancel := timeoutCtx(r, 45*time.Second)
	defer cancel()
	entries, err := sysinfo.DirBreakdown(ctx, resolved, limit)
	if err != nil {
		if errors.Is(err, sysinfo.ErrDirScanLimit) {
			return httpx.Err(http.StatusUnprocessableEntity, "disk_scan_limit",
				"the directory is too large to scan safely")
		}
		return httpx.BadRequest("cannot scan %s: %v", path, err)
	}
	httpx.JSON(w, http.StatusOK, entries)
	return nil
}

// handleSystemStream pushes a metrics snapshot on an interval the client
// chooses within a sane band, so a dashboard tab left open does not sample
// the host harder than it needs to.
func (s *Server) handleSystemStream(w http.ResponseWriter, r *http.Request) error {
	interval := 2 * time.Second
	if v := r.URL.Query().Get("interval"); v != "" {
		if ms, err := strconv.Atoi(v); err == nil {
			interval = clampDuration(time.Duration(ms)*time.Millisecond, time.Second, 30*time.Second)
		}
	}
	conn, err := s.WS.Upgrade(w, r)
	if err != nil {
		return nil // the upgrader has already written a response
	}
	defer conn.Close()

	ctx, cancel := contextWithCancel(r)
	defer cancel()
	go conn.Keepalive(ctx)
	go conn.DrainControl(cancel)

	// A fresh collector per socket keeps each client's rate deltas aligned to
	// its own interval instead of whatever the last poller happened to use.
	collector := sysinfo.NewCollector()
	if host, err := s.modules.sys.Host(ctx); err == nil {
		conn.Send("host", host)
	}
	collector.Collect(ctx) // prime the counters so the first frame carries rates

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			snap, err := collector.Collect(ctx)
			if err != nil {
				conn.SendError(err.Error())
				continue
			}
			if err := conn.Send("metrics", snap); err != nil {
				return nil
			}
		}
	}
}
