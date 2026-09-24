package deploy

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// What the recipes write so a server behind the proxy listens where the
// proxy reaches it and believes the headers the proxy sends, and what the
// runtime writes instead when the container is published to everyone.

func TestRecipesBindEveryInterfaceAndTrustTheProxy(t *testing.T) {
	t.Parallel()
	web := `<Project Sdk="Microsoft.NET.Sdk.Web"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>`
	for _, test := range []struct {
		name   string
		files  map[string]string
		config BuildPlanConfig
		want   []string
		absent []string
		once   []string
	}{
		{
			name:   "every node server binds HOST",
			files:  map[string]string{"package.json": `{"scripts":{"start":"node server.js"},"dependencies":{"express":"4"}}`, "package-lock.json": "{}"},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "node", StartCommand: "npm run start"},
			want:   []string{"ENV NODE_ENV=production\nENV HOST=0.0.0.0\nCOPY --from=build /app /app"},
			absent: []string{"PROTOCOL_HEADER"},
		},
		{
			name:   "astro keeps one HOST",
			files:  map[string]string{"package.json": `{"scripts":{"build":"astro build"},"dependencies":{"astro":"5","@astrojs/node":"9"}}`, "package-lock.json": "{}"},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "node", BuildCommand: "npm run build", StartCommand: "node ./dist/server/entry.mjs"},
			once:   []string{"ENV HOST=0.0.0.0"},
		},
		{
			name:   "sveltekit trusts the forwarded headers",
			files:  map[string]string{"package.json": `{"scripts":{"build":"vite build"},"devDependencies":{"@sveltejs/kit":"2","@sveltejs/adapter-node":"5"}}`, "package-lock.json": "{}"},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "node", BuildCommand: "npm run build", StartCommand: "node build"},
			want: []string{"ENV HOST=0.0.0.0", "ENV PROTOCOL_HEADER=x-forwarded-proto", "ENV HOST_HEADER=x-forwarded-host",
				"ENV ADDRESS_HEADER=x-forwarded-for", "ENV XFF_DEPTH=1"},
		},
		{
			name:   "static output gets no server environment",
			files:  map[string]string{"package.json": `{"scripts":{"build":"vite build"},"devDependencies":{"vite":"6"}}`, "package-lock.json": "{}"},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "node", BuildCommand: "npm run build", OutputDirectory: "dist"},
			absent: []string{"HOST=0.0.0.0"},
		},
		{
			name:   "python trusts the proxy and binds uvicorn and flask run",
			files:  map[string]string{"requirements.txt": "fastapi\nuvicorn\n", "main.py": "app = FastAPI()\n"},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "python", StartCommand: "uvicorn main:app --host 0.0.0.0 --port ${PORT:-8000}"},
			want: []string{"ENV FORWARDED_ALLOW_IPS=* UVICORN_HOST=0.0.0.0 FLASK_RUN_HOST=0.0.0.0",
				`CMD ["/bin/sh","-c","uvicorn main:app --host 0.0.0.0 --port ${PORT:-8000}"]`},
		},
		{
			name:   "spring boot is bridged to PORT and trusts the proxy",
			files:  map[string]string{"pom.xml": "<project><parent><artifactId>spring-boot-starter-parent</artifactId></parent></project>"},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "java"},
			want: []string{"ENV SERVER_FORWARD_HEADERS_STRATEGY=framework",
				`CMD ["/bin/sh","-c","exec env SERVER_PORT=${PORT:-8080} java -jar /app/app.jar"]`},
		},
		{
			name:   "quarkus",
			files:  map[string]string{"pom.xml": "<project><artifactId>quarkus-bom</artifactId></project>"},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "java"},
			want: []string{"ENV QUARKUS_HTTP_PROXY_PROXY_ADDRESS_FORWARDING=true QUARKUS_HTTP_PROXY_ALLOW_X_FORWARDED=true",
				"exec env QUARKUS_HTTP_PORT=${PORT:-8080} java -jar /app/app.jar"},
		},
		{
			name:   "micronaut",
			files:  map[string]string{"build.gradle": "plugins { id 'io.micronaut.application' }"},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "java"},
			want:   []string{"exec env MICRONAUT_SERVER_PORT=${PORT:-8080} java -jar /app/app.jar"},
			absent: []string{"FORWARD"},
		},
		{
			name:   "javalin keeps its own port",
			files:  map[string]string{"pom.xml": "<project><groupId>io.javalin</groupId></project>"},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "java"},
			want:   []string{`CMD ["java","-jar","/app/app.jar"]`},
		},
		{
			name:   "a custom java start command is the operator's",
			files:  map[string]string{"pom.xml": "<project><parent><artifactId>spring-boot-starter-parent</artifactId></parent></project>"},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "java", StartCommand: "java -Xmx256m -jar /app/app.jar"},
			want:   []string{`CMD ["/bin/sh","-c","java -Xmx256m -jar /app/app.jar"]`, "ENV SERVER_FORWARD_HEADERS_STRATEGY=framework"},
			absent: []string{"SERVER_PORT"},
		},
		{
			name:   "asp.net trusts forwarded headers",
			files:  map[string]string{"api.csproj": web},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "dotnet"},
			want: []string{"ENV ASPNETCORE_FORWARDEDHEADERS_ENABLED=true",
				`CMD ["/bin/sh","-c","ASPNETCORE_HTTP_PORTS=${PORT:-8080} dotnet /app/api.dll"]`},
		},
		{
			name:   "one kestrel endpoint is bridged",
			files:  map[string]string{"api.csproj": web, "appsettings.json": `{"Kestrel":{"Endpoints":{"Http":{"Url":"http://localhost:5000"}}}}`},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "dotnet"},
			want:   []string{`ASPNETCORE_HTTP_PORTS=${PORT:-8080} Kestrel__Endpoints__Http__Url=http://+:${PORT:-8080} dotnet /app/api.dll`},
		},
		{
			name:   "urls are bridged",
			files:  map[string]string{"api.csproj": web, "appsettings.Production.json": `{"Urls":"http://localhost:5000"}`},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "dotnet"},
			want:   []string{`ASPNETCORE_HTTP_PORTS=${PORT:-8080} URLS=http://+:${PORT:-8080} dotnet /app/api.dll`},
		},
		{
			name:   "a console program is not given web settings",
			files:  map[string]string{"tool.csproj": `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><OutputType>Exe</OutputType><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>`},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "dotnet"},
			absent: []string{"FORWARDEDHEADERS"},
		},
		{
			name:   "rocket is bound to every interface on PORT",
			files:  map[string]string{"Cargo.toml": "[package]\nname = \"svc\"\n[dependencies]\nrocket = \"0.5\"\n"},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "rust"},
			want:   []string{"ENV ROCKET_ADDRESS=0.0.0.0", `CMD ["/bin/sh","-c","exec env ROCKET_PORT=${PORT:-8000} /app"]`},
			absent: []string{"ENTRYPOINT"},
		},
		{
			name:   "rocket keeps its address under a custom start",
			files:  map[string]string{"Cargo.toml": "[package]\nname = \"svc\"\n[dependencies]\nrocket = \"0.5\"\n"},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "rust", StartCommand: "/app --verbose"},
			want:   []string{"ENV ROCKET_ADDRESS=0.0.0.0", `CMD ["/bin/sh","-c","/app --verbose"]`},
			absent: []string{"ROCKET_PORT"},
		},
		{
			name:   "axum keeps its entrypoint",
			files:  map[string]string{"Cargo.toml": "[package]\nname = \"svc\"\n[dependencies]\naxum = \"0.8\"\n"},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "rust"},
			want:   []string{`ENTRYPOINT ["/app"]`},
			absent: []string{"ROCKET"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			for path, content := range test.files {
				writeBuildFixture(t, root, path, content)
			}
			prepared, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), root, test.config, false, "just-dashboard/test:run-1")
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range test.want {
				if !strings.Contains(prepared.DockerfilePreview, want) {
					t.Fatalf("Dockerfile missing %q:\n%s", want, prepared.DockerfilePreview)
				}
			}
			for _, absent := range test.absent {
				if strings.Contains(prepared.DockerfilePreview, absent) {
					t.Fatalf("Dockerfile carries %q:\n%s", absent, prepared.DockerfilePreview)
				}
			}
			for _, once := range test.once {
				if strings.Count(prepared.DockerfilePreview, once) != 1 {
					t.Fatalf("Dockerfile carries %q other than once:\n%s", once, prepared.DockerfilePreview)
				}
			}
		})
	}
}

func TestKestrelBridgeMovesOneEndpointAndNamesSeveral(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		settings  kestrelSettings
		bridge    string
		unbridged string
	}{
		{kestrelSettings{}, "", ""},
		{kestrelSettings{urls: "http://localhost:5000;https://localhost:5001"}, "URLS=http://+:${PORT:-8080}", ""},
		{kestrelSettings{endpoints: map[string]string{"Web": "http://0.0.0.0:5000"}, urls: "http://*:6000"}, "Kestrel__Endpoints__Web__Url=http://+:${PORT:-8080}", ""},
		{kestrelSettings{endpoints: map[string]string{"Secure": "https://0.0.0.0:5001"}}, "", "Secure"},
		{kestrelSettings{endpoints: map[string]string{"A__B": "http://0.0.0.0:5001"}}, "", "A__B"},
		{kestrelSettings{endpoints: map[string]string{"Http": "http://0.0.0.0:5000", "Https": "https://0.0.0.0:5001"}}, "", "Http, Https"},
	} {
		bridge, unbridged := kestrelBridge(test.settings)
		if strings.Join(bridge, " ") != test.bridge || (test.unbridged == "") != (unbridged == "") || !strings.Contains(unbridged, test.unbridged) {
			t.Fatalf("%+v: bridge %v, unbridged %q", test.settings, bridge, unbridged)
		}
	}
}

func TestRuntimeWithdrawsProxyTrustFromAPublicContainer(t *testing.T) {
	t.Parallel()
	valueOf := func(environment []dockerx.EnvVar, name string) (string, bool) {
		for _, variable := range environment {
			if variable.Name == name {
				return variable.Value, true
			}
		}
		return "", false
	}
	environment, names := containerRuntimeEnvironment(RuntimePlanConfig{InternalPort: 8000, BindAddress: "0.0.0.0"}, map[string]string{"ADDRESS_HEADER": "x-real-ip"})
	for name, want := range map[string]string{
		"FORWARDED_ALLOW_IPS": "127.0.0.1", "ASPNETCORE_FORWARDEDHEADERS_ENABLED": "false", "SERVER_FORWARD_HEADERS_STRATEGY": "none",
		"QUARKUS_HTTP_PROXY_PROXY_ADDRESS_FORWARDING": "false", "PROTOCOL_HEADER": "", "HOST_HEADER": "", "ADDRESS_HEADER": "x-real-ip",
	} {
		if got, ok := valueOf(environment, name); !ok || got != want {
			t.Fatalf("%s = %q (%v), want %q: %+v", name, got, ok, want, environment)
		}
	}
	if _, ok := valueOf(environment, "XFF_DEPTH"); ok {
		t.Fatalf("a setting whose value does not change was written: %+v", environment)
	}
	if len(names) != 1 || names[0] != "ADDRESS_HEADER" {
		t.Fatalf("withdrawn settings reported as plan variables: %v", names)
	}
	for _, plan := range []RuntimePlanConfig{
		{InternalPort: 8000, BindAddress: "127.0.0.1"},
		{InternalPort: 8000},
		{InternalPort: 8000, BindAddress: "0.0.0.0", HostNetwork: true},
	} {
		environment, _ := containerRuntimeEnvironment(plan, nil)
		if _, ok := valueOf(environment, "FORWARDED_ALLOW_IPS"); ok {
			t.Fatalf("%+v withdrew trust: %+v", plan, environment)
		}
	}
}

func TestNodeServerRuntimeEnvironmentMerges(t *testing.T) {
	t.Parallel()
	if got := strings.Join(nodeServerRuntimeEnv("astro", []string{"HOST=0.0.0.0"}), " "); got != "HOST=0.0.0.0" {
		t.Fatalf("astro = %q", got)
	}
	if got := strings.Join(nodeServerRuntimeEnv("remix", []string{"HOST=::"}), " "); got != "HOST=::" {
		t.Fatalf("a framework's own HOST did not win: %q", got)
	}
	if got := strings.Join(nodeServerRuntimeEnv("sveltekit", nil), " "); got != "HOST=0.0.0.0 PROTOCOL_HEADER=x-forwarded-proto HOST_HEADER=x-forwarded-host ADDRESS_HEADER=x-forwarded-for XFF_DEPTH=1" {
		t.Fatalf("sveltekit = %q", got)
	}
}

type recordingRouteProxy struct {
	countingActivationProxy
	routes []proxysvc.DeploymentRoute
}

func (p *recordingRouteProxy) ApplyDeploymentRoute(ctx context.Context, route proxysvc.DeploymentRoute) (proxysvc.DeploymentRouteResult, error) {
	p.routes = append(p.routes, route)
	return p.countingActivationProxy.ApplyDeploymentRoute(ctx, route)
}

func TestActivationCarriesThePlansRequestBodyLimitToTheRoute(t *testing.T) {
	fixture := newReleaseStoreFixture(t)
	plan := RuntimePlanConfig{
		InternalPort: 3000, BindAddress: "127.0.0.1", Strategy: StrategyBlueGreen, MaxRequestBodyMB: 256,
		Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{},
	}
	fixture.addPlanWithRuntime(t, 1, strings.Repeat("a", 40), plan)
	run, lease := fixture.claimedRun(t, 1)
	release := createRuntimeCandidateWithDomains(t, fixture, *run, lease, plan, "body-candidate", nil,
		[]PlannedDomain{{Hostname: "app.example.test", Ownership: OwnershipManaged}})
	if _, err := fixture.runs.RecordCandidateRuntime(context.Background(), *run, lease.Token, ReleaseRuntimeInput{
		ReleaseID: release.Release.ID, Kind: "container", RuntimeID: "body-candidate",
		Host: "127.0.0.1", Port: 32124, Metadata: json.RawMessage(`{"version":1}`),
	}); err != nil {
		t.Fatal(err)
	}
	owner := &orderingRuntimeOwner{running: map[string]bool{"body-candidate": true}, allowConcurrent: true}
	proxy := &recordingRouteProxy{}
	executor := &NormalizedStepExecutor{store: fixture.runs, variables: fixture.variables, runtime: owner, proxy: proxy}
	result := executor.activate(context.Background(), StepExecution{Run: *run, ClaimToken: lease.Token, Output: discardStepOutput{}},
		mustExecutionPlan(t, fixture, *run))
	if result.State != StepPassed || len(proxy.routes) != 1 || proxy.routes[0].MaxBodyMB != 256 {
		t.Fatalf("activation = %#v, routes = %+v", result, proxy.routes)
	}
}
