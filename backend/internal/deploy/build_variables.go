package deploy

import (
	"fmt"
	"sort"
)

func buildVariableNames(variables map[string]string) []string {
	names := make([]string, 0, len(variables))
	for name := range variables {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Build scope is a usable contract on its own. Explicit stage mappings narrow
// selected credentials to install, or widen a value to install and build
// alike; other build values reach only the build command through ephemeral
// mounts, never ARG, ENV or the generated Dockerfile.
func recipeBuildBindings(config BuildPlanConfig, names []string) ([]BuildSecretConfig, error) {
	bindings := append([]BuildSecretConfig{}, config.Secrets...)
	if config.Method != BuildRecipe {
		return bindings, nil
	}
	seen := make(map[string]bool, len(bindings))
	for _, binding := range bindings {
		seen[binding.Variable] = true
	}
	for _, name := range names {
		if ValidateEnvKey(name) != nil {
			return nil, fmt.Errorf("%w: invalid build variable name", ErrInvalidVariable)
		}
		if !seen[name] {
			bindings = append(bindings, BuildSecretConfig{Variable: name, Step: "build"})
			seen[name] = true
		}
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].Variable < bindings[j].Variable })
	return bindings, nil
}
