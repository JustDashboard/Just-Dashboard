package proxysvc

import (
	"net"
	"strconv"
	"strings"
)

// ListenerRoute is a proxy site forwarding to a socket.
type ListenerRoute struct {
	Site string `json:"site"`
	// ServerName is the site's first name, empty for a catch-all.
	ServerName string `json:"serverName,omitempty"`
	TLS        bool   `json:"tls"`
}

// SitesForUpstreamPort is the enabled sites forwarding to port on this
// machine through a socket bound to bound ("" or a wildcard for any address).
//
// An upstream may be written as localhost:3000, 127.0.0.1:3000, [::1]:3000
// or one of the host's own addresses, and all of them reach a wildcard
// socket; a loopback socket answers only the loopback spellings, and a socket
// on one address only that address. A name other than localhost is a
// container or another machine, which a host port does not answer for.
func SitesForUpstreamPort(vhosts []VHost, port int, bound string) []VHost {
	own := ownAddresses()
	out := []VHost{}
	for _, v := range vhosts {
		if !v.Enabled {
			continue
		}
		for _, up := range v.Upstreams {
			if upstreamReaches(up, port, bound, own) {
				out = append(out, v)
				break
			}
		}
	}
	return out
}

// upstreamReaches reads one proxy_pass or reverse_proxy value, which for
// Caddy may name several targets.
func upstreamReaches(upstream string, port int, bound string, own map[string]bool) bool {
	for _, target := range strings.Fields(upstream) {
		host, p, ok := upstreamHostPort(target)
		if !ok || p != port {
			continue
		}
		if answersFor(host, bound, own) {
			return true
		}
	}
	return false
}

// upstreamHostPort takes the host and port out of http://127.0.0.1:3000/api,
// localhost:3000 or :3000. A target without a port — an upstream block's
// name, a scheme's default — names no port to match.
func upstreamHostPort(target string) (string, int, bool) {
	if i := strings.Index(target, "://"); i >= 0 {
		target = target[i+3:]
	}
	if i := strings.IndexAny(target, "/?"); i >= 0 {
		target = target[:i]
	}
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return "", 0, false
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return "", 0, false
	}
	return host, n, true
}

// answersFor says whether a connection to host lands on a socket bound to
// bound. Connecting to 0.0.0.0 or :: reaches loopback on Linux.
func answersFor(host, bound string, own map[string]bool) bool {
	loopback := host == "" || strings.EqualFold(host, "localhost")
	ip := net.ParseIP(host)
	if ip != nil && (ip.IsLoopback() || ip.IsUnspecified()) {
		loopback = true
	}
	switch {
	case bound == "" || isWildcard(bound):
		return loopback || (ip != nil && own[ip.String()])
	case bindScope(bound) == ScopeLoopback:
		return loopback
	default:
		b := net.ParseIP(bound)
		return ip != nil && b != nil && ip.Equal(b)
	}
}

// ownAddresses is the host's interface addresses, so an upstream written as
// the machine's LAN or Docker bridge address still counts as this machine.
func ownAddresses() map[string]bool {
	out := map[string]bool{}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return out
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok {
			out[n.IP.String()] = true
		}
	}
	return out
}

// AttachProxy says what the proxy does with each socket: the sites
// forwarding to it, the stream nginx listens on it for, and on nginx's own
// sockets how many sites it serves there. A stream is named only on a
// socket nginx holds with the stream directory included, so a stream file
// nginx never reads is not credited with a port something else took.
func AttachProxy(listeners []Listener, vhosts []VHost, streams *StreamStatus) {
	for i := range listeners {
		l := &listeners[i]
		if l.Protocol == "tcp" {
			for _, v := range SitesForUpstreamPort(vhosts, int(l.Port), l.Address) {
				route := ListenerRoute{Site: v.Name, TLS: v.TLS}
				if len(v.ServerNames) > 0 && v.ServerNames[0] != "_" {
					route.ServerName = v.ServerNames[0]
				}
				l.Routes = append(l.Routes, route)
			}
		}
		if l.Process != "nginx" {
			continue
		}
		if streams != nil && streams.Included {
			for _, s := range streams.Streams {
				if s.Listen == int(l.Port) && s.Protocol == l.Protocol {
					l.Stream = s.Name
					break
				}
			}
		}
		if l.Protocol == "tcp" && l.Stream == "" {
			l.ServedSites = nginxSitesOn(vhosts, int(l.Port))
		}
	}
}

// nginxSitesOn counts the enabled nginx sites listening on port. A server
// block with no listen directive listens on 80.
func nginxSitesOn(vhosts []VHost, port int) int {
	n := 0
	for _, v := range vhosts {
		if !v.Enabled || v.Kind != KindNginx {
			continue
		}
		if len(v.Listen) == 0 && port == 80 {
			n++
			continue
		}
		for _, listen := range v.Listen {
			if nginxListenPort(listen) == port {
				n++
				break
			}
		}
	}
	return n
}

// nginxListenPort reads 443 from "443 ssl", "[::]:443 ssl" or
// "127.0.0.1:443"; a bare address listens on 80.
func nginxListenPort(listen string) int {
	fields := strings.Fields(listen)
	if len(fields) == 0 {
		return 0
	}
	value := fields[0]
	if strings.HasPrefix(value, "unix:") {
		return 0
	}
	if i := strings.LastIndex(value, ":"); i >= 0 && !strings.HasSuffix(value, "]") {
		value = value[i+1:]
	}
	if n, err := strconv.Atoi(value); err == nil {
		return n
	}
	return 80
}
