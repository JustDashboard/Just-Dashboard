package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// The records point-in-time recovery keeps: each archive, the restore points
// named in it, and the copies recovered from it (db_wal_archives,
// db_wal_restore_points and db_wal_restores in the store's schema).

// pitrArchive is one server's archive. The destination's keys and the private
// half of the encryption key are sealed and never leave this file except into
// a WAL-G configuration.
type pitrArchive struct {
	ID           int64                `json:"id"`
	Container    string               `json:"container"`
	ConnectionID int64                `json:"connectionId"`
	Stream       string               `json:"stream"`
	TargetKind   backups.TargetKind   `json:"targetKind"`
	Target       backups.TargetConfig `json:"target"`
	// HasCredentials says keys are stored without saying anything about them.
	HasCredentials bool `json:"hasCredentials"`
	// KeyFingerprint names the key the archive is encrypted with.
	KeyFingerprint  string       `json:"keyFingerprint"`
	Schedule        string       `json:"schedule"`
	RetentionDays   int          `json:"retentionDays"`
	Image           string       `json:"image"`
	CreatedAt       time.Time    `json:"createdAt"`
	CreatedBy       string       `json:"createdBy,omitempty"`
	EndedAt         *time.Time   `json:"endedAt,omitempty"`
	Reading         *pitrReading `json:"reading,omitempty"`
	LastBackupAt    *time.Time   `json:"lastBackupAt,omitempty"`
	LastBackupError string       `json:"lastBackupError,omitempty"`

	secretsEnc, publicKey, privateKeyEnc, tool string
}

// pitrReading is what an archive held when it was last listed.
type pitrReading struct {
	Backups []dbx.WALBackup `json:"backups"`
	// Latest is the newest whole segment of log the archive holds, and when
	// it arrived there.
	Latest   string        `json:"latest,omitempty"`
	LatestAt *time.Time    `json:"latestAt,omitempty"`
	Window   dbx.WALWindow `json:"window"`
	// Size is everything the archive keeps in the destination, backups and
	// log together.
	Size   int64     `json:"size"`
	ReadAt time.Time `json:"readAt"`
}

const pitrArchiveCols = `id, container, connection_id, stream, target_kind, target_cfg, secrets_enc, public_key,
 private_key_enc, schedule, retention_days, image, tool, created_at, created_by, ended_at, reading,
 last_backup_at, last_backup_error`

func scanPITRArchive(row interface{ Scan(...any) error }) (*pitrArchive, error) {
	var (
		a                          pitrArchive
		kind, cfg, reading         string
		created, ended, lastBackup int64
	)
	if err := row.Scan(&a.ID, &a.Container, &a.ConnectionID, &a.Stream, &kind, &cfg, &a.secretsEnc, &a.publicKey,
		&a.privateKeyEnc, &a.Schedule, &a.RetentionDays, &a.Image, &a.tool, &created, &a.CreatedBy, &ended, &reading,
		&lastBackup, &a.LastBackupError); err != nil {
		return nil, err
	}
	a.TargetKind = backups.TargetKind(kind)
	if err := json.Unmarshal([]byte(cfg), &a.Target); err != nil {
		return nil, errors.New("an archive's destination is malformed")
	}
	a.HasCredentials = a.secretsEnc != ""
	a.KeyFingerprint = dbx.WALKeyFingerprint(a.publicKey)
	a.CreatedAt = time.Unix(created, 0).UTC()
	if ended > 0 {
		at := time.Unix(ended, 0).UTC()
		a.EndedAt = &at
	}
	if lastBackup > 0 {
		at := time.Unix(lastBackup, 0).UTC()
		a.LastBackupAt = &at
	}
	if reading != "" {
		var r pitrReading
		if err := json.Unmarshal([]byte(reading), &r); err == nil {
			a.Reading = &r
		}
	}
	return &a, nil
}

var errNoPITRArchive = httpx.Err(http.StatusNotFound, "not_found", "no such archive")

// pitrArchivesOf is every archive kept for a server, the one it writes into
// first, then the newest.
func (s *Server) pitrArchivesOf(ctx context.Context, container string) ([]*pitrArchive, error) {
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT `+pitrArchiveCols+` FROM db_wal_archives WHERE container = ?
		ORDER BY ended_at = 0 DESC, created_at DESC, id DESC`, container)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*pitrArchive{}
	for rows.Next() {
		a, err := scanPITRArchive(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Server) pitrArchiveByID(ctx context.Context, id int64) (*pitrArchive, error) {
	a, err := scanPITRArchive(s.Store.DB.QueryRowContext(ctx, `SELECT `+pitrArchiveCols+` FROM db_wal_archives WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNoPITRArchive
	}
	return a, err
}

// activePITRArchives is every archive a server is writing into, for the
// schedule.
func (s *Server) activePITRArchives(ctx context.Context) ([]*pitrArchive, error) {
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT `+pitrArchiveCols+` FROM db_wal_archives WHERE ended_at = 0 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*pitrArchive{}
	for rows.Next() {
		a, err := scanPITRArchive(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// sealPITRArchive seals an archive's destination keys and the private half
// of its encryption key into the record, as a backup job's keys are sealed.
func (s *Server) sealPITRArchive(a *pitrArchive, secrets *backups.TargetSecrets, private string) error {
	a.secretsEnc = ""
	if secrets != nil && (secrets.AccessKeyID != "" || secrets.SecretAccessKey != "") {
		raw, _ := json.Marshal(secrets)
		sealed, err := s.Sealer.Seal(string(raw))
		if err != nil {
			return err
		}
		a.secretsEnc = sealed
	}
	sealed, err := s.Sealer.Seal(private)
	if err != nil {
		return err
	}
	a.privateKeyEnc = sealed
	return nil
}

// insertPITRArchive records an archive a server is about to write into, its
// keys already sealed.
func (s *Server) insertPITRArchive(ctx context.Context, a *pitrArchive) (int64, error) {
	cfg, _ := json.Marshal(a.Target)
	res, err := s.Store.DB.ExecContext(ctx, `INSERT INTO db_wal_archives(container, connection_id, stream, target_kind,
		target_cfg, secrets_enc, public_key, private_key_enc, schedule, retention_days, image, tool, created_at, created_by)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.Container, a.ConnectionID, a.Stream, string(a.TargetKind), string(cfg), a.secretsEnc, a.publicKey, a.privateKeyEnc,
		a.Schedule, a.RetentionDays, a.Image, a.tool, time.Now().Unix(), a.CreatedBy)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// pitrSecrets opens an archive's destination keys for the length of one
// configuration.
func (s *Server) pitrSecrets(a *pitrArchive) (*backups.TargetSecrets, error) {
	if a.secretsEnc == "" {
		return &backups.TargetSecrets{}, nil
	}
	plain, err := s.Sealer.Open(a.secretsEnc)
	if err != nil {
		return nil, err
	}
	var secrets backups.TargetSecrets
	if err := json.Unmarshal([]byte(plain), &secrets); err != nil {
		return nil, err
	}
	return &secrets, nil
}

func (s *Server) pitrPrivateKey(a *pitrArchive) (string, error) {
	return s.Sealer.Open(a.privateKeyEnc)
}

func (s *Server) endPITRArchive(ctx context.Context, id int64) error {
	_, err := s.Store.DB.ExecContext(ctx, `UPDATE db_wal_archives SET ended_at = ? WHERE id = ? AND ended_at = 0`, time.Now().Unix(), id)
	return err
}

func (s *Server) deletePITRArchive(ctx context.Context, id int64) error {
	_, err := s.Store.DB.ExecContext(ctx, `DELETE FROM db_wal_archives WHERE id = ?`, id)
	return err
}

func (s *Server) updatePITRSchedule(ctx context.Context, id int64, schedule string, days int) error {
	_, err := s.Store.DB.ExecContext(ctx, `UPDATE db_wal_archives SET schedule = ?, retention_days = ? WHERE id = ?`, schedule, days, id)
	return err
}

func (s *Server) recordPITRReading(ctx context.Context, id int64, reading *pitrReading) error {
	raw, err := json.Marshal(reading)
	if err != nil {
		return err
	}
	_, err = s.Store.DB.ExecContext(ctx, `UPDATE db_wal_archives SET reading = ?, read_at = ? WHERE id = ?`,
		string(raw), reading.ReadAt.Unix(), id)
	return err
}

// recordPITRBackup keeps how the last base backup went: when one last
// succeeded, and why the last attempt failed if it did.
func (s *Server) recordPITRBackup(ctx context.Context, id int64, failure error) error {
	if failure != nil {
		_, err := s.Store.DB.ExecContext(ctx, `UPDATE db_wal_archives SET last_backup_error = ? WHERE id = ?`, failure.Error(), id)
		return err
	}
	_, err := s.Store.DB.ExecContext(ctx, `UPDATE db_wal_archives SET last_backup_at = ?, last_backup_error = '' WHERE id = ?`,
		time.Now().Unix(), id)
	return err
}

// pitrRestorePoint is a moment named in an archive's log.
type pitrRestorePoint struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	LSN       string    `json:"lsn"`
	CreatedAt time.Time `json:"createdAt"`
	CreatedBy string    `json:"createdBy,omitempty"`
}

func (s *Server) pitrRestorePoints(ctx context.Context, archive int64) ([]pitrRestorePoint, error) {
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT id, name, lsn, created_at, created_by FROM db_wal_restore_points
		WHERE archive_id = ? ORDER BY created_at DESC, id DESC`, archive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []pitrRestorePoint{}
	for rows.Next() {
		var p pitrRestorePoint
		var at int64
		if err := rows.Scan(&p.ID, &p.Name, &p.LSN, &at, &p.CreatedBy); err != nil {
			return nil, err
		}
		p.CreatedAt = time.Unix(at, 0).UTC()
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Server) insertPITRRestorePoint(ctx context.Context, archive int64, name, lsn, by string) (*pitrRestorePoint, error) {
	now := time.Now().UTC()
	res, err := s.Store.DB.ExecContext(ctx, `INSERT INTO db_wal_restore_points(archive_id, name, lsn, created_at, created_by)
		VALUES(?,?,?,?,?)`, archive, name, lsn, now.Unix(), by)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &pitrRestorePoint{ID: id, Name: name, LSN: lsn, CreatedAt: now.Truncate(time.Second), CreatedBy: by}, nil
}

// pitrRestore is one copy recovered from an archive.
type pitrRestore struct {
	ID           int64              `json:"id"`
	ArchiveID    int64              `json:"archiveId"`
	Source       string             `json:"source"`
	Container    string             `json:"container"`
	ConnectionID int64              `json:"connectionId,omitempty"`
	Port         int                `json:"port"`
	Target       dbx.RecoveryTarget `json:"target"`
	BaseBackup   string             `json:"baseBackup"`
	RecoveredTo  *time.Time         `json:"recoveredTo,omitempty"`
	// State is "ready" — a server of its own beside the one it was recovered
	// from — or "switched", in that server's place.
	State      string     `json:"state"`
	Replaced   string     `json:"replaced,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	CreatedBy  string     `json:"createdBy,omitempty"`
	SwitchedAt *time.Time `json:"switchedAt,omitempty"`
}

const pitrRestoreCols = `id, archive_id, source, container, connection_id, port, target, base_backup, recovered_to,
 state, replaced, created_at, created_by, switched_at`

func scanPITRRestore(row interface{ Scan(...any) error }) (*pitrRestore, error) {
	var (
		r                            pitrRestore
		target                       string
		recovered, created, switched int64
	)
	if err := row.Scan(&r.ID, &r.ArchiveID, &r.Source, &r.Container, &r.ConnectionID, &r.Port, &target, &r.BaseBackup,
		&recovered, &r.State, &r.Replaced, &created, &r.CreatedBy, &switched); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(target), &r.Target)
	if recovered > 0 {
		at := time.Unix(recovered, 0).UTC()
		r.RecoveredTo = &at
	}
	r.CreatedAt = time.Unix(created, 0).UTC()
	if switched > 0 {
		at := time.Unix(switched, 0).UTC()
		r.SwitchedAt = &at
	}
	return &r, nil
}

// pitrRestoresOf is every copy recovered from one of a server's archives, or
// switched in for it, newest first.
func (s *Server) pitrRestoresOf(ctx context.Context, container string) ([]*pitrRestore, error) {
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT `+pitrRestoreCols+` FROM db_wal_restores
		WHERE source = ? OR container = ? ORDER BY created_at DESC, id DESC`, container, container)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*pitrRestore{}
	for rows.Next() {
		r, err := scanPITRRestore(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Server) pitrRestoreByID(ctx context.Context, id int64) (*pitrRestore, error) {
	r, err := scanPITRRestore(s.Store.DB.QueryRowContext(ctx, `SELECT `+pitrRestoreCols+` FROM db_wal_restores WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.Err(http.StatusNotFound, "not_found", "no such restored copy")
	}
	return r, err
}

func (s *Server) insertPITRRestore(ctx context.Context, r *pitrRestore) (int64, error) {
	target, _ := json.Marshal(r.Target)
	recovered := int64(0)
	if r.RecoveredTo != nil {
		recovered = r.RecoveredTo.Unix()
	}
	res, err := s.Store.DB.ExecContext(ctx, `INSERT INTO db_wal_restores(archive_id, source, container, connection_id, port,
		target, base_backup, recovered_to, state, created_at, created_by) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		r.ArchiveID, r.Source, r.Container, r.ConnectionID, r.Port, string(target), r.BaseBackup, recovered, "ready",
		time.Now().Unix(), r.CreatedBy)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Server) markPITRRestoreSwitched(ctx context.Context, id int64, container, replaced string) error {
	_, err := s.Store.DB.ExecContext(ctx, `UPDATE db_wal_restores SET state = 'switched', container = ?, replaced = ?,
		connection_id = 0, switched_at = ? WHERE id = ?`, container, replaced, time.Now().Unix(), id)
	return err
}

func (s *Server) deletePITRRestore(ctx context.Context, id int64) error {
	_, err := s.Store.DB.ExecContext(ctx, `DELETE FROM db_wal_restores WHERE id = ?`, id)
	return err
}
