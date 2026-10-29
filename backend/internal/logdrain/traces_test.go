package logdrain

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
)

const (
	testTrace  = "4bf92f3577b34da6a3ce929d0e0e4736"
	testSpan   = "a3ce929d0e0e4736"
	testParent = "00f067aa0ba902b7"
)

// span is the part of an OTLP/JSON span a test reads.
type span struct {
	TraceID           string `json:"traceId"`
	SpanID            string `json:"spanId"`
	ParentSpanID      string `json:"parentSpanId"`
	Name              string `json:"name"`
	Kind              int    `json:"kind"`
	StartTimeUnixNano string `json:"startTimeUnixNano"`
	EndTimeUnixNano   string `json:"endTimeUnixNano"`
	Attributes        []struct {
		Key   string `json:"key"`
		Value struct {
			StringValue *string  `json:"stringValue"`
			IntValue    *string  `json:"intValue"`
			DoubleValue *float64 `json:"doubleValue"`
		} `json:"value"`
	} `json:"attributes"`
	Status struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"status"`
}

func (s span) attribute(key string) string {
	for _, attribute := range s.Attributes {
		if attribute.Key != key {
			continue
		}
		switch {
		case attribute.Value.StringValue != nil:
			return *attribute.Value.StringValue
		case attribute.Value.IntValue != nil:
			return *attribute.Value.IntValue
		case attribute.Value.DoubleValue != nil:
			raw, _ := json.Marshal(*attribute.Value.DoubleValue)
			return string(raw)
		}
	}
	return ""
}

type tracesPayload struct {
	ResourceSpans []struct {
		Resource struct {
			Attributes []struct {
				Key   string `json:"key"`
				Value struct {
					StringValue *string `json:"stringValue"`
					IntValue    *string `json:"intValue"`
				} `json:"value"`
			} `json:"attributes"`
		} `json:"resource"`
		ScopeSpans []struct {
			Spans []span `json:"spans"`
		} `json:"scopeSpans"`
	} `json:"resourceSpans"`
}

func (p tracesPayload) resource(index int) map[string]string {
	out := map[string]string{}
	for _, attribute := range p.ResourceSpans[index].Resource.Attributes {
		switch {
		case attribute.Value.StringValue != nil:
			out[attribute.Key] = *attribute.Value.StringValue
		case attribute.Value.IntValue != nil:
			out[attribute.Key] = *attribute.Value.IntValue
		}
	}
	return out
}

func TestARequestIsASpanOfTheTraceTheIngressSent(t *testing.T) {
	end := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	records := []Record{
		{At: end, ProjectID: 7, Project: "shop", Environment: "production", Release: 12, Stream: StreamTraces,
			Request: &Request{Method: "GET", Path: "/orders/1041", Query: "tab=items", Host: "shop.example.test", Proto: "HTTP/2.0",
				Status: 502, Size: 31, DurationMs: 12.5, Timed: true, TLS: true, RemoteIP: "203.0.113.9", UserAgent: "Mozilla/5.0",
				TraceID: testTrace, SpanID: testSpan, ParentSpanID: testParent, ClientClass: "browser"}},
		{At: end, ProjectID: 7, Project: "shop", Environment: "production", Release: 12, Stream: StreamTraces,
			Request: &Request{Method: "POST", Path: "/", Status: 404, TraceID: testTrace, SpanID: "b3ce929d0e0e4736"}},
		{At: end, ProjectID: 7, Project: "shop", Environment: "production", Release: 13, Stream: StreamTraces,
			Request: &Request{Method: "GET", Path: "/health", Status: 200}},
	}
	encoded, err := encodeTraces(records, "web-01")
	if err != nil {
		t.Fatal(err)
	}
	var payload tracesPayload
	if err := json.Unmarshal(encoded.Body, &payload); err != nil || encoded.ContentType != "application/json" {
		t.Fatalf("payload = %s, %v", encoded.Body, err)
	}
	// One resource per release: the release is the service's version.
	if len(payload.ResourceSpans) != 2 {
		t.Fatalf("resources = %d", len(payload.ResourceSpans))
	}
	resource := payload.resource(0)
	for key, want := range map[string]string{
		"service.name": "shop", "service.namespace": "just-dashboard", "deployment.environment.name": "production",
		"service.version": "12", "host.name": "web-01", "just_dashboard.project_id": "7",
	} {
		if resource[key] != want {
			t.Fatalf("resource %s = %q, want %q", key, resource[key], want)
		}
	}
	spans := payload.ResourceSpans[0].ScopeSpans[0].Spans
	first := spans[0]
	if first.TraceID != testTrace || first.SpanID != testSpan || first.ParentSpanID != testParent || first.Kind != 2 ||
		first.Name != "GET /orders/{id}" {
		t.Fatalf("span = %+v", first)
	}
	if first.EndTimeUnixNano != "1793264400000000000" || first.StartTimeUnixNano != "1793264399987500000" {
		t.Fatalf("times = %s .. %s", first.StartTimeUnixNano, first.EndTimeUnixNano)
	}
	for key, want := range map[string]string{
		"http.request.method": "GET", "http.route": "/orders/{id}", "url.path": "/orders/1041", "url.query": "tab=items",
		"url.scheme": "https", "http.response.status_code": "502", "server.address": "shop.example.test",
		"network.protocol.name": "http", "network.protocol.version": "2.0", "client.address": "203.0.113.9",
		"just_dashboard.client.class": "browser", "just_dashboard.release": "12", "deployment.environment.name": "production",
		"http.server.request.duration": "0.0125",
	} {
		if got := first.attribute(key); got != want {
			t.Fatalf("attribute %s = %q, want %q", key, got, want)
		}
	}
	// A 5xx is the server's error; a 4xx is the client's and leaves the
	// status unset.
	if first.Status.Code != 2 || first.Status.Message != "502" || spans[1].Status.Code != 0 {
		t.Fatalf("statuses = %+v, %+v", first.Status, spans[1].Status)
	}
	// An untimed request is a span of no length; one with no context still
	// gets valid ids.
	if spans[1].StartTimeUnixNano != spans[1].EndTimeUnixNano || spans[1].Name != "POST /" || spans[1].ParentSpanID != "" {
		t.Fatalf("an untimed span = %+v", spans[1])
	}
	untraced := payload.ResourceSpans[1].ScopeSpans[0].Spans[0]
	if len(untraced.TraceID) != 32 || len(untraced.SpanID) != 16 || untraced.TraceID == strings.Repeat("0", 32) {
		t.Fatalf("a request without context = %+v", untraced)
	}
}

func TestARouteIsThePathWithItsIdsTakenOut(t *testing.T) {
	for path, want := range map[string]string{
		"":                     "/",
		"/":                    "/",
		"/orders/1041":         "/orders/{id}",
		"/orders/1041/items/7": "/orders/{id}/items/{id}",
		"/users/3fa85f64-5717-4562-b3fc-2c963f66afa6/avatar": "/users/{id}/avatar",
		"/blobs/4bf92f3577b34da6a3ce929d0e0e4736":            "/blobs/{id}",
		"/invite/Xk2pQ9vLm8RtYw4ZcB7nJh":                     "/invite/{id}",
		"/docs/getting-started-with-the-dashboard":           "/docs/getting-started-with-the-dashboard",
		"/api/v2/items": "/api/v2/items",
	} {
		if got := RouteTemplate(path); got != want {
			t.Errorf("%q = %q, want %q", path, got, want)
		}
	}
}

func TestSpansGoToTracesBesideTheLogsPath(t *testing.T) {
	for endpoint, want := range map[string]string{
		"https://otlp.example.test":                    "https://otlp.example.test/v1/traces",
		"https://otlp-gateway.example.test/otlp/":      "https://otlp-gateway.example.test/otlp/v1/traces",
		"http://127.0.0.1:4318/v1/logs":                "http://127.0.0.1:4318/v1/traces",
		"https://collector.example.test/v1/traces":     "https://collector.example.test/v1/traces",
		"https://collector.example.test/x/v1/logs?a=1": "https://collector.example.test/x/v1/traces?a=1",
	} {
		if got := tracesEndpoint(endpoint); got != want {
			t.Errorf("%s = %s, want %s", endpoint, got, want)
		}
	}
}

func TestTracesAreAnOTLPDrainsAndItsLinkIsAnAddress(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	if _, err := s.Create(ctx, Write{Name: "loki", Kind: KindLoki, Endpoint: "https://loki.example.test", Enabled: true,
		Streams: []string{"traces"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("traces to Loki = %v", err)
	}
	for _, link := range []string{"javascript:alert('{traceId}')", "https://tempo.example.test/trace", "ftp://x/{traceId}", "https://x/{traceId} y"} {
		if _, err := s.Create(ctx, Write{Name: "tempo", Kind: KindOTLP, Endpoint: "https://otlp.example.test", Enabled: true,
			Streams: []string{"traces"}, TraceURL: link}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("the link %q was accepted: %v", link, err)
		}
	}
	created, err := s.Create(ctx, Write{Name: "tempo", Kind: KindOTLP, Endpoint: "https://otlp.example.test", Enabled: true,
		Streams: []string{"traces", "requests"}, TraceURL: " https://grafana.example.test/explore?traceId={traceId} "})
	if err != nil || created.TraceURL != "https://grafana.example.test/explore?traceId={traceId}" ||
		strings.Join(created.Streams, ",") != "requests,traces" {
		t.Fatalf("created = %+v, %v", created, err)
	}
	// Without the traces stream there is no trace to link to.
	updated, err := s.Update(ctx, created.ID, Write{Name: "tempo", Kind: KindOTLP, Endpoint: "https://otlp.example.test", Enabled: true,
		Streams: []string{"requests"}, TraceURL: "https://grafana.example.test/explore?traceId={traceId}"})
	if err != nil || updated.TraceURL != "" {
		t.Fatalf("updated = %+v, %v", updated, err)
	}
}

// pathReceiver records each delivery by the path it was posted to.
type pathReceiver struct {
	mu     sync.Mutex
	server *httptest.Server
	bodies map[string][][]byte
	refuse map[string]int
	auth   []string
}

func newPathReceiver(t *testing.T) *pathReceiver {
	r := &pathReceiver{bodies: map[string][][]byte{}, refuse: map[string]int{}}
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		reader, err := gzip.NewReader(req.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		body, _ := io.ReadAll(reader)
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.refuse[req.URL.Path] > 0 {
			r.refuse[req.URL.Path]--
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		r.bodies[req.URL.Path] = append(r.bodies[req.URL.Path], body)
		r.auth = append(r.auth, req.Header.Get("Authorization"))
	}))
	t.Cleanup(r.server.Close)
	return r
}

func (r *pathReceiver) count(path string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.bodies[path])
}

// The manager end to end: one request the ingress recorded becomes a log
// line on /v1/logs and a span on /v1/traces with the same ids and the
// drain's header, and a refused span is retried without sending the line
// again.
func TestARecordedRequestIsShippedAsALineAndASpan(t *testing.T) {
	s := newTestStore(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	receiver := newPathReceiver(t)
	receiver.refuse["/v1/traces"] = 1
	if _, err := s.Create(ctx, Write{Name: "collector", Kind: KindOTLP, Endpoint: receiver.server.URL, Enabled: true,
		Streams: []string{"requests", "traces"}, Headers: headers(Header{Name: "Authorization", Value: "Bearer fixture"})}); err != nil {
		t.Fatal(err)
	}
	requests := &fakeRequests{}
	manager := NewManager(s, fakeCatalogue{targets: []Target{
		{ProjectID: 7, Project: "shop", EnvironmentID: 70, Environment: "production", Route: "env-70", Release: 12},
	}}, &fakeContainers{}, requests, nil)
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the request record followed", func() bool {
		manager.mu.RLock()
		defer manager.mu.RUnlock()
		return len(manager.tailers) == 1
	})
	time.Sleep(50 * time.Millisecond)
	entry, ok := accesslog.Parse(`{"ts":1793264400.0,"logger":"http.log.access","request":{"remote_ip":"203.0.113.9","proto":"HTTP/1.1","method":"GET","host":"shop.example.test","uri":"/orders/9","headers":{"User-Agent":["Mozilla/5.0 (X11; Linux x86_64)"]}},"duration":0.004,"size":12,"status":200,"trace_id":"` + testTrace + `","span_id":"` + testSpan + `","parent_span_id":""}`)
	if !ok {
		t.Fatal("the fixture entry did not parse")
	}
	requests.mu.Lock()
	requests.entries = append(requests.entries, entry)
	requests.mu.Unlock()
	eventually(t, "the span after its refusal", func() bool { return receiver.count("/v1/traces") == 1 })
	if receiver.count("/v1/logs") != 1 {
		t.Fatalf("the line was sent %d times", receiver.count("/v1/logs"))
	}
	receiver.mu.Lock()
	logBody, spanBody, auth := receiver.bodies["/v1/logs"][0], receiver.bodies["/v1/traces"][0], receiver.auth
	receiver.mu.Unlock()
	var payload tracesPayload
	if err := json.Unmarshal(spanBody, &payload); err != nil {
		t.Fatal(err)
	}
	got := payload.ResourceSpans[0].ScopeSpans[0].Spans[0]
	if got.TraceID != testTrace || got.SpanID != testSpan || got.Name != "GET /orders/{id}" ||
		got.attribute("just_dashboard.release") != "12" || got.attribute("just_dashboard.client.class") != "browser" {
		t.Fatalf("span = %+v", got)
	}
	if !strings.Contains(string(logBody), `"traceId":"`+testTrace+`"`) || !strings.Contains(string(logBody), `"spanId":"`+testSpan+`"`) {
		t.Fatalf("the log line does not name its span: %s", logBody)
	}
	for _, header := range auth {
		if header != "Bearer fixture" {
			t.Fatalf("a delivery went without the drain's header: %q", auth)
		}
	}
	eventually(t, "the delivery on record", func() bool {
		for _, drain := range mustList(t, s) {
			return drain.Status.Shipped == 2 && drain.Status.LastSuccessAt != nil
		}
		return false
	})
}

func TestASpanNeverGoesToALogsOnlyDestination(t *testing.T) {
	batch := []Record{
		{Stream: StreamRequests, Request: &Request{Method: "GET"}},
		{Stream: StreamTraces, Request: &Request{Method: "GET"}},
		{Stream: StreamOutput, Message: "up"},
	}
	logs, spans := splitSignals(batch, KindOTLP)
	if len(logs) != 2 || len(spans) != 1 {
		t.Fatalf("otlp: %d logs %d spans", len(logs), len(spans))
	}
	logs, spans = splitSignals(batch, KindLoki)
	if len(logs) != 2 || len(spans) != 0 {
		t.Fatalf("loki: %d logs %d spans", len(logs), len(spans))
	}
}
