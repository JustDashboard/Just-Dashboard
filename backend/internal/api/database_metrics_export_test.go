package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
)

func TestPromNameTurnsEngineKeysIntoMetricNames(t *testing.T) {
	for key, want := range map[string]string{
		"rowsInserted":             "rows_inserted",
		"walBytes":                 "wal_bytes",
		"Innodb_buffer_pool_reads": "innodb_buffer_pool_reads",
		"Com select":               "com_select",
		"IOPS":                     "iops",
		"p99Ms":                    "p99_ms",
		"--odd--":                  "odd",
		"écrit":                    "crit",
	} {
		if got := promName(key); got != want {
			t.Errorf("promName(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestPromFamiliesDropWhatWouldBreakAScrape(t *testing.T) {
	out := newPromFamilies()
	labels := []promLabel{{"name", "a"}}
	out.add("x", promGauge, "first", labels, 1)
	out.add("x", promGauge, "again", labels, 2)                 // same series twice
	out.add("x", promCounter, "other kind", labels, 3)          // same name, other type
	out.add("y", promGauge, "not a number", labels, nanValue()) // JSON cannot hold it, a map can
	var b strings.Builder
	if _, err := out.WriteTo(&b); err != nil {
		t.Fatal(err)
	}
	want := "# HELP jd_database_x first\n# TYPE jd_database_x gauge\njd_database_x{name=\"a\"} 1\n"
	if b.String() != want {
		t.Fatalf("exposition = %q, want %q", b.String(), want)
	}
}

func nanValue() float64 {
	zero := 0.0
	return zero / zero
}

// promLine is one sample of the text format: name, optional labels, value.
var promLine = regexp.MustCompile(`^([a-zA-Z_:][a-zA-Z0-9_:]*)(\{(?:[a-zA-Z_][a-zA-Z0-9_]*="(?:[^"\\]|\\.)*",?)*\})? (-?[0-9.e+-]+)$`)

// checkExposition holds a body to the text format's own rules: every family is
// introduced once by HELP and TYPE, its lines follow together, and no series
// repeats. A scraper rejects the whole body on the first violation.
func checkExposition(t *testing.T, body string) map[string]string {
	t.Helper()
	typed := map[string]string{}
	seen := map[string]bool{}
	closed := map[string]bool{}
	current := ""
	samples := map[string]string{}
	for _, line := range strings.Split(strings.TrimSuffix(body, "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "# HELP "):
			current = strings.Fields(line)[2]
			if closed[current] || typed[current] != "" {
				t.Fatalf("family %s introduced twice", current)
			}
		case strings.HasPrefix(line, "# TYPE "):
			fields := strings.Fields(line)
			if fields[2] != current {
				t.Fatalf("TYPE for %s follows HELP for %s", fields[2], current)
			}
			typed[current] = fields[3]
		default:
			match := promLine.FindStringSubmatch(line)
			if match == nil {
				t.Fatalf("not a sample line: %q", line)
			}
			if match[1] != current {
				t.Fatalf("%s sample outside its family %s", match[1], current)
			}
			if seen[match[1]+match[2]] {
				t.Fatalf("series repeated: %s%s", match[1], match[2])
			}
			seen[match[1]+match[2]] = true
			samples[match[1]+match[2]] = match[3]
		}
		if current != "" {
			for name := range typed {
				if name != current {
					closed[name] = true
				}
			}
		}
	}
	return samples
}

// scrape asks the endpoint with a bearer key, from the loopback address the
// test allowlist admits.
func scrape(s *Server, method, secret string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/api/v1/metrics/databases", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	return w
}

func testMetricsKey(t *testing.T, s *Server, scope bool) string {
	t.Helper()
	var user *auth.User
	var err error
	user, err = s.Auth.CreateUser(t.Context(), fmt.Sprintf("scraper-%t", scope), "Correct-Horse-Battery-9", auth.RoleAdmin, false)
	if err != nil {
		t.Fatal(err)
	}
	var secret string
	if scope {
		_, secret, err = s.Auth.CreateScopedAPIToken(t.Context(), user, "prometheus", auth.CapMetricsRead, time.Hour)
	} else {
		_, secret, err = s.Auth.CreateAPIToken(t.Context(), user, "everything", auth.RoleReadOnly, time.Hour)
	}
	if err != nil {
		t.Fatal(err)
	}
	return secret
}

func saveSample(t *testing.T, s *Server, id int64, age time.Duration, snapshot string) {
	t.Helper()
	rec, err := scanDBConn(s.Store.DB.QueryRow(`SELECT `+dbConnColumns+` FROM db_connections WHERE id=?`, id).Scan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`INSERT INTO db_metric_samples VALUES(?,?,?,?)`,
		id, databaseMetricIdentity(rec), time.Now().Add(-age).UnixMilli(), snapshot); err != nil {
		t.Fatal(err)
	}
}

const postgresSnapshot = `{"supported":true,"at":"2026-10-17T10:00:00Z","driver":"postgres","version":"17.2","role":"primary",
	"database":"shop","uptimeSeconds":86400,"databaseBytes":1048576,
	"connections":{"total":12,"active":3,"idle":8,"idleInTransaction":1,"waiting":0,"max":100},
	"counters":{"transactionsCommitted":5000,"rowsInserted":42,"walBytes":9000},
	"gauges":{"databaseConnections":12,"replicationLagSeconds":0.5},
	"pool":{"open":2,"inUse":1,"idle":1,"waitCount":0,"waitDuration":"0s","maxOpen":10,"maxIdleClosed":0,"maxLifetimeClosed":0}}`

const redisSnapshot = `{"server":{"redis_version":"8.0.0","counters":{"uptime_in_seconds":600,"connected_clients":3,
	"used_memory":2048,"total_commands_processed":1000,"keyspace_hits":300,"keyspace_misses":20,"keys":7},
	"flavor":"redis","version":"8.0.0","role":"primary","mode":"standalone"}}`

const mongoSnapshot = `{"server":{"version":"8.0.1","role":"primary","uptime":900,"connections":{"current":5,"available":800},
	"mem":{"resident":128},"network":{"bytesIn":1000,"bytesOut":2000,"numRequests":30},
	"opcounters":{"insert":1,"query":2,"update":3,"delete":4,"getmore":5,"command":6},
	"documents":{"inserted":7,"returned":8,"updated":9,"deleted":10}}}`

func TestDatabaseMetricsExportServesEveryRecordedServer(t *testing.T) {
	s := testServer(t)
	pg := saveOpsConnection(t, s, `main "primary"\1`, dbx.DriverPostgres, "postgres://user:private-password@127.0.0.1:1/shop")
	cache := saveOpsConnection(t, s, "cache", dbx.DriverRedis, "redis://127.0.0.1:1/0")
	docs := saveOpsConnection(t, s, "docs", dbx.DriverMongo, "mongodb://user:private-password@127.0.0.1:1/")
	stale := saveOpsConnection(t, s, "stale", dbx.DriverMySQL, "mysql://user:pw@127.0.0.1:1/db")
	saveOpsConnection(t, s, "never", dbx.DriverClickHouse, "clickhouse://127.0.0.1:1/db")
	saveOpsConnection(t, s, "file", dbx.DriverSQLite, s.Cfg.FileRoots[0]+"/x.db")
	saveSample(t, s, pg, 5*time.Second, postgresSnapshot)
	// A newer reading wins over an older one.
	saveSample(t, s, pg, 5*time.Minute, strings.Replace(postgresSnapshot, `"rowsInserted":42`, `"rowsInserted":1`, 1))
	saveSample(t, s, cache, 10*time.Second, redisSnapshot)
	saveSample(t, s, docs, 10*time.Second, mongoSnapshot)
	saveSample(t, s, stale, 10*time.Minute, postgresSnapshot)

	reply := scrape(s, http.MethodGet, testMetricsKey(t, s, true))
	if reply.Code != http.StatusOK {
		t.Fatalf("scrape = %d %s", reply.Code, reply.Body.String())
	}
	if got := reply.Header().Get("Content-Type"); got != "text/plain; version=0.0.4; charset=utf-8" {
		t.Fatalf("content type = %q", got)
	}
	if reply.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("a scrape was cacheable")
	}
	body := reply.Body.String()
	samples := checkExposition(t, body)

	pgLabels := fmt.Sprintf(`{driver="postgres",id="%d",name="main \"primary\"\\1"}`, pg)
	for series, want := range map[string]string{
		"jd_database_up" + pgLabels:                                                                                "1",
		"jd_database_connections" + pgLabels:                                                                       "12",
		"jd_database_connections_idle_in_transaction" + pgLabels:                                                   "1",
		"jd_database_connections_max" + pgLabels:                                                                   "100",
		"jd_database_size_bytes" + pgLabels:                                                                        "1048576",
		"jd_database_uptime_seconds" + pgLabels:                                                                    "86400",
		"jd_database_rows_inserted_total" + pgLabels:                                                               "42",
		"jd_database_transactions_committed_total" + pgLabels:                                                      "5000",
		"jd_database_replication_lag_seconds" + pgLabels:                                                           "0.5",
		"jd_database_pool_connections_in_use" + pgLabels:                                                           "1",
		fmt.Sprintf(`jd_database_up{driver="redis",id="%d",name="cache"}`, cache):                                  "1",
		fmt.Sprintf(`jd_database_commands_total{driver="redis",id="%d",name="cache"}`, cache):                      "1000",
		fmt.Sprintf(`jd_database_connections{driver="redis",id="%d",name="cache"}`, cache):                         "3",
		fmt.Sprintf(`jd_database_keys{driver="redis",id="%d",name="cache"}`, cache):                                "7",
		fmt.Sprintf(`jd_database_operations_total{driver="mongodb",id="%d",name="docs",operation="update"}`, docs): "3",
		fmt.Sprintf(`jd_database_memory_resident_bytes{driver="mongodb",id="%d",name="docs"}`, docs):               "134217728",
	} {
		if samples[series] != want {
			t.Errorf("%s = %q, want %q", series, samples[series], want)
		}
	}
	if !strings.Contains(body, fmt.Sprintf(`jd_database_info{database="shop",driver="postgres",id="%d",name="main \"primary\"\\1",role="primary",version="17.2"} 1`, pg)) {
		t.Errorf("no info series for the SQL server:\n%s", body)
	}

	// A reading older than three intervals says the server is quiet and
	// nothing else: its figures would pass for current ones.
	staleUp := fmt.Sprintf(`jd_database_up{driver="mysql",id="%d",name="stale"}`, stale)
	if samples[staleUp] != "0" {
		t.Errorf("stale server up = %q", samples[staleUp])
	}
	for series := range samples {
		if strings.Contains(series, `name="stale"`) && !strings.HasPrefix(series, "jd_database_up") && !strings.HasPrefix(series, "jd_database_last_sample_timestamp_seconds") {
			t.Errorf("stale server still exports %s", series)
		}
	}
	// Never recorded and file-backed servers have no series at all.
	if strings.Contains(body, `name="never"`) || strings.Contains(body, `name="file"`) {
		t.Errorf("servers with nothing recorded were exported:\n%s", body)
	}
	if strings.Contains(body, "private-password") {
		t.Fatal("the endpoint disclosed a connection credential")
	}
}

func TestDatabaseMetricsExportAnswersNothingToTheWrongCaller(t *testing.T) {
	s := testServer(t)
	scoped := testMetricsKey(t, s, true)

	if w := scrape(s, http.MethodGet, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("no credential = %d", w.Code)
	}
	if w := scrape(s, http.MethodGet, "vpsd_"+strings.Repeat("0", 43)); w.Code != http.StatusUnauthorized {
		t.Fatalf("unknown key = %d", w.Code)
	}
	if w := scrape(s, http.MethodPost, scoped); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST = %d", w.Code)
	}

	// A signed-in browser is not an API key.
	viewer := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "browser", auth.RoleAdmin)}
	if w := viewer.do(http.MethodGet, "/api/v1/metrics/databases", "", nil); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "token_required") {
		t.Fatalf("session = %d %s", w.Code, w.Body.String())
	}

	// An ordinary key reads it too: it holds the capability through its role.
	if w := scrape(s, http.MethodGet, testMetricsKey(t, s, false)); w.Code != http.StatusOK {
		t.Fatalf("ordinary key = %d %s", w.Code, w.Body.String())
	}

	// A revoked key stops working at once.
	var id int64
	s.Store.DB.QueryRow(`SELECT id FROM api_tokens WHERE scope='metrics.read'`).Scan(&id)
	if err := s.Auth.RevokeAPIToken(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if w := scrape(s, http.MethodGet, scoped); w.Code != http.StatusUnauthorized {
		t.Fatalf("revoked key = %d", w.Code)
	}
}

func TestAScopedKeyReachesNothingButTheMetrics(t *testing.T) {
	s := testServer(t)
	secret := testMetricsKey(t, s, true)
	if w := scrape(s, http.MethodGet, secret); w.Code != http.StatusOK {
		t.Fatalf("scoped key on its own route = %d %s", w.Code, w.Body.String())
	}
	h := s.Routes()
	// Every route the walk finds, but the scrape itself, refuses the key:
	// reads, writes and the ones a readonly role may call.
	checked := 0
	for _, rt := range apiRoutes(t, h) {
		if rt.pattern == "/api/v1"+databaseMetricsPath {
			continue
		}
		req := httptest.NewRequest(rt.method, rt.path, nil)
		req.RemoteAddr = "127.0.0.1:9999"
		req.Header.Set("Authorization", "Bearer "+secret)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		checked++
		if w.Code < 400 {
			t.Errorf("%s %s answered a scoped key %d", rt.method, rt.pattern, w.Code)
		}
	}
	if checked < 100 {
		t.Fatalf("only %d routes were tried", checked)
	}
	for _, path := range []string{"/api/v1/databases/fleet", "/api/v1/tokens/", "/api/v1/system/host", "/api/v1/databases/metrics-export"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "127.0.0.1:9999"
		req.Header.Set("Authorization", "Bearer "+secret)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "key_scope") {
			t.Errorf("GET %s with a scoped key = %d %s", path, w.Code, w.Body.String())
		}
	}
}

func TestMintingAScopedKeyIsAuditedAndReadOnly(t *testing.T) {
	c, s := newClient(t)
	minted := c.do(http.MethodPost, "/api/v1/tokens/", `{"name":"prometheus","scope":"metrics.read","ttlDays":90}`, nil)
	var result struct {
		Token  auth.APIToken `json:"token"`
		Secret string        `json:"secret"`
	}
	if err := json.Unmarshal(minted.Body.Bytes(), &result); err != nil || minted.Code != http.StatusCreated {
		t.Fatalf("create = %d %s: %v", minted.Code, minted.Body.String(), err)
	}
	if result.Token.Scope != "metrics.read" || result.Token.Role != auth.RoleReadOnly {
		t.Fatalf("scoped key = %+v", result.Token)
	}
	if w := scrape(s, http.MethodGet, result.Secret); w.Code != http.StatusOK {
		t.Fatalf("minted key does not scrape: %d %s", w.Code, w.Body.String())
	}
	if list := c.do(http.MethodGet, "/api/v1/tokens/", "", nil); !strings.Contains(list.Body.String(), `"scope":"metrics.read"`) {
		t.Fatalf("list lost the scope: %s", list.Body.String())
	}
	var detail string
	if err := s.Store.DB.QueryRow(`SELECT detail FROM audit_log WHERE action='dashboard.token.create'`).Scan(&detail); err != nil || !strings.Contains(detail, "metrics.read") {
		t.Fatalf("audit detail = %q, %v", detail, err)
	}
	for _, body := range []string{
		`{"name":"x","scope":"system.admin"}`,
		`{"name":"x","scope":"read"}`,
		`{"name":"","scope":"metrics.read"}`,
	} {
		if w := c.do(http.MethodPost, "/api/v1/tokens/", body, nil); w.Code != http.StatusBadRequest {
			t.Errorf("%s = %d", body, w.Code)
		}
	}
	// A key cannot mint another, scoped or not.
	if w := c.do(http.MethodPost, "/api/v1/tokens/", `{"name":"y","scope":"metrics.read"}`, map[string]string{"Authorization": "Bearer " + result.Secret}); w.Code != http.StatusForbidden {
		t.Fatalf("a scoped key minted a key: %d", w.Code)
	}
}
