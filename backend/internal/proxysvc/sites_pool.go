package proxysvc

import (
	"fmt"
	"net"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// SitePool is a site's main upstream as several servers: an nginx upstream
// block the catch-all location forwards to. Health checking is passive, the
// only kind stock nginx has: a server is set aside for FailTimeout after
// MaxFails real requests to it fail within FailTimeout, and nothing probes it
// in between.
type SitePool struct {
	// Method is how a request picks a server: empty is round robin, and
	// least_conn, ip_hash, hash (the request URI, consistent) or random.
	Method string `json:"method,omitempty"`
	// Scheme is how the servers are spoken to: empty is http, or https.
	Scheme  string       `json:"scheme,omitempty"`
	Servers []PoolServer `json:"servers"`
	// Keepalive is how many idle connections each worker keeps open to the
	// servers; 0 opens one per request, nginx's default.
	Keepalive int `json:"keepalive,omitempty"`
	// RetryOn is when a request is passed to the next server, as
	// proxy_next_upstream spells it; empty is nginx's default, error and
	// timeout, and ["off"] never retries.
	RetryOn []string `json:"retryOn,omitempty"`
	// Tries caps the servers one request may try; 0 is no cap.
	Tries int `json:"tries,omitempty"`
}

// PoolServer is one server of a pool. Zero values are nginx's defaults:
// weight 1, one failure, ten seconds.
type PoolServer struct {
	Address     string `json:"address"`
	Weight      int    `json:"weight,omitempty"`
	MaxFails    int    `json:"maxFails,omitempty"`
	FailTimeout int    `json:"failTimeout,omitempty"`
	// Backup gets requests only while every other server is unavailable.
	Backup bool `json:"backup,omitempty"`
	// Down keeps the server in the file and sends it nothing.
	Down bool `json:"down,omitempty"`
}

// poolMethods are the balancing methods the form offers, as written.
var poolMethods = map[string]string{
	"least_conn": "least_conn;",
	"ip_hash":    "ip_hash;",
	"hash":       "hash $request_uri consistent;",
	"random":     "random;",
}

// retryConditions are the proxy_next_upstream conditions stock nginx reads.
var retryConditions = []string{
	"error", "timeout", "invalid_header", "http_500", "http_502", "http_503",
	"http_504", "http_403", "http_404", "http_429", "non_idempotent", "off",
}

const maxPoolServers = 64

func (spec *SiteSpec) poolName() string { return NginxIdent(spec.Name) + "_pool" }

// poolUpstream is the address the catch-all forwards to when the site has a
// pool: the upstream block's name behind the servers' scheme.
func (spec *SiteSpec) poolUpstream() string {
	scheme := "http"
	if spec.Pool.Scheme == "https" {
		scheme = "https"
	}
	return scheme + "://" + spec.poolName()
}

// mainUpstream is what the site's catch-all forwards to.
func (spec *SiteSpec) mainUpstream() string {
	if spec.Pool != nil {
		return spec.poolUpstream()
	}
	return spec.Upstream
}

func validatePool(spec *SiteSpec) error {
	pool := spec.Pool
	if pool == nil {
		return nil
	}
	if spec.Kind != "proxy" {
		return fmt.Errorf("only a site that forwards to an application has a pool of servers")
	}
	if spec.Upstream != "" {
		return fmt.Errorf("a site forwards to one upstream or to a pool of servers, not both")
	}
	if _, ok := poolMethods[pool.Method]; pool.Method != "" && !ok {
		return fmt.Errorf("the balancing method must be round robin, least connections, client IP, URI hash or random")
	}
	if pool.Scheme != "" && pool.Scheme != "http" && pool.Scheme != "https" {
		return fmt.Errorf("the pool's servers are spoken to over http or https")
	}
	if len(pool.Servers) == 0 {
		return fmt.Errorf("a pool needs at least one server")
	}
	if len(pool.Servers) > maxPoolServers {
		return fmt.Errorf("a pool may have at most %d servers", maxPoolServers)
	}
	primary := false
	for _, server := range pool.Servers {
		if err := validPoolAddress(server.Address); err != nil {
			return err
		}
		if server.Weight < 0 || server.Weight > 1000 {
			return fmt.Errorf("%s: weight must be between 1 and 1000", server.Address)
		}
		if server.MaxFails < 0 || server.MaxFails > 1000 {
			return fmt.Errorf("%s: max fails must be between 1 and 1000", server.Address)
		}
		if server.FailTimeout < 0 || server.FailTimeout > 3600 {
			return fmt.Errorf("%s: fail timeout must be between 1 and 3600 seconds", server.Address)
		}
		// nginx refuses the file: these methods pick a server from the
		// request, and a backup has no place in that choice.
		if server.Backup && (pool.Method == "hash" || pool.Method == "ip_hash" || pool.Method == "random") {
			return fmt.Errorf("%s: a backup server cannot be used with the client IP, URI hash or random method", server.Address)
		}
		primary = primary || !server.Backup
	}
	if !primary {
		return fmt.Errorf("a pool needs at least one server that is not a backup")
	}
	if pool.Keepalive < 0 || pool.Keepalive > 1024 {
		return fmt.Errorf("kept-open connections must be between 0 and 1024")
	}
	if pool.Tries < 0 || pool.Tries > 100 {
		return fmt.Errorf("tries must be between 0 and 100")
	}
	seen := map[string]bool{}
	for _, cond := range pool.RetryOn {
		if !slices.Contains(retryConditions, cond) {
			return fmt.Errorf("%q is not a condition nginx retries on", cond)
		}
		if seen[cond] {
			return fmt.Errorf("%s is listed twice in the retry conditions", cond)
		}
		seen[cond] = true
	}
	if seen["off"] && len(pool.RetryOn) > 1 {
		return fmt.Errorf("retrying off cannot be combined with conditions")
	}
	// $proxy_host is the block's name, which is nobody's host name.
	if spec.HostHeader == "upstream" {
		return fmt.Errorf("with a pool the upstream's own name is the pool's internal one — send the visitor's Host or a custom one")
	}
	if pool.Scheme == "https" && (spec.UpstreamSNI || spec.UpstreamVerify) && spec.UpstreamTLSName == "" {
		return fmt.Errorf("an HTTPS pool needs the TLS name its servers answer to, since the pool's own name is internal")
	}
	return nil
}

var poolSocketRe = regexp.MustCompile(`^unix:/[A-Za-z0-9._/-]{1,255}$`)

// validPoolAddress is a server as the upstream block's server line takes
// it: host, host:port, [IPv6]:port or unix:/path — no scheme, no path.
func validPoolAddress(addr string) error {
	if strings.HasPrefix(addr, "unix:") {
		if !poolSocketRe.MatchString(addr) {
			return fmt.Errorf("%q: a socket is unix: followed by an absolute path", addr)
		}
		return nil
	}
	if strings.Contains(addr, "://") {
		return fmt.Errorf("%q: give the server as host:port, without http:// — the pool's scheme applies to every server", addr)
	}
	host, port := addr, ""
	if h, p, err := net.SplitHostPort(addr); err == nil {
		host, port = h, p
	} else if strings.HasPrefix(addr, "[") && strings.HasSuffix(addr, "]") {
		host = addr[1 : len(addr)-1]
	} else if strings.Contains(addr, ":") {
		return fmt.Errorf("%q is not a server address; write an IPv6 address in brackets, like [::1]:3000", addr)
	}
	if port != "" {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("%q: the port is not valid", addr)
		}
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.To4() == nil && !strings.HasPrefix(addr, "[") {
			return fmt.Errorf("%q: write an IPv6 address in brackets, like [::1]:3000", addr)
		}
		return nil
	}
	if strings.HasPrefix(host, "*") || !domainRe.MatchString(host) {
		return fmt.Errorf("%q is not a server address, for example 127.0.0.1:3000", addr)
	}
	return nil
}

// renderPool writes the site's upstream block. keepalive comes after the
// method, which is where nginx requires it.
func renderPool(l *lines, spec *SiteSpec) {
	pool := spec.Pool
	l.add("# The servers the site's requests are shared between. Health checks are")
	l.add("# passive: a server whose requests fail is set aside for a while, and")
	l.add("# nothing probes it in between.")
	l.add("upstream %s {", spec.poolName())
	if pool.Method != "" {
		l.add("    %s", poolMethods[pool.Method])
	}
	for _, server := range pool.Servers {
		line := "    server " + server.Address
		if server.Weight > 0 {
			line += " weight=" + strconv.Itoa(server.Weight)
		}
		if server.MaxFails > 0 {
			line += " max_fails=" + strconv.Itoa(server.MaxFails)
		}
		if server.FailTimeout > 0 {
			line += " fail_timeout=" + strconv.Itoa(server.FailTimeout) + "s"
		}
		if server.Backup {
			line += " backup"
		}
		if server.Down {
			line += " down"
		}
		l.add("%s;", line)
	}
	if pool.Keepalive > 0 {
		l.add("    keepalive %d;", pool.Keepalive)
	}
	l.add("}")
}

// renderPoolLocation writes what a location forwarding to the pool needs
// beyond an ordinary proxy_pass.
func renderPoolLocation(l *lines, loc SiteLocation, spec *SiteSpec) {
	pool := spec.Pool
	if len(pool.RetryOn) > 0 {
		l.add("        proxy_next_upstream %s;", strings.Join(pool.RetryOn, " "))
	}
	if pool.Tries > 0 {
		l.add("        proxy_next_upstream_tries %d;", pool.Tries)
	}
	// nginx closes an upstream connection after each request unless the
	// Connection header it sends is cleared; with WebSockets the site's
	// upgrade map clears it instead.
	if pool.Keepalive > 0 && !loc.WebSockets {
		l.add("        proxy_set_header Connection \"\";")
	}
}

func poolWarnings(spec *SiteSpec) []string {
	pool := spec.Pool
	if pool == nil || spec.Kind != "proxy" {
		return nil
	}
	var warnings []string
	for _, server := range pool.Servers {
		if !strings.HasPrefix(server.Address, "unix:") && isPublicUpstream("http://"+server.Address) {
			warnings = append(warnings, fmt.Sprintf(
				"%s is not on this machine. That is fine for a gateway, and a mistake if you meant 127.0.0.1.", server.Address))
		}
	}
	if slices.Contains(pool.RetryOn, "non_idempotent") {
		warnings = append(warnings,
			"Retrying a POST, PUT or DELETE on the next server can carry it out twice when the first server had already acted on it.")
	}
	return warnings
}

// poolParse reads upstream blocks back, and the site's pool is whichever of
// them the catch-all forwards to.
type poolParse struct {
	pools map[string]*SitePool
	open  *SitePool
}

func newPoolParse() *poolParse { return &poolParse{pools: map[string]*SitePool{}} }

func (p *poolParse) object(name, value string) {
	if name != "upstream" || !strings.HasSuffix(value, "{") {
		return
	}
	id := strings.TrimSpace(strings.TrimSuffix(value, "{"))
	p.open = &SitePool{Servers: []PoolServer{}}
	p.pools[id] = p.open
}

// entry reads one statement directly inside an upstream block.
func (p *poolParse) entry(raw string) {
	if p.open == nil {
		return
	}
	name, value := cutDirective(raw)
	switch name {
	case "least_conn", "ip_hash":
		p.open.Method = name
	case "random":
		if value == "" {
			p.open.Method = name
		}
	case "hash":
		if strings.Join(strings.Fields(value), " ") == "$request_uri consistent" {
			p.open.Method = name
		}
	case "keepalive":
		p.open.Keepalive, _ = strconv.Atoi(value)
	case "server":
		fields := strings.Fields(value)
		if len(fields) == 0 {
			return
		}
		server := PoolServer{Address: fields[0]}
		for _, param := range fields[1:] {
			key, arg, _ := strings.Cut(param, "=")
			switch key {
			case "weight":
				server.Weight, _ = strconv.Atoi(arg)
			case "max_fails":
				server.MaxFails, _ = strconv.Atoi(arg)
			case "fail_timeout":
				server.FailTimeout = parseSeconds(arg)
			case "backup":
				server.Backup = true
			case "down":
				server.Down = true
			}
		}
		p.open.Servers = append(p.open.Servers, server)
	}
}

func (p *poolParse) closed() { p.open = nil }

// settle makes the catch-all's upstream the site's pool when it names one of
// the file's upstream blocks.
func (p *poolParse) settle(spec *SiteSpec, retryOn []string, tries int) {
	scheme, id, ok := strings.Cut(spec.Upstream, "://")
	if !ok || (scheme != "http" && scheme != "https") {
		return
	}
	pool := p.pools[id]
	if pool == nil {
		return
	}
	if scheme == "https" {
		pool.Scheme = "https"
	}
	pool.RetryOn, pool.Tries = retryOn, tries
	spec.Pool, spec.Upstream = pool, ""
}

// poolKeepsAlive says whether the site keeps connections to its pool open.
func (spec *SiteSpec) poolKeepsAlive() bool {
	return spec.Kind == "proxy" && spec.Pool != nil && spec.Pool.Keepalive > 0
}
