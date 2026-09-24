package deploy

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// The .NET recipe publishes one project with the SDK image for its target
// framework and runs it on the matching ASP.NET Core (or plain runtime)
// image. Supported targets are the releases Microsoft still ships images for.
var (
	dotnetRecipeVersions = []string{"8.0", "9.0", "10.0"}
	dotnetTargetRE       = regexp.MustCompile(`<TargetFramework(s?)>\s*([^<]+)\s*</TargetFrameworks?>`)
	dotnetVersionRE      = regexp.MustCompile(`^net([0-9]+\.[0-9]+)$`)
	dotnetSdkRE          = regexp.MustCompile(`Sdk\s*=\s*"([^"]+)"`)
	dotnetAssemblyRE     = regexp.MustCompile(`<AssemblyName>\s*([^<\s]+)\s*</AssemblyName>`)
	dotnetOutputTypeRE   = regexp.MustCompile(`<OutputType>\s*([A-Za-z]+)\s*</OutputType>`)
)

type dotnetProject struct {
	file        string
	version     string
	web         bool
	exe         bool
	assembly    string
	multiTarget bool
}

func parseDotnetProject(file string, content []byte) (dotnetProject, error) {
	text := string(content)
	project := dotnetProject{file: file, assembly: strings.TrimSuffix(path.Base(file), path.Ext(file))}
	if match := dotnetAssemblyRE.FindStringSubmatch(text); match != nil && safeRelativePath(match[1]) && !strings.ContainsAny(match[1], "/\\") {
		project.assembly = match[1]
	}
	if match := dotnetSdkRE.FindStringSubmatch(text); match != nil {
		project.web = match[1] == "Microsoft.NET.Sdk.Web"
	}
	if match := dotnetOutputTypeRE.FindStringSubmatch(text); match != nil {
		project.exe = strings.EqualFold(match[1], "Exe") || strings.EqualFold(match[1], "WinExe")
	}
	match := dotnetTargetRE.FindStringSubmatch(text)
	if match == nil {
		return project, fmt.Errorf("%w: %s declares no <TargetFramework>; the .NET recipe needs one to choose its SDK", ErrUnsupportedBuilder, file)
	}
	project.multiTarget = match[1] == "s"
	for _, target := range strings.Split(match[2], ";") {
		parsed := dotnetVersionRE.FindStringSubmatch(strings.TrimSpace(target))
		if parsed == nil || !slices.Contains(dotnetRecipeVersions, parsed[1]) {
			continue
		}
		if slices.Index(dotnetRecipeVersions, parsed[1]) > slices.Index(dotnetRecipeVersions, project.version) {
			project.version = parsed[1]
		}
	}
	if project.version == "" || (!project.multiTarget && strings.Contains(match[2], ";")) {
		return project, fmt.Errorf("%w: the .NET recipe builds portable net8.0, net9.0 and net10.0 targets; %s targets %s — use a Dockerfile for other targets", ErrUnsupportedBuilder, file, strings.TrimSpace(match[2]))
	}
	return project, nil
}

// chooseDotnetProject picks the one project a root publishes: the only
// project file, or among several the one web application.
func chooseDotnetProject(projects map[string][]byte) (dotnetProject, error) {
	names := make([]string, 0, len(projects))
	for name := range projects {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return dotnetProject{}, fmt.Errorf("%w: .NET recipe requires a .csproj file", ErrUnsupportedBuilder)
	}
	if len(names) == 1 {
		return parseDotnetProject(names[0], projects[names[0]])
	}
	var web []dotnetProject
	var firstErr error
	for _, name := range names {
		project, err := parseDotnetProject(name, projects[name])
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if project.web {
			web = append(web, project)
		}
	}
	if len(web) == 1 {
		return web[0], nil
	}
	if firstErr != nil && len(web) == 0 {
		return dotnetProject{}, firstErr
	}
	return dotnetProject{}, fmt.Errorf("%w: several .NET projects at this root (%s); set the root directory to the one to publish", ErrUnsupportedBuilder, strings.Join(names, ", "))
}

// dotnetCandidate builds the candidate for a root with project files.
func dotnetCandidate(marker *detectedMarkers, rootLabel string) DetectedCandidate {
	candidate := DetectedCandidate{
		Name: ".NET service in " + rootLabel, Profile: ProfileWorker, Confidence: ConfidenceMedium,
		Framework: "dotnet", Recipe: "dotnet",
		Evidence:      []DetectionEvidence{},
		NeedsDecision: []string{},
	}
	project, err := chooseDotnetProject(marker.csprojs)
	if err != nil {
		candidate.Confidence = ConfidenceLow
		candidate.RecipeIssue = err.Error()
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
	candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, project.file), Reason: ".NET project targeting net" + project.version})
	switch {
	case project.web:
		candidate.Framework = "aspnet"
		candidate.Name = "ASP.NET Core application in " + rootLabel
		candidate.Profile, candidate.Port, candidate.Confidence = ProfileWeb, 8080, ConfidenceHigh
	case project.exe:
		candidate.NeedsDecision = append(candidate.NeedsDecision, "confirm whether this program serves HTTP (web application) or runs as a worker, and its port")
	default:
		candidate.Confidence = ConfidenceLow
		candidate.NeedsDecision = append(candidate.NeedsDecision, project.file+" builds a library, not a program; confirm the project to publish")
	}
	if procfileWeb := procfileProcess(marker.procfile, "web"); procfileWeb != "" && rejectPlanSecretLiteral("Procfile web process", procfileWeb) == nil {
		candidate.StartCommand = procfileWeb
		candidate.Profile = ProfileWeb
		if candidate.Port == 0 {
			candidate.Port = 8080
		}
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, "Procfile"), Reason: "web process: " + boundedEvidence(procfileWeb)})
	}
	return candidate
}

func selectDotnetRecipe(root string) (dotnetProject, error) {
	projects := map[string][]byte{}
	for _, name := range listContainedFiles(root, ".csproj") {
		if content, err := readContainedRegular(root, name, 512<<10); err == nil {
			projects[name] = content
		}
	}
	return chooseDotnetProject(projects)
}

func renderDotnetDockerfile(project dotnetProject, config BuildPlanConfig, bases []ResolvedImage, installSecrets, buildSecrets string) ([]string, error) {
	if len(bases) != 2 {
		return nil, ErrBuilderUnavailable
	}
	build := strings.TrimSpace(config.BuildCommand)
	restore := "dotnet restore " + project.file
	framework := ""
	if project.multiTarget {
		// Publishing several frameworks requires an explicit target; restore
		// the same target so an older or Windows-only sibling does not need a
		// different SDK or workload inside this Linux recipe.
		framework = " --framework net" + project.version
		restore += " -p:TargetFramework=net" + project.version
	}
	if build == "" {
		build = "dotnet publish " + project.file + " -c Release --no-restore -o /out" + framework
	}
	lines := []string{
		"FROM " + immutableImageReference(bases[0]) + " AS build",
		"WORKDIR /src",
		"COPY . .",
		"RUN " + installSecrets + restore,
		"RUN " + buildSecrets + build,
		"RUN test -f /out/" + project.assembly + ".dll || (echo '.NET publish must write /out/" + project.assembly + ".dll; configure the build command and project together' >&2; exit 1)",
		"FROM " + immutableImageReference(bases[1]),
		"WORKDIR /app",
		// The application runs as the image's app user, so the directory it
		// runs from — where a relative app.db lands — its data directory and
		// the Data Protection key ring are made that user's. A volume mounted
		// on one of them copies that ownership the first time it is used.
		"RUN mkdir -p " + dotnetRuntimeDataDir + " " + dotnetDataProtectionAt + " && chown app:app /app " + dotnetRuntimeDataDir + " /home/app/.aspnet " + dotnetDataProtectionAt,
		"COPY --from=build --chown=app:app /out /app",
		"USER app",
	}
	if strings.TrimSpace(config.StartCommand) == "" {
		// Kestrel reads its port from ASPNETCORE_HTTP_PORTS, not PORT; the
		// shell bridges the one the runtime injects.
		lines = append(lines, shellCMD("ASPNETCORE_HTTP_PORTS=${PORT:-8080} dotnet /app/"+project.assembly+".dll"))
	} else {
		lines = append(lines, shellCMD(config.StartCommand))
	}
	return lines, nil
}
