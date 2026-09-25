package deploy

import (
	"path"
	"slices"
	"strings"
)

// What a JavaScript package candidate records beyond its commands: the
// framework's findings for preflight, the scripts that start development
// servers, and the shapes that make a package something other than a plain
// service — a Meteor application, a workspace root, an entry only Bun runs,
// an Nx workspace's application projects.

// withNodeFrameworkFacts adds the framework's findings and the package's
// development scripts to the build record, creating it when the build
// record itself had nothing to say.
func withNodeFrameworkFacts(record *DetectedNodeBuild, findings []PreflightFinding, devScripts map[string]string) *DetectedNodeBuild {
	if len(findings) == 0 && len(devScripts) == 0 {
		return record
	}
	if record == nil {
		record = &DetectedNodeBuild{}
	}
	findings = append(record.Findings, findings...)
	if len(findings) > nodeFrameworkFindingsKept {
		findings = findings[:nodeFrameworkFindingsKept]
	}
	record.Findings = findings
	if devScripts != nil {
		record.DevScripts = devScripts
	}
	return record
}

// nodeFrameworkFindingsKept bounds the framework findings a candidate carries.
const nodeFrameworkFindingsKept = 8

// nodeDecisionFindings carries the framework's open questions to preflight,
// each with the build setting that answers it, so a project deployed again
// later is asked them before Deploy as well, not only when it was created.
func nodeDecisionFindings(decisions []string) []PreflightFinding {
	findings := make([]PreflightFinding, 0, len(decisions))
	for _, decision := range decisions {
		findings = append(findings, nodeFinding("node_decision_open", PreflightWarning,
			"Detection left a question about this application open", decision,
			"The repository does not settle it, so the plan runs detection's best guess, which may not build or serve.",
			"Answer it in the build settings, or change the repository so its configuration says it.", nodeDecisionField(decision)))
	}
	return findings
}

// nodeDecisionField is the build setting whose value answers a question.
func nodeDecisionField(decision string) string {
	switch {
	case strings.Contains(decision, "package manager"):
		return "configuration.build.packageManager"
	case strings.Contains(decision, "output directory"):
		return "configuration.build.outputDirectory"
	case strings.Contains(decision, "start"), strings.Contains(decision, "entry"):
		return "configuration.build.startCommand"
	}
	return "configuration.build"
}

// addNodeDecision asks a question after the build record is made.
func addNodeDecision(candidate *DetectedCandidate, decision string) {
	candidate.NeedsDecision = append(candidate.NeedsDecision, decision)
	candidate.NodeBuild = withNodeFrameworkFacts(candidate.NodeBuild, nodeDecisionFindings([]string{decision}), nil)
}

// applyNodePackageShape marks what the package is when it is not a plain
// service: a Meteor application, which the recipe refuses; a workspace root
// with nothing of its own to start, which is offered but never chosen over
// its members; a start command that runs an entry only Bun serves on a
// runtime that is not Bun.
func applyNodePackageShape(candidate *DetectedCandidate, install *nodeInstallSource, files nodeRootFiles, framework bool) {
	if files.has(".meteor/release") {
		candidate.Framework = "meteor"
		candidate.Confidence = ConfidenceLow
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(candidate.Root, ".meteor/release"), Reason: "a Meteor application"})
		return
	}
	if !framework && install.workspace && install.context == install.dir && strings.TrimSpace(candidate.StartCommand) == "" &&
		candidate.OutputDirectory == "" && candidate.Demotion == "" {
		candidate.Demotion = "workspace root; the applications are its member packages"
	}
	runner := firstNonEmpty(candidate.PackageManager, "npm")
	if runner == "bun" || candidate.OutputDirectory != "" {
		return
	}
	if candidate.Framework == "elysia" {
		addNodeDecision(candidate, "Elysia serves through Bun's own server; choose Bun as the package manager, or add @elysiajs/node")
		candidate.Confidence = minConfidence(candidate.Confidence, ConfidenceMedium)
		return
	}
	file := nodeStartedFile(candidate.StartCommand)
	if head, ok := files.entryHeads[file]; ok && nodeBunOnlyEntry(head) {
		addNodeDecision(candidate, file+" exports a fetch handler, which only Bun serves by itself; choose Bun as the package manager, or serve it with @hono/node-server")
		candidate.Confidence = minConfidence(candidate.Confidence, ConfidenceMedium)
	}
}

// frameworkSchemaTool is the migration step of a framework that owns its
// migrations, in the shape detectSchemaTool gives a package's own tool.
func frameworkSchemaTool(schema *nodeFrameworkSchema, root string) *detectedSchemaTool {
	if schema == nil {
		return nil
	}
	tool := schemaToolByName(schema.tool)
	if tool == nil {
		return nil
	}
	return &detectedSchemaTool{Tool: *tool, Command: schema.command, owned: true, Evidence: DetectionEvidence{
		Path: joinRoot(root, schema.file), Reason: boundedEvidenceSentence(tool.Label + " migrations, applied before the server starts: " + schema.command),
	}}
}

func minConfidence(current, cap DetectionConfidence) DetectionConfidence {
	if confidenceRank(current) > confidenceRank(cap) {
		return cap
	}
	return current
}

// nxCandidates makes each application project of an Nx workspace a
// candidate at the workspace root, whose lockfile installs it and whose own
// Nx builds it. base is the root package's candidate as far as its install;
// the root itself is not offered, since its dependencies are every
// application's.
func nxCandidates(marker *detectedMarkers, base DetectedCandidate, facts nodeInstallFacts, files nodeRootFiles, settled bool, schema *detectedSchemaTool) []DetectedCandidate {
	runner := firstNonEmpty(base.PackageManager, "npm")
	var manifest nodeManifest
	parseNodeManifest(marker.packageJSON, &manifest)
	withSchema := func(manager, start string) string {
		if schema == nil || schema.Command == "" || start == "" || schema.Tool.applied(start) {
			return start
		}
		return nodeSchemaStep(manager, schema.Tool, schema.Command) + " && " + start
	}
	candidates := []DetectedCandidate{}
	for _, project := range files.nx.projects {
		nx := project.candidate(runner)
		candidate := base
		candidate.Name = project.name
		candidate.Framework = nx.framework
		if candidate.Framework == "" && nx.start != "" {
			candidate.Framework = matchNodeServerLibrary(manifest)
		}
		candidate.BuildCommand, candidate.StartCommand = nx.build, withSchema(runner, nx.start)
		candidate.OutputDirectory, candidate.SPAFallback = nx.output, nx.spa
		candidate.Evidence = append(slices.Clone(base.Evidence), DetectionEvidence{
			Path: joinRoot(marker.root, path.Join(project.root, "project.json")), Reason: boundedEvidenceSentence(nx.evidence),
		})
		candidate.NeedsDecision = slices.Clone(base.NeedsDecision)
		candidate.Confidence = ConfidenceHigh
		switch {
		case nx.output != "":
			candidate.Profile, candidate.Port = ProfileStatic, 80
		case nx.start != "":
			candidate.Profile, candidate.Port = ProfileWeb, nx.port
		default:
			candidate.Profile, candidate.Port = ProfileWorker, 0
		}
		var asked []string
		if nx.decision != "" {
			asked = append(asked, nx.decision)
			candidate.NeedsDecision = append(candidate.NeedsDecision, nx.decision)
			candidate.Confidence = ConfidenceMedium
		}
		if !settled {
			candidate.readingConfidence = candidate.Confidence
			candidate.Confidence = ConfidenceLow
		}
		if schema != nil {
			candidate.SchemaTool, candidate.SchemaCommand = schema.Tool.Name, ""
			if candidate.StartCommand != nx.start {
				candidate.SchemaCommand = schema.Command
			}
		}
		candidate.NodeInstalls = facts.detectedInstalls(base.PackageManager, false, nil, func(manager string) (string, string) {
			commands := project.candidate(manager)
			return commands.build, withSchema(manager, commands.start)
		})
		candidate.NodeBuild = withNodeFrameworkFacts(detectedNodeBuild(facts, candidate.Framework, candidate.BuildCommand), nodeDecisionFindings(asked), nil)
		candidate.Variables = facts.registry
		if _, err := validateNodeRecipeContent(marker.packageJSON, files, BuildPlanConfig{
			Method: BuildRecipe, Recipe: "node", PackageManager: runner, BuildCommand: candidate.BuildCommand,
			StartCommand: candidate.StartCommand, OutputDirectory: candidate.OutputDirectory, SPAFallback: candidate.SPAFallback,
		}); err != nil {
			candidate.RecipeIssue = err.Error()
		}
		candidates = append(candidates, newDetectedCandidate(marker.root, BuildRecipe, candidate))
	}
	return candidates
}
