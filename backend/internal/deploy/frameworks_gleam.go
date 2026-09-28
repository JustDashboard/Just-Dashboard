package deploy

import (
	"fmt"
	"io/fs"
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
	gleamPortCallRE    = regexp.MustCompile(`mist\.port\(`)
	gleamPortDefaultRE = regexp.MustCompile(`unwrap\(\s*(\d{2,5})\s*\)`)
	gleamReadsPortRE   = regexp.MustCompile(`(?:envoy\.get|os\.get_env)\(\s*"PORT"`)
	gleamMistServerRE  = regexp.MustCompile(`\bmist\.(?:new|start|start_http|start_https)\b`)
	gleamMistBindRE    = regexp.MustCompile(`\bmist\.bind\(|import\s+mist\.\{[^}]*\bbind\b`)
	gleamMistMajorRE   = regexp.MustCompile(`\{\s*name\s*=\s*"mist"\s*,\s*version\s*=\s*"(\d+)\.`)
)

// mistDefaultPort is where mist listens when the code never calls
// mist.port.
const mistDefaultPort = 4000

type gleamRecipe struct {
	locked bool
}

// gleamProject is what the recipe and detection read from a gleam.toml root.
type gleamProject struct {
	err    error
	locked bool
	port   int
	// portFrom is the call that fixes port whatever PORT says; readsPort
	// is the file that reads PORT instead.
	portFrom, readsPort string
	// loopback is the file that builds a mist server without mist.bind,
	// which mist 3 and later leave on localhost.
	loopback string
}

func readGleamProject(root string) gleamProject {
	manifest := readRecipeFile(root, "gleam.toml", 64<<10)
	lock := readRecipeFile(root, "manifest.toml", 256<<10)
	project := gleamProject{locked: regularExists(root, "manifest.toml")}
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
	literal, fallback, calls, server, binds := 0, 0, false, "", false
	literalFrom := ""
	for _, relative := range gleamSources(root) {
		content := readRecipeFile(root, relative, 128<<10)
		if gleamReadsPortRE.Match(content) && project.readsPort == "" {
			project.readsPort = relative
			if match := gleamPortDefaultRE.FindSubmatch(content); match != nil {
				fallback, _ = strconv.Atoi(string(match[1]))
			}
		}
		calls = calls || gleamPortCallRE.Match(content)
		if match := gleamPortRE.FindSubmatch(content); match != nil && literal == 0 {
			literal, _ = strconv.Atoi(string(match[1]))
			literalFrom = relative + " mist.port(" + string(match[1]) + ")"
		}
		if server == "" && gleamMistServerRE.Match(content) {
			server = relative
		}
		binds = binds || gleamMistBindRE.Match(content)
	}
	switch {
	case literal > 0 && literal < 65536:
		project.port = literal
		if project.readsPort == "" {
			project.portFrom = literalFrom
		}
	case server != "" && !calls:
		// Reading PORT changes nothing when it never reaches mist.port.
		project.port, project.portFrom, project.readsPort = mistDefaultPort, server+" never calls mist.port, so mist listens on its default", ""
	case project.readsPort != "" && fallback > 0 && fallback < 65536:
		project.port = fallback
	default:
		project.port = 8000
	}
	// mist before 3.0 had no bind and listened on every interface.
	major := 0
	if match := gleamMistMajorRE.FindSubmatch(lock); match != nil {
		major, _ = strconv.Atoi(string(match[1]))
	}
	if server != "" && !binds && (major == 0 || major >= 3) {
		project.loopback = server
	}
	return project
}

// gleamSources are the modules under src/ a server is built in, bounded.
func gleamSources(root string) []string {
	sources := []string{}
	base := filepath.Join(root, "src")
	_ = filepath.WalkDir(base, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return filepath.SkipDir
		}
		if len(sources) >= 64 {
			return filepath.SkipAll
		}
		relative, relErr := filepath.Rel(root, current)
		if relErr != nil {
			return nil
		}
		if entry.IsDir() {
			if strings.Count(filepath.ToSlash(relative), "/") > 4 {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".gleam") {
			sources = append(sources, filepath.ToSlash(relative))
		}
		return nil
	})
	return sources
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
		listen := DetectedListen{}
		reason := "mist listens on " + strconv.Itoa(project.port)
		switch {
		case project.portFrom != "":
			listen.Port, listen.PortFrom = project.port, listenText(joinRoot(root, project.portFrom))
			reason += " whatever PORT says (" + project.portFrom + ")"
		case project.readsPort != "":
			listen.ReadsPort, listen.ReadsPortFrom = true, listenText(joinRoot(root, project.readsPort)+" reads PORT")
			reason += " or the PORT it reads"
		}
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(root, "src"), Reason: reason})
		if project.loopback != "" {
			listen.Loopback, listen.LoopbackCertain = "localhost", true
			listen.LoopbackFrom = listenText(joinRoot(root, project.loopback) + " builds a mist server without mist.bind, and mist listens on localhost unless it is called")
			candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(root, project.loopback),
				Reason: "mist listens on localhost unless mist.bind is called, which nothing outside the container reaches"})
		}
		if listen != (DetectedListen{}) {
			candidate.Listen = &listen
		}
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
