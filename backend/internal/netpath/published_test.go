package netpath

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

const publishedID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func publishedProviders() PublishedProviders {
	return PublishedProviders{
		Publication: func(context.Context) (Publication, error) {
			return Publication{Container: "shop-db", HostIP: "0.0.0.0", HostPort: 5432, ContainerPort: 5432, Protocol: "tcp",
				Addresses: []PublishedAddress{{Network: "bridge", IPv4: "10.0.0.2"}}}, nil
		},
		Chains: func(context.Context, string) netsec.DockerChains {
			return netsec.DockerChains{Family: "inet", NAT: netsec.ParseDockerNAT("-A DOCKER ! -i docker0 -p tcp -m tcp --dport 5432 -j DNAT --to-destination 10.0.0.2:5432"),
				User: []string{"-A DOCKER-USER -j RETURN"}, ForwardPolicy: "DROP",
				Forward: []string{"-A FORWARD -j ts-forward", "-A FORWARD -j DOCKER-USER", "-A FORWARD -j DOCKER-FORWARD", "-A FORWARD -j ufw-before-forward"},
				Filter:  []string{"-A DOCKER -d 10.0.0.2/32 ! -i docker0 -o docker0 -p tcp -m tcp --dport 5432 -j ACCEPT"}}
		},
		Firewall: func(context.Context) (*netsec.FirewallStatus, error) {
			return &netsec.FirewallStatus{Backend: netsec.BackendUFW, Available: true, Enabled: true, Policy: netsec.DefaultPolicy{Incoming: "deny"}}, nil
		},
		Gateway: func(context.Context) (*netx.GatewayView, error) { return &netx.GatewayView{}, nil },
		Owners: func(context.Context) (OwnerSnapshot, error) {
			return OwnerSnapshot{Listeners: []proxysvc.Listener{{Protocol: "tcp", Address: "0.0.0.0", Port: 5432, Process: "docker-proxy", Reach: "all",
				Container: &proxysvc.ListenerContainer{ID: publishedID[:12], Name: "shop-db", Published: true}}}}, nil
		},
		External: func(context.Context, int) ([]ExternalMeasurement, error) { return nil, nil },
		PublicAddress: func(context.Context) (string, error) {
			return "", nil
		},
	}
}

func layer(result *Result, id string) Evidence {
	for _, e := range result.Evidence {
		if e.ID == id {
			return e
		}
	}
	return Evidence{}
}

func TestInvestigatePublishedJoinsDockerNATFiltersProviderAndMeasurement(t *testing.T) {
	request := PublishedRequest{ContainerID: publishedID, HostPort: 5432, Protocol: "tcp", Family: "inet"}
	result, err := InvestigatePublished(t.Context(), request, publishedProviders())
	if err != nil {
		t.Fatal(err)
	}
	order := []string{}
	for _, e := range result.Evidence {
		order = append(order, e.ID)
	}
	if strings.Join(order, ",") != "publication,listener,dnat,docker-user,firewall,gateway,proxy,provider,external" {
		t.Fatalf("layers: %v", order)
	}
	if e := layer(result, "dnat"); e.Basis != Observed || e.State != "observed" || result.Scope.Address != "10.0.0.2" {
		t.Fatalf("Docker's translation must be observed and pinned to the container: %+v %+v", e, result.Scope)
	}
	if e := layer(result, "docker-user"); e.State != "empty" {
		t.Fatalf("Docker's own RETURN is not an operator rule: %+v", e)
	}
	firewall := layer(result, "firewall")
	if !strings.Contains(strings.Join(firewall.Limitations, " "), "inbound default do not apply") {
		t.Fatalf("ufw's inbound default must be said not to hold a published port: %+v", firewall)
	}
	if firewall.State != "docker_admits_unless_earlier" || !strings.Contains(strings.Join(firewall.Limitations, " "), "ts-forward") || !strings.Contains(result.Comparison, "admitted by Docker's rule ahead of the firewall adapter, unless ts-forward drops it first") {
		t.Fatalf("Docker's accept ahead of ufw decides the forwarded leg unless the chain before it drops it: %+v %s", firewall, result.Comparison)
	}
	clean := publishedProviders()
	clean.Chains = func(context.Context, string) netsec.DockerChains {
		return netsec.DockerChains{NAT: netsec.ParseDockerNAT("-A DOCKER ! -i docker0 -p tcp -m tcp --dport 5432 -j DNAT --to-destination 10.0.0.2:5432"),
			User:    []string{"-A DOCKER-USER -j RETURN"},
			Forward: []string{"-A FORWARD -j DOCKER-USER", "-A FORWARD -j DOCKER-FORWARD", "-A FORWARD -j ufw-before-forward"},
			Filter:  []string{"-A DOCKER -d 10.0.0.2/32 ! -i docker0 -o docker0 -p tcp -m tcp --dport 5432 -j ACCEPT"}}
	}
	if e := layer(mustInvestigate(t, request, clean), "firewall"); e.State != "docker_admits" {
		t.Fatalf("with nothing ahead of Docker's accept it decides alone: %+v", e)
	}
	dropping := publishedProviders()
	dropping.Chains = func(context.Context, string) netsec.DockerChains {
		chains := clean.Chains(t.Context(), "inet")
		chains.User = []string{"-A DOCKER-USER -p tcp -m tcp --dport 5432 -j DROP", "-A DOCKER-USER -j RETURN"}
		return chains
	}
	if e := layer(mustInvestigate(t, request, dropping), "firewall"); e.State != "docker_admits_unless_earlier" || !strings.Contains(e.Summary, "DOCKER-USER") {
		t.Fatalf("an operator rule in DOCKER-USER qualifies Docker's accept: %+v", e)
	}
	if e := layer(result, "provider"); e.Basis != Unknown || !strings.Contains(e.Facts[0].Value, "provider's translation") {
		t.Fatalf("provider policy is always unknown: %+v", e)
	}
	if e := layer(result, "external"); e.Basis != Unknown {
		t.Fatalf("no measurement means unknown, not unreachable: %+v", e)
	}
	if !strings.Contains(result.Comparison, "Provider policy: unknown") {
		t.Fatalf("comparison: %s", result.Comparison)
	}

	p := publishedProviders()
	p.Chains = func(context.Context, string) netsec.DockerChains {
		return netsec.DockerChains{NAT: netsec.ParseDockerNAT("-A DOCKER ! -i docker0 -p tcp -m tcp --dport 5432 -j DNAT --to-destination 10.0.0.9:5432"),
			User: []string{"-A DOCKER-USER -s 198.51.100.0/24 -j DROP", "-A DOCKER-USER -j RETURN"}}
	}
	p.Gateway = func(context.Context) (*netx.GatewayView, error) {
		view := &netx.GatewayView{Forwards: []netx.ForwardView{{Name: "db", Protocol: "tcp", Interface: "eth0", Ports: "5000-5500", Target: "10.9.0.4", TargetPort: "5432", Enabled: true}}}
		view.Capability.Layers = []netx.PolicyLayer{{Family: "inet", Table: "crowdsec", Chain: "forward", Hook: "forward", Policy: "accept", Status: "unknown"}}
		return view, nil
	}
	at := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	p.External = func(context.Context, int) ([]ExternalMeasurement, error) {
		return []ExternalMeasurement{{Source: "Probe A", Placement: "external_host", Address: "203.0.113.9", State: "connected", Basis: "measured", At: at}}, nil
	}
	result, err = InvestigatePublished(t.Context(), request, p)
	if err != nil {
		t.Fatal(err)
	}
	if e := layer(result, "dnat"); e.State != "mismatch" {
		t.Fatalf("a translation to another address is a mismatch: %+v", e)
	}
	if e := layer(result, "firewall"); e.State == "docker_admits" {
		t.Fatalf("without FORWARD read, Docker's accept is not assumed: %+v", e)
	}
	if e := layer(result, "docker-user"); e.Basis != Unknown || e.State != "rules_present" || len(e.Facts) != 1 {
		t.Fatalf("operator rules are listed, not evaluated: %+v", e)
	}
	if e := layer(result, "gateway"); e.State != "competing" || e.Basis != Unknown {
		t.Fatalf("a competing gateway forward and a foreign forward chain: %+v", e)
	}
	external := layer(result, "external")
	if external.Basis != Measured || !strings.Contains(external.Facts[0].Value, "not on this host") {
		t.Fatalf("a measurement through a provider address says so: %+v", external)
	}
}

func TestInvestigatePublishedKeepsUnreadLayersUnknown(t *testing.T) {
	request := PublishedRequest{ContainerID: publishedID, HostPort: 5432, Protocol: "tcp", Family: "inet"}
	p := publishedProviders()
	p.Chains = func(context.Context, string) netsec.DockerChains {
		return netsec.DockerChains{NATError: "iptables: No chain/target/match by that name.", UserError: "permission denied"}
	}
	result, err := InvestigatePublished(t.Context(), request, p)
	if err != nil {
		t.Fatal(err)
	}
	if e := layer(result, "dnat"); e.Basis != Unknown || !strings.Contains(e.Summary, "nftables backend") {
		t.Fatalf("an unreadable nat chain is unknown, with why: %+v", e)
	}
	if e := layer(result, "docker-user"); e.Basis != Unknown || e.State != "unavailable" {
		t.Fatalf("an unreadable DOCKER-USER is unknown: %+v", e)
	}
	p = publishedProviders()
	p.Publication = func(context.Context) (Publication, error) { return Publication{}, errors.New("no such container") }
	result, err = InvestigatePublished(t.Context(), request, p)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range result.Evidence[1:] {
		if e.State != "skipped" {
			t.Fatalf("without the publication no later layer is assembled: %+v", e)
		}
	}
	if _, err := InvestigatePublished(t.Context(), PublishedRequest{ContainerID: "short", HostPort: 5432, Protocol: "tcp", Family: "inet"}, p); err == nil {
		t.Fatal("a short container ID is refused")
	}
	if _, err := InvestigatePublished(t.Context(), PublishedRequest{ContainerID: publishedID, HostPort: 0, Protocol: "sctp", Family: "inet"}, p); err == nil {
		t.Fatal("a port outside the closed shape is refused")
	}
}

func mustInvestigate(t *testing.T, request PublishedRequest, p PublishedProviders) *Result {
	t.Helper()
	result, err := InvestigatePublished(t.Context(), request, p)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
