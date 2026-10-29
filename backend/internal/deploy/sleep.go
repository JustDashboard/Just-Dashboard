package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// Scale to zero. An environment whose plan says so (runtime.sleep) has its
// live release's container stopped once its route has served no request for
// the plan's idle minutes — stopped, not removed: the image, the volumes and
// the network stay — and started again by the next request. The route keeps
// answering throughout (proxysvc/deployment_sleep.go): a request that finds
// the application asleep is held by the proxy while the dashboard starts the
// container and waits for its readiness, and is then sent on as it came, so
// the visitor gets the page after a pause rather than an error. One that
// outlasts what the proxy holds a visitor for gets the waking page, which
// tries again by itself.
//
// The state is rows (deploy_sleep_spans): an open row is the environment
// asleep, with when and why; a closed one is a stretch of its history, with
// what woke it and how long the start took. A restarted dashboard resumes
// from them: an open row whose container is running again is closed, one
// whose release is no longer live is closed, and the rest stay asleep until
// a request comes.

// SleepPlan is the plan's scale-to-zero terms.
type SleepPlan struct {
	Enabled bool `json:"enabled"`
	// IdleMinutes is how long the route serves no request before the
	// container is stopped; 0 is DefaultSleepIdleMinutes.
	IdleMinutes int `json:"idleMinutes,omitempty"`
	// WakeTimeoutSeconds bounds a wake: the start and the readiness checks
	// after it. 0 is DefaultWakeTimeoutSeconds.
	WakeTimeoutSeconds int `json:"wakeTimeoutSeconds,omitempty"`
}

const (
	DefaultSleepIdleMinutes   = 15
	MinSleepIdleMinutes       = 5
	MaxSleepIdleMinutes       = 1440
	DefaultWakeTimeoutSeconds = 60
	MinWakeTimeoutSeconds     = 5
	MaxWakeTimeoutSeconds     = 600
	// maxWakeHoldSeconds is the longest a visitor's request is held while the
	// application starts. Past it the waking page answers and tries again:
	// a page that loads for longer than half a minute reads as broken, and
	// a CDN or load balancer in front gives up around then too.
	maxWakeHoldSeconds = 30
)

func (p *SleepPlan) on() bool { return p != nil && p.Enabled }

func (p *SleepPlan) idleMinutes() int {
	if p == nil || p.IdleMinutes <= 0 {
		return DefaultSleepIdleMinutes
	}
	return p.IdleMinutes
}

func (p *SleepPlan) idle() time.Duration { return time.Duration(p.idleMinutes()) * time.Minute }

func (p *SleepPlan) wakeTimeoutSeconds() int {
	if p == nil || p.WakeTimeoutSeconds <= 0 {
		return DefaultWakeTimeoutSeconds
	}
	return p.WakeTimeoutSeconds
}

func (p *SleepPlan) wakeTimeout() time.Duration {
	return time.Duration(p.wakeTimeoutSeconds()) * time.Second
}

// holdSeconds is how long the proxy holds a request while the application
// starts: the whole wake, up to what a visitor waits for.
func (p *SleepPlan) holdSeconds() int { return min(p.wakeTimeoutSeconds(), maxWakeHoldSeconds) }

// validateSleepPlan holds the terms to their bounds and refuses sleep where
// stopping the container would break something a request cannot bring back.
func validateSleepPlan(c PlanConfiguration) error {
	plan := c.Runtime.Sleep
	if plan == nil {
		return nil
	}
	if plan.IdleMinutes != 0 && (plan.IdleMinutes < MinSleepIdleMinutes || plan.IdleMinutes > MaxSleepIdleMinutes) {
		return invalidField("runtime.sleep.idleMinutes", "an environment sleeps after %d to %d idle minutes, or zero for %d", MinSleepIdleMinutes, MaxSleepIdleMinutes, DefaultSleepIdleMinutes)
	}
	if plan.WakeTimeoutSeconds != 0 && (plan.WakeTimeoutSeconds < MinWakeTimeoutSeconds || plan.WakeTimeoutSeconds > MaxWakeTimeoutSeconds) {
		return invalidField("runtime.sleep.wakeTimeoutSeconds", "a wake waits %d to %d seconds for readiness, or zero for %d", MinWakeTimeoutSeconds, MaxWakeTimeoutSeconds, DefaultWakeTimeoutSeconds)
	}
	if !plan.Enabled {
		return nil
	}
	if reason := sleepRefusal(c); reason != "" {
		return invalidField("runtime.sleep.enabled", "%s", reason)
	}
	return nil
}

// sleepRefusal says why this plan cannot sleep, or "" when it can.
func sleepRefusal(c PlanConfiguration) string {
	switch {
	case c.Build.Method == BuildCompose || c.Build.Method == BuildLegacyCompose:
		return "a Compose stack is several containers that start in an order of their own; sleeping stops one container, so it is offered for a single-container release"
	case c.Runtime.HostNetwork:
		return "a host-network application is reached at the host's own port, not through the proxy's route, so no request could wake it"
	case c.Runtime.Tailnet != nil:
		return "a tailnet-only environment is reached through tailscale serve, not through the proxy's route, so no request could wake it"
	case len(servedDomains(c.Domains)) == 0:
		return "only a request through the proxy's route wakes a sleeping application, and no domain serves this one; add a domain it serves"
	}
	for _, check := range c.Checks {
		if check.Phase != "readiness" || !check.Required {
			continue
		}
		switch CheckKind(check.Kind) {
		case CheckDockerHealth:
			return fmt.Sprintf("the required readiness check %q reads Docker's health verdict, which a started container is given only after its image's HEALTHCHECK interval — 30 seconds unless the image says otherwise — and a stopped one has none, so every wake would outlast what a visitor waits for; use an HTTP or TCP readiness check", check.Name)
		case CheckPublicRoute:
			return fmt.Sprintf("the required readiness check %q asks the public route, which while the application sleeps answers by waking it, so the wake would wait on itself; use an HTTP or TCP readiness check", check.Name)
		}
	}
	return ""
}

// canonicalSleepPlan is the plan's sleep stored as nothing when it asks for
// nothing, so a switch turned on and off is no change.
func canonicalSleepPlan(plan *SleepPlan) *SleepPlan {
	if plan == nil || (!plan.Enabled && plan.IdleMinutes == 0 && plan.WakeTimeoutSeconds == 0) {
		return nil
	}
	copied := *plan
	return &copied
}

// sleepLabel is the plan's terms in one line, for the release comparison and
// the preflight finding.
func sleepLabel(plan *SleepPlan) string {
	if !plan.on() {
		return "off"
	}
	return fmt.Sprintf("after %d idle minutes, woken within %d s", plan.idleMinutes(), plan.wakeTimeoutSeconds())
}

// sleepFindings is preflight's word on sleep: what it will do, and that the
// route it depends on records the requests the idle minutes are counted from.
func sleepFindings(configuration PlanConfiguration, observation HostObservation) []PreflightFinding {
	plan := configuration.Runtime.Sleep
	if !plan.on() {
		return nil
	}
	served := servedDomains(configuration.Domains)
	hostnames := make([]string, 0, len(served))
	for _, domain := range served {
		hostnames = append(hostnames, domain.Hostname)
		for _, observed := range observation.Domains {
			if strings.EqualFold(observed.Hostname, domain.Hostname) && !observed.ProxyAvailable {
				return []PreflightFinding{finding("sleep_without_record", PreflightBlocked,
					"Sleeping needs the proxy to record "+domain.Hostname+"'s requests", sleepLabel(plan),
					"The idle minutes are counted from the requests the public route's proxy records, and a request through it is what wakes the application. No proxy is available for this name, so it would sleep and never wake.",
					"Open Proxy to check the ingress, or turn sleeping off.", "proxy", "runtime.sleep.enabled")}
			}
		}
	}
	return []PreflightFinding{finding("sleep_after_idle", PreflightPass,
		fmt.Sprintf("The container is stopped after %d minutes without a request and started by the next one", plan.idleMinutes()), sleepLabel(plan),
		"The proxy records every request "+strings.Join(hostnames, ", ")+" serves, and the idle minutes are counted from that record. Asleep, the container is stopped, not removed; a request holds for up to "+
			strconv.Itoa(plan.holdSeconds())+" s while it starts and passes its readiness checks, then reaches it as sent, and one that waits longer gets a waking page that tries again.",
		"", "deploy", "runtime.sleep.enabled")}
}

// --- the record -------------------------------------------------------------

// SleepSpan is one stretch an environment spent asleep. WokeAt is nil while
// it is asleep now.
type SleepSpan struct {
	ID            int64      `json:"id"`
	EnvironmentID int64      `json:"environmentId"`
	ReleaseID     int64      `json:"releaseId"`
	ContainerID   string     `json:"containerId,omitempty"`
	IdleMinutes   int        `json:"idleMinutes"`
	Reason        string     `json:"reason"`
	LastRequestAt *time.Time `json:"lastRequestAt,omitempty"`
	SleptAt       time.Time  `json:"sleptAt"`
	WokeAt        *time.Time `json:"wokeAt,omitempty"`
	// WakeTrigger is what woke it: a request, Wake now (manual), a run, or
	// the dashboard finding it started outside a wake (repair).
	WakeTrigger string  `json:"wakeTrigger,omitempty"`
	WakeDetail  string  `json:"wakeDetail,omitempty"`
	WakeSeconds float64 `json:"wakeSeconds,omitempty"`
	WakeError   string  `json:"wakeError,omitempty"`
}

// Wake triggers.
const (
	WakeByRequest = "request"
	WakeByHand    = "manual"
	WakeByRun     = "run"
	WakeByRepair  = "repair"
)

// ErrAlreadyAsleep refuses a second open stretch for one environment.
var ErrAlreadyAsleep = errors.New("the environment is already asleep")

const sleepSpanColumns = `id, environment_id, release_id, container_id, idle_minutes, reason, last_request_at, slept_at, woke_at, wake_trigger, wake_detail, wake_seconds, wake_error`

func scanSleepSpan(row interface{ Scan(...any) error }) (*SleepSpan, error) {
	var span SleepSpan
	var lastRequest, slept, woke int64
	if err := row.Scan(&span.ID, &span.EnvironmentID, &span.ReleaseID, &span.ContainerID, &span.IdleMinutes, &span.Reason,
		&lastRequest, &slept, &woke, &span.WakeTrigger, &span.WakeDetail, &span.WakeSeconds, &span.WakeError); err != nil {
		return nil, err
	}
	span.SleptAt = time.Unix(slept, 0).UTC()
	if lastRequest > 0 {
		at := time.Unix(lastRequest, 0).UTC()
		span.LastRequestAt = &at
	}
	if woke > 0 {
		at := time.Unix(woke, 0).UTC()
		span.WokeAt = &at
	}
	return &span, nil
}

// OpenSleepSpan is the environment's stretch asleep now, or nil.
func (s *OrchestrationStore) OpenSleepSpan(ctx context.Context, environmentID int64) (*SleepSpan, error) {
	span, err := scanSleepSpan(s.db.QueryRowContext(ctx, `SELECT `+sleepSpanColumns+` FROM deploy_sleep_spans WHERE environment_id = ? AND woke_at = 0`, environmentID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return span, err
}

// SleepSpans are the environment's stretches asleep, newest first, that
// ended after since or are still open.
func (s *OrchestrationStore) SleepSpans(ctx context.Context, environmentID int64, since time.Time, limit int) ([]SleepSpan, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+sleepSpanColumns+` FROM deploy_sleep_spans
		WHERE environment_id = ? AND (woke_at = 0 OR woke_at >= ?) ORDER BY id DESC LIMIT ?`, environmentID, since.Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	spans := []SleepSpan{}
	for rows.Next() {
		span, err := scanSleepSpan(rows)
		if err != nil {
			return nil, err
		}
		spans = append(spans, *span)
	}
	return spans, rows.Err()
}

func (s *OrchestrationStore) openSleepSpan(ctx context.Context, span SleepSpan) (SleepSpan, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	last := int64(0)
	if span.LastRequestAt != nil {
		last = span.LastRequestAt.Unix()
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO deploy_sleep_spans(environment_id, release_id, container_id, idle_minutes, reason, last_request_at, slept_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)`, span.EnvironmentID, span.ReleaseID, span.ContainerID, span.IdleMinutes, span.Reason, last, span.SleptAt.Unix())
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return span, ErrAlreadyAsleep
		}
		return span, err
	}
	span.ID, _ = result.LastInsertId()
	return span, nil
}

func (s *OrchestrationStore) closeSleepSpan(ctx context.Context, id int64, at time.Time, trigger, detail string, seconds float64, failure string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	woke := at.Unix()
	if woke <= 0 {
		woke = 1
	}
	_, err := s.db.ExecContext(ctx, `UPDATE deploy_sleep_spans SET woke_at = ?, wake_trigger = ?, wake_detail = ?, wake_seconds = ?, wake_error = ?
		WHERE id = ? AND woke_at = 0`, woke, trigger, detail, seconds, failure, id)
	return err
}

// --- the sleeper ------------------------------------------------------------

// SleepEnvironment names an environment the way the audit and the lifecycle
// feed name it: a deployment is audited against its project's name.
type SleepEnvironment struct {
	ProjectID     int64  `json:"projectId"`
	EnvironmentID int64  `json:"environmentId"`
	Project       string `json:"project"`
}

// SleepAudit records what the sleeper did on nobody's request — the idle
// decision, putting an environment to sleep, a wake — and a wake somebody
// asked for, under the actor given.
type SleepAudit func(ctx context.Context, action string, environment SleepEnvironment, actor string, detail map[string]any, success bool)

// SleepProxy is what sleeping asks of the proxy: the switch beside the
// route, the route brought up to a started container's address, and where
// the wake is reached.
type SleepProxy interface {
	DeploymentSleeping(context.Context, string) (proxysvc.SleepSwitch, error)
	SetDeploymentSleeping(context.Context, string, bool) (proxysvc.SleepSwitch, error)
	RefreshDeploymentRoute(context.Context, string) (string, error)
	WakeSockets(context.Context) ([]proxysvc.WakeSocket, error)
	WakeSocketReached(context.Context, proxysvc.WakeSocket) bool
}

// Sleeper puts idle environments to sleep and wakes them.
type Sleeper struct {
	executor *NormalizedStepExecutor
	record   RequestRecord
	audit    SleepAudit
	now      func() time.Time
	interval time.Duration
	log      *slog.Logger

	mu      sync.Mutex
	locks   map[int64]*sync.Mutex
	flights map[int64]*wakeFlight
	// decided is the last pass's word on each environment that sleeps, for
	// the overview to say why one is awake.
	decided map[int64]SleepDecision
	// plans caches each release's sleep terms: a release's snapshot never
	// changes, and the pass reads every live release once a minute.
	plans map[int64]*SleepPlan

	sockets *wakeSockets
}

// SleepDecision is what the last pass made of an environment that sleeps.
type SleepDecision struct {
	At     time.Time `json:"at"`
	Asleep bool      `json:"asleep"`
	Reason string    `json:"reason"`
	// LastRequestAt is the newest request the record held, if it held any
	// since the release went live.
	LastRequestAt *time.Time `json:"lastRequestAt,omitempty"`
	// SleepsAt is when the environment goes to sleep if nothing arrives.
	SleepsAt *time.Time `json:"sleepsAt,omitempty"`
}

func NewSleeper(executor *NormalizedStepExecutor, record RequestRecord, audit SleepAudit, log *slog.Logger) *Sleeper {
	if log == nil {
		log = slog.Default()
	}
	s := &Sleeper{
		executor: executor, record: record, audit: audit, now: time.Now, interval: time.Minute, log: log,
		locks: map[int64]*sync.Mutex{}, flights: map[int64]*wakeFlight{}, decided: map[int64]SleepDecision{}, plans: map[int64]*SleepPlan{},
	}
	s.sockets = newWakeSockets(s)
	if executor != nil {
		executor.sleeper = s
	}
	return s
}

// WithInterval sets how often environments are looked at. The default is a
// minute: the idle line is minutes long, and a pass reads every live
// release's record.
func (s *Sleeper) WithInterval(interval time.Duration) *Sleeper {
	if interval > 0 {
		s.interval = interval
	}
	return s
}

// WithClock replaces the clock the idle minutes are measured by.
func (s *Sleeper) WithClock(now func() time.Time) *Sleeper {
	if now != nil {
		s.now = now
	}
	return s
}

// Start runs the pass until the context ends, and the wake sockets with it.
func (s *Sleeper) Start(ctx context.Context) {
	if s == nil || s.executor == nil || s.executor.store == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			pass, cancel := context.WithTimeout(ctx, 5*time.Minute)
			s.sockets.ensure(pass)
			s.Tick(pass)
			cancel()
			select {
			case <-ctx.Done():
				s.sockets.close()
				return
			case <-ticker.C:
			}
		}
	}()
}

func (s *Sleeper) environmentLock(environmentID int64) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, ok := s.locks[environmentID]
	if !ok {
		lock = &sync.Mutex{}
		s.locks[environmentID] = lock
	}
	return lock
}

func (s *Sleeper) proxy() SleepProxy {
	if s.executor == nil {
		return nil
	}
	proxy, _ := s.executor.proxy.(SleepProxy)
	return proxy
}

func (s *Sleeper) warmer() RuntimeWarmer {
	if s.executor == nil {
		return nil
	}
	warmer, _ := s.executor.runtime.(RuntimeWarmer)
	return warmer
}

// sleepCandidate is an environment with a live release.
type sleepCandidate struct {
	SleepEnvironment
	Kind      EnvironmentKind
	ReleaseID int64
}

func (s *Sleeper) candidates(ctx context.Context) ([]sleepCandidate, error) {
	rows, err := s.executor.store.db.QueryContext(ctx, `SELECT e.id, e.project_id, p.name, e.kind, e.live_release_id
		FROM deploy_environments e JOIN deploy_projects p ON p.id = e.project_id
		WHERE e.archived_at = 0 AND p.archived_at = 0 AND e.live_release_id > 0 ORDER BY e.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []sleepCandidate{}
	for rows.Next() {
		var c sleepCandidate
		if err := rows.Scan(&c.EnvironmentID, &c.ProjectID, &c.Project, &c.Kind, &c.ReleaseID); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Sleeper) environment(ctx context.Context, environmentID int64) (sleepCandidate, error) {
	var c sleepCandidate
	err := s.executor.store.db.QueryRowContext(ctx, `SELECT e.id, e.project_id, p.name, e.kind, e.live_release_id
		FROM deploy_environments e JOIN deploy_projects p ON p.id = e.project_id WHERE e.id = ?`, environmentID).
		Scan(&c.EnvironmentID, &c.ProjectID, &c.Project, &c.Kind, &c.ReleaseID)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrEnvironmentNotFound
	}
	return c, err
}

// planFor is a release's sleep terms, nil for a release that never sleeps.
func (s *Sleeper) planFor(ctx context.Context, releaseID int64) (*SleepPlan, runtimeReleaseSnapshot, error) {
	_, snapshot, err := s.executor.releaseSnapshot(ctx, releaseID)
	if err != nil {
		return nil, snapshot, err
	}
	s.mu.Lock()
	s.plans[releaseID] = canonicalSleepPlan(snapshot.Plan.Sleep)
	s.mu.Unlock()
	return snapshot.Plan.Sleep, snapshot, nil
}

func (s *Sleeper) cachedPlan(ctx context.Context, releaseID int64) *SleepPlan {
	s.mu.Lock()
	plan, ok := s.plans[releaseID]
	s.mu.Unlock()
	if ok {
		return plan
	}
	plan, _, _ = s.planFor(ctx, releaseID)
	return plan
}

func (s *Sleeper) decide(environmentID int64, decision SleepDecision) {
	s.mu.Lock()
	s.decided[environmentID] = decision
	s.mu.Unlock()
}

// Decision is the last pass's word on an environment, if it sleeps.
func (s *Sleeper) Decision(environmentID int64) (SleepDecision, bool) {
	if s == nil {
		return SleepDecision{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	decision, ok := s.decided[environmentID]
	return decision, ok
}

// Tick is one pass over every environment with a live release.
func (s *Sleeper) Tick(ctx context.Context) {
	candidates, err := s.candidates(ctx)
	if err != nil {
		s.log.Warn("environments that sleep could not be read", "error", err)
		return
	}
	for _, candidate := range candidates {
		if ctx.Err() != nil {
			return
		}
		plan := s.cachedPlan(ctx, candidate.ReleaseID)
		span, err := s.executor.store.OpenSleepSpan(ctx, candidate.EnvironmentID)
		if err != nil {
			s.log.Warn("an environment's sleep could not be read", "environment", candidate.EnvironmentID, "error", err)
			continue
		}
		if span != nil {
			s.reconcileAsleep(ctx, candidate, *span)
			continue
		}
		if !plan.on() {
			continue
		}
		s.consider(ctx, candidate, plan)
	}
}

// reconcileAsleep closes a stretch the environment is no longer in: its
// release is no longer the live one, or its container is running again —
// started by hand, or by a Docker restart policy after a daemon restart.
func (s *Sleeper) reconcileAsleep(ctx context.Context, candidate sleepCandidate, span SleepSpan) {
	lock := s.environmentLock(candidate.EnvironmentID)
	lock.Lock()
	defer lock.Unlock()
	route := deploymentRouteName(candidate.EnvironmentID)
	current, err := s.executor.store.OpenSleepSpan(ctx, candidate.EnvironmentID)
	if err != nil || current == nil || current.ID != span.ID {
		return
	}
	reason := ""
	if span.ReleaseID != candidate.ReleaseID {
		reason = "Release " + strconv.FormatInt(span.ReleaseID, 10) + " was asleep and is no longer live."
	} else if runtime, err := s.executor.store.RuntimeForRelease(ctx, span.ReleaseID); err == nil && s.warmer() != nil {
		if state, err := s.warmer().WarmState(ctx, *runtime); err == nil && state == "running" {
			reason = "Its container was started outside a wake."
		}
	}
	if reason == "" {
		s.decide(candidate.EnvironmentID, SleepDecision{At: s.now().UTC(), Asleep: true, Reason: span.Reason, LastRequestAt: span.LastRequestAt})
		return
	}
	if proxy := s.proxy(); proxy != nil {
		if _, err := proxy.SetDeploymentSleeping(ctx, route, false); err != nil {
			s.log.Warn("a sleeping switch could not be cleared", "route", route, "error", err)
			return
		}
	}
	if err := s.executor.store.closeSleepSpan(ctx, span.ID, s.now().UTC(), WakeByRepair, reason, 0, ""); err != nil {
		s.log.Warn("a stretch asleep could not be closed", "environment", candidate.EnvironmentID, "error", err)
		return
	}
	s.audited(ctx, "deploy.environment.wake", candidate.SleepEnvironment, "system", map[string]any{
		"releaseId": span.ReleaseID, "trigger": WakeByRepair, "reason": reason,
	}, true)
}

// consider is the idle decision for an awake environment whose release
// sleeps: asleep after the plan's idle minutes with no request, unless
// something says not now.
func (s *Sleeper) consider(ctx context.Context, candidate sleepCandidate, plan *SleepPlan) {
	now := s.now().UTC()
	awake := func(reason string, last *time.Time, sleepsAt *time.Time) {
		s.decide(candidate.EnvironmentID, SleepDecision{At: now, Reason: reason, LastRequestAt: last, SleepsAt: sleepsAt})
	}
	if candidate.Kind == EnvironmentPreview {
		awake("A preview never sleeps: it is reached at its own address, not through the route a request wakes.", nil, nil)
		return
	}
	if reason := s.notNow(ctx, candidate); reason != "" {
		awake(reason, nil, nil)
		return
	}
	release, err := s.executor.store.Release(ctx, candidate.ReleaseID)
	if err != nil || release.Release.ActivatedAt == nil {
		awake("The live release's record could not be read.", nil, nil)
		return
	}
	route := deploymentRouteName(candidate.EnvironmentID)
	proxy := s.proxy()
	if proxy == nil || s.record == nil {
		awake("This host has no proxy that records requests, so nothing is measured.", nil, nil)
		return
	}
	if !s.sockets.serving() {
		awake("The wake is not listening for this host's proxy yet, so a stopped application could not be woken.", nil, nil)
		return
	}
	state, err := proxy.DeploymentSleeping(ctx, route)
	if err != nil {
		awake("The proxy could not be asked about the route.", nil, nil)
		return
	}
	if !state.Ready {
		awake("The live route does not wake the application. Deploy once with sleeping on, and it sleeps from then on.", nil, nil)
		return
	}
	// The idle minutes run from the latest of the release going live, its
	// last wake and its last request.
	from := *release.Release.ActivatedAt
	if spans, err := s.executor.store.SleepSpans(ctx, candidate.EnvironmentID, from, 1); err == nil && len(spans) > 0 && spans[0].WokeAt != nil && spans[0].WokeAt.After(from) {
		from = *spans[0].WokeAt
	}
	window, err := s.record.Window(ctx, route, accesslog.Filter{Since: from, Until: now, Limit: 1})
	if err != nil {
		awake("The request record could not be read, so the environment is not put to sleep blind.", nil, nil)
		return
	}
	var last *time.Time
	if window.Result != nil && window.Result.Summary.Total > 0 {
		// The newest request since; a record that cannot say when counts
		// as one this moment, which only puts sleep off.
		at := now
		if stamp := window.Result.Summary.Last; stamp != nil {
			if parsed, err := time.Parse(time.RFC3339Nano, *stamp); err == nil {
				at = parsed.UTC()
			}
		}
		last = &at
		if at.After(from) {
			from = at
		}
	}
	sleepsAt := from.Add(plan.idle())
	if now.Before(sleepsAt) {
		reason := fmt.Sprintf("Awake: it sleeps at %s unless a request arrives.", sleepsAt.Format("15:04 UTC"))
		awake(reason, last, &sleepsAt)
		return
	}
	reason := fmt.Sprintf("No request reached it for %d minutes", plan.idleMinutes())
	if last != nil {
		reason += "; the last was at " + last.Format("2006-01-02 15:04 UTC")
	} else {
		reason += " since release #" + strconv.FormatInt(release.Release.Number, 10) + " went live"
	}
	reason += "."
	if err := s.sleep(ctx, candidate, plan, reason, last); err != nil {
		s.log.Warn("an idle environment could not be put to sleep", "environment", candidate.EnvironmentID, "error", err)
		awake("It could not be put to sleep: "+err.Error(), last, nil)
	}
}

// notNow is a reason the environment stays awake whatever its record says:
// a run is about to change it, a warm release is being watched beside it, or
// its container is not running for a reason of its own.
func (s *Sleeper) notNow(ctx context.Context, candidate sleepCandidate) string {
	var active int
	if err := s.executor.store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deploy_runs WHERE environment_id = ? AND state NOT IN ('succeeded','failed','cancelled','rolled_back','superseded')`, candidate.EnvironmentID).Scan(&active); err != nil {
		return "Its runs could not be read."
	}
	if active > 0 {
		return "A run is queued or in progress."
	}
	if _, err := s.executor.store.OpenRollbackWatch(ctx, candidate.EnvironmentID); err == nil {
		return "The previous release is kept warm for an instant rollback; it sleeps once that window closes."
	}
	runtime, err := s.executor.store.RuntimeForRelease(ctx, candidate.ReleaseID)
	if err != nil {
		return "The live release has no runtime record."
	}
	if runtime.Kind != "container" {
		return "Only a single container sleeps."
	}
	if runtime.State != "live" {
		return "The live release was stopped by hand, so it is left as it is."
	}
	if warmer := s.warmer(); warmer != nil {
		if state, err := warmer.WarmState(ctx, *runtime); err != nil || state != "running" {
			return "Its container is not running, and only a running application is put to sleep."
		}
	}
	return ""
}

// sleep puts one environment to sleep: the stretch is recorded first, then
// the switch, then the container is stopped. In that order a dashboard that
// stops half-way leaves a stretch the next pass or the next request repairs,
// never a switch with no stretch behind it, which would refuse every visitor.
func (s *Sleeper) sleep(ctx context.Context, candidate sleepCandidate, plan *SleepPlan, reason string, lastRequest *time.Time) error {
	lock := s.environmentLock(candidate.EnvironmentID)
	lock.Lock()
	defer lock.Unlock()
	if again := s.notNow(ctx, candidate); again != "" {
		return errors.New(again)
	}
	runtime, err := s.executor.store.RuntimeForRelease(ctx, candidate.ReleaseID)
	if err != nil {
		return err
	}
	_, snapshot, err := s.executor.releaseSnapshot(ctx, candidate.ReleaseID)
	if err != nil {
		return err
	}
	route := deploymentRouteName(candidate.EnvironmentID)
	detail := map[string]any{
		"releaseId": candidate.ReleaseID, "idleMinutes": plan.idleMinutes(), "reason": reason,
	}
	if lastRequest != nil {
		detail["lastRequestAt"] = lastRequest.Format(time.RFC3339)
	}
	// The decision first: it is what the stop the lifecycle feed shows next
	// is attributed to.
	s.audited(ctx, "deploy.environment.idle", candidate.SleepEnvironment, "system", detail, true)
	span, err := s.executor.store.openSleepSpan(ctx, SleepSpan{
		EnvironmentID: candidate.EnvironmentID, ReleaseID: candidate.ReleaseID, ContainerID: runtime.RuntimeID,
		IdleMinutes: plan.idleMinutes(), Reason: reason, LastRequestAt: lastRequest, SleptAt: s.now().UTC(),
	})
	if err != nil {
		return err
	}
	fail := func(cause error) error {
		recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_, _ = s.proxy().SetDeploymentSleeping(recovery, route, false)
		_ = s.executor.store.closeSleepSpan(recovery, span.ID, s.now().UTC(), WakeByRepair, "it could not be put to sleep", 0, cause.Error())
		detail["error"] = cause.Error()
		s.audited(recovery, "deploy.environment.sleep", candidate.SleepEnvironment, "system", detail, false)
		return cause
	}
	if _, err := s.proxy().SetDeploymentSleeping(ctx, route, true); err != nil {
		return fail(err)
	}
	variables, err := s.executor.runtimeVariablesForRelease(ctx, candidate.ReleaseID, *runtime)
	if err != nil {
		return fail(err)
	}
	stop, err := s.executor.runtime.Stop(ctx, *runtime, snapshot.Plan, variables, false, nil)
	if err != nil {
		// Whatever the stop did, the container may still be serving: the
		// request goes to the wake, which finds it running and lets it on.
		return fail(err)
	}
	detail["stop"] = stop
	s.audited(ctx, "deploy.environment.sleep", candidate.SleepEnvironment, "system", detail, true)
	s.decide(candidate.EnvironmentID, SleepDecision{At: s.now().UTC(), Asleep: true, Reason: reason, LastRequestAt: lastRequest})
	return nil
}

func (s *Sleeper) audited(ctx context.Context, action string, environment SleepEnvironment, actor string, detail map[string]any, success bool) {
	if s.audit != nil {
		s.audit(ctx, action, environment, actor, detail, success)
	}
}

// --- waking -----------------------------------------------------------------

// WakeOutcome is what a wake came to, in the words the proxy's wake reads.
type WakeOutcome struct {
	// Status is proxysvc.WakeAwake, WakeStarting or WakeNothing.
	Status int `json:"status"`
	// Upstream is the address the application answers at, host:port, on a
	// proxy that needs telling.
	Upstream string `json:"upstream,omitempty"`
	// Reason says what happened, in words.
	Reason string `json:"reason"`
	// Seconds is how long the start took to answer.
	Seconds float64 `json:"seconds,omitempty"`
}

// WakeRequest is who or what asks for a wake.
type WakeRequest struct {
	Trigger string
	// Detail is the host a request asked for, or the run, or the person.
	Detail string
	Actor  string
}

// wakeFlight is one wake under way. answered closes as soon as its outcome
// is known — the application answering, at its address — so every request
// waiting on it goes on while the switch is cleared and the stretch closed.
type wakeFlight struct {
	answered chan struct{}
	// settled closes once the switch is cleared and the stretch closed.
	settled chan struct{}
	once    sync.Once
	outcome WakeOutcome
}

func (f *wakeFlight) answer(s *Sleeper, outcome WakeOutcome) {
	f.once.Do(func() {
		s.mu.Lock()
		f.outcome = outcome
		s.mu.Unlock()
		close(f.answered)
	})
}

// Wake brings an environment's application back, waiting up to hold for it.
// One wake runs per environment: every request that arrives while one is
// under way waits on that one. A wait that ends before the wake does answers
// WakeStarting, and the wake carries on to its own bound.
func (s *Sleeper) Wake(ctx context.Context, environmentID int64, request WakeRequest, hold time.Duration) WakeOutcome {
	s.mu.Lock()
	flight, running := s.flights[environmentID]
	if !running {
		flight = &wakeFlight{answered: make(chan struct{}), settled: make(chan struct{})}
		s.flights[environmentID] = flight
		go func() {
			outcome := s.wake(environmentID, request, flight)
			flight.answer(s, outcome)
			s.mu.Lock()
			delete(s.flights, environmentID)
			s.mu.Unlock()
			close(flight.settled)
		}()
	}
	s.mu.Unlock()
	timer := time.NewTimer(hold)
	defer timer.Stop()
	select {
	case <-flight.answered:
		s.mu.Lock()
		defer s.mu.Unlock()
		return flight.outcome
	case <-timer.C:
		return WakeOutcome{Status: proxysvc.WakeStarting, Reason: "It is still starting."}
	case <-ctx.Done():
		return WakeOutcome{Status: proxysvc.WakeStarting, Reason: "The request went away while it started."}
	}
}

// Settle waits for a wake under way to finish its bookkeeping — the switch
// cleared, the stretch closed — so a reading taken after it says awake.
func (s *Sleeper) Settle(ctx context.Context, environmentID int64) {
	s.mu.Lock()
	flight, running := s.flights[environmentID]
	s.mu.Unlock()
	if !running {
		return
	}
	select {
	case <-flight.settled:
	case <-ctx.Done():
	}
}

// wake does the work, on a context of its own: a visitor who gives up does
// not leave the application half started.
func (s *Sleeper) wake(environmentID int64, request WakeRequest, flight *wakeFlight) WakeOutcome {
	started := time.Now()
	candidate, err := s.environment(context.Background(), environmentID)
	if err != nil {
		return WakeOutcome{Status: proxysvc.WakeNothing, Reason: "The environment could not be read."}
	}
	plan := s.cachedPlan(context.Background(), candidate.ReleaseID)
	ctx, cancel := context.WithTimeout(context.Background(), plan.wakeTimeout()+30*time.Second)
	defer cancel()
	lock := s.environmentLock(environmentID)
	lock.Lock()
	defer lock.Unlock()
	route := deploymentRouteName(environmentID)
	proxy := s.proxy()
	span, err := s.executor.store.OpenSleepSpan(ctx, environmentID)
	if err != nil {
		return WakeOutcome{Status: proxysvc.WakeNothing, Reason: "Its sleep could not be read."}
	}
	runtime, runtimeErr := s.executor.store.RuntimeForRelease(ctx, candidate.ReleaseID)
	running := func() bool {
		if runtimeErr != nil || s.warmer() == nil {
			return false
		}
		state, err := s.warmer().WarmState(ctx, *runtime)
		return err == nil && state == "running"
	}
	if span == nil {
		// Nothing asleep. A request that met a switch an instant before a
		// wake removed it is let on to the application that answers now; one
		// the application itself failed is answered as it would have been.
		if running() {
			upstream := ""
			if proxy != nil {
				upstream, _ = proxy.RefreshDeploymentRoute(ctx, route)
			}
			return WakeOutcome{Status: proxysvc.WakeAwake, Upstream: upstream, Reason: "It is awake."}
		}
		return WakeOutcome{Status: proxysvc.WakeNothing, Reason: "It is not asleep; its container is down for another reason."}
	}
	finish := func(failure string) {
		if proxy != nil {
			if _, err := proxy.SetDeploymentSleeping(ctx, route, false); err != nil {
				s.log.Warn("a sleeping switch could not be cleared", "route", route, "error", err)
			}
		}
		answered := time.Since(started).Seconds()
		if flight != nil {
			select {
			case <-flight.answered:
				answered = flight.outcome.Seconds
			default:
			}
		}
		_ = s.executor.store.closeSleepSpan(ctx, span.ID, s.now().UTC(), request.Trigger, request.Detail, answered, failure)
	}
	detail := map[string]any{"releaseId": span.ReleaseID, "trigger": request.Trigger, "detail": request.Detail}
	actor := request.Actor
	if actor == "" {
		actor = "system"
	}
	if span.ReleaseID != candidate.ReleaseID || runtimeErr != nil {
		finish("the release that was asleep is no longer live")
		detail["error"] = "the release that was asleep is no longer live"
		s.audited(ctx, "deploy.environment.wake", candidate.SleepEnvironment, actor, detail, false)
		return WakeOutcome{Status: proxysvc.WakeNothing, Reason: "The release that was asleep is no longer live."}
	}
	if !running() {
		variables, err := s.executor.runtimeVariablesForRelease(ctx, candidate.ReleaseID, *runtime)
		if err == nil {
			err = s.executor.runtime.StartExisting(ctx, *runtime, variables, nil)
		}
		if err != nil {
			finish(err.Error())
			detail["error"] = err.Error()
			s.audited(ctx, "deploy.environment.wake", candidate.SleepEnvironment, actor, detail, false)
			return WakeOutcome{Status: proxysvc.WakeNothing, Reason: "Its container did not start: " + err.Error()}
		}
	}
	ready, why := s.waitReady(ctx, candidate.ReleaseID, *runtime, plan.wakeTimeout())
	upstream := ""
	if proxy != nil {
		upstream, err = proxy.RefreshDeploymentRoute(ctx, route)
		if err != nil {
			s.log.Warn("a woken route could not be brought to its container's address", "route", route, "error", err)
		}
	}
	seconds := time.Since(started).Seconds()
	reason := fmt.Sprintf("Woken in %.1f s.", seconds)
	if !ready {
		reason = "Started, but " + why
	}
	// Sent on whether or not the checks passed in time: the application is
	// running, and what it answers is the visitor's best answer now. Every
	// request held on this wake goes on at once; the switch and the record
	// follow, and a request that meets the switch in between is let on by
	// a wake that finds nothing asleep and the application up.
	outcome := WakeOutcome{Status: proxysvc.WakeAwake, Upstream: upstream, Reason: reason, Seconds: seconds}
	if flight != nil {
		flight.answer(s, outcome)
	}
	failure := ""
	if !ready {
		failure = why
	}
	finish(failure)
	detail["seconds"] = seconds
	if failure != "" {
		detail["error"] = failure
	}
	// A wake somebody pressed for is audited by the request that asked.
	if request.Trigger != WakeByHand {
		s.audited(ctx, "deploy.environment.wake", candidate.SleepEnvironment, actor, detail, ready)
	}
	s.decide(environmentID, SleepDecision{At: s.now().UTC(), Reason: reason})
	return outcome
}

// waitReady waits for the started container to pass the release's own
// readiness checks, or — for a release without one — to accept a
// connection on its port, until the bound.
func (s *Sleeper) waitReady(ctx context.Context, releaseID int64, runtime ReleaseRuntime, bound time.Duration) (bool, string) {
	ctx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()
	_, snapshot, err := s.executor.releaseSnapshot(ctx, releaseID)
	if err != nil {
		return false, "its release could not be read"
	}
	target := targetForRuntime(runtime, snapshot)
	// Each check one attempt at a time, every few tenths of a second: a
	// check's own interval is a second or more, and its first attempt at a
	// process still starting is a failure that would cost the visitor that
	// second.
	var checks []PlannedCheck
	for _, check := range snapshot.Checks {
		if check.Phase == "readiness" && check.Required && check.Kind != string(CheckPublicRoute) {
			checks = append(checks, singleAttempt(check))
		}
	}
	host := ""
	if served := servedDomains(snapshot.Domains); len(served) > 0 {
		host = served[0].Hostname
	}
	for {
		if len(checks) == 0 || s.executor.checks == nil {
			// No check of its own: the application answering HTTP at all.
			// A connection alone says nothing, since Docker's proxy holds the
			// published port whether or not anything listens behind it.
			if httpAnswers(ctx, target.Host, target.Port, host) {
				return true, ""
			}
		} else {
			passed := true
			for _, check := range checks {
				if evidence := s.executor.checks.Run(ctx, check, target); evidence.Outcome != HealthPassed {
					passed = false
					break
				}
			}
			if passed {
				return true, ""
			}
		}
		select {
		case <-ctx.Done():
			return false, fmt.Sprintf("it did not pass its readiness checks within %d s", int(bound.Seconds()))
		case <-time.After(150 * time.Millisecond):
		}
	}
}

// singleAttempt is a check asked once, for a wake that asks again itself.
func singleAttempt(check PlannedCheck) PlannedCheck {
	config, err := decodeCheckConfiguration(check.Config)
	if err != nil {
		return check
	}
	config.Attempts = 1
	if config.TimeoutSeconds == 0 || config.TimeoutSeconds > 5 {
		config.TimeoutSeconds = 5
	}
	raw, err := json.Marshal(config)
	if err != nil {
		return check
	}
	check.Config = raw
	return check
}

// httpAnswers says whether an HTTP application answers at host:port at all,
// whatever it answers.
func httpAnswers(ctx context.Context, host string, port int, name string) bool {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(host, strconv.Itoa(port))+"/", nil)
	if err != nil {
		return false
	}
	if name != "" {
		request.Host = name
	}
	client := http.Client{
		Timeout:       2 * time.Second,
		Transport:     &http.Transport{Proxy: nil, DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	response.Body.Close()
	return true
}

// Asleep is the environment's open stretch, or nil.
func (s *Sleeper) Asleep(ctx context.Context, environmentID int64) (*SleepSpan, error) {
	if s == nil || s.executor == nil {
		return nil, nil
	}
	return s.executor.store.OpenSleepSpan(ctx, environmentID)
}

// HoldFor is how long a request for the environment is held while it wakes.
func (s *Sleeper) HoldFor(ctx context.Context, environmentID int64) time.Duration {
	candidate, err := s.environment(ctx, environmentID)
	if err != nil {
		return time.Second
	}
	return time.Duration(s.cachedPlan(ctx, candidate.ReleaseID).holdSeconds()) * time.Second
}

// endSleep wakes an environment a run is starting on, so every run finds the
// live release as it expects it: running, routed and awake. A stop needs no
// container started to stop it, so its stretch is only closed.
func (e *NormalizedStepExecutor) endSleep(ctx context.Context, execution StepExecution) {
	if e.sleeper == nil || execution.Run.Operation == OperationScheduled || execution.Run.Operation == OperationPreviewRemove {
		return
	}
	span, err := e.store.OpenSleepSpan(ctx, execution.Run.EnvironmentID)
	if err != nil || span == nil {
		return
	}
	number := execution.Run.RunNumber
	if number == 0 {
		number = execution.Run.ID
	}
	detail := fmt.Sprintf("run #%d", number)
	if execution.Run.Operation == OperationStop {
		lock := e.sleeper.environmentLock(execution.Run.EnvironmentID)
		lock.Lock()
		defer lock.Unlock()
		if proxy := e.sleeper.proxy(); proxy != nil {
			_, _ = proxy.SetDeploymentSleeping(ctx, deploymentRouteName(execution.Run.EnvironmentID), false)
		}
		_ = e.store.closeSleepSpan(ctx, span.ID, e.sleeper.now().UTC(), WakeByRun, detail+" stopped it", 0, "")
		_ = stepLog(execution, "status", "The environment was asleep; this stop ends that without starting it")
		return
	}
	_ = stepLog(execution, "status", "The environment is asleep; waking it before this run")
	plan := e.sleeper.cachedPlan(ctx, span.ReleaseID)
	outcome := e.sleeper.Wake(ctx, execution.Run.EnvironmentID, WakeRequest{Trigger: WakeByRun, Detail: detail}, plan.wakeTimeout()+30*time.Second)
	// The run starts on a closed stretch, not one the wake is still closing.
	e.sleeper.Settle(ctx, execution.Run.EnvironmentID)
	_ = stepLog(execution, "status", outcome.Reason)
}

// SleepStatus is an environment's sleep as the overview reads it.
type SleepStatus struct {
	// Enabled is the live release's terms, which are the ones in force; a
	// change saved since goes live with the next release.
	Enabled            bool `json:"enabled"`
	IdleMinutes        int  `json:"idleMinutes"`
	WakeTimeoutSeconds int  `json:"wakeTimeoutSeconds"`
	HoldSeconds        int  `json:"holdSeconds"`
	// State is off, awake, asleep or waking.
	State string `json:"state"`
	// Since is when it went to sleep, while it is asleep.
	Since         *time.Time `json:"since,omitempty"`
	Reason        string     `json:"reason,omitempty"`
	LastRequestAt *time.Time `json:"lastRequestAt,omitempty"`
	SleepsAt      *time.Time `json:"sleepsAt,omitempty"`
	// Switch is the route's switch as the proxy reports it.
	Switch *proxysvc.SleepSwitch `json:"switch,omitempty"`
	// Recent are the stretches of the last seven days, newest first.
	Recent []SleepSpan `json:"recent"`
	// AsleepSeconds is how much of those seven days it spent asleep.
	AsleepSeconds int64 `json:"asleepSeconds"`
}

// Status reads an environment's sleep.
func (s *Sleeper) Status(ctx context.Context, environmentID int64) (SleepStatus, error) {
	out := SleepStatus{State: "off", Recent: []SleepSpan{}}
	candidate, err := s.environment(ctx, environmentID)
	if err != nil {
		return out, err
	}
	var plan *SleepPlan
	if candidate.ReleaseID > 0 {
		plan = s.cachedPlan(ctx, candidate.ReleaseID)
	}
	out.Enabled = plan.on()
	out.IdleMinutes, out.WakeTimeoutSeconds, out.HoldSeconds = plan.idleMinutes(), plan.wakeTimeoutSeconds(), plan.holdSeconds()
	now := s.now().UTC()
	spans, err := s.executor.store.SleepSpans(ctx, environmentID, now.Add(-7*24*time.Hour), 50)
	if err != nil {
		return out, err
	}
	out.Recent = spans
	for _, span := range spans {
		from, to := span.SleptAt, now
		if span.WokeAt != nil {
			to = *span.WokeAt
		}
		if limit := now.Add(-7 * 24 * time.Hour); from.Before(limit) {
			from = limit
		}
		if to.After(from) {
			out.AsleepSeconds += int64(to.Sub(from).Seconds())
		}
	}
	if decision, ok := s.Decision(environmentID); ok {
		out.Reason, out.LastRequestAt, out.SleepsAt = decision.Reason, decision.LastRequestAt, decision.SleepsAt
	}
	if out.Enabled {
		out.State = "awake"
	}
	if len(spans) > 0 && spans[0].WokeAt == nil {
		out.State, out.Since, out.Reason, out.LastRequestAt, out.SleepsAt = "asleep", &spans[0].SleptAt, spans[0].Reason, spans[0].LastRequestAt, nil
	}
	s.mu.Lock()
	_, waking := s.flights[environmentID]
	s.mu.Unlock()
	if waking {
		out.State = "waking"
	}
	if proxy := s.proxy(); proxy != nil && out.Enabled {
		if state, err := proxy.DeploymentSleeping(ctx, deploymentRouteName(environmentID)); err == nil {
			out.Switch = &state
		}
	}
	return out, nil
}

// SleepingContainers are the containers asleep by design now, by id, with
// the reason each was put to sleep: the host's alerts read a stopped one
// of these as asleep rather than down.
func (s *OrchestrationStore) SleepingContainers(ctx context.Context) (map[string]SleepSpan, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+sleepSpanColumns+` FROM deploy_sleep_spans WHERE woke_at = 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]SleepSpan{}
	for rows.Next() {
		span, err := scanSleepSpan(rows)
		if err != nil {
			return nil, err
		}
		if span.ContainerID != "" {
			out[span.ContainerID] = *span
		}
	}
	return out, rows.Err()
}

// SleepScheduleConflict is the first step of a schedule that needs the
// container running — a command run inside it, a game server's console —
// which an environment that sleeps would fail at, or "" for none.
func SleepScheduleConflict(steps []ScheduleStep) string {
	for _, step := range steps {
		switch step.Action {
		case "container_command":
			return "runs a command inside the container"
		case "game_command":
			return "sends a command to the game server's console"
		}
	}
	return ""
}

// SleepOn says whether a plan's runtime asks to sleep.
func (c RuntimePlanConfig) SleepOn() bool { return c.Sleep.on() }
