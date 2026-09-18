// Package backups runs scheduled and manual backups of files and directories
// to local disk or S3-compatible object storage (AWS S3, Backblaze B2).
//
// Provider credentials never touch the database in the clear: they are sealed
// with the dashboard's master key and opened only for the duration of a
// transfer.
package backups

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type TargetKind string

const (
	TargetLocal TargetKind = "local"
	TargetS3    TargetKind = "s3"
	TargetB2    TargetKind = "b2"
)

func (t TargetKind) Valid() bool {
	switch t {
	case TargetLocal, TargetS3, TargetB2:
		return true
	}
	return false
}

// TargetConfig is the non-secret half of a destination. Bucket and endpoint
// are useful to display; keys are held separately and sealed.
type TargetConfig struct {
	Bucket   string `json:"bucket,omitempty"`
	Region   string `json:"region,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
	Prefix   string `json:"prefix,omitempty"`
	Path     string `json:"path,omitempty"`
}

// TargetSecrets is sealed at rest and never returned by the API.
type TargetSecrets struct {
	AccessKeyID     string `json:"accessKeyId,omitempty"`
	SecretAccessKey string `json:"secretAccessKey,omitempty"`
}

type Job struct {
	ID         int64        `json:"id"`
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
	// HasCredentials tells the UI whether keys are stored without revealing
	// anything about them.
	HasCredentials bool       `json:"hasCredentials"`
	LastRun        *Run       `json:"lastRun,omitempty"`
	NextRun        *time.Time `json:"nextRun,omitempty"`
	// LastSuccessAt is when the newest successful artifact was taken, so a
	// job whose last attempt failed still says when it was last protected.
	LastSuccessAt *time.Time `json:"lastSuccessAt,omitempty"`
	// Overdue says a scheduled job has gone two intervals without a
	// successful run. It is computed by the API from the schedule and the
	// run history; a job that only runs by hand is never overdue.
	Overdue bool `json:"overdue"`
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
	if len(j.Sources) == 0 {
		return fmt.Errorf("at least one source path is required")
	}
	if !j.TargetKind.Valid() {
		return fmt.Errorf("target must be one of local, s3, b2")
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
