package deploy

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// detachedNames is the app domain this server holds: under .acme only.
type detachedNames struct{}

func (detachedNames) DetachedAppDomain(_ context.Context, hostname string) bool {
	h := strings.ToLower(hostname)
	return strings.HasSuffix(h, ".just-dashboard.com") && !strings.HasSuffix(h, ".acme.just-dashboard.com")
}

func releaseWithDomains(t *testing.T, domains []PlannedDomain) *ReleaseWithArtifacts {
	t.Helper()
	raw, err := json.Marshal(runtimeReleaseSnapshot{Version: 1, Domains: domains, Plan: RuntimePlanConfig{Strategy: "stop_first"}})
	if err != nil {
		t.Fatal(err)
	}
	digest := digestBytes(raw)
	envelope, _ := json.Marshal(map[string]json.RawMessage{"snapshot": raw})
	return &ReleaseWithArtifacts{
		Release:   Release{ConfigDigest: digest, Strategy: "stop_first"},
		Artifacts: []ReleaseArtifact{{Kind: ArtifactRuntimeConfig, State: "available", Digest: digest, Metadata: envelope}},
	}
}

// Lifecycle (DESIGN 3.5): a handle released, suspended or detached while in
// use. Every step reads the release without its name, so no route serves it
// and nothing is ordered for it; a redirect to it goes with it.
func TestEveryStepReadsAReleaseWithoutItsDetachedNames(t *testing.T) {
	e := (&NormalizedStepExecutor{}).WithAppDomains(detachedNames{})
	release := releaseWithDomains(t, []PlannedDomain{
		{Hostname: "shop.example.test", HTTPS: true},
		{Hostname: "shop.old.just-dashboard.com", HTTPS: true},
		{Hostname: "www.shop.example.test", HTTPS: true, RedirectTo: "shop.old.just-dashboard.com"},
		{Hostname: "shop.acme.just-dashboard.com", HTTPS: true},
	})
	_, snapshot, err := e.decodeReleaseSnapshot(t.Context(), release)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, domain := range snapshot.Domains {
		names = append(names, domain.Hostname)
	}
	if strings.Join(names, ",") != "shop.example.test,shop.acme.just-dashboard.com" {
		t.Fatalf("names = %v", names)
	}
	// Without the app domain's view nothing is left out.
	_, all, _ := (&NormalizedStepExecutor{}).decodeReleaseSnapshot(t.Context(), release)
	if len(all.Domains) != 4 {
		t.Fatalf("domains = %v", all.Domains)
	}
}

func TestTheCertificateStepNamesTheDetachedNamesItLeftOut(t *testing.T) {
	e := (&NormalizedStepExecutor{certificates: &certificateIssuerFake{result: proxysvc.DeploymentCertificate{Outcome: proxysvc.CertificateReused, Name: "own"}}}).
		WithAppDomains(detachedNames{})
	kept, detached := e.detachedDomains(t.Context(), []PlannedDomain{
		{Hostname: "shop.example.test", HTTPS: true}, {Hostname: "shop.old.just-dashboard.com", HTTPS: true},
	})
	if len(kept) != 1 || len(detached) != 1 {
		t.Fatalf("kept %v detached %v", kept, detached)
	}
	// Other public names remain: the step passes as a warning naming it.
	result := withDetachedWarning(e.certificateStep(t.Context(), certificateNames(kept), discardCertificateLog), detached)
	if result.State != StepWarning || !strings.Contains(string(result.Evidence), "shop.old.just-dashboard.com") ||
		!strings.Contains(string(result.Evidence), `"certificate":"own"`) {
		t.Fatalf("warning = %#v %s", result, result.Evidence)
	}
	// It was the only public name: the step fails with what to do.
	failed := appDomainDetachedResult([]string{"shop.old.just-dashboard.com"})
	if failed.State != StepFailed || failed.ErrorCode != "app_domain_detached" ||
		!strings.Contains(failed.ErrorMessage, "Claim the address again") || !strings.Contains(failed.ErrorMessage, "another address") {
		t.Fatalf("failure = %#v", failed)
	}
	// A failure of the rest stays the failure it was.
	if got := withDetachedWarning(StepResult{State: StepFailed, ErrorCode: "certificate_issue_failed"}, detached); got.State != StepFailed {
		t.Fatalf("failure = %#v", got)
	}
}
