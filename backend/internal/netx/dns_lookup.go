package netx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// Effective lookups use the native resolver policy. Direct comparisons bypass
// that policy and require explicit destinations and disclosure acknowledgement.

// lookupTimeout is how long each resolver has. Long enough for a resolver on
// another continent, short enough that one dead server does not hold the page.
// A variable so tests do not wait it out.
var lookupTimeout = 3 * time.Second

// Docker overlays its own resolv.conf on the host's mounted /etc. Read the
// effective chain in the same host namespace as the native resolver adapter.
var dnsLookupFileExecutor TrafficExecutor = runDNSNative

func readLookupResolvConf(ctx context.Context) (ResolvConf, error) {
	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	rc := ResolvConf{Path: resolvConfPath, Mode: "static", Nameservers: []string{}, Search: []string{}}
	out, err := dnsLookupFileExecutor(ctx, "cat", resolvConfPath)
	if err != nil || len(out) > 64<<10 {
		return rc, errors.New("the host configured resolver chain is unreadable; no effective query was sent")
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(strings.SplitN(strings.SplitN(line, "#", 2)[0], ";", 2)[0])
		if len(fields) > 1 && (fields[0] == "search" || fields[0] == "domain") {
			rc.Search = fields[1:]
			continue
		}
		if len(fields) == 0 || fields[0] != "nameserver" {
			continue
		}
		if len(fields) != 2 {
			return rc, errors.New("the host configured resolver chain is malformed; no effective query was sent")
		}
		if _, err := parseDNSServer(fields[1]); err != nil {
			return rc, errors.New("the host configured resolver chain contains an unsupported destination; no effective query was sent")
		}
		rc.Nameservers = append(rc.Nameservers, fields[1])
	}
	return rc, nil
}

// maxLookupResolvers bounds the fan-out. A host with every veth carrying a
// resolver would otherwise start dozens of queries for one click.
const maxLookupResolvers = 16

// dnsDial opens the connection a query to one resolver travels over. The
// address is always the resolver's own, port 53. A variable so tests answer
// from a local socket instead of the network.
var dnsDial = func(ctx context.Context, network, address string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, network, address)
}

// LookupAnswer is one resolver's reply.
type LookupAnswer struct {
	Server string `json:"server"`
	// Label says whose resolver it is: the stub, a link, a preset.
	Label     string   `json:"label"`
	Answers   []string `json:"answers"`
	LatencyMS float64  `json:"latencyMs"`
	Error     string   `json:"error,omitempty"`
	// Transport is how a direct comparison reached the destination: udp, tcp
	// (a truncated answer retried) or tls (verified for TLSName). Effective
	// lookups leave it empty: resolved chooses its own upstream transport.
	Transport  string `json:"transport,omitempty"`
	TLSName    string `json:"tlsName,omitempty"`
	TLSVersion string `json:"tlsVersion,omitempty"`
	// AuthenticatedData and Signatures answer a comparison that asked with the
	// DNSSEC OK bit: the destination's AD claim and the RRSIGs it returned.
	// Neither is validation by the dashboard.
	AuthenticatedData *bool `json:"authenticatedData,omitempty"`
	Signatures        *int  `json:"signatures,omitempty"`
}

// LookupResult records the native-policy test or an explicitly selected comparison.
type LookupResult struct {
	Name    string              `json:"name"`
	Type    string              `json:"type"`
	Answers []LookupAnswer      `json:"results"`
	Mode    string              `json:"mode"`
	Route   string              `json:"route"`
	Note    string              `json:"note"`
	Targets []LookupDestination `json:"comparisonTargets"`
	Omitted []LookupDestination `json:"omittedTargets"`
}

// lookupTypes are the supported DNS record types.
var lookupTypes = map[string]bool{"A": true, "AAAA": true, "CNAME": true, "MX": true, "TXT": true, "NS": true, "PTR": true, "SRV": true}

// lookupTarget is a resolver to ask.
type lookupTarget struct{ server, label, tlsName string }

// LookupDestination identifies a configured or preset destination; callers can
// select only these addresses, so comparison cannot become an arbitrary probe.
type LookupDestination struct {
	Server string `json:"server"`
	Label  string `json:"label"`
	// TLSName is the identity a DNS-over-TLS comparison verifies: the preset's
	// or the #name a configured server carries.
	TLSName string `json:"tlsName,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

type LookupOptions struct {
	Mode                  string   `json:"mode"`
	Destinations          []string `json:"destinations"`
	AcknowledgeDisclosure bool     `json:"acknowledgeDisclosure"`
	// Transport is classic (UDP, TCP on truncation) or tls for a comparison;
	// DNSSEC sets the DO bit. Both are comparison-only.
	Transport string `json:"transport,omitempty"`
	DNSSEC    bool   `json:"dnssec,omitempty"`
	// AcknowledgeForwarding lets an effective lookup send a private name to a
	// configured non-public resolver whose own forwarding is unknown.
	AcknowledgeForwarding bool `json:"acknowledgeForwarding,omitempty"`
}

// DNSPolicyRefusal is a lookup the policy-aware test will not send: Code says
// which (dns_private_name_public_upstream or dns_private_name_unknown_forwarding).
type DNSPolicyRefusal struct {
	Code   string
	Reason string
}

func (e *DNSPolicyRefusal) Error() string { return e.Reason }

// Lookup retains the original entry point but an includePublic flag alone no
// longer authorizes direct disclosure to every configured/preset resolver.
func (s *Service) Lookup(ctx context.Context, name, rtype string, includePublic ...bool) (*LookupResult, error) {
	if len(includePublic) > 0 && includePublic[0] {
		return nil, errors.New("direct comparison requires named destinations and disclosure acknowledgement")
	}
	return s.LookupWithOptions(ctx, name, rtype, LookupOptions{})
}

func (s *Service) LookupWithOptions(ctx context.Context, name, rtype string, opts LookupOptions) (*LookupResult, error) {
	rtype = strings.ToUpper(strings.TrimSpace(rtype))
	if rtype == "" {
		rtype = "A"
	}
	if !lookupTypes[rtype] {
		return nil, errors.New("the record type is one of A, AAAA, CNAME, MX, TXT, NS, PTR or SRV")
	}
	name, err := cleanLookupName(name, rtype)
	if err != nil {
		return nil, err
	}
	if opts.Mode == "" {
		opts.Mode = "effective"
	}
	if opts.Mode != "effective" && opts.Mode != "compare" {
		return nil, errors.New("lookup mode is effective or compare")
	}
	if opts.Mode == "effective" && (len(opts.Destinations) > 0 || opts.AcknowledgeDisclosure || opts.Transport != "" || opts.DNSSEC) {
		return nil, errors.New("named destinations, disclosure acknowledgement, transport and DNSSEC options require comparison mode")
	}
	if opts.Mode == "compare" && opts.AcknowledgeForwarding {
		return nil, errors.New("comparison takes disclosure acknowledgement, not forwarding acknowledgement")
	}
	if opts.Transport == "" {
		opts.Transport = "classic"
	}
	if opts.Transport != "classic" && opts.Transport != "tls" {
		return nil, errors.New("comparison transport is classic or tls")
	}
	rv := s.readResolved(ctx, false)
	rc, chainErr := readLookupResolvConf(ctx)
	all, omitted := s.lookupDestinationInventory(rv, rc)
	if chainErr != nil {
		omitted = append(omitted, LookupDestination{Label: "host resolv.conf configured chain", Reason: chainErr.Error()})
	}
	res := &LookupResult{Name: name, Type: rtype, Mode: opts.Mode, Answers: []LookupAnswer{}, Targets: all, Omitted: omitted}
	var targets []lookupTarget
	if opts.Mode == "compare" {
		if !opts.AcknowledgeDisclosure || len(opts.Destinations) == 0 {
			return nil, errors.New("comparison sends this name, including private names, directly to the selected destinations; select destinations and acknowledge disclosure")
		}
		if len(opts.Destinations) > maxLookupResolvers {
			return nil, fmt.Errorf("select at most %d comparison destinations", maxLookupResolvers)
		}
		by := map[string]LookupDestination{}
		for _, d := range all {
			by[d.Server] = d
		}
		seen := map[string]bool{}
		for _, server := range opts.Destinations {
			d, ok := by[server]
			if !ok {
				return nil, fmt.Errorf("%s is not an available configured or preset resolver", server)
			}
			if seen[server] {
				continue
			}
			seen[server] = true
			if opts.Transport == "tls" && d.TLSName == "" {
				// A selected destination that cannot be asked this way is said, not dropped.
				res.Omitted = append(res.Omitted, LookupDestination{Server: d.Server, Label: d.Label, Reason: "no DNS-over-TLS identity is configured for this destination, so its certificate cannot be verified; it was not asked"})
				continue
			}
			tlsName := ""
			if opts.Transport == "tls" {
				tlsName = d.TLSName
			}
			targets = append(targets, lookupTarget{d.Server, d.Label, tlsName})
		}
		if len(targets) == 0 {
			return nil, errors.New("none of the selected destinations has a DNS-over-TLS identity; choose one that names its certificate, such as a preset")
		}
		res.Route = "explicit comparison"
		res.Note = "Direct classic DNS to the selected destinations bypasses split-DNS routing, host records, resolver encryption and DNSSEC validation. Private names are disclosed to each selected destination."
		if opts.Transport == "tls" {
			res.Route = "explicit comparison over DNS over TLS"
			res.Note = "DNS over TLS to each selected destination's configured identity, with its certificate verified against this host's trust store. This bypasses split-DNS routing and host records, and private names are disclosed to each selected destination."
		}
		if opts.DNSSEC {
			res.Note += " The DNSSEC OK bit was set: the AD flag is each destination's own claim to have validated, and returned signatures were not verified by the dashboard."
		}
	} else {
		if chainErr != nil {
			return nil, chainErr
		}
		delegates := false
		for _, entry := range rc.Nameservers {
			if entry == resolvedStub || entry == resolvedStubAlt {
				delegates = true
			}
		}
		if delegates {
			if err := nativePrivateNameGuard(name, rtype, rc, rv); err != nil {
				return nil, err
			}
			// A classic stub request can follow an alias into another native
			// scope before the original caller can check it. Use the same explicit
			// alias walker as retained evidence, including fresh ownership checks.
			start := time.Now()
			native, err := s.InvestigateDNS(ctx, DNSInvestigationRequest{Name: name, Type: rtype}, nil)
			if err != nil {
				return nil, err
			}
			if native.Error != "" {
				return nil, errors.New(native.Error)
			}
			res.Answers = []LookupAnswer{{Server: resolvedStub, Label: "systemd-resolved effective policy", Answers: native.Answers, LatencyMS: millis(time.Since(start))}}
			labels := []string{}
			for _, policy := range native.Policy {
				labels = append(labels, policy.Interface)
			}
			res.Route = native.PolicyMatch + ": " + strings.Join(labels, ", ") + "; aliases checked before each native question"
			res.Note = "Absolute fresh DNS records from the identified host resolver, with bounded explicit CNAME checks before every target. Hosts/NSS, search expansion, DNAME and application DNS are excluded; retained policy investigations expose detailed per-question evidence."
			return res, nil
		}
		for _, entry := range rc.Nameservers {
			sv, err := parseDNSServer(entry)
			if err == nil {
				targets = append(targets, lookupTarget{server: sv.lookupServer(), label: "resolv.conf effective policy"})
			}
			if len(targets) == maxLookupResolvers {
				break
			}
		}
		res.Route = "resolv.conf order"
		res.Note = "Absolute DNS wire query through the actual resolv.conf servers in order, stopping at the first response. A separately running resolved service does not establish this chain's ownership. Hosts/NSS, search expansion, private split policy, recursive alias disclosure, encryption and DNSSEC validation are not measured."
		acknowledged, err := foreignPrivateNameGuard(name, rtype, rc, rv, targets, opts.AcknowledgeForwarding)
		if err != nil {
			return nil, err
		}
		if acknowledged != "" {
			res.Note += " " + acknowledged
		}
	}
	if len(targets) == 0 {
		return nil, errors.New("no usable resolver destination is configured")
	}
	ask := func(t lookupTarget) LookupAnswer {
		start := time.Now()
		if opts.Mode != "compare" {
			answers, err := lookupVia(ctx, t.server, name, rtype)
			a := LookupAnswer{Server: t.server, Label: t.label, Answers: answers, LatencyMS: millis(time.Since(start))}
			if a.Answers == nil {
				a.Answers = []string{}
			}
			if err != nil {
				a.Error = lookupError(err)
			}
			return a
		}
		queryCtx, cancel := context.WithTimeout(ctx, lookupTimeout)
		defer cancel()
		answers, meta, err := lookupDNSWire(queryCtx, t.server, name, rtype, wireOptions{tlsName: t.tlsName, dnssec: opts.DNSSEC})
		a := LookupAnswer{Server: t.server, Label: t.label, Answers: answers, LatencyMS: millis(time.Since(start)), Transport: meta.transport, TLSName: t.tlsName, TLSVersion: meta.tlsVersion}
		if a.Answers == nil {
			a.Answers = []string{}
		}
		if opts.DNSSEC && meta.transport != "" && err == nil {
			ad, signatures := meta.authenticated, meta.signatures
			a.AuthenticatedData, a.Signatures = &ad, &signatures
		}
		if err != nil {
			a.Error = lookupError(err)
			if state, reason := dnsTLSFailure(err, t.tlsName); t.tlsName != "" && state == "untrusted" {
				a.Error = reason
			}
		}
		return a
	}
	if opts.Mode == "effective" {
		for _, t := range targets {
			a := ask(t)
			res.Answers = append(res.Answers, a)
			if a.Error == "" || a.Error == "no such record" || ctx.Err() != nil {
				break
			}
		}
	} else {
		res.Answers = make([]LookupAnswer, len(targets))
		var wg sync.WaitGroup
		for i, t := range targets {
			wg.Add(1)
			go func() { defer wg.Done(); res.Answers[i] = ask(t) }()
		}
		wg.Wait()
	}
	return res, nil
}

// Inventory is not permission to query it. Invalid/scoped entries are shown as
// omissions, and the fan-out cap applies to selected destinations, not discovery.
func (s *Service) lookupDestinationInventory(rv ResolvedView, configured ...ResolvConf) ([]LookupDestination, []LookupDestination) {
	out, omitted := []LookupDestination{}, []LookupDestination{}
	var rc ResolvConf
	if len(configured) > 0 {
		rc = configured[0]
	} else {
		var err error
		rc, err = readLookupResolvConf(context.Background())
		if err != nil {
			omitted = append(omitted, LookupDestination{Label: "host resolv.conf configured chain", Reason: err.Error()})
		}
	}
	seen := map[string]bool{}
	addEntries := func(entries []string, label, iface string) {
		for _, e := range entries {
			sv, err := parseDNSServer(e)
			if err != nil {
				omitted = append(omitted, LookupDestination{Server: e, Label: label, Reason: "unsupported or invalid resolver address"})
				continue
			}
			if sv.addr.IsLinkLocalUnicast() && sv.iface == "" {
				sv.iface = iface
			}
			if sv.addr.IsLinkLocalUnicast() && sv.iface == "" {
				omitted = append(omitted, LookupDestination{Server: e, Label: label, Reason: "link-local resolver has no interface scope"})
				continue
			}
			server := sv.lookupServer()
			if !seen[server] {
				seen[server] = true
				out = append(out, LookupDestination{Server: server, Label: label, TLSName: sv.tlsName})
				continue
			}
			// A configured address a preset also names keeps the identity the
			// preset publishes, so it can be compared over TLS too.
			for i := range out {
				if out[i].Server == server && out[i].TLSName == "" {
					out[i].TLSName = sv.tlsName
				}
			}
		}
	}
	if rv.Active {
		addEntries([]string{resolvedStub}, "systemd-resolved stub", "")
		addEntries(rv.Global.Servers, "global upstream", "")
		addEntries(rv.Global.Fallback, "global fallback", "")
		for _, l := range rv.Links {
			addEntries(l.Servers, l.Name, l.Name)
		}
		if rv.Error != "" {
			omitted = append(omitted, LookupDestination{Label: "resolved scopes", Reason: rv.Error})
		}
	} else {
		addEntries(rc.Nameservers, "resolv.conf", "")
	}
	if rv.Active {
		addEntries(rc.Nameservers, "resolv.conf configured chain", "")
	}
	for _, p := range DNSPresets() {
		for _, sv := range p.Servers {
			if a, err := netip.ParseAddr(sv); err == nil && a.Is4() {
				addEntries([]string{sv + "#" + p.TLSName}, p.Name, "")
				break
			}
		}
	}
	return out, omitted
}

func resolvedLookupRoute(rv ResolvedView, name, rtype string) string {
	if rv.Error != "" {
		return "native resolved routing; scope evidence unavailable: " + rv.Error
	}
	if rtype == "PTR" {
		a, _ := netip.ParseAddr(name)
		name = reverseDNSName(a)
	}
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	best := -1
	labels := []string{}
	consider := func(domains []string, label string) {
		for _, domain := range domains {
			domain = strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(domain, "~"), "."))
			if domain != "" && name != domain && !strings.HasSuffix(name, "."+domain) {
				continue
			}
			score := 0
			if domain != "" {
				score = len(strings.Split(domain, "."))
			}
			if score > best {
				best = score
				labels = []string{}
			}
			if score == best {
				labels = append(labels, label+" ("+domain+")")
			}
		}
	}
	consider(rv.Global.Domains, "global")
	for _, l := range rv.Links {
		consider(l.Domains, l.Name)
	}
	if best >= 0 {
		return "longest matching domain: " + strings.Join(labels, ", ") + "; resolved selects the upstream"
	}
	return "native resolved default-route policy; resolved selects the upstream"
}

// A declared private suffix remains a boundary when its link has lost DNS
// scope. Asking the stub then can invoke resolved's default-scope fallback.
func resolvedLookupPolicyAvailable(rv ResolvedView, name, rtype string) error {
	if rv.Error != "" {
		return errors.New("native resolved policy is unreadable; no query or fallback was used")
	}
	if rtype == "PTR" {
		a, _ := netip.ParseAddr(name)
		name = reverseDNSName(a)
	}
	all := []DNSPolicyScope{{Index: 0, Domains: rv.Global.Domains, Servers: rv.Global.Servers, ActiveDNS: len(rv.Global.Servers) > 0}}
	for _, link := range rv.Links {
		all = append(all, DNSPolicyScope{Index: link.Index, Domains: link.Domains, Servers: link.Servers, DefaultRoute: link.DefaultRoute, ActiveDNS: containsString(link.Scopes, "DNS")})
	}
	chosen, _ := dnsBestPolicy(name, all)
	if len(chosen) == 0 {
		return errors.New("no readable native DNS policy scope applies; no query or fallback was used")
	}
	for _, scope := range chosen {
		if !scope.ActiveDNS || len(scope.Servers) == 0 {
			return errors.New("a declared best-match DNS scope is unavailable; no query or default-scope fallback was used")
		}
	}
	return nil
}

// cleanLookupName validates what will be asked. Everything but PTR takes a
// name; PTR takes the address whose name is wanted.
func cleanLookupName(name, rtype string) (string, error) {
	name = strings.TrimSpace(name)
	if rtype == "PTR" {
		a, err := netip.ParseAddr(name)
		if err != nil || a.Zone() != "" {
			return "", fmt.Errorf("a PTR lookup takes an IP address")
		}
		return a.Unmap().String(), nil
	}
	if _, err := netip.ParseAddr(name); err == nil {
		return "", fmt.Errorf("give a name rather than an address; a PTR lookup finds the name for an address")
	}
	// Underscores belong to service names: _sip._tcp.example.com.
	if err := validDNSName(name, rtype == "SRV" || rtype == "TXT"); err != nil {
		return "", err
	}
	return strings.TrimSuffix(name, "."), nil
}

// lookupVia asks one resolver. The name is made absolute with a trailing dot
// so the search domains in /etc/resolv.conf are not appended to it: a
// resolver race that answered for example.com.openstacklocal would be racing
// a different question.
func lookupVia(ctx context.Context, server, name, rtype string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	return lookupDNS(ctx, server, name, rtype)
}

// lookupError shortens Go's resolver errors to what a table cell can hold. A
// name that does not exist is an answer, not a fault, and reads as one.
func lookupError(err error) string {
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return "no answer in " + lookupTimeout.String()
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		switch {
		case dnsErr.IsNotFound:
			return "no such record"
		case dnsErr.IsTimeout:
			return "no answer in " + lookupTimeout.String()
		case dnsErr.IsTemporary && dnsErr.Err != "":
			return dnsErr.Err
		}
		if dnsErr.Err != "" {
			return dnsErr.Err
		}
	}
	return err.Error()
}
