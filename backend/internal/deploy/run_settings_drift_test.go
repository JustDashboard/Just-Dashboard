package deploy

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestRunSettingsDriftNamesWhatChangedSinceTheRun(t *testing.T) {
	t.Parallel()
	fixture := newReleaseStoreFixture(t)
	fixture.addPlan(t, 1, strings.Repeat("a", 40))
	run, _ := fixture.claimedRun(t, 1)
	ctx := context.Background()

	drift, err := fixture.variables.RunSettingsDrift(ctx, fixture.projectID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if drift.Changed || len(drift.Changes) != 0 || drift.PlanRevision != 1 || drift.DesiredRevision != 1 {
		t.Fatalf("an unchanged environment drifted: %+v", drift)
	}

	// The operator switches the package manager and the port, and scopes the
	// run's variable to the build too.
	fixture.addPlanWithRuntime(t, 2, strings.Repeat("a", 40), RuntimePlanConfig{
		InternalPort: 8080, BindAddress: "127.0.0.1", Strategy: StrategyBlueGreen,
		Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{},
	})
	build, _ := json.Marshal(BuildPlanConfig{Method: BuildDockerfile, Dockerfile: "Dockerfile", PackageManager: "bun",
		Secrets: []BuildSecretConfig{}, ReleaseTasks: []ReleaseTaskConfig{}})
	if _, err := fixture.base.DB.Exec(`DELETE FROM deploy_build_plans WHERE environment_id=? AND revision=2`, fixture.envID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.base.DB.Exec(
		"INSERT INTO deploy_build_plans(environment_id, revision, method, config_json, evidence_json, preview, digest, created_at) VALUES(?, 2, 'dockerfile', ?, '{}', ?, ?, ?)",
		fixture.envID, string(build), string(build), digestBytes(build), fixture.now.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.base.DB.Exec(`UPDATE deploy_variable_revisions SET active=0 WHERE environment_id=? AND key='TOKEN'`, fixture.envID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.base.DB.Exec(
		`INSERT INTO deploy_variable_revisions(environment_id, key, revision, sensitivity, scopes, value_enc, value_digest, active, created_by, created_at)
		 SELECT environment_id, key, 2, sensitivity, 'build,runtime,release_task', value_enc, value_digest, 1, 'admin', created_at
		   FROM deploy_variable_revisions WHERE environment_id=? AND key='TOKEN' AND revision=1`, fixture.envID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.base.DB.Exec(
		`INSERT INTO deploy_variable_revisions(environment_id, key, revision, sensitivity, scopes, value_enc, value_digest, active, created_by, created_at)
		 VALUES(?, 'NODE_OPTIONS', 1, 'plain', 'build', 'sealed', ?, 1, 'admin', ?)`,
		fixture.envID, fakeContentDigest("--max-old-space-size=3072"), fixture.now.Unix()); err != nil {
		t.Fatal(err)
	}

	drift, err = fixture.variables.RunSettingsDrift(ctx, fixture.projectID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []SettingsChange{
		{Kind: "build", Field: "packageManager", Change: "added", After: "bun"},
		{Kind: "runtime", Field: "internalPort", Change: "changed", Before: "3000", After: "8080"},
		{Kind: "variable", Field: "NODE_OPTIONS", Change: "added", After: "build"},
		{Kind: "variable", Field: "TOKEN", Change: "scope", Before: "runtime,release_task", After: "build,runtime,release_task"},
	}
	if !drift.Changed || drift.PlanRevision != 1 || drift.DesiredRevision != 2 || !reflect.DeepEqual(drift.Changes, want) {
		t.Fatalf("drift = %+v\nwant changes %+v", drift, want)
	}
	encoded, _ := json.Marshal(drift)
	if strings.Contains(string(encoded), "release-store-super-secret") || strings.Contains(string(encoded), "sha256:") {
		t.Fatalf("drift carries a value or a digest: %s", encoded)
	}
	if _, err := fixture.variables.RunSettingsDrift(ctx, fixture.projectID+1, run.ID); err == nil {
		t.Fatal("another project's run was read")
	}
}
