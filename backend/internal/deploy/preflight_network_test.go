package deploy

import (
	"strings"
	"testing"
)

// Preflight re-checks detection's listen facts against the plan as it
// stands, so a loopback bind or a port the server fixes is named before
// Deploy rather than as a readiness timeout after it.

func networkDraft(candidate DetectedCandidate, profile WorkloadProfile) *Draft {
	candidate = newDetectedCandidate(candidate.Root, candidate.BuildMethod, candidate)
	return &Draft{Data: DraftData{
		Intent: &DraftIntentConfig{Name: "app", Profile: profile},
		Source: &DraftSourceConfig{Kind: SourceGit},
		Detection: &DetectionResult{
			Source: SourceIdentity{Kind: SourceGit, Revision: strings.Repeat("a", 40)}, Candidates: []DetectedCandidate{candidate}, SelectedID: candidate.ID,
		},
	}}
}

func networkConfiguration(recipe, start string, port int) PlanConfiguration {
	return PlanConfiguration{
		Build:   BuildPlanConfig{Method: BuildRecipe, Recipe: recipe, StartCommand: start},
		Runtime: RuntimePlanConfig{InternalPort: port, BindAddress: "127.0.0.1", Strategy: StrategyBlueGreen},
	}
}

func TestPreflightNamesLoopbackBindsBeforeDeploy(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name          string
		candidate     DetectedCandidate
		configuration PlanConfiguration
		environment   map[string]string
		code          string
		severity      PreflightSeverity
		field         string
	}{
		{
			name: "certain loopback in code blocks",
			candidate: DetectedCandidate{Name: "api", BuildMethod: BuildRecipe, Recipe: "node", StartCommand: "npm run start",
				Listen: &DetectedListen{Port: 3000, PortFrom: "server.js:1", Loopback: "127.0.0.1", LoopbackFrom: "server.js:1 listen(3000, '127.0.0.1')", LoopbackCertain: true}},
			configuration: networkConfiguration("node", "npm run start", 3000),
			code:          "listen_loopback", severity: PreflightBlocked, field: "configuration.build",
		},
		{
			name: "framework default loopback warns",
			candidate: DetectedCandidate{Name: "api", BuildMethod: BuildRecipe, Recipe: "node", StartCommand: "npm run start",
				Listen: &DetectedListen{Loopback: "localhost", LoopbackFrom: "app.js:3 listen({ port: 3000 })"}},
			configuration: networkConfiguration("node", "npm run start", 3000),
			code:          "listen_loopback", severity: PreflightWarning, field: "configuration.build",
		},
		{
			name: "a recipe fix is a pass",
			candidate: DetectedCandidate{Name: "api", BuildMethod: BuildRecipe, Recipe: "rust",
				Listen: &DetectedListen{Loopback: "127.0.0.1", LoopbackFrom: "Rocket's default address", LoopbackRecipeFix: "ROCKET_ADDRESS=0.0.0.0"}},
			configuration: networkConfiguration("rust", "", 8000),
			code:          "listen_loopback_moved", severity: PreflightPass,
		},
		{
			name: "the recipe fix does not reach a Dockerfile",
			candidate: DetectedCandidate{Name: "api", BuildMethod: BuildRecipe, Recipe: "rust",
				Listen: &DetectedListen{Loopback: "127.0.0.1", LoopbackFrom: "Rocket's default address", LoopbackRecipeFix: "ROCKET_ADDRESS=0.0.0.0"}},
			configuration: PlanConfiguration{Build: BuildPlanConfig{Method: BuildDockerfile}, Runtime: RuntimePlanConfig{InternalPort: 8000}},
			code:          "listen_loopback", severity: PreflightWarning,
		},
		{
			name: "HOST set in the plan is a pass",
			candidate: DetectedCandidate{Name: "api", BuildMethod: BuildDockerfile,
				Listen: &DetectedListen{Loopback: "localhost", LoopbackFrom: "server.js:1", LoopbackVariable: "HOST"}},
			configuration: func() PlanConfiguration {
				configuration := PlanConfiguration{Build: BuildPlanConfig{Method: BuildDockerfile}, Runtime: RuntimePlanConfig{InternalPort: 3000}}
				configuration.Variables = []PlannedVariable{{Name: "HOST", Sensitivity: "plain", Scopes: []string{"runtime"}, Value: "0.0.0.0"}}
				return configuration
			}(),
			code: "listen_loopback_moved", severity: PreflightPass,
		},
		{
			name: "HOST removed from the plan warns on its variable",
			candidate: DetectedCandidate{Name: "api", BuildMethod: BuildDockerfile,
				Listen: &DetectedListen{Loopback: "localhost", LoopbackFrom: "server.js:1", LoopbackVariable: "HOST"}},
			configuration: PlanConfiguration{Build: BuildPlanConfig{Method: BuildDockerfile}, Runtime: RuntimePlanConfig{InternalPort: 3000}},
			code:          "listen_loopback", severity: PreflightWarning, field: "variables.HOST",
		},
		{
			name: "HOST typed into the environment is a pass",
			candidate: DetectedCandidate{Name: "api", BuildMethod: BuildDockerfile,
				Listen: &DetectedListen{Loopback: "localhost", LoopbackFrom: "server.js:1", LoopbackVariable: "HOST"}},
			configuration: PlanConfiguration{Build: BuildPlanConfig{Method: BuildDockerfile}, Runtime: RuntimePlanConfig{InternalPort: 3000}},
			environment:   map[string]string{"HOST": "0.0.0.0"},
			code:          "listen_loopback_moved", severity: PreflightPass,
		},
		{
			name:          "a replaced start command is read on its own",
			candidate:     DetectedCandidate{Name: "api", BuildMethod: BuildRecipe, Recipe: "python", StartCommand: "uvicorn main:app --host 0.0.0.0 --port ${PORT:-8000}"},
			configuration: networkConfiguration("python", "hypercorn main:app", 8000),
			code:          "listen_loopback", severity: PreflightBlocked, field: "configuration.build.startCommand",
		},
		{
			name:          "a replaced uvicorn without a host is moved by the recipe",
			candidate:     DetectedCandidate{Name: "api", BuildMethod: BuildRecipe, Recipe: "python", StartCommand: "uvicorn main:app --host 0.0.0.0 --port ${PORT:-8000}"},
			configuration: networkConfiguration("python", "uvicorn main:app --port ${PORT:-8000}", 8000),
			code:          "listen_loopback_moved", severity: PreflightPass,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			draft := networkDraft(test.candidate, ProfileWeb)
			draft.environment = test.environment
			found := findingByCode(networkFindings(draft, test.configuration), test.code)
			if found == nil || found.Severity != test.severity || (test.field != "" && found.FieldID != test.field) {
				t.Fatalf("finding = %+v in %+v", found, networkFindings(draft, test.configuration))
			}
		})
	}
	// A worker answers nothing, so where it would listen is not a question.
	draft := networkDraft(DetectedCandidate{Name: "bot", BuildMethod: BuildRecipe, Recipe: "node",
		Listen: &DetectedListen{Loopback: "127.0.0.1", LoopbackCertain: true}}, ProfileWorker)
	if found := findingByCode(networkFindings(draft, networkConfiguration("node", "", 0)), "listen_loopback"); found != nil {
		t.Fatalf("worker got %+v", found)
	}
}

func TestPreflightComparesFixedPortsWithThePlan(t *testing.T) {
	t.Parallel()
	fixed := DetectedCandidate{Name: "api", BuildMethod: BuildRecipe, Recipe: "node", StartCommand: "npm run start",
		Listen: &DetectedListen{Port: 4000, PortFrom: "server.js:2 listen(4000)"}}
	draft := networkDraft(fixed, ProfileWeb)
	if found := findingByCode(networkFindings(draft, networkConfiguration("node", "npm run start", 3000)), "port_hardcoded"); found == nil ||
		found.Severity != PreflightWarning || found.FieldID != "runtime.internalPort" || !strings.Contains(found.Title, "4000") {
		t.Fatalf("port_hardcoded = %+v", found)
	}
	if found := findingByCode(networkFindings(draft, networkConfiguration("node", "npm run start", 4000)), "port_from_source"); found == nil || found.Severity != PreflightPass {
		t.Fatalf("port_from_source = %+v", found)
	}
	follows := DetectedCandidate{Name: "api", BuildMethod: BuildRecipe, Recipe: "go", Listen: &DetectedListen{ReadsPort: true, ReadsPortFrom: "main.go:5"}}
	if found := findingByCode(networkFindings(networkDraft(follows, ProfileWeb), networkConfiguration("go", "", 9999)), "port_from_source"); found == nil ||
		!strings.Contains(found.Means, "PORT=9999") {
		t.Fatalf("follows PORT = %+v", found)
	}
	replaced := networkConfiguration("python", "uvicorn main:app --host 0.0.0.0 --port 5000", 8000)
	python := DetectedCandidate{Name: "api", BuildMethod: BuildRecipe, Recipe: "python", StartCommand: "uvicorn main:app --host 0.0.0.0 --port ${PORT:-8000}",
		Listen: &DetectedListen{ReadsPort: true}}
	if found := findingByCode(networkFindings(networkDraft(python, ProfileWeb), replaced), "port_hardcoded"); found == nil || !strings.Contains(found.Measured, "--port 5000") {
		t.Fatalf("replaced command port = %+v", found)
	}
	endpoints := DetectedCandidate{Name: "api", BuildMethod: BuildRecipe, Recipe: "dotnet", Listen: &DetectedListen{Unbridged: "appsettings.json declares Http, Https"}}
	if found := findingByCode(networkFindings(networkDraft(endpoints, ProfileWeb), networkConfiguration("dotnet", "", 8080)), "listen_endpoints_unbridged"); found == nil {
		t.Fatal("several Kestrel endpoints went unmentioned")
	}
}

func TestPreflightWarnsAboutPythonDevelopmentServers(t *testing.T) {
	t.Parallel()
	candidate := DetectedCandidate{Name: "api", BuildMethod: BuildRecipe, Recipe: "python", StartCommand: "gunicorn app:app"}
	for command, dev := range map[string]bool{
		"python manage.py runserver 0.0.0.0:8000": true, "flask run --host 0.0.0.0": true, "uvicorn main:app --reload": true,
		"fastapi dev main.py": true, "fastapi run main.py": false, "gunicorn app:app": false,
	} {
		found := findingByCode(networkFindings(networkDraft(candidate, ProfileWeb), networkConfiguration("python", command, 8000)), "start_command_dev_server")
		if (found != nil) != dev {
			t.Fatalf("%q: %+v", command, found)
		}
	}
}

func TestPreflightReportsProxyTrustAndTheBodyLimit(t *testing.T) {
	t.Parallel()
	domains := []PlannedDomain{{Hostname: "app.example.test", HTTPS: true, Ownership: OwnershipManaged}}
	python := DetectedCandidate{Name: "api", BuildMethod: BuildRecipe, Recipe: "python", Framework: "django", StartCommand: "gunicorn x"}
	configuration := networkConfiguration("python", "gunicorn x", 8000)
	configuration.Domains = domains
	findings := networkFindings(networkDraft(python, ProfileWeb), configuration)
	if found := findingByCode(findings, "proxy_headers_trusted"); found == nil || found.Measured != "FORWARDED_ALLOW_IPS=*" {
		t.Fatalf("trusted = %+v", found)
	}
	// Zero means each proxy's own deployment default, which differs: nginx
	// is given 64 MB, Caddy keeps having none.
	if found := findingByCode(findings, "request_body_limit"); found == nil || found.Measured != "default (64 MB on nginx, no limit on Caddy)" ||
		found.FieldID != "runtime.maxRequestBodyMb" || !strings.Contains(found.Means, "Caddy proxy sets no limit") {
		t.Fatalf("body limit = %+v", found)
	}
	configuration.Runtime.MaxRequestBodyMB = 512
	if found := findingByCode(networkFindings(networkDraft(python, ProfileWeb), configuration), "request_body_limit"); found == nil ||
		found.Measured != "512 MB" || found.Title != "Uploads up to 512 MB reach the application" {
		t.Fatalf("explicit body limit = %+v", found)
	}
	for _, public := range []struct {
		runtime  func(*RuntimePlanConfig)
		measured string
	}{
		{func(runtime *RuntimePlanConfig) { runtime.BindAddress = "0.0.0.0" }, "0.0.0.0"},
		{func(runtime *RuntimePlanConfig) { runtime.HostNetwork = true }, "host network"},
		{func(runtime *RuntimePlanConfig) {
			runtime.Ports = []PublishedPort{{HostPort: 8000, ContainerPort: 8000}}
		}, "port 8000 published on host port 8000 on every interface"},
	} {
		exposed := configuration
		exposed.Runtime.Ports = nil
		public.runtime(&exposed.Runtime)
		findings = networkFindings(networkDraft(python, ProfileWeb), exposed)
		if found := findingByCode(findings, "forwarded_headers_untrusted"); found == nil || found.Severity != PreflightWarning ||
			found.Measured != public.measured || !strings.Contains(found.Means, "FORWARDED_ALLOW_IPS") || findingByCode(findings, "proxy_headers_trusted") != nil {
			t.Fatalf("%s = %+v", public.measured, findings)
		}
	}
	configuration.Runtime.Ports = []PublishedPort{{HostPort: 2222, ContainerPort: 22}}
	if found := findingByCode(networkFindings(networkDraft(python, ProfileWeb), configuration), "proxy_headers_trusted"); found == nil {
		t.Fatal("an unrelated published port gave up the proxy's trust")
	}

	auth := DetectedCandidate{Name: "web", BuildMethod: BuildRecipe, Recipe: "node", Framework: "nextjs", StartCommand: "npm run start",
		NetworkVariables: []DetectedNetworkVariable{{Name: "AUTH_TRUST_HOST", Value: "true", Reason: "next-auth 5"}}}
	configuration = networkConfiguration("node", "npm run start", 3000)
	configuration.Domains = domains
	if found := findingByCode(networkFindings(networkDraft(auth, ProfileWeb), configuration), "proxy_trust_variable_missing"); found == nil || found.FieldID != "variables.AUTH_TRUST_HOST" {
		t.Fatalf("removed variable = %+v", found)
	}
	configuration.Variables = []PlannedVariable{{Name: "AUTH_TRUST_HOST", Sensitivity: "plain", Scopes: []string{"runtime"}, Value: "true"}}
	if found := findingByCode(networkFindings(networkDraft(auth, ProfileWeb), configuration), "proxy_headers_trusted"); found == nil || found.Measured != "AUTH_TRUST_HOST" {
		t.Fatalf("variable trust = %+v", found)
	}
	// The runtime withdraws only what the recipe image set; a variable the
	// plan sets is still trusted, and the warning says so.
	configuration.Runtime.BindAddress = "0.0.0.0"
	if found := findingByCode(networkFindings(networkDraft(auth, ProfileWeb), configuration), "forwarded_headers_untrusted"); found == nil ||
		!strings.Contains(found.Means, "AUTH_TRUST_HOST is still trusted") || !strings.Contains(found.Action, "Remove AUTH_TRUST_HOST") {
		t.Fatalf("public bind with a trusted variable = %+v", found)
	}
	configuration.Runtime.BindAddress = "127.0.0.1"
	configuration.Domains = nil
	if findings := networkFindings(networkDraft(auth, ProfileWeb), configuration); findingByCode(findings, "request_body_limit") != nil || findingByCode(findings, "proxy_headers_trusted") != nil {
		t.Fatalf("a plan with no route got route findings: %+v", findings)
	}
}

func TestRequestBodyLimitIsValidated(t *testing.T) {
	t.Parallel()
	for limit, valid := range map[int]bool{0: true, 1: true, 64: true, MaxRequestBodyMB: true, -1: false, MaxRequestBodyMB + 1: false} {
		configuration := limitPlan(func(runtime *RuntimePlanConfig) { runtime.MaxRequestBodyMB = limit })
		err := configuration.Validate()
		if (err == nil) != valid {
			t.Fatalf("limit %d: %v", limit, err)
		}
		if err != nil && !strings.Contains(err.Error(), "request body limit") {
			t.Fatalf("limit %d: %v", limit, err)
		}
	}
	if (RuntimePlanConfig{}).requestBodyLimitLabel() != "default (64 MB on nginx, no limit on Caddy)" ||
		(RuntimePlanConfig{MaxRequestBodyMB: 10}).requestBodyLimitLabel() != "10 MB" {
		t.Fatal("limit label")
	}
}

func TestPreflightKeepsCodeFactsWhenTheStartCommandChanges(t *testing.T) {
	t.Parallel()
	// Switching the package manager rewrites the start command; the port the
	// code fixes and the loopback it binds have not moved.
	candidate := DetectedCandidate{Name: "api", BuildMethod: BuildRecipe, Recipe: "node", StartCommand: "npm run start",
		Listen: &DetectedListen{Port: 4000, PortFrom: "server.js:2 listen(4000, '127.0.0.1')", Loopback: "127.0.0.1",
			LoopbackFrom: "server.js:2 listen(4000, '127.0.0.1')", LoopbackCertain: true}}
	findings := networkFindings(networkDraft(candidate, ProfileWeb), networkConfiguration("node", "bun run start", 3000))
	if found := findingByCode(findings, "port_hardcoded"); found == nil || !strings.Contains(found.Title, "4000") {
		t.Fatalf("port = %+v", findings)
	}
	if found := findingByCode(findings, "listen_loopback"); found == nil || found.Severity != PreflightBlocked {
		t.Fatalf("loopback = %+v", findings)
	}
	// A Kestrel bridge exists only in the default start command.
	dotnet := DetectedCandidate{Name: "api", BuildMethod: BuildRecipe, Recipe: "dotnet",
		Listen: &DetectedListen{Loopback: "localhost", LoopbackFrom: "appsettings.json http://localhost:5000", LoopbackRecipeFix: "Kestrel__Endpoints__Http__Url=http://+:${PORT:-8080}"}}
	findings = networkFindings(networkDraft(dotnet, ProfileWeb), networkConfiguration("dotnet", "dotnet /app/api.dll", 8080))
	if found := findingByCode(findings, "listen_loopback"); found == nil || found.Severity != PreflightWarning {
		t.Fatalf("custom .NET start = %+v", findings)
	}
	// Rocket's address is image environment, so a custom start keeps it.
	rocket := DetectedCandidate{Name: "api", BuildMethod: BuildRecipe, Recipe: "rust",
		Listen: &DetectedListen{Loopback: "127.0.0.1", LoopbackFrom: "Rocket's default address", LoopbackRecipeFix: "ROCKET_ADDRESS=0.0.0.0"}}
	findings = networkFindings(networkDraft(rocket, ProfileWeb), networkConfiguration("rust", "/app --verbose", 8000))
	if found := findingByCode(findings, "listen_loopback_moved"); found == nil {
		t.Fatalf("custom Rocket start = %+v", findings)
	}
}

func TestPreflightLoopbackIsCertainOnlyForTheCodeThatRuns(t *testing.T) {
	t.Parallel()
	candidate := DetectedCandidate{Name: "api", BuildMethod: BuildRecipe, Recipe: "node", StartCommand: "npm run start",
		Listen: &DetectedListen{Loopback: "127.0.0.1", LoopbackFrom: "server.js:2 listen(4000, '127.0.0.1')", LoopbackCertain: true}}
	for _, test := range []struct {
		name          string
		configuration PlanConfiguration
		severity      PreflightSeverity
	}{
		{name: "the detected command", configuration: networkConfiguration("node", "npm run start", 4000), severity: PreflightBlocked},
		{name: "the same script through another manager", configuration: networkConfiguration("node", "pnpm run start", 4000), severity: PreflightBlocked},
		{name: "another command may run other code", configuration: networkConfiguration("node", "node dist/main.js", 4000), severity: PreflightWarning},
		{
			name:          "a Dockerfile runs its own CMD",
			configuration: PlanConfiguration{Build: BuildPlanConfig{Method: BuildDockerfile}, Runtime: RuntimePlanConfig{InternalPort: 4000}},
			severity:      PreflightWarning,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			found := findingByCode(networkFindings(networkDraft(candidate, ProfileWeb), test.configuration), "listen_loopback")
			if found == nil || found.Severity != test.severity {
				t.Fatalf("listen_loopback = %+v", found)
			}
			if test.severity == PreflightWarning && !strings.Contains(found.Means, "debug or admin endpoint") {
				t.Fatalf("an uncertain loopback reads as certain: %+v", found)
			}
		})
	}
}

func TestPreflightChecksThePublicURLNextAuthBuildsCallbacksFrom(t *testing.T) {
	t.Parallel()
	candidate := DetectedCandidate{Name: "web", BuildMethod: BuildRecipe, Recipe: "node", Framework: "nextjs", StartCommand: "npm run start",
		NetworkVariables: []DetectedNetworkVariable{{Name: "NEXTAUTH_URL", DomainTemplate: "{{scheme}}://{{hostname}}", Reason: "next-auth 4.24.0 in package.json builds its callback URLs from NEXTAUTH_URL"}}}
	domains := []PlannedDomain{{Hostname: "App.Example.test", HTTPS: true, Ownership: OwnershipManaged}}
	variable := func(value string) []PlannedVariable {
		return []PlannedVariable{{Name: "NEXTAUTH_URL", Sensitivity: "plain", Scopes: []string{"runtime"}, Value: value, DomainTemplate: "{{scheme}}://{{hostname}}"}}
	}
	for _, test := range []struct {
		name        string
		domains     []PlannedDomain
		variables   []PlannedVariable
		environment map[string]string
		code        string
		title       string
		action      string
	}{
		{name: "filled from the domain", domains: domains, variables: variable("https://app.example.test")},
		{name: "typed into the environment", environment: map[string]string{"NEXTAUTH_URL": "https://sso.example.test"}},
		{name: "missing with no domain", code: "public_url_variable_missing", title: "NEXTAUTH_URL is not set", action: "Add a domain"},
		{
			name: "missing with a domain", domains: domains, code: "public_url_variable_missing",
			title: "NEXTAUTH_URL is not set", action: "Set NEXTAUTH_URL=https://app.example.test.",
		},
		{name: "empty", variables: []PlannedVariable{{Name: "NEXTAUTH_URL", Sensitivity: "plain", Scopes: []string{"runtime"}, DomainTemplate: "{{scheme}}://{{hostname}}"}},
			code: "public_url_variable_missing", title: "NEXTAUTH_URL is empty"},
		{
			name: "left on a domain the plan no longer routes", domains: domains, variables: variable("https://old.example.test/api/auth"),
			code: "public_url_variable_stale", title: "NEXTAUTH_URL names old.example.test, not the primary domain",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			configuration := networkConfiguration("node", "npm run start", 3000)
			configuration.Domains, configuration.Variables = test.domains, test.variables
			draft := networkDraft(candidate, ProfileWeb)
			draft.environment = test.environment
			findings := networkFindings(draft, configuration)
			for _, code := range []string{"public_url_variable_missing", "public_url_variable_stale"} {
				found := findingByCode(findings, code)
				if (found != nil) != (code == test.code) {
					t.Fatalf("%s = %+v", code, found)
				}
				if found != nil && (found.Title != test.title || found.Severity != PreflightWarning || found.FieldID != "variables.NEXTAUTH_URL" ||
					!strings.Contains(found.Action, test.action)) {
					t.Fatalf("%s = %+v", code, found)
				}
			}
		})
	}
}

// The environment classification and the network facts can both speak for
// one variable — next-auth 4's NEXTAUTH_URL, a HOST the code falls back to
// loopback without — and each such variable is named once.
func TestEnvironmentAndNetworkFindingsNameAVariableOnce(t *testing.T) {
	t.Parallel()
	candidate := DetectedCandidate{Name: "web", BuildMethod: BuildRecipe, Recipe: "node", StartCommand: "node server.js",
		NetworkVariables: []DetectedNetworkVariable{
			{Name: "NEXTAUTH_URL", DomainTemplate: "{{scheme}}://{{hostname}}", Reason: "next-auth 4.24.0 in package.json builds its callback URLs from NEXTAUTH_URL"},
			{Name: "HOST", Value: "0.0.0.0", Reason: "server.js falls back to 127.0.0.1 without HOST"},
		},
		Variables: []DetectedVariable{
			{Name: "NEXTAUTH_URL", Sources: []string{"package.json"}, Setup: "domain", SetupReason: "NextAuth builds its callback URLs from it", DomainTemplate: "{{scheme}}://{{hostname}}"},
			{Name: "HOST", Sources: []string{"server.js"}, Setup: "default", DefaultValue: "0.0.0.0", SetupReason: "read as the address the server listens on"},
		},
		Listen: &DetectedListen{Loopback: "127.0.0.1", LoopbackFrom: "server.js:3", LoopbackCertain: true, LoopbackVariable: "HOST"}}
	count := func(findings []PreflightFinding, prefixes ...string) int {
		total := 0
		for _, item := range findings {
			for _, prefix := range prefixes {
				if strings.HasPrefix(item.Code, prefix) {
					total++
				}
			}
		}
		return total
	}
	for _, environment := range []map[string]string{nil, {"HOST": "127.0.0.1"}} {
		configuration := networkConfiguration("node", "node server.js", 3000)
		draft := networkDraft(candidate, ProfileWeb)
		draft.Data.Configuration = &configuration
		draft.environment = environment
		findings := preflightFindings(draft, configuration, HostObservation{}, true)
		if count(findings, "public_url_variable_", "public_url_unbound_", "public_url_mismatch_") != 1 || findingByCode(findings, "public_url_unbound_nextauth_url") == nil {
			t.Fatalf("NEXTAUTH_URL findings with HOST=%q: %+v", environment["HOST"], findings)
		}
		if count(findings, "listen_loopback", "host_variable_loopback_") != 1 || findingByCode(findings, "listen_loopback") == nil {
			t.Fatalf("HOST findings with HOST=%q: %+v", environment["HOST"], findings)
		}
	}
}
