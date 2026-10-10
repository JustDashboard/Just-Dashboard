package netcapture

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
)

type Runner func(context.Context, Request) (*Result, error)
type activeCapture struct {
	jobID    string
	done     chan struct{}
	shutdown bool
}
type pendingCapture struct {
	run      Run
	artifact []byte
}

type Service struct {
	mu       sync.Mutex
	db       *sql.DB
	jobs     *jobs.Manager
	runner   Runner
	active   map[string]*activeCapture
	pending  map[string]pendingCapture
	started  bool
	stopping bool
	now      func() time.Time
}

func New(db *sql.DB, manager *jobs.Manager, runner Runner) *Service {
	return &Service{db: db, jobs: manager, runner: runner, active: map[string]*activeCapture{}, pending: map[string]pendingCapture{}, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) ready() error {
	if s == nil || !s.started || s.stopping || s.runner == nil {
		return ErrUnavailable
	}
	return nil
}

// Startup records interruption without replaying a capture. The host timeout
// remains the independent maximum lifetime of any predecessor native process.
func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return nil
	}
	runs, err := s.list(ctx)
	if err != nil {
		return err
	}
	for _, run := range runs {
		if Terminal(run.Status) {
			continue
		}
		now := s.now()
		run.Status, run.EndedAt, run.Error = "interrupted", &now, "Backend restarted before recording native cleanup; the independent host time limit remains in force"
		if err := s.update(ctx, run, nil); err != nil {
			return err
		}
	}
	if err := s.prune(ctx, false); err != nil {
		return err
	}
	s.started = true
	return nil
}

func (s *Service) flush(ctx context.Context) error {
	for id, final := range s.pending {
		if err := s.update(ctx, final.run, final.artifact); err != nil {
			return fmt.Errorf("final capture recording needs attention: %w", err)
		}
		delete(s.pending, id)
	}
	return nil
}

func (s *Service) prune(ctx context.Context, reserve bool) error {
	limit := MaxRetained
	if reserve {
		limit--
	}
	var unfinished int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM network_packet_captures WHERE status NOT IN ('completed','failed','cancelled','interrupted')`).Scan(&unfinished); err != nil {
		return err
	}
	// Active records may be older than every terminal record. Reserve their
	// slots before ordering terminal rows so pruning never exceeds the total cap.
	_, err := s.db.ExecContext(ctx, `DELETE FROM network_packet_captures WHERE status IN ('completed','failed','cancelled','interrupted') AND (created_at<? OR id IN (SELECT id FROM network_packet_captures WHERE status IN ('completed','failed','cancelled','interrupted') ORDER BY created_at DESC,id DESC LIMIT -1 OFFSET ?))`, s.now().Add(-Retention).UnixMilli(), max(0, limit-unfinished))
	return err
}

func (s *Service) Create(ctx context.Context, name string, request Request, actor string) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return Run{}, err
	}
	name, err := ValidateName(name)
	if err != nil {
		return Run{}, err
	}
	request, err = Validate(request)
	if err != nil {
		return Run{}, err
	}
	if err := s.flush(ctx); err != nil {
		return Run{}, err
	}
	if len(s.active) >= MaxRunning {
		return Run{}, ErrBusy
	}
	if err := s.prune(ctx, true); err != nil {
		return Run{}, err
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return Run{}, err
	}
	run := Run{ID: hex.EncodeToString(raw[:]), Name: name, Request: request, Status: "queued", CreatedAt: s.now(), CreatedBy: actor,
		Limitations: []string{"Capture is scoped to one observed native interface and one explicitly selected address family.", "PCAP contains original packet bytes up to the snapshot length, which can include credentials or application data. Downloads require system.admin.", "Kernel drop counts are retained only when tcpdump reports them; absent counters are unknown. Capture is not proof of all traffic or every namespace.", "The bounded artifact is retained for 24 hours, up to 32 capture records and 64 MiB of packet data in total.", "An abrupt backend exit leaves final cleanup unverified; the host timeout limits any surviving capture to its selected duration plus two seconds."}}
	data, _ := json.Marshal(run)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO network_packet_captures(id,status,created_at,payload,artifact) VALUES(?,?,?,?,?)`, run.ID, run.Status, run.CreatedAt.UnixMilli(), string(data), []byte{}); err != nil {
		return Run{}, err
	}
	active := &activeCapture{done: make(chan struct{})}
	s.active[run.ID] = active
	gate := make(chan struct{})
	job, _ := s.jobs.StartExclusive(JobPrefix+run.ID+".", jobs.Spec{Kind: JobPrefix + run.ID + ".pcap", Title: "Packet capture", StartedBy: actor, Timeout: time.Duration(request.Seconds+10) * time.Second}, func(ctx context.Context, out jobs.Emitter) error { <-gate; return s.execute(ctx, run, out) })
	active.jobID, run.JobID = job.ID, job.ID
	err = s.update(ctx, run, nil)
	if err != nil {
		s.jobs.Cancel(job.ID)
	}
	close(gate)
	if err != nil {
		return Run{}, err
	}
	return run, nil
}

func (s *Service) execute(ctx context.Context, run Run, out jobs.Emitter) error {
	s.mu.Lock()
	active := s.active[run.ID]
	now := s.now()
	run.Status, run.StartedAt = "running", &now
	err := s.update(context.Background(), run, nil)
	s.mu.Unlock()
	var result *Result
	if err == nil && ctx.Err() == nil {
		out.Status("Capturing within the selected packet, time and byte limits")
		progressCtx := context.WithValue(ctx, progressKey{}, func(progress Result) {
			s.mu.Lock()
			defer s.mu.Unlock()
			current, readErr := s.get(context.Background(), run.ID)
			if readErr != nil || current.Status != "running" {
				return
			}
			progress.Artifact = nil
			progress.ArtifactAvailable = false
			current.Result = &progress
			// Progress is provisional. A failed telemetry write never changes
			// the immutable request or permits another native execution.
			_ = s.update(context.Background(), current, nil)
		})
		result, err = s.runner(progressCtx, run.Request)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now = s.now()
	run.EndedAt = &now
	run.Result = result
	artifact := []byte{}
	if result != nil {
		artifact = result.Artifact
		result.Artifact = nil
		if len(artifact) > run.Request.MaxBytes || len(artifact) > MaxArtifactBytes {
			artifact = nil
			result.ArtifactAvailable = false
			err = errors.Join(err, fmt.Errorf("capture runner exceeded its artifact bound"))
		}
	}
	switch {
	case active.shutdown:
		run.Status = "interrupted"
	case ctx.Err() == context.Canceled:
		run.Status = "cancelled"
	case ctx.Err() != nil || err != nil || result == nil:
		run.Status = "failed"
	default:
		run.Status = "completed"
	}
	if err != nil {
		run.Error = err.Error()
		if len(run.Error) > 2048 {
			run.Error = run.Error[:2048]
		}
	}
	if saveErr := s.update(context.Background(), run, artifact); saveErr != nil {
		s.pending[run.ID] = pendingCapture{run: run, artifact: artifact}
		err = errors.Join(err, saveErr)
	}
	delete(s.active, run.ID)
	close(active.done)
	if pruneErr := s.prune(context.Background(), false); pruneErr != nil {
		err = errors.Join(err, pruneErr)
	}
	return err
}

func (s *Service) update(ctx context.Context, run Run, artifact []byte) error {
	data, err := json.Marshal(run)
	if err != nil {
		return err
	}
	if len(data) > 65536 {
		return fmt.Errorf("capture metadata exceeded its bound")
	}
	query := `UPDATE network_packet_captures SET status=?,payload=? WHERE id=?`
	args := []any{run.Status, string(data), run.ID}
	if artifact != nil {
		query = `UPDATE network_packet_captures SET status=?,payload=?,artifact=? WHERE id=?`
		args = []any{run.Status, string(data), artifact, run.ID}
	}
	_, err = s.db.ExecContext(ctx, query, args...)
	return err
}

func (s *Service) get(ctx context.Context, id string) (Run, error) {
	if !ValidID(id) {
		return Run{}, ErrNotFound
	}
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT payload FROM network_packet_captures WHERE id=?`, id).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Run{}, ErrNotFound
		}
		return Run{}, err
	}
	var run Run
	if len(raw) > 65536 || json.Unmarshal([]byte(raw), &run) != nil || run.ID != id {
		return Run{}, fmt.Errorf("saved capture metadata is unreadable")
	}
	if _, err := Validate(run.Request); err != nil {
		return Run{}, fmt.Errorf("saved capture request is unreadable: %w", err)
	}
	return run, nil
}

func (s *Service) list(ctx context.Context) ([]Run, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM network_packet_captures ORDER BY created_at DESC,id DESC LIMIT ?`, MaxRetained)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	runs := []Run{}
	for _, id := range ids {
		run, err := s.get(ctx, id)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, nil
}

func (s *Service) List(ctx context.Context) ([]Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := s.flush(ctx); err != nil {
		return nil, err
	}
	if err := s.prune(ctx, false); err != nil {
		return nil, err
	}
	return s.list(ctx)
}
func (s *Service) Get(ctx context.Context, id string) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return Run{}, err
	}
	if err := s.flush(ctx); err != nil {
		return Run{}, err
	}
	if err := s.prune(ctx, false); err != nil {
		return Run{}, err
	}
	return s.get(ctx, id)
}

func (s *Service) Artifact(ctx context.Context, id string) (Run, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return Run{}, nil, err
	}
	if err := s.flush(ctx); err != nil {
		return Run{}, nil, err
	}
	if err := s.prune(ctx, false); err != nil {
		return Run{}, nil, err
	}
	run, err := s.get(ctx, id)
	if err != nil {
		return Run{}, nil, err
	}
	if !Terminal(run.Status) || run.Result == nil || !run.Result.ArtifactAvailable {
		return run, nil, ErrNotFound
	}
	var size int
	if err := s.db.QueryRowContext(ctx, `SELECT length(artifact) FROM network_packet_captures WHERE id=?`, id).Scan(&size); err != nil {
		return run, nil, err
	}
	if size < 24 || size > MaxArtifactBytes {
		return run, nil, fmt.Errorf("saved capture artifact is unreadable")
	}
	var artifact []byte
	err = s.db.QueryRowContext(ctx, `SELECT artifact FROM network_packet_captures WHERE id=?`, id).Scan(&artifact)
	if err == nil {
		w := &pcapWriter{request: run.Request, stop: func() {}}
		_, _ = w.Write(artifact)
		verified := w.result()
		if w.err != nil || len(w.pending) > 0 || len(w.data) != len(artifact) || verified.SHA256 != run.Result.SHA256 {
			err = fmt.Errorf("saved capture artifact integrity could not be established")
		}
	}
	return run, artifact, err
}

func (s *Service) Cancel(ctx context.Context, id string) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return Run{}, err
	}
	run, err := s.get(ctx, id)
	if err != nil {
		return Run{}, err
	}
	if active := s.active[id]; active != nil {
		run.Status = "cancelling"
		s.jobs.Cancel(active.jobID)
		if err := s.update(ctx, run, nil); err != nil {
			return Run{}, fmt.Errorf("capture stop was requested but recording failed: %w", err)
		}
	}
	return run, nil
}

func (s *Service) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return err
	}
	if err := s.flush(ctx); err != nil {
		return err
	}
	run, err := s.get(ctx, id)
	if err != nil {
		return err
	}
	if !Terminal(run.Status) {
		return ErrRunning
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM network_packet_captures WHERE id=?`, id)
	return err
}

func (s *Service) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.stopping = true
	done := []chan struct{}{}
	for _, active := range s.active {
		active.shutdown = true
		s.jobs.Cancel(active.jobID)
		done = append(done, active.done)
	}
	s.mu.Unlock()
	for _, finished := range done {
		select {
		case <-finished:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// Support export omits names, actors, tuples, filters, native text and packet
// bytes. Original PCAP is a separate authorized download and is never redacted.
func (s *Service) Support(ctx context.Context, id string) ([]byte, error) {
	run, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	result := run.Result
	if result != nil {
		copy := *result
		copy.NativeSummary = ""
		copy.Artifact = nil
		copy.SHA256 = ""
		copy.InterfaceIndex = 0
		result = &copy
	}
	return json.Marshal(struct {
		Version   int        `json:"version"`
		ID        string     `json:"id"`
		Status    string     `json:"status"`
		CreatedAt time.Time  `json:"createdAt"`
		StartedAt *time.Time `json:"startedAt,omitempty"`
		EndedAt   *time.Time `json:"endedAt,omitempty"`
		Packets   int        `json:"packetLimit"`
		Seconds   int        `json:"timeLimitSeconds"`
		Bytes     int        `json:"byteLimit"`
		Snapshot  int        `json:"snapshotLength"`
		Result    *Result    `json:"result,omitempty"`
		Redaction string     `json:"redaction"`
	}{1, run.ID, run.Status, run.CreatedAt, run.StartedAt, run.EndedAt, run.Request.Packets, run.Request.Seconds, run.Request.MaxBytes, run.Request.SnapshotLength, result, "Original packet bytes, request tuple/filter, interface, names, actors and native text are omitted. Unknown drop/cleanup evidence remains unknown."})
}
