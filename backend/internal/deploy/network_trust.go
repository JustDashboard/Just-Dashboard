package deploy

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// Every routed deployment is reached the same way: the managed proxy
// terminates TLS and forwards plain HTTP from its own address, adding
// X-Forwarded-Proto, X-Forwarded-Host and X-Forwarded-For. A framework that
// does not believe those headers thinks it is served over http from the
// proxy's address, and generates http:// redirects and OAuth callbacks,
// refuses its own form posts and rate-limits everyone as one client. The
// recipes switch each framework's trust on, because the proxy is the only
// thing that reaches a container published on loopback, and the proxy
// replaces whatever a client sent in those headers. A release the proxy does
// not front alone — no route, a public interface, host networking — gives up
// that guarantee, so the runtime switches the recipe image's trust back off
// for it; see withdrawnProxyTrust.

// proxyTrustSetting is one environment setting a recipe writes so its
// framework believes the proxy, with the value the runtime writes instead
// for a container anyone can reach directly.
type proxyTrustSetting struct {
	name, value, withdrawn string
}

var (
	pythonProxyTrust = []proxyTrustSetting{
		// gunicorn, uvicorn and the uvicorn inside Gradio, Chainlit and
		// NiceGUI all read it; the default trusts only 127.0.0.1, and the
		// proxy reaches the container from a Docker network address.
		{"FORWARDED_ALLOW_IPS", "*", "127.0.0.1"},
	}
	sveltekitProxyTrust = []proxyTrustSetting{
		{"PROTOCOL_HEADER", "x-forwarded-proto", ""},
		// adapter-node before 5.5 reads the host from whatever header this
		// names, with no fallback: an empty name makes every origin
		// https://undefined and refuses every form action. host is its own
		// default.
		{"HOST_HEADER", "x-forwarded-host", "host"},
		// adapter-node's getClientAddress throws when this header is absent,
		// so it is only safe where every request comes through the proxy.
		{"ADDRESS_HEADER", "x-forwarded-for", ""}, {"XFF_DEPTH", "1", "1"},
	}
	dotnetProxyTrust = []proxyTrustSetting{{"ASPNETCORE_FORWARDEDHEADERS_ENABLED", "true", "false"}}
	springProxyTrust = []proxyTrustSetting{{"SERVER_FORWARD_HEADERS_STRATEGY", "framework", "none"}}
	quarkusTrust     = []proxyTrustSetting{
		{"QUARKUS_HTTP_PROXY_PROXY_ADDRESS_FORWARDING", "true", "false"}, {"QUARKUS_HTTP_PROXY_ALLOW_X_FORWARDED", "true", "false"},
	}
)

// recipeProxyTrust is what the automatic recipe sets for a recipe and
// framework, in the order it writes them.
func recipeProxyTrust(recipe, framework string) []proxyTrustSetting {
	switch {
	case recipe == "python":
		return pythonProxyTrust
	case recipe == "node" && framework == "sveltekit":
		return sveltekitProxyTrust
	case recipe == "dotnet" && framework == "aspnet":
		return dotnetProxyTrust
	case recipe == "java" && framework == "spring-boot":
		return springProxyTrust
	case recipe == "java" && framework == "quarkus":
		return quarkusTrust
	}
	return nil
}

// proxyTrustSettings is every setting any recipe writes, once per name, in
// name order.
func proxyTrustSettings() []proxyTrustSetting {
	seen := map[string]bool{}
	var settings []proxyTrustSetting
	for _, recipe := range [][]proxyTrustSetting{pythonProxyTrust, sveltekitProxyTrust, dotnetProxyTrust, springProxyTrust, quarkusTrust} {
		for _, setting := range recipe {
			if !seen[setting.name] {
				seen[setting.name] = true
				settings = append(settings, setting)
			}
		}
	}
	sort.Slice(settings, func(i, j int) bool { return settings[i].name < settings[j].name })
	return settings
}

// imageProxyTrust names the trust settings a recipe build's Dockerfile sets
// in its final stage — the ones the runtime may withdraw. A repository's own
// Dockerfile or a pulled image is never second-guessed: whatever it bakes in
// is its author's decision, and the runtime writes nothing over it.
func imageProxyTrust(prepared PreparedBuild) []string {
	if prepared.Method != BuildRecipe {
		return nil
	}
	trusted := map[string]string{}
	for _, setting := range proxyTrustSettings() {
		trusted[setting.name] = setting.value
	}
	set := map[string]bool{}
	for _, raw := range strings.Split(prepared.DockerfilePreview, "\n") {
		line := strings.TrimSpace(raw)
		switch keyword, rest, _ := strings.Cut(line, " "); strings.ToUpper(keyword) {
		case "FROM":
			set = map[string]bool{}
		case "ENV":
			for _, field := range strings.Fields(rest) {
				name, value, ok := strings.Cut(field, "=")
				if ok && trusted[name] != "" && trusted[name] == strings.Trim(value, `"`) {
					set[name] = true
				}
			}
		}
	}
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func trustEnvironment(settings []proxyTrustSetting) []string {
	lines := make([]string, 0, len(settings))
	for _, setting := range settings {
		lines = append(lines, setting.name+"="+setting.value)
	}
	return lines
}

// nodeServerRuntimeEnv is the runtime stage's environment for a Node server:
// HOST=0.0.0.0 for every server that binds the address in HOST — Nuxt 2,
// Nitro, adapter-node, Adonis and code written `process.env.HOST ||
// 'localhost'` — which is harmless where HOST is unused, the framework's
// proxy trust, and the framework's own settings, which win on a name.
func nodeServerRuntimeEnv(framework string, frameworkEnv []string) []string {
	env := append([]string{"HOST=0.0.0.0"}, trustEnvironment(recipeProxyTrust("node", framework))...)
	for _, setting := range frameworkEnv {
		name, _, _ := strings.Cut(setting, "=")
		kept := env[:0]
		for _, existing := range env {
			if existingName, _, _ := strings.Cut(existing, "="); existingName != name {
				kept = append(kept, existing)
			}
		}
		env = append(kept, setting)
	}
	return env
}

// pythonRecipeNetworkEnv is the Python image's environment: proxy trust,
// and the hosts uvicorn and `flask run` read when their command names none,
// since both default to 127.0.0.1. An explicit --host still wins.
func pythonRecipeNetworkEnv() string {
	return "ENV " + strings.Join(append(trustEnvironment(pythonProxyTrust), "UVICORN_HOST=0.0.0.0", "FLASK_RUN_HOST=0.0.0.0"), " ")
}

// javaRuntimeStart is the default start command of a framework that reads
// its port from configuration: the environment variable that outranks
// application.properties is set from the PORT the runtime injects, and exec
// keeps java as PID 1 so it receives SIGTERM. Frameworks without such a
// variable run as before.
func javaRuntimeStart(framework string) string {
	bridge := jvmPortBridge[framework]
	if bridge == "" {
		return ""
	}
	return "exec env " + bridge + "=${PORT:-8080} java -jar /app/app.jar"
}

// Rocket's default address is 127.0.0.1. ROCKET_ADDRESS is image
// environment, so a custom start command keeps it; the default start bridges
// PORT into ROCKET_PORT. Both outrank Rocket.toml.
const (
	rocketAddressEnv = "ENV ROCKET_ADDRESS=0.0.0.0"
	rocketStart      = "exec env ROCKET_PORT=${PORT:-8000} /app"
)

// kestrelBridge is what the default start command sets so Kestrel listens on
// PORT on every interface despite an appsettings file: the one named
// endpoint's URL, or Urls. Kestrel endpoints replace Urls and the
// ASPNETCORE_HTTP_PORTS bridge entirely, and several endpoints cannot share
// one port, so those are named for a warning instead of bridged.
func kestrelBridge(settings kestrelSettings) ([]string, string) {
	const url = "http://+:${PORT:-8080}"
	switch len(settings.endpoints) {
	case 0:
		if settings.urls != "" {
			return []string{"URLS=" + url}, ""
		}
		return nil, ""
	case 1:
		for name, endpoint := range settings.endpoints {
			if strings.Contains(name, "__") || !strings.HasPrefix(strings.ToLower(endpoint), "http://") {
				return nil, "the Kestrel endpoint " + name + " in appsettings.json (" + endpoint + ") cannot be moved to the planned port"
			}
			return []string{"Kestrel__Endpoints__" + name + "__Url=" + url}, ""
		}
	}
	names := make([]string, 0, len(settings.endpoints))
	for name := range settings.endpoints {
		names = append(names, name)
	}
	sort.Strings(names)
	return nil, "appsettings.json declares " + strings.Join(names, ", ") + " as Kestrel endpoints, which cannot all move to the planned port"
}

// dotnetRuntimeStart is the ASP.NET Core default start command: Kestrel's
// port comes from ASPNETCORE_HTTP_PORTS, not PORT, and an appsettings
// endpoint or Urls outranks that, so the shell bridges the PORT the runtime
// injects into each.
func dotnetRuntimeStart(assembly string, bridge []string) string {
	return strings.Join(append(append([]string{"ASPNETCORE_HTTP_PORTS=${PORT:-8080}"}, bridge...), "dotnet /app/"+assembly+".dll"), " ")
}

// publicExposure says how the application's port is reachable without the
// proxy — host networking, a public bind address, or an extra published port
// that is the application's own on every interface — or "" when the proxy is
// the only way in. Forwarded headers the application trusts could then come
// from anyone.
func publicExposure(plan RuntimePlanConfig) string {
	switch {
	case plan.HostNetwork:
		return "host network"
	case plan.BindAddress == "0.0.0.0" || plan.BindAddress == "::":
		return plan.BindAddress
	}
	for _, published := range plan.Ports {
		if published.ContainerPort == plan.InternalPort && published.effectiveProtocol() == "tcp" &&
			(published.BindAddress == "" || published.BindAddress == "0.0.0.0" || published.BindAddress == "::") {
			return fmt.Sprintf("port %d published on host port %d on every interface", published.ContainerPort, published.HostPort)
		}
	}
	return ""
}

func publishesPublicly(plan RuntimePlanConfig) bool {
	return publicExposure(plan) != ""
}

// withdrawnProxyTrust is the environment that switches the recipe image's
// proxy trust back off when the proxy does not front the release alone:
// with no route nothing adds the forwarded headers (and adapter-node's
// ADDRESS_HEADER then throws on every request that asks for the client),
// and with a public port anyone can forge them. A variable the plan sets
// itself always wins.
func withdrawnProxyTrust(snapshot runtimeReleaseSnapshot, variables map[string]string) []dockerx.EnvVar {
	if len(snapshot.ProxyTrust) == 0 || (len(snapshot.Domains) > 0 && !publishesPublicly(snapshot.Plan)) {
		return nil
	}
	baked := map[string]bool{}
	for _, name := range snapshot.ProxyTrust {
		baked[name] = true
	}
	var environment []dockerx.EnvVar
	for _, setting := range proxyTrustSettings() {
		if _, explicit := variables[setting.name]; baked[setting.name] && !explicit && setting.withdrawn != setting.value {
			environment = append(environment, dockerx.EnvVar{Name: setting.name, Value: setting.withdrawn})
		}
	}
	return environment
}

// DefaultMaxRequestBodyMB is the upload ceiling a route gets when the plan
// names none: large enough for a phone photo or a document, which nginx's
// own 1 MB default refused before the application saw the request.
// MaxRequestBodyMB bounds a typo, not a use case.
const (
	DefaultMaxRequestBodyMB = proxysvc.DefaultDeploymentMaxBodyMB
	MaxRequestBodyMB        = 10 << 10
)

// requestBodyLimitLabel says what the managed route lets through. With no
// limit in the plan that depends on the proxy: the host nginx driver writes
// DefaultMaxRequestBodyMB, since its own default refused a phone photo, and
// Caddy, which has no default limit, is left without one.
func (c RuntimePlanConfig) requestBodyLimitLabel() string {
	if c.MaxRequestBodyMB == 0 {
		return fmt.Sprintf("default (%d MB on nginx, no limit on Caddy)", DefaultMaxRequestBodyMB)
	}
	return fmt.Sprintf("%d MB", c.MaxRequestBodyMB)
}
