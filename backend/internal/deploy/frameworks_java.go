package deploy

import (
	"fmt"
	"regexp"
	"strings"
)

// The Java recipe builds with Maven or Gradle on a Temurin JDK and runs the
// one executable jar the build produces on the matching JRE. Versions are the
// LTS releases the catalogue's images carry.
var (
	javaRecipeVersions = map[string]bool{"11": true, "17": true, "21": true, "25": true}
	javaPomVersionRE   = regexp.MustCompile(`<(?:java\.version|maven\.compiler\.(?:release|source|target)|release)>\s*(?:1\.)?([0-9]{1,2})\s*<`)
	javaGradleRE       = regexp.MustCompile(`(?:JavaLanguageVersion\.of\(|jvmToolchain\(|VERSION_|sourceCompatibility\s*=\s*['"]?(?:1\.)?|toolchain\s*\{[^}]*languageVersion[^}]*of\()\s*([0-9]{1,2})`)
	javaVersionFileRE  = regexp.MustCompile(`\b(?:1\.)?([0-9]{1,2})\b`)
	javaModulesRE      = regexp.MustCompile(`<modules>`)
)

var javaFrameworks = []struct {
	marker, name string
	port         int
}{
	{"spring-boot", "spring-boot", 8080}, {"quarkus", "quarkus", 8080}, {"micronaut", "micronaut", 8080},
	{"io.javalin", "javalin", 7070}, {"io.ktor", "ktor", 8080}, {"io.helidon", "helidon", 8080}, {"io.vertx", "vertx", 8080},
}

// chooseJavaRecipeVersion reads the language level a project declares — a
// `.java-version` file, then the pom's compiler properties, then Gradle's
// toolchain or compatibility settings — and maps it to a catalogue release.
func chooseJavaRecipeVersion(versionFile, pom, gradle string) (string, error) {
	selected := ""
	if match := javaVersionFileRE.FindStringSubmatch(firstMeaningfulLine(versionFile)); match != nil {
		selected = match[1]
	}
	if selected == "" {
		if match := javaPomVersionRE.FindStringSubmatch(pom); match != nil {
			selected = match[1]
		}
	}
	if selected == "" {
		if match := javaGradleRE.FindStringSubmatch(gradle); match != nil {
			selected = match[1]
		}
	}
	if selected == "" {
		return "21", nil
	}
	if !javaRecipeVersions[selected] {
		return "", fmt.Errorf("%w: the Java recipe builds on Java 11, 17, 21 or 25; this project declares %s — use a Dockerfile for other releases", ErrUnsupportedBuilder, selected)
	}
	return selected, nil
}

func firstMeaningfulLine(content string) string {
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line != "" && !strings.HasPrefix(line, "#") {
			return line
		}
	}
	return ""
}

// javaCandidate builds the candidate for a root with a Maven pom or a Gradle
// build script.
func javaCandidate(marker *detectedMarkers, rootLabel string) DetectedCandidate {
	tool, manifest := "maven", "pom.xml"
	if len(marker.pomXML) == 0 {
		tool = "gradle"
		manifest = marker.gradleBuildPath
	}
	candidate := DetectedCandidate{
		Name: "Java service in " + rootLabel, Profile: ProfileWorker, Confidence: ConfidenceMedium,
		Framework: "java", Recipe: "java",
		Evidence:      []DetectionEvidence{{Path: joinRoot(marker.root, manifest), Reason: strings.ToUpper(tool[:1]) + tool[1:] + " build definition"}},
		NeedsDecision: []string{},
	}
	build := string(marker.pomXML)
	if tool == "gradle" {
		build = string(marker.gradleBuild)
	}
	if _, err := chooseJavaRecipeVersion(string(marker.javaVersionFile), string(marker.pomXML), string(marker.gradleBuild)); err != nil {
		candidate.RecipeIssue = err.Error()
	}
	if tool == "maven" && javaModulesRE.MatchString(build) {
		candidate.Confidence = ConfidenceLow
		candidate.RecipeIssue = "multi-module Maven project; set the root directory to the module that builds the application, or use a Dockerfile"
	}
	for _, framework := range javaFrameworks {
		if strings.Contains(build, framework.marker) {
			candidate.Framework = framework.name
			candidate.Profile, candidate.Port = ProfileWeb, framework.port
			candidate.Confidence = ConfidenceHigh
			candidate.Name = framework.name + " application in " + rootLabel
			candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, manifest), Reason: framework.name + " dependency; listens on " + fmt.Sprint(framework.port) + " by convention"})
			break
		}
	}
	if candidate.Framework == "java" {
		candidate.NeedsDecision = append(candidate.NeedsDecision, "confirm whether this application serves HTTP (web application) or runs as a worker, and its port")
	}
	if tool == "gradle" && !marker.gradlew {
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, manifest), Reason: "no Gradle wrapper; the image's own Gradle builds it"})
	}
	if procfileWeb := procfileProcess(marker.procfile, "web"); procfileWeb != "" && rejectPlanSecretLiteral("Procfile web process", procfileWeb) == nil {
		candidate.StartCommand = procfileWeb
		candidate.Profile = ProfileWeb
		if candidate.Port == 0 {
			candidate.Port = 8080
		}
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, "Procfile"), Reason: "web process: " + boundedEvidence(procfileWeb)})
	}
	if candidate.RecipeIssue != "" && candidate.Confidence == ConfidenceHigh {
		candidate.Confidence = ConfidenceMedium
	}
	return candidate
}

// javaFramework is the first catalogue framework a build definition names.
func javaFramework(build string) string {
	for _, framework := range javaFrameworks {
		if strings.Contains(build, framework.marker) {
			return framework.name
		}
	}
	return ""
}

type javaRecipe struct {
	tool    string
	version string
	wrapper bool
	// framework decides the start command's port bridge and proxy trust.
	framework string
}

func selectJavaRecipe(root string) (javaRecipe, error) {
	pom, _ := readContainedRegular(root, "pom.xml", 512<<10)
	gradle := []byte{}
	for _, name := range []string{"build.gradle.kts", "build.gradle"} {
		if regularExists(root, name) {
			gradle, _ = readContainedRegular(root, name, 512<<10)
			break
		}
	}
	versionFile, _ := readContainedRegular(root, ".java-version", 4096)
	switch {
	case len(pom) > 0:
		if javaModulesRE.Match(pom) {
			return javaRecipe{}, fmt.Errorf("%w: multi-module Maven project; set the root directory to the module that builds the application or use a Dockerfile", ErrUnsupportedBuilder)
		}
	case regularExists(root, "build.gradle") || regularExists(root, "build.gradle.kts"):
	default:
		return javaRecipe{}, fmt.Errorf("%w: Java recipe requires pom.xml or a build.gradle script", ErrUnsupportedBuilder)
	}
	version, err := chooseJavaRecipeVersion(string(versionFile), string(pom), string(gradle))
	if err != nil {
		return javaRecipe{}, err
	}
	framework := javaFramework(string(pom) + string(gradle))
	if len(pom) > 0 {
		return javaRecipe{tool: "maven", version: version, framework: framework}, nil
	}
	return javaRecipe{tool: "gradle", version: version, wrapper: regularExists(root, "gradlew"), framework: framework}, nil
}

func renderJavaDockerfile(recipe javaRecipe, config BuildPlanConfig, bases []ResolvedImage, installSecrets, buildSecrets string) ([]string, error) {
	if len(bases) != 2 {
		return nil, ErrBuilderUnavailable
	}
	build := strings.TrimSpace(config.BuildCommand)
	jars, tool := "target", "Maven"
	lines := []string{"FROM " + immutableImageReference(bases[0]) + " AS build", "WORKDIR /src", "COPY . ."}
	if recipe.tool == "maven" {
		if build == "" {
			build = "mvn -q -B -DskipTests package"
		}
		lines = append(lines, "RUN "+installSecrets+"mvn -q -B dependency:resolve || true")
	} else {
		tool, jars = "Gradle", "build/libs"
		runner := "gradle"
		if recipe.wrapper {
			runner = "chmod +x ./gradlew && ./gradlew"
		}
		if build == "" {
			build = runner + " --no-daemon -q build -x test"
		}
	}
	lines = append(lines,
		"RUN "+buildSecrets+build,
		// The one executable jar: not a sources, javadoc, plain (Spring Boot's
		// unrepackaged) or original (the pre-shade) artifact.
		"RUN mkdir -p /out && cp \"$(ls "+jars+"/*.jar 2>/dev/null | grep -v -e '-sources' -e '-javadoc' -e '-plain' -e '/original-' | head -n 1)\" /out/app.jar || (echo '"+tool+" build produced no executable jar in "+jars+"/' >&2; exit 1)",
		"FROM "+immutableImageReference(bases[1]),
		"RUN adduser -D -u 10001 app",
		"USER app",
		"WORKDIR /app",
		"COPY --from=build /out/app.jar /app/app.jar",
		// The JVM sizes its heap from the container's limit rather than the host's memory.
		`ENV JAVA_TOOL_OPTIONS="-XX:MaxRAMPercentage=75"`,
	)
	if trust := trustEnvironment(recipeProxyTrust("java", recipe.framework)); len(trust) > 0 {
		lines = append(lines, "ENV "+strings.Join(trust, " "))
	}
	if strings.TrimSpace(config.StartCommand) == "" && javaRuntimeStart(recipe.framework) != "" {
		lines = append(lines, shellCMD(javaRuntimeStart(recipe.framework)))
	} else if strings.TrimSpace(config.StartCommand) == "" {
		lines = append(lines, `CMD ["java","-jar","/app/app.jar"]`)
	} else {
		lines = append(lines, shellCMD(config.StartCommand))
	}
	return lines, nil
}
