package deploy

import (
	"context"
	"strings"
	"testing"
)

// Rotating a secret detection classified keeps its framework's shape — a
// Laravel key keeps its base64: prefix — while any other name still gets 32
// URL-safe random bytes.
func TestRotationKeepsTheDetectedSecretShape(t *testing.T) {
	ctx := context.Background()
	fixture := newPlanningStoreFixture(t)
	projectID, environmentID := insertConfigurationFixtureWithEvidence(t, fixture,
		`{"candidates":[{"id":"candidate-1","name":"app","root":"","profile":"web","buildMethod":"none","confidence":"high","evidence":[],"needsDecision":[],`+
			`"variables":[{"name":"APP_KEY","sources":[".env.example"],"setup":"generate","generateFormat":"laravel","generateLength":32}]}]}`)
	initial := "base64:initial"
	if _, err := fixture.plans.PutVariable(ctx, projectID, environmentID, "APP_KEY", "operator", VariableWriteRequest{
		Revision: 1, Value: &initial, Sensitivity: "secret", Scopes: []string{"runtime"},
	}); err != nil {
		t.Fatal(err)
	}
	rotated, err := fixture.plans.RotateVariable(ctx, projectID, environmentID, "APP_KEY", "operator", 2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rotated.GeneratedValue, "base64:") || len(rotated.GeneratedValue) != 51 {
		t.Fatalf("rotated APP_KEY = %q", rotated.GeneratedValue)
	}
	generated, err := fixture.plans.GenerateVariable(ctx, projectID, environmentID, "WEBHOOK_TOKEN", "operator", 3, []string{"runtime"})
	if err != nil {
		t.Fatal(err)
	}
	if len(generated.GeneratedValue) != 43 || strings.ContainsAny(generated.GeneratedValue, "+/=") {
		t.Fatalf("generated WEBHOOK_TOKEN = %q", generated.GeneratedValue)
	}
}
