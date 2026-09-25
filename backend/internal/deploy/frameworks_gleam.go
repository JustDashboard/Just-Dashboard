package deploy

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// The Gleam recipe builds an Erlang shipment — the compiled BEAM files and a
// start script — with the Gleam project's own image, and runs it on that
// same image, whose Erlang the shipment was compiled for.

const (
	gleamRecipeVersion = "1.18.1"
	gleamImage         = "ghcr.io/gleam-lang/gleam:v" + gleamRecipeVersion + "-erlang-alpine"
)

var (
	gleamRequirementRE = regexp.MustCompile(`(?m)^\s*gleam\s*=\s*"([^"]*)"`)
	gleamTargetRE      = regexp.MustCompile(`(?m)^\s*target\s*=\s*"([a-z]+)"`)
	gleamPortRE        = regexp.MustCompile(`mist\.port\(\s*(\d{2,5})\s*\)`)
	gleamReadsPortRE   = regexp.MustCompile(`(?:envoy\.get|os\.get_env)\(\s*"PORT"`)
)

type gleamRecipe struct {
	locked bool
}

// gleamProject is what the recipe and detection read from a gleam.toml root.
type gleamProject struct {
	err       error
	locked    bool
	port      int
	readsPort bool
}

func readGleamProject(root string) gleamProject {
	manifest := readRecipeFile(root, "gleam.toml", 64<<10)
	project := gleamProject{locked: regularExists(root, "manifest.toml"), port: 8000}
	if match := gleamTargetRE.FindSubmatch(manifest); match != nil && string(match[1]) != "erlang" {
		project.err = fmt.Errorf("%w: gleam.toml targets %s; the Gleam recipe builds an Erlang shipment — build a JavaScript target as a static site with a Dockerfile", ErrUnsupportedBuilder, match[1])
		return project
	}
	if match := gleamRequirementRE.FindSubmatch(manifest); match != nil {
		version, _, _ := parseLanguageVersion(gleamRecipeVersion)
		if constraint, ok := parseVersionConstraint(string(match[1])); ok && !constraint.allows(version) {
			project.err = fmt.Errorf("%w: gleam.toml requires gleam %s; the Gleam recipe builds with %s — use a Dockerfile", ErrUnsupportedBuilder, boundedText(string(match[1]), 64), gleamRecipeVersion)
			return project
		}
	}
	sources, _ := filepath.Glob(filepath.Join(root, "src", "*.gleam"))
	for index, source := range sources {
		if index >= 32 {
			break
		}
		relative, err := filepath.Rel(root, source)
		if err != nil {
			continue
		}
		content := readRecipeFile(root, relative, 128<<10)
		project.readsPort = project.readsPort || gleamReadsPortRE.Match(content)
		if match := gleamPortRE.FindSubmatch(content); match != nil {
			if port, err := strconv.Atoi(string(match[1])); err == nil && port > 0 && port < 65536 {
				project.port = port
			}
		}
	}
	return project
}

func gleamCandidate(buildRoot, root string, match *ecosystemMatch) DetectedCandidate {
	project := readGleamProject(buildRoot)
	candidate := DetectedCandidate{
		Name: match.label + " in " + rootLabelOf(root), Profile: match.profile, Confidence: ConfidenceHigh,
		Framework: match.framework, Recipe: "gleam", StartCommand: "/app/entrypoint.sh run",
		Evidence:             []DetectionEvidence{{Path: joinRoot(root, "gleam.toml"), Reason: "gleam export erlang-shipment with Gleam " + gleamRecipeVersion}},
		NeedsDecision:        []string{},
		UnpinnedDependencies: !project.locked,
		Toolchain:            &DetectedToolchain{Language: "gleam", Release: gleamRecipeVersion},
	}
	if match.profile == ProfileWeb {
		candidate.Port = project.port
		reason := "mist listens on " + strconv.Itoa(project.port)
		if project.readsPort {
			reason += " or the PORT it reads"
		}
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(root, "src"), Reason: reason})
	} else {
		candidate.Confidence = ConfidenceMedium
		candidate.NeedsDecision = append(candidate.NeedsDecision, "confirm whether this Gleam application serves HTTP (web application) or runs as a worker, and its port")
	}
	if project.err != nil {
		candidate.RecipeIssue = recipeRefusalText(project.err, "")
		candidate.Confidence = ConfidenceLow
	}
	return candidate
}

func selectGleamRecipe(root string) (gleamRecipe, error) {
	if !regularExists(root, "gleam.toml") {
		return gleamRecipe{}, fmt.Errorf("%w: Gleam recipe requires gleam.toml", ErrUnsupportedBuilder)
	}
	project := readGleamProject(root)
	if project.err != nil {
		return gleamRecipe{}, project.err
	}
	return gleamRecipe{locked: project.locked}, nil
}

func renderGleamDockerfile(recipe gleamRecipe, config BuildPlanConfig, bases []ResolvedImage, installSecrets, buildSecrets string) ([]string, error) {
	gleam, err := resolveCatalogueImage(bases, gleamImage)
	if err != nil {
		return nil, err
	}
	lines := []string{
		"FROM " + immutableImageReference(gleam) + " AS build",
		"WORKDIR /app",
		"COPY . .",
		"RUN " + installSecrets + "gleam deps download",
	}
	if command := strings.TrimSpace(config.BuildCommand); command != "" {
		lines = append(lines, "RUN "+buildSecrets+command)
	}
	lines = append(lines,
		"RUN "+buildSecrets+"gleam export erlang-shipment",
		"FROM "+immutableImageReference(gleam),
		"RUN adduser -D -u 10001 app && mkdir -p /app/data && chown app:app /app /app/data",
		"WORKDIR /app",
		"COPY --from=build --chown=app:app /app/build/erlang-shipment/ /app/",
		"USER app",
	)
	start := strings.TrimSpace(config.StartCommand)
	if start == "" {
		start = "/app/entrypoint.sh run"
	}
	return append(lines, shellCMD(start)), nil
}
