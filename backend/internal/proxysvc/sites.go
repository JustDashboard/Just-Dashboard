package proxysvc

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Putting a domain in front of a port, without writing nginx.
//
// This is the gap between this dashboard and Nginx Proxy Manager, and it is
// the single most common thing anybody does to a server: something is running
// on 127.0.0.1:3000 and it needs to be app.example.com with a certificate.
// Doing it by hand means knowing eight proxy_set_header lines by heart, and
// getting one wrong produces a site that works until somebody logs in.
//
// The spec is the dashboard's description of a site, not nginx's — the same
// argument dockerx.ContainerSpec makes about container.Config. Rendering
// happens on the server, once, so there is exactly one implementation of "what
// does this mean"; a second one in TypeScript would drift, and the version
// that mattered would be the one nobody was reading. The output is ordinary
// nginx: it can be read in the editor on this page, committed, and edited by
// hand afterwards, and the form reads it back.
type SiteSpec struct {
	// Name is the file name under sites-available.
	Name    string   `json:"name"`
	Domains []string `json:"domains"`
	// Kind is proxy, static or redirect.
	Kind string `json:"kind"`

	Upstream string `json:"upstream,omitempty"`
	Root     string `json:"root,omitempty"`
	// RedirectTo is the destination for a redirect site, and Permanent
	// decides 301 against 302. The distinction matters more than it looks:
	// browsers cache a 301 more or less forever.
	RedirectTo string `json:"redirectTo,omitempty"`
	Permanent  bool   `json:"permanent,omitempty"`

	TLS        bool   `json:"tls"`
	CertPath   string `json:"certPath,omitempty"`
	KeyPath    string `json:"keyPath,omitempty"`
	ForceHTTPS bool   `json:"forceHttps"`
	// ManagedACME selects the fixed host/container-shared deployment webroot.
	ManagedACME bool `json:"managedAcme,omitempty"`
	HSTS        bool `json:"hsts"`
	HTTP2       bool `json:"http2"`

	WebSockets      bool   `json:"webSockets"`
	Gzip            bool   `json:"gzip"`
	BlockExploits   bool   `json:"blockExploits"`
	SecurityHeaders bool   `json:"securityHeaders"`
	ClientMaxBody   string `json:"clientMaxBody,omitempty"`
	ProxyTimeout    int    `json:"proxyTimeout,omitempty"`

	AllowFrom      []string `json:"allowFrom"`
	DenyFrom       []string `json:"denyFrom"`
	BasicAuthFile  string   `json:"basicAuthFile,omitempty"`
	BasicAuthRealm string   `json:"basicAuthRealm,omitempty"`

	AccessLog bool           `json:"accessLog"`
	Locations []SiteLocation `json:"locations"`
	// Custom is appended verbatim inside the server block. It is the escape
	// hatch, and it is the one field not validated beyond refusing an
	// unbalanced brace — a form that cannot express everything needs
	// somewhere to put the rest.
	Custom string `json:"custom,omitempty"`
}

// SiteLocation is an extra path handled differently from the site's default.
type SiteLocation struct {
	Path       string `json:"path"`
	Upstream   string `json:"upstream,omitempty"`
	Root       string `json:"root,omitempty"`
	WebSockets bool   `json:"webSockets"`
}

// managedMarker is written into every generated file and read back when the
// form loads one. A file without it was written by hand, and the form says so
// rather than silently offering to overwrite somebody's work.
const managedMarker = "# Managed by Just Dashboard."

var (
	siteNameRe     = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	domainRe       = regexp.MustCompile(`^(\*\.)?[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)
	bodySizeRe     = regexp.MustCompile(`^\d{1,6}[kKmMgG]?$`)
	locationPathRe = regexp.MustCompile(`^/[A-Za-z0-9._~!$&'()*+,;=:@%/-]{0,255}$`)
	absPathRe      = regexp.MustCompile(`^/[A-Za-z0-9._/-]{1,255}$`)
)

// ValidateSpec checks everything that would otherwise become a broken config
// or, worse, a working one that does something else.
//
// Every scalar is refused if it contains a newline, a semicolon or a brace.
// The endpoint already requires system.admin — the same capability as writing
// the file outright — so this is not a privilege boundary; it is the
// difference between a form field and a way to smuggle directives past the
// person reading the form.
func ValidateSpec(spec *SiteSpec) error {
	if !siteNameRe.MatchString(spec.Name) {
		return fmt.Errorf("name must be lowercase letters, digits, dots, dashes or underscores")
	}
	// The listing hides anything that looks like a backup, since that is what
	// a deleted site leaves behind. Refusing the name here means the form says
	// so, rather than writing a file that then never appears on the page.
	if isBackupFile(spec.Name) {
		return fmt.Errorf("a name ending like a backup file is hidden from the site list — call it something else")
	}
	if len(spec.Domains) == 0 {
		return fmt.Errorf("at least one domain is required")
	}
	for _, d := range spec.Domains {
		if !domainRe.MatchString(d) {
			return fmt.Errorf("%q is not a valid domain name", d)
		}
	}
	switch spec.Kind {
	case "proxy":
		if err := validUpstream(spec.Upstream); err != nil {
			return err
		}
	case "static":
		if !absPathRe.MatchString(spec.Root) {
			return fmt.Errorf("root must be an absolute path")
		}
	case "redirect":
		if err := validRedirect(spec.RedirectTo); err != nil {
			return err
		}
	default:
		return fmt.Errorf("kind must be proxy, static or redirect")
	}
	if spec.TLS {
		if !absPathRe.MatchString(spec.CertPath) || !absPathRe.MatchString(spec.KeyPath) {
			return fmt.Errorf("a TLS site needs an absolute path to its certificate and key")
		}
	}
	if spec.ClientMaxBody != "" && !bodySizeRe.MatchString(spec.ClientMaxBody) {
		return fmt.Errorf("upload limit must be a size like 50m")
	}
	if spec.ProxyTimeout < 0 || spec.ProxyTimeout > 3600 {
		return fmt.Errorf("timeout must be between 0 and 3600 seconds")
	}
	if spec.BasicAuthFile != "" && !absPathRe.MatchString(spec.BasicAuthFile) {
		return fmt.Errorf("the password file must be an absolute path")
	}
	if spec.BasicAuthRealm != "" && strings.ContainsAny(spec.BasicAuthRealm, "\"\n;{}") {
		return fmt.Errorf("the password prompt may not contain quotes, semicolons or braces")
	}
	for _, list := range [][]string{spec.AllowFrom, spec.DenyFrom} {
		for _, entry := range list {
			if err := validACLEntry(entry); err != nil {
				return err
			}
		}
	}
	// The renderer always writes a catch-all `location /`, so one in the list
	// is a second block for the same path: nginx takes the first and ignores
	// the rest, which is a site quietly serving something other than what the
	// form shows.
	seenLocation := map[string]bool{"/": spec.Kind != "redirect"}
	for _, loc := range spec.Locations {
		if !locationPathRe.MatchString(loc.Path) {
			return fmt.Errorf("location %q must be a path starting with /", loc.Path)
		}
		if seenLocation[loc.Path] {
			if loc.Path == "/" {
				return fmt.Errorf("everything not matched by another path already goes to the site's main upstream — remove the location for /")
			}
			return fmt.Errorf("two locations both handle %s; nginx would use the first and ignore the second", loc.Path)
		}
		seenLocation[loc.Path] = true
		if loc.Upstream != "" {
			if err := validUpstream(loc.Upstream); err != nil {
				return err
			}
		} else if loc.Root != "" {
			if !absPathRe.MatchString(loc.Root) {
				return fmt.Errorf("location %s: root must be an absolute path", loc.Path)
			}
		} else {
			return fmt.Errorf("location %s needs either an upstream or a root", loc.Path)
		}
	}
	if strings.Count(spec.Custom, "{") != strings.Count(spec.Custom, "}") {
		return fmt.Errorf("the extra configuration has unbalanced braces")
	}
	return nil
}

func validUpstream(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("an upstream address is required, for example http://127.0.0.1:3000")
	}
	if strings.ContainsAny(raw, " \t\n;{}") {
		return fmt.Errorf("the upstream address contains characters that are not allowed")
	}
	if strings.HasPrefix(raw, "unix:") {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("the upstream must look like http://127.0.0.1:3000")
	}
	if port := u.Port(); port != "" {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("the upstream port is not valid")
		}
	}
	return nil
}

func validRedirect(raw string) error {
	raw = strings.TrimSpace(raw)
	if strings.ContainsAny(raw, " \t\n;{}\"") {
		return fmt.Errorf("the redirect target contains characters that are not allowed")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("the redirect target must be a full URL, for example https://example.com")
	}
	return nil
}

func validACLEntry(entry string) error {
	entry = strings.TrimSpace(entry)
	if entry == "all" {
		return nil
	}
	if _, _, err := net.ParseCIDR(entry); err == nil {
		return nil
	}
	if net.ParseIP(entry) != nil {
		return nil
	}
	return fmt.Errorf("%q is not an IP address, a CIDR, or the word all", entry)
}

// SpecWarnings are the choices that are legal and probably not what was meant.
//
// The same idea as dockerx's toEngine warnings: refusing them would be wrong,
// because each has a legitimate use, and staying silent would be worse.
func SpecWarnings(spec *SiteSpec) []string {
	warnings := []string{}
	if !spec.TLS && spec.Kind != "redirect" {
		warnings = append(warnings,
			"Without TLS this site is served in plain text, and anything typed into it crosses the network readable. Issue a certificate from the Certificates tab and turn TLS on.")
	}
	if spec.TLS && !spec.ForceHTTPS {
		warnings = append(warnings,
			"Plain HTTP still serves this site. A visitor who types the bare domain gets the unencrypted version.")
	}
	if spec.HSTS && !spec.TLS {
		warnings = append(warnings,
			"HSTS is ignored on a plain-HTTP site — browsers only honour the header when it arrives over TLS.")
	}
	if spec.Kind == "proxy" && isPublicUpstream(spec.Upstream) {
		warnings = append(warnings,
			"The upstream is not on this machine. That is fine for a gateway, and a mistake if you meant 127.0.0.1.")
	}
	if spec.WebSockets && spec.ProxyTimeout > 0 && spec.ProxyTimeout < 60 {
		warnings = append(warnings,
			"A short read timeout closes idle WebSocket connections. Sixty seconds or more is usual for anything long-lived.")
	}
	if spec.Kind != "proxy" && len(spec.Locations) > 0 {
		warnings = append(warnings,
			"Extra paths are only applied to a site that forwards to an application. On this one they are not written into the config at all.")
	}
	if spec.BasicAuthFile != "" && spec.Kind == "redirect" {
		// Same reason the allow list does not restrict a redirect: `return`
		// is answered in the rewrite phase, before the access phase where
		// auth_basic lives, so the password prompt never appears.
		warnings = append(warnings,
			"A redirect is answered before nginx checks the password, so this site will not prompt for one. Put the password on whatever the redirect points at.")
	}
	if len(spec.AllowFrom) > 0 {
		warnings = append(warnings,
			"Only the listed addresses will reach this site. Everything else is refused — check the list includes however you reach it yourself.")
		if spec.Kind == "redirect" {
			// nginx runs `return` in the rewrite phase, which is before the
			// access phase allow and deny live in, so a redirect answers
			// before the address is ever checked. Said plainly rather than
			// rendered anyway: an access control that is silently skipped is
			// worse than one the operator was told not to rely on.
			warnings = append(warnings,
				"Except on a redirect: nginx answers a redirect before it checks the address, so the allow list will not restrict this site. Put the restriction on whatever the redirect points at.")
		}
	}
	return warnings
}

func isPublicUpstream(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "" || host == "localhost" {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// A hostname: could be anything, and a container name is the common
		// case. Not worth a warning.
		return false
	}
	return !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast()
}

func containsDenyAll(list []string) bool {
	for _, entry := range list {
		if strings.TrimSpace(entry) == "all" {
			return true
		}
	}
	return false
}
