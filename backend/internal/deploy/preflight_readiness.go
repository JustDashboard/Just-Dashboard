package deploy

import (
	"encoding/json"
	"fmt"
	"strings"
)

// readinessPreflightFindings say, before anything is built, what the
// release's readiness gate will meet: where its check came from, a start
// command that would end the container, a worker planned as a web service,
// a host allowlist or HTTPS redirect that refuses the probe (and visitors),
// and a model downloaded at every start.
func readinessPreflightFindings(draft *Draft, configuration PlanConfiguration) []PreflightFinding {
	findings := []PreflightFinding{}
	selected := rootDetectionCandidate(draft.Data.Detection, configuration.Build)
	profile := draft.Data.Intent.Profile
	findings = append(findings, startCommandFindings(selected, configuration)...)
	if selected != nil && selected.BackgroundWorker != nil && (profile == ProfileWeb || profile == ProfileStatic) {
		worker := selected.BackgroundWorker
		findings = append(findings, finding("web_profile_without_listener", PreflightWarning,
			"This looks like a "+worker.Kind+"; it will never answer the readiness probe", worker.Evidence,
			"A web workload is released only once it answers HTTP on its port, and a process that connects out and never listens fails that gate every time.",
			"Switch the workload to Worker.", "deploy", "detection.profile"))
	}
	if profile != ProfileWeb && profile != ProfileStatic {
		return findings
	}
	var readiness *DetectedReadiness
	if selected != nil && selected.BuildMethod == configuration.Build.Method {
		readiness = selected.Readiness
	}
	check, config, ok := plannedReadinessCheck(configuration.Checks)
	if ok && check.Kind == string(CheckHTTP) {
		route := firstNonEmpty(config.Path, "/")
		if config.AcceptAnyAnswer {
			findings = append(findings, finding("readiness_root_unverified", PreflightPass,
				"Readiness accepts any answer from GET "+route, "any status below 500 except 400 and 421",
				"No health route that must answer 200 was found, so readiness proves the server answers rather than that this page works; a release that answers 404 everywhere would still go live.",
				"Add a health route that returns 200 (for example /health) and point the readiness check at it.", "deploy", "checks"))
		}
		if readiness != nil && readiness.RootRoute == "unrouted" && route == "/" && !config.AcceptAnyAnswer && len(config.ExpectedStatus) == 0 {
			findings = append(findings, finding("readiness_path_unrouted", PreflightWarning,
				"Readiness asks for a page at / that the application does not route", "GET / expecting 2xx; no route for / in the application",
				"The application's router answers an unrouted path with 404, so every attempt fails although the service is healthy.",
				"Point the readiness check at a route the application serves, or accept any answer from /.", "deploy", "checks"))
		}
	}
	if readiness != nil && ok && readiness.Source != readinessFromConvention &&
		((check.Kind == string(CheckHTTP) && readiness.Kind == string(CheckHTTP) && firstNonEmpty(config.Path, "/") == readiness.Path) ||
			(check.Kind == string(CheckDockerHealth) && readiness.Kind == string(CheckDockerHealth))) {
		// A pass shows only its title, so the title carries the path.
		title, measured := "Readiness waits for the Dockerfile's HEALTHCHECK", readiness.Evidence
		if readiness.Kind == string(CheckHTTP) {
			title = "Readiness probes GET " + readiness.Path + ", which the source declares"
			measured = "GET " + readiness.Path + " · " + readiness.Evidence
		}
		findings = append(findings, finding("readiness_path_detected", PreflightPass, title, measured,
			"The release is verified the way the application says it is healthy.", "", "deploy", "checks"))
	}
	if readiness == nil {
		return findings
	}
	findings = append(findings, hostAllowlistFindings(readiness, configuration.Domains,
		runtimeCheckHost(firstNonEmpty(configuration.Runtime.BindAddress, "127.0.0.1")))...)
	if readiness.HTTPSRedirect != "" {
		https := false
		for _, domain := range configuration.Domains {
			https = https || domain.HTTPS
		}
		switch {
		case !https:
			findings = append(findings, finding("readiness_redirects_to_https", PreflightWarning,
				"The application redirects every plain-HTTP request to HTTPS, and this plan serves no HTTPS domain", readiness.HTTPSRedirect,
				"Readiness and visitors reach the application over plain HTTP, and each request is sent to an https address this plan never serves.",
				"Add a domain with HTTPS, or stop the application redirecting when TLS ends before it (Rails: config.assume_ssl = true; Django: SECURE_SSL_REDIRECT = False; Phoenix: remove force_ssl).",
				"deploy", "domains"))
		case readiness.HTTPSRedirectIgnoresProxy && strings.HasPrefix(readiness.HTTPSRedirect, "force_ssl"):
			findings = append(findings, finding("readiness_redirects_to_https", PreflightWarning,
				"The application redirects to HTTPS without trusting the proxy's X-Forwarded-Proto", readiness.HTTPSRedirect+" without rewrite_on",
				"TLS ends at the proxy, so Phoenix sees every request as plain HTTP and redirects it to HTTPS again: a loop for visitors.",
				"Set force_ssl: [rewrite_on: [:x_forwarded_proto]] on the endpoint.", "deploy", "domains"))
		case readiness.HTTPSRedirectIgnoresProxy:
			findings = append(findings, finding("readiness_redirects_to_https", PreflightWarning,
				"The application redirects to HTTPS without trusting the proxy's X-Forwarded-Proto", readiness.HTTPSRedirect+" without SECURE_PROXY_SSL_HEADER",
				"TLS ends at the proxy, so Django sees every request as plain HTTP and redirects it to HTTPS again: a loop for visitors and for readiness.",
				"Set SECURE_PROXY_SSL_HEADER = ('HTTP_X_FORWARDED_PROTO', 'https') in the settings.", "deploy", "domains"))
		}
	}
	if readiness.ModelDownload != "" {
		findings = append(findings, modelDownloadFinding(readiness, configuration))
	}
	return findings
}

// plannedReadinessCheck is the first required readiness check, the one
// hasReadinessCheck counts, with its decoded configuration.
func plannedReadinessCheck(checks []PlannedCheck) (PlannedCheck, CheckConfiguration, bool) {
	for _, check := range checks {
		if check.Phase != "readiness" || !check.Required {
			continue
		}
		config, err := decodeCheckConfiguration(check.Config)
		if err != nil || config.Disabled {
			continue
		}
		return check, config, true
	}
	return PlannedCheck{}, CheckConfiguration{}, false
}

// startCommandFindings refuse a recipe start command that would end the
// container, whether it detaches itself or runs a package script that does,
// and warn about a Dockerfile command that does the same.
func startCommandFindings(selected *DetectedCandidate, configuration PlanConfiguration) []PreflightFinding {
	var issue *startDetachIssue
	command, field := "", "build.startCommand"
	switch configuration.Build.Method {
	case BuildRecipe:
		command = configuration.Build.StartCommand
		issue = classifyStartCommand(command)
		if issue == nil && selected != nil && selected.StartDetaches != nil && selected.StartDetaches.Script != "" {
			for _, segment := range strings.Split(command, "&&") {
				if scriptSegmentName(segment) == selected.StartDetaches.Script {
					detach := selected.StartDetaches
					issue = &startDetachIssue{detach.Effect, detach.Reason, detach.Action}
					command = detach.Script + " script: " + detach.Command
					break
				}
			}
		}
	case BuildDockerfile:
		field = "runtime.command"
		if len(configuration.Runtime.Command) > 0 {
			command = dockerfileCommandLine(string(mustJSON(configuration.Runtime.Command)))
			issue = classifyStartCommand(command)
		} else if selected != nil && selected.BuildMethod == BuildDockerfile && selected.StartDetaches != nil {
			detach := selected.StartDetaches
			issue = &startDetachIssue{detach.Effect, detach.Reason, detach.Action}
			command = detach.Source + ": " + detach.Command
		}
	}
	if issue == nil {
		return nil
	}
	measured := boundedEvidence(command)
	if issue.effect == startDetachBackgrounds {
		return []PreflightFinding{finding("start_command_backgrounds", PreflightWarning,
			"The start command runs a second process in the background", measured, issue.reason, issue.action, "deploy", field)}
	}
	severity := PreflightBlocked
	if configuration.Build.Method == BuildDockerfile {
		// The Dockerfile is the operator's own and may pair its command with
		// an entrypoint detection did not read.
		severity = PreflightWarning
	}
	return []PreflightFinding{finding("start_command_daemonizes", severity,
		"The start command puts the application in the background and exits", measured,
		issue.reason+"; the restart policy starts it again and again, and readiness never passes.",
		issue.action+".", "deploy", field)}
}

// hostAllowlistFindings warn when the application's literal host allowlist
// refuses the name readiness and visitors use: the planned domains, or, when
// there are none, the loopback address the probe dials and sends as Host.
// "localhost" is not that address: an allowlist of only it answers the
// probe 400.
func hostAllowlistFindings(readiness *DetectedReadiness, domains []PlannedDomain, probeHost string) []PreflightFinding {
	if readiness.AllowedHostsSource == "" {
		return nil
	}
	var refused []string
	if len(domains) == 0 {
		if strings.Contains(probeHost, ":") {
			probeHost = "[" + probeHost + "]"
		}
		if !hostAllowed(probeHost, readiness.AllowedHosts) {
			refused = append(refused, probeHost)
		}
	}
	for _, domain := range domains {
		if !hostAllowed(domain.Hostname, readiness.AllowedHosts) {
			refused = append(refused, domain.Hostname)
		}
	}
	if len(refused) == 0 {
		return nil
	}
	allowed := "an empty list"
	if len(readiness.AllowedHosts) > 0 {
		encoded, _ := json.Marshal(readiness.AllowedHosts)
		allowed = string(encoded)
	}
	measured := fmt.Sprintf("%s is not in %s (%s)", strings.Join(refused, ", "), readiness.AllowedHostsSource, allowed)
	action := "Add the planned domain to the allowlist, or read it from a variable set to the domain."
	if len(domains) == 0 {
		action = "Add a domain to this plan and to the allowlist, or allow " + refused[0] + " for a deployment without one."
	}
	return []PreflightFinding{finding("readiness_host_allowlist", PreflightWarning,
		"The application's host allowlist refuses the name it is reached by", boundedEvidence(measured),
		"The application answers a request for a host outside its allowlist with 400 or 403, so readiness fails and visitors are refused.",
		action, "deploy", "domains")}
}

// hostAllowed matches a host the way Django's ALLOWED_HOSTS and Rails'
// config.hosts do: exactly, "*" for any, and a leading dot for a domain and
// its subdomains.
func hostAllowed(host string, allowlist []string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, pattern := range allowlist {
		pattern = strings.ToLower(pattern)
		switch {
		case pattern == "*" || pattern == host:
			return true
		case strings.HasPrefix(pattern, ".") && (host == pattern[1:] || strings.HasSuffix(host, pattern)):
			return true
		}
	}
	return false
}

func modelDownloadFinding(readiness *DetectedReadiness, configuration PlanConfiguration) PreflightFinding {
	cache := readiness.ModelCache
	for _, mount := range configuration.Runtime.Mounts {
		if cache != "" && (mount.Target == cache || strings.HasPrefix(cache, strings.TrimSuffix(mount.Target, "/")+"/")) && !mount.ReadOnly {
			return finding("python_model_download_at_start", PreflightPass,
				"Model downloads are kept across releases", readiness.ModelDownload+"; "+mount.Target+" is a volume",
				"Only the first release downloads the model.", "", "deploy", "runtime.mounts")
		}
	}
	wait := ""
	if check, config, ok := plannedReadinessCheck(configuration.Checks); ok && check.Kind == string(CheckHTTP) && config.Attempts > 0 {
		seconds := config.Attempts * max(config.IntervalSeconds, 1)
		wait = fmt.Sprintf(" Readiness waits up to %d min.", (seconds+59)/60)
	}
	action := "Keep the model cache on a volume so later releases reuse the download; the first start can take minutes." + wait
	if cache != "" {
		action = "Add a volume mounted at " + cache + " so later releases reuse the download (a writable volume makes releases stop-first); the first start can take minutes." + wait
	}
	return finding("python_model_download_at_start", PreflightWarning,
		"The application downloads a model when it starts", readiness.ModelDownload,
		"The server listens only once the download finishes, and the download is part of the container, so every release fetches it again.",
		action, "deploy", "runtime.mounts")
}
