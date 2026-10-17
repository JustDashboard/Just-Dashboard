package backups

import (
	"testing"
	"time"
)

func TestNextScheduledReadsTheSchedulersOwnZone(t *testing.T) {
	after := time.Date(2026, time.October, 17, 2, 30, 0, 0, time.Local)
	next, ok := NextScheduled("0 3 * * *", after)
	if !ok || !next.Equal(time.Date(2026, time.October, 17, 3, 0, 0, 0, time.Local)) {
		t.Fatalf("daily at three after 02:30 = %v, %v", next, ok)
	}
	if next, ok := NextScheduled("@weekly", after); !ok || !next.After(after) || next.Weekday() != time.Sunday {
		t.Fatalf("@weekly = %v, %v", next, ok)
	}
	for _, expr := range []string{"", "not a schedule", "61 * * * *"} {
		if _, ok := NextScheduled(expr, after); ok {
			t.Fatalf("%q fires", expr)
		}
	}
}
