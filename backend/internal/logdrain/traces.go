package logdrain

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The traces stream: every request a deployment's ingress records becomes
// one OpenTelemetry span, sent with OTLP/HTTP's JSON encoding to the drain's
// /v1/traces — a SERVER span named for the request's method and route, from
// when the ingress began to answer to when it finished, in a resource that
// names the deployment as its service. The span's ids are the ones the
// ingress sent the application in its traceparent (proxysvc/trace.go), so the
// application's own spans, exported wherever it exports them, are its
// children in the same trace.

// spanKindServer and the two status codes are OTLP's enum values, which its
// JSON encoding writes as numbers.
const (
	spanKindServer  = 2
	statusCodeUnset = 0
	statusCodeError = 2
)

type otlpSpanStatus struct {
	Code    int    `json:"code"`
	Message string `json:"message,omitempty"`
}

type otlpSpan struct {
	TraceID           string          `json:"traceId"`
	SpanID            string          `json:"spanId"`
	ParentSpanID      string          `json:"parentSpanId,omitempty"`
	Name              string          `json:"name"`
	Kind              int             `json:"kind"`
	StartTimeUnixNano string          `json:"startTimeUnixNano"`
	EndTimeUnixNano   string          `json:"endTimeUnixNano"`
	Attributes        []otlpAttribute `json:"attributes"`
	Status            otlpSpanStatus  `json:"status"`
}

// tracesEndpoint is the drain's address with /v1/traces, as an exporter
// finishes OTEL_EXPORTER_OTLP_ENDPOINT: after a base or a prefix, or in place
// of /v1/logs when the address names the logs path itself.
func tracesEndpoint(endpoint string) string {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return endpoint
	}
	path := strings.TrimRight(parsed.Path, "/")
	switch {
	case strings.HasSuffix(path, "/v1/traces"):
		return endpoint
	case strings.HasSuffix(path, "/v1/logs"):
		path = strings.TrimSuffix(path, "/v1/logs")
	}
	parsed.Path = path + "/v1/traces"
	return parsed.String()
}

// encodeTraces renders a batch of requests as spans: one resource per
// project, environment and release.
func encodeTraces(records []Record, hostName string) (Encoded, error) {
	type resourceKey struct {
		project     string
		environment string
		projectID   int64
		release     int64
	}
	order := []resourceKey{}
	grouped := map[resourceKey][]otlpSpan{}
	for _, record := range records {
		if record.Request == nil {
			continue
		}
		key := resourceKey{record.Project, record.Environment, record.ProjectID, record.Release}
		if _, ok := grouped[key]; !ok {
			order = append(order, key)
		}
		grouped[key] = append(grouped[key], spanOf(record))
	}
	type scopeSpans struct {
		Scope map[string]string `json:"scope"`
		Spans []otlpSpan        `json:"spans"`
	}
	type resourceSpans struct {
		Resource   map[string][]otlpAttribute `json:"resource"`
		ScopeSpans []scopeSpans               `json:"scopeSpans"`
	}
	payload := struct {
		ResourceSpans []resourceSpans `json:"resourceSpans"`
	}{ResourceSpans: []resourceSpans{}}
	for _, key := range order {
		resource := []otlpAttribute{
			stringAttribute("service.name", key.project),
			stringAttribute("service.namespace", "just-dashboard"),
			stringAttribute("deployment.environment.name", key.environment),
			intAttribute("just_dashboard.project_id", key.projectID),
		}
		if key.release > 0 {
			// The release a span was served by is the service's version.
			resource = append(resource, stringAttribute("service.version", strconv.FormatInt(key.release, 10)))
		}
		if hostName != "" {
			resource = append(resource, stringAttribute("host.name", hostName))
		}
		payload.ResourceSpans = append(payload.ResourceSpans, resourceSpans{
			Resource:   map[string][]otlpAttribute{"attributes": resource},
			ScopeSpans: []scopeSpans{{Scope: map[string]string{"name": "just-dashboard"}, Spans: grouped[key]}},
		})
	}
	body, err := json.Marshal(payload)
	return Encoded{Body: body, ContentType: "application/json"}, err
}

// spanOf is one request as a span. A request the ingress did not time is a
// span of no length at the instant it was recorded; one with no trace context
// — a route rendered before the ingress sent one — is given ids here, so it is
// still a span, of a trace nothing else joins.
func spanOf(record Record) otlpSpan {
	request := record.Request
	end := record.At
	start := end
	if request.Timed && request.DurationMs > 0 {
		start = end.Add(-time.Duration(request.DurationMs * float64(time.Millisecond)))
	}
	route := RouteTemplate(request.Path)
	attributes := []otlpAttribute{
		stringAttribute("http.request.method", request.Method),
		stringAttribute("http.route", route),
		stringAttribute("url.path", request.Path),
		intAttribute("http.response.status_code", int64(request.Status)),
		intAttribute("http.response.body.size", request.Size),
		stringAttribute("deployment.environment.name", record.Environment),
	}
	scheme := "http"
	if request.TLS {
		scheme = "https"
	}
	attributes = append(attributes, stringAttribute("url.scheme", scheme))
	if request.Query != "" {
		attributes = append(attributes, stringAttribute("url.query", request.Query))
	}
	if request.Host != "" {
		attributes = append(attributes, stringAttribute("server.address", request.Host))
	}
	if name, version, ok := strings.Cut(request.Proto, "/"); ok {
		attributes = append(attributes, stringAttribute("network.protocol.name", strings.ToLower(name)),
			stringAttribute("network.protocol.version", version))
	}
	if request.RemoteIP != "" {
		attributes = append(attributes, stringAttribute("client.address", request.RemoteIP))
	}
	if request.UserAgent != "" {
		attributes = append(attributes, stringAttribute("user_agent.original", request.UserAgent))
	}
	if request.ClientClass != "" {
		attributes = append(attributes, stringAttribute("just_dashboard.client.class", request.ClientClass))
	}
	if record.Release > 0 {
		attributes = append(attributes, intAttribute("just_dashboard.release", record.Release))
	}
	if request.Timed {
		attributes = append(attributes, doubleAttribute("http.server.request.duration", request.DurationMs/1000))
	}
	// A server span is an error for a 5xx alone: a 4xx is the client's.
	status := otlpSpanStatus{Code: statusCodeUnset}
	if request.Status >= 500 {
		status = otlpSpanStatus{Code: statusCodeError, Message: strconv.Itoa(request.Status)}
	}
	trace, span := request.TraceID, request.SpanID
	if trace == "" || span == "" {
		trace, span = NewTraceID(), NewSpanID()
	}
	return otlpSpan{
		TraceID: trace, SpanID: span, ParentSpanID: request.ParentSpanID,
		Name: request.Method + " " + route, Kind: spanKindServer,
		StartTimeUnixNano: strconv.FormatInt(start.UnixNano(), 10), EndTimeUnixNano: strconv.FormatInt(end.UnixNano(), 10),
		Attributes: attributes, Status: status,
	}
}

var (
	numericSegment = regexp.MustCompile(`^\d+$`)
	uuidSegment    = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	hexSegment     = regexp.MustCompile(`^[0-9a-fA-F]{16,}$`)
	tokenSegment   = regexp.MustCompile(`^[A-Za-z0-9_-]{20,}$`)
	digitRun       = regexp.MustCompile(`\d`)
)

// RouteTemplate is a path with what identifies one record taken out — a
// number, a UUID, a long hex or token id each read {id} — because a span's
// name is what a trace service groups by, and /orders/1041 and /orders/1042
// are one route. The ingress does not know the application's routes; this is
// the shape of one, and the real path is the span's url.path.
func RouteTemplate(path string) string {
	if path == "" || path == "/" {
		return "/"
	}
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		switch {
		case segment == "":
		case numericSegment.MatchString(segment), uuidSegment.MatchString(segment), hexSegment.MatchString(segment):
			segments[i] = "{id}"
		case tokenSegment.MatchString(segment) && digitRun.MatchString(segment):
			segments[i] = "{id}"
		}
	}
	route := strings.Join(segments, "/")
	if len(route) > 200 {
		route = route[:200]
	}
	return route
}

// NewTraceID and NewSpanID are random ids in W3C trace context's spelling,
// for a request whose ingress sent none.
func NewTraceID() string { return randomHex(16) }

func NewSpanID() string { return randomHex(8) }

func randomHex(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
