package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
)

var (
	ErrGitManualOnly     = errors.New("automatic deployments are disabled for this environment")
	ErrGitPolicyConflict = errors.New("existing hook filters disagree; save one automatic deployment policy")
	// ErrPromotionOnly refuses a build in an environment that takes only
	// releases promoted from staging, whoever or whatever asked for it.
	ErrPromotionOnly = errors.New("production takes only releases promoted from staging; deploy to staging and promote that release")
	// ErrStagingRequired refuses promotion-only without a staging
	// environment to promote from: production would have no way to change.
	ErrStagingRequired = errors.New("only promotions can be required once the deployment has a staging environment")
)

type GitDeploymentPolicy struct {
	Automatic    bool     `json:"automatic"`
	WatchInclude []string `json:"watchInclude"`
	WatchExclude []string `json:"watchExclude"`
	// CommitStatuses reports each run's state back to the source commit on
	// GitHub through the dashboard's own credential. Default on, so a
	// repository that already deploys from here gains it without an edit.
	CommitStatuses bool `json:"commitStatuses"`
	// PromotionOnly makes production refuse every build — a push, a hook, a
	// schedule or a person pressing Deploy — so the only new code it runs is
	// an image staging already ran, promoted. Redeploy, restart and rollback
	// reuse releases production already has and stay available.
	PromotionOnly bool `json:"promotionOnly"`
	Revision      int  `json:"revision"`
	Inherited     bool `json:"inherited"`
	Conflict      bool `json:"conflict"`
}

// Key digests the fields that decide whether a change deploys. Commit-status
// reporting is deliberately excluded: toggling it must not invalidate cached
// polling decisions or race an in-flight enqueue. Promotion-only is omitted
// while off, so every key stored before it existed still matches.
func (p GitDeploymentPolicy) Key() string {
	return digestBytes(mustJSON(struct {
		Automatic     bool     `json:"automatic"`
		WatchInclude  []string `json:"watchInclude"`
		WatchExclude  []string `json:"watchExclude"`
		Revision      int      `json:"revision"`
		Inherited     bool     `json:"inherited"`
		Conflict      bool     `json:"conflict"`
		PromotionOnly bool     `json:"promotionOnly,omitempty"`
	}{p.Automatic, p.WatchInclude, p.WatchExclude, p.Revision, p.Inherited, p.Conflict, p.PromotionOnly}))
}

type gitPolicyQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func gitDeploymentPolicy(ctx context.Context, q gitPolicyQuery, environmentID int64) (GitDeploymentPolicy, error) {
	p := GitDeploymentPolicy{Automatic: true, CommitStatuses: true, WatchInclude: []string{}, WatchExclude: []string{}}
	var automatic, commitStatuses, promotionOnly int
	var include, exclude string
	err := q.QueryRowContext(ctx, `SELECT automatic,include_json,exclude_json,commit_statuses,promotion_only,revision FROM deploy_git_policies WHERE environment_id=?`, environmentID).
		Scan(&automatic, &include, &exclude, &commitStatuses, &promotionOnly, &p.Revision)
	if err == nil {
		p.Automatic = automatic != 0
		p.CommitStatuses = commitStatuses != 0
		p.PromotionOnly = promotionOnly != 0
		if json.Unmarshal([]byte(include), &p.WatchInclude) != nil || json.Unmarshal([]byte(exclude), &p.WatchExclude) != nil {
			return p, ErrInvalidPlan
		}
		return p, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return p, err
	}
	// Upgrades retain existing hook restrictions for polling too. Different
	// legacy filters require an explicit decision instead of silently choosing
	// whichever provider happens to deliver first.
	rows, err := q.QueryContext(ctx, `SELECT config_json FROM deploy_triggers WHERE environment_id=? AND enabled=1 ORDER BY id`, environmentID)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var cfg TriggerConfig
		if err := rows.Scan(&raw); err != nil {
			return p, err
		}
		if json.Unmarshal([]byte(raw), &cfg) != nil {
			return p, ErrInvalidPlan
		}
		if !branchTriggerConfig(cfg) || (len(cfg.WatchInclude) == 0 && len(cfg.WatchExclude) == 0) {
			continue
		}
		inc, e1 := canonicalWatchPatterns(cfg.WatchInclude)
		exc, e2 := canonicalWatchPatterns(cfg.WatchExclude)
		if e1 != nil || e2 != nil {
			p.Conflict = true
			continue
		}
		if p.Inherited && (!slices.Equal(p.WatchInclude, inc) || !slices.Equal(p.WatchExclude, exc)) {
			p.Conflict = true
		}
		p.WatchInclude, p.WatchExclude, p.Inherited = inc, exc, true
	}
	return p, rows.Err()
}

func branchTriggerConfig(cfg TriggerConfig) bool {
	if len(cfg.Events) == 0 {
		return true
	}
	for _, event := range cfg.Events {
		switch strings.ToLower(event) {
		case "push", "push hook", "repo:push", "deploy":
			return true
		}
	}
	return false
}

func canonicalWatchPatterns(values []string) ([]string, error) {
	if len(values) > 64 {
		return nil, fmt.Errorf("at most 64 watch patterns are allowed")
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 512 || strings.ContainsAny(value, "\\\x00\r\n") || strings.HasPrefix(value, "/") {
			return nil, fmt.Errorf("watch patterns must be relative repository paths")
		}
		for _, part := range strings.Split(value, "/") {
			if part == ".." || part == "." || part == "" {
				return nil, fmt.Errorf("watch patterns cannot contain empty, dot or parent segments")
			}
		}
		if _, err := path.Match(value, ""); err != nil {
			return nil, fmt.Errorf("invalid watch pattern %q", value)
		}
		result = append(result, value)
	}
	slices.Sort(result)
	return slices.Compact(result), nil
}

func (s *OrchestrationStore) GitDeploymentPolicy(ctx context.Context, projectID, environmentID int64) (GitDeploymentPolicy, error) {
	if _, err := s.EnvironmentExecutionTarget(ctx, projectID, environmentID); err != nil {
		return GitDeploymentPolicy{}, err
	}
	return gitDeploymentPolicy(ctx, s.db, environmentID)
}

func (s *OrchestrationStore) SetGitDeploymentPolicy(ctx context.Context, projectID, environmentID int64, p GitDeploymentPolicy) (GitDeploymentPolicy, error) {
	var err error
	if p.WatchInclude, err = canonicalWatchPatterns(p.WatchInclude); err != nil {
		return p, err
	}
	if p.WatchExclude, err = canonicalWatchPatterns(p.WatchExclude); err != nil {
		return p, err
	}
	if p.Revision < 0 {
		return p, ErrRevisionConflict
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return p, err
	}
	defer tx.Rollback()
	var kind EnvironmentKind
	if err := tx.QueryRowContext(ctx, `SELECT e.kind FROM deploy_environments e JOIN deploy_projects p ON p.id=e.project_id WHERE e.id=? AND p.id=? AND e.archived_at=0 AND p.archived_at=0`, environmentID, projectID).Scan(&kind); err != nil {
		return p, ErrEnvironmentNotFound
	}
	current, err := gitDeploymentPolicy(ctx, tx, environmentID)
	if err != nil {
		return p, err
	}
	if current.Revision != p.Revision {
		return p, ErrRevisionConflict
	}
	if p.PromotionOnly && !current.PromotionOnly {
		if kind != EnvironmentProduction {
			return p, fmt.Errorf("%w: only production takes promotions", ErrInvalidPlan)
		}
		if _, err := stagingEnvironmentTx(ctx, tx, projectID); err != nil {
			if errors.Is(err, ErrEnvironmentNotFound) {
				return p, ErrStagingRequired
			}
			return p, err
		}
	}
	p.Inherited, p.Conflict = false, false
	p.Revision++
	if err := writeGitPolicyTx(ctx, tx, environmentID, p, s.now().UTC().Unix()); err != nil {
		return p, err
	}
	return p, tx.Commit()
}

// commitGitPolicy records the automatic-deployment decision taken while a
// project was being created. No decision writes no row, which leaves the
// defaults in `gitDeploymentPolicy` exactly as they were for every caller that
// does not ask; a decision is stored at revision 1, so the Automation page
// edits it from there like any other saved policy.
func commitGitPolicy(
	ctx context.Context,
	tx *sql.Tx,
	environmentID int64,
	policy *GitDeploymentPolicy,
	now int64,
) error {
	if policy == nil {
		return nil
	}
	include, err := canonicalWatchPatterns(policy.WatchInclude)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidPlan, err)
	}
	exclude, err := canonicalWatchPatterns(policy.WatchExclude)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidPlan, err)
	}
	stored := GitDeploymentPolicy{
		Automatic:      policy.Automatic,
		WatchInclude:   include,
		WatchExclude:   exclude,
		CommitStatuses: policy.CommitStatuses,
		Revision:       1,
	}
	return writeGitPolicyTx(ctx, tx, environmentID, stored, now)
}

func writeGitPolicyTx(ctx context.Context, tx *sql.Tx, environmentID int64, p GitDeploymentPolicy, now int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO deploy_git_policies(environment_id,automatic,include_json,exclude_json,commit_statuses,promotion_only,revision,updated_at) VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(environment_id) DO UPDATE SET automatic=excluded.automatic,include_json=excluded.include_json,exclude_json=excluded.exclude_json,commit_statuses=excluded.commit_statuses,promotion_only=excluded.promotion_only,revision=excluded.revision,updated_at=excluded.updated_at`,
		environmentID, boolInt(p.Automatic), string(mustJSON(p.WatchInclude)), string(mustJSON(p.WatchExclude)), boolInt(p.CommitStatuses), boolInt(p.PromotionOnly), p.Revision, now)
	return err
}

func syncTriggerGitPolicyTx(ctx context.Context, tx *sql.Tx, environmentID int64, cfg TriggerConfig, now int64) error {
	if !branchTriggerConfig(cfg) || (cfg.WatchInclude == nil && cfg.WatchExclude == nil) {
		return nil
	}
	p, err := gitDeploymentPolicy(ctx, tx, environmentID)
	if err != nil {
		return err
	}
	p.WatchInclude, err = canonicalWatchPatterns(cfg.WatchInclude)
	if err != nil {
		return err
	}
	p.WatchExclude, err = canonicalWatchPatterns(cfg.WatchExclude)
	if err != nil {
		return err
	}
	p.Revision++
	return writeGitPolicyTx(ctx, tx, environmentID, p, now)
}

func automaticDeploymentTrigger(trigger TriggerKind) bool {
	return sourceTriggered(trigger) || trigger == TriggerSchedule || trigger == TriggerAPI || trigger == TriggerGenericHook
}

func validateGitPolicyAdmissionTx(ctx context.Context, tx *sql.Tx, req RunRequest) error {
	if req.Operation == OperationDeploy || req.Operation == OperationForceBuild {
		// Every trigger, a person's included: a build is exactly what a
		// promotion-only environment exists to refuse.
		p, err := gitDeploymentPolicy(ctx, tx, req.EnvironmentID)
		if err != nil {
			return err
		}
		if p.PromotionOnly {
			return ErrPromotionOnly
		}
	}
	if !automaticDeploymentTrigger(req.Trigger) || (req.Operation != OperationDeploy && req.Operation != OperationForceBuild && req.Operation != OperationRedeploy) {
		return nil
	}
	p, err := gitDeploymentPolicy(ctx, tx, req.EnvironmentID)
	if err != nil {
		return err
	}
	if !p.Automatic {
		return ErrGitManualOnly
	}
	if p.Conflict {
		return ErrGitPolicyConflict
	}
	if req.ExpectedGitPolicy != "" && req.ExpectedGitPolicy != p.Key() {
		return ErrRevisionConflict
	}
	return nil
}
