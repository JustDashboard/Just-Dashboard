package deploy

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
			(build.Recipe == "" || candidate.Recipe == "" || candidate.Recipe == build.Recipe)
	}
	if selected := selectedDetectionCandidate(detection); selected != nil && matches(selected) {
		return selected
	}
	var best *DetectedCandidate
	for index := range detection.Candidates {
		candidate := &detection.Candidates[index]
		if matches(candidate) && (best == nil || confidenceRank(candidate.Confidence) > confidenceRank(best.Confidence)) {
			best = candidate
		}
	}
	return best
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
		if _, err := chooseGoRecipeVersion(build.GoVersion, candidate.GoVersionFile,
			goModuleForVersionCheck(candidate.GoMinimumVersion, candidate.GoToolchain)); err != nil {
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

// pythonDeclaredRange is the interpreter range pyproject declares, bounded
// like any other detection text.
func pythonDeclaredRange(files map[string][]byte) string {
	match := pythonRequiresRE.FindStringSubmatch(string(files["pyproject.toml"]))
	if match == nil || len(match[1]) > 128 || strings.ContainsAny(match[1], "\x00\r\n") {
		return ""
	}
	return match[1]
}

// pythonInstallKind names the manifest the recipe installs from, in the
// order selectPythonInstall reads them.
func pythonInstallKind(files map[string][]byte) string {
	pyproject, hasPyproject := files["pyproject.toml"]
	switch _, uv := files["uv.lock"]; {
	case uv:
		return "uv.lock"
	}
	if _, poetry := files["poetry.lock"]; poetry || (hasPyproject && strings.Contains(string(pyproject), "[tool.poetry]")) {
		return "poetry.lock"
	}
	if _, requirements := files["requirements.txt"]; requirements {
		return "requirements.txt"
	}
	if hasPyproject {
		return "pyproject.toml"
	}
	return ""
}

// startCommandFinding asks for the start command every server recipe needs
// before the recipe is asked to render one it would refuse.
func startCommandFinding(recipe string, candidate *DetectedCandidate, build BuildPlanConfig) (PreflightFinding, bool) {
	if strings.TrimSpace(build.StartCommand) != "" {
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

// detectedVariableFindings reports the variables detection read that the plan
// does not set. Only names read in a way that fails without a value ask for
// one; the rest are listed once, since code reads many optional variables.
func detectedVariableFindings(candidate *DetectedCandidate, configured map[string]bool) []PreflightFinding {
	if candidate == nil || len(candidate.Variables) == 0 {
		return nil
	}
	databases := map[string]string{}
	for _, database := range candidate.Databases {
		databases[database.Variable] = database.Engine
	}
	findings := []PreflightFinding{}
	optional := []string{}
	required := 0
	for _, variable := range candidate.Variables {
		if configured[variable.Name] {
			continue
		}
		if !variable.Required {
			optional = append(optional, variable.Name)
			continue
		}
		required++
		if required > 8 {
			optional = append(optional, variable.Name)
			continue
		}
		source := "the source"
		if len(variable.Sources) > 0 {
			source = variable.Sources[0]
		}
		action := "Set " + variable.Name + " in Variables before deploying."
		if engine := databases[variable.Name]; engine != "" {
			action = "Link a " + engine + " database under Databases, which sets " + variable.Name + ", or set it in Variables."
		}
		findings = append(findings, finding(variableFindingCode("variable_detected_required_", variable.Name), PreflightWarning,
			"A variable the code requires has no value", variable.Name+" (read in "+source+")",
			"The code reads it without a default, so the application stops at start without it.",
			action, "deploy", "variables."+variable.Name))
	}
	if len(optional) > 0 {
		listed := optional
		if len(listed) > 8 {
			listed = append(append([]string(nil), listed[:8]...), fmt.Sprintf("and %d more", len(optional)-8))
		}
		findings = append(findings, finding("variables_detected_unset", PreflightPass,
			fmt.Sprintf("%d detected variable%s left unset", len(optional), plural(len(optional))), strings.Join(listed, ", "),
			"The code or its example env file names them, each read with a default or optionally; they are not set.",
			"", "deploy", "variables"))
	}
	return findings
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

func plural(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
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
