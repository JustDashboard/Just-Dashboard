package api

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// The connection routes against real servers.
//
// Like the other live suites these skip rather than fail when an engine is
// not there. Unlike them they never fall back to an engine's standard port: a
// variable that is unset skips the engine, because on a machine that runs
// databases the standard port is somebody's production server.

type liveSummary struct {
	summaryView
	Version       string `json:"version"`
	VersionNumber string `json:"versionNumber"`
	FlavorLabel   string `json:"flavorLabel"`
	LatencyMs     int64  `json:"latencyMs"`
}

func TestLiveConnectionSummaryAndFleet(t *testing.T) {
	for _, c := range []struct {
		driver dbx.Driver
		env    string
	}{
		{dbx.DriverPostgres, "JD_TEST_POSTGRES_DSN"},
		{dbx.DriverMySQL, "JD_TEST_MYSQL_DSN"},
		{dbx.DriverRedis, "JD_TEST_REDIS_DSN"},
		{dbx.DriverMongo, "JD_TEST_MONGO_DSN"},
		{dbx.DriverClickHouse, "JD_TEST_CLICKHOUSE_DSN"},
	} {
		t.Run(string(c.driver), func(t *testing.T) {
			dsn := os.Getenv(c.env)
			if dsn == "" {
				t.Skipf("%s unset", c.env)
			}
			_, router, id := liveAPIRouter(t, c.driver, dsn)

			rec := do(t, router, http.MethodGet, pathf("/databases/%d", id), "")
			if rec.Code != http.StatusOK {
				t.Fatalf("summary = %d %s", rec.Code, rec.Body.String())
			}
			got := readJSON[liveSummary](t, rec)
			if got.State != dbStateRunning || !got.OK || got.Error != "" {
				t.Fatalf("a server that answers its ping reads as %q: %+v", got.State, got)
			}
			if !dbx.FlavorOf(c.driver, got.Flavor) || got.FlavorLabel == "" || got.Version == "" || got.VersionNumber == "" {
				t.Errorf("identity = %q (%q) %q / %q", got.Flavor, got.FlavorLabel, got.Version, got.VersionNumber)
			}
			if got.Capabilities["dump"] != true {
				t.Errorf("capabilities = %v", got.Capabilities)
			}
			switch got.Source {
			case "docker", "host", "remote":
			default:
				t.Errorf("source = %q", got.Source)
			}

			// The fleet says the same of it, and says it again from what it
			// kept rather than from a second dial.
			for range 2 {
				fleet := readJSON[dbFleetView](t, do(t, router, http.MethodGet, "/databases/fleet", ""))
				if len(fleet.Connections) != 1 || fleet.Connections[0].State != dbStateRunning ||
					fleet.Connections[0].Flavor != got.Flavor || fleet.Connections[0].Source != got.Source {
					t.Errorf("fleet = %+v, summary said %s/%s", fleet.Connections, got.Flavor, got.Source)
				}
			}

			// And the test button reaches it, which for Redis it never did.
			test := readJSON[struct {
				OK      bool   `json:"ok"`
				Error   string `json:"error"`
				Flavor  string `json:"flavor"`
				Version string `json:"version"`
			}](t, do(t, router, http.MethodPost, "/databases/test",
				`{"driver":"`+string(c.driver)+`","dsn":"`+dsn+`"}`))
			if !test.OK || test.Flavor != got.Flavor || test.Version == "" {
				t.Errorf("test connection = %+v", test)
			}
			rec = do(t, router, http.MethodPost, "/databases/",
				`{"name":"probed-`+string(c.driver)+`","driver":"`+string(c.driver)+`","dsn":"`+dsn+`","probe":true}`)
			if rec.Code != http.StatusCreated {
				t.Errorf("a probed create of a server that answers = %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestLiveProvisionAdoptAndPower starts a server from each template named in
// JD_TEST_PROVISION_ENGINES ("redis,valkey,clickhouse:24.8" — an engine,
// optionally with one of its versions), connects it the way the page does,
// and turns it off and on again.
//
// It is the one test that proves a template end to end: that the image
// starts, that the password it was handed is the one it enforces, that
// detection reads the same password back, and that what answers is the
// product the template says it is. It creates containers and volumes named
// jdcc-b1b-<engine> and removes them again.
func TestLiveProvisionAdoptAndPower(t *testing.T) {
	engines := os.Getenv("JD_TEST_PROVISION_ENGINES")
	if engines == "" {
		t.Skip("JD_TEST_PROVISION_ENGINES unset")
	}
	for _, spec := range strings.Split(engines, ",") {
		engine, version, _ := strings.Cut(strings.TrimSpace(spec), ":")
		t.Run(engine, func(t *testing.T) {
			tmpl, ok := provisionTemplates[engine]
			if !ok {
				t.Fatalf("no template %q", engine)
			}
			h := &connHarness{t: t, s: testServer(t)}
			h.s.Cfg.BackupLocalDir = t.TempDir()
			// The test server has no Docker of its own; this one test is
			// given the machine's.
			docker := dockerx.New(envOr("JD_TEST_DOCKER_HOST", "unix:///var/run/docker.sock"))
			t.Cleanup(func() { _ = docker.Close() })
			h.s.modules.docker = docker
			router := h.as("admin")
			name := "jdcc-b1b-" + engine
			remove := func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				_ = docker.RemoveContainer(ctx, name, true, true)
				_ = docker.RemoveVolume(ctx, name+"-data", true)
			}
			remove()
			t.Cleanup(remove)

			rec := do(t, router, http.MethodPost, "/databases/provision",
				`{"engine":"`+engine+`","name":"`+name+`","version":"`+version+`"}`)
			if rec.Code != http.StatusAccepted {
				t.Fatalf("provision = %d %s", rec.Code, rec.Body.String())
			}
			started := readJSON[struct {
				Container string `json:"container"`
				Exposure  string `json:"exposure"`
				Flavor    string `json:"flavor"`
				Port      int    `json:"port"`
			}](t, rec)
			if started.Container != name || started.Exposure != "local" || started.Flavor != tmpl.flavor {
				t.Fatalf("provision result = %+v", started)
			}

			// Adopt proves the credentials detection read; ping proves the
			// engine is up. Both are retried, as the page retries them.
			var id int64
			awaitServer := func(what string) {
				t.Helper()
				deadline := time.Now().Add(3 * time.Minute)
				for {
					rec := do(t, router, http.MethodPost, "/databases/adopt", `{"container":"`+name+`"}`)
					if rec.Code == http.StatusOK || rec.Code == http.StatusCreated {
						id = readJSON[summaryView](t, rec).ID
						ping := readJSON[struct {
							OK bool `json:"ok"`
						}](t, do(t, router, http.MethodGet, pathf("/databases/%d/ping", id), ""))
						if ping.OK {
							return
						}
					}
					if time.Now().After(deadline) {
						t.Fatalf("%s: %s did not answer in time: %d %s", what, name, rec.Code, rec.Body.String())
					}
					time.Sleep(2 * time.Second)
				}
			}
			awaitServer("after provisioning")

			summary := func() liveSummary {
				t.Helper()
				rec := do(t, router, http.MethodGet, pathf("/databases/%d", id), "")
				if rec.Code != http.StatusOK {
					t.Fatalf("summary = %d %s", rec.Code, rec.Body.String())
				}
				return readJSON[liveSummary](t, rec)
			}
			got := summary()
			if got.State != dbStateRunning || got.Source != "docker" || got.Container == nil || got.Container.Name != name {
				t.Fatalf("summary of a provisioned server = %+v", got)
			}
			if got.Flavor != tmpl.flavor || got.VersionNumber == "" {
				t.Errorf("the server from the %s template answered as %q %q", engine, got.Flavor, got.VersionNumber)
			}
			if got.Exposure != "local" || got.Power.Via != "docker" || !got.Power.Stop || got.Power.Start {
				t.Errorf("exposure %q, power %+v", got.Exposure, got.Power)
			}

			if rec := do(t, router, http.MethodPost, pathf("/databases/%d/power", id), `{"action":"stop"}`); rec.Code != http.StatusOK {
				t.Fatalf("stop = %d %s", rec.Code, rec.Body.String())
			}
			got = summary()
			if got.State != dbStateStopped || got.Container == nil || got.Container.State == "running" ||
				!got.Power.Start || got.Power.Stop {
				t.Errorf("after stop: %+v", got)
			}
			// What it said it was survives its being stopped.
			if got.Flavor != tmpl.flavor {
				t.Errorf("a stopped server lost its flavour: %q", got.Flavor)
			}
			fleet := readJSON[dbFleetView](t, do(t, router, http.MethodGet, "/databases/fleet", ""))
			if len(fleet.Connections) != 1 || fleet.Connections[0].State != dbStateStopped || fleet.Connections[0].OK {
				t.Errorf("fleet after stop = %+v", fleet.Connections)
			}

			// Stopped, so there is nothing to restart: the route holds the line
			// the summary drew, and Docker is not asked.
			if rec := do(t, router, http.MethodPost, pathf("/databases/%d/power", id), `{"action":"restart"}`); rec.Code != http.StatusConflict ||
				!strings.Contains(rec.Body.String(), "power_unavailable") {
				t.Errorf("restart of a stopped server = %d %s", rec.Code, rec.Body.String())
			}

			if rec := do(t, router, http.MethodPost, pathf("/databases/%d/power", id), `{"action":"start"}`); rec.Code != http.StatusOK {
				t.Fatalf("start = %d %s", rec.Code, rec.Body.String())
			}
			awaitServer("after start")
			if got = summary(); got.State != dbStateRunning {
				t.Errorf("after start: %+v", got)
			}

			// A restart with a grace of its own: the engine is given the time
			// to shut down, comes back, and still holds the password it had.
			rec = do(t, router, http.MethodPost, pathf("/databases/%d/power", id), `{"action":"restart","timeoutSeconds":30}`)
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"state":"running"`) {
				t.Fatalf("restart = %d %s", rec.Code, rec.Body.String())
			}
			awaitServer("after restart")
		})
	}
}

// A capability flag is a promise that the section it gates has something to
// show. Each flag that has a read route of its own is held to it here, on
// every engine that answers: a flag that is true for an engine whose route
// then fails is a tab drawn over an error.
//
// One flag is one feature and not one address: the sessions of a SQL server,
// the clients of a Redis and the operations of a MongoDB are the same section
// of three pages, served by three routes.
func TestLiveCapabilityFlagsAnswerOnRealServers(t *testing.T) {
	everywhere := func(route string) map[string]string {
		return map[string]string{"sql": route, "redis": route, "mongodb": route}
	}
	routes := map[string]map[string]string{
		"roles":           everywhere("/server/roles"),
		"extensions":      everywhere("/server/extensions"),
		"settings":        everywhere("/settings"),
		"stats":           everywhere("/stats"),
		"queryLog":        everywhere("/querylog"),
		"sessions":        {"sql": "/activity", "redis": "/redis/clients", "mongodb": "/mongo/ops"},
		"replication":     {"sql": "/replication", "redis": "/redis/server", "mongodb": "/mongo/replication"},
		"statements":      {"sql": "/statements"},
		"advisor":         {"sql": "/advisor"},
		"locks":           {"sql": "/locks"},
		"tableStats":      {"sql": "/tablestats"},
		"indexStats":      {"sql": "/indexstats"},
		"maintenance":     {"sql": "/maintenance"},
		"privileges":      {"sql": "/server/privileges"},
		"catalog":         {"sql": "/catalog"},
		"clickhouseViews": {"sql": "/clickhouse/parts"},
		"keys":            {"redis": "/keys"},
		"keyTree":         {"redis": "/keys/tree"},
		"serverInfo":      {"redis": "/redis/server"},
		"commandStats":    {"redis": "/redis/commandstats"},
		"latency":         {"redis": "/redis/latency"},
		"memoryAnalysis":  {"redis": "/redis/analysis"},
		"aclRules":        {"redis": "/redis/acl"},
		"pubsub":          {"redis": "/redis/pubsub"},
		"collections":     {"mongodb": "/mongo/collections"},
		"profiler":        {"mongodb": "/mongo/profiler"},
	}
	for _, c := range []struct {
		driver dbx.Driver
		env    string
	}{
		{dbx.DriverPostgres, "JD_TEST_POSTGRES_DSN"},
		// The administrative accounts: listing the server's users is the
		// server's to allow, and an application account is rightly refused.
		{dbx.DriverMySQL, "JD_TEST_MYSQL_ADMIN_DSN"},
		{dbx.DriverMySQL, "JD_TEST_MYSQL8_ADMIN_DSN"},
		{dbx.DriverMSSQL, "JD_TEST_MSSQL_DSN"},
		{dbx.DriverRedis, "JD_TEST_REDIS_DSN"},
		{dbx.DriverRedis, "JD_TEST_VALKEY_DSN"},
		{dbx.DriverRedis, "JD_TEST_KEYDB_DSN"},
		{dbx.DriverRedis, "JD_TEST_DRAGONFLY_DSN"},
		{dbx.DriverMongo, "JD_TEST_MONGO_DSN"},
		{dbx.DriverClickHouse, "JD_TEST_CLICKHOUSE_DSN"},
	} {
		t.Run(c.env, func(t *testing.T) {
			dsn := os.Getenv(c.env)
			if dsn == "" {
				t.Skipf("%s unset", c.env)
			}
			_, router, id := liveAPIRouter(t, c.driver, dsn)
			summary := readJSON[liveSummary](t, do(t, router, http.MethodGet, pathf("/databases/%d", id), ""))
			family := string(c.driver)
			if c.driver.IsSQL() {
				family = "sql"
			}
			for flag, byFamily := range routes {
				route := byFamily[family]
				if route == "" || summary.Capabilities[flag] != true {
					continue
				}
				rec := do(t, router, http.MethodGet, pathf("/databases/%d", id)+route, "")
				if rec.Code != http.StatusOK {
					t.Errorf("%s (%s) is said to have %q and %s answers %d %s",
						c.driver, summary.Flavor, flag, route, rec.Code, strings.TrimSpace(rec.Body.String()))
				}
			}
		})
	}
}

// A protected connection against real servers: the statements and the
// pipeline that got past the check by not looking like a write are refused,
// and what they would have made is not there afterwards.
func TestLiveProtectedConnectionIsNotWrittenTo(t *testing.T) {
	protect := func(t *testing.T, s *Server, id int64) {
		t.Helper()
		if _, err := s.Store.DB.Exec(`UPDATE db_connections SET read_only = 1 WHERE id = ?`, id); err != nil {
			t.Fatal(err)
		}
	}
	refused := func(t *testing.T, router http.Handler, path, body string) {
		t.Helper()
		rec := do(t, router, http.MethodPost, path, body)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "connection_read_only") {
			t.Errorf("POST %s %s = %d %s, want 409 connection_read_only", path, body, rec.Code, rec.Body.String())
		}
	}

	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("JD_TEST_POSTGRES_DSN")
		if dsn == "" {
			t.Skip("JD_TEST_POSTGRES_DSN unset")
		}
		s, router, id := liveAPIRouter(t, dbx.DriverPostgres, dsn)
		protect(t, s, id)
		path := pathf("/databases/%d/query", id)
		refused(t, router, path, `{"query":"SELECT 1 AS a INTO jd_b1b_protected_into"}`)
		refused(t, router, path, `{"query":"CREATE TABLE jd_b1b_protected_into (a int)"}`)
		refused(t, router, path, `{"query":"WITH s AS (SELECT 1 AS a) MERGE INTO jd_b1b_protected_into t USING s ON t.a = s.a WHEN NOT MATCHED THEN INSERT (a) VALUES (s.a)"}`)
		rec := do(t, router, http.MethodPost, path,
			`{"query":"SELECT count(*) AS made FROM information_schema.tables WHERE table_name = 'jd_b1b_protected_into'"}`)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"made"`) {
			t.Fatalf("a read on a protected connection = %d %s", rec.Code, rec.Body.String())
		}
		got := readJSON[struct {
			Result struct {
				Rows [][]any `json:"rows"`
			} `json:"result"`
		}](t, rec)
		if len(got.Result.Rows) != 1 || len(got.Result.Rows[0]) != 1 || fmt.Sprint(got.Result.Rows[0][0]) != "0" {
			t.Errorf("a table was made on a protected connection: %v", got.Result.Rows)
		}
	})

	t.Run("mysql", func(t *testing.T) {
		dsn := os.Getenv("JD_TEST_MYSQL_DSN")
		if dsn == "" {
			t.Skip("JD_TEST_MYSQL_DSN unset")
		}
		s, router, id := liveAPIRouter(t, dbx.DriverMySQL, dsn)
		protect(t, s, id)
		path := pathf("/databases/%d/query", id)
		refused(t, router, path, `{"query":"SELECT 1 INTO OUTFILE '/tmp/jd_b1b_protected.csv'"}`)
		if rec := do(t, router, http.MethodPost, path, `{"query":"SELECT 1 AS answers"}`); rec.Code != http.StatusOK {
			t.Errorf("a read on a protected connection = %d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("mongodb", func(t *testing.T) {
		dsn := os.Getenv("JD_TEST_MONGO_DSN")
		if dsn == "" {
			t.Skip("JD_TEST_MONGO_DSN unset")
		}
		s, router, id := liveAPIRouter(t, dbx.DriverMongo, dsn)
		info, err := dbx.ParseDSN(dbx.DriverMongo, dsn)
		if err != nil || info.Database == "" {
			t.Skipf("the fixture DSN names no database: %v", err)
		}
		ctx := context.Background()
		client, err := dbx.MongoClient(ctx, dsn)
		if err != nil {
			t.Skip(err)
		}
		defer client.Disconnect(ctx)
		const source, copied = "jd_b1b_protect_src", "jd_b1b_protect_out"
		drop := func() {
			_ = dbx.MongoDropCollection(ctx, client, info.Database, source)
			_ = dbx.MongoDropCollection(ctx, client, info.Database, copied)
		}
		drop()
		defer drop()
		if rec := do(t, router, http.MethodPost, pathf("/databases/%d/documents", id),
			`{"collection":"`+source+`","document":"{\"a\":1}"}`); rec.Code != http.StatusOK {
			t.Fatalf("seeding the source collection = %d %s", rec.Code, rec.Body.String())
		}
		protect(t, s, id)
		path := pathf("/databases/%d/aggregate", id)
		for _, body := range []string{
			`{"collection":"` + source + `","pipeline":"[{\"$out\":\"` + copied + `\"}]"}`,
			`{"collection":"` + source + `","pipeline":"[]","Pipeline":"[{\"$out\":\"` + copied + `\"}]"}`,
			`{"collection":"` + source + `","PIPELINE":"[{\"$merge\":{\"into\":\"` + copied + `\"}}]"}`,
		} {
			refused(t, router, path, body)
		}
		rec := do(t, router, http.MethodPost, path, `{"collection":"`+source+`","pipeline":"[{\"$match\":{\"a\":1}}]"}`)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"writes":false`) {
			t.Errorf("a reading pipeline on a protected connection = %d %s", rec.Code, rec.Body.String())
		}
		collections, err := dbx.MongoCollections(ctx, client, info.Database)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range collections {
			if c.Name == copied {
				t.Errorf("a pipeline wrote %s on a protected connection", copied)
			}
		}
	})
}

// A server that is there and refuses the password is not one to ask again:
// the next attempt is refused the same way, and a page that offered "Try
// again" for it would be offering nothing. Each engine says no in its own
// words, and each has to be read as a no and not as a server that is away.
func TestLiveAWrongPasswordIsNotWorthAskingAgain(t *testing.T) {
	const wrong = "not-the-password"
	inURL := func(dsn string) string {
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		u.User = url.UserPassword(u.User.Username(), wrong)
		return u.String()
	}
	nobody := func(dsn string) string {
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		u.User = url.UserPassword("jd-nobody", wrong)
		return u.String()
	}
	for _, c := range []struct {
		driver dbx.Driver
		env    string
		refuse func(dsn string) string
		read   string
	}{
		{dbx.DriverPostgres, "JD_TEST_POSTGRES_DSN", inURL, "/tables"},
		{dbx.DriverMySQL, "JD_TEST_MYSQL_DSN", func(dsn string) string {
			user, rest, _ := strings.Cut(dsn, "@")
			name, _, _ := strings.Cut(user, ":")
			return name + ":" + wrong + "@" + rest
		}, "/tables"},
		{dbx.DriverMSSQL, "JD_TEST_MSSQL_DSN", inURL, "/tables"},
		{dbx.DriverClickHouse, "JD_TEST_CLICKHOUSE_DSN", inURL, "/tables"},
		// The two fixtures that ask for no password: an account that is not
		// there is what each refuses. A Redis whose default user has none
		// takes any password given for it.
		{dbx.DriverRedis, "JD_TEST_REDIS_DSN", nobody, "/keys"},
		{dbx.DriverMongo, "JD_TEST_MONGO_DSN", nobody, "/schemas"},
	} {
		t.Run(string(c.driver), func(t *testing.T) {
			dsn := os.Getenv(c.env)
			if dsn == "" {
				t.Skipf("%s unset", c.env)
			}
			// Reached first with the password it has, so that a refusal is
			// the password's and not a server that is down.
			liveAPIRouter(t, c.driver, dsn)
			h := newConnHarness(t)
			id := h.add("refused", c.driver, c.refuse(dsn))
			rec := do(t, h.as(auth.RoleReadOnly), http.MethodGet, pathf("/databases/%d", id)+c.read, "")
			got := readJSON[errorView](t, rec)
			if rec.Code != http.StatusBadGateway || got.Error.Code != "connect_failed" {
				t.Fatalf("a wrong password = %d %s, want 502 connect_failed", rec.Code, rec.Body.String())
			}
			if got.Error.Retryable {
				t.Errorf("a wrong password is marked worth asking again: %s", got.Error.Message)
			}
			if strings.Contains(rec.Body.String(), wrong) {
				t.Errorf("the refusal repeats the password: %s", rec.Body.String())
			}
		})
	}
}

// relay stands between the dashboard and a real server, so that a test can
// take the server away without stopping one other tests share: it forwards
// every connection until cut, and then refuses them and drops the ones it has.
func relay(t *testing.T, backend string) (addr string, cut func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var (
		mu    sync.Mutex
		conns []net.Conn
		gone  bool
	)
	go func() {
		for {
			client, err := ln.Accept()
			if err != nil {
				return
			}
			server, err := net.Dial("tcp", backend)
			if err != nil {
				client.Close()
				continue
			}
			mu.Lock()
			if gone {
				mu.Unlock()
				client.Close()
				server.Close()
				continue
			}
			conns = append(conns, client, server)
			mu.Unlock()
			go func() { _, _ = io.Copy(server, client); server.Close() }()
			go func() { _, _ = io.Copy(client, server); client.Close() }()
		}
	}()
	cut = func() {
		mu.Lock()
		defer mu.Unlock()
		gone = true
		ln.Close()
		for _, c := range conns {
			c.Close()
		}
	}
	t.Cleanup(cut)
	return ln.Addr().String(), cut
}

// A page that is open when its server goes away — stopped, restarted, the
// network gone — asks again on its next poll and is answered by a pool whose
// connections are dead. That is a read whose server is not there, and it is
// marked worth asking again whichever of the two codes it arrives under: the
// one for a server that could not be opened, or the one for a read it did not
// answer.
func TestLiveAReadWhoseServerWentAwayIsWorthAskingAgain(t *testing.T) {
	throughURL := func(dsn, addr string) (string, string) {
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		backend := u.Host
		u.Host = addr
		return u.String(), backend
	}
	for _, c := range []struct {
		driver  dbx.Driver
		env     string
		through func(dsn, addr string) (string, string)
		read    string
	}{
		{dbx.DriverPostgres, "JD_TEST_POSTGRES_DSN", throughURL, "/tables"},
		// A page of rows answers a request that was wrong with a 400. This is
		// not one, and must not be taken for one.
		{dbx.DriverPostgres, "JD_TEST_POSTGRES_DSN", throughURL, "/browse?schema=pg_catalog&table=pg_namespace"},
		{dbx.DriverPostgres, "JD_TEST_POSTGRES_DSN", throughURL, "/count?schema=pg_catalog&table=pg_namespace"},
		{dbx.DriverMySQL, "JD_TEST_MYSQL_DSN", func(dsn, addr string) (string, string) {
			before, rest, _ := strings.Cut(dsn, "tcp(")
			backend, after, _ := strings.Cut(rest, ")")
			return before + "tcp(" + addr + ")" + after, backend
		}, "/tables"},
		{dbx.DriverMSSQL, "JD_TEST_MSSQL_DSN", throughURL, "/tables"},
		{dbx.DriverClickHouse, "JD_TEST_CLICKHOUSE_DSN", throughURL, "/tables"},
		{dbx.DriverRedis, "JD_TEST_REDIS_DSN", throughURL, "/keys"},
		{dbx.DriverMongo, "JD_TEST_MONGO_DSN", throughURL, "/schemas"},
	} {
		t.Run(string(c.driver)+c.read, func(t *testing.T) {
			dsn := os.Getenv(c.env)
			if dsn == "" {
				t.Skipf("%s unset", c.env)
			}
			// The server's own address is read out of the connection string
			// before the relay's is written into it.
			_, backend := c.through(dsn, "127.0.0.1:1")
			addr, cut := relay(t, backend)
			relayed, _ := c.through(dsn, addr)
			_, router, id := liveAPIRouter(t, c.driver, relayed)
			path := pathf("/databases/%d", id) + c.read
			if rec := do(t, router, http.MethodGet, path, ""); rec.Code != http.StatusOK {
				t.Fatalf("a read while the server is there = %d %s", rec.Code, rec.Body.String())
			}
			cut()
			rec := do(t, router, http.MethodGet, path, "")
			got := readJSON[errorView](t, rec)
			if rec.Code != http.StatusBadGateway || (got.Error.Code != "connect_failed" && got.Error.Code != "query_failed") {
				t.Fatalf("a read after the server went away = %d %s", rec.Code, rec.Body.String())
			}
			if !got.Error.Retryable {
				t.Errorf("a read whose server went away is not marked worth asking again: %s %s", got.Error.Code, got.Error.Message)
			}
		})
	}
}

// The connection's reading of a container the machine's own Docker runs: what
// it may use, what the power route would do to it, and a change of power from
// the moment it is asked for until the engine has caught up — on the summary
// and on the fleet entry, which have to agree.
//
// JD_TEST_POWER_REDIS_DSN names a Redis in a container this test may stop and
// start: one of its own, published on loopback, from an image detection knows
// as Redis. It has no default, because stopping a server is not something to
// do to one somebody else is using.
func TestLivePowerIsReadInFlightOnARealContainer(t *testing.T) {
	dsn := os.Getenv("JD_TEST_POWER_REDIS_DSN")
	if dsn == "" {
		t.Skip("set JD_TEST_POWER_REDIS_DSN to a Redis in a container this test may stop and start")
	}
	h := &connHarness{t: t, s: testServer(t)}
	h.s.Cfg.BackupLocalDir = t.TempDir()
	docker := dockerx.New(envOr("JD_TEST_DOCKER_HOST", "unix:///var/run/docker.sock"))
	t.Cleanup(func() { _ = docker.Close() })
	h.s.modules.docker = docker
	admin, reader := h.as(auth.RoleAdmin), h.as(auth.RoleReadOnly)
	id := h.add("power", dbx.DriverRedis, dsn)
	path := pathf("/databases/%d", id)

	// Read as another browser would: by a role that may only look.
	read := func() (summaryView, dbFleetView) {
		t.Helper()
		summary := readJSON[summaryView](t, do(t, reader, http.MethodGet, path, ""))
		fleet := readJSON[dbFleetView](t, do(t, reader, http.MethodGet, "/databases/fleet", ""))
		if len(fleet.Connections) != 1 {
			t.Fatalf("fleet = %+v", fleet.Connections)
		}
		return summary, fleet
	}
	summary, fleet := read()
	if summary.Source != "docker" || summary.Container == nil || summary.State != dbStateRunning {
		t.Skipf("%s is not a running container this dashboard can see: %+v", dsn, summary)
	}
	name := summary.Container.Name
	if !strings.HasPrefix(name, "jdcc-") {
		t.Skipf("%s is not one of this run's containers; it is left alone", name)
	}
	t.Cleanup(func() {
		// Left running, as it was found.
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_ = docker.Lifecycle(ctx, name, dockerx.ActionStart, nil)
	})

	// What the daemon says the container may use is what the summary says,
	// running or not.
	limits := func(when string, got summaryView) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		detail, err := docker.Inspect(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		c := got.Container
		if c == nil || c.MemoryLimit != detail.MemoryLimit || c.CPULimit != detail.CPULimit || c.RestartPolicy != detail.RestartPol {
			t.Errorf("%s: the summary says %+v, Docker says memory %d, %v processors, restart %q",
				when, c, detail.MemoryLimit, detail.CPULimit, detail.RestartPol)
		}
		t.Logf("%s: memory %d, %v processors, restart %q", when, detail.MemoryLimit, detail.CPULimit, detail.RestartPol)
	}
	limits("running", summary)
	agree := func(when string, summary summaryView, fleet dbFleetView) {
		t.Helper()
		entry := fleet.Connections[0]
		if entry.Power != summary.Power || entry.Managed != summary.Managed || entry.State != summary.State {
			t.Errorf("%s: the fleet says %s, power %+v, managed=%v; the summary %s, power %+v, managed=%v",
				when, entry.State, entry.Power, entry.Managed, summary.State, summary.Power, summary.Managed)
		}
		if (entry.InFlight == nil) != (summary.InFlight == nil) {
			t.Errorf("%s: in flight on the fleet = %+v, on the summary = %+v", when, entry.InFlight, summary.InFlight)
		}
	}
	agree("running", summary, fleet)
	if summary.Power.Via != "docker" || !summary.Power.Stop || !summary.Power.Restart || summary.Power.Start || summary.InFlight != nil {
		t.Fatalf("a running container: power %+v, in flight %+v", summary.Power, summary.InFlight)
	}

	power := func(action string) {
		t.Helper()
		if rec := do(t, admin, http.MethodPost, path+"/power", `{"action":"`+action+`","timeoutSeconds":20}`); rec.Code != http.StatusOK {
			t.Fatalf("%s = %d %s", action, rec.Code, rec.Body.String())
		}
	}
	// Until the server reads the state the action leaves it in, every reading
	// carries the change; from then on none does.
	settle := func(action, state string) {
		t.Helper()
		deadline := time.Now().Add(dbPowerSettle)
		for {
			summary, fleet := read()
			agree("after "+action, summary, fleet)
			if summary.State == state {
				if summary.InFlight != nil {
					t.Errorf("%s: the server reads %s and the change is still in flight: %+v", action, state, summary.InFlight)
				}
				return
			}
			if summary.InFlight == nil || summary.InFlight.Action != action {
				t.Errorf("%s: the server reads %s, not yet %s, and the change in flight is %+v", action, summary.State, state, summary.InFlight)
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: the server never read %s: %+v", action, state, summary)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	power("stop")
	settle("stop", dbStateStopped)
	summary, fleet = read()
	agree("stopped", summary, fleet)
	if !summary.Power.Start || summary.Power.Stop || summary.Power.Restart {
		t.Errorf("a stopped container: power %+v", summary.Power)
	}
	limits("stopped", summary)

	power("start")
	settle("start", dbStateRunning)
	power("restart")
	settle("restart", dbStateRunning)
	summary, fleet = read()
	agree("after the restart", summary, fleet)
	if !summary.Power.Stop || summary.InFlight != nil || fleet.Connections[0].InFlight != nil {
		t.Errorf("after the restart: power %+v, in flight %+v", summary.Power, summary.InFlight)
	}
}
