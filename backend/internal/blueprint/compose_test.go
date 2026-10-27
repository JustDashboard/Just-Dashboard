package blueprint

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// minimalComposeTemplate is the smallest stack the rules accept: an
// application behind the domain and a database it shares one generated
// password with.
func minimalComposeTemplate() *Blueprint {
	return &Blueprint{
		ID: "stack", Version: "1.0.0", Name: "Stack", Category: CategoryHTTP, Profile: ProfileCompose,
		Description: "An application and its database.", IconID: "globe", DocsURL: "https://example.test/docs",
		Access: Access{Kind: AccessSetup, Note: "The first visitor creates the account."},
		Provenance: Provenance{Maintainer: "Just Dashboard", License: "MIT", UpstreamURL: "https://example.test",
			ReviewedAt: "2026-10-27", MinimumDashboard: "0.8.2"},
		Inputs:  []Input{{Name: "domain", Kind: InputDomain, Label: "Public domain", Required: true}},
		Secrets: []Secret{{Name: "db-password", Variable: "DB_PASSWORD", Label: "Database password", Length: 32}},
		Services: []Service{
			{
				Name: "app", Role: RoleApp, Purpose: "The application",
				Image: Image{Reference: "example/app:1.2.3", TagPolicy: "pinned"},
				Environment: []Environment{
					{Name: "DATABASE_URL", Value: "postgres://app:{{secret.db-password}}@db:5432/app"},
					{Name: "APP_URL", Value: "{{variable.APP_URL}}"},
					{Name: "PRICE", Value: "costs $5"},
				},
				Ports:       []Port{{Name: "http", Internal: 8080, Protocol: "tcp", Purpose: "Web", Exposure: "proxy", Primary: true}},
				Volumes:     []Volume{{Name: "uploads", Target: "/uploads", Purpose: "Uploads", Data: true, Backup: true}},
				Resources:   Resources{MemoryMB: 256, MinMemoryMB: 128},
				Healthcheck: Healthcheck{Test: []string{"wget", "-q", "--spider", "http://127.0.0.1:8080/health"}, IntervalSeconds: 10, TimeoutSeconds: 5, Retries: 6},
				DependsOn:   []Dependency{{Service: "db", Condition: StartHealthy}},
			},
			{
				Name: "db", Role: RoleDatabase, Purpose: "PostgreSQL",
				Image:       Image{Reference: "postgres:17.6-alpine", TagPolicy: "pinned"},
				Environment: []Environment{{Name: "POSTGRES_PASSWORD", Value: "{{secret.db-password}}"}},
				Ports:       []Port{{Name: "postgres", Internal: 5432, Protocol: "tcp", Purpose: "SQL", Exposure: "internal"}},
				Volumes:     []Volume{{Name: "data", Target: "/var/lib/postgresql/data", Purpose: "Tables", Data: true, Backup: true}},
				Resources:   Resources{MemoryMB: 512},
				Healthcheck: Healthcheck{Test: []string{"pg_isready", "-U", "app"}, IntervalSeconds: 5, TimeoutSeconds: 5, Retries: 10},
			},
		},
		Checks:     []Check{{Name: "Health", Kind: CheckHTTP, Phase: "readiness", Required: true, Path: "/health", Port: "http"}},
		Operations: Operations{Startup: []Operation{{Kind: OperationSetVariable, Name: "APP_URL", Value: "https://{{input.domain}}"}}},
		Update:     Update{Detector: "registry"},
		Fixtures:   []Fixture{{Name: "default", Inputs: map[string]string{"domain": "app.example.test"}}},
	}
}

func TestMinimalComposeTemplateIsValid(t *testing.T) {
	t.Parallel()
	if err := Validate(minimalComposeTemplate()); err != nil {
		t.Fatal(err)
	}
}

// Each mutation is one way a stack could be unsafe or simply broken, and each
// is refused when the catalogue loads — that is, at build time — rather than
// when an operator presses Deploy.
func TestComposeValidationRejectsEachUnsafeShape(t *testing.T) {
	t.Parallel()
	app := func(b *Blueprint) *Service { return &b.Services[0] }
	db := func(b *Blueprint) *Service { return &b.Services[1] }
	for _, testCase := range []struct {
		name   string
		mutate func(*Blueprint)
		want   string
	}{
		{"services without the compose profile", func(b *Blueprint) { b.Profile = ProfileWeb }, "must use profile"},
		{"one service", func(b *Blueprint) { b.Services = b.Services[:1] }, "runs 2-8 services"},
		{"a top-level image beside the services", func(b *Blueprint) {
			b.Image = Image{Reference: "example/app:1", TagPolicy: "pinned"}
		}, "not at the top level"},
		{"a top-level memory figure", func(b *Blueprint) { b.Resources.MemoryMB = 512 }, "not at the top level"},
		{"privilege", func(b *Blueprint) { b.Security = Security{Privileged: true, Reason: "because"} }, "cannot ask for privileges"},
		{"an install operation", func(b *Blueprint) {
			b.Operations.Install = []Operation{{Kind: OperationWriteFile, Path: "/data/x"}}
		}, "nothing else"},
		{"a memory input", func(b *Blueprint) {
			b.Inputs = append(b.Inputs, Input{Name: "memory", Kind: InputMemory, Label: "Memory", Default: "512"})
		}, "limits are per service"},
		{"two services of one name", func(b *Blueprint) { db(b).Name = "app" }, "declared twice"},
		{"no public service", func(b *Blueprint) { app(b).Ports[0].Exposure = "internal"; app(b).Ports[0].Primary = false }, "exactly one service"},
		{"two public services", func(b *Blueprint) { db(b).Ports[0].Exposure = "proxy"; db(b).Role = RoleApp }, "exactly one service"},
		{"a public database", func(b *Blueprint) {
			app(b).Ports[0].Exposure = "internal"
			app(b).Ports[0].Primary = false
			db(b).Ports[0].Exposure = "proxy"
		}, "only an application is routed"},
		{"a port published on every interface", func(b *Blueprint) { db(b).Ports[0].Exposure = "direct" }, "proxy or internal"},
		{"a UDP port", func(b *Blueprint) { db(b).Ports[0].Protocol = "udp" }, "TCP only"},
		{"the Docker socket", func(b *Blueprint) {
			app(b).Volumes = append(app(b).Volumes, Volume{Name: "docker", Target: "/var/run/docker.sock", Purpose: "Socket", ReadOnly: true})
		}, "Docker socket"},
		{"a database with nowhere to keep its tables", func(b *Blueprint) { db(b).Volumes = nil }, "no persistent data volume"},
		{"a shared volume described two ways", func(b *Blueprint) {
			db(b).Volumes = append(db(b).Volumes, Volume{Name: "uploads", Target: "/srv/uploads", Purpose: "Uploads", Data: true, Backup: false})
		}, "describes its data and backup differently"},
		{"a literal password", func(b *Blueprint) {
			db(b).Environment = []Environment{{Name: "POSTGRES_PASSWORD", Value: "hunter2"}}
		}, "credential-shaped"},
		{"a password half literal", func(b *Blueprint) {
			db(b).Environment = []Environment{{Name: "POSTGRES_PASSWORD", Value: "x{{secret.db-password}}"}}
		}, "credential-shaped"},
		{"a literal password inside a URL", func(b *Blueprint) {
			app(b).Environment[0].Value = "postgres://app:hunter2@db:5432/app"
		}, "literal password into a URL"},
		{"a reference to a secret nobody generates", func(b *Blueprint) {
			app(b).Environment[0].Value = "postgres://app:{{secret.nope}}@db:5432/app"
		}, "does not generate"},
		{"a reference to a variable nothing sets", func(b *Blueprint) { app(b).Environment[1].Value = "{{variable.NOPE}}" }, "no input or startup operation sets"},
		{"an untyped placeholder", func(b *Blueprint) { app(b).Environment[1].Value = "{{input.domain}}" }, "placeholder other than"},
		{"a templated command", func(b *Blueprint) { app(b).Image.Command = []string{"serve", "--key={{secret.db-password}}"} }, "templates its command"},
		{"a templated healthcheck", func(b *Blueprint) {
			db(b).Healthcheck.Test = []string{"check", "{{secret.db-password}}"}
		}, "templates its healthcheck"},
		{"no healthcheck", func(b *Blueprint) { db(b).Healthcheck = Healthcheck{} }, "needs a healthcheck"},
		{"a healthcheck that asks for a shell", func(b *Blueprint) {
			db(b).Healthcheck.Test = []string{"CMD-SHELL", "pg_isready"}
		}, "without a shell"},
		{"a healthcheck with no timing", func(b *Blueprint) { db(b).Healthcheck.IntervalSeconds = 0 }, "healthcheck timing"},
		{"a dependency on nothing", func(b *Blueprint) { app(b).DependsOn = []Dependency{{Service: "cache", Condition: StartHealthy}} }, "not another service"},
		{"an unknown start condition", func(b *Blueprint) { app(b).DependsOn[0].Condition = "ready" }, "conditions are healthy"},
		{"a start-order cycle", func(b *Blueprint) { db(b).DependsOn = []Dependency{{Service: "app", Condition: StartStarted}} }, "cycle"},
		{"a secret no service is given", func(b *Blueprint) {
			b.Secrets = append(b.Secrets, Secret{Name: "spare", Variable: "SPARE_KEY", Label: "Spare", Length: 32})
		}, "no service references it"},
		{"an unknown role", func(b *Blueprint) { db(b).Role = "sidecar" }, "is not one of"},
		{"egress on the public service", func(b *Blueprint) { app(b).Egress = true }, "already reaches the internet"},
		{"a service with no memory figure", func(b *Blueprint) { db(b).Resources = Resources{} }, "memory figure"},
		{"a check naming a private service's port", func(b *Blueprint) {
			b.Checks = []Check{{Name: "SQL", Kind: CheckTCP, Phase: "readiness", Required: true, Port: "postgres"}}
		}, "names undeclared port"},
		{"a mutable service image with no explanation", func(b *Blueprint) { db(b).Image.TagPolicy = "mutable" }, "must explain itself"},
		{"a secret alphabet the generator does not write", func(b *Blueprint) { b.Secrets[0].Alphabet = "emoji" }, "letters and digits, or hex"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			candidate := minimalComposeTemplate()
			testCase.mutate(candidate)
			err := Validate(candidate)
			if err == nil {
				t.Fatalf("accepted %s", testCase.name)
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error %q does not mention %q", err, testCase.want)
			}
		})
	}
}

type renderedCompose struct {
	Services map[string]struct {
		Image       string            `yaml:"image"`
		Command     []string          `yaml:"command"`
		PullPolicy  string            `yaml:"pull_policy"`
		Restart     string            `yaml:"restart"`
		Environment map[string]string `yaml:"environment"`
		Ports       []string          `yaml:"ports"`
		Volumes     []string          `yaml:"volumes"`
		DependsOn   map[string]struct {
			Condition string `yaml:"condition"`
		} `yaml:"depends_on"`
		Healthcheck struct {
			Test        []string `yaml:"test"`
			Interval    string   `yaml:"interval"`
			Timeout     string   `yaml:"timeout"`
			Retries     int      `yaml:"retries"`
			StartPeriod string   `yaml:"start_period"`
		} `yaml:"healthcheck"`
		MemLimit string         `yaml:"mem_limit"`
		CPUs     float64        `yaml:"cpus"`
		Logging  map[string]any `yaml:"logging"`
		Networks []string       `yaml:"networks"`
	} `yaml:"services"`
	Volumes  map[string]any `yaml:"volumes"`
	Networks map[string]struct {
		Internal bool `yaml:"internal"`
	} `yaml:"networks"`
}

func parseRenderedCompose(t *testing.T, document string) renderedCompose {
	t.Helper()
	var parsed renderedCompose
	decoder := yaml.NewDecoder(strings.NewReader(document))
	decoder.KnownFields(true)
	if err := decoder.Decode(&parsed); err != nil {
		t.Fatalf("rendered document is not the Compose shape this test knows: %v\n%s", err, document)
	}
	return parsed
}

// The rendered stack is what an operator is promised: one service published,
// on loopback only; everything else on a network with no route in from the
// proxy and no route out; every shared value an interpolation of the
// deployment's own variables; every service with a healthcheck and a limit.
func TestComposeTemplatesRenderTheStackTheyPromise(t *testing.T) {
	t.Parallel()
	entries, err := All()
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, entry := range append([]*Blueprint{minimalComposeTemplate()}, entries...) {
		if !entry.IsCompose() {
			continue
		}
		checked++
		t.Run(entry.ID, func(t *testing.T) {
			plan, err := Render(entry, entry.Fixtures[0].Inputs)
			if err != nil {
				t.Fatal(err)
			}
			document := parseRenderedCompose(t, plan.Compose)
			public := entry.PublicService()
			if len(document.Services) != len(entry.Services) {
				t.Fatalf("rendered %d services, declared %d", len(document.Services), len(entry.Services))
			}
			if !document.Networks[composePrivateNetwork].Internal {
				t.Fatal("the private network has a route out")
			}
			secretVariables := map[string]string{}
			for _, secret := range entry.Secrets {
				secretVariables[secret.Variable] = secret.Name
			}
			referenced := map[string]int{}
			footprint := 0
			for _, declared := range entry.Services {
				service, found := document.Services[declared.Name]
				if !found {
					t.Fatalf("service %s was not rendered", declared.Name)
				}
				footprint += declared.Resources.MemoryMB
				if service.Image != declared.Image.Reference {
					t.Fatalf("%s runs %q, declared %q", declared.Name, service.Image, declared.Image.Reference)
				}
				if service.MemLimit != fmt.Sprintf("%dm", declared.Resources.MemoryMB) {
					t.Fatalf("%s is limited to %q", declared.Name, service.MemLimit)
				}
				if len(service.Healthcheck.Test) < 2 || service.Healthcheck.Test[0] != "CMD" {
					t.Fatalf("%s has healthcheck %q; it must run its argv without a shell", declared.Name, service.Healthcheck.Test)
				}
				networks := append([]string(nil), service.Networks...)
				sort.Strings(networks)
				if declared.Name == public.Name {
					// The ingress joins the first network by name. It must be
					// the edge, which no private service is on.
					if len(service.Ports) != 1 || !strings.HasPrefix(service.Ports[0], "127.0.0.1:") {
						t.Fatalf("public service publishes %q; it publishes its proxy port on loopback and nothing else", service.Ports)
					}
					if networks[0] != composeEdgeNetwork || !slices.Contains(networks, composePrivateNetwork) {
						t.Fatalf("public service is on %q", service.Networks)
					}
				} else {
					if len(service.Ports) > 0 {
						t.Fatalf("private service %s publishes %q", declared.Name, service.Ports)
					}
					if slices.Contains(networks, composeEdgeNetwork) || !slices.Contains(networks, composePrivateNetwork) {
						t.Fatalf("private service %s is on %q", declared.Name, service.Networks)
					}
					if slices.Contains(networks, composeEgressNetwork) != declared.Egress {
						t.Fatalf("private service %s egress is %v, declared %v", declared.Name, !declared.Egress, declared.Egress)
					}
				}
				for _, dependency := range declared.DependsOn {
					if service.DependsOn[dependency.Service].Condition != "service_"+string(dependency.Condition) {
						t.Fatalf("%s does not wait for %s to be %s", declared.Name, dependency.Service, dependency.Condition)
					}
				}
				for name, value := range service.Environment {
					for _, match := range strings.SplitAfter(value, "}") {
						start := strings.LastIndex(match, "${")
						if start < 0 {
							continue
						}
						variable := strings.TrimSuffix(match[start+2:], "}")
						if _, isSecret := secretVariables[variable]; isSecret {
							referenced[variable]++
						}
					}
					if composeSecretShaped(name) && !strings.HasPrefix(value, "${") {
						t.Fatalf("%s gives credential-shaped %s a literal", declared.Name, name)
					}
				}
			}
			for variable := range secretVariables {
				if referenced[variable] == 0 {
					t.Fatalf("secret %s reaches no service", variable)
				}
			}
			if plan.MemoryMB != footprint {
				t.Fatalf("plan footprint %d MB, services add up to %d MB", plan.MemoryMB, footprint)
			}
			// Nothing about a deployment's values is in the plan or the
			// document: a generated secret is a name and a length.
			encoded, _ := json.Marshal(plan)
			for _, variable := range plan.Variables {
				if variable.Generated && (variable.Value != "" || !strings.Contains(plan.Compose, "${"+variable.Name+"}")) {
					t.Fatalf("generated secret %s is not passed by reference", variable.Name)
				}
			}
			if strings.Contains(string(encoded), "hunter2") {
				t.Fatal("a literal credential reached the plan")
			}
		})
	}
	if checked < 2 {
		t.Fatalf("checked %d Compose templates", checked)
	}
}

// The two services of the minimal stack share one generated password: the
// database is started with it and the application's URL carries the same
// reference, so both read the one sealed value at start.
func TestComposeServicesShareOneGeneratedSecretByReference(t *testing.T) {
	t.Parallel()
	plan, err := Render(minimalComposeTemplate(), map[string]string{"domain": "App.Example.Test"})
	if err != nil {
		t.Fatal(err)
	}
	document := parseRenderedCompose(t, plan.Compose)
	if got := document.Services["db"].Environment["POSTGRES_PASSWORD"]; got != "${DB_PASSWORD}" {
		t.Fatalf("database password renders as %q", got)
	}
	if got := document.Services["app"].Environment["DATABASE_URL"]; got != "postgres://app:${DB_PASSWORD}@db:5432/app" {
		t.Fatalf("application URL renders as %q", got)
	}
	if got := document.Services["app"].Environment["APP_URL"]; got != "${APP_URL}" {
		t.Fatalf("plan variable renders as %q", got)
	}
	// A literal dollar is text, never an interpolation Compose would expand.
	if got := document.Services["app"].Environment["PRICE"]; got != "costs $$5" {
		t.Fatalf("literal dollar renders as %q", got)
	}
	generated := 0
	for _, variable := range plan.Variables {
		if variable.Name == "DB_PASSWORD" && variable.Generated && variable.Sensitivity == "secret" && variable.Value == "" {
			generated++
		}
		if variable.Name == "APP_URL" && variable.Value != "https://app.example.test" {
			t.Fatalf("APP_URL is %q", variable.Value)
		}
	}
	if generated != 1 {
		t.Fatal("the shared password is not one generated secret")
	}
	if len(plan.Domains) != 1 || plan.Domains[0] != "app.example.test" {
		t.Fatalf("domains %q", plan.Domains)
	}
	if plan.Image != "example/app:1.2.3" || len(plan.Ports) != 1 || plan.Ports[0].Internal != 8080 {
		t.Fatalf("the plan does not follow the public service: %s %+v", plan.Image, plan.Ports)
	}
	if len(plan.Services) != 2 || !plan.Services[0].Public || plan.Services[1].Public {
		t.Fatalf("services %+v", plan.Services)
	}
}

func TestComposeRenderingIsAFunctionOfTheDefinition(t *testing.T) {
	t.Parallel()
	first, err := Render(minimalComposeTemplate(), map[string]string{"domain": "one.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Render(minimalComposeTemplate(), map[string]string{"domain": "two.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	// Inputs become variables, never text in the document, so the release a
	// source stores is the same reviewed file whatever was typed.
	if first.Compose != second.Compose || first.Digest == second.Digest {
		t.Fatal("the document changed with an input, or the plan did not")
	}
	if port := TemplateHostPort("stack"); port < 20000 || port >= 40000 || port != TemplateHostPort("stack") {
		t.Fatalf("host port preference %d", port)
	}
}

func TestComposeSummaryListsServicesAndFootprint(t *testing.T) {
	t.Parallel()
	summary := Summarize(minimalComposeTemplate())
	if summary.SourceKind != "compose" || summary.MemoryMB != 768 || summary.Image != "example/app:1.2.3" {
		t.Fatalf("summary %+v", summary)
	}
	if len(summary.Services) != 2 || !summary.Services[0].Public || summary.Services[1].Role != RoleDatabase {
		t.Fatalf("services %+v", summary.Services)
	}
	if supported, reason := DeploymentSupport(minimalComposeTemplate()); !supported {
		t.Fatalf("a valid stack is not deployable: %s", reason)
	}
	single, err := Get("postgresql")
	if err != nil {
		t.Fatal(err)
	}
	if Summarize(single).SourceKind != "blueprint" || len(Summarize(single).Services) != 0 {
		t.Fatal("a single image is summarised as a stack")
	}
}
