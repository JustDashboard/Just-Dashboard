package deploy

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type PreviewApproval struct {
	ID             int64         `json:"id"`
	TriggerID      int64         `json:"triggerId"`
	ProviderRef    string        `json:"providerRef"`
	Revision       string        `json:"revision"`
	Repository     string        `json:"repository"`
	HeadRepository string        `json:"headRepository"`
	HeadRef        string        `json:"headRef,omitempty"` // absent where the delivery predates recording it
	Author         string        `json:"author"`
	State          string        `json:"state"`
	ApprovedBy     string        `json:"approvedBy,omitempty"`
	Generation     int64         `json:"generation"`
	Configured     bool          `json:"configured"`
	UpdatedAt      time.Time     `json:"updatedAt"`
	Event          ProviderEvent `json:"-"`
}

// Signed delivery proves which provider sent the code, not who has reviewed
// it. Each immutable revision needs its own administrator approval.
func (s *AutomationStore) requirePreviewApproval(ctx context.Context, trigger *Trigger, event ProviderEvent) error {
	ref := strconv.Itoa(event.PreviewNumber)
	now := s.now().UTC().Unix()
	if event.PreviewClosed {
		_, err := s.db.ExecContext(ctx, `UPDATE deploy_preview_approvals SET state='closed',updated_at=? WHERE trigger_id=? AND provider_ref=?`, now, trigger.ID, ref)
		return err
	}
	if !gitObjectIDRE.MatchString(event.Revision) || event.PreviewRef == "" {
		return fmt.Errorf("%w: preview requires an immutable provider revision and ref", ErrWrongEvent)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := recordPreviewRevisionTx(ctx, tx, trigger.ID, ref, event, now); err != nil {
		return err
	}
	var state string

	if err := tx.QueryRowContext(ctx, `SELECT state FROM deploy_preview_approvals WHERE trigger_id=? AND provider_ref=? AND revision=?`, trigger.ID, ref, event.Revision).Scan(&state); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if state != "approved" {
		return ErrPreviewApproval
	}
	return nil
}

func scanPreviewApproval(row interface{ Scan(...any) error }) (*PreviewApproval, error) {
	var approval PreviewApproval
	var encoded string
	var updated int64
	if err := row.Scan(&approval.ID, &approval.TriggerID, &approval.ProviderRef, &approval.Revision,
		&encoded, &approval.State, &approval.ApprovedBy, &approval.Generation, &updated, &approval.Configured); err != nil {
		return nil, err
	}
	if json.Unmarshal([]byte(encoded), &approval.Event) != nil {
		return nil, ErrInvalidPlan
	}
	approval.Repository, approval.HeadRepository, approval.Author = approval.Event.Repository, approval.Event.HeadRepository, approval.Event.Author
	approval.HeadRef = approval.Event.HeadRef
	approval.UpdatedAt = time.Unix(updated, 0).UTC()
	return &approval, nil
}

const previewApprovalColumns = `a.id,a.trigger_id,a.provider_ref,a.revision,a.event_json,a.state,a.approved_by,a.generation,a.updated_at,
EXISTS(SELECT 1 FROM deploy_preview_refs p JOIN deploy_environments e ON e.id=p.environment_id JOIN deploy_sources s ON s.environment_id=e.id AND s.revision=e.desired_revision JOIN deploy_runtime_plans r ON r.environment_id=e.id AND r.revision=e.desired_revision WHERE p.trigger_id=a.trigger_id AND p.provider_ref=a.provider_ref AND p.state='open' AND e.archived_at=0 AND json_extract(s.identity_json,'$.revision')=a.revision AND json_extract(r.config_json,'$.previewIsolation')=1 AND NOT EXISTS(SELECT 1 FROM deploy_preview_quarantines q WHERE q.environment_id=e.id AND q.status<>'cleared'))`

func (s *AutomationStore) ApprovedPreviewRevision(ctx context.Context, environmentID int64, revision int) (string, error) {
	var sourceRevision string
	err := s.db.QueryRowContext(ctx, `SELECT a.revision FROM deploy_preview_refs p JOIN deploy_sources s ON s.environment_id=p.environment_id JOIN deploy_preview_approvals a ON a.trigger_id=p.trigger_id AND a.provider_ref=p.provider_ref AND a.revision=json_extract(s.identity_json,'$.revision') WHERE p.environment_id=? AND s.revision=? AND p.state='open' AND a.state='approved'`, environmentID, revision).Scan(&sourceRevision)
	if err != nil {
		return "", ErrPreviewApproval
	}
	return sourceRevision, nil
}

func (s *AutomationStore) ListPreviewApprovals(ctx context.Context, projectID int64) ([]PreviewApproval, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+previewApprovalColumns+` FROM deploy_preview_approvals a JOIN deploy_triggers t ON t.id=a.trigger_id JOIN deploy_environments e ON e.id=t.environment_id WHERE e.project_id=? AND a.state IN ('pending','approved') ORDER BY a.id DESC LIMIT 200`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []PreviewApproval{}
	for rows.Next() {
		approval, err := scanPreviewApproval(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *approval)
	}
	return result, rows.Err()
}

func (s *AutomationStore) ApprovePreview(ctx context.Context, projectID, approvalID int64, revision, actor string) (*PreviewApproval, error) {
	if !gitObjectIDRE.MatchString(revision) || strings.TrimSpace(actor) == "" {
		return nil, ErrPreviewApproval
	}
	result, err := s.db.ExecContext(ctx, `UPDATE deploy_preview_approvals SET state='approved',approved_by=?,updated_at=? WHERE id=? AND revision=? AND state IN ('pending','approved') AND trigger_id IN (SELECT t.id FROM deploy_triggers t JOIN deploy_environments e ON e.id=t.environment_id JOIN deploy_projects p ON p.id=e.project_id WHERE e.project_id=? AND e.archived_at=0 AND p.archived_at=0 AND t.enabled=1)`, actor, s.now().UTC().Unix(), approvalID, revision, projectID)
	if err != nil {
		return nil, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return nil, ErrPreviewApproval
	}
	return scanPreviewApproval(s.db.QueryRowContext(ctx, `SELECT `+previewApprovalColumns+` FROM deploy_preview_approvals a WHERE a.id=?`, approvalID))
}

// RejectPreview refuses a pending or previously approved revision. It only
// ever moves a row out of the active set ('pending'/'approved'), so
// ApprovePreview's own WHERE clause already refuses a rejected revision
// without needing to know about the new state; the trigger's next event for
// this pull request inserts a fresh row at the new revision, independent of
// this one's fate.
func (s *AutomationStore) RejectPreview(ctx context.Context, projectID, approvalID int64, revision, actor string) (*PreviewApproval, error) {
	if !gitObjectIDRE.MatchString(revision) || strings.TrimSpace(actor) == "" {
		return nil, ErrPreviewApproval
	}
	result, err := s.db.ExecContext(ctx, `UPDATE deploy_preview_approvals SET state='rejected',approved_by=?,updated_at=? WHERE id=? AND revision=? AND state IN ('pending','approved') AND trigger_id IN (SELECT t.id FROM deploy_triggers t JOIN deploy_environments e ON e.id=t.environment_id JOIN deploy_projects p ON p.id=e.project_id WHERE e.project_id=? AND e.archived_at=0 AND p.archived_at=0 AND t.enabled=1)`, actor, s.now().UTC().Unix(), approvalID, revision, projectID)
	if err != nil {
		return nil, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return nil, ErrPreviewApproval
	}
	return scanPreviewApproval(s.db.QueryRowContext(ctx, `SELECT `+previewApprovalColumns+` FROM deploy_preview_approvals a WHERE a.id=?`, approvalID))
}

func previewVolumeName(environmentID int64, target string) string {
	digest := sha256.Sum256([]byte(target))
	return fmt.Sprintf("jd-preview-e%d-%s", environmentID, hex.EncodeToString(digest[:8]))
}

func createIsolatedPreviewPlansTx(ctx context.Context, tx *sql.Tx, sourceID int64, sourceRevision int, environmentID int64, revision int, now int64) error {
	var buildRaw, runtimeRaw, method, evidence string
	if err := tx.QueryRowContext(ctx, `SELECT b.method,b.config_json,b.evidence_json,r.config_json FROM deploy_build_plans b JOIN deploy_runtime_plans r ON r.environment_id=b.environment_id AND r.revision=b.revision WHERE b.environment_id=? AND b.revision=?`, sourceID, sourceRevision).Scan(&method, &buildRaw, &evidence, &runtimeRaw); err != nil {
		return err
	}
	var build BuildPlanConfig
	var runtime RuntimePlanConfig
	if json.Unmarshal([]byte(buildRaw), &build) != nil || json.Unmarshal([]byte(runtimeRaw), &runtime) != nil {
		return ErrInvalidPlan
	}
	build.Method = BuildMethod(method)
	if build.Method == BuildCompose || runtime.HostNetwork || runtime.Privileged || len(runtime.Capabilities) > 0 || len(runtime.Devices) > 0 {
		return fmt.Errorf("%w: previews currently require a container build without host access", ErrPreviewIsolation)
	}
	var quarantined int
	if sourceID == environmentID {
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM deploy_preview_quarantines WHERE environment_id=? AND status='quarantined'`, environmentID).Scan(&quarantined); err != nil {
			return err
		}
	}
	if sourceID == environmentID && (!runtime.PreviewIsolation || quarantined != 0) {
		// Existing preview copies predate isolation. Their variables and links
		// have no provenance proving they are distinct from production.
		if _, err := tx.ExecContext(ctx, `UPDATE deploy_variable_revisions SET active=0 WHERE environment_id=?`, environmentID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM deploy_dependencies WHERE environment_id=? AND release_id=0 AND kind<>'domain'`, environmentID); err != nil {
			return err
		}
	}
	build.ReleaseTasks, build.Secrets = nil, nil
	runtime.PreviewIsolation, runtime.HostPort, runtime.Ports, runtime.BindAddress = true, 0, nil, "127.0.0.1"
	for index := range runtime.Mounts {
		mount := &runtime.Mounts[index]
		mount.Source, mount.Ownership = previewVolumeName(environmentID, mount.Target), OwnershipManaged
	}
	if len(runtime.Mounts) > 0 {
		runtime.Strategy = StrategyStopFirst
	}
	if runtime.Strategy == "" {
		runtime.Strategy = StrategyBlueGreen
	}
	buildJSON, runtimeJSON := mustJSON(build), mustJSON(runtime)
	if _, err := tx.ExecContext(ctx, `INSERT INTO deploy_build_plans(environment_id,revision,method,config_json,evidence_json,preview,digest,created_at) VALUES(?,?,?,?,?,?,?,?)`, environmentID, revision, method, string(buildJSON), evidence, renderBuildPreview(build), buildPlanDigest(build), now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO deploy_runtime_plans(environment_id,revision,config_json,preview,digest,created_at) VALUES(?,?,?,?,?,?)`, environmentID, revision, string(runtimeJSON), renderRuntimePreview(runtime), digestBytes(runtimeJSON), now); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE deploy_environments SET strategy=?,expected_downtime=? WHERE id=?`, runtime.Strategy, boolInt(runtime.Strategy == StrategyStopFirst), environmentID)
	return err
}

func copyPreviewChecksTx(ctx context.Context, tx *sql.Tx, sourceID, environmentID, now int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT name,kind,phase,config_json,required,ordinal FROM deploy_checks WHERE environment_id=? AND runtime_plan_id=0`, sourceID)
	if err != nil {
		return err
	}
	type check struct {
		name, kind, phase, config string
		required, ordinal         int
	}
	checks := []check{}
	for rows.Next() {
		var item check
		if err := rows.Scan(&item.name, &item.kind, &item.phase, &item.config, &item.required, &item.ordinal); err != nil {
			rows.Close()
			return err
		}
		checks = append(checks, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, item := range checks {
		config, err := decodeCheckConfiguration(json.RawMessage(item.config))
		if err != nil {
			return err
		}
		// A check must exercise the preview; an inherited production URL can
		// otherwise report readiness while the new runtime is broken.
		if config.URL != "" {
			parsed, err := url.Parse(config.URL)
			if err != nil {
				return err
			}
			config.Path = parsed.Path
		}
		config.URL, config.Host, config.Port = "", "", 0
		if _, err := tx.ExecContext(ctx, `INSERT INTO deploy_checks(environment_id,runtime_plan_id,name,kind,phase,config_json,required,ordinal,created_at) VALUES(?,0,?,?,?,?,?,?,?)`, environmentID, item.name, item.kind, item.phase, string(mustJSON(config)), item.required, item.ordinal, now); err != nil {
			return err
		}
	}
	return nil
}

func validatePreviewPlan(environmentID int64, build BuildPlanConfig, runtime RuntimePlanConfig) error {
	if !runtime.PreviewIsolation || build.Method == BuildCompose || len(build.ReleaseTasks) > 0 ||
		runtime.Privileged || runtime.HostNetwork || len(runtime.Devices) > 0 || len(runtime.Capabilities) > 0 || runtime.HostPort != 0 || len(runtime.Ports) != 0 || runtime.BindAddress != "127.0.0.1" {
		return ErrPreviewIsolation
	}
	for _, mount := range runtime.Mounts {
		if mount.Source != previewVolumeName(environmentID, mount.Target) || mount.Ownership != OwnershipManaged {
			return ErrPreviewIsolation
		}
	}
	return nil
}

func validatePreviewAdmissionTx(ctx context.Context, tx *sql.Tx, req RunRequest) error {
	if err := previewQuarantineAdmissionTx(ctx, tx, req.EnvironmentID); err != nil {
		return err
	}
	if req.Operation == OperationPreviewRemove {
		return nil
	}
	var quarantined int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM deploy_preview_quarantines WHERE environment_id=? AND status='quarantined'`, req.EnvironmentID).Scan(&quarantined); err != nil {
		return err
	}
	if quarantined != 0 {
		return fmt.Errorf("%w: configure an approved revision after quarantine", ErrPreviewIsolation)
	}
	var buildRaw, runtimeRaw, identityRaw string
	var triggerID int64
	var ref string
	err := tx.QueryRowContext(ctx, `SELECT b.config_json,r.config_json,s.identity_json,p.trigger_id,p.provider_ref FROM deploy_build_plans b JOIN deploy_runtime_plans r ON r.environment_id=b.environment_id AND r.revision=b.revision JOIN deploy_sources s ON s.environment_id=b.environment_id AND s.revision=b.revision JOIN deploy_preview_refs p ON p.environment_id=b.environment_id WHERE b.environment_id=? AND b.revision=? AND p.state='open'`, req.EnvironmentID, req.PlanRevision).Scan(&buildRaw, &runtimeRaw, &identityRaw, &triggerID, &ref)
	if err != nil {
		return fmt.Errorf("%w: approved preview configuration is missing", ErrPreviewApproval)
	}
	var build BuildPlanConfig
	var runtime RuntimePlanConfig
	var identity SourceIdentity
	if json.Unmarshal([]byte(buildRaw), &build) != nil || json.Unmarshal([]byte(runtimeRaw), &runtime) != nil || json.Unmarshal([]byte(identityRaw), &identity) != nil {
		return ErrInvalidPlan
	}
	if req.SourceRevision != "" && req.SourceRevision != identity.Revision {
		return ErrPreviewApproval
	}
	if err := validatePreviewPlan(req.EnvironmentID, build, runtime); err != nil {
		return err
	}
	var approved int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM deploy_preview_approvals WHERE trigger_id=? AND provider_ref=? AND revision=? AND state='approved'`, triggerID, ref, identity.Revision).Scan(&approved); err != nil {
		return err
	}
	if approved != 1 {
		return ErrPreviewApproval
	}
	return nil
}
