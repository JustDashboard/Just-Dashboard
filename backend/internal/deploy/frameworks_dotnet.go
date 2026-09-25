package deploy

import (
	"path"
	"regexp"
	"sort"
	"strings"
)

// The .NET recipe publishes one project with the SDK image for its target
// framework and runs it on the matching ASP.NET Core (or plain runtime)
// image, or serves a Blazor WebAssembly app's static output. Supported
// targets are the releases Microsoft still ships images for.
var (
	dotnetRecipeVersions = []string{"8.0", "9.0", "10.0"}
	dotnetVersionRE      = regexp.MustCompile(`^net([0-9]+\.[0-9]+)$`)
)

// dotnetMarker is what detection read of a directory's project files.
type dotnetMarker struct {
	projects []*dotnetProjectFacts
	chosen   *dotnetProjectFacts
	choice   string
	build    dotnetBuild
	// variables are the registry credentials the NuGet.config files that
	// apply to the chosen project read; program is an Aspire AppHost's
	// Program.cs, for what it wires into the projects it starts.
	variables []DetectedVariable
	program   []byte
}

// chooseDotnetProject picks the one project a directory publishes: the only
// one that is not a test, or among several the one web application, or the
// one program.
func chooseDotnetProject(projects []*dotnetProjectFacts) (*dotnetProjectFacts, string) {
	var kept []*dotnetProjectFacts
	for _, project := range projects {
		if project.kind != dotnetKindTest {
			kept = append(kept, project)
		}
	}
	if len(kept) == 0 {
		kept = projects
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].file < kept[j].file })
	if len(kept) == 1 {
		return kept[0], ""
	}
	for _, kinds := range [][]string{{dotnetKindWeb}, {dotnetKindWeb, dotnetKindBlazorWasm, dotnetKindWorker, dotnetKindExe}} {
		var matched []*dotnetProjectFacts
		for _, project := range kept {
			for _, kind := range kinds {
				if project.kind == kind {
					matched = append(matched, project)
				}
			}
		}
		if len(matched) == 1 {
			return matched[0], ""
		}
		if len(matched) > 1 {
			break
		}
	}
	names := make([]string, 0, len(kept))
	for _, project := range kept {
		names = append(names, project.name)
	}
	return nil, "several .NET projects at this root (" + strings.Join(names, ", ") + "); set the root directory to the one to publish"
}

// dotnetCandidate builds the candidate for a directory with project files.
func dotnetCandidate(marker *detectedMarkers, rootLabel string) DetectedCandidate {
	candidate := DetectedCandidate{
		Name: ".NET service in " + rootLabel, Profile: ProfileWorker, Confidence: ConfidenceMedium,
		Framework: "dotnet", Recipe: "dotnet",
		Evidence:      []DetectionEvidence{},
		NeedsDecision: []string{},
	}
	info := marker.dotnet
	if info == nil || info.chosen == nil {
		candidate.Confidence = ConfidenceLow
		candidate.RecipeIssue = "no .NET project at this root could be read"
		if info != nil && info.choice != "" {
			candidate.RecipeIssue = info.choice
		}
		names := make([]string, 0, len(marker.csprojs))
		for name := range marker.csprojs {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, name), Reason: ".NET project file"})
		}
		return candidate
	}
	project := info.chosen
	targets := "a target framework the recipe cannot read"
	if project.targetText != "" {
		targets = project.targetText
	}
	candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: project.file, Reason: ".NET project targeting " + boundedEvidence(targets)})
	for _, props := range project.props {
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: props, Reason: "Directory.Build.props sets properties for " + project.name})
	}
	if context := info.build.context; context != project.dir {
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: contextLabel(displayDir(context)),
			Reason: boundedEvidenceSentence("published from " + contextLabel(displayDir(context)) + ", which holds the projects it references and the MSBuild, NuGet and SDK files that apply to it")})
	}
	if info.build.sdk.version != "" {
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: info.build.sdk.source, Reason: "pins .NET SDK " + info.build.sdk.version})
	}
	switch project.kind {
	case dotnetKindWeb:
		candidate.Framework = "aspnet"
		candidate.Name = "ASP.NET Core application in " + rootLabel
		candidate.Profile, candidate.Port, candidate.Confidence = ProfileWeb, 8080, ConfidenceHigh
	case dotnetKindBlazorWasm:
		// The browser runs it; nginx serves what publish writes to wwwroot.
		candidate.Framework = "blazor-wasm"
		candidate.Name = "Blazor WebAssembly app in " + rootLabel
		candidate.Profile, candidate.Port, candidate.Confidence = ProfileStatic, 80, ConfidenceHigh
		candidate.OutputDirectory, candidate.SPAFallback = "wwwroot", true
	case dotnetKindWorker:
		candidate.Name = ".NET worker in " + rootLabel
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: project.file, Reason: "Microsoft.NET.Sdk.Worker: a background service"})
	case dotnetKindExe:
		candidate.NeedsDecision = append(candidate.NeedsDecision, "confirm whether this program serves HTTP (web application) or runs as a worker, and its port")
	case dotnetKindTest:
		candidate.Confidence = ConfidenceLow
		candidate.NeedsDecision = append(candidate.NeedsDecision, project.name+" is a test project, not a program; confirm the project to publish")
	case dotnetKindAppHost:
		candidate.Confidence = ConfidenceLow
		candidate.NeedsDecision = append(candidate.NeedsDecision, project.name+" is an Aspire AppHost, which orchestrates the other projects locally; deploy each service project instead")
	default:
		candidate.Confidence = ConfidenceLow
		candidate.NeedsDecision = append(candidate.NeedsDecision, project.name+" builds a library, not a program; confirm the project to publish")
	}
	if len(project.native) > 0 {
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: project.file,
			Reason: "sets " + strings.Join(project.native, ", ") + "; the recipe publishes the framework-dependent dll instead"})
	}
	if project.spaRoot != "" {
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRootDir(project.spaRoot, "package.json"),
			Reason: "publish builds the front end in " + contextLabel(displayDir(project.spaRoot)) + " with npm; the recipe adds Node to the SDK image"})
	}
	if procfileWeb := procfileProcess(marker.procfile, "web"); procfileWeb != "" && rejectPlanSecretLiteral("Procfile web process", procfileWeb) == nil {
		candidate.StartCommand = procfileWeb
		candidate.Profile = ProfileWeb
		if candidate.Port == 0 {
			candidate.Port = 8080
		}
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, "Procfile"), Reason: "web process: " + boundedEvidence(procfileWeb)})
	}
	candidate.DotnetBuild = info.detected()
	candidate.Variables = append(candidate.Variables, info.variables...)
	return candidate
}

// detected is the chosen project as the candidate keeps it for preflight.
func (m *dotnetMarker) detected() *DetectedDotnetBuild {
	project := m.chosen
	build := &DetectedDotnetBuild{
		Project: project.file, Kind: project.kind, Targets: project.targets, TargetText: boundedEvidence(project.targetText),
		MultiTarget: project.multiTarget, SDKPin: m.build.sdk.version, SDKPinFrom: m.build.sdk.source,
		RollForward: m.build.rollForward, Native: project.native, SPARoot: project.spaRoot,
	}
	if m.build.context != project.dir {
		build.Context = contextLabel(displayDir(m.build.context))
	}
	for _, reference := range dotnetReferences(project, m.build.closure) {
		build.References = append(build.References, DetectedDotnetReference{Project: reference.project, Targets: boundedEvidence(reference.targets)})
	}
	return build
}

// toolchainFacts turns what a candidate kept back into what the toolchain
// plan reads.
func (b *DetectedDotnetBuild) toolchainFacts() dotnetToolchainFacts {
	facts := dotnetToolchainFacts{
		project: path.Base(b.Project), targets: b.Targets, targetText: b.TargetText, multi: b.MultiTarget,
		pin: b.SDKPin, pinFrom: b.SDKPinFrom, rollForward: b.RollForward,
	}
	for _, reference := range b.References {
		facts.references = append(facts.references, dotnetReference{project: reference.Project, targets: reference.Targets})
	}
	return facts
}
