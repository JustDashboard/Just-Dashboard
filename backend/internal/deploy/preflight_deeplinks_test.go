package deploy

import "testing"

// A finding's link lands on the thing it names: the Sites list filtered to
// the hostname a linked route should have answered, and the Certificates tab
// where a DNS provider's credentials are saved for a challenge over DNS.
func TestPreflightDeepLinksLandOnTheThing(t *testing.T) {
	candidate := newDetectedCandidate("", BuildNone, DetectedCandidate{
		Name: "existing", Profile: ProfileImported, Confidence: ConfidenceHigh,
		Evidence: []DetectionEvidence{}, NeedsDecision: []string{},
	})
	draft := &Draft{Data: DraftData{
		Intent: &DraftIntentConfig{Name: "existing", Profile: ProfileImported},
		Source: &DraftSourceConfig{Kind: SourceImport, Mode: SourceModeExistingContainer, ResourceID: "runtime-1"},
		Detection: &DetectionResult{
			Source:     SourceIdentity{Kind: SourceImport, Repository: "runtime-1"},
			Candidates: []DetectedCandidate{candidate}, SelectedID: candidate.ID,
		},
	}}
	configuration := PlanConfiguration{
		Build: BuildPlanConfig{Method: BuildNone},
		Runtime: RuntimePlanConfig{
			Strategy: StrategyStopFirst, BindAddress: "127.0.0.1", HostPort: 8443,
			Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{},
		},
		Variables: []PlannedVariable{}, Checks: []PlannedCheck{}, Dependencies: []PlannedDependency{},
		Domains: []PlannedDomain{
			{Hostname: "linked.example.test", HTTPS: false, Ownership: OwnershipLinked},
			{Hostname: "*.wild.example.test", HTTPS: true, Ownership: OwnershipManaged},
			{Hostname: "*.dns.example.test", HTTPS: true, Ownership: OwnershipManaged},
		},
	}
	observation := HostObservation{
		Facilities: map[string]FacilityObservation{"docker": {Available: true}},
		Domains: []DomainObservation{
			{Hostname: "linked.example.test", ProxyAvailable: true, DNSAvailable: true, PointsHere: true},
			{Hostname: "*.wild.example.test", ProxyAvailable: true, DNSAvailable: true, PointsHere: true,
				CertificateRefusal: "Save a DNS provider's API token under Certificates."},
			{Hostname: "*.dns.example.test", ProxyAvailable: true, DNSAvailable: true, PointsHere: true,
				CertificateDNSProvider: "Cloudflare"},
		},
	}
	findings := preflightFindings(draft, configuration, observation, false)
	assertC6Finding(t, findings, "domain_link_missing", PreflightDecision, "/proxy/sites?q=linked.example.test")
	assertC6Finding(t, findings, "certificate_wildcard_unavailable", PreflightBlocked, "/proxy/certificates?tab=dns")
	assertC6Finding(t, findings, "certificate_automatic", PreflightPass, "/proxy/certificates?tab=dns")
}
