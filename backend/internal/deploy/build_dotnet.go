package deploy

import (
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// The .NET recipe restores and publishes one project from the directory
// that holds everything it builds with (detect_dotnet.go), running dotnet
// in the project's own directory so global.json resolves the SDK the way it
// does for a developer there, and runs the framework-dependent dll on the
// runtime image of its target, or serves a Blazor WebAssembly app's wwwroot
// with nginx.

// dotnetToolchainFacts are what the project and its files say about the
// release it targets and the SDK that builds it.
type dotnetToolchainFacts struct {
	project     string
	targets     []string
	targetText  string
	multi       bool
	pin         string
	pinFrom     string
	rollForward string
	// references are the projects it references, directly or through
	// others, with the target frameworks each declares.
	references []dotnetReference
}

// dotnetReference is a referenced project's checkout path and what its
// <TargetFramework> or <TargetFrameworks> says.
type dotnetReference struct {
	project string
	targets string
}

// dotnetToolchainPlan is planDotnetToolchain's decision: the release the
// project is published for, whether publish has to name it and restore
// has to be held to it, and the SDK image tag that builds it.
type dotnetToolchainPlan struct {
	target   string
	explicit bool
	scoped   bool
	sdk      string
	sdkFrom  string
	// pinned names the pin that chose an exact SDK image, for a refusal
	// when Microsoft never published it.
	pinned string
}

func dotnetMajor(release string) int {
	major, _, _ := strings.Cut(release, ".")
	value, _ := strconv.Atoi(major)
	return value
}

// planDotnetToolchain chooses the target and the SDK. The operator's
// release wins and retargets the project when it declares another one;
// otherwise the newest supported target it declares. A global.json pin
// decides the SDK when its roll-forward policy allows it to build that
// target: the exact SDK image for a patch-level policy, the pin's own
// release for one that rolls forward across feature bands.
func planDotnetToolchain(facts dotnetToolchainFacts, override string) (dotnetToolchainPlan, error) {
	var plan dotnetToolchainPlan
	switch {
	case override != "":
		if !slices.Contains(dotnetRecipeVersions, override) {
			return plan, dotnetVersionRefusal(fmt.Sprintf("the .NET recipe builds net8.0, net9.0 and net10.0; the build settings ask for %s", override))
		}
		plan.target = override
	case len(facts.targets) > 0:
		plan.target = facts.targets[len(facts.targets)-1]
	case strings.TrimSpace(facts.targetText) == "" || strings.Contains(facts.targetText, "$("):
		return plan, dotnetVersionRefusal(facts.project + " declares no <TargetFramework> the recipe can read; declare it, or choose the .NET version in the build settings to publish for")
	case len(facts.references) > 0:
		// A retarget would reach the projects it references too (below).
		return plan, dotnetVersionRefusal("the .NET recipe builds portable net8.0, net9.0 and net10.0 targets; " + facts.project + " targets " + strings.TrimSpace(facts.targetText) +
			" — change its <TargetFramework> (choosing the .NET version in the build settings would retarget the projects it references too: " +
			dotnetReferenceList(facts.references) + "), or use a Dockerfile")
	default:
		return plan, dotnetVersionRefusal("the .NET recipe builds portable net8.0, net9.0 and net10.0 targets; " + facts.project + " targets " + strings.TrimSpace(facts.targetText) +
			" — choose a .NET version in the build settings to retarget it, or use a Dockerfile")
	}
	framework := "net" + plan.target
	declared := dotnetFrameworks(facts.targetText)
	plan.explicit = facts.multi || !slices.Contains(declared, framework)
	plan.sdk, plan.sdkFrom = plan.target, "the target framework"
	if facts.pin != "" {
		if err := pinDotnetSDK(&plan, facts); err != nil {
			return plan, err
		}
	}
	// Restore resolves every framework a project declares, and those are
	// the frameworks its references are built for. A framework it does not
	// declare, or a sibling this image cannot restore — one that needs a
	// workload or a newer SDK — holds the restore to the one it publishes,
	// and restore hands that framework to every project it references.
	reason, remedy := "", ""
	if !slices.Contains(declared, framework) {
		reason = "it declares " + strings.TrimSpace(facts.targetText)
		remedy = "set <TargetFramework>" + framework + "</TargetFramework> in " + facts.project + " itself instead of choosing it in the build settings"
	}
	for _, sibling := range declared {
		if reason == "" && sibling != framework && !dotnetRestorable(sibling, plan.sdk) {
			reason = "the SDK image cannot restore its " + sibling + " target"
			remedy = "add " + framework + " to their target frameworks"
		}
	}
	if reason == "" {
		return plan, nil
	}
	plan.scoped = true
	var mismatched []dotnetReference
	for _, reference := range facts.references {
		// A framework the reader could not resolve is not held against it.
		if frameworks := dotnetFrameworks(reference.targets); len(frameworks) > 0 && !strings.Contains(reference.targets, "$(") &&
			!slices.Contains(frameworks, framework) {
			mismatched = append(mismatched, reference)
		}
	}
	if len(mismatched) > 0 {
		return plan, dotnetVersionRefusal(fmt.Sprintf("the recipe restores %s for %s alone (%s), and restore gives that framework to every project it references: %s would be restored for %s and built for the frameworks they declare (NETSDK1005); %s, or use a Dockerfile",
			facts.project, framework, reason, dotnetReferenceList(mismatched), framework, remedy))
	}
	return plan, nil
}

// pinDotnetSDK chooses the SDK image a global.json or version-manager pin
// allows for the plan's target.
func pinDotnetSDK(plan *dotnetToolchainPlan, facts dotnetToolchainFacts) error {
	match := dotnetSDKVersionRE.FindStringSubmatch(facts.pin)
	if match == nil {
		return nil
	}
	pinned := match[1] + "." + match[2]
	sdk := facts.pin
	switch strings.ToLower(facts.rollForward) {
	case "feature", "latestfeature":
		sdk = pinned
	case "minor", "latestminor", "major", "latestmajor":
		sdk = pinned
		if dotnetMajor(plan.target) > dotnetMajor(pinned) {
			sdk = plan.target
		}
	}
	if match[3] == "" {
		// A version manager naming only major.minor pins that channel.
		sdk = pinned
	}
	if dotnetMajor(sdk) < dotnetMajor(plan.target) {
		return dotnetVersionRefusal(fmt.Sprintf("%s pins .NET SDK %s (rollForward %s), which cannot build net%s; raise the pin or set rollForward to latestMajor", facts.pinFrom, facts.pin, dotnetRollForward(facts.rollForward), plan.target))
	}
	plan.sdk, plan.sdkFrom = sdk, facts.pinFrom+" pins SDK "+facts.pin
	if sdk == facts.pin && match[3] != "" {
		plan.pinned = fmt.Sprintf("%s pins .NET SDK %s (rollForward %s)", facts.pinFrom, facts.pin, dotnetRollForward(facts.rollForward))
	}
	return nil
}

// dotnetSDKPinUnavailable names a pin whose exact SDK image does not exist:
// Microsoft publishes an image only for each patch of the feature band that
// was newest when it shipped — sdk:8.0.100 and sdk:8.0.414, never 8.0.119 —
// so a patch-level pin to an older band has none, and dotnet refuses every
// newer band the image could carry.
func dotnetSDKPinUnavailable(project dotnetProject, err error) error {
	reference := "mcr.microsoft.com/dotnet/sdk:" + project.plan.sdk
	text := strings.ToLower(err.Error())
	if project.plan.pinned == "" || !strings.Contains(err.Error(), "resolve reviewed base image "+reference+":") ||
		(!strings.Contains(text, "manifest unknown") && !strings.Contains(text, "not found")) {
		return err
	}
	return toolchainVersionError{code: "dotnet_sdk_pin_unavailable", text: project.plan.pinned + ", and Microsoft publishes no " + reference +
		" image (only each patch of the feature band that was newest when it shipped has one); set rollForward to latestFeature in global.json, or pin an SDK release that has an image"}
}

// dotnetRollForward is a global.json policy as the SDK applies it.
func dotnetRollForward(policy string) string {
	if policy == "" {
		return "latestPatch"
	}
	return policy
}

// dotnetFrameworks are the target frameworks a <TargetFramework(s)> value
// lists, lowercased.
func dotnetFrameworks(text string) []string {
	var frameworks []string
	for _, framework := range strings.Split(strings.ToLower(text), ";") {
		if framework = strings.TrimSpace(framework); framework != "" {
			frameworks = append(frameworks, framework)
		}
	}
	return frameworks
}

// dotnetRestorable says the SDK image restores a target framework: not one
// newer than the SDK, and not one whose platform needs a workload
// (android, ios, maccatalyst); Windows targets restore anywhere.
func dotnetRestorable(framework, sdk string) bool {
	release, platform, _ := strings.Cut(framework, "-")
	if platform != "" && !strings.HasPrefix(platform, "windows") {
		return false
	}
	match := dotnetVersionRE.FindStringSubmatch(release)
	return match == nil || dotnetMajor(match[1]) <= dotnetMajor(sdk)
}

// dotnetReferences are the projects a project's build references, with the
// frameworks they declare.
func dotnetReferences(chosen *dotnetProjectFacts, closure []*dotnetProjectFacts) []dotnetReference {
	var references []dotnetReference
	for _, project := range closure {
		if project != chosen && dotnetProjectFile(project.name) && len(references) < 64 {
			references = append(references, dotnetReference{project: project.file, targets: project.targetText})
		}
	}
	return references
}

func dotnetReferenceList(references []dotnetReference) string {
	names := make([]string, 0, len(references))
	for _, reference := range references {
		names = append(names, reference.project)
	}
	if len(names) > 4 {
		names = append(names[:4], fmt.Sprintf("%d more", len(references)-4))
	}
	return strings.Join(names, ", ")
}

func dotnetVersionRefusal(text string) error {
	return toolchainVersionError{code: "dotnet_version_unsupported", text: text}
}

// dotnetProject is the project the recipe publishes and how.
type dotnetProject struct {
	// file is the project file's name in its own directory, member that
	// directory under the build context and context the build context.
	file    string
	member  string
	context string
	kind    string
	plan    dotnetToolchainPlan
	// version is the release the project is published for.
	version  string
	web      bool
	assembly string
	native   []string
	// kestrel is where the project's appsettings make Kestrel listen, which
	// the default start command moves onto PORT.
	kestrel kestrelSettings
	// seeds are the committed SQLite files copied into the data directory
	// (recipe_runtime_files.go), relative to the project's directory.
	seeds []string
	spa   *dotnetSPA
}

// dotnetSPA is a front end the project's publish builds with a Node
// package manager: its directory under the build context and the install
// the Node recipe's planner chooses for it.
type dotnetSPA struct {
	dir     string
	plan    nodeInstallPlan
	install bool
}

func selectDotnetRecipe(boundary, root string, config BuildPlanConfig) (dotnetProject, error) {
	files := newBuildFiles(boundary)
	defer files.close()
	reader := newDotnetReader(files)
	dir := checkoutRoot(checkoutPath(boundary, root))
	var projects []*dotnetProjectFacts
	for _, extension := range dotnetProjectExtensions {
		for _, name := range listContainedFiles(root, extension) {
			if facts := reader.project(joinRootDir(dir, name)); facts != nil {
				projects = append(projects, facts)
			}
		}
	}
	if len(projects) == 0 {
		return dotnetProject{}, fmt.Errorf("%w: .NET recipe requires a .csproj, .fsproj or .vbproj file", ErrUnsupportedBuilder)
	}
	chosen, refusal := chooseDotnetProject(projects)
	if chosen == nil {
		return dotnetProject{}, fmt.Errorf("%w: %s", ErrUnsupportedBuilder, refusal)
	}
	switch chosen.kind {
	case dotnetKindTest:
		return dotnetProject{}, fmt.Errorf("%w: %s is a test project; set the root directory to the project to publish", ErrUnsupportedBuilder, chosen.name)
	case dotnetKindAppHost:
		return dotnetProject{}, fmt.Errorf("%w: %s is an Aspire AppHost, which orchestrates other projects on a developer's machine; deploy each service project on its own", ErrUnsupportedBuilder, chosen.name)
	}
	build := reader.build(chosen)
	plan, err := planDotnetToolchain(dotnetToolchainFacts{
		project: chosen.name, targets: chosen.targets, targetText: chosen.targetText, multi: chosen.multiTarget,
		pin: build.sdk.version, pinFrom: build.sdk.source, rollForward: build.rollForward,
		references: dotnetReferences(chosen, build.closure),
	}, config.DotnetVersion)
	if err != nil {
		return dotnetProject{}, err
	}
	project := dotnetProject{
		file: chosen.name, member: relativeBuildPath(build.context, chosen.dir), context: build.context, kind: chosen.kind,
		plan: plan, version: plan.target, web: chosen.kind == dotnetKindWeb, assembly: chosen.assembly, native: chosen.native,
	}
	for _, written := range []string{project.member, project.file, project.assembly} {
		if written != "" && !recipePath(written) {
			return dotnetProject{}, fmt.Errorf("%w: the .NET recipe writes %q into the Dockerfile, and it holds characters it does not quote; rename it or use a Dockerfile", ErrUnsupportedBuilder, written)
		}
	}
	if chosen.spaRoot != "" {
		spa, err := dotnetSPAInstall(boundary, build.context, chosen.spaRoot, config.TargetPlatform)
		if err != nil {
			return dotnetProject{}, err
		}
		project.spa = spa
	}
	if project.web {
		project.kestrel = readKestrelSettings(func(name string) ([]byte, bool) {
			content, err := readContainedRegular(root, name, 512<<10)
			return content, err == nil
		})
	}
	return project, nil
}

// dotnetSPAInstall plans the front end's install with the Node recipe's
// planner, from the lockfile in its own directory. Node is copied into the
// SDK image, which is glibc, so the plan uses the Debian Node image.
func dotnetSPAInstall(boundary, context, spaRoot, platform string) (*dotnetSPA, error) {
	dir := relativeBuildPath(context, spaRoot)
	if dir != "" && !recipePath(dir) {
		return nil, fmt.Errorf("%w: the .NET recipe writes %q into the Dockerfile, and it holds characters it does not quote; use a Dockerfile", ErrUnsupportedBuilder, dir)
	}
	spa := &dotnetSPA{dir: dir}
	source, err := readNodeInstallSource(filepath.Join(boundary, filepath.FromSlash(spaRoot)), "", nodeTargetArch(platform), newNodeReadBudget())
	if err != nil {
		return nil, fmt.Errorf("%w: the .NET recipe reads %s to add the Node release the project's publish builds its front end with, and it is not a regular file of at most %d KiB; use a Dockerfile",
			ErrUnsupportedBuilder, joinRootDir(spaRoot, "package.json"), nodeManifestMax>>10)
	}
	plan := planNodeInstall(source.facts, nodeInstallChoice{assets: true})
	if plan.blocked != nil {
		// A plan the recipe cannot install from stops before it chooses
		// Node; the publish's own npm still runs on the release the front
		// end asks for.
		plan.node = nodeReleaseFor(source.facts)
	}
	plan.family = nodeFamilyGlibc
	if plan.bun != "" {
		plan.bun = bunImage(bunReleaseOf(plan.bun), nodeFamilyGlibc)
	}
	spa.plan, spa.install = plan, plan.blocked == nil
	return spa, nil
}

// dotnetRecipeBases lists the images the recipe resolves, in the order the
// Dockerfile references them: the SDK, the runtime (nginx for a Blazor
// WebAssembly app), then the Node image — and Bun — a front end builds with.
func dotnetRecipeBases(project dotnetProject) []string {
	runtime := "mcr.microsoft.com/dotnet/runtime:" + project.version
	switch {
	case project.kind == dotnetKindBlazorWasm:
		runtime = recipeBaseCatalogue["static"][0]
	case project.web:
		runtime = "mcr.microsoft.com/dotnet/aspnet:" + project.version
	}
	bases := []string{"mcr.microsoft.com/dotnet/sdk:" + project.plan.sdk, runtime}
	if project.spa != nil {
		bases = append(bases, nodeImage(project.spa.plan.node.major, nodeFamilyGlibc))
		if project.spa.plan.bun != "" {
			bases = append(bases, project.spa.plan.bun)
		}
	}
	return bases
}

func dotnetToolchainLabel(project dotnetProject) string {
	label := "dotnet " + project.version
	if project.plan.sdk != project.version {
		label += " (sdk " + project.plan.sdk + ")"
	}
	return label
}

func renderDotnetDockerfile(project dotnetProject, config BuildPlanConfig, bases []ResolvedImage, installSecrets, buildSecrets string) ([]string, error) {
	want := 2
	if project.spa != nil {
		want = 3
		if project.spa.plan.bun != "" {
			want = 4
		}
	}
	if len(bases) != want {
		return nil, ErrBuilderUnavailable
	}
	var lines []string
	if project.spa != nil {
		bun := ""
		if project.spa.plan.bun != "" {
			bun = immutableImageReference(bases[3])
		}
		lines = append(lines, "FROM "+immutableImageReference(bases[2])+" AS spa-node")
		lines = append(lines, project.spa.plan.toolchainLines(bun)...)
	}
	lines = append(lines, "FROM "+immutableImageReference(bases[0])+" AS build")
	if project.spa != nil {
		// The project's publish runs npm itself; Node comes from the
		// catalogue's Debian image, which shares the SDK image's C library.
		lines = append(lines, "COPY --from=spa-node /usr/local/ /usr/local/")
		if len(project.spa.plan.corepack) > 0 {
			lines = append(lines, "COPY --from=spa-node /opt/corepack /opt/corepack")
			for _, line := range project.spa.plan.toolchainLines("") {
				if strings.HasPrefix(line, "ENV ") {
					lines = append(lines, line)
				}
			}
		}
	}
	lines = append(lines, "WORKDIR /src", "COPY . .")
	if project.spa != nil && project.spa.install {
		spaDir := "/src"
		if project.spa.dir != "" {
			spaDir += "/" + project.spa.dir
		}
		lines = append(lines, "WORKDIR "+spaDir, nodeRunWith(installSecrets, project.spa.plan.installDefaults(), project.spa.plan.installLine()))
	}
	workdir := "/src"
	if project.member != "" {
		workdir += "/" + project.member
	}
	if workdir != "/src" || (project.spa != nil && project.spa.install) {
		lines = append(lines, "WORKDIR "+workdir)
	}
	build := strings.TrimSpace(config.BuildCommand)
	restore := "dotnet restore " + project.file
	framework := ""
	if project.plan.explicit {
		// A project with several frameworks, or retargeted to one it does
		// not declare, is published for the one the plan chose.
		framework = " --framework net" + project.version
	}
	if project.plan.scoped {
		restore += " -p:TargetFramework=net" + project.version
	}
	native := ""
	if len(project.native) > 0 {
		// AOT and single-file publishing need the AOT SDK image and write a
		// native executable, not the dll this recipe runs; the JIT,
		// framework-dependent publish runs the same application. Restore
		// and publish must agree on it, or publish finds no assets.
		native = " " + dotnetPublishDisabled
		restore += native
	}
	if build == "" {
		build = "dotnet publish " + project.file + " -c Release --no-restore -o /out" + framework + native
	}
	lines = append(lines, "RUN "+installSecrets+restore, "RUN "+buildSecrets+build)
	if project.kind == dotnetKindBlazorWasm {
		output := strings.TrimSpace(config.OutputDirectory)
		if output == "" {
			output = "wwwroot"
		} else if !validOutputDirectory(output) || (output != "." && !recipePath(output)) {
			return nil, fmt.Errorf("%w: static output directory is invalid", ErrUnsupportedBuilder)
		}
		published := path.Join("/out", output)
		lines = append(lines,
			"RUN test -f "+published+"/index.html || (echo '.NET publish must produce "+published+"/index.html; a Blazor WebAssembly project writes its site there' >&2; exit 1)",
			"FROM "+immutableImageReference(bases[1]))
		lines = append(lines, staticServerLines(config.SPAFallback)...)
		return append(lines, "COPY --from=build "+published+"/ /usr/share/nginx/html/"), nil
	}
	lines = append(lines,
		"RUN test -f /out/"+project.assembly+".dll || (echo '.NET publish must write /out/"+project.assembly+".dll; configure the build command and project together' >&2; exit 1)",
		"FROM "+immutableImageReference(bases[1]),
		"WORKDIR /app",
		// The application runs as the image's app user, so the directory it
		// runs from — where a relative app.db lands — its data directory and
		// the Data Protection key ring are made that user's. A volume mounted
		// on one of them copies that ownership the first time it is used.
		"RUN mkdir -p "+dotnetRuntimeDataDir+" "+dotnetDataProtectionAt+" && chown app:app /app "+dotnetRuntimeDataDir+" /home/app/.aspnet "+dotnetDataProtectionAt,
		"COPY --from=build --chown=app:app /out /app",
	)
	for _, seed := range project.seeds {
		lines = append(lines, "COPY --from=build --chown=app:app "+path.Join(workdir, seed)+" "+dotnetRuntimeDataDir+"/"+path.Base(seed))
	}
	lines = append(lines, "USER app")
	if project.web {
		lines = append(lines, "ENV "+strings.Join(trustEnvironment(dotnetProxyTrust), " "))
	}
	if strings.TrimSpace(config.StartCommand) == "" {
		// Kestrel reads its port from ASPNETCORE_HTTP_PORTS, not PORT; the
		// shell bridges the one the runtime injects.
		bridge, _ := kestrelBridge(project.kestrel)
		lines = append(lines, shellCMD(dotnetRuntimeStart(project.assembly, bridge)))
	} else {
		lines = append(lines, shellCMD(config.StartCommand))
	}
	return lines, nil
}

// dotnetRecipeNotes are the decisions preparation made that the run log
// states.
func dotnetRecipeNotes(project dotnetProject) []string {
	var notes []string
	if project.member != "" {
		notes = append(notes, "Publishes "+project.member+"/"+project.file+" from "+contextLabel(displayDir(project.context))+
			", which holds the projects it references and the MSBuild, NuGet and SDK files that apply to it")
	}
	if project.plan.sdk != project.version {
		notes = append(notes, "Builds with mcr.microsoft.com/dotnet/sdk:"+project.plan.sdk+": "+project.plan.sdkFrom)
	}
	if project.plan.explicit {
		notes = append(notes, "Publishes for net"+project.version)
	}
	if len(project.native) > 0 {
		notes = append(notes, "The project sets "+strings.Join(project.native, ", ")+"; the recipe publishes the framework-dependent dll instead")
	}
	if project.spa != nil && !project.spa.install {
		notes = append(notes, "The recipe does not install the front end in "+contextLabel(project.spa.dir)+" ("+strings.ToLower(project.spa.plan.blocked.Title)+"); the project's own publish runs npm")
	}
	return notes
}

// withoutContextIgnored drops the seeds the build context's .dockerignore
// leaves out, which a COPY from the build stage could not find.
func withoutContextIgnored(context, member string, seeds []string) []string {
	ignored := dockerIgnoredPaths(context)
	kept := seeds[:0:0]
	for _, seed := range seeds {
		if !ignored(member + "/" + seed) {
			kept = append(kept, seed)
		}
	}
	return kept
}
