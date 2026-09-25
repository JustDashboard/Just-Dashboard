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
		for _, item := range record.Findings {
			switch item.Code {
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
