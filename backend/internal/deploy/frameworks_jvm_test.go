package deploy

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The JVM recipe reads a build the way Maven and Gradle do: a module from
// the reactor or settings root that owns it, catalog aliases resolved, the
// artifact its packaging plugin produces, and the Java release from the
// build and version files, on images that exist for the host.

func detectCompiled(t *testing.T, files map[string]string) (DetectionResult, string) {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		writeBuildFixture(t, root, name, content)
	}
	result, err := (Detector{}).DetectPath(context.Background(), root, SourceIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	return result, root
}

func compiledCandidate(result DetectionResult, root, recipe string) *DetectedCandidate {
	for index := range result.Candidates {
		if result.Candidates[index].Root == root && (recipe == "" || result.Candidates[index].Recipe == recipe) {
			return &result.Candidates[index]
		}
	}
	return nil
}

func prepareCompiled(t *testing.T, boundary, root string, config BuildPlanConfig) PreparedBuild {
	t.Helper()
	prepared, err := NewArtifactBuilder(&artifactBackendFake{}).PrepareWithin(context.Background(), boundary, filepath.Join(boundary, filepath.FromSlash(root)), config, false, "t:1")
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}

func assertCompiledDockerfile(t *testing.T, dockerfile string, want ...string) {
	t.Helper()
	for _, line := range want {
		if !strings.Contains(dockerfile, line) {
			t.Fatalf("Dockerfile missing %q:\n%s", line, dockerfile)
		}
	}
}

func TestJavaReleaseReadsEveryWayAVersionIsWritten(t *testing.T) {
	t.Parallel()
	for value, want := range map[string]int{
		"21": 21, "17.0.2": 17, "1.8": 8, "1.8.0_402": 8, "8u402": 8, "temurin-21.0.2+13": 21, "21.0.2-tem": 21,
		"openjdk64-17.0.1": 17, "zulu-11.66.15": 11, "corretto-17.0.4.9.1": 17, "graalvm-community-25": 25, "": 0, "latest": 0,
	} {
		if got := javaRelease(value); got != want {
			t.Errorf("javaRelease(%q) = %d, want %d", value, got, want)
		}
	}
}

func TestJavaVersionFilesPinTheJDK(t *testing.T) {
	t.Parallel()
	for name, fixture := range map[string]struct {
		files  map[string]string
		want   int
		source string
	}{
		"java-version":      {map[string]string{".java-version": "17.0.9\n"}, 17, ".java-version"},
		"sdkman":            {map[string]string{".sdkmanrc": "# SDKMAN\njava=21.0.2-tem\n"}, 21, ".sdkmanrc"},
		"asdf":              {map[string]string{".tool-versions": "nodejs 22\njava temurin-17.0.10+7\n"}, 17, ".tool-versions"},
		"mise":              {map[string]string{"mise.toml": "[tools]\njava = \"temurin-21\"\n"}, 21, "mise.toml"},
		"mise inline table": {map[string]string{".mise.toml": "[tools]\njava = { version = \"25\" }\n"}, 25, ".mise.toml"},
		"heroku":            {map[string]string{"system.properties": "java.runtime.version=11\n"}, 11, "system.properties"},
		"java-version wins": {map[string]string{".java-version": "17\n", ".tool-versions": "java 21\n"}, 17, ".java-version"},
	} {
		root := t.TempDir()
		for file, content := range fixture.files {
			writeBuildFixture(t, root, file, content)
		}
		files := newBuildFiles(root)
		pin, release := javaVersionPin(files, ".")
		files.close()
		if release != fixture.want || pin.source != fixture.source {
			t.Errorf("%s: pin %d from %q", name, release, pin.source)
		}
	}
}

func TestPlanJavaToolchainChoosesImagesThatRunTheBuild(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name     string
		facts    javaToolchainFacts
		override string
		release  int
		runner   int
		provide  int
		image    string
		code     string
	}{
		{name: "maven default", facts: javaToolchainFacts{tool: "maven"}, release: 21, runner: 21, image: "maven:3-eclipse-temurin-21"},
		{name: "maven java 8", facts: javaToolchainFacts{tool: "maven", declared: 8}, release: 8, runner: 8, image: "maven:3-eclipse-temurin-8"},
		{name: "non-LTS maps up", facts: javaToolchainFacts{tool: "maven", declared: 24}, release: 25, runner: 25, image: "maven:3-eclipse-temurin-25"},
		{name: "newer than the catalogue", facts: javaToolchainFacts{tool: "maven", declared: 26}, code: "java_version_unsupported"},
		{name: "a pin older than the build", facts: javaToolchainFacts{tool: "maven", declared: 21, pinned: 17, pinnedFrom: ".java-version"}, release: 21, runner: 21, image: "maven:3-eclipse-temurin-21"},
		{name: "a pin newer than the build", facts: javaToolchainFacts{tool: "maven", declared: 17, pinned: 21, pinnedFrom: ".java-version"}, release: 21, runner: 21, image: "maven:3-eclipse-temurin-21"},
		{name: "override newer than the build", facts: javaToolchainFacts{tool: "maven", declared: 17}, override: "25", release: 25, runner: 25, image: "maven:3-eclipse-temurin-25"},
		{name: "override older than the build", facts: javaToolchainFacts{tool: "maven", declared: 21}, override: "17", code: "java_version_unsupported"},
		{name: "gradle 25 without a wrapper", facts: javaToolchainFacts{tool: "gradle", declared: 25, toolchain: true}, release: 25, runner: 25, image: "gradle:9-jdk25"},
		{name: "gradle 11 without a wrapper", facts: javaToolchainFacts{tool: "gradle", declared: 11}, release: 11, runner: 11, image: "gradle:8-jdk11"},
		{name: "wrapper 8.14 on 21", facts: javaToolchainFacts{tool: "gradle", declared: 21, toolchain: true, wrapper: "8.14.3", wrapperUsable: true}, release: 21, runner: 21, image: "eclipse-temurin:21-jdk"},
		{name: "wrapper 8.4 with a toolchain 21", facts: javaToolchainFacts{tool: "gradle", declared: 21, toolchain: true, wrapper: "8.4", wrapperUsable: true}, release: 21, runner: 17, provide: 21, image: "eclipse-temurin:17-jdk"},
		{name: "wrapper 8.4 compiling for 21", facts: javaToolchainFacts{tool: "gradle", declared: 21, wrapper: "8.4", wrapperUsable: true}, code: "gradle_wrapper_incompatible"},
		{name: "wrapper 8.4 with nothing declared", facts: javaToolchainFacts{tool: "gradle", wrapper: "8.4", wrapperUsable: true}, release: 17, runner: 17, image: "eclipse-temurin:17-jdk"},
		{name: "wrapper 8.14 on 25", facts: javaToolchainFacts{tool: "gradle", declared: 25, wrapper: "8.14", wrapperUsable: true}, code: "gradle_wrapper_incompatible"},
		{name: "wrapper 9 compiling for 11", facts: javaToolchainFacts{tool: "gradle", declared: 11, wrapper: "9.1.0", wrapperUsable: true}, release: 11, runner: 17, image: "eclipse-temurin:17-jdk"},
		{name: "wrapper 9 with a toolchain 11", facts: javaToolchainFacts{tool: "gradle", declared: 11, toolchain: true, wrapper: "9.1.0", wrapperUsable: true}, release: 11, runner: 25, provide: 11, image: "eclipse-temurin:25-jdk"},
		{name: "a toolchain the catalogue lacks", facts: javaToolchainFacts{tool: "gradle", declared: 22, toolchain: true}, code: "java_version_unsupported"},
		{name: "foojay downloads it", facts: javaToolchainFacts{tool: "gradle", declared: 22, toolchain: true, foojay: true}, release: 25, runner: 25, image: "gradle:9-jdk25"},
		{name: "a missing wrapper jar still names Gradle 9", facts: javaToolchainFacts{tool: "gradle", declared: 21, wrapper: "9.0.0"}, release: 21, runner: 21, image: "gradle:9-jdk21"},
	} {
		plan, err := planJavaToolchain(fixture.facts, fixture.override)
		var refusal toolchainVersionError
		switch {
		case fixture.code != "":
			if !errors.As(err, &refusal) || refusal.code != fixture.code || !errors.Is(err, ErrUnsupportedBuilder) {
				t.Errorf("%s: %v, want %s", fixture.name, err, fixture.code)
			}
		case err != nil:
			t.Errorf("%s: %v", fixture.name, err)
		case plan.release != fixture.release || plan.runner != fixture.runner || plan.provide != fixture.provide ||
			plan.buildImage(fixture.facts.wrapperUsable) != fixture.image || plan.runtimeImage() != "eclipse-temurin:"+strconv.Itoa(fixture.release)+"-jre":
			t.Errorf("%s: plan %+v, image %s", fixture.name, plan, plan.buildImage(fixture.facts.wrapperUsable))
		}
	}
	if plan, _ := planJavaToolchain(javaToolchainFacts{tool: "maven", declared: 24, declaredFrom: "java.version in pom.xml"}, ""); !strings.Contains(plan.mapped, "Java 24") {
		t.Fatalf("mapped note = %q", plan.mapped)
	}
}

const springParentPOM = `<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <parent><groupId>org.springframework.boot</groupId><artifactId>spring-boot-starter-parent</artifactId><version>3.5.0</version></parent>
  <groupId>com.example</groupId><artifactId>shop-parent</artifactId><version>1.0.0</version>
  <packaging>pom</packaging>
  <properties><java.version>17</java.version></properties>
  <modules><module>core</module><module>app</module></modules>
</project>`

const springAppModulePOM = `<project>
  <modelVersion>4.0.0</modelVersion>
  <parent><groupId>com.example</groupId><artifactId>shop-parent</artifactId><version>1.0.0</version></parent>
  <artifactId>shop-app</artifactId>
  <dependencies>
    <dependency><groupId>com.example</groupId><artifactId>shop-core</artifactId><version>${project.version}</version></dependency>
    <dependency><groupId>org.springframework.boot</groupId><artifactId>spring-boot-starter-web</artifactId></dependency>
  </dependencies>
  <build><plugins><plugin><groupId>org.springframework.boot</groupId><artifactId>spring-boot-maven-plugin</artifactId></plugin></plugins></build>
</project>`

const springCoreModulePOM = `<project>
  <modelVersion>4.0.0</modelVersion>
  <parent><groupId>com.example</groupId><artifactId>shop-parent</artifactId><version>1.0.0</version></parent>
  <artifactId>shop-core</artifactId>
  <dependencies><dependency><groupId>org.springframework.boot</groupId><artifactId>spring-boot-starter-data-jpa</artifactId></dependency></dependencies>
</project>`

func TestMavenReactorBuildsTheApplicationModuleFromTheReactor(t *testing.T) {
	t.Parallel()
	result, root := detectCompiled(t, map[string]string{
		"pom.xml": springParentPOM, "app/pom.xml": springAppModulePOM, "core/pom.xml": springCoreModulePOM,
		"app/src/main/resources/application-prod.yml": "spring:\n  datasource:\n    url: ${DATABASE_URL}\n",
	})
	if len(result.Candidates) != 1 {
		t.Fatalf("candidates = %+v", result.Candidates)
	}
	app := result.Candidates[0]
	if app.Root != "app" || app.Framework != "spring-boot" || app.Confidence != ConfidenceHigh || app.RecipeIssue != "" || result.SelectedID != app.ID ||
		app.JavaBuild == nil || app.JavaBuild.Context != "." || app.JavaBuild.Module != ":shop-app" || app.JavaBuild.Release != 17 ||
		app.JavaBuild.Packaging != javaPackagingSpringBoot {
		t.Fatalf("app = %+v / %+v", app, app.JavaBuild)
	}
	if !slices.ContainsFunc(result.SetAside, func(item DetectionSetAside) bool { return item.Path == "core" && item.Kind == "library" }) {
		t.Fatalf("core not set aside: %+v", result.SetAside)
	}
	profile := slices.IndexFunc(app.Variables, func(variable DetectedVariable) bool { return variable.Name == "SPRING_PROFILES_ACTIVE" })
	if profile < 0 || app.Variables[profile].Example != "prod" || app.Variables[profile].Required {
		t.Fatalf("variables = %+v", app.Variables)
	}
	prepared := prepareCompiled(t, root, "app", BuildPlanConfig{Method: BuildRecipe, Recipe: "java", RootDirectory: "app"})
	if prepared.ContextDirectory != "." || prepared.Toolchain != "java 17 (maven)" {
		t.Fatalf("prepared = %+v", prepared)
	}
	assertCompiledDockerfile(t, prepared.DockerfilePreview,
		"FROM maven:3-eclipse-temurin-17@sha256:", "RUN mvn -B -ntp -DskipTests -pl :shop-app -am package",
		"RUN cd /src/app/target && (for f in *.jar;", "FROM eclipse-temurin:17-jre@sha256:",
		`CMD ["/bin/sh","-c","exec env SERVER_PORT=${PORT:-8080} java -jar /app/app.jar"]`)
	// Preparation writes the Dockerfile where the build runs from.
	if !regularExists(root, ".just-dashboard/Dockerfile") || regularExists(filepath.Join(root, "app"), ".just-dashboard/Dockerfile") {
		t.Fatal("the generated Dockerfile is not at the reactor root")
	}
	// Setting the root directory to the reactor names what to do.
	_, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), root, BuildPlanConfig{Method: BuildRecipe, Recipe: "java"}, false, "t:1")
	if !errors.Is(err, ErrUnsupportedBuilder) || !strings.Contains(err.Error(), "aggregates Maven modules") {
		t.Fatalf("reactor root prepared: %v", err)
	}
}

func TestMavenModuleWithAnInRepositoryParentOnly(t *testing.T) {
	t.Parallel()
	result, root := detectCompiled(t, map[string]string{
		"pom.xml": `<project><groupId>x</groupId><artifactId>parent</artifactId><version>1</version><packaging>pom</packaging><properties><maven.compiler.release>21</maven.compiler.release></properties></project>`,
		"service/pom.xml": `<project><parent><groupId>x</groupId><artifactId>parent</artifactId><version>1</version></parent><artifactId>service</artifactId>` +
			`<build><plugins><plugin><artifactId>maven-shade-plugin</artifactId></plugin></plugins></build></project>`,
	})
	service := compiledCandidate(result, "service", "java")
	if len(result.Candidates) != 1 || service == nil || service.JavaBuild.Release != 21 || service.JavaBuild.Packaging != javaPackagingFatJar {
		t.Fatalf("candidates = %+v", result.Candidates)
	}
	prepared := prepareCompiled(t, root, "service", BuildPlanConfig{Method: BuildRecipe, Recipe: "java", RootDirectory: "service"})
	assertCompiledDockerfile(t, prepared.DockerfilePreview, "RUN mvn -B -ntp -DskipTests -f service/pom.xml package",
		"(for f in *-jar-with-dependencies.jar *-shaded.jar *-fat.jar *-all.jar *-runner.jar *-exec.jar *.jar;")
}

const gradleInitSettings = `plugins {
    // Apply the foojay-resolver plugin to allow automatic download of JDKs
    id("org.gradle.toolchains.foojay-resolver-convention") version "0.10.0"
}

rootProject.name = "demo"
include("app", "utilities")
`

const gradleInitCatalog = `# This file was generated by the Gradle 'init' task.
[versions]
guava = "33.4.6-jre"
junit-jupiter = "5.12.1"

[libraries]
guava = { module = "com.google.guava:guava", version.ref = "guava" }
junit-jupiter = { module = "org.junit.jupiter:junit-jupiter", version.ref = "junit-jupiter" }
`

func TestGradleInitBuildsTheApplicationProjectFromTheSettingsRoot(t *testing.T) {
	t.Parallel()
	result, root := detectCompiled(t, map[string]string{
		"settings.gradle.kts":                      gradleInitSettings,
		"gradle/libs.versions.toml":                gradleInitCatalog,
		"gradlew":                                  "#!/bin/sh\r\nexec java -jar gradle/wrapper/gradle-wrapper.jar \"$@\"\r\n",
		"gradle/wrapper/gradle-wrapper.properties": "distributionUrl=https\\://services.gradle.org/distributions/gradle-8.14.3-bin.zip\n",
		"gradle/wrapper/gradle-wrapper.jar":        "PK",
		"app/build.gradle.kts": "plugins {\n    application\n}\n\ndependencies {\n    implementation(libs.guava)\n    implementation(project(\":utilities\"))\n}\n\n" +
			"java {\n    toolchain {\n        languageVersion = JavaLanguageVersion.of(21)\n    }\n}\n\napplication {\n    mainClass = \"org.example.app.App\"\n}\n",
		"utilities/build.gradle.kts": "plugins {\n    `java-library`\n}\n",
	})
	if len(result.Candidates) != 1 {
		t.Fatalf("candidates = %+v", result.Candidates)
	}
	app := result.Candidates[0]
	if app.Root != "app" || app.JavaBuild == nil || app.JavaBuild.Module != ":app" || app.JavaBuild.Context != "." ||
		app.JavaBuild.Packaging != javaPackagingApplication || !app.JavaBuild.Toolchain || app.JavaBuild.Release != 21 || !app.JavaBuild.Foojay ||
		app.JavaBuild.Wrapper != "8.14.3" || !app.JavaBuild.WrapperUsable {
		t.Fatalf("app = %+v / %+v", app, app.JavaBuild)
	}
	if !strings.Contains(string(mustJSON(app.Evidence)), "com.google.guava:guava") && !strings.Contains(string(mustJSON(app.Evidence)), "project :app") {
		t.Fatalf("evidence = %+v", app.Evidence)
	}
	prepared := prepareCompiled(t, root, "app", BuildPlanConfig{Method: BuildRecipe, Recipe: "java", RootDirectory: "app"})
	assertCompiledDockerfile(t, prepared.DockerfilePreview,
		"FROM eclipse-temurin:21-jdk@sha256:", `RUN sed -i 's/\r$//' gradlew && chmod +x gradlew`,
		"RUN ./gradlew --no-daemon --console=plain :app:installDist", "RUN cd /src/app/build/install && set -- */",
		`{ [ "$name" = app ] || mv "/out/bin/$name" /out/bin/app; }`, "COPY --from=build /out/ /app/", `CMD ["/app/bin/app"]`)
	if prepared.ContextDirectory != "." || prepared.Toolchain != "java 21 (gradle 8.14.3)" {
		t.Fatalf("prepared = %+v", prepared)
	}
}

func TestGradleCatalogAliasesNameTheFramework(t *testing.T) {
	t.Parallel()
	result, root := detectCompiled(t, map[string]string{
		"settings.gradle.kts": `rootProject.name = "shop"`,
		"gradle/libs.versions.toml": "[versions]\nspring-boot = \"3.5.0\"\n\n[libraries]\nspring-boot-starter-web = { module = \"org.springframework.boot:spring-boot-starter-web\" }\n" +
			"spring-boot-starter-actuator = { group = \"org.springframework.boot\", name = \"spring-boot-starter-actuator\" }\n\n[plugins]\nspring-boot = { id = \"org.springframework.boot\", version.ref = \"spring-boot\" }\nspring-dependency-management = \"io.spring.dependency-management:1.1.7\"\n",
		"build.gradle.kts": "plugins {\n    java\n    alias(libs.plugins.spring.boot)\n    alias(libs.plugins.spring.dependency.management)\n}\n\njava { toolchain { languageVersion.set(JavaLanguageVersion.of(21)) } }\n\n" +
			"dependencies {\n    implementation(libs.spring.boot.starter.web)\n    implementation(libs.spring.boot.starter.actuator)\n}\n",
	})
	app := compiledCandidate(result, "", "java")
	if app == nil || app.Framework != "spring-boot" || app.Port != 8080 || app.Profile != ProfileWeb || app.JavaBuild.Packaging != javaPackagingSpringBoot {
		t.Fatalf("candidates = %+v", result.Candidates)
	}
	// Readiness reads the actuator the alias stands for.
	if app.Readiness == nil || app.Readiness.Path != "/actuator/health" {
		t.Fatalf("readiness = %+v", app.Readiness)
	}
	prepared := prepareCompiled(t, root, "", BuildPlanConfig{Method: BuildRecipe, Recipe: "java"})
	assertCompiledDockerfile(t, prepared.DockerfilePreview, "FROM gradle:8-jdk21@sha256:", "RUN gradle --no-daemon --console=plain :bootJar",
		"RUN cd /src/build/libs && (for f in *.jar;")
}

func TestKotlinMultiplatformServerNamesTheAndroidSDKItNeeds(t *testing.T) {
	t.Parallel()
	catalog := "[versions]\nkotlin = \"2.1.0\"\nktor = \"3.1.0\"\nagp = \"8.7.3\"\n\n[libraries]\nktor-server-core = { module = \"io.ktor:ktor-server-core-jvm\", version.ref = \"ktor\" }\n" +
		"ktor-server-netty = { module = \"io.ktor:ktor-server-netty-jvm\", version.ref = \"ktor\" }\n\n[plugins]\nandroidApplication = { id = \"com.android.application\", version.ref = \"agp\" }\n" +
		"androidLibrary = { id = \"com.android.library\", version.ref = \"agp\" }\nkotlinJvm = { id = \"org.jetbrains.kotlin.jvm\", version.ref = \"kotlin\" }\n" +
		"kotlinMultiplatform = { id = \"org.jetbrains.kotlin.multiplatform\", version.ref = \"kotlin\" }\nktor = { id = \"io.ktor.plugin\", version.ref = \"ktor\" }\n"
	files := map[string]string{
		"settings.gradle.kts":       "rootProject.name = \"KotlinProject\"\nenableFeaturePreview(\"TYPESAFE_PROJECT_ACCESSORS\")\ninclude(\":composeApp\")\ninclude(\":server\")\ninclude(\":shared\")\n",
		"gradle/libs.versions.toml": catalog,
		"build.gradle.kts": "plugins {\n    alias(libs.plugins.androidApplication) apply false\n    alias(libs.plugins.androidLibrary) apply false\n" +
			"    alias(libs.plugins.kotlinJvm) apply false\n    alias(libs.plugins.kotlinMultiplatform) apply false\n    alias(libs.plugins.ktor) apply false\n}\n",
		"server/build.gradle.kts": "plugins {\n    alias(libs.plugins.kotlinJvm)\n    alias(libs.plugins.ktor)\n    application\n}\n\napplication {\n    mainClass.set(\"org.example.project.ApplicationKt\")\n}\n\n" +
			"dependencies {\n    implementation(projects.shared)\n    implementation(libs.ktor.server.core)\n    implementation(libs.ktor.server.netty)\n}\n",
		"shared/build.gradle.kts":     "plugins {\n    alias(libs.plugins.kotlinMultiplatform)\n    alias(libs.plugins.androidLibrary)\n}\nkotlin {\n    androidTarget()\n    jvm()\n}\n",
		"composeApp/build.gradle.kts": "plugins {\n    alias(libs.plugins.kotlinMultiplatform)\n    alias(libs.plugins.androidApplication)\n}\n",
	}
	result, _ := detectCompiled(t, files)
	server := compiledCandidate(result, "server", "java")
	if server == nil || server.Framework != "ktor" || !strings.Contains(server.RecipeIssue, ":shared") || !strings.Contains(server.RecipeIssue, "Android") {
		t.Fatalf("server = %+v", server)
	}
	for _, root := range []string{"", "shared", "composeApp"} {
		if compiledCandidate(result, root, "java") != nil {
			t.Fatalf("%q is still a candidate: %+v", root, result.Candidates)
		}
	}
	// Without an Android target the shared module builds on the JVM, and
	// the server is Ktor's fat jar.
	files["shared/build.gradle.kts"] = "plugins {\n    alias(libs.plugins.kotlinMultiplatform)\n}\nkotlin {\n    jvm()\n}\n"
	result, root := detectCompiled(t, files)
	server = compiledCandidate(result, "server", "java")
	if server == nil || server.RecipeIssue != "" || server.Confidence != ConfidenceHigh || result.SelectedID != server.ID {
		t.Fatalf("server = %+v / %+v", server, result.Candidates)
	}
	prepared := prepareCompiled(t, root, "server", BuildPlanConfig{Method: BuildRecipe, Recipe: "java", RootDirectory: "server"})
	assertCompiledDockerfile(t, prepared.DockerfilePreview, "RUN gradle --no-daemon --console=plain :server:buildFatJar",
		"RUN cd /src/server/build/libs && (for f in *-all.jar *.jar;", `CMD ["java","-jar","/app/app.jar"]`)
}

func TestJavaArtifactFollowsThePackagingPlugin(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{"quarkus fast-jar", map[string]string{"pom.xml": `<project><artifactId>q</artifactId><dependencies><dependency><groupId>io.quarkus</groupId><artifactId>quarkus-rest</artifactId></dependency></dependencies>` +
			`<build><plugins><plugin><groupId>io.quarkus.platform</groupId><artifactId>quarkus-maven-plugin</artifactId></plugin></plugins></build></project>`},
			[]string{"RUN test -f /src/target/quarkus-app/quarkus-run.jar && mkdir -p /out && cp -R /src/target/quarkus-app /out/quarkus-app",
				`"exec env QUARKUS_HTTP_PORT=${PORT:-8080} java -jar /app/quarkus-app/quarkus-run.jar"`}},
		{"quarkus uber-jar", map[string]string{"pom.xml": `<project><artifactId>q</artifactId><build><plugins><plugin><artifactId>quarkus-maven-plugin</artifactId></plugin></plugins></build></project>`,
			"src/main/resources/application.properties": "quarkus.package.jar.type=uber-jar\n"},
			[]string{"(for f in *-runner.jar;", "java -jar /app/app.jar"}},
		{"gradle quarkus", map[string]string{"build.gradle.kts": "plugins { java\n id(\"io.quarkus\") }\n"},
			[]string{":quarkusBuild", "/src/build/quarkus-app/quarkus-run.jar"}},
		{"ktor", map[string]string{"build.gradle.kts": "plugins { kotlin(\"jvm\") version \"2.1.0\"\n id(\"io.ktor.plugin\") version \"3.1.0\" }\ndependencies { implementation(\"io.ktor:ktor-server-netty\") }\n"},
			[]string{":buildFatJar", "(for f in *-all.jar *.jar;"}},
		{"shadow", map[string]string{"build.gradle": "plugins {\n id 'java'\n id 'com.gradleup.shadow' version '8.3.5'\n}\njar { manifest { attributes 'Main-Class': 'x.Main' } }\n"},
			[]string{":shadowJar", "*-all.jar"}},
		{"assembly", map[string]string{"pom.xml": `<project><artifactId>a</artifactId><build><plugins><plugin><artifactId>maven-assembly-plugin</artifactId><configuration><descriptorRefs><descriptorRef>jar-with-dependencies</descriptorRef></descriptorRefs><archive><manifest><mainClass>x.Main</mainClass></manifest></archive></configuration></plugin></plugins></build></project>`},
			[]string{"(for f in *-jar-with-dependencies.jar"}},
		{"helidon", map[string]string{"pom.xml": `<project><parent><groupId>io.helidon.applications</groupId><artifactId>helidon-se</artifactId><version>4.1.0</version></parent><artifactId>h</artifactId><properties><mainClass>x.Main</mainClass></properties></project>`},
			[]string{"for d in lib libs; do if [ -d \"$d\" ]; then cp -R \"$d\" /out/; fi; done", "exec env SERVER_PORT=${PORT:-8080} java -jar /app/app.jar"}},
		{"spring boot war", map[string]string{"pom.xml": `<project><artifactId>w</artifactId><packaging>war</packaging><dependencies><dependency><artifactId>spring-boot-starter-web</artifactId></dependency></dependencies><build><plugins><plugin><artifactId>spring-boot-maven-plugin</artifactId></plugin></plugins></build></project>`},
			[]string{"(for f in *.war;", "cp \"$f\" /out/app.war", "java -jar /app/app.war"}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			_, root := detectCompiled(t, fixture.files)
			prepared := prepareCompiled(t, root, "", BuildPlanConfig{Method: BuildRecipe, Recipe: "java"})
			assertCompiledDockerfile(t, prepared.DockerfilePreview, fixture.want...)
		})
	}
}

func TestGradleWrapperRunsOnAJDKItSupports(t *testing.T) {
	t.Parallel()
	wrapper := func(version string) map[string]string {
		return map[string]string{
			"gradlew": "#!/bin/sh\n", "gradle/wrapper/gradle-wrapper.jar": "PK",
			"gradle/wrapper/gradle-wrapper.properties": "distributionUrl=https\\://services.gradle.org/distributions/gradle-" + version + "-all.zip\n",
		}
	}
	files := wrapper("8.4")
	files["build.gradle"] = "plugins { id 'java'\n id 'application' }\njava { toolchain { languageVersion = JavaLanguageVersion.of(21) } }\napplication { mainClass = 'x.Main' }\n"
	_, root := detectCompiled(t, files)
	prepared := prepareCompiled(t, root, "", BuildPlanConfig{Method: BuildRecipe, Recipe: "java"})
	assertCompiledDockerfile(t, prepared.DockerfilePreview, "FROM eclipse-temurin:17-jdk@sha256:",
		"COPY --from=eclipse-temurin:21-jdk@sha256:", " /opt/java/openjdk /opt/jdk-21",
		"RUN ./gradlew --no-daemon --console=plain -Porg.gradle.java.installations.paths=/opt/jdk-21 :installDist", "FROM eclipse-temurin:21-jre@sha256:")
	if prepared.Toolchain != "java 21 (gradle 8.4 on java 17)" || !slices.ContainsFunc(prepared.Notes, func(note string) bool { return strings.Contains(note, "/opt/jdk-21") }) {
		t.Fatalf("prepared = %+v", prepared)
	}
	// Compiling for 21 without a toolchain needs a Gradle that runs on 21.
	files["build.gradle"] = "plugins { id 'application' }\nsourceCompatibility = '21'\n"
	result, root := detectCompiled(t, files)
	_, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), root, BuildPlanConfig{Method: BuildRecipe, Recipe: "java"}, false, "t:1")
	var refusal toolchainVersionError
	if !errors.As(err, &refusal) || refusal.code != "gradle_wrapper_incompatible" || !strings.Contains(err.Error(), "--gradle-version 8.5") {
		t.Fatalf("incompatible wrapper prepared: %v", err)
	}
	if candidate := result.Candidates[0]; candidate.RecipeIssue != "" {
		t.Fatalf("a release question became a refusal of the source: %q", candidate.RecipeIssue)
	}
	findings := compiledRecipeFindings(&result.Candidates[0], PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe, Recipe: "java"}})
	if item := findingByCode(findings, "gradle_wrapper_incompatible"); item == nil || item.Severity != PreflightBlocked || item.FieldID != "configuration.build.javaVersion" {
		t.Fatalf("findings = %+v", findings)
	}
	// Choosing Java 17 in the build settings clears it.
	findings = compiledRecipeFindings(&result.Candidates[0], PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe, Recipe: "java", JavaVersion: "17"}})
	if item := findingByCode(findings, "gradle_wrapper_incompatible"); item != nil {
		t.Fatalf("an older release still refused: %+v", item)
	}
	if item := findingByCode(findings, "java_version_unsupported"); item == nil {
		t.Fatalf("Java 17 accepted for a build compiling for 21: %+v", findings)
	}
	// A wrapper whose jar is ignored runs nothing; the image's Gradle builds.
	files = wrapper("8.14.3")
	delete(files, "gradle/wrapper/gradle-wrapper.jar")
	files["build.gradle.kts"] = "plugins { application }\napplication { mainClass.set(\"x.Main\") }\n"
	result, root = detectCompiled(t, files)
	if build := result.Candidates[0].JavaBuild; build == nil || !build.WrapperJarMissing || build.WrapperUsable {
		t.Fatalf("build = %+v", build)
	}
	findings = compiledRecipeFindings(&result.Candidates[0], PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe, Recipe: "java"}})
	if item := findingByCode(findings, "gradle_wrapper_jar_missing"); item == nil || item.Severity != PreflightWarning {
		t.Fatalf("findings = %+v", findings)
	}
	prepared = prepareCompiled(t, root, "", BuildPlanConfig{Method: BuildRecipe, Recipe: "java"})
	assertCompiledDockerfile(t, prepared.DockerfilePreview, "FROM gradle:8-jdk21@sha256:", "RUN gradle --no-daemon --console=plain :installDist")
	if strings.Contains(prepared.DockerfilePreview, "./gradlew") {
		t.Fatalf("the wrapper without its jar ran:\n%s", prepared.DockerfilePreview)
	}
}

func TestJavaReleaseSourcesAndMapping(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name    string
		files   map[string]string
		image   string
		runtime string
		mapped  bool
	}{
		{"sdkman pins a newer JDK", map[string]string{".sdkmanrc": "java=21.0.2-tem\n", "pom.xml": "<project><properties><maven.compiler.release>17</maven.compiler.release></properties></project>"}, "maven:3-eclipse-temurin-21", "eclipse-temurin:21-jre", false},
		{"spring boot 2 on java 8", map[string]string{"pom.xml": "<project><properties><java.version>1.8</java.version></properties></project>"}, "maven:3-eclipse-temurin-8", "eclipse-temurin:8-jre", false},
		{"non-LTS maps to 25", map[string]string{"pom.xml": "<project><properties><java.version>24</java.version></properties></project>"}, "maven:3-eclipse-temurin-25", "eclipse-temurin:25-jre", true},
		{"release resolves through a property", map[string]string{"pom.xml": "<project><properties><jdk>11</jdk></properties><build><plugins><plugin><artifactId>maven-compiler-plugin</artifactId><configuration><release>${jdk}</release></configuration></plugin></plugins></build></project>"}, "maven:3-eclipse-temurin-11", "eclipse-temurin:11-jre", false},
		{"groovy VERSION_1_8", map[string]string{"build.gradle": "apply plugin: 'java'\nsourceCompatibility = JavaVersion.VERSION_1_8\n"}, "gradle:8-jdk8", "eclipse-temurin:8-jre", false},
	} {
		result, root := detectCompiled(t, fixture.files)
		prepared := prepareCompiled(t, root, "", BuildPlanConfig{Method: BuildRecipe, Recipe: "java"})
		assertCompiledDockerfile(t, prepared.DockerfilePreview, "FROM "+fixture.image+"@sha256:", "FROM "+fixture.runtime+"@sha256:")
		findings := compiledRecipeFindings(&result.Candidates[0], PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe, Recipe: "java"}})
		if (findingByCode(findings, "java_version_mapped") != nil) != fixture.mapped || findingByCode(findings, "java_toolchain") == nil {
			t.Fatalf("%s: findings = %+v", fixture.name, findings)
		}
	}
}

func TestJavaProductionProfiles(t *testing.T) {
	t.Parallel()
	vaadin := `<project><artifactId>v</artifactId><dependencies><dependency><groupId>com.vaadin</groupId><artifactId>vaadin-spring-boot-starter</artifactId></dependency></dependencies>` +
		`<build><plugins><plugin><artifactId>spring-boot-maven-plugin</artifactId></plugin></plugins></build>%s</project>`
	result, root := detectCompiled(t, map[string]string{"pom.xml": strings.Replace(vaadin, "%s", `<profiles><profile><id>production</id><build><plugins><plugin><groupId>com.vaadin</groupId><artifactId>vaadin-maven-plugin</artifactId><executions><execution><goals><goal>build-frontend</goal></goals></execution></executions></plugin></plugins></build></profile></profiles>`, 1)})
	prepared := prepareCompiled(t, root, "", BuildPlanConfig{Method: BuildRecipe, Recipe: "java"})
	assertCompiledDockerfile(t, prepared.DockerfilePreview, "mvn -B -ntp -DskipTests -Pproduction package")
	if build := result.Candidates[0].JavaBuild; build.VaadinDevMode || !slices.Equal(build.Profiles, []string{"production"}) {
		t.Fatalf("build = %+v", build)
	}
	result, _ = detectCompiled(t, map[string]string{"pom.xml": strings.Replace(vaadin, "%s", "", 1)})
	findings := compiledRecipeFindings(&result.Candidates[0], PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe, Recipe: "java"}})
	if item := findingByCode(findings, "vaadin_dev_mode"); item == nil || item.Severity != PreflightWarning {
		t.Fatalf("findings = %+v", findings)
	}
	_, root = detectCompiled(t, map[string]string{
		".yo-rc.json": `{"generator-jhipster": {"applicationType": "monolith"}}`,
		"pom.xml":     `<project><artifactId>j</artifactId><build><plugins><plugin><artifactId>spring-boot-maven-plugin</artifactId></plugin></plugins></build><profiles><profile><id>dev</id></profile><profile><id>prod</id></profile></profiles></project>`,
	})
	assertCompiledDockerfile(t, prepareCompiled(t, root, "", BuildPlanConfig{Method: BuildRecipe, Recipe: "java"}).DockerfilePreview, "-Pprod package")
}

func TestJavaArtifactThatCannotRunIsSaidBeforeDeploy(t *testing.T) {
	t.Parallel()
	for name, fixture := range map[string]struct {
		files    map[string]string
		severity PreflightSeverity
	}{
		"plain jar":   {map[string]string{"pom.xml": "<project><artifactId>tool</artifactId></project>"}, PreflightWarning},
		"servlet war": {map[string]string{"pom.xml": "<project><artifactId>site</artifactId><packaging>war</packaging></project>"}, PreflightBlocked},
		"main class":  {map[string]string{"pom.xml": "<project><artifactId>tool</artifactId><build><plugins><plugin><artifactId>maven-jar-plugin</artifactId><configuration><archive><manifest><mainClass>x.Main</mainClass></manifest></archive></configuration></plugin></plugins></build></project>"}, ""},
	} {
		result, _ := detectCompiled(t, fixture.files)
		findings := compiledRecipeFindings(&result.Candidates[0], PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe, Recipe: "java"}})
		item := findingByCode(findings, "java_artifact_not_runnable")
		if (fixture.severity == "") != (item == nil) || (item != nil && item.Severity != fixture.severity) {
			t.Fatalf("%s: %+v", name, findings)
		}
	}
	// A java-library is not a service at all.
	result, _ := detectCompiled(t, map[string]string{"build.gradle.kts": "plugins { `java-library` }\n"})
	if result.Candidates[0].NotDeployable != "library" {
		t.Fatalf("candidate = %+v", result.Candidates[0])
	}
}

func TestJavaPathsTheDockerfileCannotHoldAreRefused(t *testing.T) {
	t.Parallel()
	_, root := detectCompiled(t, map[string]string{
		"pom.xml":        `<project><artifactId>p</artifactId><packaging>pom</packaging><modules><module>my app</module></modules></project>`,
		"my app/pom.xml": `<project><parent><artifactId>p</artifactId></parent><artifactId>app</artifactId><build><plugins><plugin><artifactId>maven-shade-plugin</artifactId></plugin></plugins></build></project>`,
	})
	_, err := NewArtifactBuilder(&artifactBackendFake{}).PrepareWithin(context.Background(), root, filepath.Join(root, "my app"), BuildPlanConfig{Method: BuildRecipe, Recipe: "java"}, false, "t:1")
	if !errors.Is(err, ErrUnsupportedBuilder) || !strings.Contains(err.Error(), "does not quote") {
		t.Fatalf("prepared: %v", err)
	}
}

func TestRecipeRefusesABaseWithoutTheTargetPlatform(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeBuildFixture(t, root, "pom.xml", "<project><artifactId>a</artifactId></project>")
	// The fake publishes every base for linux/amd64 alone.
	_, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), root,
		BuildPlanConfig{Method: BuildRecipe, Recipe: "java", TargetPlatform: "linux/arm64"}, false, "t:1")
	if !errors.Is(err, ErrUnsupportedBuilder) || !strings.Contains(err.Error(), "has no image for linux/arm64") {
		t.Fatalf("prepared: %v", err)
	}
	if _, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), root,
		BuildPlanConfig{Method: BuildRecipe, Recipe: "java", TargetPlatform: "linux/amd64"}, false, "t:1"); err != nil {
		t.Fatal(err)
	}
}

// Where preflight has the tree, the recipe's own dry run outvotes the
// release findings detection's facts raise, and says nothing twice.
func TestCompiledReleaseFindingsGiveWayToTheDryRun(t *testing.T) {
	t.Parallel()
	facts := javaToolchainFacts{tool: "gradle", declared: 21, declaredFrom: "build.gradle", wrapper: "8.4", wrapperUsable: true}
	_, refusal := planJavaToolchain(facts, "")
	build := BuildPlanConfig{Method: BuildRecipe, Recipe: "java"}
	findings := javaPlanFindings(&DetectedJavaBuild{Tool: "gradle", Release: 21, ReleaseFrom: "build.gradle", Wrapper: "8.4", WrapperUsable: true}, build)
	merged := applyDryRunVerdict(findings, refusal, build)
	if findingByCode(merged, "gradle_wrapper_incompatible") == nil || findingByCode(merged, "recipe_unsupported") != nil {
		t.Fatalf("merged = %+v", merged)
	}
	if merged := applyDryRunVerdict(findings, nil, build); findingByCode(merged, "gradle_wrapper_incompatible") != nil {
		t.Fatalf("a dry run that prepared left the finding: %+v", merged)
	}
	for text, field := range map[string]string{
		refusal.Error(): "configuration.build.javaVersion",
		dotnetVersionRefusal("Api.csproj declares no <TargetFramework> the recipe can read").Error():                           "configuration.build.dotnetVersion",
		dotnetVersionRefusal("global.json pins .NET SDK 8.0.100 (rollForward latestPatch), which cannot build net9.0").Error(): "configuration.build.dotnetVersion",
	} {
		if got := recipeRefusalField(recipeRefusalText(errors.New(text), "")); got != field {
			t.Errorf("%q points at %s, want %s", text, got, field)
		}
	}
}

func TestGradleSubprojectsInheritTheRootScriptsPlugins(t *testing.T) {
	t.Parallel()
	result, root := detectCompiled(t, map[string]string{
		"settings.gradle": "rootProject.name = 'legacy'\ninclude 'web', 'model'\n",
		"build.gradle": "buildscript {\n  dependencies { classpath 'org.springframework.boot:spring-boot-gradle-plugin:2.7.18' }\n}\n" +
			"subprojects {\n  apply plugin: 'java'\n  sourceCompatibility = '11'\n}\nproject(':web') {\n  apply plugin: 'org.springframework.boot'\n}\n",
		"web/build.gradle":   "apply plugin: 'org.springframework.boot'\ndependencies {\n  implementation project(':model')\n  implementation 'org.springframework.boot:spring-boot-starter-web'\n}\n",
		"model/build.gradle": "dependencies { }\n",
	})
	web := compiledCandidate(result, "web", "java")
	if len(result.Candidates) != 1 || web == nil || web.Framework != "spring-boot" || web.JavaBuild.Packaging != javaPackagingSpringBoot || web.JavaBuild.Release != 11 {
		t.Fatalf("candidates = %+v", result.Candidates)
	}
	prepared := prepareCompiled(t, root, "web", BuildPlanConfig{Method: BuildRecipe, Recipe: "java", RootDirectory: "web"})
	assertCompiledDockerfile(t, prepared.DockerfilePreview, "FROM gradle:8-jdk11@", "RUN gradle --no-daemon --console=plain :web:bootJar", "FROM eclipse-temurin:11-jre@")
}
