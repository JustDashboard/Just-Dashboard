package netsec

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
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

// DNSAuthority answers "who is authoritative for this name, and do they
// agree": the zone's NS set, the parent's delegation and glue for it, and each
// authoritative address asked directly with recursion off for its SOA serial
// and NS set. A plain A lookup can show none of this.
func (s *Service) DNSAuthority(ctx context.Context, target string) (*ProbeResult, error) {
	if !ValidTarget(target) {
		return nil, fmt.Errorf("target must be a hostname or IP address")
	}
	if net.ParseIP(strings.TrimSpace(target)) != nil {
		return nil, fmt.Errorf("an authority check takes a domain name, not an IP address")
	}
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	start := time.Now()
	res := &ProbeResult{Tool: "dnsauth", Target: strings.TrimSpace(target), Records: []string{}}
	checkAuthority(ctx, res, target, authorityLookups)
	res.Duration = time.Since(start).Round(time.Millisecond).String()
	var b strings.Builder
	for _, fact := range res.Facts {
		fmt.Fprintf(&b, "%s: %s\n", fact.Label, fact.Value)
	}
	for _, table := range res.Tables {
		fmt.Fprintf(&b, "\n%s\n", table.Title)
		for _, row := range table.Rows {
			fmt.Fprintf(&b, "  %s\n", strings.Join(row, "  "))
		}
	}
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
		_, res.Summary = dialFailure(err)
		res.Verdict = ProbeFailed
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
		if rerr == nil && n == len(buf) {
			// There was more than one read's worth; say so rather than
			// quoting a truncated banner as the whole thing.
			out += "\n… (truncated to 4 KB)"
		}
		res.OK = true
		res.Output = out
		id := identifyBanner(port, buf[:n])
		describeBanner(res, id)
		res.Verdict = ProbeOK
		if id.Confidence == "low" || id.Confidence == "none" {
			res.Verdict = ProbeUnknown
		}
		res.Summary = "The service greeted with a banner identified as " + id.Protocol + " (" + id.Confidence + " confidence)."
		return res, nil
	}
	res.Verdict = ProbeUnknown
	if rerr != nil && !isTimeout(rerr) {
		res.Error = rerr.Error()
		res.Output = "Connected, then the connection closed without a banner."
		res.Summary = "Connected, then the service closed the connection without a greeting; it could not be identified."
		return res, nil
	}
	res.Error = "connected but the service sent nothing within 5 seconds"
	res.Output = "Connected, but the service sent no banner. It is waiting for a request first — " +
		"HTTP services behave this way, so try the HTTP tool against this port instead."
	res.Summary = "Connected, but the service waits for the client to speak first; it could not be identified from a greeting."
	res.link("Inspect HTTP on this port", "/network/tools?tool=http&target="+url.QueryEscape(target))
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
	if !diagnosticHas("ssh-keyscan") {
		res.Error = "ssh-keyscan is not installed on this host"
		return res, nil
	}
	out, elapsed, err := diagnosticRun(ctx, 20*time.Second, "ssh-keyscan",
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

// targetURL renders the URL a web tool starts from: HTTPS everywhere except
// port 80, the same rule HTTPCheck uses.
func targetURL(scheme, target string, port int) string {
	u := url.URL{Scheme: scheme, Host: net.JoinHostPort(target, strconv.Itoa(port)), Path: "/"}
	return u.String()
}

func sinceMs(t time.Time) string {
	return time.Since(t).Round(time.Millisecond).String()
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

// Listeners answers the inward question the outward tools disclaim: what is
// this host actually bound to, on which addresses. `ss` with process names,
// falling back to netstat where iproute2 never arrived. Each row links to the
// Ports page, which owns ownership and exposure.
func (s *Service) Listeners(ctx context.Context) (*ProbeResult, error) {
	res := &ProbeResult{Tool: "listeners", Target: "this host"}
	var out, elapsed, tool string
	var err error
	switch {
	case diagnosticHas("ss"):
		tool = "ss"
		out, elapsed, err = diagnosticRun(ctx, 10*time.Second, "ss", "-tulnp")
	case diagnosticHas("netstat"):
		tool = "netstat"
		out, elapsed, err = diagnosticRun(ctx, 10*time.Second, "netstat", "-tulnp")
	default:
		res.Error = "neither ss nor netstat is installed on this host"
		res.Verdict = ProbeFailed
		return res, nil
	}
	if len(out) > maxProbeOutput {
		out = out[:maxProbeOutput] + "\n… (truncated)"
	}
	res.Output, res.Duration, res.OK = out, elapsed, err == nil
	if err != nil {
		res.Error = err.Error()
		res.Verdict, res.Summary = ProbeFailed, "The socket table could not be read."
		return res, nil
	}
	describeListeners(res, out, tool)
	return res, nil
}

// Egress reports how this host reaches the internet, each family on its own:
// the route the kernel selects, its source and the default routes. Read-only
// and targetless — the answer is about this machine, not a destination.
func (s *Service) Egress(ctx context.Context) (*ProbeResult, error) {
	res := &ProbeResult{Tool: "egress", Target: "this host", Records: []string{}}
	if !diagnosticHas("ip") {
		res.Error = "ip is not installed on this host"
		res.Verdict = ProbeFailed
		return res, nil
	}
	start := time.Now()
	var b strings.Builder
	var families []egressFamily
	for _, family := range []struct{ flag, label, target string }{
		{"-4", "IPv4", "1.1.1.1"},
		{"-6", "IPv6", "2606:4700:4700::1111"},
	} {
		f := egressFamily{Label: family.label}
		fmt.Fprintf(&b, "%s:\n", family.label)
		out, _, err := diagnosticRun(ctx, 10*time.Second, "ip", family.flag, "route", "get", family.target)
		if def, _, derr := diagnosticRun(ctx, 10*time.Second, "ip", family.flag, "route", "show", "default"); derr == nil {
			for _, line := range strings.Split(strings.TrimSpace(def), "\n") {
				if strings.TrimSpace(line) != "" {
					f.Defaults = append(f.Defaults, strings.TrimSpace(line))
				}
			}
		}
		if err != nil {
			f.RouteErr = strings.TrimSpace(nonEmptyOr(out, err.Error()))
			fmt.Fprintf(&b, "No route found: %v\n\n", err)
			families = append(families, f)
			continue
		}
		b.WriteString(out + "\n")
		if route, ok := parseRouteGet(out); ok {
			f.Route, f.RouteOK = route, true
			if a, err := netip.ParseAddr(route.Source); err == nil {
				f.SourceVal, f.Scope = a, addressScope(a)
				res.Records = append(res.Records, route.Source)
			}
		}
		if len(f.Defaults) > 0 {
			fmt.Fprintf(&b, "Default route:\n%s\n", strings.Join(f.Defaults, "\n"))
		}
		b.WriteString("\n")
		families = append(families, f)
	}
	describeEgress(res, families)
	res.Duration = time.Since(start).Round(time.Millisecond).String()
	if !res.OK {
		res.Error = "Neither IPv4 nor IPv6 has a route to the diagnostic addresses."
	}
	res.Output = strings.TrimSpace(b.String() + "Only the kernel routing table was queried; no internet request was sent. A local source address does not reveal an upstream NAT's public address.")
	return res, nil
}

// Neighbours shows the ARP/NDP cache — which LAN neighbours this host has
// talked to recently and what state each entry is in. It sends nothing.
func (s *Service) Neighbours(ctx context.Context) (*ProbeResult, error) {
	res := &ProbeResult{Tool: "neigh", Target: "this host"}
	if !diagnosticHas("ip") {
		res.Error = "ip is not installed on this host"
		res.Verdict = ProbeFailed
		return res, nil
	}
	out, elapsed, err := diagnosticRun(ctx, 10*time.Second, "ip", "neigh", "show")
	if len(out) > maxProbeOutput {
		out = out[:maxProbeOutput] + "\n… (truncated)"
	}
	res.Output, res.Duration, res.OK = out, elapsed, err == nil
	if err != nil {
		res.Error = err.Error()
		res.Verdict, res.Summary = ProbeFailed, "The neighbour cache could not be read."
		return res, nil
	}
	describeNeighbours(res, out)
	if strings.TrimSpace(out) == "" {
		res.Output = "No neighbours known — the ARP/NDP table is empty."
	}
	return res, nil
}
