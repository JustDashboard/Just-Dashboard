package deploy

import "sort"

func (d *Draft) refreshEnvironmentKeys() {
	d.EnvironmentKeys = nil
	for name := range d.environment {
		// Empty overrides still suppress defaults and must survive a resumed form.
		d.EnvironmentKeys = append(d.EnvironmentKeys, name)
	}
	sort.Strings(d.EnvironmentKeys)
}

func (d *Draft) withEnvironmentMetadata(configuration PlanConfiguration) PlanConfiguration {
	seen := map[string]bool{}
	for _, variable := range configuration.Variables {
		seen[variable.Name] = true
	}
	// Existing declarations remain the fallback when an override is removed,
	// including an automatic password generator and operator-selected scopes.
	// Commit marks the supplied value secret independently of its fallback.
	names := make([]string, 0, len(d.environment))
	for name := range d.environment {
		if !seen[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		configuration.Variables = append(configuration.Variables, PlannedVariable{
			Name: name, Sensitivity: "secret", Scopes: []string{"runtime", "build"},
		})
	}
	return canonicalConfiguration(configuration)
}

// The same precedence is used for required checks, reference validation and
// commit, including a deliberately empty input overriding a generated default.
func (d *Draft) variableValues(configuration PlanConfiguration) map[string]string {
	values := make(map[string]string, len(configuration.Variables))
	for _, variable := range configuration.Variables {
		switch {
		case variable.Reference != "":
			values[variable.Name] = variable.Reference
		case variable.Generate != 0:
			values[variable.Name] = "generated-at-commit"
		default:
			values[variable.Name] = variable.Value
		}
	}
	for name, value := range d.environment {
		values[name] = value
	}
	return values
}
