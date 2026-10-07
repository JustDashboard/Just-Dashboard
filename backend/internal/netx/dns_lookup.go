package netx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"
)

// A name is asked of every resolver this host knows about at once, and each
// one's answer and time are reported side by side. The question this answers
// is "why is this name slow, or wrong, here" — and the fast way to find out is
// to see the stub, the upstream it forwards to and a public resolver answer
// the same question in the same second.

// lookupTimeout is how long each resolver has. Long enough for a resolver on
// another continent, short enough that one dead server does not hold the page.
// A variable so tests do not wait it out.
var lookupTimeout = 3 * time.Second

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
}

// LookupResult is a name asked of every resolver.
type LookupResult struct {
	Name    string         `json:"name"`
	Type    string         `json:"type"`
	Answers []LookupAnswer `json:"results"`
}

// lookupTypes are the record types Go's resolver can ask for.
var lookupTypes = map[string]bool{"A": true, "AAAA": true, "CNAME": true, "MX": true, "TXT": true, "NS": true, "PTR": true, "SRV": true}

// lookupTarget is a resolver to ask.
type lookupTarget struct{ server, label string }

// Lookup asks every resolver in view for name. The resolvers are the ones this
// host is configured with — never an address the caller supplies — which is
// what makes it safe behind the read capability: it cannot be used to make the
// server send queries to a chosen machine, only to ask its own resolvers and
// the public ones the presets name.
func (s *Service) Lookup(ctx context.Context, name, rtype string) (*LookupResult, error) {
	rtype = strings.ToUpper(strings.TrimSpace(rtype))
	if rtype == "" {
		rtype = "A"
	}
	if !lookupTypes[rtype] {
		return nil, fmt.Errorf("the record type is one of A, AAAA, CNAME, MX, TXT, NS, PTR or SRV")
	}
	name, err := cleanLookupName(name, rtype)
	if err != nil {
		return nil, err
	}
	targets := s.lookupTargets(ctx)

	res := &LookupResult{Name: name, Type: rtype, Answers: make([]LookupAnswer, len(targets))}
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		// Each goroutine ends when its lookup does, and the lookup ends at
		// lookupTimeout at the latest.
		go func() {
			defer wg.Done()
			start := time.Now()
			answers, err := lookupVia(ctx, t.server, name, rtype)
			a := LookupAnswer{Server: t.server, Label: t.label, Answers: answers, LatencyMS: millis(time.Since(start))}
			if a.Answers == nil {
				a.Answers = []string{}
			}
			if err != nil {
				a.Error = lookupError(err)
			}
			res.Answers[i] = a
		}()
	}
	wg.Wait()
	return res, nil
}

// lookupTargets are the resolvers in view: the stub, every server resolved
// uses (global, then each link's), and each preset's first IPv4 address. A
// server in two places is asked once, under the first label.
func (s *Service) lookupTargets(ctx context.Context) []lookupTarget {
	var out []lookupTarget
	seen := map[string]bool{}
	add := func(server, label string) {
		if len(out) >= maxLookupResolvers || seen[server] {
			return
		}
		seen[server] = true
		out = append(out, lookupTarget{server, label})
	}
	addEntries := func(entries []string, label string) {
		for _, e := range entries {
			if a, ok := serverIP(e); ok {
				add(a.String(), label)
			}
		}
	}

	rv := s.readResolved(ctx, false)
	if rv.Active {
		add(resolvedStub, "systemd-resolved stub")
		addEntries(rv.Global.Servers, "global upstream")
		addEntries(rv.Global.Fallback, "global fallback")
		for _, l := range rv.Links {
			addEntries(l.Servers, l.Name)
		}
	} else {
		addEntries(readResolvConf(resolvConfPath).Nameservers, "resolv.conf")
	}
	for _, p := range DNSPresets() {
		for _, sv := range p.Servers {
			if a, err := netip.ParseAddr(sv); err == nil && a.Is4() {
				add(sv, p.Name)
				break
			}
		}
	}
	return out
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
	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dnsDial(ctx, network, net.JoinHostPort(server, "53"))
		},
	}
	fqdn := name + "."
	switch rtype {
	case "A", "AAAA":
		network := "ip4"
		if rtype == "AAAA" {
			network = "ip6"
		}
		ips, err := r.LookupIP(ctx, network, fqdn)
		out := make([]string, 0, len(ips))
		for _, ip := range ips {
			out = append(out, ip.String())
		}
		return out, err
	case "CNAME":
		cname, err := r.LookupCNAME(ctx, fqdn)
		if err != nil {
			return nil, err
		}
		// With no CNAME the resolver hands the name back.
		if strings.EqualFold(strings.TrimSuffix(cname, "."), name) {
			return []string{}, nil
		}
		return []string{cname}, nil
	case "MX":
		mx, err := r.LookupMX(ctx, fqdn)
		out := make([]string, 0, len(mx))
		for _, m := range mx {
			out = append(out, fmt.Sprintf("%d %s", m.Pref, m.Host))
		}
		return out, err
	case "TXT":
		return r.LookupTXT(ctx, fqdn)
	case "NS":
		ns, err := r.LookupNS(ctx, fqdn)
		out := make([]string, 0, len(ns))
		for _, n := range ns {
			out = append(out, n.Host)
		}
		sort.Strings(out)
		return out, err
	case "PTR":
		return r.LookupAddr(ctx, name)
	case "SRV":
		_, srv, err := r.LookupSRV(ctx, "", "", fqdn)
		out := make([]string, 0, len(srv))
		for _, s := range srv {
			out = append(out, fmt.Sprintf("%d %d %d %s", s.Priority, s.Weight, s.Port, s.Target))
		}
		return out, err
	}
	return nil, fmt.Errorf("unsupported record type %s", rtype)
}

// lookupError shortens Go's resolver errors to what a table cell can hold. A
// name that does not exist is an answer, not a fault, and reads as one.
func lookupError(err error) string {
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
