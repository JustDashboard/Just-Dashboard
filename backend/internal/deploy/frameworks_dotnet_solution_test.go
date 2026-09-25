package deploy

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// A .NET project is published from the directory that holds everything it
// builds with: the projects it references and the MSBuild, NuGet and SDK
// files that apply to it, with the properties Directory.Build.props sets.

const solutionWebProject = `<Project Sdk="Microsoft.NET.Sdk.Web">
  <ItemGroup>
    <ProjectReference Include="..\MyApp.Core\MyApp.Core.csproj" />
  </ItemGroup>
  <ItemGroup>
    <PackageReference Include="Microsoft.EntityFrameworkCore.Design" />
  </ItemGroup>
</Project>`

func solutionFiles() map[string]string {
	return map[string]string{
		"MyApp.sln":                        "Microsoft Visual Studio Solution File, Format Version 12.00\n" + `Project("{FAE04EC0-301F-11D3-BF4B-00C04F79EFBC}") = "MyApp.Api", "src\MyApp.Api\MyApp.Api.csproj", "{1}"` + "\nEndProject\n",
		"Directory.Build.props":            "<Project>\n  <PropertyGroup>\n    <TargetFramework>net10.0</TargetFramework>\n    <Nullable>enable</Nullable>\n    <AssemblyName>$(MSBuildProjectName).Host</AssemblyName>\n  </PropertyGroup>\n</Project>\n",
		"Directory.Packages.props":         "<Project>\n  <PropertyGroup><ManagePackageVersionsCentrally>true</ManagePackageVersionsCentrally></PropertyGroup>\n  <ItemGroup><PackageVersion Include=\"Microsoft.EntityFrameworkCore.Design\" Version=\"10.0.0\" /></ItemGroup>\n</Project>\n",
		"global.json":                      "{\n  // the SDK every developer builds with\n  \"sdk\": { \"version\": \"10.0.100\", \"rollForward\": \"latestFeature\" }\n}\n",
		"src/MyApp.Api/MyApp.Api.csproj":   solutionWebProject,
		"src/MyApp.Api/Program.cs":         "var builder = WebApplication.CreateBuilder(args);\nvar app = builder.Build();\napp.MapGet(\"/\", () => \"ok\");\napp.Run();\n",
		"src/MyApp.Core/MyApp.Core.csproj": `<Project Sdk="Microsoft.NET.Sdk"></Project>`,
		"tests/MyApp.Tests/MyApp.Tests.csproj": `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><IsPackable>false</IsPackable></PropertyGroup>` +
			`<ItemGroup><PackageReference Include="Microsoft.NET.Test.Sdk" /><PackageReference Include="xunit" /><ProjectReference Include="..\..\src\MyApp.Api\MyApp.Api.csproj" /></ItemGroup></Project>`,
	}
}

func TestDotnetSolutionPublishesTheWebProjectFromTheSolutionRoot(t *testing.T) {
	t.Parallel()
	result, root := detectCompiled(t, solutionFiles())
	if len(result.Candidates) != 1 {
		t.Fatalf("candidates = %+v", result.Candidates)
	}
	api := result.Candidates[0]
	if api.Root != "src/MyApp.Api" || api.Framework != "aspnet" || api.Confidence != ConfidenceHigh || api.RecipeIssue != "" || result.SelectedID != api.ID ||
		api.DotnetBuild == nil || api.DotnetBuild.Context != "." || !slices.Equal(api.DotnetBuild.Targets, []string{"10.0"}) ||
		api.DotnetBuild.SDKPin != "10.0.100" || api.DotnetBuild.RollForward != "latestFeature" {
		t.Fatalf("api = %+v / %+v", api, api.DotnetBuild)
	}
	for _, path := range []string{"src/MyApp.Core", "tests/MyApp.Tests"} {
		if !slices.ContainsFunc(result.SetAside, func(item DetectionSetAside) bool { return item.Path == path }) {
			t.Fatalf("%s not set aside: %+v", path, result.SetAside)
		}
	}
	prepared := prepareCompiled(t, root, "src/MyApp.Api", BuildPlanConfig{Method: BuildRecipe, Recipe: "dotnet", RootDirectory: "src/MyApp.Api"})
	if prepared.ContextDirectory != "." || prepared.Toolchain != "dotnet 10.0" {
		t.Fatalf("prepared = %+v", prepared)
	}
	assertCompiledDockerfile(t, prepared.DockerfilePreview, "FROM mcr.microsoft.com/dotnet/sdk:10.0@sha256:", "WORKDIR /src\nCOPY . .\nWORKDIR /src/src/MyApp.Api",
		"RUN dotnet restore MyApp.Api.csproj\n", "RUN dotnet publish MyApp.Api.csproj -c Release --no-restore -o /out\n",
		"test -f /out/MyApp.Api.Host.dll", "FROM mcr.microsoft.com/dotnet/aspnet:10.0@sha256:", "dotnet /app/MyApp.Api.Host.dll")
	findings := compiledRecipeFindings(&api, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe, Recipe: "dotnet"}})
	for _, code := range []string{"dotnet_toolchain", "dotnet_project_selected"} {
		if item := findingByCode(findings, code); item == nil || item.Severity != PreflightPass {
			t.Fatalf("%s: %+v", code, findings)
		}
	}
}

func TestGlobalJSONPinChoosesTheSDKImage(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		facts    dotnetToolchainFacts
		override string
		sdk      string
		target   string
		explicit bool
		refused  bool
	}{
		{facts: dotnetToolchainFacts{targets: []string{"8.0"}, targetText: "net8.0"}, sdk: "8.0", target: "8.0"},
		{facts: dotnetToolchainFacts{targets: []string{"8.0"}, targetText: "net8.0", pin: "8.0.100", pinFrom: "global.json"}, sdk: "8.0.100", target: "8.0"},
		{facts: dotnetToolchainFacts{targets: []string{"8.0"}, targetText: "net8.0", pin: "8.0.100", pinFrom: "global.json", rollForward: "disable"}, sdk: "8.0.100", target: "8.0"},
		{facts: dotnetToolchainFacts{targets: []string{"8.0"}, targetText: "net8.0", pin: "8.0.100", pinFrom: "global.json", rollForward: "latestFeature"}, sdk: "8.0", target: "8.0"},
		{facts: dotnetToolchainFacts{targets: []string{"8.0"}, targetText: "net8.0", pin: "9.0.100", pinFrom: "global.json", rollForward: "latestMajor"}, sdk: "9.0", target: "8.0"},
		{facts: dotnetToolchainFacts{targets: []string{"9.0"}, targetText: "net9.0", pin: "8.0.100", pinFrom: "global.json", rollForward: "latestMajor"}, sdk: "9.0", target: "9.0"},
		{facts: dotnetToolchainFacts{targets: []string{"9.0"}, targetText: "net9.0", pin: "8.0.100", pinFrom: "global.json"}, refused: true},
		{facts: dotnetToolchainFacts{targets: []string{"8.0", "10.0"}, targetText: "net8.0;net10.0", multi: true}, sdk: "10.0", target: "10.0", explicit: true},
		{facts: dotnetToolchainFacts{targets: []string{"8.0", "10.0"}, targetText: "net8.0;net10.0", multi: true}, override: "8.0", sdk: "8.0", target: "8.0", explicit: true},
		{facts: dotnetToolchainFacts{targetText: "net6.0"}, refused: true},
		{facts: dotnetToolchainFacts{targetText: "net6.0"}, override: "8.0", sdk: "8.0", target: "8.0", explicit: true},
		{facts: dotnetToolchainFacts{targetText: "$(AppTarget)"}, override: "9.0", sdk: "9.0", target: "9.0", explicit: true},
		{facts: dotnetToolchainFacts{targets: []string{"8.0"}, targetText: "net8.0", pin: "8.0", pinFrom: ".tool-versions"}, sdk: "8.0", target: "8.0"},
	} {
		plan, err := planDotnetToolchain(fixture.facts, fixture.override)
		var refusal toolchainVersionError
		switch {
		case fixture.refused:
			if !errors.As(err, &refusal) || refusal.code != "dotnet_version_unsupported" {
				t.Errorf("%+v: %v", fixture.facts, err)
			}
		case err != nil || plan.sdk != fixture.sdk || plan.target != fixture.target || plan.explicit != fixture.explicit:
			t.Errorf("%+v %q: %+v, %v", fixture.facts, fixture.override, plan, err)
		}
	}
	// The pin is read from the directory dotnet runs in, and the recipe
	// builds with the exact SDK a patch-level policy asks for.
	result, root := detectCompiled(t, map[string]string{
		"global.json": `{"sdk":{"version":"8.0.100"}}`,
		"Api.csproj":  `<Project Sdk="Microsoft.NET.Sdk.Web"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>`,
	})
	if build := result.Candidates[0].DotnetBuild; build.SDKPin != "8.0.100" || build.SDKPinFrom != "global.json" {
		t.Fatalf("build = %+v", build)
	}
	prepared := prepareCompiled(t, root, "", BuildPlanConfig{Method: BuildRecipe, Recipe: "dotnet"})
	assertCompiledDockerfile(t, prepared.DockerfilePreview, "FROM mcr.microsoft.com/dotnet/sdk:8.0.100@sha256:", "FROM mcr.microsoft.com/dotnet/aspnet:8.0@sha256:")
	if prepared.Toolchain != "dotnet 8.0 (sdk 8.0.100)" {
		t.Fatalf("toolchain = %q", prepared.Toolchain)
	}
	// A setting that cannot build is the plan's to fix, not the source's.
	findings := compiledRecipeFindings(&result.Candidates[0], PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe, Recipe: "dotnet", DotnetVersion: "10.0"}})
	if item := findingByCode(findings, "dotnet_version_unsupported"); item == nil || item.FieldID != "configuration.build.dotnetVersion" || !strings.Contains(item.Measured, "8.0.100") {
		t.Fatalf("findings = %+v", findings)
	}
}

func TestDotnetProjectKinds(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name      string
		files     map[string]string
		framework string
		profile   WorkloadProfile
		port      int
		decision  string
		runtime   string
	}{
		{"worker sdk", map[string]string{"Worker.csproj": `<Project Sdk="Microsoft.NET.Sdk.Worker"><PropertyGroup><TargetFramework>net9.0</TargetFramework></PropertyGroup></Project>`},
			"dotnet", ProfileWorker, 0, "", "mcr.microsoft.com/dotnet/runtime:9.0"},
		{"giraffe f#", map[string]string{"App.fsproj": `<Project Sdk="Microsoft.NET.Sdk.Web"><PropertyGroup><TargetFramework>net9.0</TargetFramework></PropertyGroup>` +
			`<ItemGroup><Compile Include="Program.fs" /></ItemGroup><ItemGroup><PackageReference Include="Giraffe" Version="7.0.2" /></ItemGroup></Project>`,
			"Program.fs": "open Giraffe\n"}, "aspnet", ProfileWeb, 8080, "", "mcr.microsoft.com/dotnet/aspnet:9.0"},
		{"visual basic console", map[string]string{"Tool.vbproj": `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><OutputType>Exe</OutputType><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>`},
			"dotnet", ProfileWorker, 0, "confirm whether this program serves HTTP", "mcr.microsoft.com/dotnet/runtime:8.0"},
		{"console hosting kestrel", map[string]string{"Host.csproj": `<Project><Sdk Name="Microsoft.NET.Sdk" /><PropertyGroup><OutputType>Exe</OutputType><TargetFramework>net8.0</TargetFramework></PropertyGroup>` +
			`<ItemGroup><FrameworkReference Include="Microsoft.AspNetCore.App" /></ItemGroup></Project>`}, "aspnet", ProfileWeb, 8080, "", "mcr.microsoft.com/dotnet/aspnet:8.0"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			result, root := detectCompiled(t, fixture.files)
			if len(result.Candidates) != 1 {
				t.Fatalf("candidates = %+v", result.Candidates)
			}
			candidate := result.Candidates[0]
			if candidate.Recipe != "dotnet" || candidate.Framework != fixture.framework || candidate.Profile != fixture.profile || candidate.Port != fixture.port ||
				!strings.Contains(strings.Join(candidate.NeedsDecision, " "), fixture.decision) || (fixture.decision == "" && len(candidate.NeedsDecision) > 0) {
				t.Fatalf("candidate = %+v", candidate)
			}
			assertCompiledDockerfile(t, prepareCompiled(t, root, "", BuildPlanConfig{Method: BuildRecipe, Recipe: "dotnet"}).DockerfilePreview, "FROM "+fixture.runtime+"@sha256:")
		})
	}
	// A test project beside the application is set aside; alone it is
	// still named, and the recipe refuses it.
	result, root := detectCompiled(t, map[string]string{"Api.Tests.csproj": `<Project Sdk="MSTest.Sdk/3.6.0"><PropertyGroup><TargetFramework>net9.0</TargetFramework></PropertyGroup></Project>`})
	if len(result.Candidates) != 1 || result.Candidates[0].Confidence != ConfidenceLow {
		t.Fatalf("candidates = %+v", result.Candidates)
	}
	if _, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), root, BuildPlanConfig{Method: BuildRecipe, Recipe: "dotnet"}, false, "t:1"); err == nil || !strings.Contains(err.Error(), "test project") {
		t.Fatalf("test project prepared: %v", err)
	}
}

func TestDotnetNativePublishingIsTurnedOffOnBothSteps(t *testing.T) {
	t.Parallel()
	result, root := detectCompiled(t, map[string]string{
		"Directory.Build.props": "<Project><PropertyGroup><PublishAot>true</PublishAot></PropertyGroup></Project>",
		"Api.csproj":            `<Project Sdk="Microsoft.NET.Sdk.Web"><PropertyGroup><TargetFramework>net10.0</TargetFramework><InvariantGlobalization>true</InvariantGlobalization></PropertyGroup></Project>`,
	})
	if build := result.Candidates[0].DotnetBuild; !slices.Equal(build.Native, []string{"PublishAot"}) {
		t.Fatalf("build = %+v", build)
	}
	prepared := prepareCompiled(t, root, "", BuildPlanConfig{Method: BuildRecipe, Recipe: "dotnet"})
	assertCompiledDockerfile(t, prepared.DockerfilePreview,
		"RUN dotnet restore Api.csproj "+dotnetPublishDisabled+"\n", "RUN dotnet publish Api.csproj -c Release --no-restore -o /out "+dotnetPublishDisabled+"\n", "test -f /out/Api.dll")
	if item := findingByCode(compiledRecipeFindings(&result.Candidates[0], PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe, Recipe: "dotnet"}}), "dotnet_aot_disabled"); item == nil {
		t.Fatal("no dotnet_aot_disabled finding")
	}
}

const blazorClientProject = `<Project Sdk="Microsoft.NET.Sdk.BlazorWebAssembly">
  <PropertyGroup><TargetFramework>net9.0</TargetFramework></PropertyGroup>
  <ItemGroup><PackageReference Include="Microsoft.AspNetCore.Components.WebAssembly" Version="9.0.0" /></ItemGroup>
</Project>`

func TestBlazorWebAssemblyIsPublishedAndServedAsAStaticSite(t *testing.T) {
	t.Parallel()
	result, root := detectCompiled(t, map[string]string{
		"Client.csproj":       blazorClientProject,
		"wwwroot/index.html":  "<!DOCTYPE html><html><body><div id=\"app\"></div><script src=\"_framework/blazor.webassembly.js\"></script></body></html>",
		"Program.cs":          "var builder = WebAssemblyHostBuilder.CreateDefault(args);\n",
		"wwwroot/css/app.css": "body{}",
	})
	if len(result.Candidates) != 1 {
		t.Fatalf("candidates = %+v", result.Candidates)
	}
	client := result.Candidates[0]
	if client.Framework != "blazor-wasm" || client.Profile != ProfileStatic || client.Port != 80 || client.OutputDirectory != "wwwroot" || !client.SPAFallback ||
		client.Confidence != ConfidenceHigh || result.SelectedID != client.ID {
		t.Fatalf("client = %+v", client)
	}
	prepared := prepareCompiled(t, root, "", BuildPlanConfig{Method: BuildRecipe, Recipe: "dotnet", OutputDirectory: "wwwroot", SPAFallback: true})
	assertCompiledDockerfile(t, prepared.DockerfilePreview, "FROM mcr.microsoft.com/dotnet/sdk:9.0@sha256:", "RUN dotnet publish Client.csproj -c Release --no-restore -o /out",
		"RUN test -f /out/wwwroot/index.html", "FROM nginx:1.29-alpine@sha256:", "try_files $uri $uri/ /index.html;", "COPY --from=build /out/wwwroot/ /usr/share/nginx/html/")
	// A client its ASP.NET Core host publishes is part of the host.
	result, _ = detectCompiled(t, map[string]string{
		"Server/Server.csproj": `<Project Sdk="Microsoft.NET.Sdk.Web"><PropertyGroup><TargetFramework>net9.0</TargetFramework></PropertyGroup><ItemGroup><ProjectReference Include="..\Client\Client.csproj" /></ItemGroup></Project>`,
		"Client/Client.csproj": blazorClientProject, "Client/wwwroot/index.html": "<html></html>",
	})
	if len(result.Candidates) != 1 || result.Candidates[0].Root != "Server" || result.Candidates[0].Framework != "aspnet" {
		t.Fatalf("candidates = %+v", result.Candidates)
	}
}

func TestDotnetSPABuildsWithNodeInTheSDKImage(t *testing.T) {
	t.Parallel()
	result, root := detectCompiled(t, map[string]string{
		"Web.csproj": `<Project Sdk="Microsoft.NET.Sdk.Web"><PropertyGroup><TargetFramework>net8.0</TargetFramework><SpaRoot>ClientApp\</SpaRoot></PropertyGroup>` +
			`<Target Name="PublishRunWebpack" AfterTargets="ComputeFilesToPublish"><Exec WorkingDirectory="$(SpaRoot)" Command="npm install" /><Exec WorkingDirectory="$(SpaRoot)" Command="npm run build" /></Target></Project>`,
		"ClientApp/package.json":      `{"name":"client","scripts":{"build":"vite build"},"devDependencies":{"vite":"6.0.0"}}`,
		"ClientApp/package-lock.json": `{"name":"client","lockfileVersion":3,"requires":true,"packages":{"":{"name":"client","devDependencies":{"vite":"6.0.0"}},"node_modules/vite":{"version":"6.0.0"}}}`,
	})
	web := compiledCandidate(result, "", "dotnet")
	if web == nil || web.DotnetBuild.SPARoot != "ClientApp" || result.SelectedID != web.ID {
		t.Fatalf("candidates = %+v", result.Candidates)
	}
	if client := compiledCandidate(result, "ClientApp", "node"); client == nil || !strings.Contains(client.Demotion, "Web.csproj") {
		t.Fatalf("client = %+v", client)
	}
	prepared := prepareCompiled(t, root, "", BuildPlanConfig{Method: BuildRecipe, Recipe: "dotnet"})
	assertCompiledDockerfile(t, prepared.DockerfilePreview, "FROM node:22-bookworm-slim@sha256:", " AS spa-node", "COPY --from=spa-node /usr/local/ /usr/local/",
		"WORKDIR /src/ClientApp\nRUN npm ci", "WORKDIR /src\nRUN dotnet restore Web.csproj")
}

func TestAspireAppHostIsSetAsideAndItsWiringNamed(t *testing.T) {
	t.Parallel()
	result, _ := detectCompiled(t, map[string]string{
		"Shop.AppHost/Shop.AppHost.csproj": `<Project Sdk="Microsoft.NET.Sdk"><Sdk Name="Aspire.AppHost.Sdk" Version="9.2.0" /><PropertyGroup><OutputType>Exe</OutputType><TargetFramework>net9.0</TargetFramework><IsAspireHost>true</IsAspireHost></PropertyGroup>` +
			`<ItemGroup><ProjectReference Include="..\Shop.ApiService\Shop.ApiService.csproj" /><ProjectReference Include="..\Shop.Web\Shop.Web.csproj" /></ItemGroup></Project>`,
		"Shop.AppHost/Program.cs": "var builder = DistributedApplication.CreateBuilder(args);\n\nvar cache = builder.AddRedis(\"cache\");\n\n" +
			"var apiService = builder.AddProject<Projects.Shop_ApiService>(\"apiservice\");\n\n" +
			"builder.AddProject<Projects.Shop_Web>(\"webfrontend\")\n    .WithExternalHttpEndpoints()\n    .WithReference(cache)\n    .WithReference(apiService);\n\nbuilder.Build().Run();\n",
		"Shop.ApiService/Shop.ApiService.csproj":           `<Project Sdk="Microsoft.NET.Sdk.Web"><PropertyGroup><TargetFramework>net9.0</TargetFramework></PropertyGroup><ItemGroup><ProjectReference Include="..\Shop.ServiceDefaults\Shop.ServiceDefaults.csproj" /></ItemGroup></Project>`,
		"Shop.Web/Shop.Web.csproj":                         `<Project Sdk="Microsoft.NET.Sdk.Web"><PropertyGroup><TargetFramework>net9.0</TargetFramework></PropertyGroup><ItemGroup><ProjectReference Include="..\Shop.ServiceDefaults\Shop.ServiceDefaults.csproj" /></ItemGroup></Project>`,
		"Shop.ServiceDefaults/Shop.ServiceDefaults.csproj": `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net9.0</TargetFramework><IsAspireSharedProject>true</IsAspireSharedProject></PropertyGroup></Project>`,
	})
	roots := []string{}
	for _, candidate := range result.Candidates {
		roots = append(roots, candidate.Root)
	}
	slices.Sort(roots)
	if !slices.Equal(roots, []string{"Shop.ApiService", "Shop.Web"}) {
		t.Fatalf("roots = %v", roots)
	}
	web := compiledCandidate(result, "Shop.Web", "dotnet")
	if !slices.Equal(web.DotnetBuild.Aspire, []string{"ConnectionStrings__cache", "services__apiservice__http__0"}) || web.DotnetBuild.AppHost != "Shop.AppHost/Shop.AppHost.csproj" {
		t.Fatalf("web = %+v", web.DotnetBuild)
	}
	findings := compiledRecipeFindings(web, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe, Recipe: "dotnet"}})
	if item := findingByCode(findings, "dotnet_aspire_orchestration"); item == nil || !strings.Contains(item.Measured, "ConnectionStrings__cache") {
		t.Fatalf("findings = %+v", findings)
	}
}
