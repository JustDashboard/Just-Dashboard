package selfcfg

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

// Where a restart in progress keeps its state.
//
// The same shape, and for the same reason, as internal/selfupdate: the process
// carrying the work out is a sibling container, not this one, because this one
// is among the things being restarted. Two processes, one file, replaced
// atomically — and the backend that comes back up reads it to find out what
// the backend that went down was doing.
const (
	StateFile = "self-config.json"
	LogFile   = "self-config.log"
)

type Status string

const (
	StatusPending Status = "pending"
	StatusRunning Status = "running"
	StatusSuccess Status = "success"
	StatusFailed  Status = "failed"
	// StatusRolledBack is the outcome this whole feature is built around: the
	// new configuration did not come back up, the previous .env was put back,
	// and the dashboard is running the way it was before the operator touched
	// it. It is a failure of the change and a success of the safety net, and
	// conflating it with either of the other two would misreport one of them.
	StatusRolledBack Status = "rolled_back"
)

func (s Status) Live() bool { return s == StatusPending || s == StatusRunning }

// Action is what was asked for.
type Action string

const (
	// ActionApply writes new settings and restarts into them.
	ActionApply Action = "apply"
	// ActionRestart recreates the containers with the configuration already on
	// disk, relocating ports that another service has since occupied.
	ActionRestart Action = "restart"
	// ActionRebuild rebuilds the images first. Same configuration, new
	// binaries — what an operator wants after editing the checkout by hand.
	ActionRebuild Action = "rebuild"
)

type Phase string

const (
	PhaseQueued   Phase = "queued"
	PhaseApplying Phase = "applying"
	PhaseWaiting  Phase = "waiting"
	PhaseRollback Phase = "rollback"
	PhaseFinished Phase = "finished"
)

// Run is one restart, whatever asked for it.
type Run struct {
	ID     string `json:"id"`
	Status Status `json:"status"`
	Phase  Phase  `json:"phase"`
	Action Action `json:"action"`

	// Changes is what moved, for the record an operator reads afterwards. A
	// restart or a rebuild carries none.
	Changes []Change `json:"changes,omitempty"`

	Dir     string `json:"dir"`
	Compose string `json:"compose"`
	Image   string `json:"image"`
	// EnvPath and Backup are the file that was written and the copy taken
	// first. Both are host paths: the sibling mounts the checkout at the same
	// name, so one string means the same file to both halves and to the
	// operator reading it over ssh.
	EnvPath string `json:"envPath,omitempty"`
	Backup  string `json:"backup,omitempty"`
	// Health is the URL the sibling probes to decide whether the new
	// configuration worked. It is the *new* backend port, which is exactly why
	// it travels in the record rather than being recomputed later by a process
	// that would have to guess.
	Health string `json:"health,omitempty"`
	// RollbackHealth is the same probe for the configuration being replaced,
	// used only if the new one has to be undone. It is recorded up front
	// because by the time it is needed the settings it came from have already
	// been overwritten.
	RollbackHealth string `json:"rollbackHealth,omitempty"`
	// Endpoint is where the operator should go once this is done. Worth
	// recording rather than deriving, because after a port change the browser
	// showing this record can no longer reach the dashboard that wrote it.
	Endpoint  string `json:"endpoint,omitempty"`
	Container string `json:"container"`

	// Note is something an operator has to know about a run that otherwise
	// worked. There is one today: a Tailscale certificate that could not be
	// issued, so the new address is served with a self-signed one until the
	// tailnet will issue the real thing.
	Note string `json:"note,omitempty"`

	Actor      string     `json:"actor"`
	StartedAt  time.Time  `json:"startedAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	Error      string     `json:"error,omitempty"`
}

// Store reads and writes the run record and its transcript.
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
		return nil, fmt.Errorf("the restart record is corrupt: %w", err)
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
		return errors.New("no restart is in progress")
	}
	fn(run)
	return s.save(run)
}

// Finish closes a run out. err nil means the new configuration is live.
func (s *Store) Finish(err error, rolledBack bool) error {
	return s.Update(func(r *Run) {
		now := time.Now().UTC()
		r.FinishedAt = &now
		r.Phase = PhaseFinished
		switch {
		case err == nil:
			r.Status, r.Error = StatusSuccess, ""
		case rolledBack:
			r.Status, r.Error = StatusRolledBack, err.Error()
		default:
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

// Tail returns the last of the transcript, or "" when there is none.
func (s *Store) Tail() string {
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
	if fi.Size() > maxLogTail {
		if _, err := f.Seek(fi.Size()-maxLogTail, io.SeekStart); err != nil {
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

// Clear forgets a finished run, which is what dismissing the card does.
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
