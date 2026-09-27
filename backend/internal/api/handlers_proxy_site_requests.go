package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
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

// openRequestRecord resolves a route of the shared record: a file a site's
// access_log names, held to the log roots, and anything else as the
// deployment route it has always been.
func (s *Server) openRequestRecord(ctx context.Context, route string) (accesslog.Reader, accesslog.Facts, error) {
	if reader, facts, ok := proxysvc.SiteRecordReader(route, s.modules.logs.Allow); ok {
		return reader, facts, nil
	}
	return s.modules.proxy.AccessLogReader(ctx, route)
}

// siteRequestRoute is the record a site's requests are read from, resolved
// from its file on every question — a read of one small file — so an edit
// that moved its access_log is read from the next poll on.
func (s *Server) siteRequestRoute(name string) (string, error) {
	return s.modules.proxy.SiteRequestRoute(name, s.modules.logs.Allow)
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
	route, err := s.siteRequestRoute(name)
	if err != nil {
		result.Reason = siteRecordReason(err)
		return result
	}
	window, err := s.modules.requests.Window(ctx, route, filter)
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
	// Refused as a request rather than as a socket that opens and closes: a
	// site with nothing to follow is an answer the page already shows.
	route, err := s.siteRequestRoute(name)
	if err != nil {
		return httpx.BadRequest("%s", siteRecordReason(err))
	}
	return s.followRequests(w, r, route,
		"This site's request record could not be followed. Open Proxy to check the engine is running.")
}

func (s *Server) handleSiteRequestExport(w http.ResponseWriter, r *http.Request) error {
	name, err := siteParam(r)
	if err != nil {
		return err
	}
	route, err := s.siteRequestRoute(name)
	if err != nil {
		return httpx.BadRequest("%s", siteRecordReason(err))
	}
	return s.exportRequests(w, r, route, "requests-"+name)
}
