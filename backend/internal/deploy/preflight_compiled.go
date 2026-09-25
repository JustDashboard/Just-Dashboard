package deploy

import (
	"errors"
	"strconv"
	"strings"
)

// compiledRecipeFindings judge a JVM or .NET plan from the facts detection
// kept (DetectedJavaBuild, DetectedDotnetBuild): the release the plan's own
// settings choose, which module or project the build selects and from
// where, what the recipe changes about publishing, and — for Rust too — the
// registry credentials the install needs. Where preflight has the tree, the recipe's
// own dry run outvotes the release findings (recipe_preflight.go).
func compiledRecipeFindings(candidate *DetectedCandidate, configuration PlanConfiguration) []PreflightFinding {
	build := configuration.Build
	if candidate == nil || build.Method != BuildRecipe {
		return nil
	}
	recipe := build.Recipe
	if recipe == "" {
		recipe = candidate.Recipe
	}
	var findings []PreflightFinding
	switch {
	case recipe == "java" && candidate.JavaBuild != nil:
		findings = javaPlanFindings(candidate.JavaBuild, build)
	case recipe == "dotnet" && candidate.DotnetBuild != nil:
		findings = dotnetPlanFindings(candidate.DotnetBuild, build)
	case recipe == "rust":
		// A private Cargo registry's token is read by cargo fetch.
	default:
		return nil
	}
	return append(findings, registryCredentialFindings(candidate, configuration)...)
}

func javaPlanFindings(facts *DetectedJavaBuild, build BuildPlanConfig) []PreflightFinding {
	var findings []PreflightFinding
	plan, err := planJavaToolchain(facts.toolchainFacts(), build.JavaVersion)
	var refusal toolchainVersionError
	switch {
	case errors.As(err, &refusal) && refusal.code == "gradle_wrapper_incompatible":
		findings = append(findings, finding(refusal.code, PreflightBlocked,
			"The Gradle wrapper cannot run on the Java release this build needs", refusal.text,
			"The build runs the committed wrapper's Gradle, and each Gradle release runs on a bounded range of JDKs.",
			"Upgrade the wrapper and commit it, or choose an older Java version in Build settings.", "deploy", "configuration.build.javaVersion"))
	case errors.As(err, &refusal):
		findings = append(findings, finding(refusal.code, PreflightBlocked,
			"The Java release cannot build this source", refusal.text,
			"The recipe builds with Temurin JDK 8, 11, 17, 21 or 25 and runs on the JRE of the same release.",
			"Choose a Java version in Build settings that the build files allow, or build from a Dockerfile.", "deploy", "configuration.build.javaVersion"))
	case err == nil:
		measured := facts.Tool + " · builds on Java " + strconv.Itoa(plan.runner) + ", runs on " + plan.runtimeImage() + " (" + plan.from + ")"
		if plan.provide > 0 {
			measured += " · toolchain Java " + strconv.Itoa(plan.provide) + " provided beside it"
		}
		findings = append(findings, finding("java_toolchain", PreflightPass,
			"Java release", boundedFindingText(measured),
			"The build image and the runtime JRE are Temurin's Ubuntu images, published for amd64 and arm64.", "", "deploy", "configuration.build.javaVersion"))
		if plan.mapped != "" {
			findings = append(findings, finding("java_version_mapped", PreflightWarning,
				"The declared Java release is built on a newer one", plan.mapped,
				"The recipe carries the LTS releases; a newer JDK compiles for the declared release and its JRE runs the result.",
				"Choose a Java version in Build settings to pin it, or declare an LTS release.", "deploy", "configuration.build.javaVersion"))
		}
	}
	if facts.Module != "" || facts.Context != "" {
		code, what := "java_module_selected", "Maven module"
		if facts.Tool == "gradle" {
			code, what = "gradle_subproject_selected", "Gradle project"
		}
		measured := firstNonEmpty(facts.Module, "the root project") + " from " + firstNonEmpty(facts.Context, ".")
		findings = append(findings, finding(code, PreflightPass,
			"Builds one "+what+" from its build's root", boundedFindingText(measured),
			"The module is built from the directory that owns it, with its parent build files and the modules it depends on; the root directory alone would miss them.",
			"", "deploy", "configuration.build.rootDirectory"))
	}
	if facts.WrapperJarMissing {
		findings = append(findings, finding("gradle_wrapper_jar_missing", PreflightWarning,
			"The Gradle wrapper's jar is not committed", "gradlew without gradle/wrapper/gradle-wrapper.jar",
			"The wrapper cannot run without its jar (a *.jar ignore rule usually leaves it out), so the image's own Gradle builds instead, which may not be the release the build was written for.",
			"Commit gradle/wrapper/gradle-wrapper.jar (git add -f) so the build runs the Gradle it pins.", "deploy", "configuration.build"))
	}
	if strings.TrimSpace(build.BuildCommand) == "" && !facts.Runnable && !facts.Aggregator && !facts.Library {
		severity, means := PreflightWarning, "Nothing in the build files packages a runnable application — no Spring Boot, Quarkus, Micronaut, Ktor, Shadow or application plugin, no assembly or shade jar, no main class in the manifest — so the build may produce only a library jar, which the recipe refuses after building."
		if facts.Packaging == javaPackagingWar {
			severity, means = PreflightBlocked, "The build packages a WAR for a servlet container, which the recipe does not run."
		}
		findings = append(findings, finding("java_artifact_not_runnable", severity,
			"The build may not produce a runnable jar", firstNonEmpty(facts.Packaging, "no packaging plugin or main class"), means,
			"Apply the plugin that packages the framework (or set a Main-Class), or build from a Dockerfile.", "deploy", "configuration.build"))
	}
	if facts.VaadinDevMode {
		findings = append(findings, finding("vaadin_dev_mode", PreflightWarning,
			"Vaadin builds in development mode", "no production profile or build-frontend goal",
			"A development-mode Vaadin application compiles its front end at startup and needs Node, which the runtime image does not have.",
			"Add Vaadin's production profile (or the build-frontend goal) to the pom, as the Vaadin starters do.", "deploy", "configuration.build"))
	}
	if len(facts.Profiles) > 0 {
		findings = append(findings, finding("java_production_profile", PreflightPass,
			"Builds the production profile", "-P"+strings.Join(facts.Profiles, ","),
			"The build bakes the production configuration and front end into the artifact.", "", "deploy", "configuration.build"))
	}
	return findings
}

func dotnetPlanFindings(facts *DetectedDotnetBuild, build BuildPlanConfig) []PreflightFinding {
	var findings []PreflightFinding
	plan, err := planDotnetToolchain(facts.toolchainFacts(), build.DotnetVersion)
	var refusal toolchainVersionError
	switch {
	case errors.As(err, &refusal):
		findings = append(findings, finding(refusal.code, PreflightBlocked,
			"The .NET release cannot build this project", refusal.text,
			"The recipe publishes net8.0, net9.0 or net10.0 with the matching SDK image, or the SDK global.json pins.",
			"Choose a .NET version in Build settings, change the project's target framework or global.json, or build from a Dockerfile.", "deploy", "configuration.build.dotnetVersion"))
	case err == nil:
		measured := "net" + plan.target + " · sdk:" + plan.sdk
		if plan.sdk != plan.target {
			measured += " (" + plan.sdkFrom + ")"
		}
		findings = append(findings, finding("dotnet_toolchain", PreflightPass,
			".NET release", boundedFindingText(measured),
			"The SDK image builds the project and the runtime image of its target runs it.", "", "deploy", "configuration.build.dotnetVersion"))
	}
	if facts.Context != "" {
		findings = append(findings, finding("dotnet_project_selected", PreflightPass,
			"Publishes one project from its solution's root", boundedFindingText(facts.Project+" from "+facts.Context),
			"The build context holds the projects it references and the Directory.*.props, NuGet.config and global.json files that apply to it.",
			"", "deploy", "configuration.build.rootDirectory"))
	}
	if len(facts.Native) > 0 {
		findings = append(findings, finding("dotnet_aot_disabled", PreflightPass,
			"Publishes the framework-dependent dll", strings.Join(facts.Native, ", ")+" set to false",
			"Native AOT and single-file publishing need another SDK image and write a native executable; the JIT-compiled dll runs the same application.",
			"", "deploy", "configuration.build"))
	}
	if facts.SPARoot != "" {
		findings = append(findings, finding("dotnet_spa_node", PreflightPass,
			"Node is added for the front end the publish builds", facts.SPARoot,
			"The project's publish runs npm; the recipe copies Node into the SDK image and installs the front end's dependencies from its lockfile first.",
			"", "deploy", "configuration.build"))
	}
	if len(facts.Aspire) > 0 {
		findings = append(findings, finding("dotnet_aspire_orchestration", PreflightWarning,
			"The Aspire AppHost wires settings this deployment has to set", boundedFindingText(strings.Join(facts.Aspire, ", ")),
			facts.AppHost+" hands this project service-discovery addresses and connection strings when it starts it; deployed on its own, nothing sets them.",
			"Add them as variables: a linked database's connection string, and the other services' URLs.", "deploy", "variables"))
	}
	return findings
}
