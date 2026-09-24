package deploy

import (
	"slices"
	"strings"
)

// nodeInstallFindings judges a plan's Node install — the Node recipe's, or
// the PHP recipe's asset stage — from what detection recorded for each
// package manager: the recipe computes the same plan from the same files,
// so what is found here is what the build does.
func nodeInstallFindings(candidate *DetectedCandidate, configuration PlanConfiguration) []PreflightFinding {
	build := configuration.Build
	chosen := build.PackageManager
	if len(candidate.NodeInstalls) == 0 {
		return legacyNodeInstallFindings(candidate, chosen)
	}
	manager := chosen
	if manager == "" {
		manager = candidate.PackageManager
	}
	if manager == "" {
		if len(candidate.PackageManagers) > 1 {
			return []PreflightFinding{finding("package_manager_ambiguous", PreflightBlocked,
				"Competing lockfiles need a package manager", nodeLockfileSummary(candidate),
				"No lockfile is the one a frozen install would accept, and nothing in the repository names its manager.",
				"Choose the package manager, declare packageManager in package.json, or delete the stale lockfile.", "deploy", "configuration.build.packageManager")}
		}
		return nil
	}
	index := slices.IndexFunc(candidate.NodeInstalls, func(install DetectedNodeInstall) bool { return install.Manager == manager })
	if index < 0 {
		return nil
	}
	install := candidate.NodeInstalls[index]
	findings := append([]PreflightFinding{}, install.Findings...)
	if slices.ContainsFunc(findings, func(item PreflightFinding) bool { return item.Severity == PreflightBlocked }) {
		return findings
	}
	if candidate.Recipe == "php" {
		return append(findings, finding("build_commands", PreflightPass,
			"Front-end assets build commands", install.Install+" · "+install.BuildCommand,
			"The PHP recipe's asset stage runs these before copying public/build into the image.", "", "deploy", "configuration.build.packageManager"))
	}
	commands := []struct{ label, field, saved string }{
		{"build", "configuration.build.buildCommand", build.BuildCommand},
		{"start", "configuration.build.startCommand", build.StartCommand},
	}
	runs := map[string]string{}
	for _, command := range commands {
		runs[command.label] = nodeRunnerFor(command.saved, manager)
		if runs[command.label] == command.saved {
			continue
		}
		findings = append(findings, finding("runner_mismatch", PreflightWarning,
			"The "+command.label+" command names another package manager",
			"`"+command.saved+"` runs as `"+runs[command.label]+"`",
			"The build installs with "+nodeManagerLabel(manager)+", so a command still naming another manager's runner is run through "+manager+" instead.",
			"Save the command with "+manager+"'s runner, or choose the package manager it names.", "deploy", command.field))
	}
	if nodeCommandTools(nil, []string{runs["build"], runs["start"]})["deno"] {
		return append(findings, finding("command_runner_missing", PreflightBlocked,
			"A command needs a runtime the Node image does not have", "deno",
			"The build or start command calls deno, which the Node recipe does not install.",
			"Use a Dockerfile, or change the command to one the Node image runs.", "deploy", "configuration.build.buildCommand"))
	}
	for _, segment := range nodeInstallSegments(runs["build"]) {
		if !slices.ContainsFunc(findings, func(item PreflightFinding) bool { return item.Code == "install_in_build_command" }) {
			findings = append(findings, finding("install_in_build_command", PreflightWarning,
				"The build command installs dependencies again", segment,
				"The recipe already ran "+install.Install+"; installing again in the build resolves versions the lockfile did not pin.",
				"Remove the install from the build command.", "deploy", "configuration.build.buildCommand"))
		}
	}
	measured := install.Install
	if runs["build"] != "" {
		measured += " · " + runs["build"]
	}
	if runs["start"] != "" && build.OutputDirectory == "" {
		measured += " · start: " + runs["start"]
	}
	findings = append(findings, finding("build_commands", PreflightPass,
		"Install, build and start commands", boundedFindingText(measured),
		"These run in the generated Dockerfile, in this order.", "", "deploy", "configuration.build"))
	return append(findings, registryCredentialFindings(candidate, configuration)...)
}

// registryCredentialFindings checks that every credential a package
// manager's configuration names reaches the install step: a build-scoped
// variable mapped to install. Values are not compared — at deploy time they
// are sealed — only that the install can receive one.
func registryCredentialFindings(candidate *DetectedCandidate, configuration PlanConfiguration) []PreflightFinding {
	findings := []PreflightFinding{}
	for _, variable := range candidate.Variables {
		if variable.Step != "install" {
			continue
		}
		declared := slices.ContainsFunc(configuration.Variables, func(planned PlannedVariable) bool {
			return planned.Name == variable.Name && slices.Contains(planned.Scopes, "build")
		})
		mapped := slices.ContainsFunc(configuration.Build.Secrets, func(secret BuildSecretConfig) bool {
			return secret.Variable == variable.Name && buildSecretReaches(secret.Step, "install")
		})
		if declared && mapped {
			continue
		}
		severity := PreflightWarning
		means := strings.Join(variable.Sources, ", ") + " authenticates the registry with " + variable.Name + "; the install runs without it."
		if variable.InstallRequired {
			severity = PreflightBlocked
			means = strings.Join(variable.Sources, ", ") + " authenticates a registry this project installs packages from with " + variable.Name + "; without it the install fails with 401 or 404."
		}
		action := "Add " + variable.Name + " as a build variable; it is mapped to the install step."
		if declared {
			action = "Map " + variable.Name + " to the install step in the build settings."
		}
		findings = append(findings, finding("registry_token_missing", severity,
			"A registry credential does not reach the install", variable.Name, means, action, "deploy", "variables."+variable.Name))
	}
	return findings
}

func nodeLockfileSummary(candidate *DetectedCandidate) string {
	if len(candidate.Lockfiles) == 0 {
		return strings.Join(candidate.PackageManagers, ", ")
	}
	notes := []string{}
	for _, lockfile := range candidate.Lockfiles {
		notes = append(notes, lockfile.Note)
	}
	return boundedFindingText(strings.Join(notes, "; "))
}

// legacyNodeInstallFindings judges a candidate detected before installs
// were recorded per manager, from its lockfile names alone.
func legacyNodeInstallFindings(candidate *DetectedCandidate, chosen string) []PreflightFinding {
	switch {
	case chosen != "" && len(candidate.PackageManagers) > 0 && !slices.Contains(candidate.PackageManagers, chosen):
		return []PreflightFinding{finding("package_manager_lockfile_missing", PreflightBlocked,
			"Selected package manager has no lockfile", chosen+"; lockfiles for "+strings.Join(candidate.PackageManagers, ", "),
			"A frozen install needs the selected manager's own lockfile.",
			"Choose a package manager whose lockfile is committed, or commit its lockfile.", "deploy", "configuration.build.packageManager")}
	case chosen == "" && candidate.PackageManager == "" && len(candidate.PackageManagers) > 1:
		return []PreflightFinding{finding("package_manager_ambiguous", PreflightBlocked,
			"Competing lockfiles need a package manager", strings.Join(candidate.PackageManagers, ", "),
			"Installing from a lockfile the project no longer maintains builds untested dependency versions.",
			"Choose the package manager, declare packageManager in package.json, or delete the stale lockfile.", "deploy", "configuration.build.packageManager")}
	}
	return nil
}
