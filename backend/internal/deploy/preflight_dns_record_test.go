package deploy

import (
	"context"
	"strings"
	"testing"
)

// A name whose lookup does not reach this server is asked about once, and
// the answer becomes a warning beside the lookup's own: the record can be
// created from here (with the Domains settings' Create the record as its
// fix), or it is missing and has to be made where the zone is kept. A record
// the zone already holds says nothing more.
func TestPreflightNamesTheRecordADomainNeeds(t *testing.T) {
	draft := &Draft{Data: DraftData{
		Intent:    &DraftIntentConfig{Name: "site", Profile: ProfileWeb},
		Source:    &DraftSourceConfig{Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://example.test/o/r.git", Ref: "main"},
		Detection: &DetectionResult{Source: SourceIdentity{Kind: SourceGit, Revision: strings.Repeat("a", 40)}},
	}}
	domain := PlannedDomain{Hostname: "localhost", HTTPS: false, Ownership: OwnershipManaged}
	configuration := PlanConfiguration{
		Build:   BuildPlanConfig{Method: BuildNone, Secrets: []BuildSecretConfig{}, ReleaseTasks: []ReleaseTaskConfig{}},
		Runtime: RuntimePlanConfig{Strategy: StrategyStopFirst, Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{}},
		Domains: []PlannedDomain{domain}, Checks: []PlannedCheck{}, Variables: []PlannedVariable{}, Dependencies: []PlannedDependency{},
	}
	observe := func(answer DNSRecordObservation, said bool) ([]PreflightFinding, []string) {
		t.Helper()
		asked := []string{}
		observer := NewHostPreflightObserver([]string{t.TempDir()}, t.TempDir(), nil, ingressProxyFake{}).
			WithDNSRecords(func(_ context.Context, planned PlannedDomain) (DNSRecordObservation, bool) {
				asked = append(asked, planned.Hostname)
				return answer, said
			})
		// localhost resolves to loopback, which is never this server's
		// public address: the lookup's answer is "not here".
		observation, err := observer.Observe(context.Background(), ObservationRequest{Domains: []PlannedDomain{domain}})
		if err != nil || len(observation.Domains) != 1 {
			t.Fatalf("%#v %v", observation.Domains, err)
		}
		return preflightFindings(draft, configuration, observation, false), asked
	}

	findings, asked := observe(DNSRecordObservation{
		Records: []string{"A localhost → 203.0.113.7"}, Missing: false, Writable: true, Provider: "Cloudflare", Zone: "localhost",
	}, true)
	writable := findingByCode(findings, "dns_record_writable")
	if len(asked) != 1 || writable == nil || writable.Severity != PreflightWarning || writable.Title != "DNS record can be corrected from here" ||
		writable.Measured != "A localhost → 203.0.113.7" || !strings.Contains(writable.Means, "Cloudflare credentials saved under Certificates reach the zone localhost") ||
		writable.Fix == nil || writable.Fix.Kind != "dns_record" || writable.Fix.Field != "domains.localhost" || writable.Fix.Value != "localhost" {
		t.Fatalf("asked %v, writable %#v", asked, writable)
	}
	if findingByCode(findings, "dns_unverified") == nil {
		t.Fatal("the lookup's own finding is gone")
	}

	findings, _ = observe(DNSRecordObservation{
		Records: []string{"A localhost → 203.0.113.7", "AAAA localhost → 2001:db8::7"}, Missing: true,
		Detail: "None of the DNS credentials saved here reaches a zone that holds localhost.",
	}, true)
	missing := findingByCode(findings, "dns_record_missing")
	if missing == nil || missing.Title != "DNS record is missing" || missing.DeepLink != "/proxy/certificates" ||
		missing.Measured != "A localhost → 203.0.113.7; AAAA localhost → 2001:db8::7" || !strings.Contains(missing.Means, "None of the DNS credentials") ||
		findingByCode(findings, "dns_record_writable") != nil {
		t.Fatalf("missing %#v", missing)
	}

	for label, answer := range map[string]DNSRecordObservation{
		"already in the zone": {Records: []string{"A localhost → 203.0.113.7"}, Present: true, Writable: true},
		"nothing to write":    {},
	} {
		findings, _ = observe(answer, true)
		if findingByCode(findings, "dns_record_writable") != nil || findingByCode(findings, "dns_record_missing") != nil {
			t.Fatalf("%s: %#v", label, findings)
		}
	}
	findings, _ = observe(DNSRecordObservation{Records: []string{"A x → y"}}, false)
	if findingByCode(findings, "dns_record_missing") != nil {
		t.Fatal("an observer with nothing to say was taken at its word")
	}
}

// A name is asked about when it does not reach where it should: this
// server, or Cloudflare's edge for a tunnel's name. Behind Cloudflare's
// proxy it is already there; a tunnel's name that reaches this server still
// wants its CNAME.
func TestDNSRecordIsAskedOnlyWhenTheNameDoesNotReachWhereItShould(t *testing.T) {
	asked := 0
	observer := NewHostPreflightObserver(nil, "", nil).WithDNSRecords(func(context.Context, PlannedDomain) (DNSRecordObservation, bool) {
		asked++
		return DNSRecordObservation{Records: []string{"r"}}, true
	})
	direct := PlannedDomain{Hostname: "app.example.com"}
	tunnel := PlannedDomain{Hostname: "app.example.com", Tunnel: true}
	cases := []struct {
		planned                 PlannedDomain
		pointsHere, behindProxy bool
		asks                    bool
	}{
		{direct, false, false, true},
		{direct, true, false, false},
		{direct, false, true, false},
		{tunnel, false, false, true},
		{tunnel, true, false, true},
		{tunnel, false, true, false},
	}
	for _, c := range cases {
		asked = 0
		got := observer.observeDNSRecord(context.Background(), c.planned, c.pointsHere, c.behindProxy)
		if (got != nil) != c.asks || (asked == 1) != c.asks {
			t.Errorf("%+v here=%v proxy=%v: observed %v after %d asks", c.planned, c.pointsHere, c.behindProxy, got, asked)
		}
	}
	if NewHostPreflightObserver(nil, "", nil).observeDNSRecord(context.Background(), direct, false, false) != nil {
		t.Fatal("an observer with no DNS record half answered")
	}
	// A tunnel's name carries its record finding beside the tunnel's.
	record := &DNSRecordObservation{Records: []string{"CNAME app.example.com → abc.cfargotunnel.com (proxied)"}, Missing: true, Writable: true, Provider: "Cloudflare", Zone: "example.com"}
	draft := &Draft{Data: DraftData{
		Intent:    &DraftIntentConfig{Name: "site", Profile: ProfileWeb},
		Source:    &DraftSourceConfig{Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://example.test/o/r.git", Ref: "main"},
		Detection: &DetectionResult{Source: SourceIdentity{Kind: SourceGit, Revision: strings.Repeat("a", 40)}},
	}}
	configuration := PlanConfiguration{
		Build:   BuildPlanConfig{Method: BuildNone, Secrets: []BuildSecretConfig{}, ReleaseTasks: []ReleaseTaskConfig{}},
		Runtime: RuntimePlanConfig{Strategy: StrategyStopFirst, Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{}},
		Domains: []PlannedDomain{tunnel}, Checks: []PlannedCheck{}, Variables: []PlannedVariable{}, Dependencies: []PlannedDependency{},
	}
	findings := preflightFindings(draft, configuration, HostObservation{
		Domains: []DomainObservation{{Hostname: "app.example.com", Record: record}},
		Tunnel:  &TunnelObservation{Name: "production", Connected: true},
	}, false)
	if findingByCode(findings, "tunnel_domain") == nil || findingByCode(findings, "dns_record_writable") == nil {
		t.Fatalf("the tunnel's name: %#v", findings)
	}
}
