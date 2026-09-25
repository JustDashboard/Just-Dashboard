package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strconv"
	"strings"
)

// The Deno recipe runs a project's own `start` task on the official image,
// after `deno install` has cached what deno.json imports.

type denoConfig struct {
	Tasks   map[string]json.RawMessage `json:"tasks"`
	Imports map[string]string          `json:"imports"`
}

// parseDenoConfig reads deno.json or deno.jsonc. JSONC's comments and
// trailing commas are stripped first; a file that still does not parse
// yields an empty configuration rather than an error, since the task names
// are a convenience and the recipe can still run an entry file.
func parseDenoConfig(content []byte) denoConfig {
	content = manifestText(content)
	var config denoConfig
	if json.Unmarshal(content, &config) == nil {
		return config
	}
	config = denoConfig{}
	if json.Unmarshal(denoJSONWithoutComments(content), &config) != nil {
		return denoConfig{}
	}
	return config
}

// Comments and trailing commas are syntax only outside strings. Replacing
// them with whitespace preserves URLs, shell globs and escaped task quotes.
func denoJSONWithoutComments(content []byte) []byte {
	cleaned := append([]byte(nil), content...)
	inString := false
	for i := 0; i < len(cleaned); i++ {
		if inString {
			if cleaned[i] == '\\' {
				i++
			} else if cleaned[i] == '"' {
				inString = false
			}
			continue
		}
		if cleaned[i] == '"' {
			inString = true
			continue
		}
		if cleaned[i] != '/' || i+1 == len(cleaned) {
			continue
		}
		switch cleaned[i+1] {
		case '/':
			for i < len(cleaned) && cleaned[i] != '\n' && cleaned[i] != '\r' {
				cleaned[i] = ' '
				i++
			}
		case '*':
			start := i
			i += 2
			for i+1 < len(cleaned) && !(cleaned[i] == '*' && cleaned[i+1] == '/') {
				i++
			}
			if i+1 == len(cleaned) || i == len(cleaned) {
				return nil
			}
			i++
			for j := start; j <= i; j++ {
				if cleaned[j] != '\n' && cleaned[j] != '\r' {
					cleaned[j] = ' '
				}
			}
		}
	}
	inString = false
	for i := 0; i < len(cleaned); i++ {
		if inString {
			if cleaned[i] == '\\' {
				i++
			} else if cleaned[i] == '"' {
				inString = false
			}
			continue
		}
		if cleaned[i] == '"' {
			inString = true
		} else if cleaned[i] == ',' {
			for j := i + 1; j < len(cleaned); j++ {
				if strings.ContainsRune(" \t\r\n", rune(cleaned[j])) {
					continue
				}
				if cleaned[j] == '}' || cleaned[j] == ']' {
					cleaned[i] = ' '
				}
				break
			}
		}
	}
	return cleaned
}

// denoTask reads a task's command, which is a string or an object with a
// `command` field in newer configurations.
func (c denoConfig) task(name string) string {
	raw, ok := c.Tasks[name]
	if !ok {
		return ""
	}
	var command string
	if json.Unmarshal(raw, &command) == nil {
		return strings.TrimSpace(command)
	}
	var object struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(raw, &object) == nil {
		return strings.TrimSpace(object.Command)
	}
	return ""
}

var denoEntryFiles = []string{
	"main.ts", "server.ts", "mod.ts", "main.js", "server.js", "main.tsx", "index.ts", "app.ts",
	"src/main.ts", "src/server.ts", "src/index.ts",
}

// denoCandidate builds the candidate for a root with a deno.json, or a
// package.json that Deno runs (readDenoProjects).
func denoCandidate(marker *detectedMarkers, rootLabel string) DetectedCandidate {
	config := parseDenoConfig(marker.denoJSON)
	candidate := DetectedCandidate{
		Name: "Deno service in " + rootLabel, Profile: ProfileWeb, Confidence: ConfidenceMedium,
		Framework: "deno", Recipe: "deno", Port: 8000,
		Evidence:      []DetectionEvidence{{Path: joinRoot(marker.root, marker.denoJSONPath), Reason: "Deno configuration"}},
		NeedsDecision: []string{},
	}
	project := denoProject{config: config}
	if marker.deno != nil {
		project = *marker.deno
	}
	if marker.denoJSONPath == "package.json" {
		candidate.Evidence[0].Reason = "package.json scripts run by deno task"
	}
	candidate.UnpinnedDependencies = !marker.denoLock && (len(config.Imports) > 0 || len(project.packageDeps) > 0)
	for name := range config.Imports {
		if strings.HasPrefix(name, "fresh") || strings.HasPrefix(name, "$fresh") || strings.HasPrefix(name, "@fresh/") {
			candidate.Framework = "fresh"
			candidate.Name = "Fresh application in " + rootLabel
			break
		}
	}
	if build := config.task("build"); build != "" {
		candidate.BuildCommand = "deno task build"
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, marker.denoJSONPath), Reason: "build task: " + boundedEvidence(build)})
	}
	watching := false
	switch {
	case procfileProcess(marker.procfile, "web") != "" && rejectPlanSecretLiteral("Procfile web process", procfileProcess(marker.procfile, "web")) == nil:
		candidate.StartCommand = procfileProcess(marker.procfile, "web")
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, "Procfile"), Reason: "web process: " + boundedEvidence(candidate.StartCommand)})
	case config.task("start") != "" && denoWatcher(config.task("start")):
		// Fresh 1's start task is its development server: a file watcher
		// that bundles on request and ignores what the build task wrote.
		switch {
		case config.task("preview") != "":
			candidate.StartCommand = "deno task preview"
		case config.task("serve") != "" && !denoWatcher(config.task("serve")):
			candidate.StartCommand = "deno task serve"
		case marker.denoEntries["main.ts"]:
			candidate.StartCommand = "deno run -A main.ts"
		default:
			candidate.StartCommand, watching = "deno task start", true
		}
		reason := "start task runs a development watcher (" + boundedEvidence(config.task("start")) + "); using " + candidate.StartCommand
		if watching {
			reason = "start task runs a development watcher (" + boundedEvidence(config.task("start")) + ") and nothing else serves the build"
		}
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, marker.denoJSONPath), Reason: reason})
	case config.task("start") != "":
		candidate.StartCommand = "deno task start"
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, marker.denoJSONPath), Reason: "start task: " + boundedEvidence(config.task("start"))})
	default:
		for _, entry := range denoEntryFiles {
			if marker.denoEntries[entry] {
				candidate.StartCommand = "deno run --allow-all " + entry
				candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, entry), Reason: "entry file"})
				break
			}
		}
		if candidate.StartCommand == "" {
			candidate.Confidence = ConfidenceLow
			candidate.NeedsDecision = append(candidate.NeedsDecision, "add a start task to deno.json or choose the entry file to run")
		}
	}
	facts := &DetectedDeno{Version: project.release, VersionFrom: project.versionFrom, Declared: boundedEvidence(project.declared),
		LockStale: boundPHPNames(project.stale), WatchStart: watching}
	if project.release == "" {
		facts.Version, _, _ = denoRelease("")
	}
	if facts.VersionFrom != "" {
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: facts.VersionFrom, Reason: "builds on Deno " + facts.Version})
	}
	if len(facts.LockStale) > 0 {
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, "deno.lock"),
			Reason: boundedEvidenceSentence("deno.lock does not record " + strings.Join(facts.LockStale, ", ") + "; the install runs without --frozen")})
	}
	if entry := denoCommandEntry(candidate.StartCommand, project.task); entry != "" && (project.entries[entry] || marker.denoEntries[entry]) {
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, entry), Reason: "caches " + entry + "'s module graph at build"})
	}
	candidate.Deno = facts
	return candidate
}

// denoRecipe is what the builder needs beyond the plan: the release and its
// image, whether deno.lock is installed frozen, and the file whose module
// graph is cached at build.
type denoRecipe struct {
	release, image string
	major          int
	locked         bool
	entry          string
	versionFrom    string
}

func selectDenoRecipe(boundary, root string, config BuildPlanConfig) (denoRecipe, error) {
	project, err := openDenoProject(boundary, root)
	if err != nil {
		return denoRecipe{}, err
	}
	configured := regularExists(root, "deno.json") || regularExists(root, "deno.jsonc")
	if !configured {
		// A package.json project Deno runs installs the same way: deno install
		// reads package.json, and deno task runs its scripts.
		marker := &detectedMarkers{denoLock: regularExists(root, "deno.lock")}
		for _, lockfile := range nodeLockfileNames {
			if regularExists(root, lockfile.path) {
				marker.lockfiles = append(marker.lockfiles, lockfile.path)
			}
		}
		marker.packageJSON, _ = readContainedRegular(root, "package.json", 512<<10)
		if !marker.denoRunsPackage() {
			return denoRecipe{}, fmt.Errorf("%w: Deno recipe requires deno.json or deno.jsonc, or a package.json whose scripts run deno", ErrUnsupportedBuilder)
		}
	}
	if strings.TrimSpace(config.StartCommand) == "" && strings.TrimSpace(config.OutputDirectory) == "" {
		return denoRecipe{}, fmt.Errorf("%w: Deno recipe requires a start command (deno task start, or deno run an entry file)", ErrUnsupportedBuilder)
	}
	major, _ := strconv.Atoi(strings.SplitN(project.release, ".", 2)[0])
	recipe := denoRecipe{
		release: project.release, image: project.image, major: major, versionFrom: project.versionFrom,
		// A stale lock is installed the way a stale package-lock.json is:
		// resolved again, with the warning preflight gave before Deploy.
		locked: project.lock && len(project.stale) == 0,
	}
	// A start that runs what the build writes (Fresh 2's _fresh/server.js,
	// an adapter's dist/server.js) has no graph to cache from the checkout.
	if entry := denoCommandEntry(config.StartCommand, project.task); entry != "" && regularExists(root, entry) {
		recipe.entry = entry
	}
	return recipe, nil
}

// toolchain names the release for the build evidence, and where the
// repository declared it.
func (r denoRecipe) toolchain() string {
	if r.versionFrom != "" {
		return "deno " + r.release + " (" + path.Base(r.versionFrom) + ")"
	}
	return "deno " + r.release
}

// settleDenoImage keeps a declared exact release only when its image
// exists, falling back to the catalogue's release of that major with a
// logged note instead of failing the build on a registry lookup.
func (b *ArtifactBuilder) settleDenoImage(ctx context.Context, recipe *denoRecipe) []string {
	fallback := recipeBaseCatalogue["deno:"+strconv.Itoa(recipe.major)]
	if len(fallback) == 0 || recipe.image == fallback[0] {
		return nil
	}
	if _, err := b.backend.ResolveImage(ctx, recipe.image, ""); err == nil {
		return nil
	}
	note := "the declared Deno release " + recipe.release + " has no " + recipe.image + " image; building with " + fallback[0]
	recipe.image, recipe.release = fallback[0], denoReleases[recipe.major]
	return []string{note}
}

func renderDenoDockerfile(recipe denoRecipe, config BuildPlanConfig, bases []ResolvedImage, installSecrets, buildSecrets string) ([]string, error) {
	if len(bases) == 0 {
		return nil, ErrBuilderUnavailable
	}
	lines := []string{
		"FROM " + immutableImageReference(bases[0]) + " AS build",
		"WORKDIR /app",
		"COPY . .",
	}
	if recipe.major != 1 {
		// Deno 1's install is a script installer; its cache step below is
		// what caches the application's modules.
		install := "deno install"
		if recipe.locked {
			install += " --frozen"
		}
		lines = append(lines, "RUN "+installSecrets+install)
	}
	if command := strings.TrimSpace(config.BuildCommand); command != "" {
		lines = append(lines, "RUN "+buildSecrets+command)
	}
	if strings.TrimSpace(config.OutputDirectory) != "" {
		// A site generator's output is served by the stage that follows.
		return lines, nil
	}
	// URL and npm: imports written only in code are not deno.json's, so
	// without this they are fetched at every container start. It runs after
	// the build, which may write a file the entry imports; the lock still
	// verifies every module it records.
	switch {
	case recipe.entry == "":
	case recipe.major == 1:
		lines = append(lines, "RUN "+installSecrets+"deno cache "+recipe.entry)
	default:
		lines = append(lines, "RUN "+installSecrets+"deno install --entrypoint "+recipe.entry)
	}
	lines = append(lines, shellCMD(config.StartCommand))
	return lines, nil
}
