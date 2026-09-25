package deploy

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
)

// Preflight findings for repository shape: what the source is, which of its
// parts this project deploys, and what it will not run. Each is a finding
// before Deploy rather than a cause after a failed build, so the operator
// reads "this repository is an npm library" or "the Celery worker is not
// deployed" on the review screen instead of discovering it later.

// detectionOutcomeFindings says whether detection chose, could not choose, or
// found nothing to deploy — and when what it found is not a service, says
// what it is instead of "nothing was detected".
func detectionOutcomeFindings(detection *DetectionResult, configuration PlanConfiguration) []PreflightFinding {
	notService := []string{}
	for _, item := range detection.SetAside {
		if notDeployableKinds[item.Kind] != "" {
			notService = append(notService, rootLabelOf(item.Path)+": "+item.Reason)
		}
	}
	if len(detection.Candidates) == 0 {
		if len(notService) > 0 {
			return []PreflightFinding{finding("source_not_a_service", PreflightBlocked,
				"This repository is not a service this server can run", boundedText(strings.Join(notService, "; "), 512),
				"What detection found builds for another platform or is used by other programs; there is no process to serve.",
				"Choose a different repository, or commit a Dockerfile that runs a server.", "deploy", "configuration.build.method")}
		}
		measured := ""
		if len(detection.SetAside) > 0 {
			reasons := []string{}
			for index, item := range detection.SetAside {
				if index == 3 {
					break
				}
				reasons = append(reasons, item.Path+" ("+item.Reason+")")
			}
			measured = boundedText("set aside: "+strings.Join(reasons, "; "), 512)
		}
		return []PreflightFinding{finding("detection_empty", PreflightBlocked,
			"No deployable plan was detected", measured, "There is no build/runtime candidate to review.",
			"Choose a build method and configuration.", "deploy", "configuration.build.method")}
	}
	allNotDeployable := true
	for _, candidate := range detection.Candidates {
		allNotDeployable = allNotDeployable && candidate.NotDeployable != ""
	}
	selected := selectedDetectionCandidate(detection)
	if allNotDeployable {
		candidate := detection.Candidates[0]
		if selected != nil {
			candidate = *selected
		}
		return []PreflightFinding{notAServiceFinding(candidate, notAServiceSeverity(candidate, configuration))}
	}
	if selected == nil {
		return []PreflightFinding{finding("detection_ambiguous", PreflightDecision,
			"Choose one detected candidate", detectionSelectionMeasured(detection, fmt.Sprintf("%d candidates", len(detection.Candidates))),
			"Multiple equally strong roots or methods were found.", "Select the intended root and method.",
			"deploy", "detection.selectedId")}
	}
	// The ranking's own reason (detect_ranking.go); a detection saved before
	// it recorded one is explained from the candidates.
	findings := []PreflightFinding{finding("detection_selected", PreflightPass,
		"Detected plan selected", boundedText(detectionSelectionMeasured(detection, selectionReason(*selected, detection.Candidates)), 512),
		"The build plan has explicit evidence.", "", "deploy", "detection")}
	if selected.NotDeployable != "" {
		findings = append(findings, notAServiceFinding(*selected, notAServiceSeverity(*selected, configuration)))
	}
	return findings
}

// notAServiceSeverity blocks a source that is not a service until the
// operator says how it runs anyway — a start command of their own, or a
// build method other than the one detection judged. The verdict then stays
// visible as a warning but no longer stands between them and the deploy:
// detection can be wrong, and a src-layout bot is not a library.
func notAServiceSeverity(candidate DetectedCandidate, configuration PlanConfiguration) PreflightSeverity {
	build := configuration.Build
	if command := strings.TrimSpace(build.StartCommand); command != "" && command != candidate.StartCommand {
		return PreflightWarning
	}
	if build.Method != "" && build.Method != candidate.BuildMethod {
		return PreflightWarning
	}
	return PreflightBlocked
}

func notAServiceFinding(candidate DetectedCandidate, severity PreflightSeverity) PreflightFinding {
	what := notDeployableKinds[candidate.NotDeployable]
	measured := rootLabelOf(candidate.Root) + " is " + what
	for _, evidence := range candidate.Evidence {
		if strings.Contains(evidence.Reason, ":") && (strings.Contains(evidence.Reason, "library") || strings.Contains(evidence.Reason, "extension") ||
			strings.Contains(evidence.Reason, "application") || strings.Contains(evidence.Reason, "tool") || strings.Contains(evidence.Reason, "Action") ||
			strings.Contains(evidence.Reason, "notebook") || strings.Contains(evidence.Reason, "Windows")) {
			measured = evidence.Path + ": " + evidence.Reason
			break
		}
	}
	return finding("source_not_a_service", severity,
		"This repository is "+what+"; there is no process to serve", boundedText(measured, 512),
		"Deploying it would build successfully and then exit, restart, or fail on a call nothing here can answer.",
		"Pick the example or docs app if the repository has one, choose a different repository, or set the start command of a server it really contains.",
		"deploy", "configuration.build")
}

// selectionReason says why the selected candidate won, so a pass reads as a
// judgement and not only an id.
func selectionReason(selected DetectedCandidate, candidates []DetectedCandidate) string {
	reason := selected.Name + " (" + string(selected.Confidence) + " confidence)"
	others := []string{}
	for _, candidate := range candidates {
		if candidate.ID == selected.ID {
			continue
		}
		switch {
		case candidate.NotDeployable != "":
			others = append(others, rootLabelOf(candidate.Root)+" is "+notDeployableKinds[candidate.NotDeployable])
		case candidate.Demotion != "":
			others = append(others, candidate.Demotion)
		default:
			others = append(others, rootLabelOf(candidate.Root)+" has "+string(candidate.Confidence)+" confidence")
		}
		if len(others) == 3 {
			break
		}
	}
	if len(others) > 0 {
		reason += "; ranked above: " + strings.Join(others, "; ")
	}
	return reason
}

// gitRequirementFindings replaces the blanket submodule and LFS decisions:
// only what lies under the build root matters, a submodule on the source's
// own host is fetched with its access, and LFS needs git-lfs on this host.
func gitRequirementFindings(source *DraftSourceConfig, detection *DetectionResult, configuration PlanConfiguration, observation HostObservation) []PreflightFinding {
	var findings []PreflightFinding
	requirements := detection.GitRequirements
	root := strings.Trim(configuration.Build.RootDirectory, "/")
	if requirements.Submodules {
		needed, elsewhere := []string{}, []string{}
		for _, submodule := range requirements.SubmoduleList {
			if !submoduleInRoot(submodule, root) {
				continue
			}
			needed = append(needed, submodule.Path)
			if !submodule.SameSource {
				elsewhere = append(elsewhere, submodule.Path)
			}
		}
		switch {
		case !requirements.SubmodulesChecked:
			severity, action := PreflightDecision, "Choose whether required submodules should be fetched."
			if source.IncludeSubmodules {
				severity, action = PreflightPass, ""
			}
			findings = append(findings, finding("git_submodules", severity,
				"Repository declares Git submodules", fmt.Sprintf("included: %t", source.IncludeSubmodules),
				"Bounded detection does not fetch submodule repositories or their credentials.", action, "git", "source.includeSubmodules"))
		case len(needed) == 0:
			findings = append(findings, finding("git_submodules", PreflightPass,
				"Declared submodules are outside the build root", fmt.Sprintf("%d declared", len(requirements.SubmoduleList)),
				"Nothing this project builds is inside a submodule.", "", "git", "source.includeSubmodules"))
		case source.IncludeSubmodules:
			findings = append(findings, finding("git_submodules", PreflightPass,
				"Git submodules are fetched with the source", strings.Join(needed, ", "),
				"The release checks out every submodule the build root holds.", "", "git", "source.includeSubmodules"))
		case len(elsewhere) > 0:
			findings = append(findings, finding("git_submodules", PreflightDecision,
				"A submodule on another host needs a decision", boundedText(strings.Join(elsewhere, ", "), 512),
				"Its repository is not reached with this source's own access, and the build root holds it.",
				"Include submodules (the source's credential must reach that host), or leave them out if the build does not need them.",
				"git", "source.includeSubmodules"))
		default:
			findings = append(findings, finding("git_submodules", PreflightWarning,
				"Git submodules will not be fetched", boundedText(strings.Join(needed, ", "), 512),
				"These directories will be empty in the build, although they are on the repository's own host.",
				"Turn on submodules in the Source section unless the build does not need them.", "git", "source.includeSubmodules"))
		}
	}
	if requirements.LFS {
		tracked, exact := lfsFilesUnder(requirements, root)
		available := observation.Facilities["git-lfs"].Available
		count := fmt.Sprintf("%d LFS file(s) under the build root", tracked)
		if !exact {
			// Past the listed paths a nested root's count is a floor, and a
			// zero is not a reason to pass.
			count = fmt.Sprintf("at least %d of the repository's %d LFS files under the build root; only %d are listed",
				tracked, requirements.LFSFiles, len(requirements.LFSPaths))
		}
		switch {
		case !requirements.LFSChecked:
			severity, action := PreflightDecision, "Choose whether required Git LFS objects should be fetched."
			if source.IncludeLFS {
				severity, action = PreflightPass, ""
			}
			findings = append(findings, finding("git_lfs", severity,
				"Repository declares Git LFS objects", fmt.Sprintf("included: %t", source.IncludeLFS),
				"Bounded detection skips LFS object downloads.", action, "git", "source.includeLfs"))
		case tracked == 0 && exact:
			findings = append(findings, finding("git_lfs", PreflightPass,
				"No Git LFS files under the build root", "", "The LFS patterns match nothing this project builds.", "", "git", "source.includeLfs"))
		case source.IncludeLFS && !available:
			findings = append(findings, finding("git_lfs_unavailable", PreflightBlocked,
				"git-lfs is not installed on this host", count,
				"The release would stop at acquiring the source: Git cannot download LFS objects without git-lfs.",
				"Install git-lfs on the server (apt install git-lfs), or turn LFS off to deploy the pointer files.", "git", "source.includeLfs"))
		case source.IncludeLFS:
			findings = append(findings, finding("git_lfs", PreflightPass,
				"Git LFS objects are downloaded with the source", count,
				"git-lfs is installed and the release pulls the objects.", "", "git", "source.includeLfs"))
		default:
			title := "Git LFS files will deploy as pointer files"
			if tracked == 0 {
				title = "Git LFS files may deploy as pointer files"
			}
			findings = append(findings, finding("git_lfs", PreflightWarning, title, count,
				"Each LFS-tracked file holds a few lines of text naming the object instead of its content.",
				"Turn on LFS in the Source section unless the build does not need these files.", "git", "source.includeLfs"))
		}
	}
	return findings
}

// repoShapeFindings are the findings about the selected candidate's shape.
func repoShapeFindings(detection *DetectionResult, configuration PlanConfiguration) []PreflightFinding {
	var findings []PreflightFinding
	if len(detection.Alternatives) > 0 {
		alternative := detection.Alternatives[0]
		code := "source_has_packaged_release"
		if alternative.Kind == "image" {
			code = "source_publishes_image"
		}
		title := alternative.Label + " has a reviewed template"
		action := "Deploy it from the Template tab instead, unless you mean to build and run this checkout yourself."
		if alternative.Kind == "image" {
			title = "The project publishes its own image: " + alternative.Label
			action = "Deploy the published image from the Docker image tab instead, unless you mean to build this checkout."
		}
		findings = append(findings, finding(code, PreflightWarning, boundedText(title, 256), alternative.Evidence,
			"Building an application's own repository from source runs its development tree; the packaged release is how the project says to run it.",
			action, "deploy", "source"))
	}
	selected := selectedDetectionCandidate(detection)
	method := configuration.Build.Method
	root := strings.Trim(configuration.Build.RootDirectory, "/")
	if method == BuildRecipe || method == BuildDockerfile || method == BuildStatic {
		for _, item := range detection.SetAside {
			if item.Kind == "virtualenv" && underRoot(item.Path, root) {
				findings = append(findings, finding("committed_virtualenv", PreflightWarning,
					"A Python virtualenv is committed to the repository", item.Path,
					"It is copied into the build context and the image, and it was built for another machine.",
					"Remove "+item.Path+"/ from the repository or add it to .dockerignore.", "deploy", "configuration.build"))
				break
			}
		}
	}
	if selected == nil || selected.Root != root {
		return findings
	}
	switch {
	case strings.HasPrefix(selected.Demotion, "static files inside"):
		findings = append(findings, finding("static_candidate_nested", PreflightWarning,
			"These static files belong to an application", selected.Demotion,
			"nginx will serve the folder as it is; the application that normally serves it, and everything it computes, will not run.",
			"Select the application's candidate instead, unless this folder really is a site of its own.", "deploy", "detection.selectedId"))
	case selected.DesktopShell != "":
		findings = append(findings, finding("desktop_frontend_only", PreflightWarning,
			"This is the frontend of a desktop application", selected.Demotion,
			"In a browser, its invoke() calls into the "+frameworkDisplayName(selected.DesktopShell)+" shell have nothing to answer them.",
			"Deploy it only if the frontend works without the desktop shell.", "deploy", "detection.selectedId"))
	case len(selected.Companions) > 0:
		others := make([]string, 0, len(selected.Companions))
		for _, companion := range selected.Companions {
			others = append(others, rootLabelOf(companion))
		}
		findings = append(findings, finding("companion_service_not_deployed", PreflightWarning,
			"The other half of this application is not deployed by this project", "companion in "+strings.Join(others, ", "),
			"The repository is a frontend and an API; one project runs one of them, and the frontend's calls reach the API only when both run.",
			"After this project is created, create a second project from the same repository with root directory "+others[0]+
				", and point the frontend's API URL at this project's address.", "deploy", "detection.selectedId"))
	case selected.Demotion != "":
		findings = append(findings, finding("selected_candidate_demoted", PreflightWarning,
			"The selected candidate is not the repository's application", selected.Demotion,
			"Detection ranks it below the application: it is an example, a docs site or a workspace root.",
			"Select the application's candidate unless this is what you mean to deploy.", "deploy", "detection.selectedId"))
	}
	command := strings.TrimSpace(configuration.Build.StartCommand)
	deployingProcess := false
	for _, process := range selected.Processes {
		deployingProcess = deployingProcess || (process.Command != "" && process.Command == command)
	}
	if !deployingProcess && method != BuildCompose {
		for _, process := range selected.Processes {
			measured := process.Name
			if process.Command != "" {
				measured += ": " + process.Command
			}
			if process.Kind == "release" {
				if strings.Contains(process.Command, "migrat") && (strings.Contains(command, "migrat") || selected.SchemaInStart) {
					continue
				}
				// The release command detection read is planned as a release
				// task, and release_command_unmapped asks for it while it is
				// not (preflight_image.go).
				if sameReleaseCommand(process.Command, selected.ReleaseCommand) || releaseTaskRuns(configuration.Build.ReleaseTasks, process.Command) {
					continue
				}
				findings = append(findings, finding("release_process_not_run", PreflightWarning,
					"The source's release command is not run", boundedText(measured, 512),
					process.Reason+"; nothing in this plan runs it before a release starts.",
					"Add it as a release task that runs in the release image, or chain it in front of the start command (<release> && <start>).",
					"deploy", "configuration.build.releaseTasks"))
				continue
			}
			if solidQueueRunsInPuma(*selected, configuration, process) {
				continue
			}
			action := "After this project is created, create another project from the same repository with the Worker type"
			if process.Command != "" {
				action += " and the start command " + process.Command
			}
			action += ", linked to the same database and variables."
			if process.Kind == "web" {
				action = "Create another project from the same repository for it, with the start command " + process.Command + " and its own port."
			}
			findings = append(findings, finding(variableFindingCode("secondary_process_not_deployed_", process.Name), PreflightWarning,
				"This source defines a "+processKindLabel(process.Kind)+" that this project does not run", boundedText(measured, 512),
				process.Reason+". One project runs one process, so this one never starts unless it has a project of its own.",
				boundedText(action, 512), "deploy", "configuration.build.startCommand"))
		}
	}
	for _, code := range selected.ServerlessCode {
		switch {
		case code.Blocking:
			findings = append(findings, finding("edge_runtime_code_not_deployed", PreflightBlocked,
				"This application's server code runs only on "+serverlessLabel(code.Platform), boundedText(code.Entry+" ("+strings.Join(code.Paths, ", ")+")", 512),
				code.Entry+" is a Worker entry: a container build serves the client files and never runs it, so every API route is missing.",
				"Serve the API from Node (for Hono, @hono/node-server) with a start command, or use a Dockerfile that runs workerd.",
				"deploy", "configuration.build"))
			continue
		case code.Platform == "cloudflare-workers":
			findings = append(findings, finding("edge_runtime_code_not_deployed", PreflightWarning,
				"A Cloudflare Worker entry is not deployed", boundedText(code.Entry+" ("+strings.Join(code.Paths, ", ")+")", 512),
				"The application's own server runs; "+code.Entry+" runs only on Cloudflare Workers, so whatever it alone serves will be missing.",
				"Move what the Worker serves into the application, or deploy the Worker on Cloudflare.", "deploy", "configuration.build"))
			continue
		}
		findings = append(findings, finding("serverless_functions_dropped", PreflightWarning,
			serverlessLabel(code.Platform)+" functions are not deployed", boundedText(strings.Join(code.Paths, ", "), 512),
			"They run on that platform's function runtime; the static site deploys without them, and requests to their routes will not reach any code.",
			"Move them into a server this project runs, or deploy them on their platform.", "deploy", "configuration.build"))
	}
	if len(selected.ImportCaseMismatches) > 0 && (method == BuildRecipe || method == BuildDockerfile) {
		severity := PreflightWarning
		first := selected.ImportCaseMismatches[0]
		if first.Language == "javascript" && method == BuildRecipe && selected.Recipe == "node" {
			severity = PreflightBlocked
		}
		lines := []string{}
		for index, mismatch := range selected.ImportCaseMismatches {
			if index == 3 {
				break
			}
			if mismatch.Language == "php" {
				lines = append(lines, mismatch.Specifier+" is in "+mismatch.File+"; Composer expects "+mismatch.Actual)
			} else {
				lines = append(lines, fmt.Sprintf("%s:%d imports %s; the file is %s", mismatch.File, mismatch.Line, mismatch.Specifier, mismatch.Actual))
			}
		}
		findings = append(findings, finding("import_case_mismatch", severity,
			"An import differs from its file name only in letter case", boundedText(strings.Join(lines, "; "), 512),
			"macOS and Windows resolve it; the Linux build does not, and fails with \"Module not found\" or \"Class not found\".",
			"Rename the import (or the file) so the case matches exactly, and commit the change.", "deploy", "configuration.build"))
	}
	for _, manifest := range selected.PlatformManifests {
		if len(manifest.SystemPackages) > 0 && method == BuildRecipe && !platformPackagesPlanned(manifest.SystemPackages, configuration.Build) {
			findings = append(findings, finding("platform_system_packages_ignored", PreflightWarning,
				"The repository asks for system packages the recipe does not install", boundedText(strings.Join(manifest.SystemPackages, ", "), 512),
				manifest.File+" installs them on its platform; the automatic recipe's image carries only its language toolchain.",
				"Use a Dockerfile that installs them if the build or the application needs them.", "deploy", "configuration.build.method"))
		}
		if manifest.Redirects > 0 && (selected.Profile == ProfileStatic || method == BuildStatic) {
			findings = append(findings, finding("static_redirects_unsupported", PreflightWarning,
				"Redirect rules are not applied", fmt.Sprintf("%d rule(s) in %s", manifest.Redirects, manifest.File),
				"The static server here applies the single-page fallback only; other redirects and rewrites from another platform's file are ignored.",
				"Serve the redirects from the application, or accept that those paths answer 404.", "deploy", "configuration.build"))
		}
	}
	return findings
}

func processKindLabel(kind string) string {
	switch kind {
	case "scheduler":
		return "scheduler"
	case "web":
		return "second server"
	}
	return "background worker"
}

// releaseTaskRuns says a planned release task runs the command.
func releaseTaskRuns(tasks []ReleaseTaskConfig, command string) bool {
	for _, task := range tasks {
		if sameReleaseCommand(task.Command, command) {
			return true
		}
	}
	return false
}

// sameReleaseCommand compares two spellings of one command: fly.toml names
// a release's script by its path in the image (/app/bin/migrate), where the
// Phoenix release overlay gives the path a release task runs from the
// image's working directory (bin/migrate).
func sameReleaseCommand(a, b string) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	if strings.HasPrefix(b, "/") {
		a, b = b, a
	}
	return a == b || (strings.HasPrefix(a, "/") && !strings.HasPrefix(b, "/") && strings.HasSuffix(a, "/"+b))
}

// solidQueueRunsInPuma says a Solid Queue worker's jobs already run inside
// the web server: Rails 8's puma.rb loads the plugin whenever
// SOLID_QUEUE_IN_PUMA is set, which the plan declares by default
// (detect_variables.go), so bin/jobs has nothing left to run.
func solidQueueRunsInPuma(candidate DetectedCandidate, configuration PlanConfiguration, process DetectedProcess) bool {
	if !solidQueueCommandRE.MatchString(process.Command) {
		return false
	}
	readByPuma := false
	for _, variable := range candidate.Variables {
		if variable.Name == "SOLID_QUEUE_IN_PUMA" {
			for _, source := range variable.Sources {
				readByPuma = readByPuma || path.Base(source) == "puma.rb"
			}
		}
	}
	for _, variable := range configuration.Variables {
		// Ruby reads any string as true, so any value turns the plugin on.
		if variable.Name == "SOLID_QUEUE_IN_PUMA" {
			return readByPuma && (variable.Value != "" || variable.Reference != "")
		}
	}
	return false
}

var solidQueueCommandRE = regexp.MustCompile(`(?:^|[\s/])bin/jobs\b|\bsolid_queue:start\b`)

// platformPackagesPlanned says the Python recipe installs every system
// package another platform's file asks for: the plan carries them, under
// the name Debian trixie gives each.
func platformPackagesPlanned(packages []string, build BuildPlanConfig) bool {
	if build.Recipe != "python" {
		return false
	}
	for _, name := range packages {
		if renamed := aptTrixieNames[name]; renamed != "" {
			name = renamed
		}
		if !slices.Contains(build.SystemPackages, name) {
			return false
		}
	}
	return true
}
