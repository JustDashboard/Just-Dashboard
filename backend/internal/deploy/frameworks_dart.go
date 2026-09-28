package deploy

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// The Dart recipe compiles a server — a Dart Frog application, or a shelf
// or plain dart:io server — ahead of time with `dart compile exe` on the
// official dart image, and runs the executable on Debian slim as an
// unprivileged user. An AOT executable needs only glibc, and slim keeps a
// shell for the start command and the release tasks the official image's
// scratch-based /runtime does not.

var dartRecipeVersions = []string{"3.9", "3.10", "3.11", "3.12", "3.13"}

const (
	dartFrogCLIVersion = "1.2.14"
	dartRuntimeImage   = "debian:trixie-slim"
)

var (
	pubspecSDKRE     = regexp.MustCompile(`(?m)^\s{2}sdk:\s*["']?([^"'\n#]+?)["']?\s*(?:#.*)?$`)
	pubspecNameRE    = regexp.MustCompile(`(?m)^name:\s*["']?([a-z_][a-z0-9_]*)`)
	pubspecLockSDKRE = regexp.MustCompile(`(?m)^\s{2}dart:\s*"([^"]+)"`)
)

// dartServeDirectories are the folders a Dart server conventionally serves
// or reads relative to its working directory, copied beside the executable.
var dartServeDirectories = []string{"public", "static", "assets", "templates", "views"}

type dartRecipe struct {
	version string
	frog    bool
	entry   string
	locked  bool
	// mirror says the install reads PUB_HOSTED_URL: pub then resolves the
	// lock's pub.dev URLs against the mirror, which --enforce-lockfile
	// refuses, so the install keeps the locked versions without it.
	mirror bool
	serve  []string
}

func (r dartRecipe) image() string { return "dart:" + r.version }

func (r dartRecipe) bases() []string { return []string{r.image(), dartRuntimeImage} }

// chooseDartRecipeVersion picks the newest catalogue SDK both pubspec.yaml's
// environment and the lock's resolved SDK range allow.
func chooseDartRecipeVersion(pubspec, lock []byte) (string, error) {
	requirements := []string{}
	if match := pubspecSDKRE.FindSubmatch(pubspec); match != nil {
		requirements = append(requirements, strings.TrimSpace(string(match[1])))
	}
	if match := pubspecLockSDKRE.FindSubmatch(lock); match != nil {
		requirements = append(requirements, string(match[1]))
	}
	for index := len(dartRecipeVersions) - 1; index >= 0; index-- {
		allowed := true
		for _, requirement := range requirements {
			constraint, ok := parseVersionConstraint(requirement)
			allowed = allowed && ok && constraint.allowsFamily(dartRecipeVersions[index])
		}
		if allowed {
			return dartRecipeVersions[index], nil
		}
	}
	return "", fmt.Errorf("%w: the Dart recipe builds with Dart %s; the project requires sdk %s — use a Dockerfile",
		ErrUnsupportedBuilder, strings.Join(dartRecipeVersions, ", "), boundedText(strings.Join(requirements, " and "), 96))
}

// dartProject is what the recipe and detection read from a pubspec root.
type dartProject struct {
	name    string
	version string
	err     error
	frog    bool
	entry   string
	locked  bool
}

func readDartProject(root string) dartProject {
	pubspec := readRecipeFile(root, "pubspec.yaml", 64<<10)
	lock := readRecipeFile(root, "pubspec.lock", 1<<20)
	project := dartProject{locked: lock != nil}
	if match := pubspecNameRE.FindSubmatch(pubspec); match != nil {
		project.name = string(match[1])
	}
	project.version, project.err = chooseDartRecipeVersion(pubspec, lock)
	project.frog = strings.Contains(string(pubspec), "dart_frog:") && directoryHasEntries(root, "routes")
	if project.frog {
		return project
	}
	candidates := []string{"bin/server.dart"}
	if project.name != "" {
		candidates = append(candidates, "bin/"+project.name+".dart")
	}
	candidates = append(candidates, "bin/main.dart")
	for _, entry := range candidates {
		if regularExists(root, entry) {
			project.entry = entry
			break
		}
	}
	if project.entry == "" && project.err == nil {
		project.err = fmt.Errorf("%w: no bin/server.dart, bin/<name>.dart or bin/main.dart to compile; set a build command that writes /out/server", ErrUnsupportedBuilder)
	}
	return project
}

func dartCandidate(buildRoot, root string, match *ecosystemMatch) DetectedCandidate {
	project := readDartProject(buildRoot)
	candidate := DetectedCandidate{
		Name: match.label + " in " + rootLabelOf(root), Profile: match.profile, Confidence: ConfidenceHigh,
		Framework: match.framework, Recipe: "dart", StartCommand: "/app/server",
		Evidence:             []DetectionEvidence{},
		NeedsDecision:        []string{},
		UnpinnedDependencies: !project.locked,
		Toolchain:            &DetectedToolchain{Language: "dart", Release: project.version},
	}
	switch {
	case project.frog:
		candidate.Toolchain.Tool = "dart-frog"
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(root, "routes"), Reason: "dart_frog build, then dart compile exe on Dart " + project.version})
	case project.entry != "":
		candidate.Toolchain.Tool = "dart"
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(root, project.entry), Reason: "dart compile exe " + project.entry + " on Dart " + project.version})
	}
	if match.profile == ProfileWeb {
		candidate.Port = 8080
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(root, "pubspec.yaml"), Reason: "Dart Frog and the shelf template listen on PORT, 8080 by default"})
	} else {
		candidate.Confidence = ConfidenceMedium
		candidate.NeedsDecision = append(candidate.NeedsDecision, "confirm whether this Dart program serves HTTP (web application) or runs as a worker, and its port")
	}
	if project.err != nil {
		candidate.RecipeIssue = recipeRefusalText(project.err, "")
		candidate.Confidence = ConfidenceLow
	}
	return candidate
}

func selectDartRecipe(root string, config BuildPlanConfig) (dartRecipe, error) {
	if !regularExists(root, "pubspec.yaml") {
		return dartRecipe{}, fmt.Errorf("%w: Dart recipe requires pubspec.yaml", ErrUnsupportedBuilder)
	}
	project := readDartProject(root)
	if project.err != nil {
		return dartRecipe{}, project.err
	}
	recipe := dartRecipe{version: project.version, frog: project.frog, entry: project.entry, locked: project.locked}
	for _, secret := range config.Secrets {
		recipe.mirror = recipe.mirror || (secret.Variable == "PUB_HOSTED_URL" && buildSecretReaches(secret.Step, "install"))
	}
	for _, directory := range dartServeDirectories {
		if directoryHasEntries(root, directory) && !slices.Contains(recipe.serve, directory) {
			recipe.serve = append(recipe.serve, directory)
		}
	}
	return recipe, nil
}

func renderDartDockerfile(recipe dartRecipe, config BuildPlanConfig, bases []ResolvedImage, installSecrets, buildSecrets string) ([]string, error) {
	dart, err := resolveCatalogueImage(bases, recipe.image())
	if err != nil {
		return nil, err
	}
	runtime, err := resolveCatalogueImage(bases, dartRuntimeImage)
	if err != nil {
		return nil, err
	}
	fetch := "dart pub get"
	if recipe.locked && !recipe.mirror {
		fetch += " --enforce-lockfile"
	}
	lines := []string{
		"FROM " + immutableImageReference(dart) + " AS build",
		"WORKDIR /app",
		"COPY . .",
		"RUN " + installSecrets + fetch,
	}
	build := strings.TrimSpace(config.BuildCommand)
	switch {
	case build != "":
	case recipe.frog:
		lines = append(lines, "RUN "+installSecrets+"dart pub global activate dart_frog_cli "+dartFrogCLIVersion)
		// dart_frog build writes a plain Dart server into build/, which
		// resolves the same packages offline before it is compiled.
		build = "dart pub global run dart_frog_cli:dart_frog build && cd build && dart pub get --offline && dart compile exe bin/server.dart -o /out/server"
	default:
		build = "dart compile exe " + recipe.entry + " -o /out/server"
	}
	lines = append(lines,
		"RUN mkdir -p /out",
		"RUN "+buildSecrets+build,
		`RUN test -x /out/server || (echo 'the Dart build must write an executable to /out/server' >&2; exit 1)`,
		"FROM "+immutableImageReference(runtime),
		debianPackagesLine([]string{"ca-certificates"}),
		unprivilegedDebianUser,
		"WORKDIR /app",
		"COPY --from=build --chown=10001:10001 /out/server /app/server",
	)
	for _, directory := range recipe.serve {
		source := "/app/" + directory
		if recipe.frog && directory == "public" {
			source = "/app/build/public"
		}
		lines = append(lines, "COPY --from=build --chown=10001:10001 "+source+"/ /app/"+directory+"/")
	}
	lines = append(lines, "USER 10001")
	start := strings.TrimSpace(config.StartCommand)
	if start == "" {
		start = "/app/server"
	}
	return append(lines, shellCMD(start)), nil
}
