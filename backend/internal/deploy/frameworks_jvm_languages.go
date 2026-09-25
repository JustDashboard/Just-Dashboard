package deploy

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Scala and Clojure build on the JVM the way the Java recipe does — a JDK
// image for the build, a Temurin JRE for the runtime, an unprivileged user,
// the heap sized from the container's limit — with their own build tools:
// sbt's native-packager `stage` or `assembly`, Leiningen's `uberjar`, and a
// tools.build `uber` task. The builders are the sbt project's
// sbtscala/scala-sbt and the official clojure image; each launcher fetches
// the sbt, Scala and Clojure releases the project itself pins.

var jvmLanguageJDKs = []string{"17", "21", "25"}

var (
	sbtVersionRE      = regexp.MustCompile(`(?m)^\s*sbt\.version\s*=\s*([0-9][0-9A-Za-z.-]*)`)
	sbtPlayPluginRE   = regexp.MustCompile(`"(?:org\.playframework|com\.typesafe\.play)"\s*%\s*"sbt-plugin"`)
	sbtPackagerRE     = regexp.MustCompile(`"sbt-native-packager"`)
	sbtAssemblyRE     = regexp.MustCompile(`"sbt-assembly"`)
	sbtPlayEnabledRE  = regexp.MustCompile(`enablePlugins\([^)]*\bPlay(?:Scala|Java|MinimalJava|Service)\b`)
	sbtStageEnabledRE = regexp.MustCompile(`enablePlugins\([^)]*\b(?:Play(?:Scala|Java|MinimalJava|Service)|JavaAppPackaging|JavaServerAppPackaging)\b`)
	sbtProjectRE      = regexp.MustCompile(`(?m)^\s*lazy\s+val\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*\(?\s*project(?:\s+in\s+file\(\s*"([^"\n]*)"\s*\)|\.in\(\s*file\(\s*"([^"\n]*)"\s*\)\s*\))?`)
	sbtNameRE         = regexp.MustCompile(`\bname\s*:=\s*"{1,3}([^"\n]+)"{1,3}`)
	sbtScriptNameRE   = regexp.MustCompile(`\bexecutableScriptName\s*:=\s*"([^"\n]+)"`)
	sbtJavaReleaseRE  = regexp.MustCompile(`"-?-release(?::(\d{1,2})"|"\s*,?\s*"(\d{1,2})")|"-java-output-version"\s*,?\s*"(\d{1,2})"`)
	sbtNonWordRE      = regexp.MustCompile(`\W+`)
	leinMainRE        = regexp.MustCompile(`:main\s+(\^:skip-aot\s+)?[A-Za-z]`)
	leinAOTRE         = regexp.MustCompile(`:aot\s+(?::all|\[)`)
	cljBuildAliasRE   = regexp.MustCompile(`:build\s*\{`)
	cljUberTaskRE     = regexp.MustCompile(`\(defn\s+(uber(?:jar)?)\b`)
	// The stage directory and start script are written into the generated
	// Dockerfile, so only a plain relative path and file name are taken.
	sbtStageDirRE   = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,128}$`)
	sbtScriptFileRE = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)
)

// jvmLanguageRecipe is what the Scala and Clojure recipes build with.
type jvmLanguageRecipe struct {
	language, tool, jdk string
	// sbt is the launcher series the project's sbt.version needs.
	sbt string
	// stage is sbt stage's output directory and script the start script
	// it wrote; task is the tools.build function that writes the uberjar.
	stage, script, task string
	play                bool
}

func (r jvmLanguageRecipe) builderImage() string {
	switch r.tool {
	case "lein":
		return "clojure:temurin-" + r.jdk + "-lein"
	case "tools-deps":
		return "clojure:temurin-" + r.jdk + "-tools-deps"
	}
	return "sbtscala/scala-sbt:eclipse-temurin-" + r.jdk + "_" + r.sbt + ".x"
}

// runtimeImage is the JRE the program runs on: Ubuntu's for sbt stage,
// whose start script is bash, Alpine's for a jar.
func (r jvmLanguageRecipe) runtimeImage() string {
	if r.tool == "sbt-stage" {
		return "eclipse-temurin:" + r.jdk + "-jre"
	}
	return "eclipse-temurin:" + r.jdk + "-jre-alpine"
}

func (r jvmLanguageRecipe) bases() []string { return []string{r.builderImage(), r.runtimeImage()} }

func (r jvmLanguageRecipe) toolchain() string {
	switch r.tool {
	case "sbt-stage":
		return "scala · sbt " + r.sbt + ".x stage on java " + r.jdk
	case "sbt-assembly":
		return "scala · sbt " + r.sbt + ".x assembly on java " + r.jdk
	case "lein":
		return "clojure · lein uberjar on java " + r.jdk
	}
	return "clojure · clojure -T:build " + r.task + " on java " + r.jdk
}

// start is the command that runs the program: the start script sbt stage
// wrote, with Play's port, pid file and host filter given as system
// properties, or the uberjar.
func (r jvmLanguageRecipe) start() string {
	if r.tool != "sbt-stage" {
		return "exec java -jar /app/app.jar"
	}
	start := "exec /app/bin/" + r.script
	if r.play {
		// Play's secret comes from the configuration the recipe writes
		// beside the application's own, which keeps it out of argv. Play
		// reads its port from http.port, writes RUNNING_PID where the next
		// container would find it, and answers only localhost until its
		// host filter allows the domain the proxy forwards.
		start += " -Dconfig.file=" + playSecretConfig + " -Dhttp.port=${PORT:-9000} -Dpidfile.path=/dev/null -Dplay.filters.hosts.allowed.0=."
	}
	return start
}

// playSecretConfig is the configuration a Play start loads: the
// application's own, then the secret from APPLICATION_SECRET. Play's
// reference.conf never reads that variable, and a stock application.conf
// leaves play.http.secret.key at "changeme", which production refuses. An
// include of the bare name "application" loads application.conf (or .json,
// .properties) from beside it when sbt stage externalised conf/, and from
// the classpath when it did not.
const playSecretConfig = "/app/conf/just-dashboard.conf"

var playSecretConfigLines = []string{`include "application"`, `play.http.secret.key = ${?APPLICATION_SECRET}`}

// chooseJVMLanguageJDK takes the JDK the root's version files pin — read as
// the Java recipe reads them (javaVersionPin) — then a release the build
// (manifest) compiles for, else the reviewed 21. A release below 17 in the
// build is only the bytecode it targets, which a 17 compiler still writes
// and a 17 JRE runs; target returns it.
func chooseJVMLanguageJDK(pin toolchainPin, build []byte, manifest string) (jdk, source, target string, err error) {
	selected := ""
	if pin.version != "" {
		selected, source = pin.version, pin.source
	} else if match := sbtJavaReleaseRE.FindSubmatch(build); match != nil {
		selected, source = firstNonEmpty(string(match[1]), string(match[2]), string(match[3])), manifest
		if release, err := strconv.Atoi(selected); err == nil && release < 17 {
			return "17", source, selected, nil
		}
	}
	if selected == "" {
		return "21", "", "", nil
	}
	if !slices.Contains(jvmLanguageJDKs, selected) {
		return "", "", "", fmt.Errorf("%w: the recipe builds on Java %s; %s names %s — use a Dockerfile for other releases",
			ErrUnsupportedBuilder, strings.Join(jvmLanguageJDKs, ", "), source, selected)
	}
	return selected, source, "", nil
}

// sbtStagedProject finds the project sbt stage packages: the root, or the
// one subproject whose settings enable the packager.
func sbtStagedProject(build string) (dir, name string) {
	definitions := sbtProjectRE.FindAllStringSubmatchIndex(build, -1)
	if len(definitions) == 0 {
		name = "app"
		if match := sbtNameRE.FindStringSubmatch(build); match != nil {
			name = match[1]
		}
		if match := sbtScriptNameRE.FindStringSubmatch(build); match != nil {
			return "", match[1]
		}
		return "", sbtNormalizedName(name)
	}
	chosen := -1
	for index, definition := range definitions {
		end := len(build)
		if index+1 < len(definitions) {
			end = definitions[index+1][0]
		}
		if sbtStageEnabledRE.MatchString(build[definition[0]:end]) {
			directory := firstNonEmpty(submatch(build, definition, 2), submatch(build, definition, 3))
			if chosen < 0 || directory == "." || directory == "" {
				chosen = index
			}
		}
	}
	if chosen < 0 {
		chosen = 0
	}
	definition := definitions[chosen]
	end := len(build)
	if chosen+1 < len(definitions) {
		end = definitions[chosen+1][0]
	}
	chunk := build[definition[0]:end]
	directory := path.Clean(firstNonEmpty(submatch(build, definition, 2), submatch(build, definition, 3), "."))
	id := submatch(build, definition, 1)
	name = id
	if directory == "." && submatch(build, definition, 2) == "" && submatch(build, definition, 3) == "" {
		// `lazy val x = project` sits in a directory named after the val.
		directory = id
	}
	// Settings written at the top of build.sbt, outside every project
	// definition, apply to the root project.
	own := chunk
	if directory == "." {
		own = build[:definitions[0][0]] + "\n" + chunk
	}
	if match := sbtNameRE.FindStringSubmatch(own); match != nil {
		name = match[1]
	}
	if match := sbtScriptNameRE.FindStringSubmatch(own); match != nil {
		return strings.TrimPrefix(directory, "."), match[1]
	}
	if directory == "." {
		directory = ""
	}
	return directory, sbtNormalizedName(name)
}

func submatch(text string, indexes []int, group int) string {
	if 2*group+1 >= len(indexes) || indexes[2*group] < 0 {
		return ""
	}
	return text[indexes[2*group]:indexes[2*group+1]]
}

// sbtNormalizedName is sbt's normalizedName, which names the start script.
func sbtNormalizedName(name string) string {
	return strings.Trim(sbtNonWordRE.ReplaceAllString(strings.ToLower(name), "-"), "-")
}

// jvmLanguageProject is what the recipe and detection read from an sbt,
// Leiningen or deps.edn root.
type jvmLanguageProject struct {
	recipe jvmLanguageRecipe
	source string
	// target is a lower Java release the build compiles for.
	target string
	err    error
}

func readScalaProject(root string) jvmLanguageProject {
	build := string(readRecipeFile(root, "build.sbt", 256<<10))
	plugins := string(readRecipeFile(root, "project/plugins.sbt", 64<<10))
	project := jvmLanguageProject{recipe: jvmLanguageRecipe{language: "scala", sbt: "1"}}
	if match := sbtVersionRE.FindStringSubmatch(string(readRecipeFile(root, "project/build.properties", 4096))); match != nil && strings.HasPrefix(match[1], "2.") {
		project.recipe.sbt = "2"
	}
	project.recipe.jdk, project.source, project.target, project.err = chooseJVMLanguageJDK(jvmLanguageJavaPin(root), []byte(build), "build.sbt")
	if project.err != nil {
		return project
	}
	project.recipe.play = sbtPlayPluginRE.MatchString(plugins) || sbtPlayEnabledRE.MatchString(build)
	switch {
	case (project.recipe.play || sbtPackagerRE.MatchString(plugins)) && sbtStageEnabledRE.MatchString(build):
		project.recipe.tool = "sbt-stage"
		project.recipe.stage, project.recipe.script = sbtStagedProject(build)
		if project.recipe.stage != "" && (!safeRelativePath(project.recipe.stage) || !sbtStageDirRE.MatchString(project.recipe.stage)) {
			project.err = fmt.Errorf("%w: build.sbt packages a project whose directory is not a plain relative path; use a Dockerfile", ErrUnsupportedBuilder)
		} else if !sbtScriptFileRE.MatchString(project.recipe.script) {
			project.err = fmt.Errorf("%w: build.sbt names a start script that is not a plain file name; use a Dockerfile", ErrUnsupportedBuilder)
		}
	case sbtAssemblyRE.MatchString(plugins):
		project.recipe.tool = "sbt-assembly"
	default:
		project.err = fmt.Errorf("%w: sbt builds no runnable package here; enable sbt-native-packager (enablePlugins(JavaAppPackaging)) or add sbt-assembly in project/plugins.sbt", ErrUnsupportedBuilder)
	}
	return project
}

func readClojureProject(root string) jvmLanguageProject {
	project := jvmLanguageProject{recipe: jvmLanguageRecipe{language: "clojure"}}
	var build []byte
	switch {
	case regularExists(root, "project.clj"):
		build = readRecipeFile(root, "project.clj", 256<<10)
		project.recipe.tool = "lein"
		switch match := leinMainRE.FindSubmatch(build); {
		case match == nil:
			project.err = fmt.Errorf("%w: project.clj names no :main, so the uberjar has no entry point java -jar can start", ErrUnsupportedBuilder)
		case len(match[1]) > 0 && !leinAOTRE.Match(build):
			project.err = fmt.Errorf("%w: project.clj skips ahead-of-time compilation for :main and never compiles it; add :aot :all to the :uberjar profile", ErrUnsupportedBuilder)
		}
	case regularExists(root, "deps.edn"):
		build = readRecipeFile(root, "deps.edn", 256<<10)
		project.recipe.tool = "tools-deps"
		match := cljUberTaskRE.FindSubmatch(readRecipeFile(root, "build.clj", 256<<10))
		if !cljBuildAliasRE.Match(build) || match == nil {
			project.err = fmt.Errorf("%w: deps.edn builds no uberjar; add a :build alias with tools.build and a build.clj whose uber function calls b/uber", ErrUnsupportedBuilder)
		} else {
			project.recipe.task = string(match[1])
		}
	default:
		project.err = fmt.Errorf("%w: Clojure recipe requires project.clj or deps.edn", ErrUnsupportedBuilder)
	}
	if project.err != nil {
		return project
	}
	manifest := "project.clj"
	if project.recipe.tool == "tools-deps" {
		manifest = "deps.edn"
	}
	project.recipe.jdk, project.source, project.target, project.err = chooseJVMLanguageJDK(jvmLanguageJavaPin(root), build, manifest)
	return project
}

// jvmLanguageJavaPin is the JDK the version files at a Scala or Clojure
// root pin: .java-version, .sdkmanrc, .tool-versions, mise.toml or
// system.properties.
func jvmLanguageJavaPin(root string) toolchainPin {
	files := newBuildFiles(root)
	defer files.close()
	pin, _ := javaVersionPin(files, ".")
	return pin
}

func selectJVMLanguageRecipe(root, kind string) (jvmLanguageRecipe, error) {
	var project jvmLanguageProject
	if kind == "scala" {
		if !regularExists(root, "build.sbt") {
			return jvmLanguageRecipe{}, fmt.Errorf("%w: Scala recipe requires build.sbt", ErrUnsupportedBuilder)
		}
		project = readScalaProject(root)
	} else {
		project = readClojureProject(root)
	}
	return project.recipe, project.err
}

// jvmLanguageCandidate is the recipe candidate for an sbt, Leiningen or
// deps.edn root.
func jvmLanguageCandidate(buildRoot, root string, match *ecosystemMatch) DetectedCandidate {
	var project jvmLanguageProject
	manifest := "build.sbt"
	if match.recipe == "scala" {
		project = readScalaProject(buildRoot)
	} else if project = readClojureProject(buildRoot); project.recipe.tool == "tools-deps" {
		manifest = "deps.edn"
	} else {
		manifest = "project.clj"
	}
	candidate := DetectedCandidate{
		Name: match.label + " in " + rootLabelOf(root), Profile: match.profile, Confidence: ConfidenceHigh,
		Framework: match.framework, Recipe: match.recipe, Port: match.port,
		StartCommand:  project.recipe.start(),
		Evidence:      []DetectionEvidence{},
		NeedsDecision: []string{},
		Toolchain:     &DetectedToolchain{Language: match.recipe, Release: project.recipe.jdk, From: project.source, Tool: project.recipe.tool},
	}
	if project.recipe.play {
		candidate.Framework, candidate.Profile, candidate.Port = "play", ProfileWeb, 9000
		candidate.Name = "Play application in " + rootLabelOf(root)
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(root, "project/plugins.sbt"),
			Reason: "Play listens on http.port, which the start command sets from PORT, and reads its secret from APPLICATION_SECRET through " + playSecretConfig})
	}
	if project.err != nil {
		candidate.RecipeIssue = recipeRefusalText(project.err, "")
		candidate.Confidence = ConfidenceLow
	} else {
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(root, manifest), Reason: project.recipe.toolchain()})
		if project.target != "" {
			candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(root, manifest),
				Reason: "compiles for Java " + project.target + ", which Java " + project.recipe.jdk + " builds and runs"})
		}
	}
	if candidate.Profile != ProfileWeb {
		if candidate.Confidence == ConfidenceHigh {
			candidate.Confidence = ConfidenceMedium
		}
		candidate.NeedsDecision = append(candidate.NeedsDecision, "confirm whether this program serves HTTP (web application) or runs as a worker, and its port")
	}
	return candidate
}

func renderJVMLanguageDockerfile(recipe jvmLanguageRecipe, config BuildPlanConfig, bases []ResolvedImage, installSecrets, buildSecrets string) ([]string, error) {
	builder, err := resolveCatalogueImage(bases, recipe.builderImage())
	if err != nil {
		return nil, err
	}
	runtime, err := resolveCatalogueImage(bases, recipe.runtimeImage())
	if err != nil {
		return nil, err
	}
	lines := []string{"FROM " + immutableImageReference(builder) + " AS build", "WORKDIR /src", "COPY . ."}
	build := strings.TrimSpace(config.BuildCommand)
	var fetch, output string
	switch recipe.tool {
	case "sbt-stage":
		fetch, output = "sbt -batch update", "RUN test -d "+path.Join(firstNonEmpty(recipe.stage, "."), "target/universal/stage/bin")+" || (echo 'sbt stage must write target/universal/stage' >&2; exit 1)"
		if build == "" {
			build = "sbt -batch stage"
		}
	case "sbt-assembly":
		fetch, output = "sbt -batch update", largestJarLine("sbt assembly", "target", "*/target")
		if build == "" {
			build = "sbt -batch assembly"
		}
	case "lein":
		fetch, output = "lein deps", largestJarLine("lein uberjar", "target")
		if build == "" {
			build = "lein uberjar"
		}
	default:
		fetch, output = "clojure -P && clojure -P -T:build", largestJarLine("clojure -T:build "+recipe.task, "target")
		if build == "" {
			build = "clojure -T:build " + recipe.task
		}
	}
	lines = append(lines, "RUN "+installSecrets+fetch, "RUN "+buildSecrets+build, output)
	lines = append(lines, "FROM "+immutableImageReference(runtime))
	if recipe.tool == "sbt-stage" {
		lines = append(lines, unprivilegedDebianUser, "WORKDIR /app",
			"COPY --from=build --chown=10001:10001 /src/"+path.Join(firstNonEmpty(recipe.stage, "."), "target/universal/stage")+"/ /app/")
		if recipe.play {
			quoted := make([]string, 0, len(playSecretConfigLines))
			for _, line := range playSecretConfigLines {
				quoted = append(quoted, "'"+line+"'")
			}
			lines = append(lines, "RUN mkdir -p "+path.Dir(playSecretConfig)+" && printf '%s\\n' "+strings.Join(quoted, " ")+" > "+playSecretConfig)
		}
		lines = append(lines, "USER 10001")
	} else {
		lines = append(lines, "RUN adduser -D -u 10001 app && mkdir -p /app/data && chown app:app /app /app/data", "USER app", "WORKDIR /app",
			"COPY --from=build /out/app.jar /app/app.jar")
	}
	// The JVM sizes its heap from the container's limit rather than the host's memory.
	lines = append(lines, `ENV JAVA_TOOL_OPTIONS="-XX:MaxRAMPercentage=75"`)
	start := strings.TrimSpace(config.StartCommand)
	if start == "" {
		start = recipe.start()
	}
	return append(lines, shellCMD(start)), nil
}
