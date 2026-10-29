package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/docker/docker/api/types/network"
	dockerclient "github.com/docker/docker/client"
)

// Exercises the whole create/adopt/connect path and proves that another
// application container can authenticate using the URL returned by the API.
func TestLiveDeploymentDatabaseConnection(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 on a Docker host")
	}
	for _, engine := range []string{"postgres", "mysql", "mariadb", "redis", "mongodb"} {
		t.Run(engine, func(t *testing.T) {
			s, router := dbTestRouter(t)
			s.modules.docker = dockerx.New("unix:///var/run/docker.sock")
			// Before anything is created, so a registry failure leaves nothing to clean up.
			pullEngineImage(t, s.modules.docker, engine)
			projectID, environmentID, _ := insertDeploymentConfigurationAPI(t, s)
			name := fmt.Sprintf("jd-redesign-%s-%d", engine, time.Now().UnixNano())
			probe := name + "-probe"
			blocker := name + "-ip-holder"
			// Each container goes with its anonymous volumes (the MongoDB image
			// declares two); a named one, the database's data, is removed apart.
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				_ = s.modules.docker.RemoveContainer(ctx, probe, true, true)
				_ = s.modules.docker.RemoveContainer(ctx, blocker, true, true)
				if err := s.modules.deployDatabases.RemoveRuntimeNetworks(ctx, environmentID); err != nil {
					t.Errorf("network cleanup: %v", err)
				}
				_ = s.modules.docker.RemoveContainer(ctx, name, true, true)
				if err := s.modules.docker.RemoveVolume(ctx, name+"-data", false); err != nil {
					t.Errorf("test volume cleanup: %v", err)
				}
			})
			body, _ := json.Marshal(map[string]string{"engine": engine, "name": name, "database": "app", "exposure": "local"})
			created := do(t, router, http.MethodPost, "/databases/provision", string(body))
			if created.Code != http.StatusAccepted {
				t.Fatalf("provision failed: HTTP %d: %s", created.Code, created.Body)
			}
			if engine == "mongodb" {
				assertMongoKernelMitigation(t, s, created.Body.Bytes(), name)
			}
			// The container is created and started by now, with its image already local, so
			// this budget is the engine's own first start and nothing else: seconds for
			// every engine here, MongoDB's two servers included.
			const budget = 2 * time.Minute
			waitStarted := time.Now()
			deadline := waitStarted.Add(budget)
			var conn dbConnection
			var problem string
			for {
				adopted := do(t, router, http.MethodPost, "/databases/adopt", fmt.Sprintf(`{"container":%q}`, name))
				if adopted.Code >= 200 && adopted.Code < 300 {
					if err := json.Unmarshal(adopted.Body.Bytes(), &conn); err != nil {
						t.Fatal(err)
					}
					ping := do(t, router, http.MethodGet, pathf("/databases/%d/ping", conn.ID), "")
					var health struct {
						OK bool `json:"ok"`
					}
					_ = json.Unmarshal(ping.Body.Bytes(), &health)
					if health.OK {
						break
					}
					problem = fmt.Sprintf("adopted as connection %d, but its ping answered HTTP %d: %s", conn.ID, ping.Code, clip(ping.Body.String(), 400))
				} else {
					problem = fmt.Sprintf("the last adopt answered HTTP %d: %s", adopted.Code, clip(adopted.Body.String(), 400))
					if engine == "mongodb" {
						// Provisioning gave the server what it needs to start on
						// this kernel; if it still refuses, that stopped working,
						// and waiting out the budget would only fail later with the
						// same words in its log. It fails rather than skips: a
						// required live fixture that does not run is a failure of
						// the live job by design.
						tail, _ := fixtureLogTail(s.modules.docker, name, 5)
						if refusal := mongoKernelRefusal(tail); refusal != "" {
							t.Fatalf("mongodb: MongoDB refused to start on this host's kernel (Linux %s, MongoDB %s) with the environment provisioning gave it (%s), so the mitigation in database_mongo_kernel.go no longer lets it start: %s",
								s.hostKernel(t.Context()), deploy.MongoKernelTicket, fixtureTunables(s.modules.docker, name), clip(refusal, 300))
						}
					}
				}
				if time.Now().After(deadline) {
					t.Fatalf("%s did not accept connections within %s; %s\n%s", engine, budget, problem, describeFixtureContainer(s.modules.docker, name))
				}
				time.Sleep(time.Second)
			}
			t.Logf("%s: accepted connections %s after its container started (budget %s)", engine, time.Since(waitStarted).Round(time.Millisecond), budget)
			revealed := do(t, router, http.MethodGet, pathf("/databases/%d/url", conn.ID)+"?target=container", "")
			if revealed.Code != http.StatusOK {
				t.Fatalf("application URL unavailable: HTTP %d", revealed.Code)
			}
			var result struct {
				URL string `json:"url"`
			}
			if err := json.Unmarshal(revealed.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			application, err := url.Parse(result.URL)
			if err != nil {
				t.Fatal("invalid application URL")
			}
			if databaseLoopback(application.Hostname()) {
				t.Fatal("application URL still uses loopback")
			}
			if application.Hostname() != databaseDNSName(conn.ID) {
				t.Fatal("application URL has no stable logical identity")
			}
			info, err := dbx.ParseDSN(conn.Driver, result.URL)
			if err != nil {
				t.Fatal("application URL cannot be parsed")
			}
			networks, err := s.modules.deployDatabases.NetworksForRuntime(t.Context(), environmentID, deploy.RuntimePlanConfig{}, map[string]string{"DATABASE_URL": result.URL})
			if err != nil || len(networks) != 1 {
				t.Fatalf("application network unavailable: %v", err)
			}
			previewResult, err := s.Store.DB.Exec(`INSERT INTO deploy_environments(project_id,name,slug,kind,created_at,updated_at) VALUES(?,'Isolated preview','preview-db-refusal','preview',1,1)`, projectID)
			if err != nil {
				t.Fatal(err)
			}
			previewID, _ := previewResult.LastInsertId()
			if value, err := s.modules.deployDatabases.ResolveVariable(t.Context(), previewID, 1, strconv.FormatInt(conn.ID, 10)); !errors.Is(err, deploy.ErrPreviewIsolation) || value != "" {
				t.Fatalf("preview resolved production credentials before build: %v", err)
			}
			if _, err := s.modules.deployDatabases.NetworksForRuntime(t.Context(), previewID, deploy.RuntimePlanConfig{}, map[string]string{"DATABASE_URL": result.URL}); !errors.Is(err, deploy.ErrPreviewIsolation) {
				t.Fatalf("preview joined production database: %v", err)
			}
			var previewNetworks int
			if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_database_networks WHERE environment_id=?`, previewID).Scan(&previewNetworks); err != nil || previewNetworks != 0 {
				t.Fatalf("refused preview allocated database resources: %d %v", previewNetworks, err)
			}
			spec := dockerx.ContainerSpec{Name: probe, Image: provisionTemplates[engine].image, Start: true, Networks: networks}
			switch engine {
			case "postgres":
				spec.Env = []dockerx.EnvVar{{Name: "PGPASSWORD", Value: info.Password}}
				spec.Command = []string{"psql", "-h", info.Host, "-p", info.Port, "-U", info.User, "-d", info.Database, "-c", "SELECT 1"}
			case "mysql", "mariadb":
				command := "mysql"
				if engine == "mariadb" {
					command = "mariadb"
				}
				spec.Env = []dockerx.EnvVar{{Name: "MYSQL_PWD", Value: info.Password}}
				spec.Command = []string{command, "--host", info.Host, "--port", info.Port, "--user", info.User, "--database", info.Database, "--execute", "SELECT 1"}
			case "redis":
				spec.Env = []dockerx.EnvVar{{Name: "REDISCLI_AUTH", Value: info.Password}}
				spec.Command = []string{"redis-cli", "-h", info.Host, "-p", info.Port, "PING"}
			case "mongodb":
				spec.Env = []dockerx.EnvVar{{Name: "DATABASE_URL", Value: result.URL}}
				spec.Command = []string{"mongosh", "--quiet", "--nodb", "--eval", `const database = connect(process.env.DATABASE_URL); if (!database.runCommand({ping:1}).ok) quit(1)`}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			if _, err := s.modules.docker.Create(ctx, spec, nil); err != nil {
				t.Fatalf("client container did not start: %v", err)
			}
			for {
				observed, err := s.modules.docker.Inspect(ctx, probe)
				if err != nil {
					t.Fatal(err)
				}
				if observed.State == "exited" {
					if observed.ExitCode != 0 {
						t.Fatalf("application failed to authenticate: exit %d", observed.ExitCode)
					}
					break
				}
				if ctx.Err() != nil {
					t.Fatal("application connection timed out")
				}
				time.Sleep(500 * time.Millisecond)
			}
			before, err := s.modules.docker.Inspect(ctx, name)
			if err != nil {
				t.Fatal(err)
			}
			oldIP := ""
			for _, endpoint := range before.NetworkList {
				if endpoint.Name == networks[0] {
					oldIP = endpoint.IPAddress
				}
			}
			if oldIP == "" {
				t.Fatal("database was not joined to application network")
			}
			replacement, err := s.modules.docker.SpecOf(ctx, name)
			if err != nil {
				t.Fatal("could not capture replacement fixture")
			}
			if err := s.modules.docker.RemoveContainer(ctx, name, true, true); err != nil {
				t.Fatal(err)
			}
			if _, err := s.modules.docker.Create(ctx, dockerx.ContainerSpec{Name: blocker, Image: "caddy:2-alpine", Entrypoint: []string{"sleep"}, Command: []string{"300"}, Start: true, Pull: "missing"}, nil); err != nil {
				t.Fatal(err)
			}
			cli, err := dockerclient.NewClientWithOpts(dockerclient.WithHost("unix:///var/run/docker.sock"), dockerclient.WithAPIVersionNegotiation())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = cli.Close() })
			if err := cli.NetworkConnect(ctx, networks[0], blocker, &network.EndpointSettings{IPAMConfig: &network.EndpointIPAMConfig{IPv4Address: oldIP}}); err != nil {
				t.Fatal(err)
			}
			replacement.NetworkMode = ""
			replacement.Networks = []string{"bridge"}
			replacement.Start = true
			if _, err := s.modules.docker.Create(ctx, *replacement, nil); err != nil {
				t.Fatal("database replacement failed")
			}
			deadline = time.Now().Add(time.Minute)
			for {
				ping := do(t, router, http.MethodGet, pathf("/databases/%d/ping", conn.ID), "")
				var health struct {
					OK bool `json:"ok"`
				}
				_ = json.Unmarshal(ping.Body.Bytes(), &health)
				if health.OK {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("replacement database did not become healthy")
				}
				time.Sleep(500 * time.Millisecond)
			}
			if err := s.modules.deployDatabases.Reconcile(t.Context()); err != nil {
				t.Fatal(err)
			}
			after, err := s.modules.docker.Inspect(t.Context(), name)
			if err != nil {
				t.Fatal(err)
			}
			newIP := ""
			for _, endpoint := range after.NetworkList {
				if endpoint.Name == networks[0] {
					newIP = endpoint.IPAddress
				}
			}
			if newIP == "" || newIP == oldIP {
				t.Fatal("fixture did not change database IP")
			}
			if err := s.modules.docker.Lifecycle(t.Context(), probe, dockerx.ActionStart, nil); err != nil {
				t.Fatal(err)
			}
			deadline = time.Now().Add(30 * time.Second)
			for {
				observed, err := s.modules.docker.Inspect(t.Context(), probe)
				if err != nil {
					t.Fatal(err)
				}
				if observed.State == "exited" {
					if observed.ExitCode != 0 {
						t.Fatal("unchanged application could not reconnect after database replacement")
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("application reconnection timed out")
				}
				time.Sleep(250 * time.Millisecond)
			}
			again := do(t, router, http.MethodGet, pathf("/databases/%d/url", conn.ID)+"?target=container", "")
			var same struct {
				URL string `json:"url"`
			}
			if again.Code != http.StatusOK || json.Unmarshal(again.Body.Bytes(), &same) != nil || same.URL != result.URL {
				t.Fatal("logical application URL changed after replacement")
			}
			// A secret read must preserve the host-side saved connection and all
			// publication remains restricted to loopback.
			saved, _, err := s.dbConnRow(t.Context(), conn.ID)
			if err != nil || !databaseLoopback(saved.Host) {
				t.Fatal("host connection changed")
			}
			observed, err := s.modules.docker.Inspect(t.Context(), name)
			if err != nil {
				t.Fatal(err)
			}
			for _, port := range observed.Ports {
				if port.PublicPort != 0 && port.IP != "127.0.0.1" {
					t.Fatal("database was published outside loopback")
				}
			}
			if engine == "redis" {
				if err := s.modules.deployDatabases.RemoveRuntimeNetworks(t.Context(), environmentID); err == nil {
					t.Fatal("network removal disconnected an application")
				}
				if err := s.modules.docker.RemoveContainer(t.Context(), probe, true, false); err != nil {
					t.Fatal(err)
				}
				if err := s.modules.docker.RemoveContainer(t.Context(), blocker, true, false); err != nil {
					t.Fatal(err)
				}
				if err := s.modules.deployDatabases.RemoveRuntimeNetworks(t.Context(), environmentID); err != nil {
					t.Fatal(err)
				}
				stillRunning, err := s.modules.docker.Inspect(t.Context(), name)
				if err != nil || stillRunning.State != "running" {
					t.Fatal("network cleanup stopped the linked database")
				}
				if _, err := s.modules.docker.CreateNetwork(t.Context(), dockerx.NetworkSpec{Name: networks[0], Labels: map[string]string{"fixture": "foreign-owner"}}); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = s.modules.docker.RemoveNetwork(context.Background(), networks[0]) })
				if _, err := s.modules.deployDatabases.NetworksForRuntime(t.Context(), environmentID, deploy.RuntimePlanConfig{}, map[string]string{"DATABASE_URL": result.URL}); err == nil {
					t.Fatal("foreign network was adopted by matching name")
				}
				foreign, err := s.modules.docker.InspectNetwork(t.Context(), networks[0])
				if err != nil || len(foreign.Containers) != 0 || foreign.Labels["fixture"] != "foreign-owner" {
					t.Fatal("foreign network was mutated")
				}
			}
			t.Logf("%s: authenticated before and after replacement at a different IP using the same client container and logical URL on port %s", engine, strconv.Itoa(provisionTemplates[engine].port))
		})
	}
	// After the engines, so the MongoDB image is already here.
	t.Run("mongodb-kernel-mitigation", liveMongoKernelMitigation)
}

// pullEngineImage makes the engine's image local before the fixture creates
// anything, and logs what that took. The provision route pulls a missing image
// itself, inside its own request and before the connection wait begins, so a
// pull never counted against connectionBudget; doing it here keeps a slow or
// refused registry from reading as a database that would not start, and puts the
// pull's duration in the log beside the connection wait's.
func pullEngineImage(t *testing.T, docker *dockerx.Client, engine string) {
	t.Helper()
	image := provisionTemplates[engine].image
	if docker.ImagePresent(t.Context(), image) {
		t.Logf("%s: image %s already present, nothing to pull", engine, image)
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	started := time.Now()
	progress := make(chan dockerx.PullProgress, 16)
	go func() {
		for range progress {
		}
	}()
	err := docker.PullImage(ctx, dockerx.ImageRef(image), progress)
	close(progress)
	took := time.Since(started).Round(time.Millisecond)
	if err != nil {
		t.Fatalf("%s: pulling %s failed after %s: %v", engine, image, took, err)
	}
	t.Logf("%s: pulled %s in %s", engine, image, took)
}

// clip is text short enough for a failure message: a response body, or one of
// MongoDB's JSON log lines, which run to a kilobyte each.
func clip(text string, limit int) string {
	text = strings.TrimSpace(text)
	if len(text) > limit {
		return strings.ToValidUTF8(text[:limit], "") + "..."
	}
	return text
}

// describeFixtureContainer says what became of the engine's container, for a
// failure message. The adopt route answers 400 both for a container that is not
// running and for one it cannot connect to, so the reply alone cannot tell an
// engine that crashed from one that is only slow; the state and the last log
// lines can.
func describeFixtureContainer(docker *dockerx.Client, name string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	observed, err := docker.Inspect(ctx, name)
	if err != nil {
		return fmt.Sprintf("container %s: could not be inspected: %v", name, err)
	}
	out := fmt.Sprintf("container %s: state %q, exit code %d, restarts %d, oom-killed %t, engine error %q",
		name, observed.State, observed.ExitCode, observed.RestartNum, observed.OOMKilled, observed.Error)
	tail, err := fixtureLogTail(docker, name, 20)
	if err != nil {
		return out + fmt.Sprintf("; logs unavailable: %v", err)
	}
	for i := range tail {
		tail[i] = clip(tail[i], 300)
	}
	return out + "\nlast log lines:\n" + strings.Join(tail, "\n")
}

// fixtureLogTail is the last n log lines of a fixture container.
func fixtureLogTail(docker *dockerx.Client, name string, n int) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	lines, closer, err := docker.Logs(ctx, name, dockerx.LogOptions{Tail: strconv.Itoa(n)})
	if err != nil {
		return nil, err
	}
	defer closer.Close()
	var tail []string
	for line := range lines {
		tail = append(tail, line.Text)
	}
	return tail, nil
}

// assertMongoKernelMitigation holds the provision route to what this kernel
// asks of MongoDB 8: on Linux 6.19 to 7.0.13 the container is started with
// GLIBC_TUNABLES handing rseq to glibc and labelled with the kernel, and the
// reply says so; on any other kernel neither is there.
func assertMongoKernelMitigation(t *testing.T, s *Server, reply []byte, name string) {
	t.Helper()
	kernel := s.hostKernel(t.Context())
	want := mongoKernelNeedsMitigation(kernel, "8.0")
	var answered struct {
		KernelMitigation string `json:"kernelMitigation"`
	}
	if err := json.Unmarshal(reply, &answered); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	observed, err := s.modules.docker.Inspect(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	tunables := envMap(observed.Env)[deploy.MongoKernelTunables]
	_, labelled := observed.Labels[mongoKernelLabel]
	if want != (answered.KernelMitigation != "") || want != deploy.MongoKernelMitigated(tunables) || want != labelled {
		t.Fatalf("mongodb on Linux %s (mitigation needed: %t): the reply said %q, the container's %s is %q, labelled %t",
			kernel, want, answered.KernelMitigation, deploy.MongoKernelTunables, tunables, labelled)
	}
	t.Logf("mongodb: Linux %s, mitigation needed %t, container started with %s=%q", kernel, want, deploy.MongoKernelTunables, tunables)
}

// fixtureTunables is a fixture container's GLIBC_TUNABLES, for a failure message.
func fixtureTunables(docker *dockerx.Client, name string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	observed, err := docker.Inspect(ctx, name)
	if err != nil {
		return "could not be inspected: " + err.Error()
	}
	return deploy.MongoKernelTunables + "=" + envMap(observed.Env)[deploy.MongoKernelTunables]
}

// TestMongoKernelRefusal pins the line a MongoDB 8.0 container logged on a GitHub
// runner image whose kernel is 6.19 or newer, before exiting 1 and being
// restarted fourteen times, and that nothing else is taken for it.
func TestMongoKernelRefusal(t *testing.T) {
	refusal := `{"t":{"$date":"2026-10-29T04:18:06.566Z"},"s":"F",  "c":"CONTROL",  "id":12257600,"ctx":"main","msg":"MongoDB cannot start: Linux kernel versions 6.19 and newer has a known incompatibility with this version of MongoDB. See https://jira.mongodb.org/browse/SERVER-121912 for more information."}`
	if got := mongoKernelRefusal([]string{`{"msg":"Waiting for connections"}`, refusal}); got != refusal {
		t.Fatalf("the refusal was not recognised: %q", got)
	}
	for _, line := range []string{
		`{"msg":"Waiting for connections"}`,
		`{"s":"F","msg":"MongoDB cannot start: the data directory is not writable"}`,
		`{"s":"F","msg":"Illegal instruction"}`,
	} {
		if got := mongoKernelRefusal([]string{line}); got != "" {
			t.Fatalf("%s was taken for a kernel refusal", line)
		}
	}
}
