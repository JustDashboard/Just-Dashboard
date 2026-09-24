package deploy

import (
	"encoding/json"
	"path"
	"regexp"
	"sort"
	"strings"
)

// Language- and framework-specific environment reads. Everything here is a
// bounded regular-expression or line reader over bytes the scanner already
// holds; nothing is evaluated, imported or executed.

var (
	// Python reads beyond the generic call forms: the bare environ imported
	// from os, django-environ's implicit URL readers and dj-database-url.
	pythonImportsEnvironRE = regexp.MustCompile(`(?m)^\s*from\s+os\s+import\s+[^\n]*\benviron\b`)
	pythonBareEnvironRE    = regexp.MustCompile(`(?:^|[^.\w])environ(?:\[\s*|\.get\(\s*)['"]([A-Z][A-Z0-9_]+)['"]`)
	pythonSubscriptReadRE  = regexp.MustCompile(`(?:\bos\.|(?:^|[^.\w]))environ\[\s*['"]([A-Z][A-Z0-9_]+)['"]\s*\]`)
	pythonCallReadRE       = regexp.MustCompile(`\b(env(?:\.(?:str|int|bool|float|list|tuple|dict|json|url|db|db_url|cache|cache_url|email|email_url|search_url|path))?|config)\(\s*['"]([A-Z][A-Z0-9_]+)['"]\s*`)
	pythonKeywordArgRE     = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s*=(?:[^=]|$)`)
	pythonCastArgRE        = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*$`)
	pythonImplicitURLRE    = regexp.MustCompile(`\benv\.(db|db_url|cache|cache_url|email|email_url|search_url)\(\s*\)`)
	pythonDatabaseURLRE    = regexp.MustCompile(`\bdj_database_url\.config\(([^)]*)\)`)
	pythonNamedEnvArgRE    = regexp.MustCompile(`\benv\s*=\s*['"]([A-Z][A-Z0-9_]+)['"]`)
	pythonSettingsClassRE  = regexp.MustCompile(`(?m)^class\s+\w+\s*\(([^)]*)\)\s*:`)
	pythonFieldRE          = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s*:\s*([^=#]+?)\s*(?:=\s*(.+?))?\s*(?:#.*)?$`)
	pythonEnvPrefixRE      = regexp.MustCompile(`env_prefix\s*=\s*['"]([A-Za-z0-9_]*)['"]`)
	pythonAliasRE          = regexp.MustCompile(`\b(?:validation_alias|alias|env)\s*=\s*['"]([A-Za-z_][A-Za-z0-9_]*)['"]`)
	pythonDebugAssignRE    = regexp.MustCompile(`(?m)^(?:DEBUG|app\.config\[['"]DEBUG['"]\]|app\.debug)\s*=\s*(.+)$`)
	pythonSecretLiteralRE  = regexp.MustCompile(`(?m)^SECRET_KEY\s*=\s*[rbuRBU]?['"]([^'"]*)`)
	pythonTruthyDefaultRE  = regexp.MustCompile(`(?i)(?:,\s*(?:default\s*=\s*)?)(?:['"](?:true|1|yes|on)['"]|True\b|1\b)`)
	pythonQuotedNameRE     = regexp.MustCompile(`['"]([A-Z][A-Z0-9_]+)['"]`)
	pythonHostListRE       = regexp.MustCompile(`(?m)^ALLOWED_HOSTS\s*=`)
	pythonSplitSpaceRE     = regexp.MustCompile(`\.split\(\s*(?:\)|['"] ['"]\s*\))`)
	pythonSplitCommaRE     = regexp.MustCompile(`\.split\(\s*['"],['"]\s*\)|\benv\.list\(|\bCsv\(|\bCommaSeparatedStrings\b|\bcast\s*=\s*list\b`)

	// Ruby: ENV.fetch without a default or block raises at the read.
	rubyFetchRE = regexp.MustCompile(`\bENV\.fetch\(\s*['"]([A-Z][A-Z0-9_]+)['"]\s*\)`)

	// Elixir: fetch_env! and `get_env(...) || raise` fail at the read, and a
	// `|| "value"` fallback is a documented default.
	elixirReadRE    = regexp.MustCompile(`System\.(get_env|fetch_env!?)\(\s*"([A-Z][A-Z0-9_]+)"(\s*,\s*"([^"]*)")?\s*\)`)
	elixirRaiseRE   = regexp.MustCompile(`^\s*\|\|\s*raise\b`)
	elixirDefaultRE = regexp.MustCompile(`^\s*\|\|\s*"([^"]*)"`)

	// Rust: std::env and dotenvy reads, the compile-time macros, clap's env
	// attribute, and a dotenv load whose failure ends the process.
	rustEnvVarRE        = regexp.MustCompile(`\b(?:std::)?env::var(?:_os)?\(\s*"([A-Z][A-Z0-9_]+)"\s*\)`)
	rustDotenvVarRE     = regexp.MustCompile(`\bdotenvy?::var\(\s*"([A-Z][A-Z0-9_]+)"`)
	rustEnvMacroRE      = regexp.MustCompile(`\benv!\(\s*"([A-Z][A-Z0-9_]+)"`)
	rustOptionEnvRE     = regexp.MustCompile(`\boption_env!\(\s*"([A-Z][A-Z0-9_]+)"`)
	rustClapEnvRE       = regexp.MustCompile(`#\[(?:arg|clap)\([^\]]*?\benv\s*=\s*"([A-Z][A-Z0-9_]+)"`)
	rustRequiredTailRE  = regexp.MustCompile(`^\s*(?:\.expect\(|\.unwrap\(\)|\?)`)
	rustDotenvFatalRE   = regexp.MustCompile(`(?:\bdotenvy?::dotenv|(?:^|[^:\w])dotenv)\(\)\s*(?:\.expect\(|\.unwrap\(\)|\?)`)
	goDotenvLoadRE      = regexp.MustCompile(`\bgodotenv\.(?:Load|Overload)\(\s*(?:"\.env"\s*)?\)`)
	goDotenvFatalRE     = regexp.MustCompile(`\blog\.Fatal|\bpanic\(|\bos\.Exit\(|\bslog\.Error\(`)
	goStructTagRE       = regexp.MustCompile("`[^`\n]*`")
	goEnvTagRE          = regexp.MustCompile(`\benv:"([A-Z][A-Z0-9_]+)((?:,[A-Za-z]+)*)"`)
	goEnvconfigTagRE    = regexp.MustCompile(`\benvconfig:"([A-Z][A-Z0-9_]+)"`)
	goTagDefaultRE      = regexp.MustCompile(`\b(?:envDefault|default):"([^"]*)"`)
	jvmGetenvMapRE      = regexp.MustCompile(`System\.getenv\(\)\s*\.get(?:OrDefault)?\(\s*"([A-Z][A-Z0-9_]+)"`)
	jvmValueRE          = regexp.MustCompile(`@Value\(\s*"\$\{([A-Z][A-Z0-9_]+)(:[^}]*)?\}`)
	scalaSysEnvRE       = regexp.MustCompile(`\bsys\.env(?:\.get|\.getOrElse)?\(\s*"([A-Z][A-Z0-9_]+)"`)
	clojureGetenvRE     = regexp.MustCompile(`\(System/getenv\s+"([A-Z][A-Z0-9_]+)"`)
	dotnetEnvRE         = regexp.MustCompile(`Environment\.GetEnvironmentVariable\(\s*"([A-Z][A-Z0-9_]+)"`)
	dotnetConfigIndexRE = regexp.MustCompile(`Configuration\[\s*"([A-Za-z][A-Za-z0-9_:]*)"\s*\]`)
	dotnetConnectionRE  = regexp.MustCompile(`GetConnectionString\(\s*"([A-Za-z][A-Za-z0-9_]*)"\s*\)`)
	dartEnvRE           = regexp.MustCompile(`Platform\.environment\[\s*['"]([A-Z][A-Z0-9_]+)['"]\s*\]`)
	swiftEnvRE          = regexp.MustCompile(`(?:\bEnvironment\.get\(\s*"|ProcessInfo\.processInfo\.environment\[\s*")([A-Z][A-Z0-9_]+)"`)
	haskellEnvRE        = regexp.MustCompile(`\b(?:getEnv|lookupEnv)\s+"([A-Z][A-Z0-9_]+)"`)
	gleamEnvRE          = regexp.MustCompile(`\benvoy\.get\(\s*"([A-Z][A-Z0-9_]+)"`)

	// Configuration templates.
	springPlaceholderRE = regexp.MustCompile(`\$\{([A-Z][A-Z0-9_]+)(?::([^}]*))?\}`)
	springKeyValueRE    = regexp.MustCompile(`^([A-Za-z0-9_.%\-]+)\s*[:=]\s*(.*)$`)
	springPlaceholderAt = regexp.MustCompile(`^["']?\$\{([A-Z][A-Z0-9_]+)`)
	hoconPlaceholderRE  = regexp.MustCompile(`\$\{(\?)?([A-Z][A-Z0-9_]+)\}`)
	erbEnvRE            = regexp.MustCompile(`\bENV(?:\.fetch\(|\[)\s*['"]([A-Z][A-Z0-9_]+)['"]`)

	// JavaScript: files whose reads happen while the build runs, SvelteKit's
	// env modules, the bundler blocks that compile values into client code,
	// and the schema-first env declarations of t3, Astro and AdonisJS.
	buildConfigFileRE   = regexp.MustCompile(`^(?:next|vite|astro|svelte|nuxt|remix|react-router|prisma|gatsby-config|gatsby-node|docusaurus|quasar|rsbuild|rspack|webpack|app)\.config\.[cm]?[jt]s$|^gatsby-(?:config|node)\.[cm]?[jt]s$`)
	svelteStaticEnvRE   = regexp.MustCompile(`import\s*\{([^}]*)\}\s*from\s*['"]\$env/static/(private|public)['"]`)
	svelteDynamicEnvRE  = regexp.MustCompile(`['"]\$env/dynamic/(?:private|public)['"]`)
	envMemberRE         = regexp.MustCompile(`\benv\.([A-Z][A-Z0-9_]+)`)
	viteDefineKeyRE     = regexp.MustCompile(`['"](?:process\.env|import\.meta\.env)\.([A-Z][A-Z0-9_]+)['"]\s*:`)
	nextEnvBlockRE      = regexp.MustCompile(`\benv\s*:\s*\{([^{}]*)\}`)
	objectKeyRE         = regexp.MustCompile(`(?m)(?:^|[{,])\s*([A-Z][A-Z0-9_]+)\s*(?::|,|$)`)
	t3SchemaKeyRE       = regexp.MustCompile(`(?m)^\s*([A-Z][A-Z0-9_]+)\s*:\s*z\b(.*)$`)
	astroEnvFieldRE     = regexp.MustCompile(`([A-Z][A-Z0-9_]+)\s*:\s*envField\.\w+\(\s*\{([^}]*)\}`)
	adonisSchemaRE      = regexp.MustCompile(`([A-Z][A-Z0-9_]+)\s*:\s*Env\.schema\.\w+(\.optional)?\(`)
	nuxtRuntimeConfigRE = regexp.MustCompile(`\bruntimeConfig\s*:\s*\{`)
	prismaConfigEnvRE   = regexp.MustCompile(`\benv\(\s*['"]([A-Z][A-Z0-9_]+)['"]\s*\)`)

	// HOST read next to a listen call, or falling back to the any address,
	// is a bind address, not a public name.
	hostBindContextRE = regexp.MustCompile(`\.listen\(|ListenAndServe|\.bind\(|bind_addr|SocketAddr|TcpListener|HttpServer::new|axum::serve|\bserve\(|\.run\(|uvicorn\.run|Kestrel|UseUrls`)
	hostReadRE        = regexp.MustCompile(`['"]HOST['"]|\.HOST\b`)
	anyAddressRE      = regexp.MustCompile(`['"](?:0\.0\.0\.0|::)['"]`)

	// Database drivers that only speak a hosted provider's protocol.
	hostedImportRE = regexp.MustCompile(`(?:from\s+|require\(\s*|import\(\s*)['"](drizzle-orm/neon-http|drizzle-orm/neon-serverless|drizzle-orm/vercel-postgres|drizzle-orm/planetscale-serverless|@neondatabase/serverless|@vercel/postgres|@planetscale/database|@prisma/adapter-neon|@prisma/adapter-planetscale|@upstash/redis|@vercel/kv)['"]`)
	accelerateRE   = regexp.MustCompile(`\bwithAccelerate\(`)
	neonConfigRE   = regexp.MustCompile(`\bneonConfig\.`)

	// Schema capabilities a created database has to provide.
	extensionSQLRE        = regexp.MustCompile(`(?i)create\s+extension\s+(?:if\s+not\s+exists\s+)?["']?(vector|postgis)\b`)
	extensionRailsRE      = regexp.MustCompile(`\benable_extension\s*\(?\s*['"](vector|postgis)['"]`)
	extensionPrismaRE     = regexp.MustCompile(`(?s)extensions\s*=\s*\[([^\]]*)\]`)
	prismaExtensionNameRE = regexp.MustCompile(`\b(vector|postgis)\b`)
	javaScriptKeyRE       = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)
	unsupportedTypeRE     = regexp.MustCompile(`Unsupported\(\s*"(vector|geometry|geography)`)
	drizzleVectorRE       = regexp.MustCompile(`\bvector\(\s*['"][A-Za-z0-9_]+['"]\s*,\s*\{\s*dimensions`)
	pythonVectorRE        = regexp.MustCompile(`\bfrom\s+pgvector[.\s]|\bimport\s+pgvector\b|\bgeoalchemy2\b|django\.contrib\.gis`)

	// External identity providers and webhooks whose allowlists name a domain.
	authProviderImportRE = regexp.MustCompile(`['"](?:next-auth|@auth/core|@auth/[a-z-]+)/providers/([a-z0-9-]+)['"]`)
	betterAuthSocialRE   = regexp.MustCompile(`\bsocialProviders\s*:\s*\{`)
	firebaseAuthRE       = regexp.MustCompile(`['"]firebase/auth['"]`)
	supabaseAuthRE       = regexp.MustCompile(`\.auth\.(?:signInWithOAuth|signInWithOtp|signUp|resetPasswordForEmail)\(`)
	stripeWebhookRE      = regexp.MustCompile(`\.webhooks\.constructEvent(?:Async)?\(`)
	callbackURLRE        = regexp.MustCompile(`\bcallbackURL\s*:\s*['"](/[A-Za-z0-9_/.\-]*)['"]`)
	passportStrategyRE   = regexp.MustCompile(`\bnew\s+(?:[A-Za-z_$][A-Za-z0-9_$]*\.)?([A-Za-z0-9]*?)(?:OAuth2?|OAuth20)?Strategy\s*\(`)
	allauthProviderRE    = regexp.MustCompile(`allauth\.socialaccount\.providers\.([a-z0-9_]+)`)
	codegenLocalhostRE   = regexp.MustCompile(`https?://(?:localhost|127\.0\.0\.1|0\.0\.0\.0)(?::\d+)?[/'"\s]`)
)

// Observation kinds recorded per source file; environmentNotesFor turns the
// ones a root owns into its candidates' EnvironmentNotes.
const (
	observeSecretLiteral    = "secret_key_literal"
	observeDebugLiteral     = "debug_literal"
	observeDebugDefault     = "debug_default_true"
	observeHostList         = "allowed_hosts"
	observeDotenvRequired   = "dotenv_file_required"
	observeHostedDriver     = "hosted_driver"
	observeExtension        = "database_extension"
	observeCallback         = "auth_callback"
	observeAuthService      = "auth_service"
	observeStripeWebhook    = "stripe_webhook"
	observeConnectionName   = "connection_string"
	observeSpringDatasource = "spring_datasource"
	observeBuildLocalhost   = "codegen_localhost"
	// observeCommittedEngine records the engine a committed real env file's
	// URL scheme names — the scheme only, never the value.
	observeCommittedEngine = "committed_engine"
)

// ignoredAuthProviders sign in without a third party holding a callback.
var ignoredAuthProviders = map[string]bool{
	"credentials": true, "email": true, "nodemailer": true, "resend": true, "sendgrid": true,
	"passkey": true, "webauthn": true, "http-email": true, "postmark": true, "mailgun": true,
	"forwardemail": true, "loops": true,
}

type environmentFacts struct {
	// files holds the few manifests and settings files classification reads
	// whole, keyed by path; an empty slice records presence alone.
	files        map[string][]byte
	observations []environmentObservation
	seen         map[string]bool
}

type environmentObservation struct {
	source string
	kind   string
	detail string
}

func (f *environmentFacts) observe(source, kind, detail string) {
	key := source + "\x00" + kind + "\x00" + detail
	if f.seen == nil {
		f.seen = map[string]bool{}
	}
	if f.seen[key] || len(f.observations) >= 512 {
		return
	}
	f.seen[key] = true
	f.observations = append(f.observations, environmentObservation{source: source, kind: kind, detail: detail})
}

// relIs reports whether a slash path is the named file at any depth.
func relIs(rel, suffix string) bool {
	rel = strings.ToLower(rel)
	return rel == suffix || strings.HasSuffix(rel, "/"+suffix)
}

// environmentFactFile names the files read whole for what they say about the
// framework rather than for variable reads: lockfiles and manifests of the
// languages whose markers detection does not hold, Rails credentials and
// settings, and Phoenix's release overlay.
func environmentFactFile(rel, name string) bool {
	switch name {
	case "gemfile.lock", "gemfile", "mix.exs", "pubspec.yaml", "package.swift":
		return true
	}
	for _, suffix := range []string{
		"config/credentials.yml.enc", "config/credentials/production.yml.enc", "config/master.key",
		"config/application.rb", "config/puma.rb", "config/environments/production.rb", "config/database.yml",
		"rel/overlays/bin/migrate",
	} {
		if relIs(rel, suffix) {
			return true
		}
	}
	return strings.HasPrefix(name, "codegen.") && (strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".json"))
}

// envConfigKind names configuration templates read with their own grammar.
func envConfigKind(rel, name string) string {
	extension := path.Ext(name)
	switch {
	case strings.HasPrefix(name, "appsettings") && extension == ".json":
		return "appsettings"
	case strings.HasPrefix(name, "application") && (extension == ".properties" || extension == ".yml" || extension == ".yaml") &&
		(strings.Contains("/"+strings.ToLower(rel), "/src/main/resources/") || path.Base(path.Dir(rel)) == "config" || path.Dir(rel) == "."):
		return "spring"
	case name == "application.conf":
		return "hocon"
	case (extension == ".yml" || extension == ".yaml") && path.Base(path.Dir(rel)) == "config":
		return "erb"
	}
	return ""
}

// observeFacts keeps the fact files whole and records what a source says
// beyond its reads.
func (s *envScanner) observeFacts(rel, name string, content []byte) {
	if environmentFactFile(rel, name) {
		if relIs(rel, "config/credentials.yml.enc") || relIs(rel, "config/credentials/production.yml.enc") ||
			relIs(rel, "config/master.key") || relIs(rel, "rel/overlays/bin/migrate") {
			// Presence is the whole fact; the encrypted or secret bytes are
			// never kept.
			s.facts.files[rel] = []byte{}
		} else {
			s.facts.files[rel] = content
		}
		if strings.HasPrefix(name, "codegen.") && codegenLocalhostRE.Match(content) {
			s.facts.observe(rel, observeBuildLocalhost, "")
		}
	}
}

// observeSchema reads a Prisma schema or a committed SQL migration for the
// extensions the database must offer.
func (s *envScanner) observeSchema(rel string, content []byte) {
	for _, match := range extensionSQLRE.FindAllSubmatch(content, -1) {
		s.facts.observe(rel, observeExtension, strings.ToLower(string(match[1])))
	}
	if match := extensionPrismaRE.FindSubmatch(content); match != nil {
		for _, extension := range prismaExtensionNameRE.FindAllSubmatch(match[1], -1) {
			s.facts.observe(rel, observeExtension, string(extension[1]))
		}
	}
	for _, match := range unsupportedTypeRE.FindAllSubmatch(content, -1) {
		extension := "postgis"
		if string(match[1]) == "vector" {
			extension = "vector"
		}
		s.facts.observe(rel, observeExtension, extension)
	}
}

// scanConfig reads configuration templates: Spring and Quarkus placeholders,
// HOCON substitutions, Rails ERB in config YAML, and .NET appsettings.
func (s *envScanner) scanConfig(kind, rel string, content []byte) {
	switch kind {
	case "spring":
		// A profile nobody activates in production — application-dev.yml,
		// application-test.properties — says nothing about this deployment,
		// and one that may be (staging, docker) is listed but decides no
		// requiredness: only the base file and prod* profiles always load.
		file := strings.ToLower(path.Base(rel))
		profile := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSuffix(file, path.Ext(file)), "application"), "-")
		if springDevProfile(profile) {
			return
		}
		always := profile == "" || strings.HasPrefix(profile, "prod")
		for _, match := range springPlaceholderRE.FindAllSubmatchIndex(content, -1) {
			name := string(content[match[2]:match[3]])
			if match[4] < 0 {
				flags := envReadFlags(0)
				if always {
					flags = envReadRequired
				}
				s.recordRead(name, rel, "", flags)
				continue
			}
			value := string(content[match[4]:match[5]])
			flags := envReadFlags(0)
			if loopbackValue(name, value) {
				flags |= envReadLocalhost
			}
			s.recordRead(name, rel, "", flags)
			if !loopbackValue(name, value) {
				s.recordDefault(name, rel, value)
			}
		}
		for _, variable := range springDatasourceVariables(path.Ext(file) == ".properties", content) {
			s.facts.observe(rel, observeSpringDatasource, variable)
		}
	case "hocon":
		for _, match := range hoconPlaceholderRE.FindAllSubmatch(content, -1) {
			flags := envReadFlags(0)
			if len(match[1]) == 0 {
				flags = envReadRequired
			}
			s.recordRead(string(match[2]), rel, "", flags)
		}
	case "erb":
		if !strings.Contains(string(content), "<%") {
			return
		}
		for _, match := range erbEnvRE.FindAllSubmatch(content, -1) {
			s.record(string(match[1]), rel, "")
		}
		s.scanRubyFetch(rel, content, true)
	case "appsettings":
		s.scanAppsettings(rel, content)
	}
}

// springDevProfile names the Spring profiles only a developer's machine or
// the test suite activates.
func springDevProfile(profile string) bool {
	for _, prefix := range []string{"dev", "local", "test", "ci"} {
		if strings.HasPrefix(profile, prefix) {
			return true
		}
	}
	return false
}

// springDatasourceKeys are the full keys a JVM framework reads its JDBC URL
// from; a `url:` anywhere else — a webhook's, a mail server's — is not one.
var springDatasourceKeys = map[string]bool{
	"spring.datasource.url": true, "spring.datasource.jdbc-url": true, "spring.datasource.jdbcurl": true,
	"spring.datasource.hikari.jdbc-url": true, "spring.datasource.hikari.jdbcurl": true,
	"quarkus.datasource.jdbc.url": true, "datasources.default.url": true,
}

// springDatasourceVariables reads the variables a datasource URL key is
// bound to, in file order: a properties file's full keys, or a YAML file's
// nested ones by indentation. Quarkus's %prod. prefix is the production
// profile; its %dev. and %test. keys are skipped.
func springDatasourceVariables(properties bool, content []byte) []string {
	type level struct {
		indent int
		key    string
	}
	found := []string{}
	stack := []level{}
	for index, line := range strings.Split(string(content), "\n") {
		if index > 4000 {
			break
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			stack = stack[:0]
			continue
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "!") || strings.HasPrefix(trimmed, "-") {
			continue
		}
		match := springKeyValueRE.FindStringSubmatch(trimmed)
		if match == nil {
			continue
		}
		key, value := strings.ToLower(match[1]), strings.TrimSpace(match[2])
		if !properties {
			indent := len(line) - len(strings.TrimLeft(line, " "))
			for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
				stack = stack[:len(stack)-1]
			}
			if value == "" {
				stack = append(stack, level{indent: indent, key: key})
				continue
			}
			parents := make([]string, 0, len(stack)+1)
			for _, parent := range stack {
				parents = append(parents, parent.key)
			}
			key = strings.Join(append(parents, key), ".")
		}
		if strings.HasPrefix(key, "%") {
			profile, rest, _ := strings.Cut(key, ".")
			if profile != "%prod" {
				continue
			}
			key = rest
		}
		if placeholder := springPlaceholderAt.FindStringSubmatch(value); placeholder != nil && springDatasourceKeys[key] {
			found = append(found, placeholder[1])
		}
	}
	return found
}

// scanAppsettings lists .NET configuration an image expects from the
// environment: every connection string, and every empty leaf, as the
// double-underscore names the environment provider maps onto them.
func (s *envScanner) scanAppsettings(rel string, content []byte) {
	var document map[string]any
	if json.Unmarshal(stripJSONComments(content), &document) != nil {
		return
	}
	production := strings.EqualFold(path.Base(rel), "appsettings.json") || strings.EqualFold(path.Base(rel), "appsettings.Production.json")
	var walk func(prefix []string, value any, depth int)
	walk = func(prefix []string, value any, depth int) {
		if depth > 6 {
			return
		}
		switch typed := value.(type) {
		case map[string]any:
			keys := make([]string, 0, len(typed))
			for key := range typed {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				if depth == 0 && (key == "Logging" || key == "AllowedHosts") {
					continue
				}
				walk(append(append([]string(nil), prefix...), key), typed[key], depth+1)
			}
		case string:
			if len(prefix) == 0 {
				return
			}
			connection := len(prefix) == 2 && strings.EqualFold(prefix[0], "ConnectionStrings")
			if !connection && typed != "" {
				return
			}
			name := strings.ToUpper(strings.Join(prefix, "__"))
			name = strings.NewReplacer("-", "_", ".", "_").Replace(name)
			flags := envReadFlags(0)
			if production && typed != "" && loopbackValue(name, typed) {
				flags |= envReadLocalhost
			}
			s.recordRead(name, rel, "", flags)
			if connection {
				s.facts.observe(rel, observeConnectionName, prefix[1])
			}
		}
	}
	walk(nil, document, 0)
}

// stripJSONComments removes // and /* */ comments outside strings, which
// .NET's configuration reader accepts and encoding/json does not.
func stripJSONComments(content []byte) []byte {
	out := make([]byte, 0, len(content))
	inString, escaped := false, false
	for index := 0; index < len(content); index++ {
		c := content[index]
		if inString {
			out = append(out, c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			out = append(out, c)
			continue
		}
		if c == '/' && index+1 < len(content) && content[index+1] == '/' {
			for index < len(content) && content[index] != '\n' {
				index++
			}
			out = append(out, '\n')
			continue
		}
		if c == '/' && index+1 < len(content) && content[index+1] == '*' {
			end := strings.Index(string(content[index+2:]), "*/")
			if end < 0 {
				break
			}
			index += end + 3
			continue
		}
		out = append(out, c)
	}
	return out
}

// scanLanguage applies the reads of one source file's own language.
func (s *envScanner) scanLanguage(extension, rel, name string, content []byte) {
	switch extension {
	case ".py":
		s.scanPython(rel, name, content)
	case ".rb", ".cr":
		s.scanRubyFetch(rel, content, strings.HasPrefix(rel, "config/") || strings.Contains(rel, "/config/"))
	case ".ex", ".exs":
		s.scanElixir(rel, content)
	case ".rs":
		s.scanRust(rel, name, content)
	case ".go":
		s.scanGo(rel, content)
	case ".java", ".kt", ".kts", ".scala", ".clj":
		for _, expression := range []*regexp.Regexp{jvmGetenvMapRE, scalaSysEnvRE, clojureGetenvRE} {
			for _, match := range expression.FindAllSubmatch(content, -1) {
				s.record(string(match[1]), rel, "")
			}
		}
		for _, match := range jvmValueRE.FindAllSubmatch(content, -1) {
			if len(match[2]) == 0 {
				s.recordRead(string(match[1]), rel, "", envReadRequired)
				continue
			}
			s.record(string(match[1]), rel, "")
			s.recordDefault(string(match[1]), rel, string(match[2][1:]))
		}
	case ".cs", ".fs":
		for _, match := range dotnetEnvRE.FindAllSubmatch(content, -1) {
			s.record(string(match[1]), rel, "")
		}
		for _, match := range dotnetConfigIndexRE.FindAllSubmatch(content, -1) {
			s.record(strings.ToUpper(strings.ReplaceAll(string(match[1]), ":", "__")), rel, "")
		}
		for _, match := range dotnetConnectionRE.FindAllSubmatch(content, -1) {
			s.record("CONNECTIONSTRINGS__"+strings.ToUpper(string(match[1])), rel, "")
			s.facts.observe(rel, observeConnectionName, string(match[1]))
		}
	case ".dart":
		s.recordAll(dartEnvRE, rel, content)
	case ".swift":
		s.recordAll(swiftEnvRE, rel, content)
	case ".hs":
		s.recordAll(haskellEnvRE, rel, content)
	case ".gleam":
		s.recordAll(gleamEnvRE, rel, content)
	}
	if envJavaScriptSource[extension] {
		s.scanJavaScript(rel, name, content)
	}
	s.observeCommon(rel, content)
	if _, reads := s.flags["HOST"][rel]; reads && hostReadAsBind(content) {
		s.flags["HOST"][rel] |= envReadBindHost
	}
}

// hostReadAsBind reports a HOST read that is the address a server listens
// on: within a few lines of a listen or bind call, or falling back to the
// any address. Reading PORT beside it proves nothing — links and callbacks
// are built from both.
func hostReadAsBind(content []byte) bool {
	lines := strings.Split(string(content), "\n")
	for index, line := range lines {
		if !hostReadRE.MatchString(line) {
			continue
		}
		if anyAddressRE.MatchString(line) ||
			hostBindContextRE.MatchString(strings.Join(lines[max(0, index-6):min(len(lines), index+7)], "\n")) {
			return true
		}
	}
	return false
}

func (s *envScanner) recordAll(expression *regexp.Regexp, rel string, content []byte) {
	for _, match := range expression.FindAllSubmatch(content, -1) {
		s.record(string(match[1]), rel, "")
	}
}

// observeCommon records what any source says about the database it needs.
func (s *envScanner) observeCommon(rel string, content []byte) {
	for _, expression := range []*regexp.Regexp{extensionSQLRE, extensionRailsRE} {
		for _, match := range expression.FindAllSubmatch(content, -1) {
			s.facts.observe(rel, observeExtension, strings.ToLower(string(match[1])))
		}
	}
	if drizzleVectorRE.Match(content) {
		s.facts.observe(rel, observeExtension, "vector")
	}
}

// pythonImportTimeModule names the modules a Python application executes as
// it starts: its settings and configuration, and the entry files a server is
// pointed at.
func pythonImportTimeModule(rel string) bool {
	switch path.Base(rel) {
	case "settings.py", "config.py", "conf.py", "configuration.py", "main.py", "app.py", "wsgi.py", "asgi.py",
		"server.py", "application.py", "__init__.py", "run.py", "api.py", "index.py":
		return true
	}
	for _, segment := range strings.Split(path.Dir(rel), "/") {
		if segment == "settings" || segment == "config" || segment == "conf" {
			return true
		}
	}
	return false
}

// pythonDefBodies reports, per line, whether the line sits inside a function
// body, where a read runs only when the function is called.
func pythonDefBodies(lines []string) []bool {
	inside := make([]bool, len(lines))
	stack := []int{}
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			inside[index] = len(stack) > 0
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		for len(stack) > 0 && indent <= stack[len(stack)-1] {
			stack = stack[:len(stack)-1]
		}
		inside[index] = len(stack) > 0
		if strings.HasPrefix(trimmed, "def ") || strings.HasPrefix(trimmed, "async def ") {
			stack = append(stack, indent)
		}
	}
	return inside
}

func lineIndex(content []byte, offset int) int {
	return strings.Count(string(content[:offset]), "\n")
}

// callArguments splits a call's remaining arguments, from just after the
// first one up to its closing parenthesis, at their top-level commas —
// skipping nested brackets and string literals — bounded.
func callArguments(content []byte, offset int) []string {
	arguments := []string{}
	depth, start := 1, offset
	var quote byte
	end := min(len(content), offset+400)
	for index := offset; index < end; index++ {
		c := content[index]
		if quote != 0 {
			if c == '\\' {
				index++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
			if depth == 0 {
				return append(arguments, strings.TrimSpace(string(content[start:index])))
			}
		case ',':
			if depth == 1 {
				arguments = append(arguments, strings.TrimSpace(string(content[start:index])))
				start = index + 1
			}
		}
	}
	return append(arguments, strings.TrimSpace(string(content[start:end])))
}

// pythonReadHasDefault reports whether an env()/config() read after its
// name carries a default. django-environ's typed readers (env.bool, env.int…)
// and python-decouple's config() take the default as the second positional
// argument; django-environ's bare env(), its list/tuple/dict readers and
// Starlette's config() take a cast there and the default after it. A type
// name in that position is a cast, anything else — a literal, a call — is a
// default, which also covers environs, whose bare env() is its str().
func pythonReadHasDefault(callee string, arguments []string) bool {
	positional := []string{}
	for _, argument := range arguments {
		if argument == "" {
			continue
		}
		if keyword := pythonKeywordArgRE.FindStringSubmatch(argument); keyword != nil {
			if keyword[1] == "default" {
				return true
			}
			continue
		}
		positional = append(positional, argument)
	}
	if len(positional) == 0 {
		return false
	}
	switch callee {
	case "env", "config", "env.list", "env.tuple", "env.dict":
		first := positional[0]
		return len(positional) > 1 || !pythonCastArgRE.MatchString(first) || first == "True" || first == "False" || first == "None"
	}
	return true
}

func (s *envScanner) scanPython(rel, name string, content []byte) {
	lines := strings.Split(string(content), "\n")
	inside := pythonDefBodies(lines)
	importTime := pythonImportTimeModule(rel)
	requiredFlag := func(offset int) envReadFlags {
		if importTime && !inside[min(lineIndex(content, offset), len(inside)-1)] {
			return envReadRequired
		}
		return envReadRequiredForm
	}
	if pythonImportsEnvironRE.Match(content) {
		s.recordAll(pythonBareEnvironRE, rel, content)
	}
	bare := pythonImportsEnvironRE.Match(content)
	for _, match := range pythonSubscriptReadRE.FindAllSubmatchIndex(content, -1) {
		text := string(content[match[0]:match[1]])
		if !strings.Contains(text, "os.") && !bare {
			continue
		}
		rest := strings.TrimLeft(string(content[match[1]:min(len(content), match[1]+4)]), " \t")
		if strings.HasPrefix(rest, "=") && !strings.HasPrefix(rest, "==") {
			// An assignment sets the variable for later reads; it is not one.
			continue
		}
		s.recordRead(string(content[match[2]:match[3]]), rel, "", requiredFlag(match[0]))
	}
	for _, match := range pythonCallReadRE.FindAllSubmatchIndex(content, -1) {
		rest := string(content[match[1]:min(len(content), match[1]+1)])
		required := rest == ")"
		if rest == "," {
			required = !pythonReadHasDefault(string(content[match[2]:match[3]]), callArguments(content, match[1]+1))
		}
		if required {
			s.recordRead(string(content[match[4]:match[5]]), rel, "", requiredFlag(match[0]))
		}
	}
	implicit := map[string]string{"db": "DATABASE_URL", "db_url": "DATABASE_URL", "cache": "CACHE_URL", "cache_url": "CACHE_URL",
		"email": "EMAIL_URL", "email_url": "EMAIL_URL", "search_url": "SEARCH_URL"}
	for _, match := range pythonImplicitURLRE.FindAllSubmatchIndex(content, -1) {
		s.recordRead(implicit[string(content[match[2]:match[3]])], rel, "", requiredFlag(match[0]))
	}
	for _, match := range pythonDatabaseURLRE.FindAllSubmatchIndex(content, -1) {
		arguments := string(content[match[2]:match[3]])
		variable := "DATABASE_URL"
		if named := pythonNamedEnvArgRE.FindStringSubmatch(arguments); named != nil {
			variable = named[1]
		}
		flags := envReadFlags(0)
		if !strings.Contains(arguments, "default") {
			flags = requiredFlag(match[0])
		}
		s.recordRead(variable, rel, "", flags)
	}
	s.scanPythonSettingsClasses(rel, lines)
	s.scanPythonDebug(rel, name, content)
	s.scanPythonHostList(rel, name, content)
	if match := pythonSecretLiteralRE.FindSubmatch(content); match != nil && (name == "settings.py" || strings.Contains(rel, "settings/") || name == "config.py") &&
		!pythonDevSettingsModule(rel) {
		detail := "literal"
		if strings.HasPrefix(string(match[1]), "django-insecure-") {
			detail = "django-insecure"
		}
		s.facts.observe(rel, observeSecretLiteral, detail)
	}
	if pythonVectorRE.Match(content) {
		switch {
		case strings.Contains(string(content), "pgvector"):
			s.facts.observe(rel, observeExtension, "vector")
		default:
			s.facts.observe(rel, observeExtension, "postgis")
		}
	}
	for _, match := range allauthProviderRE.FindAllSubmatch(content, -1) {
		s.facts.observe(rel, observeCallback, "django-allauth|"+string(match[1])+"|/accounts/"+string(match[1])+"/login/callback/")
	}
	if stripeWebhookRE.Match(content) {
		s.facts.observe(rel, observeStripeWebhook, "")
	}
}

// scanPythonSettingsClasses reads pydantic-settings classes: every annotated
// field is a variable, upper-cased (the reader is case-insensitive), behind
// the class's env_prefix unless an alias names it, and required when it has
// no default — the class is instantiated as the application starts.
func (s *envScanner) scanPythonSettingsClasses(rel string, lines []string) {
	content := strings.Join(lines, "\n")
	if !strings.Contains(content, "BaseSettings") {
		return
	}
	for _, match := range pythonSettingsClassRE.FindAllStringSubmatchIndex(content, -1) {
		if !strings.Contains(content[match[2]:match[3]], "BaseSettings") {
			continue
		}
		start := lineIndex([]byte(content), match[0]) + 1
		body := []string{}
		indent := -1
		for index := start; index < len(lines) && index < start+200; index++ {
			line := lines[index]
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			current := len(line) - len(strings.TrimLeft(line, " \t"))
			if indent < 0 {
				indent = current
			}
			if current < indent || current == 0 {
				break
			}
			body = append(body, line)
		}
		text := strings.Join(body, "\n")
		prefix := ""
		if found := pythonEnvPrefixRE.FindStringSubmatch(text); found != nil {
			prefix = strings.ToUpper(found[1])
		}
		for _, line := range body {
			if len(line)-len(strings.TrimLeft(line, " \t")) != indent {
				continue
			}
			field := pythonFieldRE.FindStringSubmatch(strings.TrimSpace(line))
			if field == nil || strings.HasPrefix(field[1], "_") || field[1] == "model_config" || strings.Contains(field[2], "ClassVar") {
				continue
			}
			name := prefix + strings.ToUpper(field[1])
			if alias := pythonAliasRE.FindStringSubmatch(field[3]); alias != nil {
				name = strings.ToUpper(alias[1])
			}
			defaultText := strings.TrimSpace(field[3])
			required := defaultText == "" ||
				(strings.HasPrefix(defaultText, "Field(") && strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(defaultText, "Field(")), "...") && !strings.Contains(defaultText, "default"))
			flags := envReadFlags(0)
			if required {
				flags = envReadRequired
			}
			s.recordRead(name, rel, "", flags)
			if !required && (strings.HasPrefix(defaultText, `"`) || strings.HasPrefix(defaultText, `'`)) {
				if value := unquotedEnvValue(defaultText); !loopbackValue(name, value) {
					s.recordDefault(name, rel, value)
				}
			}
		}
	}
}

// pythonDevSettingsModule names the settings modules only a developer's
// machine or the test suite loads — config/settings/local.py, settings_dev.py,
// test.py — where debug on and a literal key are the point, not a leak.
func pythonDevSettingsModule(rel string) bool {
	for _, part := range strings.Split(strings.TrimSuffix(path.Base(rel), ".py"), "_") {
		switch part {
		case "local", "dev", "develop", "development", "test", "tests", "testing", "ci":
			return true
		}
	}
	return false
}

// pythonStatement returns an assignment's right-hand side from offset, with
// the continuation lines a bracketed or chained expression runs onto, bounded.
func pythonStatement(content []byte, offset int) string {
	lines := strings.Split(string(content[offset:min(len(content), offset+600)]), "\n")
	statement := lines[0]
	for _, line := range lines[1:min(len(lines), 8)] {
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == line && !strings.HasPrefix(trimmed, ")") && !strings.HasPrefix(trimmed, "]") && !strings.HasPrefix(trimmed, ".") {
			break
		}
		statement += " " + strings.TrimSpace(line)
	}
	return statement
}

// scanPythonDebug reads how Django or Flask turn debug mode on: a literal
// True, or an environment read whose own default is truthy — which ships
// tracebacks to every visitor unless that variable is set. The observation
// carries the variable the expression reads and the value that turns debug
// off for the way the default is parsed — nothing when no value can (bool()
// of any non-empty string is True).
func (s *envScanner) scanPythonDebug(rel, name string, content []byte) {
	if pythonDevSettingsModule(rel) {
		return
	}
	for _, match := range pythonDebugAssignRE.FindAllSubmatch(content, -1) {
		expression := strings.TrimSpace(string(match[1]))
		variable := ""
		for _, read := range pythonQuotedNameRE.FindAllStringSubmatch(expression, -1) {
			if strings.Contains(read[1], "DEBUG") {
				variable = read[1]
				break
			}
		}
		reads := variable != "" &&
			(strings.Contains(expression, "environ") || strings.Contains(expression, "getenv") ||
				strings.Contains(expression, "env(") || strings.Contains(expression, "env.bool(") || strings.Contains(expression, "config("))
		switch {
		case !reads && (expression == "True" || strings.HasPrefix(expression, "True ")):
			if name == "settings.py" || strings.Contains(rel, "settings/") {
				s.facts.observe(rel, observeDebugLiteral, "")
			}
		case reads && pythonTruthyDefaultRE.MatchString(expression):
			seed := "False"
			switch {
			case strings.Contains(expression, "int("):
				seed = "0"
			case strings.HasPrefix(expression, "bool(") && !strings.Contains(expression, "==") && !strings.Contains(expression, " in "):
				seed = ""
			}
			s.facts.observe(rel, observeDebugDefault, variable+"|"+seed)
		}
	}
}

// scanPythonHostList records which variable Django's settings read
// ALLOWED_HOSTS from and how they split it: a comma list (env.list,
// .split(",")) and a space list (.split(" "), .split()) need their own
// rendering of the planned domain, and a value in the wrong one is a single
// host Django never matches, so every request, readiness included, is a 400.
// The separator stays empty when the expression does not say.
func (s *envScanner) scanPythonHostList(rel, name string, content []byte) {
	if !(name == "settings.py" || strings.Contains(rel, "settings/")) || pythonDevSettingsModule(rel) {
		return
	}
	for _, match := range pythonHostListRE.FindAllIndex(content, -1) {
		expression := pythonStatement(content, match[1])
		read := pythonQuotedNameRE.FindStringSubmatch(expression)
		if read == nil {
			continue
		}
		separator := ""
		switch {
		case pythonSplitSpaceRE.MatchString(expression):
			separator = "space"
		case pythonSplitCommaRE.MatchString(expression):
			separator = "comma"
		}
		s.facts.observe(rel, observeHostList, read[1]+"|"+separator)
	}
}

func (s *envScanner) scanRubyFetch(rel string, content []byte, bootTime bool) {
	for _, match := range rubyFetchRE.FindAllSubmatchIndex(content, -1) {
		rest := strings.TrimLeft(string(content[match[1]:min(len(content), match[1]+8)]), " \t")
		if strings.HasPrefix(rest, "{") || strings.HasPrefix(rest, "do") {
			continue
		}
		flags := envReadRequiredForm
		if bootTime {
			flags = envReadRequired
		}
		s.recordRead(string(content[match[2]:match[3]]), rel, "", flags)
	}
}

func (s *envScanner) scanElixir(rel string, content []byte) {
	config := strings.HasPrefix(rel, "config/") || strings.Contains(rel, "/config/")
	if config && (path.Base(rel) == "dev.exs" || path.Base(rel) == "test.exs") {
		// Mix loads these only in its dev and test environments; a release
		// never reads them.
		return
	}
	for _, match := range elixirReadRE.FindAllSubmatchIndex(content, -1) {
		function := string(content[match[2]:match[3]])
		name := string(content[match[4]:match[5]])
		rest := string(content[match[1]:min(len(content), match[1]+200)])
		flags := envReadFlags(0)
		if function == "fetch_env!" || elixirRaiseRE.MatchString(rest) {
			flags = envReadRequiredForm
			if config {
				flags = envReadRequired
			}
		}
		s.recordRead(name, rel, "", flags)
		switch {
		case match[8] >= 0:
			s.recordDefault(name, rel, string(content[match[8]:match[9]]))
		default:
			if found := elixirDefaultRE.FindStringSubmatch(rest); found != nil {
				s.recordDefault(name, rel, found[1])
			}
		}
	}
}

func (s *envScanner) scanRust(rel, name string, content []byte) {
	for _, match := range rustEnvVarRE.FindAllSubmatchIndex(content, -1) {
		flags := envReadFlags(0)
		if rustRequiredTailRE.Match(content[match[1]:min(len(content), match[1]+16)]) {
			flags = envReadRequiredForm
		}
		s.recordRead(string(content[match[2]:match[3]]), rel, "", flags)
	}
	s.recordAll(rustDotenvVarRE, rel, content)
	s.recordAll(rustClapEnvRE, rel, content)
	for _, match := range rustEnvMacroRE.FindAllSubmatch(content, -1) {
		// Compiled in: the build fails without it, and the runtime never reads it.
		s.recordRead(string(match[1]), rel, "", envReadBuild|envReadRequired)
	}
	for _, match := range rustOptionEnvRE.FindAllSubmatch(content, -1) {
		s.recordRead(string(match[1]), rel, "", envReadBuild)
	}
	if (name == "main.rs" || strings.Contains(rel, "src/bin/")) && rustDotenvFatalRE.Match(content) {
		s.facts.observe(rel, observeDotenvRequired, "dotenvy")
	}
}

func (s *envScanner) scanGo(rel string, content []byte) {
	for _, tag := range goStructTagRE.FindAll(content, -1) {
		defaults := goTagDefaultRE.FindSubmatch(tag)
		if match := goEnvTagRE.FindSubmatch(tag); match != nil {
			flags := envReadFlags(0)
			if strings.Contains(string(match[2]), "required") || strings.Contains(string(match[2]), "notEmpty") {
				flags = envReadRequired
			}
			s.recordRead(string(match[1]), rel, "", flags)
			if defaults != nil {
				s.recordDefault(string(match[1]), rel, string(defaults[1]))
			}
		}
		if match := goEnvconfigTagRE.FindSubmatch(tag); match != nil {
			flags := envReadFlags(0)
			if strings.Contains(string(tag), `required:"true"`) {
				flags = envReadRequired
			}
			s.recordRead(string(match[1]), rel, "", flags)
			if defaults != nil {
				s.recordDefault(string(match[1]), rel, string(defaults[1]))
			}
		}
	}
	if strings.HasPrefix(rel, "testdata/") || strings.Contains(rel, "/testdata/") {
		// The recipe's own search skips what `go build` skips.
		return
	}
	for _, match := range goDotenvLoadRE.FindAllIndex(content, -1) {
		if goDotenvFatalRE.Match(content[match[1]:min(len(content), match[1]+240)]) {
			s.facts.observe(rel, observeDotenvRequired, "godotenv")
			break
		}
	}
}

// scanJavaScript reads what JavaScript frameworks decide at build time.
func (s *envScanner) scanJavaScript(rel, name string, content []byte) {
	text := string(content)
	if buildConfigFileRE.MatchString(name) {
		// Everything a framework config reads is read while the build runs.
		for variable, flags := range s.namesReadIn(rel) {
			s.flags[variable][rel] = flags | envReadBuild
		}
		if strings.HasPrefix(name, "vite.config.") {
			for _, match := range viteDefineKeyRE.FindAllStringSubmatch(text, -1) {
				s.recordRead(match[1], rel, "", envReadBuild|envReadInlined)
			}
			if strings.Contains(text, "loadEnv(") {
				for _, match := range envMemberRE.FindAllStringSubmatch(text, -1) {
					s.recordRead(match[1], rel, "", envReadBuild)
				}
			}
		}
		if strings.HasPrefix(name, "next.config.") {
			for _, block := range nextEnvBlockRE.FindAllStringSubmatch(text, -1) {
				for _, key := range objectKeyRE.FindAllStringSubmatch(block[1], -1) {
					s.recordRead(key[1], rel, "", envReadBuild|envReadInlined)
				}
			}
		}
		if strings.HasPrefix(name, "nuxt.config.") {
			s.scanNuxtRuntimeConfig(rel, text)
		}
		// Prisma's config helper throws on a missing variable while the
		// config loads, which `prisma generate` does during the build.
		if strings.HasPrefix(name, "prisma.config.") && strings.Contains(text, "prisma/config") {
			for _, match := range prismaConfigEnvRE.FindAllStringSubmatch(text, -1) {
				s.recordRead(match[1], rel, "", envReadBuild|envReadRequired)
			}
		}
		if strings.HasPrefix(name, "astro.config.") {
			for _, match := range astroEnvFieldRE.FindAllStringSubmatch(text, -1) {
				options := match[2]
				flags := envReadFlags(0)
				if !strings.Contains(options, "optional: true") && !strings.Contains(options, "default:") {
					flags |= envReadRequired
				}
				if strings.Contains(options, `"client"`) || strings.Contains(options, `'client'`) {
					flags |= envReadBuild | envReadInlined
				}
				s.recordRead(match[1], rel, "", flags)
			}
		}
	}
	for _, match := range svelteStaticEnvRE.FindAllStringSubmatch(text, -1) {
		flags := envReadBuild | envReadRequired
		if match[2] == "public" {
			flags |= envReadInlined
		}
		for _, item := range strings.Split(match[1], ",") {
			field := strings.Fields(strings.TrimSpace(item))
			if len(field) > 0 {
				s.recordRead(field[0], rel, "", flags)
			}
		}
	}
	if svelteDynamicEnvRE.MatchString(text) {
		for _, match := range envMemberRE.FindAllStringSubmatch(text, -1) {
			s.record(match[1], rel, "")
		}
	}
	if strings.Contains(text, "createEnv(") {
		for _, match := range t3SchemaKeyRE.FindAllStringSubmatch(text, -1) {
			flags := envReadBuild
			if !strings.Contains(match[2], ".optional()") && !strings.Contains(match[2], ".default(") {
				flags |= envReadRequired
			}
			s.recordRead(match[1], rel, "", flags)
		}
	}
	if strings.Contains(text, "Env.schema.") {
		for _, match := range adonisSchemaRE.FindAllStringSubmatch(text, -1) {
			flags := envReadRequired
			if match[2] != "" {
				flags = 0
			}
			s.recordRead(match[1], rel, "", flags)
		}
	}
	for _, match := range hostedImportRE.FindAllStringSubmatch(text, -1) {
		s.facts.observe(rel, observeHostedDriver, hostedDriverForImport(match[1]))
	}
	if accelerateRE.MatchString(text) {
		s.facts.observe(rel, observeHostedDriver, "prisma-accelerate")
	}
	if neonConfigRE.MatchString(text) {
		s.facts.observe(rel, observeHostedDriver, "neon-ws")
	}
	for _, match := range authProviderImportRE.FindAllStringSubmatch(text, -1) {
		if !ignoredAuthProviders[match[1]] {
			s.facts.observe(rel, observeCallback, "authjs|"+match[1]+"|")
		}
	}
	if location := betterAuthSocialRE.FindStringIndex(text); location != nil {
		keys, _ := objectLiteralKeys(text[location[1]-1:])
		for _, key := range keys {
			s.facts.observe(rel, observeCallback, "better-auth|"+strings.ToLower(key)+"|")
		}
	}
	if firebaseAuthRE.MatchString(text) && strings.Contains(text, "getAuth(") {
		s.facts.observe(rel, observeAuthService, "firebase")
	}
	if supabaseAuthRE.MatchString(text) {
		s.facts.observe(rel, observeAuthService, "supabase")
	}
	if stripeWebhookRE.MatchString(text) {
		s.facts.observe(rel, observeStripeWebhook, javaScriptRoutePath(rel))
	}
	// Passport's callbackURL, named by the strategy constructed around it.
	// Better Auth's client takes a callbackURL too — where to land after
	// sign-in, not an address a provider holds — so a file with no strategy
	// is not read.
	for _, match := range callbackURLRE.FindAllStringSubmatchIndex(text, -1) {
		strategies := passportStrategyRE.FindAllStringSubmatch(text[max(0, match[0]-800):match[0]], -1)
		if len(strategies) == 0 {
			continue
		}
		provider := strings.ToLower(strategies[len(strategies)-1][1])
		if provider == "" {
			provider = "passport"
		}
		s.facts.observe(rel, observeCallback, "passport|"+provider+"|"+text[match[2]:match[3]])
	}
	if (strings.HasPrefix(name, "codegen.") || strings.HasPrefix(name, "openapi-ts.config.") || strings.HasPrefix(name, "orval.config.")) &&
		codegenLocalhostRE.MatchString(text) {
		s.facts.observe(rel, observeBuildLocalhost, "")
	}
}

// namesReadIn returns the names already recorded from one source.
func (s *envScanner) namesReadIn(rel string) map[string]envReadFlags {
	result := map[string]envReadFlags{}
	for name, sources := range s.flags {
		if flags, ok := sources[rel]; ok {
			result[name] = flags
		}
	}
	return result
}

// scanNuxtRuntimeConfig lists the NUXT_ variables that override a Nuxt
// runtimeConfig: a key apiSecret is NUXT_API_SECRET, and public.apiBase is
// NUXT_PUBLIC_API_BASE.
func (s *envScanner) scanNuxtRuntimeConfig(rel, text string) {
	location := nuxtRuntimeConfigRE.FindStringIndex(text)
	if location == nil {
		return
	}
	keys, nested := objectLiteralKeys(text[location[1]-1:])
	for _, key := range keys {
		if key == "public" {
			publicKeys, _ := objectLiteralKeys(nested[key])
			for _, inner := range publicKeys {
				s.recordRead("NUXT_PUBLIC_"+snakeUpper(inner), rel, "", envReadBuild|envReadInlined)
			}
			continue
		}
		if _, object := nested[key]; object {
			continue
		}
		s.record("NUXT_"+snakeUpper(key), rel, "")
	}
}

// objectLiteralKeys reads the top-level keys of a JavaScript object literal
// that starts at text[0] == '{', and the source of each value that is itself
// an object. It skips strings and stops at the matching brace or 16 KiB.
func objectLiteralKeys(text string) ([]string, map[string]string) {
	keys := []string{}
	nested := map[string]string{}
	if text == "" || text[0] != '{' {
		return keys, nested
	}
	depth := 0
	var quote byte
	token := strings.Builder{}
	expectKey := true
	lastKey := ""
	valueStart := -1
	for index := 0; index < len(text) && index < 16<<10; index++ {
		c := text[index]
		if quote != 0 {
			if c == '\\' {
				index++
				continue
			}
			if c == quote {
				quote = 0
			} else if depth == 1 && expectKey {
				token.WriteByte(c)
			}
			continue
		}
		switch c {
		case '"', '\'', '`':
			quote = c
		case '{', '[', '(':
			depth++
			if depth == 2 && c == '{' && lastKey != "" {
				valueStart = index
			}
		case '}', ']', ')':
			if depth == 2 && c == '}' && valueStart >= 0 && lastKey != "" {
				nested[lastKey] = text[valueStart : index+1]
				valueStart = -1
			}
			depth--
			if depth == 0 {
				return keys, nested
			}
		case ':':
			if depth == 1 && expectKey {
				key := strings.TrimSpace(token.String())
				if key != "" && javaScriptKeyRE.MatchString(key) {
					keys = append(keys, key)
					lastKey = key
				}
				token.Reset()
				expectKey = false
			}
		case ',':
			if depth == 1 {
				token.Reset()
				expectKey = true
			}
		default:
			if depth == 1 && expectKey {
				token.WriteByte(c)
			}
		}
	}
	return keys, nested
}

// snakeUpper turns a camelCase key into the SCREAMING_SNAKE name Nuxt maps it from.
func snakeUpper(key string) string {
	out := strings.Builder{}
	for index, r := range key {
		if r >= 'A' && r <= 'Z' && index > 0 {
			out.WriteByte('_')
		}
		out.WriteRune(r)
	}
	return strings.ToUpper(out.String())
}

// hostedDriverForImport names the provider protocol an import speaks.
func hostedDriverForImport(module string) string {
	switch module {
	case "drizzle-orm/neon-http", "@neondatabase/serverless", "@prisma/adapter-neon":
		return "neon-http"
	case "drizzle-orm/neon-serverless":
		return "neon-ws"
	case "drizzle-orm/vercel-postgres", "@vercel/postgres":
		return "vercel-postgres"
	case "drizzle-orm/planetscale-serverless", "@planetscale/database", "@prisma/adapter-planetscale":
		return "planetscale-http"
	case "@upstash/redis", "@vercel/kv":
		return "upstash-rest"
	}
	return ""
}

// javaScriptRoutePath is the URL path a Next.js route handler or API page
// serves, which is what a webhook provider has to be given.
func javaScriptRoutePath(rel string) string {
	segments := strings.Split(rel, "/")
	for index, segment := range segments {
		switch {
		case segment == "app" && index < len(segments)-1 && strings.HasPrefix(segments[len(segments)-1], "route."):
			parts := []string{}
			for _, part := range segments[index+1 : len(segments)-1] {
				if strings.HasPrefix(part, "(") && strings.HasSuffix(part, ")") {
					continue
				}
				parts = append(parts, part)
			}
			return "/" + strings.Join(parts, "/")
		case segment == "pages" && index+1 < len(segments) && segments[index+1] == "api":
			file := strings.TrimSuffix(segments[len(segments)-1], path.Ext(segments[len(segments)-1]))
			parts := append(append([]string{}, segments[index+1:len(segments)-1]...), file)
			if file == "index" {
				parts = parts[:len(parts)-1]
			}
			return "/" + strings.Join(parts, "/")
		}
	}
	return ""
}
