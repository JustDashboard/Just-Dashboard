package hublink

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/tailnetgate"
)

// The codes the URL rule refuses with (protocol section 2.2). They are the API's
// error codes as they are.
const (
	CodeURLInvalid   = "hub_url_invalid"
	CodeNotTailnet   = "hub_url_not_tailnet"
	CodeSameHost     = "hub_url_same_host"
	CodeNotOnTailnet = "hub_not_on_tailnet"
	// CodeURLRefused is a stored link this server stopped using: the address
	// fails the rule now, or another machine answers at the hub's name.
	CodeURLRefused    = "hub_url_refused"
	tailnetSuffixTail = ".ts.net"
)

// URLError is a hub address the rule refuses, with the step's code.
type URLError struct {
	Code    string
	Message string
}

func (e *URLError) Error() string { return e.Message }

func urlError(code, format string, a ...any) *URLError {
	return &URLError{Code: code, Message: fmt.Sprintf(format, a...)}
}

// labelPattern is one DNS label as a MagicDNS machine name spells it.
var labelPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// readOrigin applies steps 1 and 3 of the rule: the input read as an origin and
// spelt one way. A bare host is read as https:// plus that host. The result is
// the stored spelling every later comparison uses.
func readOrigin(raw string) (string, *url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil, urlError(CodeURLInvalid, "name the hub, for example https://hub.example.ts.net")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Hostname() == "" || u.Opaque != "" {
		return "", nil, urlError(CodeURLInvalid, "%q is not a URL with a host", clip(raw, 200))
	}
	if u.User != nil {
		return "", nil, urlError(CodeURLInvalid, "the hub's address must not carry a user name or password")
	}
	rest := raw[strings.Index(raw, "://")+3:]
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(rest, "?#") {
		return "", nil, urlError(CodeURLInvalid, "the hub's address must be an origin only, with no path, query or fragment")
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return "", nil, urlError(CodeURLInvalid, "%q is not a port", p)
		}
	}
	return spell(u), u, nil
}

// spell is accountlink's spelling of an origin, with a trailing dot on the
// host removed: lowercase scheme and host, and no port that is the scheme's
// own.
func spell(u *url.URL) string {
	scheme := strings.ToLower(u.Scheme)
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	port := u.Port()
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		host += ":" + port
	}
	return scheme + "://" + host
}

// hostOf is the host of a spelt origin, without brackets or port.
func hostOf(origin string) string {
	u, err := url.Parse(origin)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// ParseTestOrigin checks JD_HUB_LINK_TEST_ORIGIN: one exact origin, http or
// https, on a loopback host. It is for tests that run a hub with no tailnet and
// nothing else; empty means unset.
func ParseTestOrigin(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	origin, u, err := readOrigin(raw)
	if err != nil {
		return "", err
	}
	if s := strings.ToLower(u.Scheme); s != "http" && s != "https" {
		return "", errors.New("it must start with http:// or https://")
	}
	if !loopbackHost(u.Hostname()) {
		return "", errors.New("its host must be a loopback host: localhost, 127.0.0.0/8 or [::1]")
	}
	return origin, nil
}

func loopbackHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.Unmap().IsLoopback()
}

// isAddress is an IP literal, zone and all.
func isAddress(host string) bool {
	if _, err := netip.ParseAddr(host); err == nil {
		return true
	}
	return net.ParseIP(host) != nil
}

// TailnetStatus is what the rule needs from `tailscale status --json`.
type TailnetStatus struct {
	// Running is BackendState being Running.
	Running bool
	// Suffix is the tailnet's MagicDNS suffix, without a trailing dot.
	Suffix string
	// Self is this machine's MagicDNS name, without a trailing dot.
	Self string
	// Peers maps each peer's MagicDNS name, lowercase and without a trailing
	// dot, to its tailnet IPv4 address.
	Peers map[string]netip.Addr
}

// Tailnet reads the host's tailnet. The dashboard's is the host's tailscale
// CLI; tests give their own.
type Tailnet interface {
	Status(ctx context.Context) (TailnetStatus, error)
}

// suffixOf checks step 5: a tailnet this server is on, with MagicDNS.
func suffixOf(st TailnetStatus, err error) (string, error) {
	switch {
	case err != nil:
		return "", urlError(CodeNotTailnet, "this server is not on a tailnet with MagicDNS (%s), so it cannot connect to a hub", clip(err.Error(), 200))
	case !st.Running:
		return "", urlError(CodeNotTailnet, "this server is not on a tailnet with MagicDNS: Tailscale is not running")
	case st.Suffix == "" || !strings.HasSuffix(st.Suffix, tailnetSuffixTail):
		return "", urlError(CodeNotTailnet, "this server is not on a tailnet with MagicDNS")
	}
	return st.Suffix, nil
}

// oneMachine checks steps 6 and 7: one label under this server's own suffix,
// and not this server's own name.
func oneMachine(host, suffix, self string) error {
	label, rest, ok := strings.Cut(host, ".")
	if !ok || rest != suffix || !labelPattern.MatchString(label) {
		return urlError(CodeNotTailnet, "the hub must be a machine on this server's tailnet, named like hub.%s", suffix)
	}
	// A hub on this server's own name, on any port, would be sent this
	// dashboard's session cookie: browsers send a host-only cookie to every
	// port of its host.
	if self != "" && host == strings.ToLower(self) {
		return urlError(CodeSameHost, "%s is this server's own name; the hub must run on another machine", host)
	}
	return nil
}

// checkURL is the whole rule, applied when a person or the terminal names a
// hub. Step 8 runs only here, and finds the hub node, which is nil when
// tailscaled could not be asked or the origin is the test origin.
func (s *Service) checkURL(ctx context.Context, raw string) (string, *Machine, error) {
	origin, u, err := readOrigin(raw)
	if err != nil {
		return "", nil, err
	}
	if s.testOrigin != "" && origin == s.testOrigin {
		return origin, nil, nil
	}
	if strings.ToLower(u.Scheme) != "https" {
		return "", nil, urlError(CodeURLInvalid, "the hub's address must start with https://")
	}
	host := hostOf(origin)
	if isAddress(host) {
		return "", nil, urlError(CodeURLInvalid, "name the hub by its tailnet name, not by an address")
	}
	st, err := s.tailnet.Status(ctx)
	suffix, err := suffixOf(st, err)
	if err != nil {
		return "", nil, err
	}
	if err := oneMachine(host, suffix, st.Self); err != nil {
		return "", nil, err
	}
	node, err := s.confirmNode(ctx, host)
	if err != nil {
		return "", nil, err
	}
	return origin, node, nil
}

// confirmNode is step 8: the address this dashboard would dial belongs, by
// tailscaled's own account, to the machine of that name, and that machine is
// the hub node. When tailscaled cannot be asked the check is skipped and
// logged, and the node is recorded by a later check (2.4); the certificate and
// the steps before already bind the name.
func (s *Service) confirmNode(ctx context.Context, host string) (*Machine, error) {
	if s.whois == nil {
		return nil, nil
	}
	addr, err := s.dial.resolve(ctx, host)
	if err != nil {
		// Nothing to ask about; the request itself will say the hub cannot
		// be reached.
		return nil, nil
	}
	peer, err := s.whois.WhoIs(ctx, addr)
	switch {
	case errors.Is(err, tailnetgate.ErrNotAPeer):
		return nil, urlError(CodeNotOnTailnet, "no machine on this tailnet holds %s, the address %s has", addr, host)
	case err == nil && strings.TrimSpace(peer.StableID) == "":
		err = fmt.Errorf("%w: its answer names no StableID", tailnetgate.ErrUnavailable)
		fallthrough
	case err != nil:
		s.log.Info("the hub's tailnet node was not confirmed; tailscaled could not be asked", "hub", host, "err", err)
		return nil, nil
	case !strings.EqualFold(strings.TrimSuffix(peer.Node, "."), host):
		return nil, urlError(CodeNotOnTailnet, "%s is held by %s on this tailnet, not by %s", addr, clip(peer.Node, 100), host)
	}
	m := machineOf(peer)
	// The address was checked just now; the request Connect sends next goes
	// to it without asking again.
	s.gate.pass(host, addr, s.now())
	return &m, nil
}

// admitStored is the rule as it is applied to a stored link each time the
// connector starts with one (protocol 2.2, "When the rule runs"), and says why
// it fails. A hub at the test origin passes while that origin is set.
// Otherwise steps 1 to 4, which need no tailnet, always run: the address must
// still be an https name, not an IP literal or a loopback name. Steps 5 to 7
// run when this server's suffix can be read: still one machine under it that
// is not this server. When the suffix cannot be read they are skipped this
// time and the link runs.
func (s *Service) admitStored(ctx context.Context, stored string) error {
	if s.testOrigin != "" && stored == s.testOrigin {
		return nil
	}
	origin, u, err := readOrigin(stored)
	if err != nil {
		return err
	}
	if strings.ToLower(u.Scheme) != "https" {
		return errors.New("it is not an https address, and no test origin names it")
	}
	host := hostOf(origin)
	if isAddress(host) {
		return errors.New("it is an address, not a tailnet name")
	}
	if loopbackHost(host) {
		return errors.New("it is a loopback name, not a tailnet name, and no test origin names it")
	}
	st, err := s.tailnet.Status(ctx)
	if err != nil || !st.Running || st.Suffix == "" {
		s.log.Info("the hub's address was not checked; this server's tailnet could not be read", "hub", origin, "err", err)
		return nil
	}
	if _, err := suffixOf(st, nil); err != nil {
		return err
	}
	return oneMachine(host, st.Suffix, st.Self)
}

// ErrURLRefused is a stored link this server no longer uses: its address fails
// the rule now (the server moved to another tailnet, or the test origin that
// named it is gone), or another machine answers at the hub's name. The link is
// kept but idle until a person disconnects.
var ErrURLRefused = errors.New("this server no longer accepts the hub's address")

func refused(why error) error {
	return fmt.Errorf("%w: %v; disconnect, then connect again", ErrURLRefused, why)
}

// sameOriginAs reports whether raw is an absolute URL on origin, spelt as the
// rule spells it.
func sameOriginAs(origin, raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Host == "" || u.Opaque != "" {
		return false
	}
	return spell(u) == origin
}

// clip shortens text for a message, at a character boundary.
func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
