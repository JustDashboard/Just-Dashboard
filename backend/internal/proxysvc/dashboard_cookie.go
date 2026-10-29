package proxysvc

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/sessioncookie"
)

// The dashboard's session cookie, kept from the applications the dashboard
// puts on its own name.
//
// A browser scopes a cookie to a host name and never to a port. The
// dashboard's cookie is __Host- prefixed (sessioncookie), so no other name
// is ever sent it — but every other port of the dashboard's own name is:
// the tailnet address of every pull request preview and tailnet-only
// deployment, which tailscale serve publishes on the node's MagicDNS name
// the dashboard is reached by, and a deployment route whose domain is the
// name the dashboard is served on. The application cannot use the cookie in
// a browser (the API wants X-JD-CSRF and a JSON body, and the cookie is
// SameSite=Strict), but its server would hold a bearer credential for this
// dashboard. So every proxy the dashboard writes in front of an application
// on its own name takes the dashboard's cookies out of the request and
// passes every other cookie through untouched: the tailnet front of every
// tailnet address (tailnet_gate.go), and a deployment route serving the
// dashboard's own name (DashboardHost).

// DashboardCookies are the cookie names those proxies take out: the
// dashboard's own, current and legacy.
func DashboardCookies() []string { return sessioncookie.Names() }

// cookieNameRe is a cookie name the renderers put in a regular expression
// unescaped: a token of letters, digits, underscore and hyphen.
var cookieNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func validCookieNames(names []string) error {
	if len(names) > 8 {
		return errors.New("a route drops at most eight cookies")
	}
	for _, name := range names {
		if !cookieNameRe.MatchString(name) {
			return fmt.Errorf("%q is not a cookie name a route can drop", name)
		}
	}
	return nil
}

// caddyDropCookies is what a reverse_proxy block says to forward the Cookie
// header without the named cookies: the first replacement takes out every
// name=value with the separator before it, the second the separator left at
// the start when the first cookie was one of them. Caddy applies each to
// every Cookie field of the request, so a header split in two is cleaned as
// well. A name inside another (my_jd_session) is not touched: a name is only
// matched at the start or after a separator, up to its =.
func caddyDropCookies(indent string, names []string) string {
	if len(names) == 0 {
		return ""
	}
	pattern := `(^|; *)(` + strings.Join(names, "|") + `)=[^;]*`
	return indent + "header_up Cookie " + quoteCaddy(pattern) + ` ""` + "\n" +
		indent + `header_up Cookie "^; *" ""` + "\n"
}

// quoteCaddy quotes a Caddyfile token that holds no quote or backslash, which
// a cookie pattern never does.
func quoteCaddy(token string) string { return `"` + token + `"` }

// nginxDropCookieMaps writes, at the top of a site, the maps that take the
// named cookies out of the visitor's Cookie header, one map a name, each
// reading the one before; it returns the variable the location sends as
// Cookie. nginx joins every Cookie field into $http_cookie, and a value left
// empty is not sent at all.
//
// Each map takes out one cookie of its name, the first: the name the
// dashboard issues over HTTPS is __Host- prefixed, which a browser keeps once
// per name (Path=/ and no Domain are required), so one is all there is. The
// legacy and plain names are the dashboard's own once each as well; a
// further copy under one of them, with a longer path, is one the
// application set itself. Caddy's replacement takes out every copy.
func nginxDropCookieMaps(l *lines, ident string, names []string) string {
	input := "$http_cookie"
	for index, name := range names {
		output := fmt.Sprintf("%s_ck%d", ident, index+1)
		if index == len(names)-1 {
			output = ident + "_cookie"
		}
		// PCRE2 limits a capture's name to 32 characters, too few for the
		// site's ident. The names are shared by every site's maps, which is
		// safe: a map's value is made the moment its own expression matched,
		// before any other map is read, and nginx lets a capture's variable
		// be defined by more than one expression.
		rest, head, tail := fmt.Sprintf("jd_ckr%d", index+1), fmt.Sprintf("jd_ckh%d", index+1), fmt.Sprintf("jd_ckt%d", index+1)
		l.add("map %s $%s {", input, output)
		l.add("    default %s;", input)
		l.add("    \"~^%s=[^;]*(?:; *)?(?<%s>.*)$\" $%s;", name, rest, rest)
		l.add("    \"~^(?<%s>.*?); *%s=[^;]*(?<%s>.*)$\" \"$%s$%s\";", head, name, tail, head, tail)
		l.add("}")
		input = "$" + output
	}
	return input
}

// WithDashboardHost names the host the dashboard itself is reached by, the
// installation's JD_SITE: a deployment route that serves it drops the
// dashboard's cookies. Called once, before the service is used.
func (s *Service) WithDashboardHost(host string) *Service {
	s.dashboardHost = DashboardHost(host)
	return s
}

// DashboardHost is a site address as a cookie's host: lower case, without a
// port, brackets or a trailing dot.
func DashboardHost(site string) string {
	host := strings.TrimSpace(site)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.Trim(host, "[]"), ".")
	return strings.ToLower(host)
}

// servesHost says whether a route's served names include host, by name or by
// a wildcard one label above it. A redirecting name serves no application.
func servesHost(route DeploymentRoute, host string) bool {
	if host == "" {
		return false
	}
	for _, name := range route.Domains {
		name = strings.ToLower(strings.TrimSuffix(name, "."))
		if name == host || strings.HasPrefix(name, "*.") && wildcardCertificateCovers(name, host) {
			return true
		}
	}
	return false
}

// onDashboardName is the route as this host's proxy writes it: one serving
// the dashboard's own name drops the dashboard's cookies, whatever port the
// dashboard is on. Activation and verification both render through it, so
// the two never disagree about the route's bytes.
func (s *Service) onDashboardName(route DeploymentRoute) DeploymentRoute {
	if !servesHost(route, s.dashboardHost) {
		return route
	}
	drop := append([]string(nil), route.DropCookies...)
	for _, name := range DashboardCookies() {
		if !containsFold(drop, name) {
			drop = append(drop, name)
		}
	}
	route.DropCookies = drop
	return route
}
