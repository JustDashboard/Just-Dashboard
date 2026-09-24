package deploy

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

type countingRuntimeObserver struct {
	calls      atomic.Int64
	containers []dockerx.Container
	err        error
}

func (o *countingRuntimeObserver) ListContainersWithLabels(
	_ context.Context,
	_ map[string]string,
) ([]dockerx.Container, error) {
	o.calls.Add(1)
	if o.err != nil {
		return nil, o.err
	}
	return o.containers, nil
}

type countingProxyObserver struct {
	availabilityCalls atomic.Int64
	vhostCalls        atomic.Int64
	certificateCalls  atomic.Int64
	availability      proxysvc.Availability
	vhosts            []proxysvc.VHost
	certificates      []proxysvc.Certificate
	vhostErr          error
	certificateErr    error
}

func (o *countingProxyObserver) Availability(context.Context) proxysvc.Availability {
	o.availabilityCalls.Add(1)
	return o.availability
}

func (o *countingProxyObserver) ListVHosts(context.Context) ([]proxysvc.VHost, error) {
	o.vhostCalls.Add(1)
	return o.vhosts, o.vhostErr
}

func (o *countingProxyObserver) ListCertificates(context.Context) ([]proxysvc.Certificate, error) {
	o.certificateCalls.Add(1)
	return o.certificates, o.certificateErr
}

type countingDependencyObserver struct {
	calls     atomic.Int64
	requested [][]PlannedDependency
	byKey     map[string]DependencyObservation
	err       error
}

func (o *countingDependencyObserver) ObserveDependencies(
	_ context.Context,
	dependencies []PlannedDependency,
) ([]DependencyObservation, error) {
	o.calls.Add(1)
	o.requested = append(o.requested, dependencies)
	if o.err != nil {
		return nil, o.err
	}
	result := make([]DependencyObservation, 0, len(dependencies))
	for _, dependency := range dependencies {
		observation, found := o.byKey[dependencyKey(dependency.ResourceKind, dependency.ResourceID)]
		if !found {
			observation = DependencyObservation{
				Kind: dependency.Kind, ResourceKind: dependency.ResourceKind,
				ResourceID: dependency.ResourceID, Detail: "not found",
			}
		}
		observation.Kind = dependency.Kind
		observation.ResourceKind, observation.ResourceID = dependency.ResourceKind, dependency.ResourceID
		result = append(result, observation)
	}
	return result, nil
}

func operationsSnapshot() json.RawMessage {
	snapshot := map[string]any{
		"version": 1,
		"plan": map[string]any{
			"strategy": "blue_green", "internalPort": 3000, "bindAddress": "127.0.0.1",
			"mounts": []map[string]any{
				{"source": "app-data", "target": "/data", "ownership": "managed"},
				{"source": "/srv/release-fixture/media", "target": "/media", "readOnly": true, "ownership": "linked"},
			},
		},
		"image": map[string]any{
			"reference": "example.test/app:v1", "digest": fakeContentDigest("operations-image"),
		},
		"variables": []map[string]any{},
		"dependencies": []map[string]any{
			{"kind": "backup", "ownership": "managed", "resourceKind": "backup_job", "resourceId": "4",
				"config": map[string]any{"requiredBeforeDeploy": true, "maxAgeSeconds": 86400}},
			{"kind": "database", "ownership": "linked", "resourceKind": "database_connection", "resourceId": "2"},
		},
		"checks": []map[string]any{},
		"domains": []map[string]any{
			{"hostname": "app.example.test", "https": true, "ownership": "managed"},
		},
		"planInputsDigest": "sha256:" + strings.Repeat("0", 64),
		"sourceIdentity":   map[string]any{"kind": "local", "revision": strings.Repeat("a", 40)},
	}
	return mustJSON(snapshot)
}

// liveOperationsFixture creates one live release whose snapshot names a volume,
// a bind path, a domain, a backup job and a database connection.
func liveOperationsFixture(t *testing.T) (*releaseStoreFixture, *ReleaseWithArtifacts) {
	t.Helper()
	fixture := newReleaseStoreFixture(t)
	fixture.addPlanWithRuntime(t, 1, strings.Repeat("a", 40), RuntimePlanConfig{
		InternalPort: 3000, BindAddress: "127.0.0.1", Strategy: StrategyBlueGreen,
		Command: []string{}, Capabilities: []string{}, Devices: []string{},
		Mounts: []RuntimeMount{
			{Source: "app-data", Target: "/data", Ownership: OwnershipManaged},
			{Source: "/srv/release-fixture/media", Target: "/media", ReadOnly: true, Ownership: OwnershipLinked},
		},
	})
	run, lease := fixture.claimedRun(t, 1)
	snapshot := operationsSnapshot()
	release, err := fixture.runs.CreateCandidateRelease(context.Background(), *run, lease.Token, CandidateReleaseInput{
		Artifacts: []ReleaseArtifactInput{{
			Kind: ArtifactImage, Reference: "example.test/app:v1", Digest: fakeContentDigest("operations-image"),
			Metadata: json.RawMessage(`{"os":"linux","architecture":"amd64"}`), SizeBytes: 4096,
		}},
		Prepared: PreparedBuild{
			Method: BuildDockerfile, Dockerfile: "Dockerfile", DockerfileDigest: fakeContentDigest("Dockerfile"),
			BuildArgv: []string{"docker", "buildx", "build", "."}, BaseImages: []ResolvedImage{},
			CachePolicy: "reuse", SecretIDs: []string{},
		},
		RuntimeSnapshot: snapshot, RuntimeDigest: digestBytes(snapshot),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.base.DB.Exec(`
		INSERT INTO deploy_release_runtimes(
		  release_id, environment_id, kind, runtime_id, name, working_directory,
		  host, port, state, metadata_json, created_at, updated_at)
		VALUES(?, ?, 'container', 'c0ffee0000ff', 'jd-e1-r1', '', '127.0.0.1', 31001, 'live', '{}', ?, ?)`,
		release.Release.ID, fixture.envID, fixture.now.Unix(), fixture.now.Unix()); err != nil {
		t.Fatal(err)
	}
	fixture.finishCandidate(t, release.Release.ID, run.ID, lease.Token)
	return fixture, release
}

func (f *releaseStoreFixture) deploymentSummary(t *testing.T) DeploymentSummary {
	t.Helper()
	summary, err := f.runs.DeploymentSummary(context.Background(), f.projectID, QueueBudget{Heavy: 1, Light: 2})
	if err != nil {
		t.Fatal(err)
	}
	return *summary
}

func healthyOperationsOwners(environmentID int64) (OperationsOwners, *countingDependencyObserver, *countingProxyObserver) {
	dependencies := &countingDependencyObserver{byKey: map[string]DependencyObservation{
		dependencyKey("docker_volume", "app-data"):               {Available: true, Status: "available", DeepLink: "/docker/volumes/app-data"},
		dependencyKey("bind_path", "/srv/release-fixture/media"): {Available: true, Status: "available"},
		dependencyKey("backup_job", "4"):                         {Available: true, Status: "success", Fresh: true, DeepLink: "/backups"},
		dependencyKey("database_connection", "2"):                {Available: true, Status: "primary", DeepLink: "/databases/2"},
	}}
	proxy := &countingProxyObserver{
		availability: proxysvc.Availability{Nginx: true, Certbot: true},
		vhosts: []proxysvc.VHost{{
			Name: deploymentRouteName(environmentID), Enabled: true,
			ServerNames: []string{"app.example.test"}, TLS: true,
		}},
		certificates: []proxysvc.Certificate{{
			Name: "app.example.test", Domains: []string{"app.example.test"}, DaysLeft: 70, Issuer: "R10",
		}},
	}
	runtime := &countingRuntimeObserver{containers: []dockerx.Container{{
		ID: "c0ffee0000ff", Name: "jd-e1-r1", State: "running", Health: "healthy", Image: "example.test/app:v1",
		Labels: map[string]string{
			"io.just-dashboard.managed":        "true",
			"io.just-dashboard.environment-id": "1",
			"io.just-dashboard.release-id":     "1",
		},
	}}}
	return OperationsOwners{
		Runtime: runtime, Proxy: proxy, Certificates: proxy, Dependencies: dependencies,
	}, dependencies, proxy
}

func TestOperationsSummaryReadsEveryOwnerOnceAndDiagnosesNothing(t *testing.T) {
	t.Parallel()
	fixture, release := liveOperationsFixture(t)
	owners, dependencies, proxy := healthyOperationsOwners(fixture.envID)
	summary := fixture.deploymentSummary(t)
	operations, err := fixture.runs.Operations(context.Background(), owners, summary)
	if err != nil {
		t.Fatal(err)
	}
	if operations.Evidence != "release" || operations.ReleaseID != release.Release.ID {
		t.Fatalf("operations evidence = %q release %d", operations.Evidence, operations.ReleaseID)
	}
	if operations.Domains.Status != statusAvailable || len(operations.Domains.Domains) != 1 {
		t.Fatalf("domains = %#v", operations.Domains)
	}
	domain := operations.Domains.Domains[0]
	if domain.Route != "served" || domain.Certificate != "valid" || domain.CertificateIssuer != "R10" ||
		domain.ServedBy != deploymentRouteName(fixture.envID) ||
		domain.DeepLink != "/proxy/sites?site="+deploymentRouteName(fixture.envID) {
		t.Fatalf("domain route = %#v", domain)
	}
	if len(operations.Runtime.Services) != 1 || operations.Runtime.Services[0].Image != "example.test/app:v1" {
		t.Fatalf("runtime services = %#v", operations.Runtime.Services)
	}
	if operations.Storage.Status != statusAvailable || len(operations.Storage.Mounts) != 2 {
		t.Fatalf("storage = %#v", operations.Storage)
	}
	if operations.Storage.Mounts[0].Kind != "volume" || operations.Storage.Mounts[0].Status != "present" ||
		operations.Storage.Mounts[0].DeepLink != "/docker/volumes?volume=app-data" {
		t.Fatalf("volume mount = %#v", operations.Storage.Mounts[0])
	}
	if operations.Storage.Mounts[1].Kind != "bind" || operations.Storage.Mounts[1].Status != "present" {
		t.Fatalf("bind mount = %#v", operations.Storage.Mounts[1])
	}
	if operations.Backups.Status != statusAvailable || len(operations.Backups.Jobs) != 1 ||
		!operations.Backups.Jobs[0].Required || !operations.Backups.Jobs[0].Fresh {
		t.Fatalf("backups = %#v", operations.Backups)
	}
	if operations.Dependencies.Status != statusAvailable || len(operations.Dependencies.Items) != 1 ||
		operations.Dependencies.Items[0].ResourceKind != "database_connection" {
		t.Fatalf("dependencies = %#v", operations.Dependencies)
	}
	if len(operations.Diagnosis.Findings) != 0 || len(operations.Diagnosis.Silences) != 0 {
		t.Fatalf("healthy operations diagnosed %v / %v",
			findingCodes(operations.Diagnosis), silenceSubjects(operations.Diagnosis))
	}
	// Four resources, one inventory call. Rendering four rows must not become
	// four reads of the owning module.
	if got := dependencies.calls.Load(); got != 1 {
		t.Fatalf("dependency owner called %d times, want 1", got)
	}
	if len(dependencies.requested) != 1 || len(dependencies.requested[0]) != 4 {
		t.Fatalf("dependency batch = %#v", dependencies.requested)
	}
	if proxy.vhostCalls.Load() != 1 || proxy.certificateCalls.Load() != 1 || proxy.availabilityCalls.Load() != 1 {
		t.Fatalf("proxy owner calls = %d vhosts, %d certificates, %d availability",
			proxy.vhostCalls.Load(), proxy.certificateCalls.Load(), proxy.availabilityCalls.Load())
	}
}

// A host without Docker, nginx or Backups must render unavailable evidence with
// a reason, never an empty section that reads as success.
func TestOperationsSummaryRendersUnavailableEvidenceForEveryAbsentModule(t *testing.T) {
	t.Parallel()
	fixture, _ := liveOperationsFixture(t)
	summary := fixture.deploymentSummary(t)
	operations, err := fixture.runs.Operations(context.Background(), OperationsOwners{}, summary)
	if err != nil {
		t.Fatal(err)
	}
	for name, section := range map[string][2]string{
		"runtime":      {operations.Runtime.Status, operations.Runtime.Reason},
		"domains":      {operations.Domains.Status, operations.Domains.Reason},
		"storage":      {operations.Storage.Status, operations.Storage.Reason},
		"backups":      {operations.Backups.Status, operations.Backups.Reason},
		"dependencies": {operations.Dependencies.Status, operations.Dependencies.Reason},
	} {
		if section[0] != statusUnavailable || strings.TrimSpace(section[1]) == "" {
			t.Fatalf("%s section = status %q reason %q, want unavailable with a reason", name, section[0], section[1])
		}
	}
	if len(operations.Diagnosis.Findings) != 0 {
		t.Fatalf("absent modules produced claims %v", findingCodes(operations.Diagnosis))
	}
	if operations.Diagnosis.Status != "partial" || len(operations.Diagnosis.Silences) == 0 {
		t.Fatalf("diagnosis = %#v", operations.Diagnosis)
	}
	if operations.Domains.SiteName != deploymentRouteName(fixture.envID) {
		t.Fatalf("site name = %q", operations.Domains.SiteName)
	}
}

func TestOperationsSummaryReportsOwnerObservedFailuresAsDiagnosableFindings(t *testing.T) {
	t.Parallel()
	fixture, _ := liveOperationsFixture(t)
	owners, dependencies, proxy := healthyOperationsOwners(fixture.envID)
	dependencies.byKey[dependencyKey("docker_volume", "app-data")] = DependencyObservation{Detail: "Docker volume was not found"}
	dependencies.byKey[dependencyKey("backup_job", "4")] = DependencyObservation{Available: true, Status: "failed"}
	proxy.certificates = []proxysvc.Certificate{{
		Name: "app.example.test", Domains: []string{"app.example.test"}, Expired: true,
	}}
	operations, err := fixture.runs.Operations(context.Background(), owners, fixture.deploymentSummary(t))
	if err != nil {
		t.Fatal(err)
	}
	codes := findingCodes(operations.Diagnosis)
	for _, wanted := range []string{"storage_missing", "certificate_expired", "backup_stale"} {
		if !contains(codes, wanted) {
			t.Fatalf("expected %q in %v", wanted, codes)
		}
	}
	if operations.Diagnosis.Status != "assessed" {
		t.Fatalf("status = %q, want assessed: every owner answered", operations.Diagnosis.Status)
	}
}

// Another site claiming the deployment's hostname is a critical finding, and it
// must name the site an operator has to open.
func TestOperationsSummaryDetectsForeignProxyOwnershipOfADeploymentDomain(t *testing.T) {
	t.Parallel()
	fixture, _ := liveOperationsFixture(t)
	owners, _, proxy := healthyOperationsOwners(fixture.envID)
	proxy.vhosts = []proxysvc.VHost{{
		Name: "legacy.conf", Enabled: true, ServerNames: []string{"app.example.test"},
	}}
	operations, err := fixture.runs.Operations(context.Background(), owners, fixture.deploymentSummary(t))
	if err != nil {
		t.Fatal(err)
	}
	if operations.Domains.Domains[0].Route != "foreign" || operations.Domains.Domains[0].ServedBy != "legacy.conf" {
		t.Fatalf("domain = %#v", operations.Domains.Domains[0])
	}
	if !contains(findingCodes(operations.Diagnosis), "domain_foreign_route") {
		t.Fatalf("findings = %v", findingCodes(operations.Diagnosis))
	}
}

func TestOperationsSummaryForUndeployedProjectNamesItsMissingReleaseInsteadOfGuessing(t *testing.T) {
	t.Parallel()
	fixture := newReleaseStoreFixture(t)
	fixture.addPlan(t, 1, strings.Repeat("a", 40))
	owners, dependencies, proxy := healthyOperationsOwners(fixture.envID)
	operations, err := fixture.runs.Operations(context.Background(), owners, fixture.deploymentSummary(t))
	if err != nil {
		t.Fatal(err)
	}
	if operations.Evidence != "none" || operations.ReleaseID != 0 || strings.TrimSpace(operations.Reason) == "" {
		t.Fatalf("operations = %#v", operations)
	}
	if dependencies.calls.Load() != 0 || proxy.vhostCalls.Load() != 0 {
		t.Fatalf("undeployed project read owners: %d dependency, %d proxy",
			dependencies.calls.Load(), proxy.vhostCalls.Load())
	}
	if len(operations.Diagnosis.Findings) != 0 {
		t.Fatalf("undeployed project produced claims %v", findingCodes(operations.Diagnosis))
	}
}

func TestReleaseComparisonNamesEveryChangedFieldAndHidesVariableValues(t *testing.T) {
	t.Parallel()
	fixture, first := liveOperationsFixture(t)
	fixture.now = fixture.now.Add(time.Minute)
	fixture.addPlanWithRuntime(t, 2, strings.Repeat("b", 40), RuntimePlanConfig{
		InternalPort: 8080, BindAddress: "127.0.0.1", Strategy: StrategyStopFirst,
		Command: []string{"serve", "--port", "8080"}, Capabilities: []string{}, Devices: []string{},
		Mounts: []RuntimeMount{{Source: "app-data", Target: "/data", Ownership: OwnershipManaged}},
	})
	run, lease := fixture.claimedRun(t, 2)
	second := map[string]any{
		"version": 1,
		"plan": map[string]any{
			"strategy": "stop_first", "internalPort": 8080, "command": []string{"serve", "--port", "8080"},
			"mounts": []map[string]any{{"source": "app-data", "target": "/data", "ownership": "managed"}},
		},
		"variables": []map[string]any{
			{"name": "TOKEN", "sensitivity": "secret", "scopes": "runtime", "valueDigest": fakeContentDigest("rotated")},
		},
		"dependencies": []map[string]any{
			{"kind": "database", "ownership": "linked", "resourceKind": "database_connection", "resourceId": "2"},
		},
		"checks": []map[string]any{
			{"name": "ready", "kind": "http", "phase": "readiness", "required": true},
		},
		"image": map[string]any{
			"reference": "example.test/app:v2", "digest": fakeContentDigest("operations-image-two"),
		},
		"domains":          []map[string]any{{"hostname": "app.example.test", "https": true, "ownership": "managed"}},
		"planInputsDigest": "sha256:" + strings.Repeat("1", 64),
		"sourceIdentity":   map[string]any{"kind": "local", "revision": strings.Repeat("b", 40)},
	}
	snapshot := mustJSON(second)
	next, err := fixture.runs.CreateCandidateRelease(context.Background(), *run, lease.Token, CandidateReleaseInput{
		Artifacts: []ReleaseArtifactInput{{
			Kind: ArtifactImage, Reference: "example.test/app:v2", Digest: fakeContentDigest("operations-image-two"),
			Metadata: json.RawMessage(`{"os":"linux","architecture":"amd64"}`), SizeBytes: 5120,
		}},
		Prepared: PreparedBuild{
			Method: BuildDockerfile, Dockerfile: "Dockerfile", DockerfileDigest: fakeContentDigest("Dockerfile"),
			BuildArgv: []string{"docker", "buildx", "build", "."}, BaseImages: []ResolvedImage{},
			CachePolicy: "reuse", SecretIDs: []string{},
		},
		RuntimeSnapshot: snapshot, RuntimeDigest: digestBytes(snapshot),
	})
	if err != nil {
		t.Fatal(err)
	}
	comparison, err := fixture.runs.CompareReleaseDetail(context.Background(), first.Release.ID, next.Release.ID)
	if err != nil {
		t.Fatal(err)
	}
	if comparison.Detail.Status != statusAvailable {
		t.Fatalf("detail = %#v", comparison.Detail)
	}
	changed := map[string]bool{}
	for _, field := range comparison.Detail.Fields {
		changed[field.Field] = field.Changed
	}
	for _, field := range []string{"source revision", "image", "command", "internal port", "strategy", "persistent storage"} {
		if !changed[field] {
			t.Fatalf("%q reported unchanged: %#v", field, comparison.Detail.Fields)
		}
	}
	if len(comparison.Detail.Variables) != 1 || comparison.Detail.Variables[0].Name != "TOKEN" ||
		comparison.Detail.Variables[0].Change != "added" || !comparison.Detail.Variables[0].Secret {
		t.Fatalf("variables = %#v", comparison.Detail.Variables)
	}
	for _, variable := range comparison.Detail.Variables {
		if strings.Contains(variable.To, "rotated") || strings.Contains(variable.From, "rotated") {
			t.Fatalf("variable comparison leaked a value: %#v", variable)
		}
	}
	backupRemoved := false
	for _, dependency := range comparison.Detail.Dependencies {
		if dependency.Name == "backup_job 4" && dependency.Change == "removed" {
			backupRemoved = true
		}
	}
	if !backupRemoved {
		t.Fatalf("dependencies = %#v", comparison.Detail.Dependencies)
	}
	if len(comparison.Detail.Checks) != 1 || comparison.Detail.Checks[0].Change != "added" {
		t.Fatalf("checks = %#v", comparison.Detail.Checks)
	}
	if len(comparison.Detail.Domains) != 1 || comparison.Detail.Domains[0].Change != "unchanged" {
		t.Fatalf("domains = %#v", comparison.Detail.Domains)
	}
	if len(comparison.Artifacts) == 0 {
		t.Fatal("comparison reported no artifact retention status")
	}
	for _, artifact := range comparison.Artifacts {
		if strings.TrimSpace(artifact.Reason) == "" {
			t.Fatalf("artifact %s carries no retention reason", artifact.Reference)
		}
	}
}

type staticImageUpdates struct {
	calls  atomic.Int64
	result ImageUpdateResult
}

func (u *staticImageUpdates) CheckUpdate(_ context.Context, reference string, _ bool) ImageUpdateResult {
	u.calls.Add(1)
	u.result.Ref = reference
	return u.result
}

func TestReleaseUpdateStatusUsesTheRecordedReferenceAndDegradesHonestly(t *testing.T) {
	t.Parallel()
	fixture, release := liveOperationsFixture(t)
	unavailable, err := fixture.runs.ReleaseUpdate(context.Background(), nil, release.Release.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unavailable.Status != statusUnavailable || strings.TrimSpace(unavailable.Reason) == "" {
		t.Fatalf("update without Docker = %#v", unavailable)
	}
	// The recorded reference is the tag the release was resolved from, with any
	// pinned digest stripped: the registry is asked about that tag, never about
	// the immutable digest the release already owns.
	updates := &staticImageUpdates{result: ImageUpdateResult{
		State: "outdated", LocalDigest: "sha256:local", RemoteDigest: "sha256:remote",
	}}
	outdated, err := fixture.runs.ReleaseUpdate(context.Background(), updates, release.Release.ID)
	if err != nil {
		t.Fatal(err)
	}
	if outdated.Status != statusAvailable || outdated.State != "outdated" ||
		outdated.Reference != "example.test/app:v1" || updates.calls.Load() != 1 {
		t.Fatalf("update = %#v after %d registry calls", outdated, updates.calls.Load())
	}
}
