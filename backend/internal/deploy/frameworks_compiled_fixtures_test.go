package deploy

import (
	"context"
	"path/filepath"
	"testing"
)

// The live fixtures the JVM and .NET recipes are proven on
// (TestLiveDetectedFrameworkBuildAndServing) are detected and prepared the
// same way without Docker, so a change that would break them fails here
// first.
func TestCompiledLiveFixturesDetectAndPrepare(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name, root, framework, recipe, context string
		want                                   []string
	}{
		{"java-reactor", "app", "spring-boot", "java", ".", []string{"RUN mvn -B -ntp -DskipTests -pl :jd-acceptance-app -am package", "FROM eclipse-temurin:17-jre@"}},
		{"gradle-multiproject", "app", "java", "java", ".", []string{"RUN gradle --no-daemon --console=plain :app:installDist", `CMD ["/app/bin/app"]`}},
		{"dotnet-solution", "src/Shop.Api", "aspnet", "dotnet", ".", []string{"WORKDIR /src/src/Shop.Api", "RUN dotnet publish Shop.Api.csproj -c Release --no-restore -o /out", "FROM mcr.microsoft.com/dotnet/sdk:10.0@"}},
		{"blazor-wasm", "", "blazor-wasm", "dotnet", "", []string{"COPY --from=build /out/wwwroot/ /usr/share/nginx/html/"}},
		{"fsharp", "", "aspnet", "dotnet", "", []string{"RUN dotnet restore App.fsproj", "dotnet /app/App.dll"}},
		{"dotnet-spa", "", "aspnet", "dotnet", "", []string{"FROM node:22-bookworm-slim@", "COPY --from=spa-node /usr/local/ /usr/local/", "WORKDIR /src/ClientApp\nRUN npm ci"}},
		{"gradle-composite", "backend/app", "java", "java", ".", []string{"RUN gradle --no-daemon --console=plain -p backend :app:installDist", "RUN cd /src/backend/app/build/install && "}},
		{"dotnet-multitarget", "src/Api", "aspnet", "dotnet", "src", []string{"WORKDIR /src/Api", "RUN dotnet restore Api.csproj\n",
			"RUN dotnet publish Api.csproj -c Release --no-restore -o /out --framework net9.0", "FROM mcr.microsoft.com/dotnet/aspnet:9.0@"}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			copyFrameworkFixture(t, fixture.name, root)
			result, err := (Detector{}).DetectPath(context.Background(), root, SourceIdentity{})
			if err != nil {
				t.Fatal(err)
			}
			candidate := selectedDetectionCandidate(&result)
			if candidate == nil || candidate.Root != fixture.root || candidate.Framework != fixture.framework || candidate.Recipe != fixture.recipe || candidate.RecipeIssue != "" {
				t.Fatalf("selected %+v of %+v", candidate, result.Candidates)
			}
			prepared, err := NewArtifactBuilder(&artifactBackendFake{}).PrepareWithin(context.Background(), root, filepath.Join(root, filepath.FromSlash(candidate.Root)),
				BuildPlanConfig{Method: BuildRecipe, Recipe: candidate.Recipe, OutputDirectory: candidate.OutputDirectory, SPAFallback: candidate.SPAFallback}, false, "t:1")
			if err != nil {
				t.Fatal(err)
			}
			if prepared.ContextDirectory != fixture.context {
				t.Fatalf("context = %q", prepared.ContextDirectory)
			}
			assertCompiledDockerfile(t, prepared.DockerfilePreview, fixture.want...)
		})
	}
}
