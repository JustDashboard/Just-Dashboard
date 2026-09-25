package deploy

import (
	"encoding/json"
	"strings"
	"testing"
)

func readinessDraft(profile WorkloadProfile, candidate DetectedCandidate) *Draft {
	candidate.ID = "candidate-1"
	return &Draft{Data: DraftData{
		Intent:    &DraftIntentConfig{Name: "app", Profile: profile},
		Source:    &DraftSourceConfig{Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://example.test/o/r.git", Ref: "main"},
		Detection: &DetectionResult{Candidates: []DetectedCandidate{candidate}, SelectedID: "candidate-1"},
	}}
}

func readinessPlan(method BuildMethod, start string, check string, domains ...PlannedDomain) PlanConfiguration {
	configuration := PlanConfiguration{
		Build:   BuildPlanConfig{Method: method, StartCommand: start},
		Runtime: RuntimePlanConfig{Strategy: StrategyBlueGreen},
		Domains: domains,
	}
	if check != "" {
		kind := string(CheckHTTP)
		if check == "docker" {
			kind, check = string(CheckDockerHealth), `{"attempts":20}`
		}
		configuration.Checks = []PlannedCheck{{Name: "readiness", Kind: kind, Phase: "readiness", Required: true, Config: json.RawMessage(check)}}
	}
	return configuration
}

func findingByCode(findings []PreflightFinding, code string) *PreflightFinding {
	for index := range findings {
		if findings[index].Code == code {
			return &findings[index]
		}
	}
	return nil
}

func TestPreflightRefusesStartCommandsThatDetach(t *testing.T) {
	t.Parallel()
	pm2Script := DetectedCandidate{BuildMethod: BuildRecipe, Recipe: "node", StartDetaches: &DetectedStartDetach{
		Command: "pm2 start app.js", Script: "start", Source: "package.json", Effect: startDetachExits,
		Reason: "pm2 start hands the application to pm2's background daemon", Action: "Start it with pm2-runtime",
	}}
	for _, test := range []struct {
		name      string
		candidate DetectedCandidate
		plan      PlanConfiguration
		code      string
		severity  PreflightSeverity
	}{
		{"the detected script", pm2Script, readinessPlan(BuildRecipe, "npm run start", ""), "start_command_daemonizes", PreflightBlocked},
		{"the script behind a schema step", pm2Script, readinessPlan(BuildRecipe, "npx prisma migrate deploy && npm run start", ""), "start_command_daemonizes", PreflightBlocked},
		{"an edited command", pm2Script, readinessPlan(BuildRecipe, "node app.js", ""), "", ""},
		{"a typed command", DetectedCandidate{BuildMethod: BuildRecipe}, readinessPlan(BuildRecipe, "gunicorn app:app -D", ""), "start_command_daemonizes", PreflightBlocked},
		{"two processes", DetectedCandidate{BuildMethod: BuildRecipe}, readinessPlan(BuildRecipe, "node worker.js & node server.js", ""), "start_command_backgrounds", PreflightWarning},
		{"a Dockerfile command", DetectedCandidate{BuildMethod: BuildDockerfile, StartDetaches: &DetectedStartDetach{
			Command: "pm2 start server.js", Source: "Dockerfile", Effect: startDetachExits, Reason: "r", Action: "a"}},
			readinessPlan(BuildDockerfile, "", ""), "start_command_daemonizes", PreflightWarning},
	} {
		findings := readinessPreflightFindings(readinessDraft(ProfileWorker, test.candidate), test.plan)
		for _, code := range []string{"start_command_daemonizes", "start_command_backgrounds"} {
			item := findingByCode(findings, code)
			if code != test.code {
				if item != nil {
					t.Fatalf("%s: unexpected %+v", test.name, item)
				}
				continue
			}
			if item == nil || item.Severity != test.severity || item.Measured == "" || item.FieldID == "" {
				t.Fatalf("%s: %+v", test.name, findings)
			}
		}
	}
	// A Dockerfile whose command the plan overrides is judged by the override.
	plan := readinessPlan(BuildDockerfile, "", "")
	plan.Runtime.Command = []string{"pm2-runtime", "start", "server.js"}
	candidate := DetectedCandidate{BuildMethod: BuildDockerfile, StartDetaches: &DetectedStartDetach{Command: "pm2 start server.js", Source: "Dockerfile", Effect: startDetachExits}}
	if item := findingByCode(readinessPreflightFindings(readinessDraft(ProfileWorker, candidate), plan), "start_command_daemonizes"); item != nil {
		t.Fatalf("override ignored: %+v", item)
	}
}

func TestPreflightWarnsAboutAWorkerPlannedAsWeb(t *testing.T) {
	t.Parallel()
	candidate := DetectedCandidate{BuildMethod: BuildRecipe, Profile: ProfileWorker, BackgroundWorker: &DetectedBackgroundWorker{
		Library: "discord.js", Kind: "Discord bot", Evidence: "Discord bot (discord.js); nothing in the package listens for HTTP"}}
	item := findingByCode(readinessPreflightFindings(readinessDraft(ProfileWeb, candidate), readinessPlan(BuildRecipe, "npm run start", `{"path":"/"}`)), "web_profile_without_listener")
	if item == nil || item.Severity != PreflightWarning || !strings.Contains(item.Title, "Discord bot") || item.Action != "Switch the workload to Worker." {
		t.Fatalf("web bot = %+v", item)
	}
	if item := findingByCode(readinessPreflightFindings(readinessDraft(ProfileWorker, candidate), readinessPlan(BuildRecipe, "npm run start", "")), "web_profile_without_listener"); item != nil {
		t.Fatalf("worker bot = %+v", item)
	}
}

func TestPreflightNamesWhereReadinessCameFrom(t *testing.T) {
	t.Parallel()
	rails := DetectedCandidate{BuildMethod: BuildDockerfile, Readiness: &DetectedReadiness{
		Kind: "http", Path: "/up", Source: readinessFromFramework, Evidence: "Rails health route in config/routes.rb"}}
	findings := readinessPreflightFindings(readinessDraft(ProfileWeb, rails), readinessPlan(BuildDockerfile, "", `{"path":"/up","attempts":20}`))
	if item := findingByCode(findings, "readiness_path_detected"); item == nil || item.Severity != PreflightPass || item.Measured != "GET /up · Rails health route in config/routes.rb" {
		t.Fatalf("detected = %+v", findings)
	}
	// An operator's own path is theirs; nothing claims it came from the source.
	findings = readinessPreflightFindings(readinessDraft(ProfileWeb, rails), readinessPlan(BuildDockerfile, "", `{"path":"/"}`))
	if findingByCode(findings, "readiness_path_detected") != nil {
		t.Fatalf("edited path claimed: %+v", findings)
	}
	docker := DetectedCandidate{BuildMethod: BuildDockerfile, Readiness: &DetectedReadiness{Kind: "docker_health", Source: readinessFromHealthcheck, Evidence: "HEALTHCHECK in the Dockerfile runs node"}}
	if item := findingByCode(readinessPreflightFindings(readinessDraft(ProfileWeb, docker), readinessPlan(BuildDockerfile, "", "docker")), "readiness_path_detected"); item == nil {
		t.Fatal("Docker health readiness was not named")
	}
	api := DetectedCandidate{BuildMethod: BuildRecipe, Readiness: &DetectedReadiness{Kind: "http", Path: "/", AcceptAnyAnswer: true, Source: readinessFromConvention}}
	findings = readinessPreflightFindings(readinessDraft(ProfileWeb, api), readinessPlan(BuildRecipe, "uvicorn main:app", `{"path":"/","acceptAnyAnswer":true}`))
	if item := findingByCode(findings, "readiness_root_unverified"); item == nil || item.Severity != PreflightPass {
		t.Fatalf("any answer = %+v", findings)
	}
	if findingByCode(findings, "readiness_path_detected") != nil {
		t.Fatal("a convention was presented as declared")
	}
	unrouted := DetectedCandidate{BuildMethod: BuildRecipe, Readiness: &DetectedReadiness{Kind: "http", Path: "/", AcceptAnyAnswer: true, Source: readinessFromConvention, RootRoute: "unrouted"}}
	findings = readinessPreflightFindings(readinessDraft(ProfileWeb, unrouted), readinessPlan(BuildRecipe, "uvicorn main:app", `{"path":"/"}`))
	if item := findingByCode(findings, "readiness_path_unrouted"); item == nil || item.Severity != PreflightWarning {
		t.Fatalf("unrouted = %+v", findings)
	}
}

func TestPreflightWarnsWhenTheHostAllowlistRefusesTheDomain(t *testing.T) {
	t.Parallel()
	domain := PlannedDomain{Hostname: "app.example.com", HTTPS: true, Ownership: OwnershipManaged}
	for _, test := range []struct {
		name    string
		allowed []string
		domains []PlannedDomain
		bind    string
		warn    bool
	}{
		{"exact", []string{"app.example.com"}, []PlannedDomain{domain}, "", false},
		{"subdomain pattern", []string{".example.com"}, []PlannedDomain{domain}, "", false},
		{"wildcard", []string{"*"}, []PlannedDomain{domain}, "", false},
		{"another host", []string{"old.example.org"}, []PlannedDomain{domain}, "", true},
		{"empty list", nil, []PlannedDomain{domain}, "", true},
		{"no domain, loopback refused", []string{"app.example.com"}, nil, "", true},
		{"no domain, loopback allowed", []string{"127.0.0.1", "app.example.com"}, nil, "", false},
		// The probe sends Host 127.0.0.1, which "localhost" does not match.
		{"no domain, only localhost allowed", []string{"localhost"}, nil, "", true},
		{"no domain, DEBUG's local names", []string{".localhost", "127.0.0.1", "[::1]"}, nil, "", false},
		{"no domain, IPv6 probe", []string{"127.0.0.1"}, nil, "::", true},
		{"no domain, IPv6 probe allowed", []string{"[::1]"}, nil, "::", false},
	} {
		candidate := DetectedCandidate{BuildMethod: BuildRecipe, Readiness: &DetectedReadiness{
			Kind: "http", Path: "/", Source: readinessFromConvention, AllowedHosts: test.allowed, AllowedHostsSource: "ALLOWED_HOSTS in mysite/settings.py"}}
		plan := readinessPlan(BuildRecipe, "gunicorn", `{"path":"/"}`, test.domains...)
		plan.Runtime.BindAddress = test.bind
		item := findingByCode(readinessPreflightFindings(readinessDraft(ProfileWeb, candidate), plan), "readiness_host_allowlist")
		if (item != nil) != test.warn {
			t.Fatalf("%s: %+v", test.name, item)
		}
		if item != nil && (item.Severity != PreflightWarning || !strings.Contains(item.Measured, "ALLOWED_HOSTS in mysite/settings.py")) {
			t.Fatalf("%s: %+v", test.name, item)
		}
	}
}

func TestPreflightWarnsAboutHTTPSRedirectsThePlanCannotServe(t *testing.T) {
	t.Parallel()
	https := PlannedDomain{Hostname: "app.example.com", HTTPS: true, Ownership: OwnershipManaged}
	plain := PlannedDomain{Hostname: "app.example.com", Ownership: OwnershipManaged}
	for _, test := range []struct {
		name    string
		ignores bool
		domains []PlannedDomain
		warn    string
	}{
		{"rails without a domain", false, nil, "serves no HTTPS domain"},
		{"rails over plain HTTP", false, []PlannedDomain{plain}, "serves no HTTPS domain"},
		{"rails over HTTPS", false, []PlannedDomain{https}, ""},
		{"django without the proxy header", true, []PlannedDomain{https}, "X-Forwarded-Proto"},
	} {
		candidate := DetectedCandidate{BuildMethod: BuildDockerfile, Readiness: &DetectedReadiness{
			Kind: "http", Path: "/up", Source: readinessFromFramework, HTTPSRedirect: "config.force_ssl in config/environments/production.rb", HTTPSRedirectIgnoresProxy: test.ignores}}
		item := findingByCode(readinessPreflightFindings(readinessDraft(ProfileWeb, candidate), readinessPlan(BuildDockerfile, "", `{"path":"/up"}`, test.domains...)), "readiness_redirects_to_https")
		if test.warn == "" {
			if item != nil {
				t.Fatalf("%s: %+v", test.name, item)
			}
			continue
		}
		if item == nil || item.Severity != PreflightWarning || !strings.Contains(item.Title, test.warn) {
			t.Fatalf("%s: %+v", test.name, item)
		}
	}
}

func TestPreflightWarnsAboutAModelDownloadedAtEveryStart(t *testing.T) {
	t.Parallel()
	candidate := DetectedCandidate{BuildMethod: BuildRecipe, Readiness: &DetectedReadiness{
		Kind: "http", Path: "/", Source: readinessFromConvention, Attempts: 60, IntervalSeconds: 10,
		ModelDownload: "SentenceTransformer in main.py", ModelCache: "/root/.cache/huggingface"}}
	plan := readinessPlan(BuildRecipe, "uvicorn main:app", `{"path":"/","attempts":60,"intervalSeconds":10}`)
	item := findingByCode(readinessPreflightFindings(readinessDraft(ProfileWeb, candidate), plan), "python_model_download_at_start")
	if item == nil || item.Severity != PreflightWarning || item.Measured != "SentenceTransformer in main.py" ||
		!strings.Contains(item.Action, "/root/.cache/huggingface") || !strings.Contains(item.Action, "up to 10 min") {
		t.Fatalf("download = %+v", item)
	}
	plan.Runtime.Mounts = []RuntimeMount{{Source: "model-cache", Target: "/root/.cache", Ownership: OwnershipManaged}}
	item = findingByCode(readinessPreflightFindings(readinessDraft(ProfileWeb, candidate), plan), "python_model_download_at_start")
	if item == nil || item.Severity != PreflightPass {
		t.Fatalf("kept cache = %+v", item)
	}
}

// The findings reach the one list preflight returns, so the advisory check
// before Deploy shows them.
func TestPreflightIncludesReadinessFindings(t *testing.T) {
	t.Parallel()
	candidate := DetectedCandidate{BuildMethod: BuildRecipe, Profile: ProfileWeb, Readiness: &DetectedReadiness{Kind: "http", Path: "/", AcceptAnyAnswer: true, Source: readinessFromConvention}}
	draft := readinessDraft(ProfileWeb, candidate)
	draft.Data.Detection.Source = SourceIdentity{Kind: SourceGit, Revision: strings.Repeat("a", 40)}
	findings := preflightFindings(draft, readinessPlan(BuildRecipe, "uvicorn main:app", `{"path":"/","acceptAnyAnswer":true}`), HostObservation{}, false)
	if findingByCode(findings, "readiness_root_unverified") == nil {
		t.Fatalf("findings = %+v", findings)
	}
}
