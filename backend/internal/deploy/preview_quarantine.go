package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

const previewQuarantineReason = "This preview predates verified isolation. Its previous runtime must be stopped before an approved deployment can run."

type PreviewQuarantineTarget struct {
	EnvironmentID       int64
	ReleaseIDs          []int64
	ContainerIDs        []string
	IncompleteOwnership bool
}

type PreviewQuarantineOwner interface {
	QuarantinePreview(context.Context, PreviewQuarantineTarget) error
}

type PreviewRouteRemover interface {
	RemoveDeploymentRoute(context.Context, string) error
}

// PreparePreviewQuarantines runs before the deployment engine starts. Persisting
// the block and fencing old work precedes any Docker or proxy side effect.
func (s *OrchestrationStore) PreparePreviewQuarantines(ctx context.Context) ([]int64, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT e.id,COALESCE(b.config_json,'{}'),COALESCE(r.config_json,'{}'),COALESCE(src.identity_json,'{}')
		FROM deploy_environments e
		LEFT JOIN deploy_build_plans b ON b.environment_id=e.id AND b.revision=e.desired_revision
		LEFT JOIN deploy_runtime_plans r ON r.environment_id=e.id AND r.revision=e.desired_revision
		LEFT JOIN deploy_sources src ON src.environment_id=e.id AND src.revision=e.desired_revision
		WHERE e.kind='preview' AND NOT EXISTS(SELECT 1 FROM deploy_preview_quarantines q WHERE q.environment_id=e.id)`)
	if err != nil {
		return nil, err
	}
	type candidate struct {
		id                     int64
		build, runtime, source string
	}
	var candidates []candidate
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.id, &item.build, &item.runtime, &item.source); err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var quarantined []int64
	var events []RunEvent
	now := s.now().UTC()
	for _, item := range candidates {
		var build BuildPlanConfig
		var runtime RuntimePlanConfig
		var source SourceIdentity
		safe := json.Unmarshal([]byte(item.build), &build) == nil &&
			json.Unmarshal([]byte(item.runtime), &runtime) == nil &&
			json.Unmarshal([]byte(item.source), &source) == nil &&
			validatePreviewPlan(item.id, build, runtime) == nil
		if safe {
			approved, err := reviewedPreviewRevisionTx(ctx, tx, item.id, source.Revision)
			if err != nil {
				return nil, err
			}
			safe = approved
		}
		if safe {
			// A new desired plan cannot attest to containers from an older release.
			var unsafeReleases int
			err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM deploy_releases rel
				LEFT JOIN deploy_runtime_plans rp ON rp.id=rel.runtime_plan_id
				WHERE rel.environment_id=? AND rel.state<>'quarantined'
				AND COALESCE(json_extract(rp.config_json,'$.previewIsolation'),0)<>1`, item.id).Scan(&unsafeReleases)
			if err != nil {
				return nil, err
			}
			safe = unsafeReleases == 0
		}
		if safe {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO deploy_preview_quarantines(environment_id,status,reason,created_at,updated_at) VALUES(?,'pending',?,?,?)`, item.id, previewQuarantineReason, now.Unix(), now.Unix()); err != nil {
			return nil, err
		}
		runEvents, err := failQuarantinedPreviewRunsTx(ctx, tx, item.id, now)
		if err != nil {
			return nil, err
		}
		events = append(events, runEvents...)
		quarantined = append(quarantined, item.id)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.publish(events)
	return quarantined, nil
}

func reviewedPreviewRevisionTx(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, environmentID int64, revision string) (bool, error) {
	if !gitObjectIDRE.MatchString(revision) {
		return false, nil
	}
	var count int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deploy_preview_approvals a
		JOIN deploy_preview_refs p ON p.trigger_id=a.trigger_id AND p.provider_ref=a.provider_ref
		WHERE p.environment_id=? AND a.revision=? AND a.approved_by<>''`, environmentID, revision).Scan(&count)
	return count == 1, err
}

func failQuarantinedPreviewRunsTx(ctx context.Context, tx *sql.Tx, environmentID int64, now time.Time) ([]RunEvent, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,state FROM deploy_runs WHERE environment_id=? AND state NOT IN ('succeeded','failed','cancelled','rolled_back','superseded')`, environmentID)
	if err != nil {
		return nil, err
	}
	type activeRun struct {
		id    int64
		state RunState
	}
	var runs []activeRun
	for rows.Next() {
		var run activeRun
		if err := rows.Scan(&run.id, &run.state); err != nil {
			rows.Close()
			return nil, err
		}
		runs = append(runs, run)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var events []RunEvent
	for _, run := range runs {
		states := []RunState{RunFailed}
		if run.state == RunActivating {
			states = []RunState{RunFailedActivation, RunFailed}
		}
		for _, state := range states {
			event, err := appendEventTx(ctx, tx, now, run.id, 0, EventRunState, "status", "", mustJSON(map[string]any{
				"from": run.state, "state": state, "code": "preview_quarantined", "reason": previewQuarantineReason,
			}))
			if err != nil {
				return nil, err
			}
			events = append(events, event)
			run.state = state
		}
		if _, err := tx.ExecContext(ctx, `UPDATE deploy_runs SET state='failed',status='failed',ended_at=?,terminal_code='preview_quarantined',terminal_reason=?,lease_token='',lease_until=0 WHERE id=?`, now.Unix(), previewQuarantineReason, run.id); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE deploy_steps SET status='failed',ended_at=?,error_code='preview_quarantined',error_message=? WHERE run_id=? AND status='running'`, now.Unix(), previewQuarantineReason, run.id); err != nil {
			return nil, err
		}
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM deploy_queue_leases WHERE environment_id=?`, environmentID)
	return events, err
}

func (s *OrchestrationStore) pendingPreviewQuarantines(ctx context.Context) ([]PreviewQuarantineTarget, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT q.environment_id,COALESCE(r.id,0) FROM deploy_preview_quarantines q
		LEFT JOIN deploy_releases r ON r.environment_id=q.environment_id WHERE q.status='pending' ORDER BY q.environment_id,r.id`)
	if err != nil {
		return nil, err
	}
	targets := []PreviewQuarantineTarget{}
	for rows.Next() {
		var environmentID, releaseID int64
		if err := rows.Scan(&environmentID, &releaseID); err != nil {
			rows.Close()
			return nil, err
		}
		if len(targets) == 0 || targets[len(targets)-1].EnvironmentID != environmentID {
			targets = append(targets, PreviewQuarantineTarget{EnvironmentID: environmentID})
		}
		if releaseID != 0 {
			targets[len(targets)-1].ReleaseIDs = append(targets[len(targets)-1].ReleaseIDs, releaseID)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for index := range targets {
		target := &targets[index]
		runtimes, err := s.db.QueryContext(ctx, `SELECT kind,runtime_id,metadata_json FROM deploy_release_runtimes WHERE environment_id=?`, target.EnvironmentID)
		if err != nil {
			return nil, err
		}
		for runtimes.Next() {
			var kind, id, metadata string
			if err := runtimes.Scan(&kind, &id, &metadata); err != nil {
				runtimes.Close()
				return nil, err
			}
			if kind == "container" {
				target.ContainerIDs = append(target.ContainerIDs, id)
			} else if kind == "compose" {
				var recorded dockerReleaseRuntimeMetadata
				if json.Unmarshal([]byte(metadata), &recorded) != nil || (len(recorded.ContainerIDs) == 0 && recorded.PrimaryContainerID == "") {
					target.IncompleteOwnership = true
					continue
				}
				target.ContainerIDs = append(target.ContainerIDs, recorded.ContainerIDs...)
				if recorded.PrimaryContainerID != "" {
					target.ContainerIDs = append(target.ContainerIDs, recorded.PrimaryContainerID)
				}
			} else {
				target.IncompleteOwnership = true
			}
		}
		err = runtimes.Err()
		runtimes.Close()
		if err != nil {
			return nil, err
		}
	}
	return targets, nil
}

func (s *OrchestrationStore) completePreviewQuarantine(ctx context.Context, environmentID int64) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.now().UTC().Unix()
	result, err := tx.ExecContext(ctx, `UPDATE deploy_preview_quarantines SET status='quarantined',updated_at=? WHERE environment_id=? AND status='pending'`, now, environmentID)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return ErrPreviewIsolation
	}
	for _, statement := range []string{
		`UPDATE deploy_release_runtimes SET state='quarantined',updated_at=? WHERE environment_id=?`,
		`UPDATE deploy_releases SET state='quarantined',retired_at=? WHERE environment_id=?`,
		`UPDATE deploy_environments SET live_release_id=0,updated_at=? WHERE id=? AND kind='preview'`,
	} {
		if _, err := tx.ExecContext(ctx, statement, now, environmentID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func previewQuarantineAdmissionTx(ctx context.Context, tx *sql.Tx, environmentID int64) error {
	var blocked int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM deploy_preview_quarantines WHERE environment_id=? AND status='pending'`, environmentID).Scan(&blocked); err != nil {
		return err
	}
	if blocked != 0 {
		return fmt.Errorf("%w: previous runtime isolation is incomplete", ErrPreviewIsolation)
	}
	return nil
}

// Quarantine retries remain separate from deployment work: an unavailable Docker
// daemon must leave a durable admission block without disabling the dashboard.
type PreviewQuarantineController struct {
	store       *OrchestrationStore
	owner       PreviewQuarantineOwner
	routes      PreviewRouteRemover
	audit       func(context.Context, int64, string, bool)
	report      func(error)
	mu          sync.Mutex
	reconcileMu sync.Mutex
	stop        context.CancelFunc
	done        chan struct{}
}

func NewPreviewQuarantineController(store *OrchestrationStore, owner PreviewQuarantineOwner, routes PreviewRouteRemover, audit func(context.Context, int64, string, bool), report func(error)) *PreviewQuarantineController {
	return &PreviewQuarantineController{store: store, owner: owner, routes: routes, audit: audit, report: report}
}

func (c *PreviewQuarantineController) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stop != nil {
		return nil
	}
	ids, err := c.store.PreparePreviewQuarantines(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		c.record(ctx, id, "prepare", true)
	}
	c.attempt(ctx)
	workerCtx, stop := context.WithCancel(ctx)
	c.stop, c.done = stop, make(chan struct{})
	go func() {
		defer close(c.done)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.C:
				c.attempt(workerCtx)
			}
		}
	}()
	return nil
}

func (c *PreviewQuarantineController) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stop != nil {
		c.stop()
		<-c.done
		c.stop = nil
	}
}

func (c *PreviewQuarantineController) attempt(ctx context.Context) {
	bounded, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if err := c.Reconcile(bounded); err != nil && c.report != nil {
		c.report(err)
	}
}

func (c *PreviewQuarantineController) Reconcile(ctx context.Context) error {
	c.reconcileMu.Lock()
	defer c.reconcileMu.Unlock()
	targets, err := c.store.pendingPreviewQuarantines(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, target := range targets {
		if c.owner == nil || c.routes == nil {
			return ErrRuntimeUnavailable
		}
		stopErr := c.owner.QuarantinePreview(ctx, target)
		c.record(ctx, target.EnvironmentID, "stop", stopErr == nil)
		// Withdraw the route even when stopping a runtime needs another attempt.
		routeErr := c.routes.RemoveDeploymentRoute(ctx, deploymentRouteName(target.EnvironmentID))
		c.record(ctx, target.EnvironmentID, "route", routeErr == nil)
		if err := errors.Join(stopErr, routeErr); err != nil {
			failures = append(failures, fmt.Errorf("preview environment %d isolation incomplete: %w", target.EnvironmentID, err))
			continue
		}
		err := c.store.completePreviewQuarantine(ctx, target.EnvironmentID)
		c.record(ctx, target.EnvironmentID, "complete", err == nil)
		if err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (c *PreviewQuarantineController) record(ctx context.Context, environmentID int64, phase string, success bool) {
	if c.audit != nil {
		bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		c.audit(bounded, environmentID, phase, success)
	}
}
