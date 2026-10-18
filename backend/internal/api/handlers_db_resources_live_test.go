package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// TestLiveProvisionedLimitsAreAppliedInPlaceAndSurviveAReplacement starts a
// server from each template JD_TEST_PROVISION_ENGINES names with a memory and
// a processor limit, then holds the Resources routes to what Docker does: a
// raised limit is applied without a restart, the dashboard's own replacement
// of the container (the path a change of exposure and the Docker page's
// Recreate take) keeps it, and lifting the processor limit replaces the
// container with no limit and the same data. The memory limit is never lifted,
// so no server here may use more of the machine than 512 MiB. It
// creates containers and volumes named jdt-resource-limits-<engine> and
// removes them again.
func TestLiveProvisionedLimitsAreAppliedInPlaceAndSurviveAReplacement(t *testing.T) {
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
			docker := dockerx.New(envOr("JD_TEST_DOCKER_HOST", "unix:///var/run/docker.sock"))
			t.Cleanup(func() { _ = docker.Close() })
			h.s.modules.docker = docker
			router := h.as("admin")
			name := "jdt-resource-limits-" + engine
			remove := func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				_ = docker.RemoveContainer(ctx, name, true, true)
				_ = docker.RemoveVolume(ctx, name+"-data", true)
			}
			remove()
			t.Cleanup(remove)

			// MySQL's first start needs 512 MiB; the others are started under
			// less so that the raise below changes the memory limit too.
			memory := max(int64(448), provisionMinMemoryMB(tmpl))
			rec := do(t, router, http.MethodPost, "/databases/provision", fmt.Sprintf(
				`{"engine":"%s","name":"%s","version":"%s","memoryMb":%d,"cpus":0.5}`, engine, name, version, memory))
			if rec.Code != http.StatusAccepted {
				t.Fatalf("provision = %d %s", rec.Code, rec.Body.String())
			}
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
			inspect := func() *dockerx.ContainerDetail {
				t.Helper()
				detail, err := docker.Inspect(context.Background(), name)
				if err != nil {
					t.Fatal(err)
				}
				return detail
			}
			if d := inspect(); d.MemoryLimit != memory*mib || d.CPULimit != 0.5 {
				t.Fatalf("provisioned with %d bytes and %g processors", d.MemoryLimit, d.CPULimit)
			}

			got := readJSON[resourcesView](t, do(t, router, http.MethodGet, pathf("/databases/%d/resources", id), ""))
			if !got.Editable || !got.Running || got.Limits.MemoryBytes != memory*mib || got.Limits.CPUs != 0.5 || got.UsedBytes <= 0 {
				t.Errorf("the reading of a provisioned server = %+v", got)
			}
			if want := dbx.MemoryBufferSetting(tmpl.driver, tmpl.flavor); got.Buffer == nil || got.Buffer.Setting != want ||
				(tmpl.driver != dbx.DriverRedis && got.Buffer.Bytes <= 0) {
				t.Errorf("the engine's buffer = %+v (%s), want %s", got.Buffer, got.BufferError, want)
			}

			// Raised in place: the same process, under the new limits.
			before := inspect()
			rec = do(t, router, http.MethodPut, pathf("/databases/%d/resources", id), `{"memoryMb":512,"cpus":1}`)
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"replaced":false`) {
				t.Fatalf("raise = %d %s", rec.Code, rec.Body.String())
			}
			after := inspect()
			if after.ID != before.ID || *after.StartedAt != *before.StartedAt || after.MemoryLimit != 512*mib || after.CPULimit != 1 {
				t.Errorf("after the raise: %s started %v with %d bytes and %g processors; before: %s started %v",
					after.ID, after.StartedAt, after.MemoryLimit, after.CPULimit, before.ID, before.StartedAt)
			}

			// The dashboard's own replacement reads the configuration back.
			if _, err := docker.Recreate(context.Background(), name, dockerx.RecreateOptions{}, nil); err != nil {
				t.Fatalf("recreate: %v", err)
			}
			if d := inspect(); d.ID == after.ID || d.MemoryLimit != 512*mib || d.CPULimit != 1 {
				t.Errorf("after a recreate: %s with %d bytes and %g processors", d.ID, d.MemoryLimit, d.CPULimit)
			}
			awaitServer("after a recreate")

			// Lifted: a new container, the memory limit kept, the data too.
			replaced := inspect()
			rec = do(t, router, http.MethodPut, pathf("/databases/%d/resources", id), `{"cpus":0}`)
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"replaced":true`) {
				t.Fatalf("lift = %d %s", rec.Code, rec.Body.String())
			}
			if d := inspect(); d.ID == replaced.ID || d.MemoryLimit != 512*mib || d.CPULimit != 0 || d.State != "running" {
				t.Errorf("after the lift: %s %s with %d bytes and %g processors", d.ID, d.State, d.MemoryLimit, d.CPULimit)
			}
			awaitServer("after the lift")
			got = readJSON[resourcesView](t, do(t, router, http.MethodGet, pathf("/databases/%d/resources", id), ""))
			if got.Limits.MemoryBytes != 512*mib || got.Limits.CPUs != 0 {
				t.Errorf("the reading after the lift = %+v", got.Limits)
			}
		})
	}
}

// Against the fixtures, the reading carries the engine's own buffer under the
// name the capability table gives it.
func TestLiveResourcesReadTheEnginesBuffer(t *testing.T) {
	for _, c := range []struct {
		driver dbx.Driver
		env    string
	}{
		{dbx.DriverPostgres, "JD_TEST_POSTGRES_DSN"},
		{dbx.DriverMySQL, "JD_TEST_MYSQL_DSN"},
		{dbx.DriverMySQL, "JD_TEST_MYSQL8_DSN"},
		{dbx.DriverRedis, "JD_TEST_REDIS_DSN"},
		{dbx.DriverRedis, "JD_TEST_VALKEY_DSN"},
		{dbx.DriverRedis, "JD_TEST_KEYDB_DSN"},
		{dbx.DriverRedis, "JD_TEST_DRAGONFLY_DSN"},
		{dbx.DriverMongo, "JD_TEST_MONGO_DSN"},
		{dbx.DriverClickHouse, "JD_TEST_CLICKHOUSE_DSN"},
		{dbx.DriverMSSQL, "JD_TEST_MSSQL_DSN"},
	} {
		t.Run(c.env, func(t *testing.T) {
			dsn := os.Getenv(c.env)
			if dsn == "" {
				t.Skipf("%s unset", c.env)
			}
			_, router, id := liveAPIRouter(t, c.driver, dsn)
			summary := readJSON[liveSummary](t, do(t, router, http.MethodGet, pathf("/databases/%d", id), ""))
			got := readJSON[resourcesView](t, do(t, router, http.MethodGet, pathf("/databases/%d/resources", id), ""))
			word, has := summary.Capabilities["memoryBuffer"].(string)
			switch {
			case !has && (got.Buffer != nil || got.BufferError != ""):
				t.Errorf("%s (%s) keeps no buffer and the reading says %+v %q", c.driver, summary.Flavor, got.Buffer, got.BufferError)
			case has && (got.Buffer == nil || got.Buffer.Setting != word):
				t.Errorf("%s (%s) keeps %s and the reading says %+v %q", c.driver, summary.Flavor, word, got.Buffer, got.BufferError)
			case has && c.driver != dbx.DriverRedis && got.Buffer.Bytes <= 0:
				t.Errorf("%s (%s): %s is %d", c.driver, summary.Flavor, word, got.Buffer.Bytes)
			}
		})
	}
}
