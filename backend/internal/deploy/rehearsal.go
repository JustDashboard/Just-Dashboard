package deploy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Migration rehearsal: the release tasks, run once first against a copy.
//
// A release task is the one step of a deployment that changes the database
// the live release is serving from, before anything else about the new
// release is known to work. A migration that fails on the data production
// actually holds — a NOT NULL on a column with nulls in it, a unique index
// over duplicates, a rename the old code still reads — fails there, half
// applied, with the release stopped and the old one now pointed at a schema
// it does not expect. The rehearsal restores the linked database from its
// backup into a container nobody else can reach, runs the same tasks with
// the same image and the same environment against it, and only lets the run
// continue to the real tasks when they pass there. The copy's address is the
// production database's own name, answered on a network only the copy and
// the task share, so not one variable is rewritten and nothing the task does
// can reach production: the network has no route out.

const (
	RehearsalWhenUnavailableBlock = "block"
	RehearsalWhenUnavailableWarn  = "warn"
	rehearsalLabel                = "io.just-dashboard.rehearsal"
)

// ErrRehearsalUnavailable is a rehearsal that could not be carried out — no
// backup to restore, an engine it does not restore, a copy that would not
// start — as opposed to a task that ran against the copy and failed.
var ErrRehearsalUnavailable = errors.New("the migration could not be rehearsed")

// RehearsalPolicy is one environment's terms. A rehearsal is opt-in: it
// restores the whole database once per deployment that runs a release task,
// which on a large database is minutes the operator should choose to spend.
type RehearsalPolicy struct {
	Enabled bool `json:"enabled"`
	// ConnectionID names the database to rehearse against when the release
	// task's variables reach more than one; zero is the only one they reach.
	ConnectionID int64 `json:"connectionId"`
	// WhenUnavailable is what a rehearsal that cannot be carried out does:
	// block the release, or let it continue with a warning on the run.
	WhenUnavailable       string     `json:"whenUnavailable"`
	RestoreTimeoutSeconds int        `json:"restoreTimeoutSeconds"`
	MaxBackupMB           int        `json:"maxBackupMb"`
	UpdatedBy             string     `json:"updatedBy,omitempty"`
	UpdatedAt             *time.Time `json:"updatedAt,omitempty"`
}

// DefaultRehearsalPolicy is off. When it is turned on, a rehearsal that
// cannot run blocks the release: the operator asked for the proof, and a
// release that goes out without it should be their decision, not a default.
func DefaultRehearsalPolicy() RehearsalPolicy {
	return RehearsalPolicy{
		WhenUnavailable: RehearsalWhenUnavailableBlock, RestoreTimeoutSeconds: 600, MaxBackupMB: 2048,
	}
}

func validateRehearsalPolicy(policy RehearsalPolicy) (RehearsalPolicy, error) {
	if policy.WhenUnavailable == "" {
		policy.WhenUnavailable = RehearsalWhenUnavailableBlock
	}
	switch {
	case policy.WhenUnavailable != RehearsalWhenUnavailableBlock && policy.WhenUnavailable != RehearsalWhenUnavailableWarn:
		return policy, fmt.Errorf("%w: when a rehearsal cannot run, the release is blocked or warned", ErrInvalidPlan)
	case policy.ConnectionID < 0:
		return policy, fmt.Errorf("%w: the database is a saved connection", ErrInvalidPlan)
	case policy.RestoreTimeoutSeconds < 30 || policy.RestoreTimeoutSeconds > 6*3600:
		return policy, fmt.Errorf("%w: the restore limit is between 30 seconds and 6 hours", ErrInvalidPlan)
	case policy.MaxBackupMB < 1 || policy.MaxBackupMB > 1<<20:
		return policy, fmt.Errorf("%w: the backup size limit is between 1 MB and 1 TB", ErrInvalidPlan)
	}
	return policy, nil
}

func (s *OrchestrationStore) RehearsalPolicy(ctx context.Context, environmentID int64) (RehearsalPolicy, error) {
	policy := DefaultRehearsalPolicy()
	var enabled int
	var updatedAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT enabled, connection_id, when_unavailable, restore_timeout_seconds, max_backup_mb, updated_by, updated_at
		  FROM deploy_rehearsal_policies WHERE environment_id = ?`, environmentID).
		Scan(&enabled, &policy.ConnectionID, &policy.WhenUnavailable, &policy.RestoreTimeoutSeconds,
			&policy.MaxBackupMB, &policy.UpdatedBy, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return policy, nil
	}
	if err != nil {
		return policy, err
	}
	policy.Enabled, policy.UpdatedAt = enabled != 0, unixTimePtr(updatedAt)
	return policy, nil
}

// PutRehearsalPolicy saves an environment's terms; the next run reads them.
func (s *OrchestrationStore) PutRehearsalPolicy(ctx context.Context, projectID, environmentID int64, policy RehearsalPolicy, actor string) (RehearsalPolicy, error) {
	policy, err := validateRehearsalPolicy(policy)
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
	enabled := 0
	if policy.Enabled {
		enabled = 1
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO deploy_rehearsal_policies(environment_id, enabled, connection_id, when_unavailable,
		  restore_timeout_seconds, max_backup_mb, updated_by, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(environment_id) DO UPDATE SET enabled = excluded.enabled, connection_id = excluded.connection_id,
		  when_unavailable = excluded.when_unavailable, restore_timeout_seconds = excluded.restore_timeout_seconds,
		  max_backup_mb = excluded.max_backup_mb, updated_by = excluded.updated_by, updated_at = excluded.updated_at`,
		environmentID, enabled, policy.ConnectionID, policy.WhenUnavailable, policy.RestoreTimeoutSeconds,
		policy.MaxBackupMB, actor, s.now().UTC().Unix()); err != nil {
		return policy, err
	}
	return s.RehearsalPolicy(ctx, environmentID)
}

// RehearsalCopyRequest asks for the throwaway copy one run rehearses against.
type RehearsalCopyRequest struct {
	Run          EngineRun
	ConnectionID int64
	Policy       RehearsalPolicy
}

// RehearsalCopy is a restored copy, running on a network of its own. Network
// is what the rehearsal's task containers join, and nothing else.
type RehearsalCopy struct {
	Network   string          `json:"network"`
	Container string          `json:"container"`
	Database  RehearsalTarget `json:"database"`
	Source    RehearsalSource `json:"source"`
	RestoreMS int64           `json:"restoreMs"`
}

// RehearsalTarget is the database rehearsed against.
type RehearsalTarget struct {
	ConnectionID int64  `json:"connectionId"`
	Name         string `json:"name"`
	Engine       string `json:"engine"`
	Database     string `json:"database"`
	Image        string `json:"image"`
}

// RehearsalSource is the backup the copy was restored from.
type RehearsalSource struct {
	// Kind is "pitr" for a WAL-G point-in-time recovery, "dump" for a backup
	// job's native dump.
	Kind        string     `json:"kind"`
	BackupRunID int64      `json:"backupRunId,omitempty"`
	TakenAt     *time.Time `json:"takenAt,omitempty"`
	Bytes       int64      `json:"bytes,omitempty"`
	Method      string     `json:"method,omitempty"`
}

// RehearsalLockSample is one reading of the copy's lock table while a task
// runs: a session waiting for a lock, or a lock that would block the live
// application's reads or writes in production.
type RehearsalLockSample struct {
	Waiting  bool   `json:"waiting"`
	Relation string `json:"relation"`
	Mode     string `json:"mode"`
}

// MigrationRehearser is the Databases owner's side: it makes the copy,
// reads its locks and removes it. The tasks themselves run through the
// release-task runtime, exactly as they will for real.
type MigrationRehearser interface {
	PrepareRehearsalCopy(context.Context, RehearsalCopyRequest, func(BuildLog) error) (*RehearsalCopy, error)
	SampleRehearsalLocks(context.Context, RehearsalCopy) ([]RehearsalLockSample, bool, error)
	RemoveRehearsalCopy(context.Context, EngineRun) error
}

// WithMigrationRehearser attaches the Databases owner's copies. Without it a
// rehearsal turned on is reported as unavailable, never silently skipped.
func (e *NormalizedStepExecutor) WithMigrationRehearser(rehearser MigrationRehearser) *NormalizedStepExecutor {
	e.rehearser = rehearser
	return e
}

// RehearsalReport is what the run page shows for the step.
type RehearsalReport struct {
	Rehearsed bool              `json:"rehearsed"`
	Reason    string            `json:"reason,omitempty"`
	Database  *RehearsalTarget  `json:"database,omitempty"`
	Source    *RehearsalSource  `json:"source,omitempty"`
	RestoreMS int64             `json:"restoreMs,omitempty"`
	Tasks     []RehearsalTask   `json:"tasks"`
	Locks     RehearsalLocks    `json:"locks"`
	Isolation *RehearsalNetwork `json:"isolation,omitempty"`
	CleanedUp bool              `json:"cleanedUp"`
	TotalMS   int64             `json:"totalMs"`
}

type RehearsalTask struct {
	Name       string `json:"name"`
	DurationMS int64  `json:"durationMs"`
	ExitCode   int    `json:"exitCode"`
	Error      string `json:"error,omitempty"`
}

// RehearsalLocks is what the copy's lock table showed while the tasks ran.
// Waits are sessions that waited for a lock; Held are the locks that block
// an application's reads or writes, and roughly for how long — the time a
// production table would have been unavailable for the same migration.
type RehearsalLocks struct {
	Read       bool                `json:"read"`
	Samples    int                 `json:"samples"`
	MaxWaiting int                 `json:"maxWaiting"`
	WaitedMS   int64               `json:"waitedMs"`
	Held       []RehearsalHeldLock `json:"held"`
}

type RehearsalHeldLock struct {
	Relation string `json:"relation"`
	Mode     string `json:"mode"`
	HeldMS   int64  `json:"heldMs"`
}

type RehearsalNetwork struct {
	Network        string `json:"network"`
	Internal       bool   `json:"internal"`
	Published      bool   `json:"published"`
	VolumesMounted bool   `json:"volumesMounted"`
}

// rehearsalDatabaseIDs are the managed databases a task's variables reach,
// by the names those databases answer on: db-<id>.jd.internal and its
// pooler's db-<id>-pool.jd.internal.
func rehearsalDatabaseIDs(variables map[string]string) []int64 {
	seen := map[int64]bool{}
	ids := []int64{}
	for _, value := range variables {
		parsed, err := url.Parse(value)
		if err != nil {
			continue
		}
		host := strings.ToLower(parsed.Hostname())
		if !strings.HasPrefix(host, "db-") || !strings.HasSuffix(host, ".jd.internal") {
			continue
		}
		raw := strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(host, "db-"), ".jd.internal"), "-pool")
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// chooseRehearsalTarget picks the database to restore: the one the task's
// variables reach, or, when they reach several, the one the terms name. It
// answers why not when it cannot choose.
func chooseRehearsalTarget(reached []int64, named int64) (int64, string) {
	if named > 0 {
		for _, id := range reached {
			if id == named {
				return id, ""
			}
		}
		return 0, fmt.Sprintf("the chosen database (connection %d) is not one the release task's variables reach", named)
	}
	switch len(reached) {
	case 0:
		return 0, "the release task's variables reach no database this dashboard manages, so there is nothing to restore a copy of"
	case 1:
		return reached[0], ""
	default:
		names := make([]string, 0, len(reached))
		for _, id := range reached {
			names = append(names, strconv.FormatInt(id, 10))
		}
		return 0, fmt.Sprintf("the release task reaches %d databases (connections %s); choose the one to rehearse against in Build settings", len(reached), strings.Join(names, ", "))
	}
}

// rehearseMigration is the rehearse_migration step.
func (e *NormalizedStepExecutor) rehearseMigration(ctx context.Context, execution StepExecution, plan *StoredExecutionPlan) StepResult {
	if len(plan.Build.ReleaseTasks) == 0 {
		return StepResult{State: StepSkipped, Evidence: mustJSON(map[string]any{"reason": "no release tasks configured"})}
	}
	policy, err := e.store.RehearsalPolicy(ctx, execution.Run.EnvironmentID)
	if err != nil {
		return normalizedStepFailure(err)
	}
	if !policy.Enabled {
		return StepResult{State: StepSkipped, Evidence: mustJSON(map[string]any{"reason": "migration rehearsal is turned off for this environment"})}
	}
	started := time.Now()
	report := RehearsalReport{Tasks: []RehearsalTask{}, Locks: RehearsalLocks{Held: []RehearsalHeldLock{}}}
	unavailable := func(reason string) StepResult {
		report.Reason = reason
		report.TotalMS = time.Since(started).Milliseconds()
		if policy.WhenUnavailable == RehearsalWhenUnavailableWarn {
			_ = stepLog(execution, "stderr", "Not rehearsed: "+reason+". The release continues, as this environment's terms allow.")
			return StepResult{State: StepWarning, Evidence: mustJSON(report)}
		}
		_ = stepLog(execution, "stderr", "Not rehearsed: "+reason)
		return StepResult{State: StepFailed, ErrorCode: "rehearsal_unavailable", Evidence: mustJSON(report),
			ErrorMessage: "The release tasks were not rehearsed, so they were not run and production was not touched: " + reason + "."}
	}
	for _, task := range plan.Build.ReleaseTasks {
		if task.Runner != ReleaseTaskRunnerImage {
			return unavailable(fmt.Sprintf("release task %q runs in the dashboard's shell, and only a task that runs in the release image can be rehearsed", task.Name))
		}
	}
	if e.rehearser == nil {
		return unavailable("this host cannot make database copies")
	}
	request, runner, err := e.releaseTaskImageRequest(ctx, execution, plan)
	if err != nil {
		return unavailable("the release image could not be prepared: " + err.Error())
	}
	releaseValues, err := e.variablesForScope(ctx, execution.Run.ID, execution.Run.EnvironmentID, "release_task")
	if err != nil {
		return normalizedStepFailure(err)
	}
	reached := map[string]string{}
	for name, value := range request.Variables {
		reached[name] = value
	}
	for _, task := range plan.Build.ReleaseTasks {
		for _, name := range task.Env {
			if value, ok := releaseValues[name]; ok {
				reached[name] = value
			}
		}
	}
	target, why := chooseRehearsalTarget(rehearsalDatabaseIDs(reached), policy.ConnectionID)
	if target == 0 {
		return unavailable(why)
	}
	if execution.Run.Operation == OperationPromote {
		// The tasks are production's and so are the variables; only the
		// image is staging's. Saying so here is what tells a reader of the
		// transcript which database the copy is of.
		_ = stepLog(execution, "status", "Rehearsing production's release tasks in the promoted image "+request.Image+" with production's variables")
	}
	emit := func(line BuildLog) error { return stepLog(execution, line.Stream, line.Text) }
	// Whatever the rehearsal made goes with it, however it ends; a run a
	// restart interrupted finds its own leftovers removed before it starts
	// again, and the next process start removes any nobody came back for.
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cancel()
		if err := e.rehearser.RemoveRehearsalCopy(cleanup, execution.Run); err != nil {
			_ = stepLog(execution, "stderr", "The rehearsal's copy could not be removed yet: "+err.Error())
		}
	}()
	if err := e.rehearser.RemoveRehearsalCopy(ctx, execution.Run); err != nil {
		return unavailable("a copy an interrupted rehearsal left behind could not be removed: " + err.Error())
	}
	_ = stepLog(execution, "status", fmt.Sprintf("Restoring a copy of database connection %d to rehearse the release tasks against", target))
	restoreCtx, cancelRestore := context.WithTimeout(ctx, time.Duration(policy.RestoreTimeoutSeconds)*time.Second)
	restored, err := e.rehearser.PrepareRehearsalCopy(restoreCtx, RehearsalCopyRequest{Run: execution.Run, ConnectionID: target, Policy: policy}, emit)
	restoreTimedOut := restoreCtx.Err() != nil && ctx.Err() == nil
	cancelRestore()
	if ctx.Err() != nil {
		return StepResult{State: StepCancelled, ErrorCode: "cancelled", ErrorMessage: "the rehearsal was cancelled"}
	}
	if err != nil {
		reason := strings.TrimPrefix(err.Error(), ErrRehearsalUnavailable.Error()+": ")
		if restoreTimedOut {
			reason = fmt.Sprintf("the copy was not restored within %d seconds", policy.RestoreTimeoutSeconds)
		}
		return unavailable(reason)
	}
	report.Rehearsed = true
	report.Database, report.Source, report.RestoreMS = &restored.Database, &restored.Source, restored.RestoreMS
	report.Isolation = &RehearsalNetwork{Network: restored.Network, Internal: true}
	_ = stepLog(execution, "status", fmt.Sprintf("Restored %s from %s in %s; running the release tasks against it",
		restored.Database.Name, sourceWords(restored.Source), (time.Duration(restored.RestoreMS)*time.Millisecond).Round(100*time.Millisecond)))

	sampler := newLockSampler(e.rehearser, *restored)
	failed := ""
	for index, task := range plan.Build.ReleaseTasks {
		taskRequest := request
		taskRequest.Task, taskRequest.Index = task, index
		taskRequest.Networks, taskRequest.Rehearsal = []string{restored.Network}, true
		_ = stepLog(execution, "status", "Rehearsing release task "+task.Name)
		output := newLineRing(outputRingLines, outputRingBytes)
		taskEmit := func(line BuildLog) error {
			if err := emit(line); err != nil {
				return err
			}
			output.add(collectedLine{text: truncateUTF8Prefix(line.Text, collectedLineBytes)})
			return nil
		}
		stop := sampler.start(ctx)
		evidence, _, taskErr := runImageReleaseTask(ctx, runner, taskRequest, releaseValues, taskEmit)
		stop()
		entry := RehearsalTask{Name: task.Name, DurationMS: evidence.DurationMS, ExitCode: evidence.ExitCode}
		if taskErr != nil {
			entry.Error = lastOutputLine(output.lines, taskErr)
			failed = task.Name
		}
		report.Tasks = append(report.Tasks, entry)
		if taskErr != nil {
			break
		}
	}
	report.Locks = sampler.report()
	report.TotalMS = time.Since(started).Milliseconds()
	for _, held := range report.Locks.Held {
		if lockCovered(report.Locks.Held, held) {
			continue
		}
		waits := "writes"
		if held.Mode == "AccessExclusiveLock" {
			waits = "reads and writes"
		}
		_ = stepLog(execution, "status", fmt.Sprintf("Held %s on %s for about %s — production %s of %s would wait that long",
			held.Mode, held.Relation, (time.Duration(held.HeldMS)*time.Millisecond).Round(100*time.Millisecond), waits, held.Relation))
	}
	if ctx.Err() != nil {
		return StepResult{State: StepCancelled, ErrorCode: "cancelled", ErrorMessage: "the rehearsal was cancelled", Evidence: mustJSON(report)}
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	report.CleanedUp = e.rehearser.RemoveRehearsalCopy(cleanup, execution.Run) == nil
	cancel()
	if failed != "" {
		last := report.Tasks[len(report.Tasks)-1]
		return StepResult{State: StepFailed, ErrorCode: "rehearsal_failed", Evidence: mustJSON(report),
			ErrorMessage: fmt.Sprintf("Release task %q failed against a copy of %s restored from %s (exit code %d), so the release stopped before production was touched: %s",
				failed, restored.Database.Name, sourceWords(restored.Source), last.ExitCode, last.Error)}
	}
	_ = stepLog(execution, "status", fmt.Sprintf("The release tasks passed against the copy in %s; running them against production",
		(time.Duration(report.TotalMS)*time.Millisecond).Round(100*time.Millisecond)))
	return StepResult{State: StepPassed, Evidence: mustJSON(report)}
}

func sourceWords(source RehearsalSource) string {
	if source.Kind == "pitr" {
		return "its point-in-time archive"
	}
	if source.TakenAt != nil {
		return "the backup of " + source.TakenAt.UTC().Format("2006-01-02 15:04 UTC")
	}
	return "its latest backup"
}

// lastOutputLine is the line a failed task's error is told by: the last
// thing it printed, which is the database's own refusal far more often than
// not, or the runner's error when it printed nothing.
func lastOutputLine(lines []collectedLine, err error) string {
	for index := len(lines) - 1; index >= 0; index-- {
		if text := strings.TrimSpace(lines[index].text); text != "" {
			return text
		}
	}
	return err.Error()
}

// lockSampler reads the copy's lock table every quarter second while a task
// runs and folds the readings into a report.
type lockSampler struct {
	rehearser MigrationRehearser
	target    RehearsalCopy
	interval  time.Duration

	mu         sync.Mutex
	read       bool
	samples    int
	maxWaiting int
	waited     time.Duration
	held       map[string]*heldSpan
	order      []string
}

type heldSpan struct {
	relation, mode string
	first, last    time.Time
}

func newLockSampler(rehearser MigrationRehearser, restored RehearsalCopy) *lockSampler {
	return &lockSampler{rehearser: rehearser, target: restored, interval: 250 * time.Millisecond, held: map[string]*heldSpan{}}
}

func (l *lockSampler) start(ctx context.Context) func() {
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		ticker := time.NewTicker(l.interval)
		defer ticker.Stop()
		for {
			l.take(ctx)
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() {
		close(done)
		<-finished
	}
}

func (l *lockSampler) take(ctx context.Context) {
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	samples, supported, err := l.rehearser.SampleRehearsalLocks(readCtx, l.target)
	if err != nil || !supported {
		return
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.read = true
	l.samples++
	waiting := 0
	for _, sample := range samples {
		if sample.Waiting {
			waiting++
			continue
		}
		key := sample.Relation + "\x00" + sample.Mode
		span := l.held[key]
		if span == nil {
			span = &heldSpan{relation: sample.Relation, mode: sample.Mode, first: now}
			l.held[key] = span
			l.order = append(l.order, key)
		}
		span.last = now
	}
	if waiting > l.maxWaiting {
		l.maxWaiting = waiting
	}
	if waiting > 0 {
		l.waited += l.interval
	}
}

func (l *lockSampler) report() RehearsalLocks {
	l.mu.Lock()
	defer l.mu.Unlock()
	locks := RehearsalLocks{Read: l.read, Samples: l.samples, MaxWaiting: l.maxWaiting, WaitedMS: l.waited.Milliseconds(), Held: []RehearsalHeldLock{}}
	for _, key := range l.order {
		span := l.held[key]
		// A lock seen once was held for at least one interval.
		duration := span.last.Sub(span.first) + l.interval
		locks.Held = append(locks.Held, RehearsalHeldLock{Relation: span.relation, Mode: span.mode, HeldMS: duration.Milliseconds()})
	}
	return locks
}

// lockModeStrength orders the modes the copy's lock table is read for,
// strongest first.
var lockModeStrength = map[string]int{"AccessExclusiveLock": 0, "ExclusiveLock": 1, "ShareRowExclusiveLock": 2, "ShareLock": 3}

// lockCovered reports a lock that a stronger one on the same table, held at
// least as long, says everything about: the SHARE a CREATE INDEX takes inside
// the transaction of an ALTER TABLE that already holds ACCESS EXCLUSIVE.
func lockCovered(held []RehearsalHeldLock, lock RehearsalHeldLock) bool {
	mine, known := lockModeStrength[lock.Mode]
	if !known {
		return false
	}
	for _, other := range held {
		theirs, ok := lockModeStrength[other.Mode]
		if ok && other.Relation == lock.Relation && theirs < mine && other.HeldMS >= lock.HeldMS {
			return true
		}
	}
	return false
}
