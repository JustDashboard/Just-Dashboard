package deploy

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Findings about what a build will meet that neither detection nor the
// recipe can know alone: the host's memory, the values the configuration
// gives the build, and platform variables an operator's pasted .env sets.
// Values are compared, never echoed, except a port number, a NODE_ENV word
// and a host name, which are not secrets.

// buildMemoryFindings warns when the memory and swap the host has free are
// below what the selected recipe's build is estimated to peak at.
func buildMemoryFindings(candidate *DetectedCandidate, configuration PlanConfiguration, observation HostObservation) []PreflightFinding {
	build := configuration.Build
	if candidate == nil || build.Method != BuildRecipe || observation.AvailableMemory <= 0 {
		return nil
	}
	estimate, label := 0, ""
	switch build.Recipe {
	case "node":
		if strings.TrimSpace(build.BuildCommand) == "" {
			return nil
		}
		estimate, label = 1024, "this build"
		if candidate.NodeBuild != nil && candidate.NodeBuild.MemoryMiB > 0 {
			estimate = candidate.NodeBuild.MemoryMiB
		}
		if framework := nodeFrameworkByName(candidate.Framework); framework != nil {
			label = "a " + framework.Label + " build"
		}
	case "php":
		if len(candidate.NodeInstalls) > 0 {
			estimate, label = 1024, "the asset build"
		}
	case "rust":
		estimate, label = 2048, "a Rust release build"
		if facts := candidate.Rust; facts != nil {
			switch {
			case facts.Fullstack == "leptos":
				estimate, label = 3072, "a Leptos build (server and WebAssembly site)"
			case facts.HeavyRelease != "":
				estimate, label = 3072, "a Rust release build with "+facts.HeavyRelease
			}
		}
	case "java":
		estimate, label = 1536, "a Maven or Gradle build"
	case "dotnet":
		estimate, label = 1536, "dotnet publish"
	}
	free := observation.AvailableMemory + observation.AvailableSwap
	if estimate == 0 || free >= int64(estimate)<<20 {
		return nil
	}
	return []PreflightFinding{finding("build_memory_low", PreflightWarning,
		"The build may run out of memory",
		fmt.Sprintf("%d MiB free (%d MiB memory, %d MiB swap); %s peaks around %d MiB", free>>20, observation.AvailableMemory>>20, observation.AvailableSwap>>20, label, estimate),
		buildMemoryMeans(build.Recipe),
		"Add swap (2 GiB is usually enough), stop other workloads while it builds, or build the image elsewhere and deploy it as an image.",
		"metrics", "")}
}

// buildMemoryMeans is what running short does to the build, and what the
// recipe already does about it.
func buildMemoryMeans(recipe string) string {
	means := "The build can be killed part way through (exit 137) or stop with \"JavaScript heap out of memory\", and while it runs the services on this server have less memory."
	if recipe == "rust" {
		means = "rustc can be killed part way through (exit 137), and while it runs the services on this server have less memory. The recipe runs as many compile jobs as whole gigabytes are free, which lowers the peak but not the link of the final binary."
	}
	return means
}

// buildValue reports whether the build command receives a variable: it is
// build-scoped, mapped to a step that reaches the build, and has a value.
func buildValue(configuration PlanConfiguration, values map[string]string, name string) bool {
	for _, secret := range configuration.Build.Secrets {
		if secret.Variable == name && !buildSecretReaches(secret.Step, "build") {
			return false
		}
	}
	return slices.ContainsFunc(configuration.Variables, func(variable PlannedVariable) bool {
		return variable.Name == name && slices.Contains(variable.Scopes, "build")
	}) && values[name] != ""
}

func runtimeValue(configuration PlanConfiguration, values map[string]string, name string) bool {
	return slices.ContainsFunc(configuration.Variables, func(variable PlannedVariable) bool {
		return variable.Name == name && slices.Contains(variable.Scopes, "runtime")
	}) && values[name] != ""
}

// buildEnvValidationFindings judges an env-validation schema the build
// imports against the values the build receives.
func buildEnvValidationFindings(candidate *DetectedCandidate, configuration PlanConfiguration, values map[string]string) []PreflightFinding {
	record := candidate.NodeBuild
	if record == nil || record.EnvSchema == "" || strings.TrimSpace(configuration.Build.BuildCommand) == "" {
		return nil
	}
	// Without the skip, a name detection read as a required build-time
	// value is already refused on its own field (build_variable_missing_*).
	judged := func(name string) bool {
		if record.EnvSkippable {
			return false
		}
		return slices.ContainsFunc(candidate.Variables, func(variable DetectedVariable) bool {
			return variable.Name == name && variable.Phase == "build" && variable.Required
		})
	}
	findings := []PreflightFinding{}
	server, unset := []string{}, []string{}
	for _, name := range record.EnvServer {
		if envProvidedNames[name] || buildValue(configuration, values, name) || judged(name) {
			continue
		}
		server = append(server, name)
		if !runtimeValue(configuration, values, name) {
			unset = append(unset, name)
		}
	}
	switch {
	case len(server) == 0:
	case record.EnvSkippable:
		item := finding("build_env_validation_skipped", PreflightPass,
			"Environment validation is skipped during the build", strings.Join(boundedNames(server), ", "),
			record.EnvSchema+" validates these server variables when the build imports it, and the build has no value for them; the build runs with SKIP_ENV_VALIDATION=1, and the server validates them when it starts.",
			"", "deploy", "variables."+server[0])
		if len(unset) > 0 {
			item.Severity = PreflightWarning
			item.Means += " " + strings.Join(boundedNames(unset), ", ") + " have no value at all, so the server will refuse to start."
			item.Action = "Set " + strings.Join(boundedNames(unset), ", ") + " before deploying."
			item.FieldID = "variables." + unset[0]
		}
		findings = append(findings, item)
	default:
		findings = append(findings, finding("build_env_missing", PreflightWarning,
			"The build validates variables it has no value for", strings.Join(boundedNames(server), ", "),
			record.EnvSchema+" validates these server variables when the build imports it, and the build receives none of them, so the build stops with \"Invalid environment variables\".",
			"Give these variables build scope, or add skipValidation: !!process.env.SKIP_ENV_VALIDATION to createEnv so the build can skip it.",
			"deploy", "variables."+server[0]))
	}
	client := []string{}
	for _, name := range record.EnvClient {
		if !buildValue(configuration, values, name) && !judged(name) {
			client = append(client, name)
		}
	}
	if len(client) > 0 {
		findings = append(findings, finding("build_env_client_missing", PreflightWarning,
			"Browser variables have no build value", strings.Join(boundedNames(client), ", "),
			"The build compiles these into the JavaScript browsers download, so without a value there they are undefined in the browser whatever the server has.",
			"Give them a value with build scope.", "deploy", "variables."+client[0]))
	}
	return findings
}

// nodeBuildSkipsValidation is the env schema a Node recipe build imports
// when the build runs past it with SKIP_ENV_VALIDATION, which it does for a
// schema that honours the variable.
func nodeBuildSkipsValidation(candidate *DetectedCandidate, configuration PlanConfiguration) *DetectedNodeBuild {
	if candidate == nil || candidate.NodeBuild == nil || !candidate.NodeBuild.EnvSkippable || candidate.NodeBuild.EnvSchema == "" ||
		configuration.Build.Method != BuildRecipe || configuration.Build.Recipe != "node" || strings.TrimSpace(configuration.Build.BuildCommand) == "" {
		return nil
	}
	return candidate.NodeBuild
}

// nodePrerenderFrameworks render pages during the build, which is where a
// page that queries its database at build time runs.
var nodePrerenderFrameworks = []string{"nextjs", "nuxt", "astro", "sveltekit", "gatsby"}

// buildDatabaseFindings warns when a prerendering build is given a
// database it cannot reach: a linked database's db-N.jd.internal alias
// exists only on the environment network the running release joins, and
// loopback inside the build is the build container itself.
func buildDatabaseFindings(candidate *DetectedCandidate, configuration PlanConfiguration, values map[string]string) []PreflightFinding {
	if !slices.Contains(nodePrerenderFrameworks, candidate.Framework) || strings.TrimSpace(configuration.Build.BuildCommand) == "" {
		return nil
	}
	database := candidate.SchemaTool != "" || slices.ContainsFunc(candidate.Databases, func(detected DetectedDatabase) bool {
		return detected.Engine != "redis"
	})
	if !database {
		return nil
	}
	findings := []PreflightFinding{}
	for _, variable := range configuration.Variables {
		if !buildValue(configuration, values, variable.Name) {
			continue
		}
		measured := ""
		if reference, err := ParseVariableReference(variable.Reference); err == nil && reference.Kind == "database" {
			measured = variable.Name + " is a linked database, which only the running release can reach"
		} else if host := buildValueHost(values[variable.Name]); strings.HasSuffix(host, ".jd.internal") {
			measured = variable.Name + " points at " + host + ", which only the running release can resolve"
		} else if host != "" && nodeLoopbackHost(host) {
			measured = variable.Name + " points at " + host + ", which inside the build is the build container itself"
		}
		if measured == "" {
			continue
		}
		item := finding("build_database_unreachable", PreflightWarning,
			"The build cannot reach the database", measured,
			"The build runs apart from the environment's network, so a page the build prerenders that queries the database fails the build (\"Can't reach database server\", ENOTFOUND or ECONNREFUSED); pages rendered on request are unaffected.",
			"Render pages that read the database on request (export const dynamic = \"force-dynamic\" or await connection() in Next.js), or remove "+variable.Name+"'s build scope if the build does not read it.",
			"deploy", "variables."+variable.Name)
		if !buildReadNeedsValue(candidate, configuration, variable.Name) {
			// Nothing detection saw reads it while the build runs, so the
			// build scope only hands the build an address it cannot reach.
			item.Action = "Nothing in the build reads " + variable.Name + ", so remove its build scope; if a page the build prerenders queries the database, render it on request instead (export const dynamic = \"force-dynamic\" or await connection() in Next.js)."
			item.Fix = &CauseFix{Kind: fixRemoveVariableScope, Field: "variables." + variable.Name, Scope: "build"}
		}
		findings = append(findings, item)
	}
	return findings
}

// buildReadNeedsValue says whether the plan's build reads the variable
// itself: detection saw a read while the build runs — a framework config
// file, a static env import, a browser prefix — that the recipe does not
// supply with a placeholder of its own.
func buildReadNeedsValue(candidate *DetectedCandidate, configuration PlanConfiguration, name string) bool {
	if slices.Contains(prismaRecipeSupplied(candidate, configuration), name) {
		return false
	}
	for _, variable := range candidate.Variables {
		if variable.Name == name {
			return variable.Phase == "build" || variable.BrowserInlined
		}
	}
	return false
}

// buildValueHost is the host of a URL-shaped value, empty for anything else.
func buildValueHost(value string) string {
	if !strings.Contains(value, "://") {
		return ""
	}
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}

func nodeLoopbackHost(host string) bool {
	host = strings.Trim(strings.ToLower(host), "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

var nodeEnvWordRE = regexp.MustCompile(`^[a-z]{1,16}$`)

// platformVariableFindings warns about variables that replace what the
// deployment sets itself, typically pasted from a local .env: PORT, which
// then differs from the port the proxy and readiness check use; NODE_ENV,
// which undoes the recipe's production build and runtime; and a HOST or
// HOSTNAME on loopback, which frameworks bind to.
func platformVariableFindings(configuration PlanConfiguration, values map[string]string) []PreflightFinding {
	findings := []PreflightFinding{}
	scoped := func(name, scope string) bool {
		return slices.ContainsFunc(configuration.Variables, func(variable PlannedVariable) bool {
			return variable.Name == name && slices.Contains(variable.Scopes, scope)
		}) && values[name] != ""
	}
	runtime := configuration.Runtime
	served := configuration.Build.Method != BuildCompose && configuration.Build.Method != BuildLegacyCompose &&
		configuration.Build.Method != BuildStatic && configuration.Build.OutputDirectory == ""
	if served && scoped("PORT", "runtime") && runtime.InternalPort > 0 && !runtime.HostNetwork {
		if port, err := strconv.Atoi(strings.TrimSpace(values["PORT"])); err == nil && port != runtime.InternalPort {
			findings = append(findings, finding("port_variable_mismatch", PreflightWarning,
				"PORT differs from the internal port", fmt.Sprintf("PORT=%d; the internal port is %d", port, runtime.InternalPort),
				fmt.Sprintf("The deployment sets PORT to the internal port unless a variable does; this one makes the server listen on %d while the proxy and the readiness check connect to %d.", port, runtime.InternalPort),
				fmt.Sprintf("Remove PORT, which the deployment sets itself, or set the internal port to %d.", port), "deploy", "variables.PORT"))
		}
	}
	if configuration.Build.Method == BuildRecipe && configuration.Build.Recipe == "node" {
		value := strings.TrimSpace(values["NODE_ENV"])
		if value != "" && value != "production" && (scoped("NODE_ENV", "build") || scoped("NODE_ENV", "runtime")) {
			measured := "NODE_ENV is set to another value"
			if nodeEnvWordRE.MatchString(value) {
				measured = "NODE_ENV=" + value
			}
			reaches := []string{}
			if scoped("NODE_ENV", "build") {
				reaches = append(reaches, "the build")
			}
			if scoped("NODE_ENV", "runtime") {
				reaches = append(reaches, "the running server")
			}
			findings = append(findings, finding("node_env_not_production", PreflightWarning,
				"NODE_ENV is not production", measured,
				"It reaches "+strings.Join(reaches, " and ")+", replacing the production mode the recipe builds and runs in: frameworks build and serve their development variants.",
				"Remove NODE_ENV; the recipe sets production.", "deploy", "variables.NODE_ENV"))
		}
	}
	// HOST is a bind address like BIND or LISTEN_HOST, which the environment
	// check names for every recipe (host_variable_loopback_host). HOSTNAME
	// is one only to Next.js standalone and the servers that copy it, so it
	// is named here, under the same code shape.
	if served && scoped("HOSTNAME", "runtime") {
		if host := strings.TrimSpace(values["HOSTNAME"]); nodeLoopbackHost(host) {
			findings = append(findings, finding("host_variable_loopback_hostname", PreflightWarning,
				"HOSTNAME binds the server to loopback", "HOSTNAME="+host,
				"Servers that bind to HOSTNAME (Next.js standalone, and servers written the same way) then listen only inside the container, where the proxy and the readiness check cannot reach them.",
				"Remove HOSTNAME, or set it to 0.0.0.0.", "deploy", "variables.HOSTNAME"))
		}
	}
	return findings
}
