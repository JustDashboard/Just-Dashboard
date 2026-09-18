package deploy

import (
	"context"
	"encoding/json"
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

func TestC6PreflightSurfacesNetworkFirewallAndDependencyGates(t *testing.T) {
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
			Strategy: StrategyStopFirst, BindAddress: "0.0.0.0", HostPort: 8443,
			Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{},
		},
		Variables: []PlannedVariable{}, Checks: []PlannedCheck{},
		Domains: []PlannedDomain{{Hostname: "app.example.test", HTTPS: true, Ownership: OwnershipManaged}},
		Dependencies: []PlannedDependency{
			{Kind: "database", Ownership: OwnershipLinked, ResourceKind: "database", ResourceID: "12", Config: json.RawMessage(`{}`)},
			{Kind: "storage", Ownership: OwnershipObserved, ResourceKind: "volume", ResourceID: "shared", Config: json.RawMessage(`{}`)},
			{Kind: "backup", Ownership: OwnershipLinked, ResourceKind: "backup_job", ResourceID: "42", Config: json.RawMessage(`{"maxAgeSeconds":3600}`)},
		},
	}
	observation := HostObservation{
		Facilities: map[string]FacilityObservation{"docker": {Available: true}},
		Ports:      []PortObservation{{Address: "0.0.0.0", Port: 8443, Protocol: "tcp", InUse: true, Detail: "claimed by another runtime"}},
		Domains: []DomainObservation{{
			Hostname: "app.example.test", ProxyAvailable: true, Conflict: true,
			DNSAvailable: false, CertificateAvailable: false, CertificateAutomation: true,
			Detail: "lookup timed out",
		}},
		Firewall: FirewallObservation{Available: true, Enabled: true, Allows: false, Backend: "ufw"},
		Dependencies: []DependencyObservation{
			{Kind: "database", ResourceKind: "database", ResourceID: "12", Available: false, Detail: "database is stopped", DeepLink: "/databases/12"},
			{Kind: "storage", ResourceKind: "volume", ResourceID: "shared", Available: false, Detail: "volume was not found", DeepLink: "/docker/volumes/shared"},
			{Kind: "backup", ResourceKind: "backup_job", ResourceID: "42", Available: true, Fresh: false, Status: "last success is stale", DeepLink: "/backups/42"},
		},
	}

	findings := preflightFindings(draft, configuration, observation, false)
	assertC6Finding(t, findings, "port_conflict", PreflightWarning, "")
	assertC6Finding(t, findings, "domain_conflict", PreflightBlocked, "/proxy/sites")
	assertC6Finding(t, findings, "dns_unverified", PreflightUnavailable, "/proxy/sites")
	// certbot on this host means the release itself will order the certificate,
	// so a name nobody has published before is reported rather than refused.
	assertC6Finding(t, findings, "certificate_automatic", PreflightPass, "/proxy/certificates")
	assertC6Finding(t, findings, "public_bind", PreflightWarning, "")
	assertC6Finding(t, findings, "firewall_mismatch", PreflightBlocked, "/security/firewall")
	assertC6Finding(t, findings, "database_unavailable", PreflightBlocked, "/databases/12")
	assertC6Finding(t, findings, "storage_unavailable", PreflightDecision, "/docker/volumes/shared")
	assertC6Finding(t, findings, "backup_stale", PreflightWarning, "/backups/42")
}

// Without certbot nothing on this host can produce a certificate, and a plan
// that cannot be activated should be refused while it is still a plan.
func TestPreflightBlocksHTTPSOnAHostThatCannotIssue(t *testing.T) {
	draft := &Draft{Data: DraftData{
		Intent:    &DraftIntentConfig{Name: "site", Profile: ProfileWeb},
		Source:    &DraftSourceConfig{Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://example.test/o/r.git", Ref: "main"},
		Detection: &DetectionResult{Source: SourceIdentity{Kind: SourceGit, Revision: strings.Repeat("a", 40)}},
	}}
	configuration := PlanConfiguration{
		Build:   BuildPlanConfig{Method: BuildNone, Secrets: []BuildSecretConfig{}, ReleaseTasks: []ReleaseTaskConfig{}},
		Runtime: RuntimePlanConfig{Strategy: StrategyStopFirst, Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{}},
		Domains: []PlannedDomain{{Hostname: "app.example.test", HTTPS: true, Ownership: OwnershipManaged}},
		Checks: []PlannedCheck{{
			Name: "readiness", Kind: string(CheckHTTP), Phase: "readiness", Required: true,
			Config: json.RawMessage(`{"path":"/"}`),
		}},
		Variables: []PlannedVariable{}, Dependencies: []PlannedDependency{},
	}
	observation := HostObservation{
		Facilities: map[string]FacilityObservation{"docker": {Available: true}},
		Domains: []DomainObservation{{
			Hostname: "app.example.test", ProxyAvailable: true, DNSAvailable: true, PointsHere: true,
			CertificateAvailable: false, CertificateAutomation: false,
		}},
	}
	findings := preflightFindings(draft, configuration, observation, false)
	assertC6Finding(t, findings, "certificate_unavailable", PreflightBlocked, "/proxy/certificates")
	for _, item := range findings {
		if item.Code == "certificate_automatic" {
			t.Fatalf("a host without certbot claimed automatic issuance: %#v", item)
		}
	}
}

func TestExecutionPreflightAllowsRemappablePortConflict(t *testing.T) {
	fixture := newReleaseStoreFixture(t)
	planRuntime := RuntimePlanConfig{
		InternalPort: 3000, HostPort: 18443, BindAddress: "127.0.0.1", Strategy: StrategyStopFirst,
		Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{},
	}
	fixture.addPlanWithRuntime(t, 1, strings.Repeat("a", 40), planRuntime)
	run, _ := fixture.claimedRun(t, 1)
	plan, err := fixture.runs.ExecutionPlan(context.Background(), *run)
	if err != nil {
		t.Fatal(err)
	}
	observer := &preflightObserverFake{observation: HostObservation{
		Facilities: map[string]FacilityObservation{
			"git": {Available: true}, "docker": {Available: true}, "buildx": {Available: true},
		},
		Ports: []PortObservation{{Address: "127.0.0.1", Port: 18443, Protocol: "tcp", InUse: true}},
	}}
	executor := &NormalizedStepExecutor{store: fixture.runs, preflight: observer}
	result := executor.analyzePlan(context.Background(), StepExecution{Run: *run}, plan)
	if result.State != StepFailed || result.ErrorCode != "readiness_missing" || observer.calls != 1 {
		t.Fatalf("execution preflight = %#v, observations=%d", result, observer.calls)
	}
	if len(observer.requests) != 1 || observer.requests[0].ExistingProxySite !=
		"just-dashboard-env-"+strconv.FormatInt(fixture.envID, 10)+".conf" {
		t.Fatalf("execution observation request = %#v", observer.requests)
	}
}

func TestExecutionObservationExcludesOnlyItsOwnManagedProxySiteFromConflicts(t *testing.T) {
	proxy := &planningProxyFake{vhosts: []proxysvc.VHost{{
		Name: "just-dashboard-env-12.conf", ServerNames: []string{"localhost"},
	}}}
	observer := NewHostPreflightObserver([]string{t.TempDir()}, t.TempDir(), nil, proxy)
	request := ObservationRequest{
		Domains:           []PlannedDomain{{Hostname: "localhost", Ownership: OwnershipManaged}},
		ExistingProxySite: "just-dashboard-env-12.conf",
	}
	observation, err := observer.Observe(context.Background(), request)
	if err != nil || len(observation.Domains) != 1 || observation.Domains[0].Conflict {
		t.Fatalf("own route observation = %#v, error=%v", observation.Domains, err)
	}
	proxy.vhosts = append(proxy.vhosts, proxysvc.VHost{Name: "operator-site.conf", ServerNames: []string{"localhost"}})
	observation, err = observer.Observe(context.Background(), request)
	if err != nil || len(observation.Domains) != 1 || !observation.Domains[0].Conflict {
		t.Fatalf("other route observation = %#v, error=%v", observation.Domains, err)
	}
}

func TestExecutionObservationRecognizesPortHeldByItsLiveContainer(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	docker := &planningDockerFake{container: &dockerx.ContainerSpec{Ports: []dockerx.PortMapping{{
		HostIP: "127.0.0.1", HostPort: port, ContainerPort: 3000, Protocol: "tcp",
	}}}}
	observer := NewHostPreflightObserver([]string{t.TempDir()}, t.TempDir(), docker)
	observation, err := observer.Observe(context.Background(), ObservationRequest{
		ExistingRuntimeID: "live-container", ExistingRuntimeKind: "container",
		Ports: []PortObservation{{Address: "127.0.0.1", Port: port, Protocol: "tcp"}},
	})
	if err != nil || len(observation.Ports) != 1 || observation.Ports[0].InUse ||
		!observation.Ports[0].OwnedByDeployment {
		t.Fatalf("live deployment port observation = %#v, error=%v", observation.Ports, err)
	}
}

func assertC6Finding(t *testing.T, findings []PreflightFinding, code string, severity PreflightSeverity, deepLink string) {
	t.Helper()
	for _, finding := range findings {
		if finding.Code != code || finding.Severity != severity {
			continue
		}
		if deepLink != "" && finding.DeepLink != deepLink {
			t.Fatalf("finding %s deep link = %q, want %q", code, finding.DeepLink, deepLink)
		}
		return
	}
	t.Fatalf("missing %s/%s finding in %#v", code, severity, findings)
}

// A managed named volume does not exist until the first release mounts it.
// Its absence is what a fresh deployment looks like, not a broken dependency;
// only a volume the deployment does not own, or one Docker could not inspect,
// keeps blocking.
func TestPreflightLetsAManagedVolumeBeCreatedOnFirstStart(t *testing.T) {
	candidate := newDetectedCandidate("", BuildImage, DetectedCandidate{
		Name: "redis", Profile: ProfileService, Confidence: ConfidenceHigh,
		Evidence: []DetectionEvidence{{Path: "redis@1.0.0", Reason: "fixture"}}, NeedsDecision: []string{},
	})
	draft := &Draft{Data: DraftData{
		Intent: &DraftIntentConfig{Name: "cache", Profile: ProfileService},
		Source: &DraftSourceConfig{Kind: SourceBlueprint, Mode: SourceModeBlueprint, BlueprintID: "redis", BlueprintVersion: "1.0.0"},
		Detection: &DetectionResult{
			Source:     SourceIdentity{Kind: SourceBlueprint, Repository: "redis:7.4-alpine", Revision: "sha256:" + strings.Repeat("a", 64), Digest: "sha256:" + strings.Repeat("b", 64)},
			Candidates: []DetectedCandidate{candidate}, SelectedID: candidate.ID,
		},
	}}
	configuration := PlanConfiguration{
		Build: BuildPlanConfig{Method: BuildImage},
		Runtime: RuntimePlanConfig{
			Image: "redis:7.4-alpine", Strategy: StrategyStopFirst, BindAddress: "127.0.0.1", InternalPort: 6379,
			Command: []string{}, Capabilities: []string{}, Devices: []string{},
			Mounts: []RuntimeMount{
				{Source: "cache-0123456789abcdef-data", Target: "/data", Ownership: OwnershipManaged},
				{Source: "shared-uploads", Target: "/uploads", Ownership: OwnershipLinked},
			},
		},
		Variables: []PlannedVariable{}, Checks: []PlannedCheck{}, Domains: []PlannedDomain{},
		Dependencies: []PlannedDependency{
			{Kind: "storage", Ownership: OwnershipManaged, ResourceKind: "docker_volume", ResourceID: "cache-0123456789abcdef-data", Config: json.RawMessage(`{}`)},
			{Kind: "storage", Ownership: OwnershipLinked, ResourceKind: "docker_volume", ResourceID: "shared-uploads", Config: json.RawMessage(`{}`)},
			{Kind: "storage", Ownership: OwnershipManaged, ResourceKind: "docker_volume", ResourceID: "cache-0123456789abcdef-logs", Config: json.RawMessage(`{}`)},
		},
	}
	observation := HostObservation{
		Facilities: map[string]FacilityObservation{"docker": {Available: true}},
		Dependencies: []DependencyObservation{
			{Kind: "storage", ResourceKind: "docker_volume", ResourceID: "cache-0123456789abcdef-data", Available: false, Missing: true, Detail: "Docker volume was not found", DeepLink: "/docker/volumes/cache-0123456789abcdef-data"},
			{Kind: "storage", ResourceKind: "docker_volume", ResourceID: "shared-uploads", Available: false, Missing: true, Detail: "Docker volume was not found", DeepLink: "/docker/volumes/shared-uploads"},
			{Kind: "storage", ResourceKind: "docker_volume", ResourceID: "cache-0123456789abcdef-logs", Available: false, Detail: "Docker volume could not be inspected", DeepLink: "/docker/volumes/cache-0123456789abcdef-logs"},
		},
	}
	findings := preflightFindings(draft, configuration, observation, false)
	assertC6Finding(t, findings, "storage_pending_creation", PreflightPass, "/docker/volumes/cache-0123456789abcdef-data")
	blocked := 0
	for _, item := range findings {
		if item.Code == "storage_unavailable" {
			blocked++
			if item.Severity != PreflightBlocked {
				t.Fatalf("%s should block: %#v", item.FieldID, item)
			}
		}
	}
	if blocked != 2 {
		t.Fatalf("storage_unavailable findings = %d, want the linked volume and the uninspectable one (%#v)", blocked, findings)
	}
}
