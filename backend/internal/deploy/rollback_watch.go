package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
)

// Metric-gated rollback: the previous release, kept warm.
//
// A blue/green cutover used to stop the release it replaced the moment the
// new one was live, so going back meant a rollback run: a fresh container
// from the old image, its readiness checks, a second cutover — a minute or
// more of serving whatever was wrong. The window here keeps that container
// instead, paused, for a while after the cutover. Paused rather than running
// because the old release must not go on doing work nobody routes to it —
// its schedulers, its queue consumers, its writes against a schema the new
// release may already have migrated — and a frozen process costs memory and
// nothing else. Resuming it takes milliseconds.
//
// While the window is open the request record is read every few seconds:
// the new release's 5xx share and p95 since the cutover against the old
// release's own over the half hour before it. A release that crosses either
// line — over enough requests that three unlucky ones cannot decide — is
// rolled back by an instant rollback run, which resumes the warm container
// and switches the route back to it through the same queue every other run
// takes, so nothing else can be cutting over at the same moment. A window
// that closes clean removes the warm container exactly as retirement always
// did. Every row here is durable: a restart in the middle of a window picks
// it up where it was.

const (
	RollbackWatchWatching    = "watching"
	RollbackWatchRollingBack = "rolling_back"
	RollbackWatchRolledBack  = "rolled_back"
	RollbackWatchClosing     = "closing"
	RollbackWatchClosed      = "closed"
)

// Why a window closed without rolling back.
const (
	RollbackEndWindowPassed  = "window_passed"
	RollbackEndSuperseded    = "superseded"
	RollbackEndKept          = "kept"
	RollbackEndMemory        = "memory"
	RollbackEndWarmLost      = "warm_lost"
	RollbackEndFailed        = "rollback_failed"
	RollbackEndRolledBack    = "rolled_back"
	rollbackWatchOpenStates  = `('watching', 'rolling_back', 'closing')`
	rollbackBaselineWindow   = 30 * time.Minute
	rollbackLatencyFloorMs   = 250
	rollbackMinFailures      = 5
	maxAutomaticRollbackRuns = 2
)

var (
	ErrNoRollbackWatch        = errors.New("no release is kept warm for this environment")
	ErrRollbackWatchUnchanged = errors.New("the warm release window changed concurrently")
)

// RollbackPolicy is one environment's terms. KeepWarm without Automatic keeps
// the instant rollback button and never presses it.
type RollbackPolicy struct {
	KeepWarm      bool `json:"keepWarm"`
	WindowSeconds int  `json:"windowSeconds"`
	Automatic     bool `json:"automatic"`
	// ErrorRateMargin is in percentage points over the previous release's
	// own 5xx share; LatencyFactor multiplies its p95.
	ErrorRateMargin float64    `json:"errorRateMargin"`
	LatencyFactor   float64    `json:"latencyFactor"`
	MinRequests     int        `json:"minRequests"`
	UpdatedBy       string     `json:"updatedBy,omitempty"`
	UpdatedAt       *time.Time `json:"updatedAt,omitempty"`
}

// DefaultRollbackPolicy is what an environment nobody configured gets.
// Ten minutes covers the stretch in which a bad release usually shows
// itself. Five points over the old release's share and twice its p95 (and a
// quarter of a second slower, so 4 ms becoming 9 ms is not a regression) are
// wide enough that ordinary variation never trips them, and twenty requests
// is the least a share can be read from.
func DefaultRollbackPolicy() RollbackPolicy {
	return RollbackPolicy{
		KeepWarm: true, WindowSeconds: 600, Automatic: true,
		ErrorRateMargin: 5, LatencyFactor: 2, MinRequests: 20,
	}
}

func validateRollbackPolicy(policy RollbackPolicy) (RollbackPolicy, error) {
	switch {
	case policy.WindowSeconds < 30 || policy.WindowSeconds > 3600:
		return policy, fmt.Errorf("%w: the window is between 30 seconds and an hour", ErrInvalidPlan)
	case policy.ErrorRateMargin < 0.5 || policy.ErrorRateMargin > 50:
		return policy, fmt.Errorf("%w: the 5xx margin is between 0.5 and 50 percentage points", ErrInvalidPlan)
	case policy.LatencyFactor < 1.2 || policy.LatencyFactor > 10:
		return policy, fmt.Errorf("%w: the p95 factor is between 1.2 and 10", ErrInvalidPlan)
	case policy.MinRequests < 5 || policy.MinRequests > 100000:
		return policy, fmt.Errorf("%w: at least 5 and at most 100000 requests decide", ErrInvalidPlan)
	}
	if !policy.KeepWarm {
		// Nothing warm, nothing to switch back to.
		policy.Automatic = false
	}
	policy.ErrorRateMargin = math.Round(policy.ErrorRateMargin*10) / 10
	policy.LatencyFactor = math.Round(policy.LatencyFactor*100) / 100
	return policy, nil
}

func (s *OrchestrationStore) RollbackPolicy(ctx context.Context, environmentID int64) (RollbackPolicy, error) {
	policy := DefaultRollbackPolicy()
	var keepWarm, automatic int
	var updatedAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT keep_warm, window_seconds, automatic, error_rate_margin, latency_factor, min_requests, updated_by, updated_at
		  FROM deploy_rollback_policies WHERE environment_id = ?`, environmentID).
		Scan(&keepWarm, &policy.WindowSeconds, &automatic, &policy.ErrorRateMargin, &policy.LatencyFactor,
			&policy.MinRequests, &policy.UpdatedBy, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return policy, nil
	}
	if err != nil {
		return policy, err
	}
	policy.KeepWarm, policy.Automatic, policy.UpdatedAt = keepWarm != 0, automatic != 0, unixTimePtr(updatedAt)
	return policy, nil
}

// PutRollbackPolicy saves an environment's terms. An open window keeps the
// terms it opened with; these apply from the next cutover.
func (s *OrchestrationStore) PutRollbackPolicy(ctx context.Context, projectID, environmentID int64, policy RollbackPolicy, actor string) (RollbackPolicy, error) {
	policy, err := validateRollbackPolicy(policy)
	if err != nil {
		return policy, err
	}
	var kind EnvironmentKind
	err = s.db.QueryRowContext(ctx, `SELECT kind FROM deploy_environments WHERE id = ? AND project_id = ? AND archived_at = 0`,
		environmentID, projectID).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) {
		return policy, ErrEnvironmentNotFound
	}
	if err != nil {
		return policy, err
	}
	if kind == EnvironmentPreview {
		return policy, fmt.Errorf("%w: a preview is never kept warm", ErrInvalidPlan)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO deploy_rollback_policies(environment_id, keep_warm, window_seconds, automatic,
		  error_rate_margin, latency_factor, min_requests, updated_by, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(environment_id) DO UPDATE SET keep_warm = excluded.keep_warm,
		  window_seconds = excluded.window_seconds, automatic = excluded.automatic,
		  error_rate_margin = excluded.error_rate_margin, latency_factor = excluded.latency_factor,
		  min_requests = excluded.min_requests, updated_by = excluded.updated_by, updated_at = excluded.updated_at`,
		environmentID, boolToInt(policy.KeepWarm), policy.WindowSeconds, boolToInt(policy.Automatic),
		policy.ErrorRateMargin, policy.LatencyFactor, policy.MinRequests, actor, s.now().UTC().Unix())
	if err != nil {
		return policy, err
	}
	return s.RollbackPolicy(ctx, environmentID)
}

// RollbackReading is what the ingress served over one stretch of time.
// ErrorRate is a share, 0–1; Failures are the 5xx answers it was read from.
type RollbackReading struct {
	Requests  int       `json:"requests"`
	Failures  int       `json:"failures"`
	ErrorRate float64   `json:"errorRate"`
	P95       *float64  `json:"p95,omitempty"`
	From      time.Time `json:"from"`
	Until     time.Time `json:"until"`
}

func rollbackReading(window accesslog.Window, from, until time.Time) RollbackReading {
	if window.Result == nil {
		return RollbackReading{From: from.UTC(), Until: until.UTC()}
	}
	summary := window.Result.Summary
	reading := RollbackReading{
		Requests: summary.Total, Failures: summary.Classes["5xx"], ErrorRate: summary.ErrorRate,
		From: from.UTC(), Until: until.UTC(),
	}
	if summary.Latency != nil && summary.Total > 0 {
		p95 := summary.Latency.P95
		reading.P95 = &p95
	}
	return reading
}

// RollbackVerdict is what one reading decided, in the words the page and the
// notification use.
type RollbackVerdict struct {
	Code    string `json:"code"`
	Judged  bool   `json:"judged"`
	Tripped bool   `json:"tripped"`
	Reason  string `json:"reason"`
	// LatencyJudged is false when the p95 rule had nothing to stand on: an
	// nginx record carries no durations, or the old release served too
	// little to have a p95 of its own.
	LatencyJudged bool `json:"latencyJudged"`
}

// Verdict codes. The first three decide nothing.
const (
	RollbackVerdictNoRoute        = "no_route"
	RollbackVerdictNoRecord       = "no_record"
	RollbackVerdictTooFew         = "not_enough_requests"
	RollbackVerdictWithinBaseline = "within_baseline"
	RollbackVerdictErrorRate      = "error_rate"
	RollbackVerdictLatency        = "latency"
)

// JudgeRollback compares the new release's reading with the old one's. A
// baseline that served fewer than the minimum is not trusted for either
// figure: the 5xx rule then judges against a clean share — a release that
// fails five points of its requests is rolled back whether or not the one
// before it was busy — and the p95 rule, which has no clean value to assume,
// does not judge at all.
func JudgeRollback(policy RollbackPolicy, baseline *RollbackReading, observed RollbackReading, latencyRecorded bool) RollbackVerdict {
	if observed.Requests < policy.MinRequests {
		return RollbackVerdict{
			Code: RollbackVerdictTooFew,
			Reason: fmt.Sprintf("Not enough to judge yet: %d of the %d requests a decision needs.",
				observed.Requests, policy.MinRequests),
		}
	}
	trusted := baseline != nil && baseline.Requests >= policy.MinRequests
	before := 0.0
	if trusted {
		before = baseline.ErrorRate * 100
	}
	now := observed.ErrorRate * 100
	verdict := RollbackVerdict{Judged: true}
	if observed.Failures >= rollbackMinFailures && now > before+policy.ErrorRateMargin {
		verdict.Code, verdict.Tripped = RollbackVerdictErrorRate, true
		verdict.Reason = fmt.Sprintf("5xx answers rose to %s (%d of %d requests) from %s before.",
			percent(now), observed.Failures, observed.Requests, baselineShare(baseline, trusted))
	}
	verdict.LatencyJudged = latencyRecorded && trusted && observed.P95 != nil && baseline.P95 != nil
	if !verdict.Tripped && verdict.LatencyJudged {
		was, is := *baseline.P95, *observed.P95
		if latencyRegressed(was, is, policy.LatencyFactor) {
			verdict.Code, verdict.Tripped = RollbackVerdictLatency, true
			verdict.Reason = fmt.Sprintf("p95 rose to %s from %s before (%d requests).",
				humanMillis(is), humanMillis(was), observed.Requests)
		}
	}
	if verdict.Tripped {
		return verdict
	}
	verdict.Code = RollbackVerdictWithinBaseline
	parts := []string{fmt.Sprintf("5xx %s (%s before)", percent(now), baselineShare(baseline, trusted))}
	switch {
	case verdict.LatencyJudged:
		parts = append(parts, fmt.Sprintf("p95 %s (%s before)", humanMillis(*observed.P95), humanMillis(*baseline.P95)))
	case !latencyRecorded:
		parts = append(parts, "p95 not judged: this ingress records no durations")
	default:
		parts = append(parts, "p95 not judged: the previous release served too little to compare with")
	}
	verdict.Reason = fmt.Sprintf("Within the previous release's baseline over %d requests: %s.", observed.Requests, strings.Join(parts, ", "))
	return verdict
}

// latencyRegressed is the one p95 rule the rollback watch and the traffic
// replay share: past the factor, and at least a quarter of a second slower,
// so 4 ms becoming 9 ms is not a regression.
func latencyRegressed(was, is, factor float64) bool {
	return is > was*factor && is-was >= rollbackLatencyFloorMs
}

func percent(value float64) string {
	return fmt.Sprintf("%.1f%%", value)
}

func baselineShare(baseline *RollbackReading, trusted bool) string {
	if !trusted {
		if baseline == nil || baseline.Requests == 0 {
			return "no traffic"
		}
		return fmt.Sprintf("too little traffic (%d requests)", baseline.Requests)
	}
	return percent(baseline.ErrorRate * 100)
}

// RollbackWatch is one window. ReleaseID is the release being judged and
// WarmReleaseID the one kept warm to switch back to.
type RollbackWatch struct {
	ID              int64            `json:"id"`
	ProjectID       int64            `json:"projectId"`
	EnvironmentID   int64            `json:"environmentId"`
	RunID           int64            `json:"runId"`
	ReleaseID       int64            `json:"releaseId"`
	WarmReleaseID   int64            `json:"warmReleaseId"`
	WarmRuntimeID   string           `json:"warmRuntimeId"`
	State           string           `json:"state"`
	Policy          RollbackPolicy   `json:"policy"`
	CutoverAt       time.Time        `json:"cutoverAt"`
	ExpiresAt       time.Time        `json:"expiresAt"`
	Baseline        *RollbackReading `json:"baseline,omitempty"`
	Observed        *RollbackReading `json:"observed,omitempty"`
	Verdict         *RollbackVerdict `json:"verdict,omitempty"`
	CheckedAt       *time.Time       `json:"checkedAt,omitempty"`
	RollbackRunID   int64            `json:"rollbackRunId,omitempty"`
	Attempts        int              `json:"attempts"`
	EndReason       string           `json:"endReason,omitempty"`
	EndMessage      string           `json:"endMessage,omitempty"`
	EndedAt         *time.Time       `json:"endedAt,omitempty"`
	CreatedAt       time.Time        `json:"createdAt"`
	ReleaseNumber   int64            `json:"releaseNumber"`
	WarmReleaseNum  int64            `json:"warmReleaseNumber"`
	baselineMissing bool
}

const rollbackWatchColumns = `
w.id, w.project_id, w.environment_id, w.run_id, w.release_id, w.warm_release_id, w.warm_runtime_id,
w.state, w.policy_json, w.cutover_at, w.expires_at, w.baseline_json, w.observed_json, w.verdict_json,
w.checked_at, w.rollback_run_id, w.attempts, w.end_reason, w.end_message, w.ended_at, w.created_at,
COALESCE((SELECT release_number FROM deploy_releases WHERE id = w.release_id), 0),
COALESCE((SELECT release_number FROM deploy_releases WHERE id = w.warm_release_id), 0)`

func scanRollbackWatch(row scanner) (*RollbackWatch, error) {
	var watch RollbackWatch
	var policy, baseline, observed, verdict string
	var cutover, expires, checked, ended, created int64
	if err := row.Scan(&watch.ID, &watch.ProjectID, &watch.EnvironmentID, &watch.RunID, &watch.ReleaseID,
		&watch.WarmReleaseID, &watch.WarmRuntimeID, &watch.State, &policy, &cutover, &expires, &baseline,
		&observed, &verdict, &checked, &watch.RollbackRunID, &watch.Attempts, &watch.EndReason,
		&watch.EndMessage, &ended, &created, &watch.ReleaseNumber, &watch.WarmReleaseNum); err != nil {
		return nil, err
	}
	watch.Policy = DefaultRollbackPolicy()
	_ = json.Unmarshal([]byte(policy), &watch.Policy)
	watch.CutoverAt, watch.ExpiresAt, watch.CreatedAt = unixTime(cutover), unixTime(expires), unixTime(created)
	watch.CheckedAt, watch.EndedAt = unixTimePtr(checked), unixTimePtr(ended)
	if baseline == "null" || baseline == "" {
		watch.baselineMissing = true
	} else {
		var reading RollbackReading
		if json.Unmarshal([]byte(baseline), &reading) == nil {
			watch.Baseline = &reading
		}
	}
	if observed != "null" && observed != "" {
		var reading RollbackReading
		if json.Unmarshal([]byte(observed), &reading) == nil {
			watch.Observed = &reading
		}
	}
	if verdict != "null" && verdict != "" {
		var decided RollbackVerdict
		if json.Unmarshal([]byte(verdict), &decided) == nil {
			watch.Verdict = &decided
		}
	}
	return &watch, nil
}

func (s *OrchestrationStore) RollbackWatch(ctx context.Context, id int64) (*RollbackWatch, error) {
	watch, err := scanRollbackWatch(s.db.QueryRowContext(ctx,
		`SELECT `+rollbackWatchColumns+` FROM deploy_rollback_watches w WHERE w.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoRollbackWatch
	}
	return watch, err
}

// OpenRollbackWatch is the environment's open window, if any.
func (s *OrchestrationStore) OpenRollbackWatch(ctx context.Context, environmentID int64) (*RollbackWatch, error) {
	watch, err := scanRollbackWatch(s.db.QueryRowContext(ctx, `SELECT `+rollbackWatchColumns+`
		FROM deploy_rollback_watches w WHERE w.environment_id = ? AND w.state IN `+rollbackWatchOpenStates, environmentID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoRollbackWatch
	}
	return watch, err
}

// LatestRollbackWatch is the environment's most recent window, open or not.
func (s *OrchestrationStore) LatestRollbackWatch(ctx context.Context, environmentID int64) (*RollbackWatch, error) {
	watch, err := scanRollbackWatch(s.db.QueryRowContext(ctx, `SELECT `+rollbackWatchColumns+`
		FROM deploy_rollback_watches w WHERE w.environment_id = ? ORDER BY w.id DESC LIMIT 1`, environmentID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoRollbackWatch
	}
	return watch, err
}

// OpenRollbackWatches is every window the watcher has to look at.
func (s *OrchestrationStore) OpenRollbackWatches(ctx context.Context) ([]RollbackWatch, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+rollbackWatchColumns+`
		FROM deploy_rollback_watches w WHERE w.state IN `+rollbackWatchOpenStates+` ORDER BY w.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	watches := []RollbackWatch{}
	for rows.Next() {
		watch, err := scanRollbackWatch(rows)
		if err != nil {
			return nil, err
		}
		watches = append(watches, *watch)
	}
	return watches, rows.Err()
}

type warmReleaseInput struct {
	ReleaseID     int64
	WarmReleaseID int64
	WarmRuntimeID string
	Policy        RollbackPolicy
	CutoverAt     time.Time
}

// recordWarmRelease opens a window in the same transaction that names the
// predecessor's runtime warm, under the run's lease: there is never a warm
// runtime without the window that will remove it, nor a window over a
// runtime retirement already took.
func (s *OrchestrationStore) recordWarmRelease(ctx context.Context, run EngineRun, claimToken string, input warmReleaseInput) (*RollbackWatch, error) {
	s.writeMu.Lock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.writeMu.Unlock()
		return nil, err
	}
	now := s.now().UTC()
	id, err := func() (int64, error) {
		defer tx.Rollback()
		if err := assertLeaseTx(ctx, tx, run.ID, claimToken, now); err != nil {
			return 0, err
		}
		var liveID int64
		if err := tx.QueryRowContext(ctx, `SELECT live_release_id FROM deploy_environments WHERE id = ?`, run.EnvironmentID).Scan(&liveID); err != nil {
			return 0, err
		}
		if liveID != input.ReleaseID {
			return 0, fmt.Errorf("%w: the release kept warm for is no longer live", ErrInvalidPlan)
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE deploy_release_runtimes SET state = 'warm', updated_at = ?
			 WHERE release_id = ? AND environment_id = ? AND state IN ('draining', 'live', 'ready')`,
			now.Unix(), input.WarmReleaseID, run.EnvironmentID)
		if err != nil {
			return 0, err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return 0, fmt.Errorf("%w: the previous release's runtime is not draining", ErrInvalidPlan)
		}
		expires := input.CutoverAt.Add(time.Duration(input.Policy.WindowSeconds) * time.Second)
		inserted, err := tx.ExecContext(ctx, `
			INSERT INTO deploy_rollback_watches(project_id, environment_id, run_id, release_id, warm_release_id,
			  warm_runtime_id, state, policy_json, cutover_at, expires_at, created_at, updated_at)
			VALUES(?, ?, ?, ?, ?, ?, 'watching', ?, ?, ?, ?, ?)`,
			run.ProjectID, run.EnvironmentID, run.ID, input.ReleaseID, input.WarmReleaseID, input.WarmRuntimeID,
			string(mustJSON(input.Policy)), input.CutoverAt.Unix(), expires.Unix(), now.Unix(), now.Unix())
		if err != nil {
			return 0, err
		}
		id, err := inserted.LastInsertId()
		if err != nil {
			return 0, err
		}
		return id, tx.Commit()
	}()
	s.writeMu.Unlock()
	if err != nil {
		return nil, err
	}
	return s.RollbackWatch(ctx, id)
}

// recordRollbackReading stores what the watcher read. The baseline is
// written once, the first time it could be read.
func (s *OrchestrationStore) recordRollbackReading(ctx context.Context, id int64, baseline *RollbackReading, observed *RollbackReading, verdict RollbackVerdict) error {
	now := s.now().UTC().Unix()
	observedJSON := "null"
	if observed != nil {
		observedJSON = string(mustJSON(observed))
	}
	query := `UPDATE deploy_rollback_watches SET observed_json = ?, verdict_json = ?, checked_at = ?, updated_at = ? WHERE id = ? AND state = 'watching'`
	args := []any{observedJSON, string(mustJSON(verdict)), now, now, id}
	if baseline != nil {
		query = `UPDATE deploy_rollback_watches SET baseline_json = CASE WHEN baseline_json = 'null' THEN ? ELSE baseline_json END,
			observed_json = ?, verdict_json = ?, checked_at = ?, updated_at = ? WHERE id = ? AND state = 'watching'`
		args = append([]any{string(mustJSON(baseline))}, args...)
	}
	_, err := s.db.ExecContext(context.WithoutCancel(ctx), query, args...)
	return err
}

// RecordRollbackRun records the instant rollback run enqueued for a window,
// by the watcher or an operator. The window stays watching until that run
// claims it, and nothing closes it meanwhile.
func (s *OrchestrationStore) RecordRollbackRun(ctx context.Context, id, runID int64) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	result, err := s.db.ExecContext(ctx, `
		UPDATE deploy_rollback_watches SET rollback_run_id = ?, attempts = attempts + 1, updated_at = ?
		 WHERE id = ? AND state = 'watching' AND (rollback_run_id = 0 OR rollback_run_id = ?)`,
		runID, s.now().UTC().Unix(), id, runID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrRollbackWatchUnchanged
	}
	return nil
}

// forgetRollbackRun clears an asked-for run that ended without claiming the
// window: cancelled while queued, or refused before it started.
func (s *OrchestrationStore) forgetRollbackRun(ctx context.Context, id, runID int64) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.ExecContext(ctx, `
		UPDATE deploy_rollback_watches SET rollback_run_id = 0, updated_at = ?
		 WHERE id = ? AND state = 'watching' AND rollback_run_id = ?`, s.now().UTC().Unix(), id, runID)
	return err
}

// beginCloseRollbackWatch moves a watching window to closing. It is the one
// compare-and-set that separates a close from an instant rollback claiming
// the same window: exactly one of them wins.
func (s *OrchestrationStore) beginCloseRollbackWatch(ctx context.Context, id int64, reason, message string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	result, err := s.db.ExecContext(ctx, `
		UPDATE deploy_rollback_watches SET state = 'closing', end_reason = ?, end_message = ?, updated_at = ?
		 WHERE id = ? AND state = 'watching'`, reason, message, s.now().UTC().Unix(), id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrRollbackWatchUnchanged
	}
	return nil
}

// finishCloseRollbackWatch records the warm runtime removed. The release is
// retired only if it is not, by then, the live one.
func (s *OrchestrationStore) finishCloseRollbackWatch(ctx context.Context, watch RollbackWatch) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.now().UTC().Unix()
	result, err := tx.ExecContext(ctx, `
		UPDATE deploy_rollback_watches SET state = 'closed', ended_at = ?, updated_at = ?
		 WHERE id = ? AND state = 'closing'`, now, now, watch.ID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrRollbackWatchUnchanged
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE deploy_release_runtimes SET state = 'retired', updated_at = ?
		 WHERE release_id = ? AND state = 'warm'`, now, watch.WarmReleaseID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE deploy_releases SET retired_at = ?
		 WHERE id = ? AND state <> 'live' AND retired_at = 0`, now, watch.WarmReleaseID); err != nil {
		return err
	}
	return tx.Commit()
}

// claimRollbackWatch is the instant rollback run taking the window. It is
// repeatable by the same run, so a step retried after a restart finds its
// own claim.
func (s *OrchestrationStore) claimRollbackWatch(ctx context.Context, run EngineRun, claimToken string, watchID int64) (*RollbackWatch, error) {
	s.writeMu.Lock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.writeMu.Unlock()
		return nil, err
	}
	now := s.now().UTC()
	err = func() error {
		defer tx.Rollback()
		if err := assertLeaseTx(ctx, tx, run.ID, claimToken, now); err != nil {
			return err
		}
		watch, err := scanRollbackWatch(tx.QueryRowContext(ctx,
			`SELECT `+rollbackWatchColumns+` FROM deploy_rollback_watches w WHERE w.id = ?`, watchID))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoRollbackWatch
		}
		if err != nil {
			return err
		}
		if watch.EnvironmentID != run.EnvironmentID {
			return fmt.Errorf("%w: the window belongs to another environment", ErrInvalidPlan)
		}
		switch {
		case watch.State == RollbackWatchRollingBack && watch.RollbackRunID == run.ID:
		case watch.State == RollbackWatchWatching:
		default:
			return warmUnavailable(watch)
		}
		var liveID int64
		if err := tx.QueryRowContext(ctx, `SELECT live_release_id FROM deploy_environments WHERE id = ?`, run.EnvironmentID).Scan(&liveID); err != nil {
			return err
		}
		if liveID != watch.ReleaseID {
			return fmt.Errorf("%w: release #%d is no longer the live release", ErrNoRollbackWatch, watch.ReleaseNumber)
		}
		var runtimeState string
		if err := tx.QueryRowContext(ctx, `SELECT state FROM deploy_release_runtimes WHERE release_id = ?`, watch.WarmReleaseID).Scan(&runtimeState); err != nil || runtimeState != "warm" {
			return fmt.Errorf("%w: release #%d's runtime is no longer warm", ErrNoRollbackWatch, watch.WarmReleaseNum)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE deploy_rollback_watches SET state = 'rolling_back', rollback_run_id = ?, updated_at = ?
			 WHERE id = ?`, run.ID, now.Unix(), watchID); err != nil {
			return err
		}
		return tx.Commit()
	}()
	s.writeMu.Unlock()
	if err != nil {
		return nil, err
	}
	return s.RollbackWatch(ctx, watchID)
}

func warmUnavailable(watch *RollbackWatch) error {
	if watch.State == RollbackWatchClosed || watch.State == RollbackWatchClosing {
		message := watch.EndMessage
		if message == "" {
			message = "its window closed"
		}
		return fmt.Errorf("%w: release #%d is no longer kept warm (%s)", ErrNoRollbackWatch, watch.WarmReleaseNum, strings.TrimSuffix(message, "."))
	}
	if watch.State == RollbackWatchRolledBack {
		return fmt.Errorf("%w: traffic already went back to release #%d", ErrNoRollbackWatch, watch.WarmReleaseNum)
	}
	return fmt.Errorf("%w: another rollback holds release #%d", ErrNoRollbackWatch, watch.WarmReleaseNum)
}

// returnRollbackWatch hands a claimed window back to watching: the rollback
// was cancelled, or failed somewhere a later attempt may not.
func (s *OrchestrationStore) returnRollbackWatch(ctx context.Context, watchID, runID int64) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.ExecContext(ctx, `
		UPDATE deploy_rollback_watches SET state = 'watching', rollback_run_id = 0, updated_at = ?
		 WHERE id = ? AND state = 'rolling_back' AND rollback_run_id = ?`, s.now().UTC().Unix(), watchID, runID)
	return err
}

// failRollbackWatch closes a claimed window whose warm release cannot be
// used: it failed its readiness check after resuming, or vanished. The
// caller has already removed what was left of it.
func (s *OrchestrationStore) failRollbackWatch(ctx context.Context, watch RollbackWatch, runID int64, message string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.now().UTC().Unix()
	result, err := tx.ExecContext(ctx, `
		UPDATE deploy_rollback_watches SET state = 'closed', end_reason = ?, end_message = ?, ended_at = ?, updated_at = ?
		 WHERE id = ? AND ((state = 'rolling_back' AND rollback_run_id = ?) OR state IN ('watching', 'closing'))`,
		RollbackEndFailed, message, now, now, watch.ID, runID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrRollbackWatchUnchanged
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE deploy_release_runtimes SET state = 'retired', updated_at = ?
		 WHERE release_id = ? AND state IN ('warm', 'ready', 'candidate')`, now, watch.WarmReleaseID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE deploy_releases SET retired_at = ? WHERE id = ? AND state <> 'live' AND retired_at = 0`, now, watch.WarmReleaseID); err != nil {
		return err
	}
	return tx.Commit()
}

// activateWarmRelease moves the live pointer back to the warm release in one
// transaction: it is live again with its own frozen configuration, and the
// release it replaced is retained with the reason it was taken back.
func (s *OrchestrationStore) activateWarmRelease(ctx context.Context, run EngineRun, claimToken string, watch RollbackWatch, reason string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.now().UTC()
	if err := assertLeaseTx(ctx, tx, run.ID, claimToken, now); err != nil {
		return err
	}
	var state string
	var holder int64
	if err := tx.QueryRowContext(ctx, `SELECT state, rollback_run_id FROM deploy_rollback_watches WHERE id = ?`, watch.ID).Scan(&state, &holder); err != nil {
		return err
	}
	if state != RollbackWatchRollingBack || holder != run.ID {
		return fmt.Errorf("%w: the window is not held by this rollback", ErrInvalidPlan)
	}
	var liveID int64
	if err := tx.QueryRowContext(ctx, `SELECT live_release_id FROM deploy_environments WHERE id = ?`, run.EnvironmentID).Scan(&liveID); err != nil {
		return err
	}
	if liveID == watch.WarmReleaseID {
		// A retried step whose transaction already committed.
		return tx.Commit()
	}
	if liveID != watch.ReleaseID {
		return fmt.Errorf("%w: the live release changed during the rollback", ErrInvalidPlan)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE deploy_release_runtimes SET state = 'live', updated_at = ?
		 WHERE release_id = ? AND state = 'warm'`, now.Unix(), watch.WarmReleaseID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return fmt.Errorf("%w: the warm release's runtime is no longer warm", ErrInvalidPlan)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE deploy_releases SET state = 'live', activated_at = ?, retired_at = 0 WHERE id = ?`,
		now.Unix(), watch.WarmReleaseID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE deploy_releases SET state = 'retained', rolled_back_at = ?, rollback_reason = ? WHERE id = ?`,
		now.Unix(), reason, watch.ReleaseID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE deploy_release_runtimes SET state = 'draining', updated_at = ?
		 WHERE release_id = ? AND state IN ('live', 'ready')`, now.Unix(), watch.ReleaseID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE deploy_environments SET live_release_id = ?, updated_at = ? WHERE id = ?`,
		watch.WarmReleaseID, now.Unix(), run.EnvironmentID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE deploy_runs SET release_id = ? WHERE id = ?`, watch.WarmReleaseID, run.ID); err != nil {
		return err
	}
	return tx.Commit()
}

// finishRollbackWatch records the rolled-back release's runtime removed and
// the window over.
func (s *OrchestrationStore) finishRollbackWatch(ctx context.Context, watch RollbackWatch, runID int64, message string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.now().UTC().Unix()
	if _, err := tx.ExecContext(ctx, `
		UPDATE deploy_release_runtimes SET state = 'retired', updated_at = ?
		 WHERE release_id = ? AND state IN ('draining', 'live', 'ready', 'failed')`, now, watch.ReleaseID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE deploy_releases SET retired_at = ? WHERE id = ? AND state <> 'live' AND retired_at = 0`, now, watch.ReleaseID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE deploy_rollback_watches SET state = 'rolled_back', end_reason = ?, end_message = ?, ended_at = ?, updated_at = ?
		 WHERE id = ? AND state = 'rolling_back' AND (rollback_run_id = ? OR ? = 0)`,
		RollbackEndRolledBack, message, now, now, watch.ID, runID, runID); err != nil {
		return err
	}
	return tx.Commit()
}
