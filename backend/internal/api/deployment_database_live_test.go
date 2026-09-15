package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
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
			name := fmt.Sprintf("jd-redesign-%s-%d", engine, time.Now().UnixNano())
			probe := name + "-probe"
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				_ = s.modules.docker.RemoveContainer(ctx, probe, true, false)
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
			info, err := dbx.ParseDSN(conn.Driver, result.URL)
			if err != nil {
				t.Fatal("application URL cannot be parsed")
			}
			spec := dockerx.ContainerSpec{Name: probe, Image: provisionTemplates[engine].image, Start: true}
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
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
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
			t.Logf("%s: provisioned, adopted, authenticated from another container on port %s", engine, strconv.Itoa(provisionTemplates[engine].port))
		})
	}
}
