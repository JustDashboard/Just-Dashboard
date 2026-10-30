package deploy

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/blueprint"
)

// TemplateFromPlan is "Save as template": the definition a deployment's
// frozen plan — the one its live release ran — would have been deployed
// from, in blueprint's own schema, so the next deployment of it goes through
// the catalogue's validation, planning, secrets and runtime exactly as a
// built-in does.
//
// Nothing this deployment holds that is its own survives. A secret variable
// becomes a secret input, asked for on the next deployment's Variables
// screen; its value is never read (plain holds plain literals only, and a
// name the plan calls secret is never looked up in it). A concrete domain
// becomes the domain input, and a value that named it is set from that input.
// A fixed host port becomes a number input defaulting to it. A managed
// volume's name, which carries this deployment's, is the template's own.
func TemplateFromPlan(plan *StoredExecutionPlan, plain map[string]string, meta blueprint.TemplateMeta) (*blueprint.Blueprint, error) {
	if plan == nil {
		return nil, fmt.Errorf("%w: the deployment has no frozen plan", ErrInvalidTemplate)
	}
	_, checks, domains, err := decodeStoredPlanInputs(plan)
	if err != nil {
		return nil, err
	}
	domain := ""
	for _, candidate := range domains {
		if candidate.RedirectTo == "" {
			domain = strings.ToLower(strings.TrimSpace(candidate.Hostname))
			break
		}
	}
	source := plan.SourceConfig
	switch {
	case source.Mode == SourceModeBlueprint:
		return templateFromDefinition(source, meta)
	case source.Mode == SourceModeImageReference:
		return templateFromImage(plan, plain, meta, checks, domain)
	case source.Mode == SourceModeComposePaste || source.Mode == SourceModeComposeUpload:
		return templateFromCompose(plan, plain, meta, domain)
	case source.Kind == SourceGit || source.Kind == SourceLocal || source.Mode == SourceModeComposeGit || source.Mode == SourceModeComposeLocal:
		return nil, fmt.Errorf("%w: this deployment builds from its source on this server, and a template runs published images; push the image to a registry and deploy that", ErrInvalidTemplate)
	default:
		return nil, fmt.Errorf("%w: a deployment from %s cannot be saved as a template", ErrInvalidTemplate, source.Mode)
	}
}

// templateFromDefinition is a deployment of a template saved as a template of
// its own: the definition it rendered from, under a new id, with the
// answers this deployment gave as the defaults the next one is offered —
// except the domain, which is this deployment's, and anything secret, which
// a blueprint input never holds.
func templateFromDefinition(source DraftSourceConfig, meta blueprint.TemplateMeta) (*blueprint.Blueprint, error) {
	definition, err := source.definition()
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(definition)
	if err != nil {
		return nil, err
	}
	var copied blueprint.Blueprint
	if err := json.Unmarshal(encoded, &copied); err != nil {
		return nil, err
	}
	copied.ID, copied.Retired = meta.ID, ""
	if strings.TrimSpace(meta.Name) != "" {
		copied.Name = strings.TrimSpace(meta.Name)
	}
	if strings.TrimSpace(meta.Description) != "" {
		copied.Description = strings.TrimSpace(meta.Description)
	}
	if meta.Maintainer != "" {
		copied.Provenance.Maintainer = meta.Maintainer
	}
	if meta.ReviewedAt != "" {
		copied.Provenance.ReviewedAt = meta.ReviewedAt
	}
	for index := range copied.Inputs {
		input := &copied.Inputs[index]
		value, answered := source.BlueprintInputs[input.Name]
		if !answered || value == "" {
			continue
		}
		switch input.Kind {
		case blueprint.InputDomain, blueprint.InputSecret, blueprint.InputAccept:
			continue
		}
		if secretShapedKey(input.Name) || secretShapedKey(input.Variable) {
			continue
		}
		input.Default = value
	}
	return &copied, nil
}

// templateVariables is what a deployment's frozen variables declare in a
// template: a secret input for each secret, an input defaulting to each plain
// literal, and a startup operation for a plain value that names the domain.
// The frozen snapshot carries names and digests only; plain is the one place
// a value comes from, and only a plain literal is ever in it.
func templateVariables(plan *StoredExecutionPlan, plain map[string]string, domain, domainInput string) ([]blueprint.Input, []blueprint.Operation) {
	inputs, startup := []blueprint.Input{}, []blueprint.Operation{}
	names := map[string]bool{domainInput: true}
	inputName := func(variable string) string {
		base := strings.Trim(strings.ReplaceAll(strings.ToLower(variable), "_", "-"), "-")
		for strings.Contains(base, "--") {
			base = strings.ReplaceAll(base, "--", "-")
		}
		if base == "" || !(base[0] >= 'a' && base[0] <= 'z' || base[0] >= '0' && base[0] <= '9') {
			base = "value-" + strings.TrimLeft(base, "-")
		}
		if len(base) > 56 {
			base = strings.TrimRight(base[:56], "-")
		}
		name := base
		for suffix := 2; names[name]; suffix++ {
			name = fmt.Sprintf("%s-%d", base, suffix)
		}
		names[name] = true
		return name
	}
	for _, variable := range plan.Variables {
		if !strings.Contains(","+variable.Scopes+",", ",runtime,") {
			continue
		}
		value, literal := plain[variable.Name]
		switch {
		case variable.Sensitivity != "plain" || secretShapedKey(variable.Name):
			inputs = append(inputs, blueprint.Input{
				Name: inputName(variable.Name), Kind: blueprint.InputSecret, Label: variable.Name,
				Variable: variable.Name, Required: true,
				Description: "Secret: asked for on the Variables screen. The deployment this template was saved from keeps its own value.",
			})
		case !literal || strings.ContainsAny(value, "\r\n\x00") || len(value) > 4096:
			// A reference names something of this server's, and a value on
			// several lines is not an input's default: the next deployment
			// types one, or sets the variable on its Variables screen.
			inputs = append(inputs, blueprint.Input{
				Name: inputName(variable.Name), Kind: blueprint.InputText, Label: variable.Name,
				Variable: variable.Name, Advanced: true,
				Description: "The deployment this template was saved from did not keep a plain value here; type one, or set it on the Variables screen.",
			})
		case domain != "" && strings.Contains(strings.ToLower(value), domain):
			startup = append(startup, blueprint.Operation{
				Kind: blueprint.OperationSetVariable, Name: variable.Name,
				Value: replaceFold(value, domain, "{{input."+domainInput+"}}"),
			})
		default:
			inputs = append(inputs, blueprint.Input{
				Name: inputName(variable.Name), Kind: blueprint.InputText, Label: variable.Name,
				Variable: variable.Name, Default: value, Advanced: value != "",
			})
		}
	}
	return inputs, startup
}

// replaceFold replaces every occurrence of a lower-case hostname in a value,
// however the value capitalised it.
func replaceFold(value, hostname, replacement string) string {
	lower := strings.ToLower(value)
	var output strings.Builder
	for {
		index := strings.Index(lower, hostname)
		if index < 0 {
			output.WriteString(value)
			return output.String()
		}
		output.WriteString(value[:index])
		output.WriteString(replacement)
		value, lower = value[index+len(hostname):], lower[index+len(hostname):]
	}
}

func templateFromImage(
	plan *StoredExecutionPlan,
	plain map[string]string,
	meta blueprint.TemplateMeta,
	checks []PlannedCheck,
	domain string,
) (*blueprint.Blueprint, error) {
	runtime := plan.Runtime
	if runtime.Privileged || runtime.HostNetwork || len(runtime.Capabilities) > 0 || len(runtime.Devices) > 0 {
		return nil, fmt.Errorf("%w: this deployment runs privileged, on the host's network or with capabilities or devices, which only a reviewed built-in may ask for", ErrInvalidTemplate)
	}
	image := strings.TrimSpace(plan.SourceConfig.Image)
	if image == "" {
		image = strings.TrimSpace(runtime.Image)
	}
	// The registry's own name for the default registry: what the catalogue
	// writes, and what the next server resolves the same way.
	image = strings.TrimPrefix(strings.TrimPrefix(image, "docker.io/"), "library/")
	inputs, startup := templateVariables(plan, plain, domain, "domain")
	// A routed deployment answers at a domain, and so does the next one; a
	// published or private one has none to ask for unless a value named it.
	if (runtime.InternalPort > 0 && runtime.HostPort == 0) || domain != "" {
		inputs = append([]blueprint.Input{{
			Name: "domain", Kind: blueprint.InputDomain, Label: "Public domain",
			Description: "The address the deployment answers at.", Required: len(startup) > 0,
		}}, inputs...)
	}
	spec := blueprint.ImageTemplate{
		TemplateMeta: meta, Image: image, Command: runtime.Command,
		Port: runtime.InternalPort, Protocol: runtime.Protocol,
		Direct: runtime.HostPort > 0, HostPort: runtime.HostPort,
		MemoryMB: int(runtime.MemoryMB), CPUs: runtime.CPUs,
		Inputs: inputs, Startup: startup,
	}
	if spec.Protocol == "tcp+udp" {
		return nil, fmt.Errorf("%w: this deployment publishes one port over TCP and UDP, which a template added here cannot yet say", ErrInvalidTemplate)
	}
	for _, published := range runtime.Ports {
		spec.Published = append(spec.Published, blueprint.PublishedSpec{
			Container: published.ContainerPort, Host: published.HostPort, Protocol: published.effectiveProtocol(),
		})
	}
	used := map[string]bool{}
	for _, mount := range runtime.Mounts {
		if mount.Ownership != OwnershipManaged || strings.HasPrefix(mount.Source, "/") {
			return nil, fmt.Errorf("%w: this deployment mounts %s from the host, and a template keeps what it writes in volumes of its own", ErrInvalidTemplate, mount.Source)
		}
		name := templateVolumeName(mount.Target, used)
		spec.Volumes = append(spec.Volumes, blueprint.Volume{
			Name: name, Target: mount.Target, Purpose: "What the deployment kept at " + mount.Target,
			Data: !mount.ReadOnly, Backup: !mount.ReadOnly, ReadOnly: mount.ReadOnly,
		})
	}
	// The names FromImage gives the ports, which a check names its port by.
	ports := map[int]string{}
	if spec.Port > 0 {
		ports[spec.Port] = "http"
		if spec.Direct {
			ports[spec.Port] = "main"
		}
	}
	for _, published := range spec.Published {
		name := fmt.Sprintf("port-%d", published.Container)
		if published.Protocol != "" && published.Protocol != "tcp" {
			name += "-" + published.Protocol
		}
		ports[published.Container] = name
	}
	for _, check := range checks {
		if converted, ok := templateCheck(check, ports); ok {
			spec.Checks = append(spec.Checks, converted)
		}
	}
	if !hasRequiredReadiness(spec.Checks) {
		spec.Checks = nil
	}
	return blueprint.FromImage(spec)
}

// templateVolumeName names a volume after where it is mounted: the managed
// name carries this deployment's, and the template's volumes are its own.
func templateVolumeName(target string, used map[string]bool) string {
	base := strings.Trim(strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return '-'
	}, path.Base(target)), "-")
	for strings.Contains(base, "--") {
		base = strings.ReplaceAll(base, "--", "-")
	}
	if base == "" || base == "." {
		base = "data"
	}
	if len(base) > 56 {
		base = strings.TrimRight(base[:56], "-")
	}
	name := base
	for suffix := 2; used[name]; suffix++ {
		name = fmt.Sprintf("%s-%d", base, suffix)
	}
	used[name] = true
	return name
}

// templateCheck is one of the plan's checks in the catalogue's vocabulary.
// The kinds a definition cannot declare — the public route, DNS, TLS, a
// backup's freshness — belong to the deployment rather than to what it runs,
// and the next deployment gets its own.
func templateCheck(check PlannedCheck, ports map[int]string) (blueprint.Check, bool) {
	kind := blueprint.CheckKind(check.Kind)
	switch kind {
	case blueprint.CheckHTTP, blueprint.CheckTCP, blueprint.CheckDocker, blueprint.CheckCommand, blueprint.CheckUDPProbe:
	default:
		return blueprint.Check{}, false
	}
	var config struct {
		Path            string   `json:"path"`
		Port            int      `json:"port"`
		Command         []string `json:"command"`
		Payload         string   `json:"payload"`
		Expect          string   `json:"expect"`
		Attempts        int      `json:"attempts"`
		IntervalSeconds int      `json:"intervalSeconds"`
		TimeoutSeconds  int      `json:"timeoutSeconds"`
	}
	if len(check.Config) > 0 && json.Unmarshal(check.Config, &config) != nil {
		return blueprint.Check{}, false
	}
	converted := blueprint.Check{
		Name: check.Name, Kind: kind, Phase: check.Phase, Required: check.Required,
		Path: config.Path, Command: config.Command, Payload: config.Payload, Expect: config.Expect,
	}
	if config.Port > 0 {
		name, declared := ports[config.Port]
		if !declared {
			return blueprint.Check{}, false
		}
		converted.Port = name
	}
	// The runner's attempts and pauses, read back as the startup budget a
	// definition declares (blueprintChecks writes them the other way).
	if budget := config.Attempts * max(config.IntervalSeconds, 1); budget > 0 {
		converted.TimeoutSeconds = min(budget, 900)
	} else if config.TimeoutSeconds > 0 {
		converted.TimeoutSeconds = config.TimeoutSeconds
	}
	if kind == blueprint.CheckHTTP && !strings.HasPrefix(converted.Path, "/") {
		converted.Path = "/"
	}
	switch converted.Phase {
	case "readiness", "smoke", "public":
	default:
		converted.Phase = "readiness"
	}
	return converted, true
}

func hasRequiredReadiness(checks []blueprint.Check) bool {
	for _, check := range checks {
		if check.Required {
			return true
		}
	}
	return false
}

func templateFromCompose(plan *StoredExecutionPlan, plain map[string]string, meta blueprint.TemplateMeta, domain string) (*blueprint.Blueprint, error) {
	documents := plan.SourceConfig.ComposeFiles
	if len(documents) != 1 {
		return nil, fmt.Errorf("%w: this deployment's stack is %d Compose files, and a template is one", ErrInvalidTemplate, len(documents))
	}
	inputs, startup := templateVariables(plan, plain, domain, "domain")
	inputs = append([]blueprint.Input{{
		Name: "domain", Kind: blueprint.InputDomain, Label: "Public domain",
		Description: "The address the public service answers at.",
	}}, inputs...)
	return blueprint.FromCompose(blueprint.ComposeTemplate{
		TemplateMeta: meta, Document: documents[0].Content, Inputs: inputs, Startup: startup,
		PublicService: plan.Build.PrimaryService, Domain: domain, PruneUnread: true,
	})
}
