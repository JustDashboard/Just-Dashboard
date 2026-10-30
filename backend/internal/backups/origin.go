package backups

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"os"
	"strings"
	"time"
)

// installIDSetting holds a random identity made once per install, which every
// manifest written since carries as its origin. It is how a catalogue of a
// destination tells another server's archives from this one's, and how the
// archives of one server are grouped when several write to one bucket. It
// says nothing about the machine: a host name can be reused and an address
// moves, a random value made here does neither.
const installIDSetting = "backups.install-id"

// Origin is where an archive was taken: the install that wrote it, read by
// its host name, the job and the run there, and when the run started.
type Origin struct {
	ID        string    `json:"id"`
	Host      string    `json:"host,omitempty"`
	JobID     int64     `json:"jobId"`
	JobName   string    `json:"jobName"`
	RunID     int64     `json:"runId"`
	StartedAt time.Time `json:"startedAt"`
	// Recovery is the job's application recovery check when the archive was
	// taken, so a server that imports it can run the same check against it.
	Recovery *RecoveryPlan `json:"recovery,omitempty"`
}

// JobOrigin is what an imported job was on the server it came from, and
// where this server reads its archives.
type JobOrigin struct {
	ID      string `json:"id"`
	Host    string `json:"host,omitempty"`
	JobID   int64  `json:"jobId"`
	JobName string `json:"jobName"`
	// ThisServer is an origin that is this install: archives it wrote whose
	// records it no longer holds, because their job was deleted or the
	// server was rebuilt from an older copy of its data.
	ThisServer bool `json:"thisServer,omitempty"`
}

// InstallID answers this install's identity, making it the first time it is
// asked. Two callers at once share one: the insert does nothing when a row is
// already there, and both then read it.
func (s *Store) InstallID(ctx context.Context) (string, error) {
	if s.st == nil {
		return "", errors.New("the backup store has no database")
	}
	var id string
	err := s.st.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, installIDSetting).Scan(&id)
	if err == nil && id != "" {
		return id, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if _, err := s.st.DB.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO NOTHING`,
		installIDSetting, strings.ToLower(rand.Text())); err != nil {
		return "", err
	}
	if err := s.st.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, installIDSetting).Scan(&id); err != nil {
		return "", err
	}
	return id, nil
}

// hostName is the name an origin is read by. The shipped Compose file runs the
// backend on the host's network, where a container has the host's own name.
func hostName() string {
	name, err := os.Hostname()
	if err != nil {
		return ""
	}
	return name
}

// origin is the manifest's record of a run about to be taken.
func (s *Store) origin(ctx context.Context, job *Job, runID int64, started time.Time) (*Origin, error) {
	id, err := s.InstallID(ctx)
	if err != nil {
		return nil, err
	}
	return &Origin{ID: id, Host: hostName(), JobID: job.ID, JobName: job.Name, RunID: runID,
		StartedAt: started.UTC().Truncate(time.Second), Recovery: job.Recovery}, nil
}
