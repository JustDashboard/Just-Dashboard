package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const GitWatchInterval = 5 * time.Second

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
}

type GitWatchStatus struct {
	Automatic       bool       `json:"automatic"`
	Branch          string     `json:"branch,omitempty"`
	Status          string     `json:"status"`
	Revision        string     `json:"revision,omitempty"`
	RunID           int64      `json:"runId,omitempty"`
	CheckedAt       *time.Time `json:"checkedAt,omitempty"`
	IntervalSeconds int        `json:"intervalSeconds"`
}

type gitWatchCursor struct {
	SourceKey, Revision, Status  string
	Generation, RunID, CheckedAt int64
}

func (s *OrchestrationStore) gitWatchTargets(ctx context.Context, projectID, environmentID int64) ([]GitWatchTarget, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.id, p.name, e.id, e.desired_revision, src.config_json, src.identity_json,
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
			&config, &identity, &target.LatestRunID, &target.LatestRevision); err != nil {
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
	err := s.db.QueryRowContext(ctx, `SELECT source_key,revision,generation,run_id,status,checked_at FROM deploy_git_watches WHERE environment_id = ?`, environmentID).
		Scan(&cursor.SourceKey, &cursor.Revision, &cursor.Generation, &cursor.RunID, &cursor.Status, &cursor.CheckedAt)
	return cursor, err
}

func (s *OrchestrationStore) GitWatchStatus(ctx context.Context, projectID, environmentID int64) (GitWatchStatus, error) {
	status := GitWatchStatus{Status: "not_applicable", IntervalSeconds: int(GitWatchInterval / time.Second)}
	if _, err := s.EnvironmentExecutionTarget(ctx, projectID, environmentID); err != nil {
		return status, err
	}
	targets, err := s.gitWatchTargets(ctx, projectID, environmentID)
	if err != nil || len(targets) == 0 {
		return status, err
	}
	target := targets[0]
	status.Automatic, status.Branch, status.Status = true, target.Source.Ref, "starting"
	if status.Branch == "" {
		status.Branch = "main"
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
	status.CheckedAt = unixTimePtr(cursor.CheckedAt)
	if cursor.CheckedAt != 0 && s.now().UTC().Unix()-cursor.CheckedAt > 30 {
		status.Status = "stale"
	}
	return status, nil
}

type GitRevisionResolver interface {
	ResolveGitRevision(context.Context, DraftSourceConfig) (string, error)
}

type GitWatchDispatch func(context.Context, GitWatchTarget, string, string) (*EngineRun, error)

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
	_, err := w.store.db.ExecContext(ctx, `INSERT INTO deploy_git_watches(environment_id,source_key,revision)
		VALUES(?,?,?) ON CONFLICT(environment_id) DO UPDATE SET source_key=excluded.source_key,
		revision=excluded.revision,generation=generation+1,run_id=0,status='watching',checked_at=0
		WHERE deploy_git_watches.source_key <> excluded.source_key`, target.EnvironmentID, target.SourceKey, target.BaselineRevision)
	if err != nil {
		return err
	}
	cursor, err := w.store.gitWatchCursor(ctx, target.EnvironmentID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	revision, resolveErr := w.sources.ResolveGitRevision(ctx, target.Source)
	status, runID := "watching", cursor.RunID
	if resolveErr == nil && !validGitObjectID(revision) {
		resolveErr = ErrInvalidRef
	}
	if resolveErr == nil && revision != cursor.Revision {
		if revision == target.LatestRevision {
			runID = target.LatestRunID
		} else {
			// A crash after enqueue reuses the same key. Remember attempted commits,
			// including failures, so a broken push is not rebuilt every five seconds.
			key := "git:" + digestBytes([]byte(fmt.Sprintf("%s:%d:%s", target.SourceKey, cursor.Generation, revision)))
			run, dispatchErr := w.dispatch(ctx, target, revision, key)
			resolveErr = dispatchErr
			if dispatchErr == nil {
				runID = run.ID
			}
		}
	}
	if resolveErr != nil {
		status, revision = "unavailable", cursor.Revision
	}
	_, err = w.store.db.ExecContext(ctx, `UPDATE deploy_git_watches
		SET revision=?,generation=generation+CASE WHEN revision<>? THEN 1 ELSE 0 END,run_id=?,status=?,checked_at=?
		WHERE environment_id=? AND source_key=? AND generation=?`, revision, revision, runID, status,
		w.store.now().UTC().Unix(), target.EnvironmentID, target.SourceKey, cursor.Generation)
	return err
}
