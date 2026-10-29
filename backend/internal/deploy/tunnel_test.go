package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// A domain served through Cloudflare Tunnel: stored with its flag, and only
// when it is set, so an older domain keeps its digest; HTTPS, which
// Cloudflare's edge always is; never a wildcard; plain HTTP on the route,
// which only the connector reaches; and nothing issued for it here.
func TestTunnelDomainIsPlainOnTheRouteAndNeedsNoCertificate(t *testing.T) {
	plain := PlannedDomain{Hostname: "shop.example.com", HTTPS: true, Ownership: OwnershipManaged}
	before, _ := json.Marshal(plain.domainConfig())
	if strings.Contains(string(before), "tunnel") {
		t.Fatalf("a domain without the tunnel stores %s", before)
	}
	tunnel := PlannedDomain{Hostname: "Shop.Example.com", Ownership: OwnershipManaged, Tunnel: true}
	stored, _ := json.Marshal(tunnel.domainConfig())
	if !strings.Contains(string(stored), `"tunnel":true`) {
		t.Fatalf("the tunnel flag is not stored: %s", stored)
	}
	configuration := PlanConfiguration{
		Build:   BuildPlanConfig{Method: BuildImage},
		Runtime: RuntimePlanConfig{Image: "nginx:alpine", InternalPort: 80, Strategy: StrategyBlueGreen, BindAddress: "127.0.0.1"},
		Domains: []PlannedDomain{tunnel, {Hostname: "direct.example.com", HTTPS: true, Ownership: OwnershipManaged}},
	}
	canonical := canonicalConfiguration(configuration)
	for _, domain := range canonical.Domains {
		if domain.Hostname == "shop.example.com" && (!domain.HTTPS || !domain.Tunnel) {
			t.Fatalf("canonical tunnel domain = %+v", domain)
		}
	}
	wildcard := canonical
	wildcard.Domains = []PlannedDomain{{Hostname: "*.example.com", Ownership: OwnershipManaged, Tunnel: true, HTTPS: true}}
	var validation *ValidationError
	if err := wildcard.Validate(); !errors.As(err, &validation) || validation.Field != "domains[0].tunnel" {
		t.Fatalf("a wildcard through the tunnel = %v", err)
	}

	// A list of allowed addresses would see the connector, not the visitor.
	fenced := canonical
	fenced.Runtime.AllowFrom = []string{"203.0.113.0/24"}
	if err := fenced.Validate(); !errors.As(err, &validation) || validation.Field != "runtime.allowFrom" {
		t.Fatalf("allowed addresses on a tunnel's route = %v", err)
	}

	route := deploymentRoute(7, canonical.Domains, "127.0.0.1", 32000)
	if !route.TLS || strings.Join(route.PlainHostnames, ",") != "shop.example.com" {
		t.Fatalf("route = TLS %t, plain %v", route.TLS, route.PlainHostnames)
	}
	only := deploymentRoute(7, []PlannedDomain{{Hostname: "shop.example.com", HTTPS: true, Tunnel: true}}, "127.0.0.1", 32000)
	if only.TLS {
		t.Fatal("a route whose only name is the tunnel's asks for TLS")
	}
	if names := tunnelHostnames(canonical.Domains); strings.Join(names, ",") != "shop.example.com" {
		t.Fatalf("tunnel names = %v", names)
	}
}

// Preflight speaks of the tunnel, not of this server's address and a
// certificate here: connected, a pass naming it; not, a refusal saying where
// to connect one.
func TestPreflightSpeaksOfTheTunnelForItsDomains(t *testing.T) {
	planned := PlannedDomain{Hostname: "shop.example.com", HTTPS: true, Tunnel: true}
	refused := tunnelFindings(planned, HostObservation{}, "domains.shop.example.com")
	if len(refused) != 1 || refused[0].Code != "tunnel_unavailable" || refused[0].Severity != PreflightBlocked || refused[0].DeepLink != "/proxy/tunnel" {
		t.Fatalf("without a tunnel = %+v", refused)
	}
	passed := tunnelFindings(planned, HostObservation{Tunnel: &TunnelObservation{Name: "production", Connected: true}}, "domains.shop.example.com")
	if len(passed) != 1 || passed[0].Code != "tunnel_domain" || passed[0].Severity != PreflightPass || !strings.Contains(passed[0].Measured, "production") {
		t.Fatalf("with a tunnel = %+v", passed)
	}
}

// The release's certificate step leaves a tunnel's names out, and
// activation routes them before the public-route checks; a tunnel that
// cannot be set is said in the transcript and does not fail the release.
func TestTunnelNamesAreRoutedAndNotCertified(t *testing.T) {
	executor := &NormalizedStepExecutor{}
	result := executor.certificateStep(context.Background(), nil, func(string, string) {})
	if result.State != StepSkipped {
		t.Fatalf("no names = %+v", result)
	}
	var routed []string
	executor.WithTunnelRouter(func(_ context.Context, extra []string) error {
		routed = append(routed, extra...)
		return errors.New("Cloudflare refused to set the tunnel's routes: Bad Configuration (code 1056)")
	})
	output := &recordingOutput{}
	executor.routeThroughTunnel(context.Background(), StepExecution{Output: output}, []PlannedDomain{
		{Hostname: "shop.example.com", HTTPS: true, Tunnel: true}, {Hostname: "direct.example.com", HTTPS: true},
	})
	if strings.Join(routed, ",") != "shop.example.com" || !strings.Contains(output.text(), "Cloudflare Tunnel: Cloudflare refused") {
		t.Fatalf("routed %v, transcript %q", routed, output.text())
	}
}

type recordingOutput struct{ lines []string }

func (o *recordingOutput) Log(_ string, text string) error {
	o.lines = append(o.lines, text)
	return nil
}
func (o *recordingOutput) Event(EventInput) error { return nil }
func (o *recordingOutput) text() string           { return strings.Join(o.lines, "\n") }
