package proxysvc

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// nginx's error log, grouped and said in plain words.
//
// The error log is where nginx says why a request failed — the 502 in the
// access log is "your app is not running on 127.0.0.1:3000" here — but it is
// written for someone who already knows what errno 111 is. This reads the
// recent end of it, folds the lines that are the same failure into one group,
// and puts the operator's next move beside each group where the line is one
// nginx is known to write.

// errorTailBytes bounds one read. A busy host writes a line per failed
// request, and the page asks about the last hour, not the log's history.
const errorTailBytes = 8 << 20

// maxErrorGroups bounds the answer: past this many distinct failures the rest
// are the long tail, and the report says how many lines it did not group.
const maxErrorGroups = 60

// ErrorGroup is one kind of failure over the window.
type ErrorGroup struct {
	// Pattern is the message with what varies per line — paths, sizes,
	// addresses in quotes — replaced, which is what the lines were grouped by.
	Pattern string    `json:"pattern"`
	Level   string    `json:"level"`
	Count   int       `json:"count"`
	First   time.Time `json:"first"`
	Last    time.Time `json:"last"`
	// Sample is the newest line of the group as nginx wrote it.
	Sample   string `json:"sample"`
	Upstream string `json:"upstream,omitempty"`
	Server   string `json:"server,omitempty"`
	// Title is the failure in plain words and Advice what to do about it;
	// both are empty for a line this does not recognise, and the page then
	// shows the pattern.
	Title  string `json:"title,omitempty"`
	Advice string `json:"advice,omitempty"`
}

// ErrorReport is the window's groups and what they were read from.
type ErrorReport struct {
	Site string `json:"site,omitempty"`
	// Scope says whose log this is: "site" for one the site names, "shared"
	// for nginx's own narrowed to the site's server names, "main" for nginx's
	// own whole.
	Scope  string    `json:"scope"`
	Path   string    `json:"path"`
	Exists bool      `json:"exists"`
	Since  time.Time `json:"since"`
	// Complete is false when the read's byte bound cut into the window, so
	// every count is a floor.
	Complete  bool         `json:"complete"`
	Lines     int          `json:"lines"`
	Ungrouped int          `json:"ungrouped,omitempty"`
	Groups    []ErrorGroup `json:"groups"`
	Note      string       `json:"note,omitempty"`
}

// SiteErrors reads the error log a site writes to — its own, or nginx's main
// one narrowed to the site's names — over the window since `since`. An empty
// site reads nginx's main error log whole.
func (s *Service) SiteErrors(ctx context.Context, site string, since time.Time) (ErrorReport, error) {
	report := ErrorReport{Site: site, Since: since, Groups: []ErrorGroup{}}
	var names []string
	var bodyLimit string
	if site != "" {
		logs, err := s.SiteLogsFor(site)
		if err != nil {
			return report, err
		}
		switch {
		case logs.Error != "":
			report.Scope, report.Path = "site", logs.Error
		case logs.ErrorNote != "":
			// The site names a log this does not read; nginx's own has none
			// of its lines, so reading that instead would report a quiet site.
			report.Scope, report.Note = "site", logs.ErrorNote
			return report, nil
		case len(logs.ServerNames) == 0:
			report.Scope = "shared"
			report.Note = "This site names no server_name, so its lines in nginx's shared error log cannot be told apart from other sites'."
			return report, nil
		default:
			names = logs.ServerNames
			report.Scope = "shared"
		}
		bodyLimit = s.siteBodyLimit(logs.File)
	} else {
		report.Scope = "main"
	}
	if report.Path == "" {
		path, err := confineLog(s.mainErrorLog(ctx))
		if err != nil {
			report.Note = fmt.Sprintf("nginx's error log is outside %s, which the dashboard does not read.", nginxLogRoot)
			return report, nil
		}
		report.Path = path
	}
	lines, reachedStart, exists, err := tailLines(report.Path, errorTailBytes)
	if err != nil {
		return report, err
	}
	report.Exists = exists
	report.Complete = reachedStart
	groups := map[string]*ErrorGroup{}
	largest := map[string]float64{}
	for _, raw := range lines {
		line, ok := parseErrorLine(raw)
		if !ok {
			continue
		}
		// The window's start is inside the read when any line before it was
		// read; the bound only cut into the window when none was.
		if line.at.Before(since) {
			report.Complete = true
			continue
		}
		if len(names) > 0 && !servesName(names, line.server, line.host) {
			continue
		}
		report.Lines++
		key := line.level + "\x00" + line.pattern + "\x00" + line.upstream
		g := groups[key]
		if g == nil {
			if len(groups) >= maxErrorGroups {
				report.Ungrouped++
				continue
			}
			g = &ErrorGroup{Pattern: line.pattern, Level: line.level, First: line.at, Upstream: line.upstream, Server: line.server}
			g.Title, g.Advice = explainError(line, bodyLimit)
			groups[key] = g
		}
		g.Count++
		if line.at.Before(g.First) {
			g.First = line.at
		}
		if !line.at.Before(g.Last) {
			g.Last = line.at
			g.Sample = clip(raw, 600)
		}
		if size := bodySize(line.message); size > largest[key] {
			largest[key] = size
		}
	}
	for key, g := range groups {
		// The advice for a body that was too large names a size, and the
		// size to name is the largest refused, not the first.
		if size := largest[key]; size > 0 {
			g.Advice = bodyAdvice(size, bodyLimit)
		}
		report.Groups = append(report.Groups, *g)
	}
	sort.Slice(report.Groups, func(i, j int) bool {
		a, b := report.Groups[i], report.Groups[j]
		if rank(a.Level) != rank(b.Level) {
			return rank(a.Level) < rank(b.Level)
		}
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.Last.After(b.Last)
	})
	return report, nil
}

// mainErrorLog is the error log nginx's own configuration names: the http
// block's, since that is where a request's failure goes when a site names
// none, then the main context's, then the path nginx is built with. It reads
// what nginx loads, and falls back to the built-in path when that cannot be
// dumped — the log is still there to read.
func (s *Service) mainErrorLog(ctx context.Context) string {
	fallback := nginxLogRoot + "/error.log"
	files, err := s.EffectiveConfig(ctx)
	if err != nil {
		return fallback
	}
	tree, err := NginxTree(files)
	if err != nil {
		return fallback
	}
	var main string
	for _, d := range tree {
		if d.Name == "error_log" && len(d.Args) > 0 && main == "" {
			main = d.Args[0]
		}
		if d.Name != "http" {
			continue
		}
		for _, inner := range d.Block {
			if inner.Name == "error_log" && len(inner.Args) > 0 {
				return inner.Args[0]
			}
		}
	}
	if main != "" {
		return main
	}
	return fallback
}

// siteBodyLimit is the upload limit the site sets, for the advice on a body
// that was too large: raising it needs to know where it starts.
func (s *Service) siteBodyLimit(file string) string {
	content, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	directives, err := ParseNginxFile(file, string(content), []string{"http"})
	if err != nil {
		return ""
	}
	if d := serverLogDirective(directives, "client_max_body_size"); d != nil && len(d.Args) > 0 {
		return d.Args[0]
	}
	return ""
}

// tailLines reads the last `limit` bytes of a file as whole lines, and says
// whether that reached the file's start.
func tailLines(path string, limit int64) (lines []string, reachedStart, exists bool, err error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, true, false, nil
		}
		return nil, false, false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, false, true, err
	}
	offset := info.Size() - limit
	if offset < 0 {
		offset = 0
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, false, true, err
	}
	data, err := io.ReadAll(io.LimitReader(file, limit))
	if err != nil {
		return nil, false, true, err
	}
	text := string(data)
	if offset > 0 {
		// The first line was cut by the seek; it is the one line that is not
		// what nginx wrote.
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = text[i+1:]
		}
	}
	return strings.Split(text, "\n"), offset == 0, true, nil
}

type errorLine struct {
	at       time.Time
	level    string
	message  string
	pattern  string
	server   string
	host     string
	upstream string
}

// errorLineRe matches nginx's error-log line: a local timestamp, the level,
// the worker, and the connection number when a request was involved.
var errorLineRe = regexp.MustCompile(`^(\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}) \[([a-z]+)\] \d+#\d+: (?:\*\d+ )?(.*)$`)

var (
	errorUpstreamRe = regexp.MustCompile(`, upstream: "([^"]*)"`)
	errorServerRe   = regexp.MustCompile(`, server: ([^,]*)`)
	errorHostRe     = regexp.MustCompile(`, host: "([^"]*)"`)
	quotedRe        = regexp.MustCompile(`"[^"]*"`)
	numberRe        = regexp.MustCompile(`\b\d+\b`)
)

func parseErrorLine(raw string) (errorLine, bool) {
	m := errorLineRe.FindStringSubmatch(strings.TrimRight(raw, "\r"))
	if m == nil {
		return errorLine{}, false
	}
	// nginx writes its own local time with no zone. The dashboard runs on the
	// same host, so its zone is nginx's.
	at, err := time.ParseInLocation("2006/01/02 15:04:05", m[1], time.Local)
	if err != nil {
		return errorLine{}, false
	}
	line := errorLine{at: at, level: m[2], message: m[3]}
	// What follows the message is the request it was about; the message is
	// what the lines are grouped by.
	if i := strings.Index(line.message, ", client: "); i >= 0 {
		context := line.message[i:]
		line.message = line.message[:i]
		if u := errorUpstreamRe.FindStringSubmatch(context); u != nil {
			line.upstream = upstreamAddress(u[1])
		}
		if s := errorServerRe.FindStringSubmatch(context); s != nil {
			line.server = strings.TrimSpace(s[1])
		}
		if h := errorHostRe.FindStringSubmatch(context); h != nil {
			line.host = h[1]
		}
	}
	line.pattern = numberRe.ReplaceAllString(quotedRe.ReplaceAllString(line.message, `"…"`), "N")
	return line, true
}

// upstreamAddress reduces "http://127.0.0.1:3000/api/x" to the address the
// app listens on, which is what the operator starts it on.
func upstreamAddress(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return raw
}

func servesName(names []string, server, host string) bool {
	for _, n := range names {
		if n == server || n == host || (strings.HasPrefix(n, "*.") && strings.HasSuffix(host, n[1:])) {
			return true
		}
	}
	return false
}

// rank orders the levels worst first.
func rank(level string) int {
	for i, l := range []string{"emerg", "alert", "crit", "error", "warn", "notice", "info", "debug"} {
		if l == level {
			return i
		}
	}
	return 99
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

var tooLargeBodyRe = regexp.MustCompile(`too large body: (\d+) bytes`)

// explainError is the table of the failures nginx is known to write, in the
// words an operator acts on. Only lines whose meaning is certain are here: a
// wrong explanation sends someone after the wrong thing, and an unexplained
// line still shows its pattern.
func explainError(line errorLine, bodyLimit string) (string, string) {
	msg := line.message
	at := line.upstream
	if at == "" {
		at = "its upstream"
	}
	switch {
	case strings.Contains(msg, "(111: Connection refused) while connecting to upstream"):
		return "Your app is not running on " + at,
			"Nothing accepted the connection there. Start the app, or point the site at the port it really listens on."
	case strings.Contains(msg, "(2: No such file or directory) while connecting to upstream"):
		return "Your app's socket does not exist",
			"The unix socket the site forwards to is missing, which means the app is not running or listens somewhere else."
	case strings.Contains(msg, "(13: Permission denied) while connecting to upstream"):
		return "nginx may not connect to your app",
			"Permission was denied connecting to " + at + ". On a socket, give nginx's user access to it; with SELinux, allow httpd_can_network_connect."
	case strings.Contains(msg, "upstream timed out") && strings.Contains(msg, "while connecting"):
		return "Your app did not accept the connection in time",
			"Connecting to " + at + " timed out. The app is overloaded or a firewall drops the connection."
	case strings.Contains(msg, "upstream timed out"):
		return "Your app took too long to answer",
			"The app on " + at + " accepted the request but did not answer within proxy_read_timeout (60s unless the site sets it). Find the slow request, or raise proxy_read_timeout for it."
	case strings.Contains(msg, "no live upstreams"):
		return "Every server behind this site is marked down",
			"nginx stopped sending to " + at + " after repeated failures. Fix the first failure listed for it; nginx retries it after fail_timeout."
	case strings.Contains(msg, "upstream prematurely closed connection"):
		return "Your app closed the connection without answering",
			"The app on " + at + " dropped the request mid-way, which is usually a crash or a restart. Its own logs say which."
	case strings.Contains(msg, "(104: Connection reset by peer) while reading response header from upstream"):
		return "Your app reset the connection",
			"The app on " + at + " reset the request before answering, which is usually a crash or a restart. Its own logs say which."
	case strings.Contains(msg, "upstream sent too big header"):
		return "Your app sent headers larger than nginx's buffer",
			"Large cookies are the usual cause. Raise proxy_buffer_size to 16k (and proxy_buffers to 8 16k) for this site."
	case strings.Contains(msg, "host not found in upstream"):
		return "An upstream name does not resolve",
			"The name the site forwards to has no address. Fix the name, or the DNS it depends on."
	case strings.HasPrefix(msg, "client intended to send too large body"):
		return "An upload was larger than the site allows", bodyAdvice(bodySize(msg), bodyLimit)
	case strings.Contains(msg, "failed (2: No such file or directory)") && strings.HasPrefix(msg, "open()"):
		return "A file that was asked for does not exist",
			"Usually a missing favicon or a scanner probing for files; it matters only when the path is one the site should serve."
	case strings.Contains(msg, "failed (13: Permission denied)") && strings.HasPrefix(msg, "open()"):
		return "nginx cannot read a file it serves",
			"nginx's worker user has no read access to it. Fix the file's owner or mode, and its directories' execute bit."
	case strings.Contains(msg, "directory index of") && strings.Contains(msg, "is forbidden"):
		return "A folder was asked for and has no index file",
			"Add an index.html there, or leave it: it is refused, not served."
	case strings.Contains(msg, "access forbidden by rule"):
		return "A request was refused by the site's allow and deny rules", ""
	case strings.Contains(msg, "was not found in") || strings.Contains(msg, "password mismatch"):
		return "A sign-in failed on a password-protected site",
			"Many from one client is someone guessing; block the address from Security if so."
	case strings.HasPrefix(msg, "limiting requests"):
		return "Requests were held back by a rate limit",
			"Clients sent faster than the site's limit_req allows. Raise its rate or burst if they are real users."
	case strings.HasPrefix(msg, "limiting connections"):
		return "Connections were held back by a limit", ""
	case strings.Contains(msg, "SSL_do_handshake() failed") || strings.Contains(msg, "SSL_read() failed"):
		return "A TLS handshake failed",
			"Usually an old client or a scanner; it matters only when real visitors report the site will not open."
	case strings.Contains(msg, "worker_connections are not enough"):
		return "nginx ran out of connections",
			"Raise worker_connections in the events block of nginx.conf."
	case strings.Contains(msg, "conflicting server name"):
		return "Two sites claim the same name",
			"nginx answers the name from the first site it read and ignores the other. Remove the name from one of them."
	}
	return "", ""
}

// bodySize is the size a too-large body line reports, in bytes, or zero.
func bodySize(msg string) float64 {
	m := tooLargeBodyRe.FindStringSubmatch(msg)
	if m == nil {
		return 0
	}
	size, _ := strconv.ParseFloat(m[1], 64)
	return size
}

// bodyAdvice names the limit to raise to: the next power-of-two megabytes
// above the largest body refused, so one change fits what was turned away.
func bodyAdvice(size float64, limit string) string {
	if size <= 0 {
		return "Raise client_max_body_size for this site."
	}
	mb := size / (1 << 20)
	next := int(math.Pow(2, math.Ceil(math.Log2(math.Max(mb, 1)))))
	if float64(next) <= mb {
		next *= 2
	}
	current := limit
	if current == "" {
		current = "1m, nginx's default"
	}
	return fmt.Sprintf("A client sent %.1f MB and the limit is %s. Raise the upload limit (client_max_body_size) to %dm.", mb, current, next)
}
