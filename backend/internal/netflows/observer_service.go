package netflows

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type observerRecord struct {
	cycle Cycle
	rows  []Bucket
}

func (s *Service) saveObserverRecordLocked(ctx context.Context) error {
	if s.pendingRecord == nil {
		return nil
	}
	r := s.pendingRecord
	st := s.state
	st.Observer = r.cycle.Observer
	saved, err := s.store.record(ctx, st, r.cycle, r.rows)
	if err != nil {
		s.lastError = "The kernel event batch could not be saved: " + clip(err.Error(), 512)
		s.differ = differencer{}
		return err
	}
	if r.cycle.Observer != nil && r.cycle.Observer.BatchID != "" {
		if err = s.observer.Acknowledge(r.cycle.Observer.BatchID); err != nil {
			s.lastError = "Saved kernel events await acknowledgement: " + clip(err.Error(), 512)
			return err
		}
	}
	s.state = saved
	s.pendingRecord = nil
	s.lastError = ""
	return nil
}

// SetObserver is a construction hook. Reading reports never loads programs.
func (s *Service) SetObserver(observer EventObserver) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		s.observer = observer
	}
}
func (s *Service) KernelRecording(ctx context.Context, enabled bool) (ObserverEvidence, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return ObserverEvidence{}, err
	}
	if s.observer == nil {
		return ObserverEvidence{}, ErrUnavailable
	}
	if !enabled {
		err := s.stopObserverLocked(ctx)
		return s.observer.Status(), err
	}
	if !s.state.Settings.Enabled {
		return s.observer.Status(), fmt.Errorf("%w: enable history recording before explicitly attaching the kernel observer", ErrInvalid)
	}
	if s.state.Settings.KernelObserverEnabled {
		evidence := s.observer.Status()
		if evidence.StoppedAt != nil || evidence.Status == "unavailable" {
			return evidence, fmt.Errorf("%w: stop the retained observer session before attaching again", ErrUnavailable)
		}
		return evidence, nil
	}
	// A retained active marker makes process death observable even during
	// partial attachment. It never authorizes automatic attachment at restart.
	st := s.state
	st.Settings.KernelObserverEnabled = true
	evidence := s.observer.Status()
	evidence.Status = "starting"
	st.Observer = &evidence
	var err error
	st, err = s.store.policy(ctx, st, time.Now().UTC())
	if err != nil {
		return evidence, err
	}
	s.state = st
	if err = s.observer.Start(ctx); err != nil {
		evidence = s.observer.Status()
		st.Settings.KernelObserverEnabled = evidence.AttachmentsRetained
		st.Observer = &evidence
		if saved, saveErr := s.store.policy(ctx, st, time.Now().UTC()); saveErr == nil {
			s.state = saved
		} else {
			s.lastError = "Observer refusal could not be saved: " + clip(saveErr.Error(), 512)
		}
		return evidence, err
	}
	evidence = s.observer.Status()
	st.Observer = &evidence
	st, err = s.store.policy(ctx, st, time.Now().UTC())
	if err != nil {
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		stopped, stopErr := s.observer.Stop(stopCtx)
		cancel()
		s.state.Observer = &stopped
		s.state.Settings.KernelObserverEnabled = stopped.AttachmentsRetained
		s.lastError = "Observer activation could not be saved."
		return stopped, errors.Join(err, stopErr)
	}
	s.state = st
	return evidence, nil
}
func (s *Service) stopObserverLocked(ctx context.Context) error {
	if s.observer == nil {
		return nil
	}
	if err := s.saveObserverRecordLocked(ctx); err != nil {
		return err
	}
	if !s.state.Settings.KernelObserverEnabled && !s.observer.Status().AttachmentsRetained {
		return nil
	}
	now := time.Now().UTC()
	rows, evidence, drainErr := s.observer.Drain(ctx, now)
	if drainErr == nil && evidence.BatchID != "" {
		c := Cycle{At: now, FinishedAt: now, BootID: evidence.BootID, KernelRelease: evidence.KernelRelease, Tool: "fixed cgroup observer", Observer: &evidence, DroppedEvents: &evidence.Quality.RingDrops, Sources: []Source{{ID: "kernel-cgroup", Name: "Kernel cgroup packet observations", Status: evidence.Status, ObservedAt: now}}, DockerStatus: evidence.DockerStatus, DockerError: evidence.DockerError}
		s.pendingRecord = &observerRecord{cycle: c, rows: rows}
		if err := s.saveObserverRecordLocked(ctx); err != nil {
			return err
		}
	}
	stopCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	stopped, stopErr := s.observer.Stop(stopCtx)
	cancel()
	st := s.state
	st.Observer = &stopped
	st.Settings.KernelObserverEnabled = stopped.AttachmentsRetained
	saved, err := s.store.policy(ctx, st, time.Now().UTC())
	if err != nil {
		s.state = st
		s.lastError = "Observer shutdown evidence could not be saved: " + clip(err.Error(), 512)
		return errors.Join(err, stopErr, drainErr)
	}
	s.state = saved
	if stopErr != nil || drainErr != nil {
		return errors.Join(stopErr, drainErr)
	}
	return nil
}
