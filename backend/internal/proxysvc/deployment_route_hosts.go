package proxysvc

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

// HostUpstream sends a route's requests for some of its names to a server of
// their own rather than the route's: a service domain of a Compose stack, its
// Seerr at an address beside the Jellyfin the rest of the route reaches.
// Every rule of the route — TLS and the plain names, HSTS, the password or
// the sign-in, the allowed addresses, the edge rules and the rate limit,
// CrowdSec, maintenance and the error pages, the request record and the
// trace, the dropped cookies, the body limit and the response headers —
// applies to these names exactly as to the others; only the server a
// request is passed to differs.
type HostUpstream struct {
	Hostnames []string `json:"hostnames"`
	Upstream  string   `json:"upstream"`
}

// MaxHostUpstreams is the most servers a route sends some of its names to:
// a Compose template has at most eight services, and a stack's own file a
// few more.
const MaxHostUpstreams = 16

// validateHostUpstreams holds a route's host upstreams to what both
// renderers write: names the route serves, each sent to one server, and a
// server spelled as the route's own are. A route that picks between releases
// — a canary, skew protection's cookie, or the wake of one that sleeps —
// decides per request which release answers, and a name answered by another
// service has no release to pick, so neither is written beside them.
func validateHostUpstreams(route DeploymentRoute) error {
	if len(route.HostUpstreams) == 0 {
		return nil
	}
	if len(route.HostUpstreams) > MaxHostUpstreams {
		return fmt.Errorf("a route sends its names to at most %d services of their own", MaxHostUpstreams)
	}
	switch {
	case len(route.Servers()) == 0:
		return errors.New("a route sends names to other services only beside a release of its own")
	case route.Canary != nil:
		return errors.New("a canary shares the route between two releases, and a name answered by another service has no release to share")
	case route.Skew != nil:
		return errors.New("skew protection keeps visitors on a release, and a name answered by another service has no release to keep them on")
	case route.Sleep != nil:
		return errors.New("a route that wakes its application cannot also send names to other services, which are not woken")
	}
	seen := map[string]bool{}
	// One line per server, in the route and in its record of the
	// containers behind it: a service's server is none of the release's,
	// and two entries for one server are one entry with both names.
	servers := map[string]bool{}
	for _, server := range route.Servers() {
		servers[server] = true
	}
	for _, entry := range route.HostUpstreams {
		if len(entry.Hostnames) == 0 {
			return errors.New("a service's server needs at least one of the route's names")
		}
		if err := validHostUpstreamAddress(entry.Upstream); err != nil {
			return err
		}
		if servers[entry.Upstream] {
			return fmt.Errorf("%s is named twice among the route's servers", entry.Upstream)
		}
		servers[entry.Upstream] = true
		for _, name := range entry.Hostnames {
			lower := strings.ToLower(name)
			switch {
			case strings.HasPrefix(lower, "*."):
				return fmt.Errorf("%s is a wildcard, and a service is sent the names it serves one by one", name)
			case !containsFold(route.Domains, name):
				return fmt.Errorf("%s is not a name the route serves, so it cannot be sent to a service", name)
			case seen[lower]:
				return fmt.Errorf("%s is sent to two services", name)
			}
			seen[lower] = true
		}
	}
	return nil
}

// validHostUpstreamAddress is the shape both renderers write: plain HTTP to
// an address and a port, nothing after them.
func validHostUpstreamAddress(upstream string) error {
	parsed, err := url.Parse(upstream)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" ||
		parsed.Fragment != "" || net.ParseIP(parsed.Hostname()) == nil || parsed.Port() == "" {
		return fmt.Errorf("a service's server is http://<address>:<port>, not %q", upstream)
	}
	return nil
}

// requirePrivateHostUpstreams is the rule a route's own server is held to
// where it is written: an application's publication on this host's
// loopback, which the dashboard makes. Caddy finds the container behind it
// (findTarget); nginx forwards to it as it is.
func requirePrivateHostUpstreams(route DeploymentRoute) error {
	for _, entry := range route.HostUpstreams {
		parsed, err := url.Parse(entry.Upstream)
		if err != nil {
			return err
		}
		if ip := net.ParseIP(parsed.Hostname()); ip == nil || !ip.IsLoopback() {
			return fmt.Errorf("a service's server is its publication on this host's loopback, not %s", entry.Upstream)
		}
	}
	return nil
}

// hostUpstreamFor is the server a name is sent to, if it is one a host
// upstream names.
func (route DeploymentRoute) hostUpstreamFor(name string) (string, bool) {
	for _, entry := range route.HostUpstreams {
		if containsFold(entry.Hostnames, name) {
			return entry.Upstream, true
		}
	}
	return "", false
}

// siteHostUpstream is one name a deployment route's nginx site sends to a
// server of its own, as the map writes it: the address without its scheme.
type siteHostUpstream struct {
	Host    string
	Address string
}

// siteHostUpstreams are the route's host upstreams as the nginx site writes
// them, one per name, in the order the route's Domains list the names.
func siteHostUpstreams(route DeploymentRoute) []siteHostUpstream {
	var out []siteHostUpstream
	for _, name := range route.Domains {
		if upstream, ok := route.hostUpstreamFor(name); ok {
			out = append(out, siteHostUpstream{Host: strings.ToLower(name), Address: strings.TrimPrefix(upstream, "http://")})
		}
	}
	return out
}

// hostUpstreamVar is the variable the site's catch-all forwards through
// while some of its names go to other services.
func (spec *SiteSpec) hostUpstreamVar() string { return spec.varIdent() + "_host_up" }

// validateSiteHostUpstreams refuses a map line a renderer would write wrong.
// The names are the spec's own, which ValidateSpec checks as domains, and
// each address is a pool server's, so nothing written into the map can end
// a directive early.
func validateSiteHostUpstreams(spec *SiteSpec) error {
	for _, entry := range spec.hostUpstreams {
		if !containsFold(spec.Domains, entry.Host) {
			return fmt.Errorf("%s is not one of the site's names", entry.Host)
		}
		if err := validPoolAddress(entry.Address); err != nil {
			return err
		}
	}
	if len(spec.hostUpstreams) > 0 && (spec.Kind != "proxy" || spec.Split != nil || spec.sleep != nil) {
		return errors.New("only a site forwarding to one release sends some of its names to other servers")
	}
	return nil
}

// hostMapDefault is what the map gives every name not sent elsewhere: the
// site's own server, as proxy_pass reads it after http://.
func (spec *SiteSpec) hostMapDefault() string {
	if spec.Pool != nil {
		return spec.poolName()
	}
	return strings.TrimPrefix(spec.Upstream, "http://")
}

// renderHostUpstreamMap picks the server by the name a request asked for.
// $host is the request's name in lower case, without its port, and a map
// compares strings without case, so each service domain is one line.
func renderHostUpstreamMap(l *lines, spec *SiteSpec) {
	l.add("# A service domain is answered by its own Compose service; every other")
	l.add("# name by the release. The rest of the site applies to both alike.")
	l.add("map $host $%s {", spec.hostUpstreamVar())
	width := len("default")
	for _, entry := range spec.hostUpstreams {
		width = max(width, len(entry.Host))
	}
	l.add("    %-*s %s;", width, "default", spec.hostMapDefault())
	for _, entry := range spec.hostUpstreams {
		l.add("    %-*s %s;", width, entry.Host, entry.Address)
	}
	l.add("}")
}

// hostMapLineRe reads one line of a host upstream map back, for the
// inventory: the name and the address it is sent to.
var hostMapLineRe = regexp.MustCompile(`^\s+(\S+)\s+([0-9A-Fa-f.:\[\]]+:\d+);$`)

// hostMapOpenRe opens a deployment route's host upstream map.
var hostMapOpenRe = regexp.MustCompile(`^map \$host \$jd_\w+_host_up \{$`)

// hostMapUpstreams are the servers a site file's host upstream map sends
// names to, the site's own default among them, as http:// addresses.
func hostMapUpstreams(text string) []string {
	var out []string
	inside := false
	for _, line := range strings.Split(text, "\n") {
		switch {
		case hostMapOpenRe.MatchString(line):
			inside = true
		case inside && strings.TrimSpace(line) == "}":
			inside = false
		case inside:
			if m := hostMapLineRe.FindStringSubmatch(line); m != nil {
				out = append(out, "http://"+m[2])
			}
		}
	}
	return out
}
