package proxysvc

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// What would make an issuance fail, asked before it is run.
//
// A real run that fails costs one of five failed validations an hour, and
// the reason certbot prints is the authority's — "unauthorized", "CAA record
// prevents issuance", "Connection refused" — read back a minute later. Each
// of those has a cause this host can look at first: where the name resolves,
// what its CAA records allow, whether the challenge path answers here, and
// what certbot will do with the lineage it already has.

// PreflightCheck is one finding about the request, in FindingList's shape.
// Level "ok" is a check that passed; the page lists only the others.
type PreflightCheck struct {
	ID     string `json:"id"`
	Name   string `json:"name,omitempty"`
	Check  string `json:"check"`
	Level  string `json:"level"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Advice string `json:"advice,omitempty"`
}

// LineageRelation is what certbot does with the lineages it already has when
// asked for these names: nothing new ("identical"), a new list for the one
// --cert-name names ("replaces"), a refusal under --non-interactive for a
// lineage the names contain ("expands"), or a new lineage ("new").
type LineageRelation struct {
	Kind    string   `json:"kind"`
	Lineage string   `json:"lineage,omitempty"`
	Added   []string `json:"added,omitempty"`
	Removed []string `json:"removed,omitempty"`
}

type IssuePreflight struct {
	Checks   []PreflightCheck `json:"checks"`
	Relation LineageRelation  `json:"relation"`
	Limits   RateLimits       `json:"limits"`
	// Blocking is a critical check: the real run is expected to fail.
	Blocking  bool      `json:"blocking"`
	CheckedAt time.Time `json:"checkedAt"`
}

const letsEncryptCAA = "letsencrypt.org"

// ValidCertDomain is a name certbot's -d takes: a host name or a wildcard.
func ValidCertDomain(name string) bool { return certDomainRe.MatchString(name) }

// IssuePreflight checks a request's names from this host. It changes
// nothing except a challenge file it writes into the webroot and removes.
func (s *Service) IssuePreflight(ctx context.Context, req IssueRequest, failed []FailedIssue) (*IssuePreflight, error) {
	if len(req.Domains) == 0 {
		return nil, fmt.Errorf("at least one domain is required")
	}
	if len(req.Domains) > 100 {
		return nil, fmt.Errorf("a certificate may cover at most 100 domains")
	}
	for _, d := range req.Domains {
		if !certDomainRe.MatchString(d) {
			return nil, fmt.Errorf("%q is not a valid domain name", d)
		}
	}
	switch req.Method {
	case "nginx", "standalone", "dns":
	case "webroot":
		if !absPathRe.MatchString(req.WebRoot) || filepath.Clean(req.WebRoot) != req.WebRoot {
			return nil, fmt.Errorf("the webroot must be an absolute path")
		}
	default:
		return nil, fmt.Errorf("method must be nginx, webroot, standalone or dns")
	}
	if req.CertName != "" && !certNameRe.MatchString(req.CertName) {
		return nil, fmt.Errorf("a certificate name is letters, digits, dots, dashes and underscores")
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	result := &IssuePreflight{Checks: []PreflightCheck{}, CheckedAt: time.Now().UTC()}
	var mu sync.Mutex
	add := func(checks ...PreflightCheck) {
		mu.Lock()
		result.Checks = append(result.Checks, checks...)
		mu.Unlock()
	}
	authority, err := authorityFor(req)
	if err != nil {
		return nil, err
	}

	var wg sync.WaitGroup
	slots := make(chan struct{}, 8)
	for _, name := range req.Domains {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			add(nameResolution(ctx, name, req.Method))
			add(caaCheck(ctx, name, authority))
		}(strings.ToLower(name))
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		add(s.challengeChecks(ctx, req)...)
	}()
	wg.Wait()

	confs, _ := readRenewalConfs(letsencryptDir)
	relation, check := lineageRelation(confs, req.Domains, req.CertName)
	result.Relation = relation
	add(check)

	result.Limits = CertificateRateLimits(req.Domains, failed)
	// The limits counted are Let's Encrypt's, whatever JD_ACME_DIRECTORY says
	// when the form names another authority.
	result.Limits.Applies = authority.LetsEncrypt && !authority.Staging
	add(rateChecks(result.Limits, relation, req.Staging)...)

	order := map[string]int{"critical": 0, "warning": 1, "notice": 2, "ok": 3}
	sort.SliceStable(result.Checks, func(i, j int) bool {
		a, b := result.Checks[i], result.Checks[j]
		if order[a.Level] != order[b.Level] {
			return order[a.Level] < order[b.Level]
		}
		if a.Check != b.Check {
			return a.Check < b.Check
		}
		return a.Name < b.Name
	})
	for _, c := range result.Checks {
		result.Blocking = result.Blocking || c.Level == "critical"
	}
	return result, nil
}

// nameResolution is CheckDomainDNS read for the challenge: an HTTP challenge
// needs the name to reach this host, a DNS one needs nothing of the A record.
func nameResolution(ctx context.Context, name, method string) PreflightCheck {
	check := PreflightCheck{ID: "dns:" + name, Name: name, Check: "dns"}
	if strings.HasPrefix(name, "*.") {
		check.Level = "ok"
		check.Title = name + " is proved in DNS"
		check.Detail = "A wildcard has no address of its own to check: the authority reads a TXT record instead."
		if method != "dns" {
			check.Level = "critical"
			check.Title = name + " needs a DNS challenge"
			check.Detail = "An ACME authority signs a wildcard only against a DNS-01 challenge."
			check.Advice = "Choose DNS as the method."
		}
		return check
	}
	dns := CheckDomainDNS(ctx, name)
	addresses := strings.Join(dns.Addresses, ", ")
	switch {
	case method == "dns":
		check.Level = "ok"
		check.Title = name + " is proved in DNS"
		check.Detail = "A DNS challenge does not need the name to reach this host."
		if dns.Error != "" {
			check.Level = "notice"
			check.Title = name + " does not resolve"
			check.Detail = "The certificate can still be issued over DNS, but no client can reach a site on this name yet."
		}
	case dns.Error != "":
		check.Level = "critical"
		check.Title = name + " does not resolve"
		check.Detail = dns.Error
		check.Advice = "Create an A or AAAA record for it pointing at this server, or use a DNS challenge."
	case dns.PointsHere:
		check.Level = "ok"
		check.Title = name + " resolves here"
		check.Detail = "It resolves to " + addresses + ", an address of this host."
	case dns.BehindProxy:
		check.Level = "warning"
		check.Title = name + " resolves to a CDN"
		check.Detail = "It resolves to " + addresses + ". The authority's request goes to the CDN, and passes only if the CDN forwards " + acmeChallengePath + " to this host over plain HTTP."
		check.Advice = "A DNS challenge does not depend on the CDN."
	case !dns.HostAddressesKnown:
		check.Level = "warning"
		check.Title = name + " resolves to " + addresses
		check.Detail = "This host has no public address on any interface, so whether that address is this server's could not be compared."
	default:
		check.Level = "critical"
		check.Title = name + " resolves elsewhere"
		check.Detail = "It resolves to " + addresses + "; this host's addresses are " + strings.Join(dns.HostAddresses, ", ") + ". The authority's request would go there."
		check.Advice = "Point the record at this server and wait for its TTL, or use a DNS challenge."
	}
	return check
}

// caaRecord is one CAA resource record (RFC 8659): a flag byte, a tag and a
// value.
type caaRecord struct {
	Flags uint8
	Tag   string
	Value string
	// Domain is the name the records were published at: the request's own,
	// or the closest parent that has any.
	Domain string
}

// caaCheck asks whether the authority may issue for name. Its CAA
// identifier is empty when this host does not know it.
func caaCheck(ctx context.Context, name string, authority IssueAuthority) PreflightCheck {
	check := PreflightCheck{ID: "caa:" + name, Name: name, Check: "caa"}
	wildcard := strings.HasPrefix(name, "*.")
	records, err := lookupCAASet(ctx, strings.TrimPrefix(name, "*."))
	if err != nil {
		check.Level = "warning"
		check.Title = "CAA for " + name + " could not be read"
		check.Detail = err.Error() + ". An authority that cannot read CAA records refuses to issue."
		return check
	}
	issuer := authority.caa
	allowed, restricted, listed := caaPermits(records, wildcard, issuer)
	switch {
	case !restricted:
		check.Level = "ok"
		check.Title = "No CAA record limits " + name
		check.Detail = "Without a CAA record any authority may issue for it."
	case issuer == "":
		check.Level = "warning"
		check.Title = "CAA for " + name + " names " + strings.Join(listed, ", ")
		check.Detail = "Published at " + records[0].Domain + ". Whether " + authorityHost(authority.Server) + " is one of these is its own CAA identifier, which this host does not know."
	case allowed:
		check.Level = "ok"
		check.Title = "CAA allows " + authority.Name + " for " + name
		check.Detail = "Published at " + records[0].Domain + ": " + strings.Join(listed, ", ") + "."
	default:
		check.Level = "critical"
		check.Title = "CAA forbids " + authority.Name + " for " + name
		check.Detail = "Published at " + records[0].Domain + ", it allows " + strings.Join(listed, ", ") + ". The authority must refuse."
		tag := "issue"
		if wildcard {
			tag = "issuewild"
		}
		check.Advice = fmt.Sprintf("Add a CAA record at %s: 0 %s \"%s\".", records[0].Domain, tag, issuer)
	}
	return check
}

// caaPermits evaluates the relevant record set (RFC 8659 §4): issuewild for
// a wildcard where there is any, issue otherwise; no such property is no
// restriction. An unknown tag marked critical forbids everyone.
func caaPermits(records []caaRecord, wildcard bool, issuer string) (allowed, restricted bool, listed []string) {
	var issue, issuewild []caaRecord
	for _, r := range records {
		switch strings.ToLower(r.Tag) {
		case "issue":
			issue = append(issue, r)
		case "issuewild":
			issuewild = append(issuewild, r)
		case "iodef", "issuemail", "issuevmc", "contactemail", "contactphone":
		default:
			if r.Flags&0x80 != 0 {
				return false, true, []string{"nobody (an unknown critical tag " + r.Tag + ")"}
			}
		}
	}
	set := issue
	if wildcard && len(issuewild) > 0 {
		set = issuewild
	}
	if len(set) == 0 {
		return true, false, nil
	}
	for _, r := range set {
		domain, _, _ := strings.Cut(r.Value, ";")
		domain = strings.TrimSpace(domain)
		if domain == "" {
			listed = append(listed, "no authority")
			continue
		}
		listed = append(listed, domain)
		if issuer != "" && strings.EqualFold(domain, issuer) {
			allowed = true
		}
	}
	return allowed, true, listed
}

// lookupCAASet climbs from name towards the root and answers the first
// non-empty CAA set, the one an authority applies.
func lookupCAASet(ctx context.Context, name string) ([]caaRecord, error) {
	servers, err := resolvConfServers("/etc/resolv.conf")
	if err != nil {
		return nil, err
	}
	labels := strings.Split(strings.TrimSuffix(name, "."), ".")
	for i := range labels {
		domain := strings.Join(labels[i:], ".")
		records, err := queryCAA(ctx, servers, domain)
		if err != nil {
			return nil, fmt.Errorf("the CAA query for %s failed: %w", domain, err)
		}
		if len(records) > 0 {
			return records, nil
		}
	}
	return nil, nil
}

// resolvConfServers is the nameservers the system resolver asks. Go's
// resolver has no CAA lookup, so the query is built here and sent to them.
func resolvConfServers(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("no resolver to ask: %w", err)
	}
	defer file.Close()
	var servers []string
	sc := bufio.NewScanner(file)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) >= 2 && fields[0] == "nameserver" && net.ParseIP(fields[1]) != nil {
			servers = append(servers, net.JoinHostPort(fields[1], "53"))
		}
	}
	if len(servers) == 0 {
		return nil, fmt.Errorf("no nameserver in %s", path)
	}
	return servers, nil
}

const typeCAA = dnsmessage.Type(257)

func queryCAA(ctx context.Context, servers []string, domain string) ([]caaRecord, error) {
	var last error
	for _, server := range servers {
		records, err := exchangeCAA(ctx, server, domain)
		if err == nil {
			return records, nil
		}
		last = err
		if ctx.Err() != nil {
			break
		}
	}
	return nil, last
}

func exchangeCAA(ctx context.Context, server, domain string) ([]caaRecord, error) {
	name, err := dnsmessage.NewName(domain + ".")
	if err != nil {
		return nil, err
	}
	var idBytes [2]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return nil, err
	}
	id := binary.BigEndian.Uint16(idBytes[:])
	builder := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: id, RecursionDesired: true})
	if err := builder.StartQuestions(); err != nil {
		return nil, err
	}
	if err := builder.Question(dnsmessage.Question{Name: name, Type: typeCAA, Class: dnsmessage.ClassINET}); err != nil {
		return nil, err
	}
	query, err := builder.Finish()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	answer, err := dnsRoundTrip(ctx, "udp", server, query)
	if err != nil {
		return nil, err
	}
	records, truncated, err := parseCAAAnswer(answer, id, domain)
	if truncated {
		answer, err = dnsRoundTrip(ctx, "tcp", server, query)
		if err != nil {
			return nil, err
		}
		records, _, err = parseCAAAnswer(answer, id, domain)
	}
	return records, err
}

func dnsRoundTrip(ctx context.Context, network, server string, query []byte) ([]byte, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, network, server)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if network == "udp" {
		if _, err := conn.Write(query); err != nil {
			return nil, err
		}
		buf := make([]byte, 4096)
		n, err := conn.Read(buf)
		if err != nil {
			return nil, err
		}
		return buf[:n], nil
	}
	framed := make([]byte, 2+len(query))
	binary.BigEndian.PutUint16(framed, uint16(len(query)))
	copy(framed[2:], query)
	if _, err := conn.Write(framed); err != nil {
		return nil, err
	}
	var size [2]byte
	if _, err := io.ReadFull(conn, size[:]); err != nil {
		return nil, err
	}
	answer := make([]byte, binary.BigEndian.Uint16(size[:]))
	if _, err := io.ReadFull(conn, answer); err != nil {
		return nil, err
	}
	return answer, nil
}

// parseCAAAnswer reads the CAA records of a response. NXDOMAIN is an empty
// set, as it is for the authority: the climb goes on to the parent.
func parseCAAAnswer(answer []byte, id uint16, domain string) ([]caaRecord, bool, error) {
	var parser dnsmessage.Parser
	header, err := parser.Start(answer)
	if err != nil {
		return nil, false, err
	}
	if header.ID != id || !header.Response {
		return nil, false, fmt.Errorf("the resolver's answer does not match the query")
	}
	if header.Truncated {
		return nil, true, nil
	}
	switch header.RCode {
	case dnsmessage.RCodeSuccess:
	case dnsmessage.RCodeNameError:
		return nil, false, nil
	default:
		return nil, false, fmt.Errorf("the resolver answered %s", header.RCode)
	}
	if err := parser.SkipAllQuestions(); err != nil {
		return nil, false, err
	}
	var records []caaRecord
	for {
		rh, err := parser.AnswerHeader()
		if errors.Is(err, dnsmessage.ErrSectionDone) {
			break
		}
		if err != nil {
			return nil, false, err
		}
		if rh.Type != typeCAA {
			if err := parser.SkipAnswer(); err != nil {
				return nil, false, err
			}
			continue
		}
		raw, err := parser.UnknownResource()
		if err != nil {
			return nil, false, err
		}
		record, ok := decodeCAA(raw.Data)
		if !ok {
			return nil, false, fmt.Errorf("a CAA record for %s is malformed", domain)
		}
		record.Domain = domain
		records = append(records, record)
	}
	return records, false, nil
}

func decodeCAA(data []byte) (caaRecord, bool) {
	if len(data) < 2 {
		return caaRecord{}, false
	}
	tagLen := int(data[1])
	if tagLen == 0 || len(data) < 2+tagLen {
		return caaRecord{}, false
	}
	return caaRecord{Flags: data[0], Tag: string(data[2 : 2+tagLen]), Value: string(data[2+tagLen:])}, true
}

// challengeChecks is what answers the authority's HTTP request on port 80
// for the chosen method. A DNS challenge has nothing listening to check.
func (s *Service) challengeChecks(ctx context.Context, req IssueRequest) []PreflightCheck {
	if req.Method == "dns" {
		return nil
	}
	var names []string
	for _, d := range req.Domains {
		if !strings.HasPrefix(d, "*.") {
			names = append(names, strings.ToLower(d))
		}
	}
	if len(names) == 0 {
		return nil
	}
	check := PreflightCheck{ID: "port80", Check: "http"}
	listeners, err := ListListeners(ctx)
	if err != nil {
		check.Level = "warning"
		check.Title = "Port 80 could not be read"
		check.Detail = err.Error()
		return []PreflightCheck{check}
	}
	var port80 []Listener
	for _, l := range listeners {
		if l.Protocol == "tcp" && l.Port == 80 {
			port80 = append(port80, l)
		}
	}
	holder := tcpHolder(listeners, 80)
	switch req.Method {
	case "standalone":
		if len(port80) > 0 {
			check.Level = "critical"
			check.Title = "Port 80 is taken by " + holder
			check.Detail = "certbot's standalone server binds port 80 for the challenge and cannot while another process holds it."
			check.Advice = "Use the nginx or webroot method, or stop " + holder + " for the run."
		} else {
			check.Level = "ok"
			check.Title = "Port 80 is free for certbot"
			check.Detail = "Nothing listens on port 80, so certbot's standalone server can bind it. Whether the internet reaches it is the firewall's to say."
		}
		return []PreflightCheck{check}
	case "nginx":
		switch {
		case len(port80) == 0:
			check.Level = "warning"
			check.Title = "Nothing listens on port 80"
			check.Detail = "certbot's nginx plugin answers the challenge in a server block for these names; nginx is not listening on port 80 now."
		case holder != "nginx":
			check.Level = "critical"
			check.Title = "Port 80 is held by " + holder
			check.Detail = "The authority's request reaches " + holder + ", not the nginx certbot configures."
		default:
			check.Level = "ok"
			check.Title = "nginx listens on port 80"
			check.Detail = "certbot's nginx plugin adds the challenge to it for the run. It is not probed ahead: the plugin writes that configuration itself."
		}
		return []PreflightCheck{check}
	}
	if len(port80) == 0 {
		check.Level = "critical"
		check.Title = "Nothing listens on port 80"
		check.Detail = "certbot writes the challenge into " + req.WebRoot + ", and no web server is there to serve it."
		return []PreflightCheck{check}
	}
	return probeIssueWebroot(ctx, req.WebRoot, names, port80)
}

// probeIssueWebroot writes a token where certbot would, on the side certbot
// runs on, asks the local web server for it under each name, and removes it.
// Asked over loopback, it proves the name is routed to this folder, not that
// the internet reaches port 80.
func probeIssueWebroot(ctx context.Context, root string, names []string, listeners []Listener) []PreflightCheck {
	fail := func(title, detail string) []PreflightCheck {
		return []PreflightCheck{{ID: "webroot", Check: "http", Level: "warning", Title: title, Detail: detail}}
	}
	rt, _ := loadCertbotRuntime(ctx)
	if rt == nil {
		return fail("The webroot was not probed", "certbot is not installed, so there is no side to write the challenge on.")
	}
	if rt.on(ctx, "test", "-d", root).Run() != nil {
		return []PreflightCheck{{ID: "webroot", Check: "http", Level: "critical",
			Title:  root + " is not a folder",
			Detail: "certbot would create it and write the challenge where no server looks (" + rt.where() + ")."}}
	}
	wellKnown := filepath.Join(root, ".well-known")
	dir := filepath.Join(wellKnown, "acme-challenge")
	// Only the folders made here are removed, and only while empty: rmdir
	// refuses anything else.
	var made []string
	for _, d := range []string{wellKnown, dir} {
		if rt.on(ctx, "test", "-d", d).Run() == nil {
			continue
		}
		if err := rt.on(ctx, "mkdir", d).Run(); err != nil {
			return fail("The webroot was not probed", "Could not create "+d+": "+err.Error())
		}
		made = append(made, d)
	}
	cleanupCtx := context.WithoutCancel(ctx)
	defer func() {
		for i := len(made) - 1; i >= 0; i-- {
			_ = rt.on(cleanupCtx, "rmdir", made[i]).Run()
		}
	}()
	out, err := rt.on(ctx, "mktemp", "-p", dir, "jd-preflight-XXXXXXXXXX").Output()
	path := strings.TrimSpace(string(out))
	if err != nil || filepath.Dir(path) != dir || !strings.HasPrefix(filepath.Base(path), "jd-preflight-") {
		return fail("The webroot was not probed", "Could not write a challenge file into "+dir+".")
	}
	defer func() { _ = rt.on(cleanupCtx, "rm", "-f", "--", path).Run() }()
	token := filepath.Base(path)
	write := rt.on(ctx, "tee", "--", path)
	write.Stdin = strings.NewReader(token)
	write.Stdout = io.Discard
	if err := write.Run(); err != nil {
		return fail("The webroot was not probed", "Could not write the challenge file: "+err.Error())
	}
	// mktemp makes the file 0600; the web server's user has to read it.
	if err := rt.on(ctx, "chmod", "0644", path).Run(); err != nil {
		return fail("The webroot was not probed", "Could not make the challenge file readable: "+err.Error())
	}

	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var endpoints []string
	seen := map[string]bool{}
	for _, l := range listeners {
		address := l.Address
		if address == "::" {
			address = "::1"
		} else if isWildcard(address) {
			address = "127.0.0.1"
		}
		endpoint := "http://" + net.JoinHostPort(address, strconv.Itoa(int(l.Port)))
		if !seen[endpoint] {
			seen[endpoint] = true
			endpoints = append(endpoints, endpoint)
		}
	}
	var checks []PreflightCheck
	for _, name := range names {
		check := PreflightCheck{ID: "http:" + name, Name: name, Check: "http", Level: "ok",
			Title:  name + " serves the challenge",
			Detail: "A test file written into " + dir + " came back over " + strings.Join(endpoints, " and ") + " under this name. The probe stays on this host; the firewall and any NAT in front of it are not tested."}
		for _, endpoint := range endpoints {
			problem, level := probeChallenge(ctx, client, endpoint, name, token)
			if problem != "" {
				check.Level = level
				check.Title = name + " does not serve the challenge"
				check.Detail = problem
				check.Advice = "Serve " + acmeChallengePath + " for this name from " + root + ", or choose the nginx method."
				if level == "warning" {
					check.Title = name + " redirects the challenge"
					check.Advice = "Let's Encrypt follows the redirect; the probe did not."
				}
				break
			}
		}
		checks = append(checks, check)
	}
	return checks
}

func probeChallenge(ctx context.Context, client *http.Client, endpoint, name, token string) (string, string) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+acmeChallengePath+token, nil)
	if err != nil {
		return err.Error(), "critical"
	}
	request.Host = name
	response, err := client.Do(request)
	if err != nil {
		return fmt.Sprintf("%s did not answer: %v", endpoint, err), "critical"
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
	switch {
	case response.StatusCode >= 300 && response.StatusCode < 400:
		return fmt.Sprintf("%s answered HTTP %d to %s.", endpoint, response.StatusCode, response.Header.Get("Location")), "warning"
	case response.StatusCode != http.StatusOK:
		return fmt.Sprintf("%s answered HTTP %d for the test file.", endpoint, response.StatusCode), "critical"
	case string(body) != token:
		return fmt.Sprintf("%s answered, but not with the test file: the name is served from another folder.", endpoint), "critical"
	}
	return "", ""
}

// lineageRelation says what certbot does with the lineages it has when
// asked for these names, the way certbot decides it: --cert-name first,
// then a lineage with exactly these names, then one whose names these hold.
func lineageRelation(confs []renewalConf, domains []string, certName string) (LineageRelation, PreflightCheck) {
	check := PreflightCheck{ID: "lineage", Check: "lineage"}
	want := map[string]bool{}
	for _, d := range domains {
		want[strings.ToLower(d)] = true
	}
	type lineage struct {
		name  string
		names []string
	}
	var lineages []lineage
	for _, conf := range confs {
		leaf, err := conf.leaf()
		if err != nil {
			continue
		}
		lineages = append(lineages, lineage{conf.Name, certNames(leaf)})
	}
	diff := func(names []string) (added, removed []string) {
		have := map[string]bool{}
		for _, n := range names {
			have[n] = true
			if !want[n] {
				removed = append(removed, n)
			}
		}
		for _, d := range domains {
			if !have[strings.ToLower(d)] {
				added = append(added, strings.ToLower(d))
			}
		}
		return added, removed
	}
	if certName != "" {
		for _, l := range lineages {
			if l.name != certName {
				continue
			}
			added, removed := diff(l.names)
			relation := LineageRelation{Kind: "replaces", Lineage: l.name, Added: added, Removed: removed}
			check.Level = "notice"
			check.Title = "These names become " + l.name + "'s list"
			check.Detail = "certbot issues " + l.name + " again with exactly these names."
			if len(removed) > 0 {
				check.Level = "warning"
				check.Detail += " It stops covering " + strings.Join(removed, ", ") + "."
			}
			if len(added) == 0 && len(removed) == 0 {
				relation.Kind = "identical"
				check.Title = l.name + " already has these names"
				check.Detail = "certbot keeps it until it is due for renewal; nothing new is issued unless its key changes."
			}
			return relation, check
		}
		check.Level = "ok"
		check.Title = "A new certificate named " + certName
		check.Detail = "No lineage has this name yet."
		return LineageRelation{Kind: "new", Lineage: certName}, check
	}
	for _, l := range lineages {
		if sameNames(l.names, domains) {
			check.Level = "notice"
			check.Title = l.name + " already has these names"
			check.Detail = "certbot keeps it until it is due for renewal; nothing new is issued unless its key changes or it is a test certificate."
			return LineageRelation{Kind: "identical", Lineage: l.name}, check
		}
	}
	for _, l := range lineages {
		added, removed := diff(l.names)
		if len(removed) == 0 && len(added) > 0 {
			check.Level = "critical"
			check.Title = "certbot would ask to expand " + l.name
			check.Detail = l.name + " holds a part of these names. Without --expand certbot stops to ask, and a run that cannot ask fails."
			check.Advice = "Choose " + l.name + " as the certificate, so these names become its list."
			return LineageRelation{Kind: "expands", Lineage: l.name, Added: added}, check
		}
	}
	check.Level = "ok"
	check.Title = "A new certificate"
	check.Detail = "No lineage has these names or a part of them."
	for _, l := range lineages {
		for _, n := range l.names {
			if want[n] {
				check.Level = "notice"
				check.Detail = l.name + " also covers " + n + "; certbot issues a separate certificate beside it."
				return LineageRelation{Kind: "new"}, check
			}
		}
	}
	return LineageRelation{Kind: "new"}, check
}

// rateChecks turns the meter into findings for a real run: a limit reached
// is a run the authority refuses. A test run counts against the staging
// authority's limits only, and certbot issues nothing for a lineage with
// these names that is not due.
func rateChecks(limits RateLimits, relation LineageRelation, staging bool) []PreflightCheck {
	if staging || !limits.Applies {
		return nil
	}
	var checks []PreflightCheck
	level := func(used, limit int) string {
		switch {
		case used >= limit:
			return "critical"
		case used*5 >= limit*4:
			return "warning"
		}
		return ""
	}
	frees := func(c RateCount) string {
		if c.Frees == nil {
			return ""
		}
		return " A slot frees at " + c.Frees.UTC().Format("2006-01-02 15:04 UTC") + "."
	}
	if l := level(limits.Duplicates.Used, limits.Duplicates.Limit); l != "" && relation.Kind != "identical" {
		checks = append(checks, PreflightCheck{ID: "rate:duplicates", Check: "rate", Level: l,
			Title:  fmt.Sprintf("%d of %d certificates for these names this week", limits.Duplicates.Used, limits.Duplicates.Limit),
			Detail: "Let's Encrypt signs at most five certificates for the same set of names in seven days." + frees(limits.Duplicates),
			Advice: "Add or remove a name to make it a different set, or wait."})
	}
	for _, r := range limits.Registered {
		if l := level(r.Used, r.Limit); l != "" {
			checks = append(checks, PreflightCheck{ID: "rate:registered:" + r.Domain, Name: r.Domain, Check: "rate", Level: l,
				Title:  fmt.Sprintf("%d of %d certificates for %s this week", r.Used, r.Limit, r.Domain),
				Detail: "Let's Encrypt limits new certificates per registered domain in seven days, counted here from this host only." + frees(r.RateCount)})
		}
	}
	for _, f := range limits.Failures {
		if l := level(f.Used, f.Limit); l != "" {
			checks = append(checks, PreflightCheck{ID: "rate:failures:" + f.Domain, Name: f.Domain, Check: "rate", Level: l,
				Title:  fmt.Sprintf("%d of %d failed validations for %s this hour", f.Used, f.Limit, f.Domain),
				Detail: "Counted from the real issuance runs this dashboard started." + frees(f.RateCount),
				Advice: "Get a test run to pass first."})
		}
	}
	return checks
}
