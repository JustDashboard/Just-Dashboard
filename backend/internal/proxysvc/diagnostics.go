package proxysvc

import (
	"encoding/json"
	"path/filepath"
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
	nginxStderrLine = regexp.MustCompile(`^nginx: \[(emerg|alert|crit|error|warn|notice|info)\] (.+)$`)
	nginxLogLine    = regexp.MustCompile(`^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2} \[(emerg|alert|crit|error|warn|notice|info)\] \d+#\d+: (?:\*\d+ )?(.+)$`)
	nginxPlace      = regexp.MustCompile(`^(/.*):(\d+)$`)
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
		d := Diagnostic{Level: m[1]}
		d.Message, d.File, d.Line = nginxPosition(m[2])
		out = append(out, d)
	}
	return out
}

// nginxPosition splits the " in /path:N" nginx appends to a message it can
// place. The path is the included file's name as it is, spaces and all —
// conf.d/*.conf takes "zz spaced name.conf" — so it cannot end at the first
// space. What marks where it starts is that nginx quotes what it repeats from
// the configuration: an " in /" inside quotes is part of the message, as in
// `open() "/srv/a in /b.conf" failed`, and the first one outside them is the
// position. Quotes that do not pair leave the last " in /" as the best guess.
func nginxPosition(text string) (message, file string, line int) {
	quotes, split := 0, -1
	for i := 0; i < len(text); i++ {
		if text[i] == '"' {
			quotes++
			continue
		}
		if !strings.HasPrefix(text[i:], " in /") || !nginxPlace.MatchString(text[i+4:]) {
			continue
		}
		split = i
		if quotes%2 == 0 {
			break
		}
	}
	if split < 0 {
		return text, "", 0
	}
	m := nginxPlace.FindStringSubmatch(text[split+4:])
	line, _ = strconv.Atoi(m[2])
	return text[:split], m[1], line
}

// Caddy writes a Caddyfile position into an error message in one of two
// places. Where it can, it appends it: "…, at /etc/caddy/Caddyfile:2 import
// chain", and that is read first, because the message before it can quote an
// upstream such as 'http://127.0.0.1:3000' whose "//127.0.0.1:3000" reads
// like a path and a line. Otherwise the position leads: "/etc/caddy/
// Caddyfile:3: unrecognized directive". There a path must start a word and
// start with a slash or name a Caddyfile, so neither a listen address such
// as 127.0.0.1:443 nor the "//" of a URL is taken for one.
var (
	caddyAt       = regexp.MustCompile(`, at (\S+):(\d+)(?:\s|$)`)
	caddyLocation = regexp.MustCompile(`(?:^|[\s'"(])(/[^\s:'"]+|[^\s:'"]*Caddyfile[^\s:'"]*):(\d+)`)
)

// caddyPosition is the file and line an error message points at, if any.
func caddyPosition(message string) (string, int) {
	var m []string
	if all := caddyAt.FindAllStringSubmatch(message, -1); len(all) > 0 {
		m = all[len(all)-1]
	} else if m = caddyLocation.FindStringSubmatch(message); m == nil {
		return "", 0
	}
	line, _ := strconv.Atoi(m[2])
	return m[1], line
}

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
			d.File, d.Line = caddyPosition(d.Message)
			out = append(out, d)
		}
	}
	return out
}

// diagnose fills a validation result's diagnostics from its output.
//
// A diagnostic's file is named with its symlinks resolved, the way
// allowedPath names the file being edited. nginx names the path it included,
// which for a Debian site is the sites-enabled link, so a page comparing that
// with the sites-available file it opened would never find its line.
func (r *ValidationResult) diagnose(engine string) {
	if engine == "caddy" {
		r.Diagnostics = ParseCaddyDiagnostics(r.Output)
	} else {
		r.Diagnostics = ParseNginxDiagnostics(r.Output)
	}
	for i, d := range r.Diagnostics {
		if d.File != "" {
			r.Diagnostics[i].File = resolvedFile(d.File)
		}
		if d.Level == "warn" {
			r.Warnings++
		}
	}
}

// resolvedFile is path with its symlinks resolved, or path itself when it
// cannot be: a file the engine names need not exist where this process looks.
func resolvedFile(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}
