package proxysvc

import (
	"net"
	"net/url"
	"regexp"
	"strings"
)

// Where an issuance or a renewal failed, and whose it is to fix.
//
// certbot ends a failed run with the certificate authority's words — "Timeout
// during connect (likely firewall problem)", "NXDOMAIN looking up A", "Invalid
// response … 404" — and every one of them is the failure of a different
// stage of validation, owned by somebody else: the DNS provider, a firewall in
// front of port 80 (this host's or the provider's), the web server answering
// the name, the DNS plugin's credentials, the CA's own limits. Read as one
// line of text, they all looked like "certbot failed". This reads each
// domain's problem into the stage it failed at and the owner of that stage,
// and links the page where that owner's evidence is.

// Issuance stages.
const (
	StageDNS        = "dns"
	StageCAA        = "caa"
	StageConnect    = "connect"
	StageChallenge  = "challenge"
	StageDNS01      = "dns-01"
	StageDNSPlugin  = "dns-plugin"
	StageRateLimit  = "rate-limit"
	StageAccount    = "account"
	StageLocalPort  = "local-port"
	StageInstaller  = "installer"
	StageUnknownRun = "unknown"
)

// DiagnosisLink is a page that holds the evidence for a stage.
type DiagnosisLink struct {
	Label string `json:"label"`
	Href  string `json:"href"`
}

// IssuanceDiagnosis is one problem a run reported.
type IssuanceDiagnosis struct {
	Stage      string `json:"stage"`
	StageTitle string `json:"stageTitle"`
	Owner      string `json:"owner"`
	Domain     string `json:"domain,omitempty"`
	// Address is the address the CA reached the name at, where it says.
	Address string `json:"address,omitempty"`
	// Here says the address is one of this host's own, where it can be said.
	Here *bool `json:"here,omitempty"`
	// Detail is the CA's or certbot's own words.
	Detail string          `json:"detail"`
	Action string          `json:"action"`
	Links  []DiagnosisLink `json:"links"`
}

var (
	failureLineRe = regexp.MustCompile(`(?i)error|fail|unable|could not|cannot|problem|too many|refused|denied|invalid|not valid|deactivated|lacks`)
	caProblemRe   = regexp.MustCompile(`^\s*(Domain|Type|Detail|Identifier):\s*(.*)$`)
	fetchedAtRe   = regexp.MustCompile(`^\s*([0-9a-fA-F:.]+): (?:Fetching|Invalid response from|Error getting validation data)`)
	fetchedURLRe  = regexp.MustCompile(`(?:Fetching|Invalid response from) (https?://\S+?):? `)
)

// DiagnoseIssuance reads a certbot run's output into the problems it
// reported, each at its stage and owner. local says whether an address is
// one of this host's, and whether that can be said at all; nil leaves Here
// unset.
func DiagnoseIssuance(output []string, local func(net.IP) (here, known bool)) []IssuanceDiagnosis {
	var out []IssuanceDiagnosis
	var current *IssuanceDiagnosis
	var kind string
	flush := func() {
		if current != nil && current.Detail != "" {
			classify(current, kind, local)
			out = append(out, *current)
		}
		current, kind = nil, ""
	}
	for _, line := range output {
		m := caProblemRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		value := strings.TrimSpace(m[2])
		switch m[1] {
		case "Domain", "Identifier":
			flush()
			current = &IssuanceDiagnosis{Domain: value}
		case "Type":
			if current == nil {
				current = &IssuanceDiagnosis{}
			}
			kind = value
		case "Detail":
			if current == nil {
				current = &IssuanceDiagnosis{}
			}
			current.Detail = value
		}
	}
	flush()
	if len(out) > 0 {
		return out
	}
	// No per-domain report: the run failed before or beside validation.
	text := strings.Join(output, "\n")
	line := lastMeaningfulLine(text)
	if line == "" {
		return nil
	}
	d := IssuanceDiagnosis{Detail: line}
	classify(&d, "", local)
	if d.Stage == StageUnknownRun {
		// The cause may be on any line that reads as a failure: certbot's
		// last is often its footer, and "Account registered." is progress.
		for _, l := range output {
			if !failureLineRe.MatchString(l) {
				continue
			}
			probe := IssuanceDiagnosis{Detail: strings.TrimSpace(l)}
			classify(&probe, "", local)
			if probe.Stage != StageUnknownRun {
				return []IssuanceDiagnosis{probe}
			}
		}
	}
	return []IssuanceDiagnosis{d}
}

// classify sets a problem's stage, owner, action and links from the CA's
// problem type and words.
func classify(d *IssuanceDiagnosis, kind string, local func(net.IP) (bool, bool)) {
	detail := d.Detail
	lower := strings.ToLower(detail)
	if m := fetchedAtRe.FindStringSubmatch(detail); m != nil {
		if ip := net.ParseIP(m[1]); ip != nil {
			d.Address = ip.String()
			if local != nil {
				if here, known := local(ip); known {
					d.Here = &here
				}
			}
		}
	}
	name := d.Domain
	if name == "" {
		if m := fetchedURLRe.FindStringSubmatch(detail); m != nil {
			if u, err := url.Parse(m[1]); err == nil {
				name = u.Hostname()
			}
		}
	}
	dnsTool := DiagnosisLink{"Look the name up", "/network/tools?tool=dns&target=" + url.QueryEscape(name)}
	tlsDNS := DiagnosisLink{"The name's DNS and CAA", "/proxy/tls?domain=" + url.QueryEscape(name)}
	switch {
	case strings.Contains(lower, "too many certificates") || strings.Contains(lower, "too many failed authorizations") ||
		strings.Contains(lower, "ratelimited") || strings.Contains(lower, "rate limit"):
		set(d, StageRateLimit, "The certificate authority's rate limits", "The certificate authority",
			"Wait for the window the message names, or test against the staging authority until the run passes.",
			DiagnosisLink{"Rate limits for these names", "/proxy/certificates"})
	case kind == "caa" || strings.Contains(lower, "caa record"):
		set(d, StageCAA, "CAA check", "The domain's DNS zone (its CAA records)",
			"Add a CAA record naming this authority, or remove the one that names another.", tlsDNS)
	case strings.Contains(lower, "hosted zone") || strings.Contains(lower, "credentials") || strings.Contains(lower, "api token") ||
		strings.Contains(lower, "authentication") || strings.Contains(lower, "zone_id"):
		set(d, StageDNSPlugin, "The DNS plugin's access to the zone", "The DNS provider credentials",
			"The plugin could not edit the zone: check its credentials and that they cover this domain.",
			DiagnosisLink{"DNS provider credentials", "/proxy/certificates"})
	case strings.Contains(lower, "txt record") || strings.Contains(lower, "_acme-challenge"):
		set(d, StageDNS01, "DNS-01 challenge record", "The DNS provider and the plugin's propagation wait",
			"Check that the plugin's credentials can edit the zone and that the record has propagated before validation; a longer propagation wait often fixes it.",
			DiagnosisLink{"Look the TXT record up", "/network/tools?tool=dns&target=" + url.QueryEscape("_acme-challenge."+strings.TrimPrefix(name, "*.")) + "&record=TXT"})
	case kind == "dns" || strings.Contains(lower, "dns problem") || strings.Contains(lower, "nxdomain") || strings.Contains(lower, "servfail"):
		owner, action := "The DNS provider or registrar", "Create an A or AAAA record for the name pointing at this server, then try again."
		if strings.Contains(lower, "servfail") || strings.Contains(lower, "timeout") || strings.Contains(lower, "refused") {
			owner, action = "The domain's authoritative nameservers", "The nameservers did not answer cleanly; check the delegation and that every nameserver serves the zone."
		}
		set(d, StageDNS, "Name resolution", owner, action, dnsTool,
			DiagnosisLink{"Check the delegation", "/network/tools?tool=dnsauth&target=" + url.QueryEscape(name)})
	case strings.Contains(lower, "could not bind tcp port") || strings.Contains(lower, "address already in use"):
		set(d, StageLocalPort, "Certbot's own challenge server", "The process holding port 80 on this host",
			"Use the nginx or webroot method, which answer through the web server already on port 80, or stop what holds the port.",
			DiagnosisLink{"What listens on port 80", "/proxy/ports?q=:80"})
	case kind == "connection" || strings.Contains(lower, "timeout during connect") || strings.Contains(lower, "connection refused") ||
		strings.Contains(lower, "connection reset") || strings.Contains(lower, "no route to host"):
		ports := DiagnosisLink{"What listens on port 80", "/proxy/ports?q=:80"}
		firewall := DiagnosisLink{"The host firewall", "/network/firewall"}
		outside := DiagnosisLink{"Check from outside", "/network/external"}
		switch {
		case d.Here != nil && !*d.Here:
			set(d, StageConnect, "Reaching port 80", "Whoever holds "+d.Address+": the name's record points there, or a provider maps it to this host",
				"If "+d.Address+" is not this server's public address, point the name's record here; if it is, the provider's firewall or NAT is what failed.", dnsTool, outside)
		case strings.Contains(lower, "refused"):
			set(d, StageConnect, "Reaching port 80", "Whatever answers port 80 here",
				"Nothing took the connection on port 80: start the web server on it, or check what holds it.", ports, firewall)
		default:
			set(d, StageConnect, "Reaching port 80", "A firewall in front of port 80: this host's or the provider's",
				"The authority's connection went unanswered: open port 80 to the internet in the host firewall and any provider firewall or security group.", firewall, outside, ports)
		}
	case kind == "unauthorized" || strings.Contains(lower, "invalid response") || strings.Contains(lower, "acme-challenge"):
		set(d, StageChallenge, "Serving the challenge file", "The web server answering the name on port 80",
			"The authority reached a web server that did not serve the challenge file: the name may land on another site, a redirect may leave the path, or the webroot may be another folder.",
			DiagnosisLink{"Which site answers the name", "/proxy/sites"},
			DiagnosisLink{"Ask the challenge path yourself", "/network/tools?tool=http&target=" + url.QueryEscape("http://"+name+"/.well-known/acme-challenge/test")})
	case strings.Contains(lower, "account") && failureLineRe.MatchString(lower) || strings.Contains(lower, "externalaccountrequired") || strings.Contains(lower, "invalidcontact"):
		set(d, StageAccount, "The ACME account", "The ACME account and its authority",
			"The authority refused the account: check the contact address, the External Account Binding, or register again.",
			DiagnosisLink{"The account certbot uses", "/proxy/certificates"})
	case strings.Contains(lower, "nginx plugin") || strings.Contains(lower, "nginx -c") || strings.Contains(lower, "server block"):
		set(d, StageInstaller, "Installing into nginx", "This host's nginx configuration",
			"certbot's nginx plugin could not read or change the configuration; test it, or issue with the webroot method.",
			DiagnosisLink{"Test the configuration", "/proxy/config"})
	default:
		set(d, StageUnknownRun, "Not recognised", "certbot", "Read certbot's full output above for the cause.")
	}
}

func set(d *IssuanceDiagnosis, stage, title, owner, action string, links ...DiagnosisLink) {
	d.Stage, d.StageTitle, d.Owner, d.Action = stage, title, owner, action
	d.Links = append([]DiagnosisLink{}, links...)
}

// localAddress says whether an address is one of this host's interfaces'
// (the dashboard shares the host's network namespace). An address on none of
// them is known to be elsewhere only when the host has a public address of
// its own in that family: otherwise a provider may map it onto this host.
func localAddress(ip net.IP) (here, known bool) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false, false
	}
	public := false
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		if n.IP.Equal(ip) {
			return true, true
		}
		if (n.IP.To4() == nil) == (ip.To4() == nil) && IsPublicAddress(n.IP) {
			public = true
		}
	}
	return false, public
}

// DiagnoseIssuanceHere is DiagnoseIssuance against this host's interfaces.
func DiagnoseIssuanceHere(output []string) []IssuanceDiagnosis {
	return DiagnoseIssuance(output, localAddress)
}
