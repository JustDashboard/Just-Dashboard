package logdrain

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store/storetest"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := storetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sealer, err := auth.NewSealer(strings.Repeat("4d", 32))
	if err != nil {
		t.Fatal(err)
	}
	return NewStore(st, sealer)
}

func headers(list ...Header) *[]Header { return &list }

func TestDrainWritesAreCheckedAndHeadersSealed(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	created, err := s.Create(ctx, Write{Name: "Grafana Cloud", Kind: KindOTLP, Endpoint: "https://otlp.example.test/otlp", Enabled: true,
		Streams: []string{"requests", "output", "output"}, Projects: []int64{7, 3, 7},
		Headers: headers(Header{Name: "Authorization", Value: "Basic fixture-secret"})})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(created.Streams, ",") != "output,requests" || len(created.Projects) != 2 || created.Projects[0] != 3 ||
		len(created.HeaderNames) != 1 || created.HeaderNames[0] != "Authorization" {
		t.Fatalf("created = %+v", created)
	}
	listed, _ := json.Marshal(mustList(t, s))
	if strings.Contains(string(listed), "fixture-secret") {
		t.Fatal("a header value was read back")
	}
	var sealed string
	if err := s.db.QueryRow(`SELECT config_enc FROM deploy_log_drains WHERE id=?`, created.ID).Scan(&sealed); err != nil || strings.Contains(sealed, "fixture-secret") {
		t.Fatalf("the header is stored in the clear: %v", err)
	}
	// An edit without headers keeps them; an empty list removes them.
	updated, err := s.Update(ctx, created.ID, Write{Name: "Grafana Cloud", Kind: KindOTLP, Endpoint: "https://otlp.example.test/otlp", Enabled: true, Streams: []string{"output"}})
	if err != nil || len(updated.HeaderNames) != 1 {
		t.Fatalf("headers lost on edit: %+v %v", updated, err)
	}
	cleared, err := s.Update(ctx, created.ID, Write{Name: "Grafana Cloud", Kind: KindOTLP, Endpoint: "https://otlp.example.test/otlp", Enabled: true, Streams: []string{"output"}, Headers: headers()})
	if err != nil || len(cleared.HeaderNames) != 0 {
		t.Fatalf("headers not cleared: %+v %v", cleared, err)
	}
	if _, err := s.Create(ctx, Write{Name: "Grafana Cloud", Kind: KindLoki, Endpoint: "https://loki.example.test", Streams: []string{"output"}}); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("duplicate name = %v", err)
	}

	refused := []Write{
		{Name: "", Kind: KindOTLP, Endpoint: "https://x.test", Streams: []string{"output"}},
		{Name: "a", Kind: "syslog", Endpoint: "https://x.test", Streams: []string{"output"}},
		{Name: "a", Kind: KindOTLP, Endpoint: "https://x.test", Streams: nil},
		{Name: "a", Kind: KindOTLP, Endpoint: "https://x.test", Streams: []string{"metrics"}},
		{Name: "a", Kind: KindOTLP, Endpoint: "ftp://x.test", Streams: []string{"output"}},
		{Name: "a", Kind: KindOTLP, Endpoint: "https://user:pass@x.test", Streams: []string{"output"}},
		// A credential never crosses the network unencrypted.
		{Name: "a", Kind: KindLoki, Endpoint: "http://loki.lan:3100", Streams: []string{"output"}, Headers: headers(Header{Name: "Authorization", Value: "Bearer x"})},
		{Name: "a", Kind: KindHTTP, Endpoint: "https://x.test", Streams: []string{"output"}, Headers: headers(Header{Name: "Host", Value: "evil.test"})},
		{Name: "a", Kind: KindHTTP, Endpoint: "https://x.test", Streams: []string{"output"}, Headers: headers(Header{Name: "X-Token", Value: "a\r\nInjected: 1"})},
		{Name: "a", Kind: KindHTTP, Endpoint: "https://x.test", Streams: []string{"output"}, Projects: []int64{0}},
	}
	for _, w := range refused {
		if _, err := s.Create(ctx, w); !errors.Is(err, ErrInvalid) {
			t.Errorf("Create(%+v) = %v", w, err)
		}
	}
	// No headers: plain http to a collector on the local network is fine;
	// to this machine, even with headers.
	if _, err := s.Create(ctx, Write{Name: "lan collector", Kind: KindOTLP, Endpoint: "http://10.0.0.5:4318", Streams: []string{"output"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, Write{Name: "local loki", Kind: KindLoki, Endpoint: "http://127.0.0.1:3100", Streams: []string{"output"}, Headers: headers(Header{Name: "X-Scope-OrgID", Value: "tenant"})}); err != nil {
		t.Fatal(err)
	}
}

func mustList(t *testing.T, s *Store) []Drain {
	t.Helper()
	list, err := s.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func TestLevelIsWhatALineSaysOfItself(t *testing.T) {
	cases := map[string]string{
		`{"level":"error","msg":"boom"}`:                "error",
		`{"level":30,"msg":"pino info"}`:                "info",
		`{"level":50,"msg":"pino error"}`:               "error",
		`{"severity":"WARNING","message":"x"}`:          "warn",
		`time=2026-10-26 level=debug msg="cache miss"`:  "debug",
		`ERROR: relation "users" does not exist`:        "error",
		`[WARN] deprecated option`:                      "warn",
		`2026-10-26T10:00:00Z INFO server started`:      "info",
		`npm notice New major version of npm available`: "",
		`Ready in 236ms`:                                "",
		`errors are handled elsewhere`:                  "",
	}
	for line, want := range cases {
		if got := (Record{Message: line}).Level(); got != want {
			t.Errorf("Level(%q) = %q, want %q", line, got, want)
		}
	}
	if got := (Record{Request: &Request{Status: 503}}).Level(); got != "error" {
		t.Fatalf("a 503 = %q", got)
	}
	if got := (Record{Request: &Request{Status: 404}}).Level(); got != "warn" {
		t.Fatalf("a 404 = %q", got)
	}
}

var fixtureRecords = []Record{
	{At: time.Date(2026, 10, 26, 10, 0, 1, 500, time.UTC), ProjectID: 7, Project: "shop", Environment: "production", Stream: StreamOutput,
		ContainerID: "abc123def456", ContainerName: "shop-web", IOStream: "stderr", Message: `{"level":"error","msg":"payment failed"}`},
	{At: time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC), ProjectID: 7, Project: "shop", Environment: "production", Stream: StreamOutput,
		ContainerID: "abc123def456", ContainerName: "shop-web", IOStream: "stdout", Message: "listening on :3000"},
	{At: time.Date(2026, 10, 26, 10, 0, 2, 0, time.UTC), ProjectID: 7, Project: "shop", Environment: "production", Stream: StreamRequests,
		Request: &Request{Method: "GET", Path: "/checkout", Query: "step=2", Host: "shop.example.test", Proto: "HTTP/2.0", Status: 502, Size: 120, DurationMs: 31.5, Timed: true, RemoteIP: "203.0.113.9", UserAgent: "curl/8"}},
	{At: time.Date(2026, 10, 26, 10, 0, 3, 0, time.UTC), ProjectID: 9, Project: "blog", Environment: "pr-4", Stream: StreamOutput, IOStream: "stdout", Message: "built in 2s"},
}

func TestOTLPIsTheLogsProtoInJSON(t *testing.T) {
	encoded, err := encode(KindOTLP, fixtureRecords, "web-1")
	if err != nil || encoded.ContentType != "application/json" {
		t.Fatal(err)
	}
	var payload struct {
		ResourceLogs []struct {
			Resource struct {
				Attributes []struct {
					Key   string         `json:"key"`
					Value map[string]any `json:"value"`
				} `json:"attributes"`
			} `json:"resource"`
			ScopeLogs []struct {
				LogRecords []struct {
					TimeUnixNano   string         `json:"timeUnixNano"`
					SeverityNumber int            `json:"severityNumber"`
					SeverityText   string         `json:"severityText"`
					Body           map[string]any `json:"body"`
					Attributes     []struct {
						Key   string         `json:"key"`
						Value map[string]any `json:"value"`
					} `json:"attributes"`
				} `json:"logRecords"`
			} `json:"scopeLogs"`
		} `json:"resourceLogs"`
	}
	if err := json.Unmarshal(encoded.Body, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.ResourceLogs) != 2 {
		t.Fatalf("one resource per project and environment: %d", len(payload.ResourceLogs))
	}
	resource := map[string]any{}
	for _, attribute := range payload.ResourceLogs[0].Resource.Attributes {
		resource[attribute.Key] = attribute.Value["stringValue"]
	}
	if resource["service.name"] != "shop" || resource["deployment.environment.name"] != "production" || resource["host.name"] != "web-1" {
		t.Fatalf("resource = %v", resource)
	}
	records := payload.ResourceLogs[0].ScopeLogs[0].LogRecords
	if len(records) != 3 || records[0].SeverityNumber != 17 || records[0].SeverityText != "ERROR" ||
		records[0].TimeUnixNano != strconv.FormatInt(fixtureRecords[0].At.UnixNano(), 10) || records[1].SeverityNumber != 0 {
		t.Fatalf("records = %+v", records)
	}
	request := records[2]
	attributes := map[string]map[string]any{}
	for _, attribute := range request.Attributes {
		attributes[attribute.Key] = attribute.Value
	}
	if request.Body["stringValue"] != "GET /checkout?step=2 502 31.5ms" || attributes["http.response.status_code"]["intValue"] != "502" ||
		attributes["url.path"]["stringValue"] != "/checkout" || attributes["http.server.request.duration"]["doubleValue"] != 0.0315 ||
		attributes["client.address"]["stringValue"] != "203.0.113.9" ||
		attributes["network.protocol.name"]["stringValue"] != "http" || attributes["network.protocol.version"]["stringValue"] != "2.0" {
		t.Fatalf("request record = %+v", request)
	}
}

func TestLokiStreamsAreFewAndInOrder(t *testing.T) {
	encoded, err := encode(KindLoki, fixtureRecords, "web-1")
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Streams []struct {
			Stream map[string]string `json:"stream"`
			Values [][]string        `json:"values"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(encoded.Body, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Streams) != 4 {
		t.Fatalf("streams = %+v", payload.Streams)
	}
	first := payload.Streams[0]
	if first.Stream["service_name"] != "shop" || first.Stream["environment"] != "production" || first.Stream["level"] != "error" ||
		first.Stream["iostream"] != "stderr" || first.Stream["host"] != "web-1" {
		t.Fatalf("labels = %v", first.Stream)
	}
	var requests []string
	for _, stream := range payload.Streams {
		if stream.Stream["stream"] == "requests" {
			requests = stream.Values[0]
		}
	}
	var request map[string]any
	if requests == nil || json.Unmarshal([]byte(requests[1]), &request) != nil || request["path"] != "/checkout" || request["status"] != float64(502) {
		t.Fatalf("request line = %v", requests)
	}
}

func TestJSONLinesAreOneObjectPerRecord(t *testing.T) {
	encoded, err := encode(KindHTTP, fixtureRecords, "web-1")
	if err != nil || encoded.ContentType != "application/x-ndjson" {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(encoded.Body)), "\n")
	if len(lines) != 4 {
		t.Fatalf("lines = %q", lines)
	}
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil || first["service"] != "shop" || first["level"] != "error" ||
		first["container_name"] != "shop-web" || first["timestamp"] != "2026-10-26T10:00:01.0000005Z" {
		t.Fatalf("first = %v (%v)", first, err)
	}
}

func TestEndpointGetsTheKindsPathOnlyWhenItHasNone(t *testing.T) {
	for _, test := range []struct{ kind, in, want string }{
		{KindOTLP, "http://collector:4318", "http://collector:4318/v1/logs"},
		{KindOTLP, "https://otlp-gateway.example.test/otlp", "https://otlp-gateway.example.test/otlp/v1/logs"},
		{KindOTLP, "https://otlp.example.test/otlp/v1/logs", "https://otlp.example.test/otlp/v1/logs"},
		{KindLoki, "https://logs.example.test/", "https://logs.example.test/loki/api/v1/push"},
		{KindLoki, "https://example.test/logs", "https://example.test/logs/loki/api/v1/push"},
		{KindLoki, "http://loki:3100/loki/api/v1/push", "http://loki:3100/loki/api/v1/push"},
		{KindHTTP, "https://in.example.test/ingest", "https://in.example.test/ingest"},
	} {
		if got := endpointFor(test.kind, test.in); got != test.want {
			t.Errorf("endpointFor(%s, %s) = %s", test.kind, test.in, got)
		}
	}
}

// receiver is a destination that records what it got, refusing the first
// few deliveries when asked to.
type receiver struct {
	mu       sync.Mutex
	server   *httptest.Server
	bodies   [][]byte
	headers  []http.Header
	refuse   int
	attempts int
}

func newReceiver(t *testing.T) *receiver {
	r := &receiver{}
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		reader, err := gzip.NewReader(req.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		body, _ := io.ReadAll(reader)
		r.mu.Lock()
		defer r.mu.Unlock()
		r.attempts++
		if r.refuse > 0 {
			r.refuse--
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("overloaded"))
			return
		}
		r.bodies = append(r.bodies, body)
		r.headers = append(r.headers, req.Header.Clone())
	}))
	t.Cleanup(r.server.Close)
	return r
}

func (r *receiver) lines() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, body := range r.bodies {
		out = append(out, strings.Split(strings.TrimSpace(string(body)), "\n")...)
	}
	return out
}

type fakeCatalogue struct{ targets []Target }

func (c fakeCatalogue) DrainTargets(context.Context) ([]Target, error) { return c.targets, nil }

type fakeContainers struct {
	mu         sync.Mutex
	containers []Container
	lines      map[string]chan Line
	follows    []time.Time
}

func (c *fakeContainers) DeploymentContainers(context.Context) ([]Container, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Container(nil), c.containers...), nil
}

func (c *fakeContainers) Follow(ctx context.Context, id string, since time.Time) (<-chan Line, io.Closer, error) {
	c.mu.Lock()
	c.follows = append(c.follows, since)
	source := c.lines[id]
	c.mu.Unlock()
	out := make(chan Line)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case line, ok := <-source:
				if !ok {
					return
				}
				out <- line
			}
		}
	}()
	return out, io.NopCloser(nil), nil
}

type fakeRequests struct {
	mu      sync.Mutex
	entries []accesslog.Entry
}

func (r *fakeRequests) After(_ context.Context, _ string, after uint64, limit int) ([]accesslog.Entry, uint64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if after == accesslog.FromNow {
		return nil, uint64(len(r.entries)), nil
	}
	if int(after) >= len(r.entries) {
		return nil, after, nil
	}
	return append([]accesslog.Entry(nil), r.entries[after:]...), uint64(len(r.entries)), nil
}

func eventually(t *testing.T, what string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestManagerShipsWhatEachDrainAsksForAndRetriesARefusal(t *testing.T) {
	s := newTestStore(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	everything := newReceiver(t)
	everything.refuse = 1
	shopOnly := newReceiver(t)
	if _, err := s.Create(ctx, Write{Name: "all", Kind: KindHTTP, Endpoint: everything.server.URL, Enabled: true, Streams: []string{"output", "requests"},
		Headers: headers(Header{Name: "Authorization", Value: "Bearer fixture"})}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, Write{Name: "shop requests", Kind: KindHTTP, Endpoint: shopOnly.server.URL, Enabled: true, Streams: []string{"requests"}, Projects: []int64{7}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, Write{Name: "paused", Kind: KindHTTP, Endpoint: shopOnly.server.URL, Enabled: false, Streams: []string{"output"}}); err != nil {
		t.Fatal(err)
	}
	containers := &fakeContainers{lines: map[string]chan Line{"c-shop": make(chan Line, 8), "c-blog": make(chan Line, 8)}}
	created := time.Now().Add(time.Hour)
	containers.containers = []Container{{ID: "c-shop", Name: "shop-web", EnvironmentID: 70, Created: created}, {ID: "c-blog", Name: "blog-web", EnvironmentID: 90}}
	requests := &fakeRequests{}
	manager := NewManager(s, fakeCatalogue{targets: []Target{
		{ProjectID: 7, Project: "shop", EnvironmentID: 70, Environment: "production", Route: "env-70"},
		{ProjectID: 9, Project: "blog", EnvironmentID: 90, Environment: "production", Route: "env-90"},
	}}, containers, requests, nil)
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	eventually(t, "both containers followed", func() bool {
		containers.mu.Lock()
		defer containers.mu.Unlock()
		return len(containers.follows) == 2
	})
	// A container created after the dashboard started is read from its
	// creation, so its first lines are not lost to the reconcile delay.
	containers.mu.Lock()
	sawCreated := containers.follows[0].Equal(created) || containers.follows[1].Equal(created)
	containers.mu.Unlock()
	if !sawCreated {
		t.Fatal("a new container was not followed from its creation")
	}
	containers.lines["c-shop"] <- Line{At: created.Add(time.Second), Stream: "stdout", Text: "shop is up"}
	containers.lines["c-blog"] <- Line{At: time.Now(), Stream: "stderr", Text: "blog is up"}
	eventually(t, "the output after the refusal", func() bool { return len(everything.lines()) >= 2 })
	if got := strings.Join(everything.lines(), "\n"); !strings.Contains(got, "shop is up") || !strings.Contains(got, "blog is up") {
		t.Fatalf("everything got %s", got)
	}
	everything.mu.Lock()
	attempts, authorization := everything.attempts, everything.headers[0].Get("Authorization")
	everything.mu.Unlock()
	if attempts < 2 || authorization != "Bearer fixture" {
		t.Fatalf("attempts = %d, authorization = %q", attempts, authorization)
	}
	if len(shopOnly.lines()) != 0 {
		t.Fatal("a requests-only drain received output, or a paused drain shipped")
	}
	// The status follows the delivery: two shipped after a refusal that is
	// still on record with its time.
	eventually(t, "the delivery on record", func() bool {
		for _, drain := range mustList(t, s) {
			if drain.Name == "all" {
				return drain.Status.Shipped == 2 && drain.Status.LastSuccessAt != nil && drain.Status.LastError != "" &&
					drain.Status.LastErrorAt.Before(*drain.Status.LastSuccessAt)
			}
		}
		return false
	})
}

func TestShipperDropsTheOldestPastItsBoundAndSaysSo(t *testing.T) {
	s := newTestStore(t)
	created, err := s.Create(t.Context(), Write{Name: "down", Kind: KindHTTP, Endpoint: "http://127.0.0.1:1", Enabled: true, Streams: []string{"output"}})
	if err != nil {
		t.Fatal(err)
	}
	row, _ := s.get(t.Context(), created.ID)
	shipper := newShipper(row, s, newClient(), "", nil)
	for i := 0; i < queueLimit+5; i++ {
		shipper.offer(Record{Message: "line", Stream: StreamOutput})
	}
	if shipper.queued() != queueLimit {
		t.Fatalf("queued = %d", shipper.queued())
	}
	if err := shipper.deliver(t.Context()); err == nil {
		t.Fatal("a delivery to a closed port succeeded")
	}
	if shipper.queued() != queueLimit {
		t.Fatalf("a failed batch was not put back: %d", shipper.queued())
	}
	shipper.mu.Lock()
	dropped := shipper.dropped
	shipper.mu.Unlock()
	if dropped != 5 {
		t.Fatalf("dropped = %d", dropped)
	}
}
