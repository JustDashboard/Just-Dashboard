package blueprint

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// TemplateMeta is what a template says about itself that no Compose file or
// deployment does: its name, how the first sign-in works, where it is
// documented and who added it. Everything here has a default, because the
// catalogue's schema requires every field and an operator saving a stack
// should not have to write a provenance block to do it.
type TemplateMeta struct {
	ID          string
	Name        string
	Description string
	Category    Category
	// Access defaults to "open": a template cannot know how an application
	// it has never seen signs people in, and open is the answer that warns.
	Access *Access
	// DocsURL defaults to the public image's registry page.
	DocsURL          string
	Maintainer       string
	ReviewedAt       string
	MinimumDashboard string
}

// ComposeTemplate is a pasted Compose file and what the operator declared
// beside it.
type ComposeTemplate struct {
	TemplateMeta
	Document string
	// Inputs are declared: a ${VAR} the file reads is the input whose
	// variable VAR is. A ${VAR} nobody declared becomes an input named after
	// it — a secret input when the name is credential-shaped — and an input
	// declared here that the file never reads is kept (a domain, say).
	Inputs []Input
	// Startup are set_variable operations the caller declares: a ${VAR} the
	// file reads may be one of these, set from the inputs.
	Startup []Operation
	// PublicService is the service routed to the domain, when the file does
	// not make it plain by publishing exactly one service's port.
	PublicService string
	// HealthPath makes the template's readiness check an HTTP request for
	// this path; without one it is a TCP connection to the public port.
	HealthPath string
	// Domain is the hostname a file saved from a deployment was written
	// for. Every value it appears in is set from the domain input instead,
	// so the next deployment is not told it answers at this one's address.
	Domain string
	// PruneUnread drops a declared input or operation whose variable the
	// file never reads: a deployment's variables are every one its
	// environment has, and the template keeps those its services are given.
	PruneUnread bool
}

// ImageTemplate is one image as a single-image template is built from it:
// what a single-service Compose file says, or what a deployment's frozen
// runtime plan does.
type ImageTemplate struct {
	TemplateMeta
	Image   string
	Command []string
	// Port is the container port the template routes, or publishes when
	// Direct; zero for a worker that listens on nothing.
	Port     int
	Protocol string
	Direct   bool
	// HostPort is a direct port's host number, which becomes a number input
	// with it as the default rather than a number written into the template.
	HostPort  int
	Published []PublishedSpec
	Volumes   []Volume
	MemoryMB  int
	CPUs      float64
	Inputs    []Input
	Secrets   []Secret
	Startup   []Operation
	Checks    []Check
	// HealthPath, when Checks is empty, makes the readiness check an HTTP
	// request for this path instead of a TCP connection.
	HealthPath string
}

// PublishedSpec is one more port a single image publishes beside its own.
type PublishedSpec struct {
	Container int
	Host      int
	Protocol  string
}

const maxComposeDocument = 128 << 10

func refuseCompose(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidBlueprint, fmt.Sprintf(format, args...))
}

// composeService is one service of a pasted file, read but not yet a
// template's: its environment is still the file's own text.
type composeService struct {
	name        string
	image       Image
	environment []composeVariable
	published   []int
	exposed     []int
	volumes     []Volume
	healthcheck *Healthcheck
	dependsOn   []Dependency
	memoryMB    int
	cpus        float64
}

type composeVariable struct {
	name  string
	value string
	// passthrough is `- NAME` with no value: Compose takes it from the shell
	// that runs it, which for a template is an input.
	passthrough bool
}

// Keys a service may carry that a template replaces with its own: the
// restart policy, log cap, networks and container names are the dashboard's.
var ignoredServiceKeys = map[string]bool{
	"restart": true, "container_name": true, "hostname": true, "labels": true, "logging": true,
	"networks": true, "init": true, "tty": true, "stdin_open": true, "stop_grace_period": true,
	"platform": true,
}

// FromCompose turns a pasted Compose file into a definition in this
// catalogue's own schema, which Validate and every user-template rule then
// hold exactly as they hold a built-in: a file with two or more services is a
// Compose template, one with a single service is a single image. A literal
// password in the file never reaches the definition — each becomes a secret
// the template generates — and neither does a host port: a template's public
// service answers through the proxy on a port of the template's own.
func FromCompose(spec ComposeTemplate) (*Blueprint, error) {
	if strings.TrimSpace(spec.Document) == "" {
		return nil, refuseCompose("the Compose file is empty")
	}
	if len(spec.Document) > maxComposeDocument {
		return nil, refuseCompose("the Compose file is larger than %d KiB", maxComposeDocument>>10)
	}
	var file map[string]any
	if err := yaml.Unmarshal([]byte(spec.Document), &file); err != nil {
		return nil, refuseCompose("the Compose file is not YAML: %v", err)
	}
	for key := range file {
		switch key {
		case "services", "volumes", "networks", "name", "version":
		default:
			if !strings.HasPrefix(key, "x-") {
				return nil, refuseCompose("the file declares %s at the top level, which a template cannot carry", key)
			}
		}
	}
	rawServices, ok := file["services"].(map[string]any)
	if !ok || len(rawServices) == 0 {
		return nil, refuseCompose("the file declares no services")
	}
	if err := checkComposeVolumes(file["volumes"]); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(rawServices))
	for name := range rawServices {
		names = append(names, name)
	}
	sort.Strings(names)
	services := make([]composeService, 0, len(names))
	for _, name := range names {
		body, ok := rawServices[name].(map[string]any)
		if !ok {
			return nil, refuseCompose("service %q is not a mapping", name)
		}
		service, err := readComposeService(name, body)
		if err != nil {
			return nil, err
		}
		services = append(services, service)
	}
	public, err := publicComposeService(services, spec.PublicService)
	if err != nil {
		return nil, err
	}
	if len(services) == 1 {
		return singleServiceTemplate(spec, services[0])
	}
	return composeStackTemplate(spec, services, public)
}

func checkComposeVolumes(raw any) error {
	if raw == nil {
		return nil
	}
	volumes, ok := raw.(map[string]any)
	if !ok {
		return refuseCompose("the top-level volumes are not a mapping")
	}
	for name, body := range volumes {
		options, _ := body.(map[string]any)
		for key := range options {
			switch key {
			case "labels":
			case "external":
				return refuseCompose("volume %q is external: it is this server's, and a template brings its own", name)
			default:
				return refuseCompose("volume %q sets %s; a template's volumes are plain named volumes", name, key)
			}
		}
	}
	return nil
}

func readComposeService(name string, body map[string]any) (composeService, error) {
	service := composeService{name: name}
	fail := func(format string, args ...any) (composeService, error) {
		return service, refuseCompose("service %q %s", name, fmt.Sprintf(format, args...))
	}
	if !identifierRE.MatchString(name) {
		return fail("is not named in lowercase letters, digits and dashes, which a template's services are")
	}
	keys := make([]string, 0, len(body))
	for key := range body {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := body[key]
		switch {
		case ignoredServiceKeys[key], strings.HasPrefix(key, "x-"):
		case key == "image":
			reference, ok := value.(string)
			if !ok || strings.TrimSpace(reference) == "" {
				return fail("names no image")
			}
			if strings.Contains(reference, "$") {
				return fail("interpolates its image; a template names exactly one")
			}
			service.image.Reference = strings.TrimSpace(reference)
		case key == "build":
			return fail("builds its image; a template runs published images, so push it to a registry and name it")
		case key == "command":
			command, err := composeArguments(value)
			if err != nil {
				return fail("command: %v", err)
			}
			service.image.Command = command
		case key == "pull_policy":
			switch fmt.Sprint(value) {
			case "always":
				service.image.PullPolicy = "always"
			case "missing", "if_not_present":
				service.image.PullPolicy = "missing"
			default:
				return fail("sets pull_policy %v; a template pulls when missing or always", value)
			}
		case key == "environment":
			environment, err := composeEnvironment(value)
			if err != nil {
				return fail("environment: %v", err)
			}
			service.environment = environment
		case key == "ports":
			published, err := composePorts(value)
			if err != nil {
				return fail("ports: %v", err)
			}
			service.published = published
		case key == "expose":
			exposed, err := composeExpose(value)
			if err != nil {
				return fail("expose: %v", err)
			}
			service.exposed = exposed
		case key == "volumes":
			volumes, err := composeServiceVolumes(name, value)
			if err != nil {
				return fail("%v", err)
			}
			service.volumes = volumes
		case key == "healthcheck":
			check, err := composeHealthcheck(value)
			if err != nil {
				return fail("healthcheck: %v", err)
			}
			service.healthcheck = check
		case key == "depends_on":
			dependencies, err := composeDependencies(value)
			if err != nil {
				return fail("depends_on: %v", err)
			}
			service.dependsOn = dependencies
		case key == "mem_limit":
			memory, err := composeMemory(value)
			if err != nil {
				return fail("mem_limit: %v", err)
			}
			service.memoryMB = memory
		case key == "cpus":
			cpus, err := composeCPUs(value)
			if err != nil {
				return fail("cpus: %v", err)
			}
			service.cpus = cpus
		case key == "deploy":
			if err := composeDeploy(&service, value); err != nil {
				return fail("deploy: %v", err)
			}
		default:
			// A key dropped silently changes what the service does — the
			// user it runs as, the host it reaches — so it is named instead.
			return fail("sets %s, which a template cannot carry", key)
		}
	}
	if service.image.Reference == "" {
		return fail("names no image")
	}
	return service, nil
}

// composeArguments reads a command as an argument vector. `$$` is Compose's
// literal dollar; any other `$` is an interpolation, and a command is shown
// in every process list, so a value reaches a service as environment instead.
func composeArguments(value any) ([]string, error) {
	var arguments []string
	switch typed := value.(type) {
	case string:
		arguments = shellWords(typed)
	case []any:
		for _, item := range typed {
			arguments = append(arguments, scalarText(item))
		}
	default:
		return nil, fmt.Errorf("is neither a string nor a list")
	}
	for index, argument := range arguments {
		literal, err := literalDollars(argument)
		if err != nil {
			return nil, err
		}
		arguments[index] = literal
	}
	return arguments, nil
}

// literalDollars reads `$$` as one dollar and refuses an interpolation.
func literalDollars(text string) (string, error) {
	var output strings.Builder
	for index := 0; index < len(text); index++ {
		if text[index] != '$' {
			output.WriteByte(text[index])
			continue
		}
		if index+1 < len(text) && text[index+1] == '$' {
			output.WriteByte('$')
			index++
			continue
		}
		return "", fmt.Errorf("%q reads a variable, which only an environment value may", text)
	}
	return output.String(), nil
}

// shellWords splits a command string the way Compose does for the string
// form: on unquoted whitespace, with single and double quotes grouping.
func shellWords(text string) []string {
	var words []string
	var current strings.Builder
	quote, started := rune(0), false
	for _, r := range text {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote == 0 && (r == '"' || r == '\''):
			quote, started = r, true
		case quote == 0 && (r == ' ' || r == '\t' || r == '\n'):
			if started {
				words = append(words, current.String())
				current.Reset()
				started = false
			}
		default:
			current.WriteRune(r)
			started = true
		}
	}
	if started {
		words = append(words, current.String())
	}
	return words
}

func scalarText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case int:
		return strconv.Itoa(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return fmt.Sprint(typed)
	}
}

func composeEnvironment(value any) ([]composeVariable, error) {
	var entries []composeVariable
	switch typed := value.(type) {
	case map[string]any:
		for name, raw := range typed {
			entries = append(entries, composeVariable{name: name, value: scalarText(raw), passthrough: raw == nil})
		}
	case []any:
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("an entry is not NAME=value")
			}
			name, val, found := strings.Cut(text, "=")
			entries = append(entries, composeVariable{name: name, value: val, passthrough: !found})
		}
	default:
		return nil, fmt.Errorf("is neither a mapping nor a list")
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	for _, entry := range entries {
		if !variableRE.MatchString(entry.name) {
			return nil, fmt.Errorf("%q is not a variable name", entry.name)
		}
	}
	return entries, nil
}

// composePorts reads which container ports a service publishes. The host
// side is dropped on purpose: a template's public service is published on
// loopback on a port of its own and reached through the proxy.
func composePorts(value any) ([]int, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("is not a list")
	}
	var ports []int
	for _, item := range items {
		var container string
		switch typed := item.(type) {
		case int:
			container = strconv.Itoa(typed)
		case string:
			text := typed
			if strings.Contains(text, "$") {
				return nil, fmt.Errorf("%q interpolates a variable", text)
			}
			if body, protocol, found := strings.Cut(text, "/"); found {
				if protocol != "tcp" {
					return nil, fmt.Errorf("%q is %s; a template's public port is TCP behind the proxy", text, protocol)
				}
				text = body
			}
			parts := strings.Split(text, ":")
			container = parts[len(parts)-1]
		case map[string]any:
			if protocol := scalarText(typed["protocol"]); protocol != "" && protocol != "tcp" {
				return nil, fmt.Errorf("port %v is %s; a template's public port is TCP behind the proxy", typed["target"], protocol)
			}
			container = scalarText(typed["target"])
		default:
			return nil, fmt.Errorf("an entry is neither a number, a string nor a mapping")
		}
		port, err := strconv.Atoi(container)
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("%q is not one container port", container)
		}
		if !containsInt(ports, port) {
			ports = append(ports, port)
		}
	}
	return ports, nil
}

func composeExpose(value any) ([]int, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("is not a list")
	}
	var ports []int
	for _, item := range items {
		text, _, _ := strings.Cut(scalarText(item), "/")
		port, err := strconv.Atoi(text)
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("%q is not one port", scalarText(item))
		}
		if !containsInt(ports, port) {
			ports = append(ports, port)
		}
	}
	return ports, nil
}

func containsInt(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

var volumeNameRunRE = regexp.MustCompile(`[^a-z0-9]+`)

// composeServiceVolumes reads a service's mounts as named volumes. A bind
// mount names a path on this server, which the next server does not have,
// so a template refuses one rather than guessing what it held.
func composeServiceVolumes(service string, value any) ([]Volume, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("volumes are not a list")
	}
	var volumes []Volume
	for _, item := range items {
		var source, target string
		readOnly := false
		switch typed := item.(type) {
		case string:
			parts := strings.Split(typed, ":")
			switch len(parts) {
			case 1:
				target = parts[0]
			case 2, 3:
				source, target = parts[0], parts[1]
				if len(parts) == 3 {
					for _, mode := range strings.Split(parts[2], ",") {
						readOnly = readOnly || mode == "ro"
					}
				}
			default:
				return nil, fmt.Errorf("mounts %q, which is not source:target[:mode]", typed)
			}
		case map[string]any:
			kind := scalarText(typed["type"])
			if kind != "" && kind != "volume" {
				return nil, fmt.Errorf("mounts a %s at %v; a template keeps what it writes in named volumes", kind, typed["target"])
			}
			source, target = scalarText(typed["source"]), scalarText(typed["target"])
			readOnly = typed["read_only"] == true
		default:
			return nil, fmt.Errorf("mounts something that is neither a string nor a mapping")
		}
		if strings.Contains(source+target, "$") {
			return nil, fmt.Errorf("mounts %q, which interpolates a variable", source+":"+target)
		}
		if strings.HasPrefix(source, "/") || strings.HasPrefix(source, ".") || strings.HasPrefix(source, "~") {
			return nil, fmt.Errorf("mounts the host path %s; a template keeps what it writes in named volumes, so name one instead", source)
		}
		name := source
		if name == "" {
			// An anonymous volume is kept as a named one: the template's
			// volumes survive a redeploy, and an anonymous one would not.
			name = service + "-" + target[strings.LastIndex(target, "/")+1:]
		}
		name = strings.Trim(volumeNameRunRE.ReplaceAllString(strings.ToLower(name), "-"), "-")
		volumes = append(volumes, Volume{
			Name: name, Target: target, Purpose: "What " + service + " keeps at " + target,
			Data: !readOnly, Backup: !readOnly, ReadOnly: readOnly,
		})
	}
	return volumes, nil
}

func composeHealthcheck(value any) (*Healthcheck, error) {
	body, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("is not a mapping")
	}
	if body["disable"] == true {
		return nil, nil
	}
	check := &Healthcheck{IntervalSeconds: 30, TimeoutSeconds: 30, Retries: 3}
	switch test := body["test"].(type) {
	case string:
		check.Test = []string{"sh", "-c", test}
	case []any:
		if len(test) == 0 {
			return nil, fmt.Errorf("has no test")
		}
		words := make([]string, 0, len(test))
		for _, item := range test {
			words = append(words, scalarText(item))
		}
		switch strings.ToUpper(words[0]) {
		case "NONE":
			return nil, nil
		case "CMD":
			check.Test = words[1:]
		case "CMD-SHELL":
			check.Test = []string{"sh", "-c", strings.Join(words[1:], " ")}
		default:
			check.Test = words
		}
	default:
		return nil, fmt.Errorf("has no test")
	}
	for index, argument := range check.Test {
		// A healthcheck is shown in plain text by docker inspect, so it
		// cannot be handed a value; $$NAME reads it from the container.
		literal, err := literalDollars(argument)
		if err != nil {
			return nil, fmt.Errorf("%v; write $$NAME to read it from the container's own environment", err)
		}
		check.Test[index] = literal
	}
	for key, target := range map[string]*int{
		"interval": &check.IntervalSeconds, "timeout": &check.TimeoutSeconds, "start_period": &check.StartPeriodSeconds,
	} {
		raw, present := body[key]
		if !present {
			continue
		}
		duration, err := time.ParseDuration(scalarText(raw))
		if err != nil {
			return nil, fmt.Errorf("%s %q is not a duration", key, scalarText(raw))
		}
		*target = int(math.Ceil(duration.Seconds()))
	}
	if raw, present := body["retries"]; present {
		retries, err := strconv.Atoi(scalarText(raw))
		if err != nil {
			return nil, fmt.Errorf("retries %q is not a number", scalarText(raw))
		}
		check.Retries = retries
	}
	return check, nil
}

func composeDependencies(value any) ([]Dependency, error) {
	var dependencies []Dependency
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			dependencies = append(dependencies, Dependency{Service: scalarText(item), Condition: StartStarted})
		}
	case map[string]any:
		for name, raw := range typed {
			condition := StartStarted
			if options, ok := raw.(map[string]any); ok {
				switch scalarText(options["condition"]) {
				case "", "service_started":
				case "service_healthy":
					condition = StartHealthy
				case "service_completed_successfully":
					condition = StartCompleted
				default:
					return nil, fmt.Errorf("%s waits for %q", name, scalarText(options["condition"]))
				}
			}
			dependencies = append(dependencies, Dependency{Service: name, Condition: condition})
		}
	default:
		return nil, fmt.Errorf("is neither a list nor a mapping")
	}
	sort.Slice(dependencies, func(i, j int) bool { return dependencies[i].Service < dependencies[j].Service })
	return dependencies, nil
}

var memoryRE = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)\s*([bkmg]?)b?$`)

func composeMemory(value any) (int, error) {
	text := strings.ToLower(strings.TrimSpace(scalarText(value)))
	match := memoryRE.FindStringSubmatch(text)
	if match == nil {
		return 0, fmt.Errorf("%q is not an amount of memory", text)
	}
	amount, _ := strconv.ParseFloat(match[1], 64)
	switch match[2] {
	case "", "b":
		amount /= 1 << 20
	case "k":
		amount /= 1 << 10
	case "g":
		amount *= 1 << 10
	}
	megabytes := int(math.Ceil(amount))
	if megabytes < 6 {
		return 0, fmt.Errorf("%q is less than the 6 MB Docker starts a container with", text)
	}
	return megabytes, nil
}

func composeCPUs(value any) (float64, error) {
	cpus, err := strconv.ParseFloat(strings.TrimSpace(scalarText(value)), 64)
	if err != nil || cpus < 0 {
		return 0, fmt.Errorf("%q is not a number of CPUs", scalarText(value))
	}
	return cpus, nil
}

func composeDeploy(service *composeService, value any) error {
	body, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("is not a mapping")
	}
	for key, raw := range body {
		switch key {
		case "restart_policy", "update_config", "rollback_config", "labels":
		case "replicas":
			if scalarText(raw) != "1" {
				return fmt.Errorf("asks for %v replicas; a template's services run one container each", raw)
			}
		case "resources":
			resources, _ := raw.(map[string]any)
			limits, _ := resources["limits"].(map[string]any)
			if memory, present := limits["memory"]; present {
				megabytes, err := composeMemory(memory)
				if err != nil {
					return err
				}
				service.memoryMB = megabytes
			}
			if cpus, present := limits["cpus"]; present {
				parsed, err := composeCPUs(cpus)
				if err != nil {
					return err
				}
				service.cpus = parsed
			}
		default:
			return fmt.Errorf("sets %s, which a template cannot carry", key)
		}
	}
	return nil
}

// publicComposeService decides which service owns the domain: the one the
// operator named, else the one service that publishes a port, else the one
// that exposes one. Two services publishing is a question the file cannot
// answer, so it is asked rather than guessed.
func publicComposeService(services []composeService, named string) (int, error) {
	if named != "" {
		for index, service := range services {
			if service.name == named {
				if len(service.published) == 0 && len(service.exposed) == 0 {
					return 0, refuseCompose("service %q publishes and exposes no port, so nothing would answer at the domain", named)
				}
				return index, nil
			}
		}
		return 0, refuseCompose("the file has no service %q", named)
	}
	publishing := []int{}
	for index, service := range services {
		if len(service.published) > 0 {
			publishing = append(publishing, index)
		}
	}
	if len(publishing) == 1 {
		return publishing[0], nil
	}
	if len(publishing) > 1 {
		names := []string{}
		for _, index := range publishing {
			names = append(names, services[index].name)
		}
		return 0, refuseCompose("%s each publish a port; a template routes one service to the domain, so name it", strings.Join(names, " and "))
	}
	exposing := []int{}
	for index, service := range services {
		if len(service.exposed) > 0 {
			exposing = append(exposing, index)
		}
	}
	if len(exposing) == 1 {
		return exposing[0], nil
	}
	return 0, refuseCompose("no service publishes a port, so nothing would answer at the domain; publish the public service's port (\"80:80\") or name the service")
}

// publicPort is the container port a public service is routed to.
func (s composeService) publicPort() int {
	if len(s.published) > 0 {
		return s.published[0]
	}
	if len(s.exposed) > 0 {
		return s.exposed[0]
	}
	return 0
}

var (
	databaseImages = []string{"postgres", "postgis", "timescaledb", "mysql", "mariadb", "mongo", "mongodb", "couchdb", "clickhouse", "clickhouse-server", "cassandra", "cockroach"}
	cacheImages    = []string{"redis", "valkey", "keydb", "memcached", "dragonfly"}
)

// imageRepository is an image reference's last path segment without its tag
// or digest: "postgres" for docker.io/library/postgres:17-alpine.
func imageRepository(reference string) string {
	name, _, _ := strings.Cut(reference, "@")
	if slash := strings.LastIndex(name, "/"); slash >= 0 {
		name = name[slash+1:]
	}
	name, _, _ = strings.Cut(name, ":")
	return strings.ToLower(name)
}

func serviceRole(service composeService, public bool) ServiceRole {
	if public {
		return RoleApp
	}
	repository := imageRepository(service.image.Reference)
	for _, prefix := range databaseImages {
		if repository == prefix || strings.HasPrefix(repository, prefix+"-") {
			return RoleDatabase
		}
	}
	for _, prefix := range cacheImages {
		if repository == prefix || strings.HasPrefix(repository, prefix+"-") {
			return RoleCache
		}
	}
	return RoleWorker
}

// tagPolicy reads how fixed an image reference is: a digest or a version tag
// is pinned; "latest" or no tag moves under the template, which the
// definition then has to say in its update notes.
func tagPolicy(reference string) string {
	if strings.Contains(reference, "@sha256:") {
		return "pinned"
	}
	name := reference
	if slash := strings.LastIndex(name, "/"); slash >= 0 {
		name = name[slash+1:]
	}
	_, tag, found := strings.Cut(name, ":")
	if !found || tag == "latest" || tag == "" {
		return "mutable"
	}
	return "pinned"
}

// ImageHomepage is where an image is described: its Docker Hub page, or the
// registry's own address for any other. It is the documentation link of a
// template that names none, since the schema asks every template for one.
func ImageHomepage(reference string) string {
	name, _, _ := strings.Cut(reference, "@")
	parts := strings.Split(name, "/")
	if last := parts[len(parts)-1]; strings.Contains(last, ":") {
		parts[len(parts)-1], _, _ = strings.Cut(last, ":")
	}
	host := ""
	if len(parts) > 1 && (strings.ContainsAny(parts[0], ".:") || parts[0] == "localhost") {
		host, parts = parts[0], parts[1:]
	}
	switch host {
	case "", "docker.io", "index.docker.io", "registry-1.docker.io":
		if len(parts) == 1 || (len(parts) == 2 && parts[0] == "library") {
			return "https://hub.docker.com/_/" + parts[len(parts)-1]
		}
		return "https://hub.docker.com/r/" + strings.Join(parts, "/")
	default:
		return "https://" + host + "/" + strings.Join(parts, "/")
	}
}

// templateVariables is the plan variables a template is assembling: the
// inputs declared and found, the startup operations, and the secrets it
// generates for the literal passwords the file carried.
type templateVariables struct {
	inputs   []Input
	startup  []Operation
	secrets  []Secret
	byName   map[string]bool
	inputVar map[string]int
	setVar   map[string]bool
	// literal maps a password the file wrote out to the secret generated in
	// its place, so a password two services share stays one secret.
	literal map[string]string
	// read is every variable a value of the file reads.
	read map[string]bool
	// domain is the domain input's name, and domainUsed whether a value now
	// reads it, which makes the input required.
	domain     string
	domainUsed bool
}

func newTemplateVariables(declared []Input, startup []Operation) (*templateVariables, error) {
	vars := &templateVariables{
		byName: map[string]bool{}, inputVar: map[string]int{}, setVar: map[string]bool{}, literal: map[string]string{},
		read: map[string]bool{},
	}
	for _, input := range declared {
		if vars.byName[input.Name] {
			return nil, refuseCompose("input %q is declared twice", input.Name)
		}
		vars.byName[input.Name] = true
		if input.Variable != "" {
			vars.inputVar[input.Variable] = len(vars.inputs)
		}
		if input.Kind == InputDomain && vars.domain == "" {
			vars.domain = input.Name
		}
		vars.inputs = append(vars.inputs, input)
	}
	for _, operation := range startup {
		if operation.Kind != OperationSetVariable {
			return nil, refuseCompose("a declared startup operation is %s; only set_variable is declared beside a file", operation.Kind)
		}
		vars.setVar[operation.Name] = true
		vars.startup = append(vars.startup, operation)
	}
	if vars.domain == "" {
		name := vars.uniqueName("domain")
		vars.domain = name
		vars.byName[name] = true
		vars.inputs = append(vars.inputs, Input{
			Name: name, Kind: InputDomain, Label: "Public domain",
			Description: "The address the public service answers at.",
		})
	}
	return vars, nil
}

func (v *templateVariables) uniqueName(base string) string {
	base = strings.Trim(volumeNameRunRE.ReplaceAllString(strings.ToLower(base), "-"), "-")
	if base == "" {
		base = "value"
	}
	if len(base) > 56 {
		base = strings.TrimRight(base[:56], "-")
	}
	name := base
	for suffix := 2; v.byName[name]; suffix++ {
		name = fmt.Sprintf("%s-%d", base, suffix)
	}
	return name
}

func (v *templateVariables) taken(variable string) bool {
	if _, found := v.inputVar[variable]; found {
		return true
	}
	if v.setVar[variable] {
		return true
	}
	for _, secret := range v.secrets {
		if secret.Variable == variable {
			return true
		}
	}
	return false
}

// secretFor is the generated secret that stands for a password the file
// wrote out, made on first sight. The literal — as Compose reads it, `$$`
// already one dollar — is only ever a map key.
func (v *templateVariables) secretFor(literal, variable string) string {
	if name, found := v.literal[literal]; found {
		return name
	}
	name := v.uniqueName(variable)
	v.byName[name] = true
	candidate := variable
	for suffix := 2; v.taken(candidate); suffix++ {
		candidate = fmt.Sprintf("%s_%d", variable, suffix)
	}
	v.secrets = append(v.secrets, Secret{
		Name: name, Variable: candidate, Label: variable, Length: 32,
		Description: "Generated for this deployment in place of the password the Compose file wrote out.",
	})
	v.literal[literal] = name
	return name
}

// reference answers a ${VAR} the file reads: the declared input or startup
// operation that sets VAR, else a new input named after it. A
// credential-shaped variable is a secret input, supplied on the Variables
// screen — unless the file gave it a default, which is a password written
// out, and so a secret the template generates instead.
func (v *templateVariables) reference(variable, fallback string, hasDefault bool) string {
	v.read[variable] = true
	if _, found := v.inputVar[variable]; found || v.setVar[variable] {
		return "{{variable." + variable + "}}"
	}
	for _, secret := range v.secrets {
		if secret.Variable == variable {
			return "{{secret." + secret.Name + "}}"
		}
	}
	if composeSecretShaped(variable) {
		if hasDefault && fallback != "" {
			return "{{secret." + v.secretFor(fallback, variable) + "}}"
		}
		v.addInput(Input{Name: v.uniqueName(variable), Kind: InputSecret, Label: variable, Variable: variable, Required: true})
		return "{{variable." + variable + "}}"
	}
	v.addInput(Input{
		Name: v.uniqueName(variable), Kind: InputText, Label: variable, Variable: variable,
		Default: fallback, Required: !hasDefault,
	})
	return "{{variable." + variable + "}}"
}

func (v *templateVariables) addInput(input Input) {
	v.byName[input.Name] = true
	v.inputVar[input.Variable] = len(v.inputs)
	v.inputs = append(v.inputs, input)
}

// fromDomain sets a variable whose value names the concrete domain from the
// domain input instead, which is how a built-in such as n8n's writes its
// webhook URL.
func (v *templateVariables) fromDomain(variable, value, domain string) string {
	v.domainUsed, v.read[variable] = true, true
	if !v.setVar[variable] {
		v.setVar[variable] = true
		v.startup = append(v.startup, Operation{
			Kind: OperationSetVariable, Name: variable,
			Value: strings.ReplaceAll(value, domain, "{{input."+v.domain+"}}"),
		})
	}
	return "{{variable." + variable + "}}"
}

// finish drops what the file never reads when asked to, and makes the domain
// input required once a value reads it: an operation may not template an
// input that renders as an empty substitution.
func (v *templateVariables) finish(prune bool) {
	if prune {
		inputs := v.inputs[:0]
		for _, input := range v.inputs {
			if input.Variable == "" || input.Name == v.domain || v.read[input.Variable] {
				inputs = append(inputs, input)
			}
		}
		v.inputs = inputs
		startup := v.startup[:0]
		for _, operation := range v.startup {
			if v.read[operation.Name] {
				startup = append(startup, operation)
			}
		}
		v.startup = startup
	}
	if !v.domainUsed {
		for _, operation := range v.startup {
			if strings.Contains(operation.Value, "{{input."+v.domain+"}}") {
				v.domainUsed = true
			}
		}
	}
	if !v.domainUsed {
		return
	}
	for index := range v.inputs {
		if v.inputs[index].Name == v.domain && v.inputs[index].Default == "" {
			v.inputs[index].Required = true
		}
	}
}

// segment is one stretch of an environment value: literal text, or a
// variable Compose interpolates.
type segment struct {
	literal    string
	variable   string
	fallback   string
	hasDefault bool
}

// interpolation reads Compose's own syntax: `$$` is a dollar, `${NAME}`,
// `${NAME:-default}`, `${NAME-default}`, `${NAME:?error}` and `$NAME` read a
// variable. `${NAME:+…}` writes text only when a variable is set, which a
// template has no way to say, so it is refused.
func interpolation(text string) ([]segment, error) {
	var segments []segment
	var literal strings.Builder
	flush := func() {
		if literal.Len() > 0 {
			segments = append(segments, segment{literal: literal.String()})
			literal.Reset()
		}
	}
	for index := 0; index < len(text); index++ {
		if text[index] != '$' {
			literal.WriteByte(text[index])
			continue
		}
		if index+1 >= len(text) {
			literal.WriteByte('$')
			continue
		}
		next := text[index+1]
		switch {
		case next == '$':
			literal.WriteByte('$')
			index++
		case next == '{':
			end := strings.IndexByte(text[index:], '}')
			if end < 0 {
				return nil, fmt.Errorf("%q opens ${ without closing it", text)
			}
			body := text[index+2 : index+end]
			if strings.Contains(body, "$") {
				return nil, fmt.Errorf("%q nests one variable inside another", text)
			}
			name, rest := body, ""
			if cut := strings.IndexAny(body, ":-?+"); cut >= 0 {
				name, rest = body[:cut], body[cut:]
			}
			if !variableRE.MatchString(name) {
				return nil, fmt.Errorf("%q reads %q, which is not a variable name", text, name)
			}
			current := segment{variable: name}
			switch {
			case rest == "":
			case strings.HasPrefix(rest, ":-"), strings.HasPrefix(rest, "-"):
				current.fallback, current.hasDefault = strings.TrimPrefix(strings.TrimPrefix(rest, ":"), "-"), true
			case strings.HasPrefix(rest, ":?"), strings.HasPrefix(rest, "?"):
			default:
				return nil, fmt.Errorf("%q uses ${%s}, which a template cannot express", text, body)
			}
			flush()
			segments = append(segments, current)
			index += end
		case next == '_' || (next >= 'A' && next <= 'Z') || (next >= 'a' && next <= 'z'):
			end := index + 1
			for end < len(text) && (text[end] == '_' || (text[end] >= 'A' && text[end] <= 'Z') ||
				(text[end] >= 'a' && text[end] <= 'z') || (text[end] >= '0' && text[end] <= '9')) {
				end++
			}
			flush()
			segments = append(segments, segment{variable: text[index+1 : end]})
			index = end - 1
		default:
			literal.WriteByte('$')
		}
	}
	flush()
	return segments, nil
}

// environmentValue turns one variable of the file into a template value:
// each interpolation a reference to the input or operation that sets it, a
// literal password a generated secret, the password inside a URL the same,
// and a value naming the concrete domain one set from the domain input.
func (v *templateVariables) environmentValue(entry composeVariable, domain string) (string, error) {
	if entry.passthrough {
		return v.reference(entry.name, "", false), nil
	}
	segments, err := interpolation(entry.value)
	if err != nil {
		return "", err
	}
	literalOnly := true
	for _, part := range segments {
		if part.variable != "" {
			literalOnly = false
		}
	}
	if composeSecretShaped(entry.name) {
		switch {
		case len(segments) == 1 && segments[0].variable != "":
			part := segments[0]
			return v.reference(part.variable, part.fallback, part.hasDefault), nil
		case literalOnly && entry.value != "":
			literal, _ := literalValue(entry)
			return "{{secret." + v.secretFor(literal, entry.name) + "}}", nil
		case literalOnly:
			return "", fmt.Errorf("%s is credential-shaped and empty; give it a value or leave it out", entry.name)
		default:
			return "", fmt.Errorf("%s is credential-shaped, so its whole value must be one variable or one password", entry.name)
		}
	}
	if domain != "" && strings.Contains(entry.value, domain) {
		if !literalOnly {
			return "", fmt.Errorf("%s names the domain %s and reads a variable as well; set it from one or the other", entry.name, domain)
		}
		return v.fromDomain(entry.name, entry.value, domain), nil
	}
	var output strings.Builder
	for _, part := range segments {
		if part.variable != "" {
			output.WriteString(v.reference(part.variable, part.fallback, part.hasDefault))
			continue
		}
		if strings.Contains(part.literal, "{{") || strings.Contains(part.literal, "}}") {
			return "", fmt.Errorf("%s holds braces a template reads as a placeholder", entry.name)
		}
		output.WriteString(v.withoutURLPasswords(part.literal, entry.name))
	}
	return output.String(), nil
}

// withoutURLPasswords swaps the password of every URL in a literal for the
// secret generated in its place.
func (v *templateVariables) withoutURLPasswords(literal, variable string) string {
	return urlUserinfoRE.ReplaceAllStringFunc(literal, func(match string) string {
		parts := urlUserinfoRE.FindStringSubmatch(match)
		user, password, found := strings.Cut(parts[1], ":")
		if !found || password == "" {
			return match
		}
		secret := v.secretFor(password, variable+"_PASSWORD")
		return strings.Replace(match, parts[1], user+":{{secret."+secret+"}}", 1)
	})
}

func (meta TemplateMeta) complete(publicImage string) (TemplateMeta, error) {
	if strings.TrimSpace(meta.Name) == "" {
		return meta, refuseCompose("a template needs a name")
	}
	if meta.ID == "" {
		id, err := UserID(meta.Name)
		if err != nil {
			return meta, err
		}
		meta.ID = id
	}
	if meta.Category == "" {
		meta.Category = CategoryHTTP
	}
	if meta.DocsURL == "" {
		meta.DocsURL = ImageHomepage(publicImage)
	}
	if meta.Access == nil {
		meta.Access = &Access{
			Kind: AccessOpen,
			Note: "This template was added on this dashboard, and it says nothing about how the application signs people in, so treat its address as open to whoever reaches it until you have checked.",
		}
	}
	if meta.Maintainer == "" {
		meta.Maintainer = "This dashboard"
	}
	if meta.ReviewedAt == "" {
		meta.ReviewedAt = time.Now().UTC().Format("2006-01-02")
	}
	if meta.MinimumDashboard == "" {
		meta.MinimumDashboard = "0.0.0"
	}
	return meta, nil
}

func (meta TemplateMeta) blueprint(profile Profile, description string) *Blueprint {
	if strings.TrimSpace(meta.Description) != "" {
		description = strings.TrimSpace(meta.Description)
	}
	return &Blueprint{
		ID: meta.ID, Version: "1.0.0", Name: strings.TrimSpace(meta.Name), Category: meta.Category,
		Profile: profile, Description: description, IconID: "globe", DocsURL: meta.DocsURL,
		Access: *meta.Access,
		Provenance: Provenance{
			Maintainer: meta.Maintainer, License: "Licence not stated", UpstreamURL: meta.DocsURL,
			ReviewedAt: meta.ReviewedAt, MinimumDashboard: meta.MinimumDashboard,
		},
		Update: Update{Detector: "registry"},
	}
}

// defaultMemory is a service's limit when the file sets none: enough for
// what each role usually is, and visible in the picker before the press.
func defaultMemory(role ServiceRole) int {
	switch role {
	case RoleApp, RoleDatabase:
		return 512
	default:
		return 256
	}
}

func composeStackTemplate(spec ComposeTemplate, services []composeService, publicIndex int) (*Blueprint, error) {
	public := services[publicIndex]
	meta, err := spec.TemplateMeta.complete(public.image.Reference)
	if err != nil {
		return nil, err
	}
	vars, err := newTemplateVariables(spec.Inputs, spec.Startup)
	if err != nil {
		return nil, err
	}
	definition := meta.blueprint(ProfileCompose, "")
	// A password the file writes out is named after the variable that holds
	// it (POSTGRES_PASSWORD) rather than after a URL that repeats it, so the
	// variables are registered before any URL is read.
	for _, service := range services {
		for _, entry := range service.environment {
			if literal, ok := literalValue(entry); ok && literal != "" && composeSecretShaped(entry.name) {
				vars.secretFor(literal, entry.name)
			}
		}
	}
	shareVolumeDescriptions(services)
	names := make([]string, 0, len(services))
	mutable, database := []string{}, false
	for index, service := range services {
		isPublic := index == publicIndex
		if !isPublic && len(service.published) > 0 {
			return nil, refuseCompose("service %q publishes a port, and only the public service %q is reached from outside the stack", service.name, public.name)
		}
		if service.healthcheck == nil {
			return nil, refuseCompose("service %q has no healthcheck; a template needs one on every service, since Compose starts what depends on a service only once it is healthy", service.name)
		}
		role := serviceRole(service, isPublic)
		database = database || role == RoleDatabase
		names = append(names, service.name)
		image := service.image
		image.TagPolicy = tagPolicy(image.Reference)
		if image.PullPolicy == "" {
			image.PullPolicy = "missing"
		}
		if image.TagPolicy == "mutable" {
			mutable = append(mutable, image.Reference)
		}
		built := Service{
			Name: service.name, Role: role, Image: image, Volumes: service.volumes,
			Healthcheck: *service.healthcheck, DependsOn: service.dependsOn,
			Purpose:   "The " + service.name + " service, private to the stack",
			Resources: Resources{MemoryMB: service.memoryMB, CPUs: service.cpus},
		}
		if isPublic {
			built.Purpose = "The " + service.name + " service, answering at the domain"
		}
		if built.Resources.MemoryMB == 0 {
			built.Resources.MemoryMB = defaultMemory(role)
		}
		built.Resources.MinMemoryMB = built.Resources.MemoryMB / 2
		if isPublic {
			built.Ports = append(built.Ports, Port{
				Name: "http", Internal: public.publicPort(), Protocol: "tcp",
				Purpose: "Answers at the domain, through the proxy", Exposure: "proxy", Primary: true,
			})
		}
		for _, port := range append(append([]int{}, service.published...), service.exposed...) {
			if isPublic && port == public.publicPort() {
				continue
			}
			if isPublic && containsInt(service.published, port) {
				return nil, refuseCompose("service %q publishes %d as well as %d; a template routes one port to the domain and publishes nothing else", service.name, port, public.publicPort())
			}
			name := fmt.Sprintf("port-%d", port)
			if !containsPort(built.Ports, name) {
				built.Ports = append(built.Ports, Port{
					Name: name, Internal: port, Protocol: "tcp", Exposure: "internal",
					Purpose: "Reached by the other services of the stack",
				})
			}
		}
		for _, entry := range service.environment {
			value, err := vars.environmentValue(entry, spec.Domain)
			if err != nil {
				return nil, refuseCompose("service %q: %v", service.name, err)
			}
			built.Environment = append(built.Environment, Environment{Name: entry.name, Value: value})
		}
		definition.Services = append(definition.Services, built)
	}
	vars.finish(spec.PruneUnread)
	definition.Inputs, definition.Secrets = vars.inputs, vars.secrets
	definition.Operations = Operations{Startup: vars.startup}
	definition.Checks = []Check{readinessCheck(spec.HealthPath, "http")}
	definition.Update.BackupFirst = database
	if len(mutable) > 0 {
		definition.Update.Notes = "Follows a moving tag (" + strings.Join(mutable, ", ") + "): each deploy pulls whatever it names then."
	}
	if definition.Description == "" {
		definition.Description = fmt.Sprintf("%s, from a Compose file added on this dashboard: %s, with %s answering at the domain.",
			definition.Name, strings.Join(names, ", "), public.name)
	}
	definition.Fixtures = []Fixture{{Name: "default", Inputs: fixtureInputs(definition.Inputs)}}
	return definition, nil
}

// literalValue is a variable's value when it reads no other variable, with
// Compose's `$$` read as one dollar.
func literalValue(entry composeVariable) (string, bool) {
	if entry.passthrough {
		return "", false
	}
	segments, err := interpolation(entry.value)
	if err != nil {
		return "", false
	}
	var literal strings.Builder
	for _, part := range segments {
		if part.variable != "" {
			return "", false
		}
		literal.WriteString(part.literal)
	}
	return literal.String(), true
}

// shareVolumeDescriptions describes a volume two services mount the same way
// for both, as the template's validation requires of one volume. A volume
// one of them mounts read-only is the other's output, which the stack keeps
// and backs up without calling it data a read-only mount would contradict.
func shareVolumeDescriptions(services []composeService) {
	readOnly := map[string]bool{}
	for _, service := range services {
		for _, volume := range service.volumes {
			readOnly[volume.Name] = readOnly[volume.Name] || volume.ReadOnly
		}
	}
	for index := range services {
		for position := range services[index].volumes {
			volume := &services[index].volumes[position]
			volume.Data, volume.Backup = !readOnly[volume.Name], true
		}
	}
}

func containsPort(ports []Port, name string) bool {
	for _, port := range ports {
		if port.Name == name {
			return true
		}
	}
	return false
}

func readinessCheck(path, port string) Check {
	if strings.HasPrefix(path, "/") {
		return Check{Name: "Answers " + path, Kind: CheckHTTP, Phase: "readiness", Required: true, Path: path, Port: port, TimeoutSeconds: 120}
	}
	return Check{Name: "Accepts connections", Kind: CheckTCP, Phase: "readiness", Required: true, Port: port, TimeoutSeconds: 120}
}

// fixtureInputs is the answer the template's own fixture renders with: an
// example for every input the operator has to answer, and the domain, so the
// fixture exercises the route as well.
func fixtureInputs(inputs []Input) map[string]string {
	values := map[string]string{}
	for _, input := range inputs {
		if input.Default != "" || input.Kind == InputSecret {
			continue
		}
		switch input.Kind {
		case InputDomain:
			values[input.Name] = "app.example.test"
		case InputNumber, InputMemory:
			if input.Required {
				values[input.Name] = strconv.Itoa(max(input.Minimum, 1))
			}
		case InputBoolean:
			if input.Required {
				values[input.Name] = "false"
			}
		case InputAccept:
			values[input.Name] = "true"
		case InputChoice:
			if input.Required && len(input.Choices) > 0 {
				values[input.Name] = input.Choices[0].Value
			}
		default:
			if input.Required {
				values[input.Name] = "example"
			}
		}
	}
	return values
}

// singleServiceTemplate is a Compose file with one service, which is one
// image: deployed as a single reviewed image is, not as a stack of one.
func singleServiceTemplate(spec ComposeTemplate, service composeService) (*Blueprint, error) {
	vars, err := newTemplateVariables(spec.Inputs, spec.Startup)
	if err != nil {
		return nil, err
	}
	// One image's variables are inputs, generated secrets and startup
	// operations of its own name: there is no service environment to write
	// a reference into, so each variable of the file is one of those three.
	for _, entry := range service.environment {
		if err := vars.imageVariable(entry, spec.Domain); err != nil {
			return nil, refuseCompose("service %q: %v", service.name, err)
		}
	}
	image := ImageTemplate{
		TemplateMeta: spec.TemplateMeta, Image: service.image.Reference, Command: service.image.Command,
		Port: service.publicPort(), Protocol: "tcp", Volumes: service.volumes,
		MemoryMB: service.memoryMB, CPUs: service.cpus, HealthPath: spec.HealthPath,
	}
	vars.finish(spec.PruneUnread)
	image.Inputs, image.Secrets, image.Startup = vars.inputs, vars.secrets, vars.startup
	definition, err := FromImage(image)
	if err != nil {
		return nil, err
	}
	definition.Image.PullPolicy = service.image.PullPolicy
	if definition.Image.PullPolicy == "" {
		definition.Image.PullPolicy = "missing"
	}
	return definition, nil
}

// imageVariable is one variable of a single image: an input of its own name,
// a secret generated for a password the file wrote out, or a startup
// operation for a literal or for a value built from inputs.
func (v *templateVariables) imageVariable(entry composeVariable, domain string) error {
	if entry.passthrough {
		v.reference(entry.name, "", false)
		return nil
	}
	segments, err := interpolation(entry.value)
	if err != nil {
		return err
	}
	if len(segments) == 1 && segments[0].variable == entry.name {
		part := segments[0]
		v.reference(part.variable, part.fallback, part.hasDefault)
		return nil
	}
	literalOnly := true
	for _, part := range segments {
		if part.variable != "" {
			literalOnly = false
		}
	}
	if composeSecretShaped(entry.name) {
		if !literalOnly || entry.value == "" {
			return fmt.Errorf("%s is credential-shaped, so it is either ${%s} or a password the template generates", entry.name, entry.name)
		}
		v.read[entry.name] = true
		literal, _ := literalValue(entry)
		v.secretFor(literal, entry.name)
		return nil
	}
	if literalOnly && urlUserinfoRE.MatchString(entry.value) {
		return fmt.Errorf("%s writes a password into a URL; a single image cannot be handed a generated one there", entry.name)
	}
	if domain != "" && strings.Contains(entry.value, domain) {
		if !literalOnly {
			return fmt.Errorf("%s names the domain %s and reads a variable as well; set it from one or the other", entry.name, domain)
		}
		v.fromDomain(entry.name, entry.value, domain)
		return nil
	}
	// A literal, or text around inputs: a startup operation, which may name
	// an input only by {{input.name}} and only one that cannot be empty.
	var value strings.Builder
	for _, part := range segments {
		if part.variable == "" {
			if strings.Contains(part.literal, "{{") || strings.Contains(part.literal, "}}") {
				return fmt.Errorf("%s holds braces a template reads as a placeholder", entry.name)
			}
			value.WriteString(part.literal)
			continue
		}
		if composeSecretShaped(part.variable) {
			return fmt.Errorf("%s reads %s, a secret, inside other text; a secret reaches a single image under its own name", entry.name, part.variable)
		}
		v.reference(part.variable, part.fallback, part.hasDefault)
		input := v.inputs[v.inputVar[part.variable]]
		if !input.Required && input.Default == "" {
			return fmt.Errorf("%s reads %s, which may be empty; give it a default", entry.name, part.variable)
		}
		value.WriteString("{{input." + input.Name + "}}")
	}
	if v.taken(entry.name) {
		return fmt.Errorf("%s is set twice", entry.name)
	}
	v.setVar[entry.name], v.read[entry.name] = true, true
	v.startup = append(v.startup, Operation{Kind: OperationSetVariable, Name: entry.name, Value: value.String()})
	return nil
}

// FromImage builds a single-image template. Its port is routed through the
// proxy unless it is direct; a direct port's host number, and every other
// published port's, becomes a number input defaulting to it (Port.HostInput),
// so a template saved from a deployment keeps the publication it chose as an
// answer the next deployment can change.
func FromImage(spec ImageTemplate) (*Blueprint, error) {
	meta, err := spec.TemplateMeta.complete(spec.Image)
	if err != nil {
		return nil, err
	}
	profile := ProfileWeb
	if spec.Port == 0 {
		profile = ProfileWorker
	} else if spec.Direct {
		profile = ProfileTool
	}
	definition := meta.blueprint(profile, "")
	definition.Image = Image{
		Reference: spec.Image, TagPolicy: tagPolicy(spec.Image), PullPolicy: "missing",
		Command: append([]string(nil), spec.Command...),
	}
	definition.Inputs = append([]Input(nil), spec.Inputs...)
	definition.Secrets = append([]Secret(nil), spec.Secrets...)
	definition.Volumes = append([]Volume(nil), spec.Volumes...)
	definition.Operations = Operations{Startup: append([]Operation(nil), spec.Startup...)}
	names := map[string]bool{}
	for _, input := range definition.Inputs {
		names[input.Name] = true
	}
	hostInput := func(base string, port int) string {
		name := base
		for suffix := 2; names[name]; suffix++ {
			name = fmt.Sprintf("%s-%d", base, suffix)
		}
		names[name] = true
		definition.Inputs = append(definition.Inputs, Input{
			Name: name, Kind: InputNumber, Label: fmt.Sprintf("Host port for %d", port),
			Description: "The port on this server the container's port is published on.",
			Default:     strconv.Itoa(port), Minimum: 1, Maximum: 65535, Advanced: true,
		})
		return name
	}
	protocol := strings.ToLower(spec.Protocol)
	if protocol == "" {
		protocol = "tcp"
	}
	if spec.Port > 0 {
		port := Port{Name: "http", Internal: spec.Port, Protocol: protocol, Purpose: "Answers at the domain, through the proxy", Exposure: "proxy", Primary: true}
		if spec.Direct {
			port.Name, port.Exposure, port.Purpose = "main", "direct", "Published on this server for clients the proxy cannot carry"
			if spec.HostPort > 0 {
				port.HostInput = hostInput("host-port", spec.HostPort)
			}
		}
		definition.Ports = append(definition.Ports, port)
	}
	for _, published := range spec.Published {
		name := fmt.Sprintf("port-%d", published.Container)
		if published.Protocol != "" && published.Protocol != "tcp" {
			name += "-" + published.Protocol
		}
		port := Port{
			Name: name, Internal: published.Container, Protocol: strings.ToLower(published.Protocol),
			Purpose: "Published on this server beside the main port", Exposure: "direct",
		}
		if port.Protocol == "" {
			port.Protocol = "tcp"
		}
		if published.Host > 0 && published.Host != published.Container {
			port.HostInput = hostInput(fmt.Sprintf("host-port-%d", published.Container), published.Host)
		}
		definition.Ports = append(definition.Ports, port)
	}
	definition.Resources = Resources{MemoryMB: spec.MemoryMB, CPUs: spec.CPUs}
	if definition.Resources.MemoryMB <= 0 {
		definition.Resources.MemoryMB = 512
	}
	definition.Resources.MinMemoryMB = definition.Resources.MemoryMB / 2
	definition.Checks = append([]Check(nil), spec.Checks...)
	if len(definition.Checks) == 0 {
		switch {
		case spec.Port > 0 && protocol == "tcp":
			definition.Checks = []Check{readinessCheck(spec.HealthPath, definition.Ports[0].Name)}
		default:
			definition.Checks = []Check{{Name: "Container is healthy", Kind: CheckDocker, Phase: "readiness", Required: true, TimeoutSeconds: 120}}
		}
	}
	if definition.Image.TagPolicy == "mutable" {
		definition.Update.Notes = "Follows the moving tag " + spec.Image + ": each deploy pulls whatever it names then."
	}
	if definition.Description == "" {
		definition.Description = fmt.Sprintf("%s, the image %s, as a template added on this dashboard.", definition.Name, spec.Image)
	}
	definition.Fixtures = []Fixture{{Name: "default", Inputs: fixtureInputs(definition.Inputs)}}
	return definition, nil
}
