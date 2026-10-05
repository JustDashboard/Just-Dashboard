package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

type operationsIngressObserver struct {
	*countingProxyObserver
	calls    atomic.Int64
	bindings []proxysvc.ExistingIngressBinding
	err      error
}

func (o *operationsIngressObserver) VerifyExistingIngress(_ context.Context, bindings []proxysvc.ExistingIngressBinding) error {
	o.calls.Add(1)
	o.bindings = append([]proxysvc.ExistingIngressBinding(nil), bindings...)
	return o.err
}

func operationsIngressSnapshot(t *testing.T, bindings []proxysvc.ExistingIngressBinding) runtimeReleaseSnapshot {
	t.Helper()
	var snapshot runtimeReleaseSnapshot
	if err := json.Unmarshal(operationsSnapshot(), &snapshot); err != nil {
		t.Fatal(err)
	}
	snapshot.Domains, snapshot.Dependencies, snapshot.Plan.Mounts = nil, nil, nil
	for _, binding := range bindings {
		ownership := OwnershipLinked
		if binding.Status == "hint" {
			ownership = OwnershipObserved
		}
		snapshot.Dependencies = append(snapshot.Dependencies, PlannedDependency{Kind: "ingress", Ownership: ownership, ResourceKind: existingIngressDependency, ResourceID: binding.ID, Config: mustJSON(binding)})
	}
	return snapshot
}

func TestOperationsShowExactExistingProxyServicesWithoutManagedAuthority(t *testing.T) {
	api, jobs := linkedIngressFixture(), linkedIngressFixture()
	api.ID, api.Service, api.Path, api.HTTPS = "route-api", "api", "/api/", true
	jobs.ID, jobs.Service, jobs.Path = "route-jobs", "jobs", "/jobs/"
	snapshot := operationsIngressSnapshot(t, []proxysvc.ExistingIngressBinding{jobs, api})
	fixture, _ := liveOperationsSnapshotFixture(t, mustJSON(snapshot))
	owners, dependencies, inventory := healthyOperationsOwners(fixture.envID)
	inventory.vhostErr, inventory.certificateErr = errors.New("managed inventory must not be queried"), errors.New("certificate inventory must not be queried")
	proxy := &operationsIngressObserver{countingProxyObserver: inventory}
	owners.Proxy = proxy
	operations, err := fixture.runs.Operations(t.Context(), owners, fixture.deploymentSummary(t))
	if err != nil {
		t.Fatal(err)
	}
	if operations.Domains.Status != statusAvailable || len(operations.Domains.Domains) != 2 || operations.Domains.Reason != "" || operations.Domains.SiteName != "" {
		t.Fatal("existing routes were reported as absent or managed", operations.Domains)
	}
	for i, row := range operations.Domains.Domains {
		want := []proxysvc.ExistingIngressBinding{api, jobs}[i]
		if row.ID != want.ID || row.Hostname != want.Hostname || row.Path != want.Path || row.Service != want.Service || row.Route != "served" || row.Ownership != OwnershipLinked || row.ServedBy != want.Owner || row.ProxyKind != want.ProxyKind || row.Continuity != want.Continuity {
			t.Fatal("hostname/path/service binding lost or misassociated", row)
		}
		if row.CertificateName != "" || row.CertificateLink != "" || (row.HTTPS && row.Certificate != statusUnavailable) || row.Protected {
			t.Fatal("external route acquired managed TLS/auth evidence", row)
		}
	}
	if proxy.calls.Load() != 1 || len(proxy.bindings) != 2 || inventory.availabilityCalls.Load() != 0 || inventory.vhostCalls.Load() != 0 || inventory.certificateCalls.Load() != 0 || dependencies.calls.Load() != 0 || len(operations.Dependencies.Items) != 0 {
		t.Fatal("external routes leaked into managed inventories or repeated reads")
	}
	for _, finding := range operations.Diagnosis.Findings {
		if strings.HasPrefix(finding.Code, "domain_") || strings.HasPrefix(finding.Code, "certificate_") || strings.HasPrefix(finding.Code, "dependency_") {
			t.Fatal("linked route generated a false missing/foreign finding", finding)
		}
	}
}

func TestOperationsExistingProxyDriftOrMissingReaderRemainsUnassessed(t *testing.T) {
	for _, scenario := range []string{"changed", "unverified", "missing reader"} {
		t.Run(scenario, func(t *testing.T) {
			binding := linkedIngressFixture()
			binding.HTTPS = true
			observer := &operationsIngressObserver{countingProxyObserver: &countingProxyObserver{}}
			owners := OperationsOwners{Proxy: observer}
			if scenario == "unverified" {
				binding.Status, binding.PlannedChange = "unverified", "Restore authoritative manager visibility."
			}
			if scenario == "changed" {
				observer.err = proxysvc.ErrExistingIngressChanged
			}
			if scenario == "missing reader" {
				owners.Proxy = &countingProxyObserver{}
			}
			result := observeReleaseDomainRoutes(t.Context(), owners, operationsIngressSnapshot(t, []proxysvc.ExistingIngressBinding{binding}), 1)
			if len(result.Domains) != 1 || result.Domains[0].Hostname != binding.Hostname || result.Domains[0].Route != statusUnavailable || result.Domains[0].Certificate != statusUnavailable || result.Domains[0].Detail == "" {
				t.Fatal("missing evidence became absence or served route", result)
			}
			if scenario == "unverified" && observer.calls.Load() != 0 {
				t.Fatal("unverified link was treated as authoritative")
			}
		})
	}
}

func TestOperationsDoNotInventDomainForUnassociatedManagerHint(t *testing.T) {
	binding := proxysvc.ExistingIngressBinding{ID: "manager-presence", Owner: "Traefik", Path: "/", Status: "hint", Continuity: "unverified"}
	observer := &operationsIngressObserver{countingProxyObserver: &countingProxyObserver{}}
	result := observeReleaseDomainRoutes(t.Context(), OperationsOwners{Proxy: observer}, operationsIngressSnapshot(t, []proxysvc.ExistingIngressBinding{binding}), 1)
	if len(result.Domains) != 0 || observer.calls.Load() != 0 {
		t.Fatal("manager presence became a hostname route", result)
	}
}

func TestOperationsUnverifiedManagerCannotClaimNoExistingDomain(t *testing.T) {
	binding := proxysvc.ExistingIngressBinding{ID: "unverified-manager", Owner: "Existing proxy", Status: "unverified", Continuity: "unverified"}
	result := observeReleaseDomainRoutes(t.Context(), OperationsOwners{}, operationsIngressSnapshot(t, []proxysvc.ExistingIngressBinding{binding}), 1)
	if result.Status != statusUnavailable || result.Reason == "" || len(result.Domains) != 0 {
		t.Fatal("unknown active proxy evidence became verified empty domains", result)
	}
}

func TestOperationsNamedRouteDoesNotHideUnverifiedManager(t *testing.T) {
	unknown := proxysvc.ExistingIngressBinding{ID: "unverified-manager", Owner: "Existing proxy", Status: "unverified", Continuity: "unverified"}
	observer := &operationsIngressObserver{countingProxyObserver: &countingProxyObserver{}}
	result := observeReleaseDomainRoutes(t.Context(), OperationsOwners{Proxy: observer}, operationsIngressSnapshot(t, []proxysvc.ExistingIngressBinding{linkedIngressFixture(), unknown}), 1)
	if result.Status != statusUnavailable || result.Reason == "" || len(result.Domains) != 1 || result.Domains[0].Route != "served" {
		t.Fatal("a verified route concealed another unknown proxy manager", result)
	}
}
