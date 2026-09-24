package deploy

import (
	"fmt"
	"strings"
)

// networkFindings re-checks what detection read about where the server
// listens against the plan as it now stands, and says what the proxy is
// trusted with and lets through — before Deploy is pressed, not as a
// readiness timeout afterwards. Detection's facts describe the detected
// start command; when the operator replaced it, only what the configured
// command itself says is re-read.
func networkFindings(draft *Draft, configuration PlanConfiguration) []PreflightFinding {
	findings := []PreflightFinding{}
	selected := selectedDetectionCandidate(draft.Data.Detection)
	build := configuration.Build
	runtime := configuration.Runtime
	profile := draft.Data.Intent.Profile
	serves := (profile == ProfileWeb || profile == ProfileService) && !runtime.HostNetwork
	built := build.Method == BuildRecipe || build.Method == BuildDockerfile
	if serves && built && selected != nil {
		findings = append(findings, listenFindings(draft, configuration, selected)...)
	}
	if build.Method == BuildRecipe && build.Recipe == "python" && build.StartCommand != "" {
		if facts := parseCommandListen(build.StartCommand, nil); facts.devServer != "" {
			findings = append(findings, finding("start_command_dev_server", PreflightWarning,
				"The start command runs a development server", facts.devServer+": "+boundedEvidence(facts.segment),
				"Development servers reload on file changes, run single-threaded and print debug pages; they are not made to serve the public.",
				"Start with gunicorn or uvicorn without --reload, as detection proposes for this framework.",
				"deploy", "configuration.build.startCommand"))
		}
	}
	findings = append(findings, proxyTrustFindings(draft, configuration, selected)...)
	if len(configuration.Domains) > 0 {
		measured := fmt.Sprintf("%d MB", runtime.EffectiveMaxRequestBodyMB())
		if runtime.MaxRequestBodyMB == 0 {
			measured += " (default)"
		}
		findings = append(findings, finding("request_body_limit", PreflightPass,
			"Uploads up to "+fmt.Sprintf("%d MB", runtime.EffectiveMaxRequestBodyMB())+" reach the application", measured,
			"The proxy answers a larger request with 413 before the application sees it.", "",
			"proxy", "runtime.maxRequestBodyMb"))
	}
	return findings
}

// recipeHostVariables are the host variables each recipe's image sets, which
// move a server that reads them off loopback.
func recipeHostVariables(recipe string) map[string]bool {
	switch recipe {
	case "node":
		return map[string]bool{"HOST": true}
	case "python":
		return map[string]bool{"UVICORN_HOST": true, "FLASK_RUN_HOST": true}
	}
	return nil
}

func listenFindings(draft *Draft, configuration PlanConfiguration, selected *DetectedCandidate) []PreflightFinding {
	findings := []PreflightFinding{}
	build := configuration.Build
	port := configuration.Runtime.InternalPort
	listen := DetectedListen{}
	if selected.Listen != nil {
		listen = *selected.Listen
	}
	commandField := "configuration.build.startCommand"
	sourceField := "configuration.build"
	// The detected facts describe the detected command; a replaced one is
	// read on its own, with no package scripts to follow.
	if build.Method == BuildRecipe && strings.TrimSpace(build.StartCommand) != strings.TrimSpace(selected.StartCommand) {
		command := parseCommandListen(build.StartCommand, nil)
		codeListen := listen
		listen = DetectedListen{Unbridged: codeListen.Unbridged}
		// What the code and its configuration say still holds; what the
		// detected command said does not, and neither does a bridge only the
		// default start command writes.
		if !fromStartCommand(codeListen.PortFrom) && !fromStartCommand(codeListen.ReadsPortFrom) &&
			!strings.HasPrefix(codeListen.ReadsPortFrom, "the recipe passes PORT") {
			listen.Port, listen.PortFrom = codeListen.Port, codeListen.PortFrom
			listen.ReadsPort, listen.ReadsPortFrom = codeListen.ReadsPort, codeListen.ReadsPortFrom
		}
		if codeListen.Loopback != "" && !fromStartCommand(codeListen.LoopbackFrom) {
			listen.Loopback, listen.LoopbackFrom, listen.LoopbackCertain = codeListen.Loopback, codeListen.LoopbackFrom, codeListen.LoopbackCertain
			listen.LoopbackVariable = codeListen.LoopbackVariable
			if !strings.HasPrefix(codeListen.LoopbackRecipeFix, "Kestrel__") && !strings.HasPrefix(codeListen.LoopbackRecipeFix, "URLS=") {
				listen.LoopbackRecipeFix = codeListen.LoopbackRecipeFix
			}
		}
		if host, movedBy := command.boundHost(recipeHostVariables(build.Recipe)); host != "" && loopbackHost(host) {
			listen.Loopback, listen.LoopbackFrom, listen.LoopbackCertain = host, "start command: "+boundedEvidence(command.segment), true
			listen.LoopbackRecipeFix, listen.LoopbackVariable = "", ""
		} else if movedBy != "" {
			listen.Loopback, listen.LoopbackFrom, listen.LoopbackRecipeFix = command.defaultHost, command.tool+" without a host binds "+command.defaultHost, movedBy+"=0.0.0.0"
		}
		switch {
		case command.port > 0:
			listen.Port, listen.PortFrom = command.port, "start command: "+boundedEvidence(command.segment)
		case command.followsPort:
			listen.ReadsPort, listen.ReadsPortFrom = true, "start command passes $PORT"
		case command.defaultPort > 0:
			listen.Port, listen.PortFrom = command.defaultPort, command.tool+" default port"
		}
	}
	if listen.Loopback != "" {
		fromCommand := fromStartCommand(listen.LoopbackFrom)
		field := sourceField
		if fromCommand {
			field = commandField
		}
		values := draft.variableValues(configuration)
		switch {
		case listen.LoopbackRecipeFix != "" && build.Method == BuildRecipe:
			findings = append(findings, finding("listen_loopback_moved", PreflightPass,
				"The server's loopback default is moved to every interface", listen.LoopbackFrom,
				"The recipe sets "+listen.LoopbackRecipeFix+", so the proxy and the readiness check can reach it.", "", "deploy", field))
		case listen.LoopbackVariable != "" && values[listen.LoopbackVariable] != "" && !loopbackHost(values[listen.LoopbackVariable]):
			findings = append(findings, finding("listen_loopback_moved", PreflightPass,
				"The server's bind address comes from "+listen.LoopbackVariable, listen.LoopbackFrom,
				"The plan sets "+listen.LoopbackVariable+", so the server listens where the proxy can reach it.", "", "deploy", "variables."+listen.LoopbackVariable))
		default:
			severity := PreflightWarning
			if listen.LoopbackCertain {
				severity = PreflightBlocked
			}
			action := "Bind 0.0.0.0 instead — pass host: \"0.0.0.0\" to the listen call, or read the address from HOST."
			switch {
			case listen.LoopbackVariable != "":
				action = "Set " + listen.LoopbackVariable + "=0.0.0.0; the server reads its bind address from it."
				field = "variables." + listen.LoopbackVariable
			case fromCommand:
				action = "Change the start command to bind 0.0.0.0 (for example --host 0.0.0.0)."
			}
			findings = append(findings, finding("listen_loopback", severity,
				"The server listens on "+listen.Loopback+" inside its container", listen.LoopbackFrom,
				"Only the container itself reaches "+listen.Loopback+"; the proxy and the readiness check connect from outside it, so every request is refused.",
				action, "deploy", field))
		}
	}
	if listen.Unbridged != "" {
		findings = append(findings, finding("listen_endpoints_unbridged", PreflightWarning,
			"Kestrel endpoints stay where appsettings puts them", listen.Unbridged,
			"The recipe moves one HTTP endpoint onto the planned port; several, or an HTTPS one, keep their own addresses and ports.",
			"Keep one HTTP endpoint in appsettings.json, or set the application port to the one it names.", "deploy", "runtime.internalPort"))
	}
	switch {
	case listen.Port > 0 && port > 0 && listen.Port != port:
		findings = append(findings, finding("port_hardcoded", PreflightWarning,
			fmt.Sprintf("The server listens on %d whatever PORT says", listen.Port), listen.PortFrom,
			fmt.Sprintf("The plan routes and checks port %d, where nothing listens.", port),
			fmt.Sprintf("Set the application port to %d, or make the server read PORT.", listen.Port), "deploy", "runtime.internalPort"))
	case listen.Port > 0 && listen.Port == port:
		findings = append(findings, finding("port_from_source", PreflightPass,
			fmt.Sprintf("Port %d is the one the source listens on", port), listen.PortFrom,
			"The route and the readiness check use the port the server fixes.", "", "deploy", "runtime.internalPort"))
	case listen.ReadsPort && port > 0:
		findings = append(findings, finding("port_from_source", PreflightPass,
			"The server listens on the PORT the runtime injects", listen.ReadsPortFrom,
			fmt.Sprintf("The runtime sets PORT=%d, so any application port works.", port), "", "deploy", "runtime.internalPort"))
	}
	return findings
}

// fromStartCommand reports a listen fact detection read from the detected
// start command rather than from code or configuration.
func fromStartCommand(from string) bool {
	return strings.HasPrefix(from, "start command") || strings.Contains(from, " without a host binds ") || strings.HasSuffix(from, " default port")
}

// proxyTrustFindings says which settings make the application believe the
// proxy's forwarded headers, and warns when the plan gives that trust up or
// removed a variable detection proposed for it.
func proxyTrustFindings(draft *Draft, configuration PlanConfiguration, selected *DetectedCandidate) []PreflightFinding {
	findings := []PreflightFinding{}
	if len(configuration.Domains) == 0 {
		return findings
	}
	framework := configuration.Build.Framework
	if framework == "" && selected != nil {
		framework = selected.Framework
	}
	var trusted []string
	if configuration.Build.Method == BuildRecipe {
		trusted = trustEnvironment(recipeProxyTrust(configuration.Build.Recipe, framework))
	}
	values := draft.variableValues(configuration)
	if selected != nil {
		for _, variable := range selected.NetworkVariables {
			if variable.Name == "HOST" {
				continue
			}
			// Names only: a value typed into the environment is sealed with
			// the draft, and a finding is no place to echo it.
			if value, set := values[variable.Name]; set && value != "" {
				trusted = append(trusted, variable.Name)
				continue
			}
			_, set := values[variable.Name]
			if !set && variable.DomainTemplate == "" {
				findings = append(findings, finding("proxy_trust_variable_missing", PreflightWarning,
					variable.Name+" was proposed but is not in the plan", variable.Reason,
					"Without it the application does not trust the host the proxy forwards, and refuses sign-in or form requests.",
					"Add "+variable.Name+"="+variable.Value+" as a runtime variable.", "deploy", "variables."+variable.Name))
			}
		}
	}
	if len(trusted) == 0 {
		return findings
	}
	if publishesPublicly(configuration.Runtime) {
		findings = append(findings, finding("forwarded_headers_untrusted", PreflightWarning,
			"Forwarded headers are not trusted while the port is public", configuration.Runtime.BindAddress,
			"Anyone can reach the container without the proxy and forge X-Forwarded-*, so the runtime switches the recipe's trust back off; behind the proxy the application sees http:// and the proxy's address.",
			"Publish on 127.0.0.1 so the proxy is the only way in.", "deploy", "runtime.bindAddress"))
		return findings
	}
	return append(findings, finding("proxy_headers_trusted", PreflightPass,
		"The application trusts the proxy's forwarded headers", strings.Join(trusted, " "),
		"Redirects, callback URLs and client addresses use the visitor's https URL and address, not the proxy's.", "", "deploy", "variables"))
}
