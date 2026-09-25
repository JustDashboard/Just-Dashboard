package deploy

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
)

// What a JavaScript build command's RUN needs besides its variables: a
// legacy OpenSSL provider for webpack 4 toolchains, a way past an
// env-validation schema whose server variables have no build value, and the
// env file a command names that is not committed. Each is a recipe constant
// or a shell expression, never a variable's value, and each gives way to a
// value the build supplies.
//
// The build's heap is left at V8's own default. NODE_OPTIONS is inherited by
// every node process the build starts — Next.js's page workers, a bundler's
// minifier workers — so a larger heap is a larger ceiling for each of them at
// once, and on a small host that turns a "JavaScript heap out of memory" into
// swapping and the kernel's OOM killer choosing among this server's
// services. Preflight's build_memory_low says so before Deploy instead, and
// an operator who has the memory sets NODE_OPTIONS as a build variable.

// nodeLegacyWebpack are toolchains that hash with MD4 through webpack 4,
// which OpenSSL 3 — Node 17 and later — refuses with "error:0308010C:digital
// envelope routines::unsupported", each with the release that stopped.
var nodeLegacyWebpack = []struct {
	name   string
	before nodeVersion
}{
	{"react-scripts", nodeVersion{5, 0, 0}},
	{"@vue/cli-service", nodeVersion{5, 0, 0}},
	{"webpack", nodeVersion{5, 61, 0}},
	{"nuxt", nodeVersion{2, 16, 0}},
	{"@angular-devkit/build-angular", nodeVersion{13, 0, 0}},
	{"laravel-mix", nodeVersion{6, 0, 0}},
	{"@symfony/webpack-encore", nodeVersion{1, 0, 0}},
}

// nodeEnvValidation is an env-validation schema (T3 Env) the build imports:
// the variables its server and client blocks require, and whether it
// honours SKIP_ENV_VALIDATION, as create-t3-app's does.
type nodeEnvValidation struct {
	source         string
	server, client []string
	skippable      bool
}

var (
	nodeEnvValidationPackages = []string{"@t3-oss/env-nextjs", "@t3-oss/env-core", "@t3-oss/env-nuxt"}
	nodeEnvValidationFiles    = []string{
		"src/env.js", "src/env.mjs", "src/env.ts", "env.js", "env.mjs", "env.ts", "src/env/index.ts", "src/env/index.js",
		"src/lib/env.ts", "src/lib/env.js", "lib/env.ts", "lib/env.js", "app/env.ts", "app/env.js", "src/env/server.mjs",
	}
	nodeEnvBlockRE = regexp.MustCompile(`\b(server|client)\s*:\s*\{`)
	nodeEnvFileRE  = regexp.MustCompile(`--env-file(?:=|\s+)(['"]?)([A-Za-z0-9._/-]+)(['"]?)`)
)

// readEnvValidation reads the schema of a package that declares T3 Env.
func readEnvValidation(pkg nodeFiles, manifest nodeInstallManifest) nodeEnvValidation {
	declared := false
	for _, name := range nodeEnvValidationPackages {
		declared = declared || manifest.has(name)
	}
	if !declared {
		return nodeEnvValidation{}
	}
	for _, name := range nodeEnvValidationFiles {
		content, err := pkg.read(name, nodeConfigMaxBytes)
		if err != nil || !strings.Contains(string(content), "createEnv") {
			continue
		}
		text := string(content)
		validation := nodeEnvValidation{source: name, skippable: strings.Contains(text, "SKIP_ENV_VALIDATION")}
		for _, match := range nodeEnvBlockRE.FindAllStringSubmatchIndex(text, -1) {
			keys := requiredEnvKeys(text[match[1]:])
			if text[match[2]:match[3]] == "server" {
				validation.server = mergeNames(validation.server, keys)
			} else {
				validation.client = mergeNames(validation.client, keys)
			}
		}
		return validation
	}
	return nodeEnvValidation{}
}

// requiredEnvKeys reads the top-level keys of an object literal that starts
// just after its opening brace, leaving out those whose schema is optional
// or has a default: they cannot fail validation.
func requiredEnvKeys(body string) []string {
	keys := []string{}
	depth := 0
	quote := rune(0)
	entryStart := 0
	flush := func(end int) {
		entry := strings.TrimSpace(body[entryStart:end])
		name, schema, found := strings.Cut(entry, ":")
		name = strings.Trim(strings.TrimSpace(name), `"'`)
		if !found || !envNameRE.MatchString(name) {
			return
		}
		for _, lenient := range []string{".optional()", ".default(", ".nullish()", ".catch("} {
			if strings.Contains(schema, lenient) {
				return
			}
		}
		if len(keys) < 64 {
			keys = append(keys, name)
		}
	}
	for index, r := range body {
		switch {
		case quote != 0:
			if r == quote && (index == 0 || body[index-1] != '\\') {
				quote = 0
			}
		case r == '"' || r == '\'' || r == '`':
			quote = r
		case r == '{' || r == '(' || r == '[':
			depth++
		case r == '}' || r == ')' || r == ']':
			if depth == 0 {
				flush(index)
				return keys
			}
			depth--
		case r == ',' && depth == 0:
			flush(index)
			entryStart = index + 1
		}
	}
	return keys
}

func mergeNames(existing, added []string) []string {
	for _, name := range added {
		if !slices.Contains(existing, name) {
			existing = append(existing, name)
		}
	}
	return existing
}

// nodeBuildPlan is what the build command's RUN gets from the plan.
type nodeBuildPlan struct {
	// legacyOpenSSL names the toolchain that needs the legacy provider.
	legacyOpenSSL string
	env           nodeEnvValidation
	// framework are the defaults the matched framework's build needs
	// (NITRO_PRESET for a provider preset, NODE_ENV for Strapi's admin).
	framework []nodeEnvDefault
}

// planNodeBuild records the build's environment decisions: the legacy
// OpenSSL provider, the env files the build and start commands name, and
// the schema that validates variables at build time.
func planNodeBuild(facts nodeInstallFacts, plan *nodeInstallPlan, assets bool) {
	for _, legacy := range nodeLegacyWebpack {
		version, ok := facts.directVersion(legacy.name, plan.reading)
		if !ok || !version.less(legacy.before) {
			continue
		}
		plan.buildEnv.legacyOpenSSL = fmt.Sprintf("%s %d.%d.%d", legacy.name, version.major, version.minor, version.patch)
		plan.findings = append(plan.findings, nodeFinding("legacy_openssl_provider", PreflightWarning,
			"The build needs Node's legacy OpenSSL provider", plan.buildEnv.legacyOpenSSL,
			"Its webpack 4 hashes with MD4, which OpenSSL 3 in Node 17 and later refuses (error:0308010C); the build command runs with NODE_OPTIONS=--openssl-legacy-provider, which re-enables legacy algorithms for the build alone.",
			"Upgrade to the toolchain's webpack 5 release (react-scripts 5, Vue CLI 5, Nuxt 2.16, Angular 13); the flag then goes away.", "configuration.build"))
		break
	}
	if assets {
		return
	}
	plan.buildEnv.env = facts.envValidation
	for _, command := range []struct {
		label, text string
		runtime     bool
	}{{"build", plan.build, false}, {"start", plan.start, true}} {
		for _, envFile := range nodeEnvFiles(facts.manifest.Scripts, command.text) {
			line := "[ -e " + envFile + " ] || : > " + envFile
			if directory := path.Dir(envFile); directory != "." {
				// The directory may be ignored or never committed, and the
				// redirection cannot create it.
				line = "[ -e " + envFile + " ] || { mkdir -p " + directory + " && : > " + envFile + "; }"
			}
			if command.runtime {
				plan.image.runtimeRuns = append(plan.image.runtimeRuns, line)
			} else {
				plan.image.buildRuns = append(plan.image.buildRuns, line)
			}
			plan.findings = append(plan.findings, nodeFinding("env_file_placeholder", PreflightPass,
				"The "+command.label+" command's env file is created when it is missing", "--env-file="+envFile,
				"Node exits when the file --env-file names is missing, and an env file is rarely committed or is left out by .dockerignore; the image creates it empty when the build context has none, and the process environment, where this deployment's variables are, takes precedence over the file.",
				"", "configuration.build."+command.label+"Command"))
		}
	}
}

// nodeEnvFiles are the files a command's `--env-file` flags name, following
// the package scripts it runs. Only a plain relative path is kept: it is
// written into a RUN.
func nodeEnvFiles(scripts map[string]string, command string) []string {
	files := []string{}
	if strings.TrimSpace(command) == "" {
		return files
	}
	for _, segment := range nodeReachedSegments(scripts, []string{command}, false) {
		for _, match := range nodeEnvFileRE.FindAllStringSubmatch(segment, -1) {
			file := strings.TrimPrefix(match[2], "./")
			if match[1] != match[3] || !safeRelativePath(file) || strings.HasPrefix(file, "-") || slices.Contains(files, file) {
				continue
			}
			files = append(files, file)
		}
	}
	return files
}

// buildRun renders the build command's RUN. bound names the variables the
// build step mounts, which decide whether an env-validation schema has to be
// skipped at build time.
func (p nodeInstallPlan) buildRun(mounts, command string, bound map[string]bool) string {
	assignments := []string{}
	if p.buildEnv.legacyOpenSSL != "" {
		// The provider is what makes the build work at all, so an operator's
		// NODE_OPTIONS is kept beside it rather than replacing it.
		assignments = append(assignments, `NODE_OPTIONS="--openssl-legacy-provider${NODE_OPTIONS:+ $NODE_OPTIONS}"`)
	}
	for _, value := range append(p.buildDefaults(), p.buildEnv.framework...) {
		assignments = append(assignments, value.assignment())
	}
	if len(p.buildEnv.env.missing(bound)) > 0 && p.buildEnv.env.skippable {
		assignments = append(assignments, nodeEnvDefault{"SKIP_ENV_VALIDATION", "1"}.assignment())
	}
	if len(assignments) == 0 {
		return "RUN " + mounts + command
	}
	return "RUN " + mounts + "export " + strings.Join(assignments, " ") + " && " + command
}

// missing lists the server variables the schema requires that the build
// does not mount. A name the platform supplies is never missing.
func (v nodeEnvValidation) missing(bound map[string]bool) []string {
	missing := []string{}
	for _, name := range v.server {
		if !bound[name] && !envProvidedNames[name] {
			missing = append(missing, name)
		}
	}
	return missing
}

// boundToBuild names the variables a build's mounts give the build command.
func boundToBuild(secrets []BuildSecretConfig) map[string]bool {
	bound := map[string]bool{}
	for _, secret := range secrets {
		if buildSecretReaches(secret.Step, "build") {
			bound[secret.Variable] = true
		}
	}
	return bound
}

// nodeHeavyBuilds are frameworks, and nodeHeavyPackages packages, whose
// production build peaks around 2 GiB; other frameworks peak around 1 GiB,
// and a plain TypeScript or bundler build around 512 MiB. The figures are
// upstream experience, used only to warn before a build on a host that
// cannot give them.
var (
	nodeHeavyBuilds   = []string{"nextjs", "nuxt", "angular", "gatsby", "docusaurus"}
	nodeHeavyPackages = []string{"@strapi/strapi", "payload"}
)

// nodeBuildMemoryMiB estimates the peak memory of a package's build.
func nodeBuildMemoryMiB(framework, build string, facts nodeInstallFacts) int {
	switch {
	case strings.TrimSpace(build) == "":
		return 0
	case slices.Contains(nodeHeavyBuilds, framework) || slices.ContainsFunc(nodeHeavyPackages, facts.present):
		return 2048
	case framework != "":
		return 1024
	}
	return 512
}

// detectedNodeBuild is the candidate's record of the build, or nil when
// there is nothing for preflight to judge.
func detectedNodeBuild(facts nodeInstallFacts, framework, build string) *DetectedNodeBuild {
	record := DetectedNodeBuild{MemoryMiB: nodeBuildMemoryMiB(framework, build, facts), PrismaEnv: prismaBuildPlaceholders(facts, build)}
	if validation := facts.envValidation; validation.source != "" {
		record.EnvSchema, record.EnvServer, record.EnvClient, record.EnvSkippable =
			validation.source, validation.server, validation.client, validation.skippable
	}
	if record.MemoryMiB == 0 && record.EnvSchema == "" && len(record.PrismaEnv) == 0 {
		return nil
	}
	return &record
}
