package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// A service domain is an ordinary domain of a Compose deployment that the
// route sends to another of the stack's services: a media stack's Seerr at
// its own address beside the Jellyfin the deployment's own names reach. It is
// in the route like every other name — the certificate, the DNS findings and
// every rule of the route cover it — and only the server its requests are
// passed to differs. That server is the service's publication on this host's
// loopback, which a blueprint's loopback port and a user Compose file's
// 127.0.0.1:H:C both make, read from the started containers
// (composeServicePorts) after port recovery has moved anything.

// normalizeServiceDomains writes the primary service as no service, and its
// own port as no port, so a domain naming what the route serves anyway is
// the domain every other one is and keeps their bytes and digest. primary is
// empty while the plan does not name it; internalPort is the primary's port.
func normalizeServiceDomains(domains []PlannedDomain, primary string, internalPort int) []PlannedDomain {
	for index := range domains {
		domain := &domains[index]
		if primary != "" && domain.Service == primary {
			domain.Service = ""
		}
		if domain.Service == "" && internalPort > 0 && domain.Port == internalPort {
			domain.Port = 0
		}
	}
	return domains
}

// ownDomain is the first named domain the application itself answers: the
// deployment's own address, never a service domain that sorts before it.
func ownDomain(domains []PlannedDomain) (PlannedDomain, bool) {
	for _, domain := range domains {
		if strings.TrimSpace(domain.Hostname) != "" && !domain.ServiceDomain() {
			return domain, true
		}
	}
	return PlannedDomain{}, false
}

// validateServiceDomainShapes is what a plan's own validation can say about
// its service domains without the Compose file they name: where a domain may
// route to a service at all.
func validateServiceDomainShapes(c PlanConfiguration) error {
	targets := map[string]bool{}
	for index, domain := range c.Domains {
		if !domain.ServiceDomain() {
			continue
		}
		field := fmt.Sprintf("domains[%d].service", index)
		hostname := strings.ToLower(strings.TrimSpace(domain.Hostname))
		switch {
		case c.Build.Method != BuildCompose:
			return invalidField(field, "%s routes to a Compose service, and this plan does not build a Compose stack", domain.Hostname)
		case domain.Service != "" && !validComposeServiceName(domain.Service):
			return invalidField(field, "%q is not a Compose service name", domain.Service)
		case domain.Port < 0 || domain.Port > 65535:
			return invalidField(fmt.Sprintf("domains[%d].port", index), "a container port is between 1 and 65535")
		case domain.Redirects():
			return invalidField(field, "%s redirects visitors, so it routes to no service; remove the redirect or the service", domain.Hostname)
		case strings.HasPrefix(hostname, "*."):
			return invalidField(field, "a wildcard name cannot route to a service: name each address the service answers")
		case c.Runtime.Strategy == StrategyCanary:
			// The canary's route shares the release's requests with the
			// candidate's, and the proxy refuses a name another service
			// answers beside it (proxysvc.validateHostUpstreams), so every
			// release after the first would fail its canary step.
			return invalidField(field, "%s is answered by another service, which a canary has no release to share; choose another release strategy or remove the domain's service", domain.Hostname)
		}
		targets[domain.Service+"\x00"+strconv.Itoa(domain.Port)] = true
	}
	if len(targets) > proxysvc.MaxHostUpstreams {
		return invalidField("domains", "the domains route to at most %d services of their own", proxysvc.MaxHostUpstreams)
	}
	return nil
}

// composeLoopbackPorts are the container ports a service of the analysis
// publishes on 127.0.0.1 over TCP, from the port strings Compose analysis
// keeps: 127.0.0.1:H:C, 127.0.0.1::C, with /tcp or no protocol.
func composeLoopbackPorts(service ComposeServicePlan) []int {
	var ports []int
	for _, value := range service.Ports {
		spec, protocol, _ := strings.Cut(value, "/")
		if protocol != "" && protocol != "tcp" {
			continue
		}
		host, rest, ok := strings.Cut(spec, ":")
		if !ok || host != "127.0.0.1" {
			continue
		}
		index := strings.LastIndex(rest, ":")
		if index < 0 {
			continue
		}
		container, err := strconv.Atoi(rest[index+1:])
		if err != nil || container < 1 || container > 65535 {
			continue
		}
		ports = append(ports, container)
	}
	sort.Ints(ports)
	return ports
}

// validateServiceDomains holds the service domains to the Compose stack the
// plan builds: each names a service of it, which publishes the container
// port on 127.0.0.1 — or, with no port named, exactly one such port. It also
// returns the domains with the analysis's primary service written as none,
// which is the plan's choice when the plan names none.
func validateServiceDomains(c PlanConfiguration, analysis *ComposeAnalysis) ([]PlannedDomain, error) {
	routed := false
	for _, domain := range c.Domains {
		routed = routed || domain.ServiceDomain()
	}
	if !routed {
		return c.Domains, nil
	}
	domains := append([]PlannedDomain(nil), c.Domains...)
	if analysis == nil {
		return nil, invalidField("domains", "a domain routes to a Compose service, and this plan has no Compose analysis to find it in; inspect the source again")
	}
	primary, err := chosenComposePrimaryService(c.Build, *analysis)
	if err != nil {
		return nil, invalidField("configuration.build.primaryService", "%v", err)
	}
	domains = normalizeServiceDomains(domains, primary, c.Runtime.InternalPort)
	byName := map[string]ComposeServicePlan{}
	for _, service := range analysis.Services {
		byName[service.Name] = service
	}
	for index, domain := range domains {
		if !domain.ServiceDomain() {
			continue
		}
		name := domain.Service
		if name == "" {
			name = primary
		}
		service, ok := byName[name]
		if !ok {
			return nil, invalidField(fmt.Sprintf("domains[%d].service", index), "%s routes to %s, which is not a service of the Compose stack", domain.Hostname, name)
		}
		ports := composeLoopbackPorts(service)
		switch {
		case domain.Port != 0 && !containsInt(ports, domain.Port):
			return nil, invalidField(fmt.Sprintf("domains[%d].port", index), "%s routes to port %d of %s, which the stack does not publish on 127.0.0.1; publish it as 127.0.0.1:<host port>:%d", domain.Hostname, domain.Port, name, domain.Port)
		case domain.Port == 0 && len(ports) == 0:
			return nil, invalidField(fmt.Sprintf("domains[%d].service", index), "%s routes to %s, which publishes no port on 127.0.0.1; publish its web port as 127.0.0.1:<host port>:<port>", domain.Hostname, name)
		case domain.Port == 0 && len(ports) > 1:
			return nil, invalidField(fmt.Sprintf("domains[%d].port", index), "%s publishes %s on 127.0.0.1; name the port %s serves", name, joinInts(ports), domain.Hostname)
		}
	}
	return domains, nil
}

func containsInt(values []int, value int) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func joinInts(values []int) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strconv.Itoa(value))
	}
	return strings.Join(parts, ", ")
}

// serviceDomainFindings is preflight's word on the service domains: the
// same rule the plan is saved under, refused before anything is built.
func serviceDomainFindings(draft *Draft, configuration PlanConfiguration) []PreflightFinding {
	var analysis *ComposeAnalysis
	if draft != nil && draft.Data.Detection != nil {
		analysis = draft.Data.Detection.Compose
	}
	if _, err := validateServiceDomains(configuration, analysis); err != nil {
		var invalid *ValidationError
		field := "configuration.domains"
		if errors.As(err, &invalid) && invalid.Field != "" {
			field = "configuration." + invalid.Field
		}
		return []PreflightFinding{finding("domain_service_unpublished", PreflightBlocked,
			"A domain routes to a Compose service that does not publish it", err.Error(),
			"The route sends a service domain to the service's publication on 127.0.0.1, so without one nothing answers it.",
			"Publish the service's web port on 127.0.0.1 in the Compose file, or remove the domain's service.",
			"proxy", field)}
	}
	return nil
}

// composeServicePort is one TCP port a Compose service's container publishes
// on this host's loopback, as the runtime record keeps it.
type composeServicePort struct {
	Service       string `json:"service"`
	ContainerPort int    `json:"containerPort"`
	HostPort      int    `json:"hostPort"`
}

// composeServicePorts reads every loopback TCP publication of the release's
// containers, in service and port order. It reads the started containers,
// after Compose's port recovery has moved anything it had to.
func composeServicePorts(containers []dockerx.Container, ids []string) []composeServicePort {
	own := map[string]bool{}
	for _, id := range ids {
		own[id] = true
	}
	seen := map[composeServicePort]bool{}
	ports := []composeServicePort{}
	for _, container := range containers {
		service := container.Labels["com.docker.compose.service"]
		if !own[container.ID] || service == "" {
			continue
		}
		for _, port := range container.Ports {
			if port.Type != "tcp" || port.PublicPort == 0 || port.IP != "127.0.0.1" {
				continue
			}
			entry := composeServicePort{Service: service, ContainerPort: int(port.PrivatePort), HostPort: int(port.PublicPort)}
			if !seen[entry] {
				seen[entry] = true
				ports = append(ports, entry)
			}
		}
	}
	sort.Slice(ports, func(i, j int) bool {
		if ports[i].Service != ports[j].Service {
			return ports[i].Service < ports[j].Service
		}
		if ports[i].ContainerPort != ports[j].ContainerPort {
			return ports[i].ContainerPort < ports[j].ContainerPort
		}
		return ports[i].HostPort < ports[j].HostPort
	})
	return ports
}

// errServicePortMissing is a service domain whose service the release does
// not publish where the route has to send it.
var errServicePortMissing = errors.New("service_port_missing")

// serviceHostUpstreams are the route's host upstreams for a release: each
// service domain's service and port, found in what the release's containers
// publish on loopback, one entry per server with every name it serves. A
// domain whose service is the primary and whose port is the primary's is the
// release's own, as every other domain is.
func serviceHostUpstreams(snapshot runtimeReleaseSnapshot, runtime ReleaseRuntime) ([]proxysvc.HostUpstream, error) {
	var services []PlannedDomain
	for _, domain := range snapshot.Domains {
		if domain.ServiceDomain() && !domain.Redirects() {
			services = append(services, domain)
		}
	}
	if len(services) == 0 {
		return nil, nil
	}
	if runtime.Kind != "compose" || snapshot.Compose == nil {
		return nil, fmt.Errorf("%w: %s routes to a Compose service, and this release is not a Compose stack", errServicePortMissing, services[0].Hostname)
	}
	metadata, err := decodeDockerRuntimeMetadata(runtime.Metadata)
	if err != nil {
		return nil, err
	}
	primary := ""
	if len(snapshot.Compose.Services) > 0 {
		primary = snapshot.Compose.Services[0].Plan.Name
	}
	if snapshot.Compose.PrimaryService != "" {
		primary = snapshot.Compose.PrimaryService
	}
	byUpstream := map[string]int{}
	var entries []proxysvc.HostUpstream
	for _, domain := range services {
		service := domain.Service
		if service == "" {
			service = primary
		}
		if service == primary && (domain.Port == 0 || domain.Port == snapshot.Plan.InternalPort) {
			continue
		}
		var published []composeServicePort
		for _, port := range metadata.ServicePorts {
			if port.Service == service && (domain.Port == 0 || port.ContainerPort == domain.Port) {
				published = append(published, port)
			}
		}
		switch {
		case len(published) == 0 && domain.Port != 0:
			return nil, fmt.Errorf("%w: %s routes to port %d of %s, which the release does not publish on 127.0.0.1", errServicePortMissing, domain.Hostname, domain.Port, service)
		case len(published) == 0:
			return nil, fmt.Errorf("%w: %s routes to %s, which the release does not publish on 127.0.0.1", errServicePortMissing, domain.Hostname, service)
		case len(published) > 1 && domain.Port == 0:
			return nil, fmt.Errorf("%w: %s routes to %s, which publishes several ports on 127.0.0.1; name the one it serves", errServicePortMissing, domain.Hostname, service)
		}
		upstream := "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(published[0].HostPort))
		hostname := strings.ToLower(domain.Hostname)
		if at, ok := byUpstream[upstream]; ok {
			entries[at].Hostnames = append(entries[at].Hostnames, hostname)
			continue
		}
		byUpstream[upstream] = len(entries)
		entries = append(entries, proxysvc.HostUpstream{Hostnames: []string{hostname}, Upstream: upstream})
	}
	return entries, nil
}

// servicePortFailure is the step result of a service domain the release
// cannot route: its service publishes no port where the route has to send
// it.
func servicePortFailure(err error) (string, string) {
	return "service_port_missing", strings.TrimPrefix(err.Error(), errServicePortMissing.Error()+": ")
}

// serviceRouteURLs are the addresses a public-route check asks on behalf of
// the service domains, which the release's own checks say nothing about.
func serviceRouteURLs(domains []PlannedDomain) []string {
	var urls []string
	for _, domain := range domains {
		if domain.Redirects() || !domain.ServiceDomain() || strings.HasPrefix(domain.Hostname, "*.") {
			continue
		}
		scheme := "http"
		if domain.HTTPS {
			scheme = "https"
		}
		urls = append(urls, scheme+"://"+domain.Hostname+"/")
	}
	return urls
}

// serviceRouteCheck is one of the plan's public-route checks asked of a
// service domain instead of the deployment's own names: the same attempts
// and timing, the service domain's address, and any answer below 500 taken
// as reachable, a redirect to a sign-in page included. A check that names an
// address of its own is the operator's probe of that address and is not
// asked again. The check is the domain's own, so a service that does not
// answer is not blamed on the release's primary container.
func serviceRouteCheck(check PlannedCheck, address string) (PlannedCheck, CheckTarget, bool) {
	config, err := decodeCheckConfiguration(check.Config)
	if err != nil || config.URL != "" || config.Disabled {
		return PlannedCheck{}, CheckTarget{}, false
	}
	config.URL, config.Path, config.Host, config.Port = address, "", "", 0
	config.ExpectedStatus, config.AcceptAnyAnswer = nil, true
	raw, err := json.Marshal(config)
	if err != nil {
		return PlannedCheck{}, CheckTarget{}, false
	}
	host := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(address, "https://"), "http://"), "/")
	derived := check
	derived.Name, derived.Config = check.Name+" · "+host, raw
	return derived, CheckTarget{PublicURLs: []string{address}, ServiceRoute: true}, true
}

// serviceRouteChecks runs every public-route check against every service
// domain of the release.
func serviceRouteChecks(ctx context.Context, runner *CheckRunner, checks []PlannedCheck, domains []PlannedDomain) []CheckEvidence {
	var evidence []CheckEvidence
	for _, address := range serviceRouteURLs(domains) {
		for _, check := range checks {
			if derived, target, ok := serviceRouteCheck(check, address); ok {
				evidence = append(evidence, runner.Run(ctx, derived, target))
			}
		}
	}
	return evidence
}

// serviceDomains holds a draft's service domains to the Compose analysis its
// detection made, and writes its primary service as none. A draft not yet
// detected has nothing to hold them to, and its preflight and commit do.
func (d *Draft) serviceDomains(configuration PlanConfiguration) ([]PlannedDomain, error) {
	if d.Data.Detection == nil {
		return configuration.Domains, nil
	}
	return validateServiceDomains(configuration, d.Data.Detection.Compose)
}
