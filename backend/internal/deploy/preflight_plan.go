package deploy

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"slices"
	"strings"
)

// plannedDetectionCandidate is the candidate that describes what the plan
// builds: the one detection found at the configured root with the configured
// method, preferring the selected one. Preflight's recipe checks read this
// candidate's facts, so a root typed after detection is judged by what
// detection found there — or by nothing, never by another directory.
func plannedDetectionCandidate(detection *DetectionResult, build BuildPlanConfig) *DetectedCandidate {
	if detection == nil {
		return nil
	}
	matches := func(candidate *DetectedCandidate) bool {
		return sameBuildRoot(candidate.Root, build.RootDirectory) && candidate.BuildMethod == build.Method &&
			(build.Recipe == "" || candidate.Recipe == "" || candidate.Recipe == build.Recipe) &&
			(build.Method != BuildDockerfile || sameDockerfile(candidate.Dockerfile, build.Dockerfile))
	}
	// Candidates that share a root and method (an Nx workspace's
	// applications) are told apart by the commands the plan runs: a saved
	// plan carries no selection, and the plan for one application must not
	// be judged by another's facts.
	if same := plannedByCommands(detection, build, matches); same != nil {
		return same
	}
	if selected := selectedDetectionCandidate(detection); selected != nil && matches(selected) {
		return selected
	}
	// Detection lists candidates best first (detect_ranking.go), so the
	// first match is the one the ranking prefers at that root.
	for index := range detection.Candidates {
		if candidate := &detection.Candidates[index]; matches(candidate) {
			return candidate
		}
	}
	return nil
}

// plannedByCommands is the one candidate among several matching ones whose
// build and start commands are the plan's, or failing that whose build
// command is, when exactly one is.
func plannedByCommands(detection *DetectionResult, build BuildPlanConfig, matches func(*DetectedCandidate) bool) *DetectedCandidate {
	var all []*DetectedCandidate
	for index := range detection.Candidates {
		if candidate := &detection.Candidates[index]; matches(candidate) {
			all = append(all, candidate)
		}
	}
	if len(all) < 2 {
		return nil
	}
	same := func(detected, planned string) bool { return strings.TrimSpace(detected) == strings.TrimSpace(planned) }
	for _, both := range []bool{true, false} {
		var found *DetectedCandidate
		count := 0
		for _, candidate := range all {
			if same(candidate.BuildCommand, build.BuildCommand) && (!both || same(candidate.StartCommand, build.StartCommand)) {
				found, count = candidate, count+1
			}
		}
		if count == 1 {
			return found
		}
	}
	return nil
}

// rootDetectionCandidate is the candidate whose facts about the source —
// where it listens, what it reads from the environment, what state it
// writes — describe the plan's root: the planned candidate, else one
// detection found at that root with another method, the selected one
// first. The source runs the same code whichever way it is built; only a
// directory detection never read has no facts at all.
func rootDetectionCandidate(detection *DetectionResult, build BuildPlanConfig) *DetectedCandidate {
	if planned := plannedDetectionCandidate(detection, build); planned != nil || detection == nil {
		return planned
	}
	if selected := selectedDetectionCandidate(detection); selected != nil && sameBuildRoot(selected.Root, build.RootDirectory) {
		return selected
	}
	for index := range detection.Candidates {
		if candidate := &detection.Candidates[index]; sameBuildRoot(candidate.Root, build.RootDirectory) {
			return candidate
		}
	}
	return nil
}

// sameDockerfile compares a detected Dockerfile with the plan's, both
// relative to the build root, where an empty one is the default name. A
// root can hold several (Dockerfile, Dockerfile.prod), and each is its own
// candidate with its own findings.
func sameDockerfile(detected, planned string) bool {
	clean := func(value string) string {
		if value = strings.TrimSpace(value); value == "" {
			return "Dockerfile"
		}
		return path.Clean(value)
	}
	return clean(detected) == clean(planned)
}

// detectionRootMismatchFinding reports a root directory edited to one
// detection found nothing to build at. The selected candidate's facts belong
// to another directory, so its recipe checks are not reported as passes.
func detectionRootMismatchFinding(detection *DetectionResult, build BuildPlanConfig, planned *DetectedCandidate) (PreflightFinding, bool) {
	selected := selectedDetectionCandidate(detection)
	if selected == nil || planned != nil || sameBuildRoot(selected.Root, build.RootDirectory) {
		return PreflightFinding{}, false
	}
	switch build.Method {
	case BuildRecipe, BuildDockerfile, BuildStatic:
	default:
		return PreflightFinding{}, false
	}
	root := build.RootDirectory
	if root == "" {
		root = "."
	}
	detected := selected.Root
	if detected == "" {
		detected = "."
	}
	return finding("detection_root_mismatch", PreflightDecision,
		"Detection found nothing to build in the root directory", root+"; detected "+detected,
		"The root directory was changed after detection, and no "+string(build.Method)+" build was found there, so no check has read that directory.",
		"Inspect the source again with source subdirectory "+root+", or set the root directory back to "+detected+".",
		"deploy", "configuration.build.rootDirectory"), true
}

// plannedRecipeFindings re-decides, against the plan's own settings, what a
// candidate's facts say the recipe would refuse: the toolchain, the main
// package, the interpreter range and the start command. Each is a setting,
// so none of them is frozen into the candidate when detection runs.
func plannedRecipeFindings(candidate *DetectedCandidate, build BuildPlanConfig) []PreflightFinding {
	if build.Method != BuildRecipe {
		return nil
	}
	recipe := build.Recipe
	if recipe == "" && candidate != nil {
		recipe = candidate.Recipe
	}
	findings := []PreflightFinding{}
	if item, ok := startCommandFinding(recipe, candidate, build); ok {
		findings = append(findings, item)
	}
	if candidate == nil {
		return findings
	}
	switch recipe {
	case "go":
		if _, err := chooseGoRecipeVersion(build.GoVersion, candidate.GoVersionFile, goCandidateVersionModule(candidate)); err != nil {
			measured := recipeRefusalText(err, "")
			if pinned := goVersionPins(candidate, build); pinned != "" {
				measured += " (" + pinned + ")"
			}
			findings = append(findings, finding("go_version_unsupported", PreflightBlocked,
				"Selected Go version cannot build this source", measured,
				"The toolchain must satisfy the module's declared language requirement, from the Go versions the recipe builds with.",
				"Choose a supported Go version in Build settings, or use a Dockerfile.", "deploy", "configuration.build.goVersion"))
		}
		findings = append(findings, goMainPackageFindings(candidate, build)...)
	case "python":
		if item, ok := pythonVersionFinding(candidate, build); ok {
			findings = append(findings, item)
		}
		findings = append(findings, pythonRecipeFindings(candidate, build)...)
	}
	return findings
}

// goVersionPins names where an automatically chosen Go version came from.
func goVersionPins(candidate *DetectedCandidate, build BuildPlanConfig) string {
	pins := []string{}
	if build.GoVersion != "" {
		pins = append(pins, "selected "+build.GoVersion)
	} else if candidate.GoVersionFile != "" {
		pins = append(pins, ".go-version "+candidate.GoVersionFile)
	}
	if candidate.GoMinimumVersion != "" {
		pins = append(pins, "go.mod go "+candidate.GoMinimumVersion)
	}
	if candidate.GoToolchain != "" {
		pins = append(pins, "toolchain "+candidate.GoToolchain)
	}
	if facts := candidate.Go; facts != nil && facts.WorkGo != "" {
		pins = append(pins, "go.work go "+facts.WorkGo)
	}
	if facts := candidate.Go; facts != nil && facts.WorkToolchain != "" {
		pins = append(pins, "go.work toolchain "+facts.WorkToolchain)
	}
	return strings.Join(pins, "; ")
}

// goMainPackageFindings asks for the main package the recipe cannot choose,
// and refuses a module that has none. A custom build command decides what it
// compiles, so it is asked nothing.
func goMainPackageFindings(candidate *DetectedCandidate, build BuildPlanConfig) []PreflightFinding {
	if command := strings.TrimSpace(build.BuildCommand); command != "" && command != "go build ./..." {
		return nil
	}
	if build.GoPackage != "" {
		chosen := path.Clean(build.GoPackage)
		if len(candidate.GoMainPackages) > 0 && candidate.GoMainPackagesOmitted == 0 &&
			!slices.Contains(candidate.GoMainPackages, chosen) {
			return []PreflightFinding{finding("go_main_missing", PreflightBlocked,
				"The selected Go main package is not a command", goPackageArgument(chosen)+"; main packages: "+goMainPackageList(candidate.GoMainPackages, candidate.GoMainPackagesOmitted),
				"The recipe builds one package main; the directory selected has none that builds for linux.",
				"Choose one of the module's main packages in Build settings.", "deploy", "configuration.build.goPackage")}
		}
		return nil
	}
	switch {
	case candidate.NotDeployable != "":
		// source_not_a_service names the module as a whole.
		return nil
	case candidate.GoLibrary:
		return []PreflightFinding{finding("go_main_missing", PreflightBlocked,
			"This Go module has no main package", "no package main builds for linux",
			"The recipe builds and runs a command; a library module has nothing to run.",
			"Set the root directory to the module that holds the command, or build from a Dockerfile.",
			"deploy", "configuration.build.rootDirectory")}
	case candidate.GoPackage == "" && len(candidate.GoMainPackages) > 1:
		return []PreflightFinding{finding("go_main_ambiguous", PreflightDecision,
			"Choose the Go main package to build", goMainPackageList(candidate.GoMainPackages, candidate.GoMainPackagesOmitted),
			"The module has several commands and nothing in its layout says which one is the service.",
			"Choose the main package in the build settings.", "deploy", "configuration.build.goPackage")}
	}
	return nil
}

// pythonVersionFinding compares the interpreter family the build will use
// with the range the source declares. uv and Poetry refuse an interpreter
// outside it; pip installs anyway and the application meets it at runtime,
// which is a warning rather than a refusal.
func pythonVersionFinding(candidate *DetectedCandidate, build BuildPlanConfig) (PreflightFinding, bool) {
	version := build.PythonVersion
	if version == "" {
		version = candidate.PythonVersion
	}
	if version == "" || candidate.PythonRequires == "" || pythonVersionSatisfies(version, candidate.PythonRequires) {
		return PreflightFinding{}, false
	}
	severity := PreflightWarning
	means := "pip installs the dependencies anyway, and code written for the declared range can fail on this interpreter."
	if candidate.PythonInstall == "uv.lock" || candidate.PythonInstall == "poetry.lock" {
		severity = PreflightBlocked
		means = "The locked install refuses an interpreter outside the project's declared range, so the build would fail."
	}
	action := "Choose a Python version inside the declared range, or change the version the source pins."
	if suggested := pythonVersionForConstraint(candidate.PythonRequires); suggested != "" && pythonRecipeVersionRE.MatchString(suggested) {
		action = "Choose Python " + suggested + " in Build settings, or change the version the source pins."
	}
	return finding("python_version_unsupported", severity,
		"Selected Python version cannot run this project", version+"; requires-python "+candidate.PythonRequires,
		means, action, "deploy", "configuration.build.pythonVersion"), true
}

// pythonVersionSatisfies says whether an interpreter family (3.12) can
// satisfy a requires-python or Poetry range. Families are compared, so a
// bound on a patch release admits the family that contains it; operators the
// recipe cannot judge leave the family admitted.
func pythonVersionSatisfies(version, constraint string) bool {
	minor := pythonMinor(version)
	for _, match := range pythonSpecifierRE.FindAllStringSubmatch(constraint, -1) {
		operator, bound, patch := match[1], pythonMinor(match[2]), match[3]
		switch operator {
		case ">=", ">", "^":
			if minor < bound {
				return false
			}
		case "~=":
			if (patch != "" && minor != bound) || minor < bound {
				return false
			}
		case "==":
			if minor != bound {
				return false
			}
		case "<":
			if (patch == "" && minor >= bound) || minor > bound {
				return false
			}
		case "<=":
			if minor > bound {
				return false
			}
		}
	}
	return true
}

// startCommandFinding asks for the start command every server recipe needs
// before the recipe is asked to render one it would refuse.
func startCommandFinding(recipe string, candidate *DetectedCandidate, build BuildPlanConfig) (PreflightFinding, bool) {
	if strings.TrimSpace(build.StartCommand) != "" {
		return PreflightFinding{}, false
	}
	// A Python or Deno build with an output directory is a site nginx
	// serves (MkDocs, Sphinx, Lume): nothing starts.
	if (recipe == "python" || recipe == "deno") && strings.TrimSpace(build.OutputDirectory) != "" {
		return PreflightFinding{}, false
	}
	var example string
	switch recipe {
	case "python":
		example = "such as `gunicorn app.wsgi --bind 0.0.0.0:$PORT` or `uvicorn main:app --host 0.0.0.0 --port $PORT`"
	case "node":
		if strings.TrimSpace(build.OutputDirectory) != "" {
			return PreflightFinding{}, false
		}
		example = "such as `npm run start`, or an output directory if this is a static site"
	case "deno":
		example = "such as `deno task start` or `deno run -A main.ts`"
	case "php":
		example = "such as `frankenphp php-server --root public/`"
	case "ruby":
		example = "such as `bundle exec puma --port $PORT`"
	default:
		return PreflightFinding{}, false
	}
	measured := "no start command"
	if candidate != nil {
		for _, decision := range candidate.NeedsDecision {
			if strings.Contains(decision, "start command") {
				measured = decision
				break
			}
		}
	}
	return finding("start_command_missing", PreflightBlocked,
		"The "+recipeLabel(recipe)+" recipe needs a start command", measured,
		"The recipe builds an image that runs the start command; without one the build is refused before it starts.",
		"Set the start command in the build settings, "+example+".", "deploy", "configuration.build.startCommand"), true
}

func recipeLabel(recipe string) string {
	switch recipe {
	case "node":
		return "JavaScript"
	case "php":
		return "PHP"
	}
	return strings.ToUpper(recipe[:1]) + recipe[1:]
}

// variableFindingCode is a finding code for one variable. A name can be
// longer than a code may be, so a long one is cut and told apart by a digest
// of the whole name; the full name travels in the finding's field id.
func variableFindingCode(prefix, name string) string {
	code := prefix + strings.ToLower(name)
	if len(code) <= 128 {
		return code
	}
	sum := sha256.Sum256([]byte(name))
	return code[:128-13] + "_" + hex.EncodeToString(sum[:6])
}

// configuredVariableNames is every name the plan gives a value: a variable
// row, a generated secret, a domain-bound URL, a database link, or a value
// staged in the draft.
func configuredVariableNames(configuration PlanConfiguration, staged map[string]string) map[string]bool {
	names := map[string]bool{}
	for _, variable := range configuration.Variables {
		names[variable.Name] = true
	}
	for name := range staged {
		names[name] = true
	}
	return names
}
