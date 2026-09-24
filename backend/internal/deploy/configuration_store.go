package deploy

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	maxDeploymentVariableValue = 64 << 10
	maxDotenvBytes             = 256 << 10
)

// DeploymentVariable is the ordinary read model. It intentionally carries a
// fixed mask and digest instead of a value-derived preview. Even an operator
// with read access cannot use value length or a reference chain to infer a
// secret; revealing remains a separate admin/session action.
type DeploymentVariable struct {
	Name            string             `json:"name"`
	Revision        int                `json:"revision"`
	Sensitivity     string             `json:"sensitivity"`
	Scopes          []string           `json:"scopes"`
	Masked          string             `json:"masked"`
	ValueDigest     string             `json:"valueDigest"`
	Reference       *VariableReference `json:"reference,omitempty"`
	Pending         bool               `json:"pending"`
	CreatedBy       string             `json:"createdBy"`
	CreatedAt       time.Time          `json:"createdAt"`
	EnvironmentID   int64              `json:"environmentId"`
	DesiredRevision int                `json:"desiredRevision"`
}

type VariableWriteRequest struct {
	Revision    int      `json:"revision"`
	Value       *string  `json:"value,omitempty"`
	Reference   string   `json:"reference,omitempty"`
	Sensitivity string   `json:"sensitivity"`
	Scopes      []string `json:"scopes"`
}

type DotenvImportRequest struct {
	Revision    int      `json:"revision"`
	Dotenv      string   `json:"dotenv"`
	Sensitivity string   `json:"sensitivity"`
	Scopes      []string `json:"scopes"`
}

// DotenvImportVerdict is what importing the same request would do to one name:
// added, changed, unchanged or refused. Reason names a refusal (invalid_name,
// duplicate or invalid_value) because one refused name refuses the whole
// import, and the page has to point at the line to fix.
type DotenvImportVerdict struct {
	Name   string `json:"name"`
	Line   int    `json:"line"`
	Change string `json:"change"`
	Reason string `json:"reason,omitempty"`
}

// DotenvImportPreview answers POST …/variables/import?dryRun=1, one verdict
// per name in the order the names were written.
type DotenvImportPreview struct {
	Variables []DotenvImportVerdict `json:"variables"`
}

type VariableMutationResult struct {
	Variable        DeploymentVariable   `json:"variable"`
	Variables       []DeploymentVariable `json:"variables,omitempty"`
	GeneratedValue  string               `json:"generatedValue,omitempty"`
	DesiredRevision int                  `json:"desiredRevision"`
}

type VariableReveal struct {
	Name      string             `json:"name"`
	Value     string             `json:"value"`
	Reference *VariableReference `json:"reference,omitempty"`
}

type PendingChange struct {
	Kind         string `json:"kind"`
	Name         string `json:"name"`
	Change       string `json:"change"`
	BeforeDigest string `json:"beforeDigest,omitempty"`
	AfterDigest  string `json:"afterDigest,omitempty"`
}

type PendingState struct {
	Pending          bool            `json:"pending"`
	DesiredRevision  int             `json:"desiredRevision"`
	LiveReleaseID    int64           `json:"liveReleaseId,omitempty"`
	LivePlanRevision int             `json:"livePlanRevision,omitempty"`
	Changes          []PendingChange `json:"changes"`
}

type EnvironmentConfiguration struct {
	Revision     int                  `json:"revision"`
	Build        BuildPlanConfig      `json:"build"`
	Runtime      RuntimePlanConfig    `json:"runtime"`
	Variables    []DeploymentVariable `json:"variables"`
	Dependencies []PlannedDependency  `json:"dependencies"`
	Checks       []PlannedCheck       `json:"checks"`
	Domains      []PlannedDomain      `json:"domains"`
	Pending      *PendingState        `json:"pending"`
	// Source and Identity are the environment's current desired source, so
	// the settings page can prefill the Source card from the same read that
	// already answers everything else about desired state. Both are absent
	// for a legacy project with no deploy_sources row at its desired
	// revision. Source never carries a credential secret — CredentialID
	// names one, the same as everywhere else this type is returned.
	Source   *DraftSourceConfig `json:"source,omitempty"`
	Identity *SourceIdentity    `json:"identity,omitempty"`
}

type ConfigurationWriteRequest struct {
	Revision     int                 `json:"revision"`
	Build        BuildPlanConfig     `json:"build"`
	Runtime      RuntimePlanConfig   `json:"runtime"`
	Dependencies []PlannedDependency `json:"dependencies"`
	Checks       []PlannedCheck      `json:"checks"`
	Domains      []PlannedDomain     `json:"domains"`
}

type storedVariableValue struct {
	id              int64
	name            string
	revision        int
	sensitivity     string
	scopes          []string
	value           string
	valueDigest     string
	createdBy       string
	createdAt       time.Time
	environmentID   int64
	desiredRevision int
	pending         bool
}

// ParseDotenv is the server-authoritative bulk parser. It supports comments,
// empty values and single/double-quoted multiline values while rejecting
// duplicates and trailing material after a quoted value. It deliberately does
// not expand shell variables or escapes in unquoted values.
func ParseDotenv(input string) (map[string]string, error) {
	entries, err := parseDotenvEntries(input)
	if err = firstDotenvError(entries, err); err != nil {
		return nil, err
	}
	result := make(map[string]string, len(entries))
	for _, entry := range entries {
		result[entry.name] = entry.value
	}
	return result, nil
}

// dotenvEntry is one NAME=value assignment where it was written. refused is
// the entry's own fault — a name no variable may have, a name given twice, a
// value no variable may hold — which a preview reports beside every other
// name. A line the parser cannot read past is the whole input's fault
// instead, and parseDotenvEntries returns it as its error.
type dotenvEntry struct {
	name, value string
	line        int
	refused     string
	err         error
}

// The reasons an entry is refused, as an import preview names them.
const (
	dotenvRefusedName      = "invalid_name"
	dotenvRefusedDuplicate = "duplicate"
	dotenvRefusedValue     = "invalid_value"
)

// firstDotenvError is the error a strict read stops at. Every entry read lies
// before the point a structural error stopped the parser, so the earliest
// refused entry, when there is one, comes first.
func firstDotenvError(entries []dotenvEntry, structural error) error {
	for _, entry := range entries {
		if entry.err != nil {
			return entry.err
		}
	}
	return structural
}

func parseDotenvEntries(input string) ([]dotenvEntry, error) {
	if len(input) > maxDotenvBytes {
		return nil, fmt.Errorf("%w: dotenv input exceeds 256 KiB", ErrInvalidVariable)
	}
	input = strings.ReplaceAll(input, "\r\n", "\n")
	input = strings.ReplaceAll(input, "\r", "\n")
	entries := []dotenvEntry{}
	seen := map[string]bool{}
	for offset, line := 0, 1; offset < len(input); line++ {
		entryLine := line
		for offset < len(input) && (input[offset] == ' ' || input[offset] == '\t') {
			offset++
		}
		if offset >= len(input) {
			break
		}
		if input[offset] == '\n' {
			offset++
			continue
		}
		if input[offset] == '#' {
			for offset < len(input) && input[offset] != '\n' {
				offset++
			}
			if offset < len(input) {
				offset++
			}
			continue
		}
		if strings.HasPrefix(input[offset:], "export ") {
			offset += len("export ")
		}
		keyStart := offset
		for offset < len(input) && input[offset] != '=' && input[offset] != '\n' {
			offset++
		}
		if offset >= len(input) || input[offset] != '=' {
			return entries, fmt.Errorf("%w: line %d needs NAME=value", ErrInvalidVariable, line)
		}
		key := strings.TrimSpace(input[keyStart:offset])
		entry := dotenvEntry{name: key, line: line}
		switch {
		case ValidateEnvKey(key) != nil:
			entry.refused, entry.err = dotenvRefusedName, fmt.Errorf("%w: line %d has invalid name %q", ErrInvalidVariable, line, key)
		case seen[key]:
			entry.refused, entry.err = dotenvRefusedDuplicate, fmt.Errorf("%w: duplicate name %q", ErrInvalidVariable, key)
		}
		seen[key] = true
		offset++
		for offset < len(input) && (input[offset] == ' ' || input[offset] == '\t') {
			offset++
		}
		value := ""
		if offset < len(input) && (input[offset] == '\'' || input[offset] == '"') {
			quote := input[offset]
			offset++
			var out strings.Builder
			closed := false
			for offset < len(input) {
				ch := input[offset]
				offset++
				if ch == quote {
					closed = true
					break
				}
				if ch == '\n' {
					line++
				}
				if quote == '"' && ch == '\\' && offset < len(input) {
					next := input[offset]
					switch next {
					case 'n':
						out.WriteByte('\n')
					case 'r':
						out.WriteByte('\r')
					case 't':
						out.WriteByte('\t')
					case '\\', '"':
						out.WriteByte(next)
					default:
						out.WriteByte('\\')
						out.WriteByte(next)
					}
					offset++
					continue
				}
				out.WriteByte(ch)
			}
			if !closed {
				return append(entries, entry), fmt.Errorf("%w: quoted value starting on line %d is not closed", ErrInvalidVariable, entryLine)
			}
			value = out.String()
			for offset < len(input) && (input[offset] == ' ' || input[offset] == '\t') {
				offset++
			}
			if offset < len(input) && input[offset] != '\n' && input[offset] != '#' {
				return append(entries, entry), fmt.Errorf("%w: line %d has text after its quoted value", ErrInvalidVariable, line)
			}
			if offset < len(input) && input[offset] == '#' {
				for offset < len(input) && input[offset] != '\n' {
					offset++
				}
			}
		} else {
			valueStart := offset
			for offset < len(input) && input[offset] != '\n' {
				offset++
			}
			value = strings.TrimSpace(input[valueStart:offset])
		}
		if entry.err == nil && (len(value) > maxDeploymentVariableValue || strings.ContainsRune(value, '\x00')) {
			entry.refused, entry.err = dotenvRefusedValue, fmt.Errorf("%w: value for %s is invalid or exceeds 64 KiB", ErrInvalidVariable, key)
		}
		entry.value = value
		entries = append(entries, entry)
		if offset < len(input) && input[offset] == '\n' {
			offset++
		}
		if len(entries) > 256 {
			return entries, fmt.Errorf("%w: dotenv input exceeds 256 variables", ErrInvalidVariable)
		}
	}
	return entries, nil
}

func validateVariableWrite(request VariableWriteRequest) (string, error) {
	if request.Sensitivity != "plain" && request.Sensitivity != "secret" {
		return "", fmt.Errorf("%w: sensitivity must be plain or secret", ErrInvalidVariable)
	}
	if len(request.Scopes) == 0 || len(request.Scopes) > 3 {
		return "", fmt.Errorf("%w: at least one scope is required", ErrInvalidVariable)
	}
	seen := map[string]bool{}
	for _, scope := range request.Scopes {
		if scope != "build" && scope != "runtime" && scope != "release_task" || seen[scope] {
			return "", fmt.Errorf("%w: invalid or duplicate scope %q", ErrInvalidVariable, scope)
		}
		seen[scope] = true
	}
	if (request.Value == nil) == (request.Reference == "") {
		return "", fmt.Errorf("%w: provide exactly one value or reference", ErrInvalidVariable)
	}
	value := request.Reference
	if request.Value != nil {
		value = *request.Value
	}
	if len(value) > maxDeploymentVariableValue || strings.ContainsRune(value, '\x00') {
		return "", fmt.Errorf("%w: value is invalid or exceeds 64 KiB", ErrInvalidVariable)
	}
	if request.Reference != "" {
		reference, err := ParseVariableReference(request.Reference)
		if err != nil {
			return "", err
		}
		if (reference.Kind == "credential" || reference.Kind == "database") && request.Sensitivity != "secret" {
			return "", fmt.Errorf("%w: %s references must be secret", ErrInvalidVariable, reference.Kind)
		}
	}
	return value, nil
}

func (s *PlanningStore) ListVariables(ctx context.Context, projectID, environmentID int64) ([]DeploymentVariable, error) {
	values, err := s.activeVariableValues(ctx, s.db, projectID, environmentID)
	if err != nil {
		return nil, err
	}
	return variableViews(values), nil
}

func (s *PlanningStore) RevealVariable(ctx context.Context, projectID, environmentID int64, name string) (*VariableReveal, error) {
	if ValidateEnvKey(name) != nil {
		return nil, ErrInvalidVariable
	}
	values, err := s.activeVariableValues(ctx, s.db, projectID, environmentID)
	if err != nil {
		return nil, err
	}
	for _, value := range values {
		if value.name != name {
			continue
		}
		result := &VariableReveal{Name: name, Value: value.value}
		if reference, parseErr := ParseVariableReference(value.value); parseErr == nil {
			result.Reference = &reference
		}
		return result, nil
	}
	return nil, fmt.Errorf("%w: %s", ErrVariableNotFound, name)
}

func (s *PlanningStore) PutVariable(
	ctx context.Context,
	projectID, environmentID int64,
	name, actor string,
	request VariableWriteRequest,
) (*VariableMutationResult, error) {
	if ValidateEnvKey(name) != nil {
		return nil, fmt.Errorf("%w: invalid name %q", ErrInvalidVariable, name)
	}
	value, err := validateVariableWrite(request)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	desired, err := s.advanceDesiredRevisionTx(ctx, tx, projectID, environmentID, request.Revision)
	if err != nil {
		return nil, err
	}
	if err := s.writeVariableRevisionTx(ctx, tx, environmentID, name, value, request.Sensitivity, request.Scopes, actor); err != nil {
		return nil, err
	}
	if err := s.validateActiveVariableGraphTx(ctx, tx, environmentID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	variables, err := s.ListVariables(ctx, projectID, environmentID)
	if err != nil {
		return nil, err
	}
	result := &VariableMutationResult{Variables: variables, DesiredRevision: desired}
	for _, variable := range variables {
		if variable.Name == name {
			result.Variable = variable
			break
		}
	}
	return result, nil
}

func (s *PlanningStore) GenerateVariable(
	ctx context.Context,
	projectID, environmentID int64,
	name, actor string,
	revision int,
	scopes []string,
) (*VariableMutationResult, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	generated := base64.RawURLEncoding.EncodeToString(raw)
	result, err := s.PutVariable(ctx, projectID, environmentID, name, actor, VariableWriteRequest{
		Revision: revision, Value: &generated, Sensitivity: "secret", Scopes: scopes,
	})
	if err != nil {
		return nil, err
	}
	result.GeneratedValue = generated
	return result, nil
}

func (s *PlanningStore) RotateVariable(
	ctx context.Context,
	projectID, environmentID int64,
	name, actor string,
	revision int,
) (*VariableMutationResult, error) {
	values, err := s.activeVariableValues(ctx, s.db, projectID, environmentID)
	if err != nil {
		return nil, err
	}
	for _, value := range values {
		if value.name == name {
			if value.sensitivity != "secret" {
				return nil, fmt.Errorf("%w: only secret variables can be rotated", ErrInvalidVariable)
			}
			return s.GenerateVariable(ctx, projectID, environmentID, name, actor, revision, value.scopes)
		}
	}
	return nil, fmt.Errorf("%w: %s does not exist", ErrInvalidVariable, name)
}

func (s *PlanningStore) ImportDotenv(
	ctx context.Context,
	projectID, environmentID int64,
	actor string,
	request DotenvImportRequest,
) (*VariableMutationResult, error) {
	parsed, err := ParseDotenv(request.Dotenv)
	if err != nil {
		return nil, err
	}
	if len(parsed) == 0 {
		return nil, fmt.Errorf("%w: dotenv input is empty", ErrInvalidVariable)
	}
	template := VariableWriteRequest{
		Revision: request.Revision, Value: new(string), Sensitivity: request.Sensitivity, Scopes: request.Scopes,
	}
	if _, err := validateVariableWrite(template); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	desired, err := s.advanceDesiredRevisionTx(ctx, tx, projectID, environmentID, request.Revision)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(parsed))
	for name := range parsed {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		value := parsed[name]
		if err := s.writeVariableRevisionTx(ctx, tx, environmentID, name, value, request.Sensitivity, request.Scopes, actor); err != nil {
			return nil, err
		}
	}
	if err := s.validateActiveVariableGraphTx(ctx, tx, environmentID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	variables, err := s.ListVariables(ctx, projectID, environmentID)
	if err != nil {
		return nil, err
	}
	return &VariableMutationResult{Variables: variables, DesiredRevision: desired}, nil
}

// PreviewDotenvImport answers what ImportDotenv would do with the same request
// and writes nothing. Values meet the stored ones only as digests, here on the
// server, so the answer carries neither a value nor a digest of one. What the
// import would refuse as a whole — a line the parser cannot read past, a bad
// sensitivity or scope, a reference that would not resolve — is refused here
// with the same error; a refusal that belongs to one name is that name's
// verdict instead, so every other line can still be read.
func (s *PlanningStore) PreviewDotenvImport(
	ctx context.Context,
	projectID, environmentID int64,
	request DotenvImportRequest,
) (*DotenvImportPreview, error) {
	entries, err := parseDotenvEntries(request.Dotenv)
	if err != nil {
		return nil, firstDotenvError(entries, err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("%w: dotenv input is empty", ErrInvalidVariable)
	}
	template := VariableWriteRequest{Value: new(string), Sensitivity: request.Sensitivity, Scopes: request.Scopes}
	if _, err := validateVariableWrite(template); err != nil {
		return nil, err
	}
	stored, err := s.activeVariableValues(ctx, s.db, projectID, environmentID)
	if err != nil {
		return nil, err
	}
	current := make(map[string]storedVariableValue, len(stored))
	after := make(map[string]string, len(stored)+len(entries))
	for _, value := range stored {
		current[value.name], after[value.name] = value, value.value
	}
	scopes := slices.Sorted(slices.Values(request.Scopes))
	preview := &DotenvImportPreview{Variables: make([]DotenvImportVerdict, 0, len(entries))}
	position := map[string]int{}
	refused := false
	for _, entry := range entries {
		if at, repeated := position[entry.name]; repeated {
			preview.Variables[at].Change, preview.Variables[at].Reason = "refused", dotenvRefusedDuplicate
			refused = true
			continue
		}
		position[entry.name] = len(preview.Variables)
		verdict := DotenvImportVerdict{Name: entry.name, Line: entry.line}
		existing, exists := current[entry.name]
		switch {
		case entry.refused != "":
			verdict.Change, verdict.Reason = "refused", entry.refused
			refused = true
		case !exists:
			verdict.Change = "added"
		case existing.valueDigest == digestBytes([]byte(entry.value)) &&
			existing.sensitivity == request.Sensitivity &&
			slices.Equal(slices.Sorted(slices.Values(existing.scopes)), scopes):
			verdict.Change = "unchanged"
		default:
			verdict.Change = "changed"
		}
		after[entry.name] = entry.value
		preview.Variables = append(preview.Variables, verdict)
	}
	// The import checks the references of the set it leaves behind. With a
	// refused name there is no such set: the parse already refuses the import.
	if !refused {
		if _, _, err := ResolveVariableGraph(after, nil); err != nil {
			return nil, err
		}
	}
	return preview, nil
}

func (s *PlanningStore) DeleteVariable(
	ctx context.Context,
	projectID, environmentID int64,
	name string,
	revision int,
) (int, error) {
	if ValidateEnvKey(name) != nil {
		return 0, ErrInvalidVariable
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	desired, err := s.advanceDesiredRevisionTx(ctx, tx, projectID, environmentID, revision)
	if err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE deploy_variable_revisions SET active = 0
		 WHERE environment_id = ? AND key = ? AND active = 1`, environmentID, name)
	if err != nil {
		return 0, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return 0, fmt.Errorf("%w: %s does not exist", ErrInvalidVariable, name)
	}
	if err := s.validateActiveVariableGraphTx(ctx, tx, environmentID); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return desired, nil
}

func (s *PlanningStore) writeVariableRevisionTx(
	ctx context.Context,
	tx *sql.Tx,
	environmentID int64,
	name, value, sensitivity string,
	scopes []string,
	actor string,
) error {
	sealed, err := s.sealer.Seal(value)
	if err != nil {
		return err
	}
	var revision int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(revision), 0) + 1
		  FROM deploy_variable_revisions WHERE environment_id = ? AND key = ?`, environmentID, name).Scan(&revision); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE deploy_variable_revisions SET active = 0
		 WHERE environment_id = ? AND key = ? AND active = 1`, environmentID, name); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO deploy_variable_revisions(
		  environment_id, key, revision, sensitivity, scopes, value_enc,
		  value_digest, active, created_by, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, 1, ?, ?)`, environmentID, name, revision,
		sensitivity, strings.Join(scopes, ","), sealed, digestBytes([]byte(value)), actor,
		s.now().UTC().Unix())
	return err
}

func (s *PlanningStore) advanceDesiredRevisionTx(
	ctx context.Context,
	tx *sql.Tx,
	projectID, environmentID int64,
	expected int,
) (int, error) {
	var current int
	if err := tx.QueryRowContext(ctx, `
		SELECT desired_revision FROM deploy_environments
		 WHERE id = ? AND project_id = ? AND archived_at = 0`, environmentID, projectID).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrEnvironmentNotFound
		}
		return 0, err
	}
	if expected != current {
		return 0, fmt.Errorf("%w: current revision is %d", ErrRevisionConflict, current)
	}
	next := current + 1
	for _, statement := range []string{
		`INSERT INTO deploy_sources(environment_id, revision, kind, config_json, credential_id, identity_json, digest, created_at)
		 SELECT environment_id, ?, kind, config_json, credential_id, identity_json, digest, ?
		   FROM deploy_sources WHERE environment_id = ? AND revision = ?`,
		`INSERT INTO deploy_build_plans(environment_id, revision, method, config_json, evidence_json, preview, digest, created_at)
		 SELECT environment_id, ?, method, config_json, evidence_json, preview, digest, ?
		   FROM deploy_build_plans WHERE environment_id = ? AND revision = ?`,
		`INSERT INTO deploy_runtime_plans(environment_id, revision, config_json, preview, digest, created_at)
		 SELECT environment_id, ?, config_json, preview, digest, ?
		   FROM deploy_runtime_plans WHERE environment_id = ? AND revision = ?`,
	} {
		result, err := tx.ExecContext(ctx, statement, next, s.now().UTC().Unix(), environmentID, current)
		if err != nil {
			return 0, err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return 0, fmt.Errorf("%w: revision %d has incomplete plan rows", ErrInvalidPlan, current)
		}
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE deploy_environments SET desired_revision = ?, updated_at = ?
		 WHERE id = ? AND project_id = ? AND desired_revision = ? AND archived_at = 0`,
		next, s.now().UTC().Unix(), environmentID, projectID, current)
	if err != nil {
		return 0, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return 0, ErrRevisionConflict
	}
	return next, nil
}

type variableRows interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func (s *PlanningStore) activeVariableValues(
	ctx context.Context,
	query variableRows,
	projectID, environmentID int64,
) ([]storedVariableValue, error) {
	rows, err := query.QueryContext(ctx, `
		SELECT v.id, v.key, v.revision, v.sensitivity, v.scopes, v.value_enc,
		       v.value_digest, v.created_by, v.created_at, e.desired_revision,
		       CASE WHEN e.live_release_id = 0 OR NOT EXISTS (
		         SELECT 1 FROM deploy_releases live
		         JOIN deploy_run_variable_revisions rv ON rv.run_id = live.run_id
		         WHERE live.id = e.live_release_id AND rv.variable_revision_id = v.id
		       ) THEN 1 ELSE 0 END
		  FROM deploy_variable_revisions v
		  JOIN deploy_environments e ON e.id = v.environment_id
		 WHERE v.environment_id = ? AND e.project_id = ? AND e.archived_at = 0
		   AND v.active = 1 ORDER BY v.key`, environmentID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []storedVariableValue{}
	for rows.Next() {
		var value storedVariableValue
		var scopes, sealed string
		var created int64
		var pending int
		if err := rows.Scan(&value.id, &value.name, &value.revision, &value.sensitivity,
			&scopes, &sealed, &value.valueDigest, &value.createdBy, &created,
			&value.desiredRevision, &pending); err != nil {
			return nil, err
		}
		value.scopes = strings.Split(scopes, ",")
		value.createdAt = unixTime(created)
		value.environmentID = environmentID
		value.pending = pending != 0
		value.value, err = s.sealer.Open(sealed)
		if err != nil {
			return nil, fmt.Errorf("deployment variable %s cannot be decrypted", value.name)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(values) == 0 {
		var exists int
		if err := s.db.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM deploy_environments
			 WHERE id = ? AND project_id = ? AND archived_at = 0`, environmentID, projectID).Scan(&exists); err != nil {
			return nil, err
		}
		if exists == 0 {
			return nil, ErrEnvironmentNotFound
		}
	}
	return values, nil
}

func variableViews(values []storedVariableValue) []DeploymentVariable {
	result := make([]DeploymentVariable, 0, len(values))
	for _, value := range values {
		masked := "configured"
		if value.sensitivity == "secret" {
			masked = "••••••••"
		}
		view := DeploymentVariable{
			Name: value.name, Revision: value.revision, Sensitivity: value.sensitivity,
			Scopes: append([]string(nil), value.scopes...), Masked: masked,
			ValueDigest: value.valueDigest, Pending: value.pending, CreatedBy: value.createdBy,
			CreatedAt: value.createdAt, EnvironmentID: value.environmentID,
			DesiredRevision: value.desiredRevision,
		}
		if reference, err := ParseVariableReference(value.value); err == nil {
			view.Reference = &reference
		}
		result = append(result, view)
	}
	return result
}

func (s *PlanningStore) validateActiveVariableGraphTx(ctx context.Context, tx *sql.Tx, environmentID int64) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT key, value_enc FROM deploy_variable_revisions
		 WHERE environment_id = ? AND active = 1 ORDER BY key`, environmentID)
	if err != nil {
		return err
	}
	defer rows.Close()
	values := map[string]string{}
	for rows.Next() {
		var name, sealed string
		if err := rows.Scan(&name, &sealed); err != nil {
			return err
		}
		value, err := s.sealer.Open(sealed)
		if err != nil {
			return err
		}
		values[name] = value
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, _, err = ResolveVariableGraph(values, nil)
	return err
}

// ResolveVariableGraph resolves same-environment variable references with a
// three-colour DFS. External reference kinds are delegated to the feature
// owner and may be left unresolved by passing nil; graph integrity never
// depends on contacting an external service.
func ResolveVariableGraph(
	values map[string]string,
	external func(VariableReference) (string, bool, error),
) (map[string]string, map[string]bool, error) {
	resolved := make(map[string]string, len(values))
	secretLeaf := make(map[string]bool, len(values))
	state := map[string]uint8{}
	stack := []string{}
	var visit func(string) (string, bool, error)
	visit = func(name string) (string, bool, error) {
		switch state[name] {
		case 2:
			return resolved[name], secretLeaf[name], nil
		case 1:
			cycle := append(append([]string(nil), stack...), name)
			return "", false, fmt.Errorf("%w: %s", ErrVariableCycle, strings.Join(cycle, " -> "))
		}
		value, ok := values[name]
		if !ok {
			return "", false, fmt.Errorf("%w: missing variable reference %s", ErrInvalidVariable, name)
		}
		state[name] = 1
		stack = append(stack, name)
		resolvedValue, leafSecret := value, false
		if reference, err := ParseVariableReference(value); err == nil {
			if reference.Kind == "variable" {
				resolvedValue, leafSecret, err = visit(reference.Target)
				if err != nil {
					return "", false, err
				}
			} else if external != nil {
				resolvedValue, leafSecret, err = external(reference)
				if err != nil {
					return "", false, err
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[name] = 2
		resolved[name], secretLeaf[name] = resolvedValue, leafSecret
		return resolvedValue, leafSecret, nil
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, _, err := visit(name); err != nil {
			return nil, nil, err
		}
	}
	return resolved, secretLeaf, nil
}

func parsePositiveReferenceID(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("%w: reference id must be a positive integer", ErrInvalidVariable)
	}
	return id, nil
}

// PendingState compares the current desired inputs to the immutable inputs of
// the live release. It reports only names and digests: a configuration diff is
// useful operational evidence, but it is not a second secret reveal surface.
func (s *PlanningStore) PendingState(ctx context.Context, projectID, environmentID int64) (*PendingState, error) {
	var desired int
	var liveID int64
	if err := s.db.QueryRowContext(ctx, `
		SELECT desired_revision, live_release_id FROM deploy_environments
		 WHERE id = ? AND project_id = ? AND archived_at = 0`, environmentID, projectID).
		Scan(&desired, &liveID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrEnvironmentNotFound
		}
		return nil, err
	}
	state := &PendingState{DesiredRevision: desired, LiveReleaseID: liveID, Changes: []PendingChange{}}
	if liveID == 0 {
		state.Pending = true
		state.Changes = append(state.Changes, PendingChange{Kind: "deployment", Name: "first release", Change: "added"})
		return state, nil
	}
	var liveRunID int64
	var liveSourceID, liveBuildID, liveRuntimeID int64
	if err := s.db.QueryRowContext(ctx, `
		SELECT plan_revision, run_id, source_id, build_plan_id, runtime_plan_id
		  FROM deploy_releases WHERE id = ? AND environment_id = ?`, liveID, environmentID).
		Scan(&state.LivePlanRevision, &liveRunID, &liveSourceID, &liveBuildID, &liveRuntimeID); err != nil {
		return nil, err
	}
	for _, component := range []struct {
		kind, table string
		liveID      int64
	}{
		{"source", "deploy_sources", liveSourceID},
		{"build", "deploy_build_plans", liveBuildID},
		{"runtime", "deploy_runtime_plans", liveRuntimeID},
	} {
		var before, after string
		if err := s.db.QueryRowContext(ctx, `SELECT digest FROM `+component.table+` WHERE id = ?`, component.liveID).Scan(&before); err != nil {
			return nil, err
		}
		if err := s.db.QueryRowContext(ctx, `SELECT digest FROM `+component.table+` WHERE environment_id = ? AND revision = ?`, environmentID, desired).Scan(&after); err != nil {
			return nil, err
		}
		if before != after {
			state.Changes = append(state.Changes, PendingChange{
				Kind: component.kind, Name: component.kind + " plan", Change: "changed",
				BeforeDigest: before, AfterDigest: after,
			})
		}
	}
	currentVariables, err := variableDigestMap(ctx, s.db, `
		SELECT key, value_digest FROM deploy_variable_revisions
		 WHERE environment_id = ? AND active = 1 ORDER BY key`, environmentID)
	if err != nil {
		return nil, err
	}
	liveVariables, err := variableDigestMap(ctx, s.db, `
		SELECT v.key, v.value_digest FROM deploy_run_variable_revisions rv
		 JOIN deploy_variable_revisions v ON v.id = rv.variable_revision_id
		 WHERE rv.run_id = ? ORDER BY v.key`, liveRunID)
	if err != nil {
		return nil, err
	}
	state.Changes = append(state.Changes, diffNamedDigests("variable", liveVariables, currentVariables)...)

	currentDependencies := []json.RawMessage{}
	rows, err := s.db.QueryContext(ctx, `
		SELECT json_object('kind', kind, 'ownership', ownership, 'resourceKind', resource_kind,
		                   'resourceId', resource_id, 'config', json(config_json))
		  FROM deploy_dependencies WHERE environment_id = ? AND release_id = 0
		 ORDER BY kind, resource_kind, resource_id`, environmentID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return nil, err
		}
		currentDependencies = append(currentDependencies, json.RawMessage(raw))
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	var liveDependenciesJSON, liveChecksJSON string
	if err := s.db.QueryRowContext(ctx, `
		SELECT dependencies_json, checks_json FROM deploy_run_plan_snapshots WHERE run_id = ?`, liveRunID).
		Scan(&liveDependenciesJSON, &liveChecksJSON); err != nil {
		return nil, err
	}
	currentDependenciesJSON, _ := json.Marshal(currentDependencies)
	if digestBytes([]byte(liveDependenciesJSON)) != digestBytes(currentDependenciesJSON) {
		state.Changes = append(state.Changes, PendingChange{
			Kind: "dependency", Name: "dependencies", Change: "changed",
			BeforeDigest: digestBytes([]byte(liveDependenciesJSON)), AfterDigest: digestBytes(currentDependenciesJSON),
		})
	}
	currentChecks := []json.RawMessage{}
	rows, err = s.db.QueryContext(ctx, `
		SELECT json_object('name', name, 'kind', kind, 'phase', phase, 'config', json(config_json),
		                   'required', json(CASE WHEN required <> 0 THEN 'true' ELSE 'false' END))
		  FROM deploy_checks WHERE environment_id = ? ORDER BY ordinal, name`, environmentID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return nil, err
		}
		currentChecks = append(currentChecks, json.RawMessage(raw))
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	currentChecksJSON, _ := json.Marshal(currentChecks)
	if digestBytes([]byte(liveChecksJSON)) != digestBytes(currentChecksJSON) {
		state.Changes = append(state.Changes, PendingChange{
			Kind: "check", Name: "health checks", Change: "changed",
			BeforeDigest: digestBytes([]byte(liveChecksJSON)), AfterDigest: digestBytes(currentChecksJSON),
		})
	}
	state.Pending = state.LivePlanRevision != desired || len(state.Changes) != 0
	return state, nil
}

func (s *PlanningStore) EnvironmentConfiguration(
	ctx context.Context,
	projectID, environmentID int64,
) (*EnvironmentConfiguration, error) {
	result := &EnvironmentConfiguration{
		Variables: []DeploymentVariable{}, Dependencies: []PlannedDependency{},
		Checks: []PlannedCheck{}, Domains: []PlannedDomain{},
	}
	var buildJSON, runtimeJSON string
	// The LEFT JOIN is the same desired-revision source row SaveEnvironmentSource
	// starts from; a legacy project without one leaves sourceJSON/identityJSON
	// NULL instead of failing this read.
	var sourceJSON, identityJSON sql.NullString
	if err := s.db.QueryRowContext(ctx, `
		SELECT e.desired_revision, b.config_json, r.config_json, src.config_json, src.identity_json
		  FROM deploy_environments e
		  JOIN deploy_build_plans b ON b.environment_id = e.id AND b.revision = e.desired_revision
		  JOIN deploy_runtime_plans r ON r.environment_id = e.id AND r.revision = e.desired_revision
		  LEFT JOIN deploy_sources src ON src.environment_id = e.id AND src.revision = e.desired_revision
		 WHERE e.id = ? AND e.project_id = ? AND e.archived_at = 0`, environmentID, projectID).
		Scan(&result.Revision, &buildJSON, &runtimeJSON, &sourceJSON, &identityJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrEnvironmentNotFound
		}
		return nil, err
	}
	if sourceJSON.Valid && identityJSON.Valid {
		var source DraftSourceConfig
		var identity SourceIdentity
		if json.Unmarshal([]byte(sourceJSON.String), &source) != nil || json.Unmarshal([]byte(identityJSON.String), &identity) != nil {
			return nil, fmt.Errorf("%w: desired source configuration is malformed", ErrInvalidPlan)
		}
		result.Source, result.Identity = &source, &identity
	}
	if json.Unmarshal([]byte(buildJSON), &result.Build) != nil || json.Unmarshal([]byte(runtimeJSON), &result.Runtime) != nil {
		return nil, fmt.Errorf("%w: desired plan configuration is malformed", ErrInvalidPlan)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT kind, ownership, resource_kind, resource_id, config_json
		  FROM deploy_dependencies WHERE environment_id = ? AND release_id = 0
		 ORDER BY kind, resource_kind, resource_id`, environmentID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var dependency PlannedDependency
		var config string
		if err := rows.Scan(&dependency.Kind, &dependency.Ownership, &dependency.ResourceKind, &dependency.ResourceID, &config); err != nil {
			rows.Close()
			return nil, err
		}
		dependency.Config = json.RawMessage(config)
		if dependency.Kind == "domain" {
			var domain struct {
				Hostname   string            `json:"hostname"`
				HTTPS      bool              `json:"https"`
				Protection *DomainProtection `json:"protection"`
			}
			if json.Unmarshal(dependency.Config, &domain) != nil {
				rows.Close()
				return nil, fmt.Errorf("%w: domain dependency is malformed", ErrInvalidPlan)
			}
			result.Domains = append(result.Domains, PlannedDomain{
				Hostname: domain.Hostname, HTTPS: domain.HTTPS, Ownership: dependency.Ownership,
				Protection: canonicalDomainProtection(domain.Protection),
			})
		} else {
			result.Dependencies = append(result.Dependencies, dependency)
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	rows, err = s.db.QueryContext(ctx, `
		SELECT name, kind, phase, required, config_json
		  FROM deploy_checks WHERE environment_id = ? ORDER BY ordinal, name`, environmentID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var check PlannedCheck
		var required int
		var config string
		if err := rows.Scan(&check.Name, &check.Kind, &check.Phase, &required, &config); err != nil {
			rows.Close()
			return nil, err
		}
		check.Required, check.Config = required != 0, json.RawMessage(config)
		result.Checks = append(result.Checks, check)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	result.Variables, err = s.ListVariables(ctx, projectID, environmentID)
	if err != nil {
		return nil, err
	}
	result.Pending, err = s.PendingState(ctx, projectID, environmentID)
	return result, err
}

func (s *PlanningStore) SaveEnvironmentConfiguration(
	ctx context.Context,
	projectID, environmentID int64,
	request ConfigurationWriteRequest,
) (*EnvironmentConfiguration, error) {
	if request.Dependencies == nil {
		request.Dependencies = []PlannedDependency{}
	}
	if request.Checks == nil {
		request.Checks = []PlannedCheck{}
	}
	if request.Domains == nil {
		request.Domains = []PlannedDomain{}
	}
	variables, err := s.ListVariables(ctx, projectID, environmentID)
	if err != nil {
		return nil, err
	}
	plannedVariables := make([]PlannedVariable, 0, len(variables))
	for _, variable := range variables {
		plannedVariables = append(plannedVariables, PlannedVariable{
			Name: variable.Name, Sensitivity: variable.Sensitivity, Scopes: variable.Scopes,
		})
	}
	configuration := canonicalConfiguration(PlanConfiguration{
		Build: request.Build, Runtime: request.Runtime, Variables: plannedVariables,
		Dependencies: request.Dependencies, Checks: request.Checks, Domains: request.Domains,
	})
	if err := sealDomainProtection(&configuration); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidPlan, err)
	}
	if err := configuration.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidPlan, err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var current int
	if err := tx.QueryRowContext(ctx, `
		SELECT desired_revision FROM deploy_environments
		 WHERE id = ? AND project_id = ? AND archived_at = 0`, environmentID, projectID).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrEnvironmentNotFound
		}
		return nil, err
	}
	if current != request.Revision {
		return nil, fmt.Errorf("%w: current revision is %d", ErrRevisionConflict, current)
	}
	var kind EnvironmentKind
	if err := tx.QueryRowContext(ctx, `SELECT kind FROM deploy_environments WHERE id=?`, environmentID).Scan(&kind); err != nil {
		return nil, err
	}
	if kind == EnvironmentPreview {
		if err := validatePreviewPlan(environmentID, configuration.Build, configuration.Runtime); err != nil {
			return nil, err
		}
	}
	var previousBuildJSON string
	if err := tx.QueryRowContext(ctx, `
		SELECT config_json FROM deploy_build_plans WHERE environment_id = ? AND revision = ?`,
		environmentID, current).Scan(&previousBuildJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: desired build plan is missing", ErrInvalidPlan)
		}
		return nil, err
	}
	var previousBuild BuildPlanConfig
	if err := json.Unmarshal([]byte(previousBuildJSON), &previousBuild); err != nil {
		return nil, fmt.Errorf("%w: desired build plan is malformed", ErrInvalidPlan)
	}
	configuration.Build.Framework = carriedFramework(previousBuild, configuration.Build)
	next, now := current+1, s.now().UTC().Unix()
	result, err := tx.ExecContext(ctx, `
		INSERT INTO deploy_sources(environment_id, revision, kind, config_json, credential_id, identity_json, digest, created_at)
		 SELECT environment_id, ?, kind, config_json, credential_id, identity_json, digest, ?
		   FROM deploy_sources WHERE environment_id = ? AND revision = ?`, next, now, environmentID, current)
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, fmt.Errorf("%w: desired source plan is missing", ErrInvalidPlan)
	}
	buildJSON, _ := json.Marshal(configuration.Build)
	result, err = tx.ExecContext(ctx, `
		INSERT INTO deploy_build_plans(environment_id, revision, method, config_json, evidence_json, preview, digest, created_at)
		 SELECT environment_id, ?, ?, ?, evidence_json, ?, ?, ?
		   FROM deploy_build_plans WHERE environment_id = ? AND revision = ?`, next,
		configuration.Build.Method, string(buildJSON), renderBuildPreview(configuration.Build), buildPlanDigest(configuration.Build), now,
		environmentID, current)
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, fmt.Errorf("%w: desired build plan is missing", ErrInvalidPlan)
	}
	runtimeJSON, _ := json.Marshal(configuration.Runtime)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO deploy_runtime_plans(environment_id, revision, config_json, preview, digest, created_at)
		VALUES(?, ?, ?, ?, ?, ?)`, environmentID, next, string(runtimeJSON),
		renderRuntimePreview(configuration.Runtime), digestBytes(runtimeJSON), now); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM deploy_dependencies WHERE environment_id = ? AND release_id = 0`, environmentID); err != nil {
		return nil, err
	}
	for _, dependency := range configuration.Dependencies {
		config := dependency.Config
		if len(config) == 0 {
			config = json.RawMessage(`{}`)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO deploy_dependencies(environment_id, release_id, kind, ownership, resource_kind, resource_id, config_json, created_at)
			VALUES(?, 0, ?, ?, ?, ?, ?, ?)`, environmentID, dependency.Kind, dependency.Ownership,
			dependency.ResourceKind, dependency.ResourceID, string(config), now); err != nil {
			return nil, err
		}
	}
	for _, domain := range configuration.Domains {
		domainConfig := map[string]any{"hostname": domain.Hostname, "https": domain.HTTPS}
		if domain.Protection != nil {
			domainConfig["protection"] = domain.Protection
		}
		config := mustJSON(domainConfig)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO deploy_dependencies(environment_id, release_id, kind, ownership, resource_kind, resource_id, config_json, created_at)
			VALUES(?, 0, 'domain', ?, 'proxy_site', ?, ?, ?)`, environmentID, domain.Ownership,
			domain.Hostname, string(config), now); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM deploy_checks WHERE environment_id = ?`, environmentID); err != nil {
		return nil, err
	}
	for ordinal, check := range configuration.Checks {
		config := check.Config
		if len(config) == 0 {
			config = json.RawMessage(`{}`)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO deploy_checks(environment_id, runtime_plan_id, name, kind, phase, config_json, required, ordinal, created_at)
			VALUES(?, 0, ?, ?, ?, ?, ?, ?, ?)`, environmentID, check.Name, check.Kind, check.Phase,
			string(config), boolInt(check.Required), ordinal+1, now); err != nil {
			return nil, err
		}
	}
	result, err = tx.ExecContext(ctx, `
		UPDATE deploy_environments SET desired_revision = ?, strategy = ?, expected_downtime = ?, updated_at = ?
		 WHERE id = ? AND project_id = ? AND desired_revision = ?`, next, configuration.Runtime.Strategy,
		boolInt(configuration.Runtime.Strategy == StrategyStopFirst), now, environmentID, projectID, current)
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, ErrRevisionConflict
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.EnvironmentConfiguration(ctx, projectID, environmentID)
}

// SaveEnvironmentSource changes a committed project's source — repository,
// ref, subdirectory, credential, or image reference — without touching the
// live release. It clones build and runtime forward unchanged (exactly as
// SaveEnvironmentConfiguration clones the source row) and writes only a
// fresh deploy_sources row, because the caller has already run the read-only
// inspection this needs (resolving the ref or registry digest) and is
// handing this the resulting identity; a source kind cannot change here,
// since build/runtime were built for the kind already live.
//
// PendingState already compares the live release's source digest against
// the desired one, so this alone is what makes a source edit show up as a
// pending "source" change; the Git watcher already keys its own cursor on
// the source config's digest, so a changed URL/ref resets that cursor and
// the next poll reports branch_changed on its own — neither needs any
// change here.
func (s *PlanningStore) SaveEnvironmentSource(
	ctx context.Context,
	projectID, environmentID int64,
	revision int,
	source DraftSourceConfig,
	identity SourceIdentity,
) (*EnvironmentConfiguration, error) {
	source = canonicalSourceConfig(source)
	if err := source.ValidateForDeployment(); err != nil {
		return nil, err
	}
	if err := source.validateBlueprintSecretInputs(); err != nil {
		return nil, err
	}
	if exists, err := s.credentialExists(ctx, source.CredentialID); err != nil {
		return nil, err
	} else if !exists {
		return nil, fmt.Errorf("%w: credential does not exist", ErrInvalidSource)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var current int
	var currentKind SourceKind
	var currentSourceJSON string
	if err := tx.QueryRowContext(ctx, `
		SELECT e.desired_revision, s.kind, s.config_json
		  FROM deploy_environments e
		  JOIN deploy_sources s ON s.environment_id = e.id AND s.revision = e.desired_revision
		 WHERE e.id = ? AND e.project_id = ? AND e.archived_at = 0`, environmentID, projectID).
		Scan(&current, &currentKind, &currentSourceJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrEnvironmentNotFound
		}
		return nil, err
	}
	if current != revision {
		return nil, fmt.Errorf("%w: current revision is %d", ErrRevisionConflict, current)
	}
	if currentKind != source.Kind {
		return nil, fmt.Errorf("%w: source kind cannot change from %s to %s", ErrInvalidSource, currentKind, source.Kind)
	}
	next, now := current+1, s.now().UTC().Unix()
	sourceJSON, _ := json.Marshal(source)
	identityJSON, _ := json.Marshal(identity)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO deploy_sources(environment_id, revision, kind, config_json, credential_id, identity_json, digest, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)`, environmentID, next, source.Kind, string(sourceJSON),
		source.CredentialID, string(identityJSON), digestBytes(sourceJSON, identityJSON), now); err != nil {
		return nil, err
	}
	var currentSource DraftSourceConfig
	moved := json.Unmarshal([]byte(currentSourceJSON), &currentSource) != nil ||
		!sameSourceLocation(canonicalSourceConfig(currentSource), source)
	if err := cloneBuildPlanTx(ctx, tx, environmentID, current, next, now, moved); err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO deploy_runtime_plans(environment_id, revision, config_json, preview, digest, created_at)
		 SELECT environment_id, ?, config_json, preview, digest, ?
		   FROM deploy_runtime_plans WHERE environment_id = ? AND revision = ?`, next, now, environmentID, current)
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, fmt.Errorf("%w: revision %d has incomplete plan rows", ErrInvalidPlan, current)
	}
	result, err = tx.ExecContext(ctx, `
		UPDATE deploy_environments SET desired_revision = ?, updated_at = ?
		 WHERE id = ? AND project_id = ? AND desired_revision = ? AND archived_at = 0`,
		next, now, environmentID, projectID, current)
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, ErrRevisionConflict
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.EnvironmentConfiguration(ctx, projectID, environmentID)
}

// cloneBuildPlanTx copies the build plan at one revision to the next. When
// the source has moved, a recorded framework is dropped on the way, since
// detection named the code that used to be there. The digest is copied
// either way: buildPlanDigest leaves the framework out.
func cloneBuildPlanTx(ctx context.Context, tx *sql.Tx, environmentID int64, from, to int, now int64, dropFramework bool) error {
	var method BuildMethod
	var buildJSON, evidence, preview, digest string
	if err := tx.QueryRowContext(ctx, `
		SELECT method, config_json, evidence_json, preview, digest
		  FROM deploy_build_plans WHERE environment_id = ? AND revision = ?`, environmentID, from).
		Scan(&method, &buildJSON, &evidence, &preview, &digest); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: revision %d has incomplete plan rows", ErrInvalidPlan, from)
		}
		return err
	}
	var build BuildPlanConfig
	if dropFramework && json.Unmarshal([]byte(buildJSON), &build) == nil && build.Framework != "" {
		build.Framework = ""
		encoded, _ := json.Marshal(build)
		buildJSON, preview = string(encoded), renderBuildPreview(build)
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO deploy_build_plans(environment_id, revision, method, config_json, evidence_json, preview, digest, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)`, environmentID, to, method, buildJSON, evidence, preview, digest, now)
	return err
}

type digestQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func variableDigestMap(ctx context.Context, query digestQuery, statement string, args ...any) (map[string]string, error) {
	rows, err := query.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]string{}
	for rows.Next() {
		var name, digest string
		if err := rows.Scan(&name, &digest); err != nil {
			return nil, err
		}
		result[name] = digest
	}
	return result, rows.Err()
}

func diffNamedDigests(kind string, before, after map[string]string) []PendingChange {
	names := make([]string, 0, len(before)+len(after))
	seen := map[string]bool{}
	for name := range before {
		seen[name] = true
		names = append(names, name)
	}
	for name := range after {
		if !seen[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	changes := []PendingChange{}
	for _, name := range names {
		oldDigest, oldOK := before[name]
		newDigest, newOK := after[name]
		if oldOK && newOK && oldDigest == newDigest {
			continue
		}
		change := "changed"
		if !oldOK {
			change = "added"
		} else if !newOK {
			change = "removed"
		}
		changes = append(changes, PendingChange{
			Kind: kind, Name: name, Change: change, BeforeDigest: oldDigest, AfterDigest: newDigest,
		})
	}
	return changes
}
