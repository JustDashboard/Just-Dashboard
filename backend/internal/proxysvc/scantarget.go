package proxysvc

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/idna"
)

// What the operator typed into a scan or watch field, as a host and a port.
//
// Those fields are pasted into: a URL from the address bar, host:port from a
// mail client's settings, an IPv6 literal, a name in its own script. Dialed
// verbatim, every one of them fails with a DNS error naming the whole string,
// and host:port was worse — joined with the default port it became
// "[mail.example.com:993]:443". They are read the way a browser reads them,
// and refused with the reason when they are not an address at all.
//
// frontend/src/lib/scan-target.ts is the same parser, so the page can say
// what is wrong before it sends anything; scan-target-cases.json is the table
// both are tested against.

// ScanTarget is a host to dial and the port to dial it on. Host is lowercase
// ASCII: a name in punycode, or an IP address in its canonical form.
type ScanTarget struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

// schemePorts are the schemes whose port speaks TLS from the first byte, plus
// http and ws: a TLS report of http://example.com means its HTTPS side.
var schemePorts = map[string]int{
	"https": 443, "http": 443, "wss": 443, "ws": 443,
	"imaps": 993, "pop3s": 995, "smtps": 465, "submissions": 465,
	"ldaps": 636, "ftps": 990, "ircs": 6697, "mqtts": 8883, "amqps": 5671,
}

const scanLabel = `[a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9_])?`

var scanHostRe = regexp.MustCompile(`^` + scanLabel + `(\.` + scanLabel + `)*$`)

// ParseScanTarget reads raw as a scan target. port is a separately given
// port, 0 when there is none; a port written into raw wins over a scheme's,
// and the two given ports must agree.
func ParseScanTarget(raw string, port int) (ScanTarget, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ScanTarget{}, errors.New("enter a domain, for example app.example.com")
	}
	if port < 0 || port > 65535 {
		return ScanTarget{}, fmt.Errorf("port %d is outside 1–65535", port)
	}
	schemePort := 0
	if scheme, rest, ok := strings.Cut(s, "://"); ok {
		p, known := schemePorts[strings.ToLower(scheme)]
		if !known {
			return ScanTarget{}, fmt.Errorf("%s:// does not start with TLS; use https://, imaps:// or the name alone", scheme)
		}
		schemePort, s = p, rest
	}
	// A path, a query or a fragment names a page on the server, not the server.
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	// user@host would let the text before the @ read as the destination to
	// anyone skimming the field, while the scan goes somewhere else.
	if strings.Contains(s, "@") {
		return ScanTarget{}, errors.New("a user name is not part of the address; enter the host alone")
	}
	host, written, err := splitScanHostPort(s)
	if err != nil {
		return ScanTarget{}, err
	}

	t := ScanTarget{}
	switch {
	case written != 0 && port != 0 && written != port:
		return ScanTarget{}, fmt.Errorf("the address says port %d and the port field says %d", written, port)
	case written != 0:
		t.Port = written
	case port != 0:
		t.Port = port
	case schemePort != 0:
		t.Port = schemePort
	default:
		t.Port = 443
	}

	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" {
		return ScanTarget{}, errors.New("enter a domain, for example app.example.com")
	}
	if ip := net.ParseIP(host); ip != nil {
		t.Host = ip.String()
		return t, nil
	}
	if strings.HasPrefix(host, "*.") {
		return ScanTarget{}, errors.New("a wildcard is not an address; scan one of the names it covers")
	}
	if !isASCII(host) {
		ascii, err := idna.Lookup.ToASCII(host)
		if err != nil {
			return ScanTarget{}, fmt.Errorf("%q is not a domain name", raw)
		}
		host = ascii
	}
	if len(host) > 253 || !scanHostRe.MatchString(host) {
		return ScanTarget{}, fmt.Errorf("%q is not a domain name", strings.TrimSpace(raw))
	}
	t.Host = host
	return t, nil
}

// ParseScanQuery reads a target given as two texts: ?domain= and ?port= in a
// link, or the report's name and port fields. An empty port is no port; one
// that is given must be a port, so "0" and "+993" are refused rather than read
// as 443 and 993.
func ParseScanQuery(domain, port string) (ScanTarget, error) {
	if port == "" {
		return ParseScanTarget(domain, 0)
	}
	if strings.Trim(port, "0123456789") != "" {
		return ScanTarget{}, fmt.Errorf("port %q is not a number", port)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return ScanTarget{}, fmt.Errorf("port %s is outside 1–65535", port)
	}
	return ParseScanTarget(domain, n)
}

// splitScanHostPort separates a port written after the host. An IPv6 address
// takes one only inside brackets, as in a URL; bare, its colons are its own.
func splitScanHostPort(s string) (host string, port int, err error) {
	rest := ""
	switch {
	case strings.HasPrefix(s, "["):
		end := strings.Index(s, "]")
		if end < 0 {
			return "", 0, errors.New("an IPv6 address opened with [ is not closed with ]")
		}
		host, rest = s[1:end], s[end+1:]
		if ip := net.ParseIP(host); ip == nil || !strings.Contains(host, ":") {
			return "", 0, fmt.Errorf("%q inside [ ] is not an IPv6 address", host)
		}
		if rest != "" && !strings.HasPrefix(rest, ":") {
			return "", 0, fmt.Errorf("unexpected %q after the address", rest)
		}
		rest = strings.TrimPrefix(rest, ":")
	case strings.Count(s, ":") == 1:
		host, rest, _ = strings.Cut(s, ":")
	case strings.Count(s, ":") > 1:
		if net.ParseIP(s) == nil {
			return "", 0, fmt.Errorf("%q is not an IPv6 address; write one with a port as [2001:db8::1]:443", s)
		}
		return s, 0, nil
	default:
		return s, 0, nil
	}
	if rest == "" {
		return host, 0, nil
	}
	n, err := strconv.Atoi(rest)
	if err != nil {
		return "", 0, fmt.Errorf("port %q is not a number", rest)
	}
	if n < 1 || n > 65535 {
		return "", 0, fmt.Errorf("port %d is outside 1–65535", n)
	}
	return host, n, nil
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}
