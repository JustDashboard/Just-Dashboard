package netx

import (
	"net/netip"
	"strings"
)

// The policy-aware lookup must not be the thing that hands a private name to
// a public resolver. The native path already keeps a name under a routing
// domain on its own link; two holes remained. A name the RFCs reserve for
// private use (home.arpa, internal, local, a private address's reverse name)
// that no link claims goes to resolved's default route, which is often a
// public resolver. And a host whose /etc/resolv.conf lists servers directly has
// no split policy at all, so its search domains and private names go wherever
// the first nameserver is. Those questions are refused when the server they
// would reach is public, and need an explicit acknowledgement when it is a
// private resolver whose own forwarding this page cannot see.

// dnsPrivateSuffixes are special-use and private-use names (RFC 6761, 6762,
// 7686, 8375, 9476, and the internal TLD reserved for private use) plus the
// suffixes private networks commonly use without registering them.
var dnsPrivateSuffixes = []string{"local", "localhost", "localdomain", "home.arpa", "internal", "intranet", "lan", "home", "corp", "private", "test", "invalid", "onion", "alt"}

var cgnatPrefix = netip.MustParsePrefix("100.64.0.0/10")

// privateDNSAddress is an address whose reverse name belongs to a private
// network: RFC 1918, unique-local, link-local, loopback and shared CGNAT space.
func privateDNSAddress(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsUnspecified() || cgnatPrefix.Contains(a)
}

// publicDNSServer is a resolver address reached across the internet.
func publicDNSServer(server string) bool {
	sv, err := parseDNSServer(server)
	if err != nil {
		return false
	}
	a := sv.addr.Unmap()
	return a.IsGlobalUnicast() && !privateDNSAddress(a)
}

// dnsPrivateNameReason says why a question belongs to a private namespace, or
// is empty when nothing marks it private. declared are suffixes this host
// itself treats as private: its search domains and link routing domains.
func dnsPrivateNameReason(name, rtype string, declared []string) string {
	if rtype == "PTR" {
		if a, err := netip.ParseAddr(name); err == nil && privateDNSAddress(a) {
			return "the reverse name of the private address " + a.String()
		}
		return ""
	}
	lower := strings.ToLower(strings.TrimSuffix(name, "."))
	if !strings.Contains(lower, ".") {
		return "a single-label name, which only a search domain or a local resolver can answer"
	}
	for _, d := range declared {
		bare := strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(d, "~"), "."))
		if bare != "" && bare != "." && underDomain(lower, bare) {
			return "under " + bare + ", a domain this host treats as private"
		}
	}
	for _, suffix := range dnsPrivateSuffixes {
		if underDomain(lower, suffix) {
			return "under ." + suffix + ", a name reserved for or commonly used on private networks"
		}
	}
	return ""
}

// declaredPrivateDomains are the search domains of the configured chain and the
// routing domains resolved holds for its links, without the catch-all "~.".
func declaredPrivateDomains(rc ResolvConf, rv ResolvedView) []string {
	out := append([]string{}, rc.Search...)
	if rv.Active {
		for _, l := range rv.Links {
			out = append(out, l.Domains...)
		}
		for _, d := range rv.Global.Domains {
			if d != "~." {
				out = append(out, d)
			}
		}
	}
	return out
}

// nativePrivateNameGuard refuses a private name resolved would send to a
// public default-route server: a name a link claims goes to that link, which
// is the native policy's job, so only the default route is judged here.
func nativePrivateNameGuard(name, rtype string, rc ResolvConf, rv ResolvedView) error {
	if !rv.Active || rv.Error != "" {
		return nil
	}
	reason := dnsPrivateNameReason(name, rtype, rc.Search)
	if reason == "" {
		return nil
	}
	question := name
	if rtype == "PTR" {
		a, _ := netip.ParseAddr(name)
		question = reverseDNSName(a)
	}
	scopes := []DNSPolicyScope{{Index: 0, Interface: "global", Domains: rv.Global.Domains, Servers: rv.Global.Servers, ActiveDNS: len(rv.Global.Servers) > 0}}
	for _, l := range rv.Links {
		scopes = append(scopes, DNSPolicyScope{Index: l.Index, Interface: l.Name, Domains: l.Domains, Servers: l.Servers, DefaultRoute: l.DefaultRoute, ActiveDNS: containsString(l.Scopes, "DNS")})
	}
	chosen, match := dnsBestPolicy(question, scopes)
	if strings.HasPrefix(match, "longest suffix") && !strings.HasSuffix(match, ": 0 labels") {
		return nil
	}
	for _, scope := range chosen {
		for _, server := range scope.Servers {
			if publicDNSServer(server) {
				return &DNSPolicyRefusal{Code: "dns_private_name_public_upstream", Reason: "This name is " + reason + ", and no link claims it, so systemd-resolved would send it to the public resolver " + server + " on its default route. The policy-aware test does not disclose it; give the domain a routing domain on its link, or compare named resolvers with disclosure acknowledged."}
			}
		}
	}
	return nil
}

// foreignPrivateNameGuard judges a private name on a chain resolved does not
// own: refused when the first server it would reach is public, and needing
// acknowledgement when every server is private but its forwarding is unseen.
func foreignPrivateNameGuard(name, rtype string, rc ResolvConf, rv ResolvedView, targets []lookupTarget, acknowledged bool) (string, error) {
	reason := dnsPrivateNameReason(name, rtype, declaredPrivateDomains(rc, rv))
	if reason == "" {
		return "", nil
	}
	servers := []string{}
	for _, t := range targets {
		if publicDNSServer(t.server) {
			return "", &DNSPolicyRefusal{Code: "dns_private_name_public_upstream", Reason: "This name is " + reason + ", and the host's resolv.conf lists the public resolver " + t.server + ", which would receive it. The policy-aware test does not disclose it; compare named resolvers with disclosure acknowledged to ask anyway."}
		}
		servers = append(servers, t.server)
	}
	if !acknowledged {
		return "", &DNSPolicyRefusal{Code: "dns_private_name_unknown_forwarding", Reason: "This name is " + reason + ". The host's resolv.conf sends it to " + strings.Join(servers, ", ") + ", whose own forwarding this page cannot see: it may pass the name to a public resolver. Acknowledge that to send it."}
	}
	return "This private name went to " + strings.Join(servers, ", ") + " with acknowledgement; where they forward it is not measured.", nil
}
