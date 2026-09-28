package deploy

import (
	"fmt"
	"net/url"
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
	selected := rootDetectionCandidate(draft.Data.Detection, configuration.Build)
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
	switch {
	case len(configuration.Domains) == 0:
	case runtime.MaxRequestBodyMB == 0:
		findings = append(findings, finding("request_body_limit", PreflightPass,
			"The proxy's default upload limit applies", runtime.requestBodyLimitLabel(),
			fmt.Sprintf("The host nginx proxy answers a body over %d MB with 413 before the application sees it; the Caddy proxy sets no limit of its own.", DefaultMaxRequestBodyMB),
			"", "proxy", "runtime.maxRequestBodyMb"))
	default:
		findings = append(findings, finding("request_body_limit", PreflightPass,
			"Uploads up to "+runtime.requestBodyLimitLabel()+" reach the application", runtime.requestBodyLimitLabel(),
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
			// The code was read for what the detected command runs; one that
			// no longer runs the same package script may run other code, so
			// its loopback is no longer certain — which is also how a
			// detection the operator knows to be wrong stops blocking.
			listen.Loopback, listen.LoopbackFrom = codeListen.Loopback, codeListen.LoopbackFrom
			listen.LoopbackCertain = codeListen.LoopbackCertain && sameScript(build.StartCommand, selected.StartCommand)
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
	if build.Method != selected.BuildMethod {
		// The facts were read for another way of building it; a Dockerfile
		// runs its own CMD, which may front the code rather than be it.
		listen.LoopbackCertain = false
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
			case selected.Recipe == "gleam":
				action = "Add |> mist.bind(\"0.0.0.0\") to the mist builder."
			}
			title := "The server listens on " + listen.Loopback + " inside its container"
			means := "Only the container itself reaches " + listen.Loopback + "; the proxy and the readiness check connect from outside it, so every request is refused."
			if !listen.LoopbackCertain {
				title = "A listener binds " + listen.Loopback + " inside its container"
				means = "Only the container itself reaches " + listen.Loopback + ". If this is the server the proxy should reach, the proxy and the readiness check cannot connect to it; a debug or admin endpoint on loopback is fine."
			}
			findings = append(findings, finding("listen_loopback", severity, title, listen.LoopbackFrom, means, action, "deploy", field))
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

// sameScript reports two start commands that run the same package script,
// as a package-manager switch rewrites `npm run start` into `bun run start`.
func sameScript(command, detected string) bool {
	script := func(command string) string {
		segments := strings.Split(command, "&&")
		if match := scriptRunRE.FindStringSubmatch(strings.TrimSpace(segments[len(segments)-1])); match != nil {
			return match[1]
		}
		return ""
	}
	name := script(command)
	return name != "" && name == script(detected)
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
	values := draft.variableValues(configuration)
	if selected != nil {
		findings = append(findings, publicURLFindings(configuration, selected, values)...)
	}
	if len(configuration.Domains) == 0 {
		return findings
	}
	framework := configuration.Build.Framework
	if framework == "" && selected != nil {
		framework = selected.Framework
	}
	var recipeTrust, variableTrust []string
	if configuration.Build.Method == BuildRecipe {
		recipeTrust = trustEnvironment(recipeProxyTrust(configuration.Build.Recipe, framework))
	}
	if selected != nil {
		for _, variable := range selected.NetworkVariables {
			if variable.Name == "HOST" || variable.DomainTemplate != "" {
				continue
			}
			// Names only: a value typed into the environment is sealed with
			// the draft, and a finding is no place to echo it.
			value, set := values[variable.Name]
			switch {
			case set && value != "":
				variableTrust = append(variableTrust, variable.Name)
			case !set:
				findings = append(findings, finding("proxy_trust_variable_missing", PreflightWarning,
					variable.Name+" was proposed but is not in the plan", variable.Reason,
					"Without it the application does not trust the host the proxy forwards, and refuses sign-in or form requests.",
					"Add "+variable.Name+"="+variable.Value+" as a runtime variable.", "deploy", "variables."+variable.Name))
			}
		}
	}
	trusted := append(append([]string{}, recipeTrust...), variableTrust...)
	if len(trusted) == 0 {
		return findings
	}
	if exposure := publicExposure(configuration.Runtime); exposure != "" {
		means := "Anyone can reach the container without the proxy and forge X-Forwarded-*."
		action := "Publish on 127.0.0.1, without host networking, so the proxy is the only way in."
		if len(recipeTrust) > 0 {
			means += " The runtime therefore switches the recipe's trust back off (" + strings.Join(trustNames(recipeTrust), ", ") +
				"), and behind the proxy the application sees http:// and the proxy's address."
		}
		if len(variableTrust) > 0 {
			means += " " + strings.Join(variableTrust, ", ") + " is still trusted, because the plan sets it."
			action = "Remove " + strings.Join(variableTrust, ", ") + ", or publish on 127.0.0.1 so the proxy is the only way in."
		}
		findings = append(findings, finding("forwarded_headers_untrusted", PreflightWarning,
			"Forwarded headers are not safe to trust while the port is public", exposure, means, action, "deploy", "runtime.bindAddress"))
		return findings
	}
	return append(findings, finding("proxy_headers_trusted", PreflightPass,
		"The application trusts the proxy's forwarded headers", strings.Join(trusted, " "),
		"Redirects, callback URLs and client addresses use the visitor's https URL and address, not the proxy's.", "", "deploy", "variables"))
}

func trustNames(settings []string) []string {
	names := make([]string, 0, len(settings))
	for _, setting := range settings {
		name, _, _ := strings.Cut(setting, "=")
		names = append(names, name)
	}
	return names
}

// publicURLFindings checks the public-URL variables detection proposed with
// a domain template (next-auth 4's NEXTAUTH_URL): the form fills one from the
// primary domain, and a plan with no domain, or one whose domain moved since,
// is where it goes missing or stale. Only the host of a value is echoed.
func publicURLFindings(configuration PlanConfiguration, selected *DetectedCandidate, values map[string]string) []PreflightFinding {
	findings := []PreflightFinding{}
	for _, variable := range selected.NetworkVariables {
		if variable.DomainTemplate == "" || detectedSetup(selected, variable.Name) == "domain" {
			// A public URL the environment classification set up is answered
			// by its own public_url_* findings (preflight_variables.go).
			continue
		}
		expected := ""
		if len(configuration.Domains) > 0 {
			expected = domainTemplateValue(variable.DomainTemplate, configuration.Domains[0])
		}
		action := "Add a domain, which fills it, or set " + variable.Name + " to the address visitors use."
		if expected != "" {
			action = "Set " + variable.Name + "=" + expected + "."
		}
		field := "variables." + variable.Name
		value, set := values[variable.Name]
		switch host := urlHost(value); {
		case !set:
			findings = append(findings, finding("public_url_variable_missing", PreflightWarning,
				variable.Name+" is not set", variable.Reason,
				"Without it the application builds its sign-in callback URLs for http://localhost:3000, which no visitor reaches.",
				action, "deploy", field))
		case value == "":
			findings = append(findings, finding("public_url_variable_missing", PreflightWarning,
				variable.Name+" is empty", variable.Reason,
				"An empty value is not a URL: next-auth 4 fails every /api/auth request when it reads one.",
				action, "deploy", field))
		case expected != "" && host != "" && !strings.EqualFold(host, urlHost(expected)):
			findings = append(findings, finding("public_url_variable_stale", PreflightWarning,
				variable.Name+" names "+host+", not the primary domain", variable.Name+" host "+host,
				"Sign-in callbacks are sent to "+host+" rather than "+urlHost(expected)+".",
				action, "deploy", field))
		}
	}
	return findings
}

// detectedSetup is how the environment classification supplies a detected
// variable, or "" when it does not.
func detectedSetup(candidate *DetectedCandidate, name string) string {
	for _, variable := range candidate.Variables {
		if variable.Name == name {
			return variable.Setup
		}
	}
	return ""
}

// withoutSupersededHostFindings drops the environment check's
// host_variable_loopback_<name> for a variable a listen_loopback finding
// already names: that finding knows whether the code falls back to loopback,
// and how certainly, so one field is not reported twice.
func withoutSupersededHostFindings(findings []PreflightFinding) []PreflightFinding {
	named := map[string]bool{}
	for _, item := range findings {
		if item.Code == "listen_loopback" && strings.HasPrefix(item.FieldID, "variables.") {
			named[variableFindingCode("host_variable_loopback_", strings.TrimPrefix(item.FieldID, "variables."))] = true
		}
	}
	if len(named) == 0 {
		return findings
	}
	kept := findings[:0]
	for _, item := range findings {
		if !named[item.Code] {
			kept = append(kept, item)
		}
	}
	return kept
}

// domainTemplateValue is what the form fills a domain template with.
func domainTemplateValue(template string, domain PlannedDomain) string {
	scheme := "http"
	if domain.HTTPS {
		scheme = "https"
	}
	hostname := strings.ToLower(strings.TrimSpace(domain.Hostname))
	return strings.NewReplacer("{{hostname}}", hostname, "{{scheme}}", scheme).Replace(template)
}

// urlHost is the host of a URL value, or "" when it is not one.
func urlHost(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" {
		return ""
	}
	return parsed.Hostname()
}
