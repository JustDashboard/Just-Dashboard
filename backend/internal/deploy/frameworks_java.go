package deploy

import (
	"fmt"
	"strconv"
	"strings"
)

// javaFrameworks names a JVM web framework from the build definition as the
// passes read it — dependencies, plugin ids, and what version-catalog
// aliases resolve to (detect_jvm.go) — with the port it listens on by
// convention.
var javaFrameworks = []struct {
	marker, name string
	port         int
}{
	{"spring-boot", "spring-boot", 8080}, {"org.springframework.boot", "spring-boot", 8080},
	{"quarkus", "quarkus", 8080}, {"micronaut", "micronaut", 8080},
	{"io.javalin", "javalin", 7070}, {"io.ktor", "ktor", 8080}, {"io.helidon", "helidon", 8080}, {"io.vertx", "vertx", 8080},
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

// javaFramework is the first catalogue framework a build definition names.
func javaFramework(build string) string {
	for _, framework := range javaFrameworks {
		if strings.Contains(build, framework.marker) {
			return framework.name
		}
	}
	return ""
}

func javaFrameworkPort(name string) int {
	for _, framework := range javaFrameworks {
		if framework.name == name {
			return framework.port
		}
	}
	return 0
}

// javaCandidate builds the candidate for a root with a Maven pom or a Gradle
// build script, from what its build — the reactor or settings root that
// owns it included — says.
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
	project := marker.jvm
	if project == nil {
		// The walk read the build file, the project reader could not; the
		// recipe reads the same way, and says why when it prepares.
		project = &jvmProject{tool: tool, framework: javaFramework(string(marker.pomXML) + string(marker.gradleBuild))}
	}
	candidate.Evidence = append(candidate.Evidence, project.evidence...)
	if framework := project.framework; framework != "" {
		port := javaFrameworkPort(framework)
		candidate.Framework = framework
		candidate.Profile, candidate.Port = ProfileWeb, port
		candidate.Confidence = ConfidenceHigh
		candidate.Name = framework + " application in " + rootLabel
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, manifest), Reason: framework + " dependency; listens on " + fmt.Sprint(port) + " by convention"})
	}
	if project.packagingBy != "" {
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, manifest), Reason: "runnable artifact from " + project.packagingBy})
	}
	if candidate.Framework == "java" && !project.library {
		candidate.NeedsDecision = append(candidate.NeedsDecision, "confirm whether this application serves HTTP (web application) or runs as a worker, and its port")
	}
	if project.toolchain.tool != "" {
		if plan, err := planJavaToolchain(project.toolchain, ""); err == nil {
			reason := "builds and runs on Java " + strconv.Itoa(plan.release) + " (" + plan.from + ")"
			if plan.mapped != "" {
				reason = plan.mapped
			}
			candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, manifest), Reason: boundedEvidenceSentence(reason)})
		}
	}
	if tool == "gradle" && !project.toolchain.wrapperUsable {
		reason := "no Gradle wrapper; the image's own Gradle builds it"
		if project.wrapperJarMissing {
			reason = "gradlew is committed without gradle/wrapper/gradle-wrapper.jar; the image's own Gradle builds it"
		}
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(marker.root, manifest), Reason: reason})
	}
	if project.android != "" {
		candidate.RecipeIssue = boundedEvidenceSentence(project.android + "; use a Dockerfile that installs the Android command-line tools")
		candidate.Confidence = ConfidenceLow
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
	candidate.JavaBuild = project.detected()
	candidate.Variables = append(candidate.Variables, project.variables...)
	return candidate
}

// detected is the project as the candidate keeps it for preflight.
func (p *jvmProject) detected() *DetectedJavaBuild {
	if p.tool == "" {
		return nil
	}
	build := &DetectedJavaBuild{
		Tool: p.tool, Packaging: p.packaging, Runnable: p.runnable(), Library: p.library,
		Release: p.toolchain.declared, ReleaseFrom: p.toolchain.declaredFrom, Toolchain: p.toolchain.toolchain,
		Pinned: p.toolchain.pinned, PinnedFrom: p.toolchain.pinnedFrom,
		Wrapper: p.toolchain.wrapper, WrapperUsable: p.toolchain.wrapperUsable, WrapperJarMissing: p.wrapperJarMissing,
		Foojay: p.toolchain.foojay, Profiles: p.profiles, VaadinDevMode: p.vaadinDevMode,
		Aggregator: p.aggregator,
	}
	// Gradle is named by the settings root it runs from, which a composite
	// build's wider context holds.
	where := p.context
	if p.tool == "gradle" {
		where = p.reactor
	}
	if where != p.root || p.module != "" && p.module != ":" {
		build.Context = contextLabel(displayDir(where))
		if p.module != ":" {
			build.Module = p.module
		}
	}
	return build
}

// toolchainFacts turns what a candidate kept back into what the toolchain
// plan reads.
func (b *DetectedJavaBuild) toolchainFacts() javaToolchainFacts {
	return javaToolchainFacts{
		tool: b.Tool, declared: b.Release, declaredFrom: b.ReleaseFrom, toolchain: b.Toolchain,
		pinned: b.Pinned, pinnedFrom: b.PinnedFrom, wrapper: b.Wrapper, wrapperUsable: b.WrapperUsable, foojay: b.Foojay,
	}
}
