package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// mediaPlan is a media stack's plan: Jellyfin the primary service, Seerr
// and Radarr each at an address of their own.
func mediaPlan() PlanConfiguration {
	return PlanConfiguration{
		Build: BuildPlanConfig{Method: BuildCompose, PrimaryService: "jellyfin", Secrets: []BuildSecretConfig{}, ReleaseTasks: []ReleaseTaskConfig{}},
		Runtime: RuntimePlanConfig{InternalPort: 8096, Strategy: StrategyStopFirst, BindAddress: "127.0.0.1",
			Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{}, Ports: []PublishedPort{}},
		Variables: []PlannedVariable{}, Dependencies: []PlannedDependency{}, Checks: []PlannedCheck{},
		Domains: []PlannedDomain{
			{Hostname: "media.example.test", HTTPS: true, Ownership: OwnershipManaged},
			{Hostname: "media-requests.example.test", HTTPS: true, Ownership: OwnershipManaged, Service: "seerr"},
			{Hostname: "media-radarr.example.test", HTTPS: true, Ownership: OwnershipManaged, Service: "radarr", Port: 7878},
		},
	}
}

// mediaAnalysis is the stack's Compose analysis as detection keeps it.
func mediaAnalysis() *ComposeAnalysis {
	return &ComposeAnalysis{PrimaryService: "jellyfin", Services: []ComposeServicePlan{
		{Name: "jellyfin", Ports: []string{"127.0.0.1:30080:8096"}},
		{Name: "prowlarr", Ports: []string{"127.0.0.1:30083:9696", "127.0.0.1:30084:9697"}},
		{Name: "qbittorrent", Ports: []string{"6881:6881", "127.0.0.1:30085:8080/udp"}},
		{Name: "radarr", Ports: []string{"127.0.0.1:30082:7878"}},
		{Name: "seerr", Ports: []string{"127.0.0.1::5055"}},
		{Name: "postgres"},
	}}
}

func TestServiceDomainsValidateWithThePlan(t *testing.T) {
	t.Parallel()
	if err := canonicalConfiguration(mediaPlan()).Validate(); err != nil {
		t.Fatalf("a media stack's plan was refused: %v", err)
	}
	// A service domain keeps its tunnel and its password: the route's
	// rules are the route's, whichever service answers.
	plan := mediaPlan()
	plan.Domains[1].Tunnel = true
	plan.Domains[2].Protection = &DomainProtection{Username: "admin", Hash: "$2a$10$" + strings.Repeat("a", 53)}
	if err := canonicalConfiguration(plan).Validate(); err != nil {
		t.Fatalf("a tunnelled or protected service domain was refused: %v", err)
	}
	for _, fixture := range []struct {
		name  string
		edit  func(*PlanConfiguration)
		field string
	}{
		{"outside a Compose plan", func(p *PlanConfiguration) {
			p.Build = BuildPlanConfig{Method: BuildImage, Secrets: []BuildSecretConfig{}, ReleaseTasks: []ReleaseTaskConfig{}}
			p.Runtime.Image = "nginx:alpine"
		}, "domains[0].service"},
		{"a name Compose would refuse", func(p *PlanConfiguration) { p.Domains[1].Service = "seerr app" }, "domains[1].service"},
		{"a port out of range", func(p *PlanConfiguration) { p.Domains[2].Port = 70000 }, "domains[0].port"},
		{"a redirecting name", func(p *PlanConfiguration) {
			p.Domains[1].RedirectTo = "media.example.test"
		}, "domains[1].service"},
		{"a wildcard", func(p *PlanConfiguration) { p.Domains[1].Hostname = "*.requests.example.test" }, "domains[0].service"},
		{"too many services", func(p *PlanConfiguration) {
			for index := range 16 {
				p.Domains = append(p.Domains, PlannedDomain{Hostname: "s" + strconv.Itoa(index) + ".example.test", Ownership: OwnershipManaged, Service: "s" + strconv.Itoa(index)})
			}
		}, "domains"},
	} {
		plan := mediaPlan()
		fixture.edit(&plan)
		err := canonicalConfiguration(plan).Validate()
		var validation *ValidationError
		if !errors.As(err, &validation) || validation.Field != fixture.field {
			t.Errorf("%s: %v", fixture.name, err)
		}
	}
	// A tailnet-only environment has no public domain at all.
	tailnet := mediaPlan()
	tailnet.Runtime.Tailnet = &RouteTailnet{}
	if err := canonicalConfiguration(tailnet).Validate(); err == nil || !strings.Contains(err.Error(), "tailnet") {
		t.Fatalf("a service domain under a tailnet-only environment: %v", err)
	}
}

// A domain naming the primary service, or its port, is the domain every
// other one is, with the bytes and the digest of one; a service domain's row
// carries its service and port.
func TestServiceDomainsNormaliseToThePrimaryAndKeepOtherRowsBytes(t *testing.T) {
	t.Parallel()
	plan := mediaPlan()
	plan.Domains[0].Service, plan.Domains[0].Port = " jellyfin ", 8096
	canonical := canonicalConfiguration(plan)
	var primary PlannedDomain
	for _, domain := range canonical.Domains {
		if domain.Hostname == "media.example.test" {
			primary = domain
		}
	}
	if primary.ServiceDomain() {
		t.Fatalf("the primary's own domain stayed a service domain: %+v", primary)
	}
	if raw := string(mustJSON(primary.domainConfig())); raw != `{"hostname":"media.example.test","https":true}` {
		t.Fatalf("the primary's row changed: %s", raw)
	}
	seerr := PlannedDomain{Hostname: "media-requests.example.test", HTTPS: true, Service: "seerr"}
	if raw := string(mustJSON(seerr.domainConfig())); raw != `{"hostname":"media-requests.example.test","https":true,"service":"seerr"}` {
		t.Fatalf("a service domain's row = %s", raw)
	}
	radarr := PlannedDomain{Hostname: "media-radarr.example.test", Service: "radarr", Port: 7878}
	if raw := string(mustJSON(radarr.domainConfig())); raw != `{"hostname":"media-radarr.example.test","https":false,"port":7878,"service":"radarr"}` {
		t.Fatalf("a service domain with a port's row = %s", raw)
	}
	// The primary's other port is a service domain of its own.
	other := mediaPlan()
	other.Domains[0].Service, other.Domains[0].Port = "jellyfin", 8920
	for _, domain := range canonicalConfiguration(other).Domains {
		if domain.Hostname == "media.example.test" && (domain.Service != "" || domain.Port != 8920) {
			t.Fatalf("the primary's other port = %+v", domain)
		}
	}
}

// Each service domain names a service of the stack that publishes the port
// on 127.0.0.1, which is where the route sends it.
func TestServiceDomainsAreHeldToTheComposeStack(t *testing.T) {
	t.Parallel()
	domains, err := validateServiceDomains(canonicalConfiguration(mediaPlan()), mediaAnalysis())
	if err != nil {
		t.Fatalf("the media stack was refused: %v", err)
	}
	if len(domains) != 3 {
		t.Fatalf("domains = %+v", domains)
	}
	// The analysis's primary is the plan's when the plan names none.
	unnamed := mediaPlan()
	unnamed.Build.PrimaryService = ""
	unnamed.Domains[0].Service = "jellyfin"
	domains, err = validateServiceDomains(canonicalConfiguration(unnamed), mediaAnalysis())
	if err != nil {
		t.Fatal(err)
	}
	for _, domain := range domains {
		if domain.Hostname == "media.example.test" && domain.ServiceDomain() {
			t.Fatalf("the analysis's primary stayed a service: %+v", domain)
		}
	}
	for _, fixture := range []struct {
		name, service string
		port          int
		field, says   string
	}{
		{"a service the stack does not have", "sonarr", 0, "service", "not a service of the Compose stack"},
		{"a port published on every address", "qbittorrent", 6881, "port", "does not publish on 127.0.0.1"},
		{"a UDP port", "qbittorrent", 8080, "port", "does not publish on 127.0.0.1"},
		{"a service with no loopback port", "postgres", 0, "service", "publishes no port on 127.0.0.1"},
		{"a service with two and none named", "prowlarr", 0, "port", "9696, 9697"},
		{"a port the service does not publish", "radarr", 8989, "port", "port 8989"},
	} {
		plan := mediaPlan()
		plan.Domains[1].Service, plan.Domains[1].Port = fixture.service, fixture.port
		configuration := canonicalConfiguration(plan)
		index := 0
		for position, domain := range configuration.Domains {
			if domain.Hostname == "media-requests.example.test" {
				index = position
			}
		}
		_, err := validateServiceDomains(configuration, mediaAnalysis())
		var validation *ValidationError
		if !errors.As(err, &validation) || validation.Field != "domains["+strconv.Itoa(index)+"]."+fixture.field || !strings.Contains(err.Error(), fixture.says) {
			t.Errorf("%s: %v", fixture.name, err)
		}
	}
	if _, err := validateServiceDomains(canonicalConfiguration(mediaPlan()), nil); err == nil {
		t.Error("a service domain without a Compose analysis was accepted")
	}
	// Named with its second port, Prowlarr is fine.
	plan := mediaPlan()
	plan.Domains[1].Service, plan.Domains[1].Port = "prowlarr", 9697
	if _, err := validateServiceDomains(canonicalConfiguration(plan), mediaAnalysis()); err != nil {
		t.Fatalf("a named second port was refused: %v", err)
	}
	// Preflight says the same before anything is built.
	draft := &Draft{Data: DraftData{Detection: &DetectionResult{Compose: mediaAnalysis()}}}
	bad := mediaPlan()
	bad.Domains[1].Service = "sonarr"
	findings := serviceDomainFindings(draft, canonicalConfiguration(bad))
	if len(findings) != 1 || findings[0].Code != "domain_service_unpublished" || findings[0].Severity != PreflightBlocked ||
		!strings.HasPrefix(findings[0].FieldID, "configuration.domains[") {
		t.Fatalf("findings = %+v", findings)
	}
	if findings := serviceDomainFindings(draft, canonicalConfiguration(mediaPlan())); len(findings) != 0 {
		t.Fatalf("a valid stack has findings %+v", findings)
	}
}

// The Domains page saves a service domain with its service and port, holds
// it to the Compose stack the source was last inspected as, and reads it
// back as it was saved.
func TestConfigurationStoreKeepsServiceDomains(t *testing.T) {
	ctx := context.Background()
	fixture := newReleaseStoreFixture(t)
	plan := mediaPlan()
	source := DraftSourceConfig{Kind: SourceLocal, Mode: SourceModeLocalCheckout, LocalPath: "/srv/release-fixture"}
	identity := SourceIdentity{Kind: SourceLocal, LocalPath: "/srv/release-fixture", Revision: strings.Repeat("a", 40)}
	sourceJSON, _ := json.Marshal(source)
	identityJSON, _ := json.Marshal(identity)
	buildJSON, _ := json.Marshal(plan.Build)
	evidenceJSON, _ := json.Marshal(StoredBuildEvidence{Candidates: []DetectedCandidate{}, Compose: mediaAnalysis()})
	runtimeJSON, _ := json.Marshal(plan.Runtime)
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO deploy_sources(environment_id, revision, kind, config_json, identity_json, digest, created_at) VALUES(?, 1, 'local', ?, ?, ?, ?)",
			[]any{fixture.envID, string(sourceJSON), string(identityJSON), digestBytes(sourceJSON, identityJSON), fixture.now.Unix()}},
		{"INSERT INTO deploy_build_plans(environment_id, revision, method, config_json, evidence_json, preview, digest, created_at) VALUES(?, 1, 'compose', ?, ?, ?, ?, ?)",
			[]any{fixture.envID, string(buildJSON), string(evidenceJSON), string(buildJSON), digestBytes(buildJSON), fixture.now.Unix()}},
		{"INSERT INTO deploy_runtime_plans(environment_id, revision, config_json, preview, digest, created_at) VALUES(?, 1, ?, ?, ?, ?)",
			[]any{fixture.envID, string(runtimeJSON), string(runtimeJSON), digestBytes(runtimeJSON), fixture.now.Unix()}},
		{"UPDATE deploy_environments SET desired_revision = 1 WHERE id = ?", []any{fixture.envID}},
	} {
		if _, err := fixture.base.DB.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	current, err := fixture.variables.EnvironmentConfiguration(ctx, fixture.projectID, fixture.envID)
	if err != nil {
		t.Fatal(err)
	}
	save := func(domains []PlannedDomain) error {
		_, err := fixture.variables.SaveEnvironmentConfiguration(ctx, fixture.projectID, fixture.envID, ConfigurationWriteRequest{
			Revision: current.Revision, Build: plan.Build, Runtime: plan.Runtime,
			Dependencies: current.Dependencies, Checks: current.Checks, Domains: domains,
		})
		return err
	}
	refused := mediaPlan().Domains
	refused[1].Service = "sonarr"
	if err := save(refused); !errors.Is(err, ErrInvalidPlan) || !strings.Contains(err.Error(), "sonarr") {
		t.Fatalf("a domain routed to a service the stack does not have: %v", err)
	}
	domains := mediaPlan().Domains
	domains[0].Service = "jellyfin"
	if err := save(domains); err != nil {
		t.Fatal(err)
	}
	read, err := fixture.variables.EnvironmentConfiguration(ctx, fixture.projectID, fixture.envID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]PlannedDomain{}
	for _, domain := range read.Domains {
		got[domain.Hostname] = domain
	}
	if got["media.example.test"].ServiceDomain() || got["media-requests.example.test"].Service != "seerr" ||
		got["media-radarr.example.test"].Service != "radarr" || got["media-radarr.example.test"].Port != 7878 {
		t.Fatalf("domains read back = %+v", read.Domains)
	}
}

// The runtime record keeps every loopback TCP publication of the release's
// own containers, by service.
func TestComposeServicePortsAreReadFromTheStartedContainers(t *testing.T) {
	t.Parallel()
	container := func(id, service string, ports ...dockerx.Port) dockerx.Container {
		return dockerx.Container{ID: id, Labels: map[string]string{"com.docker.compose.service": service}, Ports: ports}
	}
	containers := []dockerx.Container{
		container("c-seerr", "seerr", dockerx.Port{IP: "127.0.0.1", PrivatePort: 5055, PublicPort: 30081, Type: "tcp"}),
		container("c-jellyfin", "jellyfin",
			dockerx.Port{IP: "127.0.0.1", PrivatePort: 8096, PublicPort: 30080, Type: "tcp"},
			dockerx.Port{IP: "127.0.0.1", PrivatePort: 8096, PublicPort: 30080, Type: "tcp"},
			dockerx.Port{PrivatePort: 7359, Type: "udp"}),
		container("c-qbittorrent", "qbittorrent",
			dockerx.Port{IP: "0.0.0.0", PrivatePort: 6881, PublicPort: 6881, Type: "tcp"},
			dockerx.Port{IP: "127.0.0.1", PrivatePort: 8080, PublicPort: 30085, Type: "tcp"},
			dockerx.Port{IP: "127.0.0.1", PrivatePort: 6881, PublicPort: 6881, Type: "udp"}),
		container("c-other", "seerr", dockerx.Port{IP: "127.0.0.1", PrivatePort: 5055, PublicPort: 40000, Type: "tcp"}),
	}
	got := composeServicePorts(containers, []string{"c-seerr", "c-jellyfin", "c-qbittorrent"})
	want := []composeServicePort{
		{Service: "jellyfin", ContainerPort: 8096, HostPort: 30080},
		{Service: "qbittorrent", ContainerPort: 8080, HostPort: 30085},
		{Service: "seerr", ContainerPort: 5055, HostPort: 30081},
	}
	if string(mustJSON(got)) != string(mustJSON(want)) {
		t.Fatalf("service ports = %+v, want %+v", got, want)
	}
}

// mediaRelease is a started media stack as activation reads it: its frozen
// domains and Compose services, and the runtime record of what it published.
func mediaRelease(ports []composeServicePort) (runtimeReleaseSnapshot, ReleaseRuntime) {
	plan := mediaPlan()
	snapshot := runtimeReleaseSnapshot{Version: 1, Plan: plan.Runtime, Domains: canonicalConfiguration(plan).Domains,
		Compose: &ResolvedComposeSnapshot{PrimaryService: "jellyfin", Services: []ResolvedComposeService{
			{Plan: ComposeServicePlan{Name: "jellyfin"}}, {Plan: ComposeServicePlan{Name: "radarr"}}, {Plan: ComposeServicePlan{Name: "seerr"}},
		}}}
	runtime := ReleaseRuntime{Kind: "compose", RuntimeID: "jd-e7", Host: "127.0.0.1", Port: 30080,
		Metadata: mustJSON(dockerReleaseRuntimeMetadata{Version: 1, ServicePorts: ports})}
	return snapshot, runtime
}

func mediaPorts() []composeServicePort {
	return []composeServicePort{
		{Service: "jellyfin", ContainerPort: 8096, HostPort: 30080},
		{Service: "radarr", ContainerPort: 7878, HostPort: 30082},
		{Service: "seerr", ContainerPort: 5055, HostPort: 30081},
	}
}

// The route a release's cutover writes sends each service domain to its
// service's loopback publication, and keeps every name in the route.
func TestReleaseRouteSendsServiceDomainsToTheirPublications(t *testing.T) {
	t.Parallel()
	snapshot, runtime := mediaRelease(mediaPorts())
	route, err := releaseRoute(7, snapshot, runtime)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(route.Domains, ",") != "media-radarr.example.test,media-requests.example.test,media.example.test" ||
		route.Upstream != "http://127.0.0.1:30080" {
		t.Fatalf("route = %+v", route)
	}
	want := []proxysvc.HostUpstream{
		{Hostnames: []string{"media-radarr.example.test"}, Upstream: "http://127.0.0.1:30082"},
		{Hostnames: []string{"media-requests.example.test"}, Upstream: "http://127.0.0.1:30081"},
	}
	if string(mustJSON(route.HostUpstreams)) != string(mustJSON(want)) {
		t.Fatalf("host upstreams = %+v", route.HostUpstreams)
	}
	// Two names for one service are one server.
	snapshot.Domains = append(snapshot.Domains, PlannedDomain{Hostname: "requests.example.test", HTTPS: true, Ownership: OwnershipManaged, Service: "seerr"})
	route, err = releaseRoute(7, snapshot, runtime)
	if err != nil || len(route.HostUpstreams) != 2 ||
		strings.Join(route.HostUpstreams[1].Hostnames, ",") != "media-requests.example.test,requests.example.test" {
		t.Fatalf("host upstreams = %+v, %v", route.HostUpstreams, err)
	}
	// A release without service domains is the route it always was.
	plain, plainRuntime := mediaRelease(nil)
	plain.Domains = plain.Domains[2:]
	route, err = releaseRoute(7, plain, plainRuntime)
	if err != nil || route.HostUpstreams != nil {
		t.Fatalf("a route without service domains = %+v, %v", route, err)
	}
	if string(mustJSON(route)) != string(mustJSON(publicRoute(7, plain, plainRuntime))) {
		t.Fatalf("releaseRoute changed a route without service domains")
	}
	// The public-route checks ask the primary's names; the service
	// domains are asked on their own terms.
	if urls := publicRouteURLs(snapshot.Domains); strings.Join(urls, ",") != "https://media.example.test/" {
		t.Fatalf("public checks ask %v", urls)
	}
	if urls := serviceRouteURLs(snapshot.Domains); strings.Join(urls, ",") != "https://media-radarr.example.test/,https://media-requests.example.test/,https://requests.example.test/" {
		t.Fatalf("service checks ask %v", urls)
	}
	if served := servedDomains(snapshot.Domains); len(served) != 1 || served[0].Hostname != "media.example.test" {
		t.Fatalf("traffic replay reads %+v", served)
	}
}

func TestReleaseRouteRefusesAServiceTheReleaseDoesNotPublish(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name  string
		ports []composeServicePort
		edit  func(*runtimeReleaseSnapshot, *ReleaseRuntime)
		says  string
	}{
		{"no publication", mediaPorts()[:2], nil, "media-requests.example.test routes to seerr"},
		{"another port", []composeServicePort{mediaPorts()[0], {Service: "radarr", ContainerPort: 9999, HostPort: 30082}, mediaPorts()[2]}, nil, "port 7878 of radarr"},
		{"two ports and none named", append(mediaPorts(), composeServicePort{Service: "seerr", ContainerPort: 5056, HostPort: 30086}), nil, "several ports"},
		{"a release recorded before service ports", nil, nil, "media-radarr.example.test routes to port 7878 of radarr"},
		{"not a Compose release", mediaPorts(), func(_ *runtimeReleaseSnapshot, runtime *ReleaseRuntime) { runtime.Kind = "container" }, "not a Compose stack"},
	} {
		snapshot, runtime := mediaRelease(fixture.ports)
		if fixture.edit != nil {
			fixture.edit(&snapshot, &runtime)
		}
		_, err := releaseRoute(7, snapshot, runtime)
		if !errors.Is(err, errServicePortMissing) || !strings.Contains(err.Error(), fixture.says) {
			t.Errorf("%s: %v", fixture.name, err)
			continue
		}
		if code, message := servicePortFailure(err); code != "service_port_missing" || strings.HasPrefix(message, "service_port_missing") {
			t.Errorf("%s: failure %s %q", fixture.name, code, message)
		}
	}
}

// A public-route check of a service domain counts any answer below 500,
// an application's own sign-in included, and fails a 5xx or no answer.
func TestServiceDomainChecksTakeASignInAsReachable(t *testing.T) {
	t.Parallel()
	status := http.StatusUnauthorized
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status == http.StatusFound {
			w.Header().Set("Location", "https://elsewhere.example.test/login")
		}
		w.WriteHeader(status)
	}))
	defer server.Close()
	check := PlannedCheck{Name: "Public route", Kind: string(CheckPublicRoute), Phase: "smoke", Required: true,
		Config: json.RawMessage(`{"attempts":1,"timeoutSeconds":2,"expectedStatus":[200]}`)}
	derived, target, ok := serviceRouteCheck(check, server.URL+"/")
	if !ok || !target.ServiceRoute || !derived.Required || !strings.HasPrefix(derived.Name, "Public route · 127.0.0.1:") {
		t.Fatalf("derived = %+v, target %+v", derived, target)
	}
	if err := validateCheckConfiguration(derived.Kind, derived.Config); err != nil {
		t.Fatalf("the derived check is not a valid check: %v", err)
	}
	runner := NewCheckRunner(nil)
	for _, answer := range []int{http.StatusOK, http.StatusUnauthorized, http.StatusFound, http.StatusForbidden, http.StatusBadRequest, http.StatusNotFound} {
		status = answer
		if evidence := runner.Run(context.Background(), derived, target); evidence.Outcome != HealthPassed {
			t.Errorf("%d: %+v", answer, evidence)
		}
	}
	status = http.StatusBadGateway
	if evidence := runner.Run(context.Background(), derived, target); evidence.Outcome != HealthFailed {
		t.Errorf("502: %+v", evidence)
	}
	// A check aimed at an address of its own is the operator's probe of
	// that address, and a disabled one is off.
	for _, config := range []string{`{"url":"https://status.example.test/"}`, `{"disabled":true}`} {
		if _, _, ok := serviceRouteCheck(PlannedCheck{Name: "own", Kind: string(CheckPublicRoute), Phase: "smoke", Config: json.RawMessage(config)}, server.URL+"/"); ok {
			t.Errorf("%s was asked of a service domain", config)
		}
	}
}

// Activation writes the route with each service domain sent to its service,
// and a service the release did not publish fails it as
// service_port_missing with the candidate stopped and no route written.
func TestActivationRoutesServiceDomainsOrFailsWithoutTheirPort(t *testing.T) {
	for _, fixture := range []struct {
		name  string
		ports []composeServicePort
		code  string
	}{
		{"published", mediaPorts(), ""},
		{"missing", mediaPorts()[:2], "service_port_missing"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			ctx := context.Background()
			store := newReleaseStoreFixture(t)
			plan := mediaPlan()
			store.addPlanWithRuntime(t, 1, strings.Repeat("a", 40), plan.Runtime)
			run, lease := store.claimedRun(t, 1)
			release := createComposeCandidateWithDomains(t, store, *run, lease, plan.Runtime, canonicalConfiguration(plan).Domains)
			if _, err := store.runs.RecordCandidateRuntime(ctx, *run, lease.Token, ReleaseRuntimeInput{
				ReleaseID: release.Release.ID, Kind: "compose", RuntimeID: "jd-e1", Host: "127.0.0.1", Port: 30080,
				Metadata: mustJSON(dockerReleaseRuntimeMetadata{Version: 1, PrimaryContainerID: "c-jellyfin", ServicePorts: fixture.ports}),
			}); err != nil {
				t.Fatal(err)
			}
			owner := &orderingRuntimeOwner{running: map[string]bool{"jd-e1": true}, allowConcurrent: true}
			proxy := &schemeRouteProxy{}
			executor := &NormalizedStepExecutor{store: store.runs, variables: store.variables, runtime: owner, proxy: proxy}
			result := executor.activate(ctx, StepExecution{Run: *run, ClaimToken: lease.Token, Output: discardStepOutput{}}, mustExecutionPlan(t, store, *run))
			if fixture.code != "" {
				if result.State != StepFailed || result.ErrorCode != fixture.code || !strings.Contains(result.ErrorMessage, "media-requests.example.test routes to seerr") ||
					len(proxy.routes) != 0 || !result.Recovered {
					t.Fatalf("activation = %#v, routes %+v", result, proxy.routes)
				}
				return
			}
			if result.State != StepPassed || len(proxy.routes) != 1 {
				t.Fatalf("activation = %#v, routes = %+v", result, proxy.routes)
			}
			route := proxy.routes[0]
			if route.Upstream != "http://127.0.0.1:30080" || len(route.HostUpstreams) != 2 ||
				route.HostUpstreams[1].Upstream != "http://127.0.0.1:30081" || !containsString(route.Domains, "media-requests.example.test") {
				t.Fatalf("route = %+v", route)
			}
		})
	}
}

// createComposeCandidateWithDomains is a candidate release of a Compose
// stack whose primary service runs the release's image.
func createComposeCandidateWithDomains(
	t *testing.T,
	fixture *releaseStoreFixture,
	run EngineRun,
	lease QueueLease,
	plan RuntimePlanConfig,
	domains []PlannedDomain,
) *ReleaseWithArtifacts {
	t.Helper()
	imageDigest := fakeContentDigest("jellyfin")
	snapshot := runtimeReleaseSnapshot{
		Version: 1, Plan: plan,
		Compose: &ResolvedComposeSnapshot{PrimaryService: "jellyfin", Files: []string{"compose.yml"}, Services: []ResolvedComposeService{
			{Plan: ComposeServicePlan{Name: "jellyfin"}, Reference: "example.test/jellyfin:1", Digest: imageDigest},
			{Plan: ComposeServicePlan{Name: "radarr"}, Reference: "example.test/radarr:1", Digest: fakeContentDigest("radarr")},
			{Plan: ComposeServicePlan{Name: "seerr"}, Reference: "example.test/seerr:1", Digest: fakeContentDigest("seerr")},
		}},
		Variables: []ReleaseVariableSnapshot{}, Dependencies: []PlannedDependency{}, Checks: []PlannedCheck{},
		Domains:        append([]PlannedDomain(nil), domains...),
		SourceIdentity: SourceIdentity{Kind: SourceLocal, LocalPath: "/srv/release-fixture", Revision: strings.Repeat("a", 40)},
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	release, err := fixture.runs.CreateCandidateRelease(context.Background(), run, lease.Token, CandidateReleaseInput{
		Artifacts: []ReleaseArtifactInput{{
			Kind: ArtifactImage, Reference: "example.test/jellyfin:1", Digest: imageDigest,
			Metadata: json.RawMessage(`{"os":"linux","architecture":"amd64"}`), SizeBytes: 1024,
		}},
		Prepared: PreparedBuild{
			Method: BuildDockerfile, Dockerfile: "Dockerfile", DockerfileDigest: fakeContentDigest("Dockerfile"),
			BuildArgv: []string{"docker", "buildx", "build", "."}, BaseImages: []ResolvedImage{},
			CachePolicy: "reuse", SecretIDs: []string{},
		},
		RuntimeSnapshot: raw, RuntimeDigest: digestBytes(raw),
	})
	if err != nil {
		t.Fatal(err)
	}
	return release
}

// A rollback clones the old release, frozen domains and all, and starts its
// stack again: the route sends each service domain to the ports that start
// recorded, not the ones the release had the first time.
func TestRollbackRoutesTheClonedReleasesServiceDomainsOnItsNewPorts(t *testing.T) {
	ctx := context.Background()
	store := newReleaseStoreFixture(t)
	plan := mediaPlan()
	store.addPlanWithRuntime(t, 1, strings.Repeat("a", 40), plan.Runtime)
	firstRun, firstLease := store.claimedRun(t, 1)
	first := createComposeCandidateWithDomains(t, store, *firstRun, firstLease, plan.Runtime, canonicalConfiguration(plan).Domains)
	if _, err := store.runs.RecordCandidateRuntime(ctx, *firstRun, firstLease.Token, ReleaseRuntimeInput{
		ReleaseID: first.Release.ID, Kind: "compose", RuntimeID: "jd-e1", Host: "127.0.0.1", Port: 30080,
		Metadata: mustJSON(dockerReleaseRuntimeMetadata{Version: 1, ServicePorts: mediaPorts()}),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.runs.ActivateCandidate(ctx, firstRun.ID, firstLease.Token, first.Release.ID); err != nil {
		t.Fatal(err)
	}
	store.finishActivatedRun(t, firstRun.ID, first.Release.ID, firstLease.Token)

	store.now = store.now.Add(time.Minute)
	rollbackRun, _, err := store.runs.Enqueue(ctx, RunRequest{
		ProjectID: store.projectID, EnvironmentID: store.envID,
		Operation: OperationRollback, Trigger: TriggerManual, Actor: "admin",
		RequestDigest: "service-domain-rollback", PlanRevision: 1,
		VariableSnapshotRunID: firstRun.ID, SlotClass: SlotHeavy,
		Metadata: mustJSON(map[string]int64{"targetReleaseId": first.Release.ID}),
	})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.runs.ClaimNext(ctx, "rollback-worker", QueueBudget{}, time.Minute)
	if err != nil || lease == nil {
		t.Fatalf("lease = %#v, %v", lease, err)
	}
	claimed, err := store.runs.Run(ctx, rollbackRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	clone, err := store.runs.CloneCandidateRelease(ctx, *claimed, lease.Token, first.Release.ID)
	if err != nil {
		t.Fatal(err)
	}
	moved := []composeServicePort{
		{Service: "jellyfin", ContainerPort: 8096, HostPort: 30180},
		{Service: "radarr", ContainerPort: 7878, HostPort: 30182},
		{Service: "seerr", ContainerPort: 5055, HostPort: 30181},
	}
	if _, err := store.runs.RecordCandidateRuntime(ctx, *claimed, lease.Token, ReleaseRuntimeInput{
		ReleaseID: clone.Release.ID, Kind: "compose", RuntimeID: "jd-e1", Host: "127.0.0.1", Port: 30180,
		Metadata: mustJSON(dockerReleaseRuntimeMetadata{Version: 1, ServicePorts: moved}),
	}); err != nil {
		t.Fatal(err)
	}
	owner := &orderingRuntimeOwner{running: map[string]bool{"jd-e1": true}, allowConcurrent: true}
	proxy := &schemeRouteProxy{}
	executor := &NormalizedStepExecutor{store: store.runs, variables: store.variables, runtime: owner, proxy: proxy}
	result := executor.activate(ctx, StepExecution{Run: *claimed, ClaimToken: lease.Token, Output: discardStepOutput{}}, mustExecutionPlan(t, store, *claimed))
	if result.State != StepPassed || len(proxy.routes) != 1 {
		t.Fatalf("activation = %#v, routes = %+v", result, proxy.routes)
	}
	want := []proxysvc.HostUpstream{
		{Hostnames: []string{"media-radarr.example.test"}, Upstream: "http://127.0.0.1:30182"},
		{Hostnames: []string{"media-requests.example.test"}, Upstream: "http://127.0.0.1:30181"},
	}
	if route := proxy.routes[0]; route.Upstream != "http://127.0.0.1:30180" || string(mustJSON(route.HostUpstreams)) != string(mustJSON(want)) {
		t.Fatalf("route = %+v", route)
	}
}

// Staging is a copy of production under a name of its own: production's
// service domains are production's names, and none of them is copied, with
// or without a domain for staging.
func TestStagingLeavesProductionsServiceDomainsOut(t *testing.T) {
	ctx := context.Background()
	f := newStagingFixture(t)
	if _, err := f.base.DB.Exec(
		"INSERT INTO deploy_dependencies(environment_id, release_id, kind, ownership, resource_kind, resource_id, config_json, created_at) VALUES(?, 0, 'domain', 'managed', 'proxy_site', ?, ?, ?)",
		f.envID, "app-requests.example.com", `{"hostname":"app-requests.example.com","https":true,"service":"seerr"}`, f.now.Unix()); err != nil {
		t.Fatal(err)
	}
	copied, err := f.variables.CreateStagingEnvironment(ctx, f.projectID, StagingRequest{Domain: "staging.example.com", Actor: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := f.base.DB.Query("SELECT resource_id, config_json FROM deploy_dependencies WHERE environment_id = ? AND kind = 'domain'", copied.Environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var domains []string
	for rows.Next() {
		var name, config string
		if err := rows.Scan(&name, &config); err != nil {
			t.Fatal(err)
		}
		domains = append(domains, name+" "+config)
	}
	if len(domains) != 1 || domains[0] != `staging.example.com {"hostname":"staging.example.com","https":true}` {
		t.Fatalf("staging's domains = %q", domains)
	}
}

// A pull request's preview has the domains of its own, the trigger's
// preview pattern or none: production's service domains are production's
// names and never become a preview's.
func TestPreviewsLeaveProductionsServiceDomainsOut(t *testing.T) {
	ctx := t.Context()
	f := newAutomationFixture(t)
	setSourceRemote(t, f, f.environmentID, "https://github.com/acme/app.git", "acme/app")
	if _, err := f.store.DB.Exec(
		"INSERT INTO deploy_dependencies(environment_id, release_id, kind, ownership, resource_kind, resource_id, config_json, created_at) VALUES(?, 0, 'domain', 'managed', 'proxy_site', ?, ?, 1)",
		f.environmentID, "app-requests.example.test", `{"hostname":"app-requests.example.test","https":true,"service":"seerr"}`); err != nil {
		t.Fatal(err)
	}
	trigger, err := f.automation.EnsurePullRequestTrigger(ctx, f.projectID, f.environmentID, "acme/app", "main")
	if err != nil {
		t.Fatal(err)
	}
	event := pullRequestEvent(7, strings.Repeat("a", 40))
	if _, err := f.automation.RecordPreviewApproval(ctx, trigger, event, "admin"); err != nil {
		t.Fatal(err)
	}
	preview, created, err := f.automation.EnsurePreview(ctx, trigger, event)
	if err != nil || !created {
		t.Fatalf("preview = %#v, created = %v, %v", preview, created, err)
	}
	var domains int
	if err := f.store.DB.QueryRow("SELECT COUNT(*) FROM deploy_dependencies WHERE environment_id = ? AND kind = 'domain'", preview.EnvironmentID).Scan(&domains); err != nil {
		t.Fatal(err)
	}
	if domains != 0 {
		t.Fatalf("the preview has %d domains, want none of production's", domains)
	}
}

// The wizard's journey for a stack with a service domain: the configuration
// step holds it to the stack the detection planned, preflight has nothing to
// say about it, and commit stores its service in the domain's row.
func TestStackDraftCommitsAServiceDomain(t *testing.T) {
	ctx := context.Background()
	fixture := newPlanningStoreFixture(t)
	fake := &planningDockerFake{composeAvailable: true, composeValid: true, image: &dockerx.DistributionImage{
		Digest: "sha256:" + strings.Repeat("7", 64), Platforms: []string{"linux/amd64"},
	}}
	analyzer := NewHostSourceAnalyzer(nil, nil, t.TempDir(), fake, nil)
	draft, err := fixture.plans.Create(ctx, 41, "admin")
	if err != nil {
		t.Fatal(err)
	}
	draft, err = fixture.plans.Save(ctx, draft.ID, 41, true, DraftSaveRequest{
		Revision: draft.Revision, Step: DraftIntent, Intent: &DraftIntentConfig{Name: "media", Profile: ProfileCompose},
	})
	if err != nil {
		t.Fatal(err)
	}
	source := composeTemplateSource(t, "media-essential", map[string]string{"domain": "media.example.test"})
	if draft, err = fixture.plans.Save(ctx, draft.ID, 41, true, DraftSaveRequest{Revision: draft.Revision, Step: DraftSource, Source: &source}); err != nil {
		t.Fatal(err)
	}
	detection, err := analyzer.Analyze(ctx, *draft.Data.Source)
	if err != nil {
		t.Fatal(err)
	}
	if draft, err = fixture.plans.SaveDetection(ctx, draft.ID, 41, true, draft.Revision, detection); err != nil {
		t.Fatal(err)
	}
	configuration := *draft.Data.Configuration
	refused := configuration
	refused.Domains = append(append([]PlannedDomain(nil), configuration.Domains...),
		PlannedDomain{Hostname: "media-radarr.example.test", HTTPS: true, Ownership: OwnershipManaged, Service: "radarr"})
	if _, err := fixture.plans.Save(ctx, draft.ID, 41, true, DraftSaveRequest{Revision: draft.Revision, Step: DraftConfiguration, Configuration: &refused}); !errors.Is(err, ErrInvalidPlan) || !strings.Contains(err.Error(), "radarr") {
		t.Fatalf("a service Essential does not have: %v", err)
	}
	configuration.Domains = append(append([]PlannedDomain(nil), configuration.Domains...),
		PlannedDomain{Hostname: "media-requests.example.test", HTTPS: true, Ownership: OwnershipManaged, Service: "seerr"})
	if draft, err = fixture.plans.Save(ctx, draft.ID, 41, true, DraftSaveRequest{Revision: draft.Revision, Step: DraftConfiguration, Configuration: &configuration}); err != nil {
		t.Fatal(err)
	}
	preflight, err := PreflightDraft(ctx, draft, &preflightObserverFake{observation: HostObservation{
		Facilities: map[string]FacilityObservation{"docker": {Available: true}, "compose": {Available: true}},
		Paths:      []PathObservation{}, Ports: []PortObservation{},
	}}, true)
	if err != nil {
		t.Fatal(err)
	}
	acknowledged := []string{}
	for _, finding := range preflight.Findings {
		switch finding.Severity {
		case PreflightBlocked, PreflightDecision:
			t.Fatalf("preflight blocked: %+v", finding)
		case PreflightWarning:
			acknowledged = append(acknowledged, finding.Code)
		}
	}
	if draft, err = fixture.plans.SavePreflight(ctx, draft.ID, 41, true, draft.Revision, preflight); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.plans.Commit(ctx, draft.ID, 41, true, DraftCommitRequest{Revision: draft.Revision, AcknowledgedWarnings: acknowledged})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := fixture.store.DB.Query(`SELECT resource_id, config_json FROM deploy_dependencies WHERE environment_id = ? AND kind = 'domain' ORDER BY resource_id`, result.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := []string{}
	for rows.Next() {
		var name, config string
		if err := rows.Scan(&name, &config); err != nil {
			t.Fatal(err)
		}
		got = append(got, config)
	}
	want := []string{
		`{"hostname":"media-requests.example.test","https":true,"service":"seerr"}`,
		`{"hostname":"media.example.test","https":true}`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("committed domain rows = %q", got)
	}
}
