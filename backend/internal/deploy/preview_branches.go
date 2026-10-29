package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Database branches for pull request previews.
//
// A preview never reaches production's database: its variables and links are
// its own, and a reference to a linked database is refused for it. That keeps
// production safe and leaves the preview with no data at all, which for most
// applications is no preview. A branch is the middle way, Neon's idea on this
// server's own backups: when an environment says so for a database it links,
// each preview of it gets its own copy, restored from the newest backup or
// point-in-time archive the way a migration rehearsal's is (the Databases
// owner's prepareCopy), on the preview's own database network, with a sign-in
// made for it. The preview's ${{database.N…}} then resolves to the branch,
// and a copy of production's variables carries those references over instead
// of dropping them. Chosen columns can be masked on the copy before anything
// reads it. The branch lives as long as the preview and is removed with it,
// and a preview of a fork never gets one: its code would read the data.

const (
	PreviewBranchNone   = "none"
	PreviewBranchLatest = "branch"
	PreviewBranchMasked = "masked"

	PreviewBranchWaiting   = "waiting"
	PreviewBranchRestoring = "restoring"
	PreviewBranchReady     = "ready"
	PreviewBranchFailed    = "failed"
	PreviewBranchRemoved   = "removed"

	// previewBranchMaxColumns bounds a policy's masked columns.
	previewBranchMaxColumns = 64
	// previewBranchHistory is how long an ended branch stays listed.
	previewBranchHistory = 24 * time.Hour
)

var (
	ErrPreviewBranchNotFound = errors.New("database branch not found")
	ErrPreviewBranchEnded    = errors.New("the database branch has already ended")
	ErrPreviewBranchFork     = errors.New("a preview of a fork never gets a database branch")
	ErrPreviewBranchPolicy   = errors.New("the database link gives previews no branch")
)

// maskedColumnRE is table.column or schema.table.column, each part a plain
// identifier: the names are quoted when a statement is written, and nothing
// else is accepted, so a name is never a piece of SQL.
var maskedColumnRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_$]{0,62}(\.[A-Za-z_][A-Za-z0-9_$]{0,62}){1,2}$`)

// PreviewBranchPolicy is what one database an environment links gives the
// environment's previews.
type PreviewBranchPolicy struct {
	ConnectionID  int64      `json:"connectionId"`
	Mode          string     `json:"mode"`
	MaskedColumns []string   `json:"maskedColumns"`
	UpdatedBy     string     `json:"updatedBy,omitempty"`
	UpdatedAt     *time.Time `json:"updatedAt,omitempty"`
}

// NormalizePreviewBranchPolicy checks a policy and puts its columns in one
// spelling: trimmed, de-duplicated and sorted. A masked policy names at
// least one column; any other names none.
func NormalizePreviewBranchPolicy(policy PreviewBranchPolicy) (PreviewBranchPolicy, error) {
	if policy.Mode == "" {
		policy.Mode = PreviewBranchNone
	}
	switch policy.Mode {
	case PreviewBranchNone, PreviewBranchLatest, PreviewBranchMasked:
	default:
		return policy, fmt.Errorf("%w: previews get nothing, a branch, or a branch with columns masked", ErrInvalidPlan)
	}
	seen := map[string]bool{}
	columns := []string{}
	for _, column := range policy.MaskedColumns {
		column = strings.TrimSpace(column)
		if column == "" || seen[column] {
			continue
		}
		if !maskedColumnRE.MatchString(column) {
			return policy, fmt.Errorf("%w: %q is not a table.column name", ErrInvalidPlan, column)
		}
		seen[column] = true
		columns = append(columns, column)
	}
	sort.Strings(columns)
	if len(columns) > previewBranchMaxColumns {
		return policy, fmt.Errorf("%w: at most %d columns are masked", ErrInvalidPlan, previewBranchMaxColumns)
	}
	if policy.Mode == PreviewBranchMasked && len(columns) == 0 {
		return policy, fmt.Errorf("%w: name the columns to mask, as table.column", ErrInvalidPlan)
	}
	if policy.Mode != PreviewBranchMasked {
		columns = []string{}
	}
	policy.MaskedColumns = columns
	return policy, nil
}

// PreviewBranchPolicies is every database an environment gives its previews
// something of, by connection.
func (s *OrchestrationStore) PreviewBranchPolicies(ctx context.Context, environmentID int64) (map[int64]PreviewBranchPolicy, error) {
	return previewBranchPoliciesTx(ctx, s.db, environmentID)
}

func previewBranchPoliciesTx(ctx context.Context, db interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, environmentID int64) (map[int64]PreviewBranchPolicy, error) {
	rows, err := db.QueryContext(ctx, `SELECT connection_id, mode, masked_columns, updated_by, updated_at
		FROM deploy_preview_branch_policies WHERE environment_id = ? AND mode <> 'none'`, environmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]PreviewBranchPolicy{}
	for rows.Next() {
		var policy PreviewBranchPolicy
		var columns string
		var updated int64
		if err := rows.Scan(&policy.ConnectionID, &policy.Mode, &columns, &policy.UpdatedBy, &updated); err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(columns), &policy.MaskedColumns) != nil {
			policy.MaskedColumns = []string{}
		}
		policy.UpdatedAt = unixTimePtr(updated)
		out[policy.ConnectionID] = policy
	}
	return out, rows.Err()
}

// PutPreviewBranchPolicy saves what one linked database gives an
// environment's previews. The environment is not a preview itself, and the
// database is one it links.
func (s *OrchestrationStore) PutPreviewBranchPolicy(ctx context.Context, projectID, environmentID int64, policy PreviewBranchPolicy, actor string) (PreviewBranchPolicy, error) {
	policy, err := NormalizePreviewBranchPolicy(policy)
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
		return policy, fmt.Errorf("%w: a preview's own databases are its own; set this on the environment it previews", ErrInvalidPlan)
	}
	var linked int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM (
		  SELECT connection_id FROM deploy_database_bindings WHERE environment_id = ? AND connection_id = ?
		  UNION SELECT linked.id FROM deploy_dependencies d JOIN db_connections linked
		    ON CAST(d.resource_id AS INTEGER) = linked.id OR d.resource_id = linked.name
		   WHERE d.environment_id = ? AND d.release_id = 0 AND (d.kind = 'database' OR d.resource_kind = 'database_connection') AND linked.id = ?)`,
		environmentID, policy.ConnectionID, environmentID, policy.ConnectionID).Scan(&linked); err != nil {
		return policy, err
	}
	if linked == 0 {
		return policy, fmt.Errorf("%w: the environment does not link that database", ErrInvalidPlan)
	}
	columns, _ := json.Marshal(policy.MaskedColumns)
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO deploy_preview_branch_policies(environment_id, connection_id, mode, masked_columns, updated_by, updated_at)
		VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT(environment_id, connection_id) DO UPDATE SET mode = excluded.mode, masked_columns = excluded.masked_columns,
		  updated_by = excluded.updated_by, updated_at = excluded.updated_at`,
		environmentID, policy.ConnectionID, policy.Mode, string(columns), actor, s.now().UTC().Unix()); err != nil {
		return policy, err
	}
	policy.UpdatedBy = actor
	policy.UpdatedAt = unixTimePtr(s.now().UTC().Unix())
	return policy, nil
}

// PreviewBranch is one preview's copy of a linked database.
type PreviewBranch struct {
	ID                  int64           `json:"id"`
	ProjectID           int64           `json:"projectId"`
	EnvironmentID       int64           `json:"environmentId"`
	SourceEnvironmentID int64           `json:"sourceEnvironmentId"`
	ConnectionID        int64           `json:"connectionId"`
	Name                string          `json:"name"`
	Engine              string          `json:"engine,omitempty"`
	Database            string          `json:"database,omitempty"`
	Mode                string          `json:"mode"`
	MaskedColumns       []string        `json:"maskedColumns"`
	State               string          `json:"state"`
	Source              RehearsalSource `json:"source"`
	RecoveredTo         *time.Time      `json:"recoveredTo,omitempty"`
	Container           string          `json:"container,omitempty"`
	Network             string          `json:"network,omitempty"`
	Host                string          `json:"host,omitempty"`
	Port                int             `json:"port,omitempty"`
	MaskedRows          int64           `json:"maskedRows,omitempty"`
	Reason              string          `json:"reason,omitempty"`
	RestoreMS           int64           `json:"restoreMs,omitempty"`
	RequestedBy         string          `json:"requestedBy,omitempty"`
	CreatedAt           time.Time       `json:"createdAt"`
	ReadyAt             *time.Time      `json:"readyAt,omitempty"`
	EndedAt             *time.Time      `json:"endedAt,omitempty"`
}

// PreviewBranchActive is a branch that holds, or is about to hold, a copy.
func PreviewBranchActive(state string) bool {
	return state == PreviewBranchWaiting || state == PreviewBranchRestoring || state == PreviewBranchReady
}

// PreviewBranchHost is a branch's name on its preview's database network.
// It is not a managed database's name: production's names are never on the
// preview's network, so nothing that reads one reaches the branch by
// mistake, and nothing that reads the branch's reaches production.
func PreviewBranchHost(connectionID int64) string {
	return fmt.Sprintf("db-%d-branch.jd.internal", connectionID)
}

var previewBranchHostRE = regexp.MustCompile(`^db-([0-9]+)-branch\.jd\.internal$`)

// PreviewBranchHostConnection is the connection a branch host names, or 0.
func PreviewBranchHostConnection(host string) int64 {
	match := previewBranchHostRE.FindStringSubmatch(strings.ToLower(strings.TrimSuffix(host, ".")))
	if match == nil {
		return 0
	}
	id, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil || id <= 0 {
		return 0
	}
	return id
}

const previewBranchColumns = `id, project_id, environment_id, source_environment_id, connection_id, name, engine, database_name,
 mode, masked_columns, state, source_kind, source_at, backup_run_id, recovered_to, container, network, host, port,
 masked_rows, reason, restore_ms, requested_by, created_at, ready_at, ended_at`

func scanPreviewBranch(row scanner) (*PreviewBranch, error) {
	var branch PreviewBranch
	var columns string
	var sourceAt, recovered, created, ready, ended int64
	if err := row.Scan(&branch.ID, &branch.ProjectID, &branch.EnvironmentID, &branch.SourceEnvironmentID,
		&branch.ConnectionID, &branch.Name, &branch.Engine, &branch.Database, &branch.Mode, &columns, &branch.State,
		&branch.Source.Kind, &sourceAt, &branch.Source.BackupRunID, &recovered, &branch.Container, &branch.Network,
		&branch.Host, &branch.Port, &branch.MaskedRows, &branch.Reason, &branch.RestoreMS, &branch.RequestedBy,
		&created, &ready, &ended); err != nil {
		return nil, err
	}
	if json.Unmarshal([]byte(columns), &branch.MaskedColumns) != nil || branch.MaskedColumns == nil {
		branch.MaskedColumns = []string{}
	}
	branch.Source.TakenAt, branch.RecoveredTo = unixTimePtr(sourceAt), unixTimePtr(recovered)
	branch.CreatedAt, branch.ReadyAt, branch.EndedAt = unixTime(created), unixTimePtr(ready), unixTimePtr(ended)
	return &branch, nil
}

// previewHeadIsFork is whether a preview's head comes from another
// repository than the one it previews. A head whose repository was not
// recorded is not known to be the repository's own, and is read as a fork:
// a branch puts production's data in front of the code.
func previewHeadIsFork(event ProviderEvent) bool {
	return strings.TrimSpace(event.HeadRepository) == "" || strings.TrimSpace(event.Repository) == "" ||
		!strings.EqualFold(strings.TrimSpace(event.HeadRepository), strings.TrimSpace(event.Repository))
}

// requestPreviewBranchesTx asks for a branch of every database the source
// environment gives its previews a branch of, that the preview does not hold
// one of yet. A fork's preview is asked for none, and loses any it holds.
func requestPreviewBranchesTx(ctx context.Context, tx *sql.Tx, projectID, sourceEnvironmentID, previewEnvironmentID int64, event ProviderEvent, now int64) error {
	if previewHeadIsFork(event) {
		_, err := tx.ExecContext(ctx, `UPDATE deploy_preview_branches SET state = 'removed', reason = ?, ended_at = ?, secret_enc = ''
			WHERE environment_id = ? AND state IN ('waiting', 'restoring', 'ready')`,
			"the pull request's head is a fork's, which never gets a database branch", now, previewEnvironmentID)
		return err
	}
	policies, err := previewBranchPoliciesTx(ctx, tx, sourceEnvironmentID)
	if err != nil {
		return err
	}
	ids := make([]int64, 0, len(policies))
	for id := range policies {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, connectionID := range ids {
		policy := policies[connectionID]
		var active int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM deploy_preview_branches
			WHERE environment_id = ? AND connection_id = ? AND state IN ('waiting', 'restoring', 'ready')`,
			previewEnvironmentID, connectionID).Scan(&active); err != nil {
			return err
		}
		if active > 0 {
			continue
		}
		var name string
		_ = tx.QueryRowContext(ctx, `SELECT name FROM db_connections WHERE id = ?`, connectionID).Scan(&name)
		columns, _ := json.Marshal(policy.MaskedColumns)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO deploy_preview_branches(project_id, environment_id, source_environment_id, connection_id, name, mode,
			  masked_columns, state, requested_by, created_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, 'waiting', 'preview', ?)`,
			projectID, previewEnvironmentID, sourceEnvironmentID, connectionID, name, policy.Mode, string(columns), now); err != nil {
			return err
		}
	}
	return nil
}

// PreviewBranches lists a preview's active branches and those that ended in
// the last day, newest first.
func (s *OrchestrationStore) PreviewBranches(ctx context.Context, environmentID int64) ([]PreviewBranch, error) {
	return s.queryPreviewBranches(ctx, `SELECT `+previewBranchColumns+` FROM deploy_preview_branches
		WHERE environment_id = ? AND (state IN ('waiting', 'restoring', 'ready') OR ended_at >= ?) ORDER BY id DESC LIMIT 20`,
		environmentID, s.now().Add(-previewBranchHistory).UTC().Unix())
}

// PreviewBranchesInState is every branch in one of the states, oldest
// first, for the owner's passes.
func (s *OrchestrationStore) PreviewBranchesInState(ctx context.Context, states ...string) ([]PreviewBranch, error) {
	if len(states) == 0 {
		return []PreviewBranch{}, nil
	}
	args := make([]any, 0, len(states))
	for _, state := range states {
		args = append(args, state)
	}
	return s.queryPreviewBranches(ctx, `SELECT `+previewBranchColumns+` FROM deploy_preview_branches WHERE state IN (`+
		strings.TrimSuffix(strings.Repeat("?,", len(states)), ",")+`) ORDER BY id`, args...)
}

func (s *OrchestrationStore) queryPreviewBranches(ctx context.Context, query string, args ...any) ([]PreviewBranch, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PreviewBranch{}
	for rows.Next() {
		branch, err := scanPreviewBranch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *branch)
	}
	return out, rows.Err()
}

func (s *OrchestrationStore) PreviewBranch(ctx context.Context, id int64) (*PreviewBranch, error) {
	branch, err := scanPreviewBranch(s.db.QueryRowContext(ctx, `SELECT `+previewBranchColumns+` FROM deploy_preview_branches WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPreviewBranchNotFound
	}
	return branch, err
}

// ActivePreviewBranch is the preview's branch of a database that is waiting,
// being restored or ready, if it has one.
func (s *OrchestrationStore) ActivePreviewBranch(ctx context.Context, environmentID, connectionID int64) (*PreviewBranch, error) {
	branch, err := scanPreviewBranch(s.db.QueryRowContext(ctx, `SELECT `+previewBranchColumns+` FROM deploy_preview_branches
		WHERE environment_id = ? AND connection_id = ? AND state IN ('waiting', 'restoring', 'ready') ORDER BY id DESC LIMIT 1`,
		environmentID, connectionID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPreviewBranchNotFound
	}
	return branch, err
}

// PreviewBranchSecret is the branch's sealed sign-in, for its owner to open.
func (s *OrchestrationStore) PreviewBranchSecret(ctx context.Context, id int64) (string, error) {
	var sealed string
	err := s.db.QueryRowContext(ctx, `SELECT secret_enc FROM deploy_preview_branches WHERE id = ?`, id).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrPreviewBranchNotFound
	}
	return sealed, err
}

// ClaimPreviewBranch moves a waiting branch to restoring; false is one
// somebody else claimed or ended first.
func (s *OrchestrationStore) ClaimPreviewBranch(ctx context.Context, id int64) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	result, err := s.db.ExecContext(ctx, `UPDATE deploy_preview_branches SET state = 'restoring' WHERE id = ? AND state = 'waiting'`, id)
	if err != nil {
		return false, err
	}
	affected, _ := result.RowsAffected()
	return affected == 1, nil
}

// PreviewBranchReadiness is what a finished restore records.
type PreviewBranchReadiness struct {
	Container, Network, Host, Database, Engine string
	Port                                       int
	SecretSealed                               string
	Source                                     RehearsalSource
	RecoveredTo                                *time.Time
	MaskedRows, RestoreMS                      int64
}

func (s *OrchestrationStore) PreviewBranchReady(ctx context.Context, id int64, ready PreviewBranchReadiness) (*PreviewBranch, error) {
	recovered, sourceAt := int64(0), int64(0)
	if ready.RecoveredTo != nil {
		recovered = ready.RecoveredTo.UTC().Unix()
	}
	if ready.Source.TakenAt != nil {
		sourceAt = ready.Source.TakenAt.UTC().Unix()
	}
	s.writeMu.Lock()
	result, err := s.db.ExecContext(ctx, `
		UPDATE deploy_preview_branches SET state = 'ready', container = ?, network = ?, host = ?, port = ?, database_name = ?,
		       engine = ?, secret_enc = ?, source_kind = ?, source_at = ?, backup_run_id = ?, recovered_to = ?, masked_rows = ?,
		       restore_ms = ?, ready_at = ?
		 WHERE id = ? AND state = 'restoring'`,
		ready.Container, ready.Network, ready.Host, ready.Port, ready.Database, ready.Engine, ready.SecretSealed,
		ready.Source.Kind, sourceAt, ready.Source.BackupRunID, recovered, ready.MaskedRows, ready.RestoreMS,
		s.now().UTC().Unix(), id)
	s.writeMu.Unlock()
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, ErrPreviewBranchEnded
	}
	return s.PreviewBranch(ctx, id)
}

// EndPreviewBranch closes a branch that is still active as failed or
// removed, with the reason; its sealed sign-in goes with it unless keep says
// a refresh is about to carry it over.
func (s *OrchestrationStore) EndPreviewBranch(ctx context.Context, id int64, state, reason string) (*PreviewBranch, error) {
	if state != PreviewBranchFailed && state != PreviewBranchRemoved {
		return nil, fmt.Errorf("%w: a branch ends failed or removed", ErrInvalidPlan)
	}
	s.writeMu.Lock()
	result, err := s.db.ExecContext(ctx, `
		UPDATE deploy_preview_branches SET state = ?, reason = ?, ended_at = ?, secret_enc = ''
		 WHERE id = ? AND state IN ('waiting', 'restoring', 'ready')`, state, truncateUTF8Prefix(reason, 600), s.now().UTC().Unix(), id)
	s.writeMu.Unlock()
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		if _, err := s.PreviewBranch(ctx, id); err != nil {
			return nil, err
		}
		return nil, ErrPreviewBranchEnded
	}
	return s.PreviewBranch(ctx, id)
}

// RefreshPreviewBranch ends a preview's branch of a database and asks for a
// new one from the newest backup, carrying the sign-in over so the
// preview's address — host and password — stays what it was. The policy is
// read again: masked columns follow the environment's current word. A branch
// of a fork's preview is refused.
func (s *OrchestrationStore) RefreshPreviewBranch(ctx context.Context, environmentID, connectionID int64, actor string) (*PreviewBranch, *PreviewBranch, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	previous, err := scanPreviewBranch(tx.QueryRowContext(ctx, `SELECT `+previewBranchColumns+` FROM deploy_preview_branches
		WHERE environment_id = ? AND connection_id = ? ORDER BY id DESC LIMIT 1`, environmentID, connectionID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrPreviewBranchNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	if previous.State == PreviewBranchWaiting || previous.State == PreviewBranchRestoring {
		return nil, nil, fmt.Errorf("%w: the branch is still being restored", ErrPreviewBranchEnded)
	}
	var state string
	var headRepository, repository string
	if err := tx.QueryRowContext(ctx, `SELECT p.state, COALESCE(json_extract(a.event_json,'$.HeadRepository'),''), COALESCE(json_extract(a.event_json,'$.Repository'),'')
		FROM deploy_preview_refs p
		JOIN deploy_preview_approvals a ON a.trigger_id = p.trigger_id AND a.provider_ref = p.provider_ref AND a.state = 'approved'
		WHERE p.environment_id = ? ORDER BY a.id DESC LIMIT 1`, environmentID).Scan(&state, &headRepository, &repository); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, ErrPreviewApproval
		}
		return nil, nil, err
	}
	if state != "open" {
		return nil, nil, fmt.Errorf("%w: the preview is closed", ErrPreviewBranchEnded)
	}
	if previewHeadIsFork(ProviderEvent{HeadRepository: headRepository, Repository: repository}) {
		return nil, nil, ErrPreviewBranchFork
	}
	policies, err := previewBranchPoliciesTx(ctx, tx, previous.SourceEnvironmentID)
	if err != nil {
		return nil, nil, err
	}
	policy, ok := policies[connectionID]
	if !ok {
		return nil, nil, ErrPreviewBranchPolicy
	}
	var sealed string
	if err := tx.QueryRowContext(ctx, `SELECT secret_enc FROM deploy_preview_branches WHERE id = ?`, previous.ID).Scan(&sealed); err != nil {
		return nil, nil, err
	}
	now := s.now().UTC().Unix()
	if previous.State == PreviewBranchReady {
		if _, err := tx.ExecContext(ctx, `UPDATE deploy_preview_branches SET state = 'removed', reason = ?, ended_at = ?, secret_enc = '' WHERE id = ?`,
			"refreshed from the newest backup", now, previous.ID); err != nil {
			return nil, nil, err
		}
	}
	columns, _ := json.Marshal(policy.MaskedColumns)
	result, err := tx.ExecContext(ctx, `
		INSERT INTO deploy_preview_branches(project_id, environment_id, source_environment_id, connection_id, name, mode,
		  masked_columns, state, secret_enc, requested_by, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, 'waiting', ?, ?, ?)`,
		previous.ProjectID, environmentID, previous.SourceEnvironmentID, connectionID, previous.Name, policy.Mode,
		string(columns), sealed, actor, now)
	if err != nil {
		return nil, nil, err
	}
	id, _ := result.LastInsertId()
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	ended, err := s.PreviewBranch(ctx, previous.ID)
	if err != nil {
		return nil, nil, err
	}
	next, err := s.PreviewBranch(ctx, id)
	return ended, next, err
}

// previewBranchReferenceTx is the connection a database reference names,
// when it is one a branch of it could answer: a plain or shaped reference by
// id, or one by the connection's name. The pooled form is not: a branch has
// no pooler.
func previewBranchReferenceTx(ctx context.Context, tx *sql.Tx, value string) int64 {
	reference, err := ParseVariableReference(value)
	if err != nil || reference.Kind != "database" {
		return 0
	}
	target := reference.Target
	first, rest, _ := strings.Cut(target, ".")
	if id, err := strconv.ParseInt(first, 10, 64); err == nil && id > 0 {
		if strings.HasPrefix(rest, "pooled") {
			return 0
		}
		return id
	}
	var id int64
	for _, name := range []string{target, strings.TrimSuffix(target, ".url")} {
		if err := tx.QueryRowContext(ctx, `SELECT id FROM db_connections WHERE name = ?`, name).Scan(&id); err == nil {
			return id
		}
	}
	return 0
}
