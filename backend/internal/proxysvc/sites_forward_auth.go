package proxysvc

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// Single sign-on in front of a site: nginx's auth_request asks an auth
// server about every request before it is answered. A 2xx lets it through,
// a 401 sends the visitor to sign in and back, and anything else — a 403, a
// 500, the auth server being down — refuses it, which is auth_request's own
// behaviour and the reason the site is never served when the check fails.

// SiteForwardAuth is the auth server a site asks.
type SiteForwardAuth struct {
	// Provider decides which of the auth server's response headers carry
	// the user's name and email: authelia, authentik, oauth2-proxy or
	// custom (Remote-User and Remote-Email).
	Provider string `json:"provider"`
	// Verify is the auth server's check endpoint, asked with the request's
	// headers and no body.
	Verify string `json:"verify"`
	// SignIn is the page a visitor without a session is sent to, with the
	// address they asked for as rd.
	SignIn string `json:"signIn"`
	// PathsOnly checks only the paths that say so, where otherwise every
	// path is checked except those that say not to.
	PathsOnly bool `json:"pathsOnly,omitempty"`
}

// forwardAuthURI is the internal location the check is made through.
const forwardAuthURI = "/__jd/auth"

// forwardAuthReturn is what the sign-in page is given to send the visitor
// back to.
const forwardAuthReturn = "rd=$scheme://$http_host$request_uri"

// forwardAuthMarker introduces the comment that names the provider, which
// is otherwise indistinguishable from custom when it uses Remote-User.
const forwardAuthMarker = "# Single sign-on through "

// forwardAuthSources are the response headers, as nginx variables, each
// provider sends the user's name and email in.
var forwardAuthSources = map[string][2]string{
	"authelia":     {"remote_user", "remote_email"},
	"authentik":    {"x_authentik_username", "x_authentik_email"},
	"oauth2-proxy": {"x_auth_request_user", "x_auth_request_email"},
	"custom":       {"remote_user", "remote_email"},
}

// forwardAuthHeaders are the request headers the application is told the
// user in. Set in every forwarding location, they replace whatever the
// visitor sent, and are left out where no check ran.
var forwardAuthHeaders = []string{"Remote-User", "Remote-Email"}

// A check or sign-in address is a URL written unquoted into the file.
var forwardAuthURLRe = regexp.MustCompile(`^https?://[A-Za-z0-9._~:/?&=%+-]+$`)

func (spec *SiteSpec) ssoUserVar() string  { return NginxIdent(spec.Name) + "_sso_user" }
func (spec *SiteSpec) ssoEmailVar() string { return NginxIdent(spec.Name) + "_sso_email" }

// ssoEverywhere says whether the server checks every path by default.
func (spec *SiteSpec) ssoEverywhere() bool {
	return spec.ForwardAuth != nil && !spec.ForwardAuth.PathsOnly
}

// ssoChecks says whether a path is checked.
func (spec *SiteSpec) ssoChecks(loc SiteLocation) bool {
	if spec.ForwardAuth == nil {
		return false
	}
	if spec.ForwardAuth.PathsOnly {
		return loc.ForwardAuth == "on"
	}
	return loc.ForwardAuth != "off"
}

func validForwardAuthURL(raw, what string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if !forwardAuthURLRe.MatchString(raw) || err != nil || u.Host == "" {
		return nil, fmt.Errorf("the %s must be an http:// or https:// address", what)
	}
	return u, nil
}

func validateForwardAuth(spec *SiteSpec) error {
	f := spec.ForwardAuth
	for _, loc := range spec.Locations {
		switch {
		case loc.ForwardAuth == "":
		case f == nil:
			return fmt.Errorf("%s: a path can only skip or require single sign-on the site has", loc.Path)
		case loc.ForwardAuth == "on" && !f.PathsOnly:
			return fmt.Errorf("%s: every path already signs in unless it says not to", loc.Path)
		case loc.ForwardAuth == "off" && f.PathsOnly:
			return fmt.Errorf("%s: only the paths that ask for it sign in already", loc.Path)
		case loc.ForwardAuth != "on" && loc.ForwardAuth != "off":
			return fmt.Errorf("%s: single sign-on on a path is on or off", loc.Path)
		}
	}
	if f == nil {
		return nil
	}
	if _, ok := forwardAuthSources[f.Provider]; !ok {
		return fmt.Errorf("single sign-on is through Authelia, Authentik, oauth2-proxy or a custom server")
	}
	if spec.Kind == "redirect" {
		// `return` is answered before the access phase auth_request is in.
		return fmt.Errorf("a redirect is answered before anyone could be asked to sign in")
	}
	verify, err := validForwardAuthURL(f.Verify, "check address")
	if err != nil {
		return err
	}
	if verify.RawQuery != "" || strings.Contains(f.Verify, "?") {
		return fmt.Errorf("the check address takes no query")
	}
	if _, err := validForwardAuthURL(f.SignIn, "sign-in address"); err != nil {
		return err
	}
	if strings.Contains(f.SignIn, "rd=") {
		return fmt.Errorf("the sign-in address is given rd= itself — leave it off")
	}
	// The visitor's browser would send its password to the auth server as
	// well, which reads an Authorization header as its own credentials; and
	// under satisfy any an address or a password would let a visitor past
	// the sign-in altogether.
	if spec.BasicAuthFile != "" || spec.AccessList != "" || spec.SatisfyAny {
		return fmt.Errorf("single sign-on replaces the site's password and access list — clear them")
	}
	if spec.InterceptErrors {
		return fmt.Errorf("with the application's own error responses replaced, its own 401s would be sent to sign in — turn that off")
	}
	if f.PathsOnly {
		if spec.Kind != "proxy" {
			return fmt.Errorf("only a proxy site has paths of its own to sign in on")
		}
		if !slices.ContainsFunc(spec.Locations, func(loc SiteLocation) bool { return loc.ForwardAuth == "on" }) {
			return fmt.Errorf("choose which paths sign in")
		}
	}
	for _, loc := range spec.Locations {
		if loc.Path == forwardAuthURI {
			return fmt.Errorf("%s is where the site asks the auth server", forwardAuthURI)
		}
		// The password prompt is a 401 as well, which error_page would turn
		// into the sign-in redirect on any path.
		if loc.BasicAuthFile != "" {
			return fmt.Errorf("%s: a site that signs in cannot also ask for a password on a path", loc.Path)
		}
	}
	if spec.Headers != nil {
		for _, header := range spec.Headers.Request {
			if containsFold(forwardAuthHeaders, header.Name) {
				return fmt.Errorf("the site already sends the signed-in user as %s", header.Name)
			}
		}
	}
	return nil
}

func forwardAuthWarnings(spec *SiteSpec) []string {
	f := spec.ForwardAuth
	if f == nil {
		return nil
	}
	var warnings []string
	if spec.Kind != "proxy" {
		warnings = append(warnings,
			"Visitors sign in, but only an application behind a proxy site is told who they are.")
	}
	if u, err := url.Parse(f.SignIn); err == nil && slices.Contains(spec.Domains, u.Hostname()) {
		skipped := slices.ContainsFunc(spec.Locations, func(loc SiteLocation) bool { return !spec.ssoChecks(loc) })
		if !f.PathsOnly && !skipped {
			warnings = append(warnings,
				"The sign-in page is on this site and every path signs in, so a visitor is sent round in a loop. Add its path with single sign-on off.")
		}
	}
	return warnings
}

// renderForwardAuth writes the server's side of the check: whether every
// path is checked, the user the check answered with, and where a 401 goes.
func renderForwardAuth(l *lines, spec *SiteSpec) {
	f := spec.ForwardAuth
	if f == nil {
		return
	}
	sources := forwardAuthSources[f.Provider]
	l.add("    %s%s.", forwardAuthMarker, f.Provider)
	l.add("    # A 2xx from it lets the request through and a 401 sends the")
	l.add("    # visitor to sign in; anything else, an error or no answer included,")
	l.add("    # refuses the request.")
	if !f.PathsOnly {
		l.add("    auth_request %s;", forwardAuthURI)
	}
	l.add("    auth_request_set $%s $upstream_http_%s;", spec.ssoUserVar(), sources[0])
	l.add("    auth_request_set $%s $upstream_http_%s;", spec.ssoEmailVar(), sources[1])
	sep := "?"
	if strings.Contains(f.SignIn, "?") {
		sep = "&"
	}
	l.add("    error_page 401 =302 %s%s%s;", f.SignIn, sep, forwardAuthReturn)
	l.blank()
}

// renderForwardAuthLocation is where the check is sent. Internal, so a
// visitor cannot ask it for themselves; a subrequest skips the access
// phase, so the check is not itself checked.
func renderForwardAuthLocation(l *lines, spec *SiteSpec) {
	f := spec.ForwardAuth
	if f == nil {
		return
	}
	l.add("    location = %s {", forwardAuthURI)
	l.add("        internal;")
	l.add("        proxy_pass %s;", f.Verify)
	l.add("        proxy_pass_request_body off;")
	l.add("        proxy_set_header Content-Length \"\";")
	l.add("        proxy_set_header X-Original-URL     $scheme://$http_host$request_uri;")
	l.add("        proxy_set_header X-Original-Method  $request_method;")
	l.add("        proxy_set_header X-Forwarded-Method $request_method;")
	l.add("        proxy_set_header X-Forwarded-Proto  $scheme;")
	l.add("        proxy_set_header X-Forwarded-Host   $http_host;")
	l.add("        proxy_set_header X-Forwarded-Uri    $request_uri;")
	l.add("        proxy_set_header X-Forwarded-For    $proxy_add_x_forwarded_for;")
	if strings.HasPrefix(f.Verify, "https://") {
		l.add("        proxy_ssl_server_name on;")
	}
	if spec.cachesProxy() {
		// A stored yes would let the next visitor to the same address in.
		l.add("        proxy_cache off;")
	}
	l.add("    }")
}

// renderLocationForwardAuth writes a path's own choice.
func renderLocationForwardAuth(l *lines, loc SiteLocation, spec *SiteSpec) {
	switch {
	case spec.ForwardAuth == nil:
	case loc.ForwardAuth == "on":
		l.add("        auth_request %s;", forwardAuthURI)
	case loc.ForwardAuth == "off":
		l.add("        # This path is answered without signing in.")
		l.add("        auth_request off;")
	}
}

// renderForwardAuthHeaders tells the application who signed in.
func renderForwardAuthHeaders(l *lines, spec *SiteSpec) {
	if spec.ForwardAuth == nil {
		return
	}
	l.add("        proxy_set_header Remote-User  $%s;", spec.ssoUserVar())
	l.add("        proxy_set_header Remote-Email $%s;", spec.ssoEmailVar())
}

// forwardAuthParse collects single sign-on as the parser reads it.
type forwardAuthParse struct {
	provider   string
	userSource string
	verify     string
	signIn     string
	everywhere bool
}

// comment reads the provider's marker.
func (p *forwardAuthParse) comment(raw string) bool {
	provider, ok := strings.CutPrefix(raw, forwardAuthMarker)
	if !ok {
		return false
	}
	provider = strings.TrimSuffix(provider, ".")
	if _, known := forwardAuthSources[provider]; known {
		p.provider = provider
	}
	return true
}

// directive reads a single sign-on directive. It says whether it was one.
func (p *forwardAuthParse) directive(spec *SiteSpec, name, value, location string, current *SiteLocation) bool {
	if location == forwardAuthURI {
		if name == "proxy_pass" {
			p.verify = value
		}
		return true
	}
	switch name {
	case "auth_request":
		switch {
		case current != nil && value == forwardAuthURI:
			current.ForwardAuth = "on"
		case current != nil && value == "off":
			current.ForwardAuth = "off"
		case location == "" && value == forwardAuthURI:
			p.everywhere = true
		}
		// Elsewhere it is the renderer's own off, on the ACME challenge and
		// the pages.
		return true
	case "auth_request_set":
		fields := strings.Fields(value)
		if len(fields) == 2 && fields[0] == "$"+spec.ssoUserVar() {
			p.userSource = strings.TrimPrefix(fields[1], "$upstream_http_")
			return true
		}
		return len(fields) == 2 && fields[0] == "$"+spec.ssoEmailVar()
	case "error_page":
		target, ok := strings.CutPrefix(value, "401 =302 ")
		if !ok || location != "" {
			return false
		}
		signIn, ok := strings.CutSuffix(target, forwardAuthReturn)
		if !ok {
			return false
		}
		p.signIn = signIn[:len(signIn)-1]
		return true
	case "proxy_set_header":
		header, v, _ := strings.Cut(value, " ")
		v = strings.TrimSpace(v)
		return containsFold(forwardAuthHeaders, header) &&
			(v == "$"+spec.ssoUserVar() || v == "$"+spec.ssoEmailVar())
	}
	return false
}

func (p *forwardAuthParse) settle(spec *SiteSpec) {
	if p.verify == "" || p.signIn == "" {
		for i := range spec.Locations {
			spec.Locations[i].ForwardAuth = ""
		}
		return
	}
	provider := p.provider
	if provider == "" {
		provider = "custom"
		for name, sources := range forwardAuthSources {
			if name != "authelia" && sources[0] == p.userSource {
				provider = name
			}
		}
	}
	spec.ForwardAuth = &SiteForwardAuth{
		Provider:  provider,
		Verify:    p.verify,
		SignIn:    p.signIn,
		PathsOnly: !p.everywhere,
	}
	// A path that says what the site already does says nothing.
	for i := range spec.Locations {
		loc := &spec.Locations[i]
		if (p.everywhere && loc.ForwardAuth == "on") || (!p.everywhere && loc.ForwardAuth == "off") {
			loc.ForwardAuth = ""
		}
	}
}
