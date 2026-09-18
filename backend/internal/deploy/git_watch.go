package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const GitWatchInterval = 5 * time.Second

// ErrRefNotFound marks a manual deploy's requested branch or tag as absent
// from the remote — distinct from ErrSourceUnavailable, which means the
// remote itself could not be read at all.
var ErrRefNotFound = errors.New("git ref was not found on the remote")

// ErrRefNotApplicable marks a manual deploy's sourceRevision/ref override as
// sent against a project that cannot use one: anything other than a remote
// Git source kind (an image, Compose, blueprint or import project, or a
// local Git checkout with nothing to resolve a ref against).
var ErrRefNotApplicable = errors.New("a source revision or ref override does not apply to this deployment's source")

func IsRemoteGitSource(source DraftSourceConfig) bool {
	return (source.Kind == SourceGit || source.Kind == SourceCompose) &&
		(source.Mode == SourceModeConnectedRepository || source.Mode == SourceModeGitURL || source.Mode == SourceModeComposeGit)
}

// ResolveGitRevision observes the remote without refreshing a mirror or touching
// an operator checkout. The returned object id is frozen before work is queued.
func (a *HostSourceAnalyzer) ResolveGitRevision(ctx context.Context, source DraftSourceConfig) (string, error) {
	source = canonicalSourceConfig(source)
	if err := source.ValidateForDeployment(); err != nil {
		return "", err
	}
	if !IsRemoteGitSource(source) {
		return "", ErrInvalidSource
	}
	remote, _, err := remoteForSource(source)
	if err != nil {
		return "", err
	}
	root, cleanup, err := a.planningCacheRoot()
	if err != nil {
		return "", err
	}
	defer cleanup()
	environment, cleanupCredential, err := a.gitEnvironment(ctx, root, remote, source.CredentialID)
	if err != nil {
		return "", err
	}
	defer cleanupCredential()
	ref := source.Ref
	if ref == "" {
		ref = "main"
	}
	remoteRef, _ := planningGitRef(ref)
	resolveCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := runPlanningGit(resolveCtx, "", environment, "ls-remote", "--exit-code", "--refs", remote, remoteRef)
	if err != nil {
		return "", fmt.Errorf("%w: Git branch could not be read", ErrSourceUnavailable)
	}
	fields := strings.Fields(out)
	if len(fields) != 2 || fields[1] != remoteRef || !validGitObjectID(fields[0]) {
		return "", fmt.Errorf("%w: Git returned no immutable revision", ErrSourceUnavailable)
	}
	return fields[0], nil
}

// ResolveGitRef is ResolveGitRevision against an explicit branch or tag name
// instead of the source's own configured ref, for a manual deploy that
// targets a specific version without rewriting the environment's saved
// branch. Unlike ResolveGitRevision it distinguishes "no such ref" (git
// ls-remote --exit-code exits 2, having successfully talked to the remote)
// from "the remote could not be read at all" (any other failure), so the API
// can tell an operator which one happened instead of folding both into one
// unavailable source.
func (a *HostSourceAnalyzer) ResolveGitRef(ctx context.Context, source DraftSourceConfig, ref string) (string, error) {
	source = canonicalSourceConfig(source)
	if err := source.ValidateForDeployment(); err != nil {
		return "", err
	}
	if !IsRemoteGitSource(source) {
		return "", ErrInvalidSource
	}
	// ref reaches ls-remote as a bare argv element: one starting with "-"
	// would be read as a flag rather than a ref, and one containing a glob
	// character asks ls-remote to match a pattern instead of the exact name
	// the operator typed. validSourceRef's anchored, alphanumeric-first
	// pattern excludes both, so this is refused before any git subprocess
	// runs at all.
	if !validSourceRef(ref) {
		return "", fmt.Errorf("%w: %q is not a valid branch or tag name", ErrInvalidRef, ref)
	}
	remote, _, err := remoteForSource(source)
	if err != nil {
		return "", err
	}
	root, cleanup, err := a.planningCacheRoot()
	if err != nil {
		return "", err
	}
	defer cleanup()
	environment, cleanupCredential, err := a.gitEnvironment(ctx, root, remote, source.CredentialID)
	if err != nil {
		return "", err
	}
	defer cleanupCredential()
	remoteRef, _ := planningGitRef(ref)
	resolveCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := runPlanningGit(resolveCtx, "", environment, "ls-remote", "--exit-code", "--refs", remote, remoteRef)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 2 {
			return "", fmt.Errorf("%w: %q has no matching branch or tag", ErrRefNotFound, ref)
		}
		return "", fmt.Errorf("%w: Git remote could not be read", ErrSourceUnavailable)
	}
	fields := strings.Fields(out)
	if len(fields) != 2 || fields[1] != remoteRef || !validGitObjectID(fields[0]) {
		return "", fmt.Errorf("%w: %q has no matching branch or tag", ErrRefNotFound, ref)
	}
	return fields[0], nil
}

func (s *OrchestrationStore) SourceConfiguration(ctx context.Context, environmentID int64, revision int) (DraftSourceConfig, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT config_json FROM deploy_sources WHERE environment_id = ? AND revision = ?`, environmentID, revision).Scan(&raw)
	var source DraftSourceConfig
	if err != nil {
		return source, err
	}
	if json.Unmarshal([]byte(raw), &source) != nil {
		return source, ErrInvalidSource
	}
	return source, nil
}

type GitWatchTarget struct {
	ProjectID, EnvironmentID                    int64
	ProjectName                                 string
	PlanRevision                                int
	Source                                      DraftSourceConfig
	SourceKey, BaselineRevision, LatestRevision string
	LatestRunID                                 int64
	PolicyKey                                   string
	// LiveReleaseID gates automatic deployment: a branch change observed
	// while this release's runtime is stopped must not enqueue a deploy.
	LiveReleaseID int64
}

type GitWatchStatus struct {
	Automatic       bool                `json:"automatic"`
	Branch          string              `json:"branch,omitempty"`
	Status          string              `json:"status"`
	Revision        string              `json:"revision,omitempty"`
	RunID           int64               `json:"runId,omitempty"`
	CheckedAt       *time.Time          `json:"checkedAt,omitempty"`
	IntervalSeconds int                 `json:"intervalSeconds"`
	Reason          string              `json:"reason,omitempty"`
	Policy          GitDeploymentPolicy `json:"policy"`
}

type gitWatchCursor struct {
	SourceKey, Revision, Status, Reason, PolicyKey, BaselineRevision string
	Generation, RunID, CheckedAt                                     int64
}

func (s *OrchestrationStore) gitWatchTargets(ctx context.Context, projectID, environmentID int64) ([]GitWatchTarget, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.id, p.name, e.id, e.desired_revision, e.live_release_id, src.config_json, src.identity_json,
		       COALESCE((SELECT r.id FROM deploy_runs r WHERE r.environment_id = e.id
		         AND r.operation IN ('deploy','force_build') ORDER BY r.id DESC LIMIT 1), 0),
		       COALESCE((SELECT COALESCE(NULLIF(r.source_revision, ''), json_extract(rs.identity_json, '$.revision'))
		         FROM deploy_runs r JOIN deploy_sources rs ON rs.environment_id = r.environment_id AND rs.revision = r.plan_revision
		         WHERE r.environment_id = e.id AND rs.config_json = src.config_json
		           AND r.operation IN ('deploy','force_build') ORDER BY r.id DESC LIMIT 1), '')
		FROM deploy_environments e JOIN deploy_projects p ON p.id = e.project_id
		JOIN deploy_sources src ON src.environment_id = e.id AND src.revision = e.desired_revision
		JOIN deploy_build_plans b ON b.environment_id = e.id AND b.revision = e.desired_revision
		WHERE p.archived_at = 0 AND e.archived_at = 0 AND e.kind = 'production'
		  AND b.method <> 'legacy_compose' AND (? = 0 OR p.id = ?) AND (? = 0 OR e.id = ?)
		ORDER BY e.id`, projectID, projectID, environmentID, environmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []GitWatchTarget
	for rows.Next() {
		var target GitWatchTarget
		var config, identity string
		if err := rows.Scan(&target.ProjectID, &target.ProjectName, &target.EnvironmentID, &target.PlanRevision,
			&target.LiveReleaseID, &config, &identity, &target.LatestRunID, &target.LatestRevision); err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(config), &target.Source) != nil || !IsRemoteGitSource(target.Source) ||
			strings.HasPrefix(target.Source.Ref, "refs/tags/") {
			continue
		}
		var recorded SourceIdentity
		if json.Unmarshal([]byte(identity), &recorded) != nil {
			return nil, ErrInvalidSource
		}
		target.SourceKey = digestBytes([]byte(config))
		target.BaselineRevision = recorded.Revision
		if target.LatestRevision != "" {
			target.BaselineRevision = target.LatestRevision
		}
		result = append(result, target)
	}
	return result, rows.Err()
}

func (s *OrchestrationStore) gitWatchCursor(ctx context.Context, environmentID int64) (gitWatchCursor, error) {
	var cursor gitWatchCursor
	err := s.db.QueryRowContext(ctx, `SELECT source_key,revision,generation,run_id,status,checked_at,reason,policy_key,baseline_revision FROM deploy_git_watches WHERE environment_id = ?`, environmentID).
		Scan(&cursor.SourceKey, &cursor.Revision, &cursor.Generation, &cursor.RunID, &cursor.Status, &cursor.CheckedAt, &cursor.Reason, &cursor.PolicyKey, &cursor.BaselineRevision)
	return cursor, err
}

func (s *OrchestrationStore) GitWatchStatus(ctx context.Context, projectID, environmentID int64) (GitWatchStatus, error) {
	status := GitWatchStatus{Status: "not_applicable", IntervalSeconds: int(GitWatchInterval / time.Second)}
	if _, err := s.EnvironmentExecutionTarget(ctx, projectID, environmentID); err != nil {
		return status, err
	}
	policy, err := gitDeploymentPolicy(ctx, s.db, environmentID)
	if err != nil {
		return status, err
	}
	status.Policy = policy
	targets, err := s.gitWatchTargets(ctx, projectID, environmentID)
	if err != nil || len(targets) == 0 {
		return status, err
	}
	target := targets[0]
	status.Automatic, status.Branch, status.Status = true, target.Source.Ref, "starting"
	if status.Branch == "" {
		status.Branch = "main"
	}
	status.Automatic = policy.Automatic && !policy.Conflict
	if !policy.Automatic {
		status.Status, status.Reason = "manual_only", "manual_only"
		return status, nil
	}
	if policy.Conflict {
		status.Status, status.Reason = "policy_conflict", "policy_conflict"
		return status, nil
	}
	if target.LatestRunID == 0 {
		status.Status = "awaiting_first_deployment"
		return status, nil
	}
	cursor, err := s.gitWatchCursor(ctx, environmentID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && cursor.SourceKey != target.SourceKey) {
		return status, nil
	}
	if err != nil {
		return status, err
	}
	status.Status, status.Revision, status.RunID = cursor.Status, cursor.Revision, cursor.RunID
	status.Reason = cursor.Reason
	status.CheckedAt = unixTimePtr(cursor.CheckedAt)
	if cursor.CheckedAt != 0 && s.now().UTC().Unix()-cursor.CheckedAt > 30 {
		status.Status = "stale"
	}
	return status, nil
}

type GitRevisionResolver interface {
	ResolveGitRevision(context.Context, DraftSourceConfig) (string, error)
}

type GitWatchDispatch func(context.Context, GitWatchTarget, string, string, []string) (*EngineRun, error)

type GitWatcher struct {
	store    *OrchestrationStore
	sources  GitRevisionResolver
	dispatch GitWatchDispatch
	cancel   context.CancelFunc
	done     chan struct{}
}

func NewGitWatcher(store *OrchestrationStore, sources GitRevisionResolver, dispatch GitWatchDispatch) *GitWatcher {
	return &GitWatcher{store: store, sources: sources, dispatch: dispatch}
}

func (w *GitWatcher) Start(ctx context.Context) {
	if w == nil || w.cancel != nil {
		return
	}
	ctx, w.cancel = context.WithCancel(ctx)
	w.done = make(chan struct{})
	go func() {
		defer close(w.done)
		ticker := time.NewTicker(GitWatchInterval)
		defer ticker.Stop()
		for {
			_ = w.poll(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (w *GitWatcher) Stop() {
	if w == nil || w.cancel == nil {
		return
	}
	w.cancel()
	<-w.done
	w.cancel = nil
}

func (w *GitWatcher) poll(ctx context.Context) error {
	targets, err := w.store.gitWatchTargets(ctx, 0, 0)
	if err != nil {
		return err
	}
	var group sync.WaitGroup
	slots := make(chan struct{}, 4)
	for _, target := range targets {
		// Creation/import may still be saving variables. Monitoring starts only
		// after the project's first explicit deployment has frozen its inputs.
		if target.LatestRunID == 0 {
			continue
		}
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			group.Wait()
			return ctx.Err()
		}
		group.Add(1)
		go func() {
			defer group.Done()
			defer func() { <-slots }()
			_ = w.check(ctx, target)
		}()
	}
	group.Wait()
	return nil
}

func (w *GitWatcher) check(ctx context.Context, target GitWatchTarget) error {
	decision, err := w.evaluate(ctx, target, "")
	if err != nil {
		return err
	}
	var dispatchErr error
	if decision.Allowed {
		key := "git:" + digestBytes([]byte(fmt.Sprintf("%s:%d:%s:%s", target.SourceKey, decision.cursor.Generation, decision.Revision, decision.Target.PolicyKey)))
		run, err := w.dispatch(ctx, decision.Target, decision.Revision, key, decision.ChangedPaths)
		dispatchErr = err
		if err == nil {
			decision.RunID = run.ID
		}
	}
	return w.RecordDecision(ctx, decision, dispatchErr)
}

type GitDeploymentDecision struct {
	Target           GitWatchTarget
	Revision, Reason string
	Allowed          bool
	RunID            int64
	// ChangedPaths is set only when a watch-path filter made the watcher
	// compute the full tree diff against the baseline; it is what
	// supersession compares two automatic runs by; leaving it nil (the
	// unfiltered common case) means "unscoped" rather than "no changes".
	ChangedPaths []string
	cursor       gitWatchCursor
	record       bool
	unavailable  bool
}

// EvaluateWebhook uses the same remote observation and complete tree diff as
// polling. Late deliveries cannot move the environment back to an old commit.
func (w *GitWatcher) EvaluateWebhook(ctx context.Context, projectID, environmentID int64, event ProviderEvent) (*GitDeploymentDecision, error) {
	if w == nil {
		return nil, ErrSourceUnavailable
	}
	targets, err := w.store.gitWatchTargets(ctx, projectID, environmentID)
	if err != nil || len(targets) == 0 {
		return nil, err
	}
	target := targets[0]
	_, repository, err := remoteForSource(target.Source)
	if err != nil {
		return nil, err
	}
	if event.Repository != "" && !strings.EqualFold(strings.TrimSuffix(event.Repository, ".git"), repository) {
		return nil, ErrWrongRepository
	}
	want, _ := planningGitRef(target.Source.Ref)
	if event.Ref != "" {
		got, _ := planningGitRef(event.Ref)
		if got != want {
			return nil, ErrWrongRef
		}
	}
	if event.Revision != "" && !validGitObjectID(event.Revision) {
		return nil, ErrInvalidRef
	}
	return w.evaluate(ctx, target, event.Revision)
}

func (w *GitWatcher) evaluate(ctx context.Context, target GitWatchTarget, expectedRevision string) (*GitDeploymentDecision, error) {
	_, err := w.store.db.ExecContext(ctx, `INSERT INTO deploy_git_watches(environment_id,source_key,revision)
		VALUES(?,?,?) ON CONFLICT(environment_id) DO UPDATE SET source_key=excluded.source_key,
		revision=excluded.revision,generation=generation+1,run_id=0,status='watching',checked_at=0,reason='',policy_key=''
		WHERE deploy_git_watches.source_key <> excluded.source_key`, target.EnvironmentID, target.SourceKey, target.BaselineRevision)
	if err != nil {
		return nil, err
	}
	cursor, err := w.store.gitWatchCursor(ctx, target.EnvironmentID)
	if err != nil {
		return nil, err
	}
	policy, err := gitDeploymentPolicy(ctx, w.store.db, target.EnvironmentID)
	if err != nil {
		return nil, err
	}
	target.PolicyKey = policy.Key()
	d := &GitDeploymentDecision{Target: target, Revision: cursor.Revision, Reason: cursor.Reason, RunID: cursor.RunID, cursor: cursor, record: true}
	if !policy.Automatic {
		d.Reason = "manual_only"
		return d, nil
	}
	if policy.Conflict {
		d.Reason = "policy_conflict"
		return d, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	revision, resolveErr := w.sources.ResolveGitRevision(ctx, target.Source)
	if resolveErr != nil || !validGitObjectID(revision) {
		d.Reason, d.unavailable = "source_unavailable", true
		return d, nil
	}
	if expectedRevision != "" && expectedRevision != revision {
		d.Revision, d.Reason, d.record = expectedRevision, "superseded_revision", false
		return d, nil
	}
	d.Revision = revision
	if revision == cursor.Revision && cursor.PolicyKey == target.PolicyKey && cursor.BaselineRevision == target.BaselineRevision && cursor.Status == "watching" {
		return d, nil
	}
	if revision == target.LatestRevision && target.LatestRunID != 0 {
		d.Reason, d.RunID = "already_attempted", target.LatestRunID
		return d, nil
	}
	if target.LatestRunID != 0 && revision == cursor.Revision && cursor.PolicyKey == "" {
		d.Reason = "unchanged_revision"
		return d, nil
	}
	d.Reason = "branch_changed"
	if len(policy.WatchInclude)+len(policy.WatchExclude) > 0 {
		resolver, ok := w.sources.(GitChangedPathResolver)
		if !ok || !validGitObjectID(target.BaselineRevision) {
			d.Reason, d.unavailable = "changes_unavailable", true
			return d, nil
		}
		paths, err := resolver.ResolveGitChangedPaths(ctx, target.Source, target.BaselineRevision, revision)
		if err != nil {
			d.Reason, d.unavailable = "changes_unavailable", true
			return d, nil
		}
		if len(paths) == 0 || !MatchWatchPaths(paths, policy.WatchInclude, policy.WatchExclude) {
			d.Reason = "watch_paths_ignored"
			return d, nil
		}
		d.ChangedPaths = paths
		d.Reason = "watched_paths_changed"
	}
	// A stop leaves the live release in place; a branch change is still
	// observed and its cursor still advances (d.record stays true, unlike the
	// manual_only return above, which leaves d.Revision at the cursor's old
	// value), but must not enqueue a deployment onto a runtime the operator
	// deliberately stopped. That means a commit pushed while stopped is
	// recorded seen and deployed by the next push, not retroactively by
	// starting the runtime back up — starting it is a runtime action with no
	// hook into the watcher at all.
	if target.LiveReleaseID != 0 {
		if runtime, err := w.store.RuntimeForRelease(ctx, target.LiveReleaseID); err == nil && runtime.State == "stopped" {
			d.Reason = "stopped"
			return d, nil
		}
	}
	d.Allowed = true
	return d, nil
}

func (w *GitWatcher) RecordDecision(ctx context.Context, d *GitDeploymentDecision, dispatchErr error) error {
	if d == nil || !d.record {
		return nil
	}
	status, revision, reason := "watching", d.Revision, d.Reason
	if d.unavailable || dispatchErr != nil {
		status, revision = "unavailable", d.cursor.Revision
		if dispatchErr != nil {
			reason = "enqueue_failed"
		}
	}
	_, err := w.store.db.ExecContext(ctx, `UPDATE deploy_git_watches
		SET revision=?,generation=generation+CASE WHEN revision<>? OR policy_key<>? OR baseline_revision<>? THEN 1 ELSE 0 END,run_id=?,status=?,reason=?,policy_key=?,baseline_revision=?,checked_at=?
		WHERE environment_id=? AND source_key=? AND generation=?`, revision, revision, d.Target.PolicyKey, d.Target.BaselineRevision, d.RunID, status, reason, d.Target.PolicyKey,
		d.Target.BaselineRevision, w.store.now().UTC().Unix(), d.Target.EnvironmentID, d.Target.SourceKey, d.cursor.Generation)
	return err
}
