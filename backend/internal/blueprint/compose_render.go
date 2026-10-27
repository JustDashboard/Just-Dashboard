package blueprint

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ComposeFile is the path a Compose template's document is written to in a
// release workspace.
const ComposeFile = "compose.yaml"

// Networks a rendered stack uses. The public service is on Compose's default
// network and on the private one; everything else is on the private one only.
// The ingress joins the public container on the first of its networks by name
// (proxysvc.publishedTarget), and "default" sorts before "internal", so the
// proxy that faces the internet shares a network with the public service and
// with nothing behind it.
const (
	composeEdgeNetwork    = "default"
	composePrivateNetwork = "internal"
	composeEgressNetwork  = "egress"
)

// RenderedService is one service of a rendered Compose template, as the
// picker and the deployment's own pages describe it.
type RenderedService struct {
	Name      string           `json:"name"`
	Role      ServiceRole      `json:"role"`
	Purpose   string           `json:"purpose"`
	Image     string           `json:"image"`
	Public    bool             `json:"public,omitempty"`
	MemoryMB  int              `json:"memoryMb"`
	CPUs      float64          `json:"cpus,omitempty"`
	Volumes   []RenderedVolume `json:"volumes"`
	DependsOn []Dependency     `json:"dependsOn,omitempty"`
}

// TemplateHostPort is the loopback port a template's public service asks for.
// It is only a preference: Compose's own allocation moves it to a free port
// when another deployment of the same template already holds it, and keeps
// that choice for the release. Deriving it from the template keeps the
// rendered document a function of the definition alone.
func TemplateHostPort(id string) int {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(id))
	return 20000 + int(hash.Sum32()%20000)
}

// renderComposeTemplate fills the plan fields a Compose template answers
// differently from a single image: the image and port the release follows are
// the public service's, the footprint is the sum of every service's limit, and
// the document itself is rendered.
func renderComposeTemplate(blueprint *Blueprint, plan *Plan) error {
	public := blueprint.PublicService()
	if public == nil {
		return fmt.Errorf("%w: %s has no public service", ErrInvalidInput, blueprint.ID)
	}
	plan.Image, plan.PullPolicy = public.Image.Reference, public.Image.PullPolicy
	if plan.PullPolicy == "" {
		plan.PullPolicy = "missing"
	}
	plan.Command, plan.MemoryMB, plan.CPUs = nil, 0, 0
	for _, service := range blueprint.Services {
		plan.MemoryMB += service.Resources.MemoryMB
		plan.CPUs += service.Resources.CPUs
		rendered := RenderedService{
			Name: service.Name, Role: service.Role, Purpose: service.Purpose,
			Image: service.Image.Reference, Public: service.Name == public.Name,
			MemoryMB: service.Resources.MemoryMB, CPUs: service.Resources.CPUs,
			Volumes:   []RenderedVolume{},
			DependsOn: append([]Dependency(nil), service.DependsOn...),
		}
		for _, volume := range service.Volumes {
			rendered.Volumes = append(rendered.Volumes, RenderedVolume{
				Name: volume.Name, Target: volume.Target, Purpose: volume.Purpose,
				Data: volume.Data, Backup: volume.Backup, ReadOnly: volume.ReadOnly,
			})
		}
		plan.Services = append(plan.Services, rendered)
	}
	document, err := renderComposeDocument(blueprint, public)
	if err != nil {
		return err
	}
	plan.Compose = document
	return nil
}

// renderComposeDocument writes the stack as a Compose file. Every value a
// deployment varies — a generated password, a domain, an operator's input —
// is a `${NAME}` interpolation of the deployment's own variables, which the
// release hands Compose through a private environment file; the document is
// a function of the reviewed definition and holds no value at all.
func renderComposeDocument(blueprint *Blueprint, public *Service) (string, error) {
	secretVariables := map[string]string{}
	for _, secret := range blueprint.Secrets {
		secretVariables[secret.Name] = secret.Variable
	}
	services := mappingNode()
	volumes := mappingNode()
	declaredVolumes := map[string]bool{}
	egress := false
	for _, service := range blueprint.Services {
		node := mappingNode()
		addPair(node, "image", quoted(service.Image.Reference))
		if len(service.Image.Command) > 0 {
			addPair(node, "command", argumentList(service.Image.Command))
		}
		if service.Image.PullPolicy == "always" {
			addPair(node, "pull_policy", plainNode("always"))
		}
		addPair(node, "restart", plainNode("unless-stopped"))
		if len(service.Environment) > 0 {
			environment := mappingNode()
			for _, entry := range service.Environment {
				value, err := interpolatedValue(entry.Value, secretVariables)
				if err != nil {
					return "", fmt.Errorf("%w: service %s variable %s: %v", ErrInvalidInput, service.Name, entry.Name, err)
				}
				addPair(environment, entry.Name, quoted(value))
			}
			addPair(node, "environment", environment)
		}
		isPublic := service.Name == public.Name
		if isPublic {
			for _, port := range service.Ports {
				if port.Exposure == "proxy" {
					// Loopback only: the ingress finds the container behind this
					// publication and reaches it over the edge network.
					binding := fmt.Sprintf("127.0.0.1:%d:%d", TemplateHostPort(blueprint.ID), port.Internal)
					addPair(node, "ports", sequenceNode(quoted(binding)))
				}
			}
		}
		if len(service.Volumes) > 0 {
			mounts := sequenceNode()
			for _, volume := range service.Volumes {
				mount := volume.Name + ":" + volume.Target
				if volume.ReadOnly {
					mount += ":ro"
				}
				mounts.Content = append(mounts.Content, quoted(mount))
				if !declaredVolumes[volume.Name] {
					declaredVolumes[volume.Name] = true
					addPair(volumes, volume.Name, flowMapping())
				}
			}
			addPair(node, "volumes", mounts)
		}
		if len(service.DependsOn) > 0 {
			dependencies := mappingNode()
			for _, dependency := range service.DependsOn {
				condition := mappingNode()
				addPair(condition, "condition", plainNode("service_"+string(dependency.Condition)))
				addPair(dependencies, dependency.Service, condition)
			}
			addPair(node, "depends_on", dependencies)
		}
		healthcheck := mappingNode()
		test := append([]string{"CMD"}, service.Healthcheck.Test...)
		addPair(healthcheck, "test", argumentList(test))
		addPair(healthcheck, "interval", quoted(strconv.Itoa(service.Healthcheck.IntervalSeconds)+"s"))
		addPair(healthcheck, "timeout", quoted(strconv.Itoa(service.Healthcheck.TimeoutSeconds)+"s"))
		addPair(healthcheck, "retries", intNode(service.Healthcheck.Retries))
		if service.Healthcheck.StartPeriodSeconds > 0 {
			addPair(healthcheck, "start_period", quoted(strconv.Itoa(service.Healthcheck.StartPeriodSeconds)+"s"))
		}
		addPair(node, "healthcheck", healthcheck)
		addPair(node, "mem_limit", quoted(strconv.Itoa(service.Resources.MemoryMB)+"m"))
		if service.Resources.CPUs > 0 {
			addPair(node, "cpus", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!float",
				Value: strconv.FormatFloat(service.Resources.CPUs, 'f', -1, 64)})
		}
		// The same cap every container the dashboard creates gets: on one
		// server, an unrotated log is how a chatty service fills the disk.
		logging := mappingNode()
		addPair(logging, "driver", plainNode("json-file"))
		options := mappingNode()
		addPair(options, "max-size", quoted("10m"))
		addPair(options, "max-file", quoted("3"))
		addPair(logging, "options", options)
		addPair(node, "logging", logging)
		networks := sequenceNode()
		if isPublic {
			networks.Content = append(networks.Content, plainNode(composeEdgeNetwork))
		}
		networks.Content = append(networks.Content, plainNode(composePrivateNetwork))
		if service.Egress {
			egress = true
			networks.Content = append(networks.Content, plainNode(composeEgressNetwork))
		}
		addPair(node, "networks", networks)
		addPair(services, service.Name, node)
	}
	root := mappingNode()
	root.HeadComment = fmt.Sprintf("Rendered by Just Dashboard from the reviewed %s@%s template.\n"+
		"Values are the deployment's own variables; this file holds none.", blueprint.ID, blueprint.Version)
	addPair(root, "services", services)
	if len(volumes.Content) > 0 {
		addPair(root, "volumes", volumes)
	}
	networks := mappingNode()
	private := mappingNode()
	// No route out: a database or a cache has no reason to reach the
	// internet, and a private service that does says so with egress.
	addPair(private, "internal", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"})
	addPair(networks, composePrivateNetwork, private)
	if egress {
		addPair(networks, composeEgressNetwork, flowMapping())
	}
	addPair(root, "networks", networks)

	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(&yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}); err != nil {
		return "", err
	}
	if err := encoder.Close(); err != nil {
		return "", err
	}
	return buffer.String(), nil
}

// interpolatedValue turns a service value into Compose text: each typed
// reference becomes `${NAME}` and every literal dollar is doubled, so nothing
// the template writes as text is ever read by Compose as a variable.
func interpolatedValue(value string, secretVariables map[string]string) (string, error) {
	var output strings.Builder
	last := 0
	for _, match := range serviceReferenceRE.FindAllStringSubmatchIndex(value, -1) {
		output.WriteString(strings.ReplaceAll(value[last:match[0]], "$", "$$"))
		kind, name := value[match[2]:match[3]], value[match[4]:match[5]]
		variable := name
		if kind == "secret" {
			resolved, found := secretVariables[name]
			if !found {
				return "", fmt.Errorf("secret %q is not generated by this template", name)
			}
			variable = resolved
		}
		output.WriteString("${" + variable + "}")
		last = match[1]
	}
	output.WriteString(strings.ReplaceAll(value[last:], "$", "$$"))
	return output.String(), nil
}

func argumentList(arguments []string) *yaml.Node {
	list := sequenceNode()
	list.Style = yaml.FlowStyle
	for _, argument := range arguments {
		list.Content = append(list.Content, quoted(strings.ReplaceAll(argument, "$", "$$")))
	}
	return list
}

func mappingNode() *yaml.Node { return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"} }

func flowMapping() *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Style: yaml.FlowStyle}
}

func sequenceNode(items ...*yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: items}
}

func plainNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func quoted(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value, Style: yaml.DoubleQuotedStyle}
}

func intNode(value int) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(value)}
}

func addPair(mapping *yaml.Node, key string, value *yaml.Node) {
	mapping.Content = append(mapping.Content, plainNode(key), value)
}
