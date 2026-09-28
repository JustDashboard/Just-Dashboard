package proxysvc

import (
	"cmp"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strconv"
	"strings"
	"syscall"
)

// Why a scan never completed a handshake.
//
// "Nothing answered a TLS handshake", with Go's error under it, was the whole
// report for four different faults that need four different fixes: a name
// with no record, a port nothing listens on, a firewall that drops the
// connection, and a port that answers something other than TLS — nginx's
// `listen 443;` without `ssl` is the classic, and it answers the ClientHello
// with an HTTP 400. The scan now says how far it got, where it was connected,
// and whether that address is this server at all, since the advice for a
// refusal here is wrong for a refusal somewhere else.

// ScanFailure is how far a scan that never completed a handshake got.
type ScanFailure struct {
	// Stage is the step that failed: "dns", "connect" or "handshake".
	Stage string `json:"stage"`
	// Reason is what happened there. For dns: "no-such-host", "timeout" or
	// "error". For connect: "refused", "timeout", "unreachable" or "error".
	// For handshake: "alert" (the server refused), "plain-http" or "not-tls"
	// (something that is not TLS answered), "closed", "timeout" or "error".
	Reason string `json:"reason"`
	// Address is where the failing connection went, as ip:port, once the
	// name had resolved.
	Address string `json:"address,omitempty"`
	// Alert is the TLS alert the server refused the handshake with.
	Alert string `json:"alert,omitempty"`
	// Answer is the first bytes a service that does not speak TLS sent back.
	Answer string `json:"answer,omitempty"`
	// Where says whose answer the failure was: "here" when the connection
	// went to this server (loopback, or an address on one of its
	// interfaces), "cloudflare" for Cloudflare's proxy, "elsewhere" for
	// another host, and "unknown" when this server's own public address in
	// that family (IPv4 or IPv6) is mapped in front of it by the provider and
	// so cannot be compared.
	Where string `json:"where,omitempty"`
	// DNS is the name resolved beside this server's own addresses. It is
	// absent when the name did not resolve.
	DNS *DomainCheck `json:"dns,omitempty"`
}

// handshakeError is a handshake that failed on a connection that had opened,
// carrying the address it opened to; everything before that is the dialer's
// own error, which names its address itself.
type handshakeError struct {
	addr string
	err  error
}

func (e *handshakeError) Error() string { return e.err.Error() }
func (e *handshakeError) Unwrap() error { return e.err }

// classifyDialError names the stage and the reason of a failed handshake.
func classifyDialError(err error) ScanFailure {
	var dns *net.DNSError
	var shake *handshakeError
	switch {
	case errors.As(err, &dns):
		failure := ScanFailure{Stage: "dns", Reason: "error"}
		if dns.IsNotFound {
			failure.Reason = "no-such-host"
		} else if dns.IsTimeout {
			failure.Reason = "timeout"
		}
		return failure
	case !errors.As(err, &shake):
		failure := ScanFailure{Stage: "connect", Reason: "error"}
		var op *net.OpError
		if errors.As(err, &op) && op.Addr != nil {
			failure.Address = op.Addr.String()
		}
		switch {
		case errors.Is(err, syscall.ECONNREFUSED):
			failure.Reason = "refused"
		case errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH):
			failure.Reason = "unreachable"
		case isTimeout(err):
			failure.Reason = "timeout"
		}
		return failure
	}
	failure := ScanFailure{Stage: "handshake", Reason: "error", Address: shake.addr}
	var record tls.RecordHeaderError
	if alert, alerted := remoteAlert(err); alerted {
		failure.Reason, failure.Alert = "alert", alert
	} else if errors.As(err, &record) {
		failure.Answer = printable(record.RecordHeader[:])
		failure.Reason = "not-tls"
		if strings.HasPrefix(failure.Answer, "HTTP/") {
			failure.Reason = "plain-http"
		}
	} else if errors.Is(err, io.EOF) || errors.Is(err, syscall.ECONNRESET) {
		failure.Reason = "closed"
	} else if isTimeout(err) {
		failure.Reason = "timeout"
	}
	return failure
}

// printable keeps what can be shown of a service's bytes: printable ASCII,
// with anything else as a dot, the way a hex dump's text column does.
func printable(b []byte) string {
	var out strings.Builder
	for _, c := range b {
		if c >= 0x20 && c < 0x7f {
			out.WriteByte(c)
		} else {
			out.WriteByte('.')
		}
	}
	return out.String()
}

// unanswered records a handshake that never completed: the stage it stopped
// at, the finding for that stage, and the name's addresses beside this
// server's, which say whether the connection reached this server at all.
func unanswered(ctx context.Context, scan *TLSScan, err error) {
	failure := classifyDialError(err)
	if failure.Stage != "dns" {
		failure.DNS = CheckDomainDNS(ctx, scan.Domain)
		failure.Where = whereConnected(failure.Address, failure.DNS, localAddresses(), hostAddresses())
	}
	scan.Failure = &failure
	scan.Error = err.Error()
	scan.Grade = "F"
	finding := failureFinding(scan, failure, err)
	scan.Summary = failureSummary(scan, failure)
	scan.Findings = append(scan.Findings, finding)
}

// failureSummary is the one line under the F.
func failureSummary(scan *TLSScan, f ScanFailure) string {
	target := net.JoinHostPort(scan.Domain, strconv.Itoa(scan.Port))
	where := f.Address
	if where == "" {
		where = target
	}
	switch f.Stage + "/" + f.Reason {
	case "dns/no-such-host":
		return scan.Domain + " does not resolve."
	case "dns/timeout", "dns/error":
		return scan.Domain + " could not be looked up."
	case "connect/refused":
		return where + " refused the connection."
	case "connect/timeout":
		return where + " did not answer the connection."
	case "connect/unreachable":
		return "There is no route from this server to " + where + "."
	case "handshake/alert":
		return "The server on " + target + " refused the handshake."
	case "handshake/plain-http":
		return where + " answers plain HTTP, not TLS."
	case "handshake/not-tls":
		return where + " answers something other than TLS."
	case "handshake/closed":
		return where + " closed the connection during the handshake."
	case "handshake/timeout":
		return where + " accepted the connection and never answered the handshake."
	case "connect/error":
		return "The connection to " + where + " failed."
	}
	return "The TLS handshake with " + target + " failed."
}

// failureFinding is the stage's finding: what happened, and the fix that fits
// it.
func failureFinding(scan *TLSScan, f ScanFailure, err error) ScanFinding {
	port := strconv.Itoa(scan.Port)
	where := f.Address
	if where == "" {
		where = net.JoinHostPort(scan.Domain, port)
	}
	finding := ScanFinding{Level: "critical", Detail: err.Error()}
	switch f.Stage + "/" + f.Reason {
	case "dns/no-such-host":
		finding.ID, finding.Title = "dns.unresolved", "The name does not resolve"
		finding.Detail = "The resolver this server uses has no address for " + scan.Domain + "."
		finding.Advice = "Add an A or AAAA record for " + scan.Domain + " pointing at the server that should answer. A record made in the last few minutes may not have reached this server's resolver yet."
	case "dns/timeout", "dns/error":
		finding.ID, finding.Title = "dns.failed", "The name could not be looked up"
		finding.Advice = "The lookup failed rather than finding no record, so nothing can be said about the name yet. Scan again; if it keeps failing, check the resolvers this server uses and the name's own name servers."
	case "connect/refused":
		finding.ID, finding.Title = "tcp.refused", "Nothing is listening on port "+port
		finding.Detail = where + " refused the connection."
		finding.Advice = hereOrThere(scan, f, advice{
			here:       "Nothing here accepts connections on port " + port + ". Check that the proxy has a site listening on " + port + " and that it is running.",
			there:      "The refusal is that host's.",
			cloudflare: cloudflareOtherPorts,
		})
	case "connect/timeout":
		finding.ID, finding.Title = "tcp.timeout", "No answer on port "+port
		finding.Detail = where + " did not answer the connection within 8 seconds."
		finding.Advice = hereOrThere(scan, f, advice{
			here:       "A firewall is most likely dropping it: check that port " + port + " is allowed in, here and at the provider.",
			there:      "A firewall there is dropping it, or the host is down or no longer at that address.",
			cloudflare: cloudflareOtherPorts,
		})
	case "connect/unreachable":
		finding.ID, finding.Title = "tcp.unreachable", "No route to "+where
		finding.Advice = "This server cannot reach that address at all: typically an IPv6 address on a server with no IPv6 route, or a private address outside this server's network."
	case "handshake/alert":
		finding.ID, finding.Title = "tls.refused", "The server refused the handshake"
		finding.Detail = "It answered " + f.Alert + ", both to the handshake a current client makes and to one offering every version and cipher suite this check has."
		finding.Advice = hereOrThere(scan, f, advice{
			here: "Something on port " + port + " speaks TLS and will not finish a handshake for " + scan.Domain +
				". It may have no certificate for that name (nginx's ssl_reject_handshake and Caddy refuse a name they have no certificate for), want a client certificate, or take only cipher suites this check cannot offer, such as finite-field DHE or Camellia.",
			cloudflare: cloudflareRefusal(scan.Domain),
		})
	case "handshake/plain-http":
		finding.ID, finding.Title = "tls.plain-http", "Port "+port+" answers plain HTTP, not TLS"
		finding.Detail = fmt.Sprintf("%s answered the TLS handshake with %q: an HTTP server without TLS on that port.", where, f.Answer)
		finding.Advice = hereOrThere(scan, f, advice{
			here:       "In nginx a listen directive without ssl serves plain HTTP. Write it as listen " + port + " ssl; and give the server block a certificate.",
			there:      "Its web server serves plain HTTP on " + port + ". If that is nginx, a listen directive without ssl does it: the fix is listen " + port + " ssl; and a certificate in that server block, on that host.",
			cloudflare: cloudflarePlainHTTP(scan.Port),
		})
	case "handshake/not-tls":
		finding.ID, finding.Title = "tls.not-tls", "Port "+port+" does not speak TLS"
		finding.Detail = fmt.Sprintf("%s answered the TLS handshake with %q, which is not TLS.", where, f.Answer)
		finding.Advice = hereOrThere(scan, f, advice{
			here:       "Another service owns this port, or it starts in plain text and upgrades with STARTTLS, as SMTP on 25 and 587 and IMAP on 143 do. This check speaks TLS from the first byte.",
			cloudflare: cloudflareHandshake,
		})
	case "handshake/closed":
		finding.ID, finding.Title = "tls.closed", "The connection closed during the handshake"
		finding.Detail = where + " accepted the connection and closed it without a TLS answer."
		finding.Advice = hereOrThere(scan, f, advice{
			here:       "Something listens on " + port + " and ended the handshake: a TLS server with no certificate to offer for " + scan.Domain + ", a proxy passing the connection to a backend that is down, or a service that closes on data it does not expect.",
			cloudflare: cloudflareHandshake,
		})
	case "handshake/timeout":
		finding.ID, finding.Title = "tls.timeout", "No TLS answer on port "+port
		finding.Detail = where + " accepted the connection and sent nothing back to the handshake before the scan's 10-second limit."
		finding.Advice = hereOrThere(scan, f, advice{
			here:       "Something listens there and does not speak first in TLS: a service that waits for its own protocol, such as a database, or a proxy stalled on its backend.",
			cloudflare: cloudflareHandshake,
		})
	case "connect/error":
		finding.ID, finding.Title = "tcp.failed", "The connection to "+where+" failed"
		finding.Advice = "The error above is the network's own account of it."
	default:
		finding.ID, finding.Title = "tls.failed", "The TLS handshake failed"
		finding.Advice = "The error above is the TLS library's own account of it."
	}
	return finding
}

// whereConnected says whose answer a failed connection got. The address it
// went to decides when there is one: a name with an old record beside this
// server's may have sent it elsewhere. Without one, the name's records do.
func whereConnected(address string, dns *DomainCheck, local, public []string) string {
	own := func(ip string) bool { return slices.Contains(local, ip) }
	if host, _, err := net.SplitHostPort(address); err == nil {
		ip := net.ParseIP(host)
		switch {
		case ip == nil:
		case ip.IsLoopback() || own(ip.String()):
			return "here"
		case isKnownProxyAddress(ip):
			return "cloudflare"
		case familyKnown(ip, public):
			return "elsewhere"
		}
		return "unknown"
	}
	unseen := func(a string) bool { return !familyKnown(net.ParseIP(a), public) }
	switch {
	case dns == nil:
		return "unknown"
	case slices.ContainsFunc(dns.Addresses, own) || dns.PointsHere:
		return "here"
	case dns.BehindProxy:
		return "cloudflare"
	case len(dns.Addresses) > 0 && !slices.ContainsFunc(dns.Addresses, unseen):
		return "elsewhere"
	}
	return "unknown"
}

// familyKnown is whether an address that is on none of this server's
// interfaces is certainly another host's: only when this server has a public
// address of its own in that family. A dual-stack VM commonly has its IPv6 on
// the interface and its public IPv4 mapped in front of it by the provider, so
// an IPv4 address it does not see may still be its own.
func familyKnown(ip net.IP, public []string) bool {
	if ip == nil {
		return false
	}
	v4 := ip.To4() != nil
	return slices.ContainsFunc(public, func(p string) bool {
		own := net.ParseIP(p)
		return own != nil && (own.To4() != nil) == v4
	})
}

// localAddresses are every address on this machine's interfaces, private and
// loopback ones included: unlike a public DNS record, a scan run from this
// server does reach it on those.
func localAddresses() []string {
	out := []string{}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return out
	}
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok {
			out = append(out, ipn.IP.String())
		}
	}
	return out
}

// advice is a finding's fix for each place its answer can have come from;
// there is left empty when it reads the same as here.
type advice struct{ here, there, cloudflare string }

// hereOrThere picks the advice for a connection that reached this server,
// Cloudflare's proxy or another host. When that cannot be known both this
// server and another host are real possibilities, so both are said.
func hereOrThere(scan *TLSScan, f ScanFailure, a advice) string {
	there := cmp.Or(a.there, a.here)
	named := func(address, name string) string {
		if f.Address != "" {
			return address
		}
		return name
	}
	switch f.Where {
	case "here":
		return "That is this server. " + a.here
	case "cloudflare":
		return named(f.Address+" is", scan.Domain+" resolves to") + " Cloudflare's proxy, not this server. " + a.cloudflare
	case "elsewhere":
		out := named(f.Address+" is not this server.", scan.Domain+" does not resolve to this server.") + " " + there
		// An address scanned as itself has no record to point anywhere.
		if f.DNS != nil && len(f.DNS.HostAddresses) > 0 && net.ParseIP(scan.Domain) == nil {
			out += " If it should be served here, point its record at " + strings.Join(f.DNS.HostAddresses, ", ") + "."
		}
		return out
	}
	out := "Whether " + named(f.Address+" is", scan.Domain+" resolves to") +
		" this server cannot be told from here, because the provider maps this server's public " + addressFamily(f.Address) + "address in front of it."
	if there == a.here {
		return out + " " + a.here
	}
	return out + " If it is: " + a.here + " If not: " + there
}

// addressFamily is "IPv4 " or "IPv6 " for an ip:port, and nothing otherwise.
func addressFamily(address string) string {
	host, _, err := net.SplitHostPort(address)
	ip := net.ParseIP(host)
	switch {
	case err != nil || ip == nil:
		return ""
	case ip.To4() != nil:
		return "IPv4 "
	}
	return "IPv6 "
}

// Cloudflare's proxy makes the TLS handshake at its own edge, before the
// server behind it is asked anything, and on its published ports only: no
// fault in that handshake is the origin's, and no nginx advice fits it.
const cloudflareHTTPS = "It takes HTTPS on 443, 2053, 2083, 2087, 2096 and 8443 only"

const (
	cloudflareOtherPorts = cloudflareHTTPS + ", so a connection to any other port never reaches the server behind it."
	cloudflareHandshake  = "Its edge makes the TLS handshake itself, before the server behind it is asked anything, so nothing on that server changes this. " + cloudflareHTTPS + "."
)

// cloudflareHTTPPorts are the ports Cloudflare's proxy serves as plain HTTP.
var cloudflareHTTPPorts = []int{80, 8080, 8880, 2052, 2082, 2086, 2095}

// cloudflarePlainHTTP is the advice for Cloudflare answering a ClientHello
// with HTTP, which on its plain-HTTP ports it does by design.
func cloudflarePlainHTTP(port int) string {
	if !slices.Contains(cloudflareHTTPPorts, port) {
		return cloudflareHandshake
	}
	return "Port " + strconv.Itoa(port) + " is one of the ports its edge serves as plain HTTP (80, 8080, 8880, 2052, 2082, 2086 and 2095), whatever the server behind it does. " +
		cloudflareHTTPS + ": scan the name on one of those."
}

// cloudflareRefusal is the advice for Cloudflare's edge refusing the
// handshake, which it does for a name it holds no certificate for.
func cloudflareRefusal(domain string) string {
	if net.ParseIP(domain) != nil {
		return "Its edge makes the TLS handshake itself and finishes one only for a name it holds a certificate for, and an address is not a name: scan the name instead."
	}
	return "Its edge makes the TLS handshake itself, before the server behind it is asked anything, and refuses a name it holds no certificate for: a zone not active on Cloudflare, a certificate still being issued, or a name two levels below the zone, such as a.b.example.com, which the free Universal certificate does not cover."
}
