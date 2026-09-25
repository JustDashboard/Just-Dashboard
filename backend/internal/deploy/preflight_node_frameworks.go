package deploy

import "strings"

// nodeFrameworkFindings judges what detection recorded about the framework —
// an adapter or preset the recipe replaces, an image loader a static export
// cannot use, an entry the start script misses — against the plan as it now
// stands, and names a start command that still runs a development server.
func nodeFrameworkFindings(candidate *DetectedCandidate, configuration PlanConfiguration) []PreflightFinding {
	build := configuration.Build
	if candidate == nil || build.Method != BuildRecipe || build.Recipe != "node" {
		return nil
	}
	findings := []PreflightFinding{}
	static := strings.TrimSpace(build.OutputDirectory) != ""
	start := strings.TrimSpace(build.StartCommand)
	var devScripts map[string]string
	if record := candidate.NodeBuild; record != nil {
		devScripts = record.DevScripts
		asked := map[string]int{}
		for _, item := range record.Findings {
			switch item.Code {
			case "node_decision_open":
				// A question is answered once the setting that answers it
				// no longer holds detection's guess; the questions one
				// setting answers are one finding.
				if !nodeDecisionOpen(candidate, build, item.FieldID) {
					continue
				}
				if index, ok := asked[item.FieldID]; ok {
					findings[index].Measured = boundedFindingText(findings[index].Measured + "; " + item.Measured)
					continue
				}
				asked[item.FieldID] = len(findings)
			case "next_export_images":
				if !static {
					continue
				}
			case "nest_output_layout":
				// The operator's own start command is judged by the recipe's
				// entry check, not by detection's reading of start:prod.
				if static || start != strings.TrimSpace(candidate.StartCommand) {
					continue
				}
			case "sveltekit_adapter_substituted":
				if static {
					continue
				}
			}
			findings = append(findings, item)
		}
	}
	if static || start == "" {
		return findings
	}
	if label, segment := nodeCommandDevServer(start, devScripts); label != "" {
		findings = append(findings, finding("start_command_dev_server", PreflightWarning,
			"The start command runs a development server", boundedFindingText(label+": "+segment),
			"A development server rebuilds on every change and serves unoptimised code; ng serve, vite and astro dev also answer only on localhost, on a port of their own, so the release may never become ready.",
			"Start the production build instead: the framework's own start command, or node on the file the build writes.",
			"deploy", "configuration.build.startCommand"))
	}
	return findings
}

// nodeDecisionOpen says the plan still holds what detection proposed for the
// setting that answers a question: every command and the output directory
// for a question no one setting answers.
func nodeDecisionOpen(candidate *DetectedCandidate, build BuildPlanConfig, field string) bool {
	same := func(detected, planned string) bool { return strings.TrimSpace(detected) == strings.TrimSpace(planned) }
	start, output := same(candidate.StartCommand, build.StartCommand), same(candidate.OutputDirectory, build.OutputDirectory)
	// A plan without a package manager builds with the one detected.
	manager := build.PackageManager == "" || same(candidate.PackageManager, build.PackageManager)
	switch field {
	case "configuration.build.startCommand":
		return start && output
	case "configuration.build.outputDirectory":
		return output
	case "configuration.build.packageManager":
		return manager
	}
	return start && output && manager && same(candidate.BuildCommand, build.BuildCommand)
}

// nodeCommandDevServer is the development server or watcher a start command
// runs, directly or through a package script detection read, and the part
// of the command that starts it.
func nodeCommandDevServer(start string, devScripts map[string]string) (string, string) {
	for _, segment := range strings.Split(start, "&&") {
		segment = strings.TrimSpace(segment)
		if name := scriptSegmentName(segment); name != "" {
			if label := devScripts[name]; label != "" {
				return label, segment
			}
			continue
		}
		if label := nodeDevServerSegment(segment); label != "" {
			return label, boundedEvidence(segment)
		}
	}
	return "", ""
}
