package deploy

import (
	"sort"
	"strings"
)

func (r *dockerRecovery) applyExistingServicesScope(candidate WorkloadCandidate) {
	services := object(r.model["services"])
	present := map[string]bool{}
	for name, captures := range r.containers {
		if len(captures) > 0 {
			present[name] = true
		}
	}
	// Failed captures remain blocking instead of being reclassified as absent.
	for _, service := range candidate.Services {
		if service.ResourceID != "" {
			present[service.Name] = true
		}
	}
	excluded := map[string]bool{}
	for name := range services {
		if !present[name] {
			excluded[name] = true
		}
	}
	for name, raw := range services {
		if excluded[name] {
			continue
		}
		for _, reference := range scopedComposeServiceReferences(object(raw)) {
			if excluded[reference.service] {
				r.issue("scope_dependency_excluded", "This retained service references an excluded declared service. Import all services or resolve that relationship in the original Compose definition; its semantics cannot be silently changed.", name, reference.field, true)
			}
		}
	}
	for name := range excluded {
		r.result.Adoption.ExcludedServices = append(r.result.Adoption.ExcludedServices, name)
		r.issue("compose_services_excluded", "This declared service has no container and is excluded from the managed recipe. Its original Compose definition is left unchanged; Deploy changes will not create it. All existing running and stopped containers remain included.", name, "scope", false)
		delete(services, name)
	}
	sort.Strings(r.result.Adoption.ExcludedServices)
	r.result.Adoption.ServiceCount = len(services)
}

type scopedServiceReference struct{ service, field string }

func scopedComposeServiceReferences(service map[string]any) []scopedServiceReference {
	var references []scopedServiceReference
	add := func(field, value string) {
		if value != "" {
			references = append(references, scopedServiceReference{value, field})
		}
	}
	for _, field := range []string{"depends_on", "links", "volumes_from"} {
		if entries := object(service[field]); entries != nil {
			for name := range entries {
				add(field, name)
			}
		}
		for _, value := range recoveryStringValues(service[field]) {
			if field == "volumes_from" && strings.HasPrefix(value, "container:") {
				continue
			}
			name, _, _ := strings.Cut(value, ":")
			add(field, name)
		}
	}
	for _, field := range []string{"network_mode", "pid", "ipc", "uts"} {
		if value, ok := service[field].(string); ok && strings.HasPrefix(value, "service:") {
			add(field, strings.TrimPrefix(value, "service:"))
		}
	}
	if build := object(service["build"]); build != nil {
		if context, _ := build["context"].(string); strings.HasPrefix(context, "service:") {
			add("build.context", strings.TrimPrefix(context, "service:"))
		}
		for _, raw := range object(build["additional_contexts"]) {
			if value, ok := raw.(string); ok && strings.HasPrefix(value, "service:") {
				add("build.additional_contexts", strings.TrimPrefix(value, "service:"))
			}
		}
		for _, value := range recoveryStringValues(build["additional_contexts"]) {
			_, value, _ = strings.Cut(value, "=")
			if strings.HasPrefix(value, "service:") {
				add("build.additional_contexts", strings.TrimPrefix(value, "service:"))
			}
		}
	}
	if extends := object(service["extends"]); extends != nil {
		if value, _ := extends["service"].(string); value != "" {
			add("extends.service", value)
		}
	}
	return references
}

func recoveryStringValues(value any) []string {
	if values, ok := value.([]string); ok {
		return values
	}
	var result []string
	if values, ok := value.([]any); ok {
		for _, value := range values {
			if text, ok := value.(string); ok {
				result = append(result, text)
			}
		}
	}
	return result
}

func pruneScopedComposeResources(model map[string]any) {
	used := map[string]map[string]bool{"volumes": {}, "networks": {}, "configs": {}, "secrets": {}}
	for _, raw := range object(model["services"]) {
		service := object(raw)
		for _, field := range []string{"networks", "volumes", "configs", "secrets"} {
			if field == "networks" {
				for name := range object(service[field]) {
					used[field][name] = true
				}
			}
			for _, value := range recoveryStringValues(service[field]) {
				source, _, _ := strings.Cut(value, ":")
				used[field][source] = true
			}
			if values, ok := service[field].([]any); ok {
				for _, raw := range values {
					if mapping := object(raw); mapping != nil {
						if source, _ := mapping["source"].(string); source != "" {
							used[field][source] = true
						}
					}
				}
			}
		}
	}
	for field, names := range used {
		for name := range object(model[field]) {
			if !names[name] {
				delete(object(model[field]), name)
			}
		}
	}
}
