package netx

import (
	"context"
	"errors"
	"testing"
)

func TestHostCommandPreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := runOnHost(ctx, "true")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("host command lost cancellation: %v", err)
	}
}
