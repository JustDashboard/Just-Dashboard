package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

type observedRecoveryChecker struct {
	owner  *backupRecoveryChecker
	t      *testing.T
	checks int
}

func (o *observedRecoveryChecker) CheckRecovery(ctx context.Context, request backups.RecoveryCheckRequest) (backups.RecoveryCheckResult, error) {
	o.checks++
	result, checkErr := o.owner.CheckRecovery(ctx, request)
	containers, err := o.owner.server.modules.docker.ListContainersWithLabels(ctx, map[string]string{"io.just-dashboard.restore-check": strconv.FormatInt(request.VerificationID, 10), "io.just-dashboard.restore-owner": request.OwnerKey})
	if err != nil || len(containers) != 1 {
		o.t.Fatalf("isolated checker identity: %v %v", containers, err)
	}
	detail, err := o.owner.server.modules.docker.Inspect(ctx, containers[0].ID)
	if err != nil {
		o.t.Fatal(err)
	}
	if detail.NetworkMode != "none" || len(detail.Ports) != 0 {
		o.t.Fatalf("restore checker has network access: %+v", detail.Container)
	}
	for _, mount := range detail.Mounts {
		if mount.Source != request.Directory || mount.Destination != "/restore" {
			o.t.Fatalf("checker received an unexpected mount: %+v", mount)
		}
	}
	return result, checkErr
}
func (o *observedRecoveryChecker) CleanupRecovery(ctx context.Context, id int64, key string) error {
	return o.owner.CleanupRecovery(ctx, id, key)
}

func TestLiveSQLiteApplicationRecoveryVerification(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 to verify a real restored SQLite application")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	s := testServer(t)
	host := os.Getenv("DOCKER_HOST")
	if host == "" {
		host = "unix:///var/run/docker.sock"
	}
	s.modules.docker = dockerx.New(host)
	docker := s.modules.docker
	directory, err := filepath.Abs("testdata/sqlite-recovery")
	if err != nil {
		t.Fatal(err)
	}
	tag := fmt.Sprintf("jd-recovery-fixture:%d", time.Now().UnixNano())
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, _ = docker.RemoveImage(cleanup, tag, false, false)
	})
	image, err := docker.BuildImmutable(ctx, dockerx.ImmutableBuildOptions{Dir: directory, Tag: tag}, nil)
	if err != nil {
		t.Fatal(err)
	}
	job, db, source := sqliteRecoveryJob(t, s, image.ConfigDigest)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	application, err := docker.Create(ctx, dockerx.ContainerSpec{
		Name: fmt.Sprintf("jd-recovery-app-%d", time.Now().UnixNano()), Image: image.ConfigDigest, Start: true,
		Env:    []dockerx.EnvVar{{Name: "DATABASE_PATH", Value: "/data/state.sqlite"}},
		Mounts: []dockerx.MountSpec{{Type: "bind", Source: source, Target: "/data"}},
		Ports:  []dockerx.PortMapping{{HostIP: "127.0.0.1", HostPort: port, ContainerPort: 8080, Protocol: "tcp"}},
	}, nil)
	if application != nil {
		t.Cleanup(func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			_ = docker.RemoveContainer(cleanup, application.ID, true, true)
		})
	}
	if err != nil {
		t.Fatal(err)
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/", application.Ports[0].HostPort)
	reader := &http.Client{Timeout: time.Second}
	served := false
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
		response, err := reader.Get(url)
		if err == nil {
			var record struct {
				Schema int    `json:"schema"`
				Canary string `json:"canary"`
			}
			err = json.NewDecoder(response.Body).Decode(&record)
			response.Body.Close()
			if err == nil && record.Schema == 7 && record.Canary == "known-canary" {
				served = true
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !served {
		if lines, closer, err := docker.Logs(ctx, application.ID, dockerx.LogOptions{Tail: "30", Stdout: true, Stderr: true}); err == nil {
			for line := range lines {
				t.Log(line.Text)
			}
			closer.Close()
		}
		if detail, err := docker.Inspect(ctx, application.ID); err == nil {
			t.Logf("fixture application state=%s ports=%v image=%s", detail.State, application.Ports, image.ConfigDigest)
		}
		t.Fatal("source application never served its canary")
	}
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "recovery-live-admin", auth.RoleAdmin)}
	started := admin.do(http.MethodPost, fmt.Sprintf("/api/v1/backups/%d/run", job.ID), "", nil)
	if started.Code != http.StatusAccepted {
		t.Fatalf("start backup: %d %s", started.Code, started.Body.String())
	}
	var run *backups.Run
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
		run, err = s.modules.backupStore.LastRun(ctx, job.ID)
		if err == nil && run.Status != backups.StatusRunning {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if run == nil || run.Status != backups.StatusSuccess {
		t.Fatalf("backup failed: %+v %v", run, err)
	}
	if _, err := db.Exec(`DROP TABLE records`); err != nil {
		t.Fatal(err)
	}
	checker := &observedRecoveryChecker{owner: &backupRecoveryChecker{server: s}, t: t}
	s.modules.backupRunner.WithRecoveryChecker(checker)
	verified := admin.do(http.MethodPost, fmt.Sprintf("/api/v1/backups/runs/%d/verify-restore", run.ID), "", nil)
	if verified.Code != http.StatusOK {
		t.Fatalf("restore verification: %d %s", verified.Code, verified.Body.String())
	}
	var record backups.RestoreVerification
	if err := json.Unmarshal(verified.Body.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if !record.Matches(run, job.Recovery) {
		t.Fatalf("verification not bound to artifact/application: %+v", record)
	}
	gate := newDeploymentBackupGate(s.modules.backupStore, s.modules.backupRunner, docker)
	request := deploy.BackupGateRequest{JobID: job.ID, MaxAgeSeconds: 3600, RequireRestoreTest: true, PersistentSources: []string{source}}
	evidence, err := gate.Evaluate(ctx, request)
	if err != nil || !evidence.RestoreTested || evidence.RestoreVerificationID != record.ID || checker.checks != 1 {
		t.Fatalf("gate did not use exact-artifact proof: %+v %v checks=%d", evidence, err, checker.checks)
	}
	var tables int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='records'`).Scan(&tables); err != nil || tables != 0 {
		t.Fatal("restore check overwrote live source data")
	}
	job.Recovery.SchemaVersion = "8"
	if _, err := s.modules.backupStore.Update(ctx, job.ID, job, nil); err != nil {
		t.Fatal(err)
	}
	failed, err := gate.Evaluate(ctx, request)
	if err == nil || failed.RestoreTested || checker.checks != 2 {
		t.Fatalf("incompatible schema passed: %+v %v checks=%d", failed, err, checker.checks)
	}
	remaining, err := docker.ListContainersWithLabels(ctx, map[string]string{"io.just-dashboard.restore-check": strconv.FormatInt(record.ID, 10)})
	if err != nil || len(remaining) != 0 {
		t.Fatalf("restore checker remained: %v %v", remaining, err)
	}
	workspaces, err := filepath.Glob(filepath.Join(s.Cfg.DataDir, "staging", "restore-check-*"))
	if err != nil || len(workspaces) != 0 {
		t.Fatalf("temporary restored data remained: %v %v", workspaces, err)
	}
	if _, err := docker.Inspect(ctx, application.ID); err != nil {
		t.Fatal("restore cleanup removed the source application")
	}
	t.Logf("application image %s, artifact %s: restored %d bytes through the application's SQLite connection in %s; incompatible schema blocked", image.ConfigDigest, run.Manifest.ArtifactDigest, record.Bytes, record.EndedAt.Sub(record.StartedAt).Round(time.Millisecond))
}
