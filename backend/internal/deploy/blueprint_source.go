package deploy

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/blueprint"
)

// BlueprintPlan is a preview of a reviewed definition. Blueprint deployment is
// unavailable until the runtime and lifecycle integrations are complete.
type BlueprintPlan struct {
	Detection     DetectionResult        `json:"detection"`
	Configuration PlanConfiguration      `json:"configuration"`
	Rendered      *blueprint.Plan        `json:"rendered"`
	Summary       blueprint.Summary      `json:"summary"`
	Inputs        []blueprint.Input      `json:"inputs"`
	Automation    []blueprint.Automation `json:"automation"`
	Files         []blueprint.ConfigFile `json:"files"`
	// ComposeFiles is a Compose template's rendered document, the one its
	// source stores and every release of it runs.
	ComposeFiles []ComposeDocument `json:"composeFiles,omitempty"`
}

// RenderBlueprintPlan resolves a blueprint version, renders it against the
// operator's inputs and maps the result onto the normalized plan. It is pure
// apart from the catalogue read, so preflight can call it without side effects.
func RenderBlueprintPlan(source DraftSourceConfig, name string) (*BlueprintPlan, error) {
	definition, err := blueprint.GetVersion(source.BlueprintID, source.BlueprintVersion)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSource, err)
	}
	// Rendering does not depend on the kind, so a caller that names none —
	// a preview — gets the definition's own; one that names the wrong one is
	// told which it is.
	if source.Kind != "" {
		if err := blueprintSourceKindMatches(source, definition); err != nil {
			return nil, err
		}
	}
	rendered, err := blueprint.Render(definition, source.BlueprintInputs)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPlan, err)
	}
	if definition.IsCompose() {
		return blueprintComposePlan(definition, rendered)
	}
	profile := blueprintWorkloadProfile(rendered.Profile)
	candidate := DetectedCandidate{
		ID: definition.ID, Name: definition.Name, Profile: profile,
		BuildMethod: BuildImage, Confidence: ConfidenceHigh,
		Evidence: []DetectionEvidence{{
			Path:   definition.ID + "@" + definition.Version,
			Reason: "reviewed blueprint shipped with this dashboard",
		}},
		NeedsDecision: []string{},
	}
	for _, port := range rendered.Ports {
		if port.Primary {
			candidate.Port = port.Internal
		}
	}
	for _, input := range definition.Inputs {
		if input.Kind == blueprint.InputSecret {
			candidate.Variables = append(candidate.Variables, DetectedVariable{
				Name: input.Variable, Sources: []string{definition.ID + " blueprint"},
			})
		}
	}
	if definition.ID == "mongo-express" {
		candidate.Databases = []DetectedDatabase{{
			Engine: "mongodb", Variable: "ME_CONFIG_MONGODB_URL", Evidence: "Mongo Express requires a MongoDB connection",
		}}
	}
	// The identity names three immutable things: the image reference the
	// release pulls (its digest is resolved by detection), the reviewed
	// definition, and the digest of this exact render. Digest stays empty here
	// because only a registry lookup may fill it.
	detection := DetectionResult{
		Source: SourceIdentity{
			Kind: SourceBlueprint, Repository: rendered.Image, Ref: definition.ID + "@" + definition.Version,
			Revision: rendered.Digest,
		},
		Candidates: []DetectedCandidate{candidate}, SelectedID: candidate.ID,
	}
	configuration, err := blueprintConfiguration(definition, rendered, name)
	if err != nil {
		return nil, err
	}
	return &BlueprintPlan{
		Detection: detection, Configuration: *configuration, Rendered: rendered,
		Summary: blueprint.Summarize(definition), Inputs: definition.Inputs,
		Automation: rendered.Automation, Files: rendered.Files,
	}, nil
}

// blueprintSourceKindMatches keeps the source kind honest about what a
// definition is. A multi-service template runs through the Compose release
// path and is a Compose source; a single reviewed image is a blueprint source.
// Accepting either for both would let a stack be planned as one container.
func blueprintSourceKindMatches(source DraftSourceConfig, definition *blueprint.Blueprint) error {
	if expected := BlueprintSourceKind(definition); source.Kind != expected {
		if expected == SourceCompose {
			return fmt.Errorf("%w: %s is a multi-service template and deploys as a Compose source", ErrInvalidSource, definition.ID)
		}
		return fmt.Errorf("%w: %s is a single-image template and deploys as a blueprint source", ErrInvalidSource, definition.ID)
	}
	return nil
}

// BlueprintSourceKind is the source kind a definition deploys as.
func BlueprintSourceKind(definition *blueprint.Blueprint) SourceKind {
	if definition.IsCompose() {
		return SourceCompose
	}
	return SourceBlueprint
}

// blueprintComposePlan is RenderBlueprintPlan for a multi-service template.
// The stack is planned exactly as a pasted Compose file is — the same
// structural analysis, the same release path, the same Compose evidence — with
// the reviewed definition supplying what an operator would otherwise have to:
// the public service, its port and checks, the generated secrets every service
// shares, and the domain.
func blueprintComposePlan(definition *blueprint.Blueprint, rendered *blueprint.Plan) (*BlueprintPlan, error) {
	documents := []ComposeDocument{{Path: blueprint.ComposeFile, Content: rendered.Compose}}
	analysis, err := analyzeComposeDocuments(documents)
	if err != nil {
		// The definition passed every catalogue rule and still renders a file
		// the planner refuses: that is the catalogue's fault, never the
		// operator's, and it must not reach a deployment.
		return nil, fmt.Errorf("%w: %s renders a Compose file the planner refuses: %v", ErrInvalidPlan, definition.ID, err)
	}
	public := definition.PublicService()
	if public == nil {
		return nil, fmt.Errorf("%w: %s has no public service", ErrInvalidPlan, definition.ID)
	}
	analysis.PrimaryService = public.Name
	port := 0
	for _, candidate := range rendered.Ports {
		if candidate.Exposure == "proxy" {
			port = candidate.Internal
		}
	}
	services := make([]string, 0, len(analysis.Services))
	for _, service := range analysis.Services {
		services = append(services, service.Name)
	}
	candidate := newDetectedCandidate("", BuildCompose, DetectedCandidate{
		Name: definition.Name, Profile: ProfileCompose, Confidence: ConfidenceHigh, Port: port,
		Evidence: []DetectionEvidence{{
			Path:   definition.ID + "@" + definition.Version,
			Reason: fmt.Sprintf("reviewed multi-service template shipped with this dashboard: %s", strings.Join(services, ", ")),
		}},
	})
	// The identity names the reviewed definition (Template), the digest of
	// this exact render (Revision) and the document the release runs (Digest),
	// which is what makes a template's release as immutable as a pasted one.
	detection := DetectionResult{
		Source: SourceIdentity{
			Kind: SourceCompose, Digest: analysis.Digest, ComposeFiles: analysis.Files, Services: services,
			Revision: rendered.Digest, Template: definition.ID + "@" + definition.Version,
		},
		Candidates: []DetectedCandidate{candidate}, SelectedID: candidate.ID, Compose: &analysis,
	}
	configuration := &PlanConfiguration{
		Build: BuildPlanConfig{
			Method: BuildCompose, PrimaryService: public.Name,
			Secrets: []BuildSecretConfig{}, ReleaseTasks: []ReleaseTaskConfig{},
		},
		// Compose keeps the limits the document declares per service, and a
		// stack of stateful services is replaced, never run twice at once.
		Runtime: RuntimePlanConfig{
			InternalPort: port, Protocol: "tcp", BindAddress: "127.0.0.1", Strategy: StrategyStopFirst,
			Mounts: []RuntimeMount{}, Capabilities: []string{}, Devices: []string{},
		},
		Variables: blueprintVariables(definition, rendered), Dependencies: []PlannedDependency{},
		Checks: blueprintChecks(rendered), Domains: blueprintDomains(rendered),
	}
	return &BlueprintPlan{
		Detection: detection, Configuration: *configuration, Rendered: rendered,
		Summary: blueprint.Summarize(definition), Inputs: definition.Inputs,
		Automation: rendered.Automation, Files: rendered.Files, ComposeFiles: documents,
	}, nil
}

// WithTemplateDocuments renders a Compose template source's document from
// the reviewed definition it names and stores it in the source, replacing
// whatever the caller sent. It is how a template source is saved: the stored
// document is the server's render of a version this dashboard ships, never a
// file a client chose to label with a template's name, and from then on it is
// the release — a later dashboard that ships a newer version of the template
// does not change what this deployment runs, and a redeploy does not need
// the old version to still be shipped. Any other source is returned as it is.
func (c DraftSourceConfig) WithTemplateDocuments() (DraftSourceConfig, error) {
	if c.Mode != SourceModeBlueprint || c.Kind != SourceCompose {
		return c, nil
	}
	plan, err := RenderBlueprintPlan(c, "")
	if err != nil {
		return c, err
	}
	c.ComposeFiles = append([]ComposeDocument(nil), plan.ComposeFiles...)
	return c, nil
}

func blueprintWorkloadProfile(profile blueprint.Profile) WorkloadProfile {
	switch profile {
	case blueprint.ProfileWeb, blueprint.ProfileTool:
		return ProfileWeb
	case blueprint.ProfileGame:
		return ProfileGame
	case blueprint.ProfileDatabase, blueprint.ProfileWorker:
		return ProfileService
	default:
		return ProfileService
	}
}

func blueprintConfiguration(
	definition *blueprint.Blueprint,
	rendered *blueprint.Plan,
	name string,
) (*PlanConfiguration, error) {
	configuration := &PlanConfiguration{
		// A blueprint names an immutable image; there is nothing to build. The
		// image itself lives on the runtime plan, as it does for any image source.
		Build: BuildPlanConfig{
			Method: BuildImage, Secrets: []BuildSecretConfig{}, ReleaseTasks: []ReleaseTaskConfig{},
		},
		Runtime: RuntimePlanConfig{
			Image: rendered.Image, Command: append([]string{}, rendered.Command...), Strategy: StrategyBlueGreen,
			BindAddress: "127.0.0.1", Capabilities: append([]string(nil), rendered.Security.Capabilities...),
			Devices:    append([]string(nil), rendered.Security.Devices...),
			Privileged: rendered.Security.Privileged, HostNetwork: rendered.Security.HostNetwork,
			Mounts: []RuntimeMount{},
		},
		Variables: []PlannedVariable{}, Dependencies: []PlannedDependency{},
		Checks: []PlannedCheck{}, Domains: []PlannedDomain{},
	}
	if configuration.Runtime.Capabilities == nil {
		configuration.Runtime.Capabilities = []string{}
	}
	if configuration.Runtime.Devices == nil {
		configuration.Runtime.Devices = []string{}
	}
	if rendered.StopSignal != "" {
		configuration.Runtime.StopSignal = rendered.StopSignal
	}
	if rendered.MemoryMB >= MinRuntimeMemoryMB && rendered.MemoryMB <= MaxRuntimeMemoryMB {
		configuration.Runtime.MemoryMB = int64(rendered.MemoryMB)
	}
	if rendered.CPUs >= 0.01 && rendered.CPUs <= MaxRuntimeCPUs {
		configuration.Runtime.CPUs = rendered.CPUs
	}

	// A workload holding exclusive local data cannot run two copies at once, so
	// it is honest about stopping first rather than promising a zero-downtime
	// cutover it cannot deliver.
	exclusive := false
	for _, volume := range rendered.Volumes {
		if volume.Data {
			exclusive = true
		}
	}
	direct := false
	for _, port := range rendered.Ports {
		switch port.Exposure {
		case "proxy":
			if port.Primary || configuration.Runtime.InternalPort == 0 {
				configuration.Runtime.InternalPort = port.Internal
				configuration.Runtime.Protocol = port.Protocol
			}
		case "direct":
			direct = true
			if port.Primary || (configuration.Runtime.HostPort == 0 && configuration.Runtime.InternalPort == 0) {
				configuration.Runtime.InternalPort = port.Internal
				configuration.Runtime.Protocol = port.Protocol
				configuration.Runtime.HostPort = port.Internal
				configuration.Runtime.BindAddress = "0.0.0.0"
				continue
			}
			// Gitea's SSH next to its web port, Syncthing's sync protocol next
			// to its GUI: the proxy carries the primary port and this one is
			// published on its own number, as the definition promised.
			configuration.Runtime.Ports = append(configuration.Runtime.Ports, PublishedPort{
				HostPort: port.Internal, ContainerPort: port.Internal, Protocol: port.Protocol,
			})
		case "internal":
			if configuration.Runtime.InternalPort == 0 {
				configuration.Runtime.InternalPort = port.Internal
				configuration.Runtime.Protocol = port.Protocol
			}
		}
	}
	if exclusive || direct {
		configuration.Runtime.Strategy = StrategyStopFirst
	}

	volumePrefix := blueprintVolumePrefix(name, definition.ID)
	for _, volume := range rendered.Volumes {
		if volume.Target == "/var/run/docker.sock" && volume.ReadOnly {
			configuration.Runtime.Mounts = append(configuration.Runtime.Mounts, RuntimeMount{
				Source: "/var/run/docker.sock", Target: volume.Target, ReadOnly: true, Ownership: OwnershipLinked,
			})
			continue
		}
		configuration.Runtime.Mounts = append(configuration.Runtime.Mounts, RuntimeMount{
			Source: volumePrefix + "-" + volume.Name, Target: volume.Target,
			ReadOnly: volume.ReadOnly, Ownership: OwnershipManaged,
		})
		configuration.Dependencies = append(configuration.Dependencies, PlannedDependency{
			Kind: "storage", Ownership: OwnershipManaged, ResourceKind: "docker_volume",
			ResourceID: volumePrefix + "-" + volume.Name,
			Config:     mustJSON(map[string]any{"purpose": volume.Purpose, "data": volume.Data, "backup": volume.Backup}),
		})
	}

	configuration.Variables = blueprintVariables(definition, rendered)
	configuration.Domains = blueprintDomains(rendered)
	configuration.Checks = blueprintChecks(rendered)
	sort.Slice(configuration.Dependencies, func(i, j int) bool {
		return configuration.Dependencies[i].ResourceID < configuration.Dependencies[j].ResourceID
	})
	return configuration, nil
}

// blueprintVariables maps a render's variables onto planned ones. A generated
// secret is declared by shape only; its value is produced by the planning
// store when the deployment is committed and is sealed from then on.
func blueprintVariables(definition *blueprint.Blueprint, rendered *blueprint.Plan) []PlannedVariable {
	variables := []PlannedVariable{}
	for _, variable := range rendered.Variables {
		domainTemplate, domainRequired := blueprintDomainTemplate(definition, variable.Name)
		planned := PlannedVariable{
			Name: variable.Name, Sensitivity: variable.Sensitivity,
			Scopes: []string{"runtime"}, Required: variable.Generated || variable.Required || domainRequired,
			DomainTemplate: domainTemplate,
		}
		switch {
		case variable.Generated:
			planned.Sensitivity = "secret"
			planned.Generate = max(MinGeneratedSecretLength, min(MaxGeneratedSecretLength, variable.Length))
			planned.GenerateFormat = variable.Format
		case variable.Sensitivity != "secret":
			planned.Value = variable.Value
		}
		variables = append(variables, planned)
	}
	return variables
}

func blueprintDomains(rendered *blueprint.Plan) []PlannedDomain {
	domains := []PlannedDomain{}
	for _, domain := range rendered.Domains {
		domains = append(domains, PlannedDomain{Hostname: domain, HTTPS: true, Ownership: OwnershipManaged})
	}
	return domains
}

func blueprintChecks(rendered *blueprint.Plan) []PlannedCheck {
	checks := []PlannedCheck{}
	for _, check := range rendered.Checks {
		planned := PlannedCheck{
			Name: check.Name, Kind: string(check.Kind), Phase: check.Phase, Required: check.Required,
		}
		config := map[string]any{}
		if check.Path != "" {
			config["path"] = check.Path
		}
		if check.Port > 0 && string(check.Kind) != "game_handshake" {
			config["port"] = check.Port
		}
		if len(check.Command) > 0 {
			config["command"] = check.Command
		}
		if check.TimeoutSeconds > 0 {
			// A definition's timeout is a startup budget: how long the image
			// may take before its first honest answer. The runner knows only
			// attempts and pauses, so the budget becomes paced retries — a
			// single long request timeout would ask once and give up, and
			// retries without a pause would spend the whole budget in the
			// first few milliseconds, before the process has even bound its
			// port.
			interval := 2
			if check.TimeoutSeconds > 120 {
				interval = 10
			}
			config["timeoutSeconds"] = min(10, check.TimeoutSeconds)
			config["attempts"] = min(60, (check.TimeoutSeconds+interval-1)/interval)
			config["intervalSeconds"] = interval
		}
		planned.Config = mustJSON(config)
		checks = append(checks, planned)
	}
	return checks
}

// The primary public address can change after detection. Keep the reviewed
// relationship with its default environment values so the form can update
// untouched defaults without overwriting an operator's explicit override.
func blueprintDomainTemplate(definition *blueprint.Blueprint, variable string) (string, bool) {
	for _, input := range definition.Inputs {
		if input.Kind != blueprint.InputDomain {
			continue
		}
		if input.Variable == variable {
			return "{{hostname}}", input.Required
		}
		placeholder := "{{input." + input.Name + "}}"
		for _, operation := range definition.Operations.Startup {
			if operation.Kind != blueprint.OperationSetVariable || operation.Name != variable || !strings.Contains(operation.Value, placeholder) {
				continue
			}
			template := strings.ReplaceAll(operation.Value, placeholder, "{{hostname}}")
			if strings.Contains(template, "{{input.") {
				return "", false
			}
			template = strings.ReplaceAll(template, "https://", "{{scheme}}://")
			template = strings.ReplaceAll(template, "http://", "{{scheme}}://")
			return template, input.Required
		}
		return "", false
	}
	return "", false
}

// blueprintVolumePrefix keeps one deployment's volumes recognisable on a host
// running several copies of the same blueprint.
func blueprintVolumePrefix(name, blueprintID string) string {
	slug := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		case r == '-' || r == '_' || r == ' ':
			return '-'
		default:
			return -1
		}
	}, name)
	slug = strings.Trim(slug, "-")
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	if slug == "" {
		slug = blueprintID
	}
	if len(slug) > 40 {
		slug = strings.Trim(slug[:40], "-")
	}
	hash := sha256.Sum256([]byte(name + "\x00" + blueprintID))
	return fmt.Sprintf("%s-%x", slug, hash[:8])
}

// BlueprintGeneratedSecrets names the variables the server must generate before
// this plan can run. Values are produced by the planning store's own generator,
// never by rendering.
func BlueprintGeneratedSecrets(rendered *blueprint.Plan) []blueprint.RenderedVariable {
	generated := []blueprint.RenderedVariable{}
	for _, variable := range rendered.Variables {
		if variable.Generated {
			generated = append(generated, variable)
		}
	}
	return generated
}

// BlueprintSchedules translates a blueprint's automation presets into the
// scheduler's own closed action vocabulary. Only presets the blueprint marked
// as defaults are returned; the rest are offered in the workspace, because a
// schedule nobody asked for is a surprise that runs at 4 a.m.
func BlueprintSchedules(rendered *blueprint.Plan) []ScheduleWrite {
	if rendered == nil {
		return nil
	}
	writes := []ScheduleWrite{}
	for _, preset := range rendered.Automation {
		if !preset.Default || (rendered.BlueprintID == "minecraft-bedrock" && containsString(preset.Actions, "save")) {
			continue
		}
		steps := make([]ScheduleStep, 0, len(preset.Actions))
		translatable := true
		for _, action := range preset.Actions {
			step, ok := blueprintScheduleStep(action)
			if !ok {
				translatable = false
				break
			}
			steps = append(steps, step)
		}
		// A preset the scheduler cannot express is dropped rather than
		// approximated: a backup that silently became a restart would be worse
		// than no schedule at all.
		if !translatable || len(steps) == 0 {
			continue
		}
		timezone := preset.Timezone
		if timezone == "" {
			timezone = "UTC"
		}
		// A backup step needs a backup job the operator has not linked yet. The
		// schedule is created paused so it appears on the Automations page to
		// be completed, instead of failing every night as an invalid plan.
		enabled := true
		for _, step := range steps {
			if step.Action == "backup" && !strings.Contains(string(step.Config), "jobId") {
				enabled = false
			}
		}
		writes = append(writes, ScheduleWrite{
			Name: preset.Name, Expression: preset.Cron, Timezone: timezone,
			Enabled: enabled, Steps: steps,
		})
	}
	return writes
}

func blueprintScheduleStep(action string) (ScheduleStep, bool) {
	switch action {
	case "backup":
		return ScheduleStep{Action: "backup", Config: json.RawMessage(`{}`), Required: true}, true
	case "restart":
		return ScheduleStep{Action: "restart", Config: json.RawMessage(`{}`), Required: true}, true
	case "update":
		return ScheduleStep{Action: "deploy", Config: json.RawMessage(`{}`), Required: true}, true
	case "save":
		return ScheduleStep{
			Action: "game_command", Required: true,
			Config: json.RawMessage(`{"command":"save-all flush"}`),
		}, true
	case "broadcast":
		return ScheduleStep{
			Action: "game_command", Required: false,
			Config: json.RawMessage(`{"command":"say Scheduled maintenance starts shortly"}`),
		}, true
	default:
		return ScheduleStep{}, false
	}
}

// ValidateForDeployment keeps schema-valid previews readable while refusing
// unsupported new work before a draft or run can create persistent resources.
// A blueprint the catalogue cannot deploy end to end is refused here with the
// catalogue's own reason, so a stale draft or a direct API call gets the same
// answer the picker shows.
//
// A Compose template's release is the document its source stored, so it
// stays deployable after the dashboard stops shipping the version it was
// rendered from; it only has to have been rendered.
func (c DraftSourceConfig) ValidateForDeployment() error {
	if err := c.Validate(); err != nil {
		return err
	}
	if c.Mode != SourceModeBlueprint {
		return nil
	}
	definition, err := blueprint.GetVersion(c.BlueprintID, c.BlueprintVersion)
	if c.Kind == SourceCompose {
		if len(c.ComposeFiles) == 0 {
			return fmt.Errorf("%w: the template's Compose file has not been rendered; inspect the template again", ErrUnsupportedSource)
		}
		if err != nil {
			return nil
		}
		return blueprintSourceKindMatches(c, definition)
	}
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnsupportedSource, err)
	}
	if err := blueprintSourceKindMatches(c, definition); err != nil {
		return err
	}
	if supported, reason := blueprint.DeploymentSupport(definition); !supported {
		return fmt.Errorf("%w: %s", ErrUnsupportedSource, reason)
	}
	return nil
}

func (c DraftSourceConfig) validateBlueprintSecretInputs() error {
	if c.Mode != SourceModeBlueprint {
		return nil
	}
	definition, err := blueprint.GetVersion(c.BlueprintID, c.BlueprintVersion)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSource, err)
	}
	for _, input := range definition.Inputs {
		if input.Kind == blueprint.InputSecret && c.BlueprintInputs[input.Name] != "" {
			return fmt.Errorf("%w: %q must be supplied through encrypted variables, not blueprint inputs", ErrInvalidSource, input.Name)
		}
	}
	return nil
}

// Historical Mongo Express sources held the connection in plain inputs. A
// committed run already has its sealed variable revisions; materialization
// must neither re-read this old value nor copy it into another source marker.
func (c DraftSourceConfig) withoutBlueprintSecretInputs() (DraftSourceConfig, error) {
	if c.Mode != SourceModeBlueprint {
		return c, nil
	}
	definition, err := blueprint.GetVersion(c.BlueprintID, c.BlueprintVersion)
	if err != nil && c.Kind == SourceCompose {
		// A template version no longer shipped still runs from its stored
		// document; its inputs never held a secret, because saving refuses one.
		return c, nil
	}
	if err != nil {
		return c, fmt.Errorf("%w: %v", ErrInvalidSource, err)
	}
	inputs := make(map[string]string, len(c.BlueprintInputs))
	for key, value := range c.BlueprintInputs {
		inputs[key] = value
	}
	for _, input := range definition.Inputs {
		if input.Kind == blueprint.InputSecret {
			delete(inputs, input.Name)
		}
	}
	c.BlueprintInputs = inputs
	return c, nil
}

// Retirement stops new installations without stranding releases that already
// depend on the retained definition for redeploys and recovery.
func (c DraftSourceConfig) validateForNewDeployment() error {
	if err := c.ValidateForDeployment(); err != nil {
		return err
	}
	if c.Mode == SourceModeBlueprint {
		definition, err := blueprint.GetVersion(c.BlueprintID, c.BlueprintVersion)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrUnsupportedSource, err)
		}
		if definition.Retired != "" {
			return fmt.Errorf("%w: %s", ErrUnsupportedSource, definition.Retired)
		}
		if err := c.validateBlueprintSecretInputs(); err != nil {
			return err
		}
		if c.Kind == SourceCompose {
			// A new template deployment runs exactly this dashboard's render.
			rendered, err := c.WithTemplateDocuments()
			if err != nil {
				return err
			}
			if !slices.Equal(rendered.ComposeFiles, c.ComposeFiles) {
				return fmt.Errorf("%w: the Compose file differs from the reviewed template's render", ErrInvalidSource)
			}
		}
	}
	return nil
}
func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
