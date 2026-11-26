package stacks

import (
	"fmt"
	"slices"
	"time"
)

// Manifest is one stack: the presets it is offered as, the services they run,
// the options a person chooses, the credentials it keeps and the steps that
// configure it. It is Go, compiled in and reviewed with the adapters it
// names; nothing outside this repository can add one.
type Manifest struct {
	ID          string
	Version     string
	Title       string
	Description string
	Presets     []Preset
	Services    []ServiceInfo
	Options     []Option
	Credentials []CredentialSpec
	Steps       []Step
}

// Preset is a way to deploy a stack: a reviewed Compose template, and what
// the person is told about it before pressing Deploy.
type Preset struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Template    string   `json:"templateId"`
	Services    []string `json:"services"`
	Recommended bool     `json:"recommended,omitempty"`
	// Limitations are said before the press and again on the stack's page:
	// what this preset cannot do by itself.
	Limitations []string `json:"limitations,omitempty"`
}

// Access is who can open a service.
type Access string

const (
	// AccessPublic: the deployment's own domain routes to it.
	AccessPublic Access = "public"
	// AccessPrivate: published on this server's loopback only, for the
	// person to open through a tunnel or publish through a site of their own.
	AccessPrivate Access = "private"
	// AccessWorker: no web interface.
	AccessWorker Access = "worker"
)

// ServiceInfo is a service as the stack's pages describe it.
type ServiceInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Purpose string `json:"purpose"`
	Port    int    `json:"port,omitempty"`
	Access  Access `json:"access"`
	DocsURL string `json:"docsUrl,omitempty"`
}

// Option is a choice the person makes, held as one of the deployment's own
// variables so it is changed where every other setting is.
type Option struct {
	Name     string `json:"name"`
	Variable string `json:"variable"`
	Default  string `json:"default"`
}

// CredentialSpec is a secret the stack keeps for itself. Internal ones are
// tokens an application handed back for the stack's own use; the others are
// accounts a person signs in with and may reveal.
type CredentialSpec struct {
	Name     string `json:"name"`
	Service  string `json:"service"`
	Label    string `json:"label"`
	Username string `json:"username,omitempty"`
	Internal bool   `json:"internal,omitempty"`
	// Length of a generated value; 0 means the stack never generates it (an
	// application hands it back).
	Length int `json:"-"`
}

// PresetByID returns the preset with this id.
func (m *Manifest) PresetByID(id string) (Preset, bool) {
	for _, preset := range m.Presets {
		if preset.ID == id {
			return preset, true
		}
	}
	return Preset{}, false
}

// PresetForTemplate returns the preset a template id deploys.
func (m *Manifest) PresetForTemplate(template string) (Preset, bool) {
	for _, preset := range m.Presets {
		if preset.Template == template {
			return preset, true
		}
	}
	return Preset{}, false
}

// CredentialByName returns a credential's spec.
func (m *Manifest) CredentialByName(name string) (CredentialSpec, bool) {
	for _, credential := range m.Credentials {
		if credential.Name == name {
			return credential, true
		}
	}
	return CredentialSpec{}, false
}

// CredentialLengths is each generated credential's length by name.
func CredentialLengths(m *Manifest) map[string]int {
	lengths := map[string]int{}
	for _, credential := range m.Credentials {
		if credential.Length > 0 {
			lengths[credential.Name] = credential.Length
		}
	}
	return lengths
}

// Validate is the shape a manifest must have; it runs in a test over every
// registered manifest, so a mistake fails the build rather than a deploy.
func (m *Manifest) Validate() error {
	if m.ID == "" || m.Version == "" || m.Title == "" || len(m.Presets) == 0 {
		return fmt.Errorf("stack %q: id, version, title and a preset are required", m.ID)
	}
	services := map[string]bool{}
	for _, service := range m.Services {
		if service.Name == "" || services[service.Name] {
			return fmt.Errorf("stack %s: service %q is empty or declared twice", m.ID, service.Name)
		}
		services[service.Name] = true
	}
	presets := map[string]bool{}
	for _, preset := range m.Presets {
		if preset.ID == "" || presets[preset.ID] || preset.Template == "" {
			return fmt.Errorf("stack %s: preset %q is empty, repeated or has no template", m.ID, preset.ID)
		}
		presets[preset.ID] = true
		for _, service := range preset.Services {
			if !services[service] {
				return fmt.Errorf("stack %s: preset %s runs %q, which is not a service of the stack", m.ID, preset.ID, service)
			}
		}
	}
	credentials := map[string]bool{}
	for _, credential := range m.Credentials {
		if credential.Name == "" || credentials[credential.Name] || !services[credential.Service] {
			return fmt.Errorf("stack %s: credential %q is empty, repeated or for no service", m.ID, credential.Name)
		}
		credentials[credential.Name] = true
	}
	steps := map[string]Step{}
	for _, step := range m.Steps {
		if step.ID == "" || steps[step.ID].ID != "" || step.Run == nil || step.Title == "" {
			return fmt.Errorf("stack %s: step %q is empty, repeated, untitled or does nothing", m.ID, step.ID)
		}
		if !services[step.Service] {
			return fmt.Errorf("stack %s: step %s configures %q, which is not a service of the stack", m.ID, step.ID, step.Service)
		}
		if step.Timeout <= 0 || step.Timeout > 30*time.Minute || step.Attempts < 1 || step.Attempts > 20 {
			return fmt.Errorf("stack %s: step %s needs a timeout up to 30 minutes and 1-20 attempts", m.ID, step.ID)
		}
		switch step.Stage {
		case StageReadiness, StageInitialize, StageLink, StageVerify:
		default:
			return fmt.Errorf("stack %s: step %s has stage %q", m.ID, step.ID, step.Stage)
		}
		for _, service := range step.Needs {
			if !services[service] {
				return fmt.Errorf("stack %s: step %s needs %q, which is not a service of the stack", m.ID, step.ID, service)
			}
		}
		steps[step.ID] = step
	}
	for _, step := range m.Steps {
		for _, after := range step.After {
			if steps[after].ID == "" {
				return fmt.Errorf("stack %s: step %s runs after %q, which is not a step", m.ID, step.ID, after)
			}
		}
	}
	if cycle := stepCycle(m.Steps); cycle != nil {
		return fmt.Errorf("stack %s: steps wait for each other in a cycle: %v", m.ID, cycle)
	}
	return nil
}

func stepCycle(steps []Step) []string {
	after := map[string][]string{}
	for _, step := range steps {
		after[step.ID] = step.After
	}
	state := map[string]int{}
	var path []string
	var visit func(string) []string
	visit = func(id string) []string {
		switch state[id] {
		case 1:
			return append(slices.Clone(path[slices.Index(path, id):]), id)
		case 2:
			return nil
		}
		state[id] = 1
		path = append(path, id)
		for _, next := range after[id] {
			if cycle := visit(next); cycle != nil {
				return cycle
			}
		}
		path = path[:len(path)-1]
		state[id] = 2
		return nil
	}
	for _, step := range steps {
		if cycle := visit(step.ID); cycle != nil {
			return cycle
		}
	}
	return nil
}
