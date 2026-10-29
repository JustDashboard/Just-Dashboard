package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// liveMongoKernelMitigation is a MongoDB 8 server made the way provisioning
// made one before it knew of the kernel — the template's image, environment,
// published port and named volume, without the mitigation — and saved as a
// connection. On a kernel MongoDB 8 refuses (a runner on Linux 7.0) it
// restarts for ever, as it would on an operator's server; on any other the
// subtest stops it and has the dashboard read its kernel as 7.0.0, so the same
// path runs. The Databases page must offer the mitigation, applying it must
// bring the server up on the same volume, and the offer must then be gone.
// It runs from TestLiveDeploymentDatabaseConnection, which the live job runs.
func liveMongoKernelMitigation(t *testing.T) {
	s, router := dbTestRouter(t)
	docker := dockerx.New("unix:///var/run/docker.sock")
	s.modules.docker = docker
	pullEngineImage(t, docker, "mongodb")
	kernel := s.hostKernel(t.Context())
	refuses := deploy.MongoKernelAffected(kernel)
	if !refuses {
		s.modules.kernelRelease = func(context.Context) (string, error) { return "7.0.0-1017-azure", nil }
		t.Logf("Linux %s does not refuse MongoDB 8; the dashboard is told 7.0.0 and the server is stopped instead", kernel)
	}
	name := fmt.Sprintf("jd-redesign-mongokernel-%d", time.Now().UnixNano())
	volume := name + "-data"
	// The image declares /data/configdb a volume too. Recreate keeps a
	// replaced container's anonymous volumes, as it should for an operator's;
	// the fixture removes the one it made.
	var replacedAnonymous []string
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = docker.RemoveContainer(ctx, name, true, true)
		for _, anonymous := range replacedAnonymous {
			_ = docker.RemoveVolume(ctx, anonymous, false)
		}
		if err := docker.RemoveVolume(ctx, volume, false); err != nil {
			t.Errorf("test volume cleanup: %v", err)
		}
	})
	port, err := freeHostPort(27017, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	tmpl := provisionTemplates["mongodb"]
	const password = "kernel-fixture-1"
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	if _, err := docker.Create(ctx, dockerx.ContainerSpec{
		Name: name, Image: tmpl.image, Start: true, RestartPolicy: "unless-stopped",
		Env:    tmpl.env(tmpl.user, password, "app"),
		Ports:  []dockerx.PortMapping{{HostIP: "127.0.0.1", HostPort: port, ContainerPort: tmpl.port, Protocol: "tcp"}},
		Mounts: []dockerx.MountSpec{{Type: "volume", Source: volume, Target: tmpl.dataPath}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	if refuses {
		// It refuses at once; wait for Docker to have seen it exit.
		for deadline := time.Now().Add(time.Minute); ; time.Sleep(time.Second) {
			observed, err := docker.Inspect(ctx, name)
			if err == nil && (observed.State != "running" || observed.RestartNum > 0) {
				t.Logf("Linux %s: MongoDB 8 without the mitigation is %s after %d restarts", kernel, observed.State, observed.RestartNum)
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("MongoDB 8 kept running without the mitigation on Linux %s, which it refuses: %s", kernel, describeFixtureContainer(docker, name))
			}
		}
	} else if err := docker.Lifecycle(ctx, name, dockerx.ActionStop, nil); err != nil {
		t.Fatal(err)
	}
	sealed, err := s.Sealer.Seal(fmt.Sprintf("mongodb://%s:%s@127.0.0.1:%d/app?authSource=admin", tmpl.user, password, port))
	if err != nil {
		t.Fatal(err)
	}
	inserted, err := s.Store.DB.Exec(`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES(?,?,?,?)`,
		name, string(dbx.DriverMongo), sealed, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	id, _ := inserted.LastInsertId()

	offered := liveKernelProblem(t, router, id)
	if offered == nil || !offered.Fixable || offered.Message != mongoKernelMessage {
		t.Fatalf("the Databases page did not offer the mitigation for %s: %+v\n%s", name, offered, describeFixtureContainer(docker, name))
	}
	t.Logf("the Databases page says %q: %s", offered.Message, offered.Detail)

	if original, err := docker.Inspect(ctx, name); err == nil {
		for _, m := range original.Mounts {
			if m.Type == "volume" && m.Name != "" && m.Name != volume {
				replacedAnonymous = append(replacedAnonymous, m.Name)
			}
		}
	}
	applied := do(t, router, http.MethodPost, pathf("/databases/%d/kernel-mitigation", id), "")
	if applied.Code != http.StatusOK {
		t.Fatalf("applying the mitigation answered HTTP %d: %s", applied.Code, applied.Body)
	}
	waitStarted := time.Now()
	for deadline := waitStarted.Add(2 * time.Minute); ; time.Sleep(time.Second) {
		ping := do(t, router, http.MethodGet, pathf("/databases/%d/ping", id), "")
		var health struct {
			OK bool `json:"ok"`
		}
		_ = json.Unmarshal(ping.Body.Bytes(), &health)
		if health.OK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("MongoDB did not accept connections within 2m0s of the mitigation; the ping answered HTTP %d: %s\n%s",
				ping.Code, clip(ping.Body.String(), 400), describeFixtureContainer(docker, name))
		}
	}
	t.Logf("MongoDB accepted connections %s after the mitigation was applied", time.Since(waitStarted).Round(time.Millisecond))

	observed, err := docker.Inspect(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	onVolume := false
	for _, m := range observed.Mounts {
		onVolume = onVolume || (m.Name == volume && m.Destination == tmpl.dataPath)
	}
	tunables := envMap(observed.Env)[deploy.MongoKernelTunables]
	if !onVolume || !deploy.MongoKernelMitigated(tunables) || observed.Labels[mongoKernelLabel] == "" ||
		observed.RestartPol != "unless-stopped" {
		t.Fatalf("the recreated container: on its volume %t, %s=%q, label %q, restart policy %q",
			onVolume, deploy.MongoKernelTunables, tunables, observed.Labels[mongoKernelLabel], observed.RestartPol)
	}
	if after := liveKernelProblem(t, router, id); after != nil {
		t.Fatalf("the Databases page still offers the mitigation once it is applied: %+v", after)
	}
}

// liveKernelProblem is what the fleet says of one connection's kernel.
func liveKernelProblem(t *testing.T, router http.Handler, id int64) *dbKernelProblem {
	t.Helper()
	read := do(t, router, http.MethodGet, "/databases/fleet", "")
	if read.Code != http.StatusOK {
		t.Fatalf("fleet: HTTP %d: %s", read.Code, read.Body)
	}
	var fleet struct {
		Connections []struct {
			ID            int64            `json:"id"`
			State         string           `json:"state"`
			KernelProblem *dbKernelProblem `json:"kernelProblem"`
		} `json:"connections"`
	}
	if err := json.Unmarshal(read.Body.Bytes(), &fleet); err != nil {
		t.Fatal(err)
	}
	for _, entry := range fleet.Connections {
		if entry.ID == id {
			return entry.KernelProblem
		}
	}
	t.Fatalf("connection %d is not in the fleet", id)
	return nil
}
