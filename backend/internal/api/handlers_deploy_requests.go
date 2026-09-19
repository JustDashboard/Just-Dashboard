package api

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
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

func (s *Server) handleDeploymentLifecycle(w http.ResponseWriter, r *http.Request) error {
	environmentID, err := s.deploymentEnvironment(r)
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
	want := strconv.FormatInt(environmentID, 10)
	limit := atoiDefault(r.URL.Query().Get("limit"), 100)
	// The window is bounded here rather than in the browser: a reading like
	// "restarts in the last hour" computed during render reads the clock on
	// every re-render, which makes the figure depend on when React happened to
	// paint. Asking the server for the window makes it one answer.
	var since time.Time
	if v := r.URL.Query().Get("since"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			since = t
		}
	}
	// The buffer is host-wide and bounded, so the scan asks for a wide slice
	// and narrows it here: one environment's events are a small fraction of a
	// busy host's, and asking for only `limit` of them would return a hundred
	// events belonging to other containers and none belonging to this one.
	for _, event := range s.modules.dockerEvents.Recent(2000, []string{"container"}, "") {
		if event.Owner["environment-id"] != want {
			continue
		}
		if !since.IsZero() && event.Time.Before(since) {
			continue
		}
		result.Events = append(result.Events, event)
		if len(result.Events) >= limit {
			break
		}
	}
	if !running {
		result.Reason = "The Docker event stream is not connected, so this list may be incomplete."
	}
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

// deploymentEnvironment resolves the project in the URL to the environment its
// route and its containers are labelled with. Every reading on this page is
// keyed by the environment rather than the project, because a project with two
// environments serves two different sites.
func (s *Server) deploymentEnvironment(r *http.Request) (int64, error) {
	id, err := parseID(r)
	if err != nil {
		return 0, err
	}
	summary, err := s.modules.deployRuns.DeploymentSummary(r.Context(), id, deploy.QueueBudget{
		Heavy: s.Cfg.DeployHeavySlots,
		Light: s.Cfg.DeployLightSlots,
	})
	if err != nil {
		return 0, mapDeployError(err)
	}
	if summary.EnvironmentID <= 0 {
		return 0, httpx.BadRequest("this project has no environment to read requests for")
	}
	return summary.EnvironmentID, nil
}
