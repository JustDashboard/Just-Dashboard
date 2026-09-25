package deploy

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// The compiled-language recipes: what a starter of each looks like, what
// detection makes of it, and the Dockerfile the recipe renders for it.

func TestRustDetectionAndRecipe(t *testing.T) {
	t.Parallel()
	manifest := parseCargoManifest([]byte("[package]\nname = \"web-api\"\nversion = \"0.1.0\"\n\n[[bin]]\nname = \"api\"\npath = \"src/main.rs\"\n\n[dependencies]\naxum = \"0.8\"\ntokio = { version = \"1\", features = [\"full\"] }\n\n[dependencies.serde_json]\nversion = \"1\"\n"))
	if manifest.name != "web-api" || !slices.Equal(manifest.bins, []string{"api"}) || !manifest.deps["axum"] || !manifest.deps["serde-json"] || manifest.workspace {
		t.Fatalf("manifest = %+v", manifest)
	}
	for _, fixture := range []struct {
		name      string
		files     map[string]string
		framework string
		profile   WorkloadProfile
		port      int
		confident DetectionConfidence
		issue     string
		unpinned  bool
	}{
		{"axum service", map[string]string{"Cargo.toml": "[package]\nname = \"svc\"\n[dependencies]\naxum = \"0.8\"\n", "Cargo.lock": "version = 4\n"}, "axum", ProfileWeb, 3000, ConfidenceHigh, "", false},
		{"plain binary", map[string]string{"Cargo.toml": "[package]\nname = \"tool\"\n"}, "rust", ProfileWorker, 0, ConfidenceMedium, "", true},
		{"workspace without a package", map[string]string{"Cargo.toml": "[workspace]\nmembers = [\"crates/*\"]\n"}, "rust", ProfileWorker, 0, ConfidenceLow, "workspace", true},
		{"nightly toolchain", map[string]string{"Cargo.toml": "[package]\nname = \"svc\"\n[dependencies]\nrocket = \"0.5\"\n", "rust-toolchain.toml": "[toolchain]\nchannel = \"nightly-2025-01-01\"\n"}, "rocket", ProfileWeb, 8000, ConfidenceMedium, "nightly", true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			for path, content := range fixture.files {
				writeBuildFixture(t, root, path, content)
			}
			result, err := (Detector{}).DetectPath(context.Background(), root, SourceIdentity{})
			if err != nil || len(result.Candidates) != 1 {
				t.Fatalf("detect: %+v, %v", result, err)
			}
			candidate := result.Candidates[0]
			if candidate.Recipe != "rust" || candidate.Framework != fixture.framework || candidate.Profile != fixture.profile ||
				candidate.Port != fixture.port || candidate.Confidence != fixture.confident || candidate.UnpinnedDependencies != fixture.unpinned ||
				!strings.Contains(candidate.RecipeIssue, fixture.issue) {
				t.Fatalf("candidate = %+v", candidate)
			}
		})
	}
	root := t.TempDir()
	writeBuildFixture(t, root, "Cargo.toml", "[package]\nname = \"svc\"\n\n[[bin]]\nname = \"serve\"\n\n[dependencies]\naxum = \"0.8\"\n")
	writeBuildFixture(t, root, "Cargo.lock", "version = 4\n")
	writeBuildFixture(t, root, "rust-toolchain", "1.85.0\n")
	prepared, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), root, BuildPlanConfig{Method: BuildRecipe, Recipe: "rust"}, false, "just-dashboard/test:run-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"FROM rust:1.85.0-alpine@sha256:", "apk add --no-cache musl-dev", "cargo fetch --locked", "cargo build --release --locked",
		"cp /src/target/release/serve /out/app", "FROM alpine:3.22@sha256:", `ENTRYPOINT ["/app"]`} {
		if !strings.Contains(prepared.DockerfilePreview, want) {
			t.Fatalf("Dockerfile missing %q:\n%s", want, prepared.DockerfilePreview)
		}
	}
	if prepared.Toolchain != "rust 1.85.0" {
		t.Fatalf("toolchain = %q", prepared.Toolchain)
	}
	writeBuildFixture(t, root, "rust-toolchain", "nightly\n")
	if _, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), root, BuildPlanConfig{Method: BuildRecipe, Recipe: "rust"}, false, "t:1"); !errors.Is(err, ErrUnsupportedBuilder) {
		t.Fatalf("nightly accepted: %v", err)
	}
}

func TestJavaDetectionAndRecipe(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct{ name, versionFile, pom, gradle, want string }{
		{"default", "", "", "", "21"},
		{"pom property", "", "<properties><java.version>17</java.version></properties>", "", "17"},
		{"pom legacy source", "", "<maven.compiler.source>1.8</maven.compiler.source>", "", ""},
		{"gradle toolchain", "", "", "java { toolchain { languageVersion = JavaLanguageVersion.of(25) } }", "25"},
		{"gradle compatibility", "", "", "sourceCompatibility = '11'", "11"},
		{"kotlin jvm toolchain", "", "", "kotlin { jvmToolchain(21) }", "21"},
		{"version file wins", "17\n", "<java.version>21</java.version>", "", "17"},
	} {
		got, err := chooseJavaRecipeVersion(fixture.versionFile, fixture.pom, fixture.gradle)
		if (fixture.want == "") != (err != nil) || got != fixture.want {
			t.Fatalf("%s: version %q, %v", fixture.name, got, err)
		}
	}
	for _, fixture := range []struct {
		name      string
		files     map[string]string
		framework string
		profile   WorkloadProfile
		port      int
		issue     string
	}{
		{"spring boot maven", map[string]string{"pom.xml": "<project><parent><artifactId>spring-boot-starter-parent</artifactId></parent><properties><java.version>21</java.version></properties></project>"}, "spring-boot", ProfileWeb, 8080, ""},
		{"quarkus gradle", map[string]string{"build.gradle.kts": "plugins { id(\"io.quarkus\") }\njava { toolchain { languageVersion = JavaLanguageVersion.of(21) } }\n", "gradlew": "#!/bin/sh\n"}, "quarkus", ProfileWeb, 8080, ""},
		{"plain maven", map[string]string{"pom.xml": "<project><artifactId>tool</artifactId></project>"}, "java", ProfileWorker, 0, ""},
		{"multi-module", map[string]string{"pom.xml": "<project><modules><module>api</module></modules></project>"}, "java", ProfileWorker, 0, "multi-module"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			for path, content := range fixture.files {
				writeBuildFixture(t, root, path, content)
			}
			result, err := (Detector{}).DetectPath(context.Background(), root, SourceIdentity{})
			if err != nil || len(result.Candidates) != 1 {
				t.Fatalf("detect: %+v, %v", result, err)
			}
			candidate := result.Candidates[0]
			if candidate.Recipe != "java" || candidate.Framework != fixture.framework || candidate.Profile != fixture.profile ||
				candidate.Port != fixture.port || !strings.Contains(candidate.RecipeIssue, fixture.issue) {
				t.Fatalf("candidate = %+v", candidate)
			}
		})
	}
	root := t.TempDir()
	writeBuildFixture(t, root, "pom.xml", "<project><properties><maven.compiler.release>17</maven.compiler.release></properties></project>")
	prepared, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), root, BuildPlanConfig{Method: BuildRecipe, Recipe: "java"}, false, "t:1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"FROM maven:3-eclipse-temurin-17@sha256:", "mvn -q -B -DskipTests package", "ls target/*.jar", "FROM eclipse-temurin:17-jre-alpine@sha256:",
		`ENV JAVA_TOOL_OPTIONS="-XX:MaxRAMPercentage=75"`, `CMD ["java","-jar","/app/app.jar"]`} {
		if !strings.Contains(prepared.DockerfilePreview, want) {
			t.Fatalf("Dockerfile missing %q:\n%s", want, prepared.DockerfilePreview)
		}
	}
	root = t.TempDir()
	writeBuildFixture(t, root, "build.gradle", "plugins { id 'org.springframework.boot' }\n")
	writeBuildFixture(t, root, "gradlew", "#!/bin/sh\n")
	prepared, err = NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), root, BuildPlanConfig{Method: BuildRecipe, Recipe: "java", StartCommand: "java -Xmx256m -jar /app/app.jar"}, false, "t:1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"FROM gradle:8-jdk21@sha256:", `sed -i 's/\r$//' ./gradlew && chmod +x ./gradlew && ./gradlew --no-daemon -q build -x test`, "ls build/libs/*.jar", "-e '-plain'", `"java -Xmx256m -jar /app/app.jar"`} {
		if !strings.Contains(prepared.DockerfilePreview, want) {
			t.Fatalf("Dockerfile missing %q:\n%s", want, prepared.DockerfilePreview)
		}
	}
	if prepared.Toolchain != "java 21 (gradle)" {
		t.Fatalf("toolchain = %q", prepared.Toolchain)
	}
}

func TestDotnetDetectionAndRecipe(t *testing.T) {
	t.Parallel()
	web := `<Project Sdk="Microsoft.NET.Sdk.Web"><PropertyGroup><TargetFramework>net8.0</TargetFramework><AssemblyName>Shop.Api</AssemblyName></PropertyGroup></Project>`
	worker := `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><OutputType>Exe</OutputType><TargetFramework>net9.0</TargetFramework></PropertyGroup></Project>`
	library := `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>`
	for _, fixture := range []struct {
		name      string
		files     map[string]string
		framework string
		profile   WorkloadProfile
		port      int
		issue     string
	}{
		{"web api", map[string]string{"Api.csproj": web}, "aspnet", ProfileWeb, 8080, ""},
		{"worker", map[string]string{"Worker.csproj": worker}, "dotnet", ProfileWorker, 0, ""},
		{"web among several", map[string]string{"Api.csproj": web, "Core.csproj": library}, "aspnet", ProfileWeb, 8080, ""},
		{"old target", map[string]string{"Old.csproj": `<Project Sdk="Microsoft.NET.Sdk.Web"><PropertyGroup><TargetFramework>net6.0</TargetFramework></PropertyGroup></Project>`}, "dotnet", ProfileWorker, 0, "net6.0"},
		{"two web projects", map[string]string{"A.csproj": web, "B.csproj": web}, "dotnet", ProfileWorker, 0, "several .NET projects"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			for path, content := range fixture.files {
				writeBuildFixture(t, root, path, content)
			}
			result, err := (Detector{}).DetectPath(context.Background(), root, SourceIdentity{})
			if err != nil || len(result.Candidates) != 1 {
				t.Fatalf("detect: %+v, %v", result, err)
			}
			candidate := result.Candidates[0]
			if candidate.Recipe != "dotnet" || candidate.Framework != fixture.framework || candidate.Profile != fixture.profile ||
				candidate.Port != fixture.port || !strings.Contains(candidate.RecipeIssue, fixture.issue) {
				t.Fatalf("candidate = %+v", candidate)
			}
		})
	}
	root := t.TempDir()
	writeBuildFixture(t, root, "Api.csproj", web)
	writeBuildFixture(t, root, "Core.csproj", library)
	prepared, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), root, BuildPlanConfig{Method: BuildRecipe, Recipe: "dotnet"}, false, "t:1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"FROM mcr.microsoft.com/dotnet/sdk:8.0@sha256:", "dotnet restore Api.csproj", "dotnet publish Api.csproj -c Release --no-restore -o /out",
		"test -f /out/Shop.Api.dll", "FROM mcr.microsoft.com/dotnet/aspnet:8.0@sha256:", "USER app", `ASPNETCORE_HTTP_PORTS=${PORT:-8080} dotnet /app/Shop.Api.dll`} {
		if !strings.Contains(prepared.DockerfilePreview, want) {
			t.Fatalf("Dockerfile missing %q:\n%s", want, prepared.DockerfilePreview)
		}
	}
	root = t.TempDir()
	writeBuildFixture(t, root, "Worker.csproj", worker)
	prepared, err = NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), root, BuildPlanConfig{Method: BuildRecipe, Recipe: "dotnet"}, false, "t:1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prepared.DockerfilePreview, "FROM mcr.microsoft.com/dotnet/runtime:9.0@sha256:") || prepared.Toolchain != "dotnet 9.0" {
		t.Fatalf("worker runtime:\n%s", prepared.DockerfilePreview)
	}
}

func TestDenoDetectionAndRecipe(t *testing.T) {
	t.Parallel()
	config := parseDenoConfig([]byte("{\n  // comment\n  \"tasks\": {\"start\": \"deno run -A main.ts\", \"build\": {\"command\": \"deno run -A build.ts\"},},\n  \"imports\": {\"@fresh/core\": \"jsr:@fresh/core@^2\"}\n}"))
	if config.task("start") != "deno run -A main.ts" || config.task("build") != "deno run -A build.ts" || config.Imports["@fresh/core"] == "" {
		t.Fatalf("config = %+v", config)
	}
	for _, fixture := range []struct {
		name      string
		files     map[string]string
		framework string
		start     string
		build     string
		unpinned  bool
	}{
		{"start task", map[string]string{"deno.json": `{"tasks":{"start":"deno run -A main.ts"},"imports":{"hono":"jsr:hono"}}`, "deno.lock": "{}"}, "deno", "deno task start", "", false},
		{"fresh with build", map[string]string{"deno.jsonc": "{\n  \"tasks\": {\"build\": \"deno run -A dev.ts build\", \"start\": \"deno serve -A _fresh/server.ts\"},\n  \"imports\": {\"fresh\": \"jsr:@fresh/core@^2\"}\n}"}, "fresh", "deno task start", "deno task build", true},
		{"entry file", map[string]string{"deno.json": `{}`, "server.ts": "Deno.serve(() => new Response('x'))"}, "deno", "deno run --allow-all server.ts", "", false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			for path, content := range fixture.files {
				writeBuildFixture(t, root, path, content)
			}
			result, err := (Detector{}).DetectPath(context.Background(), root, SourceIdentity{})
			if err != nil || len(result.Candidates) != 1 {
				t.Fatalf("detect: %+v, %v", result, err)
			}
			candidate := result.Candidates[0]
			if candidate.Recipe != "deno" || candidate.Framework != fixture.framework || candidate.StartCommand != fixture.start ||
				candidate.BuildCommand != fixture.build || candidate.Port != 8000 || candidate.Profile != ProfileWeb || candidate.UnpinnedDependencies != fixture.unpinned {
				t.Fatalf("candidate = %+v", candidate)
			}
		})
	}
	root := t.TempDir()
	writeBuildFixture(t, root, "deno.json", `{"tasks":{"start":"deno run -A main.ts","build":"deno task gen"}}`)
	writeBuildFixture(t, root, "deno.lock", "{}")
	prepared, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), root, BuildPlanConfig{Method: BuildRecipe, Recipe: "deno", BuildCommand: "deno task build", StartCommand: "deno task start"}, false, "t:1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"FROM denoland/deno:alpine-2.9.7@sha256:", "RUN deno install --frozen", "RUN deno task build", `CMD ["/bin/sh","-c","deno task start"]`} {
		if !strings.Contains(prepared.DockerfilePreview, want) {
			t.Fatalf("Dockerfile missing %q:\n%s", want, prepared.DockerfilePreview)
		}
	}
	if _, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), root, BuildPlanConfig{Method: BuildRecipe, Recipe: "deno"}, false, "t:1"); !errors.Is(err, ErrUnsupportedBuilder) {
		t.Fatalf("missing start accepted: %v", err)
	}
}

// A repository is read for every language at once: a Rust crate beside a
// Python service beside a Deno tool each become their own candidate, none
// of them claiming the others' files.
func TestCompiledLanguagesCoexistAsSeparateRoots(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeBuildFixture(t, root, "services/api/Cargo.toml", "[package]\nname = \"api\"\n[dependencies]\nactix-web = \"4\"\n")
	writeBuildFixture(t, root, "services/api/src/main.rs", "fn main() {}")
	writeBuildFixture(t, root, "services/worker/requirements.txt", "celery\n")
	writeBuildFixture(t, root, "services/worker/main.py", "print('x')\n")
	writeBuildFixture(t, root, "tools/deno.json", `{"tasks":{"start":"deno run -A main.ts"}}`)
	writeBuildFixture(t, root, "tools/main.ts", "Deno.serve(() => new Response('x'))")
	result, err := (Detector{}).DetectPath(context.Background(), root, SourceIdentity{})
	if err != nil || len(result.Candidates) != 3 {
		t.Fatalf("detect: %+v, %v", result, err)
	}
	recipes := map[string]string{}
	for _, candidate := range result.Candidates {
		recipes[candidate.Root] = candidate.Recipe + ":" + candidate.Framework
	}
	want := map[string]string{"services/api": "rust:actix-web", "services/worker": "python:python", "tools": "deno:deno"}
	for root, expected := range want {
		if recipes[root] != expected {
			t.Fatalf("roots = %v", recipes)
		}
	}
	// The framework named with confidence wins the default selection; the
	// other roots stay offered as candidates.
	for _, candidate := range result.Candidates {
		if candidate.Root == "services/api" && result.SelectedID != candidate.ID {
			t.Fatalf("selected %q, want the actix-web root %q", result.SelectedID, candidate.ID)
		}
	}
}

func TestRustDefaultRunSelectsTheApplicationInsteadOfTheFirstBinary(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name string
		bins string
	}{
		{"explicit binaries", "\n[[bin]]\nname = \"maintenance\"\n[[bin]]\nname = \"serve\"\n"},
		{"automatic binary targets", ""},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			root := t.TempDir()
			writeBuildFixture(t, root, "Cargo.toml", "[package]\nname = \"api\"\ndefault-run = \"serve\"\n[dependencies]\naxum = \"0.8\"\n"+fixture.bins)
			result, err := (Detector{}).DetectPath(context.Background(), root, SourceIdentity{})
			if err != nil || len(result.Candidates) != 1 {
				t.Fatalf("detect: %+v, %v", result, err)
			}
			candidate := result.Candidates[0]
			found := false
			for _, evidence := range candidate.Evidence {
				found = found || evidence.Reason == "binary target serve"
			}
			if !found || strings.Contains(strings.Join(candidate.NeedsDecision, " "), "several binaries") {
				t.Fatalf("default-run was not honored: %+v", candidate)
			}
			prepared, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), root,
				BuildPlanConfig{Method: BuildRecipe, Recipe: "rust"}, false, "t:1")
			if err != nil || !strings.Contains(prepared.DockerfilePreview, "cp /src/target/release/serve /out/app") {
				t.Fatalf("wrong runtime binary: %v\n%s", err, prepared.DockerfilePreview)
			}
		})
	}
}

func TestDotnetMultiTargetBuildRestoresAndPublishesOneSupportedTarget(t *testing.T) {
	t.Parallel()
	for _, frameworks := range []string{"net8.0;net10.0", "net10.0;net8.0", "net6.0;net10.0-windows;net10.0"} {
		t.Run(frameworks, func(t *testing.T) {
			root := t.TempDir()
			writeBuildFixture(t, root, "Api.csproj", `<Project Sdk="Microsoft.NET.Sdk.Web"><PropertyGroup><TargetFrameworks>`+frameworks+`</TargetFrameworks></PropertyGroup></Project>`)
			prepared, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), root,
				BuildPlanConfig{Method: BuildRecipe, Recipe: "dotnet"}, false, "t:1")
			if err != nil {
				t.Fatal(err)
			}
			for _, expected := range []string{"dotnet/sdk:10.0@sha256:", "dotnet restore Api.csproj -p:TargetFramework=net10.0",
				"dotnet publish Api.csproj -c Release --no-restore -o /out --framework net10.0", "dotnet/aspnet:10.0@sha256:"} {
				if !strings.Contains(prepared.DockerfilePreview, expected) {
					t.Fatalf("multi-target recipe missing %q:\n%s", expected, prepared.DockerfilePreview)
				}
			}
		})
	}
	for _, framework := range []string{"net8.0-windows", "net10.0-android", "net10.0;net8.0"} {
		_, err := parseDotnetProject("Api.csproj", []byte(`<Project Sdk="Microsoft.NET.Sdk.Web"><PropertyGroup><TargetFramework>`+framework+`</TargetFramework></PropertyGroup></Project>`))
		if !errors.Is(err, ErrUnsupportedBuilder) {
			t.Fatalf("unsupported single target %s was accepted: %v", framework, err)
		}
	}
}
