package api

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"net/url"
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
		Limit:  atoiDefault(q.Get("limit"), 500),
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
//
// It continues from the cursor the window handed the page — a sequence, not a
// timestamp — so the rows that arrived between the window being read and the
// socket opening are neither missed nor sent twice, and two requests in the
// same second (which is every request, in nginx's format) are two. The store
// is the only reader: the socket asks it for what is new a few times a second,
// and the store reads the file at most that often however many sockets and
// pollers are open on the route.
func (s *Server) handleDeploymentRequestStream(w http.ResponseWriter, r *http.Request) error {
	environmentID, err := s.deploymentEnvironment(r)
	if err != nil {
		return err
	}
	if s.modules.requests == nil {
		return httpx.BadRequest("the proxy is unavailable, so requests cannot be followed")
	}
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

	name := deploy.RouteNameFor(environmentID)
	facts, err := s.modules.requests.Facts(ctx, name)
	if err != nil {
		conn.SendError("The request record could not be followed. Open Proxy to check the ingress is running.")
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
		batch, cursor, err := s.modules.requests.After(ctx, name, after, filter, 500)
		if err != nil {
			// One failed read is a hiccup; a run of them is the ingress gone,
			// and the page should hear that rather than watch a silent socket.
			if failures++; failures >= 10 {
				conn.SendError("The request record could not be followed. Open Proxy to check the ingress is running.")
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

// handleDeploymentRequestExport streams the window as CSV. Streamed rather
// than buffered because the whole point of a download is a window bigger than
// the page shows, and the row limit is the store's export bound rather than
// the table's.
func (s *Server) handleDeploymentRequestExport(w http.ResponseWriter, r *http.Request) error {
	environmentID, err := s.deploymentEnvironment(r)
	if err != nil {
		return err
	}
	if s.modules.requests == nil {
		return httpx.BadRequest("the proxy is unavailable, so requests cannot be exported")
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	filter := requestFilterFrom(r.URL.Query())
	name := deploy.RouteNameFor(environmentID)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q",
		fmt.Sprintf("requests-%d-%s.csv", environmentID, time.Now().UTC().Format("20060102-150405"))))
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{"time", "method", "path", "query", "status", "durationMs", "size", "client", "host", "proto", "tls", "userAgent", "referer"})
	_, err = s.modules.requests.Export(ctx, name, filter, 0, func(e accesslog.Entry) {
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
type deploymentLifecycle struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
	// Watching reports whether the event stream is connected, so an empty
	// feed can say "nothing happened" rather than leaving the reader to guess
	// whether anybody was listening.
	Watching bool            `json:"watching"`
	Since    *time.Time      `json:"since,omitempty"`
	Events   []dockerx.Event `json:"events"`
}

// deploymentEventKinds is what a deployment owns that the daemon emits events
// about. Containers are the reading the page exists for; a network vanishing
// under a running deployment is the other thing worth hearing, and `ownsEvent`
// below is what makes recognising one possible at all.
var deploymentEventKinds = []string{"container", "network"}

// ownsEvent reports whether an event happened to something this environment owns.
//
// A container says so itself: Docker puts an object's labels in the event's
// actor attributes, so the dashboard's own namespace rides along on every
// container event and the answer is a map lookup.
//
// A network does not. The daemon sends `name` and `type` for one and nothing
// else — no labels, however they were created — so the same test silently drops
// every network event, which is the whole of what including the kind would
// otherwise achieve. What is left is the name, and that this dashboard chose:
// `jd-e7-db-…` for a database network and `jd-preview-e7…` for a preview's both
// carry the environment in them. The trailing separator is what keeps
// environment 7 from claiming environment 70's.
func ownsEvent(event dockerx.Event, environmentID int64) bool {
	if event.Owner["environment-id"] == strconv.FormatInt(environmentID, 10) {
		return true
	}
	if event.Type != "network" {
		return false
	}
	preview := fmt.Sprintf("jd-preview-e%d", environmentID)
	return strings.HasPrefix(event.Name, fmt.Sprintf("jd-e%d-", environmentID)) ||
		event.Name == preview || strings.HasPrefix(event.Name, preview+"-")
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

func (s *Server) deploymentLifecycleEvents(r *http.Request, environmentID int64, limit int) []dockerx.Event {
	query := lifecycleQueryFrom(r.URL.Query())
	return ownedByEnvironment(
		s.modules.dockerEvents.Recent(eventScanWidth, query.kinds, query.search),
		environmentID, query.since, limit,
	)
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
	result.Events = s.deploymentLifecycleEvents(r, target.EnvironmentID, atoiDefault(r.URL.Query().Get("limit"), 100))
	s.correlateDeploymentEvents(r, result.Events, target.Name)
	if !running {
		result.Reason = "The Docker event stream is not connected, so this list may be incomplete."
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
	summary, err := s.modules.deployRuns.DeploymentSummary(r.Context(), id, deploy.QueueBudget{
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
