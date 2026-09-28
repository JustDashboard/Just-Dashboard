package proxysvc

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"slices"
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
	// Pool, when set, replaces Upstream with several servers.
	Pool *SitePool `json:"pool,omitempty"`
	Root string    `json:"root,omitempty"`
	// SPA answers a path with no file of its own with index.html, for a
	// single-page app whose router runs in the browser. Static sites only:
	// without it a deep link or a reload on /settings is nginx's 404.
	SPA bool `json:"spa,omitempty"`
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
	// Buffering lets nginx hold a response until it has it, which frees
	// a slow application sooner. Off by default here, because streamed
	// responses (server-sent events, progress output) must arrive as they
	// are produced.
	Buffering bool `json:"buffering,omitempty"`
	// StreamUploads hands a request body to the application as it arrives
	// rather than after nginx has read all of it: proxy_request_buffering
	// off. Named for what it does so that its zero value is nginx's default.
	StreamUploads bool `json:"streamUploads,omitempty"`
	// HostHeader is the Host the application is sent: empty is the one the
	// visitor asked for, "upstream" is the upstream's own name, and
	// "custom" is HostHeaderValue.
	HostHeader      string `json:"hostHeader,omitempty"`
	HostHeaderValue string `json:"hostHeaderValue,omitempty"`
	// UpstreamSNI sends the upstream's name in the TLS handshake, which
	// nginx does not do by default; a host serving several names over
	// HTTPS answers a handshake without one with its default certificate
	// or refuses it.
	UpstreamSNI bool `json:"upstreamSni,omitempty"`
	// UpstreamVerify checks the upstream's certificate, against UpstreamCA
	// or the system's CAs when that is empty. nginx does not by default.
	UpstreamVerify bool   `json:"upstreamVerify,omitempty"`
	UpstreamCA     string `json:"upstreamCa,omitempty"`
	// UpstreamTLSName replaces the upstream's host as the name sent and
	// checked, for an upstream addressed by IP.
	UpstreamTLSName string `json:"upstreamTlsName,omitempty"`

	AllowFrom      []string `json:"allowFrom"`
	DenyFrom       []string `json:"denyFrom"`
	BasicAuthFile  string   `json:"basicAuthFile,omitempty"`
	BasicAuthRealm string   `json:"basicAuthRealm,omitempty"`

	AccessLog bool `json:"accessLog"`
	// AccessLogPath and ErrorLogPath are the files the site's access_log and
	// error_log write to, read back from the file rather than set by the
	// form: a hand-written site logs wherever its author said, and a page that
	// guessed the managed spelling for it read an empty file and reported a
	// site nobody visits. Empty when the site logs nowhere of its own — off,
	// syslog, stderr, or no directive and so nginx's shared log.
	AccessLogPath string `json:"accessLogPath,omitempty"`
	ErrorLogPath  string `json:"errorLogPath,omitempty"`
	// LogFormat is "timed" (combined plus the request time, which traffic
	// analytics reads latency from) or "combined", nginx's stock format.
	// Empty is timed, the default for a new site.
	LogFormat string         `json:"logFormat,omitempty"`
	Locations []SiteLocation `json:"locations"`
	// Maintenance, when set, is the site's maintenance page and who gets past
	// it; On is whether visitors get it now. Kept while off, so turning it
	// back on keeps the addresses and the retry time.
	Maintenance *SiteMaintenance `json:"maintenance,omitempty"`
	// ErrorPages are the codes nginx answers with the site's own page
	// rather than its stock one: 404, 502, 503 or 504.
	ErrorPages []int `json:"errorPages,omitempty"`
	// InterceptErrors also replaces the application's own responses with
	// those codes, not only the ones nginx produces. Proxy sites only.
	InterceptErrors bool `json:"interceptErrors,omitempty"`
	// Limits caps the requests and connections one client may make.
	Limits *SiteLimits `json:"limits,omitempty"`
	// StaticCache has browsers keep the site's static files; ProxyCache
	// keeps the application's responses on disk. Nil sets neither.
	StaticCache *StaticCache `json:"staticCache,omitempty"`
	ProxyCache  *ProxyCache  `json:"proxyCache,omitempty"`
	// PagesDir is where the site's pages are served from. The service sets
	// it from its own nginx directory; it is never taken from a request.
	PagesDir string `json:"-"`
	// Custom is appended verbatim inside the server block. It is the escape
	// hatch, and it is the one field not validated beyond refusing an
	// unbalanced brace — a form that cannot express everything needs
	// somewhere to put the rest.
	Custom string `json:"custom,omitempty"`
}

// SiteLocation is an extra path handled differently from the site's default.
type SiteLocation struct {
	Path string `json:"path"`
	// Match is how nginx compares Path: empty is a prefix, "=" the exact
	// path, "^~" a prefix that wins over every regex, and "~" or "~*" a
	// regex, case-sensitive or not.
	Match    string `json:"match,omitempty"`
	Upstream string `json:"upstream,omitempty"`
	// StripPrefix forwards /api/users as /users: the path and the
	// upstream both end in a slash, which is how nginx is told to swap one
	// for the other.
	StripPrefix bool `json:"stripPrefix,omitempty"`
	// Root is a folder served at Path: /assets/app.css is <Root>/app.css.
	Root string `json:"root,omitempty"`
	// RootMode "root" keeps nginx's own reading of a folder, read back from a
	// file that used `root`: the path is appended to the folder, so
	// /assets/app.css is <Root>/assets/app.css. Rewriting such a location as
	// the folder itself would move every file it serves.
	RootMode string `json:"rootMode,omitempty"`
	// SPA answers a path under the folder that has no file of its own with
	// the folder's index.html.
	SPA        bool `json:"spa,omitempty"`
	WebSockets bool `json:"webSockets"`
	// BodyLimit, Timeout and Buffering replace the site's own on this path;
	// empty or 0 keeps the site's. Buffering and RequestBuffering are "on"
	// or "off".
	BodyLimit        string `json:"bodyLimit,omitempty"`
	Timeout          int    `json:"timeout,omitempty"`
	Buffering        string `json:"buffering,omitempty"`
	RequestBuffering string `json:"requestBuffering,omitempty"`
	// BasicAuthFile, AllowFrom and DenyFrom replace the site's for this
	// path, as nginx reads them: a location's own list is the whole list.
	BasicAuthFile string   `json:"basicAuthFile,omitempty"`
	AllowFrom     []string `json:"allowFrom,omitempty"`
	DenyFrom      []string `json:"denyFrom,omitempty"`
	// RateLimit replaces the site's request rate on this path.
	RateLimit *RequestLimit `json:"rateLimit,omitempty"`
}

// SiteMaintenance is a site's maintenance switch.
type SiteMaintenance struct {
	On bool `json:"on"`
	// RetryAfter is the seconds the 503 tells clients and crawlers to wait;
	// 0 sends no Retry-After.
	RetryAfter int `json:"retryAfter,omitempty"`
	// BypassFrom are the addresses that reach the site as usual while it is
	// in maintenance.
	BypassFrom []string `json:"bypassFrom"`
}

// errorPageCodes are the codes a site may give a page of its own, in the
// order they are written.
var errorPageCodes = []int{404, 502, 503, 504}

// maintVar and maintIPVar name the site's maintenance map and geo.
func (spec *SiteSpec) maintVar() string { return NginxIdent(spec.Name) + "_maint" }

func (spec *SiteSpec) maintIPVar() string { return NginxIdent(spec.Name) + "_maint_ip" }

// hasErrorPage says whether the site serves its own page for code.
func (spec *SiteSpec) hasErrorPage(code int) bool {
	for _, c := range spec.ErrorPages {
		if c == code {
			return true
		}
	}
	return false
}

// usesPages says whether the rendered site serves any page from PagesDir.
func (spec *SiteSpec) usesPages() bool {
	return spec.Maintenance != nil || len(spec.ErrorPages) > 0
}

// timedLog says whether the access log is written in the site's timed format.
func (spec *SiteSpec) timedLog() bool {
	return spec.LogFormat != "combined"
}

// logFormatName and connectionVar name the http-level objects the site owns.
func (spec *SiteSpec) logFormatName() string { return NginxIdent(spec.Name) + "_timed" }

func (spec *SiteSpec) connectionVar() string { return NginxIdent(spec.Name) + "_connection" }

// usesWebSockets says whether any location the renderer writes forwards
// upgrades, which is when the site's connection map is needed.
func (spec *SiteSpec) usesWebSockets() bool {
	if spec.Kind != "proxy" {
		return false
	}
	if spec.WebSockets {
		return true
	}
	for _, loc := range spec.Locations {
		if loc.WebSockets && !loc.servesFolder() {
			return true
		}
	}
	return false
}

// servesFolder says whether a location serves files rather than forwarding.
func (loc SiteLocation) servesFolder() bool {
	return loc.Upstream == "" && loc.Root != ""
}

// isPrefix says whether the location matches a path prefix, the only kind
// a folder can be served at or a prefix stripped from.
func (loc SiteLocation) isPrefix() bool { return loc.Match == "" || loc.Match == "^~" }

// isRegex says whether Path is a regular expression.
func (loc SiteLocation) isRegex() bool { return loc.Match == "~" || loc.Match == "~*" }

// renderedPath is the path the location block is written for. A folder served
// at its path ends in a slash, and so does the folder: `location /assets`
// with `alias /var/www/assets` would also answer /assets-private/key.pem from
// /var/www/assets-private, which is a neighbouring folder nobody offered. A
// stripped prefix ends in one for the same reason, and because the slash is
// what nginx swaps for the upstream's.
func (loc SiteLocation) renderedPath() string {
	if strings.HasSuffix(loc.Path, "/") || !loc.isPrefix() {
		return loc.Path
	}
	if (loc.servesFolder() && loc.RootMode == "") || (loc.StripPrefix && !loc.servesFolder()) {
		return loc.Path + "/"
	}
	return loc.Path
}

// renderedUpstream is the upstream as the location forwards to it. With the
// prefix stripped it ends in a slash, the one the path's is swapped for.
func (loc SiteLocation) renderedUpstream() string {
	if !loc.StripPrefix || loc.servesFolder() {
		return loc.Upstream
	}
	address, uri := splitUpstream(loc.Upstream)
	if strings.HasSuffix(uri, "/") {
		return loc.Upstream
	}
	if uri == "" && strings.HasPrefix(address, "unix:") {
		return address + ":/"
	}
	return address + uri + "/"
}

// blockKey is what nginx counts as the same location twice: a plain prefix
// and a ^~ one of the same path are one block to it.
func (loc SiteLocation) blockKey() string {
	if loc.isPrefix() {
		return loc.renderedPath()
	}
	return loc.Match + " " + loc.Path
}

// managedMarker is written into every generated file and read back when the
// form loads one. A file without it was written by hand, and the form says so
// rather than silently offering to overwrite somebody's work.
const managedMarker = "# Managed by Just Dashboard."

var (
	siteNameRe     = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	domainRe       = regexp.MustCompile(`^(\*\.)?[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)
	bodySizeRe     = regexp.MustCompile(`^\d{1,6}[kKmMgG]?$`)
	locationPathRe = regexp.MustCompile(`^/[A-Za-z0-9._~!&*+,=:@%/-]{0,255}$`)
	// A regex path keeps its own punctuation; what it may not carry is what
	// would end the directive or need quoting: whitespace, ; { } quotes, #.
	locationRegexRe = regexp.MustCompile(`^[^\s;{}"'#]{1,255}$`)
	hostHeaderRe    = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,252})(:\d{1,5})?$`)
	absPathRe       = regexp.MustCompile(`^/[A-Za-z0-9._/-]{1,255}$`)
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
	seenDomain := map[string]bool{}
	for _, d := range spec.Domains {
		if !domainRe.MatchString(d) {
			return fmt.Errorf("%q is not a valid domain name", d)
		}
		// nginx lowercases server names and warns that the second copy is
		// "conflicting" and ignored, which reads as another site owning it.
		if seenDomain[strings.ToLower(d)] {
			return fmt.Errorf("%s is listed twice", d)
		}
		seenDomain[strings.ToLower(d)] = true
	}
	switch spec.Kind {
	case "proxy":
		if spec.Pool == nil {
			if err := validUpstream(spec.Upstream); err != nil {
				return err
			}
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
	if spec.LogFormat != "" && spec.LogFormat != "timed" && spec.LogFormat != "combined" {
		return fmt.Errorf("log format must be timed or combined")
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
		if err := validLocation(loc); err != nil {
			return err
		}
		// Compared as written, since a folder's path gains its slash there:
		// /assets and /assets/ as two folders are one block nginx refuses.
		if seenLocation[loc.blockKey()] {
			if loc.Path == "/" && loc.isPrefix() {
				return fmt.Errorf("everything not matched by another path already goes to the site's main upstream — remove the location for /")
			}
			return fmt.Errorf("two locations both handle %s; nginx would use the first and ignore the second", loc.renderedPath())
		}
		seenLocation[loc.blockKey()] = true
	}
	if err := validUpstreamTLS(spec); err != nil {
		return err
	}
	if err := validatePool(spec); err != nil {
		return err
	}
	switch spec.HostHeader {
	case "", "upstream":
		if spec.HostHeaderValue != "" {
			return fmt.Errorf("a Host header value is only used with the custom Host header")
		}
	case "custom":
		if !hostHeaderRe.MatchString(spec.HostHeaderValue) {
			return fmt.Errorf("the Host header must be a host name, optionally with a port")
		}
	default:
		return fmt.Errorf("the Host header must be the visitor's, the upstream's or a custom one")
	}
	if m := spec.Maintenance; m != nil {
		if m.RetryAfter < 0 || m.RetryAfter > 86400 {
			return fmt.Errorf("retry after must be between 0 and 86400 seconds")
		}
		for _, entry := range m.BypassFrom {
			if err := validAddress(entry); err != nil {
				return err
			}
		}
	}
	seenCode := map[int]bool{}
	for _, code := range spec.ErrorPages {
		if !slices.Contains(errorPageCodes, code) {
			return fmt.Errorf("an error page can be set for 404, 502, 503 or 504, not %d", code)
		}
		if seenCode[code] {
			return fmt.Errorf("the %d page is listed twice", code)
		}
		seenCode[code] = true
	}
	if err := validateLimits(spec); err != nil {
		return err
	}
	if err := validateCache(spec); err != nil {
		return err
	}
	if spec.PagesDir != "" && !absPathRe.MatchString(spec.PagesDir) {
		return fmt.Errorf("the pages directory must be an absolute path")
	}
	if strings.Count(spec.Custom, "{") != strings.Count(spec.Custom, "}") {
		return fmt.Errorf("the extra configuration has unbalanced braces")
	}
	return nil
}

// validLocation checks one extra path on its own.
func validLocation(loc SiteLocation) error {
	switch loc.Match {
	case "", "=", "^~":
		if !locationPathRe.MatchString(loc.Path) {
			return fmt.Errorf("location %q must be a path starting with /", loc.Path)
		}
	case "~", "~*":
		if !locationRegexRe.MatchString(loc.Path) {
			return fmt.Errorf("the regex %q may not contain spaces, semicolons, braces, quotes or #", loc.Path)
		}
	default:
		return fmt.Errorf("location %s: the match must be a prefix, =, ^~, ~ or ~*", loc.Path)
	}
	if loc.RootMode != "" && loc.RootMode != "root" {
		return fmt.Errorf("location %s: rootMode must be empty or root", loc.Path)
	}
	if loc.Upstream != "" {
		if err := validUpstream(loc.Upstream); err != nil {
			return err
		}
		// nginx refuses the file outright: a regex match has no prefix
		// for a path on the upstream to replace.
		if _, uri := splitUpstream(loc.Upstream); loc.isRegex() && uri != "" {
			return fmt.Errorf("location %s: a regex path forwards to an upstream without a path of its own", loc.Path)
		}
		if loc.StripPrefix && !loc.isPrefix() {
			return fmt.Errorf("location %s: only a prefix path can be stripped", loc.Path)
		}
		if loc.StripPrefix && loc.Path == "/" {
			return fmt.Errorf("location %s: there is no prefix to strip from /", loc.Path)
		}
		if loc.SPA {
			return fmt.Errorf("location %s: the single-page fallback is for a folder", loc.Path)
		}
	} else if loc.Root != "" {
		if !absPathRe.MatchString(loc.Root) {
			return fmt.Errorf("location %s: root must be an absolute path", loc.Path)
		}
		// alias in an exact or regex location means something else (a file,
		// or captures), so there a folder is nginx's root: the path is
		// appended to it.
		if !loc.isPrefix() && loc.RootMode != "root" {
			return fmt.Errorf("location %s: an exact or regex path serves files with the path added to the folder", loc.Path)
		}
		if loc.SPA && !loc.isPrefix() {
			return fmt.Errorf("location %s: the single-page fallback needs a prefix path", loc.Path)
		}
		if loc.StripPrefix || loc.BodyLimit != "" || loc.Timeout != 0 || loc.Buffering != "" || loc.RequestBuffering != "" {
			return fmt.Errorf("location %s: stripping, upload limits, timeouts and buffering apply to a path that forwards", loc.Path)
		}
	} else {
		return fmt.Errorf("location %s needs either an upstream or a root", loc.Path)
	}
	if loc.BodyLimit != "" && !bodySizeRe.MatchString(loc.BodyLimit) {
		return fmt.Errorf("location %s: upload limit must be a size like 50m", loc.Path)
	}
	if loc.Timeout < 0 || loc.Timeout > 3600 {
		return fmt.Errorf("location %s: timeout must be between 0 and 3600 seconds", loc.Path)
	}
	for _, value := range []string{loc.Buffering, loc.RequestBuffering} {
		if value != "" && value != "on" && value != "off" {
			return fmt.Errorf("location %s: buffering is on, off or the site's", loc.Path)
		}
	}
	if loc.BasicAuthFile != "" && !absPathRe.MatchString(loc.BasicAuthFile) {
		return fmt.Errorf("location %s: the password file must be an absolute path", loc.Path)
	}
	for _, list := range [][]string{loc.AllowFrom, loc.DenyFrom} {
		for _, entry := range list {
			if err := validACLEntry(entry); err != nil {
				return fmt.Errorf("location %s: %w", loc.Path, err)
			}
		}
	}
	return nil
}

// validUpstreamTLS checks how the site talks to an HTTPS upstream. A name
// or a CA with nothing that uses it would be written and do nothing.
func validUpstreamTLS(spec *SiteSpec) error {
	if spec.UpstreamTLSName != "" {
		if !spec.UpstreamSNI && !spec.UpstreamVerify {
			return fmt.Errorf("the upstream TLS name is only used when the name is sent or the certificate checked")
		}
		if strings.HasPrefix(spec.UpstreamTLSName, "*") || !domainRe.MatchString(spec.UpstreamTLSName) {
			return fmt.Errorf("%q is not a valid upstream TLS name", spec.UpstreamTLSName)
		}
	}
	if spec.UpstreamCA != "" {
		if !spec.UpstreamVerify {
			return fmt.Errorf("a CA file is only used when the upstream's certificate is checked")
		}
		if !absPathRe.MatchString(spec.UpstreamCA) {
			return fmt.Errorf("the CA file must be an absolute path")
		}
	}
	return nil
}

// hasHTTPSUpstream says whether any address the site forwards to is HTTPS.
func (spec *SiteSpec) hasHTTPSUpstream() bool {
	if strings.HasPrefix(spec.mainUpstream(), "https://") {
		return true
	}
	for _, loc := range spec.Locations {
		if strings.HasPrefix(loc.Upstream, "https://") {
			return true
		}
	}
	return false
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

// validAddress is an IP address or a CIDR, as geo reads one.
func validAddress(entry string) error {
	if _, _, err := net.ParseCIDR(entry); err == nil {
		return nil
	}
	if net.ParseIP(entry) != nil {
		return nil
	}
	return fmt.Errorf("%q is not an IP address or a CIDR", entry)
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
	if spec.Kind == "proxy" {
		routes := append([]SiteLocation{}, spec.Locations...)
		for _, loc := range append(routes, SiteLocation{Path: "/", Upstream: spec.mainUpstream()}) {
			// An exact path is replaced whole and a regex one cannot carry
			// an upstream path, so the prefix swap below is a prefix's.
			if loc.Upstream == "" || !loc.isPrefix() {
				continue
			}
			for _, warning := range []string{
				upstreamSlashWarning(loc.renderedPath(), loc.renderedUpstream()),
				upstreamDecodeWarning(loc.renderedPath(), loc.renderedUpstream()),
			} {
				if warning != "" {
					warnings = append(warnings, warning)
				}
			}
		}
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
	if spec.Maintenance != nil && spec.Maintenance.On {
		warnings = append(warnings,
			"Maintenance is on: visitors get the maintenance page with a 503, except from the addresses allowed past it.")
	}
	if spec.InterceptErrors && spec.Kind != "proxy" {
		warnings = append(warnings,
			"Replacing the application's own error pages applies only to a site that forwards to an application; on this one only nginx's own errors get the site's pages.")
	}
	warnings = append(warnings, limitsWarnings(spec)...)
	warnings = append(warnings, poolWarnings(spec)...)
	warnings = append(warnings, cacheWarnings(spec)...)
	if spec.Kind == "proxy" && (spec.UpstreamSNI || spec.UpstreamVerify) && !spec.hasHTTPSUpstream() {
		warnings = append(warnings,
			"The upstream TLS settings apply only to an https:// upstream, and this site forwards to none.")
	}
	if spec.Kind == "proxy" && spec.hasHTTPSUpstream() && !spec.UpstreamVerify {
		warnings = append(warnings,
			"nginx does not check an HTTPS upstream's certificate unless told to, so anything answering at that address is trusted.")
	}
	if spec.Kind == "proxy" {
		for _, loc := range spec.Locations {
			if (len(loc.AllowFrom) > 0 || len(loc.DenyFrom) > 0) && (len(spec.AllowFrom) > 0 || len(spec.DenyFrom) > 0) {
				warnings = append(warnings, fmt.Sprintf(
					"%s has its own address list, and nginx uses only that one there: the site's list does not apply to it.", loc.Path))
			}
		}
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

// upstreamSlashWarning explains what an upstream with a path does to the
// requests a location forwards, when the two disagree about a trailing slash.
//
// The upstream is written exactly as typed, because its path is how nginx is
// told to swap the location's prefix for another: /api/ to http://x/ sends
// /api/users as /users. That swap is literal, so a slash on one side and not
// the other glues two words together or doubles a slash.
func upstreamSlashWarning(path, upstream string) string {
	u, err := url.Parse(upstream)
	if err != nil || strings.HasPrefix(upstream, "unix:") || u.Path == "" {
		return ""
	}
	if strings.HasSuffix(path, "/") == strings.HasSuffix(u.Path, "/") {
		return ""
	}
	request := strings.TrimSuffix(path, "/") + "/page"
	sent := u.Path + strings.TrimPrefix(request, path)
	return fmt.Sprintf("%s reaches the application as %s. End both the path and the upstream with a slash, or neither.",
		request, sent)
}

// upstreamDecodeWarning says what nginx does to the path it forwards to an
// upstream with a path of its own: it sends the path decoded and normalised
// rather than as the client sent it, so an encoded slash arrives as a slash.
// A path that is the location's own prefix is not written (proxyPassTarget),
// and is not warned about.
func upstreamDecodeWarning(path, upstream string) string {
	_, uri := splitUpstream(upstream)
	if uri == "" || uri == path {
		return ""
	}
	prefix := strings.TrimSuffix(path, "/")
	sent := uri + strings.TrimPrefix(prefix+"/a/b", path)
	return fmt.Sprintf("%s/a%%2Fb reaches the application as %s: an upstream with a path gets the request's path decoded. An application that needs %%2F kept needs the upstream without a path.",
		prefix, sent)
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
