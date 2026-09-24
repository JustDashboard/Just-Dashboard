package deploy

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

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
		// trust is what the runtime may later withdraw from the image.
		trust string
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
			trust: "ADDRESS_HEADER HOST_HEADER PROTOCOL_HEADER XFF_DEPTH",
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
			trust: "FORWARDED_ALLOW_IPS",
		},
		{
			name:   "spring boot is bridged to PORT and trusts the proxy",
			files:  map[string]string{"pom.xml": "<project><parent><artifactId>spring-boot-starter-parent</artifactId></parent></project>"},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "java"},
			want: []string{"ENV SERVER_FORWARD_HEADERS_STRATEGY=framework",
				`CMD ["/bin/sh","-c","exec env SERVER_PORT=${PORT:-8080} java -jar /app/app.jar"]`},
			trust: "SERVER_FORWARD_HEADERS_STRATEGY",
		},
		{
			name:   "quarkus",
			files:  map[string]string{"pom.xml": "<project><artifactId>quarkus-bom</artifactId></project>"},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "java"},
			want: []string{"ENV QUARKUS_HTTP_PROXY_PROXY_ADDRESS_FORWARDING=true QUARKUS_HTTP_PROXY_ALLOW_X_FORWARDED=true",
				"exec env QUARKUS_HTTP_PORT=${PORT:-8080} java -jar /app/app.jar"},
			trust: "QUARKUS_HTTP_PROXY_ALLOW_X_FORWARDED QUARKUS_HTTP_PROXY_PROXY_ADDRESS_FORWARDING",
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
			trust:  "SERVER_FORWARD_HEADERS_STRATEGY",
		},
		{
			name:   "asp.net trusts forwarded headers",
			files:  map[string]string{"api.csproj": web},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "dotnet"},
			want: []string{"ENV ASPNETCORE_FORWARDEDHEADERS_ENABLED=true",
				`CMD ["/bin/sh","-c","ASPNETCORE_HTTP_PORTS=${PORT:-8080} dotnet /app/api.dll"]`},
			trust: "ASPNETCORE_FORWARDEDHEADERS_ENABLED",
		},
		{
			name:   "one kestrel endpoint is bridged",
			files:  map[string]string{"api.csproj": web, "appsettings.json": `{"Kestrel":{"Endpoints":{"Http":{"Url":"http://localhost:5000"}}}}`},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "dotnet"},
			want:   []string{`ASPNETCORE_HTTP_PORTS=${PORT:-8080} Kestrel__Endpoints__Http__Url=http://+:${PORT:-8080} dotnet /app/api.dll`},
			trust:  "ASPNETCORE_FORWARDEDHEADERS_ENABLED",
		},
		{
			name:   "urls are bridged",
			files:  map[string]string{"api.csproj": web, "appsettings.Production.json": `{"Urls":"http://localhost:5000"}`},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "dotnet"},
			want:   []string{`ASPNETCORE_HTTP_PORTS=${PORT:-8080} URLS=http://+:${PORT:-8080} dotnet /app/api.dll`},
			trust:  "ASPNETCORE_FORWARDEDHEADERS_ENABLED",
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
			if got := strings.Join(imageProxyTrust(prepared), " "); got != test.trust {
				t.Fatalf("image trust = %q, want %q", got, test.trust)
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

func TestRuntimeWithdrawsTheImagesProxyTrustUnlessTheProxyAloneFrontsIt(t *testing.T) {
	t.Parallel()
	routed := []PlannedDomain{{Hostname: "app.example.test", HTTPS: true, Ownership: OwnershipManaged}}
	svelte := []string{"ADDRESS_HEADER", "HOST_HEADER", "PROTOCOL_HEADER", "XFF_DEPTH"}
	loopback := RuntimePlanConfig{InternalPort: 3000, BindAddress: "127.0.0.1"}
	for _, test := range []struct {
		name      string
		snapshot  runtimeReleaseSnapshot
		variables map[string]string
		want      string
	}{
		{name: "routed on loopback keeps the trust", snapshot: runtimeReleaseSnapshot{Plan: loopback, Domains: routed, ProxyTrust: svelte}},
		{
			// adapter-node before 5.5 has no fallback from HOST_HEADER to Host:
			// an empty value makes every origin https://undefined.
			name:     "a public bind withdraws what the image set, HOST_HEADER to host",
			snapshot: runtimeReleaseSnapshot{Plan: RuntimePlanConfig{InternalPort: 3000, BindAddress: "0.0.0.0"}, Domains: routed, ProxyTrust: svelte},
			want:     "ADDRESS_HEADER= HOST_HEADER=host PROTOCOL_HEADER=",
		},
		{
			name:      "a variable the plan sets wins",
			snapshot:  runtimeReleaseSnapshot{Plan: RuntimePlanConfig{InternalPort: 3000, BindAddress: "::"}, Domains: routed, ProxyTrust: svelte},
			variables: map[string]string{"ADDRESS_HEADER": "x-real-ip"},
			want:      "HOST_HEADER=host PROTOCOL_HEADER=",
		},
		{
			name:     "no route means no proxy adds the headers",
			snapshot: runtimeReleaseSnapshot{Plan: loopback, ProxyTrust: []string{"FORWARDED_ALLOW_IPS"}},
			want:     "FORWARDED_ALLOW_IPS=127.0.0.1",
		},
		{
			name:     "host networking is public",
			snapshot: runtimeReleaseSnapshot{Plan: RuntimePlanConfig{InternalPort: 8080, HostNetwork: true}, Domains: routed, ProxyTrust: []string{"ASPNETCORE_FORWARDEDHEADERS_ENABLED"}},
			want:     "ASPNETCORE_FORWARDEDHEADERS_ENABLED=false",
		},
		{
			name: "the application's port published again on every interface is public",
			snapshot: runtimeReleaseSnapshot{Plan: RuntimePlanConfig{InternalPort: 8080, BindAddress: "127.0.0.1",
				Ports: []PublishedPort{{HostPort: 8080, ContainerPort: 8080}}}, Domains: routed, ProxyTrust: []string{"SERVER_FORWARD_HEADERS_STRATEGY"}},
			want: "SERVER_FORWARD_HEADERS_STRATEGY=none",
		},
		{
			name: "another published port is not",
			snapshot: runtimeReleaseSnapshot{Plan: RuntimePlanConfig{InternalPort: 8080, BindAddress: "127.0.0.1",
				Ports: []PublishedPort{{HostPort: 2222, ContainerPort: 22}}}, Domains: routed, ProxyTrust: []string{"SERVER_FORWARD_HEADERS_STRATEGY"}},
		},
		{
			// A repository's own Dockerfile, a pulled image or an adopted
			// container records no trust, so nothing is written over what it
			// bakes in.
			name:     "a Dockerfile or image build is never touched",
			snapshot: runtimeReleaseSnapshot{Plan: RuntimePlanConfig{InternalPort: 3000, BindAddress: "0.0.0.0"}, Domains: routed},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var got []string
			for _, variable := range withdrawnProxyTrust(test.snapshot, test.variables) {
				got = append(got, variable.Name+"="+variable.Value)
			}
			if strings.Join(got, " ") != test.want {
				t.Fatalf("withdrawn = %q, want %q", strings.Join(got, " "), test.want)
			}
		})
	}
	// Withdrawal is the runtime's own environment, never a plan variable.
	environment, names := containerRuntimeEnvironment(RuntimePlanConfig{InternalPort: 8000, BindAddress: "0.0.0.0"}, map[string]string{"APP": "x"})
	if len(environment) != 2 || len(names) != 1 {
		t.Fatalf("environment = %+v, names = %v", environment, names)
	}
	if imageProxyTrust(PreparedBuild{Method: BuildDockerfile, DockerfilePreview: "FROM x\nENV FORWARDED_ALLOW_IPS=*\n"}) != nil {
		t.Fatal("a repository's own Dockerfile was read for trust to withdraw")
	}
	if got := imageProxyTrust(PreparedBuild{Method: BuildRecipe, DockerfilePreview: "FROM a AS build\nENV FORWARDED_ALLOW_IPS=*\nFROM b\nENV HOST=0.0.0.0\n"}); len(got) != 0 {
		t.Fatalf("a build stage's environment counted as the image's: %v", got)
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
