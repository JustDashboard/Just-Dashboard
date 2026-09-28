package api

import (
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
)

// A site's traffic and its errors, read from the logs its file names.
//
// The request record is the one a deployment's Logs page reads, parsed by the
// same package and answered in the same shape, so the chart, the facets and
// the console the deployment page draws draw a site too. The store is a
// second one, keyed by log path rather than deployment route.

// trafficWindows are the windows the page offers, as the query spells them.
var trafficWindows = map[string]time.Duration{
	"15m": 15 * time.Minute, "1h": time.Hour, "6h": 6 * time.Hour,
	"24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour,
}

// windowSince reads ?window=, defaulting to the last hour.
func windowSince(q url.Values, now time.Time) time.Time {
	d, ok := trafficWindows[q.Get("window")]
	if !ok {
		d = time.Hour
	}
	return now.Add(-d)
}

// siteTrafficFilter is the deployment page's filter with ?window= in front:
// an explicit since or until still wins, since a span dragged out of the
// chart is more exact than a preset.
func siteTrafficFilter(q url.Values) accesslog.Filter {
	filter := requestFilterFrom(q)
	if q.Get("since") == "" && q.Get("until") == "" {
		filter.Since = windowSince(q, time.Now()).UTC()
	}
	return filter
}

// siteAccessLog resolves the site in the URL to the log path the store is
// keyed by. A site with no log of its own is answered, not refused: the
// reason is what the page shows.
func (s *Server) siteAccessLog(r *http.Request) (proxysvc.SiteLogs, error) {
	logs, err := s.modules.proxy.SiteLogsFor(chi.URLParam(r, "name"))
	if errors.Is(err, proxysvc.ErrSiteNotFound) {
		return logs, httpx.Err(http.StatusNotFound, "site_not_found", err.Error())
	}
	return logs, err
}

// siteTrafficWindow is a deployment's RequestWindow with the site's logs
// beside it, so the page can name the file it read.
type siteTrafficWindow struct {
	deploy.RequestWindow
	Logs proxysvc.SiteLogs `json:"logs"`
}

func (s *Server) handleSiteTraffic(w http.ResponseWriter, r *http.Request) error {
	logs, err := s.siteAccessLog(r)
	if err != nil {
		return err
	}
	out := siteTrafficWindow{Logs: logs, RequestWindow: deploy.RequestWindow{
		Status: "unavailable", Reason: logs.AccessNote, ObservedAt: time.Now().UTC(),
		Entries: []accesslog.Entry{}, Summary: accesslog.Summary{Classes: map[string]int{}, Buckets: []accesslog.Bucket{}},
	}}
	if logs.Access == "" {
		httpx.JSON(w, http.StatusOK, out)
		return nil
	}
	ctx, cancel := timeoutCtx(r, 45*time.Second)
	defer cancel()
	window, err := s.modules.proxyExtras.siteTraffic.Window(ctx, logs.Access, siteTrafficFilter(r.URL.Query()))
	if err != nil {
		out.Reason = "The site's access log could not be read."
		httpx.JSON(w, http.StatusOK, out)
		return nil
	}
	out.Driver, out.Format = window.Facts.Driver, string(window.Facts.Format)
	// The stock format has no duration; a log_format that appends rt= does,
	// and then the readings are there to show.
	out.Latency = window.Facts.Latency || window.Result.Summary.Latency != nil
	out.Coverage, out.Complete = window.Coverage, window.Coverage.Complete
	if !window.Coverage.Exists {
		out.Reason = "Nothing has been written to " + logs.Access + " yet. It starts with the site's first request."
		httpx.JSON(w, http.StatusOK, out)
		return nil
	}
	out.Status, out.Reason = "available", ""
	out.Entries, out.Slowest, out.Summary = window.Result.Entries, window.Result.Slowest, window.Result.Summary
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// handleSiteTrafficTail is the live tail, polled: what arrived after the
// cursor the window or the last tail handed back. Without ?after= it answers
// only the cursor to start from.
func (s *Server) handleSiteTrafficTail(w http.ResponseWriter, r *http.Request) error {
	logs, err := s.siteAccessLog(r)
	if err != nil {
		return err
	}
	if logs.Access == "" {
		return httpx.Err(http.StatusConflict, "no_access_log", logs.AccessNote)
	}
	after := accesslog.FromNow
	if raw := r.URL.Query().Get("after"); raw != "" {
		if n, err := strconv.ParseUint(raw, 10, 64); err == nil {
			after = n
		}
	}
	filter := requestFilterFrom(r.URL.Query())
	filter.Since, filter.Until = time.Time{}, time.Time{}
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	entries, cursor, err := s.modules.proxyExtras.siteTraffic.After(ctx, logs.Access, after, filter, 500)
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "log_unreadable", "The site's access log could not be read.")
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"entries": entries, "cursor": cursor})
	return nil
}

func (s *Server) handleSiteTrafficExport(w http.ResponseWriter, r *http.Request) error {
	logs, err := s.siteAccessLog(r)
	if err != nil {
		return err
	}
	if logs.Access == "" {
		return httpx.Err(http.StatusConflict, "no_access_log", logs.AccessNote)
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q",
		fmt.Sprintf("requests-%s-%s.csv", logs.Site, time.Now().UTC().Format("20060102-150405"))))
	writer := csv.NewWriter(w)
	_ = writer.Write(requestCSVHeader)
	_, err = s.modules.proxyExtras.siteTraffic.Export(ctx, logs.Access, siteTrafficFilter(r.URL.Query()), 0, func(e accesslog.Entry) {
		_ = writer.Write(requestCSVRow(e))
	})
	writer.Flush()
	if err != nil {
		fmt.Fprintf(w, "# export stopped: the access log could not be read\n")
	}
	return nil
}

// siteTrafficReading is one site's last hour, for the overview.
type siteTrafficReading struct {
	Site   string `json:"site"`
	File   string `json:"file"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
	// Requests is the hour's count, which is the rate per hour.
	Requests  int      `json:"requests"`
	ErrorRate float64  `json:"errorRate"`
	Bytes     int64    `json:"bytes"`
	P95       *float64 `json:"p95,omitempty"`
	Complete  bool     `json:"complete"`
}

// handleSiteTrafficSummary is every nginx site's last hour at once. Each
// record is refreshed at most once a minute however often this is polled, so
// the overview's figures cost one read per site per minute.
func (s *Server) handleSiteTrafficSummary(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	now := time.Now().UTC()
	filter := accesslog.Filter{Since: now.Add(-time.Hour), Until: now, Limit: 1}
	out := []siteTrafficReading{}
	for _, logs := range s.modules.proxy.AllSiteLogs() {
		reading := siteTrafficReading{Site: logs.Site, File: logs.File, Status: "unavailable", Reason: logs.AccessNote}
		if logs.Access != "" && ctx.Err() == nil {
			window, err := s.modules.proxyExtras.siteTraffic.Cached(ctx, logs.Access, filter, time.Minute)
			switch {
			case err != nil:
				reading.Reason = "The site's access log could not be read."
			case !window.Coverage.Exists:
				reading.Reason = "Nothing logged yet."
			default:
				summary := window.Result.Summary
				reading.Status, reading.Reason = "available", ""
				reading.Requests = summary.Total
				reading.ErrorRate, reading.Bytes = summary.ErrorRate, summary.Bytes
				reading.Complete = window.Coverage.Complete
				if summary.Latency != nil {
					p95 := summary.Latency.P95
					reading.P95 = &p95
				}
			}
		}
		out = append(out, reading)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"observedAt": now, "sites": out})
	return nil
}

func (s *Server) handleSiteErrors(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	report, err := s.modules.proxy.SiteErrors(ctx, q.Get("site"), windowSince(q, time.Now()))
	switch {
	case errors.Is(err, proxysvc.ErrSiteNotFound):
		return httpx.Err(http.StatusNotFound, "site_not_found", err.Error())
	case err != nil:
		return httpx.Err(http.StatusBadGateway, "log_unreadable", "The error log could not be read.")
	}
	httpx.JSON(w, http.StatusOK, report)
	return nil
}
