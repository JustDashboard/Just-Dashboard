package deploy

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// The Deno recipe runs a project's own `start` task on the official image,
// after `deno install` has cached what deno.json imports.

var (
	denoLineCommentRE  = regexp.MustCompile(`(?m)^\s*//.*$`)
	denoBlockCommentRE = regexp.MustCompile(`(?s)/\*.*?\*/`)
	denoTrailingComma  = regexp.MustCompile(`,(\s*[}\]])`)
)

type denoConfig struct {
	Tasks   map[string]json.RawMessage `json:"tasks"`
	Imports map[string]string          `json:"imports"`
}

// parseDenoConfig reads deno.json or deno.jsonc. JSONC's comments and
// trailing commas are stripped first; a file that still does not parse
// yields an empty configuration rather than an error, since the task names
// are a convenience and the recipe can still run an entry file.
func parseDenoConfig(content []byte) denoConfig {
	var config denoConfig
	if json.Unmarshal(content, &config) == nil {
		return config
	}
	cleaned := denoBlockCommentRE.ReplaceAll(content, nil)
	cleaned = denoLineCommentRE.ReplaceAll(cleaned, nil)
	cleaned = denoTrailingComma.ReplaceAll(cleaned, []byte("$1"))
	_ = json.Unmarshal(cleaned, &config)
	return config
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

var denoEntryFiles = []string{"main.ts", "server.ts", "mod.ts", "main.js", "server.js", "src/main.ts", "src/server.ts"}

// denoCandidate builds the candidate for a root with a deno.json.
func denoCandidate(marker *detectedMarkers, rootLabel string) DetectedCandidate {
	config := parseDenoConfig(marker.denoJSON)
	candidate := DetectedCandidate{
		Name: "Deno service in " + rootLabel, Profile: ProfileWeb, Confidence: ConfidenceMedium,
		Framework: "deno", Recipe: "deno", Port: 8000,
		Evidence:      []DetectionEvidence{{Path: joinRoot(marker.root, marker.denoJSONPath), Reason: "Deno configuration"}},
		NeedsDecision: []string{},
	}
	candidate.UnpinnedDependencies = !marker.denoLock && len(config.Imports) > 0
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
	switch {
	case procfileProcess(marker.procfile, "web") != "" && rejectPlanSecretLiteral("Procfile web process", procfileProcess(marker.procfile, "web")) == nil:
		candidate.StartCommand = procfileProcess(marker.procfile, "web")
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, "Procfile"), Reason: "web process: " + boundedEvidence(candidate.StartCommand)})
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
	candidate.Evidence = append(candidate.Evidence, DetectionEvidence{
		Path: joinRoot(marker.root, marker.denoJSONPath), Reason: "Deno.serve default port 8000",
	})
	return candidate
}

type denoRecipe struct{ locked bool }

func selectDenoRecipe(root string, config BuildPlanConfig) (denoRecipe, error) {
	if !regularExists(root, "deno.json") && !regularExists(root, "deno.jsonc") {
		return denoRecipe{}, fmt.Errorf("%w: Deno recipe requires deno.json or deno.jsonc", ErrUnsupportedBuilder)
	}
	if strings.TrimSpace(config.StartCommand) == "" {
		return denoRecipe{}, fmt.Errorf("%w: Deno recipe requires a start command (deno task start, or deno run an entry file)", ErrUnsupportedBuilder)
	}
	return denoRecipe{locked: regularExists(root, "deno.lock")}, nil
}

func renderDenoDockerfile(recipe denoRecipe, config BuildPlanConfig, bases []ResolvedImage, installSecrets, buildSecrets string) ([]string, error) {
	if len(bases) != 1 {
		return nil, ErrBuilderUnavailable
	}
	install := "deno install"
	if recipe.locked {
		install += " --frozen"
	}
	lines := []string{
		"FROM " + immutableImageReference(bases[0]),
		"WORKDIR /app",
		"COPY . .",
		"RUN " + installSecrets + install,
	}
	if command := strings.TrimSpace(config.BuildCommand); command != "" {
		lines = append(lines, "RUN "+buildSecrets+command)
	}
	lines = append(lines, shellCMD(config.StartCommand))
	return lines, nil
}
