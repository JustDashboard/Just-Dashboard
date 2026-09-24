package deploy

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	basestore "github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

const draftTTL = 30 * 24 * time.Hour

type DraftSaveRequest struct {
	Revision      int                `json:"revision"`
	Step          DraftStep          `json:"step"`
	Intent        *DraftIntentConfig `json:"intent,omitempty"`
	Source        *DraftSourceConfig `json:"source,omitempty"`
	Configuration *PlanConfiguration `json:"configuration,omitempty"`
	// Omitted retains staged inputs; an explicit empty document clears them.
	Dotenv *string `json:"dotenv,omitempty"`
	// Retained names let a resumed form keep masked values it cannot read back.
	RetainEnvironmentKeys []string `json:"retainEnvironmentKeys,omitempty"`
}

type DraftCommitRequest struct {
	Revision             int      `json:"revision"`
	AcknowledgedWarnings []string `json:"acknowledgedWarnings"`
	// GitPolicy is the automatic-deployment decision, taken while the project
	// is being created rather than after. A Git deployment polls its branch
	// from the moment it exists and the default is to deploy every push, so
	// "manual only" used to be a setting you could reach only once an
	// unintended release had already gone out.
	GitPolicy *GitDeploymentPolicy `json:"gitPolicy,omitempty"`
}

type DraftCommitResult struct {
	ProjectID     int64 `json:"projectId"`
	EnvironmentID int64 `json:"environmentId"`
	PlanRevision  int   `json:"planRevision"`
	Created       bool  `json:"created"`
}

type PlanningStore struct {
	db          *sql.DB
	sealer      *auth.Sealer
	managedRoot string
	now         func() time.Time
	mu          sync.Mutex
	databaseURL func(context.Context, int64, int, string) (string, error)
	// installationTokens mints GitHub App credentials; nil until an App is
	// connected.
	installationTokens InstallationTokenMinter
}

func (s *PlanningStore) WithDatabaseURLResolver(resolve func(context.Context, int64, int, string) (string, error)) *PlanningStore {
	s.databaseURL = resolve
	return s
}

func NewPlanningStore(st *basestore.Store, sealer *auth.Sealer, deployRoots []string) *PlanningStore {
	root := "/"
	for _, candidate := range deployRoots {
		if candidate = strings.TrimSpace(candidate); candidate != "" {
			root = filepath.Clean(candidate)
			break
		}
	}
	return &PlanningStore{db: st.DB, sealer: sealer, managedRoot: root, now: time.Now}
}

func (s *PlanningStore) OpenCredential(ctx context.Context, id int64) (CredentialMaterial, error) {
	var kind, config, sealed string
	if err := s.db.QueryRowContext(ctx, `
		SELECT kind, config_json, secret_enc FROM deploy_credentials WHERE id = ?`, id).
		Scan(&kind, &config, &sealed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CredentialMaterial{}, fmt.Errorf("%w: credential not found", ErrSourceUnavailable)
		}
		return CredentialMaterial{}, err
	}
	secret, err := s.sealer.Open(sealed)
	if err != nil {
		return CredentialMaterial{}, fmt.Errorf("%w: credential cannot be decrypted", ErrSourceUnavailable)
	}
	if !json.Valid([]byte(config)) {
		return CredentialMaterial{}, fmt.Errorf("%w: credential configuration is malformed", ErrSourceUnavailable)
	}
	if kind == CredentialGitHubApp {
		return s.openGitHubAppCredential(ctx, json.RawMessage(config))
	}
	return CredentialMaterial{Kind: kind, Config: json.RawMessage(config), Secret: secret}, nil
}

type ScopedVariableValue struct {
	Name        string
	Sensitivity string
	Scopes      []string
	Value       string
	ValueDigest string
}

// OpenScopedVariables is an execution-only secret boundary. Callers name one
// closed scope and receive only active values in that scope; release metadata
// uses ValueDigest and never Value. Ordinary deployment reads do not call it.
func (s *PlanningStore) OpenScopedVariables(
	ctx context.Context,
	environmentID int64,
	scope string,
) ([]ScopedVariableValue, error) {
	if scope != "build" && scope != "runtime" && scope != "release_task" {
		return nil, fmt.Errorf("invalid deployment variable scope %q", scope)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT key, sensitivity, scopes, value_enc, value_digest
		  FROM deploy_variable_revisions
		 WHERE environment_id = ? AND active = 1
		 ORDER BY key`, environmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	all := []ScopedVariableValue{}
	for rows.Next() {
		var value ScopedVariableValue
		var scopes, sealed string
		if err := rows.Scan(&value.Name, &value.Sensitivity, &scopes, &sealed, &value.ValueDigest); err != nil {
			return nil, err
		}
		value.Scopes = strings.Split(scopes, ",")
		value.Value, err = s.sealer.Open(sealed)
		if err != nil {
			return nil, fmt.Errorf("deployment variable %s cannot be decrypted", value.Name)
		}
		all = append(all, value)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var revision int
	if err := s.db.QueryRowContext(ctx, `
		SELECT desired_revision FROM deploy_environments
		 WHERE id = ? AND archived_at = 0`, environmentID).Scan(&revision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrEnvironmentNotFound
		}
		return nil, err
	}
	return resolveScopedVariableValues(all, scope, func(reference VariableReference) (string, bool, error) {
		return s.resolveExternalVariableReference(ctx, environmentID, revision, 0, reference)
	})
}

// OpenRunScopedVariables opens only the exact immutable revisions captured by
// Enqueue. It intentionally validates the secret-free snapshot digest before
// decrypting any selected value, so a rotation during a run cannot change the
// build or release-task input and a damaged reference set fails closed.
func (s *PlanningStore) OpenRunScopedVariables(
	ctx context.Context,
	runID, environmentID int64,
	scope string,
) ([]ScopedVariableValue, error) {
	if scope != "build" && scope != "runtime" && scope != "release_task" {
		return nil, fmt.Errorf("invalid deployment variable scope %q", scope)
	}
	var snapshotEnvironmentID int64
	var planRevision int
	var expectedDigest string
	if err := s.db.QueryRowContext(ctx, `
		SELECT snapshot.environment_id, snapshot.digest, run.plan_revision
		  FROM deploy_run_variable_snapshots snapshot
		  JOIN deploy_runs run ON run.id = snapshot.run_id
		 WHERE snapshot.run_id = ?`, runID).Scan(&snapshotEnvironmentID, &expectedDigest, &planRevision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: run has no variable snapshot", ErrInvalidPlan)
		}
		return nil, err
	}
	if snapshotEnvironmentID != environmentID {
		return nil, fmt.Errorf("%w: run variable snapshot belongs to another environment", ErrInvalidPlan)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT v.key, v.sensitivity, v.scopes, v.value_enc, v.value_digest
		  FROM deploy_run_variable_revisions rv
		  JOIN deploy_variable_revisions v ON v.id = rv.variable_revision_id
		 WHERE rv.run_id = ? AND v.environment_id = ?
		 ORDER BY rv.ordinal`, runID, environmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	all := []ScopedVariableValue{}
	snapshot := []ReleaseVariableSnapshot{}
	seen := map[string]bool{}
	sealedValues := map[string]string{}
	for rows.Next() {
		var value ScopedVariableValue
		var scopes, sealed string
		if err := rows.Scan(&value.Name, &value.Sensitivity, &scopes, &sealed, &value.ValueDigest); err != nil {
			return nil, err
		}
		if seen[value.Name] || !contentDigestRE.MatchString(value.ValueDigest) {
			return nil, fmt.Errorf("%w: run variable snapshot has invalid revision metadata", ErrInvalidPlan)
		}
		seen[value.Name] = true
		value.Scopes = strings.Split(scopes, ",")
		snapshot = append(snapshot, ReleaseVariableSnapshot{
			Name: value.Name, Sensitivity: value.Sensitivity,
			Scopes: scopes, ValueDigest: value.ValueDigest,
		})
		sealedValues[value.Name] = sealed
		all = append(all, value)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if digestReleaseVariables(snapshot) != expectedDigest {
		return nil, fmt.Errorf("%w: run variable snapshot digest is inconsistent", ErrInvalidPlan)
	}
	for index := range all {
		all[index].Value, err = s.sealer.Open(sealedValues[all[index].Name])
		if err != nil {
			return nil, fmt.Errorf("deployment variable %s cannot be decrypted", all[index].Name)
		}
	}
	return resolveScopedVariableValues(all, scope, func(reference VariableReference) (string, bool, error) {
		return s.resolveExternalVariableReference(ctx, environmentID, planRevision, runID, reference)
	})
}

func resolveScopedVariableValues(
	all []ScopedVariableValue,
	scope string,
	external func(VariableReference) (string, bool, error),
) ([]ScopedVariableValue, error) {
	values := make(map[string]string, len(all))
	for _, value := range all {
		values[value.Name] = value.Value
	}
	resolved, secretLeaves, err := ResolveVariableGraph(values, external)
	if err != nil {
		return nil, err
	}
	result := make([]ScopedVariableValue, 0, len(all))
	for _, value := range all {
		included := false
		for _, candidate := range value.Scopes {
			if strings.TrimSpace(candidate) == scope {
				included = true
				break
			}
		}
		if !included {
			continue
		}
		value.Value = resolved[value.Name]
		if secretLeaves[value.Name] {
			value.Sensitivity = "secret"
		}
		result = append(result, value)
	}
	return result, nil
}

// resolveExternalVariableReference is the execution-side half of the closed
// reference parser. Reference identities remain in immutable variable
// revisions and release metadata; only this secret boundary opens the current
// feature-owner value. A missing or ambiguous owner fails the run instead of
// passing a literal ${{...}} token to the workload.
func (s *PlanningStore) resolveExternalVariableReference(
	ctx context.Context,
	environmentID int64,
	planRevision int,
	runID int64,
	reference VariableReference,
) (string, bool, error) {
	switch reference.Kind {
	case "credential":
		sealed, err := s.referenceSealedValue(ctx, "deploy_credentials", "secret_enc", reference.Target)
		if err != nil {
			return "", false, err
		}
		value, err := s.sealer.Open(sealed)
		if err != nil {
			return "", false, fmt.Errorf("%w: credential reference cannot be opened", ErrInvalidVariable)
		}
		return value, true, nil
	case "database":
		if s.databaseURL != nil {
			value, err := s.databaseURL(ctx, environmentID, planRevision, reference.Target)
			return value, true, err
		}
		sealed, err := s.referenceSealedValue(ctx, "db_connections", "dsn_enc", reference.Target)
		if err != nil && strings.HasSuffix(reference.Target, ".url") {
			sealed, err = s.referenceSealedValue(ctx, "db_connections", "dsn_enc", strings.TrimSuffix(reference.Target, ".url"))
		}
		if err != nil {
			return "", false, err
		}
		value, err := s.sealer.Open(sealed)
		if err != nil {
			return "", false, fmt.Errorf("%w: database reference cannot be opened", ErrInvalidVariable)
		}
		return value, true, nil
	case "domain":
		dependencies, err := s.variableReferenceDependencies(ctx, environmentID, runID)
		if err != nil {
			return "", false, err
		}
		for _, dependency := range dependencies {
			if dependency.Kind != "domain" || !strings.EqualFold(dependency.ResourceID, reference.Target) {
				continue
			}
			var config struct {
				Hostname string `json:"hostname"`
				HTTPS    bool   `json:"https"`
			}
			if json.Unmarshal(dependency.Config, &config) != nil || !strings.EqualFold(config.Hostname, reference.Target) {
				return "", false, fmt.Errorf("%w: domain reference is malformed", ErrInvalidVariable)
			}
			scheme := "http"
			if config.HTTPS {
				scheme = "https"
			}
			return scheme + "://" + strings.ToLower(config.Hostname), false, nil
		}
		return "", false, fmt.Errorf("%w: domain reference target was not found", ErrInvalidVariable)
	case "service":
		return s.resolveServiceVariableReference(ctx, environmentID, planRevision, reference.Target)
	default:
		return "", false, fmt.Errorf("%w: unsupported external reference kind", ErrInvalidVariable)
	}
}

// referenceSealedValue deliberately accepts either the stable numeric id or
// the unique operator-facing name. Table and column are selected only by the
// closed callers above; reference text is always a bound value.
func (s *PlanningStore) referenceSealedValue(
	ctx context.Context,
	table, column, target string,
) (string, error) {
	query := `SELECT ` + column + ` FROM ` + table + ` WHERE name = ?`
	argument := any(target)
	if id, err := strconv.ParseInt(target, 10, 64); err == nil && id > 0 {
		query = `SELECT ` + column + ` FROM ` + table + ` WHERE id = ?`
		argument = id
	}
	var sealed string
	if err := s.db.QueryRowContext(ctx, query, argument).Scan(&sealed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("%w: reference target was not found", ErrInvalidVariable)
		}
		return "", err
	}
	return sealed, nil
}

func (s *PlanningStore) variableReferenceDependencies(
	ctx context.Context,
	environmentID, runID int64,
) ([]PlannedDependency, error) {
	if runID != 0 {
		var encoded string
		if err := s.db.QueryRowContext(ctx, `
			SELECT dependencies_json FROM deploy_run_plan_snapshots
			 WHERE run_id = ? AND environment_id = ?`, runID, environmentID).Scan(&encoded); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("%w: run dependency snapshot is unavailable", ErrInvalidPlan)
			}
			return nil, err
		}
		var dependencies []PlannedDependency
		if json.Unmarshal([]byte(encoded), &dependencies) != nil {
			return nil, fmt.Errorf("%w: run dependency snapshot is malformed", ErrInvalidPlan)
		}
		return dependencies, nil
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT kind, ownership, resource_kind, resource_id, config_json
		  FROM deploy_dependencies
		 WHERE environment_id = ? AND release_id = 0
		 ORDER BY kind, resource_kind, resource_id`, environmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	dependencies := []PlannedDependency{}
	for rows.Next() {
		var dependency PlannedDependency
		var config string
		if err := rows.Scan(
			&dependency.Kind,
			&dependency.Ownership,
			&dependency.ResourceKind,
			&dependency.ResourceID,
			&config,
		); err != nil {
			return nil, err
		}
		dependency.Config = json.RawMessage(config)
		dependencies = append(dependencies, dependency)
	}
	return dependencies, rows.Err()
}

func (s *PlanningStore) resolveServiceVariableReference(
	ctx context.Context,
	environmentID int64,
	planRevision int,
	target string,
) (string, bool, error) {
	var encoded string
	if err := s.db.QueryRowContext(ctx, `
		SELECT evidence_json FROM deploy_build_plans
		 WHERE environment_id = ? AND revision = ?`, environmentID, planRevision).Scan(&encoded); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, fmt.Errorf("%w: service reference plan was not found", ErrInvalidPlan)
		}
		return "", false, err
	}
	var evidence StoredBuildEvidence
	if json.Unmarshal([]byte(encoded), &evidence) != nil || evidence.Compose == nil {
		return "", false, fmt.Errorf("%w: service references require a Compose service", ErrInvalidVariable)
	}

	serviceName, field := target, "host"
	service := composeServiceByName(evidence.Compose.Services, serviceName)
	if service == nil {
		for _, suffix := range []string{".host", ".port"} {
			if strings.HasSuffix(target, suffix) {
				serviceName, field = strings.TrimSuffix(target, suffix), strings.TrimPrefix(suffix, ".")
				service = composeServiceByName(evidence.Compose.Services, serviceName)
				break
			}
		}
	}
	if service == nil {
		return "", false, fmt.Errorf("%w: service reference target was not found", ErrInvalidVariable)
	}
	if field == "host" {
		return service.Name, false, nil
	}
	ports := map[int]bool{}
	for _, value := range service.Ports {
		_, port := composePort(value)
		if port > 0 {
			ports[port] = true
		}
	}
	if len(ports) != 1 {
		return "", false, fmt.Errorf("%w: service port reference is missing or ambiguous", ErrInvalidVariable)
	}
	for port := range ports {
		return strconv.Itoa(port), false, nil
	}
	panic("unreachable")
}

func composeServiceByName(services []ComposeServicePlan, name string) *ComposeServicePlan {
	for index := range services {
		if services[index].Name == name {
			return &services[index]
		}
	}
	return nil
}

func (s *PlanningStore) Create(ctx context.Context, ownerUserID int64, ownerUsername string) (*Draft, error) {
	if ownerUserID <= 0 || strings.TrimSpace(ownerUsername) == "" {
		return nil, fmt.Errorf("deployment draft owner is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	draft := &Draft{
		ID: auth.RandomToken(18), OwnerUserID: ownerUserID, OwnerUsername: ownerUsername,
		CurrentStep: DraftIntent, Revision: 1, Data: DraftData{},
		Findings: []PreflightFinding{}, CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(draftTTL),
	}
	data, _ := json.Marshal(draft.Data)
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO deploy_drafts(
		  id, owner_user_id, owner_username, current_step, revision, data_json,
		  findings_json, plan_preview, created_at, updated_at, expires_at)
		VALUES(?, ?, ?, ?, ?, ?, '[]', '', ?, ?, ?)`,
		draft.ID, draft.OwnerUserID, draft.OwnerUsername, draft.CurrentStep, draft.Revision,
		string(data), now.Unix(), now.Unix(), draft.ExpiresAt.Unix()); err != nil {
		return nil, err
	}
	return draft, nil
}

func (s *PlanningStore) Get(ctx context.Context, id string) (*Draft, error) {
	draft, err := s.scanDraft(s.db.QueryRowContext(ctx, `
		SELECT id, owner_user_id, owner_username, current_step, revision, data_json,
		       findings_json, plan_preview, committed_project_id, created_at, updated_at, expires_at, environment_enc
		  FROM deploy_drafts WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDraftNotFound
	}
	if err != nil {
		return nil, err
	}
	if draft.ExpiresAt.Before(s.now().UTC()) && draft.CommittedProjectID == 0 {
		return nil, ErrDraftExpired
	}
	return draft, nil
}

// Discard removes an unfinished setup the caller owns.
//
// Every press of Import creates a draft, and abandoning one at the source or
// configure step left it in the unfinished-setups list for its whole 30-day
// life with no way to clear it: three attempts at the same repository read as
// three pieces of unfinished work. A committed draft is never discarded — it
// is the record that a project came from this plan.
func (s *PlanningStore) Discard(ctx context.Context, id string, userID int64, admin bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	draft, err := s.getUnlocked(ctx, id)
	if err != nil {
		return err
	}
	if err := AuthorizeDraft(draft, userID, admin); err != nil {
		return err
	}
	if draft.CommittedProjectID != 0 {
		return fmt.Errorf("%w: this setup already created a project", ErrDraftCommitted)
	}
	_, err = s.db.ExecContext(ctx,
		`DELETE FROM deploy_drafts WHERE id = ? AND committed_project_id = 0`, id)
	return err
}

// ListDrafts returns the caller's own uncommitted, unexpired drafts, newest
// first, so the new-project page can offer to resume one instead of starting
// the wizard over. A committed or expired draft has nothing left to resume.
func (s *PlanningStore) ListDrafts(ctx context.Context, ownerUserID int64) ([]DraftSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, data_json, current_step, updated_at, expires_at
		  FROM deploy_drafts
		 WHERE owner_user_id = ? AND committed_project_id = 0 AND expires_at > ?
		 ORDER BY updated_at DESC, rowid DESC`, ownerUserID, s.now().UTC().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DraftSummary{}
	for rows.Next() {
		var id, data string
		var step DraftStep
		var updated, expires int64
		if err := rows.Scan(&id, &data, &step, &updated, &expires); err != nil {
			return nil, err
		}
		var parsed DraftData
		if json.Unmarshal([]byte(data), &parsed) != nil {
			continue
		}
		summary := DraftSummary{
			ID: id, CurrentStep: step, Source: draftSourceSummary(parsed.Source),
			UpdatedAt: time.Unix(updated, 0).UTC(), ExpiresAt: time.Unix(expires, 0).UTC(),
		}
		if parsed.Intent != nil {
			summary.Name = parsed.Intent.Name
		}
		out = append(out, summary)
	}
	return out, rows.Err()
}

func (s *PlanningStore) scanDraft(row interface{ Scan(...any) error }) (*Draft, error) {
	var draft Draft
	var data, findings string
	var created, updated, expires int64
	if err := row.Scan(&draft.ID, &draft.OwnerUserID, &draft.OwnerUsername, &draft.CurrentStep,
		&draft.Revision, &data, &findings, &draft.PlanPreview, &draft.CommittedProjectID,
		&created, &updated, &expires, &draft.environmentEnc); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(data), &draft.Data); err != nil {
		return nil, fmt.Errorf("decode deployment draft: %w", err)
	}
	if err := json.Unmarshal([]byte(findings), &draft.Findings); err != nil {
		return nil, fmt.Errorf("decode deployment draft findings: %w", err)
	}
	if draft.Findings == nil {
		draft.Findings = []PreflightFinding{}
	}
	draft.CreatedAt = time.Unix(created, 0).UTC()
	draft.UpdatedAt = time.Unix(updated, 0).UTC()
	draft.ExpiresAt = time.Unix(expires, 0).UTC()
	if draft.environmentEnc != "" {
		plaintext, err := s.sealer.Open(draft.environmentEnc)
		if err != nil {
			return nil, fmt.Errorf("deployment draft environment cannot be decrypted")
		}
		if err := json.Unmarshal([]byte(plaintext), &draft.environment); err != nil {
			return nil, fmt.Errorf("deployment draft environment is malformed")
		}
	}
	draft.refreshEnvironmentKeys()
	return &draft, nil
}

func AuthorizeDraft(draft *Draft, userID int64, admin bool) error {
	if draft.OwnerUserID != userID && !admin {
		return ErrDraftForbidden
	}
	return nil
}

func (s *PlanningStore) Save(
	ctx context.Context,
	id string,
	userID int64,
	admin bool,
	request DraftSaveRequest,
) (*Draft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	draft, err := s.getUnlocked(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := AuthorizeDraft(draft, userID, admin); err != nil {
		return nil, err
	}
	if draft.CommittedProjectID != 0 {
		return nil, ErrDraftCommitted
	}
	if request.Revision != draft.Revision {
		return nil, fmt.Errorf("%w: current revision is %d", ErrDraftRevision, draft.Revision)
	}
	if _, ok := draftSteps[request.Step]; !ok || request.Step == DraftDetection || request.Step == DraftPreflight {
		return nil, fmt.Errorf("%w: invalid client-saved draft step", ErrInvalidPlan)
	}
	if len(request.RetainEnvironmentKeys) > 0 && (request.Step != DraftConfiguration || request.Dotenv == nil) {
		return nil, fmt.Errorf("%w: retained environment keys require a configuration and dotenv document", ErrInvalidPlan)
	}
	switch request.Step {
	case DraftIntent:
		if request.Intent == nil || request.Source != nil || request.Configuration != nil || request.Dotenv != nil {
			return nil, fmt.Errorf("%w: intent step requires only intent data", ErrInvalidPlan)
		}
		if err := request.Intent.Validate(); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidPlan, err)
		}
		copy := *request.Intent
		draft.Data.Intent = &copy
	case DraftSource:
		if request.Source == nil || request.Intent != nil || request.Configuration != nil || request.Dotenv != nil {
			return nil, fmt.Errorf("%w: source step requires only source data", ErrInvalidPlan)
		}
		copy := canonicalSourceConfig(*request.Source)
		if err := copy.validateForNewDeployment(); err != nil {
			return nil, err
		}
		if exists, err := s.credentialExists(ctx, copy.CredentialID); err != nil {
			return nil, err
		} else if !exists {
			return nil, fmt.Errorf("%w: credential does not exist", ErrInvalidSource)
		}
		keepEnvironment := false
		if draft.Data.Source != nil {
			previous := *draft.Data.Source
			// Reinspect another branch of this repository without losing values
			// that a resumed form correctly cannot read back from the server.
			if previous.Kind == SourceGit {
				previous.Ref = copy.Ref
			}
			keepEnvironment = bytes.Equal(mustJSON(previous), mustJSON(copy))
		}
		draft.Data.Source = &copy
		draft.Data.Detection = nil
		if !keepEnvironment {
			draft.environment, draft.environmentEnc = nil, ""
			draft.refreshEnvironmentKeys()
		}
	case DraftConfiguration:
		if request.Configuration == nil || request.Intent != nil || request.Source != nil {
			return nil, fmt.Errorf("%w: configuration step requires only configuration data", ErrInvalidPlan)
		}
		copy := canonicalConfiguration(*request.Configuration)
		// Detection names the framework at commit; the browser does not.
		copy.Build.Framework = ""
		if request.Dotenv != nil {
			values, err := ParseDotenv(*request.Dotenv)
			if err != nil {
				return nil, err
			}
			for _, name := range request.RetainEnvironmentKeys {
				value, exists := draft.environment[name]
				if !exists {
					return nil, fmt.Errorf("%w: retained environment variable %q does not exist", ErrInvalidVariable, name)
				}
				if _, supplied := values[name]; !supplied {
					values[name] = value
				}
			}
			draft.environment = values
			draft.environmentEnc = ""
			if len(values) > 0 {
				plaintext, _ := json.Marshal(values)
				draft.environmentEnc, err = s.sealer.Seal(string(plaintext))
				if err != nil {
					return nil, err
				}
			}
			draft.refreshEnvironmentKeys()
		}
		copy = draft.withEnvironmentMetadata(copy)
		if err := sealDomainProtection(&copy); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidPlan, err)
		}
		if err := copy.Validate(); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidPlan, err)
		}
		if _, _, err := ResolveVariableGraph(draft.variableValues(copy), nil); err != nil {
			return nil, err
		}
		draft.Data.Configuration = &copy
	}
	draft.CurrentStep = request.Step
	draft.Revision++
	draft.Findings = []PreflightFinding{}
	draft.PlanPreview = ""
	return s.persistDraft(ctx, draft)
}

func (s *PlanningStore) SaveDetection(
	ctx context.Context,
	id string,
	userID int64,
	admin bool,
	revision int,
	detection DetectionResult,
) (*Draft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	draft, err := s.getUnlocked(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := AuthorizeDraft(draft, userID, admin); err != nil {
		return nil, err
	}
	if draft.CommittedProjectID != 0 {
		return nil, ErrDraftCommitted
	}
	if revision != draft.Revision {
		return nil, fmt.Errorf("%w: current revision is %d", ErrDraftRevision, draft.Revision)
	}
	if err := validateDetectionResult(draft.Data.Source, detection); err != nil {
		return nil, err
	}
	draft.Data.Detection = &detection
	if draft.Data.Source.Mode == SourceModeBlueprint {
		// The reviewed definition, not the browser, decides what a blueprint
		// deployment looks like: image, volumes, checks, limits and variables
		// come from the render and are offered to the operator to adjust.
		name := ""
		if draft.Data.Intent != nil {
			name = draft.Data.Intent.Name
		}
		plan, err := RenderBlueprintPlan(*draft.Data.Source, name)
		if err != nil {
			return nil, err
		}
		configuration := draft.withEnvironmentMetadata(canonicalConfiguration(plan.Configuration))
		draft.Data.Configuration = &configuration
	}
	draft.CurrentStep = DraftDetection
	draft.Revision++
	draft.Findings = []PreflightFinding{}
	draft.PlanPreview = ""
	return s.persistDraft(ctx, draft)
}

func (s *PlanningStore) SavePreflight(
	ctx context.Context,
	id string,
	userID int64,
	admin bool,
	revision int,
	result *PreflightResult,
) (*Draft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	draft, err := s.getUnlocked(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := AuthorizeDraft(draft, userID, admin); err != nil {
		return nil, err
	}
	if draft.CommittedProjectID != 0 {
		return nil, ErrDraftCommitted
	}
	if revision != draft.Revision {
		return nil, fmt.Errorf("%w: current revision is %d", ErrDraftRevision, draft.Revision)
	}
	if result == nil || result.Revision != revision || result.Plan.DraftRevision != revision ||
		len(result.Preview) > 8<<20 || result.Digest != digestBytes([]byte(result.Preview)) {
		return nil, fmt.Errorf("%w: preflight result does not match the draft revision", ErrInvalidPlan)
	}
	expectedPlan := exactPlan(draft, canonicalConfiguration(*draft.Data.Configuration))
	expectedPreview, err := json.MarshalIndent(expectedPlan, "", "  ")
	if err != nil || !bytes.Equal(expectedPreview, []byte(result.Preview)) {
		return nil, fmt.Errorf("%w: preflight action plan does not match the saved draft", ErrInvalidPlan)
	}
	if err := validatePreflightFindings(result.Findings); err != nil {
		return nil, err
	}
	draft.CurrentStep = DraftPreflight
	draft.Revision++
	draft.Findings = append([]PreflightFinding(nil), result.Findings...)
	draft.PlanPreview = result.Preview
	return s.persistDraft(ctx, draft)
}

func validatePreflightFindings(findings []PreflightFinding) error {
	if len(findings) > 1024 {
		return fmt.Errorf("%w: preflight has too many findings", ErrInvalidPlan)
	}
	for _, finding := range findings {
		validSeverity := finding.Severity == PreflightPass || finding.Severity == PreflightDecision ||
			finding.Severity == PreflightWarning || finding.Severity == PreflightBlocked ||
			finding.Severity == PreflightUnavailable
		if !validSeverity || finding.Code == "" || len(finding.Code) > 128 || finding.Title == "" ||
			len(finding.Title) > 512 || len(finding.Measured) > 4096 || len(finding.Means) > 4096 ||
			len(finding.Action) > 4096 || len(finding.Owner) > 128 || len(finding.FieldID) > 512 ||
			len(finding.DeepLink) > 2048 || strings.ContainsAny(finding.Code+finding.Owner, "\x00\r\n") {
			return fmt.Errorf("%w: preflight finding is malformed", ErrInvalidPlan)
		}
		for _, value := range []string{finding.Title, finding.Measured, finding.Means, finding.Action} {
			if rejectPlanSecretLiteral("preflight finding", value) != nil {
				return fmt.Errorf("%w: preflight finding contains credential material", ErrInvalidPlan)
			}
		}
	}
	return nil
}

func (s *PlanningStore) persistDraft(ctx context.Context, draft *Draft) (*Draft, error) {
	draft.UpdatedAt = s.now().UTC()
	data, err := json.Marshal(draft.Data)
	if err != nil {
		return nil, err
	}
	findings, err := json.Marshal(draft.Findings)
	if err != nil {
		return nil, err
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE deploy_drafts
		   SET current_step = ?, revision = ?, data_json = ?, findings_json = ?,
		       plan_preview = ?, updated_at = ?, environment_enc = ?
		 WHERE id = ? AND revision = ? AND committed_project_id = 0`,
		draft.CurrentStep, draft.Revision, string(data), string(findings), draft.PlanPreview,
		draft.UpdatedAt.Unix(), draft.environmentEnc, draft.ID, draft.Revision-1)
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, ErrDraftRevision
	}
	return draft, nil
}

func (s *PlanningStore) getUnlocked(ctx context.Context, id string) (*Draft, error) {
	draft, err := s.scanDraft(s.db.QueryRowContext(ctx, `
		SELECT id, owner_user_id, owner_username, current_step, revision, data_json,
		       findings_json, plan_preview, committed_project_id, created_at, updated_at, expires_at, environment_enc
		  FROM deploy_drafts WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDraftNotFound
	}
	if err != nil {
		return nil, err
	}
	if draft.ExpiresAt.Before(s.now().UTC()) && draft.CommittedProjectID == 0 {
		return nil, ErrDraftExpired
	}
	return draft, nil
}

func (s *PlanningStore) Commit(
	ctx context.Context,
	id string,
	userID int64,
	admin bool,
	request DraftCommitRequest,
) (*DraftCommitResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	draft, err := s.getUnlocked(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := AuthorizeDraft(draft, userID, admin); err != nil {
		return nil, err
	}
	if draft.CommittedProjectID != 0 {
		var environmentID int64
		err := s.db.QueryRowContext(ctx, `
			SELECT id FROM deploy_environments
			 WHERE project_id = ? AND slug = 'production'`, draft.CommittedProjectID).Scan(&environmentID)
		return &DraftCommitResult{
			ProjectID: draft.CommittedProjectID, EnvironmentID: environmentID,
			PlanRevision: 1, Created: false,
		}, err
	}
	if request.Revision != draft.Revision {
		return nil, fmt.Errorf("%w: current revision is %d", ErrDraftRevision, draft.Revision)
	}
	if err := validateDraftComplete(draft); err != nil {
		return nil, err
	}
	acknowledged := make(map[string]bool, len(request.AcknowledgedWarnings))
	for _, code := range request.AcknowledgedWarnings {
		acknowledged[code] = true
	}
	for _, finding := range draft.Findings {
		switch finding.Severity {
		case PreflightBlocked, PreflightDecision:
			return nil, fmt.Errorf("%w: %s", ErrPreflightBlocked, finding.Code)
		case PreflightWarning:
			if !acknowledged[finding.Code] {
				return nil, fmt.Errorf("%w: warning %s is not acknowledged", ErrPreflightBlocked, finding.Code)
			}
		}
	}

	secret := auth.RandomToken(24)
	sealedHook, err := s.sealer.Seal(secret)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := s.now().UTC()
	repoPath := sourceRepositoryPath(s.managedRoot, draft)
	branch := draft.Data.Source.Ref
	if branch == "" {
		branch = "main"
	}
	composeFile := "compose.yml"
	if len(draft.Data.Source.ComposeFiles) > 0 {
		documents := append([]ComposeDocument(nil), draft.Data.Source.ComposeFiles...)
		sort.Slice(documents, func(i, j int) bool { return documents[i].Order < documents[j].Order })
		composeFile = documents[0].Path
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO deploy_projects(
		  name, profile, repo_path, branch, compose_file, pre_command, post_command,
		  hook_secret, hook_id, enabled, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, '', '', ?, ?, 1, ?, ?)`,
		draft.Data.Intent.Name, draft.Data.Intent.Profile, repoPath, branch, composeFile,
		sealedHook, auth.RandomToken(12), now.Unix(), now.Unix())
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, fmt.Errorf("%w: deployment name already exists", ErrInvalidPlan)
		}
		return nil, err
	}
	projectID, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}
	configuration := canonicalConfiguration(*draft.Data.Configuration)
	configuration.Build.Framework = detectedFramework(draft.Data.Detection, configuration.Build)
	expectedDowntime := 0
	if configuration.Runtime.Strategy == StrategyStopFirst {
		expectedDowntime = 1
	}
	result, err = tx.ExecContext(ctx, `
		INSERT INTO deploy_environments(
		  project_id, name, slug, kind, desired_revision, strategy,
		  expected_downtime, protected, created_at, updated_at)
		VALUES(?, 'production', 'production', ?, 1, ?, ?, 1, ?, ?)`,
		projectID, EnvironmentProduction, configuration.Runtime.Strategy,
		expectedDowntime, now.Unix(), now.Unix())
	if err != nil {
		return nil, err
	}
	environmentID, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}
	if err := commitGitPolicy(ctx, tx, environmentID, request.GitPolicy, now.Unix()); err != nil {
		return nil, err
	}
	sourceJSON, _ := json.Marshal(draft.Data.Source)
	identityJSON, _ := json.Marshal(draft.Data.Detection.Source)
	sourceDigest := digestBytes(sourceJSON, identityJSON)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO deploy_sources(
		  environment_id, revision, kind, config_json, credential_id,
		  identity_json, digest, created_at)
		VALUES(?, 1, ?, ?, ?, ?, ?, ?)`, environmentID, draft.Data.Source.Kind,
		string(sourceJSON), draft.Data.Source.CredentialID, string(identityJSON), sourceDigest, now.Unix()); err != nil {
		return nil, err
	}
	buildJSON, _ := json.Marshal(configuration.Build)
	detectionJSON, _ := json.Marshal(struct {
		Candidates      []DetectedCandidate `json:"candidates"`
		Compose         *ComposeAnalysis    `json:"compose,omitempty"`
		GitRequirements GitRequirements     `json:"gitRequirements"`
	}{
		Candidates:      draft.Data.Detection.Candidates,
		Compose:         cloneComposeAnalysis(draft.Data.Detection.Compose),
		GitRequirements: draft.Data.Detection.GitRequirements,
	})
	buildPreview := renderBuildPreview(configuration.Build)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO deploy_build_plans(
		  environment_id, revision, method, config_json, evidence_json, preview, digest, created_at)
		VALUES(?, 1, ?, ?, ?, ?, ?, ?)`, environmentID, configuration.Build.Method,
		string(buildJSON), string(detectionJSON), buildPreview, buildPlanDigest(configuration.Build), now.Unix()); err != nil {
		return nil, err
	}
	runtimeJSON, _ := json.Marshal(configuration.Runtime)
	runtimePreview := renderRuntimePreview(configuration.Runtime)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO deploy_runtime_plans(
		  environment_id, revision, config_json, preview, digest, created_at)
		VALUES(?, 1, ?, ?, ?, ?)`, environmentID, string(runtimeJSON), runtimePreview,
		digestBytes(runtimeJSON), now.Unix()); err != nil {
		return nil, err
	}
	for _, variable := range configuration.Variables {
		value := variable.Reference
		sensitivity := variable.Sensitivity
		staged, supplied := draft.environment[variable.Name]
		switch {
		case supplied:
			value = staged
			sensitivity = suppliedVariableSensitivity(variable)
		case variable.Generate > 0:
			// Generated here and never anywhere else: the value exists only
			// sealed, revealed on demand through the audited reveal route.
			value, err = generatedSecret(variable.Generate)
			if err != nil {
				return nil, err
			}
		case variable.Value != "":
			value = variable.Value
		}
		sealed, err := s.sealer.Seal(value)
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO deploy_variable_revisions(
			  environment_id, key, revision, sensitivity, scopes, value_enc,
			  value_digest, active, created_by, created_at)
			VALUES(?, ?, 1, ?, ?, ?, ?, 1, ?, ?)`, environmentID, variable.Name,
			sensitivity, strings.Join(variable.Scopes, ","), sealed,
			digestBytes([]byte(value)), draft.OwnerUsername, now.Unix()); err != nil {
			return nil, err
		}
	}
	if err := s.validateActiveVariableGraphTx(ctx, tx, environmentID); err != nil {
		return nil, err
	}
	for _, dependency := range configuration.Dependencies {
		config := dependency.Config
		if len(config) == 0 {
			config = json.RawMessage(`{}`)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO deploy_dependencies(
			  environment_id, release_id, kind, ownership, resource_kind,
			  resource_id, config_json, created_at)
			VALUES(?, 0, ?, ?, ?, ?, ?, ?)`, environmentID, dependency.Kind,
			dependency.Ownership, dependency.ResourceKind, dependency.ResourceID,
			string(config), now.Unix()); err != nil {
			return nil, err
		}
	}
	// Adoption records the runtime edge as observed. It makes the relationship
	// visible without claiming ownership of, relabelling, restarting, or later
	// deleting the pre-existing Docker resource.
	if draft.Data.Source.Kind == SourceImport && draft.Data.Source.ResourceID != "" {
		resourceKind := "docker_container"
		if draft.Data.Source.Mode == SourceModeExistingStack {
			resourceKind = "compose_stack"
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO deploy_dependencies(
			  environment_id, release_id, kind, ownership, resource_kind,
			  resource_id, config_json, created_at)
			VALUES(?, 0, 'runtime', 'observed', ?, ?, '{}', ?)`, environmentID,
			resourceKind, draft.Data.Source.ResourceID, now.Unix()); err != nil {
			return nil, err
		}
	}
	for _, domain := range configuration.Domains {
		config := mustJSON(map[string]any{"hostname": domain.Hostname, "https": domain.HTTPS})
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO deploy_dependencies(
			  environment_id, release_id, kind, ownership, resource_kind,
			  resource_id, config_json, created_at)
			VALUES(?, 0, 'domain', ?, 'proxy_site', ?, ?, ?)`, environmentID,
			domain.Ownership, domain.Hostname, string(config), now.Unix()); err != nil {
			return nil, err
		}
	}
	for ordinal, check := range configuration.Checks {
		config := check.Config
		if len(config) == 0 {
			config = json.RawMessage(`{}`)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO deploy_checks(
			  environment_id, runtime_plan_id, name, kind, phase, config_json,
			  required, ordinal, created_at)
			VALUES(?, 0, ?, ?, ?, ?, ?, ?, ?)`, environmentID, check.Name,
			check.Kind, check.Phase, string(config), boolInt(check.Required), ordinal+1, now.Unix()); err != nil {
			return nil, err
		}
	}
	update, err := tx.ExecContext(ctx, `
		UPDATE deploy_drafts SET committed_project_id = ?, updated_at = ?
		 WHERE id = ? AND revision = ? AND committed_project_id = 0`,
		projectID, now.Unix(), draft.ID, draft.Revision)
	if err != nil {
		return nil, err
	}
	if affected, _ := update.RowsAffected(); affected != 1 {
		return nil, ErrDraftRevision
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &DraftCommitResult{
		ProjectID: projectID, EnvironmentID: environmentID, PlanRevision: 1, Created: true,
	}, nil
}

// generatedSecret produces a URL- and shell-safe credential of exactly the
// requested length from the operating system's random source.
func generatedSecret(length int) (string, error) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	raw := make([]byte, length)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	out := make([]byte, length)
	for i, b := range raw {
		out[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(out), nil
}

func validateDraftComplete(draft *Draft) error {
	if draft.Data.Intent == nil || draft.Data.Source == nil || draft.Data.Detection == nil ||
		draft.Data.Configuration == nil || draft.CurrentStep != DraftPreflight || draft.PlanPreview == "" {
		return ErrDraftIncomplete
	}
	if err := draft.Data.Intent.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidPlan, err)
	}
	if err := draft.Data.Source.validateForNewDeployment(); err != nil {
		return err
	}
	if err := draft.Data.Configuration.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidPlan, err)
	}
	values, _, err := ResolveVariableGraph(draft.variableValues(*draft.Data.Configuration), nil)
	if err != nil {
		return err
	}
	for _, variable := range draft.Data.Configuration.Variables {
		if variable.Required && values[variable.Name] == "" {
			return fmt.Errorf("%w: required variable %s has no value", ErrPreflightBlocked, variable.Name)
		}
	}
	return nil
}

func sourceRepositoryPath(root string, draft *Draft) string {
	source := draft.Data.Source
	if source.LocalPath != "" {
		return filepath.Clean(source.LocalPath)
	}
	return filepath.Join(root, ".just-dashboard", "deployments", draft.ID)
}

func digestBytes(parts ...[]byte) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write(part)
		_, _ = h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func renderBuildPreview(build BuildPlanConfig) string {
	preview, _ := json.MarshalIndent(build, "", "  ")
	return string(preview)
}

func renderRuntimePreview(runtime RuntimePlanConfig) string {
	preview, _ := json.MarshalIndent(runtime, "", "  ")
	return string(preview)
}
