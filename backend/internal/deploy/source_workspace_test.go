package deploy

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The deploy GitHub App's credential reaches a workspace as a fetch's alone: it
// is granted Contents: read, so it is marked ReadOnly and the workspace never
// pushes with it. A token saved by hand is not.
func TestTheDeployAppsWorkspaceCredentialIsReadOnly(t *testing.T) {
	ctx := context.Background()
	fixture := newReleaseStoreFixture(t)
	store := fixture.variables.WithInstallationTokens(&minterFake{})
	remote := "https://github.com/acme/shop.git"
	app, err := store.EnsureGitHubAppCredential(ctx, 501, "acme")
	if err != nil {
		t.Fatal(err)
	}
	credential, err := store.WorkspaceCredential(ctx, app, remote)
	if err != nil {
		t.Fatal(err)
	}
	if !credential.ReadOnly || credential.Environment["GIT_CONFIG_KEY_1"] != "http."+remote+".extraHeader" {
		t.Fatalf("the deploy App's credential = %+v", credential)
	}
	saved, err := store.CreateCredential(ctx, CredentialCreateRequest{Name: "shop-token", Kind: CredentialGitBearer, Target: "github.com", Secret: "ghp_saved_token_0123456789"})
	if err != nil {
		t.Fatal(err)
	}
	if credential, err := store.WorkspaceCredential(ctx, saved.ID, remote); err != nil || credential.ReadOnly {
		t.Fatalf("a saved token = %+v, %v", credential, err)
	}
	// Only over HTTPS, without a user in the address.
	for _, other := range []string{"git@github.com:acme/shop.git", "http://github.com/acme/shop.git", "https://x@github.com/acme/shop.git"} {
		if _, err := store.WorkspaceCredential(ctx, saved.ID, other); !errors.Is(err, ErrWorkspaceCredential) {
			t.Fatalf("a credential for %s = %v", other, err)
		}
	}
}

// What a planned variable may be is said when it is not: the value modes, the
// sensitivities and the scopes there are.
func TestAPlannedVariableThatIsNotValidSaysWhatItMayBe(t *testing.T) {
	base := func() PlanConfiguration {
		return PlanConfiguration{
			Build:   BuildPlanConfig{Method: BuildNone},
			Runtime: RuntimePlanConfig{Strategy: StrategyStopFirst},
			Variables: []PlannedVariable{{
				Name: "API_TOKEN", Sensitivity: "secret", Scopes: []string{"runtime"}, Required: true,
				Reference: "${{credential.api-token}}",
			}},
		}
	}
	if err := base().Validate(); err != nil {
		t.Fatalf("the valid configuration: %v", err)
	}
	for _, tc := range []struct {
		name   string
		change func(*PlannedVariable)
		want   string
	}{
		{"value mode", func(v *PlannedVariable) { v.ValueMode = "secret" }, `invalid value mode "secret" for API_TOKEN: it is literal or reference, or left out`},
		{"sensitivity", func(v *PlannedVariable) { v.Sensitivity = "hidden" }, "invalid sensitivity for API_TOKEN: it is plain or secret"},
		{"no scope", func(v *PlannedVariable) { v.Scopes = nil }, "variable API_TOKEN must have one to three scopes of build, runtime and release_task"},
		{"four scopes", func(v *PlannedVariable) { v.Scopes = []string{"build", "runtime", "release_task", "build"} }, "one to three scopes of build, runtime and release_task"},
		{"scope", func(v *PlannedVariable) { v.Scopes = []string{"deploy"} }, `invalid scope "deploy" for API_TOKEN: a scope is build, runtime or release_task`},
	} {
		config := base()
		tc.change(&config.Variables[0])
		err := config.Validate()
		var invalid *ValidationError
		if !errors.As(err, &invalid) || invalid.Field != "API_TOKEN" || !strings.Contains(invalid.Message, tc.want) {
			t.Fatalf("%s: %v, want %q", tc.name, err, tc.want)
		}
	}
}
