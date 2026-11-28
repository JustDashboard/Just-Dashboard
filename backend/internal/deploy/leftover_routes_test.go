package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// leftoverOwners marks routes as the store would: an environment in live is
// owned, one in archived is owned and archived, anything else is gone.
type leftoverOwners struct {
	live     map[int64]proxysvc.VHostOwner
	archived map[int64]bool
	err      error
	calls    int
}

func (o *leftoverOwners) MarkDeploymentRoutes(_ context.Context, hosts []proxysvc.VHost) error {
	o.calls++
	if o.err != nil {
		return o.err
	}
	for i := range hosts {
		id, ok := routeEnvironment(hosts[i].Name)
		if !ok {
			continue
		}
		if owner, ok := o.live[id]; ok {
			owner.Archived = o.archived[id]
			hosts[i].Owner = &owner
		}
	}
	return nil
}

func TestDomainClaimFindingSaysWhoHoldsTheDomain(t *testing.T) {
	t.Parallel()
	planned := PlannedDomain{Hostname: "jellyfin.example.test", HTTPS: true, Ownership: OwnershipManaged}
	field := "domains.jellyfin.example.test"
	for _, tc := range []struct {
		name     string
		claims   []DomainClaim
		code     string
		severity PreflightSeverity
		link     string
		measured string
	}{
		{
			name:   "a leftover route is taken over",
			claims: []DomainClaim{{Site: "just-dashboard-env-450.conf", Leftover: true}},
			code:   "domain_takeover", severity: PreflightPass,
			link:     "/proxy/sites/just-dashboard-env-450.conf",
			measured: "jellyfin.example.test · just-dashboard-env-450.conf",
		},
		{
			name:   "a live deployment's route sends the reader to its domains",
			claims: []DomainClaim{{Site: "just-dashboard-env-7.conf", Project: "media", ProjectID: 3}},
			code:   "domain_conflict", severity: PreflightBlocked,
			link:     "/deploy/3/settings/domains",
			measured: "jellyfin.example.test · media",
		},
		{
			name:   "a site of its own is opened, not the list",
			claims: []DomainClaim{{Site: "jellyfin.example.test"}},
			code:   "domain_conflict", severity: PreflightBlocked,
			link:     "/proxy/sites/jellyfin.example.test",
			measured: "jellyfin.example.test · jellyfin.example.test",
		},
		{
			name: "a leftover beside a live site is still a conflict",
			claims: []DomainClaim{
				{Site: "just-dashboard-env-450.conf", Leftover: true},
				{Site: "docker-caddy:caddy:jellyfin.example.test"},
			},
			code: "domain_conflict", severity: PreflightBlocked,
			link:     "/proxy/sites/docker-caddy:caddy:jellyfin.example.test",
			measured: "jellyfin.example.test · docker-caddy:caddy:jellyfin.example.test",
		},
		{
			name:   "an observation without claims keeps the list",
			claims: nil,
			code:   "domain_conflict", severity: PreflightBlocked,
			link: "/proxy/sites", measured: "jellyfin.example.test",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := domainClaimFinding(planned, tc.claims, field)
			if item.Code != tc.code || item.Severity != tc.severity || item.DeepLink != tc.link || item.Measured != tc.measured {
				t.Fatalf("finding = %s/%s link %q measured %q; want %s/%s link %q measured %q",
					item.Code, item.Severity, item.DeepLink, item.Measured, tc.code, tc.severity, tc.link, tc.measured)
			}
			if item.FieldID != field {
				t.Fatalf("field = %q", item.FieldID)
			}
		})
	}
}

// The route a takeover removes goes with every name it answers, not only the
// one claimed: the finding names them all, so the reader sees what else stops
// answering.
func TestTakeoverFindingNamesEverythingTheLeftoverAnswers(t *testing.T) {
	t.Parallel()
	planned := PlannedDomain{Hostname: "jellyfin.example.test", HTTPS: true, Ownership: OwnershipManaged}
	leftover := proxysvc.VHost{Name: "just-dashboard-env-450.conf",
		ServerNames: []string{"Jellyfin.example.test", "media.example.test", "_", "media.example.test"}}
	claim := claimOf(leftover, true)
	if strings.Join(claim.Names, ",") != "Jellyfin.example.test,media.example.test" {
		t.Fatalf("names = %v", claim.Names)
	}
	item := domainClaimFinding(planned, []DomainClaim{claim}, "domains.jellyfin.example.test")
	if item.Code != "domain_takeover" {
		t.Fatalf("code = %s", item.Code)
	}
	if !strings.Contains(item.Means, "jellyfin.example.test and media.example.test") ||
		!strings.Contains(item.Means, "media.example.test stops answering") {
		t.Fatalf("detail = %q", item.Means)
	}
	// A leftover that answers only the claimed name says nothing more goes.
	alone := domainClaimFinding(planned, []DomainClaim{claimOf(proxysvc.VHost{
		Name: "just-dashboard-env-451.conf", ServerNames: []string{"jellyfin.example.test"},
	}, true)}, "domains.jellyfin.example.test")
	if strings.Contains(alone.Means, "stops answering") || strings.Contains(alone.Means, "stop answering") {
		t.Fatalf("detail = %q", alone.Means)
	}
	// A live route's claim carries no names: it is not removed.
	if live := claimOf(proxysvc.VHost{Name: "just-dashboard-env-7.conf", ServerNames: []string{"a.test"},
		Owner: &proxysvc.VHostOwner{Project: "media"}}, true); len(live.Names) != 0 {
		t.Fatalf("live names = %v", live.Names)
	}
}

func TestClaimOfTakesNothingForALeftoverWithoutItsOwners(t *testing.T) {
	t.Parallel()
	gone := proxysvc.VHost{Name: "just-dashboard-env-450.conf"}
	if !claimOf(gone, true).Leftover {
		t.Fatal("a route whose environment is gone is a leftover")
	}
	if claimOf(gone, false).Leftover {
		t.Fatal("a route is not a leftover when its owners could not be read")
	}
	archived := proxysvc.VHost{Name: "just-dashboard-env-9.conf", Owner: &proxysvc.VHostOwner{Project: "old", Archived: true}}
	if !claimOf(archived, true).Leftover {
		t.Fatal("an archived project's route is a leftover")
	}
	for _, name := range []string{"jellyfin.example.test", "just-dashboard-env-07.conf", "just-dashboard-env-0.conf", "just-dashboard-env-4.conf.bak"} {
		if claimOf(proxysvc.VHost{Name: name}, true).Leftover {
			t.Fatalf("%s is not a name a deployment writes", name)
		}
	}
}

func TestPreflightTellsALeftoverRouteFromALiveOne(t *testing.T) {
	t.Parallel()
	proxy := &planningProxyFake{vhosts: []proxysvc.VHost{
		{Name: "just-dashboard-env-450.conf", ServerNames: []string{"Jellyfin.example.test"}},
		{Name: "just-dashboard-env-7.conf", ServerNames: []string{"media.example.test"}},
	}}
	owners := &leftoverOwners{live: map[int64]proxysvc.VHostOwner{7: {Project: "media", ProjectID: 3, EnvironmentID: 7}}}
	observer := NewHostPreflightObserver(nil, t.TempDir(), nil, proxy).WithRouteOwners(owners)
	observation, err := observer.Observe(context.Background(), ObservationRequest{Domains: []PlannedDomain{
		{Hostname: "jellyfin.example.test", Ownership: OwnershipManaged},
		{Hostname: "media.example.test", Ownership: OwnershipManaged},
		{Hostname: "free.example.test", Ownership: OwnershipManaged},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]DomainObservation{}
	for _, domain := range observation.Domains {
		got[domain.Hostname] = domain
	}
	if d := got["jellyfin.example.test"]; !d.Conflict || len(d.Claims) != 1 || !d.Claims[0].Leftover {
		t.Fatalf("leftover = %+v", d)
	}
	if d := got["media.example.test"]; !d.Conflict || len(d.Claims) != 1 || d.Claims[0].Leftover || d.Claims[0].ProjectID != 3 {
		t.Fatalf("live = %+v", d)
	}
	if d := got["free.example.test"]; d.Conflict || len(d.Claims) != 0 {
		t.Fatalf("free = %+v", d)
	}

	owners.err = errors.New("store is locked")
	observation, err = observer.Observe(context.Background(), ObservationRequest{Domains: []PlannedDomain{
		{Hostname: "jellyfin.example.test", Ownership: OwnershipManaged},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if d := observation.Domains[0]; !d.Conflict || d.Claims[0].Leftover {
		t.Fatalf("without owners a route stays a conflict: %+v", d)
	}
}

// takeoverProxy records what a release did to the routes it found.
type takeoverProxy struct {
	countingActivationProxy
	vhosts   []proxysvc.VHost
	removed  []string
	restored []string
	failOn   string
}

func (p *takeoverProxy) ListVHosts(context.Context) ([]proxysvc.VHost, error) {
	return append([]proxysvc.VHost(nil), p.vhosts...), nil
}

func (p *takeoverProxy) SnapshotDeploymentRoute(_ context.Context, name string) (proxysvc.DeploymentRouteSnapshot, error) {
	return proxysvc.DeploymentRouteSnapshot{Name: name, Existed: true}, nil
}

func (p *takeoverProxy) RemoveDeploymentRoute(_ context.Context, name string) error {
	if name == p.failOn {
		return errors.New("caddy refused the reload")
	}
	p.removed = append(p.removed, name)
	return nil
}

func (p *takeoverProxy) RestoreDeploymentRoute(_ context.Context, snapshot proxysvc.DeploymentRouteSnapshot) error {
	p.restored = append(p.restored, snapshot.Name)
	return nil
}

type takeoverLog struct{ lines []string }

func (l *takeoverLog) Log(_, text string) error { l.lines = append(l.lines, text); return nil }
func (l *takeoverLog) Event(EventInput) error   { return nil }
func (l *takeoverLog) said(fragment string) bool {
	for _, line := range l.lines {
		if strings.Contains(line, fragment) {
			return true
		}
	}
	return false
}

func TestReleaseTakesItsDomainOverFromALeftoverRouteOnly(t *testing.T) {
	t.Parallel()
	proxy := &takeoverProxy{vhosts: []proxysvc.VHost{
		{Name: "just-dashboard-env-12.conf", ServerNames: []string{"jellyfin.example.test"}},  // its own
		{Name: "just-dashboard-env-450.conf", ServerNames: []string{"JELLYFIN.example.test"}}, // deleted
		{Name: "just-dashboard-env-9.conf", ServerNames: []string{"jellyfin.example.test"}},   // archived
		{Name: "just-dashboard-env-7.conf", ServerNames: []string{"jellyfin.example.test"}},   // live
		{Name: "jellyfin.example.test", ServerNames: []string{"jellyfin.example.test"}},       // hand-made
		{Name: "just-dashboard-env-451.conf", ServerNames: []string{"seerr.example.test"}},    // another name
	}}
	owners := &leftoverOwners{
		live:     map[int64]proxysvc.VHostOwner{7: {Project: "media"}, 9: {Project: "old"}},
		archived: map[int64]bool{9: true},
	}
	executor := (&NormalizedStepExecutor{proxy: proxy}).WithRouteOwners(owners)
	log := &takeoverLog{}
	taken := executor.takeOverLeftoverRoutes(context.Background(), StepExecution{Output: log}, 12, []string{"jellyfin.example.test"})
	if strings.Join(proxy.removed, ",") != "just-dashboard-env-450.conf,just-dashboard-env-9.conf" {
		t.Fatalf("removed = %v", proxy.removed)
	}
	if len(taken) != 2 || !log.said("Took JELLYFIN.example.test over from just-dashboard-env-450.conf") {
		t.Fatalf("taken = %v, log = %v", taken, log.lines)
	}
	executor.restoreLeftoverRoutes(context.Background(), StepExecution{Output: log}, taken)
	if strings.Join(proxy.restored, ",") != "just-dashboard-env-450.conf,just-dashboard-env-9.conf" {
		t.Fatalf("restored = %v", proxy.restored)
	}
}

func TestReleaseTakesNothingOverWhenOwnersCannotBeRead(t *testing.T) {
	t.Parallel()
	proxy := &takeoverProxy{vhosts: []proxysvc.VHost{
		{Name: "just-dashboard-env-450.conf", ServerNames: []string{"jellyfin.example.test"}},
	}}
	executor := (&NormalizedStepExecutor{proxy: proxy}).WithRouteOwners(&leftoverOwners{err: errors.New("locked")})
	log := &takeoverLog{}
	if taken := executor.takeOverLeftoverRoutes(context.Background(), StepExecution{Output: log}, 12, []string{"jellyfin.example.test"}); len(taken) != 0 || len(proxy.removed) != 0 {
		t.Fatalf("taken = %v removed = %v", taken, proxy.removed)
	}
	if !log.said("none was taken over") {
		t.Fatalf("log = %v", log.lines)
	}

	// Without the owners at all, or without a proxy that can snapshot, a
	// release behaves as it did: its own route is written and refused.
	bare := &NormalizedStepExecutor{proxy: proxy}
	if taken := bare.takeOverLeftoverRoutes(context.Background(), StepExecution{Output: log}, 12, []string{"jellyfin.example.test"}); taken != nil {
		t.Fatalf("taken without owners = %v", taken)
	}
	plain := (&NormalizedStepExecutor{proxy: &countingActivationProxy{}}).WithRouteOwners(&leftoverOwners{})
	if taken := plain.takeOverLeftoverRoutes(context.Background(), StepExecution{Output: log}, 12, []string{"jellyfin.example.test"}); taken != nil {
		t.Fatalf("taken without a snapshotting proxy = %v", taken)
	}
}

func TestReleaseKeepsALeftoverItCouldNotRemove(t *testing.T) {
	t.Parallel()
	proxy := &takeoverProxy{failOn: "just-dashboard-env-450.conf", vhosts: []proxysvc.VHost{
		{Name: "just-dashboard-env-450.conf", ServerNames: []string{"jellyfin.example.test"}},
	}}
	executor := (&NormalizedStepExecutor{proxy: proxy}).WithRouteOwners(&leftoverOwners{})
	log := &takeoverLog{}
	if taken := executor.takeOverLeftoverRoutes(context.Background(), StepExecution{Output: log}, 12, []string{"jellyfin.example.test"}); len(taken) != 0 {
		t.Fatalf("taken = %v", taken)
	}
	if !log.said("could not be removed: caddy refused the reload") {
		t.Fatalf("log = %v", log.lines)
	}
}

// cutoverTakeoverProxy is a takeoverProxy whose cutover is applied, refused,
// or applied and then not verified, and whose route restores are recorded in
// order with the snapshot each was given.
type cutoverTakeoverProxy struct {
	takeoverProxy
	refuseApply  bool
	refuseVerify bool
	// stuck is a route whose restore fails.
	stuck string
}

func (p *cutoverTakeoverProxy) RestoreDeploymentRoute(ctx context.Context, snapshot proxysvc.DeploymentRouteSnapshot) error {
	if snapshot.Name == p.stuck {
		return errors.New("caddy refused the reload")
	}
	return p.takeoverProxy.RestoreDeploymentRoute(ctx, snapshot)
}

func (p *cutoverTakeoverProxy) ApplyDeploymentRoute(_ context.Context, route proxysvc.DeploymentRoute) (proxysvc.DeploymentRouteResult, error) {
	result := proxysvc.DeploymentRouteResult{Snapshot: proxysvc.DeploymentRouteSnapshot{Name: route.Name}, Recovered: true}
	if p.refuseApply {
		return result, errors.New("caddy refused the route")
	}
	return result, nil
}

func (p *cutoverTakeoverProxy) VerifyDeploymentRoute(context.Context, proxysvc.DeploymentRoute) error {
	if p.refuseVerify {
		return errors.New("the route answers something else")
	}
	return nil
}

// A release that took a domain over from a leftover route and then did not
// go live puts the leftover back, after its own route is restored: whether
// the proxy refused the cutover or the cutover could not be verified. One
// whose own route could not be taken out leaves the leftover out rather than
// have two routes claim the name, and one that goes live keeps the domain.
func TestReleaseThatDoesNotGoLivePutsTheLeftoverRouteBack(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name         string
		refuseApply  bool
		refuseVerify bool
		stuck        bool
		state        StepState
		code         string
		restored     bool
	}{
		{name: "the proxy refuses the cutover", refuseApply: true, state: StepFailed, code: "proxy_cutover_failed", restored: true},
		{name: "the cutover is not verified", refuseVerify: true, state: StepFailed, code: "activation_verification_failed", restored: true},
		{name: "its own route cannot be taken out", refuseVerify: true, stuck: true, state: StepFailed, code: "activation_verification_failed"},
		{name: "the release goes live", state: StepPassed},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newReleaseStoreFixture(t)
			plan := RuntimePlanConfig{
				InternalPort: 3000, BindAddress: "127.0.0.1", Strategy: StrategyBlueGreen,
				Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{},
			}
			fixture.addPlanWithRuntime(t, 1, strings.Repeat("a", 40), plan)
			run, lease := fixture.claimedRun(t, 1)
			release := createRuntimeCandidateWithDomains(t, fixture, *run, lease, plan, "takeover-candidate",
				nil, []PlannedDomain{{Hostname: "jellyfin.example.test", Ownership: OwnershipManaged}})
			if _, err := fixture.runs.RecordCandidateRuntime(context.Background(), *run, lease.Token, ReleaseRuntimeInput{
				ReleaseID: release.Release.ID, Kind: "container", RuntimeID: "takeover-candidate",
				Host: "127.0.0.1", Port: 32123, Metadata: json.RawMessage(`{"version":1}`),
			}); err != nil {
				t.Fatal(err)
			}
			proxy := &cutoverTakeoverProxy{refuseApply: test.refuseApply, refuseVerify: test.refuseVerify}
			proxy.vhosts = []proxysvc.VHost{{Name: "just-dashboard-env-450.conf", ServerNames: []string{"jellyfin.example.test"}}}
			if test.stuck {
				proxy.stuck = deploymentRouteName(fixture.envID)
			}
			executor := (&NormalizedStepExecutor{
				store: fixture.runs, variables: fixture.variables, proxy: proxy,
				runtime: &orderingRuntimeOwner{running: map[string]bool{"takeover-candidate": true}, allowConcurrent: true},
			}).WithRouteOwners(&leftoverOwners{})
			log := &takeoverLog{}
			result := executor.activate(context.Background(), StepExecution{
				Run: *run, ClaimToken: lease.Token, Output: log,
			}, mustExecutionPlan(t, fixture, *run))
			if result.State != test.state || result.ErrorCode != test.code {
				t.Fatalf("activation = %s/%s, want %s/%s", result.State, result.ErrorCode, test.state, test.code)
			}
			// The proxy's own reason is in the step's message, so the screen has
			// something to go on.
			if test.refuseVerify && !strings.Contains(result.ErrorMessage, "the proxy cutover could not be verified: the route answers something else") {
				t.Fatalf("message = %q", result.ErrorMessage)
			}
			if strings.Join(proxy.removed, ",") != "just-dashboard-env-450.conf" {
				t.Fatalf("removed = %v", proxy.removed)
			}
			own := deploymentRouteName(fixture.envID)
			switch {
			case test.restored && test.refuseApply:
				// The proxy restores its own route inside the refused apply.
				if strings.Join(proxy.restored, ",") != "just-dashboard-env-450.conf" {
					t.Fatalf("restored = %v", proxy.restored)
				}
			case test.restored:
				if strings.Join(proxy.restored, ",") != own+",just-dashboard-env-450.conf" {
					t.Fatalf("restored = %v, want this release's route and then the leftover", proxy.restored)
				}
			default:
				if len(proxy.restored) != 0 {
					t.Fatalf("put back %v", proxy.restored)
				}
			}
			if test.stuck && !log.said("were not put back, because its own route could not be taken out first") {
				t.Fatalf("log = %v", log.lines)
			}
			if test.restored && !log.said("Put the leftover route just-dashboard-env-450.conf back") {
				t.Fatalf("log = %v", log.lines)
			}
		})
	}
}
