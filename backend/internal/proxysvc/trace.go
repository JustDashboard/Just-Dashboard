package proxysvc

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// Trace context at the ingress. A deployment's route sends the application a
// W3C traceparent on every request — `00-<trace>-<span>-01` — and records the
// same ids beside the request in its access log, so the request a log drain
// exports as a span (logdrain's traces stream) is the parent of whatever
// spans the application itself records. A client that sent a valid
// traceparent has its trace continued: the trace id is its, the span is the
// ingress's, and its own span is recorded as the parent. Anything else
// starts a trace.
//
// Neither proxy has a random-hex primitive of the right sizes, so each builds
// the ids from what it has. nginx's $request_id is 32 random hex characters:
// it is a new trace's id, and its second half the span's. Caddy's
// {http.request.uuid} is a version-4 UUID: with its dashes taken out by a
// map's regular expression it is the trace id, and its last sixteen digits the
// span's. A span id that repeats half of its own trace id is still unique
// within the trace, which is all the format asks of it.

// caddyTraceDirectives come first in a route's handling, so a request the
// fence refuses or the maintenance page answers is recorded with its trace
// too. The header is set on the request itself (request_header) rather than
// on the upstream's copy, so a sign-in check through forward_auth carries the
// same trace; Caddy logs the request as it arrived, which is why the ids are
// appended to the log entry as fields of their own. A map is read when its
// output is first asked for, and log_append asks at the end of the request,
// after request_header has replaced the client's traceparent with the
// ingress's — so the client's is copied into a variable (vars, which stores
// the value it reads) before anything is replaced, and the map reads that.
const caddyTraceDirectives = "  map {http.request.uuid} {jd_new_trace} {jd_span} {\n" +
	"    ~^([0-9a-f]{8})-([0-9a-f]{4})-([0-9a-f]{4})-([0-9a-f]{4})-([0-9a-f]{12})$ \"${1}${2}${3}${4}${5}\" \"${4}${5}\"\n" +
	"  }\n" +
	"  vars jd_incoming {http.request.header.traceparent}\n" +
	"  map {http.vars.jd_incoming} {jd_trace} {jd_parent} {\n" +
	"    ~^00-([0-9a-f]{32})-([0-9a-f]{16})-[0-9a-f]{2}$ \"${1}\" \"${2}\"\n" +
	"    default {jd_new_trace} \"\"\n" +
	"  }\n" +
	"  request_header Traceparent \"00-{jd_trace}-{jd_span}-01\"\n" +
	"  log_append trace_id {jd_trace}\n" +
	"  log_append span_id {jd_span}\n" +
	"  log_append parent_span_id {jd_parent}\n"

// nginx's half: two maps at the site's top, a header on every proxied
// location, and three fields after the timed log format's.
func (spec *SiteSpec) traceVar(part string) string { return spec.varIdent() + "_trace_" + part }

func renderNginxTraceMaps(l *lines, spec *SiteSpec) {
	l.add("# W3C trace context for the application: the client's trace continued")
	l.add("# when it sent a valid traceparent, else a new one from $request_id, and")
	l.add("# the ingress's own span from the second half of $request_id.")
	l.add("map $http_traceparent $%s {", spec.traceVar("id"))
	l.add(`    "~^00-(?<jd_tp_id>[0-9a-f]{32})-[0-9a-f]{16}-[0-9a-f]{2}$" $jd_tp_id;`)
	l.add("    default $request_id;")
	l.add("}")
	l.add("map $http_traceparent $%s {", spec.traceVar("parent"))
	l.add(`    "~^00-[0-9a-f]{32}-(?<jd_tp_parent>[0-9a-f]{16})-[0-9a-f]{2}$" $jd_tp_parent;`)
	l.add("    default -;")
	l.add("}")
	l.add("map $request_id $%s {", spec.traceVar("span"))
	l.add(`    "~^[0-9a-f]{16}(?<jd_tp_span>[0-9a-f]{16})$" $jd_tp_span;`)
	l.add("}")
}

// nginxTraceLogFields are what the timed format gains, read back by
// accesslog's readTrailing.
func nginxTraceLogFields(spec *SiteSpec) string {
	return " trace=$" + spec.traceVar("id") + " span=$" + spec.traceVar("span") + " parent=$" + spec.traceVar("parent")
}

func renderNginxTraceHeader(l *lines, spec *SiteSpec) {
	l.add("        proxy_set_header traceparent       \"00-$%s-$%s-01\";", spec.traceVar("id"), spec.traceVar("span"))
}

// traceHeaderLine is how the parser knows a site file carries the trace: the
// header as renderNginxTraceHeader writes it, from the site's own variables. A
// traceparent somebody set by hand on a Proxy site is that site's header.
func traceHeaderLine(value string) bool {
	name, rest, ok := strings.Cut(strings.TrimSpace(value), " ")
	return ok && strings.EqualFold(name, "traceparent") && strings.HasPrefix(strings.TrimSpace(rest), `"00-$jd_`)
}

var caddyVersionRe = regexp.MustCompile(`^v?(\d+)\.(\d+)`)

// recordsTraces says whether the ingress's Caddy has log_append (2.8 and
// later), asked once per container. A version that cannot be read is taken
// as one that cannot: the route is served untraced rather than refused.
func (c *dockerCaddy) recordsTraces(ctx context.Context) bool {
	if supported, ok := caddyTraceSupport.Load(c.ID); ok {
		return supported.(bool)
	}
	out, err := hostexec.Command(ctx, "docker", "exec", c.ID, "caddy", "version").Output()
	supported := err == nil && caddyLogAppend(string(out))
	if err == nil {
		caddyTraceSupport.Store(c.ID, supported)
	}
	return supported
}

var caddyTraceSupport sync.Map

func caddyLogAppend(version string) bool {
	match := caddyVersionRe.FindStringSubmatch(strings.TrimSpace(version))
	if match == nil {
		return false
	}
	major, _ := strconv.Atoi(match[1])
	minor, _ := strconv.Atoi(match[2])
	return major > 2 || (major == 2 && minor >= 8)
}
