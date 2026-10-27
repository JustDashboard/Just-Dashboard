package blueprint

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

const (
	minComposeServices = 2
	maxComposeServices = 8
)

var (
	// serviceReferenceRE is the whole reference vocabulary of a service's
	// environment. Anything else between braces is refused rather than
	// passed to Compose as text it might one day interpret.
	serviceReferenceRE = regexp.MustCompile(`\{\{(secret|variable)\.([A-Za-z0-9_-]+)\}\}`)
	// urlUserinfoRE finds the userinfo of a URL written into a value, the one
	// place a literal password hides in a field whose name does not say so.
	urlUserinfoRE = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://([^/@\s"']+)@`)
)

// composeSecretMarkers are the name fragments the Compose planner refuses a
// literal for (deploy.secretShapedKey) together with the ones a single-image
// default is refused for. A template is held to both, so a value it renders is
// never one the planner would refuse after the operator pressed Deploy.
var composeSecretMarkers = []string{
	"PASSWORD", "PASSWD", "SECRET", "TOKEN", "APIKEY", "API_KEY", "PRIVATE_KEY", "ACCESS_KEY",
}

func composeSecretShaped(name string) bool {
	upper := strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
	for _, marker := range composeSecretMarkers {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}

// validateComposeTemplate holds a multi-service template to the rules a
// single image is held to, applied per service, and to the ones only a stack
// needs: one public service, a private network for the rest, start order
// without cycles, every shared secret generated here and referenced by type.
func validateComposeTemplate(blueprint *Blueprint) error {
	if blueprint.Profile != ProfileCompose {
		return fmt.Errorf("a blueprint with services is a Compose template and must use profile %q", ProfileCompose)
	}
	if len(blueprint.Services) < minComposeServices || len(blueprint.Services) > maxComposeServices {
		return fmt.Errorf("a Compose template runs %d-%d services; this one declares %d",
			minComposeServices, maxComposeServices, len(blueprint.Services))
	}
	// The single-image fields have a per-service home here. Accepting both
	// would leave a reader to guess which one the stack runs.
	image := blueprint.Image
	if image.Reference != "" || image.TagPolicy != "" || image.PullPolicy != "" || len(image.Platforms) > 0 ||
		len(image.Command) > 0 || len(blueprint.Ports) > 0 || len(blueprint.Volumes) > 0 ||
		blueprint.Resources != (Resources{}) {
		return errors.New("a Compose template declares its image, ports, volumes and resources on each service, not at the top level")
	}
	if len(blueprint.Files) > 0 {
		return errors.New("a Compose template cannot install configuration files")
	}
	// Nothing in a stack is privileged. A service that needs the host is a
	// single reviewed image with its reason written down, not one container
	// among several.
	if blueprint.Security.Privileged || blueprint.Security.HostNetwork ||
		len(blueprint.Security.Capabilities) > 0 || len(blueprint.Security.Devices) > 0 {
		return errors.New("a Compose template cannot ask for privileges")
	}
	operations := blueprint.Operations
	if len(operations.Install) > 0 || len(operations.Release) > 0 || len(operations.Stop) > 0 {
		return errors.New("a Compose template sets variables at startup and nothing else; Compose owns its install, release and stop")
	}
	for _, operation := range operations.Startup {
		if operation.Kind != OperationSetVariable {
			return fmt.Errorf("a Compose template cannot run a %q operation", operation.Kind)
		}
	}
	for _, input := range blueprint.Inputs {
		switch {
		case input.Kind == InputMemory:
			return fmt.Errorf("input %q is a memory limit; a Compose template's limits are per service", input.Name)
		case input.Kind == InputChoice:
			for _, choice := range input.Choices {
				if choice.Image != "" {
					return fmt.Errorf("input %q swaps an image; a Compose template names each service's image", input.Name)
				}
			}
		}
	}

	services := map[string]*Service{}
	for index := range blueprint.Services {
		service := &blueprint.Services[index]
		if !identifierRE.MatchString(service.Name) {
			return fmt.Errorf("service name %q is not a lowercase identifier", service.Name)
		}
		if services[service.Name] != nil {
			return fmt.Errorf("service %q is declared twice", service.Name)
		}
		services[service.Name] = service
	}
	variables := composePlanVariables(blueprint)
	secrets := map[string]bool{}
	for _, secret := range blueprint.Secrets {
		secrets[secret.Name] = false
	}
	volumes := map[string]Volume{}
	public := 0
	for index := range blueprint.Services {
		service := &blueprint.Services[index]
		fail := func(format string, args ...any) error {
			return fmt.Errorf("service %q: %s", service.Name, fmt.Sprintf(format, args...))
		}
		switch service.Role {
		case RoleApp, RoleWorker, RoleDatabase, RoleCache:
		default:
			return fail("role %q is not one of app, worker, database, cache", service.Role)
		}
		if strings.TrimSpace(service.Purpose) == "" {
			return fail("does not say what it is for")
		}
		if err := validateImage(service.Image); err != nil {
			return fail("%v", err)
		}
		for _, argument := range service.Image.Command {
			if strings.Contains(argument, "{{") {
				return fail("templates its command; a command is argv shown in every process list, so values reach it as environment")
			}
		}
		if err := validateResources(service.Resources); err != nil {
			return fail("%v", err)
		}
		if service.Resources.CPUs > 64 {
			return fail("asks for %.2f CPUs", service.Resources.CPUs)
		}
		serviceIsPublic, err := validateServicePorts(service)
		if err != nil {
			return fail("%v", err)
		}
		if serviceIsPublic {
			public++
			if service.Role != RoleApp {
				return fail("owns the domain but is a %s; only an application is routed to the public", service.Role)
			}
			if service.Egress {
				return fail("is public and so already reaches the internet; egress is for private services")
			}
		}
		if err := validateServiceVolumes(service, volumes); err != nil {
			return fail("%v", err)
		}
		if err := validateHealthcheck(service.Healthcheck); err != nil {
			return fail("%v", err)
		}
		if err := validateServiceEnvironment(service, variables, secrets); err != nil {
			return fail("%v", err)
		}
		seen := map[string]bool{}
		for _, dependency := range service.DependsOn {
			if dependency.Service == service.Name || services[dependency.Service] == nil {
				return fail("depends on %q, which is not another service of this template", dependency.Service)
			}
			if seen[dependency.Service] {
				return fail("depends on %q twice", dependency.Service)
			}
			seen[dependency.Service] = true
			switch dependency.Condition {
			case StartHealthy, StartStarted, StartCompleted:
			default:
				return fail("waits for %q to reach %q; the conditions are healthy, started and completed", dependency.Service, dependency.Condition)
			}
		}
	}
	if public != 1 {
		return fmt.Errorf("a Compose template routes exactly one service to the domain, through its one proxy port; this one has %d", public)
	}
	if cycle := dependencyCycle(blueprint.Services); cycle != "" {
		return fmt.Errorf("services wait for each other in a cycle: %s", cycle)
	}
	// A secret no service is given is a password generated for nothing, and
	// usually the sign of a reference spelled wrong.
	for _, secret := range blueprint.Secrets {
		if !secrets[secret.Name] {
			return fmt.Errorf("secret %q is generated but no service references it", secret.Name)
		}
	}
	return nil
}

// composePlanVariables are the plan variables a service may reference with
// {{variable.NAME}}: what an input writes, and what a startup operation sets.
// A generated secret is referenced by its own type instead, so a reader can
// tell from the template which values are secret.
func composePlanVariables(blueprint *Blueprint) map[string]bool {
	variables := map[string]bool{}
	for _, input := range blueprint.Inputs {
		if input.Variable != "" {
			variables[input.Variable] = true
		}
	}
	for _, operation := range blueprint.Operations.Startup {
		if operation.Kind == OperationSetVariable {
			variables[operation.Name] = true
		}
	}
	return variables
}

func validateServicePorts(service *Service) (bool, error) {
	names := map[string]bool{}
	occupied := map[int]bool{}
	public := false
	for _, port := range service.Ports {
		if !identifierRE.MatchString(port.Name) {
			return false, fmt.Errorf("port name %q is not a lowercase identifier", port.Name)
		}
		if names[port.Name] {
			return false, fmt.Errorf("port %q is declared twice", port.Name)
		}
		names[port.Name] = true
		if port.Internal < 1 || port.Internal > 65535 {
			return false, fmt.Errorf("port %q uses out-of-range internal port %d", port.Name, port.Internal)
		}
		if occupied[port.Internal] {
			return false, fmt.Errorf("two ports both claim %d", port.Internal)
		}
		occupied[port.Internal] = true
		// The stack's only published port is the proxied one on loopback. UDP
		// and a "direct" publication on every interface are what a single
		// reviewed image is for.
		if strings.ToLower(port.Protocol) != "tcp" {
			return false, fmt.Errorf("port %q uses protocol %q; a Compose template carries TCP only", port.Name, port.Protocol)
		}
		switch port.Exposure {
		case "proxy":
			if public {
				return false, errors.New("declares two proxy ports")
			}
			public = true
		case "internal":
		default:
			return false, fmt.Errorf("port %q uses exposure %q; a Compose template's ports are proxy or internal", port.Name, port.Exposure)
		}
		if port.Primary && port.Exposure != "proxy" {
			return false, fmt.Errorf("port %q is marked primary but is not the proxied port", port.Name)
		}
		if strings.TrimSpace(port.Purpose) == "" {
			return false, fmt.Errorf("port %q does not say what it is for", port.Name)
		}
	}
	return public, nil
}

// validateServiceVolumes applies the single-image volume rules to one service
// and lets two services share a volume by name, which is how a server and its
// worker see the same uploads. A shared volume is one volume, so it has to be
// described the same way by both.
func validateServiceVolumes(service *Service, declared map[string]Volume) error {
	targets := map[string]bool{}
	names := map[string]bool{}
	data := false
	for _, volume := range service.Volumes {
		if !identifierRE.MatchString(volume.Name) {
			return fmt.Errorf("volume name %q is not a lowercase identifier", volume.Name)
		}
		if names[volume.Name] {
			return fmt.Errorf("volume %q is mounted twice", volume.Name)
		}
		names[volume.Name] = true
		if !absolutePathRE.MatchString(volume.Target) || strings.Contains(volume.Target, "..") ||
			strings.ContainsAny(volume.Target, ":\r\n") {
			return fmt.Errorf("volume %q mounts at %q, which is not a contained absolute path", volume.Name, volume.Target)
		}
		if volume.Target == "/var/run/docker.sock" || strings.HasPrefix(volume.Target, "/var/run/docker") {
			return fmt.Errorf("volume %q mounts the Docker socket; no service of a stack is given the host", volume.Name)
		}
		if targets[volume.Target] {
			return fmt.Errorf("two volumes both mount at %q", volume.Target)
		}
		targets[volume.Target] = true
		if strings.TrimSpace(volume.Purpose) == "" {
			return fmt.Errorf("volume %q does not say what it holds", volume.Name)
		}
		if volume.Data {
			data = true
			if volume.ReadOnly {
				return fmt.Errorf("volume %q holds data and is mounted read-only", volume.Name)
			}
		}
		if previous, shared := declared[volume.Name]; shared {
			if previous.Data != volume.Data || previous.Backup != volume.Backup {
				return fmt.Errorf("volume %q is shared with another service, which describes its data and backup differently", volume.Name)
			}
		}
		declared[volume.Name] = volume
	}
	// The single-image rule, per service: a database with nowhere to keep its
	// tables loses them on the first redeploy.
	if service.Role == RoleDatabase && !data {
		return errors.New("is a database and declares no persistent data volume")
	}
	return nil
}

func validateHealthcheck(check Healthcheck) error {
	if len(check.Test) == 0 || len(check.Test) > 32 {
		return errors.New("needs a healthcheck: Compose starts what depends on it only once it is healthy, and the planner warns about a service without one")
	}
	for index, argument := range check.Test {
		if argument == "" || len(argument) > 512 || strings.ContainsAny(argument, "\x00\r\n") {
			return fmt.Errorf("healthcheck argument %d is empty, too long or contains control characters", index)
		}
		if strings.Contains(argument, "{{") {
			return errors.New("templates its healthcheck; a healthcheck is shown in plain text by docker inspect")
		}
	}
	switch first := strings.ToUpper(check.Test[0]); first {
	case "CMD", "CMD-SHELL", "NONE":
		return fmt.Errorf("healthcheck starts with %q; write the command itself, which always runs without a shell", check.Test[0])
	}
	if check.IntervalSeconds < 1 || check.IntervalSeconds > 300 ||
		check.TimeoutSeconds < 1 || check.TimeoutSeconds > 120 ||
		check.Retries < 1 || check.Retries > 100 ||
		check.StartPeriodSeconds < 0 || check.StartPeriodSeconds > 900 {
		return errors.New("healthcheck timing is outside 1-300s interval, 1-120s timeout, 1-100 retries and 0-900s start period")
	}
	return nil
}

func validateServiceEnvironment(service *Service, variables map[string]bool, secrets map[string]bool) error {
	names := map[string]bool{}
	for _, entry := range service.Environment {
		if !variableRE.MatchString(entry.Name) {
			return fmt.Errorf("environment variable %q is not a valid name", entry.Name)
		}
		if names[entry.Name] {
			return fmt.Errorf("environment variable %q is set twice", entry.Name)
		}
		names[entry.Name] = true
		if len(entry.Value) > 4096 || strings.ContainsAny(entry.Value, "\x00\r\n") {
			return fmt.Errorf("environment variable %q is longer than 4096 characters or contains a control character", entry.Name)
		}
		references := serviceReferenceRE.FindAllStringSubmatch(entry.Value, -1)
		for _, reference := range references {
			switch reference[1] {
			case "secret":
				if _, declared := secrets[reference[2]]; !declared {
					return fmt.Errorf("environment variable %q references secret %q, which this template does not generate", entry.Name, reference[2])
				}
				secrets[reference[2]] = true
			case "variable":
				if !variables[reference[2]] {
					return fmt.Errorf("environment variable %q references variable %q, which no input or startup operation sets", entry.Name, reference[2])
				}
			}
		}
		literal := serviceReferenceRE.ReplaceAllString(entry.Value, "")
		if strings.Contains(literal, "{{") || strings.Contains(literal, "}}") {
			return fmt.Errorf("environment variable %q uses a placeholder other than {{secret.<name>}} or {{variable.<NAME>}}", entry.Name)
		}
		// The rule a pasted Compose file is refused by, applied before anyone
		// presses Deploy: a credential-shaped name is given a reference, never
		// a literal, so the template cannot ship a working password.
		if composeSecretShaped(entry.Name) && (len(references) != 1 || references[0][0] != entry.Value) {
			return fmt.Errorf("environment variable %q is credential-shaped, so its whole value must be one {{secret.…}} or {{variable.…}} reference", entry.Name)
		}
		for _, userinfo := range urlUserinfoRE.FindAllStringSubmatch(entry.Value, -1) {
			if _, password, found := strings.Cut(userinfo[1], ":"); found && password != "" &&
				!slices.ContainsFunc(references, func(reference []string) bool { return reference[0] == password }) {
				return fmt.Errorf("environment variable %q writes a literal password into a URL", entry.Name)
			}
		}
	}
	return nil
}

// dependencyCycle names a cycle in the services' start order, or returns
// empty. Compose refuses a cycle too, but only once somebody has deployed it.
func dependencyCycle(services []Service) string {
	edges := map[string][]string{}
	for _, service := range services {
		for _, dependency := range service.DependsOn {
			edges[service.Name] = append(edges[service.Name], dependency.Service)
		}
	}
	const (
		unvisited = iota
		visiting
		done
	)
	state := map[string]int{}
	var path []string
	var visit func(string) string
	visit = func(name string) string {
		switch state[name] {
		case visiting:
			start := slices.Index(path, name)
			return strings.Join(append(append([]string{}, path[start:]...), name), " → ")
		case done:
			return ""
		}
		state[name] = visiting
		path = append(path, name)
		for _, next := range edges[name] {
			if cycle := visit(next); cycle != "" {
				return cycle
			}
		}
		path = path[:len(path)-1]
		state[name] = done
		return ""
	}
	for _, service := range services {
		if cycle := visit(service.Name); cycle != "" {
			return cycle
		}
	}
	return ""
}
