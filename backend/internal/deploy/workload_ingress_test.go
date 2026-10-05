package deploy

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

func linkedIngressFixture() proxysvc.ExistingIngressBinding {
	return proxysvc.ExistingIngressBinding{ID: "server-owned-route", Hostname: "app.example.test", Path: "/api/", Service: "web", Owner: "operator", ProxyKind: "nginx", Status: "linked", Continuity: "host_port", Upstream: "http://127.0.0.1:32000/preserved/", SourcePath: "/etc/nginx/conf.d/operator.conf", SourceDigest: fakeContentDigest("config"), Selector: "nginx:4:http://127.0.0.1:32000/preserved/", Port: 3000}
}

func TestExistingIngressRecoveryKeepsExternalOwnershipAndBaseline(t *testing.T) {
	b := linkedIngressFixture()
	snapshot := runtimeReleaseSnapshot{Version: 1, Plan: RuntimePlanConfig{Strategy: StrategyStopFirst}}
	recovered := &RecoveredWorkload{Adoption: &WorkloadAdoption{BaselineDigest: fakeContentDigest("baseline"), Snapshot: mustJSON(snapshot)}}
	if err := AttachRecoveredIngress(recovered, []proxysvc.ExistingIngressBinding{b}); err != nil {
		t.Fatal(err)
	}
	if len(recovered.Configuration.Domains) != 0 || len(recovered.Adoption.BaselineConfiguration.Domains) != 0 {
		t.Fatal("external route entered managed domain plan")
	}
	if len(recovered.Configuration.Dependencies) != 1 || recovered.Configuration.Dependencies[0].Ownership != OwnershipLinked {
		t.Fatal("lost external ownership")
	}
	if json.Unmarshal(recovered.Adoption.Snapshot, &snapshot) != nil || len(snapshot.Dependencies) != 1 || recovered.Adoption.BaselineDigest == fakeContentDigest("baseline") {
		t.Fatal("baseline did not freeze route evidence")
	}
	b.Status, b.Continuity, b.PlannedChange = "blocked", "unverified", "Persist unsaved proxy configuration."
	if !errors.Is(AttachRecoveredIngress(recovered, []proxysvc.ExistingIngressBinding{b}), ErrRecoveryBlocked) {
		t.Fatal("unverified active route allowed adoption")
	}
}

func TestExistingIngressPlanRejectsEndpointChangesBeforeStop(t *testing.T) {
	b := linkedIngressFixture()
	plan := runtimeReleaseSnapshot{Plan: RuntimePlanConfig{ComposeProjectName: "original"}, Compose: &ResolvedComposeSnapshot{Services: []ResolvedComposeService{{Plan: ComposeServicePlan{Name: "web", Ports: []string{"127.0.0.1:32000:3000"}, Networks: map[string][]string{"exact-network": {"web", "owned-alias"}}}}}}}
	if err := validateExistingIngressPlan(plan, []proxysvc.ExistingIngressBinding{b}); err != nil {
		t.Fatal(err)
	}
	plan.Compose.Services[0].Plan.Ports = []string{"127.0.0.1:32001:3000"}
	if !errors.Is(validateExistingIngressPlan(plan, []proxysvc.ExistingIngressBinding{b}), proxysvc.ErrExistingIngressChanged) {
		t.Fatal("changed published port was not blocked before stop")
	}
	b.Continuity, b.Upstream, b.Network = "network_alias", "owned-alias:3000", "exact-network"
	if err := validateExistingIngressPlan(plan, []proxysvc.ExistingIngressBinding{b}); err != nil {
		t.Fatal(err)
	}
	plan.Compose.Services[0].Plan.Networks["exact-network"] = []string{"different-alias"}
	if !errors.Is(validateExistingIngressPlan(plan, []proxysvc.ExistingIngressBinding{b}), proxysvc.ErrExistingIngressChanged) {
		t.Fatal("changed exact alias was not blocked before stop")
	}
	b.Continuity, b.Upstream = "retarget", "172.18.0.2:3000"
	if err := validateExistingIngressPlan(plan, []proxysvc.ExistingIngressBinding{b}); err != nil {
		t.Fatal(err)
	}
	delete(plan.Compose.Services[0].Plan.Networks, "exact-network")
	if !errors.Is(validateExistingIngressPlan(plan, []proxysvc.ExistingIngressBinding{b}), proxysvc.ErrExistingIngressChanged) {
		t.Fatal("removed shared network was not blocked before stop")
	}
}

func TestExistingIngressSettingsRetainAndRejectClientReplacement(t *testing.T) {
	fixture := newPlanningStoreFixture(t)
	projectID, environmentID := insertConfigurationFixture(t, fixture)
	b := linkedIngressFixture()
	_, err := fixture.store.DB.Exec(`INSERT INTO deploy_dependencies(environment_id,release_id,kind,ownership,resource_kind,resource_id,config_json,created_at) VALUES(?,0,'ingress','linked',?,?,?,?)`, environmentID, existingIngressDependency, b.ID, string(mustJSON(b)), fixture.now.Unix())
	if err != nil {
		t.Fatal(err)
	}
	before, err := fixture.plans.EnvironmentConfiguration(t.Context(), projectID, environmentID)
	if err != nil || len(before.IngressBindings) != 1 {
		t.Fatalf("read exact bindings: %+v %v", before, err)
	}
	request := ConfigurationWriteRequest{Revision: before.Revision, Build: before.Build, Runtime: before.Runtime}
	after, err := fixture.plans.SaveEnvironmentConfiguration(t.Context(), projectID, environmentID, request)
	if err != nil || len(after.IngressBindings) != 1 || len(after.Dependencies) != 1 {
		t.Fatalf("omitted route identity did not survive save: %+v %v", after, err)
	}
	request.Revision = after.Revision
	request.Dependencies = append([]PlannedDependency(nil), after.Dependencies...)
	b.Upstream = "http://127.0.0.1:9999"
	request.Dependencies[0].Config = mustJSON(b)
	if _, err := fixture.plans.SaveEnvironmentConfiguration(t.Context(), projectID, environmentID, request); !errors.Is(err, ErrInvalidPlan) {
		t.Fatal("client replaced server-owned external endpoint")
	}
	removal, err := fixture.plans.RemovalPlan(t.Context(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range removal.Targets {
		if target.Owner == "proxy" || target.ResourceID == b.ID || target.ResourceID == b.SourcePath {
			t.Fatal("external route entered managed deletion plan")
		}
	}
}

func TestExistingLinkedDomainsNeverRenderManagedRouteOrProtection(t *testing.T) {
	route := deploymentRoute(1, []PlannedDomain{{Hostname: "external.example.test", Ownership: OwnershipLinked, HTTPS: true, Protection: &DomainProtection{Username: "operator", Hash: "secret-hash"}}, {Hostname: "managed.example.test", Ownership: OwnershipManaged}}, "127.0.0.1", 3000)
	if strings.Join(route.Domains, ",") != "managed.example.test" || route.TLS || len(route.BasicAuth) != 0 {
		t.Fatal("linked domain changed managed certificate or authentication")
	}
}
