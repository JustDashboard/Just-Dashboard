package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

const existingIngressDependency = "existing_proxy_route"

func AttachRecoveredIngress(recovered *RecoveredWorkload, bindings []proxysvc.ExistingIngressBinding) error {
	if recovered == nil || recovered.Adoption == nil {
		return ErrInvalidPlan
	}
	if len(bindings) == 0 {
		return nil
	}
	recovered.Adoption.IngressBindings = append([]proxysvc.ExistingIngressBinding(nil), bindings...)
	for _, binding := range bindings {
		if binding.Status == "blocked" {
			recovered.Adoption.Blockers = append(recovered.Adoption.Blockers, fmt.Sprintf("%s%s: %s", binding.Hostname, binding.Path, binding.PlannedChange))
			continue
		}
		if binding.Status != "linked" && binding.Status != "hint" && binding.Status != "unverified" {
			continue
		}
		if binding.Continuity == "retarget" {
			message := fmt.Sprintf("%s%s: %s", binding.Hostname, binding.Path, binding.PlannedChange)
			recovered.Adoption.Warnings = append(recovered.Adoption.Warnings, message)
			recovered.Adoption.Issues = append(recovered.Adoption.Issues, AdoptionIssue{Code: "existing_ingress_retarget", Message: message, Service: binding.Service, Field: "ingress"})
		}
		ownership := OwnershipLinked
		if binding.Status == "hint" {
			ownership = OwnershipObserved
		}
		dependency := PlannedDependency{Kind: "ingress", Ownership: ownership, ResourceKind: existingIngressDependency, ResourceID: binding.ID, Config: mustJSON(binding)}
		recovered.Configuration.Dependencies = append(recovered.Configuration.Dependencies, dependency)
		recovered.Adoption.BaselineConfiguration.Dependencies = append(recovered.Adoption.BaselineConfiguration.Dependencies, dependency)
	}
	var snapshot runtimeReleaseSnapshot
	if json.Unmarshal(recovered.Adoption.Snapshot, &snapshot) != nil {
		return ErrInvalidPlan
	}
	snapshot.Dependencies = append([]PlannedDependency(nil), recovered.Adoption.BaselineConfiguration.Dependencies...)
	recovered.Adoption.Snapshot = mustJSON(snapshot)
	recovered.Adoption.BaselineDigest = digestBytes([]byte(recovered.Adoption.BaselineDigest), mustJSON(bindings))
	if len(recovered.Adoption.Blockers) > 0 {
		return ErrRecoveryBlocked
	}
	return nil
}

func ingressBindingsFromDependencies(dependencies []PlannedDependency) ([]proxysvc.ExistingIngressBinding, error) {
	all, err := publicIngressBindingsFromDependencies(dependencies)
	if err != nil {
		return nil, err
	}
	bindings := []proxysvc.ExistingIngressBinding{}
	for _, binding := range all {
		if binding.Status == "linked" || binding.Status == "unverified" {
			bindings = append(bindings, binding)
		}
	}
	return bindings, nil
}

func publicIngressBindingsFromDependencies(dependencies []PlannedDependency) ([]proxysvc.ExistingIngressBinding, error) {
	bindings := []proxysvc.ExistingIngressBinding{}
	for _, dependency := range dependencies {
		if dependency.ResourceKind != existingIngressDependency {
			continue
		}
		var binding proxysvc.ExistingIngressBinding
		if dependency.Kind != "ingress" || json.Unmarshal(dependency.Config, &binding) != nil || binding.ID != dependency.ResourceID || !(((binding.Status == "linked" || binding.Status == "unverified") && dependency.Ownership == OwnershipLinked) || (binding.Status == "hint" && dependency.Ownership == OwnershipObserved)) {
			return nil, ErrInvalidPlan
		}
		bindings = append(bindings, binding)
	}
	return bindings, nil
}

type existingIngressController interface {
	VerifyExistingIngress(context.Context, []proxysvc.ExistingIngressBinding) error
	RecoverExistingIngress(context.Context, int64, int64, []proxysvc.ExistingIngressBinding) error
	ApplyExistingIngress(context.Context, int64, int64, []proxysvc.ExistingIngressBinding, []proxysvc.ExistingIngressTarget) error
	RestoreExistingIngress(context.Context, int64, int64, []proxysvc.ExistingIngressBinding) error
}

type existingIngressRuntime interface {
	ExistingIngressTargets(context.Context, ReleaseRuntime) ([]proxysvc.ExistingIngressTarget, error)
}

func (o *DockerRuntimeOwner) ExistingIngressTargets(ctx context.Context, runtime ReleaseRuntime) ([]proxysvc.ExistingIngressTarget, error) {
	if o == nil || o.client == nil {
		return nil, ErrRuntimeUnavailable
	}
	ids := runtimeContainerIDs(runtime)
	var metadata dockerReleaseRuntimeMetadata
	if json.Unmarshal(runtime.Metadata, &metadata) != nil {
		return nil, ErrInvalidPlan
	}
	if runtime.Kind == "compose" && metadata.ProjectName != "" {
		containers, err := o.client.ListContainersWithLabels(ctx, map[string]string{"com.docker.compose.project": metadata.ProjectName})
		if err != nil {
			return nil, err
		}
		ids = nil
		allowed := map[string]bool{}
		for _, service := range metadata.ServiceNames {
			allowed[service] = true
		}
		for _, entry := range metadata.BaselineContainers {
			allowed[entry.Service] = true
		}
		for _, container := range containers {
			service := container.Labels["com.docker.compose.service"]
			if !allowed[service] || strings.EqualFold(container.Labels["com.docker.compose.oneoff"], "true") {
				continue
			}
			owned := container.Labels["io.just-dashboard.managed"] == "true" && container.Labels["io.just-dashboard.environment-id"] == strconv.FormatInt(runtime.EnvironmentID, 10) && container.Labels["io.just-dashboard.release-id"] == strconv.FormatInt(runtime.ReleaseID, 10)
			for _, entry := range metadata.BaselineContainers {
				owned = owned || entry.ID == container.ID
			}
			if !owned {
				return nil, ErrRuntimeUnavailable
			}
			ids = append(ids, container.ID)
		}
	}
	targets := []proxysvc.ExistingIngressTarget{}
	for _, id := range ids {
		detail, err := o.client.Inspect(ctx, id)
		if err != nil {
			return nil, err
		}
		service := detail.Labels["com.docker.compose.service"]
		if service == "" {
			service = "app"
		}
		ports := map[int]bool{}
		for _, port := range detail.Ports {
			if port.Type != "" && port.Type != "tcp" {
				continue
			}
			ports[int(port.PrivatePort)] = true
			if port.PublicPort > 0 {
				targets = append(targets, proxysvc.ExistingIngressTarget{Service: service, Host: port.IP, Port: int(port.PublicPort), ContainerPort: int(port.PrivatePort)})
			}
		}
		for _, network := range detail.NetworkList {
			targets = append(targets, proxysvc.ExistingIngressTarget{Service: service, ContainerID: detail.ID, Stopped: detail.State != "running", Network: network.Name, Address: network.IPAddress})
			for _, alias := range network.Aliases {
				targets = append(targets, proxysvc.ExistingIngressTarget{Service: service, ContainerID: detail.ID, Stopped: detail.State != "running", Network: network.Name, Alias: alias, Address: network.IPAddress})
			}
			for port := range ports {
				targets = append(targets, proxysvc.ExistingIngressTarget{Service: service, ContainerID: detail.ID, Stopped: detail.State != "running", Network: network.Name, Address: network.IPAddress, ContainerPort: port})
				for _, alias := range network.Aliases {
					targets = append(targets, proxysvc.ExistingIngressTarget{Service: service, ContainerID: detail.ID, Stopped: detail.State != "running", Network: network.Name, Alias: alias, Address: network.IPAddress, ContainerPort: port})
				}
			}
		}
	}
	return targets, nil
}

func (o *NativeRuntimeOwner) ExistingIngressTargets(ctx context.Context, runtime ReleaseRuntime) ([]proxysvc.ExistingIngressTarget, error) {
	if runtime.Kind == "pm2" || runtime.Kind == "systemd" {
		return []proxysvc.ExistingIngressTarget{{Service: "app", Host: runtime.Host, Port: runtime.Port}}, nil
	}
	reader, ok := o.docker.(existingIngressRuntime)
	if !ok {
		return nil, ErrRuntimeUnavailable
	}
	return reader.ExistingIngressTargets(ctx, runtime)
}

func (e *NormalizedStepExecutor) verifyExistingIngressBeforeStop(ctx context.Context, execution StepExecution, snapshot runtimeReleaseSnapshot) error {
	bindings, err := ingressBindingsFromDependencies(snapshot.Dependencies)
	if err != nil {
		return err
	}
	if len(bindings) == 0 {
		return nil
	}
	if err := validateExistingIngressPlan(snapshot, bindings); err != nil {
		return err
	}
	controller, ok := e.proxy.(existingIngressController)
	if !ok {
		return errors.New("existing proxy verification is unavailable")
	}
	if err := controller.RecoverExistingIngress(ctx, execution.Run.EnvironmentID, execution.Run.ID, bindings); err != nil {
		return err
	}
	return controller.VerifyExistingIngress(ctx, bindings)
}

// Check immutable desired configuration before replacing the live runtime.
// Final inspection after start still verifies Docker's actual network aliases.
func validateExistingIngressPlan(snapshot runtimeReleaseSnapshot, bindings []proxysvc.ExistingIngressBinding) error {
	for _, binding := range bindings {
		if binding.Status != "linked" {
			return fmt.Errorf("%w: the original proxy link is unverified", proxysvc.ErrExistingIngressChanged)
		}
		raw := binding.Upstream
		if !strings.Contains(raw, "://") {
			raw = "http://" + raw
		}
		endpoint, err := url.Parse(raw)
		if err != nil {
			return ErrInvalidPlan
		}
		hostPort, err := strconv.Atoi(endpoint.Port())
		if err != nil {
			return ErrInvalidPlan
		}
		matched := false
		if snapshot.Compose == nil {
			matched = binding.Continuity == "host_port" && binding.Service == "app" && snapshot.Plan.HostPort == hostPort && compatibleIngressHost(endpoint.Hostname(), snapshot.Plan.BindAddress)
			for _, port := range snapshot.Plan.Ports {
				matched = matched || (binding.Continuity == "host_port" && binding.Service == "app" && port.effectiveProtocol() == "tcp" && port.HostPort == hostPort && compatibleIngressHost(endpoint.Hostname(), port.BindAddress))
			}
		} else {
			for _, resolved := range snapshot.Compose.Services {
				service := resolved.Plan
				if service.Name != binding.Service {
					continue
				}
				ports := map[int]bool{}
				for _, exposed := range service.ExposedPorts {
					ports[exposed] = true
				}
				for _, value := range service.Ports {
					if strings.HasSuffix(value, "/udp") || strings.ContainsAny(value, "${}") {
						continue
					}
					published, internal := composePort(value)
					ports[internal] = true
					parts := strings.Split(value, ":")
					address := ""
					if len(parts) > 2 {
						address = strings.Trim(strings.Join(parts[:len(parts)-2], ":"), "[]")
					}
					matched = matched || (binding.Continuity == "host_port" && published == hostPort && compatibleIngressHost(endpoint.Hostname(), address))
				}
				for network, aliases := range service.Networks {
					if network != binding.Network && snapshot.Plan.ComposeProjectName+"_"+network != binding.Network {
						continue
					}
					portPreserved := ports[binding.Port] || len(ports) == 0
					if binding.Continuity == "retarget" && portPreserved {
						matched = true
					}
					if binding.Continuity == "network_alias" && portPreserved {
						for _, alias := range aliases {
							matched = matched || alias == endpoint.Hostname()
						}
					}
				}
			}
		}
		if !matched {
			return fmt.Errorf("%w: %s%s no longer preserves its exact service endpoint", proxysvc.ErrExistingIngressChanged, binding.Hostname, binding.Path)
		}
	}
	return nil
}

func compatibleIngressHost(upstream, planned string) bool {
	if upstream == planned {
		return true
	}
	loopback := upstream == "localhost" || upstream == "127.0.0.1" || upstream == "::1"
	return loopback && (planned == "" || planned == "0.0.0.0" || planned == "::" || planned == "127.0.0.1" || planned == "::1")
}

func (e *NormalizedStepExecutor) applyExistingIngress(ctx context.Context, release Release, runtime ReleaseRuntime, snapshot runtimeReleaseSnapshot) error {
	bindings, err := ingressBindingsFromDependencies(snapshot.Dependencies)
	if err != nil {
		return err
	}
	if len(bindings) == 0 {
		return nil
	}
	controller, ok := e.proxy.(existingIngressController)
	if !ok {
		return ErrRuntimeUnavailable
	}
	var targets []proxysvc.ExistingIngressTarget
	if reader, ok := e.runtime.(existingIngressRuntime); ok {
		targets, err = reader.ExistingIngressTargets(ctx, runtime)
		if err != nil {
			return err
		}
	} else {
		targets = []proxysvc.ExistingIngressTarget{{Service: "app", Host: runtime.Host, Port: runtime.Port, ContainerPort: snapshot.Plan.InternalPort}}
	}
	return controller.ApplyExistingIngress(ctx, release.EnvironmentID, release.ID, bindings, targets)
}

func (e *NormalizedStepExecutor) restoreExistingIngress(ctx context.Context, release Release, snapshot runtimeReleaseSnapshot) error {
	bindings, err := ingressBindingsFromDependencies(snapshot.Dependencies)
	if err != nil {
		return err
	}
	if len(bindings) == 0 {
		return nil
	}
	controller, ok := e.proxy.(existingIngressController)
	if !ok {
		return ErrRuntimeUnavailable
	}
	return controller.RestoreExistingIngress(ctx, release.EnvironmentID, release.ID, bindings)
}

func managedDomains(domains []PlannedDomain) []PlannedDomain {
	result := []PlannedDomain{}
	for _, domain := range domains {
		if domain.Ownership != OwnershipLinked {
			result = append(result, domain)
		}
	}
	return result
}
