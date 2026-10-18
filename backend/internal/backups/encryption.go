package backups

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"strings"
	"time"

	"filippo.io/age"
)

// A job that encrypts seals each archive to this install's backup key before
// it is written to its destination, in the age format (age-encryption.org/v1):
// the gzip-compressed tar goes through ChaCha20-Poly1305 in 64 KiB chunks,
// each authenticated on its own, with a last-chunk flag that makes a truncated
// file an error rather than a shorter archive. age rather than a format of the
// dashboard's own, because the reason to export the key is to open an archive
// where this dashboard is not, and `age --decrypt` is packaged for every system
// such an archive is likely to end up on.
//
// The key is one X25519 identity for the whole install, made the first time a
// job asks for it and sealed with the master key. One key rather than one per
// job, because deleting a job leaves its archives where they are, and a key
// that went with the job would leave them unreadable; and because a recovery
// plan the operator keeps somewhere safe should be one file, not one per job.

const (
	archiveKeySetting = "backups.archive-key"
	archiveFormatAge  = "age"
	// encryptedSuffix is added to an encrypted artifact's name, so a listing
	// of the bucket says which objects need the key before one is opened.
	encryptedSuffix = ".age"
)

var (
	// ErrNoArchiveKey is a read of an encrypted archive on an install that
	// holds no key. A new key is never made for it: it would not open the file.
	ErrNoArchiveKey = errors.New("this server holds no backup key, so it cannot open an encrypted archive; decrypt it with the exported key instead")
	// ErrArchiveKey is an encrypted archive sealed to some other key.
	ErrArchiveKey = errors.New("this archive was encrypted with a backup key this server does not hold; decrypt it with the key that was exported where it was taken")
)

// ArchiveEncryption is a manifest's record of how its archive was sealed.
//
// The manifest's artifactDigest is over the bytes at the destination — the
// ciphertext — because that is what retention, restores and recovery checks
// prove before anything is read. PlaintextDigest is over the .tar.gz inside,
// which is what an archive decrypted outside the dashboard can be checked
// against with sha256sum.
type ArchiveEncryption struct {
	Format string `json:"format"`
	// Recipient is the public half of the key it was sealed to, as an
	// exported key file's "public key" line prints it.
	Recipient       string `json:"recipient"`
	PlaintextDigest string `json:"plaintextDigest"`
	PlaintextBytes  int64  `json:"plaintextBytes"`
}

// storedArchiveKey is the settings row. Identity is sealed with the master
// key; the rest is public and lets the key be described without opening it.
type storedArchiveKey struct {
	Identity   string     `json:"identity"`
	Recipient  string     `json:"recipient"`
	CreatedAt  time.Time  `json:"createdAt"`
	ExportedAt *time.Time `json:"exportedAt,omitempty"`
}

// ArchiveKey is what the API says about the backup key, never its secret half.
type ArchiveKey struct {
	Exists     bool       `json:"exists"`
	Recipient  string     `json:"recipient,omitempty"`
	CreatedAt  *time.Time `json:"createdAt,omitempty"`
	ExportedAt *time.Time `json:"exportedAt,omitempty"`
	// What depends on it: the jobs encrypting now and the archives retention
	// still keeps that only this key opens.
	EncryptingJobs    int `json:"encryptingJobs"`
	EncryptedArchives int `json:"encryptedArchives"`
}

func (s *Store) storedArchiveKey(ctx context.Context) (*storedArchiveKey, error) {
	var raw string
	err := s.st.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, archiveKeySetting).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var stored storedArchiveKey
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return nil, errors.New("the stored backup key is malformed")
	}
	return &stored, nil
}

// archiveIdentity opens the install's backup key. With create, a missing key
// is made first; a read never makes one.
func (s *Store) archiveIdentity(ctx context.Context, create bool) (*age.X25519Identity, *storedArchiveKey, error) {
	stored, err := s.storedArchiveKey(ctx)
	if err != nil {
		return nil, nil, err
	}
	if stored == nil {
		if !create {
			return nil, nil, ErrNoArchiveKey
		}
		if err := s.createArchiveKey(ctx); err != nil {
			return nil, nil, err
		}
		if stored, err = s.storedArchiveKey(ctx); err != nil {
			return nil, nil, err
		}
	}
	plain, err := s.sealer.Open(stored.Identity)
	if err != nil {
		return nil, nil, errors.New("the backup key cannot be unsealed with this server's master key")
	}
	identity, err := age.ParseX25519Identity(plain)
	if err != nil {
		return nil, nil, errors.New("the stored backup key is malformed")
	}
	return identity, stored, nil
}

// createArchiveKey inserts a new key unless one is already there, so two jobs
// encrypting for the first time at once still end up sharing a single key.
func (s *Store) createArchiveKey(ctx context.Context) error {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		return err
	}
	sealed, err := s.sealer.Seal(identity.String())
	if err != nil {
		return err
	}
	value := encodeJSON(storedArchiveKey{Identity: sealed, Recipient: identity.Recipient().String(), CreatedAt: time.Now().UTC().Truncate(time.Second)})
	_, err = s.st.DB.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO NOTHING`, archiveKeySetting, value)
	return err
}

// ensureArchiveKey makes the key when a job that encrypts is saved, so it can
// be exported before that job's first run rather than after.
func (s *Store) ensureArchiveKey(ctx context.Context, j *Job) error {
	if !j.Encrypt {
		return nil
	}
	_, _, err := s.archiveIdentity(ctx, true)
	return err
}

// ArchiveKeyStatus describes the backup key without opening it.
func (s *Store) ArchiveKeyStatus(ctx context.Context) (*ArchiveKey, error) {
	stored, err := s.storedArchiveKey(ctx)
	if err != nil {
		return nil, err
	}
	status := &ArchiveKey{}
	if stored != nil {
		status.Exists, status.Recipient, status.ExportedAt = true, stored.Recipient, stored.ExportedAt
		created := stored.CreatedAt
		status.CreatedAt = &created
	}
	if err := s.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM backup_jobs WHERE encrypt = 1`).Scan(&status.EncryptingJobs); err != nil {
		return nil, err
	}
	err = s.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM backup_runs WHERE status = ? AND artifact != ''
		AND json_extract(manifest_json, '$.encryption.format') IS NOT NULL`, string(StatusSuccess)).Scan(&status.EncryptedArchives)
	if err != nil {
		return nil, err
	}
	return status, nil
}

// ExportArchiveKey returns the backup key as an age identity file — making the
// key first if no job has asked for one yet — and records when it left.
func (s *Store) ExportArchiveKey(ctx context.Context) (string, []byte, error) {
	identity, stored, err := s.archiveIdentity(ctx, true)
	if err != nil {
		return "", nil, err
	}
	recipient := identity.Recipient().String()
	file := fmt.Sprintf(`# Just Dashboard backup key
# created: %s
# public key: %s
#
# Opens every archive this server encrypted (names ending in .tar.gz.age).
# Keep it away from the server and its backups. Without the dashboard:
#   age --decrypt --identity this-file.txt ARCHIVE.tar.gz.age > ARCHIVE.tar.gz
%s
`, stored.CreatedAt.Format(time.RFC3339), recipient, identity.String())
	_, err = s.st.DB.ExecContext(ctx, `UPDATE settings SET value = json_set(value, '$.exportedAt', ?) WHERE key = ?`,
		time.Now().UTC().Truncate(time.Second).Format(time.RFC3339), archiveKeySetting)
	if err != nil {
		return "", nil, err
	}
	return recipient, []byte(file), nil
}

// archiveSeal encrypts an archive while it is written and keeps the digest of
// what went in, which becomes the manifest's plaintext evidence.
type archiveSeal struct {
	recipient *age.X25519Recipient
	digest    hash.Hash
	bytes     int64
}

func newArchiveSeal(identity *age.X25519Identity) *archiveSeal {
	return &archiveSeal{recipient: identity.Recipient(), digest: sha256.New()}
}

func (s *archiveSeal) Write(p []byte) (int, error) {
	s.bytes += int64(len(p))
	return s.digest.Write(p)
}

// evidence is nil for an archive written in the clear.
func (s *archiveSeal) evidence() *ArchiveEncryption {
	if s == nil {
		return nil
	}
	return &ArchiveEncryption{Format: archiveFormatAge, Recipient: s.recipient.String(),
		PlaintextDigest: "sha256:" + hex.EncodeToString(s.digest.Sum(nil)), PlaintextBytes: s.bytes}
}

// ArchiveFile is a run's verified artifact on local disk and what opens it.
type ArchiveFile struct {
	Path string
	// identity is nil for an archive written in the clear.
	identity age.Identity
}

// Encrypted says whether Open decrypts.
func (a ArchiveFile) Encrypted() bool { return a.identity != nil }

// Open streams the .tar.gz the artifact holds, decrypting a sealed one as it
// is read.
func (a ArchiveFile) Open() (io.ReadCloser, error) {
	f, err := os.Open(a.Path)
	if err != nil {
		return nil, err
	}
	if a.identity == nil {
		return f, nil
	}
	plain, err := age.Decrypt(f, a.identity)
	if err != nil {
		f.Close()
		var mismatch *age.NoIdentityMatchError
		if errors.As(err, &mismatch) {
			return nil, ErrArchiveKey
		}
		return nil, fmt.Errorf("the encrypted archive is damaged or was altered: %w", err)
	}
	return struct {
		io.Reader
		io.Closer
	}{authenticated{plain}, f}, nil
}

// authenticated names a chunk that fails authentication for what it means.
type authenticated struct{ reader io.Reader }

func (a authenticated) Read(p []byte) (int, error) {
	n, err := a.reader.Read(p)
	if err != nil && err != io.EOF {
		err = fmt.Errorf("the encrypted archive is damaged or was altered: %w", err)
	}
	return n, err
}

// entries opens the archive's tar stream. The release closes it.
func (a ArchiveFile) entries() (*tar.Reader, func(), error) {
	raw, err := a.Open()
	if err != nil {
		return nil, nil, err
	}
	gz, err := gzip.NewReader(raw)
	if err != nil {
		raw.Close()
		return nil, nil, err
	}
	return tar.NewReader(gz), func() {
		gz.Close()
		raw.Close()
	}, nil
}

// archiveFile pairs a verified artifact with the key its manifest says it was
// sealed to. A key that is not this install's is refused here, by name,
// rather than as a decryption failure halfway into a restore.
func (r *Runner) archiveFile(ctx context.Context, run *Run, path string) (ArchiveFile, error) {
	file := ArchiveFile{Path: path}
	if run.Manifest == nil || run.Manifest.Encryption == nil {
		return file, nil
	}
	encryption := run.Manifest.Encryption
	if encryption.Format != archiveFormatAge {
		return file, fmt.Errorf("archive encryption %q is not one this dashboard reads", encryption.Format)
	}
	identity, _, err := r.store.archiveIdentity(ctx, false)
	if err != nil {
		return file, err
	}
	if identity.Recipient().String() != strings.TrimSpace(encryption.Recipient) {
		return file, ErrArchiveKey
	}
	file.identity = identity
	return file, nil
}
