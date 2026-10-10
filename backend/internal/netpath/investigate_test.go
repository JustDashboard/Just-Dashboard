package netpath

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

func pathRequest() Request {
	return Request{SourceKind: "host", Target: "private.corp", Family: "inet", Protocol: "tcp", Port: 443, Measure: true}
}

func findEvidence(t *testing.T, r *Result, id string) Evidence {
	t.Helper()
	for _, e := range r.Evidence {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("missing %s: %#v", id, r.Evidence)
	return Evidence{}
}

func TestValidateBeforeOwnerRead(t *testing.T) {
	for _, change := range []func(*Request){
		func(r *Request) { r.Target = "--help" },
		func(r *Request) { r.Target = "$(id)" },
		func(r *Request) { r.SourceKind = "pid" },
		func(r *Request) { r.SourceKind = "container"; r.ContainerID = "short" },
		func(r *Request) { r.SourceAddress = "::1" },
		func(r *Request) { r.Mark = "0x1/0xff" },
		func(r *Request) { r.Port = 65536 },
		func(r *Request) { r.Protocol = "shell" },
	} {
		req := pathRequest()
		change(&req)
		_, err := Investigate(context.Background(), req, Providers{DNS: func(context.Context, Request) (DNSAnswer, error) {
			t.Fatal("invalid request reached an owner")
			return DNSAnswer{}, nil
		}})
		if err == nil {
			t.Fatalf("accepted %#v", req)
		}
	}
}

func TestPrivateDNSUnavailableStopsRouteAndProbe(t *testing.T) {
	called := 0
	r, err := Investigate(context.Background(), pathRequest(), Providers{
		DNS: func(context.Context, Request) (DNSAnswer, error) {
			return DNSAnswer{}, fmt.Errorf("native private suffix resolver is unavailable; no fallback")
		},
		Route: func(context.Context, Request) (netx.RouteLookup, error) { called++; return netx.RouteLookup{}, nil },
		Probe: func(context.Context, Request) (*netsec.ProbeResult, error) {
			called++
			return &netsec.ProbeResult{OK: true}, nil
		},
	})
	if err != nil || called != 0 || r.Scope.Address != "" || findEvidence(t, r, "dns").Basis != Unknown || findEvidence(t, r, "probe").State != "skipped" {
		t.Fatalf("fallback or false measurement: %#v %v calls=%d", r, err, called)
	}
}

func TestPinnedDNSAddressAndSourceSharedWithProbe(t *testing.T) {
	req := pathRequest()
	req.Address = "192.0.2.8"
	var routeReq, probeReq Request
	r, err := Investigate(context.Background(), req, Providers{
		DNS: func(context.Context, Request) (DNSAnswer, error) {
			return DNSAnswer{Addresses: []string{"192.0.2.7", "192.0.2.8", "::1"}, Owner: "native"}, nil
		},
		Route: func(_ context.Context, r Request) (netx.RouteLookup, error) {
			routeReq = r
			return netx.RouteLookup{Path: netx.Path{Address: r.Address, Source: "192.0.2.3", Device: "eth0"}, Table: 254}, nil
		},
		Probe: func(_ context.Context, r Request) (*netsec.ProbeResult, error) {
			probeReq = r
			return &netsec.ProbeResult{OK: true, Duration: "1ms"}, nil
		},
		Firewall: func(context.Context) (*netsec.FirewallStatus, error) {
			return &netsec.FirewallStatus{Backend: netsec.BackendUFW, Available: true, Enabled: true, Policy: netsec.DefaultPolicy{Outgoing: "deny"}}, nil
		},
	})
	if err != nil || routeReq.Address != req.Address || probeReq.Address != routeReq.Address || probeReq.SourceAddress != "192.0.2.3" || r.Scope.SourceAddress != probeReq.SourceAddress {
		t.Fatalf("tuple changed: %#v %#v %#v %v", r, routeReq, probeReq, err)
	}
	if e := findEvidence(t, r, "probe"); e.Basis != Measured || e.State != "connected" || !strings.Contains(e.Summary, "from this source at this time") {
		t.Fatalf("wrong measurement: %#v", e)
	}
	if !strings.Contains(r.Comparison, "prediction: deny") || !strings.Contains(r.Comparison, "remain unknown") || strings.Contains(r.Comparison, "Allowed") {
		t.Fatalf("model collapsed into measurement: %s", r.Comparison)
	}
	if findEvidence(t, r, "owner").Basis != Unknown {
		t.Fatal("remote ownership invented")
	}
}

func TestStaleChosenAddressDoesNotProbe(t *testing.T) {
	req := pathRequest()
	req.Address = "192.0.2.9"
	r, err := Investigate(context.Background(), req, Providers{DNS: func(context.Context, Request) (DNSAnswer, error) {
		return DNSAnswer{Addresses: []string{"192.0.2.1"}}, nil
	}, Route: func(context.Context, Request) (netx.RouteLookup, error) {
		t.Fatal("stale address reached route")
		return netx.RouteLookup{}, nil
	}})
	if err != nil || findEvidence(t, r, "dns").State != "unavailable" || r.Measurement != nil {
		t.Fatalf("stale DNS choice accepted: %#v %v", r, err)
	}
}

func TestUnsupportedTupleNeverSendsSubstituteProbe(t *testing.T) {
	for _, change := range []func(*Request){func(r *Request) { r.Protocol = "udp" }, func(r *Request) { r.Mark = "1" }} {
		req := pathRequest()
		req.Target = "192.0.2.1"
		change(&req)
		r, err := Investigate(context.Background(), req, Providers{Route: func(context.Context, Request) (netx.RouteLookup, error) {
			return netx.RouteLookup{Path: netx.Path{Source: "192.0.2.2"}}, nil
		}, Probe: func(context.Context, Request) (*netsec.ProbeResult, error) {
			t.Fatal("sent an unsupported substitute probe")
			return nil, nil
		}})
		if err != nil || findEvidence(t, r, "probe").State != "unsupported" || r.Measurement != nil {
			t.Fatalf("unsupported tuple measured: %#v %v", r, err)
		}
	}
}

func TestContainerLocalOwnerIsNotHostOwner(t *testing.T) {
	req := pathRequest()
	req.SourceKind = "container"
	req.ContainerID = strings.Repeat("a", 64)
	req.Target = "127.0.0.1"
	req.Measure = false
	r, err := Investigate(context.Background(), req, Providers{Route: func(context.Context, Request) (netx.RouteLookup, error) {
		return netx.RouteLookup{Path: netx.Path{Local: true, Device: "lo"}}, nil
	}, Owners: func(context.Context) (OwnerSnapshot, error) {
		t.Fatal("host owners queried for container loopback")
		return OwnerSnapshot{}, nil
	}})
	if err != nil || findEvidence(t, r, "owner").Basis != Unknown || r.Scope.Vantage != "container_network_namespace" {
		t.Fatalf("wrong source ownership %#v %v", r, err)
	}
}

func TestIPv6WildcardDoesNotInventIPv4Listener(t *testing.T) {
	req := pathRequest()
	req.Target = "127.0.0.1"
	req.Measure = false
	r, err := Investigate(context.Background(), req, Providers{Route: func(context.Context, Request) (netx.RouteLookup, error) {
		return netx.RouteLookup{Path: netx.Path{Local: true}}, nil
	}, Owners: func(context.Context) (OwnerSnapshot, error) {
		return OwnerSnapshot{Listeners: []proxysvc.Listener{{Protocol: "tcp", Port: 443, Address: "::", Process: "server"}}}, nil
	}})
	if err != nil || findEvidence(t, r, "owner").State != "unknown" || findEvidence(t, r, "owner").Basis != Modeled {
		t.Fatalf("dual-stack setting invented: %#v %v", r, err)
	}
}

func TestDNSCandidatesAreBounded(t *testing.T) {
	req := pathRequest()
	req.Measure = false
	values := []string{"::1", "invalid", "224.0.0.1", "0.0.0.0"}
	for i := 1; i <= 20; i++ {
		values = append(values, fmt.Sprintf("192.0.2.%d", i))
	}
	r, err := Investigate(context.Background(), req, Providers{DNS: func(context.Context, Request) (DNSAnswer, error) { return DNSAnswer{Addresses: values}, nil }})
	if err != nil || len(r.Addresses) != 8 || r.Scope.Address != "192.0.2.1" {
		t.Fatalf("unbounded/wrong family: %#v %v", r, err)
	}
}
