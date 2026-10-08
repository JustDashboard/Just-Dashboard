package netflows

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

type Service struct {
	mu            sync.Mutex
	exportMu      sync.Mutex
	store         *Store
	collector     Collector
	observer      EventObserver
	state         persistedState
	differ        differencer
	generation    uint64
	started       bool
	stopping      bool
	cancel        context.CancelFunc
	activeCancel  context.CancelFunc
	wake          chan struct{}
	done          chan struct{}
	lastError     string
	startedAt     time.Time
	pendingRecord *observerRecord
}

func New(store *Store, collector Collector) *Service {
	return &Service{store: store, collector: collector, wake: make(chan struct{}, 1), done: make(chan struct{})}
}
func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return nil
	}
	if s.store == nil {
		return ErrUnavailable
	}
	st, err := s.store.state(ctx)
	if err != nil {
		return err
	}
	if st.Settings.KernelObserverEnabled {
		st.Settings.KernelObserverEnabled = false
		if st.Observer == nil {
			st.Observer = &ObserverEvidence{}
		}
		st.Observer.Status = "interrupted"
		st.Observer.AttachmentsRetained = false
		st.Observer.BatchID = ""
		st.Observer.Reason = "The previous kernel session ended without retained shutdown evidence. Explicit opt-in is required to attach again."
		st.Observer.Quality.ShutdownTailUnknown = true
	}
	st, err = s.store.policy(ctx, st, time.Now().UTC())
	if err != nil {
		return err
	}
	s.state = st
	s.started = true
	s.startedAt = time.Now().UTC()
	loopCtx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	go s.loop(loopCtx)
	return nil
}
func (s *Service) loop(ctx context.Context) {
	defer close(s.done)
	for {
		s.mu.Lock()
		enabled := s.state.Settings.Enabled
		delay := time.Duration(s.state.Settings.IntervalSeconds) * time.Second
		s.mu.Unlock()
		if enabled {
			s.sample(ctx)
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-s.wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}
func (s *Service) sample(ctx context.Context) {
	s.mu.Lock()
	if !s.started || s.stopping || !s.state.Settings.Enabled {
		s.mu.Unlock()
		return
	}
	if s.pendingRecord != nil {
		writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_ = s.saveObserverRecordLocked(writeCtx)
		cancel()
		s.mu.Unlock()
		return
	}
	if s.collector == nil {
		s.lastError = "The native collector is unavailable."
		s.mu.Unlock()
		return
	}
	generation := s.generation
	interval := time.Duration(s.state.Settings.IntervalSeconds) * time.Second
	runCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	s.activeCancel = cancel
	s.mu.Unlock()
	c := s.collector.Collect(runCtx)
	cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activeCancel = nil
	if s.stopping || !s.state.Settings.Enabled || s.generation != generation {
		return
	}
	rows := s.differ.observe(&c, interval)
	writeCtx, writeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer writeCancel()
	if s.state.Settings.KernelObserverEnabled && s.observer != nil {
		eventRows, evidence, eventErr := s.observer.Drain(writeCtx, c.FinishedAt)
		if eventErr != nil {
			evidence.Status = "unavailable"
			evidence.Reason = clip(eventErr.Error(), 512)
		} else {
			rows = append(rows, eventRows...)
		}
		c.Observer = &evidence
		c.DroppedEvents = &evidence.Quality.RingDrops
		if evidence.BatchID != "" {
			s.pendingRecord = &observerRecord{cycle: c, rows: rows}
			_ = s.saveObserverRecordLocked(writeCtx)
			return
		}
	}
	st, err := s.store.record(writeCtx, s.state, c, rows)
	if err != nil {
		s.lastError = "The last sample could not be saved: " + clip(err.Error(), 512)
		s.differ = differencer{}
		return
	}
	s.state = st
	s.lastError = ""

}
func (s *Service) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return nil
	}
	s.stopping = true
	if s.activeCancel != nil {
		s.activeCancel()
	}
	s.cancel()
	done := s.done
	s.mu.Unlock()
	select {
	case <-done:
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.stopObserverLocked(ctx)
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *Service) ready() error {
	if !s.started || s.stopping {
		return ErrUnavailable
	}
	return nil
}
func (s *Service) Recording(ctx context.Context, enabled bool) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return Settings{}, err
	}
	st := s.state
	if !enabled && (st.Settings.KernelObserverEnabled || s.pendingRecord != nil || s.observer != nil && s.observer.Status().AttachmentsRetained) {
		// History toggles use an ordinary route. Detaching a kernel program
		// must remain an explicit destructive operation with its own budget.
		return Settings{}, fmt.Errorf("%w: stop the kernel observer explicitly before stopping history recording", ErrInvalid)
	}
	st.Settings.Enabled = enabled
	if enabled && !s.state.Settings.Enabled {
		now := time.Now().UTC()
		st.Since = &now
	}
	st, err := s.store.policy(ctx, st, time.Now().UTC())
	if err != nil {
		return Settings{}, err
	}
	s.state = st
	s.generation++
	s.differ = differencer{}
	if s.activeCancel != nil {
		s.activeCancel()
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return st.Settings, nil
}
func (s *Service) Policy(ctx context.Context, interval, days int) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return Settings{}, err
	}
	st := s.state
	st.Settings.IntervalSeconds, st.Settings.RetentionDays = interval, days
	if err := st.Settings.Validate(); err != nil {
		return Settings{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	st, err := s.store.policy(ctx, st, time.Now().UTC())
	if err != nil {
		return Settings{}, err
	}
	s.state = st
	s.generation++
	s.differ = differencer{}
	if s.activeCancel != nil {
		s.activeCancel()
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return st.Settings, nil
}
func (s *Service) Clear(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return err
	}
	if err := s.stopObserverLocked(ctx); err != nil {
		return err
	}
	st, err := s.store.clear(ctx, s.state)
	if err != nil {
		return err
	}
	s.state = st
	s.generation++
	s.differ = differencer{}
	if s.activeCancel != nil {
		s.activeCancel()
	}
	return nil
}
func (s *Service) Report(ctx context.Context, q Query) (Report, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return Report{}, err
	}
	now := time.Now().UTC()
	q, err := ValidateQuery(q, now)
	if err != nil {
		return Report{}, err
	}
	// Read-time retention never runs a host command. Idle recordings cannot
	// keep expired connection metadata simply because native sampling is off.
	st, err := s.store.policy(ctx, s.state, now)
	if err != nil {
		return Report{}, err
	}
	s.state = st
	r, err := s.store.read(ctx, q)
	if err != nil {
		return Report{}, err
	}
	r.CheckedAt = now
	r.Settings = st.Settings
	r.RecordingSince = st.Since
	r.CollectorStartedAt = s.startedAt
	r.PrunedRows = st.Pruned
	r.Coverage = append([]string{}, Coverage...)
	if st.Settings.KernelObserverEnabled {
		r.Coverage = append(r.Coverage, "The explicit fixed cgroup observer records bounded TCP/UDP header events, including short sockets; transport payload subtotals include retransmitted packets and do not prove application delivery.", "Each observed byte subtotal carries per-direction packet, known-byte and byte-gap counts. Ring drops, budget omissions, unsupported headers, missing identities, row-cap omissions and unverified owners remain separate quality evidence.", "UTC placement is a checked monotonic projection; a detected wall-clock discontinuity marks affected rows uncertain. Observer opt-in never resumes automatically after process restart.")
	}
	if s.observer != nil {
		r.KernelObserver = s.observer.Status()
	} else {
		r.KernelObserver.Status = "unavailable"
		r.KernelObserver.Reason = "The supported kernel observer is not configured. Native UDP bytes, short-lived flows and dropped-event counts remain unknown."
	}
	if st.Observer != nil && !st.Settings.KernelObserverEnabled {
		r.KernelObserver = *st.Observer
	}
	r.Status = "off"
	if st.Settings.Enabled {
		r.Status = "waiting"
		if r.LastCycle != nil {
			r.Status = "recording"
			observed := 0
			for _, src := range r.LastCycle.Sources {
				if src.Status == "observed" || src.Status == "partial" {
					observed++
				}
				if src.Status == "unavailable" || src.Status == "partial" {
					r.Status = "partial"
				}
			}
			if r.LastCycle.OmittedSources > 0 || r.LastCycle.DockerStatus != "observed" {
				r.Status = "partial"
			}
			if observed == 0 {
				r.Status = "unavailable"
			}
			if now.Sub(r.LastCycle.FinishedAt) > 2*time.Duration(st.Settings.IntervalSeconds)*time.Second {
				r.Status = "stale"
			}
		}
	}
	if s.lastError != "" {
		r.Status = "unavailable"
		r.Error = s.lastError
	}
	return r, nil
}

// Export carries the same evidence as the report, with both row and encoded
// byte caps. A truncated export retains coverage and never implies completeness.
func (s *Service) Export(ctx context.Context, q Query) ([]byte, error) {
	if !s.exportMu.TryLock() {
		return nil, fmt.Errorf("%w: another bounded export is in progress", ErrUnavailable)
	}
	defer s.exportMu.Unlock()
	var err error
	q, err = ValidateQuery(q, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	q.Limit = MaxExportRows
	r, err := s.Report(ctx, q)
	if err != nil {
		return nil, err
	}
	for {
		data, err := json.Marshal(r)
		if err != nil {
			return nil, err
		}
		if len(data) <= MaxExportBytes {
			return data, nil
		}
		if len(r.Rows) > 0 {
			r.Truncated = true
			r.Rows = r.Rows[:len(r.Rows)-max(1, len(r.Rows)/4)]
			continue
		}
		if len(r.CoverageHours) > 0 {
			r.CoverageTruncated = true
			r.CoverageHours = r.CoverageHours[max(1, len(r.CoverageHours)/4):]
			continue
		}
		return nil, fmt.Errorf("%w: metadata exceeds export byte cap", ErrUnavailable)
	}
}
