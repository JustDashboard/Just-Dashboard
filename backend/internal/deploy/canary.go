package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// A canary release is blue/green with a trial between the candidate's checks
// and the cutover: the candidate joins the live release's route for a share
// of its requests, beside the live release, for a window. Both releases'
// answers are recorded apart (the route says which release gave each one), and
// the rollback watch's own comparison — the 5xx share and the p95, on the
// environment's margins — reads the candidate against the live release side by
// side. A candidate that compares badly is removed with the live release
// untouched; one that compares well is promoted, on its own when the plan says
// so, or when an operator does.

// CanaryTerms are a canary plan's trial.
type CanaryTerms struct {
	// Percent of the route's requests the candidate is given, 1 to 50: each
	// request picks a release at random, so the share holds per request.
	Percent int `json:"percent"`
	// DurationSeconds is how long the trial lasts, 1 to 60 minutes.
	DurationSeconds int `json:"durationSeconds"`
	// AutoPromote cuts over at the end of a trial that compared well. Without
	// it the candidate keeps its share until an operator promotes or aborts.
	AutoPromote bool `json:"autoPromote,omitempty"`
}

const (
	MinCanaryPercent = 1
	MaxCanaryPercent = 50
	MinCanarySeconds = 60
	MaxCanarySeconds = 3600
	// canaryHoldLimit is how long a trial that compared well waits for an
	// operator to promote it before it is aborted: a candidate holding a
	// share of production overnight is a decision nobody made.
	canaryHoldLimit = 24 * time.Hour
)

// Canary states. Running is the window; holding is a window passed and
// waiting for an operator. Promoted and aborted are ends.
const (
	CanaryRunning  = "running"
	CanaryHolding  = "holding"
	CanaryPromoted = "promoted"
	CanaryAborted  = "aborted"
)

// Why a trial ended.
const (
	CanaryEndPassed           = "passed"
	CanaryEndOperatorPromoted = "operator_promoted"
	CanaryEndFailed           = "failed"
	CanaryEndOperatorAborted  = "operator_aborted"
	CanaryEndCancelled        = "cancelled"
	CanaryEndUnanswered       = "unanswered"
	CanaryEndCandidateLost    = "candidate_lost"
	CanaryEndRouteFailed      = "route_failed"
)

var ErrNoCanary = errors.New("no canary is running for this environment")

func validateCanary(c PlanConfiguration) error {
	terms := c.Runtime.Canary
	if c.Runtime.Strategy != StrategyCanary {
		if terms != nil {
			return invalidField("runtime.canary", "canary terms apply only to the canary strategy")
		}
		return nil
	}
	if terms == nil {
		return invalidField("runtime.canary", "the canary strategy needs its terms: a share of %d to %d percent of the requests and a window of %d to %d minutes",
			MinCanaryPercent, MaxCanaryPercent, MinCanarySeconds/60, MaxCanarySeconds/60)
	}
	if terms.Percent < MinCanaryPercent || terms.Percent > MaxCanaryPercent {
		return invalidField("runtime.canary.percent", "a canary takes between %d and %d percent of the requests", MinCanaryPercent, MaxCanaryPercent)
	}
	if terms.DurationSeconds < MinCanarySeconds || terms.DurationSeconds > MaxCanarySeconds {
		return invalidField("runtime.canary.durationSeconds", "a canary's window is between %d and %d seconds", MinCanarySeconds, MaxCanarySeconds)
	}
	if len(publicRouteURLs(c.Domains)) == 0 {
		return invalidField("runtime.strategy", "a canary shares the public route's requests, and this plan has no domain that serves the application")
	}
	return nil
}

// canaryFinding is preflight's account of the trial a canary plan's next
// release gets.
func canaryFinding(configuration PlanConfiguration) (PreflightFinding, bool) {
	terms := configuration.Runtime.Canary
	if configuration.Runtime.Strategy != StrategyCanary || terms == nil {
		return PreflightFinding{}, false
	}
	then := "and then waits for you to promote or abort it"
	if terms.AutoPromote {
		then = "and is promoted at the end when it compares well"
	}
	return finding("canary", PreflightPass,
		fmt.Sprintf("Each release is tried on %d%% of the requests for %s first", terms.Percent, canaryWindowWords(terms.DurationSeconds)),
		fmt.Sprintf("%d%% · %s", terms.Percent, canaryWindowWords(terms.DurationSeconds)),
		"The new release answers its share beside the live one, the two releases' 5xx share and p95 are compared side by side, "+
			"and one that does worse is removed with the live release untouched; one that compares well keeps its share "+then+".",
		"", "deploy", "runtime.canary"), true
}

func canaryWindowWords(seconds int) string {
	if seconds%60 == 0 {
		if seconds == 60 {
			return "a minute"
		}
		return fmt.Sprintf("%d minutes", seconds/60)
	}
	return fmt.Sprintf("%d seconds", seconds)
}

// CanaryWindow is one trial.
type CanaryWindow struct {
	ID              int64            `json:"id"`
	ProjectID       int64            `json:"projectId"`
	EnvironmentID   int64            `json:"environmentId"`
	RunID           int64            `json:"runId"`
	ReleaseID       int64            `json:"releaseId"`
	LiveReleaseID   int64            `json:"liveReleaseId"`
	Percent         int              `json:"percent"`
	DurationSeconds int              `json:"durationSeconds"`
	AutoPromote     bool             `json:"autoPromote"`
	Policy          RollbackPolicy   `json:"policy"`
	State           string           `json:"state"`
	Decision        string           `json:"decision,omitempty"`
	DecidedBy       string           `json:"decidedBy,omitempty"`
	StartedAt       time.Time        `json:"startedAt"`
	EndsAt          time.Time        `json:"endsAt"`
	Live            *RollbackReading `json:"live,omitempty"`
	Candidate       *RollbackReading `json:"candidate,omitempty"`
	Verdict         *RollbackVerdict `json:"verdict,omitempty"`
	CheckedAt       *time.Time       `json:"checkedAt,omitempty"`
	EndReason       string           `json:"endReason,omitempty"`
	EndMessage      string           `json:"endMessage,omitempty"`
	EndedAt         *time.Time       `json:"endedAt,omitempty"`
	CreatedAt       time.Time        `json:"createdAt"`
	ReleaseNumber   int64            `json:"releaseNumber"`
	LiveReleaseNum  int64            `json:"liveReleaseNumber"`
}

// Open says whether the trial still has the candidate on the route.
func (w CanaryWindow) Open() bool { return w.State == CanaryRunning || w.State == CanaryHolding }

const canaryColumns = `
c.id, c.project_id, c.environment_id, c.run_id, c.release_id, c.live_release_id, c.percent,
c.duration_seconds, c.auto_promote, c.policy_json, c.state, c.decision, c.decided_by, c.started_at,
c.ends_at, c.live_json, c.candidate_json, c.verdict_json, c.checked_at, c.end_reason, c.end_message,
c.ended_at, c.created_at,
COALESCE((SELECT release_number FROM deploy_releases WHERE id = c.release_id), 0),
COALESCE((SELECT release_number FROM deploy_releases WHERE id = c.live_release_id), 0)`

func scanCanary(row scanner) (*CanaryWindow, error) {
	var window CanaryWindow
	var policy, live, candidate, verdict string
	var autoPromote int
	var started, ends, checked, ended, created int64
	if err := row.Scan(&window.ID, &window.ProjectID, &window.EnvironmentID, &window.RunID, &window.ReleaseID,
		&window.LiveReleaseID, &window.Percent, &window.DurationSeconds, &autoPromote, &policy, &window.State,
		&window.Decision, &window.DecidedBy, &started, &ends, &live, &candidate, &verdict, &checked,
		&window.EndReason, &window.EndMessage, &ended, &created, &window.ReleaseNumber, &window.LiveReleaseNum); err != nil {
		return nil, err
	}
	window.AutoPromote = autoPromote != 0
	window.Policy = DefaultRollbackPolicy()
	_ = json.Unmarshal([]byte(policy), &window.Policy)
	window.StartedAt, window.EndsAt, window.CreatedAt = unixTime(started), unixTime(ends), unixTime(created)
	window.CheckedAt, window.EndedAt = unixTimePtr(checked), unixTimePtr(ended)
	for _, field := range []struct {
		raw  string
		into **RollbackReading
	}{{live, &window.Live}, {candidate, &window.Candidate}} {
		if field.raw != "null" && field.raw != "" {
			var reading RollbackReading
			if json.Unmarshal([]byte(field.raw), &reading) == nil {
				*field.into = &reading
			}
		}
	}
	if verdict != "null" && verdict != "" {
		var decided RollbackVerdict
		if json.Unmarshal([]byte(verdict), &decided) == nil {
			window.Verdict = &decided
		}
	}
	return &window, nil
}

func (s *OrchestrationStore) canaryWhere(ctx context.Context, where string, args ...any) (*CanaryWindow, error) {
	window, err := scanCanary(s.db.QueryRowContext(ctx,
		`SELECT `+canaryColumns+` FROM deploy_canaries c WHERE `+where+` ORDER BY c.id DESC LIMIT 1`, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoCanary
	}
	return window, err
}

// Canary reads one trial.
func (s *OrchestrationStore) Canary(ctx context.Context, id int64) (*CanaryWindow, error) {
	return s.canaryWhere(ctx, `c.id = ?`, id)
}

// OpenCanary is the environment's trial with the candidate still on the
// route, if there is one.
func (s *OrchestrationStore) OpenCanary(ctx context.Context, environmentID int64) (*CanaryWindow, error) {
	return s.canaryWhere(ctx, `c.environment_id = ? AND c.state IN ('running', 'holding')`, environmentID)
}

// LatestCanary is the environment's most recent trial, open or ended.
func (s *OrchestrationStore) LatestCanary(ctx context.Context, environmentID int64) (*CanaryWindow, error) {
	return s.canaryWhere(ctx, `c.environment_id = ?`, environmentID)
}

// canaryForRun is the trial a run holds, open or ended.
func (s *OrchestrationStore) canaryForRun(ctx context.Context, runID int64) (*CanaryWindow, error) {
	return s.canaryWhere(ctx, `c.run_id = ?`, runID)
}

type canaryInput struct {
	ReleaseID     int64
	LiveReleaseID int64
	Terms         CanaryTerms
	Policy        RollbackPolicy
}

// openCanary records a trial under the run's lease, so a restarted process
// finds the window where it was rather than starting it again.
func (s *OrchestrationStore) openCanary(ctx context.Context, run EngineRun, claimToken string, input canaryInput) (*CanaryWindow, error) {
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
		if liveID != input.LiveReleaseID {
			return 0, fmt.Errorf("%w: the release a canary shares the route with is no longer live", ErrInvalidPlan)
		}
		ends := now.Add(time.Duration(input.Terms.DurationSeconds) * time.Second)
		inserted, err := tx.ExecContext(ctx, `
			INSERT INTO deploy_canaries(project_id, environment_id, run_id, release_id, live_release_id, percent,
			  duration_seconds, auto_promote, policy_json, state, started_at, ends_at, created_at, updated_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, 'running', ?, ?, ?, ?)`,
			run.ProjectID, run.EnvironmentID, run.ID, input.ReleaseID, input.LiveReleaseID, input.Terms.Percent,
			input.Terms.DurationSeconds, boolToInt(input.Terms.AutoPromote), string(mustJSON(input.Policy)),
			now.Unix(), ends.Unix(), now.Unix(), now.Unix())
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
	return s.Canary(ctx, id)
}

// recordCanaryReading stores the two releases' figures and what they decided.
func (s *OrchestrationStore) recordCanaryReading(ctx context.Context, id int64, live, candidate *RollbackReading, verdict RollbackVerdict) error {
	now := s.now().UTC().Unix()
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.ExecContext(ctx, `
		UPDATE deploy_canaries SET live_json = ?, candidate_json = ?, verdict_json = ?, checked_at = ?, updated_at = ?
		 WHERE id = ? AND state IN ('running', 'holding')`,
		string(mustJSON(live)), string(mustJSON(candidate)), string(mustJSON(verdict)), now, now, id)
	return err
}

// holdCanary marks a window that passed and is waiting for an operator.
func (s *OrchestrationStore) holdCanary(ctx context.Context, id int64) error {
	now := s.now().UTC().Unix()
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.ExecContext(ctx, `UPDATE deploy_canaries SET state = 'holding', updated_at = ? WHERE id = ? AND state = 'running'`, now, id)
	return err
}

// closeCanary ends a trial.
func (s *OrchestrationStore) closeCanary(ctx context.Context, id int64, state, reason, message string) error {
	if state != CanaryPromoted && state != CanaryAborted {
		return fmt.Errorf("invalid canary end state %q", state)
	}
	now := s.now().UTC().Unix()
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.ExecContext(ctx, `
		UPDATE deploy_canaries SET state = ?, end_reason = ?, end_message = ?, ended_at = ?, updated_at = ?
		 WHERE id = ? AND state IN ('running', 'holding')`, state, reason, message, now, now, id)
	return err
}

// Canary decisions an operator asks for.
const (
	CanaryPromote = "promote"
	CanaryAbort   = "abort"
)

// RequestCanaryDecision asks the run holding the environment's trial to
// promote or abort it. The run carries it out within a second; the first
// decision asked for is the one taken.
func (s *OrchestrationStore) RequestCanaryDecision(ctx context.Context, projectID, environmentID int64, decision, actor string) (*CanaryWindow, error) {
	if decision != CanaryPromote && decision != CanaryAbort {
		return nil, fmt.Errorf("%w: a canary is promoted or aborted", ErrInvalidPlan)
	}
	window, err := s.OpenCanary(ctx, environmentID)
	if err != nil {
		return nil, err
	}
	if window.ProjectID != projectID {
		return nil, ErrNoCanary
	}
	if window.Decision != "" {
		return nil, fmt.Errorf("%w: release #%d is already being %s", ErrCanaryDecided, window.ReleaseNumber, canaryDecisionWord(window.Decision))
	}
	now := s.now().UTC().Unix()
	s.writeMu.Lock()
	result, err := s.db.ExecContext(ctx, `
		UPDATE deploy_canaries SET decision = ?, decided_by = ?, updated_at = ?
		 WHERE id = ? AND state IN ('running', 'holding') AND decision = ''`, decision, actor, now, window.ID)
	s.writeMu.Unlock()
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, fmt.Errorf("%w: the canary was decided or ended a moment ago", ErrCanaryDecided)
	}
	return s.Canary(ctx, window.ID)
}

var ErrCanaryDecided = errors.New("the canary has already been decided")

func canaryDecisionWord(decision string) string {
	if decision == CanaryPromote {
		return "promoted"
	}
	return "aborted"
}

// judgeCanary reads the candidate against the live release on the rollback
// watch's own rule (JudgeRollback), the two side by side over the same
// window rather than one after the other, and says so in its words.
func judgeCanary(policy RollbackPolicy, live *RollbackReading, candidate RollbackReading, latencyRecorded bool) RollbackVerdict {
	verdict := JudgeRollback(policy, live, candidate, latencyRecorded)
	verdict.Reason = strings.NewReplacer(
		" before (", " on the live release (",
		" before)", " on the live release)",
		" before.", " on the live release.",
		"Not enough to judge yet:", "Not enough to judge yet on the candidate:",
		"the previous release's baseline", "the live release's figures",
		"the previous release served too little", "the live release served too little",
	).Replace(verdict.Reason)
	return verdict
}

// holdLeaseSlot moves the run's lease out of the heavy and light budgets, or
// back into its own: a run waiting out a canary window builds nothing, and
// would otherwise hold the host's one build slot for up to an hour while
// every other deployment queued behind it. The environment stays leased, so
// nothing else runs there meanwhile.
func (s *OrchestrationStore) holdLeaseSlot(ctx context.Context, runID int64, claimToken string, hold bool) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	class := string(canaryHoldSlot)
	if !hold {
		if err := s.db.QueryRowContext(ctx, `SELECT slot_class FROM deploy_runs WHERE id = ?`, runID).Scan(&class); err != nil {
			return err
		}
	}
	_, err := s.db.ExecContext(ctx, `UPDATE deploy_queue_leases SET slot_class = ? WHERE run_id = ? AND claim_token = ?`, class, runID, claimToken)
	return err
}

// canaryHoldSlot is the lease class of a run waiting out a canary window,
// counted against neither budget.
const canaryHoldSlot SlotClass = "canary"
