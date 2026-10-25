package accountlink

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// DefaultSiteURL is the site this build links to unless JD_ACCOUNT_URL says
// otherwise.
const DefaultSiteURL = "https://just-dashboard.com"

// ParseSiteURL checks JD_ACCOUNT_URL and returns it as a bare origin. Only an
// origin is accepted: HTTPS everywhere, plain HTTP for a loopback host so a
// development site can run beside the dashboard, and no credentials, path,
// query or fragment that a request could be steered by.
func ParseSiteURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultSiteURL, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Hostname() == "" {
		return "", fmt.Errorf("%q is not a URL with a host", raw)
	}
	if u.User != nil {
		return "", errors.New("it must not carry a user name or password")
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", errors.New("it must be an origin only, with no path, query or fragment")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !loopbackHost(u.Hostname()) {
			return "", errors.New("plain http is accepted only for a loopback host such as localhost; use https")
		}
	default:
		return "", errors.New("it must start with https://")
	}
	return originOf(u), nil
}

// originOf spells an origin one way: lowercase host, no port that is the
// scheme's own. https://Just-Dashboard.com:443 and https://just-dashboard.com
// are the same place, and a comparison that took them for two would refuse the
// site's own approval address.
func originOf(u *url.URL) string {
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
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

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// sameOrigin reports whether a URL the site returned stays on the site's own
// origin, which is the only kind the dashboard offers a person to open.
func sameOrigin(origin, raw string) bool {
	want, err := url.Parse(origin)
	if err != nil || want.Host == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Host == "" {
		return false
	}
	return originOf(u) == originOf(want)
}
