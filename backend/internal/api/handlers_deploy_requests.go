package api

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// The two readings a deployment's Logs page needs that its container's output
// cannot give: what the proxy served, and what Docker did to the container.
//
// Both are about the same deployment over the same window, which is why they
// are one file: a spike of 502s and a restart at the same minute are one event
// described from two sides, and the page draws them on one timeline.

// requestFilterFrom parses the question. It is deliberately the same shape as
// the log filter beside it — a window, a set of chips, a substring — so that
// moving between the Requests and Output views does not mean learning a second
// set of controls.
func requestFilterFrom(q url.Values) accesslog.Filter {
	filter := accesslog.Filter{
		Path:   strings.TrimSpace(q.Get("path")),
		Host:   strings.TrimSpace(q.Get("host")),
		Client: strings.TrimSpace(q.Get("client")),
		// A family ("Chrome", "Googlebot") and a site ("www.google.com"), the
		// values the Agents and Came from lists rank, so a row of either is a
		// filter like every other list's.
		Agent:   strings.TrimSpace(q.Get("agent")),
		Referer: strings.TrimSpace(q.Get("referer")),
		Limit:   atoiDefault(q.Get("limit"), 500),
	}
	if methods := q.Get("methods"); methods != "" {
		filter.Methods = strings.Split(strings.ToUpper(methods), ",")
	}
	if classes := q.Get("classes"); classes != "" {
		filter.Classes = strings.Split(strings.ToLower(classes), ",")
	}
	for _, code := range strings.Split(q.Get("status"), ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(code)); err == nil && n >= 100 && n <= 599 {
			filter.Status = append(filter.Status, n)
		}
	}
	filter.MinMs, _ = strconv.ParseFloat(q.Get("minMs"), 64)
	filter.MaxMs, _ = strconv.ParseFloat(q.Get("maxMs"), 64)
	filter.PagesOnly = q.Get("pages") == "true"
	if v := q.Get("since"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			filter.Since = t
		}
	}
	if v := q.Get("until"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			filter.Until = t
		}
	}
	// An unbounded read walks the whole retained record to answer a question
	// about "now", which on a busy deployment is tens of millions of lines.
	// A default window keeps the common case instant and is reported back, so
	// an empty answer is never read as "nothing ever happened".
	if filter.Since.IsZero() && filter.Until.IsZero() {
		filter.Since = time.Now().Add(-24 * time.Hour).UTC()
	}
	return filter
}

func (s *Server) handleDeploymentRequests(w http.ResponseWriter, r *http.Request) error {
	environmentID, err := s.deploymentEnvironment(r)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 45*time.Second)
	defer cancel()
	var record deploy.RequestRecord
	if s.modules.requests != nil {
		record = s.modules.requests
	}
	window := deploy.ObserveRequests(ctx, record, environmentID, requestFilterFrom(r.URL.Query()))
	httpx.JSON(w, http.StatusOK, window)
	return nil
}

// handleDeploymentRequestStream is the live tail of the request record.
func (s *Server) handleDeploymentRequestStream(w http.ResponseWriter, r *http.Request) error {
	environmentID, err := s.deploymentEnvironment(r)
	if err != nil {
		return err
	}
	if s.modules.requests == nil {
		return httpx.BadRequest("the proxy is unavailable, so requests cannot be followed")
	}
	return s.followRequests(w, r, deploy.RouteNameFor(environmentID),
		"The request record could not be followed. Open Proxy to check the ingress is running.")
}

// handleDeploymentRequestExport streams the window as CSV.
func (s *Server) handleDeploymentRequestExport(w http.ResponseWriter, r *http.Request) error {
	environmentID, err := s.deploymentEnvironment(r)
	if err != nil {
		return err
	}
	if s.modules.requests == nil {
		return httpx.BadRequest("the proxy is unavailable, so requests cannot be exported")
	}
	return s.exportRequests(w, r, deploy.RouteNameFor(environmentID), fmt.Sprintf("requests-%d", environmentID))
}

// followRequests is the live tail of one route's record — a deployment's or
// a site's, which are one loop over two routes.
//
// It continues from the cursor the window handed the page — a sequence, not a
// timestamp — so the rows that arrived between the window being read and the
// socket opening are neither missed nor sent twice, and two requests in the
// same second (which is every request, in nginx's format) are two. The store
// is the only reader: the socket asks it for what is new a few times a second,
// and the store reads the file at most that often however many sockets and
// pollers are open on the route.
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
			// One failed read is a hiccup; a run of them is the proxy or its
			// log gone, and the page should hear that rather than watch a
			// silent socket.
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

// exportRequests streams one route's window as CSV. Streamed rather than
// buffered because the whole point of a download is a window bigger than the
// page shows, and the row limit is the store's export bound rather than the
// table's. A site's download and a deployment's share the columns, so they
// open in the same spreadsheet.
func (s *Server) exportRequests(w http.ResponseWriter, r *http.Request, route, stem string) error {
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	filter := requestFilterFrom(r.URL.Query())
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q",
		fmt.Sprintf("%s-%s.csv", stem, time.Now().UTC().Format("20060102-150405"))))
	writer := csv.NewWriter(w)
	_ = writer.Write(requestCSVHeader)
	_, err := s.modules.requests.Export(ctx, route, filter, 0, func(e accesslog.Entry) {
		_ = writer.Write(requestCSVRow(e))
	})
	writer.Flush()
	if err != nil {
		// The headers are sent; the body says what happened where a status no
		// longer can.
		fmt.Fprintf(w, "# export stopped: the request record could not be read\n")
	}
	return nil
}

// csvText is a cell a stranger wrote — a user agent, a referer, a path — made
// safe to open in a spreadsheet, which runs a cell that starts like a formula.
// A leading quote is the spreadsheets' own way to say "this is text".
func csvText(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}

// requestCSVHeader and requestCSVRow are the one spelling of a request as a
// CSV row, shared by every download of a request record.
var requestCSVHeader = []string{"time", "method", "path", "query", "status", "durationMs", "size", "client", "host", "proto", "tls", "userAgent", "referer"}

func requestCSVRow(e accesslog.Entry) []string {
	duration := ""
	if ms, ok := e.Duration(); ok {
		duration = strconv.FormatFloat(ms, 'f', 3, 64)
	}
	return []string{
		e.At().Format(time.RFC3339Nano), csvText(e.Method), csvText(e.Path), csvText(e.Query), strconv.Itoa(e.Status), duration,
		strconv.FormatInt(e.Size, 10), csvText(e.RemoteIP), csvText(e.Host), csvText(e.Proto), strconv.FormatBool(e.TLS),
		csvText(e.UserAgent), csvText(e.Referer),
	}
}

// handleDeploymentRunTraffic compares the traffic either side of a run's
// activation, beside the container metrics the run page already shows.
func (s *Server) handleDeploymentRunTraffic(w http.ResponseWriter, r *http.Request) error {
	projectID, runID, err := deploymentRunIDs(r)
	if err != nil {
		return err
	}
	snapshot, err := s.modules.deployRuns.Snapshot(r.Context(), runID)
	if err != nil {
		return mapDeployError(err)
	}
	if snapshot.Run.ProjectID != projectID {
		return mapDeployError(deploy.ErrRunNotFound)
	}
	ctx, cancel := timeoutCtx(r, 45*time.Second)
	defer cancel()
	var record deploy.RequestRecord
	if s.modules.requests != nil {
		record = s.modules.requests
	}
	httpx.JSON(w, http.StatusOK, deploy.ObserveRunTraffic(ctx, record, *snapshot, time.Now().UTC()))
	return nil
}

// handleDeploymentTrafficPulse is every project's last hour at once, for the
// fleet's cards. Cached to the minute per route: twenty cards polling every
// ten seconds must not become twenty file reads every ten seconds.
func (s *Server) handleDeploymentTrafficPulse(w http.ResponseWriter, r *http.Request) error {
	fleet, err := s.modules.deployRuns.Fleet(r.Context(), deploy.QueueBudget{
		Heavy: s.Cfg.DeployHeavySlots,
		Light: s.Cfg.DeployLightSlots,
	})
	if err != nil {
		return httpx.Internal(err)
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	var record deploy.CachedRecord
	if s.modules.requests != nil {
		record = s.modules.requests
	}
	now := time.Now().UTC()
	out := map[string]deploy.TrafficPulse{}
	for _, project := range fleet.Deployments {
		if ctx.Err() != nil {
			break
		}
		out[strconv.FormatInt(project.ID, 10)] = deploy.ObserveTrafficPulse(ctx, record, project.EnvironmentID, now)
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// deploymentLifecycle is what Docker did to this deployment's containers:
// exits with their codes, OOM kills, restart-policy firings and health flips.
//
// It is the third of the page's three questions. A container that is silent
// because the application logs nothing and a container that is silent because
// it died four minutes ago look identical in a log tail, and this is the
// reading that tells them apart.
//
// The answer is read from two places: the ring in memory, which has the
// moment, and the store, which has the month. A restart of the dashboard
// used to empty the record — the exit at four in the morning was gone by the
// time anybody asked at nine — and the record now begins at RecordSince
// rather than at the process.
type deploymentLifecycle struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
	// Watching reports whether the event stream is connected, so an empty
	// feed can say "nothing happened" rather than leaving the reader to guess
	// whether anybody was listening.
	Watching bool `json:"watching"`
	// Since is when this process connected to the stream: what the ring
	// holds from. The record itself reaches back to RecordSince.
	Since *time.Time `json:"since,omitempty"`
	// RecordSince is the oldest event the store holds for any deployment —
	// how far back the feed can see. Absent until the first event is kept.
	RecordSince *time.Time `json:"recordSince,omitempty"`
	// RetentionDays is how long an event is kept, so the footer can say what
	// the record is rather than leave the reader to infer it.
	RetentionDays int             `json:"retentionDays"`
	Events        []dockerx.Event `json:"events"`
}

// deploymentEventKinds is what a deployment owns that the daemon emits events
// about. Containers are the reading the page exists for; a network vanishing
// under a running deployment is the other thing worth hearing, and `ownsEvent`
// below is what makes recognising one possible at all.
var deploymentEventKinds = []string{"container", "network"}

// ownsEvent reports whether an event happened to something this environment
// owns: a container by the dashboard's labels on the event, a network by the
// name this dashboard gave it, since the daemon sends a network's labels on
// no event. The rule is dockerx's (Event.EnvironmentID), because the store
// that keeps a deployment's events past this process asks the same question
// of every event as it arrives, and two spellings of it would be two answers.
func ownsEvent(event dockerx.Event, environmentID int64) bool {
	return event.OwnedBy(environmentID)
}

// lifecycleQuery is the question the Events view asks, in the two halves it is
// answered in: `kinds` and `search` the buffer can apply while it walks itself,
// `since` this package applies after the owner filter.
type lifecycleQuery struct {
	kinds  []string
	search string
	since  time.Time
}

func lifecycleQueryFrom(q url.Values) lifecycleQuery {
	out := lifecycleQuery{kinds: deploymentEventKinds, search: strings.TrimSpace(q.Get("search"))}
	if raw := strings.TrimSpace(q.Get("kinds")); raw != "" {
		out.kinds = strings.Split(raw, ",")
	}
	// The window is bounded here rather than in the browser: a reading like
	// "restarts in the last hour" computed during render reads the clock on
	// every re-render, which makes the figure depend on when React happened to
	// paint. Asking the server for the window makes it one answer.
	if v := q.Get("since"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			out.since = t
		}
	}
	return out
}

// ownedByEnvironment narrows a slice of the host's buffer to one deployment.
//
// The buffer is host-wide and bounded, so the scan asks for a wide slice and
// narrows it here: one environment's events are a small fraction of a busy
// host's, and asking the buffer for only `limit` of them would return a hundred
// events belonging to other containers and none belonging to this one.
func ownedByEnvironment(events []dockerx.Event, environmentID int64, since time.Time, limit int) []dockerx.Event {
	out := []dockerx.Event{}
	for _, event := range events {
		if !ownsEvent(event, environmentID) {
			continue
		}
		if !since.IsZero() && event.Time.Before(since) {
			continue
		}
		out = append(out, event)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// deploymentLifecycleEvents is the feed's answer: the ring's events this
// environment owns and the store's, as one list newest first with each event
// once. The ring goes first because it is the one with the last few seconds
// in it; the store is what reaches past the ring's bound and past the last
// restart of this process. A store that cannot be read costs the history and
// not the feed: the ring's answer stands alone, as it did before there was a
// store.
func (s *Server) deploymentLifecycleEvents(r *http.Request, environmentID int64, limit int) []dockerx.Event {
	query := lifecycleQueryFrom(r.URL.Query())
	if limit <= 0 {
		limit = 100
	}
	recent := ownedByEnvironment(
		s.modules.dockerEvents.Recent(eventScanWidth, query.kinds, query.search),
		environmentID, query.since, limit,
	)
	kept, err := s.modules.deployEvents.Find(r.Context(), environmentID,
		dockerx.EventFilter{Kinds: query.kinds, Search: query.search}, query.since, limit)
	if err != nil {
		s.Log.Warn("deployment events could not be read from the store", "environment", environmentID, "error", err)
		return recent
	}
	return mergeLifecycleEvents(recent, kept, limit)
}

// mergeLifecycleEvents joins the ring's slice and the store's into one list,
// newest first and at most limit long, with an event held by both counted
// once. Two copies of one event agree on the daemon's nanosecond, its kind,
// its action and the object it happened to, which is the key the browser
// dedupes on as well.
func mergeLifecycleEvents(recent, kept []dockerx.Event, limit int) []dockerx.Event {
	seen := make(map[string]bool, len(recent)+len(kept))
	out := make([]dockerx.Event, 0, len(recent)+len(kept))
	for _, batch := range [][]dockerx.Event{recent, kept} {
		for _, ev := range batch {
			key := ev.Time.UTC().Format(time.RFC3339Nano) + "|" + ev.Type + "|" + ev.Action + "|" + ev.ID + "|" + ev.Name
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, ev)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// eventScanWidth is the whole ring: the owner filter below it is selective
// enough that anything narrower answers a quiet deployment with nothing.
const eventScanWidth = 2000

func (s *Server) handleDeploymentLifecycle(w http.ResponseWriter, r *http.Request) error {
	target, err := s.deploymentTarget(r)
	if err != nil {
		return err
	}
	result := deploymentLifecycle{Status: "unavailable", Events: []dockerx.Event{}}
	if s.modules.dockerEvents == nil {
		result.Reason = "Docker is unavailable on this host, so container lifecycle cannot be observed."
		httpx.JSON(w, http.StatusOK, result)
		return nil
	}
	running, watchingSince, _ := s.modules.dockerEvents.Status()
	result.Status, result.Watching = "available", running
	if !watchingSince.IsZero() {
		result.Since = &watchingSince
	}
	result.RetentionDays = int(s.modules.deployEvents.Retention() / (24 * time.Hour))
	if since, err := s.modules.deployEvents.Since(r.Context()); err == nil && !since.IsZero() {
		result.RecordSince = &since
	}
	result.Events = s.deploymentLifecycleEvents(r, target.EnvironmentID, atoiDefault(r.URL.Query().Get("limit"), 100))
	s.correlateDeploymentEvents(r, result.Events, target.Name)
	if !running {
		result.Reason = "The Docker event stream is not connected, so this list stops at the last event heard."
	}
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

// handleDeploymentLifecycleStream follows this environment's events as they
// arrive, so the feed moves on the restart rather than up to ten seconds after
// it. The buffered past goes first, for the same reason the host feed's does: a
// tab opened at 09:00 must still show the container that died at 03:00.
//
// Nothing here is correlated against the audit log. A single arriving event
// would cost its own audit query, and the poll beside this socket re-reads the
// same event with its trigger a moment later — which is why the client prefers
// the polled copy when both hold the same event.
func (s *Server) handleDeploymentLifecycleStream(w http.ResponseWriter, r *http.Request) error {
	environmentID, err := s.deploymentEnvironment(r)
	if err != nil {
		return err
	}
	if s.modules.dockerEvents == nil {
		return httpx.BadRequest("Docker is unavailable on this host, so container lifecycle cannot be followed")
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

	if recent := s.deploymentLifecycleEvents(r, environmentID, 200); len(recent) > 0 {
		if err := conn.Send("events", recent); err != nil {
			return nil
		}
	}
	events, unsubscribe := s.modules.dockerEvents.Subscribe()
	defer unsubscribe()
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-events:
			if !ok {
				return nil
			}
			if !ownsEvent(ev, environmentID) {
				continue
			}
			if err := conn.Send("events", []dockerx.Event{ev}); err != nil {
				return nil
			}
		}
	}
}

// deployCorrelationWindow is how long before a container event an audit entry
// for a release may sit and still be its cause. A release is not an instant:
// the entry is written when the button is pressed and the containers appear
// after the build, which on a cold cache is minutes rather than seconds.
const deployCorrelationWindow = 15 * time.Minute

// correlateDeploymentEvents answers "did something in this dashboard do that"
// for one deployment's containers.
//
// The host feed's match — a name, in a window symmetric around the event —
// answers it for `docker.*` actions and is run first, because an audit entry
// naming this exact container is a better answer than anything below it. It
// finds nothing for a release, though: a deploy is audited against the
// *project* ("lampino"), never the container it goes on to create, and by the
// time that container starts the entry is minutes old. So the second pass
// matches the project's name across a directional window — the entry must come
// before the event, because a release cannot be explained by a button pressed
// after it.
//
// Two actions are never a release's doing however well they line up. The
// kernel's OOM reaper and a health check changing its verdict are the daemon
// reporting something that happened *to* a container, and filing those under
// "this dashboard" would send an operator to the audit log for an answer that
// is not there.
func (s *Server) correlateDeploymentEvents(r *http.Request, events []dockerx.Event, project string) {
	s.correlateEvents(r, events)
	project = strings.TrimSpace(project)
	if project == "" {
		return
	}
	s.correlateEventsWith(r, events, "deploy.", deployCorrelationWindow,
		func(entry *audit.Entry, ev *dockerx.Event) bool {
			if strings.TrimSpace(entry.Target) != project || daemonOwnedAction(ev.Action) {
				return false
			}
			lead := ev.Time.Sub(entry.TS)
			return lead >= 0 && lead <= deployCorrelationWindow
		})
}

// daemonOwnedAction reports the events nobody asks for: a container exiting,
// the kernel stopping one, and a health check changing its verdict. `dockerx`
// draws the same line when it decides an event's source.
//
// An exit is on the list because this match is coarse. A release does cause
// one when it recreates a container, and the host feed is right to attribute
// that — it has an audit entry naming the container, seconds away. This pass
// has a project's name and a fifteen-minute window, which a container that
// crashed of its own accord a minute after an unrelated deploy fits perfectly
// well. Labelling a crash "this dashboard did it" is the one error worth
// engineering against: an exit is what the reader came here to explain.
func daemonOwnedAction(action string) bool {
	return action == "die" || action == "oom" || strings.HasPrefix(action, "health_status")
}

// deploymentEnvironment resolves the project in the URL to the environment its
// route and its containers are labelled with. Every reading on this page is
// keyed by the environment rather than the project, because a project with two
// environments serves two different sites.
func (s *Server) deploymentEnvironment(r *http.Request) (int64, error) {
	summary, err := s.deploymentTarget(r)
	if err != nil {
		return 0, err
	}
	return summary.EnvironmentID, nil
}

// deploymentTarget is the same resolution for the readings that need more of
// the deployment than its environment id — the lifecycle feed wants the
// project's name, because that is what an audit entry for a deploy names.
func (s *Server) deploymentTarget(r *http.Request) (*deploy.DeploymentSummary, error) {
	id, err := parseID(r)
	if err != nil {
		return nil, err
	}
	// ?environment= reads a staging environment's route and containers in
	// place of production's; the summary refuses one of another project.
	environmentID, err := environmentQuery(r)
	if err != nil {
		return nil, err
	}
	summary, err := s.modules.deployRuns.EnvironmentDeploymentSummary(r.Context(), id, environmentID, deploy.QueueBudget{
		Heavy: s.Cfg.DeployHeavySlots,
		Light: s.Cfg.DeployLightSlots,
	})
	if err != nil {
		return nil, mapDeployError(err)
	}
	if summary == nil || summary.EnvironmentID <= 0 {
		return nil, httpx.BadRequest("this project has no environment to read requests for")
	}
	return summary, nil
}
