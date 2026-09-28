package deploy

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// DetectedJavaBuild is what a JVM candidate's build says about building it
// (detect_jvm.go), kept so preflight can judge a plan's Java release, the
// module it builds and its artifact without the source tree.
type DetectedJavaBuild struct {
	Tool string `json:"tool"`
	// Context is the directory the build runs from when it is not the
	// candidate's root — the Maven reactor or Gradle settings root that owns
	// it — and Module how the build selects the root there (a Maven -pl
	// selector, a Gradle project path).
	Context string `json:"context,omitempty"`
	Module  string `json:"module,omitempty"`
	// Packaging is how the runnable artifact is produced (bootJar, the
	// quarkus-app directory, installDist, a fat jar); Runnable says the
	// build declares one, Library that it is a java-library, and
	// Aggregator that it only aggregates other modules.
	Packaging  string `json:"packaging,omitempty"`
	Runnable   bool   `json:"runnable,omitempty"`
	Library    bool   `json:"library,omitempty"`
	Aggregator bool   `json:"aggregator,omitempty"`
	// Release is the Java release the build files declare and where;
	// Toolchain says it is a Gradle toolchain, an exact JDK. Pinned is what a
	// version file (.java-version, .sdkmanrc, .tool-versions, mise.toml,
	// system.properties) pins.
	Release     int    `json:"release,omitempty"`
	ReleaseFrom string `json:"releaseFrom,omitempty"`
	Toolchain   bool   `json:"toolchain,omitempty"`
	Pinned      int    `json:"pinned,omitempty"`
	PinnedFrom  string `json:"pinnedFrom,omitempty"`
	// Wrapper is the Gradle release the committed wrapper runs;
	// WrapperUsable says its jar is committed too, and WrapperJarMissing
	// that gradlew is committed without it.
	Wrapper           string `json:"wrapper,omitempty"`
	WrapperUsable     bool   `json:"wrapperUsable,omitempty"`
	WrapperJarMissing bool   `json:"wrapperJarMissing,omitempty"`
	Foojay            bool   `json:"foojay,omitempty"`
	// Profiles are activated for a production build; VaadinDevMode says a
	// Vaadin build has no production profile to activate.
	Profiles      []string `json:"profiles,omitempty"`
	VaadinDevMode bool     `json:"vaadinDevMode,omitempty"`
}

// DetectedDotnetBuild is what a .NET candidate's project says about
// publishing it (detect_dotnet.go).
type DetectedDotnetBuild struct {
	// Project is the project file's checkout path, Kind what it builds
	// (web, worker, exe, library, test, apphost, blazor-wasm), and Context
	// the directory it publishes from when that is not its own.
	Project string `json:"project"`
	Kind    string `json:"kind"`
	Context string `json:"context,omitempty"`
	// Targets are the supported releases its target frameworks name, and
	// TargetText what it declares.
	Targets     []string `json:"targets,omitempty"`
	TargetText  string   `json:"targetText,omitempty"`
	MultiTarget bool     `json:"multiTarget,omitempty"`
	// SDKPin is the SDK global.json (or a version manager) pins, with its
	// roll-forward policy.
	SDKPin      string `json:"sdkPin,omitempty"`
	SDKPinFrom  string `json:"sdkPinFrom,omitempty"`
	RollForward string `json:"rollForward,omitempty"`
	// Native lists the publish settings the recipe turns off to publish a
	// framework-dependent dll; SPARoot the front end its publish builds
	// with npm.
	Native  []string `json:"native,omitempty"`
	SPARoot string   `json:"spaRoot,omitempty"`
	// AppHost is an Aspire AppHost that starts this project, and Aspire the
	// variables it wires into it (service discovery, connection strings).
	AppHost string   `json:"appHost,omitempty"`
	Aspire  []string `json:"aspire,omitempty"`
	// References are the projects it references, directly or through
	// others, with the frameworks they declare: a restore held to one
	// framework hands it to each of them.
	References []DetectedDotnetReference `json:"references,omitempty"`
}

// DetectedDotnetReference is a referenced project's checkout path and its
// <TargetFramework> or <TargetFrameworks>.
type DetectedDotnetReference struct {
	Project string `json:"project"`
	Targets string `json:"targets,omitempty"`
}

var (
	javaVersionSettingRE   = regexp.MustCompile(`^(?:8|11|17|21|25)$`)
	dotnetVersionSettingRE = regexp.MustCompile(`^(?:8|9|10)\.0$`)
)

// validateCompiledBuildSettings checks the Java and .NET release settings,
// which belong to their own recipe.
func validateCompiledBuildSettings(build BuildPlanConfig) error {
	if build.JavaVersion != "" && (build.Method != BuildRecipe || build.Recipe != "java" || !javaVersionSettingRE.MatchString(build.JavaVersion)) {
		return invalidField("build.javaVersion", "Java version must select 8, 11, 17, 21 or 25 in a Java recipe; use a Dockerfile for other releases")
	}
	if build.DotnetVersion != "" && (build.Method != BuildRecipe || build.Recipe != "dotnet" || !dotnetVersionSettingRE.MatchString(build.DotnetVersion)) {
		return invalidField("build.dotnetVersion", ".NET version must select 8.0, 9.0 or 10.0 in a .NET recipe; use a Dockerfile for other releases")
	}
	return nil
}

// validateDetectedCompiledBuild bounds what a JVM or .NET candidate keeps.
func validateDetectedCompiledBuild(candidate DetectedCandidate) error {
	malformed := fmt.Errorf("%w: detected compiled build is malformed", ErrInvalidPlan)
	text := func(value string, limit int) bool {
		return len(value) <= limit && !strings.ContainsAny(value, "\x00\r\n") &&
			rejectPlanSecretLiteral("detected build", value) == nil
	}
	list := func(values []string, count, limit int) bool {
		return len(values) <= count && !slices.ContainsFunc(values, func(value string) bool { return value == "" || !text(value, limit) })
	}
	if build := candidate.JavaBuild; build != nil {
		if (build.Tool != "maven" && build.Tool != "gradle") || !text(build.Context, 4096) || !text(build.Module, 512) ||
			!text(build.Packaging, 32) || !text(build.ReleaseFrom, 512) || !text(build.PinnedFrom, 512) || !text(build.Wrapper, 32) ||
			build.Release < 0 || build.Release > 99 || build.Pinned < 0 || build.Pinned > 99 || !list(build.Profiles, 8, 128) {
			return malformed
		}
	}
	if build := candidate.DotnetBuild; build != nil {
		references := len(build.References) <= 64
		for _, reference := range build.References {
			references = references && reference.Project != "" && text(reference.Project, 4096) && text(reference.Targets, 512)
		}
		if !text(build.Project, 4096) || !text(build.Kind, 32) || !text(build.Context, 4096) || !list(build.Targets, 8, 16) ||
			!text(build.TargetText, 512) || !text(build.SDKPin, 64) || !text(build.SDKPinFrom, 4096) || !text(build.RollForward, 32) ||
			!list(build.Native, 8, 32) || !text(build.SPARoot, 4096) || !text(build.AppHost, 4096) || !list(build.Aspire, 16, 256) || !references {
			return malformed
		}
	}
	return nil
}
