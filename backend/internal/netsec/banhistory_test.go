package netsec

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Real fail2ban.log lines. The action lines are what matter; "Found" arrives
// once per failed attempt and would bury them.
const fail2banLog = `2026-08-20 03:15:19,001 fail2ban.filter         [812]: INFO    [sshd] Found 203.0.113.9 - 2026-08-20 03:15:19
2026-08-20 03:15:22,123 fail2ban.actions        [812]: NOTICE  [sshd] Ban 203.0.113.9
2026-08-20 03:20:01,900 fail2ban.actions        [812]: NOTICE  [nginx-botsearch] Ban 198.51.100.7
2026-08-20 04:15:22,456 fail2ban.actions        [812]: NOTICE  [sshd] Unban 203.0.113.9
2026-08-20 04:16:00,010 fail2ban.filter         [812]: INFO    [sshd] Found 192.0.2.44 - 2026-08-20 04:16:00
2026-08-20 05:00:00,000 fail2ban.actions        [812]: NOTICE  [sshd] Ban 2001:db8::42
`

func TestParseBanLine(t *testing.T) {
	var events []BanEvent
	for _, line := range strings.Split(fail2banLog, "\n") {
		if ev, ok := parseBanLine(line); ok {
			events = append(events, ev)
		}
	}
	if len(events) != 4 {
		t.Fatalf("parsed %d events, want the 4 actions and neither Found line: %+v", len(events), events)
	}

	first := events[0]
	if first.Action != "ban" || first.Jail != "sshd" || first.IP != "203.0.113.9" {
		t.Errorf("first event = %+v", first)
	}
	if first.At.Format("2006-01-02 15:04:05") != "2026-08-20 03:15:22" {
		t.Errorf("time = %v", first.At)
	}

	// A jail name with a hyphen is ordinary and must not truncate.
	if events[1].Jail != "nginx-botsearch" {
		t.Errorf("jail = %q", events[1].Jail)
	}
	if events[2].Action != "unban" {
		t.Errorf("action = %q, want unban", events[2].Action)
	}
	// IPv6 addresses contain colons, which a looser pattern would cut short.
	if events[3].IP != "2001:db8::42" {
		t.Errorf("ip = %q", events[3].IP)
	}
}

func TestParseBanLineIgnoresEverythingElse(t *testing.T) {
	for _, line := range []string{
		"",
		"not a log line at all",
		"2026-08-20 03:15:19,001 fail2ban.filter         [812]: INFO    [sshd] Found 203.0.113.9",
		// fail2ban re-asserts existing bans when it starts. A restored ban is
		// not a new one, and counting it as an event would report a wave of
		// attacks every time the service restarted.
		"2026-08-20 03:15:22,123 fail2ban.actions        [812]: NOTICE  [sshd] Restore Ban 203.0.113.9",
	} {
		if ev, ok := parseBanLine(line); ok {
			t.Errorf("accepted a line it should have skipped: %q -> %+v", line, ev)
		}
	}
}

// A busy host's fail2ban.log runs to hundreds of megabytes, and three readers
// each open it every minute or two. Only the tail is read past the cap, and the
// line the seek tears in half is dropped rather than parsed.
func TestBanHistoryReadsOnlyTheTailOfALargeLog(t *testing.T) {
	previousCap, previousLogs := banLogTailBytes, fail2banLogs
	t.Cleanup(func() { banLogTailBytes, fail2banLogs = previousCap, previousLogs })

	line := func(n int) string {
		return fmt.Sprintf("2026-09-%02d 03:15:22,123 fail2ban.actions [1234]: NOTICE  [sshd] Ban 203.0.113.%d\n", 1+n%28, n%250)
	}
	var b strings.Builder
	lastTen := 0
	for i := 0; i < 200; i++ {
		b.WriteString(line(i))
		if i >= 190 {
			lastTen += len(line(i))
		}
	}
	path := filepath.Join(t.TempDir(), "fail2ban.log")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	fail2banLogs = []string{path}

	// Everything fits: every line is an event.
	banLogTailBytes = 1 << 20
	all, err := (&Service{}).BanHistory(context.Background(), 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 200 {
		t.Fatalf("read %d events from an uncapped log, want 200", len(all))
	}

	// A cap smaller than the file: fewer events, all of them well-formed, and
	// the torn first line contributes nothing.
	banLogTailBytes = int64(lastTen + 7)
	tail, err := (&Service{}).BanHistory(context.Background(), 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) != 10 {
		t.Fatalf("read %d events from a capped log, want the 10 whole lines in the tail", len(tail))
	}
	for _, ev := range tail {
		if ev.Jail != "sshd" || ev.Action != "ban" || !strings.HasPrefix(ev.IP, "203.0.113.") {
			t.Errorf("a torn line was parsed as an event: %+v", ev)
		}
	}
}
