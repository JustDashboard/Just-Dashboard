package netsec

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Mail-path, STARTTLS and blocklist checks reported stage by stage, so a
// failed lookup is never read as an absent record and a blocked port 25 is
// never read as a dead exchanger.

// mailDeps are the resolver and dialer the mail checks use; tests replace
// them with fixtures.
type mailDeps struct {
	lookupMX  func(context.Context, string) ([]*net.MX, error)
	lookupTXT func(context.Context, string) ([]string, error)
	lookupIP  func(context.Context, string) ([]netip.Addr, error)
	dial      func(context.Context, string) (net.Conn, error)
}

var mailLookups = mailDeps{
	lookupMX:  func(ctx context.Context, name string) ([]*net.MX, error) { return lookupResolver.LookupMX(ctx, name) },
	lookupTXT: func(ctx context.Context, name string) ([]string, error) { return lookupResolver.LookupTXT(ctx, name) },
	lookupIP: func(ctx context.Context, host string) ([]netip.Addr, error) {
		return lookupResolver.LookupNetIP(ctx, "ip", host)
	},
	dial: func(ctx context.Context, address string) (net.Conn, error) {
		d := &net.Dialer{Timeout: 6 * time.Second}
		return d.DialContext(ctx, "tcp", address)
	},
}

// MXCheck follows the mail path: exchangers, their addresses, SPF, DMARC and
// one SMTP connection to the preferred exchanger. Each stage reports what it
// saw; deliverability is not claimed.
func (s *Service) MXCheck(ctx context.Context, target string) (*ProbeResult, error) {
	if !ValidTarget(target) {
		return nil, fmt.Errorf("target must be a hostname or IP address")
	}
	if net.ParseIP(strings.TrimSpace(target)) != nil {
		return nil, fmt.Errorf("a mail check takes a domain, not an IP address")
	}
	domain := strings.TrimSuffix(strings.TrimSpace(target), ".")
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	start := time.Now()
	res := &ProbeResult{Tool: "mx", Target: strings.TrimSpace(target), Records: []string{}}
	checkMailPath(ctx, res, domain, mailLookups)
	res.Duration = time.Since(start).Round(time.Millisecond).String()
	return res, nil
}

func checkMailPath(ctx context.Context, res *ProbeResult, domain string, deps mailDeps) {
	var b strings.Builder
	res.fact("Evidence", "DNS answers from this host's resolver and one TCP connection from this host", BasisConfigured)
	res.fact("Not checked", "DKIM (needs a selector), reverse DNS of the exchangers, blocklists, TLS on port 25 and actual delivery", BasisConfigured)
	res.Limitations = append(res.Limitations, "Passing these stages does not show that mail is delivered or accepted; receivers also weigh DKIM, reputation and content.")
	failed := false
	unknown := false

	clock := res.begin()
	mx, err := deps.lookupMX(ctx, domain)
	type exchanger struct {
		host  string
		pref  uint16
		addrs []string
	}
	var exchangers []exchanger
	nullMX := false
	switch {
	case err != nil && !isDNSNotFound(err):
		clock.done("mx", "MX records", StageFailed, "lookup failed: "+shortDialError(err))
		res.Error = "MX lookup failed: " + err.Error()
		failed = true
	case len(mx) == 0:
		clock.done("mx", "MX records", StageWarning, "none published; senders fall back to the domain's own address (implicit MX, RFC 5321)")
		res.finding("no-mx", "warning", "No MX records", domain+" publishes no MX records, so senders try the domain's own A/AAAA address. Publish MX records if this domain receives mail.", "DNS for "+domain)
		exchangers = append(exchangers, exchanger{host: domain})
	default:
		var hosts []string
		for _, m := range mx {
			host := strings.TrimSuffix(m.Host, ".")
			if host == "" {
				nullMX = true
				continue
			}
			exchangers = append(exchangers, exchanger{host: host, pref: m.Pref})
			hosts = append(hosts, fmt.Sprintf("%d %s", m.Pref, host))
			res.Records = append(res.Records, host)
		}
		if nullMX && len(exchangers) == 0 {
			clock.done("mx", "MX records", StagePassed, "null MX: the domain declares that it accepts no mail (RFC 7505)")
			res.Records = append(res.Records, "null MX")
		} else {
			clock.done("mx", "MX records", StagePassed, strings.Join(hosts, ", "))
		}
	}
	sort.SliceStable(exchangers, func(i, j int) bool { return exchangers[i].pref < exchangers[j].pref })
	for _, e := range exchangers {
		fmt.Fprintf(&b, "MX %d  %s\n", e.pref, e.host)
	}

	table := ProbeTable{ID: "exchangers", Title: "Exchangers", Columns: []string{"Preference", "Exchanger", "Addresses"}}
	if len(exchangers) > 0 && !failed {
		clock = res.begin()
		resolving := 0
		for i := range exchangers {
			addrs, err := deps.lookupIP(ctx, exchangers[i].host)
			for _, a := range addrs {
				exchangers[i].addrs = append(exchangers[i].addrs, a.Unmap().String())
			}
			cell := strings.Join(exchangers[i].addrs, ", ")
			if err != nil || len(addrs) == 0 {
				cell = "does not resolve"
				res.finding("mx-unresolved-"+exchangers[i].host, "warning", exchangers[i].host+" does not resolve", "Mail to this exchanger cannot be delivered; senders move to the next preference.", "DNS for "+exchangers[i].host)
			} else {
				resolving++
			}
			fmt.Fprintf(&b, "        %s\n", cell)
			table.Rows = append(table.Rows, []string{strconv.Itoa(int(exchangers[i].pref)), exchangers[i].host, cell})
		}
		res.Tables = append(res.Tables, table)
		switch {
		case resolving == 0:
			clock.done("addresses", "Exchanger addresses", StageFailed, "no exchanger resolves")
			failed = true
		case resolving < len(exchangers):
			clock.done("addresses", "Exchanger addresses", StageWarning, fmt.Sprintf("%d of %d exchangers resolve", resolving, len(exchangers)))
		default:
			clock.done("addresses", "Exchanger addresses", StagePassed, fmt.Sprintf("%d of %d exchangers resolve", resolving, len(exchangers)))
		}
	} else if !failed {
		res.skipRemaining([][2]string{{"addresses", "Exchanger addresses"}}, "no exchanger to resolve")
	}

	// SPF and DMARC are policy records: read even when the MX path failed.
	clock = res.begin()
	txts, err := deps.lookupTXT(ctx, domain)
	var spf []string
	for _, t := range txts {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(t)), "v=spf1") {
			spf = append(spf, t)
		}
	}
	switch {
	case err != nil && !isDNSNotFound(err):
		clock.done("spf", "SPF", StageUnknown, "TXT lookup failed: "+shortDialError(err))
		b.WriteString("\nSPF: unknown (lookup failed)\n")
		unknown = true
	case len(spf) == 0:
		clock.done("spf", "SPF", StageWarning, "none published")
		b.WriteString("\nSPF: none published\n")
		res.finding("spf-none", "warning", "No SPF record", "Receivers cannot use SPF to check which servers may send as "+domain+". DKIM and DMARC alignment were not checked here.", "DNS for "+domain)
	case len(spf) > 1:
		clock.done("spf", "SPF", StageWarning, fmt.Sprintf("%d SPF records; receivers treat this as a permanent error", len(spf)))
		fmt.Fprintf(&b, "\nSPF: %d records\n", len(spf))
		res.finding("spf-multiple", "warning", "More than one SPF record", "RFC 7208 makes several v=spf1 records a permanent error; merge them into one.", "DNS for "+domain)
	default:
		clock.done("spf", "SPF", StagePassed, spf[0])
		fmt.Fprintf(&b, "\nSPF: %s\n", spf[0])
		res.Records = append(res.Records, "spf "+spf[0])
		if strings.Contains(strings.ToLower(spf[0]), "+all") {
			res.finding("spf-pass-all", "warning", "SPF authorises every sender", "The record ends in +all, which lets any server pass SPF for this domain.", "DNS for "+domain)
		}
	}
	clock = res.begin()
	dmarcTXT, err := deps.lookupTXT(ctx, "_dmarc."+domain)
	dmarc := ""
	for _, t := range dmarcTXT {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(t)), "v=dmarc1") {
			dmarc = t
		}
	}
	switch {
	case err != nil && !isDNSNotFound(err):
		clock.done("dmarc", "DMARC", StageUnknown, "lookup failed: "+shortDialError(err))
		b.WriteString("DMARC: unknown (lookup failed)\n")
		unknown = true
	case dmarc == "":
		clock.done("dmarc", "DMARC", StageWarning, "none published")
		b.WriteString("DMARC: none published\n")
		res.finding("dmarc-none", "notice", "No DMARC policy", "Receivers get no instruction for mail that fails SPF/DKIM alignment and send no reports.", "DNS for "+domain)
	default:
		policy := dmarcTag(dmarc, "p")
		clock.done("dmarc", "DMARC", StagePassed, dmarc)
		fmt.Fprintf(&b, "DMARC: %s\n", dmarc)
		res.Records = append(res.Records, "dmarc p="+policy)
		if policy == "none" {
			res.finding("dmarc-monitor", "notice", "DMARC is monitoring only (p=none)", "Failing mail is reported but not rejected or quarantined.", "DNS for "+domain)
		}
	}

	// One SMTP connection to the preferred exchanger that resolves.
	var best *exchanger
	for i := range exchangers {
		if len(exchangers[i].addrs) > 0 {
			best = &exchangers[i]
			break
		}
	}
	switch {
	case nullMX && len(exchangers) == 0:
		res.skipRemaining([][2]string{{"smtp_connect", "SMTP connect (port 25)"}, {"smtp_greeting", "SMTP greeting"}}, "null MX: no exchanger to contact")
	case best == nil:
		res.skipRemaining([][2]string{{"smtp_connect", "SMTP connect (port 25)"}, {"smtp_greeting", "SMTP greeting"}}, "no exchanger address")
	default:
		res.link("STARTTLS on "+best.host, toolsLink("starttls", best.host))
		res.link("Blocklists for "+best.addrs[0], toolsLink("dnsbl", best.addrs[0]))
		clock = res.begin()
		var conn net.Conn
		var dialErr error
		for _, addr := range best.addrs[:min(2, len(best.addrs))] {
			conn, dialErr = deps.dial(ctx, net.JoinHostPort(addr, "25"))
			if dialErr == nil {
				break
			}
		}
		if dialErr != nil {
			state, sentence := dialFailure(dialErr)
			status := StageFailed
			if state == "no reply" {
				status = StageUnknown
				sentence += " Many providers block outbound port 25 from servers, so this does not show the exchanger is down."
				unknown = true
			}
			clock.done("smtp_connect", "SMTP connect (port 25)", status, best.host+": "+sentence)
			fmt.Fprintf(&b, "\nSMTP on %s: %s\n", best.host, shortDialError(dialErr))
			res.skipRemaining([][2]string{{"smtp_greeting", "SMTP greeting"}}, "no connection")
			if status == StageFailed {
				res.finding("smtp-refused", "warning", best.host+" refused SMTP", "Port 25 answered with a refusal from this host. Senders would see the same if nothing listens there.", "Mail server operator")
			}
		} else {
			clock.done("smtp_connect", "SMTP connect (port 25)", StagePassed, best.host+" "+conn.RemoteAddr().String())
			clock = res.begin()
			_ = conn.SetReadDeadline(time.Now().Add(8 * time.Second))
			line, err := bufio.NewReader(conn).ReadString('\n')
			line = sanitizeBanner(line)
			switch {
			case err != nil && line == "":
				clock.done("smtp_greeting", "SMTP greeting", StageUnknown, "no greeting: "+shortDialError(err))
				unknown = true
			case strings.HasPrefix(line, "220"):
				clock.done("smtp_greeting", "SMTP greeting", StagePassed, line)
			default:
				clock.done("smtp_greeting", "SMTP greeting", StageWarning, line)
				res.finding("smtp-greeting", "warning", best.host+" did not greet with 220", "It answered "+line+"; a 421 or 554 greeting turns senders away.", "Mail server operator")
			}
			fmt.Fprintf(&b, "\nSMTP on %s: %s\n", best.host, nonEmptyOr(line, "connected, no greeting"))
			_, _ = conn.Write([]byte("QUIT\r\n"))
			conn.Close()
		}
	}
	res.Output = strings.TrimSpace(b.String())
	warnings := 0
	for _, f := range res.Findings {
		if f.Level != "notice" {
			warnings++
		}
	}
	switch {
	case failed:
		res.OK, res.Verdict = false, ProbeFailed
		res.Summary = "The mail path stops early; read the failed stage."
	case len(res.Findings) > 0:
		res.OK, res.Verdict = true, ProbeFindings
		res.Summary = fmt.Sprintf("%d finding(s) along the mail path; delivery itself was not tested.", len(res.Findings))
		if warnings == 0 && unknown {
			res.Verdict = ProbeUnknown
		}
	case unknown:
		res.OK, res.Verdict = true, ProbeUnknown
		res.Summary = "DNS records look complete; a stage could not be observed from this host."
	default:
		res.OK, res.Verdict = true, ProbeOK
		res.Summary = "Exchangers resolve, SPF and DMARC are published, and the preferred exchanger greets on port 25. Delivery itself was not tested."
	}
}

func dmarcTag(record, tag string) string {
	for _, part := range strings.Split(record, ";") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && strings.EqualFold(strings.TrimSpace(key), tag) {
			return strings.ToLower(strings.TrimSpace(value))
		}
	}
	return ""
}

// starttlsProtocols is the closed set the STARTTLS tool speaks: the greeting
// each service opens with, how it lists capabilities, the upgrade command and
// its positive answer. Ports 465/993/995 are implicit TLS and belong to the
// TLS tool.
var starttlsProtocols = map[string]struct {
	greeting, capability, capabilityEnd, advertised, upgrade, want string
}{
	"smtp": {"220", "EHLO just-dashboard", "250 ", "STARTTLS", "STARTTLS", "220"},
	"imap": {"* OK", "a000 CAPABILITY", "a000 ", "STARTTLS", "a001 STARTTLS", "a001 OK"},
	"pop3": {"+OK", "CAPA", ".", "STLS", "STLS", "+OK"},
	"ftp":  {"220", "FEAT", "211 ", "AUTH TLS", "AUTH TLS", "234"},
}

// starttlsDefaultPort fills in the port when only the protocol was named.
var starttlsDefaultPort = map[string]int{"smtp": 25, "imap": 143, "pop3": 110, "ftp": 21}

var starttlsDial = func(ctx context.Context, address string) (net.Conn, error) {
	d := &net.Dialer{Timeout: 8 * time.Second}
	return d.DialContext(ctx, "tcp", address)
}

// STARTTLSCheck negotiates encryption as a mail or FTP client would and
// names the protocol stage at which it stops: connect, greeting, capability
// listing, upgrade command, handshake or certificate trust.
func (s *Service) STARTTLSCheck(ctx context.Context, target string, port int, protocol string) (*ProbeResult, error) {
	if !ValidTarget(target) {
		return nil, fmt.Errorf("target must be a hostname or IP address")
	}
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	if protocol == "" {
		protocol = "smtp"
	}
	spec, ok := starttlsProtocols[protocol]
	if !ok {
		return nil, fmt.Errorf("protocol must be one of smtp, imap, pop3 or ftp")
	}
	if port == 0 {
		port = starttlsDefaultPort[protocol]
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("port must be between 1 and 65535")
	}
	if port == 465 || port == 993 || port == 995 {
		return nil, fmt.Errorf("port %d is implicit TLS — no upgrade happens there, use the TLS tool", port)
	}
	res := &ProbeResult{Tool: "starttls", Target: target + ":" + strconv.Itoa(port), Records: []string{}}
	var b strings.Builder
	fmt.Fprintf(&b, "%s on %d, upgrading with %q\n\n", strings.ToUpper(protocol), port, spec.upgrade)
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	began := time.Now()
	stages := [][2]string{{"connect", "TCP connect"}, {"greeting", "Greeting"}, {"capabilities", capabilityLabel(protocol)}, {"upgrade", "Upgrade command"}, {"handshake", "TLS handshake"}, {"certificate", "Certificate trust"}}
	fail := func(at int, detail string) (*ProbeResult, error) {
		res.Duration = time.Since(began).Round(time.Millisecond).String()
		res.Error = stages[at][1] + " failed: " + detail
		res.Verdict = ProbeFailed
		res.Summary = "Stopped at " + strings.ToLower(stages[at][1]) + ": " + detail
		res.skipRemaining(stages[at+1:], "not reached")
		res.Output = strings.TrimSpace(b.String())
		return res, nil
	}

	clock := res.begin()
	raw, err := starttlsDial(ctx, net.JoinHostPort(target, strconv.Itoa(port)))
	if err != nil {
		_, sentence := dialFailure(err)
		clock.done("connect", "TCP connect", StageFailed, sentence)
		b.WriteString(describeDialError(err))
		return fail(0, sentence)
	}
	defer raw.Close()
	clock.done("connect", "TCP connect", StagePassed, raw.RemoteAddr().String())
	rd := bufio.NewReader(raw)
	readLine := func() (string, error) {
		_ = raw.SetReadDeadline(time.Now().Add(8 * time.Second))
		line, err := rd.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		if line != "" {
			fmt.Fprintf(&b, "< %s\n", sanitizeBanner(line))
		}
		return line, err
	}
	send := func(command string) error {
		fmt.Fprintf(&b, "> %s\n", command)
		_ = raw.SetWriteDeadline(time.Now().Add(8 * time.Second))
		_, err := fmt.Fprintf(raw, "%s\r\n", command)
		return err
	}

	clock = res.begin()
	greeting, err := readLine()
	for err == nil && protocol != "imap" && protocol != "pop3" && len(greeting) > 3 && greeting[3] == '-' {
		// Multi-line 220- greetings end at the line with a space after the code.
		greeting, err = readLine()
	}
	if err != nil && greeting == "" {
		clock.done("greeting", "Greeting", StageFailed, "no greeting: "+shortDialError(err))
		return fail(1, "the service accepted the connection but sent no greeting")
	}
	if !strings.HasPrefix(greeting, spec.greeting) {
		detail := fmt.Sprintf("expected %q, got %q", spec.greeting, sanitizeBanner(greeting))
		clock.done("greeting", "Greeting", StageFailed, detail)
		return fail(1, detail)
	}
	clock.done("greeting", "Greeting", StagePassed, sanitizeBanner(greeting))

	clock = res.begin()
	advertised, capErr := listCapabilities(protocol, greeting, readLine, send)
	switch {
	case capErr != nil:
		clock.done("capabilities", capabilityLabel(protocol), StageWarning, capErr.Error()+"; trying the upgrade anyway")
	case !advertised:
		clock.done("capabilities", capabilityLabel(protocol), StageWarning, spec.advertised+" is not advertised; trying the upgrade anyway")
		res.finding("not-advertised", "notice", spec.advertised+" is not advertised", "Clients that follow the capability list will not attempt encryption here.", "Mail/FTP server operator")
	default:
		clock.done("capabilities", capabilityLabel(protocol), StagePassed, spec.advertised+" advertised")
	}

	clock = res.begin()
	if err := send(spec.upgrade); err != nil {
		clock.done("upgrade", "Upgrade command", StageFailed, shortDialError(err))
		return fail(3, shortDialError(err))
	}
	answer, err := readLine()
	if err != nil && answer == "" {
		clock.done("upgrade", "Upgrade command", StageFailed, "no answer: "+shortDialError(err))
		return fail(3, "no answer to the upgrade command")
	}
	if !strings.HasPrefix(answer, spec.want) {
		detail := fmt.Sprintf("the server refused the upgrade (%q)", sanitizeBanner(answer))
		clock.done("upgrade", "Upgrade command", StageFailed, detail)
		res.finding("plaintext", "warning", "No encryption on this service", "Credentials sent here travel unencrypted.", "Mail/FTP server operator")
		b.WriteString("\nPlaintext only — credentials sent here travel unencrypted.")
		return fail(3, detail)
	}
	clock.done("upgrade", "Upgrade command", StagePassed, sanitizeBanner(answer))

	clock = res.begin()
	tlsConn := tls.Client(raw, &tls.Config{InsecureSkipVerify: true, ServerName: target})
	_ = tlsConn.SetDeadline(time.Now().Add(8 * time.Second))
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		result, detail := classifyHandshake(err)
		clock.done("handshake", "TLS handshake", StageFailed, result+": "+detail)
		return fail(4, result+": "+detail)
	}
	state := tlsConn.ConnectionState()
	clock.done("handshake", "TLS handshake", StagePassed, tlsVersionName(state.Version)+", "+tls.CipherSuiteName(state.CipherSuite))

	clock = res.begin()
	certText := describeTLSState(res, target, state)
	certStatus := StagePassed
	for _, f := range res.Findings {
		if f.ID == "untrusted" || f.ID == "expired" {
			certStatus = StageWarning
		}
	}
	trust := ""
	for _, f := range res.Facts {
		if f.Label == "Trust" {
			trust = f.Value
		}
	}
	clock.done("certificate", "Certificate trust", certStatus, trust)
	res.Duration = time.Since(began).Round(time.Millisecond).String()
	res.OK = true
	b.WriteString("\nUpgrade accepted, " + tlsVersionName(state.Version) + ", " + tls.CipherSuiteName(state.CipherSuite) + "\n\n" + certText)
	res.Output = strings.TrimSpace(b.String())
	res.Summary = "The upgrade succeeded (" + tlsVersionName(state.Version) + "); " + strings.ToLower(trust[:1]) + trust[1:]
	res.Verdict = ProbeOK
	if len(res.Findings) > 0 {
		res.Verdict = ProbeFindings
	}
	_ = tlsConn.Close()
	return res, nil
}

func capabilityLabel(protocol string) string {
	switch protocol {
	case "smtp":
		return "EHLO capabilities"
	case "imap":
		return "IMAP CAPABILITY"
	case "pop3":
		return "POP3 CAPA"
	}
	return "FTP FEAT"
}

// listCapabilities asks for the protocol's capability list and reports
// whether the upgrade is advertised. An unsupported listing is an error the
// caller reports as a warning; the upgrade is still attempted.
func listCapabilities(protocol, greeting string, readLine func() (string, error), send func(string) error) (bool, error) {
	spec := starttlsProtocols[protocol]
	if protocol == "imap" && strings.Contains(strings.ToUpper(greeting), " STARTTLS") && strings.Contains(strings.ToUpper(greeting), "[CAPABILITY") {
		return true, nil
	}
	if err := send(spec.capability); err != nil {
		return false, err
	}
	advertised := false
	for i := 0; i < 64; i++ {
		line, err := readLine()
		if err != nil {
			return advertised, fmt.Errorf("capability listing ended early: %s", shortDialError(err))
		}
		upper := strings.ToUpper(line)
		switch protocol {
		case "smtp":
			if !strings.HasPrefix(line, "250") {
				return false, fmt.Errorf("EHLO was answered %q", sanitizeBanner(line))
			}
			if len(upper) > 4 && strings.TrimSpace(upper[4:]) == "STARTTLS" {
				advertised = true
			}
		case "imap":
			if strings.HasPrefix(upper, "* CAPABILITY") && strings.Contains(upper+" ", " STARTTLS ") {
				advertised = true
			}
			if strings.HasPrefix(upper, "A000 ") && !strings.HasPrefix(upper, "A000 OK") {
				return false, fmt.Errorf("CAPABILITY was answered %q", sanitizeBanner(line))
			}
		case "pop3":
			if i == 0 && !strings.HasPrefix(line, "+OK") {
				return false, errors.New("the server does not support CAPA")
			}
			if strings.TrimSpace(upper) == "STLS" {
				advertised = true
			}
		case "ftp":
			if i == 0 && !strings.HasPrefix(line, "211") {
				return false, fmt.Errorf("FEAT was answered %q", sanitizeBanner(line))
			}
			if strings.Contains(upper, "AUTH") && strings.Contains(upper, "TLS") {
				advertised = true
			}
		}
		if strings.HasPrefix(upper, strings.ToUpper(spec.capabilityEnd)) || (protocol == "pop3" && line == ".") || (protocol == "ftp" && strings.HasPrefix(line, "211 ")) {
			return advertised, nil
		}
	}
	return advertised, errors.New("the capability listing was too long")
}

// dnsblZones is the set of blocklists consulted. A listing on any of these is
// what blocks mail; fifty serial lookups would only hold the request open.
// SORBS is absent: it stopped operating in 2024 and its zone no longer
// answers meaningfully.
var dnsblZones = []string{
	"zen.spamhaus.org",
	"bl.spamcop.net",
	"b.barracudacentral.org",
	"psbl.surriel.com",
}

var (
	dnsblLookup = func(ctx context.Context, name string) ([]netip.Addr, error) {
		return lookupResolver.LookupNetIP(ctx, "ip4", name)
	}
	dnsblTXT = func(ctx context.Context, name string) ([]string, error) { return lookupResolver.LookupTXT(ctx, name) }
	dnsblNow = func() time.Time { return time.Now().UTC() }
)

var spamhausCodes = map[string]string{
	"127.0.0.2": "SBL: spam source", "127.0.0.3": "SBL CSS: snowshoe spam", "127.0.0.4": "XBL: exploited host",
	"127.0.0.5": "XBL: exploited host", "127.0.0.6": "XBL: exploited host", "127.0.0.7": "XBL: exploited host",
	"127.0.0.9": "DROP: hijacked netblock", "127.0.0.10": "PBL: ISP-designated end-user range", "127.0.0.11": "PBL: end-user range",
}

// dnsblAnswer classifies one list's answer. Lists answer 127.255.255.x when
// they refuse the query (Spamhaus does for public resolvers); that is an
// unanswered check, never a listing.
func dnsblAnswer(zone string, addrs []netip.Addr, err error) (result, code, detail string) {
	if err != nil {
		if isDNSNotFound(err) {
			return "not listed", "", ""
		}
		return "query failed", "", shortDialError(err)
	}
	var codes []string
	refused, unexpected := false, false
	for _, a := range addrs {
		codes = append(codes, a.String())
		b := a.As4()
		switch {
		case !a.Is4() || b[0] != 127:
			unexpected = true
		case b[1] == 255 && b[2] == 255:
			refused = true
		}
	}
	code = strings.Join(codes, ", ")
	switch {
	case unexpected:
		return "unexpected answer", code, "an address outside 127.0.0.0/8; the resolver may be rewriting missing names"
	case refused:
		reason := "the list refused the query"
		if strings.HasSuffix(zone, "spamhaus.org") {
			switch code {
			case "127.255.255.254":
				reason = "Spamhaus refuses queries arriving through public or open resolvers"
			case "127.255.255.255":
				reason = "Spamhaus reports too many queries from this resolver"
			case "127.255.255.252":
				reason = "Spamhaus reports a malformed query"
			}
		}
		return "query refused", code, reason
	}
	meaning := []string{}
	if strings.HasSuffix(zone, "spamhaus.org") {
		for _, c := range codes {
			if m, ok := spamhausCodes[c]; ok {
				meaning = append(meaning, m)
			}
		}
	}
	return "listed", code, strings.Join(meaning, "; ")
}

// DNSBLCheck asks the blocklists whether an address is listed. Each list's
// answer is kept with the time it was asked, and a list that could not be
// asked is reported as such rather than as clean.
func (s *Service) DNSBLCheck(ctx context.Context, target string) (*ProbeResult, error) {
	ip := net.ParseIP(strings.TrimSpace(target))
	if ip == nil {
		return nil, fmt.Errorf("a blocklist check takes an IP address, not a name")
	}
	res := &ProbeResult{Tool: "dnsbl", Target: ip.String(), Records: []string{}}
	reversed := reverseForDNSBL(ip)
	began := time.Now()
	var b strings.Builder
	table := ProbeTable{ID: "lists", Title: "Blocklists", Columns: []string{"List", "Result", "Return code", "Detail", "Checked at", "Time"}}
	counts := map[string]int{}
	for _, zone := range dnsblZones {
		q := reversed + "." + zone
		asked := dnsblNow()
		zctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		start := time.Now()
		addrs, err := dnsblLookup(zctx, q)
		took := time.Since(start)
		cancel()
		result, code, detail := dnsblAnswer(zone, addrs, err)
		if result == "listed" {
			tctx, tcancel := context.WithTimeout(ctx, 3*time.Second)
			if txt, terr := dnsblTXT(tctx, q); terr == nil && len(txt) > 0 {
				detail = strings.TrimSpace(strings.TrimPrefix(detail+"; "+strings.Join(txt, " "), "; "))
			}
			tcancel()
			res.Records = append(res.Records, zone)
			res.finding("listed-"+zone, "warning", "Listed on "+zone, nonEmptyOr(detail, "return code "+code)+". Receivers using this list reject or score mail from "+ip.String()+".", "Operator of "+ip.String())
		}
		counts[result]++
		table.Rows = append(table.Rows, []string{zone, result, code, detail, asked.Format(time.RFC3339), took.Round(time.Millisecond).String()})
		fmt.Fprintf(&b, "%-18s %-28s %s %s\n", result, zone, code, detail)
	}
	res.Tables = append(res.Tables, table)
	res.Duration = time.Since(began).Round(time.Millisecond).String()
	res.fact("Checked at", began.UTC().Format(time.RFC3339), BasisObserved)
	res.fact("Resolver", "this host's resolver; some lists refuse queries that arrive through public resolvers", BasisConfigured)
	if ip.To4() == nil {
		res.fact("Address family", "IPv6: not every list publishes IPv6 listings, so not listed is weaker evidence", BasisInferred)
	}
	unanswered := counts["query failed"] + counts["query refused"] + counts["unexpected answer"]
	res.metric("listed", "Lists with a listing", float64(counts["listed"]), "")
	res.metric("not_listed", "Lists without a listing", float64(counts["not listed"]), "")
	res.metric("unanswered", "Lists not answered", float64(unanswered), "")
	res.Limitations = append(res.Limitations, "A listing is the list's own claim at the time asked; delisting and relisting happen within hours.")
	res.OK = counts["listed"]+counts["not listed"] > 0
	switch {
	case counts["listed"] > 0:
		res.Verdict = ProbeFindings
		res.Summary = fmt.Sprintf("Listed on %d of %d lists", counts["listed"], len(dnsblZones))
		fmt.Fprintf(&b, "\nListed on %d of %d checked blocklists — outbound mail from %s will bounce in places.", counts["listed"], len(dnsblZones), ip.String())
	case unanswered > 0:
		res.Verdict = ProbeUnknown
		res.Summary = fmt.Sprintf("Not listed on %d list(s)", counts["not listed"])
		b.WriteString("\nNo listing found, but some lists did not answer.")
	default:
		res.Verdict = ProbeOK
		res.Summary = fmt.Sprintf("Not listed on any of the %d lists", len(dnsblZones))
		b.WriteString("\nNot listed on any checked blocklist.")
	}
	if unanswered > 0 {
		res.Summary += fmt.Sprintf("; %d could not be checked", unanswered)
	}
	res.Summary += "."
	res.Output = strings.TrimSpace(b.String())
	return res, nil
}
