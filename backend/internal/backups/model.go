// Package backups runs scheduled and manual backups of files and directories
// to local disk, S3-compatible object storage (AWS S3, Backblaze B2), an SFTP
// server, or any storage service rclone speaks.
//
// Provider credentials never touch the database in the clear: they are sealed
// with the dashboard's master key and opened only for the duration of a
// transfer. So is the backup key a job encrypts its archives with
// (encryption.go).
package backups

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type TargetKind string

const (
	TargetLocal  TargetKind = "local"
	TargetS3     TargetKind = "s3"
	TargetB2     TargetKind = "b2"
	TargetSFTP   TargetKind = "sftp"
	TargetRclone TargetKind = "rclone"
)

func (t TargetKind) Valid() bool {
	switch t {
	case TargetLocal, TargetS3, TargetB2, TargetSFTP, TargetRclone:
		return true
	}
	return false
}

// Remote reports a destination off this server, reached with credentials.
func (t TargetKind) Remote() bool {
	return t.Valid() && t != TargetLocal
}

// TargetConfig is the non-secret half of a destination. Bucket and endpoint
// are useful to display; keys are held separately and sealed. Path is the
// directory on a local disk, on an SFTP server, or inside an rclone remote.
type TargetConfig struct {
	Bucket   string `json:"bucket,omitempty"`
	Region   string `json:"region,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
	Prefix   string `json:"prefix,omitempty"`
	Path     string `json:"path,omitempty"`
	// SFTP: the server and account, and the host key pinned the first time
	// the server was reached (an authorized_keys line and its SHA256
	// fingerprint). A later connection presenting another key is refused.
	Host               string `json:"host,omitempty"`
	Port               int    `json:"port,omitempty"`
	Username           string `json:"username,omitempty"`
	HostKey            string `json:"hostKey,omitempty"`
	HostKeyFingerprint string `json:"hostKeyFingerprint,omitempty"`
	// RemoteType is an rclone remote's backend (drive, onedrive, webdav…),
	// read from its sealed configuration so the page can name the service.
	RemoteType string `json:"remoteType,omitempty"`
}

// TargetSecrets is sealed at rest and never returned by the API.
type TargetSecrets struct {
	AccessKeyID     string `json:"accessKeyId,omitempty"`
	SecretAccessKey string `json:"secretAccessKey,omitempty"`
	// SFTP signs in with a password, a private key, or both.
	Password   string `json:"password,omitempty"`
	PrivateKey string `json:"privateKey,omitempty"`
	Passphrase string `json:"passphrase,omitempty"`
	// RcloneConfig is one remote's section as `rclone config show` prints
	// it, OAuth tokens included.
	RcloneConfig string `json:"rcloneConfig,omitempty"`
}

// Empty is a request that carries no credentials, which keeps the saved ones.
func (s *TargetSecrets) Empty() bool {
	return s == nil || (s.AccessKeyID == "" && s.SecretAccessKey == "" && s.Password == "" &&
		s.PrivateKey == "" && s.Passphrase == "" && strings.TrimSpace(s.RcloneConfig) == "")
}

type Job struct {
	ID         int64        `json:"id"`
	Revision   int64        `json:"revision"`
	Name       string       `json:"name"`
	Sources    []string     `json:"sources"`
	Excludes   []string     `json:"excludes"`
	TargetKind TargetKind   `json:"targetKind"`
	Target     TargetConfig `json:"target"`
	Schedule   string       `json:"schedule"`
	Retention  int          `json:"retention"`
	// RetentionDays prunes a successful artifact once it is older than this
	// many days, independently of the count. Zero keeps by count alone. The
	// newest successful artifact is never pruned by age: a job whose schedule
	// stopped firing must not throw away its last good backup.
	RetentionDays int           `json:"retentionDays"`
	Enabled       bool          `json:"enabled"`
	CreatedAt     time.Time     `json:"createdAt"`
	Recovery      *RecoveryPlan `json:"recovery,omitempty"`
	SQLitePaths   []string      `json:"sqlitePaths,omitempty"`
	// DatabaseDumps are saved database connections whose native dump every
	// run captures into the archive beside the filesystem sources.
	DatabaseDumps []int64 `json:"databaseDumps,omitempty"`
	// PauseContainers are Docker containers frozen for the length of the
	// archive step, so a volume they write to is captured at one instant.
	// A pause is a SIGSTOP, not a stop: the process resumes where it was and
	// no connection is dropped, which is what makes it safe to do nightly.
	PauseContainers []string `json:"pauseContainers,omitempty"`
	// Encrypt seals each new archive to the install's backup key before it
	// is written to the destination. Archives already taken keep the form
	// they were written in, so turning it on or off never orphans one.
	Encrypt bool `json:"encrypt"`
	// HasCredentials tells the UI whether keys are stored without revealing
	// anything about them.
	HasCredentials bool `json:"hasCredentials"`
	// ForgetHostKey asks an edit to drop an SFTP destination's pinned host
	// key, so the server is pinned again the next time it is reached.
	ForgetHostKey bool       `json:"-"`
	LastRun       *Run       `json:"lastRun,omitempty"`
	NextRun       *time.Time `json:"nextRun,omitempty"`
	// LastSuccessAt is when the newest successful artifact was taken, so a
	// job whose last attempt failed still says when it was last protected.
	LastSuccessAt *time.Time `json:"lastSuccessAt,omitempty"`
	// Stored counts the successful artifacts retention has kept and their
	// total size, which is what the destination is being charged for.
	Stored StoredSummary `json:"stored"`
}

// StoredSummary is the space a job's retained artifacts take.
type StoredSummary struct {
	Runs  int   `json:"runs"`
	Bytes int64 `json:"bytes"`
}

type RunStatus string

const (
	StatusRunning RunStatus = "running"
	StatusSuccess RunStatus = "success"
	StatusFailed  RunStatus = "failed"
)

type Run struct {
	ID                  int64                `json:"id"`
	JobID               int64                `json:"jobId"`
	StartedAt           time.Time            `json:"startedAt"`
	EndedAt             *time.Time           `json:"endedAt,omitempty"`
	Status              RunStatus            `json:"status"`
	Artifact            string               `json:"artifact"`
	SizeBytes           int64                `json:"sizeBytes"`
	Log                 string               `json:"log"`
	Trigger             string               `json:"trigger"`
	Duration            string               `json:"duration,omitempty"`
	Manifest            *Manifest            `json:"manifest,omitempty"`
	RestoreVerification *RestoreVerification `json:"restoreVerification,omitempty"`
}

func encodeJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func decodeStrings(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		// Older rows and hand-edited values may be comma separated.
		for _, p := range strings.Split(raw, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	if out == nil {
		out = []string{}
	}
	return out
}

// Validate catches configuration that would only fail later, at 3am, in a
// scheduled run nobody is watching.
func (j *Job) Validate() error {
	if err := j.Recovery.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(j.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if len(j.Sources) == 0 && len(j.DatabaseDumps) == 0 {
		return fmt.Errorf("at least one source path or database dump is required")
	}
	if !j.TargetKind.Valid() {
		return fmt.Errorf("target must be one of local, s3, b2, sftp, rclone")
	}
	switch j.TargetKind {
	case TargetLocal:
		if strings.TrimSpace(j.Target.Path) == "" {
			return fmt.Errorf("a local target needs a destination path")
		}
	case TargetS3, TargetB2:
		if strings.TrimSpace(j.Target.Bucket) == "" {
			return fmt.Errorf("an object storage target needs a bucket")
		}
		if j.TargetKind == TargetB2 && strings.TrimSpace(j.Target.Endpoint) == "" {
			return fmt.Errorf("Backblaze B2 needs its S3-compatible endpoint, for example s3.us-west-004.backblazeb2.com")
		}
	case TargetSFTP:
		j.Target.Host = strings.TrimSpace(j.Target.Host)
		j.Target.Username = strings.TrimSpace(j.Target.Username)
		j.Target.Path = strings.TrimSpace(j.Target.Path)
		if !validSFTPHost(j.Target.Host) {
			return fmt.Errorf("an SFTP destination needs a host name or address, without a scheme or path")
		}
		if j.Target.Port == 0 {
			j.Target.Port = 22
		}
		if j.Target.Port < 1 || j.Target.Port > 65535 {
			return fmt.Errorf("the SFTP port must be between 1 and 65535")
		}
		if j.Target.Username == "" || strings.ContainsAny(j.Target.Username, " \t\r\n\x00@:") {
			return fmt.Errorf("an SFTP destination needs the account to sign in as")
		}
		if j.Target.Path == "" || !validRemotePath(j.Target.Path) {
			return fmt.Errorf("an SFTP destination needs the directory archives are written to")
		}
	case TargetRclone:
		j.Target.Path = strings.Trim(strings.TrimSpace(j.Target.Path), "/")
		if !validRemotePath(j.Target.Path) {
			return fmt.Errorf("the folder inside the rclone remote is not a usable path")
		}
	}
	if j.Retention < 0 {
		return fmt.Errorf("retention cannot be negative")
	}
	if j.RetentionDays < 0 {
		return fmt.Errorf("retention days cannot be negative")
	}
	if err := ValidateSchedule(j.Schedule); err != nil {
		return fmt.Errorf("schedule is not a valid cron expression: %w", err)
	}
	for i, name := range j.PauseContainers {
		name = strings.TrimSpace(name)
		if name == "" || strings.HasPrefix(name, "-") || strings.ContainsAny(name, " \t\n/") {
			return fmt.Errorf("container %q is not a container name or id", name)
		}
		j.PauseContainers[i] = name
	}
	return nil
}
