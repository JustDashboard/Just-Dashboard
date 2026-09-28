package logsx

import (
	"strings"
	"time"
)

// nginx's error log, which is where a 502 says why:
//
//	2021/07/12 00:00:20 [error] 26211#26211: *698772 upstream timed out (110:
//	Connection timed out) while reading response header from upstream, client:
//	1.2.3.4, server: example.com, request: "GET /a HTTP/1.1", upstream:
//	"http://127.0.0.1:808/a", host: "example.com"
//
// The sentence before ", client:" is what happened; the pairs after it are the
// request it happened to. The events are the handful of sentences nginx has
// for a failing upstream, a refused client and a configuration it would not
// load, matched on the fixed text nginx's source writes rather than on a
// regular expression per line. The same line also arrives without its stamp
// and pid as "nginx: [emerg] …" on the stderr of `nginx -t`, which is where a
// container that will not start says so.

var (
	nginxErrorEvents = []string{
		"upstream_refused", "upstream_timeout", "upstream_closed", "no_live_upstreams", "ssl_error",
		"body_too_large", "rate_limited", "not_found", "forbidden", "config", "startup", "reload",
		"error",
	}
	nginxErrorAttrs = []string{"client", "host", "method", "path", "upstream", "conn", "code", "pid"}
)

func init() {
	register(&Lens{
		ID:     "nginx-error",
		Events: nginxErrorEvents,
		Attrs:  nginxErrorAttrs,
		New:    func() Reader { return ReaderFunc(readNginxError) },
	})
}

const nginxErrorLayout = "2006/01/02 15:04:05"

// nginxErrorStamped tests the fixed positions of "2006/01/02 15:04:05 [",
// which is all it takes to know a line is nginx's and costs nothing on one
// that is not.
func nginxErrorStamped(text string) bool {
	return len(text) > 21 && text[4] == '/' && text[7] == '/' && text[10] == ' ' &&
		text[13] == ':' && text[16] == ':' && text[19] == ' ' && text[20] == '['
}

func nginxErrorShaped(text string) bool {
	return nginxErrorStamped(text) || strings.HasPrefix(text, "nginx: [")
}

func readNginxError(l *Line) {
	text := l.Text
	var (
		rest string
		at   time.Time
	)
	switch {
	case nginxErrorStamped(text):
		// The stamp is the server's local time with no zone, like every
		// naive stamp on the host.
		parsed, err := time.ParseInLocation(nginxErrorLayout, text[:19], time.Local)
		if err != nil {
			return
		}
		at, rest = parsed.UTC(), text[20:]
	case strings.HasPrefix(text, "nginx: ["):
		rest = text[len("nginx: "):]
	default:
		return
	}
	end := strings.IndexByte(rest, ']')
	if end < 2 {
		return
	}
	level := Normalise(rest[1:end])
	if level == "" {
		return
	}
	rest = strings.TrimPrefix(rest[end+1:], " ")

	l.SetLevel(level)
	if !at.IsZero() {
		l.Timestamp = &at
	}
	// "pid#tid: " and then, for a line about a connection, "*cid ".
	if hash := strings.IndexByte(rest, '#'); hash > 0 && nginxErrorDigits(rest[:hash]) {
		if colon := strings.Index(rest, ": "); colon > hash {
			l.SetAttr("pid", rest[:hash])
			rest = rest[colon+2:]
		}
	}
	if strings.HasPrefix(rest, "*") {
		if space := strings.IndexByte(rest, ' '); space > 1 && nginxErrorDigits(rest[1:space]) {
			l.SetAttr("conn", rest[1:space])
			rest = rest[space+1:]
		}
	}
	message := rest
	if i := strings.Index(rest, ", client: "); i >= 0 {
		message = rest[:i]
		nginxErrorContext(l, rest[i+2:])
	}
	l.SetAttr("code", nginxErrorErrno(message))
	l.Event = nginxErrorEvent(message, level)
}

// nginxErrorContext reads the "key: value" pairs nginx appends about the
// request: client, server, request, subrequest, upstream, host, referrer.
// The quoted ones are what the client sent, so a comma inside them is data.
func nginxErrorContext(l *Line, rest string) {
	var server string
	for rest != "" {
		colon := strings.Index(rest, ": ")
		if colon < 0 {
			break
		}
		key := rest[:colon]
		rest = rest[colon+2:]
		var value string
		if strings.HasPrefix(rest, `"`) {
			if end := strings.Index(rest, `", `); end >= 0 {
				value, rest = rest[1:end], rest[end+3:]
			} else {
				value, rest = strings.TrimSuffix(rest[1:], `"`), ""
			}
		} else if end := strings.Index(rest, ", "); end >= 0 {
			value, rest = rest[:end], rest[end+2:]
		} else {
			value, rest = rest, ""
		}
		switch key {
		case "client":
			l.SetAttr("client", value)
		case "server":
			server = value
		case "host":
			l.SetAttr("host", value)
		case "request":
			method, target, _ := strings.Cut(value, " ")
			target, _, _ = strings.Cut(target, " ")
			if httpAccessMethodToken(method) && target != "" {
				l.SetAttr("method", method)
				l.SetAttr("path", httpAccessPath(target))
			}
		case "upstream":
			l.SetAttr("upstream", nginxUpstreamAddress(value))
		}
	}
	// A line about a connection that never got as far as a Host header — a
	// TLS handshake — names only the server block, which is the nearest
	// thing to a host it has.
	if _, ok := l.Attrs["host"]; !ok {
		l.SetAttr("host", server)
	}
}

// nginxUpstreamAddress reduces the URL nginx was proxying to to the address
// it dialled: "http://127.0.0.1:8080/api/x" is 127.0.0.1:8080, a socket is its
// path, a named upstream block its name. Grouped by URL, one failing backend
// would be a row per path it was asked for.
func nginxUpstreamAddress(upstream string) string {
	if i := strings.Index(upstream, "://"); i >= 0 {
		upstream = upstream[i+3:]
	}
	if strings.HasPrefix(upstream, "unix:") {
		if i := strings.IndexByte(upstream[len("unix:"):], ':'); i >= 0 {
			return upstream[:len("unix:")+i]
		}
		return upstream
	}
	upstream, _, _ = strings.Cut(upstream, "/")
	return upstream
}

// nginxErrorErrno finds the "(111: Connection refused)" nginx appends to a
// failed system call. The parentheses of "connect()" come first and are not it.
func nginxErrorErrno(message string) string {
	for rest := message; ; {
		open := strings.IndexByte(rest, '(')
		if open < 0 {
			return ""
		}
		rest = rest[open+1:]
		if colon := strings.IndexByte(rest, ':'); colon > 0 && nginxErrorDigits(rest[:colon]) {
			return rest[:colon]
		}
	}
}

func nginxErrorEvent(message, level string) string {
	switch {
	case nginxErrorConfig(message):
		return "config"
	case strings.Contains(message, "upstream timed out"):
		return "upstream_timeout"
	case strings.HasPrefix(message, "no live upstreams"):
		return "no_live_upstreams"
	case strings.HasPrefix(message, "connect() ") && strings.Contains(message, "while connecting to upstream"):
		return "upstream_refused"
	case strings.Contains(message, "upstream prematurely closed"),
		strings.Contains(message, "upstream") &&
			(strings.Contains(message, "(104: Connection reset by peer)") || strings.Contains(message, "(32: Broken pipe)")):
		return "upstream_closed"
	case strings.Contains(message, "SSL"):
		return "ssl_error"
	case strings.HasPrefix(message, "client intended to send too large body"):
		return "body_too_large"
	case strings.HasPrefix(message, "limiting requests"), strings.HasPrefix(message, "limiting connections"),
		strings.HasPrefix(message, "delaying request"):
		return "rate_limited"
	case strings.HasPrefix(message, "open() ") && strings.Contains(message, "(2: No such file or directory)"),
		strings.Contains(message, `" is not found (2: No such file or directory)`):
		return "not_found"
	case strings.HasPrefix(message, "access forbidden by rule"),
		strings.HasPrefix(message, "directory index of ") && strings.HasSuffix(message, " is forbidden"),
		strings.HasPrefix(message, "open() ") && strings.Contains(message, "(13: Permission denied)"):
		return "forbidden"
	case strings.HasPrefix(message, "nginx/"), strings.HasPrefix(message, "openresty/"):
		// The version line is written once, by the master at start-up and
		// not on a reload, which makes it the one reliable start marker.
		return "startup"
	case message == "reconfiguring":
		// A reload also writes "signal process started" from the process
		// that sent it and the SIGHUP line before this one; only this one
		// is written once per reload however it was asked for.
		return "reload"
	case level == "error" || level == "critical":
		return "error"
	}
	return ""
}

// nginxErrorConfig recognises a configuration nginx refused. A directive it
// would not take is reported with the file and line — "… in
// /etc/nginx/sites-enabled/app:12" — and the rest are the few start-up
// failures that are the configuration's to fix: a listen port someone else
// holds, a certificate that will not load, two blocks claiming one name.
func nginxErrorConfig(message string) bool {
	if in := strings.LastIndex(message, " in /"); in >= 0 {
		if colon := strings.LastIndexByte(message, ':'); colon > in && nginxErrorDigits(message[colon+1:]) {
			return true
		}
	}
	return strings.HasPrefix(message, "bind() to ") || strings.HasPrefix(message, "still could not bind") ||
		strings.HasPrefix(message, "cannot load certificate") ||
		strings.HasPrefix(message, "conflicting server name")
}

func nginxErrorDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
