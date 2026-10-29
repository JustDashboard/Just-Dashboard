package logdrain

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Each format against the real thing, on loopback: a Loki fed through its
// push API and through its own OTLP endpoint and read back with LogQL; an
// OpenTelemetry Collector writing what it received to a file; and Vector's
// HTTP source writing JSON lines to a file. Every record goes out through
// deliverBatch, gzip-compressed, exactly as a shipper sends it.
func TestLiveDrainsDeliverToLokiTheCollectorAndVector(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 to run Loki, an OpenTelemetry Collector and Vector containers")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	now := time.Now().UTC().Truncate(time.Millisecond)
	records := []Record{
		{At: now.Add(-3 * time.Second), ProjectID: 7, Project: "shop-live", Environment: "production", Stream: StreamOutput,
			ContainerID: "abc123def456", ContainerName: "shop-web", IOStream: "stdout", Message: "listening on :3000"},
		{At: now.Add(-2 * time.Second), ProjectID: 7, Project: "shop-live", Environment: "production", Stream: StreamOutput,
			ContainerID: "abc123def456", ContainerName: "shop-web", IOStream: "stderr", Message: `{"level":"error","msg":"payment declined"}`},
		{At: now.Add(-time.Second), ProjectID: 7, Project: "shop-live", Environment: "production", Stream: StreamRequests,
			Request: &Request{Method: "POST", Path: "/checkout", Status: 502, Size: 31, DurationMs: 12.5, Timed: true, RemoteIP: "203.0.113.9", UserAgent: "curl/8"}},
	}
	client := newClient()

	t.Run("loki", func(t *testing.T) {
		name := liveDrainContainer(t, ctx, 3100, "grafana/loki:3.5.0", "-config.file=/etc/loki/local-config.yaml")
		base := liveDrainWait(t, ctx, name, 3100, "/ready")
		if err := deliverBatch(ctx, client, KindLoki, base, nil, records, "web-live"); err != nil {
			t.Fatal(err)
		}
		otlp := make([]Record, len(records))
		copy(otlp, records)
		for i := range otlp {
			otlp[i].Project = "shop-otlp"
		}
		if err := deliverBatch(ctx, client, KindOTLP, base+"/otlp", nil, otlp, "web-live"); err != nil {
			t.Fatal(err)
		}
		pushed := liveLokiLines(t, ctx, base, `{service_name="shop-live"}`, now)
		if len(pushed) != 3 || !strings.Contains(strings.Join(pushed, "\n"), "payment declined") || !strings.Contains(strings.Join(pushed, "\n"), `"path":"/checkout"`) {
			t.Fatalf("Loki holds %q from the push API", pushed)
		}
		errors := liveLokiLines(t, ctx, base, `{service_name="shop-live", level="error"}`, now)
		if len(errors) != 2 {
			t.Fatalf("the error stream holds %q", errors)
		}
		viaOTLP := liveLokiLines(t, ctx, base, `{service_name="shop-otlp"}`, now)
		if len(viaOTLP) != 3 || !strings.Contains(strings.Join(viaOTLP, "\n"), "POST /checkout 502 12.5ms") {
			t.Fatalf("Loki holds %q from OTLP", viaOTLP)
		}
	})

	t.Run("otel collector", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Chmod(dir, 0o777); err != nil {
			t.Fatal(err)
		}
		// Logs and traces each to a file of their own.
		config := "receivers:\n  otlp:\n    protocols:\n      http:\n        endpoint: 0.0.0.0:4318\nexporters:\n  file:\n    path: /out/logs.json\n  file/traces:\n    path: /out/traces.json\n" +
			"service:\n  pipelines:\n    logs:\n      receivers: [otlp]\n      exporters: [file]\n    traces:\n      receivers: [otlp]\n      exporters: [file/traces]\n"
		if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(config), 0o644); err != nil {
			t.Fatal(err)
		}
		name := liveDrainContainer(t, ctx, 4318, "-v", dir+":/out", "otel/opentelemetry-collector-contrib:0.128.0", "--config=/out/config.yaml")
		base := liveDrainWaitPort(t, ctx, name, 4318)
		// The collector answers the first request before its pipeline is up
		// on a slow start; a refused first delivery is retried as a shipper
		// would.
		var err error
		for attempt := 0; attempt < 10; attempt++ {
			if err = deliverBatch(ctx, client, KindOTLP, base, []Header{{Name: "Authorization", Value: "Bearer live"}}, records, "web-live"); err == nil {
				break
			}
			time.Sleep(time.Second)
		}
		if err != nil {
			t.Fatal(err)
		}
		var written []byte
		for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(500 * time.Millisecond) {
			if written, _ = os.ReadFile(filepath.Join(dir, "logs.json")); len(written) > 0 {
				break
			}
		}
		text := string(written)
		for _, want := range []string{`"service.name"`, `"shop-live"`, `"deployment.environment.name"`, `"host.name"`, `"web-live"`,
			`"severityNumber":17`, `"payment declined`, `"http.response.status_code"`, `"intValue":"502"`, `"client.address"`} {
			if !strings.Contains(text, want) {
				t.Fatalf("the collector wrote no %s in:\n%s", want, text)
			}
		}

		// The traces stream: the same request as a span, on /v1/traces with
		// the drain's header, under the trace the ingress sent.
		spans := []Record{{At: now, ProjectID: 7, Project: "shop-live", Environment: "production", Release: 12, Stream: StreamTraces,
			Request: &Request{Method: "POST", Path: "/checkout/1041", Status: 502, Size: 31, DurationMs: 12.5, Timed: true,
				RemoteIP: "203.0.113.9", UserAgent: "curl/8", ClientClass: "bot",
				TraceID: "4bf92f3577b34da6a3ce929d0e0e4736", SpanID: "a3ce929d0e0e4736", ParentSpanID: "00f067aa0ba902b7"}}}
		if err := deliverSpans(ctx, client, base, []Header{{Name: "Authorization", Value: "Bearer live"}}, spans, "web-live"); err != nil {
			t.Fatal(err)
		}
		var traced []byte
		for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(500 * time.Millisecond) {
			if traced, _ = os.ReadFile(filepath.Join(dir, "traces.json")); len(traced) > 0 {
				break
			}
		}
		var exported struct {
			ResourceSpans []struct {
				Resource struct {
					Attributes []struct {
						Key   string            `json:"key"`
						Value map[string]string `json:"value"`
					} `json:"attributes"`
				} `json:"resource"`
				ScopeSpans []struct {
					Spans []struct {
						TraceID      string `json:"traceId"`
						SpanID       string `json:"spanId"`
						ParentSpanID string `json:"parentSpanId"`
						Name         string `json:"name"`
						Kind         int    `json:"kind"`
						Status       struct {
							Code int `json:"code"`
						} `json:"status"`
					} `json:"spans"`
				} `json:"scopeSpans"`
			} `json:"resourceSpans"`
		}
		if err := json.Unmarshal([]byte(strings.SplitN(strings.TrimSpace(string(traced)), "\n", 2)[0]), &exported); err != nil ||
			len(exported.ResourceSpans) != 1 || len(exported.ResourceSpans[0].ScopeSpans) != 1 {
			t.Fatalf("the collector wrote these traces (%v):\n%s", err, traced)
		}
		got := exported.ResourceSpans[0].ScopeSpans[0].Spans[0]
		if got.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" || got.SpanID != "a3ce929d0e0e4736" || got.ParentSpanID != "00f067aa0ba902b7" ||
			got.Name != "POST /checkout/{id}" || got.Kind != 2 || got.Status.Code != 2 {
			t.Fatalf("the collector received the span as %+v", got)
		}
		for _, want := range []string{`"service.version"`, `"12"`, `"http.route"`, `"just_dashboard.client.class"`, `"bot"`, `"http.server.request.duration"`} {
			if !strings.Contains(string(traced), want) {
				t.Fatalf("the span has no %s in:\n%s", want, traced)
			}
		}
	})

	t.Run("vector", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Chmod(dir, 0o777); err != nil {
			t.Fatal(err)
		}
		config := "[sources.drain]\ntype = \"http_server\"\naddress = \"0.0.0.0:8080\"\ndecoding.codec = \"json\"\nframing.method = \"newline_delimited\"\n\n" +
			"[sinks.out]\ntype = \"file\"\ninputs = [\"drain\"]\npath = \"/out/records.ndjson\"\nencoding.codec = \"json\"\n"
		if err := os.WriteFile(filepath.Join(dir, "vector.toml"), []byte(config), 0o644); err != nil {
			t.Fatal(err)
		}
		name := liveDrainContainer(t, ctx, 8080, "-v", dir+":/out", "timberio/vector:0.47.0-debian", "--config", "/out/vector.toml")
		base := liveDrainWaitPort(t, ctx, name, 8080)
		var err error
		for attempt := 0; attempt < 10; attempt++ {
			if err = deliverBatch(ctx, client, KindHTTP, base, nil, records, "web-live"); err == nil {
				break
			}
			time.Sleep(time.Second)
		}
		if err != nil {
			t.Fatal(err)
		}
		var lines []string
		for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(500 * time.Millisecond) {
			raw, _ := os.ReadFile(filepath.Join(dir, "records.ndjson"))
			if lines = strings.Split(strings.TrimSpace(string(raw)), "\n"); len(lines) >= 3 && lines[0] != "" {
				break
			}
		}
		if len(lines) != 3 {
			t.Fatalf("vector wrote %q", lines)
		}
		var request map[string]any
		if err := json.Unmarshal([]byte(lines[2]), &request); err != nil || request["service"] != "shop-live" || request["level"] != "error" {
			t.Fatalf("the request line = %v (%v)", request, err)
		}
		if nested, ok := request["request"].(map[string]any); !ok || nested["path"] != "/checkout" || nested["status"] != float64(502) {
			t.Fatalf("the request fields = %v", request["request"])
		}
	})
}

func liveDrainContainer(t *testing.T, ctx context.Context, port int, args ...string) string {
	t.Helper()
	name := fmt.Sprintf("jd-live-drain-%d", time.Now().UnixNano())
	run := append([]string{"run", "--detach", "--name", name, "--publish", fmt.Sprintf("127.0.0.1::%d", port)}, args...)
	if out, err := exec.CommandContext(ctx, "docker", run...).CombinedOutput(); err != nil {
		t.Fatalf("docker run: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "--force", name).Run() })
	return name
}

func livePublished(t *testing.T, ctx context.Context, name string, port int) string {
	t.Helper()
	out, err := exec.CommandContext(ctx, "docker", "port", name, fmt.Sprintf("%d/tcp", port)).Output()
	if err != nil {
		t.Fatal(err)
	}
	return "http://" + strings.TrimSpace(strings.Split(string(out), "\n")[0])
}

func liveDrainWait(t *testing.T, ctx context.Context, name string, port int, path string) string {
	t.Helper()
	base := livePublished(t, ctx, name, port)
	for ctx.Err() == nil {
		if response, err := http.Get(base + path); err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return base
			}
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("%s never answered %s", name, path)
	return ""
}

func liveDrainWaitPort(t *testing.T, ctx context.Context, name string, port int) string {
	t.Helper()
	base := livePublished(t, ctx, name, port)
	address := strings.TrimPrefix(base, "http://")
	for ctx.Err() == nil {
		if conn, err := net.DialTimeout("tcp", address, time.Second); err == nil {
			conn.Close()
			return base
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("%s never listened", name)
	return ""
}

func liveLokiLines(t *testing.T, ctx context.Context, base, query string, around time.Time) []string {
	t.Helper()
	values := url.Values{"query": {query}, "start": {fmt.Sprint(around.Add(-time.Minute).UnixNano())},
		"end": {fmt.Sprint(around.Add(time.Minute).UnixNano())}, "direction": {"forward"}, "limit": {"100"}}
	var lines []string
	// Loki makes what it ingested queryable within a second or two.
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(time.Second) {
		response, err := http.Get(base + "/loki/api/v1/query_range?" + values.Encode())
		if err != nil {
			continue
		}
		raw, _ := io.ReadAll(response.Body)
		response.Body.Close()
		var answer struct {
			Data struct {
				Result []struct {
					Values [][]string `json:"values"`
				} `json:"result"`
			} `json:"data"`
		}
		if json.Unmarshal(raw, &answer) != nil {
			continue
		}
		lines = lines[:0]
		for _, stream := range answer.Data.Result {
			for _, value := range stream.Values {
				lines = append(lines, value[1])
			}
		}
		if len(lines) > 0 {
			return lines
		}
	}
	return lines
}
