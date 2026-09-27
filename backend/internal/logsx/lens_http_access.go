package logsx

import (
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
)

// The request log a web server writes: nginx's combined (and the Docker
// image's main, which only appends a field), Apache's common, Caddy's JSON
// access entry. It is read by the parser the deployment Requests page uses,
// so a request counted there and a request filtered here are the same request
// read the same way — two parsers of one format drift, and the day they do, a
// 5xx is on one page and not the other.
//
// The event is the status family, with probes taken out first: on a public
// host most refused requests are scanners trying doors, and a 404 view that
// is ninety per cent /.env is not a view of broken links.

var (
	httpAccessEvents = []string{"request", "client_error", "server_error", "probe"}
	httpAccessAttrs  = []string{
		"method", "path", "status", "class", "bytes", "client", "agent", "bot", "referer", "host",
		"proto", "duration_ms",
	}
)

func init() {
	register(&Lens{
		ID:     "http-access",
		Events: httpAccessEvents,
		Attrs:  httpAccessAttrs,
		New:    func() Reader { return &httpAccessReader{} },
	})
}

// httpAccessReader carries one thing from line to line: what each user agent
// it has met reduces to. A log has a few hundred agents over a million lines,
// and naming one — its family, whether it is a program — is some forty
// substring scans of it, which profiled as a quarter of the lens's time.
type httpAccessReader struct {
	agents map[string]httpAccessAgent
}

type httpAccessAgent struct {
	family string
	bot    bool
}

// httpAccessAgentsKept bounds the memo. A scanner that sends a new agent with
// every request would otherwise grow it for as long as the search runs; past
// the bound it starts again, which costs a few scans and nothing else.
const httpAccessAgentsKept = 512

func (r *httpAccessReader) agent(ua string) httpAccessAgent {
	if known, ok := r.agents[ua]; ok {
		return known
	}
	if r.agents == nil || len(r.agents) >= httpAccessAgentsKept {
		r.agents = make(map[string]httpAccessAgent, 64)
	}
	// The key outlives the line, so it is a copy rather than a slice of it.
	ua = strings.Clone(ua)
	named := httpAccessAgent{family: accesslog.AgentFamily(ua), bot: accesslog.IsBot(ua)}
	r.agents[ua] = named
	return named
}

func (r *httpAccessReader) Read(l *Line) {
	if !httpAccessShaped(l) {
		return
	}
	e, ok := accesslog.Parse(l.Text)
	if !ok {
		if !strings.HasPrefix(l.Text, "{") {
			httpAccessWithoutRequest(l)
		}
		return
	}
	r.record(l, e)
	l.SetLevel(httpAccessLevel(e.Status))
}

// httpAccessShaped is the cheap test in front of the parse. Only a Caddy
// access entry is worth decoding as JSON — the runtime log shares the file on
// a container's stdout — and a combined line always has its bracketed time
// followed by the quoted request.
func httpAccessShaped(l *Line) bool {
	if strings.HasPrefix(l.Text, "{") {
		return strings.HasPrefix(l.Fields["logger"], "http.log.access")
	}
	return strings.Contains(l.Text, `] "`)
}

// record writes what one request says onto its line. The Caddy lens and the
// nginx composite read requests through it too, so a request reads the same
// through all three.
func (r *httpAccessReader) record(l *Line, e accesslog.Entry) {
	l.Event = httpAccessEvent(e)
	if l.Attrs == nil {
		// Sized for the dozen values a request carries, so the map is
		// allocated once rather than grown on every line of the file.
		l.Attrs = make(map[string]string, 12)
	}
	// A method that is not a token is the escaped first bytes of a TLS
	// handshake or an RDP cookie sent to a plain port. As a facet value it
	// would be a new row for every scanner, and the raw line still shows it.
	if httpAccessMethodToken(e.Method) {
		l.SetAttr("method", e.Method)
		l.SetAttr("path", e.Path)
		l.SetAttr("proto", e.Proto)
	}
	l.SetAttr("client", e.RemoteIP)
	l.SetAttr("host", e.Host)
	l.SetAttr("referer", e.Referer)
	// Numbers formatted here and constants are nobody's slice of the line, so
	// they go in as they are rather than through SetAttr's copy.
	l.Attrs["status"] = strconv.Itoa(e.Status)
	if class := e.Class(); class != "other" {
		l.Attrs["class"] = class
	}
	l.Attrs["bytes"] = strconv.FormatInt(e.Size, 10)
	if e.UserAgent != "" {
		// The family, not the agent: grouped by the full string, every
		// Chrome point release would be its own client. The line keeps the
		// full one.
		named := r.agent(e.UserAgent)
		l.SetAttr("agent", named.family)
		if named.bot {
			l.Attrs["bot"] = "1"
		}
	}
	if ms, timed := e.Duration(); timed {
		l.SetAttrNumber("duration_ms", httpAccessMs(ms))
	}
	at := e.At()
	l.Timestamp = &at
}

// httpAccessMs rounds a duration to the microsecond, the resolution anyone
// reads a request at; the float product of Caddy's seconds carries noise
// past it that would otherwise be printed.
func httpAccessMs(ms float64) float64 {
	return math.Round(ms*1000) / 1000
}

// httpAccessPath spells a request target the way accesslog does a path:
// without its query and decoded, so an error line about a request and the
// access line of the same request land in the same "by path" row.
func httpAccessPath(target string) string {
	target, _, _ = strings.Cut(target, "?")
	if decoded, err := url.PathUnescape(target); err == nil {
		return decoded
	}
	return target
}

func httpAccessEvent(e accesslog.Entry) string {
	switch {
	case accesslog.IsProbe(e) || e.Status >= 400 && httpAccessNotHTTP(e.Method):
		return "probe"
	case e.Status >= 500:
		return "server_error"
	case e.Status >= 400:
		return "client_error"
	}
	return "request"
}

// httpAccessNotHTTP names the request lines that are not a visitor's request
// at all: bytes of another protocol sent to an HTTP port, an HTTP/2 preface
// sent to an HTTP/1 one, and a CONNECT looking for an open proxy.
func httpAccessNotHTTP(method string) bool {
	return !httpAccessMethodToken(method) || method == "PRI" || method == "CONNECT"
}

func httpAccessMethodToken(method string) bool {
	if method == "" || len(method) > 16 {
		return false
	}
	for i := 0; i < len(method); i++ {
		c := method[i]
		if (c < 'A' || c > 'Z') && c != '-' && c != '_' {
			return false
		}
	}
	return true
}

// httpAccessLevel is the status rule: the format has no level of its own,
// and "is anything failing" is the question a request log is opened with.
func httpAccessLevel(status int) string {
	switch {
	case status >= 500:
		return "error"
	case status >= 400:
		return "warn"
	}
	return "info"
}

const httpAccessTimeLayout = "02/Jan/2006:15:04:05 -0700"

// httpAccessWithoutRequest reads the one combined line accesslog.Parse turns
// away on purpose: a connection that sent no request at all, which nginx
// records as `""` (Apache as `"-"`) with a 400. The Requests page is right to
// skip it — it is not a request — but on a public host it is a steady share of
// the log, port scanners and clients that gave up, and a probe the lens cannot
// name is noise the page cannot fold away.
func httpAccessWithoutRequest(l *Line) {
	text := l.Text
	open := strings.Index(text, " [")
	closing := strings.Index(text, `] "`)
	if open <= 0 || closing < open {
		return
	}
	rest := text[closing+3:]
	switch {
	case strings.HasPrefix(rest, `" `):
		rest = rest[2:]
	case strings.HasPrefix(rest, `-" `):
		rest = rest[3:]
	default:
		return
	}
	statusText, rest, _ := strings.Cut(rest, " ")
	status, err := strconv.Atoi(statusText)
	if err != nil || status < 400 {
		return
	}
	at, err := time.Parse(httpAccessTimeLayout, text[open+2:closing])
	if err != nil {
		return
	}
	at = at.UTC()
	l.Event = "probe"
	client, _, _ := strings.Cut(text, " ")
	l.SetAttr("client", client)
	l.SetAttr("status", statusText)
	l.SetAttr("class", accesslog.Entry{Status: status}.Class())
	if size, _, _ := strings.Cut(rest, " "); size != "-" {
		l.SetAttr("bytes", size)
	}
	l.Timestamp = &at
	l.SetLevel(httpAccessLevel(status))
}
