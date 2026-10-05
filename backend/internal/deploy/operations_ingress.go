package deploy

import (
	"context"
	"sort"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// Linked ingress is frozen separately from managed domains. Recheck its
// original owner's evidence without claiming certificate or route authority.
func observeReleaseDomainRoutes(ctx context.Context, owners OperationsOwners, snapshot runtimeReleaseSnapshot, environmentID int64) DomainSummary {
	result := observeDomainRoutes(ctx, owners, snapshot.Domains, environmentID)
	bindings, err := publicIngressBindingsFromDependencies(snapshot.Dependencies)
	if err != nil {
		result.Status, result.Reason = statusUnavailable, "The live release's existing proxy bindings could not be read."
		return result
	}
	linked := []proxysvc.ExistingIngressBinding{}
	named := false
	for _, binding := range bindings {
		named = named || binding.Hostname != ""
		if binding.Status == "linked" && binding.Hostname != "" {
			linked = append(linked, binding)
		}
	}
	if !named {
		return result
	}
	controller, available := owners.Proxy.(interface {
		VerifyExistingIngress(context.Context, []proxysvc.ExistingIngressBinding) error
	})
	verified := false
	if available && len(linked) > 0 {
		readCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		verified = controller.VerifyExistingIngress(readCtx, linked) == nil
		cancel()
	}
	if len(snapshot.Domains) == 0 {
		result.Status, result.Reason = statusAvailable, ""
	}
	for _, binding := range bindings {
		if binding.Hostname == "" {
			continue
		}
		row := DomainRoute{
			ID: binding.ID, Hostname: binding.Hostname, Path: binding.Path, Service: binding.Service,
			ProxyKind: binding.ProxyKind, Continuity: binding.Continuity,
			HTTPS: binding.HTTPS, Ownership: OwnershipLinked, ServedBy: binding.Owner,
			Route: statusUnavailable, Certificate: statusUnavailable, DeepLink: "/proxy",
			Detail: "The original proxy route or active configuration could not be verified. Inspect it in its existing manager.",
		}
		if binding.Status == "hint" {
			row.Ownership = OwnershipObserved
		}
		if !binding.HTTPS {
			row.Certificate = "not requested"
		}
		if binding.Status == "linked" && verified {
			row.Route = "served"
			row.Detail = "Existing proxy continuity is verified. TLS and authentication remain owned by the original proxy manager."
		} else if binding.Status != "linked" && binding.PlannedChange != "" {
			row.Detail = binding.PlannedChange
		} else if !available {
			row.Detail = "The original proxy reader is unavailable. Route continuity has not been assessed."
		}
		result.Domains = append(result.Domains, row)
	}
	sort.Slice(result.Domains, func(i, j int) bool {
		a, b := result.Domains[i], result.Domains[j]
		if a.Hostname != b.Hostname {
			return a.Hostname < b.Hostname
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.Service != b.Service {
			return a.Service < b.Service
		}
		return a.ID < b.ID
	})
	return result
}
