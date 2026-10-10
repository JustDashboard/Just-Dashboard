package netx

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var errShapingDrift = errors.New("shaping differs from the saved configuration")

func shapingDrift(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errShapingDrift, fmt.Sprintf(format, args...))
}

// Kernel read failure must remain distinguishable from an observed mismatch.
type ShapeVerification struct {
	Status    string    `json:"status"`
	CheckedAt time.Time `json:"checkedAt"`
	Reason    string    `json:"reason,omitempty"`
}

func readShapeVerification(ctx context.Context, sh ShapeSpec) *ShapeVerification {
	v := &ShapeVerification{Status: "verified", CheckedAt: time.Now().UTC()}
	if err := verifyShaping(ctx, sh); err != nil {
		v.Status, v.Reason = "unknown", err.Error()
		if errors.Is(err, errShapingDrift) {
			v.Status = "drift"
		}
	}
	return v
}

// verifyShapeEntry is readShapeVerification for the Traffic page: it also
// compares the parameter queue with the one read back right after the entry
// was applied, so a queue changed in place with `tc qdisc change` is drift
// rather than verified. Drift inspection keeps the plain comparison, whose
// repair restores the saved entry rather than parameters nobody saved.
func (s *Service) verifyShapeEntry(ctx context.Context, sh ShapeSpec, qs []tcQdisc) (*ShapeVerification, *AppliedQueue) {
	v := readShapeVerification(ctx, sh)
	if v.Status != "verified" {
		return v, nil
	}
	applied, err := s.appliedQueue(ctx, sh)
	if err != nil {
		v.Status, v.Reason = "unknown", err.Error()
		return v, nil
	}
	if applied == nil {
		if sh.hasRoot() {
			v.Reason = "applied before parameters were kept; only the saved limits were compared"
		}
		return v, nil
	}
	if moved := compareApplied(applied, parameterQueue(sh, qs)); len(moved) > 0 {
		v.Status = "drift"
		v.Reason = "changed outside the dashboard since it was applied: " + strings.Join(moved, "; ")
	}
	return v, applied
}
