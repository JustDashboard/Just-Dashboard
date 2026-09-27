package logsx

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
)

// Caddy's runtime log: one JSON object per line, named by its "logger". This
// is what the deployment ingress and the dashboard's own proxy write to
// stderr, and it is the one place a 502 says why — the access entry records
// that a request failed, http.log.error records "dial tcp 10.0.4.3:3000:
// connect: connection refused" for the same request. The lens reads:
//
//   - http.log.access… as a request, through the access lens, so a request
//     reads the same here as in a file of them;
//   - http.log.error and the reverse proxy's own lines for the upstream and
//     what went wrong with it;
//   - the tls loggers for a certificate obtained, renewed, or given up on;
//   - start-up and every configuration load, including the one that failed.
//
// Everything else — the admin API's request log, ACME chatter, listener
// notices — keeps its structured fields and no event. The level is Caddy's
// own; the access log already writes error for a status of 500 or more, and
// the lens holds to that rule for an access line whatever level it carried.

var caddyEvents = []string{
	"request", "client_error", "server_error", "probe", "upstream_refused", "upstream_timeout",
	"error", "cert_obtained", "cert_renewed", "cert_failed", "startup", "config",
}

func init() {
	register(&Lens{
		ID:     "caddy",
		Events: caddyEvents,
		Attrs:  append(append([]string(nil), httpAccessAttrs...), "logger", "domain", "error", "upstream"),
		New:    func() Reader { return &caddyReader{} },
	})
}

type caddyReader struct {
	access httpAccessReader
}

func (r *caddyReader) Read(l *Line) {
	if !strings.HasPrefix(l.Text, "{") {
		// The one thing Caddy writes that is not JSON is the command's last
		// word before it exits — "Error: adapting config using caddyfile: …"
		// — which is how a container with a broken Caddyfile says so on
		// every restart of its loop.
		if strings.HasPrefix(l.Text, "Error: ") && strings.Contains(l.Text, "config") {
			l.Event = "config"
			l.SetLevel("error")
		}
		return
	}
	logger := l.Fields["logger"]
	if strings.HasPrefix(logger, "http.log.access") {
		// A request the client abandoned before any response is logged with
		// status 0, which the access parser refuses; it stays a plain line.
		if e, ok := accesslog.Parse(l.Text); ok {
			r.access.record(l, e)
			l.SetAttr("logger", logger)
			if e.Status >= 500 {
				l.SetLevel("error")
			}
			return
		}
	}
	switch {
	case strings.HasPrefix(logger, "http.log.error"):
		caddyUpstream(l, logger, true)
	case strings.HasPrefix(logger, "http.handlers.reverse_proxy"):
		caddyUpstream(l, logger, false)
	case strings.HasPrefix(logger, "tls"):
		caddyCertificate(l, logger)
	case logger == "admin.api":
		// "load complete" is a configuration applied — the dashboard's own
		// route changes arrive this way — and a load the admin API refused
		// says so as a request error whose reason starts with the load.
		cause := l.Fields["error"]
		if l.Message == "load complete" || l.Message == "request error" &&
			(strings.HasPrefix(cause, "loading config") || strings.HasPrefix(cause, "adapting config")) {
			l.Event = "config"
			l.SetAttr("logger", logger)
			l.SetAttr("error", cause)
		}
	case logger == "":
		switch {
		case l.Message == "serving initial configuration":
			l.Event = "startup"
		case l.Level == "warn" && l.Fields["adapter"] != "":
			// A warning the Caddyfile adapter raised ("input is not
			// formatted") is about the configuration, whatever it says.
			l.Event = "config"
		}
	}
}

// caddyFailure is what an http.log.error or reverse-proxy line says about the
// request it failed. The request is the same object the access entry
// carries, so a failure and its access line share a host and a path; the
// headers inside it are skipped rather than decoded.
type caddyFailure struct {
	Request struct {
		RemoteIP string `json:"remote_ip"`
		ClientIP string `json:"client_ip"`
		Proto    string `json:"proto"`
		Method   string `json:"method"`
		Host     string `json:"host"`
		URI      string `json:"uri"`
	} `json:"request"`
	Duration *float64 `json:"duration"`
	Status   int      `json:"status"`
	Upstream string   `json:"upstream"`
	Error    string   `json:"error"`
}

// caddyUpstream reads a request that failed at the proxy. http.log.error's
// message is the Go error itself; the reverse proxy's own lines put it in
// "error". A refused dial and a timeout are named; any other failure of a
// request Caddy answered with a 5xx is an error. Below 500 — a handler's 404,
// a client that left (499) — Caddy logs it at debug and it names nothing.
func caddyUpstream(l *Line, logger string, answered bool) {
	var f caddyFailure
	if json.Unmarshal([]byte(l.Text), &f) != nil {
		return
	}
	cause := f.Error
	if answered {
		cause = l.Message
	}
	switch {
	case strings.Contains(cause, "connection refused"):
		l.Event = "upstream_refused"
	case strings.Contains(cause, "i/o timeout"), strings.Contains(cause, "timeout awaiting response headers"),
		strings.Contains(cause, "context deadline exceeded"):
		l.Event = "upstream_timeout"
	case answered && f.Status >= 500:
		l.Event = "error"
	}
	if l.Attrs == nil {
		l.Attrs = make(map[string]string, 12)
	}
	l.SetAttr("logger", logger)
	l.SetAttr("error", cause)
	upstream := f.Upstream
	if upstream == "" {
		upstream = caddyDialed(cause)
	}
	l.SetAttr("upstream", upstream)
	if f.Request.Method != "" {
		l.SetAttr("method", f.Request.Method)
		l.SetAttr("path", httpAccessPath(f.Request.URI))
		l.SetAttr("proto", f.Request.Proto)
		l.SetAttr("host", f.Request.Host)
		client := f.Request.ClientIP
		if client == "" {
			client = f.Request.RemoteIP
		}
		l.SetAttr("client", client)
	}
	if f.Status > 0 {
		l.SetAttr("status", strconv.Itoa(f.Status))
		if class := (accesslog.Entry{Status: f.Status}).Class(); class != "other" {
			l.SetAttr("class", class)
		}
	}
	if f.Duration != nil {
		l.SetAttrNumber("duration_ms", httpAccessMs(*f.Duration*1000))
	}
}

// caddyDialed finds the address in a Go dial error: "dial tcp 10.0.4.3:3000:
// connect: connection refused" names the backend, and "dial tcp: lookup app
// on 127.0.0.11:53: no such host" names the one that does not resolve.
func caddyDialed(cause string) string {
	if i := strings.Index(cause, "dial tcp "); i >= 0 {
		address, _, found := strings.Cut(cause[i+len("dial tcp "):], ": ")
		if found {
			return address
		}
	}
	if i := strings.Index(cause, "lookup "); i >= 0 {
		name, _, _ := strings.Cut(cause[i+len("lookup "):], " ")
		return name
	}
	return ""
}

// caddyCertificate names the lines that end an attempt to get a certificate.
// An attempt that fails logs "could not get certificate from issuer" once per
// issuer and then one "will retry" (or, at the end, "final attempt; giving
// up"), so only the last counts: a failures reading of the first would count
// each attempt twice, or three times with a fallback issuer.
func caddyCertificate(l *Line, logger string) {
	switch l.Message {
	case "certificate obtained successfully":
		l.Event = "cert_obtained"
	case "certificate renewed successfully":
		l.Event = "cert_renewed"
	case "will retry", "final attempt; giving up", "job failed":
		l.Event = "cert_failed"
	default:
		return
	}
	cause := l.Fields["error"]
	domain := l.Fields["identifier"]
	if domain == "" {
		// A retry names its domain only inside the error: "[example.com]
		// Obtain: …".
		if strings.HasPrefix(cause, "[") {
			domain, _, _ = strings.Cut(cause[1:], "]")
		}
	}
	l.SetAttr("logger", logger)
	l.SetAttr("domain", domain)
	l.SetAttr("error", cause)
}
