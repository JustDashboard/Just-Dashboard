package proxysvc

import (
	"context"
	"net"
	"sort"
	"strings"
	"time"
)

// Does this domain point at this server?
//
// It is the first question of every reverse-proxy setup and the cause of most
// of the failures: certbot cannot prove control of a name that resolves
// somewhere else, and the error it gives says "challenge failed" rather than
// "your DNS is not updated yet". Answering it here, next to the button that
// issues the certificate, turns a twenty-minute detour into a line of text.

type DomainCheck struct {
	Domain    string   `json:"domain"`
	Addresses []string `json:"addresses"`
	// HostAddresses are this machine's own routable addresses, so the UI can
	// show both sides of the comparison rather than only the verdict.
	HostAddresses []string `json:"hostAddresses"`
	// HostAddressesKnown is false when this machine has no globally routable
	// address on any interface, which is every VPS behind provider NAT — AWS,
	// GCP, Azure and Oracle all hand the instance a private address and map a
	// public one in front of it. There the comparison cannot be made at all,
	// and reporting "does not point here" for a correctly configured domain is
	// worse than saying so: a check that could not run is not a failure any
	// more than it is a pass.
	HostAddressesKnown bool `json:"hostAddressesKnown"`
	// PointsHere is true when at least one resolved address belongs to this
	// machine.
	PointsHere bool `json:"pointsHere"`
	// BehindProxy marks a domain resolving to a Cloudflare-style address:
	// not this host, and not wrong either. Reporting it as a mismatch is the
	// commonest false alarm a check like this produces.
	BehindProxy bool   `json:"behindProxy"`
	Summary     string `json:"summary"`
	Error       string `json:"error,omitempty"`
}

// CheckDomainDNS resolves a name and compares it against the host's addresses.
func CheckDomainDNS(ctx context.Context, domain string) *DomainCheck {
	check := &DomainCheck{Domain: domain, Addresses: []string{}, HostAddresses: []string{}}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	ips, err := net.DefaultResolver.LookupIPAddr(ctx, domain)
	if err != nil {
		check.Error = err.Error()
		check.Summary = "The name does not resolve. If you have just created the record, DNS caches take a few minutes to a few hours."
		return check
	}
	for _, ip := range ips {
		check.Addresses = append(check.Addresses, ip.IP.String())
	}
	sort.Strings(check.Addresses)
	check.HostAddresses = hostAddresses()
	check.HostAddressesKnown = len(check.HostAddresses) > 0
	check.PointsHere, check.BehindProxy = compareAddresses(check.Addresses, check.HostAddresses)
	check.Summary = describeDomainCheck(check)
	return check
}

// compareAddresses is the whole judgement, kept separate so it can be tested
// without a resolver or a network interface.
func compareAddresses(resolved, host []string) (pointsHere, behindProxy bool) {
	own := map[string]bool{}
	for _, a := range host {
		own[a] = true
	}
	for _, a := range resolved {
		if own[a] {
			pointsHere = true
		}
	}
	if pointsHere {
		return true, false
	}
	for _, a := range resolved {
		if isKnownProxyAddress(net.ParseIP(a)) {
			behindProxy = true
		}
	}
	return false, behindProxy
}

// cloudflareRanges are the two Cloudflare publishes for its proxy. Not an
// exhaustive list of every CDN — the point is to catch the one arrangement
// that is overwhelmingly common and would otherwise read as a misconfiguration
// every time somebody looked at the page.
var cloudflareRanges = []*net.IPNet{
	mustParseCIDR("104.16.0.0/13"),
	mustParseCIDR("172.64.0.0/13"),
	mustParseCIDR("162.158.0.0/15"),
	mustParseCIDR("173.245.48.0/20"),
	mustParseCIDR("188.114.96.0/20"),
	mustParseCIDR("190.93.240.0/20"),
	mustParseCIDR("197.234.240.0/22"),
	mustParseCIDR("198.41.128.0/17"),
	mustParseCIDR("103.21.244.0/22"),
	mustParseCIDR("103.22.200.0/22"),
	mustParseCIDR("103.31.4.0/22"),
	mustParseCIDR("108.162.192.0/18"),
	mustParseCIDR("131.0.72.0/22"),
	mustParseCIDR("141.101.64.0/18"),
	mustParseCIDR("2400:cb00::/32"),
	mustParseCIDR("2606:4700::/32"),
}

func mustParseCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic("proxysvc: bad constant CIDR " + s)
	}
	return n
}

func isKnownProxyAddress(ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, n := range cloudflareRanges {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func describeDomainCheck(c *DomainCheck) string {
	switch {
	case c.PointsHere:
		return "Resolves to this server. A certificate for it can be issued over HTTP."
	case c.BehindProxy:
		return "Resolves to Cloudflare rather than to this server directly. That is normal behind a CDN, and it means an HTTP certificate challenge will not reach this host — use a DNS challenge, or turn the proxy off while issuing."
	case len(c.Addresses) == 0:
		return "The name resolves to nothing."
	case !c.HostAddressesKnown:
		return "Resolves to " + strings.Join(c.Addresses, ", ") + ". This machine has no public address of its own — the provider maps one in front of it, which is normal on AWS, Google Cloud, Azure and Oracle — so whether that is this server cannot be told from here. Check it against the address your provider shows for this instance."
	default:
		return "Resolves to " + strings.Join(c.Addresses, ", ") + ", which is not an address on this machine. Certificate issuance over HTTP will fail until the record points here."
	}
}

// PublicAddresses lists this machine's globally routable addresses, for
// callers outside this package that need to name the server to the internet —
// a generated hostname has to resolve to one of these or it is decoration.
func PublicAddresses() []string { return hostAddresses() }

var sharedAddressSpace = mustParseCIDR("100.64.0.0/10")

// IsPublicAddress excludes CGNAT explicitly: Go's IsPrivate covers RFC1918
// and IPv6 ULA, but treats Tailscale's shared address space as global unicast.
func IsPublicAddress(ip net.IP) bool {
	return ip != nil && ip.IsGlobalUnicast() && !ip.IsPrivate() &&
		!ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !sharedAddressSpace.Contains(ip)
}

// hostAddresses lists this machine's globally routable addresses. Private and
// loopback ones are left out deliberately: a public DNS record can never point
// at them, so including them could only produce a false match.
func hostAddresses() []string {
	out := []string{}
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipn.IP
			if !IsPublicAddress(ip) {
				continue
			}
			out = append(out, ip.String())
		}
	}
	sort.Strings(out)
	return out
}
