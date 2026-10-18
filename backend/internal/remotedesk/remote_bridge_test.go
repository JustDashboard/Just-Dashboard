package remotedesk

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestBridgeAddressIsHostAndPort(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"100.64.0.5:8080", "100.64.0.5:8080"},
		{" office-pc:4823 ", "office-pc:4823"},
		{"[fd7a:115c:a1e0::5]:8080", "[fd7a:115c:a1e0::5]:8080"},
		{"100.64.0.5", ""},
		{"100.64.0.5:0", ""},
		{"100.64.0.5:http", ""},
		{"bad host:8080", ""},
	} {
		got, err := ParseBridgeAddress(c.in)
		if (err == nil) != (c.want != "") || got != c.want {
			t.Errorf("%q: %q, %v", c.in, got, err)
		}
		if err != nil && !strings.Contains(err.Error(), "JD_REMOTE_DESKTOP_BRIDGE") {
			t.Errorf("%q: the error must name the setting, got %v", c.in, err)
		}
	}
}

// A bridge elsewhere is never installed or removed from here: its status is
// whether it answers, held for a few seconds since the proxy asks on every
// file, and its WebRTC is taken to be published on its own address.
func TestRemoteBridgeIsProbedAndLeftWhereItIs(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	var probes int
	var down error
	bridge := &remoteBridge{
		address: "100.64.0.5:8080",
		probe: func(context.Context, string) error {
			probes++
			return down
		},
		now: func() time.Time { return now },
	}
	ctx := context.Background()
	st := bridge.Status(ctx)
	if st.State != EngineRunning || st.Address != "100.64.0.5:8080" || st.Media != "100.64.0.5" || st.Version != BridgeVersion || st.Error != "" {
		t.Fatalf("status %+v", st)
	}
	bridge.Status(ctx)
	if probes != 1 {
		t.Fatalf("a fresh status asked again: %d probes", probes)
	}
	now = now.Add(remoteBridgeStatusFor + time.Second)
	down = errors.New("connection refused")
	st = bridge.Status(ctx)
	if probes != 2 || st.State != EngineStopped || !strings.Contains(st.Error, "100.64.0.5:8080") || !strings.Contains(st.Error, "connection refused") {
		t.Fatalf("status after the probe failed: %+v, %d probes", st, probes)
	}
	// Install is the status, asked afresh: an error names what to do when it
	// is down, and nothing when it answers.
	err := bridge.Install(ctx, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "JD_REMOTE_DESKTOP_BRIDGE") || probes != 3 {
		t.Fatalf("install of a bridge that is down: %v, %d probes", err, probes)
	}
	down = nil
	var lines []string
	if err := bridge.Install(ctx, func(line string) { lines = append(lines, line) }); err != nil || len(lines) != 1 || !strings.Contains(lines[0], "another machine") {
		t.Fatalf("install of a bridge that answers: %v %q", err, lines)
	}
	if err := bridge.Remove(ctx); err == nil || !strings.Contains(err.Error(), "100.64.0.5:8080") {
		t.Fatalf("remove: %v", err)
	}
}
