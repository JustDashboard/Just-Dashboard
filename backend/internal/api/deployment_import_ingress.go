package api

import (
	"context"
	"regexp"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

func (s *Server) attachWorkloadIngress(ctx context.Context, candidate *deploy.WorkloadCandidate, recovered *deploy.RecoveredWorkload, recoverErr error) (*deploy.RecoveredWorkload, error) {
	if recoverErr != nil || recovered == nil || recovered.Adoption == nil || s.modules.proxy == nil {
		return recovered, recoverErr
	}
	targets := []proxysvc.ExistingIngressTarget{}
	hints := []proxysvc.ExistingIngressBinding{}
	if candidate.Kind == "stack" || candidate.Kind == "container" {
		for _, service := range candidate.Services {
			detail, err := s.modules.docker.Inspect(ctx, service.ResourceID)
			if err != nil {
				return recovered, deploy.ErrSourceUnavailable
			}
			name := detail.Labels["com.docker.compose.service"]
			if name == "" {
				name = "app"
			}
			for label, value := range detail.Labels {
				if strings.HasPrefix(label, "traefik.http.routers.") && strings.HasSuffix(label, ".rule") {
					for _, host := range externalIngressHostLabel.FindAllStringSubmatch(value, -1) {
						hints = append(hints, proxysvc.ExistingIngressBinding{ID: "traefik:" + label + ":" + host[1], Hostname: host[1], Path: "/", Service: name, Owner: "Traefik", ProxyKind: "traefik", Status: "unverified", Continuity: "unverified", PlannedChange: "A Traefik routing label names this host. Verify its active router, middleware and upstream externally before Deploy changes; the dashboard will not replace them."})
					}
				}
			}
			ports := map[int]bool{}
			for _, port := range detail.Ports {
				if port.Type != "" && port.Type != "tcp" {
					continue
				}
				ports[int(port.PrivatePort)] = true
				if port.PublicPort > 0 {
					targets = append(targets, proxysvc.ExistingIngressTarget{Service: name, Host: port.IP, Port: int(port.PublicPort), ContainerPort: int(port.PrivatePort)})
				}
			}
			for _, network := range detail.NetworkList {
				targets = append(targets, proxysvc.ExistingIngressTarget{Service: name, ContainerID: detail.ID, Stopped: detail.State != "running", Network: network.Name, Address: network.IPAddress})
				for _, alias := range network.Aliases {
					targets = append(targets, proxysvc.ExistingIngressTarget{Service: name, ContainerID: detail.ID, Stopped: detail.State != "running", Network: network.Name, Alias: alias, Address: network.IPAddress})
				}
				for port := range ports {
					targets = append(targets, proxysvc.ExistingIngressTarget{Service: name, ContainerID: detail.ID, Stopped: detail.State != "running", Network: network.Name, Address: network.IPAddress, ContainerPort: port})
					for _, alias := range network.Aliases {
						targets = append(targets, proxysvc.ExistingIngressTarget{Service: name, ContainerID: detail.ID, Stopped: detail.State != "running", Network: network.Name, Alias: alias, Address: network.IPAddress, ContainerPort: port})
					}
				}
			}
		}
	} else {
		for _, service := range candidate.Services {
			for _, port := range service.Ports {
				if port.Protocol == "" || port.Protocol == "tcp" {
					targets = append(targets, proxysvc.ExistingIngressTarget{Service: "app", Host: port.HostIP, Port: port.HostPort, ContainerPort: port.ContainerPort})
				}
			}
		}
	}
	bindings, err := s.modules.proxy.CaptureExistingIngress(ctx, targets)
	if err != nil {
		// An unavailable manager is an explicit silence, never a hostname
		// inferred from application variables or a flattened proxy listing.
		recovered.Adoption.Warnings = append(recovered.Adoption.Warnings, "Existing external proxy routes could not be fully inspected. Verify their upstream continuity before Deploy changes.")
		recovered.Adoption.Issues = append(recovered.Adoption.Issues, deploy.AdoptionIssue{Code: "external_ingress_unverified", Message: "Existing external proxy routes could not be fully inspected. Verify their upstream continuity before Deploy changes.", Field: "ingress"})
		hints = append(hints, proxysvc.ExistingIngressBinding{ID: "unverified-proxy:" + recovered.Adoption.Key, Path: "/", Owner: "Existing proxy", ProxyKind: "unknown", Status: "unverified", Continuity: "unverified", PlannedChange: "The existing proxy manager could not provide verified active route evidence. Restore its configuration visibility before Deploy changes; no runtime will be stopped while this link is unverified."})
		return recovered, deploy.AttachRecoveredIngress(recovered, hints)
	}
	return recovered, deploy.AttachRecoveredIngress(recovered, append(bindings, hints...))
}

var externalIngressHostLabel = regexp.MustCompile("Host\\(\\s*[`\"']([A-Za-z0-9.-]+)[`\"']\\s*\\)")
