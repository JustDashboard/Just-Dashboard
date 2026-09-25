package deploy

import (
	"fmt"
	"strconv"
	"strings"
)

// The Java recipe builds a Maven or Gradle project from the directory its
// build runs from (detect_jvm.go) with the task that produces its runnable
// artifact — never `build`, which also runs the checks and linters — and runs
// that artifact on a Temurin JRE as an unprivileged user.

type javaRecipe struct {
	project jvmProject
	plan    javaToolchainPlan
	// member is the project's directory under the build context, "" when
	// the build runs from the project itself, and reactor the directory
	// under it the build runs from: the aggregator POM's, the settings
	// root's.
	member  string
	reactor string
}

func selectJavaRecipe(boundary, root string, config BuildPlanConfig) (javaRecipe, error) {
	files := newBuildFiles(boundary)
	defer files.close()
	project := newJVMReader(files).project(checkoutPath(boundary, root))
	if project == nil {
		return javaRecipe{}, fmt.Errorf("%w: Java recipe requires pom.xml or a build.gradle script", ErrUnsupportedBuilder)
	}
	if project.aggregator && project.packaging == "" && !project.member {
		what := "aggregates Maven modules"
		if project.tool == "gradle" {
			what = "is the root of a multi-project Gradle build"
		}
		return javaRecipe{}, fmt.Errorf("%w: %s %s and packages no application itself; set the root directory to the module that does — the recipe builds it from here", ErrUnsupportedBuilder, project.buildFile, what)
	}
	if project.android != "" {
		return javaRecipe{}, fmt.Errorf("%w: %s; use a Dockerfile that installs the Android command-line tools", ErrUnsupportedBuilder, project.android)
	}
	recipe := javaRecipe{project: *project, member: relativeBuildPath(project.context, project.root), reactor: relativeBuildPath(project.context, project.reactor)}
	for _, written := range []string{recipe.member, recipe.reactor, project.mavenSettings} {
		if written != "" && !recipePath(written) {
			return javaRecipe{}, fmt.Errorf("%w: the Java recipe writes %q into the Dockerfile, and it holds characters it does not quote; rename the directory or use a Dockerfile", ErrUnsupportedBuilder, written)
		}
	}
	if selector := strings.TrimPrefix(project.module, ":"); selector != "" && !recipeNames(selector, ":") && !recipePath(selector) {
		return javaRecipe{}, fmt.Errorf("%w: the Java recipe selects %q on the command line, and it holds characters it does not quote; use a Dockerfile", ErrUnsupportedBuilder, project.module)
	}
	plan, err := planJavaToolchain(project.toolchain, config.JavaVersion)
	if err != nil {
		return javaRecipe{}, err
	}
	recipe.plan = plan
	return recipe, nil
}

// javaRecipeBases lists the images the recipe resolves, in the order the
// Dockerfile references them: the build image, the runtime JRE, then the
// toolchain JDK copied beside the build's own when there is one.
func javaRecipeBases(recipe javaRecipe) []string {
	bases := []string{recipe.plan.buildImage(recipe.project.toolchain.wrapperUsable), recipe.plan.runtimeImage()}
	if image := recipe.plan.provideImage(); image != "" {
		bases = append(bases, image)
	}
	return bases
}

// javaToolchainLabel is the build evidence's toolchain: "java 21 (maven)",
// "java 17 (gradle 8.5 on java 21)" for a toolchain provided beside the JDK
// the wrapper runs on.
func javaToolchainLabel(recipe javaRecipe) string {
	label := "java " + strconv.Itoa(recipe.plan.release) + " (" + recipe.project.tool
	if recipe.project.tool == "gradle" {
		switch {
		case recipe.project.toolchain.wrapperUsable && recipe.project.toolchain.wrapper != "":
			label += " " + recipe.project.toolchain.wrapper
		case recipe.project.toolchain.wrapperUsable:
			label += " wrapper"
		default:
			label += " " + recipe.plan.gradleImage
		}
		if recipe.plan.runner != recipe.plan.release {
			label += " on java " + strconv.Itoa(recipe.plan.runner)
		}
	}
	return label + ")"
}

// javaTask is the Gradle task that produces a packaging's artifact.
func javaTask(packaging string) string {
	switch packaging {
	case javaPackagingSpringBoot:
		return "bootJar"
	case javaPackagingSpringBootWar:
		return "bootWar"
	case javaPackagingQuarkus, javaPackagingQuarkusUber:
		return "quarkusBuild"
	case javaPackagingKtor:
		return "buildFatJar"
	case javaPackagingShadow:
		return "shadowJar"
	case javaPackagingApplication:
		return "installDist"
	case javaPackagingJar:
		return "jar"
	}
	return "assemble"
}

// javaBuildCommand is the recipe's own build: Maven's package phase for the
// module and the modules it needs, or the Gradle task of the project's
// packaging, qualified by its project path so no other project builds.
func javaBuildCommand(recipe javaRecipe) string {
	project := recipe.project
	if project.tool == "maven" {
		parts := []string{"mvn"}
		if project.mavenWrapper {
			parts[0] = "./mvnw"
		}
		parts = append(parts, "-B", "-ntp", "-DskipTests")
		if len(project.profiles) > 0 {
			parts = append(parts, "-P"+strings.Join(project.profiles, ","))
		}
		if project.mavenSettings != "" {
			parts = append(parts, "-s", project.mavenSettings)
		}
		if recipe.reactor != "" {
			parts = append(parts, "-f", recipe.reactor+"/pom.xml")
		} else if project.reactor == project.root && recipe.member != "" {
			parts = append(parts, "-f", recipe.member+"/pom.xml")
		}
		if project.module != "" {
			parts = append(parts, "-pl", project.module, "-am")
		}
		return strings.Join(append(parts, "package"), " ")
	}
	runner := "gradle"
	if project.toolchain.wrapperUsable {
		runner = "./" + gradleWrapperPath(recipe)
	}
	parts := []string{runner, "--no-daemon", "--console=plain"}
	if recipe.reactor != "" {
		parts = append(parts, "-p", recipe.reactor)
	}
	if recipe.plan.provide > 0 {
		parts = append(parts, "-Porg.gradle.java.installations.paths=/opt/jdk-"+strconv.Itoa(recipe.plan.provide))
	}
	for _, property := range project.profiles {
		parts = append(parts, "-P"+property)
	}
	task := javaTask(project.packaging)
	if project.module == ":" || project.module == "" {
		task = ":" + task
	} else {
		task = project.module + ":" + task
	}
	return strings.Join(append(parts, task), " ")
}

// gradleWrapperPath is the settings root's gradlew under the build context.
func gradleWrapperPath(recipe javaRecipe) string {
	if recipe.reactor == "" {
		return "gradlew"
	}
	return recipe.reactor + "/gradlew"
}

// javaArtifactDir is where a packaging's artifact lands, under the
// project's own directory.
func javaArtifactDir(recipe javaRecipe) string {
	dir := "target"
	if recipe.project.tool == "gradle" {
		dir = "build/libs"
		switch recipe.project.packaging {
		case javaPackagingQuarkus:
			dir = "build/quarkus-app"
		case javaPackagingQuarkusUber:
			dir = "build"
		case javaPackagingApplication:
			dir = "build/install"
		}
	} else if recipe.project.packaging == javaPackagingQuarkus {
		dir = "target/quarkus-app"
	}
	if recipe.member != "" {
		dir = recipe.member + "/" + dir
	}
	return dir
}

// javaArtifactGlobs are the file names tried in order for a packaging's
// executable archive; each one is still checked for a Main-Class, so a thin
// jar or a library jar is never chosen whatever sorts first.
func javaArtifactGlobs(packaging string) []string {
	switch packaging {
	case javaPackagingSpringBootWar:
		return []string{"*.war"}
	case javaPackagingQuarkusUber:
		return []string{"*-runner.jar"}
	case javaPackagingKtor, javaPackagingShadow:
		return []string{"*-all.jar", "*.jar"}
	case javaPackagingFatJar:
		return []string{"*-jar-with-dependencies.jar", "*-shaded.jar", "*-fat.jar", "*-all.jar", "*-runner.jar", "*-exec.jar", "*.jar"}
	}
	return []string{"*.jar"}
}

// javaLaunch is the command that starts the artifact in the runtime image.
func javaLaunch(packaging string) string {
	switch packaging {
	case javaPackagingSpringBootWar:
		return "java -jar /app/app.war"
	case javaPackagingQuarkus:
		return "java -jar /app/quarkus-app/quarkus-run.jar"
	case javaPackagingApplication:
		return "/app/bin/app"
	}
	return "java -jar /app/app.jar"
}

// javaArtifactLines copy the runnable artifact into /out: the Quarkus
// application directory, the application plugin's distribution, or the one
// archive whose manifest names a Main-Class, with the libs directory a thin
// jar's Class-Path points at.
func javaArtifactLines(recipe javaRecipe) []string {
	dir := javaArtifactDir(recipe)
	tool := "Maven"
	if recipe.project.tool == "gradle" {
		tool = "Gradle"
	}
	switch recipe.project.packaging {
	case javaPackagingQuarkus:
		return []string{"RUN test -f /src/" + dir + "/quarkus-run.jar && mkdir -p /out && cp -R /src/" + dir + " /out/quarkus-app || (echo '" + tool + " build must produce " + dir + "/quarkus-run.jar; the Quarkus plugin writes it' >&2; exit 1)"}
	case javaPackagingApplication:
		// The start script is named for the project; it is renamed so the
		// image always starts /app/bin/app.
		return []string{`RUN cd /src/` + dir + ` && set -- */ && [ "$#" -eq 1 ] && [ -d "$1" ] && name="$(basename "$1")" && mkdir -p /out && cp -R "$1". /out/ && ` +
			`rm -f /out/bin/*.bat && { [ "$name" = app ] || mv "/out/bin/$name" /out/bin/app; } && [ -x /out/bin/app ] || (echo '` + tool + ` build must produce ` + dir + `/; installDist writes the application there' >&2; exit 1)`}
	}
	target := "/out/app.jar"
	if recipe.project.packaging == javaPackagingSpringBootWar {
		target = "/out/app.war"
	}
	return []string{`RUN cd /src/` + dir + ` && (for f in ` + strings.Join(javaArtifactGlobs(recipe.project.packaging), " ") + `; do ` +
		`case "$f" in *-sources.jar|*-javadoc.jar|*-tests.jar|*-test-fixtures.jar|*-plain.jar|*-plain.war|original-*) continue ;; esac; ` +
		`[ -f "$f" ] || continue; ` +
		`rm -rf /tmp/manifest && mkdir -p /tmp/manifest && (cd /tmp/manifest && jar xf "/src/` + dir + `/$f" META-INF/MANIFEST.MF) && grep -qi '^Main-Class:' /tmp/manifest/META-INF/MANIFEST.MF || continue; ` +
		`mkdir -p /out && cp "$f" ` + target + ` && for d in lib libs; do if [ -d "$d" ]; then cp -R "$d" /out/; fi; done && exit 0; ` +
		`done; exit 1) || (echo '` + tool + ` build produced no executable jar in ` + dir + `/' >&2; exit 1)`}
}

func renderJavaDockerfile(recipe javaRecipe, config BuildPlanConfig, bases []ResolvedImage, installSecrets, buildSecrets string) ([]string, error) {
	want := 2
	if recipe.plan.provide > 0 {
		want = 3
	}
	if len(bases) != want {
		return nil, ErrBuilderUnavailable
	}
	project := recipe.project
	lines := []string{"FROM " + immutableImageReference(bases[0]) + " AS build"}
	if recipe.plan.provide > 0 {
		// The toolchain the build declares, beside the JDK Gradle runs on;
		// the build points Gradle at it rather than letting it download one.
		lines = append(lines, "COPY --from="+immutableImageReference(bases[2])+" /opt/java/openjdk /opt/jdk-"+strconv.Itoa(recipe.plan.provide))
	}
	lines = append(lines, "WORKDIR /src", "COPY . .")
	// A wrapper committed from Windows starts with `#!/bin/sh\r`, which runs
	// as "sh\r: not found"; the CR is stripped before it is used.
	switch {
	case project.tool == "gradle" && project.toolchain.wrapperUsable:
		wrapper := gradleWrapperPath(recipe)
		lines = append(lines, `RUN sed -i 's/\r$//' `+wrapper+` && chmod +x `+wrapper)
	case project.tool == "maven" && project.mavenWrapper:
		lines = append(lines, `RUN sed -i 's/\r$//' mvnw && chmod +x mvnw`)
	}
	build := strings.TrimSpace(config.BuildCommand)
	if build == "" {
		build = javaBuildCommand(recipe)
	}
	// Maven and Gradle resolve dependencies as the build runs rather than in
	// a step of their own, so a registry credential mapped to the install
	// reaches the build that downloads with it.
	lines = append(lines, "RUN "+javaSecretMounts(config.Secrets)+build)
	lines = append(lines, javaArtifactLines(recipe)...)
	lines = append(lines,
		"FROM "+immutableImageReference(bases[1]),
		"RUN useradd --uid 10001 --create-home --shell /usr/sbin/nologin app",
		"USER app",
		"WORKDIR /app",
		"COPY --from=build /out/ /app/",
		// The JVM sizes its heap from the container's limit rather than the host's memory.
		`ENV JAVA_TOOL_OPTIONS="-XX:MaxRAMPercentage=75"`,
	)
	if trust := trustEnvironment(recipeProxyTrust("java", project.framework)); len(trust) > 0 {
		lines = append(lines, "ENV "+strings.Join(trust, " "))
	}
	launch := javaLaunch(project.packaging)
	switch {
	case strings.TrimSpace(config.StartCommand) != "":
		lines = append(lines, shellCMD(config.StartCommand))
	case javaRuntimeStart(project.framework, launch) != "":
		lines = append(lines, shellCMD(javaRuntimeStart(project.framework, launch)))
	default:
		encoded := `["` + strings.Join(strings.Fields(launch), `","`) + `"]`
		lines = append(lines, "CMD "+encoded)
	}
	return lines, nil
}

// javaSecretMounts mounts every build variable mapped to the install, to
// the build, or to both, once each.
func javaSecretMounts(secrets []BuildSecretConfig) string {
	reaching := []BuildSecretConfig{}
	for _, secret := range secrets {
		if buildSecretReaches(secret.Step, "install") || buildSecretReaches(secret.Step, "build") {
			reaching = append(reaching, BuildSecretConfig{Variable: secret.Variable, Step: "build"})
		}
	}
	return buildSecretMounts(reaching, "build")
}

// javaRecipeNotes are the decisions preparation made that the run log
// states: which module the build selects and from where, a Java release
// mapped or provided beside another, a wrapper that could not run.
func javaRecipeNotes(recipe javaRecipe) []string {
	project := recipe.project
	var notes []string
	if recipe.member != "" {
		build := "the Maven build"
		if project.tool == "gradle" {
			build = "the Gradle build"
		}
		notes = append(notes, "Builds "+firstNonEmpty(project.module, recipe.member)+" ("+recipe.member+") from "+build+" in "+contextLabel(displayDir(project.context)))
	}
	if recipe.plan.mapped != "" {
		notes = append(notes, recipe.plan.mapped)
	}
	if recipe.plan.provide > 0 {
		notes = append(notes, "Gradle runs on Java "+strconv.Itoa(recipe.plan.runner)+"; the toolchain the build declares, Java "+
			strconv.Itoa(recipe.plan.provide)+", is provided at /opt/jdk-"+strconv.Itoa(recipe.plan.provide))
	}
	notes = append(notes, recipe.plan.notes...)
	if project.wrapperJarMissing {
		notes = append(notes, "gradlew is committed without gradle/wrapper/gradle-wrapper.jar; the image's Gradle "+recipe.plan.gradleImage+" builds instead")
	}
	if len(project.profiles) > 0 {
		notes = append(notes, "Activates "+project.profilesBy+" (-P"+strings.Join(project.profiles, ",")+")")
	}
	return notes
}
