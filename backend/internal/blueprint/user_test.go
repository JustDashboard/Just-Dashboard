package blueprint

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// A template added on a dashboard is named in a namespace no built-in may
// use, so neither can ever stand in for the other.
func TestUserTemplatesCannotShadowABuiltIn(t *testing.T) {
	t.Parallel()
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range all {
		if IsUserID(entry.ID) {
			t.Fatalf("built-in %q is in the namespace of added templates", entry.ID)
		}
		named, err := UserID(entry.ID)
		if err != nil {
			t.Fatal(err)
		}
		if named == entry.ID {
			t.Fatalf("a template named after %q gets the built-in's own id", entry.ID)
		}
		if _, err := Get(named); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s resolves to a built-in: %v", named, err)
		}
		// A built-in's own definition, imported as it is, keeps its id and
		// is refused rather than stored over it.
		raw, err := json.Marshal(entry)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ParseUserTemplate(raw); err == nil || !strings.Contains(err.Error(), UserPrefix+"<name>") {
			t.Fatalf("%s was accepted as an added template: %v", entry.ID, err)
		}
	}
}

func TestUserIDIsTheNameAsASlugInTheUserNamespace(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]string{
		"Analytics Stack":       "user-analytics-stack",
		"  nginx + Redis  ":     "user-nginx-redis",
		"user-already-here":     "user-already-here",
		"My_App.v2":             "user-my-app-v2",
		strings.Repeat("a", 80): "user-" + strings.Repeat("a", 59),
	} {
		got, err := UserID(name)
		if err != nil || got != want {
			t.Fatalf("UserID(%q) = %q, %v; want %q", name, got, err, want)
		}
	}
	for _, name := range []string{"", "   ", "!!!", "--"} {
		if got, err := UserID(name); err == nil {
			t.Fatalf("UserID(%q) = %q; want a refusal", name, got)
		}
	}
}

func userMinimal() *Blueprint {
	candidate := minimalBlueprint()
	candidate.ID = "user-probe"
	return candidate
}

// Every shape the catalogue refuses in a built-in it refuses in a template
// added here, in the same words.
func TestUserTemplateValidationRefusesWhatABuiltInsDoes(t *testing.T) {
	t.Parallel()
	for _, testCase := range unsafeShapes {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			candidate := userMinimal()
			testCase.mutate(candidate)
			_, err := PrepareUserTemplate(candidate)
			if err == nil {
				t.Fatalf("accepted %s", testCase.name)
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error %q does not mention %q", err, testCase.want)
			}
		})
	}
	if _, err := PrepareUserTemplate(userMinimal()); err != nil {
		t.Fatalf("the minimal definition is refused: %v", err)
	}
}

// And a few more, which only a definition that did not ship needs.
func TestUserTemplateRulesBeyondABuiltIns(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name   string
		mutate func(*Blueprint)
		want   string
	}{
		{"an id outside the namespace", func(b *Blueprint) { b.ID = "probe" }, "named user-<name>"},
		{"a retired definition", func(b *Blueprint) { b.Retired = "gone" }, "deleted rather than retired"},
		{"a game server", func(b *Blueprint) {
			b.Profile, b.Category = ProfileGame, CategoryGame
			b.Volumes = []Volume{{Name: "world", Target: "/data", Purpose: "World", Data: true}}
		}, "game servers"},
		{"privileges with a reason", func(b *Blueprint) {
			b.Security = Security{Privileged: true, Reason: "it asked nicely"}
		}, "cannot take privileges"},
		{"capabilities with a reason", func(b *Blueprint) {
			b.Security = Security{Capabilities: []string{"NET_ADMIN"}, Reason: "routing"}
		}, "cannot take privileges"},
		{"the Docker socket", func(b *Blueprint) {
			b.Volumes = []Volume{{Name: "docker", Target: "/var/run/docker.sock", Purpose: "Containers", ReadOnly: true}}
		}, "Docker socket"},
		{"a credential default read off the input's name", func(b *Blueprint) {
			b.Inputs = []Input{{Name: "admin-password", Kind: InputText, Label: "Admin", Variable: "ADMIN_INITIAL", Default: "changeme"}}
		}, "ships a default for a credential"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			candidate := userMinimal()
			testCase.mutate(candidate)
			if _, err := PrepareUserTemplate(candidate); err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("PrepareUserTemplate = %v; want %q", err, testCase.want)
			}
		})
	}
}

// A built-in's digests are its reviewer's to write and CI's to check; an added
// template's are recorded when it is saved, and a stored one that disagrees
// with what it renders is refused as CI refuses a built-in.
func TestUserTemplateFixturesAreRecordedAndHeld(t *testing.T) {
	t.Parallel()
	candidate := userMinimal()
	raw, err := PrepareUserTemplate(candidate)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := ParseUserTemplate(raw)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Render(stored, stored.Fixtures[0].Inputs)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Fixtures[0].Digest == "" || stored.Fixtures[0].Digest != plan.Digest {
		t.Fatalf("fixture records %q, renders to %q", stored.Fixtures[0].Digest, plan.Digest)
	}
	if err := ValidateUserTemplate(stored); err != nil {
		t.Fatal(err)
	}
	stored.Fixtures[0].Digest = "sha256:" + strings.Repeat("0", 64)
	if err := ValidateUserTemplate(stored); err == nil || !strings.Contains(err.Error(), "renders to") {
		t.Fatalf("a wrong digest passed: %v", err)
	}
	unrenderable := userMinimal()
	unrenderable.Inputs = []Input{{Name: "domain", Kind: InputDomain, Label: "Domain", Required: true}}
	if _, err := PrepareUserTemplate(unrenderable); err == nil || !strings.Contains(err.Error(), "does not render") {
		t.Fatalf("a fixture that cannot render was stored: %v", err)
	}
}

const nginxRedisCompose = `
services:
  web:
    image: nginx:1.27-alpine
    ports:
      - "8080:80"
    environment:
      CACHE_HOST: cache
      GREETING: ${GREETING:-hello}
    volumes:
      - html:/usr/share/nginx/html
    depends_on:
      cache:
        condition: service_healthy
    healthcheck:
      test: ["CMD", "wget", "-q", "--spider", "http://127.0.0.1/"]
      interval: 5s
      timeout: 3s
      retries: 10
    restart: always
  cache:
    image: redis:7.4-alpine
    command: redis-server --appendonly yes
    volumes:
      - cache-data:/data
    healthcheck:
      test: ["CMD-SHELL", "redis-cli ping | grep PONG"]
      interval: 5s
    mem_limit: 128m
volumes:
  html: {}
  cache-data:
`

func TestAComposeFileBecomesAStackTemplate(t *testing.T) {
	t.Parallel()
	definition, err := FromCompose(ComposeTemplate{
		TemplateMeta: TemplateMeta{Name: "Cached site", MinimumDashboard: "0.8.2", ReviewedAt: "2026-10-29", Maintainer: "admin"},
		Document:     nginxRedisCompose,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := PrepareUserTemplate(definition)
	if err != nil {
		t.Fatal(err)
	}
	if definition.ID != "user-cached-site" || definition.Profile != ProfileCompose || len(definition.Services) != 2 {
		t.Fatalf("definition = %s %s, %d services", definition.ID, definition.Profile, len(definition.Services))
	}
	web, cache := definition.Services[1], definition.Services[0]
	if web.Name != "web" || web.Role != RoleApp || cache.Role != RoleCache {
		t.Fatalf("roles: %s=%s %s=%s", web.Name, web.Role, cache.Name, cache.Role)
	}
	if public := definition.PublicService(); public == nil || public.Name != "web" || public.Ports[0].Internal != 80 {
		t.Fatalf("public service = %+v", public)
	}
	if strings.Join(cache.Image.Command, " ") != "redis-server --appendonly yes" || cache.Resources.MemoryMB != 128 {
		t.Fatalf("cache = %+v", cache)
	}
	if strings.Join(cache.Healthcheck.Test, " ") != "sh -c redis-cli ping | grep PONG" || cache.Healthcheck.IntervalSeconds != 5 {
		t.Fatalf("cache healthcheck = %+v", cache.Healthcheck)
	}
	if strings.Join(web.Healthcheck.Test, " ") != "wget -q --spider http://127.0.0.1/" || web.DependsOn[0].Condition != StartHealthy {
		t.Fatalf("web = %+v", web)
	}
	greeting := false
	for _, input := range definition.Inputs {
		if input.Variable == "GREETING" && input.Default == "hello" && !input.Required {
			greeting = true
		}
	}
	if !greeting {
		t.Fatalf("${GREETING:-hello} did not become an input defaulting to hello: %+v", definition.Inputs)
	}
	plan, err := Render(definition, map[string]string{"domain": "site.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	// The host port the file chose is gone: the public service is published
	// on loopback on the template's own port, and nothing else is published.
	if strings.Contains(plan.Compose, "8080") || !strings.Contains(plan.Compose, "127.0.0.1:") ||
		strings.Count(plan.Compose, "ports:") != 1 || !strings.Contains(plan.Compose, "this dashboard's own user-cached-site@1.0.0") {
		t.Fatalf("rendered document:\n%s", plan.Compose)
	}
	if len(plan.Domains) != 1 || plan.Domains[0] != "site.example.test" {
		t.Fatalf("domains = %v", plan.Domains)
	}
	if _, err := ParseUserTemplate(raw); err != nil {
		t.Fatal(err)
	}
}

// A password the file writes out never reaches the definition: each becomes
// a secret the template generates, one per password however many services
// and URLs repeat it.
func TestLiteralPasswordsInAComposeFileNeverReachTheTemplate(t *testing.T) {
	t.Parallel()
	const literal = "hunter2-correct-horse"
	document := `
services:
  app:
    image: ghcr.io/example/app:2.1.0
    ports: ["3000"]
    environment:
      DATABASE_URL: postgres://app:` + literal + `@db:5432/app
      SESSION_SECRET: another-literal-secret-value
      API_TOKEN: ${API_TOKEN}
    healthcheck:
      test: ["CMD", "wget", "-q", "--spider", "http://127.0.0.1:3000/health"]
  db:
    image: postgres:17-alpine
    environment:
      POSTGRES_USER: app
      POSTGRES_PASSWORD: ` + literal + `
    volumes: ["database:/var/lib/postgresql/data"]
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U app"]
`
	definition, err := FromCompose(ComposeTemplate{TemplateMeta: TemplateMeta{Name: "App"}, Document: document, HealthPath: "/health"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := PrepareUserTemplate(definition)
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{literal, "another-literal-secret-value"} {
		if strings.Contains(string(raw), leaked) {
			t.Fatalf("the definition carries %q:\n%s", leaked, raw)
		}
	}
	plan, err := Render(definition, map[string]string{"domain": "app.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plan.Compose, literal) {
		t.Fatalf("the rendered document carries the password:\n%s", plan.Compose)
	}
	// One secret for the shared password, named after the variable that held
	// it, read by the database and inside the app's URL; one for the session.
	variables := []string{}
	for _, secret := range definition.Secrets {
		variables = append(variables, secret.Variable)
	}
	if strings.Join(variables, ",") != "SESSION_SECRET,POSTGRES_PASSWORD" {
		t.Fatalf("secrets = %+v", definition.Secrets)
	}
	if !strings.Contains(plan.Compose, "postgres://app:${POSTGRES_PASSWORD}@db:5432/app") ||
		!strings.Contains(plan.Compose, `POSTGRES_PASSWORD: "${POSTGRES_PASSWORD}"`) {
		t.Fatalf("the shared password is not one variable:\n%s", plan.Compose)
	}
	// An interpolated credential is a secret input the Variables screen asks
	// for; the database is a database because of its image.
	token := false
	for _, input := range definition.Inputs {
		token = token || (input.Variable == "API_TOKEN" && input.Kind == InputSecret && input.Required)
	}
	if !token || definition.Services[1].Role != RoleDatabase || !definition.Update.BackupFirst {
		t.Fatalf("inputs %+v, db role %s", definition.Inputs, definition.Services[1].Role)
	}
	if definition.Checks[0].Kind != CheckHTTP || definition.Checks[0].Path != "/health" {
		t.Fatalf("checks = %+v", definition.Checks)
	}
}

// Compose reads $$ as one dollar, in a variable and in a URL alike, so a
// password written with one is still one secret shared by both.
func TestAPasswordWithADollarIsOneSecretWhereverItIsWritten(t *testing.T) {
	t.Parallel()
	document := `services:
  app:
    image: ghcr.io/example/app:2.1.0
    ports: ["3000"]
    environment:
      DATABASE_URL: postgres://app:pa$$word-with-dollar@db:5432/app
    healthcheck: {test: ["CMD", "true"]}
  db:
    image: postgres:17-alpine
    environment:
      POSTGRES_PASSWORD: pa$$word-with-dollar
    volumes: ["database:/var/lib/postgresql/data"]
    healthcheck: {test: ["CMD", "pg_isready"]}
`
	definition, err := FromCompose(ComposeTemplate{TemplateMeta: TemplateMeta{Name: "Dollar"}, Document: document})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareUserTemplate(definition); err != nil {
		t.Fatal(err)
	}
	if len(definition.Secrets) != 1 || definition.Secrets[0].Variable != "POSTGRES_PASSWORD" {
		t.Fatalf("secrets = %+v", definition.Secrets)
	}
}

func TestAComposeFileATemplateCannotCarryIsRefusedByName(t *testing.T) {
	t.Parallel()
	healthy := "    healthcheck:\n      test: [\"CMD\", \"true\"]\n"
	two := func(web, other string) string {
		return "services:\n  web:\n    image: nginx:1.27\n" + web + healthy + "  other:\n    image: redis:7\n" + other + healthy
	}
	for _, testCase := range []struct{ name, document, want string }{
		{"a build", two("    ports: [\"80:80\"]\n    build: .\n", ""), "builds its image"},
		{"a bind mount", two("    ports: [\"80:80\"]\n    volumes: [\"./site:/usr/share/nginx/html\"]\n", ""), "host path ./site"},
		{"the Docker socket", two("    ports: [\"80:80\"]\n    volumes: [\"/var/run/docker.sock:/var/run/docker.sock\"]\n", ""), "host path /var/run/docker.sock"},
		{"privileges", two("    ports: [\"80:80\"]\n    privileged: true\n", ""), "sets privileged"},
		{"a user", two("    ports: [\"80:80\"]\n    user: root\n", ""), "sets user"},
		{"two publishers", two("    ports: [\"80:80\"]\n", "    ports: [\"6379:6379\"]\n"), "other and web each publish"},
		{"nothing published", two("", ""), "no service publishes a port"},
		{"UDP", two("    ports: [\"53:53/udp\"]\n", ""), "is udp"},
		{"a conditional interpolation", two("    ports: [\"80:80\"]\n    environment:\n      MODE: ${DEBUG:+debug}\n", ""), "cannot express"},
		{"an interpolated image", "services:\n  web:\n    image: nginx:${TAG}\n", "interpolates its image"},
		{"an interpolated command", two("    ports: [\"80:80\"]\n    command: [\"nginx\", \"-g\", \"${OPTS}\"]\n", ""), "reads a variable"},
		{"a templated healthcheck", "services:\n  web:\n    image: nginx:1.27\n    ports: [\"80:80\"]\n    healthcheck:\n      test: [\"CMD\", \"curl\", \"-u\", \"${PASSWORD}\"]\n", "$$NAME"},
		{"no healthcheck", "services:\n  web:\n    image: nginx:1.27\n    ports: [\"80:80\"]\n  other:\n    image: redis:7\n", "has no healthcheck"},
		{"an external volume", two("    ports: [\"80:80\"]\n", "") + "volumes:\n  data:\n    external: true\n", "is external"},
		{"top-level secrets", two("    ports: [\"80:80\"]\n", "") + "secrets:\n  key:\n    file: ./key\n", "secrets at the top level"},
		{"a database with nowhere to keep its tables", "services:\n  web:\n    image: nginx:1.27\n    ports: [\"80:80\"]\n" + healthy + "  db:\n    image: postgres:17\n    environment:\n      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}\n" + healthy, "persistent data volume"},
		{"an empty password", two("    ports: [\"80:80\"]\n    environment:\n      ADMIN_PASSWORD: \"\"\n", ""), "credential-shaped and empty"},
		{"a private service publishing", "services:\n  web:\n    image: nginx:1.27\n    ports: [\"80:80\"]\n" + healthy + "  db:\n    image: redis:7\n    ports: [\"6379\"]\n" + healthy, "each publish"},
		{"not YAML", "services: [", "not YAML"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			definition, err := FromCompose(ComposeTemplate{TemplateMeta: TemplateMeta{Name: "Refused"}, Document: testCase.document})
			if err == nil {
				_, err = PrepareUserTemplate(definition)
			}
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("got %v; want %q", err, testCase.want)
			}
		})
	}
}

// One service is one image, deployed as a single reviewed image is; its
// variables become inputs, generated secrets and startup operations.
func TestASingleServiceComposeFileIsASingleImageTemplate(t *testing.T) {
	t.Parallel()
	definition, err := FromCompose(ComposeTemplate{
		TemplateMeta: TemplateMeta{Name: "Echo"},
		Document: `services:
  echo:
    image: traefik/whoami:v1.12.0
    ports: ["8000:80"]
    environment:
      WHOAMI_NAME: ${WHOAMI_NAME:-echo}
      LOG_LEVEL: info
      ADMIN_TOKEN: written-out-token-value
    volumes: ["state:/state"]
`,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := PrepareUserTemplate(definition)
	if err != nil {
		t.Fatal(err)
	}
	if definition.IsCompose() || definition.Profile != ProfileWeb || definition.Image.Reference != "traefik/whoami:v1.12.0" ||
		definition.Ports[0].Internal != 80 || definition.Ports[0].Exposure != "proxy" {
		t.Fatalf("definition = %+v", definition)
	}
	if strings.Contains(string(raw), "written-out-token-value") || len(definition.Secrets) != 1 || definition.Secrets[0].Variable != "ADMIN_TOKEN" {
		t.Fatalf("secrets = %+v", definition.Secrets)
	}
	plan, err := Render(definition, nil)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for _, variable := range plan.Variables {
		values[variable.Name] = variable.Value
	}
	if values["WHOAMI_NAME"] != "echo" || values["LOG_LEVEL"] != "info" {
		t.Fatalf("variables = %+v", plan.Variables)
	}
}

// A file saved from a deployment names the domain that deployment answered
// at; the template sets each such value from its own domain input.
func TestTheConcreteDomainBecomesTheDomainInput(t *testing.T) {
	t.Parallel()
	definition, err := FromCompose(ComposeTemplate{
		TemplateMeta: TemplateMeta{Name: "Linked"},
		Document: `services:
  web:
    image: nginx:1.27
    ports: ["80"]
    environment:
      PUBLIC_URL: https://shop.example.com/store
    healthcheck: {test: ["CMD", "true"]}
  worker:
    image: busybox:1.36
    command: ["sleep", "infinity"]
    healthcheck: {test: ["CMD", "true"]}
`,
		Domain: "shop.example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := PrepareUserTemplate(definition)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "shop.example.com") {
		t.Fatalf("the definition names the deployment's domain:\n%s", raw)
	}
	plan, err := Render(definition, map[string]string{"domain": "next.example.org"})
	if err != nil {
		t.Fatal(err)
	}
	for _, variable := range plan.Variables {
		if variable.Name == "PUBLIC_URL" && variable.Value != "https://next.example.org/store" {
			t.Fatalf("PUBLIC_URL = %q", variable.Value)
		}
	}
	if _, err := Render(definition, nil); err == nil {
		t.Fatal("a template whose values read the domain rendered without one")
	}
}

// A direct port can take its host number from an input, which is how a
// deployment's own publication survives as an answer instead of a number.
func TestADirectPortIsPublishedOnTheHostPortItsInputChose(t *testing.T) {
	t.Parallel()
	definition, err := FromImage(ImageTemplate{
		TemplateMeta: TemplateMeta{Name: "SSH box"}, Image: "linuxserver/openssh-server:9.9",
		Port: 2222, Direct: true, HostPort: 22022,
		Published: []PublishedSpec{{Container: 8080, Host: 18080}, {Container: 9000, Host: 9000}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareUserTemplate(definition); err != nil {
		t.Fatal(err)
	}
	plan, err := Render(definition, map[string]string{"host-port": "2200"})
	if err != nil {
		t.Fatal(err)
	}
	hosts := map[string]int{}
	for _, port := range plan.Ports {
		hosts[port.Name] = port.HostPort
	}
	if hosts["main"] != 2200 || hosts["port-8080"] != 18080 || hosts["port-9000"] != 0 {
		t.Fatalf("host ports = %v", hosts)
	}
	unbounded := userMinimal()
	unbounded.Ports[0].Exposure, unbounded.Ports[0].HostInput = "direct", "host"
	unbounded.Inputs = []Input{{Name: "host", Kind: InputNumber, Label: "Host port", Default: "80"}}
	if _, err := PrepareUserTemplate(unbounded); err == nil || !strings.Contains(err.Error(), "bounded within 1-65535") {
		t.Fatalf("an unbounded host input passed: %v", err)
	}
	proxied := userMinimal()
	proxied.Ports[0].HostInput = "host"
	proxied.Inputs = []Input{{Name: "host", Kind: InputNumber, Label: "Host port", Default: "80", Minimum: 1, Maximum: 65535}}
	if _, err := PrepareUserTemplate(proxied); err == nil || !strings.Contains(err.Error(), "only a direct port") {
		t.Fatalf("a proxied port took a host input: %v", err)
	}
}

func TestComposeInterpolationReadsComposesOwnSyntax(t *testing.T) {
	t.Parallel()
	segments, err := interpolation("a$$b ${ONE} ${TWO:-two} ${THREE-} $FOUR ${FIVE:?needed}")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, part := range segments {
		if part.variable == "" {
			got = append(got, "'"+part.literal+"'")
			continue
		}
		got = append(got, part.variable+"="+part.fallback+map[bool]string{true: "!", false: ""}[part.hasDefault])
	}
	want := "'a$b ' ONE= ' ' TWO=two! ' ' THREE=! ' ' FOUR= ' ' FIVE="
	if strings.Join(got, " ") != want {
		t.Fatalf("segments = %s; want %s", strings.Join(got, " "), want)
	}
	for _, refused := range []string{"${A:+x}", "${A", "${A:-${B}}", "${9}"} {
		if _, err := interpolation(refused); err == nil {
			t.Fatalf("%q was read", refused)
		}
	}
}

func TestImageHomepage(t *testing.T) {
	t.Parallel()
	for reference, want := range map[string]string{
		"nginx:1.27":                       "https://hub.docker.com/_/nginx",
		"docker.io/library/redis:7":        "https://hub.docker.com/_/redis",
		"traefik/whoami:v1.12.0":           "https://hub.docker.com/r/traefik/whoami",
		"ghcr.io/example/app:2@sha256:abc": "https://ghcr.io/example/app",
	} {
		if got := ImageHomepage(reference); got != want {
			t.Fatalf("ImageHomepage(%q) = %q; want %q", reference, got, want)
		}
	}
}
