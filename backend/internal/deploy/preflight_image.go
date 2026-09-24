package deploy

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"sort"
	"strings"
)

// Preflight for what the repository's own container definitions and the
// plan's release tasks will do, from evidence detection already read. Every
// finding here used to be a failed build or a failed release task after a
// green Review.

type imageIssueCopy struct {
	title, means, action, field string
}

var imageIssueFindings = map[string]imageIssueCopy{
	"dockerfile_refused": {"The Dockerfile would be refused at build",
		"A secret-named value written literally into a Dockerfile ends up in an image layer or a process's arguments, so the builder refuses it.",
		"Move the literal into a runtime variable; custom Dockerfiles receive no build values.", "configuration.build.dockerfile"},
	"dockerfile_copy_source_missing": {"The Dockerfile copies a path its build context does not have",
		"BuildKit stops at this COPY with 'not found'.",
		"Commit the file, set the root directory to the directory the Dockerfile's paths are relative to, or remove the COPY.", "configuration.build.rootDirectory"},
	"dockerfile_copy_ignored": {"The Dockerfile copies a path .dockerignore leaves out",
		"BuildKit never sends an ignored path to the builder, so this COPY fails with 'not found'.",
		"Remove the rule from .dockerignore, or stop copying the path.", "configuration.build.dockerfile"},
	"dockerfile_arg_required": {"The Dockerfile needs a build argument it has no default for",
		"FROM cannot resolve its base image without the value, and a custom Dockerfile build receives no build arguments for it.",
		"Give the ARG a default in the Dockerfile.", "configuration.build.dockerfile"},
	"dockerfile_ssh_mount": {"The Dockerfile mounts an SSH agent",
		"Deployment builds never forward an SSH agent, so this RUN fails.",
		"Fetch private dependencies over HTTPS with a scoped credential, or vendor them.", "configuration.build.dockerfile"},
	"dockerfile_standalone_missing": {"The Dockerfile copies Next.js standalone output that is not configured",
		"Without output: 'standalone' in next.config, next build never writes .next/standalone and the COPY fails.",
		"Add output: 'standalone' to next.config, or build with the Next.js recipe instead of the Dockerfile.", "configuration.build.method"},
	"dockerfile_dev_server": {"The Dockerfile starts a development server",
		"A development server is unoptimised, watches files and often listens only on localhost.",
		"Start the production server in the Dockerfile's CMD, or build with the automatic recipe.", "configuration.build.method"},
	"dockerfile_missing": {"There is no Dockerfile for this application",
		"There is no automatic recipe for its language, so the repository's own Dockerfile is the only way to build it.",
		"Commit the Dockerfile the framework's template generates, then run detection again.", "configuration.build.dockerfile"},
	"script_crlf": {"A script the image runs has Windows line endings",
		"The kernel reads its interpreter as 'sh\\r', so executing it fails with 'no such file or directory'.",
		"Convert the file to LF line endings (git add --renormalize, or dos2unix) and commit it.", "configuration.build"},
	"script_not_executable": {"A script the image runs is committed without its executable bit",
		"Executing it directly fails with 'permission denied' (exit 126).",
		"Run git update-index --chmod=+x on the file and commit it, or chmod it in the Dockerfile.", "configuration.build"},
	"dockerignore_drops_recipe_input": {"The repository's .dockerignore leaves out a file the automatic build reads",
		"The rule suits the repository's own Dockerfile; the automatic build sets it aside so its inputs are present, and keeps every other rule.",
		"No change is needed for this build.", "configuration.build"},
	"php_htaccess_ignored": {"PHP access rules in .htaccess do not apply",
		"FrankenPHP serves the application directly and does not read .htaccess, so files it was protecting are reachable over HTTP.",
		"Move protected files outside the served directory, or deny them in the application.", "configuration.build.startCommand"},
}

func imageIssueFinding(code string, issues []ImageBuildIssue, prefix string) PreflightFinding {
	copy, known := imageIssueFindings[code]
	if !known {
		copy = imageIssueCopy{"The build has a known problem", "Detection read this from the repository.", "Correct the source and run detection again.", "configuration.build"}
	}
	details := []string{}
	severity := PreflightWarning
	for _, issue := range issues {
		details = append(details, prefix+issue.Detail)
		if issue.Severity == PreflightBlocked {
			severity = PreflightBlocked
		}
	}
	measured := strings.Join(details, "; ")
	if len(measured) > 2000 {
		measured = measured[:1997] + "..."
	}
	return finding(code, severity, copy.title, measured, copy.means, copy.action, "deploy", copy.field)
}

// groupedImageFindings turns issues into one finding per code, in the order
// the codes first appear.
func groupedImageFindings(issues []ImageBuildIssue, prefix string) []PreflightFinding {
	order := []string{}
	byCode := map[string][]ImageBuildIssue{}
	for _, issue := range issues {
		if byCode[issue.Code] == nil {
			order = append(order, issue.Code)
		}
		byCode[issue.Code] = append(byCode[issue.Code], issue)
	}
	findings := make([]PreflightFinding, 0, len(order))
	for _, code := range order {
		findings = append(findings, imageIssueFinding(code, byCode[code], prefix))
	}
	return findings
}

// plannedCandidate is the detected candidate the configuration still builds:
// the same method and root, and for a Dockerfile the same file. Evidence
// about any other file would be evidence about something else.
func plannedCandidate(detection *DetectionResult, build BuildPlanConfig) *DetectedCandidate {
	if detection == nil {
		return nil
	}
	for index := range detection.Candidates {
		candidate := &detection.Candidates[index]
		if candidate.BuildMethod != build.Method || candidate.Root != build.RootDirectory {
			continue
		}
		switch build.Method {
		case BuildDockerfile:
			configured := build.Dockerfile
			if configured == "" {
				configured = "Dockerfile"
			}
			if path.Clean(configured) == path.Clean(candidate.Dockerfile) {
				return candidate
			}
		case BuildRecipe:
			if build.Recipe == "" || build.Recipe == candidate.Recipe {
				return candidate
			}
		default:
			return candidate
		}
	}
	return nil
}

func imageBuildFindings(draft *Draft, configuration PlanConfiguration, observation HostObservation) []PreflightFinding {
	findings := []PreflightFinding{}
	detection := draft.Data.Detection
	build := configuration.Build
	planned := plannedCandidate(detection, build)
	if planned != nil {
		issues := []ImageBuildIssue{}
		for _, issue := range planned.ImageBuildIssues {
			// A script found through the start command is evidence about that
			// command only.
			if planned.BuildMethod == BuildRecipe && strings.HasPrefix(issue.Code, "script_") && build.StartCommand != planned.StartCommand {
				continue
			}
			issues = append(issues, issue)
		}
		findings = append(findings, groupedImageFindings(issues, "")...)
	} else if build.Method == BuildDockerfile && detectionReadDockerfiles(detection) {
		configured := build.Dockerfile
		if configured == "" {
			configured = "Dockerfile"
		}
		measured := joinRoot(build.RootDirectory, configured)
		if build.Target != "" {
			measured += ", stage " + build.Target
		}
		findings = append(findings, finding("dockerfile_unchecked", PreflightWarning,
			"The chosen Dockerfile is checked only when it builds", measured,
			"Detection read the repository's Dockerfiles as they were found; this combination of file and build context was not one of them.",
			"Choose the detected Dockerfile and root directory, or accept that problems in this file surface during the build.",
			"deploy", "configuration.build.dockerfile"))
	}
	if build.Method == BuildRecipe || build.Method == BuildStatic {
		for _, candidate := range detection.Candidates {
			if candidate.BuildMethod != BuildDockerfile || candidate.Root != build.RootDirectory || candidate.DockerfileRole == DockerfileRoleDevelopment {
				continue
			}
			if issue, blocked := candidate.blockingImageIssue(); blocked {
				findings = append(findings, finding("dockerfile_not_selected", PreflightWarning,
					"The repository's Dockerfile is not the build", joinRoot(candidate.Root, candidate.Dockerfile)+": "+issue.Detail,
					"The Dockerfile would not build as committed, so the automatic recipe builds this application instead.",
					"Fix the Dockerfile and choose it, or keep the automatic build.", "deploy", "configuration.build.method"))
				break
			}
		}
	}
	if build.Method == BuildDockerfile && planned != nil {
		if build.Target != "" && !slicesContain(planned.DockerfileStages, strings.ToLower(build.Target)) {
			stages := "it names no stages"
			if len(planned.DockerfileStages) > 0 {
				stages = "its stages are " + strings.Join(planned.DockerfileStages, ", ")
			}
			findings = append(findings, finding("dockerfile_target_missing", PreflightBlocked,
				"The Dockerfile has no stage by that name", build.Target+": "+stages,
				"buildx stops before building anything when --target names a stage the file does not have.",
				"Choose one of the Dockerfile's stages, or clear the stage to build the last one.", "deploy", "configuration.build.target"))
		}
		findings = append(findings, dockerfileArgFindings(*planned, configuration)...)
		for _, platform := range planned.DockerfilePlatforms {
			if item, ok := foreignArchitectureFinding(platform, "FROM --platform in "+joinRoot(planned.Root, planned.Dockerfile), observation); ok {
				findings = append(findings, item)
			}
		}
	}
	if build.Method == BuildRecipe && build.Recipe == "php" && phpDocumentRoot(build.StartCommand) == "" {
		findings = append(findings, finding("php_docroot_is_repository_root", PreflightWarning,
			"PHP serves the whole repository", "--root /app",
			"Dependencies, lockfiles, logs and dotfiles under the served directory are reachable over HTTP, and any PHP file in vendor/ can be executed.",
			"Move the entry point under public/, or serve the framework's own document root.", "deploy", "configuration.build.startCommand"))
	}
	findings = append(findings, composeBuildFindings(detection, configuration, observation)...)
	findings = append(findings, pastedComposeBuildFindings(draft.Data.Source, detection, configuration)...)
	findings = append(findings, releaseTaskFindings(planned, detection, configuration, observation)...)
	return findings
}

// dockerfileArgFindings: a custom Dockerfile build receives a build argument
// only for a plain, browser-public build variable the Dockerfile declares.
func dockerfileArgFindings(candidate DetectedCandidate, configuration PlanConfiguration) []PreflightFinding {
	planned := map[string]PlannedVariable{}
	for _, variable := range configuration.Variables {
		planned[variable.Name] = variable
	}
	passed, unpassed := []string{}, []string{}
	for _, arg := range candidate.DockerfileArgs {
		variable, declared := planned[arg.Name]
		buildScoped := declared && slicesContain(variable.Scopes, "build")
		switch {
		case buildScoped && variable.Sensitivity == "plain" && publicBuildVariable(arg.Name) && !reservedBuildArgName(arg.Name):
			passed = append(passed, arg.Name)
		case buildScoped && variable.Sensitivity != "plain" && publicBuildVariable(arg.Name):
			unpassed = append(unpassed, arg.Name+" (marked secret, and a secret never becomes a build argument)")
		case buildScoped:
			unpassed = append(unpassed, arg.Name+" (a build variable, but only plain browser-public ones are passed)")
		case publicBuildVariable(arg.Name) && arg.Consumed && !arg.HasDefault:
			unpassed = append(unpassed, arg.Name+" (no build-scoped variable of that name)")
		}
	}
	findings := []PreflightFinding{}
	if len(passed) > 0 {
		findings = append(findings, finding("dockerfile_build_args", PreflightPass,
			"The Dockerfile receives its public build arguments", strings.Join(passed, ", "),
			"Each is a plain build variable the Dockerfile declares with ARG, passed as --build-arg with the value only in the builder's environment.",
			"", "deploy", "variables"))
	}
	if len(unpassed) > 0 {
		findings = append(findings, finding("dockerfile_arg_not_passed", PreflightWarning,
			"A Dockerfile build argument will be empty", strings.Join(unpassed, "; "),
			"Custom Dockerfiles receive build values only for plain NEXT_PUBLIC_, VITE_, PUBLIC_, NUXT_PUBLIC_, REACT_APP_ or EXPO_PUBLIC_ variables they declare — the page's JavaScript carries those by design, so a value typed with such a name is kept plain; anything else is empty during the build.",
			"Give the ARG a default, make a browser-public variable plain and build-scoped, or build with the automatic recipe, which passes build values as BuildKit secrets.",
			"deploy", "variables"))
	}
	return findings
}

func slicesContain(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

// dockerArchitectureQemu maps a Docker architecture to the name binfmt_misc
// registers its emulator under.
var dockerArchitectureQemu = map[string]string{
	"amd64": "x86_64", "arm64": "aarch64", "arm": "arm", "386": "i386", "ppc64le": "ppc64le",
	"s390x": "s390x", "riscv64": "riscv64", "mips64le": "mips64el", "loong64": "loongarch64",
}

// foreignArchitectureFinding: an image pinned to another architecture builds
// and runs only through emulation, and without an emulator every RUN fails
// with 'exec format error'.
func foreignArchitectureFinding(platform, where string, observation HostObservation) (PreflightFinding, bool) {
	parts := strings.Split(strings.ToLower(platform), "/")
	if len(parts) < 2 || observation.Architecture == "" || parts[1] == observation.Architecture {
		return PreflightFinding{}, false
	}
	measured := fmt.Sprintf("%s pins %s; this host is %s/%s", where, platform, observation.OS, observation.Architecture)
	emulated := slicesContain(observation.Emulators, parts[1])
	if observation.EmulationObserved && !emulated {
		return finding("foreign_architecture_build", PreflightBlocked,
			"The image is for another architecture and this host cannot emulate it", measured,
			"Without a binfmt emulator for "+parts[1]+", every RUN step and the container itself fail with 'exec format error'.",
			"Remove --platform, or use $BUILDPLATFORM/$TARGETPLATFORM so the image builds for this host.", "docker", "configuration.build.dockerfile"), true
	}
	return finding("foreign_architecture_build", PreflightWarning,
		"The image is for another architecture", measured,
		"It builds and runs only under emulation, which is many times slower, if an emulator is installed at all.",
		"Remove --platform, or use $BUILDPLATFORM/$TARGETPLATFORM so the image builds for this host.", "docker", "configuration.build.dockerfile"), true
}

// observeEmulators lists the architectures binfmt_misc can run on this host.
// Reading /proc is the whole observation; nothing is registered or run.
func observeEmulators() ([]string, bool) {
	entries, err := os.ReadDir("/proc/sys/fs/binfmt_misc")
	if err != nil {
		return nil, false
	}
	byQemu := map[string]string{}
	for architecture, qemu := range dockerArchitectureQemu {
		byQemu[qemu] = architecture
	}
	emulators := []string{}
	for _, entry := range entries {
		name := strings.TrimPrefix(entry.Name(), "qemu-")
		architecture, known := byQemu[name]
		if !known || name == entry.Name() {
			continue
		}
		content, err := os.ReadFile("/proc/sys/fs/binfmt_misc/" + entry.Name())
		if err == nil && strings.HasPrefix(string(content), "enabled") {
			emulators = append(emulators, architecture)
		}
	}
	sort.Strings(emulators)
	return emulators, true
}

func composeBuildFindings(detection *DetectionResult, configuration PlanConfiguration, observation HostObservation) []PreflightFinding {
	findings := []PreflightFinding{}
	if configuration.Build.Method != BuildCompose {
		return findings
	}
	if detection == nil || detection.Compose == nil {
		// Only a repository's Compose file is found without being analysed;
		// an adopted stack runs through its own compatibility path.
		if detection != nil && (detection.Source.Kind == SourceGit || detection.Source.Kind == SourceLocal) {
			findings = append(findings, finding("compose_analysis_missing", PreflightBlocked,
				"This Compose file has not been analysed as a Compose source", "",
				"A Compose file found in a repository is only named during detection; its services, images and builds are analysed when the repository is deployed as a Compose source.",
				"Deploy this repository as a Compose stack (Compose files in Git), or choose another detected candidate.", "deploy", "source"))
		}
		return findings
	}
	findings = append(findings, composeBuildArgFindings(detection.Compose, configuration)...)
	for _, service := range detection.Compose.Services {
		prefix := "service " + service.Name + ": "
		if service.BuildContextMissing {
			findings = append(findings, finding("compose_build_context_missing", PreflightBlocked,
				"A Compose service builds from a directory the repository does not contain", prefix+service.BuildContext,
				"docker compose cannot build the service; a context under vendor/ or node_modules/ is installed by a package manager, not committed.",
				"Point build.context at a committed directory, or use a published image for the service.", "docker", "source.compose"))
		}
		missing := []string{}
		for _, file := range service.EnvFiles {
			if file.Missing && file.Required {
				missing = append(missing, file.Path)
			}
		}
		if len(missing) > 0 {
			findings = append(findings, finding("compose_env_file_missing", PreflightBlocked,
				"A Compose service reads an env_file the repository does not contain", prefix+strings.Join(missing, ", "),
				"docker compose refuses to start a service whose env_file is missing; a .env is usually ignored by Git on purpose.",
				"Set the service's variables with environment: and ${NAME} references to planned variables, mark the file required: false, or commit it.",
				"docker", "source.compose"))
		}
		findings = append(findings, groupedImageFindings(service.DockerfileIssues, prefix)...)
		if service.Platform != "" && !strings.Contains(service.Platform, "$") {
			if item, ok := foreignArchitectureFinding(service.Platform, "service "+service.Name+" platform:", observation); ok {
				findings = append(findings, item)
			}
		}
		// A service that pins `platform:` to one the image offers is pulled
		// for that platform and runs under emulation; the platform finding
		// above says whether this host can emulate it. Without the pin Docker
		// asks for this host's own platform, which the image does not have.
		pinned := service.Platform != "" && !strings.Contains(service.Platform, "$") &&
			platformListContains(service.ImagePlatforms, strings.ToLower(service.Platform))
		if len(service.ImagePlatforms) > 0 && observation.OS != "" && observation.Architecture != "" && !pinned &&
			!platformListContains(service.ImagePlatforms, observation.OS+"/"+observation.Architecture) {
			action := "Use an image or tag published for " + observation.Architecture + "."
			for _, platform := range service.ImagePlatforms {
				if parts := strings.Split(platform, "/"); len(parts) >= 2 && parts[0] == observation.OS && slicesContain(observation.Emulators, parts[1]) {
					action = "Use an image or tag published for " + observation.Architecture + ", or set platform: " + parts[0] + "/" + parts[1] +
						" on the service to run it under this host's " + parts[1] + " emulator, many times slower."
					break
				}
			}
			findings = append(findings, finding("compose_image_platform_missing", PreflightBlocked,
				"A Compose service image is not published for this host's architecture",
				prefix+service.Image+" offers "+strings.Join(service.ImagePlatforms, ", ")+"; this host is "+observation.OS+"/"+observation.Architecture,
				"Docker asks the registry for this host's platform, which the image does not offer, so the pull fails.",
				action, "docker", "source.compose"))
		}
	}
	primary, err := chosenComposePrimaryService(configuration.Build, *detection.Compose)
	switch {
	case err != nil:
		findings = append(findings, finding("compose_primary_service_missing", PreflightBlocked,
			"The chosen primary service is not in the Compose stack", configuration.Build.PrimaryService,
			"Readiness and the release's container follow the primary service, and the stack has no service by that name.",
			"Choose one of the stack's services, or clear the choice to use the detected one.", "docker", "configuration.build.primaryService"))
	case primary != "" && configuration.Build.PrimaryService != "":
		findings = append(findings, finding("compose_primary_service", PreflightPass,
			"Readiness follows the Compose service you chose", primary,
			"The primary service is the release's container identity and readiness target.",
			"", "docker", "configuration.build.primaryService"))
	case primary != "":
		findings = append(findings, finding("compose_primary_service", PreflightPass,
			"Readiness follows the Compose application service", primary,
			"The service that builds or publishes a port is the release's container identity and readiness target, never a database.",
			"", "docker", "configuration.build.primaryService"))
	}
	return findings
}

// pastedComposeBuildFindings: a pasted or uploaded Compose file arrives
// without the files around it, so a service that builds has no Dockerfile
// and no context to build from — a certain failure only the build used to
// report.
func pastedComposeBuildFindings(source *DraftSourceConfig, detection *DetectionResult, configuration PlanConfiguration) []PreflightFinding {
	if source == nil || (source.Mode != SourceModeComposePaste && source.Mode != SourceModeComposeUpload) ||
		configuration.Build.Method != BuildCompose || detection == nil || detection.Compose == nil {
		return nil
	}
	building := []string{}
	for _, service := range detection.Compose.Services {
		if service.BuildContext != "" {
			building = append(building, service.Name)
		}
	}
	if len(building) == 0 {
		return nil
	}
	return []PreflightFinding{finding("compose_build_without_checkout", PreflightBlocked,
		"A pasted Compose file builds services it has no files for", strings.Join(building, ", "),
		"Only the Compose file itself is kept; the Dockerfile and build context a service builds from are not, so its build fails with 'not found'.",
		"Deploy the repository as a Compose source (Compose files in Git or a local checkout), or give the service a published image.",
		"docker", "source.compose")}
}

// composeBuildArgFindings: a Compose build argument is interpolated from the
// plain runtime and build variables (composeBuildArgValues). One that reads a
// secret would refuse the build; one whose variable is planned for neither
// scope is empty during it. An unplanned variable is already a
// compose_variable_* decision.
func composeBuildArgFindings(compose *ComposeAnalysis, configuration PlanConfiguration) []PreflightFinding {
	planned := map[string]PlannedVariable{}
	for _, variable := range configuration.Variables {
		planned[variable.Name] = variable
	}
	secret, unscoped := []string{}, []string{}
	unscopedSeverity := PreflightWarning
	check := func(service, reader, expression, name string) {
		variable, declared := planned[name]
		switch {
		case !declared:
		case variable.Sensitivity != "plain":
			secret = append(secret, "service "+service+": "+reader+" reads "+name)
		case !slicesContain(variable.Scopes, "runtime") && !slicesContain(variable.Scopes, "build"):
			unscoped = append(unscoped, "service "+service+": "+reader+" reads "+name)
			// `${X:?message}` is the file refusing to build without it.
			if strings.Contains(expression, "${"+name+":?") || strings.Contains(expression, "${"+name+"?") {
				unscopedSeverity = PreflightBlocked
			}
		}
	}
	for _, service := range compose.Services {
		for _, arg := range service.BuildArgs {
			for _, name := range composeArgVariableNames(arg) {
				check(service.Name, "build argument "+arg.Name, arg.Value, name)
			}
		}
		for _, name := range composeExpressionNames(service.BuildTarget) {
			check(service.Name, "build target", service.BuildTarget, name)
		}
	}
	findings := []PreflightFinding{}
	if len(secret) > 0 {
		findings = append(findings, finding("compose_build_arg_secret", PreflightBlocked,
			"A Compose build argument reads a secret variable", strings.Join(secret, "; "),
			"A build argument stays in the image's history, so a secret is never passed as one and the build refuses it.",
			"If the value is public — a URL the page's JavaScript carries — make the variable plain; otherwise take it out of build.args and read it at runtime, or mount it as a BuildKit secret in the Dockerfile.",
			"deploy", "variables"))
	}
	if len(unscoped) > 0 {
		findings = append(findings, finding("compose_build_arg_unscoped", unscopedSeverity,
			"A Compose build argument will be empty", strings.Join(unscoped, "; "),
			"Build arguments are interpolated from the runtime and build variables; this one is planned only for release tasks, and one the file marks ${X:?} stops the build.",
			"Give the variable runtime or build scope.", "deploy", "variables"))
	}
	return findings
}

func releaseTaskFindings(planned *DetectedCandidate, detection *DetectionResult, configuration PlanConfiguration, observation HostObservation) []PreflightFinding {
	findings := []PreflightFinding{}
	imageTask := false
	for index, task := range configuration.Build.ReleaseTasks {
		field := fmt.Sprintf("configuration.build.releaseTasks[%d].command", index)
		if task.Runner == ReleaseTaskRunnerImage {
			imageTask = true
			if configuration.Build.Method == BuildCompose {
				findings = append(findings, composeReleaseTaskFinding(task, detection, fmt.Sprintf("configuration.build.releaseTasks[%d].runner", index)))
			}
			continue
		}
		if token, needs := releaseTaskInstalledTool(task.Command); needs {
			findings = append(findings, finding("release_task_tool_missing", PreflightBlocked,
				"A release task needs the application's installed dependencies", task.Name+": "+token,
				"This task runs in the dashboard's shell over the unbuilt source, where nothing the application installs — node_modules, a virtualenv, a bundle — exists.",
				"Run the task in the release image instead, where the application's toolchain and variables are.", "deploy", field))
			continue
		}
		for _, tool := range releaseTaskHostTools([]ReleaseTaskConfig{task}) {
			if observed, looked := observation.Facilities[releaseTaskToolFacility(tool)]; looked && !observed.Available {
				findings = append(findings, finding("release_task_tool_missing", PreflightBlocked,
					"A release task runs a program this host does not have", task.Name+": "+tool,
					"The task's shell cannot find it, so the release fails with 'not found' (exit 127).",
					"Run the task in the release image instead, or install the program on the host.", "deploy", field))
				break
			}
		}
	}
	if planned != nil && planned.ReleaseCommand != "" && !imageTask {
		findings = append(findings, finding("release_command_unmapped", PreflightWarning,
			"The repository's release command is not planned", planned.ReleaseCommand,
			"The repository declares a command that runs once before each release (usually migrations); without it the application starts against an unmigrated database.",
			"Add it as a release task that runs in the release image.", "deploy", "configuration.build.releaseTasks"))
	}
	return findings
}

// composeReleaseTaskFinding: an image task of a Compose release runs the
// primary service's image on its own, with the environment's runtime
// variables and database networks — outside the Compose project, so neither
// the stack's network nor the service's own environment: entries apply. A
// migration against the stack's own database cannot even resolve its name.
func composeReleaseTaskFinding(task ReleaseTaskConfig, detection *DetectionResult, field string) PreflightFinding {
	backing := []string{}
	if detection != nil && detection.Compose != nil {
		for _, service := range detection.Compose.Services {
			if family, known := composeImageFamily(service.Image); known && service.BuildContext == "" {
				backing = append(backing, service.Name+" ("+family+")")
			}
		}
	}
	const means = "An image task runs the primary service's image once on its own, with the environment's runtime variables and the dashboard's database networks — not on the Compose project's network, and without the service's own environment: entries."
	if len(backing) > 0 {
		return finding("release_task_outside_compose_stack", PreflightBlocked,
			"A release task cannot reach the Compose stack's own services",
			task.Name+": "+strings.Join(backing, ", ")+" run only inside the stack",
			means+" A service name such as db does not resolve from it.",
			"Run the migration from the Compose file — a one-off service the application depends_on with condition: service_completed_successfully, or the service's own command — or point the task's variables at a dashboard database.",
			"deploy", field)
	}
	return finding("release_task_outside_compose_stack", PreflightWarning,
		"A release task runs outside the Compose stack", task.Name,
		means, "Make sure the task needs only the runtime variables, or run it from the Compose file instead.", "deploy", field)
}

func releaseTaskToolFacility(tool string) string { return "release_task_tool:" + tool }

// observeReleaseTaskTools answers whether each program a host task runs is
// on the PATH of the shell that runs it — the dashboard's own, since /bin/sh
// is always found here — as a lookup, never an execution.
func observeReleaseTaskTools(tools []string, facilities map[string]FacilityObservation) {
	for _, tool := range tools {
		_, err := exec.LookPath(tool)
		facilities[releaseTaskToolFacility(tool)] = FacilityObservation{Available: err == nil}
	}
}

// detectionSelectionMeasured is why detection chose what it chose, where it
// said so, instead of a candidate id nobody can read.
func detectionSelectionMeasured(detection *DetectionResult, fallback string) string {
	if detection.SelectionReason != "" {
		return detection.SelectionReason
	}
	return fallback
}

// detectionReadDockerfiles says detection read the repository's Dockerfiles
// itself, so a configured file it has no evidence about is a different one.
func detectionReadDockerfiles(detection *DetectionResult) bool {
	if detection == nil || (detection.Source.Kind != SourceGit && detection.Source.Kind != SourceLocal) {
		return false
	}
	for _, candidate := range detection.Candidates {
		if candidate.BuildMethod == BuildDockerfile && candidate.Dockerfile != "" {
			return true
		}
	}
	return false
}
