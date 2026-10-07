package netx

import (
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

// Every value that reaches a batch file, an nft ruleset or an argument vector
// passes one of these first. The files are data for ip, tc and nft rather than
// text for a shell, but each of those has a grammar of its own — a newline
// starts a new command in a batch file, a brace closes a block in a ruleset —
// so a value is accepted only in the shape its field allows, never escaped.

// ValidIfName is the kernel's rule for a device name (dev_valid_name): one to
// fifteen bytes, not "." or "..", and no slash, colon or whitespace. Narrower
// than the kernel in one way: only ASCII letters, digits and ".-_@" are
// accepted, which every name a person types and every tool generates fits,
// and which keeps a name from meaning something to nft's lexer.
func ValidIfName(name string) error {
	if name == "" || len(name) > 15 {
		return fmt.Errorf("an interface name is 1 to 15 characters")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("%q is not a valid interface name", name)
	}
	for _, r := range name {
		if r > unicode.MaxASCII || !(unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune(".-_@", r)) {
			return fmt.Errorf("an interface name may hold only letters, digits and . - _ @")
		}
	}
	return nil
}

// ValidNamespace is `ip netns`'s rule: a name that is a file under
// /run/netns, so no slash, and nothing that is a path component on its own.
func ValidNamespace(name string) error {
	if name == "" || len(name) > 64 {
		return fmt.Errorf("a namespace name is 1 to 64 characters")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("%q is not a valid namespace name", name)
	}
	for _, r := range name {
		if r > unicode.MaxASCII || !(unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("-_.", r)) {
			return fmt.Errorf("a namespace name may hold only letters, digits and - _ .")
		}
	}
	return nil
}

// ParseAddr reads one address, either family. Zones are refused: a scoped
// link-local address names an interface in a way none of the forms here have
// a field for.
func ParseAddr(s string) (netip.Addr, error) {
	a, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil {
		return netip.Addr{}, fmt.Errorf("%q is not an IP address", s)
	}
	if a.Zone() != "" {
		return netip.Addr{}, fmt.Errorf("%q carries a zone; give the address without it", s)
	}
	return a.Unmap(), nil
}

// ParsePrefix reads a network or a single address, which is a /32 or /128.
// The host bits are kept: an interface address is 10.8.0.1/24, not
// 10.8.0.0/24, and the callers that need a network call Masked themselves.
func ParsePrefix(s string) (netip.Prefix, error) {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, "/") {
		a, err := ParseAddr(s)
		if err != nil {
			return netip.Prefix{}, err
		}
		return netip.PrefixFrom(a, a.BitLen()), nil
	}
	p, err := netip.ParsePrefix(s)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("%q is not an address or a network in CIDR form", s)
	}
	if p.Addr().Zone() != "" {
		return netip.Prefix{}, fmt.Errorf("%q carries a zone; give the network without it", s)
	}
	return netip.PrefixFrom(p.Addr().Unmap(), p.Bits()), nil
}

// familyOf names the family the way ip and nft spell it.
func familyOf(a netip.Addr) string {
	if a.Is4() {
		return "inet"
	}
	return "inet6"
}

// ParsePorts reads a port, a range written 8000-8010 or 8000:8010, and
// returns it in the form nft writes (8000-8010). A range runs low to high.
func ParsePorts(s string) (string, error) {
	s = strings.TrimSpace(s)
	sep := "-"
	if strings.Contains(s, ":") {
		sep = ":"
	}
	lo, hi, isRange := strings.Cut(s, sep)
	a, err := portNumber(lo)
	if err != nil {
		return "", err
	}
	if !isRange {
		return strconv.Itoa(a), nil
	}
	b, err := portNumber(hi)
	if err != nil {
		return "", err
	}
	if b <= a {
		return "", fmt.Errorf("a port range runs from the lower port to the higher")
	}
	return fmt.Sprintf("%d-%d", a, b), nil
}

func portNumber(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 || n > 65535 {
		return 0, fmt.Errorf("%q is not a port between 1 and 65535", s)
	}
	return n, nil
}

// portSpan is how many ports a ParsePorts result covers.
func portSpan(ports string) int {
	lo, hi, ok := strings.Cut(ports, "-")
	if !ok {
		return 1
	}
	a, _ := strconv.Atoi(lo)
	b, _ := strconv.Atoi(hi)
	return b - a + 1
}

// ParseProtocol accepts tcp, udp, and "both" where a field allows both.
func ParseProtocol(s string, allowBoth bool) (string, error) {
	switch p := strings.ToLower(strings.TrimSpace(s)); p {
	case "tcp", "udp":
		return p, nil
	case "both", "tcp+udp", "":
		if allowBoth {
			return "both", nil
		}
	}
	return "", fmt.Errorf("the protocol is tcp or udp")
}

// CleanLabel is a name a person gives something here: a port forward, a
// blocklist, a VPN client. It is stored, shown and written into a comment,
// so it is one line of printable text without the characters nft's comment
// string or a wg-quick comment would read as their own.
func CleanLabel(s string, max int) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("a name is required")
	}
	if len(s) > max {
		return "", fmt.Errorf("a name is at most %d characters", max)
	}
	for _, r := range s {
		if !unicode.IsPrint(r) || strings.ContainsRune("\"\\#;{}`$", r) {
			return "", fmt.Errorf("a name may not contain quotes, backslashes, braces, # ; ` or $")
		}
	}
	return s, nil
}

// ParseFeedURL accepts an https (or plain http) URL for a blocklist feed.
// Anything else — file:, a URL with credentials in it — is refused, because
// the server is the one fetching it.
func ParseFeedURL(s string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", fmt.Errorf("a feed is an http or https address")
	}
	if u.User != nil {
		return "", fmt.Errorf("a feed address may not carry a user name or password")
	}
	return u.String(), nil
}

// ValidCountry is an ISO 3166-1 alpha-2 code, lower case, as ipdeny names its
// zone files.
func ValidCountry(code string) (string, error) {
	c := strings.ToLower(strings.TrimSpace(code))
	if len(c) != 2 || c[0] < 'a' || c[0] > 'z' || c[1] < 'a' || c[1] > 'z' {
		return "", fmt.Errorf("%q is not a two-letter country code", code)
	}
	return c, nil
}
