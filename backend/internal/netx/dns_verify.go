package netx

import (
	"context"
	"fmt"
	"strings"
)

// One name resolving after a change used to be the whole verification. It
// proves the default route answers, not that a split-DNS domain still reaches
// its servers, that resolved runs with what was written, or that required DNS
// over TLS is what carried the answer. The plan lists every check a change is
// held to before it is made, and names the scopes no check reaches; the result
// carries what each check found. A required check that fails rolls the change
// back, as an unanswered name always has.

// DNSVerificationCheck is one check of a resolver change.
type DNSVerificationCheck struct {
	// Kind is resolution (a name answers through resolved), readback (resolved
	// runs with the settings written), transport (the encryption the answer
	// came over) or dnssec (whether resolved authenticated it).
	Kind string `json:"kind"`
	// Scope is where the check looks: the default route, a routing domain on a
	// link, the global settings, the upstream connection.
	Scope    string `json:"scope"`
	Name     string `json:"name,omitempty"`
	Required bool   `json:"required"`
	// State is planned before the change, then passed, failed, warning,
	// unknown (the evidence could not be read) or skipped.
	State  string  `json:"state"`
	Answer string  `json:"answer,omitempty"`
	Type   string  `json:"type,omitempty"`
	Millis float64 `json:"millis,omitempty"`
	Detail string  `json:"detail,omitempty"`
}

// DNSVerification is a change's plan or result.
type DNSVerification struct {
	Checks []DNSVerificationCheck `json:"checks"`
	// Unverified are scopes the change affects that no check reaches.
	Unverified []string `json:"unverified"`
}

// firstResolved is the first name that answered and how long it took, which
// the older Verified/Via/Millis fields of a result report.
func (v DNSVerification) firstResolved() (string, float64) {
	for _, c := range v.Checks {
		if c.Kind == "resolution" && c.State == "passed" {
			return c.Name, c.Millis
		}
	}
	return "", 0
}

// dnsVerifyExecutor runs the native adapter the transport and DNSSEC checks
// use. A variable so tests answer for it instead of the host's resolver.
var dnsVerifyExecutor TrafficExecutor = func(ctx context.Context, name string, args ...string) (string, error) {
	return dnsNativeExecutor(ctx, name, args...)
}

const defaultRouteScope = "default route"

// verificationScopes is the policy resolved will route by after the change:
// the global scope with what the request sets, the links as they are.
func verificationScopes(req DNSSettings, rv ResolvedView) []DNSPolicyScope {
	global := DNSPolicyScope{Index: 0, Interface: "global upstreams", Servers: rv.Global.Servers, Domains: rv.Global.Domains}
	if len(req.Servers) > 0 || containsString(req.Clear, "servers") {
		global.Servers = req.Servers
	}
	if len(req.Domains) > 0 || containsString(req.Clear, "domains") {
		global.Domains = req.Domains
	}
	global.ActiveDNS = len(global.Servers) > 0
	scopes := []DNSPolicyScope{global}
	for _, l := range rv.Links {
		scopes = append(scopes, DNSPolicyScope{Index: l.Index, Interface: l.Name, Servers: l.Servers, Domains: l.Domains, DefaultRoute: l.DefaultRoute, ActiveDNS: containsString(l.Scopes, "DNS")})
	}
	return scopes
}

// verificationScope names the scope resolved sends a name to.
func verificationScope(name string, scopes []DNSPolicyScope) string {
	chosen, match := dnsBestPolicy(name, scopes)
	if len(chosen) == 0 {
		return "no scope: no server would be asked"
	}
	labels := []string{}
	for _, c := range chosen {
		labels = append(labels, c.Interface)
	}
	if !strings.HasPrefix(match, "longest suffix") {
		return defaultRouteScope + " via " + strings.Join(labels, ", ")
	}
	domain := ""
	lower := strings.ToLower(name)
	for _, c := range chosen {
		for _, d := range c.Domains {
			bare := strings.TrimSuffix(strings.TrimPrefix(strings.ToLower(d), "~"), ".")
			if (bare == "" || bare == "." || lower == bare || strings.HasSuffix(lower, "."+bare)) && len(d) > len(domain) {
				domain = d
			}
		}
	}
	if domain == "~." || domain == "" {
		return defaultRouteScope + " via " + strings.Join(labels, ", ")
	}
	return domain + " on " + strings.Join(labels, ", ")
}

func underDomain(name, suffix string) bool {
	name, suffix = strings.ToLower(name), strings.ToLower(strings.TrimSuffix(suffix, "."))
	return name == suffix || strings.HasSuffix(name, "."+suffix)
}

// planDNSVerification lists the checks a change will be held to.
func planDNSVerification(req DNSSettings, rv ResolvedView) DNSVerification {
	v := DNSVerification{Checks: []DNSVerificationCheck{}, Unverified: []string{}}
	v.Checks = append(v.Checks, DNSVerificationCheck{Kind: "readback", Scope: "global settings", Required: true, State: "planned",
		Detail: "resolvectl status must report the servers, fallback, domains, DNSSEC and DNS over TLS written here; a later drop-in that overrides them fails the change"})
	scopes := verificationScopes(req, rv)
	defaultCovered := false
	if len(req.VerificationNames) == 0 {
		v.Checks = append(v.Checks, DNSVerificationCheck{Kind: "resolution", Scope: verificationScope(verifyNames[0], scopes), Name: strings.Join(verifyNames, " or "), Required: true, State: "planned",
			Detail: "either public name answering in A or AAAA passes"})
		defaultCovered = true
	}
	for _, name := range req.VerificationNames {
		scope := verificationScope(name, scopes)
		defaultCovered = defaultCovered || strings.HasPrefix(scope, defaultRouteScope)
		v.Checks = append(v.Checks, DNSVerificationCheck{Kind: "resolution", Scope: scope, Name: name, Required: true, State: "planned", Detail: "must answer in A or AAAA"})
	}
	for _, d := range req.Domains {
		if !strings.HasPrefix(d, "~") || d == "~." {
			continue
		}
		covered := false
		for _, name := range req.VerificationNames {
			covered = covered || underDomain(name, strings.TrimPrefix(d, "~"))
		}
		if !covered {
			v.Unverified = append(v.Unverified, d+" (routing domain set here): no verification name falls under it")
		}
	}
	if !defaultCovered && (len(req.Servers) > 0 || containsString(req.Clear, "servers")) {
		v.Unverified = append(v.Unverified, "default route through the global upstreams: no verification name is routed through it")
	}
	if len(req.Fallback) > 0 {
		v.Unverified = append(v.Unverified, "fallback servers: resolved uses them only when no other server is known, so no check reaches them")
	}
	if (req.DNSSEC != "" || req.DNSOverTLS != "") && len(rv.Links) > 0 {
		v.Unverified = append(v.Unverified, "links without their own DNSSEC or DNS over TLS setting inherit the global one; only the names above check it")
	}
	if req.Cache != "" {
		v.Unverified = append(v.Unverified, "cache mode: resolvectl status does not report it, so it is written but not read back")
	}
	transportName := "the first name that resolves"
	if len(req.VerificationNames) > 0 {
		transportName = req.VerificationNames[0]
	}
	switch req.DNSOverTLS {
	case "yes":
		v.Checks = append(v.Checks, DNSVerificationCheck{Kind: "transport", Scope: "upstream connection", Name: transportName, Required: true, State: "planned",
			Detail: "systemd-resolved must report the answer as carried over TLS under its strict identity policy"})
	case "opportunistic":
		v.Checks = append(v.Checks, DNSVerificationCheck{Kind: "transport", Scope: "upstream connection", Name: transportName, State: "planned",
			Detail: "reports whether the answer came over TLS or opportunistic mode fell back to classic DNS"})
	}
	if req.DNSSEC == "yes" || req.DNSSEC == "allow-downgrade" {
		v.Checks = append(v.Checks, DNSVerificationCheck{Kind: "dnssec", Scope: "validation", Name: transportName, State: "planned",
			Detail: "reports whether systemd-resolved authenticated the answer; an unsigned name is legitimately unauthenticated"})
	}
	return v
}

// planDNSReset is what a reset is checked with: the host's own resolvers
// answering a public name. A failure is reported, never rolled back.
func planDNSReset() DNSVerification {
	return DNSVerification{Checks: []DNSVerificationCheck{{Kind: "resolution", Scope: defaultRouteScope, Name: strings.Join(verifyNames, " or "), State: "planned", Detail: "either public name answering in A or AAAA passes"}}, Unverified: []string{}}
}

// runDNSVerification runs a plan after the restart and returns its result,
// with the sentence of the first required check that failed.
func (s *Service) runDNSVerification(ctx context.Context, req DNSSettings, plan DNSVerification) (DNSVerification, string) {
	out := DNSVerification{Checks: make([]DNSVerificationCheck, len(plan.Checks)), Unverified: append([]string{}, plan.Unverified...)}
	copy(out.Checks, plan.Checks)
	failure := ""
	fail := func(c *DNSVerificationCheck, reason string) {
		c.State = "failed"
		if c.Required && failure == "" {
			failure = reason
		}
	}
	resolvedName, resolvedType := "", ""
	var native *DNSInvestigation
	nativeRead := false
	for i := range out.Checks {
		c := &out.Checks[i]
		switch c.Kind {
		case "readback":
			state, detail := readbackDNS(ctx, req)
			c.State, c.Detail = state, detail
			if state == "failed" {
				fail(c, "systemd-resolved is not running with the settings written: "+detail)
			}
		case "resolution":
			names := []string{c.Name}
			if c.Name == strings.Join(verifyNames, " or ") {
				names = verifyNames
			}
			var last error
			for _, name := range names {
				answer, rtype, took, err := verifyName(ctx, name)
				if err == nil {
					c.State, c.Answer, c.Type, c.Millis, c.Name = "passed", answer, rtype, took, name
					if resolvedName == "" {
						resolvedName, resolvedType = name, rtype
					}
					break
				}
				last = err
				if ctx.Err() != nil {
					break
				}
			}
			if c.State != "passed" {
				c.Detail = fmt.Sprintf("%s could not be resolved through systemd-resolved (%v)", names[0], last)
				fail(c, "The new upstreams did not answer: "+c.Detail)
			}
		case "transport", "dnssec":
			// The first answer is the one inspected: transport and DNSSEC
			// describe how that answer arrived, not every scope.
			name, rtype := resolvedName, resolvedType
			if name == "" {
				c.State, c.Detail = "skipped", "no name resolved, so there was no answer to inspect"
				continue
			}
			c.Name = name
			if !nativeRead {
				nativeRead = true
				report, err := s.InvestigateDNS(ctx, DNSInvestigationRequest{Name: name, Type: rtype}, dnsVerifyExecutor)
				if err == nil {
					native = report
				}
			}
			if native == nil || native.Error != "" {
				c.State, c.Detail = "unknown", "the native resolver evidence is unavailable"
				if native != nil {
					c.Detail += ": " + native.Error
				}
				continue
			}
			if c.Kind == "transport" {
				transportResult(c, req.DNSOverTLS, native, fail)
			} else {
				dnssecResult(c, native, fail)
			}
		}
	}
	return out, failure
}

func transportResult(c *DNSVerificationCheck, mode string, r *DNSInvestigation, fail func(*DNSVerificationCheck, string)) {
	switch r.Transport.State {
	case "encrypted":
		c.State = "passed"
		if r.Trust.State == "native_policy_validated" {
			c.Detail = "systemd-resolved reports the answer came over TLS and enforced the configured server name and system trust (strict mode). The certificate was not inspected separately."
		} else {
			c.Detail = "systemd-resolved reports the answer came over TLS; opportunistic mode does not authenticate the certificate."
		}
	case "unencrypted":
		if mode == "yes" {
			c.Detail = "systemd-resolved reports a fresh answer without confidential transport, although DNS over TLS is required."
			fail(c, "Required DNS over TLS did not carry the answer: "+c.Detail)
			return
		}
		c.State, c.Detail = "warning", "The answer came over classic DNS: opportunistic mode fell back because the upstream did not complete TLS."
	default:
		c.State, c.Detail = "unknown", r.Transport.Summary
	}
}

func dnssecResult(c *DNSVerificationCheck, r *DNSInvestigation, fail func(*DNSVerificationCheck, string)) {
	switch r.DNSSEC.State {
	case "validated":
		c.State, c.Detail = "passed", "systemd-resolved authenticated the answer under its own trust anchors."
	case "not_authenticated":
		c.State, c.Detail = "warning", "Not authenticated: the name may be unsigned, or validation was downgraded or disabled for its scope."
	case "validation_failed":
		c.Detail = r.DNSSEC.Summary
		fail(c, "DNSSEC validation failed: "+c.Detail)
	default:
		c.State, c.Detail = "unknown", r.DNSSEC.Summary
	}
}

// readbackDNS compares what resolved reports for its global scope with what
// the request wrote. Lists compare as resolved prints them, normalised the way
// the drop-in writes servers.
func readbackDNS(ctx context.Context, req DNSSettings) (string, string) {
	out, err := run(ctx, "resolvectl", "status", "--no-pager")
	if err != nil || !strings.Contains("\n"+out, "\nGlobal") {
		return "unknown", "resolvectl status could not be read, so the running settings were not compared"
	}
	g, _, _ := parseResolvedStatus(out)
	diffs := []string{}
	compare := func(what string, want, got []string, normalize bool) {
		if normalize {
			got = normalizeServers(got)
		}
		if strings.Join(want, " ") != strings.Join(got, " ") {
			shown := strings.Join(got, " ")
			if shown == "" {
				shown = "none"
			}
			wanted := strings.Join(want, " ")
			if wanted == "" {
				wanted = "none"
			}
			diffs = append(diffs, fmt.Sprintf("%s are %s where %s was written", what, shown, wanted))
		}
	}
	if len(req.Servers) > 0 || containsString(req.Clear, "servers") {
		compare("global servers", req.Servers, g.Servers, true)
	}
	if len(req.Fallback) > 0 || containsString(req.Clear, "fallback") {
		compare("fallback servers", req.Fallback, g.Fallback, true)
	}
	if len(req.Domains) > 0 || containsString(req.Clear, "domains") {
		compare("global domains", req.Domains, g.Domains, false)
	}
	if req.DNSSEC != "" && g.DNSSEC != req.DNSSEC {
		diffs = append(diffs, fmt.Sprintf("DNSSEC is %s where %s was written", orDefault(g.DNSSEC, "unreported"), req.DNSSEC))
	}
	if req.DNSOverTLS != "" && g.DNSOverTLS != req.DNSOverTLS {
		diffs = append(diffs, fmt.Sprintf("DNS over TLS is %s where %s was written", orDefault(g.DNSOverTLS, "unreported"), req.DNSOverTLS))
	}
	if len(diffs) > 0 {
		return "failed", strings.Join(diffs, "; ") + ". A later drop-in or the host's resolved.conf overrides them."
	}
	return "passed", "resolvectl status reports the settings written"
}

func normalizeServers(in []string) []string {
	out := make([]string, 0, len(in))
	for _, raw := range in {
		if sv, err := parseDNSServer(raw); err == nil {
			out = append(out, sv.resolvedEntry())
		} else {
			out = append(out, raw)
		}
	}
	return out
}
