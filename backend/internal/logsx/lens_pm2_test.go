package logsx

import "testing"

// PM2's --time prefix, as ~/.pm2/pm2.log on this host spells it
// ("2026-09-15T03:08:17: PM2 log: …"), before this host's own PM2 app output
// (~/.pm2/logs/just-dashboard-web-*.log). The prefix is the host's local
// time; "YYYY-MM-DD HH:mm:ss Z" names its zone.
func TestLensPM2Prefix(t *testing.T) {
	appLensZone(t)
	appLensCheck(t, "pm2", []string{
		"2026-09-15T03:08:17: ▲ Next.js 16.3.5",
		"2026-09-15T03:08:17: - Local:         http://100.110.34.31:3400",
		"2026-09-15T03:08:17: ✓ Ready in 174ms",
		`2026-09-15 03:09:02 +00:00: error: script "start" exited with code 143`,
		// Without the setting a line is the application's own, unstamped.
		`$ next start --hostname "100.110.34.31" --port "3400"`,
		// PM2 prefixes a chunk, so a stack's later lines arrive without it.
		`2026-09-15T03:10:00: Error: Failed to find Server Action "x". This request might be from an older or newer deployment.`,
		"Read more: https://nextjs.org/docs/messages/failed-to-find-server-action",
		"    at ignore-listed frames",
	}, []appLensWant{
		{at: "2026-09-15T00:08:17Z"},
		{at: "2026-09-15T00:08:17Z"},
		{event: "startup", at: "2026-09-15T00:08:17Z", attrs: map[string]string{"port": "3400", "duration_ms": "174"}},
		{event: "shutdown", level: "info", at: "2026-09-15T03:09:02Z"},
		{},
		{event: "exception", level: "error", at: "2026-09-15T00:10:00Z", attrs: map[string]string{"error": `Error: Failed to find Server Action "x". This request might be from an older or newer deployment.`}},
		{level: "error", cont: true},
		{level: "error", cont: true},
	})
}

// Every event the pm2 lens declares, each from a real line behind PM2's prefix.
func TestLensPM2Events(t *testing.T) {
	lines := []struct{ text, event string }{
		{"GET /dev 200 0.224 ms - 2", "request"},
		{`Error: The Server Reference ID did not match the expected format. Received "y".`, "exception"},
		{"✓ Ready in 303ms", "startup"},
		{`error: script "start" exited with code 130`, "shutdown"},
		{"FATAL ERROR: Reached heap limit Allocation failed - JavaScript heap out of memory", "oom"},
		{"Error: listen EADDRINUSE: address already in use :::3000", "port_in_use"},
		{"Error: P1001: Can't reach database server at `db-4.jd.internal:5432`", "db_unreachable"},
		{"The table `public.products` does not exist in the current database.", "schema_missing"},
		{"** (RuntimeError) environment variable DATABASE_URL is missing.", "env_missing"},
		{"(node:1) [DEP0040] DeprecationWarning: The `punycode` module is deprecated. Please use a userland alternative instead.", "deprecation"},
	}
	lens, _ := LensByID("pm2")
	seen := map[string]bool{}
	for _, c := range lines {
		// A reader per line, so no line is read as the continuation of another.
		l := ParseLine("2026-09-15T03:08:17: "+c.text, "")
		lens.New().Read(&l)
		if l.Event != c.event || l.Timestamp == nil {
			t.Errorf("%q: event %q stamp %v, want %q and a stamp", c.text, l.Event, l.Timestamp, c.event)
		}
		seen[l.Event] = true
	}
	for _, event := range lens.Events {
		if !seen[event] {
			t.Errorf("event %q is declared and not exercised", event)
		}
	}
}

func BenchmarkLensPM2(b *testing.B) {
	appLensBenchmark(b, "pm2", []string{
		"2026-09-15T03:08:17: ▲ Next.js 16.3.5",
		"2026-09-15T03:08:17: ✓ Ready in 174ms",
		`$ next start --hostname "100.110.34.31" --port "3400"`,
		`error: script "start" exited with code 143`,
		"PM2 test running",
	})
}
