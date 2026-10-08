package netx

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestHostCommandPreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := runOnHost(ctx, "true")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("host command lost cancellation: %v", err)
	}
}

func TestHostCommandCancellationStopsWrappedChildren(t *testing.T) {
	for _, stdin := range [][]byte{nil, []byte("input")} {
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		started := time.Now()
		_, err := runOnHostStdin(ctx, stdin, "sh", "-c", "cat >/dev/null; sleep 30 & wait")
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 2*time.Second {
			t.Fatalf("wrapped host command survived cancellation: %v", err)
		}
	}
}
