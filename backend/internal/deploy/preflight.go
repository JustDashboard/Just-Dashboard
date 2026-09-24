package deploy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

type FacilityObservation struct {
	Available bool   `json:"available"`
	Detail    string `json:"detail,omitempty"`
}

type PathObservation struct {
	Path      string `json:"path"`
	Contained bool   `json:"contained"`
	Exists    bool   `json:"exists"`
	Writable  bool   `json:"writable"`
	Detail    string `json:"detail,omitempty"`
}

type PortObservation struct {
	Address           string `json:"address"`
	Port              int    `json:"port"`
	Protocol          string `json:"protocol"`
	InUse             bool   `json:"inUse"`
	OwnedByDeployment bool   `json:"ownedByDeployment,omitempty"`
	Detail            string `json:"detail,omitempty"`
}

type HostObservation struct {
	Facilities      map[string]FacilityObservation `json:"facilities"`
	Paths           []PathObservation              `json:"paths"`
	Ports           []PortObservation              `json:"ports"`
	OS              string                         `json:"os"`
	Architecture    string                         `json:"architecture"`
	AvailableMemory int64                          `json:"availableMemoryBytes"`
	AvailableDisk   int64                          `json:"availableDiskBytes"`
	CPUCount        int                            `json:"cpuCount,omitempty"`
	Domains         []DomainObservation            `json:"domains"`
	Firewall        FirewallObservation            `json:"firewall"`
	Dependencies    []DependencyObservation        `json:"dependencies"`
}

type DomainObservation struct {
	Hostname              string   `json:"hostname"`
	Addresses             []string `json:"addresses"`
	DNSAvailable          bool     `json:"dnsAvailable"`
	PointsHere            bool     `json:"pointsHere"`
	BehindProxy           bool     `json:"behindProxy"`
	ProxyAvailable        bool     `json:"proxyAvailable"`
	CertificateAvailable  bool     `json:"certificateAvailable"`
	CertificateAutomation bool     `json:"certificateAutomation"`
	CertificateName       string   `json:"certificateName,omitempty"`
	Conflict              bool     `json:"conflict"`
	Detail                string   `json:"detail,omitempty"`
}

type FirewallObservation struct {
	Available bool   `json:"available"`
	Enabled   bool   `json:"enabled"`
	Allows    bool   `json:"allows"`
	Backend   string `json:"backend,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

type DependencyObservation struct {
	Kind         string `json:"kind"`
	ResourceKind string `json:"resourceKind"`
	ResourceID   string `json:"resourceId"`
	Available    bool   `json:"available"`
	// Missing distinguishes "the owner looked and it is not there" from
	// "the owner could not look". A managed resource the deployment creates
	// itself is allowed to be missing before its first start.
	Missing  bool   `json:"missing,omitempty"`
	Fresh    bool   `json:"fresh,omitempty"`
	Status   string `json:"status,omitempty"`
	Detail   string `json:"detail,omitempty"`
	DeepLink string `json:"deepLink,omitempty"`
}

type ObservationRequest struct {
	NeedsGit     bool
	NeedsDocker  bool
	NeedsBuildx  bool
	NeedsCompose bool
	// ExistingProxySite is the deployment-owned route being replaced. It is
	// excluded from conflict detection while every other matching site remains
	// a hard ownership conflict.
	ExistingProxySite   string
	ExistingRuntimeID   string
	ExistingRuntimeKind string
	Paths               []string
	Ports               []PortObservation
	Domains             []PlannedDomain
	Dependencies        []PlannedDependency
	NeedsFirewall       bool
}

// PreflightObserver is intentionally read-only. A test double can prove
// preflight made exactly one observation and no mutable adapter call exists on
// the interface; the host implementation uses stat/proc, Docker/Compose
// availability, proxy inventory, DNS, and filesystem-capacity reads only.
type PreflightObserver interface {
	Observe(context.Context, ObservationRequest) (HostObservation, error)
}

type HostPreflightObserver struct {
	paths      *files.Service
	docker     PlanningDocker
	proxy      PlanningProxy
	firewall   PlanningFirewall
	resources  PlanningDependencies
	volumeRoot string
}

type PlanningFirewall interface {
	Status(context.Context) (*netsec.FirewallStatus, error)
}

type PlanningDependencies interface {
	ObserveDependencies(context.Context, []PlannedDependency) ([]DependencyObservation, error)
}

type PlanningProxy interface {
	Availability(context.Context) proxysvc.Availability
	ListVHosts(context.Context) ([]proxysvc.VHost, error)
}

func (o *HostPreflightObserver) WithFirewall(firewall PlanningFirewall) *HostPreflightObserver {
	o.firewall = firewall
	return o
}

func (o *HostPreflightObserver) WithDependencies(resources PlanningDependencies) *HostPreflightObserver {
	o.resources = resources
	return o
}

func NewHostPreflightObserver(
	deployRoots []string,
	volumeRoot string,
	docker PlanningDocker,
	proxies ...PlanningProxy,
) *HostPreflightObserver {
	observer := &HostPreflightObserver{paths: files.New(deployRoots), docker: docker, volumeRoot: volumeRoot}
	if len(proxies) > 0 {
		observer.proxy = proxies[0]
	}
	return observer
}

func (o *HostPreflightObserver) Observe(ctx context.Context, request ObservationRequest) (HostObservation, error) {
	observation := HostObservation{
		Facilities: map[string]FacilityObservation{}, Paths: []PathObservation{}, Ports: []PortObservation{},
		Domains: []DomainObservation{}, Dependencies: []DependencyObservation{}, OS: runtime.GOOS, Architecture: runtime.GOARCH,
	}
	if request.NeedsGit {
		observation.Facilities["git"] = FacilityObservation{Available: hostexec.Available("git")}
	}
	if request.NeedsDocker || request.NeedsCompose {
		facility := FacilityObservation{}
		if o.docker != nil {
			dockerCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			availability := o.docker.Ping(dockerCtx)
			cancel()
			facility.Available, facility.Detail = availability.Available, availability.Error
		}
		observation.Facilities["docker"] = facility
	}
	if request.NeedsBuildx {
		available := false
		if buildx, ok := o.docker.(interface{ BuildxAvailable(context.Context) bool }); ok {
			buildxCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			available = buildx.BuildxAvailable(buildxCtx)
			cancel()
		}
		observation.Facilities["buildx"] = FacilityObservation{Available: available}
	}
	if request.NeedsCompose {
		composeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		available := o.docker != nil && o.docker.ComposeAvailable(composeCtx)
		cancel()
		observation.Facilities["compose"] = FacilityObservation{Available: available}
	}
	if len(request.Domains) > 0 {
		proxyAvailable := false
		certificateAutomation := false
		claimedDomains := map[string]bool{}
		certificates := []proxysvc.Certificate{}
		if o.proxy != nil {
			proxyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			availability := o.proxy.Availability(proxyCtx)
			proxyAvailable = availability.Nginx || availability.Caddy
			certificateAutomation = availability.Certbot || availability.IngressContainer != ""
			vhosts, err := o.proxy.ListVHosts(proxyCtx)
			cancel()
			if err == nil {
				for _, vhost := range vhosts {
					if request.ExistingProxySite != "" && vhost.Name == request.ExistingProxySite {
						continue
					}
					for _, hostname := range vhost.ServerNames {
						claimedDomains[strings.ToLower(hostname)] = true
					}
				}
			}
			if lister, ok := o.proxy.(interface {
				ListCertificates(context.Context) ([]proxysvc.Certificate, error)
			}); ok {
				certCtx, certCancel := context.WithTimeout(ctx, 10*time.Second)
				if listed, listErr := lister.ListCertificates(certCtx); listErr == nil {
					certificates = listed
				}
				certCancel()
			}
		}
		domains := make([]DomainObservation, len(request.Domains))
		semaphore := make(chan struct{}, 8)
		var wait sync.WaitGroup
		for index, planned := range request.Domains {
			index, planned := index, planned
			wait.Add(1)
			go func() {
				defer wait.Done()
				select {
				case semaphore <- struct{}{}:
					defer func() { <-semaphore }()
				case <-ctx.Done():
					domains[index] = DomainObservation{Hostname: planned.Hostname, Detail: "DNS lookup was cancelled"}
					return
				}
				domainCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				check := proxysvc.CheckDomainDNS(domainCtx, planned.Hostname)
				cancel()
				domain := DomainObservation{
					Hostname: planned.Hostname, Addresses: append([]string(nil), check.Addresses...),
					DNSAvailable: check.Error == "" && len(check.Addresses) > 0,
					PointsHere:   check.PointsHere, BehindProxy: check.BehindProxy,
					ProxyAvailable: proxyAvailable, CertificateAutomation: certificateAutomation,
					Conflict: claimedDomains[strings.ToLower(planned.Hostname)],
				}
				for _, certificate := range certificates {
					if certificate.Error == "" && !certificate.Expired && certificateCoversDomain(certificate.Domains, planned.Hostname) {
						domain.CertificateAvailable, domain.CertificateName = true, certificate.Name
						break
					}
				}
				if check.Error != "" {
					domain.Detail = "DNS lookup did not return usable evidence"
				} else {
					domain.Detail = check.Summary
				}
				domains[index] = domain
			}()
		}
		wait.Wait()
		observation.Domains = append(observation.Domains, domains...)
	}
	for _, requested := range uniqueSorted(request.Paths) {
		pathObservation := PathObservation{Path: requested}
		resolved, err := o.paths.Resolve(requested)
		if err != nil {
			pathObservation.Detail = err.Error()
			observation.Paths = append(observation.Paths, pathObservation)
			continue
		}
		pathObservation.Path, pathObservation.Contained = resolved, true
		if info, err := os.Stat(resolved); err == nil {
			pathObservation.Exists = true
			mode := info.Mode().Perm()
			pathObservation.Writable = mode&0o200 != 0
		} else if os.IsNotExist(err) {
			parent := filepath.Dir(resolved)
			if info, parentErr := os.Stat(parent); parentErr == nil {
				pathObservation.Writable = info.Mode().Perm()&0o200 != 0
			}
		} else {
			pathObservation.Detail = err.Error()
		}
		observation.Paths = append(observation.Paths, pathObservation)
	}
	listening := listeningTCPPorts()
	existingPorts := o.existingDeploymentPorts(ctx, request)
	for _, requested := range request.Ports {
		requested.Protocol = strings.ToLower(requested.Protocol)
		if requested.Protocol == "" {
			requested.Protocol = "tcp"
		}
		if requested.Protocol == "tcp" {
			requested.InUse = listening[requested.Port]
			if requested.InUse && existingPorts[portObservationKey(requested.Port, requested.Protocol)] {
				requested.InUse, requested.OwnedByDeployment = false, true
				requested.Detail = "the current live deployment runtime owns this host port"
			} else if requested.InUse {
				requested.Detail = "a host listener already uses this TCP port"
			}
		}
		observation.Ports = append(observation.Ports, requested)
	}
	if request.NeedsFirewall {
		if o.firewall == nil {
			observation.Firewall.Detail = "firewall inventory is unavailable"
		} else {
			firewallCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			status, err := o.firewall.Status(firewallCtx)
			cancel()
			if err != nil || status == nil || !status.Available {
				observation.Firewall.Detail = "firewall status could not be read"
				if status != nil && status.Error != "" {
					observation.Firewall.Detail = status.Error
				}
			} else {
				observation.Firewall.Available, observation.Firewall.Enabled = true, status.Enabled
				observation.Firewall.Backend = string(status.Backend)
				observation.Firewall.Allows = firewallAllowsPorts(status.Rules, request.Ports)
			}
		}
	}
	if len(request.Dependencies) > 0 && o.resources != nil {
		dependencyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		observed, err := o.resources.ObserveDependencies(dependencyCtx, request.Dependencies)
		cancel()
		if err == nil {
			observation.Dependencies = observed
		}
	}
	observation.AvailableMemory = availableMemory()
	observation.CPUCount = runtime.NumCPU()
	diskRoot := o.volumeRoot
	if diskRoot == "" {
		diskRoot = "/"
	}
	var stat syscall.Statfs_t
	if syscall.Statfs(diskRoot, &stat) == nil {
		observation.AvailableDisk = int64(stat.Bavail) * int64(stat.Bsize)
	}
	return observation, nil
}

func (o *HostPreflightObserver) existingDeploymentPorts(
	ctx context.Context,
	request ObservationRequest,
) map[string]bool {
	ports := map[string]bool{}
	if o.docker == nil || request.ExistingRuntimeID == "" {
		return ports
	}
	dockerCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	switch request.ExistingRuntimeKind {
	case "container":
		spec, err := o.docker.SpecOf(dockerCtx, request.ExistingRuntimeID)
		if err != nil || spec == nil {
			return ports
		}
		for _, binding := range spec.Ports {
			protocol := strings.ToLower(binding.Protocol)
			if protocol == "" {
				protocol = "tcp"
			}
			ports[portObservationKey(binding.HostPort, protocol)] = true
		}
	case "compose":
		stacks, err := o.docker.ListStacks(dockerCtx, nil)
		if err != nil {
			return ports
		}
		for _, stack := range stacks {
			if stack.Name != request.ExistingRuntimeID {
				continue
			}
			for _, service := range stack.Services {
				for _, binding := range service.Ports {
					if binding.PublicPort > 0 {
						ports[portObservationKey(int(binding.PublicPort), binding.Type)] = true
					}
				}
			}
		}
	}
	return ports
}

func portObservationKey(port int, protocol string) string {
	return strconv.Itoa(port) + "/" + strings.ToLower(protocol)
}

func PreflightDraft(
	ctx context.Context,
	draft *Draft,
	observer PreflightObserver,
	advancedAllowed bool,
) (*PreflightResult, error) {
	if draft == nil || draft.Data.Intent == nil || draft.Data.Source == nil ||
		draft.Data.Detection == nil || draft.Data.Configuration == nil {
		return nil, ErrDraftIncomplete
	}
	if err := draft.Data.Intent.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPlan, err)
	}
	if err := draft.Data.Source.validateForNewDeployment(); err != nil {
		return nil, err
	}
	if err := validateDetectionResult(draft.Data.Source, *draft.Data.Detection); err != nil {
		return nil, err
	}
	configuration := canonicalConfiguration(*draft.Data.Configuration)
	if err := configuration.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPlan, err)
	}
	request := preflightObservationRequest(draft, configuration)
	observation := HostObservation{Facilities: map[string]FacilityObservation{}, Paths: []PathObservation{}, Ports: []PortObservation{}}
	if observer != nil {
		var err error
		observation, err = observer.Observe(ctx, request)
		if err != nil {
			return nil, err
		}
	}
	findings := preflightFindings(draft, configuration, observation, advancedAllowed)
	plan := exactPlan(draft, configuration)
	preview, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return nil, err
	}
	return &PreflightResult{
		Revision: draft.Revision, Findings: findings, Plan: plan,
		ExpectedDowntime: configuration.Runtime.Strategy == StrategyStopFirst,
		Preview:          string(preview), Digest: digestBytes(preview),
	}, nil
}

func preflightObservationRequest(draft *Draft, configuration PlanConfiguration) ObservationRequest {
	source := draft.Data.Source
	request := ObservationRequest{
		NeedsGit:     source.Kind == SourceGit || source.Kind == SourceLocal || source.Mode == SourceModeComposeGit,
		NeedsDocker:  configuration.Build.Method != BuildNone,
		NeedsBuildx:  buildMethodNeedsBuildx(configuration.Build.Method, draft.Data.Detection.Compose),
		NeedsCompose: configuration.Build.Method == BuildCompose || configuration.Build.Method == BuildLegacyCompose,
		Paths:        []string{}, Ports: []PortObservation{},
		Domains:      append([]PlannedDomain(nil), configuration.Domains...),
		Dependencies: append([]PlannedDependency(nil), configuration.Dependencies...),
	}
	if source.LocalPath != "" {
		request.Paths = append(request.Paths, source.LocalPath)
	}
	for _, mount := range configuration.Runtime.Mounts {
		if mount.Source != "" && filepath.IsAbs(mount.Source) {
			request.Paths = append(request.Paths, mount.Source)
		}
	}
	if configuration.Runtime.HostPort != 0 {
		request.Ports = append(request.Ports, PortObservation{
			Address: configuration.Runtime.BindAddress, Port: configuration.Runtime.HostPort, Protocol: "tcp",
		})
	}
	for _, port := range configuration.Runtime.Ports {
		request.Ports = append(request.Ports, PortObservation{
			Address: port.BindAddress, Port: port.HostPort, Protocol: port.effectiveProtocol(),
		})
	}
	if draft.Data.Detection != nil && draft.Data.Detection.Compose != nil {
		for _, service := range draft.Data.Detection.Compose.Services {
			for _, mount := range service.Mounts {
				if sourcePath, ok := composeBindSource(mount); ok && filepath.IsAbs(sourcePath) {
					request.Paths = append(request.Paths, sourcePath)
				}
			}
			for _, published := range service.Ports {
				if address, port, ok := composePublishedPort(published); ok {
					request.Ports = append(request.Ports, PortObservation{Address: address, Port: port, Protocol: "tcp"})
				}
			}
		}
	}
	request.Paths = uniqueSorted(request.Paths)
	request.Ports = uniquePorts(request.Ports)
	request.NeedsFirewall = publicRuntimeBind(configuration.Runtime) && len(request.Ports) > 0
	return request
}

func preflightFindings(
	draft *Draft,
	configuration PlanConfiguration,
	observation HostObservation,
	advancedAllowed bool,
) []PreflightFinding {
	findings := []PreflightFinding{}
	detection := draft.Data.Detection
	if detection.Unavailable != "" {
		findings = append(findings, finding("source_unavailable", PreflightUnavailable,
			"Part of source inspection is unavailable", detection.Unavailable,
			"The saved evidence remains visible, but unavailable evidence is not presented as a pass.",
			"Check the source facility and run detection again.", "deploy", "source"))
	}
	immutable := detection.Source.Revision
	if immutable == "" {
		immutable = detection.Source.Digest
	}
	if immutable == "" && detection.Source.Kind != SourceBlueprint && detection.Source.Kind != SourceImport {
		findings = append(findings, finding("source_identity_missing", PreflightBlocked,
			"Source has no immutable identity", "", "A release cannot be reproduced without a commit or digest.",
			"Resolve the source again.", "git", "source"))
	} else {
		findings = append(findings, finding("source_identity", PreflightPass,
			"Source identity resolved", immutable, "The planned source can be named immutably.", "", "deploy", "source"))
	}
	if detection.Source.Kind == SourceImage {
		switch {
		case len(detection.Source.Platforms) == 0 || detection.Source.OS == "" || detection.Source.Architecture == "":
			findings = append(findings, finding("image_platform_unavailable", PreflightUnavailable,
				"Image platform evidence is unavailable", "", "The registry did not declare a target OS and architecture.",
				"Choose a platform explicitly or use an image with platform metadata.", "docker", "source.platform"))
		case draft.Data.Source.Platform != "" && !platformListContains(detection.Source.Platforms, draft.Data.Source.Platform):
			findings = append(findings, finding("unsupported_runtime", PreflightBlocked,
				"Requested image platform is absent from the manifest", draft.Data.Source.Platform,
				"The registry manifest does not offer the explicitly selected platform.",
				"Choose one of the resolved image platforms.", "docker", "source.platform"))
		case observation.OS == "" || observation.Architecture == "":
			findings = append(findings, finding("host_platform_unavailable", PreflightUnavailable,
				"Host platform evidence is unavailable", "", "Image compatibility cannot be compared without the host platform.",
				"Retry preflight on the deployment host.", "docker", "source.platform"))
		case detection.Source.OS != observation.OS || detection.Source.Architecture != observation.Architecture:
			findings = append(findings, finding("unsupported_runtime", PreflightBlocked,
				"Image architecture is incompatible with this host",
				detection.Source.OS+"/"+detection.Source.Architecture+" image; "+observation.OS+"/"+observation.Architecture+" host",
				"Docker cannot run this image natively on the selected host.",
				"Choose a compatible image platform.", "docker", "source.platform"))
		default:
			findings = append(findings, finding("image_platform_compatible", PreflightPass,
				"Image platform matches this host", observation.OS+"/"+observation.Architecture,
				"The resolved image manifest includes the deployment host platform.", "", "docker", "source.platform"))
		}
	}
	selected := selectedDetectionCandidate(detection)
	if selected != nil && selected.Recipe == "go" && configuration.Build.Method == BuildRecipe && configuration.Build.GoVersion != "" {
		if _, err := chooseGoRecipeVersion(configuration.Build.GoVersion, "", []byte("go "+selected.GoMinimumVersion)); err != nil {
			findings = append(findings, finding("go_version_unsupported", PreflightBlocked,
				"Selected Go version cannot build this source", err.Error(),
				"The toolchain must satisfy the module's declared language requirement.",
				"Choose a compatible Go version or use a Dockerfile.", "deploy", "configuration.build.goVersion"))
		}
	}
	if selected != nil && selected.Recipe == "node" && configuration.Build.Method == BuildRecipe {
		switch chosen := configuration.Build.PackageManager; {
		case chosen != "" && len(selected.PackageManagers) > 0 && !slices.Contains(selected.PackageManagers, chosen):
			findings = append(findings, finding("package_manager_lockfile_missing", PreflightBlocked,
				"Selected package manager has no lockfile", chosen+"; lockfiles for "+strings.Join(selected.PackageManagers, ", "),
				"A frozen install needs the selected manager's own lockfile.",
				"Choose a package manager whose lockfile is committed, or commit its lockfile.", "deploy", "configuration.build.packageManager"))
		case chosen == "" && selected.PackageManager == "" && len(selected.PackageManagers) > 1:
			findings = append(findings, finding("package_manager_ambiguous", PreflightBlocked,
				"Competing lockfiles need a package manager", strings.Join(selected.PackageManagers, ", "),
				"Installing from a lockfile the project no longer maintains builds untested dependency versions.",
				"Choose the package manager, declare packageManager in package.json, or delete the stale lockfile.", "deploy", "configuration.build.packageManager"))
		}
	}
	if selected != nil && selected.UnpinnedDependencies && configuration.Build.Method == BuildRecipe {
		findings = append(findings, finding("dependencies_unpinned", PreflightWarning,
			"Dependencies are not pinned to exact versions", "unpinned entries in the dependency manifest",
			"Each build installs the newest versions the manifest allows, so a rebuild of this same commit can run different code.",
			"Commit a lockfile (uv lock, poetry lock, or pip freeze > requirements.txt) when rebuilds must be identical; deploying as is works today.",
			"deploy", "configuration.build"))
	}
	if selected != nil && selected.RecipeIssue != "" && configuration.Build.Method == BuildRecipe {
		findings = append(findings, finding("recipe_unsupported", PreflightBlocked,
			"Source needs a different build plan", selected.RecipeIssue,
			"The automatic recipe cannot satisfy the detected source requirements.",
			"Use a Dockerfile or correct the source adapter/toolchain and run detection again.", "deploy", "configuration.build"))
	}
	if selected != nil && selected.BuildMethod != configuration.Build.Method {
		severity := PreflightPass
		title := "Detected build method was explicitly overridden"
		action := ""
		if detection.Source.Kind == SourceCompose || detection.Source.Kind == SourceImage {
			severity = PreflightBlocked
			title = "Configured build method is incompatible with the source"
			action = "Choose the source's required build method before committing the plan."
		}
		findings = append(findings, finding("build_method_changed", severity,
			title, string(configuration.Build.Method),
			"The selected evidence proposes "+string(selected.BuildMethod)+" for this source.",
			action, "deploy", "configuration.build.method"))
	}
	if len(detection.Candidates) == 0 {
		findings = append(findings, finding("detection_empty", PreflightBlocked,
			"No deployable plan was detected", "", "There is no build/runtime candidate to review.",
			"Choose a build method and configuration.", "deploy", "configuration.build.method"))
	} else if detection.SelectedID == "" {
		findings = append(findings, finding("detection_ambiguous", PreflightDecision,
			"Choose one detected candidate", fmt.Sprintf("%d candidates", len(detection.Candidates)),
			"Multiple equally strong roots or methods were found.", "Select the intended root and method.",
			"deploy", "detection.selectedId"))
	} else {
		findings = append(findings, finding("detection_selected", PreflightPass,
			"Detected plan selected", detection.SelectedID, "The build plan has explicit evidence.", "", "deploy", "detection"))
	}
	if detection.GitRequirements.Submodules {
		severity := PreflightDecision
		action := "Choose whether required submodules should be fetched."
		if draft.Data.Source.IncludeSubmodules {
			severity, action = PreflightPass, ""
		}
		findings = append(findings, finding("git_submodules", severity,
			"Repository declares Git submodules", fmt.Sprintf("included: %t", draft.Data.Source.IncludeSubmodules),
			"Bounded detection does not fetch submodule repositories or their credentials.", action, "git", "source.includeSubmodules"))
	}
	if detection.GitRequirements.LFS {
		severity := PreflightDecision
		action := "Choose whether required Git LFS objects should be fetched."
		if draft.Data.Source.IncludeLFS {
			severity, action = PreflightPass, ""
		}
		findings = append(findings, finding("git_lfs", severity,
			"Repository declares Git LFS objects", fmt.Sprintf("included: %t", draft.Data.Source.IncludeLFS),
			"Bounded detection skips LFS object downloads.", action, "git", "source.includeLfs"))
	}
	if detection.Compose != nil {
		configuredVariables := map[string]bool{}
		for _, variable := range configuration.Variables {
			configuredVariables[variable.Name] = true
		}
		for _, variable := range detection.Compose.Variables {
			if !configuredVariables[variable] {
				findings = append(findings, finding("compose_variable_"+strings.ToLower(variable), PreflightDecision,
					"Compose variable needs a scoped value", variable,
					"Planning validation used an inert placeholder and did not inherit the dashboard environment.",
					"Add the variable with build/runtime scope or revise the Compose source.", "deploy", "variables."+variable))
			}
		}
		for index, unsupported := range detection.Compose.Unsupported {
			findings = append(findings, finding("compose_unsupported_"+strconv.Itoa(index+1), PreflightDecision,
				"Compose feature needs explicit review", unsupported,
				"The structural planner cannot promise lifecycle or ownership semantics for this field.",
				"Revise the file or explicitly choose a supported equivalent.", "docker", "source.compose"))
		}
		for index, warning := range detection.Compose.Warnings {
			findings = append(findings, finding("compose_warning_"+strconv.Itoa(index+1), PreflightWarning,
				"Compose configuration needs review", warning,
				"The effective Compose plan is valid but has an operational limitation.",
				"Review and acknowledge the limitation before commit.", "docker", "source.compose"))
		}
	}
	if detection.Truncated {
		findings = append(findings, finding("detection_truncated", PreflightWarning,
			"Repository scan reached a bound", detection.TruncatedReason,
			"Results are deterministic but may not include a deeper candidate.",
			"Confirm the selected root or narrow the source subdirectory.", "deploy", "source.subdirectory"))
	}
	for _, facility := range []struct {
		name, code, title string
		required          bool
	}{
		{"git", "git_unavailable", "Git is available", preflightObservationRequest(draft, configuration).NeedsGit},
		{"docker", "docker_unavailable", "Docker is available", preflightObservationRequest(draft, configuration).NeedsDocker},
		{"buildx", "buildx_unavailable", "Docker Buildx is available", preflightObservationRequest(draft, configuration).NeedsBuildx},
		{"compose", "compose_unavailable", "Docker Compose is available", preflightObservationRequest(draft, configuration).NeedsCompose},
	} {
		if !facility.required {
			continue
		}
		observed := observation.Facilities[facility.name]
		if observed.Available {
			findings = append(findings, finding(facility.name+"_available", PreflightPass,
				facility.title, observed.Detail, "The selected adapter can run on this host.", "", facility.name, ""))
		} else {
			findings = append(findings, finding(facility.code, PreflightBlocked,
				strings.TrimSuffix(facility.title, " is available")+" is unavailable", observed.Detail,
				"The selected plan cannot execute on this host.", "Install or configure the facility and retry preflight.",
				facility.name, ""))
		}
	}
	for _, path := range observation.Paths {
		switch {
		case !path.Contained:
			findings = append(findings, finding("path_outside_roots", PreflightBlocked,
				"Path is outside deployment roots", path.Path, "The deployment cannot read or write this path.",
				"Choose a contained path or update JD_DEPLOY_ROOTS deliberately.", "files", "runtime.mounts"))
		case !path.Exists && !path.Writable:
			findings = append(findings, finding("path_unavailable", PreflightBlocked,
				"Path cannot be created", path.Path, "Neither the target nor a writable parent is available.",
				"Create or correct the directory.", "files", "runtime.mounts"))
		default:
			findings = append(findings, finding("path_contained", PreflightPass,
				"Path is contained", path.Path, "The path stays within JD_DEPLOY_ROOTS.", "", "files", "runtime.mounts"))
		}
	}
	for _, port := range observation.Ports {
		if port.OwnedByDeployment {
			findings = append(findings, finding("port_reusable", PreflightPass,
				"Host port belongs to the live deployment", strconv.Itoa(port.Port), port.Detail,
				"", "docker", "runtime.hostPort"))
		} else if port.InUse {
			severity, remedy := PreflightBlocked, "Choose another port or import the resource that owns it."
			if !configuration.Runtime.HostNetwork {
				severity, remedy = PreflightWarning, "An available host port will be selected automatically; connection details and proxy routes use the actual binding."
			}
			findings = append(findings, finding("port_conflict", severity,
				"Host port is already in use", strconv.Itoa(port.Port), port.Detail,
				remedy, "docker", "runtime.hostPort"))
		} else {
			findings = append(findings, finding("port_available", PreflightPass,
				"Host port is available", strconv.Itoa(port.Port), "No listener currently claims this port.", "", "docker", "runtime.hostPort"))
		}
	}
	domainEvidence := make(map[string]DomainObservation, len(observation.Domains))
	for _, observed := range observation.Domains {
		domainEvidence[strings.ToLower(observed.Hostname)] = observed
	}
	for _, planned := range configuration.Domains {
		observed, ok := domainEvidence[planned.Hostname]
		field := "domains." + planned.Hostname
		if !ok {
			item := finding("dns_unverified", PreflightUnavailable,
				"Domain evidence is unavailable", planned.Hostname,
				"DNS, proxy ownership, and certificate facilities were not observed.",
				"Retry preflight when domain checks are available.", "proxy", field)
			item.DeepLink = "/proxy/sites"
			findings = append(findings, item)
			continue
		}
		if !observed.ProxyAvailable {
			item := finding("proxy_unavailable", PreflightUnavailable,
				"Reverse proxy is unavailable", planned.Hostname,
				"A managed HTTP route cannot be validated on this host.",
				"Install or configure nginx or Caddy, or choose a direct port.", "proxy", field)
			item.DeepLink = "/proxy/sites"
			findings = append(findings, item)
		}
		if observed.Conflict && planned.Ownership == OwnershipManaged {
			item := finding("domain_conflict", PreflightBlocked,
				"Domain is already claimed by another proxy site", planned.Hostname,
				"Creating another managed route would make ownership and routing ambiguous.",
				"Link the existing site or choose another domain.", "proxy", field)
			item.DeepLink = "/proxy/sites"
			findings = append(findings, item)
		} else if planned.Ownership == OwnershipLinked && !observed.Conflict {
			item := finding("domain_link_missing", PreflightDecision,
				"Linked proxy site was not found", planned.Hostname,
				"Linked ownership requires an existing proxy route.",
				"Create/select the route or change ownership to managed.", "proxy", field)
			item.DeepLink = "/proxy/sites"
			findings = append(findings, item)
		} else {
			findings = append(findings, finding("domain_ownership", PreflightPass,
				"Domain ownership is unambiguous", planned.Hostname,
				"The requested managed or linked route does not conflict with the observed proxy inventory.",
				"", "proxy", field))
		}
		switch {
		case !observed.DNSAvailable:
			item := finding("dns_unverified", PreflightUnavailable,
				"DNS could not be verified", planned.Hostname+": "+observed.Detail,
				"Certificate and public-route checks cannot rely on this lookup.",
				"Check the DNS record and retry preflight.", "proxy", field)
			item.DeepLink = "/proxy/sites"
			findings = append(findings, item)
		case observed.PointsHere:
			findings = append(findings, finding("dns_verified", PreflightPass,
				"Domain resolves to this host", strings.Join(observed.Addresses, ", "),
				"The observed address matches this server.", "", "proxy", field))
		case observed.BehindProxy:
			findings = append(findings, finding("dns_behind_proxy", PreflightWarning,
				"Domain resolves through a CDN or proxy", strings.Join(observed.Addresses, ", "),
				"An HTTP certificate challenge may not reach this server directly.",
				"Use DNS validation or temporarily disable the upstream proxy.", "proxy", field))
		default:
			findings = append(findings, finding("dns_unverified", PreflightWarning,
				"Domain does not resolve to this host", strings.Join(observed.Addresses, ", "),
				"The public route and HTTP certificate challenge will not reach this server.",
				"Update DNS or explicitly accept the delayed cutover.", "proxy", field))
		}
		if planned.HTTPS && !observed.CertificateAvailable && observed.CertificateAutomation {
			// Not a blocker any more, because the release itself closes it:
			// `provision_certificate` orders one over HTTP-01 before the
			// candidate starts. What is measured here is the capability — a
			// certbot this host actually has — and reporting it as a refusal
			// would block the ordinary first deployment of a name nobody has
			// published before, which is the case this path exists for.
			item := finding("certificate_automatic", PreflightPass,
				"HTTPS certificate will be issued during the release", planned.Hostname+"; certbot available",
				"No certificate covers this hostname yet; the release orders one before it starts the candidate.",
				"", "certificates", field)
			item.DeepLink = "/proxy/certificates"
			findings = append(findings, item)
		} else if planned.HTTPS && !observed.CertificateAvailable {
			item := finding("certificate_unavailable", PreflightBlocked,
				"HTTPS certificate is unavailable", planned.Hostname,
				"HTTPS activation has no existing certificate for this hostname and this host cannot issue one.",
				"Install certbot, or issue and import the certificate in Certificates, then retry preflight.",
				"certificates", field)
			item.DeepLink = "/proxy/certificates"
			findings = append(findings, item)
		} else if planned.HTTPS {
			item := finding("certificate_available", PreflightPass,
				"HTTPS certificate is available", observed.CertificateName,
				"The certificate inventory contains a valid certificate for this hostname.", "", "certificates", field)
			item.DeepLink = "/proxy/certificates"
			findings = append(findings, item)
		}
	}
	if configuration.Runtime.BindAddress == "0.0.0.0" || configuration.Runtime.BindAddress == "::" {
		findings = append(findings, finding("public_bind", PreflightWarning,
			"Port will be published on every interface", configuration.Runtime.BindAddress,
			"Docker-published ports may bypass ordinary firewall expectations.",
			"Prefer loopback behind the proxy unless public exposure is intentional.", "docker", "runtime.bindAddress"))
	}
	if request := preflightObservationRequest(draft, configuration); request.NeedsFirewall {
		switch {
		case !observation.Firewall.Available:
			item := finding("firewall_unavailable", PreflightUnavailable,
				"Firewall state is unavailable", observation.Firewall.Detail,
				"The public port cannot be compared with the host firewall.",
				"Inspect the firewall before deploying a public bind.", "security", "runtime.hostPort")
			item.DeepLink = "/security/firewall"
			findings = append(findings, item)
		case !observation.Firewall.Enabled:
			item := finding("firewall_mismatch", PreflightWarning,
				"Public port has no active firewall boundary", observation.Firewall.Backend,
				"The selected direct bind may be reachable from every network interface.",
				"Enable an allowlisted firewall policy or explicitly accept public exposure.", "security", "runtime.hostPort")
			item.DeepLink = "/security/firewall"
			findings = append(findings, item)
		case !observation.Firewall.Allows:
			item := finding("firewall_mismatch", PreflightBlocked,
				"Firewall does not admit the selected port", observation.Firewall.Backend,
				"The runtime may start, but clients cannot reach its direct public port.",
				"Add the port through Firewall or choose a route already admitted.", "security", "runtime.hostPort")
			item.DeepLink = "/security/firewall"
			findings = append(findings, item)
		default:
			findings = append(findings, finding("firewall_matches", PreflightPass,
				"Firewall admits the selected public port", observation.Firewall.Backend,
				"An explicit allow rule matches the planned protocol and port.", "", "security", "runtime.hostPort"))
		}
	}
	advanced := configuration.Runtime.Privileged || configuration.Runtime.HostNetwork ||
		len(configuration.Runtime.Capabilities) > 0 || len(configuration.Runtime.Devices) > 0
	for _, mount := range configuration.Runtime.Mounts {
		if mount.Source == "/var/run/docker.sock" {
			advanced = true
		}
	}
	if detection.Compose != nil {
		for _, service := range detection.Compose.Services {
			advanced = advanced || len(service.Advanced) > 0
		}
	}
	if advanced && !advancedAllowed {
		findings = append(findings, finding("advanced_authorization_required", PreflightBlocked,
			"Advanced runtime authority requires system administration", "",
			"The plan requests host-equivalent container access.", "Ask a system administrator to review this plan.",
			"security", "runtime"))
	} else if advanced {
		findings = append(findings, finding("advanced_runtime", PreflightWarning,
			"Runtime has host-equivalent options", "", "The container can affect more of the server than an ordinary workload.",
			"Review privileged, network, capability, device, and mount settings.", "security", "runtime"))
	}
	if configuration.Runtime.Strategy == StrategyBlueGreen {
		eligibleProfile := draft.Data.Intent.Profile == ProfileWeb || draft.Data.Intent.Profile == ProfileStatic
		exclusiveStorage := false
		for _, mount := range configuration.Runtime.Mounts {
			if !mount.ReadOnly {
				exclusiveStorage = true
			}
		}
		if !eligibleProfile || configuration.Runtime.HostPort != 0 || len(configuration.Runtime.Ports) != 0 || configuration.Runtime.HostNetwork || exclusiveStorage {
			findings = append(findings, finding("strategy_ineligible", PreflightBlocked,
				"Blue/green activation is not eligible", "",
				"A fixed or published host port, host network, non-HTTP profile, or writable exclusive mount prevents two candidates.",
				"Use stop-first or remove the exclusive requirement.", "deploy", "runtime.strategy"))
		} else {
			findings = append(findings, finding("strategy_blue_green", PreflightPass,
				"Candidate-first activation is eligible", "blue_green", "The old release can remain live through verification.",
				"", "deploy", "runtime.strategy"))
		}
	}
	resolvedVariables, _, variableErr := ResolveVariableGraph(draft.variableValues(configuration), nil)
	if variableErr != nil {
		findings = append(findings, finding("variable_graph_invalid", PreflightBlocked,
			"Variable references cannot resolve", "", "A variable reference is missing or cyclic.",
			"Correct the variable references before deployment.", "deploy", "variables"))
	}
	for _, variable := range configuration.Variables {
		// A literal value or a generation request satisfies a required
		// variable as well as a typed reference or sealed draft input does.
		if variable.Required && resolvedVariables[variable.Name] == "" {
			findings = append(findings, finding("variable_required_"+strings.ToLower(variable.Name), PreflightDecision,
				"Required variable needs a value", variable.Name, "The runtime would receive an empty required value.",
				"Set or reference the variable.", "deploy", "variables."+variable.Name))
		}
	}
	if len(configuration.Variables) > 0 && variableErr == nil {
		findings = append(findings, finding("variable_graph_valid", PreflightPass,
			"Variable references resolve without cycles", fmt.Sprintf("%d masked variable(s)", len(configuration.Variables)),
			"Only typed reference identities were inspected; secret leaves remain masked.", "", "deploy", "variables"))
	}
	if selected := selectedDetectionCandidate(detection); selected != nil && selected.SchemaTool != "" &&
		configuration.Build.Method == BuildRecipe {
		// A pushed schema is a warning whether or not the database is linked
		// here; it takes the place of the step's pass.
		if push := schemaPushFinding(selected, configuration.Build); push != nil {
			findings = append(findings, *push)
		} else if hasDatabaseDependency(configuration.Dependencies) {
			findings = append(findings, schemaStepFinding(selected, configuration.Build))
		}
	}
	findings = append(findings, stateFindings(selectedDetectionCandidate(detection), configuration, resolvedVariables)...)
	if (draft.Data.Intent.Profile == ProfileWeb || draft.Data.Intent.Profile == ProfileStatic) && !hasReadinessCheck(configuration.Checks) {
		findings = append(findings, finding("readiness_missing", PreflightDecision,
			"Choose a readiness check", "", "Traffic must not move to an unverified candidate.",
			"Add HTTP, TCP, or Docker-health readiness.", "deploy", "checks"))
	}
	persistentStorage := len(configuration.Runtime.Mounts)
	if detection.Compose != nil {
		for _, service := range detection.Compose.Services {
			persistentStorage += len(service.Mounts)
		}
	}
	if persistentStorage > 0 && !hasBackupDependency(configuration.Dependencies) {
		item := finding("backup_policy_missing", PreflightWarning,
			"Persistent storage has no linked backup policy", fmt.Sprintf("%d mount(s)", persistentStorage),
			"A stop-first update can preserve storage but cannot prove it is recoverable.",
			"Link a backup job or explicitly accept this risk.", "backups", "runtime.mounts")
		// The remedy lives in another feature, so the finding carries the way
		// there rather than naming a field this screen does not own.
		item.DeepLink = "/backups"
		findings = append(findings, item)
	}
	dependencyEvidence := make(map[string]DependencyObservation, len(observation.Dependencies))
	for _, observed := range observation.Dependencies {
		dependencyEvidence[observed.Kind+"\x00"+observed.ResourceKind+"\x00"+observed.ResourceID] = observed
	}
	for _, dependency := range configuration.Dependencies {
		field := "dependencies." + dependency.Kind + "." + dependency.ResourceID
		key := dependency.Kind + "\x00" + dependency.ResourceKind + "\x00" + dependency.ResourceID
		observed, ok := dependencyEvidence[key]
		if !ok {
			if dependency.Kind == "backup" || dependency.Kind == "storage" || dependency.Kind == "database" {
				findings = append(findings, finding("dependency_unavailable", PreflightUnavailable,
					"Dependency evidence is unavailable", dependency.ResourceKind+" "+dependency.ResourceID,
					"The owning feature did not return inventory evidence.",
					"Open the owning feature and verify the linked resource.", dependencyOwner(dependency), field))
			}
			continue
		}
		if !observed.Available && observed.Missing && dependency.Ownership == OwnershipManaged && dependency.ResourceKind == "docker_volume" {
			// Docker creates a named volume the first time a container mounts
			// it. Blocking on its absence made every blueprint with data
			// storage undeployable on a fresh host.
			item := finding("storage_pending_creation", PreflightPass,
				"Managed volume will be created on first start", dependency.ResourceID,
				"The deployment owns this volume; Docker creates it when the first release starts.",
				"", dependencyOwner(dependency), field)
			item.DeepLink = observed.DeepLink
			findings = append(findings, item)
			continue
		}
		if !observed.Available {
			severity := PreflightBlocked
			if dependency.Ownership == OwnershipObserved {
				severity = PreflightDecision
			}
			item := finding(dependency.Kind+"_unavailable", severity,
				"Linked "+dependency.Kind+" resource is unavailable", observed.Detail,
				"The deployment cannot safely use the named owning-feature resource.",
				"Repair, relink, or remove this dependency.", dependencyOwner(dependency), field)
			item.DeepLink = observed.DeepLink
			findings = append(findings, item)
			continue
		}
		item := finding("dependency_available", PreflightPass,
			"Dependency is available", dependency.ResourceKind+" "+dependency.ResourceID,
			"Ownership is "+string(dependency.Ownership)+" and remains visible to lifecycle plans.",
			"", dependencyOwner(dependency), field)
		item.DeepLink = observed.DeepLink
		findings = append(findings, item)
		if dependency.Kind == "backup" && observed.Status != "" && !observed.Fresh {
			item := finding("backup_stale", PreflightWarning,
				"Latest backup is outside the freshness policy", observed.Status,
				"The backup resource exists, but its last successful run is not fresh enough.",
				"Run the backup or require a backup immediately before deployment.", "backups", field)
			item.DeepLink = observed.DeepLink
			findings = append(findings, item)
		}
	}
	if limit := configuration.Runtime.MemoryMB; limit > 0 && observation.AvailableMemory > 0 && limit<<20 > observation.AvailableMemory {
		findings = append(findings, finding("runtime_memory_limit_exceeds_host", PreflightWarning,
			"Memory limit exceeds available host memory",
			fmt.Sprintf("%d MiB requested, %d MiB available", limit, observation.AvailableMemory>>20),
			"The container may be allowed more memory than the host can currently give it.",
			"Lower the limit or free host memory.", "metrics", "runtime.memoryMb"))
	}
	if limit := configuration.Runtime.CPUs; limit > 0 && observation.CPUCount > 0 && limit > float64(observation.CPUCount) {
		findings = append(findings, finding("runtime_cpu_limit_exceeds_host", PreflightWarning,
			"CPU limit exceeds host CPUs",
			fmt.Sprintf("%s CPU requested, %d available", strconv.FormatFloat(limit, 'f', -1, 64), observation.CPUCount),
			"Docker caps the container at the host's CPU count; the extra allowance has no effect.",
			"Lower the limit to at most the host CPU count.", "metrics", "runtime.cpus"))
	}
	if observation.AvailableMemory > 0 && observation.AvailableMemory < 256<<20 {
		findings = append(findings, finding("host_memory_low", PreflightWarning,
			"Host memory headroom is low", fmt.Sprintf("%d MiB available", observation.AvailableMemory>>20),
			"A build or second candidate may be killed under pressure.", "Free memory or choose stop-first.", "metrics", ""))
	}
	if observation.AvailableDisk > 0 && observation.AvailableDisk < 1<<30 {
		findings = append(findings, finding("host_disk_low", PreflightWarning,
			"Host disk headroom is low", fmt.Sprintf("%d MiB available", observation.AvailableDisk>>20),
			"Build layers or pulled images may fill the filesystem.", "Free disk before deploying.", "metrics", ""))
	}
	return findings
}

func buildMethodNeedsBuildx(method BuildMethod, compose *ComposeAnalysis) bool {
	switch method {
	case BuildRecipe, BuildDockerfile, BuildStatic:
		return true
	case BuildCompose:
		if compose != nil {
			for _, service := range compose.Services {
				if service.BuildContext != "" {
					return true
				}
			}
		}
	}
	return false
}

func platformListContains(platforms []string, requested string) bool {
	for _, platform := range platforms {
		if platform == requested || strings.HasPrefix(platform, requested+"/") {
			return true
		}
	}
	return false
}

func selectedDetectionCandidate(detection *DetectionResult) *DetectedCandidate {
	if detection == nil || detection.SelectedID == "" {
		return nil
	}
	for index := range detection.Candidates {
		if detection.Candidates[index].ID == detection.SelectedID {
			return &detection.Candidates[index]
		}
	}
	return nil
}

func composeBindSource(mount string) (string, bool) {
	if strings.Contains(mount, "${") {
		return "", false
	}
	parts := strings.Split(mount, ":")
	if len(parts) < 2 {
		return "", false
	}
	source := strings.TrimSpace(parts[0])
	if source == "" {
		return "", false
	}
	if filepath.IsAbs(source) || strings.HasPrefix(source, ".") || strings.Contains(source, "/") {
		return filepath.Clean(source), true
	}
	return "", false
}

func composePublishedPort(value string) (string, int, bool) {
	if strings.Contains(value, "${") {
		return "", 0, false
	}
	value = strings.TrimSpace(strings.SplitN(value, "/", 2)[0])
	parts := strings.Split(value, ":")
	if len(parts) < 2 {
		return "", 0, false
	}
	published := parts[len(parts)-2]
	if strings.Contains(published, "-") {
		return "", 0, false
	}
	port, err := strconv.Atoi(published)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, false
	}
	address := strings.Join(parts[:len(parts)-2], ":")
	address = strings.Trim(address, "[]")
	return address, port, true
}

func uniquePorts(values []PortObservation) []PortObservation {
	seen := map[string]bool{}
	result := make([]PortObservation, 0, len(values))
	for _, value := range values {
		key := value.Address + "\x00" + strconv.Itoa(value.Port) + "\x00" + value.Protocol
		if !seen[key] {
			seen[key] = true
			result = append(result, value)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Address != result[j].Address {
			return result[i].Address < result[j].Address
		}
		if result[i].Port != result[j].Port {
			return result[i].Port < result[j].Port
		}
		return result[i].Protocol < result[j].Protocol
	})
	return result
}

func publicRuntimeBind(runtime RuntimePlanConfig) bool {
	return runtime.HostNetwork || runtime.BindAddress == "0.0.0.0" || runtime.BindAddress == "::"
}

func certificateCoversDomain(names []string, domain string) bool {
	domain = strings.ToLower(strings.TrimSpace(domain))
	for _, name := range names {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == domain {
			return true
		}
		if strings.HasPrefix(name, "*.") {
			suffix := strings.TrimPrefix(name, "*")
			prefix := strings.TrimSuffix(domain, suffix)
			if strings.HasSuffix(domain, suffix) && prefix != "" && !strings.Contains(prefix, ".") {
				return true
			}
		}
	}
	return false
}

func firewallAllowsPorts(rules []netsec.Rule, ports []PortObservation) bool {
	for _, requested := range ports {
		allowed := false
		for _, rule := range rules {
			if strings.EqualFold(rule.Action, "ALLOW") &&
				(rule.Protocol == "" || strings.EqualFold(rule.Protocol, requested.Protocol)) &&
				firewallRuleIncludesPort(rule.Port, requested.Port) {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
	}
	return len(ports) > 0
}

func firewallRuleIncludesPort(spec string, wanted int) bool {
	for _, value := range strings.Split(spec, ",") {
		value = strings.TrimSpace(strings.SplitN(value, "/", 2)[0])
		if lower, upper, ranged := strings.Cut(value, ":"); ranged {
			lo, loErr := strconv.Atoi(lower)
			hi, hiErr := strconv.Atoi(upper)
			if loErr == nil && hiErr == nil && wanted >= lo && wanted <= hi {
				return true
			}
			continue
		}
		if port, err := strconv.Atoi(value); err == nil && port == wanted {
			return true
		}
	}
	return false
}

func finding(code string, severity PreflightSeverity, title, measured, means, action, owner, field string) PreflightFinding {
	return PreflightFinding{
		Code: code, Severity: severity, Title: title, Measured: measured,
		Means: means, Action: action, Owner: owner, FieldID: field,
	}
}

func dependencyOwner(dependency PlannedDependency) string {
	switch dependency.Kind {
	case "backup":
		return "backups"
	case "storage":
		return "docker"
	case "database":
		return "databases"
	case "port":
		return "security"
	default:
		return dependency.Kind
	}
}

func exactPlan(draft *Draft, configuration PlanConfiguration) ExactPlan {
	source := draft.Data.Detection.Source
	// Import adapters retain a bounded observed snapshot in the draft for the
	// adoption review. The executable plan needs only its normalized identity;
	// excluding raw observed JSON is a second defense against an upstream
	// Docker/provider field unexpectedly containing credential material.
	source.Observed = nil
	compose := cloneComposeAnalysis(draft.Data.Detection.Compose)
	actions := make([]PlanAction, 0, len(DefaultStepKeys))
	for ordinal, step := range DefaultStepKeys {
		action := PlanAction{Ordinal: ordinal + 1, Phase: string(step), Owner: stepOwner(step), ChangesState: stepChangesState(step)}
		switch step {
		case StepResolveSource:
			action.Action = "resolve immutable source identity"
			action.Arguments = []string{draft.Data.Detection.Source.Revision, draft.Data.Detection.Source.Digest}
		case StepAcquireSource:
			action.Action = "materialize source in managed release workspace"
			action.WorkingDirectory = configuration.Build.RootDirectory
		case StepAnalyzePlan:
			action.Action = "verify stored plan and detection evidence"
		case StepPrepareContext:
			action.Action = "prepare bounded build context"
		case StepBuildArtifact:
			action.Action = "produce immutable artifact"
			action.Arguments = []string{string(configuration.Build.Method), configuration.Build.BuildCommand}
		case StepRenderRuntime:
			action.Action = "render runtime configuration"
		case StepReleaseTask:
			action.Action = "run stored release task when configured"
		case StepBackupGate:
			action.Action = "verify required backup evidence"
		case StepProvisionCertificate:
			action.Action = "resolve or issue the certificate for the planned HTTPS domains"
			for _, domain := range configuration.Domains {
				if domain.HTTPS {
					action.Arguments = append(action.Arguments, domain.Hostname)
				}
			}
		case StepStartCandidate:
			action.Action = "start candidate release"
			action.Arguments = append([]string(nil), configuration.Runtime.Command...)
		case StepVerifyReadiness:
			action.Action = "run readiness checks"
		case StepVerifySmoke:
			action.Action = "run smoke and public-route checks"
		case StepActivate:
			action.Action = "activate candidate using " + string(configuration.Runtime.Strategy)
		case StepRetirePrevious:
			action.Action = "gracefully retire prior release"
		case StepRecordRelease:
			action.Action = "record immutable release and live revision"
		case StepNotify:
			action.Action = "deliver configured notifications"
		}
		actions = append(actions, action)
	}
	return ExactPlan{
		DraftRevision: draft.Revision, Intent: *draft.Data.Intent, Source: source,
		Build: configuration.Build, Runtime: configuration.Runtime,
		Variables: configuration.Variables, Dependencies: configuration.Dependencies,
		Checks: configuration.Checks, Domains: configuration.Domains, Compose: compose, Actions: actions,
	}
}

func cloneComposeAnalysis(source *ComposeAnalysis) *ComposeAnalysis {
	if source == nil {
		return nil
	}
	copy := *source
	copy.Files = append([]string(nil), source.Files...)
	copy.Variables = append([]string(nil), source.Variables...)
	copy.Warnings = append([]string(nil), source.Warnings...)
	copy.Unsupported = append([]string(nil), source.Unsupported...)
	copy.Services = append([]ComposeServicePlan(nil), source.Services...)
	for index := range copy.Services {
		copy.Services[index].Ports = append([]string(nil), source.Services[index].Ports...)
		copy.Services[index].Mounts = append([]string(nil), source.Services[index].Mounts...)
		copy.Services[index].Advanced = append([]string(nil), source.Services[index].Advanced...)
	}
	return &copy
}

func stepOwner(step StepKey) string {
	switch step {
	case StepResolveSource, StepAcquireSource:
		return "git/source"
	case StepBuildArtifact, StepPrepareContext:
		return "builder"
	case StepStartCandidate, StepRetirePrevious:
		return "docker"
	case StepActivate:
		return "proxy/docker"
	case StepProvisionCertificate:
		return "certificates"
	case StepBackupGate:
		return "backups"
	default:
		return "deploy"
	}
}

func stepChangesState(step StepKey) bool {
	switch step {
	case StepAcquireSource, StepPrepareContext, StepBuildArtifact, StepReleaseTask,
		StepProvisionCertificate, StepStartCandidate, StepActivate, StepRetirePrevious,
		StepRecordRelease, StepNotify:
		return true
	default:
		return false
	}
}

func hasReadinessCheck(checks []PlannedCheck) bool {
	for _, check := range checks {
		if check.Phase == "readiness" && check.Required {
			return true
		}
	}
	return false
}

// schemaStepFinding is the difference between a database that is linked and
// one that is usable: a database created here is empty, and the detected
// tool creates no table until its schema step runs somewhere in the plan.
func schemaStepFinding(candidate *DetectedCandidate, build BuildPlanConfig) PreflightFinding {
	label := candidate.SchemaTool
	if tool := schemaToolByName(candidate.SchemaTool); tool != nil {
		label = tool.Label
	}
	if schemaStepConfigured(candidate, build) {
		return finding("schema_step", PreflightPass,
			"The application's schema is applied before it serves", label,
			"The linked database receives the "+label+" schema from the start command, a release task or the package's start script.",
			"", "deploy", "build.startCommand")
	}
	action := "Choose how the " + label + " schema reaches the linked database before the application starts."
	if tool := schemaToolByName(candidate.SchemaTool); tool != nil && tool.advice != "" {
		action = tool.advice
	}
	if candidate.SchemaCommand != "" {
		action = "Run " + candidate.SchemaCommand + " in the start command before the server starts, or apply the schema another way."
	}
	return finding("schema_step_missing", PreflightWarning,
		"The linked database will not receive the application's schema", label,
		"A database created here is empty, and "+label+" creates no table until its schema step runs; the first request would fail.",
		action, "deploy", "build.startCommand")
}

func hasDatabaseDependency(dependencies []PlannedDependency) bool {
	for _, dependency := range dependencies {
		if dependency.Kind == "database" {
			return true
		}
	}
	return false
}

func hasBackupDependency(dependencies []PlannedDependency) bool {
	for _, dependency := range dependencies {
		if dependency.Kind == "backup" || dependency.ResourceKind == "backup_job" {
			return true
		}
	}
	return false
}

func listeningTCPPorts() map[int]bool {
	result := map[int]bool{}
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		file, err := os.Open(path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 4 || fields[3] != "0A" {
				continue
			}
			_, portHex, ok := strings.Cut(fields[1], ":")
			if !ok {
				continue
			}
			if port, err := strconv.ParseInt(portHex, 16, 32); err == nil {
				result[int(port)] = true
			}
		}
		_ = file.Close()
	}
	return result
}

func availableMemory() int64 {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && fields[0] == "MemAvailable:" {
			value, _ := strconv.ParseInt(fields[1], 10, 64)
			return value * 1024
		}
	}
	return 0
}

func stableFindingCodes(findings []PreflightFinding) []string {
	codes := make([]string, 0, len(findings))
	for _, item := range findings {
		codes = append(codes, item.Code)
	}
	sort.Strings(codes)
	return codes
}
