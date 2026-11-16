package deploy

import (
	"slices"
	"sort"
	"strings"
)

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
	// Commit marks the supplied value secret independently of its fallback,
	// unless it is browser-public (suppliedVariableSensitivity).
	names := make([]string, 0, len(d.environment))
	for name := range d.environment {
		if !seen[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	installOnly := d.installOnlyCredentials()
	for _, name := range names {
		scopes := []string{"runtime", "build"}
		if installOnly[name] {
			// Only the install reads a registry token, so the running
			// application never receives it.
			scopes = []string{"build"}
		}
		configuration.Variables = append(configuration.Variables, PlannedVariable{
			Name: name, ValueMode: "literal", Sensitivity: suppliedVariableSensitivity(PlannedVariable{Name: name, ValueMode: "literal", Sensitivity: "plain"}),
			Scopes: scopes,
		})
	}
	return canonicalConfiguration(d.withInstallCredentials(configuration))
}

// installOnlyCredentials are the registry credentials detection found in a
// package manager's configuration and nowhere the source reads at run time.
func (d *Draft) installOnlyCredentials() map[string]bool {
	names := map[string]bool{}
	candidate := selectedDetectionCandidate(d.Data.Detection)
	if candidate == nil {
		return names
	}
	for _, detected := range candidate.Variables {
		if detected.Step == "install" && len(detected.Sources) > 0 && !slices.ContainsFunc(detected.Sources, func(source string) bool {
			return !registryConfigSource(source)
		}) {
			names[detected.Name] = true
		}
	}
	return names
}

// withInstallCredentials maps each registry credential detection found in a
// package manager's configuration to the install step, when the plan gives
// it a build-scoped value and has not mapped it already: the install is the
// one step that reads it, and a value left on the build step never reaches
// the install that fails without it.
func (d *Draft) withInstallCredentials(configuration PlanConfiguration) PlanConfiguration {
	candidate := selectedDetectionCandidate(d.Data.Detection)
	if candidate == nil || configuration.Build.Method != BuildRecipe {
		return configuration
	}
	for _, detected := range candidate.Variables {
		if detected.Step != "install" || slices.ContainsFunc(configuration.Build.Secrets, func(secret BuildSecretConfig) bool {
			return secret.Variable == detected.Name
		}) {
			continue
		}
		if slices.ContainsFunc(configuration.Variables, func(variable PlannedVariable) bool {
			return variable.Name == detected.Name && slices.Contains(variable.Scopes, "build")
		}) {
			configuration.Build.Secrets = append(configuration.Build.Secrets, BuildSecretConfig{Variable: detected.Name, Step: "install"})
		}
	}
	return configuration
}

// suppliedVariableSensitivity is how a value typed into the environment is
// stored: secret, because that is where secrets are typed — except a
// browser-public name (NEXT_PUBLIC_, VITE_, …) not declared secret, whose
// value the framework compiles into the page's JavaScript by design. Keeping
// that one plain is what lets a custom Dockerfile or a Compose build receive
// it as a build argument, which a secret never becomes.
func suppliedVariableSensitivity(declared PlannedVariable) string {
	if declared.Sensitivity == "plain" && publicBuildVariable(declared.Name) {
		return "plain"
	}
	return "secret"
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

// Empty mode retains the shipped value-shape inference for old drafts.
func (d *Draft) variableModes(configuration PlanConfiguration) map[string]string {
	modes := map[string]string{}
	linked := linkedDatabaseConnections(configuration.Dependencies)
	for _, variable := range configuration.Variables {
		mode := variable.ValueMode
		staged, supplied := d.environment[variable.Name]
		if !supplied && variable.Reference != "" {
			mode = "reference"
		}
		// The new-deployment page links a database by writing its reference
		// into the variable's row, so it arrives as supplied text, which is
		// otherwise kept literal. Read as the text, the application received
		// `${{database.5}}` and no network to the database at all.
		if supplied && linkedDatabaseReference(staged, linked) {
			mode = "reference"
		}
		if mode == "" && (variable.Value != "" || variable.Generate > 0) {
			mode = "literal"
		}
		modes[variable.Name] = mode
	}
	return modes
}

// linkedDatabaseConnections are the saved database connections a plan links
// as dependencies, by the id each dependency names.
func linkedDatabaseConnections(dependencies []PlannedDependency) map[string]bool {
	linked := map[string]bool{}
	for _, dependency := range dependencies {
		if dependency.Kind == "database" && dependency.ResourceKind == "database_connection" && dependency.ResourceID != "" {
			linked[dependency.ResourceID] = true
		}
	}
	return linked
}

// linkedDatabaseReference says a value is the whole reference to a database
// connection the same plan links — `${{database.5}}`, `${{database.5.jdbc}}`,
// `${{database.5.pooled}}` — by its id. Linking is the operator's own act in
// this plan, so such a value is the link and never a captured value that only
// reads like one, which every other supplied value is presumed to be.
func linkedDatabaseReference(value string, linked map[string]bool) bool {
	if len(linked) == 0 {
		return false
	}
	reference, err := ParseVariableReference(value)
	if err != nil || reference.Kind != "database" {
		return false
	}
	id, _, _ := strings.Cut(reference.Target, ".")
	return linked[id]
}
func variableWriteMode(request VariableWriteRequest) string {
	if request.Reference != "" {
		return "reference"
	}
	return "literal"
}

// VariableReferences is every variable the draft would commit as a typed
// reference, by name: an explicit reference, or a value whose mode leaves it
// read as one, exactly as commit stores it and a run resolves it. It is for a
// caller deciding whether the draft may be committed by whoever is asking.
func (d *Draft) VariableReferences() map[string]VariableReference {
	out := map[string]VariableReference{}
	if d.Data.Configuration == nil {
		return out
	}
	configuration := *d.Data.Configuration
	values, modes := d.variableValues(configuration), d.variableModes(configuration)
	for name, value := range values {
		if reference, ok := parseStoredReference(value, modes[name]); ok {
			out[name] = reference
		}
	}
	return out
}
