package deploy

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Preflight over the environment detection read: what a variable is
// supplied with, whether that value can work inside the container, and
// which third parties need the planned domain registered with them. Values
// are read only to derive facts — loopback, a key's shape, a publishable
// key's own domain — and no finding repeats a secret value.

// environmentState is one plan's variables as preflight sees them.
type environmentState struct {
	candidate *DetectedCandidate
	declared  map[string]PlannedVariable
	staged    map[string]string
	// holdsValues is false for a recorded plan read back at release time: it
	// carries names and scopes, and every declared variable has a stored
	// value the executor does not open here.
	holdsValues bool
	domains     []PlannedDomain
	method      BuildMethod
	// recipeSupplied are build-time reads the Node recipe's build runs
	// without a value for: an env schema's names it skips with
	// SKIP_ENV_VALIDATION (preflight_build.go names those instead), and the
	// names prisma.config reads, which it gives a placeholder while
	// `prisma generate` runs (build_node_prisma.go).
	recipeSupplied map[string]bool
}

func newEnvironmentState(draft *Draft, configuration PlanConfiguration) environmentState {
	state := environmentState{
		candidate: rootDetectionCandidate(draft.Data.Detection, configuration.Build), declared: map[string]PlannedVariable{},
		staged: map[string]string{}, holdsValues: draft.ID != "", domains: configuration.Domains,
		method: configuration.Build.Method,
	}
	for _, variable := range configuration.Variables {
		state.declared[variable.Name] = variable
	}
	for name, value := range draft.environment {
		state.staged[name] = value
	}
	state.recipeSupplied = map[string]bool{}
	if record := nodeBuildSkipsValidation(state.candidate, configuration); record != nil {
		for _, name := range slices.Concat(record.EnvServer, record.EnvClient) {
			state.recipeSupplied[name] = true
		}
	}
	if state.candidate != nil && state.candidate.NodeBuild != nil &&
		configuration.Build.Method == BuildRecipe && configuration.Build.Recipe == "node" {
		for _, name := range state.candidate.NodeBuild.PrismaEnv {
			state.recipeSupplied[name] = true
		}
	}
	return state
}

// set reports whether the variable will hold a value.
func (e environmentState) set(name string) bool {
	if e.staged[name] != "" {
		return true
	}
	declared, ok := e.declared[name]
	if !ok {
		return false
	}
	if !e.holdsValues {
		return true
	}
	if _, staged := e.staged[name]; staged {
		// An empty input deliberately overrides a generated default.
		return false
	}
	return declared.Generate > 0 || declared.Value != "" || declared.Reference != ""
}

// value is the literal the variable will hold when this plan knows it: a
// staged input or a plain declared value. Generated values and references
// resolve later and read as empty.
func (e environmentState) value(name string) (string, bool) {
	if value, ok := e.staged[name]; ok {
		return value, value != ""
	}
	if declared, ok := e.declared[name]; ok && declared.Value != "" {
		return declared.Value, true
	}
	return "", false
}

// buildScoped reports whether a value reaches the build: a staged input
// takes its declaration's scopes, or build and runtime when undeclared.
func (e environmentState) buildScoped(name string) bool {
	if declared, ok := e.declared[name]; ok {
		for _, scope := range declared.Scopes {
			if scope == "build" {
				return true
			}
		}
		return false
	}
	return e.staged[name] != ""
}

func (e environmentState) browserInlined(variable DetectedVariable) bool {
	return variable.BrowserInlined || (e.candidate != nil && browserPrefixed(variable.Name, e.candidate.BrowserPrefixes))
}

func (e environmentState) note(code string) []EnvironmentNote {
	result := []EnvironmentNote{}
	if e.candidate == nil {
		return result
	}
	for _, note := range e.candidate.EnvironmentNotes {
		if note.Code == code {
			result = append(result, note)
		}
	}
	return result
}

func (e environmentState) primaryDomain() (PlannedDomain, bool) {
	for _, domain := range e.domains {
		if strings.TrimSpace(domain.Hostname) != "" {
			return domain, true
		}
	}
	return PlannedDomain{}, false
}

func (e environmentState) publicOrigin() string {
	domain, ok := e.primaryDomain()
	if !ok {
		return "https://<your domain>"
	}
	scheme := "http"
	if domain.HTTPS {
		scheme = "https"
	}
	return scheme + "://" + strings.ToLower(domain.Hostname)
}

func variableField(name string) string { return "variables." + name }

func listNames(names []string) string {
	if len(names) > 6 {
		return strings.Join(names[:6], ", ") + fmt.Sprintf(" and %d more", len(names)-6)
	}
	return strings.Join(names, ", ")
}

// environmentFindings is the environment half of preflight.
func environmentFindings(draft *Draft, configuration PlanConfiguration, observation HostObservation) []PreflightFinding {
	state := newEnvironmentState(draft, configuration)
	findings := []PreflightFinding{}
	findings = append(findings, valueFindings(state)...)
	if state.candidate == nil {
		return findings
	}
	findings = append(findings, detectedVariableFindings(state)...)
	findings = append(findings, frameworkFindings(state)...)
	findings = append(findings, databaseFindings(state, configuration, observation)...)
	findings = append(findings, callbackFindings(state)...)
	return findings
}

// valueFindings reads the values this plan knows for what they point at.
func valueFindings(state environmentState) []PreflightFinding {
	findings := []PreflightFinding{}
	names := make([]string, 0, len(state.declared)+len(state.staged))
	seen := map[string]bool{}
	for name := range state.declared {
		names, seen[name] = append(names, name), true
	}
	for name := range state.staged {
		if !seen[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	inlined := map[string]bool{}
	if state.candidate != nil {
		for _, variable := range state.candidate.Variables {
			if state.browserInlined(variable) {
				inlined[variable.Name] = true
			}
		}
	}
	for _, name := range names {
		value, known := state.value(name)
		if !known {
			continue
		}
		browser := inlined[name] || (state.candidate != nil && browserPrefixed(name, state.candidate.BrowserPrefixes))
		switch {
		case bindAddressName(name):
			if loopbackTarget(strings.Split(value, ":")[0]) && strings.Split(value, ":")[0] != "0.0.0.0" {
				findings = append(findings, finding(variableFindingCode("host_variable_loopback_", name), PreflightWarning,
					name+" makes the server listen on loopback only", name+" is a loopback address",
					"Inside the container, loopback is unreachable from the proxy, so readiness and every request fail.",
					"Set "+name+" to 0.0.0.0.", "deploy", variableField(name)))
			}
		case browser && loopbackValue(name, value):
			findings = append(findings, finding(variableFindingCode("build_inlines_localhost_", name), PreflightWarning,
				name+" compiles a localhost address into the browser bundle", name+" points at localhost",
				"The build writes the value into the JavaScript every visitor downloads, so each browser calls its own machine.",
				"Use the public address the browser should call, such as "+state.publicOrigin()+".", "deploy", variableField(name)))
		case loopbackValue(name, value):
			findings = append(findings, finding(variableFindingCode("variable_points_to_localhost_", name), PreflightWarning,
				name+" points at localhost, which inside the container is the app itself", name+" names a loopback host",
				"Nothing listens there from the container's point of view, so every connection is refused.",
				"Link a database (it becomes db-N.jd.internal) or use a host the container can reach.", "deploy", variableField(name)))
		}
		if browser {
			if shape, blocked := browserSecretShape(name, value); shape != "" {
				severity := PreflightWarning
				if blocked {
					severity = PreflightBlocked
				}
				findings = append(findings, finding(variableFindingCode("public_variable_secret_", name), severity,
					name+" is compiled into the JavaScript every visitor downloads", shape,
					"A browser-prefixed value is public after the build, however it is stored here.",
					"Call the API from a server route and keep the key server-only, or accept the exposure.", "deploy", variableField(name)))
			}
		}
	}
	return findings
}

var (
	browserPublicByDesign = regexp.MustCompile(`ANON_KEY|PUBLISHABLE|SITE_KEY|MEASUREMENT_ID|_DSN$|FIREBASE|MAPBOX|POSTHOG|ALGOLIA_SEARCH|SEARCH_ONLY|_GA_|_GTM_|RECAPTCHA_SITE|PUBLIC_KEY$|CLIENT_ID$`)
	browserSecretName     = regexp.MustCompile(`SECRET|PRIVATE|SERVICE_ROLE|PASSWORD|_TOKEN$|(?:OPENAI|ANTHROPIC|GEMINI|GOOGLE_AI|GROQ|MISTRAL|COHERE|REPLICATE|HUGGINGFACE|HF|DEEPSEEK|XAI|PERPLEXITY|ELEVENLABS|TOGETHER|FIREWORKS|OPENROUTER|RESEND|SENDGRID|STRIPE_SECRET)_API_KEY$|DATABASE_URL$`)
	browserSecretValue    = regexp.MustCompile(`^(?:sk_live_|rk_live_|sk_test_|rk_test_|sk-proj-|sk-ant-|sk-or-|ghp_|github_pat_|gho_|xoxb-|xoxp-|-----BEGIN)`)
)

// browserSecretShape says why a browser-inlined value looks like a secret,
// and whether it is one that must never ship: a live Stripe secret key or a
// Supabase service_role token bypasses every access rule.
func browserSecretShape(name, value string) (string, bool) {
	if strings.HasPrefix(value, "sk_live_") || strings.HasPrefix(value, "rk_live_") {
		return "the value is a live Stripe secret key", true
	}
	if jwtRole(value) == "service_role" {
		return "the value is a Supabase service_role token", true
	}
	if browserSecretValue.MatchString(value) {
		return "the value has a secret key's shape", false
	}
	upper := strings.ToUpper(name)
	if browserSecretName.MatchString(upper) && !browserPublicByDesign.MatchString(upper) {
		return "the name says it is a secret", false
	}
	return "", false
}

// jwtRole reads the role claim of an unsigned view of a JWT, which is how a
// Supabase key says whether it is the public anon key or the service key.
func jwtRole(value string) string {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return ""
	}
	var claims struct {
		Role string `json:"role"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	return claims.Role
}

// detectedVariableFindings answers what detection said each variable needs.
func detectedVariableFindings(state environmentState) []PreflightFinding {
	findings := []PreflightFinding{}
	generated, unbuilt, documented, likely := []string{}, []string{}, []string{}, []string{}
	for _, variable := range state.candidate.Variables {
		name := variable.Name
		set := state.set(name)
		switch variable.Setup {
		case "generate":
			if declared, ok := state.declared[name]; ok && declared.Generate > 0 && state.staged[name] == "" && state.holdsValues {
				generated = append(generated, name)
			}
			if !set {
				findings = append(findings, finding(variableFindingCode("secret_unset_", name), PreflightWarning,
					name+" is not set", variable.SetupReason,
					"The dashboard generates this secret when it is left to it; cleared, the application refuses to start or to sign sessions.",
					"Let the dashboard generate it, or enter your own value.", "deploy", variableField(name)))
			}
		case "domain":
			findings = append(findings, publicURLFinding(state, variable)...)
		case "default":
			if !set && variable.Name == "HOST" {
				findings = append(findings, finding("host_variable_loopback_host", PreflightWarning,
					"HOST is not set", variable.SetupReason,
					"The application's own default may listen on loopback, which the proxy cannot reach.",
					"Keep HOST at 0.0.0.0.", "deploy", variableField(name)))
			}
		case "", "paste":
			if variable.Setup == "" && !set && !variable.Required && !variable.RequiredRead && len(variable.Sources) > 0 &&
				envTemplateFile(pathBase(variable.Sources[0])) && !envRealFile(pathBase(variable.Sources[0])) {
				documented = append(documented, name)
			}
		}
		if (variable.RequiredRead || (variable.Required && !isDeclared(state, name))) && !set && variable.Setup == "" {
			likely = append(likely, name)
		}
		if variable.Phase == "build" && state.method == BuildRecipe && !state.buildScoped(name) && !state.recipeSupplied[name] {
			if variable.Required {
				findings = append(findings, finding(variableFindingCode("build_variable_missing_", name), PreflightBlocked,
					name+" is needed while the build runs", "read at build time in "+variableReadIn(variable),
					"The build fails without it: a static env import, a compile-time read or a build-time schema needs the value.",
					"Set "+name+" with Build scope.", "deploy", variableField(name)))
			} else if state.browserInlined(variable) {
				unbuilt = append(unbuilt, name)
			}
		}
		// A name the Dockerfile declares with ARG is answered by
		// dockerfile_build_args or dockerfile_arg_not_passed (preflight_image.go).
		if variable.Phase == "build" && state.method == BuildDockerfile && variable.Required && set && !dockerfileDeclaresArg(state.candidate, name) {
			findings = append(findings, finding(variableFindingCode("build_variable_unreachable_", name), PreflightWarning,
				name+" does not reach a Dockerfile build", "read at build time in "+variableReadIn(variable),
				"A repository Dockerfile builds only with the plain, browser-public build variables it declares with ARG; this one it does not declare.",
				"Declare it with ARG in the Dockerfile and a default, or build with an automatic recipe.", "deploy", variableField(name)))
		}
		if variable.LocalhostIn != "" && !set {
			code, title := variableFindingCode("variable_points_to_localhost_", name), name+" falls back to a localhost address"
			means := "The committed file supplies it when the dashboard does not, and inside the container localhost is the app itself."
			if state.browserInlined(variable) {
				code, title = variableFindingCode("build_inlines_localhost_", name), name+" compiles a localhost address into the browser bundle"
				means = "The build reads the committed file and writes its localhost value into the JavaScript every visitor downloads."
			}
			findings = append(findings, finding(code, PreflightWarning, title, variable.LocalhostIn+" sets it to a loopback address",
				means, "Set "+name+" here; a value set by the dashboard wins over the committed file.", "deploy", variableField(name)))
		}
	}
	if len(generated) > 0 {
		sort.Strings(generated)
		findings = append(findings, finding("secrets_generated", PreflightPass,
			"Secrets are generated when the project is created", listNames(generated),
			"Each is minted in its framework's format, sealed, and stays the same across releases.", "", "deploy", "variables"))
	}
	if len(unbuilt) > 0 {
		findings = append(findings, finding("browser_variables_unbuilt", PreflightWarning,
			"Browser variables will build as undefined", listNames(unbuilt),
			"The framework compiles these names into the bundle while it builds; one with no build-scoped value is undefined in every visitor's browser until the next build.",
			"Set them with Build scope before deploying, or accept that the client runs without them.", "deploy", "variables"))
	}
	if len(documented) > 0 {
		findings = append(findings, finding("documented_variables_unset", PreflightWarning,
			"Documented variables are not set", listNames(documented),
			"The repository's template lists them; the application starts without them and fails where it reads one.",
			"Set the ones this deployment uses; acknowledge the rest.", "deploy", "variables"))
	}
	if len(likely) > 0 {
		findings = append(findings, finding("variable_likely_required", PreflightWarning,
			"Variables read without a default are not set", listNames(likely),
			"The code reads these in a form that raises when the value is missing, on at least one path.",
			"Set them, or confirm the path that reads them never runs here.", "deploy", "variables"))
	}
	return findings
}

func dockerfileDeclaresArg(candidate *DetectedCandidate, name string) bool {
	for _, arg := range candidate.DockerfileArgs {
		if arg.Name == name {
			return true
		}
	}
	return false
}

func variableReadIn(variable DetectedVariable) string {
	if len(variable.Sources) == 0 {
		return "the source"
	}
	return variable.Sources[0]
}

func isDeclared(state environmentState, name string) bool {
	_, ok := state.declared[name]
	return ok
}

func pathBase(source string) string {
	if slash := strings.LastIndex(source, "/"); slash >= 0 {
		return source[slash+1:]
	}
	return source
}

// publicURLFinding compares a self-URL with the planned domain.
func publicURLFinding(state environmentState, variable DetectedVariable) []PreflightFinding {
	name := variable.Name
	code := "public_url"
	if name == "PHX_HOST" {
		code = "phoenix_host"
	}
	domain, planned := state.primaryDomain()
	if !state.set(name) {
		measured := "no domain is planned"
		if planned {
			measured = "cleared"
		}
		return []PreflightFinding{finding(variableFindingCode(code+"_unbound_", name), PreflightWarning,
			name+" has no value", measured,
			variable.SetupReason+"; without it the application builds its own links and callbacks from the wrong address.",
			"Add a domain on the runtime step, or enter the public address yourself.", "deploy", variableField(name))}
	}
	value, known := state.value(name)
	if !known {
		return nil
	}
	host := value
	if parsed, err := url.Parse(value); err == nil && parsed.Host != "" {
		host = parsed.Hostname()
	}
	// A host list reads in the separator its template was rendered with; a
	// value joined with the other one is a single host that matches nothing.
	separator := ","
	if strings.Contains(variable.DomainTemplate, " ") {
		separator = " "
	}
	host = strings.ToLower(strings.TrimSpace(strings.Split(host, separator)[0]))
	matches := false
	for _, candidate := range state.domains {
		if strings.EqualFold(candidate.Hostname, host) {
			matches = true
		}
	}
	if !matches && !loopbackValue(name, value) {
		return []PreflightFinding{finding(variableFindingCode(code+"_mismatch_", name), PreflightWarning,
			name+" names a host this deployment does not serve", "none of the planned domains",
			variable.SetupReason+"; links, redirects and origin checks will point elsewhere.",
			"Leave it bound to the domain, or plan the domain it names.", "deploy", variableField(name))}
	}
	if !matches {
		return nil
	}
	means := "It follows the domain on the runtime step until it is edited."
	if variable.Phase == "build" {
		means += " It is compiled into the bundle, so a domain change needs a rebuild."
	}
	measured := name + " → " + value
	if _, typed := state.staged[name]; typed {
		measured = name + " → " + domain.Hostname
	}
	return []PreflightFinding{finding(variableFindingCode(code+"_bound_", name), PreflightPass,
		name+" is the planned address", measured, means, "", "deploy", variableField(name))}
}

// frameworkFindings answers the notes a framework's configuration left.
func frameworkFindings(state environmentState) []PreflightFinding {
	findings := []PreflightFinding{}
	candidate := state.candidate
	if candidate.Framework == "rails" && !state.set("SECRET_KEY_BASE") && !state.set("RAILS_MASTER_KEY") {
		findings = append(findings, finding("rails_secret_missing", PreflightBlocked,
			"Rails has no secret_key_base", "neither SECRET_KEY_BASE nor RAILS_MASTER_KEY is set",
			"Rails refuses every request in production without it, including the health check.",
			"Let the dashboard generate SECRET_KEY_BASE, or paste config/master.key as RAILS_MASTER_KEY.", "deploy", variableField("SECRET_KEY_BASE")))
	}
	if credentials := state.note("rails_credentials"); len(credentials) > 0 && !state.set("RAILS_MASTER_KEY") {
		severity := PreflightWarning
		means := "Rails cannot decrypt the committed credentials, so every value read from them is nil."
		if len(state.note("rails_require_master_key")) > 0 {
			severity = PreflightBlocked
			means = "production.rb sets config.require_master_key, so Rails refuses to boot without it."
		}
		findings = append(findings, finding("rails_master_key_missing", severity,
			"RAILS_MASTER_KEY is not set", credentials[0].Path+" is committed", means,
			"Paste the contents of config/master.key as RAILS_MASTER_KEY; it cannot be generated.", "deploy", variableField("RAILS_MASTER_KEY")))
	}
	insecure := []string{}
	for _, note := range state.note(observeSecretLiteral) {
		if note.Detail == "django-insecure" {
			insecure = append(insecure, note.Path+" commits a django-insecure SECRET_KEY")
		} else {
			insecure = append(insecure, note.Path+" commits a literal SECRET_KEY")
		}
	}
	for _, note := range state.note(observeDebugLiteral) {
		insecure = append(insecure, note.Path+" turns DEBUG on literally")
	}
	for _, note := range state.note(observeDebugDefault) {
		name, seed, _ := strings.Cut(note.Detail, "|")
		switch {
		case seed == "":
			insecure = append(insecure, note.Path+" turns debug mode on for any non-empty "+name)
		case !state.set(name):
			insecure = append(insecure, note.Path+" turns debug mode on unless "+name+" is set")
		}
	}
	if len(insecure) > 0 {
		findings = append(findings, finding("django_insecure_settings", PreflightWarning,
			"Debug mode or a committed secret key would reach production", strings.Join(insecure, "; "),
			"Debug pages show settings and tracebacks to every visitor, and a committed key lets anyone who can read the repository forge sessions.",
			"Read SECRET_KEY and DEBUG from variables; the dashboard generates the key and sets debug off.", "deploy", "variables"))
	}
	for _, note := range state.note("allowed_hosts_unbound") {
		if state.set(note.Detail) {
			continue
		}
		findings = append(findings, finding(variableFindingCode("allowed_hosts_unbound_", note.Detail), PreflightWarning,
			note.Detail+" is not bound to the domain", "the settings do not say whether they split it on commas or spaces",
			"Django answers 400 to every host it does not allow, the readiness check included.",
			"Set "+note.Detail+" to the domain, localhost and 127.0.0.1, separated the way your settings split it.", "deploy", variableField(note.Detail)))
	}
	for _, note := range state.note(observeDotenvRequired) {
		if state.method == BuildRecipe {
			findings = append(findings, finding("dotenv_file_provided", PreflightPass,
				"An empty .env is created in the image", note.Path+" refuses to start without the file",
				"The loader never overrides variables already set, so the dashboard's values still win.", "", "deploy", "configuration.build"))
		} else {
			findings = append(findings, finding("dotenv_file_required", PreflightWarning,
				"The application exits when .env is missing", note.Path+" treats a missing .env as fatal",
				"A repository Dockerfile has to create the file itself; the dashboard supplies variables, not the file.",
				"Add `RUN touch .env` beside the binary in the Dockerfile, or ignore the load error.", "deploy", "configuration.build"))
		}
		break
	}
	if state.method == BuildRecipe {
		for _, note := range append(state.note("build_fetches_localhost"), state.note(observeBuildLocalhost)...) {
			findings = append(findings, finding("build_fetches_localhost", PreflightWarning,
				"The build fetches from localhost", note.Path+" "+note.Detail,
				"Nothing listens on loopback while the image builds, so a code generator or script that downloads a schema there fails the build.",
				"Commit the generated output, or point the script at an address reachable from the build.", "deploy", "configuration.build.buildCommand"))
			break
		}
	}
	// A Dockerfile reading that found the overlay made it the candidate's
	// release command, which a release task in the image runs and
	// release_command_unmapped asks for (preflight_image.go).
	if overlay := state.note("phoenix_migrate_overlay"); len(overlay) > 0 && state.method == BuildDockerfile && state.candidate.ReleaseCommand == "" {
		findings = append(findings, finding("migrations_not_run", PreflightWarning,
			"Phoenix migrations are never run", overlay[0].Path+" exists and nothing calls it",
			"The release image starts the server only, so the first query meets an empty database.",
			`Set the runtime command to /bin/sh -c "/app/bin/migrate && exec /app/bin/server".`, "deploy", "runtime.command"))
	}
	return findings
}

// databaseFindings compare what the database suggestion said with what is
// linked.
func databaseFindings(state environmentState, configuration PlanConfiguration, observation HostObservation) []PreflightFinding {
	findings := []PreflightFinding{}
	linked := hasDatabaseDependency(configuration.Dependencies)
	for _, database := range state.candidate.Databases {
		value, known := state.value(database.Variable)
		reference := known && strings.HasPrefix(value, "${{database.")
		if database.Hosted != "" && known && (reference || strings.Contains(value, ".jd.internal") || loopbackValue(database.Variable, value)) {
			findings = append(findings, finding("hosted_driver_local_database", PreflightWarning,
				"This app's database driver speaks "+hostedLabel(database.Hosted)+", not "+engineLabel(database.Engine),
				database.Variable+" is linked to a database on this server",
				"The driver sends HTTP or WebSocket requests to the provider's endpoint; a database here never answers them.",
				"Keep the hosted connection string, or switch to a TCP driver (drizzle-orm/node-postgres, @prisma/adapter-pg, ioredis) before linking a local database.",
				"deploy", variableField(database.Variable)))
		}
		if database.Format != "" && known && (strings.Contains(value, "://") || reference) && !formatMatches(database.Format, value) {
			action := "Enter the " + formatLabel(database.Format) + " form of the connection string, or link the database from the variables step, which writes it."
			if reference {
				// The same database in the consumer's shape is one suffix away,
				// and a typed reference can be edited wherever variables are.
				target := strings.TrimSuffix(strings.TrimPrefix(value, "${{database."), "}}")
				action = "Change it to ${{database." + strings.Split(target, ".")[0] + "." + database.Format + "}}, the same database in the " +
					formatLabel(database.Format) + " form."
			}
			findings = append(findings, finding("database_url_format", PreflightWarning,
				database.Variable+" needs a "+formatLabel(database.Format)+" connection string", "the value is a URL",
				"The application parses this variable as "+formatLabel(database.Format)+" and refuses a postgres:// or mysql:// URL.",
				action, "deploy", variableField(database.Variable)))
		}
		if len(database.AlsoVariables) > 0 && linked && (database.Engine == "mysql" || database.Engine == "mariadb") {
			findings = append(findings, finding("rails_multidb_create_denied", PreflightWarning,
				"db:prepare cannot create the cache, queue and cable databases", strings.Join(database.AlsoVariables, ", "),
				"The quick-setup MySQL user may use only its own database, so Rails fails creating the others.",
				"Create them on the server first, or use PostgreSQL, whose quick-setup user can create databases.", "databases", "dependencies"))
		}
		if len(database.Extensions) > 0 {
			for _, dependency := range observation.Dependencies {
				if dependency.ResourceKind != "database_connection" || dependency.Extensions == nil {
					continue
				}
				missing := []string{}
				for _, extension := range database.Extensions {
					if !containsString(dependency.Extensions, extension) {
						missing = append(missing, extension)
					}
				}
				if len(missing) > 0 {
					action := "Link a PostgreSQL started with the " + extensionImage(missing[0]) + " variant, which quick setup offers."
					if missing[0] == "postgis" && !PostGISImageSupported(observation.Architecture) {
						action = "Link a PostgreSQL with PostGIS installed; the PostGIS image quick setup uses is published for x86-64 only."
					}
					findings = append(findings, finding("database_extension_missing", PreflightBlocked,
						"The linked database cannot run this schema", "no "+strings.Join(missing, " or ")+" extension on "+dependency.Status,
						"The schema creates the extension on its first migration, and this server does not ship it.",
						action, "databases", "dependencies"))
				}
			}
		}
		if database.Engine == "mongodb" && mongoUnsupportedCPU(observation) != "" {
			findings = append(findings, finding("database_cpu_unsupported", PreflightWarning,
				"MongoDB 5 and later cannot run on this server's CPU", mongoUnsupportedCPU(observation),
				"mongod exits with an illegal-instruction fault, so the linked application restarts in a loop.",
				"Use MongoDB 4.4, or set the VM's CPU type to host so the instructions are exposed.", "databases", "dependencies"))
		}
	}
	if start := state.candidate.StartCommand; (state.candidate.Framework == "laravel" || state.candidate.Framework == "symfony" ||
		state.candidate.Framework == "rails" || state.candidate.Framework == "phoenix" || state.candidate.Framework == "hanami") &&
		migrationStartRE.MatchString(start) && !linked {
		relational := false
		for _, database := range state.candidate.Databases {
			relational = relational || database.Engine == "postgres" || database.Engine == "mysql" || database.Engine == "mariadb"
		}
		connection, _ := state.value("DB_CONNECTION")
		reachable := state.set("DB_URL") || state.set("DATABASE_URL") || state.set("DB_HOST")
		if relational && !strings.EqualFold(connection, "sqlite") && !reachable {
			findings = append(findings, finding("database_required_for_start", PreflightBlocked,
				"The start command migrates a database that does not exist", start,
				"Migrations run before the server starts, fail to connect, and the container restarts in a loop.",
				"Add the suggested database on the variables step, or set the connection variables for an existing one.", "databases", "dependencies"))
		}
	}
	return findings
}

func formatMatches(format, value string) bool {
	switch format {
	case "jdbc":
		return strings.HasPrefix(value, "jdbc:") || strings.Contains(value, ".jdbc")
	case "jdbc-mariadb":
		return strings.HasPrefix(value, "jdbc:mariadb:") || strings.Contains(value, ".jdbc-mariadb")
	case "adonet":
		return !strings.Contains(value, "://") && !strings.HasPrefix(value, "${{") || strings.Contains(value, ".adonet")
	case "mysql2":
		return strings.HasPrefix(value, "mysql2://") || strings.Contains(value, ".mysql2")
	}
	return true
}

func formatLabel(format string) string {
	return map[string]string{"jdbc": "JDBC", "jdbc-mariadb": "jdbc:mariadb://", "adonet": "ADO.NET", "mysql2": "mysql2://"}[format]
}

func hostedLabel(hosted string) string {
	return map[string]string{
		"neon-http": "Neon's HTTP protocol", "neon-ws": "Neon's WebSocket protocol", "vercel-postgres": "Vercel Postgres's pooled protocol",
		"planetscale-http": "PlanetScale's HTTP protocol", "prisma-accelerate": "Prisma Accelerate's prisma:// protocol",
		"upstash-rest": "Upstash's REST protocol",
	}[hosted]
}

func engineLabel(engine string) string {
	return map[string]string{"postgres": "PostgreSQL", "mysql": "MySQL", "mariadb": "MariaDB", "redis": "Redis", "mongodb": "MongoDB"}[engine]
}

func extensionImage(extension string) string {
	if extension == "postgis" {
		return "PostGIS"
	}
	return "pgvector"
}

// mongoUnsupportedCPU is MongoCPUUnsupported over an observation; empty when
// the features were not observed.
func mongoUnsupportedCPU(observation HostObservation) string {
	if observation.CPUFeatures == nil {
		return ""
	}
	return MongoCPUUnsupported(observation.Architecture, observation.CPUFeatures)
}

// callbackFindings list what third parties must be told about the planned
// domain, and check a Clerk production key against it.
func callbackFindings(state environmentState) []PreflightFinding {
	findings := []PreflightFinding{}
	origin := state.publicOrigin()
	host := strings.TrimPrefix(strings.TrimPrefix(origin, "https://"), "http://")
	registrations := []string{}
	for _, note := range state.note(observeCallback) {
		provider, route, _ := strings.Cut(note.Detail, "|")
		registrations = append(registrations, provider+": "+origin+route)
	}
	for _, note := range state.note(observeStripeWebhook) {
		endpoint := origin + note.Detail
		if note.Detail == "" {
			endpoint = "the route in " + note.Path
		}
		registrations = append(registrations, "Stripe webhook: "+endpoint)
	}
	for _, note := range state.note(observeAuthService) {
		switch note.Detail {
		case "firebase":
			registrations = append(registrations, "Firebase: add "+host+" to Authentication authorized domains")
		case "supabase":
			registrations = append(registrations, "Supabase: set the Site URL to "+origin+" and allow "+origin+"/**")
		case "clerk":
			registrations = append(registrations, "Clerk: add "+host+" as the production domain")
		}
	}
	if len(registrations) > 0 {
		findings = append(findings, finding("external_callback_registration", PreflightWarning,
			"Register this domain with your identity and webhook providers", strings.Join(registrations, "; "),
			"The deploy succeeds either way, but sign-in and webhooks fail until each provider lists the new address.",
			"Add each address in the provider's dashboard.", "deploy", "domains"))
	}
	for _, name := range []string{"NEXT_PUBLIC_CLERK_PUBLISHABLE_KEY", "CLERK_PUBLISHABLE_KEY", "VITE_CLERK_PUBLISHABLE_KEY",
		"PUBLIC_CLERK_PUBLISHABLE_KEY", "EXPO_PUBLIC_CLERK_PUBLISHABLE_KEY"} {
		value, known := state.value(name)
		if !known {
			continue
		}
		keyDomain := strings.ToLower(clerkKeyDomain(value))
		domain, planned := state.primaryDomain()
		if keyDomain == "" || !planned {
			continue
		}
		hostname := strings.ToLower(domain.Hostname)
		switch {
		case hostname == keyDomain || strings.HasSuffix(hostname, "."+keyDomain):
		case registrableDomain(keyDomain) == registrableDomain(hostname):
			// The same site under another name: Clerk serves it only when the
			// instance lists it, which the key cannot say.
			findings = append(findings, finding("clerk_key_domain_mismatch", PreflightWarning,
				"This Clerk production key was issued for "+keyDomain, name+" was issued for "+keyDomain+", not "+hostname,
				"A Clerk production instance serves its own domain and the subdomains it lists, so sign-in fails on "+hostname+" unless it is one.",
				"Check that "+hostname+" is allowed in the Clerk instance, or deploy on "+keyDomain+".", "deploy", variableField(name)))
		default:
			findings = append(findings, finding("clerk_key_domain_mismatch", PreflightBlocked,
				"This Clerk production key belongs to "+registrableDomain(keyDomain), name+" was issued for "+keyDomain,
				"A Clerk production instance serves only its own domain, so sign-in fails on "+hostname+".",
				"Add "+hostname+" to a Clerk production instance and use its key, or deploy on the key's domain.", "deploy", variableField(name)))
		}
	}
	return findings
}

// clerkKeyDomain decodes the frontend API domain a Clerk production
// publishable key carries: pk_live_ followed by base64 of "clerk.<domain>$".
// Development keys work on any domain and decode to nothing here.
func clerkKeyDomain(value string) string {
	if !strings.HasPrefix(value, "pk_live_") {
		return ""
	}
	encoded := strings.TrimPrefix(value, "pk_live_")
	decoded, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(encoded, "="))
	if err != nil {
		return ""
	}
	domain := strings.TrimSuffix(string(decoded), "$")
	if !plannedDomainRE.MatchString(domain) {
		return ""
	}
	return strings.TrimPrefix(domain, "clerk.")
}

// registrableDomain approximates the registrable part of a hostname: the
// last two labels, or three under a two-letter country code whose second
// level is one of the common public suffixes (co.uk, com.au).
func registrableDomain(host string) string {
	labels := strings.Split(strings.ToLower(strings.TrimSuffix(host, ".")), ".")
	if len(labels) < 2 {
		return host
	}
	keep := 2
	second := labels[len(labels)-2]
	if len(labels) >= 3 && len(labels[len(labels)-1]) == 2 &&
		(second == "co" || second == "com" || second == "org" || second == "net" || second == "ac" || second == "gov" || second == "edu") {
		keep = 3
	}
	return strings.Join(labels[len(labels)-keep:], ".")
}
