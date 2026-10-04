package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestObservedImportGuardsPreserveExistingCheckoutConfigurationAndDuplication(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newPlanningStoreFixture(t)
	projectID, environmentID := insertConfigurationFixture(t, fixture)
	source := DraftSourceConfig{
		Kind: SourceImport, Mode: SourceModeExistingCheckout, LocalPath: t.TempDir(),
	}
	identity := SourceIdentity{
		Kind: SourceImport, Repository: "checkout-app", Revision: strings.Repeat("a", 40),
	}
	sourceJSON, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	identityJSON, err := json.Marshal(identity)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DB.Exec(`
		INSERT INTO deploy_sources(environment_id, revision, kind, config_json, identity_json, digest, created_at)
		VALUES(?, 2, ?, ?, ?, ?, ?)`, environmentID, SourceImport, string(sourceJSON), string(identityJSON),
		digestBytes(sourceJSON, identityJSON), fixture.now.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DB.Exec(`
		INSERT INTO deploy_build_plans(environment_id, revision, method, config_json, evidence_json, preview, digest, created_at)
		 SELECT environment_id, 2, method, config_json, evidence_json, preview, digest, created_at
		   FROM deploy_build_plans WHERE environment_id = ? AND revision = 1`, environmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DB.Exec(`
		INSERT INTO deploy_runtime_plans(environment_id, revision, config_json, preview, digest, created_at)
		 SELECT environment_id, 2, config_json, preview, digest, created_at
		   FROM deploy_runtime_plans WHERE environment_id = ? AND revision = 1`, environmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DB.Exec(`UPDATE deploy_environments SET desired_revision = 2 WHERE id = ?`, environmentID); err != nil {
		t.Fatal(err)
	}
	configuration, err := fixture.plans.EnvironmentConfiguration(ctx, projectID, environmentID)
	if err != nil {
		t.Fatal(err)
	}
	configuration, err = fixture.plans.SaveEnvironmentConfiguration(ctx, projectID, environmentID, ConfigurationWriteRequest{
		Revision: configuration.Revision, Build: configuration.Build,
		Runtime: RuntimePlanConfig{Image: "alpine:3", Strategy: StrategyStopFirst},
	})
	if err != nil || configuration.Revision != 3 || configuration.Runtime.Image != "alpine:3" {
		t.Fatalf("existing checkout configuration=%#v err=%v", configuration, err)
	}
	source.LocalPath = t.TempDir()
	configuration, err = fixture.plans.SaveEnvironmentSource(ctx, projectID, environmentID, configuration.Revision, source, identity)
	if err != nil || configuration.Revision != 4 || configuration.Source.LocalPath != source.LocalPath {
		t.Fatalf("existing checkout source update=%#v err=%v", configuration, err)
	}
	draft, err := fixture.plans.Duplicate(ctx, projectID, 7, "tester", "checkout-copy")
	if err != nil {
		t.Fatal(err)
	}
	if draft.Data.Source.Kind != SourceImport || draft.Data.Source.Mode != SourceModeExistingCheckout ||
		draft.Data.Source.LocalPath != source.LocalPath || draft.Data.Configuration.Runtime.Image != "alpine:3" {
		t.Fatalf("existing checkout duplicate=%#v", draft)
	}
	_, err = fixture.plans.SaveEnvironmentSource(ctx, projectID, environmentID, configuration.Revision,
		DraftSourceConfig{Kind: SourceImport, Mode: SourceModeExistingStack, ResourceID: "external-stack"},
		SourceIdentity{Kind: SourceImport, Repository: "external-stack"})
	if !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("checkout converted to runtime observation: error=%v", err)
	}
}
