// Package sessioncookie names the dashboard's session cookie, for the two
// halves of the code that have to agree on it: the authenticator that sets
// and reads it, and the proxies the dashboard writes in front of other
// people's applications, which take it out of what those applications are
// sent.
//
// A browser scopes a cookie to a host name and never to a port, so the
// cookie of a dashboard at https://box.tailnet.ts.net:8443 is also sent to
// every other port of that name — the pull request previews and tailnet-only
// deployments tailscale serve publishes there, and a deployment route on the
// dashboard's own name. The prefix keeps the cookie off every other name;
// the proxies keep it from the other ports.
package sessioncookie

const (
	// Name is the cookie on a dashboard served over HTTPS. The __Host-
	// prefix is the browser's promise that it was set Secure, with Path=/
	// and no Domain, by this very host: a sibling name cannot plant one, and
	// none is sent to any other name.
	Name = "__Host-jd_session"
	// PlainName is the cookie on a dashboard served over plain HTTP on
	// loopback (JD_TLS=off, JD_DEV), where a browser refuses a __Host-
	// cookie because it cannot be Secure there.
	PlainName = "jd_session"
	// LegacyName is the name releases before 0.8.2 set. A session still
	// carrying it is read once, given Name (or PlainName) and the old cookie
	// cleared, so an upgrade signs nobody out.
	LegacyName = "vpsd_session"
)

// For is the name a dashboard issues: Name where its cookie is Secure,
// PlainName where it cannot be.
func For(secure bool) string {
	if secure {
		return Name
	}
	return PlainName
}

// Names is every name a dashboard has put in a browser, current and
// legacy: what a proxy in front of an application on the dashboard's own
// name takes out of the request.
func Names() []string {
	return []string{Name, PlainName, LegacyName}
}
