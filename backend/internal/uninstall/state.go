package uninstall

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Where an uninstall in progress keeps its state.
//
// The shape internal/selfupdate and internal/selfcfg already use, for the
// same reason: the stack this describes is taken down halfway through, so
// the record is a file both halves can read — the backend that starts the
// work and the sibling container that finishes it. Unlike those two, nothing
// comes back up afterwards to read the end of it, which is why the transcript
// is also written to DurableLog, outside the data directory a wipe deletes.
const (
	StateFile  = "self-uninstall.json"
	LogFile    = "self-uninstall.log"
	DurableLog = "/var/log/just-dashboard-uninstall.log"
	// KeptEnv is where a kept install leaves a copy of its .env. The key in
	// it is the only thing that can read the secrets in the database it kept,
	// and the checkout holding the original is one `rm -rf` from gone.
	KeptEnv = "install.env"
)

type Status string

const (
	StatusPending Status = "pending"
	StatusRunning Status = "running"
	StatusSuccess Status = "success"
	StatusFailed  Status = "failed"
)

func (s Status) Live() bool { return s == StatusPending || s == StatusRunning }

// Mode is the one question the operator answers.
type Mode string

const (
	// ModeKeep removes the dashboard and leaves everything it made: the
	// deployments keep running, and the data, backups and .env stay where a
	// reinstall finds them.
	ModeKeep Mode = "keep"
	// ModeWipe also removes the deployments and deletes the dashboard's data.
	ModeWipe Mode = "wipe"
)

func (m Mode) Valid() bool { return m == ModeKeep || m == ModeWipe }

// Phase is where the run is. The first two belong to the backend, which is
// still serving while they run; the rest belong to the sibling, and the
// browser watching loses contact during PhaseStack.
type Phase string

const (
	PhaseQueued      Phase = "queued"
	PhasePausing     Phase = "pausing"
	PhaseDeployments Phase = "deployments"
	PhaseStack       Phase = "stack"
	PhaseFiles       Phase = "files"
	PhaseFinished    Phase = "finished"
)

// Run is one uninstall.
type Run struct {
	ID     string `json:"id"`
	Status Status `json:"status"`
	Phase  Phase  `json:"phase"`
	Mode   Mode   `json:"mode"`
	// DeleteBackups is the one part of a wipe that is asked separately: the
	// archives are copies of the operator's own data, not the dashboard's.
	DeleteBackups bool `json:"deleteBackups"`

	// Host paths, all of them. The sibling mounts the checkout and the data
	// directory at these names and reaches the rest through the host's root.
	Dir       string `json:"dir"`
	Compose   string `json:"compose"`
	Project   string `json:"project,omitempty"`
	Image     string `json:"image"`
	EnvPath   string `json:"envPath"`
	DataDir   string `json:"dataDir"`
	BackupDir string `json:"backupDir,omitempty"`
	// Keep are entries of the data directory a wipe leaves in place. Today
	// that is nginx-cache when a site on this server caches into it: nginx
	// creates a cache's last directory but not its parents, so deleting it
	// would fail the next `nginx -t` for a site that has nothing to do with
	// the dashboard.
	Keep []string `json:"keep,omitempty"`

	Container string `json:"container"`
	// Warnings are what could not be removed. None of them stops the run: an
	// uninstall that gave up because one volume was in use would leave the
	// dashboard installed, which is the one outcome nobody asked for.
	Warnings []string `json:"warnings,omitempty"`

	Actor      string     `json:"actor"`
	StartedAt  time.Time  `json:"startedAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	Error      string     `json:"error,omitempty"`
}

// Store reads and writes the run record and its transcript. Only one process
// writes at a time: the backend until it launches the sibling, the sibling
// after.
type Store struct {
	dir string
	mu  sync.Mutex
}

func NewStore(dataDir string) *Store { return &Store{dir: dataDir} }

func (s *Store) Path() string    { return filepath.Join(s.dir, StateFile) }
func (s *Store) LogPath() string { return filepath.Join(s.dir, LogFile) }

func (s *Store) Load() (*Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

func (s *Store) load() (*Run, error) {
	b, err := os.ReadFile(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var run Run
	if err := json.Unmarshal(b, &run); err != nil {
		return nil, fmt.Errorf("the uninstall record is corrupt: %w", err)
	}
	return &run, nil
}

func (s *Store) Save(run *Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.save(run)
}

func (s *Store) save(run *Run) error {
	run.UpdatedAt = time.Now().UTC()
	b, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o750); err != nil {
		return err
	}
	tmp := s.Path() + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path())
}

func (s *Store) Update(fn func(*Run)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, err := s.load()
	if err != nil {
		return err
	}
	if run == nil {
		return errors.New("no uninstall is in progress")
	}
	fn(run)
	return s.save(run)
}

// Finish closes a run out. err nil means everything asked for was done,
// warnings aside. A failed run keeps the phase it failed in, which is how the
// page says which step it stopped at.
func (s *Store) Finish(err error) error {
	return s.Update(func(r *Run) {
		now := time.Now().UTC()
		r.FinishedAt = &now
		if err == nil {
			r.Status, r.Error, r.Phase = StatusSuccess, "", PhaseFinished
		} else {
			r.Status, r.Error = StatusFailed, err.Error()
		}
	})
}

func (s *Store) OpenLog() (*os.File, error) {
	if err := os.MkdirAll(s.dir, 0o750); err != nil {
		return nil, err
	}
	return os.OpenFile(s.LogPath(), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
}

func (s *Store) AppendLog() (*os.File, error) {
	if err := os.MkdirAll(s.dir, 0o750); err != nil {
		return nil, err
	}
	return os.OpenFile(s.LogPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}

const maxLogTail = 64 * 1024

// maxTranscript bounds the log route, as the restart's does.
const maxTranscript = 8 << 20

// Tail returns the last of the transcript, or "" when there is none.
func (s *Store) Tail() string { return s.tail(maxLogTail) }

// Transcript returns the whole transcript, up to maxTranscript from the end.
func (s *Store) Transcript() string { return s.tail(maxTranscript) }

func (s *Store) tail(limit int64) string {
	f, err := os.Open(s.LogPath())
	if err != nil {
		return ""
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return ""
	}
	var prefix string
	if fi.Size() > limit {
		if _, err := f.Seek(fi.Size()-limit, io.SeekStart); err != nil {
			return ""
		}
		prefix = "… earlier output trimmed …\n"
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return ""
	}
	text := string(b)
	if prefix != "" {
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = text[i+1:]
		}
	}
	return prefix + text
}

// Clear forgets a finished run.
func (s *Store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range []string{s.Path(), s.LogPath()} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
