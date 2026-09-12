package api

import (
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/blueprint"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// DockerTemplate is a starting point for the container form. It is rendered
// from the same reviewed blueprint catalogue Deployments uses, so there is one
// catalogue with one schema rather than two lists that drift apart.
type DockerTemplate struct {
	ID       string                `json:"id"`
	Name     string                `json:"name"`
	Blurb    string                `json:"blurb"`
	Category blueprint.Category    `json:"category"`
	Requires string                `json:"requires,omitempty"`
	DocsURL  string                `json:"docsUrl"`
	License  string                `json:"license"`
	Spec     dockerx.ContainerSpec `json:"spec"`
}

func (s *Server) handleDockerTemplates(w http.ResponseWriter, r *http.Request) error {
	entries, err := blueprint.Catalog()
	if err != nil {
		return httpx.Internal(err)
	}
	templates := make([]DockerTemplate, 0, len(entries))
	for _, entry := range entries {
		// Game servers are deployed through the wizard, which owns their world
		// data, EULA and update path. Offering one as a bare container here
		// would be a second way to create it with none of that.
		if entry.Profile == blueprint.ProfileGame {
			continue
		}
		rendered, renderErr := blueprint.Render(entry, nil)
		if renderErr != nil {
			continue
		}
		templates = append(templates, DockerTemplate{
			ID: entry.ID, Name: entry.Name, Blurb: entry.Description, Category: entry.Category,
			Requires: templateRequirement(entry, rendered), DocsURL: entry.DocsURL,
			License: entry.Provenance.License, Spec: templateSpec(entry, rendered),
		})
	}
	sort.Slice(templates, func(i, j int) bool {
		if templates[i].Category != templates[j].Category {
			return templates[i].Category < templates[j].Category
		}
		return templates[i].Name < templates[j].Name
	})
	httpx.JSON(w, http.StatusOK, templates)
	return nil
}

// templateRequirement names what the operator still has to decide. A starting
// point that looks complete but will not start is worse than one that says so.
func templateRequirement(entry *blueprint.Blueprint, rendered *blueprint.Plan) string {
	needs := []string{}
	for _, variable := range rendered.Variables {
		if variable.Generated {
			needs = append(needs, variable.Name)
		}
	}
	for _, input := range entry.Inputs {
		if input.Required && input.Default == "" && input.Variable != "" {
			needs = append(needs, input.Variable)
		}
	}
	if len(needs) == 0 {
		return ""
	}
	sort.Strings(needs)
	return "Set " + strings.Join(needs, ", ") + " before starting it. Deploying this through " +
		"Deployments generates the secrets for you."
}

// templateSpec renders one blueprint into the container form's own shape.
// Everything binds to loopback: a port that should be reachable from outside
// belongs behind the reverse proxy this dashboard already manages.
func templateSpec(entry *blueprint.Blueprint, rendered *blueprint.Plan) dockerx.ContainerSpec {
	spec := dockerx.ContainerSpec{
		Name: entry.ID, Image: rendered.Image,
		Env: []dockerx.EnvVar{}, Ports: []dockerx.PortMapping{},
		Mounts: []dockerx.MountSpec{}, Labels: []dockerx.LabelSpec{}, Networks: []string{},
		Limits:        dockerx.ResourceLimits{MemoryMB: int64(rendered.MemoryMB)},
		RestartPolicy: "unless-stopped", Logging: dockerx.CappedLogging(),
		Pull: "missing", Start: true, StopSignal: rendered.StopSignal,
	}
	for _, variable := range rendered.Variables {
		// A generated secret has no value here on purpose: this form is not the
		// secret generator, and a placeholder would be shipped as a password.
		value := variable.Value
		if variable.Generated {
			value = ""
		}
		spec.Env = append(spec.Env, dockerx.EnvVar{Name: variable.Name, Value: value})
	}
	for _, port := range rendered.Ports {
		if port.Exposure == "internal" {
			continue
		}
		spec.Ports = append(spec.Ports, dockerx.PortMapping{
			HostIP: "127.0.0.1", HostPort: port.Internal,
			ContainerPort: port.Internal, Protocol: port.Protocol,
		})
	}
	for _, volume := range rendered.Volumes {
		kind := "volume"
		source := entry.ID + "-" + volume.Name
		if filepath.IsAbs(volume.Target) && !volume.Data && !volume.Backup {
			// A non-data mount at an absolute host path is a bind the blueprint
			// declared deliberately, such as a read-only Docker socket.
			if volume.Target == "/var/run/docker.sock" {
				kind, source = "bind", volume.Target
			}
		}
		spec.Mounts = append(spec.Mounts, dockerx.MountSpec{
			Type: kind, Source: source, Target: volume.Target, ReadOnly: volume.ReadOnly,
		})
	}
	return spec
}
