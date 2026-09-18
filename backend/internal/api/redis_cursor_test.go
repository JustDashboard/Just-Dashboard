package api

import (
	"math"
	"testing"
)

func TestRedisCursorAcceptsFullUnsignedRange(t *testing.T) {
	for raw, want := range map[string]uint64{"": 0, "0": 0, "18446744073709551615": math.MaxUint64, "9223372036854775808": 1 << 63} {
		got, err := parseRedisCursor(raw)
		if err != nil || got != want {
			t.Errorf("cursor %q: %d %v", raw, got, err)
		}
	}
	for _, raw := range []string{"-1", "18446744073709551616", "1.5", "NaN", "1e3"} {
		if _, err := parseRedisCursor(raw); err == nil {
			t.Errorf("invalid cursor accepted: %q", raw)
		}
	}
}
