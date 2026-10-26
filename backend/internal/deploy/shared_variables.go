package deploy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Shared variables are values defined once and read by any environment whose
// own variable references them: the server's as ${{server.NAME}} by every
// project, a project's as ${{project.NAME}} by that project's environments.
// Nothing is injected by name. An environment reads a shared value only
// through a variable it declares itself, with that variable's own scopes, so
// a shared name can never silently shadow or fill in one of the
// environment's.
//
// A reference resolves the current value when a run starts, exactly as a
// credential reference does: a release freezes the reference, not the value,
// so a rollback to an older release reads today's value, and a change reaches
// a running release only through its next deploy. A shared value is always a
// literal; reference-shaped text in one is never followed, so shared values
// cannot form chains or cycles across scopes.
const (
	SharedScopeServer  = "server"
	SharedScopeProject = "project"
)

var (
	// ErrSharedVariableNotFound names a shared variable no row matches in the
	// scope asked about.
	ErrSharedVariableNotFound = errors.New("shared variable not found")
	// ErrSharedVariableInUse refuses removing a shared variable that an
	// active environment's variable still references: removing it would turn
	// that environment's next deploy into a failure.
	ErrSharedVariableInUse = errors.New("shared variable is in use")
)

// A shared name is an environment variable name that starts with a letter,
// because it is written inside a typed reference, whose targets do.
var sharedVariableNameRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,127}$`)

func sharedReferenceKind(kind string) bool {
	return kind == SharedScopeServer || kind == SharedScopeProject
}

// sharedScope is the kind of reference that reads a variable saved under
// projectID: the server's own for 0.
func sharedScope(projectID int64) string {
	if projectID == 0 {
		return SharedScopeServer
	}
	return SharedScopeProject
}

// SharedReference is the literal an environment's variable holds to read the
// shared variable name saved under projectID (0 for the server's).
func SharedReference(projectID int64, name string) string {
	return "${{" + sharedScope(projectID) + "." + name + "}}"
}

// parseStoredReference reads a stored variable value as a typed reference
// under its value mode. A value saved before modes were recorded is inferred
// from its shape, but only in the kinds that existed then: shared kinds
// arrived later, so such a value naming one was always the literal text it
// reads as, and still is.
func parseStoredReference(value, mode string) (VariableReference, bool) {
	if mode == "literal" {
		return VariableReference{}, false
	}
	reference, err := ParseVariableReference(value)
	if err != nil || (mode != "reference" && sharedReferenceKind(reference.Kind)) {
		return VariableReference{}, false
	}
	return reference, true
}

// SharedVariable is the read model: everything but the value, which only the
// audited reveal route returns.
type SharedVariable struct {
	Scope       string `json:"scope"`
	ProjectID   int64  `json:"projectId,omitempty"`
	Name        string `json:"name"`
	Sensitivity string `json:"sensitivity"`
	Masked      string `json:"masked"`
	ValueDigest string `json:"valueDigest"`
	// Reference is the literal a variable holds to read this one.
	Reference string              `json:"reference"`
	UpdatedBy string              `json:"updatedBy"`
	CreatedAt time.Time           `json:"createdAt"`
	UpdatedAt time.Time           `json:"updatedAt"`
	UsedBy    []SharedVariableUse `json:"usedBy"`
}

// SharedVariableUse is one environment variable that references a shared
// variable. Waiting says the environment's live release started before the
// shared value last changed, so it runs the earlier value until it deploys
// again; an environment that has never deployed is not waiting, since its
// first release reads the value as it is then.
type SharedVariableUse struct {
	ProjectID       int64  `json:"projectId"`
	ProjectName     string `json:"projectName"`
	EnvironmentID   int64  `json:"environmentId"`
	EnvironmentName string `json:"environmentName"`
	EnvironmentKind string `json:"environmentKind"`
	Variable        string `json:"variable"`
	Waiting         bool   `json:"waiting"`
}

// SharedVariableWrite is PUT …/shared-variables/{name}'s body. An omitted
// value keeps the sealed one, so the type can change without retyping a
// secret; a new variable requires it.
type SharedVariableWrite struct {
	Value       *string `json:"value,omitempty"`
	Sensitivity string  `json:"sensitivity"`
}

type SharedVariableReveal struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// SharedVariableChange says what a write did, for the audit entry: whether it
// created the variable and whether the value or the type changed.
type SharedVariableChange struct {
	Created            bool
	ValueChanged       bool
	SensitivityChanged bool
}

func validateSharedVariableName(name string) error {
	if !sharedVariableNameRE.MatchString(name) {
		return fmt.Errorf("%w: a shared variable's name starts with a letter, then up to 127 letters, digits or underscores", ErrInvalidVariable)
	}
	return nil
}

// sharedProject checks the scope a route names: 0 is the server's, which
// always exists; a project must exist, and must not be archived to be
// written to.
func (s *PlanningStore) sharedProject(ctx context.Context, projectID int64, write bool) error {
	if projectID == 0 {
		return nil
	}
	var archived int64
	if err := s.db.QueryRowContext(ctx, `SELECT archived_at FROM deploy_projects WHERE id = ?`, projectID).Scan(&archived); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if write && archived != 0 {
		return ErrNotFound
	}
	return nil
}

// ListSharedVariables lists the variables saved under projectID (0 for the
// server's), each with the environment variables that read it.
func (s *PlanningStore) ListSharedVariables(ctx context.Context, projectID int64) ([]SharedVariable, error) {
	if err := s.sharedProject(ctx, projectID, false); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT name, sensitivity, value_digest, updated_by, created_at, updated_at
		  FROM deploy_shared_variables
		 WHERE IFNULL(project_id, 0) = ?
		 ORDER BY name`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SharedVariable{}
	for rows.Next() {
		variable := SharedVariable{Scope: sharedScope(projectID), ProjectID: projectID, UsedBy: []SharedVariableUse{}}
		var created, updated int64
		if err := rows.Scan(&variable.Name, &variable.Sensitivity, &variable.ValueDigest, &variable.UpdatedBy, &created, &updated); err != nil {
			return nil, err
		}
		variable.CreatedAt, variable.UpdatedAt = unixTime(created), unixTime(updated)
		variable.Reference = SharedReference(projectID, variable.Name)
		variable.Masked = "configured"
		if variable.Sensitivity == "secret" {
			variable.Masked = "••••••••"
		}
		out = append(out, variable)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}
	uses, err := s.sharedVariableUses(ctx, projectID)
	if err != nil {
		return nil, err
	}
	for index := range out {
		for _, use := range uses[out[index].Name] {
			use.Waiting = use.Waiting && use.liveStarted < out[index].UpdatedAt.Unix()
			out[index].UsedBy = append(out[index].UsedBy, use.SharedVariableUse)
		}
	}
	return out, nil
}

type sharedUseRow struct {
	SharedVariableUse
	liveStarted int64
}

// sharedVariableUses finds, by shared name, every active variable of a
// non-archived environment that references a variable saved under projectID.
// References live in the sealed values, so only values saved as references
// are opened, and nothing but the reference is kept.
func (s *PlanningStore) sharedVariableUses(ctx context.Context, projectID int64) (map[string][]sharedUseRow, error) {
	query := `
		SELECT p.id, p.name, e.id, e.name, e.kind, v.key, v.value_enc,
		       e.live_release_id, COALESCE(run.started_at, 0)
		  FROM deploy_variable_revisions v
		  JOIN deploy_environments e ON e.id = v.environment_id
		  JOIN deploy_projects p ON p.id = e.project_id
		  LEFT JOIN deploy_releases live ON live.id = e.live_release_id
		  LEFT JOIN deploy_runs run ON run.id = live.run_id
		 WHERE v.active = 1 AND v.value_mode = 'reference'
		   AND e.archived_at = 0 AND p.archived_at = 0`
	arguments := []any{}
	if projectID != 0 {
		query += ` AND p.id = ?`
		arguments = append(arguments, projectID)
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY p.name, e.id, v.key`, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	kind := sharedScope(projectID)
	uses := map[string][]sharedUseRow{}
	for rows.Next() {
		var use sharedUseRow
		var sealed string
		var live int64
		if err := rows.Scan(&use.ProjectID, &use.ProjectName, &use.EnvironmentID, &use.EnvironmentName,
			&use.EnvironmentKind, &use.Variable, &sealed, &live, &use.liveStarted); err != nil {
			return nil, err
		}
		value, err := s.sealer.Open(sealed)
		if err != nil {
			return nil, fmt.Errorf("deployment variable %s cannot be decrypted", use.Variable)
		}
		reference, ok := parseStoredReference(value, "reference")
		if !ok || reference.Kind != kind {
			continue
		}
		use.Waiting = live != 0
		uses[reference.Target] = append(uses[reference.Target], use)
	}
	return uses, rows.Err()
}

// PutSharedVariable creates the named variable under projectID (0 for the
// server's) or changes its value or type.
func (s *PlanningStore) PutSharedVariable(
	ctx context.Context,
	projectID int64,
	name, actor string,
	request SharedVariableWrite,
) (*SharedVariable, SharedVariableChange, error) {
	var change SharedVariableChange
	if err := validateSharedVariableName(name); err != nil {
		return nil, change, err
	}
	if request.Sensitivity != "plain" && request.Sensitivity != "secret" {
		return nil, change, fmt.Errorf("%w: sensitivity must be plain or secret", ErrInvalidVariable)
	}
	if request.Value != nil && (len(*request.Value) > maxDeploymentVariableValue || strings.ContainsRune(*request.Value, '\x00')) {
		return nil, change, fmt.Errorf("%w: value is invalid or exceeds 64 KiB", ErrInvalidVariable)
	}
	if err := s.sharedProject(ctx, projectID, true); err != nil {
		return nil, change, err
	}
	project := sql.NullInt64{Int64: projectID, Valid: projectID != 0}
	now := s.now().UTC().Unix()
	var id int64
	var sensitivity, digest string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, sensitivity, value_digest FROM deploy_shared_variables
		 WHERE IFNULL(project_id, 0) = ? AND name = ?`, projectID, name).Scan(&id, &sensitivity, &digest)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if request.Value == nil {
			return nil, change, fmt.Errorf("%w: a new shared variable needs a value", ErrInvalidVariable)
		}
		sealed, err := s.sealer.Seal(*request.Value)
		if err != nil {
			return nil, change, err
		}
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO deploy_shared_variables(project_id, name, sensitivity, value_enc, value_digest, updated_by, created_at, updated_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?)`, project, name, request.Sensitivity, sealed,
			digestBytes([]byte(*request.Value)), actor, now, now); err != nil {
			return nil, change, err
		}
		change = SharedVariableChange{Created: true, ValueChanged: true, SensitivityChanged: true}
	case err != nil:
		return nil, change, err
	default:
		change.SensitivityChanged = sensitivity != request.Sensitivity
		if request.Value != nil && digestBytes([]byte(*request.Value)) != digest {
			sealed, err := s.sealer.Seal(*request.Value)
			if err != nil {
				return nil, change, err
			}
			if _, err := s.db.ExecContext(ctx, `
				UPDATE deploy_shared_variables SET value_enc = ?, value_digest = ? WHERE id = ?`,
				sealed, digestBytes([]byte(*request.Value)), id); err != nil {
				return nil, change, err
			}
			change.ValueChanged = true
		}
		// The time moves only with what a release reads, so a save that
		// changed nothing does not mark every user as waiting for a deploy.
		if change.ValueChanged || change.SensitivityChanged {
			if _, err := s.db.ExecContext(ctx, `
				UPDATE deploy_shared_variables SET sensitivity = ?, updated_by = ?, updated_at = ? WHERE id = ?`,
				request.Sensitivity, actor, now, id); err != nil {
				return nil, change, err
			}
		}
	}
	variables, err := s.ListSharedVariables(ctx, projectID)
	if err != nil {
		return nil, change, err
	}
	for index := range variables {
		if variables[index].Name == name {
			return &variables[index], change, nil
		}
	}
	return nil, change, ErrSharedVariableNotFound
}

// RevealSharedVariable opens one value, for the audited reveal route alone.
func (s *PlanningStore) RevealSharedVariable(ctx context.Context, projectID int64, name string) (*SharedVariableReveal, error) {
	if err := validateSharedVariableName(name); err != nil {
		return nil, err
	}
	if err := s.sharedProject(ctx, projectID, false); err != nil {
		return nil, err
	}
	value, _, err := s.sharedVariableValue(ctx, projectID, name)
	if err != nil {
		return nil, err
	}
	return &SharedVariableReveal{Name: name, Value: value}, nil
}

// DeleteSharedVariable removes the named variable, refusing while an active
// environment's variable references it. The refusal names who does, so the
// operator knows what to point elsewhere first.
func (s *PlanningStore) DeleteSharedVariable(ctx context.Context, projectID int64, name string) error {
	if err := validateSharedVariableName(name); err != nil {
		return err
	}
	if err := s.sharedProject(ctx, projectID, false); err != nil {
		return err
	}
	uses, err := s.sharedVariableUses(ctx, projectID)
	if err != nil {
		return err
	}
	if users := uses[name]; len(users) > 0 {
		named := make([]string, 0, len(users))
		for _, use := range users {
			named = append(named, fmt.Sprintf("%s (%s) as %s", use.ProjectName, use.EnvironmentName, use.Variable))
		}
		them := "that variable"
		if len(users) > 1 {
			them = "those variables"
		}
		return fmt.Errorf("%w: %s is read by %s; point %s elsewhere first", ErrSharedVariableInUse,
			name, strings.Join(named, ", "), them)
	}
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM deploy_shared_variables WHERE IFNULL(project_id, 0) = ? AND name = ?`, projectID, name)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected == 0 {
		return ErrSharedVariableNotFound
	}
	return nil
}

// sharedVariableValue opens the named variable saved under projectID (0 for
// the server's) and says whether it is secret. A missing one fails closed: a
// reference never reaches a workload as its own literal text.
func (s *PlanningStore) sharedVariableValue(ctx context.Context, projectID int64, name string) (string, bool, error) {
	var sensitivity, sealed string
	if err := s.db.QueryRowContext(ctx, `
		SELECT sensitivity, value_enc FROM deploy_shared_variables
		 WHERE IFNULL(project_id, 0) = ? AND name = ?`, projectID, name).Scan(&sensitivity, &sealed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Both sentinels: a run fails on it as on any unresolvable
			// reference, and the reveal route answers 404.
			return "", false, fmt.Errorf("%w: %w: %s.%s does not exist", ErrInvalidVariable, ErrSharedVariableNotFound, sharedScope(projectID), name)
		}
		return "", false, err
	}
	value, err := s.sealer.Open(sealed)
	if err != nil {
		return "", false, fmt.Errorf("%w: shared variable %s cannot be opened", ErrInvalidVariable, name)
	}
	return value, sensitivity == "secret", nil
}

// environmentProject is the project an environment belongs to, which is
// where its ${{project.…}} references read.
func (s *PlanningStore) environmentProject(ctx context.Context, environmentID int64) (int64, error) {
	var projectID int64
	if err := s.db.QueryRowContext(ctx, `SELECT project_id FROM deploy_environments WHERE id = ?`, environmentID).Scan(&projectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrEnvironmentNotFound
		}
		return 0, err
	}
	return projectID, nil
}

// checkSharedReference is the save-time half of a shared reference: the
// variable it names exists in that scope, and a variable reading a secret is
// itself secret, as one reading a credential or a database is. The run-time
// half, resolveExternalVariableReference, still fails closed on its own,
// since an archived project's references are not counted as uses.
func (s *PlanningStore) checkSharedReference(ctx context.Context, projectID int64, variable, sensitivity string, reference VariableReference) error {
	problem, err := s.sharedReferenceProblem(ctx, projectID, variable, sensitivity, reference)
	if err != nil {
		return err
	}
	if problem != "" {
		return fmt.Errorf("%w: %s", ErrInvalidVariable, problem)
	}
	return nil
}

// sharedReferenceProblem says what is wrong with a reference, in a sentence
// that names the variable, or nothing.
func (s *PlanningStore) sharedReferenceProblem(ctx context.Context, projectID int64, variable, sensitivity string, reference VariableReference) (string, error) {
	if !sharedReferenceKind(reference.Kind) {
		return "", nil
	}
	scopeProject := int64(0)
	if reference.Kind == SharedScopeProject {
		scopeProject = projectID
	}
	var shared string
	if err := s.db.QueryRowContext(ctx, `
		SELECT sensitivity FROM deploy_shared_variables
		 WHERE IFNULL(project_id, 0) = ? AND name = ?`, scopeProject, reference.Target).Scan(&shared); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			where := "this server"
			if reference.Kind == SharedScopeProject {
				where = "this project"
			}
			return fmt.Sprintf("%s reads %s.%s, and %s has no shared variable %s", variable, reference.Kind, reference.Target, where, reference.Target), nil
		}
		return "", err
	}
	if shared == "secret" && sensitivity != "secret" {
		return fmt.Sprintf("%s reads the secret %s.%s, so it has to be secret too", variable, reference.Kind, reference.Target), nil
	}
	return "", nil
}

// checkDraftSharedReferences holds a new project's references to the same
// rule a saved variable is: a server variable must exist, and a project one
// cannot yet, because the project it would belong to is the one being
// created. A value staged in the wizard replaces the reference, so only
// references that will be saved are checked.
func (s *PlanningStore) checkDraftSharedReferences(ctx context.Context, draft *Draft) error {
	if draft.Data.Configuration == nil {
		return nil
	}
	for _, variable := range draft.Data.Configuration.Variables {
		if _, supplied := draft.environment[variable.Name]; supplied || variable.Reference == "" {
			continue
		}
		reference, err := ParseVariableReference(variable.Reference)
		if err != nil || !sharedReferenceKind(reference.Kind) {
			continue
		}
		if reference.Kind == SharedScopeProject {
			return fmt.Errorf("%w: %w", ErrInvalidPlan, invalidField(variable.Name,
				"%s reads project.%s, but a new project has no project variables yet; use a server variable, or add it once the project exists", variable.Name, reference.Target))
		}
		problem, err := s.sharedReferenceProblem(ctx, 0, variable.Name, variable.Sensitivity, reference)
		if err != nil {
			return err
		}
		if problem != "" {
			return fmt.Errorf("%w: %w", ErrInvalidPlan, invalidField(variable.Name, "%s", problem))
		}
	}
	return nil
}
