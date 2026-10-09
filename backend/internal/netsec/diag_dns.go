package netsec

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Where a DNS answer came from. The lookup tool asks the dashboard process's
// own resolver, which reads its hosts file before DNS and follows search
// domains; an answer from either looks the same in a list of addresses. This
// reads the configuration that resolver follows and asks each configured
// nameserver the absolute question directly, so a hosts override or a
// disagreeing server is visible as such.

var (
	resolverFiles  = struct{ Hosts, Resolv, NSSwitch string }{"/etc/hosts", "/etc/resolv.conf", "/etc/nsswitch.conf"}
	lookupResolver = net.DefaultResolver
	dnsPort        = "53"
)

type resolvConf struct {
	Nameservers []string
	Search      []string
	Options     []string
}

func readResolvConf(path string) (resolvConf, error) {
	var rc resolvConf
	f, err := os.Open(path)
	if err != nil {
		return rc, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(strings.SplitN(scanner.Text(), "#", 2)[0])
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "nameserver":
			if _, err := netip.ParseAddr(fields[1]); err == nil && len(rc.Nameservers) < 3 {
				rc.Nameservers = append(rc.Nameservers, fields[1])
			}
		case "search", "domain":
			rc.Search = fields[1:]
		case "options":
			rc.Options = append(rc.Options, fields[1:]...)
		}
	}
	return rc, scanner.Err()
}

// readNSSHosts returns the sources of the hosts database in order. Go's own
// resolver honours files and dns from this line; other modules need cgo.
func readNSSHosts(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if value, ok := strings.CutPrefix(line, "hosts:"); ok {
			return strings.Join(strings.Fields(value), " "), true
		}
	}
	return "", false
}

// hostsFileMatches is what the hosts file answers for a name (A/AAAA) or an
// address (PTR).
func hostsFileMatches(path, target, recordType string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	target = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(target), "."))
	var ptr netip.Addr
	if recordType == "PTR" {
		ptr, _ = netip.ParseAddr(target)
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(strings.SplitN(line, "#", 2)[0])
		if len(fields) < 2 {
			continue
		}
		addr, err := netip.ParseAddr(fields[0])
		if err != nil {
			continue
		}
		addr = addr.Unmap()
		if recordType == "PTR" {
			if ptr.IsValid() && addr == ptr.Unmap() {
				out = append(out, strings.ToLower(fields[1]))
			}
			continue
		}
		if (recordType == "A") != addr.Is4() {
			continue
		}
		for _, name := range fields[1:] {
			if strings.ToLower(strings.TrimSuffix(name, ".")) == target {
				out = append(out, addr.String())
				break
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

type answerSource struct {
	Value  string
	Source string
}

// attributeAnswers says where each answer the process resolver gave could
// have come from. "neither" is kept: a search domain, another NSS module or an
// answer that changed between the two questions all look like that.
func attributeAnswers(process, hosts, wire []string) []answerSource {
	inHosts, inWire := map[string]bool{}, map[string]bool{}
	for _, v := range hosts {
		inHosts[strings.ToLower(v)] = true
	}
	for _, v := range wire {
		inWire[strings.ToLower(v)] = true
	}
	out := make([]answerSource, 0, len(process))
	for _, value := range process {
		key := strings.ToLower(strings.TrimSuffix(value, "."))
		source := "not matched in the hosts file or the direct DNS answers"
		switch {
		case inHosts[key] && inWire[key]:
			source = "hosts file and DNS agree"
		case inHosts[key]:
			source = "hosts file (NSS files)"
		case inWire[key]:
			source = "DNS wire answer"
		}
		out = append(out, answerSource{Value: value, Source: source})
	}
	return out
}

func lookupWireValues(reply *dnsReply, recordType string) []string {
	values := rrValues(reply.Answer, recordType)
	if recordType == "MX" {
		for i, v := range values {
			values[i] = strings.TrimSuffix(v, ".")
		}
	}
	sort.Strings(values)
	return values
}

// addLookupProvenance runs after the process resolver has answered. It
// returns the first configured nameserver's response code, which tells a
// name that does not exist from one without records of this type: Go's
// resolver reports both as not found.
func addLookupProvenance(ctx context.Context, res *ProbeResult, target, recordType string) string {
	target = strings.TrimSpace(target)
	res.fact("Resolver", "This dashboard's process resolver (Go, host network namespace)", BasisConfigured)
	nss, ok := readNSSHosts(resolverFiles.NSSwitch)
	switch {
	case !ok:
		res.fact("Name service order", "files dns (no hosts: line was readable; the resolver's default)", BasisInferred)
		nss = "files dns"
	default:
		res.fact("Name service order", nss, BasisConfigured)
	}
	rc, rcErr := readResolvConf(resolverFiles.Resolv)
	if rcErr != nil {
		res.fact("Nameservers", "unreadable: "+rcErr.Error(), BasisUnknown)
	} else if len(rc.Nameservers) == 0 {
		res.fact("Nameservers", "none configured", BasisConfigured)
	} else {
		res.fact("Nameservers", strings.Join(rc.Nameservers, ", "), BasisConfigured)
	}
	if len(rc.Search) > 0 {
		value := strings.Join(rc.Search, " ")
		if strings.HasSuffix(target, ".") {
			value += " (not applied: the name ends with a dot)"
		}
		res.fact("Search domains", value, BasisConfigured)
	}
	table := ProbeTable{ID: "sources", Title: "Answers by source", Columns: []string{"Source", "Transport", "Result", "Answers", "TTL (s)", "Time"}}
	var hosts []string
	if recordType == "A" || recordType == "AAAA" || recordType == "PTR" {
		if strings.Contains(nss, "files") {
			var err error
			hosts, err = hostsFileMatches(resolverFiles.Hosts, target, recordType)
			switch {
			case err != nil:
				table.Rows = append(table.Rows, []string{"hosts file (" + resolverFiles.Hosts + ")", "file", "unreadable", err.Error(), "", ""})
			case len(hosts) == 0:
				table.Rows = append(table.Rows, []string{"hosts file (" + resolverFiles.Hosts + ")", "file", "no entry", "", "", ""})
			default:
				table.Rows = append(table.Rows, []string{"hosts file (" + resolverFiles.Hosts + ")", "file", "entry", strings.Join(hosts, ", "), "", ""})
			}
		} else {
			table.Rows = append(table.Rows, []string{"hosts file", "file", "not consulted (not in name service order)", "", "", ""})
		}
	}
	question := strings.TrimSuffix(target, ".")
	if recordType == "PTR" {
		if a, err := netip.ParseAddr(target); err == nil {
			question = reverseName(a)
		}
	}
	var wire []string
	firstRCode := ""
	answered := 0
	distinct := map[string]bool{}
	stub := false
	for _, server := range rc.Nameservers {
		addr, _ := netip.ParseAddr(server)
		if addr.IsLoopback() {
			stub = true
		}
		reply, err := dnsExchange(ctx, net.JoinHostPort(server, dnsPort), question, recordType, true)
		if err != nil {
			table.Rows = append(table.Rows, []string{"nameserver " + server, "UDP", "no answer", shortDialError(err), "", ""})
			continue
		}
		values := lookupWireValues(reply, recordType)
		ttl := ""
		for _, rr := range reply.Answer {
			if rr.Type == recordType {
				ttl = strconv.FormatUint(uint64(rr.TTL), 10)
				break
			}
		}
		result := reply.RCode
		if reply.RCode == "NOERROR" && len(values) == 0 {
			result = "NOERROR, no " + recordType + " records"
		}
		table.Rows = append(table.Rows, []string{"nameserver " + server, reply.Transport, result, strings.Join(values, ", "), ttl, reply.Elapsed.Round(time.Millisecond).String()})
		if answered == 0 {
			wire, firstRCode = values, reply.RCode
		}
		answered++
		distinct[strings.Join(values, ",")] = true
	}
	res.Tables = append(res.Tables, table)
	if len(res.Records) > 0 {
		attributed := ProbeTable{ID: "attribution", Title: "Where each answer came from", Columns: []string{"Answer", "Source"}}
		hostOnly := false
		for _, a := range attributeAnswers(res.Records, hosts, wire) {
			attributed.Rows = append(attributed.Rows, []string{a.Value, a.Source})
			hostOnly = hostOnly || a.Source == "hosts file (NSS files)"
		}
		res.Tables = append(res.Tables, attributed)
		switch {
		case hostOnly && len(wire) > 0:
			res.finding("hosts-override", "warning", "The hosts file overrides DNS for this name",
				"Services on this host resolve "+target+" from "+resolverFiles.Hosts+"; the configured nameservers answer "+strings.Join(wire, ", ")+". Other machines follow DNS.", "This host's hosts file")
		case hostOnly && answered > 0:
			res.finding("hosts-only", "notice", "Only this host's hosts file knows this name",
				"DNS has no "+recordType+" answer for "+target+", so other machines cannot resolve it the way services on this host do.", "This host's hosts file")
		}
	}
	if answered > 1 && len(distinct) > 1 {
		res.finding("nameservers-disagree", "notice", "Configured nameservers gave different answers",
			"Compare the rows above. The process resolver uses the first server that answers, so which answer a service sees can change when a server is slow.", "Configured resolvers")
	}
	if stub {
		res.link("Inspect the local stub's upstream servers and policy", "/network/dns")
		res.Limitations = append(res.Limitations, "A loopback nameserver is a local stub or cache; its own upstream servers and encrypted transport are configured on the DNS page, not visible in this answer.")
	}
	res.Limitations = append(res.Limitations, "Direct questions are sent to each configured nameserver with recursion desired, over UDP with TCP only after truncation. Encrypted DNS used by a stub is not observed here.")
	return firstRCode
}

// The authority checker's dependencies, replaced by fixtures in tests.
type authorityDeps struct {
	lookupNS func(context.Context, string) ([]string, error)
	lookupIP func(context.Context, string) ([]netip.Addr, error)
	exchange func(context.Context, string, string, string, bool) (*dnsReply, error)
}

var authorityLookups = authorityDeps{
	lookupNS: func(ctx context.Context, name string) ([]string, error) {
		ns, err := lookupResolver.LookupNS(ctx, name)
		out := make([]string, 0, len(ns))
		for _, n := range ns {
			out = append(out, strings.ToLower(strings.TrimSuffix(n.Host, ".")))
		}
		return out, err
	},
	lookupIP: func(ctx context.Context, host string) ([]netip.Addr, error) {
		return lookupResolver.LookupNetIP(ctx, "ip", host)
	},
	exchange: func(ctx context.Context, server, name, rtype string, recursion bool) (*dnsReply, error) {
		return dnsExchange(ctx, server, name, rtype, recursion)
	},
}

func inBailiwick(host, zone string) bool {
	return host == zone || strings.HasSuffix(host, "."+zone)
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, v := range a {
		seen[v]++
	}
	for _, v := range b {
		if seen[v] == 0 {
			return false
		}
		seen[v]--
	}
	return true
}

func sortedUnique(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range values {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

// checkAuthority finds the zone, reads its delegation and glue from a parent
// server, then asks every authoritative address the same two questions with
// recursion off.
func checkAuthority(ctx context.Context, res *ProbeResult, target string, deps authorityDeps) {
	name := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(target), "."))
	zone := ""
	var nsHosts []string
	for candidate := name; candidate != ""; {
		hosts, err := deps.lookupNS(ctx, candidate)
		if err == nil && len(hosts) > 0 {
			zone, nsHosts = candidate, sortedUnique(hosts)
			break
		}
		_, rest, found := strings.Cut(candidate, ".")
		if !found {
			break
		}
		candidate = rest
	}
	if zone == "" {
		res.OK, res.Verdict = false, ProbeFailed
		res.Error = "no nameserver set was found for " + name + " or any parent zone"
		res.Summary = "No zone with a nameserver set was found for this name."
		return
	}
	res.fact("Zone", zone, BasisObserved)
	if zone != name {
		res.fact("Queried name", name+" (inside the zone)", BasisObserved)
	}
	res.fact("Zone nameservers (via this host's resolver)", strings.Join(nsHosts, ", "), BasisObserved)
	res.Records = append(res.Records, nsHosts...)
	addresses := map[string][]string{}
	for _, host := range nsHosts {
		addrs, err := deps.lookupIP(ctx, host)
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if len(addresses[host]) < 4 {
				addresses[host] = append(addresses[host], a.Unmap().String())
			}
		}
		sort.Strings(addresses[host])
	}
	problems := 0

	// Delegation and glue, as the parent zone's servers hand them out.
	parentKnown := false
	if _, parent, found := strings.Cut(zone, "."); found {
		var parentHosts []string
		parentZone := ""
		for candidate := parent; candidate != ""; {
			hosts, err := deps.lookupNS(ctx, candidate)
			if err == nil && len(hosts) > 0 {
				parentZone, parentHosts = candidate, sortedUnique(hosts)
				break
			}
			_, rest, ok := strings.Cut(candidate, ".")
			if !ok {
				break
			}
			candidate = rest
		}
		var referral *dnsReply
		var referralFrom string
		for _, host := range parentHosts {
			addrs, err := deps.lookupIP(ctx, host)
			if err != nil || len(addrs) == 0 {
				continue
			}
			server := net.JoinHostPort(addrs[0].Unmap().String(), dnsPort)
			reply, err := deps.exchange(ctx, server, zone, "NS", false)
			if err == nil {
				referral, referralFrom = reply, host
				break
			}
		}
		switch {
		case parentZone == "":
			res.fact("Delegation", "the parent zone's nameservers could not be found", BasisUnknown)
		case referral == nil:
			res.fact("Delegation", "no server for "+parentZone+" answered", BasisUnknown)
		default:
			parentKnown = true
			delegated := sortedUnique(append(rrValues(referral.Authority, "NS"), rrValues(referral.Answer, "NS")...))
			res.fact("Delegation (from "+referralFrom+" for "+parentZone+")", nonEmptyOr(strings.Join(delegated, ", "), "none"), BasisObserved)
			glue := map[string][]string{}
			for _, rr := range referral.Additional {
				if rr.Type == "A" || rr.Type == "AAAA" {
					glue[rr.Name] = append(glue[rr.Name], rr.Value)
				}
			}
			table := ProbeTable{ID: "delegation", Title: "Delegation from the parent zone", Columns: []string{"Nameserver", "Glue at parent", "Listed by the zone", "Glue check"}}
			for _, host := range delegated {
				g := sortedUnique(glue[host])
				check := "not needed (out of zone)"
				if inBailiwick(host, zone) {
					switch {
					case len(g) == 0:
						check = "missing"
						problems++
						res.finding("glue-missing-"+host, "warning", "Missing glue for "+host,
							host+" is inside "+zone+", so resolvers need its address from the parent zone; "+parentZone+" did not return one.", "Registrar / parent zone")
					case len(addresses[host]) > 0 && !sameSet(g, addresses[host]):
						check = "differs from the nameserver's records"
						problems++
						res.finding("glue-mismatch-"+host, "warning", "Glue for "+host+" disagrees with its address records",
							"The parent hands out "+strings.Join(g, ", ")+"; the zone publishes "+strings.Join(addresses[host], ", ")+".", "Registrar / parent zone")
					default:
						check = "present"
					}
				} else if len(g) > 0 {
					check = "present (not required)"
				}
				listed := "no"
				for _, n := range nsHosts {
					if n == host {
						listed = "yes"
					}
				}
				table.Rows = append(table.Rows, []string{host, nonEmptyOr(strings.Join(g, ", "), "none"), listed, check})
			}
			res.Tables = append(res.Tables, table)
			if !sameSet(delegated, nsHosts) {
				problems++
				res.finding("delegation-mismatch", "warning", "The parent's delegation and the zone's NS records differ",
					"Parent: "+nonEmptyOr(strings.Join(delegated, ", "), "none")+". Zone: "+strings.Join(nsHosts, ", ")+". Resolvers may use either set.", "Registrar / DNS provider")
			}
		}
	} else {
		res.fact("Delegation", "not checked: "+zone+" is a top-level zone", BasisUnknown)
	}

	// Every authoritative address, asked with recursion off.
	table := ProbeTable{ID: "authorities", Title: "Authoritative servers", Columns: []string{"Nameserver", "Address", "Authoritative", "SOA serial", "NS set", "Time"}}
	serials := map[uint32][]string{}
	answering, asked := 0, 0
	for _, host := range nsHosts {
		if len(addresses[host]) == 0 {
			problems++
			table.Rows = append(table.Rows, []string{host, "does not resolve", "", "", "", ""})
			res.finding("ns-unresolved-"+host, "warning", host+" has no address", "Resolvers cannot reach a nameserver whose name does not resolve.", "DNS provider")
			continue
		}
		for _, addr := range addresses[host] {
			if asked >= 12 {
				break
			}
			asked++
			server := net.JoinHostPort(addr, dnsPort)
			soa, err := deps.exchange(ctx, server, zone, "SOA", false)
			if err != nil {
				problems++
				table.Rows = append(table.Rows, []string{host, addr, "no answer", "", "", shortDialError(err)})
				res.finding("ns-silent-"+addr, "warning", host+" ("+addr+") did not answer", "A nameserver that does not answer slows or fails resolution for some clients.", "DNS provider")
				continue
			}
			if soa.RCode != "NOERROR" || !soa.Authoritative {
				problems++
				state := soa.RCode
				if soa.RCode == "NOERROR" {
					state = "no (lame)"
				}
				table.Rows = append(table.Rows, []string{host, addr, state, "", "", soa.Elapsed.Round(time.Millisecond).String()})
				res.finding("ns-lame-"+addr, "warning", host+" ("+addr+") is not authoritative for "+zone, "It answered "+soa.RCode+" without the authoritative flag: a lame delegation.", "DNS provider")
				continue
			}
			answering++
			serial := ""
			for _, rr := range soa.Answer {
				if rr.Type == "SOA" {
					serial = strconv.FormatUint(uint64(rr.Serial), 10)
					serials[rr.Serial] = append(serials[rr.Serial], addr)
					res.Records = append(res.Records, host+" "+addr+" serial "+serial)
					break
				}
			}
			nsState := "not read"
			if ns, err := deps.exchange(ctx, server, zone, "NS", false); err == nil {
				set := sortedUnique(rrValues(ns.Answer, "NS"))
				if sameSet(set, nsHosts) {
					nsState = "matches"
				} else {
					nsState = "differs: " + strings.Join(set, ", ")
					problems++
					res.finding("ns-set-"+addr, "warning", host+" ("+addr+") lists a different NS set", "It answers "+nonEmptyOr(strings.Join(set, ", "), "no NS records")+".", "DNS provider")
				}
			}
			table.Rows = append(table.Rows, []string{host, addr, "yes", nonEmptyOr(serial, "none"), nsState, soa.Elapsed.Round(time.Millisecond).String()})
		}
	}
	res.Tables = append(res.Tables, table)
	if len(serials) > 1 {
		problems++
		var parts []string
		for serial, addrs := range serials {
			parts = append(parts, fmt.Sprintf("%d on %s", serial, strings.Join(addrs, ", ")))
		}
		sort.Strings(parts)
		res.finding("serial-mismatch", "warning", "Authoritative servers disagree on the SOA serial",
			strings.Join(parts, "; ")+". A recent change may still be propagating between them; otherwise a secondary is not transferring the zone.", "DNS provider")
	}
	if len(nsHosts) == 1 {
		res.finding("single-ns", "notice", "Only one nameserver", "If it is unreachable the zone does not resolve at all.", "DNS provider")
	}
	res.metric("nameservers", "Nameservers", float64(len(nsHosts)), "")
	res.metric("answering", "Authoritative addresses answering", float64(answering), "")
	res.metric("serials", "Distinct SOA serials", float64(len(serials)), "")
	res.Limitations = append(res.Limitations, "Questions go straight to each address from this host; anycast and per-region answers can differ elsewhere.")
	switch {
	case answering == 0:
		res.OK, res.Verdict = false, ProbeFailed
		res.Summary = "No authoritative server for " + zone + " answered."
	case problems > 0:
		res.OK, res.Verdict = true, ProbeFindings
		res.Summary = fmt.Sprintf("%d of %d authoritative addresses answered; %d consistency problem(s).", answering, asked, problems)
	case !parentKnown:
		res.OK, res.Verdict = true, ProbeUnknown
		res.Summary = fmt.Sprintf("%d authoritative addresses agree; the parent delegation could not be checked.", answering)
	default:
		res.OK, res.Verdict = true, ProbeOK
		res.Summary = fmt.Sprintf("Delegation, glue and %d authoritative addresses agree (serial consistent).", answering)
	}
}
