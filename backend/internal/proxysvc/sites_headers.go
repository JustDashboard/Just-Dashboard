package proxysvc

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// SiteHeaders is what a site adds to and takes from the headers passing
// through it, and the browser policies it sends.
//
// Every value here is written inside double quotes, and none may carry a
// quote, a backslash, a semicolon, a brace, a dollar or a line break: the
// file is nginx configuration, and any of those would let a header value
// end the directive and start another. The policies that need quotes and
// semicolons of their own — a CSP's 'self' — are kept as parts, and the
// renderer puts the punctuation in.
type SiteHeaders struct {
	// Request are sent to the application with every request, after the
	// ones the renderer always sets. An empty value stops the visitor's
	// header of that name reaching it. Proxy sites only.
	Request []HeaderValue `json:"request,omitempty"`
	// Response are added to every answer, errors included.
	Response []HeaderValue `json:"response,omitempty"`
	// Hide are the application's response headers nginx drops. Proxy sites
	// only.
	Hide []string `json:"hide,omitempty"`
	// FrameOptions is "deny" or "sameorigin"; empty leaves X-Frame-Options
	// to the security headers switch.
	FrameOptions string   `json:"frameOptions,omitempty"`
	CSP          *SiteCSP `json:"csp,omitempty"`
	// Permissions is the Permissions-Policy, one feature at a time.
	Permissions []PermissionRule `json:"permissions,omitempty"`
	CORS        *SiteCORS        `json:"cors,omitempty"`
}

// HeaderValue is one header. A request header's value may instead be a
// single nginx variable, $name, and nothing else.
type HeaderValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// SiteCSP is a Content-Security-Policy. ReportOnly sends it as
// Content-Security-Policy-Report-Only, which browsers report against and do
// not enforce: the way to try a policy on a live site.
type SiteCSP struct {
	Directives []CSPDirective `json:"directives"`
	ReportOnly bool           `json:"reportOnly,omitempty"`
}

// CSPDirective is one directive and its sources. A keyword is kept without
// its quotes (self, none, unsafe-inline, nonce-…, sha256-…); the renderer
// writes them.
type CSPDirective struct {
	Name    string   `json:"name"`
	Sources []string `json:"sources,omitempty"`
}

// PermissionRule is one Permissions-Policy feature and who may use it:
// none, self or all.
type PermissionRule struct {
	Feature string `json:"feature"`
	Allow   string `json:"allow"`
}

// SiteCORS lets pages on other origins call the site from a browser.
type SiteCORS struct {
	// Origins are scheme://host[:port], or "*" alone for any origin.
	Origins []string `json:"origins"`
	Methods []string `json:"methods"`
	// Headers are the request headers a caller may send; empty allows
	// whichever the browser asks for.
	Headers []string `json:"headers,omitempty"`
	// Credentials lets the browser send cookies and HTTP auth along.
	Credentials bool `json:"credentials,omitempty"`
}

var (
	headerNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,63}$`)
	// A header value, and a CSP or Permissions-Policy part, is written in
	// double quotes; these are what could end the quotes or the directive.
	headerValueRe = regexp.MustCompile(`^[^"'\\$;{}\x00-\x1f\x7f]{0,1024}$`)
	nginxVarRe    = regexp.MustCompile(`^\$[A-Za-z_][A-Za-z0-9_]{0,63}$`)
	policyNameRe  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)
	cspSourceRe   = regexp.MustCompile(`^[A-Za-z0-9*:/._+=%?&~@!,-]{1,256}$`)
	corsOriginRe  = regexp.MustCompile(`^https?://[A-Za-z0-9]([A-Za-z0-9.-]{0,252})(:\d{1,5})?$`)
)

// corsMethods are the methods a site may allow across origins.
var corsMethods = []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}

// cspKeywords are the sources CSP spells in single quotes.
var cspKeywords = []string{
	"self", "none", "unsafe-inline", "unsafe-eval", "unsafe-hashes", "strict-dynamic",
	"report-sample", "wasm-unsafe-eval", "inline-speculation-rules",
}

var cspQuotedPrefixes = []string{"nonce-", "sha256-", "sha384-", "sha512-"}

func cspQuoted(source string) bool {
	if slices.Contains(cspKeywords, source) {
		return true
	}
	for _, prefix := range cspQuotedPrefixes {
		if strings.HasPrefix(source, prefix) {
			return true
		}
	}
	return false
}

// renderedRequestHeaders are the request headers renderLocation sets
// itself; a second proxy_set_header of one would send it twice.
var renderedRequestHeaders = []string{
	"Host", "X-Real-IP", "X-Forwarded-For", "X-Forwarded-Proto", "X-Forwarded-Host",
	"Upgrade", "Connection", "X-Client-Verify", "X-Client-Subject",
}

// nginxHiddenHeaders are the response headers nginx already drops from an
// upstream, so hiding one again would do nothing.
var nginxHiddenHeaders = []string{
	"Date", "Server", "X-Pad", "X-Accel-Expires", "X-Accel-Redirect", "X-Accel-Limit-Rate",
	"X-Accel-Buffering", "X-Accel-Charset",
}

// corsResponseHeaders are the CORS headers the renderer writes, and hides
// from the application so a browser is not sent two of each, which it
// refuses.
var corsResponseHeaders = []string{
	"Access-Control-Allow-Origin", "Access-Control-Allow-Credentials",
	"Access-Control-Allow-Methods", "Access-Control-Allow-Headers", "Access-Control-Max-Age",
}

// policyHeaders are the response headers with a control of their own.
var policyHeaders = []string{
	"X-Frame-Options", "Content-Security-Policy", "Content-Security-Policy-Report-Only",
	"Permissions-Policy", "Strict-Transport-Security",
}

func containsFold(list []string, name string) bool {
	return slices.ContainsFunc(list, func(s string) bool { return strings.EqualFold(s, name) })
}

func (spec *SiteSpec) corsVar() string      { return NginxIdent(spec.Name) + "_cors" }
func (spec *SiteSpec) preflightVar() string { return NginxIdent(spec.Name) + "_preflight" }

// anyOrigin says whether the site allows every origin, which needs no
// map: the answer is * whoever asks.
func (c *SiteCORS) anyOrigin() bool { return len(c.Origins) == 1 && c.Origins[0] == "*" }

func (spec *SiteSpec) cors() *SiteCORS {
	if spec.Headers == nil || spec.Kind == "redirect" {
		return nil
	}
	return spec.Headers.CORS
}

// headersWritten says whether the headers section adds any response header.
func (spec *SiteSpec) headersWritten() bool {
	h := spec.Headers
	return h != nil && (len(h.Response) > 0 || h.FrameOptions != "" || h.CSP != nil ||
		len(h.Permissions) > 0 || spec.cors() != nil)
}

func validateHeaders(spec *SiteSpec) error {
	h := spec.Headers
	if h == nil {
		return nil
	}
	if spec.Kind != "proxy" && (len(h.Request) > 0 || len(h.Hide) > 0) {
		return fmt.Errorf("request headers and hidden headers apply to a site that forwards to an application")
	}
	if len(h.Request)+len(h.Response)+len(h.Hide) > 64 {
		return fmt.Errorf("at most 64 request, response and hidden headers together")
	}
	seen := map[string]bool{}
	for _, header := range h.Request {
		if err := validHeaderName(header.Name); err != nil {
			return fmt.Errorf("request header: %w", err)
		}
		if containsFold(renderedRequestHeaders, header.Name) {
			return fmt.Errorf("the site already sets the %s request header", header.Name)
		}
		if seen[strings.ToLower(header.Name)] {
			return fmt.Errorf("the %s request header is listed twice", header.Name)
		}
		seen[strings.ToLower(header.Name)] = true
		if strings.Contains(header.Value, "$") {
			if !nginxVarRe.MatchString(header.Value) {
				return fmt.Errorf("the %s request header may be text or one nginx variable such as $remote_addr, not both", header.Name)
			}
		} else if !headerValueRe.MatchString(header.Value) {
			return fmt.Errorf("the %s request header may not contain quotes, backslashes, semicolons, braces or line breaks", header.Name)
		}
	}
	seen = map[string]bool{}
	for _, header := range h.Response {
		if err := validHeaderName(header.Name); err != nil {
			return fmt.Errorf("response header: %w", err)
		}
		if err := spec.reservedResponseHeader(header.Name); err != nil {
			return err
		}
		if seen[strings.ToLower(header.Name)] {
			return fmt.Errorf("the %s response header is listed twice", header.Name)
		}
		seen[strings.ToLower(header.Name)] = true
		if header.Value == "" {
			return fmt.Errorf("the %s response header needs a value", header.Name)
		}
		if !headerValueRe.MatchString(header.Value) {
			return fmt.Errorf("the %s response header may not contain quotes, backslashes, dollars, semicolons, braces or line breaks", header.Name)
		}
	}
	seen = map[string]bool{}
	for _, name := range h.Hide {
		if err := validHeaderName(name); err != nil {
			return fmt.Errorf("hidden header: %w", err)
		}
		if containsFold(nginxHiddenHeaders, name) {
			return fmt.Errorf("nginx already drops the application's %s header", name)
		}
		if seen[strings.ToLower(name)] {
			return fmt.Errorf("the %s header is hidden twice", name)
		}
		seen[strings.ToLower(name)] = true
	}
	switch h.FrameOptions {
	case "", "deny", "sameorigin":
	default:
		return fmt.Errorf("X-Frame-Options must be DENY or SAMEORIGIN")
	}
	if err := validateCSP(h.CSP); err != nil {
		return err
	}
	seen = map[string]bool{}
	for _, rule := range h.Permissions {
		if !policyNameRe.MatchString(rule.Feature) {
			return fmt.Errorf("%q is not a Permissions-Policy feature name", rule.Feature)
		}
		if seen[rule.Feature] {
			return fmt.Errorf("the %s permission is listed twice", rule.Feature)
		}
		seen[rule.Feature] = true
		switch rule.Allow {
		case "none", "self", "all":
		default:
			return fmt.Errorf("the %s permission must be allowed to nobody, the site or everyone", rule.Feature)
		}
	}
	return validateCORS(spec)
}

func validHeaderName(name string) error {
	if !headerNameRe.MatchString(name) {
		return fmt.Errorf("%q is not a header name; use letters, digits and dashes", name)
	}
	return nil
}

// reservedResponseHeader refuses a custom header the site already writes
// through a control of its own, which would send it twice.
func (spec *SiteSpec) reservedResponseHeader(name string) error {
	switch {
	case containsFold(policyHeaders, name):
		return fmt.Errorf("%s has a control of its own in the headers section", name)
	case strings.HasPrefix(strings.ToLower(name), "access-control-"):
		return fmt.Errorf("the CORS headers are written by the CORS settings")
	case spec.SecurityHeaders && (strings.EqualFold(name, "X-Content-Type-Options") || strings.EqualFold(name, "Referrer-Policy")):
		return fmt.Errorf("the security headers switch already sends %s", name)
	case spec.cachesStatic() && spec.StaticCache.Immutable && strings.EqualFold(name, "Cache-Control"):
		return fmt.Errorf("the static cache already sends Cache-Control")
	case spec.cachesProxy() && strings.EqualFold(name, "X-Cache-Status"):
		return fmt.Errorf("the proxy cache already sends X-Cache-Status")
	case strings.EqualFold(name, "Vary") && spec.cors() != nil && !spec.cors().anyOrigin():
		return fmt.Errorf("CORS already sends Vary: Origin")
	}
	return nil
}

func validateCSP(csp *SiteCSP) error {
	if csp == nil {
		return nil
	}
	if len(csp.Directives) == 0 {
		return fmt.Errorf("a Content-Security-Policy needs at least one directive")
	}
	if len(csp.Directives) > 40 {
		return fmt.Errorf("at most 40 Content-Security-Policy directives")
	}
	seen := map[string]bool{}
	for _, d := range csp.Directives {
		if !policyNameRe.MatchString(d.Name) {
			return fmt.Errorf("%q is not a Content-Security-Policy directive", d.Name)
		}
		if seen[d.Name] {
			return fmt.Errorf("the %s directive is listed twice; browsers use the first", d.Name)
		}
		seen[d.Name] = true
		if len(d.Sources) > 64 {
			return fmt.Errorf("at most 64 sources in %s", d.Name)
		}
		for _, source := range d.Sources {
			if !cspSourceRe.MatchString(source) {
				return fmt.Errorf("%q in %s is not a source; write keywords such as self without their quotes", source, d.Name)
			}
		}
	}
	return nil
}

func validateCORS(spec *SiteSpec) error {
	c := spec.Headers.CORS
	if c == nil {
		return nil
	}
	if spec.Kind == "redirect" {
		return fmt.Errorf("a redirect answers before CORS applies")
	}
	if len(c.Origins) == 0 {
		return fmt.Errorf("CORS needs at least one origin, or * for any")
	}
	if len(c.Origins) > 64 {
		return fmt.Errorf("at most 64 CORS origins")
	}
	seen := map[string]bool{}
	for _, origin := range c.Origins {
		if origin == "*" {
			if len(c.Origins) > 1 {
				return fmt.Errorf("* already allows every origin; list origins or use * alone")
			}
			if c.Credentials {
				// Browsers refuse a credentialed answer to *, and echoing any
				// origin instead would hand every site on the web the
				// visitor's session.
				return fmt.Errorf("credentials cannot be allowed from any origin; list the origins")
			}
			continue
		}
		if !corsOriginRe.MatchString(origin) {
			return fmt.Errorf("%q is not an origin; write it as https://app.example.com, with no path", origin)
		}
		if seen[strings.ToLower(origin)] {
			return fmt.Errorf("the origin %s is listed twice", origin)
		}
		seen[strings.ToLower(origin)] = true
	}
	if len(c.Methods) == 0 {
		return fmt.Errorf("CORS needs at least one allowed method")
	}
	for _, method := range c.Methods {
		if !slices.Contains(corsMethods, method) {
			return fmt.Errorf("%q is not a method CORS can allow", method)
		}
	}
	if len(c.Headers) > 64 {
		return fmt.Errorf("at most 64 CORS request headers")
	}
	for _, name := range c.Headers {
		if err := validHeaderName(name); err != nil {
			return fmt.Errorf("CORS header: %w", err)
		}
	}
	return nil
}

func headersWarnings(spec *SiteSpec) []string {
	if spec.cors() != nil && spec.BasicAuthFile != "" {
		return []string{
			"A CORS preflight is answered before the password is asked for, as browsers expect, but the requests after it still need the password.",
		}
	}
	return nil
}

// quoteHeader writes a header value in double quotes, which validation has
// kept free of anything that could end them.
func quoteHeader(value string) string { return `"` + value + `"` }

// renderCORSMaps writes the http-level half of CORS: which origins are
// answered, and which request is a preflight.
func renderCORSMaps(l *lines, spec *SiteSpec) {
	c := spec.cors()
	if !c.anyOrigin() {
		l.add("# CORS: these origins may call the site from a browser. Any other gets")
		l.add("# no Access-Control-Allow-Origin, which the browser takes as a refusal.")
		l.add("map $http_origin $%s {", spec.corsVar())
		l.add("    default \"\";")
		for _, origin := range c.Origins {
			l.add("    %s $http_origin;", quoteHeader(origin))
		}
		l.add("}")
	}
	l.add("# A preflight is the OPTIONS request a browser sends first to ask which")
	l.add("# method and headers it may use; any other OPTIONS reaches the application.")
	l.add("map \"$request_method:$http_access_control_request_method\" $%s {", spec.preflightVar())
	l.add("    default    0;")
	l.add("    \"~^OPTIONS:.\" 1;")
	l.add("}")
}

// renderSiteHeaders writes the headers section's response headers. It is
// part of renderHeaders, so a location with an add_header of its own
// repeats all of them.
func renderSiteHeaders(l *lines, spec *SiteSpec) {
	h := spec.Headers
	if h == nil {
		return
	}
	switch h.FrameOptions {
	case "deny":
		l.add("    add_header X-Frame-Options DENY always;")
	case "sameorigin":
		l.add("    add_header X-Frame-Options SAMEORIGIN always;")
	}
	if h.CSP != nil {
		name := "Content-Security-Policy"
		if h.CSP.ReportOnly {
			l.add("    # Report-only: browsers report what the policy would block and block nothing.")
			name = "Content-Security-Policy-Report-Only"
		}
		l.add("    add_header %s %s always;", name, quoteHeader(renderCSP(h.CSP)))
	}
	if len(h.Permissions) > 0 {
		l.add("    add_header Permissions-Policy %s always;", quoteHeader(renderPermissions(h.Permissions)))
	}
	if c := spec.cors(); c != nil {
		if c.anyOrigin() {
			l.add("    add_header Access-Control-Allow-Origin * always;")
		} else {
			l.add("    add_header Access-Control-Allow-Origin $%s always;", spec.corsVar())
			l.add("    # The answer depends on who asks, so a cache keeps one per origin.")
			l.add("    add_header Vary Origin always;")
		}
		if c.Credentials {
			l.add("    add_header Access-Control-Allow-Credentials true always;")
		}
		l.add("    add_header Access-Control-Allow-Methods %s always;", quoteHeader(strings.Join(c.Methods, ", ")))
		if len(c.Headers) > 0 {
			l.add("    add_header Access-Control-Allow-Headers %s always;", quoteHeader(strings.Join(c.Headers, ", ")))
		} else {
			l.add("    add_header Access-Control-Allow-Headers $http_access_control_request_headers always;")
		}
		l.add("    # Two hours, the most Chromium keeps a preflight's answer.")
		l.add("    add_header Access-Control-Max-Age 7200 always;")
	}
	for _, header := range h.Response {
		l.add("    add_header %s %s always;", header.Name, quoteHeader(header.Value))
	}
}

func renderCSP(csp *SiteCSP) string {
	parts := make([]string, 0, len(csp.Directives))
	for _, d := range csp.Directives {
		words := []string{d.Name}
		for _, source := range d.Sources {
			if cspQuoted(source) {
				source = "'" + source + "'"
			}
			words = append(words, source)
		}
		parts = append(parts, strings.Join(words, " "))
	}
	return strings.Join(parts, "; ")
}

func renderPermissions(rules []PermissionRule) string {
	parts := make([]string, 0, len(rules))
	for _, rule := range rules {
		switch rule.Allow {
		case "none":
			parts = append(parts, rule.Feature+"=()")
		case "self":
			parts = append(parts, rule.Feature+"=(self)")
		default:
			parts = append(parts, rule.Feature+"=*")
		}
	}
	return strings.Join(parts, ", ")
}

// renderServerHeaders writes the server-level request side: the
// application's headers hidden, and the CORS preflight answered.
func renderServerHeaders(l *lines, spec *SiteSpec) {
	h := spec.Headers
	if h == nil {
		return
	}
	wrote := false
	if spec.Kind == "proxy" {
		hide := append([]string{}, h.Hide...)
		if spec.cors() != nil {
			for _, name := range corsResponseHeaders {
				if !containsFold(hide, name) {
					hide = append(hide, name)
				}
			}
		}
		for _, name := range hide {
			l.add("    proxy_hide_header %s;", name)
			wrote = true
		}
	}
	if spec.cors() != nil {
		l.add("    # A CORS preflight is answered here with the headers above, before")
		l.add("    # the password and the limits, and never reaches the application.")
		l.add("    if ($%s) {", spec.preflightVar())
		l.add("        return 204;")
		l.add("    }")
		wrote = true
	}
	if wrote {
		l.blank()
	}
}

// renderRequestHeaders writes the site's own request headers into a
// location that forwards, after the ones every such location sets: a
// location's proxy_set_header replaces the server's whole list, so they
// cannot be said once above.
func renderRequestHeaders(l *lines, spec *SiteSpec) {
	if spec.Headers == nil {
		return
	}
	for _, header := range spec.Headers.Request {
		value := header.Value
		if !nginxVarRe.MatchString(value) {
			value = quoteHeader(value)
		}
		l.add("        proxy_set_header %s %s;", header.Name, value)
	}
}

// headersParse collects the headers section as the parser reads it.
type headersParse struct {
	h           SiteHeaders
	inCORS      bool
	origins     []string
	anyOrigin   bool
	corsHeader  bool
	credentials bool
	methods     []string
	allowHeader []string
	hide        []string
	vary        bool
}

var corsMapRe = regexp.MustCompile(`^\$http_origin\s+\$jd_\w+_cors\s*\{$`)

func (p *headersParse) object(name, value string) {
	if name == "map" && corsMapRe.MatchString(value) {
		p.inCORS = true
	}
}

func (p *headersParse) entry(raw string) {
	fields := strings.Fields(strings.TrimSuffix(raw, ";"))
	if p.inCORS && len(fields) == 2 && fields[0] != "default" {
		p.origins = append(p.origins, strings.Trim(fields[0], `"`))
	}
}

func (p *headersParse) closed() { p.inCORS = false }

// unquoteHeader reads a value the way nginx does when it is one quoted or
// bare word.
func unquoteHeader(value string) string {
	if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
		return value[1 : len(value)-1]
	}
	return value
}

// directive reads a headers directive. It says whether it was one.
// serverLevel is the server block itself; rootLocation the catch-all, whose
// request headers every forwarding location repeats.
func (p *headersParse) directive(name, value string, serverLevel, rootLocation bool) bool {
	switch {
	case name == "proxy_set_header" && rootLocation:
		header, v, _ := strings.Cut(value, " ")
		if containsFold(renderedRequestHeaders, header) {
			return false
		}
		p.h.Request = append(p.h.Request, HeaderValue{Name: header, Value: unquoteHeader(strings.TrimSpace(v))})
		return true
	case name == "proxy_hide_header" && serverLevel:
		p.hide = append(p.hide, value)
		return true
	case name != "add_header" || !serverLevel:
		return false
	}
	header, v, _ := strings.Cut(value, " ")
	v = strings.TrimSpace(v)
	v = strings.TrimSpace(strings.TrimSuffix(v, " always"))
	v = unquoteHeader(v)
	switch {
	case strings.EqualFold(header, "Strict-Transport-Security"), strings.EqualFold(header, "X-Content-Type-Options"):
		// The switches' own, read where they always were.
		return false
	case strings.EqualFold(header, "X-Frame-Options"):
		p.h.FrameOptions = strings.ToLower(v)
	case strings.EqualFold(header, "Content-Security-Policy"), strings.EqualFold(header, "Content-Security-Policy-Report-Only"):
		p.h.CSP = parseCSP(v)
		p.h.CSP.ReportOnly = strings.EqualFold(header, "Content-Security-Policy-Report-Only")
	case strings.EqualFold(header, "Permissions-Policy"):
		p.h.Permissions = parsePermissions(v)
	case strings.EqualFold(header, "Access-Control-Allow-Origin"):
		p.corsHeader = true
		p.anyOrigin = v == "*"
	case strings.EqualFold(header, "Access-Control-Allow-Credentials"):
		p.credentials = v == "true"
	case strings.EqualFold(header, "Access-Control-Allow-Methods"):
		p.methods = splitList(v)
	case strings.EqualFold(header, "Access-Control-Allow-Headers"):
		if !strings.HasPrefix(v, "$") {
			p.allowHeader = splitList(v)
		}
	case strings.EqualFold(header, "Access-Control-Max-Age"):
	case strings.EqualFold(header, "Vary") && v == "Origin":
		p.vary = true
	default:
		p.h.Response = append(p.h.Response, HeaderValue{Name: header, Value: v})
	}
	return true
}

func splitList(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func parseCSP(value string) *SiteCSP {
	csp := &SiteCSP{Directives: []CSPDirective{}}
	for _, part := range strings.Split(value, ";") {
		fields := strings.Fields(part)
		if len(fields) == 0 {
			continue
		}
		d := CSPDirective{Name: strings.ToLower(fields[0])}
		for _, source := range fields[1:] {
			if unquoted := strings.Trim(source, "'"); cspQuoted(unquoted) {
				source = unquoted
			}
			d.Sources = append(d.Sources, source)
		}
		csp.Directives = append(csp.Directives, d)
	}
	return csp
}

func parsePermissions(value string) []PermissionRule {
	var rules []PermissionRule
	for _, part := range splitList(value) {
		feature, allow, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		rule := PermissionRule{Feature: strings.TrimSpace(feature)}
		switch strings.TrimSpace(allow) {
		case "()":
			rule.Allow = "none"
		case "(self)":
			rule.Allow = "self"
		case "*":
			rule.Allow = "all"
		default:
			// An origin list, which the form's three choices cannot hold.
			continue
		}
		rules = append(rules, rule)
	}
	return rules
}

// settle fills the spec's headers from what was read, leaving out what the
// other switches write and what the form could not save back.
func (p *headersParse) settle(spec *SiteSpec) {
	h := p.h
	if p.corsHeader && (p.anyOrigin || len(p.origins) > 0) {
		methods := slices.DeleteFunc(p.methods, func(m string) bool { return !slices.Contains(corsMethods, m) })
		cors := &SiteCORS{Origins: p.origins, Methods: methods, Headers: p.allowHeader, Credentials: p.credentials}
		if p.anyOrigin {
			cors.Origins = []string{"*"}
		}
		h.CORS = cors
	}
	if p.vary && (h.CORS == nil || h.CORS.anyOrigin()) {
		h.Response = append(h.Response, HeaderValue{Name: "Vary", Value: "Origin"})
	}
	for _, name := range p.hide {
		if (h.CORS != nil && containsFold(corsResponseHeaders, name)) ||
			!headerNameRe.MatchString(name) || containsFold(nginxHiddenHeaders, name) {
			continue
		}
		h.Hide = append(h.Hide, name)
	}
	if spec.SecurityHeaders {
		// The switch writes SAMEORIGIN and its Referrer-Policy itself.
		if h.FrameOptions == "sameorigin" {
			h.FrameOptions = ""
		}
		h.Response = slices.DeleteFunc(h.Response, func(v HeaderValue) bool {
			return strings.EqualFold(v.Name, "Referrer-Policy") && v.Value == "strict-origin-when-cross-origin"
		})
	}
	if h.FrameOptions != "deny" && h.FrameOptions != "sameorigin" {
		h.FrameOptions = ""
	}
	// A header this form would refuse to save, such as one carrying a
	// variable, is left for the file rather than offered back broken.
	h.Request = slices.DeleteFunc(h.Request, func(v HeaderValue) bool {
		return !headerNameRe.MatchString(v.Name) ||
			(!nginxVarRe.MatchString(v.Value) && !headerValueRe.MatchString(v.Value))
	})
	h.Response = slices.DeleteFunc(h.Response, func(v HeaderValue) bool {
		return !headerNameRe.MatchString(v.Name) || v.Value == "" || !headerValueRe.MatchString(v.Value)
	})
	if spec.Kind != "proxy" {
		h.Request, h.Hide = nil, nil
	}
	h.Permissions = slices.DeleteFunc(h.Permissions, func(r PermissionRule) bool {
		return !policyNameRe.MatchString(r.Feature)
	})
	if h.CSP != nil && validateCSP(h.CSP) != nil {
		h.CSP = nil
	}
	if len(h.Request) == 0 && len(h.Response) == 0 && len(h.Hide) == 0 && h.FrameOptions == "" &&
		h.CSP == nil && len(h.Permissions) == 0 && h.CORS == nil {
		return
	}
	spec.Headers = &h
}
