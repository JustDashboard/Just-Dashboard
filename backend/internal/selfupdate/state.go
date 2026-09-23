package selfupdate

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

// Where an upgrade in progress keeps its state.
//
// Not SQLite, which is where every other piece of dashboard state lives, and
// the exception is the whole reason this file exists. The process writing this
// record is not the dashboard: it is a separate container, started so that it
// survives the dashboard being torn down and rebuilt underneath it. Two
// processes writing the same SQLite file through two different mounts is a
// locking problem nobody needs to have, and the record is a single row that
// exists for ten minutes. A JSON file replaced atomically is exactly the right
// size of mechanism, and it has the property that matters: the new backend,
// booting into a version that did not exist when the upgrade started, can read
// what the old one was doing.

const (
	// StateFile and LogFile sit in JD_DATA_DIR, which the updater container
	// mounts at the same path so both halves address one file.
	StateFile = "self-update.json"
	LogFile   = "self-update.log"
)

type Status string

const (
	// StatusPending is written by the backend before the updater container
	// exists. It is what the updater reads to learn what it was asked to do,
	// and it is also the state a run is stuck in if that container never
	// started — which is a failure the backend detects rather than a run that
	// quietly never happened.
	StatusPending Status = "pending"
	StatusRunning Status = "running"
	StatusSuccess Status = "success"
	StatusFailed  Status = "failed"
)

// Live reports whether this run is still expected to be doing something.
func (s Status) Live() bool { return s == StatusPending || s == StatusRunning }

// Phase is the step being executed, which is the only progress signal
// available: `git fetch` and `docker compose up --build` do not report a
// percentage and inventing one would be a lie about how long is left.
type Phase string

const (
	PhaseQueued     Phase = "queued"
	PhaseFetching   Phase = "fetching"
	PhaseBuilding   Phase = "building"
	PhaseRestarting Phase = "restarting"
	PhaseFinished   Phase = "finished"
)

// Run is one upgrade attempt.
type Run struct {
	ID     string `json:"id"`
	Status Status `json:"status"`
	Phase  Phase  `json:"phase"`

	FromVersion string `json:"fromVersion"`
	ToVersion   string `json:"toVersion"`
	// Ref and Dir are what the updater needs and the backend knows. They are
	// written into the record rather than passed as flags so that there is one
	// description of the job, and the record the operator reads afterwards is
	// the same one that was executed.
	Ref string `json:"ref"`
	Dir string `json:"dir"`
	// Compose is the compose file to rebuild with, relative to Dir.
	Compose string `json:"compose"`
	// Image is what the updater container runs, which is this dashboard's own
	// backend image: it already carries git, the docker CLI and the compose
	// plugin, and it is on the machine by definition.
	Image     string `json:"image"`
	Container string `json:"container"`
	// Health is the URL the updater probes once compose has recreated
	// everything, so that "the update finished" means the dashboard answered
	// rather than that a command exited zero. The updater is given no
	// configuration of its own, so the address it should ask travels with the
	// job like everything else.
	Health string `json:"health,omitempty"`

	FromCommit string `json:"fromCommit,omitempty"`
	ToCommit   string `json:"toCommit,omitempty"`

	Actor      string     `json:"actor"`
	StartedAt  time.Time  `json:"startedAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	Error      string     `json:"error,omitempty"`
}

// Store reads and writes the run record and its log.
//
// The mutex guards this process against itself. It does not — and cannot —
// guard against the updater container writing at the same time, which is why
// the two never write concurrently by design: the backend writes the pending
// record and then stops touching it, and the updater owns it from the moment
// it starts until it finishes. The one exception is the backend's reconcile
// on boot, which runs only after establishing that the updater is gone.
type Store struct {
	dir string
	mu  sync.Mutex
}

func NewStore(dataDir string) *Store { return &Store{dir: dataDir} }

func (s *Store) Path() string    { return filepath.Join(s.dir, StateFile) }
func (s *Store) LogPath() string { return filepath.Join(s.dir, LogFile) }

// Load returns the recorded run, or nil when there has never been one.
//
// A file that cannot be parsed is reported as an error rather than as "no
// run": the difference matters, because "no run" is what allows a second
// upgrade to start.
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
		return nil, fmt.Errorf("update state file is corrupt: %w", err)
	}
	return &run, nil
}

// Save replaces the record atomically. A half-written state file read by a
// backend that has just booted is the one failure this whole file exists to
// avoid, so the write goes through a temporary file in the same directory.
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

// Update applies a change to the stored run. It is the form every progress
// write takes, so no caller has to remember the read-modify-write.
func (s *Store) Update(fn func(*Run)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, err := s.load()
	if err != nil {
		return err
	}
	if run == nil {
		return errors.New("no update is in progress")
	}
	fn(run)
	return s.save(run)
}

// Finish closes a run out. err nil means it worked.
func (s *Store) Finish(err error) error {
	return s.Update(func(r *Run) {
		now := time.Now().UTC()
		r.FinishedAt = &now
		r.Phase = PhaseFinished
		if err != nil {
			r.Status = StatusFailed
			r.Error = err.Error()
			return
		}
		r.Status = StatusSuccess
		r.Error = ""
	})
}

// OpenLog truncates and opens the transcript. The previous run's log is
// deliberately not kept: the question an operator has is always about the
// upgrade that just happened, and a file that grows without bound in the data
// directory is a bug waiting for a busy year.
func (s *Store) OpenLog() (*os.File, error) {
	if err := os.MkdirAll(s.dir, 0o750); err != nil {
		return nil, err
	}
	return os.OpenFile(s.LogPath(), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
}

// AppendLog reopens the transcript to add to it. The updater runs in a
// different process from the one that opened it, so the header the backend
// wrote is kept rather than truncated a second time.
func (s *Store) AppendLog() (*os.File, error) {
	if err := os.MkdirAll(s.dir, 0o750); err != nil {
		return nil, err
	}
	return os.OpenFile(s.LogPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}

// maxLogTail is how much of the transcript the API hands back. A rebuild that
// pulls a base image and compiles a Go binary prints a few hundred kilobytes;
// the part that says what went wrong is the end of it.
const maxLogTail = 64 * 1024

// maxTranscript bounds what the log route hands back for a whole run. A
// rebuild prints a few hundred kilobytes; the cap is there so a runaway build
// cannot turn one request into a download of the data directory's disk.
const maxTranscript = 8 << 20

// Tail returns the last of the transcript, or "" when there is none.
func (s *Store) Tail() string { return s.tail(maxLogTail) }

// Transcript returns the whole transcript, up to maxTranscript from the end.
// The report carries only the Tail because it is polled every two seconds;
// this is read when somebody asks to see the earlier output.
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
	// A seek into the middle of the file lands mid-line; dropping the partial
	// first line is cheaper than rendering half a word as if it were output.
	text := string(b)
	if prefix != "" {
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = text[i+1:]
		}
	}
	return prefix + text
}

// Clear removes the record and its log, which is what "dismiss" does once a
// run has been read. Missing files are not an error — dismissing twice is a
// double-click, not a fault.
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
