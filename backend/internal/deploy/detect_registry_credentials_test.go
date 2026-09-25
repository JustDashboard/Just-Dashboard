package deploy

import (
	"slices"
	"strings"
	"testing"
)

// A private registry's credentials are named by the build's own
// configuration; each becomes an install-scoped variable preflight asks
// for, whose value reaches only the step that downloads dependencies.

func compiledInstallVariable(candidate *DetectedCandidate, name string) *DetectedVariable {
	for index := range candidate.Variables {
		if candidate.Variables[index].Name == name {
			return &candidate.Variables[index]
		}
	}
	return nil
}

func TestMavenSettingsCredentialsReachTheBuild(t *testing.T) {
	t.Parallel()
	pom := `<project><artifactId>api</artifactId>
  <repositories><repository><id>github</id><url>https://maven.pkg.github.com/acme/libs</url></repository></repositories>
  <build><plugins><plugin><artifactId>spring-boot-maven-plugin</artifactId></plugin></plugins></build></project>`
	settings := `<settings><servers>
  <server><id>github</id><username>${env.GITHUB_ACTOR}</username><password>${env.GITHUB_TOKEN}</password></server>
  <server><id>releases</id><password>${env.RELEASE_TOKEN}</password></server>
</servers></settings>`
	for name, files := range map[string]map[string]string{
		"passed by maven.config": {"pom.xml": pom, ".mvn/maven.config": "-s .mvn/ci-settings.xml\n", ".mvn/ci-settings.xml": settings},
		"committed settings.xml": {"pom.xml": pom, "settings.xml": settings},
		"settings under .mvn":    {"pom.xml": pom, ".mvn/settings.xml": settings},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			result, root := detectCompiled(t, files)
			candidate := &result.Candidates[0]
			token := compiledInstallVariable(candidate, "GITHUB_TOKEN")
			release := compiledInstallVariable(candidate, "RELEASE_TOKEN")
			if token == nil || token.Step != "install" || !token.InstallRequired || release == nil || release.InstallRequired {
				t.Fatalf("variables = %+v", candidate.Variables)
			}
			config := BuildPlanConfig{Method: BuildRecipe, Recipe: "java", Secrets: []BuildSecretConfig{{Variable: "GITHUB_TOKEN", Step: "install"}, {Variable: "GITHUB_ACTOR", Step: "install"}}}
			prepared, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(t.Context(), root, config, false, "t:1", "GITHUB_ACTOR", "GITHUB_TOKEN")
			if err != nil {
				t.Fatal(err)
			}
			// Maven resolves dependencies as it builds, so the install's
			// credentials are mounted on the build that downloads.
			command := "RUN --mount=type=secret,id=GITHUB_ACTOR,env=GITHUB_ACTOR,required=true --mount=type=secret,id=GITHUB_TOKEN,env=GITHUB_TOKEN,required=true mvn -B -ntp -DskipTests"
			switch name {
			case "committed settings.xml":
				command += " -s settings.xml"
			case "settings under .mvn":
				command += " -s .mvn/settings.xml"
			}
			assertCompiledDockerfile(t, prepared.DockerfilePreview, command+" package")
			if strings.Contains(prepared.DockerfilePreview, "ghp_") {
				t.Fatal("a value reached the Dockerfile")
			}
			findings := compiledRecipeFindings(candidate, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe, Recipe: "java"}})
			if item := findingByCode(findings, "registry_token_missing"); item == nil || item.Severity != PreflightBlocked {
				t.Fatalf("findings = %+v", findings)
			}
			mapped := PlanConfiguration{Build: config, Variables: []PlannedVariable{
				{Name: "GITHUB_TOKEN", Scopes: []string{"build"}}, {Name: "GITHUB_ACTOR", Scopes: []string{"build"}}, {Name: "RELEASE_TOKEN", Scopes: []string{"build"}},
			}}
			mapped.Build.Secrets = append(mapped.Build.Secrets, BuildSecretConfig{Variable: "RELEASE_TOKEN", Step: "install"})
			if item := findingByCode(compiledRecipeFindings(candidate, mapped), "registry_token_missing"); item != nil {
				t.Fatalf("mapped credentials still reported: %+v", item)
			}
		})
	}
}

func TestGradleRepositoryCredentialsAreInstallVariables(t *testing.T) {
	t.Parallel()
	result, _ := detectCompiled(t, map[string]string{
		"settings.gradle.kts": "dependencyResolutionManagement {\n  repositories {\n    mavenCentral()\n    maven {\n      url = uri(\"https://maven.pkg.github.com/acme/libs\")\n" +
			"      credentials {\n        username = System.getenv(\"GPR_USER\")\n        password = providers.environmentVariable(\"GPR_KEY\").get()\n      }\n    }\n  }\n}\nrootProject.name = \"svc\"\n",
		"build.gradle.kts": "plugins { application }\napplication { mainClass.set(\"x.Main\") }\nrepositories {\n  maven {\n    name = \"acmeReleases\"\n    url = uri(\"https://repo.acme.test/releases\")\n    credentials(PasswordCredentials::class)\n  }\n}\n" +
			"publishing {\n  repositories {\n    maven {\n      credentials {\n        password = System.getenv(\"PUBLISH_TOKEN\")\n      }\n    }\n  }\n}\n",
	})
	candidate := &result.Candidates[0]
	for _, name := range []string{"GPR_USER", "GPR_KEY", "ORG_GRADLE_PROJECT_acmeReleasesUsername", "ORG_GRADLE_PROJECT_acmeReleasesPassword"} {
		if variable := compiledInstallVariable(candidate, name); variable == nil || variable.Step != "install" || !variable.InstallRequired {
			t.Fatalf("%s: %+v", name, candidate.Variables)
		}
	}
	if variable := compiledInstallVariable(candidate, "PUBLISH_TOKEN"); variable != nil && variable.Step == "install" {
		t.Fatalf("a publishing credential became an install one: %+v", variable)
	}
	// The draft keeps them out of the running application.
	draft := &Draft{Data: DraftData{Detection: &DetectionResult{Candidates: result.Candidates, SelectedID: candidate.ID}}}
	if only := draft.installOnlyCredentials(); !only["GPR_KEY"] || !only["ORG_GRADLE_PROJECT_acmeReleasesPassword"] {
		t.Fatalf("install-only = %v", only)
	}
}

func TestNuGetConfigCredentialsAreInstallVariables(t *testing.T) {
	t.Parallel()
	result, root := detectCompiled(t, map[string]string{
		"NuGet.Config": `<?xml version="1.0" encoding="utf-8"?>
<configuration>
  <packageSources>
    <clear />
    <add key="nuget.org" value="https://api.nuget.org/v3/index.json" />
    <add key="Acme Feed" value="https://pkgs.dev.azure.com/acme/_packaging/feed/nuget/v3/index.json" />
  </packageSources>
  <packageSourceCredentials>
    <Acme_x0020_Feed>
      <add key="Username" value="acme" />
      <add key="ClearTextPassword" value="%ACME_FEED_TOKEN%" />
    </Acme_x0020_Feed>
    <Old>
      <add key="ClearTextPassword" value="%OLD_FEED_TOKEN%" />
    </Old>
  </packageSourceCredentials>
</configuration>`,
		"src/Api/Api.csproj": `<Project Sdk="Microsoft.NET.Sdk.Web"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>`,
	})
	api := compiledCandidate(result, "src/Api", "dotnet")
	if api == nil || api.DotnetBuild.Context != "." {
		t.Fatalf("candidates = %+v", result.Candidates)
	}
	token, old := compiledInstallVariable(api, "ACME_FEED_TOKEN"), compiledInstallVariable(api, "OLD_FEED_TOKEN")
	if token == nil || token.Step != "install" || !token.InstallRequired || old == nil || old.InstallRequired {
		t.Fatalf("variables = %+v", api.Variables)
	}
	config := BuildPlanConfig{Method: BuildRecipe, Recipe: "dotnet", Secrets: []BuildSecretConfig{{Variable: "ACME_FEED_TOKEN", Step: "install"}}}
	prepared, err := NewArtifactBuilder(&artifactBackendFake{}).PrepareWithin(t.Context(), root, root+"/src/Api", config, false, "t:1", "ACME_FEED_TOKEN")
	if err != nil {
		t.Fatal(err)
	}
	assertCompiledDockerfile(t, prepared.DockerfilePreview, "RUN --mount=type=secret,id=ACME_FEED_TOKEN,env=ACME_FEED_TOKEN,required=true dotnet restore Api.csproj\n",
		"RUN dotnet publish Api.csproj")
	if !slices.ContainsFunc(api.Evidence, func(evidence DetectionEvidence) bool { return strings.Contains(evidence.Reason, "published from .") }) {
		t.Fatalf("evidence = %+v", api.Evidence)
	}
}
