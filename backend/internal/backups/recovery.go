package backups

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// RecoveryPlan runs an application-owned checker against a disposable copy.
// The command receives the restored source namespaces at /restore; it never
// receives deployment credentials or access to the original source paths.
type RecoveryPlan struct {
	Image                string   `json:"image"`
	Command              []string `json:"command"`
	SchemaVersion        string   `json:"schemaVersion"`
	ExpectedOutputDigest string   `json:"expectedOutputDigest"`
	TimeoutSeconds       int      `json:"timeoutSeconds"`
	MaxBytes             int64    `json:"maxBytes"`
	Automatic            bool     `json:"automatic"`
}

func validSHA256(value string) bool {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && value == strings.ToLower(value)
}

func (p *RecoveryPlan) Validate() error {
	if p == nil {
		return nil
	}
	imageDigest := p.Image
	if _, digest, ok := strings.Cut(p.Image, "@"); ok {
		imageDigest = digest
	}
	if !validSHA256(imageDigest) || strings.ContainsAny(p.Image, " \t\r\n\x00") {
		return errors.New("recovery image must be an immutable sha256 image ID or repository digest")
	}
	if len(p.Command) == 0 || len(p.Command) > 64 || strings.TrimSpace(p.Command[0]) == "" {
		return errors.New("recovery check needs an executable and at most 63 arguments")
	}
	for _, arg := range p.Command {
		if len(arg) > 4096 || strings.ContainsRune(arg, 0) {
			return errors.New("recovery check argument is invalid")
		}
	}
	if strings.TrimSpace(p.SchemaVersion) == "" || len(p.SchemaVersion) > 128 || strings.ContainsAny(p.SchemaVersion, "\x00\r\n") {
		return errors.New("recovery check needs its application schema version")
	}
	if !validSHA256(p.ExpectedOutputDigest) {
		return errors.New("recovery check needs the SHA-256 digest of its expected canary output")
	}
	if p.ExpectedOutputDigest == "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		return errors.New("recovery check needs nonempty canary output")
	}
	if p.TimeoutSeconds == 0 {
		p.TimeoutSeconds = 60
	}
	if p.TimeoutSeconds < 1 || p.TimeoutSeconds > 300 {
		return errors.New("recovery check timeout must be between 1 and 300 seconds")
	}
	if p.MaxBytes == 0 {
		p.MaxBytes = 16 << 30
	}
	if p.MaxBytes < 1 || p.MaxBytes > 1<<40 {
		return errors.New("recovery extraction limit must be between 1 byte and 1 TiB")
	}
	return nil
}

func recoveryDigest(value any) string {
	encoded, _ := json.Marshal(value)
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func (p *RecoveryPlan) Digest() string { return recoveryDigest(p) }

type RestoreVerification struct {
	ID               int64      `json:"id"`
	RunID            int64      `json:"runId"`
	State            string     `json:"state"`
	StartedAt        time.Time  `json:"startedAt"`
	EndedAt          *time.Time `json:"endedAt,omitempty"`
	ArtifactDigest   string     `json:"artifactDigest"`
	ManifestDigest   string     `json:"manifestDigest"`
	PlanDigest       string     `json:"planDigest"`
	ApplicationImage string     `json:"applicationImage,omitempty"`
	SchemaVersion    string     `json:"schemaVersion"`
	OutputDigest     string     `json:"outputDigest,omitempty"`
	Entries          int        `json:"entries"`
	Bytes            int64      `json:"bytes"`
	CleanupComplete  bool       `json:"cleanupComplete"`
	Detail           string     `json:"detail,omitempty"`
}

type RecoveryCheckRequest struct {
	VerificationID int64
	OwnerKey       string
	Directory      string
	Plan           RecoveryPlan
}

type RecoveryCheckResult struct {
	ImageDigest  string
	OutputDigest string
}

// RecoveryChecker belongs to the runtime owner. Backups owns extraction,
// evidence, retention and recovery of interrupted verification attempts.
type RecoveryChecker interface {
	CheckRecovery(context.Context, RecoveryCheckRequest) (RecoveryCheckResult, error)
	CleanupRecovery(context.Context, int64, string) error
}

func (r *Runner) WithRecoveryChecker(checker RecoveryChecker) *Runner {
	r.recovery = checker
	return r
}

func (v *RestoreVerification) Matches(run *Run, plan *RecoveryPlan) bool {
	return v != nil && run != nil && run.Manifest != nil && plan != nil &&
		v.State == "passed" && v.CleanupComplete && v.RunID == run.ID &&
		v.ArtifactDigest == run.Manifest.ArtifactDigest && v.ManifestDigest == recoveryDigest(run.Manifest) &&
		v.PlanDigest == plan.Digest() && v.SchemaVersion == plan.SchemaVersion &&
		validSHA256(v.ApplicationImage) && v.OutputDigest == plan.ExpectedOutputDigest
}

func (s *Store) RestoreVerification(ctx context.Context, runID int64) (*RestoreVerification, error) {
	var raw string
	err := s.st.DB.QueryRowContext(ctx, `SELECT record_json FROM backup_restore_tests WHERE run_id=? ORDER BY id DESC LIMIT 1`, runID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var v RestoreVerification
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, err
	}
	return &v, nil
}

func (s *Store) saveRestoreVerification(ctx context.Context, record *RestoreVerification) error {
	_, err := s.st.DB.ExecContext(ctx, `UPDATE backup_restore_tests SET state=?,record_json=? WHERE id=?`, record.State, encodeJSON(record), record.ID)
	return err
}

var ErrRecoveryUnavailable = errors.New("isolated restore verification is unavailable")

func (r *Runner) VerifyRestore(ctx context.Context, runID int64) (record *RestoreVerification, returnedErr error) {
	if r.recovery == nil {
		return nil, ErrRecoveryUnavailable
	}
	if !r.restoreMu.TryLock() {
		return nil, ErrAlreadyRunning
	}
	defer r.restoreMu.Unlock()
	var pending int
	if err := r.store.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM backup_restore_tests WHERE state IN ('running','cleanup_failed')`).Scan(&pending); err != nil {
		return nil, err
	}
	if pending != 0 {
		if err := r.recoverRestoreChecks(ctx); err != nil {
			return nil, errors.New("an interrupted restore check needs cleanup before another can start")
		}
	}
	run, path, release, err := r.localArtifact(ctx, runID)
	if err != nil {
		return nil, err
	}
	defer release()
	job, err := r.store.Get(ctx, run.JobID)
	if err != nil {
		return nil, err
	}
	if job.Recovery == nil {
		return nil, errors.New("configure an application recovery check on this backup job first")
	}
	if err := job.Recovery.Validate(); err != nil {
		return nil, err
	}
	if run.Manifest == nil || !run.Manifest.Complete || len(run.Manifest.Excludes) != 0 {
		return nil, errors.New("restore verification needs a complete, unfiltered archive manifest")
	}
	if err := os.MkdirAll(r.stage, 0o700); err != nil {
		return nil, err
	}
	ownerKey := rand.Text()
	workspace := filepath.Join(r.stage, "restore-check-"+ownerKey)
	record = &RestoreVerification{RunID: runID, State: "running", StartedAt: time.Now().UTC(),
		ArtifactDigest: run.Manifest.ArtifactDigest, ManifestDigest: recoveryDigest(run.Manifest),
		PlanDigest: job.Recovery.Digest(), SchemaVersion: job.Recovery.SchemaVersion}
	result, err := r.store.st.DB.ExecContext(ctx, `INSERT INTO backup_restore_tests(run_id,state,owner_key,workspace,record_json) VALUES(?,'running',?,?,?)`, runID, ownerKey, workspace, encodeJSON(record))
	if err != nil {
		return nil, err
	}
	record.ID, err = result.LastInsertId()
	if err != nil {
		return nil, err
	}
	defer func() {
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		cleanupErr := r.recovery.CleanupRecovery(finishCtx, record.ID, ownerKey)
		if cleanupErr == nil {
			cleanupErr = removeRecoveryWorkspace(r.stage, workspace, ownerKey)
		}
		record.CleanupComplete = cleanupErr == nil
		if cleanupErr != nil {
			record.State, record.Detail = "cleanup_failed", "temporary restore resources need cleanup; verification is not accepted"
			returnedErr = errors.Join(returnedErr, errors.New(record.Detail))
		} else if returnedErr != nil {
			record.State = "failed"
			if record.Detail == "" {
				record.Detail = "restore or application canary check failed"
			}
		} else {
			record.State, record.Detail = "passed", "the application verified the restored canary and temporary resources were removed"
		}
		ended := time.Now().UTC()
		record.EndedAt = &ended
		persistCtx, persistCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer persistCancel()
		returnedErr = errors.Join(returnedErr, r.store.saveRestoreVerification(persistCtx, record))
	}()
	if err := r.store.saveRestoreVerification(ctx, record); err != nil {
		return record, err
	}
	if err := os.Mkdir(workspace, 0o700); err != nil {
		return record, err
	}
	// Copy and re-hash into private staging. A replaced local artifact cannot
	// change the bytes tested after its initial integrity check.
	archive := filepath.Join(workspace, "archive.tar.gz")
	if err := copyRecoveryArtifact(ctx, path, archive, record.ArtifactDigest, job.Recovery.MaxBytes); err != nil {
		record.Detail = "the backup artifact changed or could not be read"
		return record, err
	}
	directory := filepath.Join(workspace, "data")
	if err := os.Mkdir(directory, 0o755); err != nil {
		return record, err
	}
	restored, err := extractArchiveBounded(ctx, archive, directory, runID, job.Recovery.MaxBytes, restorePlan{})
	if err != nil {
		record.Detail = "the archive could not be restored within the configured limits"
		return record, err
	}
	record.Entries, record.Bytes = restored.Entries, restored.Bytes
	if len(restored.Skipped) != 0 {
		record.Detail = "the archive contains entries that could not be restored safely"
		return record, errors.New(record.Detail)
	}
	checkCtx, cancel := context.WithTimeout(ctx, time.Duration(job.Recovery.TimeoutSeconds)*time.Second)
	defer cancel()
	checked, err := r.recovery.CheckRecovery(checkCtx, RecoveryCheckRequest{VerificationID: record.ID, OwnerKey: ownerKey, Directory: directory, Plan: *job.Recovery})
	record.ApplicationImage, record.OutputDigest = checked.ImageDigest, checked.OutputDigest
	if err != nil {
		record.Detail = "the isolated application check did not complete successfully"
		return record, err
	}
	if !validSHA256(checked.ImageDigest) || checked.OutputDigest != job.Recovery.ExpectedOutputDigest {
		record.Detail = "the restored application did not return the expected canary"
		return record, errors.New(record.Detail)
	}
	return record, nil
}

func removeRecoveryWorkspace(stage, workspace, ownerKey string) error {
	if ownerKey == "" || strings.ContainsAny(ownerKey, "/\\.\x00") || workspace != filepath.Join(stage, "restore-check-"+ownerKey) {
		return errors.New("restore workspace ownership does not match")
	}
	return os.RemoveAll(workspace)
}

func copyRecoveryArtifact(ctx context.Context, source, destination, digest string, maxBytes int64) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	limit := maxBytes + (64 << 20)
	var available syscall.Statfs_t
	if err := syscall.Statfs(filepath.Dir(destination), &available); err != nil {
		return err
	}
	if info.Size() > limit || uint64(info.Size())+(256<<20) > available.Bavail*uint64(available.Bsize) {
		return errors.New("insufficient space or configured limit for the private archive copy")
	}
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	count, copyErr := io.Copy(out, io.LimitReader(&recoveryContextReader{ctx: ctx, reader: in}, limit+1))
	if err := errors.Join(copyErr, out.Close()); err != nil {
		return err
	}
	if count > limit {
		return errors.New("backup archive copy exceeds its configured limit")
	}
	got, err := fileDigest(ctx, destination)
	if err != nil {
		return err
	}
	if got != digest {
		return errors.New("backup artifact digest changed")
	}
	return nil
}

type recoveryContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *recoveryContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

// Recovery does not resume an interrupted check and call it successful. It
// removes only resources with the persisted ownership proof, then records failure.
func (r *Runner) RecoverRestoreChecks(ctx context.Context) error {
	if !r.restoreMu.TryLock() {
		return ErrAlreadyRunning
	}
	defer r.restoreMu.Unlock()
	return r.recoverRestoreChecks(ctx)
}

func (r *Runner) recoverRestoreChecks(ctx context.Context) error {
	rows, err := r.store.st.DB.QueryContext(ctx, `SELECT id,owner_key,workspace,record_json FROM backup_restore_tests WHERE state IN ('running','cleanup_failed')`)
	if err != nil {
		return err
	}
	type pendingCheck struct {
		id                  int64
		key, workspace, raw string
	}
	var pending []pendingCheck
	for rows.Next() {
		var item pendingCheck
		if err := rows.Scan(&item.id, &item.key, &item.workspace, &item.raw); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, item)
	}
	readErr := rows.Err()
	rows.Close()
	if readErr != nil {
		return readErr
	}
	if len(pending) > 0 && r.recovery == nil {
		return ErrRecoveryUnavailable
	}
	var errs []error
	for _, item := range pending {
		var record RestoreVerification
		if err := json.Unmarshal([]byte(item.raw), &record); err != nil {
			errs = append(errs, err)
			continue
		}
		record.ID = item.id
		cleanupErr := r.recovery.CleanupRecovery(ctx, item.id, item.key)
		if cleanupErr == nil {
			cleanupErr = removeRecoveryWorkspace(r.stage, item.workspace, item.key)
		}
		ended := time.Now().UTC()
		record.EndedAt, record.CleanupComplete = &ended, cleanupErr == nil
		record.State, record.Detail = "failed", "restore verification was interrupted; run it again"
		if cleanupErr != nil {
			record.State, record.Detail = "cleanup_failed", "temporary restore resources need cleanup"
			errs = append(errs, fmt.Errorf("restore verification %d needs cleanup", item.id))
		}
		persistCtx, persistCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		errs = append(errs, r.store.saveRestoreVerification(persistCtx, &record))
		persistCancel()
	}
	return errors.Join(errs...)
}
