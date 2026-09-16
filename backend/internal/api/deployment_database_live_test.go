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
			projectID, environmentID, _ := insertDeploymentConfigurationAPI(t, s)
			name := fmt.Sprintf("jd-redesign-%s-%d", engine, time.Now().UnixNano())
			probe := name + "-probe"
			blocker := name + "-ip-holder"
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				_ = s.modules.docker.RemoveContainer(ctx, probe, true, false)
				_ = s.modules.docker.RemoveContainer(ctx, blocker, true, false)
				if err := s.modules.deployDatabases.RemoveRuntimeNetworks(ctx, environmentID); err != nil {
					t.Errorf("network cleanup: %v", err)
				}
				_ = s.modules.docker.RemoveContainer(ctx, name, true, false)
				if err := s.modules.docker.RemoveVolume(ctx, name+"-data", false); err != nil {
					t.Errorf("test volume cleanup: %v", err)
				}
			})
			body, _ := json.Marshal(map[string]string{"engine": engine, "name": name, "database": "app"})
			created := do(t, router, http.MethodPost, "/databases/provision", string(body))
			if created.Code != http.StatusAccepted {
				t.Fatalf("provision failed: HTTP %d: %s", created.Code, created.Body)
			}
			deadline := time.Now().Add(2 * time.Minute)
			var conn dbConnection
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
				}
				if time.Now().After(deadline) {
					t.Fatalf("database did not accept connections; last adopt status %d", adopted.Code)
				}
				time.Sleep(time.Second)
			}
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
			if err := s.modules.docker.RemoveContainer(ctx, name, true, false); err != nil {
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
}
