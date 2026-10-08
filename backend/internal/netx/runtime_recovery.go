package netx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Native-owned devices keep their runtime-only contract. Their observed undo
// still needs to survive the process that applied the edit.
func (s *Service) runtimeOnly(ctx context.Context, st step) error {
	lock, err := lockChange(s.paths.Dir)
	if err != nil {
		return err
	}
	defer unlockChange(lock)
	sp, err := s.loadSpec()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(sp, "", "  ")
	if err != nil {
		return err
	}
	j, err := s.prepareChange(ctx, sp, nil, nil, append(data, '\n'), st.recovery, nil)
	if err != nil {
		return err
	}
	j.Persistence, j.Boot = "not_applicable", "not_applicable"
	var failures []string
	recoveryCtx := context.WithValue(ctx, recoveryErrorsKey{}, &failures)
	recover := func(cause error) error {
		j.Phase = "recovering"
		if err := j.save(); err != nil {
			failures = append(failures, "record recovery: "+err.Error())
		}
		rollback(recoveryCtx, st.undo)
		j.Phase, j.Runtime = "recovered", "undo_attempted"
		j.RecoveryErrors = append(j.RecoveryErrors, failures...)
		if len(j.RecoveryErrors) > 0 {
			j.Phase, j.Runtime = "degraded", "unknown"
		}
		if err := j.save(); err != nil {
			return errors.Join(cause, fmt.Errorf("recording runtime recovery: %w", err))
		}
		return cause
	}
	if st.apply != nil {
		if err := st.apply(recoveryCtx); err != nil {
			return recover(err)
		}
	}
	j.Phase, j.Runtime = "runtime_applied", "applied"
	if err := j.save(); err != nil {
		return recover(err)
	}
	if st.verify != nil {
		if err := st.verify(recoveryCtx); err != nil {
			return recover(err)
		}
	}
	j.Phase = "saved"
	if j.Watchdog == "armed" {
		j.Watchdog = "completed"
	}
	if err := j.save(); err != nil {
		return recover(err)
	}
	return nil
}
