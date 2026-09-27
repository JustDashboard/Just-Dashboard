package api

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
)

// A proxy site's requests, read the way a deployment's are.
//
// The site page answers the questions a project's Logs page answers — what
// was asked for, what failed, who is asking — so it reads the same held
// record with the same filter, window, live tail and export. The record is
// the shared store's, under a route of its own: two stores would hold two
// caps, and the cap exists to bound what the whole process keeps.

// siteRoutePrefix marks a site's route in the shared request record. A
// deployment's route is a site name the renderer chose and can hold no
// colon, so the two can never name the same record.
const siteRoutePrefix = "site:"

func siteRoute(name string) string { return siteRoutePrefix + name }

// openRequestRecord resolves a route of the shared record: a site by the file
// its own access_log names, held to the log roots, and anything else as the
// deployment route it has always been.
func (s *Server) openRequestRecord(ctx context.Context, route string) (accesslog.Reader, accesslog.Facts, error) {
	if name, ok := strings.CutPrefix(route, siteRoutePrefix); ok {
		return s.modules.proxy.SiteAccessLogReader(ctx, name, s.modules.logs.Allow)
	}
	return s.modules.proxy.AccessLogReader(ctx, route)
}

// siteParam is the site the URL names. The page escapes it, and chi hands the
// segment over as it arrived, so a name carrying a colon — a Docker Caddy
// host's — is unescaped here rather than compared in its escaped form.
func siteParam(r *http.Request) (string, error) {
	name, err := url.PathUnescape(chi.URLParam(r, "name"))
	if err != nil || name == "" || strings.ContainsAny(name, "/\\") {
		return "", httpx.BadRequest("invalid site name")
	}
	return name, nil
}

func (s *Server) handleSiteRequests(w http.ResponseWriter, r *http.Request) error {
	name, err := siteParam(r)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 45*time.Second)
	defer cancel()
	httpx.JSON(w, http.StatusOK, s.observeSiteRequests(ctx, name, requestFilterFrom(r.URL.Query())))
	return nil
}

// observeSiteRequests answers the filter over one site's record, in the shape
// a deployment's window has, so the one workspace reads both. What differs is
// the sentence an empty answer carries: a site's nothing is a log turned off
// or a file the dashboard may not read, not a route still to be added.
func (s *Server) observeSiteRequests(ctx context.Context, name string, filter accesslog.Filter) deploy.RequestWindow {
	result := deploy.RequestWindow{
		Status: "unavailable", ObservedAt: time.Now().UTC(),
		Entries: []accesslog.Entry{}, Summary: accesslog.Summary{Classes: map[string]int{}, Buckets: []accesslog.Bucket{}},
	}
	window, err := s.modules.requests.Window(ctx, siteRoute(name), filter)
	if err != nil {
		result.Reason = siteRecordReason(err)
		return result
	}
	result.Driver, result.Format, result.Latency = window.Facts.Driver, string(window.Facts.Format), window.Facts.Latency
	result.Coverage, result.Complete = window.Coverage, window.Coverage.Complete
	if !window.Coverage.Exists {
		result.Reason = "Nothing has been recorded for this site yet. Its access log is written when the site answers its first request."
		return result
	}
	result.Status = "available"
	result.Entries, result.Slowest, result.Summary = window.Result.Entries, window.Result.Slowest, window.Result.Summary
	return result
}

// siteRecordReason is the operator's next move for a record that cannot be
// read, rather than the reader's error, which names paths and sockets.
func siteRecordReason(err error) string {
	switch {
	case errors.Is(err, proxysvc.ErrSiteNotFound):
		return "This host has no site by that name any more."
	case errors.Is(err, proxysvc.ErrNoSiteAccessLog):
		return "This site keeps no access log of its own: it is off, sent to syslog, or left to nginx's shared log, whose lines do not say which site answered. Turn its access log on in the site's form to record its requests here."
	case errors.Is(err, proxysvc.ErrOutsideLogRoots):
		return "This site writes its access log outside the directories the dashboard may read. Add that directory to JD_LOG_ROOTS to read its requests here."
	}
	return "This site's request record could not be read. Open Proxy to check the engine is running."
}

func (s *Server) handleSiteRequestStream(w http.ResponseWriter, r *http.Request) error {
	name, err := siteParam(r)
	if err != nil {
		return err
	}
	return s.followRequests(w, r, siteRoute(name),
		"This site's request record could not be followed. Open Proxy to check the engine is running.")
}

func (s *Server) handleSiteRequestExport(w http.ResponseWriter, r *http.Request) error {
	name, err := siteParam(r)
	if err != nil {
		return err
	}
	return s.exportRequests(w, r, siteRoute(name), "requests-"+name)
}

// followRequests is the live tail of one route's record, as the deployment's
// stream is: it continues from the cursor the window handed the page, so the
// rows that arrived between the two are neither missed nor sent twice, and
// the store is the only reader however many sockets are open on the route.
func (s *Server) followRequests(w http.ResponseWriter, r *http.Request, route, failure string) error {
	filter := requestFilterFrom(r.URL.Query())
	// A live tail has no window: the rows are the ones arriving now, and a
	// since bound copied from the history view would silently drop them all
	// on a host whose clock differs from the browser's.
	filter.Since, filter.Until = time.Time{}, time.Time{}
	after := accesslog.FromNow
	if raw := r.URL.Query().Get("after"); raw != "" {
		if n, err := strconv.ParseUint(raw, 10, 64); err == nil {
			after = n
		}
	}

	conn, err := s.WS.Upgrade(w, r)
	if err != nil {
		return nil
	}
	defer conn.Close()
	ctx, cancel := contextWithCancel(r)
	defer cancel()
	go conn.Keepalive(ctx)
	go conn.DrainControl(cancel)

	facts, err := s.modules.requests.Facts(ctx, route)
	if err != nil {
		conn.SendError(failure)
		return nil
	}
	conn.Send("meta", map[string]any{
		"driver": facts.Driver, "format": facts.Format, "latency": facts.Latency,
	})

	ticker := time.NewTicker(400 * time.Millisecond)
	defer ticker.Stop()
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		batch, cursor, err := s.modules.requests.After(ctx, route, after, filter, 500)
		if err != nil {
			// One failed read is a hiccup; a run of them is the log gone, and
			// the page should hear that rather than watch a silent socket.
			if failures++; failures >= 10 {
				conn.SendError(failure)
				return nil
			}
			continue
		}
		failures = 0
		after = cursor
		if len(batch) > 0 {
			conn.Send("requests", batch)
		}
	}
}

// exportRequests streams one route's window as CSV, in the columns a
// deployment's export writes, so a site's download and a deployment's open in
// the same spreadsheet.
func (s *Server) exportRequests(w http.ResponseWriter, r *http.Request, route, stem string) error {
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	filter := requestFilterFrom(r.URL.Query())
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q",
		fmt.Sprintf("%s-%s.csv", stem, time.Now().UTC().Format("20060102-150405"))))
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{"time", "method", "path", "query", "status", "durationMs", "size", "client", "host", "proto", "tls", "userAgent", "referer"})
	_, err := s.modules.requests.Export(ctx, route, filter, 0, func(e accesslog.Entry) {
		duration := ""
		if ms, ok := e.Duration(); ok {
			duration = strconv.FormatFloat(ms, 'f', 3, 64)
		}
		_ = writer.Write([]string{
			e.At().Format(time.RFC3339Nano), e.Method, e.Path, e.Query, strconv.Itoa(e.Status), duration,
			strconv.FormatInt(e.Size, 10), e.RemoteIP, e.Host, e.Proto, strconv.FormatBool(e.TLS), e.UserAgent, e.Referer,
		})
	})
	writer.Flush()
	if err != nil {
		// The headers are sent; the body says what happened where a status no
		// longer can.
		fmt.Fprintf(w, "# export stopped: the request record could not be read\n")
	}
	return nil
}
