package deploy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Data-aware rollback.
//
// A rollback moves the application back to a retained release and leaves
// every database exactly as it is now: a release's image and configuration
// are immutable and can be put back, but its data was never the release's to
// keep. What can be kept is where the data stood when the release went live —
// the moment a linked PostgreSQL's point-in-time archive recovers to, or the
// backup run whose dump is the newest from before it — and from that point a
// copy of the database as it was can be restored beside production: on the
// deployment's own network, under a name of its own and with a sign-in made
// for it, for a bounded time, to compare with the live database or to read
// back what a release lost.
//
// The copy is never production. Bringing its data back into production is
// the Databases' own restore, with that route's own confirmation; pointing a
// release at the copy is not offered at all, because the copy is removed when
// it expires, and a variable that names one is refused before a release
// starts (api: databaseURLConnectionIDs).

const (
	DataPointPITR = "pitr"
	DataPointDump = "dump"
	DataPointNone = "none"

	DataCopyWaiting   = "waiting"
	DataCopyRestoring = "restoring"
	DataCopyReady     = "ready"
	DataCopyFailed    = "failed"
	DataCopySkipped   = "skipped"
	DataCopyRemoved   = "removed"

	// DataCopyLifetime is how long a copy is kept once it is ready: long
	// enough to compare and to read back what was lost, short enough that a
	// second copy of production's data does not sit on the server forgotten.
	DataCopyLifetime = 2 * time.Hour
	// DataCopyMaxAhead bounds an extension: a copy is never kept more than
	// this far from now.
	DataCopyMaxAhead = 24 * time.Hour
	// dataCopyHistory is how long an ended copy's row is still listed.
	dataCopyHistory = 24 * time.Hour
)

var (
	// ErrDataCopyUnavailable is a release whose data cannot be brought back
	// into a copy: the message says why.
	ErrDataCopyUnavailable = errors.New("data copy unavailable")
	// ErrDataCopyExists is a second copy asked for while one of the same
	// release and database is still waiting, restoring or ready.
	ErrDataCopyExists   = errors.New("a copy of this database as of this release already exists")
	ErrDataCopyNotFound = errors.New("data copy not found")
	ErrDataCopyNotReady = errors.New("the data copy is not ready")
	ErrDataCopyEnded    = errors.New("the data copy has already ended")
)

// ReleaseDataPoint is where one linked database could be brought back to, as
// of the moment a release went live. Kind none keeps the reason, so a release
// says why it cannot bring its data back rather than staying silent.
type ReleaseDataPoint struct {
	ConnectionID int64  `json:"connectionId"`
	Name         string `json:"name"`
	Engine       string `json:"engine"`
	Kind         string `json:"kind"`
	// At is the moment a point-in-time archive recovers to — the activation
	// itself — or when the backup run holding the dump started.
	At          *time.Time `json:"at,omitempty"`
	BackupRunID int64      `json:"backupRunId,omitempty"`
	BackupJobID int64      `json:"backupJobId,omitempty"`
	ArchiveID   int64      `json:"archiveId,omitempty"`
	Reason      string     `json:"reason,omitempty"`
	RecordedAt  time.Time  `json:"recordedAt"`
}

// DataPointFacts is what the Databases owner knows about one database an
// environment links, at the moment one of its releases goes live.
type DataPointFacts struct {
	ConnectionID int64
	Name, Engine string
	// ArchiveID is the point-in-time archive the server writes into now, and
	// ArchiveFrom the earliest moment it recovers to (the end of its oldest
	// base backup), zero while no base backup has been listed.
	ArchiveID   int64
	ArchiveFrom time.Time
	// DumpRunID is the newest successful backup run holding the database's
	// native dump, started at DumpAt, by the job DumpJobID.
	DumpRunID, DumpJobID int64
	DumpAt               time.Time
	// Unsupported is why no copy of this database can be restored at all —
	// an engine without a restore path — and empty when one can.
	Unsupported string
}

// DataPointObserver is the Databases owner's side: what it knows about each
// database the environment links.
type DataPointObserver interface {
	ReleaseDataFacts(ctx context.Context, environmentID int64, at time.Time) ([]DataPointFacts, error)
}

// WithDataPoints attaches the Databases owner's facts. Without it a release
// goes live with no data point, which it says when asked.
func (e *NormalizedStepExecutor) WithDataPoints(observer DataPointObserver) *NormalizedStepExecutor {
	e.dataPoints = observer
	return e
}

// DecideDataPoint is the point one database is recorded at for a release
// that went live at at: the moment itself when the server's archive reaches
// back that far, since a point-in-time recovery stops at any moment the log
// covers, and otherwise the newest dump, which is the state the database is
// known to be restorable to that is closest before the release.
func DecideDataPoint(facts DataPointFacts, at time.Time) ReleaseDataPoint {
	at = at.UTC()
	point := ReleaseDataPoint{ConnectionID: facts.ConnectionID, Name: facts.Name, Engine: facts.Engine, Kind: DataPointNone, RecordedAt: at}
	name := facts.Name
	if name == "" {
		name = fmt.Sprintf("connection %d", facts.ConnectionID)
	}
	switch {
	case facts.Unsupported != "":
		point.Reason = facts.Unsupported
	case facts.ArchiveID > 0 && !facts.ArchiveFrom.IsZero() && !facts.ArchiveFrom.After(at):
		moment := at
		point.Kind, point.At, point.ArchiveID = DataPointPITR, &moment, facts.ArchiveID
	case facts.DumpRunID > 0 && !facts.DumpAt.After(at):
		taken := facts.DumpAt.UTC()
		point.Kind, point.At, point.BackupRunID, point.BackupJobID = DataPointDump, &taken, facts.DumpRunID, facts.DumpJobID
	case facts.ArchiveID > 0:
		point.Reason = fmt.Sprintf("the point-in-time archive of %s holds no base backup from before this release went live, and no backup holds a dump of it", name)
	default:
		point.Reason = fmt.Sprintf("no backup holds a dump of %s and it has no point-in-time archive", name)
	}
	return point
}

// DataPointLine is the transcript's line for a recorded point.
func DataPointLine(point ReleaseDataPoint) string {
	switch point.Kind {
	case DataPointPITR:
		return fmt.Sprintf("Data point: %s can be brought back to %s from its point-in-time archive", point.Name, point.At.UTC().Format("2006-01-02 15:04:05 UTC"))
	case DataPointDump:
		return fmt.Sprintf("Data point: %s can be brought back to the backup of %s (backup run %d)", point.Name, point.At.UTC().Format("2006-01-02 15:04 UTC"), point.BackupRunID)
	}
	return fmt.Sprintf("No data point for %s: %s", point.Name, point.Reason)
}

// recordDataPoints writes where each linked database stood as the release
// went live. It never fails the activation: the release is live whatever is
// said about its data, and a release without a point says so when asked.
func (e *NormalizedStepExecutor) recordDataPoints(ctx context.Context, execution StepExecution, release Release, at time.Time) {
	if e.dataPoints == nil {
		return
	}
	// The pointer is committed: what follows belongs to the release, not to
	// a run that may be cancelled in the next instant.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	facts, err := e.dataPoints.ReleaseDataFacts(ctx, release.EnvironmentID, at)
	if err != nil {
		_ = stepLog(execution, "stderr", "The release's data point could not be recorded: "+err.Error())
		return
	}
	if len(facts) == 0 {
		return
	}
	points := make([]ReleaseDataPoint, 0, len(facts))
	for _, fact := range facts {
		points = append(points, DecideDataPoint(fact, at))
	}
	if err := e.store.RecordReleaseDataPoints(ctx, release.ID, points); err != nil {
		_ = stepLog(execution, "stderr", "The release's data point could not be recorded: "+err.Error())
		return
	}
	for _, point := range points {
		_ = stepLog(execution, "status", DataPointLine(point))
	}
}

// RecordReleaseDataPoints keeps a release's points. The first activation's
// stand: a resumed run that activates again is the same release going live
// once, and the moment it first did is the one its data is as of.
func (s *OrchestrationStore) RecordReleaseDataPoints(ctx context.Context, releaseID int64, points []ReleaseDataPoint) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, point := range points {
		at := int64(0)
		if point.At != nil {
			at = point.At.UTC().Unix()
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO deploy_release_data_points(release_id, connection_id, name, engine, kind, point_at,
			  backup_run_id, backup_job_id, archive_id, reason, recorded_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(release_id, connection_id) DO NOTHING`,
			releaseID, point.ConnectionID, point.Name, point.Engine, point.Kind, at,
			point.BackupRunID, point.BackupJobID, point.ArchiveID, point.Reason, point.RecordedAt.UTC().Unix()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ReleaseDataPoints reads the points of the releases named, by release.
func (s *OrchestrationStore) ReleaseDataPoints(ctx context.Context, releaseIDs ...int64) (map[int64][]ReleaseDataPoint, error) {
	out := map[int64][]ReleaseDataPoint{}
	if len(releaseIDs) == 0 {
		return out, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(releaseIDs)), ",")
	args := make([]any, 0, len(releaseIDs))
	for _, id := range releaseIDs {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT release_id, connection_id, name, engine, kind, point_at, backup_run_id, backup_job_id, archive_id, reason, recorded_at
		  FROM deploy_release_data_points WHERE release_id IN (`+placeholders+`)
		 ORDER BY release_id, name, connection_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var releaseID, at, recorded int64
		var point ReleaseDataPoint
		if err := rows.Scan(&releaseID, &point.ConnectionID, &point.Name, &point.Engine, &point.Kind, &at,
			&point.BackupRunID, &point.BackupJobID, &point.ArchiveID, &point.Reason, &recorded); err != nil {
			return nil, err
		}
		point.At, point.RecordedAt = unixTimePtr(at), unixTime(recorded)
		out[releaseID] = append(out[releaseID], point)
	}
	return out, rows.Err()
}

// DataCopyEligibility says whether "Restore data as of this release" is
// offered, and why not when it is not.
type DataCopyEligibility struct {
	Eligible bool   `json:"eligible"`
	Reason   string `json:"reason,omitempty"`
}

// DataCopyEligibilityFor decides whether a release's data can be brought
// back into a copy: a production or staging release that went live and is
// still kept, with at least one database recorded at a point a copy can be
// restored from.
func DataCopyEligibilityFor(kind EnvironmentKind, release Release, points []ReleaseDataPoint) DataCopyEligibility {
	switch {
	case kind == EnvironmentPreview:
		return DataCopyEligibility{Reason: "a preview's releases have no data copies"}
	case release.State != "live" && release.State != "retained":
		return DataCopyEligibility{Reason: "only a release that went live and is still kept can bring its data back"}
	case len(points) == 0:
		return DataCopyEligibility{Reason: "no data point was recorded when this release went live: it went live before data points were kept, or no managed database was linked"}
	}
	if len(RestorableDataPoints(points)) > 0 {
		return DataCopyEligibility{Eligible: true}
	}
	reasons := make([]string, 0, len(points))
	for _, point := range points {
		if point.Reason != "" {
			reasons = append(reasons, point.Reason)
		}
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "no linked database was recorded at a point a copy can be restored from")
	}
	return DataCopyEligibility{Reason: strings.Join(reasons, "; ")}
}

// RestorableDataPoints are the points a copy can be restored from.
func RestorableDataPoints(points []ReleaseDataPoint) []ReleaseDataPoint {
	out := []ReleaseDataPoint{}
	for _, point := range points {
		if (point.Kind == DataPointPITR || point.Kind == DataPointDump) && point.At != nil {
			out = append(out, point)
		}
	}
	return out
}

// DataCopySource is the point a copy was restored from.
type DataCopySource struct {
	Kind        string     `json:"kind"`
	At          *time.Time `json:"at,omitempty"`
	BackupRunID int64      `json:"backupRunId,omitempty"`
	BackupJobID int64      `json:"backupJobId,omitempty"`
	ArchiveID   int64      `json:"archiveId,omitempty"`
}

// DataCopy is one database restored as of a release, beside production.
type DataCopy struct {
	ID            int64          `json:"id"`
	ProjectID     int64          `json:"projectId"`
	EnvironmentID int64          `json:"environmentId"`
	ReleaseID     int64          `json:"releaseId"`
	ReleaseNumber int64          `json:"releaseNumber"`
	ConnectionID  int64          `json:"connectionId"`
	Name          string         `json:"name"`
	Engine        string         `json:"engine"`
	Database      string         `json:"database"`
	State         string         `json:"state"`
	Source        DataCopySource `json:"source"`
	RecoveredTo   *time.Time     `json:"recoveredTo,omitempty"`
	// AfterRunID is the run the copy waits for: a rollback that asked for
	// the release's data as well is restored once the rollback is over.
	AfterRunID int64      `json:"afterRunId,omitempty"`
	Container  string     `json:"container,omitempty"`
	Network    string     `json:"network,omitempty"`
	Host       string     `json:"host,omitempty"`
	Port       int        `json:"port,omitempty"`
	ExpiresAt  *time.Time `json:"expiresAt,omitempty"`
	CreatedBy  string     `json:"createdBy"`
	CreatedAt  time.Time  `json:"createdAt"`
	ReadyAt    *time.Time `json:"readyAt,omitempty"`
	EndedAt    *time.Time `json:"endedAt,omitempty"`
	Reason     string     `json:"reason,omitempty"`
	RestoreMS  int64      `json:"restoreMs,omitempty"`
}

// DataCopyActive is a copy that still holds, or is about to hold, a
// container.
func DataCopyActive(state string) bool {
	return state == DataCopyWaiting || state == DataCopyRestoring || state == DataCopyReady
}

// DataCopyExpired is a ready copy past its lifetime.
func DataCopyExpired(copy DataCopy, now time.Time) bool {
	return copy.State == DataCopyReady && copy.ExpiresAt != nil && !copy.ExpiresAt.After(now)
}

// ExtendDataCopyExpiry is a copy's new end after keeping it hours longer:
// from its current end, or from now when that has passed, never more than
// DataCopyMaxAhead from now.
func ExtendDataCopyExpiry(current *time.Time, now time.Time, hours int) (time.Time, error) {
	if hours < 1 || hours > int(DataCopyMaxAhead/time.Hour) {
		return time.Time{}, fmt.Errorf("%w: a copy is kept 1 to %d hours longer", ErrInvalidPlan, int(DataCopyMaxAhead/time.Hour))
	}
	from := now
	if current != nil && current.After(now) {
		from = *current
	}
	next := from.Add(time.Duration(hours) * time.Hour)
	if limit := now.Add(DataCopyMaxAhead); next.After(limit) {
		next = limit
	}
	return next.UTC().Truncate(time.Second), nil
}

// WaitingDataCopyDecision is what becomes of a copy waiting for a run: it
// starts once the run succeeded, waits while it goes on, and is skipped when
// the run ended any other way — a rollback that did not finish is not the
// moment to put a second copy of production's data beside it.
func WaitingDataCopyDecision(copy DataCopy, run *EngineRun) (start bool, skip string) {
	if copy.AfterRunID == 0 {
		return true, ""
	}
	if run == nil {
		return false, fmt.Sprintf("run %d it was waiting for no longer exists", copy.AfterRunID)
	}
	if !run.State.Terminal() {
		return false, ""
	}
	if run.State == RunSucceeded {
		return true, ""
	}
	return false, fmt.Sprintf("the rollback it was waiting for ended %s, so no copy was made", run.State)
}

const dataCopyColumns = `id, project_id, environment_id, release_id, release_number, connection_id, name, engine,
 database_name, state, source_kind, source_at, backup_run_id, backup_job_id, archive_id, recovered_to, after_run_id,
 container, network, host, port, expires_at, reason, restore_ms, created_by, created_at, ready_at, ended_at`

func scanDataCopy(row scanner) (*DataCopy, error) {
	var copy DataCopy
	var sourceAt, recovered, expires, created, ready, ended int64
	if err := row.Scan(&copy.ID, &copy.ProjectID, &copy.EnvironmentID, &copy.ReleaseID, &copy.ReleaseNumber,
		&copy.ConnectionID, &copy.Name, &copy.Engine, &copy.Database, &copy.State, &copy.Source.Kind, &sourceAt,
		&copy.Source.BackupRunID, &copy.Source.BackupJobID, &copy.Source.ArchiveID, &recovered, &copy.AfterRunID,
		&copy.Container, &copy.Network, &copy.Host, &copy.Port, &expires, &copy.Reason, &copy.RestoreMS,
		&copy.CreatedBy, &created, &ready, &ended); err != nil {
		return nil, err
	}
	copy.Source.At, copy.RecoveredTo, copy.ExpiresAt = unixTimePtr(sourceAt), unixTimePtr(recovered), unixTimePtr(expires)
	copy.CreatedAt, copy.ReadyAt, copy.EndedAt = unixTime(created), unixTimePtr(ready), unixTimePtr(ended)
	return &copy, nil
}

// CreateDataCopies asks for a copy of each database the release recorded at
// a restorable point. They wait, for the run named or for nobody, until the
// Databases owner claims them.
func (s *OrchestrationStore) CreateDataCopies(ctx context.Context, release Release, afterRunID int64, actor string) ([]DataCopy, error) {
	var kind EnvironmentKind
	if err := s.db.QueryRowContext(ctx, `SELECT kind FROM deploy_environments WHERE id = ? AND project_id = ? AND archived_at = 0`,
		release.EnvironmentID, release.ProjectID).Scan(&kind); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrEnvironmentNotFound
		}
		return nil, err
	}
	recorded, err := s.ReleaseDataPoints(ctx, release.ID)
	if err != nil {
		return nil, err
	}
	points := recorded[release.ID]
	if eligibility := DataCopyEligibilityFor(kind, release, points); !eligibility.Eligible {
		return nil, fmt.Errorf("%w: %s", ErrDataCopyUnavailable, eligibility.Reason)
	}
	if afterRunID > 0 {
		run, err := s.Run(ctx, afterRunID)
		if err != nil || run.ProjectID != release.ProjectID || run.EnvironmentID != release.EnvironmentID {
			return nil, fmt.Errorf("%w: run %d is not one of this environment's", ErrDataCopyUnavailable, afterRunID)
		}
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := s.now().UTC().Unix()
	ids := []int64{}
	for _, point := range RestorableDataPoints(points) {
		var active int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM deploy_data_copies
			WHERE release_id = ? AND connection_id = ? AND state IN ('waiting', 'restoring', 'ready')`,
			release.ID, point.ConnectionID).Scan(&active); err != nil {
			return nil, err
		}
		if active > 0 {
			return nil, fmt.Errorf("%w: %s as of release #%d is still kept; remove it first", ErrDataCopyExists, point.Name, release.Number)
		}
		result, err := tx.ExecContext(ctx, `
			INSERT INTO deploy_data_copies(project_id, environment_id, release_id, release_number, connection_id, name, engine,
			  state, source_kind, source_at, backup_run_id, backup_job_id, archive_id, after_run_id, created_by, created_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, 'waiting', ?, ?, ?, ?, ?, ?, ?, ?)`,
			release.ProjectID, release.EnvironmentID, release.ID, release.Number, point.ConnectionID, point.Name, point.Engine,
			point.Kind, point.At.UTC().Unix(), point.BackupRunID, point.BackupJobID, point.ArchiveID, afterRunID, actor, now)
		if err != nil {
			return nil, err
		}
		id, _ := result.LastInsertId()
		ids = append(ids, id)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	out := make([]DataCopy, 0, len(ids))
	for _, id := range ids {
		copy, err := s.DataCopy(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *copy)
	}
	return out, nil
}

// DataCopyFilter narrows an environment's copies to one run's or one
// release's.
type DataCopyFilter struct {
	RunID, ReleaseID int64
}

// DataCopies lists an environment's copies still kept, waiting or being
// restored, and those that ended within the last day, newest first.
func (s *OrchestrationStore) DataCopies(ctx context.Context, environmentID int64, filter DataCopyFilter) ([]DataCopy, error) {
	query := `SELECT ` + dataCopyColumns + ` FROM deploy_data_copies
		 WHERE environment_id = ? AND (state IN ('waiting', 'restoring', 'ready') OR ended_at >= ?)`
	args := []any{environmentID, s.now().Add(-dataCopyHistory).UTC().Unix()}
	if filter.RunID > 0 {
		query += ` AND after_run_id = ?`
		args = append(args, filter.RunID)
	}
	if filter.ReleaseID > 0 {
		query += ` AND release_id = ?`
		args = append(args, filter.ReleaseID)
	}
	return s.queryDataCopies(ctx, query+` ORDER BY id DESC LIMIT 50`, args...)
}

// DataCopiesInState is every copy in one of the states, oldest first, for
// the owner's passes.
func (s *OrchestrationStore) DataCopiesInState(ctx context.Context, states ...string) ([]DataCopy, error) {
	if len(states) == 0 {
		return []DataCopy{}, nil
	}
	args := make([]any, 0, len(states))
	for _, state := range states {
		args = append(args, state)
	}
	return s.queryDataCopies(ctx, `SELECT `+dataCopyColumns+` FROM deploy_data_copies WHERE state IN (`+
		strings.TrimSuffix(strings.Repeat("?,", len(states)), ",")+`) ORDER BY id`, args...)
}

func (s *OrchestrationStore) queryDataCopies(ctx context.Context, query string, args ...any) ([]DataCopy, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DataCopy{}
	for rows.Next() {
		copy, err := scanDataCopy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *copy)
	}
	return out, rows.Err()
}

func (s *OrchestrationStore) DataCopy(ctx context.Context, id int64) (*DataCopy, error) {
	copy, err := scanDataCopy(s.db.QueryRowContext(ctx, `SELECT `+dataCopyColumns+` FROM deploy_data_copies WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDataCopyNotFound
	}
	return copy, err
}

// DataCopySecret is the copy's sealed sign-in, for its owner to open.
func (s *OrchestrationStore) DataCopySecret(ctx context.Context, id int64) (string, error) {
	var sealed string
	err := s.db.QueryRowContext(ctx, `SELECT secret_enc FROM deploy_data_copies WHERE id = ?`, id).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrDataCopyNotFound
	}
	return sealed, err
}

// ClaimDataCopy moves a waiting copy to restoring; false is one somebody
// else claimed or ended first.
func (s *OrchestrationStore) ClaimDataCopy(ctx context.Context, id int64) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	result, err := s.db.ExecContext(ctx, `UPDATE deploy_data_copies SET state = 'restoring' WHERE id = ? AND state = 'waiting'`, id)
	if err != nil {
		return false, err
	}
	affected, _ := result.RowsAffected()
	return affected == 1, nil
}

// DataCopyReadiness is what a finished restore records.
type DataCopyReadiness struct {
	Container, Network, Host, Database string
	Port                               int
	SecretSealed                       string
	RecoveredTo                        *time.Time
	RestoreMS                          int64
}

// DataCopyReady records a restored copy and starts its lifetime.
func (s *OrchestrationStore) DataCopyReady(ctx context.Context, id int64, ready DataCopyReadiness) (*DataCopy, error) {
	now := s.now().UTC()
	recovered := int64(0)
	if ready.RecoveredTo != nil {
		recovered = ready.RecoveredTo.UTC().Unix()
	}
	s.writeMu.Lock()
	result, err := s.db.ExecContext(ctx, `
		UPDATE deploy_data_copies SET state = 'ready', container = ?, network = ?, host = ?, port = ?, database_name = ?,
		       secret_enc = ?, recovered_to = ?, restore_ms = ?, ready_at = ?, expires_at = ?
		 WHERE id = ? AND state = 'restoring'`,
		ready.Container, ready.Network, ready.Host, ready.Port, ready.Database, ready.SecretSealed, recovered,
		ready.RestoreMS, now.Unix(), now.Add(DataCopyLifetime).Unix(), id)
	s.writeMu.Unlock()
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, ErrDataCopyEnded
	}
	return s.DataCopy(ctx, id)
}

// EndDataCopy closes a copy that is still active as failed, skipped or
// removed, with the reason; one already ended is answered ErrDataCopyEnded.
// The sealed sign-in goes with it.
func (s *OrchestrationStore) EndDataCopy(ctx context.Context, id int64, state, reason string) (*DataCopy, error) {
	if state != DataCopyFailed && state != DataCopySkipped && state != DataCopyRemoved {
		return nil, fmt.Errorf("%w: a copy ends failed, skipped or removed", ErrInvalidPlan)
	}
	s.writeMu.Lock()
	result, err := s.db.ExecContext(ctx, `
		UPDATE deploy_data_copies SET state = ?, reason = ?, ended_at = ?, secret_enc = '', expires_at = 0
		 WHERE id = ? AND state IN ('waiting', 'restoring', 'ready')`, state, truncateUTF8Prefix(reason, 600), s.now().UTC().Unix(), id)
	s.writeMu.Unlock()
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		if _, err := s.DataCopy(ctx, id); err != nil {
			return nil, err
		}
		return nil, ErrDataCopyEnded
	}
	return s.DataCopy(ctx, id)
}

// ExtendDataCopy keeps a ready copy hours longer.
func (s *OrchestrationStore) ExtendDataCopy(ctx context.Context, id int64, hours int) (*DataCopy, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	copy, err := s.DataCopy(ctx, id)
	if err != nil {
		return nil, err
	}
	if copy.State != DataCopyReady {
		return nil, ErrDataCopyNotReady
	}
	next, err := ExtendDataCopyExpiry(copy.ExpiresAt, s.now().UTC(), hours)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE deploy_data_copies SET expires_at = ? WHERE id = ? AND state = 'ready'`, next.Unix(), id); err != nil {
		return nil, err
	}
	return s.DataCopy(ctx, id)
}
