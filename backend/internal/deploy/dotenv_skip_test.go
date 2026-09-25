package deploy

import (
	"context"
	"errors"
	"slices"
	"testing"
)

// A pasted local .env sets PORT and NODE_ENV for development; the settings
// page asks the import to leave them out, and the preview and the import
// agree on what that leaves.
func TestDotenvImportLeavesOutTheNamesItIsAskedTo(t *testing.T) {
	t.Parallel()
	fixture := newPlanningStoreFixture(t)
	ctx := context.Background()
	projectID, environmentID := insertConfigurationFixture(t, fixture)
	request := DotenvImportRequest{
		Revision: 1, Dotenv: "PORT=5173\nNODE_ENV=development\nAPI_URL=https://api.example.test\n",
		Sensitivity: "secret", Scopes: []string{"runtime", "build"}, Skip: []string{"PORT", "NODE_ENV"},
	}
	preview, err := fixture.plans.PreviewDotenvImport(ctx, projectID, environmentID, request)
	if err != nil {
		t.Fatal(err)
	}
	want := []DotenvImportVerdict{
		{Name: "PORT", Line: 1, Change: "skipped"},
		{Name: "NODE_ENV", Line: 2, Change: "skipped"},
		{Name: "API_URL", Line: 3, Change: "added"},
	}
	if !slices.Equal(preview.Variables, want) {
		t.Fatalf("verdicts = %+v, want %+v", preview.Variables, want)
	}
	if _, err := fixture.plans.ImportDotenv(ctx, projectID, environmentID, "operator", request); err != nil {
		t.Fatal(err)
	}
	variables, err := fixture.plans.ListVariables(ctx, projectID, environmentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(variables) != 1 || variables[0].Name != "API_URL" {
		t.Fatalf("imported = %+v", variables)
	}

	only := request
	only.Revision, only.Dotenv = variables[0].DesiredRevision, "PORT=3000\n"
	if _, err := fixture.plans.ImportDotenv(ctx, projectID, environmentID, "operator", only); !errors.Is(err, ErrInvalidVariable) {
		t.Fatalf("an import of only skipped names = %v", err)
	}
	bad := request
	bad.Skip = []string{"not a name"}
	if _, err := fixture.plans.PreviewDotenvImport(ctx, projectID, environmentID, bad); !errors.Is(err, ErrInvalidVariable) {
		t.Fatalf("an invalid skipped name = %v", err)
	}
}
