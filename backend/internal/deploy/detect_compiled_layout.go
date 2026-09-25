package deploy

import (
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Multi-module JVM builds and .NET solutions are one application built from
// many directories. Each directory with a build file is still read as its
// own root (detector.go), but only the ones that produce something to run
// stay candidates: the application module or project, built from the
// directory that owns it (build_java.go, build_dotnet.go). Library modules,
// aggregator POMs, test projects and an Aspire AppHost are set aside with
// the reason, a Blazor WebAssembly client its host publishes is not a site
// of its own, and neither is an ASP.NET Core project's wwwroot.

// readCompiledProjects reads every JVM and .NET root's build through the
// same readers the recipes use, after the walk and under their own budget.
func readCompiledProjects(checkout string, markers map[string]*detectedMarkers) {
	files := newBuildFiles(checkout)
	defer files.close()
	jvm, dotnet := newJVMReader(files), newDotnetReader(files)
	dirs := make([]string, 0, len(markers))
	for dir := range markers {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		marker := markers[dir]
		root := checkoutRoot(filepath.ToSlash(marker.root))
		if len(marker.pomXML) > 0 || len(marker.gradleBuild) > 0 {
			if project := jvm.project(root); project != nil {
				marker.jvm = project
				// The passes that match a build definition by name — the
				// framework, health modules, database drivers, Android — read
				// what the build resolves: inherited dependencies and the
				// coordinates catalog aliases stand for.
				if len(marker.pomXML) > 0 {
					marker.pomXML = []byte(project.text)
				} else {
					marker.gradleBuild = []byte(project.text)
				}
			}
		}
		if len(marker.csprojs) == 0 {
			continue
		}
		names := make([]string, 0, len(marker.csprojs))
		for name := range marker.csprojs {
			names = append(names, name)
		}
		sort.Strings(names)
		info := &dotnetMarker{}
		for _, name := range names {
			if facts := dotnet.project(joinRootDir(root, name)); facts != nil {
				info.projects = append(info.projects, facts)
			}
		}
		if len(info.projects) > 0 {
			info.chosen, info.choice = chooseDotnetProject(info.projects)
		}
		if info.chosen != nil {
			info.build = dotnet.build(info.chosen)
			info.variables = nugetRegistryCredentials(files, info.build.nuget)
			if info.chosen.kind == dotnetKindAppHost {
				for _, name := range []string{"AppHost.cs", "Program.cs"} {
					if content, ok := files.read(joinRootDir(info.chosen.dir, name), 256<<10); ok {
						info.program = content
						break
					}
				}
			}
		}
		marker.dotnet = info
	}
}

// settleCompiledLayouts keeps the candidates of multi-module builds that
// produce something to run, before the repository-shape passes rank them.
func settleCompiledLayouts(result *DetectionResult, markers map[string]*detectedMarkers, shape *repoShapeScan) {
	drop := map[string]bool{}
	settleJVMLayouts(result, markers, shape, drop)
	settleDotnetLayouts(result, markers, shape, drop)
	removeCandidates(result, func(candidate DetectedCandidate) bool { return drop[candidate.ID] })
	offerSpringProfiles(result, markers)
	addCargoRegistryCredentials(result, markers, shape.root)
}

// addCargoRegistryCredentials gives a Rust candidate the tokens of the
// private registries its .cargo/config declares, which the recipe's cargo
// fetch reads under the install's secret mount.
func addCargoRegistryCredentials(result *DetectionResult, markers map[string]*detectedMarkers, checkout string) {
	var files *buildFiles
	for index := range result.Candidates {
		candidate := &result.Candidates[index]
		marker := markers[filepath.FromSlash(candidate.Root)]
		if candidate.Recipe != "rust" || marker == nil || len(marker.cargoToml) == 0 {
			continue
		}
		if files == nil {
			files = newBuildFiles(checkout)
			defer files.close()
		}
		if variables := cargoRegistryCredentials(files, checkoutRoot(candidate.Root), marker.cargoToml); len(variables) > 0 {
			candidate.Variables = withInstallVariables(candidate.Variables, variables)
		}
	}
}

// offerSpringProfiles adds SPRING_PROFILES_ACTIVE, empty, to a Spring Boot
// root that ships production configuration: whether the application must
// run with it is the operator's call — prod usually needs variables nobody
// has set yet — so the row is the suggestion and its example the profile.
func offerSpringProfiles(result *DetectionResult, markers map[string]*detectedMarkers) {
	for _, candidate := range result.Candidates {
		marker := markers[filepath.FromSlash(candidate.Root)]
		if candidate.Recipe != "java" || marker == nil || marker.jvm == nil || marker.jvm.springProfile == "" {
			continue
		}
		for index := range result.Candidates {
			other := &result.Candidates[index]
			if other.Root != candidate.Root || slices.ContainsFunc(other.Variables, func(variable DetectedVariable) bool {
				return variable.Name == "SPRING_PROFILES_ACTIVE"
			}) || len(other.Variables) >= 64 {
				continue
			}
			other.Variables = append(other.Variables, DetectedVariable{Name: "SPRING_PROFILES_ACTIVE", Example: marker.jvm.springProfile,
				Sources: []string{marker.jvm.springProfileFile}})
		}
	}
}

func settleJVMLayouts(result *DetectionResult, markers map[string]*detectedMarkers, shape *repoShapeScan, drop map[string]bool) {
	type member struct {
		index   int
		project *jvmProject
	}
	groups := map[string][]member{}
	contexts := []string{}
	for index, candidate := range result.Candidates {
		if candidate.Recipe != "java" || candidate.BuildMethod != BuildRecipe {
			continue
		}
		marker := markers[filepath.FromSlash(candidate.Root)]
		if marker == nil || marker.jvm == nil {
			continue
		}
		if marker.jvm.buildLogic != "" {
			drop[candidate.ID] = true
			shape.addSetAside(DetectionSetAside{Path: rootLabelOf(candidate.Root), Kind: "tooling", Reason: boundedText(marker.jvm.buildLogic, 512)})
			continue
		}
		// A Gradle build is its settings root's; a composite's context
		// may be wider and hold other builds.
		context := marker.jvm.context
		if marker.jvm.tool == "gradle" {
			context = marker.jvm.reactor
		}
		if groups[context] == nil {
			contexts = append(contexts, context)
		}
		groups[context] = append(groups[context], member{index: index, project: marker.jvm})
	}
	sort.Strings(contexts)
	for _, context := range contexts {
		members := groups[context]
		first := members[0].project
		if len(members) == 1 && !first.member && !first.aggregator {
			if first.library && first.packaging == "" {
				candidate := &result.Candidates[members[0].index]
				candidate.NotDeployable = "library"
				candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: first.buildFile, Reason: "applies java-library and no plugin that packages an application"})
			}
			continue
		}
		application := func(project *jvmProject) bool { return project.runnable() && !project.androidApp }
		apps := map[int]bool{}
		for _, candidate := range members {
			if application(candidate.project) {
				apps[candidate.index] = true
			}
		}
		fallback := len(apps) == 0
		if fallback {
			// Nothing declares how it packages; a module with a web framework
			// is the one the build is for, and preflight says the artifact
			// may not run.
			for _, candidate := range members {
				if candidate.project.framework != "" && !candidate.project.aggregator && !candidate.project.androidApp {
					apps[candidate.index] = true
				}
			}
		}
		labels := []string{}
		for _, candidate := range members {
			if apps[candidate.index] {
				labels = append(labels, rootLabelOf(result.Candidates[candidate.index].Root))
			}
		}
		build := "Maven build"
		if first.tool == "gradle" {
			build = "Gradle build"
		}
		libraries := []string{}
		for _, candidate := range members {
			project := candidate.project
			root := result.Candidates[candidate.index].Root
			switch {
			case project.androidApp || apps[candidate.index]:
			case len(apps) > 0:
				drop[result.Candidates[candidate.index].ID] = true
				if !project.aggregator {
					libraries = append(libraries, rootLabelOf(root))
					shape.addSetAside(DetectionSetAside{Path: rootLabelOf(root), Kind: "library",
						Reason: boundedText("module of the "+build+" in "+contextLabel(displayDir(context))+" that runs nothing of its own; it is built with "+strings.Join(labels, ", "), 512)})
				}
			case project.aggregator:
				target := &result.Candidates[candidate.index]
				if target.RecipeIssue == "" {
					target.RecipeIssue = boundedText("no module of this "+build+" produces a runnable application: none applies a plugin that packages one (Spring Boot, Quarkus, Micronaut, Ktor, Shadow, the application plugin, an assembly or shade jar) or names a main class", 512)
					target.Confidence = ConfidenceLow
				}
			default:
				drop[result.Candidates[candidate.index].ID] = true
				shape.addSetAside(DetectionSetAside{Path: rootLabelOf(root), Kind: "library",
					Reason: boundedText("module of the "+build+" in "+contextLabel(displayDir(context))+" that runs nothing of its own", 512)})
			}
		}
		for _, candidate := range members {
			if !apps[candidate.index] {
				continue
			}
			target := &result.Candidates[candidate.index]
			if len(libraries) > 0 {
				sort.Strings(libraries)
				target.Evidence = append(target.Evidence, DetectionEvidence{Path: contextLabel(displayDir(context)),
					Reason: boundedEvidenceSentence("built together with " + strings.Join(libraries, ", ") + " from the " + build + " in " + contextLabel(displayDir(context)))})
			}
			if len(labels) > 1 {
				target.Evidence = append(target.Evidence, DetectionEvidence{Path: contextLabel(displayDir(context)),
					Reason: boundedEvidenceSentence("one of " + strings.Join(labels, ", ") + ", the applications of this " + build)})
			}
		}
	}
}

func settleDotnetLayouts(result *DetectionResult, markers map[string]*detectedMarkers, shape *repoShapeScan, drop map[string]bool) {
	type entry struct {
		index int
		info  *dotnetMarker
	}
	var entries []entry
	for index, candidate := range result.Candidates {
		if candidate.Recipe != "dotnet" || candidate.BuildMethod != BuildRecipe {
			continue
		}
		marker := markers[filepath.FromSlash(candidate.Root)]
		if marker == nil || marker.dotnet == nil || marker.dotnet.chosen == nil {
			continue
		}
		entries = append(entries, entry{index: index, info: marker.dotnet})
	}
	if len(entries) == 0 {
		return
	}
	runnable := func(kind string) bool {
		return kind == dotnetKindWeb || kind == dotnetKindWorker || kind == dotnetKindExe || kind == dotnetKindBlazorWasm
	}
	// Which runnable project builds each project it references: those are
	// part of it, not deployments of their own.
	builtBy := map[string]string{}
	hasRunnable := false
	for _, item := range entries {
		chosen := item.info.chosen
		if !runnable(chosen.kind) {
			continue
		}
		hasRunnable = true
		for _, project := range item.info.build.closure {
			if project.file != chosen.file && builtBy[project.file] == "" {
				builtBy[project.file] = chosen.file
			}
		}
	}
	others := len(result.Candidates) > 1
	var appHosts []*dotnetMarker
	for _, item := range entries {
		chosen := item.info.chosen
		candidate := &result.Candidates[item.index]
		root := rootLabelOf(candidate.Root)
		switch {
		case chosen.kind == dotnetKindTest && others:
			drop[candidate.ID] = true
			shape.addSetAside(DetectionSetAside{Path: root, Kind: "tooling", Reason: chosen.name + " is a test project; it runs the tests, it is not deployed"})
		case chosen.kind == dotnetKindAppHost && others:
			drop[candidate.ID] = true
			appHosts = append(appHosts, item.info)
			shape.addSetAside(DetectionSetAside{Path: root, Kind: "tooling",
				Reason: chosen.name + " is an Aspire AppHost: it starts the other projects on a developer's machine, and each of them deploys on its own"})
		case builtBy[chosen.file] != "" && chosen.kind == dotnetKindBlazorWasm:
			drop[candidate.ID] = true
			shape.addSetAside(DetectionSetAside{Path: root, Kind: "static-files",
				Reason: chosen.name + " is the Blazor WebAssembly client " + builtBy[chosen.file] + " publishes and serves"})
		case builtBy[chosen.file] != "" && !runnable(chosen.kind):
			drop[candidate.ID] = true
			shape.addSetAside(DetectionSetAside{Path: root, Kind: "library",
				Reason: chosen.name + " is a library " + builtBy[chosen.file] + " references; it is built with it"})
		case chosen.kind == dotnetKindLibrary && hasRunnable:
			drop[candidate.ID] = true
			shape.addSetAside(DetectionSetAside{Path: root, Kind: "library", Reason: chosen.name + " builds a library, not a program"})
		}
	}
	for _, host := range appHosts {
		wiring := aspireWiring(host)
		for _, item := range entries {
			if variables := wiring[aspireProjectKey(item.info.chosen.name)]; len(variables) > 0 {
				build := result.Candidates[item.index].DotnetBuild
				if build != nil {
					build.AppHost, build.Aspire = host.chosen.file, variables
				}
			}
		}
	}
	// What a .NET project serves itself is not a site of its own: its
	// wwwroot, and the single-page application its publish builds.
	projectDirs := map[string]bool{}
	spaRoots := map[string]string{}
	for _, item := range entries {
		chosen := item.info.chosen
		projectDirs[displayDir(chosen.dir)] = true
		if chosen.spaRoot != "" && runnable(chosen.kind) {
			spaRoots[displayDir(chosen.spaRoot)] = chosen.name
		}
	}
	for index := range result.Candidates {
		candidate := &result.Candidates[index]
		switch {
		case candidate.BuildMethod == BuildStatic && path.Base(candidate.Root) == "wwwroot" && projectDirs[displayDir(path.Dir(candidate.Root))]:
			drop[candidate.ID] = true
			shape.addSetAside(DetectionSetAside{Path: rootLabelOf(candidate.Root), Kind: "static-files",
				Reason: "the web root of the .NET project in " + rootLabelOf(displayDir(path.Dir(candidate.Root))) + ", which publishes it"})
		case candidate.Recipe == "node" && spaRoots[candidate.Root] != "" && candidate.Demotion == "":
			candidate.Demotion = boundedText("the front end "+spaRoots[candidate.Root]+" builds during its publish and serves", 512)
		}
	}
}

var (
	aspireProjectRE   = regexp.MustCompile(`AddProject<Projects\.([A-Za-z0-9_]+)>\(\s*"([A-Za-z0-9_-]+)"`)
	aspireResourceRE  = regexp.MustCompile(`\.Add\w*\(\s*"([A-Za-z0-9_-]+)"`)
	aspireVariableRE  = regexp.MustCompile(`^\s*var\s+(\w+)\s*=`)
	aspireReferenceRE = regexp.MustCompile(`\.WithReference\(\s*(\w+)\s*[),]`)
)

// aspireProjectKey is how an AppHost names a project: Projects.<name> with
// dots and dashes as underscores.
func aspireProjectKey(file string) string {
	name := strings.TrimSuffix(path.Base(file), path.Ext(file))
	return strings.ToLower(strings.NewReplacer(".", "_", "-", "_").Replace(name))
}

// aspireWiring reads an AppHost's Program.cs for what it hands each project
// through WithReference: another project's service-discovery address, or a
// resource's connection string — variables a deployment has to set itself.
func aspireWiring(host *dotnetMarker) map[string][]string {
	wiring := map[string][]string{}
	type resource struct{ name, variable string }
	resources := map[string]resource{}
	statements := strings.Split(string(host.program), ";")
	for _, statement := range statements {
		variable := aspireVariableRE.FindStringSubmatch(statement)
		if variable == nil {
			continue
		}
		if match := aspireProjectRE.FindStringSubmatch(statement); match != nil {
			resources[variable[1]] = resource{name: match[2], variable: "services__" + match[2] + "__http__0"}
		} else if matches := aspireResourceRE.FindAllStringSubmatch(statement, -1); len(matches) > 0 {
			name := matches[len(matches)-1][1]
			resources[variable[1]] = resource{name: name, variable: "ConnectionStrings__" + name}
		}
	}
	for _, statement := range statements {
		match := aspireProjectRE.FindStringSubmatch(statement)
		if match == nil {
			continue
		}
		key := strings.ToLower(match[1])
		for _, reference := range aspireReferenceRE.FindAllStringSubmatch(statement, 16) {
			if target, ok := resources[reference[1]]; ok && len(wiring[key]) < 16 {
				wiring[key] = append(wiring[key], target.variable)
			}
		}
	}
	return wiring
}
