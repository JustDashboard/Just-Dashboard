package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/metrics"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// The deployments in the Prometheus text format.
//
// The databases have had a scrape endpoint since 0.7; the deployments, which
// are what most of a server exists to run, had none — their restarts lived in
// a ring that emptied on restart and their traffic in a chart. This is the
// same shape as the databases' exposition and behind the same key: what the
// dashboard already holds about every environment, read from its own store
// and said in the one format every alerting stack reads. A scrape dials
// nothing but the local Docker socket, once, for which containers are up.
//
// Labels name the deployment and its environment and never a variable, a
// domain's secret or a container's command: `deployment_id` and `deployment`
// (the project), `environment` (its slug), and `release` (the live release's
// number) on the series that are about one release — a release is the unit a
// counter of its restarts means anything over, and a new release is a new
// series that starts again from nothing.

const (
	deploymentMetricsPath     = "/metrics/deployments"
	deploymentMetricsEndpoint = "/api/v1" + deploymentMetricsPath
	deploymentPromPrefix      = "jd_deployment_"
	// A container sample older than this is not a reading of now: the same
	// rule as the recorder's outage rule, three missed intervals.
	deploymentSampleStaleIntervals = 3
	// The request figures are over the last hour, from the cached window the
	// fleet's cards read, so twenty environments scraped every fifteen
	// seconds are not twenty file reads every fifteen seconds.
	deploymentRequestWindow = time.Hour
	// What a scrape asks the proxy or the daemon per environment is kept
	// this long, since a Caddy ingress answers the maintenance switch by
	// reading two files inside its container.
	deploymentScrapeCacheFor = time.Minute
)

// exportedEnvironment is one row of the exposition: a production or staging
// environment of a deployment that is not archived, with its live release.
type exportedEnvironment struct {
	ProjectID     int64
	Project       string
	EnvironmentID int64
	Environment   string
	Kind          string
	LiveReleaseID int64
	ReleaseNumber int64
	ActivatedAt   time.Time
}

// scrapeCache remembers what a scrape read that costs more than a query.
type scrapeCache struct {
	mu          sync.Mutex
	maintenance map[int64]cachedSwitch
}

type cachedSwitch struct {
	at    time.Time
	state proxysvc.MaintenanceSwitch
	ok    bool
}

// handleDeploymentMetricsExport serves every environment's figures. The
// route is for API keys: see mountDatabaseMetricsExport.
func (s *Server) handleDeploymentMetricsExport(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	environments, err := s.exportedEnvironments(ctx)
	if err != nil {
		// A failed scrape is the honest answer to a store that could not be
		// read: series that vanish look like deployments that went away.
		return httpx.Internal(err)
	}
	now := time.Now()
	out := newPromFamiliesFor(deploymentPromPrefix)
	running := s.managedContainers(ctx)
	for _, env := range environments {
		if ctx.Err() != nil {
			return httpx.Internal(ctx.Err())
		}
		if err := s.addEnvironmentSeries(ctx, out, env, running, now); err != nil {
			return httpx.Internal(err)
		}
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, err = out.WriteTo(w)
	return err
}

// exportedEnvironments is every environment the exposition speaks for.
// Previews are left out: they come and go with a pull request, and a series
// per preview would be a series per branch anybody ever opened.
func (s *Server) exportedEnvironments(ctx context.Context) ([]exportedEnvironment, error) {
	rows, err := s.Store.DB.QueryContext(ctx, `
		SELECT p.id, p.name, e.id, e.slug, e.kind, e.live_release_id,
		       COALESCE(r.release_number, 0), COALESCE(r.activated_at, 0)
		  FROM deploy_environments e
		  JOIN deploy_projects p ON p.id = e.project_id
		  LEFT JOIN deploy_releases r ON r.id = e.live_release_id
		 WHERE e.archived_at = 0 AND p.archived_at = 0 AND e.kind IN ('production', 'staging')
		 ORDER BY p.id, e.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []exportedEnvironment{}
	for rows.Next() {
		var env exportedEnvironment
		var activated int64
		if err := rows.Scan(&env.ProjectID, &env.Project, &env.EnvironmentID, &env.Environment, &env.Kind,
			&env.LiveReleaseID, &env.ReleaseNumber, &activated); err != nil {
			return nil, err
		}
		if activated > 0 {
			env.ActivatedAt = time.Unix(activated, 0).UTC()
		}
		out = append(out, env)
	}
	return out, rows.Err()
}

// managedContainer is what one Docker listing said about a container the
// dashboard runs, keyed below by the release it belongs to.
type managedContainer struct {
	ID, Name, State, Health string
}

// managedContainers is the daemon's answer, once per scrape, to which of the
// dashboard's containers exist and whether they run — one listing filtered by
// the dashboard's own label, partitioned by release here. nil when Docker did
// not answer, which costs the up and running series and nothing else: the
// rest of the exposition is the store's.
func (s *Server) managedContainers(ctx context.Context) map[int64][]managedContainer {
	if s.modules.docker == nil {
		return nil
	}
	listCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	containers, err := s.modules.docker.ListContainersWithLabels(listCtx, map[string]string{"io.just-dashboard.managed": "true"})
	if err != nil {
		return nil
	}
	out := map[int64][]managedContainer{}
	for _, c := range containers {
		// A release task's one-shot container carries its release's labels
		// and is not one of the release's services.
		if c.Labels["io.just-dashboard.release-task"] != "" {
			continue
		}
		release, err := strconv.ParseInt(c.Labels["io.just-dashboard.release-id"], 10, 64)
		if err != nil || release <= 0 {
			continue
		}
		out[release] = append(out[release], managedContainer{ID: c.ID, Name: c.Name, State: c.State, Health: c.Health})
	}
	return out
}

// runResults are the ends a run can come to, every one exported so a rate
// over a result that has not happened yet reads as zero rather than absent.
var runResults = []deploy.RunState{deploy.RunSucceeded, deploy.RunFailed, deploy.RunCancelled, deploy.RunRolledBack, deploy.RunSuperseded}

func (s *Server) addEnvironmentSeries(ctx context.Context, out *promFamilies, env exportedEnvironment, running map[int64][]managedContainer, now time.Time) error {
	base := []promLabel{
		{"deployment_id", strconv.FormatInt(env.ProjectID, 10)},
		{"deployment", env.Project},
		{"environment", env.Environment},
	}
	release := ""
	if env.LiveReleaseID > 0 {
		release = strconv.FormatInt(env.ReleaseNumber, 10)
	}
	withRelease := func(more ...promLabel) []promLabel {
		labels := append(append([]promLabel{}, base...), promLabel{"release", release})
		return append(labels, more...)
	}
	out.add("info", promGauge, "The deployment, its environment and the number of the release live there, as labels; 1 always.",
		append(withRelease(), promLabel{"environment_id", strconv.FormatInt(env.EnvironmentID, 10)}, promLabel{"kind", env.Kind}), 1)

	// Run outcomes: every terminal run of this environment, by how it ended.
	counts, err := s.environmentRunCounts(ctx, env.EnvironmentID)
	if err != nil {
		return err
	}
	for _, result := range runResults {
		out.add("runs_total", promCounter, "Deployment runs of this environment that have ended, by result.",
			append(append([]promLabel{}, base...), promLabel{"result", string(result)}), float64(counts[result]))
	}

	// The live release: how old it is, and what its containers are doing.
	if env.LiveReleaseID > 0 && !env.ActivatedAt.IsZero() {
		out.add("release_activated_timestamp_seconds", promGauge, "When the live release went live, in Unix seconds: its readiness checks passing and the route switched to it.",
			withRelease(), float64(env.ActivatedAt.Unix()))
		out.add("release_age_seconds", promGauge, "Seconds since the live release went live.",
			withRelease(), now.Sub(env.ActivatedAt).Seconds())
	}
	if env.LiveReleaseID > 0 {
		if running != nil {
			up, total := 0, 0
			for _, c := range running[env.LiveReleaseID] {
				total++
				if c.State == "running" && c.Health != "unhealthy" {
					up++
				}
			}
			out.add("up", promGauge, "1 when at least one of the live release's containers is running and not failing its health check, as Docker reports it now; 0 otherwise. Absent when Docker did not answer.",
				withRelease(), boolValue(up > 0))
			out.add("containers_running", promGauge, "The live release's containers Docker reports running.", withRelease(), float64(up))
			out.add("containers", promGauge, "The live release's containers Docker lists, running or not.", withRelease(), float64(total))
		}
		if err := s.addContainerSamples(ctx, out, env, withRelease, running, now); err != nil {
			return err
		}
		events, err := s.modules.deployEvents.ReleaseCounts(ctx, env.EnvironmentID, env.LiveReleaseID)
		if err != nil {
			return err
		}
		out.add("container_restarts_total", promCounter, "Starts of the live release's containers after their first, within the events' retention: a restart policy firing, a docker restart, a start after a stop.",
			withRelease(), float64(events.Restarts))
		out.add("container_oom_kills_total", promCounter, "Times the kernel's OOM killer stopped one of the live release's containers, within the events' retention.",
			withRelease(), float64(events.OOMKills))
		for _, code := range sortedLabelKeys(events.Exits) {
			out.add("container_exits_total", promCounter, "Exits of the live release's containers by status, within the events' retention; 0 is a clean exit.",
				withRelease(promLabel{"exit_code", code}), float64(events.Exits[code]))
		}
	}

	// What the ingress served in the last hour, from the request record.
	s.addRequestSeries(ctx, out, env, base, now)

	// The warm release's window, where one is or was.
	if watch, err := s.modules.deployRuns.LatestRollbackWatch(ctx, env.EnvironmentID); err == nil {
		for _, state := range []string{deploy.RollbackWatchWatching, deploy.RollbackWatchRollingBack, deploy.RollbackWatchRolledBack, deploy.RollbackWatchClosing, deploy.RollbackWatchClosed} {
			out.add("rollback_watch_state", promGauge, "The newest warm-release window's state, 1 on the series naming it: watching (the previous release kept warm while the new one is judged), rolling_back (switching back to it), rolled_back (switched back), closing or closed (the new release kept).",
				append(append([]promLabel{}, base...), promLabel{"state", state}), boolValue(watch.State == state))
		}
	} else if !errors.Is(err, deploy.ErrNoRollbackWatch) {
		return err
	}

	// The maintenance switch beside the route.
	if state, ok := s.maintenanceSwitch(ctx, env, now); ok && state.Route {
		out.add("maintenance_on", promGauge, "1 while the environment's maintenance page is switched on for its visitors, 0 while the application is served.",
			base, boolValue(state.On))
	}

	// How much of the time it was up.
	availability := s.deploymentAvailability(ctx, env.EnvironmentID, now)
	for _, window := range availability.Windows {
		labels := append(append([]promLabel{}, base...), promLabel{"window", window.Window})
		if window.Ratio != nil {
			out.add("availability_ratio", promGauge, "The share of the window's observed time the live release was running and healthy, 0 to 1, from the releases' live spans and the persisted container events. Absent while nothing was observed.",
				labels, *window.Ratio)
			out.add("availability_downtime_seconds", promGauge, "Seconds of the window the live release was down: exited, killed, paused or failing its health check, until it came back.",
				labels, float64(window.DownSeconds))
		}
		if window.Seconds > 0 {
			out.add("availability_coverage_ratio", promGauge, "The share of the window the container event record reaches back over, 0 to 1: less than 1 while the record is younger than the window.",
				labels, float64(window.CoverageSeconds)/float64(window.Seconds))
		}
	}
	return nil
}

func (s *Server) environmentRunCounts(ctx context.Context, environmentID int64) (map[deploy.RunState]int, error) {
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT state, COUNT(*) FROM deploy_runs WHERE environment_id = ? GROUP BY state`, environmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[deploy.RunState]int{}
	for rows.Next() {
		var state string
		var n int
		if err := rows.Scan(&state, &n); err != nil {
			return nil, err
		}
		counts[deploy.RunState(state)] = n
	}
	return counts, rows.Err()
}

// addContainerSamples exports the recorder's newest reading of each of the
// live release's containers. The containers are the ones the event record
// saw start for the release, joined with whatever Docker lists now, so a
// release whose start the record missed is still read once Docker answers.
func (s *Server) addContainerSamples(ctx context.Context, out *promFamilies, env exportedEnvironment, withRelease func(...promLabel) []promLabel, running map[int64][]managedContainer, now time.Time) error {
	refs, err := s.modules.deployEvents.ReleaseContainers(ctx, env.EnvironmentID, env.LiveReleaseID)
	if err != nil {
		return err
	}
	names := map[string]bool{}
	for _, ref := range refs {
		names[ref.Name] = true
	}
	for _, c := range running[env.LiveReleaseID] {
		names[c.Name] = true
	}
	stale := time.Duration(deploymentSampleStaleIntervals) * s.metricsInterval()
	for _, name := range sortedLabelKeys(names) {
		var ts int64
		var memBytes, memLimit int64
		var memLimited sql.NullInt64
		var cpuTotal sql.NullInt64
		err := s.Store.DB.QueryRowContext(ctx, `SELECT ts, mem_bytes, mem_limit, mem_limited, cpu_total FROM metric_container_samples
			WHERE name = ? ORDER BY ts DESC LIMIT 1`, name).Scan(&ts, &memBytes, &memLimit, &memLimited, &cpuTotal)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		sampled := time.Unix(ts, 0)
		labels := withRelease(promLabel{"container", name})
		out.add("container_last_sample_timestamp_seconds", promGauge, "When the recorder last sampled this container of the live release, in Unix seconds.",
			labels, float64(sampled.Unix()))
		// What the last reading said is only worth exporting while it is
		// news: a scraper would otherwise chart an hour-old figure as now.
		if now.Sub(sampled) > stale {
			continue
		}
		if cpuTotal.Valid && cpuTotal.Int64 > 0 {
			out.add("container_cpu_seconds_total", promCounter, "Processor time the container has used since it started, in seconds, from Docker's cumulative counter at the recorder's newest sample.",
				labels, float64(cpuTotal.Int64)/1e9)
		}
		out.add("container_memory_bytes", promGauge, "Memory the container held at the recorder's newest sample.", labels, float64(memBytes))
		if memLimited.Valid && memLimited.Int64 == 1 && memLimit > 0 {
			out.add("container_memory_limit_bytes", promGauge, "The memory limit the container was given, where one was.", labels, float64(memLimit))
		}
	}
	return nil
}

func (s *Server) metricsInterval() time.Duration {
	if s.Cfg != nil && s.Cfg.MetricsInterval > 0 {
		return s.Cfg.MetricsInterval
	}
	return metrics.DefaultInterval
}

// addRequestSeries exports the last hour at the ingress: how many requests
// by status class, and how slow the slow ones were where the proxy records a
// duration (Caddy does; nginx's stock format does not, and a column of zeros
// is not exported as latency).
func (s *Server) addRequestSeries(ctx context.Context, out *promFamilies, env exportedEnvironment, base []promLabel, now time.Time) {
	if s.modules.requests == nil {
		return
	}
	window, err := s.modules.requests.Cached(ctx, deploy.RouteNameFor(env.EnvironmentID),
		accesslog.Filter{Since: now.Add(-deploymentRequestWindow), Until: now, Limit: 1}, deploymentScrapeCacheFor)
	if err != nil || !window.Coverage.Exists {
		return
	}
	summary := window.Result.Summary
	for _, class := range accesslog.Classes {
		out.add("requests_last_hour", promGauge, "Requests the ingress answered for this environment in the last hour, by status class, from its access log.",
			append(append([]promLabel{}, base...), promLabel{"class", class}), float64(summary.Classes[class]))
	}
	out.add("request_error_ratio_last_hour", promGauge, "The share of the last hour's requests answered 5xx, 0 to 1.", base, summary.ErrorRate)
	if window.Facts.Latency && summary.Latency != nil {
		for quantile, ms := range map[string]float64{"0.5": summary.Latency.P50, "0.95": summary.Latency.P95, "0.99": summary.Latency.P99} {
			out.add("request_latency_seconds", promGauge, "Request duration over the last hour at the quantile named, in seconds, where the ingress records one.",
				append(append([]promLabel{}, base...), promLabel{"quantile", quantile}), ms/1000)
		}
	}
}

// maintenanceSwitch reads where the environment's switch stands, at most
// once a minute per environment.
func (s *Server) maintenanceSwitch(ctx context.Context, env exportedEnvironment, now time.Time) (proxysvc.MaintenanceSwitch, bool) {
	if s.modules.proxy == nil || s.modules.deployPlanning == nil {
		return proxysvc.MaintenanceSwitch{}, false
	}
	s.deployScrape.mu.Lock()
	cached, ok := s.deployScrape.maintenance[env.EnvironmentID]
	s.deployScrape.mu.Unlock()
	if ok && now.Sub(cached.at) < deploymentScrapeCacheFor {
		return cached.state, cached.ok
	}
	name, err := s.modules.deployPlanning.EnvironmentRouteName(ctx, env.ProjectID, env.EnvironmentID)
	var state proxysvc.MaintenanceSwitch
	read := err == nil
	if read {
		readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		state, err = s.modules.proxy.DeploymentMaintenance(readCtx, name)
		cancel()
		read = err == nil
	}
	s.deployScrape.mu.Lock()
	if s.deployScrape.maintenance == nil {
		s.deployScrape.maintenance = map[int64]cachedSwitch{}
	}
	s.deployScrape.maintenance[env.EnvironmentID] = cachedSwitch{at: now, state: state, ok: read}
	s.deployScrape.mu.Unlock()
	return state, read
}

// sortedKeys is the map's keys in order, so the exposition is the same text
// scrape after scrape.
func sortedLabelKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
