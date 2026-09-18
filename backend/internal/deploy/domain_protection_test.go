package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// The plan holds a bcrypt hash and never the password: sealing happens once
// at the write, a plan that still carries a password is refused, and what is
// stored is what an htpasswd file holds.
func TestDomainProtectionIsSealedOnceAndNeverStoredInClear(t *testing.T) {
	t.Parallel()
	plan := PlanConfiguration{Domains: []PlannedDomain{
		{Hostname: "staging.example.test", HTTPS: true, Ownership: OwnershipManaged, Protection: &DomainProtection{Username: "team", Password: "correct horse battery"}},
		{Hostname: "open.example.test", HTTPS: true, Ownership: OwnershipManaged},
	}}
	if err := sealDomainProtection(&plan); err != nil {
		t.Fatal(err)
	}
	sealed := plan.Domains[0].Protection
	if sealed.Password != "" || !bcryptHashRE.MatchString(sealed.Hash) || bcrypt.CompareHashAndPassword([]byte(sealed.Hash), []byte("correct horse battery")) != nil {
		t.Fatalf("sealed = %+v", sealed)
	}
	if plan.Domains[1].Protection != nil {
		t.Fatal("an unprotected domain gained protection")
	}
	// Sealing again leaves a sealed plan exactly as it is — the digest a
	// preflight computed must still hold at commit.
	before := sealed.Hash
	if err := sealDomainProtection(&plan); err != nil || plan.Domains[0].Protection.Hash != before {
		t.Fatalf("resealing changed the hash: %v", err)
	}
	if err := plan.Domains[0].Protection.validate("domains[0].protection"); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(plan)
	if strings.Contains(string(raw), "correct horse") || strings.Contains(string(raw), `"password"`) {
		t.Fatalf("plan leaked the password: %s", raw)
	}

	for _, fixture := range []struct {
		name       string
		protection DomainProtection
		message    string
	}{
		{"short password", DomainProtection{Username: "team", Password: "short"}, "at least 8"},
		{"long password", DomainProtection{Username: "team", Password: strings.Repeat("x", 73)}, "72 characters"},
	} {
		plan := PlanConfiguration{Domains: []PlannedDomain{{Hostname: "a.example.test", Ownership: OwnershipManaged, Protection: &fixture.protection}}}
		err := sealDomainProtection(&plan)
		var validation *ValidationError
		if !errors.As(err, &validation) || validation.Field != "domains[0].protection" || !strings.Contains(validation.Message, fixture.message) {
			t.Fatalf("%s: %v", fixture.name, err)
		}
	}
	for _, fixture := range []struct {
		name       string
		protection *DomainProtection
		message    string
	}{
		{"unsealed", &DomainProtection{Username: "team", Password: "correct horse battery"}, "sealed"},
		{"no hash", &DomainProtection{Username: "team"}, "needs a password"},
		{"bad user", &DomainProtection{Username: "te am", Hash: "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"}, "user name"},
		{"bad hash", &DomainProtection{Username: "team", Hash: "plaintext"}, "needs a password"},
	} {
		err := fixture.protection.validate("domains[0].protection")
		if err == nil || !strings.Contains(err.Error(), fixture.message) {
			t.Fatalf("%s: %v", fixture.name, err)
		}
	}
	if canonicalDomainProtection(&DomainProtection{}) != nil || canonicalDomainProtection(nil) != nil {
		t.Fatal("an empty protection object survived canonicalisation")
	}
}

// Validate refuses a plan whose protection is still a password, so no write
// path can store one by forgetting to seal.
func TestPlanValidationRefusesAnUnsealedDomainPassword(t *testing.T) {
	t.Parallel()
	plan := PlanConfiguration{
		Build:     BuildPlanConfig{Method: BuildImage, Secrets: []BuildSecretConfig{}, ReleaseTasks: []ReleaseTaskConfig{}},
		Runtime:   RuntimePlanConfig{Image: "nginx:alpine", Strategy: StrategyStopFirst, BindAddress: "127.0.0.1"},
		Variables: []PlannedVariable{}, Dependencies: []PlannedDependency{}, Checks: []PlannedCheck{},
		Domains: []PlannedDomain{{Hostname: "staging.example.test", HTTPS: true, Ownership: OwnershipManaged, Protection: &DomainProtection{Username: "team", Password: "correct horse battery"}}},
	}
	err := plan.Validate()
	var validation *ValidationError
	if !errors.As(err, &validation) || validation.Field != "domains[0].protection" {
		t.Fatalf("unsealed plan validated: %v", err)
	}
	if err := sealDomainProtection(&plan); err != nil {
		t.Fatal(err)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("sealed plan refused: %v", err)
	}
}

// Activation hands the proxy the union of the protected domains' credentials,
// once per user, and nothing for an open route.
func TestDeploymentRouteCarriesProtectionToTheProxy(t *testing.T) {
	t.Parallel()
	hash := "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"
	route := deploymentRoute(7, []PlannedDomain{
		{Hostname: "Staging.example.test", HTTPS: true, Ownership: OwnershipManaged, Protection: &DomainProtection{Username: "team", Hash: hash}},
		{Hostname: "staging2.example.test", HTTPS: true, Ownership: OwnershipManaged, Protection: &DomainProtection{Username: "team", Hash: hash}},
		{Hostname: "www.example.test", HTTPS: true, Ownership: OwnershipManaged},
	}, "127.0.0.1", 3000)
	if len(route.BasicAuth) != 1 || route.BasicAuth[0].Username != "team" || route.BasicAuth[0].Hash != hash || len(route.Domains) != 3 {
		t.Fatalf("route = %+v", route)
	}
	if open := deploymentRoute(7, []PlannedDomain{{Hostname: "a.example.test", Ownership: OwnershipManaged}}, "127.0.0.1", 3000); len(open.BasicAuth) != 0 {
		t.Fatalf("open route carries credentials: %+v", open)
	}
}

// A protected domain round-trips through the configuration store as a hash:
// the password is sealed on the way in, the read carries the hash and no
// password, and a later save that sends the hash back keeps it unchanged.
func TestConfigurationStoreSealsAndKeepsDomainProtection(t *testing.T) {
	ctx := context.Background()
	fixture := newReleaseStoreFixture(t)
	fixture.addPlan(t, 1, strings.Repeat("a", 40))
	current, err := fixture.variables.EnvironmentConfiguration(ctx, fixture.projectID, fixture.envID)
	if err != nil {
		t.Fatal(err)
	}
	request := ConfigurationWriteRequest{
		Revision: current.Revision, Build: current.Build, Runtime: current.Runtime,
		Dependencies: current.Dependencies, Checks: current.Checks,
		Domains: []PlannedDomain{{Hostname: "staging.example.test", HTTPS: true, Ownership: OwnershipManaged,
			Protection: &DomainProtection{Username: "team", Password: "correct horse battery"}}},
	}
	saved, err := fixture.variables.SaveEnvironmentConfiguration(ctx, fixture.projectID, fixture.envID, request)
	if err != nil {
		t.Fatal(err)
	}
	protection := saved.Domains[0].Protection
	if protection == nil || protection.Username != "team" || protection.Password != "" ||
		bcrypt.CompareHashAndPassword([]byte(protection.Hash), []byte("correct horse battery")) != nil {
		t.Fatalf("saved protection = %+v", protection)
	}
	read, err := fixture.variables.EnvironmentConfiguration(ctx, fixture.projectID, fixture.envID)
	if err != nil || read.Domains[0].Protection == nil || read.Domains[0].Protection.Hash != protection.Hash || read.Domains[0].Protection.Password != "" {
		t.Fatalf("read back = %+v, %v", read.Domains, err)
	}
	// Saving the read-back plan keeps the hash rather than demanding the password again.
	request.Revision, request.Domains = read.Revision, read.Domains
	request.Domains[0].HTTPS = false
	again, err := fixture.variables.SaveEnvironmentConfiguration(ctx, fixture.projectID, fixture.envID, request)
	if err != nil || again.Domains[0].Protection.Hash != protection.Hash || again.Domains[0].HTTPS {
		t.Fatalf("resave = %+v, %v", again.Domains, err)
	}
	// A short password is refused with the field it belongs to.
	request.Revision = again.Revision
	request.Domains[0].Protection = &DomainProtection{Username: "team", Password: "short"}
	_, err = fixture.variables.SaveEnvironmentConfiguration(ctx, fixture.projectID, fixture.envID, request)
	var validation *ValidationError
	if !errors.Is(err, ErrInvalidPlan) || !errors.As(err, &validation) || validation.Field != "domains[0].protection" {
		t.Fatalf("short password: %v", err)
	}
	// Removing protection stores none.
	request.Domains[0].Protection = nil
	open, err := fixture.variables.SaveEnvironmentConfiguration(ctx, fixture.projectID, fixture.envID, request)
	if err != nil || open.Domains[0].Protection != nil {
		t.Fatalf("open = %+v, %v", open.Domains, err)
	}
}
