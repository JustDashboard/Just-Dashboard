package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// PreviewOriginDashboard marks a preview an administrator started from the
// dashboard's "Test this pull request" rather than one a webhook opened.
const PreviewOriginDashboard = "dashboard"

// maxPreviewTitle bounds the pull request title kept beside a preview. GitHub
// allows 256 characters; anything longer is a title nobody typed.
const maxPreviewTitle = 256

// ErrTriggerNameTaken is returned when every name the dashboard would give
// its own pull request trigger is already held by another trigger of the
// environment; trigger names are unique per environment and kind.
var ErrTriggerNameTaken = errors.New("a trigger with that name already exists on this environment")

// recordPreviewRevisionTx is the half of the approval state machine both
// approval paths share: a new head supersedes every other pending or
// approved head of the pull request, and the head itself gets its row,
// re-entering 'pending' with a new generation when it was closed or
// superseded before. A rejected head keeps its row untouched, which is how a
// rejection outlives every later attempt at the same commit.
func recordPreviewRevisionTx(ctx context.Context, tx *sql.Tx, triggerID int64, ref string, event ProviderEvent, now int64) error {
	// An outdated pending approval must not silently approve a new PR head.
	if _, err := tx.ExecContext(ctx, `UPDATE deploy_preview_approvals SET state='superseded',updated_at=? WHERE trigger_id=? AND provider_ref=? AND revision<>? AND state IN ('pending','approved')`, now, triggerID, ref, event.Revision); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO deploy_preview_approvals(trigger_id,provider_ref,revision,event_json,created_at,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(trigger_id,provider_ref,revision) DO UPDATE SET state='pending',approved_by='',generation=deploy_preview_approvals.generation+1,event_json=excluded.event_json,updated_at=excluded.updated_at WHERE state IN ('closed','superseded')`, triggerID, ref, event.Revision, string(mustJSON(event)), now, now)
	return err
}

// RecordPreviewApproval is the dashboard's own "Test this pull request": the
// administrator who clicked is the approver of exactly this head, so the
// row a webhook would have left pending is written and approved in one
// step, under the same supersede rule as requirePreviewApproval. A head an
// administrator rejected stays rejected; only a new event can bring it back.
func (s *AutomationStore) RecordPreviewApproval(ctx context.Context, t *Trigger, event ProviderEvent, actor string) (*PreviewApproval, error) {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return nil, ErrPreviewApproval
	}
	if !t.Config.Preview || event.PreviewNumber <= 0 || event.PreviewClosed ||
		!gitObjectIDRE.MatchString(event.Revision) || event.PreviewRef == "" {
		return nil, fmt.Errorf("%w: preview requires an open pull request with an immutable revision and ref", ErrWrongEvent)
	}
	ref := strconv.Itoa(event.PreviewNumber)
	now := s.now().UTC().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := recordPreviewRevisionTx(ctx, tx, t.ID, ref, event, now); err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE deploy_preview_approvals SET state='approved',approved_by=?,updated_at=? WHERE trigger_id=? AND provider_ref=? AND revision=? AND state IN ('pending','approved') AND trigger_id IN (SELECT t.id FROM deploy_triggers t JOIN deploy_environments e ON e.id=t.environment_id JOIN deploy_projects p ON p.id=e.project_id WHERE t.id=? AND e.archived_at=0 AND p.archived_at=0 AND t.enabled=1)`, actor, now, t.ID, ref, event.Revision, t.ID)
	if err != nil {
		return nil, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return nil, ErrPreviewApproval
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return scanPreviewApproval(s.db.QueryRowContext(ctx, `SELECT `+previewApprovalColumns+` FROM deploy_preview_approvals a WHERE a.trigger_id=? AND a.provider_ref=? AND a.revision=?`, t.ID, ref, event.Revision))
}

// EnsurePullRequestTrigger finds the trigger the dashboard's own previews of
// a repository hang off, creating it on first use: previews are keyed by
// trigger, so a project needs one per repository. A trigger with a preview
// domain belongs to the webhook flow that configured it and is left alone.
// A matching trigger the operator disabled is switched back on rather than
// duplicated: the click asks for a preview, and a disabled trigger refuses
// every approval. Delivery stays off on purpose: a click on one pull request
// must not switch on App ingestion of every pull request anyone opens; the
// reconciler follows closes and new heads instead. Trigger names are unique
// per environment and kind, so when an unrelated GitHub trigger already
// holds "Pull requests" the new one carries the repository in its name.
func (s *AutomationStore) EnsurePullRequestTrigger(ctx context.Context, projectID, environmentID int64, repository, ref string) (*Trigger, error) {
	repository = strings.TrimSpace(repository)
	triggers, err := s.ListTriggers(ctx, projectID, environmentID)
	if err != nil {
		return nil, err
	}
	var disabled *Trigger
	taken := map[string]bool{}
	for index := range triggers {
		t := &triggers[index]
		if t.Kind == TriggerGitHub {
			taken[t.Name] = true
		}
		if t.Provider != "github" || !t.Config.Preview || t.Config.PreviewDomain != "" || !sameRepository(t.Config.Repository, repository) {
			continue
		}
		if t.Enabled {
			return t, nil
		}
		if disabled == nil {
			disabled = t
		}
	}
	if disabled != nil {
		if _, err := s.db.ExecContext(ctx, `UPDATE deploy_triggers SET enabled=1,updated_at=? WHERE id=?`, s.now().UTC().Unix(), disabled.ID); err != nil {
			return nil, err
		}
		return s.triggerByID(ctx, disabled.ID, projectID)
	}
	name := "Pull requests"
	if taken[name] {
		name = fmt.Sprintf("Pull requests (%s)", repository)
	}
	if taken[name] {
		return nil, fmt.Errorf("%w: rename the trigger %q to test pull requests here", ErrTriggerNameTaken, name)
	}
	created, err := s.CreateTrigger(ctx, projectID, environmentID, TriggerWrite{
		Name: name, Kind: TriggerGitHub, Provider: "github", Enabled: true,
		Config: TriggerConfig{Repository: repository, Ref: ref, Events: []string{"pull_request"}, Preview: true, PreviewQuota: 5},
	})
	if err != nil {
		return nil, err
	}
	return &created.Trigger, nil
}

// sameRepository compares two owner/name repository names the way the forge
// does: case does not matter and a trailing .git is not part of the name.
func sameRepository(a, b string) bool {
	return strings.EqualFold(strings.TrimSuffix(strings.TrimSpace(a), ".git"), strings.TrimSuffix(strings.TrimSpace(b), ".git"))
}

// MarkPreviewOrigin records where a preview came from and the pull request's
// title as the panels show it.
func (s *AutomationStore) MarkPreviewOrigin(ctx context.Context, previewID int64, origin, title string) error {
	if origin != "" && origin != PreviewOriginDashboard {
		return fmt.Errorf("%w: unknown preview origin %q", ErrInvalidPlan, origin)
	}
	title = strings.TrimSpace(title)
	if runes := []rune(title); len(runes) > maxPreviewTitle {
		title = string(runes[:maxPreviewTitle])
	}
	return s.updatePreviewRef(ctx, previewID, `origin=?,title=?`, origin, title)
}

// RecordPreviewHead notes the newest head the pull request carries. It is
// an observation about the pull request, not a change to what the preview
// runs: the preview stays at its approved revision until the next approval.
func (s *AutomationStore) RecordPreviewHead(ctx context.Context, previewID int64, headRevision string) error {
	if !gitObjectIDRE.MatchString(headRevision) {
		return fmt.Errorf("%w: preview head must be an immutable Git object id", ErrInvalidRef)
	}
	return s.updatePreviewRef(ctx, previewID, `head_revision=?`, headRevision)
}

// RecordPreviewVariablesCopied remembers the head whose production variables
// the preview carries; "" says it carries none.
func (s *AutomationStore) RecordPreviewVariablesCopied(ctx context.Context, previewID int64, revision string) error {
	if revision != "" && !gitObjectIDRE.MatchString(revision) {
		return fmt.Errorf("%w: copied revision must be an immutable Git object id", ErrInvalidRef)
	}
	return s.updatePreviewRef(ctx, previewID, `variables_copied_revision=?`, revision)
}

// updatePreviewRef applies one constant assignment list to a preview row.
// The assignments are literals from this file, never request input.
func (s *AutomationStore) updatePreviewRef(ctx context.Context, previewID int64, assignments string, args ...any) error {
	args = append(args, s.now().UTC().Unix(), previewID)
	result, err := s.db.ExecContext(ctx, `UPDATE deploy_preview_refs SET `+assignments+`,updated_at=? WHERE id=?`, args...)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return ErrTriggerNotFound
	}
	return nil
}

// PreviewsByRepository lists every preview of a repository across projects,
// newest first, for the Git page's per-checkout view.
func (s *AutomationStore) PreviewsByRepository(ctx context.Context, repository string) ([]PreviewRef, error) {
	name := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(repository), ".git"))
	if name == "" {
		return []PreviewRef{}, nil
	}
	return s.previewRefs(ctx, `WHERE LOWER(COALESCE(json_extract(t.config_json,'$.repository'),'')) IN (?,?) ORDER BY p.updated_at DESC,p.id DESC`, name, name+".git")
}

// PreviewByNumber finds a project's preview of one pull request, preferring
// an open one when several triggers know the number. It answers nil, nil
// when the project has never previewed that pull request.
func (s *AutomationStore) PreviewByNumber(ctx context.Context, projectID int64, number int) (*PreviewRef, error) {
	if number <= 0 {
		return nil, nil
	}
	previews, err := s.previewRefs(ctx, `WHERE e.project_id=? AND p.provider_ref=? ORDER BY (p.state='open') DESC,p.updated_at DESC,p.id DESC LIMIT 1`, projectID, strconv.Itoa(number))
	if err != nil || len(previews) == 0 {
		return nil, err
	}
	return &previews[0], nil
}

// OpenPreviewTargets is every open GitHub preview the reconciler has to keep
// an eye on, with what it needs to ask GitHub and to act on the answer. The
// reconciler reads owner/name#N from github.com and closes the preview on
// what it hears, so only a preview whose own source is that github.com
// repository is handed over: a trigger naming acme/app on a project cloned
// from another host's acme/app would otherwise have its preview judged by a
// stranger's pull request.
func (s *AutomationStore) OpenPreviewTargets(ctx context.Context) ([]PreviewTarget, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT p.id,p.trigger_id,e.project_id,p.environment_id,COALESCE(json_extract(t.config_json,'$.repository'),''),p.provider_ref,COALESCE(json_extract(s.identity_json,'$.revision'),''),COALESCE(json_extract(s.identity_json,'$.remote'),''),COALESCE(json_extract(s.identity_json,'$.repository'),''),p.head_revision,p.origin,p.state FROM deploy_preview_refs p JOIN deploy_environments e ON e.id=p.environment_id JOIN deploy_triggers t ON t.id=p.trigger_id LEFT JOIN deploy_sources s ON s.environment_id=e.id AND s.revision=e.desired_revision WHERE p.state='open' AND e.archived_at=0 AND t.provider='github' ORDER BY p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PreviewTarget{}
	for rows.Next() {
		var target PreviewTarget
		var ref, remote, sourceRepository string
		if err := rows.Scan(&target.PreviewID, &target.TriggerID, &target.ProjectID, &target.EnvironmentID, &target.Repository, &ref, &target.Revision, &remote, &sourceRepository, &target.HeadRevision, &target.Origin, &target.State); err != nil {
			return nil, err
		}
		target.Repository = strings.TrimSuffix(target.Repository, ".git")
		source := GitHubRepository(SourceIdentity{Kind: SourceGit, Remote: remote, Repository: sourceRepository})
		if source == "" || !sameRepository(source, target.Repository) {
			continue
		}
		target.Number, _ = strconv.Atoi(ref)
		out = append(out, target)
	}
	return out, rows.Err()
}

// previewRefColumns and previewRefFrom are the one reading of a preview row
// every PreviewRef list shares. The approval row is the one for the revision
// the preview is configured at, the address row is what the executor
// published on the tailnet, and the run is the environment's newest.
const previewRefColumns = `p.id,p.trigger_id,e.project_id,p.provider_ref,p.environment_id,e.slug,p.state,p.updated_at,COALESCE(q.status,''),COALESCE(q.reason,''),
p.origin,p.title,p.head_revision,p.variables_copied_revision,e.live_release_id,
COALESCE(json_extract(s.identity_json,'$.revision'),''),COALESCE(a.state,''),COALESCE(a.event_json,''),
COALESCE(x.kind,''),COALESCE(x.url,''),COALESCE(x.port,0),COALESCE(x.upstream_port,0),COALESCE(x.published,0),
COALESCE((SELECT d.config_json FROM deploy_dependencies d WHERE d.environment_id=e.id AND d.kind='domain' ORDER BY d.id LIMIT 1),''),
COALESCE(r.id,0),COALESCE(r.run_number,0),COALESCE(r.state,''),COALESCE(r.operation,''),COALESCE(r.requested_at,0),COALESCE(r.ended_at,0)`

const previewRefFrom = ` FROM deploy_preview_refs p
JOIN deploy_environments e ON e.id=p.environment_id
JOIN deploy_triggers t ON t.id=p.trigger_id
LEFT JOIN deploy_preview_quarantines q ON q.environment_id=e.id
LEFT JOIN deploy_sources s ON s.environment_id=e.id AND s.revision=e.desired_revision
LEFT JOIN deploy_preview_approvals a ON a.trigger_id=p.trigger_id AND a.provider_ref=p.provider_ref AND a.revision=json_extract(s.identity_json,'$.revision')
LEFT JOIN deploy_preview_addresses x ON x.environment_id=e.id
LEFT JOIN deploy_runs r ON r.id=(SELECT n.id FROM deploy_runs n WHERE n.environment_id=e.id ORDER BY n.requested_at DESC,n.id DESC LIMIT 1) `

func (s *AutomationStore) previewRefs(ctx context.Context, clause string, args ...any) ([]PreviewRef, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+previewRefColumns+previewRefFrom+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PreviewRef{}
	for rows.Next() {
		p, err := scanPreviewRef(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func scanPreviewRef(row interface{ Scan(...any) error }) (*PreviewRef, error) {
	var p PreviewRef
	var run RecentRun
	var updated, requested, ended int64
	var approvalEvent, addressKind, addressURL, domainConfig string
	var addressPort, upstreamPort, published int
	if err := row.Scan(&p.ID, &p.TriggerID, &p.ProjectID, &p.ProviderRef, &p.EnvironmentID, &p.EnvironmentSlug, &p.State, &updated, &p.IsolationStatus, &p.IsolationReason,
		&p.Origin, &p.Title, &p.HeadRevision, &p.VariablesCopiedRevision, &p.LiveReleaseID,
		&p.Revision, &p.ApprovalState, &approvalEvent,
		&addressKind, &addressURL, &addressPort, &upstreamPort, &published,
		&domainConfig,
		&run.ID, &run.RunNumber, &run.State, &run.Operation, &requested, &ended); err != nil {
		return nil, err
	}
	p.UpdatedAt = unixTime(updated)
	p.Number, _ = strconv.Atoi(p.ProviderRef)
	if approvalEvent != "" {
		var event ProviderEvent
		if json.Unmarshal([]byte(approvalEvent), &event) == nil {
			p.HeadRef, p.HeadRepository, p.Author = event.HeadRef, event.HeadRepository, event.Author
		}
	}
	switch {
	case addressKind != "":
		p.Address = &PreviewAddress{Kind: addressKind, URL: addressURL, Port: addressPort, UpstreamPort: upstreamPort, Published: published != 0}
	case domainConfig != "":
		// A webhook preview with a preview domain answers on that hostname
		// through the public proxy once a release is live.
		var domain struct {
			Hostname string `json:"hostname"`
			HTTPS    bool   `json:"https"`
		}
		if json.Unmarshal([]byte(domainConfig), &domain) == nil && domain.Hostname != "" {
			scheme := "http"
			if domain.HTTPS {
				scheme = "https"
			}
			p.Address = &PreviewAddress{Kind: "domain", URL: scheme + "://" + domain.Hostname, Published: p.LiveReleaseID != 0}
		}
	}
	if run.ID != 0 {
		run.RequestedAt = unixTime(requested)
		run.EndedAt = unixTimePtr(ended)
		p.LastRun = &run
	}
	return &p, nil
}

// previewEnvironmentKindTx answers whether an environment is a preview of the
// project, which is the only place production variables may be copied to.
func previewEnvironmentKindTx(ctx context.Context, tx *sql.Tx, projectID, environmentID int64) error {
	var kind string
	err := tx.QueryRowContext(ctx, `SELECT kind FROM deploy_environments WHERE id=? AND project_id=? AND archived_at=0`, environmentID, projectID).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrEnvironmentNotFound
	}
	if err != nil {
		return err
	}
	if EnvironmentKind(kind) != EnvironmentPreview {
		return fmt.Errorf("%w: production variables can only be copied into a preview", ErrPreviewIsolation)
	}
	return nil
}
