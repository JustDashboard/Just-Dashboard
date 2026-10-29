package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/metrics"
)

// sizingBench is a key bench whose recorder keeps a week at five-minute
// samples, so two days of history is a few hundred rows.
func sizingBench(t *testing.T) *keyBench {
	t.Helper()
	b := newKeyBench(t)
	b.s.modules.metrics = metrics.New(b.s.Store, b.s.Log, 5*time.Minute, 7*24*time.Hour)
	return b
}

// seedSizedRelease gives an environment a live container release whose plan
// is the given runtime JSON, and that container two days of samples: memory
// climbing from 300 to 500 MiB and a fifth of a core busy.
func seedSizedRelease(t *testing.T, b *keyBench, environmentID int64, containerID string, days int) {
	t.Helper()
	db := b.s.Store.DB
	var projectID, sourceID, buildID, planID int64
	if err := db.QueryRow(`SELECT e.project_id, s.id, b.id, r.id FROM deploy_environments e
		JOIN deploy_sources s ON s.environment_id = e.id AND s.revision = e.desired_revision
		JOIN deploy_build_plans b ON b.environment_id = e.id AND b.revision = e.desired_revision
		JOIN deploy_runtime_plans r ON r.environment_id = e.id AND r.revision = e.desired_revision
		WHERE e.id = ?`, environmentID).Scan(&projectID, &sourceID, &buildID, &planID); err != nil {
		t.Fatal(err)
	}
	// The run that made the release, and the plan snapshot it took, are what
	// the configuration's pending state compares the saved plan against.
	run, err := db.Exec(`INSERT INTO deploy_runs(project_id, environment_id, started_at, ended_at, status, state, plan_revision)
		VALUES(?, ?, 1, 2, 'success', 'succeeded', 1)`, projectID, environmentID)
	if err != nil {
		t.Fatal(err)
	}
	runID, _ := run.LastInsertId()
	if _, err := db.Exec(`INSERT INTO deploy_run_plan_snapshots(run_id, environment_id, digest, created_at)
		VALUES(?, ?, 'sha256:snapshot', 0)`, runID, environmentID); err != nil {
		t.Fatal(err)
	}
	result, err := db.Exec(`INSERT INTO deploy_releases(project_id, environment_id, release_number, run_id, state, plan_revision,
		source_id, build_plan_id, runtime_plan_id, created_at) VALUES(?, ?, 1, ?, 'live', 1, ?, ?, ?, 0)`,
		projectID, environmentID, runID, sourceID, buildID, planID)
	if err != nil {
		t.Fatal(err)
	}
	releaseID, _ := result.LastInsertId()
	if _, err := db.Exec(`INSERT INTO deploy_release_runtimes(release_id, environment_id, kind, runtime_id, name, state, created_at, updated_at)
		VALUES(?, ?, 'container', ?, ?, 'live', 0, 0)`, releaseID, environmentID, containerID, fmt.Sprintf("jd-e%d-r1", environmentID)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE deploy_environments SET live_release_id = ? WHERE id = ?`, releaseID, environmentID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)
	samples := days * 288
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := range samples {
		at := now.Add(-time.Duration(samples-i) * 5 * time.Minute)
		mem := int64(300+200*i/max(samples-1, 1)) << 20
		if _, err := tx.Exec(`INSERT INTO metric_container_samples(ts, name, container_id, cpu_percent, mem_bytes, cpu_total, sample_time)
			VALUES(?, ?, ?, 20, ?, ?, ?)`, at.Unix(), fmt.Sprintf("jd-e%d-r1", environmentID), containerID, mem, i*1000, float64(at.Unix())); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func decodeSizing(t *testing.T, body []byte) environmentSizing {
	t.Helper()
	var out environmentSizing
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	return out
}

var sizedRuntime = `{"strategy":"stop_first","image":"nginx:1.27-alpine","internalPort":80,"memoryMb":512,"cpus":4}`

// The sizing of an environment is a read every principal that reads the
// deployment gets — a session, an ordinary key, a deploy key — and a deploy
// key narrowed elsewhere does not. Memory's 95th percentile is 491 MiB of its
// 512, so it is too tight at 640; four cores for a fifth of one is oversized;
// the apply carries both.
func TestSizingIsAReadOfTheEnvironmentsHistory(t *testing.T) {
	b := sizingBench(t)
	image := deploy.DraftSourceConfig{Kind: deploy.SourceImage, Mode: deploy.SourceModeImageReference, Image: "nginx:1.27-alpine"}
	projectID, environmentID := seedKeyDeployment(t, b.s, "shop", image, sizedRuntime, 0)
	other, _ := seedKeyDeployment(t, b.s, "blog", image, `{"strategy":"stop_first"}`, 0)
	seedSizedRelease(t, b, environmentID, "c0ffee000001", 2)
	path := fmt.Sprintf("/api/v1/deploy/%d/environments/%d/sizing", projectID, environmentID)

	session := b.inSession(http.MethodGet, path, nil)
	if session.Code != http.StatusOK {
		t.Fatalf("session = %d %s", session.Code, session.Body.String())
	}
	sizing := decodeSizing(t, session.Body.Bytes())
	if sizing.Status != "available" || sizing.Verdict != metrics.VerdictTooTight || sizing.ProjectName != "shop" {
		t.Fatalf("sizing = %+v", sizing)
	}
	if m := sizing.Memory; m == nil || m.Verdict != metrics.VerdictTooTight || m.LimitMB != 512 || m.PeakMB != 500 || m.SuggestedMB != 640 || m.OffBy != 1.25 {
		t.Fatalf("memory = %+v", sizing.Memory)
	}
	if c := sizing.CPU; c == nil || c.Verdict != metrics.VerdictOversized || c.Limit != 4 || c.Suggested != 0.35 {
		t.Fatalf("cpu = %+v", sizing.CPU)
	}
	if sizing.Apply == nil || *sizing.Apply.MemoryMB != 640 || *sizing.Apply.CPUs != 0.35 {
		t.Fatalf("apply = %+v", sizing.Apply)
	}
	if sizing.Window.Samples != 576 || sizing.Window.Containers != 1 || sizing.Window.CoveredSeconds != 2*86400 || sizing.Revision != 1 || sizing.Pending {
		t.Errorf("window = %+v revision %d pending %v", sizing.Window, sizing.Revision, sizing.Pending)
	}

	for name, key := range map[string]string{"an ordinary key": b.ordinary, "a deploy key": b.deploy} {
		if w := b.call(key, http.MethodGet, path, nil); w.Code != http.StatusOK || decodeSizing(t, w.Body.Bytes()).Verdict != metrics.VerdictTooTight {
			t.Errorf("%s = %d %s", name, w.Code, w.Body.String())
		}
	}
	_, narrowed, err := b.s.Auth.CreateDeployToken(t.Context(), b.admin, "blog-ci", time.Hour, []int64{other})
	if err != nil {
		t.Fatal(err)
	}
	if w := b.call(narrowed, http.MethodGet, path, nil); w.Code != http.StatusForbidden || keyErrorCode(w) != "key_scope" {
		t.Errorf("a key narrowed to another deployment = %d %s", w.Code, w.Body.String())
	}
	if w := b.inSession(http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d/environments/%d/sizing", other, environmentID), nil); w.Code != http.StatusNotFound {
		t.Errorf("an environment through another deployment = %d %s", w.Code, w.Body.String())
	}

	// The fleet: the furthest off first, and a narrowed key reads its own.
	fleet := b.inSession(http.MethodGet, "/api/v1/deploy/sizing", nil)
	var all fleetSizing
	if err := json.Unmarshal(fleet.Body.Bytes(), &all); err != nil || fleet.Code != http.StatusOK || len(all.Environments) != 2 {
		t.Fatalf("fleet = %d %s", fleet.Code, fleet.Body.String())
	}
	if all.Environments[0].ProjectName != "shop" || all.Environments[1].Verdict != metrics.VerdictInsufficient || all.RetentionSeconds != 7*86400 {
		t.Errorf("fleet order = %+v", all.Environments)
	}
	mine := b.call(narrowed, http.MethodGet, "/api/v1/deploy/sizing", nil)
	if mine.Code != http.StatusOK || strings.Contains(mine.Body.String(), `"shop"`) || !strings.Contains(mine.Body.String(), `"blog"`) {
		t.Errorf("narrowed fleet = %d %s", mine.Code, mine.Body.String())
	}
	if w := b.call(b.deploy, http.MethodGet, "/api/v1/deploy/sizing", nil); w.Code != http.StatusOK {
		t.Errorf("deploy key fleet = %d %s", w.Code, w.Body.String())
	}
}

// Applying a suggestion is the configuration PUT the settings make: the plan
// changes, it is audited as a configuration update under whoever made it, the
// plan's own validation still refuses what it always refused, a stale
// revision is still a conflict, and the sizing then reads the saved limits as
// waiting for the next release.
func TestApplyingASuggestionIsTheConfigurationPut(t *testing.T) {
	b := sizingBench(t)
	image := deploy.DraftSourceConfig{Kind: deploy.SourceImage, Mode: deploy.SourceModeImageReference, Image: "nginx:1.27-alpine"}
	projectID, environmentID := seedKeyDeployment(t, b.s, "shop", image, sizedRuntime, 0)
	seedSizedRelease(t, b, environmentID, "c0ffee000002", 2)
	base := fmt.Sprintf("/api/v1/deploy/%d/environments/%d", projectID, environmentID)
	sizing := decodeSizing(t, b.inSession(http.MethodGet, base+"/sizing", nil).Body.Bytes())
	if sizing.Apply == nil {
		t.Fatalf("nothing to apply: %+v", sizing)
	}

	apply := func(key string, patch func(runtime map[string]any), revision int) (int, string) {
		read := b.inSession(http.MethodGet, base+"/configuration", nil)
		var configuration map[string]any
		if err := json.Unmarshal(read.Body.Bytes(), &configuration); err != nil {
			t.Fatal(err)
		}
		runtime := configuration["runtime"].(map[string]any)
		patch(runtime)
		body := map[string]any{
			"revision": revision, "build": configuration["build"], "runtime": runtime,
			"dependencies": configuration["dependencies"], "checks": configuration["checks"], "domains": configuration["domains"],
		}
		if key != "" {
			w := b.call(key, http.MethodPut, base+"/configuration", body)
			return w.Code, w.Body.String()
		}
		w := b.inSession(http.MethodPut, base+"/configuration", body)
		return w.Code, w.Body.String()
	}
	suggested := func(runtime map[string]any) {
		runtime["memoryMb"], runtime["cpus"] = *sizing.Apply.MemoryMB, *sizing.Apply.CPUs
	}

	// The plan's validation is the same gate: a limit Docker would refuse is
	// refused here too, and nothing is written.
	if code, body := apply("", func(runtime map[string]any) { runtime["memoryMb"] = 8 }, sizing.Revision); code != http.StatusUnprocessableEntity || !strings.Contains(body, "memory limit") {
		t.Fatalf("an invalid limit = %d %s", code, body)
	}
	if code, body := apply("", suggested, sizing.Revision); code != http.StatusOK {
		t.Fatalf("apply = %d %s", code, body)
	}
	audit := b.lastAudit("deploy.configuration.update")
	if !audit.Success || audit.Target != fmt.Sprint(environmentID) || audit.Username != "tester" || !strings.Contains(audit.Detail, `"revision":2`) {
		t.Errorf("audit = %+v", audit)
	}
	var runtimeJSON string
	if err := b.s.Store.DB.QueryRow(`SELECT r.config_json FROM deploy_runtime_plans r JOIN deploy_environments e
		ON e.id = r.environment_id AND r.revision = e.desired_revision WHERE e.id = ?`, environmentID).Scan(&runtimeJSON); err != nil {
		t.Fatal(err)
	}
	var plan deploy.RuntimePlanConfig
	if err := json.Unmarshal([]byte(runtimeJSON), &plan); err != nil || plan.MemoryMB != 640 || plan.CPUs != 0.35 || plan.Image != "nginx:1.27-alpine" {
		t.Fatalf("saved plan = %s", runtimeJSON)
	}
	after := decodeSizing(t, b.inSession(http.MethodGet, base+"/sizing", nil).Body.Bytes())
	if after.Verdict != metrics.VerdictRight || !after.Pending || after.Apply != nil || after.Revision != 2 ||
		after.Memory.LiveLimitMB != 512 || after.Memory.LimitMB != 640 {
		t.Errorf("after the apply = %+v %+v", after, after.Memory)
	}
	// An apply from a stale read is the configuration's conflict, and a
	// deploy key applies through the same route under its own name.
	if code, body := apply("", suggested, 1); code != http.StatusConflict {
		t.Errorf("a stale apply = %d %s", code, body)
	}
	if code, body := apply(b.deploy, func(runtime map[string]any) { runtime["cpus"] = 0.5 }, 2); code != http.StatusOK {
		t.Fatalf("a deploy key's apply = %d %s", code, body)
	}
	if audit := b.lastAudit("deploy.configuration.update"); audit.KeyID != b.deployID || !strings.Contains(audit.Detail, `"revision":3`) {
		t.Errorf("deploy key audit = %+v", audit)
	}
}

// What the sizing says when it cannot judge: too little history, a Compose
// stack whose file holds the limits, and a host recording nothing.
func TestSizingSaysWhyItCannotJudge(t *testing.T) {
	b := sizingBench(t)
	image := deploy.DraftSourceConfig{Kind: deploy.SourceImage, Mode: deploy.SourceModeImageReference, Image: "nginx:1.27-alpine"}
	projectID, environmentID := seedKeyDeployment(t, b.s, "young", image, sizedRuntime, 0)
	seedSizedRelease(t, b, environmentID, "c0ffee000003", 0)
	if _, err := b.s.Store.DB.Exec(`INSERT INTO metric_container_samples(ts, name, container_id, cpu_percent, mem_bytes)
		VALUES(?, 'jd-young', 'c0ffee000003', 5, 1048576)`, time.Now().Add(-time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/deploy/%d/environments/%d/sizing", projectID, environmentID)
	young := decodeSizing(t, b.inSession(http.MethodGet, path, nil).Body.Bytes())
	if young.Verdict != metrics.VerdictInsufficient || young.Memory != nil || young.Apply != nil ||
		young.Window.Samples != 1 || young.Window.CoveredSeconds != 300 || young.Window.NeededSeconds != 86400 {
		t.Errorf("an hour = %+v", young)
	}

	stack, stackEnv := seedComposeDeployment(t, b, "stack")
	compose := decodeSizing(t, b.inSession(http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d/environments/%d/sizing", stack, stackEnv), nil).Body.Bytes())
	if compose.Status != "compose" || compose.Apply != nil || !strings.Contains(compose.Reason, "file declares") {
		t.Errorf("compose = %+v", compose)
	}
	var fleet fleetSizing
	_ = json.Unmarshal(b.inSession(http.MethodGet, "/api/v1/deploy/sizing", nil).Body.Bytes(), &fleet)
	for _, environment := range fleet.Environments {
		if environment.ProjectName == "stack" {
			t.Error("the fleet sizes a Compose stack")
		}
	}

	b.s.modules.metrics = metrics.New(b.s.Store, b.s.Log, time.Minute, 0)
	off := decodeSizing(t, b.inSession(http.MethodGet, path, nil).Body.Bytes())
	if off.Status != "unavailable" || !strings.Contains(off.Reason, "JD_METRICS_RETENTION=0") {
		t.Errorf("history off = %+v", off)
	}
}

// seedComposeDeployment is a deployment whose saved build is a Compose stack.
func seedComposeDeployment(t *testing.T, b *keyBench, name string) (int64, int64) {
	t.Helper()
	db := b.s.Store.DB
	result, err := db.Exec(`INSERT INTO deploy_projects(name, profile, repo_path, branch, compose_file, hook_secret, hook_id, enabled, created_at, updated_at)
		VALUES(?, 'web', '', 'main', '', '', ?, 1, 0, 0)`, name, name+"-hook")
	if err != nil {
		t.Fatal(err)
	}
	projectID, _ := result.LastInsertId()
	result, err = db.Exec(`INSERT INTO deploy_environments(project_id, name, slug, kind, desired_revision, strategy, expected_downtime, protected, created_at, updated_at)
		VALUES(?, 'production', 'production', 'production', 1, 'stop_first', 1, 1, 0, 0)`, projectID)
	if err != nil {
		t.Fatal(err)
	}
	environmentID, _ := result.LastInsertId()
	for _, statement := range []string{
		`INSERT INTO deploy_sources(environment_id, revision, kind, config_json, credential_id, identity_json, digest, created_at)
		  VALUES(?, 1, 'compose', '{"kind":"compose"}', 0, '{}', 'sha256:seed', 0)`,
		`INSERT INTO deploy_build_plans(environment_id, revision, method, config_json, evidence_json, preview, digest, created_at)
		  VALUES(?, 1, 'compose', '{"method":"compose"}', '{}', '{}', 'sha256:build-seed', 0)`,
		`INSERT INTO deploy_runtime_plans(environment_id, revision, config_json, preview, digest, created_at)
		  VALUES(?, 1, '{"strategy":"stop_first"}', '{}', 'sha256:runtime-seed', 0)`,
	} {
		if _, err := db.Exec(statement, environmentID); err != nil {
			t.Fatal(err)
		}
	}
	return projectID, environmentID
}

// The event log's OOM kills count against the live release by its label, or
// by its container's id when the event carried no labels, and only since the
// window began.
func TestOOMKillsAreTheLiveReleases(t *testing.T) {
	now := time.Now()
	events := []dockerx.Event{
		{Type: "container", Action: "oom", Time: now, Owner: map[string]string{"release-id": "20"}},
		{Type: "container", Action: "oom", Time: now, ID: "c0ffee"},
		{Type: "container", Action: "oom", Time: now, Owner: map[string]string{"release-id": "19"}},
		{Type: "container", Action: "die", Time: now, Owner: map[string]string{"release-id": "20"}},
		{Type: "container", Action: "oom", Time: now.Add(-48 * time.Hour), Owner: map[string]string{"release-id": "20"}},
	}
	if got := countOOMKills(events, 20, []string{"c0ffee"}, now.Add(-time.Hour)); got != 2 {
		t.Errorf("kills = %d", got)
	}
	if got := countOOMKills(events, 0, nil, now.Add(-time.Hour)); got != 0 {
		t.Errorf("no live release = %d", got)
	}
}
