package proxysvc

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/net/idna"
)

// The deep DNS view answers the questions the one-line check cannot: which
// record points where and for how long, whether a CAA record stops Let's
// Encrypt before certbot ever asks, and whether the public resolvers have
// caught up with a change yet. Go's resolver hides TTLs and CAA entirely, so
// these are asked directly, one wire query at a time.

// DefaultDNSResolvers are the public resolvers the propagation table compares
// the system resolver against when no list has been saved.
var DefaultDNSResolvers = []string{"1.1.1.1", "8.8.8.8", "9.9.9.9"}

// MaxDNSResolvers keeps the table readable and the lookup a few dozen packets.
const MaxDNSResolvers = 6

// typeCAA is RFC 8659's record type, which dnsmessage has no constant for.
const typeCAA dnsmessage.Type = 257

// DNSRecord is one answer as the resolver gave it.
type DNSRecord struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	TTL   uint32 `json:"ttl"`
	Value string `json:"value"`
	// CAA only.
	Critical bool   `json:"critical,omitempty"`
	Tag      string `json:"tag,omitempty"`
}

// DNSAnswer is a query's outcome: the response code and the answer section.
type DNSAnswer struct {
	RCode   string      `json:"rcode"`
	Records []DNSRecord `json:"records"`
	// OverTCP is true when the UDP answer was truncated and asked again.
	OverTCP bool `json:"overTcp,omitempty"`
}

// Query asks one server one question, over UDP with the TCP retry a
// truncated answer requires. server is an address, with or without a port.
func Query(ctx context.Context, server, name string, qtype dnsmessage.Type) (*DNSAnswer, error) {
	addr := resolverAddress(server)
	fqdn, err := dnsmessage.NewName(strings.TrimSuffix(name, ".") + ".")
	if err != nil {
		return nil, err
	}
	var idb [2]byte
	if _, err := rand.Read(idb[:]); err != nil {
		return nil, err
	}
	id := binary.BigEndian.Uint16(idb[:])
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: id, RecursionDesired: true})
	b.EnableCompression()
	if err := b.StartQuestions(); err != nil {
		return nil, err
	}
	if err := b.Question(dnsmessage.Question{Name: fqdn, Type: qtype, Class: dnsmessage.ClassINET}); err != nil {
		return nil, err
	}
	if err := b.StartAdditionals(); err != nil {
		return nil, err
	}
	var opt dnsmessage.ResourceHeader
	// 1232 is the DNS Flag Day size: large enough for most answers, small
	// enough not to fragment; anything larger comes back truncated and goes
	// over TCP.
	if err := opt.SetEDNS0(1232, dnsmessage.RCodeSuccess, false); err != nil {
		return nil, err
	}
	if err := b.OPTResource(opt, dnsmessage.OPTResource{}); err != nil {
		return nil, err
	}
	msg, err := b.Finish()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	resp, err := exchangeUDP(ctx, addr, msg, id)
	if err != nil {
		return nil, err
	}
	answer, truncated, err := parseAnswer(resp, id)
	if err != nil {
		return nil, err
	}
	if truncated {
		resp, err = exchangeTCP(ctx, addr, msg)
		if err != nil {
			return nil, fmt.Errorf("the answer was truncated and TCP failed: %w", err)
		}
		if answer, _, err = parseAnswer(resp, id); err != nil {
			return nil, err
		}
		answer.OverTCP = true
	}
	return answer, nil
}

func resolverAddress(server string) string {
	if _, _, err := net.SplitHostPort(server); err == nil {
		return server
	}
	return net.JoinHostPort(strings.Trim(server, "[]"), "53")
}

func exchangeUDP(ctx context.Context, addr string, msg []byte, id uint16) ([]byte, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if _, err := conn.Write(msg); err != nil {
		return nil, err
	}
	buf := make([]byte, 65535)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return nil, err
		}
		// A stray datagram with another ID is someone else's answer, or a
		// spoof; the real one may still be on its way.
		if n >= 2 && binary.BigEndian.Uint16(buf[:2]) == id {
			return buf[:n], nil
		}
	}
}

func exchangeTCP(ctx context.Context, addr string, msg []byte) ([]byte, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	framed := make([]byte, 2+len(msg))
	binary.BigEndian.PutUint16(framed, uint16(len(msg)))
	copy(framed[2:], msg)
	if _, err := conn.Write(framed); err != nil {
		return nil, err
	}
	var size [2]byte
	if _, err := io.ReadFull(conn, size[:]); err != nil {
		return nil, err
	}
	resp := make([]byte, binary.BigEndian.Uint16(size[:]))
	if _, err := io.ReadFull(conn, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func parseAnswer(resp []byte, id uint16) (*DNSAnswer, bool, error) {
	var p dnsmessage.Parser
	h, err := p.Start(resp)
	if err != nil {
		return nil, false, err
	}
	if h.ID != id || !h.Response {
		return nil, false, errors.New("the resolver answered a different question")
	}
	answer := &DNSAnswer{RCode: rcodeName(h.RCode), Records: []DNSRecord{}}
	if h.Truncated {
		return answer, true, nil
	}
	if err := p.SkipAllQuestions(); err != nil {
		return nil, false, err
	}
	for {
		rh, err := p.AnswerHeader()
		if errors.Is(err, dnsmessage.ErrSectionDone) {
			break
		}
		if err != nil {
			return nil, false, err
		}
		rec := DNSRecord{Name: strings.TrimSuffix(rh.Name.String(), "."), TTL: rh.TTL}
		switch rh.Type {
		case dnsmessage.TypeA:
			r, err := p.AResource()
			if err != nil {
				return nil, false, err
			}
			rec.Type, rec.Value = "A", net.IP(r.A[:]).String()
		case dnsmessage.TypeAAAA:
			r, err := p.AAAAResource()
			if err != nil {
				return nil, false, err
			}
			rec.Type, rec.Value = "AAAA", net.IP(r.AAAA[:]).String()
		case dnsmessage.TypeCNAME:
			r, err := p.CNAMEResource()
			if err != nil {
				return nil, false, err
			}
			rec.Type, rec.Value = "CNAME", strings.TrimSuffix(r.CNAME.String(), ".")
		case dnsmessage.TypeTXT:
			r, err := p.TXTResource()
			if err != nil {
				return nil, false, err
			}
			rec.Type, rec.Value = "TXT", strings.Join(r.TXT, "")
		case typeCAA:
			r, err := p.UnknownResource()
			if err != nil {
				return nil, false, err
			}
			flags, tag, value, ok := parseCAA(r.Data)
			if !ok {
				continue
			}
			// Bit 0 (the high bit, 128) is the issuer-critical flag.
			rec.Type, rec.Critical, rec.Tag, rec.Value = "CAA", flags&0x80 != 0, tag, value
		default:
			if err := p.SkipAnswer(); err != nil {
				return nil, false, err
			}
			continue
		}
		answer.Records = append(answer.Records, rec)
	}
	return answer, false, nil
}

// parseCAA reads RFC 8659 section 4.1's wire form: flags, a tag length, the
// tag, and the value as the rest.
func parseCAA(data []byte) (flags uint8, tag, value string, ok bool) {
	if len(data) < 2 {
		return 0, "", "", false
	}
	n := int(data[1])
	if n == 0 || len(data) < 2+n {
		return 0, "", "", false
	}
	return data[0], strings.ToLower(string(data[2 : 2+n])), string(data[2+n:]), true
}

func rcodeName(rc dnsmessage.RCode) string {
	switch rc {
	case dnsmessage.RCodeSuccess:
		return "NOERROR"
	case dnsmessage.RCodeNameError:
		return "NXDOMAIN"
	case dnsmessage.RCodeServerFailure:
		return "SERVFAIL"
	case dnsmessage.RCodeRefused:
		return "REFUSED"
	case dnsmessage.RCodeFormatError:
		return "FORMERR"
	case dnsmessage.RCodeNotImplemented:
		return "NOTIMP"
	}
	return fmt.Sprintf("RCODE%d", rc)
}

// DNSAddress is one A or AAAA record, placed.
type DNSAddress struct {
	Type    string `json:"type"`
	Address string `json:"address"`
	TTL     uint32 `json:"ttl"`
	// Owner is "this-server", "cloudflare", "other", or "unknown" when the
	// machine has no public address of its own to compare with, where
	// calling a record "other" would be a guess.
	Owner string `json:"owner"`
}

// CAAReport is RFC 8659's tree walk and what it means for issuance.
type CAAReport struct {
	// Checked is each name asked, from the domain up, ending where a CAA
	// set was found or at the top-level domain.
	Checked []string `json:"checked"`
	// FoundAt is the name whose CAA set governs, empty when none does.
	FoundAt string      `json:"foundAt,omitempty"`
	Records []DNSRecord `json:"records"`
	// Issuers are the CAs the issue records name; wildcard ones those
	// issuewild names, or issue's when there is no issuewild.
	Issuers         []string `json:"issuers"`
	WildcardIssuers []string `json:"wildcardIssuers"`
	LetsEncrypt     bool     `json:"letsEncrypt"`
	// LetsEncryptWildcard is whether a wildcard for this name may be issued.
	LetsEncryptWildcard bool   `json:"letsEncryptWildcard"`
	Verdict             string `json:"verdict"`
	Error               string `json:"error,omitempty"`
}

// ResolverView is one resolver's answer for the name's addresses.
type ResolverView struct {
	Resolver  string   `json:"resolver"`
	Label     string   `json:"label"`
	System    bool     `json:"system,omitempty"`
	RCode     string   `json:"rcode,omitempty"`
	Addresses []string `json:"addresses"`
	Error     string   `json:"error,omitempty"`
	// Agrees is whether this resolver gave the same addresses as the first
	// one that answered — the system resolver, when it did.
	Agrees bool `json:"agrees"`
}

// DNSReport is everything the TLS page's DNS panel shows.
type DNSReport struct {
	Domain    string    `json:"domain"`
	CheckedAt time.Time `json:"checkedAt"`
	// Source is the resolver the records were read from.
	Source             string         `json:"source"`
	RCode              string         `json:"rcode,omitempty"`
	Error              string         `json:"error,omitempty"`
	Addresses          []DNSAddress   `json:"addresses"`
	CNAMEChain         []DNSRecord    `json:"cnameChain"`
	HostAddresses      []string       `json:"hostAddresses"`
	HostAddressesKnown bool           `json:"hostAddressesKnown"`
	PointsHere         bool           `json:"pointsHere"`
	BehindProxy        bool           `json:"behindProxy"`
	Summary            string         `json:"summary"`
	CAA                CAAReport      `json:"caa"`
	ACMEChallenge      []DNSRecord    `json:"acmeChallenge"`
	ACMEError          string         `json:"acmeError,omitempty"`
	Propagation        []ResolverView `json:"propagation"`
	// Consistent is true when every resolver that answered agreed.
	Consistent bool `json:"consistent"`
	// Resolvers is the saved comparison list, so the page can edit it.
	Resolvers []string `json:"resolvers"`
}

// NormalizeDNSName turns what was typed into the ASCII name DNS carries, and
// refuses an address, which has no records of its own to inspect.
func NormalizeDNSName(raw string) (string, error) {
	name := strings.TrimSuffix(strings.TrimSpace(raw), ".")
	if name == "" {
		return "", errors.New("a domain name is required")
	}
	if net.ParseIP(strings.Trim(name, "[]")) != nil {
		return "", errors.New("an IP address has no DNS records to inspect; give a domain name")
	}
	ascii, err := idna.Lookup.ToASCII(name)
	if err != nil {
		return "", fmt.Errorf("%q is not a domain name: %v", raw, err)
	}
	if _, err := dnsmessage.NewName(ascii + "."); err != nil {
		return "", fmt.Errorf("%q is not a domain name: %v", raw, err)
	}
	return strings.ToLower(ascii), nil
}

// ParseDNSResolvers validates a saved or submitted resolver list: IP
// addresses only, so the setting cannot become a name lookup of its own.
func ParseDNSResolvers(list []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, raw := range list {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		ip := net.ParseIP(strings.Trim(raw, "[]"))
		if ip == nil {
			return nil, fmt.Errorf("%q is not an IP address", raw)
		}
		s := ip.String()
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	if len(out) > MaxDNSResolvers {
		return nil, fmt.Errorf("at most %d resolvers can be compared", MaxDNSResolvers)
	}
	return out, nil
}

// systemNameservers reads resolv.conf the way the system resolver does.
func systemNameservers(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "nameserver" {
			// A zone suffix (fe80::1%eth0) is part of the address to dial.
			if ip := net.ParseIP(strings.SplitN(fields[1], "%", 2)[0]); ip != nil {
				out = append(out, fields[1])
			}
		}
	}
	return out
}

var resolverLabels = map[string]string{
	"1.1.1.1":              "Cloudflare",
	"1.0.0.1":              "Cloudflare",
	"8.8.8.8":              "Google",
	"8.8.4.4":              "Google",
	"9.9.9.9":              "Quad9",
	"149.112.112.112":      "Quad9",
	"208.67.222.222":       "OpenDNS",
	"2606:4700:4700::1111": "Cloudflare",
	"2001:4860:4860::8888": "Google",
	"2620:fe::fe":          "Quad9",
}

// InspectDNS builds the deep report for a name, reading records from the
// system resolver (or the first listed one when the system has none that
// answers) and comparing addresses across all of them.
func InspectDNS(ctx context.Context, domain string, resolvers []string) *DNSReport {
	return inspectDNS(ctx, domain, systemNameservers("/etc/resolv.conf"), resolvers, hostAddresses())
}

func inspectDNS(ctx context.Context, domain string, system, resolvers, host []string) *DNSReport {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	report := &DNSReport{
		Domain:        domain,
		CheckedAt:     time.Now().UTC(),
		Addresses:     []DNSAddress{},
		CNAMEChain:    []DNSRecord{},
		HostAddresses: host,
		ACMEChallenge: []DNSRecord{},
		Resolvers:     resolvers,
	}
	report.HostAddressesKnown = len(host) > 0

	views := make([]ResolverView, 0, len(resolvers)+1)
	if len(system) > 0 {
		views = append(views, ResolverView{Resolver: system[0], Label: "System resolver", System: true, Addresses: []string{}})
	} else {
		views = append(views, ResolverView{Resolver: "", Label: "System resolver", System: true, Addresses: []string{},
			Error: "/etc/resolv.conf names no nameserver"})
	}
	for _, r := range resolvers {
		label := resolverLabels[r]
		if label == "" {
			label = r
		}
		views = append(views, ResolverView{Resolver: r, Label: label, Addresses: []string{}})
	}

	var wg sync.WaitGroup
	aAnswers := make([]*DNSAnswer, len(views))
	aaaaAnswers := make([]*DNSAnswer, len(views))
	for i := range views {
		if views[i].Error != "" {
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var a, aaaa *DNSAnswer
			var errA, errAAAA error
			var inner sync.WaitGroup
			inner.Add(2)
			go func() { defer inner.Done(); a, errA = Query(ctx, views[i].Resolver, domain, dnsmessage.TypeA) }()
			go func() {
				defer inner.Done()
				aaaa, errAAAA = Query(ctx, views[i].Resolver, domain, dnsmessage.TypeAAAA)
			}()
			inner.Wait()
			switch {
			case errA != nil:
				views[i].Error = errA.Error()
			case errAAAA != nil:
				views[i].Error = errAAAA.Error()
			default:
				aAnswers[i], aaaaAnswers[i] = a, aaaa
			}
		}(i)
	}
	wg.Wait()

	for i := range views {
		if aAnswers[i] == nil {
			continue
		}
		views[i].RCode = aAnswers[i].RCode
		views[i].Addresses = addressValues(aAnswers[i], aaaaAnswers[i])
	}
	report.Consistent = judgePropagation(views)
	report.Propagation = views

	// The records themselves come from the first resolver that answered, so
	// the table and the records never disagree about who was asked.
	source := -1
	for i := range views {
		if aAnswers[i] != nil {
			source = i
			break
		}
	}
	if source < 0 {
		report.Error = "No resolver answered."
		for _, v := range views {
			if v.Error != "" {
				report.Error = v.Label + ": " + v.Error
				break
			}
		}
		report.Summary = "The name could not be looked up from this server."
		report.CAA = CAAReport{Checked: []string{}, Records: []DNSRecord{}, Issuers: []string{}, WildcardIssuers: []string{},
			Verdict: "Not checked: no resolver answered."}
		return report
	}
	server := views[source].Resolver
	report.Source = views[source].Label + " (" + server + ")"
	report.RCode = aAnswers[source].RCode

	for _, ans := range []*DNSAnswer{aAnswers[source], aaaaAnswers[source]} {
		for _, rec := range ans.Records {
			if rec.Type != "A" && rec.Type != "AAAA" {
				continue
			}
			report.Addresses = append(report.Addresses, DNSAddress{
				Type: rec.Type, Address: rec.Value, TTL: rec.TTL,
				Owner: addressOwner(rec.Value, host),
			})
		}
	}
	report.CNAMEChain = cnameChain(domain, aAnswers[source].Records, aaaaAnswers[source].Records)

	resolved := make([]string, 0, len(report.Addresses))
	for _, a := range report.Addresses {
		resolved = append(resolved, a.Address)
	}
	check := &DomainCheck{Domain: domain, Addresses: resolved, HostAddresses: host,
		HostAddressesKnown: report.HostAddressesKnown}
	check.PointsHere, check.BehindProxy = compareAddresses(resolved, host)
	report.PointsHere, report.BehindProxy = check.PointsHere, check.BehindProxy
	if report.RCode == "NXDOMAIN" {
		report.Summary = "The name does not exist (NXDOMAIN). If you have just created the record, the negative answer is cached for up to the zone's minimum TTL."
	} else {
		report.Summary = describeDomainCheck(check)
	}

	var inner sync.WaitGroup
	inner.Add(2)
	go func() {
		defer inner.Done()
		report.CAA = walkCAA(ctx, server, domain)
	}()
	go func() {
		defer inner.Done()
		ans, err := Query(ctx, server, "_acme-challenge."+domain, dnsmessage.TypeTXT)
		if err != nil {
			report.ACMEError = err.Error()
			return
		}
		for _, rec := range ans.Records {
			if rec.Type == "TXT" || rec.Type == "CNAME" {
				report.ACMEChallenge = append(report.ACMEChallenge, rec)
			}
		}
	}()
	inner.Wait()
	return report
}

func addressValues(answers ...*DNSAnswer) []string {
	out := []string{}
	for _, ans := range answers {
		for _, rec := range ans.Records {
			if rec.Type == "A" || rec.Type == "AAAA" {
				out = append(out, rec.Value)
			}
		}
	}
	sort.Strings(out)
	return out
}

// judgePropagation marks each view against the first that answered and says
// whether all that answered agree. A resolver that failed neither agrees nor
// counts against the rest: it has said nothing.
func judgePropagation(views []ResolverView) bool {
	ref := -1
	for i := range views {
		if views[i].Error == "" {
			ref = i
			break
		}
	}
	if ref < 0 {
		return false
	}
	consistent := true
	want := strings.Join(views[ref].Addresses, ",")
	for i := range views {
		if views[i].Error != "" {
			continue
		}
		views[i].Agrees = strings.Join(views[i].Addresses, ",") == want &&
			views[i].RCode == views[ref].RCode
		if !views[i].Agrees {
			consistent = false
		}
	}
	return consistent
}

func addressOwner(addr string, host []string) string {
	for _, h := range host {
		if h == addr {
			return "this-server"
		}
	}
	if isKnownProxyAddress(net.ParseIP(addr)) {
		return "cloudflare"
	}
	if len(host) == 0 {
		return "unknown"
	}
	return "other"
}

// cnameChain follows the aliases from the name itself, in the order a
// resolver walks them, whatever order they were listed in.
func cnameChain(domain string, answers ...[]DNSRecord) []DNSRecord {
	next := map[string]DNSRecord{}
	for _, list := range answers {
		for _, rec := range list {
			if rec.Type == "CNAME" {
				next[strings.ToLower(rec.Name)] = rec
			}
		}
	}
	chain := []DNSRecord{}
	name := strings.ToLower(domain)
	for len(chain) < len(next) {
		rec, ok := next[name]
		if !ok {
			break
		}
		chain = append(chain, rec)
		name = strings.ToLower(rec.Value)
	}
	return chain
}

// caaNames is RFC 8659 section 3's tree: the name, then each parent, up to
// but not including the root.
func caaNames(domain string) []string {
	labels := strings.Split(domain, ".")
	out := make([]string, 0, len(labels))
	for i := range labels {
		out = append(out, strings.Join(labels[i:], "."))
	}
	return out
}

func walkCAA(ctx context.Context, server, domain string) CAAReport {
	report := CAAReport{Checked: []string{}, Records: []DNSRecord{}}
	for _, name := range caaNames(domain) {
		report.Checked = append(report.Checked, name)
		ans, err := Query(ctx, server, name, typeCAA)
		if err == nil && ans.RCode != "NOERROR" && ans.RCode != "NXDOMAIN" {
			err = errors.New(ans.RCode)
		}
		if err != nil {
			// A CA may not treat a failed lookup as permission, and Let's
			// Encrypt refuses to issue while it fails.
			report.Error = err.Error()
			report.Issuers, report.WildcardIssuers = []string{}, []string{}
			report.Verdict = fmt.Sprintf("The CAA lookup for %s failed (%v). Let's Encrypt refuses to issue until it succeeds.", name, err)
			return report
		}
		for _, rec := range ans.Records {
			if rec.Type == "CAA" {
				report.Records = append(report.Records, rec)
			}
		}
		if len(report.Records) > 0 {
			report.FoundAt = name
			break
		}
	}
	judgeCAA(&report)
	return report
}

const letsEncryptCAA = "letsencrypt.org"

// knownCAATags are the properties a CA can be expected to understand; a
// critical record with any other tag forbids issuance outright.
var knownCAATags = map[string]bool{
	"issue": true, "issuewild": true, "iodef": true, "issuemail": true, "issuevmc": true,
	"contactemail": true, "contactphone": true,
}

// judgeCAA applies RFC 8659 section 4 to the governing set.
func judgeCAA(r *CAAReport) {
	r.Issuers, r.WildcardIssuers = []string{}, []string{}
	if len(r.Records) == 0 {
		r.LetsEncrypt, r.LetsEncryptWildcard = true, true
		r.Verdict = "No CAA record on this name or any parent, so any certificate authority may issue — Let's Encrypt included."
		return
	}
	var issue, wild []DNSRecord
	for _, rec := range r.Records {
		switch {
		case rec.Critical && !knownCAATags[rec.Tag]:
			r.Verdict = fmt.Sprintf("The CAA record at %s carries a critical %q property that certificate authorities do not understand, which forbids every one of them from issuing.", r.FoundAt, rec.Tag)
			return
		case rec.Tag == "issue":
			issue = append(issue, rec)
		case rec.Tag == "issuewild":
			wild = append(wild, rec)
		}
	}
	if len(wild) == 0 {
		wild = issue
	}
	var leParams string
	r.LetsEncrypt, r.Issuers, leParams = caaAllows(issue)
	r.LetsEncryptWildcard, r.WildcardIssuers, _ = caaAllows(wild)

	switch {
	case len(issue) == 0:
		r.Verdict = fmt.Sprintf("The CAA set at %s has no issue record, so any certificate authority may issue — Let's Encrypt included.", r.FoundAt)
	case r.LetsEncrypt:
		r.Verdict = fmt.Sprintf("Let's Encrypt may issue (CAA at %s).", r.FoundAt)
		if leParams != "" {
			r.Verdict += " Only under the record's conditions: " + leParams + "."
		}
	case len(r.Issuers) == 0:
		r.Verdict = fmt.Sprintf("No certificate authority may issue: the CAA set at %s allows none.", r.FoundAt)
	default:
		r.Verdict = fmt.Sprintf("Only %s may issue (CAA at %s). Let's Encrypt will refuse.", strings.Join(r.Issuers, ", "), r.FoundAt)
	}
	if r.LetsEncrypt != r.LetsEncryptWildcard {
		if r.LetsEncryptWildcard {
			r.Verdict += " A wildcard certificate is allowed from Let's Encrypt."
		} else {
			r.Verdict += " A wildcard certificate is not."
		}
	}
}

// caaAllows reads a set of issue or issuewild records: whether Let's Encrypt
// is named, every issuer named, and the parameters on Let's Encrypt's record.
// An empty set allows everyone; a record with no issuer (";") names nobody.
func caaAllows(records []DNSRecord) (letsEncrypt bool, issuers []string, params string) {
	issuers = []string{}
	if len(records) == 0 {
		return true, issuers, ""
	}
	for _, rec := range records {
		domain, rest, _ := strings.Cut(rec.Value, ";")
		domain = strings.ToLower(strings.TrimSpace(domain))
		if domain == "" {
			continue
		}
		issuers = append(issuers, domain)
		if domain == letsEncryptCAA {
			// A record without conditions outweighs one with them.
			if !letsEncrypt || params != "" {
				params = strings.TrimSpace(rest)
			}
			letsEncrypt = true
		}
	}
	return letsEncrypt, issuers, params
}
