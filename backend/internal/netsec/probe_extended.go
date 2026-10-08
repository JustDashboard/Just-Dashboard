package netsec

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// The big-boy half of the tools page: service banners, SSH keys, STARTTLS,
// offered TLS versions, mail deliverability, blocklists, ASN ownership and the
// host's own sockets.
//
// They obey the same rules as the diagnostics next to them — read-only, a
// validated target, argv never a shell, behind system.admin because they make
// the server send traffic to an address the caller chose. The ones that shell
// out fail soft when the binary is missing, the way traceroute does, so a
// missing optional tool is a sentence rather than a red toast.

// DNSAuthority answers "who is authoritative for this name, and where are
// they": the NS set plus each nameserver's own addresses, which is the half
// of "is this domain pointing at me yet" a plain A lookup cannot show.
//
// No subprocess: the Go resolver speaks to the host's own resolver, which is
// the point — what this machine sees is what its services will see.
func (s *Service) DNSAuthority(ctx context.Context, target string) (*ProbeResult, error) {
	if !ValidTarget(target) {
		return nil, fmt.Errorf("target must be a hostname or IP address")
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	start := time.Now()
	res := &ProbeResult{Tool: "dnsauth", Target: target, Records: []string{}}

	ns, err := net.DefaultResolver.LookupNS(ctx, target)
	if err != nil {
		res.Duration = time.Since(start).Round(time.Millisecond).String()
		res.Error = err.Error()
		res.Output = "The name did not resolve to a nameserver set."
		return res, nil
	}
	var b strings.Builder
	for _, n := range ns {
		host := strings.TrimSuffix(n.Host, ".")
		fmt.Fprintf(&b, "NS  %s\n", host)
		res.Records = append(res.Records, host)
		// Glue: where the nameserver itself lives, so a dead NS reads as an
		// address that answers nothing rather than as a name.
		addrs, aerr := net.DefaultResolver.LookupIP(ctx, "ip4", host)
		if aerr != nil {
			fmt.Fprintf(&b, "      (does not resolve: %v)\n", aerr)
			continue
		}
		for _, a := range addrs {
			fmt.Fprintf(&b, "      %s\n", a.String())
		}
	}
	res.Duration = time.Since(start).Round(time.Millisecond).String()
	res.OK = len(ns) > 0
	res.Output = strings.TrimSpace(b.String())
	return res, nil
}

// BannerGrab connects to a TCP port and reads whatever the service volunteers
// — SSH, SMTP and FTP announce themselves before a byte is sent, which makes
// this the version check that needs no scanner. Nothing is transmitted, so a
// service that waits for a request (HTTP most of all) answers with silence,
// and the output says so rather than timing out wordlessly.
func (s *Service) BannerGrab(ctx context.Context, target string, port int) (*ProbeResult, error) {
	if !ValidTarget(target) {
		return nil, fmt.Errorf("target must be a hostname or IP address")
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("port must be between 1 and 65535")
	}
	res := &ProbeResult{Tool: "banner", Target: target + ":" + strconv.Itoa(port)}
	start := time.Now()
	dialer := &net.Dialer{Timeout: 6 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(target, strconv.Itoa(port)))
	if err != nil {
		res.Duration = time.Since(start).Round(time.Millisecond).String()
		res.Error = err.Error()
		res.Output = describeDialError(err)
		return res, nil
	}
	defer conn.Close()
	// A service waiting for our request holds the connection open forever; the
	// deadline turns that into an answer instead of a hung request.
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 4096)
	n, rerr := conn.Read(buf)
	res.Duration = time.Since(start).Round(time.Millisecond).String()
	if n > 0 {
		out := sanitizeBanner(string(buf[:n]))
		if rerr == nil {
			// There was more than one read's worth; say so rather than
			// quoting a truncated banner as the whole thing.
			out += "\n… (truncated to 4 KB)"
		}
		res.OK = true
		res.Output = out
		return res, nil
	}
	if rerr != nil && !isTimeout(rerr) {
		res.Error = rerr.Error()
		res.Output = "Connected, then the connection closed without a banner."
		return res, nil
	}
	res.Error = "connected but the service sent nothing within 5 seconds"
	res.Output = "Connected, but the service sent no banner. It is waiting for a request first — " +
		"HTTP services behave this way, so try the HTTP tool against this port instead."
	return res, nil
}

// sanitizeBanner keeps a banner readable and the JSON honest: control bytes
// other than line breaks become dots rather than terminal escapes in output.
func sanitizeBanner(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f {
			return '.'
		}
		return r
	}, strings.TrimSpace(s))
}

func isTimeout(err error) bool {
	if nerr, ok := err.(net.Error); ok {
		return nerr.Timeout()
	}
	return strings.Contains(err.Error(), "timeout") || strings.Contains(err.Error(), "deadline")
}

// SSHScan reports the host keys a server offers — algorithms and SHA256
// fingerprints — which is the "did this host's key change" question answered
// without trusting the connection. It shells out because key exchange is not
// a thing to reimplement here, and fails soft where ssh-keyscan is absent.
func (s *Service) SSHScan(ctx context.Context, target string, port int) (*ProbeResult, error) {
	if !ValidTarget(target) {
		return nil, fmt.Errorf("target must be a hostname or IP address")
	}
	if port == 0 {
		port = 22
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("port must be between 1 and 65535")
	}
	res := &ProbeResult{Tool: "ssh", Target: target + ":" + strconv.Itoa(port), Records: []string{}}
	if !hostexec.AvailableOnHost("ssh-keyscan") {
		res.Error = "ssh-keyscan is not installed on this host"
		return res, nil
	}
	out, elapsed, err := runProbe(ctx, 20*time.Second, "ssh-keyscan",
		"-T", "5", "-p", strconv.Itoa(port), target)
	res.Duration = elapsed
	if err != nil {
		res.Error = err.Error()
		res.Output = strings.TrimSpace(out + "\n" + describeDialError(err))
		return res, nil
	}
	var b strings.Builder
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		keytype, keydata := fields[1], fields[2]
		fp := sshFingerprint(keydata)
		fmt.Fprintf(&b, "%s  %s\n", keytype, fp)
		res.Records = append(res.Records, keytype+" "+fp)
	}
	res.OK = true
	if len(res.Records) == 0 {
		res.Error = "ssh-keyscan answered nothing usable"
		res.Output = out
		return res, nil
	}
	res.Output = strings.TrimSpace(b.String())
	return res, nil
}

// sshFingerprint renders a key blob the way `ssh-keygen -lf` does, so a value
// copied from a client config compares directly. An undecodable blob is shown
// raw rather than dropped — a partial answer beats silence about a key.
func sshFingerprint(keydata string) string {
	raw, err := base64.StdEncoding.DecodeString(keydata)
	if err != nil {
		return keydata
	}
	sum := sha256.Sum256(raw)
	return "SHA256:" + strings.TrimRight(base64.StdEncoding.EncodeToString(sum[:]), "=")
}

// starttlsProtocols is the closed set the STARTTLS tool speaks: the command
// that upgrades the connection and the greeting each service opens with.
// Ports 465/993/995 are deliberately absent — those are implicit TLS, which
// never upgrades and belongs to the TLS tool.
var starttlsProtocols = map[string]struct{ greeting, upgrade, want string }{
	"smtp": {"220", "STARTTLS", "220"},
	"imap": {"* OK", "a001 STARTTLS", "a001 OK"},
	"pop3": {"+OK", "STLS", "+OK"},
	"ftp":  {"220", "AUTH TLS", "234"},
}

// starttlsDefaultPort fills in the port when only the protocol was named.
var starttlsDefaultPort = map[string]int{"smtp": 25, "imap": 143, "pop3": 110, "ftp": 21}

// STARTTLSCheck negotiates encryption the way a mail client or FTP client
// would — greeting, upgrade command, then the TLS handshake — and reports the
// certificate the encrypted session presents. A certificate that is expired
// or self-signed is reported, not refused: the trust verdict is information.
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

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	began := time.Now()
	dialer := &net.Dialer{Timeout: 8 * time.Second}
	raw, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(target, strconv.Itoa(port)))
	if err != nil {
		res.Duration = time.Since(began).Round(time.Millisecond).String()
		res.Error = err.Error()
		res.Output = describeDialError(err)
		return res, nil
	}
	defer raw.Close()
	rd := bufio.NewReader(raw)
	readLine := func() (string, error) {
		raw.SetReadDeadline(time.Now().Add(8 * time.Second))
		line, err := rd.ReadString('\n')
		return strings.TrimSpace(line), err
	}
	greeting, err := readLine()
	if err != nil {
		res.Duration = time.Since(began).Round(time.Millisecond).String()
		res.Error = "no greeting: " + err.Error()
		res.Output = strings.TrimSpace(b.String() + "\nThe service accepted the connection but said nothing.")
		return res, nil
	}
	fmt.Fprintf(&b, "< %s\n", greeting)
	if !strings.HasPrefix(greeting, spec.greeting) {
		res.Duration = time.Since(began).Round(time.Millisecond).String()
		res.Error = fmt.Sprintf("unexpected greeting for %s (want %q)", protocol, spec.greeting)
		res.Output = strings.TrimSpace(b.String())
		return res, nil
	}
	// SMTP wants EHLO before STARTTLS on most servers; the others upgrade
	// straight away.
	if protocol == "smtp" {
		fmt.Fprintf(raw, "EHLO just-dashboard\r\n")
		for {
			line, err := readLine()
			if err != nil {
				break
			}
			fmt.Fprintf(&b, "< %s\n", line)
			if len(line) >= 4 && line[:4] == "250 " {
				break
			}
			if !strings.HasPrefix(line, "250") {
				break
			}
		}
	}
	fmt.Fprintf(&b, "> %s\n", spec.upgrade)
	fmt.Fprintf(raw, "%s\r\n", spec.upgrade)
	answer, err := readLine()
	if err != nil {
		res.Duration = time.Since(began).Round(time.Millisecond).String()
		res.Error = "no answer to the upgrade: " + err.Error()
		res.Output = strings.TrimSpace(b.String())
		return res, nil
	}
	fmt.Fprintf(&b, "< %s\n", answer)
	if !strings.HasPrefix(answer, spec.want) {
		res.Duration = time.Since(began).Round(time.Millisecond).String()
		res.Error = fmt.Sprintf("the server refused the upgrade (want %q)", spec.want)
		res.Output = strings.TrimSpace(b.String() + "\nPlaintext only — credentials sent here travel unencrypted.")
		return res, nil
	}
	tlsConn := tls.Client(raw, &tls.Config{InsecureSkipVerify: true, ServerName: target})
	tlsConn.SetDeadline(time.Now().Add(8 * time.Second))
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		res.Duration = time.Since(began).Round(time.Millisecond).String()
		res.Error = "TLS handshake after the upgrade failed: " + err.Error()
		res.Output = strings.TrimSpace(b.String())
		return res, nil
	}
	defer tlsConn.Close()
	state := tlsConn.ConnectionState()
	recs, certText := describePeerCerts(target, state)
	res.Records = recs
	res.Duration = time.Since(began).Round(time.Millisecond).String()
	res.OK = true
	b.WriteString("\nUpgrade accepted, " + tlsVersionName(state.Version) + ", " +
		tls.CipherSuiteName(state.CipherSuite) + "\n\n" + certText)
	res.Output = strings.TrimSpace(b.String())
	return res, nil
}

// describePeerCerts renders the presented chain — subject, issuer, names,
// validity and the explicit trust verdict — shared by the TLS and STARTTLS
// tools so the two never disagree about what "trusted" means.
func describePeerCerts(target string, state tls.ConnectionState) ([]string, string) {
	if len(state.PeerCertificates) == 0 {
		return nil, "the server presented no certificate"
	}
	leaf := state.PeerCertificates[0]
	recs := append([]string{}, leaf.DNSNames...)
	var b strings.Builder
	fmt.Fprintf(&b, "Subject:  %s\n", nameOrString(leaf.Subject.CommonName, leaf.Subject.String()))
	fmt.Fprintf(&b, "Issuer:   %s\n", nameOrString(leaf.Issuer.CommonName, leaf.Issuer.String()))
	if len(leaf.DNSNames) > 0 {
		fmt.Fprintf(&b, "Names:    %s\n", strings.Join(leaf.DNSNames, ", "))
	}
	fmt.Fprintf(&b, "Valid:    %s → %s\n", leaf.NotBefore.UTC().Format("2006-01-02"), leaf.NotAfter.UTC().Format("2006-01-02"))
	now := time.Now()
	switch {
	case now.After(leaf.NotAfter):
		fmt.Fprintf(&b, "Expired %d days ago.\n", int(now.Sub(leaf.NotAfter).Hours()/24))
	case now.Before(leaf.NotBefore):
		b.WriteString("Not valid yet.\n")
	default:
		fmt.Fprintf(&b, "Expires in %d days.\n", int(time.Until(leaf.NotAfter).Hours()/24))
	}
	roots, _ := x509.SystemCertPool()
	inter := x509.NewCertPool()
	for _, c := range state.PeerCertificates[1:] {
		inter.AddCert(c)
	}
	if _, verr := leaf.Verify(x509.VerifyOptions{DNSName: target, Roots: roots, Intermediates: inter}); verr != nil {
		fmt.Fprintf(&b, "Not trusted for %s: %v\n", target, verr)
	} else {
		fmt.Fprintf(&b, "Trusted for %s.\n", target)
	}
	return recs, strings.TrimSpace(b.String())
}

// TLSSurvey dials once per protocol version, each connection pinned to exactly
// that version, and reports which the server still speaks. TLS 1.0 offered in
// 2026 is the finding; the tool names it rather than grading it, because the
// version to keep is a decision about the clients, not a score.
func (s *Service) TLSSurvey(ctx context.Context, target string, port int) (*ProbeResult, error) {
	if !ValidTarget(target) {
		return nil, fmt.Errorf("target must be a hostname or IP address")
	}
	if port == 0 {
		port = 443
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("port must be between 1 and 65535")
	}
	res := &ProbeResult{Tool: "tlssurvey", Target: target + ":" + strconv.Itoa(port), Records: []string{}}
	versions := []uint16{tls.VersionTLS10, tls.VersionTLS11, tls.VersionTLS12, tls.VersionTLS13}
	began := time.Now()
	var b strings.Builder
	for _, v := range versions {
		name := tlsVersionName(v)
		dialCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		dialer := &tls.Dialer{Config: &tls.Config{
			InsecureSkipVerify: true, ServerName: target, MinVersion: v, MaxVersion: v,
		}}
		conn, err := dialer.DialContext(dialCtx, "tcp", net.JoinHostPort(target, strconv.Itoa(port)))
		cancel()
		if err != nil {
			fmt.Fprintf(&b, "%-8s refused   %s\n", name, shortDialError(err))
			continue
		}
		state := conn.(*tls.Conn).ConnectionState()
		conn.Close()
		fmt.Fprintf(&b, "%-8s offered   %s\n", name, tls.CipherSuiteName(state.CipherSuite))
		res.Records = append(res.Records, name)
	}
	res.Duration = time.Since(began).Round(time.Millisecond).String()
	// The survey running is the success: a server speaking nothing is itself
	// the answer, and painting it red would punish the messenger.
	res.OK = true
	if len(res.Records) == 0 {
		b.WriteString("\nNo TLS version answered — the port may not speak TLS at all.")
	}
	res.Output = strings.TrimSpace(b.String())
	return res, nil
}

// shortDialError keeps one survey line to one line: the full error is what the
// TLS tool is for.
func shortDialError(err error) string {
	msg := err.Error()
	if i := strings.Index(msg, ":"); i >= 0 && len(msg) > 60 {
		if tail := strings.TrimSpace(msg[i+1:]); tail != "" {
			return tail
		}
	}
	if len(msg) > 90 {
		return msg[:90] + "…"
	}
	return msg
}

// dnsblZones is the set of blocklists consulted. Five widely-used zones rather
// than fifty: a listing on any of these is what blocks mail, and fifty serial
// lookups would hold the request open past its timeout.
var dnsblZones = []string{
	"zen.spamhaus.org",
	"bl.spamcop.net",
	"b.barracudacentral.org",
	"dnsbl.sorbs.net",
	"psbl.surriel.com",
}

// DNSBLCheck asks the blocklists whether an address is known for spam. It
// takes an IP, never a name — a name would resolve differently per list and
// the answer would be about whichever address came back first.
func (s *Service) DNSBLCheck(ctx context.Context, target string) (*ProbeResult, error) {
	ip := net.ParseIP(strings.TrimSpace(target))
	if ip == nil {
		return nil, fmt.Errorf("a blocklist check takes an IP address, not a name")
	}
	res := &ProbeResult{Tool: "dnsbl", Target: ip.String(), Records: []string{}}
	reversed := reverseForDNSBL(ip)
	began := time.Now()
	var b strings.Builder
	for _, zone := range dnsblZones {
		q := reversed + "." + zone
		zctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		addrs, err := net.DefaultResolver.LookupIP(zctx, "ip4", q)
		cancel()
		switch {
		case err == nil && len(addrs) > 0:
			fmt.Fprintf(&b, "LISTED    %-28s %s\n", zone, addrs[0].String())
			res.Records = append(res.Records, zone)
		case err == nil:
			fmt.Fprintf(&b, "clean     %s\n", zone)
		case isDNSNotFound(err):
			fmt.Fprintf(&b, "clean     %s\n", zone)
		default:
			fmt.Fprintf(&b, "unchecked %s (%v)\n", zone, err)
		}
	}
	res.Duration = time.Since(began).Round(time.Millisecond).String()
	res.OK = true
	if len(res.Records) == 0 {
		b.WriteString("\nNot listed on any checked blocklist.")
	} else {
		fmt.Fprintf(&b, "\nListed on %d of %d checked blocklists — outbound mail from %s will bounce in places.",
			len(res.Records), len(dnsblZones), ip.String())
	}
	res.Output = strings.TrimSpace(b.String())
	return res, nil
}

// reverseForDNSBL renders an address in the reversed form blocklists query:
// dotted quads backwards for v4, one nibble per label backwards for v6.
func reverseForDNSBL(ip net.IP) string {
	if v4 := ip.To4(); v4 != nil {
		return fmt.Sprintf("%d.%d.%d.%d", v4[3], v4[2], v4[1], v4[0])
	}
	v16 := ip.To16()
	const hexd = "0123456789abcdef"
	var sb strings.Builder
	for i := len(v16) - 1; i >= 0; i-- {
		sb.WriteByte(hexd[v16[i]&0x0f])
		sb.WriteByte('.')
		sb.WriteByte(hexd[v16[i]>>4])
		sb.WriteByte('.')
	}
	return strings.TrimSuffix(sb.String(), ".")
}

// isDNSNotFound reports the resolver's "no such name": for a blocklist that
// is the clean answer, not a failure.
func isDNSNotFound(err error) bool {
	var derr *net.DNSError
	if errors.As(err, &derr) {
		return derr.IsNotFound
	}
	return strings.Contains(err.Error(), "no such host")
}

// ASNLookup reports who owns an address — autonomous system, prefix, country
// and registry — via Team Cymru's whois service, which answers in one query
// where the regional registries take a chase across three referrals.
func (s *Service) ASNLookup(ctx context.Context, target string) (*ProbeResult, error) {
	if net.ParseIP(strings.TrimSpace(target)) == nil {
		return nil, fmt.Errorf("an ownership lookup takes an IP address, not a name")
	}
	res := &ProbeResult{Tool: "asn", Target: strings.TrimSpace(target)}
	if !hostexec.AvailableOnHost("whois") {
		res.Error = "whois is not installed on this host"
		return res, nil
	}
	out, elapsed, err := runProbe(ctx, 20*time.Second, "whois", "-h", "whois.cymru.com", "-v", strings.TrimSpace(target))
	res.Duration = elapsed
	if err != nil {
		res.Error = err.Error()
		res.Output = strings.TrimSpace(out)
		return res, nil
	}
	var b strings.Builder
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		// The header row starts with "AS ", data rows with the number; the
		// Bulk-mode footer and separator lines are prose, not answers.
		if strings.HasPrefix(line, "AS ") || strings.HasPrefix(line, "---") ||
			strings.HasPrefix(line, "Bulk") || strings.HasPrefix(line, "AS Name") {
			continue
		}
		fields := strings.Split(line, "|")
		if len(fields) < 7 {
			continue
		}
		for i := range fields {
			fields[i] = strings.TrimSpace(fields[i])
		}
		fmt.Fprintf(&b, "AS%-9s %s\n", fields[0], fields[6])
		fmt.Fprintf(&b, "Prefix:   %s\n", fields[2])
		fmt.Fprintf(&b, "Country:  %s   Registry: %s\n", fields[3], fields[4])
		res.Records = append(res.Records, "AS"+fields[0]+" "+fields[6], fields[2])
	}
	res.OK = len(res.Records) > 0
	if !res.OK {
		res.Error = "whois.cymru.com returned nothing usable"
		res.Output = strings.TrimSpace(out)
		if len(res.Output) > maxProbeOutput {
			res.Output = res.Output[:maxProbeOutput] + "\n… (truncated)"
		}
		return res, nil
	}
	res.Output = strings.TrimSpace(b.String())
	return res, nil
}

// MXCheck follows the mail path end to end: which exchangers the domain
// names, whether they resolve, whether the domain publishes SPF and DMARC,
// and whether the best exchanger answers on SMTP. One tool because each step
// alone sends the operator back for the next.
func (s *Service) MXCheck(ctx context.Context, target string) (*ProbeResult, error) {
	if !ValidTarget(target) {
		return nil, fmt.Errorf("target must be a hostname or IP address")
	}
	if net.ParseIP(strings.TrimSpace(target)) != nil {
		return nil, fmt.Errorf("a mail check takes a domain, not an IP address")
	}
	target = strings.TrimSpace(target)
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	start := time.Now()
	res := &ProbeResult{Tool: "mx", Target: target, Records: []string{}}
	var b strings.Builder

	mx, err := net.DefaultResolver.LookupMX(ctx, target)
	if err != nil || len(mx) == 0 {
		res.Duration = time.Since(start).Round(time.Millisecond).String()
		res.Error = "no MX records"
		if err != nil {
			res.Error += ": " + err.Error()
		}
		res.Output = target + " publishes no MX records — inbound mail has nowhere to go."
		return res, nil
	}
	best := mx[0]
	for _, m := range mx {
		host := strings.TrimSuffix(m.Host, ".")
		fmt.Fprintf(&b, "MX %d  %s\n", m.Pref, host)
		res.Records = append(res.Records, host)
		if m.Pref < best.Pref {
			best = m
		}
		addrs, aerr := net.DefaultResolver.LookupIP(ctx, "ip4", host)
		if aerr != nil || len(addrs) == 0 {
			fmt.Fprintf(&b, "        (does not resolve — mail to this exchanger bounces)\n")
			continue
		}
		for _, a := range addrs {
			fmt.Fprintf(&b, "        %s\n", a.String())
		}
	}
	if txts, terr := net.DefaultResolver.LookupTXT(ctx, target); terr == nil {
		found := false
		for _, t := range txts {
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(t)), "v=spf1") {
				fmt.Fprintf(&b, "\nSPF: %s\n", t)
				found = true
			}
		}
		if !found {
			b.WriteString("\nSPF: none — receivers cannot tell your mail from forgery.\n")
		}
	}
	if txts, terr := net.DefaultResolver.LookupTXT(ctx, "_dmarc."+target); terr == nil {
		found := false
		for _, t := range txts {
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(t)), "v=dmarc1") {
				fmt.Fprintf(&b, "DMARC: %s\n", t)
				found = true
			}
		}
		if !found {
			b.WriteString("DMARC: none.\n")
		}
	} else {
		b.WriteString("DMARC: none published.\n")
	}
	bestHost := strings.TrimSuffix(best.Host, ".")
	dialer := &net.Dialer{Timeout: 6 * time.Second}
	conn, derr := dialer.DialContext(ctx, "tcp", net.JoinHostPort(bestHost, "25"))
	if derr != nil {
		fmt.Fprintf(&b, "\nSMTP on %s: %s\n", bestHost, shortDialError(derr))
	} else {
		conn.Close()
		fmt.Fprintf(&b, "\nSMTP on %s answers on port 25.\n", bestHost)
	}
	res.Duration = time.Since(start).Round(time.Millisecond).String()
	res.OK = true
	res.Output = strings.TrimSpace(b.String())
	return res, nil
}

// targetURL renders the URL a web tool starts from: HTTPS everywhere except
// port 80, the same rule HTTPCheck uses.
func targetURL(scheme, target string, port int) string {
	u := url.URL{Scheme: scheme, Host: net.JoinHostPort(target, strconv.Itoa(port)), Path: "/"}
	return u.String()
}

// fetchHTTPChain performs the request chain HTTPCheck reports on and hands
// back each hop plus the final response, so the HTTP and header-grading tools
// share one implementation of "what does this host serve".
func fetchHTTPChain(ctx context.Context, target string, port int) (chain string, hdr http.Header, code int, dur string, derr error) {
	scheme := "https"
	if port == 80 {
		scheme = "http"
	}
	start := targetURL(scheme, target, port)
	client := &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
			DisableKeepAlives: true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	began := time.Now()
	var b strings.Builder
	current := start
	for hop := 0; hop < 10; hop++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, current, nil)
		if err != nil {
			return "", nil, 0, sinceMs(began), err
		}
		req.Header.Set("User-Agent", "Just-Dashboard/probe")
		resp, err := client.Do(req)
		if err != nil {
			return strings.TrimSpace(b.String()), nil, 0, sinceMs(began), err
		}
		fmt.Fprintf(&b, "%s %s\n%s\n", req.Method, current, resp.Status)
		loc := resp.Header.Get("Location")
		if isRedirect(resp.StatusCode) && loc != "" {
			next, perr := resp.Request.URL.Parse(loc)
			resp.Body.Close()
			if perr != nil {
				return strings.TrimSpace(b.String()), nil, 0, sinceMs(began), perr
			}
			fmt.Fprintf(&b, "  → %s\n", next.String())
			current = next.String()
			continue
		}
		defer resp.Body.Close()
		return strings.TrimSpace(b.String()), resp.Header, resp.StatusCode, sinceMs(began), nil
	}
	return strings.TrimSpace(b.String()), nil, 0, sinceMs(began), fmt.Errorf("too many redirects")
}

func sinceMs(t time.Time) string {
	return time.Since(t).Round(time.Millisecond).String()
}

// HTTPSecurity grades the response headers that keep a browser honest. It is
// the HTTP check's stricter sibling: what the host serves is one question,
// whether it hardened the answer is another. Missing HSTS on HTTPS fails;
// the rest warn, because an API has less use for a framing policy than a page
// does, and a grader that cries fail at an API is ignored for the site too.
func (s *Service) HTTPSecurity(ctx context.Context, target string, port int) (*ProbeResult, error) {
	if !ValidTarget(target) {
		return nil, fmt.Errorf("target must be a hostname or IP address")
	}
	if port == 0 {
		port = 443
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("port must be between 1 and 65535")
	}
	res := &ProbeResult{Tool: "httpsec", Target: targetURL("https", target, port), Records: []string{}}
	chain, hdr, code, dur, derr := fetchHTTPChain(ctx, target, port)
	res.Duration = dur
	if derr != nil {
		res.Error = derr.Error()
		res.Output = strings.TrimSpace(chain + "\n" + describeDialError(derr))
		return res, nil
	}
	var b strings.Builder
	b.WriteString(chain + "\n\n")
	pass, graded := 0, 0
	grade := func(name string, ok bool, detail string) {
		graded++
		if ok {
			pass++
			fmt.Fprintf(&b, "PASS  %s\n", detail)
		} else {
			fmt.Fprintf(&b, "WARN  %s\n", detail)
			res.Records = append(res.Records, name)
		}
	}
	isHTTPS := port != 80
	if hsts := hdr.Get("Strict-Transport-Security"); !isHTTPS {
		b.WriteString("N/A   HSTS only applies to HTTPS — plain HTTP cannot set it.\n")
	} else if hsts == "" {
		graded++
		res.Records = append(res.Records, "Strict-Transport-Security")
		b.WriteString("FAIL  Strict-Transport-Security is missing — first visits stay downgradeable.\n")
	} else {
		graded++
		maxAge := parseMaxAge(hsts)
		switch {
		case maxAge < 0:
			res.Records = append(res.Records, "Strict-Transport-Security")
			fmt.Fprintf(&b, "FAIL  Strict-Transport-Security is malformed: %s\n", hsts)
		case maxAge < 15552000:
			pass++
			fmt.Fprintf(&b, "PASS  Strict-Transport-Security is set but short (max-age=%d, want ≥ 15552000).\n", maxAge)
		default:
			pass++
			extra := ""
			if !strings.Contains(strings.ToLower(hsts), "includesubdomains") {
				extra = " (consider includeSubDomains)"
			}
			fmt.Fprintf(&b, "PASS  Strict-Transport-Security: %s%s\n", hsts, extra)
		}
	}
	csp := hdr.Get("Content-Security-Policy")
	grade("Content-Security-Policy", csp != "", "Content-Security-Policy "+nonEmpty(csp, "is missing — the page runs whatever script arrives."))
	frame, ancestors := hdr.Get("X-Frame-Options"), cspContains(csp, "frame-ancestors")
	grade("X-Frame-Options", frame != "" || ancestors,
		"X-Frame-Options "+nonEmpty(frame, "is missing"+map[bool]string{true: " (covered by CSP frame-ancestors).", false: " — the page is frameable, the clickjacking primitive."}[ancestors]))
	nosniff := hdr.Get("X-Content-Type-Options")
	grade("X-Content-Type-Options", strings.EqualFold(strings.TrimSpace(nosniff), "nosniff"),
		"X-Content-Type-Options "+nonEmpty(nosniff, "is missing — browsers may sniff responses into scripts."))
	ref := hdr.Get("Referrer-Policy")
	grade("Referrer-Policy", ref != "",
		"Referrer-Policy "+nonEmpty(ref, "is missing — full URLs leak to third parties by default."))
	if srv := hdr.Get("Server"); srv != "" {
		fmt.Fprintf(&b, "INFO  Server discloses %q.\n", srv)
	}
	if pw := hdr.Get("X-Powered-By"); pw != "" {
		fmt.Fprintf(&b, "INFO  X-Powered-By discloses %q — free reconnaissance.\n", pw)
	}
	fmt.Fprintf(&b, "\n%d of %d hardened.", pass, graded)
	res.OK = code < 400
	res.Output = strings.TrimSpace(b.String())
	return res, nil
}

func nonEmpty(v, missing string) string {
	if v != "" {
		return ": " + v
	}
	return " " + missing
}

// parseMaxAge reads max-age out of an HSTS value; -1 means absent or broken.
func parseMaxAge(hsts string) int {
	for _, part := range strings.Split(strings.ToLower(hsts), ";") {
		part = strings.TrimSpace(part)
		if v, ok := strings.CutPrefix(part, "max-age="); ok {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil || n < 0 {
				return -1
			}
			return n
		}
	}
	return -1
}

// cspContains reports whether a CSP value sets a directive.
func cspContains(csp, directive string) bool {
	for _, part := range strings.Split(strings.ToLower(csp), ";") {
		if strings.TrimSpace(part) == directive || strings.HasPrefix(strings.TrimSpace(part), directive+" ") {
			return true
		}
	}
	return false
}

// SiteAudit is the one-click version of "is this site actually up, securely":
// the HTTP chain, the presented certificate and the header grade in one run,
// so a new vhost gets a single verdict instead of three tabs.
func (s *Service) SiteAudit(ctx context.Context, target string, port int) (*ProbeResult, error) {
	if !ValidTarget(target) {
		return nil, fmt.Errorf("target must be a hostname or IP address")
	}
	if port == 0 {
		port = 443
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("port must be between 1 and 65535")
	}
	res := &ProbeResult{Tool: "siteaudit", Target: strings.TrimSpace(target), Records: []string{}}
	began := time.Now()
	var b strings.Builder
	httpRes, herr := s.HTTPCheck(ctx, target, port)
	if herr != nil {
		return nil, herr
	}
	fmt.Fprintf(&b, "── HTTP ──\n%s\n\n", httpRes.Output)
	tlsRes, terr := s.TLSCert(ctx, target, port)
	if terr != nil {
		return nil, terr
	}
	res.Records = append(res.Records, tlsRes.Records...)
	fmt.Fprintf(&b, "── TLS ──\n%s\n\n", tlsRes.Output)
	secRes, serr := s.HTTPSecurity(ctx, target, port)
	if serr != nil {
		return nil, serr
	}
	// The chain repeats what HTTP already showed; the grade is the new part.
	secBody := secRes.Output
	if i := strings.Index(secBody, "\n\n"); i >= 0 {
		secBody = secBody[i+2:]
	}
	fmt.Fprintf(&b, "── Headers ──\n%s", secBody)
	res.Duration = time.Since(began).Round(time.Millisecond).String()
	res.OK = httpRes.OK && tlsRes.OK
	if !res.OK {
		res.Error = "one or more checks failed — read the sections above"
	}
	res.Output = strings.TrimSpace(b.String())
	return res, nil
}

// Listeners answers the inward question the outward tools disclaim: what is
// this host actually bound to, on which addresses. `ss` with process names,
// falling back to netstat where iproute2 never arrived.
func (s *Service) Listeners(ctx context.Context) (*ProbeResult, error) {
	res := &ProbeResult{Tool: "listeners", Target: "this host"}
	var out, elapsed string
	var err error
	switch {
	case diagnosticHas("ss"):
		out, elapsed, err = diagnosticRun(ctx, 10*time.Second, "ss", "-tulnp")
	case diagnosticHas("netstat"):
		out, elapsed, err = diagnosticRun(ctx, 10*time.Second, "netstat", "-tulnp")
	default:
		res.Error = "neither ss nor netstat is installed on this host"
		return res, nil
	}
	if len(out) > maxProbeOutput {
		out = out[:maxProbeOutput] + "\n… (truncated)"
	}
	res.Output, res.Duration, res.OK = out, elapsed, err == nil
	if err != nil {
		res.Error = err.Error()
	}
	return res, nil
}

// Egress reports how this host reaches the internet: the source address and
// interface of the default route plus the default route itself. Read-only and
// targetless — the answer is about this machine, not a destination.
func (s *Service) Egress(ctx context.Context) (*ProbeResult, error) {
	res := &ProbeResult{Tool: "egress", Target: "this host", Records: []string{}}
	if !diagnosticHas("ip") {
		res.Error = "ip is not installed on this host"
		return res, nil
	}
	start := time.Now()
	var b strings.Builder
	for _, family := range []struct{ flag, label, target string }{
		{"-4", "IPv4", "1.1.1.1"},
		{"-6", "IPv6", "2606:4700:4700::1111"},
	} {
		fmt.Fprintf(&b, "%s:\n", family.label)
		out, _, err := diagnosticRun(ctx, 10*time.Second, "ip", family.flag, "route", "get", family.target)
		if err != nil {
			fmt.Fprintf(&b, "No route found: %v\n\n", err)
			continue
		}
		res.OK = true
		b.WriteString(out + "\n")
		for _, field := range []struct{ key, label string }{{"src", "Source"}, {"dev", "Interface"}} {
			if v := routeField(out, field.key); v != "" {
				fmt.Fprintf(&b, "%s: %s\n", field.label, v)
				if field.key == "src" {
					res.Records = append(res.Records, v)
				}
			}
		}
		if def, _, err := diagnosticRun(ctx, 10*time.Second, "ip", family.flag, "route", "show", "default"); err == nil {
			fmt.Fprintf(&b, "Default route:\n%s\n", strings.TrimSpace(def))
		}
		b.WriteString("\n")
	}
	if !res.OK {
		res.Error = "Neither IPv4 nor IPv6 has a route to the diagnostic addresses."
	}
	res.Duration = time.Since(start).Round(time.Millisecond).String()
	res.Output = strings.TrimSpace(b.String() + "Only the kernel routing table was queried; no internet request was sent. A local source address does not reveal an upstream NAT's public address.")
	return res, nil
}

// routeField picks a key's value out of `ip route` output, whose pairs come in
// no guaranteed order — the same reason netinfo parses routes as pairs.
func routeField(out, key string) string {
	fields := strings.Fields(out)
	for i := 0; i < len(fields)-1; i++ {
		if fields[i] == key {
			return fields[i+1]
		}
	}
	return ""
}

// Neighbours shows the ARP/NDP table — which LAN neighbours this host knows
// and whether they are reachable. The "is the printer even on the LAN"
// question, answered next to the listeners that would serve it.
func (s *Service) Neighbours(ctx context.Context) (*ProbeResult, error) {
	res := &ProbeResult{Tool: "neigh", Target: "this host"}
	if !hostexec.AvailableOnHost("ip") {
		res.Error = "ip is not installed on this host"
		return res, nil
	}
	out, elapsed, err := runProbe(ctx, 10*time.Second, "ip", "neigh", "show")
	if len(out) > maxProbeOutput {
		out = out[:maxProbeOutput] + "\n… (truncated)"
	}
	res.Output, res.Duration, res.OK = out, elapsed, err == nil
	if err != nil {
		res.Error = err.Error()
	} else if strings.TrimSpace(out) == "" {
		res.Output = "No neighbours known — the ARP/NDP table is empty."
	}
	return res, nil
}
