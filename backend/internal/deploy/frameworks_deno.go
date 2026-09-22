package deploy

import (
	"encoding/json"
	"fmt"
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
