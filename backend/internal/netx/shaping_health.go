package netx

import (
	"context"
	"errors"
	"fmt"
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
