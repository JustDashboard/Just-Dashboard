package deploy

import (
	"fmt"
	"net"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Variable classification: a detected variable should arrive configured, not
// as a blank row. The rules here are deliberately narrow — a name is only ever
// generated, bound to the domain or given a default when the root's own
// manifests name the framework that reads it that way — because the cost of a
// wrong guess is a provider credential minted as random bytes or a separate
// service's URL rewritten to this application's domain.

// rootStack is what a root's manifests say about the frameworks and libraries
// that issue, read or bind its variables. Every field is read as data.
type rootStack struct {
	root       string
	node       nodeManifest
	hasNode    bool
	python     pythonDependencies
	hasPython  bool
	composer   composerManifest
	gems       map[string]string
	mix        map[string]bool
	cargo      string
	jvm        string
	nuget      map[string]bool
	swift      string
	dart       string
	railsApp   bool
	facts      map[string][]byte
	candidates []DetectedCandidate
}

var (
	gemLockSpecRE     = regexp.MustCompile(`(?m)^    ([A-Za-z0-9_.\-]+) \(([^)]*)\)`)
	mixDepRE          = regexp.MustCompile(`\{\s*:([a-z0-9_]+)\s*,`)
	nugetReferenceRE  = regexp.MustCompile(`(?i)<PackageReference\s+Include\s*=\s*"([^"]+)"`)
	railsAppRE        = regexp.MustCompile(`<\s*Rails::Application\b`)
	solidQueuePumaRE  = regexp.MustCompile(`(?m)^\s*plugin\s+:solid_queue\b`)
	requireMasterKey  = regexp.MustCompile(`(?m)^\s*config\.require_master_key\s*=\s*true\b`)
	databaseYAMLKeyRE = regexp.MustCompile(`(?m)^  ([a-z_]+):\s*$`)
	localhostPathRE   = regexp.MustCompile(`^[A-Za-z0-9/_.\-~%]*$`)
	simpleWordRE      = regexp.MustCompile(`^[a-z][a-z0-9_\-]{0,31}$`)
	nextAuthMajorRE   = regexp.MustCompile(`(\d+)`)
	scriptLocalhostRE = regexp.MustCompile(`https?://(?:localhost|127\.0\.0\.1|0\.0\.0\.0)(?::\d+)?`)
	scriptCallsRE     = regexp.MustCompile(`\b(?:npm|pnpm|yarn|bun)\s+(?:run\s+)?([A-Za-z0-9:_\-]+)`)
)

func (s rootStack) nodeHas(names ...string) string {
	if !s.hasNode {
		return ""
	}
	for _, name := range names {
		if s.node.has(name) {
			return name
		}
	}
	return ""
}

func (s rootStack) nodeHasPrefix(prefix string) string {
	if !s.hasNode {
		return ""
	}
	names := []string{}
	for name := range s.node.Dependencies {
		names = append(names, name)
	}
	for name := range s.node.DevDependencies {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if strings.HasPrefix(name, prefix) {
			return name
		}
	}
	return ""
}

func (s rootStack) pythonHas(names ...string) string {
	if !s.hasPython {
		return ""
	}
	for _, name := range names {
		if s.python.has(name) {
			return name
		}
	}
	return ""
}

func (s rootStack) gemHas(names ...string) string {
	for _, name := range names {
		if _, ok := s.gems[name]; ok {
			return name
		}
	}
	return ""
}

func (s rootStack) rails() bool   { return s.gemHas("railties", "rails") != "" || s.railsApp }
func (s rootStack) phoenix() bool { return s.mix["phoenix"] }
func (s rootStack) django() bool  { return s.pythonHas("django") != "" }
func (s rootStack) flask() bool   { return s.pythonHas("flask") != "" }
func (s rootStack) laravel() bool { return s.composer.has("laravel/framework") }

// authJS is Auth.js v5 and its framework packages, which read AUTH_SECRET
// and trust the Host header only when told to; nextAuth4 is NextAuth v4.
func (s rootStack) authJS() bool {
	if s.nodeHasPrefix("@auth/") != "" {
		return true
	}
	version := s.node.version("next-auth")
	return version != "" && (strings.Contains(version, "beta") || nextAuthMajor(version) >= 5)
}

func (s rootStack) nextAuth4() bool {
	version := s.node.version("next-auth")
	return version != "" && !s.authJS() && nextAuthMajor(version) <= 4
}

func nextAuthMajor(version string) int {
	if match := nextAuthMajorRE.FindString(version); match != "" {
		major, _ := strconv.Atoi(match)
		return major
	}
	return 0
}

// railsBefore72 reports a locked Rails older than 7.2, which has no mysql://
// adapter alias.
func (s rootStack) railsBefore72() bool {
	version := s.gems["railties"]
	if version == "" {
		version = s.gems["rails"]
	}
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return false
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	return err1 == nil && err2 == nil && (major < 7 || (major == 7 && minor < 2))
}

func (s rootStack) jwtLibrary() string {
	if name := s.nodeHas("jsonwebtoken", "jose", "@nestjs/jwt", "passport-jwt", "@fastify/jwt", "express-jwt"); name != "" {
		return name
	}
	if name := s.pythonHas("pyjwt", "python-jose", "authlib"); name != "" {
		return name
	}
	if name := s.gemHas("jwt"); name != "" {
		return name
	}
	if s.mix["joken"] || s.mix["guardian"] {
		return "joken"
	}
	switch {
	case strings.Contains(s.cargo, "jsonwebtoken"):
		return "jsonwebtoken"
	case strings.Contains(s.jvm, "io.jsonwebtoken") || strings.Contains(s.jvm, "java-jwt"):
		return "jjwt"
	case s.nuget["system.identitymodel.tokens.jwt"] || s.nuget["microsoft.aspnetcore.authentication.jwtbearer"]:
		return "System.IdentityModel.Tokens.Jwt"
	}
	return ""
}

func (s rootStack) goModule(module string) bool {
	content := s.facts[path.Join(s.root, "go.mod")]
	return content != nil && strings.Contains(string(content), module)
}

// secretRule is one self-issued secret: the exact name, the framework whose
// presence makes it one, and the shape that framework reads.
type secretRule struct {
	name   string
	gate   func(rootStack, DetectedVariable) string
	format string
	length int
	// implied adds the variable even when nothing in the source reads it by
	// name: the framework reads it internally and refuses to start without it.
	implied bool
}

func dependencyGate(label string, has func(rootStack) string) func(rootStack, DetectedVariable) string {
	return func(stack rootStack, _ DetectedVariable) string {
		if name := has(stack); name != "" {
			return strings.ReplaceAll(label, "%s", name)
		}
		return ""
	}
}

// selfIssuedSecrets are the only names ever minted. A vendor's credential —
// STRIPE_SECRET_KEY, CLERK_SECRET_KEY, SUPABASE_JWT_SECRET, AUTH_GITHUB_SECRET
// — is issued by that vendor and never matches: every rule is an exact name
// gated by the dependency that issues it to itself.
var selfIssuedSecrets = []secretRule{
	{name: "APP_KEY", format: "laravel", length: 32, implied: true, gate: dependencyGate("Laravel encrypts with it (%s)", func(s rootStack) string {
		if s.laravel() {
			return "laravel/framework"
		}
		return ""
	})},
	{name: "APP_KEY", length: 32, gate: dependencyGate("AdonisJS encrypts with it (%s)", func(s rootStack) string { return s.nodeHas("@adonisjs/core") })},
	{name: "AUTH_SECRET", format: "base64", length: 32, implied: true, gate: dependencyGate("Auth.js signs and encrypts sessions with it (%s)", func(s rootStack) string {
		if s.authJS() {
			if name := s.nodeHas("next-auth"); name != "" {
				return name
			}
			return s.nodeHasPrefix("@auth/")
		}
		return ""
	})},
	{name: "NEXTAUTH_SECRET", format: "base64", length: 32, implied: true, gate: dependencyGate("NextAuth signs and encrypts sessions with it (%s)", func(s rootStack) string {
		if s.nextAuth4() {
			return "next-auth"
		}
		return ""
	})},
	{name: "NEXTAUTH_SECRET", format: "base64", length: 32, gate: dependencyGate("Auth.js signs and encrypts sessions with it (%s)", func(s rootStack) string {
		if s.authJS() {
			return "next-auth"
		}
		return ""
	})},
	{name: "BETTER_AUTH_SECRET", format: "base64", length: 32, implied: true, gate: dependencyGate("Better Auth signs sessions with it (%s)", func(s rootStack) string { return s.nodeHas("better-auth") })},
	{name: "PAYLOAD_SECRET", format: "hex", length: 64, implied: true, gate: dependencyGate("Payload encrypts with it (%s)", func(s rootStack) string { return s.nodeHas("payload") })},
	{name: "SECRET_KEY_BASE", format: "hex", length: 128, implied: true, gate: dependencyGate("Rails signs cookies and sessions with it (%s)", func(s rootStack) string {
		if s.rails() {
			return "rails"
		}
		return ""
	})},
	{name: "SECRET_KEY_BASE", format: "base64", length: 64, implied: true, gate: dependencyGate("Phoenix signs sessions with it (%s)", func(s rootStack) string {
		if s.phoenix() {
			return "phoenix"
		}
		return ""
	})},
	{name: "APPLICATION_SECRET", format: "hex", length: 64, implied: true, gate: func(s rootStack, _ DetectedVariable) string {
		for _, candidate := range s.candidates {
			if candidate.Framework == "play" {
				return "Play refuses to start in production without a secret of at least 256 bits; the recipe's start loads it from APPLICATION_SECRET"
			}
		}
		return ""
	}},
	{name: "SESSION_SECRET", format: "hex", length: 128, gate: func(s rootStack, variable DetectedVariable) string {
		if s.gemHas("sinatra") == "" {
			return ""
		}
		for _, source := range variable.Sources {
			if path.Ext(source) == ".rb" || path.Base(source) == "config.ru" {
				return "Sinatra signs session cookies with it, and Rack wants at least 64 bytes (" + source + ")"
			}
		}
		return ""
	}},
	{name: "SECRET_KEY", length: 50, gate: djangoSettingsGate},
	{name: "DJANGO_SECRET_KEY", length: 50, gate: djangoSettingsGate},
	{name: "SECRET_KEY", format: "hex", length: 64, gate: flaskGate},
	{name: "FLASK_SECRET_KEY", format: "hex", length: 64, gate: flaskGate},
	{name: "APP_KEYS", format: "keylist", length: 16, gate: strapiGate},
	{name: "API_TOKEN_SALT", format: "base64", length: 16, gate: strapiGate},
	{name: "ADMIN_JWT_SECRET", format: "base64", length: 16, gate: strapiGate},
	{name: "TRANSFER_TOKEN_SALT", format: "base64", length: 16, gate: strapiGate},
	{name: "ENCRYPTION_KEY", format: "base64", length: 16, gate: strapiGate},
	{name: "JWT_SECRET", format: "base64", length: 16, gate: strapiGate},
	{name: "KEY", length: 64, gate: dependencyGate("Directus identifies the instance with it (%s)", func(s rootStack) string { return s.nodeHas("directus") })},
	{name: "SECRET", length: 64, gate: dependencyGate("Directus signs tokens with it (%s)", func(s rootStack) string { return s.nodeHas("directus") })},
	{name: "JWT_SECRET", length: 64, gate: dependencyGate("Medusa signs tokens with it (%s)", func(s rootStack) string {
		return s.nodeHas("@medusajs/medusa", "@medusajs/framework")
	})},
	{name: "COOKIE_SECRET", length: 64, gate: dependencyGate("Medusa signs cookies with it (%s)", func(s rootStack) string {
		return s.nodeHas("@medusajs/medusa", "@medusajs/framework")
	})},
	{name: "SESSION_SECRET", length: 64, gate: dependencyGate("sessions are signed with it (%s)", func(s rootStack) string {
		return s.nodeHas("express-session", "cookie-session", "iron-session", "@fastify/session", "@fastify/secure-session", "koa-session", "@keystone-6/core")
	})},
	{name: "COOKIE_SECRET", length: 64, gate: dependencyGate("cookies are signed with it (%s)", func(s rootStack) string {
		return s.nodeHas("cookie-parser", "@fastify/cookie", "express-session", "cookie-session")
	})},
	{name: "JWT_SECRET", length: 64, gate: dependencyGate("tokens are signed with it (%s)", func(s rootStack) string { return s.jwtLibrary() })},
}

func strapiGate(stack rootStack, _ DetectedVariable) string {
	if stack.nodeHas("@strapi/strapi") != "" {
		return "Strapi requires it (@strapi/strapi)"
	}
	return ""
}

// djangoSettingsGate mints SECRET_KEY only when Django's own settings read
// that exact name: the same name elsewhere may be a provider's key.
func djangoSettingsGate(stack rootStack, variable DetectedVariable) string {
	if !stack.django() {
		return ""
	}
	for _, source := range variable.Sources {
		base := path.Base(source)
		if base == "settings.py" || strings.Contains(source, "settings/") {
			return "Django signs sessions and tokens with it (" + source + ")"
		}
	}
	return ""
}

func flaskGate(stack rootStack, variable DetectedVariable) string {
	if !stack.flask() || stack.django() {
		return ""
	}
	for _, source := range variable.Sources {
		if path.Ext(source) == ".py" {
			return "Flask signs session cookies with it (" + source + ")"
		}
	}
	return ""
}

// publicURLRule binds a self-URL to the planned domain when the root's
// framework reads it as its own address.
type publicURLRule struct {
	name     string
	template string
	gate     func(rootStack, DetectedVariable) string
	implied  bool
}

func ungated(reason string) func(rootStack, DetectedVariable) string {
	return func(rootStack, DetectedVariable) string { return reason }
}

var publicURLRules = []publicURLRule{
	{name: "AUTH_URL", template: "{{scheme}}://{{hostname}}", gate: func(s rootStack, _ DetectedVariable) string {
		if s.authJS() || s.nextAuth4() {
			return "Auth.js builds its callback URLs from it"
		}
		return ""
	}},
	{name: "NEXTAUTH_URL", template: "{{scheme}}://{{hostname}}", implied: true, gate: func(s rootStack, _ DetectedVariable) string {
		if s.nextAuth4() {
			return "NextAuth builds its callback URLs from it"
		}
		return ""
	}},
	{name: "NEXTAUTH_URL", template: "{{scheme}}://{{hostname}}", gate: func(s rootStack, _ DetectedVariable) string {
		if s.authJS() {
			return "Auth.js builds its callback URLs from it"
		}
		return ""
	}},
	{name: "BETTER_AUTH_URL", template: "{{scheme}}://{{hostname}}", implied: true, gate: func(s rootStack, _ DetectedVariable) string {
		if s.nodeHas("better-auth") != "" {
			return "Better Auth builds its callback URLs from it"
		}
		return ""
	}},
	{name: "ORIGIN", template: "{{scheme}}://{{hostname}}", implied: true, gate: func(s rootStack, _ DetectedVariable) string {
		if s.nodeHas("@sveltejs/adapter-node") != "" {
			return "SvelteKit's Node adapter refuses form posts from any other origin"
		}
		return ""
	}},
	{name: "APP_URL", template: "{{scheme}}://{{hostname}}", gate: func(s rootStack, _ DetectedVariable) string {
		if s.laravel() {
			return "Laravel generates absolute URLs from it"
		}
		return ""
	}},
	{name: "PHX_HOST", template: "{{hostname}}", implied: true, gate: func(s rootStack, _ DetectedVariable) string {
		if s.phoenix() {
			return "Phoenix accepts LiveView sockets only from this host"
		}
		return ""
	}},
	{name: "CSRF_TRUSTED_ORIGINS", template: "{{scheme}}://{{hostname}}", gate: func(s rootStack, _ DetectedVariable) string {
		if s.django() {
			return "Django refuses form posts from origins it does not trust"
		}
		return ""
	}},
	{name: "DJANGO_CSRF_TRUSTED_ORIGINS", template: "{{scheme}}://{{hostname}}", gate: func(s rootStack, _ DetectedVariable) string {
		if s.django() {
			return "Django refuses form posts from origins it does not trust"
		}
		return ""
	}},
	{name: "SITE_URL", template: "{{scheme}}://{{hostname}}", gate: ungated("the application's own address")},
	{name: "PUBLIC_URL", template: "{{scheme}}://{{hostname}}", gate: ungated("the application's own address")},
	{name: "BASE_URL", template: "{{scheme}}://{{hostname}}", gate: ungated("the application's own address")},
	{name: "NEXT_PUBLIC_APP_URL", template: "{{scheme}}://{{hostname}}", gate: ungated("the application's own address")},
	{name: "NEXT_PUBLIC_SITE_URL", template: "{{scheme}}://{{hostname}}", gate: ungated("the application's own address")},
	{name: "NEXT_PUBLIC_BASE_URL", template: "{{scheme}}://{{hostname}}", gate: ungated("the application's own address")},
}

// browserPrefixRules are the prefixes each framework compiles into client
// code, keyed by the dependency that does the compiling.
var browserPrefixRules = []struct {
	prefix       string
	dependencies []string
}{
	{"NEXT_PUBLIC_", []string{"next"}},
	{"VITE_", []string{"vite", "@sveltejs/kit", "astro", "@remix-run/dev", "@react-router/dev", "@solidjs/start", "@tanstack/react-start", "vitepress"}},
	{"PUBLIC_", []string{"@sveltejs/kit", "astro"}},
	{"REACT_APP_", []string{"react-scripts"}},
	{"NUXT_PUBLIC_", []string{"nuxt"}},
	{"EXPO_PUBLIC_", []string{"expo"}},
	{"GATSBY_", []string{"gatsby"}},
	{"VUE_APP_", []string{"@vue/cli-service"}},
}

// harmlessDefaults are the documented settings a template's own value may
// fill: they select a driver or a verbosity and never carry a credential or
// an address. LOG_LEVEL is pinned to info whatever the template says, since
// templates document development verbosity.
var harmlessDefaults = map[string]func(string) string{
	"LOG_LEVEL": func(example string) string {
		switch strings.ToLower(example) {
		case "trace", "debug", "info", "warn", "warning", "error", "fatal", "silent", "notice":
			return "info"
		}
		return ""
	},
	"SESSION_DRIVER":  simpleWordDefault,
	"DB_CONNECTION":   simpleWordDefault,
	"DB_CLIENT":       simpleWordDefault,
	"DATABASE_CLIENT": simpleWordDefault,
}

func simpleWordDefault(example string) string {
	if simpleWordRE.MatchString(example) {
		return example
	}
	return ""
}

// describeRootEnvironment is the second half of environment discovery for one
// root: it classifies the scanned variables, adds the ones a framework reads
// internally, re-reads the databases with every manifest the root holds, and
// records the facts preflight answers. The candidates of a root share it.
func describeRootEnvironment(marker *detectedMarkers, scanner *envScanner, prismaProviders map[string]string, candidates []DetectedCandidate) {
	if len(candidates) == 0 {
		return
	}
	root := marker.root
	stack := readRootStack(marker, scanner, candidates)
	variables := append([]DetectedVariable(nil), candidates[0].Variables...)
	prefixes := stack.browserPrefixes()
	ports := map[int]bool{}
	for _, candidate := range candidates {
		if candidate.Port > 0 {
			ports[candidate.Port] = true
		}
	}
	observations := scanner.facts.observationsFor(root, scanner)
	hostFlags, _ := scanner.rootReadFlags(root, "HOST")
	context := classification{prefixes: prefixes, ports: ports, observations: observations, bindHost: hostFlags&envReadBindHost != 0}
	for index := range variables {
		classifyVariable(stack, &variables[index], context)
	}
	variables = withImpliedVariables(stack, variables, prefixes)
	databases := detectDatabases(marker, variables, prismaProviders, manifestDatabaseEvidence(stack, observations, variables)...)
	databases = enrichDatabases(stack, databases, variables, observations)
	notes := environmentNotes(stack, observations, variables)
	for index := range candidates {
		candidate := &candidates[index]
		candidate.Variables = variables
		candidate.Databases = databases
		candidate.BrowserPrefixes = prefixes
		candidate.EnvironmentNotes = notes
		describeDockerfileFramework(stack, candidate)
	}
}

func readRootStack(marker *detectedMarkers, scanner *envScanner, candidates []DetectedCandidate) rootStack {
	stack := rootStack{root: marker.root, gems: map[string]string{}, mix: map[string]bool{}, nuget: map[string]bool{},
		facts: map[string][]byte{}, candidates: candidates}
	if len(marker.packageJSON) > 0 {
		stack.hasNode = parseNodeManifest(marker.packageJSON, &stack.node)
	}
	if marker.hasPythonManifest() {
		stack.python = readPythonDependencies(marker.pythonFiles)
		stack.hasPython = true
	}
	if len(marker.composerJSON) > 0 {
		stack.composer, _ = parseComposerManifest(marker.composerJSON)
	}
	stack.cargo = string(marker.cargoToml)
	stack.jvm = string(marker.pomXML) + "\n" + string(marker.gradleBuild)
	for _, content := range marker.csprojs {
		for _, match := range nugetReferenceRE.FindAllSubmatch(content, -1) {
			stack.nuget[strings.ToLower(string(match[1]))] = true
		}
	}
	if len(marker.goModContent) > 0 {
		stack.facts[path.Join(marker.root, "go.mod")] = marker.goModContent
	}
	for rel, content := range scanner.facts.files {
		if scanner.claimed(marker.root, rel) {
			stack.facts[rel] = content
		}
	}
	own := func(name string) []byte { return stack.facts[path.Join(marker.root, name)] }
	if lock := own("Gemfile.lock"); lock != nil {
		for _, match := range gemLockSpecRE.FindAllSubmatch(lock, -1) {
			stack.gems[strings.ToLower(string(match[1]))] = string(match[2])
		}
	} else if gemfile := own("Gemfile"); gemfile != nil {
		for _, match := range gemfileGemRE.FindAllSubmatch(gemfile, -1) {
			stack.gems[strings.ToLower(string(match[1]))] = ""
		}
	}
	if mix := own("mix.exs"); mix != nil {
		for _, match := range mixDepRE.FindAllSubmatch(mix, -1) {
			stack.mix[string(match[1])] = true
		}
		// An umbrella's root declares no dependencies of its own; the
		// applications its release runs do.
		if match := mixUmbrellaRE.FindSubmatch(mix); match != nil {
			prefix := path.Join(filepath.ToSlash(marker.root), string(match[1])) + "/"
			for rel, content := range scanner.facts.files {
				if child, ok := strings.CutPrefix(rel, prefix); ok && strings.Count(child, "/") == 1 && path.Base(child) == "mix.exs" {
					for _, dependency := range mixDepRE.FindAllSubmatch(content, -1) {
						stack.mix[string(dependency[1])] = true
					}
				}
			}
		}
	}
	// sbt, Leiningen and deps.edn name their JVM drivers the way a pom or a
	// Gradle script does.
	for _, name := range []string{"build.sbt", "project.clj", "deps.edn"} {
		if content := own(name); content != nil {
			stack.jvm += "\n" + string(content)
		}
	}
	stack.railsApp = railsAppRE.Match(own("config/application.rb"))
	stack.swift = string(marker.packageSwift)
	stack.dart = string(own("pubspec.yaml"))
	return stack
}

func (s rootStack) browserPrefixes() []string {
	prefixes := []string{}
	for _, rule := range browserPrefixRules {
		if s.nodeHas(rule.dependencies...) != "" {
			prefixes = append(prefixes, rule.prefix)
		}
	}
	return prefixes
}

func browserPrefixed(name string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(name, prefix) && len(name) > len(prefix) {
			return true
		}
	}
	return false
}

// observationsFor returns the observations recorded in sources a root owns.
func (f *environmentFacts) observationsFor(root string, scanner *envScanner) []environmentObservation {
	result := []environmentObservation{}
	for _, observation := range f.observations {
		if scanner.claimed(root, observation.source) {
			result = append(result, observation)
		}
	}
	return result
}

func observed(observations []environmentObservation, kind string) []environmentObservation {
	result := []environmentObservation{}
	for _, observation := range observations {
		if observation.kind == kind {
			result = append(result, observation)
		}
	}
	return result
}

// classification is what one root knows while its variables are classified.
type classification struct {
	prefixes     []string
	ports        map[int]bool
	observations []environmentObservation
	bindHost     bool
}

// classifyVariable decides how one variable is supplied when the operator
// types nothing, and records the phase its prefix implies.
func classifyVariable(stack rootStack, variable *DetectedVariable, context classification) {
	if browserPrefixed(variable.Name, context.prefixes) {
		variable.BrowserInlined = true
		variable.Phase = "build"
	}
	for _, rule := range selfIssuedSecrets {
		if rule.name != variable.Name {
			continue
		}
		if reason := rule.gate(stack, *variable); reason != "" {
			variable.Setup, variable.SetupReason = "generate", reason
			variable.GenerateFormat, variable.GenerateLength = rule.format, rule.length
			return
		}
	}
	if variable.Name == "RAILS_MASTER_KEY" && stack.rails() {
		variable.Setup, variable.SetupReason = "paste", "decrypts the committed Rails credentials; copy it from config/master.key"
		return
	}
	if hostList, separator, source := djangoHostList(stack, variable.Name, context.observations); hostList {
		// Bound only in the separator the settings split on: joined with the
		// other one, the list is a single host Django never matches. The
		// loopback names keep the readiness probe, which dials 127.0.0.1,
		// inside the allowlist.
		if separator != "" {
			variable.Setup, variable.SetupReason = "domain", "Django answers only the hosts it allows"
			if source != "" {
				variable.SetupReason += " (" + source + " splits it on " + map[string]string{",": "commas", " ": "spaces"}[separator] + ")"
			}
			variable.DomainTemplate = strings.Join([]string{"{{hostname}}", "localhost", "127.0.0.1"}, separator)
		}
		return
	}
	if stack.django() || stack.flask() {
		for _, debug := range observed(context.observations, observeDebugDefault) {
			name, seed, _ := strings.Cut(debug.detail, "|")
			if name == variable.Name && seed != "" {
				variable.Setup, variable.DefaultValue = "default", seed
				variable.SetupReason = "debug mode is on unless " + name + " is set (" + debug.source + ")"
				return
			}
		}
	}
	for _, rule := range publicURLRules {
		if rule.name != variable.Name {
			continue
		}
		if reason := rule.gate(stack, *variable); reason != "" {
			variable.Setup, variable.SetupReason = "domain", reason
			variable.DomainTemplate = rule.template
			if strings.HasPrefix(rule.template, "{{scheme}}") {
				variable.DomainTemplate += localhostExamplePath(variable.Example)
			}
			return
		}
	}
	if port, ok := localhostExamplePort(variable.Example); ok && context.ports[port] {
		variable.Setup = "domain"
		variable.SetupReason = "its example is this application's own development address"
		variable.DomainTemplate = "{{scheme}}://{{hostname}}" + localhostExamplePath(variable.Example)
		return
	}
	switch {
	case variable.Name == "HOST" && context.bindHost:
		variable.Setup, variable.DefaultValue = "default", "0.0.0.0"
		variable.SetupReason = "read as the address the server listens on; inside the container only 0.0.0.0 is reachable"
		return
	case variable.Name == "SOLID_QUEUE_IN_PUMA" && solidQueueInPuma(stack):
		variable.Setup, variable.DefaultValue = "default", "true"
		variable.SetupReason = "Puma runs Solid Queue's jobs only when it is set"
		return
	case variable.Name == "RAILS_LOG_TO_STDOUT" && railsLogsToFile(stack):
		variable.Setup, variable.DefaultValue = "default", "1"
		variable.SetupReason = railsLogToStdoutReason
		return
	case variable.Name == "AUTH_TRUST_HOST" && stack.authJS():
		variable.Setup, variable.DefaultValue = "default", "true"
		variable.SetupReason = "the managed proxy is the only way in and sets the Host header"
		return
	}
	if fill := harmlessDefaults[variable.Name]; fill != nil && variable.Example != "" && len(variable.Sources) > 0 &&
		envTemplateFile(path.Base(variable.Sources[0])) && !envRealFile(path.Base(variable.Sources[0])) {
		if value := fill(variable.Example); value != "" {
			variable.Setup, variable.DefaultValue = "default", value
			variable.SetupReason = "documented in " + variable.Sources[0]
		}
	}
}

// djangoHostList reports whether a variable is the list Django's
// ALLOWED_HOSTS is read from, the separator its settings split it on (","
// or " "; empty when the expression does not say), and the settings file
// that says so. A name the settings were not seen reading counts only when
// it is one of the two conventional names.
func djangoHostList(stack rootStack, name string, observations []environmentObservation) (bool, string, string) {
	if !stack.django() {
		return false, "", ""
	}
	for _, observation := range observed(observations, observeHostList) {
		variable, separator, _ := strings.Cut(observation.detail, "|")
		if variable == name {
			return true, map[string]string{"comma": ",", "space": " "}[separator], observation.source
		}
	}
	return name == "ALLOWED_HOSTS" || name == "DJANGO_ALLOWED_HOSTS", "", ""
}

// withImpliedVariables adds what a framework reads internally and refuses to
// start without, when the source never names it: Rails' SECRET_KEY_BASE,
// Auth.js's secret, SvelteKit's ORIGIN. Each carries the manifest that
// implied it as its source.
func withImpliedVariables(stack rootStack, variables []DetectedVariable, prefixes []string) []DetectedVariable {
	present := map[string]bool{}
	for _, variable := range variables {
		present[variable.Name] = true
	}
	implied := []DetectedVariable{}
	add := func(variable DetectedVariable, manifest string) {
		if present[variable.Name] {
			return
		}
		present[variable.Name] = true
		variable.Sources = []string{path.Join(stack.root, manifest)}
		if browserPrefixed(variable.Name, prefixes) {
			variable.BrowserInlined, variable.Phase = true, "build"
		}
		implied = append(implied, variable)
	}
	for _, rule := range selfIssuedSecrets {
		if !rule.implied || present[rule.name] {
			continue
		}
		if rule.name == "AUTH_SECRET" && present["NEXTAUTH_SECRET"] {
			continue
		}
		if reason := rule.gate(stack, DetectedVariable{Name: rule.name}); reason != "" {
			add(DetectedVariable{Name: rule.name, Setup: "generate", SetupReason: reason,
				GenerateFormat: rule.format, GenerateLength: rule.length, Required: true}, stack.manifestFor(rule.name))
		}
	}
	for _, rule := range publicURLRules {
		if !rule.implied || present[rule.name] {
			continue
		}
		if reason := rule.gate(stack, DetectedVariable{Name: rule.name}); reason != "" {
			add(DetectedVariable{Name: rule.name, Setup: "domain", SetupReason: reason, DomainTemplate: rule.template}, stack.manifestFor(rule.name))
		}
	}
	if stack.authJS() {
		add(DetectedVariable{Name: "AUTH_TRUST_HOST", Setup: "default", DefaultValue: "true",
			SetupReason: "the managed proxy is the only way in and sets the Host header"}, "package.json")
	}
	if railsLogsToFile(stack) {
		add(DetectedVariable{Name: "RAILS_LOG_TO_STDOUT", Setup: "default", DefaultValue: "1", SetupReason: railsLogToStdoutReason},
			"config/environments/production.rb")
	}
	if solidQueueInPuma(stack) {
		add(DetectedVariable{Name: "SOLID_QUEUE_IN_PUMA", Setup: "default", DefaultValue: "true",
			SetupReason: "Puma runs Solid Queue's jobs only when it is set"}, "config/puma.rb")
	}
	if credentials := railsCredentials(stack); credentials != "" && stack.rails() {
		add(DetectedVariable{Name: "RAILS_MASTER_KEY", Setup: "paste",
			SetupReason: "decrypts " + credentials + "; copy it from config/master.key, it cannot be generated",
			Required:    requireMasterKey.Match(stack.facts[path.Join(stack.root, "config/environments/production.rb")])},
			"config/credentials.yml.enc")
	}
	if len(implied) == 0 {
		return variables
	}
	if room := 64 - len(implied); len(variables) > room {
		variables = variables[:max(room, 0)]
	}
	return append(variables, implied...)
}

// manifestFor names the file that makes a framework's variable implied.
func (s rootStack) manifestFor(name string) string {
	switch {
	case name == "APP_KEY" && s.laravel():
		return "composer.json"
	case name == "SECRET_KEY_BASE" && s.rails(), name == "RAILS_MASTER_KEY":
		if s.gems["railties"] != "" || s.gems["rails"] != "" {
			return "Gemfile.lock"
		}
		return "config/application.rb"
	case name == "SECRET_KEY_BASE" || name == "PHX_HOST":
		return "mix.exs"
	case name == "APPLICATION_SECRET":
		return "build.sbt"
	}
	return "package.json"
}

const railsLogToStdoutReason = "production.rb logs to log/production.log, where nothing reads it, unless it is set"

// railsLogsToFile is Rails 7.0 and earlier's production.rb, which writes
// its log to a file unless RAILS_LOG_TO_STDOUT is set: a request that fails
// readiness with a 500 would leave nothing in the container's output.
func railsLogsToFile(stack rootStack) bool {
	return stack.rails() && strings.Contains(string(stack.facts[path.Join(stack.root, "config/environments/production.rb")]), "RAILS_LOG_TO_STDOUT")
}

func solidQueueInPuma(stack rootStack) bool {
	return stack.rails() && stack.gemHas("solid_queue") != "" &&
		solidQueuePumaRE.Match(stack.facts[path.Join(stack.root, "config/puma.rb")])
}

// railsCredentials names the committed encrypted credentials file a Rails
// root decrypts at boot, unless its key is committed beside it.
func railsCredentials(stack rootStack) string {
	if _, committed := stack.facts[path.Join(stack.root, "config/master.key")]; committed {
		return ""
	}
	for _, name := range []string{"config/credentials/production.yml.enc", "config/credentials.yml.enc"} {
		if _, ok := stack.facts[path.Join(stack.root, name)]; ok {
			return name
		}
	}
	return ""
}

// localhostExamplePort reads the explicit port of an http(s) example on
// loopback: http://localhost:3000 is this application's own development
// address when the application listens on 3000.
func localhostExamplePort(example string) (int, bool) {
	parsed, err := url.Parse(example)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || !loopbackTarget(parsed.Hostname()) || parsed.Port() == "" {
		return 0, false
	}
	port, err := strconv.Atoi(parsed.Port())
	return port, err == nil
}

// localhostExamplePath keeps the path of a loopback http(s) example, so
// NEXTAUTH_URL=http://localhost:3000/api/auth binds to the same path on the
// planned domain.
func localhostExamplePath(example string) string {
	parsed, err := url.Parse(example)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || !loopbackTarget(parsed.Hostname()) {
		return ""
	}
	route := strings.TrimSuffix(parsed.EscapedPath(), "/")
	if len(route) > 256 || !localhostPathRE.MatchString(route) || parsed.RawQuery != "" {
		return ""
	}
	return route
}

// describeDockerfileFramework names the framework of a repository's own
// Dockerfile when its manifests are unambiguous and the Dockerfile's own
// reading (dockerfileFramework) did not, so preflight can ask for the
// variables that framework needs, and gives Phoenix's release image the port
// its runtime.exs defaults to when neither the Dockerfile nor the
// configuration names one.
func describeDockerfileFramework(stack rootStack, candidate *DetectedCandidate) {
	if candidate.BuildMethod != BuildDockerfile {
		return
	}
	switch {
	case candidate.Framework == "" && stack.rails():
		candidate.Framework = "rails"
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: path.Join(stack.root, stack.manifestFor("SECRET_KEY_BASE")), Reason: "Rails application"})
	case (candidate.Framework == "" || candidate.Framework == "phoenix") && stack.phoenix():
		if candidate.Framework == "" {
			candidate.Framework = "phoenix"
			candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: path.Join(stack.root, "mix.exs"), Reason: "Phoenix application"})
		}
		if candidate.Port == 0 {
			candidate.Port = 4000
			candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: path.Join(stack.root, "mix.exs"),
				Reason: "Phoenix listens on PORT, 4000 by default, and the Dockerfile exposes none"})
		}
	}
}

// environmentNotes turns a root's observations and manifests into the notes
// preflight answers, in a stable order.
func environmentNotes(stack rootStack, observations []environmentObservation, variables []DetectedVariable) []EnvironmentNote {
	notes := []EnvironmentNote{}
	seen := map[string]bool{}
	add := func(code, detail, source string) {
		key := code + "\x00" + detail + "\x00" + source
		if seen[key] || len(notes) >= 32 {
			return
		}
		seen[key] = true
		notes = append(notes, EnvironmentNote{Code: code, Detail: detail, Path: source})
	}
	python := stack.django() || stack.flask()
	for _, observation := range observations {
		switch observation.kind {
		case observeSecretLiteral, observeDebugLiteral, observeDebugDefault:
			if python {
				add(observation.kind, observation.detail, observation.source)
			}
		case observeDotenvRequired:
			// A recipe image creates the file only for the entry points the
			// build compiles, so a workspace member's or an example's main.rs
			// is not this root's.
			if observation.detail == "dotenvy" && observation.source != path.Join(stack.root, "src/main.rs") &&
				path.Dir(observation.source) != path.Join(stack.root, "src/bin") {
				continue
			}
			add(observation.kind, observation.detail, observation.source)
		case observeAuthService, observeBuildLocalhost:
			add(observation.kind, observation.detail, observation.source)
		case observeStripeWebhook:
			add(observation.kind, observation.detail, observation.source)
		case observeCallback:
			parts := strings.SplitN(observation.detail, "|", 3)
			if len(parts) != 3 {
				continue
			}
			provider, route := parts[1], parts[2]
			switch parts[0] {
			case "authjs":
				base := "/api/auth"
				if stack.nodeHas("@auth/sveltekit", "@auth/express", "@auth/solid-start", "@auth/qwik") != "" && stack.nodeHas("next") == "" {
					base = "/auth"
				}
				route = base + "/callback/" + provider
			case "better-auth":
				route = "/api/auth/callback/" + provider
			}
			if route != "" {
				add(observeCallback, provider+"|"+route, observation.source)
			}
		}
	}
	for gem := range stack.gems {
		if !strings.HasPrefix(gem, "omniauth-") || gem == "omniauth-rails_csrf_protection" || gem == "omniauth-oauth2" {
			continue
		}
		strategy := strings.ReplaceAll(strings.TrimPrefix(gem, "omniauth-"), "-", "_")
		base := "/auth/"
		if stack.gemHas("devise") != "" {
			base = "/users/auth/"
		}
		add(observeCallback, strategy+"|"+base+strategy+"/callback", path.Join(stack.root, "Gemfile.lock"))
	}
	if name := stack.nodeHasPrefix("@clerk/"); name != "" {
		add(observeAuthService, "clerk", path.Join(stack.root, "package.json"))
	}
	if version := stack.node.version("@auth0/nextjs-auth0"); version != "" {
		route := "/api/auth/callback"
		if nextAuthMajor(version) >= 4 {
			route = "/auth/callback"
		}
		add(observeCallback, "auth0|"+route, path.Join(stack.root, "package.json"))
	}
	for _, script := range buildScriptsFetchingLocalhost(stack) {
		add("build_fetches_localhost", script+" script", path.Join(stack.root, "package.json"))
	}
	if credentials := railsCredentials(stack); credentials != "" && stack.rails() {
		add("rails_credentials", "", path.Join(stack.root, credentials))
		if requireMasterKey.Match(stack.facts[path.Join(stack.root, "config/environments/production.rb")]) {
			add("rails_require_master_key", "", path.Join(stack.root, "config/environments/production.rb"))
		}
	}
	for _, variable := range variables {
		if hostList, _, _ := djangoHostList(stack, variable.Name, observations); hostList && variable.Setup == "" {
			add("allowed_hosts_unbound", variable.Name, variableReadIn(variable))
		}
	}
	if _, ok := stack.facts[path.Join(stack.root, "rel/overlays/bin/migrate")]; ok && stack.phoenix() {
		add("phoenix_migrate_overlay", "", path.Join(stack.root, "rel/overlays/bin/migrate"))
	}
	sort.SliceStable(notes, func(i, j int) bool {
		if notes[i].Code != notes[j].Code {
			return notes[i].Code < notes[j].Code
		}
		return notes[i].Detail+notes[i].Path < notes[j].Detail+notes[j].Path
	})
	return notes
}

// buildScriptsFetchingLocalhost names the package scripts the build runs —
// build and its pre/post hooks, install hooks, and whatever those invoke —
// that fetch from loopback, where nothing listens during an image build.
func buildScriptsFetchingLocalhost(stack rootStack) []string {
	if !stack.hasNode {
		return nil
	}
	queue := []string{"prebuild", "build", "postinstall", "prepare"}
	visited := map[string]bool{}
	found := []string{}
	for len(queue) > 0 && len(visited) < 32 {
		name := queue[0]
		queue = queue[1:]
		if visited[name] {
			continue
		}
		visited[name] = true
		script := stack.node.Scripts[name]
		if script == "" {
			continue
		}
		if scriptLocalhostRE.MatchString(script) {
			found = append(found, name)
		}
		for _, match := range scriptCallsRE.FindAllStringSubmatch(script, -1) {
			if _, ok := stack.node.Scripts[match[1]]; ok {
				queue = append(queue, match[1], "pre"+match[1])
			}
		}
	}
	sort.Strings(found)
	return found
}

// loopbackTarget reports a host that, inside the application's container, is
// the container itself.
func loopbackTarget(host string) bool {
	host = strings.Trim(strings.ToLower(strings.TrimSpace(host)), "[]")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
}

// loopbackOrAnyAddress is a bind address written in a template: the
// development value of a variable a server listens on.
func loopbackOrAnyAddress(value string) bool {
	switch strings.Trim(strings.ToLower(strings.TrimSpace(value)), "[]") {
	case "localhost", "127.0.0.1", "0.0.0.0", "::", "::1":
		return true
	}
	return false
}

// bindAddressName names variables that hold the address a server listens
// on rather than one it connects to; loopback there is a bind problem, not a
// connection to nothing.
func bindAddressName(name string) bool {
	switch name {
	case "HOST", "BIND", "BIND_HOST", "BIND_ADDR", "BIND_ADDRESS", "LISTEN", "LISTEN_HOST", "LISTEN_ADDR",
		"LISTEN_ADDRESS", "SERVER_HOST", "SERVER_ADDRESS", "HTTP_BIND", "ADDR":
		return true
	}
	return false
}

// hostOnlyName names a variable whose value is a bare host or host:port.
func hostOnlyName(name string) bool {
	for _, suffix := range []string{"_HOST", "_HOSTNAME", "_SERVER", "_ADDR", "_ADDRESS", "_HOSTS"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// loopbackValue reports whether a value, read as a connection target, points
// at loopback: a URL or JDBC URL (including a seed list), an ADO.NET or
// libpq keyword string, or a host-only variable. The value itself is never
// returned or logged.
func loopbackValue(name, value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "${{") {
		return false
	}
	value = strings.TrimPrefix(value, "jdbc:")
	if scheme := strings.Index(value, "://"); scheme > 0 {
		authority := value[scheme+3:]
		if end := strings.IndexAny(authority, "/?#"); end >= 0 {
			authority = authority[:end]
		}
		if at := strings.LastIndex(authority, "@"); at >= 0 {
			authority = authority[at+1:]
		}
		for _, host := range strings.Split(authority, ",") {
			if hostname, _, err := net.SplitHostPort(host); err == nil {
				host = hostname
			}
			if host != "" && loopbackTarget(host) {
				return true
			}
		}
		return false
	}
	for _, part := range strings.FieldsFunc(value, func(r rune) bool { return r == ';' || r == ' ' }) {
		key, setting, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "host", "server", "data source", "datasource", "address", "addr", "hostaddr":
			host := strings.TrimSpace(setting)
			if comma := strings.IndexAny(host, ",:"); comma >= 0 && !strings.HasPrefix(host, "[") {
				host = host[:comma]
			}
			if loopbackTarget(host) || strings.EqualFold(host, "(local)") || host == "." {
				return true
			}
		}
	}
	if hostOnlyName(name) {
		host := value
		if hostname, _, err := net.SplitHostPort(value); err == nil {
			host = hostname
		}
		return loopbackTarget(host)
	}
	return false
}

var (
	variableSetups     = map[string]bool{"": true, "generate": true, "domain": true, "default": true, "paste": true}
	environmentNoteRE  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	browserPrefixShape = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,30}_$`)
)

// validateDetectedEnvironment bounds what detection adds to a candidate
// beyond names, the way validateDetectionResult bounds everything else: a
// saved detection is data a client sent back, and none of it may smuggle a
// value or a malformed template into a plan.
func validateDetectedEnvironment(candidate DetectedCandidate) error {
	for _, variable := range candidate.Variables {
		invalid := fmt.Errorf("detected variable %s is malformed", variable.Name)
		if !variableSetups[variable.Setup] || len(variable.SetupReason) > 256 || strings.ContainsAny(variable.SetupReason, "\x00\r\n") ||
			rejectPlanSecretLiteral("detected variable reason", variable.SetupReason) != nil ||
			!validGeneratedSecretFormat(variable.GenerateFormat) ||
			(variable.Phase != "" && variable.Phase != "build") ||
			len(variable.LocalhostIn) > 4096 || strings.ContainsAny(variable.LocalhostIn, "\x00\r\n") {
			return invalid
		}
		if variable.Setup == "generate" && (variable.GenerateLength < MinGeneratedSecretLength || variable.GenerateLength > MaxGeneratedSecretLength) {
			return invalid
		}
		if variable.Setup != "generate" && (variable.GenerateLength != 0 || variable.GenerateFormat != "") {
			return invalid
		}
		if variable.DomainTemplate != "" {
			literal := strings.NewReplacer("{{hostname}}", "example.com", "{{scheme}}", "https").Replace(variable.DomainTemplate)
			if variable.Setup != "domain" || len(variable.DomainTemplate) > 512 || !strings.Contains(variable.DomainTemplate, "{{hostname}}") ||
				strings.ContainsAny(literal, "{}\x00\r\n") || rejectPlanSecretLiteral("detected domain template", literal) != nil {
				return invalid
			}
		}
		if variable.DefaultValue != "" && (variable.Setup != "default" || len(variable.DefaultValue) > 256 ||
			strings.ContainsAny(variable.DefaultValue, "\x00\r\n") || strings.HasPrefix(variable.DefaultValue, "${{") ||
			rejectPlanSecretLiteral("detected default", variable.DefaultValue) != nil) {
			return invalid
		}
	}
	if len(candidate.BrowserPrefixes) > 16 {
		return fmt.Errorf("detected browser prefixes are malformed")
	}
	for _, prefix := range candidate.BrowserPrefixes {
		if !browserPrefixShape.MatchString(prefix) {
			return fmt.Errorf("detected browser prefixes are malformed")
		}
	}
	if len(candidate.EnvironmentNotes) > 64 {
		return fmt.Errorf("detected environment notes are malformed")
	}
	for _, note := range candidate.EnvironmentNotes {
		if !environmentNoteRE.MatchString(note.Code) || len(note.Detail) > 256 || len(note.Path) > 4096 ||
			strings.ContainsAny(note.Detail+note.Path, "\x00\r\n") || rejectPlanSecretLiteral("detected environment note", note.Detail) != nil {
			return fmt.Errorf("detected environment notes are malformed")
		}
	}
	return nil
}
