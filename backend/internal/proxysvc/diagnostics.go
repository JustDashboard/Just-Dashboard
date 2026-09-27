package proxysvc

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// Diagnostic is one message from the engine's config test, with the file and
// line it points at when it names one.
//
// The test's verdict is only its exit code, and nginx exits 0 through warnings
// that matter: a second site claiming a server name another already holds is
// "ignored" with a [warn], and that site then never serves. Reading the lines
// is what lets a page point at the file and line instead of printing a wall of
// text under a green tick.
type Diagnostic struct {
	// Level is the engine's own word: emerg, alert, crit, error, warn, notice
	// or info for nginx; error, warn, panic or fatal for Caddy.
	Level   string `json:"level"`
	Message string `json:"message"`
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
}

// nginx writes a test's messages in one of two shapes, depending on whether
// it could open its startup error log. When it can — root on the host, which
// is where the dashboard runs it — the log gets the timestamped line and
// stderr gets `nginx: [warn] message in /path:12`. When it cannot, the
// timestamped line itself goes to stderr. Both are nginx 1.26's own output.
var (
	nginxStderrLine = regexp.MustCompile(`^nginx: \[(emerg|alert|crit|error|warn|notice|info)\] (.+?)(?: in (\S+):(\d+))?$`)
	nginxLogLine    = regexp.MustCompile(`^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2} \[(emerg|alert|crit|error|warn|notice|info)\] \d+#\d+: (?:\*\d+ )?(.+?)(?: in (\S+):(\d+))?$`)
)

// ParseNginxDiagnostics reads the leveled lines out of `nginx -t` output. The
// closing "syntax is ok" and "test failed" lines carry no level and are not
// diagnostics; the verdict is the exit code, which the caller already has.
func ParseNginxDiagnostics(output string) []Diagnostic {
	out := []Diagnostic{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		m := nginxStderrLine.FindStringSubmatch(line)
		if m == nil {
			m = nginxLogLine.FindStringSubmatch(line)
		}
		if m == nil {
			continue
		}
		d := Diagnostic{Level: m[1], Message: m[2], File: m[3]}
		d.Line, _ = strconv.Atoi(m[4])
		out = append(out, d)
	}
	return out
}

// caddyLocation finds a Caddyfile position inside an error message, which
// Caddy writes as "/etc/caddy/Caddyfile:3: unrecognized directive" or as
// "... at /etc/caddy/Caddyfile:2 import chain". A path is required to start
// with a slash or name a Caddyfile so a listen address such as 127.0.0.1:443
// is not taken for one.
var caddyLocation = regexp.MustCompile(`(/[^\s:'"]+|[^\s:'"]*Caddyfile[^\s:'"]*):(\d+)`)

// ParseCaddyDiagnostics reads `caddy validate` output: its JSON log lines at
// warn and above, which carry file and line as fields, and the one `Error:`
// line a failed adaptation ends with. Best effort by nature — Caddy's error
// text is not a format — so what cannot be placed is kept without a location
// rather than dropped.
func ParseCaddyDiagnostics(output string) []Diagnostic {
	out := []Diagnostic{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "{"):
			var entry struct {
				Level string `json:"level"`
				Msg   string `json:"msg"`
				File  string `json:"file"`
				Line  int    `json:"line"`
			}
			if json.Unmarshal([]byte(line), &entry) != nil {
				continue
			}
			switch entry.Level {
			case "warn", "error", "panic", "fatal":
			default:
				continue
			}
			d := Diagnostic{Level: entry.Level, Message: entry.Msg}
			if entry.Line > 0 {
				d.File, d.Line = entry.File, entry.Line
			}
			out = append(out, d)
		case strings.HasPrefix(line, "Error: "):
			d := Diagnostic{Level: "error", Message: strings.TrimPrefix(line, "Error: ")}
			if m := caddyLocation.FindStringSubmatch(d.Message); m != nil {
				d.File = m[1]
				d.Line, _ = strconv.Atoi(m[2])
			}
			out = append(out, d)
		}
	}
	return out
}

// diagnose fills a validation result's diagnostics from its output.
func (r *ValidationResult) diagnose(engine string) {
	if engine == "caddy" {
		r.Diagnostics = ParseCaddyDiagnostics(r.Output)
	} else {
		r.Diagnostics = ParseNginxDiagnostics(r.Output)
	}
	for _, d := range r.Diagnostics {
		if d.Level == "warn" {
			r.Warnings++
		}
	}
}
