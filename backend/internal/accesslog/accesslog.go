// Package accesslog reads what a reverse proxy recorded about the requests it
// served, and turns it into the readings a deployment's Logs page shows.
//
// A container's stdout answers "what did the application print", which on a
// modern framework is nothing at all: a Next.js or Django production server
// logs its startup banner and then stays silent for weeks. The question an
// operator actually has — is it serving traffic, how fast, and how much of it
// is failing — is only answerable at the proxy, because that is the one place
// every request passes through regardless of what the application chose to
// write.
//
// Two formats arrive here, because the deployment section has two ingress
// drivers. The Docker Caddy ingress writes structured JSON, which carries the
// request duration. Managed host nginx writes the stock `combined` format,
// which does not — so latency is an optional reading rather than a column of
// zeros, and the page says which of the two it is looking at.
package accesslog

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Entry is one served request, in the shape the UI renders directly.
//
// Path and Query are split because they are read differently: a path is
// grouped — "which route is failing" — and a query never is, since one
// cache-buster turns every hit on a page into its own row.
//
// The shape is chosen for being held in memory by the hundred thousand, which
// is what the Store does with it: the instant is eight bytes rather than a
// formatted string, the duration is a value with a flag rather than a pointer
// to an allocation, and the strings that repeat across a whole record — a
// path, a user agent, a client address — are interned by the Store so a
// hundred thousand rows from one browser share one copy of its name. The
// wire shape is unchanged; MarshalJSON writes it.
type Entry struct {
	// Seq numbers the request within the process's record of its route, and
	// only rises. It is what a live tail continues from: exact, where "newer
	// than the last timestamp I saw" drops the second of two requests that
	// share a second — which nginx's format rounds every request to.
	Seq       uint64 `json:"seq,omitempty"`
	Method    string `json:"method"`
	Path      string `json:"path"`
	Query     string `json:"query,omitempty"`
	Host      string `json:"host,omitempty"`
	Proto     string `json:"proto,omitempty"`
	Status    int    `json:"status"`
	Size      int64  `json:"size"`
	RemoteIP  string `json:"remoteIp,omitempty"`
	UserAgent string `json:"userAgent,omitempty"`
	Referer   string `json:"referer,omitempty"`
	TLS       bool   `json:"tls,omitempty"`

	// at is unix nanoseconds, kept unexported so every consumer sorts and
	// buckets on the same value rather than re-parsing a string.
	at int64
	// duration is milliseconds, and timed says whether the format carried one
	// at all. nginx's stock `combined` has no $request_time, and a latency
	// column quietly reading zero is worse than one that says it does not know.
	duration float64
	timed    bool
}

// At is the instant this request was served.
func (e Entry) At() time.Time { return time.Unix(0, e.at).UTC() }

// Duration is how long the proxy took to answer, in milliseconds, and whether
// that was recorded at all.
func (e Entry) Duration() (float64, bool) { return e.duration, e.timed }

// MarshalJSON writes the wire shape: the instant as RFC 3339, and the duration
// only where one was measured.
func (e Entry) MarshalJSON() ([]byte, error) {
	// A defined type carries none of Entry's methods, which is what keeps this
	// from calling itself.
	type plain Entry
	out := struct {
		plain
		Time       string   `json:"time"`
		DurationMs *float64 `json:"durationMs,omitempty"`
	}{plain: plain(e), Time: e.At().Format(time.RFC3339Nano)}
	if e.timed {
		ms := e.duration
		out.DurationMs = &ms
	}
	return json.Marshal(out)
}

// Class is the status family, which is how a request log is actually read: no
// one scans for 418, they scan for "is anything 5xx".
func (e Entry) Class() string {
	switch {
	case e.Status >= 500:
		return "5xx"
	case e.Status >= 400:
		return "4xx"
	case e.Status >= 300:
		return "3xx"
	case e.Status >= 200:
		return "2xx"
	case e.Status >= 100:
		return "1xx"
	}
	return "other"
}

// Classes is the closed set the UI offers as chips, worst first.
var Classes = []string{"5xx", "4xx", "3xx", "2xx", "1xx", "other"}

// Format names how a line is spelled. It is reported alongside a reading so
// the page can explain a missing latency column rather than drawing an empty
// one.
type Format string

const (
	FormatCaddyJSON Format = "caddy-json"
	FormatCombined  Format = "nginx-combined"
)

// Parse reads one line in whichever of the two formats it is written in. The
// first byte decides: only JSON starts with a brace, and re-attempting a
// failed JSON unmarshal as a combined line costs nothing on the common path.
//
// A line that is neither — a rotation marker, a truncated tail, a blank —
// answers false rather than an error. A log is read from wherever the reader
// happened to seek to, so a partial first line is normal and not a fault.
func Parse(line string) (Entry, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return Entry{}, false
	}
	if line[0] == '{' {
		return parseCaddy(line)
	}
	return parseCombined(line)
}

// caddyLine is the subset of Caddy's access entry this reads. Caddy writes a
// great deal more per request; naming only what is rendered keeps the decode
// cheap and means an upstream addition cannot change what the page shows.
type caddyLine struct {
	TS      json.RawMessage `json:"ts"`
	Msg     string          `json:"msg"`
	Logger  string          `json:"logger"`
	Request struct {
		RemoteIP string              `json:"remote_ip"`
		ClientIP string              `json:"client_ip"`
		Proto    string              `json:"proto"`
		Method   string              `json:"method"`
		Host     string              `json:"host"`
		URI      string              `json:"uri"`
		Headers  map[string][]string `json:"headers"`
		TLS      *struct {
			Version uint16 `json:"version"`
		} `json:"tls"`
	} `json:"request"`
	Duration *float64 `json:"duration"`
	Size     int64    `json:"size"`
	Status   int      `json:"status"`
}

func parseCaddy(line string) (Entry, bool) {
	var raw caddyLine
	if json.Unmarshal([]byte(line), &raw) != nil {
		return Entry{}, false
	}
	// Caddy's own application log shares the file's shape but has no request.
	// Reading one as a served request would report a startup notice as a hit
	// with status 0.
	if raw.Request.Method == "" || raw.Status == 0 {
		return Entry{}, false
	}
	at, ok := caddyTime(raw.TS)
	if !ok {
		return Entry{}, false
	}
	entry := Entry{
		at: at.UnixNano(), Method: raw.Request.Method, Host: raw.Request.Host,
		Proto: raw.Request.Proto, Status: raw.Status, Size: raw.Size,
		RemoteIP: raw.Request.ClientIP, TLS: raw.Request.TLS != nil,
	}
	if entry.RemoteIP == "" {
		entry.RemoteIP = raw.Request.RemoteIP
	}
	entry.Path, entry.Query = splitURI(raw.Request.URI)
	entry.UserAgent = header(raw.Request.Headers, "User-Agent")
	entry.Referer = header(raw.Request.Headers, "Referer")
	if raw.Duration != nil {
		entry.duration, entry.timed = *raw.Duration*1000, true
	}
	return entry, true
}

// caddyTime reads either spelling of Caddy's timestamp. The default format is
// a float of unix seconds; an operator who set `time_format rfc3339` in the
// ingress Caddyfile gets a string, and a reader that understood only one of
// them would report an empty page rather than a parse problem.
func caddyTime(raw json.RawMessage) (time.Time, bool) {
	if len(raw) == 0 {
		return time.Time{}, false
	}
	if raw[0] == '"' {
		var text string
		if json.Unmarshal(raw, &text) != nil {
			return time.Time{}, false
		}
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.000Z0700", "2006/01/02 15:04:05"} {
			if t, err := time.Parse(layout, text); err == nil {
				return t.UTC(), true
			}
		}
		return time.Time{}, false
	}
	var seconds float64
	if json.Unmarshal(raw, &seconds) != nil || seconds <= 0 {
		return time.Time{}, false
	}
	whole, frac := int64(seconds), seconds-float64(int64(seconds))
	return time.Unix(whole, int64(frac*float64(time.Second))).UTC(), true
}

func header(headers map[string][]string, name string) string {
	if values := headers[name]; len(values) > 0 {
		return values[0]
	}
	return ""
}

const combinedTimeLayout = "02/Jan/2006:15:04:05 -0700"

// parseCombined reads nginx's stock format:
//
//	1.2.3.4 - - [19/Sep/2026:05:47:57 +0000] "GET /a?b=1 HTTP/1.1" 200 1234 "ref" "agent"
//
// It is hand-scanned rather than matched with a regular expression because a
// request log is the one file on the host that is read a million lines at a
// time, and this is the inner loop of every window the page draws.
func parseCombined(line string) (Entry, bool) {
	entry := Entry{}
	rest := line
	take := func(sep byte) (string, bool) {
		i := strings.IndexByte(rest, sep)
		if i < 0 {
			return "", false
		}
		out := rest[:i]
		rest = rest[i+1:]
		return out, true
	}
	address, ok := take(' ')
	if !ok {
		return Entry{}, false
	}
	entry.RemoteIP = address
	open := strings.IndexByte(rest, '[')
	if open < 0 {
		return Entry{}, false
	}
	rest = rest[open+1:]
	stamp, ok := take(']')
	if !ok {
		return Entry{}, false
	}
	at, err := time.Parse(combinedTimeLayout, stamp)
	if err != nil {
		return Entry{}, false
	}
	entry.at = at.UnixNano()

	request, ok := quoted(&rest)
	if !ok {
		return Entry{}, false
	}
	fields := strings.Fields(request)
	if len(fields) < 2 {
		return Entry{}, false
	}
	entry.Method = fields[0]
	entry.Path, entry.Query = splitURI(fields[1])
	if len(fields) > 2 {
		entry.Proto = fields[2]
	}
	rest = strings.TrimLeft(rest, " ")
	status, ok := take(' ')
	if !ok {
		return Entry{}, false
	}
	if entry.Status, err = strconv.Atoi(status); err != nil {
		return Entry{}, false
	}
	size := rest
	if i := strings.IndexByte(rest, ' '); i >= 0 {
		size, rest = rest[:i], rest[i+1:]
	} else {
		rest = ""
	}
	if size != "-" {
		entry.Size, _ = strconv.ParseInt(size, 10, 64)
	}
	if referer, ok := quoted(&rest); ok && referer != "-" {
		entry.Referer = referer
	}
	if agent, ok := quoted(&rest); ok && agent != "-" {
		entry.UserAgent = agent
	}
	return entry, true
}

// quoted takes the next "..." run, advancing the cursor past it. Escaped
// quotes are honoured because nginx escapes them in a user agent, and a naive
// scan ended a field early on any client that sent one.
func quoted(rest *string) (string, bool) {
	s := *rest
	start := strings.IndexByte(s, '"')
	if start < 0 {
		return "", false
	}
	var out strings.Builder
	for i := start + 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			if i+1 < len(s) {
				i++
				out.WriteByte(s[i])
			}
		case '"':
			*rest = s[i+1:]
			return out.String(), true
		default:
			out.WriteByte(s[i])
		}
	}
	return "", false
}

func splitURI(uri string) (path, query string) {
	if i := strings.IndexByte(uri, '?'); i >= 0 {
		path, query = uri[:i], uri[i+1:]
	} else {
		path = uri
	}
	// A path arrives percent-encoded and is grouped, sorted and displayed;
	// leaving it encoded puts "/users/a%20b" and "/users/a b" in two rows of
	// the top-paths table for one route.
	if decoded, err := url.PathUnescape(path); err == nil {
		path = decoded
	}
	if path == "" {
		path = "/"
	}
	return path, query
}
