package proxysvc

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// The TLS report's HTTP request, seen whole: every header the site answered
// with, and the rules that read them. The rules are pure functions of the
// headers, so what they say can be tested on crafted responses without a
// server.

// RequestShape is the HTTPS request a scan makes: its method, path and Host.
// The zero value is GET / with the scanned name as Host. SNI always names the
// scanned name, whatever Host says, so the certificate graded is the one a
// visitor to that name gets.
type RequestShape struct {
	Method string
	Path   string
	Host   string
}

// auditMethods are the methods a scan may send. A scan is a read, so it asks
// only what reading asks; a POST reaches the application, and belongs to the
// request tester, which audits it.
var auditMethods = map[string]bool{http.MethodGet: true, http.MethodHead: true, http.MethodOptions: true}

var hostHeaderRe = regexp.MustCompile(`^(\[[0-9a-f:.]+\]|[a-z0-9._-]+)(:[0-9]{1,5})?$`)

// ParseRequestShape reads the scan's ?method=, ?path= and ?host=, refusing
// anything a request line or a Host header could not carry as it is.
func ParseRequestShape(method, path, host string) (RequestShape, error) {
	shape := RequestShape{Method: strings.ToUpper(strings.TrimSpace(method))}
	if shape.Method == "" {
		shape.Method = http.MethodGet
	}
	if !auditMethods[shape.Method] {
		return RequestShape{}, fmt.Errorf("method %q is not one a scan sends: GET, HEAD or OPTIONS", method)
	}
	if path = strings.TrimSpace(path); path != "" && path != "/" {
		if len(path) > 1024 {
			return RequestShape{}, errors.New("path is longer than 1024 characters")
		}
		parsed, err := url.ParseRequestURI(path)
		if err != nil || !strings.HasPrefix(path, "/") || parsed.Host != "" || strings.ContainsAny(path, " \t\r\n#") {
			return RequestShape{}, fmt.Errorf("path %q is not a path: it starts with / and has no spaces or fragment", path)
		}
		shape.Path = path
	}
	if host = strings.ToLower(strings.TrimSpace(host)); host != "" {
		if len(host) > 255 || !hostHeaderRe.MatchString(host) {
			return RequestShape{}, fmt.Errorf("host %q is not a name or address, with a port or without", host)
		}
		shape.Host = host
	}
	return shape, nil
}

// requestPath is the path the request asks for.
func (s RequestShape) requestPath() string {
	if s.Path == "" {
		return "/"
	}
	return s.Path
}

func (s RequestShape) method() string {
	if s.Method == "" {
		return http.MethodGet
	}
	return s.Method
}

// ResponseHeader is one header line as it was answered. A Set-Cookie's value
// is replaced before it leaves the scan, since a session cookie is a secret
// and the report is copied into tickets; its name and attributes stay,
// because they are what the cookie rules judge.
type ResponseHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// The caps keep a hostile or broken site from filling the report: a few
// thousand headers, or one of a megabyte, say nothing a hundred would not.
const (
	maxAuditHeaders     = 150
	maxAuditHeaderValue = 2048
	redactedCookieValue = "<redacted>"
)

// responseHeaders lists every header in h by name, repeated headers in the
// order they came, with cookie values redacted and each value capped. The
// bool is whether any were left out.
func responseHeaders(h http.Header) ([]ResponseHeader, bool) {
	names := make([]string, 0, len(h))
	for name := range h {
		names = append(names, name)
	}
	sort.Strings(names)
	out := []ResponseHeader{}
	for _, name := range names {
		for _, value := range h[name] {
			if len(out) == maxAuditHeaders {
				return out, true
			}
			if strings.EqualFold(name, "Set-Cookie") {
				value = redactCookie(value)
			}
			if len(value) > maxAuditHeaderValue {
				value = value[:maxAuditHeaderValue] + "…"
			}
			out = append(out, ResponseHeader{Name: name, Value: value})
		}
	}
	return out, false
}

// redactCookie is a Set-Cookie line with its value replaced.
func redactCookie(line string) string {
	pair, attrs, hasAttrs := strings.Cut(line, ";")
	name, _, _ := strings.Cut(pair, "=")
	out := strings.TrimSpace(name) + "=" + redactedCookieValue
	if hasAttrs {
		out += ";" + attrs
	}
	return out
}

// setCookie is what the rules need of a Set-Cookie: its name and attributes.
type setCookie struct {
	name     string
	secure   bool
	httpOnly bool
	sameSite string
}

func parseSetCookie(line string) setCookie {
	parts := strings.Split(line, ";")
	name, _, _ := strings.Cut(parts[0], "=")
	c := setCookie{name: strings.TrimSpace(name)}
	for _, part := range parts[1:] {
		key, value, _ := strings.Cut(strings.TrimSpace(part), "=")
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "secure":
			c.secure = true
		case "httponly":
			c.httpOnly = true
		case "samesite":
			c.sameSite = strings.ToLower(strings.TrimSpace(value))
		}
	}
	return c
}

// versionRe finds a version number in a product token: nginx/1.26.0,
// PHP/8.3.4, Microsoft-IIS/10.0.
var versionRe = regexp.MustCompile(`/\s*v?\d`)

// poweredByHeaders name the application stack behind the proxy.
var poweredByHeaders = []string{"X-Powered-By", "X-AspNet-Version", "X-AspNetMvc-Version", "X-Generator"}

// compressibleTypes are the content types worth compressing: text, which
// shrinks by two thirds, as against images and archives, which are already
// compressed.
var compressibleTypes = []string{"text/", "application/javascript", "application/json", "application/xml", "application/rss+xml", "application/atom+xml", "image/svg+xml", "application/manifest+json"}

// auditResponse is the findings a response's headers support. method is the
// method asked, since a HEAD or OPTIONS answer has no body to be compressed.
func auditResponse(method string, h http.Header) []ScanFinding {
	var out []ScanFinding
	add := func(f ScanFinding) { out = append(out, f) }

	if server := h.Get("Server"); versionRe.MatchString(server) {
		f := ScanFinding{ID: "http.server-version", Level: "notice",
			Title:  "The Server header gives its version",
			Detail: "Server: " + server + ". A version tells a scanner which published vulnerabilities to try first.",
			Advice: "Name the product without the version."}
		// nginx replaces the upstream's Server with its own, so an nginx
		// version here is nginx's to hide, and server_tokens is where.
		if strings.HasPrefix(strings.ToLower(server), "nginx/") {
			f.Fix = FixServerTokens
			f.Advice = "Set server_tokens off; in the http block of nginx.conf, or in this site's server block. nginx then sends Server: nginx and leaves the version off its error pages too."
		}
		add(f)
	}
	var disclosed []string
	for _, name := range poweredByHeaders {
		if value := h.Get(name); value != "" {
			disclosed = append(disclosed, name+": "+value)
		}
	}
	if len(disclosed) > 0 {
		add(ScanFinding{ID: "http.powered-by", Level: "notice",
			Title:  "The response names the application's stack",
			Detail: strings.Join(disclosed, " · ") + ".",
			Advice: "Turn it off in the application (expose_php = Off, app.disable('x-powered-by')), or have nginx drop it with proxy_hide_header X-Powered-By; in the location that proxies to it."})
	}

	auditCookies(h.Values("Set-Cookie"), add)
	auditCSP(h, add)

	if method == http.MethodGet && h.Get("Content-Encoding") == "" && compressible(h.Get("Content-Type")) {
		// A body under a kilobyte gains little and nginx's own gzip_min_length
		// skips small ones, so only a larger or unknown length is reported.
		length, err := strconv.Atoi(h.Get("Content-Length"))
		if err != nil || length >= 1024 {
			detail := "A " + mediaType(h.Get("Content-Type")) + " response came back uncompressed although the request accepted gzip and br."
			if err == nil {
				detail = fmt.Sprintf("A %s response of %d bytes came back uncompressed although the request accepted gzip and br.", mediaType(h.Get("Content-Type")), length)
			}
			add(ScanFinding{ID: "http.uncompressed", Level: "notice",
				Title:  "Text is sent uncompressed",
				Detail: detail,
				Advice: "gzip on; compresses text/html only until gzip_types names the rest: gzip_types text/css application/javascript application/json image/svg+xml; Add gzip_proxied any; when an application behind the proxy answers."})
		}
	}

	if origin := strings.TrimSpace(h.Get("Access-Control-Allow-Origin")); origin == "*" &&
		strings.EqualFold(strings.TrimSpace(h.Get("Access-Control-Allow-Credentials")), "true") {
		add(ScanFinding{ID: "http.cors-wildcard-credentials", Level: "warning",
			Title:  "CORS allows any origin with credentials",
			Detail: "Access-Control-Allow-Origin: * with Access-Control-Allow-Credentials: true. Browsers refuse this pair, so credentialed cross-origin requests fail; it is also what a configuration looks like just before someone \"fixes\" it by echoing back whatever Origin is sent, which lets any site read the responses of a logged-in visitor.",
			Advice: "Name the origins that may call with credentials, or drop Access-Control-Allow-Credentials when none need to."})
	}
	return out
}

func compressible(contentType string) bool {
	media := mediaType(contentType)
	for _, prefix := range compressibleTypes {
		if strings.HasPrefix(media, prefix) {
			return true
		}
	}
	return false
}

func mediaType(contentType string) string {
	media, _, _ := strings.Cut(contentType, ";")
	return strings.ToLower(strings.TrimSpace(media))
}

// auditCookies reports the cookies missing each attribute, by name, one
// finding per attribute so each reads as one fix.
func auditCookies(lines []string, add func(ScanFinding)) {
	var insecure, script, sameSite, noneInsecure []string
	for _, line := range lines {
		c := parseSetCookie(line)
		if c.name == "" {
			continue
		}
		if !c.secure {
			insecure = append(insecure, c.name)
			if c.sameSite == "none" {
				noneInsecure = append(noneInsecure, c.name)
			}
		}
		if !c.httpOnly {
			script = append(script, c.name)
		}
		if c.sameSite == "" {
			sameSite = append(sameSite, c.name)
		}
	}
	if len(insecure) > 0 {
		detail := "Without Secure, " + cookieList(insecure) + " is sent over plain HTTP too, where anyone on the path can read it."
		if len(noneInsecure) > 0 {
			detail += " Browsers drop SameSite=None without Secure outright, so " + cookieList(noneInsecure) + " is not stored at all."
		}
		add(ScanFinding{ID: "http.cookie-secure", Level: "warning",
			Title: "A cookie is set without Secure", Detail: detail,
			Advice: "Set Secure on every cookie a site served over HTTPS sets. It is the application's setting (session.cookie_secure, SESSION_COOKIE_SECURE); nginx can add it with proxy_cookie_flags ~ secure; in the proxying location."})
	}
	if len(script) > 0 {
		add(ScanFinding{ID: "http.cookie-httponly", Level: "notice",
			Title:  "A cookie can be read by script",
			Detail: "Without HttpOnly, " + cookieList(script) + " can be read by any script on the page, including an injected one.",
			Advice: "Set HttpOnly on session and authentication cookies. A cookie the page's own script must read, such as a CSRF token, is the exception. nginx can add it with proxy_cookie_flags <name> httponly;"})
	}
	if len(sameSite) > 0 {
		add(ScanFinding{ID: "http.cookie-samesite", Level: "notice",
			Title:  "A cookie leaves SameSite to the browser",
			Detail: cookieList(sameSite) + " sets no SameSite. Chrome treats that as Lax; other browsers have not always, so whether it goes with requests from other sites depends on who is visiting.",
			Advice: "Say SameSite=Lax (or Strict) explicitly. nginx can add it with proxy_cookie_flags <name> samesite=lax;"})
	}
}

func cookieList(names []string) string {
	quoted := make([]string, 0, len(names))
	seen := map[string]bool{}
	for _, name := range names {
		if !seen[name] {
			seen[name] = true
			quoted = append(quoted, name)
		}
	}
	if len(quoted) == 1 {
		return "the cookie " + quoted[0]
	}
	return "the cookies " + strings.Join(quoted, ", ")
}

// auditCSP reads the policy for what makes it weaker than it looks. It says
// nothing of a site with no policy at all: the header list already does.
func auditCSP(h http.Header, report func(ScanFinding)) {
	// Each weakness is one finding however many policies share it, since a
	// finding's ID is its anchor on the page.
	seen := map[string]bool{}
	add := func(f ScanFinding) {
		if !seen[f.ID] {
			seen[f.ID] = true
			report(f)
		}
	}
	policy := strings.Join(h.Values("Content-Security-Policy"), ", ")
	if policy == "" {
		if h.Get("Content-Security-Policy-Report-Only") != "" {
			add(ScanFinding{ID: "http.csp-report-only", Level: "notice",
				Title:  "The Content-Security-Policy only reports",
				Detail: "Content-Security-Policy-Report-Only is set and Content-Security-Policy is not, so violations are reported and nothing is blocked.",
				Advice: "Once the reports are quiet, send the same policy as Content-Security-Policy."})
		}
		return
	}
	// Several policies all apply; the script sources of each are judged.
	for _, one := range strings.Split(policy, ",") {
		directives := parseCSP(one)
		sources, named := directives["script-src"]
		if !named {
			sources, named = directives["default-src"]
		}
		if !named {
			add(ScanFinding{ID: "http.csp-no-script-limit", Level: "notice",
				Title:  "The Content-Security-Policy does not limit scripts",
				Detail: "The policy (" + strings.TrimSpace(one) + ") has neither script-src nor default-src, so any script may run.",
				Advice: "Add default-src 'self' or a script-src, which is what the policy is mostly for."})
			continue
		}
		// A nonce or hash makes browsers ignore 'unsafe-inline', which
		// policies keep for older browsers.
		guarded := false
		for _, s := range sources {
			if strings.HasPrefix(s, "'nonce-") || strings.HasPrefix(s, "'sha256-") ||
				strings.HasPrefix(s, "'sha384-") || strings.HasPrefix(s, "'sha512-") || s == "'strict-dynamic'" {
				guarded = true
			}
		}
		var broad []string
		for _, s := range sources {
			switch {
			case s == "'unsafe-inline'" && !guarded:
				add(ScanFinding{ID: "http.csp-unsafe-inline", Level: "notice",
					Title:  "The Content-Security-Policy allows inline script",
					Detail: "Its script sources include 'unsafe-inline' with no nonce or hash, so an injected <script> runs: the attack the policy exists to stop.",
					Advice: "Move inline scripts into files, or give each a nonce or hash and list that instead."})
			case s == "'unsafe-eval'":
				add(ScanFinding{ID: "http.csp-unsafe-eval", Level: "notice",
					Title:  "The Content-Security-Policy allows eval",
					Detail: "Its script sources include 'unsafe-eval', so a string that reaches eval() or new Function() runs as code.",
					Advice: "Remove it unless a library needs it; most that did have a build that does not."})
			case s == "*" || s == "http:" || s == "https:" || s == "data:" || s == "blob:":
				broad = append(broad, s)
			}
		}
		if len(broad) > 0 {
			add(ScanFinding{ID: "http.csp-broad-source", Level: "notice",
				Title:  "The Content-Security-Policy allows scripts from anywhere",
				Detail: "Its script sources include " + strings.Join(broad, " ") + ", which any attacker can host a script at.",
				Advice: "List the hosts scripts actually come from, or use nonces with 'strict-dynamic'."})
		}
	}
}

// parseCSP splits one policy into its directives, names lowercased, sources
// as written. The first of a repeated directive is the one browsers use.
func parseCSP(policy string) map[string][]string {
	out := map[string][]string{}
	for _, directive := range strings.Split(policy, ";") {
		fields := strings.Fields(directive)
		if len(fields) == 0 {
			continue
		}
		name := strings.ToLower(fields[0])
		if _, seen := out[name]; seen {
			continue
		}
		sources := make([]string, 0, len(fields)-1)
		for _, s := range fields[1:] {
			sources = append(sources, strings.ToLower(s))
		}
		out[name] = sources
	}
	return out
}
