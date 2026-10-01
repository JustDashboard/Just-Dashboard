package api

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

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

			if rec := do(t, router, http.MethodPost, pathf("/databases/%d/power", id), `{"action":"start"}`); rec.Code != http.StatusOK {
				t.Fatalf("start = %d %s", rec.Code, rec.Body.String())
			}
			awaitServer("after start")
			if got = summary(); got.State != dbStateRunning {
				t.Errorf("after start: %+v", got)
			}
		})
	}
}

// A capability flag is a promise that the section it gates has something to
// show. Each flag that has a read route of its own is held to it here, on
// every engine that answers: a flag that is true for an engine whose route
// then fails is a tab drawn over an error.
func TestLiveCapabilityFlagsAnswerOnRealServers(t *testing.T) {
	routes := map[string]string{
		"roles":      "/server/roles",
		"extensions": "/server/extensions",
		"settings":   "/server/settings",
		"sessions":   "/activity",
		"statements": "/statements",
		"queryLog":   "/querylog",
		"advisor":    "/advisor",
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
			for flag, route := range routes {
				rec := do(t, router, http.MethodGet, pathf("/databases/%d", id)+route, "")
				if summary.Capabilities[flag] == true && rec.Code != http.StatusOK {
					t.Errorf("%s (%s) is said to have %q and %s answers %d %s",
						c.driver, summary.Flavor, flag, route, rec.Code, strings.TrimSpace(rec.Body.String()))
				}
			}
		})
	}
}
