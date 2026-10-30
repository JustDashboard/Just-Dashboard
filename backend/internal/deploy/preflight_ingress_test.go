package deploy

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

type ingressProxyFake struct{ availability proxysvc.Availability }

func (f ingressProxyFake) Availability(context.Context) proxysvc.Availability { return f.availability }

func (f ingressProxyFake) ListVHosts(context.Context) ([]proxysvc.VHost, error) { return nil, nil }

// The proxy status stopped naming a Docker ingress that does not run yet, but
// the release itself starts one, which then serves the domain and orders its
// certificate. Preflight keeps counting that ingress as the proxy.
func TestPreflightCountsAnIngressTheReleaseWouldStart(t *testing.T) {
	for _, availability := range []proxysvc.Availability{
		{IngressState: proxysvc.IngressProvisionable},
		{Caddy: true, IngressContainer: "edge", IngressState: proxysvc.IngressRunning},
	} {
		observer := NewHostPreflightObserver([]string{t.TempDir()}, t.TempDir(), nil, ingressProxyFake{availability})
		observation, err := observer.Observe(context.Background(), ObservationRequest{
			Domains: []PlannedDomain{{Hostname: "localhost", Ownership: OwnershipManaged}},
		})
		if err != nil || len(observation.Domains) != 1 {
			t.Fatalf("%+v: %#v, %v", availability, observation.Domains, err)
		}
		if domain := observation.Domains[0]; !domain.ProxyAvailable || !domain.CertificateAutomation {
			t.Fatalf("%+v: proxy %v, certificate automation %v", availability, domain.ProxyAvailable, domain.CertificateAutomation)
		}
	}
	observer := NewHostPreflightObserver([]string{t.TempDir()}, t.TempDir(), nil, ingressProxyFake{})
	observation, err := observer.Observe(context.Background(), ObservationRequest{
		Domains: []PlannedDomain{{Hostname: "localhost", Ownership: OwnershipManaged}},
	})
	if err != nil || observation.Domains[0].ProxyAvailable || observation.Domains[0].CertificateAutomation {
		t.Fatalf("a host with no proxy: %#v, %v", observation.Domains, err)
	}
}

// wildcardProxyFake answers how a run would prove a wildcard, and counts the
// names it was asked about.
type wildcardProxyFake struct {
	ingressProxyFake
	provider     proxysvc.DNSProvider
	refusal      error
	asked        *[]string
	certificates []proxysvc.Certificate
}

func (f wildcardProxyFake) DeploymentDNSProvider(_ context.Context, wildcard string) (proxysvc.DNSProvider, error) {
	*f.asked = append(*f.asked, wildcard)
	return f.provider, f.refusal
}

func (f wildcardProxyFake) ListCertificates(context.Context) ([]proxysvc.Certificate, error) {
	return f.certificates, nil
}

// On a host whose deployments the Docker Caddy ingress serves, a wildcard is
// asked about exactly as on nginx: not at all while a certificate covers it
// — the release reuses that lineage and the ingress serves a copy — and
// otherwise for the provider whose credentials prove it, which the finding
// names. The ingress used to be a refusal in both cases.
func TestPreflightTreatsAWildcardOnTheCaddyIngressAsOnNginx(t *testing.T) {
	cloudflare, _ := proxysvc.DNSProviderFor("cloudflare")
	caddy := ingressProxyFake{proxysvc.Availability{Caddy: true, IngressContainer: "edge", IngressState: proxysvc.IngressRunning}}
	wildcard := PlannedDomain{Hostname: "*.preview.localhost", HTTPS: true, Ownership: OwnershipManaged}
	draft := &Draft{Data: DraftData{
		Intent:    &DraftIntentConfig{Name: "site", Profile: ProfileWeb},
		Source:    &DraftSourceConfig{Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://example.test/o/r.git", Ref: "main"},
		Detection: &DetectionResult{Source: SourceIdentity{Kind: SourceGit, Revision: strings.Repeat("a", 40)}},
	}}
	configuration := PlanConfiguration{
		Build:   BuildPlanConfig{Method: BuildNone, Secrets: []BuildSecretConfig{}, ReleaseTasks: []ReleaseTaskConfig{}},
		Runtime: RuntimePlanConfig{Strategy: StrategyStopFirst, Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{}},
		Domains: []PlannedDomain{wildcard}, Checks: []PlannedCheck{}, Variables: []PlannedVariable{}, Dependencies: []PlannedDependency{},
	}

	var asked []string
	covered := wildcardProxyFake{
		ingressProxyFake: caddy, provider: cloudflare, asked: &asked,
		certificates: []proxysvc.Certificate{{Name: "preview.localhost", Domains: []string{"*.preview.localhost"}, Source: "certbot"}},
	}
	observation, err := NewHostPreflightObserver([]string{t.TempDir()}, t.TempDir(), nil, covered).
		Observe(context.Background(), ObservationRequest{Domains: []PlannedDomain{wildcard}})
	if err != nil || len(observation.Domains) != 1 {
		t.Fatalf("%#v, %v", observation.Domains, err)
	}
	if domain := observation.Domains[0]; len(asked) != 0 || !domain.CertificateAvailable || domain.CertificateName != "preview.localhost" || domain.CertificateRefusal != "" {
		t.Fatalf("a covered wildcard on the Caddy ingress: asked %v, observed %#v", asked, domain)
	}
	findings := preflightFindings(draft, configuration, observation, false)
	assertC6Finding(t, findings, "certificate_available", PreflightPass, "")
	for _, item := range findings {
		if item.Code == "certificate_wildcard_unavailable" || item.Code == "certificate_automatic" {
			t.Fatalf("a covered wildcard was not simply available: %#v", item)
		}
	}

	asked = nil
	uncovered := wildcardProxyFake{ingressProxyFake: caddy, provider: cloudflare, asked: &asked}
	observation, err = NewHostPreflightObserver([]string{t.TempDir()}, t.TempDir(), nil, uncovered).
		Observe(context.Background(), ObservationRequest{Domains: []PlannedDomain{wildcard}})
	if err != nil || len(observation.Domains) != 1 {
		t.Fatalf("%#v, %v", observation.Domains, err)
	}
	if domain := observation.Domains[0]; len(asked) != 1 || asked[0] != "*.preview.localhost" || domain.CertificateDNSProvider != "Cloudflare" || domain.CertificateRefusal != "" {
		t.Fatalf("an uncovered wildcard on the Caddy ingress: asked %v, observed %#v", asked, domain)
	}
	findings = preflightFindings(draft, configuration, observation, false)
	assertC6Finding(t, findings, "certificate_automatic", PreflightPass, "/proxy/certificates")
	if automatic := findingByCode(findings, "certificate_automatic"); automatic == nil || !strings.Contains(automatic.Measured, "DNS challenge through Cloudflare") {
		t.Fatalf("automatic finding = %#v", automatic)
	}
	if blocked := findingByCode(findings, "certificate_wildcard_unavailable"); blocked != nil {
		t.Fatalf("the Caddy ingress still refuses a wildcard: %#v", blocked)
	}
}

// Preflight asks about a wildcard what the release's certificate step will:
// whose saved DNS credentials prove it, or why nothing here can. A plain name
// is not asked, and the answer becomes the finding — automatic with the
// provider named, or blocked with the refusal and the Certificates page.
func TestPreflightAsksHowAWildcardWouldBeProved(t *testing.T) {
	cloudflare, _ := proxysvc.DNSProviderFor("cloudflare")
	domains := []PlannedDomain{
		{Hostname: "*.preview.localhost", HTTPS: true, Ownership: OwnershipManaged},
		{Hostname: "app.localhost", HTTPS: true, Ownership: OwnershipManaged},
	}
	var asked []string
	observer := NewHostPreflightObserver([]string{t.TempDir()}, t.TempDir(), nil, wildcardProxyFake{
		ingressProxyFake: ingressProxyFake{proxysvc.Availability{Nginx: true, Certbot: true}},
		provider:         cloudflare, asked: &asked,
	})
	observation, err := observer.Observe(context.Background(), ObservationRequest{Domains: domains})
	if err != nil || len(observation.Domains) != 2 {
		t.Fatalf("%#v, %v", observation.Domains, err)
	}
	if len(asked) != 1 || asked[0] != "*.preview.localhost" || observation.Domains[0].CertificateDNSProvider != "Cloudflare" ||
		observation.Domains[1].CertificateDNSProvider != "" {
		t.Fatalf("asked %v; observed %#v", asked, observation.Domains)
	}
	draft := &Draft{Data: DraftData{
		Intent:    &DraftIntentConfig{Name: "site", Profile: ProfileWeb},
		Source:    &DraftSourceConfig{Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://example.test/o/r.git", Ref: "main"},
		Detection: &DetectionResult{Source: SourceIdentity{Kind: SourceGit, Revision: strings.Repeat("a", 40)}},
	}}
	configuration := PlanConfiguration{
		Build:   BuildPlanConfig{Method: BuildNone, Secrets: []BuildSecretConfig{}, ReleaseTasks: []ReleaseTaskConfig{}},
		Runtime: RuntimePlanConfig{Strategy: StrategyStopFirst, Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{}},
		Domains: domains[:1], Checks: []PlannedCheck{}, Variables: []PlannedVariable{}, Dependencies: []PlannedDependency{},
	}
	findings := preflightFindings(draft, configuration, observation, false)
	assertC6Finding(t, findings, "certificate_automatic", PreflightPass, "/proxy/certificates")
	if automatic := findingByCode(findings, "certificate_automatic"); automatic == nil || !strings.Contains(automatic.Measured, "DNS challenge through Cloudflare") {
		t.Fatalf("automatic finding = %#v", automatic)
	}

	refusal := errors.New("*.preview.localhost is a wildcard, which only a DNS challenge can prove, and no DNS provider has credentials saved on this server")
	asked = nil
	observer = NewHostPreflightObserver([]string{t.TempDir()}, t.TempDir(), nil, wildcardProxyFake{
		ingressProxyFake: ingressProxyFake{proxysvc.Availability{Nginx: true, Certbot: true}},
		refusal:          refusal, asked: &asked,
	})
	observation, err = observer.Observe(context.Background(), ObservationRequest{Domains: domains[:1]})
	if err != nil || observation.Domains[0].CertificateRefusal != refusal.Error() {
		t.Fatalf("%#v, %v", observation.Domains, err)
	}
	findings = preflightFindings(draft, configuration, observation, false)
	assertC6Finding(t, findings, "certificate_wildcard_unavailable", PreflightBlocked, "/proxy/certificates")
	if blocked := findingByCode(findings, "certificate_wildcard_unavailable"); blocked == nil || blocked.Action != refusal.Error() {
		t.Fatalf("blocked finding = %#v", blocked)
	}
	for _, item := range findings {
		if item.Code == "certificate_automatic" {
			t.Fatalf("a wildcard nothing can prove claimed automatic issuance: %#v", item)
		}
	}
}
