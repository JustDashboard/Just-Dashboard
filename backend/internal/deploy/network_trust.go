package deploy

import (
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
// replaces whatever a client sent in those headers. A plan that publishes the
// container on a public interface gives up that guarantee, so the runtime
// switches trust back off for it — see proxyTrustWithdrawn.

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
		{"PROTOCOL_HEADER", "x-forwarded-proto", ""}, {"HOST_HEADER", "x-forwarded-host", ""},
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

// proxyTrustWithdrawn is every trust setting's withdrawn value, once per
// name, for the runtime to write when a plan publishes on every interface.
func proxyTrustWithdrawn() []proxyTrustSetting {
	seen := map[string]bool{}
	var withdrawn []proxyTrustSetting
	for _, settings := range [][]proxyTrustSetting{pythonProxyTrust, sveltekitProxyTrust, dotnetProxyTrust, springProxyTrust, quarkusTrust} {
		for _, setting := range settings {
			if setting.withdrawn == setting.value || seen[setting.name] {
				continue
			}
			seen[setting.name] = true
			withdrawn = append(withdrawn, setting)
		}
	}
	sort.Slice(withdrawn, func(i, j int) bool { return withdrawn[i].name < withdrawn[j].name })
	return withdrawn
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

// publishesPublicly reports a routed port published on every interface: the
// proxy is then not the only way in, so the forwarded headers a recipe
// trusts could come from anyone. Host networking is left as it is — the
// operator chose to hand the container the host's own interfaces, and its
// environment has never been adjusted.
func publishesPublicly(plan RuntimePlanConfig) bool {
	return !plan.HostNetwork && (plan.BindAddress == "0.0.0.0" || plan.BindAddress == "::")
}

// withdrawnProxyTrust is the environment that switches every recipe's proxy
// trust back off for a publicly published container. A variable the plan
// sets itself always wins.
func withdrawnProxyTrust(plan RuntimePlanConfig, variables map[string]string) []dockerx.EnvVar {
	if !publishesPublicly(plan) {
		return nil
	}
	var environment []dockerx.EnvVar
	for _, setting := range proxyTrustWithdrawn() {
		if _, explicit := variables[setting.name]; !explicit {
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

// EffectiveMaxRequestBodyMB is the largest body the managed route is known to
// let through: the plan's limit, else the default nginx enforces (Caddy
// enforces none by default, so it lets at least as much through).
func (c RuntimePlanConfig) EffectiveMaxRequestBodyMB() int {
	if c.MaxRequestBodyMB == 0 {
		return DefaultMaxRequestBodyMB
	}
	return c.MaxRequestBodyMB
}
