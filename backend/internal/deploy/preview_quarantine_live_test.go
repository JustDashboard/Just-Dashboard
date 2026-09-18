package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

func TestLiveLegacyPreviewQuarantinePreservesProduction(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 to exercise legacy preview quarantine")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	f := newQuarantineFixture(t)
	client := liveC4Docker(t)
	backend := NewDockerArtifactBackend(client)
	resolved, err := backend.ResolveImage(ctx, "caddy:2-alpine", "")
	if err != nil {
		t.Fatal(err)
	}
	image, err := backend.PullImage(ctx, resolved.Reference+"@"+resolved.Digest, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	volumeName := fmt.Sprintf("jd-quarantine-production-%d", f.previewID)
	if _, err := client.CreateVolume(ctx, dockerx.VolumeSpec{Name: volumeName}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.RemoveVolume(context.Background(), volumeName, false) })
	productionPort := liveC5LoopbackPort(t)
	production, err := client.Create(ctx, dockerx.ContainerSpec{
		Name: volumeName, Image: immutableRuntimeImage(image), RestartPolicy: "always", Start: true,
		Command: []string{"sh", "-c", "printf production-sentinel > /data/index.html; exec caddy file-server --listen :8080 --root /data"},
		Mounts:  []dockerx.MountSpec{{Type: "volume", Source: volumeName, Target: "/data"}},
		Ports:   []dockerx.PortMapping{{HostIP: "127.0.0.1", HostPort: productionPort, ContainerPort: 8080, Protocol: "tcp"}},
	}, nil)
	if production != nil {
		t.Cleanup(func() { _ = client.RemoveContainer(context.Background(), production.ID, true, false) })
	}
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := client.Create(ctx, dockerx.ContainerSpec{
		Name: fmt.Sprintf("jd-quarantine-legacy-%d", f.previewID), Image: immutableRuntimeImage(image), RestartPolicy: "always", Start: true,
		Command: []string{"caddy", "file-server", "--listen", ":8080", "--root", "/data"},
		Env:     []dockerx.EnvVar{{Name: "DATABASE_URL", Value: "production-secret-canary"}},
		Mounts:  []dockerx.MountSpec{{Type: "volume", Source: volumeName, Target: "/data"}},
		Labels: []dockerx.LabelSpec{
			{Name: "io.just-dashboard.managed", Value: "true"},
			{Name: "io.just-dashboard.environment-id", Value: strconv.FormatInt(f.previewID, 10)},
			{Name: "io.just-dashboard.release-id", Value: strconv.FormatInt(f.releaseID, 10)},
		},
	}, nil)
	if legacy != nil {
		t.Cleanup(func() { _ = client.RemoveContainer(context.Background(), legacy.ID, true, false) })
	}
	if err != nil {
		t.Fatal(err)
	}
	// Seed an old install's immutable runtime record with the real fixture ID.
	if _, err := f.store.DB.Exec(`DELETE FROM deploy_release_runtimes WHERE release_id=?`, f.releaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB.Exec(`INSERT INTO deploy_release_runtimes(release_id,environment_id,kind,runtime_id,state,created_at,updated_at) VALUES(?,?,'container',?,'live',1,1)`, f.releaseID, f.previewID, legacy.ID); err != nil {
		t.Fatal(err)
	}
	legacyPlan := RuntimePlanConfig{Strategy: StrategyStopFirst, InternalPort: 8080, Command: []string{"caddy", "file-server", "--listen", ":8080", "--root", "/data"}, Mounts: []RuntimeMount{{Source: volumeName, Target: "/data", Ownership: OwnershipManaged}}}
	for _, statement := range []string{
		`INSERT INTO deploy_sources(environment_id,revision,kind,config_json,identity_json,digest,created_at) SELECT environment_id,2,kind,config_json,identity_json,digest,created_at FROM deploy_sources WHERE environment_id=? AND revision=1`,
		`INSERT INTO deploy_build_plans(environment_id,revision,method,config_json,evidence_json,preview,digest,created_at) SELECT environment_id,2,method,config_json,evidence_json,preview,digest,created_at FROM deploy_build_plans WHERE environment_id=? AND revision=1`,
		`UPDATE deploy_environments SET desired_revision=2 WHERE id=?`,
	} {
		if _, err := f.store.DB.Exec(statement, f.previewID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.store.DB.Exec(`INSERT INTO deploy_runtime_plans(environment_id,revision,config_json,preview,digest,created_at) VALUES(?,2,?,'','legacy',1)`, f.previewID, string(mustJSON(legacyPlan))); err != nil {
		t.Fatal(err)
	}
	sealed, err := f.automation.sealer.Seal("production-secret-canary")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB.Exec(`INSERT INTO deploy_variable_revisions(environment_id,key,revision,sensitivity,scopes,value_enc,value_digest,active,created_by,created_at) VALUES(?,'DATABASE_URL',1,'secret','runtime',?,'canary',1,'legacy',1)`, f.previewID, sealed); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runs.PreparePreviewQuarantines(ctx); err != nil {
		t.Fatal(err)
	}
	owner := NewDockerRuntimeOwner(client)
	if err := owner.QuarantinePreview(ctx, PreviewQuarantineTarget{EnvironmentID: f.previewID, ReleaseIDs: []int64{f.releaseID}, ContainerIDs: []string{production.ID}}); !errors.Is(err, ErrPreviewIsolation) {
		t.Fatalf("accepted a production container as preview-owned: %v", err)
	}
	routes := &quarantineRoutesStub{}
	controller := NewPreviewQuarantineController(f.runs, owner, routes, nil, nil)
	if err := controller.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	observed, err := client.Inspect(ctx, legacy.ID)
	if err != nil || observed.State == "running" || observed.RestartPol != "no" {
		t.Fatalf("legacy preview was not durably stopped: %+v, %v", observed, err)
	}
	if _, err := client.InspectVolume(ctx, volumeName); err != nil {
		t.Fatalf("shared production data removed: %v", err)
	}
	productionObserved, err := client.Inspect(ctx, production.ID)
	if err != nil || productionObserved.State != "running" || productionObserved.RestartPol != "always" {
		t.Fatalf("production was changed: %v", err)
	}
	response, err := (&http.Client{Timeout: 5 * time.Second}).Get(fmt.Sprintf("http://127.0.0.1:%d/", productionPort))
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 1024))
	response.Body.Close()
	if readErr != nil || response.StatusCode != http.StatusOK || string(body) != "production-sentinel" {
		t.Fatalf("production canary changed: %q %v", body, readErr)
	}
	approvePreviewFixture(t, f.automationFixture, &f.trigger, f.event)
	if _, _, err := f.automation.EnsurePreview(ctx, &f.trigger, f.event); err != nil {
		t.Fatal(err)
	}
	var active int
	if err := f.store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_variable_revisions WHERE environment_id=? AND active=1`, f.previewID).Scan(&active); err != nil || active != 0 {
		t.Fatalf("copied production credential survived reconfiguration: %d %v", active, err)
	}
	var raw string
	if err := f.store.DB.QueryRow(`SELECT r.config_json FROM deploy_runtime_plans r JOIN deploy_environments e ON e.id=r.environment_id AND e.desired_revision=r.revision WHERE e.id=?`, f.previewID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var plan RuntimePlanConfig
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		t.Fatal(err)
	}
	if !plan.PreviewIsolation || len(plan.Mounts) != 1 || plan.Mounts[0].Source == volumeName {
		t.Fatalf("unsafe replacement plan: %s", raw)
	}
	if err := owner.RemovePreviewResources(ctx, f.previewID); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Inspect(ctx, legacy.ID); err != nil {
		t.Fatalf("quarantine deleted retained legacy container: %v", err)
	}
	if _, err := client.InspectVolume(ctx, volumeName); err != nil {
		t.Fatal(err)
	}
	t.Log("legacy preview stopped with restart disabled; exact ownership refusal preserved production, canary data and shared volume; reviewed configuration discarded inherited credential and storage source")
}
