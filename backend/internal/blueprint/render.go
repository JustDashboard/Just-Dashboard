package blueprint

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var ErrInvalidInput = errors.New("blueprint input is invalid")

// RenderedVariable is a plan variable. Generated secrets appear here by name
// and sensitivity only; their values are produced by a separate server action
// and never pass through rendering.
type RenderedVariable struct {
	Name        string `json:"name"`
	Value       string `json:"value,omitempty"`
	Sensitivity string `json:"sensitivity"`
	Generated   bool   `json:"generated,omitempty"`
	Required    bool   `json:"required,omitempty"`
	Length      int    `json:"length,omitempty"`
	Label       string `json:"label,omitempty"`
}

type RenderedPort struct {
	Name     string `json:"name"`
	Internal int    `json:"internal"`
	Protocol string `json:"protocol"`
	Exposure string `json:"exposure"`
	Purpose  string `json:"purpose"`
	Primary  bool   `json:"primary,omitempty"`
}

type RenderedVolume struct {
	Name     string `json:"name"`
	Target   string `json:"target"`
	Purpose  string `json:"purpose"`
	Data     bool   `json:"data"`
	Backup   bool   `json:"backup"`
	ReadOnly bool   `json:"readOnly,omitempty"`
}

type RenderedCheck struct {
	Name           string    `json:"name"`
	Kind           CheckKind `json:"kind"`
	Phase          string    `json:"phase"`
	Required       bool      `json:"required"`
	Path           string    `json:"path,omitempty"`
	Port           int       `json:"port,omitempty"`
	Command        []string  `json:"command,omitempty"`
	TimeoutSeconds int       `json:"timeoutSeconds,omitempty"`
}

// Plan is the secret-free normalized result of rendering. Two renders of the
// same blueprint version with the same inputs produce byte-identical Plans and
// therefore the same digest.
type Plan struct {
	BlueprintID      string             `json:"blueprintId"`
	BlueprintVersion string             `json:"blueprintVersion"`
	Profile          Profile            `json:"profile"`
	Image            string             `json:"image"`
	PullPolicy       string             `json:"pullPolicy"`
	Command          []string           `json:"command,omitempty"`
	Variables        []RenderedVariable `json:"variables"`
	Ports            []RenderedPort     `json:"ports"`
	Volumes          []RenderedVolume   `json:"volumes"`
	Checks           []RenderedCheck    `json:"checks"`
	MemoryMB         int                `json:"memoryMb"`
	CPUs             float64            `json:"cpus,omitempty"`
	StopSignal       string             `json:"stopSignal,omitempty"`
	StopCommands     []string           `json:"stopCommands,omitempty"`
	Domains          []string           `json:"domains,omitempty"`
	Acceptances      []Acceptance       `json:"acceptances,omitempty"`
	Artifacts        []RenderedArtifact `json:"artifacts,omitempty"`
	Files            []ConfigFile       `json:"files,omitempty"`
	Automation       []Automation       `json:"automation,omitempty"`
	Security         Security           `json:"security"`
	Digest           string             `json:"digest"`
}

// Acceptance records that the operator agreed to a specific linked document.
// The timestamp and actor are added by the caller when the plan is committed.
type Acceptance struct {
	Input string `json:"input"`
	Label string `json:"label"`
	URL   string `json:"url"`
}

// RenderedArtifact is an install-time download with a fixed authoritative host
// and either a checksum or a declared version source.
type RenderedArtifact struct {
	URL           string `json:"url"`
	Checksum      string `json:"checksum,omitempty"`
	VersionSource string `json:"versionSource,omitempty"`
	MaxBytes      int64  `json:"maxBytes"`
}

// Render turns a blueprint version plus operator inputs into a normalized plan.
// It is pure: no clock, no randomness, no network, no filesystem.
func Render(blueprint *Blueprint, inputs map[string]string) (*Plan, error) {
	if blueprint == nil {
		return nil, fmt.Errorf("%w: no blueprint", ErrInvalidInput)
	}
	values, err := resolveInputs(blueprint, inputs)
	if err != nil {
		return nil, err
	}
	plan := &Plan{
		BlueprintID: blueprint.ID, BlueprintVersion: blueprint.Version, Profile: blueprint.Profile,
		Image: blueprint.Image.Reference, PullPolicy: blueprint.Image.PullPolicy,
		Command:   append([]string(nil), blueprint.Image.Command...),
		Variables: []RenderedVariable{}, Ports: []RenderedPort{}, Volumes: []RenderedVolume{},
		Checks: []RenderedCheck{}, MemoryMB: blueprint.Resources.MemoryMB, CPUs: blueprint.Resources.CPUs,
		Security: blueprint.Security,
	}
	if plan.PullPolicy == "" {
		plan.PullPolicy = "missing"
	}

	variables := map[string]RenderedVariable{}
	for _, input := range blueprint.Inputs {
		value := values[input.Name]
		if choice := selectedChoice(input, value); choice != nil && choice.Image != "" {
			plan.Image = choice.Image
		}
		if input.Kind == InputAccept {
			if value == "true" {
				plan.Acceptances = append(plan.Acceptances, Acceptance{
					Input: input.Name, Label: input.Label, URL: input.AcceptURL,
				})
			}
			continue
		}
		// A kind steers the plan whether or not it also becomes a variable: the
		// memory field is the container limit first and a JVM flag second.
		switch {
		case input.Kind == InputMemory:
			if memory, convErr := strconv.Atoi(value); convErr == nil && memory > 0 {
				plan.MemoryMB = memory
			}
		case input.Kind == InputDomain && value != "":
			plan.Domains = append(plan.Domains, strings.ToLower(value))
		}
		if input.Variable == "" {
			continue
		}
		if input.Kind == InputSecret {
			variables[input.Variable] = RenderedVariable{
				Name: input.Variable, Sensitivity: "secret", Required: input.Required, Label: input.Label,
			}
			continue
		}
		variables[input.Variable] = RenderedVariable{
			Name: input.Variable, Value: value, Sensitivity: "plain", Label: input.Label,
		}
	}
	for _, secret := range blueprint.Secrets {
		variables[secret.Variable] = RenderedVariable{
			Name: secret.Variable, Sensitivity: "secret", Generated: true,
			Length: secret.Length, Label: secret.Label,
		}
	}

	for _, operation := range blueprint.Operations.Startup {
		switch operation.Kind {
		case OperationSetVariable:
			expanded, expandErr := expand(operation.Value, values)
			if expandErr != nil {
				return nil, expandErr
			}
			existing, found := variables[operation.Name]
			if found && existing.Sensitivity == "secret" {
				return nil, fmt.Errorf("%w: startup overwrites secret variable %s", ErrInvalidInput, operation.Name)
			}
			variables[operation.Name] = RenderedVariable{
				Name: operation.Name, Value: expanded, Sensitivity: "plain",
			}
		case OperationFetchArtifact:
			url, expandErr := expand(operation.URL, values)
			if expandErr != nil {
				return nil, expandErr
			}
			plan.Artifacts = append(plan.Artifacts, RenderedArtifact{
				URL: url, Checksum: operation.Checksum, VersionSource: operation.Value, MaxBytes: operation.MaxBytes,
			})
		}
	}
	for _, operation := range blueprint.Operations.Stop {
		switch operation.Kind {
		case OperationStopSignal:
			plan.StopSignal = operation.Signal
		case OperationConsole:
			plan.StopCommands = append(plan.StopCommands, operation.Value)
		}
	}

	names := make([]string, 0, len(variables))
	for name := range variables {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		plan.Variables = append(plan.Variables, variables[name])
	}

	portsByName := map[string]int{}
	for _, port := range blueprint.Ports {
		internal := port.Internal
		if override, found := values["port-"+port.Name]; found && override != "" {
			parsed, convErr := strconv.Atoi(override)
			if convErr != nil || parsed < 1 || parsed > 65535 {
				return nil, fmt.Errorf("%w: port %s must be 1-65535", ErrInvalidInput, port.Name)
			}
			internal = parsed
		}
		portsByName[port.Name] = internal
		plan.Ports = append(plan.Ports, RenderedPort{
			Name: port.Name, Internal: internal, Protocol: strings.ToLower(port.Protocol),
			Exposure: port.Exposure, Purpose: port.Purpose, Primary: port.Primary,
		})
	}
	for _, volume := range blueprint.Volumes {
		plan.Volumes = append(plan.Volumes, RenderedVolume{
			Name: volume.Name, Target: volume.Target, Purpose: volume.Purpose,
			Data: volume.Data, Backup: volume.Backup, ReadOnly: volume.ReadOnly,
		})
	}
	for _, check := range blueprint.Checks {
		rendered := RenderedCheck{
			Name: check.Name, Kind: check.Kind, Phase: check.Phase, Required: check.Required,
			Path: check.Path, Command: append([]string(nil), check.Command...),
			TimeoutSeconds: check.TimeoutSeconds,
		}
		if check.Port != "" {
			rendered.Port = portsByName[check.Port]
		}
		plan.Checks = append(plan.Checks, rendered)
	}
	plan.Files = append([]ConfigFile(nil), blueprint.Files...)
	plan.Automation = append([]Automation(nil), blueprint.Automation...)

	digest, err := planDigest(plan)
	if err != nil {
		return nil, err
	}
	plan.Digest = digest
	return plan, nil
}

// planDigest hashes the plan with the digest field cleared, so the digest is a
// function of the plan's content rather than of itself.
func planDigest(plan *Plan) (string, error) {
	bare := *plan
	bare.Digest = ""
	encoded, err := json.Marshal(bare)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func selectedChoice(input Input, value string) *Choice {
	if input.Kind != InputChoice {
		return nil
	}
	for index := range input.Choices {
		if input.Choices[index].Value == value {
			return &input.Choices[index]
		}
	}
	return nil
}

// resolveInputs applies defaults, rejects undeclared inputs and validates every
// supplied value against the blueprint's own declaration.
func resolveInputs(blueprint *Blueprint, supplied map[string]string) (map[string]string, error) {
	declared := map[string]Input{}
	for _, input := range blueprint.Inputs {
		declared[input.Name] = input
	}
	portNames := map[string]bool{}
	for _, port := range blueprint.Ports {
		portNames[port.Name] = true
	}
	for name := range supplied {
		if _, found := declared[name]; found {
			continue
		}
		if override, isPort := strings.CutPrefix(name, "port-"); isPort && portNames[override] {
			continue
		}
		return nil, fmt.Errorf("%w: %q is not an input of this blueprint", ErrInvalidInput, name)
	}
	values := map[string]string{}
	for _, input := range blueprint.Inputs {
		value, provided := supplied[input.Name]
		if input.Kind == InputSecret {
			if value != "" {
				return nil, fmt.Errorf("%w: %q must be supplied through encrypted variables, not blueprint inputs", ErrInvalidInput, input.Name)
			}
			values[input.Name] = ""
			continue
		}
		if !provided || value == "" {
			value = input.Default
		}
		if input.Kind == InputDomain {
			value = strings.ToLower(value)
		}
		if input.Required && strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("%w: %q is required", ErrInvalidInput, input.Name)
		}
		if value != "" {
			if err := validateValue(input, value); err != nil {
				return nil, err
			}
		}
		values[input.Name] = value
	}
	for name, value := range supplied {
		if strings.HasPrefix(name, "port-") {
			values[name] = value
		}
	}
	return values, nil
}

func validateValue(input Input, value string) error {
	if strings.ContainsAny(value, "\x00\r\n") {
		return fmt.Errorf("%w: %q contains a control character", ErrInvalidInput, input.Name)
	}
	if len(value) > 4096 {
		return fmt.Errorf("%w: %q is longer than 4096 characters", ErrInvalidInput, input.Name)
	}
	switch input.Kind {
	case InputText:
		if input.Pattern != "" && !regexp.MustCompile(input.Pattern).MatchString(value) {
			return fmt.Errorf("%w: %q does not match the required format", ErrInvalidInput, input.Name)
		}
	case InputDomain:
		if len(value) > 253 || !domainRE.MatchString(value) {
			return fmt.Errorf("%w: %q is not a hostname", ErrInvalidInput, input.Name)
		}
	case InputNumber, InputMemory:
		number, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("%w: %q must be a whole number", ErrInvalidInput, input.Name)
		}
		if input.Minimum != 0 && number < input.Minimum {
			return fmt.Errorf("%w: %q must be at least %d", ErrInvalidInput, input.Name, input.Minimum)
		}
		if input.Maximum != 0 && number > input.Maximum {
			return fmt.Errorf("%w: %q must be at most %d", ErrInvalidInput, input.Name, input.Maximum)
		}
	case InputBoolean, InputAccept:
		if value != "true" && value != "false" {
			return fmt.Errorf("%w: %q must be true or false", ErrInvalidInput, input.Name)
		}
		if input.Kind == InputAccept && input.Required && value != "true" {
			return fmt.Errorf("%w: %q must be accepted before this can be deployed", ErrInvalidInput, input.Name)
		}
	case InputChoice:
		if selectedChoice(input, value) == nil {
			return fmt.Errorf("%w: %q is not an offered option for %q", ErrInvalidInput, value, input.Name)
		}
	}
	return nil
}

var (
	domainRE      = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)
	placeholderRE = regexp.MustCompile(`\{\{input\.([a-z0-9-]+)\}\}`)
)

// expand substitutes {{input.name}} placeholders. Only declared inputs resolve;
// there is no environment, no shell and no arbitrary expression.
func expand(template string, values map[string]string) (string, error) {
	var failure error
	expanded := placeholderRE.ReplaceAllStringFunc(template, func(match string) string {
		name := placeholderRE.FindStringSubmatch(match)[1]
		value, found := values[name]
		if !found {
			failure = fmt.Errorf("%w: template references undeclared input %q", ErrInvalidInput, name)
			return ""
		}
		return value
	})
	if failure != nil {
		return "", failure
	}
	if strings.Contains(expanded, "{{") {
		return "", fmt.Errorf("%w: template contains an unsupported placeholder", ErrInvalidInput)
	}
	return expanded, nil
}
