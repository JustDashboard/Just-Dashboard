package deploy

import (
	"context"
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
