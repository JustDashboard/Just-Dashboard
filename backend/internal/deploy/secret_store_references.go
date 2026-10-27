package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/secretstore"
)

// A store reference reads a value an external secret manager holds —
// Vault, OpenBao, Infisical or Doppler — through a connection saved under
// Deployments → Secret stores: ${{store.NAME/path#KEY}}, or with a version
// pinned, ${{store.NAME/path#KEY@4}}.
//
// It resolves the way every other reference does, at the variable boundary
// a run opens, and is read fresh for each run: the release keeps the
// reference, never the value, so a rotation reaches the next deploy, and a
// rollback or redeploy reads the store as it is at that moment. That is the
// rule a rotated credential needs (the old one has usually been revoked by
// the time anybody rolls back), and it is the rule shared variables and
// credentials already follow. A reference that pins a version is frozen in
// the release with its version, so rolling back to that release reads that
// version exactly. Each run records which version it read of each reference
// (`secretStoreReads` in its metadata) — the version, never the value — and a
// rollback's transcript says when the store now holds another version than
// the release it restores read.
//
// A run reads every store it references at its first step, before it
// acquires a source or starts anything, so a store that refuses fails the run
// with nothing touched, in the store's own words. What it read is held in
// memory for that run's later steps — the build, the release tasks and the
// container read one consistent set — and dropped when the run finishes. A
// dashboard restarted in the middle of a run reads the stores again at the
// next step that opens variables.
const StoreReferenceKind = "store"

// storeReferenceRE admits the characters a store reference's target may hold
// beyond the closed reference grammar's — the path's slashes, the key's #
// and a pinned version's @ — for this kind alone; secretstore.ParseReference
// is what decides whether the target is one.
var storeReferenceRE = regexp.MustCompile(`^\$\{\{store\.([^{}\s]{1,300})\}\}$`)

// parseStoreReference reads ${{store.…}} or says the value is not one.
func parseStoreReference(value string) (VariableReference, bool, error) {
	match := storeReferenceRE.FindStringSubmatch(value)
	if match == nil {
		return VariableReference{}, false, nil
	}
	if _, err := secretstore.ParseReference(match[1]); err != nil {
		return VariableReference{}, true, fmt.Errorf("%w: %s", ErrInvalidVariable, storeProblem(err))
	}
	return VariableReference{Kind: StoreReferenceKind, Target: match[1]}, true, nil
}

// storeProblem is a secretstore refusal as a sentence, without the
// sentinel's prefix.
func storeProblem(err error) string {
	return strings.TrimPrefix(err.Error(), secretstore.ErrInvalid.Error()+": ")
}

// SecretStoreResolver is the connections' half of a store reference
// (secretstore.Repository).
type SecretStoreResolver interface {
	Check(ctx context.Context, reference secretstore.Reference, preview bool) error
	Resolve(ctx context.Context, references []secretstore.Reference, options secretstore.ResolveOptions) (map[string]secretstore.Read, error)
}

// WithSecretStores attaches the secret-store connections. Without them a
// store reference cannot be saved, and one already saved fails its run.
func (s *PlanningStore) WithSecretStores(resolver SecretStoreResolver) *PlanningStore {
	s.secretStores = resolver
	return s
}

// storeReadCache holds what each run read from the stores, for the run's own
// later steps. Nothing else reads it and nothing writes it to disk.
type storeReadCache struct {
	mu   sync.Mutex
	runs map[int64]storeRunReads
}

type storeRunReads struct {
	at     time.Time
	values map[string]secretstore.Read
}

// storeReadsKept bounds how long a run's reads are held if its finish is
// never observed — the observer is detached, and a run can outlive it.
const storeReadsKept = 6 * time.Hour

func (c *storeReadCache) get(runID int64) (map[string]secretstore.Read, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.runs[runID]
	if !ok || time.Since(entry.at) > storeReadsKept {
		return nil, false
	}
	return entry.values, true
}

func (c *storeReadCache) put(runID int64, values map[string]secretstore.Read) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.runs == nil {
		c.runs = map[int64]storeRunReads{}
	}
	for id, entry := range c.runs {
		if time.Since(entry.at) > storeReadsKept {
			delete(c.runs, id)
		}
	}
	c.runs[runID] = storeRunReads{at: time.Now(), values: values}
}

func (c *storeReadCache) forget(runID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.runs, runID)
}

// storeReadObserver drops a run's reads once it has finished: the values
// were for that run's steps and nothing after it.
type storeReadObserver struct{ s *PlanningStore }

func (o storeReadObserver) RunStarted(context.Context, EngineRun) {}

func (o storeReadObserver) RunFinished(_ context.Context, run EngineRun) {
	o.s.storeReads.forget(run.ID)
}

// SecretStoreRunObserver is the engine observer that forgets a finished
// run's store reads.
func (s *PlanningStore) SecretStoreRunObserver() RunObserver {
	return storeReadObserver{s: s}
}

// HasRunStoreReads says whether this process already read the stores for a
// run.
func (s *PlanningStore) HasRunStoreReads(runID int64) bool {
	_, ok := s.storeReads.get(runID)
	return ok
}

// StoreRead is one reference a run read, as its transcript and metadata say
// it: which store, where, the version the store reported and the variables
// that hold the reference. Never the value.
type StoreRead struct {
	Reference string   `json:"reference"`
	Store     string   `json:"store"`
	Location  string   `json:"location"`
	Version   string   `json:"version,omitempty"`
	Pinned    bool     `json:"pinned,omitempty"`
	Variables []string `json:"variables"`
}

// storeReferences collects the store references a set of variables holds,
// each with the variables that hold it, in a stable order.
func storeReferences(all []ScopedVariableValue) ([]secretstore.Reference, map[string][]string) {
	holders := map[string][]string{}
	references := []secretstore.Reference{}
	for _, variable := range all {
		reference, ok := parseStoredReference(variable.Value, variable.ValueMode)
		if !ok || reference.Kind != StoreReferenceKind {
			continue
		}
		parsed, err := secretstore.ParseReference(reference.Target)
		if err != nil {
			continue
		}
		if _, seen := holders[reference.Target]; !seen {
			references = append(references, parsed)
		}
		holders[reference.Target] = append(holders[reference.Target], variable.Name)
	}
	sort.Slice(references, func(i, j int) bool { return references[i].String() < references[j].String() })
	return references, holders
}

// environmentIsPreview says whether an environment is a pull request
// preview, which reads only the stores that allow previews.
func (s *PlanningStore) environmentIsPreview(ctx context.Context, environmentID int64) (bool, error) {
	var kind EnvironmentKind
	if err := s.db.QueryRowContext(ctx, `SELECT kind FROM deploy_environments WHERE id = ?`, environmentID).Scan(&kind); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, ErrEnvironmentNotFound
		}
		return false, err
	}
	return kind == EnvironmentPreview, nil
}

// readStores resolves a set of variables' store references, for a run
// (runID non-zero, kept for the run's later steps) or not.
func (s *PlanningStore) readStores(ctx context.Context, runID, environmentID int64, all []ScopedVariableValue, fresh bool) (map[string]secretstore.Read, error) {
	references, _ := storeReferences(all)
	if runID != 0 && !fresh {
		if cached, ok := s.storeReads.get(runID); ok {
			missing := false
			for _, reference := range references {
				if _, ok := cached[reference.String()]; !ok {
					missing = true
					break
				}
			}
			if !missing {
				return cached, nil
			}
		}
	}
	values := map[string]secretstore.Read{}
	if len(references) > 0 {
		if s.secretStores == nil {
			return nil, fmt.Errorf("%w: a variable references a secret store, and secret stores are unavailable", ErrInvalidVariable)
		}
		preview, err := s.environmentIsPreview(ctx, environmentID)
		if err != nil {
			return nil, err
		}
		values, err = s.secretStores.Resolve(ctx, references, secretstore.ResolveOptions{Preview: preview})
		if err != nil {
			return nil, err
		}
	}
	if runID != 0 {
		s.storeReads.put(runID, values)
	}
	return values, nil
}

// storeValue answers a store reference from what was read; the resolver
// already failed the read for anything it could not answer, so a miss here
// means the reads and the variables disagree, and fails closed.
func storeValue(reads map[string]secretstore.Read, reference VariableReference) (string, bool, error) {
	read, ok := reads[reference.Target]
	if !ok {
		return "", false, fmt.Errorf("%w: %s was not read from its store", ErrInvalidVariable, reference.Target)
	}
	return read.Value, true, nil
}

// ResolveRunSecretStores reads, fresh, every store reference the run's
// variable snapshot holds, keeps the values for the run's later steps, and
// says what it read — never a value. It is the run's first step that calls
// it, before anything is acquired or started.
func (s *PlanningStore) ResolveRunSecretStores(ctx context.Context, run EngineRun) ([]StoreRead, error) {
	all, _, err := s.runVariableValues(ctx, run.ID, run.EnvironmentID)
	if err != nil {
		return nil, err
	}
	reads, err := s.readStores(ctx, run.ID, run.EnvironmentID, all, true)
	if err != nil {
		return nil, err
	}
	references, holders := storeReferences(all)
	out := make([]StoreRead, 0, len(references))
	for _, reference := range references {
		target := reference.String()
		out = append(out, StoreRead{
			Reference: target, Store: reference.Store, Location: reference.Location(),
			Version: reads[target].Version, Pinned: reference.Version > 0, Variables: holders[target],
		})
	}
	return out, nil
}

// storeReferenceProblem is the save-time half of a store reference, in a
// sentence that names the variable, or nothing: the variable is secret, the
// store exists and can answer the reference's shape, and a preview's
// variable names only a store that serves previews.
func (s *PlanningStore) storeReferenceProblem(ctx context.Context, preview bool, variable, sensitivity string, reference VariableReference) (string, error) {
	if reference.Kind != StoreReferenceKind {
		return "", nil
	}
	parsed, err := secretstore.ParseReference(reference.Target)
	if err != nil {
		return fmt.Sprintf("%s: %s", variable, storeProblem(err)), nil
	}
	if sensitivity != "secret" {
		return fmt.Sprintf("%s reads secret store %s, so it has to be secret too", variable, parsed.Store), nil
	}
	if s.secretStores == nil {
		return fmt.Sprintf("%s reads secret store %s, and secret stores are unavailable on this server", variable, parsed.Store), nil
	}
	if err := s.secretStores.Check(ctx, parsed, preview); err != nil {
		if errors.Is(err, secretstore.ErrInvalid) {
			return fmt.Sprintf("%s: %s", variable, storeProblem(err)), nil
		}
		return "", err
	}
	return "", nil
}

func (s *PlanningStore) checkStoreReference(ctx context.Context, environmentID int64, variable, sensitivity string, reference VariableReference) error {
	if reference.Kind != StoreReferenceKind {
		return nil
	}
	preview, err := s.environmentIsPreview(ctx, environmentID)
	if err != nil {
		return err
	}
	problem, err := s.storeReferenceProblem(ctx, preview, variable, sensitivity, reference)
	if err != nil {
		return err
	}
	if problem != "" {
		return fmt.Errorf("%w: %s", ErrInvalidVariable, problem)
	}
	return nil
}

// SecretStoreUse is one environment variable that references a store.
type SecretStoreUse struct {
	ProjectID       int64  `json:"projectId"`
	ProjectName     string `json:"projectName"`
	EnvironmentID   int64  `json:"environmentId"`
	EnvironmentName string `json:"environmentName"`
	EnvironmentKind string `json:"environmentKind"`
	Variable        string `json:"variable"`
	Reference       string `json:"reference"`
}

// SecretStoreUses finds, by store name, every active variable of a
// non-archived environment that references a store. References live in the
// sealed values, so only values saved as references are opened, and nothing
// but the reference is kept.
func (s *PlanningStore) SecretStoreUses(ctx context.Context) (map[string][]SecretStoreUse, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.id, p.name, e.id, e.name, e.kind, v.key, v.value_enc
		  FROM deploy_variable_revisions v
		  JOIN deploy_environments e ON e.id = v.environment_id
		  JOIN deploy_projects p ON p.id = e.project_id
		 WHERE v.active = 1 AND v.value_mode = 'reference'
		   AND e.archived_at = 0 AND p.archived_at = 0
		 ORDER BY p.name, e.id, v.key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	uses := map[string][]SecretStoreUse{}
	for rows.Next() {
		var use SecretStoreUse
		var sealed string
		if err := rows.Scan(&use.ProjectID, &use.ProjectName, &use.EnvironmentID, &use.EnvironmentName,
			&use.EnvironmentKind, &use.Variable, &sealed); err != nil {
			return nil, err
		}
		value, err := s.sealer.Open(sealed)
		if err != nil {
			return nil, fmt.Errorf("deployment variable %s cannot be decrypted", use.Variable)
		}
		reference, ok := parseStoredReference(value, "reference")
		if !ok || reference.Kind != StoreReferenceKind {
			continue
		}
		parsed, err := secretstore.ParseReference(reference.Target)
		if err != nil {
			continue
		}
		use.Reference = reference.Target
		uses[parsed.Store] = append(uses[parsed.Store], use)
	}
	return uses, rows.Err()
}

// SecretStoreInUse refuses removing a store something still reads, naming
// who, so the operator knows what to point elsewhere first.
func (s *PlanningStore) SecretStoreInUse(ctx context.Context, name string) error {
	uses, err := s.SecretStoreUses(ctx)
	if err != nil {
		return err
	}
	users := uses[name]
	if len(users) == 0 {
		return nil
	}
	named := make([]string, 0, len(users))
	for _, use := range users {
		named = append(named, fmt.Sprintf("%s (%s) as %s", use.ProjectName, use.EnvironmentName, use.Variable))
	}
	them := "that variable"
	if len(users) > 1 {
		them = "those variables"
	}
	return fmt.Errorf("%w: %s is read by %s; point %s elsewhere first", ErrSecretStoreInUse, name, strings.Join(named, ", "), them)
}

// ErrSecretStoreInUse refuses removing a store a variable still references:
// its next deploy would fail.
var ErrSecretStoreInUse = errors.New("secret store is in use")

// previousStoreReads is what the release a rollback or redeploy restores
// read from the stores when it was made, by reference, with that release's
// number.
func (s *OrchestrationStore) previousStoreReads(ctx context.Context, run EngineRun) (map[string]string, int64) {
	if run.Operation != OperationRollback && run.Operation != OperationRedeploy {
		return nil, 0
	}
	target, err := operationTargetReleaseID(run)
	if err != nil {
		return nil, 0
	}
	var runID, number int64
	if s.db.QueryRowContext(ctx, `SELECT run_id, release_number FROM deploy_releases WHERE id = ?`, target).Scan(&runID, &number) != nil {
		return nil, 0
	}
	var raw string
	if s.db.QueryRowContext(ctx, `SELECT metadata_json FROM deploy_runs WHERE id = ?`, runID).Scan(&raw) != nil {
		return nil, 0
	}
	var metadata struct {
		Reads []StoreRead `json:"secretStoreReads"`
	}
	if json.Unmarshal([]byte(raw), &metadata) != nil {
		return nil, 0
	}
	out := map[string]string{}
	for _, read := range metadata.Reads {
		out[read.Reference] = read.Version
	}
	return out, number
}

// readsSecretStoresAt says whether a step is where a run reads its stores:
// the first step of every operation that builds or starts something — a
// deploy's resolve_source, a rollback's or redeploy's render_runtime, a
// restart's or start's start_candidate. A stop, a preview's removal and a
// managed removal read nothing, so a store that is down never keeps anybody
// from stopping a deployment.
func readsSecretStoresAt(execution StepExecution) bool {
	switch execution.Run.Operation {
	case OperationStop, OperationPreviewRemove, OperationRemoveManaged:
		return false
	}
	switch execution.Step.Key {
	case StepResolveSource, StepRenderRuntime, StepStartCandidate:
		return true
	}
	return false
}

// readSecretStores is the run's read of its stores, at its first step. It
// writes what it read to the transcript and the run's metadata — the store,
// the path and key, the version — and fails the step in the store's words
// when a store does not answer.
func (e *NormalizedStepExecutor) readSecretStores(ctx context.Context, execution StepExecution) *StepResult {
	if e.variables == nil || !readsSecretStoresAt(execution) || e.variables.HasRunStoreReads(execution.Run.ID) {
		return nil
	}
	reads, err := e.variables.ResolveRunSecretStores(ctx, execution.Run)
	if err != nil {
		var failure *secretstore.Failure
		if !errors.As(err, &failure) {
			result := normalizedStepFailure(err)
			return &result
		}
		_ = stepLog(execution, "stderr", "Could not read "+failure.Error())
		return &StepResult{State: StepFailed, ErrorCode: "secret_store_unreadable", ErrorMessage: failure.Error()}
	}
	if len(reads) == 0 {
		return nil
	}
	previous, releaseNumber := e.store.previousStoreReads(ctx, execution.Run)
	for _, read := range reads {
		line := fmt.Sprintf("Read %s from secret store %s: %s", strings.Join(read.Variables, ", "), read.Store, read.Location)
		if read.Version != "" {
			line += ", version " + read.Version
		}
		if before, ok := previous[read.Reference]; ok && before != "" && read.Version != "" && before != read.Version {
			line += fmt.Sprintf(" (release #%d read version %s; pin a version in the reference to keep one)", releaseNumber, before)
		}
		_ = stepLog(execution, "status", line)
	}
	if _, err := e.store.MergeRunMetadata(ctx, execution.Run.ID, map[string]any{"secretStoreReads": reads}); err != nil {
		_ = stepLog(execution, "stderr", "Could not record which versions were read: "+err.Error())
	}
	return nil
}
